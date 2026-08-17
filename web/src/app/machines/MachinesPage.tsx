// MachinesPage —— 开发机分区。左侧机器卡片列表 + 右侧选中机器详情，并提供
// 新增/删除开发机入口（写操作经 agentd 的写接口）。
//
// 数据源：useMachines(true)（15s 探活，spec §6——只在设置页的开发机分区可见时开表）。
// 组件挂在设置页的开发机分区里，分区切走即卸载，usePoll 的 effect 清理自动停表。
//
// 诚实展示（spec §8）：
//   - 不可达机器仍然渲染，标「已断开」并透出 error 原文——绝不静默少一台
//   - 顶部台数统计**含不可达那台**：少一台就是静默丢机器
//   - 三个未接线的操作（可用执行者开关 / 重启 agent / 打开终端）本期**只渲染不接线**：
//     它们需要 agentd 侧的写接口，不在本期。按「不置灰」纪律（spec §0），它们可点，
//     点了给出明确的「尚未实现」说明，而不是一个永远按不动的灰按钮。
//     配对开发机与 Env 文件仍然不渲染——那两项连形态都还没定。
//
// 写操作：
//   - 新增开发机：页面内联表单（不用弹窗——机器列表要保持可见，方便对照已有名字）。
//     令牌是密码型输入框，后端本就不回显；探测失败原样展示后端原文并提供
//     「仍然保存」（以 force 重发）。
//   - 删除开发机：机器卡片上的删除入口，经 ConfirmDialog 二次确认后调用删除接口。
import { useEffect, useState } from 'react'
import type { AddMachineReq, Machine, MachinesResp, ProjectTreeResp } from '../../api/types'
import { addMachine, deleteMachine, ApiError } from '../../api/client'
import { useMachines } from '../data/useMachines'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'
import { MachineDetail } from './MachineDetail'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(name: string): string {
  return name === '' ? '本机' : name
}

// dirCountOf 统计项目树里某台机器名下的全部 workspace 目录数。
// probe_error 非空的位置探不出目录，按 0 计（与 counts.ts 口径一致）。
function dirCountOf(tree: ProjectTreeResp | null, machine: string): number {
  if (!tree) return 0
  return tree.projects.reduce((n, p) => {
    const loc = p.locations.find((l) => l.machine === machine)
    return n + (loc && loc.probe_error === '' ? loc.workspaces.length : 0)
  }, 0)
}

// AddFormState 是新增表单的四个输入。字段名与 AddMachineReq 对齐，提交时直接展开。
interface AddFormState {
  name: string
  addr: string
  token: string
  user: string
}

const EMPTY_FORM: AddFormState = { name: '', addr: '', token: '', user: '' }

