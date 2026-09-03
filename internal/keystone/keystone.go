// Package keystone 是 keystone 域：协调者的拉起、唤醒决策、attach 定位与
// 人工接管互斥。本域不拥有任何持久数据（接管态随 agentd 进程内存活，见
// SetAttach 注释），只做编排决策；持久化与叙事经 keysclient 端口由组装点
// 绑定（B156.3 spec §7.0/§7.1）。
package keystone

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

// WakeKind 是唤醒事件种类。progress / 心跳类事件不在清单里 = 不唤醒
// （spec §5.3 蓝图过滤规则保持）。
type WakeKind string

const (
	WakeTaskTerminal WakeKind = "task_terminal" // 挂账 task 到回合终态（waiting_review / needs_human / turn_failed / failed）
	WakeTicket       WakeKind = "ticket"        // 工单（permission_request / question）
	WakeQueueRelease WakeKind = "queue_release" // 排队出队，醒来确认基线仍新鲜再真派
	WakeMessage      WakeKind = "message"       // 房间用户留言 / @（B156.2 消息通道落地后接线）
)

// WakeEvent 是一条待决策的唤醒事件。
type WakeEvent struct {
	Kind    WakeKind
	Card    string
	Summary string // 一行人读得懂的事件摘要，进开场简报
}

// Decision 是唤醒决策结论。
type Decision struct {
	Wake   bool
	Reason string // 不唤醒时必须给出原因；唤醒时可为空
}

// RoundResult 是一次唤醒回合的回执。
type RoundResult struct {
	Woke      bool   // 是否真的跑了协调者回合
	SessionID string // 本回合实际生效的会话 id
	Rebuilt   bool   // 是否走了重建四步（resume 失败后换载体承接同一身份）
	Escalated bool   // 是否转等人（重建也失败）
	Output    string // 回合输出原文
}

// Service 是 keystone 域的编排本体。
type Service struct {
	mu       sync.Mutex
	takeover map[string]bool // 卡号 → 人工接管中（attach 与自动唤醒互斥）
	runner   keysclient.Runner
	locator  keysclient.TerminalLocator
	narrator keysclient.Narrator
	ledger   keysclient.LedgerView
	sessions map[string]keysclient.SessionRef // 卡号 → 绑定的协调者会话引用
}

// New 组装四条出站端口。locator 允许 nil（attach 入口未装配时定位报错而非崩溃）。
func New(runner keysclient.Runner, narrator keysclient.Narrator, ledger keysclient.LedgerView, locator keysclient.TerminalLocator) *Service {
	return &Service{
		takeover: map[string]bool{},
		sessions: map[string]keysclient.SessionRef{},
		runner:   runner,
		narrator: narrator,
		ledger:   ledger,
		locator:  locator,
	}
}

// Decide 判定一条事件该不该敲醒绑定协调者。同卡积压合并不在这里——那是
// Wake 对事件批次的责任（一回合消费全部积压）。
func (s *Service) Decide(ev WakeEvent) Decision {
	switch ev.Kind {
	case WakeTaskTerminal, WakeTicket, WakeQueueRelease, WakeMessage:
		if s.AttachActive(ev.Card) {
			return Decision{Wake: false, Reason: "人工接管中：attach 会话正驾驶这张卡，自动唤醒让位"}
		}
		return Decision{Wake: true}
	default:
		return Decision{Wake: false, Reason: fmt.Sprintf("事件 %q 不在唤醒清单（progress/心跳类不唤醒）", ev.Kind)}
	}
}

