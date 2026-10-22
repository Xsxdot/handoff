// b464_dispatch_gone_test.go —— M8：经 agentd 派发入口（POST /api/tasks）断言所选登录已失效
// 时载体被统一准入拒绝，文案点名载体与「登录已失效」。域层准入在 b464_login_admission_test.go
// 已有覆盖；这一层锁 HTTP 入口确实走到同一个判据、并把原因交给用户。
//
// 边界：内存账本 + 直接 Reconcile 造登录（不起发现器/探测器）；不派真任务。
package agentd

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func TestDispatchRejectsGoneLoginWithCarrierName(t *testing.T) {
	env := newReceiverTestEnv(t)
	acc := accounts.New(env.srv.SchedulingRegistry())
	env.srv.SetAccounts(acc)
	svc := mustScheduling(t, env.srv)
	svc.SetAccounts(acc)
	acc.SetReferrers(svc.LoginReferrers)
	const machine, home = "local", "~/.handoff/home/cx"
	res, err := acc.Reconcile(accounts.Discovery{Machine: machine, At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: home, HomeAbs: "/home/cx",
		Logins: []accounts.FoundLogin{{Kind: accounts.KindCodex, Source: "auth.json", Fingerprint: "sha256:0a0a0a0a0a0a"}},
	}}})
	if err != nil || len(res.Logins) != 1 {
		t.Fatalf("造登录失败：%+v %v", res, err)
	}
	login := res.Logins[0].ID
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "muse", Machine: machine, CLI: "codex",
		MaxConcurrency: 1, Login: login})
	// 同机重扫扫不到 → 登录已失效（不删行）。
	if _, err := acc.Reconcile(accounts.Discovery{Machine: machine, At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: home, HomeAbs: "/home/cx",
	}}}); err != nil {
		t.Fatalf("标记已失效失败：%v", err)
	}
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, `,"receiver":"muse"`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("登录已失效的载体应拒派 400，得 %d：%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "登录已失效") || !strings.Contains(body, "muse") {
		t.Fatalf("拒绝文案应点名载体与「登录已失效」：%s", body)
	}
	if tasks, err := env.st.ListTasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("被拒的派发不得建任务：tasks=%+v err=%v", tasks, err)
	}
}
