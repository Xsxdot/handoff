// b453_carrier_account_test.go —— B453 段 3 gateway 面（B464 段 1 起载体 → 登录 → 账号）：载体选
// 登录的登记与投影（A13/A14：二选一、HOME 镜像、派生账号名与登录状态）、检测链只发登录 id 并
// 标记账号（W1、F16）、目标机按登录 id 在本机查代理（Q6）、他机点名派发经统一判据预检（S5）、
// 派发快照落账号 id（Q23）。
//
// 边界：两台机器的互调用 httptest 假目标机；账号与登录经账号域 Reconcile 建（不起发现器），读数
// 直接经账号域写，不起探测器。
package agentd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// wireAccounts 按 cmd 组装点同形接线账号域：gateway 账号端点、编制域准入/登记/检测标记、
// 删除拒删的载体引用查询（编制域 LoginReferrers）。
func wireAccounts(t *testing.T, env *schedEnv) *accounts.Service {
	t.Helper()
	acc := accounts.New(env.srv.SchedulingRegistry())
	env.srv.SetAccounts(acc)
	env.svc.SetAccounts(acc)
	acc.SetReferrers(env.svc.LoginReferrers)
	return acc
}

// putTestLogin 以一次发现归并在 machine 上建出一个 codex 账号与其登录（label = name，自动名即
// name），返回登录 id。home 空 = `~/.handoff/home/<name>`；"~" = 主 HOME。proxy 非空时改写登录行
// 模拟「已设代理」（登录代理端点属 B464 段 2）。B464：账号只由发现建。
func putTestLogin(t *testing.T, reg schedclient.Registry, acc *accounts.Service, name, machine, home, proxy string) string {
	t.Helper()
	if home == "" {
		home = "~/.handoff/home/" + name
	}
	sum := sha256.Sum256([]byte(name))
	fp := "sha256:" + hex.EncodeToString(sum[:])[:12]
	res, err := acc.Reconcile(accounts.Discovery{Machine: machine, At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: home, HomeAbs: "/home/" + name,
		Logins: []accounts.FoundLogin{{Kind: accounts.KindCodex, Source: "auth.json", Fingerprint: fp, Label: name}},
	}}})
	if err != nil || len(res.Logins) != 1 {
		t.Fatalf("发现建账号 %s: %+v %v", name, res, err)
	}
	id := res.Logins[0].ID
	if proxy != "" {
		setLoginProxy(t, reg, id, proxy)
	}
	return id
}

// setLoginProxy 改写登录行的 proxy 键，模拟「已设登录代理」（设置端点属 B464 段 2）。
func setLoginProxy(t *testing.T, reg schedclient.Registry, loginID, proxy string) {
	t.Helper()
	rec, err := reg.Get(accounts.KindLogin, loginID)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(rec.Body, &row); err != nil {
		t.Fatal(err)
	}
	row["proxy"] = proxy
	body, _ := json.Marshal(row)
	if _, err := reg.Put(accounts.KindLogin, loginID, rec.Version, body, "test"); err != nil {
		t.Fatal(err)
	}
}

func usedReading(t *testing.T, acc *accounts.Service, name string, used float64) {
	t.Helper()
	now := time.Now()
	reset := now.Add(2 * time.Hour)
	if err := acc.RecordProbe(name, accounts.ProbeOutcome{StartedAt: now, FinishedAt: now, Writer: "test",
		Reading: &accounts.Reading{ProbedAt: now, Windows: []accounts.Window{
			{Key: "session", UsedPercent: used, ResetsAt: &reset}}}}); err != nil {
		t.Fatalf("写账号 %s 读数: %v", name, err)
	}
}

func squadsCarrier(t *testing.T, env *schedEnv, name string) (proto.CarrierView, string) {
	t.Helper()
	code, body := schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/squads: %d %s", code, body)
	}
	var resp proto.SquadsResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码: %v", err)
	}
	for _, c := range resp.Carriers {
		if c.Name == name {
			return c, body
		}
	}
	t.Fatalf("载体 %s 不在 /api/squads：%s", name, body)
	return proto.CarrierView{}, ""
}

