// NewSessionDialog —— 项目内新建会话对话框（B444 S1/S2；B358.9 起 owner 由服务端按解析人名缺省，前端不传）。
//
// 职责：项目只读（由入口带入）、标题可空（交服务端自动命名）、卡多选（只列本项目、未进任何会话、
// 未终态的卡）、驻场小队必选（预填唯一 role=coordinator 小队）。提交中提示「正在拉起驻场者…」
// （建会话同步跑驻场者首轮，可能较久）。建成后若有被拒的卡或驻场者首轮失败，转结果视图点名展示。
//
// 边界：不发建会话请求、不开 tab——onCreate 交 Shell；本组件只读小队快照（getSquads）。
// 错误原文由调用方透传（服务端 403 文案含 console_user，前端不二次加工）。
// 卡候选过滤只是体验层：服务端仍逐卡校验并以 rejected_cards 点名（plan Q7）。
import { useEffect, useRef, useState } from 'react'
import type { SessionCreateResp, SessionSummary } from '../../api/rooms'
import { getSquads, type SquadView } from '../../api/scheduling'
import { errorMessage } from '../lib/format'
import { pullableCards, type PullableCard } from './sessionModel'

/** NewSessionInput 对话框交给 Shell 的建会话输入（project 由 Shell 持有，不在此重复）。 */
export interface NewSessionInput { title: string; cards: string[]; squad: string }

type SquadsState = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; squads: SquadView[] }