// Wake 跑一个唤醒回合：同卡积压事件合并为一回合，先读账对齐现场（以 ledger
// 为准不信记忆），再为 coordinate 席位无头续会话送入开场简报。bind 席位属于
// 当前终端，不拥有可供 agentd 续接的会话，因此必须 no-op；空座和非法旧值也
// 不能被误判成可拉起的新机器人。resume 失败才进入重建四步，最终失败转等人。
//
// spec 由组装点解析（小队 → LaunchAdmit → 载体）。它只补齐账本身份没有持久化
// 的承载环境，不决定席位来源。
func (s *Service) Wake(ctx context.Context, card string, evs []WakeEvent, spec keysclient.SessionSpec) (RoundResult, error) {
	var zero RoundResult
	if len(evs) == 0 {
		return zero, errors.New("keystone: 唤醒回合没有事件")
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].Kind < evs[j].Kind })
	if s.ledger == nil {
		return zero, errors.New("keystone: 唤醒无法读取账本席位")
	}
	seat, err := s.ledger.GetCard(card)
	if err != nil {
		return zero, fmt.Errorf("keystone: 读取卡 %s 席位: %w", card, err)
	}
	if seat.DriverSession == "" && seat.DriverSource == "" {
		s.Forget(card)
		return zero, nil
	}
	if seat.DriverSource == string(proto.SeatSourceBind) {
		s.Forget(card)
		return zero, nil
	}
	if err := proto.ValidateSeat(seat.DriverSession, proto.SeatSource(seat.DriverSource)); err != nil {
		s.Forget(card)
		return zero, fmt.Errorf("keystone: 卡 %s 席位非法: %w", card, err)
	}
	cli, sessionID, err := proto.ParseSeatIdentity(seat.DriverSession)
	if err != nil {
		s.Forget(card)
		return zero, fmt.Errorf("keystone: 解析卡 %s 席位: %w", card, err)
	}
	s.mu.Lock()
	ref, ok := s.sessions[card]
	s.mu.Unlock()
	ref.CLI = cli
	ref.SessionID = sessionID
	ref = overlayResumeRef(ref, spec)
	s.mu.Lock()
	s.sessions[card] = ref
	s.mu.Unlock()
	prompt := s.briefing(card, evs)
	if !ok {
		result, resumeErr := s.runner.Resume(ref, prompt)
		if resumeErr == nil {
			return RoundResult{Woke: true, SessionID: result.SessionID, Output: result.Output}, nil
		}
		return s.rebuildAfterResumeFailure(card, prompt, spec, ref, resumeErr)
	}
	result, err := s.runner.Resume(ref, prompt)
	if err == nil {
		return RoundResult{Woke: true, SessionID: result.SessionID, Output: result.Output}, nil
	}
	return s.rebuildAfterResumeFailure(card, prompt, spec, ref, err)
}

func (s *Service) rebuildAfterResumeFailure(card, prompt string, spec keysclient.SessionSpec, ref keysclient.SessionRef, resumeErr error) (RoundResult, error) {
	rebuildSpec := spec
	rebuildSpec.CLI = ref.CLI
	rebuilt, launchErr := s.launchRound(card, prompt, rebuildSpec, true)
	if launchErr != nil {
		_ = s.ledger.MarkNeedsHuman(card, "协调者唤醒失败：resume 与重建均不可用", "keystone")
		return RoundResult{Escalated: true}, fmt.Errorf("resume: %v; 重建: %w", resumeErr, launchErr)
	}
	rebuilt.Rebuilt = true
	return rebuilt, nil
}

// LaunchForCard 仅承接 coordinate 来源的机器人新会话。席位由 agentd 组装点在
// 回合成功后通过账本 CAS 写入；bind 当前终端不能进入此方法。
func (s *Service) LaunchForCard(ctx context.Context, card, source string, spec keysclient.SessionSpec) (RoundResult, error) {
	if source != string(proto.SeatSourceCoordinate) {
		return RoundResult{}, fmt.Errorf("拉起协调者来源必须是 coordinate，收到 %q", source)
	}
	result, err := s.launchRound(card, "", spec, false)
	if err != nil {
		return RoundResult{}, fmt.Errorf("拉起协调者（来源 %s）失败: %w", source, err)
	}
	return result, nil
}

// AttachState 返回卡的人工接管状态。
func (s *Service) AttachState(card string) bool { return s.AttachActive(card) }

// AttachActive 报告卡是否处于人工接管中。
func (s *Service) AttachActive(card string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeover[card]
}

// SetAttach 置/清「人工接管中」。互斥是同一会话不许两个驾驶员（spec §4.4）：
// attach 打开期间 Decide 恒判不唤醒。接管态只在进程内存——ptyhost 的终端会话
// 本就随 agentd 生死（内存态），agentd 重启即 attach 断开，持久化一个已经
// 不存在的接管态只会造成「永久静音」事故（拍板记录③）。
func (s *Service) SetAttach(card string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.takeover[card] = active
}

