// 直通竖切（B156.3 重档法定步骤）：一次真实调用穿过本卡全部空壳——
// 账本（临时 SQLite）→ internal/ledger/api 门面 → schedclient 端口 → 编制域
// 准入与队列 → keystone 唤醒决策与兜底链。写死的结果、真实的接线：断言冻结
// 语义（两级准入、排队顺序、协调者优先清队序、唤醒合并、attach 互斥、兜底
// 降级链），任一回归当场变红。竖切的写死结果不构成子卡的「已有活路径」。
package keystone_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// fakeRunner 记录每次调用并按脚本应答，供断言「唤醒合并为一回合」与兜底链。
type fakeRunner struct {
	launches     []string
	resumes      []string
	failNext     int  // >0 时接下来 N 次 Resume 返回错误
	failLaunches bool // 置真后 Launch 一律失败（重建也不可用的兜底场景）
}

func (f *fakeRunner) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error) {
	if f.failLaunches {
		return keysclient.TurnResult{}, errors.New("拉起不可用")
	}
	f.launches = append(f.launches, prompt)
	return keysclient.TurnResult{SessionID: "sess-new", Output: "verdict: pass"}, nil
}

func (f *fakeRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	if f.failNext > 0 {
		f.failNext--
		return keysclient.TurnResult{}, errors.New("resume 不可用")
	}
	f.resumes = append(f.resumes, prompt)
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "verdict: pass"}, nil
}

// fakeNarrator 收集叙事文本（会话子系统落地前由卡 note 兜底）。
type fakeNarrator struct {
	lines []string
}

func (n *fakeNarrator) Say(cardID, text string) error {
	n.lines = append(n.lines, text)
	return nil
}

// recordingLedger 转发真实门面的读，记录转等人调用。
type recordingLedger struct {
	f        *ledgerapi.Facade
	needs    []string
	needArgs [][2]string
}

func (r *recordingLedger) GetCard(id string) (proto.Card, error) { return r.f.GetCard(id) }
func (r *recordingLedger) EventsFromAsc(ids []string, from int64, limit int) ([]proto.LedgerEvent, error) {
	return r.f.EventsFromAsc(ids, from, limit)
}
func (r *recordingLedger) EffectiveBaseBranch(id string) (string, error) {
	return r.f.EffectiveBaseBranch(id)
}
func (r *recordingLedger) MarkNeedsHuman(cardID, reason, actor string) error {
	r.needs = append(r.needs, cardID)
	r.needArgs = append(r.needArgs, [2]string{reason, actor})
	return r.f.MarkNeedsHuman(cardID, reason, actor)
}

// registryViaFacade 是组装点适配器（facadeAsRegistry）的测试同构：把门面
// 翻译成端口。放在这里是竖切的一部分——接线必须真实穿过账本落盘。
type registryViaFacade struct {
	f *ledgerapi.Facade
}

func (a registryViaFacade) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	return a.f.Put(kind, id, expectVersion, body, actor)
}

func (a registryViaFacade) Get(kind, id string) (schedclient.Record, error) {
	e, err := a.f.Get(kind, id)
	if err != nil {
		if errors.Is(err, ledger.ErrNotFound) {
			return schedclient.Record{}, schedclient.ErrNotFound
		}
		return schedclient.Record{}, err
	}
	return schedclient.Record{ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body}, nil
}

func (a registryViaFacade) List(kind string) ([]schedclient.Record, error) {
	rows, err := a.f.List(kind)
	if err != nil {
		return nil, err
	}
	out := make([]schedclient.Record, 0, len(rows))
	for _, e := range rows {
		out = append(out, schedclient.Record{ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body})
	}
	return out, nil
}

func (a registryViaFacade) Delete(kind, id string, expectVersion int, actor string) error {
	return a.f.Delete(kind, id, expectVersion, actor)
}

