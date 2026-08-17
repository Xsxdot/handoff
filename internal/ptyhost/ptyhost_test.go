//go:build unix

package ptyhost_test

import (
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost"
)

func testHost(t *testing.T) *ptyhost.Host {
	t.Helper()
	return ptyhost.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testOpen(t *testing.T, h *ptyhost.Host) ptyhost.Session {
	t.Helper()
	s, err := h.Open(ptyhost.OpenOptions{
		BasePath: t.TempDir(), BaseKind: "workspace", Shell: "/bin/sh",
		Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color"}, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = h.Close(s.ID) })
	return s
}

// waitFor 反复调用 read 直到输出里出现 want 或超时。
// PTY 输出是流式的，不能假设一次读就拿到完整行。
func waitFor(t *testing.T, out <-chan []byte, want string) string {
	t.Helper()
	var sb strings.Builder
	deadline := time.After(10 * time.Second)
	for {
		select {
		case b, ok := <-out:
			if !ok {
				t.Fatalf("订阅通道已关闭，累计输出:\n%s", sb.String())
			}
			sb.Write(b)
			if strings.Contains(sb.String(), want) {
				return sb.String()
			}
		case <-deadline:
			t.Fatalf("等待 %q 超时，累计输出:\n%s", want, sb.String())
		}
	}
}

// 最基本的一条：开会话 → 写命令 → 从订阅里读到回显。
func TestOpenWriteAttach(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	if s.PID <= 0 || s.ExitCode != nil {
		t.Fatalf("新会话应有 pid 且 exit_code 为 nil，实得 pid=%d exit=%v", s.PID, s.ExitCode)
	}
	a, err := h.Attach(s.ID, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Detach()
	if err := h.Write(s.ID, []byte("echo HANDOFF_OK\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, a.Out, "HANDOFF_OK")
}

// 两个订阅者必须都收到同一份输出（tmux 语义，spec §3.3）。
func TestBroadcastToAllSubscribers(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a1, _ := h.Attach(s.ID, 0)
	defer a1.Detach()
	a2, err := h.Attach(s.ID, 0)
	if err != nil {
		t.Fatalf("第二次 Attach: %v", err)
	}
	defer a2.Detach()
	if got, _ := h.Get(s.ID); got.Attached != 2 {
		t.Fatalf("attached = %d，期望 2", got.Attached)
	}
	_ = h.Write(s.ID, []byte("echo BOTH\n"))
	waitFor(t, a1.Out, "BOTH")
	waitFor(t, a2.Out, "BOTH")
}

// 尺寸取所有订阅者里最小的那个：大屏一 resize 就把小屏刷成乱码是不可接受的。
func TestResizeTakesMinimumAcrossSubscribers(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a1, _ := h.Attach(s.ID, 0)
	defer a1.Detach()
	a2, _ := h.Attach(s.ID, 0)
	defer a2.Detach()

	if err := a1.Resize(200, 60); err != nil {
		t.Fatalf("a1.Resize: %v", err)
	}
	if err := a2.Resize(100, 30); err != nil {
		t.Fatalf("a2.Resize: %v", err)
	}
	got, _ := h.Get(s.ID)
	if got.Cols != 100 || got.Rows != 30 {
		t.Fatalf("生效尺寸 = %dx%d，期望 100x30（取最小）", got.Cols, got.Rows)
	}
	_ = h.Write(s.ID, []byte("stty size\n"))
	waitFor(t, a1.Out, "30 100")
}

// 断开后重连带 since，只补没看过的那段，不重复。
func TestAttachSinceResumes(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a1, _ := h.Attach(s.ID, 0)
	// macOS 上 PTY 行规程先回显按键：waitFor("FIRST") 会命中命令回显 "echo FIRST"
	// 而不是命令输出，游标被提前截住，之后真正输出的 "FIRST" 会被误判成重复回放。
	// 把标记词拆成拼接形式（echo F"IRST"），回显里就没有连续的 "FIRST"，
	// waitFor 只有等到命令输出才返回。
	_ = h.Write(s.ID, []byte("echo F\"IRST\"\n"))
	waitFor(t, a1.Out, "FIRST")
	cursor := func() uint64 { g, _ := h.Get(s.ID); return g.BytesOut }()
	a1.Detach()

	_ = h.Write(s.ID, []byte("echo SECOND\n"))
	time.Sleep(500 * time.Millisecond) // 让输出落进环

	a2, err := h.Attach(s.ID, cursor)
	if err != nil {
		t.Fatalf("重连 Attach: %v", err)
	}
	defer a2.Detach()
	if a2.Truncated {
		t.Error("256 KiB 环装得下这点输出，不该报 truncated")
	}
	if strings.Contains(string(a2.Backlog), "FIRST") {
		t.Errorf("since 之前的内容不该再回放一遍，实得:\n%s", a2.Backlog)
	}
	if !strings.Contains(string(a2.Backlog), "SECOND") {
		t.Errorf("since 之后的内容必须补齐，实得:\n%s", a2.Backlog)
	}
}

// 订阅者上限 8：第 9 个必须被明确拒绝，不是静默丢弃。
func TestSubscriberLimit(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	for i := 0; i < 8; i++ {
		a, err := h.Attach(s.ID, 0)
		if err != nil {
			t.Fatalf("第 %d 个订阅者被拒: %v", i+1, err)
		}
		defer a.Detach()
	}
	if _, err := h.Attach(s.ID, 0); err != ptyhost.ErrTooManySubscribers {
		t.Fatalf("第 9 个订阅者的错误 = %v，期望 ErrTooManySubscribers", err)
	}
}

// shell 自己退出后：会话进终态、仍在列表里、exit_code 如实记录、订阅通道关闭。
func TestShellExitKeepsTerminalSession(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a, _ := h.Attach(s.ID, 0)
	defer a.Detach()
	_ = h.Write(s.ID, []byte("exit 3\n"))

	deadline := time.After(10 * time.Second)
	for {
		g, ok := h.Get(s.ID)
		if !ok {
			t.Fatal("shell 自己退出不该让会话从列表消失（spec §3.2）")
		}
		if g.ExitCode != nil {
			if *g.ExitCode != 3 {
				t.Fatalf("exit_code = %d，期望 3", *g.ExitCode)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("等待 exit_code 超时")
		case <-time.After(50 * time.Millisecond):
		}
	}
	select {
	case _, ok := <-a.Out:
		if ok { // 可能还有残留输出，再收一次直到关闭
			for range a.Out {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("会话退出后订阅通道必须关闭，否则前端不知道该停止重连")
	}
	if err := h.Write(s.ID, []byte("echo x\n")); err != ptyhost.ErrSessionExited {
		t.Fatalf("向已退出会话写入的错误 = %v，期望 ErrSessionExited", err)
	}
}

// 显式 Close 之后会话从列表消失，再操作一律 ErrNoSession。
func TestCloseRemovesSession(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	if err := h.Close(s.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := h.Get(s.ID); ok {
		t.Fatal("显式关闭后会话必须从列表消失")
	}
	if len(h.List()) != 0 {
		t.Fatalf("List 长度 = %d，期望 0", len(h.List()))
	}
	if err := h.Close(s.ID); err != ptyhost.ErrNoSession {
		t.Fatalf("重复 Close 的错误 = %v，期望 ErrNoSession", err)
	}
}

// waitExited 轮询到会话落 exit_code 为止，用于「让 shell 在并发压力下自然退出」
// 这类用例。超时即 Fatal——等不到退出说明压测本身出了问题，继续断言没有意义。
func waitExited(t *testing.T, h *ptyhost.Host, id string) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		if g, ok := h.Get(id); ok && g.ExitCode != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("等待 shell 退出超时")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// 取快照与会话自然退出并发：不得有数据竞争。
//
// 为什么是回归用例：reap 在 shell 退出时关掉 PTY 主端这个 *os.File，而
// snapshot 要在同一个 fd 上做 TIOCGPGRP 读前台进程组。用裸 fd 号
// （os.File.Fd()）取 fd 是没有引用计数的，与并发的 Close 就是数据竞争，
// -race 下必现；不带 -race 时后果更隐蔽——ioctl 打到一个已被回收、可能
// 已被别的 goroutine 重新分配出去的 fd 号上。
//
// 本用例的价值只在 `go test -race` 下体现，裸跑必过。
func TestSnapshotDuringShellExitIsRaceFree(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				h.Get(s.ID) // 内部走 snapshot → foregroundPgid(s.f)
			}
		}()
	}
	defer func() { close(stop); wg.Wait() }()

	_ = h.Write(s.ID, []byte("exit 0\n"))
	waitExited(t, h, s.ID)
}

// 调尺寸与会话自然退出并发：不得有数据竞争。
//
// 与上一条同源：resizePty 的 TIOCSWINSZ 同样要拿到 fd。Resize 只在锁外读过
// 一次 exited 标志，那之后 reap 随时可能把 fd 关掉——所以「退出前检查过」
// 挡不住这个竞争，必须让取 fd 这一步自己持有引用。
//
// 每次交替两个尺寸：Resize 只在协商结果与当前值不同时才真去 ioctl，
// 固定尺寸会让这个用例一次都碰不到 resizePty。
func TestResizeDuringShellExitIsRaceFree(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a, err := h.Attach(s.ID, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Detach()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				_ = a.Resize(100, 30)
			} else {
				_ = a.Resize(120, 40)
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	// 订阅者必须持续排空，否则 shell 写收尾输出时会阻塞、迟迟不退出
	go func() {
		for range a.Out {
		}
	}()

	_ = h.Write(s.ID, []byte("exit 0\n"))
	waitExited(t, h, s.ID)
}
