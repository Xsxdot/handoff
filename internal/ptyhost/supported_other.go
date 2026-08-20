//go:build !unix

// supported_other.go —— 非 Unix 平台的 PTY 能力常量。
//
// 职责：为 Supported 提供编译期降级结论。
// 边界：不实现 ConPTY 或其它终端原语；调用 Open 时由具体实现返回 ErrNotSupported。
package ptyhost

const ptySupported = false
