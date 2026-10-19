// recipients.go —— B435 匹配函数：「这条会话房间消息该不该送到地址 A」的唯一出口
// （契约 docs/superpowers/specs/b435-contract.md ②）。
//
// 消费方四个：agentd push 消费循环、`card wait`、`session wait`、桌面端信箱桥。它们
// 只调 Recipients / Matches，不得自带寻址或默认收件人判定（spec §8 禁令）。
//
// 判定表（冻结，契约 §3.2；按序首个命中即定）：
//   - 结构行（by_system / pointer）→ 空集，structural；载荷 by_system:null 的旧边界在
//     解码层可见、本签名（已解码的 RoomMessage）不可见，由消费方解码时按结构行跳过；
//   - 非会话房间 → 空集，not_session_room；
//   - 有寻址（ResolveDelivery 规则不变，B358 条 20–25）→ 显式地址集：@卡号 / 回复席位作者
//     → 本会话内该卡席位地址；@驻场者身份 / 回复驻场者 → 驻场者地址；其余解析结果丢弃；
//     全部丢弃 → 空集，addressed_unresolved（不回落默认收件人）；
//   - 无寻址 + 非人类作者 → 空集，non_human_unaddressed；
//   - 无寻址 + 人类作者 → 默认收件人（恰 0 或 1 个）：有驻场者 → 驻场者；否则会话根卡恰一张
//     且有席位 → 该卡席位；根卡恰一张但空座 → root_no_seat；根卡零张或多张 → needs_mention。
//
// 边界：地址解析与席位模式无关（push / pull 同为地址，C-227）；按 mode 分流归消费方。
// 任何账本读失败都返回错误（C-228），绝不当作「未命中」。
package collab

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

// Recipients 解析一条会话房间消息的收件地址集。author 是该消息账本事件的 actor
// （默认收件人分支按它判人类作者，proto.IsHumanAuthor）。读账本失败返回错误，调用方
// 必须上抛（误判未命中会让持久水位越过该消息）。返回的 Delivery：Default=true 时恰一个
// 地址；地址集为空时 NoRecipient 给原因。
func (s *Service) Recipients(msg proto.RoomMessage, author string) (proto.Delivery, error) {
	if msg.BySystem || msg.Kind == proto.RoomMsgPointer {
		return s.noRecipient(msg, proto.NoRecipientStructural), nil
	}
	if !room.IsSessionRoom(msg.Room) {
		return s.noRecipient(msg, proto.NoRecipientNotSessionRoom), nil
	}
	replyAuthor, err := s.replyAuthorOf(msg)
	if err != nil {
		log().Warn("匹配函数读回复原作者失败", "session", msg.Room, "reply_to", msg.ReplyTo, "cause", err)
		return proto.Delivery{}, err
	}
	if room.IsAddressed(msg, replyAuthor) {
		addrs, err := s.explicitRecipients(msg, replyAuthor)
		if err != nil {
			return proto.Delivery{}, err
		}
		if len(addrs) == 0 {
			return s.noRecipient(msg, proto.NoRecipientAddressedUnresolved), nil
		}
		return proto.Delivery{Addresses: addrs}, nil
	}
	if !proto.IsHumanAuthor(author) {
		return s.noRecipient(msg, proto.NoRecipientNonHumanUnaddressed), nil
	}
	addr, reason, ok, err := s.defaultRecipient(msg.Room)
	if err != nil {
		return proto.Delivery{}, err
	}
	if !ok {
		return s.noRecipient(msg, reason), nil
	}
	return proto.Delivery{Addresses: []proto.DeliveryAddress{addr}, Default: true}, nil
}

// Matches 是匹配函数的点判形态：addr 逐字段等于 Recipients(msg, author).Addresses 中某项
// （C-226）。两个 wait 与信箱桥用它判「是不是给我的」；push 消费循环用 Recipients 拿全集。
// 二者同源，不得各自实现。读失败返回 (false, err)，调用方不得把它当未命中。
func (s *Service) Matches(msg proto.RoomMessage, author string, addr proto.DeliveryAddress) (bool, error) {
	d, err := s.Recipients(msg, author)
	if err != nil {
		return false, err
	}
	for _, a := range d.Addresses {
		if a == addr {
			return true, nil
		}
	}
	return false, nil
}

