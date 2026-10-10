// pushapi_test.go —— 设备登记 REST（spec §契约面 1）的缝级测试。
//
// 职责：从真实 HTTP（httptest + Bearer 主令牌）锁 POST/DELETE /api/push/devices
// 的登记、幂等覆盖、入参拒收、删除与 404、console_user fail-closed 403。
// 边界：不测 fanout/投递（那是 pushfanout_test 的缝）；不校验 APNs token 真伪
// （登记只回「已登记」，绝不回「已送达」）。
package agentd

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// newPushEnv 组装设备登记测试环境：真 SQLite + 主令牌，console_user 配好
// （成员服务端注入依赖它，决策 D3）。
func newPushEnv(t *testing.T) *testAgentdEnv {
	t.Helper()
	env := newTestAgentdEnv(t)
	next := *env.srv.conf()
	next.ConsoleUser = "sy"
	env.srv.cfg.Store(&next)
	return env
}

func mustListPushDevices(t *testing.T, env *testAgentdEnv, member string) []proto.PushDevice {
	t.Helper()
	got, err := env.st.ListPushDevices(member)
	if err != nil {
		t.Fatalf("ListPushDevices(%s): %v", member, err)
	}
	return got
}

// TestPushDeviceRegisterAndList 锁缝 S1：POST 登记后按服务端注入的 member 落库。
func TestPushDeviceRegisterAndList(t *testing.T) {
	env := newPushEnv(t)

	code, body := ledgerPost(t, env, "/api/push/devices",
		`{"device_id":"d1","platform":"ios","apns_token":"aabb"}`)
	if code != 200 {
		t.Fatalf("POST /api/push/devices: %d（%s）", code, body)
	}
	var resp map[string]bool
	if err := json.Unmarshal([]byte(body), &resp); err != nil || !resp["ok"] {
		t.Fatalf("响应应为 {\"ok\":true}，实为 %s（err=%v）", body, err)
	}

	got := mustListPushDevices(t, env, "user:sy")
	if len(got) != 1 {
		t.Fatalf("落库设备数 = %d，期望 1", len(got))
	}
	d := got[0]
	if d.Member != "user:sy" || d.DeviceID != "d1" || d.Platform != proto.PushPlatformIOS || d.APNSToken != "aabb" {
		t.Fatalf("登记字段错: %+v", d)
	}
	if d.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt 不应为零值: %+v", d)
	}
}

// TestPushDeviceRegisterIsIdempotent 锁同 device_id 覆盖（upsert 语义）。
func TestPushDeviceRegisterIsIdempotent(t *testing.T) {
	env := newPushEnv(t)

	if code, body := ledgerPost(t, env, "/api/push/devices",
		`{"device_id":"d1","platform":"ios","apns_token":"old"}`); code != 200 {
		t.Fatalf("首次登记: %d（%s）", code, body)
	}
	if code, body := ledgerPost(t, env, "/api/push/devices",
		`{"device_id":"d1","platform":"ios","apns_token":"new"}`); code != 200 {
		t.Fatalf("重复登记: %d（%s）", code, body)
	}

	got := mustListPushDevices(t, env, "user:sy")
	if len(got) != 1 {
		t.Fatalf("重复登记后设备数 = %d，期望 1", len(got))
	}
	if got[0].APNSToken != "new" {
		t.Fatalf("token = %q，期望被覆盖为 new", got[0].APNSToken)
	}
}

// TestPushDeviceRegisterRejectsBadInput 锁 platform 白名单（仅 ios）与必填项。
func TestPushDeviceRegisterRejectsBadInput(t *testing.T) {
	env := newPushEnv(t)
	for name, body := range map[string]string{
		"platform 非 ios": `{"device_id":"d1","platform":"android","apns_token":"aabb"}`,
		"空 token":        `{"device_id":"d1","platform":"ios","apns_token":""}`,
		"空 device_id":    `{"device_id":"","platform":"ios","apns_token":"aabb"}`,
		"坏 json":         `{`,
	} {
		code, resp := ledgerPost(t, env, "/api/push/devices", body)
		if code != 400 {
			t.Fatalf("%s: POST → %d（%s），期望 400", name, code, resp)
		}
	}
	if got := mustListPushDevices(t, env, "user:sy"); len(got) != 0 {
		t.Fatalf("拒收的请求不得落库，实有 %d 条: %+v", len(got), got)
	}
}

// TestPushDeviceDelete 锁缝 S2：删除后列表为空，再删 404。
func TestPushDeviceDelete(t *testing.T) {
	env := newPushEnv(t)
	if code, body := ledgerPost(t, env, "/api/push/devices",
		`{"device_id":"d1","platform":"ios","apns_token":"aabb"}`); code != 200 {
		t.Fatalf("登记: %d（%s）", code, body)
	}

	code, body := ledgerDelete(t, env, "/api/push/devices", `{"device_id":"d1"}`)
	if code != 200 {
		t.Fatalf("DELETE /api/push/devices: %d（%s）", code, body)
	}
	if got := mustListPushDevices(t, env, "user:sy"); len(got) != 0 {
		t.Fatalf("删除后设备数 = %d，期望 0", len(got))
	}

	code, body = ledgerDelete(t, env, "/api/push/devices", `{"device_id":"d1"}`)
	if code != 404 {
		t.Fatalf("重复 DELETE: %d（%s），期望 404", code, body)
	}
}

// TestPushDeviceRegisterRequiresConsoleUser 锁 fail-closed：未配 console_user
// 时成员无从注入，登记必须 403 而不是猜一个身份。
func TestPushDeviceRegisterRequiresConsoleUser(t *testing.T) {
	env := newTestAgentdEnv(t)
	next := *env.srv.conf()
	next.ConsoleUser = ""
	env.srv.cfg.Store(&next)

	code, body := ledgerPost(t, env, "/api/push/devices",
		`{"device_id":"d1","platform":"ios","apns_token":"aabb"}`)
	if code != 403 {
		t.Fatalf("未配 console_user: POST → %d（%s），期望 403", code, body)
	}
	if got := mustListPushDevices(t, env, "user:sy"); len(got) != 0 {
		t.Fatalf("403 不得落库，实有 %d 条", len(got))
	}
}
