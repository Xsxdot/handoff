//go:build !unix

// procgroup_other.go —— 非 unix 平台：没有进程组整组终止的保证，agy 探测不执行
// （plan §7 跨平台族：Windows 上 agy 探测报「本平台未支持」）。
package quota

import "os/exec"

const procGroupSupported = false

func isolateGroup(cmd *exec.Cmd) {}
