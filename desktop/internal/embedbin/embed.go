//go:build embedbin

package embedbin

import (
	"embed"
	"io"
)

// handoff 是 release 构建时嵌入的同目录产物文件。
//
//go:embed handoff
var handoff embed.FS

// Available 报告当前二进制是否嵌入了真实的 handoff 产物。
//
// release 构建下恒为 true。
func Available() bool { return true }

// Open 返回内嵌 handoff 二进制的只读流。
//
// release 构建下恒返回真产物；文件名 go:embed 在编译期已保证存在，
// 读不到只能说明 embed 包自身出了问题，直接 panic 不静默吞错。
func Open() (io.ReadCloser, error) {
	f, err := handoff.Open("handoff")
	if err != nil {
		panic("embedbin: 打开内嵌 handoff 失败: " + err.Error())
	}
	return f, nil
}
