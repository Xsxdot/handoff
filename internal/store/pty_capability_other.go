//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

// store 非 Unix PTY capability marker。
//
// 职责：在没有真实 PTY adapter 的平台隐藏 pty capability。
// 边界：不把普通 pipe 伪装成 PTY。
package store

const localPtySupported = false
