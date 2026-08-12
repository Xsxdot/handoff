// register.ts —— 多位置登记的项目登记编排（无 React 依赖）。
//
// 为什么逐位置收集结果而不是整体抛错：多位置登记是多次独立调用（每个位置一台
// 机器/一个目录），"整体失败"的笼统提示是被明确禁止的展示方式——用户必须知道
// 本机到底登记上没有（spec §5.2）。Promise.allSettled 逐位置收口，任何一台失败
// 都不吞掉其余结果。
import { createProject } from '../../api/client'
import type { CreateProjectResp } from '../../api/types'
import { errorMessage } from '../lib/format'

// LocationChoice 是一个登记位置：机器名（""=本机）+ Git 地址 + 可选目录。
export interface LocationChoice {
  machine: string
  gitUrl: string
  path: string
}

// RegisterOutcome 是单个位置的登记结果；error 透传 agentd 原文（带解法）。
export interface RegisterOutcome {
  machine: string
  ok: boolean
  error: string
  result?: CreateProjectResp
}

// registerAll 按选中位置逐次登记，返回逐位置结果；永不整体抛错。
export async function registerAll(choices: LocationChoice[]): Promise<RegisterOutcome[]> {
  const settled = await Promise.allSettled(choices.map((c) => registerOne(c)))
  return settled.map((s, i) => {
    const machine = choices[i].machine
    if (s.status === 'fulfilled') return { machine, ok: true, error: '', result: s.value }
    return { machine, ok: false, error: errorMessage(s.reason) }
  })
}

// registerOne 登记单个位置。path 空串时不带 path 字段——让目标机 clone 到它自己
// 的 repo_root；带 path = 登记已有目录（本机永远走这条）。
async function registerOne(choice: LocationChoice): Promise<CreateProjectResp> {
  const req: { origin_url: string; path?: string } = { origin_url: choice.gitUrl }
  if (choice.path) req.path = choice.path
  return createProject(req, choice.machine)
}
