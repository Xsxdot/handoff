package prochost

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sleepCmd 返回一条「活若干秒」的命令，按平台给出可执行体与参数。
//
// 为什么不能写死 /bin/sh：Windows 上没有它，Start 会以
// `exec: "/bin/sh": executable file not found in %PATH%` 直接失败。
// 2026-08-18 本分支的 Windows 用例第一次在 CI 上跑（run 32149311654），
// TestStartRecordsStartedAt 与 TestStartRecordsRosterPath 就是这么红的。
//
// Windows 侧用 ping 而不是 timeout：timeout.exe 在 stdin 被重定向时会直接
// 报 "Input redirection is not supported" 退出，而 shim 一定会重定向 stdin，
// 于是「用来占住几秒的进程」瞬间就没了，用例反而更难查。
// ping 发 secs+1 个包、包间隔 1 秒，约等于睡 secs 秒。
func sleepCmd(secs int) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/c", "ping", "-n", strconv.Itoa(secs + 1), "127.0.0.1"}
	}
	return "/bin/sh", []string{"-c", "sleep " + strconv.Itoa(secs)}
}

// 判据测试的固定基准：shim pid=100，启动于 t0。
const (
	testShimPID = 100
	t0          = int64(1_000_000)
)

func h() Handle { return Handle{PID: testShimPID, StartedAt: t0} }

// TestClassifyLockHeldCountsGroup 锁仍被持有 ⇒ 组长就是我们的 shim，正常计数。
//
// 这条守的是 status 的 per-task 计数：若规则一不看锁状态、一律把
// 「存在 pid==pgid 的活进程」判成复用，所有**运行中**的任务都会被误判，
// per-task 进程数将永远取不到值。
func TestClassifyLockHeldCountsGroup(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0},     // shim 自己（组长）
		{PID: 101, PGID: 100, StartedAt: t0 + 1}, // executor
		{PID: 102, PGID: 100, StartedAt: t0 + 2}, // 孙进程
		{PID: 200, PGID: 200, StartedAt: t0},     // 无关进程
	}
	got, v := classify(h(), procs, true)
	if v != VerdictOK {
		t.Fatalf("锁被持有时应为 ok，got %s", v)
	}
	assertMembers(t, got, []int{100, 101, 102})
}

// TestClassifyLeaderReuseAborts 锁已释放 + 组内有活的 pid==pgid ⇒ pgid 被复用，整组放弃。
func TestClassifyLeaderReuseAborts(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0 + 9999}, // 冒名者：pid 被复用且当了组长
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
	}
	got, v := classify(h(), procs, false)
	if v != VerdictLeaderReuse {
		t.Fatalf("应判定 leader_reuse，got %s", v)
	}
	if len(got) != 0 {
		t.Fatalf("判定复用时必须返回空集（一个都不能碰），got %v", got)
	}
}

// TestClassifyNoCredential StartedAt 缺失（老 proc.json）⇒ 凭据不全，放弃。
func TestClassifyNoCredential(t *testing.T) {
	procs := []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}
	got, v := classify(Handle{PID: testShimPID, StartedAt: 0}, procs, false)
	if v != VerdictNoCredential {
		t.Fatalf("应判定 no_credential，got %s", v)
	}
	if len(got) != 0 {
		t.Fatalf("凭据不全时必须返回空集，got %v", got)
	}
}

// TestClassifyExcludesMemberStartedBeforeShim 成员启动早于 shim ⇒ 排除（规则三双保险）。
func TestClassifyExcludesMemberStartedBeforeShim(t *testing.T) {
	procs := []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 - 1}, // 比 shim 还早：不可能是它的后代
	}
	got, v := classify(h(), procs, false)
	if v != VerdictOK {
		t.Fatalf("应为 ok，got %s", v)
	}
	assertMembers(t, got, []int{101})
}

// TestClassifyDeadLeaderNormal 锁已释放、无复用者 ⇒ 正常返回残留后代。
func TestClassifyDeadLeaderNormal(t *testing.T) {
	procs := []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 + 2},
		{PID: 300, PGID: 300, StartedAt: t0},
	}
	got, v := classify(h(), procs, false)
	if v != VerdictOK {
		t.Fatalf("应为 ok，got %s", v)
	}
	assertMembers(t, got, []int{101, 102})
}

