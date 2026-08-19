import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Overlay } from './Overlay'
import { BoardOverlay } from './BoardOverlay'
import type { ProjectTreeResp, Task } from '../../api/types'
import type { PollState } from '../data/usePoll'

function pollState(tasks: Task[]): PollState<Task[]> {
  return {
    data: tasks,
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  } as unknown as PollState<Task[]>
}

const task = {
  id: 'T1',
  name: '重构工单通道',
  state: 'waiting_answer',
  executor: 'opencode',
  branch: 'feat/x',
  machine: '',
  project_id: 'P1',
  work_dir: '/w/b2-b3',
  updated_at: '2026-08-12T00:00:00Z',
  plan_summary: '',
} as unknown as Task

const tree = {
  projects: [
    {
      project_id: 'P1',
      name: 'handoff',
      locations: [
        {
          machine: '',
          name: 'handoff',
          path: '/w',
          probe_error: '',
          workspaces: [{ path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'abc', is_main: false, managed: false, created_at: '' }],
        },
      ],
    },
  ],
  machines: [],
  unowned: [],
} as unknown as ProjectTreeResp

describe('Overlay', () => {
  it('渲染标题与内容，并有关闭按钮', () => {
    render(
      <Overlay title="任务看板" onClose={vi.fn()}>
        <p>内容</p>
      </Overlay>,
    )
    expect(screen.getByRole('dialog', { name: '任务看板' })).toBeInTheDocument()
    expect(screen.getByText('内容')).toBeInTheDocument()
  })

  it('Esc 关闭', () => {
    const onClose = vi.fn()
    render(<Overlay title="x" onClose={onClose}>内容</Overlay>)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('点遮罩关闭，点内容不关', () => {
    const onClose = vi.fn()
    render(<Overlay title="x" onClose={onClose}>内容</Overlay>)
    fireEvent.click(screen.getByText('内容'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('overlay-backdrop'))
    expect(onClose).toHaveBeenCalled()
  })

  it('点关闭按钮关闭', () => {
    const onClose = vi.fn()
    render(<Overlay title="x" onClose={onClose}>内容</Overlay>)
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('tall 时面板高度固定，不随内容伸缩', () => {
    render(<Overlay title="x" onClose={() => {}} tall><p>短内容</p></Overlay>)
    const panel = screen.getByRole('dialog')
    expect(panel.className).toMatch(/h-\[70vh\]/)
  })

  it('不传 tall 时保持贴合内容（工单弹层的既有行为）', () => {
    render(<Overlay title="x" onClose={() => {}}><p>短内容</p></Overlay>)
    expect(screen.getByRole('dialog').className).not.toMatch(/h-\[70vh\]/)
  })
})

describe('BoardOverlay', () => {
  it('四列都在，卡片按状态落列', () => {
    render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={vi.fn()} onClose={vi.fn()} />,
    )
    for (const label of ['等待执行', '进行中', 'Review', '完成']) {
      expect(screen.getByRole('heading', { name: label })).toBeInTheDocument()
    }
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    // waiting_answer 卡片里「等你答复」会出现两次（干预态标记 + 状态徽标），
    // 断言至少出现一次即可，个数不是本用例关心的。
    expect(screen.getAllByText('等你答复').length).toBeGreaterThan(0)
  })

  it('点卡片关闭弹层并带上任务所在目录', () => {
    const onOpenTask = vi.fn()
    const onClose = vi.fn()
    render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={onOpenTask} onClose={onClose} />,
    )
    fireEvent.click(screen.getByText('重构工单通道'))
    expect(onOpenTask).toHaveBeenCalledWith(expect.objectContaining({ key: '/w/b2-b3' }), 'T1')
    expect(onClose).toHaveBeenCalled()
  })

  it('筛选栏在弹层内，且每次打开都是空筛选', () => {
    const { unmount } = render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={vi.fn()} onClose={vi.fn()} />,
    )
    fireEvent.change(screen.getByPlaceholderText(/搜索/), { target: { value: '不存在的任务' } })
    expect(screen.queryByText('重构工单通道')).not.toBeInTheDocument()
    unmount()

    render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={vi.fn()} onClose={vi.fn()} />,
    )
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
  })
})
