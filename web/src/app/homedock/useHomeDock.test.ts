import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { defaultGeom, useHomeDock } from './useHomeDock'

describe('useHomeDock', () => {
  it('新建终端：进列表、被激活、浮窗打开', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
    expect(result.current.windowOpen).toBe(true)
  })

  it('seq 递增，且关掉再开不复用旧号', () => {
    // why：标题里的编号是给人认的。复用号会让「home 2」在一次会话里指过两个
    // 不同的终端，用户按标题找会找错
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newTerminal())
    const second = result.current.tabs[1]
    act(() => result.current.closeTab(second.id))
    act(() => result.current.newTerminal())
    expect(result.current.tabs[1].seq).toBe(3)
  })

  it('收起浮窗不动 tabs——会话还在', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.collapse())
    expect(result.current.windowOpen).toBe(false)
    expect(result.current.tabs).toHaveLength(1)
  })

  it('关掉激活项时激活权交给剩下的第一个', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newTerminal())
    const active = result.current.activeId!
    act(() => result.current.closeTab(active))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
  })

  it('关掉最后一个：浮窗自动收起，activeId 归 null', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.closeTab(result.current.tabs[0].id))
    expect(result.current.tabs).toHaveLength(0)
    expect(result.current.windowOpen).toBe(false)
    expect(result.current.activeId).toBeNull()
  })

  it('activate 会把收起的浮窗重新打开', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.collapse())
    act(() => result.current.activate(result.current.tabs[0].id))
    expect(result.current.windowOpen).toBe(true)
  })

  it('adopt 收编既有会话，但不抢焦点也不弹窗', () => {
    // why：恢复是后台动作。页面一加载就弹出浮窗，等于替用户点了一下
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.adopt({ id: 'r1', kind: 'terminal', seq: 1, sessionId: 's1', machine: '' }))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.windowOpen).toBe(false)
  })

  it('newFile 建出一个 file 种类的 tab 并激活它、打开浮窗', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newFile('untitled-1.md'))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.tabs[0]).toMatchObject({ kind: 'file', rel: 'untitled-1.md' })
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
    expect(result.current.windowOpen).toBe(true)
  })

  it('终端与文件共用同一个只增不减的 seq 计数器，不会撞号', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newFile('untitled-1.md'))
    act(() => result.current.newTerminal())
    expect(result.current.tabs.map((t) => t.seq)).toEqual([1, 2, 3])
  })

  it('setDraft 把草稿寄存到 tab 上，切走再切回来还在', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newFile('untitled-1.md'))
    const fileId = result.current.tabs[0].id
    act(() => result.current.newTerminal())
    act(() => result.current.setDraft(fileId, { draft: '临时内容', baseSha: 'sha-1' }))
    expect(result.current.tabs.find((t) => t.id === fileId)).toMatchObject({
      draft: '临时内容',
      baseSha: 'sha-1',
    })
    act(() => result.current.activate(fileId))
    expect(result.current.tabs.find((t) => t.id === fileId)).toMatchObject({
      draft: '临时内容',
      baseSha: 'sha-1',
    })
  })
})

describe('defaultGeom', () => {
  it('面积占视口四分之一：宽高各取一半', () => {
    const g = defaultGeom(1200, 800)
    expect(g.w).toBe(600)
    expect(g.h).toBe(400)
  })

  it('右下角贴着悬浮球：右沿与球右沿齐平，下沿在球正上方', () => {
    // why：浮窗是从右下角那颗球里长出来的。开在偏左上会让人找不到它和刚点的
    // 那颗球之间的关系（走查原话：「很奇怪」）
    const g = defaultGeom(1200, 800)
    expect(g.x + g.w).toBe(1200 - 20) // 球 right-5
    expect(g.y + g.h).toBe(800 - 44 - 44 - 8) // 球 bottom-11 + size-11 + 缝
  })

  it('顶部让位（桌面薄壳）时不会钻进窗口拖动区', () => {
    // 视口很矮时 y 会被顶到下界，那个下界必须含让位量，否则标题栏抓不住
    const g = defaultGeom(1200, 300, 28)
    expect(g.y).toBeGreaterThanOrEqual(28 + 8)
  })

  it('视口小到装不下半屏时被最小尺寸兜住，且不出左上边界', () => {
    const g = defaultGeom(500, 300)
    expect(g.w).toBe(360) // 半屏 250 比 MIN_W 小，抬到 MIN_W
    expect(g.h).toBe(200) // 半屏 150 比 MIN_H 小，抬到 MIN_H
    expect(g.x).toBeGreaterThanOrEqual(8)
    expect(g.y).toBeGreaterThanOrEqual(8)
  })
})

