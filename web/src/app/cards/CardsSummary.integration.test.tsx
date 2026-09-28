// Exercise the real fetchCards JSON boundary and the same normalized summary
// consumed by the CardsPage and task-board filter.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { fetchCards, currentUnlinkedTaskIds } from '../../api/ledger'
import type { Task } from '../../api/types'
import { unlinkedOnly } from '../board/columns'
import { CardsPage } from './CardsPage'

afterEach(() => vi.unstubAllGlobals())

describe('未挂账摘要真实 JSON 消费链', () => {
  it('把每种 Go wire 状态经真实 fetchCards 解码后交给卡片页', async () => {
    const task = { target: 'linux-01', task_id: 'T-unlinked', title: '未挂账任务', state: 'running' }
    const now = Date.now()
    const states = [
      { name: 'latest', expected: 'latest', summary: { status: 'latest', observed_at: new Date(now).toISOString(), count: 1, tasks: [task], unknown_targets: [] } },
      { name: 'partial', expected: 'partial', summary: { status: 'partial', observed_at: new Date(now).toISOString(), count: 1, tasks: [task], unknown_targets: ['linux-02'] } },
      { name: 'expired latest', expected: 'stale', summary: { status: 'latest', observed_at: new Date(now - 30_001).toISOString(), count: 1, tasks: [task], unknown_targets: [] } },
      { name: 'stale', expected: 'stale', summary: { status: 'stale', observed_at: '2020-01-01T00:00:00Z', count: 1, tasks: [task], unknown_targets: [] } },
      { name: 'unavailable', expected: 'unavailable', summary: { status: 'unavailable', observed_at: null, count: 0, tasks: [], unknown_targets: ['linux-02'] } },
      { name: 'missing status', expected: 'unavailable', summary: { observed_at: new Date(now).toISOString(), count: 1, tasks: [task], unknown_targets: [] } },
      { name: 'unknown status', expected: 'unavailable', summary: { status: 'future', observed_at: new Date(now).toISOString(), count: 1, tasks: [task], unknown_targets: [] } },
      { name: 'malformed count', expected: 'unavailable', summary: { status: 'latest', observed_at: new Date(now).toISOString(), count: 2, tasks: [task], unknown_targets: [] } },
    ]
    let wire: { cards: unknown[]; unlinked: unknown } = { cards: [], unlinked: states[0].summary }
    const json = (body: unknown) => new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.startsWith('/api/cards')) return json(wire)
      if (path === '/api/decisions?open=1') return json({ decisions: [] })
      if (path === '/api/ledger/health') return json({ enabled: true, mirror: [] })
      if (path === '/api/queue') return json({ queue: [] })
      if (path === '/api/flows') return json({ workflows: [], templates: [] })
      if (path === '/api/tasks?scope=all') return json({ tasks: [] })
      throw new Error(`unexpected request: ${path}`)
    }))

    const tasks = [{ id: 'T-unlinked' }, { id: 'T-other' }] as Task[]
    for (const state of states) {
      wire = { cards: [], unlinked: state.summary }
      const decoded = await fetchCards()
      expect(decoded.unlinked, state.name).toEqual(JSON.parse(JSON.stringify(state.summary)))
      const ids = currentUnlinkedTaskIds(decoded.unlinked, now)
      if (state.expected === 'latest') {
        expect([...ids ?? []]).toEqual(['T-unlinked'])
        expect(unlinkedOnly(tasks, ids).map((entry) => entry.id)).toEqual(['T-unlinked'])
      } else {
        expect(ids, state.name).toBeNull()
        expect(unlinkedOnly(tasks, ids).map((entry) => entry.id)).toEqual(['T-unlinked', 'T-other'])
      }

      const rendered = render(
        <MemoryRouter initialEntries={['/cards']}>
          <Routes><Route path="/cards" element={<CardsPage />} /></Routes>
        </MemoryRouter>,
      )
      if (state.expected === 'latest' && state.summary.count === 0) {
        expect(screen.queryByTestId('unlinked-summary-row')).not.toBeInTheDocument()
      } else {
        const row = await screen.findByTestId('unlinked-summary-row')
        expect(row, state.name).toHaveAttribute('data-status', state.expected)
        if (state.expected === 'stale') expect(row).toHaveTextContent('UTC')
      }
      rendered.unmount()
    }
  })
})
