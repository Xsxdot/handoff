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