describe('useHomeDock maximize', () => {
  it('切换最大化不改写 geom——还原要回到用户自己摆的位置', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.setGeom({ x: 120, y: 90, w: 500, h: 300 }))
    const before = result.current.geom
    act(() => result.current.toggleMaximize())
    expect(result.current.maximized).toBe(true)
    expect(result.current.geom).toEqual(before)
    act(() => result.current.toggleMaximize())
    expect(result.current.maximized).toBe(false)
    expect(result.current.geom).toEqual(before)
  })
})

describe('浮窗首次打开才定位', () => {
  it('几何在**打开那一刻**按视口算，不是挂载时算', () => {
    // why：挂载时页面还没定稿（实测控制台首帧 innerWidth 只有几百 px），
    // 那时算出来的浮窗会被最小尺寸兜成 360×200 钉在左上角——正是要修的毛病
    const { result } = renderHook(() => useHomeDock())
    window.innerWidth = 1600
    window.innerHeight = 900
    act(() => result.current.newTerminal())
    expect(result.current.geom).toEqual(defaultGeom(1600, 900))
  })

  it('用户摆过之后再打开，不把他摆的位置冲掉', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.setGeom({ x: 200, y: 150, w: 500, h: 300 }))
    act(() => result.current.newTerminal())
    expect(result.current.geom).toEqual({ x: 200, y: 150, w: 500, h: 300 })
  })
})

describe('hydrate', () => {
  it('hydrate 之后新建 tab 不与恢复出来的撞 id / seq', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() =>
      result.current.hydrate({
        tabs: [
          { id: 'h1', kind: 'terminal', seq: 1, sessionId: 'S1', machine: '' },
          { id: 'h5', kind: 'terminal', seq: 7, machine: '' },
        ],
        activeId: 'h5',
        windowOpen: true,
        geom: { x: 100, y: 100, w: 620, h: 340 },
        maximized: false,
      }),
    )
    expect(result.current.tabs).toHaveLength(2)
    expect(result.current.windowOpen).toBe(true)
    expect(result.current.activeId).toBe('h5')

    act(() => result.current.newTerminal())
    const fresh = result.current.tabs[2]
    // id 必须跳过已恢复的 h5
    expect(result.current.tabs.map((t) => t.id)).toHaveLength(new Set(result.current.tabs.map((t) => t.id)).size)
    expect(fresh.id).toBe('h6')
    // seq 必须跳过已恢复的 7
    expect(fresh.seq).toBe(8)
  })

  it('adopt 进来的 sessionId 形 id 不参与播种', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() =>
      result.current.hydrate({
        // 孤儿会话被 adopt 时 id 就是 sessionId（见 Shell 的调用），不是 h<n> 形状
        tabs: [{ id: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad', kind: 'terminal', seq: 3, sessionId: 'S9', machine: '' }],
        activeId: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad',
        windowOpen: false,
        geom: { x: 10, y: 40, w: 620, h: 340 },
        maximized: false,
      }),
    )
    act(() => result.current.newTerminal())
    expect(result.current.tabs[1].id).toBe('h1')
    expect(result.current.tabs[1].seq).toBe(4)
  })

  it('hydrate 之后再打开浮窗不会把恢复的位置冲掉', () => {
    const { result } = renderHook(() => useHomeDock())
    const geom = { x: 123, y: 234, w: 620, h: 340 }
    act(() => result.current.hydrate({ tabs: [], activeId: null, windowOpen: false, geom, maximized: false }))
    // newTerminal 内部会 openWindow；placed 已被 hydrate 置 true，不该重摆
    act(() => result.current.newTerminal())
    expect(result.current.geom).toEqual(geom)
  })
})
