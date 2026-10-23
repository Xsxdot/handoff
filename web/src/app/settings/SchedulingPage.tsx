// SchedulingPage.tsx —— 自动化编制展示与 CAS 编辑分区。
// 职责：展示服务端的载体/小队快照，并通过 scheduling API 保存登记草稿。
// 边界：只触发目标机探测/检测 API，不在浏览器发现机器、写 flow 或决定协调者选择规则。
// B453 S4 / B464 段 1：载体选账号登录（只列同机登录）或「无账号 · 主 HOME」，二选一；HOME 由
// 服务端按所选登录镜像（本页只读显示）。PUT 是整体替换，必须回传所选登录。账号数据读 Shell 级
// 共享快照（accounts/store），本页不另拉。B464 段 1 只做与新 wire 对齐的最小改动，对话框按原型
// 重做属后续段。
import { useEffect, useState } from 'react'
import type { ReactElement } from 'react'
import { ChevronRight } from 'lucide-react'
import type { CarrierInput, CarrierStatus, CarrierView, SquadInput, SquadMember, SquadView, SquadsResp } from '../../api/scheduling'
import { fetchMachines } from '../../api/client'
import { CARRIER_STATUS_LABEL, deleteCarrier, detectCarrier, getCarrierRunCommand, getSquads, putCarrier, putSquad } from '../../api/scheduling'
import { copyToClipboard } from '../lib/clipboard'
import { errorMessage } from '../lib/format'
import type { AccountView, LoginView } from '../../api/accounts'
import { dispatchHint, homeText, kindMeta, loginCommandText, loginStateLabel, sameMachine, supportsProbe } from '../accounts/model'
import { KindIcon, StatusText, Summary } from '../accounts/QuotaBits'
import { useAccounts, useNow } from '../accounts/store'
import '../accounts/accounts.css'

// login 三态（B464 C1）：登录 id / NO_LOGIN（「无账号 · 主 HOME」哨兵，同原型 NO_LOGIN 形）/
// 空串（存量未选，未选不得提交）。home_dir 只是所选登录 HOME 的只读回显（服务端写镜像）。
const NO_LOGIN = 'none'
type CarrierDraft = Omit<CarrierInput, 'max_concurrency' | 'login' | 'no_account'> & { name: string; maxConcurrencyText: string; login: string }
type SquadDraft = Omit<SquadInput, 'name'> & {
  name: string
  memberConcurrencyText: Record<string, string>
}
type EntityDialog =
  | { kind: 'carrier'; value: CarrierView | null }
  | { kind: 'squad'; value: SquadView | null }
  | null

// 弹窗控件：撑满所在栅格（原型 .input 为整宽，B287 返修对齐 form-grid 布局）。
const INPUT = 'h-8 w-full rounded-md border bg-background px-2 text-xs'
// 原型 .form-grid label：块级、12px、muted、下缘 4px——标签在控件上方。
const FIELD_LABEL = 'mb-1 block text-xs text-muted-foreground'
const CLI_OPTIONS = ['opencode', 'claude', 'codex', 'grok', 'agy']

