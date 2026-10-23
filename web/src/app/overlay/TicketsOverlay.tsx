// TicketsOverlay —— 全局工单弹层（spec §5.2）。
//
// 职责：把所有目录下的挂起工单列在一处，就地裁决；每行标注它属于哪个任务与目录，
// 并提供「跳到该任务」。
//
// 边界：
//   - **全局，不被当前选中目录过滤**（spec §1.3）。一张工单可能属于任何一个任务，
//     按当前目录筛掉等于要求人先猜对是哪个任务才能看到它
//   - 裁决逻辑不重写：整段复用 TicketsPanel（含「拒绝必须填理由」这条）
//
// 全局入口保留给目录间汇总；会话角色状态也可打开同一 TicketsPanel，并只展示当前
// 会话关联工单。EventChip 本身仍不可点，只负责说明事件。
import { ArrowUpRight } from 'lucide-react'
import type { BaseDir } from '../workbench/useWorkbench'
import { replyTicket } from '../../api/client'
import { TicketsPanel } from '../task/TicketsPanel'
import { Overlay } from './Overlay'
import type { GlobalTickets } from './useGlobalTickets'

export interface TicketsOverlayProps {
  disabled?: boolean
  tickets: GlobalTickets
  onOpenTask: (base: BaseDir | null, taskId: string) => void
  onClose: () => void
}

export function TicketsOverlay({ tickets, onOpenTask, onClose, disabled = false }: TicketsOverlayProps) {
  return (
    <Overlay title={`工单（${tickets.count}）`} onClose={onClose}>
      {tickets.items.length === 0 ? (
        <p className="p-6 text-center text-sm text-muted-foreground">没有待处理的工单。</p>
      ) : (
        <ul className="divide-y">
          {tickets.items.map(({ ticket, task }) => (
            <li key={ticket.id} className="p-3">
              <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
                <span className="truncate font-medium text-foreground">{task.name || task.id}</span>
                <span aria-hidden>·</span>
                <span className="truncate font-mono">{task.work_dir || '（原地）'}</span>
                <span aria-hidden>·</span>
                <span className="shrink-0">{task.machine === '' ? '本机' : task.machine}</span>
                <button
                  type="button"
                  onClick={() => {
                    onClose()
                    // 基准目录传 null：这里没有树，解析目录是 Shell 的活
                    onOpenTask(null, task.id)
                  }}
                  className="ml-auto inline-flex shrink-0 items-center gap-0.5 rounded px-1.5 py-0.5 hover:bg-accent hover:text-foreground"
                >
                  跳到该任务
                  <ArrowUpRight className="size-3" />
                </button>
              </div>
              <TicketsPanel
                tickets={[ticket]}
                disabled={disabled}
                bare
                onReply={async (t, answer) => {
                  if (disabled) return
                  await replyTicket(task.id, { ticket_id: t.id, answer })
                  tickets.refresh()
                }}
              />
            </li>
          ))}
        </ul>
      )}
    </Overlay>
  )
}
