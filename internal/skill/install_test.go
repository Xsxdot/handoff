package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallSkipsMissingAgentDirs 锁住「目录不存在就跳过，不代为创建」。
//
// why：给没装 codex 的机器造一个 ~/.codex 目录，下次那台机器上真的装了
// codex 时会拿到一个我们凭空造的半截目录结构。不给没装的 agent 造目录是
// skills/install.sh 一直以来的行为，这次搬进二进制不许悄悄改掉。
func TestInstallSkipsMissingAgentDirs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	sites, err := Install("内容", home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatal("不该给没装的 agent 造目录")
	}
	var installed, skipped int
	for _, s := range sites {
		switch s.State {
		case StateInstalled:
			installed++
		case StateSkipped:
			skipped++
			if s.Note == "" {
				t.Fatalf("跳过必须给理由: %+v", s)
			}
		}
	}
	if installed != 2 { // 基准副本 + .claude
		t.Fatalf("已装落点 = %d，期望 2", installed)
	}
	if skipped != 3 {
		t.Fatalf("跳过落点 = %d，期望 3", skipped)
	}
}

// TestInstallIsIdempotent：重复执行等于「把当前版本重新同步过去」。
// 升级路径每次都会调它，不幂等就会在第二次升级时炸。
func TestInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".config/opencode", ".grok"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	if _, err := Install("v1", home); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("v2", home); err != nil {
		t.Fatalf("第二次安装失败: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".handoff", "skill", "SKILL.md"))
	if string(b) != "v2" {
		t.Fatalf("基准副本没被同步成新内容，实得 %q", b)
	}
}

// TestInstallLinksPointAtBase 锁住软链拓扑：四家都指向同一份基准副本，
// 改一次基准四家同时生效。指错了的症状是「升级后有的 agent 是新的有的是旧的」。
func TestInstallLinksPointAtBase(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".grok"), 0o755)
	if _, err := Install("x", home); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(home, ".grok", "skills", "handoff"))
	if err != nil {
		t.Fatal(err)
	}
	if got != BasePath(home) {
		t.Fatalf("软链指向 %q，期望 %q", got, BasePath(home))
	}
}

// TestInstallReplacesRealDirectory：目标可能是上一次装的软链，也可能是
// 手工放的实体目录（skills/install.sh 的 rm -rf 就是为了这个）。
func TestInstallReplacesRealDirectory(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".grok", "skills", "handoff"), 0o755)
	os.WriteFile(filepath.Join(home, ".grok", "skills", "handoff", "SKILL.md"), []byte("手工放的"), 0o644)
	if _, err := Install("x", home); err != nil {
		t.Fatalf("目标是实体目录时必须能覆盖: %v", err)
	}
}
