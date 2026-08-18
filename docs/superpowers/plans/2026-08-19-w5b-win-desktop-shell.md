# Windows 桌面薄壳 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `handoff-desktop` 在 Windows 上与 macOS/Linux 同形态交付——薄壳释出的 CLI 落在与 `install.ps1` 完全一致的路径，且 release 流水线产出 `handoff-desktop_<tag>_windows_amd64.zip`。

**Architecture:** 三处改动互不耦合。①薄壳内部两处硬编码的 `~/.local/bin/handoff` 收敛成一个平台自适应的纯函数；②release.yml 增一个 `build-desktop-windows` job，形态照抄既有的 `build-desktop-linux`；③契约测试跟着从「两个薄壳 job」改成「三个」。构建资产（`desktop/build/windows/`）与 Taskfile 钩子**已在基线上就位**（commit `1312731c1`），本计划不碰它们。

**Tech Stack:** Go 1.26、Wails v3.0.0-beta.8、GitHub Actions、go-task。

## Global Constraints

- **`desktop/build/windows/` 与 `desktop/Taskfile.yml`、`desktop/build/Taskfile.yml`、`desktop/.gitignore` 已经改好，不要再动它们。** 基线已验：`wails3 task windows:build` 在 macOS 上退出 0，产物 `file` 报 `PE32+ executable (GUI) x86-64, for MS Windows`。
- **Taskfile 只认 `EXTRA_TAGS` / `EXTRA_LDFLAGS`，不认 `GO_FLAGS`**。传 `GO_FLAGS` 不报错、会被静默忽略，编出一个不含内嵌 CLI 的空壳，要到用户双击才暴露。
- **go:embed 的文件名固定是 `handoff`（无扩展名）**。`desktop/internal/embedbin/embed.go` 里写死 `//go:embed handoff`。Windows job 构建要内嵌的 CLI 时，输出文件名仍必须是 `desktop/internal/embedbin/handoff`——哪怕它是个 PE 可执行文件。**改成 `handoff.exe` 会让整个 Windows job 在编译期失败**（go:embed 指向不存在的文件是编译期错误）。释出到磁盘时才补 `.exe`，那是 Task 1 的职责。
- **资产落 `$RUNNER_TEMP`，不落仓库根**。落仓库根会被 job 自己末尾的干净检查判成污染。
- **资产命名 `handoff-desktop_<tag>_windows_amd64.zip`**。前缀 `handoff-desktop_` 不匹配 `handoff_*`（前缀后是 `-` 不是 `_`），这正是选它的理由：`install.sh` 与 agentd 自更新精确拼 `handoff_<tag>_<平台>` 取 CLI 资产，不会误抓薄壳。
- 日志一律用所在包既有的入口（`desktop/internal/shell` 用包级 `logger`，即 `slog.Default()`），**禁止 `fmt.Printf`**。
- 新文件必须有文件头注释（职责 + 边界），导出函数必须有 doc 注释（参数、返回、注意事项）。

---

### Task 1: 平台自适应的 CLI 落点

薄壳现在有两处硬编码 `~/.local/bin/handoff`：`desktop/main.go:268`（释出落点）与 `desktop/internal/shell/binpath.go:40`（查找候选）。在 Windows 上这两处都是错的——那里既没有 `.exe` 后缀，`~/.local/bin` 也不在 PATH 上。正确落点必须与 `install.ps1` 逐字对齐：`%LOCALAPPDATA%\Programs\handoff\handoff.exe`（见 `install.ps1` 的 `Get-HandoffInstallDir`）。

对不上的后果不是报错，是「桌面端用的 handoff 和命令行敲的是两个版本」——这一类错配最难排查，也正是当初把释出落点定成「与 install.sh 同一路径」的理由。

**Files:**
- Create: `desktop/internal/shell/clipath.go`
- Create: `desktop/internal/shell/clipath_test.go`
- Modify: `desktop/internal/shell/binpath.go:32-49`
- Modify: `desktop/main.go:262-274`

