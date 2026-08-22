// MachineLaunchers —— 开发机详情里的「自定义启动项」配置块。
//
// 职责：编辑一台机器的启动项、从 Env 文件列表提供下拉选项，并把服务端保存后的
// 最新列表重新显示出来。
//
// 边界：启动项能力由 machine.launchers_supported 决定；命令只作为表单数据展示，
// 不在前端日志中输出。服务端仍是校验与 env_missing 的唯一权威。
import { useEffect, useMemo, useState } from 'react'
import type { EnvResp, Launcher, LaunchersResp, Machine } from '../../api/types'
import { fetchEnv, fetchLaunchers, putLaunchers } from '../../api/client'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

function emptyLauncher(): Launcher {
  return { name: '', env_file: '', command: '', env_missing: false }
}

function sameLaunchers(a: Launcher[], b: Launcher[]): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

function validateDraft(list: Launcher[]): string {
  for (let i = 0; i < list.length; i++) {
    const item = list[i]
    const name = item.name.trim()
    if (!name) return `第 ${i + 1} 条启动项的名字不能为空`
    if (!item.env_file?.trim() && !item.command?.trim()) {
      return `启动项「${name}」的 Env 文件与执行命令至少填一个`
    }
  }
  return ''
}

function MachineLaunchersForm({ machine }: { machine: Machine }) {
  const [response, setResponse] = useState<LaunchersResp | null>(null)
  const [envResponse, setEnvResponse] = useState<EnvResp | null>(null)
  const [draft, setDraft] = useState<Launcher[]>([])
  const [saved, setSaved] = useState<Launcher[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dirty = !sameLaunchers(draft, saved)

  useEffect(() => {
    setResponse(null)
    setEnvResponse(null)
    setDraft([])
    setSaved([])
    setError('')
    if (!machine.reachable) return
    let cancelled = false
    void Promise.all([fetchLaunchers(machine.name), fetchEnv(machine.name)])
      .then(([next, env]) => {
        if (cancelled) return
        setResponse(next)
        setEnvResponse(env)
        setDraft(next.launchers)
        setSaved(next.launchers)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [machine.name, machine.reachable])

  const files = useMemo(() => envResponse?.files ?? [], [envResponse])

  const update = (index: number, changes: Partial<Launcher>) => {
    setDraft((current) => current.map((item, i) => i === index ? { ...item, ...changes } : item))
  }

  const save = async () => {
    if (!dirty) return
    const validationError = validateDraft(draft)
    if (validationError) {
      // 前端校验只是为了少一次往返，不是权威；服务端保存时还会再次校验。
      setError(validationError)
      return
    }
    setBusy(true)
    setError('')
    try {
      const next = await putLaunchers(machine.name, draft)
      // 不本地乐观更新：服务端可能 trim 名字并重算 env_missing，本地草稿不是最终真相。
      setResponse(next)
      setDraft(next.launchers)
      setSaved(next.launchers)
    } catch (err: unknown) {
      // 服务端 400 的中文原文要原样展示，它已经点名了哪一条启动项。
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!machine.reachable) {
    return (
      <section className="mt-4 rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm">
        <h3 className="font-medium text-amber-700">自定义启动项</h3>
        <p className="mt-1 text-foreground/80">机器已断开：{machine.error}</p>
      </section>
    )
  }

  return (
    <section className="mt-4 rounded-lg border bg-background p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">自定义启动项</h3>
        {dirty && <span className="text-xs text-amber-700">有未保存的改动</span>}
      </div>
      {error && <p role="alert" className="mt-2 break-words text-xs text-destructive">{error}</p>}
      {response === null || envResponse === null ? (
        <p className="mt-3 text-xs text-muted-foreground">{error ? '启动项配置读取失败' : '正在加载自定义启动项…'}</p>
      ) : (
        <div className="mt-3 flex flex-col gap-3">
          {draft.length === 0 && (
            <p className="text-xs text-muted-foreground">还没有自定义启动项。</p>
          )}
          {draft.map((item, index) => (
            <div key={`${index}-${item.name}`} className="rounded-md border p-3">
              <div className="flex items-start justify-between gap-2">
                <span className="text-xs font-medium">第 {index + 1} 条启动项</span>
                <Button variant="ghost" size="sm" onClick={() => setDraft((current) => current.filter((_, i) => i !== index))}>
                  删除
                </Button>
              </div>
              <div className="mt-2 flex flex-col gap-2">
                <label className="flex flex-col gap-1 text-xs">
                  <span>名称</span>
                  <input
                    aria-label={`第 ${index + 1} 条启动项名称`}
                    value={item.name}
                    onChange={(event) => update(index, { name: event.target.value })}
                    className={cn('h-8 rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring', dirty && 'border-amber-500/60')}
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs">
                  <span>Env 文件</span>
                  <select
                    aria-label={`第 ${index + 1} 条启动项 env 文件`}
                    value={item.env_file ?? ''}
                    onChange={(event) => update(index, { env_file: event.target.value })}
                    className="h-8 rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  >
                    <option value="">不指定</option>
                    {item.env_file && item.env_missing && (
                      <option value={item.env_file}>缺失：{item.env_file}</option>
                    )}
                    {files.map((file) => <option key={file.name} value={file.name}>{file.name}</option>)}
                  </select>
                  {item.env_missing && item.env_file && (
                    <span className="text-[11px] text-amber-700">env 文件缺失：{item.env_file}</span>
                  )}
                </label>
                <label className="flex flex-col gap-1 text-xs">
                  <span>启动命令</span>
                  <input
                    aria-label={`第 ${index + 1} 条启动项命令`}
                    value={item.command ?? ''}
                    onChange={(event) => update(index, { command: event.target.value })}
                    className="h-8 rounded-md border bg-background px-2 font-mono text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  />
                </label>
              </div>
            </div>
          ))}
          <Button variant="outline" size="sm" onClick={() => setDraft((current) => [...current, emptyLauncher()])}>
            添加启动项
          </Button>
          <p className="text-[11px] text-muted-foreground">本地校验只是减少往返，服务端保存时仍会再次校验。</p>
          <div className="flex items-center justify-between gap-2 border-t pt-3">
            <p className="text-[11px] text-muted-foreground">保存后新开的终端即可使用。</p>
            <Button size="sm" onClick={() => void save()} disabled={busy || !dirty}>保存</Button>
          </div>
        </div>
      )}
    </section>
  )
}

export function MachineLaunchers({ machine }: { machine: Machine }) {
  // 三态门：只有明确 true 才渲染；undefined 是旧版远端 agentd，不应画出存不进去的表单。
  if (machine.launchers_supported !== true) return null
  return <MachineLaunchersForm machine={machine} />
}
