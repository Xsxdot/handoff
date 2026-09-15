package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallPluginWritesWhenOpenCodePresent 锁住：本机有 OpenCode 配置目录时，
// 插件落到 ~/.config/opencode/plugins/handoff-monitor.ts，且不改任何 json。
func TestInstallPluginWritesWhenOpenCodePresent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	site := InstallPlugin("plugin-v1", home)
	if site.State != StateInstalled {
		t.Fatalf("state=%s note=%s path=%s", site.State, site.Note, site.Path)
	}
	want := filepath.Join(home, PluginRelFile)
	if site.Path != want {
		t.Fatalf("path=%s want=%s", site.Path, want)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "plugin-v1" {
		t.Fatalf("content=%q", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Fatal("不得改/造 opencode.json")
	}
}

// TestInstallPluginSkipsWhenOpenCodeMissing 锁住：没装 OpenCode 不代造目录。
func TestInstallPluginSkipsWhenOpenCodeMissing(t *testing.T) {
	home := t.TempDir()
	site := InstallPlugin("plugin-v1", home)
	if site.State != StateSkipped {
		t.Fatalf("state=%s，期望 skipped", site.State)
	}
	if site.Note == "" {
		t.Fatal("跳过必须给理由")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode")); !os.IsNotExist(err) {
		t.Fatal("不该给没装的 OpenCode 造 .config/opencode")
	}
}

// TestInstallPluginEmptyIsSkipped 空内嵌不得写出空文件。
func TestInstallPluginEmptyIsSkipped(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	site := InstallPlugin("", home)
	if site.State != StateSkipped {
		t.Fatalf("state=%s，空内容应跳过", site.State)
	}
	if _, err := os.Stat(filepath.Join(home, PluginRelFile)); !os.IsNotExist(err) {
		t.Fatal("空内容不得写出插件文件")
	}
}

// TestInstallPluginIdempotent 升级路径每次全量重写。
func TestInstallPluginIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s := InstallPlugin("v1", home); s.State != StateInstalled {
		t.Fatal(s.Note)
	}
	if s := InstallPlugin("v2", home); s.State != StateInstalled {
		t.Fatal(s.Note)
	}
	b, _ := os.ReadFile(filepath.Join(home, PluginRelFile))
	if string(b) != "v2" {
		t.Fatalf("got %q", b)
	}
}

// TestPluginStatusWhenOpenCodeMissing 没装 OpenCode 时只报 missing，不把插件落点算成 stale。
func TestPluginStatusWhenOpenCodeMissing(t *testing.T) {
	home := t.TempDir()
	got := PluginStatus("plugin-v1", home)
	if got.State != StateMissing {
		t.Fatalf("state=%s note=%s", got.State, got.Note)
	}
	if got.Note == "" {
		t.Fatal("缺 OpenCode 时必须给理由")
	}
	if !InSync([]Site{got}) {
		t.Fatal("missing 不算不一致")
	}
}

// TestPluginStatusHashesAgainstPluginContent skill 与插件是两份内嵌，不能用 SKILL.md 去比插件文件。
func TestPluginStatusHashesAgainstPluginContent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	InstallPlugin("plugin-v1", home)
	got := PluginStatus("plugin-v1", home)
	if got.State != StateInSync {
		t.Fatalf("state=%s note=%s", got.State, got.Note)
	}
	got = PluginStatus("plugin-v2", home)
	if got.State != StateStale {
		t.Fatalf("内容变了应是 stale，got %s", got.State)
	}
}

// TestSkillTextBranchesWaitModes 锁住 skill 正文对 OpenCode monitor / Command Code 的分支，
// 免得安装通道把一份过期纪律同步到各家 agent。
func TestSkillTextBranchesWaitModes(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// 测试在 internal/skill 下跑，仓库根是上两级。
	p := filepath.Join(root, "..", "..", "skills", "handoff", "SKILL.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "Command Code") {
		t.Fatal("skill 必须点名 Command Code 走一次性 wait")
	}
	if !strings.Contains(text, "不带 `--follow`") && !strings.Contains(text, "不带 --follow") {
		t.Fatal("Command Code 必须写明不带 --follow")
	}
	if !strings.Contains(text, "`monitor`") {
		t.Fatal("skill 必须写 OpenCode 的 monitor 工具走 follow")
	}
	if !strings.Contains(text, "每次收到一个事件") {
		t.Fatal("Command Code 必须写明每次收到事件就退出")
	}
}
