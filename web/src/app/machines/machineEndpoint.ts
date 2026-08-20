// 机器卡片上那行端点文案的唯一来源。
//
// 职责：把 Machine 的 addr / relay 两个互斥字段折成一行可展示文本
// 边界：只管展示，不判断可达性——「已断开」由 machine.reachable 单独渲染
//
// 为什么需要它：relay 形态的机器 addr 恒为空（配置层强制互斥），卡片上会一个
// 身份标识都没有——列表里两台中继机器长得完全一样。
import type { Machine } from '../../api/types'

export function machineEndpoint(machine: Pick<Machine, 'addr' | 'relay'>): string {
  if (machine.addr) return machine.addr
  if (machine.relay) return `中继 · ${machine.relay}`
  return ''
}
