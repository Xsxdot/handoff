import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { BlankTab, launchersFor, pickItemsFor, type LauncherItem } from './BlankTab'
import { TabBar } from './TabBar'
import type { BaseDir, TabGroup } from './tabs'

const base: BaseDir = { key: '/repo', kind: 'workspace', path: '/repo', label: 'main', projectName: 'handoff', machine: '' }
const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }
const group: TabGroup = { id: 'g1', name: '组 1', autoName: true, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] }
const launchers: LauncherItem[] = [{ name: '跑测试', envMissing: false }, { name: '发版', envMissing: true }]

describe('启动项入口', () => {
  it('内置项与启动项过滤保持一致', () => {
    expect(pickItemsFor(base).map((item) => item.label)).toEqual(['新终端', '新建文件', '打开任务'])
    expect(launchersFor(launchers).map((item) => item.name)).toEqual(['跑测试', '发版'])
    expect(pickItemsFor(home).map((item) => item.kind)).toEqual(['terminal'])
  })

  it('BlankTab 展示启动项及缺失 env 文案', () => {
    const onPick = vi.fn()
    render(<BlankTab base={base} onPick={onPick} launchers={launchers} onPickLauncher={onPick} />)
    expect(screen.getByText('env 文件缺失')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /跑测试/ }))
    expect(onPick).toHaveBeenCalledWith('跑测试')
  })

  it('+ 菜单与 BlankTab 共用启动项过滤', () => {
    render(<TabBar
      groups={[group]} activeGroupId="g1" base={base}
      onActivateTab={vi.fn()} onCloseTab={vi.fn()} onNew={vi.fn()}
      onNewLauncher={vi.fn()} launchers={launchers}
    />)
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    const names = within(screen.getByRole('menu')).getAllByRole('menuitem').map((item) => item.textContent?.trim())
    expect(names.some((name) => name?.includes('跑测试'))).toBe(true)
    expect(names.some((name) => name?.includes('发版'))).toBe(true)
  })

})
