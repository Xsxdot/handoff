# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# W5b-1 桌面薄壳核心 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 做出 `handoff-desktop` 薄壳：在**已经配置过 handoff 的 macOS/Linux 机器**上双击打开，托盘常驻、窗口里是真实控制台、关掉窗口后正在跑的任务不受影响。

**Architecture:** 一个 Wails v3 应用，放在仓库的 `desktop/` 目录、作为**独立的嵌套 Go module**。它 import 主模块的 `internal/config`（读监听地址与主令牌）、`internal/client`（换 ticket）、`internal/service`（判断 agentd 装没装、起没起），**不重写任何一份逻辑**。薄壳自己不实现鉴权、不代理请求、不内嵌 agentd 进程——它只是一个知道怎么握手的窗口。

**Tech Stack:** Go 1.26 / Wails v3.0.0-beta.8 / 既有的 `internal/{config,client,service}`。

## Global Constraints

以下每条都是**精确值**，每个 task 的需求都隐含包含本节：

- 薄壳框架 **Wails v3.0.0-beta.8**（P1 探针裁决，见 [spec §4.2](../specs/2026-08-16-w5-embed-and-desktop-shell-design.md) 与[探针报告](../specs/2026-08-16-w5b-p1-wails-probe-report.md)）。**不要改成 v2**：v2 没有可用的系统托盘。
- 薄壳是 `desktop/` 下的**独立嵌套 module**，module path `github.com/Xsxdot/handoff/desktop`，用 `replace github.com/Xsxdot/handoff => ../` 指回主模块。
- **主模块的 `go.mod` 一行不许动。** 主模块的 `CGO_ENABLED=0` 全平台交叉编译是承重的（`.github/workflows/release.yml:167`），薄壳要开 CGO，两者必须靠 module 边界隔开。
- **Linux 构建必须带 `-tags gtk3`**。v3 默认后端是 `gtk4 + webkitgtk-6.0`（要求 GTK ≥ 4.14），而项目基线是 Ubuntu 22.04 / Debian 12 的 `webkit2gtk-4.1`，只有 `-tags gtk3` 才走它。
- **薄壳前端的构建必须走 Wails 的 Taskfile**，不得裸调 `npm run build`（v3 的 vite 插件依赖 binding 生成器先产出 bindings，裸调必失败）。
- **薄壳绝不把 agentd 内嵌进自己的进程**，也**绝不在退出时停掉 agentd**（spec §4.3）。
- **Windows 不在本计划范围内**（spec §4.6：`service.New` 对 Windows 直接返回错误，且 B37 已评估暂不做）。代码遇到 Windows 要给出可读错误，**不许 panic、不许假装成功**。
- 托盘菜单只有两项：**「打开控制台」「退出（agentd 继续运行）」**。**不要做「停止 agentd」**——`service.Manager` 没有 `Stop`，用 `Uninstall` 冒充是错的语义（spec §4.3）。
- 日志用 `log/slog`，与主模块一致。**禁止 `fmt.Printf` 当日志用**。
- 注释与日志按 `instrumenting-code` 的要求：新文件写「职责 + 边界」头注释、导出函数写文档注释、每个错误分支带上下文、成功路径也要有一条出口日志。

---

## File Structure

```
desktop/                          新建，嵌套 module
  go.mod                          module github.com/Xsxdot/handoff/desktop（replace 指回 ../）
  Taskfile.yml                    wails3 生成后裁剪；Linux 目标加 -tags gtk3
  main.go                         Wails 应用入口：装配 tray/window/service，不放业务逻辑
  frontend/                       薄壳自带的极简页面（仅错误态与「正在启动」用）
  internal/shell/                 薄壳的全部可测逻辑，与 Wails 无关，不 import wails
    endpoint.go                   定位 agentd：读配置、判断「配没配过」
    endpoint_test.go
    handshake.go                  换 ticket → 控制台 URL
    handshake_test.go
    lifecycle.go                  agentd 装没装 / 起没起 / 拉起来
    lifecycle_test.go
    picker.go                     目录选择器的可测半边（校验与归一化）
    picker_test.go
```

**`internal/shell` 不许 import Wails。** 这是刻意的边界：Wails 的 API 要跑起 GUI 才能测，
把逻辑关在 `internal/shell` 里，它就能用普通 `go test` 覆盖，
`main.go` 只剩装配。违反这条的直接后果是这份计划里的测试全都跑不了。

---

## Task 1: 薄壳骨架与模块隔离

**Files:**
- Create: `desktop/go.mod`
- Create: `desktop/main.go`（本 task 只到「能起一个空窗口」）
- Create: `desktop/Taskfile.yml`、`desktop/frontend/`（由 `wails3 init` 生成后裁剪）
- Create: `moduleisolation_test.go`（**放主模块根目录**）

**Interfaces:**
- Consumes: 无
- Produces: 可构建的 `desktop/` 模块；后续 task 全部在它里面加代码

- [ ] **Step 1: 写失败测试——主模块不许被薄壳污染**

放在主模块根目录 `moduleisolation_test.go`：

