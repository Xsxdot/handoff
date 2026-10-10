// QueuePanel.test.tsx —— 锁定卡片看板队列折叠、字段和断线保留接缝。
// 边界：只验证服务端 QueueEntry 的呈现与打开卡回调，不测试轮询实现。
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { QueueEntry } from '../../api/scheduling'
import { QueuePanel } from './QueuePanel'

const entries: readonly QueueEntry[] = [
  {
    kind: 'launch_queue',
    id: 'q1',
    card: 'B1',
    node: '进行中',
    squad: 'exec',
    priority: '高',
    ready: false,
    actor: 'wake',
    seq: 7,
    position: 1,
  },
  {
    kind: 'ignition_queue',
    id: 'q2',
    card: 'B2',
    node: '待审阅',
    squad: 'coord',
    target: 'local',
    executor: 'opencode',
    model: 'gpt-5',
    priority: '中',
    ready: true,
    actor: 'card_step',
    seq: 8,
    position: 2,
  },
]

function renderPanel(overrides: Partial<React.ComponentProps<typeof QueuePanel>> = {}) {
  return render(
    <QueuePanel
      entries={entries}
      open={false}
      loading={false}
      hasSnapshot
      disconnected={false}
      sessionExpired={false}
      errorText=""
      onToggle={vi.fn()}
      onOpenCard={vi.fn()}
      {...overrides}
    />,
  )
}

describe('QueuePanel', () => {
  it('折叠工具条显示数量，展开后按服务端位次呈现完整字段并可打开卡片', async () => {
    const onOpenCard = vi.fn()
    const user = userEvent.setup()
    renderPanel({ onOpenCard })

    expect(screen.getByRole('button', { name: '⧗ 排队中 2' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '打开 B1' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '⧗ 排队中 2' }))

    expect(screen.getByText('B1')).toBeInTheDocument()
    expect(screen.getByText('拉起')).toBeInTheDocument()
    expect(screen.getByText('进行中 · exec')).toBeInTheDocument()
    expect(screen.getByText('高')).toBeInTheDocument()
    expect(screen.getByText('未就绪')).toBeInTheDocument()
    expect(screen.getByText('wake')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '打开 B1' }))
    expect(onOpenCard).toHaveBeenCalledWith('B1')
  })

  it('断线时保留最近队列快照并显示网络断开', () => {
    renderPanel({ open: true, disconnected: true, errorText: '网络断开' })

    expect(screen.getByText('网络断开')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '打开 B1' })).toBeInTheDocument()
  })

  // S4（B426）：compact 细横条——收缩态无大边框盒（单行高），展开行为与字段原样；
  // 缺省（桌面）容器类串逐字节不动。
  it('compact 细横条：收缩态 border-b 条无 rounded-lg 大盒，展开仍列完整队列', async () => {
    const user = userEvent.setup()
    renderPanel({ compact: true })

    const toggle = screen.getByRole('button', { name: '⧗ 排队中 2' })
    const strip = toggle.closest('section')!
    expect(strip.className).toContain('border-b')
    expect(strip.className).not.toContain('rounded-lg')
    expect(screen.queryByRole('button', { name: '打开 B1' })).not.toBeInTheDocument()

    await user.click(toggle)
    expect(screen.getByText('B1')).toBeInTheDocument()
    expect(screen.getByText('拉起')).toBeInTheDocument()
    expect(screen.getByText('未就绪')).toBeInTheDocument()
  })

  it('compact collapsed count distinguishes loading, unavailable, stale and confirmed empty snapshots', () => {
    const { rerender } = renderPanel({ compact: true, entries: [], loading: true, hasSnapshot: false })
    expect(screen.getByRole('button', { name: '⧗ 排队中 …' })).toBeInTheDocument()

    rerender(<QueuePanel entries={[]} open={false} loading={false} hasSnapshot={false} disconnected errorText="断开" sessionExpired={false} onToggle={vi.fn()} onOpenCard={vi.fn()} compact />)
    expect(screen.getByRole('button', { name: '⧗ 排队中 — · 未确认' })).toBeInTheDocument()

    rerender(<QueuePanel entries={[]} open={false} loading={false} hasSnapshot={false} disconnected={false} errorText="" sessionExpired onToggle={vi.fn()} onOpenCard={vi.fn()} compact />)
    expect(screen.getByRole('button', { name: '⧗ 排队中 — · 未确认' })).toBeInTheDocument()

    rerender(<QueuePanel entries={[]} open={false} loading={false} hasSnapshot onToggle={vi.fn()} onOpenCard={vi.fn()} disconnected={false} sessionExpired={false} errorText="" compact />)
    expect(screen.queryByRole('button', { name: /⧗ 排队中/ })).toBeNull()

    rerender(<QueuePanel entries={entries} open={false} loading={false} hasSnapshot disconnected onToggle={vi.fn()} onOpenCard={vi.fn()} sessionExpired={false} errorText="断开" compact />)
    expect(screen.getByRole('button', { name: '⧗ 排队中 2 · 未确认' })).toBeInTheDocument()
  })
})
