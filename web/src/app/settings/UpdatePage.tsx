// 设置页「更新」分区。
//
// 职责：展示桌面应用与同步状态，并为远端执行机提供一次升级入口。
// 边界：本机不提供升级按钮；升级完成仍由 GET /api/machines 的 version 变化判定，
// 不另造进度流；桌面端自我替换仍由安装包动作完成。
import { useEffect, useState } from 'react'
import { ApiError, fetchLatest, startDownload, upgradeMachine } from '../../api/client'
import type { DesktopState, LatestResp, Machine, MachineUpgradeResp } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useDownload } from '../data/useUpdate'
import { errorMessage } from '../lib/format'
import { hasNewer, isComparableVersion } from '../lib/version'

export interface UpdatePageProps {
  desktopState: DesktopState | null
  latest: LatestResp | null
}

// machineLabel 把空机器名翻成页面里的本机文案。
function machineLabel(machine: Machine): string {
  return machine.name === '' ? '本机' : machine.name
}

// downloadFileName 从 agentd 返回的绝对路径中取文件名。
function downloadFileName(path: string): string {
  return path.split(/[\\/]/).pop() || path
}

interface MachineUpgradeViewState {
  running: boolean
  force: boolean
  reason: string
  remedy: string
}

// UpdatePage 渲染更新页的桌面应用、同步状态、执行机三块内容。
export function UpdatePage({ desktopState, latest }: UpdatePageProps) {
  const machinesState = useMachines(true)
  const [latestOverride, setLatestOverride] = useState<LatestResp | null>(null)
  const [checking, setChecking] = useState(false)
  const [checkError, setCheckError] = useState('')
  const [downloadStarted, setDownloadStarted] = useState(false)
  const [downloadError, setDownloadError] = useState('')
  const [machineUpgradeStates, setMachineUpgradeStates] = useState<Record<string, MachineUpgradeViewState>>({})
  const latestView = latestOverride ?? latest
  // 有桌面状态时持续接进度；agentd 的状态在页面刷新后仍可被重新读到。
  const download = useDownload(desktopState !== null || downloadStarted)

  const refreshLatest = async () => {
    setChecking(true)
    setCheckError('')
    try {
      setLatestOverride(await fetchLatest(true))
    } catch (err: unknown) {
      setCheckError(errorMessage(err))
    } finally {
      setChecking(false)
    }
  }

  const downloadPackage = async () => {
    setDownloadStarted(true)
    setDownloadError('')
    try {
      await startDownload()
    } catch (err: unknown) {
      setDownloadError(errorMessage(err))
    }
  }

  const machines = machinesState.data?.machines ?? []

  // 升级没有单独的进度流，但**必须有出口**。两条出口，缺一不可：
  //   成功：机器版本变成了目标版本（沿用原判据）
  //   结束但没成功：服务端 upgrade 段报出 running=false 的终态
  // 只留前者是 B166 二期的缺陷——失败时版本压根不会变，按钮就永远停在「升级中」，
  // 而 agentd 其实早已放弃（真机实测：后端 10:58 记下网络超时，界面还在转）。
  useEffect(() => {
    const latestTag = latestView?.tag ?? ''
    setMachineUpgradeStates((previous) => {
      let changed = false
      const next = { ...previous }
      for (const machine of machines) {
        if (machine.name === '' || !next[machine.name]?.running) continue
        const doneOnServer = machine.upgrade !== undefined && !machine.upgrade.running
        const reachedTarget = latestTag !== '' && machine.version === latestTag
        if (reachedTarget || doneOnServer) {
          // 只清本地的「升级中」。失败原文不往本地态里抄——服务端的 upgrade 段
          // 就是它的家，抄一份就有了两个真相，且刷新页面后本地那份还会消失。
          delete next[machine.name]
          changed = true
        }
      }
      return changed ? next : previous
    })
  }, [latestView?.tag, machines])

  const startMachineUpgrade = async (name: string, force: boolean) => {
    setMachineUpgradeStates((previous) => ({
      ...previous,
      [name]: { running: true, force: false, reason: '', remedy: '' },
    }))
    try {
      const result = await upgradeMachine(name, force)
      setMachineUpgradeStates((previous) => ({
        ...previous,
        [name]: {
          running: result.accepted,
          force: false,
          reason: result.accepted ? '' : (result.reason ?? ''),
          remedy: result.accepted ? '' : (result.remedy ?? ''),
        },
      }))
    } catch (err: unknown) {
      const body = err instanceof ApiError && isMachineUpgradeBody(err.body) ? err.body : undefined
      setMachineUpgradeStates((previous) => ({
        ...previous,
        [name]: {
          running: false,
          force: err instanceof ApiError && err.status === 409 && body?.forcible === true,
          reason: body?.reason ?? errorMessage(err),
          remedy: body?.remedy ?? '',
        },
      }))
    }
  }

  return (
    <div className="flex flex-col gap-5 p-4">
      {desktopState !== null && (
        <>
          <section>
            <div className="flex items-start justify-between gap-3">
              <div>
                <h2 className="text-sm font-semibold">桌面应用</h2>
                <p className="mt-1 text-xs text-muted-foreground">安装包由 agentd 下载并校验，最后一步由你拖进「应用程序」。</p>
              </div>
              <button
                type="button"
                onClick={() => void refreshLatest()}
                disabled={checking}
                className="shrink-0 rounded-md border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
              >
                {checking ? '检查中…' : '重新检查'}
              </button>
            </div>

            <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-6 gap-y-2 rounded-lg border bg-background p-3 text-xs">
              <dt className="text-muted-foreground">当前版本</dt>
              <dd>{desktopState.app_version || '—'}</dd>
              <dt className="text-muted-foreground">最新版本</dt>
              <dd>{latestView?.tag || '—'}</dd>
              <dt className="text-muted-foreground">最近检查</dt>
              <dd>{latestView?.checked_at || '从未检查'}</dd>
            </dl>

            <details className="mt-3 rounded-lg border px-3 py-2 text-xs">
              <summary className="cursor-pointer font-medium">变更摘要</summary>
              <p className="mt-2 text-muted-foreground">当前更新接口只提供版本号，暂无发布说明正文。</p>
            </details>

            {download.data?.stage === 'done' && download.data.path ? (
              <p className="mt-3 break-words rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3 text-xs text-emerald-700">
                已下载到 {download.data.path}，请拖进「应用程序」后重开。
              </p>
            ) : download.data?.stage === 'failed' ? (
              <p role="alert" className="mt-3 break-words text-xs text-destructive">
                {download.data.error || downloadError || '下载安装包失败'}
              </p>
            ) : downloadError !== '' ? (
              <p role="alert" className="mt-3 break-words text-xs text-destructive">{downloadError}</p>
            ) : null}

            <div className="mt-3 flex items-center gap-3">
              {download.data?.stage === 'downloading' || download.data?.stage === 'verifying' ? (
                <p className="text-xs text-muted-foreground">
                  {download.data.stage === 'verifying' ? '正在校验安装包…' : download.data.percent >= 0 ? `下载中 ${download.data.percent}%` : '正在下载…'}
                </p>
              ) : (
                <button
                  type="button"
                  onClick={() => void downloadPackage()}
                  className="rounded-md bg-primary px-2.5 py-1.5 text-xs text-primary-foreground hover:opacity-90"
                >
                  {download.data?.stage === 'done' ? `再次打开 ${downloadFileName(download.data.path ?? '')}` : '下载安装包'}
                </button>
              )}
            </div>
          </section>

          <section>
            <h2 className="text-sm font-semibold">同步状态</h2>
            <div className="mt-3 flex flex-col gap-2 rounded-lg border bg-background p-3 text-xs">
              {desktopState.sync_plan === 'blocked' && (
                <div className="flex items-start justify-between gap-3">
                  <span className="font-medium">待应用</span>
                  <span className="text-right text-muted-foreground">
                    {desktopState.sync_busy < 0 ? '活跃任务数暂时无法探测' : `${desktopState.sync_busy} 个活跃任务`}；任务结束后自动应用，想立刻应用就重开一次桌面端
                  </span>
                </div>
              )}
              {desktopState.sync_plan === 'failed' && (
                <div className="flex items-start justify-between gap-3">
                  <span className="font-medium text-destructive">上次同步失败</span>
                  <span className="break-words text-right text-muted-foreground">{desktopState.sync_error || '未提供失败原因'}</span>
                </div>
              )}
              {desktopState.sync_plan !== 'blocked' && desktopState.sync_plan !== 'failed' && (
                <div className="flex items-start justify-between gap-3">
                  <span className="font-medium">上次同步</span>
                  <span className="text-muted-foreground">{desktopState.sync_plan === 'done' ? '已完成' : desktopState.sync_plan || '未判定'}</span>
                </div>
              )}
            </div>
          </section>
        </>
      )}

      <section>
        <h2 className="text-sm font-semibold">执行机</h2>
        {machinesState.sessionExpired ? (
          <p className="mt-3 text-xs text-destructive">会话已失效，请重新打开控制台。</p>
        ) : machines.length === 0 ? (
          <p className="mt-3 text-xs text-muted-foreground">正在读取执行机…</p>
        ) : (
          <div className="mt-3 divide-y rounded-lg border bg-background">
            {machines.map((machine) => (
              <MachineUpdateRow
                key={machine.name || 'local'}
                machine={machine}
                latestTag={latestView?.tag ?? ''}
                state={machineUpgradeStates[machine.name]}
                onUpgrade={(force) => void startMachineUpgrade(machine.name, force)}
              />
            ))}
          </div>
        )}
        {machinesState.disconnected && !machinesState.sessionExpired && (
          <p className="mt-2 text-xs text-destructive">读取执行机失败：{machinesState.errorText}</p>
        )}
      </section>

      {checkError !== '' && <p role="alert" className="text-xs text-destructive">重新检查失败：{checkError}</p>}
    </div>
  )
}

