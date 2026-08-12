import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { ProjectNode } from '../../api/types'
import { EMPTY_FILTER } from './filter'
import { FilterBar, type FilterBarProps } from './FilterBar'

const projects: ProjectNode[] = [
  { project_id: 'p1', origin_url: 'git@x:/a.git', name: 'alpha', locations: [] },
  { project_id: 'p2', origin_url: 'git@x:/b.git', name: 'beta', locations: [] },
]

function base(over: Partial<FilterBarProps> = {}): FilterBarProps {
  return {
    filter: EMPTY_FILTER,
    onChange: vi.fn(),
    projects,
    machines: ['', 'devbox'],
    taskCounts: { p1: 3, p2: 5 },
    taskCount: 8,
    ...over,
  }
}

function renderBar(over: Partial<FilterBarProps> = {}) {
  const props = base(over)
  render(<FilterBar {...props} />)
  return props
}

describe('FilterBar', () => {
  it('项目多选下拉每项带任务数', () => {
    renderBar()
    fireEvent.click(screen.getByRole('button', { name: /项目/ }))
    expect(screen.getByRole('option', { name: /alpha/ })).toHaveTextContent('3')
    expect(screen.getByRole('option', { name: /beta/ })).toHaveTextContent('5')
  })

  it('勾选两个项目后 filter.projects 有两个 id', () => {
    const { onChange } = renderBar()
    fireEvent.click(screen.getByRole('button', { name: /项目/ }))
    fireEvent.click(screen.getByRole('option', { name: /alpha/ }))
    fireEvent.click(screen.getByRole('option', { name: /beta/ }))
    expect(onChange.mock.calls.at(-1)![0].projects).toEqual(new Set(['p1', 'p2']))
  })

  it('开发机下拉写 machine；「本机」写的是空串不是 null', () => {
    const { onChange } = renderBar()
    fireEvent.click(screen.getByRole('button', { name: /开发机/ }))
    fireEvent.click(screen.getByRole('option', { name: '本机' }))
    expect(onChange.mock.calls.at(-1)![0].machine).toBe('')
  })

  it('「只看待处理」toggle 写 pendingOnly', () => {
    const { onChange } = renderBar()
    fireEvent.click(screen.getByRole('switch', { name: /只看待处理/ }))
    expect(onChange.mock.calls.at(-1)![0].pendingOnly).toBe(true)
  })

  it('右侧显示筛选后的任务总数，不是筛选前的', () => {
    renderBar({ taskCount: 2 })
    expect(screen.getByText(/共\s*2\s*个任务/)).toBeInTheDocument()
  })

  it('左栏点击与顶部下拉互推，不产生第三种状态', () => {
    const { onChange } = renderBar({ filter: { ...EMPTY_FILTER, projects: new Set(['p1']), machine: 'devbox' } })
    fireEvent.click(screen.getByRole('button', { name: /项目/ }))
    fireEvent.click(screen.getByRole('option', { name: /alpha/ })) // 取消 p1
    fireEvent.click(screen.getByRole('option', { name: /beta/ }))  // 选上 p2
    const next = onChange.mock.calls.at(-1)![0]
    expect(next.projects).toEqual(new Set(['p2']))
    expect(next.machine).toBeNull()
  })
})
