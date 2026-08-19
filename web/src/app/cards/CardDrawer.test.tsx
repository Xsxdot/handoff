import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CardDrawer } from './CardDrawer'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCardDetail: vi.fn().mockResolvedValue({
    card: { ID: 'B147', Title: '承载卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
    relations: [
      { From: 'B144', To: 'B147', Type: 'merged_into' },
      { From: 'B147', To: 'B95', Type: 'blocks' },
    ],
    events: [], task_states: [], effective_base_branch: '',
  }),
}))

describe('抽屉一处看', () => {
  it('承载卡显示并入区成员，关系区不重复「承载着」', async () => {
    render(<CardDrawer id="B147" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/并入本卡/)).toBeInTheDocument()
    expect(screen.getByText('B144')).toBeInTheDocument()
    expect(screen.queryByText('承载着')).not.toBeInTheDocument()
  })
})