**Interfaces:**
- Produces: `shell.DefaultCLIPath() (string, error)` —— 返回本平台约定的 handoff CLI 绝对路径。Task 2、Task 3 不消费它。
- Produces（包内私有）: `cliPathFor(goos, home, localAppData string) (string, error)` —— 上面那个的纯函数内核，供测试在任意宿主上穷举三平台。

- [ ] **Step 1: 写失败的测试**

创建 `desktop/internal/shell/clipath_test.go`：

```go
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
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd desktop && go test ./internal/shell/ -run TestCLIPathFor -v`
Expected: FAIL，编译错误 `undefined: cliPathFor`

- [ ] **Step 3: 写最小实现**

创建 `desktop/internal/shell/clipath.go`：

```go
// 本文件回答一个问题：这台机器上，handoff CLI 该装在哪、又该去哪找。
//
// 职责：
//   - DefaultCLIPath 给出本平台约定的 CLI 绝对路径，供「释出落点」与
//     「查找候选」两处共用同一个答案。
//
// 边界：
//   - **只算路径，不碰盘**。不判断存在性、不创建目录、不写文件——存在性校验
//     在 binpath.go 的 resolveOne，落盘在 release.go 的 ReleaseBinary。
//   - 不做「找一个能用的 handoff」这件事，那是 ResolveBinPath 的职责；本文件
//     只提供它的第一个候选。
//
// 为什么单独成文件：这个落点是**跨平台契约**，必须与安装脚本逐字对齐——
// Unix 侧 install.sh 装到 ~/.local/bin/handoff，Windows 侧 install.ps1 的
// Get-HandoffInstallDir 装到 %LOCALAPPDATA%\Programs\handoff。两边对不上的
// 后果不是报错，是「桌面端用的 handoff 和命令行敲的是两个版本」，属最难
// 排查的一类错配。契约集中在一处，改的时候才有唯一的地方可改。
package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultCLIPath 返回本平台约定的 handoff CLI 绝对路径。
//
// 返回：
//   - 绝对路径。Windows 为 %LOCALAPPDATA%\Programs\handoff\handoff.exe，
//     其余平台为 ~/.local/bin/handoff
//   - error：路径的两个来源（用户主目录、LOCALAPPDATA）都取不到时报错。
//     **绝不返回半截路径或相对路径**——调用方会拿它当释出落点直接写盘，
//     相对路径会把 CLI 写进进程的当前工作目录。
//
// 注意：
//   - **不保证该路径存在**，也不创建它的父目录。调用方要么把它交给
//     ReleaseBinary（对已存在的目标一律报错，绝不覆盖），要么当候选交给
//     resolveOne 做存在性校验。
func DefaultCLIPath() (string, error) {
	// UserHomeDir 失败在 Windows 上不是终局：LOCALAPPDATA 还在的话照样算得出。
	// 所以这里不提前返回，把「两个来源都没有」的判断统一交给 cliPathFor。
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warn("取不到用户主目录，改判 LOCALAPPDATA", "cause", err)
	}
	path, err := cliPathFor(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"))
	if err != nil {
		logger.Error("算不出 handoff CLI 的约定落点", "goos", runtime.GOOS, "cause", err)
		return "", err
	}
	logger.Debug("handoff CLI 约定落点", "goos", runtime.GOOS, "path", path)
	return path, nil
}

// cliPathFor 是 DefaultCLIPath 的纯函数内核。
//
// 参数：
//   - goos: 目标平台，取值同 runtime.GOOS
//   - home: 用户主目录，取不到时传空串
//   - localAppData: Windows 的 %LOCALAPPDATA%，非 Windows 或未设置时传空串
//
// 返回：
//   - 拼好的绝对路径
//   - error：该平台所需的来源全部为空
//
// 注意：
//   - 拆成纯函数是为了让三平台的落点能在**任意宿主**上穷举测试。让它读
//     runtime.GOOS 与环境变量的话，Windows 分支就只能在 Windows 上验，
//     而那正是这个仓库反复栽跟头的地方。
func cliPathFor(goos, home, localAppData string) (string, error) {
	if goos == "windows" {
		base := localAppData
		if base == "" {
			if home == "" {
				return "", errors.New("Windows 上算不出 CLI 落点：LOCALAPPDATA 未设置且取不到用户主目录")
			}
			// %LOCALAPPDATA% 缺失时按 Windows 的固定布局从 home 推。
			// 精简过的服务账户环境变量表里确实会缺这一项，直接失败等于
			// 让薄壳在那种机器上永远释出不了 CLI。
			base = filepath.Join(home, "AppData", "Local")
		}
		// Programs\handoff 与 install.ps1 的 Get-HandoffInstallDir 逐字一致，
		// 文件名带 .exe：Windows 上没有扩展名的文件既不能双击也不能被
		// CreateProcess 拉起，而这个错误要到 agentd 托管失败时才显形。
		return filepath.Join(base, "Programs", "handoff", "handoff.exe"), nil
	}
	if home == "" {
		return "", fmt.Errorf("%s 上算不出 CLI 落点：取不到用户主目录", goos)
	}
	// 与 install.sh 同一路径。localAppData 在这里被**刻意忽略**：某些环境
	// （Wine、部分 CI 镜像）会在 Unix 上也设这个变量，跟着它走会把 CLI
	// 释出到一个谁都找不到的地方。
	return filepath.Join(home, ".local", "bin", "handoff"), nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestCLIPathFor -v`
