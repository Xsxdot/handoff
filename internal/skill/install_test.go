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
	if skipped != 4 {
		t.Fatalf("跳过落点 = %d，期望 4", skipped)
	}
}

// TestInstallIsIdempotent：重复执行等于「把当前版本重新同步过去」。
// 升级路径每次都会调它，不幂等就会在第二次升级时炸。
func TestInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".config/opencode", ".grok", ".gemini/antigravity-cli"} {
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

// TestInstallWritesRealCopies 锁住落点形态：五家各自一份实体副本，内容与
// 基准一致。软链已废弃——它在 Windows 上需要管理员特权，而它买的
// 「改一处生效四处」在 go:embed + 每次全量重写的模型里收益为零（B84）。
func TestInstallWritesRealCopies(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".config/opencode", ".grok", ".gemini/antigravity-cli"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Install("内容 v1", home); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".claude/skills", ".codex/skills", ".config/opencode/skills", ".grok/skills", ".gemini/antigravity-cli/skills",
	} {
		p := filepath.Join(home, rel, "handoff", "SKILL.md")
		fi, err := os.Lstat(filepath.Join(home, rel, "handoff"))
		if err != nil {
			t.Fatalf("落点 %s 不存在: %v", rel, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("落点 %s 仍是软链，应为实体目录", rel)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读落点副本 %s: %v", p, err)
		}
		if string(b) != "内容 v1" {
			t.Errorf("落点 %s 内容 = %q，期望 %q", rel, b, "内容 v1")
		}
	}
}

// TestInstallMigratesLegacySymlink：老装机的落点是指向基准副本的软链，
// 必须能被就地换成实体副本，**且基准副本还在**。
//
// why 必须钉死后半句：RemoveAll 对软链是摘链不删目标——这是本次改动唯一
// 会咬人的语义。万一哪天改成了先解析再删，基准副本会被连带删掉，而症状
// 是「装完之后 handoff status 说四个落点都在、基准却没了」。
func TestInstallMigratesLegacySymlink(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".grok", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 先手工造一个老形态：基准副本 + 指向它的软链
	base := BasePath(home)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".grok", "skills", "handoff")
	if err := os.Symlink(base, link); err != nil {
		t.Skipf("本平台建不了软链，迁移用例无从构造: %v", err)
	}

	if _, err := Install("新内容", home); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("落点应存在: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("老软链应被换成实体目录")
	}
	b, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil || string(b) != "新内容" {
		t.Fatalf("落点副本 = %q (err=%v)，期望 %q", b, err, "新内容")
	}
	// 基准副本必须还在，且已被同步成新内容
	bb, err := os.ReadFile(filepath.Join(base, "SKILL.md"))
	if err != nil {
		t.Fatalf("基准副本被误删了: %v", err)
	}
	if string(bb) != "新内容" {
		t.Errorf("基准副本 = %q，期望 %q", bb, "新内容")
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