```go
// 本文件守住一条承重边界：桌面薄壳（desktop/）是嵌套的独立 module，
// 它的 CGO 依赖（gtk/webkit）绝不能进入主模块的构建图。
//
// 为什么值得一条测试：主模块的 CGO_ENABLED=0 全平台交叉编译是发布链的地基
//（.github/workflows/release.yml:167 解释了为什么 macOS 上开 CGO 会让产物
// 带上构建机的最低系统版本约束，且症状要到用户机器上才出现）。
// 一旦有人把 desktop 挪进主模块，`go build ./...` 会在 CI 上以一种
// 很难归因的方式挂掉——不如在这里挂，报文直说是怎么回事。
package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDesktopModuleStaysOutOfParentBuildGraph(t *testing.T) {
	out, err := exec.Command("go", "list", "./...").Output()
	if err != nil {
		t.Fatalf("go list ./... 失败: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "/desktop") {
			t.Fatalf("主模块的构建图里出现了薄壳包 %q——"+
				"desktop/ 必须是嵌套的独立 module（自带 go.mod），"+
				"否则 gtk/webkit 的 CGO 依赖会进入主模块，"+
				"CGO_ENABLED=0 的交叉编译矩阵会挂", line)
		}
	}
}
```

- [ ] **Step 2: 跑它，确认此刻是通过的（基线）**

Run: `go test -run TestDesktopModuleStaysOutOfParentBuildGraph ./... -count=1`
Expected: PASS（此时 `desktop/` 还不存在，属预期基线；这条测试的价值在 Step 4 之后）

- [ ] **Step 3: 生成 Wails v3 工程**

```bash
cd desktop 2>/dev/null || mkdir desktop
cd desktop
wails3 init -n handoff-desktop -t vanilla
```

生成后**必须**做这三件裁剪，否则后面对不上：

1. 把 `go.mod` 的 module 行改成 `module github.com/Xsxdot/handoff/desktop`，并追加：
   ```
   require github.com/Xsxdot/handoff v0.0.0
   replace github.com/Xsxdot/handoff => ../
   ```
2. 删掉模板的 `greetservice.go`，同时删掉 `frontend/src/main.ts` 里
   `import { GreetService } from "../bindings/changeme"` 那一行及其用法。
   **两处必须一起删**：只删 Go 那边会让前端构建失败在
   `Could not resolve '../bindings/changeme'`（P1 探针踩过）。
3. `Taskfile.yml` 里 Linux 的构建任务加上 `-tags gtk3`。

- [ ] **Step 4: 写最小 main.go（空窗口）**

```go
// 本文件是桌面薄壳的入口：装配窗口、托盘与启动序列。
//
// 职责：只做装配。
// 边界：**不放任何业务逻辑**——定位 agentd、握手、判断 agentd 起没起，
// 全在 internal/shell 里，那里不 import Wails，因而可以用普通 go test 覆盖。
// 往本文件里写 if/else 之前先问：它能不能挪进 internal/shell。
package main

import (
	"embed"
	"log"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("桌面薄壳启动")

	app := application.New(application.Options{
		Name:        "handoff-desktop",
		Description: "handoff 控制台桌面壳",
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac: application.MacOptions{
			// 承重：关掉最后一个窗口时进程必须活着，否则托盘常驻无从谈起
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "handoff",
		Width:  1200,
		Height: 800,
		URL:    "/",
	})
	logger.Info("主窗口已创建")

	if err := app.Run(); err != nil {
		logger.Error("薄壳运行失败", "cause", err)
		log.Fatal(err)
	}
	logger.Info("薄壳正常退出")
}
```

- [ ] **Step 5: 构建并确认隔离成立**

```bash
cd desktop && wails3 task build
cd .. && go test -run TestDesktopModuleStaysOutOfParentBuildGraph ./... -count=1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
```
Expected: 三条都成功。第三条是关键——主模块的交叉编译不受薄壳影响。

- [ ] **Step 6: 加注释**（若 Step 3/4 生成的文件缺）
  - `desktop/main.go`：文件头「职责 + 边界」已在 Step 4 给出，照抄
  - `moduleisolation_test.go`：文件头已在 Step 1 给出

- [ ] **Step 7: Commit**

```bash
git add desktop moduleisolation_test.go
git commit -m "feat(desktop): Wails v3 薄壳骨架，作为嵌套 module 与主模块隔离"
```

---

## Task 2: 定位 agentd——「配没配过」的判据

**Files:**
- Create: `desktop/internal/shell/endpoint.go`
- Test: `desktop/internal/shell/endpoint_test.go`

**Interfaces:**
- Consumes: `config.DefaultPath() string`、`config.Load(path string) (*config.Config, error)`，`config.Config` 的 `Listen string`、`Token string`
- Produces:
  ```go
  type Endpoint struct { Addr string; Token string }
  type ConfigState int
  const (
      StateUnconfigured ConfigState = iota  // 没配过：要走首次引导（W5b-2）
      StateConfigured                        // 配过：可以握手
  )
  func Resolve(path string) (Endpoint, ConfigState, error)
  ```

**这个 task 的全部难点是一个陷阱**：`config.Load` 在**文件不存在时返回默认配置且不报错**
（`internal/config/config.go:226` 起，注释原话「文件不存在时返回默认配置」）。
所以**绝不能用 `Load` 的 error 判断「配没配过」**——那样在一台全新机器上会
拿到 `Listen=127.0.0.1:7777, Token=""` 并当成配好了，然后拿空令牌去握手，
报错会是一个莫名其妙的 401。判据必须是：**配置文件存在 且 Token 非空**。

- [ ] **Step 1: 写失败测试**

