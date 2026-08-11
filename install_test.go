// 把 install.sh 的 shell 单测接进 go test ./...。
//
// why：仓库里唯一会被例行执行的测试入口是 go test。一个只能手动 bash 的
// 测试文件等于没有测试——它会在第一次改动后悄悄失效。
package main

import (
	"os/exec"
	"testing"
)

func TestInstallScriptUnits(t *testing.T) {
	out, err := exec.Command("bash", "install_test.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh 单测失败:\n%s", out)
	}
}
