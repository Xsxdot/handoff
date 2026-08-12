// usePtyRestore —— 加载时按服务端会话列表恢复终端 tab（spec §6.1）。
//
// 职责：拉一次 GET /api/pty/sessions?scope=all，把每个**活着的**会话恢复成
// 对应基准目录下的一个终端 tab。
//
// 边界：
//   - 不做前端持久化。服务端列表是唯一真相，所以「目录被删了但 tab 还在」
//     这类失效态天然不存在——会话不在列表里就是没有
//   - 只恢复终端 tab。文件 tab 与 TUI tab 仍然刷新即丢（W4 spec §10 原状）
//   - 不切换用户的选中目录：恢复是后台动作，见 restoreTerminal 的注释
//
// 为什么只拉一次而不轮询：会话列表变化的唯一来源是用户自己的操作（开/关），
// 那些路径各自会更新 tab。定时轮询等于每 N 秒向每台远程机打一次探活，
// 换来的只是「别的设备上开的会话会自己冒出来」——那不是本期要的能力。
import { useEffect, useRef, useState } from 'react'
import { fetchPtySessions } from '../../api/client'
import type { PtySession } from '../../api/types'
import { HOME_BASE, type BaseDir } from './useWorkbench'
import { errorMessage } from '../lib/format'

// baseOfSession 把一个会话反解成它所属的基准目录。
//
// 工作树的 key 必须与 ProjectTree.workspaceBase 完全一致（绝对路径）——
// 两边对不上就会出现「左栏点进这个目录，恢复出来的终端却在另一个组里」。
//
// label 退回目录名：会话不带分支信息，而树上的 label 优先用分支名。这只影响
// 标题文字，**不影响归组**（key 相同），用户点一下左栏就会换成带分支名的那个。
export function baseOfSession(s: PtySession): BaseDir {
  if (s.base_kind === 'home') {
    // 远端 home 与本机 home 必须分开：路径都叫「~」，但它们是两台机器上的两个目录
    if (s.machine !== '') {
      return { key: `~@${s.machine}`, kind: 'home', path: '~', label: `home@${s.machine}`, projectName: '', machine: s.machine }
    }
    return HOME_BASE
  }
  const name = s.base_path.split('/').filter(Boolean).pop() ?? s.base_path
  return { key: s.base_path, kind: 'workspace', path: s.base_path, label: name, projectName: '', machine: s.machine }
}

// usePtyRestore 在挂载时恢复一次终端会话。
//
// 参数：restore 为「把这个会话放进那个目录的 tab 组」的写入口
//（通常是 WorkbenchApi.restoreTerminal）
//
// 返回：error 为拉取失败的原文（空串 = 没出错）。**不吞**：拉不到列表意味着
// 用户会看到一个「终端都不见了」的界面，必须说清是为什么。
export function usePtyRestore(restore: (b: BaseDir, sessionId: string) => void): { error: string } {
  const [error, setError] = useState('')
  // ranRef 让它严格只跑一次：React 18 的 StrictMode 会把 effect 跑两遍，
  // 空依赖数组挡不住，而这里跑两遍就是两次跨机探活
  const ranRef = useRef(false)
  // restoreRef 让 effect 不必把 restore 列进依赖：调用方每次渲染都会传一个新
  // 函数引用，列进去就等于每次渲染都重新恢复一遍
  const restoreRef = useRef(restore)
  restoreRef.current = restore

  useEffect(() => {
    if (ranRef.current) return
    ranRef.current = true
    let cancelled = false
    fetchPtySessions('all')
      .then((resp) => {
        if (cancelled) return
        for (const s of resp.sessions) {
          // exit_code 出现 = 已退出。恢复一个死会话只会让人以为它还能用
          if (s.exit_code !== undefined && s.exit_code !== null) continue
          restoreRef.current(baseOfSession(s), s.id)
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [])

  return { error }
}
