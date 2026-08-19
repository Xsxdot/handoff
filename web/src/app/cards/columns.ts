// 工作项看板的纯逻辑：列 = 工作流状态序列（骨架+插入）；「需要你」
// 合一过滤 = 等人 ∪ open 裁决 ∪ conflict ∪ 未决工单；被并卡不在看板成列。
// 这些是产品契约（原型走查确认），测试钉死防漂移。
import type { CardView } from '../../api/ledger'

export function boardColumns(workflowStates: string[]): string[] {
  return [...workflowStates, '终止']
}

export function cardsInColumn(cards: CardView[], status: string): CardView[] {
  return cards.filter((card) => card.status === status && !card.following)
}

export function needsAttention(card: CardView): boolean {
  return Boolean(card.needs) || card.open_decisions > 0 || card.conflict || card.open_tickets > 0
}

export function filterNeeds(cards: CardView[], on: boolean): CardView[] {
  return on ? cards.filter(needsAttention) : cards
}