func assertMembers(t *testing.T, got, want []int) {
	t.Helper()
	sort.Ints(got)
	if len(got) != len(want) {
		t.Fatalf("成员数不符：got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("成员不符：got %v, want %v", got, want)
		}
	}
}

func containsPID(pids []int, want int) bool {
	for _, pid := range pids {
		if pid == want {
			return true
		}
	}
	return false
}

func countPID(pids []int, want int) int {
	n := 0
	for _, pid := range pids {
		if pid == want {
			n++
		}
	}
	return n
}

// stubEnum 把进程枚举换成脚本化的快照序列（最后一个快照会被重复使用）。
//
// 为什么需要序列而不是固定快照：Sweep 发完 SIGKILL 后要**复核**——再枚举一次并
// 确认组已空。固定快照会让复核永远看到同一批进程，正常路径的用例就只能走满复核
// 窗口被报成 ErrStillAlive。与 stubAlive 同款路数：先给「杀之前的现场」，再给
// 「杀之后的空现场」（more 里传 nil 即空快照）。只传 procs 时退化为固定快照，
// 与既有调用完全兼容。
func stubEnum(t *testing.T, procs []procEntry, err error, more ...[]procEntry) {
	t.Helper()
	orig := enumProcsFn
	snaps := append([][]procEntry{procs}, more...)
	i := 0
	enumProcsFn = func() ([]procEntry, error) {
		v := snaps[i]
		if i < len(snaps)-1 {
			i++
		}
		return v, err
	}
	t.Cleanup(func() { enumProcsFn = orig })
}

// TestFootprintUsesLockState 验证 Footprint 把存活锁状态正确喂给 classify。
func TestFootprintUsesLockState(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0},
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
	}
	stubEnum(t, procs, nil)

	stubAlive(t, true) // shim 活着
	got, v, err := Footprint(h())
	if err != nil || v != VerdictOK || len(got) != 2 {
		t.Fatalf("锁被持有：want ok/2 成员，got v=%s members=%v err=%v", v, got, err)
	}

	stubAlive(t, false) // shim 死了，且组长位置有活进程 ⇒ 复用
	_, v, err = Footprint(h())
	if err != nil || v != VerdictLeaderReuse {
		t.Fatalf("锁已释放：want leader_reuse，got v=%s err=%v", v, err)
	}
}

// TestStartRecordsStartedAt 验证 Start 落下的 Handle 带得到启动时刻。
//
// 这条是整个时间下界判据的源头：StartedAt 恒为 0，规则三永远降级为 no_credential，
// 清扫功能等于没上线。
func TestStartRecordsStartedAt(t *testing.T) {
	if !LockSupported() {
		t.Skip("本平台不支持文件锁")
	}
	dir := t.TempDir()
	sleepExe, sleepArgs := sleepCmd(5)
	spec := Spec{
		Argv:     append([]string{sleepExe}, sleepArgs...),
		Dir:      dir,
		Stdout:   filepath.Join(dir, "out.log"),
		Stderr:   filepath.Join(dir, "err.log"),
		LockPath: filepath.Join(dir, "shim.lock"),
		InfoPath: filepath.Join(dir, "proc.json"),
	}
	// selfExe 直接用一条 sleep 命令顶替真 shim：本用例只验 StartedAt 有没有被填上，
	// 不验 shim 行为（拿锁、读 spec.json 那些由 shim 自己的用例覆盖）
	hd, err := Start(spec, sleepExe, sleepArgs...)
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = killGroup(hd.PID) })

	// Windows 上读不到启动时刻，而且这是**刻意的**：那儿没有进程枚举
	//（procenum_other.go 的注释写明「回收职责已由 Job Object 承担，缺的只是
	// 足迹观测」），所以 Start 会打一条 warn 并把 StartedAt 留成 0。
	// 这条用例验的是「时间下界判据的源头」，那套判据本身就只在 Unix 上成立；
	// 在 Windows 上只断言 Start 确实拉起了进程，不去断言一个该平台不产出的值
	//（也不写死 StartedAt==0：将来真给 Windows 补了枚举，这里不该因此翻红）。
	if runtime.GOOS == "windows" {
		if hd.PID <= 0 {
			t.Fatalf("Start 应返回可用的 PID，got %d", hd.PID)
		}
		return
	}
	if hd.StartedAt <= 0 {
		t.Fatalf("Start 未记录 StartedAt，got %d", hd.StartedAt)
	}
	if delta := time.Now().UnixNano() - hd.StartedAt; delta < 0 || delta > int64(30*time.Second) {
		t.Fatalf("StartedAt 偏离现在过远：delta=%d ns", delta)
	}
}

