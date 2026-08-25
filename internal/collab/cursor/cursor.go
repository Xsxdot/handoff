// Package cursor 是未读游标的持久化介质（B156.2 C5，移交区 A.1 岔口四方案甲）：
// 按成员按房间记 seq 水位，datadir 下 JSON 文件缓存，tmp+rename 原子写。
// 纯缓存非权威（拍板 5.4）：重启丢失无害、打开房间即自愈；免引入第二个
// 数据库句柄。path 为空的 Store 是纯内存形态（Service.New 的默认），组装点
// 持 DataDir，用 cursor.New(path) + Service.SetCursorStore 换成文件介质。
package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store 未读游标存储：data 是 member -> roomID -> uptoSeq 的 map。全部读写
// 经互斥锁串行化（HTTP handler 并发到达的原子性在此）；文件介质每次写入
// tmp+rename 原子替换。
type Store struct {
	mu     sync.Mutex
	path   string
	loaded bool
	data   map[string]map[string]int64
}

// New 构造游标存储。path 空 = 纯内存；非空 = 文件介质，首次访问时从 path
// 加载，之后每次 MarkRead 后原子落盘。
func New(path string) *Store {
	return &Store{path: path, data: map[string]map[string]int64{}}
}

// MarkRead 置该成员在该房间的已读水位。单调：只进不退（并发到达时取大值），
// 防止旧请求把新水位拉回。member/roomID 为空直接报错（没有「谁/哪」的游标
// 无意义）。
func (s *Store) MarkRead(member, roomID string, uptoSeq int64) error {
	if member == "" || roomID == "" {
		return fmt.Errorf("游标必须带 member 与 roomID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return err
	}
	perRoom := s.data[member]
	if perRoom == nil {
		perRoom = map[string]int64{}
		s.data[member] = perRoom
	}
	if uptoSeq > perRoom[roomID] {
		perRoom[roomID] = uptoSeq
	}
	return s.persist()
}

// Cursor 读该成员在该房间的已读水位；未记过返回 0。
func (s *Store) Cursor(member, roomID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return 0, err
	}
	return s.data[member][roomID], nil
}

// ensureLoaded 首次访问时从磁盘加载（重启/换实例后读回持久化水位）。
func (s *Store) ensureLoaded() error {
	if s.path == "" || s.loaded {
		return nil
	}
	s.loaded = true
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 首次使用：无文件即空游标
		}
		return fmt.Errorf("读游标文件 %s: %w", s.path, err)
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return fmt.Errorf("解析游标文件 %s: %w", s.path, err)
	}
	return nil
}

// persist tmp+rename 原子写：主文件不被半截写损坏；崩溃残留的 tmp 残屑无害
// （拍板 5.4「重启丢失无害」同族，只是更保守——数据已过 rename 才可见）。
func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.data)
	if err != nil {
		return fmt.Errorf("编码游标: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("建游标目录: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写游标 tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("游标原子替换: %w", err)
	}
	return nil
}
