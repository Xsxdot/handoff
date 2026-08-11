// Package selfupdate 决定 agentd 什么时候换版、现在能不能换。
//
// 职责：
//   - 定时查新版，下好待命（下载与校验委托给 internal/release）
//   - 把「已就绪待命的版本」持久化到 <DataDir>/update/pending.json，
//     使 agentd 重启后不必重下
//   - 换版两道闸中的托管判据（IsManaged）已迁到 managed.go；
//     本文件只负责待命更新的持久化，「现在有没有活跃任务」那道闸在 updater.go
//   - 到窗口时替换二进制并触发优雅关停，由管理器拉起新版
//
// 边界：
//   - 不自己下载、不自己 rename：那些是 internal/release 的事
//   - 不 import internal/agentd（会成环）：关停经注入的闭包完成
//   - 不做自动回滚（D10）
package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Pending 是一个已下载、已校验、等待窗口的新版本。
type Pending struct {
	// Version 是新版本 tag，形如 v0.2.0。
	Version string `json:"version"`
	// Path 是临时二进制的完整路径（与目标二进制同目录）。
	Path string `json:"path"`
	// DownloadedAt 是下载完成时刻，供 status 展示「等了多久」。
	DownloadedAt time.Time `json:"downloaded_at"`
}

// PendingPath 返回 pending.json 的落点。
func PendingPath(dataDir string) string {
	return filepath.Join(dataDir, "update", "pending.json")
}

// LoadPending 读取待命更新。
//
// 返回：
//   - 待命更新；**文件不存在时返回 (nil, nil)**——没有待命更新是绝大多数时候的
//     正常状态，当错误处理会让日志每轮刷一条 Error，把真正的错误淹掉
//   - 错误：读失败或 JSON 损坏。损坏必须报出来，静默当成「没有」会让
//     「更新一直不生效」永远查不出原因
func LoadPending(dataDir string) (*Pending, error) {
	p := PendingPath(dataDir)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", p, err)
	}
	var out Pending
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("解析 %s（文件已损坏，删掉它即可重新下载）: %w", p, err)
	}
	return &out, nil
}

// SavePending 写入待命更新，自动建目录。
func SavePending(dataDir string, p *Pending) error {
	path := PendingPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("建目录 %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 pending: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// ClearPending 删除待命记录。文件本来就不在时返回 nil（幂等）。
func ClearPending(dataDir string) error {
	err := os.Remove(PendingPath(dataDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删 %s: %w", PendingPath(dataDir), err)
	}
	return nil
}
