//go:build darwin

// taskmark_darwin.go —— darwin 的任务标记实现：按 cwd 归属。
//
// 为什么是 cwd 而不是环境变量：macOS 对 Apple 平台二进制的 environ 做了屏蔽，
// 非 root 读 /bin/sleep、/bin/zsh 的环境变量恒为空——而工具壳正是 zsh、
// 泄漏的正是 sleep 与编译进程，环境变量方案恰好看不见最需要看见的那一类
// （spec §4.1 实测：全表 environ 可读率对平台二进制为 0，cwd 为 99.9%）。
//
// 为什么不用 x/sys/unix：它不包装 proc_pidinfo。cgo 不是选项（本仓库依赖
// 纯 Go 交叉编译），故走 stdlib 的 syscall.Syscall6。该路径在 Go 文档里标注
// deprecated，因此本文件带运行期自检，失效即整条判据降级（见 cwdReadable）。
//
// 边界：只读 cwd，不发信号、不判存活；不得 fork。
package prochost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	// sysProcInfo 是 darwin 的 proc_info 系统调用号。
	sysProcInfo = 336
	// callPIDInfo 对应 PROC_INFO_CALL_PIDINFO。
	callPIDInfo = 2
	// flavorVnodePath 对应 PROC_PIDVNODEPATHINFO，返回 proc_vnodepathinfo。
	flavorVnodePath = 9
	// vipPathOffset 是 cwd 字符串在 proc_vnodepathinfo 里的偏移。
	//
	// **这个值是实测出来的，不是照头文件推算的**：按 struct vinfo_stat 手算
	// 会得到别的数。它由 cwdReadable() 在运行期用「能否读出本进程自己的 cwd」
	// 反证，对不上就整条判据降级，绝不拿一个可能错位的解析结果去归属进程。
	vipPathOffset = 152
	// vnodePathBufSize 是一次调用的缓冲区大小（两个 vnode_info_path，各含
	// MAXPATHLEN 的路径）。
	vnodePathBufSize = 4096
)

// cwdOf 读出 pid 的当前工作目录（不 fork）。
//
// 返回：内核给出的**已解析**绝对路径；读不到时返回错误。
//
// 注意：进程刚退出、或对方是本 uid 之外的进程时会失败，这是常态，
// 调用方跳过该条即可，不该据此认定「不属于本任务」。
func cwdOf(pid int) (string, error) {
	buf := make([]byte, vnodePathBufSize)
	got, _, errno := syscall.Syscall6(sysProcInfo, callPIDInfo, uintptr(pid),
		flavorVnodePath, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return "", fmt.Errorf("proc_pidinfo(vnodepath) pid=%d: %w", pid, errno)
	}
	if got == 0 || int(got) <= vipPathOffset {
		return "", fmt.Errorf("proc_pidinfo(vnodepath) pid=%d 返回 %d 字节，不足以含路径", pid, got)
	}
	b := buf[vipPathOffset:got]
	if i := indexZero(b); i >= 0 {
		b = b[:i]
	}
	if len(b) == 0 {
		return "", fmt.Errorf("proc_pidinfo(vnodepath) pid=%d 路径为空", pid)
	}
	return string(b), nil
}

// indexZero 返回第一个 0 字节的下标；没有则返回 -1。
func indexZero(b []byte) int {
	for i := range b {
		if b[i] == 0 {
			return i
		}
	}
	return -1
}

// cwdSelfCheck 缓存偏移量自检结果：只做一次，结果全进程复用。
var cwdSelfCheck struct {
	once sync.Once
	ok   bool
}

// cwdReadable 报告本机上 cwd 判据是否可用。
//
// 判据：拿本进程试一次——用 syscall 读出的 cwd 必须等于 os.Getwd() 的解析结果。
//
// 为什么要这道自检：vipPathOffset 是实测值，而 syscall.Syscall6 在 darwin 上是
// deprecated 路径。两者任一在未来失效，解析出来的都会是垃圾字符串——而垃圾
// 字符串「恰好不等于任何 MarkRoot」，判据会**静默退化成永远不命中**，没有任何
// 报错。有了自检，失效表现为整条判据降级回 pgid + roster（spec §8 第四档），
// 那是设计好的行为，不是新的失败模式。
func cwdReadable() bool {
	cwdSelfCheck.once.Do(func() {
		want, err := os.Getwd()
		if err != nil {
			log().Warn("取本进程 cwd 失败，停用 cwd 归属判据", "cause", err)
			return
		}
		wantResolved, err := filepath.EvalSymlinks(want)
		if err != nil {
			log().Warn("解析本进程 cwd 失败，停用 cwd 归属判据", "cwd", want, "cause", err)
			return
		}
		got, err := cwdOf(os.Getpid())
		if err != nil {
			log().Warn("proc_pidinfo 自检失败，停用 cwd 归属判据，归属退回 pgid+roster",
				"cause", err)
			return
		}
		if got != wantResolved {
			log().Warn("proc_pidinfo 自检结果与 os.Getwd 不符，停用 cwd 归属判据",
				"got", got, "want", wantResolved, "offset", vipPathOffset)
			return
		}
		cwdSelfCheck.ok = true
		log().Info("cwd 归属判据可用", "offset", vipPathOffset)
	})
	return cwdSelfCheck.ok
}

// attributes 判定 pid 是否属于 cred 所描述的任务（darwin：按 cwd）。
//
// 返回：
//   - true: 该进程的 cwd 落在 cred.MarkRoot 之内
//   - ErrNotSupported: 本机自检未通过（见 cwdReadable），调用方应降级
//   - 其它错误: 该 pid 读不到（多为刚退出），调用方跳过该条
//
// 注意：cred.MarkRoot 为空即一律不命中——「仅托管 worktree 可杀」在这里落地。
// 本判据**不抗 cd**：进程 cd 出任务目录后就脱钩，这是 macOS 侧结构性的覆盖上限。
func attributes(pid int, cred TaskCred) (bool, error) {
	if cred.MarkRoot == "" {
		return false, nil
	}
	if !cwdReadable() {
		return false, ErrNotSupported
	}
	cwd, err := cwdOf(pid)
	if err != nil {
		return false, err
	}
	return underRoot(cwd, cred.MarkRoot), nil
}

// underRoot 判定 path 是否等于 root 或在 root 之下。
//
// 为什么不用 strings.HasPrefix 单独判：/a/bc 会被 /a/b 的前缀匹配命中，
// 那是另一个目录。必须带上分隔符。
func underRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
