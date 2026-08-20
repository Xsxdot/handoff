# 账本可选化与命令分层 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) 语法跟踪。

**Goal:** 账本功能变为显式 opt-in（`ledger.enabled`，默认关），执行域命令回到零 card 感知（`wait --card` 搬到 `card wait`），并补齐验收（`card accept`）与等人标记（`card needs`）两个写入口。

**Architecture:** 一个机器级开关喂三面——agentd（不开库、不起镜像、health 探针恒 200 报 enabled）、CLI（`openLedger()` 单点拦截）、web（dock/路由/看板筛选按探针门控）。执行域与账本域分层：核心动词零 ledger 引用，账本域三族命令包装执行域。

**Tech Stack:** Go（cobra CLI、`log/slog`、内部 `internal/ledger` store）、React + TS（vitest + testing-library）。

**Spec:** `docs/superpowers/specs/2026-08-20-ledger-optional-and-layering-design.md`

## Global Constraints

- 工作分支基线：`feat/b156-workbench-ledger`（不是 main）。
- Go 日志一律 `log/slog`（agentd 侧沿用现有 `logger` 变量），**禁止 `fmt.Printf` 当日志**；web 侧禁止 `console.log`。
- **任何日志不得输出 `cfg.Ledger.DSN` 的值**——DSN 含库凭据。
- 新文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc comment；非显然分支写「为什么」的中文注释。
- 每个 task 结束跑 `gofmt -l . | grep -v '^web/'`（应无输出）再 commit。
- CLI 命令的 stdout 契约：单 JSON 对象/逐行 JSON，人读的提示走 stderr——与既有 card 族一致。
- 不改 `skills/handoff/SKILL.md`（skill 改写由审核者本地完成，不在本计划内）。
- 不做：web 验收 UI、「按节点派发」按钮、子树 rollup、存量切换（spec §7）。

---

### Task 1: `ledger.enabled` 配置开关

**Files:**
- Modify: `internal/config/config.go:143`（LedgerConfig）、`internal/config/config.go`（decodeStrict 报错文案，搜 `ledger{dsn}`）
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.LedgerConfig{Enabled bool, DSN string}`——后续所有 task 读 `cfg.Ledger.Enabled` 判启用。

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 追加：

```go
// TestLedgerEnabledParse 验证 ledger.enabled 键能被严格解码接受并正确落位。
func TestLedgerEnabledParse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:0\ntoken: t\ndatadir: " + dir + "\nledger:\n  enabled: true\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Ledger.Enabled {
		t.Fatal("ledger.enabled=true 未落位")
	}
	if cfg.Ledger.DSN != "" {
		t.Fatalf("dsn 应为空, got %q", cfg.Ledger.DSN)
	}
}

// TestLedgerEnabledDefaultFalse 无 ledger 段时开关必须是关——可选化的根。
func TestLedgerEnabledDefaultFalse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:0\ntoken: t\ndatadir: " + dir + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Ledger.Enabled {
		t.Fatal("缺省必须未启用")
	}
}
```

（若文件顶部缺 `os`/`filepath` import 则补。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run 'TestLedgerEnabled' -v`
Expected: `TestLedgerEnabledParse` FAIL（`cfg.Ledger.Enabled` 字段不存在，编译错）。

- [ ] **Step 3: 实现**

`internal/config/config.go:143` 的 LedgerConfig 加字段：

```go
// LedgerConfig 账本域（任务卡）中心库配置。只描述本机如何连库，
// 不描述库里有什么——schema 归 internal/ledger 管。
type LedgerConfig struct {
	// Enabled 账本总开关，默认 false。false 时 agentd 不开账本库、
	// 不起事件镜像，CLI 账本三族命令报未启用，web 不渲染账本入口。
	// 为什么不用「dsn 非空」当信号：单机 SQLite 回退恰恰没有 dsn。
	Enabled bool `yaml:"enabled,omitempty"`
	// DSN 形如 postgres://user:pass@host:5432/db。空 = SQLite 回退。
	DSN string `yaml:"dsn,omitempty"`
}
```

