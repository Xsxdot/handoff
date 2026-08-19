// 控制台右下角更新提示框。
//
// 职责：把桌面薄壳的新版、待应用同步与同步失败状态堆叠在右下角，并把下载动作
// 交给 agentd；关闭记录只活在本次浏览器会话里。
// 边界：浏览器页面不渲染任何提示；这里不实现桌面端换版，也不维护下载进度副本。
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { startDownload } from '../../api/client'
import { useDesktopState, useDownload, useLatest } from '../data/useUpdate'
import { isDesktopShell } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { hasNewer } from '../lib/version'

type ToastKind = 'new-version' | 'blocked' | 'failed'

interface ToastCandidate {
  kind: ToastKind
  tag: string
  title: string
  description: string
}

interface UpdateToastsProps {
  // homeOpen 直接来自 Shell 持有的 useHomeDock 状态；不另造全局状态。
  homeOpen: boolean
}

const DISMISSED_PREFIX = 'handoff:update-toast:'

// dismissedKey 以 (kind, tag) 标识一条提示；新版本发布后同类提示自然重新出现。
function dismissedKey(kind: ToastKind, tag: string): string {
  return `${DISMISSED_PREFIX}${kind}:${tag}`
}

// wasDismissed 读取 sessionStorage；存储不可用时按未关闭处理，不让提示系统阻断页面。
function wasDismissed(key: string): boolean {
  try {
    return sessionStorage.getItem(key) === '1'
  } catch {
    return false
  }
}

// rememberDismissed 写本次会话的关闭标记。
function rememberDismissed(key: string): void {
  try {
    sessionStorage.setItem(key, '1')
  } catch {
    // 隐私模式等环境可能禁用 sessionStorage；内存状态仍会在当前挂载中生效。
  }
}

// fileName 从 agentd 返回的绝对路径中取安装包文件名。
function fileName(path: string): string {
  return path.split(/[\\/]/).pop() || path
}

