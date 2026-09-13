// sessionModel.test.ts —— 会话渲染词表与纯函数的缝侧守卫。
// 成员状态/ timeline kind 两张渲染词表是「看板不说谎」与「未知值不白屏」的
// 前端半边；变异（往 MEMBER_STATUS_LABEL 加 online 键）必须让第一支变红。
import { describe, expect, it } from 'vitest'
import type { SessionMember, SessionSummary } from '../../api/rooms'
import {
  MEMBER_STATUS_LABEL, TIMELINE_KIND_LABEL,
  memberStatusLabel, memberStatusText, segmentBody, timelineKindLabel, totalUnread,
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
