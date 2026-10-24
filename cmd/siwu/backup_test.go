package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Felix2yu/siwu/internal/model"
	"github.com/Felix2yu/siwu/internal/store"
)

const backupMagic = "SQLite format 3\x00"

// prepareDataDir 造一个「应用已运行过」的数据目录：有库、有数据、有密钥。
func prepareDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, store.DBFileName))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	src := model.Source{Name: "命令层验证", Kind: model.KindHTML, URL: "https://example.com"}
	if err := st.SaveSource(&src); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, store.SecretKeyFileName), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// 默认形态 db：stdout 只有快照字节，头部是 SQLite 魔数。
func TestRunBackupDBToStdout(t *testing.T) {
	dir := prepareDataDir(t)
	var out bytes.Buffer
	if err := runBackup([]string{"-data", dir}, &out); err != nil {
		t.Fatalf("runBackup: %v", err)
	}
	got := out.Bytes()
	if len(got) < 16 || string(got[:16]) != backupMagic {
		t.Fatalf("stdout 头 = %q, want %q", got[:min(len(got), 16)], backupMagic)
	}
}

// zip 形态：库里库外一并打包，解包能拿到 siwu.db 与 secret.key。
func TestRunBackupZipPacksKey(t *testing.T) {
	dir := prepareDataDir(t)
	var out bytes.Buffer
	if err := runBackup([]string{"-data", dir, "-format", "zip"}, &out); err != nil {
		t.Fatalf("runBackup zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatalf("解析 zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names[store.DBFileName] || !names[store.SecretKeyFileName] {
		t.Fatalf("zip 条目缺件: %v", names)
	}
}

// 数据目录不存在必须报错退出，不能静默产出空库或空包。
func TestRunBackupMissingDataDir(t *testing.T) {
	var out bytes.Buffer
	if err := runBackup([]string{"-data", filepath.Join(t.TempDir(), "nope")}, &out); err == nil {
		t.Fatal("数据目录缺失时应报错")
	}
	if out.Len() != 0 {
		t.Fatal("失败的导出不得往 stdout 写任何字节")
	}
}

// 未知格式要报错（而不是当 db 处理）：工具按扩展名校验，格式错位比失败更危险。
func TestRunBackupUnknownFormat(t *testing.T) {
	dir := prepareDataDir(t)
	var out bytes.Buffer
	err := runBackup([]string{"-data", dir, "-format", "tar"}, &out)
	if err == nil || !strings.Contains(err.Error(), "未知格式") {
		t.Fatalf("未知格式应报错, got %v", err)
	}
}
