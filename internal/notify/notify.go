// Package notify 封装 apprise-go：按源的策略决定发不发、什么时候发、发多详细，
// 并把发送结果（含有条目级别的「已通知」标记）写回存储。
//
// 策略维度：
//   - triggers：new_entry / content_changed / failure / recovery / silent_empty
//   - timing：immediate（立即）/ batch（30s 合并）/ digest（每日定点汇总）
//   - detail：none（只标题）/ summary（摘要）/ full（正文）
//   - throttle 同源节流、quiet_hours 静默时段
package notify

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Felix2yu/siwu/internal/events"
	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/secretbox"
	"github.com/Felix2yu/siwu/internal/store"
	"github.com/unraid/apprise-go"
)

// Sender 是可替换的发送实现（测试用假实现）。
type Sender func(ctx context.Context, urls []string, title, body string, nt apprise.NotifyType) error

// 事件类别。
const (
	KindNew     = "new_entry"
	KindChanged = "content_changed"
	KindFailure = "failure"
	KindRecover = "recovery"
	KindSilent  = "silent_empty"
)

// Notifier 负责实际发送。
type Notifier struct {
	Store *store.Store
	Box   *secretbox.Box
	Log   *slog.Logger
	Bus   *events.Bus
	Send  Sender

	Retries     int           // 失败重试次数，默认 3
	BatchWindow time.Duration // batch 合并窗口，默认 30s
	DigestPoll  time.Duration // digest 检查间隔，默认 30s

	mu       sync.Mutex
	lastSent map[int64]time.Time
	batch    map[int64]*pending
	digest   map[int64]*pending
	started  bool
}

// pending 是一个待发送的聚合桶。
type pending struct {
	source  model.Source
	entries []model.Entry
	kinds   map[string]bool
	status  string
	lastErr string
	flushAt time.Time
}

// New 构造 Notifier。
func New(st *store.Store, box *secretbox.Box, log *slog.Logger) *Notifier {
	return &Notifier{
		Store: st, Box: box, Log: log,
		Send:        defaultSender,
		Retries:     3,
		BatchWindow: 30 * time.Second,
		DigestPoll:  30 * time.Second,
		lastSent:    map[int64]time.Time{},
		batch:       map[int64]*pending{},
		digest:      map[int64]*pending{},
	}
}

// Schemas 返回当前构建支持的通知协议（前端下拉用）。
func Schemas() []string { return apprise.SupportedSchemas() }

// Validate 保存目标前校验协议。
func Validate(rawURL string) error {
	c := apprise.New()
	return c.Add(rawURL)
}

func defaultSender(_ context.Context, urls []string, title, body string, nt apprise.NotifyType) error {
	if len(urls) == 0 {
		return nil
	}
	c := apprise.New()
	if err := c.AddAll(urls...); err != nil {
		return err
	}
	return c.Send(body, apprise.WithTitle(title), apprise.WithNotifyType(nt), apprise.WithInputFormat("html"))
}

// Event 是一次运行结束后交给通知层的事实。
type Event struct {
	Source      model.Source
	Entries     []model.Entry // 新增条目
	Changed     []model.Entry // 内容有更新的条目
	Status      string        // ok | empty | error
	PrevStatus  string        // 上一次运行的状态，用于识别 recovery
	Err         string
	SilentCount int // 含本次在内连续空跑次数
}

// Dispatch 按源策略发送通知，返回需要写进运行记录的告警。
// 通知失败绝不影响入库与 RSS，只作为 warning 暴露给用户。
func (n *Notifier) Dispatch(ctx context.Context, ev Event) []string {
	policy := ev.Source.Policy.Notify
	if len(policy.TargetIDs) == 0 {
		return nil
	}
	trig := toSet(policy.Triggers)
	kinds := map[string]bool{}
	switch {
	case ev.Status == "error" && trig[KindFailure]:
		kinds[KindFailure] = true
	case ev.Status == "ok" && ev.PrevStatus == "error" && trig[KindRecover]:
		kinds[KindRecover] = true
	}
	if len(ev.Entries) > 0 && trig[KindNew] {
		kinds[KindNew] = true
	}
	if len(ev.Changed) > 0 && trig[KindChanged] {
		kinds[KindChanged] = true
	}
	if ev.Status == "empty" && ev.SilentCount >= silentAfter(policy.SilentAfter) && trig[KindSilent] {
		kinds[KindSilent] = true
	}
	if len(kinds) == 0 {
		return nil
	}
	// 静默时段只压住「内容类」通知，故障仍要吵醒人
	if inQuietHours(policy.QuietHours) && !kinds[KindFailure] && !kinds[KindSilent] {
		n.Log.Info("静默时段，跳过通知", "source", ev.Source.Slug)
		return nil
	}

	switch policy.Timing {
	case "batch":
		n.enqueue(n.batch, ev, kinds, time.Now().Add(n.BatchWindow))
		return nil
	case "digest":
		n.enqueue(n.digest, ev, kinds, nextDigestAt(time.Now(), policy.DigestAt))
		return nil
	default:
		if !kinds[KindFailure] && !kinds[KindRecover] && !n.allow(ev.Source.ID, policy.ThrottleSec) {
			n.Log.Info("触发节流，跳过通知", "source", ev.Source.Slug)
			return nil
		}
		// 新增与变更都要带上，否则「只有内容被修改」时标题会落到兜底文案
		all := make([]model.Entry, 0, len(ev.Entries)+len(ev.Changed))
		all = append(all, ev.Entries...)
		all = append(all, ev.Changed...)
		return n.deliver(ctx, ev.Source, all, kinds, ev.Status, ev.Err, policy.Detail)
	}
}

