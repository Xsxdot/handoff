package prochost

import (
	"encoding/json"
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
	if err := os.WriteFile(spec.InfoPath, []byte(`{"handle":{"pid":1,"lock_path":"x"}}`), 0o600); err != nil {
		t.Fatalf("预写 proc.json 失败: %v", err)
	}

	if err := RunShim(specPath); err != nil {
		t.Fatalf("RunShim 返回错误: %v", err)
	}
	pid, err := ChildPID(spec.InfoPath)
	if err != nil {
		t.Fatalf("读 child_pid 失败: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("shim 必须补写真实 child_pid，实得 %d", pid)
	}
	// 补写不能破坏 proc.json 里已有的字段（adapter 先写 handle，shim 后补 child_pid）
	b, _ := os.ReadFile(spec.InfoPath)
	if !strings.Contains(string(b), `"lock_path":"x"`) {
		t.Fatalf("shim 补写 child_pid 时抹掉了已有字段，实得 %q", b)
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
