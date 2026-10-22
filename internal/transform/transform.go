// Package transform 是字段清洗规则管线：正则替换、去前后缀、日期归一、模板等。
package transform

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
	xhtml "golang.org/x/net/html"
)

// Ops 列出所有支持的算子，供前端下拉。
var Ops = []string{
	"trim", "collapse_ws", "lower", "upper", "regex_replace", "regex_extract",
	"strip_prefix", "strip_suffix", "remove_tags", "html_unescape", "date_parse",
	"truncate", "replace_literal",
}

// Compile 校验并编译一条规则，返回变换函数。
func Compile(r model.Rule) (func(string) string, error) {
	op := strings.TrimSpace(r.Op)
	if op == "" {
		return nil, fmt.Errorf("规则 %q 缺少 op", r.ID)
	}
	switch op {
	case "trim":
		return strings.TrimSpace, nil
	case "collapse_ws":
		re := regexp.MustCompile(`\s+`)
		return func(s string) string { return strings.TrimSpace(re.ReplaceAllString(s, " ")) }, nil
	case "lower":
		return strings.ToLower, nil
	case "upper":
		return strings.ToUpper, nil
	case "regex_replace", "regex_extract":
		expr := r.Pattern
		if strings.Contains(r.Flags, "i") {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("规则 %q 正则非法: %w", r.ID, err)
		}
		if op == "regex_extract" {
			return func(s string) string {
				m := re.FindStringSubmatch(s)
				if len(m) == 0 {
					return s
				}
				return m[len(m)-1]
			}, nil
		}
		return func(s string) string { return re.ReplaceAllString(s, r.Replace) }, nil
	case "strip_prefix":
		return func(s string) string { return strings.TrimPrefix(s, r.Value) }, nil
	case "strip_suffix":
		return func(s string) string { return strings.TrimSuffix(s, r.Value) }, nil
	case "replace_literal":
		return func(s string) string { return strings.ReplaceAll(s, r.Value, r.Replace) }, nil
	case "remove_tags":
		return RemoveTags, nil
	case "html_unescape":
		return html.UnescapeString, nil
	case "truncate":
		n := r.N
		if n <= 0 {
			n = 200
		}
		return func(s string) string {
			rs := []rune(s)
			if len(rs) <= n {
				return s
			}
			return strings.TrimSpace(string(rs[:n])) + "…"
		}, nil
	case "date_parse":
		layouts := r.Layouts
		if len(layouts) == 0 {
			layouts = []string{"2006-01-02", "2006/01/02", "2006-01-02 15:04", "01-02", "2006年01月02日"}
		}
		loc := time.Local
		if r.TZ != "" {
			if l, err := time.LoadLocation(r.TZ); err == nil {
				loc = l
			}
		}
		return func(s string) string {
			return NormalizeDate(s, layouts, loc)
		}, nil
	default:
		return nil, fmt.Errorf("规则 %q 的 op %q 不受支持", r.ID, op)
	}
}

// RemoveTags 去标签并折叠空白。
func RemoveTags(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	doc, err := xhtml.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(b.String(), " "))
}

// Apply 按顺序执行规则，只作用于 target 命中的字段（target 为空表示全部）。
func Apply(field, value string, rules []model.Rule) string {
	for _, r := range rules {
		if len(r.Target) > 0 && !contains(r.Target, field) && !contains(r.Target, "*") {
			continue
		}
		fn, err := Compile(r)
		if err != nil {
			continue
		}
		value = fn(value)
	}
	return value
}

// ApplyField 是抽取阶段唯一入口：带 Target 的规则按字段自动生效，
// 不带 Target 的规则只在字段的 transforms 里按 id 显式引用时生效。
func ApplyField(field, value string, names []string, rules []model.Rule) string {
	for _, r := range rules {
		var hit bool
		if len(r.Target) > 0 {
			hit = contains(r.Target, field) || contains(r.Target, "*")
		} else {
			hit = contains(names, r.ID)
		}
		if !hit {
			continue
		}
		if fn, err := Compile(r); err == nil {
			value = fn(value)
		}
	}
	return value
}

// NormalizeDate 把多种日期串归一为 RFC3339；无法解析时原样返回。
func NormalizeDate(raw string, layouts []string, loc *time.Location) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			if l == "01-02" || l == "1-2" {
				now := time.Now().In(loc)
				t = time.Date(now.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
			}
			return t.Format(time.RFC3339)
		}
	}
	// 常见中文/英文格式兜底
	for _, l := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return s
}

// ParseDate 解析归一后的日期为 Unix 秒；失败返回 0。
func ParseDate(raw string) int64 {
	if raw == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Unix()
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 1e12 { // 毫秒
			return n / 1000
		}
		return n
	}
	for _, l := range []string{"2006-01-02 15:04:05", "2006-01-02", "2006/01/02", "01-02"} {
		if t, err := time.ParseInLocation(l, raw, time.Local); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// Validate 校验全部规则，返回第一个错误。
func Validate(rules []model.Rule) error {
	for _, r := range rules {
		if _, err := Compile(r); err != nil {
			return err
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
