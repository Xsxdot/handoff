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
	"os"
	"path/filepath"
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
