// MachineEnv —— 开发机详情里的「Env 文件」块（B158 spec §2.2）。
//
// 职责：给这台机器的每个 executor 指定注入哪个 env 文件（**两档**），整块一次保存。
//
// **两档不是三档**：env 没有内置默认。「不注入」在配置里表现为**键不存在**，
// 不是空串——照抄 MachineDiscipline 的三档翻译会写出脏数据（见 spec §2.3）。
//
// 形态基准：prototypes/discipline-config/pages/settings.html 的映射块。
//
// 边界：
//   - **不编辑正文**：正文在设置页的「Env 文件」分区里改，这里只选文件
//   - 不轮询：进入详情拉一次，保存后用响应刷新（响应就是最新状态）
//   - 机器断开时不发请求、不渲染控件——配置读不到也写不了，画出来只会骗人
//
// 下拉的 value 编码：'off' / `file:<文件名>`。用前缀而不是裸文件名，是为了让一个
// 名叫 "off" 的文件不会与「不注入」撞值。
import { useEffect, useMemo, useState } from 'react'
import type { EnvBinding, EnvResp, Machine } from '../../api/types'
import { fetchEnv, saveEnvMapping } from '../../api/client'
import { errorMessage } from '../lib/format'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

function bindingValue(binding: EnvBinding): string {
  if (binding.mode === 'file') return `file:${binding.file ?? ''}`
  return 'off'
}

function decodeBinding(executor: string, value: string): EnvBinding {
  if (value === 'off') return { executor, mode: 'off' }
  return {
    executor,
    mode: 'file',
    file: value.startsWith('file:') ? value.slice('file:'.length) : value,
  }
}

function bindingDescription(binding: EnvBinding): string {
  if (binding.mode === 'off') return '未配置——启动时不注入任何环境变量'
  return '正文在「Env 文件」分区里编辑'
}

// MachineEnv 展示并保存一台机器的 executor→env 文件映射。
export function MachineEnv({ machine }: { machine: Machine }) {
  const [response, setResponse] = useState<EnvResp | null>(null)
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
    void fetchEnv(machine.name)
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
        binding.executor,
        edits[binding.executor] ?? bindingValue(binding),
      ))
      const next = await saveEnvMapping(machine.name, nextBindings)
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
        <h3 className="font-medium text-amber-700">Env 文件</h3>
        <p className="mt-1 text-foreground/80">机器已断开：{machine.error}</p>
      </section>
    )
  }

  return (
    <section className="mt-4 rounded-lg border bg-background p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">Env 文件</h3>
        {dirty && <span className="text-xs text-amber-700">有未保存的改动</span>}
      </div>
      {error && <p role="alert" className="mt-2 break-words text-xs text-destructive">{error}</p>}
      {response === null ? (
        <p className="mt-3 text-xs text-muted-foreground">{error ? 'env 配置读取失败' : '正在加载 env 配置…'}</p>
      ) : bindings.length === 0 ? (
        <p className="mt-3 text-xs text-muted-foreground">该机器没有上报可用 executor。</p>
      ) : (
        <div className="mt-3 flex flex-col gap-2">
          {bindings.map((binding) => {
            const value = edits[binding.executor] ?? bindingValue(binding)
            const shownBinding = decodeBinding(binding.executor, value)
            return (
              <label key={binding.executor} className="grid grid-cols-[max-content_minmax(0,1fr)] items-center gap-3 text-xs">
                <span className="font-medium">{binding.executor}</span>
                <div className="min-w-0">
                  <select
                    aria-label={`${binding.executor} 的 env 文件`}
                    value={value}
                    onChange={(event) => setEdits((current) => ({ ...current, [binding.executor]: event.target.value }))}
                    className={cn(
                      'h-8 w-full min-w-0 rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring',
                      dirty && 'border-amber-500/60',
                    )}
                  >
                    <option value="off">不注入</option>
                    {response.files.map((file) => (
                      <option key={file.name} value={`file:${file.name}`}>{file.name}</option>
                    ))}
                  </select>
                  <span className="mt-0.5 block text-[11px] text-muted-foreground">
                    {bindingDescription(shownBinding)}
                  </span>
                </div>
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
