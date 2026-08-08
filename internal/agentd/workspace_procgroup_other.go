//go:build !unix

// 本文件提供 RunCmd 的进程组原语（非 unix 占位）：无 Setpgid 的平台降级为
// 不设组、不按组回收（进程树回收语义随平台而异），保证编译通过即可。
package agentd

import "os/exec"

// setProcGroup 非 unix 平台无进程组概念，空操作。
func setProcGroup(cmd *exec.Cmd) {}

// killProcGroup 非 unix 平台无进程组概念，空操作。
var killProcGroup = func(pid int) {}
