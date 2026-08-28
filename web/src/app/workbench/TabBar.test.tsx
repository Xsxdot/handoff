// TabBar.test.tsx —— 顶部 chrome 标签栏的测试。
//
// setup 照抄 WorkbenchPage.test.tsx：renderHook(useWorkbench) 构造布局，
// 再把 api 的 groups/activeGroupId 交给 TabBar 渲染。
import { act, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { TabBar } from './TabBar'
import { useWorkbench, type BaseDir } from './useWorkbench'

const local: BaseDir = { key: '/local', kind: 'workspace', path: '/local', label: 'local', projectName: 'handoff', machine: '' }

type Api = ReturnType<typeof useWorkbench>

function renderBar(api: Api, over: { taskName?: (id: string) => string | undefined } = {}) {
  return render(
    <TabBar
      groups={api.wb.groups}
      activeGroupId={api.wb.activeGroupId}
      base={api.base}
      onActivateTab={vi.fn()}
      onCloseTab={vi.fn()}
      onNew={vi.fn()}
      {...over}
    />,
  )
}

describe('TabBar', () => {
  it('tui tab 显示 resolver 解析的任务原名，无 resolver 时回退 TUI · 前缀', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'f22ed520abc' }, local))

    const withResolver = renderBar(hook.result.current, { taskName: () => '审 B264' })
    expect(withResolver.getByText('审 B264')).toBeInTheDocument()
    withResolver.unmount()

    const withoutResolver = renderBar(hook.result.current)
    expect(withoutResolver.getByText('TUI · f22ed520')).toBeInTheDocument()
  })

  it('激活 tui tab 渲染 dispatch-task 资产图标，terminal/file 渲染对应 lucide 图标', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'T1' }, local))
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    act(() => hook.result.current.open({ kind: 'file', rel: 'web/README.md' }, local))

    const view = renderBar(hook.result.current)
    expect(view.container.querySelector('img[src*="dispatch-task"]')).not.toBeNull()
    expect(view.container.querySelector('.lucide-terminal')).not.toBeNull()
    expect(view.container.querySelector('.lucide-file-text')).not.toBeNull()
  })

  it('激活 tab 有药丸面 tab-surface，非激活 tab 没有', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'T1' }, local))
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))

    const view = renderBar(hook.result.current)
    const surfaces = view.getAllByTestId('tab-surface')
    expect(surfaces).toHaveLength(1)
    // openTab 让最后打开的 tab 处于激活态，药丸面跟着它
    expect(surfaces[0].textContent).toContain('bash · local')
  })

  it('3 个 tab 时 tab 间短分隔线为 2 条，行尾 + 钮前没有', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'T1' }, local))
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    act(() => hook.result.current.open({ kind: 'file', rel: 'a.md' }, local))

    const view = renderBar(hook.result.current)
    expect(view.getAllByTestId('tab-sep')).toHaveLength(2)
  })

  it('关闭钮 aria-label 带 tab 标题，点击回调带 groupId 与 tabId', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'f22ed520abc' }, local))
    const onCloseTab = vi.fn()
    render(
      <TabBar
        groups={hook.result.current.wb.groups}
        activeGroupId={hook.result.current.wb.activeGroupId}
        base={hook.result.current.base}
        onActivateTab={vi.fn()}
        onCloseTab={onCloseTab}
        onNew={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '关闭 TUI · f22ed520' }))
    expect(onCloseTab).toHaveBeenCalledWith('g1', 't1')
  })

  it('点击 tab 触发激活回调；行尾 + 钮承接新建内容菜单', () => {
    const hook = renderHook(() => useWorkbench())
    act(() => hook.result.current.open({ kind: 'tui', taskId: 'T1' }, local))
    act(() => hook.result.current.open({ kind: 'terminal', seq: 1 }, local))
    const onActivateTab = vi.fn()
    render(
      <TabBar
        groups={hook.result.current.wb.groups}
        activeGroupId={hook.result.current.wb.activeGroupId}
        base={hook.result.current.base}
        onActivateTab={onActivateTab}
        onCloseTab={vi.fn()}
        onNew={vi.fn()}
      />,
    )
    const tabs = screen.getAllByRole('tab')
    fireEvent.click(tabs[1])
    expect(onActivateTab).toHaveBeenCalledWith('g1', 't2')
    expect(screen.getByRole('button', { name: '新建内容' })).toBeInTheDocument()
  })
})
