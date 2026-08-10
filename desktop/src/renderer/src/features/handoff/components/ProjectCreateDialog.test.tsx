// @vitest-environment happy-dom
/**
 * Handoff ProjectCreateDialog 测试：local/remote 约束、稳定 operation_id 与失败重试。
 *
 * 职责：
 *   - local/remote 至少一个、remote 单选
 *   - Finder 按钮只在 local existing path 出现；remote existing path 只能粘贴
 *   - clone URL 必填；clone path 自动预填并可改
 *   - 一次提交意图 = 一个稳定 operation_id（重试复用，不换新 id）
 *   - 异步失败显示错误与重试入口，重试复用同一 operation_id
 *
 * 边界：
 *   - 使用 react-testing-library（happy-dom）
 *   - 与仓库其他 Dialog 测试一致：mock shadcn 原语（真实 Radix 的 CSS 动画在
 *     happy-dom 下不结束，pointer-events:none 会阻断 userEvent）
 *   - 直接查询 DOM 按钮元素反映最新状态，不依赖 mock 捕获的渲染时快照
 *   - 不弹真实 Finder（window.handoff 由 fixture 注入）
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type * as ReactModule from 'react'
import { ProjectCreateDialog, type CreateDialogMachine } from './ProjectCreateDialog'

// vitest 未开 globals，@testing-library/react 的自动 cleanup 不生效；
// 必须在每个用例后手动清理 DOM，否则前一个 render 的元素残留，影响后续用例的
// getAllByRole('combobox') 定位。
afterEach(() => {
  cleanup()
})

vi.mock('@/components/ui/dialog', () => ({
  Dialog: ({ open, children }: { open: boolean; children: ReactModule.ReactNode }) =>
    open ? <div>{children}</div> : null,
  DialogContent: ({ children }: { children: ReactModule.ReactNode }) => <div>{children}</div>,
  DialogDescription: ({ children }: { children: ReactModule.ReactNode }) => <p>{children}</p>,
  DialogFooter: ({ children }: { children: ReactModule.ReactNode }) => <footer>{children}</footer>,
  DialogHeader: ({ children }: { children: ReactModule.ReactNode }) => <header>{children}</header>,
  DialogTitle: ({ children }: { children: ReactModule.ReactNode }) => <h1>{children}</h1>
}))

vi.mock('@/components/ui/button', () => ({
  Button: ({
    children,
    onClick,
    disabled
  }: {
    children: ReactModule.ReactNode
    onClick?: () => unknown
    disabled?: boolean
  }) => (
    <button type="button" disabled={disabled} onClick={() => onClick?.()}>
      {children}
    </button>
  )
}))

vi.mock('@/components/ui/input', () => ({
  Input: (props: {
    placeholder?: string
    value?: string
    onChange?: (e: { target: { value: string } }) => void
  }) => (
    <input
      placeholder={props.placeholder}
      value={props.value ?? ''}
      onChange={(e) => props.onChange?.({ target: { value: e.target.value } })}
    />
  )
}))

vi.mock('@/components/ui/label', () => ({
  Label: ({ children }: { children: ReactModule.ReactNode }) => <label>{children}</label>
}))

vi.mock('@/components/ui/checkbox', () => ({
  Checkbox: ({
    checked,
    onCheckedChange,
    'aria-label': ariaLabel
  }: {
    checked?: boolean
    onCheckedChange?: (checked: boolean) => void
    'aria-label'?: string
  }) => (
    <input
      aria-label={ariaLabel}
      type="checkbox"
      checked={checked}
      onChange={(event) => onCheckedChange?.(event.target.checked)}
    />
  )
}))

vi.mock('@/components/ui/select', () => ({
  Select: ({
    value,
    onValueChange,
    children
  }: {
    value: string
    onValueChange: (v: string) => void
    children: ReactModule.ReactNode
  }) => (
    <select value={value} onChange={(e) => onValueChange(e.target.value)}>
      {children}
    </select>
  ),
  SelectTrigger: ({ children }: { children: ReactModule.ReactNode }) => <>{children}</>,
  SelectValue: () => null,
  SelectContent: ({ children }: { children: ReactModule.ReactNode }) => <>{children}</>,
  SelectItem: ({ value, children }: { value: string; children: ReactModule.ReactNode }) => (
    <option value={value}>{children}</option>
  )
}))

const machines: CreateDialogMachine[] = [
  { id: 'm-local', display_name: '本机', kind: 'local' },
  { id: 'm-remote', display_name: '开发机', kind: 'remote' }
]

const operationResponse = {
  operation_id: 'fixed',
  state: 'succeeded',
  kind: 'create_project',
  targets: [],
  created_at: '2026-08-09T00:00:00Z',
  updated_at: '2026-08-09T00:00:00Z'
}

/** 填表到可提交：项目名 + 本机已有目录路径 + 机器选择。
 *
 * local 段有两个 combobox：source 选择器（已有目录/Git clone）在前，机器选择器
 * 在后——机器选择必须改第二个 combobox。
 */
function fillForm(name: string, path: string): void {
  const inputs = screen.getAllByRole('textbox')
  fireEvent.change(inputs[0]!, { target: { value: name } })
  fireEvent.change(inputs[1]!, { target: { value: path } })
  const selects = screen.getAllByRole('combobox')
  fireEvent.change(selects[1]!, { target: { value: 'm-local' } })
}

function submitButton(): HTMLElement {
  const btns = screen.getAllByRole('button')
  const btn = btns.find((b) => b.textContent?.includes('创建项目'))
  if (!btn) {
    throw new Error('提交按钮未找到')
  }
  return btn
}

