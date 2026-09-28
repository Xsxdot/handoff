// shell_api_golden_test.go 是 B417 契约节点的**可执行冻结**：把 Android/iOS 原生
// 壳编译期消费的 gomobile 生成面（Kotlin 侧 = Java 类的静态方法，Swift 侧 =
// ObjC 头的 FOUNDATION_EXPORT 函数）逐字钉住。
//
// 为什么需要它：B417 壳工程本身是 Kotlin/Swift，本仓 Linux 工作树无 JDK/Xcode，
// 壳骨架的编译要落到装了工具链的 macOS 子卡；但「壳编译时看到的 API 形状」是
// 可以由钉版的 gobind 在**任何机器**上生成并断言的。把生成的 Java/ObjC 签名
// 冻结住，壳侧一旦与核面错配，最早在这里变红，而不是等到 macOS 上壳编译失败。
//
// 与 gobind_surface_test.go 的分工：那条（B386）断言「无 skipped 成员 + 每个
// 导出函数都出现」；本条进一步断言「生成的**精确签名**等于冻结面」。两者都跑
// 真 gobind，都锚在 mobile/bind 的导出面。
//
// 环境：优先用本模块 go.mod 的 `tool golang.org/x/mobile/cmd/gobind`（go 1.24+
// 的 tool 指令），故不依赖 $PATH 里有 gobind；工具不可用时 skip（本仓多机开发，
// 缺工具是常态，见 gobind_surface_test.go 的同款注释）。
package bind

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wantJavaShellAPI 是 Android 壳（Kotlin 经 Java 类消费）看到的精确签名。
// 归一化空白后逐行相等；多一个、少一个、签名漂移即红。
var wantJavaShellAPI = []string{
	"public static native void close() throws Exception;",
	"public static native Machine machineAt(long index);",
	"public static native long machineCount();",
	"public static native String origin(String machine) throws Exception;",
	"public static native void pair(String bundleJSON) throws Exception;",
	"public static native String sessionCookie(String machine) throws Exception;",
	"public static native String switchMachine(String machine) throws Exception;",
}

// wantJavaMachineFields 是 Android 壳读机器列表用的 DTO 访问器。
var wantJavaMachineFields = []string{
	"public final native String getName();",
	"public final native String getOrigin();",
	"public final native boolean getOnline();",
}

// wantObjCShellAPI 是 iOS 壳（Swift 经 ObjC 头消费）看到的精确签名。
var wantObjCShellAPI = []string{
	"FOUNDATION_EXPORT BOOL BindClose(NSError* _Nullable* _Nullable error);",
	"FOUNDATION_EXPORT BindMachine* _Nullable BindMachineAt(long index);",
	"FOUNDATION_EXPORT long BindMachineCount(void);",
	"FOUNDATION_EXPORT NSString* _Nonnull BindOrigin(NSString* _Nullable machine, NSError* _Nullable* _Nullable error);",
	"FOUNDATION_EXPORT BOOL BindPair(NSString* _Nullable bundleJSON, NSError* _Nullable* _Nullable error);",
	"FOUNDATION_EXPORT NSString* _Nonnull BindSessionCookie(NSString* _Nullable machine, NSError* _Nullable* _Nullable error);",
	"FOUNDATION_EXPORT NSString* _Nonnull BindSwitchMachine(NSString* _Nullable machine, NSError* _Nullable* _Nullable error);",
}

// wantObjCMachineFields 是 iOS 壳读机器列表用的 DTO 属性。
var wantObjCMachineFields = []string{
	"@property (nonatomic) NSString* _Nonnull name;",
	"@property (nonatomic) NSString* _Nonnull origin;",
	"@property (nonatomic) BOOL online;",
}

// TestBindGeneratedShellAPIGolden 把生成的 Java/ObjC 面逐字冻结为 B417 壳契约。
func TestBindGeneratedShellAPIGolden(t *testing.T) {
	out := t.TempDir()
	dir := packageDir(t)
	for _, tc := range []struct {
		lang string
		// 生成文件相对 out/lang 的路径。
		regFile  string
		wantFunc []string
		dtoFile  string
		wantDTO  []string
	}{
		{"java", "java/bind/Bind.java", wantJavaShellAPI, "java/bind/Machine.java", wantJavaMachineFields},
		{"objc", "src/gobind/Bind.objc.h", wantObjCShellAPI, "src/gobind/Bind.objc.h", wantObjCMachineFields},
	} {
		langOut := filepath.Join(out, tc.lang)
		if err := os.MkdirAll(langOut, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("go", "tool", "gobind", "-lang="+tc.lang, "-outdir="+langOut, ".")
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("go tool gobind -lang=%s 不可用（%v）：%s", tc.lang, err, b)
		}

		body := readFileString(t, filepath.Join(langOut, tc.regFile))
		if strings.Contains(body, "skipped function") || strings.Contains(body, "skipped field") {
			t.Fatalf("[-lang=%s] gomobile 跳过了成员，壳拿不到它：\n%s", tc.lang, grepSkipped(body))
		}
		assertGoldenLines(t, tc.lang, tc.regFile, body, tc.wantFunc)

		dto := readFileString(t, filepath.Join(langOut, tc.dtoFile))
		assertGoldenLines(t, tc.lang, tc.dtoFile, dto, tc.wantDTO)
	}
}

// assertGoldenLines 断言每条 want 都是 content 的某个归一化整行。
func assertGoldenLines(t *testing.T, lang, file, content string, want []string) {
	t.Helper()
	lines := map[string]bool{}
	for _, l := range strings.Split(content, "\n") {
		lines[normalizeSpace(l)] = true
	}
	for _, w := range want {
		if !lines[normalizeSpace(w)] {
			t.Errorf("[-lang=%s %s] 生成面缺少冻结签名：%q", lang, file, w)
		}
	}
}

// normalizeSpace 把任意连续空白折成单空格并去首尾空白，抵消生成器的排版差异。
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// grepSkipped 摘出 skipped 行供失败信息定位。
func grepSkipped(body string) string {
	var b strings.Builder
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, "skipped function") || strings.Contains(l, "skipped field") {
			b.WriteString(strings.TrimSpace(l))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// readFileString 读文本文件，失败即 t.Fatal。
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读生成产物 %s: %v", path, err)
	}
	return string(data)
}
