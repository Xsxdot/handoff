// members.go —— 进程容器成员快照（members.json）的读写。
//
// 职责：定义一次容器成员采样的 PID 表与采样时刻，负责路径推导和原子读写。
//
// 边界：不复用 roster.json，不判定成员归属、不发信号；Windows 的 shim 写入它，
// unix 不写入。容器来源是否存在由 Handle.MembersPath 的数据决定，避免平台判断
// 散落在 Footprint 的多个调用点。
package prochost

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// MembersFileName 是容器成员快照的文件名（与 proc.json 同目录）。
const MembersFileName = "members.json"

// memberSnapshot 是一次容器成员采样的结果。
//
// SampledAt 记录采样时刻，因为 agentd 只能读文件而不能直接读 Job Object 句柄；
// 足迹输出必须能说明成员数字对应哪个时刻。
type memberSnapshot struct {
	PIDs      []int `json:"pids"`
	SampledAt int64 `json:"sampled_at"`
}

// membersPath 由 proc.json 的路径推出 members.json 的路径。
//
// infoPath 为空时返回空串，与 rosterPath 同款降级；调用方据此跳过容器来源。
func membersPath(infoPath string) string {
	if infoPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(infoPath), MembersFileName)
}

// readMembers 读取一份成员快照。
//
// 参数：path 为 members.json 的路径。
// 返回：快照；文件缺失或内容损坏时返回错误。
// 注意：缺失与损坏都不能静默返回空快照，否则上层会把「来源不可用」误读成
// 「任务确实没有进程」。
func readMembers(path string) (memberSnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return memberSnapshot{}, fmt.Errorf("读成员快照 %s: %w", path, err)
	}
	var snap memberSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return memberSnapshot{}, fmt.Errorf("解析成员快照 %s: %w", path, err)
	}
	return snap, nil
}

// writeMembers 原子写一份成员快照。
//
// 参数：path 为落点；snap 为本次采样结果。
// 注意：先写临时文件再 rename，避免 agentd 读到半截 JSON；临时文件与目标文件
// 位于同一目录，rename 才具备同一文件系统内的原子替换语义。
func writeMembers(path string, snap memberSnapshot) error {
	if path == "" {
		return fmt.Errorf("成员快照路径为空")
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("序列化成员快照: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写临时成员快照 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("落盘成员快照 %s: %w", path, err)
	}
	return nil
}

// containerSampleFn 是容器成员采样的平台缝。
//
// nil 表示本平台没有进程容器（unix），采样循环据此退出；Windows 的平台原语在
// init 中把它设为 jobProcessIDs。包级 var 让采样逻辑的测试不依赖真实 Job Object。
var containerSampleFn func() ([]int, error)

// membersSampler 持有成员快照的采样状态：路径与上一轮落盘的内容。
//
// 保留上一轮结果是为了在成员稳定时跳过原子写，避免每秒一次无意义的 I/O。
type membersSampler struct {
	path    string
	last    []int
	hasLast bool
	writes  int // 实际落盘次数，仅供测试断言「未变则不写」
}

// sample 采一次容器成员并按需落盘。
//
// 参数：l 为日志器。
// 返回：是否继续周期采样。false 只表示本平台永久没有进程容器；单次查询或
// 落盘失败都返回 true，因为下一轮可能恢复。
func (s *membersSampler) sample(l *slog.Logger) bool {
	if containerSampleFn == nil {
		l.Info("本平台无进程容器，不做成员采样")
		return false
	}
	if s.path == "" {
		l.Warn("无 info_path，无法落盘成员快照，本任务不做容器计数")
		return false
	}
	pids, err := containerSampleFn()
	if err != nil {
		l.Warn("查询容器成员失败，本轮跳过", "path", s.path, "cause", err)
		return true
	}
	if s.hasLast && equalInts(s.last, pids) {
		l.Debug("容器成员未变，跳过落盘", "count", len(pids))
		return true
	}
	if err := writeMembers(s.path, memberSnapshot{PIDs: pids, SampledAt: time.Now().UnixNano()}); err != nil {
		l.Warn("落盘成员快照失败，本轮跳过", "path", s.path, "cause", err)
		return true
	}
	s.last = append(s.last[:0], pids...)
	s.hasLast = true
	s.writes++
	l.Debug("成员快照已落盘", "count", len(pids), "path", s.path)
	return true
}

// equalInts 判断两个 PID 表是否逐项相等。
//
// 不排序、不去重：Job Object 返回的成员顺序由内核给定，保留原始序列可避免
// 不必要的分配，也不抹掉潜在的顺序变化信号。
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
