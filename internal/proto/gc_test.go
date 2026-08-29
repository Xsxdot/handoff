// gc_test.go —— B298 gc wire 形状的可执行冻结。
//
// 职责：
//   - 用金样本锁定请求字段与报告的 JSON 键
//   - 证明「已计算为零」与「尚未计算」不会被序列化成同一形状
//
// 边界：
//   - 不创建文件、不调用 agentd；资源判定测试属于 implement 节点
package proto

import (
	"encoding/json"
	"testing"
)

// TestGCGoldenJSON 冻结 B298 的最小请求与预览报告 wire 形状。
func TestGCGoldenJSON(t *testing.T) {
	request, err := json.Marshal(GCRequest{Force: true})
	if err != nil {
		t.Fatalf("marshal GCRequest: %v", err)
	}
	if got, want := string(request), `{"force":true}`; got != want {
		t.Fatalf("GCRequest JSON = %s, want %s", got, want)
	}

	zero := int64(0)
	resp, err := json.Marshal(GCResp{
		Preview:         true,
		ReleasableBytes: &zero,
		CacheRows:       []GCCacheRow{},
		WorktreeRows:    []GCWorktreeRow{},
	})
	if err != nil {
		t.Fatalf("marshal GCResp: %v", err)
	}
	want := `{"preview":true,"force":false,"releasable_bytes":0,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`
	if got := string(resp); got != want {
		t.Fatalf("GCResp JSON = %s, want %s", got, want)
	}

	missing, err := json.Marshal(GCResp{CacheRows: []GCCacheRow{}, WorktreeRows: []GCWorktreeRow{}})
	if err != nil {
		t.Fatalf("marshal missing GCResp: %v", err)
	}
	if got := string(missing); got == want {
		t.Fatal("missing releasable_bytes must not serialize like an explicit zero")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(missing, &fields); err != nil {
		t.Fatalf("unmarshal missing GCResp: %v", err)
	}
	if _, ok := fields["releasable_bytes"]; ok {
		t.Fatal("missing releasable_bytes unexpectedly present")
	}
}
