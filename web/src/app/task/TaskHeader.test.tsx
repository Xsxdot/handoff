import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { TaskHeader } from './TaskHeader'
import type { Task } from '../../api/types'

const task = {
  id: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad',
  name: '重构工单通道',
  state: 'running',
  branch: 'feat/x',
  executor: 'opencode',
  repo_dirty_count: 0,
  base_ahead: 0,
} as unknown as Task

const taskWithCumulative = {
  ...task,
  actual_model: 'gpt-5.6-sol',
  usage: { context_tokens: 24673, context_window: 258400 },
  cumulative: {
    input_tokens: 340_200,
    cached_tokens: 820_500,
    output_tokens: 39_300,
    total_tokens: 1_200_000,
    cost: { ticks: 42_000_000_000, state: 'estimated' as const },
  },
} as unknown as Task

const taskWithoutCumulative = {
  ...task,
  actual_model: 'gpt-5.6-sol',
  usage: { context_tokens: 24673, context_window: 258400 },
} as unknown as Task

describe('TaskHeader', () => {
  it('缺省仍是卡片形态，含完整定义列表', () => {
    const { container } = render(<TaskHeader task={task} />)
    expect(container.firstElementChild?.className).toContain('rounded-lg')
    expect(screen.getByText('工作目录')).toBeInTheDocument()
  })

  it('compact 去掉卡片外框，只留任务名 / 短号 / 状态', () => {
    const { container } = render(<TaskHeader task={task} compact />)
    expect(container.firstElementChild?.className).not.toContain('rounded-lg')
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.getByText('handoff-7ec762e7')).toBeInTheDocument()
    expect(screen.queryByText('工作目录')).not.toBeInTheDocument()
  })

  it('执行器行显示实际模型名与 context 占用', () => {
    const withUsage = {
      ...task,
      actual_model: 'gpt-5.6-sol',
      usage: { context_tokens: 24673, context_window: 258400 },
    } as unknown as Task
    render(<TaskHeader task={withUsage} />)
    expect(screen.getByText('opencode · gpt-5.6-sol · 24.7k / 258.4k (10%)')).toBeInTheDocument()
  })

  it('旧任务没有用量字段时不报错，只显执行器', () => {
    render(<TaskHeader task={task} />)
    expect(screen.getByText('opencode')).toBeInTheDocument()
  })

  it('默认显当前占用，按钮文案是要切去的视图', () => {
    render(<TaskHeader task={taskWithCumulative} />)
    expect(screen.getByText('opencode · gpt-5.6-sol · 24.7k / 258.4k (10%)')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '累计用量' })).toBeInTheDocument()
  })

  it('点按钮切到累计视图，按钮文案反转', () => {
    render(<TaskHeader task={taskWithCumulative} />)
    fireEvent.click(screen.getByRole('button', { name: '累计用量' }))
    expect(screen.getByText(/输入 340\.2k/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '当前占用' })).toBeInTheDocument()
  })

  it('没有累计数据时不显示切换按钮', () => {
    render(<TaskHeader task={taskWithoutCumulative} />)
    expect(screen.queryByRole('button', { name: '累计用量' })).not.toBeInTheDocument()
  })

  it('估算花费带「估算」小标，自报花费不带', () => {
    render(<TaskHeader task={taskWithCumulative} />)   // cost.state = 'estimated'
    fireEvent.click(screen.getByRole('button', { name: '累计用量' }))
    expect(screen.getByText('估算')).toBeInTheDocument()
  })
})