// TestIgnitionVerticalSlice 从开编制户口到唤醒回合跑通整条主缝。
func TestIgnitionVerticalSlice(t *testing.T) {
	st, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	defer st.Close()
	facade := ledgerapi.New(st)

	svc := scheduling.New(registryViaFacade{f: facade})

	// ① 编制户口：一个载体（物理位 1）、一个执行者小队（政策位 2）、
	//    一个协调者小队。载体与小队经账本持久化——重启后仍在。
	carrier := scheduling.Carrier{
		Name: "opencode-1", Machine: "linux-01", CLI: "opencode",
		HomeDir: "/home/coordinator/.opencode-home", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1,
	}
	if err := svc.PutCarrier(carrier, 0); err != nil {
		t.Fatalf("登记载体: %v", err)
	}
	if _, err := svc.ApplyDetect(carrier.Name, scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("检测载体: %v", err)
	}
	execSquad := scheduling.Squad{Name: "exec-1", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "opencode-1"}}}
	if err := svc.PutSquad(execSquad, 0); err != nil {
		t.Fatalf("登记执行者小队: %v", err)
	}
	if _, err := svc.Squad("exec-1"); err != nil {
		t.Fatalf("小队未持久化: %v", err)
	}

	// ② 两级准入：小队有位且载体有位才放行；有效三元组 = 覆盖 > 载体缺省。
	binding, err := svc.Admit(scheduling.IgnitionRequest{
		Card: "B300", Squad: "exec-1", Executor: "claude",
	})
	if err != nil {
		t.Fatalf("首次准入被拒: %v", err)
	}
	if binding.Carrier != "opencode-1" || binding.Target != "linux-01" || binding.Executor != "claude" {
		t.Fatalf("绑定解析错误: %+v（期望载体 opencode-1 / 机 linux-01 / 执行者覆盖 claude）", binding)
	}
	if _, err := svc.Admit(scheduling.IgnitionRequest{Card: "B301", Squad: "exec-1"}); !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("载体物理位满员仍放行: %v", err)
	}

	// ③ 排队顺序冻结：就绪度优先 → 卡优先级 → FIFO。低优先级先入队，
	//    高优先级后入队但先出队；重排入队保留原位。
	if pos, err := svc.Enqueue(scheduling.IgnitionRequest{
		Card: "B301", Squad: "exec-1", Node: "implement", Priority: "低", Actor: "t",
	}, scheduling.KindIgnitionQueue); err != nil || pos < 1 {
		t.Fatalf("低优先级入队: pos=%d err=%v", pos, err)
	}
	if pos, err := svc.Enqueue(scheduling.IgnitionRequest{
		Card: "B302", Squad: "exec-1", Node: "implement", Priority: "高", Actor: "t",
	}, scheduling.KindIgnitionQueue); err != nil || pos != 1 {
		t.Fatalf("高优先级应插到队首: pos=%d err=%v", pos, err)
	}

	// ④ 协调者优先是清队顺序：launch_queue 整队排在 ignition_queue 之前。
	if len(scheduling.QueueKinds) != 2 || scheduling.QueueKinds[0] != scheduling.KindLaunchQueue {
		t.Fatalf("清队顺序被改: %v", scheduling.QueueKinds)
	}
	if _, ok, err := svc.PopReady(scheduling.KindLaunchQueue); ok || err != nil {
		t.Fatalf("空拉起队不应有产出: %v %v", ok, err)
	}
	head, ok, err := svc.PopReady(scheduling.KindIgnitionQueue)
	if err != nil || !ok {
		t.Fatalf("出队失败: %v %v", ok, err)
	}
	if head.Card != "B302" {
		t.Fatalf("出队顺序破坏冻结语义: 头部 %s，应为 B302（就绪度→优先级→FIFO）", head.Card)
	}

	// ⑤ keystone：唤醒决策表 + 同卡合并 + attach 互斥 + 兜底降级链。
	// 先落一张真卡，让开场简报的读账路径穿过真实账本（以 ledger 为准不信记忆）。
	if _, err := st.PutWorkflow("slice", ledger.WorkflowDef{States: []string{"进行中", "已完成"}}); err != nil {
		t.Fatalf("建工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "竖切样卡", Project: "handoff", Workflow: "slice", BaseBranch: "claude/kai-156-3-ba-df7357", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	runner := &fakeRunner{}
	narrator := &fakeNarrator{}
	view := &recordingLedger{f: facade}
	ks := keystone.New(runner, narrator, view, nil)

	if d := ks.Decide(keystone.WakeEvent{Kind: "progress", Card: card.ID}); d.Wake {
		t.Fatalf("progress 类事件不应唤醒: %+v", d)
	}
	if d := ks.Decide(keystone.WakeEvent{Kind: keystone.WakeTaskTerminal, Card: card.ID}); !d.Wake {
		t.Fatalf("task 终态事件应唤醒: %+v", d)
	}

	// 开卡即绑：拉起并绑定（两入口之一），随后同卡两个积压事件合并成一回合。
	ctx := context.Background()
	opened, err := ks.LaunchForCard(ctx, card.ID, "card_create", keysclient.SessionSpec{CLI: "opencode"})
	if err != nil || opened.Rebuilt || opened.SessionID != "sess-new" {
		t.Fatalf("开卡拉起失败: %+v err=%v（首次拉起 Rebuilt 应为 false）", opened, err)
	}
	if len(runner.launches) != 1 {
		t.Fatalf("拉起应恰好一次 Launch，实际 %d", len(runner.launches))
	}
	wake, err := ks.Wake(ctx, card.ID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: card.ID, Summary: "contract 到 waiting_review"},
		{Kind: keystone.WakeTicket, Card: card.ID, Summary: "go build 权限请求"},
	}, keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("唤醒回合失败: %v", err)
	}
	if wake.SessionID != "sess-new" || len(wake.Output) == 0 {
		t.Fatalf("回合回执不完整: %+v", wake)
	}
	if n := len(runner.resumes); n != 1 {
		t.Fatalf("同卡积压事件必须合并为一回合：Resume 次数 %d ≠ 1", n)
	}
	for _, want := range []string{card.ID, "contract 到 waiting_review", "go build 权限请求", "有效基线分支"} {
		if !strings.Contains(runner.resumes[0], want) {
			t.Fatalf("开场简报缺 %q：\n%s", want, runner.resumes[0])
		}
	}

	// attach 与自动唤醒互斥：接管中 Decide 拒醒，解除后恢复。
	ks.SetAttach(card.ID, true)
	if d := ks.Decide(keystone.WakeEvent{Kind: keystone.WakeTaskTerminal, Card: card.ID}); d.Wake {
		t.Fatalf("人工接管中不应自动唤醒")
	}
	ks.SetAttach(card.ID, false)

	// 兜底降级链第一段：resume 失败 → 换载体重建（房间落「载体已更换」指针）。
	runner.failNext = 99
	rebuilt, wakeErr := ks.Wake(ctx, card.ID, []keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: card.ID}}, keysclient.SessionSpec{CLI: "opencode"})
	if wakeErr != nil {
		t.Fatalf("resume 失败后重建应成功: %v", wakeErr)
	}
	if !rebuilt.Rebuilt || rebuilt.SessionID != "sess-new" {
		t.Fatalf("重建回执不完整: %+v", rebuilt)
	}
	if len(narrator.lines) == 0 || !strings.Contains(narrator.lines[len(narrator.lines)-1], "载体已更换") {
		t.Fatalf("重建未落「载体已更换」指针: %v", narrator.lines)
	}
	if len(view.needs) != 0 {
		t.Fatalf("重建成功不应转等人: %v", view.needs)
	}

	// 兜底降级链终点：重建也失败 → 转等人 + 看板亮。
	runner.failLaunches = true
	if _, err := ks.Wake(ctx, card.ID, []keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: card.ID}}, keysclient.SessionSpec{CLI: "opencode"}); err == nil {
		t.Fatalf("resume 与重建均不可用时唤醒应报错")
	}
	if len(view.needs) == 0 || view.needs[0] != card.ID {
		t.Fatalf("兜底链终点未转等人: %v", view.needs)
	}

	// 铁律断言（spec 测试接缝 3 的竖切半边）：协调者会话承载缝只有 Launch/
	// Resume 两个动作——类型系统上不存在派发入口，本断言钉住该形状不被扩张。
	var _ interface {
		Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error)
		Resume(keysclient.SessionRef, string) (keysclient.TurnResult, error)
	} = runner
}
