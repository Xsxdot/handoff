import { describe, expect, it, vi } from 'vitest'
import {
  EMPTY_WORKBENCH,
  MIN_PANE_PX,
  activateTab,
  availablePaneWidth,
  closeTab,
  dedupKey,
  nextTerminalSeq,
  openTab,
  resizeGroups,
  setTabContent,
  splitGroup,
  tabTitle,
  type Workbench,
} from './tabs'

describe('dedupKey', () => {
  it('文件与 TUI 按目标去重，终端与空白不去重', () => {
    expect(dedupKey({ kind: 'file', rel: 'go.mod' })).toBe('file:go.mod')
    expect(dedupKey({ kind: 'tui', taskId: 'T1' })).toBe('tui:T1')
    expect(dedupKey({ kind: 'terminal', seq: 2 })).toBeNull()
    expect(dedupKey({ kind: 'blank' })).toBeNull()
  })

  it('file tab 的去重键只看 rel，草稿不参与——同一个文件不该因为改了字就开出第二个 tab', () => {
    expect(dedupKey({ kind: 'file', rel: 'a.go', draft: 'x', baseSha: 'h' })).toBe(
      dedupKey({ kind: 'file', rel: 'a.go' }),
    )
  })
})

describe('openTab', () => {
  it('开一个 tab 并自动激活它', () => {
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'go.mod' })
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[0].activeId).toBe(wb.groups[0].tabs[0].id)
  })

  it('同身份不重复打开，已存在则激活', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'go.mod' })
    const firstId = wb.groups[0].tabs[0].id
    wb = openTab(wb, { kind: 'tui', taskId: 'T1' })
    wb = openTab(wb, { kind: 'file', rel: 'go.mod' })
    expect(wb.groups[0].tabs).toHaveLength(2)
    expect(wb.groups[0].activeId).toBe(firstId)
  })

  it('终端可以在同一目录开多个', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    wb = openTab(wb, { kind: 'terminal', seq: 2 })
    expect(wb.groups[0].tabs).toHaveLength(2)
  })

  it('去重跨组生效：另一组已有同身份 tab 时激活它而不是再开一个', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'tui', taskId: 'T1' })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'a.go' }, 1)
    wb = openTab(wb, { kind: 'tui', taskId: 'T1' }, 1)
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[1].tabs).toHaveLength(1)
    expect(wb.active).toBe(0)
    expect(wb.groups[0].activeId).toBe(wb.groups[0].tabs[0].id)
  })

  it('tab id 在整个 workbench 内唯一', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'terminal', seq: 2 }, 1)
    const ids = wb.groups.flatMap((g) => g.tabs.map((t) => t.id))
    expect(new Set(ids).size).toBe(ids.length)
  })
})

describe('closeTab', () => {
  it('关掉激活 tab 后激活右邻，没有右邻则左邻', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = openTab(wb, { kind: 'file', rel: 'b.go' })
    wb = openTab(wb, { kind: 'file', rel: 'c.go' })
    const [a, b, c] = wb.groups[0].tabs
    wb = activateTab(wb, 0, b.id)
    wb = closeTab(wb, 0, b.id)
    expect(wb.groups[0].activeId).toBe(c.id)
    wb = closeTab(wb, 0, c.id)
    expect(wb.groups[0].activeId).toBe(a.id)
  })

  it('关掉非激活 tab 不改变激活项', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = openTab(wb, { kind: 'file', rel: 'b.go' })
    const [a, b] = wb.groups[0].tabs
    expect(wb.groups[0].activeId).toBe(b.id)
    wb = closeTab(wb, 0, a.id)
    expect(wb.groups[0].activeId).toBe(b.id)
  })

  it('两组时关掉一组的最后一个 tab，该组消失，另一组占满', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'b.go' }, 1)
    const bId = wb.groups[1].tabs[0].id
    wb = closeTab(wb, 1, bId)
    expect(wb.groups).toHaveLength(1)
    expect(wb.active).toBe(0)
    expect(wb.groups[0].tabs[0].content).toEqual({ kind: 'file', rel: 'a.go' })
  })

  it('三组时关空最右一组，焦点接替相邻组而不是跳回最左，sizes 重新等分', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'b.go' }, 1)
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'c.go' }, 2)
    const cId = wb.groups[2].tabs[0].id

    wb = closeTab(wb, 2, cId)

    expect(wb.groups).toHaveLength(2)
    expect(wb.active).toBe(1)
    expect(wb.sizes).toEqual([1, 1])
  })

  it('单组时关掉最后一个 tab，组保留但变空', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = closeTab(wb, 0, wb.groups[0].tabs[0].id)
    expect(wb.groups).toHaveLength(1)
    expect(wb.groups[0].tabs).toHaveLength(0)
    expect(wb.groups[0].activeId).toBeNull()
  })
})

