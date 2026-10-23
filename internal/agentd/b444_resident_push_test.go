// b444_resident_push_test.go —— B444 P1（S5 首故事）push 驻场者续轮的竖切与反例（plan §5 A1.1–A1.6、A1.9；
// 缺陷族 R1/R2/R5/R8/R9/R10）。
//
// 观察点：真账本 + 真 collab 匹配函数 + 真 consumeAutomationEventsOnce（从不直调驻场批入口）+ 真
// scheduling 名额 + 真 keystone，只把 keystone 出站 Runner 换成本文件的 b444Runner（记录每次 Resume 的
// SessionRef 与简报）。判据钉行为：Runner 收到的 ref/次数、wake_claims 行、自动化水位、会话里的系统
// 指针行、门铃信箱/opener、HTTP JSON 键的有无。每个「不唤醒」断言配同夹具「唤醒」对照，防稳定假绿。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	b444Squad       = "coord" // seedQueueCoordinator 登记的协调小队
	b444Identity    = "agent:" + b444Squad
	b444PushSession = "ses_b444_resident_1"
	b444Home        = "/tmp/b444-resident-home"
	b444Workdir     = "/tmp/b444-resident-work"
	b444Model       = "b444-free-model"
)

// b444Runner 是 keystone 出站 Runner 的记录桩：Resume 记 ref 与简报；resumeErr 非 nil 时每次返回它。
type b444Runner struct {
	mu        sync.Mutex
	refs      []keysclient.SessionRef
	prompts   []string
	launches  []keysclient.SessionSpec
	resumeErr error
}

func (r *b444Runner) Launch(spec keysclient.SessionSpec, _ string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launches = append(r.launches, spec)
	return keysclient.TurnResult{SessionID: "b444-launched"}, nil
}

func (r *b444Runner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refs = append(r.refs, ref)
	r.prompts = append(r.prompts, prompt)
	if r.resumeErr != nil {
		return keysclient.TurnResult{}, r.resumeErr
	}
	return keysclient.TurnResult{SessionID: ref.SessionID}, nil
}

func (r *b444Runner) snapshot() ([]keysclient.SessionRef, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]keysclient.SessionRef(nil), r.refs...), append([]string(nil), r.prompts...)
}

func (r *b444Runner) setResumeErr(err error) {
	r.mu.Lock()
	r.resumeErr = err
	r.mu.Unlock()
}

// b444Resumes 只取驻场者 push 会话的 resume 次数（卡席位的 resume 另计）。
func b444ResidentResumes(r *b444Runner) []keysclient.SessionRef {
	refs, _ := r.snapshot()
	var out []keysclient.SessionRef
	for _, ref := range refs {
		if ref.SessionID == b444PushSession {
			out = append(out, ref)
		}
	}
	return out
}

// newB444Env 起自动化环境并把 keystone 换成 b444Runner 出站。
func newB444Env(t *testing.T) (*ledgerEnv, *b444Runner) {
	t.Helper()
	env, _ := newNoPTYAutomationEnv(t)
	runner := &b444Runner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	return env, runner
}

// b444SitPush 经 collab 唯一写面坐一个 push 驻场者（机器默认本机别名 local）。
func b444SitPush(t *testing.T, env *ledgerEnv, session, machine string) {
	t.Helper()
	if _, err := env.srv.rooms.SitResident(proto.SessionResident{
		Session: session, Identity: b444Identity, DriverMode: proto.ResidentDriverPush,
		PushCLI: "opencode", PushSession: b444PushSession, Carrier: "coord-carrier", CarrierMachine: machine,
		HomeDir: b444Home, Workdir: b444Workdir, Model: b444Model,
	}); err != nil {
		t.Fatalf("坐 push 驻场者: %v", err)
	}
}