```go
package shell

import (
	"os"
	"path/filepath"
	"testing"
)

// 全新机器：配置文件根本不存在 → 必须判为未配置。
// 这条是本文件最重要的一条：config.Load 此时返回默认配置且 err==nil，
// 照着 err 判断会把新机器误判成「已配置」。
func TestResolveTreatsMissingFileAsUnconfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	_, state, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve 不该报错（文件不存在是正常情况）: %v", err)
	}
	if state != StateUnconfigured {
		t.Fatalf("state = %v, want StateUnconfigured", state)
	}
}

// 文件在、但 token 是空的（例如手工建了个空壳配置）→ 同样算未配置。
func TestResolveTreatsEmptyTokenAsUnconfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, state, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve 报错: %v", err)
	}
	if state != StateUnconfigured {
		t.Fatalf("state = %v, want StateUnconfigured（token 为空）", state)
	}
}

func TestResolveReturnsEndpointWhenConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: 127.0.0.1:9999\ntoken: abc123\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ep, state, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve 报错: %v", err)
	}
	if state != StateConfigured {
		t.Fatalf("state = %v, want StateConfigured", state)
	}
	if ep.Addr != "127.0.0.1:9999" || ep.Token != "abc123" {
		t.Fatalf("endpoint = %+v, want {127.0.0.1:9999 abc123}", ep)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && go test ./internal/shell/ -run TestResolve -v`
Expected: FAIL，`undefined: Resolve`

- [ ] **Step 3: 实现**

```go
// 本文件负责回答两个问题：agentd 在哪（地址与主令牌），以及这台机器配没配过 handoff。
//
// 边界：只读配置，**不碰网络**——「配置里写着某地址」和「那地址上真的有 agentd」
// 是两件事，后者是 lifecycle.go 的职责。也不写配置：首次引导（W5b-2）才写。
package shell

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/Xsxdot/handoff/internal/config"
)

// Endpoint 是连接本机 agentd 所需的最小信息。
type Endpoint struct {
	// Addr 是 agentd 的监听地址，形如 127.0.0.1:7777（不含 scheme）。
	Addr string
	// Token 是主令牌。**只在进程内使用**，不得写日志、不得传给前端。
	Token string
}

// ConfigState 表示这台机器配没配过 handoff。
type ConfigState int

const (
	// StateUnconfigured：没配过，调用方应走首次引导。
	StateUnconfigured ConfigState = iota
	// StateConfigured：配过，可以拿 Endpoint 去握手。
	StateConfigured
)

// String 让日志里的 state 是人能读的词而不是 0/1。
func (s ConfigState) String() string {
	switch s {
	case StateUnconfigured:
		return "unconfigured"
	case StateConfigured:
		return "configured"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Resolve 读取配置并判断这台机器配没配过 handoff。
//
// 参数：
//   - path: 配置文件路径。传空则用 config.DefaultPath()（~/.handoff/config.yaml）
//
// 返回：
//   - Endpoint：仅在 StateConfigured 时有意义
//   - ConfigState
//   - error：**只在配置文件存在却读不动/解析不了时返回**。文件不存在不是错误
//
// 注意：
//   - **不要用 config.Load 的 error 判断「配没配过」**。它在文件不存在时返回
//     默认配置且 err==nil，照 error 判断会把全新机器误判为已配置，
//     然后拿着空令牌去握手，症状是一个难以归因的 401。
func Resolve(path string) (Endpoint, ConfigState, error) {
	if path == "" {
		path = config.DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			slog.Info("未找到配置文件，判为未配置", "path", path)
			return Endpoint{}, StateUnconfigured, nil
		}
		return Endpoint{}, StateUnconfigured, fmt.Errorf("检查配置文件 %s: %w", path, err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return Endpoint{}, StateUnconfigured, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	if cfg.Token == "" {
		slog.Info("配置文件存在但主令牌为空，判为未配置", "path", path)
		return Endpoint{}, StateUnconfigured, nil
	}
	// 注意日志里只出地址不出令牌
	slog.Info("已定位 agentd 配置", "path", path, "addr", cfg.Listen)
	return Endpoint{Addr: cfg.Listen, Token: cfg.Token}, StateConfigured, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestResolve -v`
Expected: 三条全 PASS

- [ ] **Step 5: 加关键节点日志**（已在 Step 3 的实现里）
  - 判为未配置的两条分支各一条 Info，**带上判断依据**（文件不存在 / 令牌为空）
  - 成功定位一条 Info，**只出 addr，绝不出 token**

- [ ] **Step 6: 加注释**（已在 Step 3 给出）
  - 文件头「职责 + 边界」
  - `Resolve` 的文档注释，**必须包含那条 `config.Load` 陷阱的说明**

- [ ] **Step 7: Commit**

```bash
git add desktop/internal/shell/endpoint.go desktop/internal/shell/endpoint_test.go
git commit -m "feat(desktop): 定位 agentd 配置，并把「配没配过」的判据钉死在文件存在+令牌非空"
```

---

## Task 3: 握手——换 ticket 拿到控制台 URL

**Files:**
- Create: `desktop/internal/shell/handshake.go`
- Test: `desktop/internal/shell/handshake_test.go`

**Interfaces:**
- Consumes: `Endpoint`（Task 2）；`client.New(addr, token string) *client.Client`；
  `(*client.Client).IssueAuthTicket(ctx context.Context, deviceName string) (*proto.AuthTicketResp, error)`；
  `proto.AuthTicketResp{URL string; ExpiresAt time.Time}`
- Produces: `func ConsoleURL(ctx context.Context, ep Endpoint, deviceName string) (string, error)`