Expected: 7 条全 PASS

- [ ] **Step 5: 把两处硬编码换掉**

改 `desktop/internal/shell/binpath.go`。原文：

```go
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		if home, err := os.UserHomeDir(); err != nil {
			slog.Warn("取不到用户主目录，跳过 ~/.local/bin/handoff 候选", "cause", err)
		} else {
			candidates = append(candidates, filepath.Join(home, ".local", "bin", "handoff"))
		}
```

改成：

```go
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		// 第一候选是本平台的约定落点（Unix 是 ~/.local/bin/handoff，
		// Windows 是 %LOCALAPPDATA%\Programs\handoff\handoff.exe）。
		// 顺序承重：它必须排在 PATH 之前，否则用户 PATH 上另有一个旧版
		// handoff 时，薄壳会挑中那个旧的去托管 agentd。
		if p, err := DefaultCLIPath(); err != nil {
			slog.Warn("算不出约定落点，跳过该候选", "cause", err)
		} else {
			candidates = append(candidates, p)
		}
```

改完后 `binpath.go` 里若 `os` 或 `filepath` 已无其它使用者，删掉对应 import；`filepath` 仍被 `resolveOne` 的 `filepath.EvalSymlinks` / `filepath.Abs` 使用，**不要删它**。

改 `desktop/main.go`。原文：

```go
		home, err := os.UserHomeDir()
		if err != nil {
			logger.Error("取不到用户主目录，无法释出，不阻断向导", "cause", err)
			return
		}
		dst := filepath.Join(home, ".local", "bin", "handoff")
		if err := shell.ReleaseBinary(dst, rc); err != nil {
```

改成：

```go
		dst, err := shell.DefaultCLIPath()
		if err != nil {
			logger.Error("算不出 CLI 落点，无法释出，不阻断向导", "cause", err)
			return
		}
		if err := shell.ReleaseBinary(dst, rc); err != nil {
```

`main.go` 里 `os` 与 `filepath` 若因此不再被使用，删掉对应 import；被别处用着就留下。判据是 `go build` 能过。

- [ ] **Step 6: 补关键节点日志**

上面 Step 3 的实现里已含：`DefaultCLIPath` 入口取不到 home 时 `logger.Warn`（带 cause）、算不出落点时 `logger.Error`（带 goos + cause）、成功路径 `logger.Debug` 打出最终 path。**成功路径这条不许省**——释出落点错了是最难排查的一类问题，日志里没有「我算出来的是哪个路径」就只能靠猜。

`main.go` 的释出成功日志 `logger.Info("已释出内嵌 handoff 二进制", "dst", dst)` 已在基线上，保持不动。

确认 Step 5 改完后，`binpath.go` 里跳过候选的那条 `slog.Warn` 仍带 `cause`。

- [ ] **Step 7: 跑全量测试**