// b444ClaimRows 读驻场认领键的 wake_claims 行（seq → done）。
func b444ClaimRows(t *testing.T, env *ledgerEnv, session string) map[int64]bool {
	t.Helper()
	rows, err := o2RawDB(t, env).Query(`SELECT seq, done_at IS NOT NULL FROM wake_claims WHERE card = ?`, "resident:"+session)
	if err != nil {
		t.Fatalf("读驻场认领: %v", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var seq int64
		var done bool
		if err := rows.Scan(&seq, &done); err != nil {
			t.Fatal(err)
		}
		out[seq] = done
	}
	return out
}

// b444Pointers 数会话里的系统指针行（驻场唤醒失败的可见面）。
func b444Pointers(t *testing.T, env *ledgerEnv, session string) []string {
	t.Helper()
	events, err := env.srv.rooms.History(session, 0, 500)
	if err != nil {
		t.Fatalf("读会话历史: %v", err)
	}
	var out []string
	for _, ev := range events {
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.BySystem && msg.Kind == proto.RoomMsgPointer {
			out = append(out, msg.Body)
		}
	}
	return out
}

// A1.1 成功（竖切）：人类无 @ 发言 → Runner 恰一次 Resume，SessionID = push_session，HOME/workdir/model
// 来自驻场 bearing；认领键 resident:<会话> 收尾；水位越过该 seq。再跑一轮不重复续轮。
func TestB444PushResidentResumesOnHumanMessage(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)

	seq := sendSessionMessage(t, env.srv.rooms, session, "user:tester", "驻场者你好", nil, 0)
	drainAutomation(t, env)

	got := b444ResidentResumes(runner)
	if len(got) != 1 {
		refs, _ := runner.snapshot()
		t.Fatalf("应恰一次驻场续轮，得 %d（全部 resume: %+v）", len(got), refs)
	}
	ref := got[0]
	if ref.CLI != "opencode" || ref.HomeDir != b444Home || ref.Workdir != b444Workdir || ref.Model != b444Model {
		t.Fatalf("续轮 ref 应取自驻场 bearing，得 %+v", ref)
	}
	_, prompts := runner.snapshot()
	if !strings.Contains(prompts[0], "--agent "+b444Squad) || !strings.Contains(prompts[0], session) {
		t.Fatalf("驻场续轮简报应给出以 --agent %s 回到 %s 的发言命令: %q", b444Squad, session, prompts[0])
	}
	claims := b444ClaimRows(t, env, session)
	if done, ok := claims[seq]; !ok || !done {
		t.Fatalf("认领键 resident:%s seq=%d 应存在且已收尾，得 %v", session, seq, claims)
	}
	if c := o2Cursor(env.srv); c < seq {
		t.Fatalf("续轮成功后水位应越过 %d，得 %d", seq, c)
	}
	drainAutomation(t, env)
	if n := len(b444ResidentResumes(runner)); n != 1 {
		t.Fatalf("同一消息不得重复续轮，得 %d 次", n)
	}
}

// A1.2 反例 @卡号：同会话卡 C 坐 push 席位，人发 @C → 只有 C 的续轮，驻场者零次。对照：同夹具无 @ → 驻场者一次。
func TestB444AtCardWakesSeatNotResident(t *testing.T) {
	env, runner := newB444Env(t)
	card := createCoordCard(t, env)
	b435BindPush(t, env, card, "b444-seat-1")
	session := b435Session(t, env, card)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)

	sendSessionMessage(t, env.srv.rooms, session, "user:tester", "@"+card+" 推一下", []string{card}, 0)
	drainAutomation(t, env)
	refs, prompts := runner.snapshot()
	if len(refs) != 1 || refs[0].SessionID != "b444-seat-1" || !b435WokeCard(prompts[0], card) {
		t.Fatalf("@卡号应只续卡席位一次，得 refs=%+v", refs)
	}
	if n := len(b444ResidentResumes(runner)); n != 0 {
		t.Fatalf("@卡号不得唤醒驻场者，得 %d 次", n)
	}

	sendSessionMessage(t, env.srv.rooms, session, "user:tester", "无 @ 对照", nil, 0)
	drainAutomation(t, env)
	if n := len(b444ResidentResumes(runner)); n != 1 {
		t.Fatalf("对照：无 @ 应唤醒驻场者一次，得 %d", n)
	}
}

// A1.3 反例 desktop 驻场者：旧形态行（driver_mode 空、有 cli_session、本机）→ 零 keystone 调用，门铃照旧按响。
func TestB444DesktopResidentStaysOnDoorbell(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	o2SitResident(t, env, session, o2Resident, o2DesktopSession, o2CLISession, o2Hostname(t))
	home, spy := o2Wire(t, env.srv)
	drainAutomation(t, env)

	sendSessionMessage(t, env.srv.rooms, session, "user:tester", "桌面端驻场者", nil, 0)
	drainAutomation(t, env)
	if refs, _ := runner.snapshot(); len(refs) != 0 {
		t.Fatalf("desktop 驻场者不得经 keystone，得 %+v", refs)
	}
	if n := len(o2Letters(t, home)); n != 1 || len(spy.calls()) != 1 {
		t.Fatalf("desktop 驻场者门铃应照旧：信 %d 封、opener %d 次", n, len(spy.calls()))
	}
}