// TestSweepAliveSkipsGroupPhaseButStillRosterKills 验证 executor 存活时的降级形态：
// 段①（按 pgid 整组杀）必须跳过——它会连 executor 本体一起端掉；段②（按出生名册
// 点名）照常执行——每条成员自带 pid+出生时刻双重凭据，与 executor 的死活无关。
//
// 这条是 B119 的核心：改前两段一起放弃，导致「回合结束收掉 setsid 逃逸后代」这个
// 唯一目的从未达成（生产日志 118 次拒绝，真正跑完的清扫仅 34 次）。
func TestSweepAliveSkipsGroupPhaseButStillRosterKills(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{
		{PID: 501, StartedAt: 5100},
		{PID: 502, StartedAt: 5200},
	}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true)
	killed := stubKillProc(t)
	groupN := stubKillGroup(t, nil)
	stubEnum(t, []procEntry{
		{PID: 501, PPID: 1, PGID: 501, StartedAt: 5100},
		{PID: 502, PPID: 1, PGID: 502, StartedAt: 5200},
	}, nil)

	n, v, err := Sweep(Handle{PID: 100, StartedAt: t0, RosterPath: roster})
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("执行者存活时应返回 ErrExecutorAlive 表示段①跳过，got %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	if n != 2 {
		t.Fatalf("段②应回收 2 个名册成员，实得 %d", n)
	}
	if len(*killed) != 2 {
		t.Fatalf("应逐个发信号回收 2 条，实得 %v", *killed)
	}
	if *groupN != 0 {
		t.Fatalf("执行者存活时绝不能按组杀，实得组信号 %d 次", *groupN)
	}
}

// TestSweepAliveNeverSignalsExecutorItself 是本次唯一新增的误杀面的守门用例：
// 段②降级执行后，若名册里因任何原因含有 executor 本体的 pid，逐个发信号就会
// 杀掉一个活着的 executor——这正是段①被跳过所要避免的事。判据是 h.PID，
// 不依赖名册内容的正确性。
func TestSweepAliveNeverSignalsExecutorItself(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{
		{PID: 100, StartedAt: t0},   // executor 本体
		{PID: 501, StartedAt: 5100}, // 正常逃逸后代
	}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true)
	killed := stubKillProc(t)
	stubKillGroup(t, nil)
	stubEnum(t, []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: t0},
		{PID: 501, PPID: 1, PGID: 501, StartedAt: 5100},
	}, nil)

	n, _, err := Sweep(Handle{PID: 100, StartedAt: t0, RosterPath: roster})
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("应返回 ErrExecutorAlive，got %v", err)
	}
	if n != 1 {
		t.Fatalf("应只回收 501 这一条，实得 %d", n)
	}
	for _, pid := range *killed {
		if pid == 100 {
			t.Fatalf("对 executor 本体 pid=100 发了信号，名单 %v", *killed)
		}
	}
}

// TestSweepAliveWithoutRosterIsNoop 覆盖降级路径的下界：没有名册（升级前建的任务、
// 或 shim 还没来得及落第一次名册就死了）时段②无事可做，段①仍被跳过——结论必须是
// 「回收 0 个」而不是 panic 或误判为失败。
func TestSweepAliveWithoutRosterIsNoop(t *testing.T) {
	stubAlive(t, true)
	killed := stubKillProc(t)
	groupN := stubKillGroup(t, nil)
	stubEnum(t, []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}, nil)

	n, v, err := Sweep(Handle{PID: 100, StartedAt: t0}) // RosterPath 为空
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("应返回 ErrExecutorAlive，got %v", err)
	}
	if v != VerdictOK || n != 0 {
		t.Fatalf("无名册时应回收 0 个且 verdict ok，实得 n=%d v=%s", n, v)
	}
	if len(*killed) != 0 || *groupN != 0 {
		t.Fatalf("无名册时不该发任何信号，实得逐个 %v / 组 %d 次", *killed, *groupN)
	}
}

