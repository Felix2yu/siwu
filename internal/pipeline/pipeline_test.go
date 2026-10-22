package pipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Felix2yu/siwu/internal/fetch"
	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/store"
)

func testDeps(t *testing.T) *Deps {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Deps{
		Store:    st,
		Fetcher:  fetch.New(fetch.Options{AllowPrivate: true, MaxBytes: 4 << 20}),
		Log:      slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
		Location: time.Local,
	}
}

// pageServer 返回一个可切换响应体的测试站点。
type pageServer struct {
	mu   sync.Mutex
	body string
	srv  *httptest.Server
}

func newPageServer(t *testing.T, body string) *pageServer {
	ps := &pageServer{body: body}
	ps.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		defer ps.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, ps.body)
	}))
	t.Cleanup(ps.srv.Close)
	return ps
}

func (p *pageServer) set(body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body = body
}

func listHTML(items ...[3]string) string {
	var b strings.Builder
	b.WriteString(`<html><body><div id="news"><ul>`)
	for _, it := range items {
		fmt.Fprintf(&b, `<li class="row"><a href="%s">%s <span class="tag">NEW</span></a><span class="date">%s</span></li>`,
			it[0], it[1], it[2])
	}
	b.WriteString(`</ul></div></body></html>`)
	return b.String()
}

func listSource(id int64, url string) model.Source {
	src := model.Source{
		ID: id, Slug: "t", Name: "测试列表", Kind: model.KindHTML, URL: url,
		Enabled: true,
		Extract: model.Extract{
			Item: "css:div#news li.row",
			Fields: map[string]model.Field{
				"title": {Sel: "css:a"},
				"link":  {Sel: "css:a", Attr: "href", Absolute: true},
				"date":  {Sel: "css:.date", Transforms: []string{"d"}},
			},
		},
		Rules:  []model.Rule{{ID: "d", Op: "date_parse", Layouts: []string{"2006-01-02"}, TZ: "Asia/Shanghai"}},
		Policy: model.Policy{IntervalSec: 3600, MaxItems: 50, UpdateMode: "update"},
	}
	src.Defaults()
	return src
}

func TestListExtractDedupeAndClean(t *testing.T) {
	ps := newPageServer(t, listHTML(
		[3]string{"/a1", "标题一", "2026-10-20"},
		[3]string{"/a2", "标题二", "2026-10-21"},
	))
	d := testDeps(t)
	src := listSource(0, ps.srv.URL+"/list")
	src.Rules = append(src.Rules, model.Rule{
		ID: "strip-new", Target: []string{"title"}, Op: "regex_replace",
		Pattern: `\s*NEW\s*$`, Replace: "",
	})
	if err := d.Store.SaveSource(&src); err != nil {
		t.Fatal(err)
	}

	out, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatalf("首次运行失败: %v", err)
	}
	if out.ItemsFound != 2 || len(out.NewEntries) != 2 {
		t.Fatalf("应命中并新增 2 条，实际 found=%d new=%d", out.ItemsFound, len(out.NewEntries))
	}
	for _, e := range out.NewEntries {
		if strings.Contains(e.Title, "NEW") {
			t.Errorf("清洗规则未生效: %q", e.Title)
		}
		if !strings.HasPrefix(e.Link, ps.srv.URL) {
			t.Errorf("链接未绝对化: %q", e.Link)
		}
		if e.PublishedAt == 0 {
			t.Errorf("日期未解析: %+v", e)
		}
	}

	// 第二次：内容完全没变，应零新增零更新
	out2, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.NewEntries) != 0 || out2.Updated != 0 {
		t.Fatalf("重复运行不应产生变更，实际 new=%d updated=%d", len(out2.NewEntries), out2.Updated)
	}
	if out2.ItemsFound != 2 {
		t.Fatalf("重复运行仍应命中 2 条，实际 %d", out2.ItemsFound)
	}
}

func TestNormalizeSuppressesCounterNoise(t *testing.T) {
	render := func(views string) string {
		return `<html><body><div id="news"><ul>` +
			`<li class="row"><a href="/a1">公告</a><span class="date">2026-10-20</span>` +
			`<span class="views">浏览 ` + views + ` 次</span></li>` +
			`</ul></div></body></html>`
	}
	ps := newPageServer(t, render("100"))
	d := testDeps(t)
	src := listSource(0, ps.srv.URL)
	src.Extract.Fields["summary"] = model.Field{Sel: "css:.views"}
	src.Extract.Normalize = []string{`浏览 \d+ 次`}
	if err := d.Store.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Execute(context.Background(), src, "test", false); err != nil {
		t.Fatal(err)
	}

	ps.set(render("9999"))
	out, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Updated != 0 {
		t.Fatalf("只有访问量变化时不应记为更新，实际 updated=%d", out.Updated)
	}
}

