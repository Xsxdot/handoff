# 账本可选化与命令分层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让账本域（任务卡）成为**默认关闭的可选功能**，并把混进执行域的 `wait --card` 搬回 `card` 族，同时补上「验收结果」与「等人标记」两个缺失的 CLI 写入口。

**Architecture:** 加一个机器级开关 `ledger.enabled`（默认 false），单点拦截喂三面——CLI 在 `openLedger()` 拦、agentd 在启动时不开库不起镜像、web 用 `/api/ledger/health` 探测后条件渲染入口。执行域动词（`dispatch/wait/reply/continue/approve/done`）零 card 感知，账本三族（`card`/`workflow`/`decision`）包装执行域。

**Tech Stack:** Go 1.x（cobra CLI、`database/sql`、SQLite/PG 双方言）、React + TypeScript + Vite（web/，vitest + testing-library）。

**上游 spec:** `docs/superpowers/specs/2026-08-20-ledger-optional-and-layering-design.md`（本 plan 是它的 §3/§4/§5 落地；**§6 的 skill 改写不在本 plan 范围内**，由协调者另行完成）

## Global Constraints

- **日志用 `logger` / `slog`，禁止 `fmt.Printf` 作为日志机制**。Go 侧本仓约定见既有代码（`internal/ledger/log.go` 的 `log()`、`cmd/` 侧 `slog`）。web 侧**禁止 `console.log`**（既有红线）。
- 新建文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释；非显然分支写「为什么」的中文注释。
- 中文注释与中文错误文案，与仓库既有风格一致。
- **gofmt 必须干净**：`gofmt -l . | grep -v '^web/'` 应无输出（历史教训：执行者的 ledger 会漏 gofmt）。
- 每个 task 独立提交，提交信息用中文，遵循 `type(scope): 说明` 格式。
- **不要动 `skills/handoff/SKILL.md`**——那一节由协调者本地写。
- **不要碰 `docs/superpowers/backlog.md`**。
- 分支上已有的账本实现是已验收的，除本 plan 明确指出的点外**不重构、不改名、不"顺手优化"**。

---

### Task 1: `ledger.enabled` 配置字段 + CLI 单点拦截

**Files:**
- Modify: `internal/config/config.go:143-147`（`LedgerConfig` 加字段）、`internal/config/config.go:533`（`decodeStrict` 错误文案的已知键清单）
- Modify: `cmd/ledgercli.go`（`openLedger()` 开头拦截）
- Modify: `cmd/ledgercli_test.go:21-35`（测试基座写 config 时打开开关）
- Test: `cmd/ledgercli_test.go`

**Interfaces:**
- Consumes: 无（首个 task）
- Produces: `config.LedgerConfig.Enabled bool`；`openLedger() (*ledger.Store, error)` 在未启用时返回错误，错误文案含子串 `账本未启用`。后续 Task 2 读同一个 `cfg.Ledger.Enabled` 字段。

- [ ] **Step 1: 写失败的测试**

在 `cmd/ledgercli_test.go` 末尾追加：

```go
// TestLedgerDisabledByDefault 未配 ledger 段时 card 族必须拒绝执行，
// 且不得在 DataDir 下自建 ledger.db——「静默自建」正是本次要消灭的行为。
func TestLedgerDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	c := &config.Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir, StallTimeout: 2 * time.Hour}
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs([]string{"--config", cfgPath, "card", "add", "标题", "--project", "demo"})
	err := Execute()
	if err == nil {
		t.Fatalf("账本未启用时 card add 应报错，实际成功: %q", out.String())
	}
	if !strings.Contains(err.Error(), "账本未启用") {
		t.Fatalf("错误文案应含「账本未启用」，实际: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ledger.db")); statErr == nil {
		t.Fatalf("账本未启用时不得自建 ledger.db")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./cmd/ -run TestLedgerDisabledByDefault -count=1`
Expected: FAIL —— 当前 `card add` 会成功并生成 `ledger.db`，报 `账本未启用时 card add 应报错`

- [ ] **Step 3: 加配置字段**

`internal/config/config.go`，把 `LedgerConfig` 改成：

```go
// LedgerConfig 账本域（任务卡）中心库配置。只描述本机如何连库，
// 不描述库里有什么——schema 归 internal/ledger 管。
type LedgerConfig struct {
	// Enabled 账本域总开关，默认 false。账本是可选功能：不开时 CLI 的
	// card/workflow/decision 三族拒绝执行、agentd 不开库不起镜像、web
	// 不渲染入口。**不能用「DSN 非空」当启用信号**——单机 SQLite 用户
	// 恰恰是 DSN 为空的那一类，那样判会把他们永久排除在外。
	Enabled bool `yaml:"enabled,omitempty"`
	// DSN 形如 postgres://user:pass@host:5432/db。空 = SQLite 回退。
	DSN string `yaml:"dsn,omitempty"`
}
```

同文件 `decodeStrict` 的错误文案里，把 `ledger{dsn}` 改成 `ledger{enabled,dsn}`（该字符串在 `config.go:533` 附近，整行是一条很长的 `fmt.Errorf`，只改这一个子串，别动别处）。

- [ ] **Step 4: 加 CLI 拦截**

`cmd/ledgercli.go` 的 `openLedger()`，在 `cfg := loadCLIConfig()` 之后、取 dsn 之前插入：

```go
	// 账本是可选功能：未启用时干净拒绝，绝不静默自建 ledger.db。
	// 这里是 card/workflow/decision 三族的唯一入口，拦这一处即可覆盖全族。
	if !cfg.Ledger.Enabled {
		slog.Warn("账本未启用，拒绝账本命令", "config_dsn_set", cfg.Ledger.DSN != "")
		return nil, fmt.Errorf("账本未启用：在 config.yaml 设 ledger.enabled: true（可选 ledger.dsn 连中心库，缺省本机 SQLite）")
	}
```

如果 `cmd/ledgercli.go` 尚未 import `log/slog`，加上。

- [ ] **Step 5: 修测试基座（否则既有账本测试全红）**

`cmd/ledgercli_test.go` 的 `runLedgerCLI` 里，写 config 那行加上账本开关——**这一步必须做**，`cmd/` 下所有既有 card/workflow/decision 测试都走这个基座：

```go
		c := &config.Config{
			Listen: "127.0.0.1:0", Token: "t", DataDir: dir, StallTimeout: 2 * time.Hour,
			// 账本测试基座必须显式开账本：开关默认 false，不开则全族拒绝执行
			Ledger: config.LedgerConfig{Enabled: true},
		}
```

