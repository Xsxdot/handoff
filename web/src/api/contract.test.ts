// 契约测试：读 Go 侧生成的同一批 fixture，断言 TS 类型能解析且关键字段齐全。
//
// 这批 JSON 由 internal/proto 的 TestContractFixtures 生成并逐字节钉住（它变红
// 时这里多半也变红）；本测试是 web 侧的镜像断言：
//   - 编译期：JSON import 的类型以强类型变量承接，tsc 会在类型与字段不匹配时
//     直接报错（fixture 缺字段 / 类型漂移都逃不过 typecheck）
//   - 运行期：断言关键字段存在且类型正确，防止「类型对了但运行时缺字段」
//
// 任何一方改动线格式（字段改名/增删）都必须同步 Go 结构体、fixture 与本文件，
// 漏一处就有一个测试当场变红。
//
// 为什么用 import 读 JSON 而不是 fs：vitest 的 jsdom 环境里 import.meta.url 被
// 转换成 dev-server 路径，new URL 相对定位不可靠；JSON import 是 Vite 原生能力，
// 路径按模块相对解析、稳定且自带编译期类型。
import { describe, expect, it } from 'vitest'
import activeTaskFixture from './testdata/ActiveTask.json'
import authTicketFixture from './testdata/AuthTicketResp.json'
import buildFixture from './testdata/BuildInfo.json'
import eventFixture from './testdata/Event.json'
import machinesFixture from './testdata/MachinesResp.json'
import projectLocationFixture from './testdata/ProjectLocation.json'
import projectTreeFixture from './testdata/ProjectTreeResp.json'
import sessionFixture from './testdata/SessionInfo.json'
import statusFixture from './testdata/StatusResp.json'
import taskFixture from './testdata/Task.json'
import ticketFixture from './testdata/Ticket.json'
import {
  type ActiveTask,
  type AuthTicketResp,
  type BuildInfo,
  type Event,
  type MachinesResp,
  type ProjectLocation,
  type ProjectTreeResp,
  type SessionInfo,
  type StatusResp,
  type Task,
  type Ticket,
} from './types'

