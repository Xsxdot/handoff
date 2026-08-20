// useWorkbenchSync —— 工作台状态的水合与写回（2026-08-20 状态同步 spec §5.3）。
//
// 职责：
//   - 启动时拉一次「落盘状态 + PTY 会话列表」，合成后一次性灌入两个 hook
//   - 之后监听状态变化，把变了的行去抖 500ms 各自写回
//
// 边界：
//   - **不认识布局形状**：解码、校验、合成全在 persist.ts / dockPersist.ts / restore.ts
//   - **不管选中目录**：restoredSelected 只是把服务端存的那个 key 交出去，
//     校验它还在不在树上、要不要 select，都是 Shell 的事（要等项目树，spec §6）
//   - **只在启动时拉一次**，不做前台唤醒重拉：那一刻本端内存里的那份才是用户
//     刚才的现场，从服务端拉一份回来盖掉它是纯粹的坏（spec §1.6）
//
// 它取代了旧的会话恢复入口：布局恢复与会话恢复本来就是同一件事的两半，
// 留两个入口必然会有人只改一边。
import { useEffect, useRef, useState, type MutableRefObject } from 'react'
import {
  fetchPtySessions,
  fetchWorkbenchState,
  putWorkbenchBase,
  putWorkbenchDock,
  putWorkbenchSelected,
} from '../../api/client'
import { encodeDock, type DockSnapshot } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import { topInset } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { diffPayloads, encodeBase, isEmptyWorkbench } from './persist'
import { buildRestore } from './restore'
import type { Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'

// WRITE_DEBOUNCE_MS 是写回的去抖窗口。
//
// 500ms 照着 FileTab 草稿层的既有取舍：拖分屏分隔条会连发几十次 resize，
// 每次都 PUT 是自找的。**不挂 beforeunload 做 flush**——窗口内丢掉的最坏
// 情况是一次栏宽微调没存上，为它挂一个卸载钩子不划算。
const WRITE_DEBOUNCE_MS = 500

// WorkbenchSyncDeps 是本 hook 需要读到的状态与要调用的写入口。
//
// 全部由调用方（Shell）提供，本 hook 不自己持有任何工作台状态——
// 它是两个既有 hook 之间的一条管道，不是第三个真相。
export interface WorkbenchSyncDeps {
  byBase: Record<string, Workbench>
  baseDirs: Record<string, BaseDir>
  selectedKey: string
  dockSnapshot: DockSnapshot
  hydrateWorkbench: (entries: Array<{ base: BaseDir; wb: Workbench }>) => void
  hydrateDock: (s: DockSnapshot) => void
  adoptDockTab: (t: HomeTab) => void
}

// useWorkbenchSync 在挂载时恢复一次，并在此后持续写回。
//
// 参数：deps 是调用方提供的工作台/悬浮窗状态与水合入口。
// 返回：
//   - error: 拉取失败的原文（空串 = 没出错）。**不吞**：拉不到意味着用户会看到
//     一个「什么都没了」的界面，必须说清是为什么
//   - restoredSelected: 服务端存的「上次选中目录」的 key（空串 = 没有）。
//     调用方要在项目树到位后校验它还在不在，再决定 select
// 注意：拉取失败后 ready 闸门永久关闭，本次挂载生命周期不再写回。
export function useWorkbenchSync(deps: WorkbenchSyncDeps): { error: string; restoredSelected: string } {
  const [error, setError] = useState('')
  const [restoredSelected, setRestoredSelected] = useState('')

  // ranRef 让恢复严格只跑一次：React 18 的 StrictMode 会把 effect 跑两遍，
  // 空依赖数组挡不住，而这里跑两遍就是两次跨机探活。
  // cancelledRef 与它配对：ranRef 管「只跑一次」，cancelledRef 管「结果还要不要」，
  // 两者都必须跨 effect run。用局部变量是错的——上一轮 cleanup 会取消掉这一轮
  // 仍有效的请求，StrictMode 下开发端 100% 恢复不出任何 tab。
  //（这两条纪律原样承接自它取代的旧会话恢复入口。）
  const ranRef = useRef(false)
  const cancelledRef = useRef(false)

  // readyRef 是写回的总闸。**只有恢复成功才打开**：拉取失败时我们不知道服务端
  // 有什么，此时把本端那份空布局写回去 = 一次启动失败清空用户所有现场。
  // 宁可这一整个会话都不落盘，并把错误摆在界面上。
  const readyRef = useRef(false)

  // depsRef 让去抖回调读到**触发时**的最新状态，而不是排期那一刻闭包里的旧值
  const depsRef = useRef(deps)
  depsRef.current = deps

  // sentRef / dockSentRef / selectedSentRef 是「已经落盘的那一份」的快照，
  // 差分的基准。种子在恢复成功时播下，用的是**服务端返回的原文**而不是重新
  // 编码的结果——这样只有内容真的变了的行才会重写，而解码失败被丢弃的坏行会
  // 因为不在 next 里被判为 removed，顺带清理掉。
  const sentRef = useRef<Record<string, string>>({})
  const dockSentRef = useRef('')
  const selectedSentRef = useRef('')

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // ① 恢复：两个请求都到齐才灌入一次
  useEffect(() => {
    cancelledRef.current = false
    if (!ranRef.current) {
      ranRef.current = true
      Promise.all([fetchWorkbenchState(), fetchPtySessions('all')])
        .then(([state, sessResp]) => {
          if (cancelledRef.current) return
          const vw = window.innerWidth || document.documentElement.clientWidth
          const vh = window.innerHeight || document.documentElement.clientHeight
          const r = buildRestore({
            state,
            sessions: sessResp.sessions,
            vw: vw > 0 ? vw : 1280,
            vh: vh > 0 ? vh : 800,
            inset: topInset(),
          })

          // 承重次序：先播种快照，再灌入。颠倒的话写回 effect 会看到
          // 「本地有一堆、快照是空的」，把刚恢复的整份重推一遍——N 次无谓的 PUT，
          // 还会把全部行的 updated_at 刷成同一时刻，50 行淘汰的先后全乱
          sentRef.current = Object.fromEntries(state.bases.map((b) => [b.base_key, b.payload]))
          dockSentRef.current = state.dock
          selectedSentRef.current = state.selected

          const d = depsRef.current
          d.hydrateWorkbench(r.entries)
          if (r.dock !== null) d.hydrateDock(r.dock)
          for (const t of r.dockOrphans) d.adoptDockTab(t)
          setRestoredSelected(r.selected)
          readyRef.current = true

          if (r.dropped.length > 0) {
            console.warn('丢弃了无法解析的工作台状态行，这些目录的布局不会恢复', r.dropped)
          }
          console.debug('工作台状态恢复完成', {
            目录数: r.entries.length,
            抹掉的死会话: r.pruned,
            补进来的孤儿会话: r.adopted,
            丢弃的坏行: r.dropped.length,
            悬浮窗: r.dock !== null ? '已恢复' : '无落盘现场',
          })
        })
        .catch((err: unknown) => {
          if (cancelledRef.current) return
          // 不吞：用户会看到「什么都没了」，必须说清为什么。
          // readyRef 保持 false —— 这一整个会话都不写回
          console.warn('恢复工作台状态失败，本次不恢复任何 tab，且本会话不会写回', err)
          setError(errorMessage(err))
        })
    }
    return () => {
      cancelledRef.current = true
    }
  }, [])

  // ② 写回：状态一变就重排去抖
  useEffect(() => {
    if (!readyRef.current) return
    if (timerRef.current !== null) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      flush(depsRef.current, sentRef, dockSentRef, selectedSentRef)
    }, WRITE_DEBOUNCE_MS)
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
  }, [deps.byBase, deps.baseDirs, deps.selectedKey, deps.dockSnapshot])

  return { error, restoredSelected }
}

