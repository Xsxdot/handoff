// 本文件锁死 Mirror 跟随活 target 源：控制台运行期新增的机器无需重启即被镜像。
//
// why：Mirror 过去拿的是构造时的静态清单，加一台机器要重启 agentd 才会被
// 发现——而「加完看不见」很容易被误当成对端故障去查。
// B233.19 自 agentd/mirror_pool_test.go 随实现迁入：真池「Names 读活配置」的
// 语义由 targetclient 包自己的测试锁死，此处锁「Mirror 每次都重新询问 source」。
package workspace

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
)

// namesSource 是只回答机器清单的 RemoteTaskSource 替身（For 不会被走到）。
type namesSource struct {
	mu    sync.Mutex
	names []string
}

func (s *namesSource) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.names...)
}

func (s *namesSource) For(string) (RemoteTaskClient, error) {
	return nil, errors.New("namesSource 不提供客户端")
}

func mirrorTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestMirrorSeesTargetsAddedAtRuntime：运行期新增的 target 立刻进入枚举。
func TestMirrorSeesTargetsAddedAtRuntime(t *testing.T) {
	src := &namesSource{}
	m := NewMirror(src, nil, nopEventPublisher{}, nil, mirrorTestLogger(t))
	if got := len(m.machineNames()); got != 0 {
		t.Fatalf("初始应为 0 台，实得 %d", got)
	}

	src.mu.Lock()
	src.names = []string{"linux-01"}
	src.mu.Unlock()

	names := m.machineNames()
	if len(names) != 1 || names[0] != "linux-01" {
		t.Fatalf("运行期新增的机器要立刻可见，实得 %v", names)
	}
}
