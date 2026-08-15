// AdvanceActions —— 任务推进动作：continue / done / stop / resume。
//
// 可用性按状态机（proto.transitTable 的对齐视图）：
//   - continue / done：只在 waiting_review 可用（done 归档、continue 续发指令）
//   - stop：completed / failed（终态）不可用，其余状态可用
//   - resume：waiting_answer 可用——它是 reply 返回 502 后唯一自助出口
//     （重投已落库但未送达的应答；force 时强制收口，绕过对账）
//
// 纪律：
//   - stop / done 是不可逆操作，必须二次确认（ConfirmDialog）
//   - 断线（disabled）时全部禁用；错误把 agentd 原文透出
import { useState } from 'react'
import { CheckCircle2, Play, RotateCcw, Square, Undo2 } from 'lucide-react'
import type { Task } from '../../api/types'
import { continueTask, doneTask, resumeTask, stopTask } from '../../api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'

type Busy = 'continue' | 'done' | 'stop' | 'resume' | null

export interface AdvanceActionsProps {
  task: Task
  disabled: boolean
  onChanged: () => void
}

// isTerminal 判断任务是否已到终态（completed / failed）——stop 在这两个状态下不可用。
function isTerminal(state: string): boolean {
  return state === 'completed' || state === 'failed'
}

export function AdvanceActions({ task, disabled, onChanged }: AdvanceActionsProps) {
  const [instructions, setInstructions] = useState('')
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState<Busy>(null)
  const [message, setMessage] = useState<{ text: string; kind: 'info' | 'error' } | null>(null)
  const [confirming, setConfirming] = useState<'done' | 'stop' | null>(null)

  const canContinue = task.state === 'waiting_review'
  const canDone = task.state === 'waiting_review'
  const canStop = !isTerminal(task.state)
  const canResume = task.state === 'waiting_answer'
  const blocked = disabled || busy !== null

  const runAction = async (op: NonNullable<Busy>, action: () => Promise<void>) => {
    setBusy(op)
    setMessage(null)
    try {
      await action()
      onChanged()
    } catch (err) {
      setMessage({ text: errorMessage(err), kind: 'error' })
    } finally {
      setBusy(null)
    }
  }

  return (
    <section className="flex flex-col gap-3 rounded-lg border bg-background p-4">
      <h2 className="flex items-center gap-2 text-sm font-medium">
        <Play className="size-4" />
        推进任务
      </h2>

      {disabled && (
        <p className="text-xs text-amber-700">已断开，推进动作已禁用（保留已填内容）</p>
      )}

      {message && (
        <p role="alert" className={`break-words text-sm ${message.kind === 'error' ? 'text-destructive' : 'text-foreground/80'}`}>
          {message.text}
        </p>
      )}

      {/* continue：续发修改指令（仅 waiting_review） */}
      {canContinue && (
        <div className="flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">修改指令（回给模型，让它继续改）</span>
            <textarea
              value={instructions}
              onChange={(e) => setInstructions(e.target.value)}
              rows={2}
              className="resize-y rounded-md border border-input bg-background p-2 font-mono text-xs"
              placeholder="例如：把审批理由改为 deny: xxx，再跑一遍测试…"
            />
          </label>
          <Button
            size="sm"
            disabled={blocked || instructions.trim() === ''}
            onClick={() =>
              void runAction('continue', async () => {
                await continueTask(task.id, instructions.trim())
                setMessage({ text: '已续发指令，任务回到 running。', kind: 'info' })
                setInstructions('')
              })
            }
          >
            <Undo2 className="size-4" />
            {busy === 'continue' ? '提交中…' : '续发修改'}
          </Button>
        </div>
      )}

      {/* done / stop：不可逆，二次确认 */}
      <div className="flex flex-wrap gap-2">
        {canDone && (
          <Button
            size="sm"
            disabled={blocked}
            onClick={() => setConfirming('done')}
          >
            <CheckCircle2 className="size-4" />
            完成任务
          </Button>
        )}
        {canStop && (
          <Button
            size="sm"
            variant="destructive"
            disabled={blocked}
            onClick={() => setConfirming('stop')}
          >
            <Square className="size-4" />
            停止任务
          </Button>
        )}
      </div>

      {/* resume：重投未送达应答 / 会话对账（仅 waiting_answer） */}
      {canResume && (
        <div className="flex flex-col gap-2">
          <label className="flex items-center gap-2 text-xs">
            <input
              type="checkbox"
              checked={force}
              onChange={(e) => setForce(e.target.checked)}
              className="size-3.5"
            />
            强制收口（绕过对账，直接把任务推到 Review；会留下「人工强制」事件）
          </label>
          <Button
            size="sm"
            variant="outline"
            disabled={blocked}
            onClick={() =>
              void runAction('resume', async () => {
                const rep = await resumeTask(task.id, force)
                setMessage({
                  text: rep.note || (rep.forced ? '已强制收口。' : '已恢复执行。'),
                  kind: 'info',
                })
              })
            }
          >
            <RotateCcw className="size-4" />
            {busy === 'resume' ? '恢复中…' : '恢复执行'}
          </Button>
        </div>
      )}

      {!canContinue && !canDone && !canStop && !canResume && (
        <p className="text-xs text-muted-foreground">当前状态没有可用的推进动作。</p>
      )}

      <ConfirmDialog
        open={confirming === 'done'}
        title="完成任务？"
        description="任务将被置为 completed 并回收执行器。worktree 由 agentd 管理时会被删除。此操作不可撤销。"
        confirmLabel="完成任务"
        busy={busy === 'done'}
        onConfirm={() => {
          setConfirming(null)
          void runAction('done', async () => {
            await doneTask(task.id)
            setMessage({ text: '任务已归档为 completed。', kind: 'info' })
          })
        }}
        onCancel={() => setConfirming(null)}
      />

      <ConfirmDialog
        open={confirming === 'stop'}
        title="停止任务？"
        description="将停止执行器、作废全部挂起工单，并把任务置为 failed。此操作不可撤销。"
        confirmLabel="停止任务"
        destructive
        busy={busy === 'stop'}
        onConfirm={() => {
          setConfirming(null)
          void runAction('stop', async () => {
            const r = await stopTask(task.id)
            setMessage({
              text: r.worktree_removed
                ? '已停止；agentd 创建的工作树已删除。'
                : '已停止（工作树保留：用户自带工作树 / 原地模式，或清理失败）。',
              kind: 'info',
            })
          })
        }}
        onCancel={() => setConfirming(null)}
      />
    </section>
  )
}
