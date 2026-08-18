import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { DesktopTitleBar } from './DesktopTitleBar'
import { Breadcrumb } from './Breadcrumb'
import { DESKTOP_TOP_INSET } from '../lib/desktopShell'
import type { BaseDir } from '../workbench/useWorkbench'

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'b2-b3',
  projectName: 'handoff',
  machine: '',
}

describe('DesktopTitleBar', () => {
  it('把面包屑那几段画进标题栏，高度正好是让出的那条', () => {
    render(<DesktopTitleBar base={base} />)
    const bar = screen.getByTestId('desktop-title-bar')
    expect(bar).toHaveStyle({ height: `${DESKTOP_TOP_INSET}px` })
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('handoff')
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('b2-b3')
  })

  it('还没选目录时只显示应用名，不留一条空白', () => {
    render(<DesktopTitleBar base={null} />)
    expect(screen.getByTestId('desktop-title-bar')).toHaveTextContent('handoff')
  })

  it('标题栏里一个可点元素都不能有', () => {
    // why（承重）：薄壳把这条 28px 换成了 AppKit 的原生拖动区，里面的左键会被
    // performWindowDrag 吞掉、传不到页面。判定是纯几何的（y > frame.height - 28），
    // 不查 DOM——所以放在这里的任何按钮都是「看得见点不动」。
    // 要加交互，得先改 desktop/main.go 的 InvisibleTitleBarHeight 并接受窗口不能拖
    render(<DesktopTitleBar base={base} />)
    const bar = screen.getByTestId('desktop-title-bar')
    expect(bar.querySelectorAll('button, a, input, select, textarea')).toHaveLength(0)
  })

  it('面包屑那一行同样不能有可点元素——它是标题栏的同一份内容', () => {
    // 分屏按钮已经挪到 tab 条右端；谁再往面包屑里加按钮，薄壳里就会多一个死控件
    const { container } = render(<Breadcrumb base={base} />)
    expect(container.querySelectorAll('button, a, input')).toHaveLength(0)
  })
})
