// MachineDiscipline —— 开发机详情里的「执行纪律」块（B157 spec §2.2）。
//
// 职责：给这台机器的每个 executor 指定注入哪块纪律（三档），整块一次保存。
//
// 形态基准：prototypes/discipline-config/pages/settings.html。
//
// 边界：
//   - **不编辑正文**：正文在设置页的「执行纪律」分区里改，这里只选文件
//   - 不轮询：进入详情拉一次，保存后用响应刷新（响应就是最新状态）
//   - 机器断开时不发请求、不渲染控件——配置读不到也写不了，画出来只会骗人
//
// 下拉的 value 编码：'default' / 'off' / `file:<文件名>`。用前缀而不是裸文件名，
// 是为了让一个名叫 "off" 的文件不会与「关闭注入」撞值。
import { useEffect, useMemo, useState } from 'react'
import type { DisciplineBinding, DisciplineResp, Machine } from '../../api/types'
import { fetchDiscipline, saveDisciplineMapping } from '../../api/client'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

function bindingValue(binding: DisciplineBinding): string {
  if (binding.mode === 'file') return `file:${binding.file ?? ''}`
  return binding.mode
}

function decodeBinding(binding: DisciplineBinding, value: string): DisciplineBinding {
  if (value === 'default') {
    return { executor: binding.executor, mode: 'default', default_tier: binding.default_tier }
  }
  if (value === 'off') {
    return { executor: binding.executor, mode: 'off', default_tier: binding.default_tier }
  }
  return {
    executor: binding.executor,
    mode: 'file',
    file: value.startsWith('file:') ? value.slice('file:'.length) : value,
    default_tier: binding.default_tier,
  }
}

export function MachineDiscipline({ machine }: { machine: Machine }) {
  const [response, setResponse] = useState<DisciplineResp | null>(null)
  const [edits, setEdits] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dirty = Object.keys(edits).length > 0

  useEffect(() => {
    setResponse(null)
    setEdits({})
    setError('')
    if (!machine.reachable) return
    let cancelled = false
    void fetchDiscipline(machine.name)
      .then((next) => {
        if (!cancelled) setResponse(next)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [machine.name, machine.reachable])

  const bindings = useMemo(
    () => response?.bindings
      .filter((binding) => machine.executors.includes(binding.executor))
      .sort((a, b) => a.executor.localeCompare(b.executor)) ?? [],
    [machine.executors, response],
  )

  const save = async () => {
    if (!response || !dirty) return
    setBusy(true)
    setError('')
    try {
      const nextBindings = bindings.map((binding) => decodeBinding(
        binding,
        edits[binding.executor] ?? bindingValue(binding),
      ))
      const next = await saveDisciplineMapping(machine.name, nextBindings)
      setResponse(next)
      setEdits({})
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!machine.reachable) {
    return (
      <section className="mt-4 rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm">
        <h3 className="font-medium text-amber-700">执行纪律</h3>
        <p className="mt-1 text-foreground/80">机器已断开：{machine.error}</p>
      </section>
    )
  }

  return (
    <section className="mt-4 rounded-lg border bg-background p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">执行纪律</h3>
        {dirty && <span className="text-xs text-amber-700">有未保存的改动</span>}
      </div>
      {error && <p role="alert" className="mt-2 break-words text-xs text-destructive">{error}</p>}
      {response === null ? (
        <p className="mt-3 text-xs text-muted-foreground">{error ? '纪律配置读取失败' : '正在加载纪律配置…'}</p>
      ) : bindings.length === 0 ? (
        <p className="mt-3 text-xs text-muted-foreground">该机器没有上报可用 executor。</p>
      ) : (
        <div className="mt-3 flex flex-col gap-2">
          {bindings.map((binding) => {
            const value = edits[binding.executor] ?? bindingValue(binding)
            return (
              <label key={binding.executor} className="grid grid-cols-[max-content_minmax(0,1fr)] items-center gap-3 text-xs">
                <span className="font-medium">{binding.executor}</span>
                <select
                  aria-label={`${binding.executor} 的纪律块`}
                  value={value}
                  onChange={(event) => setEdits((current) => ({ ...current, [binding.executor]: event.target.value }))}
                  className={cn(
                    'h-8 min-w-0 rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring',
                    dirty && 'border-amber-500/60',
                  )}
                >
                  <option value="default">内置默认（{binding.default_tier}）</option>
                  {response.files.map((file) => (
                    <option key={file.name} value={`file:${file.name}`}>{file.name}</option>
                  ))}
                  <option value="off">关闭注入（不发纪律块）</option>
                </select>
              </label>
            )
          })}
          <div className="mt-2 flex items-center justify-between gap-2 border-t pt-3">
            <p className="text-[11px] text-muted-foreground">保存后下一个任务即生效，不必重启 agentd。</p>
            <Button size="sm" onClick={() => void save()} disabled={busy || !dirty}>保存</Button>
          </div>
        </div>
      )}
    </section>
  )
}
