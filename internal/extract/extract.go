package extract

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/transform"
	"golang.org/x/net/html"
)

// RawItem 是从文档里抽出的一条原始条目（字段尚未清洗）。
type RawItem struct {
	Title    string `json:"title"`
	Link     string `json:"link"`
	Date     string `json:"date"`
	Summary  string `json:"summary"`
	Category string `json:"category"`
	Content  string `json:"content"`
	GUID     string `json:"guid"`
}

// Result 是一次抽取的结果。
type Result struct {
	Items    []RawItem
	Warnings []string
	Matched  int
}

// fieldOrder 固定字段处理顺序：模板可能引用先算好的字段。
var fieldOrder = []string{"title", "link", "date", "summary", "category", "content", "guid"}

// maxItems 兜底上限，防止把整站列表拉进内存。
const maxItems = 500

// HTML 从 HTML 文档抽取条目。
func HTML(raw []byte, baseURL string, spec model.Extract, rules []model.Rule) (Result, error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("解析 HTML: %w", err)
	}
	return FromDoc(doc, baseURL, spec, rules)
}

// FromDoc 在已解析的文档上抽取（预览通道复用同一份 DOM）。
func FromDoc(doc *html.Node, baseURL string, spec model.Extract, rules []model.Rule) (Result, error) {
	res := Result{}
	var warns warnLog
	itemSel, err := Parse(spec.Item)
	if err != nil {
		return res, fmt.Errorf("条目选择器: %w", err)
	}
	nodes, err := Select(doc, itemSel)
	if err != nil {
		return res, err
	}
	res.Matched = len(nodes)
	limit := spec.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > maxItems {
		limit = maxItems
	}
	relLinks := 0
	// 条目级排除：命中的节点（或包含命中节点）直接跳过，常用于剔除广告位/置顶块
	skip := map[*html.Node]bool{}
	for _, raw := range spec.Exclude {
		sel, err := Parse(raw)
		if err != nil {
			warns.add(fmt.Sprintf("排除选择器非法 %q", raw))
			continue
		}
		hits, err := Select(doc, sel)
		if err != nil {
			continue
		}
		for _, h := range hits {
			skip[h] = true
		}
	}
	for i, node := range nodes {
		if i >= limit {
			break
		}
		if len(skip) > 0 && nodeExcluded(node, skip) {
			continue
		}
		item, itemWarns := extractNode(node, baseURL, spec, rules)
		for _, w := range itemWarns {
			warns.add(w)
		}
		if item.Link != "" && isRelative(item.Link) {
			relLinks++
		}
		if item.Title == "" && item.Link == "" {
			continue
		}
		res.Items = append(res.Items, item)
	}
	if relLinks > 0 {
		warns.add(fmt.Sprintf("%d 条链接是相对地址，已按页面地址补全", relLinks))
	}
	if len(res.Items) == 0 {
		// 命中数与产出数分开说：这两件事的修法完全不同（改条目选择器 vs 改字段选择器）
		if res.Matched == 0 {
			warns.add("条目选择器没有命中任何节点")
		} else {
			warns.add(fmt.Sprintf("命中 %d 个节点，但一条都没取到标题和链接", res.Matched))
		}
	}
	res.Warnings = warns.list()
	return res, nil
}

// warnLog 汇总警告。整页扫下来同一句话可能出现上百次（每个条目都缺同一个字段），
// 逐条抛给界面会把告警区挤成一条长缝，所以按文本聚合，出现多次的带上条数。
type warnLog struct {
	order []string
	count map[string]int
}

func (w *warnLog) add(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if w.count == nil {
		w.count = map[string]int{}
	}
	if _, ok := w.count[text]; !ok {
		w.order = append(w.order, text)
	}
	w.count[text]++
}

func (w *warnLog) list() []string {
	out := make([]string, 0, len(w.order))
	for _, text := range w.order {
		if n := w.count[text]; n > 1 {
			out = append(out, fmt.Sprintf("%s（%d 条）", text, n))
			continue
		}
		out = append(out, text)
	}
	return out
}

// nodeExcluded 判断节点自身或其后代是否落在排除集合里。
func nodeExcluded(n *html.Node, skip map[*html.Node]bool) bool {
	if skip[n] {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if nodeExcluded(c, skip) {
			return true
		}
	}
	return false
}