**这里不要自己拼 HTTP。** `internal/client` 已经有 `IssueAuthTicket`，
而 `cmd/console.go` 的文件头明写着 `--print-url` 就是「桌面壳的接线点」。
薄壳走 Go API（同进程调用），不 shell out 调 CLI——那会多一层进程与错误面。

- [ ] **Step 1: 写失败测试**

```go
package shell

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConsoleURLReturnsIssuedURL(t *testing.T) {
	var gotAuth, gotDevice string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/tickets" {
			t.Errorf("请求路径 = %q, want /api/auth/tickets", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			DeviceName string `json:"device_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotDevice = body.DeviceName
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":        "http://127.0.0.1:7777/console?ticket=deadbeef",
			"expires_at": time.Now().Add(time.Minute),
		})
	}))
	defer ts.Close()

	ep := Endpoint{Addr: strings.TrimPrefix(ts.URL, "http://"), Token: "tok"}
	got, err := ConsoleURL(context.Background(), ep, "我的 mac")
	if err != nil {
		t.Fatalf("ConsoleURL 报错: %v", err)
	}
	if got != "http://127.0.0.1:7777/console?ticket=deadbeef" {
		t.Fatalf("URL = %q，与 agentd 返回的不一致", got)
	}
	if !strings.Contains(gotAuth, "tok") {
		t.Errorf("Authorization 头没带上主令牌，实际 = %q", gotAuth)
	}
	if gotDevice != "我的 mac" {
		t.Errorf("device_name = %q, want 我的 mac", gotDevice)
	}
}

// agentd 没起来时，错误必须能让人一眼看出是「连不上」，
// 而不是抛一个赤裸的 dial tcp——薄壳的用户没有终端可看。
func TestConsoleURLSaysAgentdUnreachable(t *testing.T) {
	// 关掉的 server：端口上没人监听
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := strings.TrimPrefix(ts.URL, "http://")
	ts.Close()

	_, err := ConsoleURL(context.Background(), Endpoint{Addr: addr, Token: "tok"}, "dev")
	if err == nil {
		t.Fatal("连不上 agentd 却没报错")
	}
	if !strings.Contains(err.Error(), "agentd") {
		t.Errorf("错误信息没提到 agentd，用户无从判断，实际 = %q", err)
	}
}

// 设备名缺省要能推出来，且带得出「这是桌面端」的信息，
// 否则会话列表里全是一样的主机名，吊销时分不清哪个是哪个。
func TestDefaultDeviceNameMentionsDesktop(t *testing.T) {
	got := DefaultDeviceName()
	if !strings.Contains(got, "handoff-desktop") {
		t.Fatalf("缺省设备名 = %q，应含 handoff-desktop", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && go test ./internal/shell/ -run "TestConsoleURL|TestDefaultDeviceName" -v`
Expected: FAIL，`undefined: ConsoleURL`

- [ ] **Step 3: 实现**

```go
// 本文件负责鉴权握手：拿主令牌向 agentd 换一张一次性 ticket，得到可直接打开的控制台 URL。
//
// 职责：调 internal/client 的 IssueAuthTicket，把结果整理成一个 URL。
// 边界：
//   - **不实现任何鉴权逻辑**。凭据的签发与校验全在 agentd 侧
//   - **不 shell out 调 handoff console**。同一个进程里有 Go API，
//     多起一个进程只是多一层错误面
//   - 不判断 URL 打开后页面上有什么。那由 agentd 伺服的前端决定
package shell

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Xsxdot/handoff/internal/client"
)

// DefaultDeviceName 返回展示在 agentd 会话列表里的设备名。
//
// 带上 handoff-desktop 前缀是有意的：同一台机器上既可能有 CLI 换的会话、
// 也可能有薄壳换的会话，只写主机名会让吊销时分不清该吊哪个。
func DefaultDeviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		// 取不到主机名不是失败：服务端会补，这里给个仍然可辨认的名字
		slog.Warn("取主机名失败，设备名退化为不含主机名的形式", "cause", err)
		return "handoff-desktop"
	}
	return fmt.Sprintf("handoff-desktop (%s)", host)
}

