package prochost

import (
	"errors"
	"runtime"
	"testing"
)

// 围栏原语在受支持平台上必须能读到一个正数上限；不支持的平台必须明确报
// errFenceNotSupported 而不是返回 0——0 会被误读成「上限为零」。
func TestGetNprocLimitReportsPositiveOrNotSupported(t *testing.T) {
	n, err := getNprocLimit()
	switch runtime.GOOS {
	case "darwin", "linux":
		if err != nil {
			t.Fatalf("受支持平台读上限失败: %v", err)
		}
		if n <= 0 {
			t.Fatalf("上限应为正数，得到 %d", n)
		}
	default:
		if !errors.Is(err, errFenceNotSupported) {
			t.Fatalf("不支持的平台应返回 errFenceNotSupported，得到 %v", err)
		}
	}
}

// 非正数围栏值是调用方的 bug，必须当场拒绝：把 RLIMIT_NPROC 设成 0 会让
// 这个进程再也 fork 不出任何东西，是不可逆的自杀。
func TestSetNprocLimitRejectsNonPositive(t *testing.T) {
	for _, n := range []int{0, -1} {
		if err := setNprocLimit(n); err == nil {
			t.Fatalf("围栏值 %d 应被拒绝，却返回了 nil", n)
		}
	}
}
