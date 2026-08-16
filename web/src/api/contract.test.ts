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
import dirListFixture from './testdata/DirListResult.json'
import authTicketFixture from './testdata/AuthTicketResp.json'
import buildFixture from './testdata/BuildInfo.json'
import eventFixture from './testdata/Event.json'
import fileConflictRespFixture from './testdata/FileConflictResp.json'
import fileReadFixture from './testdata/FileRead.json'
import fileWriteReqFixture from './testdata/FileWriteReq.json'
import fileWriteRespFixture from './testdata/FileWriteResp.json'
import machinesFixture from './testdata/MachinesResp.json'
import projectLocationFixture from './testdata/ProjectLocation.json'
import projectTreeFixture from './testdata/ProjectTreeResp.json'
import ptySessionFixture from './testdata/PtySession.json'
import ptySessionsRespFixture from './testdata/PtySessionsResp.json'
import sessionFixture from './testdata/SessionInfo.json'
import statusFixture from './testdata/StatusResp.json'
import taskFixture from './testdata/Task.json'
import ticketFixture from './testdata/Ticket.json'
import frameFixture from './testdata/Frame.json'
import {
  type ActiveTask,
  type AuthTicketResp,
  type BuildInfo,
  type DirListResult,
  type Event,
  type FileConflictResp,
  type FileRead,
  type FileWriteReq,
  type FileWriteResp,
  type Frame,
  type MachinesResp,
  type ProjectLocation,
  type ProjectTreeResp,
  type PtySession,
  type PtySessionsResp,
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
    for (const key of ['id', 'target', 'repo_path', 'branch', 'plan_path', 'plan_summary', 'executor_session', 'state', 'created_at', 'updated_at', 'name', 'executor', 'model', 'work_dir', 'worktree_managed', 'base_commit', 'base_ahead', 'repo_dirty_count', 'repo_dirty_files', 'actual_model', 'usage']) {
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

  it('Task 的 usage：分子必填、分母可选', () => {
    const t: Task = taskFixture
    expect(t.actual_model).toBe('gpt-5.6-sol')
    expect(t.usage?.context_tokens).toBe(24668)
    // 分母在 fixture 里有值；不报窗口的 executor 会让这个键整个缺席，
    // 而不是给 0——那是「如实缺席」的线格式约定
    expect(t.usage?.context_window).toBe(258400)
  })
})

describe('W4a 帧契约', () => {
  it('Frame：可解析为 Frame 类型，omitempty 字段缺席', () => {
    const f: Frame = frameFixture
    expect(f.seq).toBe(42)
    expect(f.turn).toBe(2)
    expect(f.type).toBe('tool_result')
    expect(f.part).toBe('toolu_01ABCdefGHIjklMNOpqrs')
    expect(f.status).toBe('error')
    expect(f.truncated).toBe(true)
    expect(f.bytes).toBe(193422)
    expect(f.ts).toMatch(/^2026-/)
    // omitempty 的边界：这六个键必须**缺席**而不是空值。
    // 前端据此可以用 `f.delta ?? ''` 安全兜底；若它们变成 "" 或 null，
    // 说明 Go 侧丢了 omitempty，解析侧的假设就塌了。
    for (const key of ['delta', 'tool', 'input', 'ref_seq', 'event', 'reason']) {
      expect(Object.keys(frameFixture)).not.toContain(key)
    }
  })

  it('Frame：可选字段可以显式赋 undefined（指针语义镜像）', () => {
    const f: Frame = { ...frameFixture, part: undefined, status: undefined, bytes: undefined }
    expect(f.part).toBeUndefined()
  })
})

describe('DirListResult 契约', () => {
  it('目录项不带 size，普通文件带 size', () => {
    const resp: DirListResult = dirListFixture
    expect(resp.entries).toHaveLength(2)
    const [dir, file] = resp.entries
    expect(dir.is_dir).toBe(true)
    // 目录的 size 被 omitempty 省略：缺键而不是 0
    expect(dir.size).toBeUndefined()
    expect(file.is_dir).toBe(false)
    expect(file.size).toBe(1284)
  })
})

describe('PtySession 契约', () => {
  it('活着的会话：exit_code 缺席而不是 0', () => {
    const s: PtySession = ptySessionFixture
    expect(s.base_kind).toBe('workspace')
    expect(s.bytes_out).toBe(81920)
    expect('exit_code' in s).toBe(false)
  })

  it('scope=all 信封：远端会话带 machine 与 exit_code', () => {
    const resp = ptySessionsRespFixture as PtySessionsResp
    expect(resp.sessions).toHaveLength(2)
    expect(resp.sessions[0].machine).toBe('')
    expect(resp.sessions[1].machine).toBe('devbox')
    expect(resp.sessions[1].exit_code).toBe(3)
    expect(resp.machines?.map((m) => m.name)).toEqual(['', 'devbox'])
  })

  it('StatusResp：pty_supported 已上报', () => {
    const status = statusFixture as StatusResp
    expect(status.pty_supported).toBe(true)
  })

  it('StatusResp：reveal_supported 已上报', () => {
    const status = statusFixture as StatusResp
    expect(status.reveal_supported).toBe(true)
  })
})

describe('文件读写的契约', () => {
  it('FileRead：可编辑文本形态——sha256 有值、非截断非二进制', () => {
    const fr: FileRead = fileReadFixture
    expect(typeof fr.content).toBe('string')
    expect(typeof fr.size).toBe('number')
    expect(typeof fr.sha256).toBe('string')
    // omitempty 的边界：可编辑常态下 truncated/binary 必须**缺席**（缺键不是 false）
    expect('truncated' in fileReadFixture).toBe(false)
    expect('binary' in fileReadFixture).toBe(false)
  })

  it('FileWriteReq / FileWriteResp：请求带 base_sha256，响应带新哈希', () => {
    const req: FileWriteReq = fileWriteReqFixture
    expect(typeof req.content).toBe('string')
    expect(typeof req.base_sha256).toBe('string')
    const resp: FileWriteResp = fileWriteRespFixture
    expect(typeof resp.sha256).toBe('string')
    expect(typeof resp.size).toBe('number')
  })

  it('FileConflictResp：409 体带磁盘现状 current', () => {
    const c: FileConflictResp = fileConflictRespFixture
    expect(c.error).toBe('文件已被改动')
    expect(typeof c.current).toBe('object')
    expect(typeof c.current.content).toBe('string')
    expect(typeof c.current.sha256).toBe('string')
    expect(typeof c.current.size).toBe('number')
  })
})
