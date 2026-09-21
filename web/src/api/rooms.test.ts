// 房间 wire 形状的 TS 孪生金样本（B156.2 契约 §6）。testdata/RoomsFixture.json
// 与 internal/proto/rooms_fixture_test.go 的 Go 金样本逐键一致；本测试断言：
//   - 编译期：JSON 以强类型 RoomMessage / InboxItem 承接，字段漂移在 tsc 报错；
//   - 运行期：可选键的 omitempty 语义（最小样本不得含 refs/mentions/等键）、
//     kind 与 origin 词表、escalation 的 decision_id 数值。
// 改线格式必须同步 Go 结构体、fixture 与本文件，漏一处就有一个测试当场变红。
import { describe, expect, it } from 'vitest'
import fixture from './testdata/RoomsFixture.json'
import type {
  IdentityResp,
  InboxItem,
  RoomMessage,
  RoomSummary,
  SessionDetail,
  SessionMemberStatus,
  SessionSummary,
} from './rooms'

const cases = fixture as {
  case: string
  message?: RoomMessage
  item?: InboxItem
  room?: RoomSummary
  summary?: SessionSummary
  detail?: SessionDetail
  identity?: IdentityResp
}[]

describe('room message twin fixtures', () => {
  it('escalation 全字段金样本', () => {
    const msg = cases.find((c) => c.case === 'escalation-full')!.message!
    expect(msg.room).toBe('B156')
    expect(msg.kind).toBe('escalation')
    expect(msg.refs).toHaveLength(2)
    expect(msg.mentions).toEqual(['user:sy'])
    expect(msg.decision_id).toBe(7)
    expect(msg.by_system).toBeUndefined()
  })

  it('user 最小字段金样本：omitempty 键缺席', () => {
    const raw = cases.find((c) => c.case === 'user-minimal')!.message! as unknown as Record<
      string,
      unknown
    >
    for (const banned of ['refs', 'mentions', 'decision_id', 'by_system']) {
      expect(raw, `可选键 ${banned} 不得出现`).not.toHaveProperty(banned)
    }
  })

  it('kind 词表恰为七值', () => {
    const kinds = ['escalation', 'deviation', 'closing', 'relay', 'reply', 'user', 'pointer']
    const msg = cases.find((c) => c.case === 'escalation-full')!.message!
    expect(kinds).toContain(msg.kind)
  })

  it('RoomSummary 保留 unread 0、attach 四字段，缺失 attach 为 undefined', () => {
    const attached = cases.find((c) => c.case === 'room-summary-attach')!.room as RoomSummary
    expect(attached.unread).toBe(0)
    expect(attached.attach).toEqual({
      target: 'devbox', task_id: 'T1', work_dir: '/w/B1', command: 'handoff attach T1',
    })
    const global = cases.find((c) => c.case === 'room-summary-no-attach')!.room as RoomSummary
    expect(global.unread).toBe(0)
    expect(global.attach).toBeUndefined()
  })

  it('RoomSummary preview 保留 body、seq、created_at', () => {
    const attached = cases.find((c) => c.case === 'room-summary-preview')!.room as RoomSummary
    expect(attached.preview).toEqual({
      body: '最新预览', seq: 3, created_at: '1970-01-01T00:00:03Z',
    })
  })
})

describe('inbox item twin fixtures', () => {
  it('三源各一枚且 origin 词表闭合', () => {
    const items = cases.filter((c) => c.item !== null && c.item !== undefined)
    const origins = items.map((c) => c.item!.origin)
    expect(origins).toEqual(['decision', 'ticket', 'mention'])
  })

  it('decision 条目带 card_id 与 payload，其余条目省略 card_id', () => {
    const decision = cases.find((c) => c.case === 'inbox-decision')!.item!
    expect(decision.card_id).toBe('B156')
    expect(decision.payload).toBeDefined()
    for (const c of ['inbox-ticket', 'inbox-mention']) {
      const item = cases.find((x) => x.case === c)!.item!
      expect(item.card_id, `${c} 不应带 card_id`).toBeUndefined()
      expect(typeof item.ref_id).toBe('string')
    }
  })
})

