// 守住一条铁律：**测试进程里绝不允许拉起后台更新检查子进程**。
//
// 这不是洁癖，是 2026-08-13 那场事故的护栏。maybeNotifyUpdate 挂在每条命令的
// PersistentPostRun 上，它 spawn 的是 os.Executable()——在 go test 下那不是
// handoff 而是 <包>.test，而 go test 会忽略 `update-check` 这类位置参数，于是
// 子进程把整套测试从头再跑一遍，跑的过程中又走到同一段代码再 spawn。
//
// 实测后果：CI 的测试步 85 秒被 SIGTERM（退出码 143，一度被当成「测试很慢」
// 查了半天）；一台 4c8g 机器上 1011 个 cmd.test、load average 500+，ssh 都连
// 不上，只能强制重启。
//
// 边界：本文件只管「有没有 spawn」这一件事，不测更新检查本身的逻辑
// （那是 internal/selfupdate 的单测）。
package cmd

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestNoBackgroundSpawnUnderTest 断言：在缓存缺失（判定陈旧）、配置可加载的
// 条件下——也就是**最容易 spawn 的那个现场**——测试进程里一次都不该调到
// startBackgroundCheck。
//
// 为什么要刻意把条件凑成「最容易 spawn」：干净环境才会走到 spawn，开发机因为
// ~/.handoff 里有新鲜时间戳而恒早返回。用例若不自己造出干净现场，就会退化成
// 一条在任何实现下都通过的假绿——那正是这个 bug 能活到进 CI 的原因。
func TestNoBackgroundSpawnUnderTest(t *testing.T) {
	// datadir 指向空的临时目录：LoadCLICheck 读不到缓存 → CLICheckStale 为真
	dataDir := t.TempDir()
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:7777\"\ntoken: \"tok\"\ndatadir: \""+
		filepath.ToSlash(dataDir)+"\"\n")

	resetFlags(t)
	configPath = cfgPath

	spawned := 0
	orig := startBackgroundCheck
	t.Cleanup(func() { startBackgroundCheck = orig })
	startBackgroundCheck = func(string) { spawned++ }

	// 名字既不是 update-check 也不是 upgrade，才不会被开头那两个白名单挡掉
	probe := &cobra.Command{Use: "tasks"}
	maybeNotifyUpdate(probe)

	if spawned != 0 {
		t.Fatalf("测试进程里拉起了 %d 次后台更新检查——它 spawn 的是 <包>.test，"+
			"会把整套测试递归重跑，指数级炸进程（CI 退出码 143、机器 load 500+ 就是这么来的）",
			spawned)
	}
}
