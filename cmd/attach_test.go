// attach/show 命令测试：attachCommandFor 的本机/远程组装、show 命令注册。
package cmd

import (
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

// findRootCmd 在根命令下查找指定 Use 首词的子命令。
func findRootCmd(use string) *cobra.Command {
	for _, c := range rootCmd.Commands() {
		if c.Name() == use {
			return c
		}
	}
	return nil
}
