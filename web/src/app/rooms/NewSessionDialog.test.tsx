// NewSessionDialog.test.tsx —— B444 项目内建会话框（plan A3.1/A3.2 的组件半边）：项目只读、标题可空、
// 卡候选过滤、驻场小队预填与无协调小队禁用、提交中提示、结果视图点名。
// B358.9 不变式保留：无群主输入；未配 console_user 时可行动提示并禁用创建。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Card } from '../../api/ledger'
import type { SessionCreateResp, SessionSummary } from '../../api/rooms'
import { NewSessionDialog } from './NewSessionDialog'
import { pullableCards } from './sessionModel'

vi.mock('../../api/scheduling', async () => {
  const actual = await vi.importActual<typeof import('../../api/scheduling')>('../../api/scheduling')
  return { ...actual, getSquads: vi.fn() }
})
const { getSquads } = await import('../../api/scheduling')

const card = (id: string, project: string, status = '待办'): Card => ({
  id, title: `${id} 标题`, status, priority: '', project, parent: '', workflow: '', workflow_version: 0, created_at: '', updated_at: '',
})
const squad = (name: string, role = 'coordinator') => ({ name, role, members: [], version: 1 })
const cards = [card('B1', 'handoff'), card('B2', 'aim'), card('B3', 'handoff', '已完成'), card('B4', 'handoff', '终止'), card('B5', 'handoff'), card('B6', 'handoff', '进行中')]
const sessions: Pick<SessionSummary, 'cards'>[] = [{ cards: [{ card_id: 'B5' }] }]

function open(over: Partial<Parameters<typeof NewSessionDialog>[0]> = {}) {
  const props = {
    open: true, project: 'handoff', cards, sessions, busy: false, error: '', configured: true, result: null,
    onCancel: () => {}, onCreate: vi.fn(), onDone: () => {}, ...over,
  }
  return { ...render(<NewSessionDialog {...props} />), props }
}

beforeEach(() => {
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [squad('exec', 'executor'), squad('coord')], running: [] })
})

describe('pullableCards（A3.2 纯函数）', () => {
  it('排除他项目卡、终态卡、已在会话里的卡，保持顺序', () => {
    expect(pullableCards(cards, 'handoff', sessions).map((c) => c.id)).toEqual(['B1', 'B6'])
    expect(pullableCards(cards, 'aim', sessions).map((c) => c.id)).toEqual(['B2'])
    expect(pullableCards(cards, 'nobody', [])).toEqual([])
  })
})

describe('NewSessionDialog（B444）', () => {
  it('A3.1：项目只读且等于入口项目；标题可空、占位「留空则自动命名」；无群主输入（B358.9 反例）', async () => {
    open()
    const project = screen.getByRole('textbox', { name: '所属项目' })
    expect(project).toHaveValue('handoff')
    expect(project).toHaveAttribute('readonly')
    expect(screen.getByRole('textbox', { name: '会话标题' })).toHaveAttribute('placeholder', '留空则自动命名')
    expect(screen.queryByRole('combobox', { name: '群主身份' })).toBeNull()
    // 唯一协调小队预填 → 空标题也可创建
    await waitFor(() => expect(screen.getByRole('combobox', { name: '驻场小队' })).toHaveValue('coord'))
    expect(screen.getByRole('button', { name: '创建' })).toBeEnabled()
  })

  it('A3.2：卡列表只列本项目、未终态、未进会话的卡；提交带所选卡与小队', async () => {
    const { props } = open()
    const user = userEvent.setup()
    const group = screen.getByTestId('new-session-cards')
    expect(group).toHaveTextContent('B1')
    expect(group).toHaveTextContent('B6')
    for (const absent of ['B2', 'B3', 'B4', 'B5']) expect(group).not.toHaveTextContent(absent)
    await waitFor(() => expect(screen.getByRole('button', { name: '创建' })).toBeEnabled())
    await user.click(screen.getByRole('checkbox', { name: 'B6 B6 标题' }))
    await user.click(screen.getByRole('checkbox', { name: 'B1 B1 标题' }))
    await user.type(screen.getByRole('textbox', { name: '会话标题' }), '  联调 ')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(props.onCreate).toHaveBeenCalledWith({ title: '联调', cards: ['B6', 'B1'], squad: 'coord' })
  })

  it('A3.2：本项目无可拉入卡时显示空态', () => {
    open({ project: 'empty' })
    expect(screen.getByTestId('new-session-cards')).toHaveTextContent('本项目没有可拉入的卡')
  })

  it('A3.2：无协调小队 → 提示去设置建小队且创建禁用', async () => {
    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [squad('exec', 'executor')], running: [] })
    open()
    expect(await screen.findByText(/请先到「设置」里建一个协调小队/)).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: '驻场小队' })).toBeNull()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('多个协调小队不替人挑：未选前禁用创建', async () => {
    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [squad('a'), squad('b')], running: [] })
    const user = userEvent.setup()
    open()
    const select = await screen.findByRole('combobox', { name: '驻场小队' })
    expect(select).toHaveValue('')
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    await user.selectOptions(select, 'b')
    expect(screen.getByRole('button', { name: '创建' })).toBeEnabled()
  })

  it('读小队失败：显示原因并禁用创建', async () => {
    vi.mocked(getSquads).mockRejectedValue(new Error('agentd 不可达'))
    open()
    expect(await screen.findByText(/读取小队失败：agentd 不可达/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('提交中显示「正在拉起驻场者…」并禁用创建', async () => {
    open({ busy: true })
    expect(screen.getByRole('status')).toHaveTextContent('正在拉起驻场者…')
    await waitFor(() => expect(screen.getByRole('combobox', { name: '驻场小队' })).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('未配名：渲染含 console_user 的可行动提示且创建禁用（B358.9）', async () => {
    open({ configured: false })
    await waitFor(() => expect(screen.getByRole('combobox', { name: '驻场小队' })).toBeInTheDocument())
    expect(screen.getByText(/console_user/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('结果视图：点名被拒的卡与驻场者首轮失败原因', async () => {
    const result: SessionCreateResp = {
      id: 'session:9', title: 'B1 标题', owner: 'user:sy', archived: false, created_at: '', updated_at: '',
      rejected_cards: [{ card: 'B3', reason: '卡已在会话 session:4 里' }],
      resident: { error: '拉起驻场者首轮失败: 载体登录失效' },
    }
    const onDone = vi.fn()
    const user = userEvent.setup()
    open({ result, onDone })
    const view = screen.getByTestId('new-session-result')
    expect(view).toHaveTextContent('会话「B1 标题」已建立')
    expect(screen.getByTestId('new-session-rejected')).toHaveTextContent('B3：卡已在会话 session:4 里')
    expect(view).toHaveTextContent('驻场者未拉起：拉起驻场者首轮失败: 载体登录失效')
    expect(view).toHaveTextContent('handoff session resident launch session:9')
    await user.click(screen.getByRole('button', { name: '知道了' }))
    expect(onDone).toHaveBeenCalled()
  })
})
