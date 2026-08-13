import { describe, expect, it } from 'vitest'
import {
  EMPTY_WORKBENCH,
  activateTab,
  closeTab,
  dedupKey,
  nextTerminalSeq,
  openTab,
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
})

describe('splitGroup', () => {
  it('第一次分屏产生第二组并激活它；已经两组时是空操作', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    expect(wb.groups).toHaveLength(2)
    expect(wb.active).toBe(1)
    const again = splitGroup(wb)
    expect(again.groups).toHaveLength(2)
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
