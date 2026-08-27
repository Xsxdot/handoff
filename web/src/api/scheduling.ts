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
  name?: string
  machine: string
  cli: string
  home_dir: string
  model?: string
  credential: string
  max_concurrency?: number
}

export interface SquadInput {
  name?: string
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

/** POST attach=true 的服务端定位回执；machine 空串仍须保留。 */
export interface CoordinatorAttachInfo {
  machine: string
  dir: string
  command: string
}

/** GET coordinator 的状态；未绑定时 attach 明确为 null。 */
export interface CoordinatorStatus {
  bound: boolean
  attach_active: boolean
  attach: CoordinatorAttachInfo | null
}

/** POST attach=false 的服务端成功回执。 */
export interface CoordinatorAttachReleaseResp {
  ok: boolean
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

/** 参数：完整 cardId；返回：GET coordinator wire；路径段必须 encodeURIComponent。 */
export const getCoordinatorStatus = (cardId: string) =>
  request<CoordinatorStatus>(`/api/cards/${encodeURIComponent(cardId)}/coordinator`)

/** 参数：完整 cardId 与 workdir；返回：AttachInfo；不改写服务端三元组。 */
export const attachCoordinator = (cardId: string, workdir: string) =>
  postJSON<CoordinatorAttachInfo>(
    `/api/cards/${encodeURIComponent(cardId)}/attach`,
    { active: true, workdir },
  )

/** 参数：完整 cardId；返回：{ok:true}；路径段必须 encodeURIComponent。 */
export const releaseCoordinator = (cardId: string) =>
  postJSON<CoordinatorAttachReleaseResp>(
    `/api/cards/${encodeURIComponent(cardId)}/attach`,
    { active: false },
  )