// 空值和 0 都表示“不限”，服务端 wire 用字段缺席表达这个状态；正数才进入 CAS body。
function optionalConcurrency(raw: string): number | undefined {
  const value = raw.trim()
  if (value === '') return undefined
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

function carrierDraft(row: CarrierView | null): CarrierDraft {
  return {
    name: row?.name ?? '',
    machine: row?.machine ?? '本机',
    cli: row?.cli ?? 'opencode',
    model: row?.model ?? '',
    maxConcurrencyText: row?.max_concurrency?.toString() ?? '',
    login: row ? (row.no_account ? NO_LOGIN : row.login ?? '') : '',
  }
}

/** 执行器 → 兼容账号 kind（载体下拉只列这些登录；服务端仍是权威校验）。 */
const COMPATIBLE: Record<string, string[]> = {
  opencode: ['opencode-go', 'apikey'],
  codex: ['codex'],
  grok: ['grok'],
  agy: ['agy'],
  claude: ['claude'],
}

/**
 * CarrierAccount 载体详情里的「账号」一栏（S6）：账号名 · 机器 HOME（主 HOME 显示 ~），
 * 登录状态与派发提示；已失效暂停、已过期只标红，未选登录照常派发并提示去选。
 */
function CarrierAccount({ row, accounts }: { row: CarrierView; accounts: AccountView[] | null }) {
  const now = useNow()
  const [note, setNote] = useState('')
  if (row.no_account) return <span className="text-muted-foreground">无账号 · 主 HOME（无凭据）</span>
  if (!row.login) return <span className="text-muted-foreground">未选登录 · 照常派发，建议去「账号与额度」选一条</span>
  const a = accounts?.find((x) => x.id === row.account_id)
  const login = a?.logins.find((l) => l.id === row.login)
  const hint = a && login ? dispatchHint(a, login, now) : null
  const name = row.account_name ?? row.login
  const copy = async () => {
    const text = loginCommandText(login?.relogin)
    if (!text) return
    const ok = await copyToClipboard(text)
    console.info({ event: 'scheduling.relogin.copy', login: row.login, copied: ok })
    setNote(ok ? `已复制登录命令：${text}` : `请手动复制：${text}`)
  }
  return (
    <span className="acct-scope">
      <span className="acct-ref">
        {a && <KindIcon kind={a.kind} />}
        <span className="font-mono">{name}</span>
        {a && <span className="k">{kindMeta(a.kind).name}</span>}
        <span className="k">· {a ? login?.machine ?? row.machine : row.machine} {login ? homeText(login.home) : ''}</span>
        {login && <span className={`st ${login.state === 'ok' ? 'q-mute' : 'q-out'}`}>{loginStateLabel(login.state)}</span>}
        {a && <span className="st"><Summary a={a} now={now} order="lead" /></span>}
      </span>
      {hint && <span className={`block ${hint.tone}`}>{hint.text}</span>}
      {hint?.relogin && login?.relogin && <button type="button" className="tbtn" onClick={() => void copy()}>复制登录命令</button>}
      {note && <span className="block text-muted-foreground">{note}</span>}
    </span>
  )
}

function machineOptionNames(machines: { name: string }[]): string[] {
  const names = ['本机']
  for (const machine of machines) {
    if (machine.name !== '' && machine.name !== '本机' && machine.name !== 'local' && !names.includes(machine.name)) {
      names.push(machine.name)
    }
  }
  return names
}

function squadDraft(row: SquadView | null): SquadDraft {
  const members = row ? row.members.map((member) => ({ ...member })) : []
  const memberConcurrencyText: Record<string, string> = {}
  for (const member of members) {
    memberConcurrencyText[member.carrier] = member.max_concurrency?.toString() ?? ''
  }
  return {
    name: row?.name ?? '',
    role: row?.role === 'coordinator' ? 'coordinator' : 'executor',
    members,
    memberConcurrencyText,
  }
}

// 保留输入文本直到保存，避免浏览器把非法值先转成 0 或 NaN；空串才是不限。
function parseSquadPolicy(raw: string): number | undefined {
  if (raw === '') return undefined
  if (!/^[1-9][0-9]*$/.test(raw)) {
    throw new Error('小队成员政策必须是正整数；留空表示不限；合法示例：2')
  }
  const value = Number(raw)
  if (!Number.isSafeInteger(value)) {
    throw new Error('小队成员政策超出安全整数范围；请使用较小的正整数或留空表示不限')
  }
  return value
}

// 参数是当前小队草稿；返回只包含已勾选成员的 wire 对象，政策空值省略 max_concurrency。
function squadMembersForSave(draft: SquadDraft): SquadMember[] {
  return draft.members.map((member) => {
    const max = parseSquadPolicy(draft.memberConcurrencyText[member.carrier] ?? '')
    return max === undefined ? { carrier: member.carrier } : {
      carrier: member.carrier,
      max_concurrency: max,
    }
  })
}

function draftName(dialog: EntityDialog, draft: CarrierDraft | SquadDraft): string {
  return draft.name.trim() || (dialog?.value?.name ?? '')
}

function carrierStatus(row: CarrierView): CarrierStatus {
  return typeof row.status === 'string' && Object.prototype.hasOwnProperty.call(CARRIER_STATUS_LABEL, row.status)
    ? row.status
    : 'pending'
}

/** Props 为空；返回设置页的自动化编制区；保存失败时保留草稿和弹窗供处理。 */
export interface SchedulingPageProps { compact?: boolean }

/** 渲染载体、小队快照和 CAS 编辑弹窗；保存不检测，保存后的检测是第二次请求。 */
export function SchedulingPage({ compact = false }: SchedulingPageProps = {}): ReactElement {
  // 空快照先占位，保证操作入口在首个网络响应前也可见；响应到达后替换为服务端真值。
  const [snapshot, setSnapshot] = useState<SquadsResp>({ carriers: [], squads: [], running: [] })
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(true)
  const [dialog, setDialog] = useState<EntityDialog>(null)
  const [draft, setDraft] = useState<CarrierDraft | SquadDraft | null>(null)
  const [saveError, setSaveError] = useState('')
  const [saving, setSaving] = useState(false)
  const [detecting, setDetecting] = useState('')
  const [detectError, setDetectError] = useState<Record<string, string>>({})
  const [deleting, setDeleting] = useState('')
  const [deleteError, setDeleteError] = useState<Record<string, string>>({})
  const [runState, setRunState] = useState({ name: '', message: '', error: '' })
  const [machineOptions, setMachineOptions] = useState<string[]>(['本机'])
  const [expandedCarriers, setExpandedCarriers] = useState<Set<string>>(() => new Set())
  const [expandedSquads, setExpandedSquads] = useState<Set<string>>(() => new Set())
  const [compactSection, setCompactSection] = useState<'carriers' | 'squads'>('carriers')
  const { accounts, discovery } = useAccounts()
  // 已登记远端机名（machineOptions 去掉本机别名）：同机判据用它推断「本机」的账号。
  const remoteNames = machineOptions.filter((m) => m !== '本机')
  // loginsOn 这台机器上与该执行器兼容的登录（连同所属账号）；载体只能选同机兼容登录
  // （服务端仍是权威校验）。cli 空 = 不限执行器（切换机器时用于判断原选择是否还可用）。
  const loginsOn = (machine: string, cli = ''): { account: AccountView; login: LoginView }[] =>
    (accounts ?? []).flatMap((account) => account.logins
      .filter((l) => sameMachine(l.machine, machine, remoteNames) && (cli === '' || (COMPATIBLE[cli] ?? []).includes(account.kind)))
      .map((login) => ({ account, login })))

  const load = async (): Promise<void> => {
    console.info({ event: 'scheduling.load.start', scope: 'settings' })
    setLoading(true)
    setLoadError('')
    try {
      const result = await getSquads()
      console.info({ event: 'scheduling.load.success', scope: 'settings', carriers: result.carriers.length, squads: result.squads.length })
      setSnapshot(result)
    } catch (cause) {
      console.error({ event: 'scheduling.load.failure', scope: 'settings', cause })
      setLoadError(errorMessage(cause))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  useEffect(() => {
    void fetchMachines().then((resp) => {
      setMachineOptions(machineOptionNames(resp.machines))
    }).catch((cause: unknown) => {
      console.error({ event: 'scheduling.machines.failure', cause })
    })
  }, [])

  const openCarrier = (row: CarrierView | null): void => {
    setDialog({ kind: 'carrier', value: row })
    const base = carrierDraft(row)
    if (row === null) {
      // 新建缺省第一条兼容登录；这台机器没有兼容登录才缺省「无账号 · 主 HOME」（原型 settings.html:595）。
      base.login = loginsOn(base.machine, base.cli)[0]?.login.id ?? NO_LOGIN
    }
    setDraft(base)
    setSaveError('')
  }

  const openSquad = (row: SquadView | null): void => {
    setDialog({ kind: 'squad', value: row })
    setDraft(squadDraft(row))
    setSaveError('')
  }

  const closeDialog = (): void => {
    if (!saving) {
      setDialog(null)
      setDraft(null)
      setSaveError('')
    }
  }

  const detect = async (name: string): Promise<void> => {
    setDetecting(name)
    setDetectError((current) => ({ ...current, [name]: '' }))
    const started = performance.now()
    console.info({ event: 'scheduling.detect.start', name })
    try {
      await detectCarrier(name)
      console.info({ event: 'scheduling.detect.success', name, elapsed: performance.now() - started })
      await load()
    } catch (cause) {
      const message = errorMessage(cause)
      console.error({ event: 'scheduling.detect.failure', name, elapsed: performance.now() - started, cause })
      setDetectError((current) => ({ ...current, [name]: message }))
    } finally {
      setDetecting('')
    }
  }

  const runCarrier = async (name: string): Promise<void> => {
    setRunState({ name, message: '', error: '' })
    const started = performance.now()
    console.info({ event: 'scheduling.run.start', name })
    try {
      const result = await getCarrierRunCommand(name)
      const ok = await copyToClipboard(result.command)
      if (!ok) throw new Error('复制到剪贴板失败，请手动复制：' + result.command)
      console.info({ event: 'scheduling.run.success', name, elapsed: performance.now() - started })
      setRunState({ name, message: '运行命令已复制', error: '' })
    } catch (cause) {
      console.error({ event: 'scheduling.run.failure', name, elapsed: performance.now() - started, cause })
      setRunState({ name, message: '', error: errorMessage(cause) })
    }
  }

  const removeCarrier = async (row: CarrierView): Promise<void> => {
    setDeleting(row.name)
    setDeleteError((current) => ({ ...current, [row.name]: '' }))
    const started = performance.now()
    console.info({ event: 'scheduling.delete.start', name: row.name, version: row.version })
    try {
      await deleteCarrier(row.name, row.version)
      console.info({ event: 'scheduling.delete.success', name: row.name, version: row.version, elapsed: performance.now() - started })
      await load()
    } catch (cause) {
      const message = errorMessage(cause)
      console.error({ event: 'scheduling.delete.failure', name: row.name, version: row.version, elapsed: performance.now() - started, cause })
      setDeleteError((current) => ({ ...current, [row.name]: message }))
    } finally {
      setDeleting('')
    }
  }

  const save = async (): Promise<void> => {
    if (dialog === null || draft === null) return
    const name = draftName(dialog, draft)
    if (name === '') {
      console.warn('scheduling.save.validation', { kind: dialog.kind, name, reason: 'missing_name' })
      setSaveError('名称不能为空')
      return
    }
    const expect = dialog.value?.version ?? 0 // CAS 必须使用打开弹窗时的行快照版本。
    if (dialog.kind === 'carrier' && (draft as CarrierDraft).login === '') {
      console.warn('scheduling.save.validation', { kind: dialog.kind, name, reason: 'login_not_selected' })
      setSaveError('请先选择一条登录（或「无账号 · 主 HOME」）再保存；未选不会提交。')
      return
    }
    let squadMembers: SquadMember[] | undefined
    if (dialog.kind === 'squad') {
      try {
        squadMembers = squadMembersForSave(draft as SquadDraft)
      } catch (cause) {
        const message = errorMessage(cause)
        console.warn('scheduling.save.validation', {
          kind: dialog.kind, name, expect, member_count: (draft as SquadDraft).members.length, cause,
        })
        setSaveError(message)
        return
      }
    }
    setSaving(true)
    setSaveError('')
    try {
      if (dialog.kind === 'carrier') {
        const carrier = draft as CarrierDraft
        const previous = dialog.value
        // HOME 变才检测：先比登录 id——同一条登录 = 服务端镜像的 home_dir 不会变（存量行的
        // 绝对路径/`~` 写法与登录的 `~/.handoff/home/...` 写法可能不同，比 home_dir 会误判）；
        // 登录变了或从未选到选了，再比上一份回执的 home_dir 与本次所选登录的 HOME。
        const selectedLogin = carrier.login === NO_LOGIN
          ? undefined
          : (accounts ?? []).flatMap((a) => a.logins).find((l) => l.id === carrier.login)
        const nextHome = carrier.login === NO_LOGIN ? '' : selectedLogin ? (selectedLogin.home === '~' ? '' : selectedLogin.home) : (previous?.home_dir ?? '')
        // 三态比较：上一份回执「无账号」记 NO_LOGIN、存量未选记 ''、其余记登录 id。
        // 未选（''）不等于无账号；折叠成同一值会把「未选 → 无账号」误判为登录未变、不检测。
        const prevLogin = previous === null ? '' : previous.no_account ? NO_LOGIN : (previous.login ?? '')
        const sameLogin = prevLogin !== '' && prevLogin === carrier.login
        const homeChanged = previous !== null && !sameLogin && (previous.home_dir ?? '') !== nextHome
        const result = await putCarrier(name, expect, {
          name,
          machine: carrier.machine,
          cli: carrier.cli,
          model: carrier.model?.trim() || undefined,
          ...(optionalConcurrency(carrier.maxConcurrencyText) === undefined ? {} : { max_concurrency: optionalConcurrency(carrier.maxConcurrencyText) }),
          ...(carrier.login === NO_LOGIN ? { no_account: true } : { login: carrier.login }),
        })
        console.info({ event: 'scheduling.save.success', kind: 'carrier', name, version: result.version })
        if (previous === null || homeChanged) {
          const detectStarted = performance.now()
          console.info({ event: 'scheduling.detect.start', name })
          try {
            const detected = await detectCarrier(name)
            console.info({ event: 'scheduling.detect.success', name, status: detected.status, elapsed: performance.now() - detectStarted })
          } catch (cause) {
            const message = errorMessage(cause)
            console.error({ event: 'scheduling.detect.failure', name, elapsed: performance.now() - detectStarted, cause })
            setSaveError(`保存成功，但检测失败：${message}`)
            await load()
            return
          }
        }
      } else {
        const squad = draft as SquadDraft
        const result = await putSquad(name, expect, {
          name,
          role: squad.role === 'coordinator' ? 'coordinator' : 'executor',
          members: squadMembers ?? [],
        })
        console.info({
          event: 'scheduling.save.success', kind: 'squad', name, version: result.version,
          member_count: squadMembers?.length ?? 0,
          policy_count: squadMembers?.filter((member) => member.max_concurrency !== undefined).length ?? 0,
        })
      }
      setDialog(null)
      setDraft(null)
      await load()
    } catch (cause) {
      const message = errorMessage(cause)
      if (message.includes('409') || message.includes('冲突')) {
        console.warn({
          event: 'scheduling.save.conflict', kind: dialog.kind, name, expect,
          member_count: dialog.kind === 'squad' ? (draft as SquadDraft).members.length : 0, cause,
        })
      } else {
        console.error({
          event: 'scheduling.save.failure', kind: dialog.kind, name, expect,
          member_count: dialog.kind === 'squad' ? (draft as SquadDraft).members.length : 0, cause,
        })
      }
      setSaveError(message)
    } finally {
      setSaving(false)
    }
  }

  const updateDraft = (next: Partial<CarrierDraft | SquadDraft>): void => {
    setDraft((current) => current === null ? current : { ...current, ...next } as CarrierDraft | SquadDraft)
  }

  const updateCarrierName = (name: string): void => {
    setDraft((current) => current === null || !('cli' in current) ? current : { ...current, name })
  }

  const toggleExpanded = (setter: typeof setExpandedCarriers, name: string): void => {
    setter((previous) => {
      const next = new Set(previous)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  return (
    <div data-testid="automation-page" className={`flex min-h-full flex-col ${compact ? 'gap-3 p-4 mobile-automation' : 'gap-4 p-4'}`} aria-busy={loading}>
      <div>
        {!compact && <h2 className="text-sm font-semibold">自动化</h2>}
        {compact ? <details className="mobile-automation-help"><summary>自动化说明</summary><p className="mt-2 max-w-3xl text-xs leading-5 text-muted-foreground">编制 = 自动化层的「谁来跑、能同时跑几个」。载体是物理承载：一台机器上一个可领活的 CLI 档案（HOME × 模型 × 凭据 × 并发上限）；小队是角色与并发政策：工作流节点绑小队，点火时由小队解析出具体载体。准入 = 小队有位 且 载体有位；抢并发时协调者优先（只在准入排序生效，不抢占在跑任务）。保存走 CAS：编辑前读到哪个版本，保存就钉哪个版本，期间被别人改过会拒收并要求刷新。</p></details> : <p className="mt-1 max-w-3xl text-xs leading-5 text-muted-foreground">编制 = 自动化层的「谁来跑、能同时跑几个」。载体是物理承载：一台机器上一个可领活的 CLI 档案（HOME × 模型 × 凭据 × 并发上限）；小队是角色与并发政策：工作流节点绑小队，点火时由小队解析出具体载体。准入 = 小队有位 且 载体有位；抢并发时协调者优先（只在准入排序生效，不抢占在跑任务）。保存走 CAS：编辑前读到哪个版本，保存就钉哪个版本，期间被别人改过会拒收并要求刷新。</p>}
        {loadError && <p role="alert" className="mt-2 text-xs text-destructive">读取失败：{loadError} <button type="button" className="underline" onClick={() => void load()}>重试</button></p>}
      </div>

      {compact && <div role="tablist" aria-label="自动化类别" className="mobile-automation-tabs">
        <button type="button" role="tab" aria-selected={compactSection === 'carriers'} onClick={() => setCompactSection('carriers')}>载体</button>
        <button type="button" role="tab" aria-selected={compactSection === 'squads'} onClick={() => setCompactSection('squads')}>小队</button>
      </div>}
      {(!compact || compactSection === 'carriers') && <section className={compact ? 'mobile-automation-section' : 'space-y-2'}>
        <div className="flex items-center gap-2"><h3 className="text-sm font-semibold">载体</h3>{!compact && <span className="text-[11px] text-muted-foreground">并发上限是物理位：跨小队全局计数</span>}<span className="flex-1" /><button type="button" className="min-h-11 rounded-md border px-3 text-sm" onClick={() => openCarrier(null)}>登记载体</button></div>
        {snapshot.carriers.map((row) => { const status = carrierStatus(row); return <article key={row.name} className={compact ? 'mobile-automation-row' : 'rounded-lg border p-3'}>
          {compact ? <>
            <button type="button" data-testid={`automation-carrier-row-${row.name}`} aria-label={`查看载体 ${row.name}`} aria-expanded={expandedCarriers.has(row.name)} onClick={() => toggleExpanded(setExpandedCarriers, row.name)} className="mobile-automation-summary">
              <span className="mobile-automation-primary"><strong className="block truncate font-mono text-sm">{row.name}</strong><span className="block truncate text-xs text-muted-foreground">{row.machine} · {row.cli}</span></span>
              <span className="mobile-automation-trailing"><span className="text-xs" data-status={status}>{CARRIER_STATUS_LABEL[status]}</span><span className="text-[11px] text-muted-foreground">v{row.version}</span><ChevronRight aria-hidden="true" className="size-4" /></span>
            </button>
            {expandedCarriers.has(row.name) && <div className="mobile-automation-detail">
              <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs"><dt className="text-muted-foreground">模型</dt><dd>{row.model || 'CLI 默认'}</dd><dt className="text-muted-foreground">账号</dt><dd><CarrierAccount row={row} accounts={accounts} /></dd><dt className="text-muted-foreground">并发上限</dt><dd>{row.max_concurrency ?? '不限'}</dd></dl>
              {row.last_error && <p className="mt-2 text-xs text-destructive">最近检测：{row.last_error}</p>}
              {detectError[row.name] && <p role="alert" className="mt-2 text-xs text-destructive">检测失败：{detectError[row.name]}；请修正 HOME/登录后重试。</p>}
              {deleteError[row.name] && <p role="alert" className="mt-2 text-xs text-destructive">删除失败：{deleteError[row.name]}</p>}
              {runState.name === row.name && (runState.message || runState.error) && <p role={runState.error ? 'alert' : undefined} className="mt-2 text-xs">{runState.message || `复制运行命令失败：${runState.error}`}</p>}
              <details className="mobile-automation-actions"><summary aria-label={`操作 ${row.name}`}>操作</summary><div><button type="button" onClick={() => openCarrier(row)}>编辑 {row.name}</button><button type="button" disabled={detecting === row.name} onClick={() => void detect(row.name)}>{detecting === row.name ? '检测中…' : `检测 ${row.name}`}</button><button type="button" onClick={() => void runCarrier(row.name)}>运行</button><button type="button" disabled={deleting === row.name} onClick={() => void removeCarrier(row)}>{deleting === row.name ? '删除中…' : `删除 ${row.name}`}</button></div></details>
            </div>}
          </> : <>
          <div className="flex flex-wrap items-center gap-2 text-xs"><strong className="font-mono">{row.name}</strong><span className="rounded-full bg-muted px-2 py-0.5" data-status={status}>{CARRIER_STATUS_LABEL[status]}</span><span>{row.machine} · {row.cli}</span><span className="flex-1" /><span>在跑 — / {row.max_concurrency ?? '不限'} · v{row.version}</span><button type="button" className="rounded border px-2 py-1" onClick={() => openCarrier(row)}>编辑 {row.name}</button><button type="button" className="rounded border px-2 py-1" disabled={detecting === row.name} onClick={() => void detect(row.name)}>{detecting === row.name ? '检测中…' : '检测'}</button><button type="button" className="rounded border px-2 py-1" onClick={() => void runCarrier(row.name)}>运行</button><button type="button" aria-label={`删除 ${row.name}`} className="rounded border px-2 py-1 text-destructive" disabled={deleting === row.name} onClick={() => void removeCarrier(row)}>{deleting === row.name ? '删除中…' : '删除'}</button></div>
          <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs"><dt className="text-muted-foreground">模型</dt><dd>{row.model || <span className="text-muted-foreground">CLI 默认</span>}</dd><dt className="text-muted-foreground">账号</dt><dd><CarrierAccount row={row} accounts={accounts} /></dd><dt className="text-muted-foreground">并发上限</dt><dd>{row.max_concurrency ?? '不限'}</dd></dl>
          {row.last_error && <p className="mt-2 text-xs text-destructive">最近检测：{row.last_error}</p>}
          {detectError[row.name] && <p role="alert" className="mt-2 text-xs text-destructive">检测失败：{detectError[row.name]}；请修正 HOME/登录后重试。</p>}
          {deleteError[row.name] && <p role="alert" className="mt-2 text-xs text-destructive">删除失败：{deleteError[row.name]}</p>}
          {runState.name === row.name && (runState.message || runState.error) && <p role={runState.error ? 'alert' : undefined} className="mt-2 text-xs">{runState.message || `复制运行命令失败：${runState.error}`}</p>}
          </>}
        </article> })}
        {snapshot.carriers.length === 0 && <p className="rounded-lg border border-dashed p-4 text-xs text-muted-foreground">尚未登记载体，请先登记一个可用 CLI 档案。</p>}
      </section>}

      {(!compact || compactSection === 'squads') && <section className={compact ? 'mobile-automation-section' : 'space-y-2'}>
        <div className="flex items-center gap-2"><h3 className="text-sm font-semibold">小队</h3>{!compact && <span className="text-[11px] text-muted-foreground">并发政策位按成员载体计数</span>}<span className="flex-1" /><button type="button" className="min-h-11 rounded-md border px-3 text-sm" onClick={() => openSquad(null)}>建小队</button></div>
        {snapshot.squads.map((row) => <article key={row.name} className={compact ? 'mobile-automation-row' : 'rounded-lg border p-3'}>
          {compact ? <>
            <button type="button" data-testid={`automation-squad-row-${row.name}`} aria-label={`查看小队 ${row.name}`} aria-expanded={expandedSquads.has(row.name)} onClick={() => toggleExpanded(setExpandedSquads, row.name)} className="mobile-automation-summary"><span className="mobile-automation-primary"><strong className="block truncate font-mono text-sm">{row.name}</strong><span className="block truncate text-xs text-muted-foreground">{row.role === 'coordinator' ? '协调者队' : '执行者队'} · {row.members.length} 个成员</span></span><span className="mobile-automation-trailing"><span className="text-[11px] text-muted-foreground">v{row.version}</span><ChevronRight aria-hidden="true" className="size-4" /></span></button>
            {expandedSquads.has(row.name) && <div className="mobile-automation-detail"><div className="flex flex-wrap gap-2">{row.members.length > 0 ? row.members.map((member) => <span key={member.carrier} className="rounded border px-2 py-1 text-xs">{member.carrier}{member.max_concurrency ? `/${member.max_concurrency}` : ''}</span>) : <span className="text-xs text-muted-foreground">空队合法：先建队再补成员</span>}</div><dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs"><dt className="text-muted-foreground">并发政策</dt><dd>每个成员载体独立设置；空缺或 0 = 不限</dd><dt className="text-muted-foreground">绑定对象</dt><dd>{row.role === 'coordinator' ? '拉起通道（坐下 / 叫机器人）' : '工作流派发节点（flows 页配置）'}</dd></dl><button type="button" className="mt-3 min-h-11 rounded border px-3 text-sm" onClick={() => openSquad(row)}>编辑小队</button></div>}
          </> : <>
          <div className="flex flex-wrap items-center gap-2 text-xs"><strong className="font-mono">{row.name}</strong><span className="rounded-full bg-muted px-2 py-0.5">{row.role === 'coordinator' ? '协调者队' : '执行者队'}</span><span className="flex-1" /><span>成员政策 · v{row.version}</span><button type="button" className="rounded border px-2 py-1" onClick={() => openSquad(row)}>编辑</button></div>
          <div className="mt-2 flex flex-wrap gap-1.5">{row.members.length > 0 ? row.members.map((member) => <span key={member.carrier} className="rounded border px-2 py-1 text-xs" title={member.carrier}>成员：{member.carrier}{member.max_concurrency ? `/${member.max_concurrency}` : ''}</span>) : <span className="text-xs text-muted-foreground">空队合法：先建队再补成员</span>}</div>
          <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs"><dt className="text-muted-foreground">并发政策</dt><dd>每个成员载体独立设置；空缺或 0 = 不限</dd><dt className="text-muted-foreground">绑定对象</dt><dd>{row.role === 'coordinator' ? '拉起通道（坐下 / 叫机器人）' : '工作流派发节点（flows 页配置）'}</dd></dl>
          </>}
        </article>)}
        {snapshot.squads.length === 0 && <p className="rounded-lg border border-dashed p-4 text-xs text-muted-foreground">尚未登记小队，请创建 executor 或 coordinator 小队。</p>}
        <p className="text-[11px] text-muted-foreground">协调者队成员必须落在协调机；执行者队成员可以是任何执行机。</p>
      </section>}

      {dialog !== null && draft !== null && <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/30 p-4" role="dialog" aria-modal="true" aria-labelledby="scheduling-dialog-title">
        <form className={`w-full space-y-4 rounded-lg border bg-background p-5 shadow-lg ${dialog.kind === 'carrier' ? 'max-w-[480px]' : 'max-w-[440px]'}`} onSubmit={(event) => { event.preventDefault(); void save() }}>
          <div><h3 id="scheduling-dialog-title" className="text-sm font-semibold">{dialog.kind === 'carrier' ? (dialog.value ? `编辑载体 · ${dialog.value.name}（v${dialog.value.version}）` : '登记载体') : (dialog.value ? `编辑小队 · ${dialog.value.name}（v${dialog.value.version}）` : '建小队')}</h3></div>
          {dialog.kind === 'carrier' ? <div className="grid gap-x-3 gap-y-2.5 sm:grid-cols-2">
            <div><label htmlFor="sched-carrier-name" className={FIELD_LABEL}>载体名（唯一，登记后不可改）</label><input id="sched-carrier-name" aria-label="载体名" className={INPUT} readOnly={dialog.value !== null} value={draft.name} onChange={(event) => updateCarrierName(event.target.value)} /></div>
            <div><label htmlFor="sched-carrier-machine" className={FIELD_LABEL}>机器</label><select id="sched-carrier-machine" aria-label="机器" className={INPUT} value={(draft as CarrierDraft).machine} onChange={(event) => { const machine = event.target.value; const cli = (draft as CarrierDraft).cli; const cur = (draft as CarrierDraft).login; const options = loginsOn(machine, cli); const next = options.some((o) => o.login.id === cur) ? cur : (options[0]?.login.id ?? NO_LOGIN); updateDraft({ machine, login: next }) }}>{(draft as CarrierDraft).machine && !machineOptions.includes((draft as CarrierDraft).machine) && <option value={(draft as CarrierDraft).machine}>{(draft as CarrierDraft).machine}</option>}{machineOptions.map((machine) => <option key={machine} value={machine}>{machine}</option>)}</select></div>
            <div><label htmlFor="sched-carrier-cli" className={FIELD_LABEL}>CLI</label><select id="sched-carrier-cli" aria-label="CLI" className={INPUT} value={(draft as CarrierDraft).cli} onChange={(event) => { const cli = event.target.value; const options = loginsOn((draft as CarrierDraft).machine, cli); const cur = (draft as CarrierDraft).login; updateDraft({ cli, login: options.some((o) => o.login.id === cur) ? cur : (options[0]?.login.id ?? NO_LOGIN) }) }}>{CLI_OPTIONS.map((cli) => <option key={cli} value={cli}>{cli}</option>)}</select></div>
            <div><label htmlFor="sched-carrier-model" className={FIELD_LABEL}>模型（留空 = CLI 默认）</label><input id="sched-carrier-model" aria-label="模型" className={INPUT} value={(draft as CarrierDraft).model ?? ''} onChange={(event) => updateDraft({ model: event.target.value })} /></div>
            <div className="acct-scope sm:col-span-2"><label htmlFor="sched-carrier-account" className={FIELD_LABEL}>账号登录（只列这台机器上与该执行器兼容的登录；HOME / 凭据 / 代理由登录决定）</label>{(() => {
              const carrier = draft as CarrierDraft
              const options = loginsOn(carrier.machine, carrier.cli)
              const selected = options.find((o) => o.login.id === carrier.login)
              const counts: Record<string, number> = {}
              for (const o of options) counts[o.account.id] = (counts[o.account.id] ?? 0) + 1
              // 下拉里没有当前值（账号快照未到/存量未选）时保留它，不静默清掉已有选择。
              const keep = carrier.login !== '' && carrier.login !== NO_LOGIN && !selected
              const selectedLogin = selected?.login
              const others = selectedLogin ? selectedLogin.carriers.filter((c) => c !== dialog.value?.name) : []
              const hasNoCompatible = accounts !== null && options.length === 0
              const kindNames = (COMPATIBLE[carrier.cli] ?? []).map((kind) => kindMeta(kind).name).join(' / ')
              // 本机别名时用发现结果里的 self 机器名补 machine（B464 M2），新增登录向导才能预选本机。
              const linkMachine = ['', '本机', 'local'].includes(carrier.machine)
                ? (discovery.find((d) => d.self)?.machine ?? '')
                : carrier.machine
              return <>
                <select id="sched-carrier-account" aria-label="账号" className={INPUT} value={carrier.login} onChange={(event) => updateDraft({ login: event.target.value })}>
                  {carrier.login === '' && <option value="" disabled>未选登录（请选择）</option>}
                  {options.map((o) => {
                    const extra = [counts[o.account.id] > 1 ? homeText(o.login.home) : '', o.login.state !== 'ok' ? `登录${loginStateLabel(o.login.state)}` : ''].filter(Boolean).join(' · ')
                    return <option key={o.login.id} value={o.login.id}>{o.account.name}{extra ? ` — ${extra}` : ''}</option>
                  })}
                  {keep && <option value={carrier.login}>{dialog.value?.account_name ?? carrier.login}</option>}
                  <option value={NO_LOGIN}>无账号 · 主 HOME</option>
                </select>
                {hasNoCompatible && <p className="acct-pick-hint">这台机器上还没有 {carrier.cli} 登录{kindNames ? `（${kindNames}）` : ''} → <a className="underline" href={`/accounts?login=new${linkMachine ? `&machine=${encodeURIComponent(linkMachine)}` : ''}&agent=${encodeURIComponent(carrier.cli)}`}>新增登录</a></p>}
                {carrier.login === NO_LOGIN
                  ? <div className="login-info">HOME <span className="mono">~（主 HOME）</span> · 无凭据 · 代理随该机环境<br />不看额度、不看登录状态：只适合免费模型等不需要凭据的执行。</div>
                  : carrier.login === ''
                    ? <p className="acct-pick-hint">未选登录：保存前请选择一条登录（或选「无账号 · 主 HOME」）。</p>
                    : selectedLogin && selected ? <div className="login-info">
                        HOME <span className="mono">{homeText(selectedLogin.home)}</span> · 凭据 {selectedLogin.source} · 代理 {selectedLogin.proxy ? <span className="mono">{selectedLogin.proxy}</span> : '随该机环境'}<span className="from">（登录上设置）</span>
                        {others.length > 0 && <><br />与 {others.map((c) => <span key={c} className="mono">{c}</span>)} 共用这条登录</>}
                        {!supportsProbe(selected.account.kind) ? <><br />不支持额度探测：派发不看额度</> : <><br />额度 <StatusText a={selected.account} />{selected.account.state === 'ok' ? '正常' : ''}</>}
                        {selectedLogin.state === 'expired' && <><br /><span className="q-out">这条登录已过期</span>——CLI 运行时可能自行刷新；如失败请重新登录。照常派发。</>}
                        {selectedLogin.state === 'gone' && <><br /><span className="q-out">这条登录已失效</span>——重新登录之前，载体暂停派发。</>}
                      </div>
                    : <p className="acct-pick-hint">所选登录不在当前账号快照里；刷新后再保存，或重新选择。</p>}
              </>
            })()}</div>
            <div><label htmlFor="sched-carrier-conc" className={FIELD_LABEL}>并发上限（0 / 留空 = 不限）</label><input id="sched-carrier-conc" aria-label="并发上限" className={INPUT} type="number" min="0" value={(draft as CarrierDraft).maxConcurrencyText} onChange={(event) => updateDraft({ maxConcurrencyText: event.target.value })} /></div>
            <p className="text-[11px] leading-5 text-muted-foreground sm:col-span-2">载体在所选登录的 HOME 里跑，凭据不复制；同一登录可被多个载体共用，派发按账号额度判断。</p>
          </div> : <div className="grid gap-x-3 gap-y-2.5 sm:grid-cols-2">
            <div><label htmlFor="sched-squad-name" className={FIELD_LABEL}>小队名（唯一）</label><input id="sched-squad-name" aria-label="小队名" className={INPUT} readOnly={dialog.value !== null} value={draft.name} onChange={(event) => updateDraft({ name: event.target.value })} /></div>
            <div><label htmlFor="sched-squad-role" className={FIELD_LABEL}>角色（不混编）</label><select id="sched-squad-role" aria-label="角色" className={INPUT} value={(draft as SquadDraft).role} onChange={(event) => updateDraft({ role: event.target.value })}><option value="executor">执行者队</option><option value="coordinator">协调者队</option></select></div>
            <fieldset className="sm:col-span-2"><legend className={FIELD_LABEL}>成员载体（按勾选顺序解析：第一个健康且有空的载体领活）</legend><div className="mt-2 flex max-h-[140px] flex-col gap-1 overflow-y-auto rounded-md border p-2 text-xs">{snapshot.carriers.map((carrier) => {
              const squad = draft as SquadDraft
              const selected = squad.members.some((member) => member.carrier === carrier.name)
              return <div key={carrier.name} className="grid gap-1 rounded border p-2 sm:grid-cols-[minmax(0,1fr)_minmax(8rem,12rem)] sm:items-center">
                <label className="flex min-w-0 items-center gap-1.5">
                  <input type="checkbox" aria-label={`成员 ${carrier.name}`} checked={selected} onChange={(event) => {
                    const members = squad.members
                    const memberConcurrencyText = { ...squad.memberConcurrencyText }
                    updateDraft({
                      members: event.target.checked ? [...members, { carrier: carrier.name } satisfies SquadMember] : members.filter((member) => member.carrier !== carrier.name),
                      memberConcurrencyText,
                    })
                  }} />
                  <span className="font-mono">{carrier.name}</span>
                  <span className="text-muted-foreground">{carrier.machine} · {carrier.cli}</span>
                  <span className="text-muted-foreground">{carrier.model || 'CLI 默认'}</span>
                </label>
                <label>政策并发<input aria-label={`${carrier.name} 政策并发`} className={INPUT} type="text" inputMode="numeric" disabled={!selected} value={squad.memberConcurrencyText[carrier.name] ?? ''} onChange={(event) => updateDraft({ memberConcurrencyText: { ...squad.memberConcurrencyText, [carrier.name]: event.target.value } })} placeholder="留空 = 不限" /></label>
              </div>
            })}{snapshot.carriers.length === 0 && <p className="text-muted-foreground">尚未登记载体，先到上方登记一个。</p>}</div></fieldset>
            <p className="text-[11px] leading-5 text-muted-foreground sm:col-span-2">每个成员的政策上限由 wire 成员对象的 max_concurrency 表达；缺席或 0 表示不限。载体物理上限仍跨小队共享。</p>
            <p className="text-[11px] leading-5 text-muted-foreground sm:col-span-2">协调者队成员必须落在协调机；执行者队成员可以是任何执行机。</p>

          </div>}
          {saveError && <p role="alert" className="break-words text-xs text-destructive">{saveError}</p>}
          <div className="flex justify-end gap-2"><button type="button" className="rounded-md border px-3 py-1.5 text-xs" disabled={saving} onClick={closeDialog}>取消</button><button type="submit" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" disabled={saving}>保存</button></div>
        </form>
      </div>}
    </div>
  )
}