func extractNode(node *html.Node, baseURL string, spec model.Extract, rules []model.Rule) (RawItem, []string) {
	var (
		item   RawItem
		warns  []string
		values = map[string]string{}
	)
	// 两遍处理：先算取值字段，再渲染模板字段。
	// 模板可以引用任意字段（典型：link 用 {guid}），而 fieldOrder 里 link 排在 guid 之前；
	// 单遍处理会让占位符先渲染成空串，链接被悄悄截成 "...?projectId=" —— 不报错、看不出。
	for pass := 0; pass < 2; pass++ {
		for _, name := range fieldOrder {
			f, ok := spec.Fields[name]
			if !ok {
				continue
			}
			templated := f.Template != ""
			if (pass == 1) != templated {
				continue
			}
			v := ""
			switch {
			case templated:
				v = renderTemplate(f.Template, values)
			case f.Sel != "":
				v = valueFromSelectors(node, f, baseURL)
			}
			v = transform.ApplyField(name, v, f.Transforms, rules)
			values[name] = v
			assign(&item, name, v)
			if !templated && v == "" && !f.Optional && (name == "title" || name == "link") {
				warns = append(warns, fmt.Sprintf("条目缺少 %s", name))
			}
		}
	}
	if item.Link != "" && baseURL != "" {
		item.Link = absolutize(baseURL, item.Link)
	}
	return item, warns
}

func valueFromSelectors(node *html.Node, f model.Field, baseURL string) string {
	candidates := append([]string{f.Sel}, f.Fallback...)
	for _, raw := range candidates {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		sel, err := Parse(raw)
		if err != nil {
			continue
		}
		nodes, err := Select(node, sel)
		if err != nil || len(nodes) == 0 {
			continue
		}
		attr := f.Attr
		if sel.Attr != "" {
			attr = sel.Attr
		}
		v := NodeValue(nodes[0], attr)
		if v != "" {
			if f.Absolute || attr == "href" || attr == "src" {
				v = absolutize(baseURL, v)
			}
			return v
		}
	}
	return ""
}

func assign(item *RawItem, name, v string) {
	switch name {
	case "title":
		item.Title = v
	case "link":
		item.Link = v
	case "date":
		item.Date = v
	case "summary":
		item.Summary = v
	case "category":
		item.Category = v
	case "content":
		item.Content = v
	case "guid":
		item.GUID = v
	}
}

// JSON 从 JSON 文档抽取条目（item 为 `$.a.b` 路径，字段支持 `{$.x}` 模板）。
// item 允许带 `json:` 前缀：预览点选给出的候选统一带前缀，与 HTML 选择器保持一致的书写习惯。
func JSON(raw []byte, spec model.Extract, rules []model.Rule) (Result, error) {
	return JSONWithBase(raw, "", spec, rules)
}

// JSONWithBase 同 JSON，额外给出 baseURL 用于把相对链接补全成绝对地址
// （接口型源给的详情页路径多半是 /web/Detail?id=1 这种相对形式）。
func JSONWithBase(raw []byte, baseURL string, spec model.Extract, rules []model.Rule) (Result, error) {
	var res Result
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return res, fmt.Errorf("解析 JSON: %w", err)
	}
	rows, err := jsonGet(doc, strings.TrimPrefix(strings.TrimSpace(spec.Item), "json:"))
	if err != nil {
		return res, err
	}
	list, ok := rows.([]any)
	if !ok {
		return res, fmt.Errorf("item 路径 %q 没有指向数组（实际 %T）", spec.Item, rows)
	}
	res.Matched = len(list)
	limit := spec.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > maxItems {
		limit = maxItems
	}
	for i, row := range list {
		if i >= limit {
			break
		}
		var (
			item   RawItem
			values = map[string]string{}
		)
		for _, name := range fieldOrder {
			f, ok := spec.Fields[name]
			if !ok {
				continue
			}
			var v string
			switch {
			case f.Template != "":
				v = renderJSONTemplate(f.Template, row)
			case f.Sel != "":
				v = jsonString(jsonGetValue(row, f.Sel))
			}
			v = transform.ApplyField(name, v, f.Transforms, rules)
			if name == "link" && isRelative(v) {
				v = absolutize(baseURL, v)
			}
			values[name] = v
			assign(&item, name, v)
		}
		if item.Title == "" && item.Link == "" {
			continue
		}
		res.Items = append(res.Items, item)
	}
	if len(res.Items) == 0 {
		res.Warnings = append(res.Warnings, "JSON 条目列表为空，检查 item 路径与字段名")
	}
	return res, nil
}