// defaultRecipient 是默认收件人分支的唯一产出点：返回值是**单个**地址（ok=false 时
// 附原因）——「解析结果 ≤ 1 人」由返回类型保证，切片形状写不出来（源码守卫锁定）。
// sessionID 是消息所在会话；人类作者与无寻址的前置判定由调用方（Recipients）完成。
// 有驻场者先于根卡（判定表第 5 行先于第 6 行）；默认收件人不排除作者本人（C-224）。
func (s *Service) defaultRecipient(sessionID string) (addr proto.DeliveryAddress, reason proto.NoRecipientReason, ok bool, err error) {
	resident, has, err := s.lc.SessionResident(sessionID)
	if err != nil {
		log().Warn("匹配函数读驻场者失败", "session", sessionID, "cause", err)
		return proto.DeliveryAddress{}, "", false, fmt.Errorf("读会话 %s 驻场者: %w", sessionID, err)
	}
	if has {
		return proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: sessionID, Identity: resident.Identity}, "", true, nil
	}
	cards, err := s.sessionCardSnapshot(sessionID)
	if err != nil {
		return proto.DeliveryAddress{}, "", false, err
	}
	inSession := make(map[string]bool, len(cards))
	for _, c := range cards {
		inSession[c.ID] = true
	}
	var roots []proto.Card
	for _, c := range cards {
		// 会话根卡 = parent 为空、或 parent 不在本会话卡集合中的卡（C-223）。
		if c.ParentID == "" || !inSession[c.ParentID] {
			roots = append(roots, c)
		}
	}
	if len(roots) != 1 {
		return proto.DeliveryAddress{}, proto.NoRecipientNeedsMention, false, nil
	}
	root := roots[0]
	if root.DriverSession == "" {
		return proto.DeliveryAddress{}, proto.NoRecipientRootNoSeat, false, nil
	}
	return proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: sessionID, Card: root.ID, Identity: root.DriverSession}, "", true, nil
}

// explicitRecipients 是判定表第 3 行：寻址解析复用 resolveMessageTargets（ResolveDelivery
// 规则不复制），再把每个 target 映射到本会话的地址。席位身份 → 本会话内当前坐着该身份的卡；
// 同一身份同时坐本会话多张卡时，以消息里显式 @ 的卡号收窄（@子卡不连带唤醒根卡）。
// 驻场者身份 → 驻场者地址。其余 target 丢弃（C-215）。结果按 target 顺序去重（C-217）。
func (s *Service) explicitRecipients(msg proto.RoomMessage, replyAuthor string) ([]proto.DeliveryAddress, error) {
	targets, err := s.resolveMessageTargets(msg, replyAuthor)
	if err != nil {
		log().Warn("匹配函数解析寻址失败", "session", msg.Room, "cause", err)
		return nil, err
	}
	cards, err := s.sessionCardSnapshot(msg.Room)
	if err != nil {
		return nil, err
	}
	resident, hasResident, err := s.lc.SessionResident(msg.Room)
	if err != nil {
		log().Warn("匹配函数读驻场者失败", "session", msg.Room, "cause", err)
		return nil, fmt.Errorf("读会话 %s 驻场者: %w", msg.Room, err)
	}
	mentioned := make(map[string]bool, len(msg.Mentions))
	for _, m := range msg.Mentions {
		mentioned[strings.TrimSpace(m)] = true
	}
	var out []proto.DeliveryAddress
	seen := map[proto.DeliveryAddress]bool{}
	add := func(a proto.DeliveryAddress) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, target := range targets {
		var seated []proto.Card
		for _, c := range cards {
			if c.DriverSession == target {
				seated = append(seated, c)
			}
		}
		if len(seated) > 1 {
			var narrowed []proto.Card
			for _, c := range seated {
				if mentioned[c.ID] {
					narrowed = append(narrowed, c)
				}
			}
			if len(narrowed) > 0 {
				seated = narrowed
			}
		}
		for _, c := range seated {
			add(proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: msg.Room, Card: c.ID, Identity: target})
		}
		if len(seated) == 0 && hasResident && target == resident.Identity {
			add(proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: msg.Room, Identity: resident.Identity})
		}
	}
	return out, nil
}

// sessionCardSnapshot 读会话及其全部卡（含席位与 parent），按会话卡序返回。会话或卡读失败
// 返回错误（C-228）；会话内登记了但账本已无的卡跳过并 Warn（不是读失败）。
func (s *Service) sessionCardSnapshot(sessionID string) ([]proto.Card, error) {
	session, err := s.lc.GetSession(sessionID)
	if err != nil {
		log().Warn("匹配函数读会话失败", "session", sessionID, "cause", err)
		return nil, fmt.Errorf("读会话 %s: %w", sessionID, err)
	}
	cards := make([]proto.Card, 0, len(session.Cards))
	for _, id := range session.Cards {
		card, err := s.lc.GetCard(id)
		if errors.Is(err, client.ErrNotFound) {
			log().Warn("会话登记的卡在账本中不存在，不参与匹配", "session", sessionID, "card", id)
			continue
		}
		if err != nil {
			log().Warn("匹配函数读会话卡失败", "session", sessionID, "card", id, "cause", err)
			return nil, fmt.Errorf("读会话 %s 的卡 %s: %w", sessionID, id, err)
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// noRecipient 构造空地址集结果并留一条 Debug（判定可查，空集不是静默）。
func (s *Service) noRecipient(msg proto.RoomMessage, reason proto.NoRecipientReason) proto.Delivery {
	log().Debug("匹配函数：消息无收件地址", "room", msg.Room, "reason", reason,
		"mentions", len(msg.Mentions), "reply_to", msg.ReplyTo)
	return proto.Delivery{NoRecipient: reason}
}
