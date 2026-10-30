// Device layout storage: browser/App-origin-local tabs, focus, splits and dock.
// Reuses the existing codecs; contains no resource data, drafts or server writes.
import type { WorkbenchStateResp } from '../../api/types'
import { decodeDock, encodeDock, type DockSnapshot } from '../homedock/dockPersist'
import { decodeWorkbench, encodeWorkbench, GLOBAL_WORKBENCH_KEY, isEmptyWorkbench } from './persist'
import type { Workbench } from './tabs'

export const DEVICE_WORKBENCH_KEY = 'handoff.workbench.device.v1'

/** Read the captured browser storage. Missing means one-time legacy import;
 * malformed/unavailable storage throws so it cannot be overwritten by fallback. */
export function loadDeviceWorkbench(storage: Storage): WorkbenchStateResp | null {
  const raw = storage.getItem(DEVICE_WORKBENCH_KEY)
  if (raw === null) return null
  const value: unknown = JSON.parse(raw)
  if (typeof value !== 'object' || value === null) throw new Error('本设备布局格式无效')
  const record = value as Record<string, unknown>
  if (record.v !== 1 || typeof record.selected !== 'string' || typeof record.dock !== 'string' ||
      typeof record.workbench !== 'string' || (record.workbench !== '' && decodeWorkbench(record.workbench) === null) ||
      (record.dock !== '' && decodeDock(record.dock) === null)) throw new Error('本设备布局格式无效')
  return { selected: record.selected, dock: record.dock, bases: record.workbench === '' ? [] : [
    { base_key: GLOBAL_WORKBENCH_KEY, payload: record.workbench, updated_at: 0 },
  ] }
}

/** Atomic local snapshot. An explicit empty workbench remains a saved record,
 * preventing closed tabs from returning through the legacy import next launch. */
export function encodeDeviceWorkbench(workbench: Workbench, selected: string, dock: DockSnapshot): string {
  return JSON.stringify({ v: 1, workbench: isEmptyWorkbench(workbench) ? '' : encodeWorkbench(workbench), selected, dock: encodeDock(dock) })
}
