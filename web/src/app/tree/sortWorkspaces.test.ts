import { describe, expect, it } from 'vitest'
import { sortWorkspaces, type WorkspaceMetrics } from './sortWorkspaces'
import type { Workspace } from '../../api/types'

// ws 造一个最小工作树。branch 兼作身份，断言里靠它认人。
function ws(branch: string, isMain = false): Workspace {
  return { path: `/r/${branch}`, branch, head: 'abc1234', is_main: isMain, managed: false, created_at: '' }
}

// metrics 把一张 branch → 三元组的表包成回调。
function metrics(table: Record<string, [number, number, string]>) {
  return (w: Workspace): WorkspaceMetrics => {
    const [tickets, tasks, createdAt] = table[w.branch] ?? [0, 0, '']
    return { tickets, tasks, createdAt }
  }
}

const names = (list: Workspace[]) => list.map((w) => w.branch)

describe('sortWorkspaces', () => {
  it('工单数优先级最高，压过任务数与时间', () => {
    const list = [ws('a'), ws('b'), ws('c')]
    const got = sortWorkspaces(list, metrics({
      a: [0, 99, '2026-08-17T10:00:00Z'],
      b: [1, 0, '2020-01-01T00:00:00Z'],
      c: [0, 50, '2026-08-16T10:00:00Z'],
    }))
    expect(names(got)).toEqual(['b', 'a', 'c'])
  })

  it('工单数相同时按任务数降序', () => {
    const list = [ws('a'), ws('b')]
    const got = sortWorkspaces(list, metrics({
      a: [2, 1, '2026-08-17T10:00:00Z'],
      b: [2, 5, '2020-01-01T00:00:00Z'],
    }))
    expect(names(got)).toEqual(['b', 'a'])
  })

  it('工单与任务都相同时按创建时间降序（新的在前）', () => {
    const list = [ws('old'), ws('new')]
    const got = sortWorkspaces(list, metrics({
      old: [0, 0, '2020-01-01T00:00:00Z'],
      new: [0, 0, '2026-08-17T10:00:00Z'],
    }))
    expect(names(got)).toEqual(['new', 'old'])
  })

  it('主工作树恒排第一，不参与排序——哪怕别人有工单', () => {
    const list = [ws('feat'), ws('main', true)]
    const got = sortWorkspaces(list, metrics({
      feat: [9, 9, '2026-08-17T10:00:00Z'],
      main: [0, 0, '2020-01-01T00:00:00Z'],
    }))
    expect(names(got)).toEqual(['main', 'feat'])
  })

  it('空 created_at 当最旧，排在有时间的后面', () => {
    const list = [ws('empty'), ws('dated')]
    const got = sortWorkspaces(list, metrics({
      empty: [0, 0, ''],
      dated: [0, 0, '2020-01-01T00:00:00Z'],
    }))
    expect(names(got)).toEqual(['dated', 'empty'])
  })

  it('三个键全等时按 path 升序，结果稳定不随输入顺序变', () => {
    const same = metrics({})
    const forward = sortWorkspaces([ws('c'), ws('a'), ws('b')], same)
    const backward = sortWorkspaces([ws('b'), ws('c'), ws('a')], same)
    expect(names(forward)).toEqual(['a', 'b', 'c'])
    expect(names(backward)).toEqual(['a', 'b', 'c'])
  })

  it('不改入参数组', () => {
    const list = [ws('b'), ws('a')]
    const copy = [...list]
    sortWorkspaces(list, metrics({}))
    expect(list).toEqual(copy)
  })
})