同时确认该测试文件 import 了 `bytes`、`os`、`path/filepath`、`strings`、`time`、`config`（新测试用到；缺哪个补哪个）。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestLedgerDisabledByDefault|TestOpenLedgerFallbackSQLite' -count=1`
Expected: PASS（两条都过——前者证明关时拒绝，后者证明开时回退 SQLite 照旧）

- [ ] **Step 7: 跑 cmd 全包回归**

Run: `go test ./cmd/ -count=1`
Expected: PASS。若有账本相关用例红，检查它是否绕过了 `runLedgerCLI` 自己写 config——那些也要补 `Ledger: config.LedgerConfig{Enabled: true}`。

- [ ] **Step 8: 加日志与注释（本 task 的日志/注释门）**

确认已具备：
- `openLedger()` 拒绝分支有 `slog.Warn`，带 `config_dsn_set` 上下文（区分「压根没配」与「配了 dsn 但忘了开开关」两种现场）
- `LedgerConfig.Enabled` 字段有「为什么不能用 DSN 非空当信号」的注释
- `runLedgerCLI` 里新增那行有「为什么基座必须开」的注释

- [ ] **Step 9: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add internal/config/config.go cmd/ledgercli.go cmd/ledgercli_test.go
git commit -m "feat(ledger): 加 ledger.enabled 开关，CLI 未启用时干净拒绝"
```

---

### Task 2: agentd 侧门控 + health 端点挪出 503 门

**Files:**
- Modify: `cmd/agentd.go:63-79`（账本库打开块加开关判断）
- Modify: `internal/agentd/ledgerapi.go:31`（health 路由注册）、`internal/agentd/ledgerapi.go:295-302`（handler）
- Test: `internal/agentd/ledgerapi_test.go`（若不存在则新建，文件头注释见 Step 5）

**Interfaces:**
- Consumes: Task 1 的 `config.LedgerConfig.Enabled`
- Produces: `GET /api/ledger/health` 在账本未启用时返回 HTTP 200 + `{"enabled":false}`；启用时返回 `{"enabled":true,"mirror":[...]}`。Task 6 的前端按这个契约解析。

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/ledgerapi_test.go` 追加（文件不存在则新建，含文件头注释）：

```go
// TestLedgerHealthReportsDisabled 账本未挂载时 health 必须 200 + enabled:false。
// 为什么不能用 503：这个端点是前端做入口门控的探针，503 与「网络错」
// 无法区分，前端就只能靠猜。其余 /api/cards* 仍走 withLedger 的 503。
func TestLedgerHealthReportsDisabled(t *testing.T) {
	srv := newTestServer(t) // 不调 SetLedger
	req := httptest.NewRequest(http.MethodGet, "/api/ledger/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health 应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析 health 报文: %v body=%s", err, rec.Body.String())
	}
	if got["enabled"] != false {
		t.Fatalf("enabled 应为 false，实际报文: %s", rec.Body.String())
	}
}
```

**注意**：`newTestServer(t)` 与鉴权方式请照抄同目录既有测试的写法（本仓 agentd 测试有固定基座与 token 注入模式）；若既有测试用的是别的辅助名或需要带 token header，按既有写法调整，**不要新造一套基座**。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestLedgerHealthReportsDisabled -count=1`
Expected: FAIL —— 当前走 `withLedger`，未挂载账本时返回 503

- [ ] **Step 3: health 挪出 withLedger**

`internal/agentd/ledgerapi.go`，路由注册那行去掉包装：

```go
	// health 是前端的门控探针，必须恒 200：503 与网络错在浏览器侧不可区分。
	// 其余 /api/cards* 等仍走 withLedger（未挂载 = 503）。
	api.HandleFunc("GET /api/ledger/health", s.handleLedgerHealth)
```

handler 改为自己判空：

```go
// handleLedgerHealth 账本健康探针：恒 200。未启用时只回 {"enabled":false}，
// 启用时附镜像水位。前端据此决定要不要渲染账本入口。
func (s *Server) handleLedgerHealth(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	rows, err := s.ledger.MirrorHealth()
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "mirror": rows})
}
```

**注意**：`s.ledger` 的字段名与判空方式请照 `withLedger` 的实现写（打开 `ledgerapi.go` 看 `withLedger` 怎么判的，用同一个条件，别自创）。

- [ ] **Step 4: agentd 启动时按开关决定开不开库**

`cmd/agentd.go`，把现有的账本块（从注释 `// 账本镜像子系统：...` 到 `logger.Info("账本镜像未启动：无已登记 target")` 那个 else 分支结束）整体包进开关判断。改后形如：

```go
		// 账本域是可选功能（默认关）。关掉时既不开库也不起镜像，DataDir 下
		// 不会凭空多出 ledger.db；web 侧靠 /api/ledger/health 探到 enabled:false
		// 后不渲染入口。
		if !cfg.Ledger.Enabled {
			if cfg.Ledger.DSN != "" {
				// 配了 dsn 却没开开关是典型的半配状态，静默跳过会让人对着
				// 一个「配了却不生效」的库排查半天
				logger.Warn("ledger.dsn 已配置但 enabled=false，账本未启用")
			} else {
				logger.Info("账本未启用（ledger.enabled=false）")
			}
		} else {
			// 账本镜像子系统：有已登记 target 才有镜像对象；账本库按配置解析
			//（dsn 空 = DataDir/ledger.db 单机回退）。构造→go Run→Stop→Close
			// 的次序是硬约束：订阅回调在写库，Stop 必须先于账本库 Close。
			// 账本库始终打开：没有登记 target 时镜像循环不启动，但本机 web
			// 看板仍必须能读写单机回退账本。
			... 原有内容原样保留，只是缩进一层 ...
		}
```

**硬约束**：原块里的 `defer lst.Close()` / `defer lm.Stop()` 语义不能变——`defer` 在函数作用域生效，包进 `if` 块不影响注册时机与次序，但**不要**把它们改成别的关闭方式。原有的次序注释一字不改地保留。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestLedgerHealth -count=1 && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 6: 跑 agentd 全包回归**

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS（该包较慢，允许 ~90s）

- [ ] **Step 7: 加日志与注释**

