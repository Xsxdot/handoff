// permission_authority_seam_test.go —— B233.21 缝级测试：非 OpenCode 权限
// 三出口（AutoAllow / consult / escalate）的决策、重放幂等与决策复用必须出自
// internal/approval 权威；Manager 只按权威结论落账本事实并做原生回传。
//
// 手法：用桩权威（stubAuthority）替换 Manager.permAuthority，桩记录每次权威
// 调用并注入受控结论。若 Manager 本地还残留任何决策/处置实现体（judge、
// autoallow、重放幂等、复用），对应测试要么因调用序列不符、要么因账本痕迹
// 不符而红——变异可杀。
package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// stubAuthority 记录权威调用序列并按用例注入受控结论。
type stubAuthority struct {
	st *store.Store

	calls []string

	replay     bool
	hasAuto    bool
	verdict    permgate.Verdict
	reuseHit   bool
	reusePrior *proto.Ticket
	reuseFP    string

	// reuseObservedState 记录 FindReuse 被调那一刻的任务状态，
	// 锁 P1-2：复用判定早于任何状态迁移。
	reuseObservedState proto.TaskState
}

func (s *stubAuthority) record(call string) { s.calls = append(s.calls, call) }

func (s *stubAuthority) IsReplay(taskID, permID, ticketID string) bool {
	s.record("IsReplay")
	return s.replay
}

func (s *stubAuthority) HasAutoAllow(taskID, permID string) bool {
	s.record("HasAutoAllow")
	return s.hasAuto
}

func (s *stubAuthority) Judge(taskID string, ev executor.AdapterEvent) permgate.Verdict {
	s.record("Judge")
	return s.verdict
}

func (s *stubAuthority) AutoAllow(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict) {
	s.record("AutoAllow")
}

func (s *stubAuthority) FindReuse(taskID, ticketID string, ev executor.AdapterEvent) (*proto.Ticket, string, bool) {
	s.record("FindReuse")
	if s.st != nil {
		if cur, err := s.st.GetTask(taskID); err == nil {
			s.reuseObservedState = cur.State
		}
	}
	if !s.reuseHit {
		return nil, executor.PermFingerprint(ev), false
	}
	return s.reusePrior, s.reuseFP, true
}

// TestManagerBindsApprovalAuthority 生产装配必须绑定 approval.Authority——
// 编排侧接缝的实现体（三出口决策、重放幂等、复用判定）只有一个权威。
func TestManagerBindsApprovalAuthority(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	if _, ok := m.permAuthority.(*approval.Authority); !ok {
		t.Fatalf("NewManager 必须装配 approval.Authority，实得 %T", m.permAuthority)
	}
}

// TestPermissionAutoAllowExitComesFromAuthority AutoAllow 出口：判据与处置
// 都经权威。桩的 AutoAllow 不做任何事——若 Manager 本地还残留 autoallow
// 实现体（审计+回传），executor 会收到 once 或审计事件会凭空出现，测试红。
// 冻结 #14 的调用面（只有本分支到达处置）由调用序列断言锁定。
func TestPermissionAutoAllowExitComesFromAuthority(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "aa-authority"
	createRunningTask(t, st, taskID)
	stub := &stubAuthority{
		verdict: permgate.Verdict{Action: permgate.AutoAllow,
			Rule: permgate.RuleSafeCommand, Reason: "stub verdict"},
	}
	m.permAuthority = stub

	m.handlePermission(context.Background(), taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "perm-1", Text: "Bash: ls",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "ls"},
	})

	want := []string{"IsReplay", "Judge", "AutoAllow"}
	if len(stub.calls) != len(want) {
		t.Fatalf("权威调用序列 = %v, want %v", stub.calls, want)
	}
	for i := range want {
		if stub.calls[i] != want[i] {
			t.Fatalf("权威调用序列 = %v, want %v", stub.calls, want)
		}
	}
	// 处置出自权威：桩不落账本、不回传，Manager 本地不得补刀。
	if got := adapter.recordedPerms(); len(got) != 0 {
		t.Fatalf("Manager 本地不得残留 AutoAllow 回传路径: %v", got)
	}
	if evs := mustEvents(t, st, taskID); len(evs) != 0 {
		t.Fatalf("Manager 本地不得残留 AutoAllow 审计路径: %v", evs)
	}
	if _, err := st.GetTicket(taskID + ":perm-1"); !isErrNotFound(err) {
		t.Fatalf("AutoAllow 出口不得建工单（三不），err=%v", err)
	}
	cur, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("AutoAllow 出口不得改状态（三不），实得 %s", cur.State)
	}
}

