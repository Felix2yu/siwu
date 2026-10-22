// Package probe 支撑可视化框选：同源预览会话、选择器评估、重复块泛化、字段推断。
package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Felix2yu/siwu/internal/extract"
	"github.com/Felix2yu/siwu/internal/fetch"
	"github.com/Felix2yu/siwu/internal/model"
	"golang.org/x/net/html"
)

// Session 是一次预览抓取的结果。
type Session struct {
	ID        string
	URL       string
	Title     string
	HTML      []byte
	FetchedAt time.Time
}

// Probe 持有预览会话。
type Probe struct {
	fetcher  *fetch.Fetcher
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
}

// New 构造 Probe。
func New(f *fetch.Fetcher) *Probe {
	p := &Probe{fetcher: f, sessions: map[string]*Session{}, ttl: 15 * time.Minute}
	go p.gc()
	return p
}

func (p *Probe) gc() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		p.mu.Lock()
		for id, s := range p.sessions {
			if time.Since(s.FetchedAt) > p.ttl {
				delete(p.sessions, id)
			}
		}
		p.mu.Unlock()
	}
}

// Fetch 抓取并生成预览会话。
func (p *Probe) Fetch(ctx context.Context, rawURL string, engine model.Engine, req model.Request) (*Session, error) {
	src := model.Source{URL: rawURL, Engine: engine, Request: req}
	src.Defaults()
	resp, err := p.fetcher.Page(ctx, src)
	if err != nil {
		return nil, err
	}
	body := resp.Body
	if strings.Contains(strings.ToLower(resp.ContentType), "json") {
		// JSON 直接包进 <pre>，前端仍可看结构，但不能框选
		body = []byte("<html><head><meta charset=\"utf-8\">" +
			"<style>body{margin:0;background:#111;color:#ddd;font:12px/1.6 ui-monospace,monospace}" +
			"pre{padding:16px;white-space:pre-wrap;word-break:break-all}</style></head><body><pre>" +
			html.EscapeString(string(resp.Body)) + "</pre></body></html>")
	}
	safe, err := sanitize(body, firstNonEmpty(resp.FinalURL, rawURL))
	if err != nil {
		return nil, err
	}
	id := randomID()
	sess := &Session{ID: id, URL: firstNonEmpty(resp.FinalURL, rawURL), Title: extract.TitleOf(body), HTML: safe, FetchedAt: time.Now()}
	p.mu.Lock()
	if len(p.sessions) > 24 {
		for k := range p.sessions {
			delete(p.sessions, k)
			break
		}
	}
	p.sessions[id] = sess
	p.mu.Unlock()
	return sess, nil
}

// Get 取会话。
func (p *Probe) Get(id string) (*Session, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	return s, ok
}

// Extract 在预览 DOM 上跑一次抽取（与正式运行同一实现）。
func (p *Probe) Extract(id string, spec model.Extract, rules []model.Rule) (extract.Result, error) {
	s, ok := p.Get(id)
	if !ok {
		return extract.Result{}, fmt.Errorf("预览会话已过期，请重新抓取页面")
	}
	return extract.HTML(s.HTML, s.URL, spec, rules)
}

// Count 统计选择器命中数，并给前几个节点的文本作为样例。
func (p *Probe) Count(id, selector string) (int, []string, error) {
	s, ok := p.Get(id)
	if !ok {
		return 0, nil, fmt.Errorf("预览会话已过期")
	}
	doc, err := html.Parse(bytes.NewReader(s.HTML))
	if err != nil {
		return 0, nil, err
	}
	sel, err := extract.Parse(selector)
	if err != nil {
		return 0, nil, err
	}
	nodes, err := extract.Select(doc, sel)
	if err != nil {
		return 0, nil, err
	}
	var samples []string
	for i, n := range nodes {
		if i >= 5 {
			break
		}
		samples = append(samples, truncate(extract.NodeValue(n, ""), 80))
	}
	return len(nodes), samples, nil
}

// Suggestion 是框选后给出的一组候选选择器。
type Suggestion struct {
	Candidates []Candidate  `json:"candidates"`
	Fields     []FieldGuess `json:"fields"`
	Tag        string       `json:"tag"`
	Text       string       `json:"text"`
}

// Candidate 是一个候选条目选择器。
type Candidate struct {
	Kind  string `json:"kind"` // item | exact
	Label string `json:"label"`
	XPath string `json:"xpath"`
	CSS   string `json:"css"`
	Count int    `json:"count"`
}