// A1.4 反例 双重唤醒：push 驻场者 → 门铃零写信、零 opener（跳过原因 push_resident），keystone 恰一次；
// 转交收件端收到一条落驻场者的消息 → 驻场唤醒不进卡回合（woke=false、零 resume）。
func TestB444PushResidentNoDoorbellNoTransferLeak(t *testing.T) {
	env, runner := newB444Env(t)
	card := createCoordCard(t, env)
	seat := b435BindPush(t, env, card, "b444-seat-2")
	session := b435Session(t, env, card)
	b444SitPush(t, env, session, "local")
	home, spy := o2Wire(t, env.srv)
	logs := o2CaptureLog(env.srv)
	drainAutomation(t, env)

	seq := sendSessionMessage(t, env.srv.rooms, session, "user:tester", "给驻场者", nil, 0)
	drainAutomation(t, env)
	if n := len(o2Letters(t, home)); n != 0 || len(spy.calls()) != 0 {
		t.Fatalf("push 驻场者门铃应零写信零 opener，得 信 %d opener %v", n, spy.calls())
	}
	if !strings.Contains(logs.String(), "push_resident") {
		t.Fatalf("门铃跳过原因应记 push_resident；日志:\n%s", logs.String())
	}
	if n := len(b444ResidentResumes(runner)); n != 1 {
		t.Fatalf("对照：push 驻场者应经 keystone 恰一次，得 %d", n)
	}

	events, err := env.srv.autoLedger.EventsFromAsc(nil, seq-1, 10)
	if err != nil || len(events) == 0 || events[0].Seq != seq {
		t.Fatalf("取该消息账本事件: %v %+v", err, events)
	}
	body, _ := json.Marshal(proto.CoordinatorWakeReq{Seat: seat, Events: []proto.LedgerEvent{events[0]}})
	before, _ := runner.snapshot()
	code, resp := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card+"/coordinator/wake", string(body))
	if code != http.StatusOK {
		t.Fatalf("转交收件应 200，得 %d %s", code, resp)
	}
	var got proto.CoordinatorWakeResp
	if err := json.Unmarshal([]byte(resp), &got); err != nil || got.Woke {
		t.Fatalf("驻场唤醒不得进卡回合：应 woke=false，得 %+v err=%v", got, err)
	}
	if after, _ := runner.snapshot(); len(after) != len(before) {
		t.Fatalf("转交收件不得起任何回合: 前 %d 后 %d", len(before), len(after))
	}
}

// A1.5 反例 自唤醒：驻场者本人显式 @ 自己 → 零次 Resume；对照：人类 @ 驻场者 → 一次。
func TestB444ResidentSelfMentionDoesNotWake(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)

	sendSessionMessage(t, env.srv.rooms, session, b444Identity, "@"+b444Identity+" 自言自语", []string{b444Identity}, 0)
	drainAutomation(t, env)
	if n := len(b444ResidentResumes(runner)); n != 0 {
		t.Fatalf("驻场者 @ 自己不得唤醒自己，得 %d 次", n)
	}
	sendSessionMessage(t, env.srv.rooms, session, "user:tester", "@"+b444Identity+" 你在吗", []string{b444Identity}, 0)
	drainAutomation(t, env)
	if n := len(b444ResidentResumes(runner)); n != 1 {
		t.Fatalf("对照：人类 @ 驻场者应唤醒一次，得 %d", n)
	}
}