// TestPermissionEscalateExitUsesAuthorityReuseBeforeTransition escalate 出口：
// 复用判定经权威、且早于任何状态迁移（P1-2——stub 在 FindReuse 时抓拍状态），
// 之后 Manager 才落 waiting_answer → 工单 → permission_request 事件。
func TestPermissionEscalateExitUsesAuthorityReuseBeforeTransition(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "esc-authority"
	createRunningTask(t, st, taskID)
	stub := &stubAuthority{
		st:      st,
		verdict: permgate.Verdict{Action: permgate.Escalate, Reason: "stub escalate"},
	}
	m.permAuthority = stub

	m.handlePermission(context.Background(), taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "perm-1", Text: "x",
		Perm: &executor.PermRequest{Tool: executor.PermToolOther},
	})

	for i, call := range []string{"IsReplay", "Judge", "FindReuse"} {
		if stub.calls[i] != call {
			t.Fatalf("权威调用序列 = %v, want 前缀 %v", stub.calls, []string{"IsReplay", "Judge", "FindReuse"})
		}
	}
	if stub.reuseObservedState != proto.TaskStateRunning {
		t.Fatalf("复用判定必须早于状态迁移（P1-2），FindReuse 时状态 = %s", stub.reuseObservedState)
	}
	cur, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("escalate 后应 waiting_answer，实得 %s", cur.State)
	}
	tk := mustGetTicket(t, st, taskID+":perm-1")
	if tk.Answer != nil {
		t.Fatalf("挂起工单不应被应答: %+v", tk)
	}
	if !hasEvent(mustEvents(t, st, taskID), proto.EventTypePermissionRequest) {
		t.Fatal("escalate 后应落 permission_request 事件")
	}
	if got := adapter.recordedPerms(); len(got) != 0 {
		t.Fatalf("escalate 出口不回传 executor，实得 %v", got)
	}
}

// TestPermissionReuseExitComesFromAuthority 复用出口：命中与否由权威判定；
// Manager 按命中结论落 permission_reuse 事件 + 自动放行（建单答题、回传一次、
// 标记送达），全程不动状态机。
func TestPermissionReuseExitComesFromAuthority(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "reuse-authority"
	createRunningTask(t, st, taskID)
	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "perm-1", Text: "run_command: git status",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "git status"},
	}
	fp := executor.PermFingerprint(ev)
	stub := &stubAuthority{
		verdict:    permgate.Verdict{Action: permgate.Escalate, Reason: "stub escalate"},
		reuseHit:   true,
		reusePrior: &proto.Ticket{ID: taskID + ":prior"},
		reuseFP:    fp,
	}
	m.permAuthority = stub

	m.handlePermission(context.Background(), taskID, ev)

	if !hasCall(stub.calls, "FindReuse") {
		t.Fatalf("复用判定必须经权威，调用序列 = %v", stub.calls)
	}
	// 账本事实 1：permission_reuse 审计事件带 prior 工单与指纹前缀
	var reuse permissionReusePayload
	found := false
	for _, e := range mustEvents(t, st, taskID) {
		if e.Type != proto.EventTypePermissionReuse {
			continue
		}
		if err := json.Unmarshal(e.Payload, &reuse); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("命中复用必须落 permission_reuse 审计事件")
	}
	if reuse.PriorTicketID != taskID+":prior" || reuse.Fingerprint != fp[:8] {
		t.Fatalf("permission_reuse payload = %+v, want prior=%s fp8=%s",
			reuse, taskID+":prior", fp[:8])
	}
	// 账本事实 2：工单按命名空间 id 建单、精确 allow、已送达
	tk := mustGetTicket(t, st, taskID+":perm-1")
	if tk.Answer == nil || *tk.Answer != "allow" || tk.DeliveredAt == nil {
		t.Fatalf("复用放行应建单答题并标记送达: %+v", tk)
	}
	// 原生回传用裸 permID（P1-6 回程契约）
	if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "perm-1:once" {
		t.Fatalf("复用放行应回传一次 once: %v", got)
	}
	// 全程不动状态机
	cur, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("复用放行不得把任务挂起，实得 %s", cur.State)
	}
}

