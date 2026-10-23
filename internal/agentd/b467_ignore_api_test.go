package agentd

// b467_ignore_api_test.go —— B467 段 A 账号 HTTP 面的竖切：忽略/恢复子资源（A16–A19）、
// GET 投影 ignored 键的在场与缺席（A13/A15）、立即探测 409（A20）、载体保存 400（A21）。
//
// 边界：真实 Server/Handler/鉴权/JSON 与共享账本；额度接口不参与（忽略先于探测被挡）。
// 断言原始 JSON map 而不是 struct：struct 解码会把「键缺席」与零值混为一谈。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// accountJSONByID 在原始 GET /api/accounts 响应里按 id 找一行（map 形态，保留键是否在场）。
func accountJSONByID(t *testing.T, body, id string) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("解码 GET /api/accounts: %v (%s)", err, body)
	}
	rows, _ := raw["accounts"].([]any)
	for _, row := range rows {
		m, _ := row.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("响应里没有账号 %s: %s", id, body)
	return nil
}

func TestAccountIgnoreRestoreAPI(t *testing.T) {
	st, err := ledger.Open(t.TempDir() + "/shared.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	env := newAccountMachine(t, "a", st, map[string]config.Target{"a": {}}, "")
	svc := env.srv.accounts.(*accounts.Service)
	env.svc.SetAccounts(svc)
	svc.SetReferrers(env.svc.LoginReferrers)
	if _, err := svc.Reconcile(accounts.Discovery{Machine: "a", At: time.Now(), Homes: []accounts.ScannedHome{
		{Home: "~", HomeAbs: "/home/u", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "auth.json", Fingerprint: "sha256:467467467467"}}},
		{Home: "~/.handoff/home/b", HomeAbs: "/home/u/.handoff/home/b", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "auth.json", Fingerprint: "sha256:746746746746"}}},
	}}); err != nil {
		t.Fatalf("播种账号: %v", err)
	}
	idA := accounts.AccountID(accounts.KindOpencodeGo, "", "sha256:467467467467")
	idB := accounts.AccountID(accounts.KindOpencodeGo, "", "sha256:746746746746")
	vA, err := svc.Get(idA)
	if err != nil || len(vA.Logins) != 1 {
		t.Fatalf("读 A: %+v %v", vA, err)
	}
	vB, err := svc.Get(idB)
	if err != nil || len(vB.Logins) != 1 {
		t.Fatalf("读 B: %+v %v", vB, err)
	}

	// PUT 忽略 → 200，原始 JSON 的 ignored.at/by 在场。
	code, body := schedReq(t, env, http.MethodPut, "/api/accounts/"+idA+"/ignore", "")
	if code != http.StatusOK {
		t.Fatalf("忽略: %d %s", code, body)
	}
	var view proto.AccountView
	if err := json.Unmarshal([]byte(body), &view); err != nil || view.Ignored == nil ||
		view.Ignored.By != "a" || view.Ignored.At.IsZero() {
		t.Fatalf("忽略回执应带 ignored{at,by}: %s (%v)", body, err)
	}
	at := view.Ignored.At

	code, body = schedReq(t, env, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK {
		t.Fatalf("GET: %d %s", code, body)
	}
	rowA := accountJSONByID(t, body, idA)
	ig, ok := rowA["ignored"].(map[string]any)
	if !ok || ig["by"] != "a" || ig["at"] == "" {
		t.Fatalf("已忽略账号的原始 JSON 应带 ignored{at,by}: %v", rowA)
	}
	if _, present := accountJSONByID(t, body, idB)["ignored"]; present {
		t.Fatalf("未忽略账号的原始 JSON 不得有 ignored 键（缺席 ≠ null/{}）")
	}

	// 再次忽略 → 200 且 at/by 不变（幂等）。
	code, body = schedReq(t, env, http.MethodPut, "/api/accounts/"+idA+"/ignore", "")
	if code != http.StatusOK {
		t.Fatalf("重复忽略: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil || view.Ignored == nil ||
		!view.Ignored.At.Equal(at) || view.Ignored.By != "a" {
		t.Fatalf("重复忽略不得改 at/by: %s", body)
	}

	// 已忽略 → 立即探测 409 且含「已忽略」。
	code, body = schedReq(t, env, http.MethodPost, "/api/accounts/"+idA+"/probe", "")
	if code != http.StatusConflict || !strings.Contains(body, "已忽略") {
		t.Fatalf("已忽略账号立即探测应 409 含「已忽略」: %d %s", code, body)
	}

	// 载体保存选已忽略账号的登录 → 400，文案点名账号与恢复入口。
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"a","cli":"opencode","login":"`+vA.Logins[0].Login.ID+`"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "账号 "+vA.Account.Name+" 已忽略，先在账号页恢复") {
		t.Fatalf("选已忽略账号登录应 400: %d %s", code, body)
	}

	// 被载体引用 → 409 同形拒绝体、error 含「不能忽略」、不产生忽略行。
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c2?expect=0",
		`{"machine":"a","cli":"opencode","login":"`+vB.Logins[0].Login.ID+`"}`)
	if code != http.StatusOK {
		t.Fatalf("建引用载体: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPut, "/api/accounts/"+idB+"/ignore", "")
	var refusal proto.AccountDeleteRefusal
	if code != http.StatusConflict || json.Unmarshal([]byte(body), &refusal) != nil ||
		!strings.Contains(refusal.Error, "不能忽略") || len(refusal.Carriers) != 1 || refusal.Carriers[0] != "c2" {
		t.Fatalf("被引用忽略应 409 {error,carriers}: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodGet, "/api/accounts", "")
	if _, present := accountJSONByID(t, body, idB)["ignored"]; present {
		t.Fatalf("被拒绝时不得产生忽略标记: %s", body)
	}

	// DELETE 恢复 → 200，ignored 键缺席；再删幂等 200。
	for i := 0; i < 2; i++ {
		code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/"+idA+"/ignore", "")
		if code != http.StatusOK {
			t.Fatalf("恢复（第 %d 次）: %d %s", i+1, code, body)
		}
		if strings.Contains(body, `"ignored"`) {
			t.Fatalf("恢复回执不得带 ignored 键: %s", body)
		}
	}

	// 不存在账号 → 404（忽略与恢复同）。
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if code, body = schedReq(t, env, method, "/api/accounts/nope/ignore", ""); code != http.StatusNotFound {
			t.Fatalf("%s 未知账号应 404: %d %s", method, code, body)
		}
	}
}
