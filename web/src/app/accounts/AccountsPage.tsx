// AccountsPage.tsx —— 「◔ 账号」页（B453 spec S8）与登记/编辑/删除对话框（S1）。
// 形态基准 prototypes/b453-accounts/pages/accounts.html（视觉照 orca 设置页「AI 提供商账户」）。
//
// 职责：
//   - 按类型分四组（OpenCode Go / Codex / Grok / Antigravity），每组说明 +「＋ 登记账号」；
//     组内问题账号排前，每账号一张细边框紧凑卡片，点开展开代理/阈值/探测/引用载体
//   - 动作：立即探测（所在机执行）/ 编辑 / 删除（被载体引用时拦住并点名）
//   - highlight（来自 ?account=）预先展开并高亮那一张，滚到可见
//
// 边界：
//   - 数据只读 Shell 级共享快照（store.ts）；写操作经 api/accounts，回执就地合入快照
//   - 校验以服务端为准（F2 400 文案可行动，原文显示）；页面只拦空必填项
//   - 代理回显是服务端打码值（F5），原样提交 = 保留原值（段 2 裁决 7），页面不解析
import { useEffect, useRef, useState } from 'react'
import { ApiError, fetchMachines } from '../../api/client'
import { deleteAccount, putAccount, type AccountDeleteRefusal, type AccountInput, type AccountView } from '../../api/accounts'
import { errorMessage } from '../lib/format'
import {
  ago, byProblem, clock, credentialMoved, DEFAULT_INTERVAL, DEFAULT_LOW, DEFAULT_WARN, entryText, extraUsageText, INTERVALS,
  KIND_ORDER, kindMeta, KINDS, nextProbeText,
} from './model'
import { KindIcon, StatusText, WinsCompact } from './QuotaBits'
import { useAccounts, useNow } from './store'
import './accounts.css'

const INPUT = 'h-8 w-full rounded-md border bg-background px-2 text-xs'
const LABEL = 'mb-1 block text-xs text-muted-foreground'

interface Draft {
  name: string
  kind: string
  machine: string
  interval: string
  home: string
  entry: string
  proxy: string
  warn: string
  low: string
}

function draftOf(a: AccountView | null, kind: string): Draft {
  if (a) {
    return {
      name: a.name, kind: a.kind, machine: a.machine, interval: String(a.interval_minutes), home: a.home_dir,
      entry: a.entry ?? '', proxy: a.proxy ?? '', warn: String(a.warn_percent), low: String(a.low_percent),
    }
  }
  return {
    name: '', kind, machine: '', interval: String(DEFAULT_INTERVAL), home: '', entry: kindMeta(kind).entry,
    proxy: '', warn: String(DEFAULT_WARN), low: String(DEFAULT_LOW),
  }
}

// inputOf 草稿 → F9 请求体。空条目/空代理以键缺席表达（agy 无条目；opencode-go 空 = 自动；
// 不填代理 = 沿用该机代理）。
function inputOf(d: Draft): AccountInput {
  const body: AccountInput = { kind: d.kind, machine: d.machine.trim(), home_dir: d.home.trim() }
  if (d.entry.trim() && d.kind !== 'agy') body.entry = d.entry.trim()
  if (d.proxy.trim()) body.proxy = d.proxy.trim()
  body.interval_minutes = Number(d.interval)
  body.warn_percent = Number(d.warn)
  body.low_percent = Number(d.low)
  return body
}

