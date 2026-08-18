// buildinfo 包测试：VCS 戳解析，以及「测试二进制没有 vcs 戳」这一必须支持的形态。
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"testing"
)

// 有 vcs 戳时四个字段都要解析出来，含 modified 的字符串转布尔。
func TestReadParsesVCSSettings(t *testing.T) {
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "8353ef68d711eaf63eeb1287f342f3238204aec8"},
				{Key: "vcs.time", Value: "2026-08-10T01:45:37Z"},
				{Key: "vcs.modified", Value: "true"},
				{Key: "GOARCH", Value: "arm64"},
			},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = old })

	got, ok := Read()
	if !ok {
		t.Fatal("Read 应返回 ok=true")
	}
	if got.Revision != "8353ef68d711eaf63eeb1287f342f3238204aec8" {
		t.Fatalf("Revision=%q", got.Revision)
	}
	if got.Time != "2026-08-10T01:45:37Z" {
		t.Fatalf("Time=%q", got.Time)
	}
	if !got.Modified {
		t.Fatal("vcs.modified=true 必须解析为 Modified=true——它意味着这个二进制对不上任何一个提交")
	}
	if got.Go != "go1.26.1" {
		t.Fatalf("Go=%q", got.Go)
	}
}

// go test 编出的测试二进制就是这种形态：有 GoVersion，没有任何 vcs.* 设置。
// Revision 必须是空串（调用方据此显示「版本未知」），不得 panic、不得报错。
func TestReadWithoutVCSStamp(t *testing.T) {
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings:  []debug.BuildSetting{{Key: "GOARCH", Value: "arm64"}},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = old })

	got, ok := Read()
	if !ok {
		t.Fatal("Read 应返回 ok=true")
	}
	if got.Revision != "" {
		t.Fatalf("非 go build 产物的 Revision 应为空，得到 %q", got.Revision)
	}
	if got.Go != "go1.26.1" {
		t.Fatalf("Go=%q", got.Go)
	}
}

// 读不到构建信息时返回 ok=false，调用方据此降级。
func TestReadUnavailable(t *testing.T) {
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	t.Cleanup(func() { readBuildInfo = old })

	if _, ok := Read(); ok {
		t.Fatal("读不到构建信息时应返回 ok=false")
	}
}

// 注入了 releaseVersion 时，Read 必须把它带进 Version。
//
// 这是 release 构建的形态：ldflags 写入包级变量，vcs 戳同时也在。
func TestReadCarriesInjectedReleaseVersion(t *testing.T) {
	oldVer := releaseVersion
	releaseVersion = "v0.1.0"
	t.Cleanup(func() { releaseVersion = oldVer })

	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "8353ef68d711eaf63eeb1287f342f3238204aec8"},
			},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, ok := Read()
	if !ok {
		t.Fatal("Read 应返回 ok=true")
	}
	if got.Version != "v0.1.0" {
		t.Fatalf("Version=%q，期望注入的 v0.1.0", got.Version)
	}
	if got.Revision != "8353ef68d711eaf63eeb1287f342f3238204aec8" {
		t.Fatalf("注入版本号不得影响 Revision 解析，得到 %q", got.Revision)
	}
}

// 未注入时 Version 必须是空串——这是本地 go build / go run / 测试二进制的
// 真实形态，调用方据此判定「非 release 构建」并退回 revision 展示。
func TestReadWithoutInjectionHasEmptyVersion(t *testing.T) {
	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{GoVersion: "go1.26.1"}, true
	}
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, _ := Read()
	if got.Version != "" {
		t.Fatalf("非 release 构建的 Version 必须为空，得到 %q", got.Version)
	}
}

// go install 装出来的二进制没有 ldflags 注入，但模块版本在 Main.Version 里，
// 必须回落到它。
//
// why：模块路径修好之后 `go install github.com/Xsxdot/handoff@v0.2.3` 成了
// 一条对外承诺的安装路径，而这条路径永远不会经过 release.yml 的 -X 注入。
// 不回落的话，这样装的用户版本恒为 unknown，`upgrade --check` 会一直劝他
// 「需要升级」——升完还是 unknown，还是劝，成了一个没有出口的循环。
// 注意这条形态里 vcs 戳是没有的：模块代理发的是 zip，不带 git 信息。
func TestReadFallsBackToModuleVersion(t *testing.T) {
	oldVer := releaseVersion
	releaseVersion = ""
	t.Cleanup(func() { releaseVersion = oldVer })

	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Main:      debug.Module{Path: "github.com/Xsxdot/handoff", Version: "v0.2.3"},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, _ := Read()
	if got.Version != "v0.2.3" {
		t.Fatalf("go install 形态的 Version=%q，期望回落到模块版本 v0.2.3", got.Version)
	}
}

// ldflags 注入优先于模块版本。
//
// why：两者同时存在时（release 二进制在模块内构建），注入值才是这次发布的
// 真身。反过来取会让打了 tag 的构建自称成别的版本。
func TestInjectedVersionWinsOverModuleVersion(t *testing.T) {
	oldVer := releaseVersion
	releaseVersion = "v0.9.9"
	t.Cleanup(func() { releaseVersion = oldVer })

	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Main:      debug.Module{Version: "v0.2.3"},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, _ := Read()
	if got.Version != "v0.9.9" {
		t.Fatalf("Version=%q，注入值必须压过模块版本", got.Version)
	}
}

