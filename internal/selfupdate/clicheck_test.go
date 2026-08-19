// CLI 侧更新提示的测试。
package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLICheckRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &CLICheck{CheckedAt: time.Unix(1760000000, 0).UTC(), Latest: "v0.3.0"}
	if err := SaveCLICheck(dir, want); err != nil {
		t.Fatalf("SaveCLICheck: %v", err)
	}
	got := LoadCLICheck(dir)
	if got == nil || got.Latest != "v0.3.0" || !got.CheckedAt.Equal(want.CheckedAt) {
		t.Fatalf("往返不一致: %+v", got)
	}
}

// 缓存损坏必须静默当成「没有」，**不能报错、不能崩**。
//
// why：这条路径挂在**每一条** handoff 命令上。一个坏掉的缓存文件让所有命令
// 都吐错误，代价远大于少提示一次更新。这里的取舍与 IsManaged 的 fail-closed 相反，
// 因为影响面完全不同。
func TestLoadCLICheckCorruptIsSilent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(CLICheckPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CLICheckPath(dir), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadCLICheck(dir); got != nil {
		t.Fatalf("损坏的缓存应静默返回 nil，得到 %+v", got)
	}
}

func TestLoadCLICheckMissingIsSilent(t *testing.T) {
	if got := LoadCLICheck(t.TempDir()); got != nil {
		t.Fatal("缺文件应返回 nil")
	}
}

// 限流：24h 内不重复检查。
func TestCLICheckStale(t *testing.T) {
	now := time.Unix(1760000000, 0).UTC()
	if !CLICheckStale(nil, now) {
		t.Error("没有缓存时应视为过期")
	}
	fresh := &CLICheck{CheckedAt: now.Add(-23 * time.Hour)}
	if CLICheckStale(fresh, now) {
		t.Error("23h 前查过不该再查")
	}
	old := &CLICheck{CheckedAt: now.Add(-25 * time.Hour)}
	if !CLICheckStale(old, now) {
		t.Error("25h 前查过应重新检查")
	}
}

// 有新版才打提示，且提示里两个版本号都要有。
func TestNotifyLine(t *testing.T) {
	c := &CLICheck{Latest: "v0.3.0"}
	line := NotifyLine(c, "v0.2.0")
	if !strings.Contains(line, "v0.3.0") || !strings.Contains(line, "v0.2.0") {
		t.Fatalf("提示应同时含新旧版本，得到 %q", line)
	}
	if !strings.Contains(line, "handoff upgrade") {
		t.Fatalf("提示应给出下一步命令，得到 %q", line)
	}
}

// 版本相同、缓存为空、本地构建（当前版本为空）三种情况都不打提示。
//
// 本地构建那条尤其重要：开发时每条命令都被提示"有新版"是纯噪音，
// 而且本地构建本来就不该被劝去装 release。
func TestNotifyLineSilentCases(t *testing.T) {
	cases := []struct {
		name    string
		c       *CLICheck
		current string
	}{
		{"版本相同", &CLICheck{Latest: "v0.3.0"}, "v0.3.0"},
		{"没有缓存", nil, "v0.2.0"},
		{"缓存里没版本", &CLICheck{}, "v0.2.0"},
		{"本地构建", &CLICheck{Latest: "v0.3.0"}, ""},
		// 缓存比当前版本旧：升级之后缓存最长会陈 24h，此时绝不能反过来劝人降级。
		// 这不是假设：v0.1.1 发布当天，装完的机器被劝了「有新版本 v0.1.0（当前 v0.1.1）」
		{"缓存比当前旧", &CLICheck{Latest: "v0.1.0"}, "v0.1.1"},
		{"缓存旧一个次版本", &CLICheck{Latest: "v0.9.0"}, "v0.10.0"},
		{"缓存旧一个主版本", &CLICheck{Latest: "v1.0.0"}, "v2.0.0"},
		// 解析不了方向就不提示：一条方向错误的提示（劝人降级）比少提示一次更糟，
		// 而 release 工作流只产出 vX.Y.Z，解析不了本就在正常路径之外
		{"当前版本无法解析", &CLICheck{Latest: "v0.3.0"}, "dev-abc"},
		{"缓存版本无法解析", &CLICheck{Latest: "nightly"}, "v0.3.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if line := NotifyLine(tc.c, tc.current); line != "" {
				t.Fatalf("不该提示，却得到 %q", line)
			}
		})
	}
}

// 版本号按数值而非字典序比较：v0.10.0 比 v0.9.0 新，字典序会判反。
func TestNotifyLineNumericOrder(t *testing.T) {
	c := &CLICheck{Latest: "v0.10.0"}
	if line := NotifyLine(c, "v0.9.0"); line == "" {
		t.Fatal("v0.10.0 比 v0.9.0 新，应当提示")
	}
}

// TestCompareVersionIsTheOnlyExportedComparator 钉住导出入口的存在与语义。
//
// 为什么要有这条：本函数历史上被写错过（B59 验收当场抓出反向提示——装了
// v0.1.1 的机器被劝「有新版本 v0.1.0」，根因是没按三段整数比）。它现在有
// 三个消费者（CLI 提示、桌面同步、桌面通知），错一次的代价乘以三。
func TestCompareVersionIsTheOnlyExportedComparator(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.1.0", "v0.1.1", -1, true},
		{"v0.1.1", "v0.1.0", 1, true},
		{"v0.1.0", "v0.1.0", 0, true},
		// 字典序会把 v0.10.0 判成比 v0.9.0 旧——这条是本函数存在的理由
		{"v0.10.0", "v0.9.0", 1, true},
		{"v0.9.0", "v0.10.0", -1, true},
		// 前缀 v 可有可无
		{"0.2.0", "v0.1.0", 1, true},
		// 形态不符一律 ok=false
		{"v0.1", "v0.1.0", 0, false},
		{"", "v0.1.0", 0, false},
		{"v0.1.0", "rc10", 0, false},
		{"v0.1.-1", "v0.1.0", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersion(c.a, c.b)
		if ok != c.ok {
			t.Errorf("CompareVersion(%q,%q) ok = %v，想要 %v", c.a, c.b, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("CompareVersion(%q,%q) = %d，想要 %d", c.a, c.b, got, c.want)
		}
	}
}
