import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { IconMenu } from './IconMenu'

const items = (onA = vi.fn(), onB = vi.fn()) => [
  { key: 'a', label: '新终端', onSelect: onA },
  { key: 'b', label: '新建文件', hotkey: '⌘N', onSelect: onB },
]

describe('IconMenu', () => {
  it('默认收起，点触发器才弹出', () => {
    render(<IconMenu label="新建" icon={<span>+</span>} items={items()} />)
    expect(screen.queryByRole('menu')).toBeNull()
    fireEvent.click(screen.getByLabelText('新建'))
    expect(screen.getByRole('menu')).toBeInTheDocument()
    expect(screen.getAllByRole('menuitem')).toHaveLength(2)
  })

  it('选中一项后回调并收起菜单', () => {
    const onA = vi.fn()
    render(<IconMenu label="新建" icon={<span>+</span>} items={items(onA)} />)
    fireEvent.click(screen.getByLabelText('新建'))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    expect(onA).toHaveBeenCalledTimes(1)
    // 不传实参：调用方常把回调直接接到形参带可选参数的函数上，漏参会存进业务字段
    expect(onA.mock.calls[0]).toHaveLength(0)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('点外面与按 Esc 都收起', () => {
    render(<IconMenu label="新建" icon={<span>+</span>} items={items()} />)
    fireEvent.click(screen.getByLabelText('新建'))
    fireEvent.mouseDown(document.body)
    expect(screen.queryByRole('menu')).toBeNull()

    fireEvent.click(screen.getByLabelText('新建'))
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('再点一次触发器是收起，不是「点不开」', () => {
    // why：外部 mousedown 监听若不放过触发器自己，toggle 与「点外面关闭」会
    // 互相抵消——按下时先关、抬起时再开，菜单看起来永远打不开
    render(<IconMenu label="新建" icon={<span>+</span>} items={items()} />)
    const trigger = screen.getByLabelText('新建')
    fireEvent.click(trigger)
    expect(screen.getByRole('menu')).toBeInTheDocument()
    fireEvent.mouseDown(trigger)
    fireEvent.click(trigger)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('真实鼠标序列（mousedown → click）也能选中——不是点了没反应', () => {
    // why：真实点击先发 mousedown。外部关闭监听若不放过菜单自身，这一拍就把
    // 菜单项摘出 DOM，mouseup 落到空处、click 根本不发生。走查里就是这么
    // 「点了没反应」的；只用 fireEvent.click 的用例测不出来
    const onA = vi.fn()
    render(<IconMenu label="新建" icon={<span>+</span>} items={items(onA)} />)
    fireEvent.mouseDown(screen.getByLabelText('新建'))
    fireEvent.click(screen.getByLabelText('新建'))
    const item = screen.getByRole('menuitem', { name: /新终端/ })
    fireEvent.mouseDown(item)
    expect(screen.getByRole('menu')).toBeInTheDocument() // 这一拍还不能收
    fireEvent.click(item)
    expect(onA).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('菜单挂在 body 上，不留在触发器的 DOM 子树里', () => {
    // why：tab 条是 overflow-x-auto、浮窗外框是 overflow-hidden，
    // 菜单留在原地会被裁掉只露出一条边
    const { container } = render(<IconMenu label="新建" icon={<span>+</span>} items={items()} />)
    fireEvent.click(screen.getByLabelText('新建'))
    expect(container.querySelector('[role="menu"]')).toBeNull()
    expect(document.body.querySelector('[role="menu"]')).not.toBeNull()
  })
})

describe('扩展项', () => {
  it('check 选中时渲染勾、未选中不渲染', () => {
    render(
      <IconMenu
        label="偏好"
        icon={<span>i</span>}
        items={[
          { key: 'a', label: '隐藏空闲', kind: 'check', checked: true, onSelect: vi.fn() },
          { key: 'b', label: '别的开关', kind: 'check', checked: false, onSelect: vi.fn() },
        ]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    expect(screen.getByRole('menuitemcheckbox', { name: /隐藏空闲/ })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('menuitemcheckbox', { name: /别的开关/ })).toHaveAttribute('aria-checked', 'false')
  })

  it('keepOpen 的项点完菜单还在', () => {
    const onSelect = vi.fn()
    render(
      <IconMenu label="偏好" icon={<span>i</span>}
        items={[{ key: 'a', label: '隐藏空闲', kind: 'check', checked: false, keepOpen: true, onSelect }]} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏空闲/ }))
    expect(onSelect).toHaveBeenCalled()
    expect(screen.getByRole('menu')).toBeInTheDocument()
  })

  it('header 不可点', () => {
    render(
      <IconMenu label="偏好" icon={<span>i</span>}
        items={[{ key: 'h', label: '显示', kind: 'header' }]} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    expect(screen.queryByRole('menuitem', { name: '显示' })).toBeNull()
    expect(screen.getByText('显示')).toBeInTheDocument()
  })

  it('radio 用 menuitemradio 角色', () => {
    render(
      <IconMenu label="偏好" icon={<span>i</span>}
        items={[{ key: 'r', label: '活跃优先', kind: 'radio', checked: true, keepOpen: true, onSelect: vi.fn() }]} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    expect(screen.getByRole('menuitemradio', { name: /活跃优先/ })).toHaveAttribute('aria-checked', 'true')
  })

  it('不带 kind 的老用法照旧：点完就关', () => {
    const onSelect = vi.fn()
    render(<IconMenu label="操作" icon={<span>i</span>} items={[{ key: 'a', label: '关闭', onSelect }]} />)
    fireEvent.click(screen.getByRole('button', { name: '操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '关闭' }))
    expect(onSelect).toHaveBeenCalled()
    expect(screen.queryByRole('menu')).toBeNull()
  })
})
