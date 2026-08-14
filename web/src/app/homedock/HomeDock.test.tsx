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

  it('点圆钮出面板，列出已开终端与「新终端」', () => {
    render(<HomeDock {...props(dock({ tabs: [TAB_A] }))} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    expect(screen.getByText('bash · home 1')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
    // 圆钮与面板互斥：面板出来了圆钮就该消失
    expect(screen.queryByLabelText('home 基准终端')).toBeNull()
  })

  it('点清单某项 → activate 并收起面板', () => {
    const d = dock({ tabs: [TAB_A] })
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    fireEvent.click(screen.getByText('bash · home 1'))
    expect(d.activate).toHaveBeenCalledWith('a')
    expect(screen.queryByRole('button', { name: /新终端/ })).toBeNull()
  })

  it('点「新终端」走 newTerminal 并收起面板', () => {
    const d = dock()
    render(<HomeDock {...props(d)} />)
    fireEvent.click(screen.getByLabelText('home 基准终端'))
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(d.newTerminal).toHaveBeenCalledTimes(1)
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
})
