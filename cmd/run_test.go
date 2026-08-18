// run_test.go —— handoff run 的参数拼接契约（B66）与命令透传解析测试（P1-13）。
//
// 覆盖：单参数按 shell 原文透传，多参数逐个 POSIX 单引号转义；以及
// `handoff run <task> <命令...>` 里任务名后的 -v/-race/-run 等 flag 必须原样进入
// args[1:]（cobra SetInterspersed(false），而不是被当 handoff 的未知 flag 报错）。
//
// 边界：本文件只测命令行拼接与 Cobra 参数解析，不起 agentd、不发请求——远端执行
// 语义由真机走查覆盖。
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

// TestShellJoin 钉住两档行为与转义规则。
//
// 单参数那一行是**回归防线**：对它也转义会把 `handoff run T1 "cd x && go test"`
// 改坏（整条命令被当成一个带空格的命令名）。
func TestShellJoin(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"单参数按原文透传", []string{"go test ./... && go vet ./..."}, "go test ./... && go vet ./..."},
		{"多参数安全字符不加引号", []string{"go", "test", "./..."}, "go test ./..."},
		{"含空格的参数加单引号", []string{"grep", "-rn", "foo bar", "."}, "grep -rn 'foo bar' ."},
		{"内嵌单引号按 '\\'' 拆开", []string{"echo", "it's"}, `echo 'it'\''s'`},
		{"元字符必须被引住", []string{"ls", "*.go"}, "ls '*.go'"},
		{"空串参数保留为空引号", []string{"echo", ""}, "echo ''"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shellJoin(c.args); got != c.want {
				t.Errorf("shellJoin(%q) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}
