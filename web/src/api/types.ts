// 与 agentd 的线格式类型一一对应的 TS 类型。
//
// 这些类型是 internal/proto 在 web 侧的镜像，唯一的共同基准是
// web/src/api/testdata/*.json（由 Go 侧测试生成并逐字节钉住，vitest 反向断言）。
// 字段改名/增删都必须同时改 Go 结构体、fixture 与这里的类型，任一漏改都会让
// 某一侧的契约测试变红。
//
// 时间约定：agentd 把所有 time.Time 序列化为 RFC3339Nano 字符串（如
// "2026-08-11T10:30:00+08:00"），本层原样透传为 string，不做 Date 转换——
// 展示格式是界面层的事。
//
// 注意：Go 侧指针字段（*string / *time.Time）序列化为「缺席」而非 null，
// 因此这里统一标记为可选（?）而不是 string | null。

export interface Task {
  id: string
  target: string
  repo_path: string
  branch: string
  plan_path: string
  plan_summary: string
  executor_session: string
  state: string
  created_at: string
  updated_at: string
  name: string
  executor: string
  model: string
  work_dir: string
  worktree_managed: boolean
  base_commit: string
  base_ahead: number
  repo_dirty_count: number
  repo_dirty_files: string
  done_note: string    // 归档时审核者留的完成说明（handoff done --note）；""=未留说明或归档于该功能之前
  actual_model?: string   // executor 报回的实际模型名；缺省=还没报（与入参 model 不是一回事）
  usage?: Usage           // 当前 context 占用；缺省=还没有任何一次模型调用完成
  cumulative?: Cumulative  // 累计消耗；缺省=还没有账目，或本次是列表读取
  machine: string      // ""=本机；否则为本机 cfg.Targets 的键，由汇总方盖章（W3a §3）
  project_id: string   // 归属项目；未归属为 ""（W3a §1.3）
}

// Usage 是任务当前的 context 占用。
// context_window 缺省表示该 executor 不在协议里报窗口（claudecode / opencode），
// 此时只显绝对值——前端绝不自己猜分母。
export interface Usage {
  context_tokens: number
  context_window?: number
}

// Cost 是累计花费及其可信度。
// state 为 'partial' 时 ticks 只是**已知部分**的和，是下界不是总额。
export interface Cost {
  ticks: number   // 1 USD = 10^10 ticks
  state: 'reported' | 'estimated' | 'partial' | 'unknown'
}

// Cumulative 是任务的累计消耗。
// 与 Usage 是两个口径：Usage 是「现在占用多少」，本结构是「一共烧了多少」。
// 只在任务详情（GetTask）里有，列表接口不带。
export interface Cumulative {
  input_tokens: number   // 未命中缓存的输入
  cached_tokens: number  // 命中缓存的输入
  output_tokens: number  // 含 reasoning
  total_tokens: number   // 三项之和，后端算好
  cost?: Cost            // 缺省=还没有任何花费信息
}

export interface Event {
  seq: number
  task_id: string
  type: string
  payload: unknown
  created_at: string
}

export interface Ticket {
  id: string
  task_id: string
  kind: string
  request: unknown
  answer?: string
  created_at: string
  answered_at?: string
  delivered_at?: string
}

// ProjectLocation 是一条「项目 × 机器」位置记录：项目在本机的那一个工作副本
// （B62 的 project_locations 表）。GET /api/projects 返回它的数组，
// POST /api/projects 登记成功时 200 返回单条。
//
// project_id = sha256(归一化 origin) 前 16 位，跨机同一；name 只是本机内唯一
// 的人可读引用，不参与身份判定。status 是 list 时现场探得的实际状态
// （"有效"/"路径不存在"/"不是 git 仓库"），不落库，故为可选。
export interface ProjectLocation {
  project_id: string
  name: string
  path: string
  origin_url: string
  created_at: string
  status?: string
}

// CreateProjectResp 是登记成功的响应体：**200**（不是 201），体是完整的
// ProjectLocation——B62 实现如此。export type 直接复用 ProjectLocation。
export type CreateProjectResp = ProjectLocation

// Workspace 是一个 git 工作树（含主工作区自身）。W3a §2。
export interface Workspace {
  path: string
  branch: string    // detached 时为空串
  head: string      // 短 sha
  is_main: boolean
  managed: boolean  // true = agentd 自建的任务工作树
}

