package prochost

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writeSpec 把 spec 落到 dir/spec.json 并返回路径（测试辅助）。
func writeSpec(t *testing.T, dir string, s Spec) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("序列化 spec 失败: %v", err)
	}
	p := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("写 spec 失败: %v", err)
	}
	return p
}

// baseSpec 造一个最小可用 spec：子进程按 script 跑一段 sh。
func baseSpec(dir, script string) Spec {
	return Spec{
		Argv:     []string{"/bin/sh", "-c", script},
		Dir:      dir,
		Env:      []string{"PATH=/usr/bin:/bin"},
		Stdout:   filepath.Join(dir, "out.jsonl"),
		Stderr:   filepath.Join(dir, "err.log"),
		LockPath: filepath.Join(dir, "proc.lock"),
		InfoPath: filepath.Join(dir, "proc.json"),
		Sentinel: true,
	}
}

// waitFile 等文件内容出现 want，超时即失败（测试辅助）。
func waitFile(t *testing.T, path, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), want) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 %s 出现 %q 超时，实得 %q", path, want, b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunShimWritesSentinelWithExitCode(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `printf 'hello\n'; exit 7`)
	specPath := writeSpec(t, dir, spec)

	if err := RunShim(specPath); err != nil {
		t.Fatalf("RunShim 返回错误: %v", err)
	}
	got, err := os.ReadFile(spec.Stdout)
	if err != nil {
		t.Fatalf("读 stdout 失败: %v", err)
	}
	if !strings.Contains(string(got), "hello") {
		t.Fatalf("子进程 stdout 未落盘，实得 %q", got)
	}
	// 哨兵必须带真实退出码——它是 adapter 判死的唯一可靠信号
	if !strings.Contains(string(got), `"type":"handoff_exit"`) ||
		!strings.Contains(string(got), `"code":7`) {
		t.Fatalf("哨兵缺失或退出码不对，实得 %q", got)
	}
}

func TestRunShimRecordsChildPID(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `exit 0`)
	specPath := writeSpec(t, dir, spec)

	if err := RunShim(specPath); err != nil {
		t.Fatalf("RunShim 返回错误: %v", err)
	}
	pid, err := ChildPID(spec.InfoPath)
	if err != nil {
		t.Fatalf("读 child_pid 失败: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("shim 必须记下真实 child_pid，实得 %d", pid)
	}
}

// TestRunShimNeverTouchesProcInfo 钉死「proc.json 只有 adapter 一个写者」。
//
// 为什么这条测的是「文件字节没变」而不是「字段还在」：只要 shim 也对 proc.json
// 做读-改-写，它与 adapter 在 Start 返回后的那次整份覆写之间就存在丢失更新窗口
// ——shim 读到旧版、adapter 写入含 PID 的新版、shim 再写回旧版+child_pid，
// Handle.PID 归零。PID 归零的后果不是少个诊断字段：prochost.Kill 在 PID<=0 时
// 直接返回 nil，Reap 于是打出「兜底回收完成」而执行者还活着（假成功 + 孤儿进程）。
// 断言字段还在挡不住这个（丢的恰好是 adapter 后写的那个字段），只有断言
// shim 根本不写这个文件才是结构性保证。
func TestRunShimNeverTouchesProcInfo(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `exit 0`)
	specPath := writeSpec(t, dir, spec)
	const adapterWrote = `{"handle":{"pid":4242,"lock_path":"x"},"port":1}`
	if err := os.WriteFile(spec.InfoPath, []byte(adapterWrote), 0o600); err != nil {
		t.Fatalf("预写 proc.json 失败: %v", err)
	}

	if err := RunShim(specPath); err != nil {
		t.Fatalf("RunShim 返回错误: %v", err)
	}
	b, err := os.ReadFile(spec.InfoPath)
	if err != nil {
		t.Fatalf("读 proc.json 失败: %v", err)
	}
	if string(b) != adapterWrote {
		t.Fatalf("shim 改写了 proc.json（丢失更新竞态的来源）：\n预期 %s\n实得 %s", adapterWrote, b)
	}
	// child_pid 仍要拿得到——它只是换了个不共享的落点
	if pid, err := ChildPID(spec.InfoPath); err != nil || pid <= 0 {
		t.Fatalf("child_pid 应落在独立文件且可读，实得 pid=%d err=%v", pid, err)
	}
}

// TestChildPIDMissingIsAnError 钉死「没起过 shim 时 ChildPID 如实报错」，
// 不能返回 0 冒充成功——0 会被误读成「pid 为 0 的进程」。
func TestChildPIDMissingIsAnError(t *testing.T) {
	dir := t.TempDir()
	if _, err := ChildPID(filepath.Join(dir, "proc.json")); err == nil {
		t.Fatal("child.pid 不存在时 ChildPID 必须报错")
	}
}

