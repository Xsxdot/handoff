import { beforeEach, describe, expect, it, vi } from 'vitest'
import { clearDraft, draftKey, loadDraft, saveDraft } from './fileDraft'

beforeEach(() => localStorage.clear())

describe('fileDraft', () => {
  it('键包含机器、工作树路径与相对路径——三者任一不同就是另一份草稿', () => {
    expect(draftKey('devbox', '/w/b2-b3', 'go.mod'))
      .not.toBe(draftKey('local', '/w/b2-b3', 'go.mod'))
    expect(draftKey('devbox', '/w/b2-b3', 'go.mod'))
      .not.toBe(draftKey('devbox', '/w/other', 'go.mod'))
  })

  it('存了能取回来，clear 之后取不到', () => {
    const k = draftKey('devbox', '/w/b2-b3', 'go.mod')
    saveDraft(k, '改过的内容', 'basehash')
    expect(loadDraft(k)).toMatchObject({ draft: '改过的内容', baseSha: 'basehash' })
    clearDraft(k)
    expect(loadDraft(k)).toBeNull()
  })

  it('配额满时静默降级，不抛错——这时用户正在打字，一个存储配额的报错帮不上任何忙', () => {
    const k = draftKey('devbox', '/w/b2-b3', 'go.mod')
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('QuotaExceededError')
    })
    expect(() => saveDraft(k, 'x', 'h')).not.toThrow()
    spy.mockRestore()
  })

  it('配额满时按 savedAt 淘汰最旧的草稿再重试', () => {
    const k1 = draftKey('devbox', '/w/b2-b3', 'a.txt')
    const k2 = draftKey('devbox', '/w/b2-b3', 'b.txt')
    const k3 = draftKey('devbox', '/w/b2-b3', 'c.txt')
    localStorage.setItem(k1, JSON.stringify({ draft: 'a', baseSha: 'h', savedAt: 100 }))
    localStorage.setItem(k2, JSON.stringify({ draft: 'b', baseSha: 'h', savedAt: 200 }))
    localStorage.setItem(k3, JSON.stringify({ draft: 'c', baseSha: 'h', savedAt: 300 }))
    const origSetItem = Storage.prototype.setItem
    let quotaLeft = 1
    const setSpy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (
      this: Storage, key: string, value: string,
    ) {
      if (quotaLeft > 0) {
        quotaLeft -= 1
        throw new DOMException('QuotaExceededError')
      }
      origSetItem.call(this, key, value)
    })
    const removeSpy = vi.spyOn(Storage.prototype, 'removeItem')
    try {
      const newKey = draftKey('devbox', '/w/b2-b3', 'new.txt')
      saveDraft(newKey, 'x', 'h')

      expect(removeSpy).toHaveBeenCalledWith(k1)
      expect(loadDraft(newKey)).toMatchObject({ draft: 'x', baseSha: 'h' })
    } finally {
      setSpy.mockRestore()
      removeSpy.mockRestore()
    }
  })

  it('坏数据当作没有草稿，不让界面崩', () => {
    const k = draftKey('devbox', '/w/b2-b3', 'go.mod')
    localStorage.setItem(k, '{不是合法 JSON')
    expect(loadDraft(k)).toBeNull()
  })
})
