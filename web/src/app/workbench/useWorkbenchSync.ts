// Device-local layout restore/save pipeline. Shared PTY inventory only validates
// references; legacy server layout is imported once and never written/deleted.
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { fetchPtySessions, fetchWorkbenchState } from '../../api/client'
import type { DockSnapshot } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import { topInset } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { DEVICE_WORKBENCH_KEY, encodeDeviceWorkbench, loadDeviceWorkbench } from './deviceState'
import { buildRestore } from './restore'
import type { Workbench } from './tabs'

const WRITE_DEBOUNCE_MS = 500

export interface WorkbenchSyncDeps {
  workbench: Workbench
  selectedKey: string
  dockSnapshot: DockSnapshot
  hydrateWorkbench: (workbench: Workbench) => void
  lockWorkbench: () => void
  enableWorkbench: () => void
  hydrateDock: (snapshot: DockSnapshot) => void
  adoptDockTab: (tab: HomeTab) => void
}

/** Restore this browser's layout; preserve legacy records on migration or any
 * storage failure. Unavailable local persistence never falls back to server PUT. */
export function useWorkbenchSync(deps: WorkbenchSyncDeps): { error: string; restoredSelected: string; restoring: boolean } {
  const [error, setError] = useState('')
  const [restoredSelected, setRestoredSelected] = useState('')
  const [restoring, setRestoring] = useState(true)
  const readyRef = useRef(false)
  const storageRef = useRef<Storage | null>(null)
  const depsRef = useRef(deps)
  depsRef.current = deps
  const sentRef = useRef('')
  const selectedRef = useRef('')

  useLayoutEffect(() => { depsRef.current.lockWorkbench() }, [])

  useEffect(() => {
    let cancelled = false
    async function restore() {
      try {
        // Capture once: different devices/browser stores must never exchange
        // handles during asynchronous restore or debounced writes.
        const storage = window.localStorage
        storageRef.current = storage
        console.debug('workbench.device.restore.start', { key: DEVICE_WORKBENCH_KEY })
        const local = loadDeviceWorkbench(storage)
        if (local === null) console.debug('workbench.device.legacy_import.start', { source: 'server', preserve: true })
        const [state, sessions] = await Promise.all([local ?? fetchWorkbenchState(), fetchPtySessions('all').catch((err: unknown) => {
          if (!cancelled) {
            console.warn('workbench.device.inventory.error', { error: err, preserveReferences: true })
            setError(`终端列表暂不可用，已保留本设备布局：${errorMessage(err)}`)
          }
          return null
        })])
        if (cancelled) return
        const vw = window.innerWidth || document.documentElement.clientWidth || 1280
        const vh = window.innerHeight || document.documentElement.clientHeight || 800
        const restored = buildRestore({ state, sessions: sessions?.sessions ?? [], machines: sessions?.machines,
          inventoryAvailable: sessions !== null, adoptHomeOrphans: false, vw, vh, inset: topInset() })
        const current = depsRef.current
        selectedRef.current = restored.selected
        // Save the first copy before rendering. Keep an explicit empty record,
        // so later launches cannot re-import another device's old open files.
        const raw = encodeDeviceWorkbench(restored.workbench, restored.selected, restored.dock ?? current.dockSnapshot)
        try {
          storage.setItem(DEVICE_WORKBENCH_KEY, raw)
          sentRef.current = raw
          readyRef.current = true
          console.debug('workbench.device.restore.saved', { imported: local === null, bytes: raw.length })
        } catch (err) {
          console.warn('workbench.device.write.error', { phase: 'restore', error: err })
          setError(`本设备布局保存失败，改动仅在当前页面生效：${errorMessage(err)}`)
        }
        current.hydrateWorkbench(restored.workbench)
        if (restored.dock !== null) current.hydrateDock(restored.dock)
        for (const tab of restored.dockOrphans) current.adoptDockTab(tab)
        current.enableWorkbench()
        setRestoredSelected(restored.selected)
        setRestoring(false)
        console.debug('workbench.device.restore.success', { imported: local === null, dropped: restored.dropped, pruned: restored.pruned, adopted: restored.adopted })
      } catch (err) {
        if (cancelled) return
        console.warn('workbench.device.restore.error', { error: err })
        setError(`本设备布局恢复失败，本次不会保存布局：${errorMessage(err)}`)
        depsRef.current.enableWorkbench()
        setRestoring(false)
      }
    }
    void restore()
    return () => { cancelled = true }
  }, [])

  // LocalStorage is synchronous/atomic: no out-of-order HTTP responses can
  // replace newer layout. Pagehide flushes a final change before debounce fires.
  useEffect(() => {
    const flush = () => {
      if (!readyRef.current || storageRef.current === null) return
      const current = depsRef.current
      // Tree loading may postpone selecting the restored directory. Until an
      // actual selection occurs, retain it rather than saving startup's null.
      if (current.selectedKey !== '') selectedRef.current = current.selectedKey
      const raw = encodeDeviceWorkbench(current.workbench, selectedRef.current, current.dockSnapshot)
      if (raw === sentRef.current) return
      try {
        storageRef.current.setItem(DEVICE_WORKBENCH_KEY, raw)
        sentRef.current = raw
        console.debug('workbench.device.write.success', { bytes: raw.length })
      } catch (err) {
        readyRef.current = false
        console.warn('workbench.device.write.error', { phase: 'update', error: err })
        setError(`本设备布局保存失败，改动仅在当前页面生效：${errorMessage(err)}`)
      }
    }
    const timer = setTimeout(flush, WRITE_DEBOUNCE_MS)
    window.addEventListener('pagehide', flush)
    return () => { clearTimeout(timer); window.removeEventListener('pagehide', flush) }
  }, [deps.workbench, deps.selectedKey, deps.dockSnapshot, restoring])

  return { error, restoredSelected, restoring }
}
