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
