package agy

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTaskEnv(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()

	hooksPath, prompt, err := WriteTaskEnv(workDir, taskDir, "T1", "# Plan", "/tmp/perm.sock", "/bin/my handoff", "discipline")
	if err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}

	if !strings.Contains(prompt, "# Plan") {
		t.Fatalf("prompt 未包含计划内容: %s", prompt)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("读取 hooks.json 失败: %v", err)
	}

	var parsed struct {
		HandoffSafetyGate struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"handoff-safety-gate"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("解析 hooks.json 失败: %v", err)
	}

	if len(parsed.HandoffSafetyGate.PreToolUse) == 0 || len(parsed.HandoffSafetyGate.PreToolUse[0].Hooks) == 0 {
		t.Fatalf("hooks.json 配置为空: %+v", parsed)
	}

	hook := parsed.HandoffSafetyGate.PreToolUse[0].Hooks[0]
	expectedCmd := `"/bin/my handoff" permission-hook --sock "/tmp/perm.sock"`
	if hook.Command != expectedCmd {
		t.Fatalf("command 不符合预期: got %s, want %s", hook.Command, expectedCmd)
	}
	if parsed.HandoffSafetyGate.PreToolUse[0].Matcher != "run_command|write_to_file|replace_file_content|multi_replace_file_content|sed_file|read_url_content|search_web|invoke_subagent" {
		t.Fatalf("matcher 不符合预期: %s", parsed.HandoffSafetyGate.PreToolUse[0].Matcher)
	}
	if hook.Timeout != 86400 {
		t.Fatalf("timeout 不符合预期: got %d, want 86400", hook.Timeout)
	}
}

func TestWriteTaskEnvMergesExistingHooks(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()

	agentsDir := filepath.Join(workDir, ".agents")
	_ = os.MkdirAll(agentsDir, 0755)
	existingPath := filepath.Join(agentsDir, "hooks.json")
	existingContent := `{"user-linter":{"PostToolUse":[{"matcher":"run_command","hooks":[{"command":"./lint.sh"}]}]}}`
	_ = os.WriteFile(existingPath, []byte(existingContent), 0644)

	hooksPath, _, err := WriteTaskEnv(workDir, taskDir, "T2", "# Plan", "/tmp/perm.sock", "/bin/handoff", "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("读取 hooks.json 失败: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("解析 hooks.json 失败: %v", err)
	}

	if _, ok := parsed["user-linter"]; !ok {
		t.Fatalf("既有 user-linter 钩子被覆盖丢失")
	}
	if _, ok := parsed["handoff-safety-gate"]; !ok {
		t.Fatalf("handoff-safety-gate 钩子未写入")
	}
}

func TestWriteTaskEnvGitExcludeCleanStatus(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()

	// 初始化真实 git 仓库并做首次提交
	_ = exec.Command("git", "-C", workDir, "init", "-q").Run()
	_ = exec.Command("git", "-C", workDir, "config", "user.email", "test@handoff.dev").Run()
	_ = exec.Command("git", "-C", workDir, "config", "user.name", "test").Run()
	_ = os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Hello\n"), 0644)
	_ = exec.Command("git", "-C", workDir, "add", ".").Run()
	_ = exec.Command("git", "-C", workDir, "commit", "-q", "-m", "init").Run()

	// 确认调用前 git 干净
	outBefore, err := exec.Command("git", "-C", workDir, "status", "--porcelain").CombinedOutput()
	if err != nil || len(outBefore) > 0 {
		t.Fatalf("前置 git status 异常: %v\n%s", err, outBefore)
	}

	// 执行 WriteTaskEnv
	_, _, err = WriteTaskEnv(workDir, taskDir, "T-Clean", "# Plan", "/tmp/perm.sock", "/bin/handoff", "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}

	// 验证 .git/info/exclude 是否包含了 .agents 规则
	excludeOut, err := exec.Command("git", "-C", workDir, "rev-parse", "--git-path", "info/exclude").CombinedOutput()
	if err != nil {
		t.Fatalf("获取 info/exclude 路径失败: %v", err)
	}
	excludePath := strings.TrimSpace(string(excludeOut))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(workDir, excludePath)
	}
	excludeData, err := os.ReadFile(excludePath)
	if err != nil || !strings.Contains(string(excludeData), ".agents/hooks.json") {
		t.Fatalf("info/exclude 缺少排除规则: %v\n%s", err, excludeData)
	}

	// 关键断言：WriteTaskEnv 生成 .agents/hooks.json 之后，git status --porcelain 必须完全干净！
	outAfter, err := exec.Command("git", "-C", workDir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status 失败: %v", err)
	}
	if len(outAfter) > 0 {
		t.Fatalf("WriteTaskEnv 导致工作区变脏，git status --porcelain 实得:\n%s", string(outAfter))
	}
}