describe('契约 fixture 与 TS 类型', () => {
  it('Task：可解析为 Task 类型，关键字段齐全', () => {
    const task: Task = taskFixture
    expect(task.id).toBe('7ec762e7-3bd2-412c-a39c-e4cf8b4057ad')
    expect(task.state).toBe('running')
    expect(task.repo_path).toContain('/handoff')
    expect(task.branch).toBe('handoff/w1-web-scaffold')
    expect(task.created_at).toMatch(/^2026-08-11T/)
    expect(task.worktree_managed).toBe(true)
    for (const key of ['id', 'target', 'repo_path', 'branch', 'plan_path', 'plan_summary', 'executor_session', 'state', 'created_at', 'updated_at', 'name', 'executor', 'model', 'work_dir', 'worktree_managed', 'base_commit', 'base_ahead', 'repo_dirty_count', 'repo_dirty_files']) {
      expect(Object.keys(task)).toContain(key)
    }
  })

  it('Event：可解析为 Event 类型，关键字段齐全', () => {
    const ev: Event = eventFixture
    expect(ev.seq).toBe(7)
    expect(ev.task_id).toBe('7ec762e7-3bd2-412c-a39c-e4cf8b4057ad')
    expect(ev.type).toBe('question')
    expect(ev.created_at).toMatch(/^2026-08-11T/)
    expect(Object.keys(ev)).toEqual(expect.arrayContaining(['seq', 'task_id', 'type', 'payload', 'created_at']))
  })

  it('Ticket：可解析为 Ticket 类型，指针字段（answer/answered_at/delivered_at）可缺席', () => {
    const tk: Ticket = ticketFixture
    expect(tk.id).toBe('tk-1')
    expect(tk.kind).toBe('gate')
    expect(tk.answer).toBe('allow')
    expect(tk.answered_at).toMatch(/^2026-08-11T/)
    expect(tk.delivered_at).toMatch(/^2026-08-11T/)
    // 指针字段类型是可选的：把值赋成 undefined 必须能通过类型检查
    const optional: Ticket = { ...tk, answer: undefined, answered_at: undefined, delivered_at: undefined }
    expect(optional.answer).toBeUndefined()
  })

  it('ProjectLocation / AuthTicketResp / SessionInfo：可解析且关键字段齐全', () => {
    const loc: ProjectLocation = projectLocationFixture
    expect(loc.project_id).toHaveLength(16)
    expect(loc.name).toBe('handoff')
    expect(loc.path).toContain('/handoff')
    expect(loc.status).toBe('有效')

    const auth: AuthTicketResp = authTicketFixture
    expect(auth.url).toContain('/console?ticket=')
    expect(auth.expires_at).toMatch(/^2026-08-11T/)

    const sess: SessionInfo = sessionFixture
    expect(sess.id).toBe('sess-01HX')
    // revoked_at 是 created_at + 24h（2026-08-12），断言存在且能解析即可
    expect(sess.revoked_at).toMatch(/^2026-08-12T/)
  })

  it('BuildInfo / ActiveTask / StatusResp：可解析且关键字段齐全', () => {
    const build: BuildInfo = buildFixture
    expect(build.version).toBe('v0.1.0')
    expect(build.revision).toHaveLength(40)
    expect(build.go).toMatch(/^go1/)

    // JSON import 会把字符串字面量拓宽成 string（live: 'alive' → string），
    // 故用 as 收窄到带 union 的类型；live 取值合法性由下面 expect 断言
    const active = activeTaskFixture as ActiveTask
    expect(active.live).toBe('alive')
    expect(active.note).toBe('')

    const status = statusFixture as StatusResp
    expect(status.version.revision).toHaveLength(40)
    expect(status.listen).toBe('127.0.0.1:7777')
    expect(status.default_executor).toBe('opencode')
    expect(status.executors).toEqual(expect.arrayContaining(['opencode', 'claude']))
    // 六个状态键恒存在（0 与缺键对消费方是两回事）
    for (const key of ['pending', 'running', 'waiting_answer', 'waiting_review', 'completed', 'failed']) {
      expect(status.task_counts).toHaveProperty(key)
    }
    expect(status.active).toHaveLength(1)
  })
})

describe('W3a 契约', () => {
  it('ProjectTreeResp 的字段与类型一致', () => {
    const resp: ProjectTreeResp = projectTreeFixture
    expect(Array.isArray(resp.projects)).toBe(true)
    expect(Array.isArray(resp.unowned)).toBe(true)
    const loc = resp.projects[0].locations[0]
    // 单机响应的不变式：每个项目在每台机器上至多一个位置（W3a §1.1）
    expect(resp.projects[0].locations.length).toBeLessThanOrEqual(1)
    expect(typeof loc.machine).toBe('string')
    expect(typeof loc.probe_error).toBe('string')
    expect(Array.isArray(loc.workspaces)).toBe(true)
    expect(typeof loc.workspaces[0].is_main).toBe('boolean')
    expect(typeof loc.workspaces[0].managed).toBe('boolean')
  })

  it('MachinesResp 带 W3b 需要的三个只读投影', () => {
    const resp: MachinesResp = machinesFixture
    const m = resp.machines[0]
    expect(Array.isArray(m.executors)).toBe(true)
    expect(typeof m.default_executor).toBe('string')
    expect(typeof m.probe_ms).toBe('number')
    expect(typeof m.reachable).toBe('boolean')
    expect(typeof m.error).toBe('string')
  })

  it('Task 带 machine 与 project_id 两个注解字段', () => {
    const t: Task = taskFixture
    expect(typeof t.machine).toBe('string')
    expect(typeof t.project_id).toBe('string')
  })
})
