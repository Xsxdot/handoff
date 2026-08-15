// agentd 的 HTTP fetch 客户端（浏览器侧）。
//
// 职责：
//   - 封装 /api/status 与 /api/tasks 两个只读接口，返回强类型结果
//   - 出错时把状态码与响应体错误信息统一成可读的 ApiError，供界面展示
//
// 边界：
//   - 不带任何凭据逻辑：浏览器经 vite 反代访问同源 /api，cookie 由浏览器
//     自动携带，本层不碰 cookie、不碰 token、不打印任何凭据明文
//
// 错误处理约定：所有 fetch 失败（网络、非 2xx）都抛 ApiError 而不是静默吞掉，
// 上层按「可观察的失败」渲染——鉴权失败（401）特别提示重新 handoff console。
// agentd 的错误消息是中文且信息量大（「任务不存在」「任务当前状态不允许该操作」
// 「基线提交在任务仓库中不存在……请先在本地 git push」），必须原文透传，不得
// 吞成一句「操作失败」——那些消息里带着解法。
import type {
  CreateProjectReq,
  CreateProjectResp,
  CreatePtySessionReq,
  DiffResult,
  DirListResult,
  FileRead,
  FileResult,
  FileWriteReq,
  FileWriteResp,
  MachinesResp,
  PatchProjectReq,
  ProjectLocation,
  ProjectTreeResp,
  PtySession,
  PtySessionsResp,
  ReplyRequest,
  ReplyResult,
  ResumeResult,
  RunResult,
  StatusResp,
  StopResult,
  Task,
  TaskDetail,
} from './types'

// ApiError 携带 HTTP 状态码、agentd 返回的 error 字段，以及**完整响应体**。
//
// 为什么要留 body：409 的响应体除了 error 还带着 current（磁盘现状），冲突界面
// 的两个出口都要用它——「放弃我的改动」要它的正文，「用我的内容覆盖」要它的
// 哈希当新基线。只留一个 message 就得为了拿现状再发一次请求，而两次请求之间
// 又是一个新窗口。
//
// 参数：
//   - status: HTTP 状态码；0 表示请求根本没到 agentd（网络/反代层失败）
//   - message: 人类可读的原因
//   - body: 已解析的响应体；解析不出时为 undefined
export class ApiError extends Error {
  readonly status: number
  readonly body: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

// bodyOrError 从非 2xx 响应里提取 agentd 的 {"error": "…"} 原文与完整响应体；
// 读不到时回退到「状态码 + 状态文本」兜底文案。
async function bodyOrError(resp: Response): Promise<{ detail: string; body: unknown }> {
  try {
    const body = (await resp.json()) as { error?: string }
    return { detail: body.error ?? '', body }
  } catch {
    // 响应体不是 JSON 时用兜底文案，body 留 undefined
    return { detail: '', body: undefined }
  }
}

// parseResponse 统一处理一次 fetch 的完成态：非 2xx 抛 ApiError（原文透传），
// 2xx 返回解析后的 JSON。
async function parseResponse<T>(resp: Response): Promise<T> {
  if (resp.status === 401) {
    throw new ApiError(401, '未授权：浏览器会话已失效，请重新执行 handoff console 兑换 cookie')
  }
  if (!resp.ok) {
    const { detail, body } = await bodyOrError(resp)
    throw new ApiError(resp.status, detail || `agentd 返回 ${resp.status} ${resp.statusText}`, body)
  }
  return (await resp.json()) as T
}

// request 执行一次带鉴权上下文的请求，统一处理非 2xx 与网络错误。
//
// 参数：
//   - path: 以 /api 开头的路径（反代会把同源路径转给 agentd）
//   - init: 传给 fetch 的额外选项（method / body / headers）
//
// 返回：
//   - 解析后的 JSON；类型由调用方指定
//
// 注意：
//   - 401 的 message 特意写明「重跑 handoff console 换新 cookie」——这是
//     浏览器会话过期/被吊销时唯一可行动的动作，静默失败会让人无从下手
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let resp: Response
  try {
    resp = await fetch(path, { credentials: 'same-origin', ...init })
  } catch (err) {
    throw new ApiError(0, `无法连接 agentd（反代失败？）：${err instanceof Error ? err.message : String(err)}`)
  }
  return parseResponse<T>(resp)
}

// postJSON 以 JSON body 发起 POST 请求。
function postJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// putJSON 以 JSON body 发起 PUT 请求。
function putJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// patchJSON 以 JSON body 发起 PATCH 请求。
function patchJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// fetchStatus 取 agentd 的可用性与身份信息（GET /api/status）。
export function fetchStatus(): Promise<StatusResp> {
  return request<StatusResp>('/api/status')
}

