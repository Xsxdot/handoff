// automation_cursor.go —— 本机自动化消费水位的独立文件介质。
//
// 职责：保存本机自动化消费 seq；边界：不保存 automationSeen、不写共享账本、
// 不承担 room/wait cursor。文件采用同目录临时文件加 rename，避免直接截断主文件。
package agentd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type automationCursorDisk struct {
	Seq int64 `json:"seq"`
}

type automationCursorStore struct {
	path string
}

func newAutomationCursorStore(path string) *automationCursorStore {
	return &automationCursorStore{path: path}
}

// Load 读取本机自动化消费水位；缺文件表示首次运行并返回 0，其余错误保留路径上下文。
func (s *automationCursorStore) Load() (int64, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读自动化 cursor 文件 %s: %w", s.path, err)
	}
	var disk automationCursorDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return 0, fmt.Errorf("解析自动化 cursor 文件 %s: %w", s.path, err)
	}
	return disk.Seq, nil
}

// Save 以 JSON seq 原子替换本机 cursor；失败时清理临时文件并保留主文件。
func (s *automationCursorStore) Save(seq int64) error {
	raw, err := json.Marshal(automationCursorDisk{Seq: seq})
	if err != nil {
		return fmt.Errorf("编码自动化 cursor: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("创建自动化 cursor 目录 %s: %w", filepath.Dir(s.path), err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("写自动化 cursor 临时文件 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换自动化 cursor 文件 %s: %w", s.path, err)
	}
	return nil
}

// advanceAutomationCursor 先更新内存再保存文件；保存失败不回滚内存，允许至少一次重放。
func (s *Server) advanceAutomationCursor(seq int64) error {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	if seq <= s.automationCursor {
		return nil
	}
	s.automationCursor = seq
	if s.automationCursorStore == nil {
		err := fmt.Errorf("自动化 cursor 持久化未装配：seq=%d", seq)
		s.log.Error("自动化 cursor 保存失败", "seq", seq, "cause", err)
		return err
	}
	if err := s.automationCursorStore.Save(seq); err != nil {
		s.log.Error("自动化 cursor 保存失败", "seq", seq,
			"path", s.automationCursorStore.path, "cause", err)
		return fmt.Errorf("保存自动化 cursor seq=%d: %w", seq, err)
	}
	s.log.Info("自动化 cursor 已保存", "seq", seq, "path", s.automationCursorStore.path)
	return nil
}