func TestRunShimHoldsInputChannelAndFeedsChild(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "in.fifo")
	if err := CreateInputChannel(fifo); err != nil {
		t.Fatalf("建 fifo 失败: %v", err)
	}
	// 子进程从 stdin 读一行就回显退出；stdin 必须是 shim 持有的 fifo
	spec := baseSpec(dir, `read line; printf 'got:%s\n' "$line"`)
	spec.InputCh = fifo
	specPath := writeSpec(t, dir, spec)

	done := make(chan error, 1)
	go func() { done <- RunShim(specPath) }()

	// shim 持有读端后，写端才能以 O_NONBLOCK 打开（否则 ENXIO）
	if _, err := WaitInputReader(fifo, 3*time.Second); err != nil {
		t.Fatalf("shim 未在时限内持有 fifo 读端: %v", err)
	}
	f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("打开 fifo 写端失败: %v", err)
	}
	if _, err := f.WriteString("ping\n"); err != nil {
		t.Fatalf("写 fifo 失败: %v", err)
	}
	f.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunShim 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunShim 未在 5s 内结束——子进程可能没读到 stdin")
	}
	got, _ := os.ReadFile(spec.Stdout)
	if !strings.Contains(string(got), "got:ping") {
		t.Fatalf("子进程未从 fifo 收到输入，实得 %q", got)
	}
}

func TestRunShimRefusesWhenLockAlreadyHeld(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `exit 0`)
	specPath := writeSpec(t, dir, spec)
	// 先占住锁，模拟「同一任务已有 shim 在跑」——绝不能起第二个
	// （两个 executor 抢同一会话是数据损坏级后果，见 claudecode Resume 的冷恢复互斥）
	held, err := AcquireLock(spec.LockPath)
	if err != nil {
		t.Fatalf("预占锁失败: %v", err)
	}
	defer held.Release()

	if err := RunShim(specPath); err == nil {
		t.Fatal("锁已被持有时 RunShim 必须失败，实得 nil")
	}
}

