// useFramesStream.test.ts —— 帧流边界元数据：size 头存在与缺失都必须可辨。
import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { pageBytes, useFramesStream } from './useFramesStream'

const frame = `${JSON.stringify({ seq: 1, type: 'text', turn: 1, delta: '正文' })}\n`

afterEach(() => vi.unstubAllGlobals())

describe('useFramesStream', () => {
  it('缺少 X-Handoff-Frames-Size 时标记边界未知', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(frame, {
      status: 200,
      headers: { 'Content-Type': 'application/x-ndjson' },
    })))

    const { result } = renderHook(() => useFramesStream('task-1'))
    await waitFor(() => expect(result.current.active).toBe(false))

    expect(result.current.sizeUnknown).toBe(true)
    expect(result.current.startOffset).toBe(0)
    expect(result.current.frames).toHaveLength(1)
  })

  it('有 X-Handoff-Frames-Size 时按文件大小计算起始偏移', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(frame, {
      status: 200,
      headers: {
        'Content-Type': 'application/x-ndjson',
        'X-Handoff-Frames-Size': String(pageBytes + 100),
      },
    })))

    const { result } = renderHook(() => useFramesStream('task-1'))
    await waitFor(() => expect(result.current.active).toBe(false))

    expect(result.current.sizeUnknown).toBe(false)
    expect(result.current.startOffset).toBe(100)
  })
})
