// 两个弹层（EntryNameDialog / DeleteEntryDialog）的独立行为测试。
// 这里直接渲染组件、不经过 FileTree，验证的是弹层自身的输入校验、文案与回调。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EntryNameDialog } from './EntryNameDialog'
import { DeleteEntryDialog } from './DeleteEntryDialog'

describe('EntryNameDialog', () => {
  it('输入合法名点提交，回调收到名字', async () => {
    const onSubmit = vi.fn()
    render(<EntryNameDialog title="新文件" initialName="" submitLabel="创建" onSubmit={onSubmit} onCancel={vi.fn()} />)
    await userEvent.type(screen.getByLabelText('名称'), 'x.go')
    await userEvent.click(screen.getByRole('button', { name: '创建' }))
    expect(onSubmit).toHaveBeenCalledWith('x.go')
  })

  it('名字含 / 时提交 disabled 并给出理由', async () => {
    render(<EntryNameDialog title="新文件" initialName="" submitLabel="创建" onSubmit={vi.fn()} onCancel={vi.fn()} />)
    await userEvent.type(screen.getByLabelText('名称'), 'a/b.go')
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(screen.getByText(/名字不能包含/)).toBeInTheDocument()
  })

  it('名字含 \\ 时同样禁提交——服务端对两种分隔符一视同仁', async () => {
    render(<EntryNameDialog title="新文件" initialName="" submitLabel="创建" onSubmit={vi.fn()} onCancel={vi.fn()} />)
    await userEvent.type(screen.getByLabelText('名称'), 'a\\b.go')
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(screen.getByText(/名字不能包含/)).toBeInTheDocument()
  })

  it('空名字提交 disabled', () => {
    render(<EntryNameDialog title="新文件" initialName="" submitLabel="创建" onSubmit={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('重命名时初始值预填并聚焦全选，直接打字覆盖旧名', async () => {
    render(<EntryNameDialog title="重命名" initialName="old.go" submitLabel="保存" onSubmit={vi.fn()} onCancel={vi.fn()} />)
    const input = screen.getByLabelText('名称') as HTMLInputElement
    expect(input.value).toBe('old.go')
    expect(input).toHaveFocus()
    await userEvent.keyboard('new.go')
    expect((screen.getByLabelText('名称') as HTMLInputElement).value).toBe('new.go')
  })

  it('Esc 取消，不触发提交', () => {
    const onSubmit = vi.fn()
    const onCancel = vi.fn()
    render(<EntryNameDialog title="新文件" initialName="" submitLabel="创建" onSubmit={onSubmit} onCancel={onCancel} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('busy 时按钮置灰显示提交中，错误原文透出', () => {
    render(
      <EntryNameDialog
        title="重命名"
        initialName="a.go"
        submitLabel="保存"
        busy
        error="目标已存在"
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: '提交中…' })).toBeDisabled()
    expect(screen.getByText('目标已存在')).toBeInTheDocument()
  })
})

describe('DeleteEntryDialog', () => {
  it('文案点名「未被 git 跟踪的文件删除后无法恢复」', () => {
    render(<DeleteEntryDialog name="a.go" isDir={false} rel="a.go" onConfirm={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText(/未被 git 跟踪的文件删除后无法恢复/)).toBeInTheDocument()
  })

  it('点「删除」触发回调，点「取消」不触发', async () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    render(<DeleteEntryDialog name="a.go" isDir={false} rel="a.go" onConfirm={onConfirm} onCancel={onCancel} />)
    await userEvent.click(screen.getByRole('button', { name: '删除' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('Esc 取消，不触发删除', () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    render(<DeleteEntryDialog name="a.go" isDir={false} rel="a.go" onConfirm={onConfirm} onCancel={onCancel} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('目录且条目数已知时显示「至少 N 项」，不编精确数', () => {
    render(
      <DeleteEntryDialog name="internal" isDir rel="internal" dirCount={3} onConfirm={vi.fn()} onCancel={vi.fn()} />,
    )
    expect(screen.getByText(/至少 3 项/)).toBeInTheDocument()
  })

  it('目录且条目数未知时如实说「无法恢复」，不编数字', () => {
    render(<DeleteEntryDialog name="internal" isDir rel="internal" onConfirm={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText(/目录里可能还有更多内容，删除后无法恢复/)).toBeInTheDocument()
    expect(screen.queryByText(/至少 \d+ 项/)).not.toBeInTheDocument()
  })

  it('错误原文透出，删除按钮仍可用重试', async () => {
    const onConfirm = vi.fn()
    render(
      <DeleteEntryDialog
        name="a.go"
        isDir={false}
        rel="a.go"
        error="不允许写入 .git 目录"
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText('不允许写入 .git 目录')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '删除' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })
})
