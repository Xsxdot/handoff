import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { NewCardDialog } from './NewCardDialog'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  createCard: vi.fn().mockResolvedValue({ id: 'B77' }),
}))

const props = { open: true, project: 'handoff', workflows: ['feature', 'bug'], onClose: () => {} }

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
    expect(screen.getByText(/建卡后不可改/)).toBeInTheDocument()
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
