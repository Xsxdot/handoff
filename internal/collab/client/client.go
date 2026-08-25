// Package client 是协作房间域的出站门面：会话子系统对外部账本能力的全部
// 需求面，接口定义在会话这一侧（架构法第九条「接口归使用方」）。
//
// 具体实现由组装点（main.go / internal/agentd/server.go）绑定为
// internal/ledger/api.Facade；组装点之外不得 new 他方具体类型。
//
// 本接口是测试缝（spec B156.2 测试接缝清单 #2）：单测用替身断言调用契约，
// 会话逻辑可在不起账本的情况下独立测试。
package client

import (
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// LedgerClient 会话子系统消费账本能力的唯一通道。方法集与契约文档
// §3.4 一一对应；扩方法先回 contract 节点。
type LedgerClient interface {
	// GetCard 读卡（含 driver_session 绑定投影与终态判定所需字段）。
	GetCard(id string) (proto.Card, error)
	// ListActiveCards 列项目范围内「已开始未结束」的卡（成员派生规则的输入；
	// project 空 = 跨项目）。Status ∉ {已完成, 终止} 即在列。
	ListActiveCards(project string) ([]proto.Card, error)
	// RecordRoomMessage 发布房间消息事件（卡房间 cardID=卡号；群级传空串）。
	RecordRoomMessage(cardID string, msg proto.RoomMessage, actor string) (int64, error)
	// RecordMessageConsumed 落恰好一次的消费标记（幂等由账本事务保证）。
	RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error
	// EventsFromAsc 升序游标读事件（cardIDs 空 = 全流含群级无卡事件）。
	EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error)
	// BindDriver 绑定/换绑（expect=CAS 前值；实现侧落 EvDriverTakeover 审计）。
	BindDriver(id, session, carrier, expect string) error
	// DriverLease 读绑定者活性租约：过期时刻 + 行是否存在。不过滤过期，
	// 活性判定由调用方按同一时钟做。
	DriverLease(session string) (time.Time, bool, error)
}