// FieldGuess 是字段推断结果。
type FieldGuess struct {
	Name string `json:"name"`
	Sel  string `json:"sel"`
	Attr string `json:"attr"`
	Text string `json:"text"`
}

// Suggest 依据点选的节点给出候选：精确路径 + 重复块泛化 + 字段推断。
func (p *Probe) Suggest(id, xpathExpr, itemSelector string) (Suggestion, error) {
	s, ok := p.Get(id)
	if !ok {
		return Suggestion{}, fmt.Errorf("预览会话已过期")
	}
	doc, err := html.Parse(bytes.NewReader(s.HTML))
	if err != nil {
		return Suggestion{}, err
	}
	nodes, err := extract.Select(doc, mustXPath(xpathExpr))
	if err != nil || len(nodes) == 0 {
		return Suggestion{}, fmt.Errorf("选择器没有命中节点")
	}
	node := nodes[0]
	out := Suggestion{Tag: node.Data, Text: truncate(extract.NodeValue(node, ""), 120)}

	// 字段推断的根：优先用「条目选择器命中的第一个节点」。
	// 否则用户点的是标题链接，推断出来的字段会以 <a> 为根（sel="."），
	// 一旦把条目换成父级 li，这些字段就全错了。
	inferRoot := node
	if strings.TrimSpace(itemSelector) != "" {
		if sel, err := extract.Parse(itemSelector); err == nil {
			if items, err := extract.Select(doc, sel); err == nil && len(items) > 0 {
				inferRoot = items[0]
			}
		}
	}

	// 精确路径
	exact := absXPath(node)
	out.Candidates = append(out.Candidates, Candidate{
		Kind:  "exact",
		Label: "仅这个元素",
		XPath: exact,
		CSS:   cssOf(node, nil),
		Count: 1,
	})

	// 重复块泛化：向上找「同 tag + 同 class 的兄弟簇」
	cur := node
	for depth := 0; depth < 5 && cur != nil && cur.Parent != nil; depth++ {
		sig := signature(cur)
		count := 0
		parentPath := ""
		for sib := cur.Parent.FirstChild; sib != nil; sib = sib.NextSibling {
			if sib.Type == html.ElementNode && signature(sib) == sig {
				count++
			}
		}
		if count >= 2 {
			parentPath = absXPath(cur.Parent)
			sel := parentPath + "//" + sigSelector(cur)
			if n, _, err := p.Count(id, "xpath:"+sel); err == nil {
				out.Candidates = append(out.Candidates, Candidate{
					Kind:  "item",
					Label: fmt.Sprintf("重复块（%d 条）", count),
					XPath: sel,
					CSS:   cssOf(cur, cur.Parent),
					Count: n,
				})
			}
		}
		// 也提供「父节点下同类子元素」这一更宽松的形态
		if cur.Parent != nil && depth == 0 {
			sel := absXPath(cur.Parent) + "/" + cur.Data
			if n, _, err := p.Count(id, "xpath:"+sel); err == nil && n >= 2 {
				out.Candidates = append(out.Candidates, Candidate{
					Kind:  "item",
					Label: fmt.Sprintf("同标签兄弟（%d 条）", n),
					XPath: sel,
					CSS:   cur.Data,
					Count: n,
				})
			}
		}
		cur = cur.Parent
	}

	out.Fields = inferFields(inferRoot)
	return out, nil
}

func inferFields(node *html.Node) []FieldGuess {
	var out []FieldGuess
	add := func(name, sel, attr, text string) {
		for _, f := range out {
			if f.Name == name {
				return
			}
		}
		out = append(out, FieldGuess{Name: name, Sel: sel, Attr: attr, Text: truncate(text, 60)})
	}
	link := find(node, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && extract.NodeValue(n, "href") != ""
	})
	if link != nil {
		add("link", relXPath(link, node), "href", extract.NodeValue(link, ""))
		add("title", relXPath(link, node), "", extract.NodeValue(link, ""))
	}
	if h := find(node, func(n *html.Node) bool {
		return n.Type == html.ElementNode && len(n.Data) == 2 && n.Data[0] == 'h' && n.Data[1] >= '1' && n.Data[1] <= '6'
	}); h != nil {
		add("title", relXPath(h, node), "", extract.NodeValue(h, ""))
	}
	date := find(node, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		if n.Data == "time" {
			return true
		}
		attrs := strings.ToLower(extract.NodeValue(n, "class"))
		return strings.Contains(attrs, "date") || strings.Contains(attrs, "time") || n.Data == "span"
	})
	if date != nil {
		attr := ""
		if date.Data == "time" && extract.NodeValue(date, "datetime") != "" {
			attr = "datetime"
		}
		add("date", relXPath(date, node), attr, extract.NodeValue(date, ""))
	}
	sum := find(node, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		cls := strings.ToLower(extract.NodeValue(n, "class"))
		return n.Data == "p" || strings.Contains(cls, "desc") || strings.Contains(cls, "summary") || strings.Contains(cls, "intro")
	})
	if sum != nil {
		add("summary", relXPath(sum, node), "", extract.NodeValue(sum, ""))
	}
	return out
}