func TestSquadsProjectCarrierLoginAndPutRequiresOneChoice(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	login := putTestLogin(t, env.srv.SchedulingRegistry(), acc, "cx", "m1", "", "")
	// 请求体残留的 home_dir/credential/account 键按未知键忽略：HOME 由服务端按登录镜像（plan V10）。
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m1","cli":"codex","home_dir":"/h/ignored","credential":"main_home_sync","account":"x","login":"`+login+`"}`)
	if code != http.StatusOK {
		t.Fatalf("登记选登录的载体: %d %s", code, body)
	}
	c, raw := squadsCarrier(t, env, "c1")
	if c.Login != login || c.AccountName != "cx" || c.AccountID == "" || c.LoginState != "ok" ||
		c.HomeDir != "~/.handoff/home/cx" || c.NoAccount || strings.Contains(raw, `"credential"`) ||
		strings.Contains(raw, `"account_mismatch"`) || strings.Contains(raw, `"no_account"`) {
		t.Fatalf("载体投影应带所选登录的账号与状态、HOME 为登录镜像、无凭据模式键：%s", raw)
	}
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c2?expect=0",
		`{"machine":"m1","cli":"codex","no_account":true}`)
	if code != http.StatusOK {
		t.Fatalf("登记无账号载体: %d %s", code, body)
	}
	if c, raw = squadsCarrier(t, env, "c2"); !c.NoAccount || c.HomeDir != "" || c.Login != "" || c.AccountName != "" {
		t.Fatalf("无账号载体应 no_account=true、主 HOME：%s", raw)
	}
	for _, bad := range []string{
		`{"machine":"m1","cli":"codex"}`,
		`{"machine":"m1","cli":"codex","home_dir":"/h/c3"}`,
		`{"machine":"m1","cli":"codex","no_account":true,"login":"` + login + `"}`,
	} {
		if code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c3?expect=0", bad); code != http.StatusBadRequest ||
			!strings.Contains(body, "二选一") {
			t.Fatalf("login 与 no_account 须恰一个，%s 应 400：%d %s", bad, code, body)
		}
	}
}

func TestCarrierPutRejectsOtherMachineLoginAndDeleteRefusesReferenced(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	login := putTestLogin(t, env.srv.SchedulingRegistry(), acc, "cx-m", "m", "", "")
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m1","cli":"codex","login":"`+login+`"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "同机") {
		t.Fatalf("选他机登录应 400 且说明只能选同机登录：%d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m","cli":"grok","login":"`+login+`"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("kind 与 CLI 不兼容应 400：%d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m","cli":"codex","login":"login-ffffffffffffffff"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "不存在") {
		t.Fatalf("不存在的登录应 400：%d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c2?expect=0",
		`{"machine":"m","cli":"codex","login":"`+login+`"}`)
	if code != http.StatusOK {
		t.Fatalf("选同机登录: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/cx-m?expect=1", "")
	if code != http.StatusConflict || !strings.Contains(body, "c2") {
		t.Fatalf("名下登录被载体引用的账号删除应 409 并列出载体（SetReferrers 已接上）：%d %s", code, body)
	}
}

func TestCarrierDetectRemoteSendsLoginAndMarksAccount(t *testing.T) {
	var gotBody string
	env, _ := newRemoteSchedEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outcome":"quota","detail":"You've hit your usage limit","fingerprint":"sha256:cccccccccccc"}`))
	}))
	acc := wireAccounts(t, env)
	login := putTestLogin(t, env.srv.SchedulingRegistry(), acc, "cx", "remote", "", "socks5://u:secret@127.0.0.1:7897")
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"remote","cli":"codex","login":"`+login+`"}`)
	if code != http.StatusOK {
		t.Fatalf("登记远程载体: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPost, "/api/squads/carriers/c1/detect", "{}")
	if code != http.StatusOK {
		t.Fatalf("远程检测: %d %s", code, body)
	}
	var wake proto.HomeWakeReq
	if err := json.Unmarshal([]byte(gotBody), &wake); err != nil {
		t.Fatalf("解码唤起请求: %v", err)
	}
	if wake.Login != login || wake.HomeDir != "~/.handoff/home/cx" || wake.CLI != "codex" {
		t.Fatalf("唤起请求应带所选登录 id 与镜像 HOME：%s", gotBody)
	}
	for _, leak := range []string{"secret", "7897", "credential", "fingerprint", `"account"`} {
		if strings.Contains(gotBody, leak) {
			t.Fatalf("唤起请求不得带代理串或旧凭据/指纹键 %q：%s", leak, gotBody)
		}
	}
	var detect proto.CarrierDetectResp
	_ = json.Unmarshal([]byte(body), &detect)
	if detect.Status != "online" || detect.LastError != "" {
		t.Fatalf("撞额度的载体按已上线、原因不进载体（已选登录）：%+v", detect)
	}
	v, err := acc.Get("cx")
	if err != nil || v.State != accounts.StateOut || v.Record.QuotaMark == nil || v.Record.QuotaMark.Carrier != "c1" {
		t.Fatalf("所选登录的账号应被标无额度（来源载体 c1）：%+v/%v", v, err)
	}
}

func TestHostWakeResolvesLoginProxyOnHostMachine(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.txt")
	installDetectCLI(t, `echo "$HTTPS_PROXY|$NO_PROXY" > `+envFile+`
