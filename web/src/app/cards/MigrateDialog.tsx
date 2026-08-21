// MigrateDialog —— 卡的跨工作流迁移对话框。
//
// 职责边界：只负责从 /api/flows 收集目标流与显式落点列并提交迁移，
// 不做落点合法性判断——工作流版本、gate 与在飞门禁都由账本负责。
import { useEffect, useState } from 'react'
import { fetchFlows, migrateCard } from '../../api/ledger'
import type { FlowsResp } from '../../api/ledger'
import { errorMessage } from '../lib/format'

export interface MigrateDialogProps {
  open: boolean
  cardId: string
  onClose: () => void
  onMigrated: () => void
}

export function MigrateDialog({ open, cardId, onClose, onMigrated }: MigrateDialogProps) {
  const [flows, setFlows] = useState<FlowsResp | null>(null)
  const [workflow, setWorkflow] = useState('')
  const [status, setStatus] = useState('')
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setFlows(null)
    setWorkflow('')
    setStatus('')
    setBusy(false)
    setLoading(true)
    setError('')
    void fetchFlows()
      .then((result) => { if (!cancelled) setFlows(result) })
      .catch((err: unknown) => { if (!cancelled) setError(errorMessage(err)) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [open, cardId])

  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  const selectedFlow = flows?.workflows.find((item) => item.name === workflow)
  const states = selectedFlow?.def.states ?? []

  const submit = async () => {
    if (!workflow || !status) return
    setBusy(true)
    setError('')
    try {
      await migrateCard(cardId, { workflow, status })
      onMigrated()
    } catch (err) {
      // ApiError.message 是服务端原文，尤其要把 409 的在飞原因原样交给人。
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4" role="presentation" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label="迁移工作项"
        className="w-full max-w-md rounded-lg border bg-background p-4 shadow-lg"
        onClick={(event) => event.stopPropagation()}
      >
        <h2 className="text-base font-semibold">迁移工作项</h2>
        <p className="mt-1 text-xs text-muted-foreground">{cardId}：请选择目标工作流和落点列。</p>
        <div className="mt-4 space-y-3">
          <div>
            <label className="block text-xs text-muted-foreground">目标工作流</label>
            <select
              aria-label="目标工作流"
              value={workflow}
              disabled={loading || busy}
              onChange={(event) => {
                setWorkflow(event.target.value)
                // 切流后清空列值，禁止把同名列当成自动映射（基准语义 1）。
                setStatus('')
              }}
              className="mt-1 w-full rounded border bg-background px-2 py-1.5 text-sm disabled:opacity-50"
            >
              <option value="">选择目标工作流</option>
              {(flows?.workflows ?? []).map((item) => <option key={item.name} value={item.name}>{item.name} v{item.version}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-muted-foreground">落点列</label>
            <select
              aria-label="落点列"
              value={status}
              disabled={!workflow || loading || busy}
              onChange={(event) => setStatus(event.target.value)}
              className="mt-1 w-full rounded border bg-background px-2 py-1.5 text-sm disabled:opacity-50"
            >
              <option value="">选择落点列</option>
              {states.map((state) => <option key={state} value={state}>{state}</option>)}
            </select>
          </div>
        </div>
        {error && <p role="alert" className="mt-3 whitespace-pre-wrap rounded border border-destructive/40 bg-destructive/5 p-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded border px-3 py-1.5 text-sm" onClick={onClose} disabled={busy}>取消</button>
          <button
            type="button"
            className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
            disabled={busy || loading || !workflow || !status}
            onClick={() => void submit()}
          >{busy ? '提交中…' : '确认迁移'}</button>
        </div>
      </div>
    </div>
  )
}
