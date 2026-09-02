// toolchain 探测的表驱动测试。
//
// 三个外部依赖（PATH 查找、文件存在、HOME）全部经包级变量注入，
// 因此测试完全不依赖跑测机器上到底装没装这四家。
package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withStubs 替换四个探测缝（PATH 查找、文件存在、HOME、平台），返回时自动还原。
func withStubs(t *testing.T, home string, inPath map[string]bool, files map[string]bool, platform string) {
	t.Helper()
	oldLook, oldStat, oldHome, oldGOOS := lookPath, statFile, userHomeDir, goos
	lookPath = func(name string) (string, error) {
		if inPath[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	statFile = func(p string) error {
		if files[p] {
			return nil
		}
		return os.ErrNotExist
	}
	userHomeDir = func() (string, error) { return home, nil }
	goos = platform
	t.Cleanup(func() { lookPath, statFile, userHomeDir, goos = oldLook, oldStat, oldHome, oldGOOS })
}

// byName 从结果里取某一家，找不到直接失败。
func byName(t *testing.T, rs []Result, name string) Result {
	t.Helper()
	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("结果里没有 %s", name)
	return Result{}
}

// 五家都没装：全部 StateMissing，且顺序稳定。
func TestDetectAllMissing(t *testing.T) {
	withStubs(t, "/home/u", nil, nil, "darwin")
	rs := Detect()
	if len(rs) != 5 {
		t.Fatalf("应返回 5 项，得到 %d", len(rs))
	}
	want := []string{"opencode", "claude", "grok", "codex", "agy"}
	for i, w := range want {
		if rs[i].Name != w {
			t.Fatalf("第 %d 项应是 %s，得到 %s", i, w, rs[i].Name)
		}
		if rs[i].State != StateMissing {
			t.Errorf("%s 应为 StateMissing，得到 %v", w, rs[i].State)
		}
	}
}

func TestDetectAgyIsAlwaysAuthUnknownWhenInstalled(t *testing.T) {
	home := "/home/u"
	withStubs(t, home, map[string]bool{"agy": true}, nil, "darwin")
	r := byName(t, Detect(), "agy")
	if r.State != StateAuthUnknown {
		t.Fatalf("agy 装了就应是 StateAuthUnknown，得到 %v", r.State)
	}
	if r.Ready() {
		t.Fatal("StateAuthUnknown 的 Ready() 必须为 false")
	}
	if r.Path == "" {
		t.Fatal("装了就该带上可执行文件路径")
	}
}

// 装了但没凭证文件：StateNoCreds（claude 除外，见下一例）。
func TestDetectInstalledWithoutCreds(t *testing.T) {
	withStubs(t, "/home/u", map[string]bool{"opencode": true, "grok": true, "codex": true}, nil, "darwin")
	rs := Detect()
	for _, n := range []string{"opencode", "grok", "codex"} {
		if got := byName(t, rs, n).State; got != StateNoCreds {
			t.Errorf("%s 应为 StateNoCreds，得到 %v", n, got)
		}
	}
}

// 凭证文件在：StateReady。路径必须逐字符合 spec 里查实的那三条。
func TestDetectReadyUsesVerifiedCredPaths(t *testing.T) {
	home := "/home/u"
	files := map[string]bool{
		filepath.Join(home, ".local/share/opencode/auth.json"): true,
		filepath.Join(home, ".grok/auth.json"):                 true,
		filepath.Join(home, ".codex/auth.json"):                true,
	}
	withStubs(t, home, map[string]bool{"opencode": true, "grok": true, "codex": true}, files, "darwin")
	rs := Detect()
	for _, n := range []string{"opencode", "grok", "codex"} {
		r := byName(t, rs, n)
		if r.State != StateReady {
			t.Errorf("%s 应为 StateReady，得到 %v", n, r.State)
		}
		if !r.Ready() {
			t.Errorf("%s 的 Ready() 应为 true", n)
		}
	}
}

// claude 装了就只能是 StateAuthUnknown——不许猜成就绪，也不许猜成未就绪。
//
// why：Claude Code 的 OAuth 凭据在 macOS Keychain 里，轻量判据够不着；
// ~/.claude.json 存在但那是配置不是凭证。把它当就绪，用户会以为能派活；
// 当未就绪，用户会去重装一个其实已经能用的东西。两种都是编造。
func TestDetectClaudeIsAlwaysAuthUnknownWhenInstalled(t *testing.T) {
	home := "/home/u"
	// 连 ~/.claude.json 都放上，也不许因此判成就绪
	files := map[string]bool{
		filepath.Join(home, ".claude.json"):              true,
		filepath.Join(home, ".claude/.credentials.json"): true,
	}
	withStubs(t, home, map[string]bool{"claude": true}, files, "darwin")
	r := byName(t, Detect(), "claude")
	if r.State != StateAuthUnknown {
		t.Fatalf("claude 装了就应是 StateAuthUnknown，得到 %v", r.State)
	}
	if r.Ready() {
		t.Fatal("StateAuthUnknown 的 Ready() 必须为 false——登录态未知不等于就绪")
	}
	if r.Path == "" {
		t.Fatal("装了就该带上可执行文件路径")
	}
}

// 没装的 claude 仍是 StateMissing，不是 StateAuthUnknown。
func TestDetectClaudeMissing(t *testing.T) {
	withStubs(t, "/home/u", nil, nil, "darwin")
	if got := byName(t, Detect(), "claude").State; got != StateMissing {
		t.Fatalf("没装的 claude 应为 StateMissing，得到 %v", got)
	}
}

// 取不到 HOME 时不能崩，也不能把「查不了」说成「没登录」或「没装」。
func TestDetectHomeUnavailable(t *testing.T) {
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { userHomeDir = oldHome })
	withStubsKeepHome(t, map[string]bool{"opencode": true})
	r := byName(t, Detect(), "opencode")
	if r.State != StateAuthUnknown {
		t.Fatalf("HOME 不可用时装了的执行者应报 StateAuthUnknown（查不了≠没登录），得到 %v", r.State)
	}
}

// withStubsKeepHome 只替换 lookPath/statFile，保留调用方已设的 userHomeDir。
func withStubsKeepHome(t *testing.T, inPath map[string]bool) {
	t.Helper()
	oldLook, oldStat := lookPath, statFile
	lookPath = func(name string) (string, error) {
		if inPath[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	statFile = func(string) error { return os.ErrNotExist }
	t.Cleanup(func() { lookPath, statFile = oldLook, oldStat })
}

// State 的中文短语是 init 表格直接打印的内容，钉住避免改文案时漏改一处。
func TestStateStrings(t *testing.T) {
	cases := map[State]string{
		StateMissing:     "没装",
		StateNoCreds:     "已安装，未登录",
		StateReady:       "就绪",
		StateAuthUnknown: "已安装，登录态未知",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String()=%q，期望 %q", s, got, want)
		}
	}
}

// TestDetectWindowsOpencodeAuthIsUnknown 覆盖 Windows 上的 opencode 登录判据。
//
// why：.local/share/opencode/auth.json 是 XDG 落点，Windows 上 opencode 不用它。
// 拿一个在该平台不成立的路径去断言「未登录」是撒谎——如实报「查不了」。
// 反过来 grok / codex 的 ~/.grok、~/.codex 在 Windows 上同样成立，必须仍按文件判定，
// 这条断言是防止把三家一起误伤。
func TestDetectWindowsOpencodeAuthIsUnknown(t *testing.T) {
	home := "/home/u"
	inPath := map[string]bool{"opencode": true, "grok": true, "codex": true}
	files := map[string]bool{
		filepath.Join(home, ".local/share/opencode/auth.json"): true, // 就算这个文件在，Windows 上也不该拿它当判据
		filepath.Join(home, ".grok/auth.json"):                 true,
	}
	withStubs(t, home, inPath, files, "windows")

	rs := Detect()
	if got := byName(t, rs, "opencode").State; got != StateAuthUnknown {
		t.Fatalf("windows 上 opencode 应为 StateAuthUnknown，实为 %v", got)
	}
	if got := byName(t, rs, "grok").State; got != StateReady {
		t.Fatalf("windows 上 grok 凭证文件在，应为 StateReady，实为 %v", got)
	}
	if got := byName(t, rs, "codex").State; got != StateNoCreds {
		t.Fatalf("windows 上 codex 凭证文件不在，应为 StateNoCreds，实为 %v", got)
	}
	// claude 不在 PATH 里（inPath 没给它），仍应是 StateMissing——
	// 「没装」与「查不了」是两件事，本改动不得把它们混起来
	if got := byName(t, rs, "claude").State; got != StateMissing {
		t.Fatalf("windows 上 claude 未安装，应为 StateMissing，实为 %v", got)
	}
}

// TestDetectDarwinOpencodeUnchanged 是回归：非 Windows 平台行为逐字不变。
func TestDetectDarwinOpencodeUnchanged(t *testing.T) {
	home := "/home/u"
	inPath := map[string]bool{"opencode": true}
	files := map[string]bool{filepath.Join(home, ".local/share/opencode/auth.json"): true}
	withStubs(t, home, inPath, files, "darwin")

	if got := byName(t, Detect(), "opencode").State; got != StateReady {
		t.Fatalf("darwin 上凭证文件在，opencode 应为 StateReady，实为 %v", got)
	}
}
