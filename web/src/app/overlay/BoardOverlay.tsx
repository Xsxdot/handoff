// BoardOverlay —— 任务看板弹层（spec §5.1）。
//
// 职责：把既有的 BoardPage 装进 Overlay 基座，并在点开卡片时关掉自己。
//
// 边界：
//   - 看板内容一行不重写。四列布局、卡片、干预态标记全部是 BoardPage 的既有实现
//   - **全局，不被当前选中目录过滤**（spec §1.3）：它是「你还欠哪些没处理」的
//     总账，被当前选中项筛掉会直接导致漏处理
//
// 点卡片的行为（原型 AGENTS.md：Opening an actionable card returns to its existing
// task session in the workbench）：关闭弹层 → 选中该任务所在目录 → 在中央开它的
// TUI tab。三件事的实现在 Shell 的 openTaskTui 里，这里只负责先关自己。
import type { ProjectTreeResp, Task } from '../../api/types'
import type { PollState } from '../data/usePoll'
import type { BaseDir } from '../workbench/useWorkbench'
import { BoardPage } from '../board/BoardPage'
import { Overlay } from './Overlay'

export interface BoardOverlayProps {
  // unlinkedTaskIds 未挂账 task id 集合；null = 账本未就绪，看板不做未挂账过滤
  tasksState: PollState<Task[]>
  tree: ProjectTreeResp | null
  onOpenTask: (base: BaseDir | null, taskId: string) => void
  unlinkedTaskIds?: Set<string> | null
  // ledgerEnabled 由 Shell 的一次性探测传入；未启用时看板是任务主入口。
  ledgerEnabled?: boolean
  onClose: () => void
}

export function BoardOverlay({ tasksState, tree, unlinkedTaskIds, ledgerEnabled = false, onOpenTask, onClose }: BoardOverlayProps) {
  return (
    <Overlay title="任务看板（未挂账兜底）" onClose={onClose} wide tall>
      <BoardPage
        tasksState={tasksState}
        tree={tree}
        unlinkedTaskIds={unlinkedTaskIds ?? null}
        ledgerEnabled={ledgerEnabled}
        onOpenTask={(base, id) => {
          onClose()
          onOpenTask(base, id)
        }}
      />
    </Overlay>
  )
}
