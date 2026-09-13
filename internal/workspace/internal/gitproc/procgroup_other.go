//go:build !unix

// 本文件提供 RunCmd 的进程组原语（非 unix 占位）：无 Setpgid 的平台降级为
// 不设组、不按组回收（进程树回收语义随平台而异），保证编译通过即可。
package gitproc

import "os/exec"

// SetProcGroup 非 unix 平台无进程组概念，空操作。
func SetProcGroup(cmd *exec.Cmd) {}

// KillProcGroup 非 unix 平台无进程组概念，空操作。
var KillProcGroup = func(pid int) {}
