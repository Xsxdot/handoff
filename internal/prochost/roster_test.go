package prochost

import (
	"sort"
	"testing"
	"time"
)

// 一条 shim→executor→bash→go 的链，中间那一层 setsid 逃逸（pgid 自成一组）。
// 闭包必须靠 ppid 走到底，pgid 变了不影响。
func TestDescendantsOfFollowsPPIDAcrossSetsid(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 1000},   // shim（root）
		{PID: 101, PPID: 100, PGID: 100, StartedAt: 1100}, // executor，同组
		{PID: 102, PPID: 101, PGID: 102, StartedAt: 1200}, // bash 工具，setsid 逃逸
		{PID: 103, PPID: 102, PGID: 102, StartedAt: 1300}, // go test，逃逸树内
		{PID: 200, PPID: 1, PGID: 200, StartedAt: 1150},   // 无关进程
	}
	got := descendantsOf(100, procs)
	want := []rosterEntry{
		{PID: 101, StartedAt: 1100},
		{PID: 102, StartedAt: 1200},
		{PID: 103, StartedAt: 1300},
	}
	assertRoster(t, got, want)
}

// root 自己绝不能进名册：第二段清扫会对名册里每个 pid 发信号，shim 自己在里面
// 等于自杀，而且第一段的 pgid 清扫本来就覆盖它。
func TestDescendantsOfExcludesRoot(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 1000},
		{PID: 101, PPID: 100, PGID: 100, StartedAt: 1100},
	}
	for _, e := range descendantsOf(100, procs) {
		if e.PID == 100 {
			t.Fatalf("root 不得出现在名册里: %+v", descendantsOf(100, procs))
		}
	}
}

// 自环/互指不能让闭包死循环——真实进程表里 pid 1 的 ppid 就是 0 或 1，
// 而被 reparent 的进程在快照的两条记录之间也可能出现看起来成环的形态。
func TestDescendantsOfTerminatesOnCycle(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PPID: 100, PGID: 100, StartedAt: 1000}, // 自环
		{PID: 101, PPID: 102, PGID: 101, StartedAt: 1100}, // 与 102 互指
		{PID: 102, PPID: 101, PGID: 102, StartedAt: 1200},
	}
	done := make(chan []rosterEntry, 1)
	go func() { done <- descendantsOf(100, procs) }()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("自环 root 不该有后代，得到 %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("descendantsOf 未终止，闭包缺少 visited 保护")
	}
}

// 空表 / root 不在表里：返回空名册且不 panic（fail-open 的最基本形态）。
func TestDescendantsOfEmptyInputs(t *testing.T) {
	if got := descendantsOf(100, nil); len(got) != 0 {
		t.Fatalf("空进程表应得空名册，得到 %+v", got)
	}
	procs := []procEntry{{PID: 200, PPID: 1, PGID: 200, StartedAt: 1000}}
	if got := descendantsOf(100, procs); len(got) != 0 {
		t.Fatalf("root 不在表里应得空名册，得到 %+v", got)
	}
}

// assertRoster 按 pid 排序后逐条比对（闭包的遍历顺序不是契约的一部分）。
func assertRoster(t *testing.T, got, want []rosterEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("名册条数不符: got %+v, want %+v", got, want)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].PID < got[j].PID })
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("名册第 %d 条不符: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
