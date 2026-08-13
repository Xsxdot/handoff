package rawtap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wiringSite 是一个必须接入旁路的上游字节入口。
type wiringSite struct {
	file   string // 相对仓库根
	anchor string // 该入口处必须存在的调用
}

// TestAllUpstreamEntrypointsAreTapped 守住「四个 adapter 都接了旁路」这件事。
//
// 为什么用源码断言而不是行为测试：四个读循环各自需要一个真实的上游连接
// （SSE / 文件 tail / 两条 WebSocket）才能跑起来，为它们各造一套桩的成本远高于
// 本测试要防的风险——风险是「新增第五个 executor 时忘了接旁路」，而那正是
// 一条 grep 就能守住的事。
func TestAllUpstreamEntrypointsAreTapped(t *testing.T) {
	sites := []wiringSite{
		{"internal/executor/opencode/api.go", "rawTap.Write(sc.Bytes())"},
		{"internal/executor/claudecode/stream.go", "t.rawTap.Write(line)"},
		{"internal/executor/grok/acp.go", "c.rawTap.Write(data)"},
		{"internal/executor/codex/appserver.go", "c.rawTap.Write(data)"},
	}
	root := repoRoot(t)
	for _, s := range sites {
		b, err := os.ReadFile(filepath.Join(root, s.file))
		if err != nil {
			t.Fatalf("%s: %v", s.file, err)
		}
		if !strings.Contains(string(b), s.anchor) {
			t.Errorf("%s 未接入原始字节旁路，缺 %q", s.file, s.anchor)
		}
	}
}

// repoRoot 从当前包目录向上找到含 go.mod 的目录。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("未找到仓库根（go.mod）")
		}
		dir = parent
	}
}
