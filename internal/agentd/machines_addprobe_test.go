// 本文件锁死：新增开发机时空地址要被明确拒绝，而不是产出一个网络错误。
//
// why：控制台目前只能新增直连机器（AddMachineReq 没有 relay 字段），空 addr
// 一定是用户漏填。把它报成 "no Host in request URL" 等于让人去查网络。
package agentd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestAddMachineEmptyAddrRejectedClearly：空地址被校验或探测明确拒绝。
func TestAddMachineEmptyAddrRejectedClearly(t *testing.T) {
	s := newPoolWiringServer(t, &config.Config{Listen: "127.0.0.1:0",
		Targets: map[string]config.Target{}})
	defer s.CloseTargets()

	body, _ := json.Marshal(proto.AddMachineReq{Name: "ghost", Addr: "", Token: "tok"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/machines", bytes.NewReader(body))
	s.handleAddMachine(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空地址要 400，实得 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "no Host in request URL") {
		t.Fatalf("不该报网络错误：%s", rec.Body.String())
	}
}
