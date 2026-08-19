// handoff version 的 CLI 行为测试。
//
// 首行格式是**对外契约**：B54.3 的自更新会拉起新下载的二进制跑 version，
// 把首行与期望 tag 精确比对。这里的断言就是那份契约的钉子。
package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runVersion 执行一次 version 命令，返回 stdout+stderr 合并输出。
func runVersion(t *testing.T) string {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"version"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version 不应报错: %v", err)
	}
	return buf.String()
}

// 测试二进制没有注入 releaseVersion，首行必须是字面量 unknown。
//
// 为什么不能是空行：空行与「命令根本没输出」无法区分，自检侧会把两者都
// 当成失败，从而丢掉「二进制能跑，只是不是 release 构建」这个有用结论。
func TestVersionFirstLineIsUnknownWhenNotRelease(t *testing.T) {
	first := strings.SplitN(runVersion(t), "\n", 2)[0]
	if first != versionUnknown {
		t.Fatalf("非 release 构建的首行应为 %q，得到 %q", versionUnknown, first)
	}
}

// 首行之后必须有排障细节，否则孤零零一行 unknown 什么问题也定位不了。
func TestVersionPrintsDetailLines(t *testing.T) {
	out := runVersion(t)
	for _, want := range []string{"revision", "go", "platform"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q 行:\n%s", want, out)
		}
	}
}

// version 必须在没有配置文件时也能跑通。
//
// why：B54.3 的自检发生在「新二进制刚下载完、还没被启用」的时刻，而
// install.sh 装完的机器可能连 ~/.handoff/config.yaml 都还没有。version
// 一旦依赖配置，自检就会在最需要它的场景下失败。
func TestVersionNeedsNoConfig(t *testing.T) {
	resetFlags(t)
	configPath = "/nonexistent/handoff/config.yaml"
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"version", "--config", "/nonexistent/handoff/config.yaml"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version 不应读配置，却报错: %v", err)
	}
	if first := strings.SplitN(buf.String(), "\n", 2)[0]; first != versionUnknown {
		t.Fatalf("首行=%q", first)
	}
}

// version 在空 HOME 下跑完，不得在磁盘留下任何东西——这是 file 头注释那条
// 「不读配置文件、必须在没有 config.yaml 的机器上跑通」契约的完整钉子。
//
// 坑：config.Load 在文件不存在时会 firstRun 落盘（生成随机 token），而根命令
// 的 PersistentPostRun → maybeNotifyUpdate 曾因照常 Load 而把这个副作用带进
// version（以及桌面壳探测、脚本、CI 等一切调 version 的调用方）——一台装了
// CLI 但没配过的机器，任何人跑一次 version 就在真实 ~/.handoff 留下一份
// 从未配过对的 config.yaml，此后 shell.Resolve 判「已配置」，图形向导永不
// 再现。本用例用隔离的临时 HOME 钉死「跑完不留 .handoff」。
func TestVersionCreatesNothingInEmptyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// datadir 落到临时 HOME 下 .handoff；显式 --config 指向它，绕过测试进程
	// 继承的真实 ~/.handoff
	cfgPath := filepath.Join(home, ".handoff", "config.yaml")

	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"version", "--config", cfgPath})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version 不应报错: %v", err)
	}
	if first := strings.SplitN(buf.String(), "\n", 2)[0]; first != versionUnknown {
		t.Fatalf("首行=%q", first)
	}
	// 契约：跑完后整个 .handoff 都不存在（不仅 config.yaml，还有 update/ 等）
	if _, err := os.Stat(filepath.Dir(cfgPath)); !os.IsNotExist(err) {
		t.Fatalf("空 HOME 下跑 version 后不应生成 %s（got %v）——根命令钩子把 firstRun 写盘带进了 version",
			filepath.Dir(cfgPath), err)
	}
}
