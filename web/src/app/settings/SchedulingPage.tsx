// SchedulingPage.tsx —— 自动化编制配置分区。
//
// 职责：编辑载体/小队登记行并显示版本/健康快照。
// 边界：只经 api/scheduling.ts；不读 registry、不决定角色规则、不负责拉起。
import { useEffect, useState } from 'react'
import type { CarrierInput, CarrierView, SquadInput, SquadView } from '../../api/scheduling'
import { getSquads, putCarrier, putSquad } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'

type CarrierDraft = CarrierInput & { name: string }
type SquadDraft = SquadInput & { name: string }
const INPUT = 'h-8 rounded-md border bg-background px-2 text-xs'

export interface SchedulingPageProps {}

/** 空字符串表示 optional 字段缺席；0 不是“未填写”的替代值。 */
function optionalConcurrency(raw: string): number | undefined {
  if (raw.trim() === '') return undefined
  const value = Number(raw)
  return Number.isInteger(value) && value >= 0 ? value : undefined
}

function carrierDraft(row: CarrierView): CarrierDraft {
  return {
    name: row.name,
    machine: row.machine,
    cli: row.cli,
    home_dir: row.home_dir,
    model: row.model,
    credential: row.credential,
    max_concurrency: row.max_concurrency,
  }
}

function squadDraft(row: SquadView): SquadDraft {
  return {
    name: row.name,
    role: row.role,
    members: [...row.members],
    max_concurrency: row.max_concurrency,
  }
}

/**
 * 参数：空 props；返回：载体/小队编辑页；注意：保存必须使用服务端当前 version。
 */
