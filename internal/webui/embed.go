//go:build embedweb

package webui

import (
	"embed"
	"io/fs"
)

// distFS 是 release 构建时嵌入的前端产物。
//
// all: 前缀是必须的：vite 产物里可能有以 . 或 _ 开头的文件，
// 不加 all: 时 go:embed 会静默跳过它们，症状是页面缺资源而构建全绿。
//
//go:embed all:dist
var distFS embed.FS

// FS 返回控制台静态资源的根文件系统。永不返回 nil。
//
// release 构建下返回嵌入产物的 dist 子树（即 index.html 位于根）。
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// 只有 dist 目录不存在才会走到这里，而那种情况 go:embed 在编译期
		// 就会失败。这里 panic 是为了让「不可能发生」真的发不出去，
		// 而不是静默返回一个空 FS 让页面 404。
		panic("webui: 嵌入产物缺少 dist 子目录: " + err.Error())
	}
	return sub
}

// Embedded 报告当前二进制是否嵌入了真实的前端构建产物。
//
// release 构建下恒为 true。
func Embedded() bool { return true }
