//go:build !unix

// group_other.go —— 无进程组概念的平台退化路径：只杀直接子进程。opencode
// 自起 server 若成树残留，由真机清单第 6 条（Windows 条件项）承接；现役
// 协调机为 mac/linux（breakdown 缺陷族 3 原文），本文件不是本卡的验收面。
package hostapi

import (
	"os"
	"os/exec"
)

// configureProcess 无平台特化配置。
func configureProcess(cmd *exec.Cmd) {}

// killGroup 退化为单进程终止。
func killGroup(p *os.Process) error { return p.Kill() }
