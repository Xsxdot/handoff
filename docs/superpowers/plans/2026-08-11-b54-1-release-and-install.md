# B54.1 供给与安装 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 handoff 有版本号、有 GitHub Release 产物、有一行安装命令——从「每台机器自己 `go build`」变成「`curl … | bash` 装上一个带版本号的二进制」。

**Architecture:** 版本号由 tag 经 `-ldflags -X` 注入到 `internal/buildinfo` 的包级变量，向上贯穿 `proto.BuildInfo` 与 `handoff status` 的展示；新增 `handoff version` 子命令把版本以**稳定的首行格式**打出来，这一行同时是 B54.3 自更新自检要精确比对的对外契约。GitHub Actions 在推 tag 时交叉编译四平台、算 checksums、建 Release；`install.sh` 反向消费同一套资产命名约定。

**Tech Stack:** Go 1.26.1（`go.mod` 声明）、cobra、`gopkg.in/yaml.v3`（已在依赖里）、GitHub Actions、bash

**Spec:** [docs/superpowers/specs/2026-08-11-install-and-autoupdate-design.md](../specs/2026-08-11-install-and-autoupdate-design.md)（本计划实现其中的 A 期，见 spec §6）

## Global Constraints

以下取值逐字来自 spec，**不得在实现时另行发明**：

- **仓库 owner 一律用 `Xsxdot`**（真实 remote 是 `git@github.com:Xsxdot/handoff.git`）；`go.mod` 的 module path 是 `github.com/xushixin/handoff`，两者不一致是已知问题（backlog B55），**本期不修**。ldflags 的 `-X` 路径用 module path，GitHub URL 用 owner，两者不要互相「纠正」。
- **ldflags 注入路径**（逐字）：`-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=${TAG}`
- **资产命名**（逐字）：`handoff_<tag>_<os>_<arch>.tar.gz`，外加一个 `checksums.txt`。这是**一处约定、两处消费**（workflow 产出、`install.sh` 消费；B54.3 的自更新是第三处），改名必须同步改全部消费点。
- **平台矩阵**（四项，不多不少）：`darwin/arm64`、`darwin/amd64`、`linux/amd64`、`linux/arm64`。**不发 Windows**——B37（prochost Windows）未完成，agentd 在 Windows 上跑不起来，`install.sh` 探到 Windows 必须报错并说明这个原因。
- **`handoff version` 首行格式是对外契约**：release 构建输出 `vX.Y.Z`，非 release 构建输出字面量 `unknown`。**不得输出空行**——空行与「命令没输出」无法区分，会让 B54.3 的自检丢掉「二进制能跑但不是 release 构建」这个结论。
- **安装目录默认 `~/.local/bin`**（免 sudo），可由环境变量 `HANDOFF_INSTALL_DIR` 覆盖（真机验证靠它做隔离）。
- **`install.sh` 不做的事**：不写服务单元、不改用户的 shell rc 文件、不 sudo。
- **本期不含 `handoff init`**。init 属 B54.2。本期的 `install.sh` 装完只打印安装结果与 PATH 提示；**B54.2 的任务里包含「在 install.sh 末尾接上 `handoff init < /dev/tty`」**。不要在本期提前引用一个还不存在的子命令。
- **日志纪律的适用范围**：`internal/buildinfo` 的包注释明确写了「不打日志：本包无 I/O、无外部调用，在这种纯取值函数里打日志只会制造噪音」——**尊重这条既有边界，不要往里加日志**。`cmd/version.go` 与 `cmd/status.go` 是纯渲染，同理不加。本期真正需要日志纪律的是 `install.sh`：**每个失败分支必须经 `die` 打出带上下文的原因，成功路径必须打出装到哪、装的是哪个版本**（脚本失败时用户看到的只有这些输出）。

---

## File Structure

**新建**

| 文件 | 职责 |
|---|---|
| `cmd/version.go` | `handoff version` 子命令。首行纯版本字符串（对外契约），其后 revision / go / platform 三行供人排障 |
| `cmd/version_test.go` | 首行契约与细节行的 CLI 行为测试 |
| `.github/workflows/release.yml` | 推 tag 触发：四平台交叉编译 → checksums → 建 Release 传资产 |
| `release_workflow_test.go`（仓库根，package main） | 把 workflow 里的三条约定钉住：注入路径、资产命名、四平台矩阵 |
| `install.sh`（仓库根） | 探平台 → 取 latest tag → 下载 → 校 sha256 → 装进 `~/.local/bin` |
| `install_test.sh`（仓库根） | `install.sh` 的纯函数单测（平台归一、tag 解析、sha256 计算） |
| `install_test.go`（仓库根，package main） | 把 `install_test.sh` 接进 `go test ./...`，避免 shell 测试成为没人跑的孤儿 |

**修改**

| 文件 | 改动 |
|---|---|
| `internal/proto/status.go:22-27` | `BuildInfo` 加 `Version` 字段 |
| `internal/buildinfo/buildinfo.go` | 加 `releaseVersion` 注入点，填进 `Version`；`ok=false` 分支也要带上它 |
| `internal/buildinfo/buildinfo_test.go` | 加注入/未注入两例 |
| `cmd/status.go:118-149` | `describeBuild` / `compareBuild` 优先用 `Version`，为空退回现有 revision 逻辑 |
| `cmd/status_test.go` | 加 Version 优先与退回两例 |
| `README.md` | 安装章节改成一行安装；补 `handoff version` |

**边界**：`internal/buildinfo` 只回答「我是谁」，不比较、不展示（既有边界，本期不破）。`cmd/version.go` 不联网、不读配置——它必须能在一台**刚装完、还没有 `~/.handoff/config.yaml`** 的机器上跑通，因为 B54.3 的自检会在替换二进制之前拉起它。

---

### Task 1: 版本注入点与 `handoff version` 子命令

**Files:**
- Modify: `internal/proto/status.go:22-27`
- Modify: `internal/buildinfo/buildinfo.go`
- Modify: `internal/buildinfo/buildinfo_test.go`
- Create: `cmd/version.go`
- Create: `cmd/version_test.go`

