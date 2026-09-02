//go:build unix

package agentd

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/proto"
)

func launcherShell(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launcher-shell")
	script := "#!/bin/sh\nprintf 'MARK=%s TERM=%s SINK=%s\\n' \"$LAUNCHER_MARK\" \"$TERM\" \"$GROK_OSC52_SINK\"\nwhile IFS= read -r line; do printf 'GOT:%s\\n' \"$line\"; done\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("写测试 shell: %v", err)
	}
	return path
}

func createLauncherSession(t *testing.T, env *testAgentdEnv, body string) proto.PtySession {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows 不支持 PTY")
	}
	return ptyCreate(t, env, body)
}

func attachmentWait(t *testing.T, env *testAgentdEnv, id, want string, within time.Duration) bool {
	t.Helper()
	a, err := env.srv.pty.Attach(id, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(a.Detach)
	var got bytes.Buffer
	got.Write(a.Backlog)
	if strings.Contains(got.String(), want) {
		return true
	}
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	for {
		select {
		case b, ok := <-a.Out:
			if !ok {
				return strings.Contains(got.String(), want)
			}
			got.Write(b)
			if strings.Contains(got.String(), want) {
				return true
			}
		case <-deadline.C:
			return false
		}
	}
}

func newLauncherPtyEnv(t *testing.T) (*testAgentdEnv, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	env := newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, DataDir: dataDir},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	sh := launcherShell(t)
	t.Setenv("SHELL", sh)
	t.Setenv("HOME", t.TempDir())
	return env, dataDir, sh
}

func writeLauncherEnv(t *testing.T, dataDir, name, content string) {
	t.Helper()
	dir := envfile.Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建 env 目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("写 env 文件: %v", err)
	}
}

