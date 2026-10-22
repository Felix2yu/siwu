package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/secretbox"
	"github.com/Felix2yu/siwu/internal/store"
	"github.com/unraid/apprise-go"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

type sent struct {
	Title string
	Body  string
	Type  apprise.NotifyType
	At    time.Time
}

type fakeSender struct {
	mu    sync.Mutex
	calls []sent
	err   error
	fails int // 前 N 次调用返回错误
}

func (f *fakeSender) send(_ context.Context, _ []string, title, body string, nt apprise.NotifyType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sent{Title: title, Body: body, Type: nt, At: time.Now()})
	if f.err != nil {
		return f.err
	}
	if f.fails > 0 {
		f.fails--
		return errors.New("模拟通道故障")
	}
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSender) last() sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func newTestNotifier(t *testing.T) (*Notifier, *store.Store, *model.Source) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	box, err := secretbox.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	tgt := model.Target{Name: "测试通道", URL: "json://localhost/hook", Enabled: true}
	if err := st.SaveTarget(&tgt, box); err != nil {
		t.Fatal(err)
	}
	src := model.Source{Name: "测试源", URL: "https://example.com/list", Slug: "test"}
	src.Defaults()
	if err := st.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	src.Policy.Notify.TargetIDs = []int64{tgt.ID}
	src.Policy.Notify.Triggers = []string{
		KindNew, KindChanged, KindFailure, KindRecover, KindSilent,
	}
	n := New(st, box, testLogger())
	return n, st, &src
}

func insertEntry(t *testing.T, st *store.Store, srcID int64, title string) model.Entry {
	t.Helper()
	e := model.Entry{
		SourceID: srcID, EntryKey: "k-" + title, GUID: "g-" + title, Title: title,
		Summary: "摘要", PublishedAt: time.Now().Unix(), FirstSeenAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(), ContentHash: "h",
	}
	inserted, _, err := st.UpsertEntry(&e, "update")
	if err != nil || !inserted {
		t.Fatalf("写入条目失败: %v", err)
	}
	return e
}

func TestImmediateNewEntryAndMarkNotified(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	e := insertEntry(t, st, src.ID, "新公告")

	warns := n.Dispatch(context.Background(), Event{
		Source: *src, Entries: []model.Entry{e}, Status: "ok",
	})
	if len(warns) != 0 {
		t.Fatalf("不应有告警: %v", warns)
	}
	if fs.count() != 1 {
		t.Fatalf("应发送 1 次，实际 %d", fs.count())
	}
	got := fs.last()
	if !strings.Contains(got.Title, "1 条新内容") {
		t.Errorf("标题未包含条数: %q", got.Title)
	}
	if got.Type != apprise.NotifyInfo {
		t.Errorf("新增内容应为 info，得到 %v", got.Type)
	}
	entries, _ := st.Entries(src.ID, 10, 0)
	if len(entries) != 1 || entries[0].NotifiedAt == 0 {
		t.Fatalf("发送成功后应标记 notified_at，实际 %+v", entries)
	}
}

func TestTriggerFilter(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	src.Policy.Notify.Triggers = []string{KindFailure}
	e := insertEntry(t, st, src.ID, "新公告")

	if warns := n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e}, Status: "ok"}); len(warns) != 0 {
		t.Fatalf("不应有告警: %v", warns)
	}
	if fs.count() != 0 {
		t.Fatalf("未订阅 new_entry 不该发送，实际 %d 次", fs.count())
	}
}

func TestFailureAndRecovery(t *testing.T) {
	n, _, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send

	n.Dispatch(context.Background(), Event{Source: *src, Status: "error", Err: "连接超时"})
	if fs.count() != 1 || fs.last().Type != apprise.NotifyFailure {
		t.Fatalf("失败事件应发送 failure 级别，实际 %d 次", fs.count())
	}
	if !strings.Contains(fs.last().Body, "连接超时") {
		t.Errorf("正文应包含错误原因: %q", fs.last().Body)
	}

	n.Dispatch(context.Background(), Event{Source: *src, Status: "ok", PrevStatus: "error"})
	if fs.count() != 2 {
		t.Fatalf("恢复应再发一次，实际 %d", fs.count())
	}
	if !strings.Contains(fs.last().Title, "恢复") {
		t.Errorf("恢复标题不正确: %q", fs.last().Title)
	}
}

