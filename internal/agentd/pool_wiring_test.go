// 本文件锁死「Server 恒持有一个可用的 target 客户端池」。
//
// why 值得单测：NewServer 有约 50 个调用点，池若靠外部注入，漏注入的那些路径
// 会在运行时空指针崩溃——而它们大多是测试路径，生产上要等到第一次扇出才炸。
package agentd

import (
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/store"
)

func newPoolWiringServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewServer(cfg, st, testLogger(t))
}

// TestServerAlwaysHasPool：NewServer 出来就带池，无需任何注入。
func TestServerAlwaysHasPool(t *testing.T) {
	s := newPoolWiringServer(t, &config.Config{Listen: "127.0.0.1:0"})
	if s.Pool() == nil {
		t.Fatal("NewServer 必须自带 target 客户端池")
	}
	defer s.CloseTargets()
}

// TestServerPoolFollowsLiveConfig：池读的是活快照，不是构造时的那份。
func TestServerPoolFollowsLiveConfig(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:0", Targets: map[string]config.Target{}}
	s := newPoolWiringServer(t, cfg)
	defer s.CloseTargets()

	next := *cfg
	next.Targets = map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "t"}}
	s.cfg.Store(&next)

	names := s.Pool().Names()
	if len(names) != 1 || names[0] != "mac-02" {
		t.Fatalf("池要跟随活快照，实得 %v", names)
	}
}
