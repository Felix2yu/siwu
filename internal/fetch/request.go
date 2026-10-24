package fetch

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------- 压缩

// supportedEncodings 与 read 里能解开的 Content-Encoding 保持一致。
// 用户可以在源配置里写 Accept-Encoding，但只能保留这些；br/zstd 会被剔掉，
// 否则服务器真返回 br 时解不开，最终表现为「抽取到 0 条」。
var supportedEncodings = map[string]struct{}{
	"gzip":     {},
	"x-gzip":   {},
	"deflate":  {},
	"identity": {},
}

// filterAcceptEncoding 剔掉解压端不支持的编码，其余原样保留（含 q 值）。
func filterAcceptEncoding(value string) string {
	var kept []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := strings.ToLower(part)
		if i := strings.IndexByte(name, ';'); i >= 0 {
			name = strings.TrimSpace(name[:i])
		}
		if _, ok := supportedEncodings[name]; ok {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, ", ")
}

// wantsCompression 判断过滤后的协商结果里是否还有真正需要解压的编码。
// 只剩 identity 等于不压缩，这时把请求头删掉、交给 Transport 自动 gzip 更省事。
func wantsCompression(value string) bool {
	for _, part := range strings.Split(value, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if i := strings.IndexByte(name, ';'); i >= 0 {
			name = strings.TrimSpace(name[:i])
		}
		if name == "gzip" || name == "x-gzip" || name == "deflate" {
			return true
		}
	}
	return false
}

// decodeBody 按 Content-Encoding 解压。Go 的 Transport 只在请求没显式设置
// Accept-Encoding 时自动解压并删除该响应头，所以这里按响应头判断不会重复解。
func decodeBody(raw []byte, encoding string, limit int64) ([]byte, error) {
	encodings := strings.Split(strings.ToLower(encoding), ",")
	out := raw
	// 多段编码按施加顺序的逆序解
	for i := len(encodings) - 1; i >= 0; i-- {
		name := strings.TrimSpace(encodings[i])
		var err error
		switch name {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			out, err = gunzip(out, limit)
		case "deflate":
			out, err = inflate(out, limit)
		default:
			return nil, fmt.Errorf("不支持的 Content-Encoding %q：请从请求头里去掉它，或改用浏览器通道", name)
		}
		if err != nil {
			return nil, fmt.Errorf("解压 %s 响应失败: %w", name, err)
		}
	}
	return out, nil
}

func gunzip(raw []byte, limit int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return readLimited(zr, limit)
}

// inflate 处理 deflate：实际站点有 zlib 包装与裸 deflate 两种写法，先按 zlib 试。
func inflate(raw []byte, limit int64) ([]byte, error) {
	if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
		defer zr.Close()
		if out, err := readLimited(zr, limit); err == nil {
			return out, nil
		}
	}
	fr := flate.NewReader(bytes.NewReader(raw))
	defer fr.Close()
	return readLimited(fr, limit)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 8 << 20
	}
	return io.ReadAll(io.LimitReader(r, limit))
}

// ---------------------------------------------------------------- 浏览器通道的请求画像

// browserManaged 是浏览器自己生成的头。源配置里写了也一律不手工注入：
// UA 走 UserAgentOverride，Cookie 走 Network.setCookies，Accept-Encoding
// 由浏览器协商；sec-* 是指纹的一部分，伪造成不一致反而更容易被判定为机器人。
var browserManaged = map[string]struct{}{
	"user-agent":        {},
	"cookie":            {},
	"cookie2":           {},
	"host":              {},
	"content-length":    {},
	"connection":        {},
	"keep-alive":        {},
	"accept-encoding":   {},
	"transfer-encoding": {},
	"upgrade":           {},
	"te":                {},
	"trailer":           {},
	"via":               {},
	"expect":            {},
	"proxy-connection":  {},
}

