//go:build darwin

// procenum_darwin.go —— darwin 的进程枚举实现（sysctl KERN_PROC，不 fork）。
package prochost

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// enumProcs 用 sysctl KERN_PROC_UID 取当前 uid 的全部进程。
//
// 返回：
//   - 每个进程的 pid / pgid / 启动时刻（unix 纳秒）
//   - sysctl 失败时返回错误；不返回 ErrNotSupported（本平台是支持的）
//
// 注意：kinfo_proc 的 p_starttime 是 struct timeval（墙钟），直接换算 unix 纳秒。
func enumProcs() ([]procEntry, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		log().Error("sysctl 枚举进程失败", "uid", os.Getuid(), "cause", err)
		return nil, fmt.Errorf("sysctl kern.proc.uid: %w", err)
	}
	out := make([]procEntry, 0, len(kps))
	for i := range kps {
		st := kps[i].Proc.P_starttime
		out = append(out, procEntry{
			PID:       int(kps[i].Proc.P_pid),
			PPID:      int(kps[i].Eproc.Ppid),
			PGID:      int(kps[i].Eproc.Pgid),
			StartedAt: int64(st.Sec)*int64(time.Second) + int64(st.Usec)*int64(time.Microsecond),
		})
	}
	log().Debug("进程枚举完成", "uid", os.Getuid(), "count", len(out))
	return out, nil
}

// procLimit 读 kern.maxprocperuid（每 uid 进程数上限）。
func procLimit() (int, error) {
	n, err := unix.SysctlUint32("kern.maxprocperuid")
	if err != nil {
		log().Error("读 kern.maxprocperuid 失败", "cause", err)
		return 0, fmt.Errorf("sysctl kern.maxprocperuid: %w", err)
	}
	return int(n), nil
}
