// authority_test.go —— B233.21 非 OpenCode 决策权威的缝级测试。
//
// 职责：证明 Authority（重放幂等 / Judge / FindReuse / AutoAllow 处置）在
// 真实 SQLite Store 上产出与迁移前 Manager 内联实现一致的结论——
// fail-closed 三连、判据出口、审计幂等标记、复用命中与 fail-closed、
// AutoAllow「先审计后回传一次、不碰账本」的处置顺序。
//
// 边界：只打权威公开缝（approval_test 外部测试包），不碰编排侧账本；
// 三出口「决策出自权威」的编排侧装配由 orchestration 的缝级测试锁。
package approval_test

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// newAuthorityFixture 开真实 Store、落一个 running 任务并构造权威。
// 返回 DataDir 供 safe-command 域构造判据输入。
func newAuthorityFixture(t *testing.T, taskID, workdir string) (*store.Store, *approval.Authority, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{
		ID: taskID, RepoPath: workdir, WorkDir: workdir,
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	dataDir := t.TempDir()
	g, err := permgate.New(nil, slog.Default())
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := approval.NewAuthority(approval.AuthorityHooks{
		Log:     logger,
		Store:   st,
		Gate:    g,
		DataDir: dataDir,
		AuditAutoAllow: func(string, executor.AdapterEvent, permgate.Verdict) {
			t.Error("本夹具的用例不应触发 AutoAllow 审计回调")
		},
		DeliverAutoAllow: func(string, string) {
			t.Error("本夹具的用例不应触发 AutoAllow 回传回调")
		},
	})
	return st, a, dataDir
}

func TestAuthorityJudgeNilGateEscalates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := approval.NewAuthority(approval.AuthorityHooks{Log: logger, Store: st})
	v := a.Judge("t1", executor.AdapterEvent{PermissionID: "p1", Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{"main.go"}}})
	if v.Action != permgate.Escalate {
		t.Fatalf("Gate 缺失必须 fail-closed 升级，实得 %s（%s）", v.Action, v.Reason)
	}
}

func TestAuthorityJudgeNilPermEscalates(t *testing.T) {
	_, a, _ := newAuthorityFixture(t, "auth-task", t.TempDir())
	v := a.Judge("auth-task", executor.AdapterEvent{
		PermissionID: "p1", Text: "Write: /etc/hosts"})
	if v.Action != permgate.Escalate {
		t.Fatalf("Perm 缺失必须升级人工，实得 %s（%s）", v.Action, v.Reason)
	}
}