decodeStrict 报错文案里 `ledger{dsn}` 改为 `ledger{enabled,dsn}`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -v -run 'TestLedger|TestDecode|TestLoad'`
Expected: PASS（含既有用例不回归）。

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): ledger.enabled 账本总开关（默认关）"
```

---

### Task 2: agentd 门控与 health 探针

**Files:**
- Modify: `cmd/agentd.go:241-280`（账本块整体包进开关）
- Modify: `internal/agentd/ledgerapi.go:24-31`（health 路由挪出 withLedger）、`internal/agentd/ledgerapi.go`（withLedger 503 文案）
- Test: `internal/agentd/ledgerapi_test.go`（或该包既有 API 测试文件，就近追加）

**Interfaces:**
- Consumes: Task 1 的 `cfg.Ledger.Enabled`。
- Produces: `GET /api/ledger/health` 恒 200，返回 `{"enabled":bool,"mirror":[]|null}`——Task 6 前端探针的契约。其余 `/api/cards*` 等未启用时仍 503。

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/ledgerapi_test.go` 追加（复用同文件既有基座 `newTestAgentdEnv(t)` + `ledgerGet(t, env, path)` + `ledgerContainsAll`；`TestLedgerAPIWithoutLedger` 已存在并断言 `/api/cards` 503，保留不动）：

```go
// TestLedgerHealthReportsDisabled 未启用账本时探针必须 200 报 enabled=false，
// 而不是 503——前端靠它做门控，503 无法与「agentd 挂了」区分。
func TestLedgerHealthReportsDisabled(t *testing.T) {
	env := newTestAgentdEnv(t) // 不 SetLedger = 未启用
	code, body := ledgerGet(t, env, "/api/ledger/health")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d %q", code, body)
	}
	if !ledgerContainsAll(body, `"enabled":false`) {
		t.Fatalf("want enabled=false, got %q", body)
	}
}
```

已配 ledger 的路径（`TestLedgerAPI` 里若打到 `/api/ledger/health`）断言同步补 `"enabled":true`；响应新增字段不破坏 contains 型断言，跑包内测试后按红改。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestLedgerHealthReportsDisabled' -v`
Expected: FAIL——health 现在挂在 withLedger 后面，未 SetLedger 时回 503。

- [ ] **Step 3: 实现 health 挪出与文案**

`internal/agentd/ledgerapi.go`：

```go
// 路由注册处：health 不过 withLedger——它是前端门控探针，必须恒可达
api.HandleFunc("GET /api/ledger/health", s.handleLedgerHealth)
```

```go
// handleLedgerHealth 账本探针：未启用恒 200 报 enabled=false（不能用 503
// 当信号，前端无法区分「未启用」与「agentd 挂了」）；启用时附镜像水位。
func (s *Server) handleLedgerHealth(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "mirror": nil})
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

withLedger 的 503 文案改为：`"账本未启用（config.yaml 设 ledger.enabled: true）"`。

- [ ] **Step 4: 实现 agentd 启动门控**

`cmd/agentd.go:241-280` 现有账本块（`ldsn := cfg.Ledger.DSN` 起到 `logger.Info("账本镜像未启动：无已登记 target")` 止）整体包进：

```go
if cfg.Ledger.Enabled {
	// ……现有整块原样内移：Open→EnsureDefaults→SetLedger→镜像子系统……
} else {
	if cfg.Ledger.DSN != "" {
		// 只报「配了没开」这个事实，绝不打 DSN 值——里面有库凭据
		logger.Warn("ledger.dsn 已配置但 ledger.enabled=false，账本未启用")
	}
	logger.Info("账本未启用（ledger.enabled=false），事件镜像与账本 API 关闭")
}
```

块顶注释「账本库始终打开」相应改为「账本库仅在 enabled 时打开」。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -v -run 'Ledger'` 然后 `go build ./... && go vet ./...`
Expected: 全 PASS，编译干净。

- [ ] **Step 6: Commit**

```bash
git add cmd/agentd.go internal/agentd/
git commit -m "feat(agentd): 账本按 ledger.enabled 门控，health 探针恒 200 报启用态"
```

---