确认已具备：
- 未启用时 `logger.Info("账本未启用（ledger.enabled=false）")` —— 成功路径不静默，运维能从日志确认「账本确实是被关掉的，不是崩了」
- 半配状态 `logger.Warn`，带原因
- 启用路径保留原有的 `账本镜像子系统已挂载` / `账本镜像未启动：无已登记 target` 两条日志不变
- health handler 有 doc 注释说明「为什么恒 200」

- [ ] **Step 8: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add cmd/agentd.go internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go
git commit -m "feat(agentd): 账本按 enabled 开关挂载，health 探针恒 200"
```

---

### Task 3: `card wait` 搬家（执行域去 card 感知）

**Files:**
- Create: `cmd/card_wait.go`
- Modify: `cmd/wait.go`（删 `waitCardID`/`waitSubtree` 变量声明约 67-71 行、删 `RunE` 里的 card 分发与互斥校验、删 `runCardWait` 函数体、删 flag 注册第 543-544 行）
- Modify: `cmd/card.go:464`（`cardCmd.AddCommand(...)` 追加 `cardWaitCmd`）
- Rename + Modify: `cmd/wait_card_test.go` → `cmd/card_wait_test.go`
- Test: `cmd/card_wait_test.go`

**Interfaces:**
- Consumes: Task 1 的 `openLedger()`（未启用时报错）
- Produces: `handoff card wait <id> [--subtree] [--timeout <dur>]`，行为与原 `handoff wait --card <id> --subtree` 完全等价（逐事件 JSON 到 stdout、全部成员达终态退 0、超时退 `ExitTimeout`=124）

- [ ] **Step 1: 先读懂要搬的东西**

打开 `cmd/wait.go`，定位这三处（**只读，先别改**）：
1. `waitCardID` / `waitSubtree` 两个包级变量（约 67-71 行）
2. `waitCmd.RunE` 里分发到 `runCardWait` 的分支，以及与 `<task>` 位置参数互斥的校验
3. `runCardWait` 函数全体（约 170-244 行）
4. flag 注册（约 543-544 行的 `--card` / `--subtree`）

`runCardWait` 函数体要**原样搬运**，一行逻辑都不改——它的多路 wait 语义（成员集每轮重算、`allDone` 哨兵错误、超时映射 124）是已验收的行为。

- [ ] **Step 2: 改测试（新位置、新命令行）**

`git mv cmd/wait_card_test.go cmd/card_wait_test.go`，然后：

把 `TestWaitCardSubtreeExitsWhenAllDone` 改名为 `TestCardWaitSubtreeExitsWhenAllDone`，并把调用行从

```go
	waitOut, _, err := runLedgerCLI(t, dir, "wait", "--card", root.ID, "--subtree", "--timeout", "15s")
```

改为

```go
	waitOut, _, err := runLedgerCLI(t, dir, "card", "wait", root.ID, "--subtree", "--timeout", "15s")
```

把 `TestWaitCardConflictsWithTaskArg` **整个删除**——它验的是 `wait --card` 与位置参数的互斥，搬家后这条约束不再存在（`card wait` 只有一个位置参数）。改为新增一条验证执行域已去 card 感知的用例：

```go
// TestWaitRejectsCardFlag 执行域动词必须对 card 一无所知：--card 应是未知 flag。
// 这条是「分层」这个设计裁决的回归网——有人再把账本分支塞回 wait 就会红。
func TestWaitRejectsCardFlag(t *testing.T) {
	dir := t.TempDir()
	_, _, err := runLedgerCLI(t, dir, "wait", "--card", "B1")
	if err == nil {
		t.Fatalf("wait 不应再认识 --card")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("应报未知 flag，实际: %v", err)
	}
}
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./cmd/ -run 'TestCardWait|TestWaitRejectsCardFlag' -count=1`
Expected: FAIL —— `card wait` 还不存在（报 unknown command），且 `wait --card` 目前仍被接受

- [ ] **Step 4: 建新文件**

创建 `cmd/card_wait.go`：

```go
// card wait：账本单流多路 wait。
//
// 职责：跟一张卡（或其动态重算的子树）的账本事件流，逐事件输出，全部成员
// 达骨架终态即退出。
// 边界：不碰执行域的 task wait（那是 cmd/wait.go 的 handoff wait <task>）；
// 两者是分层关系——外层用本命令管卡的调度，醒来后处置具体 task 事件仍用
// 执行域动词（reply/approve/continue）。
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/logx"
)

// cardWaitSubtree 扩展到子树（后代 + 并入成员，每轮动态重算）。
var cardWaitSubtree bool

// cardWaitTimeout 总时长，0 = 不限；超时以 ExitTimeout(124) 退出，与执行域
// wait 的超时码一致，脚本侧可用同一套判断。
var cardWaitTimeout time.Duration

// cardWaitCmd 阻塞跟随一张卡（或整棵子树）的账本事件流。
var cardWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "跟随卡的账本事件流（--subtree 跟整棵子树），全部达终态退出",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", cardWaitTimeout)
		}
		return runCardWait(cmd, args[0], cardWaitSubtree, cardWaitTimeout)
	},
}

// runCardWait 账本单流多路 wait：从当前 seq 起跟子树事件（每行一个
// JSON 事件到 stdout），全部成员达骨架终态（已完成/终止）即退出 0。
// 成员集每轮重算——wait 挂起期间新拆/新并入的卡天然进流。timeout 是
// 总时长（0=不限），超时退出码 124 与单 task wait 一致。
func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error {
	... 从 cmd/wait.go 原样搬运函数体，一行不改 ...
}

func init() {
	cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
	cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0, "总时限（如 2h）；到点以 124 退出")
}
```

**注意**：import 清单以搬过来的函数体实际用到的为准（`context`/`encoding/json`/`errors`/`fmt`/`log/slog`/`time`/`cobra`/`ledger`/`logx`），多余的删掉否则编译不过。`exitCodeError` 与 `ExitTimeout` 都在 `cmd` 包内，同包直接用。

- [ ] **Step 5: 拆掉 wait.go 里的 card 分支**

`cmd/wait.go` 删除：
1. `waitCardID`、`waitSubtree` 两个变量及其注释
2. `RunE` 里 `if waitCardID != ""` 的分发分支与相关互斥校验（保留 `--follow` 与 `--until-done` 的互斥校验，那是执行域自己的）
3. `runCardWait` 函数全体
4. flag 注册的 `--card` 与 `--subtree` 两行

删完后确认 `cmd/wait.go` 不再 import `internal/ledger`（这是 spec 判据④）。

- [ ] **Step 6: 挂到 card 命令树**

`cmd/card.go:464` 的 `cardCmd.AddCommand(...)` 调用里追加 `cardWaitCmd`。

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestCardWait|TestWaitRejectsCardFlag' -count=1`
Expected: PASS

