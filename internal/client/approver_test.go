// approver_test.go —— B437 Client.ApproverDecide 的线契约与 404 降级（冻结 F3）。
package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// 404 → ErrApproverUnsupported（对端过旧，是一条结论而非异常）。
func TestApproverDecideUnsupported404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := client.New(srv.URL, "tok").ApproverDecide(context.Background(),
		proto.ApproverDecideReq{Permission: "x", Executor: "codex"})
	if !errors.Is(err, client.ErrApproverUnsupported) {
		t.Fatalf("404 应归一 ErrApproverUnsupported，得到: %v", err)
	}
}

// 200 → 逐字段解码；请求路径/Bearer/体形状正确。
func TestApproverDecideSuccess(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody proto.ApproverDecideReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proto.ApproverDecideResp{
			Decision: "approve", Reason: "ok", Model: "默认", ElapsedMS: 12,
		})
	}))
	defer srv.Close()
	out, err := client.New(srv.URL, "tok").ApproverDecide(context.Background(),
		proto.ApproverDecideReq{Permission: "Bash: ls", Executor: "codex", TaskSummary: "s", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "approve" || out.ElapsedMS != 12 {
		t.Fatalf("应答解码不符: %+v", out)
	}
	if gotPath != "/api/approver/decide" || gotAuth != "Bearer tok" {
		t.Fatalf("路径/鉴权不符: %q %q", gotPath, gotAuth)
	}
	if gotBody.Permission != "Bash: ls" || gotBody.Executor != "codex" || gotBody.TaskSummary != "s" || gotBody.Model != "m" {
		t.Fatalf("请求体不符: %+v", gotBody)
	}
}

// 5xx → 普通错误（不归一为不支持，不中断链由上层处置）。
func TestApproverDecideServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	_, err := client.New(srv.URL, "tok").ApproverDecide(context.Background(),
		proto.ApproverDecideReq{Permission: "x", Executor: "codex"})
	if err == nil || errors.Is(err, client.ErrApproverUnsupported) {
		t.Fatalf("5xx 应为普通错误: %v", err)
	}
	if code, ok := client.StatusCode(err); !ok || code != http.StatusInternalServerError {
		t.Fatalf("应带 500 状态码: %v %v", code, ok)
	}
}
