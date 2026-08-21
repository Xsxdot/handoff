import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CodegraphResp } from '../../api/types'
import { CodegraphPage } from './CodegraphPage'

// vi.mock 的工厂会被提升到文件顶部执行，直接引用普通的顶层 let 会踩
// 「Cannot access before initialization」。用 vi.hoisted 造一个可变容器，
// 每个用例改它就能换数据，不必 resetModules + 动态 import。
const state = vi.hoisted(() => ({ data: null as unknown as import('../../api/types').CodegraphResp }))

const resp: CodegraphResp = {
  baseline: {
    meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
    domains: {
      d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
      d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体' },
      'd_svc/api': { label: 'api', kind: '服务端', summary: '对外方法', parent: 'd_svc' },
    },
    containers: {
      c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
      k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc/api' },
    },
    nodes: {
      e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
      n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    },
    edges: [['e_run', 'n_runE']],
  },
  views: {},
  stale: [],
}

vi.mock('../data/useProjectTree', () => ({
  useProjectTree: () => ({ data: { projects: [{ name: 'demo' }] } }),
}))
vi.mock('./useCodegraph', () => ({
  useCodegraph: () => ({ data: state.data, error: '', loading: false, reload: () => {} }),
}))

beforeEach(() => { state.data = resp })

describe('CodegraphPage 三态下钻', () => {
  it('默认落在领域全景', async () => {
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelectorAll('[data-domain]').length).toBe(2))
    expect(screen.getByText('领域全景')).toBeTruthy()
  })
  it('进入有子领域的领域 → 再出一层全景；面包屑可逐级返回', async () => {
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelector('[data-domain="d_svc"]')).toBeTruthy())
    fireEvent.click(screen.getByTitle('下钻到领域内部：svc'))
    await waitFor(() => expect(container.querySelector('[data-domain="d_svc/api"]')).toBeTruthy())
    fireEvent.click(screen.getByText('◀ 领域全景'))
    await waitFor(() => expect(container.querySelector('[data-domain="d_cli"]')).toBeTruthy())
  })
  it('进入叶子领域 → 切到树+图视图', async () => {
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelector('[data-domain="d_cli"]')).toBeTruthy())
    fireEvent.click(screen.getByTitle('下钻到领域内部：cli'))
    await waitFor(() => expect(container.querySelectorAll('[data-node]').length).toBeGreaterThan(0))
    expect(container.querySelector('[data-domain]')).toBeNull()
  })
  it('无领域数据：降级为单领域视图并给出提示', async () => {
    state.data = { ...resp, baseline: { ...resp.baseline, domains: undefined } }
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelectorAll('[data-node]').length).toBeGreaterThan(0))
    expect(container.querySelector('[data-domain]')).toBeNull()
    expect(screen.getByText(/未包含领域划分/)).toBeTruthy()
  })
})
