// swapconf_discipline_test.go —— swapConf 对 Discipline 段的写时复制回归。
//
// 为什么单独一个文件：这条性质是「配置读者拿到的快照恒定」的一部分，
// 漏了不会有任何测试变红，但会让并发读者看到改到一半的映射。
//
// 用白盒包（package agentd）：本测试要直接调 swapConf，不值得为它开一个导出面。
package agentd

import (
	"path/filepath"
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

// TestSwapConfDeepCopiesEnv 钉住写时复制：改 Env 不得污染改之前取到的旧快照。
//
// 为什么这条要单独测：swapConf 用的是结构体浅拷 + 逐字段深拷，新增运行期
// 可变的 map 字段时**极容易漏掉一层**，而漏掉的症状是「并发读到半改状态」
// ——不会当场报错，只会在别处诡异。
func TestSwapConfDeepCopiesEnv(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
		Env: map[string]string{"opencode": "old.env"},
	}, discardLogger())
	path := filepath.Join(t.TempDir(), "config.yaml")
	env.srv.SetConfigPath(path)

	before := env.srv.conf().Env // 改动前取到的快照
	if err := env.srv.swapConf(func(c *config.Config) error {
		c.Env["opencode"] = "new.env"
		return nil
	}); err != nil {
		t.Fatalf("swapConf: %v", err)
	}
	if before["opencode"] != "old.env" || len(before) != 1 {
		t.Fatalf("旧快照被污染：%v", before)
	}
	if got := env.srv.EnvMapping(); got["opencode"] != "new.env" || len(got) != 1 {
		t.Fatalf("新快照 = %v，想要 {opencode: new.env}", got)
	}
}
