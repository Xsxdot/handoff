// AddProjectWizard —— 单页、本机优先的项目登记表单 + 逐位置结果列表。
//
// 单页照 spec §5 的干净流程：
//   - 本机固定必选（不再是可取消的 checkbox），path 必填，Git 地址选填——path
//     已有仓时留空由 agentd 现读 origin；name 选填，空则由后端从 origin 末段派生
//   - 开发机可选（勾选后单选至多一台，ADR-0008），**不再填 URL**——远程一律复用
//     本机登记响应里的权威 origin/name（registerFromForm 编排）；远程 path 选填，
//     空 = 该机 clone 到自己的 repo_root/<name>
//   - 不做浏览器侧 path 探测：path 是否存在、是不是仓，只有目标机的 agentd 知道；
//     path 不存在且无 URL 的错误交给后端 400 原文透传
//
// 提交：registerFromForm（本机优先编排，含「本机失败 → 远程未尝试」那一行）；任一
// 成功即调 onDone 让父级 refresh 项目树；结果面板逐位置显示，失败的可「重试」。远程
// 重试用本机成功结果的 origin/name；本机也失败时仅当表单填了 Git 地址才允许远程单独
// 重试，否则禁用并提示先修本机。
import { useEffect, useMemo, useState } from 'react'
import { X } from 'lucide-react'
import type { Machine } from '../../api/types'
import { Button } from '@/components/ui/button'
import { registerAll, registerFromForm, type LocationChoice, type RegisterOutcome } from './register'

export interface AddProjectWizardProps {
  open: boolean
  machines: Machine[]
  onClose: () => void
  onDone: () => void
}

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(name: string): string {
  return name === '' ? '本机' : name
}