export function NewSessionDialog({ open, project, cards, sessions, busy, error, configured, result, onCancel, onCreate, onDone }: {
  open: boolean
  project: string
  cards: PullableCard[]
  sessions: Pick<SessionSummary, 'cards'>[]
  busy: boolean
  error: string
  configured: boolean
  /** 建成但有被拒卡或驻场者失败时由 Shell 回填，对话框转结果视图；null = 表单态。 */
  result: SessionCreateResp | null
  onCancel: () => void
  onCreate: (input: NewSessionInput) => void
  onDone: () => void
}) {
  const [title, setTitle] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const [squad, setSquad] = useState('')
  const [squads, setSquads] = useState<SquadsState>({ status: 'loading' })
  // 打开瞬间（closed→open）重置表单并重读小队：打开后的输入是用户的，不许后台刷新覆盖。
  const wasOpen = useRef(false)
  useEffect(() => {
    const opening = open && !wasOpen.current
    wasOpen.current = open
    if (!opening) return
    setTitle('')
    setPicked([])
    setSquad('')
    setSquads({ status: 'loading' })
    let cancelled = false
    getSquads()
      .then((resp) => {
        if (cancelled) return
        const coordinators = (resp.squads ?? []).filter((item) => item.role === 'coordinator')
        setSquads({ status: 'ready', squads: coordinators })
        // 预填：恰一个协调小队时直接选中；多个时留空让人选（不替人挑）。
        if (coordinators.length === 1) setSquad(coordinators[0].name)
        console.debug('session.create_dialog.squads_loaded', { project, coordinators: coordinators.map((item) => item.name) })
      })
      .catch((cause: unknown) => {
        if (cancelled) return
        setSquads({ status: 'error', message: errorMessage(cause) })
        console.warn('session.create_dialog.squads_failed', { project, error: errorMessage(cause) })
      })
    return () => { cancelled = true }
  }, [open, project])
  if (!open) return null

  const candidates = pullableCards(cards, project, sessions)
  const coordinators = squads.status === 'ready' ? squads.squads : []
  const noCoordinator = squads.status === 'ready' && coordinators.length === 0
  const canCreate = configured && !busy && squads.status === 'ready' && squad !== ''
  const toggle = (id: string) => setPicked((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id])

  return (
    <div role="dialog" aria-label="新建会话" className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
      <div className="flex max-h-[calc(100dvh-2rem)] w-full max-w-md flex-col overflow-hidden rounded-xl border bg-background shadow-xl">
        <h3 className="shrink-0 px-4 pt-4 text-sm font-semibold">新建会话</h3>
        {result !== null ? (
          <CreateResultView result={result} onDone={onDone} />
        ) : (
          <>
            <div className="min-h-0 flex-1 overflow-y-auto px-4">
              <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-project">项目</label>
              <input id="new-session-project" aria-label="所属项目" value={project} readOnly
                className="mt-1 w-full cursor-default rounded-md border bg-muted/40 px-2 py-1 text-sm" />

              <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-title">标题</label>
              <input id="new-session-title" aria-label="会话标题" value={title} placeholder="留空则自动命名" disabled={busy}
                onChange={(event) => setTitle(event.target.value)}
                className="mt-1 w-full rounded-md border px-2 py-1 text-sm" />

              <div className="mt-3 text-xs text-muted-foreground" id="new-session-cards-label">卡（可多选）</div>
              <div role="group" aria-labelledby="new-session-cards-label" data-testid="new-session-cards"
                className="mt-1 max-h-48 overflow-y-auto rounded-md border">
                {candidates.length === 0 ? (
                  <p className="px-2 py-2 text-xs text-muted-foreground">本项目没有可拉入的卡</p>
                ) : candidates.map((card) => (
                  <label key={card.id} className="flex min-w-0 cursor-pointer items-center gap-2 border-b px-2 py-1.5 text-sm last:border-b-0">
                    <input type="checkbox" checked={picked.includes(card.id)} disabled={busy} onChange={() => toggle(card.id)}
                      aria-label={`${card.id} ${card.title}`} />
                    <b className="shrink-0 font-mono text-xs">{card.id}</b>
                    <span className="min-w-0 flex-1 truncate">{card.title}</span>
                    <span className="shrink-0 text-xs text-muted-foreground">{card.status}</span>
                  </label>
                ))}
              </div>

              <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-squad">驻场小队</label>
              {squads.status === 'loading' && <p className="mt-1 text-xs text-muted-foreground">正在读取小队…</p>}
              {squads.status === 'error' && <p role="alert" className="mt-1 text-xs text-destructive">读取小队失败：{squads.message}</p>}
              {noCoordinator && (
                <p role="alert" className="mt-1 text-xs text-amber-700">
                  还没有协调小队（role=coordinator），无法拉起驻场者。请先到「设置」里建一个协调小队再新建会话。
                </p>
              )}
              {coordinators.length > 0 && (
                <select id="new-session-squad" aria-label="驻场小队" value={squad} disabled={busy} required
                  onChange={(event) => setSquad(event.target.value)}
                  className="mt-1 w-full rounded-md border bg-background px-2 py-1 text-sm">
                  {coordinators.length > 1 && <option value="">请选择协调小队</option>}
                  {coordinators.map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
                </select>
              )}

              {!configured && (
                <p role="alert" className="mt-2 text-xs text-amber-700">
                  本机未配置控制台人名 console_user，无法确定身份；请在 agentd 配置文件中设置 console_user 后重试。
                </p>
              )}
              {error !== '' && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
              {busy && <p role="status" className="mt-2 text-xs text-muted-foreground">正在拉起驻场者…</p>}
            </div>
            <div className="flex shrink-0 justify-end gap-2 px-4 py-4">
              <button type="button" onClick={onCancel} disabled={busy} className="rounded-md border px-2.5 py-1 text-xs disabled:opacity-50">取消</button>
              <button type="button" disabled={!canCreate}
                onClick={() => onCreate({ title: title.trim(), cards: picked, squad })}
                className="rounded-md bg-slate-900 px-2.5 py-1 text-xs text-white disabled:opacity-50">创建</button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// CreateResultView 建成后的点名结果：被拒的卡逐张列原因；驻场者首轮失败列原因与重试命令
// （Web 重试按钮落 roadmap，plan Q4）。会话本身已建成并已开 tab。
function CreateResultView({ result, onDone }: { result: SessionCreateResp; onDone: () => void }) {
  const rejected = result.rejected_cards ?? []
  const residentError = result.resident?.error ?? ''
  return (
    <>
      <div className="min-h-0 flex-1 overflow-y-auto px-4" data-testid="new-session-result">
        <p className="mt-3 text-sm">会话「{result.title}」已建立。</p>
        {rejected.length > 0 && (
          <div role="alert" className="mt-3 text-xs">
            <p className="font-medium text-amber-700">以下卡未拉入：</p>
            <ul className="mt-1 list-disc pl-5">
              {rejected.map((item) => <li key={item.card} data-testid="new-session-rejected"><b className="font-mono">{item.card}</b>：{item.reason}</li>)}
            </ul>
          </div>
        )}
        {residentError !== '' && (
          <div role="alert" className="mt-3 text-xs">
            <p className="font-medium text-destructive">驻场者未拉起：{residentError}</p>
            <p className="mt-1 break-all text-muted-foreground">可稍后重试：<code>handoff session resident launch {result.id}</code></p>
          </div>
        )}
      </div>
      <div className="flex shrink-0 justify-end px-4 py-4">
        <button type="button" onClick={onDone} className="rounded-md bg-slate-900 px-2.5 py-1 text-xs text-white">知道了</button>
      </div>
    </>
  )
}
