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
// 都吐错误，代价远大于少提示一次更新。这里的取舍与 LoadPending 相反，
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if line := NotifyLine(tc.c, tc.current); line != "" {
				t.Fatalf("不该提示，却得到 %q", line)
			}
		})
	}
}
