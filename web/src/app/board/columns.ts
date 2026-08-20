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

import type { Task } from '../../api/types'

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

// StateTone 是任务状态的视觉基调。看板卡片消费它决定圆点与文字的颜色。
//
// 与看板列（BoardColumn）是两个维度，不要混：列回答「归到哪一堆」，
// 基调回答「要不要抓你的眼睛」。completed 与 failed 同列不同调，
// waiting_answer 与 waiting_review 不同列却同调。
export type StateTone = 'idle' | 'active' | 'intervention' | 'done' | 'failed'

const STATE_TONES: Record<string, StateTone> = {
  pending: 'idle',
  running: 'active',
  waiting_answer: 'intervention',
  waiting_review: 'intervention',
  completed: 'done',
  failed: 'failed',
}

// stateTone 返回一个任务状态的视觉基调。
//
// 参数：
//   - state: 任务状态机的状态字符串，可能是本前端还不认识的新状态
//
// 返回：
//   - 该状态的视觉基调；未知状态回退 idle
//
// 注意：
//   - 未知状态回退 idle 而不是 active，刻意与 stateToColumn 的回退值不同。
//     分列回退 active 是为了让未知状态显眼地出现在「进行中」列（不消失）；
//     染色回退 idle 是因为把没见过的状态涂成绿色或琥珀色等于替它编造语义。
export function stateTone(state: string): StateTone {
  return STATE_TONES[state] ?? 'idle'
}

// needsIntervention 报告任务是否处于干预态——卡在你这儿、等你动手。
//
// 参数：
//   - state: 任务状态机的状态字符串
//
// 返回：
//   - waiting_answer / waiting_review 为 true，其余为 false
//
// 注意：
//   - 这个集合是**跨模块的口径**，仓库里另有三处依赖同一定义：
//     filter.ts 的 pendingOnly 筛选、counts.ts 的 pending 计数、
//     ProjectTree 的 wsCounts。改这里必须四处同改，columns.test.ts
//     有一条用例专门钉这个口径。
//   - failed 不在其中：它是终态，没有「等你动手就能继续」这回事，
//     想接着干的路径是重新 dispatch。
export function needsIntervention(state: string): boolean {
  return state === 'waiting_answer' || state === 'waiting_review'
}

// stateBadgeVariant 把任务状态映射成 Badge 的视觉变体。
//
// 参数：
//   - state: 任务状态机的状态字符串
//
// 返回：
//   - Badge 的 variant 名
//
// 注意：
//   - **只有任务详情页顶栏（TaskHeader）消费它。** 看板卡片改用
//     stateTone + StateDot 的「圆点 + 文字」形态（形态基准见 spec §1.1）。
//     两者曾共用这一个函数，那是耦合失误：看板是密集列表，行内标记的
//     视觉噪声本就该低于胶囊；详情页只有一个状态，胶囊才恰当。
//   - 返回类型里仍保留 'outline' 字面量是刻意的：Badge 支持这一档，将来
//     别的状态可能用得上；收窄类型只会逼后来人再改一次签名。
export function stateBadgeVariant(
  state: string,
): 'default' | 'secondary' | 'destructive' | 'outline' | 'intervention' {
  switch (state) {
    case 'failed':
      return 'destructive'
    case 'waiting_answer':
    case 'waiting_review':
      return 'intervention'
    case 'running':
      return 'default'
    default:
      return 'secondary'
  }
}

// unlinkedOnly 把任务列表收敛到「未挂账」——账本里没有卡认领它的那些。
//
// why 它存在：账本启用时工作项看板（/cards）是主入口，本页是未挂账 task 的兜底；
// 账本未启用时本页即主入口，不应调用这个过滤器把挂卡 task 隐藏掉。
//
// 参数：unlinked 为未挂账 task id 集合；传 null 表示账本还没读到，此时**不过滤**
// ——宁可多显示几条，也不能因为账本没到位就把任务凭空藏起来。
export function unlinkedOnly(tasks: Task[], unlinked: Set<string> | null): Task[] {
  if (unlinked === null) return tasks
  return tasks.filter((task) => unlinked.has(task.id))
}