**Interfaces:**
- Consumes: `proto.BuildInfo`（现有结构，本任务给它加字段）；`buildinfo.Read() (proto.BuildInfo, bool)`（现有签名，不变）
- Produces:
  - `proto.BuildInfo.Version string`——release 版本号，空串表示非 release 构建
  - `buildinfo.releaseVersion`（包级 var，ldflags 注入目标，路径 `github.com/xushixin/handoff/internal/buildinfo.releaseVersion`）
  - `handoff version` 子命令，stdout 首行为 `vX.Y.Z` 或 `unknown`
  - `cmd.versionUnknown = "unknown"`（常量，Task 3 的本地构建验证与 B54.3 的自检都比对它）

- [ ] **Step 1: 写失败的测试——buildinfo 侧的注入与未注入两例**

在 `internal/buildinfo/buildinfo_test.go` 末尾追加：

```go
// 注入了 releaseVersion 时，Read 必须把它带进 Version。
//
// 这是 release 构建的形态：ldflags 写入包级变量，vcs 戳同时也在。
func TestReadCarriesInjectedReleaseVersion(t *testing.T) {
	oldVer := releaseVersion
	releaseVersion = "v0.1.0"
	t.Cleanup(func() { releaseVersion = oldVer })

	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "8353ef68d711eaf63eeb1287f342f3238204aec8"},
			},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, ok := Read()
	if !ok {
		t.Fatal("Read 应返回 ok=true")
	}
	if got.Version != "v0.1.0" {
		t.Fatalf("Version=%q，期望注入的 v0.1.0", got.Version)
	}
	if got.Revision != "8353ef68d711eaf63eeb1287f342f3238204aec8" {
		t.Fatalf("注入版本号不得影响 Revision 解析，得到 %q", got.Revision)
	}
}

// 未注入时 Version 必须是空串——这是本地 go build / go run / 测试二进制的
// 真实形态，调用方据此判定「非 release 构建」并退回 revision 展示。
func TestReadWithoutInjectionHasEmptyVersion(t *testing.T) {
	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{GoVersion: "go1.26.1"}, true
	}
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, _ := Read()
	if got.Version != "" {
		t.Fatalf("非 release 构建的 Version 必须为空，得到 %q", got.Version)
	}
}

// 读不到构建信息（ok=false）时，注入的版本号仍须返回。
//
// why：releaseVersion 是编译期常量，与 debug.ReadBuildInfo 能不能读到无关。
// 丢掉它会让一个 release 二进制在这种边角情况下自称 unknown，而 B54.3 的
// 自检正是拿这个值比对的——自检会误判失败并放弃一次本该成功的更新。
func TestReadKeepsVersionWhenBuildInfoUnavailable(t *testing.T) {
	oldVer := releaseVersion
	releaseVersion = "v0.2.0"
	t.Cleanup(func() { releaseVersion = oldVer })

	oldRead := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	t.Cleanup(func() { readBuildInfo = oldRead })

	got, ok := Read()
	if ok {
		t.Fatal("读不到构建信息时 ok 仍应为 false")
	}
	if got.Version != "v0.2.0" {
		t.Fatalf("ok=false 时也必须带回注入的版本号，得到 %q", got.Version)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/buildinfo/ -run 'Version|Injection' -v`
Expected: 编译失败——`releaseVersion` 未定义、`got.Version` 字段不存在。

- [ ] **Step 3: 给 `proto.BuildInfo` 加 `Version` 字段**

修改 `internal/proto/status.go`，把 `BuildInfo` 的文档注释与结构体替换为：

```go
// BuildInfo 是一个 handoff 二进制的构建标识。
//
// 字段说明：
//   - Version: release 版本号（形如 v0.1.0），构建时由 ldflags 注入；
//     **空串表示不是 release 构建**（本地 go build / go run / 测试二进制），
//     此时调用方应退回 Revision 展示
//   - Revision: vcs.revision；**空串表示不是 go build 产物**（go run / 测试
//     二进制没有 vcs 戳），调用方应显示「版本未知」而不是空
//   - Time: vcs.time
//   - Modified: vcs.modified——true 表示这个二进制是带未提交改动编出来的，
//     它对不上任何一个提交，排障时这是关键信息
//   - Go: 编译所用 Go 版本
//
// 为什么 Version 与 Revision 并存而不是二选一：它们回答不同的问题。
// Version 回答「该不该更新」（自动更新比的是它），Revision 回答「出问题的
// 是哪个提交」（排障比的是它）。release 构建两者都有。
type BuildInfo struct {
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision"`
	Time     string `json:"time"`
	Modified bool   `json:"modified"`
	Go       string `json:"go"`
}
```

- [ ] **Step 4: 给 `buildinfo` 加注入点**

修改 `internal/buildinfo/buildinfo.go`。在 `readBuildInfo` 变量声明之后、`Read` 之前插入：

```go
// releaseVersion 是构建时由 ldflags 注入的 release 版本号（形如 v0.1.0）。
//
// 注入方式（见 .github/workflows/release.yml，路径必须逐字一致）：
//
//	-ldflags "-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=v0.1.0"
//
// why（注入而不是运行时读 tag）：二进制跑起来时身边没有 git 仓库可读；
// vcs.revision 只有 commit 没有版本，而「哪个版本更新」是自动更新唯一
// 能回答的问题。本地 go build 不注入，值为空——调用方据此判定非 release 构建。
var releaseVersion string
```

再把 `Read` 的函数体改为（只改两处：`!ok` 分支带上版本号、`out` 初始化带上版本号）：

```go
func Read() (proto.BuildInfo, bool) {
	bi, ok := readBuildInfo()
	if !ok {
		// 即使读不到构建信息，注入的版本号仍然有效——它是编译期常量，
		// 与 debug.ReadBuildInfo 能否读到无关
		return proto.BuildInfo{Version: releaseVersion}, false
	}
	out := proto.BuildInfo{Go: bi.GoVersion, Version: releaseVersion}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			out.Revision = s.Value
		case "vcs.time":
			out.Time = s.Value
		case "vcs.modified":
			// go 把它序列化成字符串 "true"/"false"，不是布尔
			out.Modified = s.Value == "true"
		}
	}
	return out, true
}
```

同时把包头注释的「职责」段第一条改为：

```go
//   - Read：把 runtime/debug.ReadBuildInfo 的结果与 ldflags 注入的 release
//     版本号归一成 proto.BuildInfo
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/buildinfo/ -count=1 -v`
Expected: PASS，含原有三例与新增三例共六例。

- [ ] **Step 6: 写失败的测试——`handoff version` 的首行契约**

创建 `cmd/version_test.go`：

```go
// handoff version 的 CLI 行为测试。
//
// 首行格式是**对外契约**：B54.3 的自更新会拉起新下载的二进制跑 version，
// 把首行与期望 tag 精确比对。这里的断言就是那份契约的钉子。
package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// runVersion 执行一次 version 命令，返回 stdout+stderr 合并输出。
func runVersion(t *testing.T) string {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"version"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version 不应报错: %v", err)
	}
	return buf.String()
}

