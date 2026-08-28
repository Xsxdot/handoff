import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { Card, CardDetail } from '../../api/ledger'
import type { Task } from '../../api/types'
import { CardDrawer } from './CardDrawer'

// card 造一张字段齐全、**大小写与线格式一致**的卡。
//
// why 存在：这些夹具原先手写 `{ ID, Title, Status, Attachments,
// AcceptanceCriteria }`——Go 结构体的字段名，不是 agentd 实际吐的 JSON。
// 抽屉里的 value() 有一层 PascalCase 兜底，所以断言照样绿，夹具却在验证一个
// 不存在的世界；线格式的真相以 src/api/testdata/CardDetail.json 为准，是小写
// snake_case。这里一次性钉住，顺带补齐 Card 必填字段（缺一个 tsc 就红）。
function card(over: Partial<Card> = {}): Card {
  return {
    id: 'B1', title: '卡', status: '进行中', priority: '中', project: 'handoff',
    parent: '', workflow: 'triage', workflow_version: 1,
    attachments: [], acceptance_criteria: '',
    created_at: '', updated_at: '', ...over,
  }
}

// task 造一个字段齐全的线格式 Task：缺一个必填字段 tsc 就红。
function task(over: Partial<Task> = {}): Task {
  return {
    id: 'task-x', target: 'local', repo_path: '/repo/handoff', branch: '',
    plan_path: '', plan_summary: '', executor_session: '', state: 'running',
    created_at: '', updated_at: '', name: '', executor: '', model: '',
    work_dir: '', worktree_managed: false, base_commit: '', base_ahead: 0,
    repo_dirty_count: 0, repo_dirty_files: '', done_note: '', machine: '', project_id: '', ...over,
  }
}

// detailWithRows 造一张带挂账行的卡详情。大小写与线格式一致：
// task_states 是账本的 Go 风格 PascalCase（api/ledger.ts:58-64），
// 任务流是 snake_case（api/types.ts:15-42）——这条接缝正是被测对象。
const detailWithRows = (rows: CardDetail['task_states']): CardDetail => ({
  card: card({ id: 'B30', title: '在跑的卡', status: '进行中' }),
  relations: [], events: [], effective_base_branch: '', decisions: [], needs: '',
  children: [], task_states: rows,
})

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  fetchTaskDetail: vi.fn(),
  replyTicket: vi.fn(),
}))

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCardDetail: vi.fn().mockResolvedValue({
    card: card({ id: 'B147', title: '承载卡', status: '进行中' }),
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
  clearCardNeeds: vi.fn().mockResolvedValue({ ok: true }),
}))

vi.mock('../../api/scheduling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/scheduling')>()),
  getCoordinatorStatus: vi.fn().mockResolvedValue({ bound: false, attach_active: false, attach: null }),
}))

describe('抽屉一处看', () => {
  it('承载卡显示并入区成员，关系区不重复「承载着」', async () => {
    render(<CardDrawer id="B147" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/并入本卡/)).toBeInTheDocument()
    expect(screen.getByText('B144')).toBeInTheDocument()
    expect(screen.queryByText('承载着')).not.toBeInTheDocument()
  })
})

describe('抽屉协调者接缝', () => {
  it('使用真实 CoordinatorPanel 的未绑定入口，并移除旧节点执行入口', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: card({ id: 'B282', title: '协调者入口', status: '进行中' }),
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B282" onClose={() => {}} onOpenCard={() => {}} nodes={[{ name: '进行中', dispatch: true }] as never} />)
    expect(await screen.findByText('未绑定')).toBeVisible()
    expect(screen.getByRole('button', { name: '▶ 拉起协调者' })).toBeVisible()
    expect(screen.queryByRole('button', { name: /跑「/ })).not.toBeInTheDocument()
  })
})