### Task 3: CLI 单点门 `openLedger`

**Files:**
- Modify: `cmd/ledgercli.go`（openLedger 头部加拦截）
- Modify: `cmd/ledgercli_test.go:26`（测试基座最小 config 补 `Ledger: config.LedgerConfig{Enabled: true}`）
- Test: `cmd/ledgercli_test.go`

**Interfaces:**
- Consumes: Task 1 的 `cfg.Ledger.Enabled`；既有 `loadCLIConfig()`。
- Produces: 未启用时账本三族（card/workflow/decision）统一报错文案——Task 4/5 的新命令走同一个门，无需各自判断。

- [ ] **Step 1: 改测试基座（先做——否则所有既有账本测试在 Step 3 后齐红）**

`cmd/ledgercli_test.go:26` 的最小 config 补开关：

```go
c := &config.Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir,
	StallTimeout: 2 * time.Hour, Ledger: config.LedgerConfig{Enabled: true}}
```

- [ ] **Step 2: 写失败测试**

同文件追加：

```go
// TestLedgerDisabledBlocksCardFamily 未启用时账本命令族必须统一报未启用
// 文案，而不是静默自建 ledger.db——可选化的 CLI 面。
func TestLedgerDisabledBlocksCardFamily(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	c := &config.Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir, StallTimeout: 2 * time.Hour}
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatal(err)
	}
	resetAllFlags(rootCmd)
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"--config", cfgPath, "card", "list"})
	err := Execute()
	if err == nil || !strings.Contains(err.Error(), "账本未启用") {
		t.Fatalf("want 未启用报错, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ledger.db")); !os.IsNotExist(statErr) {
		t.Fatal("未启用时不得自建 ledger.db")
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestLedgerDisabledBlocksCardFamily' -v`
Expected: FAIL——现在 openLedger 不看开关，card list 正常执行。

- [ ] **Step 4: 实现**

`cmd/ledgercli.go` 的 `openLedger()` 顶部：

```go
func openLedger() (*ledger.Store, error) {
	cfg := loadCLIConfig()
	// 单点门：账本三族命令全部经这里开库，未启用在此统一拦截，
	// 各命令无需自带判断（spec §3）
	if !cfg.Ledger.Enabled {
		return nil, fmt.Errorf("账本未启用：在 config.yaml 设 ledger.enabled: true（可选 ledger.dsn 连中心库，缺省本机 SQLite）")
	}
	// ……以下原样……
}
```

- [ ] **Step 5: 跑包内全量确认无回归**

Run: `go test ./cmd/ -count=1`
Expected: 全 PASS（基座已在 Step 1 补开关，既有账本用例不受影响）。

- [ ] **Step 6: Commit**

```bash
git add cmd/ledgercli.go cmd/ledgercli_test.go
git commit -m "feat(cli): openLedger 单点拦截未启用账本，测试基座显式开启"
```

---

### Task 4: `card wait` 搬家，`wait.go` 去 ledger 化

**Files:**
- Create: `cmd/card_wait.go`
- Modify: `cmd/wait.go`（删 `--card`/`--subtree` flag、互斥校验、`runCardWait`，Args 收回 ExactArgs(1)）
- Create: `cmd/card_wait_test.go`（迁自 `cmd/wait_card_test.go`）
- Delete: `cmd/wait_card_test.go`

**Interfaces:**
- Consumes: Task 3 的 `openLedger()` 门；既有 `exitCodeError`/`ExitTimeout`/`logx.Setup`。
- Produces: `handoff card wait <id> [--subtree] [--timeout <时长>]`，行为与原 `wait --card` 逐字等价（stdout 逐行 JSON 事件、全员终态退出 0、超时退出码 `ExitTimeout`）。

- [ ] **Step 1: 新建 `cmd/card_wait.go`**

