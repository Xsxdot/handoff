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
import cardStepReqFixture from './testdata/CardStepReq.json'
import dirListFixture from './testdata/DirListResult.json'
import disciplineMappingReqFixture from './testdata/DisciplineMappingReq.json'
import disciplineRespFixture from './testdata/DisciplineResp.json'
import executorDefaultReqFixture from './testdata/ExecutorDefaultReq.json'
import executorDefaultRespFixture from './testdata/ExecutorDefaultResp.json'
import taskPlanFixture from './testdata/TaskPlan.json'
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
import workspaceCardResultsFixture from './testdata/WorkspaceCardResults.json'
import createWorktreeReqFixture from './testdata/CreateWorktreeReq.json'
import createWorktreeReqEmptyFixture from './testdata/CreateWorktreeReqEmpty.json'
import ptySessionFixture from './testdata/PtySession.json'
import ptySessionsRespFixture from './testdata/PtySessionsResp.json'
import sessionFixture from './testdata/SessionInfo.json'
import statusFixture from './testdata/StatusResp.json'
import taskFixture from './testdata/Task.json'
import tasksRespFixture from './testdata/TasksResp.json'
import ticketFixture from './testdata/Ticket.json'
import frameFixture from './testdata/Frame.json'
import envRespFixture from './testdata/EnvResp.json'
import envKeysFixture from './testdata/EnvKeysResp.json'
import envMappingReqFixture from './testdata/EnvMappingReq.json'
import launchersRespFixture from './testdata/LaunchersResp.json'
import createPtySessionReqFixture from './testdata/CreatePtySessionReq.json'
import workbenchBaseFixture from './testdata/WorkbenchBase.json'
import workbenchStateFixture from './testdata/WorkbenchStateResp.json'
import newCardReqFixture from './testdata/NewCardReq.json'
import cardCreateRespFixture from './testdata/CardCreateResp.json'
import migrateCardReqFixture from './testdata/MigrateCardReq.json'
import migrateCardRespFixture from './testdata/MigrateCardResp.json'
import ledgerEventFixture from './testdata/LedgerEvent.json'
import cardViewFixture from './testdata/CardView.json'
import cardDetailFixture from './testdata/CardDetail.json'
import nodeDefFixture from './testdata/NodeDef.json'
import flowDetailFixture from './testdata/FlowDetail.json'
import {
  type ActiveTask,
  type AuthTicketResp,
  type BuildInfo,
  type DirListResult,
  type DisciplineMappingReq,
  type DisciplineResp,
  type Event,
  type EnvKeysResp,
  type CreatePtySessionReq,
  type EnvMappingReq,
  type EnvResp,
  type ExecutorDefaultReq,
  type ExecutorDefaultResp,
  type FileConflictResp,
  type FileRead,
  type FileWriteReq,
  type FileWriteResp,
  type Frame,
  type MachinesResp,
  type ProjectLocation,
  type Workspace,
  type CreateWorktreeReq,
  type LaunchersResp,
  type ProjectTreeResp,
  type PtySession,
  type PtySessionsResp,
  type SessionInfo,
  type StatusResp,
  type Task,
  type TaskPlan,
  type TasksResp,
  type Ticket,
  type WorkbenchBaseRow,
  type WorkbenchStateResp,
} from './types'
import type {
  CardDetail,
  CardCreateResp,
  CardView,
  FlowDetail,
  LedgerEvent,
  MigrateCardReq,
  MigrateCardResp,
  NewCardReq,
  NodeDef,
  CardStepReq,
} from './ledger'
import squadsFixture from './testdata/SquadsResp.json'
import queueFixture from './testdata/QueueResp.json'
import coordinatorLaunchFixture from './testdata/CoordinatorLaunchResp.json'
import {
  type CarrierView,
  type CoordinatorLaunchResp,
  type QueueEntry,
  type SquadView,
} from './scheduling'

