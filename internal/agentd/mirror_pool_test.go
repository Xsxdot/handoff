// 本文件锁死 Mirror 跟随活配置：控制台运行期新增的机器无需重启即被镜像。
//
// why：Mirror 过去拿的是 NewMirror 时的静态 cfg，加一台机器要重启 agentd 才
// 会被发现——而「加完看不见」很容易被误当成对端故障去查。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestMirrorSeesTargetsAddedAtRuntime：运行期新增的 target 立刻进入枚举。
func TestMirrorSeesTargetsAddedAtRuntime(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:0", Targets: map[string]config.Target{}}
	s := newPoolWiringServer(t, cfg)
	defer s.CloseTargets()

	m := NewMirror(s.Pool(), nil, NewHub(), testLogger(t))
	if got := len(m.machineNames()); got != 0 {
		t.Fatalf("初始应为 0 台，实得 %d", got)
	}

	next := *cfg
	next.Targets = map[string]config.Target{"linux-01": {Addr: "10.0.0.3:7777", Token: "t"}}
	s.cfg.Store(&next)

	names := m.machineNames()
	if len(names) != 1 || names[0] != "linux-01" {
		t.Fatalf("运行期新增的机器要立刻可见，实得 %v", names)
	}
}