/** 填表后等待提交按钮变为可用（强制 re-render flush，模拟真实点击时序）。 */
function clickSubmit(): void {
  const btn = submitButton()
  expect((btn as HTMLButtonElement).disabled).toBe(false)
  fireEvent.click(btn)
}

function retryButton(): HTMLElement {
  const btns = screen.getAllByRole('button')
  const btn = btns.find((b) => b.textContent?.includes('重试'))
  if (!btn) {
    throw new Error('重试按钮未找到')
  }
  return btn
}

describe('ProjectCreateDialog', () => {
  it('requires at least one location (submit disabled without path)', () => {
    render(
      <ProjectCreateDialog open machines={machines} onSubmit={vi.fn()} onOpenChange={vi.fn()} />
    )
    expect((submitButton() as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows Finder button for local existing path', () => {
    render(
      <ProjectCreateDialog open machines={machines} onSubmit={vi.fn()} onOpenChange={vi.fn()} />
    )
    const btns = screen.getAllByRole('button')
    expect(btns.some((b) => b.textContent?.includes('选择目录'))).toBe(true)
  })

  it('prefills clone path from the Git URL and preserves an explicit override', () => {
    render(
      <ProjectCreateDialog open machines={machines} onSubmit={vi.fn()} onOpenChange={vi.fn()} />
    )

    fireEvent.change(screen.getAllByRole('combobox')[0]!, { target: { value: 'git_clone' } })
    const inputs = screen.getAllByRole('textbox') as HTMLInputElement[]
    fireEvent.change(inputs[1]!, { target: { value: 'git@github.com:openai/handoff.git' } })
    expect(inputs[2]!.value).toBe('~/.handoff/handoff')

    fireEvent.change(inputs[2]!, { target: { value: '/workspace/custom-handoff' } })
    fireEvent.change(inputs[1]!, { target: { value: 'git@github.com:openai/renamed.git' } })
    expect(inputs[2]!.value).toBe('/workspace/custom-handoff')
  })

  it('supports a remote-only project while keeping Finder local-only', async () => {
    const onSubmit = vi.fn().mockResolvedValue(operationResponse)
    render(
      <ProjectCreateDialog open machines={machines} onSubmit={onSubmit} onOpenChange={vi.fn()} />
    )

    fireEvent.click(screen.getByRole('checkbox', { name: '使用本机目录' }))
    fireEvent.click(screen.getByRole('checkbox', { name: '使用远端目录' }))
    expect(screen.queryByRole('button', { name: /选择目录/ })).toBeNull()

    const textboxes = screen.getAllByRole('textbox')
    fireEvent.change(textboxes[0]!, { target: { value: 'remote-project' } })
    fireEvent.change(textboxes[1]!, { target: { value: '/srv/remote-project' } })
    const selects = screen.getAllByRole('combobox')
    fireEvent.change(selects[1]!, { target: { value: 'm-remote' } })
    clickSubmit()

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit.mock.calls[0]?.[0]).toMatchObject({
      name: 'remote-project',
      locations: [
        {
          machine_id: 'm-remote',
          role: 'remote',
          source: 'existing_path',
          path: '/srv/remote-project'
        }
      ]
    })
  })

  it('submits once and keeps operation id stable across retries', async () => {
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new Error('agentd 暂时不可用'))
      .mockResolvedValueOnce(operationResponse)
    render(
      <ProjectCreateDialog open machines={machines} onSubmit={onSubmit} onOpenChange={vi.fn()} />
    )
    fillForm('super-debug', '/Users/me/handoff')

    // 第一次提交：失败 → 错误与重试入口出现
    clickSubmit()
    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1)
    })
    await screen.findByTestId('handoff-create-error')

    // 重试：成功
    fireEvent.click(retryButton())
    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(2)
    })

    // 两次提交的 operation_id 必须相同（一次提交意图 = 一个稳定 id）
    const ids = onSubmit.mock.calls.map((c) => (c[0] as { operation_id: string }).operation_id)
    expect(ids).toHaveLength(2)
    expect(ids[0]).toBe(ids[1])
    // 成功后显示 Operation 状态
    await screen.findByTestId('handoff-operation-result')
  })

  it('shows actionable error message with retry entry on failure', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('远端 agentd 不可达'))
    render(
      <ProjectCreateDialog open machines={machines} onSubmit={onSubmit} onOpenChange={vi.fn()} />
    )
    fillForm('super-debug', '/Users/me/handoff')
    clickSubmit()
    const errorBox = await screen.findByTestId('handoff-create-error')
    expect(errorBox.textContent).toContain('远端 agentd 不可达')
    const btns = screen.getAllByRole('button')
    expect(btns.some((b) => b.textContent?.includes('重试'))).toBe(true)
  })

  it('uses a new operation id after a fresh dialog open (new submit intent)', async () => {
    const onSubmit = vi.fn().mockResolvedValue(operationResponse)
    const { rerender } = render(
      <ProjectCreateDialog open machines={machines} onSubmit={onSubmit} onOpenChange={vi.fn()} />
    )
    // 第一次提交意图
    fillForm('first', '/a')
    clickSubmit()
    await screen.findByTestId('handoff-operation-result')

    // 关闭再打开对话框 = 新的提交意图
    rerender(
      <ProjectCreateDialog
        open={false}
        machines={machines}
        onSubmit={onSubmit}
        onOpenChange={vi.fn()}
      />
    )
    rerender(
      <ProjectCreateDialog open machines={machines} onSubmit={onSubmit} onOpenChange={vi.fn()} />
    )
    fillForm('second', '/b')
    clickSubmit()

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(2)
    })
    const ids = onSubmit.mock.calls.map((c) => (c[0] as { operation_id: string }).operation_id)
    expect(ids[0]).not.toBe(ids[1])
  })
})
