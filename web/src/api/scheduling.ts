// 编制域登记面/队列/协调者拉起的 TS 镜像（B156.3 K3）。
// 与 internal/proto/scheduling.go 一字不差；线格式由 testdata 三个 fixture 钉住
// （contract.test.ts 断言）。K6 的配置页/排队形态/拉起按钮消费本文件。
import { postJSON, putJSON, request } from './client'

export interface CarrierView {
  name: string
  machine: string
  cli: string
  home_dir: string
  model?: string
  credential: string
  max_concurrency?: number
  healthy: boolean
  version: number
}

export interface SquadView {
  name: string
  role: string
  members: string[]
  max_concurrency?: number
  version: number
}

export interface SquadsResp {
  carriers: CarrierView[]
  squads: SquadView[]
}

export interface CarrierInput {
  machine: string
  cli: string
  home_dir: string
  model?: string
  credential: string
  max_concurrency?: number
}

export interface SquadInput {
  role: string
  members: string[]
  max_concurrency?: number
}

export interface SquadPutResp {
  name: string
  version: number
}

export interface QueueEntry {
  kind: string
  id: string
  card: string
  node?: string
  squad: string
  target?: string
  executor?: string
  model?: string
  priority?: string
  ready: boolean
  actor: string
  seq: number
  position: number
}

export interface QueueResp {
  queue: QueueEntry[]
}

export interface CoordinatorLaunchResp {
  woke: boolean
  session_id?: string
  rebuilt: boolean
  escalated: boolean
  output?: string
}

export const getSquads = () => request<SquadsResp>('/api/squads')
export const getQueue = () => request<QueueResp>('/api/queue')
export const putCarrier = (name: string, expect: number, input: CarrierInput) =>
  putJSON<SquadPutResp>(
    `/api/squads/carriers/${encodeURIComponent(name)}?expect=${expect}`,
    input,
  )
export const putSquad = (name: string, expect: number, input: SquadInput) =>
  putJSON<SquadPutResp>(
    `/api/squads/squads/${encodeURIComponent(name)}?expect=${expect}`,
    input,
  )
export const launchCoordinator = (cardId: string) =>
  postJSON<CoordinatorLaunchResp>(
    `/api/cards/${encodeURIComponent(cardId)}/coordinator/launch`,
    {},
  )
