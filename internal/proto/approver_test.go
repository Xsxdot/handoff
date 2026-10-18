// approver_test.go —— B437 代他机裁决 wire 的金样本断言（冻结 F1/F2/F4）。
package proto

import (
	"encoding/json"
	"sort"
	"testing"
)

func approverKeysOf(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func assertKeys(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("键集不符：want %v got %v", want, got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("键集不符：want %v got %v", want, got)
		}
	}
}

// TestApproverDecideReqKeys 钉住 F1：请求键集固定，且**不存在 prompt 类字段**。
func TestApproverDecideReqKeys(t *testing.T) {
	req := ApproverDecideReq{Permission: "Bash: ls", Executor: "codex", TaskSummary: "s", Model: "m"}
	got := approverKeysOf(t, req)
	assertKeys(t, got, []string{"executor", "model", "permission", "task_summary"})
	for _, k := range got {
		if k == "prompt" {
			t.Fatal("请求体不得含 prompt 字段（冻结 F1）")
		}
	}
	// 可空字段省略后不出键
	minimal := approverKeysOf(t, ApproverDecideReq{Permission: "x", Executor: "codex"})
	assertKeys(t, minimal, []string{"executor", "permission"})
}

// TestApproverDecideRespKeys 钉住 F2：error_kind 仅 decision=error 时出现。
func TestApproverDecideRespKeys(t *testing.T) {
	ok := approverKeysOf(t, ApproverDecideResp{Decision: "approve", Reason: "r", Model: "默认", ElapsedMS: 5})
	assertKeys(t, ok, []string{"decision", "elapsed_ms", "model", "reason"})
	errResp := approverKeysOf(t, ApproverDecideResp{Decision: "error", Reason: "r", Model: "默认", ElapsedMS: 5, ErrorKind: "no_executor"})
	assertKeys(t, errResp, []string{"decision", "elapsed_ms", "error_kind", "model", "reason"})
}

// TestApproverDecideOldVersionDecodes 钉住 F4：宽松 JSON（未知键忽略）+ 缺字段可解。
func TestApproverDecideOldVersionDecodes(t *testing.T) {
	var req ApproverDecideReq
	if err := json.Unmarshal([]byte(`{"permission":"x","executor":"codex","future_key":1}`), &req); err != nil {
		t.Fatalf("未知键应被忽略: %v", err)
	}
	if req.Permission != "x" || req.Executor != "codex" || req.TaskSummary != "" || req.Model != "" {
		t.Fatalf("旧版（缺字段）应解出空值: %+v", req)
	}
	var resp ApproverDecideResp
	if err := json.Unmarshal([]byte(`{"decision":"error","reason":"r","model":"默认","elapsed_ms":3,"error_kind":"weird"}`), &resp); err != nil {
		t.Fatalf("未知 error_kind 应可解: %v", err)
	}
	if resp.ErrorKind != "weird" {
		t.Fatalf("解码应原样保留 error_kind（归一在发起方）: %+v", resp)
	}
}
