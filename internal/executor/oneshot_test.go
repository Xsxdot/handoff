// executor 包 one-shot 调用映射测试。
package executor_test

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestOneShotArgs(t *testing.T) {
	cases := []struct {
		name, exec, model, prompt string
		want                      []string
		wantErr                   bool
	}{
		{"opencode 带模型", "opencode", "m1", "p", []string{"opencode", "run", "-m", "m1", "p"}, false},
		{"opencode 无模型", "opencode", "", "p", []string{"opencode", "run", "p"}, false},
		{"claude 带模型", "claude", "haiku", "p", []string{"claude", "-p", "--model", "haiku", "p"}, false},
		{"claude 无模型", "claude", "", "p", []string{"claude", "-p", "p"}, false},
		{"grok 带模型", "grok", "grok-4.5", "p",
			[]string{"grok", "--effort", "low", "-m", "grok-4.5", "-p", "p"}, false},
		{"grok 不带模型", "grok", "", "p",
			[]string{"grok", "--effort", "low", "-p", "p"}, false},
		{"agy 带模型", "agy", "claude-3-5-sonnet", "p", []string{"agy", "--model", "claude-3-5-sonnet", "-p", "p"}, false},
		{"agy 无模型", "agy", "", "p", []string{"agy", "-p", "p"}, false},
		{"codex 无模型", "codex", "", "p", []string{"codex", "exec", "--skip-git-repo-check", "--ephemeral", "--color", "never", "--sandbox", "read-only", "--ignore-user-config", "p"}, false},
		{"codex 带模型", "codex", "gpt-5", "p", []string{"codex", "exec", "--skip-git-repo-check", "--ephemeral", "--color", "never", "--sandbox", "read-only", "--ignore-user-config", "-m", "gpt-5", "p"}, false},
		{"未知执行者", "gemini", "", "p", nil, true},
	}
	for _, c := range cases {
		got, err := executor.OneShotArgs(c.exec, c.model, c.prompt)
		if c.wantErr != (err != nil) {
			t.Fatalf("%s: err=%v", c.name, err)
		}
		if !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
