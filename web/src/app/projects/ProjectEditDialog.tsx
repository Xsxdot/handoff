// ProjectEditDialog —— 项目位置的编辑弹层。
//
// 入口：项目树机器行右键菜单「编辑」。弹层标题是项目名，内容按 location 分 tab
// （每台机器一栏，""=本机显示「本机」），每栏可改引用名与路径，路径用普通粘贴
// 输入框——浏览器里没有 Finder，形态与登记向导第二步完全一致（spec §9）。
// 机器维度不可编辑：换机器 = 注销后重新添加，只展示机器名并给出理由文案。
//
// 底部「本次改动」只列真的变了的字段、每条带后果说明（改名动的是按名寻址的
// 入口，project_id 不变；改路径则已登记工作树仍指向旧目录）；无改动时保存禁用。
//
// 保存：对每个有改动的 location 各发一次 patchProject——本机（""）不带 machine
// 参数、远程带 machine=该机器名。Promise.allSettled 逐位置收口，永不整体抛错、
// 永不回滚成功项（跨机事务不存在）；结果逐位置列出，失败项透传 agentd 原文并
// 可重试。有任一成功即调 onDone 让父级刷新项目树。
import { useEffect, useMemo, useState } from 'react'
import { X } from 'lucide-react'
import { patchProject } from '../../api/client'
import type { PatchProjectReq, ProjectLocationNode, ProjectNode } from '../../api/types'
import { Button } from '@/components/ui/button'
import { errorMessage } from '../lib/format'
import { cn } from '@/lib/utils'

export interface ProjectEditDialogProps {
  open: boolean
  project: ProjectNode | null   // 被编辑的项目（右键时所在的项目）
  onClose: () => void
  onDone: () => void            // 保存有成功项后调用，父级用它刷新项目树
}

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(machine: string): string {
  return machine === '' ? '本机' : machine
}

// Change 是一次「真的变了」的字段改动。逐字段建模而不是逐 location：一个位置
// 可以同时改引用名与路径，它们各自独立列出、独立说明后果。
interface Change {
  machine: string
  field: 'name' | 'path'
  old: string
  new: string
}

// changesFor 遍历所有 location，只收集与登记值不一致的字段。
function changesFor(project: ProjectNode, edits: Record<string, { name: string; path: string }>): Change[] {
  const list: Change[] = []
  for (const loc of project.locations) {
    const e = edits[loc.machine]
    if (!e) continue
    if (e.name !== loc.name) list.push({ machine: loc.machine, field: 'name', old: loc.name, new: e.name })
    if (e.path !== loc.path) list.push({ machine: loc.machine, field: 'path', old: loc.path, new: e.path })
  }
  return list
}

// consequence 给一条改动配人话后果。改名会动「按名字寻址」的入口（注销、CLI）
// 但 project_id 不变，任务与工作树不失联；改路径则已登记工作树仍指向旧目录。
function consequence(project: ProjectNode, c: Change): string {
  if (c.field === 'name') {
    return `${machineLabel(c.machine)}位置的引用名 ${c.old} → ${c.new}。project_id 不变，任务与工作树不失联；但按旧名寻址的入口（注销、CLI）将指向新名`
  }
  const loc = project.locations.find((l) => l.machine === c.machine)
  const n = loc?.workspaces.length ?? 0
  return `该位置已登记 ${n} 个工作树，它们仍指向旧目录，需要在保存后逐个确认；新的派发将使用新路径`
}

// Outcome 是单台的保存结果；error 透传 agentd 原文。
interface Outcome {
  machine: string
  ok: boolean
  error: string
}

