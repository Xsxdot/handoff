// 房间 wire 形状的 TS 孪生金样本（B156.2 契约 §6）。testdata/RoomsFixture.json
// 与 internal/proto/rooms_fixture_test.go 的 Go 金样本逐键一致；本测试断言：
//   - 编译期：JSON 以强类型 RoomMessage / InboxItem 承接，字段漂移在 tsc 报错；
//   - 运行期：可选键的 omitempty 语义（最小样本不得含 refs/mentions/等键）、
//     kind 与 origin 词表、escalation 的 decision_id 数值。
// 改线格式必须同步 Go 结构体、fixture 与本文件，漏一处就有一个测试当场变红。
import { describe, expect, it } from 'vitest'
import fixture from './testdata/RoomsFixture.json'
import type { InboxItem, RoomMessage } from './rooms'

const cases = fixture as {
  case: string
  message?: RoomMessage
  item?: InboxItem
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