// INPUT_CLASS 与登记向导 / 项目编辑弹层的输入框样式保持一致，界面词汇统一。
const INPUT_CLASS = 'h-8 w-full rounded-md border border-input bg-background px-2.5 text-xs shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function MachinesPage({ tree }: { tree: ProjectTreeResp | null }) {
  const machinesState = useMachines(true)
  const [selected, setSelected] = useState<string | null>(null)
  const [lastProbe, setLastProbe] = useState<number | null>(null)

  // override：新增/删除成功后服务端返回的最新机器列表。立刻用它刷新界面
  // （不干等下一轮 15s 探活），等下一轮 poll 的 data 落地后自然被覆盖清空。
  const [override, setOverride] = useState<MachinesResp | null>(null)

  const [showAddForm, setShowAddForm] = useState(false)
  const [form, setForm] = useState<AddFormState>(EMPTY_FORM)
  const [submitting, setSubmitting] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [forceOffered, setForceOffered] = useState(false)

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  // 每次探活成功（data 有新值）就记下本页时刻，作为「最后心跳」的观测基准。
  // 首次尚无数据时保持 null 显示「—」——刚打开就显示「刚刚」而 agentd 已跑三天
  // 是一种好看的假象，必须标明这是本页打开以来的观测（见 MachineDetail 的注脚）。
  useEffect(() => {
    if (machinesState.data !== null) setLastProbe(Date.now())
  }, [machinesState.data])

  // 轮询带来新 data 时丢弃本地 override——服务端是唯一真相源。
  useEffect(() => {
    if (machinesState.data !== null) setOverride(null)
  }, [machinesState.data])

  const machines = override?.machines ?? machinesState.data?.machines ?? []
  // 默认选中第一台（本机优先）；列表刷新后若已选机器仍在则保持，否则回落第一台。
  const activeMachine = machines.find((m) => m.name === (selected ?? '')) ?? machines[0] ?? null

  const patchForm = (patch: Partial<AddFormState>) => setForm((prev) => ({ ...prev, ...patch }))

  const submitAdd = async (force: boolean) => {
    setSubmitting(true)
    setAddError(null)
    setForceOffered(false)
    try {
      const req: AddMachineReq = { ...form }
      if (force) req.force = true
      const resp = await addMachine(req)
      setOverride(resp)
      machinesState.refresh()
      setShowAddForm(false)
      setForm(EMPTY_FORM)
    } catch (err) {
      const msg = errorMessage(err)
      setAddError(msg)
      // 为什么只有 400 才给「仍然保存」：400 几乎都是可达性探测失败，机器临时
      // 离线是合理场景，不该因此完全加不进来；但默认仍探测，因为绝大多数失败
      // 是粘错地址或令牌。非 400（无权限、反代挂等）重发同样无解，不给出口。
      setForceOffered(err instanceof ApiError && err.status === 400)
    } finally {
      setSubmitting(false)
    }
  }

  const confirmDelete = async () => {
    if (deleteTarget === null) return
    setDeleteBusy(true)
    setDeleteError('')
    try {
      const resp = await deleteMachine(deleteTarget)
      setOverride(resp)
      machinesState.refresh()
      if (selected === deleteTarget) setSelected(null)
      setDeleteTarget(null)
    } catch (err) {
      setDeleteError(errorMessage(err))
    } finally {
      setDeleteBusy(false)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-3">
      {machinesState.sessionExpired && <SessionExpiredBanner />}
      {machinesState.disconnected && !machinesState.sessionExpired && (
        <DisconnectedBanner message={machinesState.errorText} />
      )}

      {machinesState.data === null ? (
        machinesState.sessionExpired ? null : (
          <LoadFailed message={machinesState.errorText || '正在连接 agentd…'} onRetry={() => window.location.reload()} />
        )
      ) : (
        <>
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold">开发机分区</h2>
            <Button size="sm" variant="outline" onClick={() => setShowAddForm((v) => !v)}>
              {showAddForm ? '收起表单' : '新增开发机'}
            </Button>
          </div>

          {showAddForm && (
            <AddMachineForm
              form={form}
              patchForm={patchForm}
              submitting={submitting}
              error={addError}
              forceOffered={forceOffered}
              onSubmit={() => void submitAdd(false)}
              onForce={() => void submitAdd(true)}
            />
          )}

          <dl className="flex items-center gap-8 rounded-lg border bg-background px-4 py-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">开发机</dt>
              <dd className="mt-0.5 text-lg font-semibold">{machines.length}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">在线</dt>
              <dd className="mt-0.5 text-lg font-semibold">{machines.filter((m) => m.reachable).length}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">运行任务</dt>
              <dd className="mt-0.5 text-lg font-semibold">{machines.reduce((n, m) => n + m.active_tasks, 0)}</dd>
            </div>
          </dl>

          <div className="flex items-start gap-4">
            <div className="flex w-80 shrink-0 flex-col gap-2">
              {machines.map((m) => (
                <MachineCard
                  key={m.name}
                  machine={m}
                  active={m.name === activeMachine?.name}
                  onSelect={() => setSelected(m.name)}
                  onDelete={() => {
                    setDeleteTarget(m.name)
                    setDeleteError('')
                  }}
                />
              ))}
            </div>
            <div className="min-w-0 flex-1">
              {activeMachine && (
                <MachineDetail
                  machine={activeMachine}
                  dirCount={dirCountOf(tree, activeMachine.name)}
                  lastProbe={lastProbe}
                />
              )}
            </div>
          </div>
        </>
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        title="删除开发机"
        description={
          deleteTarget !== null
            ? `将删除「${deleteTarget}」这台开发机的登记。删除后这台机器上的项目位置、任务与终端都不再可见，但不会改动该机器上的代码。`
            : ''
        }
        confirmLabel="删除"
        destructive
        busy={deleteBusy}
        error={deleteError || undefined}
        onConfirm={() => void confirmDelete()}
        onCancel={() => {
          setDeleteTarget(null)
          setDeleteError('')
        }}
      />
    </div>
  )
}

// AddMachineForm 是「新增开发机」的页内内联表单。做成分行网格而不是弹窗：
// 机器列表要保持可见，方便对照已有名字与地址（spec §0 同时只有一个弹层）。
function AddMachineForm({
  form,
  patchForm,
  submitting,
  error,
  forceOffered,
  onSubmit,
  onForce,
}: {
  form: AddFormState
  patchForm: (patch: Partial<AddFormState>) => void
  submitting: boolean
  error: string | null
  forceOffered: boolean
  onSubmit: () => void
  onForce: () => void
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (!submitting) onSubmit()
      }}
      className="rounded-lg border bg-background p-4"
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <label htmlFor="machine-name" className="flex flex-col gap-1 text-sm">
          <span className="font-medium">名字</span>
          <input
            id="machine-name"
            type="text"
            required
            value={form.name}
            onChange={(e) => patchForm({ name: e.target.value })}
            className={INPUT_CLASS}
          />
        </label>

        <label htmlFor="machine-addr" className="flex flex-col gap-1 text-sm">
          <span className="font-medium">地址</span>
          <input
            id="machine-addr"
            type="text"
            required
            value={form.addr}
            onChange={(e) => patchForm({ addr: e.target.value })}
            placeholder="100.73.238.21:7777"
            className={INPUT_CLASS}
          />
        </label>

        <div className="flex flex-col gap-1 text-sm">
          {/* 令牌用密码型输入框：令牌仅随本次请求发送，后端不回显、不落盘，
              也不写进任何日志。这个输入不落任何持久化，提交即随请求体发走。 */}
          <label htmlFor="machine-token" className="font-medium">令牌</label>
          <input
            id="machine-token"
            type="password"
            required
            value={form.token}
            onChange={(e) => patchForm({ token: e.target.value })}
            className={INPUT_CLASS}
          />
          <span className="text-[11px] text-muted-foreground">对方机器 handoff init 末尾会打出来</span>
        </div>

        <div className="flex flex-col gap-1 text-sm">
          <label htmlFor="machine-user" className="font-medium">ssh 用户</label>
          <input
            id="machine-user"
            type="text"
            value={form.user}
            onChange={(e) => patchForm({ user: e.target.value })}
            className={INPUT_CLASS}
          />
          <span className="text-[11px] text-muted-foreground">attach / pull 要用</span>
        </div>
      </div>

      <p className="mt-2 text-[11px] text-muted-foreground">
        令牌只进不出：后端不回显它，也不会把它写进任何日志。
      </p>

      {error && (
        <div
          role="alert"
          className="mt-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive"
        >
          <p className="whitespace-pre-wrap">{error}</p>
          {forceOffered && (
            <button
              type="button"
              onClick={onForce}
              disabled={submitting}
              className="mt-2 rounded-md border border-destructive/40 px-2 py-1 text-[11px] hover:bg-destructive/10 disabled:opacity-50"
            >
              仍然保存
            </button>
          )}
        </div>
      )}

      <div className="mt-3 flex justify-end gap-2">
        <Button type="submit" disabled={submitting}>
          {submitting ? '提交中…' : '添加'}
        </Button>
      </div>
    </form>
  )
}

