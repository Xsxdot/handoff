// useWorkbenchSync.ts —— 全局 Workbench 与服务端状态行的水合/写回管道。
//
// 职责：并行读取布局与所有 PTY，恢复一次；之后对 global payload、selected、dock 去抖写回。
// 边界：不解释布局、不选树节点；纯编解码/合成分别由 persist.ts/restore.ts 负责。
import { useEffect, useRef, useState, type MutableRefObject } from 'react'
import { fetchPtySessions, fetchWorkbenchState, putWorkbenchBase, putWorkbenchDock, putWorkbenchSelected } from '../../api/client'
import { encodeDock, type DockSnapshot } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import { topInset } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { diffPayloads, encodeWorkbench, isEmptyWorkbench } from './persist'
import { buildRestore } from './restore'
import type { Workbench } from './tabs'

const WRITE_DEBOUNCE_MS = 500

export interface WorkbenchSyncDeps {
  workbench: Workbench
  selectedKey: string
  dockSnapshot: DockSnapshot
  hydrateWorkbench: (workbench: Workbench) => void
  hydrateDock: (snapshot: DockSnapshot) => void
  adoptDockTab: (tab: HomeTab) => void
}

/** 恢复全局布局并持续写回；拉取失败会关闭本次会话的写回闸门。 */
export function useWorkbenchSync(deps: WorkbenchSyncDeps): { error: string; restoredSelected: string } {
  const [error, setError] = useState('')
  const [restoredSelected, setRestoredSelected] = useState('')
  const ranRef = useRef(false)
  const cancelledRef = useRef(false)
  const readyRef = useRef(false)
  const depsRef = useRef(deps)
  depsRef.current = deps
  const sentRef = useRef<Record<string, string>>({})
  const dockSentRef = useRef('')
  const selectedSentRef = useRef('')
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    cancelledRef.current = false
    if (!ranRef.current) {
      ranRef.current = true
      Promise.all([fetchWorkbenchState(), fetchPtySessions('all')])
        .then(([state, sessions]) => {
          if (cancelledRef.current) return
          const vw = window.innerWidth || document.documentElement.clientWidth || 1280
          const vh = window.innerHeight || document.documentElement.clientHeight || 800
          const restored = buildRestore({ state, sessions: sessions.sessions, machines: sessions.machines, vw, vh, inset: topInset() })
          sentRef.current = Object.fromEntries(state.bases.map((row) => [row.base_key, row.payload]))
          dockSentRef.current = state.dock
          selectedSentRef.current = state.selected
          const current = depsRef.current
          current.hydrateWorkbench(restored.workbench)
          if (restored.dock !== null) current.hydrateDock(restored.dock)
          for (const tab of restored.dockOrphans) current.adoptDockTab(tab)
          setRestoredSelected(restored.selected)
          readyRef.current = true
          console.debug('workbench.restore.success', {
            global_key: '__global_workbench__', legacy: restored.legacy, dropped: restored.dropped,
            pruned: restored.pruned, '清除的外来悬浮窗 tab': restored.purged, adopted: restored.adopted,
          })
        })
        .catch((err: unknown) => {
          if (cancelledRef.current) return
          console.warn('workbench.restore.error', { error: err })
          setError(errorMessage(err))
        })
    }
    return () => { cancelledRef.current = true }
  }, [])

  useEffect(() => {
    if (!readyRef.current) return
    if (timerRef.current !== null) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      flush(depsRef.current, sentRef, dockSentRef, selectedSentRef)
    }, WRITE_DEBOUNCE_MS)
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
  }, [deps.workbench, deps.selectedKey, deps.dockSnapshot])

  return { error, restoredSelected }
}

function flush(
  deps: WorkbenchSyncDeps,
  sentRef: MutableRefObject<Record<string, string>>,
  dockSentRef: MutableRefObject<string>,
  selectedSentRef: MutableRefObject<string>,
): void {
  const next: Record<string, string> = {}
  if (!isEmptyWorkbench(deps.workbench)) next.__global_workbench__ = encodeWorkbench(deps.workbench)
  const { changed, removed } = diffPayloads(sentRef.current, next)
  for (const key of changed) {
    const payload = next[key]
    console.debug('workbench.write.start', { key, bytes: payload.length })
    putWorkbenchBase(key, payload)
      .then(() => {
        sentRef.current[key] = payload
        console.debug('workbench.write.success', { key, bytes: payload.length })
      })
      .catch((error: unknown) => console.warn('workbench.write.error', { key, bytes: payload.length, error }))
  }
  for (const key of removed) {
    console.debug('workbench.delete.start', { key })
    putWorkbenchBase(key, null)
      .then(() => {
        delete sentRef.current[key]
        console.debug('workbench.delete.success', { key })
      })
      .catch((error: unknown) => console.warn('workbench.delete.error', { key, error }))
  }
  const dockRaw = encodeDock(deps.dockSnapshot)
  if (dockRaw !== dockSentRef.current) {
    console.debug('workbench.dock.write.start', { bytes: dockRaw.length })
    putWorkbenchDock(dockRaw)
      .then(() => { dockSentRef.current = dockRaw; console.debug('workbench.dock.write.success', { bytes: dockRaw.length }) })
      .catch((error: unknown) => console.warn('workbench.dock.write.error', { error }))
  }
  if (deps.selectedKey !== selectedSentRef.current) {
    const key = deps.selectedKey
    console.debug('workbench.selected.write.start', { key })
    putWorkbenchSelected(key)
      .then(() => { selectedSentRef.current = key; console.debug('workbench.selected.write.success', { key }) })
      .catch((error: unknown) => console.warn('workbench.selected.write.error', { key, error }))
  }
}