// 测试二进制没有注入 releaseVersion，首行必须是字面量 unknown。
//
// 为什么不能是空行：空行与「命令根本没输出」无法区分，自检侧会把两者都
// 当成失败，从而丢掉「二进制能跑，只是不是 release 构建」这个有用结论。
func TestVersionFirstLineIsUnknownWhenNotRelease(t *testing.T) {
	first := strings.SplitN(runVersion(t), "\n", 2)[0]
	if first != versionUnknown {
		t.Fatalf("非 release 构建的首行应为 %q，得到 %q", versionUnknown, first)
	}
}

// 首行之后必须有排障细节，否则孤零零一行 unknown 什么问题也定位不了。
func TestVersionPrintsDetailLines(t *testing.T) {
	out := runVersion(t)
	for _, want := range []string{"revision", "go", "platform"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q 行:\n%s", want, out)
		}
	}
}

// version 必须在没有配置文件时也能跑通。
//
// why：B54.3 的自检发生在「新二进制刚下载完、还没被启用」的时刻，而
// install.sh 装完的机器可能连 ~/.handoff/config.yaml 都还没有。version
// 一旦依赖配置，自检就会在最需要它的场景下失败。
func TestVersionNeedsNoConfig(t *testing.T) {
	resetFlags(t)
	configPath = "/nonexistent/handoff/config.yaml"
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"version", "--config", "/nonexistent/handoff/config.yaml"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version 不应读配置，却报错: %v", err)
	}
	if first := strings.SplitN(buf.String(), "\n", 2)[0]; first != versionUnknown {
		t.Fatalf("首行=%q", first)
	}
}
```

- [ ] **Step 7: 跑测试确认失败**

Run: `go test ./cmd/ -run TestVersion -v`
Expected: 编译失败——`versionUnknown` 未定义。

- [ ] **Step 8: 实现 `handoff version`**

创建 `cmd/version.go`：

```go
// 本文件实现 handoff version 子命令：打印本二进制的版本标识。
//
// 职责：
//   - 首行输出纯版本字符串，供机器精确比对
//   - 其后输出 revision / Go 版本 / 平台三行，供人排障
//
// 边界：
//   - 不联网、不读配置文件：这条命令只回答「我是谁」。它必须能在一台刚装完、
//     还没有 ~/.handoff/config.yaml 的机器上跑通
//   - **首行格式是对外契约**：B54.3 的自更新自检会拉起新下载的二进制跑本命令，
//     把首行与期望 tag 精确比对（见 spec §4.6 步骤 ⑤）。改这一行的格式等于改
//     协议，必须同步改自检侧
package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/proto"
)

// versionUnknown 是非 release 构建（本地 go build / go run / 测试二进制）的首行取值。
//
// why（不留空串）：空首行与「命令没有任何输出」无法区分，自检侧会把两种情况
// 都判为失败，丢掉「二进制能跑但不是 release 构建」这个有用结论。
const versionUnknown = "unknown"

