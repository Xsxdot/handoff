//go:build unix

// supported_unix.go —— ptyhost 公共包的 Unix PTY 能力常量。
//
// 职责：为 Supported 提供编译期能力结论。
// 边界：不启动 PTY、不连接引擎；引擎自己的同名常量留在 engine 包，避免包依赖环。
package ptyhost

const ptySupported = true
