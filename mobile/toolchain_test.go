// 本文件锁 mobile/ 模块的工具链与构建契约：go.mod 的 gobind tool 指令、
// build.sh 的两条硬约束（Android -androidapi 21 / iOS -target=ios）、
// README 的 token 轮换唯一处置。三者都是壳工程能编起来的前提，而本仓
// linux 开发机跑不了真正的 gomobile bind——这些静态契约是机内唯一能锁的。
//
// 边界：不调用 gomobile / NDK / Xcode，不发网络请求；只读本目录下的文本文件。
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mobileDir 返回本测试文件所在目录（mobile/ 模块根），不依赖 go test 的 cwd。
func mobileDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	return filepath.Dir(thisFile)
}

// readMobile 读 mobile/ 下相对路径 p 的文本；读不到即 Fail。
func readMobile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(mobileDir(t), p))
	if err != nil {
		t.Fatalf("读 mobile/%s 失败: %v", p, err)
	}
	return string(b)
}

// TestGoModHasGobindToolDirective 锁 go.mod 的 tool 指令。
// 没有它，新版 gomobile 在 bind 时报 golang.org/x/mobile 不在依赖图内
// （见 golang.org/x/mobile/cmd/gomobile/bind.go#mobileModuleAvailable）。
func TestGoModHasGobindToolDirective(t *testing.T) {
	gomod := readMobile(t, "go.mod")
	if !strings.Contains(gomod, "tool golang.org/x/mobile/cmd/gobind") {
		t.Fatalf("mobile/go.mod 缺 tool golang.org/x/mobile/cmd/gobind 指令"+
			"（gomobile bind 会报 golang.org/x/mobile 不在依赖图内）:\n%s", gomod)
	}
}

// TestBuildScriptCarriesAndroidAPI21：Android 分支必须显式 -androidapi 21
// （NDK 30 移除 API<21，gomobile 缺省 minsdk=16），脚本必须 fail-closed，
// iOS 分支必须 -target=ios；注释须记 NDK 版本（版本不写死就会随环境漂）。
func TestBuildScriptCarriesAndroidAPI21(t *testing.T) {
	sh := readMobile(t, "build.sh")
	for _, want := range []string{"set -euo pipefail", "-androidapi 21", "-target=ios", "NDK"} {
		if !strings.Contains(sh, want) {
			t.Fatalf("mobile/build.sh 缺 %q:\n%s", want, sh)
		}
	}
}

// TestReadmeDocumentsTokenRotation：README 必须写明 token 泄露的唯一处置是
// 轮换节点 token 并重启，且点出 revoke（吊销 cookie 会话）不足以处置 token 泄露。
func TestReadmeDocumentsTokenRotation(t *testing.T) {
	readme := readMobile(t, "README.md")
	for _, want := range []string{"轮换", "重启", "revoke"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("mobile/README.md 缺 %q——轮换文档不完整:\n%s", want, readme)
		}
	}
}

// checklinknameFlag 是 Android 绑定产物必需的链接参数（B438.2）：pion/transport 的
// stdnet 在 Android 上经 wlynxg/anet 枚举网卡，anet 以 //go:linkname 引用
// net.zoneCache，Go 1.23 起链接器默认拒绝。只给 Android 开，别处一律不开。
const checklinknameFlag = "-ldflags=-checklinkname=0"

// shellFunc 截出 build.sh 里一个 shell 函数的函数体（`name() {` 到行首 `}`）。
func shellFunc(t *testing.T, sh, name string) string {
	t.Helper()
	start := strings.Index(sh, name+"() {")
	if start < 0 {
		t.Fatalf("mobile/build.sh 缺函数 %s", name)
	}
	end := strings.Index(sh[start:], "\n}")
	if end < 0 {
		t.Fatalf("mobile/build.sh 函数 %s 未闭合", name)
	}
	return sh[start : start+end]
}

// TestChecklinknameOnlyOnAndroid：Android 分支带该参数（否则 AAR 链接失败），
// iOS 分支与桌面/发布构建都不带（不把 linkname 检查的关闭扩散到不需要的产物）。
func TestChecklinknameOnlyOnAndroid(t *testing.T) {
	sh := readMobile(t, "build.sh")
	if !strings.Contains(shellFunc(t, sh, "build_android"), checklinknameFlag) {
		t.Fatalf("mobile/build.sh 的 build_android 缺 %q（anet linkname，AAR 会链接失败）", checklinknameFlag)
	}
	if strings.Contains(shellFunc(t, sh, "build_ios"), "checklinkname") {
		t.Fatal("mobile/build.sh 的 build_ios 不得关闭 linkname 检查")
	}
	root := filepath.Dir(mobileDir(t))
	for _, p := range []string{"desktop/Taskfile.yml", "scripts/build-release-local.sh", "scripts/build-deploy.sh"} {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", p, err)
		}
		if strings.Contains(string(b), "checklinkname") {
			t.Fatalf("%s 不得关闭 linkname 检查（只 Android 绑定需要）", p)
		}
	}
}

// TestReadmeDocumentsChecklinkname：README 必须写明该参数的原因与 Go 升级风险。
func TestReadmeDocumentsChecklinkname(t *testing.T) {
	readme := readMobile(t, "README.md")
	for _, want := range []string{"checklinkname", "wlynxg/anet", "net.zoneCache", "netlink", "升级 Go", "TestGomobileSurfaceHasNoSkips", "模拟器"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("mobile/README.md 缺 %q——checklinkname 原因/风险未写全", want)
		}
	}
}
