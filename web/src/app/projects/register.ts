// register.ts —— 项目登记的请求组装与「本机 → 远程」编排（无 React 依赖）。
//
// 编排职责：registerFromForm 先登记本机；本机成功且勾了远程再登记远程，远程的
// origin_url / name 一律用**本机响应**里的权威值——浏览器侧不猜 origin（path 已有
// 仓时 origin 只有 agentd 现读磁盘才知道），也不擅自补 name。
//
// 为什么逐位置收集结果而不是整体抛错：多位置登记是多次独立调用（每个位置一台
// 机器/一个目录），"整体失败"的笼统提示是被明确禁止的展示方式——用户必须知道
// 本机到底登记上没有（spec §5.2）。每个位置单独收口，任何一台失败都不吞掉其余
// 结果；失败文案经 errorMessage 透传 agentd 原文（里面带着解法）。
import { createProject } from '../../api/client'
import type { CreateProjectReq, CreateProjectResp } from '../../api/types'
import { errorMessage } from '../lib/format'

// LocationChoice 是一个登记位置：机器名（""=本机）+ 可选的 Git 地址 / 名称 / 目录。
// 三个字段都可空——registerOne 只把非空字段放进请求体，由 agentd 按
// 「path 是否给出 / 目录是否存在 / origin 是否给出」三态分派。
export interface LocationChoice {
  machine: string
  originUrl?: string
  name?: string
  path?: string
}

// RegisterFormInput 是单页表单的一次提交：本机 path 必填（空串由后端 400 兜底），
// git URL 与 name 选填；remoteMachine 为 null 表示不登记远程。
export interface RegisterFormInput {
  name: string
  localPath: string
  gitUrl: string
  remoteMachine: string | null
  remotePath: string
}

// RegisterOutcome 是单个位置的登记结果；error 透传 agentd 原文（带解法）。
export interface RegisterOutcome {
  machine: string
  ok: boolean
  error: string
  result?: CreateProjectResp
}

// registerFromForm 执行「本机优先」编排：先打本机；本机失败或没勾远程则只回
// 本机一条；本机成功且勾了远程时，远程请求的 origin_url / name 用本机**响应**里
// 的权威值（不采信表单串），远程 path 非空才带（空 = 该机 clone 到自己的
// repo_root/<name>）。
export async function registerFromForm(input: RegisterFormInput): Promise<RegisterOutcome[]> {
  const local = await settleOne({
    machine: '',
    originUrl: input.gitUrl,
    name: input.name,
    path: input.localPath,
  })
  const outcomes: RegisterOutcome[] = [local]
  if (!local.ok || !input.remoteMachine) return outcomes

  const remote = await settleOne({
    machine: input.remoteMachine,
    originUrl: local.result!.origin_url,
    name: local.result!.name,
    path: input.remotePath,
  })
  outcomes.push(remote)
  return outcomes
}

// registerAll 按选中位置逐次登记，返回逐位置结果；永不整体抛错。
// 保留给「结果页按位置重试」：入参为完整 LocationChoice[]（重试方自己决定
// 每个位置带哪些字段）。
export async function registerAll(choices: LocationChoice[]): Promise<RegisterOutcome[]> {
  return Promise.all(choices.map(settleOne))
}

// settleOne 登记单个位置并把成败收口为 RegisterOutcome。
async function settleOne(choice: LocationChoice): Promise<RegisterOutcome> {
  try {
    const result = await registerOne(choice)
    return { machine: choice.machine, ok: true, error: '', result }
  } catch (reason) {
    return { machine: choice.machine, ok: false, error: errorMessage(reason) }
  }
}

// registerOne 登记单个位置：只把非空字段放进 body（与 client 一致）——
// origin 空 = 让 agentd 现读已有仓或报 400，path 空 = 让目标机 clone 到自己的
// repo_root，name 空 = 由后端从 origin 末段派生。
async function registerOne(choice: LocationChoice): Promise<CreateProjectResp> {
  const req: CreateProjectReq = {}
  const origin = (choice.originUrl ?? '').trim()
  if (origin) req.origin_url = origin
  const name = (choice.name ?? '').trim()
  if (name) req.name = name
  const path = (choice.path ?? '').trim()
  if (path) req.path = path
  return createProject(req, choice.machine)
}
