// run 子命令的命令透传解析测试（P1-13）。
//
// 覆盖：`handoff run <task> <命令...>` 里任务名后的 -v/-race/-run 等 flag 必须
// 原样进入 args[1:]（cobra SetInterspersed(false)），而不是被当 handoff 的未知
// flag 报错——审核者最主要的验证动作（跑测试）直接可用。
package cmd

import (
	"context"
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

// TestRunCommandPassesArgsVerbatim：`handoff run T1 go test -v -run X ./...` 的
// 命令参数必须原样到达 RunE。cobra 默认的 flag 穿插解析会把任务名后的 -v/-run
// 当 handoff 的 flag 吃掉并报 unknown flag（P1-13 修复前的直接报错路径）；
// SetInterspersed(false) 后解析在首个位置参数（任务名）处停止，命令原文进入
// args[1:]。RunE 用桩替换，聚焦 cobra 解析本身。
func TestRunCommandPassesArgsVerbatim(t *testing.T) {
	var gotArgs []string
	oldRunE := runCmd.RunE
	runCmd.RunE = func(cmd *cobra.Command, args []string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	t.Cleanup(func() { runCmd.RunE = oldRunE })

	rootCmd.SetArgs([]string{"run", "T1", "go", "test", "-v", "-run", "TestX", "./..."})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	want := []string{"T1", "go", "test", "-v", "-run", "TestX", "./..."}
	if !slices.Equal(gotArgs, want) {
		t.Fatalf("run 收到的 args=%q, want %q（命令参数必须原样透传）", gotArgs, want)
	}
}
