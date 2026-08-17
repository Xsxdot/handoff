//go:build embedbin

package embedbin_test

import (
	"io"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/embedbin"
)

// 只在 release 构建路径跑。这道门存在的理由与 webui 那道相同：
// go:embed 一个 0 字节占位文件也能编译通过，「编译过了」不代表
// 里面真的是一个可执行的 handoff。
func TestEmbeddedBinaryIsPlausible(t *testing.T) {
	if !embedbin.Available() {
		t.Fatal("带 embedbin 标签时 Available() 必须为 true")
	}
	rc, err := embedbin.Open()
	if err != nil {
		t.Fatalf("Open() 失败：%v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("读取内嵌二进制失败：%v", err)
	}
	// handoff 的 release 产物是 18MB 量级；低于 1MB 说明嵌进来的
	// 不是真产物（多半是占位文件或半截拷贝）
	if n < 1<<20 {
		t.Fatalf("内嵌二进制只有 %d 字节，不像真产物", n)
	}
}