describe('抽屉里的裁决', () => {
  it('挂卡的请示要出正文、候选项与答复入口，不能只在 timeline 里剩一行', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: card({ id: 'B8', title: 'WS 被 503', status: '进行中' }),
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
      card: card({ id: 'B8', title: 'WS 被 503', status: '进行中' }),
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
      card: card({ id: 'B3', title: '镜像断链', status: '待合并' }),
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
      card: card({ id: 'B9', title: '普通卡', status: '待办' }),
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
      card: card({ id: 'B10', title: '合并卡', status: '进行中' }),
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
      card: card({ id: 'B156', title: '父卡', status: '进行中' }),
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
      card: card({ id: 'B160', title: '叶子卡', status: '待办' }),
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
      card: card({ id: 'B170', title: '待验卡', status: '已完成', acceptance_criteria: '判据：全绿' }),
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
      card: card({ id: 'B171', title: '进行中的卡', status: '进行中' }),
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
  // 显式标 CardDetail：不标的话 relations/events 会被推成 never[]，
  // 任何一处想在 spread 之上改字段都得靠 `as never` 把类型检查关掉
  const detail: CardDetail = {
    card: card({ id: 'B20', title: '原标题' }),
    relations: [], events: [], task_states: [], effective_base_branch: 'feat/x', decisions: [], needs: '',
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
      card: { ...detail.card, attachments: [{ kind: 'plan', path: 'docs/p.md' }] },
    })
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '摘掉 docs/p.md' }))
    await waitFor(() => expect(vi.mocked(ledger.detachFile)).toHaveBeenCalledWith('B20', 'docs/p.md'))
  })

  it('基线分支显示继承态并提供编辑入口', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('继承 feat/x')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '编辑基线' })).toBeInTheDocument()
  })

  it('基线三态分别显示自设、继承和未设置', async () => {
    const ledger = await import('../../api/ledger')
    const cases = [
      { id: 'B21', own: 'cards/own', effective: 'cards/own', label: '自设 cards/own' },
      { id: 'B22', own: '', effective: 'cards/inherited', label: '继承 cards/inherited' },
      { id: 'B23', own: '', effective: '', label: '未设置/回落项目主线' },
    ]
    for (const current of cases) {
      vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
        ...detail, card: card({ id: current.id, base_branch: current.own }), effective_base_branch: current.effective,
      } as never)
      const view = render(<CardDrawer id={current.id} onClose={() => {}} onOpenCard={() => {}} />)
      expect(await screen.findByText(current.label)).toBeInTheDocument()
      view.unmount()
    }
  })

  it('保存与清除基线发送精确 payload，409 保留旧值并显示原文', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    const patch = vi.mocked(ledger.patchCard)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑基线' }))
    fireEvent.change(screen.getByLabelText('基线分支'), { target: { value: 'cards/new-base' } })
    fireEvent.click(screen.getByRole('button', { name: '保存基线' }))
    await waitFor(() => expect(patch).toHaveBeenCalledWith('B20', { base_branch: 'cards/new-base' }))

    patch.mockRejectedValueOnce(new Error('B20 已在 cards/first 首次派发，基线已冻结'))
    fireEvent.click(screen.getByRole('button', { name: '编辑基线' }))
    fireEvent.change(screen.getByLabelText('基线分支'), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: '保存基线' }))
    expect(await screen.findByText(/cards\/first 首次派发/)).toBeInTheDocument()
    expect(screen.getByLabelText('基线分支')).toHaveValue('')
  })
})

describe('抽屉里的工单入口', () => {
  const withTask = {
    card: card({ id: 'B30', title: '在跑的卡', status: '进行中' }),
    relations: [], events: [], effective_base_branch: '', decisions: [],
    task_states: [{ Target: 'linux-01', TaskID: 'task-abc', Purpose: 'implement', LastType: 'question', LastSeq: 9 }],
  }

  it('展开关联执行行能看到该 task 的挂起工单', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(withTask as never)
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-abc', state: 'waiting_answer' },
      tickets: [{ id: 'tk-1', kind: 'ask', request: '这里要用哪个基线？' }],
      events: [],
    } as never)
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /task-abc/ }))
    expect(await screen.findByText('这里要用哪个基线？')).toBeInTheDocument()
  })

  it('在抽屉里作答走 replyTicket，不用跳去任务页', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(withTask as never)
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-abc', state: 'waiting_answer' },
      tickets: [{ id: 'tk-1', kind: 'ask', request: '这里要用哪个基线？' }],
      events: [],
    } as never)
    vi.mocked(client.replyTicket).mockResolvedValue({ ok: true } as never)
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /task-abc/ }))
    const box = await screen.findByPlaceholderText('输入你的回答…')
    fireEvent.change(box, { target: { value: 'feat/x' } })
    fireEvent.click(screen.getByRole('button', { name: /提交|回答|发送/ }))
    await waitFor(() => expect(vi.mocked(client.replyTicket)).toHaveBeenCalledWith(
      'task-abc', expect.objectContaining({ ticket_id: 'tk-1' }),
    ))
  })

  it('没有挂起工单时说清楚，不留一片空白', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(withTask as never)
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-abc', state: 'running' }, tickets: [], events: [],
    } as never)
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /task-abc/ }))
    expect(await screen.findByText(/没有等待处理的工单/)).toBeInTheDocument()
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
      card: card({ id: 'B1.1', title: '抽屉环节动作按钮', status: '待审阅' }),
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
      card: card({ id: 'B1.1', title: 'x', status: '待审阅' }),
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
      card: card({ id: 'B1.1', title: 'x', status: '待审阅' }),
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [],
      needs: '审阅未取到报文',
    } as never)
    render(<CardDrawer id="B1.1" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('审阅未取到报文')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '已处理' }))
    await waitFor(() => expect(vi.mocked(ledger.clearCardNeeds)).toHaveBeenCalledWith('B1.1'))
  })
})

