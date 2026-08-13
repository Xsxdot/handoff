package prochost

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

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
	spec := Spec{
		Argv:     []string{"/bin/sh", "-c", "sleep 5"},
		Dir:      dir,
		Stdout:   filepath.Join(dir, "out.log"),
		Stderr:   filepath.Join(dir, "err.log"),
		LockPath: filepath.Join(dir, "shim.lock"),
		InfoPath: filepath.Join(dir, "proc.json"),
	}
	// selfExe 直接用 /bin/sh 顶替真 shim：本用例只验 StartedAt 有没有被填上，
	// 不验 shim 行为（拿锁、读 spec.json 那些由 shim 自己的用例覆盖）
	hd, err := Start(spec, "/bin/sh", "-c", "sleep 5")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = killGroup(hd.PID) })
	if hd.StartedAt <= 0 {
		t.Fatalf("Start 未记录 StartedAt，got %d", hd.StartedAt)
	}
	if delta := time.Now().UnixNano() - hd.StartedAt; delta < 0 || delta > int64(30*time.Second) {
		t.Fatalf("StartedAt 偏离现在过远：delta=%d ns", delta)
	}
}

// TestSweepRefusesWhenExecutorAlive 锁仍被持有时 Sweep 必须拒绝执行且不发信号。
//
// 杀活着的执行者是 Kill 的职责。两者风险模型不同，互相代劳就会把 Kill 那条
// 「不确认存活就绝不发信号」的纪律绕过去。
func TestSweepRefusesWhenExecutorAlive(t *testing.T) {
	stubEnum(t, []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}, nil)
	stubAlive(t, true)
	n := stubKillGroup(t, nil)

	_, _, err := Sweep(h())
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("执行者存活时应返回 ErrExecutorAlive，got %v", err)
	}
	if *n != 0 {
		t.Fatalf("执行者存活却发了 %d 次信号", *n)
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
	spec := Spec{
		Argv:     []string{"/bin/sh", "-c", "sleep 5"},
		Dir:      dir,
		Stdout:   filepath.Join(dir, "out.log"),
		Stderr:   filepath.Join(dir, "err.log"),
		LockPath: filepath.Join(dir, "shim.lock"),
		InfoPath: filepath.Join(dir, "proc.json"),
	}
	hd, err := Start(spec, "/bin/sh", "-c", "sleep 5")
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
