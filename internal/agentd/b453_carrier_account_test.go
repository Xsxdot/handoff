// b453_carrier_account_test.go —— B453 段 3 gateway 面：载体账号引用的登记与投影（F13）、
// 检测链发指纹键与标记账号（F15/F16、Q19）、目标机按账号名在本机查代理（Q6）、
// 他机点名派发经统一判据预检（S5）。
//
// 边界：两台机器的互调用 httptest 假目标机；账号读数直接经账号域写，不起探测器。
package agentd

import (
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
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// wireAccounts 按 cmd 组装点同形接线账号域：gateway 账号端点、编制域准入/登记/检测标记、
// 删除拒删的载体引用查询。
func wireAccounts(t *testing.T, env *schedEnv) *accounts.Service {
	t.Helper()
	acc := accounts.New(env.srv.SchedulingRegistry())
	env.srv.SetAccounts(acc)
	env.svc.SetAccounts(acc)
	acc.SetReferrers(env.svc.CarriersUsingAccount)
	return acc
}

func putTestAccount(t *testing.T, acc *accounts.Service, name, machine, proxy string) {
	t.Helper()
	if _, err := acc.Put(accounts.Account{Name: name, Kind: accounts.KindCodex, Machine: machine,
		HomeDir: "/home/" + name, Entry: "tokens", Proxy: proxy, IntervalMinutes: 15, WarnPercent: 60, LowPercent: 80}, 0); err != nil {
		t.Fatalf("登记账号 %s: %v", name, err)
	}
}

func readingWithFingerprint(t *testing.T, acc *accounts.Service, name, fp string, used float64) {
	t.Helper()
	now := time.Now()
	reset := now.Add(2 * time.Hour)
	if err := acc.RecordProbe(name, accounts.ProbeOutcome{StartedAt: now, FinishedAt: now, Writer: "test",
		Reading: &accounts.Reading{ProbedAt: now, Fingerprint: fp, Windows: []accounts.Window{
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

func TestSquadsProjectAccountAndFingerprintMismatch(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	putTestAccount(t, acc, "cx", "m1", "")
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m1","cli":"codex","home_dir":"/h/c1","credential":"standalone","account":"cx"}`)
	if code != http.StatusOK {
		t.Fatalf("登记带账号的载体: %d %s", code, body)
	}
	c, raw := squadsCarrier(t, env, "c1")
	if c.Account != "cx" || c.AccountMismatch || strings.Contains(raw, "account_mismatch") {
		t.Fatalf("未检测时：account=%q mismatch 键应缺席：%s", c.Account, raw)
	}

	readingWithFingerprint(t, acc, "cx", "sha256:aaaaaaaaaaaa", 10)
	if _, err := env.svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true, Fingerprint: "sha256:bbbbbbbbbbbb"}, ""); err != nil {
		t.Fatalf("检测写指纹: %v", err)
	}
	if c, raw = squadsCarrier(t, env, "c1"); !c.AccountMismatch {
		t.Fatalf("两侧指纹都在且不同应标 account_mismatch：%s", raw)
	}
	if _, err := env.svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true, Fingerprint: "sha256:aaaaaaaaaaaa"}, ""); err != nil {
		t.Fatalf("检测写指纹: %v", err)
	}
	if c, raw = squadsCarrier(t, env, "c1"); c.AccountMismatch || strings.Contains(raw, "account_mismatch") {
		t.Fatalf("指纹一致不标：%s", raw)
	}
	if _, err := env.svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("检测无指纹: %v", err)
	}
	if c, raw = squadsCarrier(t, env, "c1"); c.AccountMismatch {
		t.Fatalf("载体侧指纹缺席不比对：%s", raw)
	}
}

func TestCarrierPutRejectsOtherMachineAccountAndDeleteRefusesReferenced(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	putTestAccount(t, acc, "cx-m", "m", "")
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"m1","cli":"codex","home_dir":"/h/c1","credential":"standalone","account":"cx-m"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "同机") {
		t.Fatalf("选他机账号应 400 且说明只能选同机账号：%d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPut, "/api/squads/carriers/c2?expect=0",
		`{"machine":"m","cli":"codex","home_dir":"/h/c2","credential":"standalone","account":"cx-m"}`)
	if code != http.StatusOK {
		t.Fatalf("选同机账号: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/cx-m?expect=1", "")
	if code != http.StatusConflict || !strings.Contains(body, "c2") {
		t.Fatalf("被载体引用的账号删除应 409 并列出载体（SetReferrers 已接上）：%d %s", code, body)
	}
}

func TestCarrierDetectRemoteSendsFingerprintKeysAndMarksAccount(t *testing.T) {
	var gotBody string
	env, _ := newRemoteSchedEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outcome":"quota","detail":"You've hit your usage limit","fingerprint":"sha256:cccccccccccc"}`))
	}))
	acc := wireAccounts(t, env)
	putTestAccount(t, acc, "cx", "remote", "socks5://u:secret@127.0.0.1:7897")
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"remote","cli":"codex","home_dir":"/h/c1","credential":"standalone","account":"cx"}`)
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
	if wake.FingerprintKind != "codex" || wake.FingerprintEntry != "tokens" || wake.Account != "cx" {
		t.Fatalf("唤起请求应带所选账号 kind/条目/名：%s", gotBody)
	}
	if strings.Contains(gotBody, "secret") || strings.Contains(gotBody, "7897") {
		t.Fatalf("代理串不得过机间 wire：%s", gotBody)
	}
	var detect proto.CarrierDetectResp
	_ = json.Unmarshal([]byte(body), &detect)
	if detect.Status != "online" || detect.LastError != "" {
		t.Fatalf("撞额度的载体按已上线、原因不进载体（已绑账号）：%+v", detect)
	}
	c, err := env.svc.Carrier("c1")
	if err != nil || c.CredFingerprint != "sha256:cccccccccccc" {
		t.Fatalf("指纹应写回载体：%+v/%v", c, err)
	}
	v, err := acc.Get("cx")
	if err != nil || v.State != accounts.StateOut || v.Record.QuotaMark == nil || v.Record.QuotaMark.Carrier != "c1" {
		t.Fatalf("绑定账号应被标无额度（来源载体 c1）：%+v/%v", v, err)
	}
}

func TestHostWakeResolvesAccountProxyOnHostMachine(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.txt")
	installDetectCLI(t, `echo "$HTTPS_PROXY|$NO_PROXY" > `+envFile+`
