import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CgDiff, CgGraph } from '../../api/types'
import { DomainPanorama } from './DomainPanorama'
import { mergeView } from './graphmath'

const g: CgGraph = {
  meta: { project: 'demo', branch: 'main', commit: 'abc', scannedAt: '2026-08-21', generator: 'test' },
  domains: {
    d_cli: { label: 'cli', kind: '命令层', summary: '命令入口' },
    d_svc: { label: 'svc', kind: '服务端', summary: '服务与实体' },
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
    n_save: { kind: 'func', container: 'k_svc', name: 'Server.Save', file: 'svc/server.go', line: 9 },
    m_task: { kind: 'model', container: 'k_ent', name: 'Task', file: 'svc/task.go', line: 2 },
  },
  edges: [['e_run', 'n_runE'], ['n_runE', 'n_save'], ['n_save', 'm_task']],
}
const noop = () => {}
const base = { selectedDomain: '', selectedEdge: '', onSelectDomain: noop, onSelectEdge: noop, onEnter: noop }
const domainsOf = (c: HTMLElement) =>
  [...c.querySelectorAll('[data-domain]')].map((e) => (e as HTMLElement).dataset.domain).sort()

describe('DomainPanorama', () => {
  it('顶层：领域卡 + 领域间连线，卡上显示职责与统计', () => {
    const { container } = render(<DomainPanorama view={mergeView(g)} scope={null} {...base} />)
    expect(domainsOf(container)).toEqual(['d_cli', 'd_svc'])
    expect(container.querySelectorAll('[data-dedge]')).toHaveLength(1)
    expect(screen.getByText('命令入口')).toBeTruthy()
    expect(screen.getByText(/1 处调用/)).toBeTruthy()
  })
  it('下钻一层：子领域实卡 + 域外虚线占位卡', () => {
    const { container } = render(<DomainPanorama view={mergeView(g)} scope="d_svc" {...base} />)
    expect(domainsOf(container)).toEqual(['d_svc/api', 'd_svc/store', 'ext:d_cli'])
  })
  it('进入按钮下钻；占位卡点击横跳到该领域', () => {
    const onEnter = vi.fn()
    const { container } = render(<DomainPanorama view={mergeView(g)} scope="d_svc" {...base} onEnter={onEnter} />)
    fireEvent.click(screen.getByTitle('下钻到领域内部：api'))
    expect(onEnter).toHaveBeenCalledWith('d_svc/api')
    fireEvent.click(container.querySelector('[data-domain="ext:d_cli"]')!)
    expect(onEnter).toHaveBeenLastCalledWith('d_cli')
  })
  it('叠加 diff 视图时，领域卡显示加/改/删计数徽标', () => {
    const d: CgDiff = {
      view: 'branch:x',
      nodesAdded: { n_new: { kind: 'func', container: 'k_svc', name: 'Server.New', file: 'svc/new.go', line: 2 } },
      nodesDeleted: ['n_save'],
    }
    const { container } = render(<DomainPanorama view={mergeView(g, d)} scope={null} {...base} />)
    const card = container.querySelector('[data-domain="d_svc"]')!
    expect(card.querySelector('[data-badge="add"]')!.textContent).toBe('+1')
    expect(card.querySelector('[data-badge="del"]')!.textContent).toBe('-1')
    expect(card.querySelector('[data-badge="mod"]')).toBeNull()
  })
  it('点卡片选中领域、点连线标签选中连线', () => {
    const onSelectDomain = vi.fn()
    const onSelectEdge = vi.fn()
    const { container } = render(
      <DomainPanorama view={mergeView(g)} scope={null} {...base}
        onSelectDomain={onSelectDomain} onSelectEdge={onSelectEdge} />)
    fireEvent.click(container.querySelector('[data-domain="d_svc"]')!)
    expect(onSelectDomain).toHaveBeenCalledWith('d_svc')
    fireEvent.click(container.querySelector('[data-dedge]')!)
    expect(onSelectEdge).toHaveBeenCalledWith('d_cli|d_svc')
  })
})
