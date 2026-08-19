// windows_build_test.go —— Windows 交叉编译门禁。
//
// 职责：把「GOOS=windows 必须能编译」从口头约定变成可执行的断言。
//
// 边界：只验证能编译，不验证能运行——Windows 运行时实现是 B 期的事
// （见 spec §7）。本用例在 -short 下跳过：它要跑一次完整交叉编译，约数秒。
package prochost

import (
	"os/exec"
	"testing"
)

// TestWindowsCrossCompiles 断言整个模块在 GOOS=windows 下编译通过。
//
// 为什么这条测试值得存在：A 期的全部价值就是「架构上平台无关、Windows 实现留空」。
// 没有门禁的话，任何人往 adapter 里加一个 syscall.Xxx 都会悄悄把 Windows 之路
// 重新堵死，而 CI 全绿——本项目此前正是这样卡在 syscall.Mkfifo 上的。
func TestWindowsCrossCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("-short：跳过交叉编译门禁")
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Env = append(cmd.Environ(), "GOOS=windows", "GOARCH=amd64")
	cmd.Dir = ".." + string('/') + ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("GOOS=windows go build ./... 失败（Windows 之路被堵死）:\n%s", out)
	}
}

// TestWindowsVets 断言整个模块在 GOOS=windows 下 vet 通过。
//
// 为什么 build 门不够：build 只看非测试代码，而 unix-only 的**测试**文件同样会
// 把 Windows 之路堵死——它们不加 build tag 时，任何人在 Windows 上跑 go test
// 都会先撞编译错误。B37 落地后真机 e2e 不可能每个 PR 跑，vet 门是唯一守得住的。
func TestWindowsVets(t *testing.T) {
	if testing.Short() {
		t.Skip("-short：跳过交叉 vet 门禁")
	}
	cmd := exec.Command("go", "vet", "./...")
	cmd.Env = append(cmd.Environ(), "GOOS=windows", "GOARCH=amd64")
	cmd.Dir = ".." + string('/') + ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("GOOS=windows go vet ./... 失败（Windows 之路被堵死）：\n%s", out)
	}
}
