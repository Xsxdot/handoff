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

// totalUnread 会话 tab 徽章的聚合读数（Σ unread）。
export function totalUnread(summaries: SessionSummary[]): number {
  return summaries.reduce((total, summary) => total + summary.unread, 0)
}

// isSelfActor 自方消息判定：控制台发言 actor 恒服务端注入 web:<host>（roomUserActor）。
export function isSelfActor(actor: string): boolean {
  return actor.startsWith('web:')
}
