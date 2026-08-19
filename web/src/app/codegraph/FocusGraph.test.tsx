import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CgGraph } from '../../api/types'
import { FocusGraph } from './FocusGraph'
import { mergeView } from './graphmath'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-19', generator: 'test' },
  containers: { c_cli: { label: 'CLI', kind: '入口', entry: true }, k_svc: { label: 'svc', kind: '服务端' } },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4 },
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_save']],
}

const noop = () => {}
const base = {
  depth: 2, staleIds: new Set<string>(), onDepth: noop, onSelect: noop,
  canBack: false, canFwd: false, onBack: noop, onFwd: noop,
}

describe('FocusGraph', () => {
  it('渲染焦点邻域且方向正确（上游卡在焦点上方）', () => {
    const { container } = render(<FocusGraph view={mergeView(g)} foci={['n_do']} onFocus={noop} {...base} />)
    const cards = [...container.querySelectorAll('[data-node]')] as HTMLElement[]
    const top = (id: string) => parseFloat(cards.find((c) => c.dataset.node === id)!.style.top)
    expect(top('n_runE')).toBeLessThan(top('n_do'))
    expect(top('n_do')).toBeLessThan(top('n_save'))
  })
  it('单击换焦点、⌘+单击并集', () => {
    const onFocus = vi.fn()
    const { container } = render(<FocusGraph view={mergeView(g)} foci={['n_do']} onFocus={onFocus} {...base} />)
    const save = container.querySelector('[data-node="n_save"]')!
    fireEvent.click(save)
    expect(onFocus).toHaveBeenCalledWith('n_save', false)
    fireEvent.click(save, { metaKey: true })
    expect(onFocus).toHaveBeenLastCalledWith('n_save', true)
  })
  it('多焦点渲染 chips，层级下拉回调 onDepth', () => {
    const onDepth = vi.fn()
    render(<FocusGraph view={mergeView(g)} foci={['n_runE', 'n_save']} onFocus={noop}
      {...base} onDepth={onDepth} />)
    expect(screen.getByText('runE')).toBeTruthy()
    fireEvent.change(screen.getByTitle('上下游各展开几级'), { target: { value: '0' } })
    expect(onDepth).toHaveBeenCalledWith(0)
  })
})
