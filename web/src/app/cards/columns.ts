// 工作项看板的纯逻辑：列 = 工作流状态序列（骨架+插入）；「需要你」
// 合一过滤 = 等人 ∪ open 裁决 ∪ conflict ∪ 未决工单；被并卡不在看板成列。
// 这些是产品契约（原型走查确认），测试钉死防漂移。
import type { CardView } from '../../api/ledger'

export interface BoardLayout {
  columns: string[]
  state_to_column: Record<string, string>
  fallback: string
}

export const DEFAULT_BOARD_COLUMNS = ['代办', '沟通中', '进行中', '审核中', '结束']

export function defaultBoardLayout(states: string[]): BoardLayout {
  const state_to_column: Record<string, string> = {
    待办: '代办', 已出spec: '沟通中', '已出 spec': '沟通中',
    进行中: '进行中', 待审阅: '审核中', 待合并: '审核中',
    已完成: '结束', 终止: '结束',
  }
  for (const state of states) if (!(state in state_to_column)) state_to_column[state] = '进行中'
  return { columns: [...DEFAULT_BOARD_COLUMNS], state_to_column, fallback: '进行中' }
}

export function normalizeBoardLayout(layout: BoardLayout | undefined, states: string[]): BoardLayout {
  const fallback = layout ?? defaultBoardLayout(states)
  if (fallback.columns.length !== 5 || new Set(fallback.columns).size !== 5 || fallback.columns.some((column) => column.trim() === '')) {
    return defaultBoardLayout(states)
  }
  const columns = [...fallback.columns]
  const state_to_column = { ...fallback.state_to_column }
  const safeFallback = columns.includes(fallback.fallback) ? fallback.fallback : columns[0]
  for (const state of states) {
    if (!state_to_column[state] || !columns.includes(state_to_column[state])) state_to_column[state] = safeFallback
  }
  return { columns, state_to_column, fallback: safeFallback }
}

export function boardColumnFor(status: string, layout: BoardLayout): string {
  const mapped = layout.state_to_column[status]
  return mapped && layout.columns.includes(mapped) ? mapped : layout.fallback
}

export function boardColumns(states: string[], layout?: BoardLayout): string[] {
  return normalizeBoardLayout(layout, states).columns
}

export function cardsInColumn(cards: CardView[], column: string, layout?: BoardLayout): CardView[] {
  const resolved = normalizeBoardLayout(layout, cards.map((card) => card.status))
  return cards.filter((card) => boardColumnFor(card.status, resolved) === column && !card.following)
}

export function needsAttention(card: CardView): boolean {
  return Boolean(card.needs) || card.open_decisions > 0 || card.conflict || card.open_tickets > 0
}

export function filterNeeds(cards: CardView[], on: boolean): CardView[] {
  return on ? cards.filter(needsAttention) : cards
}

// mergeStateOrder 把多条工作流的状态序列合成一条列序。
//
// why 不能取并集：并集的顺序 = 首次出现顺序，先出现的工作流若没有后置状态，
// 另一条流独有的后置状态就会被甩到「已完成」后面去了——看板上后置状态会排在
// 最后一列（2026-08-19 真机看到）。这里按各序列
// 内部的先后关系做拓扑排序，同层保持首次出现顺序，两条流的相对次序都不破坏。
//
// 参数：sequences 各工作流的状态序列（每条自身有序）。
// 返回：合并后的状态序列，含全部出现过的状态，不重复。
// 注意：先后关系成环（两条流对同一对状态的次序相反）时不报错也不丢状态，
// 环上剩余状态按首次出现顺序追加——列序是呈现，宁可退化也不能少画一列。
export function mergeStateOrder(sequences: string[][]): string[] {
  const order: string[] = []
  const after = new Map<string, Set<string>>() // 状态 → 必须排在它前面的状态集
  for (const states of sequences) {
    for (const [index, state] of states.entries()) {
      if (!after.has(state)) {
        after.set(state, new Set())
        order.push(state)
      }
      if (index > 0) after.get(state)?.add(states[index - 1])
    }
  }
  const placed = new Set<string>()
  const result: string[] = []
  let progressed = true
  while (progressed && placed.size < order.length) {
    progressed = false
    for (const state of order) {
      if (placed.has(state)) continue
      const blockers = after.get(state) ?? new Set()
      if ([...blockers].every((blocker) => placed.has(blocker))) {
        placed.add(state)
        result.push(state)
        progressed = true
      }
    }
  }
  for (const state of order) if (!placed.has(state)) result.push(state) // 环上的兜底
  return result
}

// visibleColumns 决定看板实际画哪几列。
//
// why 筛选时要折叠空列：「需要你」筛完只剩两三张卡，但看板照画全部列，命中的
// 卡被中间的空列推到横向滚动区外面——徽标写着 4、屏幕上只看得见 2
// （2026-08-19 真机看到）。不筛选时空列要留着，看板得能看出流程形状。
export function visibleColumns(columns: string[], cards: CardView[], collapseEmpty: boolean, layout?: BoardLayout): string[] {
  if (!collapseEmpty) return columns
  return columns.filter((column) => cardsInColumn(cards, column, layout).length > 0)
}
