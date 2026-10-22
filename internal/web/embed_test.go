package web

import (
	"io/fs"
	"testing"
)

func TestFSIsWalkable(t *testing.T) {
	root := FS()
	if root == nil {
		t.Fatal("FS() 返回 nil")
	}
	count := 0
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("遍历失败: %v", err)
	}
	if count == 0 {
		t.Fatal("dist 目录为空，至少应有 .gitkeep 占位")
	}
}

// 占位态（只有 .gitkeep）与构建态都必须自洽：HasBuild 只能说明有没有产物，
// 不能因为没构建就 panic。
func TestHasBuildMatchesContents(t *testing.T) {
	entries, err := fs.ReadDir(FS(), ".")
	if err != nil {
		t.Fatalf("读 dist 失败: %v", err)
	}
	real := 0
	for _, e := range entries {
		if e.Name() != ".gitkeep" {
			real++
		}
	}
	if got := HasBuild(); got != (real > 0) {
		t.Fatalf("HasBuild()=%v，实际产物数=%d", got, real)
	}
}