// MachineCard 是左侧一台机器的卡片：名称（""=本机）、地址、连接状态、活跃任务数。
// 点击选中；不可达机器照样渲染并标「已断开」。远程机器（name !== ""）卡片底部
// 有删除入口——本机不可删，删除只作用于登记记录，不动机器上的代码。
function MachineCard({
  machine,
  active,
  onSelect,
  onDelete,
}: {
  machine: Machine
  active: boolean
  onSelect: () => void
  onDelete: () => void
}) {
  const remote = machine.name !== ''
  return (
    <div
      className={cn(
        'flex flex-col overflow-hidden rounded-lg border bg-background transition-colors',
        active ? 'border-primary bg-accent/40' : 'hover:bg-accent/40',
      )}
    >
      <button type="button" onClick={onSelect} aria-pressed={active} className="flex flex-col gap-1 p-3 text-left">
        <div className="flex items-center gap-2">
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{machineLabel(machine.name)}</span>
          <span className={cn('shrink-0 text-[11px]', machine.reachable ? 'text-emerald-600' : 'text-amber-600')}>
            {machine.reachable ? '已连接' : '已断开'}
          </span>
        </div>
        <div className="truncate font-mono text-xs text-muted-foreground">{machine.addr}</div>
        <div className="text-[11px] text-muted-foreground">{machine.active_tasks} 活跃任务</div>
      </button>
      {remote && (
        <button
          type="button"
          onClick={onDelete}
          className="border-t border-border px-3 py-1 text-left text-[11px] text-destructive hover:bg-destructive/10"
        >
          删除
        </button>
      )}
    </div>
  )
}
