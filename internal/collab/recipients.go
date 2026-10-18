// recipients.go —— B435 匹配函数：「这条会话房间消息该不该送到地址 A」的唯一出口
// （契约 docs/superpowers/specs/b435-contract.md ②）。
//
// 消费方四个：agentd push 消费循环、`card wait`、`session wait`、桌面端信箱桥。它们
// 只调 Recipients / Matches，不得自带寻址或默认收件人判定（spec §8 禁令）。
//
// 判定表（冻结，实现节点落码）：
//   - 结构行（by_system / pointer / by_system:null）→ 空集，structural；
//   - 非会话房间 → 空集，not_session_room；
//   - 有寻址（ResolveDelivery 规则不变，B358 条 20–25）→ 显式地址集：@卡号 / 回复席位作者
//     → 本会话内该卡席位地址；@驻场者身份 / 回复驻场者 → 驻场者地址；其余解析结果丢弃；
//     全部丢弃 → 空集，addressed_unresolved（不回落默认收件人）；
//   - 无寻址 + 非人类作者 → 空集，non_human_unaddressed；
//   - 无寻址 + 人类作者 → 默认收件人（恰 0 或 1 个）：有驻场者 → 驻场者；否则会话根卡恰一张
//     且有席位 → 该卡席位；根卡恰一张但空座 → root_no_seat；根卡零张或多张 → needs_mention。
//
// Ticket 0：本文件只有签名，三个函数一律返回 errRecipientsNotImplemented（不返回任何
// 成功结果）。实现节点替换函数体，签名改动须回 contract 节点。
package collab

import (
	"errors"

	"github.com/Xsxdot/handoff/internal/proto"
)

// errRecipientsNotImplemented 是 Ticket 0 空壳期的哨兵：调用方拿到它必须当作判定失败
// 上抛，绝不当作「未命中」吞掉。实现节点落码后删除。
var errRecipientsNotImplemented = errors.New("collab: B435 匹配函数尚未实现（Ticket 0 空壳）")

// Recipients 解析一条会话房间消息的收件地址集。author 是该消息账本事件的 actor
// （默认收件人分支按它判人类作者，proto.IsHumanAuthor）。读账本失败返回错误，调用方
// 必须上抛（误判未命中会让持久水位越过该消息）。
func (s *Service) Recipients(msg proto.RoomMessage, author string) (proto.Delivery, error) {
	return proto.Delivery{}, errRecipientsNotImplemented
}

// Matches 是匹配函数的点判形态：addr ∈ Recipients(msg, author).Addresses。两个 wait
// 与信箱桥用它判「是不是给我的」；push 消费循环用 Recipients 拿全集。二者同源，
// 不得各自实现。
func (s *Service) Matches(msg proto.RoomMessage, author string, addr proto.DeliveryAddress) (bool, error) {
	return false, errRecipientsNotImplemented
}

// defaultRecipient 是默认收件人分支的唯一产出点：返回值是**单个**地址（ok=false 时
// 附原因）——「解析结果 ≤ 1 人」由返回类型保证，切片形状写不出来（源码守卫锁定）。
// sessionID 是消息所在会话；人类作者与无寻址的前置判定由调用方（Recipients）完成。
func (s *Service) defaultRecipient(sessionID string) (addr proto.DeliveryAddress, reason proto.NoRecipientReason, ok bool, err error) {
	return proto.DeliveryAddress{}, "", false, errRecipientsNotImplemented
}
