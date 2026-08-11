// columns.ts —— 看板列与任务状态机的映射（硬契约，vitest 钉死）。
//
// 列是状态机六状态的聚合视图，映射是**产品契约**而非实现细节，改动必须同步
// 测试 src/app/board/columns.test.ts：
//
//   | 状态机            | 看板列   | 备注                        |
//   | pending           | waiting  | 等待执行                     |
//   | running           | active   | 进行中                       |
//   | waiting_answer    | active   | 进行中 + 「等你答复」标记      |
//   | waiting_review    | review   | Review                       |
//   | completed / failed| done     | failed 需视觉区分             |
//
// waiting_answer 刻意不单列：它在实际使用中是瞬时状态，绝大多数工单审核者
// 当场就答了，给它独立一列是给噪声让位。

export type BoardColumn = 'waiting' | 'active' | 'review' | 'done'

// BOARD_COLUMNS 是看板从左到右的列顺序。
export const BOARD_COLUMNS: readonly BoardColumn[] = ['waiting', 'active', 'review', 'done']

// COLUMN_LABELS 是每列的中文标题。
export const COLUMN_LABELS: Record<BoardColumn, string> = {
  waiting: '等待执行',
  active: '进行中',
  review: 'Review',
  done: '完成',
}

// STATE_TO_COLUMN 是任务状态机到看板列的映射。
// 未知状态回退 active：宁可让新状态显眼地出现在「进行中」，也不让任务从看板上
// 凭空消失（失联比归类错误更糟）。
const STATE_TO_COLUMN: Record<string, BoardColumn> = {
  pending: 'waiting',
  running: 'active',
  waiting_answer: 'active',
  waiting_review: 'review',
  completed: 'done',
  failed: 'done',
}

// stateToColumn 返回一个任务状态所属的看板列。
export function stateToColumn(state: string): BoardColumn {
  return STATE_TO_COLUMN[state] ?? 'active'
}

// isWaitingAnswer 报告任务是否处于「等你答复」：进行中列里需要醒目标记的状态。
export function isWaitingAnswer(state: string): boolean {
  return state === 'waiting_answer'
}

// isFailed 报告任务是否失败：完成列里需要视觉区分的状态。
export function isFailed(state: string): boolean {
  return state === 'failed'
}

// STATE_LABELS 是任务状态的展示文案（看板卡片与详情页共用）。
export const STATE_LABELS: Record<string, string> = {
  pending: '等待执行',
  running: '进行中',
  waiting_answer: '等你答复',
  waiting_review: 'Review',
  completed: '已完成',
  failed: '失败',
}

// stateLabel 返回状态的中文文案；未知状态原样透出（不吞数据）。
export function stateLabel(state: string): string {
  return STATE_LABELS[state] ?? state
}

// stateBadgeVariant 把任务状态映射成 Badge 的视觉变体：
// failed 用 destructive（视觉区分），waiting_answer 用 destructive（要人介入），
// waiting_review 用 outline（安静待审），running 用 default（活跃），其余次要。
export function stateBadgeVariant(
  state: string,
): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (state) {
    case 'failed':
    case 'waiting_answer':
      return 'destructive'
    case 'running':
      return 'default'
    case 'waiting_review':
      return 'outline'
    case 'completed':
      return 'secondary'
    default:
      return 'secondary'
  }
}
