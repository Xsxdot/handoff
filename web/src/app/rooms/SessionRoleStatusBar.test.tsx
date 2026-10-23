import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { fetchTaskDetail, replyTicket } from '../../api/client'
import type { SessionDetail, SessionRoleStatusProjection } from '../../api/rooms'
import { SessionRoleStatusBar } from './SessionRoleStatusBar'
import golden from '../../api/testdata/SessionDetailB456.json'

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  fetchTaskDetail: vi.fn(),
  replyTicket: vi.fn(),
}))

const ticket = { id: 'ticket-draft', task_id: 'task-draft', kind: 'ask', request: { kind: 'ask', question: '请说明下一步怎么选。' }, created_at: '2026-10-23T07:00:00Z' }
const projection = (observedAt: string): SessionRoleStatusProjection => ({ observed_at: observedAt, roles: [{
  id: 'coordinator:B456', kind: 'coordinator', identity: 'B456', card_id: 'B456', state: 'needs_you',
  conclusion: '有一项需要你决定', ticket_refs: [{ card_id: 'B456', target: 'local', task_id: 'task-draft', ticket_id: 'ticket-draft', task_type: 'question' }],
}] })

beforeEach(() => vi.clearAllMocks())

describe('SessionRoleStatusBar ticket snapshots', () => {
  it('restores focus through nested role detail and ticket drawer closes', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ task: {} as never, pending_tickets: [ticket], recent_events: [] })
    const user = userEvent.setup()
    const { roles } = projection('2026-10-23T07:05:00Z')
    render(<SessionRoleStatusBar sessionId="session:b456" archived={false} summary={null} projection={{ observed_at: '2026-10-23T07:05:00Z', roles }} />)

    const banner = await screen.findByRole('button', { name: /协调者 · B456：有 1 项需要你处理/ })
    await user.click(banner)
    const roleDialog = await screen.findByRole('dialog', { name: /协调者 · B456/ })
    const openTickets = within(roleDialog).getByRole('button', { name: '查看待处理事项' })
    await user.click(openTickets)
    expect(await screen.findByRole('dialog', { name: '待你处理' })).toBeInTheDocument()

    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '待你处理' })).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(openTickets))
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /协调者 · B456/ })).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(banner))
  })

  it('consumes the real Go-serialized B456 JSON without requiring optional since fields', async () => {
    const detail = golden as SessionDetail
    const projection = detail.role_status!
    const coordinator = projection.roles.find((role) => role.kind === 'coordinator')!
    const executor = projection.roles.find((role) => role.kind === 'executor')!
    expect(coordinator.state).toBe('exception')
    expect(coordinator.recent?.[0]).toMatchObject({ source: 'wake_round', tone: 'failed' })
    expect(executor.state).toBe('unknown')
    expect(executor.since).toBeUndefined()
    expect(executor.ticket_refs?.[0]).toMatchObject({ task_id: 'task-b456', ticket_id: 'ticket-b456' })
    vi.mocked(fetchTaskDetail).mockRejectedValue(new Error('ticket unavailable'))
    render(<SessionRoleStatusBar sessionId={detail.summary.id} projection={projection} archived={false} summary={detail.summary} />)
    expect((await screen.findAllByRole('button', { name: /协调者 · P1/ })).length).toBeGreaterThan(0)
    await userEvent.setup().click(screen.getAllByRole('button', { name: /协调者 · P1/ })[0])
    const roleDialog = await screen.findByRole('dialog', { name: /协调者 · P1/ })
    expect(roleDialog.closest('.role-status-portal')?.parentElement).toBe(document.body)
    expect(within(roleDialog).getAllByText(/载体不可用/).length).toBeGreaterThan(0)
  })

  it('read failure removes live CTA but preserves the open ticket draft until the same ticket is re-read', async () => {
    vi.mocked(fetchTaskDetail)
      .mockResolvedValueOnce({ task: {} as never, pending_tickets: [ticket], recent_events: [] })
      .mockRejectedValueOnce(new Error('mirror unreachable'))
      .mockResolvedValueOnce({ task: {} as never, pending_tickets: [ticket], recent_events: [] })
    vi.mocked(replyTicket).mockResolvedValue({ ok: true })
    const user = userEvent.setup()
    const props = { sessionId: 'session:b456', archived: false, summary: null }
    const { rerender } = render(<SessionRoleStatusBar {...props} projection={projection('2026-10-23T07:10:00Z')} />)

    await user.click(await screen.findByRole('button', { name: /需要你处理 1 件事/ }))
    const drawer = await screen.findByRole('dialog', { name: '待你处理' })
    expect(drawer.closest('.role-status-portal')?.parentElement).toBe(document.body)
    const answer = await within(drawer).findByRole('textbox', { name: '回答（原样透传给模型）' })
    await user.type(answer, '先查卡上的交付记录')

    rerender(<SessionRoleStatusBar {...props} projection={projection('2026-10-23T07:11:00Z')} />)
    expect(await within(drawer).findByRole('alert')).toHaveTextContent('mirror unreachable')
    expect(within(drawer).getByRole('textbox', { name: '回答（原样透传给模型）' })).toHaveValue('先查卡上的交付记录')
    expect(within(drawer).getByRole('button', { name: '提交回答' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: /需要你处理 1 件事/ })).toBeNull()

    rerender(<SessionRoleStatusBar {...props} projection={projection('2026-10-23T07:12:00Z')} />)
    await waitFor(() => expect(within(drawer).getByRole('button', { name: '提交回答' })).toBeEnabled())
    expect(within(drawer).getByRole('textbox', { name: '回答（原样透传给模型）' })).toHaveValue('先查卡上的交付记录')
  })

  it('a failed task read does not disable a separately verified task ticket', async () => {
    const ticketA = { ...ticket, id: 'ticket-A', task_id: 'task-A' }
    const refs = projection('2026-10-23T07:20:00Z').roles[0]
    refs.ticket_refs = [
      { card_id: 'B456', target: 'local', task_id: 'task-A', ticket_id: 'ticket-A', task_type: 'question' },
      { card_id: 'B456', target: 'local', task_id: 'task-B', ticket_id: 'ticket-B', task_type: 'question' },
    ]
    vi.mocked(fetchTaskDetail)
      .mockResolvedValueOnce({ task: {} as never, pending_tickets: [ticketA], recent_events: [] })
      .mockRejectedValueOnce(new Error('task B offline'))
    const user = userEvent.setup()
    render(<SessionRoleStatusBar sessionId="session:b456" projection={{ observed_at: '2026-10-23T07:20:00Z', roles: [refs] }} archived={false} summary={null} />)
    await user.click(await screen.findByRole('button', { name: /需要你处理 1 件事/ }))
    const drawer = await screen.findByRole('dialog', { name: '待你处理' })
    expect(await within(drawer).findByRole('alert')).toHaveTextContent('task B offline')
    expect(within(drawer).getByRole('textbox', { name: '回答（原样透传给模型）' })).toBeEnabled()
  })

  it('explains that a coordinator has not been observed when other roles are present', () => {
    render(<SessionRoleStatusBar sessionId="session:resident" archived={false} summary={null} projection={{ observed_at: '2026-10-23T07:25:00Z', roles: [{
      id: 'resident:local', kind: 'resident', state: 'unknown', conclusion: '最近消息已发送，尚未确认接续',
    }] }} />)
    expect(screen.getByText('本会话尚未观察到卡协调者。')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /驻场者/ }).length).toBeGreaterThan(0)
  })

  it('orders same-state roles by oldest valid observation, then puts missing or invalid times last by id', () => {
    const row = (id: string, since?: string) => ({
      id: `coordinator:${id}`, kind: 'coordinator' as const, identity: id, card_id: id,
      state: 'waiting' as const, conclusion: '等待接续', since,
    })
    const { container } = render(<SessionRoleStatusBar sessionId="session:sort" archived={false} summary={null} projection={{
      observed_at: '2026-10-23T07:40:00Z', roles: [
        row('missing'), row('newer', '2020-02-01T00:00:00Z'), row('bad', 'not-a-time'),
        row('older', '2020-01-01T00:00:00Z'), row('future', '2999-01-01T00:00:00Z'),
      ],
    }} />)
    const ids = [...container.querySelectorAll<HTMLElement>('[data-role-chip]')].map((node) => node.dataset.roleChip)
    expect(ids).toEqual(['coordinator:older', 'coordinator:newer', 'coordinator:bad', 'coordinator:future', 'coordinator:missing'])
  })

  it('keeps a previously verified CTA during the next poll and does not steal focus from an open detail', async () => {
    const user = userEvent.setup()
    const detail = projection('2026-10-23T07:30:00Z')
    const role = { ...detail.roles[0], ticket_refs: undefined, state: 'exception' as const, conclusion: '协调者唤醒失败' }
    const detailWithRole = (observedAt: string, conclusion: string) => ({ ...projection(observedAt), roles: [{ ...role, conclusion }] })
    vi.mocked(fetchTaskDetail)
      .mockResolvedValueOnce({ task: {} as never, pending_tickets: [ticket], recent_events: [] })
      .mockImplementationOnce(() => new Promise(() => {}))
    const props = { sessionId: 'session:b456', archived: false, summary: null }
    const { rerender } = render(<SessionRoleStatusBar {...props} projection={detail} />)
    expect(await screen.findByRole('button', { name: /需要你处理 1 件事/ })).toBeVisible()
    rerender(<SessionRoleStatusBar {...props} projection={projection('2026-10-23T07:31:00Z')} />)
    await waitFor(() => expect(fetchTaskDetail).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('button', { name: /需要你处理 1 件事/ })).toBeVisible()

    rerender(<SessionRoleStatusBar {...props} projection={detailWithRole('2026-10-23T07:32:00Z', '打开详情后更新的说明')} />)
    await user.click(screen.getAllByRole('button', { name: /协调者 · B456/ })[0])
    const dialog = await screen.findByRole('dialog', { name: /协调者 · B456/ })
    const summary = within(dialog).getByText('技术细节')
    summary.focus()
    rerender(<SessionRoleStatusBar {...props} projection={detailWithRole('2026-10-23T07:33:00Z', '轮询更新的说明')} />)
    expect(document.activeElement).toBe(summary)

    const outerKey = vi.fn()
    window.addEventListener('keydown', outerKey)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /协调者 · B456/ })).toBeNull())
    expect(outerKey).not.toHaveBeenCalled()
    await waitFor(() => expect(document.activeElement).toBe(screen.getAllByRole('button', { name: /协调者 · B456/ })[0]))
    window.removeEventListener('keydown', outerKey)
  })

  it('does not claim a prior successful observation when the first status read fails', () => {
    render(<SessionRoleStatusBar sessionId="session:first-failure" archived={false} summary={null} unavailable unavailableReason="服务暂不可达（503）" />)
    expect(screen.getByRole('status')).toHaveTextContent('服务暂不可达（503）')
    expect(screen.getByRole('status')).toHaveTextContent('尚无成功观测')
    expect(screen.queryByText('此服务暂不支持角色状态。')).toBeNull()
  })

  it('marks retained roles unknown after a later read fails and identifies the last successful observation', () => {
    const retained = { observed_at: '2026-10-23T07:10:00Z', roles: [{
      id: 'coordinator:B456', kind: 'coordinator' as const, identity: 'B456', card_id: 'B456',
      state: 'running' as const, conclusion: '协调者正在查看交付', since: '2026-10-23T07:00:00Z',
    }] }
    render(<SessionRoleStatusBar sessionId="session:stale-read" archived={false} summary={null} projection={retained} unavailable unavailableReason="连接被重置" />)
    expect(screen.getByRole('status')).toHaveTextContent('连接被重置')
    expect(screen.getByRole('status')).toHaveTextContent('角色状态未知，保留最后成功观测')
    expect(screen.getAllByRole('button', { name: /协调者 · B456，未知/ }).length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: /协调者 · B456，运行中/ })).toBeNull()
  })
})
