import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CodegraphResp } from '../../api/types'
import { CodegraphPage } from './CodegraphPage'

// vi.mock 的工厂会被提升到文件顶部执行，直接引用普通的顶层 let 会踩
// 「Cannot access before initialization」。用 vi.hoisted 造一个可变容器，
// 每个用例改它就能换数据，不必 resetModules + 动态 import。
const state = vi.hoisted(() => ({
  data: null as unknown as import('../../api/types').CodegraphResp,
  error: '',
  loading: false,
  reloads: 0,
}))

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
  useCodegraph: () => ({
    data: state.data,
    error: state.error,
    loading: state.loading,
    reload: () => { state.reloads += 1 },
  }),
}))

// 只在 data 上做用例区分不够：空态/错误态是 error+data=null 的组合，
// 每个用例前必须把这三样一起复位，否则前一个用例的 error 会漏到下一个
beforeEach(() => {
  state.data = resp
  state.error = ''
  state.loading = false
  state.reloads = 0
})

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
  it('进入叶子领域：默认焦点取本域的根，不是全图第一个入口', async () => {
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelector('[data-domain="d_svc"]')).toBeTruthy())
    fireEvent.click(screen.getByTitle('下钻到领域内部：svc'))
    await waitFor(() => expect(container.querySelector('[data-domain="d_svc/api"]')).toBeTruthy())
    fireEvent.click(screen.getByTitle('下钻到领域内部：api'))
    await waitFor(() => expect(container.querySelectorAll('[data-node]').length).toBeGreaterThan(0))
    // 焦点越界的症状：左树列的是本域的根，焦点图与右详情却停在域外节点上，
    // 两栏在讲两个不同领域的事。demo run 属于 d_cli，不能成为 d_svc/api 的默认焦点。
    expect(container.querySelector('h3')?.textContent).toBe('runE')
  })
  it('无领域数据：降级为单领域视图并给出提示', async () => {
    state.data = { ...resp, baseline: { ...resp.baseline, domains: undefined } }
    const { container } = render(<CodegraphPage />)
    await waitFor(() => expect(container.querySelectorAll('[data-node]').length).toBeGreaterThan(0))
    expect(container.querySelector('[data-domain]')).toBeNull()
    expect(screen.getByText(/未包含领域划分/)).toBeTruthy()
  })
})

// 空态/错误态：内容区换掉，工具条必须留着。
// why 单独一组：这里验的不是「显示了什么文案」，而是「出错后还能不能换项目」
// ——原实现在 error 时整页 return，选中一个没扫过图的项目就再也换不回去了。
describe('CodegraphPage 非图状态', () => {
  it('项目没扫过图：给空态而不是红字，且项目下拉仍在（能换回去）', async () => {
    state.data = null as unknown as CodegraphResp
    state.error = '项目 aio 未生成代码图（无 codegraph/baseline.json）'
    render(<CodegraphPage />)
    await waitFor(() => expect(screen.getByText(/还没有代码图/)).toBeTruthy())
    // 工具条恒在：没有它，这一页就是死胡同（项目下拉 + 视图下拉共两个）
    expect(screen.getAllByRole('combobox')).toHaveLength(2)
    expect(screen.getByRole('button', { name: '刷新' })).toBeTruthy()
    // 「没扫过」不是故障，不该出现报错原文
    expect(screen.queryByText(/取代码图失败/)).toBeNull()
  })

  it('真出错：照抄报错原文并可重试，工具条同样留着', async () => {
    state.data = null as unknown as CodegraphResp
    state.error = 'agentd 不可达: connection refused'
    render(<CodegraphPage />)
    await waitFor(() => expect(screen.getByText('取代码图失败')).toBeTruthy())
    expect(screen.getByText(/connection refused/)).toBeTruthy()
    expect(screen.getAllByRole('combobox').length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(state.reloads).toBe(1)
  })

  it('加载中只说加载中，不提前判空', async () => {
    state.data = null as unknown as CodegraphResp
    state.loading = true
    render(<CodegraphPage />)
    await waitFor(() => expect(screen.getByText('加载中…')).toBeTruthy())
    expect(screen.queryByText(/还没有代码图/)).toBeNull()
  })
})