describe('setTabContent', () => {
  it('空白 tab 选了种类后原地变成对应内容，位置与 id 不变', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    const id = wb.groups[0].tabs[0].id
    wb = setTabContent(wb, 0, id, { kind: 'terminal', seq: 1 })
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[0].tabs[0].id).toBe(id)
    expect(wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
  })

  it('选中的目标已在别的 tab 里打开时，合并到那个 tab 并关掉空白 tab', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    const existing = wb.groups[0].tabs[0].id
    wb = openTab(wb, { kind: 'blank' })
    const blank = wb.groups[0].tabs[1].id
    wb = setTabContent(wb, 0, blank, { kind: 'file', rel: 'a.go' })
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[0].activeId).toBe(existing)
  })

  it('回写内容不抢焦点——这是「点不进新标签页」的根因', () => {
    // why：setTabContent 是「换这个 tab 的内容」，不是「切到这个 tab」。
    // 焊在一起的话，FileTab 卸载时回写草稿（Shell.tsx onDraftChange）
    // 会把焦点从用户刚点开的空白 tab 拽回文件 tab
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'go.mod' })
    const fileId = wb.groups[0].tabs[0].id
    wb = openTab(wb, { kind: 'blank' })
    const blankId = wb.groups[0].tabs[1].id
    expect(wb.groups[0].activeId).toBe(blankId)

    // 模拟 FileTab 卸载时的草稿回写：写的是**非激活**的那个 tab
    wb = setTabContent(wb, 0, fileId, { kind: 'file', rel: 'go.mod', draft: 'x', baseSha: 'h' })

    expect(wb.groups[0].activeId).toBe(blankId)
    expect(wb.groups[0].tabs[0].content).toEqual({
      kind: 'file',
      rel: 'go.mod',
      draft: 'x',
      baseSha: 'h',
    })
  })

  it('回写非焦点组的内容不把焦点组抢过去', () => {
    // why：终端会话 id 回写（Shell.tsx onSession）可能发生在用户已经切到
    // 另一组之后。next.active 一起改掉的话，分屏时焦点会莫名跳组
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    const termId = wb.groups[0].tabs[0].id
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'blank' }, 1)
    expect(wb.active).toBe(1)

    wb = setTabContent(wb, 0, termId, { kind: 'terminal', seq: 1, sessionId: 'S1' })

    expect(wb.active).toBe(1)
    expect(wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
  })
})

describe('splitGroup', () => {
  it('连续分屏到三组封顶，第四次是空操作', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    expect(wb.groups).toHaveLength(2)
    expect(wb.active).toBe(1)
    wb = splitGroup(wb)
    expect(wb.groups).toHaveLength(3)
    expect(wb.active).toBe(2)
    // 到顶时原样返回同一个对象引用：调用方据此可以跳过一次无谓的 setState
    const again = splitGroup(wb)
    expect(again).toBe(wb)
  })

  it('每次分屏后 sizes 与 groups 等长且等分', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    expect(wb.sizes).toEqual([1])
    wb = splitGroup(wb)
    expect(wb.sizes).toEqual([1, 1])
    wb = splitGroup(wb)
    expect(wb.sizes).toEqual([1, 1, 1])
  })
})