printf '%s\n' '{"type":"text","sessionID":"ses_detect","part":{"type":"text","text":"ok"}}'`)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	env := newDirectSchedEnv(t, &config.Config{Name: "self", Token: testToken, DataDir: t.TempDir()})
	acc := wireAccounts(t, env)
	reg := env.srv.SchedulingRegistry()
	mine := putTestLogin(t, reg, acc, "cx", "self", "", "http://127.0.0.1:18080")
	other := putTestLogin(t, reg, acc, "cx-other", "elsewhere", "", "http://127.0.0.1:19090")

	code, body := schedReq(t, env, http.MethodPost, "/api/host/wake",
		`{"cli":"opencode","home_dir":"`+t.TempDir()+`","login":"`+mine+`"}`)
	if code != http.StatusOK {
		t.Fatalf("唤起: %d %s", code, body)
	}
	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("读假 CLI 环境: %v", err)
	}
	if strings.TrimSpace(string(got)) != "http://127.0.0.1:18080|localhost,127.0.0.1,::1" {
		t.Fatalf("检测回合应走本机登录代理且补 NO_PROXY，得 %q", got)
	}

	code, body = schedReq(t, env, http.MethodPost, "/api/host/wake",
		`{"cli":"opencode","home_dir":"`+t.TempDir()+`","login":"`+other+`"}`)
	if code != http.StatusOK {
		t.Fatalf("唤起（他机登录）: %d %s", code, body)
	}
	got, _ = os.ReadFile(envFile)
	if strings.Contains(string(got), "19090") {
		t.Fatalf("登录不在本机时不得套用它的代理，得 %q", got)
	}
}

func TestRouteNamedRemoteCarrierUsesUnifiedCriterion(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	login := putTestLogin(t, env.srv.SchedulingRegistry(), acc, "cx", "m1", "", "")
	if err := env.svc.PutCarrier(scheduling.Carrier{Name: "c1", Machine: "m1", CLI: "codex", Login: login}, 0); err != nil {
		t.Fatalf("登记: %v", err)
	}
	if _, err := env.svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("上线: %v", err)
	}
	usedReading(t, acc, "cx", 100)
	_, err := env.srv.routeOrdinaryDispatch(dispatchRequest{Receiver: "c1"})
	if !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "载体 c1 不可用：账号 cx 无额度") {
		t.Fatalf("点名他机载体而账号无额度应拒并点名，得 %v", err)
	}
}

// TestDispatchCarrierAccountSnapshotReachesTaskRow 锁 Q6 的 gateway 半边：落点机派发查载体所选
// 登录，把账号 id/kind/登录代理交编排落任务行（B464 Q23：任务行 account 存账号 id）；任务快照
// wire 不带代理串。
func TestDispatchCarrierAccountSnapshotReachesTaskRow(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	svc := mustScheduling(t, env.srv)
	acc := accounts.New(env.srv.SchedulingRegistry())
	env.srv.SetAccounts(acc)
	svc.SetAccounts(acc)
	login := putTestLogin(t, env.srv.SchedulingRegistry(), acc, "cx", "linux-01", accounts.MainHome, "socks5://u:secret@127.0.0.1:7897")
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "codex", MaxConcurrency: 1, Login: login,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"","frozen_target":"linux-01","executor":"codex","home_dir":""`))
	if rr.Code != http.StatusOK {
		t.Fatalf("冻结派发应 200，实得 %d（%s）", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "7897") {
		t.Fatalf("任务快照 wire 不得带登录代理：%s", rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	stored, err := env.srv.st.GetTask(task.ID)
	if err != nil {
		t.Fatalf("读任务行: %v", err)
	}
	v, err := acc.Get("cx")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Account != v.Account.ID || stored.AccountKind != accounts.KindCodex || stored.AccountProxy != "socks5://u:secret@127.0.0.1:7897" {
		t.Fatalf("任务行账号快照 = %q/%q/%q", stored.Account, stored.AccountKind, stored.AccountProxy)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("清理任务返回 %d: %s", stop.Code, stop.Body.String())
	}
}
