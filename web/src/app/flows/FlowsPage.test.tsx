// FlowsPage 测试：验证工作流编辑加载、发布新版本与原文错误展示。
// 边界：节点字段细节由 NodeEditor 测试覆盖，本文件只检查页面编排。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { getSquads } from '../../api/scheduling'
import { FlowsPage } from './FlowsPage'

vi.mock('../../api/scheduling', () => ({
  getSquads: vi.fn().mockResolvedValue({ carriers: [], squads: [] }),
}))

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchFlows: vi.fn().mockResolvedValue({
    workflows: [{ name: 'feature', version: 3, def: { states: ['待办', '已完成'] } }],
    templates: [{ name: 'review-generic', version: 1, def: {} }],
  }),
  fetchFlow: vi.fn().mockResolvedValue({
    name: 'feature', version: 3, states: ['待办', '已完成'],
    nodes: [{ name: '待办', next: '已完成' }, { name: '已完成' }],
  }),
  fetchDisciplineNames: vi.fn().mockResolvedValue(['implement', 'review', 'finishing']),
  putFlow: vi.fn().mockResolvedValue({ name: 'feature', version: 4 }),
}))

beforeEach(() => {
  vi.clearAllMocks()
})

describe('工作流页可编辑', () => {
  it('保存调 putFlow 并把新版本号显示出来', async () => {
    const ledger = await import('../../api/ledger')
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(await screen.findByRole('button', { name: '保存为新版本' }))
    await waitFor(() => expect(vi.mocked(ledger.putFlow)).toHaveBeenCalledWith(
      'feature', expect.any(Array), expect.objectContaining({ columns: expect.any(Array), fallback: expect.any(String) }),
    ))
    expect(await screen.findByText(/v4/)).toBeInTheDocument()
  })

  it('保存前明说这是「发新版本」，老卡不受影响', async () => {
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    expect(await screen.findByText(/发布一个新版本.*已有的卡仍走各自钉住的版本/)).toBeInTheDocument()
  })

  it('后端拒绝时把真因显示出来，不是「保存失败」四个字', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.putFlow).mockRejectedValueOnce(new Error('节点 "A" 的 Next 指向不存在的节点 "B"'))
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(await screen.findByRole('button', { name: '保存为新版本' }))
    expect(await screen.findByText(/Next 指向不存在的节点/)).toBeInTheDocument()
  })

  it('只保留节点编排表作为看板映射编辑入口', async () => {
    const ledger = await import('../../api/ledger')
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    expect(screen.queryByRole('region', { name: '看板列映射' })).not.toBeInTheDocument()
    const orchestration = await screen.findByRole('region', { name: '节点编排' })
    fireEvent.change(within(orchestration).getByRole('combobox', { name: '节点 待办 的看板列' }), { target: { value: '沟通中' } })
    fireEvent.click(await screen.findByRole('button', { name: '保存为新版本' }))
    await waitFor(() => expect(vi.mocked(ledger.putFlow)).toHaveBeenCalledWith(
      'feature', expect.any(Array), expect.objectContaining({ state_to_column: expect.objectContaining({ 待办: '沟通中' }) }),
    ))
  })

  it('以工作流详情节点作为固定行，不提供加列或删节点控件', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchFlows).mockResolvedValue({ workflows: [{ name: 'feature', version: 2, def: { states: ['待办', '进行中'] } }], templates: [] })
    vi.mocked(ledger.fetchFlow).mockResolvedValue({
      name: 'feature', version: 2, states: ['待办', '进行中'],
      nodes: [{ name: '待办', override: { executor: 'old' } }, { name: '进行中' }],
      board: { columns: ['代办', '沟通中', '进行中', '审核中', '结束'], fallback: '代办', state_to_column: { 待办: '代办', 进行中: '进行中' } },
    })
    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [] })
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    const orchestration = await screen.findByRole('region', { name: '节点编排' })
    expect(within(orchestration).getByText('节点编排')).toBeVisible()
    expect(within(orchestration).getByRole('cell', { name: '待办' })).toBeVisible()
    expect(within(orchestration).getAllByRole('cell', { name: '进行中' })[0]).toBeVisible()
    expect(screen.queryByRole('button', { name: '加一列' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /删除/ })).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '节点 进行中 的派发小队' })).toHaveValue('')
    expect(vi.mocked(ledger.fetchDisciplineNames)).not.toHaveBeenCalled()
  })

  it('把节点小队写入 override.squad 并保留其他覆盖；拉起通道只列 coordinator', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchFlows).mockResolvedValue({ workflows: [{ name: 'feature', version: 2, def: { states: ['待办', '进行中'] } }], templates: [] })
    vi.mocked(ledger.fetchFlow).mockResolvedValue({
      name: 'feature', version: 2, states: ['待办', '进行中'],
      nodes: [{ name: '待办', override: { executor: 'old' } }, { name: '进行中' }],
      board: { columns: ['代办', '沟通中', '进行中', '审核中', '结束'], fallback: '代办', state_to_column: { 待办: '代办', 进行中: '进行中' } },
    })
    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [
      { name: 'exec', role: 'executor', members: [], version: 1 },
      { name: 'coord', role: 'coordinator', members: [], version: 1 },
    ] })
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.change(await screen.findByRole('combobox', { name: '节点 进行中 的派发小队' }), { target: { value: 'exec' } })
    expect(screen.getByRole('combobox', { name: '拉起通道 的派发小队' })).toHaveValue('coord')
    expect(screen.getByRole('combobox', { name: '拉起通道 的派发小队' })).not.toHaveValue('exec')
    fireEvent.click(screen.getByRole('button', { name: '保存为新版本' }))
    await waitFor(() => expect(vi.mocked(ledger.putFlow)).toHaveBeenCalledWith('feature', [
      { name: '待办', override: { executor: 'old' } },
      { name: '进行中', override: { squad: 'exec' } },
    ], expect.anything()))
  })

  it('协调者小队不唯一时只展示歧义，不伪造 flow 级 launch 字段', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchFlows).mockResolvedValue({ workflows: [{ name: 'feature', version: 2, def: { states: ['待办'] } }], templates: [] })
    vi.mocked(ledger.fetchFlow).mockResolvedValue({
      name: 'feature', version: 2, states: ['待办'], nodes: [{ name: '待办' }], board: undefined,
    })
    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [
      { name: 'coord-a', role: 'coordinator', members: [], version: 1 },
      { name: 'coord-b', role: 'coordinator', members: [], version: 1 },
    ] })
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    expect(await screen.findByText(/协调者小队不唯一/)).toBeVisible()
    expect(vi.mocked(ledger.putFlow)).not.toHaveBeenCalledWith(expect.anything(), expect.anything(), expect.objectContaining({ launch_squad: expect.anything() }))
  })
})
