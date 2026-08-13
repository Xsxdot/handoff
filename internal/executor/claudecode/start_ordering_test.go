package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestStartWritesPromptBeforeWaitingReady 钉住 Start 的关键时序：**prompt 必须先于
// 等 init**。
//
// 为什么必须有这条测试（2026-08-09 真机 e2e 实测发现的错误假设）：`claude -p
// --input-format stream-json` **不会在启动时主动吐 system/init**——它要先收到
// 第一条输入消息才吐。旧实现「先等 init 再投 prompt」与这个契约互为死锁，任务
// 永远卡在就绪超时。单测的合成 out.jsonl fixture（testdata/turn_success.jsonl）
// 本身就假设 init 会自己来，抓不到这条；本测试的假执行者**模拟 claude 的真实
// 契约**：只有在检测到输入被写进 fifo 之后，才往 out.jsonl 追加 system/init。
// 旧代码在它下面必然超时失败，新代码通过——这是本修复的唯一护栏。
func TestStartWritesPromptBeforeWaitingReady(t *testing.T) {
	installFakeClaude(t)
	taskDir := shortTestDir(t)
	repoPath := shortTestDir(t)

	// 桩 startProcHost：把 spec.json 落盘后，以本测试二进制的 TestShimEntry
	// 作为 shim 进程 detached 拉起（真实 RunShim 路径：开 fifo、跑假 claude）。
	shimPID := installFakeShim(t, fakeShimReal)

	a := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := a.Start(ctx, executor.StartReq{
		Task:        proto.Task{ID: "T-order", RepoPath: repoPath},
		PlanContent: "测试计划",
		TaskDir:     taskDir,
	})
	if err != nil {
		t.Fatalf("Start 应成功（prompt 先投、init 随后到达）：%v", err)
	}

	// 假执行者的 init 事件应已产出（session_id 与假 claude 写的一致）
	r := a.lookup("T-order")
	if r == nil {
		t.Fatal("Start 成功后运行态缺失")
	}
	select {
	case ev := <-r.evCh:
		if ev.Type != "progress" || ev.SessionID != "sess-fake" {
			t.Fatalf("init 事件应先到且带假执行者的 session_id，实际 %+v", ev)
		}
	// 死线放到 10s（仍在外层 15s ctx 之内）：本用例要等一个真的子进程被拉起、
	// 写 fifo、事件穿过 streamLoop。2s 在多包并发 -race 把 CPU 吃满时不够用，
	// 已实测偶发失败。放宽不削弱断言——顺序错了照样失败，只是容得下慢机器。
	case <-time.After(10 * time.Second):
		t.Fatal("10s 内未收到 init progress 事件")
	}

	// 收摊：Stop 幂等，运行态可能已被 streamLoop 随哨兵终结而注销
	_ = a.Stop("T-order")
	_ = shimPID
}

// TestStartWaitsForFIFOReader 钉住 StartProc 对「进程返回早于 fifo 读端就绪」的
// 兜底。
//
// 契约（沿自 2026-08-09 真机 e2e 次生缺陷）：Start 只代表 shim 已被 fork，
// **不代表 shim 已打开 in.fifo**；而 WriteInput 以 O_WRONLY|O_NONBLOCK 打开 fifo，
// POSIX 规定读端未就绪时 open 直接失败（errno ENXIO，macOS 文案 "device not
// configured"）。StartProc 必须先确认读端在位再返回，否则 adapter 随即投 prompt
// 必然 ENXIO 失败。shim（测试二进制的 TestShimEntry）要重新启动一个 test 进程、
// 读 spec、开 fifo，天然就有数百毫秒延迟——Start 仍必须等它。
func TestStartWaitsForFIFOReader(t *testing.T) {
	installFakeClaude(t)
	taskDir := shortTestDir(t)
	repoPath := shortTestDir(t)

	installFakeShim(t, fakeShimReal)

	a := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := a.Start(ctx, executor.StartReq{
		Task:        proto.Task{ID: "T-race", RepoPath: repoPath},
		PlanContent: "测试计划",
		TaskDir:     taskDir,
	})
	if err != nil {
		t.Fatalf("读端晚到 Start 仍应成功：%v", err)
	}

	// 收摊：与上一测试同款，Stop 幂等
	_ = a.Stop("T-race")
}

