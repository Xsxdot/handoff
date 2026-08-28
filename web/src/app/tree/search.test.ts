// 左栏过滤的契约测试。
//
// 可见性只有一条规则：节点可见 ⟺ 自身命中 或 任一后代命中。自身命中时
// 整棵子树都显示（搜项目名要能看到它下面的全部内容）。这些用例逐条钉住
// 这条规则的四个层级与两个方向。
import { describe, expect, it } from 'vitest'
import type { ProjectTreeResp, Task } from '../../api/types'
import type { OpenedWorkbenchItem } from '../workbench/tabs'
import { filterTree } from './search'

function task(over: Partial<Task>): Task {
  return {
    id: 'i', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '', name: '',
    executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '', machine: '', project_id: 'p1', ...over,
  }
}

// 两个项目：handoff（本机，主目录 /w + 工作树 /w/b2-b3）、nova（devbox，主目录 /srv/n）
const tree: ProjectTreeResp = {
  projects: [
    {
      project_id: 'p1', origin_url: '', name: 'handoff',
      locations: [{
        machine: '', name: 'handoff', path: '/w', probe_error: '',
        workspaces: [
          { path: '/w', branch: 'main', head: 'a', is_main: true, managed: false, created_at: '' },
          { path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'b', is_main: false, managed: true, created_at: '' },
        ],
      }],
    },
    {
      project_id: 'p2', origin_url: '', name: 'nova',
      locations: [{
        machine: 'devbox', name: 'nova', path: '/srv/n', probe_error: '',
        workspaces: [{ path: '/srv/n', branch: 'main', head: 'c', is_main: true, managed: false, created_at: '' }],
      }],
    },
  ],
  unowned: ['scratchpad'],
}

const tasks: Task[] = [
  task({ id: 'T1', project_id: 'p1', machine: '', work_dir: '/w/b2-b3', name: '重构工单通道' }),
  task({ id: 'T2', project_id: 'p2', machine: 'devbox', work_dir: '/srv/n', name: '补齐图像校验' }),
  task({ id: 'T9', project_id: '', machine: '', work_dir: '', name: '孤儿任务' }),
]

describe('filterTree', () => {
  it('空查询：全部可见，N 等于项目总数', () => {
    const r = filterTree(tree, tasks, '')
    expect(r.projectCount).toBe(2)
    expect(r.projects).toHaveLength(2)
    expect(r.unassignedTasks).toHaveLength(1)
    expect(r.unownedNames).toEqual(['scratchpad'])
    expect(r.isEmpty).toBe(false)
  })

  it('命中项目名：该项目整棵子树可见，另一个项目消失', () => {
    const r = filterTree(tree, tasks, 'handoff')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('handoff')
    expect(r.projects[0].locations[0].workspaces).toHaveLength(2)
  })

  it('命中机器名：其祖先项目可见，非该机器的项目消失', () => {
    const r = filterTree(tree, tasks, 'devbox')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('nova')
  })

  // "" 的机器要能用「本机」搜到——这是 machineLabel 的口径，不是原始字段
  it('命中「本机」：本机上的项目可见', () => {
    const r = filterTree(tree, tasks, '本机')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('handoff')
  })

  it('命中目录名：只留下命中的那个目录，兄弟目录消失', () => {
    const r = filterTree(tree, tasks, 'b2-b3')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].locations[0].workspaces).toHaveLength(1)
    expect(r.projects[0].locations[0].workspaces[0].branch).toBe('integration/b2-b3')
  })

  it('命中任务名：祖先链全部可见，兄弟目录消失', () => {
    const r = filterTree(tree, tasks, '重构工单')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('handoff')
    expect(r.projects[0].locations[0].workspaces).toHaveLength(1)
    expect(r.projects[0].locations[0].workspaces[0].path).toBe('/w/b2-b3')
  })

  it('命中已结束任务名：祖先项目和机器仍可见（目录已被回收）', () => {
    const withArchived = [
      ...tasks,
      task({ id: 'T-old', state: 'completed', work_dir: '/w/gone', name: '已回收的任务' }),
    ]
    const r = filterTree(tree, withArchived, '已回收')
    expect(r.projectCount).toBe(1)
    expect(r.projects[0].name).toBe('handoff')
    expect(r.projects[0].locations[0].machine).toBe('')
  })

  it('未归属分组参与过滤，且不计入 N', () => {
    const r = filterTree(tree, tasks, '孤儿')
    expect(r.projectCount).toBe(0)
    expect(r.unassignedTasks).toHaveLength(1)
    expect(r.unownedNames).toHaveLength(0)
    expect(r.isEmpty).toBe(false)   // 未归属有货，不算空
  })

  it('未登记目录名也能搜到', () => {
    const r = filterTree(tree, tasks, 'scratch')
    expect(r.projectCount).toBe(0)
    expect(r.unownedNames).toEqual(['scratchpad'])
    expect(r.isEmpty).toBe(false)
  })

  it('零命中：isEmpty 为真', () => {
    const r = filterTree(tree, tasks, 'zzzz-nothing')
    expect(r.projectCount).toBe(0)
    expect(r.unassignedTasks).toHaveLength(0)
    expect(r.unownedNames).toHaveLength(0)
    expect(r.isEmpty).toBe(true)
  })

  it('大小写不敏感，首尾空白被 trim', () => {
    expect(filterTree(tree, tasks, '  HANDOFF  ').projectCount).toBe(1)
    expect(filterTree(tree, tasks, '  ').projectCount).toBe(2)   // 全空白等同空查询
  })

  it('打开的文件/终端/TUI 也能把项目、机器和目录祖先带入搜索结果', () => {
    const opened: OpenedWorkbenchItem[] = [
      {
        tabId: 'file-1', groupId: 'g1', column: 0, row: 0,
        base: { key: '/srv/n', kind: 'workspace', path: '/srv/n', label: 'main', projectName: 'nova', machine: 'devbox' },
        content: { kind: 'file', rel: 'src/opened-file.ts' }, label: 'src/opened-file.ts',
      },
      {
        tabId: 'term-1', groupId: 'g1', column: 1, row: 0,
        base: { key: '/w/b2-b3', kind: 'workspace', path: '/w/b2-b3', label: 'integration/b2-b3', projectName: 'handoff', machine: '' },
        content: { kind: 'terminal', seq: 0 }, label: '终端 · opened-terminal',
      },
    ]
    const byFile = filterTree(tree, tasks, 'opened-file', opened)
    expect(byFile.projects[0].name).toBe('nova')
    expect(byFile.projects[0].locations[0].machine).toBe('devbox')
    expect(byFile.projects[0].locations[0].workspaces[0].path).toBe('/srv/n')
    const byTerminal = filterTree(tree, tasks, 'opened-terminal', opened)
    expect(byTerminal.projects[0].name).toBe('handoff')
    expect(byTerminal.projects[0].locations[0].workspaces[0].path).toBe('/w/b2-b3')
  })
})
