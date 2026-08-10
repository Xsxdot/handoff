//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

// store Unix PTY capability marker。
//
// 职责：让本机 Machine 只在存在真实 Unix PTY adapter 时声明 pty capability。
// 边界：不启动进程，也不依赖 ptyservice，避免 store 形成依赖环。
package store

const localPtySupported = true