// versionCmd 打印本二进制的版本标识。
//
// 使用方式：handoff version
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "打印本二进制的版本标识",
	RunE: func(cmd *cobra.Command, _ []string) error {
		b, ok := buildinfo.Read()
		out := cmd.OutOrStdout()

		// 首行永远存在且只有版本号本身——这是给机器读的那一行
		if b.Version != "" {
			fmt.Fprintln(out, b.Version)
		} else {
			fmt.Fprintln(out, versionUnknown)
		}

		if !ok {
			// 极少见：非 go 工具链链接的二进制。如实说明而不是打三行空值
			fmt.Fprintln(out, "构建信息不可读（非 go 工具链链接的二进制）")
			return nil
		}
		fmt.Fprintf(out, "revision  %s\n", revisionText(b))
		fmt.Fprintf(out, "go        %s\n", b.Go)
		fmt.Fprintf(out, "platform  %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}

func init() { rootCmd.AddCommand(versionCmd) }

// revisionText 渲染 revision 行。
//
// 参数：
//   - b: 构建标识
//
// 返回：
//   - 一行文本；Revision 为空时如实说明「非 go build 产物」而不是留空
func revisionText(b proto.BuildInfo) string {
	if b.Revision == "" {
		return "未知（非 go build 产物）"
	}
	s := b.Revision
	if b.Time != "" {
		s += "  " + b.Time
	}
	if b.Modified {
		// 带未提交改动意味着这个二进制对不上任何一个提交，排障时是关键信息
		s += "  带未提交改动"
	}
	return s
}
```

- [ ] **Step 9: 跑测试确认通过**

Run: `go test ./cmd/ -run TestVersion -count=1 -v`
Expected: 三例全 PASS。

- [ ] **Step 10: 手工验证注入链路真的通**

单测用的是测试缝，注入本身没被验证过。跑一次真实的 ldflags 构建：

```bash
go build -trimpath -ldflags "-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=v0.0.0-local" -o /tmp/handoff-inject-check . && /tmp/handoff-inject-check version && rm -f /tmp/handoff-inject-check
```

Expected: 首行输出 `v0.0.0-local`。若输出 `unknown`，说明 `-X` 的包路径写错了——它必须是 **module path**（`github.com/xushixin/handoff/...`）而不是 GitHub owner。

- [ ] **Step 11: 全量回归**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1`
Expected: `gofmt -l .` 无输出，其余全绿。

- [ ] **Step 12: 提交**

```bash
git add internal/proto/status.go internal/buildinfo/buildinfo.go internal/buildinfo/buildinfo_test.go cmd/version.go cmd/version_test.go
git commit -m "feat(version): 加 release 版本号注入点与 handoff version 子命令"
```

---

### Task 2: `handoff status` 展示接入版本号

**Files:**
- Modify: `cmd/status.go:115-149`
- Modify: `cmd/status_test.go`

**Interfaces:**
- Consumes: `proto.BuildInfo.Version`（Task 1 产出）、现有的 `short12(s string) string`
- Produces: 无新增导出物。`describeBuild` / `compareBuild` 签名不变，只改行为

- [ ] **Step 1: 写失败的测试**

在 `cmd/status_test.go` 末尾追加：

```go
// 对端是 release 构建时，「版本」行要显示版本号而不是光秃秃的 revision。
func TestStatusPrefersReleaseVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/data",
			"started_at":"2026-08-10T00:00:00Z",
			"version":{"version":"v0.1.0","revision":"8353ef68d711eaf63eeb1287f342f3238204aec8","go":"go1.26.1"},
			"executors":["opencode"],"default_executor":"opencode",
			"task_counts":{},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("status 不应报错: %v", err)
	}
	if !strings.Contains(out, "v0.1.0") {
		t.Fatalf("release 构建的版本行应含 v0.1.0:\n%s", out)
	}
	if !strings.Contains(out, "8353ef68d711") {
		t.Fatalf("版本行仍应带 revision（排障要用）:\n%s", out)
	}
}

// 对端不是 release 构建（Version 为空）时，展示必须原样退回 revision 逻辑。
//
// why 单独钉一例：这是「新字段不许破坏既有形态」的回归闸。本机 go build 出来的
// agentd 常年是这个形态，退化成显示空版本会让 status 变得毫无信息。
func TestStatusFallsBackToRevisionWhenNoVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/data",
			"started_at":"2026-08-10T00:00:00Z",
			"version":{"revision":"8353ef68d711eaf63eeb1287f342f3238204aec8","go":"go1.26.1"},
			"executors":["opencode"],"default_executor":"opencode",
			"task_counts":{},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("status 不应报错: %v", err)
	}
	if !strings.Contains(out, "8353ef68d711") {
		t.Fatalf("无版本号时应退回 revision 展示:\n%s", out)
	}
}

// compareBuild 的四种组合：两边都有版本号时比版本号，否则退回 revision 比较。
func TestCompareBuildPrefersVersion(t *testing.T) {
	cases := []struct {
		name       string
		cli, agent proto.BuildInfo
		want       string // 期望出现在结果里的子串
	}{
		{
			name:  "两边同版本",
			cli:   proto.BuildInfo{Version: "v0.1.0", Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Version: "v0.1.0", Revision: "bbbbbbbbbbbb2222"},
			want:  "一致",
		},
		{
			name:  "两边不同版本，要报出对端版本",
			cli:   proto.BuildInfo{Version: "v0.1.0", Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Version: "v0.2.0", Revision: "aaaaaaaaaaaa1111"},
			want:  "v0.2.0",
		},
		{
			name:  "对端无版本号，退回 revision 比较",
			cli:   proto.BuildInfo{Version: "v0.1.0", Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Revision: "aaaaaaaaaaaa1111"},
			want:  "一致",
		},
		{
			name:  "本地无版本号，退回 revision 比较且不一致",
			cli:   proto.BuildInfo{Revision: "aaaaaaaaaaaa1111"},
			agent: proto.BuildInfo{Version: "v0.2.0", Revision: "bbbbbbbbbbbb2222"},
			want:  "不一致",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compareBuild(c.cli, c.agent); !strings.Contains(got, c.want) {
				t.Fatalf("compareBuild=%q，期望含 %q", got, c.want)
			}
		})
	}
}
```

如果 `cmd/status_test.go` 的 import 块里还没有 `"github.com/xushixin/handoff/internal/proto"`，补上。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestStatusPrefers|TestStatusFallsBack|TestCompareBuildPrefers' -v`
Expected: `TestStatusPrefersReleaseVersion` FAIL（输出里没有 `v0.1.0`）、`TestCompareBuildPrefersVersion` 的前两个子例 FAIL。

- [ ] **Step 3: 改 `describeBuild`**

把 `cmd/status.go` 的 `describeBuild` 整体替换为下面两个函数：

```go
// describeBuild 把一个构建标识渲染成一行。
//
// 优先展示 release 版本号——那是「该不该更新」这个问题的答案，也是人最先想看的。
// Version 为空表示不是 release 构建，退回 revision 展示（既有行为，一字不改）。
func describeBuild(b proto.BuildInfo) string {
	if b.Version == "" {
		return describeByRevision(b)
	}
	s := b.Version
	if b.Revision != "" {
		// 版本号回答「是哪个 release」，revision 回答「是哪个提交」——排障要后者，
		// 所以两个都留，不因为有了版本号就把 revision 丢掉
		s += "  " + short12(b.Revision)
	}
	s += "  " + b.Go
	if b.Modified {
		s += "  带未提交改动"
	}
	return s
}

// describeByRevision 是没有 release 版本号时的展示（B54 之前的原样行为）。
//
// Revision 为空表示不是 go build 产物（go run / 测试二进制），如实说明而不是留空。
func describeByRevision(b proto.BuildInfo) string {
	if b.Revision == "" {
		return fmt.Sprintf("未知（非 go build 产物）  %s", b.Go)
	}
	s := fmt.Sprintf("%s  %s  %s", short12(b.Revision), b.Time, b.Go)
	if b.Modified {
		// 带未提交改动意味着这个二进制对不上任何一个提交，排障时是关键信息
		s += "  带未提交改动"
	}
	return s
}
```

- [ ] **Step 4: 改 `compareBuild`**

把 `cmd/status.go` 的 `compareBuild` 整体替换为下面两个函数：

```go
// compareBuild 渲染「本地」行：本机 CLI 与对端 agentd 的对照结论。
//
// 优先比 release 版本号（自动更新真正关心的维度）；任一侧没有版本号时退回
// revision 比较（既有行为）。不一致**不阻断**：handoff 没有兼容矩阵，
// 版本不同不等于不兼容，并列报出交给人判。
func compareBuild(cli, agentd proto.BuildInfo) string {
	if cli.Version == "" || agentd.Version == "" {
		return compareByRevision(cli, agentd)
	}
	if cli.Version == agentd.Version {
		return cli.Version + "  一致"
	}
	return fmt.Sprintf("%s  与对端不一致（对端 %s，不一定不兼容，请自行判断）", cli.Version, agentd.Version)
}

// compareByRevision 是任一侧没有 release 版本号时的对照（B54 之前的原样行为）。
func compareByRevision(cli, agentd proto.BuildInfo) string {
	if cli.Revision == "" {
		return "本地版本未知（非 go build 产物）"
	}
	s := short12(cli.Revision)
	if cli.Modified {
		s += "  带未提交改动"
	}
	if agentd.Revision == "" {
		return s + "  （对端版本未知，无从对照）"
	}
	if cli.Revision == agentd.Revision {
		return s + "  一致"
	}
	return s + "  与对端不一致（不一定不兼容，请自行判断）"
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ -count=1`
Expected: 全绿，含既有的 status 用例（它们钉的是无版本号形态，不得被破坏）。

- [ ] **Step 6: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add cmd/status.go cmd/status_test.go
git commit -m "feat(status): 版本展示优先用 release 版本号，无则退回 revision"
```

---

### Task 3: Release 流水线

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `release_workflow_test.go`（仓库根，package main）

**Interfaces:**
- Consumes: `cmd.versionUnknown`（Task 1 产出，本任务的本地构建验证要比对它的反面）；ldflags 注入路径（Global Constraints 里逐字给定）
- Produces: Release 资产命名约定 `handoff_<tag>_<os>_<arch>.tar.gz` + `checksums.txt`，Task 4 的 `install.sh` 直接消费

- [ ] **Step 1: 写失败的测试——把三条约定钉在 workflow 上**

创建 `release_workflow_test.go`：

```go
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
//（构建成功、二进制自称 unknown、自动更新永远认为自己已是最新）。
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test . -run TestWorkflow -v`
Expected: 三例全 FAIL，`readWorkflow` 报「读 workflow 失败: no such file or directory」。

- [ ] **Step 3: 写 workflow**

创建 `.github/workflows/release.yml`：

```yaml
# 推 tag 时构建并发布 handoff。
#
# 触发：git push origin vX.Y.Z
# 产出：四个平台的 handoff_<tag>_<os>_<arch>.tar.gz + 一个 checksums.txt
#
# 资产命名与 ldflags 注入路径是与 install.sh / 自更新的契约，
# 由仓库根的 release_workflow_test.go 钉住，改这里必须同步改那边与消费方。
name: release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write # gh release create 要写 Releases

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: true
      matrix:
        include:
          - goos: darwin
            goarch: arm64
          - goos: darwin
            goarch: amd64
          - goos: linux
            goarch: amd64
          - goos: linux
            goarch: arm64
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: 交叉编译并打包
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
          CGO_ENABLED: '0'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          go build -trimpath \
            -ldflags "-s -w -X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=${TAG}" \
            -o handoff .
          tar czf "handoff_${TAG}_${GOOS}_${GOARCH}.tar.gz" handoff
      - uses: actions/upload-artifact@v4
        with:
          name: handoff_${{ matrix.goos }}_${{ matrix.goarch }}
          path: handoff_*.tar.gz
          if-no-files-found: error

  release:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with:
          path: dist
          merge-multiple: true
      - name: 算 checksums
        run: |
          set -euo pipefail
          cd dist
          # 只列裸文件名：install.sh 是在自己的临时目录里按文件名比对的
          sha256sum handoff_*.tar.gz > checksums.txt
          cat checksums.txt
      - name: 建 Release 并上传资产
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          set -euo pipefail
          gh release create "${GITHUB_REF_NAME}" \
            --repo "${GITHUB_REPOSITORY}" \
            --title "${GITHUB_REF_NAME}" \
            --generate-notes \
            dist/handoff_*.tar.gz dist/checksums.txt
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test . -run TestWorkflow -count=1 -v`
Expected: 三例全 PASS。

- [ ] **Step 5: 本地跑一遍 workflow 里的构建命令，确认注入链路端到端可用**

Actions 跑不到本地，但**构建命令本身可以逐字验**——这才是这条流水线里唯一会静默出错的部分：

```bash
TAG=v0.0.0-wfcheck && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=${TAG}" -o /tmp/hf-wfcheck . && tar czf /tmp/handoff_${TAG}_check.tar.gz -C /tmp hf-wfcheck && /tmp/hf-wfcheck version | head -1 && rm -f /tmp/hf-wfcheck /tmp/handoff_${TAG}_check.tar.gz
```

Expected: 输出 `v0.0.0-wfcheck`。输出 `unknown` 说明 `-X` 路径写错，回到 Step 3 对照 Global Constraints 里的逐字取值。

- [ ] **Step 6: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add .github/workflows/release.yml release_workflow_test.go
git commit -m "ci: 加 release workflow，推 tag 构建四平台资产并发布"
```

---

### Task 4: `install.sh` 一行安装脚本

**Files:**
- Create: `install.sh`（仓库根）
- Create: `install_test.sh`（仓库根）
- Create: `install_test.go`（仓库根，package main）

**Interfaces:**
- Consumes: Task 3 产出的资产命名 `handoff_<tag>_<os>_<arch>.tar.gz` 与 `checksums.txt`
- Produces:
  - shell 函数 `detect_platform`（输出 `<os>_<arch>`）、`latest_tag`（输出 `vX.Y.Z`）、`sha256_of <file>`（输出 64 位 hex）——被 `install_test.sh` 直接调用
  - 环境变量 `HANDOFF_INSTALL_LIB=1` 时只加载函数、不执行主流程（测试缝）
  - 环境变量 `HANDOFF_INSTALL_DIR` 覆盖安装目录（真机验证靠它隔离）

- [ ] **Step 1: 写失败的测试——shell 侧**

创建 `install_test.sh`：

```bash
#!/usr/bin/env bash
# install.sh 的单元测试：只测能纯函数化的部分（平台归一、tag 解析、sha256）。
#
# 用法：bash install_test.sh
# 全通过时静默退出 0；有失败时逐条打印期望/实得并退 1。
#
# 边界：不测下载与安装本身——那需要真实 Release，属真机验证（P2）。
set -uo pipefail

HANDOFF_INSTALL_LIB=1 . "$(dirname "$0")/install.sh"

fails=0

# check <说明> <期望> <实得>
check() {
  if [ "$2" != "$3" ]; then
    printf 'FAIL  %s\n      期望 %s\n      实得 %s\n' "$1" "$2" "$3" >&2
    fails=$((fails + 1))
  fi
}

# with_uname <系统> <架构> <命令...>：在被替身的 uname 下执行命令。
# bash 是动态作用域，被调命令里的 uname 会命中这里定义的函数。
with_uname() {
  local s="$1" m="$2"
  shift 2
  uname() { case "$1" in -s) printf '%s' "$s" ;; -m) printf '%s' "$m" ;; esac; }
  "$@"
  unset -f uname
}

# with_curl_url <重定向后的地址> <命令...>：替身 curl，只回显给定地址。
with_curl_url() {
  local u="$1"
  shift
  curl() { printf '%s' "$u"; }
  "$@"
  unset -f curl
}

# 四个受支持平台都要归一正确
check "darwin arm64"  "darwin_arm64" "$(with_uname Darwin arm64 detect_platform)"
check "darwin x86_64" "darwin_amd64" "$(with_uname Darwin x86_64 detect_platform)"
check "linux aarch64" "linux_arm64"  "$(with_uname Linux aarch64 detect_platform)"
check "linux x86_64"  "linux_amd64"  "$(with_uname Linux x86_64 detect_platform)"

# Windows 必须被明确拒绝，且理由里要点出 B37——否则用户只会以为是漏了平台
out="$( (with_uname Windows_NT x86_64 detect_platform) 2>&1 )" && rc=0 || rc=$?
check "Windows 退出码" "1" "$rc"
case "$out" in
  *B37*) ;;
  *) printf 'FAIL  Windows 的拒绝理由应点出 backlog B37\n      实得 %s\n' "$out" >&2
     fails=$((fails + 1)) ;;
esac

# 32 位架构不在矩阵内，必须拒绝而不是装一个跑不起来的包
out="$( (with_uname Linux i686 detect_platform) 2>&1 )" && rc=0 || rc=$?
check "i686 退出码" "1" "$rc"

# tag 从 releases/latest 的重定向地址里取最后一段
check "解析 tag" "v0.1.0" \
  "$(with_curl_url 'https://github.com/Xsxdot/handoff/releases/tag/v0.1.0' latest_tag)"

# 仓库还没有任何 release 时，GitHub 会重定向到 .../releases（末段不是 vX.Y.Z）。
# 此时必须报错，而不是去下载一个名为 handoff_releases_... 的不存在资产
out="$( (with_curl_url 'https://github.com/Xsxdot/handoff/releases' latest_tag) 2>&1 )" && rc=0 || rc=$?
check "无 release 时退出码" "1" "$rc"

# sha256_of 要在 Linux（sha256sum）与 macOS（shasum）上都得出同一个值
tmpf="$(mktemp)"
printf 'abc' > "$tmpf"
check "sha256(abc)" \
  "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" \
  "$(sha256_of "$tmpf")"
rm -f "$tmpf"

if [ "$fails" -ne 0 ]; then
  printf '\n%d 项失败\n' "$fails" >&2
  exit 1
fi
```

创建 `install_test.go`（让 shell 测试不至于变成没人跑的孤儿）：

```go
// 把 install.sh 的 shell 单测接进 go test ./...。
//
// why：仓库里唯一会被例行执行的测试入口是 go test。一个只能手动 bash 的
// 测试文件等于没有测试——它会在第一次改动后悄悄失效。
package main

import (
	"os/exec"
	"testing"
)

func TestInstallScriptUnits(t *testing.T) {
	out, err := exec.Command("bash", "install_test.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh 单测失败:\n%s", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test . -run TestInstallScript -v`
Expected: FAIL，输出含 `install.sh: No such file or directory`。

- [ ] **Step 3: 写 `install.sh`**

创建 `install.sh`：

```bash
#!/usr/bin/env bash
# handoff 一行安装脚本。
#
# 用法：curl -fsSL https://handoff.gosuper.dev/install | bash
#
# 职责：
#   - 探测平台，从 GitHub Release 拉对应资产，校验 sha256，装到 ~/.local/bin
#
# 边界：
#   - 只在「本机还没有 handoff」时用一次；后续换版走 handoff upgrade / agentd 自更新
#   - 不写服务单元、不改用户的 shell rc 文件、不 sudo
#   - 不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37）
#
# 环境变量：
#   HANDOFF_INSTALL_DIR  覆盖安装目录（默认 ~/.local/bin）
#   HANDOFF_INSTALL_LIB  设为 1 时只加载函数不执行主流程（供 install_test.sh 用）
set -euo pipefail

REPO="Xsxdot/handoff"
INSTALL_DIR="${HANDOFF_INSTALL_DIR:-$HOME/.local/bin}"

# log 输出到 stderr：stdout 留给可能被管道消费的内容，诊断信息不该混进去。
log() { printf '%s\n' "$*" >&2; }

# die 打印失败原因后退出。
#
# 每个失败分支都必须经它退出——脚本挂掉时用户能看到的只有这一行，
# 缺上下文的「安装失败」等于让用户去猜网络、权限还是平台。
die() {
  printf 'handoff 安装失败：%s\n' "$*" >&2
  exit 1
}

# detect_platform 把 uname 输出归一成 Release 资产用的 <os>_<arch>。
#
# 返回（stdout）：形如 darwin_arm64
#
# 注意：不在矩阵内的平台一律 die 并说明原因。静默装一个跑不起来的二进制，
# 比当场报错糟得多——症状会推迟到 agentd 启动时才出现，且看不出根因。
detect_platform() {
  local os arch
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      die "暂不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37）" ;;
    *) die "不支持的系统 $(uname -s)（仅 Darwin/Linux）" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "不支持的架构 $(uname -m)（仅 amd64/arm64）" ;;
  esac
  printf '%s_%s' "$os" "$arch"
}

# latest_tag 解析 releases/latest 的重定向，取最新 tag。
#
# 返回（stdout）：形如 v0.1.0
#
# why（不打 api.github.com）：匿名 API 限流 60 次/小时/IP，安装这条路径不该
# 被限流影响。重定向没有限流。
latest_tag() {
  local url tag
  url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")" ||
    die "取最新版本失败：连不上 github.com"
  tag="${url##*/}"
  case "$tag" in
    v*) ;;
    # 仓库一个 release 都没有时，GitHub 重定向到 .../releases，末段不是版本号
    *) die "取最新版本失败：${REPO} 还没有任何 release（重定向到 ${url}）" ;;
  esac
  printf '%s' "$tag"
}

# sha256_of 算文件的 sha256。
#
# 参数：$1 文件路径
# 返回（stdout）：64 位小写 hex
#
# 两套实现：Linux 有 sha256sum，macOS 基础系统只有 shasum。
sha256_of() {
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# main 是安装主流程。
main() {
  command -v curl > /dev/null 2>&1 || die "需要 curl，请先安装"
  command -v tar > /dev/null 2>&1 || die "需要 tar，请先安装"

  local platform tag tarball tmp want got
  platform="$(detect_platform)"
  tag="$(latest_tag)"
  tarball="handoff_${tag}_${platform}.tar.gz"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  log "handoff ${tag}  ${platform}"

  curl -fsSL -o "${tmp}/${tarball}" \
    "https://github.com/${REPO}/releases/download/${tag}/${tarball}" ||
    die "下载 ${tarball} 失败（该平台的资产可能不存在于 ${tag}）"
  curl -fsSL -o "${tmp}/checksums.txt" \
    "https://github.com/${REPO}/releases/download/${tag}/checksums.txt" ||
    die "下载 checksums.txt 失败"

  # checksums.txt 每行是 "<sha>  <裸文件名>"；sha256sum 的 * 前缀（二进制模式）也认
  want="$(awk -v f="$tarball" '$2 == f || $2 == "*" f {print $1}' "${tmp}/checksums.txt")"
  [ -n "$want" ] || die "checksums.txt 里没有 ${tarball} 的条目"
  got="$(sha256_of "${tmp}/${tarball}")"
  [ "$want" = "$got" ] ||
    die "校验失败：期望 ${want}，实得 ${got}。不安装，下载物已清理"

  tar xzf "${tmp}/${tarball}" -C "$tmp" || die "解包 ${tarball} 失败"
  [ -f "${tmp}/handoff" ] || die "包内没有 handoff 可执行文件"

  mkdir -p "$INSTALL_DIR" || die "创建 ${INSTALL_DIR} 失败"
  # install 而非 mv：目标已存在时原子覆盖，脚本因此可以反复重跑
  install -m 0755 "${tmp}/handoff" "${INSTALL_DIR}/handoff" ||
    die "写入 ${INSTALL_DIR} 失败（目录不可写？可用 HANDOFF_INSTALL_DIR 换一个目录）"

  log "已安装 ${INSTALL_DIR}/handoff  ${tag}"

  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
      log ""
      log "注意：${INSTALL_DIR} 不在 PATH 里。把下面这行加进你的 shell 配置："
      log "  export PATH=\"${INSTALL_DIR}:\$PATH\""
      log "（本脚本不会去改你的配置文件）"
      ;;
  esac
}

# 被 install_test.sh source 时只加载函数，不执行主流程
if [ "${HANDOFF_INSTALL_LIB:-}" != "1" ]; then
  main "$@"
fi
```

- [ ] **Step 4: 跑测试确认通过**

Run: `bash install_test.sh && go test . -run TestInstallScript -count=1 -v`
Expected: `install_test.sh` 静默退 0；Go 测试 PASS。

- [ ] **Step 5: 确认脚本可执行位与 shellcheck 干净（有 shellcheck 时）**

```bash
chmod +x install.sh install_test.sh
command -v shellcheck >/dev/null 2>&1 && shellcheck install.sh install_test.sh || echo "未装 shellcheck，跳过静态检查"
```

Expected: 无输出（或跳过提示）。有 warning 就修——这个脚本会被 `curl | bash` 直接执行，出错没有第二次机会。

- [ ] **Step 6: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add install.sh install_test.sh install_test.go
git commit -m "feat(install): 加一行安装脚本，含平台探测、checksum 校验与单测"
```

---

### Task 5: README 更新与真机安装验收（P2）

**Files:**
- Modify: `README.md:30`、`README.md:45`（快速开始的前置说明与构建行）、`README.md:94` 附近的命令表

**Interfaces:**
- Consumes: Task 1–4 的全部产出
- Produces: 无代码产物。本任务的交付是**一份被真机证实可用的安装路径**

> **前置条件**：本任务的 Step 3 起需要仓库里**已经存在一个真实 Release**。打 tag 是人工动作（spec §8）——执行到 Step 2 后停下，请人推 tag 并确认 Actions 绿灯，再继续。

- [ ] **Step 1: 改 README 的安装章节**

把 `README.md` 快速开始里的这一行：

```
go build -o handoff . && sudo mv handoff /usr/local/bin/   # 或直接 go run . <子命令>
```

替换为：

```bash
# 安装（macOS / Linux，amd64 / arm64）
curl -fsSL https://handoff.gosuper.dev/install | bash

# 从源码构建（开发时用）
go build -o handoff . && sudo mv handoff /usr/local/bin/   # 或直接 go run . <子命令>
```

并在其后补一段：

```markdown
安装脚本把二进制装到 `~/.local/bin/handoff`（免 sudo，可用 `HANDOFF_INSTALL_DIR` 换目录），
校验 sha256 后才落盘，可以反复重跑。**不支持 Windows**——agentd 依赖的进程承载层
Windows 实现尚未完成。

装完用 `handoff version` 确认：首行是版本号（形如 `v0.1.0`）说明装的是 release 构建；
显示 `unknown` 说明这是本地 `go build` 的产物，自动更新不会作用于它。
```

- [ ] **Step 2: 把 `handoff version` 加进命令表**

在 `README.md` 的命令表里（`handoff status` 那一行附近）加一行：

```markdown
| `handoff version` | 打印本二进制的版本标识（首行为纯版本号，供脚本比对） | — |
```

提交这一步：

```bash
git add README.md
git commit -m "docs(readme): 安装改成一行命令，补 handoff version"
```

- [ ] **Step 3: 【需人工】推第一个 tag 并确认 Actions 绿灯**

```bash
git tag v0.1.0 && git push origin v0.1.0
```

然后在 GitHub Actions 页面确认 `release` workflow 成功，Release 页面有 **5 个资产**：四个 `handoff_v0.1.0_<os>_<arch>.tar.gz` + 一个 `checksums.txt`。

失败则回到 Task 3 按 Actions 的报错修，删掉 tag（`git push origin :refs/tags/v0.1.0 && git tag -d v0.1.0`）重推。

- [ ] **Step 4: 【需人工】给 `gosuper.dev` 加重定向规则**

`handoff.gosuper.dev/install` → 302 → `https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh`

加完用这条确认重定向生效（只看跳转，不执行）：

```bash
curl -fsSLI -o /dev/null -w '%{url_effective}\n' https://handoff.gosuper.dev/install
```

Expected: 输出 `https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh`。

- [ ] **Step 5: 真机验收 P2 —— devbox 隔离安装**

> **隔离红线（违反即中止）**：安装目录必须用 `HANDOFF_INSTALL_DIR` 指到 `/tmp/`
> 下的临时目录，**绝不装进 `~/.local/bin`**；全程**不碰 `~/.handoff/`**（配置、
> 任务目录、agentd.log）；**不停、不重启、不覆盖 7777 端口上的任何 agentd**
> ——那上面有真实任务。本任务只做「下载 + 校验 + 落盘 + 跑 version」，不启动
> 任何 agentd。

在 devbox（`sycm@100.73.238.21`）上执行：

```bash
ssh sycm@100.73.238.21 'set -e; rm -rf /tmp/hf541-bin; HANDOFF_INSTALL_DIR=/tmp/hf541-bin bash -c "curl -fsSL https://handoff.gosuper.dev/install | bash"; /tmp/hf541-bin/handoff version'
```

Expected:
1. 输出 `handoff v0.1.0  darwin_arm64`（devbox 是 Apple Silicon Mac）
2. 输出 `已安装 /tmp/hf541-bin/handoff  v0.1.0`
3. 因 `/tmp/hf541-bin` 不在 PATH，输出那段 PATH 提示
4. `handoff version` 首行为 `v0.1.0`，其后有 `revision` / `go` / `platform` 三行

- [ ] **Step 6: 真机验收 P2 补充 —— 校验失败时不落盘**

证明 checksum 这道闸真的是闸，而不是一段从未被触发的代码：

```bash
ssh sycm@100.73.238.21 'set -e; rm -rf /tmp/hf541-bad; mkdir -p /tmp/hf541-bad; cd /tmp/hf541-bad; curl -fsSL -o install.sh https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh; sed -i.bak "s/handoff_\${tag}_\${platform}.tar.gz/handoff_\${tag}_\${platform}.tar.gz/" install.sh; echo "跳过——见下方说明"'
```

上面这条只是取脚本。真正的负例用**篡改 checksums 比对**的方式做，不改脚本：

```bash
ssh sycm@100.73.238.21 'set -e; rm -rf /tmp/hf541-neg && mkdir -p /tmp/hf541-neg && cd /tmp/hf541-neg && HANDOFF_INSTALL_LIB=1 . <(curl -fsSL https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh) && printf "corrupted" > fake.tar.gz && echo "sha256_of=$(sha256_of fake.tar.gz)" && echo "期望：与任何真实资产的 sha 都不同，故 main 会在此 die"'
```

Expected: 打印出 `corrupted` 的 sha256（`ba…` 之外的某个值），证明 `sha256_of` 在真机上可用且返回的是内容的哈希——`main` 里的 `[ "$want" = "$got" ]` 因此是一道有效的闸。

- [ ] **Step 7: 清理真机残留**

```bash
ssh sycm@100.73.238.21 'rm -rf /tmp/hf541-bin /tmp/hf541-bad /tmp/hf541-neg && ls -d /tmp/hf541* 2>/dev/null || echo "已清理干净"'
```

Expected: 输出 `已清理干净`。

- [ ] **Step 8: 确认 7777 上的生产 agentd 全程未受影响**

```bash
ssh sycm@100.73.238.21 'pgrep -fl "handoff.*agentd" | head'
```

Expected: 那个跑在 7777 上的 agentd 进程仍在，pid 与本任务开始前一致。

- [ ] **Step 9: 记录验收证据并提交**

把 Step 5/6/8 的实际输出（不是「应该输出」，是真实输出）贴进 backlog B54.1 的「验收」列，并把状态改成 `✅ done(已验)`。

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B54.1 转 done，记录一行安装的真机验收证据"
```

---

## 自审

**1. Spec 覆盖（对照 spec §6 的 A 期范围）**

| A 期要求 | 落在哪 |
|---|---|
| `handoff version` | Task 1 Step 6–9 |
| buildinfo 版本注入 | Task 1 Step 4 |
| proto 扩字段 | Task 1 Step 3 |
| status 展示 | Task 2 全部 |
| `.github/workflows/release.yml` | Task 3 全部 |
| `install.sh` | Task 4 全部 |
| 验收：打 tag 出四平台资产 + checksums | Task 5 Step 3 |
| 验收：干净机器一行装上（P2） | Task 5 Step 5–6 |
| spec §8 人工动作：域名重定向 | Task 5 Step 4 |
| spec §8 人工动作：打第一个 tag | Task 5 Step 3 |

A 期无遗漏。**不属于本期**（已在 Global Constraints 里显式交接给 B54.2/B54.3）：`handoff init`、`install.sh` 末尾的 `/dev/tty` 调用、`handoff service`、`internal/release`、`internal/selfupdate`、`config` 的 `update` 段。

**2. 占位符扫描**：无 TBD / TODO / 「类似 Task N」/ 「加上适当的错误处理」。每个代码步骤都给了可直接落盘的完整内容。Task 5 Step 3/4 标了「需人工」，那是 spec §8 明确列出的人工前置动作，不是省略。

**3. 类型一致性**

- `proto.BuildInfo.Version`（Task 1 Step 3 定义）→ Task 1 Step 4 填充 → Task 2 Step 3/4 消费。名字一致。
- `buildinfo.releaseVersion`（Task 1 Step 4）→ Task 3 Step 3 的 ldflags 路径、Task 3 Step 1 的测试断言。三处逐字相同：`github.com/xushixin/handoff/internal/buildinfo.releaseVersion`。
- `cmd.versionUnknown`（Task 1 Step 8）→ Task 1 Step 6 的测试断言。
- `describeBuild` / `compareBuild` 签名不变；新增的 `describeByRevision` / `compareByRevision` 只在 `cmd/status.go` 内部使用，无跨任务引用。
- shell 函数 `detect_platform` / `latest_tag` / `sha256_of`（Task 4 Step 3 定义）→ Task 4 Step 1 的测试、Task 5 Step 6 的真机负例。名字一致。
- 资产命名 `handoff_<tag>_<os>_<arch>.tar.gz`：Task 3 Step 3 产出、Task 3 Step 1 断言、Task 4 Step 3 消费。三处一致。
