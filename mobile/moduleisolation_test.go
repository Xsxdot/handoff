// 本文件守住一条承重边界：移动端薄壳 module（mobile/）是嵌套的独立 module，
// 它的 Go 连接核经 gomobile 编为 AAR/XCFramework，绝不能进入主模块构建图。
//
// 先例：仓根 moduleisolation_test.go#TestDesktopModuleStaysOutOfParentBuildGraph
// 守 desktop/（用 runtime.Caller 定位自己，不依赖 go test 的 cwd）；
// 本文件守 mobile/，两条边界同款不同物。
package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mobileRoot 返回 mobile/ 模块根（本测试文件所在目录），不依赖 go test 的 cwd。
func mobileRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	return filepath.Dir(thisFile)
}

// TestMobileModuleStaysOutOfParentBuildGraph：主模块 `go list ./...` 不得含
// mobile/ 模块的包；同时 mobile/ 模块自己必须能列出 bind 包。
func TestMobileModuleStaysOutOfParentBuildGraph(t *testing.T) {
	root := filepath.Dir(mobileRoot(t))
	mobile := mobileRoot(t)

	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("在仓根 go list ./... 失败: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "github.com/Xsxdot/handoff/mobile" ||
			strings.HasPrefix(line, "github.com/Xsxdot/handoff/mobile/") {
			t.Fatalf("主模块的构建图里出现了移动端包 %q——"+
				"mobile/ 必须是嵌套的独立 module（自带 go.mod + replace ../），"+
				"否则 gomobile 绑定面会被卷进根模块构建图", line)
		}
	}

	cmd = exec.Command("go", "list", "./...")
	cmd.Dir = mobile
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("在 mobile/ go list ./... 失败: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "github.com/Xsxdot/handoff/mobile/bind") {
		t.Fatalf("mobile/ 模块未列出 bind 包，嵌套 module 形状漂移: %q", out)
	}
}