// flush 把当前状态与「已落盘快照」做差分，逐项写回。
//
// 参数：d 是触发时的最新状态；三个 ref 是快照，写成功后就地更新。
// 返回：无。**不抛**——任一项失败只 warn，下一次状态变动会自然重试。
// 注意：只有成功完成单项请求后才更新对应快照。
//
// 为什么成功之后才更新快照（而不是乐观更新）：失败的那一项留在旧快照里，
// 下次差分仍会判为「变了」，于是自动重试。乐观更新等于一次网络抖动
// 就永久丢掉那一行。
function flush(
  d: WorkbenchSyncDeps,
  sentRef: MutableRefObject<Record<string, string>>,
  dockSentRef: MutableRefObject<string>,
  selectedSentRef: MutableRefObject<string>,
) {
  const next: Record<string, string> = {}
  for (const [key, wb] of Object.entries(d.byBase)) {
    // 空工作台编码为「删除」：用户把一个目录的 tab 全关掉就是不想再看见它，
    // 存一行空记录只会白占 50 行配额里的一格
    if (isEmptyWorkbench(wb)) continue
    const base = d.baseDirs[key]
    if (base === undefined) {
      // 有 tab 组却没有元数据，编码不出来。正常不会发生（useWorkbench 的四个
      // 写入口都会登记），出现了就是那边漏了一处，必须能被搜到
      console.warn('工作台状态写回：缺少基准元数据，跳过该行', key)
      continue
    }
    next[key] = encodeBase(base, wb)
  }

  const { changed, removed } = diffPayloads(sentRef.current, next)
  for (const key of changed) {
    const payload = next[key]
    putWorkbenchBase(key, payload)
      .then(() => {
        sentRef.current[key] = payload
      })
      .catch((err: unknown) => console.warn('工作台状态写回失败，下次变动会重试', key, err))
  }
  for (const key of removed) {
    putWorkbenchBase(key, null)
      .then(() => {
        delete sentRef.current[key]
      })
      .catch((err: unknown) => console.warn('工作台状态删除失败，下次变动会重试', key, err))
  }

  const dockRaw = encodeDock(d.dockSnapshot)
  if (dockRaw !== dockSentRef.current) {
    putWorkbenchDock(dockRaw)
    .then(() => {
        dockSentRef.current = dockRaw
      })
      .catch((err: unknown) => console.warn('悬浮窗现场写回失败，下次变动会重试', 'dock', err))
  }

  if (d.selectedKey !== selectedSentRef.current) {
    const key = d.selectedKey
    putWorkbenchSelected(key)
      .then(() => {
        selectedSentRef.current = key
      })
      .catch((err: unknown) => console.warn('选中目录写回失败，下次变动会重试', 'selected', key, err))
  }

  if (changed.length > 0 || removed.length > 0) {
    console.debug('工作台状态写回', { 写入: changed.length, 删除: removed.length })
  }
}
