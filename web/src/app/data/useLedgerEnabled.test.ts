// useLedgerEnabled 的账本总开关探测测试：成功、关闭、失败与 loading 四种契约。
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useLedgerEnabled } from './useLedgerEnabled'
import * as ledgerApi from '../../api/ledger'

describe('useLedgerEnabled', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('enabled:true 时返回启用', async () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockResolvedValue({ enabled: true, mirror: [] })
    const { result } = renderHook(() => useLedgerEnabled())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.enabled).toBe(true)
  })

  it('enabled:false 时返回未启用', async () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockResolvedValue({ enabled: false })
    const { result } = renderHook(() => useLedgerEnabled())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.enabled).toBe(false)
  })

  // 请求失败按未启用处理：宁可少显示一个入口，也不要亮一个点进去就 503 的入口
  it('请求失败时按未启用处理', async () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockRejectedValue(new Error('connection refused'))
    const { result } = renderHook(() => useLedgerEnabled())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.enabled).toBe(false)
  })

  // loading 期间必须按未启用渲染，否则会闪一下账本入口再消失
  it('初始为 loading 且 enabled 为 false', () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockReturnValue(new Promise(() => {}))
    const { result } = renderHook(() => useLedgerEnabled())
    expect(result.current.loading).toBe(true)
    expect(result.current.enabled).toBe(false)
  })
})
