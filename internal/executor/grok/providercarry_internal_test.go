// providercarry_internal_test.go —— 段抽取纯逻辑的表驱动断言。
//
// 用内部测试包（package grok）：被测函数未导出。这些用例全是字符串进、
// 结构出，不碰文件系统、不起进程。
package grok

import (
	"strings"
	"testing"
)

func TestExtractProviderConfig(t *testing.T) {
	cases := []struct {
		name         string
		content      string
		wantNames    []string
		wantDefault  string
		wantContains []string // ModelSections 必须含有的片段
		wantAbsent   []string // ModelSections 必须不含的片段
	}{
		{
			name: "两个 provider 段与 default 全部抽出",
			content: `[models]
default = "deepseek-v4-pro"

[model.deepseek-v4-pro]
model = "deepseek-v4-pro"
base_url = "https://example.invalid/v1"
api_key = "sk-SENTINEL-PRO"

[model.deepseek-v4-flash]
model = "deepseek-v4-flash"
api_key = "sk-SENTINEL-FLASH"
`,
			wantNames:   []string{"model.deepseek-v4-pro", "model.deepseek-v4-flash"},
			wantDefault: "deepseek-v4-pro",
			wantContains: []string{
				"[model.deepseek-v4-pro]", "sk-SENTINEL-PRO",
				"[model.deepseek-v4-flash]", "sk-SENTINEL-FLASH",
			},
			wantAbsent: []string{"[models]"},
		},
		{
			name: "provider 段后跟别的段：切在边界不吞下一段",
			content: `[model.x]
model = "x"

[marketplace]
enabled = true

[ui]
permission_mode = "always-approve"
`,
			wantNames:    []string{"model.x"},
			wantDefault:  "",
			wantContains: []string{"[model.x]", `model = "x"`},
			wantAbsent:   []string{"[marketplace]", "enabled = true", "always-approve"},
		},
		{
			name: "段内注释与缩进原样保留",
			content: `[model.y]
  # 这条注释必须活下来
  model = "y"
`,
			wantNames:    []string{"model.y"},
			wantContains: []string{"  # 这条注释必须活下来", `  model = "y"`},
		},
		{
			name: "数组表 [[x]] 终结 provider 段，不被误收",
			content: `[model.z]
model = "z"

[[servers]]
url = "https://example.invalid"
`,
			wantNames:    []string{"model.z"},
			wantContains: []string{"[model.z]"},
			wantAbsent:   []string{"[[servers]]", "url ="},
		},
		{
			name: "default 带行内注释",
			content: `[models]
default = "abc"  # 平时用这个
`,
			wantDefault: "abc",
		},
		{
			name: "default_reasoning_effort 不得被误当成 default",
			content: `[models]
default_reasoning_effort = "high"
`,
			wantDefault: "",
		},
		{
			name: "无 provider 段无 default：返回零值",
			content: `[ui]
permission_mode = "always-approve"
`,
			wantDefault: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractProviderConfig(c.content)

			if got.DefaultModel != c.wantDefault {
				t.Errorf("DefaultModel = %q，期望 %q", got.DefaultModel, c.wantDefault)
			}
			if len(got.SectionNames) != len(c.wantNames) {
				t.Fatalf("SectionNames = %v，期望 %v", got.SectionNames, c.wantNames)
			}
			for i, want := range c.wantNames {
				if got.SectionNames[i] != want {
					t.Errorf("SectionNames[%d] = %q，期望 %q", i, got.SectionNames[i], want)
				}
			}
			for _, want := range c.wantContains {
				if !strings.Contains(got.ModelSections, want) {
					t.Errorf("ModelSections 缺 %q，实际:\n%s", want, got.ModelSections)
				}
			}
			for _, bad := range c.wantAbsent {
				if strings.Contains(got.ModelSections, bad) {
					t.Errorf("ModelSections 不该含 %q，实际:\n%s", bad, got.ModelSections)
				}
			}
		})
	}
}

// TestExtractModelsExtraCarriesAuxiliaryKnobs 钉住 B138 的搬运面：[models] 段里
// default 之外的键（web_search / session_summary / image_description）必须原样
// 搬走。它们各自决定一条辅助链路用哪个模型，**没写就回落内建的 grok-4.6**——
// 在自定义 provider 的机器上，那个模型名对端不认，请求带着当前模型的凭据打过去
// 会被 400 顶回（2026-08-18 win-b37 真机实证）。
func TestExtractModelsExtraCarriesAuxiliaryKnobs(t *testing.T) {
	cases := []struct {
		name         string
		content      string
		wantContains []string
		wantAbsent   []string
		wantKeys     []string
	}{
		{
			name: "default 之外的键全部搬走，default 与段头不搬",
			content: `[models]
default = "deepseek-v4-pro"
web_search = "deepseek-v4-flash"
session_summary = "deepseek-v4-flash"
`,
			wantContains: []string{
				`web_search = "deepseek-v4-flash"`,
				`session_summary = "deepseek-v4-flash"`,
			},
			wantAbsent: []string{"[models]", "default ="},
			wantKeys:   []string{"web_search", "session_summary"},
		},
		{
			name: "段边界：不吞下一段",
			content: `[models]
web_search = "flash"

[ui]
permission_mode = "always-approve"
`,
			wantContains: []string{`web_search = "flash"`},
			wantAbsent:   []string{"[ui]", "always-approve"},
			wantKeys:     []string{"web_search"},
		},
		{
			name: "注释原样保留且不算键",
			content: `[models]
# 标题生成走便宜的那个
session_summary = "flash"
`,
			wantContains: []string{"# 标题生成走便宜的那个", `session_summary = "flash"`},
			wantKeys:     []string{"session_summary"},
		},
		{
			name: "default_reasoning_effort 归 extra，不被当成 default 摘走",
			content: `[models]
default_reasoning_effort = "high"
`,
			wantContains: []string{`default_reasoning_effort = "high"`},
			wantKeys:     []string{"default_reasoning_effort"},
		},
		{
			name: "只有 default 时 extra 为空",
			content: `[models]
default = "x"
`,
			wantAbsent: []string{"default", "="},
			wantKeys:   nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractProviderConfig(c.content)
			for _, want := range c.wantContains {
				if !strings.Contains(got.ModelsExtra, want) {
					t.Errorf("ModelsExtra 缺 %q，实际:\n%s", want, got.ModelsExtra)
				}
			}
			for _, bad := range c.wantAbsent {
				if strings.Contains(got.ModelsExtra, bad) {
					t.Errorf("ModelsExtra 不该含 %q，实际:\n%s", bad, got.ModelsExtra)
				}
			}
			if len(got.ModelsExtraKeys) != len(c.wantKeys) {
				t.Fatalf("ModelsExtraKeys = %v，期望 %v", got.ModelsExtraKeys, c.wantKeys)
			}
			for i, want := range c.wantKeys {
				if got.ModelsExtraKeys[i] != want {
					t.Errorf("ModelsExtraKeys[%d] = %q，期望 %q", i, got.ModelsExtraKeys[i], want)
				}
			}
			// 归一化契约：非空时必须以单个 \n 收尾，拼进 [models] 段才不会走样
			if got.ModelsExtra != "" && !strings.HasSuffix(got.ModelsExtra, "\n") {
				t.Errorf("ModelsExtra 必须以 \\n 收尾，实际: %q", got.ModelsExtra)
			}
			if strings.HasSuffix(got.ModelsExtra, "\n\n") {
				t.Errorf("ModelsExtra 不该有尾随空行，实际: %q", got.ModelsExtra)
			}
		})
	}
}
