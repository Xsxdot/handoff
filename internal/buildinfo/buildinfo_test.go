// buildinfo 包测试：VCS 戳解析，以及「测试二进制没有 vcs 戳」这一必须支持的形态。
package buildinfo

import (
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
