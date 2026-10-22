package probe

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// sanitize 是预览的关键安全边界：第三方页面只允许「渲染」，不允许执行任何脚本，
// 也不允许把自己的 CSP / 表单 / 事件处理器带进我们的同源上下文。
func sanitize(raw []byte, baseURL string) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	dropTags := map[string]bool{
		"script": true, "iframe": true, "object": true, "embed": true,
		"frame": true, "frameset": true, "noscript": true, "template": true,
	}
	var remove []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if dropTags[n.Data] {
				remove = append(remove, n)
				return
			}
			if n.Data == "link" {
				for _, a := range n.Attr {
					if a.Key == "rel" && strings.Contains(strings.ToLower(a.Val), "preload") {
						remove = append(remove, n)
						return
					}
				}
			}
			if n.Data == "meta" {
				for _, a := range n.Attr {
					if a.Key == "http-equiv" && strings.EqualFold(a.Val, "content-security-policy") {
						remove = append(remove, n)
						return
					}
				}
			}
			// 去掉所有 on* 事件属性与 form 的提交目标
			attrs := n.Attr[:0]
			for _, a := range n.Attr {
				if strings.HasPrefix(strings.ToLower(a.Key), "on") {
					continue
				}
				if n.Data == "form" && (a.Key == "action" || a.Key == "method") {
					continue
				}
				attrs = append(attrs, a)
			}
			n.Attr = attrs
		}
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			walk(c)
			c = next
		}
	}
	walk(doc)
	for _, n := range remove {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}

	// 注入 <base>（让相对资源仍指向目标站）与 picker
	head := findHead(doc)
	if head == nil {
		return nil, errNoHead
	}
	if baseURL != "" {
		base := &html.Node{Type: html.ElementNode, Data: "base"}
		base.Attr = []html.Attribute{{Key: "href", Val: baseURL}}
		head.InsertBefore(base, head.FirstChild)
	}
	// 内联注入而不是 <script src>：页面里有 <base href="目标站">，
	// 相对路径会指到目标站去，内联可以完全绕开这个问题。
	script := &html.Node{Type: html.ElementNode, Data: "script"}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: string(pickerJS)})
	head.AppendChild(script)

	var out bytes.Buffer
	if err := html.Render(&out, doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

var errNoHead = &headError{}

type headError struct{}

func (*headError) Error() string { return "预览页面缺少 <head>" }

func findHead(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "head" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if h := findHead(c); h != nil {
			return h
		}
	}
	return nil
}
