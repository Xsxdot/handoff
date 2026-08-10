//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

// ptyservice 非 Unix adapter：本阶段明确拒绝，不伪装 ConPTY 已完成。
//
// 职责：保持跨平台编译并返回 capability unsupported 的底层原因。
// 边界：不回退 pipe 或普通 child_process，因为那不具备 PTY 语义。
package ptyservice

import "fmt"

type ptyProcess interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(uint16, uint16) error
	Terminate() error
	Kill() error
	Wait() (int, error)
}

// Supported 报告当前构建不具备真实 PTY adapter。
func Supported() bool { return false }

func defaultShell() string { return "" }

func startPtyProcess(string, string, uint16, uint16) (ptyProcess, error) {
	return nil, fmt.Errorf("当前平台不支持 PTY")
}
