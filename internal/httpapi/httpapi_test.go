package httpapi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/siwu/internal/events"
	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/notify"
	"github.com/Felix2yu/siwu/internal/scheduler"
	"github.com/Felix2yu/siwu/internal/secretbox"
	"github.com/Felix2yu/siwu/internal/store"
)

func testServer(t *testing.T, feedToken string) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	box, err := secretbox.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := &Server{
		Store: st, Box: box, Log: log, Bus: events.New(),
		Notifier: notify.New(st, box, log),
		BaseURL:  "https://siwu.example", FeedToken: feedToken, Version: "test",
	}
	srv.Scheduler = scheduler.New(nil, st, log, 1)
	return srv, st
}

func seed(t *testing.T, st *store.Store) model.Source {
	t.Helper()
	src := model.Source{Slug: "news", Name: "新闻", Kind: model.KindHTML, URL: "https://example.com", Enabled: true}
	src.Defaults()
	if err := st.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"第一条", "第二条", "第三条"} {
		e := model.Entry{
			SourceID: src.ID, EntryKey: title, GUID: title, Title: title,
			Link:        "https://example.com/" + title,
			ContentHTML: "<p>" + title + " 的正文</p>",
			PublishedAt: time.Now().Add(-time.Duration(i) * time.Hour).Unix(),
			FirstSeenAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
			ContentHash: title,
		}
		if _, _, err := st.UpsertEntry(&e, "update"); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func do(t *testing.T, srv *Server, path string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Result()
}

func putSource(t *testing.T, srv *Server, src model.Source) model.Source {
	t.Helper()
	body, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/sources/%d", src.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存源应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	var got model.Source
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestUpdateSourceReschedulesOnPolicyChange(t *testing.T) {
	srv, st := testServer(t, "")
	src := model.Source{
		Name: "新闻", Kind: model.KindHTML, URL: "https://example.com", Enabled: true,
		Extract: model.Extract{Item: "article", Fields: map[string]model.Field{"title": {Sel: "h2"}}},
		Policy:  model.Policy{IntervalSec: 3600},
	}
	if err := st.SaveSource(&src); err != nil {
		t.Fatal(err)
	}

	// 改间隔后应按新间隔从保存时刻重排
	src.Policy.IntervalSec = 600
	start := time.Now()
	first := putSource(t, srv, src)
	wait := time.Unix(first.NextRunAt, 0).Sub(start)
	if wait < 540*time.Second || wait > 660*time.Second {
		t.Fatalf("改间隔后下次运行应在 9~11 分钟后，实际 %s", wait)
	}
	stored, err := st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.NextRunAt != first.NextRunAt {
		t.Fatalf("下次运行时间未落库：响应 %d，库内 %d", first.NextRunAt, stored.NextRunAt)
	}
	if stored.Policy.IntervalSec != 600 {
		t.Fatalf("间隔未落库：期望 600，实际 %d", stored.Policy.IntervalSec)
	}

	// 只改名字不应重排
	src.Name = "新闻（改名）"
	second := putSource(t, srv, src)
	if second.NextRunAt != first.NextRunAt {
		t.Fatalf("未改调度却被重排：%d -> %d", first.NextRunAt, second.NextRunAt)
	}
}

func TestFeedTokenAndConditional(t *testing.T) {
	srv, st := testServer(t, "s3cret")
	seed(t, st)

	if resp := do(t, srv, "/f/news.xml", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("缺令牌应 401，实际 %d", resp.StatusCode)
	}
	resp := do(t, srv, "/f/news.xml?k=s3cret", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("带令牌应 200，实际 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<item>") {
		t.Fatal("RSS 未包含条目")
	}
	etag := resp.Header.Get("ETag")
	if etag == "" || resp.Header.Get("Last-Modified") == "" {
		t.Fatal("订阅应带 ETag 与 Last-Modified")
	}
	if resp := do(t, srv, "/f/news.xml?k=s3cret", map[string]string{"If-None-Match": etag}); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("ETag 命中应 304，实际 %d", resp.StatusCode)
	}
}

func TestFeedGzip(t *testing.T) {
	srv, st := testServer(t, "")
	seed(t, st)
	resp := do(t, srv, "/f/news.xml", map[string]string{"Accept-Encoding": "gzip"})
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("应返回 gzip，实际 %q", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "第一条") {
		t.Fatal("解压后内容不完整")
	}
	if resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Error("压缩响应应带 Vary: Accept-Encoding")
	}
}

func TestOPMLExport(t *testing.T) {
	srv, st := testServer(t, "tk")
	seed(t, st)
	resp := do(t, srv, "/api/export/opml", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OPML 导出应 200，实际 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, `<opml version="2.0">`) {
		t.Fatalf("不是合法 OPML 头: %s", text[:min(120, len(text))])
	}
	if !strings.Contains(text, `xmlUrl="https://siwu.example/f/news.xml?k=tk"`) {
		t.Errorf("xmlUrl 应带令牌与完整地址: %s", text)
	}
}

func TestStats(t *testing.T) {
	srv, st := testServer(t, "")
	seed(t, st)
	resp := do(t, srv, "/api/stats", nil)
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{`"sources":1`, `"entries":3`, `"enabled":1`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("统计缺少 %s: %s", want, body)
		}
	}
}

func TestSettingsAPI(t *testing.T) {
	srv, _ := testServer(t, "")
	req := httptest.NewRequest(http.MethodPut, "/api/settings",
		strings.NewReader(`{"defaults":{"interval_sec":900,"max_items":20,"retention_days":7,"update_mode":"archive","article_enabled":true}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存设置应 200，实际 %d: %s", rec.Code, rec.Body.String())
	}

	resp := do(t, srv, "/api/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读取设置应 200，实际 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	for _, want := range []string{
		`"interval_sec":900`, `"max_items":20`, `"update_mode":"archive"`,
		`"article_enabled":true`, `"version":"test"`, `"base_url":"https://siwu.example"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("设置响应缺少 %s: %s", want, text)
		}
	}

	// 越界值应在服务端收敛，而不是原样落库
	req = httptest.NewRequest(http.MethodPut, "/api/settings",
		strings.NewReader(`{"defaults":{"interval_sec":0,"max_items":0,"update_mode":"nope"}}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	body, _ = io.ReadAll(rec.Result().Body)
	if !strings.Contains(string(body), fmt.Sprintf(`"interval_sec":%d`, model.DefaultIntervalSec)) ||
		!strings.Contains(string(body), `"update_mode":"update"`) {
		t.Errorf("越界值未收敛: %s", body)
	}
}

func TestSSEStreamsEvents(t *testing.T) {
	srv, st := testServer(t, "")
	seed(t, st)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type 不对: %q", ct)
	}
	reader := bufio.NewReader(resp.Body)
	// 等订阅建立
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "connected") {
		t.Fatalf("首行应为连接确认，实际 %q", line)
	}
	// 等一小会儿确保 Subscribe 已完成
	time.Sleep(100 * time.Millisecond)
	srv.Bus.Publish(events.Event{Type: "run.start", SourceName: "新闻", Slug: "news"})

	got := ""
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		l, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		got += l
		// 一个事件以空行结束：等读完 event/data 两行再断言
		if strings.Contains(got, "run.start") && strings.Contains(got, "\n\n") {
			break
		}
	}
	if !strings.Contains(got, "event: run.start") || !strings.Contains(got, `"slug":"news"`) {
		t.Fatalf("未收到预期事件: %q", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
