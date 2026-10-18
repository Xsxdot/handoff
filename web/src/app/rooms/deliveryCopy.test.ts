// deliveryCopy.test.ts —— B435 房间页投递标注文案金样本（spec §9、契约 §3.5 消费方映射、W plan Q8/Q9）。
// 常量字面量只在 deliveryCopy.ts 与本锁测试各出现一次；组件测试一律 import 常量。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NO_RECIPIENT_REASONS, type NoRecipientReason } from '../../api/rooms'
import {
  DELIVERY_READ_TIME_HINT,
  NEEDS_MENTION_COPY,
  NO_RECIPIENT_COPY,
  noRecipientLabel,
} from './deliveryCopy'

afterEach(() => { vi.restoreAllMocks() })

describe('deliveryCopy 文案冻结', () => {
  it('spec §9 两条文案逐字', () => {
    expect(NO_RECIPIENT_COPY).toBe('无收件人（@ 指定）')
    expect(NEEDS_MENTION_COPY).toBe('需 @')
  })

  it('读时语义悬停说明（Q8 裁定稿）逐字', () => {
    expect(DELIVERY_READ_TIME_HINT).toBe('按当前席位与驻场者判定；补座或坐下后可能不再显示')
  })
})

describe('noRecipientLabel（契约 §3.5 映射）', () => {
  const table: Record<NoRecipientReason, string | null> = {
    structural: null,
    not_session_room: null,
    non_human_unaddressed: null,
    addressed_unresolved: NO_RECIPIENT_COPY,
    root_no_seat: NO_RECIPIENT_COPY,
    needs_mention: NEEDS_MENTION_COPY,
  }

  it('六值逐一映射：四值两文案、三值不标', () => {
    for (const reason of NO_RECIPIENT_REASONS) {
      expect(noRecipientLabel(reason), reason).toBe(table[reason])
    }
  })

  it('structural 不得标「无收件人」', () => {
    expect(noRecipientLabel('structural')).toBeNull()
  })

  it('缺键（undefined）不标、不告警', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(noRecipientLabel(undefined)).toBeNull()
    expect(warn).not.toHaveBeenCalled()
  })

  it('词表外值不标，console.warn 带键名与原值且同值只告警一次', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const drifted = JSON.parse('"brand_new_reason"')
    expect(noRecipientLabel(drifted)).toBeNull()
    expect(noRecipientLabel(drifted)).toBeNull()
    expect(warn).toHaveBeenCalledTimes(1)
    expect(warn.mock.calls[0][0]).toMatchObject({ key: 'no_recipient', value: 'brand_new_reason' })
  })
})