/** AccountDialog 登记/编辑同一表单；kind 换了就回填该 kind 的缺省条目。保存成功回调 onSaved（是否该立即探测）。 */
function AccountDialog({ editing, kind, machines, onClose, onSaved }: {
  editing: AccountView | null
  kind: string
  machines: string[]
  onClose: () => void
  onSaved: (name: string, probe: boolean) => void
}) {
  const { apply } = useAccounts()
  const [d, setD] = useState<Draft>(() => draftOf(editing, kind))
  const [homeTouched, setHomeTouched] = useState(editing !== null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const managed = d.kind === 'agy' // agy 没有可登记的凭据条目：CLI 自己的登录态
  const title = editing ? `编辑账号 · ${editing.name}` : `登记账号 · ${kindMeta(d.kind).name}`
  const set = (patch: Partial<Draft>) => setD((cur) => ({ ...cur, ...patch }))

  const save = async () => {
    const name = d.name.trim()
    if (!name || !d.machine.trim() || !d.home.trim()) {
      setError('账号名、所在机器、凭据 HOME 都必填')
      return
    }
    const expect = editing?.version ?? 0
    const body = inputOf(d)
    setSaving(true)
    setError('')
    console.info('accounts.save.start', { name, expect, kind: body.kind, machine: body.machine })
    try {
      const view = await putAccount(name, expect, body)
      console.info('accounts.save.success', { name, version: view.version, state: view.state })
      apply(view)
      onClose()
      onSaved(view.name, credentialMoved(editing, body))
    } catch (cause) {
      console.error('accounts.save.failure', { name, expect, cause })
      setError(errorMessage(cause))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/30 p-4" role="dialog" aria-modal="true" aria-label={title}>
      <form className="w-full max-w-[520px] space-y-3 rounded-lg border bg-background p-5 shadow-lg" onSubmit={(e) => { e.preventDefault(); void save() }}>
        <h3 className="text-sm font-semibold">{title}</h3>
        <div className="grid gap-x-3 gap-y-2.5 sm:grid-cols-2">
          <div><label htmlFor="af-name" className={LABEL}>账号名（唯一，登记后不可改）</label>
            <input id="af-name" aria-label="账号名" className={`${INPUT} font-mono`} placeholder="如 og-team" readOnly={editing !== null} value={d.name}
              onChange={(e) => { const name = e.target.value; set(homeTouched ? { name } : { name, home: name.trim() ? `~/.handoff/home/${name.trim()}` : '' }) }} /></div>
          <div><label htmlFor="af-kind" className={LABEL}>类型</label>
            <select id="af-kind" aria-label="类型" className={INPUT} value={d.kind} onChange={(e) => set({ kind: e.target.value, entry: kindMeta(e.target.value).entry })}>
              {KIND_ORDER.map((k) => <option key={k} value={k}>{KINDS[k].label}</option>)}
            </select></div>
          <div><label htmlFor="af-machine" className={LABEL}>所在机器（探测在这台机器上执行）</label>
            <input id="af-machine" aria-label="所在机器" list="af-machines" className={INPUT} placeholder="全队名，如 linux-01" value={d.machine} onChange={(e) => set({ machine: e.target.value })} />
            <datalist id="af-machines">{machines.map((m) => <option key={m} value={m} />)}</datalist></div>
          <div><label htmlFor="af-interval" className={LABEL}>探测间隔</label>
            <select id="af-interval" aria-label="探测间隔" className={INPUT} value={d.interval} onChange={(e) => set({ interval: e.target.value })}>
              {INTERVALS.map((n) => <option key={n} value={String(n)}>{n === 60 ? '每 1 小时' : `每 ${n} 分钟`}</option>)}
            </select></div>
          <div className="sm:col-span-2"><label htmlFor="af-home" className={LABEL}>凭据 HOME（该机上的路径，绝对路径或 ~ 开头）</label>
            <input id="af-home" aria-label="凭据 HOME" className={`${INPUT} font-mono`} placeholder="/root/.handoff/home/…" value={d.home} onChange={(e) => { setHomeTouched(true); set({ home: e.target.value }) }} /></div>
          <div className="sm:col-span-2"><label htmlFor="af-entry" className={LABEL}>{d.kind === 'opencode-go' ? '凭据条目（留空 = 自动；opencode.db = 登录账号；其他 = auth.json 里的键）' : '凭据条目（HOME 下 auth.json 里的键）'}</label>
            <input id="af-entry" aria-label="凭据条目" className={`${INPUT} font-mono`} disabled={managed} placeholder={managed ? '由 agy CLI 管理' : (kindMeta(d.kind).entryHint ?? '')} value={managed ? '' : d.entry} onChange={(e) => set({ entry: e.target.value })} />
            <p className="mt-1 text-[11px] leading-5 text-muted-foreground">{managed
              ? '由 agy CLI 管理（agy CLI 自己的登录态），无需登记条目。只登记 HOME，保存后所在机 agentd 读登录态、回填短指纹。'
              : '只登记位置。保存后所在机 agentd 读取凭据、回填短指纹，密钥不进账本。'}</p></div>
          <div className="sm:col-span-2"><label htmlFor="af-proxy" className={LABEL}>代理（可选，只用于这个账号的探测与派发）</label>
            <input id="af-proxy" aria-label="代理" className={`${INPUT} font-mono`} placeholder="留空 = 沿用该机代理，如 socks5://127.0.0.1:7897" value={d.proxy} onChange={(e) => set({ proxy: e.target.value })} />
            {editing?.proxy?.includes('***') && <p className="mt-1 text-[11px] leading-5 text-muted-foreground">密码已打码显示；不改就原样保留。</p>}</div>
          <div><label htmlFor="af-warn" className={LABEL}>预警线（只变色，不影响派发）</label>
            <div className="flex items-center gap-1.5"><input id="af-warn" aria-label="预警线" className={`${INPUT} w-[90px]`} type="number" min={0} max={100} value={d.warn} onChange={(e) => set({ warn: e.target.value })} /><span className="text-xs text-muted-foreground">%</span></div></div>
          <div><label htmlFor="af-low" className={LABEL}>低额度线（达到即暂停派发）</label>
            <div className="flex items-center gap-1.5"><input id="af-low" aria-label="低额度线" className={`${INPUT} w-[90px]`} type="number" min={0} max={100} value={d.low} onChange={(e) => set({ low: e.target.value })} /><span className="text-xs text-muted-foreground">%</span></div></div>
          <p className="text-[11px] leading-5 text-muted-foreground sm:col-span-2">按任一窗口（会话 5h / 每周 / 每月）的已用百分比判断；任一窗口到 100% 即无额度。默认预警 {DEFAULT_WARN}%、低额度 {DEFAULT_LOW}%。</p>
        </div>
        {error && <p role="alert" className="break-words text-xs text-destructive">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" className="rounded-md border px-3 py-1.5 text-xs" disabled={saving} onClick={onClose}>取消</button>
          <button type="submit" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" disabled={saving}>{saving ? '保存中…' : '保存'}</button>
        </div>
      </form>
    </div>
  )
}

/** DeleteDialog 删除确认；仍被载体引用（快照或服务端 409）时拦住并点名去改哪些载体。 */
function DeleteDialog({ a, onClose }: { a: AccountView; onClose: () => void }) {
  const { remove } = useAccounts()
  const [refs, setRefs] = useState<string[]>(a.carriers)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const run = async () => {
    setBusy(true)
    setError('')
    console.info('accounts.delete.start', { name: a.name, expect: a.version })
    try {
      await deleteAccount(a.name, a.version)
      console.info('accounts.delete.success', { name: a.name })
      remove(a.name)
      onClose()
    } catch (cause) {
      const body = cause instanceof ApiError ? (cause.body as Partial<AccountDeleteRefusal> | undefined) : undefined
      if (cause instanceof ApiError && cause.status === 409 && Array.isArray(body?.carriers) && body.carriers.length) {
        console.warn('accounts.delete.referenced', { name: a.name, carriers: body.carriers })
        setRefs(body.carriers)
      } else {
        console.error('accounts.delete.failure', { name: a.name, cause })
        setError(errorMessage(cause))
      }
    } finally {
      setBusy(false)
    }
  }
  const blocked = refs.length > 0
  const title = blocked ? `不能删除 ${a.name}` : `删除账号 ${a.name}？`
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/30 p-4" role="dialog" aria-modal="true" aria-label={title}>
      <div className="w-full max-w-[420px] rounded-lg border bg-background p-5 shadow-lg">
        <h3 className="mb-2 text-sm font-semibold">{title}</h3>
        <p className="text-xs leading-6 text-muted-foreground">{blocked
          ? <>仍有 {refs.length} 个载体引用这个账号：{refs.map((c, i) => <span key={c}>{i > 0 && '、'}<b className="font-mono text-foreground">{c}</b></span>)}。先在「设置 · 自动化」把它们换到别的账号，或清空账号选择。</>
          : `只删除账本里的登记，${a.machine} 上的凭据文件不动。删除后各机控制台同时看不到它。`}</p>
        {error && <p role="alert" className="mt-2 break-words text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded-md border px-3 py-1.5 text-xs" onClick={onClose}>{blocked ? '关闭' : '取消'}</button>
          {!blocked && <button type="button" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" disabled={busy} onClick={() => void run()}>删除</button>}
        </div>
      </div>
    </div>
  )
}

/** AccountCard 一张紧凑卡片：名 + 套餐小标 + 状态短语；弱色凭据位置行；窗口横排；点开展开详情。 */
function AccountCard({ a, open, flash, now, autoProbe, onToggle, onEdit, onDelete }: {
  a: AccountView; open: boolean; flash: boolean; now: number
  /** 保存后自动探测的请求序号（每次保存 +1）；变了就探测一次，结果与失败写在本卡片的提示行。 */
  autoProbe: number
  onToggle: () => void; onEdit: () => void; onDelete: () => void
}) {
  const { probe } = useAccounts()
  const [probing, setProbing] = useState(false)
  const [note, setNote] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (flash) ref.current?.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
  }, [flash])
  const runProbe = async () => {
    setProbing(true)
    setNote(`已请求 ${a.machine} 的 agentd 探测…`)
    try {
      const v = await probe(a.name)
      setNote(v.state === 'fail' ? `探测完成：仍失败（${v.last_error?.message ?? '原因未知'}）。检查 ${a.machine} 上的代理或登录。` : '探测完成，已写回账本。')
    } catch (cause) {
      setNote(`探测请求失败：${errorMessage(cause)}`)
    } finally {
      setProbing(false)
    }
  }
  const probeRef = useRef(runProbe)
  probeRef.current = runProbe
  useEffect(() => {
    if (autoProbe > 0) void probeRef.current()
  }, [autoProbe])
  const lastAttempt = a.last_attempt_at ? `${ago(a.last_attempt_at, now)}（${clock(Date.parse(a.last_attempt_at))}）` : '从未'
  const extra = extraUsageText(a)
  const hasWins = (a.reading?.windows.length ?? 0) > 0
  return (
    <div ref={ref} data-testid={`acard-${a.name}`} className={`acard${open ? ' open' : ''}${flash ? ' flash' : ''}`}
      onClick={(e) => { if (!(e.target as HTMLElement).closest('button, a, .a-detail')) onToggle() }}>
      <div className="a-r1">
        <span className="a-chev">›</span>
        <span className="a-name">{a.name}</span>
        {a.reading?.plan_type && <span className="q-tag" title="套餐">{a.reading.plan_type}</span>}
        <span className="spacer" />
        <span className="a-state"><StatusText a={a} /></span>
        <span className="a-actions">
          <button type="button" className="tbtn" disabled={probing} onClick={() => void runProbe()}>{probing ? '探测中…' : '立即探测'}</button>
          <button type="button" className="tbtn" onClick={onEdit}>编辑</button>
          <button type="button" className="tbtn" onClick={onDelete}>删除</button>
        </span>
      </div>
      <div className="a-r2">{a.machine} · {entryText(a)} · {a.reading?.fingerprint || '指纹待首次探测回填'}</div>
      <div className="a-r3">
        {hasWins
          ? <><WinsCompact a={a} now={now} />{a.state === 'fail' && <div className="a-note mt-1.5">显示的是 {ago(a.reading?.probed_at, now)}的读数</div>}</>
          : <div className="a-note">{a.state === 'none' ? '尚未探测，保存后由所在机 agentd 探测一次' : `额度未知 · ${a.last_error?.message ?? '无读数'}`}</div>}
      </div>
      {open && (
        <div className="a-detail">
          <dl className="a-kv">
            <dt>类型</dt><dd>{kindMeta(a.kind).label}</dd>
            <dt>代理</dt><dd>{a.proxy ? <code>{a.proxy}</code> : <span className="q-mute">沿用该机代理</span>}</dd>
            <dt>阈值</dt><dd>预警 ≥ {a.warn_percent}%（只变色） · 低额度 ≥ {a.low_percent}%（暂停派发）</dd>
            <dt>探测</dt><dd>每 {a.interval_minutes} 分钟 · 上次 {lastAttempt} · {nextProbeText(a, now)}{a.reading && a.state === 'fail' ? ` · 最后成功读数 ${ago(a.reading.probed_at, now)}` : ''}{a.stale ? ' · 读数已过期' : ''}</dd>
            {a.last_error && <><dt>上次错误</dt><dd>△ {a.last_error.message}</dd></>}
            {extra && <><dt>额外额度</dt><dd className="q-mono">{extra}</dd></>}
            <dt>使用此账号的载体</dt><dd>{a.carriers.length ? a.carriers.map((c, i) => <span key={c}>{i > 0 && '、'}<span className="q-mono">{c}</span></span>) : <span className="q-mute">没有载体引用</span>}</dd>
          </dl>
        </div>
      )}
      {note && <p className="a-probe-note">{note}</p>}
    </div>
  )
}

/** AccountsPage 账号与额度页；highlight = ?account= 指定的账号（预先展开、高亮、滚到可见）。 */
export function AccountsPage({ highlight = '' }: { highlight?: string }) {
  const { accounts, errorText } = useAccounts()
  const now = useNow()
  const [opened, setOpened] = useState<Set<string>>(() => new Set(highlight ? [highlight] : []))
  const [dialog, setDialog] = useState<{ kind: string; editing: AccountView | null } | null>(null)
  const [deleting, setDeleting] = useState<AccountView | null>(null)
  const [machines, setMachines] = useState<string[]>([])
  // 保存后自动探测（m2）：name → 请求序号；卡片看到序号变化就探测一次。不阻塞保存。
  const [autoProbe, setAutoProbe] = useState<Record<string, number>>({})
  const onSaved = (name: string, probe: boolean) => {
    if (!probe) return
    console.info('accounts.save.probe', { name })
    setAutoProbe((cur) => ({ ...cur, [name]: (cur[name] ?? 0) + 1 }))
  }

  useEffect(() => {
    if (highlight) setOpened((cur) => new Set(cur).add(highlight))
  }, [highlight])

  useEffect(() => {
    void fetchMachines().then((resp) => {
      setMachines(resp.machines.map((m) => m.name).filter((n) => n !== ''))
    }).catch((cause: unknown) => {
      console.error('accounts.machines.failure', { cause })
    })
  }, [])

  const list = accounts ?? []
  const machineOptions = [...new Set([...machines, ...list.map((a) => a.machine)])]
  const toggle = (name: string) => setOpened((cur) => {
    const next = new Set(cur)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    return next
  })

  return (
    <div className="acct-scope acct-body" data-testid="accounts-page">
      <div className="acct-wrap">
        <div className="acct-head"><h1>账号与额度</h1></div>
        <p className="acct-desc">账号登记在账本里，各机控制台看到同一份；密钥不离机，额度由所在机 agentd 探测。<br />低额度或无额度时，引用它的载体暂停派发，额度恢复后自动回来。</p>
        {accounts === null && !errorText && <p className="a-note">正在读取账号…</p>}
        {errorText && <p role="alert" className="a-note">读取账号失败：{errorText}{accounts ? '（显示的是上次读到的数据）' : ''}</p>}
        {KIND_ORDER.map((kind) => {
          const meta = KINDS[kind]
          const group = byProblem(list.filter((a) => a.kind === kind))
          return (
            <section key={kind} className="kgroup" data-testid={`kgroup-${kind}`}>
              <div className="kg-head">
                <div className="grow">
                  <h2 className="kg-title"><KindIcon kind={kind} />{meta.name}</h2>
                  <div className="kg-desc">{meta.blurb}</div>
                </div>
                <button type="button" className="rounded-md border px-2.5 py-1 text-xs" onClick={() => setDialog({ kind, editing: null })}>＋ 登记账号</button>
              </div>
              {group.map((a) => (
                <AccountCard key={a.name} a={a} now={now} open={opened.has(a.name)} flash={a.name === highlight} autoProbe={autoProbe[a.name] ?? 0}
                  onToggle={() => toggle(a.name)} onEdit={() => setDialog({ kind: a.kind, editing: a })} onDelete={() => setDeleting(a)} />
              ))}
              {accounts !== null && group.length === 0 && <div className="a-empty">还没有 {meta.name} 账号，点右上「登记账号」。</div>}
            </section>
          )
        })}
      </div>
      {dialog && <AccountDialog key={dialog.editing?.name ?? `new-${dialog.kind}`} editing={dialog.editing} kind={dialog.kind} machines={machineOptions} onClose={() => setDialog(null)} onSaved={onSaved} />}
      {deleting && <DeleteDialog a={deleting} onClose={() => setDeleting(null)} />}
    </div>
  )
}
