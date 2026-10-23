// AccountsPage.tsx —— 「◔ 账号」页（B464 S1–S7 形态，形态基准 prototypes/b464-accounts/pages/accounts.html）。
//
// 职责：
//   - 按六种 kind 分组（OpenCode Go / Codex / Grok / Antigravity / Claude / 第三方 API key），组内问题账号排前
//   - 账号卡：身份摘要 + 额度窗口 + 登录子表（机器/HOME/来源/指纹/状态/代理/载体）+ 可展开的阈值与探测信息
//   - 动作：改名（内联）、改阈值/间隔（展开区）、编辑登录代理、复制登录命令、立即探测、在其他机器登录、删除
//   - 页头「上次发现 x 前 · 立即发现」（逐机报错）与「＋ 新增登录」三步对话框
//
// 边界：
//   - 数据只读 Shell 级共享快照（store.ts）；写操作经 api/accounts，回执就地合入快照
//   - 校验以服务端为准（400/409 文案原文显示）；页面只拦空必填项与明显非法目录名
//   - 载体/额度的状态判断归 model.ts；本页不推导 state
//   - 凭据值与原始身份永不出现：只展示服务端给的来源名与短指纹
import { useEffect, useRef, useState } from 'react'
import { ApiError, fetchMachines } from '../../api/client'
import {
  deleteAccount, discoverAccounts, putAccount,
  type AccountDeleteRefusal, type AccountInput, type AccountView, type MachineDiscoverResult,
} from '../../api/accounts'
import { errorMessage } from '../lib/format'
import { Logins } from './Logins'
import { LoginWizard } from './LoginWizard'
import {
  ago, byProblem, DEFAULT_INTERVAL, DEFAULT_LOW, DEFAULT_WARN, extraUsageText, failBrief, INTERVALS,
  KIND_ORDER, kindMeta, machinesText, nextProbeText, supportsProbe,
} from './model'
import { KindIcon, StatusText, WinsCompact } from './QuotaBits'
import { useAccounts, useNow } from './store'
import './accounts.css'

const INPUT = 'h-8 w-full rounded-md border bg-background px-2 text-xs'

/** AccountInputOf 展开区的阈值/间隔 → A6 编辑体。 */
function thresholdInput(warn: string, low: string, interval: string): AccountInput {
  return { interval_minutes: Number(interval), warn_percent: Number(warn), low_percent: Number(low) }
}

