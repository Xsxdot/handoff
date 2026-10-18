// b435_retired_b434_guard_test.go —— B435.5（X）退役面源码守卫（plan b435.5 §5.2、拆解 §3.5 ④、
// 契约 C-238）。
//
// 职责：扫全仓非测试 .go（含独立 module desktop/），B434 退役符号与字面量任一出现即红——
// 退役以本名单为准，不以测试数为准。词法实现：注释与字符串里出现同样算（残留的注释会把
// 读者引向不存在的代码）。
// 边界：不扫 codegraph/、docs/、web/、plugins/（图、文档、前端与 mod 不在本卡）；同名陷阱
// （裸 launcher/desktop、/api/desktop/state、desktopNow、DesktopWakeMarker 等）不入名单（N5）。
// 唯一豁免：账本退役迁移本身必须按表名删表，seat_desktops / desktop_launchers 两个字面量
// 只许出现在 internal/ledger/b434_retire_migrate.go。
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// b435RetiredIdentifiers 是退役标识符（按词边界；带 * 的为前缀匹配）。
var b435RetiredIdentifiers = []string{
	"SeatDesktop*", "DesktopLauncher*", "DesktopDeliver*", "DesktopLaunchMaterial",
	"ClaudeNewSessionURLPrefix", "ClaudeNewQMaxChars",
	"desktopNeedsHumanWakes", "desktopNeedsHumanSystemActors",
	"wakeDesktopSeat", "wakeRemoteDesktopSeat", "openViaDesktopLauncher", "renderLauncherLetter",
	"desktopLetterAssembler", "renderDesktopLetter",
	"desktopWakeStall*", "desktopWakeRetry*", "desktopWakeRound", "recordDesktopWakeFailure", "clearDesktopWakeStall",
	"handleDesktopLauncherOpen", "handleDesktopLaunchMaterial", "handleDesktopDeliver",
	"runCardCoordinateDesktop", "prepareSeatDesktop", "launcherCmd",
	// Q2 甲：转交路径上无生产者的 LetterUnopened 两端分支与转交标记。
	"desktopUnopened*", "desktopUndelivered*", "withForwardedWake", "isForwardedWake",
}

// b435RetiredLiterals 是退役字符串字面量（子串匹配）。
var b435RetiredLiterals = []string{
	"seat_desktops", "desktop_launchers", "/api/desktop/deliver", "/coordinator/desktop-launch", "claude://code/new",
}

// b435RetiredLiteralAllow 是字面量豁免：迁移文件按表名删表。
var b435RetiredLiteralAllow = map[string]map[string]bool{
	filepath.FromSlash("internal/ledger/b434_retire_migrate.go"): {"seat_desktops": true, "desktop_launchers": true},
}

func b435RetiredPattern() *regexp.Regexp {
	alts := make([]string, 0, len(b435RetiredIdentifiers))
	for _, id := range b435RetiredIdentifiers {
		if strings.HasSuffix(id, "*") {
			alts = append(alts, regexp.QuoteMeta(strings.TrimSuffix(id, "*"))+`\w*`)
		} else {
			alts = append(alts, regexp.QuoteMeta(id))
		}
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(alts, "|") + `)\b`)
}

// b435RetiredHits 返回 path 内容里的退役命中（「行号: 命中」）。
func b435RetiredHits(path, content string, ids *regexp.Regexp) []string {
	var hits []string
	for i, line := range strings.Split(content, "\n") {
		for _, m := range ids.FindAllString(line, -1) {
			hits = append(hits, strconvItoa(i+1)+": "+m)
		}
		for _, lit := range b435RetiredLiterals {
			if strings.Contains(line, lit) && !b435RetiredLiteralAllow[path][lit] {
				hits = append(hits, strconvItoa(i+1)+": "+lit)
			}
		}
	}
	return hits
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestB435B434RetiredSurfaceGone(t *testing.T) {
	ids := b435RetiredPattern()
	skipDirs := map[string]bool{"codegraph": true, "docs": true, "web": true, "plugins": true,
		".git": true, ".claude": true, "node_modules": true, "prototypes": true}
	scanned := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for _, h := range b435RetiredHits(path, string(raw), ids) {
			t.Errorf("退役面残留 %s:%s", path, h)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描仓库: %v", err)
	}
	// 扫描面守卫：desktop/ module 与 internal/agentd 都必须在扫描面内，否则守卫空转。
	if scanned < 100 {
		t.Fatalf("扫描面过小（%d 个文件），守卫失效", scanned)
	}
	for _, must := range []string{"desktop/internal/shell/external_deeplink.go", "internal/agentd/desktopwake.go"} {
		if _, err := os.Stat(filepath.FromSlash(must)); err != nil {
			t.Fatalf("扫描面必含 %s: %v", must, err)
		}
	}
}

// 名单自检：守卫能变红（构造一行含退役符号的内容必须命中；同名陷阱不得命中）。
func TestB435B434RetiredGuardBites(t *testing.T) {
	ids := b435RetiredPattern()
	if h := b435RetiredHits("x.go", "// s.desktopWakeRound(card)\n", ids); len(h) != 1 {
		t.Fatalf("注释里的退役符号必须命中: %v", h)
	}
	if h := b435RetiredHits("x.go", `x := "seat_desktops"`, ids); len(h) != 1 {
		t.Fatalf("退役字面量必须命中: %v", h)
	}
	if h := b435RetiredHits(filepath.FromSlash("internal/ledger/b434_retire_migrate.go"), `DROP TABLE seat_desktops`, ids); len(h) != 0 {
		t.Fatalf("迁移文件的表名字面量应豁免: %v", h)
	}
	for _, trap := range []string{"launcher", "Launcher", "desktopNow", "desktopOpen", "desktopHome", "/api/desktop/state",
		"DesktopState", "DesktopWakeMarker", "DesktopWakeDirRel", "DesktopSessionPrefix", "ClaudeContinueURLPrefix",
		"ValidateDesktopSessionID", "desktopLetterNameRetries", "desktopLetterTmpMaxAge", "retiredSeatDesktopRow",
		"/api/launchers", "session sit --desktop-session"} {
		if h := b435RetiredHits("x.go", trap, ids); len(h) != 0 {
			t.Fatalf("同名陷阱 %q 不得命中: %v", trap, h)
		}
	}
}