// ProjectLocationNode 是一个项目在一台机器上的位置。
// 不变式：单机响应里每个项目的 locations 恒为 0 或 1 条（W3a §1.1）。
export interface ProjectLocationNode {
  machine: string          // ""=本机；否则为 cfg.Targets 的键
  name: string             // 登记名
  path: string
  workspaces: Workspace[]
  probe_error: string      // 探测失败的人话说明，空串=正常
}

export interface ProjectNode {
  project_id: string
  origin_url: string
  name: string
  locations: ProjectLocationNode[]
}

// MachineStatus 是跨机汇总信封里的每台机器的应答情况。W3a §5.3。
// 硬约束：任何一台没答上来都必须出现在这里且 ok=false 带原因。
export interface MachineStatus {
  name: string
  ok: boolean
  fetched_at: string
  error: string
}

// TasksResp 是 GET /api/tasks?scope=all 的跨机汇总信封（W3a §5.3）。
//
// 与 ProjectTreeResp / PtySessionsResp 不同，这里的 machines 不是可选的：
// 汇总端点恒返回它（每台 target 都有一行，答不上来的那台 ok=false 带原因）。
// 不带 scope=all 的 GET /api/tasks 是另一种形状——裸数组，不走本类型。
//
// tasks 里远端条目的 machine 由本机 agentd 盖章，本机条目为空串；数据取自
// 镜像快照，新旧看对应 machines 行的 fetched_at。
export interface TasksResp {
  machines: MachineStatus[]
  tasks: Task[]
}

// ProjectTreeResp 是 GET /api/projects/tree 的响应；
// machines 仅在 ?scope=all 时出现。
export interface ProjectTreeResp {
  projects: ProjectNode[]
  unowned: string[]           // 算不出 project_id 的脏行（登记名），诚实列出不吞
  machines?: MachineStatus[]
}

// Machine 是 GET /api/machines 的单台投影。W3a §4。
export interface Machine {
  name: string              // ""=本机
  addr: string
  reachable: boolean
  version: string
  executors: string[]
  default_executor: string
  probe_ms: number          // 本机恒 0（进程内直查）
  active_tasks: number
  error: string             // reachable=false 时必非空
  // pty_supported 三态：缺席/null = 对端没上报（**不是**不支持），
  // false = 平台明确不支持，true = 支持。
  pty_supported?: boolean | null
  // reveal_supported 三态同 pty_supported：缺席/null = 对端没上报（**不是**不支持）。
  // 注意它只是**平台**支持度——真能不能揭示还要看浏览器是不是和 agentd 在同一台
  // 机器上，那一层由 FileTree 用 location.hostname 判（spec §4.3）。
  reveal_supported?: boolean | null
}

export interface MachinesResp {
  machines: Machine[]
}

// AddMachineReq 是 POST /api/machines 的请求体。
//
// token 只进不出：后端接受它，但 Machine 类型没有对应字段，任何响应里
// 都不会回显。force=true 跳过后端的可达性探测（对端临时离线时用）。
export interface AddMachineReq {
  name: string
  addr: string
  token: string
  user: string
  force?: boolean
}

// CreateProjectReq 是 POST /api/projects 的请求体。B62 spec §6.3。
// 形态由 path / 路径是否存在 / origin_url 是否非空共同决定：
//   - path 有且目录已存在 → 登记已有仓（origin_url 可省，省则 agentd 现读 origin）
//   - path 有且目录不存在 + origin_url 有 → clone 到该 path
//   - path 空 + origin_url 有 → 由该机 clone 到自己的 repo_root/<name>
//   - 其余组合（path 空且无 origin、path 不存在且无 origin）→ 400
// 调用方只放非空字段——agentd 对空串与缺席一视同仁，但缺席更干净。
export interface CreateProjectReq {
  origin_url?: string
  name?: string
  path?: string
}

// PatchProjectReq 是 PATCH /api/projects/{name} 的请求体。B95。
// 两个字段都可选、不能都空：new_name 改引用名，path 改路径。
export interface PatchProjectReq {
  new_name?: string
  path?: string
}

export interface AuthTicketResp {
  url: string
  expires_at: string
}

export interface SessionInfo {
  id: string
  device_name: string
  created_at: string
  expires_at: string
  last_seen_at: string
  revoked_at?: string
}

export interface BuildInfo {
  version?: string
  revision: string
  time: string
  modified: boolean
  go: string
}

export type Live = 'alive' | 'dead' | 'unknown'

