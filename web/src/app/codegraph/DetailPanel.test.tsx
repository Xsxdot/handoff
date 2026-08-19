import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CgDiff, CgGraph } from '../../api/types'
import { fetchCodegraphSource } from '../../api/client'
import { DetailPanel } from './DetailPanel'
import { mergeView } from './graphmath'

vi.mock('../../api/client', () => ({
  fetchCodegraphSource: vi.fn().mockResolvedValue({
    file: 'svc/server.go', from: 1, lines: ['package svc', '', 'func Do() {}'],
  }),
}))

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-19', generator: 'test' },
  containers: { c_cli: { label: 'CLI', kind: '入口', entry: true }, k_svc: { label: 'svc.Server', kind: '服务端' } },
  nodes: {
    e_run: { kind: 'entry', container: 'c_cli', name: 'demo run', file: 'cmd/run.go', line: 3 },
    n_runE: { kind: 'func', container: 'k_svc', name: 'runE', file: 'cmd/run.go', line: 5 },
    n_do: { kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4 },
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_do'], ['n_do', 'n_save']],
}

const d: CgDiff = {
  view: 'branch:x',
  nodesModified: {
    n_do: {
      kind: 'func', container: 'k_svc', name: 'Server.Do', file: 'svc/server.go', line: 4,
      signature: 'func Do(ctx context.Context) error', signatureOld: 'func Do() error',
      params: [['ctx', 'context.Context', '请求上下文']], returns: 'error',
      summary: '干活', tests: [{ name: 'TestDo', file: 'svc/server_test.go:10', snippet: 'assert' }],
    },
  },
}

describe('DetailPanel', () => {
  it('展示新旧签名/参数/返回/测试/调用关系/失鲜，并实时读取源码', async () => {
    const onJump = vi.fn()
    render(<DetailPanel project="demo" view={mergeView(g, d)} nodeId="n_do"
      stale={new Set(['n_do'])} onJump={onJump} />)

    expect(screen.getByText('func Do(ctx context.Context) error')).toBeTruthy()
    expect(screen.getByText('func Do() error')).toHaveClass('line-through')
    expect(screen.getByText('ctx')).toBeTruthy()
    expect(screen.getByText('TestDo')).toBeTruthy()
    expect(screen.getByText(/疑似失鲜/)).toBeTruthy()

    fireEvent.click(screen.getByText(/← runE/))
    expect(onJump).toHaveBeenCalledWith('n_runE')
    fireEvent.click(screen.getByText(/→ Server.Save/))
    expect(onJump).toHaveBeenCalledWith('n_save')

    fireEvent.click(screen.getByText(/源码（实时读自/))
    await waitFor(() => expect(fetchCodegraphSource).toHaveBeenCalledWith('demo', 'svc/server.go', 4))
    expect(screen.getByText(/package svc/)).toBeTruthy()
    expect(screen.getByText(/1 package svc/)).toBeTruthy()
  })
})
