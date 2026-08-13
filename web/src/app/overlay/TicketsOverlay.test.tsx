import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { TicketsOverlay } from './TicketsOverlay'
import type { GlobalTickets } from './useGlobalTickets'

const item = {
  ticket: { id: 'K1', kind: 'question', question: '要不要加重试？' },
  task: { id: 'T1', name: '重构工单通道', machine: '', work_dir: '/w/b2-b3', project_id: 'P1' },
} as unknown as GlobalTickets['items'][number]

const tickets: GlobalTickets = { items: [item], count: 1, refresh: vi.fn() }

describe('TicketsOverlay', () => {
  it('列出工单并标注它属于哪个任务与目录', () => {
    render(<TicketsOverlay tickets={tickets} onOpenTask={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByRole('dialog', { name: /工单/ })).toBeInTheDocument()
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.getByText('/w/b2-b3')).toBeInTheDocument()
  })

  it('每行有「跳到该任务」，点了关闭弹层', () => {
    const onOpenTask = vi.fn()
    const onClose = vi.fn()
    render(<TicketsOverlay tickets={tickets} onOpenTask={onOpenTask} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: '跳到该任务' }))
    expect(onOpenTask).toHaveBeenCalledWith(null, 'T1')
    expect(onClose).toHaveBeenCalled()
  })

  it('一张工单都没有时给出明确空态，不是空白', () => {
    render(<TicketsOverlay tickets={{ items: [], count: 0, refresh: vi.fn() }} onOpenTask={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText(/没有待处理的工单/)).toBeInTheDocument()
  })
})
