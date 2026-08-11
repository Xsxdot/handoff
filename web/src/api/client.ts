// agentd 的 HTTP fetch 客户端（浏览器侧）。
//
// 职责：
//   - 封装 /api/status 与 /api/tasks 两个只读接口，返回强类型结果
//   - 出错时把状态码与响应体错误信息统一成可读的 ApiError，供界面展示
//
// 边界：
//   - 不带任何凭据逻辑：浏览器经 vite 反代访问同源 /api，cookie 由浏览器
//     自动携带，本层不碰 cookie、不碰 token、不打印任何凭据明文
//   - 本轮只有只读接口；写操作（dispatch/reply/…）是后续任务
//
// 错误处理约定：所有 fetch 失败（网络、非 2xx）都抛 ApiError 而不是静默吞掉，
// 上层按「可观察的失败」渲染——鉴权失败（401）特别提示重新 handoff console。
import type { StatusResp, Task } from './types'

// ApiError 携带 HTTP 状态码与 agentd 返回的 error 字段（读不到时给兜底文案）。
//
// 参数：
//   - status: HTTP 状态码；0 表示请求根本没到 agentd（网络/反代层失败）
//   - message: 人类可读的原因
export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// request 执行一次带鉴权上下文的 GET，统一处理非 2xx 与网络错误。
//
// 参数：
//   - path: 以 /api 开头的路径（反代会把同源路径转给 agentd）
//
// 返回：
//   - 解析后的 JSON；类型由调用方指定
//
// 注意：
//   - 401 的 message 特意写明「重跑 handoff console 换新 cookie」——这是
//     浏览器会话过期/被吊销时唯一可行动的动作，静默失败会让人无从下手
async function request<T>(path: string): Promise<T> {
  let resp: Response
  try {
    resp = await fetch(path, { credentials: 'same-origin' })
  } catch (err) {
    throw new ApiError(0, `无法连接 agentd（反代失败？）：${err instanceof Error ? err.message : String(err)}`)
  }
  if (resp.status === 401) {
    throw new ApiError(401, '未授权：浏览器会话已失效，请重新执行 handoff console 兑换 cookie')
  }
  if (!resp.ok) {
    let detail = ''
    try {
      const body = (await resp.json()) as { error?: string }
      detail = body.error ?? ''
    } catch {
      // 响应体不是 JSON 时保留空 detail，用兜底文案
    }
    throw new ApiError(resp.status, detail || `agentd 返回 ${resp.status} ${resp.statusText}`)
  }
  return (await resp.json()) as T
}

// fetchStatus 取 agentd 的可用性与身份信息（GET /api/status）。
export function fetchStatus(): Promise<StatusResp> {
  return request<StatusResp>('/api/status')
}

// fetchTasks 取全部任务（GET /api/tasks；空列表返回 [] 而非 null）。
export function fetchTasks(): Promise<Task[]> {
  return request<Task[]>('/api/tasks')
}