- [ ] **Step 8: 验证判据④（wait.go 零 ledger 依赖）**

Run: `grep -c 'ledger' cmd/wait.go`
Expected: `0`（若非 0，检查残留的 import 或注释引用并清理）

- [ ] **Step 9: 跑 cmd 全包回归**

Run: `go test ./cmd/ -count=1`
Expected: PASS

- [ ] **Step 10: 加日志与注释**

确认已具备：
- `cmd/card_wait.go` 有文件头注释（职责 + 边界，含「不碰执行域 task wait」这条分层边界）
- `runCardWait` 的 doc 注释原样保留
- `cardWaitTimeout` 变量有「为什么超时码要与执行域一致」的注释

- [ ] **Step 11: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add cmd/card_wait.go cmd/wait.go cmd/card.go cmd/card_wait_test.go
git commit -m "refactor(cli): wait --card 搬为 card wait，执行域去 card 感知"
```

---

### Task 4: `card accept` 验收写入口

**Files:**
- Create: `cmd/card_records.go`
- Modify: `cmd/card.go:464`（`AddCommand` 追加 `cardAcceptCmd`）
- Test: `cmd/card_records_test.go`

**Interfaces:**
- Consumes: Task 1 的 `openLedger()`、既有 `ledgerActor() string`、既有 `(*ledger.Store).RecordAcceptance(cardID string, verified bool, evidence, actor string) error`
- Produces: `handoff card accept <id> [--evidence <文本>] [--unverified]`，落 `acceptance_recorded` 事件（`ledger.EvAcceptanceRecorded`），payload 含 `verified_on_real_machine` 与 `evidence`

- [ ] **Step 1: 写失败的测试**

创建 `cmd/card_records_test.go`：

```go
// 回合末四分法两个写入口（card accept / card needs）的 CLI 测试。
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// newTestCard 建一张卡并返回 id，供本文件各用例复用。
func newTestCard(t *testing.T, dir, title string) string {
	t.Helper()
	out, _, err := runLedgerCLI(t, dir, "card", "add", title, "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("解析建卡输出 %q: %v", out, err)
	}
	return got.ID
}

// TestCardAcceptRecordsVerified 已验必须落事件且带证据。
func TestCardAcceptRecordsVerified(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "验收卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "accept", id, "--evidence", "go test ./... 全绿"); err != nil {
		t.Fatalf("card accept: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "show", id)
	if err != nil {
		t.Fatalf("card show: %v", err)
	}
	if !strings.Contains(out, "acceptance_recorded") {
		t.Fatalf("事件流缺 acceptance_recorded: %q", out)
	}
	if !strings.Contains(out, "go test ./... 全绿") {
		t.Fatalf("事件流缺证据原文: %q", out)
	}
}

// TestCardAcceptRequiresEvidence 「已验」而不给证据必须拒绝——本项目的
// 取证文化：已验是一个断言，无证据的断言不许落账。
func TestCardAcceptRequiresEvidence(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "无证据卡")
	_, _, err := runLedgerCLI(t, dir, "card", "accept", id)
	if err == nil {
		t.Fatalf("已验不带证据应报错")
	}
	if !strings.Contains(err.Error(), "证据") {
		t.Fatalf("错误文案应提到证据，实际: %v", err)
	}
	out, _, showErr := runLedgerCLI(t, dir, "card", "show", id)
	if showErr != nil {
		t.Fatalf("card show: %v", showErr)
	}
	if strings.Contains(out, "acceptance_recorded") {
		t.Fatalf("拒绝时不得落事件: %q", out)
	}
}

