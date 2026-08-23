// agentd 的 HTTP fetch 客户端（浏览器侧）。
//
// 职责：
//   - 封装 /api/status、/api/tasks 与工作台状态接口，返回强类型结果
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
  AddMachineReq,
  BranchesResult,
  CreateWorktreeReq,
  CgSourceResp,
  CodegraphResp,
  CreateProjectReq,
  CreateProjectResp,
  CreatePtySessionReq,
  DisciplineBinding,
  DisciplineResp,
  DesktopState,
  DiffResult,
  DirEntry,
  DirListResult,
  EnvBinding,
  EnvKeysResp,
  EnvResp,
  FileRead,
  FileResult,
  FileWriteReq,
  FileWriteResp,
  ExecutorDefaultReq,
  ExecutorDefaultResp,
  MachinesResp,
  MachineUpgradeResp,
  DownloadState,
  LatestResp,
  Launcher,
  LaunchersResp,
  PatchProjectReq,
  ProjectLocation,
  ProjectBranchesResp,
  ProjectTreeResp,
  PtySession,
  PtySessionsResp,
  ReplyRequest,
  ReplyResult,
  ResumeResult,
  RunResult,
  SearchResult,
  StatusResp,
  StopResult,
  Task,
  TaskDetail,
  TaskPlan,
  TasksResp,
  Workspace,
  WorkbenchStateResp,
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
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let resp: Response
  try {
    resp = await fetch(path, { credentials: 'same-origin', ...init })
  } catch (err) {
    throw new ApiError(0, `无法连接 agentd（反代失败？）：${err instanceof Error ? err.message : String(err)}`)
  }
  return parseResponse<T>(resp)
}

// requestAllowNoContent 允许某个接口用 204 表示「当前没有资源」。
//
// 为什么不改 parseResponse：其它接口的 2xx 都有 JSON body，全局放宽会把接口
// 契约错误静默变成 undefined；桌面状态只有「没有薄壳」这一个明确的 204 语义。
async function requestAllowNoContent<T>(path: string): Promise<T | null> {
  let resp: Response
  try {
    resp = await fetch(path, { credentials: 'same-origin' })
  } catch (err) {
    throw new ApiError(0, `无法连接 agentd（反代失败？）：${err instanceof Error ? err.message : String(err)}`)
  }
  if (resp.status === 204) return null
  return parseResponse<T>(resp)
}

// postJSON 以 JSON body 发起 POST 请求。
export function postJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// putJSON 以 JSON body 发起 PUT 请求；除 method 外与 postJSON 相同。
export function putJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// patchJSON 以 JSON body 发起 PATCH 请求；除 method 外与 postJSON 相同。
export function patchJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// deleteJSON 以 JSON body 发起 DELETE 请求；除 method 外与 postJSON 相同。
export function deleteJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// fetchStatus 取 agentd 的可用性与身份信息（GET /api/status）。
export function fetchStatus(): Promise<StatusResp> {
  return request<StatusResp>('/api/status')
}

// fetchDesktopState 取薄壳上报的自身状态；204 表示当前没有活跃薄壳，解成 null。
export function fetchDesktopState(): Promise<DesktopState | null> {
  return requestAllowNoContent<DesktopState>('/api/desktop/state')
}

// fetchLatest 取 agentd 缓存的最新 release tag；refresh=true 强制绕过 24h 缓存。
export function fetchLatest(refresh = false): Promise<LatestResp> {
  return request<LatestResp>(`/api/update/latest${refresh ? '?refresh=1' : ''}`)
}

// fetchDownloadState 取桌面端安装包下载进度与结果。
export function fetchDownloadState(): Promise<DownloadState> {
  return request<DownloadState>('/api/update/desktop/download')
}

// startDownload 请求 agentd 下载、校验并打开桌面端安装包；响应状态由下载轮询读取。
export function startDownload(): Promise<void> {
  return postJSON<DownloadState>('/api/update/desktop/download', {}).then(() => undefined)
}

