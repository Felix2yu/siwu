// Package feedgen 把条目渲染成 RSS 2.0 / Atom / JSON Feed。
package feedgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/gorilla/feeds"
)

// Format 是订阅输出格式。
type Format string

const (
	FormatRSS      Format = "rss"
	FormatAtom     Format = "atom"
	FormatJSONFeed Format = "json"
)

// Build 渲染订阅内容。
func Build(src model.Source, entries []model.Entry, format Format, feedURL string) ([]byte, error) {
	if format == FormatJSONFeed {
		return buildJSON(src, entries, feedURL)
	}
	f := &feeds.Feed{
		Title:       feedTitle(src),
		Link:        &feeds.Link{Href: src.URL},
		Description: feedDescription(src),
		Created:     time.Unix(firstSeen(entries), 0),
		Updated:     time.Unix(lastUpdated(entries), 0),
	}
	for _, e := range entries {
		item := &feeds.Item{
			Title:   fallback(e.Title, "(无标题)"),
			Link:    &feeds.Link{Href: e.Link},
			Id:      e.GUID,
			Created: time.Unix(publishedAt(e), 0),
			Updated: time.Unix(publishedAt(e), 0),
		}
		item.Description = e.Summary
		if item.Description == "" {
			item.Description = truncateRunes(e.ContentText, 200)
		}
		item.Content = e.ContentHTML
		f.Items = append(f.Items, item)
	}
	var (
		out string
		err error
	)
	switch format {
	case FormatAtom:
		out, err = f.ToAtom()
	default:
		out, err = f.ToRss()
	}
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

func buildJSON(src model.Source, entries []model.Entry, feedURL string) ([]byte, error) {
	type item struct {
		ID            string   `json:"id"`
		URL           string   `json:"url,omitempty"`
		Title         string   `json:"title,omitempty"`
		Summary       string   `json:"summary,omitempty"`
		ContentHTML   string   `json:"content_html,omitempty"`
		DatePublished string   `json:"date_published,omitempty"`
		Tags          []string `json:"tags,omitempty"`
	}
	doc := struct {
		Version     string `json:"version"`
		Title       string `json:"title"`
		HomePageURL string `json:"home_page_url"`
		FeedURL     string `json:"feed_url"`
		Description string `json:"description,omitempty"`
		Items       []item `json:"items"`
	}{
		Version:     "https://jsonfeed.org/version/1.1",
		Title:       feedTitle(src),
		HomePageURL: src.URL,
		FeedURL:     feedURL,
		Description: feedDescription(src),
	}
	for _, e := range entries {
		it := item{
			ID:          e.GUID,
			URL:         e.Link,
			Title:       e.Title,
			Summary:     e.Summary,
			ContentHTML: e.ContentHTML,
		}
		if ts := publishedAt(e); ts > 0 {
			it.DatePublished = time.Unix(ts, 0).UTC().Format(time.RFC3339)
		}
		if e.Category != "" {
			it.Tags = []string{e.Category}
		}
		doc.Items = append(doc.Items, it)
	}
	return json.Marshal(doc)
}

// ETag 用内容哈希做条件请求的标识。
func ETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// LastModified 取订阅内容的最新时间，用于 If-Modified-Since。
// 没有条目时退回当前时间，避免发出 1970 的 Last-Modified。
func LastModified(entries []model.Entry) time.Time {
	if len(entries) == 0 {
		return time.Now()
	}
	var max int64
	for _, e := range entries {
		for _, ts := range []int64{publishedAt(e), e.UpdatedAt, e.FirstSeenAt} {
			if ts > max {
				max = ts
			}
		}
	}
	if max <= 0 {
		return time.Now()
	}
	return time.Unix(max, 0)
}

func feedTitle(src model.Source) string {
	if src.Name != "" {
		return src.Name
	}
	return src.Slug
}

func feedDescription(src model.Source) string {
	return fmt.Sprintf("司寤监控 · %s · %s", kindLabel(src.Kind), src.URL)
}

func kindLabel(k model.Kind) string {
	switch k {
	case model.KindPage:
		return "页面快照"
	case model.KindJSON:
		return "接口"
	case model.KindRSS:
		return "订阅转发"
	default:
		return "列表"
	}
}

func publishedAt(e model.Entry) int64 {
	if e.PublishedAt > 0 {
		return e.PublishedAt
	}
	return e.FirstSeenAt
}

func firstSeen(entries []model.Entry) int64 {
	if len(entries) == 0 {
		return time.Now().Unix()
	}
	return entries[len(entries)-1].FirstSeenAt
}

func lastUpdated(entries []model.Entry) int64 {
	if len(entries) == 0 {
		return time.Now().Unix()
	}
	return publishedAt(entries[0])
}

func fallback(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