// TestCardAcceptUnverified 未验可以不带证据（对应 backlog 的 done(未验)）。
func TestCardAcceptUnverified(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "未验卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "accept", id, "--unverified"); err != nil {
		t.Fatalf("card accept --unverified: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "show", id)
	if err != nil {
		t.Fatalf("card show: %v", err)
	}
	if !strings.Contains(out, "acceptance_recorded") {
		t.Fatalf("事件流缺 acceptance_recorded: %q", out)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./cmd/ -run TestCardAccept -count=1`
Expected: FAIL —— `unknown command "accept" for "handoff card"`

- [ ] **Step 3: 实现命令**

创建 `cmd/card_records.go`：

```go
// 回合末四分法的两个写入口：card accept（完成项的验收结果）与
// card needs（阻断需人工的等人标记）。
//
// 职责：把 ledger.Store 上已有的 RecordAcceptance / MarkNeedsHuman /
// ClearNeedsHuman 三个方法接出 CLI 门面。
// 边界：只落事件，不改卡状态——状态流转一律走 card move（由工作流 gate
// 校验）；验收判据文本归 card update --accept，本文件只管「验的结果」。
package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

// cardAcceptEvidence 验收证据原文（命令 + 结果）。
var cardAcceptEvidence string

// cardAcceptUnverified 落「未验」而非「已验」。
var cardAcceptUnverified bool

// cardAcceptCmd 记录验收结果。
//
// 参数：<id> 卡 id；--evidence 证据原文（已验时必填）；--unverified 落未验。
// 注意：本命令只落 acceptance_recorded 事件，不推状态。是否「验过了才能进
// 下一态」由工作流的 RequireAcceptance gate 决定，是政策不是本命令的事。
var cardAcceptCmd = &cobra.Command{
	Use:   "accept <id>",
	Short: "记验收结果（缺省已验，需 --evidence；--unverified 落未验）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		verified := !cardAcceptUnverified
		evidence := strings.TrimSpace(cardAcceptEvidence)
		// 「已验」是一个断言，无证据的断言不许落账——这是本项目取证文化的
		// 硬约束，不是可选的输入校验。未验则允许空证据（就是「还没验」）。
		if verified && evidence == "" {
			return fmt.Errorf("已验必须带证据：加 --evidence <命令与结果>，或用 --unverified 记为未验")
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		slog.Info("记验收结果", "card", args[0], "verified", verified, "evidence_bytes", len(evidence))
		if err := st.RecordAcceptance(args[0], verified, evidence, actor); err != nil {
			slog.Error("记验收结果失败", "card", args[0], "verified", verified, "err", err)
			return err
		}
		slog.Info("验收结果已落账", "card", args[0], "verified", verified)
		fmt.Fprintf(cmd.OutOrStdout(), "已记录：%s %s\n", args[0], map[bool]string{true: "已验", false: "未验"}[verified])
		return nil
	},
}
```

`init()` 与 flag 注册放在本文件末尾（Task 5 会往同一个 `init()` 里加 needs 的 flag）：

```go
func init() {
	cardAcceptCmd.Flags().StringVar(&cardAcceptEvidence, "evidence", "", "证据原文（命令 + 结果）；已验时必填")
	cardAcceptCmd.Flags().BoolVar(&cardAcceptUnverified, "unverified", false, "记为未验（证据可空）")
}
```

`cmd/card.go:464` 的 `AddCommand` 追加 `cardAcceptCmd`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestCardAccept -count=1`
Expected: PASS（三条全过）

- [ ] **Step 5: 加日志与注释（复核）**

确认已具备：
- 进入时 Info 日志带 card / verified / evidence 长度（**不打证据原文**，它可能很长）
- 错误分支 Error 日志带上下文与 cause
- **成功路径有 Info 日志**（`验收结果已落账`）——静默成功是「调试靠猜」的头号来源
- 文件头注释、`cardAcceptCmd` doc 注释、「为什么已验必须带证据」的 why 注释

- [ ] **Step 6: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add cmd/card_records.go cmd/card_records_test.go cmd/card.go
git commit -m "feat(ledger): card accept 记验收结果，已验强制带证据"
```

---

### Task 5: `card needs` 等人标记写入口

**Files:**
- Modify: `cmd/card_records.go`（追加命令，复用 Task 4 建的文件与 `init()`）
- Modify: `cmd/card.go:464`（`AddCommand` 追加 `cardNeedsCmd`）
- Test: `cmd/card_records_test.go`（追加用例）

**Interfaces:**
- Consumes: Task 1 的 `openLedger()`、既有 `ledgerActor()`、既有 `(*ledger.Store).MarkNeedsHuman(cardID, reason, actor string) error` 与 `(*ledger.Store).ClearNeedsHuman(cardID, actor string) error`、Task 4 的 `newTestCard` 测试辅助
- Produces: `handoff card needs <id> <reason...>` / `handoff card needs <id> --clear`

- [ ] **Step 1: 写失败的测试**

在 `cmd/card_records_test.go` 追加：

```go
// TestCardNeedsMarkAndClear 打等人标记后 card list --needs 可见，--clear 后消失。
func TestCardNeedsMarkAndClear(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "等人卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "needs", id, "等用户授权删远端分支"); err != nil {
		t.Fatalf("card needs: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "list", "--needs")
	if err != nil {
		t.Fatalf("card list --needs: %v", err)
	}
	if !strings.Contains(out, id) {
		t.Fatalf("打标后应出现在 --needs 列表: %q", out)
	}
	if !strings.Contains(out, "等用户授权删远端分支") {
		t.Fatalf("--needs 列表应带原因: %q", out)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "needs", id, "--clear"); err != nil {
		t.Fatalf("card needs --clear: %v", err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "list", "--needs")
	if err != nil {
		t.Fatalf("card list --needs: %v", err)
	}
	if strings.Contains(out, id) {
		t.Fatalf("清除后不应再出现: %q", out)
	}
}

// TestCardNeedsRequiresReason 打标必须给原因——「等人」不带 reason 等于
// 在注意力平面上放一个没人知道为什么的红点。
func TestCardNeedsRequiresReason(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "无因卡")
	_, _, err := runLedgerCLI(t, dir, "card", "needs", id)
	if err == nil {
		t.Fatalf("不带原因应报错")
	}
	if !strings.Contains(err.Error(), "原因") {
		t.Fatalf("错误文案应提到原因，实际: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./cmd/ -run TestCardNeeds -count=1`
Expected: FAIL —— `unknown command "needs" for "handoff card"`

- [ ] **Step 3: 实现命令**

在 `cmd/card_records.go` 追加：

```go
// cardNeedsClear 清除等人标记而不是打标。
var cardNeedsClear bool

// cardNeedsCmd 打/清等人标记。
//
// 参数：<id> 卡 id；<reason...> 原因（打标时必填，多词自动拼接）；
// --clear 清除标记（此时不需要原因）。
// 注意：本命令与节点执行器自动打的标记同源同显——审阅超轮、合并冲突那些
// 由 internal/ledgernode 落的标记走的是同一个 MarkNeedsHuman。
var cardNeedsCmd = &cobra.Command{
	Use:   "needs <id> [reason...]",
	Short: "打等人标记（原因必填）；--clear 清除",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		reason := strings.TrimSpace(strings.Join(args[1:], " "))
		// 「等人」是注意力平面上的一个红点，没有原因的红点等于噪音——
		// 看的人无从判断该做什么，只能再去翻事件流
		if !cardNeedsClear && reason == "" {
			return fmt.Errorf("等人标记必须带原因：handoff card needs %s <原因>", id)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		actor := ledgerActor()
		if cardNeedsClear {
			slog.Info("清除等人标记", "card", id)
			if err := st.ClearNeedsHuman(id, actor); err != nil {
				slog.Error("清除等人标记失败", "card", id, "err", err)
				return err
			}
			slog.Info("等人标记已清除", "card", id)
			fmt.Fprintf(cmd.OutOrStdout(), "已清除等人标记：%s\n", id)
			return nil
		}
		slog.Info("打等人标记", "card", id, "reason", reason)
		if err := st.MarkNeedsHuman(id, reason, actor); err != nil {
			slog.Error("打等人标记失败", "card", id, "reason", reason, "err", err)
			return err
		}
		slog.Info("等人标记已落账", "card", id, "reason", reason)
		fmt.Fprintf(cmd.OutOrStdout(), "已标记等人：%s（%s）\n", id, reason)
		return nil
	},
}
```

在本文件既有的 `init()` 里追加：

```go
	cardNeedsCmd.Flags().BoolVar(&cardNeedsClear, "clear", false, "清除等人标记（不需要原因）")
```

`cmd/card.go:464` 的 `AddCommand` 追加 `cardNeedsCmd`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestCardNeeds -count=1`
Expected: PASS（两条都过）

- [ ] **Step 5: 跑 cmd 全包回归**

Run: `go test ./cmd/ -count=1`
Expected: PASS

- [ ] **Step 6: 加日志与注释（复核）**

确认已具备：打标/清除两条路径各有进入 Info、错误 Error 带 cause、**成功 Info**；`cardNeedsCmd` doc 注释说明与节点执行器同源；「为什么原因必填」的 why 注释。

- [ ] **Step 7: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add cmd/card_records.go cmd/card_records_test.go cmd/card.go
git commit -m "feat(ledger): card needs 打/清等人标记，原因必填"
```

---

### Task 6: 前端账本启用探测

**Files:**
- Modify: `web/src/api/ledger.ts:104-107`（`fetchLedgerHealth` 返回类型加 `enabled`）
- Create: `web/src/app/data/useLedgerEnabled.ts`
- Test: `web/src/app/data/useLedgerEnabled.test.ts`

**Interfaces:**
- Consumes: Task 2 的 `GET /api/ledger/health` → `{"enabled":boolean, "mirror"?:[...]}`
- Produces: `useLedgerEnabled(): { enabled: boolean; loading: boolean }`。Task 7 的三个消费点用它。**契约：请求失败一律 `enabled:false`**；`loading` 为 true 期间调用方按未启用渲染（宁可晚一拍出现，也不要闪一个点进去 503 的入口）。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/data/useLedgerEnabled.test.ts`：

```ts
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useLedgerEnabled } from './useLedgerEnabled'
import * as ledgerApi from '../../api/ledger'

describe('useLedgerEnabled', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('enabled:true 时返回启用', async () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockResolvedValue({ enabled: true, mirror: [] })
    const { result } = renderHook(() => useLedgerEnabled())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.enabled).toBe(true)
  })

  it('enabled:false 时返回未启用', async () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockResolvedValue({ enabled: false })
    const { result } = renderHook(() => useLedgerEnabled())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.enabled).toBe(false)
  })

  // 请求失败按未启用处理：宁可少显示一个入口，也不要亮一个点进去就 503 的入口
  it('请求失败时按未启用处理', async () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockRejectedValue(new Error('connection refused'))
    const { result } = renderHook(() => useLedgerEnabled())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.enabled).toBe(false)
  })

  // loading 期间必须按未启用渲染，否则会闪一下账本入口再消失
  it('初始为 loading 且 enabled 为 false', () => {
    vi.spyOn(ledgerApi, 'fetchLedgerHealth').mockReturnValue(new Promise(() => {}))
    const { result } = renderHook(() => useLedgerEnabled())
    expect(result.current.loading).toBe(true)
    expect(result.current.enabled).toBe(false)
  })
})
```

**注意**：本仓 web 测试的 import 路径别名与 `renderHook` 来源请对照同目录既有测试；若既有测试用的是 `@/` 别名，改成一致的写法。

- [ ] **Step 2: 跑测试确认它失败**

Run（在 `web/` 下）: `npx vitest run src/app/data/useLedgerEnabled.test.ts`
Expected: FAIL —— 模块不存在

- [ ] **Step 3: 改 API 类型**

`web/src/api/ledger.ts`：

```ts
export interface LedgerHealth {
  // enabled 账本域总开关。未启用时后端只回这一个字段（恒 200，不是 503——
  // 503 在浏览器侧与网络错无法区分，那样前端只能靠猜）。
  enabled: boolean
  mirror?: { Target: string; LastSeq: number; UpdatedAt: string }[]
}