func TestSilentEmpty(t *testing.T) {
	n, _, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	src.Policy.Notify.SilentAfter = 3

	// 前两次空跑不告警
	for i := 0; i < 2; i++ {
		n.Dispatch(context.Background(), Event{Source: *src, Status: "empty", SilentCount: i + 1})
	}
	if fs.count() != 0 {
		t.Fatalf("未达阈值不该发送，实际 %d", fs.count())
	}
	n.Dispatch(context.Background(), Event{Source: *src, Status: "empty", SilentCount: 3})
	if fs.count() != 1 || fs.last().Type != apprise.NotifyWarning {
		t.Fatalf("连续 3 次空跑应发 warning，实际 %d", fs.count())
	}
	if !strings.Contains(fs.last().Body, "改版") {
		t.Errorf("正文应提示页面改版风险: %q", fs.last().Body)
	}
}

func TestQuietHoursOnlyBlocksContent(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	src.Policy.Notify.QuietHours = "00:00-23:59"
	e := insertEntry(t, st, src.ID, "新公告")

	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e}, Status: "ok"})
	if fs.count() != 0 {
		t.Fatal("静默时段不应发内容通知")
	}
	n.Dispatch(context.Background(), Event{Source: *src, Status: "error", Err: "挂了"})
	if fs.count() != 1 {
		t.Fatal("静默时段仍应发故障通知")
	}
}

func TestThrottle(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	src.Policy.Notify.ThrottleSec = 60
	e1 := insertEntry(t, st, src.ID, "第一条")
	e2 := insertEntry(t, st, src.ID, "第二条")

	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e1}, Status: "ok"})
	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e2}, Status: "ok"})
	if fs.count() != 1 {
		t.Fatalf("节流窗口内应只发 1 次，实际 %d", fs.count())
	}
}

func TestBatchMerges(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	src.Policy.Notify.Timing = "batch"
	e1 := insertEntry(t, st, src.ID, "第一条")
	e2 := insertEntry(t, st, src.ID, "第二条")

	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e1}, Status: "ok"})
	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e2}, Status: "ok"})
	if fs.count() != 0 {
		t.Fatalf("batch 模式在窗口内不该立即发送，实际 %d", fs.count())
	}
	n.Flush(context.Background())
	if fs.count() != 1 {
		t.Fatalf("冲刷后应合并为 1 条消息，实际 %d", fs.count())
	}
	if !strings.Contains(fs.last().Title, "2 条新内容") {
		t.Errorf("合并标题不正确: %q", fs.last().Title)
	}
}

func TestDigestWaitsForSlot(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	src.Policy.Notify.Timing = "digest"
	src.Policy.Notify.DigestAt = "09:00"
	e := insertEntry(t, st, src.ID, "第一条")

	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e}, Status: "ok"})
	if fs.count() != 0 {
		t.Fatal("digest 模式不该立即发送")
	}
	n.mu.Lock()
	p := n.digest[src.ID]
	n.mu.Unlock()
	if p == nil || p.flushAt.Hour() != 9 || p.flushAt.Minute() != 0 {
		t.Fatalf("digest 应在次日 09:00 冲刷，实际 %+v", p)
	}
	n.Flush(context.Background())
	if fs.count() != 1 {
		t.Fatalf("强制冲刷应发送 1 次，实际 %d", fs.count())
	}
}

func TestRetryThenWarning(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{err: errors.New("通道不可用")}
	n.Send = fs.send
	n.Retries = 3
	e := insertEntry(t, st, src.ID, "公告")

	warns := n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e}, Status: "ok"})
	if fs.count() != 3 {
		t.Fatalf("应重试 3 次，实际调用 %d 次", fs.count())
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "通知发送失败") {
		t.Fatalf("重试耗尽后应返回告警，实际 %v", warns)
	}
	entries, _ := st.Entries(src.ID, 10, 0)
	if entries[0].NotifiedAt != 0 {
		t.Fatal("发送失败不应标记已通知")
	}
}

func TestDetailLevels(t *testing.T) {
	n, st, src := newTestNotifier(t)
	fs := &fakeSender{}
	n.Send = fs.send
	e := insertEntry(t, st, src.ID, "带摘要的公告")

	src.Policy.Notify.Detail = "none"
	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e}, Status: "ok"})
	if strings.Contains(fs.last().Body, "摘要") {
		t.Error("detail=none 不应包含摘要")
	}

	src.Policy.Notify.Detail = "summary"
	n.mu.Lock()
	delete(n.lastSent, src.ID)
	n.mu.Unlock()
	n.Dispatch(context.Background(), Event{Source: *src, Entries: []model.Entry{e}, Status: "ok"})
	if !strings.Contains(fs.last().Body, "摘要") {
		t.Error("detail=summary 应包含摘要")
	}
}
