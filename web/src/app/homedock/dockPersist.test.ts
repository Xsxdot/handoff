// dockPersist.test.ts —— 悬浮窗现场编解码、会话清理与几何夹紧的纯函数测试。
//
// 职责：验证悬浮窗落盘格式、草稿隔离、死会话处理和跨视口恢复边界。
// 边界：不测试 React 状态容器、HTTP 或 DOM；这些由 useHomeDock 和同步层负责。
import { describe, expect, it } from 'vitest'
import {
  DOCK_PERSIST_VERSION,
  clampGeom,
  decodeDock,
  encodeDock,
  pruneDeadDockSessions,
  type DockSnapshot,
} from './dockPersist'

function snap(): DockSnapshot {
  return {
    tabs: [
      { id: 'h1', kind: 'terminal', seq: 1, sessionId: 'S1', machine: '' },
      { id: 'h2', kind: 'file', seq: 2, machine: '', rel: 'notes.md', draft: '改了一半', baseSha: 'abc' },
    ],
    activeId: 'h2',
    windowOpen: true,
    geom: { x: 100, y: 80, w: 620, h: 340 },
    maximized: false,
  }
}

describe('encodeDock / decodeDock', () => {
  it('往返之后相等，但草稿被剥掉', () => {
    const out = decodeDock(encodeDock(snap()))
    expect(out).not.toBeNull()
    expect(out!.tabs[1]).toEqual({ id: 'h2', kind: 'file', seq: 2, machine: '', rel: 'notes.md' })
    expect(out!.tabs[0]).toEqual({ id: 'h1', kind: 'terminal', seq: 1, sessionId: 'S1', machine: '' })
    expect(out!.activeId).toBe('h2')
    expect(out!.windowOpen).toBe(true)
    expect(out!.geom).toEqual({ x: 100, y: 80, w: 620, h: 340 })
    expect(out!.maximized).toBe(false)
  })

  it.each([
    ['不是 JSON', 'nope'],
    ['版本不认识', JSON.stringify({ v: 99, tabs: [], activeId: null, windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['tabs 不是数组', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: {}, activeId: null, windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['kind 不认识', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: [{ id: 'h1', kind: 'video', seq: 1, machine: '' }], activeId: 'h1', windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['activeId 指向不存在的 tab', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: [], activeId: 'h9', windowOpen: false, geom: { x: 0, y: 0, w: 1, h: 1 }, maximized: false })],
    ['geom 有非数字', JSON.stringify({ v: DOCK_PERSIST_VERSION, tabs: [], activeId: null, windowOpen: false, geom: { x: 0, y: 0, w: 'wide', h: 1 }, maximized: false })],
  ])('坏数据「%s」整份丢弃', (_n, raw) => {
    expect(decodeDock(raw as string)).toBeNull()
  })
})

describe('pruneDeadDockSessions', () => {
  it('死 sessionId 被抹掉，tab 留着', () => {
    const out = pruneDeadDockSessions(snap().tabs, new Set<string>())
    expect(out[0]).toEqual({ id: 'h1', kind: 'terminal', seq: 1, machine: '' })
    expect(out).toHaveLength(2)
  })

  it('活会话与非终端 tab 原样保留', () => {
    const tabs = snap().tabs
    const out = pruneDeadDockSessions(tabs, new Set(['S1']))
    expect(out[0].sessionId).toBe('S1')
    expect(out[1]).toEqual(tabs[1])
  })
})

describe('clampGeom', () => {
  it('上次在大屏、这次在小屏时把浮窗拉回视口内', () => {
    const out = clampGeom({ x: 2000, y: 1400, w: 900, h: 700 }, 1280, 800, 28)
    expect(out.x + out.w).toBeLessThanOrEqual(1280)
    expect(out.y + out.h).toBeLessThanOrEqual(800)
    expect(out.x).toBeGreaterThanOrEqual(8)
    expect(out.y).toBeGreaterThanOrEqual(28 + 8)
  })

  it('本来就在视口内的几何原样返回', () => {
    const g = { x: 100, y: 100, w: 620, h: 340 }
    expect(clampGeom(g, 1280, 800, 28)).toEqual(g)
  })

  it('尺寸不小于下界', () => {
    const out = clampGeom({ x: 0, y: 0, w: 10, h: 10 }, 1280, 800, 0)
    expect(out.w).toBeGreaterThanOrEqual(360)
    expect(out.h).toBeGreaterThanOrEqual(200)
  })

  it('视口比最小尺寸还小时，保证不出屏优先于保证最小尺寸', () => {
    const out = clampGeom({ x: 0, y: 0, w: 620, h: 340 }, 300, 150, 0)
    expect(out.x).toBe(8)
    expect(out.y).toBe(8)
    // 视口装不下 360×200 时不再强行放大，宽高被视口夹住
    expect(out.w).toBeLessThanOrEqual(300 - 8)
    expect(out.h).toBeLessThanOrEqual(150 - 8)
  })
})
