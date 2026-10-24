package fetch

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFilterAcceptEncoding(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"gzip, deflate, br", "gzip, deflate"},
		{"br", ""},
		{"gzip;q=0.8, br;q=0.5", "gzip;q=0.8"},
		{"identity", "identity"},
		{"GZIP", "GZIP"},
	}
	for _, c := range cases {
		if got := filterAcceptEncoding(c.in); got != c.want {
			t.Errorf("filterAcceptEncoding(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWantsCompression(t *testing.T) {
	if wantsCompression("identity") {
		t.Error("identity 不应要求解压")
	}
	if !wantsCompression("gzip, deflate") {
		t.Error("gzip/deflate 应要求解压")
	}
}

func TestDecodeBody(t *testing.T) {
	const want = "<html>压缩过的页面</html>"

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(want)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var zl bytes.Buffer
	zz := zlib.NewWriter(&zl)
	if _, err := zz.Write([]byte(want)); err != nil {
		t.Fatal(err)
	}
	if err := zz.Close(); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name     string
		encoding string
		body     []byte
	}{
		{"gzip", "gzip", gz.Bytes()},
		{"x-gzip", "x-gzip", gz.Bytes()},
		{"deflate", "deflate", zl.Bytes()},
		{"identity", "identity", []byte(want)},
		{"empty", "", []byte(want)},
	} {
		got, err := decodeBody(c.body, c.encoding, 1<<20)
		if err != nil {
			t.Fatalf("%s: 解压失败: %v", c.name, err)
		}
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", c.name, got, want)
		}
	}

	if _, err := decodeBody([]byte("x"), "br", 1<<20); err == nil {
		t.Error("br 应报不支持，而不是返回乱码")
	}
}

func TestDoCustomAcceptEncoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			// 协商里剔掉了 br，服务端不应再返回 br
			http.Error(w, "missing gzip", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte("<p>ok</p>"))
		_ = zw.Close()
	}))
	defer srv.Close()

	f := New(Options{AllowPrivate: true})
	resp, err := f.Do(context.Background(), Request{
		URL:     srv.URL,
		Headers: map[string]string{"Accept-Encoding": "gzip, deflate, br"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if string(resp.Body) != "<p>ok</p>" {
		t.Fatalf("body = %q，自定义 Accept-Encoding 后没有解压", resp.Body)
	}
}

func TestDoTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	f := New(Options{AllowPrivate: true})
	start := time.Now()
	_, err := f.Do(context.Background(), Request{URL: srv.URL, Timeout: 100 * time.Millisecond})
	if err == nil {
		t.Fatal("超时应当报错，timeout_sec 没有生效")
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("耗时 %v，请求没有按源配置的超时中断", elapsed)
	}
}

func TestBrowserProfile(t *testing.T) {
	p := newBrowserProfile(map[string]string{
		"User-Agent":       "custom-ua",
		"Referer":          "https://example.com/list",
		"Cookie":           "a=1; b=2",
		"Sec-Fetch-Site":   "none",
		"Accept-Encoding":  "gzip, br",
		"X-Requested-With": "XMLHttpRequest",
		"Content-Length":   "10",
	}, map[string]string{"c": "3", "a": "override"})

	if p.UA != "custom-ua" {
		t.Errorf("UA = %q", p.UA)
	}
	if _, ok := p.Extra["Sec-Fetch-Site"]; ok {
		t.Error("sec-* 不应手工注入")
	}
	if _, ok := p.Extra["Accept-Encoding"]; ok {
		t.Error("Accept-Encoding 不应手工注入")
	}
	if _, ok := p.Extra["Content-Length"]; ok {
		t.Error("Content-Length 不应手工注入")
	}
	if p.Extra["Referer"] != "https://example.com/list" || p.Extra["X-Requested-With"] != "XMLHttpRequest" {
		t.Errorf("业务头没有进入待注入集合: %v", p.Extra)
	}
	cookies := map[string]string{}
	for _, c := range p.Cookies {
		cookies[c.Name] = c.Value
	}
	if cookies["a"] != "override" || cookies["b"] != "2" || cookies["c"] != "3" {
		t.Errorf("cookies = %v，字段应覆盖请求头里的同名 Cookie", cookies)
	}
}

func TestMergeAPI(t *testing.T) {
	p := newBrowserProfile(nil, nil)
	p.mergeAPI(map[string]string{
		"Referer": "https://example.com/",
		"Origin":  "https://example.com",
		"X-Token": "t",
		"Cookie":  "sid=1",
	})
	if p.Extra["Referer"] == "" || p.Extra["Origin"] == "" {
		t.Errorf("fetch 禁止的头应提到 tab 级: %v", p.Extra)
	}
	if _, ok := p.Extra["X-Token"]; ok {
		t.Error("能在 fetch init 里设的头不应重复注入")
	}
	if len(p.Cookies) != 1 || p.Cookies[0].Name != "sid" {
		t.Errorf("cookies = %v", p.Cookies)
	}
}

func TestCookieOrigins(t *testing.T) {
	got := cookieOrigins([]string{
		"https://a.com/x/y",
		"https://a.com/z",
		"http://b.com:8080/p?q=1",
		"ftp://c.com/file",
		"",
	})
	want := []string{"https://a.com", "http://b.com:8080"}
	if len(got) != len(want) {
		t.Fatalf("origins = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("origins[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
