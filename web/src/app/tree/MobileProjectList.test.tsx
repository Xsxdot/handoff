// MobileProjectList 的组件契约测试（S2/B426）：
// apphead（项目 + ＋添加项目）、项目卡流（图标=名称前两字符+底色块、名称、›）、
// 第二行位置 chips（机器名 · N 活跃 / 离线，活跃数=位置上下沉睡任务口径）。
// 纯投影呈现测试：不发请求，数据经 props 注入（与 Shell 持有源同一形状）。
import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MobileProjectList } from './MobileProjectList'
import type { MachineStatus, ProjectNode, Task } from '../../api/types'

// project：本机（两个工作树）+ linux-01（一个工作树）两处可用位，mac-02 探测失败。
const project: ProjectNode = {
  project_id: 'p1',
  origin_url: '',
  name: 'handoff',
  locations: [
    {
      machine: '',
      name: 'handoff',
      path: '/r/handoff',
      workspaces: [
        { path: '/r/handoff', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' },
        { path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'abc', is_main: false, managed: true, created_at: '' },
      ],
      probe_error: '',
    },
    {
      machine: 'linux-01',
      name: 'handoff',
      path: '/srv/handoff',
      workspaces: [
        { path: '/srv/handoff', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' },
      ],
      probe_error: '',
    },
    {
      machine: 'mac-02',
      name: 'handoff',
      path: '/Users/x/handoff',
      workspaces: [],
      probe_error: 'CONTEXT.md「项目位置不可用」',
    },
  ],
}

function mkTask(id: string, machine: string, workDir: string, state: string, projectId = 'p1'): Task {
  return {
    id, target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state, created_at: '', updated_at: '', name: `任务${id}`,
    executor: '', model: '', work_dir: workDir, worktree_managed: true,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '', machine, project_id: projectId,
  }
}

// t1/t2 在本机两个工作树上（running + waiting_review）→ 本机 2 活跃；
// t3 在 linux-01 但已 done → 不计活跃；t4 是别的项目的 → 不计入本项目。
const tasks = [
  mkTask('t1', '', '/r/handoff', 'running'),
  mkTask('t2', '', '/w/b2-b3', 'waiting_review'),
  mkTask('t3', 'linux-01', '/srv/handoff', 'done'),
  mkTask('t4', '', '/r/handoff', 'running', 'p-other'),
]

const machines: MachineStatus[] = []

function renderList(overrides: Partial<Parameters<typeof MobileProjectList>[0]> = {}) {
  const props = {
    projects: [project],
    machines,
    tasks,
    onOpenProject: vi.fn(),
    onAddProject: vi.fn(),
    ...overrides,
  }
  render(<MobileProjectList {...props} />)
  return props
}

describe('MobileProjectList apphead', () => {
  it('「项目」标题 + ＋添加项目钮；点添加走既有向导通道回调', () => {
    const props = renderList()
    expect(screen.getByText('项目')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('mobile-add-project'))
    expect(props.onAddProject).toHaveBeenCalledTimes(1)
  })
})

describe('MobileProjectList 项目卡流', () => {
  it('每项目一卡：图标=名称前两字符+哈希底色块、名称、›；点卡回调 onOpenProject(project_id)', () => {
    const props = renderList()
    const card = screen.getByTestId('mobile-project-card')
    // 图标 = 名称前两字符（plan 权威口径；原型 mock 图标 'ho' 与其项目名不自洽，不跟）
    const icon = within(card).getByText('ha')
    expect(icon.getAttribute('style')).toContain('var(--project-') // 哈希取色经 CSS 变量
    expect(within(card).getByText('handoff')).toBeInTheDocument()
    expect(within(card).getByText('›')).toBeInTheDocument()
    fireEvent.click(card)
    expect(props.onOpenProject).toHaveBeenCalledWith('p1')
  })

  it('位置 chips：本机 · 2 活跃（running/waiting_answer/waiting_review 口径）；linux-01 零活跃裸机器名（无「0 活跃」）；mac-02 探测失败标离线', () => {
    renderList()
    const card = screen.getByTestId('mobile-project-card')
    const locs = within(card).getAllByTestId('mobile-project-loc')
    expect(locs).toHaveLength(3)
    expect(locs[0].textContent).toBe('本机 · 2 活跃')
    // 原型 mobile-projects 卡三：零活跃位置只显机器名，不摆「0 活跃」
    expect(locs[1].textContent).toBe('linux-01')
    expect(locs[2].textContent).toBe('mac-02 · 离线')
  })

  it('离线判定二源：MachineStatus.ok=false 也标离线（locationProblem 同源判据，不只看 probe_error）', () => {
    renderList({
      machines: [{ name: 'linux-01', ok: false, fetched_at: '', error: 'dial tcp: connect refused' }],
    })
    const locs = within(screen.getByTestId('mobile-project-card')).getAllByTestId('mobile-project-loc')
    expect(locs[1].textContent).toBe('linux-01 · 离线')
  })

  it('多项目逐卡渲染；空项目列表给空态文案不白屏', () => {
    renderList({
      projects: [
        project,
        { project_id: 'p2', origin_url: '', name: 'gokit', locations: [{ machine: '', name: 'gokit', path: '/r/gokit', workspaces: [], probe_error: '' }] },
      ],
    })
    const cards = screen.getAllByTestId('mobile-project-card')
    expect(cards).toHaveLength(2)
    expect(within(cards[1]).getByText('gokit')).toBeInTheDocument()
    renderList({ projects: [] })
    expect(screen.getByText(/还没有登记任何项目/)).toBeInTheDocument()
  })
})
