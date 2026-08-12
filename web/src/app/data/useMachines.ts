// useMachines —— 机器探活：15s，且**仅在 /machines 可见时**开表。
// 探活会向每台远程机发 GET /api/status，没人看的时候没有理由持续打扰它们。
import { usePoll } from './usePoll'
import { fetchMachines } from '../../api/client'
import type { MachinesResp } from '../../api/types'

const MACHINES_INTERVAL = 15000
export function useMachines(enabled: boolean): ReturnType<typeof usePoll<MachinesResp>> {
  return usePoll(fetchMachines, MACHINES_INTERVAL, { enabled })
}
