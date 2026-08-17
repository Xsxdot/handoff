//go:build !embedbin

package embedbin_test

import (
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/embedbin"
)

// 默认构建下必须诚实地报告「没有内嵌二进制」，且 Open 必须返回错误
// 而不是一个空 reader——释出一个 0 字节的 handoff 比不释出坏得多。
func TestStubReportsUnavailable(t *testing.T) {
	if embedbin.Available() {
		t.Fatal("不带 embedbin 标签时 Available() 必须为 false")
	}
	if _, err := embedbin.Open(); err == nil {
		t.Fatal("不带 embedbin 标签时 Open() 必须返回错误")
	}
}