// TestSweepAbortsOnLeaderReuse pgid 被复用时必须整组放弃且**绝不发信号**。
//
// 这是本次改动最重要的一条：误杀被复用 pgid 的代价是杀掉机器上毫不相干的
// 进程组（B47 现场：旧实现 300 条成功命令误杀 114 次）。
func TestSweepAbortsOnLeaderReuse(t *testing.T) {
	stubEnum(t, []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0 + 9999}, // 冒名组长
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
	}, nil)
	stubAlive(t, false)
	n := stubKillGroup(t, nil)

	killed, v, err := Sweep(h())
	if err != nil {
		t.Fatalf("判定复用不该返回错误（那是正常结论），got %v", err)
	}
	if v != VerdictLeaderReuse {
		t.Fatalf("want leader_reuse, got %s", v)
	}
	if killed != 0 || *n != 0 {
		t.Fatalf("判定复用却动了手：killed=%d signals=%d", killed, *n)
	}
}

// TestSweepNoCredentialAborts 凭据不全时放弃且不发信号。
func TestSweepNoCredentialAborts(t *testing.T) {
	stubEnum(t, []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}, nil)
	stubAlive(t, false)
	n := stubKillGroup(t, nil)

	killed, v, err := Sweep(Handle{PID: testShimPID, StartedAt: 0})
	if err != nil || v != VerdictNoCredential || killed != 0 || *n != 0 {
		t.Fatalf("凭据不全应放弃：v=%s killed=%d signals=%d err=%v", v, killed, *n, err)
	}
}

// TestSweepKillsGroupOnce 正常路径：恰好一次组信号，返回成员数。
func TestSweepKillsGroupOnce(t *testing.T) {
	shrinkBackoff(t)
	stubEnum(t, []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 + 2},
	}, nil, nil) // 第二个快照为空：复核时确认组已清
	stubAlive(t, false)
	n := stubKillGroup(t, nil)

	killed, v, err := Sweep(h())
	if err != nil {
		t.Fatalf("正常路径不该报错: %v", err)
	}
	if v != VerdictOK || killed != 2 {
		t.Fatalf("want ok/2, got %s/%d", v, killed)
	}
	if *n != 1 {
		t.Fatalf("应恰好发一次组信号，实发 %d 次", *n)
	}
}

// TestSweepAndFootprintAgree 孪生一致性：同一输入下，两者的成员集合必须完全相同。
//
// 这条钉住整个设计的核心不变式——「数出来的」与「会被杀的」是同一批。
// 两者若各写一份枚举/过滤，status 报 3 个而 Sweep 杀 5 个这种事没人会发现。
func TestSweepAndFootprintAgree(t *testing.T) {
	shrinkBackoff(t)
	procs := []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 - 5}, // 规则三排除
		{PID: 103, PGID: 100, StartedAt: t0 + 3},
		{PID: 200, PGID: 200, StartedAt: t0 + 1}, // 别的组
	}
	stubEnum(t, procs, nil, procs, nil) // 顺序：Footprint 一次、Sweep 一次、复核一次空快照
	stubAlive(t, false)
	_ = stubKillGroup(t, nil)

	members, v1, err1 := Footprint(h())
	killed, v2, err2 := Sweep(h())
	if err1 != nil || err2 != nil {
		t.Fatalf("不该报错: %v / %v", err1, err2)
	}
	if v1 != v2 {
		t.Fatalf("孪生判定不一致：Footprint=%s Sweep=%s", v1, v2)
	}
	if len(members) != killed {
		t.Fatalf("孪生成员数不一致：Footprint=%d Sweep=%d", len(members), killed)
	}
}

