// DisciplinePage —— 设置页「执行纪律」分区（B157 spec §2.1）。
//
// 职责：按机器编辑 <DataDir>/discipline/ 下的纪律块正文；内置两版只读展示，
// 可「以此为模板新建」。
//
// 形态基准：prototypes/discipline-config/pages/settings.html。
//
// 边界：
//   - **不轮询**：配置不是实时事实，进分区/切机器/保存后各拉一次即可。
//     照抄开发机分区的 15s 探活会把用户正在编辑的正文覆盖掉
//   - 不做删除与改名（改名会让映射静默指空，见 spec §1.1）
//   - 映射不在这里改：那是开发机详情的事，本页只标注「谁在用」
//   - 断开的机器不发请求、不画编辑器，直接展示 error 原文（诚实展示纪律）
import { useEffect, useMemo, useState } from 'react'
import type { DisciplineResp, DisciplineBinding } from '../../api/types'
import {
  ApiError,
  fetchDiscipline,
  fetchDisciplineFile,
  saveDisciplineFile,
} from '../../api/client'
import { useMachines } from '../data/useMachines'
import { errorMessage } from '../lib/format'
import { LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { BlockEditor } from './BlockEditor'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

type Selection =
  | { machine: string; kind: 'builtin'; tier: string }
  | { machine: string; kind: 'file'; name: string }

function machineLabel(name: string): string {
  return name === '' ? '本机' : name
}

function usersForBinding(bindings: DisciplineBinding[], kind: Selection['kind'], value: string): string[] {
  return bindings
    .filter((b) => kind === 'file'
      ? b.mode === 'file' && b.file === value
      : b.mode === 'default' && b.default_tier === value)
    .map((b) => b.executor)
}

function bindingHint(users: string[]): string {
  return users.length === 0 ? '未被引用' : `${users.join('、')} 在用`
}

// DisciplinePage 提供按机器编辑纪律块正文与从内置版本新建文件的设置分区。
export function DisciplinePage() {
  const machinesState = useMachines(true)
  const machineList = machinesState.data?.machines
  const machines = useMemo(() => machineList ?? [], [machineList])
  const [machine, setMachine] = useState('')
  const [response, setResponse] = useState<DisciplineResp | null>(null)
  const [selected, setSelected] = useState<Selection | null>(null)
  const [draft, setDraft] = useState('')
  const [baseSha, setBaseSha] = useState('')
  const [loadingFile, setLoadingFile] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const [notice, setNotice] = useState('')
  const [newOpen, setNewOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newContent, setNewContent] = useState('')

  const activeMachine = machines.find((m) => m.name === machine) ?? machines[0] ?? null
  const activeReachable = activeMachine?.reachable ?? false
  const hasActiveMachine = activeMachine !== null
  const machineSelected = machines.some((m) => m.name === machine)

  // 机器列表是外部探活数据，但只用它决定当前机器是否还存在，不触碰正在编辑的 draft。
  useEffect(() => {
    if (machines.length > 0 && !machines.some((m) => m.name === machine)) {
      setMachine(machines[0].name)
    }
  }, [machines, machine])

  // 配置数据只在切机器或在线状态变化时拉取；机器列表的 15s 刷新不会覆盖编辑器。
  useEffect(() => {
    setResponse(null)
    setSelected(null)
    setError('')
    setConflict(false)
    setNotice('')
    // 机器列表没有本机时，先等选择状态切到第一台远程机，避免空 machine 误查本机。
    if (!hasActiveMachine || !activeReachable || !machineSelected) return
    let cancelled = false
    void fetchDiscipline(machine)
      .then((next) => {
        if (cancelled) return
        setResponse(next)
        const tier = next.builtins[0]?.tier ?? 'subagent'
        setSelected({ machine, kind: 'builtin', tier })
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [machine, activeReachable, hasActiveMachine, machineSelected])

  const selectedBuiltin = selected?.kind === 'builtin'
    ? response?.builtins.find((b) => b.tier === selected.tier) ?? null
    : null
  const selectedFileName = selected?.kind === 'file' ? selected.name : ''
  const selectedFile = selectedFileName === '' ? null : selectedFileName
  const selectedFileInfo = selectedFile === null
    ? null
    : response?.files.find((f) => f.name === selectedFile) ?? null

  const selectBuiltin = (tier: string) => {
    setSelected({ machine, kind: 'builtin', tier })
    setError('')
    setConflict(false)
    setNotice('')
  }

  const selectFile = (name: string) => {
    setSelected({ machine, kind: 'file', name })
    setError('')
    setConflict(false)
    setNotice('')
  }

  // 切换文件才重置 draft/baseSha；机器列表刷新不在依赖项中，因此不会覆盖输入。
  useEffect(() => {
    const selectedKind = selected?.kind
    const selectedMachine = selected?.machine
    if (selectedKind !== 'file' || selectedMachine !== machine || selectedFileName === '') return
    let cancelled = false
    setLoadingFile(true)
    setError('')
    setConflict(false)
    void fetchDisciplineFile(machine, selectedFileName)
      .then((file) => {
        if (cancelled) return
        setDraft(file.content)
        setBaseSha(file.sha256 ?? '')
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
      .finally(() => {
        if (!cancelled) setLoadingFile(false)
      })
    return () => {
      cancelled = true
    }
  }, [machine, selected?.kind, selected?.machine, selectedFileName])

  const reloadFile = async () => {
    if (selected?.kind !== 'file') return
    setLoadingFile(true)
    setError('')
    setConflict(false)
    try {
      const file = await fetchDisciplineFile(machine, selected.name)
      setDraft(file.content)
      setBaseSha(file.sha256 ?? '')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoadingFile(false)
    }
  }

  const refresh = async () => {
    const next = await fetchDiscipline(machine)
    setResponse(next)
  }

  const save = async () => {
    if (selected?.kind !== 'file') return
    setBusy(true)
    setError('')
    setConflict(false)
    setNotice('')
    try {
      const result = await saveDisciplineFile(machine, selected.name, {
        content: draft,
        base_sha256: baseSha,
      })
      setBaseSha(result.sha256)
      await refresh()
      setNotice('已保存；下一个任务即生效，正在跑的任务不受影响')
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

  const openNew = (template?: string) => {
    setNewName('')
    setNewContent(template ?? '')
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
    setConflict(false)
    try {
      await saveDisciplineFile(machine, name, { content: newContent, base_sha256: '' })
      await refresh()
      setNewOpen(false)
      setSelected({ machine, kind: 'file', name })
      setNotice(`已新建 ${name}；还没有任何 executor 用它——去开发机分区指过去`)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const machineButtons = useMemo(() => machines.map((m) => (
    <button
      key={m.name}
      type="button"
      onClick={() => setMachine(m.name)}
      aria-pressed={m.name === machine}
      className={cn(
        'rounded-md border px-2.5 py-1 text-xs hover:bg-accent',
        m.name === machine && 'border-primary bg-primary/10 font-medium',
        !m.reachable && 'text-muted-foreground',
      )}
    >
      {machineLabel(m.name)}{!m.reachable && '（已断开）'}
    </button>
  )), [machines, machine])

  if (machinesState.data === null) {
    return <div className="p-6"><LoadFailed message={machinesState.errorText || '正在连接 agentd…'} onRetry={() => window.location.reload()} /></div>
  }

  return (
    <div className="flex min-h-full flex-col gap-3 p-4">
      {machinesState.sessionExpired && <SessionExpiredBanner />}
      <div className="flex flex-wrap items-center gap-2 border-b pb-3">
        <h2 className="mr-2 text-sm font-semibold">执行纪律</h2>
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
        <p className="p-4 text-sm text-muted-foreground">正在加载纪律配置…</p>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col gap-4 lg:flex-row">
          <aside className="flex w-full shrink-0 flex-col gap-3 lg:w-72">
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">内置（只读，随二进制分发）</p>
              <div className="flex flex-col gap-1">
                {response.builtins.map((builtin) => {
                  const users = usersForBinding(response.bindings, 'builtin', builtin.tier)
                  return (
                    <div key={builtin.tier}>
                      <button
                        type="button"
                        onClick={() => selectBuiltin(builtin.tier)}
                        aria-pressed={selected?.kind === 'builtin' && selected.tier === builtin.tier}
                        className={cn(
                          'w-full rounded-md border px-3 py-2 text-left text-sm hover:bg-accent',
                          selected?.kind === 'builtin' && selected.tier === builtin.tier && 'border-primary bg-primary/10',
                        )}
                      >
                        {builtin.tier}
                        <span className="mt-0.5 block text-[11px] text-muted-foreground">{bindingHint(users)}</span>
                      </button>
                    </div>
                  )
                })}
              </div>
            </div>

            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">{machineLabel(machine)} 上的文件</p>
              <p className="mb-1 break-all font-mono text-[11px] text-muted-foreground">{response.dir}</p>
              <div className="flex flex-col gap-1">
                {response.files.length === 0 ? (
                  <p className="px-1 text-xs text-muted-foreground">暂无用户文件</p>
                ) : response.files.map((file) => {
                  const users = usersForBinding(response.bindings, 'file', file.name)
                  return (
                    <button
                      key={file.name}
                      type="button"
                      onClick={() => selectFile(file.name)}
                      aria-pressed={selected?.kind === 'file' && selected.name === file.name}
                      className={cn(
                        'rounded-md border px-3 py-2 text-left text-sm hover:bg-accent',
                        selected?.kind === 'file' && selected.name === file.name && 'border-primary bg-primary/10',
                      )}
                    >
                      {file.name}
                      <span className="mt-0.5 block text-[11px] text-muted-foreground">{bindingHint(users)}</span>
                    </button>
                  )
                })}
              </div>
            </div>
            <Button variant="outline" size="sm" className="w-full" onClick={() => openNew()}>
              ＋ 新建文件
            </Button>
          </aside>

          <section className="flex min-w-0 flex-1 flex-col rounded-lg border bg-background p-4">
            {selectedBuiltin && (
              <BlockEditor
                title={`内置 ${selectedBuiltin.tier}`}
                ariaLabel="纪律块正文"
                content={selectedBuiltin.content}
                readOnly
                templateLabel="以此为模板新建"
                onTemplate={() => openNew(selectedBuiltin.content)}
              />
            )}
            {selectedFile !== null && (
              <BlockEditor
                title={selectedFile}
                ariaLabel="纪律块正文"
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
            )}
          </section>
        </div>
      )}

      {newOpen && (
        <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/30 p-4">
          <form role="dialog" aria-modal="true" onSubmit={(event) => void createFile(event)} className="flex w-full max-w-xl flex-col gap-3 rounded-lg border bg-background p-4 shadow-lg">
            <h3 className="text-sm font-semibold">新建纪律块文件</h3>
            <label className="text-xs font-medium" htmlFor="discipline-new-name">文件名（纯文件名）</label>
            <input id="discipline-new-name" value={newName} onChange={(event) => setNewName(event.target.value)} className="h-8 rounded-md border px-2 text-xs" />
            <label className="text-xs font-medium" htmlFor="discipline-new-content">起始内容</label>
            <textarea id="discipline-new-content" value={newContent} onChange={(event) => setNewContent(event.target.value)} className="min-h-48 rounded-md border p-2 font-mono text-xs" />
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
