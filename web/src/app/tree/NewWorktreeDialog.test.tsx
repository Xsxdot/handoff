// NewWorktreeDialog.test.tsx —— 新建工作树弹层的交互测试。
//
// 职责：验证分支加载、占用置灰、提交回调与原文错误展示。
// 边界：不测试后端 git 行为；agentd 核心测试位于 internal/agentd。
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { NewWorktreeDialog } from './NewWorktreeDialog'
import * as client from '../../api/client'
import * as ledger from '../../api/ledger'
import type { CardView } from '../../api/ledger'

const branches = {
  branches: [
    { name: 'main', worktree: '/w' },
    { name: 'feat/free', worktree: '' },
  ],
  default: 'main',
  worktree_root: '/data/worktrees/manual',
}

const cardA: CardView = { id: 'B1', title: '卡 A', project: 'handoff', status: '待办', priority: '中', workflow: 'triage', parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0, conflict: false, open_tickets: 0 }
const cardB: CardView = { ...cardA, id: 'B2', title: '卡 B' }

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(ledger, 'fetchCards').mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: null, unknown_targets: null } })
  vi.spyOn(ledger, 'fetchCardDetail').mockResolvedValue({
    card: cardA as never, relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
  })
})

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
    await waitFor(() => expect(screen.getByText('工作树已创建')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '完成' }))
    expect(onCreated).toHaveBeenCalledWith(ws)
    expect(client.createWorktree).toHaveBeenCalledWith('handoff', { mode: 'new_branch', branch: 'feat/x', base: 'main' }, 'mac-02')
  })

  it('列出卡候选，已派发卡置灰并说明冻结', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [
        { ...cardA, base_frozen: true },
        { ...cardB, base_frozen: false },
      ],
      unlinked: { count: 0, tasks: null, unknown_targets: null },
    })
    open()
    expect(await screen.findByRole('checkbox', { name: '选择卡 B1' })).toBeDisabled()
    expect(screen.getByText('已派发，基线已冻结')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: '选择卡 B2' })).not.toBeDisabled()
    expect(ledger.fetchCards).toHaveBeenCalledWith('project=handoff')
  })

  it('卡候选直接使用列表冻结标记，不为每张卡追加详情请求', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    const frozen = { ...cardA, base_frozen: true } as CardView & { base_frozen: boolean }
    const free = { ...cardB, base_frozen: false } as CardView & { base_frozen: boolean }
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [frozen, free], unlinked: { count: 0, tasks: null, unknown_targets: null } })
    vi.mocked(ledger.fetchCardDetail).mockClear()
    open()
    expect(await screen.findByRole('checkbox', { name: '选择卡 B1' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: '选择卡 B2' })).not.toBeDisabled()
    expect(ledger.fetchCardDetail).not.toHaveBeenCalled()
  })

  it('选择卡时按顺序发送 card_ids，零选择不发送该键', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    vi.spyOn(client, 'createWorktree').mockResolvedValue({ path: '/w', branch: 'feat/test', head: '', is_main: false, managed: true, created_at: '' })
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [cardA, cardB], unlinked: { count: 0, tasks: null, unknown_targets: null } })
    const firstView = open()
    await screen.findByRole('checkbox', { name: '选择卡 B1' })
    fireEvent.click(screen.getByRole('checkbox', { name: '选择卡 B1' }))
    fireEvent.click(screen.getByRole('checkbox', { name: '选择卡 B2' }))
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/two-cards' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(client.createWorktree).toHaveBeenCalledWith(
      'handoff', { mode: 'new_branch', branch: 'feat/two-cards', base: 'main', card_ids: ['B1', 'B2'] }, 'mac-02',
    ))

    vi.mocked(client.createWorktree).mockClear()
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: null, unknown_targets: null } })
    firstView.unmount()
    open()
    await waitFor(() => expect(screen.getByLabelText('分支名')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/no-card' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(client.createWorktree).toHaveBeenCalled())
    const request = vi.mocked(client.createWorktree).mock.calls[0][1]
    expect(Object.keys(request)).not.toContain('card_ids')
  })

  it('工作树成功后显示逐卡混合结果，完成前不卸载结果', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [cardA, cardB], unlinked: { count: 0, tasks: null, unknown_targets: null } })
    vi.spyOn(client, 'createWorktree').mockResolvedValue({
      path: '/data/worktrees/manual/feat-mixed', branch: 'feat/mixed', head: 'abc', is_main: false, managed: true, created_at: '',
      card_results: [{ id: 'B1', ok: true }, { id: 'B2', ok: false, error: '卡 B2 已冻结' }],
    })
    const onCreated = vi.fn()
    open({ onCreated })
    await screen.findByRole('checkbox', { name: '选择卡 B1' })
    fireEvent.click(screen.getByRole('checkbox', { name: '选择卡 B1' }))
    fireEvent.click(screen.getByRole('checkbox', { name: '选择卡 B2' }))
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/mixed' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('工作树已创建')).toBeInTheDocument()
    expect(screen.getByText((_, element) => element?.tagName === 'LI' && (element.textContent ?? '').includes('卡 B2 已冻结'))).toBeInTheDocument()
    expect(onCreated).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '完成' }))
    expect(onCreated).toHaveBeenCalledTimes(1)
  })

  it('账本候选 503 只告警，不阻止建树且不发送 card_ids', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    vi.mocked(ledger.fetchCards).mockRejectedValue(new Error('账本不可用：503'))
    const onCreated = vi.fn()
    vi.spyOn(client, 'createWorktree').mockResolvedValue({ path: '/w', branch: 'feat/no-ledger', head: '', is_main: false, managed: true, created_at: '' })
    open({ onCreated })
    expect(await screen.findByText(/账本不可用：503/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/no-ledger' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(client.createWorktree).toHaveBeenCalled())
    const request = vi.mocked(client.createWorktree).mock.calls[0][1]
    expect(Object.keys(request)).not.toContain('card_ids')
  })

  it('卡候选加载失败提供重试入口', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    vi.mocked(ledger.fetchCards).mockRejectedValueOnce(new Error('账本暂不可用'))
    vi.mocked(ledger.fetchCards).mockResolvedValueOnce({ cards: [cardA], unlinked: { count: 0, tasks: null, unknown_targets: null } })
    open()
    expect(await screen.findByText(/账本暂不可用/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重试卡候选' }))
    expect(await screen.findByRole('checkbox', { name: '选择卡 B1' })).toBeInTheDocument()
    expect(ledger.fetchCards).toHaveBeenCalledTimes(2)
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
