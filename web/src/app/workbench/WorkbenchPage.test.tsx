import { describe, expect, it, vi } from 'vitest'
import { act, createEvent, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import { WorkbenchPage as ActualWorkbenchPage, type WorkbenchPageProps } from './WorkbenchPage'
import { BlankTab } from './BlankTab'
import { createUntitledFile } from './newFile'
import type { BaseDir, WorkbenchApi } from './useWorkbench'
import type { ProjectTreeResp, Task } from '../../api/types'
import { useWorkbench } from './useWorkbench'
import { EMPTY_WORKBENCH, openTab, splitGroup } from './tabs'

vi.mock('./newFile', () => ({ createUntitledFile: vi.fn() }))

type TestWorkbenchPageProps = Omit<WorkbenchPageProps, 'tree' | 'tasks'> &
  Partial<Pick<WorkbenchPageProps, 'tree' | 'tasks'>>

function WorkbenchPage(props: TestWorkbenchPageProps) {
  return <ActualWorkbenchPage {...props} tree={props.tree ?? null} tasks={props.tasks ?? []} />
}

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'b2-b3',
  projectName: 'handoff',
  machine: '',
}

const pickerTree: ProjectTreeResp = {
  projects: [
    {
      project_id: 'project-1',
      origin_url: '',
      name: 'handoff',
      locations: [
        {
          machine: '',
          name: 'handoff',
          path: '/r',
          workspaces: [
            { path: base.path, branch: 'b2-b3', head: 'abc', is_main: false, managed: false, created_at: '' },
          ],
          probe_error: '',
        },
      ],
    },
  ],
  unowned: [],
}

function pickerTask(id: string, name: string, projectId = 'project-1'): Task {
  return {
    id,
    target: '',
    repo_path: '/r',
    branch: 'b2-b3',
    plan_path: '',
    plan_summary: '',
    executor_session: '',
    state: 'running',
    created_at: '2026-08-18T00:00:00Z',
    updated_at: '2026-08-18T00:00:00Z',
    name,
    executor: '',
    model: '',
    work_dir: base.path,
    worktree_managed: false,
    base_commit: '',
    base_ahead: 0,
    repo_dirty_count: 0,
    repo_dirty_files: '',
    done_note: '',
    machine: '',
    project_id: projectId,
  }
}

// dt 造一个够用的 DataTransfer 替身。
// jsdom 里 fireEvent.drop 的 dataTransfer 是我们自己塞进去的普通对象，
// 只要有 types / getData 两样，被测代码就跑得动。
function dt(taskId: string, from: BaseDir | null) {
  const data: Record<string, string> = {
    'text/handoff-task': taskId,
    'text/handoff-base': JSON.stringify(from),
  }
  return { types: Object.keys(data), getData: (k: string) => data[k] ?? '', dropEffect: '' }
}

function dropAt(el: Element, clientX: number, dataTransfer: ReturnType<typeof dt>) {
  // jsdom 的 fireEvent.drop 不接受 clientX 初始化值；先造事件再写入属性，
  // 让 React 的 DragEvent 读到真实偏移量。
  const event = createEvent.drop(el, { dataTransfer })
  Object.defineProperty(event, 'clientX', { value: clientX })
  fireEvent(el, event)
}