func isBrowserManaged(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return true
	}
	if strings.HasPrefix(k, "sec-") || strings.HasPrefix(k, "proxy-") {
		return true
	}
	_, ok := browserManaged[k]
	return ok
}

// fetchForbidden 是 Fetch 规范禁止脚本设置的请求头，放在 fetch init 里会被
// 静默忽略，所以这些只走 tab 级注入。
var fetchForbidden = map[string]struct{}{
	"accept-charset":                 {},
	"accept-encoding":                {},
	"access-control-request-headers": {},
	"access-control-request-method":  {},
	"connection":                     {},
	"content-length":                 {},
	"cookie":                         {},
	"cookie2":                        {},
	"date":                           {},
	"dnt":                            {},
	"expect":                         {},
	"host":                           {},
	"keep-alive":                     {},
	"origin":                         {},
	"referer":                        {},
	"te":                             {},
	"trailer":                        {},
	"transfer-encoding":              {},
	"upgrade":                        {},
	"via":                            {},
	"user-agent":                     {},
}

func isFetchForbidden(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return true
	}
	if strings.HasPrefix(k, "sec-") || strings.HasPrefix(k, "proxy-") {
		return true
	}
	_, ok := fetchForbidden[k]
	return ok
}

type cookiePair struct{ Name, Value string }

// browserProfile 是浏览器通道要补的请求画像：源配置里的 UA、附加请求头与 Cookie。
type browserProfile struct {
	UA      string
	Extra   map[string]any
	Cookies []cookiePair
}

func newBrowserProfile(headers, cookies map[string]string) browserProfile {
	p := browserProfile{Extra: map[string]any{}}
	p.merge(headers)
	// 显式 cookies 字段优先级高于请求头里手写的 Cookie
	for k, v := range cookies {
		p.addCookie(k, v)
	}
	return p
}

// merge 合并一组请求头。后合并的覆盖先前的同名头。
func (p *browserProfile) merge(headers map[string]string) {
	for k, v := range headers {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "user-agent":
			if ua := strings.TrimSpace(v); ua != "" {
				p.UA = ua
			}
		case "cookie":
			for _, c := range parseCookieHeader(v) {
				p.addCookie(c.Name, c.Value)
			}
		default:
			if isBrowserManaged(k) {
				continue
			}
			p.Extra[k] = v
		}
	}
}

// mergeAPI 合并接口级请求头。能在 fetch init 里设的留给 init（见 browserAPI），
// 只有 Referer / Origin 这类 fetch 禁止脚本设置的头才提到 tab 级。
func (p *browserProfile) mergeAPI(headers map[string]string) {
	for k, v := range headers {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "user-agent":
			if ua := strings.TrimSpace(v); ua != "" {
				p.UA = ua
			}
		case "cookie":
			for _, c := range parseCookieHeader(v) {
				p.addCookie(c.Name, c.Value)
			}
		default:
			if isFetchForbidden(k) && !isBrowserManaged(k) {
				p.Extra[k] = v
			}
		}
	}
}

func (p *browserProfile) addCookie(name, value string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for i := range p.Cookies {
		if p.Cookies[i].Name == name {
			p.Cookies[i].Value = value
			return
		}
	}
	p.Cookies = append(p.Cookies, cookiePair{Name: name, Value: value})
}

func parseCookieHeader(value string) []cookiePair {
	var out []cookiePair
	for _, seg := range strings.Split(value, ";") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		name, val, ok := strings.Cut(seg, "=")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		out = append(out, cookiePair{Name: name, Value: strings.TrimSpace(val)})
	}
	return out
}

// cookieOrigins 把目标地址归并成去重的 origin 列表。跨域的接口地址也要算一份，
// 否则页内 fetch 到另一个域时 credentials:'include' 带不上源配置的 Cookie。
func cookieOrigins(urls []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		origin := u.Scheme + "://" + u.Host
		if seen[origin] {
			continue
		}
		seen[origin] = true
		out = append(out, origin)
	}
	return out
}
