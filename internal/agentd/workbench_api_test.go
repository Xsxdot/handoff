// workbench_api_test.go —— 工作台状态四个端点的测试（白盒包：要直接读 store 对账）。
//
// 职责：验证工作台状态 API 的空值语义、往返、删除、长度校验与坏请求体。
// 边界：不测试前端布局编解码；payload 在 agentd 中应保持不透明字符串。
package agentd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// strptr 把字面量取址，供 *string 字段使用。
func strptr(s string) *string { return &s }

// TestWorkbenchStateEmpty 空库时三个字段都必须是「明确的空」而不是缺席。
func TestWorkbenchStateEmpty(t *testing.T) {
	env := newTestAgentdEnv(t)
	var resp proto.WorkbenchStateResp
	if code := env.getJSON(t, "/api/workbench/state", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Selected != "" || resp.Dock != "" {
		t.Fatalf("空库时 selected/dock 应为空串，得到 %+v", resp)
	}
	if resp.Bases == nil {
		t.Fatal("bases 应是空数组而不是 null——null 会让前端多一条判空分支")
	}
	if len(resp.Bases) != 0 {
		t.Fatalf("bases = %d，期望 0", len(resp.Bases))
	}
}

// TestWorkbenchBaseRoundTrip 写一行、读回来、再删掉。
func TestWorkbenchBaseRoundTrip(t *testing.T) {
	env := newTestAgentdEnv(t)

	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "/repo/a", Payload: strptr(`{"v":1}`)}, nil); code != 200 {
		t.Fatalf("PUT base code = %d, want 200", code)
	}
	var resp proto.WorkbenchStateResp
	env.getJSON(t, "/api/workbench/state", &resp)
	if len(resp.Bases) != 1 || resp.Bases[0].BaseKey != "/repo/a" || resp.Bases[0].Payload != `{"v":1}` {
		t.Fatalf("读回 = %+v", resp.Bases)
	}
	if resp.Bases[0].UpdatedAt <= 0 {
		t.Fatalf("updated_at = %d，服务端必须盖上时间戳", resp.Bases[0].UpdatedAt)
	}

	// payload 取 null = 删除该行
	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "/repo/a", Payload: nil}, nil); code != 200 {
		t.Fatalf("PUT null code = %d, want 200", code)
	}
	resp = proto.WorkbenchStateResp{}
	env.getJSON(t, "/api/workbench/state", &resp)
	if len(resp.Bases) != 0 {
		t.Fatalf("payload=null 应删除该行，实际 = %+v", resp.Bases)
	}
}

// TestWorkbenchSelectedAndDock 覆盖两个单例，含「空串 = 清空」。
func TestWorkbenchSelectedAndDock(t *testing.T) {
	env := newTestAgentdEnv(t)

	env.putJSON(t, "/api/workbench/state/selected", proto.WorkbenchSelectedReq{BaseKey: "/repo/a"}, nil)
	env.putJSON(t, "/api/workbench/state/dock", proto.WorkbenchDockReq{Payload: strptr(`{"v":1}`)}, nil)

	var resp proto.WorkbenchStateResp
	env.getJSON(t, "/api/workbench/state", &resp)
	if resp.Selected != "/repo/a" || resp.Dock != `{"v":1}` {
		t.Fatalf("resp = %+v", resp)
	}

	// selected 写空串 = 没有选中任何目录（合法状态，不是删除信号）
	env.putJSON(t, "/api/workbench/state/selected", proto.WorkbenchSelectedReq{BaseKey: ""}, nil)
	// dock 写 null = 清空现场
	env.putJSON(t, "/api/workbench/state/dock", proto.WorkbenchDockReq{Payload: nil}, nil)

	resp = proto.WorkbenchStateResp{}
	env.getJSON(t, "/api/workbench/state", &resp)
	if resp.Selected != "" || resp.Dock != "" {
		t.Fatalf("清空后 resp = %+v", resp)
	}
}

// TestWorkbenchBaseRejects 覆盖三种 400。
func TestWorkbenchBaseRejects(t *testing.T) {
	env := newTestAgentdEnv(t)

	// ① base_key 为空
	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "", Payload: strptr("{}")}, nil); code != 400 {
		t.Fatalf("空 base_key code = %d, want 400", code)
	}

	// ② payload 超长
	big := strings.Repeat("x", maxWorkbenchPayload+1)
	if code := env.putJSON(t, "/api/workbench/state/base",
		proto.WorkbenchBaseReq{BaseKey: "/repo/a", Payload: &big}, nil); code != 400 {
		t.Fatalf("超长 payload code = %d, want 400", code)
	}

	// ③ dock payload 超长走同一条闸
	if code := env.putJSON(t, "/api/workbench/state/dock",
		proto.WorkbenchDockReq{Payload: &big}, nil); code != 400 {
		t.Fatalf("超长 dock code = %d, want 400", code)
	}

	// 三次拒绝之后库里必须什么都没有——400 不能有副作用
	bases, singles, err := env.st.ListWorkbench()
	if err != nil {
		t.Fatalf("ListWorkbench: %v", err)
	}
	if len(bases) != 0 || len(singles) != 0 {
		t.Fatalf("400 不该落库，bases=%v singles=%v", bases, singles)
	}
	_ = store.WorkbenchKeySelected // 引用一次，确认常量在本包可见
}

// TestWorkbenchBadJSON 坏 JSON 一律 400。
func TestWorkbenchBadJSON(t *testing.T) {
	env := newTestAgentdEnv(t)
	for _, path := range []string{
		"/api/workbench/state/base",
		"/api/workbench/state/selected",
		"/api/workbench/state/dock",
	} {
		// 传一个类型对不上的 body（base_key 应是 string，这里给数字）
		if code := env.putJSON(t, path, map[string]any{"base_key": 42, "payload": 42}, nil); code != 400 {
			t.Fatalf("%s 坏 JSON code = %d, want 400", path, code)
		}
	}
}
