// sessionModel.test.ts —— 会话渲染词表与纯函数的缝侧守卫。
// 成员状态/ timeline kind 两张渲染词表是「看板不说谎」与「未知值不白屏」的
// 前端半边；变异（往 MEMBER_STATUS_LABEL 加 online 键）必须让第一支变红。
import { describe, expect, it } from 'vitest'
import type { SessionMember, SessionSummary } from '../../api/rooms'
import {
  MEMBER_KIND_LABEL, MEMBER_STATUS_LABEL, TIMELINE_KIND_LABEL,
  applyMention, filterSessionsByProject, isSelfActor, memberKindLabel, memberStatusLabel, memberStatusText,
  mentionCandidates, segmentBody, signatureText, timelineKindLabel, totalUnread,
} from './sessionModel'

describe('成员状态渲染词表（看板不说谎的前端半边）', () => {
  it('标签表恰四值，任何标签不含在线字样（反例断言）', () => {
    expect(Object.keys(MEMBER_STATUS_LABEL).sort()).toEqual(['empty', 'last_active', 'listening', 'working'])
    for (const label of Object.values(MEMBER_STATUS_LABEL)) {
      expect(label).not.toContain('在线')
      expect(label).not.toContain('online')
    }
  })
  it('四值逐一渲染中文标签', () => {
    expect(memberStatusLabel('working')).toBe('工作中')
    expect(memberStatusLabel('listening')).toBe('监听中')
    expect(memberStatusLabel('last_active')).toBe('最后活跃')
    expect(memberStatusLabel('empty')).toBe('空座')
  })
  it('词表外取值原样透传——绝不发明「在线」翻译', () => {
    expect(memberStatusLabel('online')).toBe('online')
    expect(memberStatusLabel('未来新值')).toBe('未来新值')
  })
  it('last_active 且带时间戳渲染相对时间', () => {
    const member = { identity: 'user:sy', kind: 'human', status: 'last_active', last_active: '2026-09-12T00:00:00Z' } as SessionMember
    expect(memberStatusText(member)).toContain('最后活跃')
  })
  it('last_active 无时间戳回落纯标签', () => {
    const member = { identity: 'u', kind: 'human', status: 'last_active' } as SessionMember
    expect(memberStatusText(member)).toBe('最后活跃')
  })
})

describe('@分段', () => {
  it('@命中 mentions 的高亮分段（token 去 @ 后与 mention 逐字相等）', () => {
    expect(segmentBody('@B233.16 守卫的收口归你', ['B233.16'])).toEqual([
      { text: '@B233.16', mention: true },
      { text: ' 守卫的收口归你', mention: false },
    ])
  })
  it('未命中的 @ 不高亮；无 mentions 全不高亮', () => {
    expect(segmentBody('@别人 看看', ['B233.16'])).toEqual([
      { text: '@别人', mention: false }, { text: ' 看看', mention: false },
    ])
    expect(segmentBody('@B233.16 hi', undefined)).toEqual([
      { text: '@B233.16', mention: false }, { text: ' hi', mention: false },
    ])
  })
})

describe('timeline kind 标签', () => {
  it('词表恰八值且中文标签互异（渲染与 Go 词表逐值对齐）', () => {
    expect(Object.keys(TIMELINE_KIND_LABEL).sort()).toEqual(
      ['archived', 'card_closed', 'card_joined', 'card_left', 'created', 'needs_human', 'seat_bound', 'seat_rebound'].sort(),
    )
    expect(new Set(Object.values(TIMELINE_KIND_LABEL)).size).toBe(8)
  })
  it('未知 kind 原样透传（渲染兜底不白屏）', () => {
    expect(timelineKindLabel('weird_kind')).toBe('weird_kind')
  })
})

describe('未读聚合', () => {
  it('会话 tab 徽章 = Σ unread', () => {
    expect(totalUnread([{ unread: 2 }, { unread: 0 }, { unread: 3 }] as SessionSummary[])).toBe(5)
  })
})

describe('项目筛选（B358.8 #6 纯函数缝）', () => {
  const summaryOf = (id: string, cardIds: string[]): SessionSummary => ({
    id, kind: 'session', title: id, owner: 'user:sy', archived: false, unread: 0,
    needs_human: false, last_activity: '',
    cards: cardIds.map((card_id) => ({ card_id })),
  })
  const projectOfCard = (cardId: string): string =>
    ({ B1: 'handoff', B2: 'aim' })[cardId as 'B1' | 'B2'] ?? ''
  const sessions = [summaryOf('s1', ['B1']), summaryOf('s2', ['B1', 'B2']), summaryOf('s3', []), summaryOf('s4', ['B9'])]

  it('缺省「全部」（空串）全显', () => {
    expect(filterSessionsByProject(sessions, projectOfCard, '')).toHaveLength(4)
  })
  it('选中项目：任一锚定卡命中即显示（多卡并集）', () => {
    expect(filterSessionsByProject(sessions, projectOfCard, 'handoff').map((s) => s.id)).toEqual(['s1', 's2'])
    expect(filterSessionsByProject(sessions, projectOfCard, 'aim').map((s) => s.id)).toEqual(['s2'])
  })
  it('无卡会话与映射缺失的卡只归「全部」', () => {
    expect(filterSessionsByProject(sessions, projectOfCard, 'handoff')).not.toContain(sessions[2])
    expect(filterSessionsByProject(sessions, projectOfCard, 'aim')).not.toContain(sessions[3])
  })
})

