// useWebPrefs —— Web 客户端偏好的**共享**状态层（B369.8 §3.4）。
//
// 职责：让「设置中心的偏好控件」与「Shell 的消费点（会话打开方式、底栏角标）」
// 读写同一份状态。
//
// 为什么是模块级单例 + 订阅，而不是 Context：SettingsHub 与 Shell 的消费点不在
// 同一棵子树下（设置中心是 mobile-home 覆盖层里的一个内容面，角标在底栏），
// 套 Provider 要动到 Shell 的 JSX 结构，收益不抵改动面。模块级订阅是
// useTreePrefs 验证过的同款最小解。
//
// 边界：
//   - 不认识 React 之外的东西；规则本身仍在 webPrefs.ts（本文件只管状态与订阅）
//   - 不跨标签页同步（不监听 storage 事件）：与 useTreePrefs 同一条取舍
import { useCallback, useEffect, useState } from 'react'
import { loadPrefs, savePrefs, type WebPrefs } from './webPrefs'

// current 是进程内唯一的一份偏好。惰性初始化：模块加载时读一次 localStorage。
let current: WebPrefs = loadPrefs()

// subscribers 是全部活着的挂载点。用 Set 而不是数组：退订是按引用删，O(1)。
const subscribers = new Set<(p: WebPrefs) => void>()

// setPrefs 落盘并通知全部订阅者。落盘与通知必须成对，分开写迟早漏一处。
function setPrefs(next: WebPrefs) {
  current = next
  savePrefs(next)
  for (const notify of subscribers) notify(next)
}

// useWebPrefs 返回当前偏好与更新函数。任意多个挂载点共享同一份状态。
//
// 返回：
//   - [0] 当前偏好（同一时刻所有挂载点拿到的是同一个对象）
//   - [1] 更新函数：落盘 + 通知全部挂载点
export function useWebPrefs(): [WebPrefs, (next: WebPrefs) => void] {
  const [prefs, setLocal] = useState<WebPrefs>(current)
  useEffect(() => {
    subscribers.add(setLocal)
    // 订阅建立与初始 useState 之间可能已有一次更新，补一次对齐
    setLocal(current)
    return () => {
      subscribers.delete(setLocal)
    }
  }, [])
  return [prefs, useCallback(setPrefs, [])]
}

// __resetWebPrefsForTest 重置模块级状态；只供测试隔离使用，生产代码不得调用。
export function __resetWebPrefsForTest(): void {
  current = loadPrefs()
  subscribers.clear()
}
