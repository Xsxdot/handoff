// treePrefs.test.ts —— 左栏偏好持久化与纯函数规则的单元测试。
//
// 职责：用最小的手写数据验证排序、隐藏和空闲目录折叠。
// 边界：不挂载 React 树；组件装配测试位于 ProjectTree.test.tsx。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_PREFS, PREFS_KEY, loadPrefs, savePrefs,
  sortProjects, splitHiddenProjects, splitIdleWorkspaces,
} from './treePrefs'

beforeEach(() => localStorage.clear())

describe('偏好读写', () => {
  it('没存过时给默认值', () => {
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })

  it('存过就读回来', () => {
    savePrefs({ ...DEFAULT_PREFS, hideIdleWorktrees: true, hiddenProjects: ['p2'] })
    expect(loadPrefs().hideIdleWorktrees).toBe(true)
    expect(loadPrefs().hiddenProjects).toEqual(['p2'])
  })

  it('坏 JSON 静默回退默认值，不抛错', () => {
    localStorage.setItem(PREFS_KEY, '{不是 json')
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })

  it('版本号对不上就丢弃', () => {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ v: 99, hideIdleWorktrees: true }))
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })

  it('字段类型不对就丢弃（防手改）', () => {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ v: 1, hideIdleWorktrees: 'yes', projectSort: 'active', hiddenProjects: [] }))
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })

  it('旧盘没有 hideArchived 时当 false，不整份丢弃', () => {
    // v:1 当初没有这个字段。bump 版本会把用户的排序和隐藏名单清掉，所以只补默认值。
    localStorage.setItem(PREFS_KEY, JSON.stringify({
      v: 1, hideIdleWorktrees: true, projectSort: 'name', hiddenProjects: ['p2'],
    }))
    expect(loadPrefs()).toEqual({
      v: 1, hideIdleWorktrees: true, projectSort: 'name', hiddenProjects: ['p2'], hideArchived: false,
    })
  })

  it('hideArchived 能存能取', () => {
    savePrefs({ ...DEFAULT_PREFS, hideArchived: true })
    expect(loadPrefs().hideArchived).toBe(true)
  })
})

describe('项目排序', () => {
  const list = [
    { id: 'a', name: 'zeta', active: 1, updatedAt: '2026-08-18T10:00:00+08:00' },
    { id: 'b', name: 'alpha', active: 3, updatedAt: '2026-08-16T10:00:00+08:00' },
    { id: 'c', name: 'mid', active: 1, updatedAt: '2026-08-17T10:00:00+08:00' },
  ]
  const metricsOf = (x: (typeof list)[number]) => ({ active: x.active, updatedAt: x.updatedAt, name: x.name })

  it('active：活跃多的在前，相同活跃按名称升序兜底', () => {
    expect(sortProjects(list, metricsOf, 'active').map((x) => x.id)).toEqual(['b', 'c', 'a'])
  })

  it('name：纯名称升序', () => {
    expect(sortProjects(list, metricsOf, 'name').map((x) => x.id)).toEqual(['b', 'c', 'a'])
  })

  it('recent：最近动过的在前，没有时间的排最后', () => {
    const withEmpty = [...list, { id: 'd', name: 'never', active: 0, updatedAt: '' }]
    expect(sortProjects(withEmpty, metricsOf, 'recent').map((x) => x.id)).toEqual(['a', 'c', 'b', 'd'])
  })

  it('不改入参', () => {
    const copy = [...list]
    sortProjects(list, metricsOf, 'name')
    expect(list).toEqual(copy)
  })
})

describe('项目隐藏', () => {
  const list = [{ id: 'p1' }, { id: 'p2' }, { id: 'p3' }]
  it('剔除名单里的，并报出被剔了几个', () => {
    const r = splitHiddenProjects(list, (x) => x.id, ['p2'])
    expect(r.shown.map((x) => x.id)).toEqual(['p1', 'p3'])
    expect(r.hiddenCount).toBe(1)
  })
  it('名单为空时原样返回', () => {
    expect(splitHiddenProjects(list, (x) => x.id, []).hiddenCount).toBe(0)
  })
})

describe('空闲目录折叠', () => {
  const list = [
    { p: '/w', isMain: true, selected: false, active: 0 },
    { p: '/w/busy', isMain: false, selected: false, active: 2 },
    { p: '/w/idle', isMain: false, selected: false, active: 0 },
    { p: '/w/picked', isMain: false, selected: true, active: 0 },
  ]
  const infoOf = (x: (typeof list)[number]) => ({ isMain: x.isMain, selected: x.selected, active: x.active })

  it('关掉开关时一个都不折', () => {
    expect(splitIdleWorkspaces(list, infoOf, false).hidden).toEqual([])
  })

  it('开着时只折无活跃任务的，主工作树与选中目录豁免', () => {
    const r = splitIdleWorkspaces(list, infoOf, true)
    expect(r.shown.map((x) => x.p)).toEqual(['/w', '/w/busy', '/w/picked'])
    expect(r.hidden.map((x) => x.p)).toEqual(['/w/idle'])
  })

  it('保持入参顺序（排序已由 sortWorkspaces 定好，这里不得重排）', () => {
    const r = splitIdleWorkspaces([...list].reverse(), infoOf, true)
    expect(r.shown.map((x) => x.p)).toEqual(['/w/picked', '/w/busy', '/w'])
  })
})