export const fetchLedgerHealth = () => request<LedgerHealth>('/api/ledger/health')
```

若 `fetchLedgerHealth` 已有调用方依赖旧的内联类型（`grep -rn "fetchLedgerHealth" web/src` 查一下），一并改为用 `LedgerHealth`。

- [ ] **Step 4: 写 hook**

创建 `web/src/app/data/useLedgerEnabled.ts`：

```ts
// 账本是否启用的一次性探测。
//
// 职责：查一次 /api/ledger/health，把 enabled 交给调用方做入口门控。
// 边界：只回答「开没开」，不回答镜像健康——那是 /cards 页自己的事。
// 不做轮询：开关是 agentd 启动期决定的，运行期不会变；改了配置要重启
// agentd，那时前端也会重连。
import { useEffect, useState } from 'react'
import { fetchLedgerHealth } from '../../api/ledger'

export interface LedgerEnabledState {
  enabled: boolean
  loading: boolean
}

// useLedgerEnabled 返回账本启用状态。
//
// 契约：请求失败一律按未启用处理；loading 期间 enabled 恒 false，调用方
// 据此渲染即可（宁可入口晚一拍出现，也不要闪一下再消失）。
export function useLedgerEnabled(): LedgerEnabledState {
  const [enabled, setEnabled] = useState(false)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let stopped = false
    fetchLedgerHealth()
      .then((health) => {
        if (stopped) return
        setEnabled(Boolean(health.enabled))
      })
      .catch(() => {
        // 探不到就当没开：老版本 agentd 没有这个端点、网络断开都会走到这里，
        // 两种情况下亮出账本入口都会让用户点进一个坏页面
        if (!stopped) setEnabled(false)
      })
      .finally(() => {
        if (!stopped) setLoading(false)
      })
    return () => { stopped = true }
  }, [])

  return { enabled, loading }
}
```

- [ ] **Step 5: 跑测试确认通过**

Run（在 `web/` 下）: `npx vitest run src/app/data/useLedgerEnabled.test.ts`
Expected: PASS（四条全过）

- [ ] **Step 6: 类型检查**

Run（在 `web/` 下）: `npx tsc --noEmit`
Expected: 0 错

- [ ] **Step 7: 注释复核**

确认已具备：`useLedgerEnabled.ts` 文件头注释（职责 + 边界 + 为什么不轮询）、导出函数与接口的注释、catch 分支的「为什么失败要当未启用」注释。**确认没有 `console.log`。**

- [ ] **Step 8: 提交**

```bash
git add web/src/api/ledger.ts web/src/app/data/useLedgerEnabled.ts web/src/app/data/useLedgerEnabled.test.ts
git commit -m "feat(web): 加 useLedgerEnabled 探测账本启用状态"
```

---

### Task 7: 前端三处入口门控

**Files:**
- Modify: `web/src/app/shell/Shell.tsx`（约 79-95 行的 poll 与角标、约 360-405 行的 dock props 与路由注册）
- Modify: `web/src/app/tree/ProjectTree.tsx`（约 199 行 props 签名、约 713-729 行「工作项」按钮、约 746 行「流程」按钮）
- Modify: `web/src/app/board/BoardPage.tsx:47`（`onlyUnlinked` 默认值）
- Modify: `web/src/app/board/columns.ts:157` 附近的注释（「工作项看板已是主入口」改为条件性表述）
- Test: `web/src/app/board/BoardPage.test.tsx`（追加用例；文件不存在则新建）

**Interfaces:**
- Consumes: Task 6 的 `useLedgerEnabled()`
- Produces: 账本未启用时——dock 无「工作项」与「流程」两个图标、`/cards` `/flows` 路由不注册、角标不计卡数、任务看板不默认「只看未挂账」

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/board/BoardPage.test.tsx` 追加（照抄同文件既有用例的 render 辅助与 props 构造方式；文件不存在则参照 `web/src/app/cards/` 下既有测试新建）：