// Start 必须把名册路径记进 Handle：Sweep 在 agentd 进程里跑，它只有 proc.json
// 反序列化出来的 Handle，没有 spec，推不出任务目录。这个字段是两个进程之间
// 唯一的交接点，漏填的表现是「第二段清扫永远静默地不干活」。
func TestStartRecordsRosterPath(t *testing.T) {
	if !LockSupported() {
		t.Skip("本平台不支持文件锁")
	}
	dir := t.TempDir()
	sleepExe, sleepArgs := sleepCmd(5)
	spec := Spec{
		Argv:     append([]string{sleepExe}, sleepArgs...),
		Dir:      dir,
		Stdout:   filepath.Join(dir, "out.log"),
		Stderr:   filepath.Join(dir, "err.log"),
		LockPath: filepath.Join(dir, "shim.lock"),
		InfoPath: filepath.Join(dir, "proc.json"),
	}
	hd, err := Start(spec, sleepExe, sleepArgs...)
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = killGroup(hd.PID) })
	want := filepath.Join(dir, RosterFileName)
	if hd.RosterPath != want {
		t.Fatalf("Handle.RosterPath 应为 %s，实得 %q", want, hd.RosterPath)
	}
}

// 升级前写下的 proc.json 没有 roster_path 字段，读出来是空串。这必须是一条
// 安静的降级路径（只做第一段清扫），不是错误——老任务不该因为升级就被动手。
func TestHandleWithoutRosterPathDecodesEmpty(t *testing.T) {
	var h Handle
	if err := json.Unmarshal([]byte(`{"pid":100,"lock_path":"/tmp/x.lock","started_at":1000}`), &h); err != nil {
		t.Fatalf("解析老 proc.json: %v", err)
	}
	if h.RosterPath != "" {
		t.Fatalf("老 proc.json 应解出空 RosterPath，实得 %q", h.RosterPath)
	}
}

// stubKillProc 记录单 pid 信号的调用序列，供第二段清扫的用例断言「杀了谁」。
func stubKillProc(t *testing.T) *[]int {
	t.Helper()
	var got []int
	orig := killProcFn
	killProcFn = func(pid int) error { got = append(got, pid); return nil }
	t.Cleanup(func() { killProcFn = orig })
	return &got
}

// 名册里的 pid 存活且出生时刻完全一致 —— 点名回收。
func TestSweepKillsRosterMembers(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	shrinkBackoff(t)
	stubAlive(t, false)
	killed := stubKillProc(t)
	// **必须同时 stub 组信号**：不 stub 就会对 pgid=100 发真 SIGKILL；而且这里
	// 断言它调用 0 次本身就是判据——第二段绝不能退化成按组杀
	groupN := stubKillGroup(t, nil)
	// 第一段：组内只有 501 且它自成一组，shim 的组是空的 → 无成员；
	// 第二段：501 在表里且出生时刻吻合 → 点名回收
	stubEnum(t, []procEntry{{PID: 501, PPID: 1, PGID: 501, StartedAt: 5100}}, nil)
	h := Handle{PID: 100, StartedAt: t0, RosterPath: roster}
	n, v, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	if n != 1 {
		t.Fatalf("应回收 1 个名册成员，实得 %d", n)
	}
	if len(*killed) != 1 || (*killed)[0] != 501 {
		t.Fatalf("应对 pid 501 单独发信号，实得 %v", *killed)
	}
	if *groupN != 0 {
		t.Fatalf("点名回收必须逐个发信号，不得按组杀，实得组信号 %d 次", *groupN)
	}
}

// **B47 红线**：pid 还在，但出生时刻对不上 —— pid 已易主，绝不发信号。
// 这条用例是整个 Plan B 最重要的一条：判据松掉的代价是杀掉用户的无关进程。
func TestSweepSkipsRosterMemberWithMismatchedBirth(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	shrinkBackoff(t)
	stubAlive(t, false)
	killed := stubKillProc(t)
	// pid 501 还在，但它的出生时刻是 9999——这是内核把 501 分给了别的进程
	stubEnum(t, []procEntry{{PID: 501, PPID: 1, PGID: 501, StartedAt: 9999}}, nil)
	h := Handle{PID: 100, StartedAt: t0, RosterPath: roster}
	n, v, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	if n != 0 {
		t.Fatalf("出生时刻不符不得回收，实得 killed=%d", n)
	}
	if len(*killed) != 0 {
		t.Fatalf("出生时刻不符时绝不能发信号，实得 %v", *killed)
	}
}

