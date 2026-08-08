//go:build unix

// opennonblock_unix.go —— 打开审阅文件时的 O_NONBLOCK 标志（unix 实现）。
//
// 职责：给 ReadFile 提供平台相关的「非阻塞打开」标志。
//
// 边界：只声明常量，不含任何逻辑。
package agentd

import "syscall"

// openNonBlock 让打开特殊文件（尤其是没有写端的 FIFO）立即返回而不是挂住，
// 使 ReadFile 的「不是普通文件」判定可达（见 ReadFile 的 why 注释）。
const openNonBlock = syscall.O_NONBLOCK
