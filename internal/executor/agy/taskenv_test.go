package agy

import (
	"os"
	"strings"
	"testing"
)

func TestWriteTaskEnv(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()

	hooksPath, prompt, err := WriteTaskEnv(workDir, taskDir, "T1", "# Plan", "/tmp/perm.sock", "/bin/handoff", "discipline")
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

	content := string(data)
	if !strings.Contains(content, "/bin/handoff permission-hook --sock /tmp/perm.sock") {
		t.Fatalf("hooks.json 未包含预期命令: %s", content)
	}
	if !strings.Contains(content, "run_command") {
		t.Fatalf("hooks.json 未包含 matcher run_command: %s", content)
	}
}
