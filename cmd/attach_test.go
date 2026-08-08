// attach/show 命令测试：attachCommandFor 的本机/远程组装、show 命令注册。
package cmd

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/config"
)

func TestAttachCommandForLocal(t *testing.T) {
	cfg := &config.Config{}
	argv, err := attachCommandFor("abcdefgh-1234", "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "attach", "-t", "handoff-abcdefgh"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("got %v want %v", argv, want)
	}
}

func TestAttachCommandForRemote(t *testing.T) {
	cfg := &config.Config{Targets: map[string]config.Target{"dev": {Addr: "devbox:7777"}}}
	argv, err := attachCommandFor("abcdefgh-1234", "dev", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh", "-t", "devbox", "tmux", "attach", "-t", "handoff-abcdefgh"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("got %v want %v", argv, want)
	}
}

func TestAttachCommandForUnknownTarget(t *testing.T) {
	if _, err := attachCommandFor("t", "ghost", &config.Config{}); err == nil {
		t.Fatalf("未配对 target 应报错")
	}
}

// TestShowCommandRegistered 防止改名回归：rootCmd 下存在 "show"（快照）命令，
// 且 attach 的 Short 已是终端实况语义。
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

// TestRunAttachExecvesResolvedPath 覆盖 runAttach 的 exec 调用（P0-1）：
// syscall.Exec 是 execve(2) 直接封装、不做 PATH 查找，第一参必须是 LookPath
// 解析出的可执行文件绝对路径；argv[0] 保持裸名（execve 约定）。
func TestRunAttachExecvesResolvedPath(t *testing.T) {
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux 未安装，无法验证")
	}
	var gotBin string
	var gotArgv []string
	oldExec := execveFn
	execveFn = func(argv0 string, argv []string, env []string) error {
		gotBin, gotArgv = argv0, argv
		return nil
	}
	t.Cleanup(func() { execveFn = oldExec })

	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:7777\"\ntoken: \"t\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runAttach(cmd, nil, "abcdefgh-1234"); err != nil {
		t.Fatalf("runAttach: %v", err)
	}
	if gotBin != tmuxBin {
		t.Fatalf("execveFn 第一参应为 LookPath 解析路径，got %q want %q", gotBin, tmuxBin)
	}
	if !filepath.IsAbs(gotBin) {
		t.Fatalf("execveFn 第一参应为绝对路径，got %q", gotBin)
	}
	if len(gotArgv) == 0 || gotArgv[0] != "tmux" {
		t.Fatalf("argv[0] 应保持裸名 tmux，got %v", gotArgv)
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
