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
  // ranRef 与 cancelledRef 配对：ranRef 管「只跑一次」，cancelledRef 管「结果
  // 还要不要」，两者都必须跨 effect run，缺一不可。用局部变量是错的——上一轮
  // cleanup 会取消掉这一轮仍有效的请求
  const ranRef = useRef(false)
  const cancelledRef = useRef(false)

  // 与 usePtyRestore 同因同修：cancelledRef 必须跨 effect run，否则 StrictMode
  // 下第一轮 cleanup 会取消掉唯一那次请求，能力表永远停在 null。
  // 停在 null 不会出错（三态门对 null 的反应是放行），但等于这个门没生效。
  useEffect(() => {
    cancelledRef.current = false
    if (!ranRef.current) {
      ranRef.current = true
      fetchMachines()
        .then((resp) => {
          if (cancelledRef.current) return
          const next: Record<string, boolean> = {}
          for (const m of resp.machines) {
            // 只收明确上报的：缺席/null 不进表，查询时自然落到 null
            if (typeof m.pty_supported === 'boolean') next[m.name] = m.pty_supported
          }
          setMap(next)
        })
        .catch((err: unknown) => {
          if (cancelledRef.current) return
          console.warn('拉取机器能力位失败，PTY 三态门降级为一律放行', err)
          setError(errorMessage(err))
        })
    }
    return () => {
      cancelledRef.current = true
    }
  }, [])

  return {
    supported: (machine: string) => (map && machine in map ? map[machine] : null),
    error,
  }
}
