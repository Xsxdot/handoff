import { describe, expect, it } from 'vitest'
import type { Task } from '../../api/types'
import { countsForMachine, countsForProject } from './counts'

function t(over: Partial<Task>): Task {
  return {
    id: 'i', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '', name: '',
    executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '',
    machine: '', project_id: 'p1', ...over,
  }
}

describe('聚合计数', () => {
  it('目录数 = 该项目所有机器下的 workspace 总数', () => {
    const project = { project_id: 'p1', origin_url: '', name: 'alpha', locations: [
      { machine: '', name: 'alpha', path: '/a', probe_error: '', workspaces: [
        { path: '/a', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' },
        { path: '/a-wt', branch: 'feat', head: 'def', is_main: false, managed: true, created_at: '' } ] },
      { machine: 'devbox', name: 'alpha', path: '/srv/a', probe_error: '', workspaces: [
        { path: '/srv/a', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' } ] },
    ] }
    expect(countsForProject([], project).dirs).toBe(3)
    expect(countsForMachine([], project, 'devbox').dirs).toBe(1)
  })

  it('运行数只数 running；待处理数 = waiting_answer + waiting_review', () => {
    const tasks = [
      t({ project_id: 'p1', machine: '', state: 'running' }),
      t({ project_id: 'p1', machine: 'devbox', state: 'running' }),
      t({ project_id: 'p1', machine: '', state: 'waiting_answer' }),
      t({ project_id: 'p1', machine: '', state: 'waiting_review' }),
      t({ project_id: 'p1', machine: '', state: 'completed' }),
      t({ project_id: 'p2', machine: '', state: 'running' }),   // 别的项目，不该被算进来
    ]
    const project = { project_id: 'p1', origin_url: '', name: 'alpha', locations: [] }
    expect(countsForProject(tasks, project)).toMatchObject({ running: 2, pending: 2 })
    expect(countsForMachine(tasks, project, '')).toMatchObject({ running: 1, pending: 2 })
  })

  it('计数从任务流算，探测失败的 location 不影响运行/待处理数', () => {
    const project = { project_id: 'p1', origin_url: '', name: 'alpha', locations: [
      { machine: '', name: 'alpha', path: '/a', probe_error: '目录不存在', workspaces: [] } ] }
    const tasks = [t({ project_id: 'p1', machine: '', state: 'running' })]
    expect(countsForProject(tasks, project)).toMatchObject({ dirs: 0, running: 1 })
  })
})
