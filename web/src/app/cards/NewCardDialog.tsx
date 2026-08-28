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
import { useEffect, useRef, useState } from 'react'
import { createCard } from '../../api/ledger'
import { launchCoordinator } from '../../api/scheduling'
import { ApiError, fetchProjectBranches, fetchProjects } from '../../api/client'
import type { ProjectBranchesResp } from '../../api/types'
import { errorMessage } from '../lib/format'

// parseTitles 把多行标题文本解析成建卡清单：一行一张。
//
// 容错规则（spec §3.3）：逐行 trim、跳过空行、去掉行首的 `-` `*` `•` 或
// `1.` `1)` `1、` 式列表前缀——从聊天记录或文档粘一份清单进来就能直接用。
// 数字前缀允许零空格（「1.标题」也认）；`-`/`*` 后面必须跟空白才算前缀，
// 否则负数样标题（-40ms）会被剥得缺胳膊少腿。
export function parseTitles(raw: string): string[] {
  return raw
    .split('\n')
    .map((line) => line.replace(/^\s*(?:[-*•]\s+|\d+[.)、]\s*)/, '').trim())
    .filter((line) => line !== '')
}

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

// branchOptionsOf 与 NewWorktreeDialog.baseOptions 同款：本地分支表并上服务端
// 推导的 default。default 可能是 origin/main 这种远端跟踪分支名，不在本地分支
// 表里——不并进来，引用它的地方会落空，看起来像「没选基线」而实际有值且合法。
function branchOptionsOf(data: ProjectBranchesResp): string[] {
  const names = data.branches.map((branch) => branch.name)
  if (data.default !== '' && !names.includes(data.default)) return [data.default, ...names]
  return names
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
  const [workflow, setWorkflow] = useState(workflows[0] ?? '')
  const [priority, setPriority] = useState('中')
  const [baseBranch, setBaseBranch] = useState('')
  const [busy, setBusy] = useState(false)
  const [coordinate, setCoordinate] = useState(false)
  // picked 是用户手动动过的选择；null = 还没动过，让位给三档预选派生
  const [picked, setPicked] = useState<string | null>(null)
  const [registered, setRegistered] = useState<string[]>([])
  const [projectsError, setProjectsError] = useState('')
  const branchSeq = useRef(0)
  const [branchOptions, setBranchOptions] = useState<string[]>([])
  const [branchDefault, setBranchDefault] = useState('')
  const [branchHint, setBranchHint] = useState('')
  const [result, setResult] = useState<{
    succeeded: { title: string; id: string; coordinator?: 'launched' | 'failed'; coordinatorError?: string }[]
    failed: { title: string; reason: string }[]
  } | null>(null)

  useEffect(() => {
    // 流列表异步到齐或刷新时，只保留仍存在的手选值；空列表保持空值，交给账本解析。
    setWorkflow((current) => workflows.includes(current) ? current : (workflows[0] ?? ''))
  }, [workflows])

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
      setPicked(null); setTitle(''); setBaseBranch(''); setResult(null); setCoordinate(false)
      setBranchOptions([]); setBranchDefault(''); setBranchHint('')
      branchSeq.current++
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

  // 分支列表跟随有效项目重取：预选（含打开时的第一档）也要取，不只手动切换才取。
  // seq 防竞态：连切两个项目时，慢到的旧响应不得覆盖新项目的结果。
  // 成功时把服务端推导的默认基线显式填入输入框——它可能是不在本地分支表里的
  // 远端跟踪名，看不见就等于没选（spec §6 判据「出现在选项中且被预选」）。
  // 切项目在此清空已填分支：旧项目的分支名在新项目下无意义。
  useEffect(() => {
    if (!open || projectValue === '') return
    setBaseBranch('')
    setBranchOptions([]); setBranchDefault(''); setBranchHint('')
    const seq = ++branchSeq.current
    fetchProjectBranches(projectValue)
      .then((resp) => {
        if (seq !== branchSeq.current) return
        setBranchOptions(branchOptionsOf(resp))
        setBranchDefault(resp.default)
        setBaseBranch(resp.default)
      })
      .catch((err) => {
        if (seq !== branchSeq.current) return
        setBranchOptions([]); setBranchDefault('')
        // 未登记（404）是合法场景：给未登记项目建卡照常能完成，退回手输并说明；
        // 其余失败同样退回手输但透出原文——缩略成「加载失败」会把唯一线索弄丢
        setBranchHint(
          err instanceof ApiError && err.status === 404
            ? `项目 ${projectValue} 未登记位置，分支需手输`
            : errorMessage(err),
        )
      })
  }, [open, projectValue])

  if (!open) return null

  const titles = parseTitles(title)

  const submit = async () => {
    if (titles.length === 0 || projectValue === '') return
    setBusy(true)
    setResult(null)
    console.info('card-create.start', { count: titles.length, coordinate })
    const succeeded: { title: string; id: string; coordinator?: 'launched' | 'failed'; coordinatorError?: string }[] = []
    const failed: { title: string; reason: string }[] = []
    // 串行不并发：并发会让卡号顺序与用户写下顺序对不上，而 B 号顺序是人读
    // 账本时的隐含线索（spec §5）。逐条提交逐条记账，已成功的不回滚。
    for (const one of titles) {
      try {
        const created = await createCard({
          title: one,
          project: projectValue,
          priority,
          ...(workflow ? { workflow } : {}),
          ...(parent ? { parent } : {}),
          ...(baseBranch.trim() ? { base_branch: baseBranch.trim() } : {}),
        })
        if (!coordinate) {
          console.info('card-create.done', { card: created.id, coordinator: 'skipped' })
          succeeded.push({ title: one, id: created.id })
          continue
        }
        try {
          await launchCoordinator(created.id, 'card_create')
          console.info('card-create.done', { card: created.id, coordinator: 'launched' })
          succeeded.push({ title: one, id: created.id, coordinator: 'launched' })
        } catch (cause) {
          const reason = errorMessage(cause)
          console.warn('card-create.coordinator.error', { card: created.id, cause })
          succeeded.push({ title: one, id: created.id, coordinator: 'failed', coordinatorError: reason })
        }
      } catch (err) {
        console.error('card-create.error', { title: one, cause: err })
        failed.push({ title: one, reason: errorMessage(err) })
      }
    }
    setBusy(false)
    if (succeeded.length > 0) saveLastProject(projectValue)
    const coordinatorFailures = succeeded.some((row) => row.coordinator === 'failed')
    if (failed.length > 0 || coordinatorFailures) {
      // 有失败就留在原地展示结果：成功列卡号、失败列原因；用户改掉失败那几行
      // 直接再点一次，不用从头重来（spec 故事 7）
      setResult({ succeeded, failed })
      return
    }
    setTitle('')
    setBaseBranch('')
    onCreated(succeeded[succeeded.length - 1].id)
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
        <textarea
          id="new-card-title" rows={3} className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          placeholder="一行一张卡；粘贴多行时 - / * / 1. 前缀和空行会被忽略"
          value={title} onChange={(e) => setTitle(e.target.value)} autoFocus
        />
        <p className="mt-1 text-xs text-muted-foreground">
          {titles.length > 0 ? `将建 ${titles.length} 张卡${titles.length > 1 ? '，共用下方字段' : ''}` : '一行一张卡'}
        </p>
        <div className="mt-3 grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-workflow">工作流</label>
            <select
              id="new-card-workflow" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={workflow} onChange={(e) => setWorkflow(e.target.value)}
            >
              {workflows.length === 0
                ? <option value="">由账本按实际工作流解析</option>
                : workflows.map((name) => <option key={name} value={name}>{name}</option>)}
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
          list="new-card-base-options"
          placeholder={parent
            ? '留空 = 继承父卡'
            : branchDefault !== '' ? `留空 = ${branchDefault}` : '留空 = 项目主线'}
          value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)}
        />
        <datalist id="new-card-base-options">
          {branchOptions.map((name) => <option key={name} value={name} />)}
        </datalist>
        {branchHint !== '' && <p className="mt-1 text-xs text-muted-foreground">{branchHint}</p>}
        <p className="mt-1 text-xs text-muted-foreground">
          这张卡的合并目标。<b>建卡后不可改</b>——已派出去的任务会按它工作。
        </p>
        <label className="mt-3 flex items-start gap-2 text-xs text-muted-foreground">
          <input type="checkbox" aria-label="创建后拉起协调者并绑定（开卡即绑）" checked={coordinate} onChange={(event) => setCoordinate(event.target.checked)} />
          <span><span className="font-medium text-foreground">创建后拉起协调者并绑定（开卡即绑）</span><br />先创建工作项，再逐张调用协调者拉起；拉起失败不会回滚已建卡。</span>
        </label>
        {result !== null && (
          <div className="mt-3 space-y-1 rounded border p-2 text-xs">
            {result.succeeded.map((row, i) => (
              <p key={`${row.id}-${i}`}>已建 <span className="font-mono">{row.id}</span> · {row.title}{row.coordinator === 'failed' && <span className="text-destructive"> · 协调者失败：{row.coordinatorError}</span>}</p>
            ))}
            {result.failed.map((row, i) => (
              <p key={`${row.title}-${i}`} className="text-destructive">「{row.title}」未建成：{row.reason}</p>
            ))}
          </div>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded border px-3 py-1.5 text-sm" onClick={onClose}>取消</button>
          <button
            type="button" className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
            disabled={busy || titles.length === 0 || projectValue === ''}
            onClick={() => void submit()}
          >建卡</button>
        </div>
      </div>
    </div>
  )
}
