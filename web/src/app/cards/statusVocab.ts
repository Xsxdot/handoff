// statusVocab —— 账本状态词表的前端半边（「词表外状态串即 fail」的可判锚）。
//
// 职责：把 internal/ledger/types.go:15-19 的五个 Status 字面量固化成 TS 常量，
//       并提供「是否词表内」的判据；词表外取值原样透传（不白屏、不发明翻译）。
// 边界：
//   - 不碰 React、不发请求、不做 HTTP
//   - 词表逐值一致由 statusVocab.test.ts 读 Go 源码锁定（改 Go 词表必红）；
//     移动「卡」tab 的筛选 chip 直接迭代本词表，不另抄一份
export const CARD_STATUSES = ['待办', '进行中', '待审阅', '已完成', '终止'] as const

export type CardStatus = (typeof CARD_STATUSES)[number]

const KNOWN = new Set<string>(CARD_STATUSES)

// isKnownCardStatus 判断一个状态串是否在受控词表内。
// 注意：词表外**不是错误**——工作流可自定义状态；调用方按兜底渲染，不丢弃卡。
export function isKnownCardStatus(status: string): boolean {
  return KNOWN.has(status)
}