describe('抽屉里的关联执行实况', () => {
  it('行上显示任务流的真实 state，而不是最后一条镜像事件的类型', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-run', Purpose: 'implement', LastType: 'turn_end', LastSeq: 12 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-run', state: 'running' })]}
      />,
    )
    // 卡头部的状态 chip 也叫「进行中」，断言必须收在任务行里
    const row = await screen.findByRole('button', { name: /^task-run/ })
    expect(within(row).getByText('进行中')).toBeInTheDocument()
    expect(within(row).queryByText('turn_end')).not.toBeInTheDocument()
  })

  it('关键回归：state=running 而最后一条镜像是 turn_failed 时，行上仍显示进行中', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-run', Purpose: 'implement', LastType: 'turn_failed', LastSeq: 13 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-run', state: 'running' })]}
      />,
    )
    const row = await screen.findByRole('button', { name: /^task-run/ })
    // turn_failed 可 continue 不是终态；把它当状态渲染出来 = 和看板打架
    expect(within(row).queryByText(/turn_failed/)).not.toBeInTheDocument()
    expect(within(row).getByText('进行中')).toBeInTheDocument()
  })

  it('任务已不在任务流里时如实显示「实况未知」，把 LastType 当线索列出，不冒充状态', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-gone', Purpose: 'implement', LastType: 'question', LastSeq: 3 },
    ]))
    // tasks=[]：流已接入但查无此任务（归档清出流是真实情形）
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} tasks={[]} />)
    const row = await screen.findByRole('button', { name: /^task-gone/ })
    expect(within(row).getByText('实况未知 · 最后事件 question')).toBeInTheDocument()
    // 六个已知状态标签一个都不许出现：不知道就说不知道
    for (const label of ['等待执行', '进行中', '等你答复', 'Review', '已完成', '失败']) {
      expect(within(row).queryByText(label)).not.toBeInTheDocument()
    }
  })

  it('连最后事件类型都没有时只说「实况未知」，不再沿用旧的「未知」占位', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-fresh', Purpose: 'plan', LastType: '', LastSeq: 0 },
    ]))
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} tasks={[]} />)
    const row = await screen.findByRole('button', { name: /^task-fresh/ })
    // getByText 默认整串精确匹配，不会误中带线索的长串
    expect(within(row).getByText('实况未知')).toBeInTheDocument()
    expect(within(row).queryByText(/^最后事件/)).not.toBeInTheDocument()
  })
})

describe('抽屉里的关联执行排序与计数', () => {
  it('运行中的行排在前面，其余按最后事件序号倒序', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-old-done', Purpose: 'implement', LastType: 'turn_end', LastSeq: 20 },
      { Target: 'linux-01', TaskID: 'task-live', Purpose: 'implement', LastType: 'question', LastSeq: 5 },
      { Target: 'local', TaskID: 'task-new-done', Purpose: 'review', LastType: 'review_verdict', LastSeq: 40 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[
          task({ id: 'task-old-done', state: 'completed' }),
          task({ id: 'task-live', state: 'running' }),
          task({ id: 'task-new-done', state: 'failed' }),
        ]}
      />,
    )
    const section = (await screen.findByText(/关联执行/)).closest('section') as HTMLElement
    const names = within(section).getAllByRole('button').map((element) => element.textContent ?? '')
    expect(names[0]).toMatch(/^task-live/)
    expect(names[1]).toMatch(/^task-new-done/) // 已结束里 LastSeq 40 的在前
    expect(names[2]).toMatch(/^task-old-done/)
  })

  it('区块标题的在跑计数与任务流一致（waiting_review 也算在跑，completed 不算）', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-a', Purpose: 'implement', LastType: 'question', LastSeq: 1 },
      { Target: 'local', TaskID: 'task-b', Purpose: 'review', LastType: 'review_requested', LastSeq: 2 },
      { Target: 'local', TaskID: 'task-c', Purpose: 'plan', LastType: 'turn_end', LastSeq: 3 },
    ]))
    render(
      <CardDrawer
        id="B30" onClose={() => {}} onOpenCard={() => {}}
        tasks={[
          task({ id: 'task-a', state: 'waiting_answer' }),
          task({ id: 'task-b', state: 'waiting_review' }),
          task({ id: 'task-c', state: 'completed' }),
        ]}
      />,
    )
    expect(await screen.findByText('关联执行 · 2 个在跑 / 共 3 个')).toBeInTheDocument()
  })

  it('任务流未接入时标题不带计数——不知道就说不知道，不谎报「0 个在跑」', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-x', Purpose: 'plan', LastType: '', LastSeq: 0 },
    ]))
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('关联执行（task）')).toBeInTheDocument()
  })
})

