package pipeline

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/extract"
)

func compile(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}

type rssDoc struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
			GUID        string `xml:"guid"`
			Category    string `xml:"category"`
			Content     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomDoc struct {
	Title   string `xml:"title"`
	Updated string `xml:"updated"`
	Entries []struct {
		Title   string `xml:"title"`
		ID      string `xml:"id"`
		Summary string `xml:"summary"`
		Content string `xml:"content"`
		Pub     string `xml:"published"`
		Updated string `xml:"updated"`
		Links   []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

// parseFeed 解析 RSS 2.0 / Atom：同一个订阅源既可能是哪种格式，靠内容判断。
func parseFeed(raw []byte) ([]extract.RawItem, string, []string, error) {
	var warnings []string
	var rss rssDoc
	if err := xml.Unmarshal(raw, &rss); err == nil && len(rss.Channel.Items) > 0 {
		items := make([]extract.RawItem, 0, len(rss.Channel.Items))
		for _, it := range rss.Channel.Items {
			items = append(items, extract.RawItem{
				Title:    strings.TrimSpace(it.Title),
				Link:     strings.TrimSpace(it.Link),
				Summary:  strings.TrimSpace(it.Description),
				Date:     strings.TrimSpace(it.PubDate),
				GUID:     strings.TrimSpace(it.GUID),
				Category: strings.TrimSpace(it.Category),
				Content:  strings.TrimSpace(it.Content),
			})
		}
		return items, strings.TrimSpace(rss.Channel.Title), warnings, nil
	}
	var atom atomDoc
	if err := xml.Unmarshal(raw, &atom); err == nil && len(atom.Entries) > 0 {
		items := make([]extract.RawItem, 0, len(atom.Entries))
		for _, e := range atom.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			date := e.Pub
			if date == "" {
				date = e.Updated
			}
			items = append(items, extract.RawItem{
				Title:   strings.TrimSpace(e.Title),
				Link:    link,
				Summary: strings.TrimSpace(e.Summary),
				Content: strings.TrimSpace(e.Content),
				Date:    strings.TrimSpace(date),
				GUID:    strings.TrimSpace(e.ID),
			})
		}
		return items, strings.TrimSpace(atom.Title), warnings, nil
	}
	return nil, "", warnings, fmt.Errorf("既不是 RSS 也不是 Atom，或订阅源为空")
}

// formatTime 是内部日期格式化的小工具，保留以便测试输出可读。
func formatTime(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format(time.RFC3339)
}
