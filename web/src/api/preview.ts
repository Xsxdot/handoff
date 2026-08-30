// 远端预览会话的 HTTP 镜像；实际 owner 持久化、隧道与浏览器启动在 agentd。
import { deleteJSON, postJSON, request } from './client'
import type {
  PreviewCloseResp,
  PreviewListResp,
  PreviewOpenReq,
  PreviewOpenResp,
  PreviewSession,
} from './types'

export function fetchPreviews(scope: 'all' | undefined = 'all'): Promise<PreviewListResp> {
  return request<PreviewListResp>(`/api/previews${scope === 'all' ? '?scope=all' : ''}`)
}

export function createPreview(req: PreviewOpenReq): Promise<PreviewSession> {
  return postJSON<PreviewSession>('/api/previews', req)
}

export function closePreview(id: string): Promise<PreviewCloseResp> {
  return deleteJSON<PreviewCloseResp>(`/api/previews/${encodeURIComponent(id)}`, {})
}

export function openPreview(id: string, machine = ''): Promise<PreviewOpenResp> {
  const query = machine ? `?machine=${encodeURIComponent(machine)}` : ''
  return postJSON<PreviewOpenResp>(`/api/previews/${encodeURIComponent(id)}/open${query}`, {})
}