// UpdateToasts 渲染桌面壳内的可关闭更新提示。
export function UpdateToasts({ homeOpen }: UpdateToastsProps) {
  const desktop = useDesktopState()
  const latest = useLatest()
  const [downloadRequested, setDownloadRequested] = useState(false)
  const [downloadStartError, setDownloadStartError] = useState('')
  const [dismissedKeys, setDismissedKeys] = useState<Set<string>>(() => new Set())
  const state = desktop.data
  const latestTag = latest.data?.tag ?? ''
  const hasNewVersion = state !== null && state !== undefined && state.app_version !== '' && latestTag !== ''
    ? hasNewer(latestTag, state.app_version)
    : false
  // 有新版提示存在时就保持 1s 进度轮询：这样刷新页面后也能接回 agentd 内存中的进度。
  const download = useDownload(hasNewVersion || downloadRequested)

  if (!isDesktopShell() || state === null || state === undefined) return null

  const tag = latestTag || state.app_version || 'unknown'
  const candidates: ToastCandidate[] = []
  if (hasNewVersion) {
    candidates.push({
      kind: 'new-version',
      tag,
      title: `有新版 ${latestTag} 可下载`,
      description: '桌面应用安装包会由 agentd 下载并校验，完成后打开下载位置。',
    })
  }
  if (state.sync_plan === 'blocked') {
    const busy = state.sync_busy < 0 ? '活跃任务数暂时无法探测' : `${state.sync_busy} 个任务正在进行`
    candidates.push({
      kind: 'blocked',
      tag,
      title: '有更新待应用',
      description: `${busy}，会在任务结束后自动应用；想立刻应用就重开一次桌面端。`,
    })
  }
  if (state.sync_plan === 'failed') {
    candidates.push({
      kind: 'failed',
      tag,
      title: '上次同步失败',
      description: state.sync_error || '桌面应用同步失败，请打开更新页查看详情。',
    })
  }

  const visible = candidates.filter((candidate) => {
    const key = dismissedKey(candidate.kind, candidate.tag)
    return !dismissedKeys.has(key) && !wasDismissed(key)
  })
  if (visible.length === 0) return null

  const close = (candidate: ToastCandidate) => {
    const key = dismissedKey(candidate.kind, candidate.tag)
    rememberDismissed(key)
    setDismissedKeys((prev) => new Set(prev).add(key))
    setDownloadStartError('')
  }

  const downloadCandidate = (candidate: ToastCandidate) => {
    if (candidate.kind !== 'new-version') return
    setDownloadRequested(true)
    setDownloadStartError('')
    void startDownload().catch((err: unknown) => setDownloadStartError(errorMessage(err)))
  }

  return (
    <section
      aria-label="更新提示"
      className={`fixed right-5 z-50 flex w-[min(380px,calc(100vw-2.5rem))] flex-col gap-2 ${homeOpen ? 'bottom-[236px]' : 'bottom-5'}`}
    >
      {visible.map((candidate) => {
        const key = dismissedKey(candidate.kind, candidate.tag)
        const isDownload = candidate.kind === 'new-version'
        const downloadForCandidate = download.data?.tag === candidate.tag ? download.data : null
        const stage = downloadForCandidate?.stage ?? 'idle'
        const downloading = isDownload && (downloadRequested || downloadForCandidate !== null) &&
          (stage === 'downloading' || stage === 'verifying')
        const done = isDownload && downloadForCandidate?.stage === 'done'
        const failed = isDownload && downloadForCandidate?.stage === 'failed'

        return (
          <article key={key} className="rounded-lg border bg-background p-3 shadow-xl">
            <div className="flex items-start gap-2">
              <div className="min-w-0 flex-1">
                <h2 className="text-sm font-semibold">{candidate.title}</h2>
                {done ? (
                  <p className="mt-1 break-words text-xs text-emerald-700">
                    {downloadForCandidate?.opened
                      ? `已下载 ${fileName(downloadForCandidate.path ?? '')}，已在访达中打开`
                      : `已下载到 ${downloadForCandidate?.path ?? '下载目录'}`}
                  </p>
                ) : (
                  <p className="mt-1 break-words text-xs text-muted-foreground">
                    {failed && downloadForCandidate?.error
                      ? downloadForCandidate.error
                      : downloadStartError || candidate.description}
                  </p>
                )}
              </div>
              <button
                type="button"
                aria-label={`关闭${candidate.title.split(' ')[0]}`}
                onClick={() => close(candidate)}
                className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                ×
              </button>
            </div>

            {downloading && (
              <div className="mt-3" aria-label="下载进度">
                <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-primary transition-all"
                    style={{ width: `${downloadForCandidate?.percent !== undefined && downloadForCandidate.percent >= 0 ? downloadForCandidate.percent : 8}%` }}
                  />
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  {stage === 'verifying' ? '正在校验安装包…' : downloadForCandidate?.percent !== undefined && downloadForCandidate.percent >= 0 ? `下载中 ${downloadForCandidate.percent}%` : '正在下载…'}
                </p>
              </div>
            )}

            <div className="mt-3 flex items-center gap-3">
              {isDownload && !done && !downloading ? (
                <button
                  type="button"
                  onClick={() => downloadCandidate(candidate)}
                  className="rounded-md bg-primary px-2.5 py-1.5 text-xs text-primary-foreground hover:opacity-90"
                >
                  {failed ? '重试' : '下载'}
                </button>
              ) : !isDownload ? (
                <button
                  type="button"
                  onClick={() => close(candidate)}
                  className="rounded-md bg-primary px-2.5 py-1.5 text-xs text-primary-foreground hover:opacity-90"
                >
                  知道了
                </button>
              ) : null}
              <Link
                to="/settings?section=update"
                className="text-xs text-muted-foreground hover:text-foreground hover:underline"
              >
                查看详情
              </Link>
            </div>
          </article>
        )
      })}
    </section>
  )
}