Run: `cd desktop && go test ./... -count=1` 与 `cd .. && go test ./... -count=1`
Expected: 两个模块都 0 FAIL

Run: `cd desktop && GOOS=windows GOARCH=amd64 go build -tags production ./...`
Expected: 退出 0（这条专门防 Windows 分支写出只在 Unix 上编得过的代码）

- [ ] **Step 8: gofmt 与 vet**

Run: `cd desktop && gofmt -l . | grep -v frontend` 与 `go vet ./...`
Expected: 两条都无输出

- [ ] **Step 9: Commit**

```bash
git add desktop/internal/shell/clipath.go desktop/internal/shell/clipath_test.go desktop/internal/shell/binpath.go desktop/main.go
git commit -m "feat(desktop): CLI 落点按平台走，Windows 对齐 install.ps1

薄壳原有两处硬编码 ~/.local/bin/handoff。Windows 上这两处都是错的：
那里没有 .exe 后缀、~/.local/bin 也不在 PATH 上，释出的 CLI 既双击不起
也不会被 CreateProcess 拉起。

收敛成 shell.DefaultCLIPath()，Windows 落 %LOCALAPPDATA%\\Programs\\handoff\\
handoff.exe——与 install.ps1 的 Get-HandoffInstallDir 逐字一致。对不上的
后果不是报错，是「桌面端用的 handoff 和命令行敲的是两个版本」。

内核拆成纯函数 cliPathFor(goos, home, localAppData)，三平台落点能在任意
宿主上穷举测试；期望值用 filepath.Join 拼而不写字面分隔符（分隔符跟宿主
走，不跟参数里的 goos 走）。"
```

---

### Task 2: release.yml 的 build-desktop-windows job

**Files:**
- Modify: `.github/workflows/release.yml`（在 `build-desktop-darwin` job 之后、`release` job 之前插入新 job；并扩 `release` 的 `needs`）

**Interfaces:**
- Consumes: 无（不依赖 Task 1 的代码，两者可并行审查）
- Produces: artifact `handoff-desktop_windows`，内含 `handoff-desktop_<tag>_windows_amd64.zip`

- [ ] **Step 1: 插入新 job**

在 `.github/workflows/release.yml` 里 `build-desktop-darwin` job 结束之后、`  release:` 之前，插入：