```tsx
// 账本未启用时任务看板是主入口，不能默认藏起挂了卡的 task。
// ledgerEnabled=false 时 onlyUnlinked 必须为 false。
it('账本未启用时不默认只看未挂账', () => {
  render(
    <BoardPage
      tasksState={makeTasksState([taskA, taskB])}
      tree={null}
      unlinkedTaskIds={new Set([taskA.id])}
      ledgerEnabled={false}
      onOpenTask={() => {}}
    />,
  )
  // 两条 task 都应出现——未启用账本时「未挂账」这个概念不该影响可见性
  expect(screen.getByText(taskA.title)).toBeInTheDocument()
  expect(screen.getByText(taskB.title)).toBeInTheDocument()
})

it('账本启用时默认只看未挂账', () => {
  render(
    <BoardPage
      tasksState={makeTasksState([taskA, taskB])}
      tree={null}
      unlinkedTaskIds={new Set([taskA.id])}
      ledgerEnabled
      onOpenTask={() => {}}
    />,
  )
  expect(screen.getByText(taskA.title)).toBeInTheDocument()
  expect(screen.queryByText(taskB.title)).not.toBeInTheDocument()
})
```

`taskA` / `taskB` / `makeTasksState` 按同文件既有 fixture 构造；`taskA` 在 `unlinkedTaskIds` 里、`taskB` 不在。

- [ ] **Step 2: 跑测试确认它失败**

Run（在 `web/` 下）: `npx vitest run src/app/board/BoardPage.test.tsx`
Expected: FAIL —— `ledgerEnabled` prop 不存在，第一条用例里 `taskB` 被默认筛选藏掉

- [ ] **Step 3: 改 BoardPage**

`web/src/app/board/BoardPage.tsx`，props 加一个字段并改默认值：

```tsx
  // unlinkedTaskIds 未挂账 task id 集合；null = 账本未就绪，此时不做未挂账过滤
  unlinkedTaskIds?: Set<string> | null
  // ledgerEnabled 账本是否启用。未启用时本页是任务的**主入口**，不能默认
  // 只看未挂账——那会把绝大多数 task 藏起来。启用时本页降级为兜底入口。
  ledgerEnabled?: boolean
}

export function BoardPage({ tasksState, tree, unlinkedTaskIds = null, ledgerEnabled = false, onOpenTask }: BoardPageProps) {
  const [filter, setFilter] = useState<BoardFilter>(EMPTY_FILTER)
  // 默认只看未挂账**仅在账本启用时成立**：那时工作项看板（/cards）是主入口，
  // 本页降级为「账本管不到的 task」的兜底，挂了卡的 task 在卡抽屉的「关联执行」
  // 区看。账本没启用时本页就是主入口，必须显示全部。
  const [onlyUnlinked, setOnlyUnlinked] = useState(ledgerEnabled)
```

同时检查页面上「只看未挂账」那个开关控件：账本未启用时应**整体不渲染**（「未挂账」概念不出现）。找到渲染该开关的 JSX，用 `{ledgerEnabled && (...)}` 包起来。

`web/src/app/board/columns.ts:157` 附近那条「工作项看板已是主入口」的注释改为条件性表述，例如「账本启用时工作项看板是主入口，本页为兜底；未启用时本页即主入口」。

- [ ] **Step 4: 改 ProjectTree**

`web/src/app/tree/ProjectTree.tsx`：props 接口加 `ledgerEnabled?: boolean`（带注释说明「未启用时不渲染账本两个入口」），解构时给默认 `ledgerEnabled = false`；把「工作项」按钮（约 713-729 行，`aria-label="工作项"` 那个 `<button>`）与「流程」按钮（约 740-750 行，`onClick={onOpenFlows}` 那个）各自用 `{ledgerEnabled && (...)}` 包起来。

- [ ] **Step 5: 改 Shell**

`web/src/app/shell/Shell.tsx`：

1. 顶部调用 hook：`const { enabled: ledgerEnabled } = useLedgerEnabled()`（import 从 `../data/useLedgerEnabled`）
2. 两个账本轮询改为条件启用（`usePoll` 已支持 `opts.enabled`，见 `web/src/app/data/usePoll.ts:27-31`）：

```tsx
  const cardsState = usePoll(fetchCards, 2500, { enabled: ledgerEnabled })
  const decisionsState = usePoll(() => fetchDecisions(true), 2500, { enabled: ledgerEnabled })
```

3. `cardNeedsCount` 的 `useMemo` 开头加早退，未启用时恒 0：

```tsx
  const cardNeedsCount = useMemo(() => {
    // 账本未启用时角标恒 0：轮询已关，cardsState 永远是 null，这里显式返回
    // 比依赖「null 恰好算出 0」可靠
    if (!ledgerEnabled) return 0
    ...原有内容...
  }, [ledgerEnabled, cardsState.data, decisionsState.data])
```

4. 给 `<ProjectTree>` 传 `ledgerEnabled={ledgerEnabled}`
5. 给 `<BoardPage>` 传 `ledgerEnabled={ledgerEnabled}`（找到 BoardPage 的渲染点，overlay 或路由里）
6. `/cards` 与 `/flows` 两个 `<Route>` 条件注册：

```tsx
            {ledgerEnabled && <Route path="/cards" element={<CardsPage />} />}
            {ledgerEnabled && <Route path="/flows" element={<FlowsPage />} />}
```

