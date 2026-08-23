// NewWorktreeDialog —— 在某台机器的某个项目位置上新建工作树（spec §2.2）。
//
// 入口：左栏机器行 hover 出现的 + 按钮，以及机器行右键菜单的「新建工作树」。
//
// 两种模式与 dispatch 的 --new-branch / --branch 对齐：
//   - 新建分支：填分支名 + 选基线（基线默认取服务端推导的 default）
//   - 检出已有分支：选一个尚未被别的工作树占用的分支
// 被占用的分支**列出但置灰**并标出占用者——直接不列会让人以为分支丢了。
//
// 边界：
//   - **只建树，不派任务**：本期 web 端没有 dispatch 表单，建完的树照旧走 CLI 派发
//   - 不自己拼落点完整路径：目录名的生成规则只有服务端一份，前端复刻必然分叉，
//     所以只回显服务端给的落点根
//   - 不刷新项目树、不选中新目录：那是 ProjectTree/Shell 的职责，这里只把
//     新工作树交回去（onCreated）
import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { createWorktree, fetchProjectBranches } from '../../api/client'
import type { ProjectBranchesResp, Workspace } from '../../api/types'
import { fetchCardDetail, fetchCards } from '../../api/ledger'
import type { CardView } from '../../api/ledger'
import { Button } from '@/components/ui/button'
import { errorMessage } from '../lib/format'

/** 新建工作树弹层的输入；登记名用于寻址，机器名用于选择目标位置。 */
export interface NewWorktreeDialogProps {
  open: boolean
  // projectName 是**登记名**（ProjectLocationNode.name）。跨机时它与 ProjectNode.name
  // 可能不同，接口按登记名寻址，传错会 404。
  projectName: string
  machine: string
  onClose: () => void
  onCreated: (ws: Workspace) => void
}

// INPUT_CLASS 与项目编辑弹层的输入框保持一字不差，界面词汇统一。
const INPUT_CLASS = 'h-8 w-full rounded-md border border-input bg-background px-2.5 text-xs shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring'

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(machine: string): string {
  return machine === '' ? '本机' : machine
}

// baseOptions 是基线下拉的选项：本地分支表，外加**服务端推导出的 default**。
//
// 为什么要额外并进 default：服务端的推导优先取 origin/HEAD，返回的是
// "origin/main" 这种远端跟踪分支名，它不在本地分支表里。不并进来，下拉的 value
// 就落不到任何 option 上，选择框显示为空白——用户看到的是「基线没选」，
// 而实际上它有值且完全合法（git worktree add 认远端跟踪分支作起点）。
function baseOptions(data: ProjectBranchesResp): string[] {
  const names = data.branches.map((b) => b.name)
  if (data.default !== '' && !names.includes(data.default)) return [data.default, ...names]
  return names
}

type CardOption = {
  card: CardView
  dispatched: boolean
  detailError: string
}

