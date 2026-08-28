// CoordinatorPanel.tsx —— 卡抽屉里的协调者状态、拉起、attach 入口。
// 职责：把生命周期 HTTP 回执转成人能行动的状态与定位三元组。
// 边界：不创建第二套 TUI；不解析目录；原生终端行为由真机验证。
import { useState } from 'react'
import {
  attachCoordinator,
  getCoordinatorStatus,
  launchCoordinator,
  releaseCoordinator,
} from '../../api/scheduling'
import type { CoordinatorAttachInfo } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'

/** cardId 是完整卡号；组件只显示服务端定位信息，不承诺浏览器已打开原生终端。 */
export interface CoordinatorPanelProps {
  cardId: string
}

/** 参数：卡号；返回：协调者状态/拉起/attach/交回操作区；真机终端由另行清单验证。 */
export function CoordinatorPanel({ cardId }: CoordinatorPanelProps) {
  const state = usePoll(() => getCoordinatorStatus(cardId), 5000)
  const [busy, setBusy] = useState<'launch' | 'attach' | 'release' | null>(null)
  const [actionError, setActionError] = useState('')
  const [launchOutput, setLaunchOutput] = useState('')
  const [attachInfo, setAttachInfo] = useState<CoordinatorAttachInfo | null>(null)

  const launch = async () => {
    setBusy('launch')
    setActionError('')
    setLaunchOutput('')
    try {
      const result = await launchCoordinator(cardId)
      setLaunchOutput(result.output ?? (result.escalated ? '协调者已转人工处理' : '协调者已拉起'))
      state.refresh()
    } catch (err) {
      setActionError(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const attach = async () => {
    setBusy('attach')
    setActionError('')
    try {
      const result = await attachCoordinator(cardId, state.data?.attach?.dir ?? '')
      setAttachInfo(result)
      state.refresh()
    } catch (err) {
      setActionError(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const release = async () => {
    setBusy('release')
    setActionError('')
    try {
      await releaseCoordinator(cardId)
      setAttachInfo(null)
      state.refresh()
    } catch (err) {
      setActionError(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  if (state.sessionExpired) {
    return (
      <section className="mb-5">
        <h3 className="mb-1.5 text-xs font-semibold">协调者</h3>
        <SessionExpiredBanner />
      </section>
    )
  }

  if (state.data === null) {
    return (
      <section className="mb-5">
        <h3 className="mb-1.5 text-xs font-semibold">协调者</h3>
        <LoadFailed message={state.errorText || '正在读取协调者状态…'} onRetry={() => state.refresh()} />
      </section>
    )
  }

  const info = attachInfo ?? state.data.attach
  const disabled = state.disconnected || busy !== null

  return (
    <section className="mb-5 rounded-lg border p-3">
      <div className="flex items-center gap-2">
        <h3 className="text-xs font-semibold">协调者</h3>
        <span className="text-xs">{state.data.bound ? '已绑定' : '未绑定'}</span>
        {state.data.attach_active && <span className="rounded-full border border-amber-300 bg-amber-50 px-1.5 text-[10px]">人工接管中</span>}
      </div>
      {state.disconnected && <DisconnectedBanner compact message={state.errorText} />}
      {!state.data.bound && <Button className="mt-2" size="sm" disabled={disabled} onClick={() => void launch()}>拉起协调者</Button>}
      {state.data.bound && !state.data.attach_active && <Button className="mt-2" size="sm" variant="outline" disabled={disabled} onClick={() => void attach()}>打开终端</Button>}
      {state.data.attach_active && <Button className="mt-2" size="sm" variant="outline" disabled={disabled} onClick={() => void release()}>交回无头</Button>}
      {launchOutput && <p className="mt-2 break-words text-xs text-muted-foreground">{launchOutput}</p>}
      {info && (
        <dl className="mt-2 grid gap-1 text-xs">
          <div><dt className="inline text-muted-foreground">机器：</dt><dd className="inline">{info.machine || '本机'}</dd></div>
          <div><dt className="inline text-muted-foreground">目录：</dt><dd className="inline break-all font-mono">{info.dir || '—'}</dd></div>
          <div><dt className="inline text-muted-foreground">命令：</dt><dd className="inline break-all font-mono">{info.command || '—'}</dd></div>
        </dl>
      )}
      {actionError && <p role="alert" className="mt-2 break-words text-xs text-destructive">{actionError}</p>}
    </section>
  )
}