describe('resizeGroups', () => {
  // 两栏起手：sizes 是 [1, 1]，总和 2，各占一半
  const twoGroups = () => splitGroup(openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' }))

  it('分隔条右移，左栏变宽右栏变窄，权重总和不变', () => {
    const wb = resizeGroups(twoGroups(), 0, 0.1, 0.2)
    expect(wb.sizes[0]).toBeCloseTo(1.2)
    expect(wb.sizes[1]).toBeCloseTo(0.8)
    expect(wb.sizes[0] + wb.sizes[1]).toBeCloseTo(2)
  })

  it('拖过头时夹在 minRatio，不把一栏压成一条缝', () => {
    // 从各占 0.5 出发想推 0.9 过去，右栏会变成 -0.4——必须被夹回 0.2
    const wb = resizeGroups(twoGroups(), 0, 0.9, 0.2)
    const total = wb.sizes[0] + wb.sizes[1]
    expect(wb.sizes[1] / total).toBeCloseTo(0.2)
    expect(wb.sizes[0] / total).toBeCloseTo(0.8)
  })

  it('已经贴着下限还继续往同一方向拖，是空操作（返回同一个对象）', () => {
    const wb = resizeGroups(twoGroups(), 0, 0.9, 0.2)
    expect(resizeGroups(wb, 0, 0.5, 0.2)).toBe(wb)
  })

  it('容器窄到两栏都放不下 minRatio 时，拒绝改动而不是算出负宽度', () => {
    const wb = twoGroups()
    expect(resizeGroups(wb, 0, 0.1, 0.6)).toBe(wb)
  })

  it('越界的 dividerIndex 是空操作并留下一条 warn', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const wb = twoGroups()
    expect(resizeGroups(wb, 1, 0.1, 0.2)).toBe(wb)
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('三栏拖到底时，扣除两条 5px 分隔条后被挤栏仍至少 240px', () => {
    const containerWidth = 1060
    const paneWidth = availablePaneWidth(containerWidth, [5, 5])
    expect(paneWidth).toBe(1050)

    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    wb = splitGroup(wb)
    wb = resizeGroups(wb, 1, 1, MIN_PANE_PX / paneWidth)

    const total = wb.sizes.reduce((a, b) => a + b, 0)
    const squeezedPanePx = (wb.sizes[2] / total) * paneWidth
    expect(squeezedPanePx).toBeGreaterThanOrEqual(MIN_PANE_PX)
    expect(squeezedPanePx).toBeCloseTo(MIN_PANE_PX)
  })
})

describe('nextTerminalSeq', () => {
  it('从 1 起，跨组取最大值 +1', () => {
    expect(nextTerminalSeq(EMPTY_WORKBENCH)).toBe(1)
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'terminal', seq: 4 }, 1)
    expect(nextTerminalSeq(wb)).toBe(5)
  })
})

describe('tabTitle', () => {
  it('终端带基准目录名，文件取路径末段，TUI 带短 id，空白是「新建标签页」', () => {
    expect(tabTitle({ kind: 'terminal', seq: 2 }, 'b2-b3')).toBe('bash · b2-b3 (2)')
    expect(tabTitle({ kind: 'terminal', seq: 1 }, 'b2-b3')).toBe('bash · b2-b3')
    expect(tabTitle({ kind: 'file', rel: 'internal/agentd/server.go' }, 'b2-b3')).toBe('server.go')
    expect(tabTitle({ kind: 'tui', taskId: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad' }, 'b2-b3')).toBe('TUI · 7ec762e7')
    expect(tabTitle({ kind: 'blank' }, 'b2-b3')).toBe('新建标签页')
  })
})

// 不可变性：所有写入函数都必须返回新对象，否则 React 不会重渲染
describe('不可变性', () => {
  it('openTab 不修改入参', () => {
    const before: Workbench = EMPTY_WORKBENCH
    const after = openTab(before, { kind: 'blank' })
    expect(before.groups[0].tabs).toHaveLength(0)
    expect(after).not.toBe(before)
  })
})

describe('终端 tab 的会话身份', () => {
  it('还没建出会话的终端仍然永不去重——再点一次就是真的想要第二个', () => {
    expect(dedupKey({ kind: 'terminal', seq: 1 })).toBeNull()
    expect(dedupKey({ kind: 'terminal', seq: 2 })).toBeNull()
  })

  it('已有会话 id 的终端按会话去重：刷新恢复不该长出两个同一会话的 tab', () => {
    expect(dedupKey({ kind: 'terminal', seq: 1, sessionId: 'abc' })).toBe('pty:abc')
  })

  it('重复 openTab 同一个会话只得到一个 tab', () => {
    let wb = EMPTY_WORKBENCH
    wb = openTab(wb, { kind: 'terminal', seq: 1, sessionId: 'abc' })
    wb = openTab(wb, { kind: 'terminal', seq: 2, sessionId: 'abc' })
    expect(wb.groups[0].tabs).toHaveLength(1)
  })

  it('不同会话各占一个 tab', () => {
    let wb = EMPTY_WORKBENCH
    wb = openTab(wb, { kind: 'terminal', seq: 1, sessionId: 'a' })
    wb = openTab(wb, { kind: 'terminal', seq: 2, sessionId: 'b' })
    expect(wb.groups[0].tabs).toHaveLength(2)
  })

  it('nextTerminalSeq 不受 sessionId 影响', () => {
    let wb = EMPTY_WORKBENCH
    wb = openTab(wb, { kind: 'terminal', seq: 1, sessionId: 'a' })
    expect(nextTerminalSeq(wb)).toBe(2)
  })
})
