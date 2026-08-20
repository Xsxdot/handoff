import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { CardDrawer } from './CardDrawer'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCardDetail: vi.fn().mockResolvedValue({
    card: { ID: 'B147', Title: '承载卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
    relations: [
      { From: 'B144', To: 'B147', Type: 'merged_into' },
      { From: 'B147', To: 'B95', Type: 'blocks' },
    ],
    events: [], task_states: [], effective_base_branch: '', decisions: [],
  }),
  answerDecision: vi.fn().mockResolvedValue(undefined),
}))

describe('抽屉一处看', () => {
  it('承载卡显示并入区成员，关系区不重复「承载着」', async () => {
    render(<CardDrawer id="B147" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/并入本卡/)).toBeInTheDocument()
    expect(screen.getByText('B144')).toBeInTheDocument()
    expect(screen.queryByText('承载着')).not.toBeInTheDocument()
  })
})

describe('抽屉里的裁决', () => {
  it('挂卡的请示要出正文、候选项与答复入口，不能只在 timeline 里剩一行', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B8', Title: 'WS 被 503', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [{ id: 5, card_id: 'B8', body: '就地重试还是直接退化？', options: ['重试三次', '立即退化'], status: 'open', answer: '' }],
    } as never)
    render(<CardDrawer id="B8" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('就地重试还是直接退化？')).toBeInTheDocument()
    expect(screen.getByText('重试三次')).toBeInTheDocument()
    fireEvent.change(screen.getByPlaceholderText('答复这条请示…'), { target: { value: '立即退化但要出告警' } })
    fireEvent.click(screen.getByRole('button', { name: '答复' }))
    await waitFor(() => expect(vi.mocked(ledger.answerDecision)).toHaveBeenCalledWith(5, '立即退化但要出告警'))
  })

  it('已答复的请示显示答案，不再给答复框', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B8', Title: 'WS 被 503', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [{ id: 5, card_id: 'B8', body: '就地重试还是直接退化？', options: null, status: 'answered', answer: '立即退化但要出告警' }],
    } as never)
    render(<CardDrawer id="B8" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/已答复：立即退化但要出告警/)).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('答复这条请示…')).not.toBeInTheDocument()
  })
})

describe('抽屉里的「需要你」', () => {
  it('等人卡要说得出原因——看板有角标，抽屉不能什么都不显示', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B3', Title: '镜像断链', Status: '待合并', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '基线是主线：合并永远人工',
    } as never)
    render(<CardDrawer id="B3" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/需要你/)).toBeInTheDocument()
    expect(screen.getByText('基线是主线：合并永远人工')).toBeInTheDocument()
  })

  it('既不等人也没请示时不画这一区，免得平白多一块空标题', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B9', Title: '普通卡', Status: '待办', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '',
    } as never)
    render(<CardDrawer id="B9" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('普通卡')
    expect(screen.queryByText(/需要你/)).not.toBeInTheDocument()
  })
})

describe('抽屉里的合并事件', () => {
  it('branch_merged 使用专用摘要显示工作分支与基线', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B10', Title: '合并卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
      relations: [],
      events: [{
        seq: 1,
        card_id: 'B10',
        type: 'branch_merged',
        actor: 'node:merge',
        payload: {
          work_branch: 'feat/x',
          pushed_work_branch: true,
          merged_into: 'integration/y',
          pushed_base: 'integration/y',
        },
        created_at: '',
      }],
      task_states: [], effective_base_branch: '', decisions: [], needs: '',
    } as never)
    render(<CardDrawer id="B10" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/合并 feat\/x → integration\/y/)).toBeInTheDocument()
    expect(screen.queryByText(/\{/)).not.toBeInTheDocument()
  })
})
