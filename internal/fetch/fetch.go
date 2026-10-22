// Package fetch 负责把页面/接口取回来：HTTP 通道带字符集嗅探与体积上限，
// browser 通道走 CDP（可连外置 headless-shell，也可用本机 Chrome）。
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
	"golang.org/x/net/html/charset"
)

// DefaultUA 伪装成常规桌面浏览器，减少被简单 UA 规则挡掉的概率。
const DefaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// Options 构造 Fetcher。
type Options struct {
	AllowPrivate bool   // 允许访问私网/回环（默认拒绝，防 SSRF）
	BrowserWS    string // 全局 CDP 地址，如 ws://browser:9222
	ChromePath   string // 本机 Chrome/Chromium 可执行文件路径（不配则自动查找）
	MaxBytes     int64
}

// Fetcher 是无状态的可复用抓取器。
type Fetcher struct {
	opts   Options
	client *http.Client
}

// New 构造 Fetcher。
func New(opts Options) *Fetcher {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 8 << 20
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          64,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &Fetcher{
		opts: opts,
		client: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("重定向次数过多（>5）")
				}
				return nil
			},
		},
	}
}

// Response 是抓取结果。
type Response struct {
	Body        []byte
	ContentType string
	Status      int
	FinalURL    string
}

// Do 执行一次抓取（含重试与 SSRF 校验）。
func (f *Fetcher) Do(ctx context.Context, req Request) (Response, error) {
	if err := f.checkURL(req.URL); err != nil {
		return Response{}, err
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * 400 * time.Millisecond):
			}
		}
		resp, err := f.once(ctx, method, req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.Status >= 500 {
			lastErr = fmt.Errorf("服务端返回 %d", resp.Status)
			continue
		}
		return resp, nil
	}
	return Response{}, lastErr
}

func (f *Fetcher) once(ctx context.Context, method string, req Request) (Response, error) {
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return Response{}, err
	}
	hr.Header.Set("User-Agent", DefaultUA)
	hr.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	hr.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	for k, v := range req.Headers {
		hr.Header.Set(k, v)
	}
	for k, v := range req.Cookies {
		hr.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if req.Proxy != "" {
		pu, err := url.Parse(req.Proxy)
		if err != nil {
			return Response{}, fmt.Errorf("代理地址非法: %w", err)
		}
		if tr, ok := f.client.Transport.(*http.Transport); ok {
			clone := tr.Clone()
			clone.Proxy = http.ProxyURL(pu)
			client := *f.client
			client.Transport = clone
			resp, err := client.Do(hr)
			if err != nil {
				return Response{}, err
			}
			return f.read(resp)
		}
	}
	resp, err := f.client.Do(hr)
	if err != nil {
		return Response{}, err
	}
	return f.read(resp)
}

func (f *Fetcher) read(resp *http.Response) (Response, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, f.opts.MaxBytes))
	if err != nil {
		return Response{}, err
	}
	ct := resp.Header.Get("Content-Type")
	out := Response{ContentType: ct, Status: resp.StatusCode, FinalURL: resp.Request.URL.String()}
	// 字符集：优先响应头，其次 <meta charset>，最终兜到 UTF-8
	if isTextual(ct) {
		r, err := charset.NewReader(bytes.NewReader(raw), ct)
		if err == nil {
			if decoded, derr := io.ReadAll(io.LimitReader(r, f.opts.MaxBytes)); derr == nil {
				raw = decoded
			}
		}
	}
	out.Body = raw
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return out, nil
}

func isTextual(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "text/") || strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") || ct == ""
}

// checkURL 拒绝非 http(s) 与私网地址（除非显式放开）。
func (f *Fetcher) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("地址非法: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 http/https，收到 %q", u.Scheme)
	}
	if f.opts.AllowPrivate {
		return nil
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("地址缺少主机名")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil // DNS 解析失败交给实际请求报错
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("%s 解析到内网地址 %s，已拒绝（SIWU_ALLOW_PRIVATE=1 可放开）", host, ip)
		}
	}
	return nil
}

// Request 是抓一次的完整参数。
type Request struct {
	URL     string
	Method  string
	Headers map[string]string
	Cookies map[string]string
	Proxy   string
	Body    string
	Timeout time.Duration
}

// FromSource 按源的配置构造 HTTP 请求参数。
func FromSource(src model.Source, target string) Request {
	timeout := time.Duration(src.Request.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return Request{
		URL:     target,
		Method:  http.MethodGet,
		Headers: src.Request.Headers,
		Cookies: src.Request.Cookies,
		Proxy:   src.Request.Proxy,
		Timeout: timeout,
	}
}

// WithTimeout 返回带超时的 ctx。
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = 20 * time.Second
	}
	return context.WithTimeout(ctx, d)
}
