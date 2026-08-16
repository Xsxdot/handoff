// attach/show 命令测试：attach 的 render 流语义、show 命令注册。
package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// TestSSHHostFromTarget 覆盖 target → ssh 目标的换算共用函数（pull 专用）：
// user 非空 → user@host；user 为空 → 只有 host（与历史行为一致）。
func TestSSHHostFromTarget(t *testing.T) {
	cases := []struct {
		name string
		trg  config.Target
		want string
	}{
		{"user 非空带端口", config.Target{Addr: "100.73.238.21:7777", User: "sycm"}, "sycm@100.73.238.21"},
		{"user 为空带端口", config.Target{Addr: "devbox:7777"}, "devbox"},
		{"user 非空无端口", config.Target{Addr: "devbox", User: "sycm"}, "sycm@devbox"},
		{"user 为空无端口", config.Target{Addr: "devbox"}, "devbox"},
	}
	for _, c := range cases {
		if got := sshHostFromTarget(c.trg); got != c.want {
			t.Fatalf("%s: sshHostFromTarget=%q, want %q", c.name, got, c.want)
		}
	}
}

func TestShowCommandRegistered(t *testing.T) {
	show := findRootCmd("show")
	if show == nil {
		t.Fatal("rootCmd 下应存在 show 命令（快照改名后的出口）")
	}
	attach := findRootCmd("attach")
	if attach == nil {
		t.Fatal("rootCmd 下应存在 attach 命令")
	}
	if !strings.Contains(attach.Short, "终端") {
		t.Fatalf("attach 的 Short 应为终端实况语义，得到 %q", attach.Short)
	}
}

// TestRunAttachStreamsToStdout 钉死 attach 的新语义：
// 从 agentd 的 render endpoint 取流并原样打印，不再 exec 任何外部命令。
func TestRunAttachStreamsToStdout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/render") {
			w.Header().Set("X-Handoff-Render-Size", "5")
			w.Write([]byte("hello"))
			return
		}
		w.Write([]byte(`{"task":{"id":"T1","target":""}}`))
	}))
	defer ts.Close()

	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	c.SetContext(context.Background())
	cli := client.New(ts.Listener.Addr().String(), "tok")
	if err := runAttach(c, cli, "T1"); err != nil {
		t.Fatalf("runAttach 失败: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("实况未打印到 stdout，实得 %q", out.String())
	}
}

// TestRunAttachRemoteNeedsNoSSH 钉死跨平台收益：
// 远程 target 不再拼 ssh 命令——复用 agentd 连接即可，因此 Windows 协调者也能用。
func TestRunAttachRemoteNeedsNoSSH(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/render") {
			w.Header().Set("X-Handoff-Render-Size", "2")
			w.Write([]byte("ok"))
			return
		}
		w.Write([]byte(`{"task":{"id":"T1","target":"devbox"}}`))
	}))
	defer ts.Close()

	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	c.SetContext(context.Background())
	if err := runAttach(c, client.New(ts.Listener.Addr().String(), "tok"), "T1"); err != nil {
		t.Fatalf("远程 target 的 attach 失败: %v", err)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("远程实况未打印，实得 %q", out.String())
	}
}

// TestIsTTYRejectsDevNull 覆盖修复 5：/dev/null 是字符设备但绝不是终端——旧实现只判
// os.ModeCharDevice 会把 /dev/null 误判成 TTY，导致脚本按标准做法 handoff attach
// < /dev/null 走进交互分支、打完表格再报「读取选择」错误（非 TTY 降级路径在最该
// 生效的场景里失效）。go-isatty 判 ioctl 终端语义，/dev/null 无终端属性 → false。
func TestIsTTYRejectsDevNull(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("打开 %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	oldStdin := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = oldStdin }()
	if isTTY() {
		t.Fatalf("/dev/null 应被判为非终端（旧实现按 ModeCharDevice 误判）")
	}
}

// findRootCmd 在根命令下查找指定 Use 首词的子命令。
func findRootCmd(use string) *cobra.Command {
	for _, c := range rootCmd.Commands() {
		if c.Name() == use {
			return c
		}
	}
	return nil
}

// TestPickAttachTaskNonTTYIncludesTarget 验证非 TTY 建议命令带 --target。
// why：远程任务照抄不带 --target 的命令会打到本机 agentd——先 404，
// 再 attach 一个本机根本不存在的会话，两条错都指不到真正的原因。
func TestPickAttachTaskNonTTYIncludesTarget(t *testing.T) {
	tasks := []proto.TaskView{
		{Task: proto.Task{ID: "aaaaaaaa-1111", Target: "devbox", State: proto.TaskStateRunning, Executor: "opencode"}},
		{Task: proto.Task{ID: "bbbbbbbb-2222", Target: "", State: proto.TaskStateRunning, Executor: "opencode"}},
	}
	var buf bytes.Buffer
	printAttachSuggestions(&buf, tasks)
	got := buf.String()
	if !strings.Contains(got, "handoff attach aaaaaaaa-1111 --target devbox") {
		t.Errorf("远程任务的建议命令必须带 --target，实得:\n%s", got)
	}
	if strings.Contains(got, "handoff attach bbbbbbbb-2222 --target") {
		t.Errorf("本机任务不应带 --target，实得:\n%s", got)
	}
}
