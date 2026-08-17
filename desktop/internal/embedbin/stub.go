//go:build !embedbin

package embedbin

import (
	"errors"
	"io"
)

// ErrNotEmbedded 是默认构建下 Open 返回的错误。
//
// 必须**诚实**：不能释出一个 0 字节的假 handoff，那比不释出坏得多。
var ErrNotEmbedded = errors.New("embedbin: 本二进制未带 -tags embedbin 构建，不包含内嵌 handoff")

// Available 报告当前二进制是否嵌入了真实的 handoff 产物。
//
// 默认构建下恒为 false。调用方（如 shell.DecideRelease）应据此走保守分支：
// 用用户已有的 handoff，不要覆盖。
func Available() bool { return false }

// Open 返回内嵌 handoff 二进制的只读流。
//
// 默认构建下恒返回 ErrNotEmbedded。报文直接点明「未带 -tags embedbin」，
// 而不是笼统的 "not available"——后者出现在用户机器上时没人知道下一步该干什么。
func Open() (io.ReadCloser, error) {
	return nil, ErrNotEmbedded
}
