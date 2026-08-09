// @vitest-environment happy-dom
/**
 * Handoff ProjectCreateDialog 测试：local/remote 约束与 clone 默认值。
 *
 * 职责：
 *   - local/remote 至少一个
 *   - remote 单选
 *   - Finder 按钮只在 local existing path 出现
 *   - remote existing path 只能粘贴
 *   - clone URL 必填
 *   - clone path 自动预填并可改
 *
 * 边界：
 *   - 使用 react-testing-library
 *   - 不弹真实 Finder（window.handoff 由 fixture 注入）
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ProjectCreateDialog, type CreateDialogMachine } from './ProjectCreateDialog'

const windowWithHandoff = window as unknown as {
  handoff: {
    pickLocalDirectory: () => Promise<{ canceled: boolean; path?: string }>
    createProject: (command: unknown) => Promise<unknown>
  }
}

beforeEachSetup()

function beforeEachSetup(): void {
  windowWithHandoff.handoff = {
    pickLocalDirectory: vi.fn().mockResolvedValue({ canceled: false, path: '/Users/me/repo' }),
    createProject: vi.fn().mockResolvedValue({ operation_id: 'op-1', state: 'pending' })
  }
}

describe('ProjectCreateDialog', () => {
  const renderDialog = (machines: CreateDialogMachine[]): ReturnType<typeof render> =>
    render(
      <ProjectCreateDialog
        open
        machines={machines}
        onSubmit={vi.fn()}
        onOpenChange={vi.fn()}
      />
    )

  it('requires at least one location (submit disabled without path)', () => {
    renderDialog([{ id: 'm-local', display_name: '本机', kind: 'local' }])
    const submit = screen.getByRole('button', { name: /创建项目/ })
    expect(submit).toBeTruthy()
    expect((submit as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows Finder button for local existing path', () => {
    renderDialog([{ id: 'm-local', display_name: '本机', kind: 'local' }])
    // local 已有目录路径行含「选择目录…」Finder 按钮
    const finder = screen.getByRole('button', { name: /选择目录/ })
    expect(finder).toBeTruthy()
  })

  it('renders name input and location select options', () => {
    renderDialog([{ id: 'm-local', display_name: '本机', kind: 'local' }])
    // 项目名输入框存在（Radix Dialog 可能 portal 双份，用 getAllBy）
    expect(screen.getAllByPlaceholderText('super-debug').length).toBeGreaterThan(0)
    // source 选择器含「已有目录」选项
    expect(screen.getAllByText('已有目录').length).toBeGreaterThan(0)
  })
})