// 只有 vX.Y.Z 形态才算 release 版本号，其余一律退回空串。
//
// why（这条是回落的安全边界，删了会造成两处真实回归）：
//   - 仓库内 go build 时 Main.Version 是 "(devel)"。把它当版本号，status 就会
//     拿 "(devel)" 顶掉 revision 展示——而本地构建恰恰只有 revision 有排障价值。
//   - `go install ...@main` 得到的是 v0.0.0-2026…-<sha> 伪版本。它在语义上
//     恒小于任何真实 tag，认了它就等于把开发版说成「比 v0.2.3 旧的 release」，
//     升级巡检会据此劝人「降级到最新」。
func TestModuleVersionFallbackRejectsNonRelease(t *testing.T) {
	for _, mv := range []string{
		"(devel)",
		"v0.0.0-20260813120000-8353ef68d711",
		"",
		"devel",
		"0.2.3",
	} {
		t.Run(mv, func(t *testing.T) {
			oldVer := releaseVersion
			releaseVersion = ""
			t.Cleanup(func() { releaseVersion = oldVer })

			oldRead := readBuildInfo
			readBuildInfo = func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{
					GoVersion: "go1.26.1",
					Main:      debug.Module{Version: mv},
				}, true
			}
			t.Cleanup(func() { readBuildInfo = oldRead })

			got, _ := Read()
			if got.Version != "" {
				t.Fatalf("Main.Version=%q 不是 release 形态，Version 必须为空，得到 %q", mv, got.Version)
			}
		})
	}
}

// 读不到构建信息（ok=false）时，注入的版本号仍须返回。
//
// why：releaseVersion 是编译期常量，与 debug.ReadBuildInfo 能不能读到无关。
// 丢掉它会让一个 release 二进制在这种边角情况下自称 unknown，而 B54.3 的
// 自检正是拿这个值比对的——自检会误判失败并放弃一次本该成功的更新。
func TestReadKeepsVersionWhenBuildInfoUnavailable(t *testing.T) {
	oldVer := releaseVersion
	releaseVersion = "v0.2.0"
	t.Cleanup(func() { releaseVersion = oldVer })

	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, ok := Read()
	if ok {
		t.Fatal("读不到构建信息时 ok 仍应为 false")
	}
	if got.Version != "v0.2.0" {
		t.Fatalf("ok=false 时也必须带回注入的版本号，得到 %q", got.Version)
	}
}

// TestReadFillsPlatform 锁住「两条返回路径都填 Platform」。
//
// why：Read 有一条降级分支（读不到 debug.BuildInfo 时只返回 Version），
// 只在主路径填就会让「非 go build 产物」的 agentd 报空平台，而空平台
// 在远程升级里的语义是「对端过旧，拒绝升级」——一个填漏导致的假拒绝。
func TestReadFillsPlatform(t *testing.T) {
	bi, _ := Read()
	want := runtime.GOOS + "/" + runtime.GOARCH
	if bi.Platform != want {
		t.Fatalf("Platform = %q，期望 %q", bi.Platform, want)
	}
}

// 注入的 revision 必须**覆盖**自动戳，而不是只在自动戳为空时兜底。
//
// 为什么这条是承重的：linked git worktree 里 go build 的自动戳非空但**指向
// 主工作树**——同一份源码，worktree 构建戳出主工作树的 HEAD 与脏状态，独立
// 克隆构建才戳出真实值（B146 实测）。兜底语义（空才用注入值）在这里恒不生效，
// 所以优先级写反了等于没修。
func TestReadInjectedRevisionOverridesAutoStamp(t *testing.T) {
	oldRead, oldRev, oldMod := readBuildInfo, releaseRevision, releaseModified
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings: []debug.BuildSetting{
				// 自动戳：主工作树的状态，非空且是脏的
				{Key: "vcs.revision", Value: "c32a1f8b19980fe8ae7b150ca7135aa5f030a8d1"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, true
	}
	releaseRevision = "85c1e2322a086e237f63261f7b9ea3e05f2733e4"
	releaseModified = "false"
	t.Cleanup(func() { readBuildInfo, releaseRevision, releaseModified = oldRead, oldRev, oldMod })

	got, ok := Read()
	if !ok {
		t.Fatal("Read 应返回 ok=true")
	}
	if got.Revision != "85c1e2322a086e237f63261f7b9ea3e05f2733e4" {
		t.Fatalf("注入值必须覆盖自动戳，实得 Revision=%q", got.Revision)
	}
	if got.Modified {
		t.Fatal("注入 releaseModified=false 时不得沿用自动戳的 modified=true——那正是凭空多出来的「带未提交改动」")
	}
}

// 不注入时行为必须与改动前逐字节一致：自动戳原样透出。
func TestReadWithoutInjectionKeepsAutoStamp(t *testing.T) {
	oldRead, oldRev, oldMod := readBuildInfo, releaseRevision, releaseModified
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "c32a1f8b19980fe8ae7b150ca7135aa5f030a8d1"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, true
	}
	releaseRevision, releaseModified = "", ""
	t.Cleanup(func() { readBuildInfo, releaseRevision, releaseModified = oldRead, oldRev, oldMod })

	got, _ := Read()
	if got.Revision != "c32a1f8b19980fe8ae7b150ca7135aa5f030a8d1" || !got.Modified {
		t.Fatalf("未注入时应原样透出自动戳，实得 Revision=%q Modified=%v", got.Revision, got.Modified)
	}
}
