// projectnotify.go —— B435 O1 项目房间系统通知（spec D8；契约 C-243–C-245；O1 plan Q6 甲 / Q7 乙 /
// Q8 甲 / Q14 甲）。
//
// 职责：自动化消费循环的旁路一步——卡事件 card_created / status_moved / needs_human 在该卡项目的项目
// 房间里产生恰一条 `agent:handoff` 作者、`Mentions=[驻场者身份]` 的普通消息（kind=user、by_system 缺键）。
// 项目无项目房间、房间无驻场者 → 零条（C-245，只落卡事件）。通知从不创建项目房间（拆解 §2.4 第 1 条）。
//
// 恰一条怎样成立：
//   - 多机：每条源事件按 `wake_claims (card="notify:<事件类型>", seq=源 seq)` 认领（P7 乙，复用 B389 认领
//     语义）；在飞认领经 CursorWatermark 挡住本机游标，他机让过即不记 handled、下轮重试；
//   - Send 与收尾之间崩溃：通知 Refs 带锚 "seq:<源 seq>"，Send 前点读项目房间里 agent:handoff 写下、带同锚
//     的通知，命中即只收尾不重发（Q7 乙；同一时刻只有一名认领持有者，接管者必能读到前任已提交的消息）；
//   - 本进程内：handled 集只记「已发且收尾 / 判定不适用 / 确定性放弃」，其余（让过、瞬时失败）下轮重来。
//
// 失败三分（Q8 甲）：不适用 → 不认领、记 handled、Debug；确定性拒绝（归档 ErrReadOnly、ErrNotWriter、
// ErrNoRoom）→ Warn、收尾认领、记 handled（放弃本条）；其余错误（账本读写、候选查询）→ Warn、认领留在飞、
// 不记 handled、下轮重试，不设上限。认领之前的账本读失败也取认领挡水位；持有认领后事件变为不适用 → 收尾
// 自己的认领（否则在飞认领永久挡水位，整轮停摆）。
//
// 边界：不做寻址判定（通知 @驻场者 → 驻场者地址由 collab.Recipients 在消费侧判定，C-214/C-231）、不唤醒
// 任何人、不写门铃信（O2）、不改消费循环任何 seen 写点；正文是给组织者（LLM）读的自然语言，O2 简报不得
// 依赖其字面或锚格式（plan §9）。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// projectNotifyClaimPrefix 是通知认领的 card 键前缀：wake_claims.card = "notify:<事件类型>"。卡号不含
// ":"，与卡唤醒认领不撞；认领表只按 (card,seq) 读、水位按 seq 聚合。
const projectNotifyClaimPrefix = "notify:"

// projectNotifyAnchorPrefix 是通知 Refs 里的幂等锚前缀（"seq:<源事件 seq>"，Q7 乙）。
const projectNotifyAnchorPrefix = "seq:"

// projectNotifyCandidatePage 是幂等锚点读的单页大小（MentionCandidatesContext 的 limit）。
const projectNotifyCandidatePage = 200

// 通知正文模板（Q14 甲；正文含卡号与项目名，给组织者读）。
const (
	projectNoticeCardCreated = "卡 %s 新建：%s（项目 %s）"
	projectNoticeStatusMoved = "卡 %s 换列：%s → %s（项目 %s）"
	projectNoticeNeedsHuman  = "卡 %s 需要人：%s（项目 %s）"
)

// projectNotifyHandled 是通知器的 handled 集（Server.projectNotify，零值可用）。进程内存：重启后清空，
// 由已终局认领（ClaimWake=false）与幂等锚兜住不重发。不裁剪：与 automationSeen 同量级（每事件至多一项）。
type projectNotifyHandled struct {
	mu      sync.Mutex
	handled map[int64]struct{}
}

func (h *projectNotifyHandled) isHandled(seq int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.handled[seq]
	return ok
}

func (h *projectNotifyHandled) markHandled(seq int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.handled == nil {
		h.handled = make(map[int64]struct{})
	}
	h.handled[seq] = struct{}{}
}

// projectNoticeTarget 是一条源事件判定为「适用」后的通知去向。
type projectNoticeTarget struct {
	project  string
	room     string
	resident string
	body     string
}