// layout 给一个元素钉死 getBoundingClientRect。
// jsdom 里所有元素的宽高都是 0，不钉的话 dropZoneAt 恒返回 center，
// 三个投放区的用例会全部「通过」却什么也没测到。
function layout(el: Element, width: number) {
  el.getBoundingClientRect = () =>
    ({ left: 0, top: 0, right: width, bottom: 400, width, height: 400, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
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
    splitAt: vi.fn(),
    openInNewPane: vi.fn(),
    closeById: vi.fn(),
    resize: vi.fn(),
    restoreTerminal: vi.fn(),
    byBase: {},
    baseDirs: {},
    hydrate: vi.fn(),
    ...overrides,
  }
}

describe('BlankTab', () => {
  it('列出三项且只有三项：新终端 / 新建文件 / 打开任务', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /新建文件/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /打开任务/ })).toBeInTheDocument()
    expect(screen.getAllByRole('button')).toHaveLength(3)
  })

  it('带快捷键提示', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByText('⌘T')).toBeInTheDocument()
    expect(screen.getByText('⌘N')).toBeInTheDocument()
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

  it('印在面板上的快捷键是真能按的（⌘T / ⌘N / ⌘⇧A）', () => {
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={base} onPick={onPick} />)
    const panel = container.firstElementChild as HTMLElement
    fireEvent.keyDown(panel, { key: 't', metaKey: true })
    fireEvent.keyDown(panel, { key: 'n', metaKey: true })
    fireEvent.keyDown(panel, { key: 'a', metaKey: true, shiftKey: true })
    expect(onPick.mock.calls.map((c) => c[0])).toEqual(['terminal', 'newfile', 'tui'])
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

  it('home 基准下 ⌘N 不生效——隐藏项不能被快捷键绕过', () => {
    const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={home} onPick={onPick} />)
    fireEvent.keyDown(container.firstElementChild as HTMLElement, { key: 'n', metaKey: true })
    expect(onPick).not.toHaveBeenCalled()
  })

  it('没按 meta 的普通输入不被吞掉', () => {
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={base} onPick={onPick} />)
    fireEvent.keyDown(container.firstElementChild as HTMLElement, { key: 't' })
    expect(onPick).not.toHaveBeenCalled()
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

  it('空态用品牌标志，不用字母 h 占位', () => {
    render(
      <WorkbenchPage
        api={api({ base: null })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    const mark = screen.getByRole('img', { name: 'handoff' })
    expect(mark).toHaveAttribute('src', '/handoff-mark.svg')
    expect(screen.queryByText('h')).toBeNull()
  })

  it('选中目录但没有 tab 时，中央仍然给出可用起点（种类选择）', () => {
    render(<WorkbenchPage api={api()} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
  })

  it('点 + 弹菜单列出三种去处，不再先落一个空白 tab', () => {
    const open = vi.fn()
    render(<WorkbenchPage api={api({ open })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))
    expect(open).not.toHaveBeenCalled()
    expect(screen.getByRole('menuitem', { name: /新终端/ })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /新建文件/ })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /打开任务/ })).toBeInTheDocument()
  })

  it('+ 菜单选「新终端」，终端开在这条 tab 条自己的组里', () => {
    const openTerminal = vi.fn()
    render(
      <WorkbenchPage api={api({ openTerminal })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    expect(openTerminal).toHaveBeenCalledWith(base, 0)
  })

  it('+ 菜单选「打开任务」先弹选择器，选中后才开 tab（取消不留空壳）', () => {
    const open = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    render(
      <WorkbenchPage
        api={api({ wb, open })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
        tree={pickerTree}
        tasks={[pickerTask('T1', '任务一')]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /打开任务/ }))
    // 选中之前不开 tab：用户按 Esc 取消后不该留下一个空壳
    expect(open).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /任务一/ }))
    expect(open).toHaveBeenCalledWith({ kind: 'tui', taskId: 'T1' }, undefined, 0)
  })

  it('机器开不了终端时，+ 菜单里没有终端项（不置灰）', () => {
    render(
      <WorkbenchPage
        api={api()}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
        terminalUnavailable="这台机器没有 PTY 能力"
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))
    expect(screen.queryByRole('menuitem', { name: /新终端/ })).toBeNull()
    expect(screen.getByRole('menuitem', { name: /新建文件/ })).toBeInTheDocument()
  })

  // 走查回归：分屏后焦点在右组，点**左**组的 + 必须开在左组。
  // 原实现的 onNew 丢掉了 TabBar 传来的组号，openTab 退回 wb.active，于是
  // 「点哪个 + 都开在焦点组」。
  it('分屏时点非焦点组的 +，新 tab 开在被点的那一组', async () => {
    const open = vi.fn()
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = { ...wb, groups: [...wb.groups, { tabs: [], activeId: null }], active: 1 }
    render(
      <WorkbenchPage api={api({ wb, open })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />,
    )
    const plus = screen.getAllByRole('button', { name: '新建标签页' })
    expect(plus).toHaveLength(2)
    vi.mocked(createUntitledFile).mockResolvedValueOnce('untitled-1.md')
    fireEvent.click(plus[0])
    fireEvent.click(screen.getByRole('menuitem', { name: /新建文件/ }))
    await waitFor(() =>
      expect(open).toHaveBeenCalledWith({ kind: 'file', rel: 'untitled-1.md' }, undefined, 0),
    )
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

  it('空白 tab 点「打开任务」弹出选择器，选中后原地变成 TUI tab', () => {
    const setContent = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb, setContent })}
        tree={pickerTree}
        tasks={[pickerTask('T1', '任务一')]}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /打开任务/ }))
    expect(screen.getByRole('dialog', { name: '选择要打开的任务' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /任务一/ }))
    expect(setContent).toHaveBeenCalledWith(0, id, { kind: 'tui', taskId: 'T1' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('点「新建文件」建出 untitled-1.md 并原地变成 file tab', async () => {
    vi.mocked(createUntitledFile).mockResolvedValueOnce('untitled-1.md')
    const setContent = vi.fn()
    const onFileCreated = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb, setContent })}
        onAddProject={vi.fn()}
        onFileCreated={onFileCreated}
        renderContent={() => <div>内容</div>}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /新建文件/ }))
    await waitFor(() => {
      expect(setContent).toHaveBeenCalledWith(0, id, { kind: 'file', rel: 'untitled-1.md' })
    })
    expect(onFileCreated).toHaveBeenCalledOnce()
  })

  it('建文件失败时把 agentd 的原文显示出来，不吞成「操作失败」', async () => {
    vi.mocked(createUntitledFile).mockRejectedValueOnce(new Error('草稿区不可写：磁盘已满'))
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    render(<WorkbenchPage api={api({ wb })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)

    fireEvent.click(screen.getByRole('button', { name: /新建文件/ }))
    expect(await screen.findByText('新建文件失败：草稿区不可写：磁盘已满')).toBeInTheDocument()
    expect(screen.queryByText('操作失败')).not.toBeInTheDocument()
  })

  it('选中的任务已在别的 tab 里开着时，激活那个并关掉这个空白 tab', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(base))
    act(() => result.current.open({ kind: 'tui', taskId: 'T1' }))
    act(() => result.current.open({ kind: 'blank' }))
    const existingId = result.current.wb.groups[0].tabs[0].id
    const blankId = result.current.wb.groups[0].tabs[1].id

    render(
      <WorkbenchPage
        api={result.current}
        tree={pickerTree}
        tasks={[pickerTask('T1', '任务一')]}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /打开任务/ }))
    act(() => fireEvent.click(screen.getByRole('button', { name: /任务一/ })))

    expect(result.current.wb.groups[0].tabs.map((t) => t.id)).toEqual([existingId])
    expect(result.current.wb.groups[0].tabs.map((t) => t.id)).not.toContain(blankId)
    expect(result.current.wb.groups[0].activeId).toBe(existingId)
  })

  it('空组面板点「打开任务」先开一个空白 tab 承接，再弹选择器', () => {
    const open = vi.fn()
    const { rerender } = render(
      <WorkbenchPage
        api={api({ open })}
        tree={pickerTree}
        tasks={[pickerTask('T1', '任务一')]}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /打开任务/ }))
    expect(open).toHaveBeenCalledWith({ kind: 'blank' }, undefined, 0)

    const wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    rerender(
      <WorkbenchPage
        api={api({ wb })}
        tree={pickerTree}
        tasks={[pickerTask('T1', '任务一')]}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /打开任务/ }))
    expect(screen.getByRole('dialog', { name: '选择要打开的任务' })).toBeInTheDocument()
  })

  it('终端不可用时选择面板不列终端项，改说一句实话', () => {
    render(
      <WorkbenchPage
        api={api()}
        onAddProject={vi.fn()}
        terminalUnavailable="这台机器的 agentd 运行在不支持 PTY 的平台上"
        renderContent={() => <div>内容</div>}
      />,
    )
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
    expect(screen.getByText(/不支持 PTY/)).toBeInTheDocument()
  })

  it('onBeforeClose 返回 false 时 tab 不关——上层要先删服务端会话', () => {
    const close = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1, sessionId: 's1' })
    render(
      <WorkbenchPage
        api={api({ wb, close })}
        onAddProject={vi.fn()}
        onBeforeClose={() => false}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '关闭 bash · b2-b3' }))
    expect(close).not.toHaveBeenCalled()
  })

  it('没挂 onBeforeClose 时照常直接关——拦截是加出来的，不是默认的', () => {
    const close = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1, sessionId: 's1' })
    const id = wb.groups[0].tabs[0].id
    render(<WorkbenchPage api={api({ wb, close })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    fireEvent.click(screen.getByRole('button', { name: '关闭 bash · b2-b3' }))
    expect(close).toHaveBeenCalledWith(0, id)
  })
})