```yaml
  # 薄壳的 windows 资产。放在 windows runner 上而不是从 Linux 交叉编译：
  # wails3 的 generate:syso 要把图标与清单编进资源，且 windows/Taskfile.yml
  # 里 `powershell Remove-item *.syso` 与 `rm -f *.syso` 是按 platforms 分支的
  # ——原生跑才走到 Windows 那条，交叉编译时走的是另一条、等于没验。
  #
  # 只发 zip，不发 NSIS 安装器：未签名的安装器会撞 SmartScreen，还要多引入
  # makensis 与 webview2 引导器两个依赖；zip 里就是一个可直接双击的 exe，
  # 与 darwin 的 .zip 同形态。build/windows/nsis/ 留在仓库里是为了本地
  # `wails3 task windows:package` 能跑，**CI 不跑它**。
  build-desktop-windows:
    needs: verify
    runs-on: windows-latest
    defaults:
      run:
        shell: bash
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@v4
        with:
          node-version: '24'
          cache: npm
          cache-dependency-path: web/package-lock.json

      # Windows 不需要 gtk3 那类构建标签（那是 linux 上 webkit2gtk 的事）
      - name: 装 wails3
        run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.8

      # 输出文件名必须是 handoff（无扩展名）——embed.go 里写死 //go:embed handoff。
      # 改成 handoff.exe 会让下一步在编译期直接失败。它是不是 PE 可执行文件与
      # 文件名无关，释出到磁盘时才补 .exe（见 shell.DefaultCLIPath）。
      - name: 构建要内嵌的 handoff
        env:
          CGO_ENABLED: '0'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          npm --prefix web ci
          npm --prefix web run build
          test -f web/dist/index.html
          rm -rf internal/webui/dist
          cp -R web/dist internal/webui/dist
          go build -trimpath -tags embedweb \
            -ldflags "-s -w -X github.com/Xsxdot/handoff/internal/buildinfo.releaseVersion=${TAG}" \
            -o desktop/internal/embedbin/handoff .

      # ARCH=amd64 必须显式传：不传时 wails3 取宿主架构，runner 换成 arm64
      # 的那天会静默产出一个名字叫 amd64、实际是 arm64 的资产。
      #
      # 变量名承重：Taskfile 只认 EXTRA_TAGS / EXTRA_LDFLAGS，传 GO_FLAGS 会被
      # 静默忽略，编出一个 embedbin.Available() 走 stub、根本不含内嵌 CLI 的
      # 薄壳，而这要到用户双击之后才暴露。
      - name: 构建薄壳
        working-directory: desktop
        env:
          CGO_ENABLED: '0'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          npm --prefix frontend ci
          wails3 task windows:build \
            ARCH=amd64 \
            EXTRA_TAGS=embedbin \
            EXTRA_LDFLAGS="-X=github.com/Xsxdot/handoff/desktop/internal/embedbin.Version=${TAG}"

      # 落点是 RUNNER_TEMP 而不是仓库根：资产是产物，留在工作树里会被下一步的
      # 干净检查当成污染。
      - name: 打包为发布资产
        shell: pwsh
        run: |
          $ErrorActionPreference = 'Stop'
          $tag = $env:GITHUB_REF_NAME
          $exe = 'desktop/bin/handoff-desktop.exe'
          if (-not (Test-Path $exe)) { throw "薄壳产物不存在：$exe" }
          $out = Join-Path $env:RUNNER_TEMP "handoff-desktop_${tag}_windows_amd64.zip"
          Compress-Archive -Path $exe -DestinationPath $out -Force
          Get-Item $out | Format-List Name, Length

      - name: 确认工作区干净
        run: |
          set -euo pipefail
          out="$(git status --porcelain)"
          test -z "$out" || { echo "构建污染了工作区："; echo "$out"; exit 1; }

      - uses: actions/upload-artifact@v4
        with:
          name: handoff-desktop_windows
          path: ${{ runner.temp }}/handoff-desktop_*.zip
          if-no-files-found: error
```

- [ ] **Step 2: 扩 release 的 needs**

把 `release` job 的这一行：

```yaml
    needs: [build-unix, build-darwin, build-desktop-linux, build-desktop-darwin]
```

改成：

```yaml
    needs: [build-unix, build-darwin, build-desktop-linux, build-desktop-darwin, build-desktop-windows]
```

并把它上面那段注释里的「四个构建 job」改成「五个构建 job」。

`release` job 的其余部分**不用改**：`download-artifact` 用的是 `merge-multiple: true`（自动收全部 artifact），`sha256sum ... handoff-desktop_*` 与 `gh release create ... dist/handoff-desktop_*` 都是通配，新资产天然被收进去。

- [ ] **Step 3: 校验 YAML 仍合法**

Run: `python3 -c "import yaml,sys; d=yaml.safe_load(open('.github/workflows/release.yml')); print(sorted(d['jobs'].keys()))"`
Expected: 输出里含 `build-desktop-windows`，且 `release` 在列

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci(release): 增 build-desktop-windows，补齐三平台桌面资产

形态照抄 build-desktop-linux，三处 Windows 特有的差别写在注释里：
不需要 gtk3 标签；ARCH=amd64 必须显式传（不传取宿主架构，runner 换代
会静默产出名实不符的资产）；打包用 pwsh 的 Compress-Archive。