**注意**：`<Routes>` 的直接子元素为 `false` 时 react-router 会报错。若报错，改用展开写法：把两个 Route 收进一个数组变量再展开，或者用 `{ledgerEnabled ? <><Route .../><Route .../></> : null}` —— **以本仓 react-router 版本实际能跑通的写法为准，跑测试验证**。

- [ ] **Step 6: 跑测试确认通过**

Run（在 `web/` 下）: `npx vitest run src/app/board/BoardPage.test.tsx`
Expected: PASS（两条都过）

- [ ] **Step 7: 跑 web 全量**

Run（在 `web/` 下）: `npx tsc --noEmit && npx vitest run`
Expected: tsc 0 错；vitest 全绿。**若有既有用例因新增 required prop 而红**，说明 prop 没给默认值——补默认值而不是改测试。

- [ ] **Step 8: 构建 + lint**

Run（在 `web/` 下）: `npm run build && npx eslint src --max-warnings 13`
Expected: build 通过；eslint 0 error（本仓有 13 条既有 warning，不新增即可）

- [ ] **Step 9: 注释复核**

确认已具备：三个改动点各有「为什么」注释（BoardPage 的主/兜底入口切换、ProjectTree 的入口隐藏、Shell 的角标早退）。**确认没有 `console.log`。**

- [ ] **Step 10: 提交**

```bash
git add web/src/app/shell/Shell.tsx web/src/app/tree/ProjectTree.tsx web/src/app/board/BoardPage.tsx web/src/app/board/columns.ts web/src/app/board/BoardPage.test.tsx
git commit -m "feat(web): 账本未启用时不渲染入口，任务看板回主入口形态"
```

---

### Task 8: 全量门与端到端手工验证

**Files:**
- 无新增；本 task 只跑验证并修出现的问题

**Interfaces:**
- Consumes: Task 1-7 的全部产出
- Produces: 一份可信的「全绿」结论

- [ ] **Step 1: 后端全量**

```bash
gofmt -l . | grep -v '^web/'
go build ./... && go vet ./... && go test ./... -count=1
```

Expected: gofmt 无输出；build/vet 退 0；test 全绿。

**沙箱注意**：若在受限沙箱内跑，可能出现与本次改动无关的环境性失败（`mktemp` 被拒、`sysctl` 权限、临时目录相关的 panic）。这类失败要**如实记进 ledger 并说明形状**，不许写成「全绿」，也不许当成本次改动引入的红。

- [ ] **Step 2: 前端全量**

```bash
cd web && npx tsc --noEmit && npx vitest run && npm run build
```

Expected: 全部通过。

- [ ] **Step 3: 红线自检**

```bash
grep -rn 'fmt\.Printf' internal/ cmd/ | grep -v '_test.go'
grep -rn 'console\.log' web/src | grep -v test
```

Expected: 两条都无输出（若 `internal/` 有既有的 `fmt.Printf`，确认不是本次新增的）。

- [ ] **Step 4: 把 Step 1-3 的实际输出写进 ledger**

按纪律块要求落 ledger：每条命令的**实际输出**（不是预期），沙箱受限导致的失败如实标注形状。**没跑到结果的不许写结论。**

**本 task 到此为止。** 端到端的真机验证（起实例、敲 `handoff card ...`）**不属于执行者范围**——纪律块禁止调用 handoff CLI，那部分见文末「审核者本地验收清单」，由协调者执行。

- [ ] **Step 5: 提交（如有修复）**

```bash
gofmt -l . | grep -v '^web/'
git add -A
git commit -m "fix(ledger): 全量门与端到端验证发现的问题修复"
```

若 Step 1-6 全绿无需修复，本步跳过，不要造空提交。

---

## 附一：审核者本地验收清单（**不派发**，协调者执行）

以下步骤要敲 `handoff` CLI 或起 agentd 实例，与 B 版纪律块的「不要调用
handoff CLI、不要起任何新的 executor 进程」直接冲突，**故意留在派发范围
之外**。执行者跑完 Task 1-8 后由协调者本地补做。

**A. 默认关（spec 判据①）**

```bash
mkdir -p /tmp/ledger-off && printf 'listen: 127.0.0.1:0\ntoken: t\ndatadir: /tmp/ledger-off\nstalltimeout: 2h\n' > /tmp/ledger-off/config.yaml
go run . --config /tmp/ledger-off/config.yaml card add 测试 --project demo; ls /tmp/ledger-off/
```

判据：报「账本未启用」非 0 退出；目录里**没有** `ledger.db`。

**B. 打开后全族可用（判据②⑤⑥）**

```bash
mkdir -p /tmp/ledger-on && printf 'listen: 127.0.0.1:0\ntoken: t\ndatadir: /tmp/ledger-on\nstalltimeout: 2h\nledger:\n  enabled: true\n' > /tmp/ledger-on/config.yaml
go run . --config /tmp/ledger-on/config.yaml card add 测试卡 --project demo --workflow bug
```

取输出里的 id 记为 `<ID>`：

```bash
go run . --config /tmp/ledger-on/config.yaml card accept <ID> --evidence "go test ./... 全绿"
go run . --config /tmp/ledger-on/config.yaml card needs <ID> "等用户拍板"
go run . --config /tmp/ledger-on/config.yaml card list --needs
go run . --config /tmp/ledger-on/config.yaml card show <ID>
go run . --config /tmp/ledger-on/config.yaml card accept <ID>   # 应报「已验必须带证据」
```

**C. web 门控真机走查（判据③）**

起**隔离**实例（独立 DataDir + 端口，**绝不重启 launchd 托管的生产
agentd**——它会用旧二进制把改动顶回去）。分别用 `ledger.enabled` 关/开
两份 config 各起一次，在真实控制台确认：关时 dock 无「工作项」与「流程」
两个图标、`/cards` 直达不渲染看板、任务看板无「只看未挂账」开关；开时
三者恢复现状。登录走 `handoff console --print-url`（**一次性 ticket，
谁先打开谁消费掉**，自己验过一次要重开一张给用户）。

**D. 清理**

```bash
rm -rf /tmp/ledger-off /tmp/ledger-on
```

## 附二：本 plan 明确不做

- **`skills/handoff/SKILL.md` 的「账本模式」节**（spec §6）——由协调者本地写，执行者**不要动这个文件**。
- web 的验收开关 UI、「按节点派发」按钮、子任务树 rollup。
- 存量切换、`backlog.md` 冻结、合回 main。
- 合并节点的 origin 依赖问题（spec §7 列的 D 组三条观察）。
