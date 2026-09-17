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
  reply_to?: number // 被回复消息的账本 seq；隐式寻址原作者；omitempty——缺键≠0
  // device 端戳（B358.9）：发送端登记的设备名，只用于落款显示；不参与任何判定。
  // 缺失省键——少的是落款，不是权力。
  device?: string
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

// ---- B358 会话（群）域 wire 形状（§3.2 键集；与 internal/proto/sessions.go 逐字段对应，
// 孪生金样本 = testdata/RoomsFixture.json + internal/proto/sessions_fixture_test.go，改形状先回 contract）----

export const SESSION_KIND = 'session'

export type SessionMemberKind = 'human' | 'agent' | 'seat'
// 「看板不说谎」：与 proto.SessionMember* 状态词表逐值一致（sessions_fixture_test.go 冻结）。
export type SessionMemberStatus = 'working' | 'listening' | 'last_active' | 'empty'

export interface SessionMember {
  identity: string
  kind: SessionMemberKind
  card_id?: string
  card_title?: string
  status: SessionMemberStatus
  last_active?: string
}

export interface SessionCard { card_id: string; title?: string; status?: string; seat?: string }

export interface SessionSummary {
  id: string
  kind: string
  title: string
  owner: string
  archived: boolean
  unread: number
  needs_human: boolean
  last_activity: string
  preview?: RoomPreview
  members?: SessionMember[]
  cards?: SessionCard[]
}

export interface Session {
  id: string; title: string; owner: string; archived: boolean
  members?: string[]; cards?: string[]
  created_at: string; updated_at: string
}

export interface SessionNode { card_id: string; title?: string; node: string; round?: number; state: string; target?: string }

export type SessionTimelineKind =
  | 'created' | 'archived' | 'card_joined' | 'card_left'
  | 'seat_bound' | 'seat_rebound' | 'needs_human' | 'card_closed'

export interface SessionTimelineEvent {
  seq: number; kind: SessionTimelineKind
  card_id?: string; detail?: string; actor?: string
  created_at: string
}

export interface SessionDetail { summary: SessionSummary; nodes?: SessionNode[]; timeline?: SessionTimelineEvent[] }

// ---- C8 接线（契约 §3.5 端点 + §3.6 收件箱；响应信封形状与 C6 handler 逐字一致）----
import { deleteJSON, postJSON, request } from './client'

// RoomsPage 是 GET /api/rooms 的分页信封（B374，契约 §3.5）：rooms 恒出、
// has_more 恒出、next_cursor 仅非空时出键。与 internal/proto/rooms.go 逐字段对应。
export interface RoomsPage {
  rooms: RoomSummary[]
  next_cursor?: string
  has_more: boolean
}

// fetchRooms 会话列表（GET /api/rooms?project=&cursor=&limit=）。
//
// 首屏必须显式传 limit（拍板 P4：不带 limit 也不带 cursor 即旧客户端，服务端 426）。
// next_cursor 原样回传取下一页；has_more=false 终止续载。
export const fetchRooms = (
  opts: { project?: string; cursor?: string; limit?: number } = {},
): Promise<RoomsPage> => {
  const q = new URLSearchParams()
  if (opts.project) q.set('project', opts.project)
  if (opts.cursor) q.set('cursor', opts.cursor)
  if (opts.limit !== undefined) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return request<RoomsPage>(`/api/rooms${qs ? `?${qs}` : ''}`)
}

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

// ---- B358.6 会话六端点（B358.4 已收口的 /api/sessions 面；发言/已读/历史复用上方
// sendRoomMessage/markRoomRead/fetchRoomMessages——会话号作 {id}，签名零改动）----

// fetchSessions 会话列表（GET /api/sessions）：member 维度服务端注入（web:<host>），
// 前端不得自报身份——不设 member 参数（与 S4 缺陷族 5 反例镜像）。
export const fetchSessions = (): Promise<SessionSummary[]> =>
  request<{ sessions: SessionSummary[] }>('/api/sessions').then((r) => r.sessions ?? [])

// fetchSessionDetail 会话详情三块（GET /api/sessions/{id}）。
export const fetchSessionDetail = (id: string): Promise<SessionDetail> =>
  request<SessionDetail>(`/api/sessions/${encodeURIComponent(id)}`)

// createSession 建会话（POST /api/sessions {title, owner}）：owner 统一记法
// user:<name>/agent:<name>（服务端前缀校验为权威，客户端仅预检）；审计 actor
// 服务端注入——请求体无该字段。
export const createSession = (title: string, owner: string): Promise<Session> =>
  postJSON<Session>('/api/sessions', { title, owner })

// archiveSession 归档会话（POST …/archive，幂等）。控制台本期无归档入口
// （CLI `session archive` 是操作面），函数保留为六端点镜像完整性。
export const archiveSession = (id: string): Promise<{ ok: boolean }> =>
  postJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/archive`, {})

// addSessionMember 以当前身份加入会话（POST /api/sessions/{id}/members，B366：
// 控制台 403「以当前身份加入会话」一键的后端）。成员身份服务端权威：identity
// 缺省不出键（空体）——服务端以注入 actor 入列，前端不自报身份（与 fetchSessions
// 同款纪律，B358.4 门禁）；identity 参数保留为端点形状镜像，服务端仍校验统一
// 记法前缀并忽略塞值（TestSessionMemberAddEndpoint ③④ 反例）。
export const addSessionMember = (id: string, identity = ''): Promise<{ ok: boolean }> => {
  const payload: { identity?: string } = {}
  if (identity !== '') payload.identity = identity
  return postJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/members`, payload)
}

// joinSessionCard 拉卡进群（POST …/cards {card}；进群 ≠ 配人）。
export const joinSessionCard = (id: string, card: string): Promise<{ ok: boolean }> =>
  postJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/cards`, { card })

// leaveSessionCard 移卡出群（DELETE …/cards/{cardID}，幂等）。控制台本期无入口，镜像保留。
export const leaveSessionCard = (id: string, card: string): Promise<{ ok: boolean }> =>
  deleteJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/cards/${encodeURIComponent(card)}`, {})
