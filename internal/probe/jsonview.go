package probe

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

// 接口型源的预览不是「不能框选的一坨 <pre>」，而是一棵可点选的 JSON 树：
// 每一行都带 data-jpath，点一下就把该节点的路径回传给编辑器，
// 由后端按会话 Kind 生成条目路径与字段推断。
//
// 树是服务端渲染的静态 HTML：折叠、展开、选中由注入的 picker.js 完成，
// 预览域内的第三方脚本一律不执行（sanitize 已剥离）。

const (
	jsonMaxNodes    = 4000 // 超大响应只渲染前若干节点，避免预览页卡死
	jsonPreviewRows = 8    // 数组默认渲染的项数，其余折叠
	jsonOpenDepth   = 2    // 默认展开的层级
	jsonMaxText     = 96   // 单个值的展示长度：树要紧凑，长正文不该撑满预览
)

const jsonViewCSS = "body{margin:0;padding:10px 12px;background:#fff;color:#1f2430;" +
	"font:12px/1.75 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}" +
	"#jv{padding-bottom:24px}" +
	".r{position:relative;padding:0 4px;border-radius:3px;white-space:pre-wrap;word-break:break-all}" +
	".r:hover{background:#f3f1ff}" +
	".r.sel{background:#e7e3ff;box-shadow:inset 0 0 0 1px #7f77dd}" +
	".k{color:#1f4fd8}" +
	".d{color:#8a8f9c}" +
	".tw{display:inline-block;width:12px;color:#8a8f9c;cursor:pointer;user-select:none}" +
	".v.s{color:#0a7a4a}" +
	".v.n{color:#b35c00}" +
	".v.b{color:#7d3cc4}" +
	".v.z{color:#9aa0a6}" +
	".v.a,.v.o{color:#6b7280}" +
	".m{color:#534ab7;cursor:pointer}" +
	".c{margin-left:13px;padding-left:5px;border-left:1px dotted #e1e4ea}" +
	".r.fold+.c{display:none}" +
	".rest{display:none}" +
	".tip{padding:6px 4px 8px;color:#6b7280;font:11px/1.6 system-ui,-apple-system,sans-serif}"

// isJSONResponse 判断一次响应是不是 JSON：先看 Content-Type，再看首字符。
// 不少内部接口（尤其 ASP.NET/IIS）会谎报 text/html，所以两种都要看。
func isJSONResponse(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "json") {
		return true
	}
	s := strings.TrimLeft(string(body), " \t\r\n\ufeff")
	if s == "" {
		return false
	}
	return s[0] == '{' || s[0] == '['
}

// parseJSONDoc 解析响应体；失败返回 nil（调用方按 HTML 处理）。
func parseJSONDoc(body []byte) (any, bool) {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	return doc, true
}

// parseJSONDocGuess 综合 Content-Type 与首字符判断，能解析才当 JSON。
func parseJSONDocGuess(body []byte, contentType string) (any, bool) {
	if !isJSONResponse(contentType, body) {
		return nil, false
	}
	return parseJSONDoc(body)
}

// 脚本里的异步取数调用：命中就说明列表很可能不在静态 HTML 里。
var (
	ajaxCallRe = regexp.MustCompile(`(?:\$\.(?:get|post|getJSON|ajax)|fetch|axios\.(?:get|post))\s*\(\s*['"]([^'"]+)['"]`)
	ajaxURLRe  = regexp.MustCompile(`\burl\s*:\s*['"]([^'"]+)['"]`)
	// 列表容器：静态页面里几乎没有这些标签，基本可以断定内容是脚本塞进去的
	listTags = map[string]bool{"li": true, "tr": true, "article": true, "option": true}
	// 静态资源不算接口
	assetExt = []string{".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf"}
)

