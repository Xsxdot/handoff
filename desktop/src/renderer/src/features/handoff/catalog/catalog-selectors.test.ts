/**
 * Handoff catalog 选择器测试：Project → Machine → Workspace → Task 层级。
 *
 * 职责：
 *   - 按稳定 ID 归组
 *   - 计数选择器正确
 *
 * 边界：
 *   - 纯函数，不依赖 window.handoff
 */
import { describe, expect, it } from 'vitest'
import type { BootstrapResponse } from '../../../../../shared/handoff/contracts'
import { selectProjectTree } from './catalog-selectors'

const bootstrap: BootstrapResponse = {
  machines: [
    {
      id: 'm1',
      display_name: '本机',
      kind: 'local',
      endpoint: '',
      protocol_version: 1,
      capabilities: {},
      status: 'connected',
      last_seen_at: null
    }
  ],
  projects: [
    {
      id: 'p1',
      name: 'handoff',
      created_at: '2026-08-09T00:00:00Z',
      updated_at: '2026-08-09T00:00:00Z'
    }
  ],
  locations: [
    {
      id: 'loc1',
      project_id: 'p1',
      machine_id: 'm1',
      role: 'local',
      main_workspace_id: 'ws1',
      source: 'existing_path',
      created_at: '2026-08-09T00:00:00Z',
      updated_at: '2026-08-09T00:00:00Z'
    }
  ],
  workspaces: [
    {
      id: 'ws1',
      machine_id: 'm1',
      location_id: 'loc1',
      kind: 'main',
      path: '/r',
      canonical_path: '/r',
      availability: 'available',
      last_scanned_at: '2026-08-09T00:00:00Z'
    },
    {
      id: 'ws2',
      machine_id: 'm1',
      location_id: 'loc1',
      kind: 'worktree',
      path: '/wt',
      canonical_path: '/wt',
      branch: 'feat/x',
      availability: 'available',
      last_scanned_at: '2026-08-09T00:00:00Z'
    }
  ],
  git_refs: [],
  active_task_summaries: [
    {
      task_id: 't1',
      machine_id: 'm1',
      workspace_id: 'ws1',
      name: '任务',
      executor: 'opencode',
      state: 'running',
      attention: 1,
      updated_at: '2026-08-09T00:00:00Z'
    }
  ],
  operations: [],
  control_revision: 1
}

describe('selectProjectTree', () => {
  it('groups project → machine/location → main/worktree → task', () => {
    const tree = selectProjectTree(bootstrap)
    expect(tree).toHaveLength(1)
    const project = tree[0]!
    expect(project.name).toBe('handoff')
    expect(project.locations).toHaveLength(1)
    const location = project.locations[0]!
    expect(location.workspaces).toHaveLength(2)
    // main workspace 在 worktree 之前
    expect(location.workspaces[0]!.kind).toBe('main')
    expect(location.workspaces[1]!.kind).toBe('worktree')
    expect(location.workspaces[0]!.tasks).toHaveLength(1)
    expect(location.workspaces[0]!.tasks[0]!.taskId).toBe('t1')
  })

  it('shows workspace counts on project row', () => {
    const tree = selectProjectTree(bootstrap)
    expect(tree[0]!.workspaceCount).toBe(2)
    expect(tree[0]!.runningTaskCount).toBe(1)
  })
})
