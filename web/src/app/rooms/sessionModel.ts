// sessionModel —— 会话内容面的纯投影层：成员状态/ timeline kind 两张渲染词表、
// @分段、未读聚合。边界：不发请求、不碰 React；两张词表是「看板不说谎」
// （proto 四值词表）与「未知值不白屏」（透传兜底）的前端半边，词表漂移由
// sessionModel.test 与孪生金样本双侧拦截。
import type { SessionMember, SessionSummary } from '../../api/rooms'
import { formatRelative } from '../lib/format'

// 恰四值（proto.SessionMember* 词表）；listening 生产零载体（B358.2 澄清③），
// 词表位保留。反例断言：任何值含「在线/online」即红。
export const MEMBER_STATUS_LABEL: Readonly<Record<string, string>> = {
  working: '工作中', listening: '监听中', last_active: '最后活跃', empty: '空座',
}

// memberStatusLabel 成员状态渲染：词表内出中文标签，词表外原样透传。
export function memberStatusLabel(status: string): string {
  return MEMBER_STATUS_LABEL[status] ?? status
}

// memberStatusText 成员行的状态文案：last_active 附相对时间（可证实的读数）。
export function memberStatusText(member: SessionMember): string {
  if (member.status === 'last_active' && member.last_active) {
    return `最后活跃 ${formatRelative(member.last_active)}`
  }
  return memberStatusLabel(member.status)
}

// 恰八值（proto.SessionEvent* 词表）；未知 ledger 事件不进 timeline（collab 侧），
// 但前端仍对未知 kind 透传兜底（缺陷族 7 不白屏）。
export const TIMELINE_KIND_LABEL: Readonly<Record<string, string>> = {
  created: '会话建立', archived: '会话归档', card_joined: '卡进群', card_left: '卡移出',
  seat_bound: '协调者入座', seat_rebound: '席位换绑', needs_human: '需要你', card_closed: '卡收口',
}

// timelineKindLabel timeline 事件渲染：未知 kind 原样透传，不白屏。
export function timelineKindLabel(kind: string): string {
  return TIMELINE_KIND_LABEL[kind] ?? kind
}

export interface BodySegment { text: string; mention: boolean }

// segmentBody 按空白切 @token，与 mentions 逐字比对（token 去 @ 前缀）。
export function segmentBody(body: string, mentions?: string[]): BodySegment[] {
  const set = new Set(mentions ?? [])
  return body
    .split(/(@[^\s]+)/g)
    .filter((token) => token !== '')
    .map((token) => ({ text: token, mention: token.startsWith('@') && set.has(token.slice(1)) }))
}

// 恰三值（proto.SessionMemberKind 词表）：详情成员行与 @ 候选面板共用的 kind 标签。
export const MEMBER_KIND_LABEL: Readonly<Record<string, string>> = {
  human: '成员', agent: '代理', seat: '席位',
}

// memberKindLabel 成员 kind 渲染：词表内出中文标签，词表外原样透传。
export function memberKindLabel(kind: string): string {
  return MEMBER_KIND_LABEL[kind] ?? kind
}

// MENTION_TOKEN_RE 草稿末尾的进行中 mention token（@ 开头到空白前）。
// 只认草稿末尾：光标中段编辑的联想属增强，v1 按末尾 token 投影。
export const MENTION_TOKEN_RE = /@[^\s]*$/

// mentionCandidates 从会话成员投影 @ 候选（B358.8 #2）：空座（identity 空）不进
// 候选——@ 空座无寻址对象；typed 为 @ 后已输入的片段，大小写不敏感包含过滤。
// 同一身份的显式成员与席位可能并存（服务端 sessionMembers 不按 identity 去重），
// 候选按身份去重且非席位记录优先（review P2：重复身份会撞 React key 与重复行）。
export function mentionCandidates(members: SessionMember[], typed: string): SessionMember[] {
  const q = typed.toLowerCase()
  const matched = members
    .filter((member) => member.identity !== '')
    .filter((member) => q === '' || member.identity.toLowerCase().includes(q))
  const byIdentity = new Map<string, SessionMember>()
  for (const member of matched) {
    const existing = byIdentity.get(member.identity)
    if (existing === undefined || (existing.kind === 'seat' && member.kind !== 'seat')) {
      byIdentity.set(member.identity, member)
    }
  }
  return [...byIdentity.values()]
}

// applyMention 把草稿末尾的进行中 token 整体替换为完整统一记法 @<identity>，
// 尾随一个空格终结 token（否则替换后的 @identity 仍是「进行中 token」，面板关不掉；
// 与 segmentBody/send 的 mentions 提取逐字兼容——token 恒为 @ + 非空白字符）。
export function applyMention(draft: string, identity: string): string {
  return draft.replace(MENTION_TOKEN_RE, `@${identity} `)
}

// filterSessionsByProject 按项目筛选会话列表（B358.8 #6）：project='' 为「全部」
// 缺省全显；选中项目时会话任一锚定卡的 project 命中才显示；无卡会话只归「全部」。
export function filterSessionsByProject(
  sessions: SessionSummary[],
  projectOfCard: (cardId: string) => string,
  project: string,
): SessionSummary[] {
  if (project === '') return sessions
  return sessions.filter((session) => (session.cards ?? []).some((card) => projectOfCard(card.card_id) === project))
}

// totalUnread 会话 tab 徽章的聚合读数（Σ unread）。
export function totalUnread(summaries: SessionSummary[]): number {
  return summaries.reduce((total, summary) => total + summary.unread, 0)
}

// isSelfActor 自方消息判定：控制台发言 actor 恒服务端注入 web:<host>（roomUserActor）。
export function isSelfActor(actor: string): boolean {
  return actor.startsWith('web:')
}