// A1.6 序列化边界（真实转换链）：账本 push 行 → api.Facade → proto → collab 成员投影 → HTTP
// GET /api/sessions/{id}：驻场者成员 JSON 有 driver_mode=push 与 bearing 键；旧 desktop 行的成员 JSON
// **没有** driver_mode 键（断言键缺失）。
func TestB444SessionDetailResidentDriverKeys(t *testing.T) {
	env, _ := newB444Env(t)
	pushSession := b435Session(t, env)
	b444SitPush(t, env, pushSession, "linux-01")
	deskSession := b435Session(t, env)
	if _, err := env.srv.rooms.RecordResidentRound(deskSession, o2Resident, "stale-push-round", "start", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.RecordResidentRound(deskSession, o2Resident, "stale-push-round", "fail", "stale push failure"); err != nil {
		t.Fatal(err)
	}
	o2SitResident(t, env, deskSession, o2Resident, o2DesktopSession, o2CLISession, "")

	residentMember := func(session string) map[string]json.RawMessage {
		t.Helper()
		code, body := ledgerGet(t, env.testAgentdEnv, "/api/sessions/"+session)
		if code != http.StatusOK {
			t.Fatalf("GET 会话详情 %s: %d %s", session, code, body)
		}
		var detail struct {
			Summary struct {
				Members []map[string]json.RawMessage `json:"members"`
			} `json:"summary"`
		}
		if err := json.Unmarshal([]byte(body), &detail); err != nil {
			t.Fatal(err)
		}
		for _, m := range detail.Summary.Members {
			if string(m["kind"]) == `"resident"` {
				return m
			}
		}
		t.Fatalf("会话 %s 详情无驻场者成员: %s", session, body)
		return nil
	}
	push := residentMember(pushSession)
	for key, want := range map[string]string{
		"driver_mode": `"push"`, "carrier": `"coord-carrier"`, "carrier_machine": `"linux-01"`, "model": `"` + b444Model + `"`,
	} {
		if got := string(push[key]); got != want {
			t.Fatalf("push 驻场成员 %s 应为 %s，得 %q（成员 %v）", key, want, got, push)
		}
	}
	desk := residentMember(deskSession)
	for _, key := range []string{"driver_mode", "carrier", "carrier_machine", "model"} {
		if _, present := desk[key]; present {
			t.Fatalf("旧 desktop 驻场成员不得出现 %s 键（缺失 ≠ 空值），得 %v", key, desk)
		}
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/sessions/"+deskSession)
	var detail proto.SessionDetail
	if code != http.StatusOK || json.Unmarshal([]byte(body), &detail) != nil {
		t.Fatalf("desktop SessionDetail: %d %s", code, body)
	}
	for _, role := range detail.RoleStatus.Roles {
		if role.Kind == "resident" && role.Identity == o2Resident {
			if role.State != "unknown" {
				t.Fatalf("desktop resident has no round lifecycle source and must remain unknown: %+v", role)
			}
			return
		}
	}
	t.Fatalf("desktop resident role absent from role_status: %s", body)
}

// A1.9 失败可见：准入满 → 认领留在飞、水位不越过、会话里系统指针行存在且多轮不刷屏（≤1）；名额恢复后
// 下一轮成功、消息不丢。
func TestB444ResidentAdmissionFullKeepsClaimAndPoints(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)
	svc := mustScheduling(t, env.srv)
	hold, err := svc.AdmitSeatCarrier(b444Squad, "coord-carrier")
	if err != nil {
		t.Fatalf("占满名额: %v", err)
	}

	seq := sendSessionMessage(t, env.srv.rooms, session, "user:tester", "名额满时的消息", nil, 0)
	for i := 0; i < 3; i++ {
		_, _, _ = env.srv.consumeAutomationEventsOnce(context.Background())
	}
	if n := len(b444ResidentResumes(runner)); n != 0 {
		t.Fatalf("准入满不得续轮，得 %d", n)
	}
	if done, ok := b444ClaimRows(t, env, session)[seq]; !ok || done {
		t.Fatalf("准入满认领应留在飞（存在且未收尾），得 ok=%v done=%v", ok, done)
	}
	if c := o2Cursor(env.srv); c >= seq {
		t.Fatalf("准入满水位不得越过 %d，得 %d", seq, c)
	}
	pointers := b444Pointers(t, env, session)
	if len(pointers) == 0 || len(pointers) > 1 {
		t.Fatalf("准入满会话里应恰有系统指针行且不刷屏（1 条），得 %d: %v", len(pointers), pointers)
	}
	if !strings.Contains(pointers[0], b444Identity) {
		t.Fatalf("指针行应点名驻场者 %s: %q", b444Identity, pointers[0])
	}

	if err := svc.Release(hold.Squad, hold.Carrier); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, env)
	if n := len(b444ResidentResumes(runner)); n != 1 {
		t.Fatalf("名额恢复后应续轮一次（消息不丢），得 %d", n)
	}
	if c := o2Cursor(env.srv); c < seq {
		t.Fatalf("恢复后水位应越过 %d，得 %d", seq, c)
	}
}

