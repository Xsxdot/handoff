// MachineExecutor —— 开发机详情里的「缺省执行者」块（B160 spec §2.2）。
//
// 职责：改这台机器不带 --executor 派发时用哪个执行者，以及**它的**默认模型。
//
// **model 是「default 的模型」，不是全局默认**：agentd 的 resolveModel 只在
// execName == default 时才套用它，派别的执行器返回空串。所以：
//   - 两项必须同块、共用一个保存按钮，中间不插别的东西
//   - model 输入框的标签随 default 变（「opencode 的默认模型」→「codex 的…」），
//     让「改 default 会连带改变 model 的作用对象」这个效应在保存前就可见
//
// **model 服务端不校验**：agentd 不认识任何执行器的模型名单，没有可判据
//（模型名按执行器、也按机器不同）。这是「用文案承担校验」的少数正当场合。
//
// 边界：
//   - 缺省执行者只能从 available 里选，不给自由输入：填一个该机没有的名字，
//     此后每一次不带 --executor 的派发都会失败（服务端还有第二道校验）
//   - 不轮询：进入详情拉一次，保存后用响应刷新
//   - 机器断开时不发请求、不渲染控件
import { useEffect, useState } from 'react'
import type { ExecutorDefaultReq, ExecutorDefaultResp, Machine } from '../../api/types'
import { fetchExecutorDefault, saveExecutorDefault } from '../../api/client'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// MachineExecutor 展示并保存一台机器的缺省执行者及其默认模型。
export function MachineExecutor({ machine }: { machine: Machine }) {
  const [response, setResponse] = useState<ExecutorDefaultResp | null>(null)
  const [draftDefault, setDraftDefault] = useState('')
  const [draftModel, setDraftModel] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dirty = response !== null &&
    (draftDefault !== response.default || draftModel !== response.model)

  useEffect(() => {
    setResponse(null)
    setDraftDefault('')
    setDraftModel('')
    setError('')
    if (!machine.reachable) return
    let cancelled = false
    void fetchExecutorDefault(machine.name)
      .then((next) => {
        if (cancelled) return
        setResponse(next)
        setDraftDefault(next.default)
        setDraftModel(next.model)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [machine.name, machine.reachable])

  const save = async () => {
    if (response === null || !dirty) return
    setBusy(true)
    setError('')
    const req: ExecutorDefaultReq = { default: draftDefault, model: draftModel }
    try {
      const next = await saveExecutorDefault(machine.name, req)
      setResponse(next)
      setDraftDefault(next.default)
      setDraftModel(next.model)
    } catch (err: unknown) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!machine.reachable) {
    return (
      <section className="mt-4 rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm">
        <h3 className="font-medium text-amber-700">缺省执行者</h3>
        <p className="mt-1 text-foreground/80">机器已断开：{machine.error}</p>
      </section>
    )
  }

  return (
    <section className="mt-4 rounded-lg border bg-background p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">缺省执行者</h3>
        {dirty && <span className="text-xs text-amber-700">有未保存的改动</span>}
      </div>
      {error && <p role="alert" className="mt-2 break-words text-xs text-destructive">{error}</p>}
      {response === null ? (
        <p className="mt-3 text-xs text-muted-foreground">{error ? '缺省执行者配置读取失败' : '正在加载缺省执行者配置…'}</p>
      ) : (
        <div className="mt-3 flex flex-col gap-3">
          <label className="flex flex-col gap-1 text-xs">
            <span className="font-medium">缺省执行者</span>
            <select
              aria-label="缺省执行者"
              value={draftDefault}
              onChange={(event) => setDraftDefault(event.target.value)}
              className={cn(
                'h-8 w-full rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring',
                dirty && 'border-amber-500/60',
              )}
            >
              {response.available.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </label>

          <label className="flex flex-col gap-1 text-xs">
            <span className="font-medium">{draftDefault} 的默认模型</span>
            <input
              type="text"
              aria-label={`${draftDefault} 的默认模型`}
              value={draftModel}
              onChange={(event) => setDraftModel(event.target.value)}
              placeholder={`留空——用 ${draftDefault} 自己的默认模型`}
              className={cn(
                'h-8 w-full rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring',
                dirty && 'border-amber-500/60',
              )}
            />
            <span className="mt-0.5 text-[11px] text-muted-foreground">
              只对上面选的缺省执行者生效。派其他执行器时用 <code>--model</code> 逐次指定。
            </span>
          </label>

          <div className="flex items-center justify-between gap-2 border-t pt-3">
            <p className="text-[11px] text-muted-foreground">保存后下一个任务即生效，不必重启 agentd。</p>
            <Button size="sm" onClick={() => void save()} disabled={busy || !dirty}>保存</Button>
          </div>
        </div>
      )}
    </section>
  )
}