func TestExcludeSelectorSkipsAd(t *testing.T) {
	body := `<html><body><div id="news"><ul>
<li class="row"><a href="/a1">正常一</a><span class="date">2026-10-20</span></li>
<li class="row ad"><a href="/ad">广告位</a><span class="date">2026-10-20</span></li>
<li class="row"><a href="/a2">正常二</a><span class="date">2026-10-20</span></li>
</ul></div></body></html>`
	ps := newPageServer(t, body)
	d := testDeps(t)
	src := listSource(0, ps.srv.URL)
	src.Extract.Exclude = []string{"css:li.ad"}
	if err := d.Store.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	out, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if out.ItemsFound != 2 {
		t.Fatalf("排除广告后应只剩 2 条，实际 %d", out.ItemsFound)
	}
	for _, e := range out.NewEntries {
		if e.Title == "广告位" {
			t.Fatal("广告条目未被排除")
		}
	}
}

func TestPageSnapshotThreshold(t *testing.T) {
	page := func(lines ...string) string {
		return `<html><body><div id="box"><p>` + strings.Join(lines, "</p><p>") + `</p></div></body></html>`
	}
	ps := newPageServer(t, page("甲", "乙", "丙"))
	d := testDeps(t)
	src := model.Source{
		Slug: "snap", Name: "页面快照", Kind: model.KindPage, URL: ps.srv.URL,
		Enabled: true,
		Extract: model.Extract{Scope: "css:div#box"},
		Policy:  model.Policy{IntervalSec: 600, MinChangedLines: 3, UpdateMode: "archive"},
	}
	src.Defaults()
	if err := d.Store.SaveSource(&src); err != nil {
		t.Fatal(err)
	}

	out, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.NewEntries) != 1 {
		t.Fatalf("首次快照应产出 1 条，实际 %d", len(out.NewEntries))
	}
	if !strings.Contains(out.NewEntries[0].Title, "首次快照") {
		t.Errorf("首次标题不正确: %q", out.NewEntries[0].Title)
	}

	// 只改 1 行：低于阈值，不产出
	ps.set(page("甲", "乙", "丙", "丁"))
	out, err = d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.NewEntries) != 0 {
		t.Fatalf("变化低于阈值不应产出条目，实际 %d 条", len(out.NewEntries))
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "未达阈值") {
			found = true
		}
	}
	if !found {
		t.Errorf("应给出未达阈值的告警，实际 %v", out.Warnings)
	}

	// 再改 3 行：累计超过阈值，产出
	ps.set(page("甲", "乙", "丙", "丁", "戊", "己"))
	out, err = d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.NewEntries) != 1 {
		t.Fatalf("累计变化超过阈值应产出 1 条，实际 %d", len(out.NewEntries))
	}
	if !strings.Contains(out.NewEntries[0].Title, "内容变化") {
		t.Errorf("变化标题不正确: %q", out.NewEntries[0].Title)
	}
}

func TestRSSForward(t *testing.T) {
	rss := `<?xml version="1.0"?><rss version="2.0"><channel><title>上游订阅</title>
<item><title>上游一</title><link>https://up.example/1</link><guid>up-1</guid>
<description>摘要一</description><pubDate>Wed, 21 Oct 2026 08:00:00 +0800</pubDate></item>
<item><title>上游二</title><link>https://up.example/2</link><guid>up-2</guid>
<pubDate>Tue, 20 Oct 2026 08:00:00 +0800</pubDate></item>
</channel></rss>`
	ps := newPageServer(t, rss)
	d := testDeps(t)
	src := model.Source{
		Slug: "fwd", Name: "转发源", Kind: model.KindRSS, URL: ps.srv.URL, Enabled: true,
		Policy: model.Policy{IntervalSec: 3600, MaxItems: 50},
	}
	src.Defaults()
	if err := d.Store.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	out, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if out.ItemsFound != 2 || len(out.NewEntries) != 2 {
		t.Fatalf("RSS 转发应入库 2 条，实际 found=%d new=%d", out.ItemsFound, len(out.NewEntries))
	}
	if out.NewEntries[0].Summary == "" {
		t.Error("RSS 的 description 应作为摘要保留")
	}
	// 再跑一次不应重复
	out2, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.NewEntries) != 0 {
		t.Fatalf("RSS 转发不应重复入库，实际 %d", len(out2.NewEntries))
	}
}

func TestFilterIncludeExclude(t *testing.T) {
	ps := newPageServer(t, listHTML(
		[3]string{"/a1", "招聘公告", "2026-10-20"},
		[3]string{"/a2", "讲座预告", "2026-10-21"},
	))
	d := testDeps(t)
	src := listSource(0, ps.srv.URL)
	src.Filter = model.Filter{Exclude: []string{"招聘"}, Field: "title"}
	if err := d.Store.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	out, err := d.Execute(context.Background(), src, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.NewEntries) != 1 || !strings.Contains(out.NewEntries[0].Title, "讲座预告") {
		t.Fatalf("关键词排除未生效: %+v", out.NewEntries)
	}
}
