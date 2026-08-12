import { describe, expect, it } from 'vitest'
import type { ProjectNode } from '../../api/types'
import type { Task } from '../../api/types'
import {
  EMPTY_FILTER, applyFilter,
  setPendingOnly, setProjects, setSearch,
} from './filter'

const tree: ProjectNode[] = [
  { project_id: 'p1', origin_url: 'git@x:/a.git', name: 'alpha', locations: [
      { machine: '', name: 'alpha', path: '/a', probe_error: '', workspaces: [] },
      { machine: 'devbox', name: 'alpha', path: '/srv/a', probe_error: '', workspaces: [] } ] },
  { project_id: 'p2', origin_url: 'git@x:/b.git', name: 'beta', locations: [
      { machine: '', name: 'beta', path: '/b', probe_error: '', workspaces: [] } ] },
]

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

describe('BoardFilter 写入规则', () => {
  it('顶部多选改 projects；若当前 machine 不再属于任一选中项目则一并清空', () => {
    const f = { ...EMPTY_FILTER, projects: new Set(['p1']), machine: 'devbox' }
    const next = setProjects(f, new Set(['p2']), tree) // p2 只有本机位置
    expect(next.machine).toBeNull()
  })

  it('顶部多选改 projects；machine 仍属于选中项目时保留', () => {
    const f = { ...EMPTY_FILTER, projects: new Set(['p1']), machine: 'devbox' }
    const next = setProjects(f, new Set(['p1', 'p2']), tree)
    expect(next.machine).toBe('devbox')
  })

  it('空集 = 全部，不是"一个都不选"', () => {
    const tasks = [t({ id: 'a', project_id: 'p1' }), t({ id: 'b', project_id: 'p2' })]
    expect(applyFilter(tasks, EMPTY_FILTER, tree)).toHaveLength(2)
  })
})

describe('applyFilter', () => {
  const tasks = [
    t({ id: 'a', project_id: 'p1', machine: '',       work_dir: '/a',    name: '重构登录', state: 'running' }),
    t({ id: 'b', project_id: 'p1', machine: 'devbox', work_dir: '/srv/a', name: '修 CI',   state: 'waiting_answer' }),
    t({ id: 'c', project_id: 'p2', machine: '',       work_dir: '/b',    name: '写文档',   state: 'completed' }),
    t({ id: 'd', project_id: '',   machine: '',       work_dir: '/x',    name: '游离任务', state: 'running' }),
  ]

  it('按项目筛', () => {
    const f = { ...EMPTY_FILTER, projects: new Set(['p1']) }
    expect(applyFilter(tasks, f, tree).map((x) => x.id)).toEqual(['a', 'b'])
  })

  it('按机器收窄（""=本机，不是"不筛"）', () => {
    const f = { ...EMPTY_FILTER, projects: new Set(['p1']), machine: '' }
    expect(applyFilter(tasks, f, tree).map((x) => x.id)).toEqual(['a'])
  })

  it('按工作树收窄', () => {
    const f = { ...EMPTY_FILTER, projects: new Set(['p1']), machine: 'devbox', workspace: '/srv/a' }
    expect(applyFilter(tasks, f, tree).map((x) => x.id)).toEqual(['b'])
  })

  it('只看待处理 = waiting_answer + waiting_review', () => {
    expect(applyFilter(tasks, setPendingOnly(EMPTY_FILTER, true), tree).map((x) => x.id)).toEqual(['b'])
  })

  it('搜索匹配任务名、项目名、执行者名', () => {
    expect(applyFilter(tasks, setSearch(EMPTY_FILTER, '登录'), tree).map((x) => x.id)).toEqual(['a'])
    expect(applyFilter(tasks, setSearch(EMPTY_FILTER, 'beta'), tree).map((x) => x.id)).toEqual(['c'])
    expect(applyFilter(tasks, setSearch(EMPTY_FILTER, 'opencode'), tree)).toHaveLength(4)
  })

  it('未归属任务不被项目筛选吞掉——不选项目时它必须在', () => {
    expect(applyFilter(tasks, EMPTY_FILTER, tree).map((x) => x.id)).toContain('d')
  })
})