// ConsoleURL 换一张 ticket，返回可直接交给 webview 打开的控制台 URL。
//
// 参数：
//   - ctx: 取消与超时
//   - ep: Task 2 的 Resolve 产出的地址与主令牌
//   - deviceName: 展示名；传空则用 DefaultDeviceName()
//
// 返回：
//   - 控制台 URL。**有效期很短（60 秒）**，拿到就该立刻加载，不要缓存复用
//   - error：连不上 agentd、令牌不对、或响应无法解析
//
// 注意：
//   - 返回的 URL 里带着一次性凭据，**不得写进日志**
func ConsoleURL(ctx context.Context, ep Endpoint, deviceName string) (string, error) {
	if deviceName == "" {
		deviceName = DefaultDeviceName()
	}
	slog.Info("开始鉴权握手", "addr", ep.Addr, "device_name", deviceName)

	tk, err := client.New(ep.Addr, ep.Token).IssueAuthTicket(ctx, deviceName)
	if err != nil {
		// client 的报文已经含「连接 agentd … 失败（它在运行吗？）」，这里补上地址维度
		slog.Error("鉴权握手失败", "addr", ep.Addr, "cause", err)
		return "", fmt.Errorf("向 agentd %s 换取控制台入场券失败: %w", ep.Addr, err)
	}
	// 只记过期时间，不记 URL：URL 里带一次性凭据
	slog.Info("鉴权握手成功", "addr", ep.Addr, "expires_at", tk.ExpiresAt)
	return tk.URL, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run "TestConsoleURL|TestDefaultDeviceName" -v`
Expected: 三条全 PASS

- [ ] **Step 5: 加关键节点日志**（已在 Step 3）
  - 握手开始 / 成功 / 失败三条，失败带 cause 与 addr
  - **凭据类内容一律不入日志**：不打 token，不打返回的 URL

- [ ] **Step 6: 加注释**（已在 Step 3）
  - 文件头三条边界
  - `ConsoleURL` 的「URL 含一次性凭据、不得入日志、不要缓存」

- [ ] **Step 7: Commit**

```bash
git add desktop/internal/shell/handshake.go desktop/internal/shell/handshake_test.go
git commit -m "feat(desktop): 鉴权握手复用 internal/client，不重写也不 shell out"
```

---

## Task 4: agentd 生命周期——在不在、起不起得来

**Files:**
- Create: `desktop/internal/shell/lifecycle.go`
- Test: `desktop/internal/shell/lifecycle_test.go`

**Interfaces:**
- Consumes: `service.New(log *slog.Logger) (service.Manager, error)`；
  `service.Manager` 接口（`Install(Spec) error` / `Uninstall() error` / `Status() (Status, error)` / `Kind() string` / `UnitPath() (string, error)`）；
  `service.Spec{BinPath, ConfigPath, LogPath string}`；`service.Status{Installed, Running bool; Detail string}`
- Produces:
  ```go
  type managerFactory func(*slog.Logger) (service.Manager, error)
  var newManager managerFactory = service.New   // 测试缝
  func EnsureRunning(log *slog.Logger, spec service.Spec) error
  ```

**两条硬约束**：
1. **绝不把 agentd 跑进薄壳进程**。要么它已经在跑，要么用 `service.Manager` 托管起来。
2. **Windows 上 `service.New` 返回错误**（spec §4.6）。这里要把那个错误原样带出来，
   **不许 panic，也不许当成「装好了」继续往下走**。

- [ ] **Step 1: 写失败测试**

```go
package shell

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/service"
)

// fakeManager 是 service.Manager 的测试替身：Install 只记录被调用，不碰真系统。
// 必须有它——真 Install 会往 launchd/systemd 写单元文件，测试绝不允许。
type fakeManager struct {
	status     service.Status
	statusErr  error
	installErr error
	installed  bool
	gotSpec    service.Spec
}

func (f *fakeManager) Install(s service.Spec) error   { f.installed = true; f.gotSpec = s; return f.installErr }
func (f *fakeManager) Uninstall() error               { return nil }
func (f *fakeManager) Status() (service.Status, error) { return f.status, f.statusErr }
func (f *fakeManager) Kind() string                    { return "fake" }
func (f *fakeManager) UnitPath() (string, error)       { return "/fake/unit", nil }

func withManager(t *testing.T, m service.Manager, err error) {
	t.Helper()
	prev := newManager
	newManager = func(*slog.Logger) (service.Manager, error) { return m, err }
	t.Cleanup(func() { newManager = prev })
}

// 已经在跑：绝不能再 Install 一次——那会重装单元、打断正在跑的任务。
func TestEnsureRunningDoesNothingWhenAlreadyRunning(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Running: true}}
	withManager(t, f, nil)
	if err := EnsureRunning(slog.Default(), service.Spec{BinPath: "/bin/handoff"}); err != nil {
		t.Fatalf("EnsureRunning 报错: %v", err)
	}
	if f.installed {
		t.Fatal("agentd 已在运行却又执行了 Install——会打断正在跑的任务")
	}
}

// 没装：装上。并且 Spec 要原样传下去。
func TestEnsureRunningInstallsWhenAbsent(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: false}}
	withManager(t, f, nil)
	spec := service.Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}
	if err := EnsureRunning(slog.Default(), spec); err != nil {
		t.Fatalf("EnsureRunning 报错: %v", err)
	}
	if !f.installed {
		t.Fatal("agentd 没装却没有执行 Install")
	}
	if f.gotSpec != spec {
		t.Fatalf("传给 Install 的 Spec = %+v, want %+v", f.gotSpec, spec)
	}
}

