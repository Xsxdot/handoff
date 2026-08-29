// gc_test.go —— handoff gc CLI 的旧 agentd 降级行为测试。
//
// 职责：
//   - 锁定预览模式收到 404 时输出可行动的版本过旧提示
//   - 证明 CLI 将 gc 预览请求交给 client，而不在本地猜测清理结果
//
// 边界：
//   - 不验证 agentd 的实际清理逻辑，那属于 internal/agentd 的测试范围
//   - 不复用 rootCmd 执行，避免测试全局 flag 与配置解析污染本用例
package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// TestRunGCDegradesOnOldAgentd 锁定 gc 预览遇到双端点旧版本判定后的成功提示。
func TestRunGCDegradesOnOldAgentd(t *testing.T) {
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	oldForce, oldYes, oldJSON := gcForce, gcYes, gcJSON
	gcForce, gcYes, gcJSON = true, false, false
	t.Cleanup(func() { gcForce, gcYes, gcJSON = oldForce, oldYes, oldJSON })

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	if err := runGC(cmd, client.New(ts.URL, "test-token"), ts.URL); err != nil {
		t.Fatalf("旧 agentd 预览应成功降级，得到错误: %v", err)
	}
	if !strings.Contains(out.String(), "过旧") {
		t.Fatalf("旧 agentd 提示应包含过旧，实得：%s", out.String())
	}
	if len(paths) != 1 || paths[0] != "GET /api/gc?force=true" {
		t.Fatalf("预览请求应只发 GET /api/gc?force=true，实得：%v", paths)
	}
}

// TestRenderGCDistinguishesUnknownBytes 锁定人读输出对字节量缺席与零值的区分。
func TestRenderGCDistinguishesUnknownBytes(t *testing.T) {
	zero := int64(0)
	var computed bytes.Buffer
	renderGC(&computed, &proto.GCResp{ReleasableBytes: &zero})
	if got := computed.String(); !strings.Contains(got, "将释放字节：0") {
		t.Fatalf("已计算为零的报告应显示 0，实得：%s", got)
	}

	var unknown bytes.Buffer
	renderGC(&unknown, &proto.GCResp{})
	if got := unknown.String(); !strings.Contains(got, "将释放字节：未计算") {
		t.Fatalf("未计算字节的报告应显示未计算，实得：%s", got)
	}
}
