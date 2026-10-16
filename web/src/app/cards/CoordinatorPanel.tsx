// CoordinatorPanel.tsx —— 卡抽屉里的协调者三态、人工 attach 入口与 Claude 桌面端坐席入口（B434）。
// 边界：状态来自 /coordinator，attach 的目录和命令来自服务端；本组件不解析、拼接或改写 command，
// 原生终端打开由 Workbench/Shell 接缝负责。
// B434 桌面端坐席：拉起材料（folder/brief/launcher）只来自 GET /coordinator/desktop-launch，本组件
// 不拼简报、不手拼深链（编码归 claudeDeepLink.ts，薄壳桥归 desktopShell.ts）；坐席文案一律取
// desktopSeatCopy.ts 常量，源码里不写文案字面量（契约条 242 以 ?raw 读本文件锁住，注释里也不许出现）。
// 打开动作拿不到回执（浏览器 <a href> 与薄壳 OpenURL 都不知道桌面端收没收到），所以浏览器形态下
// 复制兜底常驻；薄壳形态下桥不可用、编码拒绝或抛错时展开（契约 §3.12、breakdown §5.6）。
import { useEffect, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import { flushSync } from 'react-dom'
import { attachCoordinator, getCoordinatorStatus, getDesktopLaunchMaterial, launchCoordinator, openViaDesktopLauncher, rebindCoordinatorLaunch, releaseCoordinator } from '../../api/scheduling'
import type { CoordinatorAttachInfo, CoordinatorStatus, DesktopLaunchMaterial } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { buildClaudeNewSessionURL, CLAUDE_Q_MAX_CHARS } from '../lib/claudeDeepLink'
import type { ClaudeDeepLinkRejectReason, ClaudeDeepLinkResult } from '../lib/claudeDeepLink'
import { copyToClipboard } from '../lib/clipboard'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { isDesktopShell, requestOpenDeepLink } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'
import { DESKTOP_COPY_BRIEF_BUTTON, DESKTOP_COPY_BRIEF_HINT, DESKTOP_LAUNCHER_BUTTON, DESKTOP_LAUNCHER_SENT, DESKTOP_SEAT_BUTTON, DESKTOP_TRUST_HINT } from './desktopSeatCopy'

/** 参数：完整 cardId 与终端回调；回调接收服务端原样 AttachInfo，调用方负责打开终端。 */
export interface CoordinatorPanelProps {
  cardId: string
  onOpenTerminal: (info: CoordinatorAttachInfo) => void
}

type CoordinatorView = 'unbound' | 'bound' | 'attach-active' | 'invalid'

type CoordinatorBusy = 'launch' | 'rebind' | 'attach' | 'release' | 'desktop-launcher'

// DesktopMaterialState 是拉起材料的读取态：loading 期间坐下按钮禁用；unavailable 的 reason
// 是服务端 error 原文（409 无本机位置等），逐字进按钮 title 与说明文字。
type DesktopMaterialState =
  | { kind: 'loading' }
  | { kind: 'ready'; material: DesktopLaunchMaterial }
  | { kind: 'unavailable'; reason: string }

// DesktopOpenState 是本挂载期最近一次点击坐下的结果：opened = 打开动作已发出（薄壳桥或浏览器 <a>）；
// fallback = 编码拒绝 / 无桥 / 编码抛错 / <a> 未挂上，需展开复制兜底；idle = 尚未点击。
type DesktopOpenState =
  | { kind: 'idle' }
  | { kind: 'opened'; via: 'bridge' | 'anchor'; url: string }
  | { kind: 'fallback'; cause: ClaudeDeepLinkRejectReason | 'no_bridge' | 'encode_threw' | 'anchor_missing' }

// 两个初始态用模块级常量：effect 里重置成同一引用时 React 直接跳过重渲染，
// 不给挂载期平白多一轮渲染（新建对象字面量每次都会触发一次）。
const MATERIAL_LOADING: DesktopMaterialState = { kind: 'loading' }
const OPEN_IDLE: DesktopOpenState = { kind: 'idle' }

function coordinatorView(status: CoordinatorStatus): CoordinatorView {
  if (!status.bound && !status.attach_active && status.attach === null) return 'unbound'
  // bind 席位没有 agentd 内存中的 SessionRef，因此合法 bound 可以没有 attach。
  if (status.bound && !status.attach_active) return 'bound'
  if (status.bound && status.attach_active && status.attach !== null) return 'attach-active'
  return 'invalid'
}

function statusText(view: CoordinatorView): string {
  if (view === 'unbound') return '未绑定'
  if (view === 'bound') return '已绑定'
  if (view === 'attach-active') return '人工接管中'
  return '状态不一致'
}

// rejectLogDetail 把编码拒绝原因译成日志说明。switch 穷尽 ClaudeDeepLinkRejectReason 且不留 default：
// 联合类型新增取值时这里编译失败，逼调用方决定新值怎么处置（缺陷族·枚举新值过白名单）。
function rejectLogDetail(reason: ClaudeDeepLinkRejectReason): string {
  switch (reason) {
    case 'folder_not_absolute':
      return '材料 folder 不是绝对路径'
    case 'brief_empty':
      return '材料 brief 为空'
    case 'brief_too_long':
      return `材料 brief 超过 ${CLAUDE_Q_MAX_CHARS} 字符`
    case 'not_implemented':
      return '深链编码未实现'
  }
}

/** 参数：卡号与终端回调；返回：协调者状态/拉起/attach/交回区与桌面端坐席入口；command 仅交给终端接缝。 */
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
  const [busy, setBusy] = useState<CoordinatorBusy | null>(null)
  const [actionError, setActionError] = useState('')
  const [launchOutput, setLaunchOutput] = useState('')
  const [attachConfirm, setAttachConfirm] = useState(false)
  const [material, setMaterial] = useState<DesktopMaterialState>(MATERIAL_LOADING)
  const [openState, setOpenState] = useState<DesktopOpenState>(OPEN_IDLE)
  const anchorRef = useRef<HTMLAnchorElement>(null)
  const unbound = state.data !== null && coordinatorView(state.data) === 'unbound'

  // 空座时读一次拉起材料；换卡或从已绑定回到空座时重读。不走 usePoll：材料只在空座有用，
  // 每 5s 重读会让服务端反复解析项目位置与简报；cancelled 防止换卡后旧卡的材料落到新卡上。
  useEffect(() => {
    if (!unbound) return
    let cancelled = false
    setMaterial(MATERIAL_LOADING)
    setOpenState(OPEN_IDLE)
    console.info('coordinator.desktop_material.start', { card: cardId })
    getDesktopLaunchMaterial(cardId).then(
      (result) => {
        if (cancelled) return
        console.info('coordinator.desktop_material.done', { card: cardId, folder: result.folder, brief_chars: result.brief.length, launcher: result.launcher ?? '' })
        setMaterial({ kind: 'ready', material: result })
      },
      (cause: unknown) => {
        if (cancelled) return
        console.error('coordinator.desktop_material.error', { card: cardId, cause })
        setMaterial({ kind: 'unavailable', reason: errorMessage(cause) })
      },
    )
    return () => {
      cancelled = true
    }
  }, [cardId, unbound])

  const launch = async (): Promise<void> => {
    setBusy('launch')
    setActionError('')
    setLaunchOutput('')
    console.info('coordinator.launch.start', { card: cardId, active: false })
    try {
      const result = await launchCoordinator(cardId)
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

  // seatWithClaudeCode 是坐下按钮的点击处理：材料 → 编码 → 薄壳桥或浏览器 <a>；任何一步打不开
  // 都落到 fallback 让复制兜底展开，不静默无反应。日志只记长度不记 URL/简报全文。
  const seatWithClaudeCode = (): void => {
    if (material.kind !== 'ready') return
    const { folder, brief } = material.material
    const shell = isDesktopShell()
    console.info('coordinator.desktop_seat.start', { card: cardId, shell, folder, brief_chars: brief.length })
    let encoded: ClaudeDeepLinkResult
    try {
      encoded = buildClaudeNewSessionURL(folder, brief)
    } catch (cause) {
      // why：encodeURIComponent 遇孤立代理项抛 URIError；真实材料到不了这里（服务端 JSON 只产合法
      // UTF-8），但异常冒泡会让点击静默失效，所以退到复制兜底。
      console.error('coordinator.desktop_seat.encode_threw', { card: cardId, cause })
      setOpenState({ kind: 'fallback', cause: 'encode_threw' })
      return
    }
    if (!encoded.ok) {
      console.warn('coordinator.desktop_seat.rejected', { card: cardId, reason: encoded.reason, detail: rejectLogDetail(encoded.reason) })
      setOpenState({ kind: 'fallback', cause: encoded.reason })
      return
    }
    const url = encoded.url
    if (shell) {
      if (!requestOpenDeepLink(url)) {
        console.warn('coordinator.desktop_seat.no_bridge', { card: cardId })
        setOpenState({ kind: 'fallback', cause: 'no_bridge' })
        return
      }
      console.info('coordinator.desktop_seat.done', { card: cardId, via: 'bridge', url_chars: url.length })
      setOpenState({ kind: 'opened', via: 'bridge', url })
      return
    }
    // 浏览器：先同步渲染出 <a href>，再在同一次点击的调用栈里点它，claude:// 交给浏览器原生导航；
    // 不在异步回调里导航，免得依赖浏览器对「非手势导航」的放行策略。
    flushSync(() => setOpenState({ kind: 'opened', via: 'anchor', url }))
    const anchor = anchorRef.current
    if (anchor === null) {
      console.error('coordinator.desktop_seat.anchor_missing', { card: cardId })
      setOpenState({ kind: 'fallback', cause: 'anchor_missing' })
      return
    }
    anchor.click()
    console.info('coordinator.desktop_seat.done', { card: cardId, via: 'anchor', url_chars: url.length })
  }

  // copyBrief 把材料简报原文写进剪贴板。copyToClipboard 的同步段（execCommand）必须留在点击调用栈里，
  // 所以直接调用、不先 await 别的东西；失败进 role="alert" 段，不假装成功。
  const copyBrief = (brief: string): void => {
    setActionError('')
    console.info('coordinator.desktop_copy.start', { card: cardId, brief_chars: brief.length })
    void copyToClipboard(brief).then((ok) => {
      if (ok) {
        console.info('coordinator.desktop_copy.done', { card: cardId })
        return
      }
      console.warn('coordinator.desktop_copy.failed', { card: cardId })
      setActionError('简报没能写进剪贴板：浏览器拒绝了剪贴板写入')
    })
  }

  // openLauncher 请项目登记的拉起器代开一场桌面端会话（契约 §3.12 拉起器代开入口）。成功只给提示、
  // 不刷新绑定态：拉起器开会话、新会话 card bind 要几分钟，由 5s 轮询自然读到已绑定。
  // 失败文本逐字取服务端 error（409 无拉起器 / 席位有人 / 无本机位置、502 投递失败），不加前缀。
  const openLauncher = async (): Promise<void> => {
    setBusy('desktop-launcher')
    setActionError('')
    setLaunchOutput('')
    console.info('coordinator.desktop_launcher.start', { card: cardId })
    try {
      const result = await openViaDesktopLauncher(cardId)
      console.info('coordinator.desktop_launcher.done', { card: cardId, launcher: result.launcher, machine: result.machine, handled_by: result.handled_by })
      setLaunchOutput(DESKTOP_LAUNCHER_SENT)
    } catch (cause) {
      console.error('coordinator.desktop_launcher.error', { card: cardId, cause })
      setActionError(errorMessage(cause))
    } finally {
      setBusy(null)
    }
  }

  const rebind = async (): Promise<void> => {
    setBusy('rebind')
    setActionError('')
    setLaunchOutput('')
    console.info('coordinator.rebind.start', { card: cardId, mode: 'launch' })
    try {
      const result = await rebindCoordinatorLaunch(cardId)
      console.info('coordinator.rebind.done', { card: cardId, mode: 'launch', session: result.session_id })
      setLaunchOutput(result.output ?? '协调者已换绑')
      state.refresh()
    } catch (cause) {
      console.error('coordinator.rebind.error', { card: cardId, mode: 'launch', cause })
      setActionError(`叫机器人换绑失败：${errorMessage(cause)}`)
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
  const shell = isDesktopShell()
  const seatDisabled = disabled || material.kind !== 'ready'
  // 拉起器按钮是「不出现」而非禁用：材料未就绪、409、读取失败、launcher 缺键或空串一律不渲染（条 203/204）。
  const showLauncher = material.kind === 'ready' && (material.material.launcher ?? '') !== ''
  // 浏览器形态常驻：<a href="claude://…"> 失败时页面感知不到；薄壳形态只在打不开时展开。
  const showCopy = material.kind === 'ready' && (!shell || openState.kind === 'fallback')

  return (
    <section className="mb-5 rounded-lg border p-3">
      <div className="flex items-center gap-2">
        {heading}
        <span className="text-xs">{statusText(view)}</span>
      </div>
      {state.disconnected && <DisconnectedBanner compact message={state.errorText} />}
      {view === 'unbound' && (
        <div className="mt-2 flex flex-wrap gap-2">
          <Button size="sm" disabled={disabled} onClick={() => void launch()}>▶ 叫机器人</Button>
          <Button size="sm" variant="outline" disabled={seatDisabled} title={material.kind === 'unavailable' ? material.reason : undefined} onClick={seatWithClaudeCode}>{DESKTOP_SEAT_BUTTON}</Button>
          {showLauncher && <Button size="sm" variant="outline" disabled={disabled} onClick={() => void openLauncher()}>{DESKTOP_LAUNCHER_BUTTON}</Button>}
        </div>
      )}
      {view === 'unbound' && material.kind === 'unavailable' && <p className="mt-1 break-words text-xs text-muted-foreground">{material.reason}</p>}
      {view === 'unbound' && showCopy && material.kind === 'ready' && (
        <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
          <Button size="sm" variant="outline" onClick={() => copyBrief(material.material.brief)}>{DESKTOP_COPY_BRIEF_BUTTON}</Button>
          <span className="text-muted-foreground">{DESKTOP_COPY_BRIEF_HINT}</span>
        </div>
      )}
      {view === 'unbound' && openState.kind === 'opened' && <p className="mt-2 text-xs text-muted-foreground">{DESKTOP_TRUST_HINT}</p>}
      {view === 'unbound' && openState.kind === 'opened' && openState.via === 'anchor' && <a ref={anchorRef} href={openState.url} aria-hidden="true" tabIndex={-1} className="sr-only" />}
      {view === 'bound' && <div className="mt-2 flex gap-2">{info && <Button size="sm" variant="outline" disabled={disabled} onClick={() => { setActionError(''); setAttachConfirm(true) }}>打开终端</Button>}<Button size="sm" variant="outline" disabled={disabled} onClick={() => void rebind()}>换绑：叫机器人</Button></div>}
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
