// room_narrator.go —— 叙事落点的房间实现（B233.22 自 agentd/server.go 拆出归域）。
//
// 容器 k_agentd_roomNarrator 应然域为 d_keystone：本类型是 keystone 叙事落点的
// 房间适配器，按 keysclient.Narrator 使用方接口实现，构造经 cmd 组装点注入
// keystone.New。职责与行为不变。
package keystone

import (
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/proto"
)

// roomNarrator 是叙事落点的房间实现：B156.2 房间制已落地，按 keysclient.Narrator
// 预告的换绑路径把协调者叙事从卡 note 迁到卡房间——薄里程碑指针行（仅系统组件
// 可书）。本路经 d_collab 入站门面的指针专用入口 Service.Pointer：kind=pointer
// 与 BySystem=true 由 Pointer 自己置，房间解析与只读判定也归 collab 执法；
// keystone 不感知差异。凡承重必须落账，通道不再是兜底通道。
//
// 当前实况（协调者复核，2026-08-26 更新）：Pointer 已由 C4 子卡填肉并入功能线
// （归属一度写作 C7，后改判给 C4），上一段描述的即是它今天的真实行为——房间解析
// 走 room.Resolve、只读/终态房返回 ErrReadOnly、kind 与 BySystem 由 Pointer 自置。
// 本路因此是 Service.Pointer 在仓内的**第一个上游消费方**。
//
// 连带一条给下游子卡的判据：Pointer 落账的 actor 是 collab 包内常量
// "system:pointer"，proto.RoomMessage 也没有字段记「哪个系统组件写的」，而签名
// 已冻结且不含 actor 参数。所以本路的指针行与 C7 的派发指针行在账本里只能靠正文
// 区分——针对指针行的断言一律写成存在式，不要写成计数式（「恰好一条」「行数 +1」），
// 否则两条上游都活着的时候会互相把对方变成偶发红。
type roomNarrator struct {
	c *collab.Service
}

func (n roomNarrator) Say(cardID, text string) error {
	_, err := n.c.Pointer(cardID, proto.RoomMessage{Body: text})
	return err
}

// NewRoomNarrator 构造房间叙事适配器（B233.18：构造上移 cmd 组装点；返回
// keysclient.Narrator 使用方接口）。
func NewRoomNarrator(c *collab.Service) keysclient.Narrator { return roomNarrator{c: c} }