/** AccountDetail 账号展开区：身份、阈值/间隔（仅支持探测的 kind）、探测时刻与失败原因。 */
function AccountDetail({ a }: { a: AccountView }) {
  const now = useNow()
  const [warn, setWarn] = useState(String(a.warn_percent ?? DEFAULT_WARN))
  const [low, setLow] = useState(String(a.low_percent ?? DEFAULT_LOW))
  const [interval, setIntervalText] = useState(String(a.interval_minutes ?? DEFAULT_INTERVAL))
  const [saving, setSaving] = useState(false)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const { apply } = useAccounts()
  const quota = supportsProbe(a.kind)
  const extra = extraUsageText(a)
  const lastAttempt = a.last_attempt_at ? `${ago(a.last_attempt_at, now)}` : '从未'
  const save = async () => {
    setSaving(true)
    setError('')
    console.info('accounts.thresholds.start', { account: a.id, warn, low, interval })
    try {
      const view = await putAccount(a.id, a.version, thresholdInput(warn, low, interval))
      console.info('accounts.thresholds.success', { account: a.id, version: view.version })
      apply(view)
      setNote(`已保存阈值：预警 ${view.warn_percent ?? warn}% · 低额度 ${view.low_percent ?? low}% · 每 ${view.interval_minutes ?? interval} 分钟探测。`)
    } catch (cause) {
      console.error('accounts.thresholds.failure', { account: a.id, cause })
      setError(errorMessage(cause))
    } finally {
      setSaving(false)
    }
  }
  return (
    <div className="a-detail">
      <dl className="a-kv">
        <dt>类型</dt><dd>{kindMeta(a.kind).label}</dd>
        <dt>身份</dt><dd className="mono">{a.label || a.id}</dd>
        {a.provider && <><dt>provider</dt><dd className="mono">{a.provider}</dd></>}
        {quota ? <>
          <dt>阈值</dt><dd>
            <div className="th-row">
              <span>预警</span><input aria-label="预警线" className={`${INPUT} w-[64px]`} type="number" min={0} max={100} value={warn} onChange={(e) => setWarn(e.target.value)} />
              <span>%（只变色）</span>
              <span>低额度</span><input aria-label="低额度线" className={`${INPUT} w-[64px]`} type="number" min={0} max={100} value={low} onChange={(e) => setLow(e.target.value)} />
              <span>%（暂停派发）</span>
              <span>探测</span>
              <select aria-label="探测间隔" className={INPUT} value={interval} onChange={(e) => setIntervalText(e.target.value)}>
                {INTERVALS.map((n) => <option key={n} value={String(n)}>{n === 60 ? '每 1 小时' : `每 ${n} 分钟`}</option>)}
              </select>
              <button type="button" className="tbtn" disabled={saving} onClick={() => void save()}>{saving ? '保存中…' : '保存阈值'}</button>
            </div>
            <p className="a-note">按任一窗口（会话 5h / 每周 / 每月）的已用百分比判断；任一窗口到 100% 即无额度。默认预警 {DEFAULT_WARN}%、低额度 {DEFAULT_LOW}%。</p>
          </dd>
          <dt>探测</dt><dd>每 {a.interval_minutes ?? DEFAULT_INTERVAL} 分钟 · 上次 {lastAttempt} · {nextProbeText(a, now)}{a.stale ? ' · 读数已过期' : ''}</dd>
        </> : <><dt>额度</dt><dd><span className="q-mute">不支持额度探测：没有阈值与探测间隔，引用它的载体照常派发</span></dd></>}
        {a.last_error && <><dt>上次错误</dt><dd className="q-mute">△ {a.last_error.message}</dd></>}
        {extra && <><dt>额外额度</dt><dd className="q-mono">{extra}</dd></>}
        <dt>阈值/间隔说明</dt><dd className="q-mute">预警 ≥ {a.warn_percent ?? '—'}%（只变色） · 低额度 ≥ {a.low_percent ?? '—'}%（暂停派发）</dd>
      </dl>
      {note && <p className="a-probe-note">{note}</p>}
      {error && <p role="alert" className="a-probe-note q-out">{error}</p>}
    </div>
  )
}

