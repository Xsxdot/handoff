package prochost

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestEnumProcsFindsSelf 验证枚举能找到本进程，且 pgid 与内核一致。
// 这是整套足迹判据的地基：pgid 读错，规则一二三全部失去意义。
func TestEnumProcsFindsSelf(t *testing.T) {
	procs, err := enumProcs()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if !errors.Is(err, errNotSupported) {
			t.Fatalf("非 darwin/linux 应返回 errNotSupported，got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("enumProcs 失败: %v", err)
	}
	self := os.Getpid()
	wantPGID, err := syscall.Getpgid(self)
	if err != nil {
		t.Fatalf("Getpgid 失败: %v", err)
	}
	for _, p := range procs {
		if p.PID != self {
			continue
		}
		if p.PGID != wantPGID {
			t.Fatalf("本进程 pgid 读错：got %d, want %d", p.PGID, wantPGID)
		}
		// 本进程必然启动于「现在」之前、且不早于一年前——粗窗口足以抓出
		// 单位换算错误（秒当纳秒会落到 1970，jiffies 未换算会落到未来）
		now := time.Now().UnixNano()
		if p.StartedAt <= 0 || p.StartedAt > now || p.StartedAt < now-int64(365*24*time.Hour) {
			t.Fatalf("本进程 StartedAt 不合理：%d（now=%d）", p.StartedAt, now)
		}
		return
	}
	t.Fatalf("枚举结果里没有本进程 pid=%d（共 %d 条）", self, len(procs))
}

// 枚举结果里本进程那条的 PPID 必须等于内核认的父进程——这是后代闭包唯一的
// 链接字段，两个平台各写各的解析，用真进程对一次比任何桩都可靠。
func TestEnumProcsFillsPPID(t *testing.T) {
	procs, err := enumProcs()
	if err != nil {
		t.Skipf("本平台不支持进程枚举: %v", err)
	}
	self, ppid := os.Getpid(), os.Getppid()
	for _, p := range procs {
		if p.PID == self {
			if p.PPID != ppid {
				t.Fatalf("本进程 PPID 应为 %d，枚举得到 %d", ppid, p.PPID)
			}
			return
		}
	}
	t.Fatalf("枚举结果里没有本进程 pid=%d", self)
}

// TestProcLimitPositive 验证能读到每 uid 上限。
func TestProcLimitPositive(t *testing.T) {
	n, err := procLimit()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if !errors.Is(err, errNotSupported) {
			t.Fatalf("非 darwin/linux 应返回 errNotSupported，got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("procLimit 失败: %v", err)
	}
	if n <= 0 {
		t.Fatalf("上限应为正数，got %d", n)
	}
}