export interface ActiveTask {
  id: string
  name: string
  state: string
  executor: string
  repo_path: string
  live: Live
  note: string
}

export interface StatusResp {
  version: BuildInfo
  listen: string
  data_dir: string
  started_at: string
  executors: string[]
  default_executor: string
  task_counts: Record<string, number>
  active: ActiveTask[]
  // 缺席 = 对端 agentd 没上报（版本过旧），**不等于 false**。见 types 头注释的三态约定。
  pty_supported?: boolean
  reveal_supported?: boolean
}

// taskDetail 是 GET /api/tasks/{id} 的响应体（任务 + 待办工单 + 最近事件）。
// PendingTickets / RecentEvents 在 agentd 侧归一化为 [] 而非 null，这里仍标记
// 为必填数组（契约测试锁死）。
export interface TaskDetail {
  task: Task
  pending_tickets: Ticket[]
  recent_events: Event[]
}

// TicketRequest 是工单 request 字段的**可读视图**。
//
// 注意：request 在 agentd 侧是 json.RawMessage（{"kind":"gate","permission":…}
// 或 {"kind":"ask","question":…}），事件 payload 里的 permission 是截断过的
// 摘要，全文只在工单里——展示必须解析本对象，不要读事件。
export interface TicketRequest {
  kind?: string
  permission?: string
  question?: string
  [key: string]: unknown
}

// replyRequest 是 POST /api/tasks/{id}/reply 的请求体；answer 的编码契约见
// app/task/review.ts 的 buildTicketAnswer（批准恒为 "allow"，拒绝必须带理由）。
export interface ReplyRequest {
  ticket_id: string
  answer: string
}

// replyResult 是 reply 接口的响应体；Relayed=false 表示「已落库但 executor 侧
// 递送失败」，此时 HTTP 状态码是 502，Reason 给出可行动的原因。
export interface ReplyResult {
  ok: boolean
  relayed?: boolean
  reason?: string
}

// stopResult 是 stop 接口的响应体；worktree_removed 如实反映 managed worktree
// 是否被删除（false=用户自带 worktree / 原地模式或清理失败）。
export interface StopResult {
  status: string
  worktree_removed: boolean
}

// runResult 是 run 接口的响应体；非零退出也是 200，退出码在响应体里
// （10 分钟超时会被杀，退出码 124）。
export interface RunResult {
  stdout: string
  exit_code: number
}

// diffResult 是 diff 接口的响应体。
export interface DiffResult {
  diff: string
}

// TaskPlan 是 GET /api/tasks/{id}/plan 的响应：派发当刻交给 executor 的指令原文。
//
// size 是磁盘真实大小而非 content.length：truncated 为真时两者不同，
// 要显示给人看的是真实大小。
export interface TaskPlan {
  name: string
  content: string
  size: number
  truncated?: boolean
}

// BranchesResult 是 GET /api/tasks/{id}/branches 的响应：本地分支名 + 推导默认。
// default 为空串 = 推导不出，前端下拉退化为仅「自动推导」项。
export interface BranchesResult {
  branches: string[]
  default: string
}

// fileResult 是 file 接口的响应体。
export interface FileResult {
  content: string
}

// FileRead 是 GET /api/workspaces/file 的响应体（proto.FileRead 的镜像）。
//
// sha256 只在**完整且是文本**时有值。空值即「这文件不可编辑」——前端拿它当
// 三态判据，不要另外按扩展名猜二进制，那必然与服务端分叉。
export interface FileRead {
  content: string
  size: number
  truncated?: boolean
  binary?: boolean
  sha256?: string
}

// FileWriteReq 是 PUT /api/workspaces/file 的请求体。
export interface FileWriteReq {
  content: string
  base_sha256: string
}

// FileWriteResp 是写入成功的响应；sha256 直接当下一次写入的 base_sha256。
export interface FileWriteResp {
  sha256: string
  size: number
}

// FileConflictResp 是 409 的响应体，current 是磁盘现状。
export interface FileConflictResp {
  error: string
  current: FileRead
}

// resumeResult 是 resume 接口的响应体（RecoverReport 的镜像）。
export interface ResumeResult {
  task: string
  redelivered: number
  executor_gone: boolean
  reconciled: boolean
  turn_ended: boolean
  emitted: number
  forced: boolean
  state: string
  note: string
}

