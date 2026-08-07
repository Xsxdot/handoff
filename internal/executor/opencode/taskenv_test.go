// taskenv 测试：验证任务环境物料（opencode.json / prompt.md）的生成契约与
// 回合协议 trailer 提取语义，覆盖幂等覆盖与损坏 JSON 容错。
package opencode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/opencode"
)

// TestWriteTaskEnv 验证：
//   - 生成路径为 taskDir 下的 opencode.json 与 prompt.md
//   - 配置文件是合法 JSON 且四类 permission 均为 "ask"
//   - prompt.md 含任务 ID、计划原文与提问纪律 JSON 样例
//   - 重复调用幂等覆盖：第二次调用以新内容覆盖旧文件，不报错
func TestWriteTaskEnv(t *testing.T) {
	quietLog(t)
	taskDir := t.TempDir()
	const taskID = "T-2026-0001"
	plan := "1. 实现 foo\n2. 修复 bar\n{\"ask\":\"要第三方库吗?\"}"

	configPath, promptPath, err := opencode.WriteTaskEnv(taskDir, taskID, plan)
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
			Edit              string `json:"edit"`
			Bash              string `json:"bash"`
			Webfetch          string `json:"webfetch"`
			ExternalDirectory string `json:"external_directory"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatalf("opencode.json 不是合法 JSON: %v\n%s", err, cfgRaw)
	}
	for name, got := range map[string]string{
		"edit": cfg.Permission.Edit, "bash": cfg.Permission.Bash,
		"webfetch": cfg.Permission.Webfetch, "external_directory": cfg.Permission.ExternalDirectory,
	} {
		if got != "ask" {
			t.Errorf("permission.%s = %q，期望 ask", name, got)
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
	if _, _, err := opencode.WriteTaskEnv(taskDir, taskID, newPlan); err != nil {
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

// TestParseTrailer 表驱动验证 trailer 提取语义：
//   - 末行 {"ask":...} → kind=ask 且 Question 正确
//   - 末行 finish JSON → kind=finish 且 Branch/Commit/Summary 全部正确
//   - JSON 出现在正文中间而末行是普通文本 → 仍取最后一个 { 开头行
//   - 全文无 JSON → none
//   - JSON 损坏 → none 且不 panic
func TestParseTrailer(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		wantKind string
		want     opencode.Trailer
	}{
		{
			name:     "末行 ask",
			text:     "我先看一下代码。\n{\"ask\":\"用哪个库?\"}",
			wantKind: "ask",
			want:     opencode.Trailer{Question: "用哪个库?"},
		},
		{
			name:     "末行 finish",
			text:     "完成。\n{\"branch\":\"feat/handoff-mvp\",\"commit\":\"abc123\",\"summary\":\"实现任务配置与协议解析\"}",
			wantKind: "finish",
			want: opencode.Trailer{
				Branch:  "feat/handoff-mvp",
				Commit:  "abc123",
				Summary: "实现任务配置与协议解析",
			},
		},
		{
			name:     "JSON 在中间末行是普通文本",
			text:     "先说明一下\n{\"ask\":\"用哪个库?\"}\n这是普通文本结尾",
			wantKind: "ask",
			want:     opencode.Trailer{Question: "用哪个库?"},
		},
		{
			name:     "全文无 JSON",
			text:     "好的，我先看看。\n然后开始。",
			wantKind: "none",
			want:     opencode.Trailer{},
		},
		{
			name:     "JSON 损坏不 panic",
			text:     "输出如下：\n{\"ask\": \"未闭合",
			wantKind: "none",
			want:     opencode.Trailer{},
		},
		{
			name:     "合法 JSON 但无协议字段",
			text:     "{\"foo\":1}",
			wantKind: "none",
			want:     opencode.Trailer{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, got := opencode.ParseTrailer(tc.text)
			if kind != tc.wantKind {
				t.Errorf("kind = %q，期望 %q", kind, tc.wantKind)
			}
			if got != tc.want {
				t.Errorf("Trailer = %+v，期望 %+v", got, tc.want)
			}
		})
	}
}
