//go:build darwin || linux

package prochost

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const fenceHelperEnv = "HANDOFF_FENCE_HELPER"

// TestHelperFenceParent 扮演 shim：装上围栏，再以 setsid 拉起一个孙进程，
// 把孙进程看到的软限打到 stdout。生产里这两步分别由 RunShim 与 executor 完成。
func TestHelperFenceParent(t *testing.T) {
	if os.Getenv(fenceHelperEnv) != "parent" {
		t.Skip("非 helper 调用")
	}
	want, _ := strconv.Atoi(os.Getenv("HANDOFF_FENCE_VALUE"))
	if err := setNprocLimit(want); err != nil {
		os.Stdout.WriteString("SETFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperFenceChild")
	cmd.Env = append(os.Environ(), fenceHelperEnv+"=child")
	// Setsid=true 让子进程 pid==sid==pgid，完全脱离本进程的会话与进程组——
	// 精确复刻 opencode Bash 工具对每条命令做的那件事
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		os.Stdout.WriteString("CHILDFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	os.Stdout.Write(out)
	os.Exit(0)
}

// TestHelperFenceChild 是被 setsid 拉起的孙进程：报告自己的会话身份与软限。
func TestHelperFenceChild(t *testing.T) {
	if os.Getenv(fenceHelperEnv) != "child" {
		t.Skip("非 helper 调用")
	}
	lim, err := getNprocLimit()
	if err != nil {
		os.Stdout.WriteString("GETFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	sid, _ := syscall.Getsid(0)
	os.Stdout.WriteString("PID=" + strconv.Itoa(os.Getpid()) +
		" SID=" + strconv.Itoa(sid) +
		" LIMIT=" + strconv.Itoa(lim) + "\n")
	os.Exit(0)
}

// 围栏必须穿透 setsid：这是整个方案的地基。地基塌了，按进程组收不到的那些
// 逃逸树就同样不受围栏约束，B73 等于没做。
//
// 为什么必须在子进程里压限值而不是在测试进程里：setNprocLimit 同压软硬限，
// 是不可逆的单向门——在测试进程里压一次，之后所有用例（以及 go test 自己
// 要 fork 的编译/测试二进制）都会跟着受限。
//
// 为什么 want 取 cur-1 而不是 cur：want==cur 时一个什么都不做的空围栏也能让
// 子孙进程看到同样的值，用例会退化成恒真（正是 B47 要防的假用例）。取 cur-1
// 确保断言 LIMIT=want 匹配到的是围栏真实改过的值（cur-1）而非环境软限 cur。
func TestFenceSurvivesSetsid(t *testing.T) {
	cur, err := getNprocLimit()
	if err != nil || cur < 64 {
		t.Skip("当前软限太小或读不到，无法构造可区分的围栏值")
	}
	want := cur - 1 // 恒 <= hard（软限本就 <= 硬限），且远大于当前占用，与环境值可区分
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperFenceParent")
	cmd.Env = append(os.Environ(), fenceHelperEnv+"=parent",
		"HANDOFF_FENCE_VALUE="+strconv.Itoa(want))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper 执行失败: %v (输出 %q)", err, out)
	}
	line := strings.TrimSpace(string(out))
	if !strings.Contains(line, "LIMIT="+strconv.Itoa(want)) {
		t.Fatalf("setsid 后的孙进程应继承围栏 %d，实际输出 %q", want, line)
	}
	// 同时确认它真的逃逸了（pid==sid），否则这条用例证明不了任何事
	var pid, sid int
	for _, f := range strings.Fields(line) {
		k, v, _ := strings.Cut(f, "=")
		n, _ := strconv.Atoi(v)
		switch k {
		case "PID":
			pid = n
		case "SID":
			sid = n
		}
	}
	if pid == 0 || pid != sid {
		t.Fatalf("孙进程未真正脱离会话（pid=%d sid=%d），本用例不成立: %q", pid, sid, line)
	}
}
