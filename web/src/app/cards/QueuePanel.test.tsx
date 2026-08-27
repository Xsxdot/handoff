// QueuePanel.test.tsx —— 后端队列快照的呈现与卡号回接测试。
//
// 边界：position、ready 和输入顺序来自服务端；本组件不重排、不推导缺席字段，
// 轮询及页面组装由 CardsPage 测试覆盖。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { QueueEntry } from '../../api/scheduling'
import { QueuePanel, queuePositionByCard } from './QueuePanel'

const entry = (over: Partial<QueueEntry> = {}): QueueEntry => ({
  kind: 'ignition_queue', id: 'q1', card: 'B1', node: 'implement', squad: 'exec',
  target: 'linux', executor: 'opencode', model: 'gpt-5', priority: '高',
  ready: false, actor: 'cli:user', seq: 1, position: 1, ...over,
})

const props = (over: Partial<React.ComponentProps<typeof QueuePanel>> = {}) => ({
  entries: [], loading: false, disconnected: false, sessionExpired: false,
  errorText: '', onOpenCard: vi.fn(), ...over,
})

describe('QueuePanel', () => {
  it('按服务端顺序显示 position 与队列快照字段，不自行排序', () => {
    render(<QueuePanel {...props({
      entries: [
        entry({ id: 'q2', card: 'B2', node: 'review', ready: true, position: 2 }),
        entry({ id: 'q1', card: 'B1', node: undefined, squad: 'coord', priority: '', position: 1 }),
        entry({ id: 'q3', card: 'B3', node: 'implement', position: 3 }),
      ],
    })} />)
    const rows = [...screen.getByRole('list').querySelectorAll('li')]
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining('#2'), expect.stringContaining('#1'), expect.stringContaining('#3'),
    ])
    expect(rows[0]).toHaveTextContent('B2')
    expect(rows[0]).toHaveTextContent('review')
    expect(rows[0]).toHaveTextContent('可运行')
    expect(rows[1]).toHaveTextContent('coord')
    expect(rows[1]).toHaveTextContent('等待条件')
    expect(rows[1]).not.toHaveTextContent('优先级')
  })

  it('同卡取最早服务端位次，空 card 不进入 map，0 保留为真实值', () => {
    const positions = queuePositionByCard([
      entry({ card: 'B1', position: 4 }), entry({ card: 'B1', position: 2 }), entry({ card: '', position: 0 }),
    ])
    expect(positions.get('B1')).toBe(2)
    expect(positions.has('')).toBe(false)
    render(<QueuePanel {...props({ entries: [entry({ card: 'B0', position: 0, node: undefined, squad: '' })] })} />)
    expect(screen.getByText('#0')).toBeInTheDocument()
  })

  it('点击卡号回调完整 cardId', () => {
    const onOpenCard = vi.fn()
    render(<QueuePanel {...props({ entries: [entry({ card: 'B1.2' }), entry({ id: 'q2', card: 'B2' })], onOpenCard })} />)
    fireEvent.click(screen.getByRole('button', { name: 'B1.2' }))
    expect(onOpenCard).toHaveBeenCalledWith('B1.2')
  })

  it('断线保留旧 entries 且显示原因；首拉失败与会话失效不伪造空队列', () => {
    const { rerender } = render(<QueuePanel {...props({ entries: [entry()], disconnected: true, errorText: '连接被拒绝' })} />)
    expect(screen.getByText('B1')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('已断开')
    expect(screen.getByRole('alert')).toHaveTextContent('连接被拒绝')
    expect(screen.queryByText('当前没有排队项。')).not.toBeInTheDocument()

    rerender(<QueuePanel {...props({ loading: true, errorText: '首拉超时' })} />)
    expect(screen.getByText('正在读取队列…')).toBeInTheDocument()
    rerender(<QueuePanel {...props({ sessionExpired: true, errorText: '401' })} />)
    expect(screen.getByText(/会话已失效/)).toBeInTheDocument()
    expect(screen.queryByText('当前没有排队项。')).not.toBeInTheDocument()
  })
})
