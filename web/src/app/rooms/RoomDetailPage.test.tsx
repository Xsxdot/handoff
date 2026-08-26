// 房间页：历史分页（before 游标）+ 发送 POST + 只读禁写（已知陷阱二：disabled 不算数，
// 必须真触发提交/点击再断言没有发出 POST /api/rooms/{id}/messages 的 fetch）+ 同表正控。
// 本文件在 fetch 边界断言（stubGlobal fetch），不 mock rooms 模块。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { LedgerEvent } from '../../api/ledger'
import type { RoomHistoryItem, RoomSummary } from '../../api/rooms'
import { RoomDetailPage } from './RoomDetailPage'

function jsonResp(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

const roomB1 = (over: Partial<RoomSummary> = {}): RoomSummary => ({
  id: 'B1', kind: 'card', title: 'B1 卡会话', live: false, read_only: false,
  last_activity: '2026-08-26T08:00:00+08:00', ...over,
})

const ev = (seq: number, body: string, kind = 'user'): RoomHistoryItem =>
  ({ seq, card_id: 'B1', type: 'room_message', actor: 'cli:me@box', payload: { room: 'B1', kind, body }, created_at: '2026-08-26T08:00:00+08:00' }) as unknown as LedgerEvent as RoomHistoryItem

// fetchStub 挡下房间页全部请求；older 在 before 查询时返回。
function fetchStub(rooms: RoomSummary[], messages: RoomHistoryItem[], older: RoomHistoryItem[] = []) {
  const fn = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const method = init?.method ?? 'GET'
    if (url === '/api/rooms' && method === 'GET') return Promise.resolve(jsonResp({ rooms }))
    if (url.startsWith('/api/rooms/B1/messages') && method === 'GET') {
      return Promise.resolve(jsonResp({ messages: url.includes('before=') ? older : messages }))
    }
    if (url === '/api/rooms/B1/messages' && method === 'POST') return Promise.resolve(jsonResp({ seq: 99 }))
    if (url === '/api/rooms/B1/read' && method === 'POST') return Promise.resolve(jsonResp({ ok: true }))
    return Promise.resolve(jsonResp({}))
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

const postMessages = (fn: ReturnType<typeof vi.fn>) =>
  fn.mock.calls.filter(([input, init]) => {
    const method = (init as RequestInit | undefined)?.method ?? 'GET'
    return String(input) === '/api/rooms/B1/messages' && method === 'POST'
  })

const readPosts = (fn: ReturnType<typeof vi.fn>) =>
  fn.mock.calls.filter(([input, init]) => {
    const method = (init as RequestInit | undefined)?.method ?? 'GET'
    return String(input) === '/api/rooms/B1/read' && method === 'POST'
  })

const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/rooms/B1']}>
      <Routes>
        <Route path="/rooms/:id" element={<RoomDetailPage />} />
      </Routes>
    </MemoryRouter>,
  )

describe('房间页历史', () => {
  it('渲染最新一页，「加载更早」按 before=最老seq-1 请求并叠加', async () => {
    const fn = fetchStub([roomB1()], [ev(1, '第一条'), ev(2, '第二条')], [ev(0, '更早一条')])
    renderPage()
    expect(await screen.findByText('第一条')).toBeInTheDocument()
    expect(screen.getByText('第二条')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '加载更早' }))
    await waitFor(() => expect(screen.getByText('更早一条')).toBeInTheDocument())
    const older = fn.mock.calls.find(([input]) => String(input).includes('before=0')) as [string, RequestInit] | undefined
    expect(older).toBeDefined()
  })

  it('打开房间即已读：到新 maxSeq 置一次 markRoomRead(id, maxSeq)', async () => {
    const fn = fetchStub([roomB1()], [ev(1, 'a'), ev(2, 'b')])
    renderPage()
    await screen.findByText('a')
    await waitFor(() => expect(readPosts(fn)).toHaveLength(1))
    expect(JSON.parse(String((readPosts(fn)[0][1] as RequestInit).body))).toEqual({ upto_seq: 2 })
  })
})

describe('房间页发送', () => {
  it('可写房（read_only=false）：发送框 POST messages 且清空输入', async () => {
    const fn = fetchStub([roomB1()], [ev(1, 'hi')])
    renderPage()
    await screen.findByText('hi')
    fireEvent.change(screen.getByLabelText('消息正文'), { target: { value: '新的留言' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(postMessages(fn)).toHaveLength(1))
    expect(JSON.parse(String((postMessages(fn)[0][1] as RequestInit).body))).toEqual({ body: '新的留言' })
    await waitFor(() => expect((screen.getByLabelText('消息正文') as HTMLTextAreaElement).value).toBe(''))
  })

  it('只读房禁写（反面断言）：真触发发送点击，没有 POST messages 的 fetch', async () => {
    const fn = fetchStub([roomB1({ read_only: true })], [ev(1, '已归档')])
    renderPage()
    await screen.findByText('已归档')
    // 先填非空正文再点发送：空正文判定会先于守卫兜底，空 draft 的反面断言没有牙
    // （删守卫变异仍绿）。填了正文，read_only 守卫就是唯一拦截点，删它本用例必红。
    fireEvent.change(screen.getByLabelText('消息正文'), { target: { value: '不应发出的内容' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    // 轮询 500ms 断言仍是 0：若写路径被接上 fetch，这里必然翻红
    await expect.poll(() => postMessages(fn).length).toBe(0)
  })

  it('同表正控：同一交互在 read_only=false 房间必须打出 POST messages', async () => {
    const fn = fetchStub([roomB1({ read_only: false })], [ev(1, '正常')])
    renderPage()
    await screen.findByText('正常')
    fireEvent.change(screen.getByLabelText('消息正文'), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(postMessages(fn)).toHaveLength(1))
  })
})