// Start 启动聚合发送循环。ctx 结束时把剩余队列冲刷一次。
func (n *Notifier) Start(ctx context.Context) {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return
	}
	n.started = true
	n.mu.Unlock()

	poll := n.DigestPoll
	if poll <= 0 {
		poll = 30 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			n.flushAll(context.Background())
			return
		case <-ticker.C:
			n.flushDue(context.Background())
		}
	}
}

// Flush 立即冲刷所有待发队列（手动测试或关闭前调用）。
func (n *Notifier) Flush(ctx context.Context) { n.flushAll(ctx) }

func (n *Notifier) enqueue(bucket map[int64]*pending, ev Event, kinds map[string]bool, flushAt time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	p, ok := bucket[ev.Source.ID]
	if !ok {
		p = &pending{source: ev.Source, kinds: map[string]bool{}, flushAt: flushAt}
		bucket[ev.Source.ID] = p
	}
	p.source = ev.Source
	if ev.Status == "error" {
		p.status = "error"
		p.lastErr = ev.Err
	}
	for _, k := range keysOf(kinds) {
		p.kinds[k] = true
	}
	seen := map[int64]bool{}
	for _, e := range p.entries {
		seen[e.ID] = true
	}
	for _, e := range append(append([]model.Entry{}, ev.Entries...), ev.Changed...) {
		if e.ID != 0 && seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		p.entries = append(p.entries, e)
	}
	// 取较早的冲刷时间，避免后来的事件把已排队的通知往后推
	if p.flushAt.IsZero() || flushAt.Before(p.flushAt) {
		p.flushAt = flushAt
	}
}

func (n *Notifier) flushDue(ctx context.Context) {
	now := time.Now()
	var ready []*pending
	n.mu.Lock()
	for id, p := range n.batch {
		if !p.flushAt.After(now) {
			ready = append(ready, p)
			delete(n.batch, id)
		}
	}
	for id, p := range n.digest {
		if !p.flushAt.After(now) {
			ready = append(ready, p)
			delete(n.digest, id)
		}
	}
	n.mu.Unlock()
	for _, p := range ready {
		n.deliver(ctx, p.source, p.entries, p.kinds, p.status, p.lastErr, p.source.Policy.Notify.Detail)
	}
}

func (n *Notifier) flushAll(ctx context.Context) {
	n.mu.Lock()
	var ready []*pending
	for id, p := range n.batch {
		ready = append(ready, p)
		delete(n.batch, id)
	}
	for id, p := range n.digest {
		ready = append(ready, p)
		delete(n.digest, id)
	}
	n.mu.Unlock()
	for _, p := range ready {
		n.deliver(ctx, p.source, p.entries, p.kinds, p.status, p.lastErr, p.source.Policy.Notify.Detail)
	}
}

// deliver 真正发送一批通知。
func (n *Notifier) deliver(ctx context.Context, src model.Source, entries []model.Entry, kinds map[string]bool, status, lastErr, detail string) []string {
	urls, err := n.Store.TargetURLs(src.Policy.Notify.TargetIDs, n.Box)
	if err != nil {
		n.Log.Error("读取通知目标失败", "err", err)
		return []string{fmt.Sprintf("通知目标读取失败: %v", err)}
	}
	if len(urls) == 0 {
		return []string{"通知目标为空或已停用，本次未发送"}
	}
	nt, title := summarize(src, entries, kinds, status)
	body := renderBody(src, entries, kinds, status, lastErr, detail)
	if err := n.sendWithRetry(ctx, urls, title, body, nt); err != nil {
		n.Log.Error("发送通知失败", "source", src.Slug, "err", err)
		if n.Bus != nil {
			n.Bus.Publish(events.Event{
				Type: "notify.fail", SourceID: src.ID, SourceName: src.Name, Slug: src.Slug,
				Error: err.Error(),
			})
		}
		return []string{fmt.Sprintf("通知发送失败（已重试 %d 次）: %v", n.Retries, err)}
	}
	// 只标记内容类条目，故障类事件没有条目
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		if e.ID != 0 {
			ids = append(ids, e.ID)
		}
	}
	if err := n.Store.MarkNotified(ids, time.Now().Unix()); err != nil {
		n.Log.Warn("标记已通知失败", "err", err)
	}
	if n.Bus != nil {
		n.Bus.Publish(events.Event{
			Type: "notify.ok", SourceID: src.ID, SourceName: src.Name, Slug: src.Slug,
			Message: title,
		})
	}
	return nil
}