只发 zip 不发 NSIS 安装器：未签名安装器会撞 SmartScreen，还要多引入
makensis 与 webview2 引导器；zip 里是可直接双击的 exe，与 darwin 同形。"
```

---

### Task 3: 契约测试跟上「三个薄壳 job」

`release_workflow_test.go` 的 `TestDesktopJobsCarryLoadBearingFlags` 现在按「两个薄壳 job」写死了出现次数。不改它，新 job 漏传 `EXTRA_TAGS` 也照样绿。

**Files:**
- Modify: `release_workflow_test.go:336-395`

**Interfaces:**
- Consumes: Task 2 产出的 `build-desktop-windows` job

- [ ] **Step 1: 改测试（先改期望，让它红）**

在 `TestDesktopJobsCarryLoadBearingFlags` 里，`build-desktop-darwin` 那段断言之后、`wf := stripYAMLComments(...)` 之前，插入：

```go
	if win, ok := jobs["build-desktop-windows"]; !ok {
		t.Fatal("release.yml 缺 build-desktop-windows job")
	} else if !strings.HasPrefix(win.RunsOn, "windows") {
		t.Fatalf("windows 薄壳必须在 Windows runner 上构建（generate:syso 与 "+
			"Taskfile 里按 platforms 分支的清理命令只有原生跑才走到），实得 runs-on=%q", win.RunsOn)
	}
```

把三条计数从 2 改成 3：

```go
		{"EXTRA_TAGS=embedbin", 3},
		{"EXTRA_LDFLAGS=", 3},
		{"desktop/internal/embedbin.Version=", 3},
```

再往那张表里加一条 Windows 专属的：

```go
		// ARCH=amd64 不传的话 wails3 取宿主架构，runner 换代那天会静默产出一个
		// 名字叫 amd64、实际是 arm64 的资产——CI 全绿，用户下载后才发现跑不起来。
		{"ARCH=amd64", 1},
```

把 needs 断言那个循环的列表补上新 job：

```go
	for _, dep := range []string{"build-desktop-linux", "build-desktop-darwin", "build-desktop-windows"} {
```

- [ ] **Step 2: 跑测试确认它先红后绿**

若 Task 2 尚未合入本分支，此步应 FAIL（缺 job）。Task 2 已在时应 PASS。

Run: `go test . -run TestDesktopJobsCarryLoadBearingFlags -v`
Expected: PASS（Task 2 已完成的前提下）

- [ ] **Step 3: 变异复验——这道门是不是假门**

按仓库房规（B86 立的规矩），新写的门必须用变异证明它抓得住。**逐条做，每条做完把文件还原再做下一条。还原用 `git checkout --` 之前先确认工作区已提交，否则会连未提交的改动一起抹掉。**

四条变异，每条都必须让测试**变红**：

1. 把 `.github/workflows/release.yml` 里 windows job 的 `EXTRA_TAGS=embedbin` 改成 `GO_FLAGS=embedbin` → 期望红（计数 3→2）
2. 把 windows job 的 `ARCH=amd64 \` 整行删掉 → 期望红
3. 把 `release` 的 needs 里 `build-desktop-windows` 删掉 → 期望红
4. 把 windows job 的 `runs-on: windows-latest` 改成 `ubuntu-latest` → 期望红

任何一条变异后测试仍绿，说明那条断言是假门，**必须当场改到能抓住**再继续。

Run（每条变异后）: `go test . -run TestDesktopJobsCarryLoadBearingFlags`

- [ ] **Step 4: 跑全量测试**

Run: `go test ./... -count=1` 与 `gofmt -l . | grep -v -e '^web/' -e '^desktop/frontend'` 与 `go vet ./...`
Expected: 0 FAIL、gofmt 无输出、vet 无输出

- [ ] **Step 5: Commit**

```bash
git add release_workflow_test.go
git commit -m "test(release): 契约门跟上第三个薄壳 job

原门按两个薄壳 job 写死出现次数，新增 windows job 漏传 EXTRA_TAGS 也照样
绿。三条计数 2→3，另加 ARCH=amd64 与 runs-on 前缀两条 windows 专属断言。

四条变异逐条复验过（GO_FLAGS 替换、删 ARCH、删 needs、换 runs-on），
每条都能让测试变红。"
```

---

## 不在本计划范围内（由协调者本地执行，不要派发）

以下步骤需要驱动 handoff 自身或真实发版，**执行者不要做，也不要尝试调用 handoff CLI**：

- 打 tag 触发真实流水线（`v0.3.0-rc4`）并盯三个桌面 job 的结果
- 在 win-b37 真机上验证释出落点与薄壳启动
- CHANGELOG 与 backlog 的收口记账