printf '%s\n' '{"type":"text","sessionID":"ses_detect","part":{"type":"text","text":"ok"}}'`)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	env := newDirectSchedEnv(t, &config.Config{Name: "self", Token: testToken, DataDir: t.TempDir()})
	acc := wireAccounts(t, env)
	putTestAccount(t, acc, "cx", "self", "http://127.0.0.1:18080")
	putTestAccount(t, acc, "cx-other", "elsewhere", "http://127.0.0.1:19090")

	code, body := schedReq(t, env, http.MethodPost, "/api/host/wake",
		`{"cli":"opencode","home_dir":"`+t.TempDir()+`","fingerprint_kind":"codex","account":"cx"}`)
	if code != http.StatusOK {
		t.Fatalf("唤起: %d %s", code, body)
	}
	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("读假 CLI 环境: %v", err)
	}
	if strings.TrimSpace(string(got)) != "http://127.0.0.1:18080|localhost,127.0.0.1,::1" {
		t.Fatalf("检测回合应走本机账号代理且补 NO_PROXY，得 %q", got)
	}

	code, body = schedReq(t, env, http.MethodPost, "/api/host/wake",
		`{"cli":"opencode","home_dir":"`+t.TempDir()+`","account":"cx-other"}`)
	if code != http.StatusOK {
		t.Fatalf("唤起（他机账号）: %d %s", code, body)
	}
	got, _ = os.ReadFile(envFile)
	if strings.Contains(string(got), "19090") {
		t.Fatalf("账号不在本机时不得套用它的代理，得 %q", got)
	}
}

func TestRouteNamedRemoteCarrierUsesUnifiedCriterion(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	putTestAccount(t, acc, "cx", "m1", "")
	putOnline := func(name string) {
		if err := env.svc.PutCarrier(scheduling.Carrier{Name: name, Machine: "m1", CLI: "codex", HomeDir: "/h/" + name,
			Credential: scheduling.CredentialStandalone, Account: "cx"}, 0); err != nil {
			t.Fatalf("登记: %v", err)
		}
		if _, err := env.svc.ApplyDetect(name, scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
			t.Fatalf("上线: %v", err)
		}
	}
	putOnline("c1")
	readingWithFingerprint(t, acc, "cx", "", 100)
	_, err := env.srv.routeOrdinaryDispatch(dispatchRequest{Receiver: "c1"})
	if !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "载体 c1 不可用：账号 cx 无额度") {
		t.Fatalf("点名他机载体而账号无额度应拒并点名，得 %v", err)
	}
}

// TestDispatchCarrierAccountSnapshotReachesTaskRow 锁 Q6 的 gateway 半边：落点机派发查载体所选
// 账号，把名/kind/代理交编排落任务行；任务快照 wire 不带代理串。
func TestDispatchCarrierAccountSnapshotReachesTaskRow(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	svc := mustScheduling(t, env.srv)
	acc := accounts.New(env.srv.SchedulingRegistry())
	env.srv.SetAccounts(acc)
	svc.SetAccounts(acc)
	putTestAccount(t, acc, "cx", "linux-01", "socks5://u:secret@127.0.0.1:7897")
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1, Account: "cx",
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"","frozen_target":"linux-01","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusOK {
		t.Fatalf("冻结派发应 200，实得 %d（%s）", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "7897") {
		t.Fatalf("任务快照 wire 不得带账号代理：%s", rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	stored, err := env.srv.st.GetTask(task.ID)
	if err != nil {
		t.Fatalf("读任务行: %v", err)
	}
	if stored.Account != "cx" || stored.AccountKind != accounts.KindCodex || stored.AccountProxy != "socks5://u:secret@127.0.0.1:7897" {
		t.Fatalf("任务行账号快照 = %q/%q/%q", stored.Account, stored.AccountKind, stored.AccountProxy)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("清理任务返回 %d: %s", stop.Code, stop.Body.String())
	}
}