// shellPageHint 识别「列表靠脚本异步渲染、HTTP 通道抓下来是空壳」的页面。
// 这类页面框选无从下手，必须早点告诉用户：换浏览器通道，或直接按接口型配脚本里那个接口。
func shellPageHint(body []byte) string {
	doc, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var (
		hasScript bool
		text      strings.Builder
		scripts   strings.Builder
		listNodes int
	)
	const enough = 400
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if text.Len() >= enough && listNodes > 0 {
			return
		}
		switch n.Type {
		case xhtml.ElementNode:
			switch n.Data {
			case "script", "style", "noscript", "template":
				if n.Data == "script" {
					hasScript = true
					if n.FirstChild != nil && n.FirstChild.Type == xhtml.TextNode {
						scripts.WriteString(n.FirstChild.Data)
						scripts.WriteString("\n")
					}
				}
				return
			}
			if listTags[n.Data] {
				listNodes++
			}
		case xhtml.TextNode:
			text.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil && (text.Len() < enough || listNodes == 0); c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if !hasScript {
		return ""
	}
	// 有静态列表结构就认它是个正常的列表页，不打扰
	if listNodes > 0 {
		return ""
	}
	// 两种空壳：① 几乎没有静态文本；② 有脚本取数、页面里却没有列表结构（SPA 典型）
	emptyText := len([]rune(strings.TrimSpace(text.String()))) < 200
	ajax := ajaxCallRe.MatchString(scripts.String()) || ajaxURLRe.MatchString(scripts.String())
	if !emptyText && !(ajax && text.Len() < 800) {
		return ""
	}
	// 能看出脚本在调哪个接口就直接给出来，省得用户去翻源码
	if api := firstAPIGuess(scripts.String()); api != "" {
		return "页面里的列表由脚本异步渲染，HTTP 通道抓不到：把渲染通道改成「浏览器渲染」，" +
			"或按接口型把 " + api + " 填进 api.url（脚本里就是这个地址）。"
	}
	return "页面里的列表由脚本异步渲染，HTTP 通道抓不到：把渲染通道改成「浏览器渲染」，或按接口型配上真实的接口地址。"
}

// firstAPIGuess 从脚本里挑第一个像接口的路径（跳过 .js/.css 等静态资源）。
func firstAPIGuess(script string) string {
	var seen []string
	for _, re := range []*regexp.Regexp{ajaxCallRe, ajaxURLRe} {
		for _, m := range re.FindAllStringSubmatch(script, -1) {
			u := strings.TrimSpace(m[1])
			if u == "" || strings.HasPrefix(u, "#") {
				continue
			}
			low := strings.ToLower(u)
			skip := false
			for _, ext := range assetExt {
				if strings.HasSuffix(low, ext) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			dup := false
			for _, s := range seen {
				if s == u {
					dup = true
					break
				}
			}
			if !dup {
				seen = append(seen, u)
			}
		}
		if len(seen) > 0 {
			break
		}
	}
	if len(seen) == 0 {
		return ""
	}
	return seen[0]
}

// jsonTreeHTML 把 JSON 文档渲染成可点选的树页面。
func jsonTreeHTML(doc any, note string) []byte {
	var v jsonView
	if strings.TrimSpace(note) != "" {
		v.b.WriteString(`<div class="tip">` + html.EscapeString(note) + `</div>`)
	}
	v.b.WriteString(`<div id="jv">`)
	v.node(0, "", "$", doc, false)
	v.b.WriteString(`</div>`)
	if v.truncated {
		v.b.WriteString(`<div class="tip">响应过大，预览只渲染了前 ` +
			strconv.Itoa(jsonMaxNodes) + ` 个节点；选择与抽取仍按完整响应计算。</div>`)
	}
	return []byte("<!DOCTYPE html><html lang=\"zh\"><head><meta charset=\"utf-8\">" +
		"<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">" +
		"<title>接口预览</title><style>" + jsonViewCSS + "</style></head>" +
		"<body data-mode=\"json\">" + v.b.String() + "</body></html>")
}

type jsonView struct {
	b         strings.Builder
	nodes     int
	truncated bool
}

func (v *jsonView) node(depth int, key, path string, val any, forceOpen bool) {
	if v.nodes >= jsonMaxNodes {
		v.truncated = true
		return
	}
	v.nodes++
	switch t := val.(type) {
	case map[string]any:
		v.open(depth, key, path, "object", "{"+strconv.Itoa(len(t))+" 个字段}", forceOpen)
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v.node(depth+1, k, childPath(path, k), t[k], false)
		}
		v.close()
	case []any:
		v.open(depth, key, path, "array", "["+strconv.Itoa(len(t))+" 条]", forceOpen)
		head := len(t)
		if head > jsonPreviewRows {
			head = jsonPreviewRows
		}
		for i := 0; i < head; i++ {
			// 第一条记录默认展开：用户要看的就是一条记录长什么样
			v.node(depth+1, "["+strconv.Itoa(i)+"]", fmt.Sprintf("%s[%d]", path, i), t[i], i == 0)
		}
		if rest := len(t) - head; rest > 0 {
			v.b.WriteString(`<div class="r m" data-more="1">… 还有 ` + strconv.Itoa(rest) + ` 条，点击展开</div>`)
			v.b.WriteString(`<div class="c rest">`)
			for i := head; i < len(t); i++ {
				v.node(depth+1, "["+strconv.Itoa(i)+"]", fmt.Sprintf("%s[%d]", path, i), t[i], false)
			}
			v.b.WriteString(`</div>`)
		}
		v.close()
	default:
		v.leaf(key, path, val)
	}
}

func (v *jsonView) open(depth int, key, path, kind, hint string, forceOpen bool) {
	folded := depth >= jsonOpenDepth && !forceOpen
	cls := "r kid"
	if folded {
		cls += " fold"
	}
	arrow := "▾"
	if folded {
		arrow = "▸"
	}
	v.b.WriteString(`<div class="` + cls + `" data-jpath="` + html.EscapeString(path) +
		`" data-jtype="` + kind + `"><span class="tw">` + arrow + `</span>`)
	if key != "" {
		v.b.WriteString(`<span class="k">` + html.EscapeString(key) + `</span><span class="d">: </span>`)
	}
	v.b.WriteString(`<span class="v ` + kind[:1] + `">` + html.EscapeString(hint) + `</span></div>`)
	v.b.WriteString(`<div class="c">`)
}

func (v *jsonView) close() { v.b.WriteString(`</div>`) }

func (v *jsonView) leaf(key, path string, val any) {
	kind, text := "z", "null"
	switch t := val.(type) {
	case string:
		kind, text = "s", quoteForView(t)
	case bool:
		kind = "b"
		text = strconv.FormatBool(t)
	case float64:
		kind = "n"
		text = strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		kind, text = "z", "null"
	default:
		kind = "s"
		b, _ := json.Marshal(val)
		text = string(b)
	}
	// 值一律转义后再进 HTML：接口返回什么都不能在预览里变成标签
	text = html.EscapeString(text)
	v.b.WriteString(`<div class="r" data-jpath="` + html.EscapeString(path) + `" data-jtype="` + kind + `">`)
	if key != "" {
		v.b.WriteString(`<span class="k">` + html.EscapeString(key) + `</span><span class="d">: </span>`)
	}
	v.b.WriteString(`<span class="v ` + kind + `">` + text + `</span></div>`)
}

func truncateForView(s string) string {
	rs := []rune(s)
	if len(rs) <= jsonMaxText {
		return s
	}
	return string(rs[:jsonMaxText]) + "…"
}

// quoteForView 给字符串值加引号并显示成一行：换行/制表符转义，双引号保持原样
// （外层已做 HTML 转义，不需要再转义，否则满屏都是反斜杠，正文类字段根本没法读）。
var viewEscaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "", "\t", "\\t")

