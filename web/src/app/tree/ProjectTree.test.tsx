import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { ProjectTreeResp, Task } from '../../api/types'
import { EMPTY_FILTER, type BoardFilter } from '../board/filter'
import { ProjectTree } from './ProjectTree'

function task(over: Partial<Task>): Task {
  return {
    id: 'i', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '', name: '',
    executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    machine: '', project_id: 'p1', ...over,
  }
}

const twoWorkspacesTree: ProjectTreeResp = {
  projects: [
    {
      project_id: 'p1', origin_url: 'git@x:/a.git', name: 'alpha',
      locations: [
        {
          machine: '', name: 'alpha', path: '/a', probe_error: '',
          workspaces: [
            { path: '/a', branch: 'main', head: 'abc', is_main: true, managed: false },
            { path: '/a-wt', branch: 'feat', head: 'def', is_main: false, managed: true },
          ],
        },
      ],
    },
  ],
  unowned: [],
}

function renderTree(overrides: { tree?: ProjectTreeResp; tasks?: Task[]; filter?: BoardFilter } = {}) {
  const onFilterChange = vi.fn()
  const onOpenTask = vi.fn()
  render(
    <ProjectTree
      tree={overrides.tree ?? twoWorkspacesTree}
      tasks={overrides.tasks ?? []}
      filter={overrides.filter ?? EMPTY_FILTER}
      onFilterChange={onFilterChange}
      onOpenTask={onOpenTask}
    />,
  )
  return { onFilterChange, onOpenTask }
}

