// rooms.ts fetch 函数的端点契约测试（B156.2 C8）。
//
// 逐端点断言 URL/方法/请求体形状；并复用 testdata/RoomsFixture.json（契约 §6
// 金样本）做解码断言——澄清三：C6 httptest 全链一发 + 本文件对同一夹具断言，
// 链条经孪生夹具闭合。RoomSummary 不在金样本（台账 D2），用内联样本断言。
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  addSessionMember,
  archiveSession,
  createSession,
  fetchIdentity,
  fetchInbox,
  fetchRoomMessages,
  fetchRooms,
  fetchSessionDetail,
  fetchSessions,
  joinSessionCard,
  leaveSessionCard,
  markRoomRead,
  sendRoomMessage,
} from './rooms'
import fixture from './testdata/RoomsFixture.json'
import type { SessionDetail, SessionSummary } from './rooms'

const cases = fixture as {
  case: string
  message?: { room: string; kind: string; body: string; refs?: string[]; mentions?: string[]; decision_id?: number }
  item?: { origin: string; title: string; card_id?: string; ref_id: string; payload?: unknown }
  summary?: SessionSummary
  detail?: SessionDetail
}[]

function jsonResp(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

const roomsFixture: unknown = [
  {
    id: 'B1',
    kind: 'card',
    project: 'handoff',
    title: 'B1 卡会话',
    bound_session: 'console:alice@box',
    live: true,
    read_only: false,
    last_activity: '2026-08-26T08:00:00.123456789+08:00',
    unread: 0,
    attach: { target: 'devbox', task_id: 'T1', work_dir: '/w/B1', command: 'handoff attach T1' },
    preview: { body: '真实 HTTP preview', seq: 7, created_at: '2026-08-26T08:00:00.123456789+08:00' },
  },
  {
    id: 'project:handoff',
    kind: 'project',
    title: 'handoff 项目群',
    live: false,
    read_only: false,
    last_activity: '2026-08-26T07:00:00+08:00',
    unread: 0,
  },
]

afterEach(() => vi.unstubAllGlobals())

describe('fetchRooms', () => {
  it('首屏显式带 limit=50，project 走查询串，解包信封', async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(jsonResp({ rooms: roomsFixture, has_more: false })))
    vi.stubGlobal('fetch', fetchMock)
    const page = await fetchRooms({ project: 'handoff', limit: 50 })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/rooms?project=handoff&limit=50')
    expect(page.rooms).toHaveLength(2)
    // last_activity 原样直通（time.Time RFC3339Nano → TS string），不做格式转换
    expect(page.rooms[0].last_activity).toBe('2026-08-26T08:00:00.123456789+08:00')
    expect(page.rooms[0].attach).toEqual({
      target: 'devbox', task_id: 'T1', work_dir: '/w/B1', command: 'handoff attach T1',
    })
  })

  it('续载原样回传 cursor，不带 limit', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResp({ rooms: [], has_more: false }))
    vi.stubGlobal('fetch', fetchMock)
    await fetchRooms({ cursor: 'eyJhIjoxLCJyIjoiQjQyIn0' })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/rooms?cursor=eyJhIjoxLCJyIjoiQjQyIn0')
  })

  it('缺失/零值可分辨：has_more=false 且 next_cursor 缺席 vs has_more=true 且非空', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResp({ rooms: [], has_more: false }))
      .mockResolvedValueOnce(jsonResp({ rooms: [], has_more: true, next_cursor: 'C1' }))
    vi.stubGlobal('fetch', fetchMock)
    const last = await fetchRooms({ limit: 50 })
    expect(last.has_more).toBe(false)
    expect(last.next_cursor).toBeUndefined()
    const more = await fetchRooms({ limit: 50 })
    expect(more.has_more).toBe(true)
    expect(more.next_cursor).toBe('C1')
  })

  it('bound_session 缺席不出键：解码为 undefined 而非空串（可空 vs 零值）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResp({ rooms: [{ id: 'global', kind: 'global', title: '全员群', live: false, read_only: false, last_activity: 'x', unread: 0 }], has_more: false }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const [room] = (await fetchRooms({ limit: 50 })).rooms
    expect(room.bound_session).toBeUndefined()
    expect(room.unread).toBe(0)
    expect(room.attach).toBeUndefined()
  })
})

describe('fetchRoomMessages', () => {
  it('GET /api/rooms/{id}/messages，before/limit 走查询串', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ messages: [] }))
    vi.stubGlobal('fetch', fetchMock)
    await fetchRoomMessages('B1', { before: 10, limit: 200 })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/rooms/B1/messages?before=10&limit=200')
  })

  it('解码金样本 message 为 LedgerEvent 载荷（澄清三：对同一夹具断言）', async () => {
    const escalation = cases.find((c) => c.case === 'escalation-full')!.message!
    const events = [
      { seq: 7, card_id: 'B156', type: 'room_message', actor: 'cli:me@box', payload: escalation, created_at: '2026-08-25T10:00:00+08:00' },
    ]
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ messages: events }))
    vi.stubGlobal('fetch', fetchMock)
    const messages = await fetchRoomMessages('B156')
    const payload = messages[0].payload as { kind: string; refs?: string[]; decision_id?: number }
    expect(messages[0].seq).toBe(7)
    expect(payload.kind).toBe('escalation')
    expect(payload.refs).toHaveLength(2)
    expect(payload.decision_id).toBe(7)
  })
})

