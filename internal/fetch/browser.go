package fetch

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
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

	// 源配置里的 UA/请求头/Cookie 必须在导航前注入，否则这次请求用不上。
	if err := f.applyProfile(tabCtx, newBrowserProfile(src.Request.Headers, src.Request.Cookies), src.URL); err != nil {
		return Response{}, err
	}

	if err := chromedp.Do(tabCtx, chromedp.Navigate(src.URL)); err != nil {
		return Response{}, fmt.Errorf("浏览器打开页面失败: %w", err)
	}
	if src.Request.WaitSelector != "" {
		_ = chromedp.Do(tabCtx, chromedp.WaitReady(chromedp.Search(src.Request.WaitSelector)))
	}
	wait := src.Request.WaitMS
	if wait <= 0 {
		// H5/SPA 在 load 事件之后才关闭自己的加载 toast，冷启动 2s 仍可能抓到。
		// 实测这个站点 3s 后 toast 才 display:none；站点更慢时用「渲染后等待」调大。
		wait = 3000
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

	// 前置页面与接口若不同域，Cookie 两边都要落，否则 credentials:'include' 带不上。
	// 导航前只套页面级画像：接口的 Referer 要在导航后覆盖，否则页面也带上接口的 Referer。
	pageProfile := newBrowserProfile(src.Request.Headers, src.Request.Cookies)
	if err := f.applyProfile(tabCtx, pageProfile, src.URL, api.URL); err != nil {
		return Response{}, err
	}

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

	// 接口级画像覆盖 tab 级头：Referer/Origin 这类 fetch 设不了的头只能这样带。
	// Network.setExtraHTTPHeaders 是整体替换，所以要带上源级业务头再合并。
	apiProfile := newBrowserProfile(src.Request.Headers, src.Request.Cookies)
	apiProfile.mergeAPI(api.Headers)
	if err := f.applyProfile(tabCtx, apiProfile, api.URL); err != nil {
		return Response{}, err
	}

	method := api.Method
	if method == "" {
		method = http.MethodGet
	}
	// fetch init 只放脚本能设置的头；Referer/Origin/UA/Cookie 走 tab 级注入。
	headers := make(map[string]string, len(api.Headers))
	for k, v := range api.Headers {
		if isFetchForbidden(k) {
			continue
		}
		headers[k] = v
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

// applyProfile 把源配置的 UA、附加请求头与 Cookie 注入 tab。
// 必须在 Navigate 之前调用：Network.setExtraHTTPHeaders 只对之后发出的请求生效。
func (f *Fetcher) applyProfile(ctx context.Context, p browserProfile, urls ...string) error {
	if p.UA == "" && len(p.Extra) == 0 && len(p.Cookies) == 0 {
		return nil
	}
	err := chromedp.Do(ctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		if p.UA != "" {
			// 只换 UA 字符串不会同步 Sec-CH-UA，严格站点仍可能看出不一致；
			// 需要完全一致时用真实浏览器通道即可，这里只解决「UA 被名单挡」。
			if _, err := cdp.Call(ctx, t, emulation.SetUserAgentOverride,
				emulation.SetUserAgentOverrideParams{UserAgent: p.UA}); err != nil {
				return fmt.Errorf("设置 UA 失败: %w", err)
			}
		}
		if len(p.Extra) > 0 {
			if _, err := cdp.Call(ctx, t, network.SetExtraHTTPHeaders,
				network.SetExtraHTTPHeadersParams{Headers: network.Headers(p.Extra)}); err != nil {
				return fmt.Errorf("注入请求头失败: %w", err)
			}
		}
		if params := cookieParams(p.Cookies, urls); len(params) > 0 {
			if _, err := cdp.Call(ctx, t, network.SetCookies,
				network.SetCookiesParams{Cookies: params}); err != nil {
				return fmt.Errorf("注入 Cookie 失败: %w", err)
			}
		}
		return nil
	}))
	if err != nil {
		return fmt.Errorf("浏览器通道套用源配置: %w", err)
	}
	return nil
}

func cookieParams(cookies []cookiePair, urls []string) []*network.CookieParam {
	origins := cookieOrigins(urls)
	out := make([]*network.CookieParam, 0, len(origins)*len(cookies))
	for _, origin := range origins {
		for _, c := range cookies {
			out = append(out, &network.CookieParam{Name: c.Name, Value: c.Value, URL: origin})
		}
	}
	return out
}
