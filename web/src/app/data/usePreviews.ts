// usePreviews —— 预览聚合列表与本机开窗投影。
//
// 职责：
//   - 首次从 coordinator 拉取 scope=all，并订阅当前 agentd 的 preview WS
//   - 以 machine+session id 合并 created/closed 事件，不把 preview 事件混进任务 cursor
//   - 只把当前页面收到的 OpenPreview 成功结果记为本机短暂 is-open 投影
//
// 边界：不宣称浏览器进程附着的权威状态；刷新、关浏览器或 owner session 仍存在时，
// open 集合都可能丢失。owner truth 与机器失败信息仍来自 API/WS 响应。
import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchPreviews, openPreview } from '../../api/preview'
import type { PreviewEvent, PreviewListResp, PreviewSession } from '../../api/types'
import { connectPreviewEvents, type PreviewWsOptions } from '../../api/ws'
import { errorMessage } from '../lib/format'

export interface PreviewRowModel {
  session: PreviewSession
  projectId: string | null
  machine: string
  label: string
}

export interface PreviewState {
  data: PreviewListResp | null
  error: string
  opening: ReadonlySet<string>
  open: ReadonlySet<string>
}

// previewKey is the local identity of a mirrored session. Machine is mandatory
// in the key because two owners may publish the same session id independently.
export function previewKey(session: PreviewSession, machine: string = session.machine ?? ''): string {
  return `${machine}\x1f${session.id}`
}

// normalizePreviewOrigin mirrors projectid.NormalizeGitURL: it accepts both
// HTTPS and scp-like git origins, lowercases only the host, and leaves path
// case intact. Empty values remain empty and therefore join to no project.
export function normalizePreviewOrigin(raw: string): string {
  let value = raw.trim()
  if (value === '') return ''
  for (const scheme of ['ssh://', 'git://', 'https://', 'http://']) {
    if (value.toLowerCase().startsWith(scheme)) {
      value = value.slice(scheme.length)
      break
    }
  }
  const slash = value.indexOf('/')
  const at = value.indexOf('@')
  if (at >= 0 && (slash < 0 || at < slash)) value = value.slice(at + 1)
  const colon = value.indexOf(':')
  const pathSlash = value.indexOf('/')
  if (colon >= 0 && (pathSlash < 0 || colon < pathSlash)) {
    const rest = value.slice(colon + 1)
    const segment = rest.split('/')[0]
    if (segment !== '' && /^[0-9]+$/.test(segment)) value = value.slice(0, colon) + rest.slice(segment.length)
    else value = value.slice(0, colon) + '/' + rest
  }
  value = value.replace(/\/+$/, '')
  if (value.endsWith('.git')) value = value.slice(0, -4).replace(/\/+$/, '')
  const hostEnd = value.indexOf('/')
  return hostEnd > 0 ? value.slice(0, hostEnd).toLowerCase() + value.slice(hostEnd) : value.toLowerCase()
}

// previewLabel produces the compact row label required by the left tree.
// Missing port/branch is represented honestly rather than invented.
export function previewLabel(session: PreviewSession): string {
  let label = session.entry_url
  try {
    const url = new URL(session.entry_url)
    label = url.hostname === 'localhost' && url.port ? `localhost:${url.port}` : url.host || session.entry_url
  } catch {
    // Keep the wire value visible when it is not a valid URL.
  }
  return session.branch ? `${session.branch} · ${label}` : label
}

function mergeEvent(prev: PreviewListResp, event: PreviewEvent): PreviewListResp {
  const machine = event.machine ?? event.session.machine ?? ''
  const session = machine ? { ...event.session, machine } : { ...event.session, machine: undefined }
  const key = previewKey(session, machine)
  const sessions = prev.sessions.filter((candidate) => previewKey(candidate) !== key)
  if (event.type === 'preview.created') sessions.push(session)
  return { ...prev, sessions }
}

// usePreviews returns the aggregate snapshot, connection errors, and local
// opening/open projections. The returned open callback rejects after recording
// the error so callers may add their own toast or test the failure boundary.
export function usePreviews(): {
  data: PreviewListResp | null
  error: string
  refresh: () => void
  open: (id: string, machine: string) => Promise<void>
  isOpen: (id: string, machine: string) => boolean
  openKeys: ReadonlySet<string>
  openingKeys: ReadonlySet<string>
} {
  const [data, setData] = useState<PreviewListResp | null>(null)
  const [error, setError] = useState('')
  const [opening, setOpening] = useState<ReadonlySet<string>>(new Set())
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const openingRef = useRef(new Set<string>())
  const openRef = useRef(new Set<string>())

  const reload = useCallback(async () => {
    try {
      const next = await fetchPreviews('all')
      setData(next)
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    let active = true
    let connection: { close: () => void } | null = null
    const onEvent = (event: PreviewEvent) => {
      if (!active || (event.type !== 'preview.created' && event.type !== 'preview.closed')) return
      if (event.type === 'preview.closed') {
        const machine = event.machine ?? event.session.machine ?? ''
        const key = previewKey(event.session, machine)
        if (openRef.current.delete(key)) setOpen(new Set(openRef.current))
      }
      setData((prev) => prev === null ? prev : mergeEvent(prev, event))
    }
    const refreshSnapshot = async () => {
      const next = await fetchPreviews('all')
      if (!active) return
      setData(next)
      setError('')
    }
    const load = async () => {
      try {
        await refreshSnapshot()
        if (!active) return
        const options: PreviewWsOptions = {
          onEvent,
          onError: (message) => active && setError(message),
          beforeReconnect: refreshSnapshot,
        }
        connection = connectPreviewEvents(options)
      } catch (err) {
        if (active) setError(errorMessage(err))
      }
    }
    void load()
    return () => {
      active = false
      connection?.close()
      openingRef.current.clear()
      openRef.current.clear()
    }
  }, [])

  const refresh = useCallback(() => { void reload() }, [reload])
  const openSession = useCallback(async (id: string, machine: string) => {
    const key = `${machine}\x1f${id}`
    if (openingRef.current.has(key)) return
    openingRef.current.add(key)
    setOpening(new Set(openingRef.current))
    try {
      const result = await openPreview(id, machine)
      if (!result.opened) throw new Error('agentd 未确认 preview 已打开')
      openRef.current.add(key)
      setOpen(new Set(openRef.current))
    } catch (err) {
      setError(errorMessage(err))
      throw err
    } finally {
      openingRef.current.delete(key)
      setOpening(new Set(openingRef.current))
    }
  }, [])
  const isOpen = useCallback((id: string, machine: string) => open.has(`${machine}\x1f${id}`), [open])

  return { data, error, refresh, open: openSession, isOpen, openKeys: open, openingKeys: opening }
}
