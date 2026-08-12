// AddProjectWizard —— 项目登记两步向导：选位置 → 配来源 → 逐位置结果。
//
// 两步照 spec §5：
//   1. 选位置：本机 checkbox + 远程机器单选（ADR-0008：至多一台远程）；不可达的
//      远程机器可选，但旁边提示「登记可能失败」——要不要试是用户的决定，不替他挡
//   2. 配来源：每个选中位置填 Git 地址与可选目录。本机位置只支持粘贴已有目录路径，
//      没有 Finder 选择器（浏览器里没有 Finder，spec §9）；clone 路径留空时由该
//      机器 clone 到它自己的 repo_root，**不硬编码 ~/.handoff/<name>**（原型标的
//      默认路径与 B62 实际的 repo_root/<name> 不一致，显示可能错的路径比不显示更糟）
//
// 提交：逐位置 registerAll（Promise.allSettled 逐位置收口，spec §5.2），任一成功
// 即调 onDone 让父级 refresh 项目树；结果面板逐位置显示，失败的可「重试」。
import { useEffect, useMemo, useState } from 'react'
import { X } from 'lucide-react'
import type { Machine } from '../../api/types'
import { Button } from '@/components/ui/button'
import { registerAll, type LocationChoice, type RegisterOutcome } from './register'

export interface AddProjectWizardProps {
  open: boolean
  machines: Machine[]
  onClose: () => void
  onDone: () => void
}

type Step = 'locations' | 'sources' | 'results'

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(name: string): string {
  return name === '' ? '本机' : name
}