func TestAuthorityJudgeUnknownTaskEscalates(t *testing.T) {
	_, a, _ := newAuthorityFixture(t, "auth-task", t.TempDir())
	v := a.Judge("no-such-task", executor.AdapterEvent{
		PermissionID: "p1", Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{"main.go"}}})
	if v.Action != permgate.Escalate {
		t.Fatalf("任务读不到时范围不可知，必须升级，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestAuthorityJudgeVerdicts 锁判据权威的两个静态出口（Consult 出口的路由
// 由审批链是否可用决定，归编排侧分流，权威只产出判据结论）：
// 工作区内写 → AutoAllow；越界写 → Escalate；task tmp 域安全命令 → AutoAllow
// 且 Rule=RuleSafeCommand。
func TestAuthorityJudgeVerdicts(t *testing.T) {
	work := t.TempDir()
	_, a, dataDir := newAuthorityFixture(t, "auth-verdicts", work)
	taskID := "auth-verdicts"

	write := func(paths ...string) executor.AdapterEvent {
		return executor.AdapterEvent{Type: "permission", PermissionID: "p-write",
			Text: "Write: main.go",
			Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: paths}}
	}
	v := a.Judge(taskID, write(filepath.Join(work, "main.go")))
	if v.Action != permgate.AutoAllow {
		t.Fatalf("工作区内写入应自动放行，实得 %s（%s）", v.Action, v.Reason)
	}
	v = a.Judge(taskID, write("/etc/hosts"))
	if v.Action != permgate.Escalate {
		t.Fatalf("越界写必须升级人工，实得 %s（%s）", v.Action, v.Reason)
	}
	tmpDir := executor.TaskTmpDir(dataDir, taskID)
	cmd := "go test ./... > " + filepath.Join(tmpDir, "out")
	v = a.Judge(taskID, executor.AdapterEvent{Type: "permission", PermissionID: "p-cmd",
		Text: "Bash: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd}})
	if v.Action != permgate.AutoAllow || v.Rule != permgate.RuleSafeCommand {
		t.Fatalf("task tmp safe command verdict = %#v, want AutoAllow safe-command", v)
	}
}

func TestEscalateLogLevel(t *testing.T) {
	cases := []struct {
		rule string
		want slog.Level
	}{
		{"", slog.LevelWarn},                       // 越界写/结构缺失：本该静默却被拦
		{permgate.RuleSelfCommand, slog.LevelWarn}, // 自指令（B115）
		{"sudo_*", slog.LevelInfo},                 // 黑名单命中：改动前后都会被拦
	}
	for _, c := range cases {
		if got := approval.EscalateLogLevel(c.rule); got != c.want {
			t.Fatalf("EscalateLogLevel(%q) = %v，期望 %v", c.rule, got, c.want)
		}
	}
}

func TestAuthorityHasAutoAllow(t *testing.T) {
	st, a, _ := newAuthorityFixture(t, "auth-auto", t.TempDir())
	taskID := "auth-auto"
	if a.HasAutoAllow(taskID, "step_1") {
		t.Fatal("无审计事件时不得判定 AutoAllow 重放")
	}
	if _, err := st.AppendEvent(taskID, proto.EventTypePermissionAutoAllow, map[string]any{
		"permission_id": "step_1",
	}); err != nil {
		t.Fatal(err)
	}
	if !a.HasAutoAllow(taskID, "step_1") {
		t.Fatal("审计事件在即应判定 AutoAllow 已放行")
	}
	if a.HasAutoAllow(taskID, "step_2") {
		t.Fatal("不同 permission_id 不得命中")
	}
}

func TestAuthorityIsReplayRules(t *testing.T) {
	st, a, _ := newAuthorityFixture(t, "auth-replay", t.TempDir())
	taskID := "auth-replay"

	// 规则 1：无工单无审计 → 新请求
	if a.IsReplay(taskID, "p1", taskID+":p1") {
		t.Fatal("无工单无审计是新请求，不是重放")
	}
	// 规则 2：审计事件在（AutoAllow 无工单路径）→ 重放
	if _, err := st.AppendEvent(taskID, proto.EventTypePermissionAutoAllow, map[string]any{
		"permission_id": "p-audit",
	}); err != nil {
		t.Fatal(err)
	}
	if !a.IsReplay(taskID, "p-audit", taskID+":p-audit") {
		t.Fatal("AutoAllow 审计在，重放必须判真（幂等标记）")
	}
	// 规则 3：工单已应答 → 重放
	if _, err := st.CreateTicket(&proto.Ticket{ID: taskID + ":p-answered", TaskID: taskID,
		Kind: "gate", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AnswerTicket(taskID+":p-answered", "allow"); err != nil {
		t.Fatal(err)
	}
	if !a.IsReplay(taskID, "p-answered", taskID+":p-answered") {
		t.Fatal("已答工单重放必须判真（再动一次就是重复交付）")
	}
	// 规则 4：工单在、事件也在、未应答 → 重放（P1-7 幂等承诺）
	if _, err := st.CreateTicket(&proto.Ticket{ID: taskID + ":p-full", TaskID: taskID,
		Kind: "gate", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(taskID, proto.EventTypePermissionRequest, map[string]any{
		"ticket_id": taskID + ":p-full", "kind": "gate",
	}); err != nil {
		t.Fatal(err)
	}
	if !a.IsReplay(taskID, "p-full", taskID+":p-full") {
		t.Fatal("工单与事件俱全的重放必须判真")
	}
	// 规则 5：工单在、事件缺失 → **不是**重放（N-4 自愈补发）
	if _, err := st.CreateTicket(&proto.Ticket{ID: taskID + ":p-half", TaskID: taskID,
		Kind: "gate", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if a.IsReplay(taskID, "p-half", taskID+":p-half") {
		t.Fatal("半截状态必须放行补发，判重放会让协调者永远等不到唤醒")
	}
}

func TestAuthorityFindReuseMissThenHit(t *testing.T) {
	st, a, _ := newAuthorityFixture(t, "auth-reuse", t.TempDir())
	taskID := "auth-reuse"
	ev := executor.AdapterEvent{Type: "permission", PermissionID: "p1",
		Text: "run_command: git status",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "git status"}}
	fp := executor.PermFingerprint(ev)
	if fp == "" {
		t.Fatal("指纹不应为空")
	}
	if _, _, hit := a.FindReuse(taskID, taskID+":p1", ev); hit {
		t.Fatal("无既有批准时不得命中复用")
	}
	// 播种一张已送达的 allow gate 工单（复用面只认 answer=allow 且已送达）
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: taskID + ":prior", TaskID: taskID, Kind: "gate",
		CreatedAt: time.Now().UTC(), Fingerprint: fp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AnswerTicket(taskID+":prior", "allow"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTicketDelivered(taskID + ":prior"); err != nil {
		t.Fatal(err)
	}
	prior, gotFP, hit := a.FindReuse(taskID, taskID+":p1", ev)
	if !hit || prior == nil || prior.ID != taskID+":prior" || gotFP != fp {
		t.Fatalf("已送达 allow 工单应命中复用：hit=%v prior=%+v fp=%q", hit, prior, gotFP)
	}
}

func TestAuthorityFindReuseClosedStoreFailsClosed(t *testing.T) {
	st, a, _ := newAuthorityFixture(t, "auth-reuse-closed", t.TempDir())
	workdir := t.TempDir()
	if err := st.CreateTask(&proto.Task{ID: "second", RepoPath: workdir, WorkDir: workdir,
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close() // 查询必失败
	ev := executor.AdapterEvent{Type: "permission", PermissionID: "p1", Text: "x",
		Perm: &executor.PermRequest{Tool: executor.PermToolOther}}
	if _, _, hit := a.FindReuse("second", "second:p1", ev); hit {
		t.Fatal("查询失败必须 fail-closed 到「照常问人」，错误复用是安全事故")
	}
}

// TestAuthorityAutoAllowSequencesAuditBeforeDeliver 锁 AutoAllow 出口处置：
// 先审计、再恰好一次回传，且权威自身不落任何工单/事件/状态（三不）。
func TestAuthorityAutoAllowSequencesAuditBeforeDeliver(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	taskID := "auth-aa"
	if err := st.CreateTask(&proto.Task{ID: taskID, RepoPath: t.TempDir(),
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var order []string
	aa := approval.NewAuthority(approval.AuthorityHooks{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:   st,
		DataDir: t.TempDir(),
		AuditAutoAllow: func(id string, ev executor.AdapterEvent, verdict permgate.Verdict) {
			order = append(order, "audit:"+ev.PermissionID)
		},
		DeliverAutoAllow: func(id, permID string) {
			order = append(order, "deliver:"+permID)
		},
	})
	ev := executor.AdapterEvent{Type: "permission", PermissionID: "step_9", Text: "Bash: ls",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "ls"}}
	aa.AutoAllow(taskID, ev, permgate.Verdict{Action: permgate.AutoAllow, Rule: permgate.RuleSafeCommand})
	if len(order) != 2 || order[0] != "audit:step_9" || order[1] != "deliver:step_9" {
		t.Fatalf("AutoAllow 处置顺序 = %v，want [audit deliver] 各一次", order)
	}
	// 三不：无工单、无事件（任务无任何落库痕迹）、状态不动
	if evs, _ := st.EventsFrom(taskID, 0, 100); len(evs) != 0 {
		t.Fatalf("AutoAllow 处置不得落任何事件: %v", evs)
	}
	if pend, _ := st.PendingTickets(taskID); len(pend) != 0 {
		t.Fatalf("AutoAllow 处置不得建工单: %v", pend)
	}
	cur, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("AutoAllow 处置不得改状态，实得 %s", cur.State)
	}
}
