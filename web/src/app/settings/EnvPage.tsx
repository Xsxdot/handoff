// EnvPage —— 设置页「Env 文件」分区（B158 spec §2.1）。
//
// 职责：按机器查看与编辑 <DataDir>/env/ 下的 env 文件。
//
// **默认视图是变量清单，不是正文**：日常最高频的问题是「这台机给某个 executor
// 注了哪些变量」，回答它不需要看见任何一个值。点「编辑正文」才拉含值全文。
//
// 诚实的边界（spec §7）：点「编辑正文」后全文（含值）确实会到浏览器——不然
// 没法编辑。默认掩码防的是肩窥、截图、录屏、把整页贴给别人，**不是防浏览器
// 本身，更不是加密**。不要在界面上写出任何「凭据不出执行机」之类的承诺。
//
// 形态基准：prototypes/discipline-config/pages/settings.html（三栏骨架照搬 B157）。
//
// 边界：
//   - **不轮询**：进分区/切机器/保存后各拉一次即可，照抄开发机的 15s 探活会
//     把用户正在编辑的正文覆盖掉
//   - 不做删除与改名（改名会让映射静默指空）
//   - 映射不在这里改：那是开发机详情的事
//   - 断开的机器不发请求、不画编辑器，直接展示 error 原文
import { useCallback, useEffect, useMemo, useState } from 'react'
import type { EnvKey, EnvResp } from '../../api/types'
import {
  ApiError,
  fetchEnv,
  fetchEnvFile,
  fetchEnvKeys,
  saveEnvFile,
} from '../../api/client'
import { useMachines } from '../data/useMachines'
import { errorMessage } from '../lib/format'
import { LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { BlockEditor } from './BlockEditor'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

function machineLabel(name: string): string {
  return name === '' ? '本机' : name
}

// KeyList 渲染变量清单。**只有 key 名与值长度**——本组件连接收值的 prop 都没有，
// 这是 spec §7 凭据边界在前端的结构性保证。
function KeyList({ keys }: { keys: EnvKey[] }) {
  if (keys.length === 0) {
    return <p className="mt-3 text-xs text-muted-foreground">这个文件里没有变量（可能只有注释或空行）。</p>
  }
  return (
    <ul className="mt-3 divide-y rounded-md border">
      {keys.map((key) => (
        <li key={key.key} className="flex items-center gap-3 px-3 py-1.5 text-xs">
          <span className="font-mono font-medium">{key.key}</span>
          <span className="text-muted-foreground">{key.value_bytes} 字节</span>
          {key.duplicate && <span className="text-amber-700">重复定义（后者覆盖）</span>}
          <span className="ml-auto font-mono text-muted-foreground">••••••</span>
        </li>
      ))}
    </ul>
  )
}

// EnvPage 提供按机器查看 env 文件变量清单与显式编辑正文的设置分区。
export function EnvPage() {
  const machinesState = useMachines(true)
  const machines = useMemo(() => machinesState.data?.machines ?? [], [machinesState.data])
  const [machine, setMachine] = useState('')
  const [response, setResponse] = useState<EnvResp | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [keys, setKeys] = useState<EnvKey[] | null>(null)
  const [draft, setDraft] = useState('')
  const [baseSha, setBaseSha] = useState('')
  const [editMode, setEditMode] = useState(false)
  const [loadingFile, setLoadingFile] = useState(false)
  const [loadingKeys, setLoadingKeys] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const [notice, setNotice] = useState('')
  const [newOpen, setNewOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newContent, setNewContent] = useState('')

  const activeMachine = machines.find((item) => item.name === machine) ?? machines[0] ?? null
  const activeReachable = activeMachine?.reachable ?? false
  const hasActiveMachine = activeMachine !== null
  const machineSelected = machines.some((item) => item.name === machine)
  const selectedFileInfo = selected === null
    ? null
    : response?.files.find((file) => file.name === selected) ?? null

  // 机器列表是外部探活数据，但只用它决定当前机器是否还存在，不触碰正在编辑的 draft。
  useEffect(() => {
    if (machines.length > 0 && !machines.some((item) => item.name === machine)) {
      setMachine(machines[0].name)
    }
  }, [machines, machine])

  // 配置数据只在切机器或在线状态变化时拉取；机器列表的 15s 刷新不会覆盖编辑器。
  useEffect(() => {
    setResponse(null)
    setSelected(null)
    setKeys(null)
    setEditMode(false)
    setError('')
    setConflict(false)
    setNotice('')
    if (!hasActiveMachine || !activeReachable || !machineSelected) return
    let cancelled = false
    void fetchEnv(machine)
      .then((next) => {
        if (cancelled) return
        setResponse(next)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [machine, activeReachable, hasActiveMachine, machineSelected])

  const loadKeys = useCallback(async (name: string) => {
    setLoadingKeys(true)
    try {
      const next = await fetchEnvKeys(machine, name)
      setKeys(next.keys)
    } catch (err) {
      setError(errorMessage(err))
      setKeys(null)
    } finally {
      setLoadingKeys(false)
    }
  }, [machine])

  // 选中文件时只拉无值的变量清单；正文必须由用户显式点击「编辑正文」触发。
  useEffect(() => {
    if (selected === null || response === null || editMode || !activeReachable) return
    setError('')
    void loadKeys(selected)
  }, [selected, response, editMode, activeReachable, loadKeys])

  const selectFile = (name: string) => {
    setSelected(name)
    setEditMode(false)
    setKeys(null)
    setError('')
    setConflict(false)
    setNotice('')
  }

  const editFile = async () => {
    if (selected === null) return
    setLoadingFile(true)
    setError('')
    setConflict(false)
    setNotice('')
    try {
      const file = await fetchEnvFile(machine, selected)
      setDraft(file.content)
      setBaseSha(file.sha256 ?? '')
      setEditMode(true)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoadingFile(false)
    }
  }

  const reloadFile = async () => {
    if (selected === null) return
    setLoadingFile(true)
    setError('')
    setConflict(false)
    try {
      const file = await fetchEnvFile(machine, selected)
      setDraft(file.content)
      setBaseSha(file.sha256 ?? '')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoadingFile(false)
    }
  }

  const refresh = async () => {
    const next = await fetchEnv(machine)
    setResponse(next)
    if (selected !== null && next.files.some((file) => file.name === selected)) {
      await loadKeys(selected)
    }
  }

  const save = async () => {
    if (selected === null) return
    setBusy(true)
    setError('')
    setConflict(false)
    setNotice('')
    try {
      await saveEnvFile(machine, selected, { content: draft, base_sha256: baseSha })
      setEditMode(false)
      await refresh()
      setNotice('已保存；下一个任务即生效')
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setConflict(true)
        setError('盘上的内容和你打开时不一样了——重新加载会丢弃当前编辑')
      } else {
        setError(errorMessage(err))
      }
    } finally {
      setBusy(false)
    }
  }

  const openNew = () => {
    setNewName('')
    setNewContent('')
    setNewOpen(true)
    setError('')
    setConflict(false)
  }

  const createFile = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const name = newName.trim()
    if (name === '') {
      setError('文件名不能为空')
      return
    }
    setBusy(true)
    setError('')
    try {
      await saveEnvFile(machine, name, { content: newContent, base_sha256: '' })
      const next = await fetchEnv(machine)
      setResponse(next)
      setNewOpen(false)
      setSelected(name)
      setKeys(null)
      setNotice(`已新建 ${name}`)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const machineButtons = useMemo(() => machines.map((item) => (
    <button
      key={item.name}
      type="button"
      onClick={() => setMachine(item.name)}
      aria-pressed={item.name === machine}
      className={cn(
        'rounded-md border px-2.5 py-1 text-xs hover:bg-accent',
        item.name === machine && 'border-primary bg-primary/10 font-medium',
        !item.reachable && 'text-muted-foreground',
      )}
    >
      {machineLabel(item.name)}{!item.reachable && '（已断开）'}
    </button>
  )), [machines, machine])

  if (machinesState.data === null) {
    return <div className="p-6"><LoadFailed message={machinesState.errorText || '正在连接 agentd…'} onRetry={() => window.location.reload()} /></div>
  }

  return (
    <div className="flex min-h-full flex-col gap-3 p-4">
      {machinesState.sessionExpired && <SessionExpiredBanner />}
      <div className="flex flex-wrap items-center gap-2 border-b pb-3">
        <h2 className="mr-2 text-sm font-semibold">Env 文件</h2>
        <span className="text-xs text-muted-foreground">选择机器：</span>
        {machineButtons}
      </div>

      {activeMachine && !activeMachine.reachable ? (
        <div role="alert" className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm">
          <p className="font-medium text-amber-700">机器已断开</p>
          <p className="mt-1 break-words text-foreground/80">{activeMachine.error}</p>
        </div>
      ) : error && response === null ? (
        <div role="alert" className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">{error}</div>
      ) : response === null ? (
        <p className="p-4 text-sm text-muted-foreground">正在加载 env 配置…</p>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col gap-4 lg:flex-row">
          <aside className="flex w-full shrink-0 flex-col gap-3 lg:w-72">
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">{machineLabel(machine)} 上的文件</p>
              <p className="mb-1 break-all font-mono text-[11px] text-muted-foreground">{response.dir}</p>
              <div className="flex flex-col gap-1">
                {response.files.length === 0 ? (
                  <p className="px-1 text-xs text-muted-foreground">暂无用户文件</p>
                ) : response.files.map((file) => (
                  <button
                    key={file.name}
                    type="button"
                    onClick={() => selectFile(file.name)}
                    aria-pressed={selected === file.name}
                    className={cn(
                      'rounded-md border px-3 py-2 text-left text-sm hover:bg-accent',
                      selected === file.name && 'border-primary bg-primary/10',
                    )}
                  >
                    {file.name}
                    <span className="mt-0.5 block text-[11px] text-muted-foreground">{file.size} 字节</span>
                  </button>
                ))}
              </div>
            </div>
            <Button variant="outline" size="sm" className="w-full" onClick={openNew}>＋ 新建文件</Button>
          </aside>

          <section className="flex min-w-0 flex-1 flex-col rounded-lg border bg-background p-4">
            {selected === null ? (
              <p className="text-sm text-muted-foreground">请选择一个 env 文件。</p>
            ) : editMode ? (
              <BlockEditor
                title={selected}
                ariaLabel="env 文件正文"
                content={draft}
                readOnly={false}
                loading={loadingFile}
                onChange={setDraft}
                onSave={() => void save()}
                saving={busy}
                error={error}
                conflict={conflict}
                notice={notice}
                onReload={() => void reloadFile()}
                size={selectedFileInfo?.size}
              />
            ) : (
              <>
                <div className="flex items-center justify-between gap-2">
                  <div>
                    <h3 className="text-sm font-semibold">{selected}</h3>
                    <p className="text-[11px] text-muted-foreground">变量清单（值掩码）</p>
                  </div>
                  <Button size="sm" onClick={() => void editFile()} disabled={loadingKeys}>编辑正文</Button>
                </div>
                {error && <div role="alert" className="mt-2 text-xs text-destructive">{error}</div>}
                {loadingKeys ? (
                  <p className="mt-3 text-xs text-muted-foreground">正在加载变量清单…</p>
                ) : keys !== null ? (
                  <KeyList keys={keys} />
                ) : (
                  <p className="mt-3 text-xs text-muted-foreground">正在加载变量清单…</p>
                )}
                {notice && <p className="mt-2 text-xs text-emerald-700">{notice}</p>}
              </>
            )}
          </section>
        </div>
      )}

      {newOpen && (
        <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/30 p-4">
          <form role="dialog" aria-modal="true" onSubmit={(event) => void createFile(event)} className="flex w-full max-w-xl flex-col gap-3 rounded-lg border bg-background p-4 shadow-lg">
            <h3 className="text-sm font-semibold">新建 Env 文件</h3>
            <label className="text-xs font-medium" htmlFor="env-new-name">文件名（纯文件名）</label>
            <input id="env-new-name" value={newName} onChange={(event) => setNewName(event.target.value)} className="h-8 rounded-md border px-2 text-xs" />
            <label className="text-xs font-medium" htmlFor="env-new-content">起始内容</label>
            <textarea id="env-new-content" value={newContent} onChange={(event) => setNewContent(event.target.value)} className="min-h-48 rounded-md border p-2 font-mono text-xs" />
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" size="sm" onClick={() => setNewOpen(false)}>取消</Button>
              <Button type="submit" size="sm" disabled={busy}>新建</Button>
            </div>
          </form>
        </div>
      )}
    </div>
  )
}
