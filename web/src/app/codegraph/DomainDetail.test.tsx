import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CgGraph } from '../../api/types'
import { DomainDetail } from './DomainDetail'
import { mergeView } from './graphmath'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体', desc: '干活与落库' },
  },
  containers: {
    c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
    k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc' },
  },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
  },
  edges: [['e_run', 'n_runE']],
}
const v = mergeView(g)
const noop = () => {}

describe('DomainDetail', () => {
  it('领域详情：职责 + 内部逻辑 + 对外接口（带调用方领域）', () => {
    render(<DomainDetail view={v} scope={null} domainId="d_svc" edgeKey=""
      onEnterNode={noop} onEnterDomain={noop} />)
    expect(screen.getByText('服务与实体')).toBeTruthy()
    expect(screen.getByText('干活与落库')).toBeTruthy()
    expect(screen.getByText(/runE/)).toBeTruthy()
    expect(screen.getByText(/← cli/)).toBeTruthy()
  })
  it('领域详情：点接口下钻到该节点', () => {
    const onEnterNode = vi.fn()
    render(<DomainDetail view={v} scope={null} domainId="d_svc" edgeKey=""
      onEnterNode={onEnterNode} onEnterDomain={noop} />)
    fireEvent.click(screen.getByText(/runE/))
    expect(onEnterNode).toHaveBeenCalledWith('n_runE')
  })
  it('连线详情：逐对列出谁调用了谁的接口', () => {
    render(<DomainDetail view={v} scope={null} domainId="" edgeKey="d_cli|d_svc"
      onEnterNode={noop} onEnterDomain={noop} />)
    expect(screen.getByText('cli → svc')).toBeTruthy()
    expect(screen.getByText(/1 处跨领域调用/)).toBeTruthy()
    expect(screen.getByText(/demo run/)).toBeTruthy()
    expect(screen.getByText(/runE/)).toBeTruthy()
  })
  it('都为空时渲染空壳，不崩', () => {
    const { container } = render(<DomainDetail view={v} scope={null} domainId="" edgeKey=""
      onEnterNode={noop} onEnterDomain={noop} />)
    expect(container.querySelector('aside')).toBeTruthy()
  })
})
