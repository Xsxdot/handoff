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
})