describe('sendRoomMessage', () => {
  it('POST /api/rooms/{id}/messages，body 形状 body/mentions（refs 空不出现）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ seq: 99 }))
    vi.stubGlobal('fetch', fetchMock)
    await sendRoomMessage('B1', '先停一下', { mentions: ['user:sy'] })
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/rooms/B1/messages')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ body: '先停一下', mentions: ['user:sy'] })
  })
})

describe('markRoomRead', () => {
  it('POST /api/rooms/{id}/read，body {upto_seq}', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)
    await markRoomRead('B1', 42)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/rooms/B1/read')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ upto_seq: 42 })
  })
})

describe('fetchInbox', () => {
  it('GET /api/inbox，解码金样本三源（澄清三）', async () => {
    const items = cases.filter((c) => c.item !== undefined).map((c) => c.item!)
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ items }))
    vi.stubGlobal('fetch', fetchMock)
    const inbox = await fetchInbox()
    expect(inbox.map((i) => i.origin)).toEqual(['decision', 'ticket', 'mention'])
    const decision = inbox.find((i) => i.origin === 'decision')!
    expect(decision.card_id).toBe('B156')
    expect(decision.ref_id).toBe('7')
    expect(decision.payload).toBeDefined()
    const ticket = inbox.find((i) => i.origin === 'ticket')!
    expect(ticket.card_id).toBeUndefined()
  })
})

// —— B358.6 会话端点契约（六端点 + 复用面）：URL/方法/请求体逐端点断言，
// 并对同一 fixture 做解码断言（序列化边界穿真实 fetch stub → request() 解析）。——

describe('fetchSessions (B358.6)', () => {
  it('GET /api/sessions 恰此 URL（无 member 参数——身份服务端注入），解包 sessions 数组', async () => {
    const summary = cases.find((c) => c.case === 'session-summary-golden')!.summary
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResp({ sessions: [summary] })))
    vi.stubGlobal('fetch', fetchMock)
    const sessions = await fetchSessions()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/sessions')
    expect(sessions).toHaveLength(1)
    expect(sessions[0].members![1].kind).toBe('seat')
  })
})

describe('fetchSessionDetail (B358.6)', () => {
  it('GET /api/sessions/{id}，id 走 encodeURIComponent，解码金样本 detail', async () => {
    const detail = cases.find((c) => c.case === 'session-detail-golden')!.detail
    const fetchMock = vi.fn().mockResolvedValue(jsonResp(detail))
    vi.stubGlobal('fetch', fetchMock)
    const out = await fetchSessionDetail('session:7')
    expect(fetchMock.mock.calls[0][0]).toBe('/api/sessions/session%3A7')
    expect(out.timeline).toHaveLength(6)
  })
})

// —— B366 补员端点（控制台「以当前身份加入会话」一键的后端镜像）：成员身份
// 服务端权威，identity 缺省不出键（前端不自报身份，与 fetchSessions 同款纪律）。——

describe('addSessionMember (B366)', () => {
  it('POST /api/sessions/{id}/members 空体 {}——identity 不出键（前端不自报身份）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)
    await addSessionMember('session:7')
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/sessions/session%3A7/members')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({})
  })

  it('显式 identity 原样出键（端点形状镜像；服务端仍校验统一记法并忽略塞值）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)
    await addSessionMember('session:7', 'user:sy')
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/sessions/session%3A7/members')
    expect(JSON.parse(init.body as string)).toEqual({ identity: 'user:sy' })
  })
})

describe('session 写面 (B358.6)', () => {
  it('createSession POST {title}——不再带 owner（服务端按解析人名缺省）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ id: 'session:1', title: 'T', owner: 'user:sycm', archived: false, created_at: '', updated_at: '' }))
    vi.stubGlobal('fetch', fetchMock)
    await createSession('需求对齐')
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/sessions')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ title: '需求对齐' })
  })

  it('fetchIdentity GET /api/identity 三键解码', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResp({ member: 'user:sycm', device: 'mbp / Safari', configured: true }))
    vi.stubGlobal('fetch', fetchMock)
    const id = await fetchIdentity()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/identity')
    expect(id).toEqual({ member: 'user:sycm', device: 'mbp / Safari', configured: true })
  })

  it('joinSessionCard POST {card} / leaveSessionCard DELETE / archiveSession POST', async () => {
    // mockImplementation 而非 mockResolvedValue：本用例连续发三次请求，同一 Response
    // 实例的 body 只能读一次（与本文件 fetchRooms 用例同坑同解）
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResp({ ok: true })))
    vi.stubGlobal('fetch', fetchMock)
    await joinSessionCard('session:1', 'B1')
    const [joinUrl, joinInit] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(joinUrl).toBe('/api/sessions/session%3A1/cards')
    expect(joinInit.method).toBe('POST')
    expect(JSON.parse(joinInit.body as string)).toEqual({ card: 'B1' })
    await leaveSessionCard('session:1', 'B1')
    const [leaveUrl, leaveInit] = fetchMock.mock.calls[1] as [string, RequestInit]
    expect(leaveUrl).toBe('/api/sessions/session%3A1/cards/B1')
    expect(leaveInit.method).toBe('DELETE')
    await archiveSession('session:1')
    expect(fetchMock.mock.calls[2][0]).toBe('/api/sessions/session%3A1/archive')
  })
})