// 平台不支持（Windows）：把原因原样带出来，不许吞、不许 panic。
func TestEnsureRunningSurfacesUnsupportedPlatform(t *testing.T) {
	withManager(t, nil, errors.New("暂不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成"))
	err := EnsureRunning(slog.Default(), service.Spec{})
	if err == nil {
		t.Fatal("平台不支持却没报错")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("错误信息丢掉了平台原因，实际 = %q", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && go test ./internal/shell/ -run TestEnsureRunning -v`
Expected: FAIL，`undefined: EnsureRunning`

- [ ] **Step 3: 实现**

```go
// 本文件负责 agentd 的存活：判断它装没装、起没起，必要时用 internal/service 托管起来。
//
// 边界（三条都是承重的，见 spec §4.3）：
//   - **绝不把 agentd 跑进薄壳进程**。agentd 必须活过薄壳、必须能在无 GUI 机器上裸跑，
//     且 B59 的更新机制假设它由 service 托管
//   - **绝不在薄壳退出时停掉 agentd**。执行者不能随关窗陪葬
//   - 已在运行时**什么都不做**。重复 Install 会重装单元、打断正在跑的任务
package shell

import (
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/service"
)

// newManager 是 service.New 的测试缝：测试注入替身，避免真的往 launchd/systemd 写单元。
// 生产路径永远是 service.New。
var newManager = service.New

// EnsureRunning 确保本机 agentd 处于运行状态。
//
// 参数：
//   - log: 日志入口，会透传给 service.Manager
//   - spec: 托管所需的二进制、配置与日志路径
//
// 返回：
//   - error：平台不支持、查状态失败、或安装失败。**平台不支持时原样带出原因**
//     （Windows 上 service.New 会说明是 B37 未完成），不要压成一句「失败」
//
// 注意：
//   - agentd 已在运行时本函数**不做任何事**，这是刻意的
func EnsureRunning(log *slog.Logger, spec service.Spec) error {
	m, err := newManager(log)
	if err != nil {
		log.Error("无法获得服务管理器", "cause", err)
		return fmt.Errorf("这台机器上无法托管 agentd: %w", err)
	}
	st, err := m.Status()
	if err != nil {
		log.Error("查询 agentd 状态失败", "kind", m.Kind(), "cause", err)
		return fmt.Errorf("查询 agentd 状态: %w", err)
	}
	if st.Running {
		log.Info("agentd 已在运行，无需干预", "kind", m.Kind(), "detail", st.Detail)
		return nil
	}
	log.Info("agentd 未在运行，准备托管拉起", "kind", m.Kind(), "installed", st.Installed, "bin", spec.BinPath)
	if err := m.Install(spec); err != nil {
		log.Error("托管 agentd 失败", "kind", m.Kind(), "bin", spec.BinPath, "cause", err)
		return fmt.Errorf("托管并拉起 agentd: %w", err)
	}
	log.Info("agentd 已托管并拉起", "kind", m.Kind())
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestEnsureRunning -v`
Expected: 三条全 PASS

- [ ] **Step 5: 加关键节点日志**（已在 Step 3）
  - 「已在运行」也要有一条 Info——**没有它就分不清「跳过了」和「压根没跑到这」**
  - 三个错误分支各带 cause 与足够定位的字段

- [ ] **Step 6: 加注释**（已在 Step 3）：文件头三条承重边界、`newManager` 为什么是 var

- [ ] **Step 7: Commit**

```bash
git add desktop/internal/shell/lifecycle.go desktop/internal/shell/lifecycle_test.go
git commit -m "feat(desktop): agentd 生命周期——已在跑就不动，没装才托管，平台不支持原样报出"
```

---

## Task 5: 目录选择器的可测半边（收口 B110 本机部分）

**Files:**
- Create: `desktop/internal/shell/picker.go`
- Test: `desktop/internal/shell/picker_test.go`

**Interfaces:**
- Consumes: 无（本 task 不碰 Wails）
- Produces: `func NormalizeProjectDir(raw string) (string, error)`

Wails 的原生对话框调用放在 Task 6 的装配里；**选出来之后的校验与归一化放在这里**，
因为那部分才是会出错、且能测的部分。

- [ ] **Step 1: 写失败测试**

```go
package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeProjectDirAcceptsExistingDir(t *testing.T) {
	dir := t.TempDir()
	got, err := NormalizeProjectDir(dir)
	if err != nil {
		t.Fatalf("报错: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("返回值不是绝对路径: %q", got)
	}
}

func TestNormalizeProjectDirRejectsMissing(t *testing.T) {
	_, err := NormalizeProjectDir(filepath.Join(t.TempDir(), "不存在"))
	if err == nil {
		t.Fatal("目录不存在却没报错")
	}
}

// 选到文件而不是目录：报文必须说清「这是文件不是目录」，
// 只说「无效路径」会让人以为是路径拼错了。
func TestNormalizeProjectDirRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NormalizeProjectDir(f)
	if err == nil {
		t.Fatal("选中文件却没报错")
	}
	if !strings.Contains(err.Error(), "目录") {
		t.Fatalf("报文没说清是目录问题，实际 = %q", err)
	}
}

func TestNormalizeProjectDirRejectsEmpty(t *testing.T) {
	if _, err := NormalizeProjectDir("   "); err == nil {
		t.Fatal("空输入却没报错")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && go test ./internal/shell/ -run TestNormalizeProjectDir -v`
Expected: FAIL，`undefined: NormalizeProjectDir`

- [ ] **Step 3: 实现**

```go
// 本文件是目录选择器里「选完之后」的那半边：校验与归一化。
//
// 为什么单独一个文件：原生对话框本身要跑起 GUI 才能调，测不了；
// 而真正会出错的是选完之后——用户可能取消、可能选到文件、可能选到已被删掉的路径。
// 把这半边关在这里，就能用普通 go test 覆盖。
//
// 边界：**只判断本机路径**。给远程开发机添项目时选出来的本机路径没有意义，
// 这个不对称与 B108「Reveal in Finder 只做本机半边」是同一个，已被接受（spec §4.5）。
package shell

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// NormalizeProjectDir 校验并归一化用户选中的项目目录。
//
// 参数：
//   - raw: 原生对话框返回的路径。用户取消时可能是空串
//
// 返回：
//   - 绝对路径
//   - error：空输入、路径不存在、或选中的不是目录。报文要说清是哪一种
func NormalizeProjectDir(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		// 用户取消选择也会走到这里，属正常操作，用 Debug 不用 Warn
		slog.Debug("目录选择返回空值（用户取消或对话框未选中）")
		return "", fmt.Errorf("没有选择任何目录")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		slog.Error("目录路径归一化失败", "raw", p, "cause", err)
		return "", fmt.Errorf("解析路径 %s: %w", p, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		slog.Error("选中的路径不可用", "path", abs, "cause", err)
		return "", fmt.Errorf("路径 %s 不可用: %w", abs, err)
	}
	if !info.IsDir() {
		slog.Error("选中的是文件而不是目录", "path", abs)
		return "", fmt.Errorf("%s 是文件，不是目录——请选择项目所在的目录", abs)
	}
	slog.Info("已选定项目目录", "path", abs)
	return abs, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestNormalizeProjectDir -v`
Expected: 四条全 PASS

- [ ] **Step 5: 加关键节点日志**（已在 Step 3）
  - 用户取消走 Debug 而非 Warn——那不是异常
  - 三个错误分支各自说清是哪一种问题
  - 成功一条 Info

- [ ] **Step 6: 加注释**（已在 Step 3）：文件头解释「为什么单独一个文件」与本机/远程不对称

- [ ] **Step 7: Commit**

```bash
git add desktop/internal/shell/picker.go desktop/internal/shell/picker_test.go
git commit -m "feat(desktop): 目录选择的校验与归一化，报文区分取消/不存在/选到文件"
```

---

## Task 6: 装配——启动序列、托盘、目录选择器绑定

**Files:**
- Modify: `desktop/main.go`（替换 Task 1 的最小版）

**Interfaces:**
- Consumes: `shell.Resolve`、`shell.ConsoleURL`、`shell.EnsureRunning`、`shell.NormalizeProjectDir`、`shell.DefaultDeviceName`
- Produces: 可运行的薄壳

本 task 没有单元测试——它全是 Wails 装配，测不了。
**它的验收在 Task 7 的真机走查**。这也是前面五个 task 把逻辑关在 `internal/shell` 的原因。

- [ ] **Step 1: 写启动序列与托盘**

```go
// 本文件是桌面薄壳的入口：装配窗口、托盘与启动序列。
//
// 职责：只做装配与错误呈现。
// 边界：
//   - **不放业务逻辑**。定位、握手、生命周期、路径校验全在 internal/shell，
//     那里不 import Wails，因而可以用普通 go test 覆盖
//   - **不在退出路径上停 agentd**（spec §4.3 承重）
//   - 托盘只有「打开控制台」「退出」两项。**不做「停止 agentd」**：
//     service.Manager 没有 Stop，用 Uninstall 冒充是错的语义
package main

import (
	"context"
	"embed"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("桌面薄壳启动")

	app := application.New(application.Options{
		Name:        "handoff-desktop",
		Description: "handoff 控制台桌面壳",
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac: application.MacOptions{
			// 承重：关掉最后一个窗口时进程必须活着，托盘才谈得上常驻
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "handoff",
		Width:  1200,
		Height: 800,
		URL:    "/",
	})

	openConsole := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		ep, state, err := shell.Resolve("")
		if err != nil {
			logger.Error("读取配置失败", "cause", err)
			showError(app, "读取 handoff 配置失败", err.Error())
			return
		}
		if state == shell.StateUnconfigured {
			// 首次引导是 W5b-2 的范围。在它做出来之前，这里必须给一条
			// 能自救的指引，而不是一个空白窗口
			logger.Info("这台机器还没配置过 handoff")
			showError(app, "还没有配置 handoff",
				"请先在终端执行 handoff init 完成配置，然后重新打开本应用。\n"+
					"（图形化首次引导将在后续版本提供）")
			return
		}
		if err := shell.EnsureRunning(logger, specFor(ep)); err != nil {
			logger.Error("确保 agentd 运行失败", "cause", err)
			showError(app, "无法启动 agentd", err.Error())
			return
		}
		url, err := shell.ConsoleURL(ctx, ep, shell.DefaultDeviceName())
		if err != nil {
			logger.Error("握手失败", "cause", err)
			showError(app, "无法连接 agentd", err.Error())
			return
		}
		// 不打 url：里面带一次性凭据
		logger.Info("加载控制台")
		win.SetURL(url)
		win.Show()
	}

	tray := app.SystemTray.New()
	tray.SetLabel("handoff")
	menu := app.Menu.New()
	menu.Add("打开控制台").OnClick(func(*application.Context) { openConsole() })
	menu.Add("退出（agentd 继续运行）").OnClick(func(*application.Context) {
		// 只退薄壳。agentd 与它拉起的执行者继续跑，这是招牌属性
		logger.Info("用户从托盘退出薄壳；agentd 不受影响")
		app.Quit()
	})
	tray.SetMenu(menu)
	logger.Info("系统托盘已就绪")

	// 目录选择器：暴露给前端，收口 B110 的本机半边
	app.Event.On("pick-project-dir", func(*application.CustomEvent) {
		raw, err := app.Dialog.OpenFile().
			CanChooseDirectories(true).
			CanChooseFiles(false).
			SetTitle("选择项目目录").
			PromptForSingleSelection()
		if err != nil {
			logger.Error("打开目录选择器失败", "cause", err)
			return
		}
		dir, err := shell.NormalizeProjectDir(raw)
		if err != nil {
			logger.Warn("目录选择未产生可用结果", "cause", err)
			app.Event.Emit("project-dir-error", err.Error())
			return
		}
		logger.Info("目录已选定并回传前端", "path", dir)
		app.Event.Emit("project-dir-picked", dir)
	})

	go openConsole()

	if err := app.Run(); err != nil {
		logger.Error("薄壳运行失败", "cause", err)
		log.Fatal(err)
	}
	logger.Info("薄壳正常退出；agentd 未被触碰")
}

// specFor 组装托管 agentd 所需的路径。
//
// BinPath 取当前可执行文件所在目录旁的 handoff——薄壳与 CLI 用同一份二进制是
// W5b-2 内嵌方案的前提（spec §5.2）。取不到时退回 PATH 上的 handoff。
func specFor(_ shell.Endpoint) service.Spec {
	// 具体路径策略在 W5b-2 内嵌二进制时才完整；本轮先用 PATH 上的 handoff，
	// 并在日志里说清，避免它悄悄变成一个隐藏约定
	slog.Info("本轮用 PATH 上的 handoff 托管 agentd；内嵌与释出策略见 W5b-2")
	return service.Spec{BinPath: "handoff"}
}

// showError 用原生对话框呈现错误。
//
// 为什么不是往页面里写：此刻页面很可能还没加载出来（握手就是失败在加载之前），
// 往一个空白 webview 里写字用户看不到。
func showError(app *application.App, title, detail string) {
	d := app.Dialog.Error()
	d.SetTitle(title)
	d.SetMessage(detail)
	d.Show()
}
```

- [ ] **Step 2: 构建**

Run: `cd desktop && wails3 task build`
Expected: 成功

- [ ] **Step 3: 确认主模块仍然干净**

```bash
go test -run TestDesktopModuleStaysOutOfParentBuildGraph ./... -count=1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
```
Expected: 都成功

- [ ] **Step 4: 加关键节点日志**（已在 Step 1）
  - 启动、托盘就绪、加载控制台、从托盘退出各一条
  - 每个失败分支既弹对话框**也**打 Error 日志——对话框用户看得见，日志排障看得见
  - **不打 URL、不打 token**

- [ ] **Step 5: 加注释**（已在 Step 1）：文件头三条边界；`specFor` 与 `showError` 的文档注释

- [ ] **Step 6: Commit**

```bash
git add desktop/main.go
git commit -m "feat(desktop): 装配启动序列、托盘与目录选择器绑定"
```

---

## Task 7: 真机走查（P4 在内）

**Files:** 无代码改动。产出是走查记录，写进 `docs/superpowers/plans/2026-08-16-w5b1-desktop-shell-core.md` 的本节末尾。

**这个 task 不能由 subagent 自称完成**——它要求真的启动一个 agentd、真的跑一个任务、
真的关掉窗口再看进程。**没有真机证据就报 BLOCKED，不要写「应该没问题」。**

- [ ] **Step 1: 准备一个隔离的 agentd**

**绝不动 `~/.handoff`。** 用临时数据目录：

```bash
export HANDOFF_TEST_DIR=$(mktemp -d)
handoff agentd --config "$HANDOFF_TEST_DIR/config.yaml" &
```

- [ ] **Step 2: 走查五项，逐项记录实际输出**

| # | 走查项 | 通过判据 |
|---|--------|---------|
| 1 | 双击 `.app` | 窗口打开，里面是**真实控制台**（不是 stub 页、不是 404） |
| 2 | 托盘 | 菜单栏出现 handoff 图标，菜单有且只有「打开控制台」「退出（agentd 继续运行）」两项 |
| 3 | 关闭窗口 | 薄壳进程仍存活（`pgrep -f handoff-desktop` 有输出），托盘图标还在 |
| 4 | **P4**：关窗口时正在跑任务 | 派一个任务跑着 → 关掉薄壳窗口 → **执行者进程仍存活、任务继续推进** |
| 5 | 目录选择器 | 弹出原生目录框，选中后前端拿到绝对路径；选文件时报文说的是「是文件，不是目录」 |

- [ ] **Step 3: 记录结果**

在本节末尾追加一张表，**写实际输出而不是「通过」**。例如
`pgrep -f handoff-desktop → 41823`、`handoff show <task> → state=running`。

- [ ] **Step 4: 清理**

```bash
handoff stop <task>   # 若还在跑
rm -rf "$HANDOFF_TEST_DIR"
```

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/plans/2026-08-16-w5b1-desktop-shell-core.md
git commit -m "docs(w5b1): 真机走查记录，含 P4 执行者存活验证"
```

### 走查记录

<!-- Task 7 执行时在此追加。没有真机证据就不要填。 -->

---

## 本计划刻意不做的

| 不做 | 去哪做 | 为什么不在这里 |
|---|---|---|
| 图形化首次引导 | W5b-2 | 未配置时本计划给的是「请先跑 handoff init」的指引，已能自救 |
| 内嵌 `handoff` 二进制并释出 | W5b-2 | 本计划用 PATH 上的 handoff，已够跑通全链路 |
| 三平台构建链、签名公证、release 资产 | W5b-3 | 还卡在 §4.6 的 Windows 裁决与 P1 的 Linux 半边 |
| Windows 支持 | 待用户裁决（spec §4.6） | `service.New` 对 Windows 直接返回错误，且 B37 已评估暂不做 |
| 托盘「停止 agentd」 | 待补 `service.Manager.Stop`（spec §4.3） | 现在没有这个能力，用 `Uninstall` 冒充是错的语义 |
| 薄壳自更新 | 非目标（spec §7） | B59 已有操作者触发的机制 |
