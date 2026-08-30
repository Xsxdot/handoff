// gc_test.go —— B298 gc client 旧 agentd 探测与请求形状测试。
//
// 职责：
//   - 锁定 POST 404 后再探测 GET 的双 404 旧版本判定
//   - 确认 --force 透过 JSON 请求体/查询参数到达 client 边界
//
// 边界：
//   - 不连接真实 agentd；真实清理与 CLI 退出码属于 implement/acceptance 节点
package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestGCPostDouble404IsUnsupported 冻结老 agentd 的双路由探测语义。
func TestGCPostDouble404IsUnsupported(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			var req proto.GCRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Force {
				t.Errorf("POST body force = %v, want true", err)
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "token").GC(context.Background(), true)
	if !errors.Is(err, ErrGCUnsupported) {
		t.Fatalf("error = %v, want ErrGCUnsupported", err)
	}
	if got, want := len(paths), 2; got != want {
		t.Fatalf("request count = %d, want %d (%v)", got, want, paths)
	}
	if paths[0] != "POST /api/gc" || paths[1] != "GET /api/gc?force=true" {
		t.Fatalf("paths = %v, want POST then forced GET probe", paths)
	}
}

func TestGCPreviewAndGCDecode200ReleasableBytes(t *testing.T) {
	zero := int64(0)
	present, err := json.Marshal(proto.GCResp{
		Preview: true, ReleasableBytes: &zero,
		CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{},
	})
	if err != nil {
		t.Fatal(err)
	}
	absent, err := json.Marshal(proto.GCResp{
		Preview: false, CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write(absent)
			return
		}
		n++
		_, _ = w.Write(present)
	}))
	t.Cleanup(ts.Close)
	cl := New(ts.URL, "tok")
	pre, err := cl.GCPreview(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pre.ReleasableBytes != nil {
		t.Fatalf("缺席 JSON 必须解成 nil，实得 %+v", pre.ReleasableBytes)
	}
	got, err := cl.GC(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReleasableBytes == nil || *got.ReleasableBytes != 0 {
		t.Fatalf("显式 0 必须可分，实得 %+v", got.ReleasableBytes)
	}
	if n != 1 {
		t.Fatalf("POST 次数=%d", n)
	}
}
