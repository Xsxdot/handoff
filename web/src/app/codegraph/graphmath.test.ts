import { describe, expect, it } from 'vitest'
import { chainTree, layoutBands, mergeView, neighborhood, scannedEntries } from './graphmath'
import type { CgDiff, CgGraph } from '../../api/types'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-19', generator: 'test' },
  containers: { c_cli: { label: 'CLI', kind: '入口', entry: true }, k_svc: { label: 'svc.Server', kind: '服务端' } },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    e_skip: { kind: 'entry', container: 'c_cli', name: 'demo skip', file: 'cmd/skip.go', line: 1, unscanned: true },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4 },
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_save']],
}
const d: CgDiff = {
  view: 'branch:x',
  nodesAdded: { n_audit: { kind: 'func', container: 'k_svc', name: 'Server.Audit', file: 'svc/audit.go', line: 3 } },
  nodesModified: { n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4, signature: 'new', signatureOld: 'old' } },
  nodesDeleted: ['n_save'],
  edgesAdded: [['n_do', 'n_audit']],
  edgesDeleted: [['n_do', 'n_save']],
}

describe('mergeView', () => {
  it('基准视图无 status，diff 视图状态齐全', () => {
    const base = mergeView(g)
    expect(Object.keys(base.nodes)).toHaveLength(5)
    expect(base.nodes.n_do.status).toBe('')
    const v = mergeView(g, d)
    expect(v.nodes.n_audit.status).toBe('added')
    expect(v.nodes.n_do.status).toBe('modified')
    expect(v.nodes.n_do.signatureOld).toBe('old')
    expect(v.nodes.n_save.status).toBe('deleted')
    expect(v.edges.find((e) => e.to === 'n_audit')?.status).toBe('added')
    expect(v.edges.find((e) => e.to === 'n_save')?.status).toBe('deleted')
  })
})

describe('scannedEntries', () => {
  it('unscanned 入口不进树', () => {
    expect(scannedEntries(mergeView(g))).toEqual(['e_run'])
  })
})

describe('neighborhood', () => {
  it('深度截断：depth=1 只有焦点±1', () => {
    const dist = neighborhood(mergeView(g), ['n_do'], 1)
    expect(Object.keys(dist).sort()).toEqual(['n_do', 'n_runE', 'n_save'])
    expect(dist.n_runE).toBe(-1)
    expect(dist.n_save).toBe(1)
  })
  it('并集：两焦点都在 0 层', () => {
    const dist = neighborhood(mergeView(g), ['n_runE', 'n_save'], 0)
    expect(dist.n_runE).toBe(0)
    expect(dist.n_save).toBe(0)
    expect(dist.e_run).toBe(-1)
  })
  it('deleted 不参与遍历', () => {
    const dist = neighborhood(mergeView(g, d), ['e_run'], 0)
    expect(dist.n_save).toBeUndefined()
    expect(dist.n_audit).toBe(3)
  })
})

describe('layoutBands', () => {
  it('竖向：dist 越大 y 越大，同层 y 相等', () => {
    const v = mergeView(g)
    const dist = neighborhood(v, ['n_do'], 0)
    const { py } = layoutBands(v, dist)
    expect(py.e_run).toBeLessThan(py.n_runE)
    expect(py.n_runE).toBeLessThan(py.n_do)
    expect(py.n_do).toBeLessThan(py.n_save)
  })
})

describe('chainTree', () => {
  it('循环截断且标记 cycle', () => {
    const cyc: CgGraph = { ...g, edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_runE']] }
    const tree = chainTree(mergeView(cyc), 'e_run')
    // e_run → runE → do → (cycle runE)
    expect(tree.children[0].children[0].children[0].cycle).toBe(true)
  })
})