// FrameType 是结构化回合帧的类型（W4a §3.2）。
//
// 刻意用 `string` 而不是收窄的 union：前端比后端晚部署是常态，契约新增一种帧
// 时旧前端必须还能解析并渲染成中性条目，而不是在类型层就把它判为非法。
// 已知取值集中在 KNOWN_FRAME_TYPES，供渲染层分发。
export type FrameType = string

// KNOWN_FRAME_TYPES 是本前端版本认识的帧类型。不在其中的一律走「未知类型」分支。
export const KNOWN_FRAME_TYPES = ['text', 'reasoning', 'tool_call', 'tool_result', 'event', 'turn_start'] as const

// Frame 是 frames.jsonl 的一行，也是 GET /api/tasks/{id}/frames 流的一行。
//
// 与 internal/proto.Frame 一一对应。Go 侧带 omitempty 的字段在这里都是可选（?）
// 而不是 `| null`——它们缺席而不是取空值（contract.test.ts 钉住了这一点）。
//
// 两套 seq 不要混用：`seq` 是**任务内**从 1 开始的帧行号；`ref_seq` 只出现在
// `event` 帧上，指向 events 表的**库级**自增 seq。
//
// 配对与拼接都靠 `part`：`text`/`reasoning` 按 part 拼接增量，`tool_call` 与其
// `tool_result` 用同一个 part 配对。part 只在**同一回合内**唯一，跨回合会重复，
// 所以任何以 part 为键的索引都必须带上 turn。
export interface Frame {
  seq: number
  ts: string
  turn: number
  type: FrameType
  part?: string
  delta?: string
  tool?: string
  input?: string
  output?: string
  status?: string
  truncated?: boolean
  bytes?: number
  ref_seq?: number
  event?: string
  reason?: string
  // turn_start（send）携带的审核者指令原文；dispatch 与旧帧缺席
  instructions?: string
}

// DirEntry 是 GET /api/workspaces/dir 列举出的一项。
//
// size 只对普通文件存在（Go 侧 omitempty）：目录是**缺键**而不是 0，
// 前端不要用 `entry.size ?? 0` 去掩盖这个区别——它是「这是目录」的第二个证据。
export interface DirEntry {
  name: string
  is_dir: boolean
  size?: number
  // ignored 为真表示被 .gitignore 排除（服务端跑的是 git check-ignore）。
  // 与 size 同理：false 被 omitempty 省略，**缺键 = 未被忽略**。服务端查不出来
  // 时也一律不带这个键（fail open），所以前端不能把缺席读成「一定不是垃圾」，
  // 只能读成「没有理由把它弱化」——正是弱化展示该有的保守方向。
  ignored?: boolean
}

// DirListResult 是 GET /api/workspaces/dir 的响应体；entries 永不为 null。
export interface DirListResult {
  entries: DirEntry[]
}

// SearchHit 是搜索命中的一行（GET /api/workspaces/search）。
export interface SearchHit {
  rel: string
  line: number
  text: string
}

// SearchResult 是搜索的完整结论；truncated 为真表示结果被截断
// （撞到条数上限或超时，服务端只返回了已有部分）。
export interface SearchResult {
  hits: SearchHit[]
  truncated: boolean
}

// PtySession 是一个 PTY 终端会话（W4 PTY 终端 spec §3.1）。
//
// exit_code 缺席 = 会话还活着（Go 侧是 *int + omitempty）。不要写
// `session.exit_code ?? 0`——那会把「跑着的会话」显示成「正常退出」。
export interface PtySession {
  id: string
  machine: string        // ""=本机；否则为汇总方 cfg.Targets 的键
  base_path: string
  base_kind: string      // 'workspace' | 'home'
  shell: string
  created_at: string
  cols: number
  rows: number
  attached: number
  pid: number
  exit_code?: number
  foreground: boolean    // 有前台命令在跑，控制台据此在关 tab 前先确认
  bytes_out: number      // /ws/pty 的 since 水位
}

export interface PtySessionsResp {
  sessions: PtySession[]
  machines?: MachineStatus[]
}

export interface CreatePtySessionReq {
  base_path: string
  base_kind: string
  cols: number
  rows: number
}

// PtyControl 是 /ws/pty 上的 text 帧。二进制帧是 PTY 原始字节，不经过这里。
export interface PtyControl {
  type: string           // 'attached' | 'exit' | 'error' | 'resize'
  since: number
  truncated: boolean
  exit_code?: number
  message?: string
  cols?: number
  rows?: number
}