describe('契约 fixture 与 TS 类型', () => {
	it('CardStepReq：六字段可由 Go fixture 解析', () => {
		const req: CardStepReq = cardStepReqFixture
		expect(req).toEqual({
			step: 'review', target: 'linux-01', executor: 'codex', model: 'gpt-5',
			extra: '只检查本轮改动', actor: 'cli:alice@linux-01#1234',
		})
		expect(Object.keys(req)).toEqual(['step', 'target', 'executor', 'model', 'extra', 'actor'])
	})

	it('账本 wire：建卡/迁移 DTO 与派生投影字段完整', () => {
		const create: NewCardReq = newCardReqFixture
		const created: CardCreateResp = cardCreateRespFixture
		const migrate: MigrateCardReq = migrateCardReqFixture
		const response: MigrateCardResp = migrateCardRespFixture
		const event: LedgerEvent = ledgerEventFixture
		const view: CardView = cardViewFixture
		const detail: CardDetail = cardDetailFixture
		const node: NodeDef = nodeDefFixture
		const flow: FlowDetail = flowDetailFixture
		expect(create.workflow).toBeUndefined()
		expect(created.id).toBe('B167')
		expect(Object.keys(create)).toEqual(expect.arrayContaining(['title', 'project']))
		expect(migrate.workflow).toBe('domain')
		expect(migrate.status).toBe('拆解')
		expect(migrate.version).toBeUndefined()
		expect(response.ok).toBe(true)
		expect(response.from.workflow).toBe('bug')
		expect(response.to.status).toBe('拆解')
		expect(view.base_frozen).toBe(false)
		expect(event.payload).toMatchObject({
			from_workflow: 'bug', from_version: 1, from_status: '进行中',
			to_workflow: 'domain', to_version: 1, to_status: '拆解',
		})
		expect(Object.keys(view)).toEqual(expect.arrayContaining(['children_total', 'children_done', 'open_tickets']))
		expect(Object.keys(detail)).toEqual(expect.arrayContaining(['card', 'relations', 'events', 'task_states', 'effective_base_branch', 'decisions', 'needs', 'children']))
		expect(node.dispatch).toBeUndefined()
		expect(node.produces).toEqual({
			kind: 'doc',
			path: 'docs/superpowers/plans/b201-plan.md',
		})
		expect(flow.states).toEqual(['待办', '定性中', '已定性'])
	})
  it('Task：可解析为 Task 类型，关键字段齐全', () => {
    const task: Task = taskFixture
    expect(task.id).toBe('7ec762e7-3bd2-412c-a39c-e4cf8b4057ad')
    expect(task.state).toBe('running')
    expect(task.repo_path).toContain('/handoff')
    expect(task.branch).toBe('handoff/w1-web-scaffold')
    expect(task.created_at).toMatch(/^2026-08-11T/)
    expect(task.worktree_managed).toBe(true)
    for (const key of ['id', 'target', 'repo_path', 'branch', 'plan_path', 'plan_summary', 'executor_session', 'state', 'created_at', 'updated_at', 'name', 'executor', 'model', 'work_dir', 'worktree_managed', 'base_commit', 'base_ahead', 'repo_dirty_count', 'repo_dirty_files', 'actual_model', 'usage', 'timing']) {
      expect(Object.keys(task)).toContain(key)
    }
  })

  it('Task.timing：三分法自洽，tool_ms 与 tool_span_ms 互不冒充', () => {
    const task: Task = taskFixture
    const t = task.timing!
    expect(t).toBeDefined()
    expect(t.total_ms - t.api_ms - t.tool_span_ms).toBe(t.other_ms)
    expect(t.tool_ms).toBeGreaterThan(t.tool_span_ms)
    expect(t.partial).toBe(false)
    const bash = t.buckets!.find((b) => b.label === 'Bash')!
    expect(bash.sub!.map((s) => s.label)).toEqual(['go test', 'git status'])
    for (const s of bash.sub!) expect(s.sub).toBeUndefined()
  })

  // 注意 TS 侧 TasksResp.tasks 是扁平的 Task[]（Go 的 TaskView 内嵌 Task，
  // JSON 把它摊平了），不是 { task, watchers } 的嵌套形状
  it('TasksResp 的任务不带 timing（ListTasks 不填，夹具必须与那个事实一致）', () => {
    const resp = tasksRespFixture as TasksResp
    expect(resp.tasks.length).toBeGreaterThan(0)
    for (const t of resp.tasks) {
      expect(Object.keys(t)).not.toContain('timing')
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
  // 任务流（useTasks）就吃这个信封。钉两件事：远端条目带 machine 章、
  // 答不上来的机器在 machines 里 ok=false 带原因——后者是「看板上的空
  // 到底是没任务还是没拉到」的唯一区分依据。
  it('TasksResp 信封：远端任务带 machine 章，失联机器 ok=false 带原因', () => {
    const resp = tasksRespFixture as TasksResp
    expect(Array.isArray(resp.tasks)).toBe(true)
    expect(resp.machines.map((m) => m.name)).toEqual(['', 'devbox'])
    const down = resp.machines.find((m) => m.name === 'devbox')!
    expect(down.ok).toBe(false)
    expect(down.error).not.toBe('')
    const remote = resp.tasks.find((t) => t.machine !== '')
    expect(remote?.machine).toBe('devbox')
  })

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

  it('B205 建树请求与逐卡挂接结果的可选字段在线格式一致', () => {
    const req = createWorktreeReqFixture as CreateWorktreeReq
    expect(req.card_ids).toEqual(['B205', 'B205.1'])
    const legacyReq = createWorktreeReqEmptyFixture as CreateWorktreeReq
    expect(legacyReq.card_ids).toBeUndefined()

    const ws: Workspace = workspaceCardResultsFixture
    expect(ws.card_results).toEqual([
      { id: 'B205', ok: true },
      { id: 'B206', ok: false, error: '卡已派发：基线已冻结' },
    ])
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

describe('Env 文件契约', () => {
  it('EnvResp 两档都在线格式里，off 档不带 file 键', () => {
    const resp = envRespFixture as EnvResp
    expect(resp.bindings.map((b) => b.mode).sort()).toEqual(['file', 'off'])
    const off = resp.bindings.find((b) => b.mode === 'off')!
    expect(off.file).toBeUndefined()
    const file = resp.bindings.find((b) => b.mode === 'file')!
    expect(file.file).toBe('proxy.env')
    // env 没有内置默认：响应里不得出现 builtins/default_tier 这类 discipline 概念
    expect('builtins' in envRespFixture).toBe(false)
    expect('default_tier' in (off as object)).toBe(false)
  })

  it('EnvKeysResp：只有 key 名与值长度，值不在线格式里', () => {
    const resp = envKeysFixture as EnvKeysResp
    expect(resp.keys.map((k) => k.key)).toEqual(['HTTPS_PROXY', 'GOPROXY', 'EMPTY_ONE'])
    // 值为空的那条也必须带 value_bytes: 0（int 无 omitempty），否则界面判不出「空值」
    expect(resp.keys[2].value_bytes).toBe(0)
    expect(resp.keys[1].duplicate).toBe(true)
    // 结构性判据：整份 fixture 里没有任何名为 value/content 的键
    const raw = JSON.stringify(envKeysFixture)
    expect(raw).not.toMatch(/"value"|"content"/)
  })

  it('EnvMappingReq：整段替换，两条 binding', () => {
    const req = envMappingReqFixture as EnvMappingReq
    expect(req.bindings).toHaveLength(2)
  })
})

describe('需求 B 启动项契约', () => {
  it('LaunchersResp：三种合法形态，env_missing 是必有键不是 omitempty', () => {
    const resp: LaunchersResp = launchersRespFixture
    expect(resp.launchers).toHaveLength(3)
    // 三种合法形态：只带 env / 只带命令 / 两者都带。**没有第四种**——
    // 两者都空与「新终端」完全等价，服务端会 400 拒掉
    expect(resp.launchers[0].command).toBeUndefined()
    expect(resp.launchers[1].env_file).toBeUndefined()
    expect(resp.launchers[2].env_file).toBeDefined()
    expect(resp.launchers[2].command).toBeDefined()
    // env_missing 不带 omitempty：false 也必须在线格式里出现。
    // 前端靠「缺键」与「false」的区别判断服务端认不认识这个字段——
    // 这条断言用 in 而不是取值，取值分不出 false 与 undefined
    for (const l of resp.launchers) expect('env_missing' in l).toBe(true)
    expect(resp.launchers[0].env_missing).toBe(true)
    expect(resp.launchers[1].env_missing).toBe(false)
  })

  it('CreatePtySessionReq：rel 与两个新字段都在线格式里', () => {
    const req: CreatePtySessionReq = createPtySessionReqFixture
    // rel 是这次补进 TS 声明的字段：Go 侧一直有，TS 侧一直漏，
    // 因为调用点用对象展开写法绕过了超额属性检查。这条断言是它的看守
    expect(req.rel).toBe('web')
    expect(req.env_file).toBe('proxy.env')
    expect(req.init_command).toBe('npm run dev')
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
    expect(f.dur_ms).toBe(1500)
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
    expect(resp.entries).toHaveLength(3)
    const [dir, file] = resp.entries
    expect(dir.is_dir).toBe(true)
    // 目录的 size 被 omitempty 省略：缺键而不是 0
    expect(dir.size).toBeUndefined()
    expect(file.is_dir).toBe(false)
    expect(file.size).toBe(1284)
  })

  it('ignored 只在被忽略时出现：未忽略的条目是缺键而不是 false', () => {
    const resp: DirListResult = dirListFixture
    const [dir, file, ignored] = resp.entries
    expect(ignored.ignored).toBe(true)
    expect('ignored' in dir).toBe(false)
    expect('ignored' in file).toBe(false)
  })
})

describe('TaskPlan 契约', () => {
  it('派发指令原文带文件名与真实大小；未截断时 truncated 缺席', () => {
    const plan: TaskPlan = taskPlanFixture
    expect(plan.name).toBe('b119-dispatch.md')
    expect(plan.content).toContain('执行纪律')
    expect(plan.size).toBe(36)
    expect('truncated' in plan).toBe(false)
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

describe('执行纪律契约', () => {
  it('DisciplineResp 三档与内置两版都在线格式里', () => {
    const resp = disciplineRespFixture as DisciplineResp
    const req = disciplineMappingReqFixture as DisciplineMappingReq
    expect(resp.builtins.map((b) => b.tier)).toEqual(['subagent', 'single-context'])
    expect(resp.bindings.map((b) => b.mode).sort()).toEqual(['default', 'file', 'off'])
    // mode=default 的条目不带 file 键（omitempty），但 default_tier 必须在
    const def = resp.bindings.find((b) => b.mode === 'default')!
    expect(def.file).toBeUndefined()
    expect(def.default_tier).toBe('subagent')
    expect(req.bindings).toHaveLength(2)
  })
})

describe('缺省执行者契约', () => {
  it('ExecutorDefaultResp：available 是升序名单，default 在其中', () => {
    const resp = executorDefaultRespFixture as ExecutorDefaultResp
    expect([...resp.available].sort()).toEqual(resp.available)
    expect(resp.available).toContain(resp.default)
  })

  it('ExecutorDefaultReq：model 空串必须在场，不能被 omitempty 吃掉', () => {
    // 缺了这个键，前端就没法表达「清空默认模型」——只能表达「不改」
    expect('model' in executorDefaultReqFixture).toBe(true)
    expect((executorDefaultReqFixture as ExecutorDefaultReq).model).toBe('')
  })
})

describe('工作台状态契约', () => {
  it('WorkbenchStateResp 的 payload 是字符串而不是嵌套对象', () => {
    const base: WorkbenchBaseRow = workbenchBaseFixture
    expect(typeof base.base_key).toBe('string')
    // 这条断言是本用例存在的理由：payload 一旦被写成嵌套对象，
    // persist.ts 里的 JSON.parse 会在运行时炸，而 typecheck 未必拦得住
    expect(typeof base.payload).toBe('string')
    expect(typeof base.updated_at).toBe('number')

    const state: WorkbenchStateResp = workbenchStateFixture
    expect(typeof state.selected).toBe('string')
    expect(typeof state.dock).toBe('string')
    expect(Array.isArray(state.bases)).toBe(true)
    expect(typeof state.bases[0].payload).toBe('string')
  })
})

describe('scheduling wire', () => {
  it('SquadsResp 携带版本行与显式布尔', () => {
    const c: CarrierView = squadsFixture.carriers[0]
    expect(c.version).toBeGreaterThan(0)
    expect(c.healthy).toBe(true)
    expect(c.max_concurrency).toBe(2)
    const s: SquadView = squadsFixture.squads[0]
    expect(s.role).toBe('coordinator')
    expect(s.max_concurrency).toBeUndefined() // omitempty：0 以键缺席表达
  })
  it('QueueEntry 的 ready=false 显式在场且位次为正', () => {
    const e: QueueEntry = queueFixture.queue[0]
    expect(e.ready).toBe(false) // 样本即 false；缺键会在 tsc/undefined 处暴露
    expect(e.position).toBeGreaterThan(0)
    expect(e.kind).toBe('ignition_queue')
  })
  it('CoordinatorLaunchResp 关键字段', () => {
    const r: CoordinatorLaunchResp = coordinatorLaunchFixture
    expect(r.woke).toBe(true)
    expect(typeof r.session_id).toBe('string')
  })
})
