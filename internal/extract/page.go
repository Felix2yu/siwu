package extract

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// TitleOf 取文档 <title>。
func TitleOf(raw []byte) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var (
		title string
		walk  func(*html.Node)
	)
	walk = func(n *html.Node) {
		if title != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			title = strings.TrimSpace(n.FirstChild.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return title
}
