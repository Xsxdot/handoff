import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { ApiError } from '../../api/client'
import { launchCoordinator } from '../../api/scheduling'
import { NewCardDialog, parseTitles } from './NewCardDialog'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  createCard: vi.fn().mockResolvedValue({ id: 'B77' }),
}))

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  fetchProjects: vi.fn().mockResolvedValue([
    { project_id: 'p1', name: 'handoff', path: '/h', origin_url: '', created_at: '' },
    { project_id: 'p2', name: 'sq', path: '/s', origin_url: '', created_at: '' },
  ]),
  fetchProjectBranches: vi.fn().mockImplementation((name: string) =>
    name === 'sq'
      ? Promise.resolve({ branches: [{ name: 'develop', worktree: '' }], default: 'develop', worktree_root: '/w' })
      : Promise.resolve({ branches: [{ name: 'main', worktree: '' }], default: 'origin/main', worktree_root: '/w' })),
}))

vi.mock('../../api/scheduling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/scheduling')>()),
  launchCoordinator: vi.fn(),
}))

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
})

const props = { open: true, project: 'handoff', cardProjects: ['handoff'], workflows: ['feature', 'bug'], onClose: () => {} }

describe('建卡对话框', () => {
  it('填标题选工作流即可建卡，建成后把新卡号交给调用方', async () => {
    const ledger = await import('../../api/ledger')
    const onCreated = vi.fn()
    render(<NewCardDialog {...props} onCreated={onCreated} />)
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '新工作项' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledWith(
      expect.objectContaining({ title: '新工作项', project: 'handoff', workflow: 'feature' }),
    ))
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('B77'))
  })

  it('工作流列表为空时不伪造 feature，提交省略 workflow 交给账本解析', async () => {
    const ledger = await import('../../api/ledger')
    render(<NewCardDialog {...props} workflows={[]} onCreated={() => {}} />)
    const workflow = screen.getByLabelText('工作流') as HTMLSelectElement
    expect(workflow.value).toBe('')
    expect(screen.getByText('由账本按实际工作流解析')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '待解析的工作项' } })
    expect(screen.getByRole('button', { name: '建卡' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledWith(
      expect.not.objectContaining({ workflow: expect.anything() }),
    ))
  })

  it('标题为空时建卡按钮不可用——别把明知会 400 的请求发出去', () => {
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    expect(screen.getByRole('button', { name: '建卡' })).toBeDisabled()
  })

  it('基线分支标明「建卡后不可改」', () => {
    render(<NewCardDialog {...props} cardProjects={['ghost', 'handoff']} onCreated={() => {}} />)
    expect(screen.getAllByText(/建卡后不可改/)).toHaveLength(2)
  })

  it('后端报错时把原文显示出来，不是静默失败', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard).mockRejectedValueOnce(new Error('project 与 workflow 都是必填'))
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    expect(await screen.findByText(/project 与 workflow 都是必填/)).toBeInTheDocument()
  })
})

describe('项目选择', () => {
  it('候选是 /api/projects 与卡上历史值的并集，去重排序；无筛选值无历史时停在空', async () => {
    render(<NewCardDialog {...props} project="" cardProjects={['benchmarking', 'handoff']} onCreated={() => {}} />)
    const sel = await screen.findByLabelText('项目') as HTMLSelectElement
    await waitFor(() =>
      expect([...sel.options].map((option) => option.value)).toEqual(['', 'benchmarking', 'handoff', 'sq']),
    )
    expect(sel.value).toBe('')
  })

  it('一档预选：顶部筛选值显示在下拉里，用户扫一眼就知道对不对', async () => {
    render(<NewCardDialog {...props} project="handoff" onCreated={() => {}} />)
    const sel = await screen.findByLabelText('项目') as HTMLSelectElement
    await waitFor(() => expect(sel.value).toBe('handoff'))
  })

  it('二档预选：无筛选值时取上次建卡项目（localStorage），且必须还在候选里', async () => {
    localStorage.setItem('handoff.cards.lastProject', 'handoff')
    render(<NewCardDialog {...props} project="" onCreated={() => {}} />)
    const sel = await screen.findByLabelText('项目') as HTMLSelectElement
    await waitFor(() => expect(sel.value).toBe('handoff'))
  })

  it('三档皆无：不预选、按钮禁用、提示「请先选择项目」、不调 createCard', async () => {
    const ledger = await import('../../api/ledger')
    render(<NewCardDialog {...props} project="" cardProjects={[]} onCreated={() => {}} />)
    expect((await screen.findByLabelText('项目') as HTMLSelectElement).value).toBe('')
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '某件事' } })
    expect(screen.getByRole('button', { name: '建卡' })).toBeDisabled()
    expect(screen.getByText('请先选择项目')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    expect(vi.mocked(ledger.createCard)).not.toHaveBeenCalled()
  })

  it('选了项目后按钮解禁，提交值以对话框下拉为准（不是调用方传入的预选建议）', async () => {
    const ledger = await import('../../api/ledger')
    render(<NewCardDialog {...props} project="" cardProjects={['benchmarking']} onCreated={() => {}} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'sq' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '第一张 sq 卡' } })
    expect(screen.getByRole('button', { name: '建卡' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledWith(
      expect.objectContaining({ title: '第一张 sq 卡', project: 'sq' }),
    ))
  })
})

