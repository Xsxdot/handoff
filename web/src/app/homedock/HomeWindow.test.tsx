import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { HomeWindow } from './HomeWindow'

const tabs = [
  { id: 'a', kind: 'terminal' as const, seq: 1, machine: '' },
  { id: 'b', kind: 'terminal' as const, seq: 2, machine: '' },
]
const geom = { x: 100, y: 100, w: 600, h: 300 }
const base = () => ({
  tabs, activeId: 'a', geom,
  onGeom: vi.fn(), onActivate: vi.fn(), onNew: vi.fn(), onNewFile: vi.fn(),
  onKill: vi.fn(), onCollapse: vi.fn(),
  renderTab: (t: { id: string }) => <div data-testid={`content-${t.id}`} />,
})

describe('HomeWindow', () => {
  it('只渲染激活 tab 的内容', () => {
    render(<HomeWindow {...base()} />)
    expect(screen.getByTestId('content-a')).toBeInTheDocument()
    expect(screen.queryByTestId('content-b')).toBeNull()
  })

  // 本窗是浅色控制台里的一块深色表面，放进来的 tab 用主题令牌上色。
  // 两个类缺一不可：dark 换令牌档，text-foreground 才真的把颜色写下来供继承——
  // 只有 dark 时 textarea 会一路继承到 body 上那个浅色主题的近黑色，
  // 落在深色底上等于隐形（走查实测：新建文件后正文看不见）。
  //
  // **这条只钉住两个类还在，钉不住「渲染出来对比度够」**：jsdom 不编译 Tailwind，
  // 算不出真实颜色。真实颜色是在浏览器里量的（textarea 的 color 从 oklch(0.196)
  // 变成 oklch(0.804)），那次验证记在提交信息里。
  it('窗口根节点带 dark 与 text-foreground，主题令牌才落得到子 tab 上', () => {
    const { container } = render(<HomeWindow {...base()} />)
    const root = container.querySelector('section')
    expect(root).not.toBeNull()
    expect(root!.className).toContain('dark')
    expect(root!.className).toContain('text-foreground')
  })

  it('收起走 onCollapse，不走 onKill', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.click(screen.getByLabelText('收起（会话保留）'))
    expect(p.onCollapse).toHaveBeenCalledTimes(1)
    expect(p.onKill).not.toHaveBeenCalled()
  })

  it('tab 上的 × 走 onKill，且不误触发激活', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.click(screen.getByLabelText('关闭 bash · home 2'))
    expect(p.onKill).toHaveBeenCalledWith('b')
    expect(p.onActivate).not.toHaveBeenCalled()
  })

  it('拖标题栏改位置', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    const title = screen.getByTestId('home-window-title')
    fireEvent.pointerDown(title, { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 40, clientY: 25 })
    fireEvent.pointerUp(document)
    expect(p.onGeom).toHaveBeenCalledWith(expect.objectContaining({ x: 140, y: 125 }))
  })

  it('拉右下角改尺寸', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.pointerDown(screen.getByTestId('home-window-corner'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 30, clientY: 20 })
    fireEvent.pointerUp(document)
    expect(p.onGeom).toHaveBeenCalledWith(expect.objectContaining({ w: 630, h: 320 }))
  })

  it('菜单里选「新终端」时不给 onNew 传任何实参——传了就会变成 machine', () => {
    // why：onSelect={onNew} 会把参数直接喂进
    // useHomeDock.newTerminal(machine?: string)，HomeTab.machine 存成非字符串，
    // 关会话时拼出 ?machine=[object Object] 当场炸。
    // TS 拦不住：(machine?: string) => void 对 () => void 是合法赋值
    const onNew = vi.fn()
    render(
      <HomeWindow
        tabs={[{ id: 'a', kind: 'terminal', seq: 1, machine: '' }]}
        activeId="a"
        geom={{ x: 0, y: 0, w: 600, h: 300 }}
        onGeom={vi.fn()}
        onActivate={vi.fn()}
        onNew={onNew}
        onKill={vi.fn()}
        onCollapse={vi.fn()}
        renderTab={() => <div />}
      />,
    )
    fireEvent.click(screen.getByLabelText('新建'))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    expect(onNew).toHaveBeenCalledTimes(1)
    expect(onNew.mock.calls[0]).toHaveLength(0)
  })

  it('tab 条上只有一个「新建」图标，两种去处在菜单里', () => {
    // why：两个相邻的小图标（+ 与文件）分不清哪个是哪个，合成一个菜单后
    // 「新建什么」由文字说清楚
    const p = base()
    render(<HomeWindow {...p} />)
    expect(screen.queryByLabelText('新建临时文件')).toBeNull()
    fireEvent.click(screen.getByLabelText('新建'))
    expect(screen.getByRole('menuitem', { name: /新终端/ })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('menuitem', { name: /新建临时文件/ }))
    expect(p.onNewFile).toHaveBeenCalledTimes(1)
    expect(p.onNewFile.mock.calls[0]).toHaveLength(0)
  })

  it('onNewFile 缺省时菜单里没有临时文件项，不置灰', () => {
    render(
      <HomeWindow
        {...base()}
        onNewFile={undefined}
      />,
    )
    fireEvent.click(screen.getByLabelText('新建'))
    expect(screen.getByRole('menuitem', { name: /新终端/ })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /新建临时文件/ })).toBeNull()
  })

  it('最大化后铺满视口、收起拉伸角，再点还原回到原来的 geom', () => {
    // why：铺满时若还留着拉伸角，拉一下就会造出「既不是全屏也不是 geom」的中间态
    const p = { ...base(), maximized: false, onToggleMaximize: vi.fn() }
    const { rerender, container } = render(<HomeWindow {...p} />)
    fireEvent.click(screen.getByLabelText('最大化'))
    expect(p.onToggleMaximize).toHaveBeenCalledTimes(1)

    rerender(<HomeWindow {...p} maximized />)
    const win = container.querySelector('section') as HTMLElement
    expect(win.style.left).toBe('8px')
    expect(win.style.right).toBe('8px')
    expect(win.style.width).toBe('auto')
    expect(screen.queryByTestId('home-window-corner')).toBeNull()
    // 铺满时拖标题栏不该改几何
    fireEvent.pointerDown(screen.getByTestId('home-window-title'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 40, clientY: 25 })
    fireEvent.pointerUp(document)
    expect(p.onGeom).not.toHaveBeenCalled()

    rerender(<HomeWindow {...p} maximized={false} />)
    expect((container.querySelector('section') as HTMLElement).style.width).toBe('600px')
  })

  it('file 种类的 tab 标题显示文件名而不是 bash · home N', () => {
    render(
      <HomeWindow
        {...base()}
        tabs={[{ id: 'f', kind: 'file', rel: 'untitled-1.md', seq: 1, machine: '' }]}
        activeId="f"
        renderTab={() => <div />}
      />,
    )
    expect(screen.getByText('untitled-1.md', { selector: 'button' })).toBeInTheDocument()
    expect(screen.queryByText(/bash · home/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /关闭 untitled-1\.md/ })).toHaveAttribute(
      'title',
      '关闭（文件保留在草稿区）',
    )
  })
})
