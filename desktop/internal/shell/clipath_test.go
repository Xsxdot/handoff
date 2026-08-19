package shell

import (
	"path/filepath"
	"testing"
)

// 期望值一律用 filepath.Join 拼，不写字面分隔符。
//
// 为什么：filepath 的分隔符跟着**宿主**走，不跟着参数里的 goos 走。在 macOS 上
// 断言 `C:\...\handoff.exe` 必然失败，而那失败反映的是测试自己的平台假设，
// 不是被测代码的缺陷。用 Join 拼期望，断言的是「路径由哪几段组成」——那才是
// 这个函数真正的契约。
func TestCLIPathForWindowsUsesLocalAppData(t *testing.T) {
	got, err := cliPathFor("windows", `C:\Users\u`, `C:\Users\u\AppData\Local`)
	if err != nil {
		t.Fatalf("windows 有 LOCALAPPDATA 时不该报错: %v", err)
	}
	want := filepath.Join(`C:\Users\u\AppData\Local`, "Programs", "handoff", "handoff.exe")
	if got != want {
		t.Fatalf("落点必须与 install.ps1 的 Get-HandoffInstallDir 一致\n got %q\nwant %q", got, want)
	}
}

// LOCALAPPDATA 缺失是能修的：按 Windows 的固定布局从 home 推出来，
// 而不是直接失败。真实场景是精简过的服务账户环境变量表。
func TestCLIPathForWindowsFallsBackToHome(t *testing.T) {
	got, err := cliPathFor("windows", `C:\Users\u`, "")
	if err != nil {
		t.Fatalf("LOCALAPPDATA 缺失应回退到 home，不该报错: %v", err)
	}
	want := filepath.Join(`C:\Users\u`, "AppData", "Local", "Programs", "handoff", "handoff.exe")
	if got != want {
		t.Fatalf("回退落点不对\n got %q\nwant %q", got, want)
	}
}

// 两个来源都没有时必须报错，**不得**返回一个相对路径或半截路径。
// 半截路径会被 ReleaseBinary 当成落点真写下去，写到进程 CWD 里。
func TestCLIPathForWindowsBothSourcesMissing(t *testing.T) {
	if got, err := cliPathFor("windows", "", ""); err == nil {
		t.Fatalf("home 与 LOCALAPPDATA 都取不到时必须报错，实得 %q", got)
	}
}

func TestCLIPathForUnixUsesLocalBin(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		got, err := cliPathFor(goos, "/home/u", "")
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		want := filepath.Join("/home/u", ".local", "bin", "handoff")
		if got != want {
			t.Fatalf("%s 落点必须与 install.sh 一致\n got %q\nwant %q", goos, got, want)
		}
	}
}

// Unix 上 LOCALAPPDATA 恰好被设了（比如从 Wine 或某些 CI 继承来）也绝不能
// 改变落点——那会让 macOS 上的薄壳把 CLI 释出到一个谁都找不到的地方。
func TestCLIPathForUnixIgnoresLocalAppData(t *testing.T) {
	got, err := cliPathFor("darwin", "/home/u", `C:\Users\u\AppData\Local`)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/home/u", ".local", "bin", "handoff")
	if got != want {
		t.Fatalf("Unix 落点不得受 LOCALAPPDATA 影响\n got %q\nwant %q", got, want)
	}
}

func TestCLIPathForUnixMissingHome(t *testing.T) {
	if got, err := cliPathFor("linux", "", ""); err == nil {
		t.Fatalf("取不到 home 时必须报错，实得 %q", got)
	}
}

// 文件名后缀是承重的：Windows 上没有 .exe 的文件双击不起、CreateProcess
// 也拉不起来，而这个错误要到 agentd 托管失败时才显形。
func TestCLIPathForNamesBinaryPerPlatform(t *testing.T) {
	win, err := cliPathFor("windows", `C:\Users\u`, `C:\Users\u\AppData\Local`)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(win) != "handoff.exe" {
		t.Fatalf("Windows 上文件名必须是 handoff.exe，实得 %q", filepath.Base(win))
	}
	nix, err := cliPathFor("darwin", "/home/u", "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(nix) != "handoff" {
		t.Fatalf("Unix 上文件名必须是 handoff（无扩展名），实得 %q", filepath.Base(nix))
	}
}
