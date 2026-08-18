// useGlobalTickets —— 跨任务的挂起工单聚合（spec §5.2）。
//
// 职责：给左栏角标与工单弹层提供「此刻一共欠多少张工单、分别属于谁」。
//
// 边界：
//   - 不做裁决：应答走 TicketsPanel → replyTicket，本 hook 只提供 refresh
//   - 不跨机：前端目前只请求本机 /api/tasks，不带 ?scope=all（spec §5.2 末段）
//
// 取数形态（无新接口）：pending_tickets 只出现在 GET /api/tasks/{id} 的响应里，
// 任务列表上没有。所以从任务流筛出 state === 'waiting_answer' 的任务，逐个取详情。
// 这是 N+1，但可接受：waiting_answer 的任务每一个都在**阻塞一个人**，数量天然
// 极小。若将来常态化到两位数，再加 GET /api/tickets 汇总接口——那时它有真实依据。
//
// 为什么按 id 集合而不是按数组身份去重触发：任务流每 2.5s 换一个新数组，按数组
// 身份做依赖会每 2.5s 打一轮 N+1 请求。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { fetchTaskDetail } from '../../api/client'
import type { Task, Ticket } from '../../api/types'

export interface GlobalTicket {
  ticket: Ticket
  task: Task
}

export interface GlobalTickets {
  items: GlobalTicket[]
  count: number
  // byWorkDir 是「目录绝对路径 → 挂起工单张数」，供左栏目录行排序用。
  //
  // 空 work_dir 的任务**不进这张表**：它们按原地模式归主目录，而这里不知道
  // 哪个是主目录（那要看项目树）。归集主目录那一步由 ProjectTree 做——它手上
  // 有 ws.is_main，判据与 tasksOfWorkspace 一致，两处不会分叉。
  byWorkDir: Map<string, number>
  refresh: () => void
}

export function useGlobalTickets(tasks: Task[]): GlobalTickets {
  const [items, setItems] = useState<GlobalTicket[]>([])
  const [nonce, setNonce] = useState(0)

  const waiting = useMemo(() => tasks.filter((t) => t.state === 'waiting_answer'), [tasks])
  // key 是 waiting 任务的 id 集合的稳定表示。任务流每 2.5s 换新数组，但只要这批
  // id 没变就不需要重新取详情。
  const key = useMemo(() => waiting.map((t) => t.id).sort().join(','), [waiting])

  useEffect(() => {
    if (key === '') {
      setItems([])
      return
    }
    let cancelled = false
    const ids = key.split(',')
    // 逐个任务取详情；单个失败只丢它自己那份，不连累其余（诚实降级）
    Promise.all(
      ids.map(async (id) => {
        try {
          const d = await fetchTaskDetail(id)
          const task = waiting.find((t) => t.id === id)
          if (!task) return []
          return (d.pending_tickets ?? []).map((ticket) => ({ ticket, task }))
        } catch {
          return []
        }
      }),
    ).then((groups) => {
      if (!cancelled) setItems(groups.flat())
    })
    return () => {
      cancelled = true
    }
    // waiting 故意不进依赖：它每 2.5s 是新数组，而 key 已经代表了它的身份
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, nonce])

  const refresh = useCallback(() => setNonce((n) => n + 1), [])

  // 从 items 派生而不是在取详情时顺手累加：items 是这个 hook 的单一真相，
  // 两份状态各自累加迟早会对不上（一次失败的详情请求只丢它自己那份，
  // 而累加器不知道该减掉多少）。
  const byWorkDir = useMemo(() => {
    const m = new Map<string, number>()
    for (const { task } of items) {
      if (task.work_dir === '') continue
      m.set(task.work_dir, (m.get(task.work_dir) ?? 0) + 1)
    }
    return m
  }, [items])

  return { items, count: items.length, byWorkDir, refresh }
}
