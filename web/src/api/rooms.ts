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

// RoomAttach 是房间详情可执行的任务 attach 投影；target 缺席表示当前 agentd。
export interface RoomAttach {
  target?: string
  task_id: string
  work_dir: string
  command: string
}

// RoomPreview 是服务端随会话列表投影的最后一条消息摘要；列表不再逐房间读历史。
export interface RoomPreview {
  body: string
  seq: number
  created_at: string
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
  unread: number
  attach?: RoomAttach
  preview?: RoomPreview
}

export type RoomHistoryItem = LedgerEvent

// ---- C8 接线（契约 §3.5 端点 + §3.6 收件箱；响应信封形状与 C6 handler 逐字一致）----
import { postJSON, request } from './client'

// fetchRooms 会话列表（GET /api/rooms?project=）。project 省略取全部。
export const fetchRooms = (project = ''): Promise<RoomSummary[]> =>
  request<{ rooms: RoomSummary[] }>(
    `/api/rooms${project ? `?project=${encodeURIComponent(project)}` : ''}`,
  ).then((response) => response.rooms ?? [])

// fetchRoomMessages 房间历史（GET /api/rooms/{id}/messages）。before 排他游标、
// limit<=0 由服务端取 200；返回升序 room_message 事件。
export const fetchRoomMessages = (
  id: string,
  opts: { before?: number; limit?: number } = {},
): Promise<RoomHistoryItem[]> => {
  const q = new URLSearchParams()
  if (opts.before !== undefined) q.set('before', String(opts.before))
  if (opts.limit !== undefined) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return request<{ messages: RoomHistoryItem[] }>(
    `/api/rooms/${encodeURIComponent(id)}/messages${qs ? `?${qs}` : ''}`,
  ).then((response) => response.messages ?? [])
}

// sendRoomMessage 用户发言（POST /api/rooms/{id}/messages）。kind 服务端固定 user、
// actor 服务端注入；refs/mentions 为空时不出键（与 Go 侧 omitempty 一致）。
export const sendRoomMessage = (
  id: string,
  body: string,
  opts: { refs?: string[]; mentions?: string[] } = {},
): Promise<{ seq: number }> => {
  const payload: { body: string; refs?: string[]; mentions?: string[] } = { body }
  if (opts.refs !== undefined && opts.refs.length > 0) payload.refs = opts.refs
  if (opts.mentions !== undefined && opts.mentions.length > 0) payload.mentions = opts.mentions
  return postJSON<{ seq: number }>(`/api/rooms/${encodeURIComponent(id)}/messages`, payload)
}

// markRoomRead 置已读游标（POST /api/rooms/{id}/read）：打开房间即已读（spec §7）。
export const markRoomRead = (id: string, uptoSeq: number): Promise<{ ok: boolean }> =>
  postJSON<{ ok: boolean }>(`/api/rooms/${encodeURIComponent(id)}/read`, { upto_seq: uptoSeq })

// fetchInbox 待回复收件箱（GET /api/inbox）：三源聚合在 gateway 编排（契约 §3.6）。
export const fetchInbox = (): Promise<InboxItem[]> =>
  request<{ items: InboxItem[] }>('/api/inbox').then((response) => response.items ?? [])