function isMachineUpgradeBody(value: unknown): value is MachineUpgradeResp {
  return typeof value === 'object' && value !== null && 'verdict' in value && 'forcible' in value
}

// MachineUpdateRow 渲染一台执行机的更新状态；本机只显示随桌面应用更新，不接升级动作。
function MachineUpdateRow({ machine, latestTag, state, onUpgrade }: {
  machine: Machine
  latestTag: string
  state?: MachineUpgradeViewState
  onUpgrade: (force: boolean) => void
}) {
  const local = machine.name === ''
  // 版本比不出来（开发构建的版本戳是提交号）不等于「已是最新」。此前两者都落到
  // hasNewer=false，界面就把「我不知道」说成了「你没事」，还顺手把升级按钮藏了——
  // 而后端对这种机器的结论是 needs_upgrade，本来就该能升。
  const comparable = machine.version !== '' && isComparableVersion(machine.version)
  const upgradeAvailable = !local && machine.reachable && machine.version !== '' && latestTag !== '' &&
    (!comparable || hasNewer(latestTag, machine.version))
  // 服务端的 running 优先于本地态：刷新页面后本地态就没了，服务端仍知道在跑。
  const running = machine.upgrade?.running === true || state?.running === true
  // 两种失败来源，都要显示：
  //   本地态 = 请求当场被拒（409 忙 / 422 非托管），这类根本没进后台，服务端不留痕
  //   服务端 = 后台跑完但没成功，这是刷新页面后唯一还在的那份
  const serverFailed = machine.upgrade !== undefined && !machine.upgrade.running &&
    machine.upgrade.status !== undefined && machine.upgrade.status !== 'ok'
  const reason = state?.reason || (serverFailed ? (machine.upgrade?.reason ?? '') : '')
  const remedy = state?.remedy || (serverFailed ? (machine.upgrade?.remedy ?? '') : '')
  const statusText = local
    ? '随桌面应用一起更新'
    : running
      ? '升级中…'
      : !comparable
        ? '版本无法比较'
        : upgradeAvailable
          ? '可升级'
          : '已是最新'
  return (
    <div className="flex items-center gap-3 px-3 py-2.5 text-xs">
      <span className="w-28 shrink-0 font-medium">{machineLabel(machine)}</span>
      <span className="font-mono text-muted-foreground">{machine.version || '—'}</span>
      <span className={machine.reachable ? 'text-emerald-700' : 'text-amber-700'}>{machine.reachable ? '已连接' : '已断开'}</span>
      <div className="ml-auto flex items-center gap-2 text-right">
        <div className="text-muted-foreground">
          {statusText}
          {reason && <p className="mt-1 max-w-72 break-words text-destructive">{reason}</p>}
          {remedy && <p className="mt-1 max-w-72 break-words text-amber-700">{remedy}</p>}
        </div>
        {!local && upgradeAvailable && (
          running ? (
            <button type="button" disabled className="rounded-md border px-2 py-1 text-xs opacity-50">
              升级中…
            </button>
          ) : state?.force ? (
            <button
              type="button"
              onClick={() => onUpgrade(true)}
              className="rounded-md border border-amber-500 px-2 py-1 text-xs text-amber-700 hover:bg-amber-500/10"
            >
              仍要升级
            </button>
          ) : (
            <button
              type="button"
              onClick={() => onUpgrade(false)}
              className="rounded-md bg-primary px-2 py-1 text-xs text-primary-foreground hover:opacity-90"
            >
              升级到 {latestTag}
            </button>
          )
        )}
      </div>
    </div>
  )
}
