// claudemod_test.go —— 锁 B434 契约 §4.8 条 128–137 与 breakdown §5.8（非法 JSON 不改写、保序保值）。
//
// 全部经 InstallClaudeMod / ClaudeModStatus（契约 §3.13 冻结签名）进入，home 一律 t.TempDir()，
// mod 一律 fstest.MapFS——不读真实家目录、不依赖构建产物。
package skill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// testClaudeMod 是一份三件形状的假 mod；内容带版本号，便于造 stale。
func testClaudeMod(version string) fstest.MapFS {
	return fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(`{"name":"handoff-desktop-wake","version":"` + version + `"}`)},
		"hooks/hooks.json":           {Data: []byte(`{ "modules": ["./register.ts"] }`)},
		"hooks/register.ts":          {Data: []byte("// register " + version + "\nexport const register = () => {}\n")},
	}
}

// homeWithClaude 造一个有 ~/.claude 的 home，可选写入 settings.json 原文。
func homeWithClaude(t *testing.T, settings string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func readSettings(t *testing.T, home string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pluginDirsOf 用标准库（不经被测代码）读出 env.CLAUDE_CODE_PLUGIN_DIRS。
func pluginDirsOf(t *testing.T, settings []byte) string {
	t.Helper()
	var doc struct {
		Env map[string]*string `json:"env"`
	}
	if err := json.Unmarshal(settings, &doc); err != nil {
		t.Fatalf("settings.json 不是合法 JSON: %v\n%s", err, settings)
	}
	v := doc.Env["CLAUDE_CODE_PLUGIN_DIRS"]
	if v == nil {
		t.Fatalf("env.CLAUDE_CODE_PLUGIN_DIRS 缺失:\n%s", settings)
	}
	return *v
}

// topKeys 用 json.Decoder 读出顶层键序（独立于被测实现）。
func topKeys(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// compactValue 把一个顶层键的值压成紧凑形式，用于「保值」比较（空白不是值的一部分）。
func compactValue(t *testing.T, b []byte, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	raw, ok := m[key]
	if !ok {
		return "<absent>"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestB434InstallClaudeModWritesFilesVerbatim 锁条 128：全部文件逐字节原样落到安装目录。
func TestB434InstallClaudeModWritesFilesVerbatim(t *testing.T) {
	home := homeWithClaude(t, "")
	mod := testClaudeMod("1")
	site := InstallClaudeMod(mod, home)
	target := filepath.Join(home, ClaudeModRelDir)
	if site.State != StateInstalled || site.Path != target {
		t.Fatalf("site=%+v，期望 installed @ %s", site, target)
	}
	for rel, f := range mod {
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s 未写入: %v", rel, err)
		}
		if !bytes.Equal(got, f.Data) {
			t.Fatalf("%s 内容被改动:\n got %q\nwant %q", rel, got, f.Data)
		}
	}
}

// TestB434InstallClaudeModAddsPluginDir 锁条 129：settings.json 不存在 / 无 env / env 无该变量三种起点，装后都含该目录。
func TestB434InstallClaudeModAddsPluginDir(t *testing.T) {
	for name, settings := range map[string]string{
		"无 settings.json": "",
		"无 env":           `{"model":"opus"}`,
		"env 无该变量":        `{"env":{"FOO":"1"}}`,
		"该变量为空串":          `{"env":{"CLAUDE_CODE_PLUGIN_DIRS":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := homeWithClaude(t, settings)
			if s := InstallClaudeMod(testClaudeMod("1"), home); s.State != StateInstalled {
				t.Fatalf("site=%+v", s)
			}
			got := pluginDirsOf(t, readSettings(t, home))
			if want := filepath.Join(home, ClaudeModRelDir); got != want {
				t.Fatalf("CLAUDE_CODE_PLUGIN_DIRS=%q，期望 %q", got, want)
			}
		})
	}
}

// TestB434InstallClaudeModKeepsOtherPluginDirs 锁条 130：原有目录项原样保留、顺序不变，本目录追加在末尾。
func TestB434InstallClaudeModKeepsOtherPluginDirs(t *testing.T) {
	sep := string(filepath.ListSeparator)
	orig := "/opt/a" + sep + "~/mods/b"
	home := homeWithClaude(t, `{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"`+orig+`"}}`)
	if s := InstallClaudeMod(testClaudeMod("1"), home); s.State != StateInstalled {
		t.Fatalf("site=%+v", s)
	}
	got := filepath.SplitList(pluginDirsOf(t, readSettings(t, home)))
	want := []string{"/opt/a", "~/mods/b", filepath.Join(home, ClaudeModRelDir)}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("目录项=%q，期望 %q", got, want)
	}
}

// TestB434InstallClaudeModKeepsOtherSettingsKeys 锁条 131：其它顶层键保序保值，env 里其它变量保序保值。
func TestB434InstallClaudeModKeepsOtherSettingsKeys(t *testing.T) {
	orig := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(go test:*)"], "deny": []},
  "env": {"ZZZ": "last", "CLAUDE_CODE_PLUGIN_DIRS": "/opt/a", "AAA": "first"},
  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo <done> & ok"}]}]},
  "unicode": "中文 é"
}`
	home := homeWithClaude(t, orig)
	if s := InstallClaudeMod(testClaudeMod("1"), home); s.State != StateInstalled {
		t.Fatalf("site=%+v", s)
	}
	after := readSettings(t, home)
	if got, want := strings.Join(topKeys(t, after), ","), "model,permissions,env,hooks,unicode"; got != want {
		t.Fatalf("顶层键序=%s，期望 %s", got, want)
	}
	for _, k := range []string{"model", "permissions", "hooks", "unicode"} {
		if got, want := compactValue(t, after, k), compactValue(t, []byte(orig), k); got != want {
			t.Fatalf("键 %s 的值被改动:\n got %s\nwant %s", k, got, want)
		}
	}
	envBefore := []byte(compactValue(t, []byte(orig), "env"))
	envAfter := []byte(compactValue(t, after, "env"))
	if got := strings.Join(topKeys(t, envAfter), ","); got != "ZZZ,CLAUDE_CODE_PLUGIN_DIRS,AAA" {
		t.Fatalf("env 键序=%s", got)
	}
	for _, k := range []string{"ZZZ", "AAA"} {
		if compactValue(t, envAfter, k) != compactValue(t, envBefore, k) {
			t.Fatalf("env.%s 被改动", k)
		}
	}
}

// TestB434InstallClaudeModIdempotent 锁「去重」：已含本目录（绝对或 ~ 写法）时 settings.json 一个字节都不改。
func TestB434InstallClaudeModIdempotent(t *testing.T) {
	sep := string(filepath.ListSeparator)
	for name, list := range map[string]string{
		"绝对路径": "/opt/a" + sep + "@ABS@",
		"~ 写法": "~/" + ClaudeModRelDir + sep + "/opt/a",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			list = strings.ReplaceAll(list, "@ABS@", filepath.Join(home, ClaudeModRelDir))
			os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
			orig := []byte("{\"env\":{\"CLAUDE_CODE_PLUGIN_DIRS\":\"" + list + "\"}}")
			os.WriteFile(filepath.Join(home, ".claude", "settings.json"), orig, 0o600)
			for i := 0; i < 2; i++ {
				if s := InstallClaudeMod(testClaudeMod("1"), home); s.State != StateInstalled {
					t.Fatalf("第 %d 次 site=%+v", i+1, s)
				}
			}
			if after := readSettings(t, home); !bytes.Equal(after, orig) {
				t.Fatalf("已含本目录仍改写了 settings.json:\n%s", after)
			}
		})
	}
	// 从零装两次：只追加一次
	home := homeWithClaude(t, "")
	InstallClaudeMod(testClaudeMod("1"), home)
	InstallClaudeMod(testClaudeMod("1"), home)
	if got := pluginDirsOf(t, readSettings(t, home)); got != filepath.Join(home, ClaudeModRelDir) {
		t.Fatalf("重复安装后 CLAUDE_CODE_PLUGIN_DIRS=%q", got)
	}
}

// TestB434InstallClaudeModSkipsWithoutClaudeHome 锁条 132/133：没装 Claude Code 报 skipped，不造 ~/.claude，也不写 mod。
func TestB434InstallClaudeModSkipsWithoutClaudeHome(t *testing.T) {
	home := t.TempDir()
	site := InstallClaudeMod(testClaudeMod("1"), home)
	if site.State != StateSkipped || site.Note == "" {
		t.Fatalf("site=%+v，期望 skipped 且带理由", site)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("不得代造 ~/.claude")
	}
	if _, err := os.Stat(filepath.Join(home, ClaudeModRelDir)); !os.IsNotExist(err) {
		t.Fatal("跳过时不得写 mod 文件")
	}
}

// TestB434InstallClaudeModRefusesUnmergeableSettings 锁 breakdown §5.8 静默失败条：
// settings.json 合并不了 → skipped + Note，settings.json 一个字节不改，mod 文件也不写。
func TestB434InstallClaudeModRefusesUnmergeableSettings(t *testing.T) {
	for name, settings := range map[string]string{
		"非法 JSON":  `{"env": {`,
		"顶层是数组":    `[1,2]`,
		"env 不是对象": `{"env":"x"}`,
		"变量不是字符串":  `{"env":{"CLAUDE_CODE_PLUGIN_DIRS":["/a"]}}`,
		"env 键重复":  `{"env":{},"env":{}}`,
		"对象后有多余内容": `{"a":1} {"b":2}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := homeWithClaude(t, settings)
			site := InstallClaudeMod(testClaudeMod("1"), home)
			if site.State != StateSkipped || !strings.Contains(site.Note, "settings.json") {
				t.Fatalf("site=%+v，期望 skipped 且 Note 点名 settings.json", site)
			}
			if got := string(readSettings(t, home)); got != settings {
				t.Fatalf("settings.json 被改写: %q", got)
			}
			if _, err := os.Stat(filepath.Join(home, ClaudeModRelDir)); !os.IsNotExist(err) {
				t.Fatal("合并失败时不得写 mod 文件")
			}
		})
	}
}

// TestB434InstallClaudeModRefusesBrokenEmbed：内嵌为 nil、为空或缺清单（embed 丢了点目录）一律 skipped、什么都不写。
func TestB434InstallClaudeModRefusesBrokenEmbed(t *testing.T) {
	noManifest := testClaudeMod("1")
	delete(noManifest, ".claude-plugin/plugin.json")
	for name, mod := range map[string]fstest.MapFS{"nil": nil, "空": {}, "缺清单": noManifest} {
		t.Run(name, func(t *testing.T) {
			home := homeWithClaude(t, "")
			var site Site
			if mod == nil {
				site = InstallClaudeMod(nil, home)
			} else {
				site = InstallClaudeMod(mod, home)
			}
			if site.State != StateSkipped || site.Note == "" {
				t.Fatalf("site=%+v", site)
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
				t.Fatal("内嵌坏了不得改 settings.json")
			}
		})
	}
}

// TestB434InstallClaudeModFollowsSymlinkedSettings：settings.json 是符号链接（dotfiles 仓库）时改链接目标，链接本身保留。
func TestB434InstallClaudeModFollowsSymlinkedSettings(t *testing.T) {
	home := homeWithClaude(t, "")
	real := filepath.Join(t.TempDir(), "dotfiles-settings.json")
	os.WriteFile(real, []byte(`{"model":"opus"}`), 0o600)
	link := filepath.Join(home, ".claude", "settings.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("本平台不能建符号链接:", err)
	}
	if s := InstallClaudeMod(testClaudeMod("1"), home); s.State != StateInstalled {
		t.Fatalf("site=%+v", s)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("settings.json 的符号链接被替换成了普通文件")
	}
	b, _ := os.ReadFile(real)
	if pluginDirsOf(t, b) != filepath.Join(home, ClaudeModRelDir) {
		t.Fatalf("链接目标未更新:\n%s", b)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Fatalf("原文件权限 0600 未保留: %v", fi.Mode().Perm())
	}
}

// TestB434InstallClaudeModPreservesRandomSettings 是序列化边界的 roundtrip 属性测试：
// 随机构造 settings.json（随机键序、嵌套、转义字符），装一次后除 env.CLAUDE_CODE_PLUGIN_DIRS 外语义恒等、键序不变。
func TestB434InstallClaudeModPreservesRandomSettings(t *testing.T) {
	r := rand.New(rand.NewSource(434))
	for i := 0; i < 200; i++ {
		orig := randomSettings(r)
		home := homeWithClaude(t, orig)
		if s := InstallClaudeMod(testClaudeMod("1"), home); s.State != StateInstalled {
			t.Fatalf("样本 %d site=%+v\n%s", i, s, orig)
		}
		after := readSettings(t, home)
		var before, got map[string]any
		json.Unmarshal([]byte(orig), &before)
		if err := json.Unmarshal(after, &got); err != nil {
			t.Fatalf("样本 %d 装后不是合法 JSON: %v", i, err)
		}
		gotEnv, _ := got["env"].(map[string]any)
		if gotEnv["CLAUDE_CODE_PLUGIN_DIRS"] != filepath.Join(home, ClaudeModRelDir) {
			t.Fatalf("样本 %d 未写入目录: %v", i, gotEnv)
		}
		delete(gotEnv, "CLAUDE_CODE_PLUGIN_DIRS")
		if len(gotEnv) == 0 {
			if _, had := before["env"]; !had {
				delete(got, "env")
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(before) {
			t.Fatalf("样本 %d 其它内容被改动:\nbefore %v\n after %v", i, before, got)
		}
		wantKeys := topKeys(t, []byte(orig))
		if _, had := before["env"]; !had {
			wantKeys = append(wantKeys, "env")
		}
		if strings.Join(topKeys(t, after), ",") != strings.Join(wantKeys, ",") {
			t.Fatalf("样本 %d 键序被改动", i)
		}
	}
}

// randomSettings 生成一个随机的 settings.json 对象原文（不含 env，或含不带目标变量的 env）。
func randomSettings(r *rand.Rand) string {
	var b strings.Builder
	b.WriteString("{")
	n := r.Intn(6)
	used := map[string]bool{}
	first := true
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k%d_%s", r.Intn(1000), []string{"", "<&>", "中", `\"q\"`, " "}[r.Intn(5)])
		if used[k] {
			continue
		}
		used[k] = true
		if !first {
			b.WriteString(",\n  ")
		}
		first = false
		fmt.Fprintf(&b, "%q: %s", k, randomValue(r, 3))
	}
	if r.Intn(2) == 0 {
		if !first {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"env": {"X_%d": %q}`, r.Intn(100), "v<>&")
	}
	b.WriteString("}")
	return b.String()
}

func randomValue(r *rand.Rand, depth int) string {
	switch c := r.Intn(6); {
	case depth == 0 || c == 0:
		return fmt.Sprintf("%q", fmt.Sprintf("s%d<&>中\t", r.Intn(100)))
	case c == 1:
		return fmt.Sprint(r.Intn(1e6))
	case c == 2:
		return []string{"true", "false", "null", "1.5e3"}[r.Intn(4)]
	case c == 3:
		return "[" + randomValue(r, depth-1) + " , " + randomValue(r, depth-1) + "]"
	default:
		return fmt.Sprintf(`{ "a%d" : %s }`, r.Intn(10), randomValue(r, depth-1))
	}
}

// installedHome 造一个已按 mod 装好、settings 含目录的 home，供状态用例改坏。
func installedHome(t *testing.T) string {
	t.Helper()
	home := homeWithClaude(t, "")
	if s := InstallClaudeMod(testClaudeMod("1"), home); s.State != StateInstalled {
		t.Fatalf("site=%+v", s)
	}
	return home
}

// TestB434ClaudeModStatusInSync 锁条 134：文件全一致且 env 含目录 → in_sync；引擎写的 types 不算漂移。
func TestB434ClaudeModStatusInSync(t *testing.T) {
	home := installedHome(t)
	types := filepath.Join(home, ClaudeModRelDir, ".claude-plugin", "types", "claude-code", "index.d.ts")
	os.MkdirAll(filepath.Dir(types), 0o755)
	os.WriteFile(types, []byte("// engine wrote this"), 0o644)
	if s := ClaudeModStatus(testClaudeMod("1"), home); s.State != StateInSync {
		t.Fatalf("site=%+v，期望 in_sync", s)
	}
}

// TestB434ClaudeModStatusStale 锁条 135：任一文件内容与内嵌不一致 → stale，且 InSync 为假（命令层会提示重装）。
func TestB434ClaudeModStatusStale(t *testing.T) {
	home := installedHome(t)
	s := ClaudeModStatus(testClaudeMod("2"), home)
	if s.State != StateStale || InSync([]Site{s}) {
		t.Fatalf("site=%+v，期望 stale", s)
	}
}

// TestB434ClaudeModStatusEnvMissing 锁条 136：文件全对但 env 不含目录 → 不报 in_sync（报 stale，Note 点名变量）。
func TestB434ClaudeModStatusEnvMissing(t *testing.T) {
	home := installedHome(t)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"/opt/a"}}`), 0o600)
	s := ClaudeModStatus(testClaudeMod("1"), home)
	if s.State == StateInSync || !strings.Contains(s.Note, "CLAUDE_CODE_PLUGIN_DIRS") {
		t.Fatalf("site=%+v，env 不含目录不得报 in_sync", s)
	}
}

// TestB434ClaudeModStatusMissing 锁条 137：任一 mod 文件缺失 → missing；没装 Claude Code 也是 missing（带理由）。
func TestB434ClaudeModStatusMissing(t *testing.T) {
	home := installedHome(t)
	os.Remove(filepath.Join(home, ClaudeModRelDir, "hooks", "register.ts"))
	if s := ClaudeModStatus(testClaudeMod("1"), home); s.State != StateMissing || !strings.Contains(s.Note, "hooks/register.ts") {
		t.Fatalf("site=%+v，期望 missing 并点名缺的文件", s)
	}
	if s := ClaudeModStatus(testClaudeMod("1"), t.TempDir()); s.State != StateMissing || s.Note == "" {
		t.Fatalf("没装 Claude Code: site=%+v", s)
	}
}