// 名册里的 pid 已经不在进程表里 —— 早就退了，无需动作，也不算失败。
func TestSweepSkipsRosterMemberAlreadyGone(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	shrinkBackoff(t)
	stubAlive(t, false)
	killed := stubKillProc(t)
	stubEnum(t, []procEntry{}, nil)
	h := Handle{PID: 100, StartedAt: t0, RosterPath: roster}
	n, _, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 0 || len(*killed) != 0 {
		t.Fatalf("已退出的成员不该有任何动作，实得 killed=%d, signals=%v", n, *killed)
	}
}

// 没有名册（老任务/刚起来）：第一段照常，第二段安静跳过，不报错。
func TestSweepWithoutRosterStillDoesGroupPhase(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, false)
	stubKillGroup(t, nil) // 第一段会真的发组信号，必须 stub
	stubEnum(t,
		[]procEntry{{PID: 101, PPID: 100, PGID: 100, StartedAt: t0 + 1}}, nil,
		[]procEntry{})
	h := Handle{PID: 100, StartedAt: t0} // RosterPath 为空
	n, v, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if v != VerdictOK || n != 1 {
		t.Fatalf("无名册时第一段仍应正常，实得 killed=%d verdict=%s", n, v)
	}
}

// Footprint 报的数必须和 Sweep 会动手的范围一致：只报 pgid 那层、却杀两层，
// 等于让 handoff footprint 的输出骗人（B70：宣称什么就得是什么）。
func TestFootprintIncludesRosterMembers(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true) // executor 活着，Footprint 对活任务也要能用
	stubEnum(t, []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: t0},       // shim
		{PID: 101, PPID: 100, PGID: 100, StartedAt: t0 + 1}, // executor，同组
		{PID: 501, PPID: 101, PGID: 501, StartedAt: 5100},   // 逃逸后代，在名册里
	}, nil)
	h := Handle{PID: 100, LockPath: filepath.Join(dir, "x.lock"), StartedAt: t0, RosterPath: roster}
	members, v, err := Footprint(h)
	if err != nil {
		t.Fatalf("Footprint: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	assertMembers(t, members, []int{100, 101, 501})
}

// 与 Sweep 同一条红线：出生时刻对不上的名册成员**不计入**足迹。
// 数字上多算一个只是难看，但它会让协调者以为残留还在、去追一个不存在的东西。
func TestFootprintExcludesReusedRosterPID(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true)
	stubEnum(t, []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: t0},
		{PID: 501, PPID: 1, PGID: 501, StartedAt: 9999}, // pid 易主
	}, nil)
	h := Handle{PID: 100, LockPath: filepath.Join(dir, "x.lock"), StartedAt: t0, RosterPath: roster}
	members, _, err := Footprint(h)
	if err != nil {
		t.Fatalf("Footprint: %v", err)
	}
	assertMembers(t, members, []int{100})
}

// TestSweepKillsMarkOnlyMembers 钉住 Sweep 与 Footprint 报的是同一批：
// 标记独有的成员必须真的被杀，否则 handoff footprint 数出来的就是句空话（B70）。
func TestSweepKillsMarkOnlyMembers(t *testing.T) {
	stubEnum(t, []procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000},
		{PID: 200, PGID: 200, StartedAt: 1200}, // 标记独有
	}, nil)
	stubAlive(t, false)
	stubKillGroup(t, nil)
	killedPIDs := stubKillProc(t)

	oldAttr := attributesFn
	t.Cleanup(func() { attributesFn = oldAttr })
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return pid == 200, nil }

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	if _, _, err := Sweep(h); err != nil {
		t.Fatalf("清扫不应报错：%v", err)
	}
	if !containsPID(*killedPIDs, 200) {
		t.Fatalf("标记独有的成员 200 未被回收：killed=%v", *killedPIDs)
	}
}