```go
// card wait：账本单流多路 wait。跟一张卡（或 --subtree 整棵树）的事件流，
// 全部成员达骨架终态（已完成/终止）即退出 0。
//
// 边界：本文件只有账本 wait；单 task 的 wait 在 cmd/wait.go（执行域，
// 零 ledger 引用——分层原则见 spec §2）。
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/spf13/cobra"
)

var (
	// cardWaitSubtree 扩展到子树（后代 + 并入成员，每轮动态重算）。
	cardWaitSubtree bool
	// cardWaitTimeout 总时长上限，0=不限；超时以 ExitTimeout 退出，
	// 与单 task wait 的语义一致（脚本按退出码分流）。
	cardWaitTimeout time.Duration
)

var cardWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "账本单流多路 wait：跟一张卡（--subtree 跟整棵树），全员终态退出",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardWaitTimeout < 0 {
			return fmt.Errorf("--timeout 必须为正时长（当前 %s）；不设上限请省略该参数", cardWaitTimeout)
		}
		return runCardWait(cmd, args[0], cardWaitSubtree, cardWaitTimeout)
	},
}

func init() {
	cardWaitCmd.Flags().BoolVar(&cardWaitSubtree, "subtree", false, "扩展到子树（后代 + 并入成员，动态）")
	cardWaitCmd.Flags().DurationVar(&cardWaitTimeout, "timeout", 0, "总时长上限（如 1h），0=不限；超时退出码与单 task wait 一致")
	cardCmd.AddCommand(cardWaitCmd)
}
```

然后把 `cmd/wait.go` 的 `runCardWait` 函数**原样剪切**到本文件末尾（函数体一字不改，含注释）。

- [ ] **Step 2: 净化 `cmd/wait.go`**

按序删：
1. `waitCardID` / `waitSubtree` 两个包级变量及注释（`cmd/wait.go:67-71`）；
2. init 里 `--card`/`--subtree` 两行 flag 注册（`cmd/wait.go:543-544`）；
3. RunE 里 `if waitCardID != "" { ... }` 与 `if len(args) != 1 { ... }` 两段，恢复为：

```go
		taskID := args[0]
		// main 的 relay 改造把带 token 的 client 构造收进 newTargetClient()，
		// 这里只还需要 addr 做日志与 pull 的落点，token 不再单独取
		addr, _, err := TargetEndpoint()
```

4. `Args: cobra.MaximumNArgs(1)` 改回 `Args: cobra.ExactArgs(1)`；
5. 删掉 wait.go 里因搬家而不再使用的 import（编译器会点名；预期 `ledger` 必删，`encoding/json`/`errors` 若仍被本文件其他函数用则保留）。

- [ ] **Step 3: 迁测试**

新建 `cmd/card_wait_test.go`，把 `TestWaitCardSubtreeExitsWhenAllDone` 整体搬入并改两处：函数名 `TestCardWaitSubtreeExitsWhenAllDone`，调用行改为：

```go
	waitOut, _, err := runLedgerCLI(t, dir, "card", "wait", root.ID, "--subtree", "--timeout", "15s")
```

追加两条守门用例：

```go
// TestWaitCardFlagRemoved 执行域 wait 不再认 --card——分层后账本地址
// 只存在于 card 族（spec §4.1）。
func TestWaitCardFlagRemoved(t *testing.T) {
	dir := t.TempDir()
	_, _, err := runLedgerCLI(t, dir, "wait", "--card", "B1")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("want unknown flag, got %v", err)
	}
}

// TestWaitRequiresExactlyOneArg wait 收回 ExactArgs(1)。
func TestWaitRequiresExactlyOneArg(t *testing.T) {
	dir := t.TempDir()
	_, _, err := runLedgerCLI(t, dir, "wait")
	if err == nil {
		t.Fatal("零参数应报错")
	}
}
```

删除 `cmd/wait_card_test.go`（其中 `TestWaitCardConflictsWithTaskArg` 随 flag 一起废止——互斥场景已不存在）。

- [ ] **Step 4: 验证零 ledger 引用 + 全量测试**

Run: `grep -c "ledger" cmd/wait.go`（Expected: `0`）；`go build ./... && go test ./cmd/ -count=1`
Expected: 编译过、全 PASS。

- [ ] **Step 5: Commit**