describe('ProjectTree', () => {
  it('层级是 项目 → 机器 → 目录 → 任务', () => {
    renderTree({
      tasks: [
        task({ id: 't1', project_id: 'p1', machine: '', work_dir: '/a', name: '重构登录' }),
        task({ id: 't2', project_id: 'p1', machine: '', work_dir: '/a-wt', name: '修 CI' }),
      ],
    })
    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(screen.getByText('本机')).toBeInTheDocument()
    expect(screen.getByText('/a')).toBeInTheDocument()
    expect(screen.getByText('/a-wt')).toBeInTheDocument()
    expect(screen.getByText('重构登录')).toBeInTheDocument()
    expect(screen.getByText('修 CI')).toBeInTheDocument()
  })

  it('不可达机器保持可见、标已断开、且不可展开', () => {
    const tree: ProjectTreeResp = {
      projects: [
        {
          project_id: 'p1', origin_url: '', name: 'alpha',
          locations: [{ machine: 'devbox', name: 'alpha', path: '/srv/a', probe_error: '', workspaces: [] }],
        },
      ],
      unowned: [],
      machines: [{ name: 'devbox', ok: false, fetched_at: '', error: 'dial tcp 10.0.0.8:7777: connect: connection refused' }],
    }
    renderTree({ tree })
    const row = screen.getByRole('button', { name: /devbox/ })
    expect(row).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByText('已断开')).toBeInTheDocument()
    expect(screen.getByText(/connection refused/)).toBeInTheDocument()
    fireEvent.click(row)
    expect(screen.queryByText('/srv/a')).toBeNull()
  })

  it('probe_error 只影响该 location，不炸整棵树', () => {
    const tree: ProjectTreeResp = {
      projects: [
        {
          project_id: 'p1', origin_url: '', name: 'alpha',
          locations: [{ machine: '', name: 'alpha', path: '/a', probe_error: '目录不存在', workspaces: [] }],
        },
        {
          project_id: 'p2', origin_url: '', name: 'beta',
          locations: [{ machine: '', name: 'beta', path: '/b', probe_error: '', workspaces: [] }],
        },
      ],
      unowned: [],
    }
    renderTree({ tree })
    expect(screen.getByText('目录不存在')).toBeInTheDocument()
    expect(screen.getByText('beta')).toBeInTheDocument()
    expect(screen.getByText('alpha')).toBeInTheDocument()
  })

  it('未归属任务挂在末尾的「未归属」分组，不被吞掉', () => {
    renderTree({ tasks: [task({ id: 'u1', project_id: '', machine: '', work_dir: '/x', name: '游离任务' })] })
    expect(screen.getByText('未归属')).toBeInTheDocument()
    expect(screen.getByText('游离任务')).toBeInTheDocument()
  })

  it('点项目/机器/目录写 filter，点任务导航', () => {
    const onFilterChange = vi.fn()
    const onOpenTask = vi.fn()
    const tree: ProjectTreeResp = {
      projects: [
        {
          project_id: 'p1', origin_url: '', name: 'alpha',
          locations: [{ machine: '', name: 'alpha', path: '/a', probe_error: '', workspaces: [{ path: '/a', branch: 'main', head: 'abc', is_main: true, managed: false }] }],
        },
      ],
      unowned: [],
    }
    const tasks = [task({ id: 't1', project_id: 'p1', machine: '', work_dir: '/a', name: '重构登录' })]

    const view = render(
      <ProjectTree tree={tree} tasks={tasks} filter={EMPTY_FILTER} onFilterChange={onFilterChange} onOpenTask={onOpenTask} />,
    )

    fireEvent.click(screen.getByRole('button', { name: /alpha/ }))
    expect(onFilterChange.mock.calls.at(-1)![0]).toMatchObject({ machine: null, workspace: null })
    expect(onFilterChange.mock.calls.at(-1)![0].projects).toEqual(new Set(['p1']))

    view.rerender(
      <ProjectTree tree={tree} tasks={tasks} filter={{ ...EMPTY_FILTER, projects: new Set(['p1']) }} onFilterChange={onFilterChange} onOpenTask={onOpenTask} />,
    )
    fireEvent.click(screen.getByRole('button', { name: /本机/ }))
    expect(onFilterChange.mock.calls.at(-1)![0].projects).toEqual(new Set(['p1']))
    expect(onFilterChange.mock.calls.at(-1)![0].machine).toBe('')
    expect(onFilterChange.mock.calls.at(-1)![0].workspace).toBeNull()

    view.rerender(
      <ProjectTree tree={tree} tasks={tasks} filter={{ ...EMPTY_FILTER, projects: new Set(['p1']), machine: '' }} onFilterChange={onFilterChange} onOpenTask={onOpenTask} />,
    )
    fireEvent.click(screen.getByRole('button', { name: /\/a/ }))
    expect(onFilterChange.mock.calls.at(-1)![0].machine).toBe('')
    expect(onFilterChange.mock.calls.at(-1)![0].workspace).toBe('/a')

    fireEvent.click(screen.getByRole('button', { name: /重构登录/ }))
    expect(onOpenTask).toHaveBeenCalledWith('t1')
  })

  it('多选时左栏不高亮单项，显示选中计数', () => {
    const tree: ProjectTreeResp = {
      projects: [
        { project_id: 'p1', origin_url: '', name: 'alpha', locations: [{ machine: '', name: 'alpha', path: '/a', probe_error: '', workspaces: [] }] },
        { project_id: 'p2', origin_url: '', name: 'beta', locations: [{ machine: '', name: 'beta', path: '/b', probe_error: '', workspaces: [] }] },
      ],
      unowned: [],
    }
    renderTree({ tree, filter: { ...EMPTY_FILTER, projects: new Set(['p1', 'p2']) } })
    expect(screen.getByText('已选 2 个项目')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /alpha/ })).not.toHaveAttribute('aria-current', 'true')
    expect(screen.getByRole('button', { name: /beta/ })).not.toHaveAttribute('aria-current', 'true')
  })

  it('单选项目时对应行有选中态', () => {
    renderTree({ filter: { ...EMPTY_FILTER, projects: new Set(['p1']) } })
    expect(screen.getByRole('button', { name: /alpha/ })).toHaveAttribute('aria-current', 'true')
  })
})