// TestMarkKillReverifiesBeforeSignal 钉住发信号前必须复验标记。
//
// 枚举与发信号之间进程可能已退出且 pid 被复用；标记是活读的，
// 复验一次的成本是一个 syscall，而误杀的代价是打掉用户的 shell（B47）。
func TestMarkKillReverifiesBeforeSignal(t *testing.T) {
	procs := []procEntry{{PID: 200, PGID: 200, StartedAt: 1200}}
	killedPIDs := stubKillProc(t)

	oldAttr := attributesFn
	t.Cleanup(func() { attributesFn = oldAttr })
	calls := 0
	attributesFn = func(pid int, cred TaskCred) (bool, error) {
		calls++
		// 第一次（筛选）命中，第二次（杀前复验）不再命中 ⇒ pid 已易主
		return calls == 1, nil
	}

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	killed := markKill(h, procs)
	if killed != 0 || len(*killedPIDs) != 0 {
		t.Fatalf("复验不通过时不得发信号：killed=%d pids=%v", killed, *killedPIDs)
	}
	if calls != 2 {
		t.Fatalf("应恰好复验一次：attributes 调用 %d 次", calls)
	}
}

// TestMarkKillSkipsWhenUnsupported 钉住平台不支持时安静返回 0，不影响前两段。
func TestMarkKillSkipsWhenUnsupported(t *testing.T) {
	oldAttr := attributesFn
	t.Cleanup(func() { attributesFn = oldAttr })
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return false, ErrNotSupported }

	killedPIDs := stubKillProc(t)
	killed := markKill(Handle{PID: 100, StartedAt: 1000, TaskID: "t1"},
		[]procEntry{{PID: 200, StartedAt: 1200}})
	if killed != 0 || len(*killedPIDs) != 0 {
		t.Fatalf("不支持时不得发信号：killed=%d pids=%v", killed, *killedPIDs)
	}
}

// TestFootprintIncludesMarkOnlyMembers 钉住本条需求的核心价值：
// 标记判据要捞回 pgid 与 roster 都看不见的那批进程。
func TestFootprintIncludesMarkOnlyMembers(t *testing.T) {
	stubEnum(t, []procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000}, // shim 自己
		{PID: 101, PGID: 100, StartedAt: 1100}, // 同组，pgid 能看见
		{PID: 200, PGID: 200, StartedAt: 1200}, // setsid 逃逸，只有标记看得见
	}, nil)
	stubAlive(t, true)

	oldAttr := attributesFn
	t.Cleanup(func() { attributesFn = oldAttr })
	attributesFn = func(pid int, cred TaskCred) (bool, error) {
		return pid == 200 || pid == 101, nil
	}

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	members, v, err := Footprint(h)
	if err != nil || v != VerdictOK {
		t.Fatalf("判定应通过：v=%v err=%v", v, err)
	}
	if !containsPID(members, 200) {
		t.Fatalf("标记独有的成员 200 未被捞回：members=%v", members)
	}
	if countPID(members, 101) != 1 {
		t.Fatalf("同时被 pgid 与标记命中的 101 必须去重，members=%v", members)
	}
}

// TestFootprintMarkRespectsStartedAtFloor 钉住时间下界对标记成员照样施加——
// 枚举与发信号之间的 pid 复用窗口，这道护栏不能因为换判据就撤（B47）。
func TestFootprintMarkRespectsStartedAtFloor(t *testing.T) {
	stubEnum(t, []procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000},
		{PID: 300, PGID: 300, StartedAt: 500}, // 比 shim 还早
	}, nil)
	stubAlive(t, true)

	oldAttr := attributesFn
	t.Cleanup(func() { attributesFn = oldAttr })
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return pid == 300, nil }

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	members, _, _ := Footprint(h)
	if containsPID(members, 300) {
		t.Fatalf("比 shim 更早启动的进程不得因标记命中而入选：members=%v", members)
	}
}

// TestFootprintDegradesWhenMarkUnsupported 钉住平台不支持时不影响既有两段。
func TestFootprintDegradesWhenMarkUnsupported(t *testing.T) {
	stubEnum(t, []procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000},
		{PID: 101, PGID: 100, StartedAt: 1100},
	}, nil)
	stubAlive(t, true)

	oldAttr := attributesFn
	t.Cleanup(func() { attributesFn = oldAttr })
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return false, ErrNotSupported }

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	members, v, err := Footprint(h)
	if err != nil || v != VerdictOK {
		t.Fatalf("平台不支持标记不该让判定失败：v=%v err=%v", v, err)
	}
	if !containsPID(members, 101) {
		t.Fatalf("pgid 那段必须照常工作：members=%v", members)
	}
}