// A1.9 失败可见（非准入错误）：resume 报错 → 认领收尾 + 退避，会话里恰一条指针行；不落 needs_human、不重建。
func TestB444ResidentResumeFailurePointsOnce(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)
	if _, err := env.srv.rooms.RecordResidentRound(session, b444Identity, "resident-old-fail", "start", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.RecordResidentRound(session, b444Identity, "resident-old-fail", "fail", "older resume error"); err != nil {
		t.Fatal(err)
	}
	runner.setResumeErr(errors.New("b444 载体不可达"))

	seq := sendSessionMessage(t, env.srv.rooms, session, "user:tester", "载体坏了", nil, 0)
	for i := 0; i < 3; i++ {
		_, _, _ = env.srv.consumeAutomationEventsOnce(context.Background())
	}
	if n := len(b444ResidentResumes(runner)); n != 1 {
		t.Fatalf("非准入失败应试跑恰一次后退避，得 %d", n)
	}
	if done := b444ClaimRows(t, env, session)[seq]; !done {
		t.Fatalf("非准入失败应收尾认领（不挡水位）")
	}
	pointers := b444Pointers(t, env, session)
	if len(pointers) != 1 || !strings.Contains(pointers[0], b444Identity) {
		t.Fatalf("应恰一条点名驻场者的指针行，得 %v", pointers)
	}
	if n := o2CountEvents(t, env, ledger.EvNeedsHuman); n != 0 {
		t.Fatalf("驻场唤醒失败不落卡级 needs_human，得 %d", n)
	}
	if refs, _ := runner.snapshot(); len(refs) != 1 {
		t.Fatalf("驻场续轮失败不得重建（Launch），得 resume %d", len(refs))
	}
	assertResidentRoleState(t, env, session, "exception")

	// A distinct later human event opens a new real producer round; successful
	// end must supersede the older failure instead of leaving a stale exception.
	runner.setResumeErr(nil)
	sendSessionMessage(t, env.srv.rooms, session, "user:tester", "恢复后继续", nil, 0)
	drainAutomation(t, env)
	assertResidentRoleState(t, env, session, "idle")
}

// A1.9 归档会话：在途消息在会话归档后 → 零回合、标 seen（水位越过，不活锁）、零认领。
func TestB444ArchivedSessionResidentZeroRound(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)
	seq := sendSessionMessage(t, env.srv.rooms, session, "user:tester", "归档前最后一句", nil, 0)
	if err := env.srv.rooms.ArchiveSession(session, "user:tester"); err != nil {
		t.Fatalf("归档: %v", err)
	}
	drainAutomation(t, env)
	drainAutomation(t, env)
	if n := len(b444ResidentResumes(runner)); n != 0 {
		t.Fatalf("归档会话不得续轮，得 %d", n)
	}
	if c := o2Cursor(env.srv); c < seq {
		t.Fatalf("归档会话的驻场唤醒应标 seen、水位越过 %d，得 %d", seq, c)
	}
	if rows := b444ClaimRows(t, env, session); len(rows) != 0 {
		t.Fatalf("归档会话零认领，得 %v", rows)
	}
}

// R9 多机共库：push 驻场者 bearing 在他机 → 本机零认领、零回合、水位照常越过（每机游标独立）。
func TestB444RemoteResidentNotClaimedLocally(t *testing.T) {
	env, runner := newB444Env(t)
	const other = "b444-other-machine"
	if other == o2Hostname(t) {
		t.Skip("hostname 恰为夹具机器名")
	}
	session := b435Session(t, env)
	b444SitPush(t, env, session, other)
	drainAutomation(t, env)
	seq := sendSessionMessage(t, env.srv.rooms, session, "user:tester", "他机驻场者", nil, 0)
	drainAutomation(t, env)
	if refs, _ := runner.snapshot(); len(refs) != 0 {
		t.Fatalf("他机驻场者本机不得续轮，得 %+v", refs)
	}
	if rows := b444ClaimRows(t, env, session); len(rows) != 0 {
		t.Fatalf("他机驻场者本机不得认领，得 %v", rows)
	}
	if c := o2Cursor(env.srv); c < seq {
		t.Fatalf("本机水位应越过 %d（不挡），得 %d", seq, c)
	}
	if p := b444Pointers(t, env, session); len(p) != 0 {
		t.Fatalf("他机驻场者本机不落指针行，得 %v", p)
	}
}
