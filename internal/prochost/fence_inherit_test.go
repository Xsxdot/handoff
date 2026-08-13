//go:build darwin || linux

package prochost

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
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
	// 用 x/sys/unix 而不是 syscall：syscall.Getsid 只在 darwin/BSD 上有，
	// linux 的 syscall 包没有导出它——本文件的构建标签含 linux，直接用
	// syscall.Getsid 会让 ubuntu 上的 go vet/test 编译失败（CI 实测炸过）
	sid, _ := unix.Getsid(0)
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

// TestHelperFenceRaise 扮演被围住的 executor：装上围栏后试图把限值抬回装之前。
func TestHelperFenceRaise(t *testing.T) {
	if os.Getenv(fenceHelperEnv) != "raise" {
		t.Skip("非 helper 调用")
	}
	want, _ := strconv.Atoi(os.Getenv("HANDOFF_FENCE_VALUE"))
	orig, _ := strconv.Atoi(os.Getenv("HANDOFF_ORIG_LIMIT"))
	if err := setNprocLimit(want); err != nil {
		os.Stdout.WriteString("SETFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	// 抬回装之前的软硬限：硬限只能降不能升（升需特权），正确实现下必然失败
	if err := setNprocLimit(orig); err != nil {
		os.Stdout.WriteString("RAISE_DENIED\n")
	} else {
		os.Stdout.WriteString("RAISE_OK\n")
	}
	os.Exit(0)
}

// 围栏必须是单向门：被围住的进程不能把限值抬回装之前。只压软限的话，
// executor 一句 setrlimit 就能拆掉围栏，整个方案形同虚设。
// 为什么抬回目标是原值 cur 而不是 2 倍：能不能回到「装之前」才是单向门要证的
// 事，抬到一个随便的大数只是顺带。want 取 cur-1（与 TestFenceSurvivesSetsid
// 同款），确保与环境值可区分、用例不恒真。
func TestFenceCannotBeRaisedBack(t *testing.T) {
	cur, err := getNprocLimit()
	if err != nil || cur < 64 {
		t.Skip("当前软限太小或读不到，无法构造可区分的围栏值")
	}
	want := cur - 1
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperFenceRaise")
	cmd.Env = append(os.Environ(), fenceHelperEnv+"=raise",
		"HANDOFF_FENCE_VALUE="+strconv.Itoa(want),
		"HANDOFF_ORIG_LIMIT="+strconv.Itoa(cur))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper 执行失败: %v (输出 %q)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "RAISE_DENIED" {
		t.Fatalf("围栏应拆不掉，helper 报告 %q", got)
	}
}
