// 编制域登记面、队列与协调者生命周期 wire 的唯一 web fetch 面。
// 与 internal/proto/scheduling.go 对齐；testdata fixture 和 contract.test.ts 钉住
// 线格式。本文件不管理业务状态，也不解析、拼接或替换服务端返回的命令。
import { postJSON, putJSON, request } from './client'

export interface CarrierView {
  name: string
  machine: string
  cli: string
  home_dir: string
  model?: string
  credential: string
  max_concurrency?: number
  status?: CarrierStatus
  last_error?: string
  version: number
}

export type CarrierStatus = 'pending' | 'online' | 'quota' | 'unreachable'
export type ProbeKind = 'empty' | 'logged_in' | 'occupied'
export type WakeOutcome = 'ready' | 'need_login' | 'quota' | 'unreachable'

export interface SquadView {
  name: string
  role: string
  members: SquadMember[]
  version: number
}

export interface SquadMember {
  carrier: string
  max_concurrency?: number
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
  members: SquadMember[]
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

/** 获取载体与小队快照；空数组和服务端错误均原样交给调用方。 */
export const getSquads = () => request<SquadsResp>('/api/squads')

/** 获取瞬时调度队列；本函数不把空队列改写为缺席值。 */
export const getQueue = () => request<QueueResp>('/api/queue')

// 服务端用 max_concurrency 缺席表达「不限」；保护 API 边界不把调用方传入的
// 0 序列化成另一种语义，尤其是表单从数字输入转换而来的零值。
function omitZeroConcurrency<T extends CarrierInput>(input: T): T {
  if (input.max_concurrency !== 0) return input
  const copy = { ...input }
  delete copy.max_concurrency
  return copy
}

/** 按载体名和 CAS 版本更新载体；expect=0 表示新建。 */
export const putCarrier = (name: string, expect: number, input: CarrierInput) =>
  putJSON<SquadPutResp>(
    `/api/squads/carriers/${encodeURIComponent(name)}?expect=${expect}`,
    omitZeroConcurrency(input),
  )

// 服务端用成员 max_concurrency 缺席表达「不限」；仅将调用方已经给出的 0
// 投影为缺席，不能把非法文本在 API 层静默变成不限，页面负责输入校验。
function omitZeroSquadConcurrency(input: SquadInput): SquadInput {
  return {
    ...input,
    members: input.members.map((member) => {
      if (member.max_concurrency !== 0) return member
      const copy = { ...member }
      delete copy.max_concurrency
      return copy
    }),
  }
}

/** 按小队名和 CAS 版本更新小队；expect=0 表示新建，成员政策缺席表示不限。 */
export const putSquad = (name: string, expect: number, input: SquadInput) =>
  putJSON<SquadPutResp>(
    `/api/squads/squads/${encodeURIComponent(name)}?expect=${expect}`,
    omitZeroSquadConcurrency(input),
  )

/** 探测目标机路径；machine 走 query，不进 body。本函数不改写 kind。 */
export const probeHome = (input: HomeProbeReq) => {
  const q = input.machine ? `?machine=${encodeURIComponent(input.machine)}` : ''
  const body: HomeProbeReq = { cli: input.cli, path: input.path }
  if (input.credential) body.credential = input.credential
  return postJSON<HomeProbeResp>(`/api/host/probe${q}`, body)
}

/** 本机/目标机有时限唤起；machine 走 query。控制台不应直接调，检测编排才用。 */
export const wakeHome = (input: HomeWakeReq) => {
  const q = input.machine ? `?machine=${encodeURIComponent(input.machine)}` : ''
  const body: HomeWakeReq = { cli: input.cli, home_dir: input.home_dir }
  if (input.credential) body.credential = input.credential
  if (input.model) body.model = input.model
  return postJSON<HomeWakeResp>(`/api/host/wake${q}`, body)
}

/** 对已登记载体做一次检测写状态。 */
export const detectCarrier = (name: string) =>
  postJSON<CarrierDetectResp>(
    `/api/squads/carriers/${encodeURIComponent(name)}/detect`,
    {},
  )

/** 取服务端生成的运行命令；调用方只复制，不拼接。 */
export const getCarrierRunCommand = (name: string) =>
  request<CarrierRunCommandResp>(
    `/api/squads/carriers/${encodeURIComponent(name)}/run-command`,
  )

/** 四态英文键 → 用户可见中文名；与 scheduling.CarrierStatus.Label 同一份词表。 */
export const CARRIER_STATUS_LABEL: Record<CarrierStatus, string> = {
  pending: '未上线',
  online: '已上线',
  quota: '限额中',
  unreachable: '不可达',
}

/** 登记弹窗默认 HOME 串；空名字返回空串。与 scheduling.DefaultHomeDir 同一格式。 */
export function defaultHomeDir(name: string): string {
  const trimmed = name.trim()
  return trimmed ? `~/.handoff/home/${trimmed}` : ''
}

export interface HomeProbeReq {
  cli: string
  path: string
  credential?: string
  machine?: string
}

export interface HomeProbeResp {
  kind: ProbeKind
  detail?: string
}

export interface HomeWakeReq {
  cli: string
  home_dir: string
  credential?: string
  model?: string
  machine?: string
}

export interface HomeWakeResp {
  outcome: WakeOutcome
  detail?: string
}

export interface CarrierDetectResp {
  name: string
  status: CarrierStatus
  last_error?: string
  version: number
}

export interface CarrierRunCommandResp {
  command: string
}

export type CoordinatorLaunchSource = 'manual' | 'card_create'

/**
 * 拉起协调者回合；省略 source 时发送空对象以保留服务端 manual 默认值，
 * card_create 只用于开卡即绑审计。返回的回合结果由服务端产生。
 */
export const launchCoordinator = (cardId: string, source?: CoordinatorLaunchSource) =>
  postJSON<CoordinatorLaunchResp>(
    `/api/cards/${encodeURIComponent(cardId)}/coordinator/launch`,
    source === undefined ? {} : { source },
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
