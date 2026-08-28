// CoordinatorPanel.tsx —— 卡抽屉里的协调者三态与人工 attach 入口。
// 边界：状态来自 /coordinator，attach 的目录和命令来自服务端；本组件不解析、拼接或改写 command，
// 原生终端打开由 Workbench/Shell 接缝负责。
import { useState } from 'react'
import type { ReactElement } from 'react'
import { attachCoordinator, getCoordinatorStatus, launchCoordinator, releaseCoordinator } from '../../api/scheduling'
import type { CoordinatorAttachInfo, CoordinatorStatus } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'

/** 参数：完整 cardId 与终端回调；回调接收服务端原样 AttachInfo，调用方负责打开终端。 */
export interface CoordinatorPanelProps {
  cardId: string
  onOpenTerminal: (info: CoordinatorAttachInfo) => void
}

type CoordinatorView = 'unbound' | 'bound' | 'attach-active' | 'invalid'

function coordinatorView(status: CoordinatorStatus): CoordinatorView {
  if (!status.bound && !status.attach_active && status.attach === null) return 'unbound'
  if (status.bound && !status.attach_active && status.attach !== null) return 'bound'
  if (status.bound && status.attach_active && status.attach !== null) return 'attach-active'
  return 'invalid'
}

function statusText(view: CoordinatorView): string {
  if (view === 'unbound') return '未绑定'
  if (view === 'bound') return '已绑定'
  if (view === 'attach-active') return '人工接管中'
  return '状态不一致'
}

/** 参数：卡号与终端回调；返回：协调者状态/拉起/attach/交回区；command 仅交给终端接缝。 */
export function CoordinatorPanel({ cardId, onOpenTerminal }: CoordinatorPanelProps): ReactElement {
  const state = usePoll(async () => {
    console.info('coordinator.status.start', { card: cardId })
    try {
      const result = await getCoordinatorStatus(cardId)
      console.info('coordinator.status.done', { card: cardId, bound: result.bound, attach_active: result.attach_active })
      return result
    } catch (cause) {
      console.error('coordinator.status.error', { card: cardId, cause })
      throw cause
    }
  }, 5000)
  const [busy, setBusy] = useState<'launch' | 'attach' | 'release' | null>(null)
  const [actionError, setActionError] = useState('')
  const [launchOutput, setLaunchOutput] = useState('')
  const [attachConfirm, setAttachConfirm] = useState(false)

  const launch = async (): Promise<void> => {
    setBusy('launch')
    setActionError('')
    setLaunchOutput('')
    console.info('coordinator.launch.start', { card: cardId, active: false, source: 'manual' })
    try {
      const result = await launchCoordinator(cardId, 'manual')
      console.info('coordinator.launch.done', { card: cardId, active: false, woke: result.woke, rebuilt: result.rebuilt, escalated: result.escalated })
      setLaunchOutput(result.output ?? (result.escalated ? '协调者已转人工处理' : '协调者已拉起'))
      state.refresh()
    } catch (cause) {
      console.error('coordinator.launch.error', { card: cardId, active: false, cause })
      setActionError(errorMessage(cause))
    } finally {
      setBusy(null)
    }
  }

  const confirmAttach = async (): Promise<void> => {
    const info = state.data?.attach
    if (!info) {
      setActionError('协调者状态没有可 attach 的服务端目录，请刷新状态')
      return
    }
    setBusy('attach')
    setActionError('')
    console.info('coordinator.attach.start', { card: cardId, active: true, dir: info.dir })
    try {
      const result = await attachCoordinator(cardId, info.dir)
      console.info('coordinator.attach.done', { card: cardId, active: true, machine: result.machine, dir: result.dir })
      setAttachConfirm(false)
      onOpenTerminal(result)
      state.refresh()
    } catch (cause) {
      console.error('coordinator.attach.error', { card: cardId, active: true, cause })
      setActionError(errorMessage(cause))
    } finally {
      setBusy(null)
    }
  }

  const release = async (): Promise<void> => {
    setBusy('release')
    setActionError('')
    console.info('coordinator.release.start', { card: cardId, active: false })
    try {
      const result = await releaseCoordinator(cardId)
      if (!result.ok) throw new Error('交回无头失败：服务端未确认释放')
      console.info('coordinator.release.done', { card: cardId, active: false })
      state.refresh()
    } catch (cause) {
      console.error('coordinator.release.error', { card: cardId, active: false, cause })
      setActionError(errorMessage(cause))
    } finally {
      setBusy(null)
    }
  }

  const heading = <h3 className="mb-1.5 text-xs font-semibold">协调者</h3>
  if (state.sessionExpired) return <section className="mb-5">{heading}<SessionExpiredBanner /></section>
  if (state.data === null) return <section className="mb-5">{heading}<LoadFailed message={state.errorText || '正在读取协调者状态…'} onRetry={state.refresh} /></section>

  const view = coordinatorView(state.data)
  const info = state.data.attach
  const disabled = state.disconnected || busy !== null

  return (
    <section className="mb-5 rounded-lg border p-3">
      <div className="flex items-center gap-2">
        {heading}
        <span className="text-xs">{statusText(view)}</span>
      </div>
      {state.disconnected && <DisconnectedBanner compact message={state.errorText} />}
      {view === 'unbound' && <Button className="mt-2" size="sm" disabled={disabled} onClick={() => void launch()}>▶ 拉起协调者</Button>}
      {view === 'bound' && <Button className="mt-2" size="sm" variant="outline" disabled={disabled} onClick={() => { setActionError(''); setAttachConfirm(true) }}>打开终端</Button>}
      {view === 'attach-active' && <Button className="mt-2" size="sm" variant="outline" disabled={disabled} onClick={() => void release()}>交回无头</Button>}
      {view === 'invalid' && <p role="alert" className="mt-2 text-xs text-destructive">服务端协调者状态不一致，请刷新重试。</p>}
      {launchOutput && <p className="mt-2 break-words text-xs text-muted-foreground">{launchOutput}</p>}
      {info && (
        <dl className="mt-2 grid gap-1 text-xs">
          <div><dt className="inline text-muted-foreground">机器：</dt><dd className="inline">{info.machine || '本机'}</dd></div>
          <div><dt className="inline text-muted-foreground">目录：</dt><dd className="inline break-all font-mono">{info.dir || '—'}</dd></div>
          <div><dt className="inline text-muted-foreground">命令：</dt><dd className="inline break-all font-mono">{info.command || '—'}</dd></div>
        </dl>
      )}
      {actionError && <p role="alert" className="mt-2 break-words text-xs text-destructive">{actionError}</p>}
      <ConfirmDialog
        open={attachConfirm}
        title="确认打开协调者终端"
        description={info ? `目录：${info.dir}\n命令：${info.command}\nattach 与自动唤醒互斥` : '协调者状态没有可 attach 的服务端定位。'}
        confirmLabel="确认 attach"
        busy={busy === 'attach'}
        error={actionError}
        onConfirm={() => void confirmAttach()}
        onCancel={() => { if (busy === null) setAttachConfirm(false) }}
      />
    </section>
  )
}