describe('基线分支', () => {
  it('选定项目即取分支：default 是远端跟踪名也并进选项并被填入，切项目重取并换值', async () => {
    const api = await import('../../api/client')
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    const base = await screen.findByLabelText('基线分支') as HTMLInputElement
    await waitFor(() => {
      expect(base.value).toBe('origin/main')
      expect(vi.mocked(api.fetchProjectBranches)).toHaveBeenCalledWith('handoff')
    })
    const options = document.querySelectorAll('#new-card-base-options option')
    expect([...options].some((option) => (option as HTMLOptionElement).value === 'origin/main')).toBe(true)
    fireEvent.change(screen.getByLabelText('项目'), { target: { value: 'sq' } })
    await waitFor(() => {
      expect(base.value).toBe('develop')
      expect(vi.mocked(api.fetchProjectBranches)).toHaveBeenLastCalledWith('sq')
    })
  })

  it('项目未登记（404）降级纯手输并说明原因，不弹错；手输名照常随卡提交', async () => {
    const api = await import('../../api/client')
    const ledger = await import('../../api/ledger')
    vi.mocked(api.fetchProjectBranches).mockImplementation((name: string) =>
      name === 'ghost'
        ? Promise.reject(new ApiError(404, '项目 ghost 未登记'))
        : Promise.resolve({ branches: [{ name: 'main', worktree: '' }], default: 'main', worktree_root: '/w' }))
    render(<NewCardDialog {...props} cardProjects={['ghost', 'handoff']} onCreated={() => {}} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'ghost' } })
    expect(await screen.findByText(/未登记位置，分支需手输/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('基线分支'), { target: { value: 'feat/from-scratch' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '给未登记项目建卡' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledWith(
      expect.objectContaining({ project: 'ghost', base_branch: 'feat/from-scratch' }),
    ))
  })
})

describe('标题批量', () => {
  it('开卡即绑不把 coordinate 写入 createCard，并在每张成功卡后拉起协调者', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard)
      .mockResolvedValueOnce({ id: 'B1' })
      .mockResolvedValueOnce({ id: 'B2' })
    vi.mocked(launchCoordinator).mockResolvedValue({ woke: true, rebuilt: false, escalated: false })
    render(<NewCardDialog {...props} project="handoff" onCreated={() => {}} />)
    fireEvent.click(screen.getByRole('checkbox', { name: /开卡即绑/ }))
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '一\n二' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledTimes(2))
    expect(vi.mocked(ledger.createCard).mock.calls[0][0]).not.toHaveProperty('coordinate')
    expect(launchCoordinator).toHaveBeenNthCalledWith(1, 'B1', 'card_create')
    expect(launchCoordinator).toHaveBeenNthCalledWith(2, 'B2', 'card_create')
  })

  it('协调者拉起失败不回滚已建卡，并逐条显示失败原因', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard).mockResolvedValue({ id: 'B3' })
    vi.mocked(launchCoordinator).mockRejectedValue(new Error('409: 协调者队列已满'))
    render(<NewCardDialog {...props} project="handoff" onCreated={() => {}} />)
    fireEvent.click(screen.getByRole('checkbox', { name: /开卡即绑/ }))
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '保留已建卡' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    expect(await screen.findByText(/B3/)).toBeVisible()
    expect(screen.getByText(/协调者失败/)).toBeVisible()
    expect(screen.getByText(/队列已满/)).toBeVisible()
  })

  it('parseTitles：列表前缀、空行、trim；负数样标题不被误伤', () => {
    expect(parseTitles('- 一\n* 二\n1. 三\n2) 四\n3、五\n\n  六  \n-40ms')).toEqual(
      ['一', '二', '三', '四', '五', '六', '-40ms'],
    )
    expect(parseTitles('   ')).toEqual([])
    expect(parseTitles('')).toEqual([])
  })

  it('N 行提交按输入顺序串行调 createCard N 次，标题各异其余字段相同；成功后回调最后一张', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard)
      .mockResolvedValueOnce({ id: 'B201' })
      .mockResolvedValueOnce({ id: 'B202' })
      .mockResolvedValueOnce({ id: 'B203' })
    const onCreated = vi.fn()
    render(<NewCardDialog {...props} project="" onCreated={onCreated} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'handoff' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '- 一\n二\n3. 三' } })
    expect(screen.getByText(/将建 3 张卡/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(ledger.createCard).toHaveBeenCalledTimes(3))
    const calls = vi.mocked(ledger.createCard).mock.calls.map((call) => call[0])
    expect(calls.map((request) => request.title)).toEqual(['一', '二', '三'])
    for (const request of calls) {
      expect(request).toMatchObject({ project: 'handoff', workflow: 'feature', priority: '中' })
    }
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('B203'))
  })

  it('部分失败不回滚：成功的列卡号、失败的列行内容与原因，留在原地可改行重试', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard)
      .mockResolvedValueOnce({ id: 'B204' })
      .mockRejectedValueOnce(new Error('title 与 workflow 都是必填'))
      .mockResolvedValueOnce({ id: 'B205' })
    const onCreated = vi.fn()
    render(<NewCardDialog {...props} project="" onCreated={onCreated} />)
    fireEvent.change(await screen.findByLabelText('项目'), { target: { value: 'handoff' } })
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '甲\n乙\n丙' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(screen.getAllByText(/^已建 /)).toHaveLength(2))
    expect(screen.getByText(/B204/)).toBeInTheDocument()
    expect(screen.getByText(/乙.*title 与 workflow 都是必填/)).toBeInTheDocument()
    expect(onCreated).not.toHaveBeenCalled()
  })
})
