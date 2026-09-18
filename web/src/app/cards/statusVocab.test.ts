// 词表金样本：TS 词表必须与 Go 真源逐值一致。
// 为什么读 Go 源码而不是手抄：手抄的词表会在 Go 侧加/改状态时静默漂移，
// 「两端各自有测试、中间没测」正是本卡要堵的形状。读源码让 Go 变则此测必红。
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { CARD_STATUSES, isKnownCardStatus } from './statusVocab'

describe('账本状态词表（与 internal/ledger/types.go 逐值一致）', () => {
  it('TS 词表 = Go 的五个 Status 字面量，顺序也一致', () => {
    const here = dirname(fileURLToPath(import.meta.url))
    const go = readFileSync(resolve(here, '../../../../internal/ledger/types.go'), 'utf8')
    const got = [...go.matchAll(/Status(?:Todo|Doing|Review|Done|Closed)\s*=\s*"([^"]+)"/g)].map((m) => m[1])
    expect(got).toEqual(['待办', '进行中', '待审阅', '已完成', '终止'])
    expect([...CARD_STATUSES]).toEqual(got)
  })

  it('反例：词表外状态不被认作已知（绝不扩词表吞未知值）', () => {
    expect(isKnownCardStatus('自定义状态')).toBe(false)
    expect(isKnownCardStatus('completed')).toBe(false)
  })
})
