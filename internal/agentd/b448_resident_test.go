package agentd

// b448_resident_test.go —— B448 S2/S3：会话驻场者首轮转交竖切与反例（plan §5 V2 / V2a–c / V6 驻场腿）。
//
// 观察点：两台真实 agentd 共享账本，打受理机 A 的 POST /api/sessions 与 POST /api/sessions/{id}/resident/launch；
// 执行机 B 无控制台身份（F5）。判据：响应 resident 形状、驻场行（push_session 补写、carrier_machine=B）、
// 两台 Runner 调用数、会话指针行条数（Q3 甲：恰一条，谁写由回执 pointed 与重读驻场位决定）、两级计数。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

func b448CreateSession(t *testing.T, p *b448Pair, card string) proto.SessionCreateResp {
	t.Helper()
	code, raw := ledgerPost(t, p.a.testAgentdEnv, "/api/sessions",
		fmt.Sprintf(`{"project":"handoff","cards":[%q],"squad":%q}`, card, b448Squad))
	if code != http.StatusOK {
		t.Fatalf("建会话应 200，得 %d %s", code, raw)
	}
	var resp proto.SessionCreateResp
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("解码建会话响应: %v %s", err, raw)
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &keys)
	var resident map[string]json.RawMessage
	_ = json.Unmarshal(keys["resident"], &resident)
	if _, ok := resident["handled_by"]; ok {
		t.Fatalf("入口 resident 不得外泄 handled_by（F18）: %s", raw)
	}
	return resp
}

// V2（S2 成功，建会话入口）：驻场者在 B 拉起——响应 identity/carrier/carrier_machine=B；驻场行由 B 写且
// push_session 已补写；B 恰一次 Launch、A 零次；workdir 在 B 解析；两级计数归零。
func TestB448ResidentCreateTransfersToCarrierMachine(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	card := b448Card(t, p)
	resp := b448CreateSession(t, p, card)
	if resp.Resident == nil || resp.Resident.Error != "" || resp.Resident.Identity != "agent:"+b448Squad ||
		resp.Resident.Carrier != b448Carrier || resp.Resident.CarrierMachine != b448MachineB {
		t.Fatalf("驻场者应在执行机坐下: %+v", resp.Resident)
	}
	r, ok, err := p.a.ledger.SessionResident(resp.ID)
	if err != nil || !ok {
		t.Fatalf("驻场行应存在: ok=%v err=%v", ok, err)
	}
	if r.DriverMode != ledger.ResidentDriverPush || r.PushSession != "ses_t448-linux_1" ||
		r.CarrierMachine != b448MachineB || r.Workdir != "/repo/t448-linux" {
		t.Fatalf("驻场行应由执行机写且 push_session 已补写: %+v", r)
	}
	if len(p.a.runner.launchSpecs()) != 0 || len(p.b.runner.launchSpecs()) != 1 {
		t.Fatalf("只有执行机拉起：A=%d B=%d", len(p.a.runner.launchSpecs()), len(p.b.runner.launchSpecs()))
	}
	if n := len(b444Pointers(t, p.a.ledgerEnv, resp.ID)); n != 0 {
		t.Fatalf("成功不得落指针行，得 %d", n)
	}
	p.assertZeroCounts(t)
}

