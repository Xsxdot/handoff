// swapconf_discipline_test.go —— swapConf 对 Discipline 段的写时复制回归。
//
// 为什么单独一个文件：这条性质是「配置读者拿到的快照恒定」的一部分，
// 漏了不会有任何测试变红，但会让并发读者看到改到一半的映射。
//
// 用白盒包（package agentd）：本测试要直接调 swapConf，不值得为它开一个导出面。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func TestSwapConfDeepCopiesDiscipline(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:      testToken,
		Discipline: map[string]string{"codex": "old.md"},
	}, discardLogger())

	before := env.srv.DisciplineMapping()
	if err := env.srv.swapConf(func(c *config.Config) error {
		c.Discipline["codex"] = "new.md"
		return nil
	}); err != nil {
		t.Fatalf("swapConf: %v", err)
	}
	if before["codex"] != "old.md" {
		t.Fatalf("旧快照被就地改动：codex = %q，写时复制失效", before["codex"])
	}
	if got := env.srv.DisciplineMapping()["codex"]; got != "new.md" {
		t.Fatalf("新快照未生效：%q", got)
	}
}
