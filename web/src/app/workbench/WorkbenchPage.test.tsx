import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { WorkbenchPage } from './WorkbenchPage'
import { BlankTab } from './BlankTab'
import type { BaseDir, WorkbenchApi } from './useWorkbench'
import { EMPTY_WORKBENCH, openTab } from './tabs'

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'b2-b3',
  projectName: 'handoff',
  machine: '',
}

function api(overrides: Partial<WorkbenchApi> = {}): WorkbenchApi {
  return {
    base,
    wb: EMPTY_WORKBENCH,
    select: vi.fn(),
    open: vi.fn(),
    openTerminal: vi.fn(),
    close: vi.fn(),
    activate: vi.fn(),
    setContent: vi.fn(),
    split: vi.fn(),
    restoreTerminal: vi.fn(),
    ...overrides,
  }
}

describe('BlankTab', () => {
  it('列出三项且只有三项：新终端 / 打开文件 / 打开任务 TUI', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /打开文件/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /打开任务 TUI/ })).toBeInTheDocument()
    expect(screen.getAllByRole('button')).toHaveLength(3)
  })

  it('带快捷键提示', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByText('⌘T')).toBeInTheDocument()
    expect(screen.getByText('⌘⇧O')).toBeInTheDocument()
    expect(screen.getByText('⌘⇧A')).toBeInTheDocument()
  })

  it('home 基准下只有新终端一项（spec §2.6）', () => {
    const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }
    render(<BlankTab base={home} onPick={vi.fn()} />)
    expect(screen.getAllByRole('button')).toHaveLength(1)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
  })

  it('显示基准目录，让人知道这个 tab 会开在哪', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByText(/b2-b3/)).toBeInTheDocument()
  })

  it('点一项回调对应种类', () => {
    const onPick = vi.fn()
    render(<BlankTab base={base} onPick={onPick} />)
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(onPick).toHaveBeenCalledWith('terminal')
  })

  it('印在面板上的快捷键是真能按的（⌘T / ⌘⇧O / ⌘⇧A）', () => {
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={base} onPick={onPick} />)
    const panel = container.firstElementChild as HTMLElement
    fireEvent.keyDown(panel, { key: 't', metaKey: true })
    fireEvent.keyDown(panel, { key: 'o', metaKey: true, shiftKey: true })
    fireEvent.keyDown(panel, { key: 'a', metaKey: true, shiftKey: true })
    expect(onPick.mock.calls.map((c) => c[0])).toEqual(['terminal', 'file', 'tui'])
  })

  // 走查回归：上面那条用例把按键直接打在面板元素上，绕过了「面板有没有焦点」，
  // 所以它一直是绿的，而真机上面板压根没拿到焦点（autoFocus 对普通 div 不生效），
  // 印上去的 ⌘T 按下去没反应。这两条从 activeElement 出发，堵住这个缺口。
  it('面板挂载即拿到焦点，否则印上去的快捷键是死的', () => {
    const { container } = render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(document.activeElement).toBe(container.firstElementChild)
  })

  it('不预先聚焦、直接对着当前焦点按 ⌘T，也能开出终端', () => {
    const onPick = vi.fn()
    render(<BlankTab base={base} onPick={onPick} />)
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 't', metaKey: true })
    expect(onPick).toHaveBeenCalledWith('terminal')
  })

  it('从「等目标」态点返回选择后，面板重新拿回焦点', () => {
    const { container, rerender } = render(
      <BlankTab base={base} onPick={vi.fn()} hint="在右侧文件树里点一个文件" onBack={vi.fn()} />,
    )
    expect(document.activeElement).not.toBe(container.firstElementChild)
    rerender(<BlankTab base={base} onPick={vi.fn()} />)
    expect(document.activeElement).toBe(container.firstElementChild)
  })

  it('home 基准下 ⌘⇧O 不生效——隐藏项不能被快捷键绕过', () => {
    const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={home} onPick={onPick} />)
    fireEvent.keyDown(container.firstElementChild as HTMLElement, { key: 'o', metaKey: true, shiftKey: true })
    expect(onPick).not.toHaveBeenCalled()
  })

  it('没按 meta 的普通输入不被吞掉', () => {
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={base} onPick={onPick} />)
    fireEvent.keyDown(container.firstElementChild as HTMLElement, { key: 't' })
    expect(onPick).not.toHaveBeenCalled()
  })

  it('hint 非空时换成指路 + 返回选择，不再列三项', () => {
    render(<BlankTab base={base} onPick={vi.fn()} hint="在右侧文件树里点一个文件" onBack={vi.fn()} />)
    expect(screen.getByText('在右侧文件树里点一个文件')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
  })

  it('点返回选择回到三项', () => {
    const onBack = vi.fn()
    render(<BlankTab base={base} onPick={vi.fn()} hint="随便什么提示" onBack={onBack} />)
    fireEvent.click(screen.getByRole('button', { name: '返回选择' }))
    expect(onBack).toHaveBeenCalled()
  })
})

