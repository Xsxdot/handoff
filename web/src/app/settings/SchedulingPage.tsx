// SchedulingPage.tsx —— 自动化编制展示与 CAS 编辑分区。
// 职责：展示服务端的载体/小队快照，并通过 scheduling API 保存登记草稿。
// 边界：只触发目标机探测/检测 API，不在浏览器发现机器、写 flow 或决定协调者选择规则。
import { useEffect, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import type { CarrierInput, CarrierStatus, CarrierView, HomeProbeResp, SquadInput, SquadMember, SquadView, SquadsResp } from '../../api/scheduling'
import { CARRIER_STATUS_LABEL, defaultHomeDir, detectCarrier, getCarrierRunCommand, getSquads, probeHome, putCarrier, putSquad } from '../../api/scheduling'
import { copyToClipboard } from '../lib/clipboard'
import { errorMessage } from '../lib/format'

type CarrierDraft = Omit<CarrierInput, 'max_concurrency'> & { name: string; maxConcurrencyText: string; homeAuto: boolean }
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
const MACHINE_OPTIONS = ['本机', 'mac-02', 'win-b37', 'linux-01']
const CLI_OPTIONS = ['opencode', 'claude', 'codex', 'grok']
const CREDENTIAL_OPTIONS = [
  { value: 'standalone', label: '独立账号' },
  { value: 'main_home_sync', label: '主 HOME 同步' },
]

// 空值和 0 都表示“不限”，服务端 wire 用字段缺席表达这个状态；正数才进入 CAS body。
function optionalConcurrency(raw: string): number | undefined {
  const value = raw.trim()
  if (value === '') return undefined
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

function carrierDraft(row: CarrierView | null): CarrierDraft {
  const name = row?.name ?? ''
  const home = row?.home_dir ?? defaultHomeDir(name)
  return {
    name,
    machine: row?.machine ?? '本机',
    cli: row?.cli ?? 'opencode',
    home_dir: home,
    model: row?.model ?? '',
    credential: row?.credential ?? 'standalone',
    maxConcurrencyText: row?.max_concurrency?.toString() ?? '',
    homeAuto: row === null || home === defaultHomeDir(name),
  }
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
export type SchedulingPageProps = Record<string, never>

/** 渲染载体、小队快照和 CAS 编辑弹窗；保存不检测，保存后的检测是第二次请求。 */
export function SchedulingPage(props: SchedulingPageProps = {}): ReactElement {
  void props
  // 空快照先占位，保证操作入口在首个网络响应前也可见；响应到达后替换为服务端真值。
  const [snapshot, setSnapshot] = useState<SquadsResp>({ carriers: [], squads: [] })
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(true)
  const [dialog, setDialog] = useState<EntityDialog>(null)
  const [draft, setDraft] = useState<CarrierDraft | SquadDraft | null>(null)
  const [saveError, setSaveError] = useState('')
  const [saving, setSaving] = useState(false)
  const [probe, setProbe] = useState<HomeProbeResp | null>(null)
  const [probeError, setProbeError] = useState('')
  const [probing, setProbing] = useState(false)
  const [detecting, setDetecting] = useState('')
  const [detectError, setDetectError] = useState<Record<string, string>>({})
  const [runState, setRunState] = useState({ name: '', message: '', error: '' })
  const probeSequence = useRef(0)

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

  const openCarrier = (row: CarrierView | null): void => {
    setDialog({ kind: 'carrier', value: row })
    setDraft(carrierDraft(row))
    setSaveError('')
    setProbe(null)
    setProbeError('')
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
      setProbe(null)
      setProbeError('')
    }
  }

  // 探测请求只消费当前 draft；序号使旧响应不能覆盖用户刚改过的 HOME/CLI/机器。
  // `~` 仍原样交给目标机解释，页面不展开路径。
  useEffect(() => {
    if (dialog?.kind !== 'carrier' || draft === null || !('home_dir' in draft)) return
    const carrier = draft as CarrierDraft
    if (carrier.home_dir === '' || carrier.cli === '') {
      setProbe(null)
      setProbeError('')
      return
    }
    const sequence = ++probeSequence.current
    setProbing(true)
    setProbe(null)
    setProbeError('')
    const timer = window.setTimeout(() => {
      const started = performance.now()
      console.info({ event: 'scheduling.probe.start', cli: carrier.cli, machine: carrier.machine, path: carrier.home_dir })
      void probeHome({
        cli: carrier.cli,
        path: carrier.home_dir,
        credential: carrier.credential,
        machine: carrier.machine,
      }).then((result) => {
        if (sequence !== probeSequence.current) return
        console.info({ event: 'scheduling.probe.success', cli: carrier.cli, machine: carrier.machine, path: carrier.home_dir, kind: result.kind, elapsed: performance.now() - started })
        setProbe(result)
      }).catch((cause: unknown) => {
        if (sequence !== probeSequence.current) return
        console.error({ event: 'scheduling.probe.failure', cli: carrier.cli, machine: carrier.machine, path: carrier.home_dir, elapsed: performance.now() - started, cause })
        setProbeError(errorMessage(cause))
      }).finally(() => {
        if (sequence === probeSequence.current) setProbing(false)
      })
    }, 0)
    return () => window.clearTimeout(timer)
  }, [dialog?.kind, draft && 'home_dir' in draft ? draft.home_dir : '', draft && 'cli' in draft ? draft.cli : '', draft && 'credential' in draft ? draft.credential : '', draft && 'machine' in draft ? draft.machine : ''])

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

  const save = async (): Promise<void> => {
    if (dialog === null || draft === null) return
    const name = draftName(dialog, draft)
    if (name === '') {
      console.warn('scheduling.save.validation', { kind: dialog.kind, name, reason: 'missing_name' })
      setSaveError('名称不能为空')
      return
    }
    const expect = dialog.value?.version ?? 0 // CAS 必须使用打开弹窗时的行快照版本。
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
        const homeChanged = previous !== null && previous.home_dir !== carrier.home_dir
        const result = await putCarrier(name, expect, {
          name,
          machine: carrier.machine,
          cli: carrier.cli,
          home_dir: carrier.home_dir,
          model: carrier.model?.trim() || undefined,
          credential: carrier.credential,
          ...(optionalConcurrency(carrier.maxConcurrencyText) === undefined ? {} : { max_concurrency: optionalConcurrency(carrier.maxConcurrencyText) }),
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
    setDraft((current) => {
      if (current === null || !('homeAuto' in current)) return current
      const carrier = current as CarrierDraft
      const followsDefault = carrier.homeAuto && carrier.home_dir === defaultHomeDir(carrier.name)
      return { ...carrier, name, home_dir: followsDefault ? defaultHomeDir(name) : carrier.home_dir, homeAuto: followsDefault }
    })
  }

  return (
    <div className="flex min-h-full flex-col gap-4 p-4" aria-busy={loading}>
      <div>
        <h2 className="text-sm font-semibold">自动化</h2>
        <p className="mt-1 max-w-3xl text-xs leading-5 text-muted-foreground">编制 = 自动化层的「谁来跑、能同时跑几个」。载体是物理承载：一台机器上一个可领活的 CLI 档案（HOME × 模型 × 凭据 × 并发上限）；小队是角色与并发政策：工作流节点绑小队，点火时由小队解析出具体载体。准入 = 小队有位 且 载体有位；抢并发时协调者优先（只在准入排序生效，不抢占在跑任务）。保存走 CAS：编辑前读到哪个版本，保存就钉哪个版本，期间被别人改过会拒收并要求刷新。</p>
        {loadError && <p role="alert" className="mt-2 text-xs text-destructive">读取失败：{loadError} <button type="button" className="underline" onClick={() => void load()}>重试</button></p>}
      </div>

      <section className="space-y-2">
        <div className="flex items-center gap-2"><h3 className="text-xs font-semibold">载体</h3><span className="text-[11px] text-muted-foreground">并发上限是物理位：跨小队全局计数</span><span className="flex-1" /><button type="button" className="rounded-md border px-2.5 py-1 text-xs" onClick={() => openCarrier(null)}>登记载体</button></div>
        {snapshot.carriers.map((row) => { const status = carrierStatus(row); return <article key={row.name} className="rounded-lg border p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs"><strong className="font-mono">{row.name}</strong><span className="rounded-full bg-muted px-2 py-0.5" data-status={status}>{CARRIER_STATUS_LABEL[status]}</span><span>{row.machine} · {row.cli}</span><span className="flex-1" /><span>在跑 — / {row.max_concurrency ?? '不限'} · v{row.version}</span><button type="button" className="rounded border px-2 py-1" onClick={() => openCarrier(row)}>编辑 {row.name}</button><button type="button" className="rounded border px-2 py-1" disabled={detecting === row.name} onClick={() => void detect(row.name)}>{detecting === row.name ? '检测中…' : '检测'}</button><button type="button" className="rounded border px-2 py-1" onClick={() => void runCarrier(row.name)}>运行</button></div>
          <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs"><dt className="text-muted-foreground">HOME 档案</dt><dd className="font-mono">{row.home_dir}</dd><dt className="text-muted-foreground">模型</dt><dd>{row.model || <span className="text-muted-foreground">CLI 默认</span>}</dd><dt className="text-muted-foreground">凭据来源</dt><dd>{row.credential}</dd><dt className="text-muted-foreground">并发上限</dt><dd>{row.max_concurrency ?? '不限'}</dd></dl>
          {row.last_error && <p className="mt-2 text-xs text-destructive">最近检测：{row.last_error}</p>}
          {detectError[row.name] && <p role="alert" className="mt-2 text-xs text-destructive">检测失败：{detectError[row.name]}；请修正 HOME/登录后重试。</p>}
          {runState.name === row.name && (runState.message || runState.error) && <p role={runState.error ? 'alert' : undefined} className="mt-2 text-xs">{runState.message || `复制运行命令失败：${runState.error}`}</p>}
        </article> })}
        {snapshot.carriers.length === 0 && <p className="rounded-lg border border-dashed p-4 text-xs text-muted-foreground">尚未登记载体，请先登记一个可用 CLI 档案。</p>}
      </section>

      <section className="space-y-2">
        <div className="flex items-center gap-2"><h3 className="text-xs font-semibold">小队</h3><span className="text-[11px] text-muted-foreground">并发政策位按成员载体计数</span><span className="flex-1" /><button type="button" className="rounded-md border px-2.5 py-1 text-xs" onClick={() => openSquad(null)}>建小队</button></div>
        {snapshot.squads.map((row) => <article key={row.name} className="rounded-lg border p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs"><strong className="font-mono">{row.name}</strong><span className="rounded-full bg-muted px-2 py-0.5">{row.role === 'coordinator' ? '协调者队' : '执行者队'}</span><span className="flex-1" /><span>成员政策 · v{row.version}</span><button type="button" className="rounded border px-2 py-1" onClick={() => openSquad(row)}>编辑</button></div>
          <div className="mt-2 flex flex-wrap gap-1.5">{row.members.length > 0 ? row.members.map((member) => <span key={member.carrier} className="rounded border px-2 py-1 text-xs" title={member.carrier}>成员：{member.carrier}{member.max_concurrency ? `/${member.max_concurrency}` : ''}</span>) : <span className="text-xs text-muted-foreground">空队合法：先建队再补成员</span>}</div>
          <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs"><dt className="text-muted-foreground">并发政策</dt><dd>每个成员载体独立设置；空缺或 0 = 不限</dd><dt className="text-muted-foreground">绑定对象</dt><dd>{row.role === 'coordinator' ? '拉起通道（坐下 / 叫机器人）' : '工作流派发节点（flows 页配置）'}</dd></dl>
        </article>)}
        {snapshot.squads.length === 0 && <p className="rounded-lg border border-dashed p-4 text-xs text-muted-foreground">尚未登记小队，请创建 executor 或 coordinator 小队。</p>}
        <p className="text-[11px] text-muted-foreground">协调者队成员必须落在协调机；执行者队成员可以是任何执行机。</p>
      </section>

      {dialog !== null && draft !== null && <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/30 p-4" role="dialog" aria-modal="true" aria-labelledby="scheduling-dialog-title">
        <form className={`w-full space-y-4 rounded-lg border bg-background p-5 shadow-lg ${dialog.kind === 'carrier' ? 'max-w-[480px]' : 'max-w-[440px]'}`} onSubmit={(event) => { event.preventDefault(); void save() }}>
          <div><h3 id="scheduling-dialog-title" className="text-sm font-semibold">{dialog.kind === 'carrier' ? (dialog.value ? `编辑载体 · ${dialog.value.name}（v${dialog.value.version}）` : '登记载体') : (dialog.value ? `编辑小队 · ${dialog.value.name}（v${dialog.value.version}）` : '建小队')}</h3></div>
          {dialog.kind === 'carrier' ? <div className="grid gap-x-3 gap-y-2.5 sm:grid-cols-2">
            <div><label htmlFor="sched-carrier-name" className={FIELD_LABEL}>载体名（唯一，登记后不可改）</label><input id="sched-carrier-name" aria-label="载体名" className={INPUT} readOnly={dialog.value !== null} value={draft.name} onChange={(event) => updateCarrierName(event.target.value)} /></div>
            <div><label htmlFor="sched-carrier-machine" className={FIELD_LABEL}>机器</label><select id="sched-carrier-machine" aria-label="机器" className={INPUT} value={(draft as CarrierDraft).machine} onChange={(event) => updateDraft({ machine: event.target.value })}>{(draft as CarrierDraft).machine && !MACHINE_OPTIONS.includes((draft as CarrierDraft).machine) && <option value={(draft as CarrierDraft).machine}>{(draft as CarrierDraft).machine}</option>}{MACHINE_OPTIONS.map((machine) => <option key={machine} value={machine}>{machine}</option>)}</select></div>
            <div><label htmlFor="sched-carrier-cli" className={FIELD_LABEL}>CLI</label><select id="sched-carrier-cli" aria-label="CLI" className={INPUT} value={(draft as CarrierDraft).cli} onChange={(event) => updateDraft({ cli: event.target.value })}>{CLI_OPTIONS.map((cli) => <option key={cli} value={cli}>{cli}</option>)}</select></div>
            <div><label htmlFor="sched-carrier-model" className={FIELD_LABEL}>模型（留空 = CLI 默认）</label><input id="sched-carrier-model" aria-label="模型" className={INPUT} value={(draft as CarrierDraft).model ?? ''} onChange={(event) => updateDraft({ model: event.target.value })} /></div>
            <div className="sm:col-span-2"><label htmlFor="sched-carrier-home" className={FIELD_LABEL}>HOME 档案（隔离 HOME 路径；协调者 = 全套，执行者 = 干净会话）</label><input id="sched-carrier-home" aria-label="HOME 档案" className={INPUT} value={(draft as CarrierDraft).home_dir} onChange={(event) => updateDraft({ home_dir: event.target.value, homeAuto: false })} /></div>
            <div><label htmlFor="sched-carrier-cred" className={FIELD_LABEL}>凭据来源</label><select id="sched-carrier-cred" aria-label="凭据来源" className={INPUT} value={(draft as CarrierDraft).credential} onChange={(event) => updateDraft({ credential: event.target.value })}>{CREDENTIAL_OPTIONS.map((credential) => <option key={credential.value} value={credential.value}>{credential.label}</option>)}</select></div>
            <div><label htmlFor="sched-carrier-conc" className={FIELD_LABEL}>并发上限（0 / 留空 = 不限）</label><input id="sched-carrier-conc" aria-label="并发上限" className={INPUT} type="number" min="0" value={(draft as CarrierDraft).maxConcurrencyText} onChange={(event) => updateDraft({ maxConcurrencyText: event.target.value })} /></div>
            <p className="text-[11px] leading-5 text-muted-foreground sm:col-span-2">主 HOME 同步 = 把主环境的认证态搬进隔离 HOME；两个同账户载体的真实限额共享，跨载体账户池的归属归 roadmap 的「限额探测」。</p>
            {probing && <p className="text-[11px] text-muted-foreground sm:col-span-2">正在检测目标机 HOME…</p>}
            {probe && <p className="text-[11px] sm:col-span-2">{probe.kind === 'empty' ? '目标 HOME 为空，检测时可创建。' : probe.kind === 'logged_in' ? '已发现该 CLI 凭据。' : '目录非空但未见该 CLI 凭据，不会覆盖已有文件。'}{probe.detail ? ` ${probe.detail}` : ''}</p>}
            {probeError && <p role="alert" className="text-[11px] text-destructive sm:col-span-2">HOME 检测失败：{probeError}</p>}
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
