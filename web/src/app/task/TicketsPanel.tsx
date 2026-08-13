// TicketsPanel —— 工单面板（审核台核心）。
//
// 浏览器点「批准」与 CLI 敲 `reply --approve` 是同一件事，因此应答编码与 agentd
// 的契约严格对齐（review.ts）：gate 批准 → "allow"，gate 拒绝 → "deny: <理由>"，
// ask → 自由文本原样透传。
//
// 展示纪律：
//   - 权限/提问**全文完整展示、不截断**——读工单的 request 字段，不读事件
//     （事件 payload 里的 permission 是截断摘要，全文只在工单里）
//   - 拒绝**必须填理由**才能提交：理由会回到模型手里，不填它就原地重试同样的
//     操作、白烧一轮
//   - 断线（disabled）时全部控件禁用，但不能让已填的草稿丢失
import { useState } from 'react'
import { MessageSquareText, ShieldCheck } from 'lucide-react'
import type { Ticket } from '../../api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  buildTicketAnswer,
  parseTicketRequest,
  ticketKindLabel,
  validateReply,
} from './review'
import { errorMessage } from '../lib/format'

export interface TicketsPanelProps {
  tickets: Ticket[]
  disabled: boolean
  onReply: (ticket: Ticket, answer: string) => Promise<void>
  // bare 去掉本组件自带的卡片外框与「挂起工单」标题，只留工单本体：
  // 全局工单弹层里每行已是自己的 li 容器与任务归属行，再套一层卡片和标题就重了
  bare?: boolean
}

export function TicketsPanel({ tickets, disabled, onReply, bare = false }: TicketsPanelProps) {
  if (bare) {
    return (
      <div className="flex flex-col gap-3">
        {disabled && <p className="text-xs text-amber-700">已断开，暂不能作答（保留草稿）</p>}
        {tickets.length === 0 ? (
          <p className="text-sm text-muted-foreground">没有等待处理的工单。</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {tickets.map((t) => (
              <TicketCard key={t.id} ticket={t} disabled={disabled} onReply={onReply} />
            ))}
          </ul>
        )}
      </div>
    )
  }
  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-background p-4">
      <header className="flex items-center justify-between">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          <MessageSquareText className="size-4" />
          挂起工单
        </h2>
        <Badge variant={tickets.length > 0 ? 'destructive' : 'secondary'}>{tickets.length}</Badge>
      </header>
      {disabled && <p className="text-xs text-amber-700">已断开，暂不能作答（保留草稿）</p>}
      {tickets.length === 0 ? (
        <p className="text-sm text-muted-foreground">没有等待处理的工单。</p>
      ) : (
        <ul className="flex flex-col gap-3">
          {tickets.map((t) => (
            <TicketCard key={t.id} ticket={t} disabled={disabled} onReply={onReply} />
          ))}
        </ul>
      )}
    </section>
  )
}

// TicketCard 单张工单的审批界面。内部状态（拒绝理由/自由文本草稿）以 ticket.id
// 为 key 由父级 list 保持；工单被回答后从 pending 消失，草稿随组件卸载丢弃。
function TicketCard({
  ticket,
  disabled,
  onReply,
}: {
  ticket: Ticket
  disabled: boolean
  onReply: (ticket: Ticket, answer: string) => Promise<void>
}) {
  const req = parseTicketRequest(ticket)
  const isGate = req.kind === 'gate'
  const [mode, setMode] = useState<'idle' | 'deny'>('idle')
  const [reason, setReason] = useState('')
  const [freeText, setFreeText] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (action: 'approve' | 'deny' | 'answer') => {
    const validation = validateReply(req.kind, action, reason)
    if (validation) {
      setError(validation)
      return
    }
    const answer = buildTicketAnswer(req.kind, action, reason, freeText)
    setSubmitting(true)
    setError(null)
    try {
      await onReply(ticket, answer)
    } catch (err) {
      // agentd 的错误消息信息量大（「状态不允许」「工单已被回答」），必须原文透出
      setError(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  const blocked = disabled || submitting

  return (
    <li className="flex flex-col gap-2 rounded-md border p-3">
      <div className="flex items-center gap-2">
        <Badge variant={isGate ? 'default' : 'outline'}>{ticketKindLabel(req.kind)}</Badge>
        <span className="font-mono text-xs text-muted-foreground">{ticket.id}</span>
      </div>

      {/* 权限/提问全文：完整展示、不截断 */}
      <pre className="whitespace-pre-wrap break-words rounded-md bg-muted/50 p-2 font-mono text-xs leading-relaxed">
        {req.text || '（空）'}
      </pre>

      {error && (
        <p role="alert" className="break-words text-sm text-destructive">
          {error}
        </p>
      )}

      {isGate ? (
        mode === 'idle' ? (
          <div className="flex flex-wrap gap-2">
            <Button size="sm" disabled={blocked} onClick={() => void submit('approve')}>
              <ShieldCheck className="size-4" />
              批准
            </Button>
            <Button size="sm" variant="outline" disabled={blocked} onClick={() => setMode('deny')}>
              拒绝
            </Button>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs text-muted-foreground">
                拒绝理由（必填：会回到模型手里，不填它就原地重试同样的操作）
              </span>
              <textarea
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                rows={2}
                className="resize-y rounded-md border border-input bg-background p-2 font-mono text-xs"
                placeholder="例如：这个命令有破坏性，先改方案再继续…"
              />
              {reason.trim() === '' && (
                <span className="text-xs text-destructive">拒绝必须填写理由</span>
              )}
            </label>
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="destructive"
                disabled={blocked || reason.trim() === ''}
                onClick={() => void submit('deny')}
              >
                {submitting ? '提交中…' : '提交拒绝'}
              </Button>
              <Button size="sm" variant="ghost" disabled={submitting} onClick={() => setMode('idle')}>
                取消
              </Button>
            </div>
          </div>
        )
      ) : (
        <div className="flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">回答（原样透传给模型）</span>
            <textarea
              value={freeText}
              onChange={(e) => setFreeText(e.target.value)}
              rows={2}
              className="resize-y rounded-md border border-input bg-background p-2 font-mono text-xs"
              placeholder="输入你的回答…"
            />
          </label>
          <div>
            <Button
              size="sm"
              disabled={blocked || freeText.trim() === ''}
              onClick={() => void submit('answer')}
            >
              {submitting ? '提交中…' : '提交回答'}
            </Button>
          </div>
        </div>
      )}
    </li>
  )
}
