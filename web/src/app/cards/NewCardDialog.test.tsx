import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { NewCardDialog } from './NewCardDialog'

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

  it('标题为空时建卡按钮不可用——别把明知会 400 的请求发出去', () => {
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    expect(screen.getByRole('button', { name: '建卡' })).toBeDisabled()
  })

  it('基线分支标明「建卡后不可改」', () => {
    render(<NewCardDialog {...props} onCreated={() => {}} />)
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
