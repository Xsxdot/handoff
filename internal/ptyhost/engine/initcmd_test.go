//go:build unix

// initcmd_test.go —— OpenOptions.InitCommand 的行为锁。
//
// 职责：钉住三件事——不给命令时行为不变、给了命令时首字节即写、
// shell 一直不出声时 3s 兜底照样写。
//
// 边界：不验命令在真实 login shell 里的语义（那是真机清单第 2 条），
// 只验「字节有没有按时写进 PTY 输入」。用假 shell 把「什么时候出第一个字节」
// 变成测试能控制的量。
package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/engine"
)

// fakeShell 写一个可执行的假 shell 并返回其路径。
//
// body 之后统一接一个 read 回显循环：这样「命令有没有被写进去」可以通过
// 会话输出观察到，而 body 决定 shell 在**收到输入之前**出不出声——那正是
// 首字节路径与兜底路径的分界。
//
// 假 shell 会拿到一个 `-l` 参数（startPty 起的是 login shell），脚本忽略它。
func fakeShell(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-shell")
	script := "#!/bin/sh\n" + body + "\nwhile IFS= read -r line; do echo \"GOT:$line\"; done\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatalf("写假 shell: %v", err)
	}
	return p
}

// openAndCollect 开一个会话并订阅它，返回「等一行含 want 的输出」的函数。
//
// testHost 与 Attach 都是包内既有测试的现成写法，订阅前的存量与订阅后的
// 增量都收，避免 banner 在 Attach 之前吐完而漏判。
func openAndCollect(t *testing.T, opt ptyhost.OpenOptions) (*engine.Engine, ptyhost.Session, func(want string, within time.Duration) bool) {
	t.Helper()
	h := testHost(t)
	sess, err := h.Open(opt)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = h.Close(sess.ID) })
	a, err := h.Attach(sess.ID, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(a.Detach)
	var sb strings.Builder
	sb.Write(a.Backlog)
	wait := func(want string, within time.Duration) bool {
		if strings.Contains(sb.String(), want) {
			return true
		}
		deadline := time.After(within)
		for {
			select {
			case b, ok := <-a.Out:
				if !ok {
					return strings.Contains(sb.String(), want)
				}
				sb.Write(b)
				if strings.Contains(sb.String(), want) {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	return h, sess, wait
}

// TestInitCommandEmptyKeepsOldBehaviour 是兼容性守卫：不给命令时什么都不写。
//
// 反向断言必须配正面断言（下面两个用例就是），单独一条「没有 GOT:」在
// 写入路径整个被删掉之后照样绿。
func TestInitCommandEmptyKeepsOldBehaviour(t *testing.T) {
	sh := fakeShell(t, "printf 'READY\\n'")
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(),
	})
	if !wait("READY", 5*time.Second) {
		t.Fatal("假 shell 的 banner 没出现，用例前提不成立")
	}
	if wait("GOT:", 1500*time.Millisecond) {
		t.Fatal("InitCommand 为空时不得向 PTY 写入任何东西")
	}
}

// TestInitCommandWrittenOnFirstByte 钉住首字节路径：banner 一出就写，
// 远早于 3s 兜底。
func TestInitCommandWrittenOnFirstByte(t *testing.T) {
	sh := fakeShell(t, "printf 'READY\\n'")
	start := time.Now()
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(), InitCommand: "echo hello",
	})
	if !wait("GOT:echo hello", 5*time.Second) {
		t.Fatal("首字节到达后应写入启动命令")
	}
	if el := time.Since(start); el >= 2*time.Second {
		t.Errorf("首字节路径不应等到兜底：耗时 %v", el)
	}
}

// TestInitCommandFallbackWrites 钉住「超时不是失败」：shell 一直不出声，
// 3s 后照样写。
func TestInitCommandFallbackWrites(t *testing.T) {
	sh := fakeShell(t, "")
	start := time.Now()
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(), InitCommand: "echo hello",
	})
	if !wait("GOT:echo hello", 15*time.Second) {
		t.Fatal("shell 不出声时也必须在兜底到点后写入启动命令")
	}
	if el := time.Since(start); el < 2500*time.Millisecond {
		t.Errorf("兜底路径不该提前触发：耗时 %v", el)
	}
}

// TestInitCommandWritesExactlyCommandPlusNewline 钉住 Q4(a) 的拍板：
// 写进去的就是命令原文 + \n，不带任何前缀标记。
func TestInitCommandWritesExactlyCommandPlusNewline(t *testing.T) {
	sh := fakeShell(t, "printf 'READY\\n'")
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(), InitCommand: "echo hello",
	})
	if !wait("GOT:echo hello", 5*time.Second) {
		t.Fatal("应写入启动命令")
	}
	if wait("GOT:echo hello\r\nGOT:", 800*time.Millisecond) {
		t.Fatal("只应写入一行，实际写了不止一行")
	}
}
