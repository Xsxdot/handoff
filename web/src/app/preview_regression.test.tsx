// B294 跨域回归：钉住 wire 零值、跨机器身份和页面展示 helper 的边界。
// 真 Chromium、真实 OS focus、跨机 DNS 与重启恢复不在 jsdom 通过范围内，归协调者真机清单。
import { describe, expect, it } from 'vitest'
import zeroFixture from '../api/testdata/PreviewZeroValues.json'
import type { PreviewSession } from '../api/types'
import { normalizePreviewOrigin, previewKey, previewLabel } from './data/usePreviews'

describe('preview 跨域回归', () => {
  it('zero TTL 保留，缺席 optional 字段不被页面投影编造', () => {
    const zero = zeroFixture as PreviewSession
    expect(zero.ttl_seconds).toBe(0)
    expect('via' in zero).toBe(false)
    expect('origin_url' in zero).toBe(false)
    expect(previewLabel(zero)).toBe('localhost:0')
  })

  it('相同 session id 在不同 machine 下不会撞 key，origin 归一化保留路径大小写', () => {
    const base: PreviewSession = { ...zeroFixture, id: 'same', origin_url: 'git@github.com:Org/Repo.git' }
    expect(previewKey(base, '')).not.toBe(previewKey(base, 'devbox'))
    expect(normalizePreviewOrigin(base.origin_url ?? '')).toBe('github.com/Org/Repo')
  })
})
