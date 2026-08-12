// usePtySupport —— 每台机器的 PTY 能力位（spec §5.5）。
//
// 职责：加载时拉一次 GET /api/machines，把 pty_supported 整理成
// 「机器名 → true / false / null」的查询函数。
//
// 边界：
//   - 只读能力位，不管会话
//   - **不轮询**。能力位是平台属性，一台机器不会跑着跑着就不支持 PTY 了；
//     useMachines 那条「没人看的时候别打扰远程机」的纪律在这里同样成立，
//     所以只在加载时打一次
//
// 三态是这个 hook 存在的全部理由：一个 boolean 会把「老 agentd 没上报」
// 压成「不支持」，于是终端入口在一台其实能用的机器上凭空消失。
import { useEffect, useRef, useState } from 'react'
import { fetchMachines } from '../../api/client'
import { errorMessage } from '../lib/format'

export interface PtySupport {
  // supported 返回 null 表示**不知道**：没拉到、机器不在列表里、或对端没上报。
  // 调用方对 null 的正确反应是「照常放行，出了错再说实话」，不是「禁用」。
  supported: (machine: string) => boolean | null
  error: string
}

export function usePtySupport(): PtySupport {
  const [map, setMap] = useState<Record<string, boolean> | null>(null)
  const [error, setError] = useState('')
  const ranRef = useRef(false)

  useEffect(() => {
    if (ranRef.current) return
    ranRef.current = true
    let cancelled = false
    fetchMachines()
      .then((resp) => {
        if (cancelled) return
        const next: Record<string, boolean> = {}
        for (const m of resp.machines) {
          // 只收明确上报的：缺席/null 不进表，查询时自然落到 null
          if (typeof m.pty_supported === 'boolean') next[m.name] = m.pty_supported
        }
        setMap(next)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [])

  return {
    supported: (machine: string) => (map && machine in map ? map[machine] : null),
    error,
  }
}
