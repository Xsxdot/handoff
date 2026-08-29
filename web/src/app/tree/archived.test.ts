import { describe, expect, it } from 'vitest'
import { RECENT_TERMINAL_MS, archivedKey, archivedTasks, isTerminalState, recentlyCompleted } from './archived'
import type { Task } from '../../api/types'

// 任务夹具照抄 ProjectTree.test.tsx 的 task() 造法：只填必要字段。
function t(x: Partial<Task>): Task {
  return {
    id: 'i', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'completed', created_at: '', updated_at: '', name: 'n',
    executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '', machine: '', project_id: 'p1', ...x,
  } as Task
}

describe('isTerminalState', () => {
  it('只有 completed / failed 算终态', () => {
    expect(isTerminalState('completed')).toBe(true)
    expect(isTerminalState('failed')).toBe(true)
    expect(isTerminalState('running')).toBe(false)
    expect(isTerminalState('waiting_review')).toBe(false)
  })
})

describe('archivedTasks（B288 口径：项目内全部终态，不再看目录）', () => {
  it('completed 任务目录还在（原地/工作树健在）也收进', () => {
    const out = archivedTasks([t({ id: 'live', work_dir: '/w/live' })])
    expect(out.get(archivedKey('p1'))?.map((x) => x.id)).toEqual(['live'])
  })

  it('completed 任务目录已被回收（orphan）同样收进（现状回归）', () => {
    const out = archivedTasks([t({ id: 'gone', work_dir: '/w/gone' })])
    expect(out.get(archivedKey('p1'))?.map((x) => x.id)).toEqual(['gone'])
  })

  it('failed 同收；running 与 waiting_* 不收', () => {
    const out = archivedTasks([
      t({ id: 'f', state: 'failed' }),
      t({ id: 'r', state: 'running' }),
      t({ id: 'w1', state: 'waiting_answer' }),
      t({ id: 'w2', state: 'waiting_review' }),
    ])
    expect(out.get(archivedKey('p1'))?.map((x) => x.id)).toEqual(['f'])
  })

  it('未归属任务（project_id 为空）不收', () => {
    const out = archivedTasks([t({ id: 'orphan', project_id: '' })])
    expect(out.size).toBe(0)
  })

  it('同项目两台机器的终态归进同一个键，值内顺序 = 任务流原顺序', () => {
    const out = archivedTasks([
      t({ id: 'a', machine: '' }),
      t({ id: 'b', machine: 'mac-02', state: 'failed' }),
      t({ id: 'c', machine: '' }),
    ])
    const list = out.get(archivedKey('p1'))
    expect(list).toHaveLength(3)
    expect(list!.map((x) => x.id)).toEqual(['a', 'b', 'c'])
  })

  it('没有终态任务的项目不出键（调用方按「取不到就不渲染」处理）', () => {
    const out = archivedTasks([t({ id: 'r', state: 'running' })])
    expect(out.has(archivedKey('p1'))).toBe(false)
  })

  it('archivedKey 键就是 projectID 本身', () => {
    expect(archivedKey('p1')).toBe('p1')
  })
})

describe('recentlyCompleted（终态 30 分钟缓冲窗，2026-08-29）', () => {
  // RFC3339Nano 例：updated_at 距 now 恰好 10 分钟。
  const NOW = Date.parse('2026-08-29T12:00:00+08:00')
  const ago = (ms: number) => new Date(NOW - ms).toISOString()

  it('终态且在窗内（29 分钟）→ true：留在任务列表', () => {
    expect(recentlyCompleted(t({ state: 'completed', updated_at: ago(29 * 60_000) }), NOW)).toBe(true)
    expect(recentlyCompleted(t({ state: 'failed', updated_at: ago(29 * 60_000) }), NOW)).toBe(true)
  })

  it('终态但已出窗（31 分钟）→ false：交给「已结束」', () => {
    expect(recentlyCompleted(t({ state: 'completed', updated_at: ago(31 * 60_000) }), NOW)).toBe(false)
  })

  it('窗界值本身（恰好 30 分钟）算出窗——「以内」是开区间', () => {
    expect(recentlyCompleted(t({ state: 'completed', updated_at: ago(RECENT_TERMINAL_MS) }), NOW)).toBe(false)
  })

  it('非终态一律 false（缓冲窗不是未终态任务的通行证）', () => {
    expect(recentlyCompleted(t({ state: 'running', updated_at: ago(0) }), NOW)).toBe(false)
    expect(recentlyCompleted(t({ state: 'waiting_review', updated_at: ago(0) }), NOW)).toBe(false)
  })

  it('updated_at 解析不了时按出窗处理：宁可早进已结束，不假留在列表', () => {
    expect(recentlyCompleted(t({ state: 'completed', updated_at: '' }), NOW)).toBe(false)
    expect(recentlyCompleted(t({ state: 'completed', updated_at: '不是时间' }), NOW)).toBe(false)
  })
})
