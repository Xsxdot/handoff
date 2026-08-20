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
  acceptCard: vi.fn().mockResolvedValue({ ok: true }),
  patchCard: vi.fn().mockResolvedValue({ ok: true }),
  attachFile: vi.fn().mockResolvedValue({ ok: true }),
  detachFile: vi.fn().mockResolvedValue({ ok: true }),
  runCardStep: vi.fn().mockResolvedValue({ ok: true }),
  clearCardNeeds: vi.fn().mockResolvedValue({ ok: true }),
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

describe('抽屉里的子任务', () => {
  it('有直接子卡时列出来，点 id 能跳转', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B156', Title: '父卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '',
      children: [
        { id: 'B156.1', title: '子卡一', status: '待办' },
        { id: 'B156.2', title: '子卡二', status: '已完成' },
      ],
    })
    const opened: string[] = []
    render(<CardDrawer id="B156" onClose={() => {}} onOpenCard={(id) => opened.push(id)} />)
    expect(await screen.findByText(/子任务/)).toBeInTheDocument()
    expect(screen.getByText('子卡一')).toBeInTheDocument()
    expect(screen.getByText('已完成')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'B156.1' }))
    expect(opened).toEqual(['B156.1'])
  })

  it('没有子卡时整区不渲染——空区块比没有区块更吵', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B160', Title: '叶子卡', Status: '待办', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B160" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('叶子卡')
    expect(screen.queryByText(/子任务/)).not.toBeInTheDocument()
  })
})

describe('抽屉里的验收', () => {
  it('未验且已完成显示「待真机验」，标记已验要带证据', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B170', Title: '待验卡', Status: '已完成', Attachments: [], AcceptanceCriteria: '判据：全绿' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    const accept = vi.mocked(ledger.acceptCard).mockResolvedValue({ ok: true })
    render(<CardDrawer id="B170" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('待真机验')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /标记已验/ }))
    const box = screen.getByPlaceholderText(/证据/)
    // 空证据不许提交
    expect(screen.getByRole('button', { name: '确认' })).toBeDisabled()
    fireEvent.change(box, { target: { value: '真机跑了 3 轮' } })
    fireEvent.click(screen.getByRole('button', { name: '确认' }))
    await waitFor(() => expect(accept).toHaveBeenCalledWith('B170', '真机跑了 3 轮'))
  })

  it('未验且未完成显示「未验」——三态里这一态原来是缺的', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B171', Title: '进行中的卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B171" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('进行中的卡')
    expect(screen.getByText('未验')).toBeInTheDocument()
    expect(screen.queryByText('待真机验')).not.toBeInTheDocument()
  })
})

describe('抽屉里的编辑', () => {
  const detail = {
    card: { ID: 'B20', Title: '原标题', Status: '进行中', Priority: '中', Attachments: [], AcceptanceCriteria: '' },
    relations: [], events: [], task_states: [], effective_base_branch: 'feat/x', decisions: [],
  }

  it('改标题走 patchCard，只发 title 一个字段', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '改标题' }))
    fireEvent.change(screen.getByDisplayValue('原标题'), { target: { value: '改过的标题' } })
    fireEvent.click(screen.getByRole('button', { name: '保存标题' }))
    await waitFor(() => expect(vi.mocked(ledger.patchCard)).toHaveBeenCalledWith('B20', { title: '改过的标题' }))
  })

  it('写验收判据走 patchCard，只发 acceptance_criteria', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑判据' }))
    fireEvent.change(screen.getByPlaceholderText('这张卡怎样算做完了…'), { target: { value: '全量测试绿' } })
    fireEvent.click(screen.getByRole('button', { name: '保存判据' }))
    await waitFor(() => expect(vi.mocked(ledger.patchCard)).toHaveBeenCalledWith('B20', { acceptance_criteria: '全量测试绿' }))
  })

  it('挂附件要同时给 kind 与 path', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.change(await screen.findByPlaceholderText('docs/superpowers/plans/…'), {
      target: { value: 'docs/superpowers/plans/x.md' },
    })
    fireEvent.click(screen.getByRole('button', { name: '挂上' }))
    await waitFor(() => expect(vi.mocked(ledger.attachFile)).toHaveBeenCalledWith('B20', 'plan', 'docs/superpowers/plans/x.md'))
  })

  it('已挂的附件能摘掉', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      ...detail,
      card: { ...detail.card, Attachments: [{ Kind: 'plan', Path: 'docs/p.md' }] },
    } as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '摘掉 docs/p.md' }))
    await waitFor(() => expect(vi.mocked(ledger.detachFile)).toHaveBeenCalledWith('B20', 'docs/p.md'))
  })

  it('基线分支只读且注明不可改', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('feat/x')).toBeInTheDocument()
    expect(screen.getByText(/建卡时定，不可改/)).toBeInTheDocument()
  })
})