export function SchedulingPage(_props: SchedulingPageProps) {
  const state = usePoll(getSquads, 10000)
  const [carriers, setCarriers] = useState<Record<string, CarrierDraft>>({})
  const [squads, setSquads] = useState<Record<string, SquadDraft>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState<string | null>(null)

  useEffect(() => {
    if (!state.data) return
    setCarriers(Object.fromEntries(state.data.carriers.map((row) => [row.name, carrierDraft(row)])))
    setSquads(Object.fromEntries(state.data.squads.map((row) => [row.name, squadDraft(row)])))
  }, [state.data])

  const saveCarrier = async (row: CarrierView) => {
    const draft = carriers[row.name]
    if (!draft) return
    const key = 'carrier:' + row.name
    setBusy(key)
    setErrors((current) => ({ ...current, [key]: '' }))
    try {
      await putCarrier(row.name, row.version, { ...draft, name: draft.name || undefined })
      state.refresh()
    } catch (err) {
      setErrors((current) => ({ ...current, [key]: errorMessage(err) }))
    } finally {
      setBusy(null)
    }
  }

  const saveSquad = async (row: SquadView) => {
    const draft = squads[row.name]
    if (!draft) return
    const key = 'squad:' + row.name
    setBusy(key)
    setErrors((current) => ({ ...current, [key]: '' }))
    try {
      await putSquad(row.name, row.version, { ...draft, name: draft.name || undefined })
      state.refresh()
    } catch (err) {
      setErrors((current) => ({ ...current, [key]: errorMessage(err) }))
    } finally {
      setBusy(null)
    }
  }

  if (state.sessionExpired) {
    return <div className="p-3"><SessionExpiredBanner /></div>
  }
  if (state.data === null) {
    return <div className="p-3"><LoadFailed message={state.errorText || '正在读取载体/小队配置…'} onRetry={() => state.refresh()} /></div>
  }

  return <div className="flex min-h-full flex-col gap-4 p-3">
    {state.disconnected && <DisconnectedBanner message={state.errorText} />}
    <div>
      <h2 className="text-sm font-semibold">自动化编制</h2>
      <p className="mt-1 text-xs text-muted-foreground">载体是物理承载，小队是角色与并发策略；保存使用当前版本的 CAS。</p>
    </div>
    <section className="rounded-lg border p-3">
      <h3 className="mb-2 text-xs font-semibold">载体</h3>
      {state.data.carriers.map((row) => {
        const draft = carriers[row.name] ?? carrierDraft(row)
        const key = 'carrier:' + row.name
        return <div key={row.name} className="mb-3 rounded-md border p-2">
          <div className="mb-2 flex items-center gap-2 text-xs">
            <span className="font-mono">{row.name}</span>
            <span className="text-muted-foreground">v{row.version}</span>
            <span>{row.healthy ? '健康' : '未探活'}</span>
          </div>
          <div className="grid gap-2 md:grid-cols-3">
            <input aria-label={row.name + ' machine'} className={INPUT} value={draft.machine} onChange={(event) => setCarriers((current) => ({ ...current, [row.name]: { ...draft, machine: event.target.value } }))} />
            <input aria-label={row.name + ' cli'} className={INPUT} value={draft.cli} onChange={(event) => setCarriers((current) => ({ ...current, [row.name]: { ...draft, cli: event.target.value } }))} />
            <input aria-label={row.name + ' home_dir'} className={INPUT} value={draft.home_dir} onChange={(event) => setCarriers((current) => ({ ...current, [row.name]: { ...draft, home_dir: event.target.value } }))} />
            <input aria-label={row.name + ' model'} className={INPUT} value={draft.model ?? ''} onChange={(event) => setCarriers((current) => ({ ...current, [row.name]: { ...draft, model: event.target.value || undefined } }))} />
            <input aria-label={row.name + ' credential'} className={INPUT} value={draft.credential} onChange={(event) => setCarriers((current) => ({ ...current, [row.name]: { ...draft, credential: event.target.value } }))} />
            <input aria-label={row.name + ' max_concurrency'} className={INPUT} type="number" min="0" value={draft.max_concurrency ?? ''} onChange={(event) => setCarriers((current) => ({ ...current, [row.name]: { ...draft, max_concurrency: optionalConcurrency(event.target.value) } }))} />
          </div>
          <div className="mt-2"><Button size="sm" variant="outline" disabled={state.disconnected || busy !== null} onClick={() => void saveCarrier(row)}>保存载体</Button></div>
          {errors[key] && <p role="alert" className="mt-1 break-words text-xs text-destructive">{errors[key]}</p>}
        </div>
      })}
      {state.data.carriers.length === 0 && <p className="text-xs text-muted-foreground">尚未登记载体。</p>}
    </section>
    <section className="rounded-lg border p-3">
      <h3 className="mb-2 text-xs font-semibold">小队</h3>
      {state.data.squads.map((row) => {
        const draft = squads[row.name] ?? squadDraft(row)
        const key = 'squad:' + row.name
        return <div key={row.name} className="mb-3 rounded-md border p-2">
          <div className="mb-2 flex items-center gap-2 text-xs">
            <span className="font-mono">{row.name}</span>
            <span className="text-muted-foreground">v{row.version}</span>
          </div>
          <div className="grid gap-2 md:grid-cols-3">
            <input aria-label={row.name + ' role'} className={INPUT} value={draft.role} onChange={(event) => setSquads((current) => ({ ...current, [row.name]: { ...draft, role: event.target.value } }))} />
            <input aria-label={row.name + ' members'} className={INPUT} value={draft.members.join(',')} onChange={(event) => setSquads((current) => ({ ...current, [row.name]: { ...draft, members: event.target.value.split(',').map((value) => value.trim()).filter(Boolean) } }))} />
            <input aria-label={row.name + ' max_concurrency'} className={INPUT} type="number" min="0" value={draft.max_concurrency ?? ''} onChange={(event) => setSquads((current) => ({ ...current, [row.name]: { ...draft, max_concurrency: optionalConcurrency(event.target.value) } }))} />
          </div>
          <div className="mt-2"><Button size="sm" variant="outline" disabled={state.disconnected || busy !== null} onClick={() => void saveSquad(row)}>保存小队</Button></div>
          {errors[key] && <p role="alert" className="mt-1 break-words text-xs text-destructive">{errors[key]}</p>}
        </div>
      })}
      {state.data.squads.length === 0 && <p className="text-xs text-muted-foreground">尚未登记小队。</p>}
    </section>
  </div>
}
