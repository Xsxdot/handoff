// launchers.test.tsx —— 工作台启动项展示、过滤与入口一致性测试。
import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { BlankTab, launchersFor, pickItemsFor, type LauncherItem } from './BlankTab'
import { TabBar } from './TabBar'
import type { BaseDir } from './useWorkbench'

const base: BaseDir = {
  key: '/repo', kind: 'workspace', path: '/repo', label: 'main', projectName: 'handoff', machine: '',
}

const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }
const launchers: LauncherItem[] = [
  { name: '跑测试', envMissing: false },
  { name: '发版', envMissing: true },
]

describe('工作台启动项纯函数', () => {
  it('内置三项在上，启动项在下', () => {
    expect(pickItemsFor(base).map((item) => item.label)).toEqual(['新终端', '新建文件', '打开任务'])
    expect(launchersFor(launchers).map((item) => item.name)).toEqual(['跑测试', '发版'])
  })

  it('终端不可用时内置终端与启动项都摘掉，home 仍保留启动项', () => {
    expect(pickItemsFor(base, '无 PTY').map((item) => item.kind)).toEqual(['newfile', 'tui'])
    expect(launchersFor(launchers, '无 PTY')).toEqual([])
    expect(pickItemsFor(home).map((item) => item.kind)).toEqual(['terminal'])
    expect(launchersFor(launchers)).toEqual(launchers)
  })
})

describe('BlankTab / + 菜单的启动项入口', () => {
  it('启动项可见、缺失标注可见且不分配快捷键', () => {
    const onPickLauncher = vi.fn()
    render(<BlankTab base={base} onPick={vi.fn()} launchers={launchers} onPickLauncher={onPickLauncher} />)
    expect(screen.getByRole('button', { name: /跑测试/ })).toBeInTheDocument()
    expect(screen.getByText(/env 文件缺失/)).toBeInTheDocument()
    expect(screen.queryByText('⌘⇧L')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /跑测试/ }))
    expect(onPickLauncher).toHaveBeenCalledWith('跑测试')
  })

  it('+ 菜单与空白面板共用同一份启动项过滤结果', () => {
    const onNewLauncher = vi.fn()
    const panel = render(
      <BlankTab base={base} onPick={vi.fn()} launchers={launchers} onPickLauncher={vi.fn()} />,
    )
    const panelNames = within(screen.getByRole('list', { name: '自定义启动项' }))
      .getAllByRole('button')
      .map((button) => button.textContent?.replace('env 文件缺失', '').trim())
    panel.unmount()
    render(
      <TabBar
        group={0}
        tabs={[]}
        activeId={null}
        base={base}
        onActivate={vi.fn()}
        onClose={vi.fn()}
        onNew={vi.fn()}
        launchers={launchers}
        onNewLauncher={onNewLauncher}
        onSplit={vi.fn()}
        canSplit
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))
    const menuNames = screen.getAllByRole('menuitem').map((item) => item.textContent?.trim())
    expect(menuNames.slice(-launchers.length)).toEqual(panelNames)
    expect(screen.getByRole('menuitem', { name: '跑测试' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('menuitem', { name: '跑测试' }))
    expect(onNewLauncher).toHaveBeenCalledWith(0, '跑测试')
  })
})