// INPUT_CLASS 与登记向导第二步的输入框样式保持一字不差，界面词汇统一。
const INPUT_CLASS = 'h-8 rounded-md border border-input bg-background px-2.5 text-xs shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function ProjectEditDialog({ open, project, onClose, onDone }: ProjectEditDialogProps) {
  const [tab, setTab] = useState('')
  const [edits, setEdits] = useState<Record<string, { name: string; path: string }>>({})
  const [outcomes, setOutcomes] = useState<Outcome[] | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // 每次重新打开都重置到初始状态（对话框复用，不能带着上次的输入与结果）。
  useEffect(() => {
    if (!open || !project) return
    setTab(project.locations[0]?.machine ?? '')
    setEdits(Object.fromEntries(project.locations.map((l) => [l.machine, { name: l.name, path: l.path }])))
    setOutcomes(null)
    setSubmitting(false)
  }, [open, project])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const changes = useMemo<Change[]>(() => (project ? changesFor(project, edits) : []), [project, edits])

  if (!open || !project) return null

  const loc = project.locations.find((l) => l.machine === tab) ?? project.locations[0]

  // buildReq 组装一次 PATCH 的请求体：只带真改了的字段。
  const buildReq = (target: ProjectLocationNode): PatchProjectReq => {
    const e = edits[target.machine]
    const req: PatchProjectReq = {}
    if (e.name !== target.name) req.new_name = e.name
    if (e.path !== target.path) req.path = e.path
    return req
  }

  const save = async () => {
    if (!project) return
    setSubmitting(true)
    const targets = project.locations.filter((l) => {
      const e = edits[l.machine]
      return e && (e.name !== l.name || e.path !== l.path)
    })
    const settled = await Promise.allSettled(
      targets.map((t) => patchProject(t.name, buildReq(t), t.machine === '' ? undefined : t.machine)),
    )
    const results: Outcome[] = settled.map((s, i) => {
      const t = targets[i]
      if (s.status === 'fulfilled') return { machine: t.machine, ok: true, error: '' }
      return { machine: t.machine, ok: false, error: errorMessage(s.reason) }
    })
    setOutcomes(results)
    setSubmitting(false)
    if (results.some((o) => o.ok)) onDone()
  }

  const retry = async (machine: string) => {
    if (!project) return
    const t = project.locations.find((l) => l.machine === machine)
    if (!t) return
    try {
      await patchProject(t.name, buildReq(t), t.machine === '' ? undefined : t.machine)
      setOutcomes((prev) => (prev ?? []).map((o) => (o.machine === machine ? { machine, ok: true, error: '' } : o)))
      onDone()
    } catch (err) {
      setOutcomes((prev) =>
        (prev ?? []).map((o) => (o.machine === machine ? { machine, ok: false, error: errorMessage(err) } : o)),
      )
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="edit-title"
        className="w-full max-w-lg rounded-lg border bg-background p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <h2 id="edit-title" className="text-base font-semibold">
            {project.name}
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

        {outcomes ? (
          /* 保存后的结果视图：逐位置列出，成功不回滚、失败透传原文可重试 */
          <div className="mt-4 flex flex-col gap-2">
            <p className="text-sm text-muted-foreground">保存结果（逐位置）：</p>
            {outcomes.map((o) => (
              <div key={o.machine} className="flex items-center gap-2 rounded-md border p-3 text-sm">
                <span className="font-medium">{machineLabel(o.machine)}</span>
                {o.ok ? (
                  <span className="text-emerald-600">已保存 ✓</span>
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
        ) : (
          <>
            {/* location 分 tab：每台机器一栏 */}
            <div role="tablist" className="mt-4 flex gap-1 border-b">
              {project.locations.map((l) => (
                <button
                  key={l.machine}
                  type="button"
                  role="tab"
                  aria-selected={tab === l.machine}
                  onClick={() => setTab(l.machine)}
                  className={cn(
                    '-mb-px rounded-t-md border-b-2 px-3 py-1.5 text-sm',
                    tab === l.machine
                      ? 'border-primary font-medium text-foreground'
                      : 'border-transparent text-muted-foreground',
                  )}
                >
                  {machineLabel(l.machine)}
                </button>
              ))}
            </div>

            {loc && (
              <div className="mt-3 flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">{machineLabel(loc.machine)}</p>
                {/* 机器维度不可编辑：换机器 = 注销后重新添加 */}
                <p className="text-[11px] text-muted-foreground">机器维度不可编辑：换机器 = 注销后重新添加</p>
                <input
                  value={edits[loc.machine]?.name ?? ''}
                  onChange={(e) =>
                    setEdits((p) => ({
                      ...p,
                      [loc.machine]: { name: e.target.value, path: p[loc.machine]?.path ?? loc.path },
                    }))
                  }
                  aria-label={`${machineLabel(loc.machine)} 引用名`}
                  placeholder="引用名"
                  disabled={submitting}
                  className={INPUT_CLASS}
                />
                <input
                  value={edits[loc.machine]?.path ?? ''}
                  onChange={(e) =>
                    setEdits((p) => ({
                      ...p,
                      [loc.machine]: { name: p[loc.machine]?.name ?? loc.name, path: e.target.value },
                    }))
                  }
                  aria-label={`${machineLabel(loc.machine)} 路径`}
                  placeholder="粘贴本机已有目录路径"
                  disabled={submitting}
                  className={INPUT_CLASS}
                />
              </div>
            )}

            {/* 「本次改动」只列真的变了的字段，每条带后果说明 */}
            <div className="mt-4">
              <p className="text-sm text-muted-foreground">本次改动</p>
              {changes.length === 0 ? (
                <p className="mt-1 text-xs text-muted-foreground">没有改动</p>
              ) : (
                <ul className="mt-1 flex flex-col gap-1">
                  {changes.map((c) => (
                    <li
                      key={`${c.machine}:${c.field}`}
                      className="rounded-md border p-2 text-xs text-muted-foreground"
                    >
                      {consequence(project, c)}
                    </li>
                  ))}
                </ul>
              )}
            </div>

            <div className="mt-4 flex justify-end gap-2">
              <Button variant="outline" onClick={onClose}>
                取消
              </Button>
              <Button onClick={() => void save()} disabled={changes.length === 0 || submitting}>
                {submitting ? '保存中…' : '保存'}
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
