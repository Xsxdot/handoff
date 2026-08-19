package main

import (
	"image"
	_ "image/png"
	"os"
	"strings"
	"testing"
)

// 桌面端图标的两个**输入源**必须自洽。产物（icon.ico / icons.icns / Assets.car）
// 由 wails3 generate icons 在构建期生成且已入 .gitignore，钉产物没有意义——
// 钉的是「拿什么去生成」。
//
// 这两条都对应真实存在的静默失败：图标错了不会让任何构建报错，要到用户双击
// 看见一个空白方框、或托盘里是别人的 logo 才发现。

const appIconPath = "desktop/build/appicon.png"

// appicon.png 是 ico 与 icns 的唯一输入。必须是**正方形**且不小于 1024——
// icns 有 1024 档位，源图不够大只能拉伸，Retina 下发虚。
func TestDesktopAppIconIsSquareAndLargeEnough(t *testing.T) {
	f, err := os.Open(appIconPath)
	if err != nil {
		t.Fatalf("读 %s: %v", appIconPath, err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("解码 %s: %v", appIconPath, err)
	}
	if format != "png" {
		t.Fatalf("%s 必须是 PNG，实际 %s", appIconPath, format)
	}
	if cfg.Width != cfg.Height {
		t.Fatalf("应用图标必须是正方形，实际 %dx%d——非方图会被拉伸或加黑边",
			cfg.Width, cfg.Height)
	}
	if cfg.Width < 1024 {
		t.Fatalf("应用图标至少 1024（icns 有 1024 档位），实际 %d", cfg.Width)
	}
}

// macOS 的图标来源必须**只有一条**：Info.plist 的 CFBundleIconName 与
// generate:icons 的 -iconcomposerinput 必须同时在、或同时不在。
//
// 为什么这是一条门而不是一句注释：两者分居两个文件，改一处忘另一处**不会有任何
// 报错**。
//   - 有 CFBundleIconName、不产 Assets.car → macOS 找不到资源目录，静默回落
//     CFBundleIconFile（看起来没事，直到某个 macOS 版本不再回落）
//   - 产 Assets.car、没 CFBundleIconName → 白生成一份没人用的资源目录
//
// 本项目**刻意选了「都不要」**：Icon Composer 会把 SVG 当图层去合成（阴影、高光、
// 半透明、按 scale 缩放），对扁平品牌标志做不到与原图一致（2026-08-19 实测字形
// 偏小、发灰、带投影）。代价是放弃 macOS 26 的分层/着色变体。
func TestMacIconSourceIsSingular(t *testing.T) {
	plist, err := os.ReadFile("desktop/build/darwin/Info.plist")
	if err != nil {
		t.Fatalf("读 Info.plist: %v", err)
	}
	taskfile, err := os.ReadFile("desktop/build/Taskfile.yml")
	if err != nil {
		t.Fatalf("读 build/Taskfile.yml: %v", err)
	}
	hasIconName := strings.Contains(string(plist), "CFBundleIconName")
	// 只看真正生效的那一行，注释里提到这个 flag 不算
	var passesComposer bool
	for _, line := range strings.Split(string(taskfile), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "wails3 generate icons") &&
			strings.Contains(trimmed, "-iconcomposerinput") {
			passesComposer = true
		}
	}
	if hasIconName != passesComposer {
		t.Fatalf("macOS 图标来源不自洽：Info.plist 有 CFBundleIconName=%v，"+
			"而 generate:icons 传 -iconcomposerinput=%v——两者必须同进同退，"+
			"不一致时 macOS 只会静默回落，不会报错", hasIconName, passesComposer)
	}
	// CFBundleIconFile 是本项目当前唯一的图标来源，丢了就没有图标
	if !strings.Contains(string(plist), "CFBundleIconFile") {
		t.Fatal("Info.plist 缺 CFBundleIconFile，.app 会没有图标")
	}
}