describe('抽屉里的任务跳转', () => {
  it('点 ↗ 发起跳转回调，且不触发展开', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-j', Purpose: 'implement', LastType: 'turn_end', LastSeq: 7 },
    ]))
    const onJump = vi.fn()
    render(
      <CardDrawer
        id="B31" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-j' })]} onJumpToTask={onJump}
      />,
    )
    fireEvent.click(await screen.findByRole('button', { name: '跳到 task-j' }))
    expect(onJump).toHaveBeenCalledTimes(1)
    expect(onJump).toHaveBeenCalledWith('task-j')
    // 展开没被误触：aria-expanded 还是 false，工单加载占位也没出现
    expect(screen.getByRole('button', { name: /^task-j/ })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('正在读取工单…')).not.toBeInTheDocument()
  })

  it('没给跳转回调时不画 ↗ 按钮', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'local', TaskID: 'task-nojump', Purpose: 'plan', LastType: '', LastSeq: 0 },
    ]))
    render(<CardDrawer id="B34" onClose={() => {}} onOpenCard={() => {}} tasks={[]} />)
    await screen.findByRole('button', { name: /^task-nojump/ })
    expect(screen.queryByRole('button', { name: /跳到/ })).not.toBeInTheDocument()
  })

  it('整行点击仍然展开工单面板——跳转按钮不抢走既有入口', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detailWithRows([
      { Target: 'linux-01', TaskID: 'task-tk', Purpose: 'implement', LastType: 'question', LastSeq: 9 },
    ]))
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-tk', state: 'waiting_answer' },
      tickets: [{ id: 'tk-9', kind: 'ask', request: '这里要用哪个基线？' }],
      events: [],
    } as never)
    render(
      <CardDrawer
        id="B33" onClose={() => {}} onOpenCard={() => {}}
        tasks={[task({ id: 'task-tk', state: 'waiting_answer' })]} onJumpToTask={vi.fn()}
      />,
    )
    fireEvent.click(await screen.findByRole('button', { name: /^task-tk/ }))
    expect(await screen.findByText('这里要用哪个基线？')).toBeInTheDocument()
  })
})

describe('抽屉头部状态唯一化（B287）', () => {
  // 断言圈定在头部（data-testid）：抽屉正文里的看板列条/timeline 也可能出现
  // 同名状态词，全文计数会误伤。夹具复用本文件 card() 与 fetchCardDetail mock。
  it('无节点标签时头部状态只渲染一枚深色 chip', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: card({ id: 'B287', title: '状态唯一化', status: '进行中' }),
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B287" onClose={() => {}} onOpenCard={() => {}} />)
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    const header = within(drawer).getByTestId('card-drawer-header')
    expect(within(header).getAllByText('进行中')).toHaveLength(1)
    expect(within(header).getAllByText('进行中')[0]!.className).toContain('bg-slate-900')
  })

  it('多节点列时 chip 显示节点标签，头部同样只有一枚', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: card({ id: 'B287b', title: '多节点列', status: '进行中' }),
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B287b" onClose={() => {}} onOpenCard={() => {}} nodes={[{ name: '起草' }, { name: '进行中' }] as never} />)
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    const header = within(drawer).getByTestId('card-drawer-header')
    expect(within(header).getAllByText('进行中')).toHaveLength(1)
    expect(within(header).queryAllByText('起草')).toHaveLength(0)
  })
})
