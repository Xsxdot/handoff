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
  it('不出滚动条：靠平移而不是滚动来看画布外的领域', () => {
    const { container } = render(<DomainPanorama view={mergeView(g)} scope={null} {...base} />)
    const wrap = container.firstElementChild as HTMLElement
    expect(wrap.className).toContain('overflow-hidden')
    expect(wrap.className).not.toContain('overflow-auto')
  })
  it('容器还没量到尺寸时不适配：缩放不能算成 0 或负数', () => {
    // jsdom 的 clientWidth/clientHeight 恒为 0，正好是「还没测量」这个真实场景。
    // 没有守卫时 vw = 0 - 48 = -48，算出 scale(-0.21)：画布翻转塌陷。
    const { container } = render(<DomainPanorama view={mergeView(g)} scope={null} {...base} />)
    // 注意外层 wrap 自己也带 relative 类，要从 wrap 里面找画布
    const wrap = container.firstElementChild as HTMLElement
    const canvas = wrap.querySelector('div.relative') as HTMLElement
    const z = Number(/scale\(([-\d.]+)\)/.exec(canvas.style.transform)![1])
    expect(z).toBe(1)
  })
  it('空白拖动平移画布；在卡片上按下不平移（那是拖卡片）', () => {
    const { container } = render(<DomainPanorama view={mergeView(g)} scope={null} {...base} />)
    const wrap = container.firstElementChild as HTMLElement
    const canvas = wrap.querySelector('div.relative') as HTMLElement
    const before = canvas.style.transform
    // 空白处按下并拖动 → 画布位移
    fireEvent.mouseDown(wrap, { clientX: 10, clientY: 10 })
    fireEvent.mouseMove(window, { clientX: 90, clientY: 50 })
    fireEvent.mouseUp(window)
    expect(canvas.style.transform).not.toBe(before)
    const d = (t: string) => (/translate\(([-\d.]+)px, ([-\d.]+)px\)/.exec(t) as RegExpExecArray).slice(1).map(Number)
    expect(d(canvas.style.transform)[0] - d(before)[0]).toBe(80)
    expect(d(canvas.style.transform)[1] - d(before)[1]).toBe(40)
    // 在卡片上按下 → 不平移，交给卡片自己的拖拽
    const held = canvas.style.transform
    const card = container.querySelector('[data-domain]') as HTMLElement
    fireEvent.mouseDown(card, { clientX: 200, clientY: 200 })
    fireEvent.mouseMove(window, { clientX: 300, clientY: 260 })
    fireEvent.mouseUp(window)
    expect(canvas.style.transform).toBe(held)
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
