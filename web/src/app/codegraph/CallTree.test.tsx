import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CgGraph } from '../../api/types'
import { CallTree } from './CallTree'
import { mergeView } from './graphmath'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-19', generator: 'test' },
  containers: { c_cli: { label: 'CLI', kind: '入口', entry: true }, k_svc: { label: 'svc', kind: '服务端' } },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    e_skip: { kind: 'entry', container: 'c_cli', name: 'demo skip', file: 'cmd/skip.go', line: 1, unscanned: true },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
  },
  edges: [['e_run', 'n_runE']],
}
const noop = () => {}

const gd: CgGraph = {
  ...g,
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '干活' },
  },
  containers: {
    c_cli: { label: 'CLI 命令', kind: '入口', entry: true, domain: 'd_cli' },
    k_svc: { label: 'svc.Server', kind: '服务端', domain: 'd_svc' },
  },
}

describe('CallTree', () => {
  it('只列已扫描入口；点名字触发 onFocus；⌘+点传 additive', () => {
    const onFocus = vi.fn()
    render(<CallTree view={mergeView(g)} foci={['e_run']} open={new Set(['e_run'])}
      scope={null} onCrossJump={noop} onToggle={() => {}} onFocus={onFocus} />)
    expect(screen.queryByText(/demo skip/)).toBeNull()
    fireEvent.click(screen.getByText('runE()'))
    expect(onFocus).toHaveBeenCalledWith('n_runE', false)
    fireEvent.click(screen.getByText('runE()'), { metaKey: true })
    expect(onFocus).toHaveBeenLastCalledWith('n_runE', true)
  })
  it('领域下钻：根是本域入口/接口，域外被调方是可点的横跳行', () => {
    const onCrossJump = vi.fn()
    render(<CallTree view={mergeView(gd)} foci={['e_run']} open={new Set(['e_run'])} scope="d_cli"
      onToggle={() => {}} onFocus={() => {}} onCrossJump={onCrossJump} />)
    fireEvent.click(screen.getByText(/↗ runE · svc 领域/))
    expect(onCrossJump).toHaveBeenCalledWith('n_runE')
  })
})
