// Package web 内嵌前端产物。dist/ 只提交 .gitkeep 占位，构建时由 scripts/build.sh 填充。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS 返回 dist 子文件系统。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return embedded
	}
	return sub
}

// HasBuild 判断是否真的有构建产物（只有 .gitkeep 时视为未构建）。
func HasBuild() bool {
	entries, err := fs.ReadDir(embedded, "dist")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != ".gitkeep" {
			return true
		}
	}
	return false
}
