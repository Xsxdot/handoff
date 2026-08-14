import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ContextMenu } from './ContextMenu'

const items = [{ label: '注销', onSelect: vi.fn(), danger: true }]

describe('ContextMenu', () => {
  it('渲染成 menu，项是 menuitem', () => {
    render(<ContextMenu x={10} y={20} items={items} onClose={vi.fn()} />)
    expect(screen.getByRole('menu')).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: '注销' })).toBeInTheDocument()
  })

  it('点菜单项：先执行动作，再关闭', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    render(<ContextMenu x={10} y={20} items={[{ label: '注销', onSelect }]} onClose={onClose} />)
    fireEvent.click(screen.getByRole('menuitem', { name: '注销' }))
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('按 Esc 关闭', () => {
    const onClose = vi.fn()
    render(<ContextMenu x={10} y={20} items={items} onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('点菜单外关闭，点菜单内不关', () => {
    const onClose = vi.fn()
    render(<ContextMenu x={10} y={20} items={items} onClose={onClose} />)
    fireEvent.pointerDown(screen.getByRole('menu'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.pointerDown(document.body)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('贴着视口右/下缘弹出时向内翻转，不被裁掉', () => {
    // why：右键点在窗口右边缘时，菜单从点击点向右展开会有一半在视口外，
    // 而它是 fixed 定位，页面滚动也拉不回来
    Object.defineProperty(window, 'innerWidth', { value: 800, configurable: true })
    Object.defineProperty(window, 'innerHeight', { value: 600, configurable: true })
    // jsdom 的 getBoundingClientRect 恒返回 0（量不出真实尺寸），翻转判断
    // 永不触发。这里打桩成有实际尺寸的菜单（140×40）再断言，用完 restore，
    // 不污染其他用例
    const rect = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockReturnValue({ width: 140, height: 40 } as DOMRect)
    render(<ContextMenu x={790} y={590} items={items} onClose={vi.fn()} />)
    const menu = screen.getByRole('menu')
    expect(Number.parseFloat(menu.style.left)).toBeLessThan(790)
    expect(Number.parseFloat(menu.style.top)).toBeLessThan(590)
    rect.mockRestore()
  })

  it('打开时焦点落到第一项', () => {
    render(<ContextMenu x={10} y={20} items={items} onClose={vi.fn()} />)
    expect(screen.getByRole('menuitem', { name: '注销' })).toHaveFocus()
  })
})