export function AddProjectWizard({ open, machines, onClose, onDone }: AddProjectWizardProps) {
  const [step, setStep] = useState<Step>('locations')
  const [localSelected, setLocalSelected] = useState(false)
  const [remoteMachine, setRemoteMachine] = useState<string | null>(null)
  const [sources, setSources] = useState<Record<string, { gitUrl: string; path: string }>>({})
  const [outcomes, setOutcomes] = useState<RegisterOutcome[] | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // 每次重新打开都重置到第一步（对话框复用，不能带着上次的选择/结果）。
  useEffect(() => {
    if (open) {
      setStep('locations')
      setLocalSelected(false)
      setRemoteMachine(null)
      setSources({})
      setOutcomes(null)
      setSubmitting(false)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const remoteMachines = useMemo(() => machines.filter((m) => m.name !== ''), [machines])
  const locations = useMemo(() => {
    const list: string[] = []
    if (localSelected) list.push('')
    if (remoteMachine) list.push(remoteMachine)
    return list
  }, [localSelected, remoteMachine])

  const goSources = () => {
    setSources((prev) => {
      const next = { ...prev }
      for (const m of locations) if (!next[m]) next[m] = { gitUrl: '', path: '' }
      return next
    })
    setStep('sources')
  }

  const canSubmit = locations.every((m) => (sources[m]?.gitUrl ?? '').trim() !== '')

  const submit = async () => {
    setSubmitting(true)
    const choices: LocationChoice[] = locations.map((m) => ({
      machine: m,
      gitUrl: sources[m]?.gitUrl ?? '',
      path: sources[m]?.path ?? '',
    }))
    const result = await registerAll(choices)
    setOutcomes(result)
    setSubmitting(false)
    setStep('results')
    if (result.some((o) => o.ok)) onDone()
  }

  const retry = async (machine: string) => {
    const choice: LocationChoice = {
      machine,
      gitUrl: sources[machine]?.gitUrl ?? '',
      path: sources[machine]?.path ?? '',
    }
    const result = await registerAll([choice])
    setOutcomes((prev) => (prev ?? []).map((o) => (o.machine === machine ? result[0] : o)))
    if (result[0].ok) onDone()
  }

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="wizard-title"
        className="w-full max-w-lg rounded-lg border bg-background p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <h2 id="wizard-title" className="text-base font-semibold">
            添加项目
          </h2>
          <button
            type="button"
            aria-label="关闭"
            onClick={onClose}
            className="rounded p-1 text-muted-foreground hover:text-foreground"
          >
            <X className="size-4" />
          </button>
        </div>

        {step === 'locations' && (
          <div className="mt-4 flex flex-col gap-3">
            <p className="text-sm text-muted-foreground">选择登记到哪些位置（本机 + 至多一台远程机器）。</p>
            <label className="flex cursor-pointer items-center gap-2 rounded-md border p-3 text-sm hover:bg-accent/40">
              <input type="checkbox" checked={localSelected} onChange={(e) => setLocalSelected(e.target.checked)} />
              <span className="font-medium">本机</span>
            </label>
            {remoteMachines.map((m) => (
              <label key={m.name} className="flex cursor-pointer items-center gap-2 rounded-md border p-3 text-sm hover:bg-accent/40">
                <input
                  type="radio"
                  name="remote-machine"
                  checked={remoteMachine === m.name}
                  onChange={() => setRemoteMachine(m.name)}
                />
                <span className="font-medium">{m.name}</span>
                {!m.reachable && <span className="ml-auto text-xs text-amber-600">（登记可能失败）</span>}
              </label>
            ))}
            <div className="mt-2 flex justify-end gap-2">
              <Button variant="outline" onClick={onClose}>
                取消
              </Button>
              <Button onClick={goSources} disabled={locations.length === 0}>
                下一步
              </Button>
            </div>
          </div>
        )}

        {step === 'sources' && (
          <div className="mt-4 flex flex-col gap-3">
            <p className="text-sm text-muted-foreground">为每个位置填 Git 仓库地址与可选目录。</p>
            {locations.map((m) => (
              <div key={m} className="flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">{machineLabel(m)}</p>
                <input
                  value={sources[m]?.gitUrl ?? ''}
                  onChange={(e) => setSources((p) => ({ ...p, [m]: { ...p[m], gitUrl: e.target.value } }))}
                  placeholder="Git 仓库地址"
                  className="h-8 rounded-md border border-input bg-background px-2.5 text-xs shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
                <input
                  value={sources[m]?.path ?? ''}
                  onChange={(e) => setSources((p) => ({ ...p, [m]: { ...p[m], path: e.target.value } }))}
                  placeholder={m === '' ? '粘贴本机已有目录路径' : '粘贴路径（留空则由该机器 clone 到它自己的 repo_root）'}
                  className="h-8 rounded-md border border-input bg-background px-2.5 text-xs shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
                />
                {m !== '' && (
                  <p className="text-[11px] text-muted-foreground">路径留空时由该机器 clone 到它自己的 repo_root</p>
                )}
              </div>
            ))}
            <div className="mt-2 flex justify-end gap-2">
              <Button variant="outline" onClick={() => setStep('locations')}>
                上一步
              </Button>
              <Button onClick={() => void submit()} disabled={!canSubmit || submitting}>
                {submitting ? '提交中…' : '提交'}
              </Button>
            </div>
          </div>
        )}

        {step === 'results' && outcomes && (
          <div className="mt-4 flex flex-col gap-2">
            <p className="text-sm text-muted-foreground">登记结果（逐位置）：</p>
            {outcomes.map((o) => (
              <div key={o.machine} className="flex items-center gap-2 rounded-md border p-3 text-sm">
                <span className="font-medium">{machineLabel(o.machine)}</span>
                {o.ok ? (
                  <span className="text-emerald-600">已登记</span>
                ) : (
                  <>
                    <span className="min-w-0 flex-1 break-words text-destructive">{o.error}</span>
                    <Button variant="outline" size="sm" onClick={() => void retry(o.machine)}>
                      重试
                    </Button>
                  </>
                )}
              </div>
            ))}
            <div className="mt-2 flex justify-end">
              <Button onClick={onClose}>完成</Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
