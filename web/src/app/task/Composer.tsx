// Composer —— 任务推进的对话式收口（AdvanceActions 的继任者）。
//
// 可用性按状态机（proto.transitTable 对齐视图，与 AdvanceActions 相同）：
//   - 输入框 + 续发修改（continue）/ 完成任务（done）：仅 waiting_review
//   - 停止任务：非终态可用，弱化为红字按钮（不可逆，二次确认）
//   - 恢复执行 + 强制收口（resume/force）：仅 waiting_answer
//   - 终态：只读说明；断线：禁用但保留已填内容
//
// 交互：Enter 发送，Shift+Enter 换行——「对话」的形态判据之一（spec §2.4）。
import { useState } from 'react'
import type { Task } from '../../api/types'
import { continueTask, doneTask, resumeTask, stopTask } from '../../api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'

type Busy = 'continue' | 'done' | 'stop' | 'resume' | null

// isTerminal 判断任务是否已到终态（completed / failed）。
function isTerminal(state: string): boolean {
  return state === 'completed' || state === 'failed'
}

// HINTS 是各状态下 composer 上方的提示语（人话说明当前能做什么）。
const HINTS: Record<string, string> = {
  running: '任务运行中——回合结束进入待审后才能下指令；停止任务随时可用。',
  waiting_review: '这一轮已干完，等你裁决——下修改指令让它继续，或完成归档。',
  waiting_answer: '任务在等一张工单的应答——裁决入口在左栏底部的工单面板。',
}

// Composer 渲染推进区。disabled=断线；onChanged 在任何动作成功后回调刷新。
export function Composer({ task, disabled, onChanged }: { task: Task; disabled: boolean; onChanged: () => void }) {
  const [instructions, setInstructions] = useState('')
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState<Busy>(null)
  const [message, setMessage] = useState<{ text: string; kind: 'info' | 'error' } | null>(null)
  const [confirming, setConfirming] = useState<'done' | 'stop' | null>(null)

  const canReview = task.state === 'waiting_review'
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

  const send = () => {
    if (instructions.trim() === '' || blocked || !canReview) return
    void runAction('continue', async () => {
      await continueTask(task.id, instructions.trim())
      setMessage({ text: '已续发指令，任务回到 running。', kind: 'info' })
      setInstructions('')
    })
  }

  if (isTerminal(task.state)) {
    return (
      <div className="border-t px-3.5 py-2.5">
        <p className="mx-auto max-w-[760px] text-xs text-muted-foreground">
          任务已{task.state === 'completed' ? '归档（completed）' : '终结（failed）'}
          {task.done_note ? `——完成说明：${task.done_note}` : '，没有可用的推进动作。'}
        </p>
      </div>
    )
  }

  return (
    <div className="border-t px-3.5 py-2.5">
      <div className="mx-auto max-w-[760px]">
        <p className="mb-1.5 text-xs text-muted-foreground">
          {disabled ? '已断开，推进动作已禁用（保留已填内容）' : (HINTS[task.state] ?? '')}
        </p>
        {message && (
          <p role="alert" className={`mb-1.5 break-words text-sm ${message.kind === 'error' ? 'text-destructive' : 'text-foreground/80'}`}>
            {message.text}
          </p>
        )}

        {canResume && (
          <div className="mb-1.5 flex flex-wrap items-center gap-3 text-xs">
            <label className="flex items-center gap-1.5">
              <input type="checkbox" className="size-3.5" checked={force} onChange={(e) => setForce(e.target.checked)} />
              强制收口（绕过对账，直接推到 Review；会留下「人工强制」事件）
            </label>
            <Button
              size="sm" variant="outline" disabled={blocked}
              onClick={() => void runAction('resume', async () => {
                const rep = await resumeTask(task.id, force)
                setMessage({ text: rep.note || (rep.forced ? '已强制收口。' : '已恢复执行。'), kind: 'info' })
              })}
            >
              {busy === 'resume' ? '恢复中…' : '恢复执行'}
            </Button>
          </div>
        )}

        <div className="flex flex-col gap-1.5 rounded-xl border bg-background p-2 focus-within:border-muted-foreground/50">
          <textarea
            value={instructions}
            onChange={(e) => setInstructions(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
            }}
            rows={2}
            disabled={blocked || !canReview}
            className="resize-none bg-transparent text-sm leading-relaxed outline-none disabled:opacity-60"
            placeholder={canReview ? '下修改指令，回给模型让它继续改…（Enter 发送，Shift+Enter 换行）' : '任务运行中，暂不能下指令'}
          />
          <div className="flex items-center gap-2">
            {canStop && (
              <button type="button" disabled={blocked} onClick={() => setConfirming('stop')} className="px-1 text-xs text-destructive hover:underline disabled:opacity-50">
                停止任务
              </button>
            )}
            <span className="flex-1" />
            {canReview && (
              <>
                <Button size="sm" variant="outline" disabled={blocked} onClick={() => setConfirming('done')}>
                  ✓ 完成任务
                </Button>
                <Button size="sm" disabled={blocked || instructions.trim() === ''} onClick={send}>
                  {busy === 'continue' ? '提交中…' : '↑ 续发修改'}
                </Button>
              </>
            )}
          </div>
        </div>
      </div>

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
              text: r.worktree_removed ? '已停止；agentd 创建的工作树已删除。' : '已停止（工作树保留：用户自带工作树 / 原地模式，或清理失败）。',
              kind: 'info',
            })
          })
        }}
        onCancel={() => setConfirming(null)}
      />
    </div>
  )
}