/** 加载目标机器的分支并提交一棵手工工作树。 */
export function NewWorktreeDialog({ open, projectName, machine, onClose, onCreated }: NewWorktreeDialogProps) {
  const [mode, setMode] = useState<'new_branch' | 'existing_branch'>('new_branch')
  const [branch, setBranch] = useState('')
  const [base, setBase] = useState('')
  const [existing, setExisting] = useState('')
  const [data, setData] = useState<ProjectBranchesResp | null>(null)
  const [loadError, setLoadError] = useState('')
  const [submitError, setSubmitError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [cardOptions, setCardOptions] = useState<CardOption[]>([])
  const [selectedCardIDs, setSelectedCardIDs] = useState<string[]>([])
  const [cardLoadError, setCardLoadError] = useState('')
  const [createdWorkspace, setCreatedWorkspace] = useState<Workspace | null>(null)
  // reloadKey 只为「重试」按钮服务：改它就重跑下面那个 effect
  const [reloadKey, setReloadKey] = useState(0)

  // 每次打开都重置：弹层是复用的同一个实例，不重置会把上一次的输入与报错带过来
  useEffect(() => {
    if (!open) return
    setMode('new_branch')
    setBranch('')
    setExisting('')
    setSubmitError('')
    setData(null)
    setLoadError('')
    setCardOptions([])
    setSelectedCardIDs([])
    setCardLoadError('')
    setCreatedWorkspace(null)
    let alive = true
    fetchProjectBranches(projectName, machine)
      .then((resp) => {
        if (!alive) return
        setData(resp)
        setBase(resp.default)
      })
      .catch((err) => {
        if (!alive) return
        // 原文透出：这里最常见的失败是「机器不可达」，缩略成「加载失败」
        // 就把唯一可行动的信息弄丢了
        setLoadError(errorMessage(err))
      })
    // alive 挡住「弹层已关但请求才回来」的 setState
    return () => { alive = false }
  }, [open, projectName, machine, reloadKey])

  // 卡列表是可选的账本增强：账本不可用时只告警，分支工作树仍可创建。
  useEffect(() => {
    if (!open) return
    let alive = true
    const loadCards = async () => {
      try {
        const response = await fetchCards(`project=${encodeURIComponent(projectName)}`)
        const options = await Promise.all((response.cards ?? []).map(async (card) => {
          try {
            const detail = await fetchCardDetail(card.id)
            const dispatched = (detail.events ?? []).some((event) => event.type === 'dispatched')
            return { card, dispatched, detailError: '' }
          } catch (err) {
            return { card, dispatched: false, detailError: errorMessage(err) }
          }
        }))
        if (!alive) return
        setCardOptions(options)
      } catch (err) {
        if (!alive) return
        setCardOptions([])
        setCardLoadError(errorMessage(err))
      }
    }
    void loadCards()
    return () => { alive = false }
  }, [open, projectName])

  if (!open) return null

  const free = (data?.branches ?? []).filter((b) => b.worktree === '')
  const canSubmit =
    !submitting && data !== null &&
    (mode === 'new_branch' ? branch.trim() !== '' && base !== '' : existing !== '')

  const submit = async () => {
    setSubmitting(true)
    setSubmitError('')
    try {
      const request = mode === 'new_branch'
        ? { mode, branch: branch.trim(), base } as const
        : { mode, branch: existing, base: '' } as const
      const cardIDs = selectedCardIDs.filter((id) => cardOptions.some((option) => option.card.id === id && !option.dispatched))
      const requestWithCards = cardIDs.length > 0 ? { ...request, card_ids: cardIDs } : request
      const ws = await createWorktree(
        projectName,
        requestWithCards,
        machine,
      )
      setCreatedWorkspace(ws)
    } catch (err) {
      setSubmitError(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div role="dialog" aria-label="新建工作树" className="w-[420px] rounded-lg border bg-background p-4 shadow-lg">
        <div className="mb-3 flex items-center">
          <h2 className="text-sm font-semibold">新建工作树</h2>
          <button type="button" aria-label="关闭" onClick={onClose} className="ml-auto text-muted-foreground hover:text-foreground">
            <X className="size-4" />
          </button>
        </div>
        <p className="mb-3 text-xs text-muted-foreground">
          项目 {projectName} · {machineLabel(machine)}
        </p>

        {createdWorkspace ? (
          <div className="space-y-3">
            <p className="text-sm font-medium">工作树已创建</p>
            <p className="break-all text-xs text-muted-foreground">{createdWorkspace.branch} · {createdWorkspace.path}</p>
            {createdWorkspace.card_results && createdWorkspace.card_results.length > 0 ? (
              <ul className="space-y-1 text-xs">
                {createdWorkspace.card_results.map((result) => (
                  <li key={result.id} className={result.ok ? 'text-emerald-700' : 'break-words text-destructive'}>
                    {result.id}：{result.ok ? '已挂接' : (result.error ?? '挂接失败')}
                  </li>
                ))}
              </ul>
            ) : <p className="text-xs text-muted-foreground">没有请求挂接卡</p>}
            <div className="flex justify-end">
              <Button size="sm" onClick={() => { onCreated(createdWorkspace); onClose() }}>完成</Button>
            </div>
          </div>
        ) : loadError !== '' ? (
          <div className="space-y-2">
            <p className="break-words text-xs text-destructive">{loadError}</p>
            <Button size="sm" variant="outline" onClick={() => setReloadKey((k) => k + 1)}>重试</Button>
          </div>
        ) : data === null ? (
          <p className="text-xs text-muted-foreground">正在读取分支…</p>
        ) : (
          <div className="space-y-3">
            {cardLoadError !== '' && <p className="break-words text-xs text-amber-700">账本候选加载失败：{cardLoadError}</p>}
            {cardOptions.length > 0 && (
              <fieldset className="space-y-1.5 rounded border p-2">
                <legend className="px-1 text-xs text-muted-foreground">关联卡（可选）</legend>
                {cardOptions.map((option) => (
                  <label key={option.card.id} className="flex items-start gap-2 text-xs">
                    <input
                      type="checkbox"
                      aria-label={`选择卡 ${option.card.id}`}
                      checked={selectedCardIDs.includes(option.card.id)}
                      disabled={option.dispatched}
                      onChange={(event) => setSelectedCardIDs((current) => event.target.checked
                        ? [...current, option.card.id]
                        : current.filter((id) => id !== option.card.id))}
                    />
                    <span className="min-w-0 flex-1 break-words">
                      <span className="font-mono">{option.card.id}</span> · {option.card.title}
                      {option.dispatched && <span className="ml-1 text-muted-foreground">已派发，基线已冻结</span>}
                      {!option.dispatched && option.detailError !== '' && <span className="ml-1 text-amber-700">详情不可用：{option.detailError}</span>}
                    </span>
                  </label>
                ))}
              </fieldset>
            )}
            <label className="flex items-center gap-2 text-xs">
              <input type="radio" aria-label="新建分支" checked={mode === 'new_branch'} onChange={() => setMode('new_branch')} />
              <span>新建分支</span>
            </label>
            {mode === 'new_branch' && (
              <div className="space-y-2 pl-5">
                <label className="block text-xs">
                  <span className="mb-1 block text-muted-foreground">分支名</span>
                  <input aria-label="分支名" className={INPUT_CLASS} value={branch} onChange={(e) => setBranch(e.target.value)} />
                </label>
                <label className="block text-xs">
                  <span className="mb-1 block text-muted-foreground">基线</span>
                  <select aria-label="基线" className={INPUT_CLASS} value={base} onChange={(e) => setBase(e.target.value)}>
                    {baseOptions(data).map((name) => (
                      <option key={name} value={name}>{name}</option>
                    ))}
                  </select>
                </label>
              </div>
            )}

            <label className="flex items-center gap-2 text-xs">
              <input type="radio" aria-label="检出已有分支" checked={mode === 'existing_branch'} onChange={() => setMode('existing_branch')} />
              <span>检出已有分支</span>
            </label>
            {mode === 'existing_branch' && (
              <div className="pl-5">
                <select
                  aria-label="已有分支"
                  className={INPUT_CLASS}
                  value={existing}
                  onChange={(e) => setExisting(e.target.value)}
                >
                  <option value="">请选择</option>
                  {data.branches.map((b) => (
                    <option key={b.name} value={b.name} disabled={b.worktree !== ''}>
                      {b.worktree !== '' ? `${b.name}（已被 ${b.worktree} 占用）` : b.name}
                    </option>
                  ))}
                </select>
                {free.length === 0 && (
                  <p className="mt-1 text-[11px] text-muted-foreground">所有分支都已被工作树检出，请改用「新建分支」</p>
                )}
              </div>
            )}

            <p className="text-[11px] text-muted-foreground">
              将建在 {data.worktree_root} 下，目录名按分支名生成
            </p>
            {submitError !== '' && <p className="break-words text-xs text-destructive">{submitError}</p>}
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>取消</Button>
              <Button size="sm" onClick={submit} disabled={!canSubmit}>{submitting ? '创建中…' : '创建'}</Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
