// 看板卡片的呈现契约测试。
//
// 第一条用例钉的是 B75 的既有缺陷：waiting_answer 曾在同一张卡上并排渲染
// 两个一模一样的红徽章（waitingAnswer 那个 + stateLabel 那个）。它是回归
// 防线，删不得。
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Task } from '../../api/types'
import { TaskCard } from './BoardPage'

function task(over: Partial<Task>): Task {
  return {
    id: 'T1', target: '', repo_path: '', branch: 'feat/x', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '2026-08-12T00:00:00Z',
    name: '重构工单通道', executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '', machine: '', project_id: 'p1', ...over,
  }
}

function renderCard(state: string) {
  return render(<TaskCard task={task({ state })} projectName="handoff" onOpen={vi.fn()} />)
}

describe('TaskCard', () => {
  // B75 的回归防线：这条红了说明重复徽章又回来了
  it('waiting_answer 的「等你答复」只出现一次', () => {
    renderCard('waiting_answer')
    expect(screen.getAllByText('等你答复')).toHaveLength(1)
  })

  it('两个干预态都带卡片级干预标记', () => {
    const { container: a } = renderCard('waiting_answer')
    expect(a.firstChild).toHaveClass('border-state-intervention/45')
    const { container: r } = renderCard('waiting_review')
    expect(r.firstChild).toHaveClass('border-state-intervention/45')
  })

  it('failed 保持红色区分，且不带干预标记', () => {
    const { container } = renderCard('failed')
    expect(container.firstChild).toHaveClass('border-destructive/40')
    expect(container.firstChild).not.toHaveClass('border-state-intervention/45')
  })

  it('running / completed 两种标记都不带', () => {
    const { container: a } = renderCard('running')
    expect(a.firstChild).not.toHaveClass('border-state-intervention/45')
    expect(a.firstChild).not.toHaveClass('border-destructive/40')
    const { container: c } = renderCard('completed')
    expect(c.firstChild).not.toHaveClass('border-state-intervention/45')
  })

  it('状态用圆点 + 文字呈现，文案与状态对得上', () => {
    renderCard('waiting_review')
    expect(screen.getByText('Review').className).toContain('text-state-intervention-text')
  })

  it('未归属项目显示「未归属」，本机显示「本机」', () => {
    render(<TaskCard task={task({ state: 'running' })} projectName="" onOpen={vi.fn()} />)
    expect(screen.getByText('未归属')).toBeInTheDocument()
    expect(screen.getByText('本机')).toBeInTheDocument()
  })
})
