//go:build !unix

// PTY 平台原语在非 unix 平台上的桩实现。
//
// 职责：让 `GOOS=windows go build ./...` 能过，并把「不支持」这个事实
// 沿 ErrNotSupported 一路传到 HTTP 层（501）与 /api/status 的 pty_supported=false。
//
// 边界：不做任何模拟。Windows 的 ConPTY 是另一套 API，本轮不假装支持（spec §10）。
//
// 文件名用 _other 而不是 _windows：Go 会把 _windows.go 当成隐式 GOOS 约束，
// 那样 `//go:build !unix` 里除 windows 外的其它非 unix 平台就没有实现了。
// 与 internal/prochost/platform_other.go 同款。
package ptyhost

import (
	"os"
	"os/exec"
)

const ptySupported = false

func startPty(shell, cwd string, env []string, cols, rows int) (*os.File, *exec.Cmd, error) {
	return nil, nil, ErrNotSupported
}

func resizePty(f *os.File, cols, rows int) error { return ErrNotSupported }

func terminatePty(cmd *exec.Cmd) error { return ErrNotSupported }

func killPty(cmd *exec.Cmd) error { return ErrNotSupported }

// waitExitCode 在本平台永远不会被调用（会话根本起不来），返回 -1 表示未知。
func waitExitCode(cmd *exec.Cmd) int { return -1 }

// foregroundPgid 在没有 PTY 的平台上恒为「读不到」。
//
// 返回 false 而不是「没有前台进程」：这里连会话都开不出来，给一个 bool 结论
// 等于替一个不存在的东西作证。
func foregroundPgid(*os.File) (int, bool) { return 0, false }
