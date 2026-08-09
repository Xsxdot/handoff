package executor

import "testing"

// TestNormalizePermTool 锁死归一化映射。
//
// 只收本项目实际见过的工具名——不猜 grok/opencode 的别名，那两个的真实
// 取值由 Task 1 的真机探针给出，各自在 adapter 侧补进本表。
func TestNormalizePermTool(t *testing.T) {
	cases := map[string]string{
		"Bash":     PermToolBash,
		"bash":     PermToolBash,
		"  Bash  ": PermToolBash,
		"Write":    PermToolWrite,
		"Edit":     PermToolEdit,
		"WebFetch": PermToolWebFetch,
		"Glob":     PermToolOther,
		"":         PermToolOther,
	}
	for raw, want := range cases {
		if got := NormalizePermTool(raw); got != want {
			t.Errorf("NormalizePermTool(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

// TestAdapterEventPermOptional Perm 是可选字段：不填时为 nil，
// manager 据此走 fail-closed 升级。
func TestAdapterEventPermOptional(t *testing.T) {
	if (AdapterEvent{Type: "permission"}).Perm != nil {
		t.Fatal("未填写的 Perm 必须为 nil")
	}
	ev := AdapterEvent{Type: "permission",
		Perm: &PermRequest{Tool: PermToolWrite, Paths: []string{"/x"}}}
	if ev.Perm.Tool != PermToolWrite || len(ev.Perm.Paths) != 1 {
		t.Fatal("Perm 字段未按预期携带结构")
	}
}