func quoteForView(s string) string {
	return `"` + viewEscaper.Replace(truncateForView(s)) + `"`
}

// childPath 拼接路径段：下标段直接跟在父路径后（$.data[0]），其余用点连接。
func childPath(parent, key string) string {
	if strings.HasPrefix(key, "[") {
		return parent + key
	}
	if parent == "$" {
		return "$." + key
	}
	return parent + "." + key
}

// ---------------------------------------------------------------- JSON 求值

func jsonExpr(selector string) string {
	s := strings.TrimSpace(selector)
	s = strings.TrimPrefix(s, "json:")
	return strings.TrimSpace(s)
}

// jsonValue 在会话文档上求一个路径。路径非法或不存在都返回 nil, nil。
func jsonValue(doc any, expr string) (any, error) {
	return jsonGetDoc(doc, jsonExpr(expr))
}

// jsonGetDoc 是 jsonPath 子集求值：$.a.b[0].c。
func jsonGetDoc(doc any, path string) (any, error) {
	p := strings.TrimSpace(path)
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("JSON 路径必须以 $ 开头: %q", path)
	}
	segs, err := splitJSONPath(p)
	if err != nil {
		return nil, err
	}
	cur := doc
	for _, seg := range segs {
		if seg.key != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, nil
			}
			v, ok := m[seg.key]
			if !ok {
				return nil, nil
			}
			cur = v
		}
		if seg.idx >= 0 {
			list, ok := cur.([]any)
			if !ok || seg.idx >= len(list) {
				return nil, nil
			}
			cur = list[seg.idx]
		}
	}
	return cur, nil
}

type jsonSeg struct {
	key string
	idx int
}

