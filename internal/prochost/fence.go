// fence.go —— 进程围栏的策略层（本 task 只落错误哨兵，策略在 Task 2 补齐）。
package prochost

import "errors"

// errFenceNotSupported 表示本平台没有进程围栏实现。
//
// 为什么要与 errNotSupported（进程枚举）分开：两者可以独立缺失，混用会让
// 「数得出但围不住」这种真实存在的状态没法表达。
var errFenceNotSupported = errors.New("本平台不支持进程围栏")
