package prochost

import (
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

// stubEnum 把进程枚举换成固定快照。
func stubEnum(t *testing.T, procs []procEntry, err error) {
	t.Helper()
	orig := enumProcsFn
	enumProcsFn = func() ([]procEntry, error) { return procs, err }
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