// TestPermissionReplayIdempotenceViaAuthority 重放幂等出自权威：
// IsReplay 判真后跳过全部中介，回传与否取决于权威的 AutoAllow 审计标记。
// case A：审计在 → 仍回传一次 once（agy 双 hook 契约）；case B：审计不在
// （如已答工单外的纯重放）→ 吞掉不回传。
func TestPermissionReplayIdempotenceViaAuthority(t *testing.T) {
	t.Run("audit 标记在则回传 once", func(t *testing.T) {
		m, st, _, adapter := newTestManager(t)
		taskID := "replay-once"
		createRunningTask(t, st, taskID)
		stub := &stubAuthority{replay: true, hasAuto: true}
		m.permAuthority = stub
		m.handlePermission(context.Background(), taskID, executor.AdapterEvent{
			Type: "permission", PermissionID: "perm-1", Text: "x",
			Perm: &executor.PermRequest{Tool: executor.PermToolOther},
		})
		want := []string{"IsReplay", "HasAutoAllow"}
		if len(stub.calls) != 2 || stub.calls[0] != want[0] || stub.calls[1] != want[1] {
			t.Fatalf("重放路径权威调用 = %v, want %v", stub.calls, want)
		}
		if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "perm-1:once" {
			t.Fatalf("AutoAllow 无工单重放仍须回传 once: %v", got)
		}
		if evs := mustEvents(t, st, taskID); len(evs) != 0 {
			t.Fatalf("重放不得补发任何事件: %v", evs)
		}
	})
	t.Run("audit 标记不在则吞掉", func(t *testing.T) {
		m, st, _, adapter := newTestManager(t)
		taskID := "replay-swallow"
		createRunningTask(t, st, taskID)
		stub := &stubAuthority{replay: true, hasAuto: false}
		m.permAuthority = stub
		m.handlePermission(context.Background(), taskID, executor.AdapterEvent{
			Type: "permission", PermissionID: "perm-1", Text: "x",
			Perm: &executor.PermRequest{Tool: executor.PermToolOther},
		})
		if got := adapter.recordedPerms(); len(got) != 0 {
			t.Fatalf("非 AutoAllow 重放不得回传: %v", got)
		}
		cur, err := st.GetTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.State != proto.TaskStateRunning {
			t.Fatalf("重放不得触发状态迁移，实得 %s", cur.State)
		}
	})
}

// TestPermissionConsultExitKeepsManagerLedger consult 出口（spec §范围 2 降级
// 阀的残余面）：分流决策与账本机制同在 Manager——判据出 Consult 后，若审批者
// 可用则登记 inflight 并异步裁决（既有 approver 集成测试覆盖），不可用则退化
// 升级人工。本测试锁「判据 Consult + 审批者缺席 → 完整走 escalate 账本」，
// 即 consult 分流没有第二条权威。
func TestPermissionConsultExitKeepsManagerLedger(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	taskID := "consult-authority"
	createRunningTask(t, st, taskID)
	stub := &stubAuthority{
		verdict: permgate.Verdict{Action: permgate.Consult, Reason: "stub consult"},
	}
	m.permAuthority = stub

	m.handlePermission(context.Background(), taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "perm-1", Text: "x",
		Perm: &executor.PermRequest{Tool: executor.PermToolOther},
	})
	if !hasCall(stub.calls, "Judge") {
		t.Fatalf("判据必须经权威，调用序列 = %v", stub.calls)
	}
	cur, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("审批者缺席时 consult 退化为升级人工，实得 %s", cur.State)
	}
	mustGetTicket(t, st, taskID+":perm-1")
}

func hasCall(calls []string, name string) bool {
	for _, c := range calls {
		if c == name {
			return true
		}
	}
	return false
}

func isErrNotFound(err error) bool {
	return err != nil && err == store.ErrNotFound
}
