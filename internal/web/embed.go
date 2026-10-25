// Package web 内嵌前端产物。dist/ 只提交 .gitkeep 占位，构建时由 scripts/build.sh 填充。
package web

import (
	"embed"
	"io/fs"
	"mime"
)

// 前端资产自带类型映射：scratch 容器里没有系统 mime 表，SVG 图标和
// manifest 会被 ServeContent 嗅探成 text/plain，部分浏览器会拒绝加载。
func init() {
	for ext, typ := range map[string]string{
		".svg":         "image/svg+xml",
		".ico":         "image/x-icon",
		".webmanifest": "application/manifest+json",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

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
