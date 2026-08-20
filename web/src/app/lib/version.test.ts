// 更新版本比较的契约测试。
//
// 职责：守住数字段与预发布版本的排序语义。
// 边界：比较规则与 internal/selfupdate.CompareVersion 对齐，不测试界面文案。
import { describe, expect, it } from 'vitest'
import { hasNewer, isComparableVersion } from './version'

describe('hasNewer', () => {
  it('认为 v0.10.0 比 v0.9.0 新', () => {
    expect(hasNewer('v0.10.0', 'v0.9.0')).toBe(true)
  })

  it('认为 rc8 早于同号正式版', () => {
    expect(hasNewer('v0.3.0', 'v0.3.0-rc8')).toBe(true)
    expect(hasNewer('v0.3.0-rc8', 'v0.3.0')).toBe(false)
  })
})

describe('isComparableVersion', () => {
  it('认得出正式版与预发布版', () => {
    expect(isComparableVersion('v0.3.3')).toBe(true)
    expect(isComparableVersion('0.3.0-rc8')).toBe(true)
  })

  // 承重：开发构建的版本戳是提交号。此前它走 hasNewer 得到 false，与「已经最新」
  // 无法区分，界面就把「我比不出来」显示成了「你没事」（真机实测：linux-01 显示
  // v0.3.3 可用、那台标「已是最新」，且升级按钮根本不出现）。
  it('认得出比不了的版本戳', () => {
    expect(isComparableVersion('7dec31185aaa')).toBe(false)
    expect(isComparableVersion('')).toBe(false)
    expect(isComparableVersion('v0.3')).toBe(false)
  })
})
