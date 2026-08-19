package main

import (
	"encoding/json"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
)

// 桌面端图标的两个**输入源**必须自洽。产物（icon.ico / icons.icns / Assets.car）
// 由 wails3 generate icons 在构建期生成且已入 .gitignore，钉产物没有意义——
// 钉的是「拿什么去生成」。
//
// 这两条都对应真实存在的静默失败：图标错了不会让任何构建报错，要到用户双击
// 看见一个空白方框、或托盘里是别人的 logo 才发现。

const (
	appIconPath    = "desktop/build/appicon.png"
	iconBundlePath = "desktop/build/appicon.icon"
)

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

// icon.json 的每个 image-name 都必须在 Assets/ 下真的存在。
//
// 这是本文件最承重的一条：Icon Composer 的清单与资源文件是**按文件名字符串**
// 关联的，改了资源文件名而忘了改清单，`wails3 generate icons` **照样退出 0**，
// 只是 Assets.car 里那一层是空的——macOS 上得到一个没有字形的图标。
func TestIconComposerManifestReferencesExistingAssets(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(iconBundlePath, "icon.json"))
	if err != nil {
		t.Fatalf("读 icon.json: %v", err)
	}
	var manifest struct {
		Groups []struct {
			Layers []struct {
				ImageName string `json:"image-name"`
			} `json:"layers"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("解析 icon.json: %v", err)
	}
	var seen int
	for _, g := range manifest.Groups {
		for _, l := range g.Layers {
			if l.ImageName == "" {
				continue
			}
			seen++
			p := filepath.Join(iconBundlePath, "Assets", l.ImageName)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("icon.json 引用了不存在的资源 %q（期望在 %s）——"+
					"generate icons 不会因此报错，只会产出一个没有字形的图标: %v",
					l.ImageName, p, err)
			}
		}
	}
	if seen == 0 {
		t.Fatal("icon.json 里一个 image-name 都没有，图标层是空的")
	}
}