describe('分屏分隔条', () => {
  const twoGroups = () => splitGroup(openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' }))

  it('单组时没有分隔条', () => {
    render(<WorkbenchPage api={api()} onAddProject={vi.fn()} renderContent={() => null} />)
    expect(screen.queryByRole('separator')).not.toBeInTheDocument()
  })

  it('两组之间有一条分隔条，三组有两条', () => {
    const { unmount } = render(
      <WorkbenchPage api={api({ wb: twoGroups() })} onAddProject={vi.fn()} renderContent={() => null} />,
    )
    expect(screen.getAllByRole('separator')).toHaveLength(1)
    unmount()

    render(
      <WorkbenchPage api={api({ wb: splitGroup(twoGroups()) })} onAddProject={vi.fn()} renderContent={() => null} />,
    )
    expect(screen.getAllByRole('separator')).toHaveLength(2)
  })

  it('分隔条按 → 键调宽左栏，按 ← 键调窄', () => {
    const resize = vi.fn()
    render(
      <WorkbenchPage api={api({ wb: twoGroups(), resize })} onAddProject={vi.fn()} renderContent={() => null} />,
    )
    const sep = screen.getByRole('separator')

    fireEvent.keyDown(sep, { key: 'ArrowRight' })
    // jsdom 的 getBoundingClientRect 恒为 0，量不到容器宽度时 minRatio 传 0
    expect(resize).toHaveBeenLastCalledWith(0, 0.02, 0)

    fireEvent.keyDown(sep, { key: 'ArrowLeft' })
    expect(resize).toHaveBeenLastCalledWith(0, -0.02, 0)
  })

  it('各栏按 sizes 的权重铺开', () => {
    const wb = { ...twoGroups(), sizes: [3, 1] }
    render(<WorkbenchPage api={api({ wb })} onAddProject={vi.fn()} renderContent={() => null} />)
    const panes = screen.getAllByRole('tablist').map((tl) => tl.closest('section') as HTMLElement)
    expect(panes[0]).toHaveStyle({ flexGrow: '3' })
    expect(panes[1]).toHaveStyle({ flexGrow: '1' })
  })
})

describe('拖放投放区', () => {
  // 这些用例共用一个已选中基准、单栏、栏宽 400px 的工作台。
  // 400px 宽下边缘区是 min(100, 120) = 100px。
  const setup = () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.select(base))
    const view = render(
      <WorkbenchPage
        api={hook.result.current}
        tree={pickerTree}
        tasks={[]}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    return {
      hook,
      section: view.container.querySelector('section') as HTMLElement,
      rerender: () =>
        view.rerender(
          <WorkbenchPage
            api={hook.result.current}
            tree={pickerTree}
            tasks={[]}
            onAddProject={vi.fn()}
            renderContent={() => <div>内容</div>}
          />,
        ),
    }
  }

  it('拖到栏中间：在那一栏开 tab，不分屏', () => {
    const { hook, section } = setup()
    layout(section, 400)
    dropAt(section, 200, dt('T1', null))
    expect(hook.result.current.wb.groups).toHaveLength(1)
    expect(hook.result.current.wb.groups[0].tabs.at(-1)?.content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('拖到右边缘：在右边分出新栏并在其中打开', () => {
    const { hook, section } = setup()
    layout(section, 400)
    dropAt(section, 390, dt('T1', null))
    expect(hook.result.current.wb.groups).toHaveLength(2)
    expect(hook.result.current.wb.groups[1].tabs).toHaveLength(1)
    expect(hook.result.current.wb.groups[1].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('拖到左边缘：新栏插在左边，原来那栏被推到右边', () => {
    const { hook, section } = setup()
    layout(section, 400)
    dropAt(section, 10, dt('T1', null))
    expect(hook.result.current.wb.groups).toHaveLength(2)
    expect(hook.result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('拖一个已经开着的任务到边缘：不分屏，激活已有的那个——不能留下空栏', () => {
    // 走查实测的缺陷：先 splitAt 再 open，openTab 的跨组去重会把已有 tab 在
    // 原栏激活，刚分出来的新栏空在那儿。用户要的是「分屏并打开」，
    // 拿到一个空栏比不分屏更糟。
    const { hook, section, rerender } = setup()
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'T1' }))
    rerender()
    layout(section, 400)
    dropAt(section, 390, dt('T1', null))
    expect(hook.result.current.wb.groups).toHaveLength(1)
    expect(hook.result.current.wb.groups.flatMap((g) => g.tabs)).toHaveLength(1)
  })

  it('已到三栏时拖边缘退化成在这栏开 tab，不是无效投放', () => {
    const { hook, section, rerender } = setup()
    act(() => {
      hook.result.current.split()
      hook.result.current.split()
    })
    rerender()
    layout(section, 400)
    dropAt(section, 390, dt('T1', null))
    expect(hook.result.current.wb.groups).toHaveLength(3)
    // 落在了被拖到的那一栏，而不是什么都没发生。
    expect(hook.result.current.wb.groups.flatMap((g) => g.tabs)).toHaveLength(1)
  })

  it('没有 handoff MIME 的拖放被忽略——从别处拖进来的东西不该开出 tab', () => {
    const { hook, section } = setup()
    layout(section, 400)
    const foreign = { types: ['text/plain'], getData: () => 'https://example.com', dropEffect: '' }
    const event = createEvent.drop(section, { dataTransfer: foreign })
    Object.defineProperty(event, 'clientX', { value: 200 })
    fireEvent(section, event)
    expect(hook.result.current.wb.groups.flatMap((g) => g.tabs)).toHaveLength(0)
  })
})
