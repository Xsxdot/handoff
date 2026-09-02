package agy

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestGitRepo(t *testing.T, workDir string) {
	t.Helper()
	commands := [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@handoff.dev"},
		{"config", "user.name", "test"},
	}
	for _, args := range commands {
		if out, err := exec.Command("git", append([]string{"-C", workDir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Hello\n"), 0644); err != nil {
		t.Fatalf("写 README.md 失败: %v", err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", workDir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败: %v\n%s", args, err, out)
		}
	}
}

func testGitExcludePath(t *testing.T, workDir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", workDir, "rev-parse", "--git-path", "info/exclude").CombinedOutput()
	if err != nil {
		t.Fatalf("获取 info/exclude 路径失败: %v\n%s", err, out)
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}
	return path
}

func assertGitStatusEmpty(t *testing.T, workDir string) {
	t.Helper()
	out, err := exec.Command("git", "-C", workDir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status 失败: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Fatalf("git status --porcelain 非空:\n%s", out)
	}
}

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
	for _, line := range strings.Split(string(excludeData), "\n") {
		if strings.TrimSpace(line) == ".agents/" {
			t.Fatalf("info/exclude 不应隐藏整个 .agents/ 目录:\n%s", excludeData)
		}
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

func TestRestoreTrackedHooks(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()
	initTestGitRepo(t, workDir)

	hooksPath := filepath.Join(workDir, agentsDirName, hooksFileName)
	original := []byte("{\"user-linter\":{}}\n")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		t.Fatalf("创建 .agents 目录失败: %v", err)
	}
	if err := os.WriteFile(hooksPath, original, 0644); err != nil {
		t.Fatalf("写原始 hooks.json 失败: %v", err)
	}
	for _, args := range [][]string{{"add", ".agents/hooks.json"}, {"commit", "-q", "-m", "add hooks"}} {
		if out, err := exec.Command("git", append([]string{"-C", workDir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败: %v\n%s", args, err, out)
		}
	}

	gotPath, _, err := WriteTaskEnv(workDir, taskDir, "T-tracked", "# Plan", "/tmp/perm.sock", "/bin/handoff", "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}
	if gotPath != hooksPath {
		t.Fatalf("hooks 路径不符合预期: got %s, want %s", gotPath, hooksPath)
	}
	if err := RestoreTaskEnv(taskDir); err != nil {
		t.Fatalf("RestoreTaskEnv 失败: %v", err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("读取还原后的 hooks.json 失败: %v", err)
	}
	if strings.Contains(string(data), "perm.sock") {
		t.Fatalf("还原后的 hooks.json 仍含 perm.sock: %s", data)
	}
	if !strings.Contains(string(data), "user-linter") {
		t.Fatalf("还原后的 hooks.json 丢失 user-linter: %s", data)
	}
	assertGitStatusEmpty(t, workDir)
	if err := os.WriteFile(hooksPath, append(data, []byte("x\n")...), 0644); err != nil {
		t.Fatalf("修改还原后的 hooks.json 失败: %v", err)
	}
	out, err := exec.Command("git", "-C", workDir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("再次 git status 失败: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("清除 skip-worktree 后再次修改 hooks.json 应可见")
	}
	if _, err := os.Stat(filepath.Join(taskDir, restoreFileName)); !os.IsNotExist(err) {
		t.Fatalf("Restore 后 sidecar 应不存在，实得: %v", err)
	}
}

func TestRestoreNewHooks(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()
	initTestGitRepo(t, workDir)

	hooksPath, _, err := WriteTaskEnv(workDir, taskDir, "T-new", "# Plan", "/tmp/perm.sock", "/bin/handoff", "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}
	if _, err := os.Stat(hooksPath); err != nil {
		t.Fatalf("WriteTaskEnv 应创建 hooks.json: %v", err)
	}
	data, err := os.ReadFile(hooksPath)
	if err != nil || !strings.Contains(string(data), "handoff-safety-gate") {
		t.Fatalf("新建 hooks.json 缺 handoff-safety-gate: %v\n%s", err, data)
	}

	if err := RestoreTaskEnv(taskDir); err != nil {
		t.Fatalf("RestoreTaskEnv 失败: %v", err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Fatalf("Restore 新建 hooks 后文件应不存在，实得: %v", err)
	}
	assertGitStatusEmpty(t, workDir)
	excludeData, err := os.ReadFile(testGitExcludePath(t, workDir))
	if err != nil {
		t.Fatalf("读取 info/exclude 失败: %v", err)
	}
	for _, line := range strings.Split(string(excludeData), "\n") {
		if strings.TrimSpace(line) == ".agents/" || strings.TrimSpace(line) == ".agents/hooks.json" {
			t.Fatalf("Restore 后不应保留本轮 exclude 规则 %q:\n%s", strings.TrimSpace(line), excludeData)
		}
	}
}

func TestRestorePreservesExistingHooksExclude(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()
	initTestGitRepo(t, workDir)
	excludePath := testGitExcludePath(t, workDir)
	excludeData, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("读取初始 info/exclude 失败: %v", err)
	}
	if err := os.WriteFile(excludePath, append(excludeData, []byte("\n.agents/hooks.json\n")...), 0644); err != nil {
		t.Fatalf("写入既有 hooks exclude 失败: %v", err)
	}

	if _, _, err := WriteTaskEnv(workDir, taskDir, "T-existing-exclude", "# Plan", "/tmp/perm.sock", "/bin/handoff", ""); err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}
	if err := RestoreTaskEnv(taskDir); err != nil {
		t.Fatalf("RestoreTaskEnv 失败: %v", err)
	}

	excludeData, err = os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("读取还原后的 info/exclude 失败: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(excludeData), "\n") {
		if strings.TrimSpace(line) == ".agents/hooks.json" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Restore 不应删除任务前已有的 hooks exclude，实得 %d 条:\n%s", count, excludeData)
	}
}

func TestRestoreMissingSidecarIsIdempotent(t *testing.T) {
	if err := RestoreTaskEnv(t.TempDir()); err != nil {
		t.Fatalf("缺 sidecar 的 Restore 应幂等成功: %v", err)
	}
}