// TestCountGroupCountsOnlyItsOwnGroup 断言：只数同组成员，无关进程不计入。
func TestCountGroupCountsOnlyItsOwnGroup(t *testing.T) {
	stubProcs(t, []procEntry{
		{PID: 300, PGID: 300, StartedAt: t0},     // 组长（PTY 里的 shell）
		{PID: 301, PGID: 300, StartedAt: t0 + 1}, // 它起的命令
		{PID: 302, PGID: 300, StartedAt: t0 + 2},
		{PID: 400, PGID: 400, StartedAt: t0}, // 无关
	})
	n, err := CountGroup(300)
	if err != nil {
		t.Fatalf("不该出错: %v", err)
	}
	if n != 3 {
		t.Fatalf("同组成员应为 3，实得 %d", n)
	}
}

// TestCountGroupEmptyGroupIsZeroNotError 断言：组里一个都没有是 0 而不是错误
// （会话刚退出、进程刚被收走都会走到这里）。
func TestCountGroupEmptyGroupIsZeroNotError(t *testing.T) {
	stubProcs(t, []procEntry{{PID: 400, PGID: 400, StartedAt: t0}})
	n, err := CountGroup(300)
	if err != nil || n != 0 {
		t.Fatalf("空组应当是 (0, nil)，实得 (%d, %v)", n, err)
	}
}

// TestCountGroupPropagatesEnumFailure 断言：枚举失败必须上抛，
// **不能降级成 0**——0 会被渲染成「没有残留」，那是个假结论。
func TestCountGroupPropagatesEnumFailure(t *testing.T) {
	orig := enumProcsFn
	enumProcsFn = func() ([]procEntry, error) { return nil, ErrNotSupported }
	t.Cleanup(func() { enumProcsFn = orig })
	if _, err := CountGroup(300); err == nil {
		t.Fatalf("枚举失败必须上抛")
	}
}

// stubProcs 把进程枚举替换成固定结果（沿用本文件既有的 enumProcsFn 接缝）。
func stubProcs(t *testing.T, procs []procEntry) {
	t.Helper()
	orig := enumProcsFn
	enumProcsFn = func() ([]procEntry, error) { return procs, nil }
	t.Cleanup(func() { enumProcsFn = orig })
}

// 进程枚举失败要分两档记：平台不支持是**预期形态**（Debug），真故障才是 Error。
//
// 背景（B144）：非 darwin/linux 上 enumProcs 恒定返回 ErrNotSupported。改前
// 一律按 Error 打，Windows 执行机上每次 handoff status 都在 agentd 日志里刷两条
// 红字，把真正的枚举故障淹在噪音里。
func TestLogEnumFailureTiersByCause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		want    slog.Level
		notWant slog.Level
	}{
		{"平台不支持降为 Debug", ErrNotSupported, slog.LevelDebug, slog.LevelError},
		{"真故障仍是 Error", errors.New("/proc 读取失败"), slog.LevelError, slog.LevelDebug},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			old := slog.Default()
			slog.SetDefault(slog.New(h))
			t.Cleanup(func() { slog.SetDefault(old) })

			logEnumFailure("足迹枚举失败", tc.err, "pid", 4242)

			out := buf.String()
			if !strings.Contains(out, "level="+tc.want.String()) {
				t.Fatalf("应按 %s 记，实得：%s", tc.want, out)
			}
			if strings.Contains(out, "level="+tc.notWant.String()) {
				t.Fatalf("不应按 %s 记，实得：%s", tc.notWant, out)
			}
			// 无论哪一档，cause 与上下文都必须在——降级的是档位，不是信息量
			for _, want := range []string{"cause=", "pid=4242"} {
				if !strings.Contains(out, want) {
					t.Fatalf("日志应含 %q，实得：%s", want, out)
				}
			}
		})
	}
}