// notifyProjectRoom 是消费循环对每条游标之后事件调用的旁路（Q6 甲：在 seen 去重之前、无返回值）。
// 参数 ev：自动化账本事件。只处理三类卡事件；其余直接返回。注意：本函数从不让整轮失败，一切失败按
// Q8 甲三分处置并留日志。
func (s *Server) notifyProjectRoom(ctx context.Context, ev proto.LedgerEvent) {
	switch ev.Type {
	case ledger.EvCardCreated, ledger.EvStatusMoved, ledger.EvNeedsHuman:
	default:
		return
	}
	if ev.CardID == "" || s.projectNotify.isHandled(ev.Seq) {
		return
	}
	key := projectNotifyClaimPrefix + ev.Type
	if s.ledger == nil || s.rooms == nil {
		s.log.Debug("项目房间通知不适用：账本或会话服务未装配", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"has_ledger", s.ledger != nil, "has_rooms", s.rooms != nil)
		s.projectNotify.markHandled(ev.Seq)
		return
	}
	target, applicable, reason, err := s.projectNoticeTargetOf(ev)
	if err != nil {
		// 其余错误（认领之前）：取认领挡住水位，下轮重试；认领也拿不到时游标推进同样会因账本故障失败。
		s.log.Warn("项目房间通知判定读失败，持认领下轮重试", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "cause", err)
		s.holdProjectNotifyClaim(ev, key)
		return
	}
	if !applicable {
		s.settleInapplicableNotice(ev, key, reason)
		return
	}
	holder := s.wakeClaimHolder()
	got, err := s.ledger.ClaimWake(ev.Seq, key, holder, wakeClaimTTL)
	if err != nil {
		s.log.Warn("项目房间通知认领失败，下轮重试", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "cause", err)
		return
	}
	if !got {
		s.log.Info("项目房间通知已被他机认领或已终局，本机让过", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"room", target.room, "holder", holder)
		return
	}
	anchor := projectNotifyAnchorPrefix + strconv.FormatInt(ev.Seq, 10)
	sent, err := s.projectNoticeAlreadySent(ctx, target, ev.Seq, anchor)
	if err != nil {
		s.log.Warn("项目房间通知幂等点读失败，认领留在飞下轮重试", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"room", target.room, "cause", err)
		return
	}
	if sent {
		s.log.Info("项目房间通知已由前任发出，只收尾认领", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"room", target.room, "anchor", anchor)
		s.finishProjectNotice(ev, key, holder)
		return
	}
	msgSeq, err := s.rooms.Send(target.room, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: target.body,
		Mentions: []string{target.resident}, Refs: []string{ev.CardID, anchor},
	}, proto.SystemNotifierIdentity)
	if err != nil {
		if errors.Is(err, collab.ErrReadOnly) || errors.Is(err, collab.ErrNotWriter) || errors.Is(err, collab.ErrNoRoom) {
			s.log.Warn("项目房间通知被确定性拒绝，放弃本条", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
				"project", target.project, "room", target.room, "cause", err)
			s.finishProjectNotice(ev, key, holder)
			return
		}
		s.log.Warn("项目房间通知发送失败，认领留在飞下轮重试", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"project", target.project, "room", target.room, "cause", err)
		return
	}
	s.log.Info("项目房间通知已发", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "project", target.project,
		"room", target.room, "resident", target.resident, "message_seq", msgSeq)
	s.finishProjectNotice(ev, key, holder)
}

// projectNoticeTargetOf 判定源事件是否适用并给出去向。返回：applicable=false 时 reason 给原因（卡无项目、
// 项目无项目房间、房间无驻场者、卡不存在）；err 非空表示账本读失败（不得当作不适用）。
func (s *Server) projectNoticeTargetOf(ev proto.LedgerEvent) (projectNoticeTarget, bool, string, error) {
	card, err := s.ledger.GetCard(ev.CardID)
	if errors.Is(err, ledger.ErrNotFound) {
		return projectNoticeTarget{}, false, "卡不存在", nil
	}
	if err != nil {
		return projectNoticeTarget{}, false, "", fmt.Errorf("读卡 %s: %w", ev.CardID, err)
	}
	if card.Project == "" {
		return projectNoticeTarget{}, false, "卡无项目", nil
	}
	room, ok, err := s.ledger.ProjectRoom(card.Project)
	if err != nil {
		return projectNoticeTarget{}, false, "", fmt.Errorf("读项目 %s 的项目房间: %w", card.Project, err)
	}
	if !ok {
		return projectNoticeTarget{}, false, "项目无项目房间", nil
	}
	resident, ok, err := s.ledger.SessionResident(room)
	if err != nil {
		return projectNoticeTarget{}, false, "", fmt.Errorf("读项目房间 %s 驻场者: %w", room, err)
	}
	if !ok {
		return projectNoticeTarget{}, false, "项目房间无驻场者", nil
	}
	return projectNoticeTarget{
		project: card.Project, room: room, resident: resident.Identity,
		body: projectNoticeBody(ev, card),
	}, true, "", nil
}

