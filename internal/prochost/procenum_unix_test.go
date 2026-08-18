//go:build unix

// procenum_unix_test.go —— procenum 的 unix 内核字段测试。
//
// 职责：验证 unix 平台枚举到的进程组 ID 与内核一致。
// 边界：只覆盖 syscall.Getpgid 这类 unix-only 断言，Windows 保留平台中立的枚举测试。
package prochost

import (
	"os"
	"syscall"
	"testing"
)

// TestEnumProcsFindsSelfPGID 验证枚举能找到本进程，且 pgid 与内核一致。
// 这是整套足迹判据的地基：pgid 读错，规则一二三全部失去意义。
func TestEnumProcsFindsSelfPGID(t *testing.T) {
	procs, err := enumProcs()
	if err != nil {
		t.Fatalf("enumProcs 失败: %v", err)
	}
	wantPGID, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("Getpgid 失败: %v", err)
	}
	for _, p := range procs {
		if p.PID == os.Getpid() {
			if p.PGID != wantPGID {
				t.Fatalf("本进程 pgid 读错：got %d, want %d", p.PGID, wantPGID)
			}
			return
		}
	}
	t.Fatalf("枚举结果里没有本进程 pid=%d", os.Getpid())
}
