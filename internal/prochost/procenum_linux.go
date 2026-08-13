//go:build linux

// procenum_linux.go —— linux 的进程枚举实现（读 /proc，不 fork）。
package prochost

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// bootTimeNano 读 /proc/stat 的 btime，返回系统启动时刻（unix 纳秒）。
//
// 为什么需要它：/proc/<pid>/stat 的 starttime 是「自开机以来的时钟嘀嗒数」，
// 必须叠加开机时刻才能变成可与 shim 启动时刻直接比较的绝对时间。
func bootTimeNano() (int64, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, fmt.Errorf("读 /proc/stat: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "btime ") {
			continue
		}
		sec, perr := strconv.ParseInt(strings.TrimSpace(line[len("btime "):]), 10, 64)
		if perr != nil {
			return 0, fmt.Errorf("解析 btime: %w", perr)
		}
		return sec * int64(time.Second), nil
	}
	return 0, fmt.Errorf("/proc/stat 里没有 btime 行")
}

// clockTick 是 linux 的 USER_HZ。内核对 /proc 恒定按 100 导出，与 CONFIG_HZ 无关。
const clockTick = 100

// enumProcs 遍历 /proc 取当前 uid 的全部进程。
//
// 返回：
//   - 每个进程的 pid / pgid / 启动时刻（unix 纳秒）
//   - /proc 不可读时返回错误；单个进程读失败一律跳过（进程随时会消失，那是常态）
func enumProcs() ([]procEntry, error) {
	boot, err := bootTimeNano()
	if err != nil {
		log().Error("读系统启动时刻失败", "cause", err)
		return nil, err
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		log().Error("读 /proc 失败", "cause", err)
		return nil, fmt.Errorf("读 /proc: %w", err)
	}
	uid := os.Getuid()
	out := make([]procEntry, 0, 256)
	for _, e := range ents {
		pid, cerr := strconv.Atoi(e.Name())
		if cerr != nil {
			continue // 非数字目录不是进程
		}
		fi, serr := os.Stat("/proc/" + e.Name())
		if serr != nil {
			continue // 进程刚消失：常态，不是错误
		}
		sys, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || int(sys.Uid) != uid {
			continue // 只要当前 uid 的
		}
		ppid, pgid, start, perr := readStat(pid, boot)
		if perr != nil {
			continue // 同上：读到一半进程没了
		}
		out = append(out, procEntry{PID: pid, PPID: ppid, PGID: pgid, StartedAt: start})
	}
	log().Debug("进程枚举完成", "uid", uid, "count", len(out))
	return out, nil
}

// readStat 解析 /proc/<pid>/stat，取 ppid（字段 4）、pgrp（字段 5）与 starttime（字段 22）。
//
// 注意：字段 2 是 comm，可能含空格与右括号（如 "(my prog)"），因此必须从
// **最后一个** ')' 之后开始切分，不能直接按空格分割整行。
func readStat(pid int, bootNano int64) (ppid, pgid int, startedAt int64, err error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, 0, err
	}
	s := string(b)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 >= len(s) {
		return 0, 0, 0, fmt.Errorf("stat 格式异常 pid=%d", pid)
	}
	// idx+2 起是字段 3（state）；fields[0]=state, fields[1]=ppid, fields[2]=pgrp,
	// fields[19]=starttime
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 20 {
		return 0, 0, 0, fmt.Errorf("stat 字段不足 pid=%d, got %d", pid, len(fields))
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("解析 ppid pid=%d: %w", pid, err)
	}
	pgid, err = strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("解析 pgrp pid=%d: %w", pid, err)
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("解析 starttime pid=%d: %w", pid, err)
	}
	return ppid, pgid, bootNano + ticks*int64(time.Second)/clockTick, nil
}

// procLimit 读当前进程的 RLIMIT_NPROC 软上限（每 uid 可创建进程数）。
func procLimit() (int, error) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(unix.RLIMIT_NPROC, &rl); err != nil {
		log().Error("读 RLIMIT_NPROC 失败", "cause", err)
		return 0, fmt.Errorf("getrlimit RLIMIT_NPROC: %w", err)
	}
	return int(rl.Cur), nil
}
