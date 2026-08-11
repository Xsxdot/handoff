// 审批台交互测试：批准/拒绝/提问的提交契约与「拒绝必须带理由」的 UI 硬约束。
//
// 浏览器点「批准」与 CLI 敲 `reply --approve` 是同一件事：批准提交的 answer 必须
// 恒为 "allow"；拒绝必须带理由（否则 UI 阻止提交）且编码为 "deny: <理由>"；
// 提问自由文本原样透传。权限/提问全文完整展示、不截断。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { Ticket } from '../../api/types'
import { TicketsPanel } from './TicketsPanel'

// vitest 未开 globals 时 RTL 不会自动注册 cleanup，这里显式接上，避免跨用例
// 残留 DOM 导致查询命中多个元素。
afterEach(cleanup)

const gateTicket: Ticket = {
  id: 'tk-gate',
  task_id: 't1',
  kind: 'gate',
  request: { kind: 'gate', permission: 'Bash: go test ./...' },
  created_at: '2026-08-11T10:30:00+08:00',
}

const askTicket: Ticket = {
  id: 'tk-ask',
  task_id: 't1',
  kind: 'ask',
  request: { kind: 'ask', question: '表结构用单数还是复数?' },
  created_at: '2026-08-11T10:30:00+08:00',
}

describe('TicketsPanel 审批交互', () => {
  it('批准 gate 工单提交 answer=allow', async () => {
    const onReply = vi.fn().mockResolvedValue(undefined)
    render(<TicketsPanel tickets={[gateTicket]} disabled={false} onReply={onReply} />)
    fireEvent.click(screen.getByRole('button', { name: /批准/ }))
    await waitFor(() => expect(onReply).toHaveBeenCalledWith(gateTicket, 'allow'))
  })

  it('拒绝必须填理由才能提交（硬约束）', async () => {
    const onReply = vi.fn().mockResolvedValue(undefined)
    render(<TicketsPanel tickets={[gateTicket]} disabled={false} onReply={onReply} />)
    fireEvent.click(screen.getByRole('button', { name: '拒绝' }))

    // 理由为空：提交按钮禁用，且给出明确提示
    const submit = screen.getByRole('button', { name: '提交拒绝' })
    expect(submit).toBeDisabled()
    expect(screen.getByText('拒绝必须填写理由')).toBeInTheDocument()
    expect(onReply).not.toHaveBeenCalled()

    // 填了理由才能提交，编码为 "deny: <理由>"
    fireEvent.change(screen.getByPlaceholderText(/例如：这个命令有破坏性/), {
      target: { value: '太危险' },
    })
    expect(submit).toBeEnabled()
    fireEvent.click(submit)
    await waitFor(() => expect(onReply).toHaveBeenCalledWith(gateTicket, 'deny: 太危险'))
  })

  it('提问工单自由文本原样透传', async () => {
    const onReply = vi.fn().mockResolvedValue(undefined)
    render(<TicketsPanel tickets={[askTicket]} disabled={false} onReply={onReply} />)
    fireEvent.change(screen.getByPlaceholderText(/输入你的回答/), {
      target: { value: '单数' },
    })
    fireEvent.click(screen.getByRole('button', { name: '提交回答' }))
    await waitFor(() => expect(onReply).toHaveBeenCalledWith(askTicket, '单数'))
  })

  it('权限/提问全文完整展示、不截断', () => {
    const longPermission = `Bash: ${'x'.repeat(500)}` // 远超事件摘要截断线（200 字符）
    render(
      <TicketsPanel
        tickets={[{ ...gateTicket, request: { kind: 'gate', permission: longPermission } }]}
        disabled={false}
        onReply={vi.fn()}
      />,
    )
    expect(screen.getByText(longPermission)).toBeInTheDocument()
  })

  it('应答失败把 agentd 错误原文透出，不吞成「操作失败」', async () => {
    const onReply = vi.fn().mockRejectedValue(new Error('任务当前状态不允许该操作'))
    render(<TicketsPanel tickets={[gateTicket]} disabled={false} onReply={onReply} />)
    fireEvent.click(screen.getByRole('button', { name: /批准/ }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('任务当前状态不允许该操作'),
    )
  })

  it('断线时全部控件禁用', () => {
    render(<TicketsPanel tickets={[gateTicket]} disabled onReply={vi.fn()} />)
    expect(screen.getByRole('button', { name: /批准/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: '拒绝' })).toBeDisabled()
  })
})
