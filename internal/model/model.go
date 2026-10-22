// Package model 定义领域模型。所有可配置项都以结构化字段存在，落库时整体序列化。
package model

import (
	"net"
	"strings"
)

// Kind 是监控源的抽取形态。
type Kind string

const (
	KindHTML Kind = "html" // 列表型：条目集合来自 HTML
	KindPage Kind = "page" // 页面型：整页或区域文本快照，变化才产出条目
	KindJSON Kind = "json" // 接口型：条目集合来自 JSON API（可带浏览器上下文）
	KindRSS  Kind = "rss"  // 已有 RSS/Atom，重新提供
)

// Engine 是抓取通道，按源自选。
type Engine string

const (
	EngineHTTP    Engine = "http"
	EngineBrowser Engine = "browser"
)

// Source 是一个监控源。
type Source struct {
	ID      int64   `json:"id"`
	Slug    string  `json:"slug"`
	Name    string  `json:"name"`
	Kind    Kind    `json:"kind"`
	URL     string  `json:"url"`
	Engine  Engine  `json:"engine"`
	Enabled bool    `json:"enabled"`
	Request Request `json:"request"`
	Extract Extract `json:"extract"`
	Rules   []Rule  `json:"rules"`
	Filter  Filter  `json:"filter"`
	Article Article `json:"article"`
	Policy  Policy  `json:"policy"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
	LastRunAt int64 `json:"last_run_at"`
	NextRunAt int64 `json:"next_run_at"`
	Failures  int   `json:"consecutive_failures"`
}

// Request 是抓取参数。
type Request struct {
	Headers      map[string]string `json:"headers"`
	Cookies      map[string]string `json:"cookies"`
	Proxy        string            `json:"proxy"`
	TimeoutSec   int               `json:"timeout_sec"`
	Charset      string            `json:"charset"`
	BrowserWS    string            `json:"browser_ws"`    // 覆盖全局 CDP 地址
	WaitMS       int               `json:"wait_ms"`       // 浏览器渲染后额外等待
	WaitSelector string            `json:"wait_selector"` // 等到该选择器出现
	API          *APISpec          `json:"api,omitempty"`
}

// APISpec 描述 json 型源要请求的接口。InBrowser 为真时在已加载页面的上下文里
// 发请求，从而带上页面运行期生成的 WAF 签名头。
type APISpec struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	InBrowser bool              `json:"in_browser"`
	WaitMS    int               `json:"wait_ms"`
}

// Extract 描述怎么从文档里取条目。
type Extract struct {
	Item        string           `json:"item"`   // 条目选择器（html/json 路径）
	Limit       int              `json:"limit"`  // 最多取多少条，0=默认 50
	Fields      map[string]Field `json:"fields"` // title/link/date/summary/category/content/guid
	Scope       string           `json:"scope"`  // page 型的监控区域
	Exclude     []string         `json:"exclude"`
	Normalize   []string         `json:"normalize"`
	IgnoreRegex []string         `json:"ignore_regex"`
}

// Field 是单个字段的取值规则。
type Field struct {
	Sel        string   `json:"sel"`
	Attr       string   `json:"attr"`
	Absolute   bool     `json:"absolute"`
	Fallback   []string `json:"fallback"`
	Optional   bool     `json:"optional"`
	Template   string   `json:"template"`
	Transforms []string `json:"transforms"`
}

// Rule 是一条清洗规则。
type Rule struct {
	ID      string   `json:"id"`
	Target  []string `json:"target"`
	Op      string   `json:"op"`
	Pattern string   `json:"pattern"`
	Replace string   `json:"replace"`
	Flags   string   `json:"flags"`
	Value   string   `json:"value"`
	Layouts []string `json:"layouts"`
	TZ      string   `json:"tz"`
	N       int      `json:"n"`
}

// Filter 是关键词/正则的包含排除。
type Filter struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
	Field   string   `json:"field"`
	Regex   bool     `json:"regex"`
}

// Article 控制是否跟进详情页取正文。
type Article struct {
	Enabled     bool   `json:"enabled"`
	Content     string `json:"content"`
	Title       string `json:"title"`
	Date        string `json:"date"`
	Readability bool   `json:"readability"`
	MaxChars    int    `json:"max_chars"`
}

// Policy 是调度与去重策略。
type Policy struct {
	IntervalSec     int          `json:"interval_sec"`
	Cron            string       `json:"cron"` // 非空时优先于 IntervalSec
	MaxItems        int          `json:"max_items"`
	RetentionDays   int          `json:"retention_days"`
	MinChangedLines int          `json:"min_changed_lines"`
	UpdateMode      string       `json:"update_mode"` // ignore | update | archive
	Notify          NotifyPolicy `json:"notify"`
}

// NotifyPolicy 是通知策略。
type NotifyPolicy struct {
	Triggers    []string `json:"triggers"` // new_entry | content_changed | failure | recovery | silent_empty
	Timing      string   `json:"timing"`   // immediate | batch | digest
	TargetIDs   []int64  `json:"target_ids"`
	ThrottleSec int      `json:"throttle_sec"`
	Detail      string   `json:"detail"`       // none | summary | full
	QuietHours  string   `json:"quiet_hours"`  // "22:00-08:00"
	DigestAt    string   `json:"digest_at"`    // "09:00"，digest 模式的汇总时刻
	SilentAfter int      `json:"silent_after"` // 连续多少次空跑算静默失效
}

// Entry 是去重后的一条订阅条目。
type Entry struct {
	ID          int64  `json:"id"`
	SourceID    int64  `json:"source_id"`
	EntryKey    string `json:"entry_key"`
	GUID        string `json:"guid"`
	Title       string `json:"title"`
	Link        string `json:"link"`
	Summary     string `json:"summary"`
	ContentHTML string `json:"content_html"`
	ContentText string `json:"content_text"`
	Category    string `json:"category"`
	PublishedAt int64  `json:"published_at"`
	FirstSeenAt int64  `json:"first_seen_at"`
	UpdatedAt   int64  `json:"updated_at"`
	ContentHash string `json:"content_hash"`
	NotifiedAt  int64  `json:"notified_at"`
}

// Run 是一次运行记录。
type Run struct {
	ID           int64    `json:"id"`
	SourceID     int64    `json:"source_id"`
	StartedAt    int64    `json:"started_at"`
	FinishedAt   int64    `json:"finished_at"`
	Status       string   `json:"status"` // running | ok | empty | error
	Trigger      string   `json:"trigger"`
	ItemsFound   int      `json:"items_found"`
	ItemsNew     int      `json:"items_new"`
	ItemsUpdated int      `json:"items_updated"`
	DurationMS   int64    `json:"duration_ms"`
	Error        string   `json:"error"`
	Warnings     []string `json:"warnings"`
}

// Target 是一个通知目标。
type Target struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url,omitempty"` // 只写；读取时给 Masked
	Masked    string `json:"masked,omitempty"`
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
}

