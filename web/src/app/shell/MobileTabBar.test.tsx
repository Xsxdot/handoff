// MobileTabBar 的缝侧守卫：三个一级 tab 的存在、顺序、选择上抛与角标。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MobileTabBar } from './MobileTabBar'

describe('MobileTabBar', () => {
  it('渲染三个一级 tab，顺序为 工作台/工作项/设置', () => {
    render(<MobileTabBar active="sessions" onSelect={vi.fn()} />)
    const tabs = screen.getAllByRole('tab')
    expect(tabs.map((el) => el.getAttribute('data-testid'))).toEqual([
      'mobile-tab-projects', 'mobile-tab-cards', 'mobile-tab-settings',
    ])
  })

  it('点击把目标 tab 上抛', () => {
    const onSelect = vi.fn()
    render(<MobileTabBar active="sessions" onSelect={onSelect} />)
    fireEvent.click(screen.getByTestId('mobile-tab-cards'))
    expect(onSelect).toHaveBeenCalledWith('cards')
  })

  it('active 反映在 aria-selected', () => {
    render(<MobileTabBar active="projects" onSelect={vi.fn()} />)
    expect(screen.getByTestId('mobile-tab-projects')).toHaveAttribute('aria-selected', 'true')
    expect(screen.queryByTestId('mobile-tab-sessions')).not.toBeInTheDocument()
  })

  it('角标：会话显示 Σ 未读、卡显示需要你数，0 时不渲染', () => {
    render(<MobileTabBar active="sessions" onSelect={vi.fn()} unread={2} needsCount={3} />)
    expect(screen.getByTestId('mobile-tab-projects')).toHaveTextContent('2')
    expect(screen.getByTestId('mobile-tab-cards')).toHaveTextContent('3')
    expect(screen.getByTestId('mobile-tab-settings')).not.toHaveTextContent(/\d/)
  })
})
