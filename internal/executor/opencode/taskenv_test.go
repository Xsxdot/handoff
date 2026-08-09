// taskenv 测试：验证任务环境物料（opencode.json / prompt.md）的生成契约，
// 覆盖幂等覆盖与损坏 JSON 容错。回合协议 trailer 提取语义的测试在
// internal/executor/turn/protocol_test.go（B3 抽取共享包后随实现迁移）。
package opencode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/opencode"
)

// TestTaskModelOverridesEnv 验证任务级 model 优先于 HANDOFF_OPENCODE_MODEL：
// 两源都设置时写出 json 的 model 取任务值。
func TestTaskModelOverridesEnv(t *testing.T) {
	quietLog(t)
	t.Setenv("HANDOFF_OPENCODE_MODEL", "env-model")
	taskDir := t.TempDir()
	configPath, _, err := opencode.WriteTaskEnv(taskDir, "t1", "task-model", "plan")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Model string `json:"model"`
	}
	raw, _ := os.ReadFile(configPath)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "task-model" {
		t.Fatalf("任务级 model 应优先于 env：json.model=%q", cfg.Model)
	}
}

// TestTaskModelFallsBackToEnvThenEmpty 验证 model 三级优先级：
// 任务 model 空 → 回退 env；env 也空 → 不写 model 键（omitempty，executor 自身默认）。
func TestTaskModelFallsBackToEnvThenEmpty(t *testing.T) {
	quietLog(t)
	t.Run("env 兜底", func(t *testing.T) {
		t.Setenv("HANDOFF_OPENCODE_MODEL", "env-model")
		configPath, _, err := opencode.WriteTaskEnv(t.TempDir(), "t1", "", "plan")
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Model string `json:"model"`
		}
		raw, _ := os.ReadFile(configPath)
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Model != "env-model" {
			t.Fatalf("任务 model 空应回退 env：json.model=%q", cfg.Model)
		}
	})
	t.Run("都空则不写", func(t *testing.T) {
		t.Setenv("HANDOFF_OPENCODE_MODEL", "")
		configPath, _, err := opencode.WriteTaskEnv(t.TempDir(), "t1", "", "plan")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(configPath)
		if strings.Contains(string(raw), `"model"`) {
			t.Fatalf("model 空且 env 空时不应写 model 键（omitempty）：%s", raw)
		}
	})
}

// TestWriteTaskEnv 验证：
//   - 生成路径为 taskDir 下的 opencode.json 与 prompt.md
//   - 配置文件是合法 JSON 且权限为静态分级：edit 放行、bash 模式表
//     （危险模式 ask、其余 allow）、webfetch/external_directory 仍 ask
//   - prompt.md 含任务 ID、计划原文与提问纪律 JSON 样例
//   - 重复调用幂等覆盖：第二次调用以新内容覆盖旧文件，不报错
func TestWriteTaskEnv(t *testing.T) {
	quietLog(t)
	taskDir := t.TempDir()
	const taskID = "T-2026-0001"
	plan := "1. 实现 foo\n2. 修复 bar\n{\"ask\":\"要第三方库吗?\"}"

	configPath, promptPath, err := opencode.WriteTaskEnv(taskDir, taskID, "", plan)
	if err != nil {
		t.Fatalf("WriteTaskEnv: %v", err)
	}
	if configPath != filepath.Join(taskDir, "opencode.json") {
		t.Errorf("config 路径 %q，期望 %q", configPath, filepath.Join(taskDir, "opencode.json"))
	}
	if promptPath != filepath.Join(taskDir, "prompt.md") {
		t.Errorf("prompt 路径 %q，期望 %q", promptPath, filepath.Join(taskDir, "prompt.md"))
	}

	cfgRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取 opencode.json: %v", err)
	}
	var cfg struct {
		Permission struct {
			Edit              string            `json:"edit"`
			Bash              map[string]string `json:"bash"`
			Webfetch          string            `json:"webfetch"`
			ExternalDirectory string            `json:"external_directory"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatalf("opencode.json 不是合法 JSON: %v\n%s", err, cfgRaw)
	}
	// 静态分级底线：改代码/常规命令放行，危险模式与外访仍上审批门
	if cfg.Permission.Edit != "allow" {
		t.Errorf("permission.edit = %q，期望 allow（任务分支内改代码是派发目的本身）", cfg.Permission.Edit)
	}
	if cfg.Permission.Webfetch != "ask" {
		t.Errorf("permission.webfetch = %q，期望 ask", cfg.Permission.Webfetch)
	}
	if cfg.Permission.ExternalDirectory != "ask" {
		t.Errorf("permission.external_directory = %q，期望 ask", cfg.Permission.ExternalDirectory)
	}
	if cfg.Permission.Bash["*"] != "allow" {
		t.Errorf("permission.bash[*] = %q，期望 allow（常规命令兜底放行）", cfg.Permission.Bash["*"])
	}
	// 危险模式必须逐条在场且为 ask——少一条就是静默放行破坏性操作
	for _, pattern := range []string{
		"*rm -rf*", "*rm -fr*", "rm *", "*sudo*",
		"*git push*", "*git reset --hard*", "*--force*", "curl *", "wget *",
	} {
		if got := cfg.Permission.Bash[pattern]; got != "ask" {
			t.Errorf("permission.bash[%q] = %q，期望 ask", pattern, got)
		}
	}

	promptMD, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("读取 prompt.md: %v", err)
	}
	got := string(promptMD)
	if !strings.Contains(got, taskID) {
		t.Errorf("prompt.md 应包含任务 ID %q", taskID)
	}
	if !strings.Contains(got, plan) {
		t.Errorf("prompt.md 应包含计划原文 %q\n实际:\n%s", plan, got)
	}
	if !strings.Contains(got, `{"ask":`) {
		t.Errorf("prompt.md 应包含提问纪律 JSON 样例 {\"ask\":")
	}

	newPlan := "改版后的计划：只做一件事"
	if _, _, err := opencode.WriteTaskEnv(taskDir, taskID, "", newPlan); err != nil {
		t.Fatalf("重复调用 WriteTaskEnv: %v", err)
	}
	again, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("重复调用后读取 prompt.md: %v", err)
	}
	if !strings.Contains(string(again), newPlan) {
		t.Errorf("重复调用应以新计划覆盖 prompt.md，实际不含 %q", newPlan)
	}
}

// TestExternalDirectoryIsAsk 锁死 B27 对 opencode 的真实拦截点。
//
// opencode 的越界写入不是靠 edit 的 ask 拦的（edit 是 allow、范围内写入
// 无事件），而是靠 external_directory。这条一旦被改成 allow，写
// ~/.ssh/authorized_keys 就会连事件都不留。
func TestExternalDirectoryIsAsk(t *testing.T) {
	quietLog(t)
	configPath, _, err := opencode.WriteTaskEnv(t.TempDir(), "t1", "", "plan")
	if err != nil {
		t.Fatalf("WriteTaskEnv: %v", err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取 opencode.json: %v", err)
	}
	var cfg struct {
		Permission struct {
			ExternalDirectory string `json:"external_directory"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("opencode.json 解析失败: %v", err)
	}
	if got := cfg.Permission.ExternalDirectory; got != "ask" {
		t.Fatalf("external_directory 必须是 ask，实得 %q——这是 opencode 侧唯一的越界写入拦截点（B27）", got)
	}
}
