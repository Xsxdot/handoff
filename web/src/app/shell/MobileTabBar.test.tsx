// MobileTabBar 的缝侧守卫：四个一级 tab 的存在、顺序、选择上抛与角标。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MobileTabBar } from './MobileTabBar'

describe('MobileTabBar', () => {
  it('渲染四个一级 tab，顺序为 会话/卡/项目/设置', () => {
    render(<MobileTabBar active="sessions" onSelect={vi.fn()} />)
    const tabs = screen.getAllByRole('tab')
    expect(tabs.map((el) => el.getAttribute('data-testid'))).toEqual([
      'mobile-tab-sessions', 'mobile-tab-cards', 'mobile-tab-projects', 'mobile-tab-settings',
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
    expect(screen.getByTestId('mobile-tab-sessions')).toHaveAttribute('aria-selected', 'false')
  })

  it('角标：会话显示 Σ 未读、卡显示需要你数，0 时不渲染', () => {
    render(<MobileTabBar active="sessions" onSelect={vi.fn()} unread={2} needsCount={3} />)
    expect(screen.getByTestId('mobile-tab-sessions')).toHaveTextContent('2')
    expect(screen.getByTestId('mobile-tab-cards')).toHaveTextContent('3')
    expect(screen.getByTestId('mobile-tab-projects')).not.toHaveTextContent(/\d/)
  })
})