/** DeleteDialog 删除确认：有载体引用时拦住并点名；无引用时写明下次发现会重新出现。 */
function DeleteDialog({ a, onClose, onRemoved }: { a: AccountView; onClose: () => void; onRemoved: () => void }) {
  const [refs, setRefs] = useState<string[]>(a.carriers)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const run = async () => {
    setBusy(true)
    setError('')
    console.info('accounts.delete.start', { account: a.id, name: a.name, expect: a.version })
    try {
      await deleteAccount(a.id, a.version)
      console.info('accounts.delete.success', { account: a.id, name: a.name })
      onRemoved()
      onClose()
    } catch (cause) {
      const body = cause instanceof ApiError ? (cause.body as Partial<AccountDeleteRefusal> | undefined) : undefined
      if (cause instanceof ApiError && cause.status === 409 && Array.isArray(body?.carriers) && body.carriers.length) {
        console.warn('accounts.delete.referenced', { account: a.id, carriers: body.carriers })
        setRefs(body.carriers)
      } else {
        console.error('accounts.delete.failure', { account: a.id, cause })
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
      <div className="w-full max-w-[440px] rounded-lg border bg-background p-5 shadow-lg">
        <h3 className="mb-2 text-sm font-semibold">{title}</h3>
        <p className="text-xs leading-6 text-muted-foreground">{blocked
          ? <>仍有 {refs.length} 个载体在用它的登录：{refs.map((c, i) => <span key={c}>{i > 0 && '、'}<b className="font-mono text-foreground">{c}</b></span>)}。先在「设置 · 自动化」把它们换到别的账号登录，或改选「无账号 · 主 HOME」。</>
          : <>只删账本里的账号与 {a.logins.length} 条登录记录，{machinesText(a)} 上的凭据文件与 HOME 都不动。若这些 HOME 里仍登着这个身份，<b className="font-medium text-foreground">下次发现会重新认出来</b>——要彻底去掉，先在该机退出登录。</>}</p>
        {error && <p role="alert" className="mt-2 break-words text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded-md border px-3 py-1.5 text-xs" onClick={onClose}>{blocked ? '关闭' : '取消'}</button>
          {!blocked && <button type="button" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" disabled={busy} onClick={() => void run()}>{busy ? '删除中…' : '删除'}</button>}
        </div>
      </div>
    </div>
  )
}

const PENCIL = <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M10.8 2.6l2.6 2.6-7.8 7.8H3v-2.6z" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" /></svg>

/** AccountCard 一张账号卡：身份与状态、额度窗口、登录子表、可展开的阈值/探测详情。 */
function AccountCard({ a, open, flash, now, renaming, notice, failure, onRename, onToggle, onOtherLogin, onDelete }: {
  a: AccountView
  open: boolean
  flash: boolean
  now: number
  renaming: { name: string; value: string } | null
  notice?: string
  failure?: string
  onRename: (value: string, done?: boolean) => void
  onToggle: () => void
  onOtherLogin: () => void
  onDelete: () => void
}) {
  const { probe, applyLogin } = useAccounts()
  const [probing, setProbing] = useState(false)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => { if (flash) ref.current?.scrollIntoView?.({ block: 'center', behavior: 'smooth' }) }, [flash])
  const quota = supportsProbe(a.kind)
  const runProbe = async () => {
    setProbing(true)
    setError('')
    setNote(`已请求探测 ${a.name}…`)
    console.info('accounts.probe.start', { account: a.id })
    try {
      const v = await probe(a.name)
      setNote(v.state === 'fail' ? `探测完成：仍失败（${failBrief(v)}）。检查登录所在机的代理或登录。` : '探测完成，已写回账本。')
    } catch (cause) {
      console.error('accounts.probe.failure', { account: a.id, cause })
      setError(`探测请求失败：${errorMessage(cause)}`)
    } finally {
      setProbing(false)
    }
  }
  const hasWins = (a.reading?.windows.length ?? 0) > 0
  return (
    <div ref={ref} data-testid={`acard-${a.name}`} className={`acard${open ? ' open' : ''}${flash ? ' flash' : ''}`}
      onClick={(e) => { if (!(e.target as HTMLElement).closest('button, a, input, select, .a-detail, .lgs')) onToggle() }}>
      <div className="a-r1">
        <span className="a-chev">›</span>
        {renaming
          ? <input aria-label="账号名" className={`${INPUT} a-rename`} autoFocus value={renaming.value}
              onChange={(e) => onRename(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') onRename(renaming.value, true); if (e.key === 'Escape') onRename(a.name, true) }} />
          : <>
            <span className="a-name">{a.name}</span>
            <button type="button" className="tbtn ibtn" aria-label={`改名 ${a.name}`} title="改名" onClick={() => onRename(a.name)}>{PENCIL}</button>
            {(a.provider || a.reading?.plan_type) && <span className="q-tag" title={a.provider ? 'provider' : '套餐'}>{a.provider || a.reading?.plan_type}</span>}
          </>}
        <span className="spacer" />
        <span className="a-state">{quota ? <StatusText a={a} /> : null}</span>
        <span className="a-actions">
          {quota && <button type="button" className="tbtn" disabled={probing} onClick={() => void runProbe()}>{probing ? '探测中…' : '立即探测'}</button>}
          <button type="button" className="tbtn" onClick={onOtherLogin}>在其他机器登录</button>
          <button type="button" className="tbtn" onClick={onDelete}>删除</button>
        </span>
      </div>
      <div className="a-r2">{[a.label || a.id, `${a.logins.length} 条登录 · ${machinesText(a)}`].filter(Boolean).join(' · ')}</div>
      <div className="a-r3">
        {quota
          ? (hasWins
            ? <><WinsCompact a={a} now={now} />{a.state === 'fail' && a.reading && <div className="a-note mt-1.5">显示的是 {ago(a.reading.probed_at, now)}的读数</div>}</>
            : <div className="a-note">{a.state === 'none' ? '尚未探测，发现后由持有正常登录的机器探测一次' : `额度未知 · ${a.last_error?.message ?? '无读数'}`}</div>)
          : <div className="a-note">不支持额度探测</div>}
      </div>
      <Logins kind={a.kind} logins={a.logins} onLogin={applyLogin} />
      {open && <AccountDetail a={a} />}
      {note && <p className="a-probe-note">{note}</p>}
      {error && <p role="alert" className="a-probe-note q-out">{error}</p>}
      {notice && <p className="a-probe-note">{notice}</p>}
      {failure && <p role="alert" className="a-probe-note q-out">{failure}</p>}
    </div>
  )
}

interface NewLoginPreset { machine?: string; agent?: string }

/** AccountsPage 账号与额度页；highlight = ?account= 名字，newLogin = ?login=new 预选。 */
export function AccountsPage({ highlight = '', newLogin = null }: { highlight?: string; newLogin?: NewLoginPreset | null }) {
  const { accounts, discovery, errorText, refresh, remove } = useAccounts()
  const now = useNow()
  const [opened, setOpened] = useState<Set<string>>(() => new Set(highlight ? [highlight] : []))
  const [renaming, setRenaming] = useState<{ name: string; value: string } | null>(null)
  const [deleting, setDeleting] = useState<AccountView | null>(null)
  const [wizard, setWizard] = useState<{ open: boolean; account: AccountView | null; preset?: NewLoginPreset }>(
    () => ({ open: newLogin !== null, account: null, preset: newLogin ?? undefined }),
  )
  const [machines, setMachines] = useState<string[]>([])
  const [notes, setNotes] = useState<Record<string, string>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [finding, setFinding] = useState(false)
  const [findResult, setFindResult] = useState<MachineDiscoverResult[] | null>(null)
  const [findError, setFindError] = useState('')
  const { apply } = useAccounts()

  useEffect(() => { if (highlight) setOpened((cur) => new Set(cur).add(highlight)) }, [highlight])
  useEffect(() => {
    void fetchMachines().then((resp) => setMachines(resp.machines.map((m) => m.name).filter((n) => n !== '')))
      .catch((cause: unknown) => console.error('accounts.machines.failure', { cause }))
  }, [])

  const list = accounts ?? []
  const selfMachines = discovery.filter((d) => d.self).map((d) => d.machine)
  const machineOptions = [...new Set([...machines, ...list.flatMap((a) => a.logins.map((l) => l.machine))])]
  const toggle = (name: string) => setOpened((cur) => { const next = new Set(cur); if (next.has(name)) next.delete(name); else next.add(name); return next })
  const note = (name: string, text: string) => setNotes((cur) => ({ ...cur, [name]: text }))
  const trouble = (name: string, text: string) => setErrors((cur) => ({ ...cur, [name]: text }))

  // 改名：Enter/失焦提交；空串 = 放弃；非法字符与服务端 409 都在卡上原文显示。
  const commitRename = (a: AccountView, value: string, done = false) => {
    setRenaming({ name: a.name, value })
    if (!done) return
    const name = value.trim()
    if (!name || name === a.name) { setRenaming(null); return }
    if (name.includes(':') || name.includes('/')) { trouble(a.name, '账号名不能含「:」或「/」'); setRenaming(null); return }
    console.info('accounts.rename.start', { account: a.id, from: a.name, to: name })
    void putAccount(a.id, a.version, { name }).then((view) => {
      console.info('accounts.rename.success', { account: a.id, to: view.name })
      apply(view)
      setRenaming(null)
      setErrors((cur) => { const next = { ...cur }; delete next[a.name]; return next })
    }).catch((cause: unknown) => {
      console.error('accounts.rename.failure', { account: a.id, to: name, cause })
      trouble(a.name, errorMessage(cause))
      setRenaming(null)
    })
  }

  const discoverNow = async () => {
    setFinding(true)
    setFindError('')
    console.info('accounts.discover.start')
    try {
      const resp = await discoverAccounts()
      const failed = resp.machines.filter((m) => !m.ok)
      console.info('accounts.discover.done', { machines: resp.machines.length, failed: failed.length })
      setFindResult(resp.machines)
      refresh()
    } catch (cause) {
      console.error('accounts.discover.failure', { cause })
      setFindError(errorMessage(cause))
    } finally {
      setFinding(false)
    }
  }

  const shown = findResult ?? discovery
  const latest = shown.map((d) => Date.parse(d.at ?? '')).filter((t) => !Number.isNaN(t)).sort((x, y) => y - x)[0]
  const problems = shown.filter((d) => 'ok' in d ? !d.ok : !!d.error)
  const discoveryText = `上次发现 ${latest ? ago(new Date(latest).toISOString(), now) : '从未'} · ${shown.length} 台机器${shown.length ? `（${shown.map((d) => d.machine).join('、')}）` : ''}`
  const problemText = problems.map((d) => `${d.machine}：${('error' in d ? d.error : '') || '发现失败'}`).join('；')

  return (
    <div className="acct-scope acct-body" data-testid="accounts-page">
      <div className="acct-wrap">
        <div className="acct-head">
          <h1>账号与额度</h1>
          <span className="flex-1" />
          <span className="disc" data-testid="discovery-note">
            {discoveryText}{problemText && ` · ${problemText}`}{findError && ` · 立即发现失败：${findError}`}
          </span>
          <button type="button" className="rounded-md border px-2.5 py-1 text-xs" disabled={finding} onClick={() => void discoverNow()}>{finding ? '发现中…' : '立即发现'}</button>
          <button type="button" className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground" onClick={() => setWizard({ open: true, account: null })}>＋ 新增登录</button>
        </div>
        <p className="acct-desc">各机 agentd 自动扫主 HOME 与 <span className="mono">~/.handoff/home/*</span>，认出已登录的身份，同一身份归到同一个账号下；密钥不离机，只存短指纹。<br />额度挂在账号上：低额度或无额度时，用它任一条登录的载体都暂停派发，额度恢复后自动回来。</p>
        {accounts === null && !errorText && <p className="a-note">正在读取账号…</p>}
        {errorText && <p role="alert" className="a-note">读取账号失败：{errorText}{accounts ? '（显示的是上次读到的数据）' : ''}</p>}
        {KIND_ORDER.map((kind) => {
          const meta = kindMeta(kind)
          const group = byProblem(list.filter((a) => a.kind === kind))
          return (
            <section key={kind} className="kgroup" data-testid={`kgroup-${kind}`}>
              <div className="kg-head">
                <div className="grow">
                  <h2 className="kg-title"><KindIcon kind={kind} />{meta.name}</h2>
                  <div className="kg-desc">{meta.blurb}</div>
                </div>
              </div>
              {group.map((a) => (
                <AccountCard key={a.name} a={a} now={now} open={opened.has(a.name)} flash={a.name === highlight}
                  renaming={renaming && renaming.name === a.name ? renaming : null}
                  notice={notes[a.name]}
                  failure={errors[a.name]}
                  onRename={(value, done) => commitRename(a, value, done)}
                  onToggle={() => toggle(a.name)}
                  onOtherLogin={() => setWizard({ open: true, account: a })}
                  onDelete={() => setDeleting(a)} />
              ))}
              {accounts !== null && group.length === 0 && <div className="a-empty">还没有 {meta.name} 账号。点右上「＋ 新增登录」在某台机器上登录 {meta.cli}，识别后自动出现在这里。</div>}
            </section>
          )
        })}
      </div>
      {deleting && <DeleteDialog a={deleting} onClose={() => setDeleting(null)} onRemoved={() => remove(deleting.name)} />}
      <LoginWizard open={wizard.open} machines={machineOptions} selfMachines={selfMachines} account={wizard.account} preset={wizard.preset}
        onClose={() => setWizard((cur) => ({ ...cur, open: false }))}
        onDone={() => { refresh(); console.info('accounts.login_wizard.done') }}
        onNotice={(text) => { const name = wizard.account?.name ?? ''; if (name) note(name, text); else setNotes((cur) => ({ ...cur, __page: text })) }} />
      {notes.__page && wizard.open === false && <p className="a-note">{notes.__page}</p>}
    </div>
  )
}