describe('@ 候选与 token 替换（B358.8 #2 纯函数缝）', () => {
  const members: SessionMember[] = [
    { identity: 'user:sy', kind: 'human', status: 'working' },
    { identity: 'Agent:Opendev', kind: 'agent', status: 'working', card_title: '执行卡' },
    { identity: '', kind: 'seat', card_id: 'B1', status: 'empty' },
  ]

  it('kind 标签恰三值，词表外透传', () => {
    expect(Object.keys(MEMBER_KIND_LABEL).sort()).toEqual(['agent', 'human', 'seat'])
    expect(memberKindLabel('human')).toBe('成员')
    expect(memberKindLabel('agent')).toBe('代理')
    expect(memberKindLabel('seat')).toBe('席位')
    expect(memberKindLabel('未来新值')).toBe('未来新值')
  })

  it('候选：空串=全量；空座不进候选；片段大小写不敏感包含过滤', () => {
    expect(mentionCandidates(members, '')).toHaveLength(2)
    expect(mentionCandidates(members, '').map((member) => member.identity)).toEqual(['user:sy', 'Agent:Opendev'])
    expect(mentionCandidates(members, 'sy').map((member) => member.identity)).toEqual(['user:sy'])
    expect(mentionCandidates(members, 'OPEND').map((member) => member.identity)).toEqual(['Agent:Opendev'])
    expect(mentionCandidates(members, '没有人')).toEqual([])
  })

  it('候选按身份去重且非席位记录优先（review P2：显式成员+席位可并存）', () => {
    const duplicated: SessionMember[] = [
      { identity: 'user:sy', kind: 'seat', status: 'working', card_id: 'B1', card_title: '席位卡' },
      { identity: 'user:sy', kind: 'human', status: 'working' },
    ]
    const candidates = mentionCandidates(duplicated, '')
    expect(candidates).toHaveLength(1)
    expect(candidates[0]).toMatchObject({ identity: 'user:sy', kind: 'human' })
    // 顺序保持：非席位顶替原席位的位置，其余身份相对次序不变
    const triple = [...duplicated, { identity: 'agent:a1', kind: 'agent', status: 'working' } as SessionMember]
    expect(mentionCandidates(triple, '').map((member) => member.identity)).toEqual(['user:sy', 'agent:a1'])
  })

  it('applyMention 整体替换末尾 token 为 @<identity> 并以空格终结', () => {
    expect(applyMention('看下 @sy', 'user:sy')).toBe('看下 @user:sy ')
    expect(applyMention('@', 'agent:a1')).toBe('@agent:a1 ')
    // 无进行中 token 时原样返回（防御：点选只发生在面板开着时）
    expect(applyMention('没有 token', 'user:sy')).toBe('没有 token')
  })

  it('替换结果与 send 的 mentions 提取逐字兼容（token = @ + 非空白）', () => {
    const draft = applyMention('hi @ope', 'Agent:Opendev')
    expect(draft.match(/@[^\s]+/g)).toEqual(['@Agent:Opendev'])
    // 高亮联动：segmentBody 以 @token 分段
    expect(segmentBody(draft, ['Agent:Opendev'])).toEqual([
      { text: 'hi ', mention: false },
      { text: '@Agent:Opendev', mention: true },
      { text: ' ', mention: false },
    ])
  })
})

describe('自方判定与落款（B358.9 P-4）', () => {
  it('isSelfActor 与人名精确等值：他人 user:/agent:/web: 都不给自方', () => {
    expect(isSelfActor('user:sycm', 'user:sycm')).toBe(true)
    expect(isSelfActor('user:other', 'user:sycm')).toBe(false)
    expect(isSelfActor('agent:opencode', 'user:sycm')).toBe(false)
    expect(isSelfActor('web:127.0.0.1', 'user:sycm')).toBe(false)
  })
  it('identity 拉取失败（null）回落 user: 前缀；web: 永不判自方（反例）', () => {
    expect(isSelfActor('user:sycm', null)).toBe(true)
    expect(isSelfActor('web:127.0.0.1', null)).toBe(false)
  })
  it('signatureText 有端戳出人名·端名，缺端戳只出人名', () => {
    expect(signatureText('user:sycm', 'mbp / Safari')).toBe('user:sycm · mbp / Safari')
    expect(signatureText('user:sycm', undefined)).toBe('user:sycm')
  })
})
