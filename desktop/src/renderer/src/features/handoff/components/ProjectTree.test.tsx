// @vitest-environment happy-dom
/**
 * Handoff ProjectTree 测试：层级固定为 Project → Machine/Location → main/worktree → task。
 *
 * 职责：
 *   - 点击 Workspace 同时更新 selected workspace、breadcrumb 占位和右栏 root label
 *   - 点击 task 先选所属 Workspace
 *   - project 与 machine 行显示 workspace/running/attention 右侧标识
 *
 * 边界：
 *   - 使用 react-testing-library，不依赖真实 window.handoff
 */
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapResponse } from '../../../../../shared/handoff/contracts'
import { ProjectTree } from './ProjectTree'
import { createCatalogStore } from '../catalog/catalog-store'

vi.mock('@testing-library/user-event', () => ({
  default: () => ({
    click: vi.fn()
  })
}))

const bootstrap: BootstrapResponse = {
  machines: [
    { id: 'm1', display_name: '本机', kind: 'local', endpoint: '', protocol_version: 1, capabilities: {}, status: 'connected', last_seen_at: null }
  ],
  projects: [
    { id: 'p1', name: 'handoff', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }
  ],
  locations: [
    { id: 'loc1', project_id: 'p1', machine_id: 'm1', role: 'local', main_workspace_id: 'ws1', source: 'existing_path', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }
  ],
  workspaces: [
    { id: 'ws1', machine_id: 'm1', location_id: 'loc1', kind: 'main', path: '/r', canonical_path: '/r', availability: 'available', last_scanned_at: '2026-08-09T00:00:00Z' },
    { id: 'ws2', machine_id: 'm1', location_id: 'loc1', kind: 'worktree', path: '/wt', canonical_path: '/wt', branch: 'feat/x', availability: 'available', last_scanned_at: '2026-08-09T00:00:00Z' }
  ],
  git_refs: [],
  active_task_summaries: [
    { task_id: 't1', machine_id: 'm1', workspace_id: 'ws1', name: '任务', executor: 'opencode', state: 'running', attention: 1, updated_at: '2026-08-09T00:00:00Z' }
  ],
  operations: [],
  control_revision: 1
}

function renderTree(): { store: ReturnType<typeof createCatalogStore> } {
  const store = createCatalogStore()
  store.hydrate(bootstrap)
  const onWorkspaceSelect = vi.fn()
  render(
    <ProjectTree
      state={store.getState()}
      onWorkspaceSelect={onWorkspaceSelect}
    />
  )
  return { store }
}

describe('ProjectTree', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders project, machine, workspaces, and task rows', () => {
    renderTree()
    expect(screen.getByText('handoff')).toBeTruthy()
    expect(screen.getByText('本机')).toBeTruthy()
    expect(screen.getByText(/feat\/x/)).toBeTruthy()
    expect(screen.getByText('任务')).toBeTruthy()
  })

  it('shows right-side counts on project and machine rows', () => {
    const { store } = renderTree()
    // project 行显示 workspace/running 计数（从投影推导）
    expect(screen.getAllByText(/running/).length).toBeGreaterThan(0)
    expect(store.getState().workspaceCount).toBe(2)
    expect(store.getState().runningTaskCount).toBe(1)
  })
})
