package agentd

// b453_recover_at_test.go —— 审查第 1 轮 I2：账号读面带出服务端推导的预计恢复时刻 recover_at
// （spec r1.2 D1 / accounts.RecoverAt），前端直接读它，不再自算一份（曾取越线窗口最早，与
// 后端相反）。穿真实 Handler/JSON 锁三条：越线窗口取最晚；撞额度标记而无越线窗口 → 键缺席；
// 不阻挡（正常）→ 键缺席。
import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func TestB453AccountViewRecoverAtFollowsDomainRule(t *testing.T) {
	env, svc := newSingleAccountMachine(t)
	for _, name := range []string{"cx-low", "cx-mark", "cx-ok"} {
		// B464：账号只由发现建（label = name，自动名即 name）；各在一台机上，互不影响登录状态。
		putTestLogin(t, env.srv.SchedulingRegistry(), svc, name, "m-"+name, "", "")
	}
	now := time.Now().UTC()
	in2h, in5d := now.Add(2*time.Hour).Truncate(time.Second), now.Add(5*24*time.Hour).Truncate(time.Second)
	probe := func(name string, ws ...accounts.Window) {
		t.Helper()
		if err := svc.RecordProbe(name, accounts.ProbeOutcome{StartedAt: now.Add(-time.Minute), FinishedAt: now.Add(-time.Minute),
			Reading: &accounts.Reading{ProbedAt: now.Add(-time.Minute), Windows: ws}}); err != nil {
			t.Fatal(err)
		}
	}
	// 5h 85% 2h 后重置 + 周 90% 5d 后重置：两个都越线，恢复要等最晚那个 → 5d
	probe("cx-low", accounts.Window{Key: "session", UsedPercent: 85, ResetsAt: &in2h},
		accounts.Window{Key: "weekly", UsedPercent: 90, ResetsAt: &in5d})
	// 撞额度标记而窗口都没越线：无额度但恢复时刻未知
	probe("cx-mark", accounts.Window{Key: "weekly", UsedPercent: 10, ResetsAt: &in5d})
	if err := svc.MarkQuota("cx-mark", accounts.QuotaMark{At: now, Task: "t1", Machine: "a"}); err != nil {
		t.Fatal(err)
	}
	probe("cx-ok", accounts.Window{Key: "weekly", UsedPercent: 10, ResetsAt: &in5d})

	code, body := schedReq(t, env, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/accounts: %d %s", code, body)
	}
	var raw struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	var resp proto.AccountsResp
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	by := map[string]proto.AccountView{}
	for _, a := range resp.Accounts {
		by[a.Name] = a
	}
	if low := by["cx-low"]; low.State != "low" || low.RecoverAt == nil || !low.RecoverAt.Equal(in5d) {
		t.Fatalf("cx-low 应为 low 且 recover_at = 最晚越线窗口重置 %v，得 %s / %v", in5d, low.State, low.RecoverAt)
	}
	for i, a := range raw.Accounts {
		name := resp.Accounts[i].Name
		if _, has := a["recover_at"]; has && name != "cx-low" {
			t.Fatalf("%s 的恢复时刻未知/不阻挡，recover_at 应缺席: %s", name, body)
		}
	}
	if by["cx-mark"].State != "out" {
		t.Fatalf("cx-mark 应为 out（撞额度标记），得 %s", by["cx-mark"].State)
	}
}