// TestStartProcKillsShimWhenFIFOReaderNeverReady 钉住 StartProc 对「等读端
// 超时」这条失败路径的资源回收：shim 已被拉起、锁已持有，waitInputReader
// 超时返回错误时，StartProc 必须自行 Kill 回收 shim——调用方 rollback 依赖
// r.proc 判空，而 StartProc 失败时返回 nil，r.proc 拿不到句柄，不回收就成了
// 孤儿进程（与 init 就绪超时的清理行为一致，见 spec §4.1）。Kill 失败只 Warn，
// 不盖掉 waitInputReader 那条真因错误。
//
// 自检：把 WaitInputReader 失败处那行 p.Kill() 临时去掉，本测试必然失败
// （victim 进程未被回收）——红，说明钉子有效。
//
// why（锁在本进程持有、victim 是独立进程组）：假 shim 走 test 二进制重入要
// 数百毫秒启动，而 fifoReaderTimeout 压到 200ms 后 p.Kill() 会在它抢到锁之前
// 执行、误判「锁空闲」——测不到回收路径。改成本进程持锁 + 一个真实 detached
// 进程作 victim：Alive 恒 true（锁真被持有）、Kill 走 killGroup(victim.Pid)
// 连坐真进程，时序由测试自己控制，与旧 tmuxKill 缝的语义一致。
func TestStartProcKillsShimWhenFIFOReaderNeverReady(t *testing.T) {
	taskDir := shortTestDir(t)
	repoPath := shortTestDir(t)

	// 本进程持有「shim」的存活锁，victim 是独立进程组的 sleep——模拟已拉起的 shim
	lockPath := filepath.Join(taskDir, "proc.lock")
	held, err := prochost.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("预占锁失败: %v", err)
	}
	t.Cleanup(func() { held.Release() })
	victim := exec.Command("/bin/sh", "-c", "sleep 1000")
	victim.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := victim.Start(); err != nil {
		t.Fatalf("拉起 victim 失败: %v", err)
	}
	// 收割 zombie：SIGKILL 后进程变 zombie，不 Wait 回收则信号 0 探测恒为「存活」。
	// Wait 只能有一个调用方（os/exec 的 Wait 非并发安全），由 goroutine 独占收割权，
	// Cleanup 只 Kill + 等 goroutine 完成，不再二次 Wait。
	reaped := make(chan struct{})
	go func() { _ = victim.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = victim.Process.Kill(); <-reaped })

	stubClaudeLookup(t)
	old := startProcHost
	startProcHost = func(spec prochost.Spec, selfExe string, extra ...string) (prochost.Handle, error) {
		return prochost.Handle{PID: victim.Process.Pid, LockPath: lockPath}, nil
	}
	t.Cleanup(func() { startProcHost = old })
	// 把超时压到毫秒量级，别让测试真等满 5s
	oldTimeout := fifoReaderTimeout
	fifoReaderTimeout = 200 * time.Millisecond
	t.Cleanup(func() { fifoReaderTimeout = oldTimeout })

	a := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = a.Start(ctx, executor.StartReq{
		Task:        proto.Task{ID: "T-kill", RepoPath: repoPath},
		PlanContent: "测试计划",
		TaskDir:     taskDir,
	})
	if err == nil {
		t.Fatal("读端永远不就绪，Start 应失败")
	}
	// StartProc 超时路径必须自行 Kill shim（victim 进程组被连坐）
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(victim.Process.Pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("WaitInputReader 超时后 shim 未被回收（调用方 rollback 拿不到 r.proc）")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fakeShimKind 是假 shim 的角色。
type fakeShimKind int

const (
	// fakeShimReal 走真实 RunShim：开 fifo、跑假 claude、写哨兵。
	fakeShimReal fakeShimKind = iota
)

// installFakeShim 把 startProcHost 桩成「用本测试二进制的 shim 入口 detached 拉起
// shim」，返回被拉起的 shim 进程 pid（供断言回收）。
func installFakeShim(t *testing.T, kind fakeShimKind) int {
	t.Helper()
	var shimPID int
	old := startProcHost
	startProcHost = func(spec prochost.Spec, selfExe string, extra ...string) (prochost.Handle, error) {
		specPath := filepath.Join(filepath.Dir(spec.InfoPath), "spec.json")
		b, err := json.Marshal(spec)
		if err != nil {
			return prochost.Handle{}, err
		}
		if err := os.WriteFile(specPath, b, 0o600); err != nil {
			return prochost.Handle{}, err
		}
		cmd := exec.Command(os.Args[0], "-test.run", "^TestShimEntry$", "-test.v=false")
		cmd.Env = append(os.Environ(),
			"CLAUDECODE_TEST_SHIM=1",
			"CLAUDECODE_TEST_SHIM_KIND="+fmt.Sprint(kind),
			"CLAUDECODE_TEST_SPEC="+specPath,
		)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
		if err := cmd.Start(); err != nil {
			return prochost.Handle{}, err
		}
		pid := cmd.Process.Pid
		_ = cmd.Process.Release()
		shimPID = pid
		return prochost.Handle{PID: pid, LockPath: spec.LockPath}, nil
	}
	t.Cleanup(func() { startProcHost = old })
	return shimPID
}

// TestShimEntry 不是测试：被 installFakeShim 以子进程方式调用，扮演 shim。
// 生产里这个角色由 handoff _shim 承担，这里用测试二进制的入口重入。
func TestShimEntry(t *testing.T) {
	if os.Getenv("CLAUDECODE_TEST_SHIM") != "1" {
		t.Skip("非 shim 调用")
	}
	switch os.Getenv("CLAUDECODE_TEST_SHIM_KIND") {
	case fmt.Sprint(fakeShimReal), "":
		if err := prochost.RunShim(os.Getenv("CLAUDECODE_TEST_SPEC")); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

// installFakeClaude 把假 claude 放进 PATH 首位：StartProc 以 LookPath("claude")
// 解析，PATH 里先放假执行者。假执行者模拟真实契约——收到首条输入前不吐任何
// 东西；读到输入后吐 system/init，然后像真实 claude 一样保持进程存活（cat 持续
// 消费 stdin，直到 fifo 写端关闭）。
func installFakeClaude(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "claude")
	fakeScript := `#!/bin/sh
# 假 claude：模拟真实契约——收到首条输入前不吐任何东西；读到输入后吐 system/init，
# 然后像真实 claude 一样保持进程存活（cat 持续消费 stdin，直到 fifo 写端关闭）
while IFS= read -r line; do
  if [ -n "$line" ]; then
    printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-fake"}'
    cat > /dev/null
    exit 0
  fi
done
`
	if err := os.WriteFile(fake, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// shortTestDir 建一个短路径目录（os.TempDir 下用短随机名），供 unix socket /
// 命名管道使用——macOS 的 sockaddr 路径上限 104 字节，t.TempDir() 因嵌入长测试名
// 会超限（bind/mkfifo 报 invalid argument）；随测试结束清理。
func shortTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("hc-%d", time.Now().UnixNano()%1e9))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