const inputClass =
  'h-8 rounded-md border border-input bg-background px-2.5 text-xs shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function AddProjectWizard({ open, machines, onClose, onDone }: AddProjectWizardProps) {
  const [view, setView] = useState<'form' | 'results'>('form')
  const [name, setName] = useState('')
  const [localPath, setLocalPath] = useState('')
  const [gitUrl, setGitUrl] = useState('')
  const [remoteEnabled, setRemoteEnabled] = useState(false)
  const [remoteMachine, setRemoteMachine] = useState<string | null>(null)
  const [remotePath, setRemotePath] = useState('')
  const [outcomes, setOutcomes] = useState<RegisterOutcome[] | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // 每次重新打开都重置到表单（对话框复用，不能带着上次的填写/结果）。
  useEffect(() => {
    if (open) {
      setView('form')
      setName('')
      setLocalPath('')
      setGitUrl('')
      setRemoteEnabled(false)
      setRemoteMachine(null)
      setRemotePath('')
      setOutcomes(null)
      setSubmitting(false)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const remoteMachines = useMemo(() => machines.filter((m) => m.name !== ''), [machines])

  // path 必填；gitUrl 不作要求（path 不存在且无 URL 的错误交给后端 400 原文）；
  // 勾了远程就必须选定一台机器。
  const canSubmit = localPath.trim() !== '' && (!remoteEnabled || remoteMachine !== null)

  const submit = async () => {
    setSubmitting(true)
    // 「本机失败 → 远程未尝试」那一行由 registerFromForm 产出（编排知识只有一份）。
    const result = await registerFromForm({
      name,
      localPath,
      gitUrl,
      remoteMachine: remoteEnabled ? remoteMachine : null,
      remotePath,
    })
    setOutcomes(result)
    setSubmitting(false)
    setView('results')
    if (result.some((o) => o.ok)) onDone()
  }

  // 本机是否已有成功结果——决定远程重试能用权威 origin 还是只能退到表单 gitUrl。
  const localSucceeded = (outcomes ?? []).some((o) => o.machine === '' && o.ok)
  const canRetryRemote = localSucceeded || gitUrl.trim() !== ''

  const retry = async (machine: string) => {
    let choice: LocationChoice
    if (machine === '') {
      // 本机重试：用表单字段原样再打一次
      choice = { machine: '', originUrl: gitUrl, name, path: localPath }
    } else {
      // 远程重试：优先本机成功结果里的权威 origin/name；本机也失败时退到表单
      // gitUrl（调用方保证 canRetryRemote，即此时 gitUrl 非空）。
      const local = (outcomes ?? []).find((o) => o.machine === '')
      if (local?.ok && local.result) {
        choice = { machine, originUrl: local.result.origin_url, name: local.result.name, path: remotePath }
      } else {
        // 远程重试：优先本机成功结果里的权威 origin/name；本机也失败时退到表单
        // gitUrl + name（调用方保证 canRetryRemote，即此时 gitUrl 非空）。
        // name 必须一起退，否则远程会按 origin 末段自己派生一个，跟用户在表单里
        // 填的名字对不上——两台机器上同一个项目叫两个名字是最难查的那类问题。
        choice = { machine, originUrl: gitUrl, name, path: remotePath }
      }
    }
    const result = await registerAll([choice])
    setOutcomes((prev) => (prev ?? []).map((o) => (o.machine === machine ? result[0] : o)))
    if (result[0].ok) onDone()
  }

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="wizard-title"
        className="w-full max-w-lg rounded-lg border bg-background p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <h2 id="wizard-title" className="text-base font-semibold">
            添加项目
          </h2>
          <button
            type="button"
            aria-label="关闭"
            onClick={onClose}
            className="rounded p-1 text-muted-foreground hover:text-foreground"
          >
            <X className="size-4" />
          </button>
        </div>

        {view === 'form' && (
          <div className="mt-4 flex flex-col gap-4">
            <label className="flex flex-col gap-1 text-sm">
              <span className="font-medium">名称</span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="可选；默认用仓库名"
                className={inputClass}
              />
            </label>

            <div className="flex flex-col gap-2 rounded-md border p-3">
              <p className="text-sm font-medium">本机</p>
              <input
                value={localPath}
                onChange={(e) => setLocalPath(e.target.value)}
                placeholder="本机目录路径（必填）"
                className={inputClass}
              />
              <input
                value={gitUrl}
                onChange={(e) => setGitUrl(e.target.value)}
                placeholder="Git 地址（可选；path 已有仓时可留空自动读取）"
                className={inputClass}
              />
            </div>

            <div className="flex flex-col gap-2 rounded-md border p-3">
              <label className="flex cursor-pointer items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={remoteEnabled}
                  onChange={(e) => setRemoteEnabled(e.target.checked)}
                />
                <span className="font-medium">同时登记到开发机</span>
              </label>
              {remoteEnabled && (
                <>
                  {remoteMachines.map((m) => (
                    <label
                      key={m.name}
                      className="flex cursor-pointer items-center gap-2 rounded-md border p-3 text-sm hover:bg-accent/40"
                    >
                      <input
                        type="radio"
                        name="remote-machine"
                        checked={remoteMachine === m.name}
                        onChange={() => setRemoteMachine(m.name)}
                      />
                      <span className="font-medium">{m.name}</span>
                      {!m.reachable && <span className="ml-auto text-xs text-amber-600">（登记可能失败）</span>}
                    </label>
                  ))}
                  <input
                    value={remotePath}
                    onChange={(e) => setRemotePath(e.target.value)}
                    placeholder="远程目录路径（可选；留空由该机器 clone 到自己的 repo_root）"
                    className={inputClass}
                  />
                  <p className="text-[11px] text-muted-foreground">
                    远程复用本机登记到的仓库地址，无需再填 Git URL
                  </p>
                </>
              )}
            </div>

            <div className="mt-2 flex justify-end gap-2">
              <Button variant="outline" onClick={onClose}>
                取消
              </Button>
              <Button onClick={() => void submit()} disabled={!canSubmit || submitting}>
                {submitting ? '提交中…' : '提交'}
              </Button>
            </div>
          </div>
        )}

        {view === 'results' && outcomes && (
          <div className="mt-4 flex flex-col gap-2">
            <p className="text-sm text-muted-foreground">登记结果（逐位置）：</p>
            {outcomes.map((o) => {
              const retryDisabled = o.machine !== '' && !canRetryRemote
              // skipped 行的文案按**当前**状态算，不用提交那一刻烤死的串：
              // 本机随后重试成功时，"未尝试"的原因就从"本机失败"变成"只差点一下"。
              const message = o.skipped
                ? localSucceeded
                  ? '未尝试：本机已登记，点重试即可登记远程'
                  : '未尝试：本机登记失败'
                : o.error
              return (
                <div key={o.machine} className="flex items-center gap-2 rounded-md border p-3 text-sm">
                  <span className="font-medium">{machineLabel(o.machine)}</span>
                  {o.ok ? (
                    <span className="text-emerald-600">已登记</span>
                  ) : (
                    <>
                      <span className="min-w-0 flex-1 break-words text-destructive">
                        {message}
                        {retryDisabled && (
                          <span className="block text-[11px] text-muted-foreground">
                            先修好本机，或在本机区块填 Git 地址后再重试远程
                          </span>
                        )}
                      </span>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={retryDisabled}
                        onClick={() => void retry(o.machine)}
                      >
                        重试
                      </Button>
                    </>
                  )}
                </div>
              )
            })}
            <div className="mt-2 flex justify-end">
              <Button onClick={onClose}>完成</Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