// Locate 产出 attach 定位信息（机器 → 目录 → resume 命令）。
func (s *Service) Locate(card, workdir string) (keysclient.AttachInfo, error) {
	s.mu.Lock()
	ref, ok := s.sessions[card]
	s.mu.Unlock()
	if !ok {
		if s.ledger == nil {
			return keysclient.AttachInfo{}, errors.New("keystone: 该卡没有绑定的协调者会话")
		}
		seat, err := s.ledger.GetCard(card)
		if err != nil || seat.DriverSource != string(proto.SeatSourceCoordinate) {
			return keysclient.AttachInfo{}, errors.New("keystone: 该卡没有绑定的协调者会话")
		}
		cli, sessionID, err := proto.ParseSeatIdentity(seat.DriverSession)
		if err != nil {
			return keysclient.AttachInfo{}, fmt.Errorf("keystone: 解析卡 %s 席位: %w", card, err)
		}
		ref = keysclient.SessionRef{CLI: cli, SessionID: sessionID, Workdir: workdir}
	}
	if s.locator == nil {
		return keysclient.AttachInfo{}, errors.New("keystone: attach 定位未装配")
	}
	return s.locator.Locate(ref, workdir)
}

// Forget 清除进程内的旧会话引用。账本是席位真相；bind/self 换绑成功或读到
// 空座、bind、非法旧值时必须调用它，防止旧机器人在下一轮被 Resume。
func (s *Service) Forget(card string) {
	s.mu.Lock()
	delete(s.sessions, card)
	s.mu.Unlock()
}

// launchRound 用新载体承接同一协调者身份并绑定。spec 由组装点解析后传入
// （协调者小队 → LaunchAdmit → Binding → Carrier 读 HomeDir，契约 §15 澄清 2），
// 不再自造空 spec——骨架期忽略入参的欠账已补齐，防空 spec 回潮。重建四步的
// 输入由开场简报携带：读卡 → 会话史（账本事件流）→ timeline → 仓内文档指针。
func (s *Service) launchRound(card, extra string, spec keysclient.SessionSpec, rebuild bool) (RoundResult, error) {
	result, err := s.runner.Launch(spec, s.briefing(card, nil)+extra)
	if err != nil {
		return RoundResult{}, err
	}
	ref := keysclient.SessionRef{
		CLI: spec.CLI, SessionID: result.SessionID,
		HomeDir: spec.HomeDir, Workdir: spec.Workdir, Model: spec.Model,
	}
	s.mu.Lock()
	s.sessions[card] = ref
	s.mu.Unlock()
	// 指针只在重建成功后落：失败前落「载体已更换」会在每次重试写一行，
	// 合上房间 History 的最老窗就把发送看起来修成了「没反应」（B274）。
	if rebuild && s.narrator != nil {
		_ = s.narrator.Say(card, "载体已更换：新载体承接同一协调者身份（重建四步已执行）")
	}
	return RoundResult{Woke: true, SessionID: result.SessionID, Rebuilt: rebuild, Output: result.Output}, nil
}

// overlayResumeRef 把 Wake 入参里非空的承载环境盖到已绑定的 ref 上；CLI 不在
// 此处覆盖，因为 CLI/session identity 已由账本席位决定，spec 只描述当前载体。
func overlayResumeRef(ref keysclient.SessionRef, spec keysclient.SessionSpec) keysclient.SessionRef {
	if spec.HomeDir != "" {
		ref.HomeDir = spec.HomeDir
	}
	if spec.Workdir != "" {
		ref.Workdir = spec.Workdir
	}
	if spec.Model != "" {
		ref.Model = spec.Model
	}
	return ref
}

// briefing 把开场评估要读的东西拼成回合简报：卡字段、基线新鲜度、本次积压
// 事件。以 ledger 为准不信记忆——每回合重读，天然幂等。
func (s *Service) briefing(card string, evs []WakeEvent) string {
	b := "你是本卡的机器协调者。醒来第一件事：读卡、查依赖、看基线新鲜度；" +
		"不适合现在推就在房间说明原因并休眠。\n\n## 本卡上下文\n\n- 卡号：" + card + "\n"
	if c, err := s.ledger.GetCard(card); err == nil {
		b += "- 标题：" + c.Title + "\n"
	}
	if base, err := s.ledger.EffectiveBaseBranch(card); err == nil && base != "" {
		b += "- 有效基线分支：" + base + "（本卡的合并目标以此为准，不要越过它碰别的分支）\n"
	}
	if len(evs) > 0 {
		b += "\n## 本次唤醒事件\n"
		for _, ev := range evs {
			b += "- [" + string(ev.Kind) + "] " + ev.Summary + "\n"
		}
	}
	b += "\n点火前看上一节点产出：先看文件清单再看裁决块。\n"
	return b
}
