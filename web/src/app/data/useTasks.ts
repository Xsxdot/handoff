// useTasks —— 任务流：2.5s。W2 已验证的节奏，W3b 不改——看板卡片、左栏任务节点、
// 全部聚合计数都从这条流算，它跳动其余才跟着跳动。
import { usePoll } from './usePoll'
import { fetchTasks } from '../../api/client'
import type { Task } from '../../api/types'

const TASKS_INTERVAL = 2500
export function useTasks(): ReturnType<typeof usePoll<Task[]>> {
  return usePoll(fetchTasks, TASKS_INTERVAL)
}
