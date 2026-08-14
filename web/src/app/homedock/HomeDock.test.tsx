import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { HomeDock } from './HomeDock'
import type { HomeTab } from './useHomeDock'

// dock 造一个 HomeDockApi 替身。只给本组件真正读写的字段，
// 其余动作用 vi.fn() —— 本用例测的是入口面板，不是状态机（那是 Task 1 的事）
function dock(over: Partial<{ tabs: HomeTab[]; windowOpen: boolean }> = {}) {
  return {
    tabs: [] as HomeTab[],
    activeId: null,
    windowOpen: false,
    geom: { x: 0, y: 0, w: 600, h: 300 },
    newTerminal: vi.fn(),
    activate: vi.fn(),
    collapse: vi.fn(),
    closeTab: vi.fn(),
    setSession: vi.fn(),
    setGeom: vi.fn(),
    adopt: vi.fn(),
    ...over,
  }
}

const TAB_A: HomeTab = { id: 'a', seq: 1, machine: '' }
const TAB_B: HomeTab = { id: 'b', seq: 2, machine: '' }
const props = (d: ReturnType<typeof dock>) => ({
  dock: d,
  onKill: vi.fn(),
  renderTab: () => <div data-testid="term" />,
})

describe('HomeDock', () => {
  it('无会话时圆钮不带角标', () => {
    render(<HomeDock {...props(dock())} />)
    expect(screen.getByLabelText('home 基准终端')).toBeInTheDocument()
    expect(screen.queryByTestId('home-badge')).toBeNull()
  })

  it('有会话时角标显示数量', () => {
    render(<HomeDock {...props(dock({ tabs: [TAB_A, TAB_B] }))} />)
    expect(screen.getByTestId('home-badge')).toHaveTextContent('2')
  })

  it('浮窗收起后角标仍在——这是「收起不杀」的唯一可见证据', () => {
    // why：浮窗收起后，圆钮角标是用户唯一能看到「还有几个会话活着」的地方。
    // 没有它，「收起不杀」这条口径在界面上就不成立——用户会以为会话没了
    render(<HomeDock {...props(dock({ tabs: [TAB_A, TAB_B], windowOpen: false }))} />)
    expect(screen.getByTestId('home-badge')).toHaveTextContent('2')
  })

  it('windowOpen 时渲染浮窗内容，收起时不渲染', () => {
    const { rerender } = render(<HomeDock {...props(dock({ tabs: [TAB_A], windowOpen: true, activeId: 'a' } as never))} />)
    expect(screen.getByTestId('term')).toBeInTheDocument()
    rerender(<HomeDock {...props(dock({ tabs: [TAB_A], windowOpen: false }))} />)
    expect(screen.queryByTestId('term')).toBeNull()
  })

  it('零终端时点圆钮直接开一个终端，不弹中间面板', () => {
    const d = dock()
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.newTerminal).toHaveBeenCalledTimes(1)
    expect(d.activate).not.toHaveBeenCalled()
  })

  it('已有终端且浮窗收起时，点圆钮重开到收起前那个', () => {
    // why：collapse 刻意不动 activeId，所以「收起前你在看哪个」这个信息还在。
    // 永远取最后一个会让「收起→重开」把用户挪到别的终端上
    // 工厂把 activeId 收窄成 null 字面量，赋不了值，断言整体类型绕开
    const d = dock({ tabs: [TAB_A, TAB_B], windowOpen: false } as never)
    ;(d as { activeId: string | null }).activeId = 'a'
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.activate).toHaveBeenCalledWith('a')
    expect(d.newTerminal).not.toHaveBeenCalled()
  })

  it('activeId 为 null 时兜底到最后一个 tab，不把 null 送进 activate', () => {
    const d = dock({ tabs: [TAB_A, TAB_B], windowOpen: false } as never)
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.activate).toHaveBeenCalledWith('b')
  })

  it('浮窗开着时点圆钮 = 收起', () => {
    const d = dock({ tabs: [TAB_A], windowOpen: true, activeId: 'a' } as never)
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(d.collapse).toHaveBeenCalledTimes(1)
    expect(d.newTerminal).not.toHaveBeenCalled()
    expect(d.activate).not.toHaveBeenCalled()
  })

  it('浮窗开着时圆钮仍在——它是开合开关，不是只在收起时出现', () => {
    const d = dock({ tabs: [TAB_A], windowOpen: true, activeId: 'a' } as never)
    render(<HomeDock {...props(d)} />)
    expect(screen.getByLabelText('home 基准终端')).toBeInTheDocument()
  })
})