func (n *Notifier) sendWithRetry(ctx context.Context, urls []string, title, body string, nt apprise.NotifyType) error {
	attempts := n.Retries
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	wait := time.Second
	for i := 0; i < attempts; i++ {
		if err := n.Send(ctx, urls, title, body, nt); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(wait):
		}
		wait *= 2
	}
	return lastErr
}

func (n *Notifier) allow(sourceID int64, throttleSec int) bool {
	if throttleSec <= 0 {
		return true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	last, ok := n.lastSent[sourceID]
	if ok && time.Since(last) < time.Duration(throttleSec)*time.Second {
		return false
	}
	n.lastSent[sourceID] = time.Now()
	return true
}

// summarize 决定通知标题与级别。
func summarize(src model.Source, entries []model.Entry, kinds map[string]bool, status string) (apprise.NotifyType, string) {
	name := src.Name
	if name == "" {
		name = src.Slug
	}
	switch {
	case kinds[KindFailure]:
		return apprise.NotifyFailure, fmt.Sprintf("[失败] %s", name)
	case kinds[KindSilent]:
		return apprise.NotifyWarning, fmt.Sprintf("[无产出] %s 连续空跑", name)
	case kinds[KindRecover]:
		return apprise.NotifySuccess, fmt.Sprintf("[恢复] %s", name)
	case len(entries) > 0:
		if kinds[KindChanged] && !kinds[KindNew] {
			return apprise.NotifyInfo, fmt.Sprintf("[更新] %s · %d 条内容有变化", name, len(entries))
		}
		return apprise.NotifyInfo, fmt.Sprintf("[%s] %d 条新内容", name, len(entries))
	default:
		return apprise.NotifyInfo, fmt.Sprintf("[%s] %s", name, status)
	}
}

func renderBody(src model.Source, entries []model.Entry, kinds map[string]bool, status, lastErr, detail string) string {
	var b strings.Builder
	if lastErr != "" {
		b.WriteString("<p><b>错误：</b>")
		b.WriteString(html.EscapeString(lastErr))
		b.WriteString("</p>")
	}
	b.WriteString(fmt.Sprintf("<p>来源：<a href=\"%s\">%s</a></p>",
		html.EscapeString(src.URL), html.EscapeString(src.Name)))
	if kinds[KindSilent] {
		b.WriteString("<p>连续多次没有抽取到条目。页面可能改版、选择器可能已失效。</p>")
	}
	if kinds[KindRecover] {
		b.WriteString("<p>上一次运行失败，本次已恢复正常。</p>")
	}
	if detail == "none" {
		return b.String()
	}
	limit := 20
	if len(entries) > limit {
		b.WriteString(fmt.Sprintf("<p>共 %d 条，下面列出前 %d 条：</p>", len(entries), limit))
	}
	for i, e := range entries {
		if i >= limit {
			b.WriteString(fmt.Sprintf("<p>…另有 %d 条</p>", len(entries)-i))
			break
		}
		title := html.EscapeString(e.Title)
		if e.Link != "" {
			title = fmt.Sprintf("<a href=\"%s\">%s</a>", html.EscapeString(e.Link), title)
		}
		b.WriteString("<p>• " + title)
		if e.PublishedAt > 0 {
			b.WriteString(" <small>" + time.Unix(e.PublishedAt, 0).Format("2006-01-02 15:04") + "</small>")
		}
		b.WriteString("</p>")
		if detail == "full" && e.ContentHTML != "" {
			b.WriteString(e.ContentHTML)
		} else if detail == "summary" && e.Summary != "" {
			b.WriteString("<p><small>" + html.EscapeString(e.Summary) + "</small></p>")
		}
	}
	if len(entries) == 0 && status == "empty" {
		b.WriteString("<p>本次没有抽取到条目。</p>")
	}
	return b.String()
}

// nextDigestAt 计算下一个 HH:MM（本地时区）；非法输入按 09:00 处理。
func nextDigestAt(now time.Time, spec string) time.Time {
	h, m := 9, 0
	parts := strings.SplitN(strings.TrimSpace(spec), ":", 2)
	if len(parts) == 2 {
		hh, err1 := strconv.Atoi(parts[0])
		mm, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil && hh >= 0 && hh <= 23 && mm >= 0 && mm <= 59 {
			h, m = hh, mm
		}
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// inQuietHours 支持 "22:00-08:00" 形式；跨零点按区间处理。
func inQuietHours(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return false
	}
	start, ok1 := parseClock(parts[0])
	end, ok2 := parseClock(parts[1])
	if !ok1 || !ok2 {
		return false
	}
	now := time.Now()
	cur := now.Hour()*60 + now.Minute()
	if start <= end {
		return cur >= start && cur < end
	}
	return cur >= start || cur < end
}

func parseClock(s string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// silentAfter 兜底：老配置没有这个字段时按 3 次算。
func silentAfter(n int) int {
	if n <= 0 {
		return 3
	}
	return n
}

func toSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, s := range list {
		out[s] = true
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
