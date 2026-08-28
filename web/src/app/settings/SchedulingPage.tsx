// SchedulingPage.tsx —— 自动化编制展示与 CAS 编辑分区。
// 职责：展示服务端的载体/小队快照，并通过 scheduling API 保存登记草稿。
// 边界：不探活、不发现机器、不写 flow，也不决定协调者的服务端选择规则。
import { useEffect, useState } from 'react'
import type { ReactElement } from 'react'
import type { CarrierInput, CarrierView, SquadInput, SquadView, SquadsResp } from '../../api/scheduling'
import { getSquads, putCarrier, putSquad } from '../../api/scheduling'
import { errorMessage } from '../lib/format'

type CarrierDraft = Omit<CarrierInput, 'max_concurrency'> & { name: string; maxConcurrencyText: string }
type SquadDraft = Omit<SquadInput, 'max_concurrency'> & { name: string; maxConcurrencyText: string }
type EntityDialog =
  | { kind: 'carrier'; value: CarrierView | null }
  | { kind: 'squad'; value: SquadView | null }
  | null

const INPUT = 'h-8 rounded-md border bg-background px-2 text-xs'
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
  return {
    name: row?.name ?? '',
    machine: row?.machine ?? '本机',
    cli: row?.cli ?? 'opencode',
    home_dir: row?.home_dir ?? '',
    model: row?.model ?? '',
    credential: row?.credential ?? 'standalone',
    maxConcurrencyText: row?.max_concurrency?.toString() ?? '',
  }
}

function squadDraft(row: SquadView | null): SquadDraft {
  return {
    name: row?.name ?? '',
    role: row?.role === 'coordinator' ? 'coordinator' : 'executor',
    members: row ? [...row.members] : [],
    maxConcurrencyText: row?.max_concurrency?.toString() ?? '',
  }
}

function draftName(dialog: EntityDialog, draft: CarrierDraft | SquadDraft): string {
  return draft.name.trim() || (dialog?.value?.name ?? '')
}

/** Props 为空；返回设置页的自动化编制区；保存失败时保留草稿和弹窗供处理。 */
export type SchedulingPageProps = Record<string, never>

