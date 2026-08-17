import { describe, expect, it } from 'vitest'
import { archivedKey, archivedTasks, isTerminalState } from './archived'
import type { ProjectTreeResp, Task } from '../../api/types'

// tree 造一棵只含必要字段的树：一个项目、两台机器。
function tree(): ProjectTreeResp {
  return {
    projects: [
      {
        project_id: 'p1',
        name: 'handoff',
        locations: [
          {
            machine: '',
            name: 'handoff',
            path: '/repo',
            probe_error: '',
            workspaces: [
              { path: '/repo', branch: 'main', head: 'aaaaaaa', is_main: true, managed: false },
              { path: '/wt/live', branch: 'feat', head: 'bbbbbbb', is_main: false, managed: true },
            ],
          },
          {
            machine: 'mac-02',
            name: 'handoff',
            path: '/remote',
            probe_error: '',
            workspaces: [],
          },
        ],
      },
    ],
    unowned: [],
  } as unknown as ProjectTreeResp
}

function t(x: Partial<Task>): Task {
  return { id: 'id', project_id: 'p1', machine: '', work_dir: '', state: 'completed', name: 'n', ...x } as Task
}

describe('isTerminalState', () => {
  it('只有 completed / failed 算终态', () => {
    expect(isTerminalState('completed')).toBe(true)
    expect(isTerminalState('failed')).toBe(true)
    expect(isTerminalState('running')).toBe(false)
    expect(isTerminalState('waiting_review')).toBe(false)
  })
})

describe('archivedTasks', () => {
  it('目录已被回收的终态任务按 项目+机器 归集', () => {
    const tasks = [
      t({ id: 'gone', work_dir: '/wt/removed' }),
      t({ id: 'gone-failed', work_dir: '/wt/removed2', state: 'failed' }),
      t({ id: 'remote', machine: 'mac-02', work_dir: '/remote/wt/x' }),
    ]
    const out = archivedTasks(tree(), tasks)
    expect(out.get(archivedKey('p1', ''))?.map((x) => x.id)).toEqual(['gone', 'gone-failed'])
    expect(out.get(archivedKey('p1', 'mac-02'))?.map((x) => x.id)).toEqual(['remote'])
  })

  it('目录还在的终态任务不收——它照常挂在目录行下面', () => {
    const out = archivedTasks(tree(), [t({ id: 'live', work_dir: '/wt/live' })])
    expect(out.size).toBe(0)
  })

  it('原地任务（work_dir 为空）在主目录还在时不收', () => {
    const out = archivedTasks(tree(), [t({ id: 'inplace', work_dir: '' })])
    expect(out.size).toBe(0)
  })

  it('非终态任务一律不收，哪怕目录已经不在', () => {
    const out = archivedTasks(tree(), [t({ id: 'run', state: 'running', work_dir: '/wt/removed' })])
    expect(out.size).toBe(0)
  })

  it('未归属任务不收（树末尾的「未归属」分组管它们）', () => {
    const out = archivedTasks(tree(), [t({ id: 'orphan', project_id: '', work_dir: '/wt/removed' })])
    expect(out.size).toBe(0)
  })

  it('项目在该机器上没有位置时不收——缺的是整个位置，不是一个目录', () => {
    const out = archivedTasks(tree(), [t({ id: 'nowhere', machine: 'mac-99', work_dir: '/x' })])
    expect(out.size).toBe(0)
  })
})