```bash
git add cmd/wait.go cmd/card_wait.go cmd/card_wait_test.go
git rm cmd/wait_card_test.go
git commit -m "refactor(cli): wait --card 搬到 card wait，执行域动词回到零 ledger 引用"
```

---

### Task 5: `card accept` 与 `card needs` 写入口

**Files:**
- Create: `cmd/card_accept.go`
- Create: `cmd/card_needs.go`
- Test: `cmd/card_accept_test.go`、`cmd/card_needs_test.go`

**Interfaces:**
- Consumes: `st.RecordAcceptance(cardID string, verified bool, evidence, actor string) error`（`internal/ledger/events.go:214`）、`st.MarkNeedsHuman(cardID, reason, actor string) error`（`events.go:227`）、`st.ClearNeedsHuman(cardID, actor string) error`（`events.go:241`）、`openLedger()`、`ledgerActor()`。
- Produces: `card accept <id> --evidence <文本> [--unverified]`、`card needs <id> <reason...>` / `card needs <id> --clear`。

- [ ] **Step 1: 写失败测试 `cmd/card_accept_test.go`**

```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// 验收写入口：已验必须带证据；事件落 card show 的事件流。
func TestCardAcceptRecordsVerifiedEvent(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "验收卡", "--project", "demo", "--workflow", "bug")
	var card struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)

	if _, _, err := runLedgerCLI(t, dir, "card", "accept", card.ID, "--evidence", "go test ./... 全绿"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	showOut, _, err := runLedgerCLI(t, dir, "card", "show", card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showOut, "acceptance_recorded") || !strings.Contains(showOut, "go test ./... 全绿") {
		t.Fatalf("事件缺失: %q", showOut)
	}
}

// 已验缺证据必须拒绝且不落事件——取证文化：没有证据的「已验」是假账。
func TestCardAcceptVerifiedRequiresEvidence(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "验收卡", "--project", "demo", "--workflow", "bug")
	var card struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)

	_, _, err := runLedgerCLI(t, dir, "card", "accept", card.ID)
	if err == nil || !strings.Contains(err.Error(), "--evidence") {
		t.Fatalf("want evidence 报错, got %v", err)
	}
	showOut, _, _ := runLedgerCLI(t, dir, "card", "show", card.ID)
	if strings.Contains(showOut, "acceptance_recorded") {
		t.Fatal("报错路径不得落事件")
	}
}

// --unverified 允许无证据（对应 done(未验) 的形态）。
func TestCardAcceptUnverified(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "验收卡", "--project", "demo", "--workflow", "bug")
	var card struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)

	if _, _, err := runLedgerCLI(t, dir, "card", "accept", card.ID, "--unverified"); err != nil {
		t.Fatalf("unverified accept: %v", err)
	}
	showOut, _, _ := runLedgerCLI(t, dir, "card", "show", card.ID)
	if !strings.Contains(showOut, `"verified_on_real_machine":false`) {
		t.Fatalf("未验事件缺失: %q", showOut)
	}
}
```

- [ ] **Step 2: 写失败测试 `cmd/card_needs_test.go`**

```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// 等人标记写入口：打上后 card list --needs 可见，--clear 后消失。
func TestCardNeedsMarkAndClear(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "等人卡", "--project", "demo", "--workflow", "bug")
	var card struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)

	if _, _, err := runLedgerCLI(t, dir, "card", "needs", card.ID, "等", "mac-02", "授权"); err != nil {
		t.Fatalf("needs: %v", err)
	}
	listOut, _, _ := runLedgerCLI(t, dir, "card", "list", "--needs")
	if !strings.Contains(listOut, card.ID) {
		t.Fatalf("--needs 应可见 %s: %q", card.ID, listOut)
	}

	if _, _, err := runLedgerCLI(t, dir, "card", "needs", card.ID, "--clear"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	listOut, _, _ = runLedgerCLI(t, dir, "card", "list", "--needs")
	if strings.Contains(listOut, card.ID) {
		t.Fatalf("clear 后不应再见 %s: %q", card.ID, listOut)
	}
}

// 标等人不带 reason 必须拒绝（store 层已强制，CLI 提前给出可读文案）。
func TestCardNeedsRequiresReason(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "等人卡", "--project", "demo", "--workflow", "bug")
	var card struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)

	_, _, err := runLedgerCLI(t, dir, "card", "needs", card.ID)
	if err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("want reason 报错, got %v", err)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestCardAccept|TestCardNeeds' -v`