// fetchTasks 取全部任务（GET /api/tasks；空列表返回 [] 而非 null）。
export function fetchTasks(): Promise<Task[]> {
  return request<Task[]>('/api/tasks')
}

// fetchTaskDetail 取任务详情（GET /api/tasks/{id}）：任务 + 待办工单 + 最近事件，
// 后两者空时也是 []。
export function fetchTaskDetail(id: string): Promise<TaskDetail> {
  return request<TaskDetail>(`/api/tasks/${encodeURIComponent(id)}`)
}

// fetchTaskDiff 取任务分支相对基准的改动（GET /api/tasks/{id}/diff，base 可省）。
export function fetchTaskDiff(id: string, base?: string): Promise<DiffResult> {
  const q = base ? `?base=${encodeURIComponent(base)}` : ''
  return request<DiffResult>(`/api/tasks/${encodeURIComponent(id)}/diff${q}`)
}

// fetchTaskFile 读任务仓库内单个文件（GET /api/tasks/{id}/file?path=）。
export function fetchTaskFile(id: string, path: string): Promise<FileResult> {
  return request<FileResult>(
    `/api/tasks/${encodeURIComponent(id)}/file?path=${encodeURIComponent(path)}`,
  )
}

// runTaskCommand 在任务仓库执行审阅命令（POST /api/tasks/{id}/run）：
// 非零退出也是 200，退出码在响应体里；10 分钟超时退出码为 124。
export function runTaskCommand(id: string, cmd: string): Promise<RunResult> {
  return postJSON<RunResult>(`/api/tasks/${encodeURIComponent(id)}/run`, { cmd })
}

// replyTicket 回答一张工单（POST /api/tasks/{id}/reply）。
// answer 的编码契约见 app/task/review.ts：批准恒为 "allow"，拒绝必带理由。
export function replyTicket(id: string, req: ReplyRequest): Promise<ReplyResult> {
  return postJSON<ReplyResult>(`/api/tasks/${encodeURIComponent(id)}/reply`, req)
}

// continueTask 向任务续发修改指令（POST /api/tasks/{id}/continue，仅
// waiting_review 可用）。
export function continueTask(id: string, instructions: string): Promise<{ ok: boolean }> {
  return postJSON<{ ok: boolean }>(`/api/tasks/${encodeURIComponent(id)}/continue`, { instructions })
}

// doneTask 归档任务（POST /api/tasks/{id}/done，仅 waiting_review 可用）。
export function doneTask(id: string): Promise<{ ok: boolean }> {
  return postJSON<{ ok: boolean }>(`/api/tasks/${encodeURIComponent(id)}/done`, {})
}

// stopTask 主动中止任务（POST /api/tasks/{id}/stop）：停 executor、作废挂起
// 工单、落 failed；worktree_removed 如实反映 managed worktree 是否被删除。
export function stopTask(id: string): Promise<StopResult> {
  return postJSON<StopResult>(`/api/tasks/${encodeURIComponent(id)}/stop`, {})
}

// resumeTask 显式恢复卡死的任务（POST /api/tasks/{id}/resume[?force=true]）：
// 重投已落库但未送达的应答，force 时强制收口到 waiting_review。
export function resumeTask(id: string, force = false): Promise<ResumeResult> {
  const q = force ? '?force=true' : ''
  return postJSON<ResumeResult>(`/api/tasks/${encodeURIComponent(id)}/resume${q}`, {})
}

// machineQuery 把机器名编码成查询串；空串（本机）不带参数。
//
// 为什么用查询参数而不是请求体字段：登记请求体是 B62 定的、由本机 agentd
// 原样转发给目标机，往里塞路由字段会污染 B62 的契约。
function machineQuery(machine?: string, sep: '?' | '&' = '?'): string {
  return machine ? `${sep}machine=${encodeURIComponent(machine)}` : ''
}

// fetchProjectTree 取项目树（GET /api/projects/tree）。
//
// 参数：
//   - scope: 传 'all' 取跨机汇总版（响应多一个 machines 字段，见 §5.3）
//
// 注意：本接口带 git worktree 现场探测，**不要放进 2.5s 热路径**。
export function fetchProjectTree(scope?: 'all'): Promise<ProjectTreeResp> {
  return request<ProjectTreeResp>(`/api/projects/tree${scope === 'all' ? '?scope=all' : ''}`)
}

// fetchMachines 取机器投影与探活结果（GET /api/machines）。
// 单台不可达是数据不是错误：整体仍 200，该台 reachable=false 且 error 带原文。
export function fetchMachines(): Promise<MachinesResp> {
  return request<MachinesResp>('/api/machines')
}

