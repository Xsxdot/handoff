// resident.go —— B444 P1（S5）push 驻场者的拉起与续轮（plan §3 P1 keystone 项）。
//
// 职责：以「会话房间 id」为键承接 push 驻场者的 launch/resume 回合，并组织驻场者简报。
// 边界：keystone 不读驻场表（plan §3 架构方向：keysclient.LedgerView 不扩）——驻场者的载体落点
// （SessionRef）与简报所需会话上下文（ResidentBrief）都由 agentd 读账本后传入；本文件只做回合编排。
// 驻场 ref 表（Service.residents）与卡 ref 表（Service.sessions）分开存：会话 id 与卡号虽然字面不会
// 撞（session:<n> vs B<n>），分表让 Forget(卡) / 重建等卡路径在结构上碰不到驻场者（缺陷族 R1）。
// 驻场者续轮失败**不重建**：重建要重写驻场行（换会话 id），那是 agentd 编排层的事，本层只透传错误。
package keystone

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

// ResidentBrief 是驻场者回合简报所需的会话上下文，由 agentd 读账本后传入。
type ResidentBrief struct {
	Session string   // 会话房间 id（session:<n>）
	Agent   string   // 驻场身份 agent:<名> 的名字段：`session send --agent` 的值（push 驻场者 = 小队名）
	Title   string   // 会话当前标题
	Cards   []string // 会话里的卡号（会话卡序）
}

// WakeResident 续 push 驻场者的同一 CLI 会话一轮（B444 B1）。参数：ref 是 agentd 从驻场行 bearing
// 读出并规范化的会话引用（CLI、SessionID 必填）；brief 是简报上下文；evs 是本批唤醒事件（至少一条）。
// 返回：成功 → Woke=true、SessionID=回合实际会话。错误：入参不全 → 直接拒；Runner.Resume 失败 →
// 以 %w 透传（agentd 据 keysclient 哨兵分类），驻场 ref 保留、不重建、不落 needs_human（驻场者不是卡，
// 可见面由 agentd 写会话系统指针行）。
func (s *Service) WakeResident(ctx context.Context, ref keysclient.SessionRef, brief ResidentBrief, evs []WakeEvent) (RoundResult, error) {
	if brief.Session == "" || brief.Agent == "" || ref.CLI == "" || ref.SessionID == "" {
		err := fmt.Errorf("keystone: 驻场者续轮入参不全（session=%q agent=%q cli=%q has_session=%v）",
			brief.Session, brief.Agent, ref.CLI, ref.SessionID != "")
		log().Warn("驻场者续轮被拒：入参不全", "cause", err)
		return RoundResult{}, err
	}
	if len(evs) == 0 {
		return RoundResult{}, errors.New("keystone: 驻场者续轮没有事件")
	}
	s.mu.Lock()
	s.residents[brief.Session] = ref
	s.mu.Unlock()
	log().Info("驻场者续轮开始", "session", brief.Session, "agent", brief.Agent, "cli", ref.CLI,
		"push_session", ref.SessionID, "event_count", len(evs))
	result, err := s.runner.Resume(ref, residentBriefing(brief, evs, false))
	if err != nil {
		log().Warn("驻场者续轮失败，保留会话不重建", "session", brief.Session, "push_session", ref.SessionID, "cause", err)
		return RoundResult{SessionID: ref.SessionID}, fmt.Errorf("驻场者 resume: %w", err)
	}
	log().Info("驻场者续轮完成", "session", brief.Session, "push_session", result.SessionID, "output_bytes", len(result.Output))
	return RoundResult{Woke: true, SessionID: result.SessionID, Output: result.Output}, nil
}

// LaunchForResident 为会话拉起 push 驻场者首轮（新 CLI 会话，B444 P2 建会话编排的消费面）。参数：spec 由
// agentd 组装（小队 → 准入 → 载体）；brief 同 WakeResident。返回：Runner 生成的新会话 id（调用方据此写
// 驻场行 push_session）。驻场身份是 agent: 而不是席位，首轮即可发言，故简报与续轮同形（只多首轮标注）。
func (s *Service) LaunchForResident(ctx context.Context, spec keysclient.SessionSpec, brief ResidentBrief) (RoundResult, error) {
	if brief.Session == "" || brief.Agent == "" || spec.CLI == "" {
		err := fmt.Errorf("keystone: 驻场者拉起入参不全（session=%q agent=%q cli=%q）", brief.Session, brief.Agent, spec.CLI)
		log().Warn("驻场者拉起被拒：入参不全", "cause", err)
		return RoundResult{}, err
	}
	log().Info("驻场者首轮拉起开始", "session", brief.Session, "agent", brief.Agent, "cli", spec.CLI)
	result, err := s.runner.Launch(spec, residentBriefing(brief, nil, true))
	if err != nil {
		log().Warn("驻场者首轮拉起失败", "session", brief.Session, "cli", spec.CLI, "cause", err)
		return RoundResult{}, fmt.Errorf("驻场者 launch: %w", err)
	}
	s.mu.Lock()
	s.residents[brief.Session] = keysclient.SessionRef{
		CLI: spec.CLI, SessionID: result.SessionID, HomeDir: spec.HomeDir, Workdir: spec.Workdir, Model: spec.Model,
	}
	s.mu.Unlock()
	log().Info("驻场者首轮拉起完成", "session", brief.Session, "push_session", result.SessionID)
	return RoundResult{Woke: true, SessionID: result.SessionID, Output: result.Output}, nil
}

