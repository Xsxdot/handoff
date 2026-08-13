// 结构化权限载荷提取的白盒测试：字段名全部来自 Task 1 真机取样。
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// TestPermRequestFromToolCall 用 Task 1 真机取样的三份载荷断言结构提取。
//
// 字段名全部来自真机样本（testdata/perm_*.json）——grok 的载荷形态在本项目
// 已经猜错过两次，不再手写想象中的形状。三份样本各锁一件事：
//   - perm_write.json          Write，绝对路径
//   - perm_edit_absolute.json  Edit，绝对路径
//   - perm_edit_relative.json  Edit，**相对路径**（真机确实会给相对路径）
func TestPermRequestFromToolCall(t *testing.T) {
	cases := []struct {
		file     string
		wantTool string
		wantPath string
	}{
		{"perm_write.json", executor.PermToolWrite, "/Users/sycm/.handoff/worktrees/a2e10493/probe.md"},
		{"perm_edit_absolute.json", executor.PermToolEdit, "/Users/sycm/.handoff/worktrees/a2e10493/probe.md"},
		{"perm_edit_relative.json", executor.PermToolEdit, "probe.md"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.file))
			if err != nil {
				t.Fatalf("读真机载荷样本: %v", err)
			}
			var p struct {
				ToolCall permToolCall `json:"toolCall"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("解析真机载荷样本: %v", err)
			}
			req := permRequestFromToolCall(p.ToolCall)
			if req == nil {
				t.Fatal("真机写文件载荷必须能提取出结构")
			}
			if req.Tool != c.wantTool {
				t.Fatalf("工具名 = %q，期望 %q", req.Tool, c.wantTool)
			}
			if len(req.Paths) != 1 || req.Paths[0] != c.wantPath {
				t.Fatalf("路径 = %v，期望 [%s]（相对路径必须原样透传，解析交给 permgate）",
					req.Paths, c.wantPath)
			}
		})
	}
}

// TestPermRequestToolCallKindIsUseless 锁死一条真机教训：toolCall.kind 对
// Write 和 Edit 一律是 "edit"，不能拿它当工具名来源。这条用例存在的意义是
// 让任何一个想「简化成读 kind」的后来者立刻红。
func TestPermRequestToolCallKindIsUseless(t *testing.T) {
	for _, f := range []string{"perm_write.json", "perm_edit_absolute.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatalf("读真机载荷样本: %v", err)
		}
		var p struct {
			ToolCall struct {
				Kind string `json:"kind"`
			} `json:"toolCall"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("解析: %v", err)
		}
		if p.ToolCall.Kind != "edit" {
			t.Fatalf("%s 的 toolCall.kind = %q，真机实测两者都是 \"edit\"；"+
				"若 grok 改了形态，本用例与 permRequestFromToolCall 都要复核", f, p.ToolCall.Kind)
		}
	}
}

// TestPermRequestBashKeepsFullCommand 命令必须取完整原文，不能取 title 的
// 摘要——title 的 200 截断会把命令尾部的危险片段切掉。
func TestPermRequestBashKeepsFullCommand(t *testing.T) {
	long := "go test ./... && " + strings.Repeat("x", 300) + " && rm -rf /tmp/x"
	in, err := json.Marshal(map[string]string{"command": long})
	if err != nil {
		t.Fatalf("构造入参: %v", err)
	}
	req := permRequestFromToolCall(permToolCall{Kind: "execute", RawInput: in})
	if req == nil || req.Command != long {
		t.Fatalf("命令必须取完整原文，实得 %+v", req)
	}
}

// TestPermRequestNilWhenNoStructure 提取不出结构时返回 nil，不伪造空壳。
func TestPermRequestNilWhenNoStructure(t *testing.T) {
	if req := permRequestFromToolCall(permToolCall{RawInput: json.RawMessage(`{}`)}); req != nil {
		t.Fatalf("无可用字段时必须返回 nil，实得 %+v", req)
	}
}

// TestWriteEditNotInAllow 锁死 B27：写文件类工具不得回到 allow 表。
func TestWriteEditNotInAllow(t *testing.T) {
	for _, r := range allowRules {
		if strings.HasPrefix(r, "Write") || strings.HasPrefix(r, "Edit") {
			t.Fatalf("%s 不得出现在 allowRules——那等于写仓库外路径不经任何人（B27）", r)
		}
	}
}
