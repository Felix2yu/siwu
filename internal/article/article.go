// Package article 跟进详情页取正文：指定选择器优先，readability 兜底，最后净化。
package article

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/Felix2yu/siwu/internal/extract"
	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/transform"
	"github.com/go-shiori/go-readability"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

// Result 是详情页抽取结果。Method 标明正文来自选择器还是 readability，供预览展示。
type Result struct {
	Title       string `json:"title"`
	Date        string `json:"date"`
	ContentHTML string `json:"content_html"`
	ContentText string `json:"content_text"`
	Method      string `json:"method"` // selector | readability
}

var policy = bluemonday.UGCPolicy()

// Extract 从已下载的详情页 HTML 抽取正文。
func Extract(raw []byte, pageURL string, spec model.Article) (Result, error) {
	var out Result
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return out, fmt.Errorf("解析详情页: %w", err)
	}

	if spec.Title != "" {
		if sel, err := extract.Parse(spec.Title); err == nil {
			if nodes, _ := extract.Select(doc, sel); len(nodes) > 0 {
				out.Title = extract.NodeValue(nodes[0], "")
			}
		}
	}
	if spec.Date != "" {
		if sel, err := extract.Parse(spec.Date); err == nil {
			if nodes, _ := extract.Select(doc, sel); len(nodes) > 0 {
				out.Date = extract.NodeValue(nodes[0], "")
			}
		}
	}

	var body string
	if spec.Content != "" {
		if sel, err := extract.Parse(spec.Content); err == nil {
			if nodes, _ := extract.Select(doc, sel); len(nodes) > 0 {
				body = extract.InnerHTML(nodes[0])
			}
		}
	}
	if strings.TrimSpace(body) != "" {
		out.Method = "selector"
	}
	if strings.TrimSpace(body) == "" && spec.Readability {
		if u, err := url.Parse(pageURL); err == nil {
			if art, err := readability.FromReader(bytes.NewReader(raw), u); err == nil {
				body = art.Content
				if out.Title == "" {
					out.Title = art.Title
				}
				out.Method = "readability"
			}
		}
	}
	if strings.TrimSpace(body) == "" {
		return out, fmt.Errorf("正文选择器未命中，且 readability 未能识别正文")
	}
	body = policy.Sanitize(body)
	body = absolutizeHTML(body, pageURL)
	out.ContentHTML = body
	out.ContentText = transform.RemoveTags(body)

	max := spec.MaxChars
	if max > 0 && len([]rune(out.ContentText)) > max {
		rs := []rune(out.ContentText)
		out.ContentText = strings.TrimSpace(string(rs[:max])) + "…"
	}
	return out, nil
}

// absolutizeHTML 把正文里的相对链接与图片补全成绝对地址，否则阅读器里点不动。
func absolutizeHTML(fragment, base string) string {
	if base == "" || !strings.Contains(fragment, "href=") && !strings.Contains(fragment, "src=") {
		return fragment
	}
	b, err := url.Parse(base)
	if err != nil {
		return fragment
	}
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return fragment
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for i, a := range n.Attr {
				switch a.Key {
				case "href", "src":
					if strings.HasPrefix(a.Val, "http://") || strings.HasPrefix(a.Val, "https://") ||
						strings.HasPrefix(a.Val, "data:") || strings.HasPrefix(a.Val, "//") || a.Val == "" {
						continue
					}
					if r, err := url.Parse(a.Val); err == nil {
						n.Attr[i].Val = b.ResolveReference(r).String()
					}
				case "data-src":
					if n.Attr[i].Val != "" && !strings.HasPrefix(n.Attr[i].Val, "data:") {
						// 懒加载图：把 data-src 提成 src，顺手删掉占位
						if r, err := url.Parse(n.Attr[i].Val); err == nil {
							n.Attr = append(n.Attr, html.Attribute{Key: "src", Val: b.ResolveReference(r).String()})
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	// readability 的 Content 是整篇文档（含 html/head/body），只保留 body 的内容，
	// 否则 RSS 正文里会多出 <head></head><body> 这种壳。
	root := doc
	var body *html.Node
	var findBody func(*html.Node)
	findBody = func(n *html.Node) {
		if body != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBody(c)
		}
	}
	findBody(doc)
	if body != nil {
		root = body
	}
	var b2 strings.Builder
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "head" || c.Data == "script" || c.Data == "style") {
			continue
		}
		_ = html.Render(&b2, c)
	}
	return strings.TrimSpace(b2.String())
}
