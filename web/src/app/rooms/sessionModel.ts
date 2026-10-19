// sessionModel —— 会话内容面的纯投影层：成员状态/ timeline kind 两张渲染词表、
// @分段、未读聚合。边界：不发请求、不碰 React；两张词表是「看板不说谎」
// （proto 四值词表）与「未知值不白屏」（透传兜底）的前端半边，词表漂移由
// sessionModel.test 与孪生金样本双侧拦截。
import type { Card } from '../../api/ledger'
import type { SessionMember, SessionMemberKind, SessionSummary } from '../../api/rooms'
import { formatRelative, isUnrecordedTime } from '../lib/format'

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
//
// 时间缺席、或为 Go 零值（`0001-01-01T00:00:00Z`，协议里表示「无」）时渲染
// 「未记录」——零值不是「很久以前」，把它算成相对时间会得到「739877 天前」
// 这种假的精确读数（B385 的现场）。只报可证实的，不知道就说不知道。
export function memberStatusText(member: SessionMember): string {
  if (member.status === 'last_active') {
    const at = member.last_active
    if (!at || isUnrecordedTime(at)) return '最后活跃 未记录'
    return `最后活跃 ${formatRelative(at)}`
  }
  return memberStatusLabel(member.status)
}

// 恰九值（proto.SessionEvent* 词表，B444 增 renamed）；未知 ledger 事件不进 timeline（collab 侧），
// 但前端仍对未知 kind 透传兜底（缺陷族 7 不白屏）。
export const TIMELINE_KIND_LABEL: Readonly<Record<string, string>> = {
  created: '会话建立', archived: '会话归档', card_joined: '卡进群', card_left: '卡移出',
  seat_bound: '协调者入座', seat_rebound: '席位换绑', needs_human: '需要你', card_closed: '卡收口',
  renamed: '会话改名',
}

// timelineKindLabel timeline 事件渲染：未知 kind 原样透传，不白屏。
export function timelineKindLabel(kind: string): string {
  return TIMELINE_KIND_LABEL[kind] ?? kind
}

// timelineDetailText timeline 行的 detail 读数。renamed 的 detail 是服务端 JSON
// {from,title,session}，渲染成「旧名 → 新名」；其余 kind 或解析不出预期形状时原样透传
// （载荷可对质，不二次解释）。
export function timelineDetailText(row: { kind: string; detail?: string }): string {
  const raw = row.detail ?? ''
  if (row.kind !== 'renamed' || raw === '') return raw
  try {
    const parsed: unknown = JSON.parse(raw)
    if (parsed !== null && typeof parsed === 'object') {
      const { from, title } = parsed as { from?: unknown; title?: unknown }
      if (typeof from === 'string' && typeof title === 'string') return `${from} → ${title}`
    }
  } catch {
    // 非 JSON：原样透传，不白屏。
  }
  return raw
}

export interface BodySegment { text: string; mention: boolean }