Expected: FAIL（`unknown command "accept"` / `"needs"`）。

- [ ] **Step 4: 实现 `cmd/card_accept.go`**

```go
// card accept：验收结果写入口。判据文本归 card update --accept（写「怎么才
// 算验过」）；本命令写「验的结果」——落 acceptance_recorded 事件，已验/待
// 真机验由最后一条该事件推导。
//
// 边界：不校验判据是否存在——落不落验收记录永远自愿，只有工作流配了
// RequireAcceptance gate 它才成为那条流的门（spec §4.2）。
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

var (
	cardAcceptEvidence   string
	cardAcceptUnverified bool
)

var cardAcceptCmd = &cobra.Command{
	Use:   "accept <id>",
	Short: "落验收结果事件：--evidence 证据（已验必填）；--unverified 记为未验",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		verified := !cardAcceptUnverified
		// 已验必须带证据：没有证据的「已验」是假账（取证文化，与
		// backlog done(已验) 需测试证据同一条纪律）
		if verified && strings.TrimSpace(cardAcceptEvidence) == "" {
			return fmt.Errorf("已验必须带证据：--evidence 不能为空（确实未验请用 --unverified）")
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.RecordAcceptance(args[0], verified, cardAcceptEvidence, ledgerActor()); err != nil {
			return err
		}
		slog.Info("验收已落账", "card", args[0], "verified", verified)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"ok": true, "card": args[0], "verified": verified,
		})
	},
}

func init() {
	cardAcceptCmd.Flags().StringVar(&cardAcceptEvidence, "evidence", "", "验收证据（命令 + 结果，如 'go test ./... 全绿'）")
	cardAcceptCmd.Flags().BoolVar(&cardAcceptUnverified, "unverified", false, "记为未验（证据可空）")
	cardCmd.AddCommand(cardAcceptCmd)
}
```

- [ ] **Step 5: 实现 `cmd/card_needs.go`**

```go
// card needs：等人标记写入口。回合末四分法的「阻断需人工」一格由此有门；
// 与节点执行器自动打的标记同源（都走 MarkNeedsHuman），看板同显。
//
// 边界：reason 语义由 store 强制非空；标记不落列，从最后一条
// needs_human/needs_cleared 事件推导（spec §4.3）。
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

var cardNeedsClear bool

var cardNeedsCmd = &cobra.Command{
	Use:   "needs <id> [reason...]",
	Short: "打等人标记（reason 必填）；--clear 解除",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id := args[0]
		if cardNeedsClear {
			if len(args) > 1 {
				return fmt.Errorf("--clear 不带 reason（解除就是解除，理由写 card note）")
			}
			if err := st.ClearNeedsHuman(id, ledgerActor()); err != nil {
				return err
			}
			slog.Info("等人标记已解除", "card", id)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"ok": true, "card": id, "needs": ""})
		}
		reason := strings.TrimSpace(strings.Join(args[1:], " "))
		if reason == "" {
			return fmt.Errorf("标等人必须带 reason（如 card needs %s 等 mac-02 授权；解除用 --clear）", id)
		}
		if err := st.MarkNeedsHuman(id, reason, ledgerActor()); err != nil {
			return err
		}
		slog.Info("等人标记已打上", "card", id, "reason", reason)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"ok": true, "card": id, "needs": reason})
	},
}

func init() {
	cardNeedsCmd.Flags().BoolVar(&cardNeedsClear, "clear", false, "解除等人标记")
	cardCmd.AddCommand(cardNeedsCmd)
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestCardAccept|TestCardNeeds' -v && go test ./cmd/ -count=1`
Expected: 新用例 PASS，包内无回归。

- [ ] **Step 7: Commit**

```bash
git add cmd/card_accept.go cmd/card_needs.go cmd/card_accept_test.go cmd/card_needs_test.go
git commit -m "feat(cli): card accept 验收写入口 + card needs 等人标记入口"
```