func splitJSONPath(path string) ([]jsonSeg, error) {
	p := strings.TrimPrefix(strings.TrimSpace(path), "$")
	p = strings.TrimPrefix(p, ".")
	var segs []jsonSeg
	if p == "" {
		return segs, nil
	}
	for _, raw := range strings.Split(p, ".") {
		if raw == "" {
			continue
		}
		key := raw
		idx := -1
		if i := strings.Index(raw, "["); i >= 0 {
			if !strings.HasSuffix(raw, "]") {
				return nil, fmt.Errorf("JSON 路径段格式错误: %q", raw)
			}
			key = raw[:i]
			n, err := strconv.Atoi(raw[i+1 : len(raw)-1])
			if err != nil {
				return nil, fmt.Errorf("JSON 路径下标非法: %q", raw)
			}
			idx = n
		}
		if key == "" && idx < 0 {
			return nil, fmt.Errorf("JSON 路径段为空: %q", raw)
		}
		segs = append(segs, jsonSeg{key: key, idx: idx})
	}
	return segs, nil
}

// ---------------------------------------------------------------- 计数与样例

// jsonCount 给「条目路径」算命中数与样例，语义与 HTML 的 Count 对齐。
func jsonCount(doc any, expr string) (int, []string, error) {
	val, err := jsonValue(doc, expr)
	if err != nil {
		return 0, nil, err
	}
	switch t := val.(type) {
	case nil:
		return 0, nil, nil
	case []any:
		var samples []string
		for i, it := range t {
			if i >= 5 {
				break
			}
			samples = append(samples, truncate(jsonSampleText(it), 80))
		}
		return len(t), samples, nil
	case map[string]any:
		return 1, []string{truncate(jsonSampleText(t), 80)}, nil
	default:
		return 1, []string{truncate(jsonScalarText(t), 80)}, nil
	}
}

// jsonSampleText 给一个对象取「像标题的那一项」，取不到就退回首项。
func jsonSampleText(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return jsonScalarText(v)
	}
	// 键名大小写与命名风格各异（ArtName / art_name），统一走启发式匹配
	if k := pickKey(m, titleKeys); k != "" {
		return jsonScalarText(m[k])
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s := jsonScalarText(m[k]); s != "" {
			return s
		}
	}
	return ""
}

func jsonScalarText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// ---------------------------------------------------------------- 字段推断

var (
	titleKeys   = []string{"title", "name", "subject", "artname", "标题", "名称"}
	linkKeys    = []string{"url", "link", "href", "detailurl", "详情页", "链接"}
	dateKeys    = []string{"pubdate", "publishdate", "date", "time", "created", "updated", "starttime", "发布时间", "时间"}
	summaryKeys = []string{"summary", "desc", "description", "abstract", "intro", "remark", "brief", "摘要", "简介"}
	guidKeys    = []string{"id", "guid", "uuid", "key", "artid"}
	contentKeys = []string{"content", "body", "html", "text", "正文", "内容"}
)

// jsonInferFields 以一条记录为根，按键名启发式推断字段。
// 返回的选择器是「相对记录」的路径（$.ArtName），与 extract.JSON 的字段语义一致。
func jsonInferFields(row any) []FieldGuess {
	m, ok := row.(map[string]any)
	if !ok {
		return nil
	}
	var out []FieldGuess
	add := func(name, key string) {
		for _, f := range out {
			if f.Name == name {
				return
			}
		}
		out = append(out, FieldGuess{
			Name: name,
			Sel:  childPath("$", key),
			Text: truncate(jsonScalarText(m[key]), 60),
		})
	}
	// 顺序即优先级：标题 → 链接 → 时间 → 摘要 → 正文 → 唯一标识
	if k := pickKey(m, titleKeys); k != "" {
		add("title", k)
	}
	if k := pickKey(m, linkKeys); k != "" {
		add("link", k)
	}
	if k := pickKey(m, dateKeys); k != "" {
		add("date", k)
	}
	if k := pickKey(m, summaryKeys); k != "" {
		add("summary", k)
	}
	if k := pickKey(m, contentKeys); k != "" {
		add("content", k)
	}
	if k := pickKey(m, guidKeys); k != "" {
		add("guid", k)
	}
	return out
}

// pickKey 按候选键名找：先精确匹配，再「包含」匹配，跳过明显不是正文的键。
func pickKey(m map[string]any, keys []string) string {
	for _, want := range keys {
		for k := range m {
			if strings.EqualFold(k, want) {
				return k
			}
		}
	}
	for _, want := range keys {
		for k := range m {
			lk := strings.ToLower(k)
			if !strings.Contains(lk, want) {
				continue
			}
			if strings.Contains(lk, "pic") || strings.Contains(lk, "img") || strings.Contains(lk, "icon") {
				continue // 图片字段不是标题/链接
			}
			return k
		}
	}
	return ""
}