// TestHelperShimEntry 不是测试：作为 Start 拉起的「shim 可执行体」入口，
// 把 --spec 后面的路径交给 RunShim。生产里这个角色由 handoff _shim 承担。
func TestHelperShimEntry(t *testing.T) {
	if os.Getenv(helperEnv) != "shimentry" {
		t.Skip("非 helper 调用")
	}
	if err := RunShim(os.Getenv("PROCHOST_TEST_SPEC")); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestSentinelWrittenAfterParentDeath(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `sleep 1; exit 3`)
	specPath := writeSpec(t, dir, spec)

	// 用 helper 进程扮演 agentd：Start 出 shim 后立刻退出
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHelperShimEntry$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		helperEnv+"=shimentry", "PROCHOST_TEST_SPEC="+specPath)
	h, err := startWith(cmd, spec)
	if err != nil {
		t.Fatalf("拉起 shim 失败: %v", err)
	}
	t.Cleanup(func() { _ = Kill(h) })

	// shim 在跑：锁必须被持有。Start 只代表 fork 成功，锁要等 shim 起来
	// 读到 spec 才被持有——轮询到超时，而不是立即断言（Start 的文档注释
	// 明确说「返回不代表已持锁」）
	startDeadline := time.Now().Add(5 * time.Second)
	for !Alive(h) {
		if time.Now().After(startDeadline) {
			t.Fatal("shim 未在 5s 内持锁")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 子进程 1s 后退出；此刻没有任何「agentd」在 waitpid——哨兵必须由 shim 写出。
	// 这是 shim 存在的根本理由：agentd 离线期间 executor 退出的退出码不能丢。
	got := waitFile(t, spec.Stdout, `"type":"handoff_exit"`, 10*time.Second)
	if !strings.Contains(got, `"code":3`) {
		t.Fatalf("哨兵退出码不对，实得 %q", got)
	}
	// shim 退出后锁被内核释放，Alive 必须回到 false
	deadline := time.Now().Add(3 * time.Second)
	for Alive(h) {
		if time.Now().After(deadline) {
			t.Fatal("shim 已完成但 Alive 仍为 true")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startWith 是测试专用小 helper：像 prochost.Start 一样以 detached 方式拉起
// cmd，返回其 Handle。之所以不直接用 Start——生产的 Start 固定拼 `_shim --spec`，
// 而测试二进制的入口是 `-test.run`；Start 自身的 argv 拼装由单测覆盖。
func startWith(cmd *exec.Cmd, spec Spec) (Handle, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return Handle{}, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return Handle{PID: pid, LockPath: spec.LockPath}, nil
}

// TestShimLogsLandInTaskDirShimLog（8.1 回归）：spawnDetached 必须把 shim 的
// stderr 接到调用方给的日志文件，否则 shim 的 slog（含撞墙归因那行）全落进
// /dev/null，协调者在任务目录里什么都读不到。生产里该文件是 <taskDir>/shim.log，
// 由 Start 打开后传入；这里直接用真实 shim 入口经 spawnDetached 拉起，断言
// shim 自己必打的那行 slog 出现在日志文件里。
func TestShimLogsLandInTaskDirShimLog(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, "exit 0")
	specPath := writeSpec(t, dir, spec)
	logPath := filepath.Join(dir, "shim.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("打开 shim.log: %v", err)
	}
	defer f.Close()

	// spawnDetached 继承本进程环境，helper 开关经 t.Setenv 注入（t.Cleanup 还原）
	t.Setenv(helperEnv, "shimentry")
	t.Setenv("PROCHOST_TEST_SPEC", specPath)
	pid, err := spawnDetached(
		[]string{os.Args[0], "-test.run", "^TestHelperShimEntry$", "-test.v=false"},
		dir, f)
	if err != nil {
		t.Fatalf("spawnDetached 拉起 shim 失败: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// shim 每次必打的 slog 行；它能出现在 shim.log 里，就证明 stderr 接线没被
	// 悄悄退回 /dev/null
	got := waitFile(t, logPath, "shim 拉起执行者进程", 10*time.Second)
	if !strings.Contains(got, "shim 拉起执行者进程") {
		t.Fatalf("shim 日志应落入任务目录 shim.log，实得 %q", got)
	}
}

// shim 必须在拉起 executor 后**立即**落一次名册，而不是等第一个周期到点。
// 短命任务（几秒就结束）在等待周期的窗口里死掉的话，名册永远是空的，
// 第二段清扫就等于不存在——这正是逃逸残留最容易发生的场景（编译、跑测试）。
func TestShimWritesRosterImmediately(t *testing.T) {
	dir := t.TempDir()
	// 让 executor 活得久一点，好让我们在它还活着时读到名册
	spec := baseSpec(dir, "sleep 5")
	specPath := writeSpec(t, dir, spec)

	t.Setenv(helperEnv, "shimentry")
	t.Setenv("PROCHOST_TEST_SPEC", specPath)
	pid, err := spawnDetached(
		[]string{os.Args[0], "-test.run", "^TestHelperShimEntry$", "-test.v=false"},
		dir, nil)
	if err != nil {
		t.Fatalf("拉起 shim: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	path := rosterPath(spec.InfoPath)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, rerr := readRoster(path)
		if rerr == nil && len(entries) > 0 {
			// 名册里必须有真实存活的 pid，且带非零出生时刻
			for _, e := range entries {
				if e.PID <= 0 || e.StartedAt <= 0 {
					t.Fatalf("名册条目字段不全: %+v", e)
				}
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("10s 内没等到非空名册（path=%s）", path)
}

func TestRosterSamplerKeepsEscapedDescendantAcrossRounds(t *testing.T) {
	dir := t.TempDir()
	info := filepath.Join(dir, "proc.json")
	self := os.Getpid()

	orig := enumProcsFn
	defer func() { enumProcsFn = orig }()

	// 第一轮：工具壳 200（ppid=self）与它的子进程 300 都在树里
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: self, PPID: 1, PGID: self, StartedAt: 900},
			{PID: 200, PPID: self, PGID: 200, StartedAt: 1000},
			{PID: 300, PPID: 200, PGID: 200, StartedAt: 1100},
		}, nil
	}
	s := &rosterSampler{path: rosterPath(info)}
	s.sample(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 第二轮：工具壳退出，300 被 reparent 给 launchd
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: self, PPID: 1, PGID: self, StartedAt: 900},
			{PID: 300, PPID: 1, PGID: 200, StartedAt: 1100},
		}, nil
	}
	s.sample(slog.New(slog.NewTextHandler(io.Discard, nil)))

	got, err := readRoster(rosterPath(info))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PID != 300 {
		t.Fatalf("工具壳退出后逃逸后代必须仍在名册里，got=%v", got)
	}
}

func TestRosterSamplerSkipsWriteWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	info := filepath.Join(dir, "proc.json")
	self := os.Getpid()

	orig := enumProcsFn
	defer func() { enumProcsFn = orig }()
	enumProcsFn = func() ([]procEntry, error) {
		return []procEntry{
			{PID: self, PPID: 1, PGID: self, StartedAt: 900},
			{PID: 300, PPID: self, PGID: self, StartedAt: 1100},
		}, nil
	}

	l := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &rosterSampler{path: rosterPath(info)}
	s.sample(l)
	st1, err := os.Stat(rosterPath(info))
	if err != nil {
		t.Fatal(err)
	}
	if s.writes != 1 {
		t.Fatalf("第一轮必须落盘一次，writes=%d", s.writes)
	}
	s.sample(l)
	if s.writes != 1 {
		t.Fatalf("同一批后代第二轮不得再落盘，writes=%d", s.writes)
	}
	st2, err := os.Stat(rosterPath(info))
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("内容未变时不应重写文件")
	}
}

func TestRosterIntervalIsOneSecond(t *testing.T) {
	// 间隔是本次修复的承重件之一：工具壳只活约 1 秒，15s 的 tick 打不中它。
	if rosterInterval != time.Second {
		t.Fatalf("rosterInterval 应为 1s，实为 %v", rosterInterval)
	}
}
