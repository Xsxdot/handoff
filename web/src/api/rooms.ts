// 协作房间域（B156.2）的 TS 契约镜像。与 internal/proto/rooms.go 逐字段
// 对应；形状由 Go 金样本（internal/proto/rooms_fixture_test.go）与本目录
// testdata/RoomsFixture.json 孪生锁定，改形状先回 contract 节点。
import type { LedgerEvent } from './ledger'

export const ROOM_MSG_ESCALATION = 'escalation'
export const ROOM_MSG_DEVIATION = 'deviation'
export const ROOM_MSG_CLOSING = 'closing'
export const ROOM_MSG_RELAY = 'relay'
export const ROOM_MSG_REPLY = 'reply'
export const ROOM_MSG_USER = 'user'
export const ROOM_MSG_POINTER = 'pointer'

export type RoomMsgKind =
  | 'escalation'
  | 'deviation'
  | 'closing'
  | 'relay'
  | 'reply'
  | 'user'
  | 'pointer'

// RoomMessage 是 room_message 账本事件的载荷 schema。
export interface RoomMessage {
  room: string
  kind: RoomMsgKind
  body: string
  refs?: string[]
  mentions?: string[]
  decision_id?: number
  by_system?: boolean
}

export const INBOX_ORIGIN_DECISION = 'decision'
export const INBOX_ORIGIN_TICKET = 'ticket'
export const INBOX_ORIGIN_MENTION = 'mention'

export type InboxOrigin = 'decision' | 'ticket' | 'mention'

// InboxItem 是待回复收件箱的聚合条目（三源：open 裁决 / 兜底工单 / @提及）。
export interface InboxItem {
  origin: InboxOrigin
  title: string
  card_id?: string
  ref_id: string
  payload?: unknown
}

// RoomSummary 是会话列表（扁平活动排序）的单行。
export interface RoomSummary {
  id: string
  kind: 'card' | 'project' | 'global'
  project?: string
  title: string
  bound_session?: string
  live: boolean
  read_only: boolean
  last_activity: string
}

export type RoomHistoryItem = LedgerEvent

// fetch 函数随实现节点接线（契约交棒欠账 #9）；本提交只冻结类型镜像。
