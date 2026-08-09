// @vitest-environment happy-dom
/**
 * HandoffApp 测试：作为 renderer root 的三栏骨架。
 *
 * 职责：
 *   - 渲染三栏（项目树 / 中栏 / 右栏占位）
 *   - 连接状态展示
 *
 * 边界：
 *   - 使用 react-testing-library；window.handoff 注入 fixture
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { HandoffApp } from './HandoffApp'

const handoffFixture = {
  bootstrap: vi.fn().mockResolvedValue({
    machines: [],
    projects: [],
    locations: [],
    workspaces: [],
    git_refs: [],
    active_task_summaries: [],
    operations: [],
    control_revision: 0
  }),
  createProject: vi.fn(),
  getOperation: vi.fn(),
  pickLocalDirectory: vi.fn(),
  onControlEvent: vi.fn().mockReturnValue(() => undefined),
  onConnectionStatus: vi.fn().mockReturnValue(() => undefined),
  subscribeControl: vi.fn().mockResolvedValue(undefined),
  unsubscribeControl: vi.fn().mockResolvedValue(undefined)
}

;(window as unknown as { handoff: unknown }).handoff = handoffFixture

describe('HandoffApp', () => {
  it('renders the three-pane workbench shell', () => {
    render(<HandoffApp />)
    expect(screen.getByTestId('handoff-project-tree')).toBeTruthy()
    expect(screen.getByTestId('handoff-center-pane')).toBeTruthy()
    expect(screen.getByTestId('handoff-right-pane')).toBeTruthy()
  })
})