// Go strings.Fields / unicode.IsSpace 的空白集；JS \s 少了 U+0085，
// 多了 U+FEFF，不能用作跨端寻址的 token 边界。
// eslint-disable-next-line no-control-regex -- 必须包含 Go unicode.IsSpace 的 ASCII 控制空白。
const goSpace = /[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/
// eslint-disable-next-line no-control-regex -- 与 goSpace 使用同一套 Go 空白字符。
const goSpaceRun = /[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/

// extractSessionMentions 与 CLI 的完整 @token 金样本一致：只收空白分隔且
// 合法的统一身份或卡号，避免正文里的邮箱和残缺 @ 无意寻址。
export function extractSessionMentions(body: string): string[] {
  const seen = new Set<string>()
  for (const field of body.split(goSpaceRun)) {
    if (!field.startsWith('@')) continue
    const token = field.slice(1)
    if ((/^(?:user|agent):[^:]+$/.test(token) && !goSpace.test(token)) || /^[A-Z]{1,4}[0-9]+(?:\.[0-9]+)*$/.test(token)) seen.add(token)
  }
  return [...seen]
}

// segmentBody 按空白切 @token，与 mentions 逐字比对（token 去 @ 前缀）。
export function segmentBody(body: string, mentions?: string[]): BodySegment[] {
  const set = new Set(mentions ?? [])
  return body
    .split(/(@[^\s]+)/g)
    .filter((token) => token !== '')
    .map((token) => ({ text: token, mention: token.startsWith('@') && set.has(token.slice(1)) }))
}

// 与 proto.SessionMember* 种类词表逐值一致（B435 增 resident）：详情成员行与 @ 候选面板共用的 kind 标签。
// satisfies 让 SessionMemberKind 新增取值而这里未登记时编译失败。
export const MEMBER_KIND_LABEL: Readonly<Record<string, string>> = {
  human: '成员', agent: '代理', seat: '席位', resident: '驻场者',
} satisfies Record<SessionMemberKind, string>

// RESIDENT_ORGANIZER_LABEL 项目房间里的驻场者称「组织者」（spec 术语，W plan Q6）。
export const RESIDENT_ORGANIZER_LABEL = '组织者'

// memberKindLabel 成员 kind 渲染：词表内出中文标签，词表外原样透传。
export function memberKindLabel(kind: string): string {
  return MEMBER_KIND_LABEL[kind] ?? kind
}

// memberRoleLabel 成员行/候选的角色标签：resident 在项目房间（summary.project 非空）标
// 「组织者」，否则同 memberKindLabel。只看 kind 与所在会话的 project，席位行不受影响。
export function memberRoleLabel(
  member: Pick<SessionMember, 'kind'>,
  summary: Pick<SessionSummary, 'project'> | null | undefined,
): string {
  if (member.kind === 'resident' && summary?.project) return RESIDENT_ORGANIZER_LABEL
  return memberKindLabel(member.kind)
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

// PROJECT_ROOM_BADGE 会话列表行上项目房间的徽章（W plan Q7 裁定稿）。
export const PROJECT_ROOM_BADGE = '项目房间'

// SessionProjects 会话的项目归属读数：names 去重保序（项目房间标记在前，卡项目随后）；
// hasUnknownCard = 有卡在 projectOfCard 里查不到项目（归属不可证实，调用方不得编造项目）。
export interface SessionProjects { names: string[]; hasUnknownCard: boolean }

// sessionProjects 会话 → 所属项目名集合（B435 Q7，C-510；B444 S8）=
// home_project ∪ summary.project ∪ 卡项目，去重保序、home_project 打头。
// 这是「会话属于哪个项目」的唯一规则：桌面项目树（Shell）、移动工作台（MobileWorkspace）
// 与侧栏筛选（filterSessionsByProject）共用；项目房间与项目内会话零卡也归属其项目。
// 旧会话 home_project 缺键（或空串）时结果与 B435 规则逐例一致，不迁移。
export function sessionProjects(
  session: Pick<SessionSummary, 'project' | 'home_project' | 'cards'>,
  projectOfCard: (cardId: string) => string,
): SessionProjects {
  const cardProjects = (session.cards ?? []).map((card) => projectOfCard(card.card_id))
  const names = [...new Set([session.home_project ?? '', session.project ?? '', ...cardProjects].filter(Boolean))]
  return { names, hasUnknownCard: cardProjects.some((name) => !name) }
}

/** PullableCard 建会话框只读卡的四个键（CardView 与 Card 都满足）。 */
export type PullableCard = Pick<Card, 'id' | 'title' | 'status' | 'project'>

// 账本终态（internal/ledger/types.go StatusDone/StatusClosed）：终态卡不可拉入会话。
const TERMINAL_CARD_STATUSES = new Set(['已完成', '终止'])

/**
 * pullableCards 建会话框的卡候选：属本项目、未终态、不在任何（已知）会话里，保持入参顺序。
 * @param cards 卡流快照（全部项目）
 * @param project 会话所属项目名（与 card.project 逐字比对，同服务端预校验口径）
 * @param sessions 会话列表快照（含归档）；列表外的会话占用由服务端拒绝并点名
 */
export function pullableCards<T extends PullableCard>(cards: T[], project: string, sessions: Pick<SessionSummary, 'cards'>[]): T[] {
  const taken = new Set(sessions.flatMap((session) => (session.cards ?? []).map((card) => card.card_id)))
  return cards.filter((card) => card.project === project && !TERMINAL_CARD_STATUSES.has(card.status) && !taken.has(card.id))
}

// filterSessionsByProject 按项目筛选会话列表（B358.8 #6）：project='' 为「全部」
// 缺省全显；选中项目时会话所属项目集合（sessionProjects：项目房间标记 ∪ 锚定卡项目）
// 命中才显示；无卡且非项目房间的会话只归「全部」。
export function filterSessionsByProject(
  sessions: SessionSummary[],
  projectOfCard: (cardId: string) => string,
  project: string,
): SessionSummary[] {
  if (project === '') return sessions
  return sessions.filter((session) => sessionProjects(session, projectOfCard).names.includes(project))
}

// totalUnread 会话 tab 徽章的聚合读数（Σ unread）。
export function totalUnread(summaries: SessionSummary[]): number {
  return summaries.reduce((total, summary) => total + summary.unread, 0)
}

// isSelfActor 自方消息判定：与本次请求的控制台人名精确等值（B358.9 P-4 甲）。
// selfMember 取服务端 GET /api/identity 的 member；拉取失败（null）回落
// user: 前缀——但绝不把 web:<host> 旧脸判为自方（本卡要退役的机器位）。
export function isSelfActor(actor: string, selfMember: string | null): boolean {
  if (selfMember !== null) return actor === selfMember
  return actor.startsWith('user:')
}

// signatureText 落款：人名 + 端名（有端戳时）；端戳缺失只出人名（决定 1）。
export function signatureText(actor: string, device?: string): string {
  return device ? `${actor} · ${device}` : actor
}
