// 账本是否启用的一次性探测。
//
// 职责：查一次 /api/ledger/health，把 enabled 交给调用方做入口门控。
// 边界：只回答「开没开」，不回答镜像健康——那是 /cards 页自己的事。
// 不做轮询：开关是 agentd 启动期决定的，运行期不会变；改了配置要重启
// agentd，那时前端也会重连。
import { useEffect, useState } from 'react'
import { fetchLedgerHealth } from '../../api/ledger'

// LedgerEnabledState 是一次性账本探测对调用方暴露的最小状态。
export interface LedgerEnabledState {
  enabled: boolean
  loading: boolean
}

// useLedgerEnabled 返回账本启用状态。
//
// 契约：请求失败一律按未启用处理；loading 期间 enabled 恒 false，调用方
// 据此渲染即可（宁可入口晚一拍出现，也不要闪一下再消失）。
export function useLedgerEnabled(): LedgerEnabledState {
  const [enabled, setEnabled] = useState(false)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let stopped = false
    fetchLedgerHealth()
      .then((health) => {
        if (stopped) return
        setEnabled(Boolean(health.enabled))
      })
      .catch(() => {
        // 探不到就当没开：老版本 agentd 没有这个端点、网络断开都会走到这里，
        // 两种情况下亮出账本入口都会让用户点进一个坏页面
        if (!stopped) setEnabled(false)
      })
      .finally(() => {
        if (!stopped) setLoading(false)
      })
    return () => { stopped = true }
  }, [])

  return { enabled, loading }
}