// projectNoticeBody 按事件类型套正文模板；载荷缺字段时以占位文字代之（正文只给人读，不承载判定）。
func projectNoticeBody(ev proto.LedgerEvent, card ledger.Card) string {
	var payload struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(ev.Payload, &payload)
	switch ev.Type {
	case ledger.EvStatusMoved:
		return fmt.Sprintf(projectNoticeStatusMoved, card.ID, orPlaceholder(payload.From), orPlaceholder(payload.To), card.Project)
	case ledger.EvNeedsHuman:
		return fmt.Sprintf(projectNoticeNeedsHuman, card.ID, orPlaceholder(payload.Reason), card.Project)
	default:
		return fmt.Sprintf(projectNoticeCardCreated, card.ID, card.Title, card.Project)
	}
}

func orPlaceholder(v string) string {
	if v == "" {
		return "（未记录）"
	}
	return v
}

// projectNoticeAlreadySent 是 Q7 乙幂等点读：项目房间里 seq 之后 @驻场者 的无卡消息中，是否已有作者
// agent:handoff、Refs 含锚的一条。按页读到底（每页有界）；读失败返回错误（不得当作未发）。
func (s *Server) projectNoticeAlreadySent(ctx context.Context, target projectNoticeTarget, after int64, anchor string) (bool, error) {
	cursor := after
	for {
		page, err := s.ledger.MentionCandidatesContext(ctx, target.resident, target.room, true, cursor, projectNotifyCandidatePage)
		if err != nil {
			return false, err
		}
		for _, ev := range page {
			cursor = ev.Seq
			if ev.Actor != proto.SystemNotifierIdentity {
				continue
			}
			var msg struct {
				Refs []string `json:"refs"`
			}
			if err := json.Unmarshal(ev.Payload, &msg); err != nil {
				s.log.Warn("幂等点读跳过载荷无效的候选", "seq", ev.Seq, "room", target.room, "cause", err)
				continue
			}
			for _, ref := range msg.Refs {
				if ref == anchor {
					return true, nil
				}
			}
		}
		if len(page) < projectNotifyCandidatePage {
			return false, nil
		}
	}
}

// finishProjectNotice 收尾本机认领并记 handled。收尾失败只留日志、不记 handled：认领留在飞挡水位，
// 下轮同 holder 续期后幂等锚命中即补收尾。
func (s *Server) finishProjectNotice(ev proto.LedgerEvent, key, holder string) {
	if err := s.ledger.CompleteWake(ev.Seq, key, holder); err != nil {
		s.log.Warn("项目房间通知认领收尾失败，下轮重试", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "cause", err)
		return
	}
	s.projectNotify.markHandled(ev.Seq)
}

// holdProjectNotifyClaim 在认领之前的读失败时取认领挡住水位（不发、不收尾）；拿不到认领只留日志。
func (s *Server) holdProjectNotifyClaim(ev proto.LedgerEvent, key string) {
	got, err := s.ledger.ClaimWake(ev.Seq, key, s.wakeClaimHolder(), wakeClaimTTL)
	if err != nil {
		s.log.Warn("项目房间通知挡水位认领失败", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "cause", err)
		return
	}
	s.log.Info("项目房间通知持认领挡水位", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "claimed", got)
}

// settleInapplicableNotice 处置判定不适用的事件：通常直接记 handled；若该 seq 上仍有在飞认领（本进程此前
// 持有，或前任进程遗留），先接管并收尾——否则它永久挡住水位。他机未过期的认领让过、不记 handled。
func (s *Server) settleInapplicableNotice(ev proto.LedgerEvent, key, reason string) {
	w, err := s.ledger.CursorWatermark(ev.Seq-1, ev.Seq)
	if err != nil {
		s.log.Warn("项目房间通知不适用但在飞认领探测失败，下轮重试", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"reason", reason, "cause", err)
		return
	}
	if w < ev.Seq {
		holder := s.wakeClaimHolder()
		got, err := s.ledger.ClaimWake(ev.Seq, key, holder, wakeClaimTTL)
		if err != nil {
			s.log.Warn("项目房间通知不适用，接管在飞认领失败，下轮重试", "seq", ev.Seq, "card", ev.CardID, "cause", err)
			return
		}
		if !got {
			s.log.Info("项目房间通知不适用，在飞认领属他机，本机让过", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "reason", reason)
			return
		}
		s.log.Info("项目房间通知不再适用，收尾在飞认领", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "reason", reason)
		s.finishProjectNotice(ev, key, holder)
		return
	}
	s.log.Debug("项目房间通知不适用", "seq", ev.Seq, "card", ev.CardID, "type", ev.Type, "reason", reason)
	s.projectNotify.markHandled(ev.Seq)
}
