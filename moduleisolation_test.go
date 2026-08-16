// 本文件守住一条承重边界：桌面薄壳（desktop/）是嵌套的独立 module，
// 它的 CGO 依赖（gtk/webkit）绝不能进入主模块的构建图。
//
// 为什么值得一条测试：主模块的 CGO_ENABLED=0 全平台交叉编译是发布链的地基
// （.github/workflows/release.yml:167 解释了为什么 macOS 上开 CGO 会让产物
// 带上构建机的最低系统版本约束，且症状要到用户机器上才出现）。
// 一旦有人把 desktop 挪进主模块，`go build ./...` 会在 CI 上以一种
// 很难归因的方式挂掉——不如在这里挂，报文直说是怎么回事。
package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDesktopModuleStaysOutOfParentBuildGraph(t *testing.T) {
	out, err := exec.Command("go", "list", "./...").Output()
	if err != nil {
		t.Fatalf("go list ./... 失败: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "/desktop") {
			t.Fatalf("主模块的构建图里出现了薄壳包 %q——"+
				"desktop/ 必须是嵌套的独立 module（自带 go.mod），"+
				"否则 gtk/webkit 的 CGO 依赖会进入主模块，"+
				"CGO_ENABLED=0 的交叉编译矩阵会挂", line)
		}
	}
}
