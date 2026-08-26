package cmd

// card coordinate 命令级回归（B156.3 K3 Task E）。launch 端点本体归 K4：
// 这里用 stub 锁命令侧契约——请求形状（POST 路径+空对象体）、成功渲染、
// 参数校验、以及服务端 400 指路文案的可行动透传（岔口四方案 B 的用户可见半边）。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TE-1：请求形状 + 成功渲染。请求体必须是空 JSON 对象（K4 的 handler 据此知道
// 没有覆盖参数；形状变更属跨卡事件，见 plan §7 待拍板 3）。
func TestCardCoordinatePostsEmptyObjectToLaunchPath(t *testing.T) {
	dir := t.TempDir()
	var gotPath, gotMethod, gotBody string
	stubSquadAgentd(t, dir, http.StatusOK,
		`{"woke":true,"session_id":"sess-01HX","rebuilt":false,"escalated":false,"output":"开场评估完成"}`,
		func(r *http.Request, body string) {
			gotPath, gotMethod, gotBody = r.URL.Path, r.Method, body
		})
	out, _, err := runLedgerCLI(t, dir, "card", "coordinate", "B42")
	if err != nil {
		t.Fatalf("coordinate 失败: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/cards/B42/coordinator/launch" {
		t.Fatalf("请求形状不符: %s %s", gotMethod, gotPath)
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(gotBody), &in); err != nil || len(in) != 0 {
		t.Fatalf("请求体应是空 JSON 对象: %q err=%v", gotBody, err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("stdout 应是 JSON: %s", out)
	}
	if resp["woke"] != true || resp["session_id"] != "sess-01HX" {
		t.Fatalf("渲染不符: %s", out)
	}
}

// TE-2：缺卡号被 Args 拒绝（不发请求——正控同 TE-1）。
func TestCardCoordinateRequiresCardArg(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "card", "coordinate"); err == nil {
		t.Fatal("缺卡号应被 Args 拒绝")
	}
}

// TE-3：岔口四的用户可见半边——未登记协调者小队时服务端 400 报文含指路文案，
// CLI 必须把它原样送到操作者眼前（含 "squad create" 具体命令字样）。
func TestCardCoordinateSurfacesSquadCreatePointer(t *testing.T) {
	dir := t.TempDir()
	stubSquadAgentd(t, dir, http.StatusBadRequest,
		`{"error":"未登记协调者小队：先 handoff squad create --name coord --role coordinator 登记后再拉起"}`,
		nil)
	_, errOut, err := runLedgerCLI(t, dir, "card", "coordinate", "B42")
	if err == nil || !strings.Contains(err.Error()+errOut, "squad create") {
		t.Fatalf("400 指路文案必须透传到操作者，得 %v stderr=%s", err, errOut)
	}
}
