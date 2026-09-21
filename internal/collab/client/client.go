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
	"errors"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrNotFound 是出站账本能力「对象不存在」的会话侧哨兵（架构法第九条：接口
// 归使用方）。实现侧（internal/ledger/api.Facade）把账本 ErrNotFound 翻译成
// 它；门面据它映射 collab.ErrNoRoom。接口不 import ledger，故哨兵只能定义
// 在使用方一侧——否则把账本错误类型泄进接口。
var ErrNotFound = errors.New("collab: 账本对象不存在")

// LedgerClient 会话子系统消费账本能力的唯一通道。方法集与契约文档
// §3.4 一一对应；扩方法先回 contract 节点。
type LedgerClient interface {
	// GetCard 读卡（含 driver_session 绑定投影与终态判定所需字段）。
	GetCard(id string) (proto.Card, error)
	// ListActiveCards 列项目范围内「已开始未结束」的卡（成员派生规则的输入；
	// project 空 = 跨项目）。Status ∉ {已完成, 终止} 即在列。
	ListActiveCards(project string) ([]proto.Card, error)
	// ListAllCards 列项目范围内全部卡（含终态与并入成员）：账本
	// ListCards{IncludeTerminal:true} 的直通镜像（B156.2 岔口一方案甲还债
	// 直通）。终态房间「沉底可列」与并入只读判定（Following 非空）的唯一
	// 枚举源。注意 Following 只在列表方法上有值：GetCard 走单卡读、不派生
	// 跟随态，恒为空串。
	ListAllCards(project string) ([]proto.Card, error)
	// RecordRoomMessage 发布房间消息事件（卡房间 cardID=卡号；群级传空串）。
	RecordRoomMessage(cardID string, msg proto.RoomMessage, actor string) (int64, error)
	// RecordMessageConsumed 落恰好一次的消费标记（幂等由账本事务保证）。
	RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error
	// EventsFromAsc 升序游标读事件（cardIDs 空 = 全流含群级无卡事件）。
	EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error)
	// --- B358 会话（群）域账本能力 ---
	// CreateSession 建一场会话（群），返回带分配 id 的会话本体。
	CreateSession(title, owner, actor string) (proto.Session, error)
	// GetSession 读单会话；不存在返回错误（由组装点翻译为 ErrNoRoom）。
	GetSession(id string) (proto.Session, error)
	// ListSessions 列全部会话（含归档），按创建序。
	ListSessions() ([]proto.Session, error)
	// ArchiveSession 显式归档会话；归档后只读，幂等。
	ArchiveSession(id, actor string) error
	// JoinCardToSession 把卡拉进会话；卡已属其它会话时返回错误。
	JoinCardToSession(sessionID, cardID, actor string) error
	// LeaveCardToSession 把卡移出会话；幂等。
	LeaveCardToSession(sessionID, cardID, actor string) error
	// SessionOfCard 返回该卡当前所属会话 id；不属于任何会话返回空串。
	SessionOfCard(cardID string) (string, error)
	// AddSessionMember 把一个显式成员（人或主 agent 外部会话身份）记入会话。
	AddSessionMember(sessionID, identity, actor string) error
	// BindDriver 绑定/换绑（expect=CAS 前值；实现侧落 EvDriverTakeover 审计）。
	BindDriver(id, session, carrier, expect string) error
	// DriverLease 读绑定者活性租约：过期时刻 + 行是否存在。不过滤过期，
	// 活性判定由调用方按同一时钟做。
	DriverLease(session string) (time.Time, bool, error)
}
