// taskRun —— 「挂账行 → 任务流实况」的共享关联口径（B369.8 §3.2）。
//
// 职责：把账本 task_states 行关联到任务流里的真实任务，并判定「此刻在不在跑」。
// 两个消费方：CardDrawer（关联执行块）与 Shell（compact 会话打开方式 scene 档的
// 解析链）——同一口径两处用，抽到这里避免复制出第二套判定。
//
// 边界：
//   - linkedTaskOf 关联不上返回 undefined：关联不上是真实情形（任务已归档清出流 /
//     流首拉未回），调用方按「实况未知」如实降级，不猜不冒充——与
//     internal/ledger/taskstate.go 文件头「滞后要显性化，不拿陈旧实况冒充新鲜」
//     是同一纪律在前端的落法。
//   - isRunningRow 口径刻意与看板分栏、任务选择弹层同源：非 isTerminalState 即在跑
//     （waiting_answer/waiting_review 是「等你动手」，不是「结束」；spec §5 明令
//     复用这一个终态集合，不许自造第三套）。关联不上的一律不算在跑：不知道的事
//     不能报成「活着」。
import type { Task } from '../../api/types'
import type { TaskStateRow } from '../../api/ledger'
import { isTerminalState } from '../workbench/TaskPickerDialog'

export function linkedTaskOf(row: TaskStateRow, tasks: Task[] | undefined): Task | undefined {
  return tasks?.find((task) => task.id === row.TaskID)
}

export function isRunningRow(row: TaskStateRow, tasks: Task[] | undefined): boolean {
  const live = linkedTaskOf(row, tasks)
  return live !== undefined && !isTerminalState(live.state)
}
