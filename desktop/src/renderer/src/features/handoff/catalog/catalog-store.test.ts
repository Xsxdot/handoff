/**
 * Handoff CatalogStore 测试：bootstrap 原子替换与 control event 增量。
 *
 * 职责：
 *   - hydrate 原子替换
 *   - apply 按 revision 严格递增
 *   - 重复 event 幂等
 *   - gap 触发重新 bootstrap 而非猜补
 *   - project/machine trailing counts 直接读 TaskSummary/Workspace 投影
 *   - Machine unavailable 不删除最后已知子树
 *
 * 边界：
 *   - 纯 store 测试，不依赖 window.handoff
 */
import { describe, expect, it } from 'vitest'
import type { BootstrapResponse, ControlEventEnvelope } from '../../../../../shared/handoff/contracts'
import {
  createCatalogStore,
  type CatalogState
} from './catalog-store'

const emptyBootstrap: BootstrapResponse = {
  machines: [],
  projects: [],
  locations: [],
  workspaces: [],
  git_refs: [],
  active_task_summaries: [],
  operations: [],
  control_revision: 0
}

function makeBootstrap(overrides: Partial<BootstrapResponse>): BootstrapResponse {
  return { ...emptyBootstrap, ...overrides }
}

describe('CatalogStore', () => {
  it('hydrates atomically replacing previous state', () => {
    const store = createCatalogStore()
    store.hydrate(makeBootstrap({ control_revision: 1 }))
    expect(store.getState().controlRevision).toBe(1)

    // 第二次 hydrate 原子替换（含 revision 回落）
    store.hydrate(makeBootstrap({ control_revision: 5 }))
    expect(store.getState().controlRevision).toBe(5)
  })

  it('applies events in strictly increasing revision order', () => {
    const store = createCatalogStore()
    store.hydrate(makeBootstrap({ control_revision: 0 }))
    store.apply({
      revision: 1,
      kind: 'workspace.upsert',
      resource_id: 'ws1',
      payload: { id: 'ws1' },
      created_at: '2026-08-09T00:00:00Z'
    })
    store.apply({
      revision: 2,
      kind: 'workspace.upsert',
      resource_id: 'ws2',
      payload: { id: 'ws2' },
      created_at: '2026-08-09T00:00:01Z'
    })
    expect(store.getState().controlRevision).toBe(2)
  })

  it('ignores duplicate events idempotently', () => {
    const store = createCatalogStore()
    store.hydrate(makeBootstrap({ control_revision: 0 }))
    const ev: ControlEventEnvelope = {
      revision: 1,
      kind: 'workspace.upsert',
      resource_id: 'ws1',
      payload: { id: 'ws1' },
      created_at: '2026-08-09T00:00:00Z'
    }
    store.apply(ev)
    store.apply(ev) // 重复
    expect(store.getState().controlRevision).toBe(1)
  })

  it('requests rebootstrap on gap instead of guessing', () => {
    const store = createCatalogStore()
    let rebootstrapCalled = 0
    store.getState().resetFromGap = () => {
      rebootstrapCalled++
    }
    store.hydrate(makeBootstrap({ control_revision: 1 }))
    // 跳到 5：中间 2-4 缺失 → gap
    store.apply({
      revision: 5,
      kind: 'workspace.upsert',
      resource_id: 'ws5',
      payload: {},
      created_at: '2026-08-09T00:00:00Z'
    })
    expect(rebootstrapCalled).toBe(1)
  })

  it('derives trailing counts from projections', () => {
    const store = createCatalogStore()
    store.hydrate(
      makeBootstrap({
        control_revision: 3,
        machines: [
          { id: 'm1', display_name: '本机', kind: 'local', endpoint: '', protocol_version: 1, capabilities: {}, status: 'connected', last_seen_at: null }
        ],
        projects: [{ id: 'p1', name: 'handoff', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }],
        locations: [
          { id: 'loc1', project_id: 'p1', machine_id: 'm1', role: 'local', main_workspace_id: 'ws1', source: 'existing_path', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }
        ],
        workspaces: [
          { id: 'ws1', machine_id: 'm1', location_id: 'loc1', kind: 'main', path: '/r', canonical_path: '/r', availability: 'available', last_scanned_at: '2026-08-09T00:00:00Z' }
        ],
        active_task_summaries: [
          { task_id: 't1', machine_id: 'm1', workspace_id: 'ws1', name: '任务', executor: 'opencode', state: 'running', attention: 1, updated_at: '2026-08-09T00:00:00Z' }
        ]
      })
    )
    const state: CatalogState = store.getState()
    expect(state.workspaceCount).toBe(1)
    expect(state.runningTaskCount).toBe(1)
    expect(state.attentionCount).toBe(1)
    expect(state.projectCount).toBe(1)
    expect(state.machineCount).toBe(1)
  })

  it('keeps last-known subtree when machine unavailable', () => {
    const store = createCatalogStore()
    store.hydrate(
      makeBootstrap({
        control_revision: 1,
        machines: [
          { id: 'm1', display_name: '远端', kind: 'remote', endpoint: 'http://x', protocol_version: 1, capabilities: {}, status: 'connected', last_seen_at: null }
        ],
        projects: [{ id: 'p1', name: 'p', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }],
        locations: [
          { id: 'loc1', project_id: 'p1', machine_id: 'm1', role: 'remote', main_workspace_id: 'ws1', source: 'existing_path', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }
        ],
        workspaces: [
          { id: 'ws1', machine_id: 'm1', location_id: 'loc1', kind: 'main', path: '/r', canonical_path: '/r', availability: 'available', last_scanned_at: '2026-08-09T00:00:00Z' }
        ]
      })
    )
    // 机器变 unavailable（如断线）：不删除 last-known 子树
    store.hydrate(
      makeBootstrap({
        control_revision: 2,
        machines: [
          { id: 'm1', display_name: '远端', kind: 'remote', endpoint: 'http://x', protocol_version: 1, capabilities: {}, status: 'unavailable', last_seen_at: null }
        ],
        projects: [{ id: 'p1', name: 'p', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }],
        locations: [
          { id: 'loc1', project_id: 'p1', machine_id: 'm1', role: 'remote', main_workspace_id: 'ws1', source: 'existing_path', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }
        ],
        workspaces: [
          { id: 'ws1', machine_id: 'm1', location_id: 'loc1', kind: 'main', path: '/r', canonical_path: '/r', availability: 'available', last_scanned_at: '2026-08-09T00:00:00Z' }
        ]
      })
    )
    const state = store.getState()
    expect(state.machines[0]?.status).toBe('unavailable')
    expect(state.workspaces).toHaveLength(1)
    expect(state.projects).toHaveLength(1)
  })

  it('selects a workspace', () => {
    const store = createCatalogStore()
    store.hydrate(makeBootstrap({ control_revision: 1 }))
    store.selectWorkspace('ws-42')
    expect(store.getState().selectedWorkspaceId).toBe('ws-42')
  })
})
