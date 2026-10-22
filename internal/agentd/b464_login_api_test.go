// b464_login_api_test.go —— 登录代理端点与账号管理的 HTTP 面（B464 段 2，plan A6/A8/A9、V7/V9）。
//
// 职责：锁 PUT /api/logins/{id}/proxy（服务端读-合并-CAS、不要求 expect、回打码 LoginView、
// 打码回填保留原值、空串清除、校验失败 400、登录不存在 404、缺 proxy 键 400）及其四处消费
// 中 gateway 侧三处（检测回合 hostWakeLogin、派发快照 dispatchAccount、协调者回合
// coordinatorAccountProxy；探测器一处在 accountwork）读到端点写入的值；账号改名（冲突 409、
// 非法字符 400、改名后按新名读到同一账号）；na 账号立即探测 409、写阈值 400。
// 边界：账号与登录经账号域 Reconcile 建（不起发现器）；登录 HOME 用绝对路径写法，不展开进程 HOME。
package agentd

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func TestLoginProxyEndpointMergesAndFeedsConsumers(t *testing.T) {
	home := t.TempDir() // 登录 HOME（绝对路径写法，展开不经进程 HOME）
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	self := env.srv.conf().Name
	if self == "" {
		self = "local"
	}
	login := putOpencodeLogin(t, acc, self, home)
	putOnlineCarrier(t, env.svc, scheduling.Carrier{Name: "oc", Machine: self, CLI: "opencode", MaxConcurrency: 1, Login: login})

	const full = "http://u:pw-SENTINEL@127.0.0.1:7890"
	code, body := schedReq(t, env, http.MethodPut, "/api/logins/"+login+"/proxy", `{"proxy":"`+full+`"}`)
	if code != http.StatusOK || strings.Contains(body, "pw-SENTINEL") {
		t.Fatalf("设代理应 200 且回打码值：%d %s", code, body)
	}
	var lv proto.LoginView
	if err := json.Unmarshal([]byte(body), &lv); err != nil || lv.ID != login || lv.Proxy != accounts.MaskProxy(full) ||
		len(lv.Carriers) != 1 || lv.Carriers[0] != "oc" {
		t.Fatalf("响应应是该登录的 LoginView（打码代理、引用载体）：%+v %v", lv, err)
	}
	consumers := func() map[string]string {
		return map[string]string{
			"检测回合":  env.srv.hostWakeLogin(login).Proxy,
			"派发快照":  env.srv.dispatchAccount("oc").Proxy,
			"协调者回合": env.srv.coordinatorAccountProxy("oc", "opencode", home),
		}
	}
	for name, got := range consumers() {
		if got != full {
			t.Fatalf("%s 应读到端点写入的登录代理，得 %q", name, got)
		}
	}
	if code, body := schedReq(t, env, http.MethodPut, "/api/logins/"+login+"/proxy", `{"proxy":"`+accounts.MaskProxy(full)+`"}`); code != http.StatusOK {
		t.Fatalf("打码回填：%d %s", code, body)
	}
	if l, _ := acc.Login(login); l.Proxy != full {
		t.Fatalf("打码回填应保留原值，得 %q", l.Proxy)
	}
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/logins/" + login + "/proxy", `{"proxy":"ftp://nope"}`, http.StatusBadRequest},
		{"/api/logins/" + login + "/proxy", `{}`, http.StatusBadRequest},
		{"/api/logins/" + login + "/proxy", `not json`, http.StatusBadRequest},
		{"/api/logins/login-0000000000000000/proxy", `{"proxy":""}`, http.StatusNotFound},
	} {
		if code, body := schedReq(t, env, http.MethodPut, tc.path, tc.body); code != tc.want || strings.Contains(body, "pw-SENTINEL") {
			t.Fatalf("PUT %s %s → %d %s，want %d", tc.path, tc.body, code, body, tc.want)
		}
	}
	if code, body := schedReq(t, env, http.MethodPut, "/api/logins/"+login+"/proxy", `{"proxy":""}`); code != http.StatusOK ||
		strings.Contains(body, `"proxy"`) {
		t.Fatalf("空串清除，未设代理时键缺席：%d %s", code, body)
	}
	for name, got := range consumers() {
		if got != "" {
			t.Fatalf("清除后 %s 不再套用代理，得 %q", name, got)
		}
	}
}

func TestAccountAPIRenameConflictAndNAProbe(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	putTestLogin(t, env.srv.SchedulingRegistry(), acc, "cx", "m1", "", "")
	putTestLogin(t, env.srv.SchedulingRegistry(), acc, "cy", "m", "", "")
	cx, err := acc.Get("cx")
	if err != nil {
		t.Fatal(err)
	}
	put := func(ref string, version int, body string) (int, string) {
		return schedReq(t, env, http.MethodPut, "/api/accounts/"+ref+"?expect="+strconv.Itoa(version), body)
	}
	if code, body := put(cx.Account.ID, cx.Version, `{"name":"cy"}`); code != http.StatusConflict || !strings.Contains(body, "已被") {
		t.Fatalf("改名撞名应 409：%d %s", code, body)
	}
	if code, body := put(cx.Account.ID, cx.Version, `{"name":"cx/main"}`); code != http.StatusBadRequest {
		t.Fatalf("改名含 / 应 400：%d %s", code, body)
	}
	code, body := put(cx.Account.ID, cx.Version, `{"name":"cx-main"}`)
	var av proto.AccountView
	if code != http.StatusOK || json.Unmarshal([]byte(body), &av) != nil || av.Name != "cx-main" || av.ID != cx.Account.ID {
		t.Fatalf("合法改名应 200 且回新名：%d %s", code, body)
	}
	if code, body := schedReq(t, env, http.MethodPost, "/api/accounts/cx-main/probe", ""); code == http.StatusNotFound {
		t.Fatalf("改名后按新名可引用：%d %s", code, body)
	}

	if _, err := acc.Reconcile(accounts.Discovery{Machine: "m1", At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: "~/.handoff/home/cl", HomeAbs: "/home/cl", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindClaude, Source: "claude-auth", Fingerprint: "sha256:c1c1c1c1c1c1", Label: "cl@example.com"}}}}}); err != nil {
		t.Fatal(err)
	}
	cl, err := acc.Get("claude:c1c1c1c1c1c1")
	if err != nil || cl.State != accounts.StateNA {
		t.Fatalf("claude 账号应 na：%+v %v", cl, err)
	}
	if code, body := schedReq(t, env, http.MethodPost, "/api/accounts/claude:c1c1c1c1c1c1/probe", ""); code != http.StatusConflict ||
		!strings.Contains(body, "不支持额度探测") {
		t.Fatalf("na 账号立即探测应 409：%d %s", code, body)
	}
	if code, body := put(cl.Account.ID, cl.Version, `{"warn_percent":50,"low_percent":70,"interval_minutes":15}`); code != http.StatusBadRequest {
		t.Fatalf("na 账号写阈值应 400：%d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK || !strings.Contains(body, `"state":"na"`) {
		t.Fatalf("GET 应带 na 状态：%d %s", code, body)
	}
}
