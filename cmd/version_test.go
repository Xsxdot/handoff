// handoff version 的 CLI 行为测试。
//
// 首行格式是**对外契约**：B54.3 的自更新会拉起新下载的二进制跑 version，
// 把首行与期望 tag 精确比对。这里的断言就是那份契约的钉子。
package cmd

import (
	"bytes"
	"context"
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
