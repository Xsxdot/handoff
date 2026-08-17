// useTaskPlan —— 取一个任务的派发指令原文（会话流顶部那条气泡的数据源）。
//
// 边界：
//   - 取不到就当没有：404 的三种含义（任务不存在 / 老任务没归档 / 文件被删）
//     对界面是同一件事——没有可展示的派发指令。不弹错、不重试，会话流本身
//     不受影响（同 useChangedFiles 的降级纪律）
//   - 只取一次：派发指令在任务创建那一刻就定死了，不随事件流变化，
//     不需要跟着 2.5s 心跳刷
import { useEffect, useState } from 'react'
import { fetchTaskPlan } from '../../api/client'
import type { TaskPlan } from '../../api/types'

// useTaskPlan 返回派发指令；null = 还没取到或这个任务没有。
export function useTaskPlan(taskId: string): TaskPlan | null {
  const [plan, setPlan] = useState<TaskPlan | null>(null)
  useEffect(() => {
    let cancelled = false
    setPlan(null) // 换任务先清空，避免上一条任务的指令闪现在新任务顶上
    fetchTaskPlan(taskId)
      .then((p) => {
        if (!cancelled) setPlan(p)
      })
      .catch(() => {
        if (!cancelled) setPlan(null)
      })
    return () => {
      cancelled = true
    }
  }, [taskId])
  return plan
}
