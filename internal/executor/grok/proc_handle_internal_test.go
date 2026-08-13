// proc_handle_internal_test.go —— ProcHandle 取进程句柄的白盒用例（Task 5）。
//
// 为何不追加进 reap_test.go：该文件是外置包 grok_test，够不到 writeProcInfo /
// quietLogger；本文件以同包白盒补上这两条用例。
package grok

import (
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// TestProcHandleReadsProcInfo 验证 adapter 能把 proc.json 里的进程句柄交出来。
//
// agentd 侧的清扫与计数都靠它取 Handle——取不到就只能降级为「无凭据」，
// 整个足迹功能对该 adapter 静默失效。
func TestProcHandleReadsProcInfo(t *testing.T) {
	dir := t.TempDir()
	want := prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, "shim.lock"), StartedAt: 999}
	if err := writeProcInfo(dir, &procInfo{Handle: want, Port: 1234, Secret: "s"}); err != nil {
		t.Fatalf("写 proc.json 失败: %v", err)
	}
	a := New(quietLogger())
	got, err := a.ProcHandle("task-1", dir)
	if err != nil {
		t.Fatalf("ProcHandle 失败: %v", err)
	}
	if got != want {
		t.Fatalf("Handle 不符：got %+v, want %+v", got, want)
	}
}

// TestProcHandleMissingProcInfo proc.json 不存在时必须报错，不能返回零值 Handle。
//
// 零值 Handle 的 PID=0，classify 会判 no_credential 降级——语义上没错，但
// 调用方拿不到「读失败」这个事实，日志里就少了一条能解释「为什么这个任务
// 从来没被清扫过」的线索。
func TestProcHandleMissingProcInfo(t *testing.T) {
	a := New(quietLogger())
	if _, err := a.ProcHandle("task-1", t.TempDir()); err == nil {
		t.Fatal("proc.json 缺失时应返回错误")
	}
}
