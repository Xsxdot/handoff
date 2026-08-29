// ResizableSidebar.test.tsx —— 左侧导航栏默认宽度、持久化与拖拽调整测试。
//
// 只验证容器的布局交互，不挂载项目树；ProjectTree 的数据与行行为由它自己的
// 测试负责，避免把布局测试绑到任务流 fixture。
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ResizableSidebar } from './ResizableSidebar'
import { DEFAULT_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH, MIN_SIDEBAR_WIDTH, SIDEBAR_WIDTH_KEY } from './sidebarResize'

beforeEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

describe('ResizableSidebar', () => {
  it('默认使用原型基准宽度，并在 localStorage 落盘', () => {
    render(<ResizableSidebar><div>tree</div></ResizableSidebar>)

    const sidebar = screen.getByRole('complementary')
    expect(sidebar).toHaveStyle({ width: `${DEFAULT_SIDEBAR_WIDTH}px` })
    expect(sidebar).toHaveClass('bg-background')
    expect(sidebar).not.toHaveClass('bg-sidebar')
    expect(localStorage.getItem(SIDEBAR_WIDTH_KEY)).toBe(String(DEFAULT_SIDEBAR_WIDTH))
  })

  it('拖拽时按位移调整宽度，并夹在最小最大范围内', () => {
    render(<ResizableSidebar><div>tree</div></ResizableSidebar>)

    const handle = screen.getByRole('separator')
    fireEvent.pointerDown(handle, { pointerId: 1, clientX: 100 })
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 160 })

    expect(screen.getByRole('complementary')).toHaveStyle({ width: `${DEFAULT_SIDEBAR_WIDTH + 60}px` })

    fireEvent.pointerMove(handle, { pointerId: 1, clientX: -1000 })
    expect(screen.getByRole('complementary')).toHaveStyle({ width: `${MIN_SIDEBAR_WIDTH}px` })

    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 3000 })
    expect(screen.getByRole('complementary')).toHaveStyle({ width: `${MAX_SIDEBAR_WIDTH}px` })

    fireEvent.pointerUp(handle, { pointerId: 1 })
    expect(localStorage.getItem(SIDEBAR_WIDTH_KEY)).toBe(String(MAX_SIDEBAR_WIDTH))
  })

  it('支持方向键微调宽度', () => {
    render(<ResizableSidebar><div>tree</div></ResizableSidebar>)

    const handle = screen.getByRole('separator')
    fireEvent.keyDown(handle, { key: 'ArrowLeft' })
    expect(handle).toHaveAttribute('aria-valuenow', String(DEFAULT_SIDEBAR_WIDTH - 16))
    fireEvent.keyDown(handle, { key: 'ArrowRight' })
    expect(handle).toHaveAttribute('aria-valuenow', String(DEFAULT_SIDEBAR_WIDTH))
  })
})