func find(root *html.Node, pred func(*html.Node) bool) *html.Node {
	if root == nil {
		return nil
	}
	if pred(root) {
		return root
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if got := find(c, pred); got != nil {
			return got
		}
	}
	return nil
}

// ---------------------------------------------------------------- xpath 生成

func absXPath(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.ElementNode && hasID(n) {
		return fmt.Sprintf("//*[@id=%s]", quoteXPath(attr(n, "id")))
	}
	var parts []string
	cur := n
	for cur != nil && cur.Type == html.ElementNode {
		if hasID(cur) {
			parts = append([]string{fmt.Sprintf("//*[@id=%s]", quoteXPath(attr(cur, "id")))}, parts...)
			break
		}
		parts = append([]string{segment(cur)}, parts...)
		cur = cur.Parent
	}
	if len(parts) == 0 {
		return ""
	}
	if !strings.HasPrefix(parts[0], "//") {
		return "/" + strings.Join(parts, "/")
	}
	return strings.Join(parts, "/")
}

func relXPath(target, ancestor *html.Node) string {
	if target == nil {
		return ""
	}
	if target == ancestor {
		return "."
	}
	var parts []string
	cur := target
	for cur != nil && cur != ancestor && cur.Type == html.ElementNode {
		parts = append([]string{segment(cur)}, parts...)
		cur = cur.Parent
	}
	if len(parts) == 0 {
		return "."
	}
	return ".//" + strings.Join(parts, "/")
}

func segment(n *html.Node) string {
	base := n.Data
	if cls := firstClass(n); cls != "" {
		return fmt.Sprintf("%s[contains(@class,%s)]", base, quoteXPath(cls))
	}
	// 无 class 时用同名兄弟序号定位
	idx := 1
	for sib := n.PrevSibling; sib != nil; sib = sib.PrevSibling {
		if sib.Type == html.ElementNode && sib.Data == n.Data {
			idx++
		}
	}
	return fmt.Sprintf("%s[%d]", base, idx)
}

// sigSelector 把签名转成合法 XPath 节点测试（signature 里的 | 只用于比较）。
func sigSelector(n *html.Node) string {
	if cls := firstClass(n); cls != "" {
		return fmt.Sprintf("%s[contains(@class,%s)]", n.Data, quoteXPath(cls))
	}
	return n.Data
}

func signature(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}
	cls := firstClass(n)
	return n.Data + "|" + cls
}

func cssOf(n *html.Node, ancestor *html.Node) string {
	if n == nil {
		return ""
	}
	var parts []string
	cur := n
	for cur != nil && cur.Type == html.ElementNode && cur != ancestor {
		seg := cur.Data
		if cls := firstClass(cur); cls != "" {
			seg += "." + cls
		}
		parts = append([]string{seg}, parts...)
		cur = cur.Parent
	}
	return strings.Join(parts, " > ")
}

func hasID(n *html.Node) bool { return attr(n, "id") != "" }

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func firstClass(n *html.Node) string {
	cls := strings.Fields(attr(n, "class"))
	if len(cls) == 0 {
		return ""
	}
	return cls[0]
}

func quoteXPath(s string) string {
	if strings.Contains(s, "'") {
		return `"` + s + `"`
	}
	return "'" + s + "'"
}

func mustXPath(expr string) extract.Selector {
	if !strings.HasPrefix(expr, "xpath:") && !strings.HasPrefix(expr, "css:") {
		if strings.HasPrefix(expr, "/") || strings.HasPrefix(expr, "(") {
			return extract.Selector{Engine: "xpath", Expr: expr}
		}
		return extract.Selector{Engine: "css", Expr: expr}
	}
	sel, err := extract.Parse(expr)
	if err != nil {
		return extract.Selector{Engine: "xpath", Expr: expr}
	}
	return sel
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\u00a0", " "))
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
