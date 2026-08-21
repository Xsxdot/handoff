import { describe, expect, it } from 'vitest'
import type { CgGraph } from '../../api/types'
import { mergeView } from './graphmath'
import {
  childDomainsOf, domainAgg, domainAncestors, domainPathOf, hasDomains, inScope, leafRoots,
} from './domains'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体', desc: '干活与落库' },
    'd_svc/api': { label: 'api', kind: '服务端', summary: '对外方法', parent: 'd_svc' },
    'd_svc/store': { label: 'store', kind: '存储', summary: '实体存放', parent: 'd_svc' },
  },
  containers: {
    c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
    k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc/api' },
    k_ent: { label: 'store 实体', kind: '实体', domain: 'd_svc/store' },
  },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4 },
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
    m_task: { kind: 'model', container: 'k_ent', name: 'Task', file: 'svc/task.go', line: 2 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_save'], ['n_save', 'm_task']],
}
const v = mergeView(g)

describe('领域路径与层级', () => {
  it('domainPathOf 返回顶层到叶子', () => {
    expect(domainPathOf(v, 'k_svc')).toEqual(['d_svc', 'd_svc/api'])
    expect(domainPathOf(v, 'c_cli')).toEqual(['d_cli'])
  })
  it('domainAncestors 走 parent 链而不是拆字符串', () => {
    expect(domainAncestors(v, 'd_svc/store')).toEqual(['d_svc', 'd_svc/store'])
  })
  it('childDomainsOf：null 给顶层，领域给直接子领域', () => {
    expect(childDomainsOf(v, null)).toEqual(['d_cli', 'd_svc'])
    expect(childDomainsOf(v, 'd_svc')).toEqual(['d_svc/api', 'd_svc/store'])
    expect(childDomainsOf(v, 'd_svc/api')).toEqual([])
  })
  it('无领域段时 hasDomains 为假', () => {
    const bare = mergeView({ ...g, domains: undefined })
    expect(hasDomains(bare)).toBe(false)
    expect(hasDomains(v)).toBe(true)
  })
})

describe('domainAgg', () => {
  it('顶层：两张卡一条连线，接口带调用方领域', () => {
    const agg = domainAgg(v, null)
    expect(Object.keys(agg.cards).sort()).toEqual(['d_cli', 'd_svc'])
    expect([...agg.edges.keys()]).toEqual(['d_cli|d_svc'])
    expect(agg.edges.get('d_cli|d_svc')!.pairs).toHaveLength(1)
    expect([...agg.ifaces.d_svc.get('n_runE')!]).toEqual(['d_cli'])
  })
  it('下钻一层：子领域实卡 + 域外虚线占位卡，跨界边保留', () => {
    const agg = domainAgg(v, 'd_svc')
    expect(Object.keys(agg.cards).sort()).toEqual(['d_svc/api', 'd_svc/store', 'ext:d_cli'])
    expect(agg.cards['ext:d_cli'].ext).toBe(true)
    expect([...agg.edges.keys()].sort()).toEqual(['d_svc/api|d_svc/store', 'ext:d_cli|d_svc/api'])
  })
})

describe('leafRoots', () => {
  it('叶子领域的树根 = 本域入口 + 被外部调用的接口', () => {
    expect(leafRoots(v, 'd_cli')).toEqual(['e_run'])
    expect(leafRoots(v, 'd_svc/api')).toEqual(['n_runE'])
    expect(leafRoots(v, 'd_svc/store')).toEqual(['m_task'])
  })
})

describe('inScope', () => {
  it('scope=null 全在范围内；领域按路径包含判定', () => {
    expect(inScope(v, 'm_task', null)).toBe(true)
    expect(inScope(v, 'm_task', 'd_svc')).toBe(true)
    expect(inScope(v, 'm_task', 'd_svc/api')).toBe(false)
  })
})
