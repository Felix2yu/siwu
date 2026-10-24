package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
)

// 浏览器通道的注入行为只能对着真实浏览器验证：设 SIWU_TEST_CHROME 指向
// chrome-headless-shell（或 Chrome）可执行文件后运行；CI 默认跳过。
func TestBrowserProfileInjection(t *testing.T) {
	chrome := os.Getenv("SIWU_TEST_CHROME")
	if chrome == "" {
		t.Skip("未设置 SIWU_TEST_CHROME，跳过浏览器通道集成测试")
	}

	var (
		mu      sync.Mutex
		pageHdr http.Header
		apiHdr  http.Header
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pageHdr = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body>ok</body></html>")
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		apiHdr = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(Options{AllowPrivate: true, ChromePath: chrome})
	src := model.Source{
		Kind:   model.KindJSON,
		Engine: model.EngineBrowser,
		URL:    srv.URL + "/page",
		Request: model.Request{
			Headers: map[string]string{
				"User-Agent": "siwu-test-ua",
				"Referer":    srv.URL + "/list",
				"X-Test":     "page-level",
			},
			Cookies:    map[string]string{"sid": "abc"},
			WaitMS:     100,
			TimeoutSec: 20,
			API: &model.APISpec{
				URL:       srv.URL + "/api",
				Method:    http.MethodGet,
				InBrowser: true,
				WaitMS:    100,
				Headers:   map[string]string{"X-Token": "t", "Referer": srv.URL + "/api-ref"},
			},
		},
	}

	resp, err := f.API(context.Background(), src)
	if err != nil {
		t.Fatalf("页面内取接口失败: %v", err)
	}
	if !strings.Contains(string(resp.Body), `"ok":true`) {
		t.Fatalf("body = %q", resp.Body)
	}

	mu.Lock()
	defer mu.Unlock()
	if pageHdr == nil || apiHdr == nil {
		t.Fatal("站点没有收到请求")
	}
	if got := pageHdr.Get("User-Agent"); got != "siwu-test-ua" {
		t.Errorf("页面 UA = %q，源配置的 UA 没有生效", got)
	}
	if got := pageHdr.Get("Referer"); got != srv.URL+"/list" {
		t.Errorf("页面 Referer = %q", got)
	}
	if got := pageHdr.Get("X-Test"); got != "page-level" {
		t.Errorf("页面 X-Test = %q", got)
	}
	if got := pageHdr.Get("Cookie"); !strings.Contains(got, "sid=abc") {
		t.Errorf("页面 Cookie = %q，源配置的 Cookie 没有注入", got)
	}
	if got := apiHdr.Get("X-Token"); got != "t" {
		t.Errorf("接口 X-Token = %q，fetch init 没有带上接口级请求头", got)
	}
	if got := apiHdr.Get("Cookie"); !strings.Contains(got, "sid=abc") {
		t.Errorf("接口 Cookie = %q，跨请求没带上源配置的 Cookie", got)
	}
	if got := apiHdr.Get("Referer"); got != srv.URL+"/api-ref" {
		t.Errorf("接口 Referer = %q，接口级头没有覆盖 tab 级", got)
	}
}
