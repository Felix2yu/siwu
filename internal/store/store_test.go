package store

import (
	"path/filepath"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/secretbox"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "siwu.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSaveAndLoadSource(t *testing.T) {
	st := openTest(t)
	src := model.Source{
		Name: "苏州文旅", Kind: model.KindHTML, URL: "https://example.com/news",
		Engine: model.EngineHTTP, Enabled: true,
		Extract: model.Extract{Item: "css:ul li", Fields: map[string]model.Field{
			"title": {Sel: "css:a"}, "link": {Sel: "css:a", Attr: "href", Absolute: true},
		}},
	}
	if err := st.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	if src.ID == 0 || src.Slug == "" {
		t.Fatalf("保存后应有 id 与 slug: %+v", src)
	}
	got, err := st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Extract.Fields["link"].Attr != "href" || !got.Extract.Fields["link"].Absolute {
		t.Fatalf("字段配置未正确往返: %+v", got.Extract.Fields["link"])
	}
	if got.Policy.IntervalSec != model.DefaultIntervalSec {
		t.Fatalf("默认值未补齐: %d", got.Policy.IntervalSec)
	}
}

func TestUpsertDedupes(t *testing.T) {
	st := openTest(t)
	src := model.Source{Name: "x", Kind: model.KindHTML, URL: "https://example.com"}
	if err := st.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	e := model.Entry{SourceID: src.ID, EntryKey: "k1", GUID: "g1", Title: "标题", ContentHash: "h1", FirstSeenAt: 1}
	inserted, updated, err := st.UpsertEntry(&e, "update")
	if err != nil || !inserted || updated {
		t.Fatalf("首次写入应为新增: inserted=%v updated=%v err=%v", inserted, updated, err)
	}
	if e.ID == 0 {
		t.Fatal("新增后应把自增 id 回填到 entry，通知层要用它标记已通知")
	}
	inserted, updated, err = st.UpsertEntry(&e, "update")
	if err != nil || inserted || updated {
		t.Fatalf("同内容重复写入不应变更: inserted=%v updated=%v err=%v", inserted, updated, err)
	}
	e.ContentHash = "h2"
	inserted, updated, err = st.UpsertEntry(&e, "update")
	if err != nil || inserted || !updated {
		t.Fatalf("内容变化应记为更新: inserted=%v updated=%v err=%v", inserted, updated, err)
	}
	n, _ := st.CountEntries(src.ID)
	if n != 1 {
		t.Fatalf("去重后应有 1 条，实际 %d", n)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := openTest(t)
	got, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Defaults.IntervalSec != model.DefaultIntervalSec || got.Defaults.UpdateMode != "update" {
		t.Fatalf("未保存时应返回内置默认值: %+v", got.Defaults)
	}
	want := model.Settings{Defaults: model.SourceDefaults{
		IntervalSec: 600, MaxItems: 20, RetentionDays: 7,
		UpdateMode: "archive", ArticleEnabled: true,
	}}
	if err := st.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Defaults != want.Defaults {
		t.Fatalf("设置未正确往返: got=%+v want=%+v", got.Defaults, want.Defaults)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	st := openTest(t)
	src := model.Source{Name: "p", Kind: model.KindPage, URL: "https://example.com"}
	if err := st.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	if snap, err := st.GetSnapshot(src.ID); err != nil || snap.Hash != "" {
		t.Fatalf("初始快照应为空: %+v err=%v", snap, err)
	}
	if err := st.PutSnapshot(src.ID, Snapshot{Hash: "abc", Text: "正文", CapturedAt: 10}); err != nil {
		t.Fatal(err)
	}
	snap, err := st.GetSnapshot(src.ID)
	if err != nil || snap.Hash != "abc" || snap.Text != "正文" {
		t.Fatalf("快照往返失败: %+v err=%v", snap, err)
	}
}

func TestTargetsEncrypted(t *testing.T) {
	st := openTest(t)
	box, err := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	target := model.Target{Name: "ntfy", URL: "ntfys://ntfy.example.com/siwu", Kind: "ntfy", Enabled: true}
	if err := st.SaveTarget(&target, box); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListTargets(box)
	if err != nil || len(list) != 1 {
		t.Fatalf("读取目标失败: %+v err=%v", list, err)
	}
	if list[0].URL != "ntfys://ntfy.example.com/siwu" {
		t.Fatalf("解密后应还原原文: %q", list[0].URL)
	}
	if list[0].Masked == list[0].URL {
		t.Fatal("对外展示必须是掩码")
	}
}
