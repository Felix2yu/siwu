package feedgen

import (
	"strings"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
)

func sample() (model.Source, []model.Entry) {
	src := model.Source{Slug: "visitsz", Name: "苏州文旅 · 最新资讯", Kind: model.KindHTML,
		URL: "https://visitsz.wglj.suzhou.com.cn/news.aspx"}
	entries := []model.Entry{
		{GUID: "https://example.com/a", Title: "公告 A", Link: "https://example.com/a",
			Summary: "摘要 A", ContentHTML: "<p>正文 <b>A</b></p>", PublishedAt: 1770000000, FirstSeenAt: 1770000000},
		{GUID: "https://example.com/b", Title: "公告 B", Link: "https://example.com/b",
			PublishedAt: 1769000000, FirstSeenAt: 1769000000},
	}
	return src, entries
}

func TestRSSHasContentEncoded(t *testing.T) {
	src, entries := sample()
	body, err := Build(src, entries, FormatRSS, "http://localhost:8788/f/visitsz.xml")
	if err != nil {
		t.Fatal(err)
	}
	out := string(body)
	for _, want := range []string{"<rss", "content:encoded", "<guid", "公告 A", "苏州文旅"} {
		if !strings.Contains(out, want) {
			t.Errorf("RSS 缺少 %q", want)
		}
	}
}

func TestAtomAndJSONFeed(t *testing.T) {
	src, entries := sample()
	atom, err := Build(src, entries, FormatAtom, "http://localhost:8788/f/visitsz.atom")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(atom), "<feed") {
		t.Error("Atom 输出异常")
	}
	js, err := Build(src, entries, FormatJSONFeed, "http://localhost:8788/f/visitsz.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"jsonfeed.org/version/1.1", "content_html", "公告 A"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("JSON Feed 缺少 %q", want)
		}
	}
}

func TestETagStable(t *testing.T) {
	src, entries := sample()
	a, _ := Build(src, entries, FormatRSS, "x")
	b, _ := Build(src, entries, FormatRSS, "x")
	if ETag(a) != ETag(b) {
		t.Fatal("同内容 ETag 应稳定")
	}
}
