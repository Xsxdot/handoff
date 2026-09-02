// taskName.test.ts —— 任务显示名的纯函数表驱动测试。
//
// 「名是 plan_summary 前缀 → 视为没带信息」这条口径对应后端 deriveName 的
// prompt 前 20 字兜底（internal/agentd/manager.go）：charter 派发的提示词开头
// 千篇一律，这种名分不清谁是谁，界面改用分支；显式名与 plan 文件名派生名
// 不受影响。口径与 2026-08-29 真机数据核对过（bench-* 名均非摘要前缀）。
import { describe, expect, it } from 'vitest'
import { taskDisplayName } from './taskName'

describe('taskDisplayName', () => {
  it('name 是有效名（非摘要前缀）时用 name', () => {
    expect(taskDisplayName({ name: '审 B264', branch: 'cards/B264', plan_summary: '# 先读纪律' })).toBe('审 B264')
    expect(taskDisplayName({ name: 'bench-logpanel-blank', branch: 'handoff/fc123109', plan_summary: '# 运行态日志面板切换' })).toBe('bench-logpanel-blank')
  })

  it('name 是 plan_summary 的前缀（prompt 派生名）→ 用 branch 分清谁是谁', () => {
    expect(taskDisplayName({
      name: '你是 charter 流程的节点执行者。对卡 ',
      branch: 'cards/B285-review-2',
      plan_summary: '你是 charter 流程的节点执行者。对卡 B285-review-2\n',
    })).toBe('cards/B285-review-2')
  })

  it('name 与摘要完全相同同样视为没带信息 → 用 branch', () => {
    expect(taskDisplayName({ name: '什么都不用做', branch: 'feat/b76-smoke', plan_summary: '什么都不用做' })).toBe('feat/b76-smoke')
  })

  it('name 缺席且 branch 非空时用 branch', () => {
    expect(taskDisplayName({ name: '', branch: 'cards/B285-review-2', plan_summary: '摘要' })).toBe('cards/B285-review-2')
  })

  it('name 与 branch 都没用时回退 plan_summary', () => {
    expect(taskDisplayName({ name: '', branch: '', plan_summary: '摘要' })).toBe('摘要')
  })

  it('全空时用「（无名称）」', () => {
    expect(taskDisplayName({ name: '', branch: '', plan_summary: '' })).toBe('（无名称）')
  })
})
