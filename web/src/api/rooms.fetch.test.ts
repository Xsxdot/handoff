// rooms.ts fetch 函数的端点契约测试（B156.2 C8）。
//
// 逐端点断言 URL/方法/请求体形状；并复用 testdata/RoomsFixture.json（契约 §6
// 金样本）做解码断言——澄清三：C6 httptest 全链一发 + 本文件对同一夹具断言，
// 链条经孪生夹具闭合。RoomSummary 不在金样本（台账 D2），用内联样本断言。
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  fetchInbox,
  fetchRoomMessages,
  fetchRooms,
  markRoomRead,
  sendRoomMessage,
} from './rooms'
import fixture from './testdata/RoomsFixture.json'

const cases = fixture as {
  case: string
  message?: { room: string; kind: string; body: string; refs?: string[]; mentions?: string[]; decision_id?: number }
  item?: { origin: string; title: string; card_id?: string; ref_id: string; payload?: unknown }
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
  it('GET /api/rooms，project 参数走查询串，解包 rooms 数组', async () => {
    // mockImplementation 而非 mockResolvedValue：同一 Response 的 body 只能读一次，
    // 本用例连续发两次请求，每次都要新的 Response 实例
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResp({ rooms: roomsFixture })))
    vi.stubGlobal('fetch', fetchMock)
    const rooms = await fetchRooms()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/rooms')
    expect(rooms).toHaveLength(2)
    // last_activity 原样直通（time.Time RFC3339Nano → TS string），不做格式转换
    expect(rooms[0].last_activity).toBe('2026-08-26T08:00:00.123456789+08:00')
    expect(rooms[0].live).toBe(true)
    expect(rooms[0].read_only).toBe(false)
    expect(rooms[0].bound_session).toBe('console:alice@box')
    expect(rooms[0].unread).toBe(0)
    expect(rooms[0].attach).toEqual({
      target: 'devbox', task_id: 'T1', work_dir: '/w/B1', command: 'handoff attach T1',
    })

    await fetchRooms('handoff')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/rooms?project=handoff')
  })

  it('bound_session 缺席不出键：解码为 undefined 而非空串（可空 vs 零值）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResp({ rooms: [{ id: 'global', kind: 'global', title: '全员群', live: false, read_only: false, last_activity: 'x', unread: 0 }] }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const [room] = await fetchRooms()
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