// —— B358.6 会话孪生金样本：与 internal/proto/sessions_fixture_test.go 逐键一致，
// 值以同一 JSON 文件为唯一来源（fixture 与 Go 金样本同源，TS 侧不手抄）。——
describe('session twin fixtures (B358.6)', () => {
  it('SessionSummary 金样本键集逐键在文，kind 恒 session', () => {
    const summary = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    for (const key of ['id', 'kind', 'title', 'owner', 'archived', 'unread', 'needs_human', 'last_activity']) {
      expect(summary, `SessionSummary 缺键 ${key}`).toHaveProperty(key)
    }
    expect(summary.kind).toBe('session')
    expect(summary.unread).toBe(2)
    expect(summary.needs_human).toBe(true)
    expect(summary.preview).toBeUndefined()
  })

  it('成员键集与 omitempty 语义：identity 恒在、席位带 card_id/card_title、last_active 零值省键', () => {
    const summary = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    const [human, seat] = summary.members!
    expect(human).toMatchObject({ identity: 'user:sy', kind: 'human', status: 'last_active' })
    expect(human).not.toHaveProperty('last_active')
    expect(human).not.toHaveProperty('card_id')
    expect(seat).toMatchObject({ identity: '', kind: 'seat', card_id: 'B1', card_title: '竖切卡', status: 'working' })
  })

  it('归档空座场：members omitempty 缺键（缺失≠空数组）、空座卡 seat 缺键=还没配人', () => {
    const summary = cases.find((c) => c.case === 'session-summary-archived-empty')!.summary!
    expect(summary.archived).toBe(true)
    expect(summary.members).toBeUndefined()
    expect(summary.cards![0].seat).toBeUndefined()
  })

  it('SessionDetail 键集：summary 恒在、nodes/timeline 在文', () => {
    const detail = cases.find((c) => c.case === 'session-detail-golden')!.detail!
    for (const key of ['summary', 'nodes', 'timeline']) {
      expect(detail, `SessionDetail 缺键 ${key}`).toHaveProperty(key)
    }
    expect(detail.nodes![0]).toMatchObject({ card_id: 'B1', node: 'implement', state: 'running' })
    expect(detail.nodes![0]).not.toHaveProperty('round')
    expect(detail.timeline![0]).toMatchObject({ seq: 1, kind: 'created' })
    expect(detail.timeline![1]).not.toHaveProperty('actor')
  })

  it('timeline kind 词表恰八值且 fixture 逐值在文（消费方渲染必须与 Go 词表对齐）', () => {
    const kinds = ['created', 'archived', 'card_joined', 'card_left', 'seat_bound', 'seat_rebound', 'needs_human', 'card_closed']
    const detail = cases.find((c) => c.case === 'session-detail-golden')!.detail!
    const fixtureKinds = detail.timeline!.map((row) => row.kind)
    for (const kind of ['created', 'card_joined', 'seat_bound', 'seat_rebound', 'needs_human', 'card_closed']) {
      expect(fixtureKinds, `fixture 应含 kind=${kind}`).toContain(kind)
    }
    for (const kind of fixtureKinds) expect(kinds, `未知 timeline kind ${kind}`).toContain(kind)
  })

  it('成员状态词表恰四值；online 不是合法取值（反例断言：词表闭包）', () => {
    const four: SessionMemberStatus[] = ['working', 'listening', 'last_active', 'empty']
    const summary = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    for (const member of summary.members!) expect(four, `成员状态越表: ${member.status}`).toContain(member.status)
    expect(four).not.toContain('online' as SessionMemberStatus)
    expect(four).not.toContain('在线' as SessionMemberStatus)
  })

  it('reply_to omitempty 三态：缺键=undefined（非 0）、非零在线', () => {
    const minimal = cases.find((c) => c.case === 'user-minimal')!.message! as unknown as Record<string, unknown>
    expect(minimal, '缺键必须 decode 成 undefined 而不是 0（可空 vs 零值分辨）').not.toHaveProperty('reply_to')
    const replied = cases.find((c) => c.case === 'user-reply')!.message!
    expect(replied.reply_to).toBe(42)
  })
})

// —— B358.9 身份读缝与端戳的 TS 孪生金样本：identity-* 与 user-stamped 三 case
// 与 Go 金样本 internal/proto/identity_test.go#TestIdentityRespWireShape、
// sessions_fixture_test.go#TestRoomMessageDeviceStampGolden 逐键一致，
// 唯一来源是同一 JSON 文件（TS 不手抄）。——
describe('身份读缝孪生样本（B358.9）', () => {
  it('identity-configured 三键恒出且与 Go 金样本逐键一致', () => {
    const id = cases.find((c) => c.case === 'identity-configured')!.identity as IdentityResp
    expect(Object.keys(id).sort()).toEqual(['configured', 'device', 'member'])
    expect(id.member).toBe('user:sycm')
    expect(id.device).toBe('mbp / Safari')
    expect(id.configured).toBe(true)
  })

  it('identity-unconfigured：值空但键仍在（缺失≠零值）', () => {
    const id = cases.find((c) => c.case === 'identity-unconfigured')!.identity as IdentityResp
    expect(Object.keys(id).sort()).toEqual(['configured', 'device', 'member'])
    expect(id.configured).toBe(false)
  })

  it('user-stamped：device 键在线；user-minimal 缺 device 键（omitempty）', () => {
    const stamped = cases.find((c) => c.case === 'user-stamped')!.message!
    expect(stamped.device).toBe('mbp / Safari')
    const raw = cases.find((c) => c.case === 'user-minimal')!.message! as unknown as Record<string, unknown>
    for (const banned of ['refs', 'mentions', 'decision_id', 'by_system', 'device']) {
      expect(raw, `可选键 ${banned} 不得出现`).not.toHaveProperty(banned)
    }
  })
})