// ForgetResident 清掉会话的驻场 ref（驻场者被请下或换人时由 agentd 调用；账本是真相，本表只是缓存）。
func (s *Service) ForgetResident(session string) {
	s.mu.Lock()
	delete(s.residents, session)
	s.mu.Unlock()
}

// residentBriefingRole 是驻场者简报的角色句（spec「驻场者简报」）：会话 agent 回人话；卡协调者一卡一个，
// 驻场者不接管卡席位（spec Out of Scope「永不做」）。简报文本，不是机制。
const residentBriefingRole = "你是本会话的驻场者（会话 agent）：人在会话里不带 @ 的发言归你回。" +
	"**你是一次性回合**：没有交互窗口，本轮跑完即结束；人再说话会再次唤醒你（续跑同一会话）。\n"

// residentBriefingCoordinate 指路「为卡叫协调者」（S6，既有 card coordinate；不改代码）。
const residentBriefingCoordinate = "会话里的卡需要推进时，为它叫协调者：`handoff card coordinate <卡号>`" +
	"（卡协调者一卡一个，按卡工作；你不接管卡席位）。" + residentBriefingCoordinateSync

// residentBriefingCoordinateSync 说明 card coordinate 的同步与耗时（B444 P2 返工 F2，真机 R4：它要等该卡协调者
// 首轮跑完，可能好几分钟；载体 shell 工具 120s 默认超时杀掉后模型误报失败、可能重复叫）。
const residentBriefingCoordinateSync = "这条命令是**同步**的：它会等该卡协调者首轮跑完才返回，可能要好几分钟——" +
	"调用时把工具超时设到至少 20 分钟；超时不等于失败，先用 `handoff card show <卡号>` 看 driver_session 是否已坐上再判断；" +
	"同一张卡绝不重复 coordinate。"

// residentBriefingSpeakOnly 限定发言身份（B444 P2 返工 F2，真机 R4：首轮发不了言时模型冒用了卡席位的
// --cli/--session）。返回带小队名的句子。
func residentBriefingSpeakOnly(agent string) string {
	return "只用 `--agent " + agent + "` 发言，绝不用 `--cli/--session`（那是卡席位的身份，不是你的）。"
}

// residentBriefingCoordinateNoSlot 是叫协调者失败时的处置句（B444 协调者裁决 9：驻场回合里同步叫协调者可能
// 撞同一小队的名额——R3 待真机判定，修法未定；在此之前不让驻场者循环重试烧回合）。
const residentBriefingCoordinateNoSlot = "若 `card coordinate` 报无名额 / 409，就在本会话里如实告诉人（哪张卡、报了什么），" +
	"不要反复重试。"

// residentBriefingFirstRound 是 launch 首轮的标注：驻场身份是 agent: 而非席位，首轮即可发言（与卡席位
// 首轮「发不了房间消息」相反，不得照搬 briefingFirstRound）。
const residentBriefingFirstRound = "本轮为首轮：你现在就可以在会话里说话。"

// residentSessionSend 是驻场者的发言命令（cmd/session.go：--agent 编码为 agent:<名>，与 --cli/--session 互斥）。
func residentSessionSend(agent, session string) string {
	return "handoff session send --agent " + agent + " " + session + " <正文>"
}

// residentRename 是零卡未命名会话的首轮改名指令（plan Q6 甲：--expect-title 作 CAS，人先改过名就保留人的）。
func residentRename(agent, session string) string {
	return "handoff session rename " + session + " --agent " + agent + " --expect-title " +
		proto.SessionUntitledTitle + " <概括名>"
}

// residentBriefing 拼驻场者回合简报：角色句、发言命令与只用 --agent 的限定、叫协调者指路（同步与超时处置）、
// 口吻句、（零卡且仍是占位名时）CAS 改名指令、会话最小上下文与本次唤醒事件。不给任何「以席位身份发言」的
// 指示（`send --cli …`、HANDOFF 席位环境键）——只点名禁止 `--cli/--session`（真机 R4 冒用卡席位的教训）。
func residentBriefing(brief ResidentBrief, evs []WakeEvent, firstRound bool) string {
	b := residentBriefingRole
	if firstRound {
		b += residentBriefingFirstRound
	}
	b += "回人话**发到本会话**：`" + residentSessionSend(brief.Agent, brief.Session) + "`。" +
		residentBriefingSpeakOnly(brief.Agent) + residentBriefingCoordinate + residentBriefingCoordinateNoSlot + briefingRoomTone
	if len(brief.Cards) == 0 && brief.Title == proto.SessionUntitledTitle {
		b += "\n本会话还没有名字：读懂人说的事后，用 `" + residentRename(brief.Agent, brief.Session) +
			"` 起一个概括性名字；命令若因标题已变被拒，说明人已手动命名，保留人的名字即可。"
	}
	b += "\n\n## 本会话上下文\n\n- 会话：" + brief.Session + "\n- 标题：" + brief.Title + "\n"
	if len(brief.Cards) == 0 {
		b += "- 卡：无\n"
	} else {
		b += "- 卡：" + strings.Join(brief.Cards, "、") + "\n"
	}
	return b + briefingWakeEvents(evs)
}