describe('抽屉里的环节动作', () => {
  it('派发审阅点一次即置灰并提示看 Timeline', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B180', Title: '待审卡', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    const run = vi.mocked(ledger.runCardStep).mockResolvedValue({ ok: true })
    render(<CardDrawer id="B180" onClose={() => {}} onOpenCard={() => {}} />)
    const button = await screen.findByRole('button', { name: /派发审阅/ })
    fireEvent.click(button)
    await waitFor(() => expect(run).toHaveBeenCalledWith('B180', 'review'))
    expect(await screen.findByText(/进展见下方 Timeline/)).toBeInTheDocument()
  })

  it('409 原地显示冲突原因，不吞掉后端文案', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B181', Title: '待审卡', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    vi.mocked(ledger.runCardStep).mockRejectedValue(new Error('B181 的 review 环节正在运行'))
    render(<CardDrawer id="B181" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /合入集成分支/ }))
    expect(await screen.findByText(/正在运行/)).toBeInTheDocument()
  })

  it('不提供实现类按钮——它要挂 plan 文件，浏览器里没有', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B182', Title: '卡', Status: '待办', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B182" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('卡')
    // 直接写字面量：这条断言的全部意义就是「界面上不存在这个名字的按钮」，
    // 把名字拆开只是为了躲某条 grep，会让后来人看不懂它在断言什么
    expect(screen.queryByRole('button', { name: /派发实现/ })).not.toBeInTheDocument()
  })
})

describe('审阅裁决的呈现与等人标记的撤回', () => {
  // 2026-08-20 真机看到：裁决正文整个塞在 payload.raw 这个 JSON 字符串里，
  // 走 eventSummary 的兜底会渲染成转义两遍的裸串——这个看板上最该一眼看清的
  // 东西，反而最难读。
  it('review_verdict 要渲染成裁决卡片，不是一坨裸 JSON', async () => {
    const ledger = await import('../../api/ledger')
    const raw = JSON.stringify({
      verdict: 'fail',
      findings: [
        { severity: 'major', summary: '验收项全部未实现', file: 'greet.go' },
        { severity: 'minor', summary: '缺文件头注释' },
      ],
      notes: '未跑 UI 点击：仓库无对应页面，标为未验证。',
    })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B1.1', Title: '抽屉环节动作按钮', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
      relations: [], task_states: [], effective_base_branch: '', decisions: [],
      events: [{ seq: 43, card_id: 'B1.1', type: 'review_verdict', actor: 'node:review', payload: { node: 'review', pass: false, raw }, created_at: '' }],
    } as never)
    render(<CardDrawer id="B1.1" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('审阅未过')).toBeInTheDocument()
    expect(screen.getByText('验收项全部未实现')).toBeInTheDocument()
    expect(screen.getByText('greet.go')).toBeInTheDocument()
    expect(screen.getByText('major')).toBeInTheDocument()
    expect(screen.getByText(/标为未验证/)).toBeInTheDocument()
    // 裸 JSON 不该再出现在界面上
    expect(screen.queryByText(/\{"node"/)).not.toBeInTheDocument()
  })

  it('裁决报文解析不动时退回显示原文，不能把裁决吞掉', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B1.1', Title: 'x', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
      relations: [], task_states: [], effective_base_branch: '', decisions: [],
      events: [{ seq: 43, card_id: 'B1.1', type: 'review_verdict', actor: 'node:review', payload: { node: 'review', pass: false, raw: '这不是 JSON' }, created_at: '' }],
    } as never)
    render(<CardDrawer id="B1.1" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/这不是 JSON/)).toBeInTheDocument()
  })

  // 撤回入口此前只有 CLI 一条路：红旗挂在抽屉上、撤它却要回命令行。
  it('等人标记要能在抽屉里直接撤回', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B1.1', Title: 'x', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [],
      needs: '审阅未取到报文',
    } as never)
    render(<CardDrawer id="B1.1" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('审阅未取到报文')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '已处理' }))
    await waitFor(() => expect(vi.mocked(ledger.clearCardNeeds)).toHaveBeenCalledWith('B1.1'))
  })
})
