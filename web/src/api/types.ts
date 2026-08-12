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
  machine: string      // ""=本机；否则为本机 cfg.Targets 的键，由汇总方盖章（W3a §3）
  project_id: string   // 归属项目；未归属为 ""（W3a §1.3）
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
}

export interface MachinesResp {
  machines: Machine[]
}

// CreateProjectReq 是 POST /api/projects 的请求体。B62 spec §6.3。
//   带 path  = 登记该机器上已有目录（本机永远走这条）
//   不带 path = 由该机器 clone 到自己的 repo_root/<name>
export interface CreateProjectReq {
  origin_url: string
  name?: string
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

// fileResult 是 file 接口的响应体。
export interface FileResult {
  content: string
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