---

### Task 6: web 门控

**Files:**
- Modify: `web/src/api/ledger.ts:105-108`（fetchLedgerHealth 类型）
- Create: `web/src/app/data/useLedgerEnabled.ts`
- Modify: `web/src/app/shell/Shell.tsx`（79-95 轮询与角标、368-370 与 392-400 入口与路由）
- Modify: `web/src/app/tree/ProjectTree.tsx`（Props、199、714-750 两个按钮）
- Modify: `web/src/app/board/BoardPage.tsx`（Props、46-54 默认筛选与开关）
- Modify: `web/src/app/board/columns.ts:157`（注释改条件性表述）
- Test: `web/src/app/tree/ProjectTree.test.tsx`、`web/src/app/board/BoardPage.test.tsx`（就近追加用例；文件名以实际为准，先 `ls` 再落）

**Interfaces:**
- Consumes: Task 2 的 `{"enabled":bool,"mirror":[]|null}`。
- Produces: `useLedgerEnabled(): boolean`；`ProjectTree` 与 `BoardPage` 各新增 `ledgerEnabled: boolean` prop。

- [ ] **Step 1: 改 API 类型**

`web/src/api/ledger.ts`：

```ts
export const fetchLedgerHealth = () =>
  request<{
    enabled: boolean
    mirror: { Target: string; LastSeq: number; UpdatedAt: string }[] | null
  }>('/api/ledger/health')
```

- [ ] **Step 2: 新建 `web/src/app/data/useLedgerEnabled.ts`**

```ts
// 账本启用探测：挂载时查一次 /api/ledger/health。
// 请求失败按未启用处理——宁可少显示入口，也不显示一个点进去 503 的
// 入口（agentd 版本旧或网络错时，账本入口整体隐身，spec §5）。
import { useEffect, useState } from 'react'
import { fetchLedgerHealth } from '../../api/ledger'

export function useLedgerEnabled(): boolean {
  const [enabled, setEnabled] = useState(false)
  useEffect(() => {
    let alive = true
    fetchLedgerHealth()
      .then((health) => { if (alive) setEnabled(health.enabled) })
      .catch(() => { if (alive) setEnabled(false) })
    return () => { alive = false }
  }, [])
  return enabled
}
```

- [ ] **Step 3: Shell 接线**

`web/src/app/shell/Shell.tsx`：

```tsx
const ledgerEnabled = useLedgerEnabled()
// 未启用时不轮询账本端点——它们只会 503 刷日志
const cardsState = usePoll(fetchCards, 2500, { enabled: ledgerEnabled })
const decisionsState = usePoll(() => fetchDecisions(true), 2500, { enabled: ledgerEnabled })
```

- `/cards`、`/flows` 两条 `<Route>` 包成 `{ledgerEnabled && <Route ... />}`；
- `<ProjectTree ... ledgerEnabled={ledgerEnabled} />`；
- BoardPage 的渲染处传 `ledgerEnabled={ledgerEnabled}`；
- `Shell.tsx:88` 起「任务看板降级为兜底」注释改为条件性表述（「账本启用时任务看板降级为未挂账兜底；未启用时它就是主入口」）。

- [ ] **Step 4: ProjectTree 门控**

Props 加 `ledgerEnabled?: boolean`（缺省 false——探针没回来前不显示，回来了再长出来）；「工作项」按钮（`:714-728`）与「流程」按钮（`:746` 一带）包成 `{ledgerEnabled && (<button ...>...)}`。`ProjectTree.tsx:75` 注释同步改条件性表述。

- [ ] **Step 5: BoardPage 门控**

Props 加 `ledgerEnabled?: boolean`（缺省 false）。改动两处：

```tsx
// 账本启用时默认只看未挂账（工作项看板是主入口，本页兜底）；
// 未启用时本页就是主入口，不做未挂账过滤，开关也不出现。
const [onlyUnlinked, setOnlyUnlinked] = useState(true)
const unlinkedFilterOn = ledgerEnabled && onlyUnlinked
const filtered = applyFilter(unlinkedFilterOn ? unlinkedOnly(tasks, unlinkedTaskIds) : tasks, filter, projects)
```