// V2a + V2（resident/launch 入口）+ V2c：B 回合失败 → resident.error 非空、驻场位空、指针行恰一条（B 写，
// pointed=true，A 不追加）；排除故障后经 A 的 resident/launch 重试 → 200 且驻场行在 B；再叫 → 409、零新增指针。
func TestB448ResidentFailureOnCarrierPointsOnceThenRetry(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	card := b448Card(t, p)
	p.b.runner.launchErr = errors.New("载体登录失效")
	resp := b448CreateSession(t, p, card)
	if resp.Resident == nil || resp.Resident.Identity != "" || !strings.Contains(resp.Resident.Error, "载体登录失效") {
		t.Fatalf("执行机首轮失败应只带 resident.error: %+v", resp.Resident)
	}
	if _, ok, err := p.a.ledger.SessionResident(resp.ID); err != nil || ok {
		t.Fatalf("失败后驻场位应为空: ok=%v err=%v", ok, err)
	}
	if ptrs := b444Pointers(t, p.a.ledgerEnv, resp.ID); len(ptrs) != 1 || !strings.Contains(ptrs[0], "resident launch") {
		t.Fatalf("指针行应恰一条（执行机写），得 %q", ptrs)
	}
	p.assertZeroCounts(t)

	p.b.runner.mu.Lock()
	p.b.runner.launchErr = nil
	p.b.runner.mu.Unlock()
	code, raw := ledgerPost(t, p.a.testAgentdEnv, "/api/sessions/"+resp.ID+"/resident/launch",
		fmt.Sprintf(`{"squad":%q}`, b448Squad))
	if code != http.StatusOK {
		t.Fatalf("重试叫驻场者应 200，得 %d %s", code, raw)
	}
	var outcome proto.SessionResidentOutcome
	if err := json.Unmarshal([]byte(raw), &outcome); err != nil || outcome.CarrierMachine != b448MachineB ||
		outcome.Identity != "agent:"+b448Squad || strings.Contains(raw, "handled_by") {
		t.Fatalf("重试响应应带执行机落点且不外泄 handled_by: %s", raw)
	}
	if r, ok, _ := p.a.ledger.SessionResident(resp.ID); !ok || r.CarrierMachine != b448MachineB || r.PushSession == "" {
		t.Fatalf("重试后驻场行应在执行机且已补写: ok=%v %+v", ok, r)
	}
	code, raw = ledgerPost(t, p.a.testAgentdEnv, "/api/sessions/"+resp.ID+"/resident/launch", `{}`)
	if code != http.StatusConflict {
		t.Fatalf("已有驻场者再叫应 409，得 %d %s", code, raw)
	}
	if n := len(b444Pointers(t, p.a.ledgerEnv, resp.ID)); n != 1 {
		t.Fatalf("409 不得新增指针行，得 %d 条", n)
	}
	if len(p.a.runner.launchSpecs()) != 0 || len(p.b.runner.launchSpecs()) != 2 {
		t.Fatalf("两次拉起都应在执行机：A=%d B=%d", len(p.a.runner.launchSpecs()), len(p.b.runner.launchSpecs()))
	}
	p.assertZeroCounts(t)
}

// V2b：B 不可达 → resident.error 点名载体与机器；指针行恰一条（A 写：没回执且驻场位空）；会话与卡保留。
func TestB448ResidentUnreachablePointsOnceByAcceptor(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	p.a.setTargets(t, map[string]string{b448MachineB: "127.0.0.1:1"})
	card := b448Card(t, p)
	resp := b448CreateSession(t, p, card)
	if resp.Resident == nil || !strings.Contains(resp.Resident.Error, "协调者载体 "+b448Carrier+" 在 "+b448MachineB+"，转交到该机失败") {
		t.Fatalf("不可达应点名载体与机器: %+v", resp.Resident)
	}
	if ptrs := b444Pointers(t, p.a.ledgerEnv, resp.ID); len(ptrs) != 1 || !strings.Contains(ptrs[0], b448MachineB) {
		t.Fatalf("指针行应恰一条且点名机器，得 %q", ptrs)
	}
	if owner, _ := p.a.ledger.SessionOfCard(card); owner != resp.ID {
		t.Fatalf("卡应仍在会话里，得 %q", owner)
	}
	if len(p.b.runner.launchSpecs()) != 0 {
		t.Fatal("不可达不得拉起")
	}
	p.assertZeroCounts(t)
}

// V6 驻场腿：老对端（404）→ resident.error 为版本过旧文案；指针行恰一条（A 写：有回执、pointed=false）。
func TestB448ResidentOldPeerPointsOnce(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	old := testhttp.NewServer(t, http.NotFoundHandler())
	p.a.setTargets(t, map[string]string{b448MachineB: strings.TrimPrefix(old.URL, "http://")})
	card := b448Card(t, p)
	resp := b448CreateSession(t, p, card)
	if resp.Resident == nil || !strings.Contains(resp.Resident.Error, b448MachineB+" 的 agentd 版本过旧，不支持首轮转交，请升级") {
		t.Fatalf("老对端应报版本过旧: %+v", resp.Resident)
	}
	if n := len(b444Pointers(t, p.a.ledgerEnv, resp.ID)); n != 1 {
		t.Fatalf("老对端指针行应恰一条，得 %d", n)
	}
	p.assertZeroCounts(t)
}
