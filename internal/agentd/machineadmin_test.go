// 本文件白盒测试 Server 的配置读写：并发读快照、落盘失败回滚、
// 新增/删除开发机的领域逻辑（validateAddMachine / addMachine / removeMachine）。
package agentd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
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
		// StallTimeout 必须有正时长：config.Load 重读时校验要求 >0，
		// TestAddAndRemoveMachine 会从文件重新加载
		StallTimeout: 2 * time.Hour,
		Targets:      map[string]config.Target{},
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

func TestValidateAddMachine(t *testing.T) {
	existing := map[string]config.Target{"dup": {Addr: "1.2.3.4:7777", Token: "t"}}
	cases := []struct {
		name    string
		req     proto.AddMachineReq
		wantErr bool
		isDup   bool
	}{
		{"正常", proto.AddMachineReq{Name: "box", Addr: "10.0.0.1:7777", Token: "t"}, false, false},
		{"名字为空", proto.AddMachineReq{Name: "", Addr: "10.0.0.1:7777", Token: "t"}, true, false},
		{"名字含空格", proto.AddMachineReq{Name: "my box", Addr: "10.0.0.1:7777", Token: "t"}, true, false},
		{"重名", proto.AddMachineReq{Name: "dup", Addr: "10.0.0.1:7777", Token: "t"}, true, true},
		{"地址缺端口", proto.AddMachineReq{Name: "box", Addr: "10.0.0.1", Token: "t"}, true, false},
		{"令牌为空", proto.AddMachineReq{Name: "box", Addr: "10.0.0.1:7777", Token: ""}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateAddMachine(c.req, existing)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v，实际 %v", c.wantErr, err)
			}
			if c.isDup && !errors.Is(err, ErrMachineExists) {
				t.Fatalf("重名应可被 errors.Is(ErrMachineExists) 识别，实际 %v", err)
			}
		})
	}
}

func TestAddAndRemoveMachine(t *testing.T) {
	s := newAdminServer(t)
	req := proto.AddMachineReq{Name: "box", Addr: "10.0.0.1:7777", Token: "secret", User: "me"}
	if err := s.addMachine(req); err != nil {
		t.Fatalf("新增失败: %v", err)
	}
	got, ok := s.conf().Targets["box"]
	if !ok || got.Addr != "10.0.0.1:7777" || got.Token != "secret" || got.User != "me" {
		t.Fatalf("落库内容不对: addr=%q user=%q token_set=%v ok=%v", got.Addr, got.User, got.Token != "", ok)
	}
	// 落盘后重新读文件，必须还在（否则重启即丢）
	reloaded, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatalf("重读配置失败: %v", err)
	}
	if _, ok := reloaded.Targets["box"]; !ok {
		t.Fatal("配置文件里没有新增的机器")
	}
	if err := s.removeMachine("box"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok := s.conf().Targets["box"]; ok {
		t.Fatal("删除后仍在内存里")
	}
	if err := s.removeMachine("box"); !errors.Is(err, ErrMachineNotFound) {
		t.Fatalf("删除不存在的机器应返回 ErrMachineNotFound，实际 %v", err)
	}
}
