// Package extract 把选择器 DSL 落到文档上：HTML 用 XPath/CSS，JSON 用路径子集。
package extract

import (
	"fmt"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/antchfx/htmlquery"
	"github.com/antchfx/xpath"
	"golang.org/x/net/html"
)

// Selector 是解析后的选择器。
type Selector struct {
	Engine string // css | xpath | json | const
	Expr   string
	Attr   string
}

// Parse 解析 `[css:|xpath:|json:] <expr> [@attr]`，默认引擎为 css。
func Parse(raw string) (Selector, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Selector{}, fmt.Errorf("空选择器")
	}
	sel := Selector{Engine: "css"}
	switch {
	case strings.HasPrefix(s, "css:"):
		s = strings.TrimPrefix(s, "css:")
	case strings.HasPrefix(s, "xpath:"):
		sel.Engine, s = "xpath", strings.TrimPrefix(s, "xpath:")
	case strings.HasPrefix(s, "json:"):
		sel.Engine, s = "json", strings.TrimPrefix(s, "json:")
	case strings.HasPrefix(s, "const:"):
		sel.Engine, s = "const", strings.TrimPrefix(s, "const:")
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "./"), strings.HasPrefix(s, "("):
		// 框选器给出的是裸 XPath，按 XPath 解释而不是 CSS
		sel.Engine = "xpath"
	}
	if sel.Engine == "css" && !strings.HasPrefix(s, "$") {
		if i := strings.LastIndex(s, "@"); i > 0 {
			s, sel.Attr = s[:i], s[i+1:]
		}
	}
	sel.Expr = strings.TrimSpace(s)
	if sel.Expr == "" && sel.Engine != "const" {
		return Selector{}, fmt.Errorf("选择器 %q 的表达式为空", raw)
	}
	if err := Validate(sel); err != nil {
		return Selector{}, err
	}
	return sel, nil
}

// Validate 编译选择器，语法错误在这里暴露（保存前调用）。
func Validate(sel Selector) error {
	switch sel.Engine {
	case "css":
		if _, err := cascadia.Compile(sel.Expr); err != nil {
			return fmt.Errorf("CSS 选择器非法 %q: %w", sel.Expr, err)
		}
	case "xpath":
		if _, err := xpath.Compile(sel.Expr); err != nil {
			return fmt.Errorf("XPath 非法 %q: %w", sel.Expr, err)
		}
	case "json":
		if !strings.HasPrefix(sel.Expr, "$") {
			return fmt.Errorf("JSON 路径必须以 $ 开头: %q", sel.Expr)
		}
		if _, err := normalizeJSONPath(sel.Expr); err != nil {
			return err
		}
	}
	return nil
}

// Select 在节点上求值，返回命中节点。
func Select(top *html.Node, sel Selector) ([]*html.Node, error) {
	switch sel.Engine {
	case "css":
		cs, err := cascadia.Compile(sel.Expr)
		if err != nil {
			return nil, err
		}
		if top == nil {
			return nil, nil
		}
		return cascadia.QueryAll(top, cs), nil
	case "xpath":
		if top == nil {
			return nil, nil
		}
		nodes, err := htmlquery.QueryAll(top, sel.Expr)
		if err != nil {
			return nil, fmt.Errorf("XPath 求值失败 %q: %w", sel.Expr, err)
		}
		return nodes, nil
	default:
		return nil, fmt.Errorf("引擎 %q 不适用于节点选择", sel.Engine)
	}
}

// NodeValue 取节点上的字段值；attr 为 text/html 时分别取文本与内层 HTML。
func NodeValue(n *html.Node, attr string) string {
	if n == nil {
		return ""
	}
	switch attr {
	case "text":
		return strings.TrimSpace(htmlquery.InnerText(n))
	case "html":
		return InnerHTML(n)
	case "":
		return strings.TrimSpace(htmlquery.InnerText(n))
	default:
		return strings.TrimSpace(htmlquery.SelectAttr(n, attr))
	}
}

// InnerHTML 序列化节点的子节点。
func InnerHTML(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return b.String()
		}
	}
	return strings.TrimSpace(b.String())
}
