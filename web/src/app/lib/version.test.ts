// 更新版本比较的契约测试。
//
// 职责：守住数字段与预发布版本的排序语义。
// 边界：比较规则与 internal/selfupdate.CompareVersion 对齐，不测试界面文案。
import { describe, expect, it } from 'vitest'
import { hasNewer } from './version'

describe('hasNewer', () => {
  it('认为 v0.10.0 比 v0.9.0 新', () => {
    expect(hasNewer('v0.10.0', 'v0.9.0')).toBe(true)
  })

  it('认为 rc8 早于同号正式版', () => {
    expect(hasNewer('v0.3.0', 'v0.3.0-rc8')).toBe(true)
    expect(hasNewer('v0.3.0-rc8', 'v0.3.0')).toBe(false)
  })
})