// fetchTasks 取全部任务（GET /api/tasks?scope=all），拆掉信封只交出任务数组。
//
// 为什么恒带 scope=all 而不是留个可选参数：控制台是「本机 agentd 是唯一入口」
// 模型下的**跨机**看板——左栏计数按 machine 归集、看板卡片印「本机 / mac-02」、
// 筛选器有机器下拉，整条前端链路本就是按跨机写的。只有这一处取数据时退回了
// 本机视角，结果是远端机器上跑的任务在界面上根本不存在（看板「进行中」恒空、
// 树上远端计数恒 0、也就无从点开它的 TUI）。留成可选参数等于把这个坑留着。
//
// 代价为零：scope=all 由 agentd 从本机镜像快照拼（tasksfanout.go），**不现场
// 扇出远端**，所以 2.5s 轮询的快慢与远端可达性解耦，和只查本机一样便宜。
//
// 信封里的 machines（每台机器的快照新旧）本函数丢弃：任务流的消费方只要任务。
// 机器可达性另有 useMachines / 树流的同名字段负责，这里再抄一份只会有两份漂移。
export function fetchTasks(): Promise<Task[]> {
  return request<TasksResp>('/api/tasks?scope=all').then((r) => r.tasks ?? [])
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

// fetchTaskPlan 取派发当刻交给 executor 的指令原文（GET /api/tasks/{id}/plan）。
//
// 404 有三种含义（任务不存在 / 老任务没归档 / 归档文件被删），调用方一律按
// 「没有可展示的派发指令」处理：这条数据是**补充**，缺了不影响会话流本身。
export function fetchTaskPlan(id: string): Promise<TaskPlan> {
  return request<TaskPlan>(`/api/tasks/${encodeURIComponent(id)}/plan`)
}

// fetchTaskBranches 取任务仓库的本地分支列表（审阅栏基准下拉的数据源）。
export function fetchTaskBranches(id: string): Promise<BranchesResult> {
  return request<BranchesResult>(`/api/tasks/${encodeURIComponent(id)}/branches`)
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

// upgradeMachine 请求一台远端执行机升级到最新版本。
// 409/422 等拒绝由 request 抛出 ApiError；完整的 MachineUpgradeResp 保存在 body 中。
export function upgradeMachine(name: string, force = false): Promise<MachineUpgradeResp> {
  const q = force ? '?force=1' : ''
  return request<MachineUpgradeResp>(`/api/machines/${encodeURIComponent(name)}/upgrade${q}`, {
    method: 'POST',
  })
}

// fetchDiscipline 取某台机器的纪律配置面（GET /api/discipline）：
// 目录、内置两版全文、该机文件列表、每个 executor 的档位。
export function fetchDiscipline(machine: string): Promise<DisciplineResp> {
  return request<DisciplineResp>(`/api/discipline${machineQuery(machine)}`)
}

// fetchDisciplineFile 读某台机器上一个纪律块文件的正文（GET /api/discipline/file）。
// 内置两版不走这条——它们的全文已在 fetchDiscipline 的结果里。
export function fetchDisciplineFile(machine: string, name: string): Promise<FileRead> {
  return request<FileRead>(
    `/api/discipline/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`,
  )
}

// saveDisciplineFile 写一个纪律块文件（PUT /api/discipline/file）。
//
// req.base_sha256 为空串表示新建：目标已存在时后端回 409，绝不静默覆盖。
// 冲突（409）时响应体是 FileConflictResp，由调用方按 ApiError 处理。
export function saveDisciplineFile(
  machine: string, name: string, req: FileWriteReq,
): Promise<FileWriteResp> {
  return putJSON<FileWriteResp>(
    `/api/discipline/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`, req,
  )
}

// saveDisciplineMapping 整段替换某台机器的 executor→纪律块映射
//（PUT /api/discipline/mapping），返回保存后的最新配置面。
export function saveDisciplineMapping(
  machine: string, bindings: DisciplineBinding[],
): Promise<DisciplineResp> {
  return putJSON<DisciplineResp>(`/api/discipline/mapping${machineQuery(machine)}`, { bindings })
}

// fetchEnv 取某台机器的 env 配置面（GET /api/env）：
// 目录、该机文件列表、每个 executor 的档位（两档）。
export function fetchEnv(machine: string): Promise<EnvResp> {
  return request<EnvResp>(`/api/env${machineQuery(machine)}`)
}

// fetchLaunchers 取某台机器的工作台启动项（GET /api/launchers）。
// env_missing 是服务端每次读盘现算的派生字段，不在前端缓存为真相。
export function fetchLaunchers(machine: string): Promise<LaunchersResp> {
  return request<LaunchersResp>(`/api/launchers${machineQuery(machine)}`)
}

// putLaunchers 整段替换某台机器的工作台启动项（PUT /api/launchers）。
// 返回保存后的最新列表，界面直接用它刷新，不做本地乐观更新。
export function putLaunchers(machine: string, launchers: Launcher[]): Promise<LaunchersResp> {
  return putJSON<LaunchersResp>(`/api/launchers${machineQuery(machine)}`, { launchers })
}

// fetchEnvKeys 取一个 env 文件的变量清单（GET /api/env/file/keys）。
//
// **响应里没有值**，只有 key 名、值的字节长度与重复标记。这是 Env 分区的
// 默认视图；要看值必须显式调 fetchEnvFile。
export function fetchEnvKeys(machine: string, name: string): Promise<EnvKeysResp> {
  return request<EnvKeysResp>(
    `/api/env/file/keys?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`,
  )
}

// fetchEnvFile 读一个 env 文件的**含值全文**（GET /api/env/file）。
//
// 只在用户点「编辑正文」时调用——默认视图走 fetchEnvKeys。
export function fetchEnvFile(machine: string, name: string): Promise<FileRead> {
  return request<FileRead>(
    `/api/env/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`,
  )
}

// saveEnvFile 写一个 env 文件（PUT /api/env/file）。
//
// req.base_sha256 为空串表示新建：目标已存在时后端回 409，绝不静默覆盖。
// 正文语法错误时后端回 400，message 是 Parse 的原文（自带行号）——调用方
// 应原样展示，那是用户改对的唯一线索。
export function saveEnvFile(
  machine: string, name: string, req: FileWriteReq,
): Promise<FileWriteResp> {
  return putJSON<FileWriteResp>(
    `/api/env/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`, req,
  )
}

// saveEnvMapping 整段替换某台机器的 executor→env 文件映射
//（PUT /api/env/mapping），返回保存后的最新配置面。
export function saveEnvMapping(
  machine: string, bindings: EnvBinding[],
): Promise<EnvResp> {
  return putJSON<EnvResp>(`/api/env/mapping${machineQuery(machine)}`, { bindings })
}

// fetchExecutorDefault 取某台机器的缺省执行者配置（GET /api/executor/default）。
export function fetchExecutorDefault(machine: string): Promise<ExecutorDefaultResp> {
  return request<ExecutorDefaultResp>(`/api/executor/default${machineQuery(machine)}`)
}

// saveExecutorDefault 整体替换某台机器的缺省执行者与其默认模型
//（PUT /api/executor/default），返回保存后的最新状态。
//
// req.model 为空串表示「清空默认模型」，是有意义的取值，不是「不改」。
// req.default 不在该机名单内时后端回 400，message 里带可选名单——原样展示。
export function saveExecutorDefault(
  machine: string, req: ExecutorDefaultReq,
): Promise<ExecutorDefaultResp> {
  return putJSON<ExecutorDefaultResp>(`/api/executor/default${machineQuery(machine)}`, req)
}

// addMachine 新增一台远程开发机（POST /api/machines）。
//
// 参数：
//   - req: 机器名、地址、令牌、ssh 用户；force=true 跳过可达性探测
//
// 返回：新的机器列表（与 fetchMachines 同结构）
//
// 注意：后端在 force 未置时会做一次可达性探测，探不通抛 ApiError(400)，
// message 即探测失败原文——调用方应原样展示给用户，那是判断「连不上」
// 还是「没授权」的唯一线索。
export function addMachine(req: AddMachineReq): Promise<MachinesResp> {
  return postJSON<MachinesResp>('/api/machines', req)
}

// deleteMachine 删除一台远程开发机（DELETE /api/machines/{name}）。
//
// 注意：机器名可能含需要转义的字符，必须 encodeURIComponent。
export function deleteMachine(name: string): Promise<MachinesResp> {
  return request<MachinesResp>(`/api/machines/${encodeURIComponent(name)}`, { method: 'DELETE' })
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

// createWorkspaceEntry 在工作树内新建一个空文件或空目录（POST /api/workspaces/entry）。
//
// rel 是父目录的相对路径（空串 = 工作树根）；name 必须是单层名，kind 取 'file' | 'dir'。
// 撞名 409、名字含 / 400，错误原文都在 ApiError.message 里。
export function createWorkspaceEntry(
  path: string,
  rel: string,
  name: string,
  kind: 'file' | 'dir',
  machine?: string,
): Promise<DirEntry> {
  return postJSON<DirEntry>(`/api/workspaces/entry?${workspaceQuery(path, rel, machine)}`, { name, kind })
}

// copyWorkspaceEntry 把工作树内 rel 条目复制一份到同级（POST /api/workspaces/entry/copy）。
//
// 副本名由服务端按「foo copy.go / foo copy 2.go」规则计算并返回，前端不参与命名。
export function copyWorkspaceEntry(path: string, rel: string, machine?: string): Promise<DirEntry> {
  return postJSON<DirEntry>(`/api/workspaces/entry/copy?${workspaceQuery(path, rel, machine)}`, {})
}

// renameWorkspaceEntry 把工作树内 rel 条目改名为单层 newName（PATCH /api/workspaces/entry）。
// 不做跨目录移动：newName 含 / 由服务端 400。
export function renameWorkspaceEntry(
  path: string,
  rel: string,
  newName: string,
  machine?: string,
): Promise<DirEntry> {
  return patchJSON<DirEntry>(`/api/workspaces/entry?${workspaceQuery(path, rel, machine)}`, {
    new_name: newName,
  })
}

// deleteWorkspaceEntry 删除工作树内 rel 条目（DELETE /api/workspaces/entry），
// 目录连同内容一并删，服务端不做回收站。
export function deleteWorkspaceEntry(path: string, rel: string, machine?: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(
    `/api/workspaces/entry?${workspaceQuery(path, rel, machine)}`,
    { method: 'DELETE' },
  )
}

// searchWorkspace 在工作树 rel 子树内按关键词搜索命中行（GET /api/workspaces/search）。
// q 必须非空（空词服务端 400）；hits 命中 rel 含 scope 前缀、line 从 1 起。
export function searchWorkspace(path: string, rel: string, q: string, machine?: string): Promise<SearchResult> {
  return request<SearchResult>(
    `/api/workspaces/search?${workspaceQuery(path, rel, machine)}&q=${encodeURIComponent(q)}`,
  )
}

// revealInFinder 在**本机**访达中显示工作树内 rel 条目（POST /api/workspaces/reveal）。
// rel 可为空串（揭示工作树根）。
//
// **故意没有 machine 参数**：远程条目不可能在本机访达里打开，端点对 ?machine=
// 一律 400。签名不给这个参数，就没有人能不小心传它。
export function revealInFinder(path: string, rel: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(
    `/api/workspaces/reveal?${workspaceQuery(path, rel)}`,
    { method: 'POST' },
  )
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

// fetchProjects 列全部项目位置（GET /api/projects）。
//
// 返回 ProjectLocation[]。服务端保证空列表序列化成 [] 而不是 null，调用方不必再做
// null 归一。
export function fetchProjects(): Promise<ProjectLocation[]> {
  return request<ProjectLocation[]>('/api/projects')
}

// fetchProjectBranches 列项目位置的本地分支（GET /api/projects/{name}/branches）。
//
// name 是**登记名**（ProjectLocationNode.name），不是 ProjectNode.name——后者取的是
// 该项目下首条登记的名字，跨机时两者可能不同，用错会寻址到别的位置或 404。
// machine 省略或空串 = 本机。
export function fetchProjectBranches(name: string, machine?: string): Promise<ProjectBranchesResp> {
  return request<ProjectBranchesResp>(`/api/projects/${encodeURIComponent(name)}/branches${machineQuery(machine)}`)
}

// fetchCodegraph 取项目的基线、全部 diff 视图与 file:line 保鲜报告。
export function fetchCodegraph(project: string): Promise<CodegraphResp> {
  return request<CodegraphResp>(`/api/projects/${encodeURIComponent(project)}/codegraph`)
}

// fetchCodegraphSource 按节点 file:line 实时读取源码窗口。
export function fetchCodegraphSource(project: string, file: string, line: number, span = 40): Promise<CgSourceResp> {
  return request<CgSourceResp>(
    `/api/projects/${encodeURIComponent(project)}/codegraph/source?file=${encodeURIComponent(file)}&line=${line}&span=${span}`,
  )
}

// createWorktree 在项目位置上新建一棵工作树（POST /api/projects/{name}/worktrees）。
//
// 返回的 Workspace 与项目树上那一条同口径，可直接拿去组装 BaseDir 选中，
// 不必等下一轮树刷新。name 的取值同 fetchProjectBranches。
export function createWorktree(name: string, req: CreateWorktreeReq, machine?: string): Promise<Workspace> {
  return postJSON<Workspace>(`/api/projects/${encodeURIComponent(name)}/worktrees${machineQuery(machine)}`, req)
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

// fetchWorkbenchState 一次拉全工作台状态（GET /api/workbench/state）。
//
// 返回：服务端的完整工作台状态。
// 注意：
// **只在应用启动时调一次。** 不做前台唤醒时重拉：那一刻本端内存里的那份才是
// 用户刚才的现场，从服务端拉一份回来盖掉它是纯粹的坏（spec §1.6）。
export function fetchWorkbenchState(): Promise<WorkbenchStateResp> {
  return request<WorkbenchStateResp>('/api/workbench/state')
}

// putWorkbenchBase 写一行基准状态（PUT /api/workbench/state/base）。
//
// 参数：baseKey 是基准目录身份；payload 是序列化字符串，null 表示删除。
// 返回：写入或删除完成的 Promise；失败时抛 ApiError。
// 注意：
// payload 传 null = 删除该行（一个目录的 tab 全关光了就该删，不存空记录）。
// 400 = base_key 为空或 payload 超过 256 KiB。
export async function putWorkbenchBase(baseKey: string, payload: string | null): Promise<void> {
  await putJSON<unknown>('/api/workbench/state/base', { base_key: baseKey, payload })
}

// putWorkbenchSelected 写「当前选中的基准目录」（PUT /api/workbench/state/selected）。
// 参数：baseKey 是基准目录 key；空串表示当前没有选中目录。
// 返回：写入完成的 Promise；失败时抛 ApiError。
// 注意：空串是合法值，不应把它当成请求参数缺失。
export async function putWorkbenchSelected(baseKey: string): Promise<void> {
  await putJSON<unknown>('/api/workbench/state/selected', { base_key: baseKey })
}

// putWorkbenchDock 写悬浮窗现场（PUT /api/workbench/state/dock）。
// 参数：payload 是序列化字符串，null 表示清空。
// 返回：写入或清空完成的 Promise；失败时抛 ApiError。
// 注意：payload 原样发送，编解码由工作台状态层负责。
// payload 传 null = 清空。
export async function putWorkbenchDock(payload: string | null): Promise<void> {
  await putJSON<unknown>('/api/workbench/state/dock', { payload })
}
