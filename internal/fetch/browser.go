package fetch

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

// Page 按源配置的 engine 抓页面。
func (f *Fetcher) Page(ctx context.Context, src model.Source) (Response, error) {
	if src.Engine == model.EngineBrowser {
		return f.browserPage(ctx, src)
	}
	return f.Do(ctx, FromSource(src, src.URL))
}

// API 抓取 json 型源配置的接口。InBrowser 时在页面上下文里发请求，
// 从而带上 WAF 运行期生成的签名头与 Cookie。
func (f *Fetcher) API(ctx context.Context, src model.Source) (Response, error) {
	if src.Request.API == nil {
		return Response{}, fmt.Errorf("json 型源缺少 api 配置")
	}
	if src.Request.API.InBrowser || src.Engine == model.EngineBrowser {
		return f.browserAPI(ctx, src)
	}
	req := FromSource(src, src.Request.API.URL)
	req.Method = src.Request.API.Method
	req.Body = src.Request.API.Body
	if len(src.Request.API.Headers) > 0 {
		merged := map[string]string{}
		for k, v := range src.Request.Headers {
			merged[k] = v
		}
		for k, v := range src.Request.API.Headers {
			merged[k] = v
		}
		req.Headers = merged
	}
	return f.Do(ctx, req)
}

func (f *Fetcher) browserPage(ctx context.Context, src model.Source) (Response, error) {
	timeout := time.Duration(src.Request.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tabCtx, stop, err := f.tab(ctx, src)
	if err != nil {
		return Response{}, err
	}
	defer stop()

	if err := chromedp.Do(tabCtx, chromedp.Navigate(src.URL)); err != nil {
		return Response{}, fmt.Errorf("浏览器打开页面失败: %w", err)
	}
	if src.Request.WaitSelector != "" {
		_ = chromedp.Do(tabCtx, chromedp.WaitReady(chromedp.Search(src.Request.WaitSelector)))
	}
	wait := src.Request.WaitMS
	if wait <= 0 {
		wait = 800
	}
	_ = chromedp.Do(tabCtx, chromedp.Sleep(time.Duration(wait)*time.Millisecond))

	htmlText, err := chromedp.Run(tabCtx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		return Response{}, fmt.Errorf("取渲染后 DOM 失败: %w", err)
	}
	return Response{Body: []byte(htmlText), ContentType: "text/html; charset=utf-8", Status: 200, FinalURL: src.URL}, nil
}

func (f *Fetcher) browserAPI(ctx context.Context, src model.Source) (Response, error) {
	api := src.Request.API
	timeout := time.Duration(src.Request.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 40 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tabCtx, stop, err := f.tab(ctx, src)
	if err != nil {
		return Response{}, err
	}
	defer stop()

	if err := chromedp.Do(tabCtx, chromedp.Navigate(src.URL)); err != nil {
		return Response{}, fmt.Errorf("浏览器打开前置页面失败: %w", err)
	}
	if src.Request.WaitSelector != "" {
		_ = chromedp.Do(tabCtx, chromedp.WaitReady(chromedp.Search(src.Request.WaitSelector)))
	}
	wait := api.WaitMS
	if wait <= 0 {
		wait = src.Request.WaitMS
	}
	if wait <= 0 {
		wait = 4000
	}
	_ = chromedp.Do(tabCtx, chromedp.Sleep(time.Duration(wait)*time.Millisecond))

	method := api.Method
	if method == "" {
		method = http.MethodGet
	}
	headers := api.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return Response{}, err
	}
	bodyExpr := "null"
	if api.Body != "" {
		bodyExpr = fmt.Sprintf("%q", api.Body)
	}
	script := fmt.Sprintf(`(async () => {
  const r = await fetch(%q, {method: %q, headers: %s, body: %s, credentials: 'include'});
  const text = await r.text();
  if (!r.ok) { throw new Error('HTTP ' + r.status + ': ' + text.slice(0, 200)); }
  return text;
})()`, api.URL, method, string(headersJSON), bodyExpr)

	text, err := chromedp.Run(tabCtx, chromedp.Evaluate[string](script, chromedp.EvalAwaitPromise))
	if err != nil {
		return Response{}, fmt.Errorf("页面上下文取接口失败（接口可能拒绝或签名缺失）: %w", err)
	}
	return Response{Body: []byte(text), ContentType: "application/json", Status: 200, FinalURL: api.URL}, nil
}

// tab 建一个浏览器 tab。优先连 SIWU_BROWSER_WS / 源级 browser_ws 指定的
// 外置浏览器；没配则回退到本机 Chrome。
func (f *Fetcher) tab(ctx context.Context, src model.Source) (context.Context, func(), error) {
	ws := strings.TrimSpace(src.Request.BrowserWS)
	if ws == "" {
		ws = strings.TrimSpace(f.opts.BrowserWS)
	}
	var (
		allocCtx context.Context
		cancel   context.CancelFunc
	)
	if ws != "" {
		allocCtx, cancel = remote.NewAllocator(ctx, ws)
	} else {
		opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
		opts = append(opts,
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
		)
		if f.opts.ChromePath != "" {
			opts = append(opts, chromedp.ExecPath(f.opts.ChromePath))
		}
		allocCtx, cancel = chromedp.NewExecAllocator(ctx, opts...)
	}
	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	stop := func() {
		tabCancel()
		cancel()
	}
	return tabCtx, stop, nil
}
