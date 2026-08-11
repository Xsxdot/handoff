// release workflow 的约定测试。
//
// 为什么值得单测一个 CI 配置：资产命名与注入路径是「一处约定、多处消费」——
// workflow 产出、install.sh 消费、B54.3 的自更新是第三处。改错任何一边都不会
// 在编译期暴露，只会在真机上表现为「404 找不到资产」或「装上的二进制自称 unknown」。
// 这个测试让漂移在 go test 阶段就翻红。
package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// readWorkflow 读 workflow 原文，并顺带验证它是合法 YAML。
func readWorkflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("读 workflow 失败: %v", err)
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("workflow 不是合法 YAML: %v", err)
	}
	return string(b)
}

// ldflags 的 -X 路径必须是 module path，写成 GitHub owner 会静默失效
// （构建成功、二进制自称 unknown、自动更新永远认为自己已是最新）。
func TestWorkflowInjectsVersionAtModulePath(t *testing.T) {
	const want = "-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion="
	if !strings.Contains(readWorkflow(t), want) {
		t.Fatalf("workflow 缺少注入路径 %q", want)
	}
}

// 资产命名是与 install.sh 的契约，模式变了两边必须一起变。
func TestWorkflowUsesAgreedAssetNaming(t *testing.T) {
	wf := readWorkflow(t)
	for _, want := range []string{
		`handoff_${TAG}_${GOOS}_${GOARCH}.tar.gz`,
		"checksums.txt",
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("workflow 缺少约定 %q", want)
		}
	}
}

// 平台矩阵必须正好是这四项：少一项等于某个平台装不上，
// 多一项（尤其 windows）等于发一个 agentd 根本跑不起来的二进制（backlog B37）。
func TestWorkflowCoversExactlyFourPlatforms(t *testing.T) {
	wf := readWorkflow(t)
	for _, pair := range []string{
		"goos: darwin\n            goarch: arm64",
		"goos: darwin\n            goarch: amd64",
		"goos: linux\n            goarch: amd64",
		"goos: linux\n            goarch: arm64",
	} {
		if !strings.Contains(wf, pair) {
			t.Fatalf("矩阵缺少组合:\n%s", pair)
		}
	}
	if strings.Contains(wf, "windows") {
		t.Fatal("不得发布 windows 资产：prochost 的 Windows 实现尚未完成（backlog B37），装了也跑不起来")
	}
}