func TestCreatePtySessionNoLauncherFieldsUnchanged(t *testing.T) {
	env, _, _ := newLauncherPtyEnv(t)
	sess := createLauncherSession(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	if !attachmentWait(t, env, sess.ID, "TERM=xterm-256color", 5*time.Second) {
		t.Fatal("不带启动项字段时基础 sessionEnv 未保持原行为")
	}
	if !attachmentWait(t, env, sess.ID, "SINK=1", 5*time.Second) {
		t.Fatal("不带启动项字段时 PTY 进程环境必须带 GROK_OSC52_SINK=1")
	}
	if attachmentWait(t, env, sess.ID, "GOT:", 300*time.Millisecond) {
		t.Fatal("不带启动项字段时不得向 PTY 写入命令")
	}
}

func TestCreatePtySessionEnvFileOnly(t *testing.T) {
	env, dataDir, _ := newLauncherPtyEnv(t)
	writeLauncherEnv(t, dataDir, "launcher.env", "LAUNCHER_MARK=from-file\n")
	sess := createLauncherSession(t, env, `{"base_kind":"home","env_file":"launcher.env"}`)
	if !attachmentWait(t, env, sess.ID, "MARK=from-file", 5*time.Second) {
		t.Fatal("env 文件变量没有进入终端环境")
	}
}

func TestCreatePtySessionCommandOnly(t *testing.T) {
	env, _, _ := newLauncherPtyEnv(t)
	sess := createLauncherSession(t, env, `{"base_kind":"home","init_command":"echo command-only"}`)
	if !attachmentWait(t, env, sess.ID, "GOT:echo command-only", 5*time.Second) {
		t.Fatal("init_command 没有原样透传进终端")
	}
}

func TestCreatePtySessionBoth(t *testing.T) {
	env, dataDir, _ := newLauncherPtyEnv(t)
	writeLauncherEnv(t, dataDir, "both.env", "LAUNCHER_MARK=both\n")
	sess := createLauncherSession(t, env, `{"base_kind":"home","env_file":"both.env","init_command":"echo both"}`)
	if !attachmentWait(t, env, sess.ID, "MARK=both", 5*time.Second) {
		t.Fatal("env_file 与 init_command 同时存在时 env 未透传")
	}
	if !attachmentWait(t, env, sess.ID, "GOT:echo both", 5*time.Second) {
		t.Fatal("env_file 与 init_command 同时存在时命令未透传")
	}
}

// TestPinGrokOsc52SinkStripsDuplicates execve/getenv 取首次出现。输入里
// 已有 0 时只 append 会留下 0 在前；必须剥掉同名键再钉 1。
func TestPinGrokOsc52SinkStripsDuplicates(t *testing.T) {
	got := pinGrokOsc52Sink([]string{"A=1", "GROK_OSC52_SINK=0", "B=2", "GROK_OSC52_SINK="})
	var sinks []string
	for _, e := range got {
		if strings.HasPrefix(e, "GROK_OSC52_SINK=") {
			sinks = append(sinks, e)
		}
	}
	if len(sinks) != 1 || sinks[0] != "GROK_OSC52_SINK=1" {
		t.Fatalf("钉死结果=%v sinks=%v，want 恰好一个 GROK_OSC52_SINK=1", got, sinks)
	}
}

// TestCreatePtySessionPinsGrokOsc52Sink 锁 B303：GROK_OSC52_SINK=1 写死在
// PTY 进程环境末尾，launcher env_file 把它写成 0 也不能关。
func TestCreatePtySessionPinsGrokOsc52Sink(t *testing.T) {
	env, dataDir, _ := newLauncherPtyEnv(t)
	writeLauncherEnv(t, dataDir, "nosink.env", "GROK_OSC52_SINK=0\n")
	sess := createLauncherSession(t, env, `{"base_kind":"home","env_file":"nosink.env"}`)
	if !attachmentWait(t, env, sess.ID, "SINK=1", 5*time.Second) {
		t.Fatal("env_file 把 GROK_OSC52_SINK 写成 0 时仍必须是 1")
	}
	if attachmentWait(t, env, sess.ID, "SINK=0", 300*time.Millisecond) {
		t.Fatal("进程环境不得留下 GROK_OSC52_SINK=0")
	}
}

func TestCreatePtySessionEnvFileOverridesSessionEnv(t *testing.T) {
	env, dataDir, _ := newLauncherPtyEnv(t)
	writeLauncherEnv(t, dataDir, "term.env", "TERM=dumb\n")
	sess := createLauncherSession(t, env, `{"base_kind":"home","env_file":"term.env"}`)
	if !attachmentWait(t, env, sess.ID, "MARK= TERM=dumb", 5*time.Second) {
		t.Fatal("env 文件定义的 TERM 没有覆盖 sessionEnv 的缺省值")
	}
}

func TestCreatePtySessionMissingEnvFileRejected(t *testing.T) {
	env, _, _ := newLauncherPtyEnv(t)
	before := len(env.srv.pty.List())
	resp, body := ptyPost(t, env, `{"base_kind":"home","env_file":"missing.env"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "missing.env") {
		t.Fatalf("缺失 env 文件应返回点名文件的 400，得到 %d: %s", resp.StatusCode, body)
	}
	if got := len(env.srv.pty.List()); got != before {
		t.Fatalf("env 文件错误时不应创建会话，创建前后 %d -> %d", before, got)
	}
}

func TestCreatePtySessionValidEnvFileCreatesSession(t *testing.T) {
	env, dataDir, _ := newLauncherPtyEnv(t)
	writeLauncherEnv(t, dataDir, "valid.env", "A=1\n")
	before := len(env.srv.pty.List())
	_ = createLauncherSession(t, env, `{"base_kind":"home","env_file":"valid.env"}`)
	if got := len(env.srv.pty.List()); got != before+1 {
		t.Fatalf("合法 env 文件应创建一个会话，创建前后 %d -> %d", before, got)
	}
}

func TestCreatePtySessionEnvFileWithSeparatorRejected(t *testing.T) {
	env, _, _ := newLauncherPtyEnv(t)
	resp, body := ptyPost(t, env, `{"base_kind":"home","env_file":"../bad.env"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "env 文件名非法") {
		t.Fatalf("非法 env 文件名应透传 ErrBadName，得到 %d: %s", resp.StatusCode, body)
	}
}

func TestStatusReportsLaunchersSupported(t *testing.T) {
	env := newTestAgentdEnv(t)
	m, _, _, _ := newTestManager(t)
	env.srv.SetManager(m)
	var st proto.StatusResp
	if code := env.getJSON(t, "/api/status", &st); code != http.StatusOK {
		t.Fatalf("GET /api/status = %d", code)
	}
	if st.LaunchersSupported == nil || !*st.LaunchersSupported {
		t.Fatalf("launchers_supported 应为 true，实得 %v", st.LaunchersSupported)
	}
}

func TestLocalMachineReportsLaunchersSupported(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, DataDir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	m, _, _, _ := newTestManager(t)
	env.srv.SetManager(m)
	machine := env.srv.localMachine()
	if machine.LaunchersSupported == nil || !*machine.LaunchersSupported {
		t.Fatalf("本机 launchers_supported 应为 true，实得 %v", machine.LaunchersSupported)
	}
}

func TestFillFromStatusCarriesLaunchersSupportedIncludingNil(t *testing.T) {
	yes := true
	var m proto.Machine
	fillFromStatus(&m, &proto.StatusResp{LaunchersSupported: &yes})
	if m.LaunchersSupported == nil || !*m.LaunchersSupported {
		t.Fatalf("true 没被搬运过来：%v", m.LaunchersSupported)
	}
	var m2 proto.Machine
	fillFromStatus(&m2, &proto.StatusResp{})
	if m2.LaunchersSupported != nil {
		t.Fatalf("对端没上报时应保持 nil，实际 %v", *m2.LaunchersSupported)
	}
}
