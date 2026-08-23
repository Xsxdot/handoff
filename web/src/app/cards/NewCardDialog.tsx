// NewCardDialog —— 建卡对话框（单张是批量的 n=1 特例）。
//
// 职责：
//   - 收集建卡字段：项目 / 标题（多行=批量）/ 工作流 / 优先级 / 父卡 / 基线分支
//   - 项目下拉自己渲染：候选 = GET /api/projects 登记名 ∪ 调用方传入的卡上
//     历史值（cardProjects）；预选三档全部可见——筛选值 > 上次使用
//     (localStorage) > 不预选。静默选错项目的成因是不可见，不是没得选
//   - 选定项目后取分支列表做「下拉+手输」（Task 4 接 datalist）
//   - 标题多行批量提交（Task 5）
//
// 边界：
//   - 不管建完之后干什么——打开抽屉、刷新列表都由调用方决定（onCreated）
//   - 项目与基线分支**建卡后不可改**：表单上写明，而不是让人建完才发现改不了
import { useEffect, useState } from 'react'
import { createCard } from '../../api/ledger'
import { fetchProjects } from '../../api/client'
import { errorMessage } from '../lib/format'

const LAST_PROJECT_KEY = 'handoff.cards.lastProject'

function loadLastProject(): string {
  try {
    return localStorage.getItem(LAST_PROJECT_KEY) ?? ''
  } catch {
    return ''
  }
}

function saveLastProject(name: string): void {
  try {
    localStorage.setItem(LAST_PROJECT_KEY, name)
  } catch (err) {
    console.warn('[NewCardDialog] 上次建卡项目写入失败，本次只在内存生效', err)
  }
}

export function NewCardDialog({
  open, project, cardProjects, workflows, parent, onClose, onCreated,
}: {
  open: boolean
  // 顶部筛选当前值：「全部项目」= 空串。只是第一档预选建议，提交值以下拉为准；
  // 约定它来自 CardsPage 的筛选器（候选恒含卡上历史值 ⊆ 本组件并集），
  // 因此预选值总能落在某个 option 上、显示得出来。
  project: string
  // 现有卡上出现过的 project 值。卡的 project 是自由字符串，只认登记表会让
  // 历史卡所属的项目从下拉里消失，所以并集而不是取代。
  cardProjects: string[]
  workflows: string[]
  parent?: string
  onClose: () => void
  onCreated: (id: string) => void
}) {
  const [title, setTitle] = useState('')
  const [workflow, setWorkflow] = useState(workflows[0] ?? 'feature')
  const [priority, setPriority] = useState('中')
  const [baseBranch, setBaseBranch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // picked 是用户手动动过的选择；null = 还没动过，让位给三档预选派生
  const [picked, setPicked] = useState<string | null>(null)
  const [registered, setRegistered] = useState<string[]>([])
  const [projectsError, setProjectsError] = useState('')

  useEffect(() => {
    if (!open) return
    let alive = true
    fetchProjects()
      .then((locs) => { if (alive) setRegistered(locs.map((loc) => loc.name)) })
      .catch((err) => {
        if (!alive) return
        // 登记表读不到只降级候选来源（卡上历史值仍在），不弹错堵门
        setRegistered([])
        setProjectsError(errorMessage(err))
      })
    return () => { alive = false }
  }, [open])

  useEffect(() => {
    if (!open) {
      // 关闭即复位：下次打开重新按三档预选，不带上一轮的残值
      setPicked(null); setTitle(''); setBaseBranch(''); setError('')
    }
  }, [open])

  const candidates = [...new Set([...registered, ...cardProjects])].sort()

  // 预选三档（spec §3.1）：筛选值 > 上次使用 > 空。派生而非 effect 写 state——
  // 候选异步到齐的时序不会把中间态钉死进 state；用户一旦动过下拉即让位。
  let projectValue = picked ?? ''
  if (picked === null && project !== '') projectValue = project
  if (picked === null && project === '') {
    const last = loadLastProject()
    if (last !== '' && candidates.includes(last)) projectValue = last
  }

  if (!open) return null

  const submit = async () => {
    const trimmed = title.trim()
    if (!trimmed || projectValue === '') return
    setBusy(true)
    setError('')
    try {
      const result = await createCard({
        title: trimmed, project: projectValue, workflow, priority,
        ...(parent ? { parent } : {}),
        ...(baseBranch.trim() ? { base_branch: baseBranch.trim() } : {}),
      })
      setTitle('')
      setBaseBranch('')
      saveLastProject(projectValue)
      onCreated(result.id)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4">
      <div className="w-full max-w-md rounded-lg border bg-background p-4 shadow-lg">
        <h2 className="text-base font-semibold">新建工作项</h2>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-project">项目</label>
        <select
          id="new-card-project" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          value={projectValue} onChange={(e) => setPicked(e.target.value)}
        >
          <option value="">请选择项目</option>
          {candidates.map((name) => <option key={name} value={name}>{name}</option>)}
        </select>
        {projectValue === '' && <p className="mt-1 text-xs text-amber-700">请先选择项目</p>}
        {projectsError !== '' && (
          <p className="mt-1 text-xs text-muted-foreground">登记位置读取失败，候选只剩现有卡上的项目：{projectsError}</p>
        )}
        <p className="mt-1 text-xs text-muted-foreground">建卡后不可改</p>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-title">标题</label>
        <input
          id="new-card-title" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          value={title} onChange={(e) => setTitle(e.target.value)} autoFocus
        />
        <div className="mt-3 grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-workflow">工作流</label>
            <select
              id="new-card-workflow" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={workflow} onChange={(e) => setWorkflow(e.target.value)}
            >
              {workflows.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-priority">优先级</label>
            <select
              id="new-card-priority" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={priority} onChange={(e) => setPriority(e.target.value)}
            >
              {['高', '中', '低'].map((level) => <option key={level} value={level}>{level}</option>)}
            </select>
          </div>
        </div>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-base">基线分支</label>
        <input
          id="new-card-base" className="mt-1 w-full rounded border px-2 py-1.5 font-mono text-sm"
          placeholder={parent ? '留空 = 继承父卡' : '留空 = 项目主线'}
          value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)}
        />
        <p className="mt-1 text-xs text-muted-foreground">
          这张卡的合并目标。<b>建卡后不可改</b>——已派出去的任务会按它工作。
        </p>
        {error !== '' && <p className="mt-3 rounded border border-amber-500/40 bg-amber-500/10 p-2 text-xs">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded border px-3 py-1.5 text-sm" onClick={onClose}>取消</button>
          <button
            type="button" className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
            disabled={busy || title.trim() === '' || projectValue === ''}
            onClick={() => void submit()}
          >建卡</button>
        </div>
      </div>
    </div>
  )
}
