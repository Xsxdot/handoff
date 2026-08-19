// GeneralPage.test.tsx —— 设置页「常规」分区的浏览器偏好行为测试。

import { describe, expect, it, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderHook } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { GeneralPage } from './GeneralPage'
import { useTreePrefs } from '../tree/useTreePrefs'
import { PREFS_KEY } from '../tree/treePrefs'

const tree = {
  projects: [
    { project_id: 'p1', name: 'handoff' },
    { project_id: 'p2', name: 'nova' },
  ],
  unowned: [],
} as never

beforeEach(() => {
  localStorage.clear()
})

describe('GeneralPage', () => {
  it('点明范围：这些只保存在当前浏览器', () => {
    render(<GeneralPage tree={tree} />)
    expect(screen.getByText(/只保存在当前浏览器/)).toBeInTheDocument()
  })

  it('改排序后落盘，且共享状态里也变了（左栏会跟着变）', async () => {
    const shared = renderHook(() => useTreePrefs())
    render(<GeneralPage tree={tree} />)
    await userEvent.click(screen.getByRole('radio', { name: '名称' }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).projectSort).toBe('name')
    // 这条才是 B160 的重点：不是「设置页自己变了」，而是「同一份状态变了」
    expect(shared.result.current[0].projectSort).toBe('name')
  })

  it('取消勾选一个项目 = 加进隐藏名单（名单存的是不显示谁）', async () => {
    render(<GeneralPage tree={tree} />)
    await userEvent.click(screen.getByRole('checkbox', { name: 'nova' }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).hiddenProjects).toEqual(['p2'])
  })

  it('折叠空闲工作树是个开关', async () => {
    render(<GeneralPage tree={tree} />)
    await userEvent.click(screen.getByRole('checkbox', { name: /隐藏无活跃任务的工作树/ }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).hideIdleWorktrees).toBe(true)
  })

  it('隐藏已结束分组是个开关', async () => {
    render(<GeneralPage tree={tree} />)
    await userEvent.click(screen.getByRole('checkbox', { name: /隐藏已结束分组/ }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).hideArchived).toBe(true)
  })

  it('项目树还没到时不画项目那一组，但另外两项照常可用', () => {
    render(<GeneralPage tree={null} />)
    expect(screen.getByRole('checkbox', { name: /隐藏无活跃任务的工作树/ })).toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: 'nova' })).not.toBeInTheDocument()
  })
})