// Defaults 补齐零值，保证老配置与前端省略字段时行为一致。
func (s *Source) Defaults() {
	if s.Kind == "" {
		s.Kind = KindHTML
	}
	if s.Engine == "" {
		s.Engine = EngineHTTP
	}
	if s.Request.TimeoutSec <= 0 {
		s.Request.TimeoutSec = 20
	}
	if s.Extract.Limit <= 0 {
		s.Extract.Limit = 50
	}
	if s.Policy.IntervalSec <= 0 {
		s.Policy.IntervalSec = 3600
	}
	if s.Policy.MaxItems <= 0 {
		s.Policy.MaxItems = 50
	}
	if s.Policy.UpdateMode == "" {
		s.Policy.UpdateMode = "update"
	}
	if s.Policy.Notify.Timing == "" {
		s.Policy.Notify.Timing = "immediate"
	}
	if s.Policy.Notify.Detail == "" {
		s.Policy.Notify.Detail = "summary"
	}
	if s.Policy.Notify.DigestAt == "" {
		s.Policy.Notify.DigestAt = "09:00"
	}
	if s.Policy.Notify.SilentAfter <= 0 {
		s.Policy.Notify.SilentAfter = 3
	}
	if len(s.Policy.Notify.Triggers) == 0 {
		s.Policy.Notify.Triggers = []string{"new_entry", "failure", "recovery"}
	}
	if s.Policy.Notify.ThrottleSec < 0 {
		s.Policy.Notify.ThrottleSec = 0
	}
	if s.Article.MaxChars <= 0 {
		s.Article.MaxChars = 4000
	}
}

// Slug 从名字或 URL 推导一个可用的订阅路径片段。
func Slug(name, rawURL string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out != "" {
		return out
	}
	// 中文名推不出 ASCII：退回 URL 主机首段
	if host := hostLabel(rawURL); host != "" {
		return host
	}
	return "feed"
}

func hostLabel(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// IP 主机没有可读名字，用 localhost 兜底（否则 127.0.0.1 会得到 slug "127"）
	host := s
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return "localhost"
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 {
		return ""
	}
	first := parts[0]
	if first == "www" && len(parts) > 1 {
		first = parts[1]
	}
	var b strings.Builder
	for _, r := range first {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