// Snapshot 抽取 page 型监控区域的归一化文本；同时返回该区域的 HTML。
func Snapshot(raw []byte, baseURL string, spec model.Extract) (text string, regionHTML string, warnings []string, err error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", "", nil, fmt.Errorf("解析 HTML: %w", err)
	}
	roots := []*html.Node{doc}
	if strings.TrimSpace(spec.Scope) != "" {
		sel, err := Parse(spec.Scope)
		if err != nil {
			return "", "", nil, fmt.Errorf("区域选择器: %w", err)
		}
		nodes, err := Select(doc, sel)
		if err != nil {
			return "", "", nil, err
		}
		if len(nodes) == 0 {
			return "", "", []string{"区域选择器没有命中，已退回整页"}, nil
		}
		roots = nodes[:1]
		if len(nodes) > 1 {
			warnings = append(warnings, fmt.Sprintf("区域选择器命中 %d 个节点，只取第一个", len(nodes)))
		}
	}
	for _, ex := range spec.Exclude {
		sel, err := Parse(ex)
		if err != nil {
			continue
		}
		for _, root := range roots {
			nodes, _ := Select(root, sel)
			for _, n := range nodes {
				n.Parent.RemoveChild(n)
			}
		}
	}
	var parts []string
	for _, root := range roots {
		parts = append(parts, BlockText(root))
	}
	text = strings.Join(parts, "\n")
	for _, pat := range spec.IgnoreRegex {
		re, err := regexp.Compile(pat)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("忽略正则非法 %q", pat))
			continue
		}
		text = re.ReplaceAllString(text, "")
	}
	text = collapseSpace(text)
	if len(roots) > 0 {
		regionHTML = InnerHTML(roots[0])
	}
	if text == "" {
		warnings = append(warnings, "监控区域归一化后为空文本")
	}
	return text, regionHTML, warnings, nil
}

var innerSpace = regexp.MustCompile(`[ \t\x{00a0}]+`)

// blockTags 是快照文本里需要断行的块级元素。
var blockTags = map[string]bool{
	"p": true, "div": true, "li": true, "ul": true, "ol": true, "dl": true,
	"tr": true, "td": true, "th": true, "table": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "header": true, "footer": true, "nav": true,
	"aside": true, "main": true, "blockquote": true, "pre": true, "dt": true, "dd": true,
}

// BlockText 把节点渲染成「每个块级元素一行」的纯文本。
// page 型快照必须用它：InnerText 会把整块折成一行，
// 行级 diff 与 min_changed_lines 阈值也就失去意义了。
func BlockText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		switch node.Type {
		case html.ElementNode:
			switch node.Data {
			case "script", "style", "noscript", "template":
				return
			}
			block := blockTags[node.Data]
			if block {
				b.WriteByte('\n')
			}
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if block {
				b.WriteByte('\n')
			}
		case html.TextNode:
			t := strings.TrimSpace(node.Data)
			if t == "" {
				return
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(t)
		}
	}
	walk(n)
	return collapseSpace(b.String())
}

func collapseSpace(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(innerSpace.ReplaceAllString(l, " "))
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------- helpers

var tmplVar = regexp.MustCompile(`\{([a-zA-Z_]+)\}`)

func renderTemplate(tpl string, values map[string]string) string {
	return tmplVar.ReplaceAllStringFunc(tpl, func(m string) string {
		key := strings.Trim(m, "{}")
		if v, ok := values[key]; ok {
			return v
		}
		return ""
	})
}

var jsonTmplVar = regexp.MustCompile(`\{(\$[^}]*)\}`)

func renderJSONTemplate(tpl string, row any) string {
	return jsonTmplVar.ReplaceAllStringFunc(tpl, func(m string) string {
		path := strings.Trim(m, "{}")
		return jsonString(jsonGetValue(row, path))
	})
}

func jsonGetValue(row any, path string) any {
	path = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(path), "json:"))
	if !strings.HasPrefix(path, "$") {
		return nil
	}
	v, err := jsonGet(row, path)
	if err != nil {
		return nil
	}
	return v
}

func jsonString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

func isRelative(link string) bool {
	return !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "//")
}

// absolutize 把相对地址按 base 补全。
func absolutize(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || base == "" {
		return ref
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}
