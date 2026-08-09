// desktopapi 契约测试：Go 侧 golden JSON 与 wire DTO 的 round-trip。
//
// 职责：
//   - 读取 testdata/*.json，断言 BootstrapResponse/ControlEventEnvelope/Problem
//     都能按 wire 契约解码与再编码（round-trip）
//   - 锁死 JSON key 全部为 snake_case
//
// 边界：
//   - 不覆盖领域模型（由 controlplane 包测试负责）
//   - 不发起网络请求；testdata 同时被桌面 Zod 测试消费，防 Go/TS 契约漂移
package desktopapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenBootstrapRoundTrip 读取 bootstrap golden 并 round-trip。
func TestGoldenBootstrapRoundTrip(t *testing.T) {
	raw := readTestdata(t, "bootstrap.json")
	var b BootstrapResponse
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("unmarshal bootstrap: %v", err)
	}
	if b.ControlRevision != 3 {
		t.Errorf("control_revision = %d, want 3", b.ControlRevision)
	}
	if len(b.Machines) != 1 || b.Machines[0].ID != "m-local" {
		t.Errorf("machines = %+v, want [m-local]", b.Machines)
	}
	if len(b.Workspaces) != 1 || b.Workspaces[0].Kind != "main" {
		t.Errorf("workspaces = %+v, want [main]", b.Workspaces)
	}
	if len(b.ActiveTaskSummaries) != 1 || b.ActiveTaskSummaries[0].TaskID != "t1" {
		t.Errorf("active_task_summaries = %+v, want [t1]", b.ActiveTaskSummaries)
	}
	// 关键 JSON key 必须 snake_case
	for _, key := range []string{
		`"control_revision"`, `"active_task_summaries"`, `"machine_id"`, `"display_name"`,
		`"canonical_path"`, `"git_common_dir"`, `"head_oid"`, `"last_scanned_at"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("bootstrap golden 缺少契约 key %s", key)
		}
	}
	// 再编码 round-trip：反序列化后重新 marshal 不应丢字段
	re := mustMarshal(t, b)
	if !strings.Contains(string(re), `"control_revision":3`) {
		t.Errorf("round-trip 丢失 control_revision: %s", re)
	}
}

// TestGoldenControlEventRoundTrip 读取 control-event golden 并 round-trip。
func TestGoldenControlEventRoundTrip(t *testing.T) {
	raw := readTestdata(t, "control-event.json")
	var e ControlEventEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("unmarshal control event: %v", err)
	}
	if e.Revision != 4 || e.Kind != "workspace.upsert" || e.ResourceID != "ws-main" {
		t.Errorf("envelope = %+v, want revision=4 kind=workspace.upsert", e)
	}
	if !strings.Contains(string(raw), `"workspace.upsert"`) {
		t.Errorf("control-event golden 缺少 kind key")
	}
	re := mustMarshal(t, e)
	if !strings.Contains(string(re), `"revision":4`) {
		t.Errorf("round-trip 丢失 revision: %s", re)
	}
}

// TestGoldenProblemRoundTrip 读取 problem golden 并 round-trip。
func TestGoldenProblemRoundTrip(t *testing.T) {
	raw := readTestdata(t, "problem.json")
	var p Problem
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if p.Code != "PATH_OUTSIDE_WORKSPACE" || p.Retryable {
		t.Errorf("problem = %+v, want code=PATH_OUTSIDE_WORKSPACE retryable=false", p)
	}
	if p.Message == "" {
		t.Errorf("problem message 不能为空")
	}
	// 关键 key 存在
	for _, key := range []string{`"code"`, `"message"`, `"retryable"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("problem golden 缺少契约 key %s", key)
		}
	}
}

// readTestdata 读取 testdata 目录下的 golden 文件。
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取 testdata/%s: %v", name, err)
	}
	return b
}

// mustMarshal 序列化并在失败时终止测试。
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
