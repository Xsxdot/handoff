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
  fileDocumentSchema,
  fileEntrySchema,
  fileSearchResultSchema,
  fileStreamFrameSchema,
  gitStatusSnapshotSchema,
  previewSessionSchema,
  problemSchema,
  ptyServerFrameSchema,
  ptySessionSchema
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

  it('parses workspace resource goldens', () => {
    expect(fileEntrySchema.parse(JSON.parse(testdata('file-entry.json'))).kind).toBe('directory')
    expect(fileDocumentSchema.parse(JSON.parse(testdata('file-document.json'))).version).toBe(
      'sha256:2cf24dba'
    )
    expect(
      fileSearchResultSchema.parse(JSON.parse(testdata('file-search-result.json'))).matches
    ).toHaveLength(1)
    expect(
      fileStreamFrameSchema.parse(JSON.parse(testdata('file-stream-frame.json'))).replay
    ).toHaveLength(1)
    expect(gitStatusSnapshotSchema.parse(JSON.parse(testdata('git-status.json'))).branch).toBe(
      'main'
    )
    expect(ptySessionSchema.parse(JSON.parse(testdata('pty-session.json'))).incarnation).toBe(
      'inc-1'
    )
    expect(ptyServerFrameSchema.parse(JSON.parse(testdata('pty-frame.json'))).seq).toBe(7)
    expect(
      previewSessionSchema.parse(JSON.parse(testdata('preview-session.json'))).preview_session_id
    ).toBe('preview-1')
  })

  it('rejects resource payloads missing concurrency identity', () => {
    const file = JSON.parse(testdata('file-document.json')) as Record<string, unknown>
    delete file.version
    expect(() => fileDocumentSchema.parse(file)).toThrow()

    const frame = JSON.parse(testdata('pty-frame.json')) as Record<string, unknown>
    delete frame.incarnation
    expect(() => ptyServerFrameSchema.parse(frame)).toThrow()
    frame.incarnation = 'inc-1'
    delete frame.seq
    expect(() => ptyServerFrameSchema.parse(frame)).toThrow()

    const fileStream = JSON.parse(testdata('file-stream-frame.json')) as Record<string, unknown>
    delete fileStream.through_seq
    expect(() => fileStreamFrameSchema.parse(fileStream)).toThrow()
  })
})
