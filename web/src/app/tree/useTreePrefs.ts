// useTreePrefs —— 左栏显示偏好的**共享**状态层（B160 §4.3）。
//
// 职责：让「左栏的偏好菜单」与「设置页的常规分区」读写同一份状态。
//
// 为什么是模块级单例 + 订阅，而不是 Context：ProjectTree 与 SettingsPage 不在
// 同一棵子树下（设置页整页替换中央内容区），套 Provider 要动到 Shell，收益
// 不抵改动面。模块级订阅是这个形状最小的解。
//
// 为什么不是「每个挂载点各自 useState(loadPrefs())」：那正是本文件要修的 bug
// ——设置页改一份，左栏那份不会知道，直到刷新页面。
//
// 边界：
//   - 不认识 React 之外的东西；规则本身仍在 treePrefs.ts（本文件只管状态与订阅）
//   - 不跨标签页同步（不监听 storage 事件）：两个标签页各改各的属于罕见场景，
//     为它引入「另一个标签页把我正在改的覆盖掉」的新问题不划算
import { useCallback, useEffect, useState } from 'react'
import { loadPrefs, savePrefs, type TreePrefs } from './treePrefs'

// current 是进程内唯一的一份偏好。惰性初始化：模块加载时读一次 localStorage。
let current: TreePrefs = loadPrefs()

// subscribers 是全部活着的挂载点。用 Set 而不是数组：退订是按引用删，O(1)。
const subscribers = new Set<(p: TreePrefs) => void>()

// setPrefs 落盘并通知全部订阅者。落盘与通知必须成对，分开写迟早漏一处。
function setPrefs(next: TreePrefs) {
  current = next
  savePrefs(next)
  for (const notify of subscribers) notify(next)
}

// useTreePrefs 返回当前偏好与更新函数。任意多个挂载点共享同一份状态。
//
// 返回：
//   - [0] 当前偏好（同一时刻所有挂载点拿到的是同一个对象）
//   - [1] 更新函数：落盘 + 通知全部挂载点
export function useTreePrefs(): [TreePrefs, (next: TreePrefs) => void] {
  const [prefs, setLocal] = useState<TreePrefs>(current)
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

// __resetTreePrefsForTest 重置模块级状态；只供测试隔离使用，生产代码不得调用。
export function __resetTreePrefsForTest(): void {
  current = loadPrefs()
  subscribers.clear()
}
