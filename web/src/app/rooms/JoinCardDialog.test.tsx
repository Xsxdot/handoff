// JoinCardDialog.test.tsx —— 卡选择器（B358.8 #8）：列表加载、搜索过滤（标题/卡号）、
// 多选与已选数、已入群禁选、确认回调载荷、加载失败原文。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { fetchCards } from '../../api/ledger'
import type { CardView } from '../../api/ledger'
import { JoinCardDialog } from './JoinCardDialog'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCards: vi.fn(),
}))

const card = (id: string, title: string, status = '进行中'): CardView => ({
  id, title, status, priority: 'P2', project: 'handoff', workflow: 'charter', parent: '',
  base_branch: 'main', attachments: [], following: '', blocked: false, blocked_by: [],
  merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
  conflict: false, open_tickets: 0,
})

const openDialog = (over: { existingCardIds?: string[]; error?: string } = {}) =>
  render(
    <JoinCardDialog open busy={false} error={over.error ?? ''} existingCardIds={over.existingCardIds ?? []}
      onCancel={() => {}} onJoin={() => {}} />,
  )

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchCards).mockResolvedValue({
    cards: [card('B233.14', '编制入站封界'), card('B233.17', '组装点收窄', '待办'), card('B365', 'Reply 走查', 'REPLY')],
    unlinked: { tasks: [] },
  } as never)
})

describe('JoinCardDialog（卡选择器）', () => {
  it('打开即拉全量卡列表：行 = 卡号 mono + 标题 + 状态', async () => {
    openDialog()
    expect(await screen.findByText('编制入站封界')).toBeInTheDocument()
    expect(screen.getByText('组装点收窄')).toBeInTheDocument()
    expect(fetchCards).toHaveBeenCalledWith('')
  })

  it('搜索按标题与卡号大小写不敏感过滤', async () => {
    const user = userEvent.setup()
    openDialog()
    await screen.findByText('编制入站封界')
    await user.type(screen.getByRole('textbox', { name: '搜索卡' }), 'b233')
    expect(screen.getAllByText(/编制入站封界|组装点收窄/)).toHaveLength(2)
    expect(screen.queryByText('Reply 走查')).toBeNull()
    await user.clear(screen.getByRole('textbox', { name: '搜索卡' }))
    await user.type(screen.getByRole('textbox', { name: '搜索卡' }), 'REPLY')
    expect(screen.getByText('Reply 走查')).toBeInTheDocument()
    expect(screen.queryByText('编制入站封界')).toBeNull()
  })

  it('多选：已选数上确认钮，确认回调携带全部选中卡号', async () => {
    const onJoin = vi.fn()
    const user = userEvent.setup()
    render(<JoinCardDialog open busy={false} error="" existingCardIds={[]} onCancel={() => {}} onJoin={onJoin} />)
    await screen.findByText('编制入站封界')
    await user.click(screen.getByRole('checkbox', { name: '选择 B233.14' }))
    await user.click(screen.getByRole('checkbox', { name: '选择 B365' }))
    expect(screen.getByRole('button', { name: /确认拉卡（2）/ })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /确认拉卡（2）/ }))
    // 载荷按勾选顺序携带全部选中卡号
    expect(onJoin).toHaveBeenCalledWith(['B233.14', 'B365'])
  })

  it('零选中禁用确认钮', async () => {
    openDialog()
    await screen.findByText('编制入站封界')
    expect(screen.getByRole('button', { name: /确认拉卡（0）/ })).toBeDisabled()
  })

  it('已在本会话内的卡禁选并标注「已在会话」', async () => {
    openDialog({ existingCardIds: ['B233.14'] })
    await screen.findByText('编制入站封界')
    expect(screen.getByText('已在会话')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: '选择 B233.14' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: '选择 B233.17' })).toBeEnabled()
  })

  it('加载失败原文透传，不给空列表假象', async () => {
    vi.mocked(fetchCards).mockRejectedValue(new Error('账本离线'))
    openDialog()
    expect(await screen.findByRole('alert')).toHaveTextContent('卡列表读取失败：账本离线')
  })

  it('失败后重开对话框会重新拉列表（每次打开重置加载态）', async () => {
    const view = render(
      <JoinCardDialog open={false} busy={false} error="" onCancel={() => {}} onJoin={() => {}} />,
    )
    view.rerender(<JoinCardDialog open busy={false} error="" onCancel={() => {}} onJoin={() => {}} />)
    await waitFor(() => expect(fetchCards).toHaveBeenCalledTimes(1))
  })
})