// workspaceQuery 拼两个工作树接口共用的查询串。
//
// rel 省略表示工作树根；machine 省略或空串 = 本机（与 Task.machine 的空串语义一致）。
function workspaceQuery(path: string, rel?: string, machine?: string): string {
  const q = new URLSearchParams({ path })
  if (rel) q.set('rel', rel)
  if (machine) q.set('machine', machine)
  return q.toString()
}

// fetchWorkspaceDir 列举工作树内一层目录（GET /api/workspaces/dir）。
//
// path 必须是 GET /api/projects/tree 给出的某个 Workspace.path 原样值——
// agentd 侧按等值比对做白名单，任意路径返回 400（spec §7.1）。
export function fetchWorkspaceDir(path: string, rel?: string, machine?: string): Promise<DirListResult> {
  return request<DirListResult>(`/api/workspaces/dir?${workspaceQuery(path, rel, machine)}`)
}

// fetchWorkspaceFile 读工作树内单个文件（GET /api/workspaces/file）。
//
// 返回的是完整 FileRead 而不是只有 content：写回需要 sha256 当基线，
// 三态展示需要 binary / truncated / size。
export function fetchWorkspaceFile(path: string, rel: string, machine?: string): Promise<FileRead> {
  return request<FileRead>(`/api/workspaces/file?${workspaceQuery(path, rel, machine)}`)
}

// writeWorkspaceFile 写回工作树内单个文件（PUT /api/workspaces/file）。
//
// 参数：
//   - req.base_sha256: **上一次读到那一版**的哈希；对不上时抛 409 的 ApiError，
//     其 body 是 FileConflictResp（带磁盘现状）
//
// 注意：成功返回的 sha256 就是下一次写入的 base_sha256，不需要再读一次
export function writeWorkspaceFile(
  path: string,
  rel: string,
  req: FileWriteReq,
  machine?: string,
): Promise<FileWriteResp> {
  return putJSON<FileWriteResp>(`/api/workspaces/file?${workspaceQuery(path, rel, machine)}`, req)
}

// createProject 登记一个项目位置（POST /api/projects）。
//
// 参数：
//   - req: 带 path = 登记该机已有目录；不带 path = 由该机 clone 到自己的 repo_root
//   - machine: 目标机器名；省略或空串 = 本机
export function createProject(req: CreateProjectReq, machine?: string): Promise<CreateProjectResp> {
  return postJSON<CreateProjectResp>(`/api/projects${machineQuery(machine)}`, req)
}

// deleteProject 注销一个项目位置（DELETE /api/projects/{name}）。
// 只解除登记，不删除磁盘上的代码。
export function deleteProject(name: string, machine?: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(
    `/api/projects/${encodeURIComponent(name)}${machineQuery(machine)}`,
    { method: 'DELETE' },
  )
}

// patchProject 改一个项目位置的引用名或路径（PATCH /api/projects/{name}）。
//
// req 的 new_name/path 均可选但不能都空；name 是**当前**引用名（旧名）——
// 改名也是按旧名寻址这条资源。machine 为目标机器名，省略或空串 = 本机。
export function patchProject(name: string, req: PatchProjectReq, machine?: string): Promise<ProjectLocation> {
  return patchJSON<ProjectLocation>(`/api/projects/${encodeURIComponent(name)}${machineQuery(machine)}`, req)
}

// fetchPtySessions 列终端会话（GET /api/pty/sessions）。
//
// scope='all' 取跨机汇总（多一个 machines 字段）。**这是会话恢复的唯一真相源**：
// 前端不做任何本地持久化，列表里没有的会话就是不存在（spec §6.1）。
export function fetchPtySessions(scope?: 'all'): Promise<PtySessionsResp> {
  return request<PtySessionsResp>(`/api/pty/sessions${scope === 'all' ? '?scope=all' : ''}`)
}

// createPtySession 开一个终端会话（POST /api/pty/sessions）。
//
// base_kind='home' 时 base_path 被服务端忽略（它用自己的 $HOME）。
// 501 = 那台机器的平台不支持 PTY；400 = base_path 不是已探测到的工作树。
export function createPtySession(req: CreatePtySessionReq, machine?: string): Promise<PtySession> {
  return postJSON<PtySession>(`/api/pty/sessions${machineQuery(machine)}`, req)
}

// deletePtySession 显式关闭一个终端会话（DELETE /api/pty/sessions/{id}）。
//
// **只有用户点 × 才该调它。** 组件卸载、切基准目录、关页面都只断 WS，
// 不调这里——否则「跑一晚上的 build」会被一次切目录杀掉（spec §3.2）。
export function deletePtySession(id: string, machine?: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(
    `/api/pty/sessions/${encodeURIComponent(id)}${machineQuery(machine)}`,
    { method: 'DELETE' },
  )
}