// ---------------------------------------------------------------- 建议

// jsonSuggest 依据点中的 JSON 路径给出条目路径候选与字段推断。
func (p *Probe) jsonSuggest(s *Session, expr, itemSelector string) (Suggestion, error) {
	if s.Doc == nil {
		return Suggestion{}, fmt.Errorf("预览会话没有 JSON 文档")
	}
	path := jsonExpr(expr)
	val, err := jsonValue(s.Doc, path)
	if err != nil {
		return Suggestion{}, err
	}
	out := Suggestion{
		Tag:  jsonKindOf(val),
		Text: truncate(jsonSampleText(val), 120),
	}

	// 推断的根：优先用「条目路径指向的记录」，与 HTML 侧以条目节点为根的语义一致。
	rowPath := jsonWalkToRecord(s.Doc, path)
	if item := strings.TrimSpace(itemSelector); item != "" {
		if v, err := jsonValue(s.Doc, item); err == nil {
			if list, ok := v.([]any); ok && len(list) > 0 {
				// 条目选择器指的是数组，推断字段要落在第一条记录上
				rowPath = jsonExpr(item) + "[0]"
			}
		}
	}
	row, _ := jsonValue(s.Doc, rowPath)

	// 条目候选：记录所在的数组
	if itemPath := arrayPathOf(s.Doc, rowPath); itemPath != "" {
		if n, _, err := jsonCount(s.Doc, itemPath); err == nil && n > 0 {
			out.Candidates = append(out.Candidates, Candidate{
				Kind:  "item",
				Label: fmt.Sprintf("记录列表（%d 条）", n),
				XPath: "json:" + itemPath,
				CSS:   itemPath,
				Count: n,
			})
		}
	}
	// 精确候选：点中的节点本身
	n, _, _ := jsonCount(s.Doc, path)
	out.Candidates = append(out.Candidates, Candidate{
		Kind:  "exact",
		Label: "仅这个字段",
		XPath: "json:" + path,
		CSS:   path,
		Count: n,
	})
	out.Fields = jsonInferFields(row)
	return out, nil
}

func jsonKindOf(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	default:
		return "null"
	}
}

// jsonWalkToRecord 从点中的路径往回找最近的一条「记录」（数组元素对象）。
// 点字段时字段要以记录为根，点整条记录时就是它自己。
func jsonWalkToRecord(doc any, path string) string {
	p := jsonExpr(path)
	if v, err := jsonValue(doc, p); err == nil {
		if _, ok := v.([]any); ok {
			return p + "[0]"
		}
	}
	segs, err := splitJSONPath(p)
	if err != nil {
		return p
	}
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i].idx >= 0 {
			// 截到这个下标段为止：$.data[0].ArtName → $.data[0]
			return rebuildPath(segs[:i+1])
		}
	}
	// 路径里没有下标：若它本身是对象，就当作记录
	if v, err := jsonValue(doc, p); err == nil {
		if _, ok := v.(map[string]any); ok {
			return p
		}
	}
	return "$"
}

// arrayPathOf 返回「记录所在的数组」路径：$.data[0].ArtName → $.data。
func arrayPathOf(doc any, rowPath string) string {
	p := jsonExpr(rowPath)
	segs, err := splitJSONPath(p)
	if err != nil {
		return ""
	}
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i].idx >= 0 {
			// 下标段的父路径要剥掉下标本身：`$.data[0]` 的父是 `$.data`
			parent := rebuildPath(segs[:i])
			if segs[i].key != "" {
				parent += "." + segs[i].key
			}
			if v, err := jsonValue(doc, parent); err == nil {
				if _, ok := v.([]any); ok {
					return parent
				}
			}
			return ""
		}
	}
	// 没下标：若路径自己就是数组，直接用
	if v, err := jsonValue(doc, p); err == nil {
		if _, ok := v.([]any); ok {
			return p
		}
	}
	return ""
}

func rebuildPath(segs []jsonSeg) string {
	if len(segs) == 0 {
		return "$"
	}
	out := "$"
	for _, s := range segs {
		if s.key != "" {
			out += "." + s.key
		}
		if s.idx >= 0 {
			out += "[" + strconv.Itoa(s.idx) + "]"
		}
	}
	return out
}
