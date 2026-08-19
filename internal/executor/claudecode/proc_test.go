package claudecode

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// TestClaudeArgvHasNoShell 钉死「argv 直传、不经任何 shell」：
// 旧实现把命令拼进 sh 脚本，值里的 $ 会被二次展开、引号会改变语义。
// 换成 argv 后这类问题从根上消失——但只有断言 argv 结构才能防止回退。
func TestClaudeArgvHasNoShell(t *testing.T) {
	argv := claudeArgv(StartProcReq{
		SessionID:    "sess-1",
		Model:        "opus",
		SettingsPath: "/tmp/a b/settings.json", // 含空格：旧实现必须引号转义，新实现天然安全
		MCPPath:      "/tmp/mcp.json",
	})
	if argv[0] != "claude" {
		t.Fatalf("argv[0] 必须是 claude，实得 %q", argv[0])
	}
	for _, a := range argv {
		if strings.Contains(a, "'") || strings.Contains(a, "\\") {
			t.Fatalf("argv 不应含 shell 引号/转义残留: %q", a)
		}
	}
	joined := strings.Join(argv, "\x00")
	for _, want := range []string{
		"--session-id\x00sess-1", "--model\x00opus",
		"--settings\x00/tmp/a b/settings.json",
		"--permission-prompt-tool\x00mcp__handoff__ask",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv 缺 %q，实得 %v", want, argv)
		}
	}
	// Resume 与非 Resume 语义相反，写错的表现是「日志说恢复成功、模型什么都不记得」
	rargv := claudeArgv(StartProcReq{SessionID: "sess-1", Resume: true})
	rjoined := strings.Join(rargv, "\x00")
	if !strings.Contains(rjoined, "--resume\x00sess-1") ||
		strings.Contains(rjoined, "--session-id") {
		t.Fatalf("Resume=true 必须用 --resume 且不带 --session-id，实得 %v", rargv)
	}
}

// TestStartProcWritesProcInfoBeforeSpawn 钉死写前置时序：
// proc.json 必须在 Start 之前落盘，否则 Start 成功但进程记录缺失时 Reap 无据可查。
func TestStartProcWritesProcInfoBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	stubClaudeLookup(t)
	var infoExistedAtSpawn bool
	orig := startProcHost
	startProcHost = func(spec prochost.Spec, selfExe string, extra ...string) (prochost.Handle, error) {
		_, err := os.Stat(filepath.Join(dir, procInfoFileName))
		infoExistedAtSpawn = err == nil
		return prochost.Handle{PID: 4242, LockPath: spec.LockPath}, nil
	}
	t.Cleanup(func() { startProcHost = orig })

	// FIFO 读端由假 shim 不会打开，把等待超时调到毫秒级快速走完
	origTimeout := fifoReaderTimeout
	fifoReaderTimeout = 10 * time.Millisecond
	t.Cleanup(func() { fifoReaderTimeout = origTimeout })

	_, _ = StartProc(context.Background(), StartProcReq{
		RepoPath: dir, TaskID: "abcdefgh12", TaskDir: dir, SessionID: "s1",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !infoExistedAtSpawn {
		t.Fatal("proc.json 必须在拉起 shim 之前落盘（写前置时序）")
	}
}

// TestProcAliveDelegatesToLock 钉死「存活只看锁」：
// 旧实现看 tmux has-session，而第二窗口的 tail -f 会吊着会话导致假存活。
func TestProcAliveDelegatesToLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	p := &Proc{Handle: prochost.Handle{PID: os.Getpid(), LockPath: lock}, TaskDir: dir}

	if p.Alive() {
		t.Fatal("锁无人持有时必须判死")
	}
	// 写入死亡哨兵不应影响「锁被持有 = 活着」之外的判定顺序：
	// claude 的完整判据是「锁被持有 且 无哨兵」，这里只验证锁这一半
	if err := os.WriteFile(filepath.Join(dir, outFileName),
		[]byte(`{"type":"handoff_exit","code":0}`+"\n"), 0o600); err != nil {
		t.Fatalf("造哨兵失败: %v", err)
	}
	if p.Alive() {
		t.Fatal("有哨兵时必须判死")
	}
}

func TestProcExitedDetectsSentinel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, outFileName)

	if err := os.WriteFile(out, []byte(`{"type":"system","subtype":"init"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exited, _ := procExited(out); exited {
		t.Error("无哨兵时不应判死")
	}

	f, err := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"handoff_exit","code":3}` + "\n")
	f.Close()

	exited, code := procExited(out)
	if !exited || code != 3 {
		t.Errorf("哨兵应判死且带退出码，得到 exited=%v code=%d", exited, code)
	}
}

func TestWriteInputRoundTrip(t *testing.T) {
	// 本用例验的是 Unix FIFO 那条路：CreateInputChannel 建出一个真文件，
	// 读端能 O_RDWR 常开。Windows 上输入通道是**命名管道**，且服务端归 shim
	// 建——CreateInputChannel 在那儿是刻意的 no-op（prochost 的
	// TestWindowsCreateInputChannelIsNoop 正是钉这一点），所以它既不报错、
	// 也不留下任何可打开的文件。
	//
	// 2026-08-18 之前这里的守卫是「CreateInputChannel 返回错误就 skip」，
	// 而 Windows 上它返回 nil，守卫整个失效，直接栽在下面的 OpenFile 上
	//（run 32149311654）。Windows 那条路由
	// internal/prochost/inputch_windows_test.go 覆盖，含 B128 真机抓到的
	// ERROR_PIPE_BUSY 窗口，不存在没人测的缺口。
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的输入通道是命名管道、服务端由 shim 建，见 prochost/inputch_windows_test.go")
	}
	dir := t.TempDir()
	p := &Proc{TaskDir: dir}
	if err := prochost.CreateInputChannel(filepath.Join(dir, fifoFileName)); err != nil {
		t.Skipf("mkfifo 不可用（平台限制）: %v", err)
	}
	// 读端常开，模拟 shim 的 O_RDWR
	rd, err := os.OpenFile(filepath.Join(dir, fifoFileName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	if err := p.WriteInput("你好"); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	buf := make([]byte, 256)
	n, err := rd.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	line := string(buf[:n])
	if !strings.Contains(line, `"type":"user"`) || !strings.Contains(line, "你好") {
		t.Errorf("fifo 内容不符: %q", line)
	}
}
