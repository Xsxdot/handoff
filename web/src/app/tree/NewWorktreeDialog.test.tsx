import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { NewWorktreeDialog } from './NewWorktreeDialog'
import * as client from '../../api/client'

const branches = {
  branches: [
    { name: 'main', worktree: '/w' },
    { name: 'feat/free', worktree: '' },
  ],
  default: 'main',
  worktree_root: '/data/worktrees/manual',
}

beforeEach(() => vi.restoreAllMocks())

function open(over: Partial<Parameters<typeof NewWorktreeDialog>[0]> = {}) {
  return render(
    <NewWorktreeDialog
      open
      projectName="handoff"
      machine="mac-02"
      onClose={vi.fn()}
      onCreated={vi.fn()}
      {...over}
    />,
  )
}

describe('建树弹层', () => {
  it('打开时拉分支列表，基线默认选中 default，落点根如实回显', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toHaveValue('main'))
    expect(client.fetchProjectBranches).toHaveBeenCalledWith('handoff', 'mac-02')
    expect(screen.getByText(/\/data\/worktrees\/manual/)).toBeInTheDocument()
  })

  it('推导出的基线是 origin/main 这种远端名时，下拉里也得有它（否则显示为空白）', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue({
      branches: [{ name: 'main', worktree: '/w' }],
      default: 'origin/main',
      worktree_root: '/data/worktrees/manual',
    })
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toHaveValue('origin/main'))
    expect(screen.getByRole('option', { name: 'origin/main' })).toBeInTheDocument()
  })

  it('检出已有分支模式下，被占用的分支不可选且标出占用者', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('检出已有分支'))
    const opt = screen.getByRole('option', { name: /main（已被 \/w 占用）/ }) as HTMLOptionElement
    expect(opt.disabled).toBe(true)
  })

  it('创建成功把新工作树交回调用方', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    const ws = { path: '/data/worktrees/manual/feat-x', branch: 'feat/x', head: 'abc', is_main: false, managed: true, created_at: '' }
    vi.spyOn(client, 'createWorktree').mockResolvedValue(ws)
    const onCreated = vi.fn()
    open({ onCreated })
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/x' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(ws))
    expect(client.createWorktree).toHaveBeenCalledWith('handoff', { mode: 'new_branch', branch: 'feat/x', base: 'main' }, 'mac-02')
  })

  it('创建失败把 agentd 原文贴出来，不缩略成「操作失败」', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    vi.spyOn(client, 'createWorktree').mockRejectedValue(new Error('分支 feat/x 已存在，请换个名字'))
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/x' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(screen.getByText(/分支 feat\/x 已存在，请换个名字/)).toBeInTheDocument())
  })

  it('分支名为空时创建按钮禁用', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('拉分支失败时给原文与重试', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockRejectedValue(new Error('机器 mac-02 不可达'))
    open()
    await waitFor(() => expect(screen.getByText(/机器 mac-02 不可达/)).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })
})
