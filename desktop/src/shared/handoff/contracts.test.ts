/**
 * Handoff 契约跨语言测试：直接读取 Go golden JSON（internal/desktopapi/testdata）
 * 断言 Bootstrap/ControlEvent/Problem 可被 Zod 解析，防止 Go/TS 契约漂移。
 *
 * 职责：
 *   - Bootstrap、ControlEvent、Problem 全部解析
 *   - 未知字段 strip
 *   - 缺关键 version/revision 拒绝
 *
 * 边界：
 *   - 只读 golden 文件，不发起网络
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  bootstrapResponseSchema,
  controlEventEnvelopeSchema,
  problemSchema
} from './contracts'

const testdata = (name: string): string =>
  readFileSync(resolve(process.cwd(), '../internal/desktopapi/testdata', name), 'utf8')

describe('handoff contracts vs Go golden', () => {
  it('parses bootstrap golden with revision', () => {
    const parsed = bootstrapResponseSchema.parse(JSON.parse(testdata('bootstrap.json')))
    expect(parsed.control_revision).toBe(3)
    expect(parsed.machines[0]?.kind).toBe('local')
    expect(parsed.workspaces[0]?.kind).toBe('main')
    expect(parsed.active_task_summaries[0]?.state).toBe('running')
  })

  it('parses control-event golden with revision and kind', () => {
    const parsed = controlEventEnvelopeSchema.parse(JSON.parse(testdata('control-event.json')))
    expect(parsed.revision).toBe(4)
    expect(parsed.kind).toBe('workspace.upsert')
    expect(parsed.resource_id).toBe('ws-main')
  })

  it('parses problem golden', () => {
    const parsed = problemSchema.parse(JSON.parse(testdata('problem.json')))
    expect(parsed.code).toBe('PATH_OUTSIDE_WORKSPACE')
    expect(parsed.retryable).toBe(false)
  })

  it('strips unknown fields', () => {
    const parsed = bootstrapResponseSchema.parse({
      machines: [],
      projects: [],
      locations: [],
      workspaces: [],
      git_refs: [],
      active_task_summaries: [],
      operations: [],
      control_revision: 0,
      some_future_field: 'ignored'
    })
    expect(parsed).not.toHaveProperty('some_future_field')
  })

  it('rejects missing control_revision', () => {
    expect(() =>
      bootstrapResponseSchema.parse({
        machines: [],
        projects: [],
        locations: [],
        workspaces: [],
        git_refs: [],
        active_task_summaries: [],
        operations: []
      })
    ).toThrow()
  })

  it('rejects missing revision on control event', () => {
    expect(() =>
      controlEventEnvelopeSchema.parse({
        kind: 'workspace.upsert',
        resource_id: 'ws1',
        payload: {},
        created_at: '2026-08-09T00:00:00Z'
      })
    ).toThrow()
  })
})
