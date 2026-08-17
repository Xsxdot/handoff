package agentd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/store"
)

// newAdminServer 造一台只用于配置读写测试的 Server：真实临时配置文件 + 空存储。
func newAdminServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{
		Listen:  "127.0.0.1:0",
		Token:   "t",
		DataDir: dir,
		Targets: map[string]config.Target{},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("准备配置失败: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "handoff.db"))
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetConfigPath(cfgPath)
	return s
}

// 并发读快照 + 并发换配置不得报竞态，且不得丢更新。
// 用 -race 跑才有意义。
func TestConfSnapshotConcurrent(t *testing.T) {
	s := newAdminServer(t)
	const writers = 10
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = len(s.conf().Targets) }()
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("m%d", i)
			if err := s.swapConf(func(c *config.Config) error {
				c.Targets[name] = config.Target{Addr: "127.0.0.1:1", Token: "x"}
				return nil
			}); err != nil {
				t.Errorf("换配置失败: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(s.conf().Targets); got != writers {
		t.Fatalf("并发写丢更新：期望 %d 台，实际 %d 台", writers, got)
	}
}

// 落盘失败时内存快照必须回滚，否则重启后配置凭空消失。
func TestSwapConfRollbackOnSaveFailure(t *testing.T) {
	s := newAdminServer(t)
	// 把配置路径指到一个不可写的位置：父路径里夹了一个普通文件，
	// MkdirAll 建不出目录（ENOTDIR），Save 必失败——父目录「不存在」不够，
	// config.Save 会先把父目录 MkdirAll 出来
	block := filepath.Join(t.TempDir(), "block")
	if err := os.WriteFile(block, []byte("x"), 0o644); err != nil {
		t.Fatalf("造不可写路径失败: %v", err)
	}
	s.SetConfigPath(filepath.Join(block, "config.yaml"))
	before := len(s.conf().Targets)
	err := s.swapConf(func(c *config.Config) error {
		c.Targets["x"] = config.Target{Addr: "127.0.0.1:1", Token: "x"}
		return nil
	})
	if err == nil {
		t.Fatal("落盘应当失败")
	}
	if got := len(s.conf().Targets); got != before {
		t.Fatalf("落盘失败后内存未回滚：期望 %d 台，实际 %d 台", before, got)
	}
	_ = os.Remove("")
}

// mutate 返回错误时不得落盘、不得换快照。
func TestSwapConfMutateErrorAborts(t *testing.T) {
	s := newAdminServer(t)
	sentinel := fmt.Errorf("不干了")
	err := s.swapConf(func(c *config.Config) error {
		c.Targets["x"] = config.Target{Addr: "127.0.0.1:1", Token: "x"}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("应原样返回 mutate 的错误，实际 %v", err)
	}
	if len(s.conf().Targets) != 0 {
		t.Fatal("mutate 失败后不该换快照")
	}
}
