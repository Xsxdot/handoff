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

export interface Repo {
  name: string
  path: string
  origin_url: string
  created_at: string
  status?: string
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
