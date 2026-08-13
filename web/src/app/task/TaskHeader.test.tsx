import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
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
})