/** 渲染载体、小队快照和 CAS 编辑弹窗；命令/探活等运行时能力不在此页面。 */
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

  const load = async (): Promise<void> => {
    console.info('scheduling.load', { scope: 'settings' })
    setLoading(true)
    setLoadError('')
    try {
      const result = await getSquads()
      console.info('scheduling.load.done', { scope: 'settings', carriers: result.carriers.length, squads: result.squads.length })
      setSnapshot(result)
    } catch (cause) {
      console.error('scheduling.load.error', { scope: 'settings', cause })
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

  const save = async (): Promise<void> => {
    if (dialog === null || draft === null) return
    const name = draftName(dialog, draft)
    if (name === '') {
      setSaveError('名称不能为空')
      return
    }
    const expect = dialog.value?.version ?? 0 // CAS 必须使用打开弹窗时的行快照版本。
    setSaving(true)
    setSaveError('')
    try {
      if (dialog.kind === 'carrier') {
        const carrier = draft as CarrierDraft
        const result = await putCarrier(name, expect, {
          name,
          machine: carrier.machine,
          cli: carrier.cli,
          home_dir: carrier.home_dir,
          model: carrier.model?.trim() || undefined,
          credential: carrier.credential,
          ...(optionalConcurrency(carrier.maxConcurrencyText) === undefined ? {} : { max_concurrency: optionalConcurrency(carrier.maxConcurrencyText) }),
        })
        console.info('scheduling.save.done', { kind: 'carrier', name, version: result.version })
      } else {
        const squad = draft as SquadDraft
        const result = await putSquad(name, expect, {
          name,
          role: squad.role === 'coordinator' ? 'coordinator' : 'executor',
          members: squad.members,
          ...(optionalConcurrency(squad.maxConcurrencyText) === undefined ? {} : { max_concurrency: optionalConcurrency(squad.maxConcurrencyText) }),
        })
        console.info('scheduling.save.done', { kind: 'squad', name, version: result.version })
      }
      setDialog(null)
      setDraft(null)
      await load()
    } catch (cause) {
      const message = errorMessage(cause)
      if (message.includes('409') || message.includes('冲突')) {
        console.warn('scheduling.save.conflict', { kind: dialog.kind, name, expect, cause })
      } else {
        console.error('scheduling.save.error', { kind: dialog.kind, name, expect, cause })
      }
      setSaveError(message)
    } finally {
      setSaving(false)
    }
  }

  const updateDraft = (next: Partial<CarrierDraft | SquadDraft>): void => {
    setDraft((current) => current === null ? current : { ...current, ...next } as CarrierDraft | SquadDraft)
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
        {snapshot.carriers.map((row) => <article key={row.name} className="rounded-lg border p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs"><strong className="font-mono">{row.name}</strong><span className={`size-2 rounded-full ${row.healthy ? 'bg-emerald-500' : 'bg-muted-foreground'}`} title={row.healthy ? '健康' : '未探活'} /><span>{row.machine} · {row.cli}</span><span className="flex-1" /><span>在跑 — / {row.max_concurrency ?? '不限'} · v{row.version}</span><button type="button" className="rounded border px-2 py-1" onClick={() => openCarrier(row)}>编辑 {row.name}</button></div>
          <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs"><dt className="text-muted-foreground">HOME 档案</dt><dd className="font-mono">{row.home_dir}</dd><dt className="text-muted-foreground">模型</dt><dd>{row.model || <span className="text-muted-foreground">CLI 默认</span>}</dd><dt className="text-muted-foreground">凭据来源</dt><dd>{row.credential}</dd><dt className="text-muted-foreground">并发上限</dt><dd>{row.max_concurrency ?? '不限'}</dd></dl>
        </article>)}
        {snapshot.carriers.length === 0 && <p className="rounded-lg border border-dashed p-4 text-xs text-muted-foreground">尚未登记载体，请先登记一个可用 CLI 档案。</p>}
      </section>

      <section className="space-y-2">
        <div className="flex items-center gap-2"><h3 className="text-xs font-semibold">小队</h3><span className="text-[11px] text-muted-foreground">并发上限是政策位：按队计数</span><span className="flex-1" /><button type="button" className="rounded-md border px-2.5 py-1 text-xs" onClick={() => openSquad(null)}>建小队</button></div>
        {snapshot.squads.map((row) => <article key={row.name} className="rounded-lg border p-3">
          <div className="flex flex-wrap items-center gap-2 text-xs"><strong className="font-mono">{row.name}</strong><span className="rounded-full bg-muted px-2 py-0.5">{row.role === 'coordinator' ? '协调者队' : '执行者队'}</span><span className="flex-1" /><span>在跑 — / {row.max_concurrency ?? '不限'} · v{row.version}</span><button type="button" className="rounded border px-2 py-1" onClick={() => openSquad(row)}>编辑</button></div>
          <div className="mt-2 flex flex-wrap gap-1.5">{row.members.length > 0 ? row.members.map((member) => <span key={member} className="rounded border px-2 py-1 text-xs" title={member}>成员：{member}</span>) : <span className="text-xs text-muted-foreground">空队合法：先建队再补成员</span>}</div>
          <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs"><dt className="text-muted-foreground">并发上限</dt><dd>{row.max_concurrency ?? '不限'}</dd><dt className="text-muted-foreground">绑定对象</dt><dd>{row.role === 'coordinator' ? '拉起通道（开卡即绑 / 一键拉起）' : '工作流派发节点（flows 页配置）'}</dd></dl>
        </article>)}
        {snapshot.squads.length === 0 && <p className="rounded-lg border border-dashed p-4 text-xs text-muted-foreground">尚未登记小队，请创建 executor 或 coordinator 小队。</p>}
        <p className="text-[11px] text-muted-foreground">协调者队成员必须落在协调机；执行者队成员可以是任何执行机。</p>
      </section>

      {dialog !== null && draft !== null && <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/30 p-4" role="dialog" aria-modal="true" aria-labelledby="scheduling-dialog-title">
        <form className="w-full max-w-xl space-y-4 rounded-lg border bg-background p-5 shadow-lg" onSubmit={(event) => { event.preventDefault(); void save() }}>
          <div><h3 id="scheduling-dialog-title" className="text-sm font-semibold">{dialog.kind === 'carrier' ? (dialog.value ? `编辑载体 · ${dialog.value.name}` : '登记载体') : (dialog.value ? `编辑小队 · ${dialog.value.name}` : '建小队')}</h3><p className="mt-1 text-xs text-muted-foreground">{dialog.value ? `当前版本 v${dialog.value.version}，保存会校验此版本。` : '新建使用 expect=0。'}</p></div>
          {dialog.kind === 'carrier' ? <div className="grid gap-3 sm:grid-cols-2">
            <label className="space-y-1 text-xs">载体名<input aria-label="载体名" className={INPUT} readOnly={dialog.value !== null} value={draft.name} onChange={(event) => updateDraft({ name: event.target.value })} /></label>
            <label className="space-y-1 text-xs">机器<select aria-label="机器" className={INPUT} value={(draft as CarrierDraft).machine} onChange={(event) => updateDraft({ machine: event.target.value })}>{(draft as CarrierDraft).machine && !MACHINE_OPTIONS.includes((draft as CarrierDraft).machine) && <option value={(draft as CarrierDraft).machine}>{(draft as CarrierDraft).machine}</option>}{MACHINE_OPTIONS.map((machine) => <option key={machine} value={machine}>{machine}</option>)}</select></label>
            <label className="space-y-1 text-xs">CLI<select aria-label="CLI" className={INPUT} value={(draft as CarrierDraft).cli} onChange={(event) => updateDraft({ cli: event.target.value })}>{CLI_OPTIONS.map((cli) => <option key={cli} value={cli}>{cli}</option>)}</select></label>
            <label className="space-y-1 text-xs">模型<input aria-label="模型" className={INPUT} value={(draft as CarrierDraft).model ?? ''} onChange={(event) => updateDraft({ model: event.target.value })} /></label>
            <label className="space-y-1 text-xs sm:col-span-2">HOME 档案（隔离 HOME 路径；协调者 = 全套，执行者 = 干净会话）<input aria-label="HOME 档案" className={INPUT} value={(draft as CarrierDraft).home_dir} onChange={(event) => updateDraft({ home_dir: event.target.value })} /></label>
            <label className="space-y-1 text-xs">凭据来源<select aria-label="凭据来源" className={INPUT} value={(draft as CarrierDraft).credential} onChange={(event) => updateDraft({ credential: event.target.value })}>{CREDENTIAL_OPTIONS.map((credential) => <option key={credential.value} value={credential.value}>{credential.label}</option>)}</select></label>
            <label className="space-y-1 text-xs">并发上限（0 / 留空 = 不限）<input aria-label="并发上限" className={INPUT} type="number" min="0" value={(draft as CarrierDraft).maxConcurrencyText} onChange={(event) => updateDraft({ maxConcurrencyText: event.target.value })} /></label>
            <p className="text-[11px] leading-5 text-muted-foreground sm:col-span-2">主 HOME 同步 = 把主环境的认证态搬进隔离 HOME；两个同账户载体的真实限额共享，跨载体账户池的归属归 roadmap 的「限额探测」。</p>
          </div> : <div className="space-y-3">
            <label className="block space-y-1 text-xs">小队名<input aria-label="小队名" className={INPUT} readOnly={dialog.value !== null} value={draft.name} onChange={(event) => updateDraft({ name: event.target.value })} /></label>
            <label className="block space-y-1 text-xs">角色<select aria-label="角色" className={INPUT} value={(draft as SquadDraft).role} onChange={(event) => updateDraft({ role: event.target.value })}><option value="executor">executor</option><option value="coordinator">coordinator</option></select></label>
            <fieldset><legend className="text-xs">成员载体（按勾选顺序写入）</legend><div className="mt-2 grid gap-2 sm:grid-cols-2">{snapshot.carriers.map((carrier) => <label key={carrier.name} className="text-xs"><input type="checkbox" checked={(draft as SquadDraft).members.includes(carrier.name)} onChange={(event) => { const members = (draft as SquadDraft).members; updateDraft({ members: event.target.checked ? [...members, carrier.name] : members.filter((member) => member !== carrier.name) }) }} /> {carrier.name}</label>)}</div></fieldset>
            <label className="block space-y-1 text-xs">并发上限（0 / 留空 = 不限）<input aria-label="并发上限" className={INPUT} type="number" min="0" value={(draft as SquadDraft).maxConcurrencyText} onChange={(event) => updateDraft({ maxConcurrencyText: event.target.value })} /></label>
          </div>}
          {saveError && <p role="alert" className="break-words text-xs text-destructive">{saveError}</p>}
          <div className="flex justify-end gap-2"><button type="button" className="rounded-md border px-3 py-1.5 text-xs" disabled={saving} onClick={closeDialog}>取消</button><button type="submit" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" disabled={saving}>保存</button></div>
        </form>
      </div>}
    </div>
  )
}
