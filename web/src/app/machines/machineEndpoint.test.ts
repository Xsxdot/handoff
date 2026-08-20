import { describe, expect, it } from 'vitest'
import { machineEndpoint } from './machineEndpoint'

describe('machineEndpoint', () => {
  it('直连机器显示地址', () => {
    expect(machineEndpoint({ addr: '100.73.238.21:7777', relay: '' })).toBe('100.73.238.21:7777')
  })

  it('中继机器显示节点名', () => {
    expect(machineEndpoint({ addr: '', relay: 'linux-01' })).toBe('中继 · linux-01')
  })

  // 两者都有时以 addr 为准：addr 与 relay 在配置层互斥，真出现说明配置有问题，
  // 显示直连地址至少是可验证的那一个。
  it('两者都有时以地址为准', () => {
    expect(machineEndpoint({ addr: '10.0.0.1:7777', relay: 'x' })).toBe('10.0.0.1:7777')
  })

  // 本机的 addr 是 listen 地址，relay 恒空；两者皆空只发生在数据异常时，
  // 返回空串让调用方的 truncate 容器自然收成 0 高，不要塞占位符。
  it('两者都空时返回空串', () => {
    expect(machineEndpoint({ addr: '', relay: '' })).toBe('')
  })
})
