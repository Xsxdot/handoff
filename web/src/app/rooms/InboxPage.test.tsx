// 收件箱页：三源分区 + 决策选项按钮直调既有 answerDecision + 答复后明示文案。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { answerDecision } from '../../api/ledger'
import { fetchInbox } from '../../api/rooms'
import type { InboxItem } from '../../api/rooms'
import { InboxPage } from './InboxPage'

vi.mock('../../api/rooms', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/rooms')>()),
  fetchInbox: vi.fn(),
}))
vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  answerDecision: vi.fn().mockResolvedValue({ ok: true }),
}))

const items: InboxItem[] = [
  {
    origin: 'decision', title: '推翻级简报：契约语义冲突', card_id: 'B156', ref_id: '7',
    payload: { id: 7, card_id: 'B156', body: '要不要先退化？', options: ['重试三次', '立即退化'], status: 'open', created_by: 'cli:me@box' },
  },
  { origin: 'ticket', title: '权限工单待答复', ref_id: 'tkt-42' },
  { origin: 'mention', title: '@你：改动影响 B145', card_id: 'B145', ref_id: '918' },
]

describe('收件箱页', () => {
  beforeEach(() => vi.mocked(fetchInbox).mockResolvedValue(items))

  it('三源分区渲染', async () => {
    render(<MemoryRouter><InboxPage /></MemoryRouter>)
    expect(await screen.findByText('要不要先退化？')).toBeInTheDocument()
    expect(screen.getByText('权限工单待答复')).toBeInTheDocument()
    expect(screen.getByText('@你：改动影响 B145')).toBeInTheDocument()
    expect(screen.getByText('需要你 · 简报')).toBeInTheDocument()
    expect(screen.getByText('兜底上浮工单')).toBeInTheDocument()
  })

  it('决策选项按钮点击直调 answerDecision（既有通道，不新造）', async () => {
    render(<MemoryRouter><InboxPage /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: '立即退化' }))
    await waitFor(() => expect(vi.mocked(answerDecision)).toHaveBeenCalledWith(7, '立即退化'))
  })

  it('答复后显示「答复已落账，等待协调者唤醒」', async () => {
    render(<MemoryRouter><InboxPage /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: '重试三次' }))
    expect(await screen.findByText('答复已落账，等待协调者唤醒。')).toBeInTheDocument()
  })
})