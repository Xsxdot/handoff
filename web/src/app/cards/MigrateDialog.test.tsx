import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MigrateDialog } from './MigrateDialog'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchFlows: vi.fn(),
  migrateCard: vi.fn(),
}))

const flows = {
  workflows: [
    { name: '流 A', version: 1, def: { states: ['A 待办', 'A 审阅'] } },
    { name: '流 B', version: 1, def: { states: ['B 待办', 'B 完成'] } },
  ],
  templates: [],
}

function selects() {
  return screen.getAllByRole('combobox') as HTMLSelectElement[]
}

describe('MigrateDialog', () => {
  it('落点列随所选工作流刷新，且不预选同名列', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchFlows).mockResolvedValue(flows)
    render(<MigrateDialog open cardId="B1" onClose={vi.fn()} onMigrated={vi.fn()} />)

    await waitFor(() => expect(selects()[0].options).toHaveLength(3))
    const [workflow, status] = selects()
    fireEvent.change(workflow, { target: { value: '流 A' } })
    fireEvent.change(status, { target: { value: 'A 审阅' } })
    expect(status.value).toBe('A 审阅')

    fireEvent.change(workflow, { target: { value: '流 B' } })
    expect([...status.options].map((option) => option.value)).toEqual(['', 'B 待办', 'B 完成'])
    expect(status.value).toBe('')
  })

  it('409 的服务端文案原样展示', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchFlows).mockResolvedValue(flows)
    vi.mocked(ledger.migrateCard).mockRejectedValueOnce(new Error('卡 B1 的任务 T-1 仍在运行，不能迁移'))
    render(<MigrateDialog open cardId="B1" onClose={vi.fn()} onMigrated={vi.fn()} />)

    await waitFor(() => expect(selects()[0].options).toHaveLength(3))
    const [workflow, status] = selects()
    fireEvent.change(workflow, { target: { value: '流 A' } })
    fireEvent.change(status, { target: { value: 'A 待办' } })
    fireEvent.click(screen.getByRole('button', { name: '确认迁移' }))

    expect(await screen.findByText('卡 B1 的任务 T-1 仍在运行，不能迁移')).toBeInTheDocument()
  })
})
