package store

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
)

// sqliteMagic 是 SQLite 数据库文件的 16 字节头，外部备份工具按它校验产物。
const sqliteMagic = "SQLite format 3\x00"

// openExportPair 在同一数据目录上开一对连接：live 模拟正在运行的应用（WAL 写入），
// export 模拟 docker exec 进来的导出进程。
func openExportPair(t *testing.T) (live, exp *Store, dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	live, err := Open(filepath.Join(dataDir, DBFileName))
	if err != nil {
		t.Fatalf("打开 live 库失败: %v", err)
	}
	t.Cleanup(func() { _ = live.Close() })
	exp, err = OpenForExport(dataDir)
	if err != nil {
		t.Fatalf("打开导出连接失败: %v", err)
	}
	t.Cleanup(func() { _ = exp.Close() })
	return live, exp, dataDir
}

// 快照先是合法的 SQLite 文件（头 16 字节），再能被读回：应用还在运行（写连接不关、
// 行还留在 WAL 里）时，刚提交的条目必须出现在快照里，且 integrity_check 通过。
func TestWriteSQLiteSnapshotReadableWhileWriterLive(t *testing.T) {
	live, exp, _ := openExportPair(t)

	src := model.Source{Name: "备份验证", Kind: model.KindHTML, URL: "https://example.com/news"}
	if err := live.SaveSource(&src); err != nil {
		t.Fatalf("保存源: %v", err)
	}
	e := model.Entry{SourceID: src.ID, EntryKey: "k1", GUID: "g1", Title: "运行中写入的条目", ContentHash: "h1", FirstSeenAt: 1}
	if inserted, _, err := live.UpsertEntry(&e, "update"); err != nil || !inserted {
		t.Fatalf("写入条目: inserted=%v err=%v", inserted, err)
	}

	var buf bytes.Buffer
	if err := exp.WriteSQLiteSnapshot(&buf); err != nil {
		t.Fatalf("写出快照: %v", err)
	}
	snap := buf.Bytes()
	if len(snap) < 16 || string(snap[:16]) != sqliteMagic {
		t.Fatalf("快照头 = %q, want %q", snap[:min(len(snap), 16)], sqliteMagic)
	}

	path := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(path, snap, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var title string
	if err := db.QueryRow(`SELECT title FROM entries WHERE entry_key = 'k1'`).Scan(&title); err != nil {
		t.Fatalf("读回快照里的条目: %v", err)
	}
	if title != "运行中写入的条目" {
		t.Fatalf("快照里的条目标题 = %q", title)
	}
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil {
		t.Fatal(err)
	}
	if check != "ok" {
		t.Fatalf("integrity_check = %q, want ok", check)
	}
}

// 数据文件不存在就是路径配错：导出必须报错，绝不建目录、绝不凭空造一个空库。
func TestOpenForExportMissingDB(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := OpenForExport(dataDir); err == nil {
		t.Fatal("数据文件缺失时 OpenForExport 应报错")
	}
	if _, err := os.Stat(filepath.Join(dataDir, DBFileName)); !os.IsNotExist(err) {
		t.Fatal("失败的导出不得创建数据库文件")
	}
}

// zip 形态 = 库快照 + secret.key，一份产物还原全部；快照条目本身仍是合法 SQLite。
func TestWriteExportZIPCarriesKeyAndSnapshot(t *testing.T) {
	_, exp, dataDir := openExportPair(t)
	const keyBody = "fake-key-body"
	if err := os.WriteFile(filepath.Join(dataDir, SecretKeyFileName), []byte(keyBody), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	n, err := exp.WriteExportZIP(&buf)
	if err != nil {
		t.Fatalf("写出 zip: %v", err)
	}
	if n != 1 {
		t.Fatalf("库外文件数 = %d, want 1", n)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("解析 zip: %v", err)
	}
	got := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name] = body
	}
	if len(got) != 2 {
		t.Fatalf("zip 条目 = %v, want [siwu.db secret.key]", mapKeys(got))
	}
	snap := got[DBFileName]
	if len(snap) < 16 || string(snap[:16]) != sqliteMagic {
		t.Fatalf("zip 里的快照头 = %q, want %q", snap[:min(len(snap), 16)], sqliteMagic)
	}
	if string(got[SecretKeyFileName]) != keyBody {
		t.Fatalf("zip 里的密钥内容不匹配: %q", got[SecretKeyFileName])
	}
}

// 密钥尚未生成的部署（极端情况）不该让导出失败：zip 里少一个条目仍可恢复库本身。
func TestWriteExportZIPWithoutKeyStillWorks(t *testing.T) {
	_, exp, _ := openExportPair(t)
	var buf bytes.Buffer
	n, err := exp.WriteExportZIP(&buf)
	if err != nil {
		t.Fatalf("无密钥时写出 zip: %v", err)
	}
	if n != 0 {
		t.Fatalf("库外文件数 = %d, want 0", n)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != DBFileName {
		names := make([]string, 0, len(zr.File))
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Fatalf("zip 条目 = %v, want [siwu.db]", names)
	}
}

func mapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