describe('WorkbenchPage', () => {
  it('未选中目录时显示全局空态而不是死空白', () => {
    render(
      <WorkbenchPage
        api={api({ base: null })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    expect(screen.getByText(/从侧边栏选择一个目录开始/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /添加项目/ })).toBeInTheDocument()
  })

  it('选中目录但没有 tab 时，中央仍然给出可用起点（种类选择）', () => {
    render(<WorkbenchPage api={api()} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
  })

  it('点 + 开一个空白 tab，开在这条 tab 条自己的组里', () => {
    const open = vi.fn()
    render(<WorkbenchPage api={api({ open })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))
    expect(open).toHaveBeenCalledWith({ kind: 'blank' }, undefined, 0)
  })

  // 走查回归：分屏后焦点在右组，点**左**组的 + 必须开在左组。
  // 原实现的 onNew 丢掉了 TabBar 传来的组号，openTab 退回 wb.active，于是
  // 「点哪个 + 都开在焦点组」。
  it('分屏时点非焦点组的 +，新 tab 开在被点的那一组', () => {
    const open = vi.fn()
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = { ...wb, groups: [...wb.groups, { tabs: [], activeId: null }], active: 1 }
    render(
      <WorkbenchPage api={api({ wb, open })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />,
    )
    const plus = screen.getAllByRole('button', { name: '新建标签页' })
    expect(plus).toHaveLength(2)
    fireEvent.click(plus[0])
    expect(open).toHaveBeenCalledWith({ kind: 'blank' }, undefined, 0)
  })

  // 同一个偏差的另一半：空组的种类选择面板也得把组号带上。
  it('空组的空态面板选「新终端」，终端开在这个空组而不是焦点组', () => {
    const openTerminal = vi.fn()
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = { ...wb, groups: [{ tabs: [], activeId: null }, ...wb.groups], active: 1 }
    render(
      <WorkbenchPage
        api={api({ wb, openTerminal })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(openTerminal).toHaveBeenCalledWith(base, 0)
  })

  // 走查回归：空组面板与空白 tab 面板是三元的相邻分支，不给 key 时 React 原地复用，
  // BlankTab 不重挂 → 不重新聚焦 → 点 + 之后印在面板上的 ⌘T 是死的。
  it('从空组点 + 开出空白 tab 后，面板重新拿到焦点（快捷键才是活的）', () => {
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    const { container, rerender } = render(
      <WorkbenchPage api={api()} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />,
    )
    const emptyPanel = document.activeElement
    rerender(<WorkbenchPage api={api({ wb })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    expect(document.activeElement).not.toBe(emptyPanel)
    expect(container.contains(document.activeElement)).toBe(true)
    expect((document.activeElement as HTMLElement).textContent).toContain('新终端')
  })

  it('渲染 tab 条与激活 tab 的内容', () => {
    const wb = openTab(openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a/go.mod' }), {
      kind: 'tui',
      taskId: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad',
    })
    render(
      <WorkbenchPage
        api={api({ wb })}
        onAddProject={vi.fn()}
        renderContent={(c) => <div>渲染:{c.kind}</div>}
      />,
    )
    expect(screen.getByRole('tab', { name: /go.mod/ })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /TUI · 7ec762e7/ })).toBeInTheDocument()
    expect(screen.getByText('渲染:tui')).toBeInTheDocument()
    expect(screen.queryByText('渲染:file')).not.toBeInTheDocument()
  })

  it('点 tab 激活它，点关闭按钮关掉它', () => {
    const activate = vi.fn()
    const close = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a/go.mod' })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb, activate, close })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('tab', { name: /go.mod/ }))
    expect(activate).toHaveBeenCalledWith(0, id)
    fireEvent.click(screen.getByRole('button', { name: /关闭 go.mod/ }))
    expect(close).toHaveBeenCalledWith(0, id)
  })

  it('两组时左右并排，各有自己的 tab 条', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = { ...wb, groups: [...wb.groups, { tabs: [], activeId: null }], active: 1 }
    render(<WorkbenchPage api={api({ wb })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    expect(screen.getAllByRole('tablist')).toHaveLength(2)
  })

  it('renderContent 拿得到自己所在的组号与 tab id', () => {
    const seen: Array<[number, string]> = []
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb })}
        onAddProject={vi.fn()}
        renderContent={(_c, _b, group, tabId) => {
          seen.push([group, tabId])
          return <div>内容</div>
        }}
      />,
    )
    expect(seen[0]).toEqual([0, id])
  })

  it('空白 tab 选了种类后调 setContent 而不是再开一个 tab', () => {
    const setContent = vi.fn()
    const open = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb, setContent, open })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(setContent).toHaveBeenCalledWith(0, id, { kind: 'terminal', seq: 1 })
    expect(open).not.toHaveBeenCalled()
  })
})