「只看未挂账」开关的 JSX 包 `{ledgerEnabled && (...)}`。`columns.ts:157` 注释改条件性表述。

- [ ] **Step 6: 组件测试**

`web/src/app/tree/ProjectTree.test.tsx` 有 `props({...})` 覆写工厂（文件头有使用说明，先读它）；两个按钮的 aria-label 实测为「工作项」「流程」。追加：

```tsx
it('账本未启用时不渲染工作项与流程入口', () => {
  render(<ProjectTree {...props({ ledgerEnabled: false })} />)
  expect(screen.queryByLabelText('工作项')).toBeNull()
  expect(screen.queryByLabelText('流程')).toBeNull()
})

it('账本启用时渲染工作项与流程入口', () => {
  render(<ProjectTree {...props({ ledgerEnabled: true })} />)
  expect(screen.getByLabelText('工作项')).toBeInTheDocument()
  expect(screen.getByLabelText('流程')).toBeInTheDocument()
})
```

注意既有用例未传 `ledgerEnabled`（缺省 false）——若其中有断言「工作项」按钮存在的用例，给它补 `ledgerEnabled: true` 而不是改缺省值。

`web/src/app/board/BoardPage.test.tsx` 沿同文件既有 fixture 追加：

```tsx
it('账本未启用时无未挂账筛选，任务全量显示', () => {
  // fixture 抄同文件既有用例：一个挂账 task + 一个未挂账 task，
  // unlinkedTaskIds 只含后者；断言两者都渲染且开关文案不出现
  render(<BoardPage {...既有基座参数} ledgerEnabled={false} unlinkedTaskIds={new Set(['t1'])} />)
  expect(screen.queryByText(/只看未挂账/)).toBeNull()
})
```

- [ ] **Step 7: 跑前端门**

Run: `cd web && npx tsc --noEmit && npx vitest run`
Expected: 0 错、全绿。

- [ ] **Step 8: Commit**

```bash
git add web/src/
git commit -m "feat(web): 账本入口按 /api/ledger/health 探针门控，未启用时任务看板回主入口"
```

---

### Task 7: 全量终审门

- [ ] **Step 1: 后端全量**

Run: `gofmt -l . | grep -v '^web/'`（应无输出）；`go build ./... && go vet ./... && go test ./... -count=1`
Expected: 全绿。

- [ ] **Step 2: 前端全量**

Run: `cd web && npx tsc --noEmit && npx vitest run && npx eslint src --max-warnings=-1 2>/dev/null || npx eslint src`
Expected: tsc 0 错、vitest 全绿、eslint 0 error（既有 warning 不算）。

- [ ] **Step 3: 红线自查**

Run: `grep -rn "fmt.Printf" internal/ --include="*.go" | grep -v _test`（应无新增）；`grep -rn "console.log" web/src --include="*.ts*"`（应无输出）；`grep -rn "cfg.Ledger.DSN" cmd/ internal/ | grep -i "log\|slog\|Info\|Warn\|Error"`（应无输出——DSN 不进日志）。

- [ ] **Step 4: Commit（如有收尾改动）并写 ledger**

按执行纪律块要求逐 task 落盘 ledger。

---

## 审核者本地验收清单（不派发——需要驱动 agentd/浏览器，与纪律块冲突）

以下由审核者在本地完成，不写入执行者任务：

1. **判据①**：新 config（无 ledger 段）起隔离 agentd（独立 DataDir + 端口，配方见交接文档「怎么验」），确认 DataDir 下不生成 `ledger.db`、`curl /api/ledger/health` 返回 `{"enabled":false,...}`、`card add` 报未启用文案。
2. **判据②**：`ledger.enabled: true` 且 dsn 空，SQLite 回退照旧、card 全族可用。
3. **判据③**：浏览器实测 off/on 两态的 dock 图标、`/cards` 直达、任务看板筛选。
4. **spec §6 skill 改写**：由审核者本地完成（用户指定，不在本计划）。
