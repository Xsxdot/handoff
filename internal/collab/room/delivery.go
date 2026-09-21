// 投递寻址（B358 本卡核心契约的纯函数半边）。
//
// 一条消息的接收人由发送者写下的寻址决定，与「群里有谁」无关：寻址 = 显式
// mentions（可多个）+ reply_to（隐式寻址原作者，一人）。无寻址 => 不唤醒任何人。
// 本文件只做纯判定，不读账本；账本解析（@卡号 → 当前席位、reply_to → 原作者）
// 在门面 Service.WakeTargets 里注入。
//
// 扇出禁令的机械兑现：本函数签名里根本没有「成员集合」这个入参——任何按成员
// 集合投递的形状都写不出来。加源码级守卫见 internal/agentd 的唤醒路径测试。
package room

import (
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ResolveDelivery 把一条消息的寻址解析为被唤醒成员身份集。
//
// 规则：
//   - 显式 mentions 逐个解析：@卡号 经 resolveSeat 解析为该卡当前席位（换绑后
//     依然指向新席位）。resolveSeat 返回 isCard=true 时，seat 非空则唤醒该席位、
//     空则落空（卡在群里但没人坐——@ 不到任何人）；isCard=false 表示 mention
//     不是卡号（外部会话身份如 user:sy），原样使用。
//   - reply_to 命中时把 replyAuthor（被回复消息的作者）作为隐式目标；空串忽略。
//   - BySystem 为真或 kind=pointer 的系统结构行不唤醒任何人。
//   - 无任何寻址返回 nil。
//
// 去重保持首次出现顺序（同一人既被 @ 又回复时只唤醒一次）。
func ResolveDelivery(msg proto.RoomMessage, replyAuthor string, resolveSeat func(mention string) (seat string, isCard bool)) []string {
	if msg.BySystem || msg.Kind == proto.RoomMsgPointer {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(identity string) {
		identity = strings.TrimSpace(identity)
		if identity == "" || seen[identity] {
			return
		}
		seen[identity] = true
		out = append(out, identity)
	}
	for _, mention := range msg.Mentions {
		mention = strings.TrimSpace(mention)
		if mention == "" {
			continue
		}
		if resolveSeat != nil {
			if seat, isCard := resolveSeat(mention); isCard {
				// 卡号：命中当前席位则唤醒；空座则落空（不唤醒任何人）。
				add(seat)
				continue
			}
		}
		add(mention)
	}
	if msg.ReplyTo > 0 {
		add(replyAuthor)
	}
	return out
}

// IsAddressed 判断消息是否携带任何寻址（显式 @ 或 reply_to）。空 actor 的
// reply_to 不算寻址（找不到原作者时按无寻址处理）。
func IsAddressed(msg proto.RoomMessage, replyAuthor string) bool {
	if msg.BySystem || msg.Kind == proto.RoomMsgPointer {
		return false
	}
	for _, m := range msg.Mentions {
		if strings.TrimSpace(m) != "" {
			return true
		}
	}
	return msg.ReplyTo > 0 && strings.TrimSpace(replyAuthor) != ""
}
