// B439：B233.27 接收机准入改走自名判据——载体登记机 == 自名时，冻结派发
// 必须在自名接收机上准入（不再依赖「登记名指向 Listen」的地址判据）。
package agentd

import (
	"net/http"
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling"
)

func TestB439SelfNameCarrierMachineAdmitted(t *testing.T) {
	env := newReceiverTestEnv(t)
	// 登记名的地址**不**指向本机（地址判会说「不是本机」），但自名 == linux-01；
	// 准入必须靠名字判放行，不能退回地址判。
	putTarget(t, env.srv, "linux-01", "192.0.2.9:7777")
	env.srv.conf().Name = "linux-01"
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"linux-01","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusOK {
		t.Fatalf("自名载体冻结派发返回 %d: %s", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Carrier != "self-box" {
		t.Fatalf("任务载体=%q, want self-box", task.Carrier)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("清理自名任务返回 %d: %s", stop.Code, stop.Body.String())
	}
}
