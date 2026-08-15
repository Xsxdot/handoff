package prochost

import (
	"os"
	"path/filepath"
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

// 环不能让闭包死循环——真实进程表里 pid 1 的 ppid 就是 0 或 1（自环），被
// reparent 的进程在快照的两条记录之间也可能出现看起来成环的形态。
//
// fixture 必须是**从 root 可达**的互指 2-环：root 自环那种形态会被建索引时的
// 自环跳过挡掉、根本走不进 BFS，删掉 visited 也不会死循环（2026-08-12 变异
// 检验实证：原 fixture 的 101↔102 互指对 root 100 不可达，visited 有没有都在
// 场不影响终止性，那条用例是空转的）。这里 100↔101 互相为父，有 visited 时
// 回到 100 被挡住、正常终止；没有 visited 时无限互推——变异才抓得住。
func TestDescendantsOfTerminatesOnCycle(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PPID: 101, PGID: 100, StartedAt: 1000}, // root，与 101 互指
		{PID: 101, PPID: 100, PGID: 101, StartedAt: 1100},
	}
	done := make(chan []rosterEntry, 1)
	go func() { done <- descendantsOf(100, procs) }()
	select {
	case got := <-done:
		assertRoster(t, got, []rosterEntry{{PID: 101, StartedAt: 1100}})
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

// 写—读往返：落盘的内容必须原样读回来。
func TestWriteReadRosterRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), RosterFileName)
	want := []rosterEntry{{PID: 101, StartedAt: 1100}, {PID: 102, StartedAt: 1200}}
	if err := writeRoster(path, want); err != nil {
		t.Fatalf("写名册: %v", err)
	}
	got, err := readRoster(path)
	if err != nil {
		t.Fatalf("读名册: %v", err)
	}
	assertRoster(t, got, want)
}

// 名册不存在**不是错误**：任务刚起来还没到第一次落盘、或这是升级前建的老任务，
// 都是正常形态。Sweep 靠 (nil, nil) 安静跳过第二段，不能因此把清扫判成失败。
func TestReadRosterMissingIsNotError(t *testing.T) {
	got, err := readRoster(filepath.Join(t.TempDir(), RosterFileName))
	if err != nil {
		t.Fatalf("名册缺失不该报错，得到 %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("名册缺失应得空名册，得到 %+v", got)
	}
}

// 名册损坏必须**报错**而不是当成空名册：空名册意味着「确实没有后代」，
// 损坏意味着「有后代但我读不出来」，两者对调用方是不同的决定（后者要打日志
// 让人看见），这与 errNotSupported 不能退化成空集是同一条纪律。
func TestReadRosterCorruptReportsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), RosterFileName)
	if err := os.WriteFile(path, []byte("{不是 json"), 0o600); err != nil {
		t.Fatalf("造损坏文件: %v", err)
	}
	if _, err := readRoster(path); err == nil {
		t.Fatal("损坏的名册必须报错，不得静默当成空名册")
	}
}

// 落盘必须原子：读者任何时刻看到的要么是上一版完整名册，要么是新版完整名册，
// 不存在读到半截的窗口。这里断言临时文件没有残留，且权限是 0600（名册暴露
// 本机进程结构，与 spec.json 同级别对待）。
func TestWriteRosterIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, RosterFileName)
	if err := writeRoster(path, []rosterEntry{{PID: 101, StartedAt: 1100}}); err != nil {
		t.Fatalf("写名册: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录: %v", err)
	}
	if len(ents) != 1 || ents[0].Name() != RosterFileName {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("目录里应只剩名册本身（临时文件必须已 rename 掉），实得 %v", names)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("名册权限应为 0600，实得 %v", fi.Mode().Perm())
	}
}

func TestMergeRosterKeepsEscapedDescendant(t *testing.T) {
	// 第一轮：工具壳 200 与它的子进程 300 都能由 ppid 闭包到
	prev := []rosterEntry{{PID: 200, StartedAt: 1000}, {PID: 300, StartedAt: 1100}}
	// 第二轮：工具壳已退出，300 被 reparent 给 launchd（ppid=1），闭包走不到它
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 900},   // shim
		{PID: 300, PPID: 1, PGID: 200, StartedAt: 1100},  // 逃逸后代，仍活着
	}
	cur := descendantsOf(100, procs) // 本轮闭包为空
	got := mergeRoster(prev, cur, procs)
	if len(got) != 1 || got[0].PID != 300 || got[0].StartedAt != 1100 {
		t.Fatalf("逃逸后代必须留在名册里，got=%v", got)
	}
}

func TestMergeRosterDropsDeadAndReusedPID(t *testing.T) {
	prev := []rosterEntry{
		{PID: 300, StartedAt: 1100}, // 已从进程表消失
		{PID: 400, StartedAt: 1200}, // pid 还在，但 StartedAt 变了 = pid 被复用
	}
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 900},
		{PID: 400, PPID: 1, PGID: 400, StartedAt: 9999},
	}
	got := mergeRoster(prev, nil, procs)
	if len(got) != 0 {
		t.Fatalf("已死与 pid 复用的条目都必须剪掉，got=%v", got)
	}
}

func TestMergeRosterUnionsAndSortsByPID(t *testing.T) {
	prev := []rosterEntry{{PID: 500, StartedAt: 1000}}
	cur := []rosterEntry{{PID: 300, StartedAt: 1100}, {PID: 500, StartedAt: 1000}}
	procs := []procEntry{
		{PID: 300, PPID: 100, PGID: 100, StartedAt: 1100},
		{PID: 500, PPID: 100, PGID: 100, StartedAt: 1000},
	}
	got := mergeRoster(prev, cur, procs)
	if len(got) != 2 || got[0].PID != 300 || got[1].PID != 500 {
		t.Fatalf("并集须去重且按 pid 升序，got=%v", got)
	}
}

func TestMarshalRosterStableForSameSet(t *testing.T) {
	a, err := marshalRoster([]rosterEntry{{PID: 2, StartedAt: 20}, {PID: 1, StartedAt: 10}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := marshalRoster([]rosterEntry{{PID: 2, StartedAt: 20}, {PID: 1, StartedAt: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("同一批条目的序列化结果必须逐字节一致：%s vs %s", a, b)
	}
}
