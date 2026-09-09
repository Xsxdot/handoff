# B351 实现计划：远端孤儿任务自动回收

状态：按 `docs/superpowers/specs/b351.md`、`docs/superpowers/specs/b351-contract.md`、`docs/superpowers/specs/b351-breakdown.md` 执行；本文件是实现节点的唯一计划输入。

## 1. 目标、冻结契约与边界

本卡锁定三件事：

1. `Dispatcher.ViaTemplate` 在远端任务已经创建、但本地快照/挂账落账失败时，记录一条独立的耗费轮次，并通过注入的补偿函数执行 `Stop` 后 `Reclaim(force=true)`。
2. 同一 `card_id,purpose` 的有效轮次是成功挂账数加失败耗费轮次数；失败轮次不能写入 `card_tasks`，也不能伪造 `TaskLink`、事件或新的 HTTP/event/CLI 通道。
3. CLI 与 agentd 的生产组装都注入真实远端补偿器；原有 direct `cmd/card_node.go` 路径不扩大到本卡。

冻结接口逐字符采用 contract Ticket 0：

```go
type DispatchCompensator func(ctx context.Context, target, taskID string) error

// Dispatcher.Compensate
Compensate DispatchCompensator

func (s *Store) RecordDispatchRound(cardID, purpose string) error

func (c *Client) StopAndReclaim(ctx context.Context, taskID string) error
```

现有对侧接口保持原签名并直接复用：

```go
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)
func (s *Store) PurposeRounds(cardID, purpose string) (int, error)
func (c *Client) Stop(ctx context.Context, taskID string) (bool, error)
func (c *Client) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)
```

不新增路由、事件、`card_tasks` 字段、远程删除 branch 行为、ledger 到 client 的直接依赖；补偿依赖只经 `DispatchCompensator` 进入 `ledgerstep`。

## 2. 已亲跑的基线与判据

计划写出前已在基线 `cards/B233.1-charter-7` 的工作树亲跑以下命令；原始结果已同步记录在 `docs/superpowers/ledgers/2026-09-09-b351-plan-ledger.md`：

```text
go test ./internal/ledger -run '^(TestB351|TestPurposeRounds|TestDDLDialectParity)' -count=1
ok   github.com/Xsxdot/handoff/internal/ledger  0.879s

go test ./internal/ledgerstep -run '^(TestB351|TestViaTemplateSecondRoundGetsNumberedBranch|TestViaTemplateEmptyTargetIsLocal)' -count=1
ok   github.com/Xsxdot/handoff/internal/ledgerstep  1.581s

go test ./internal/client -run '^(TestB351|TestReclaimOnOldAgentdReportsUnsupported|TestReclaimUnknownTaskIsNotMistakenForUnsupported|TestReclaimForceCarriesIntoRequestBody)' -count=1
ok   github.com/Xsxdot/handoff/internal/client  0.017s

go test ./cmd ./internal/agentd -run '^TestB351' -count=1
ok   github.com/Xsxdot/handoff/cmd  0.014s [no tests to run]
ok   github.com/Xsxdot/handoff/internal/agentd  0.004s [no tests to run]

go build ./...
(exit 0, no output)
```

这些基线测试绿只证明当前既有行为可执行，不代表 B351 已实现。实现后判据必须钉行为：

- ledger：真实 SQLite 与 PostgreSQL DDL 文本均出现 `card_dispatch_rounds` 及 `(card_id,purpose)` 索引；真实 `Store.RecordDispatchRound` 写入一行；`PurposeRounds` 返回成功挂账数加失败轮次数，按 purpose 隔离，重复记录累计，零值计数与字段缺失不混淆。
- ledgerstep：成功派发不调用补偿；Transport 出错不调用补偿；WriteGate 关闭与 `RecordDispatchAndLinkTask` 出错各恰调用一次 `RecordDispatchRound` 和一次注入的 `Compensate`；原始错误仍可由 `errors.Is` 识别；补偿失败只记录日志，不替换原始派发错误。
- client：Stop 先于 Reclaim；Stop 404 直接软成功且不 Reclaim；Stop 409 继续 Reclaim；Reclaim 404 或旧 agentd 的 `ErrReclaimUnsupported` 软成功；其他错误原样返回；Reclaim 请求体的 `force` 为 JSON 布尔 `true`。
- 组装：CLI 与 agentd 通过真实 `targetClient`/`clientForTarget` 注入补偿；HTTP 请求顺序为 Stop→Reclaim；本地 target 保持原本地 client 语义；direct `cmd/card_node.go` 无变更。

基线另有已知失败，不由本卡引入：

```text
go test ./cmd -run '^TestRepoContractGate$' -count=1
--- FAIL: TestRepoContractGate
    graph_gate_test.go:38: 契约违规 [dead-contract] 契约 d_execution→d_orchestration 声明的方向没有活跃 call、implements 或组装点豁免边（期望在该方向看到至少一条跨子系统边）
    graph_gate_test.go:38: 契约违规 [dead-interface] 契约 d_execution→d_orchestration 声明的接口 "ApprovalClient" 在 d_execution 中不存在（无同名非 deleted 节点；期望在 d_execution 找到）
    graph_gate_test.go:40: legacy 命中: map[...]，warn 147 条
FAIL
```

`go test ./... -count=1` 基线同样以 `TestRepoContractGate` 失败收尾；其余输出中的包测试已跑到，不能把全量结果写成通过。实现验收只认本卡列出的最小包测试加 `go build ./...`；全量测试由协调者统一执行。

## 3. 任务 DAG 与文件边界

执行顺序固定为：

```text
Task 1 ledger schema + round persistence
        ↓
Task 2 client StopAndReclaim
        ↓
Task 3 ledgerstep failure outlet + compensation
        ↓
Task 4 CLI/agentd assembly + cross-seam regressions
```

每个 task 只触及其文件集；测试只跑所触及包。每个 task 的第一步均先重跑本节列明的基线判据，再改文件。新增函数的日志与注释在同一 task 完成，不拆成独立红绿周期。

## 4. Task 1：独立失败轮次账本

### 文件集

- `internal/ledger/store.go`：两种方言的 `card_dispatch_rounds` 表和索引 DDL。
- `internal/ledger/dispatch_rounds.go`：实现冻结的 `Store.RecordDispatchRound`。
- `internal/ledger/events.go`：将 `Store.PurposeRounds` 从 `card_tasks` 计数改为成功挂账数加失败轮次数。
- `internal/ledger/store_test.go`：schema 存在性断言。
- `internal/ledger/ddl_parity_test.go`：两方言表/索引/列与约束的文本 parity 断言。
- `internal/ledger/events_test.go`：真实 Store 的累计、purpose 隔离、零值行为。

### Interfaces

Consumes：

```go
func (s *Store) mutate(fn func(*sql.Tx, *eventSink) error) error
func (s *Store) q(query string) string
func (s *Store) tval(t time.Time) any
func (s *Store) timeNow() time.Time
func getCardTx(s *Store, tx *sql.Tx, id string) (Card, error)
func (s *Store) TasksOf(cardID string) ([]TaskLink, error)
```

Produces：

```go
func (s *Store) RecordDispatchRound(cardID, purpose string) error
func (s *Store) PurposeRounds(cardID, purpose string) (int, error)
```

### 步骤

1. 在基线先运行：

   ```bash
   go test ./internal/ledger -run '^(TestB351|TestPurposeRounds|TestDDLDialectParity|TestOpenCreatesSchema)' -count=1
   ```

   预期：与第 2 节相同为绿，且当前没有 B351 失败轮次断言；若输出不同，将原文写入台账后按实际结果调整，不把未跑结果写进计划执行记录。

2. 在 `internal/ledger/store_test.go` 的既有 `newTestStore`/schema 测试中加入可判定断言：查询 `sqlite_master` 必须找到 `card_dispatch_rounds` 和 `idx_card_dispatch_rounds_card_purpose`，表列至少包含 `card_id`、`purpose`、`created_at`；现有 `card_tasks` 断言保持不动。测试仍使用现有 `newTestStore` fixture，不新建连接层。

3. 在 `internal/ledger/ddl_parity_test.go` 复用既有 DDL 提取 helper，加入以下逐项断言；不得只比较表名：

   ```go
   func TestB351DispatchRoundsDDLDialectParity(t *testing.T) {
       pg := strings.Join(ddlStatements(true), "\n")
       sqlite := strings.Join(ddlStatements(false), "\n")
       for _, ddl := range []struct {
           name string
           text string
       }{{"postgres", pg}, {"sqlite", sqlite}} {
           t.Run(ddl.name, func(t *testing.T) {
               for _, fragment := range []string{
                   "card_dispatch_rounds",
                   "card_id",
                   "purpose",
                   "created_at",
                   "idx_card_dispatch_rounds_card_purpose",
               } {
                   if !strings.Contains(ddl.text, fragment) {
                       t.Fatalf("DDL 缺少 B351 片段 %q", fragment)
                   }
               }
               if !strings.Contains(ddl.text, "REFERENCES cards(id)") {
                   t.Fatal("DDL 缺少 card_dispatch_rounds.card_id 对 cards(id) 的外键")
               }
               if !strings.Contains(ddl.text, "(card_id, purpose)") {
                   t.Fatal("DDL 缺少按 card_id,purpose 的索引")
               }
           })
       }
   }
   ```

4. 先写失败轮次的声明缝测试，再运行红测：在 `internal/ledger/events_test.go` 复用已存在的 `seedStore(t)`（`cards_test.go`）和 `mk(t,s,title)`（`relations_test.go`），新增 `TestB351PurposeRoundsAddsFailedRoundsByPurpose`。本测试属于“复用既有夹具/harness”的允许例外，不复制夹具实现；测试代码必须按以下逐条断言落地，入口只能是公开的 `Store.RecordDispatchRound` 与 `Store.PurposeRounds`：

   - `s := seedStore(t)`、`c := mk(t, s, "数失败轮次")` 后，`s.PurposeRounds(c.ID, PurposeImplement)` 返回 `(0,nil)`。
   - `s.LinkTask(c.ID, "acc", "T-success", PurposeImplement, "test")` 成功；这是当前 `tasks.go` 的精确签名，不把 `TaskLink` 结构体传给它。
   - 连续两次 `s.RecordDispatchRound(c.ID, PurposeImplement)` 返回 nil；一次 `s.RecordDispatchRound(c.ID, PurposeReview)` 返回 nil。
   - `s.PurposeRounds(c.ID, PurposeImplement)` 返回 `(3,nil)`，即一条成功挂账加两条失败轮次。
   - `s.PurposeRounds(c.ID, PurposeReview)` 返回 `(1,nil)`；`s.PurposeRounds(c.ID, "integration")` 返回 `(0,nil)`，证明 purpose 隔离、缺失行与零值可区分。
   - 测试失败信息必须包含实际得到的 count 与 error；不得只断言 error nil。

5. 运行 Task 1 红测，保存真实失败输出；只把“新断言失败”作为红判据，不预写错误文本。若失败原文出现 `ledger: dispatch round not wired`、缺表或计数不符，按实际原文追加台账；其他原文同样原样记录。

6. 在 `internal/ledger/store.go` 的 PG 与 SQLite DDL 列表中，紧邻 `card_tasks` 表及其索引加入完整片段：

   ```go
   `CREATE TABLE IF NOT EXISTS card_dispatch_rounds (
       card_id TEXT NOT NULL REFERENCES cards(id),
       purpose TEXT NOT NULL,
       created_at TIMESTAMPTZ NOT NULL)`,
   `CREATE INDEX IF NOT EXISTS idx_card_dispatch_rounds_card_purpose
       ON card_dispatch_rounds(card_id, purpose)`,
   ```

   SQLite 分支使用同样的表和索引定义，仅将时间类型写为既有 SQLite 约定的 `TEXT`：

   ```go
   `CREATE TABLE IF NOT EXISTS card_dispatch_rounds (
       card_id TEXT NOT NULL REFERENCES cards(id),
       purpose TEXT NOT NULL,
       created_at TEXT NOT NULL)`,
   `CREATE INDEX IF NOT EXISTS idx_card_dispatch_rounds_card_purpose
       ON card_dispatch_rounds(card_id, purpose)`,
   ```

7. 用下列完整实现替换 `internal/ledger/dispatch_rounds.go` 的未接线 stub；它只写新表，不写事件和 `card_tasks`：

   ```go
   // 本文件负责记录远端任务已经创建但本地派发落账失败的耗费轮次。
   // 边界：只操作 card_dispatch_rounds，不执行远端 HTTP，不生成事件或 TaskLink。
   package ledger

   import (
       "database/sql"
       "fmt"
   )

   // RecordDispatchRound 记录一次未形成 card_tasks 挂账的耗费轮次。
   // 参数 cardID 和 purpose 必须非空；返回值保留事务、卡不存在或 SQL 错误。
   // 注意：该记录是独立计数，不向事件流广播，也不替代成功挂账。
   func (s *Store) RecordDispatchRound(cardID, purpose string) error {
       log().Debug("派发耗费轮次落账进入", "card", cardID, "purpose", purpose)
       err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
           if cardID == "" {
               return fmt.Errorf("派发耗费轮次: 卡号不能为空")
           }
           if purpose == "" {
               return fmt.Errorf("派发耗费轮次: purpose 不能为空")
           }
           if _, err := getCardTx(s, tx, cardID); err != nil {
               return fmt.Errorf("派发耗费轮次: 卡 %s: %w", cardID, err)
           }
           if _, err := tx.Exec(s.q(`INSERT INTO card_dispatch_rounds
               (card_id, purpose, created_at) VALUES (?, ?, ?)`),
               cardID, purpose, s.tval(s.timeNow())); err != nil {
               return fmt.Errorf("写派发耗费轮次: %w", err)
           }
           return nil
       })
       if err != nil {
           log().Warn("派发耗费轮次落账失败", "card", cardID, "purpose", purpose, "cause", err)
           return err
       }
       log().Info("派发耗费轮次落账完成", "card", cardID, "purpose", purpose)
       return nil
   }
   ```

   当前 `getCardTx` 的精确签名已核实为 `func getCardTx(s *Store, tx *sql.Tx, id string) (Card, error)`（`internal/ledger/cards.go:374`）；实现直接按此签名调用并保留卡存在性检查。

8. 将 `internal/ledger/events.go` 的 `PurposeRounds` 改为一次成功挂账计数加一次失败轮次 SQL 计数，保留 `ReviewRounds` 对它的调用。实现必须使用 `s.q` 适配 SQLite/PG，并为查询错误带 card/purpose 上下文日志。完整逻辑如下：

   ```go
   func (s *Store) PurposeRounds(cardID, purpose string) (int, error) {
       links, err := s.TasksOf(cardID)
       if err != nil {
           log().Warn("读取成功派发轮次失败", "card", cardID, "purpose", purpose, "cause", err)
           return 0, err
       }
       succeeded := 0
       for _, link := range links {
           if link.Purpose == purpose {
               succeeded++
           }
       }
       var failed int
       if err := s.db.QueryRow(s.q(`SELECT COUNT(*)
           FROM card_dispatch_rounds WHERE card_id = ? AND purpose = ?`),
           cardID, purpose).Scan(&failed); err != nil {
           log().Warn("读取派发耗费轮次失败", "card", cardID, "purpose", purpose, "cause", err)
           return 0, fmt.Errorf("读派发耗费轮次: %w", err)
       }
       total := succeeded + failed
       log().Info("派发轮次读取完成", "card", cardID, "purpose", purpose,
           "succeeded", succeeded, "failed", failed, "total", total)
       return total, nil
   }
   ```

9. 运行 Task 1 绿测与最小包范围：

   ```bash
   gofmt -w internal/ledger/store.go internal/ledger/dispatch_rounds.go internal/ledger/events.go internal/ledger/store_test.go internal/ledger/events_test.go internal/ledger/ddl_parity_test.go
   go test ./internal/ledger -run '^(TestB351|TestPurposeRounds|TestB351DispatchRoundsDDLDialectParity|TestDDLDialectParity|TestOpenCreatesSchema)' -count=1
   ```

   预期：exit 0；输出中的 `ok github.com/Xsxdot/handoff/internal/ledger` 必须真实出现。Task 1 交付前补充导出方法注释、新文件头注释、非显然“成功挂账+失败计数”的原因注释，并把命令及原文输出追加台账。

## 5. Task 2：client Stop→Reclaim 补偿原子流程

### 文件集

- `internal/client/compensate.go`：实现冻结的 `Client.StopAndReclaim`。
- `internal/client/client_test.go`：复用真实 `httptest.Server` 与 `Client`，锁定顺序、软错误、force body。

### Interfaces

Consumes：

```go
func (c *Client) Stop(ctx context.Context, taskID string) (bool, error)
func (c *Client) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)
var ErrReclaimUnsupported error
type ReclaimRejected struct { /* 复用当前定义 */ }
```

Produces：

```go
func (c *Client) StopAndReclaim(ctx context.Context, taskID string) error
```

### 步骤

1. 在基线先运行：

   ```bash
   go test ./internal/client -run '^(TestB351|TestStopParsesWorktreeRemoved|TestReclaimOnOldAgentdReportsUnsupported|TestReclaimUnknownTaskIsNotMistakenForUnsupported|TestReclaimForceCarriesIntoRequestBody)' -count=1
   ```

   预期：exit 0；`Client.Stop` 已 POST `/api/tasks/{id}/stop`，`Client.Reclaim` 已 POST `/api/tasks/{id}/reclaim` 且 body 使用 `force`。

2. 在 `internal/client/client_test.go` 先添加真实 HTTP 声明缝表驱动测试 `TestB351StopAndReclaimOrderAndSoftErrors`。本测试复用当前文件中 `httptest.NewServer`、`client.New(srv.URL, "tok")`、`httpStatusError` 错误测试风格，不直调私有 `do`；这是允许的既有 harness 例外，以下断言逐条实现：

   - Stop 404：handler 记录唯一请求为 `POST /api/tasks/task-b351/stop`，`StopAndReclaim` 返回 nil，未收到 reclaim。
   - Stop 409：handler 依次收到 stop、reclaim；Stop 返回 409、Reclaim 返回 200 合法 JSON 时整体返回 nil。
   - Stop 500：handler 只收到 stop，整体返回非 nil，且未收到 reclaim。
   - Stop 200、Reclaim 404：依次收到 stop、reclaim，整体返回 nil。
   - Stop 200、Reclaim 500：依次收到 stop、reclaim，整体返回非 nil。
   - 每个收到 reclaim 的分支解码 request body 为 `map[string]bool`，断言键 `force` 存在且值为 true；不能只搜索字符串。
   - 每个非 nil 错误分支保留 HTTP 状态语义：未知 500 不得被判为 `ErrReclaimUnsupported`。
   - 另在现有 `TestReclaimOnOldAgentdReportsUnsupported` handler 形态上调用 `StopAndReclaim`：Stop 返回 200 合法 JSON 后，Reclaim 的动作 404 与列表 404 组合产生 `ErrReclaimUnsupported`，整体返回 nil。

3. 运行 Task 2 红测；当前 stub 应使新增声明缝断言失败。只把真实测试输出写入台账，不把预期文本当作结果。

4. 替换 `internal/client/compensate.go`，完整实现如下；`httpStatusError`、`ErrReclaimUnsupported` 与 `Client.log` 均复用当前包定义，不复制类型：

   ```go
   // 本文件负责把远端孤儿任务的 Stop 与强制 Reclaim 串成一个补偿操作。
   // 边界：只调用 client 已有 HTTP 方法，不新增路由、超时、重试或 branch 删除逻辑。
   package client

   import (
       "context"
       "errors"
       "net/http"
   )

   func hasHTTPStatus(err error, code int) bool {
       var statusErr *httpStatusError
       return errors.As(err, &statusErr) && statusErr.code == code
   }

   // StopAndReclaim 先停止远端任务，再以 force=true 回收其孤儿资源。
   // 参数 taskID 是远端任务 ID；返回 nil 表示已完成或命中契约规定的软成功。
   // 注意：Stop 404 不再发 Reclaim；Stop 409 继续 Reclaim；Reclaim 404/不支持视为软成功。
   func (c *Client) StopAndReclaim(ctx context.Context, taskID string) error {
       c.log().Info("孤儿派发补偿进入", "task", taskID)
       _, err := c.Stop(ctx, taskID)
       if err != nil {
           if hasHTTPStatus(err, http.StatusNotFound) {
               c.log().Info("Stop 返回 404，孤儿补偿视为完成", "task", taskID)
               return nil
           }
           if !hasHTTPStatus(err, http.StatusConflict) {
               c.log().Error("Stop 失败，未进入 Reclaim", "task", taskID, "cause", err)
               return err
           }
           c.log().Warn("Stop 返回 409，继续强制回收", "task", taskID, "cause", err)
       } else {
           c.log().Info("Stop 成功，开始强制回收", "task", taskID)
       }
       if _, err := c.Reclaim(ctx, taskID, true); err != nil {
           if hasHTTPStatus(err, http.StatusNotFound) || errors.Is(err, ErrReclaimUnsupported) {
               c.log().Info("Reclaim 不可用或任务已消失，孤儿补偿视为完成", "task", taskID, "cause", err)
               return nil
           }
           c.log().Error("Reclaim 失败", "task", taskID, "cause", err)
           return err
       }
       c.log().Info("孤儿派发补偿完成", "task", taskID)
       return nil
   }
   ```

   `hasHTTPStatus` 只识别当前包的 `*httpStatusError`，不能通过字符串匹配把未知任务误判成旧 agentd；context 取消、deadline 和非 404/409/unsupported 错误必须原样传回。补偿成功和每个错误分支均有结构化日志；不使用 `print`。

5. 运行 Task 2 绿测：

   ```bash
   gofmt -w internal/client/compensate.go internal/client/client_test.go
   go test ./internal/client -run '^(TestB351|TestB351StopAndReclaimOrderAndSoftErrors|TestStopParsesWorktreeRemoved|TestReclaimOnOldAgentdReportsUnsupported|TestReclaimUnknownTaskIsNotMistakenForUnsupported|TestReclaimForceCarriesIntoRequestBody)' -count=1
   ```

   预期：exit 0，输出 `ok github.com/Xsxdot/handoff/internal/client`；把原始输出追加台账。Task 2 仅跑 `./internal/client`，不在此 task 跑全量。

## 6. Task 3：ViaTemplate 失败出口与注入补偿

### 文件集

- `internal/ledgerstep/dispatch.go`：复用现有 `Dispatcher.Compensate` 字段，加入失败补偿 helper 与两个落账失败出口。
- `internal/ledgerstep/dispatch_test.go`：从真实 `ViaTemplate` 锁定 WriteGate 与 `RecordDispatchAndLinkTask` 两个失败接缝。

### Interfaces

Consumes：

```go
type DispatchCompensator func(ctx context.Context, target, taskID string) error
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)
func (s *Store) RecordDispatchRound(cardID, purpose string) error
func (s *Store) RecordDispatchAndLinkTask(cardID string, snap ledger.DispatchSnapshot) (int64, int64, error)
```

Produces：`Dispatcher` 现有字段 `Compensate DispatchCompensator` 的运行时行为：只在 Transport 已返回 task ID、写闸或本地落账失败时执行注入 callback。

### 步骤

1. 在基线先运行：

   ```bash
   go test ./internal/ledgerstep -run '^(TestViaTemplateSecondRoundGetsNumberedBranch|TestViaTemplateEmptyTargetIsLocal|TestB2336DispatchFailureRollsBackSnapshot|TestViaTemplateStopsSnapshotAfterWriteGateCloses)' -count=1
   ```

   预期：exit 0；既有成功分支、Transport 失败回滚和 WriteGate 关闭行为均可执行。

2. 在 `internal/ledgerstep/dispatch_test.go` 复用当前已有的 `dispatchTestCard(t)`（精确返回 `(*ledger.Store, ledger.Card)`）以及两个现有声明缝测试，不复制 fixture 实现；这是允许的既有 harness 例外。将 `TestViaTemplateStopsSnapshotAfterWriteGateCloses` 扩为 B351 断言：Transport 返回精确 task ID `T-write-gate`；Dispatcher 的 `Compensate` 追加 `target+":"+taskID`；真实 WriteGate 第一次释放 run lock；ViaTemplate 从该入口返回 `errors.Is(err, ErrWriteGateClosed)`；`PurposeRounds(card.ID, ledger.PurposeImplement)` 返回 `(1,nil)`；callback 列表精确为 `[]string{"mac-02:T-write-gate"}`；`TasksOf(card.ID)` 返回空列表；事件中没有该 task 的 `EvDispatched`。

   同样扩展 `TestB2336DispatchFailureRollsBackSnapshot`：沿用它当前对 `T-already-linked` 的真实 `LinkTask` 冲突装置；Dispatcher 的 Transport 返回该 task ID，Compensate 记录调用参数；ViaTemplate 通过同一入口返回非 nil；`PurposeRounds(card.ID, ledger.PurposeImplement)` 返回 `(2,nil)`（预占成功挂账 1 + 当前失败轮次 1）；callback 列表精确为 `[]string{"mac-02:T-already-linked"}`；已有事件扫描继续断言该 task 没有新 `EvDispatched`，且 `TasksOf` 仍只有预占的那一行。

   再新增 `TestB351TransportFailureDoesNotCompensate`，直接照抄同文件 `dispatchTestCard` 的真实初始化形态：Transport 返回 `"", "", errors.New("transport-b351")`；Compensate 记录调用次数；从 `ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02", Node: "doing"})` 进入；断言返回非 nil、callback 次数为 0、`PurposeRounds(card.ID, ledger.PurposeImplement)` 为 `(0,nil)`、`TasksOf(card.ID)` 为空。三支测试都必须从 `ViaTemplate` 进入，不得改成 helper 单测。

3. 运行 Task 3 红测；只把三支测试的真实失败输出追加台账，不预写失败原因。

4. `Dispatcher` 已有冻结字段 `Compensate DispatchCompensator`（当前源码 `internal/ledgerstep/dispatch.go:79-85` 已核实）；本步只在 `ViaTemplate` 两个失败出口调用同文件 helper。helper 完整逻辑如下：

   ```go
   func (d *Dispatcher) compensateFailedDispatch(
       ctx context.Context,
       card ledger.Card,
       purpose, target, taskID string,
       originalErr error,
   ) {
       logger := slog.Default().With("card", card.ID, "purpose", purpose, "target", target, "task", taskID)
       logger.Warn("模板派发记录失败，登记耗费轮次", "cause", originalErr)
       if err := d.St.RecordDispatchRound(card.ID, purpose); err != nil {
           logger.Error("派发耗费轮次落账失败，保留原始错误", "cause", err)
       }
       if d.Compensate == nil {
           logger.Warn("派发补偿钩子未装配")
           return
       }
       logger.Info("派发失败补偿开始")
       if err := d.Compensate(ctx, target, taskID); err != nil {
           logger.Error("派发失败补偿失败，保留原始错误", "cause", err)
           return
       }
       logger.Info("派发失败补偿完成")
   }
   ```

   在现有 `WriteGate` 关闭分支中，保持已有 `ErrWriteGateClosed` wrapping，构造原始返回错误后调用：

   ```go
   originalErr := fmt.Errorf("派发落账被拒：%w", ErrWriteGateClosed)
   d.compensateFailedDispatch(ctx, c, purpose, target, taskID, originalErr)
   return zero, originalErr
   ```

   在 `RecordDispatchAndLinkTask` 出错分支中，保持已有返回文本和 `errors.Is` 链：

   ```go
   originalErr := fmt.Errorf("快照与挂账落账: %w", err)
   d.compensateFailedDispatch(ctx, c, purpose, target, taskID, originalErr)
   return zero, originalErr
   ```

   `target` 必须是 transport 实际使用的归一目标；taskID 必须是 transport 已返回的非空 ID。Transport 返回 error 的分支不调用 helper，因为没有远端任务可回收；成功落账不调用 helper。补偿函数返回 error 时只日志记录，不能覆盖 `originalErr`。每个分支带 card/purpose/target/task 上下文，成功路径也写开始/完成日志；不使用 `print`。

5. 更新 `internal/ledgerstep/dispatch.go` 文件头/导出字段注释，明确 `Compensate` 只负责已创建远端 task 的失败回收；在 helper 上写“为什么落独立轮次而不是复用 `card_tasks`”的非显然逻辑注释。

6. 运行 Task 3 绿测与最小包范围：

   ```bash
   gofmt -w internal/ledgerstep/dispatch.go internal/ledgerstep/dispatch_test.go
   go test ./internal/ledgerstep -run '^(TestB351TransportFailureDoesNotCompensate|TestViaTemplateSecondRoundGetsNumberedBranch|TestViaTemplateEmptyTargetIsLocal|TestB2336DispatchFailureRollsBackSnapshot|TestViaTemplateStopsSnapshotAfterWriteGateCloses)' -count=1
   ```

   预期：exit 0，输出 `ok github.com/Xsxdot/handoff/internal/ledgerstep`；把原始输出追加台账。Task 3 不跑 CLI、agentd 或全量测试。

## 7. Task 4：CLI/agentd 真实组装与跨接缝回归

### 文件集

- `cmd/card_dispatch.go`：CLI `Dispatcher` 生产组装注入 `Compensate`。
- `cmd/card_dispatch_test.go`：真实 CLI dispatch→HTTP Stop→HTTP Reclaim 回归。
- `cmd/dispatch_discipline_test.go`：扩展既有 `captureTarget` httptest harness，记录 Stop/Reclaim 顺序并解码 force。
- `internal/agentd/cardstep.go`：agentd `Dispatcher` 生产组装注入 `Compensate`。
- `internal/agentd/ledgerapi_test.go`：真实 agentd `startCardStep`→捕获 runner→ViaTemplate→HTTP 回归。
- `internal/agentd/cardstep_discipline_test.go`：扩展既有 `fakeTargetMachine` httptest harness，记录 Stop/Reclaim 顺序并解码 force。
- `cmd/card_node.go`：只读核对，禁止改动；它是本卡明确 OOS 的 direct path。

### Interfaces

Consumes：

```go
func targetClient(target string) (*client.Client, func(), error)
func (s *Server) clientForTarget(target string) (*client.Client, error)
func (s *Server) CanonicalTarget(target string) string
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)
func (c *Client) StopAndReclaim(ctx context.Context, taskID string) error
```

Produces：

```go
Dispatcher{Compensate: func(ctx context.Context, target, taskID string) error}
```

### 步骤

1. 在基线先运行：

   ```bash
   go test ./cmd ./internal/agentd -run '^(TestB351|TestCardDispatch|TestStartCardStep|TestStepTransport)' -count=1
   ```

   预期：命令 exit 0；现有匹配不到的 `TestB351` 可以显示 `[no tests to run]`，但不能据此宣称 B351 已覆盖。另运行 `go build ./...`，预期 exit 0、无输出。

2. 在 `cmd/dispatch_discipline_test.go` 的既有 `captureTarget` 中增加只服务 B351 测试的请求记录字段；保留原有 `/api/status` 和 `/api/tasks` 行为，新增 `/api/tasks/<id>/stop` 返回 `200 {"status":"stopped","worktree_removed":false}`，新增 `/api/tasks/<id>/reclaim` 解码 `map[string]bool` 并返回 `200 {"removed":true,"action":"removed"}`。在 `cmd/card_dispatch_test.go` 新增 `TestB351CardDispatchAssemblesStopAndReclaimCompensator`，复用精确签名的 `runLedgerCLI(t, dir, args...) (string,string,error)`、`swapDispatchTransportWithOpts`、`writeCardDispatchConfig(t, dir, addr)`、`newCaptureTarget(t, statusBody)`；这是允许的既有 harness 例外，以下断言逐条实现：

   - 通过 `runLedgerCLI` 建卡、写入 `feature-impl` 模板和 `implement` 纪律；通过 `ledger.Open(filepath.Join(dir, "ledger.db"))` 调用精确签名 `LinkTask(cardID, "fake-01", "T-b351-cli", ledger.PurposeImplement, "test")` 预占一行。
   - `swapDispatchTransportWithOpts` 返回同一 `T-b351-cli`，令 `cardDispatchCmd.RunE` 的真实 `ViaTemplate` 进入 `RecordDispatchAndLinkTask` 冲突出口；不直接调用 callback。
   - CLI 通过 `runLedgerCLI(t, dir, "card", "dispatch", cardID, "--template", "feature-impl", "--target", "fake-01")` 返回非 nil。
   - `captureTarget` 记录的完整请求顺序至少包含 status 探活后，补偿请求精确为 `POST /api/tasks/T-b351-cli/stop`、`POST /api/tasks/T-b351-cli/reclaim`；Reclaim body 的 `force` 键存在且是 bool true。
   - 账本重读 `PurposeRounds(cardID, ledger.PurposeImplement)` 为 `(2,nil)`，`TasksOf(cardID)` 仍只有预占行；这条断言穿过 CLI→ledger SQL 序列化边界。

3. 在 `internal/agentd/ledgerapi_test.go` 新增 agentd 声明缝测试，复用精确的 `newLedgerEnv(t)`、`seedCardWithProject`、`seedDisciplineOnLedger`、`newFakeTargetMachine`、`registerFakeTarget`、`ledgerPost` 和 `runStepFn` runner channel harness；在 `internal/agentd/cardstep_discipline_test.go` 扩展该 fake target 的 handler 记录 Stop/Reclaim。具体步骤和断言固定为：

   - `env := newLedgerEnv(t)`，用既有 helper 种 `B1` card、implement 纪律；`yes := true`，登记 `fake-01`；打开 `POST /api/cards/B1/step`，body 精确为 `{"step":"进行中","target":"fake-01","actor":"cli:b351"}`。
   - 在调用 step 前通过 `env.ledger.LinkTask(card.ID, "fake-01", "T-fake-01", ledger.PurposeImplement, "test")` 预占与 fake target 固定返回值相同的 task ID。
   - 测试函数名固定为 `TestB351AgentdStartCardStepAssemblesCompensator`；`runStepFn` 捕获 `*ledgerstep.StepRunner`；HTTP 受理必须为 202；捕获 runner 后断言 `runner.Dispatcher.Compensate != nil`，再从该真实组装的 dispatcher 调 `ViaTemplate(context.Background(), card, ledgerstep.TemplateDispatch{Template: "feature-impl", Target: "fake-01", Node: "doing"})`。
   - `fakeTargetMachine` 返回固定 task `T-fake-01` 后，真实 ledger 冲突出口返回非 nil；fake handler 收到 stop 再 reclaim，Reclaim body 的 `force` 是 bool true；不得只测试 callback 字段。
   - `env.ledger.PurposeRounds(card.ID, ledger.PurposeImplement)` 为 `(2,nil)`，`TasksOf` 仍只有预占行；HTTP 请求顺序和 round 读回均为 pass/fail 断言。

4. 在 `cmd/card_dispatch.go` 的 `Dispatcher` literal 中注入以下 callback；必须在每次 callback 中通过既有 `targetClient` 管理 cleanup：

   ```go
   Compensate: func(ctx context.Context, target, taskID string) error {
       cl, done, err := targetClient(target)
       if err != nil {
           slog.Error("CLI 派发失败补偿创建 client 失败", "card", card.ID, "target", target, "task", taskID, "cause", err)
           return err
       }
       defer done()
       slog.Info("CLI 派发失败补偿开始", "card", card.ID, "target", target, "task", taskID)
       if err := cl.StopAndReclaim(ctx, taskID); err != nil {
           slog.Error("CLI 派发失败补偿失败", "card", card.ID, "target", target, "task", taskID, "cause", err)
           return err
       }
       slog.Info("CLI 派发失败补偿完成", "card", card.ID, "target", target, "task", taskID)
       return nil
   },
   ```

   该 callback 位于当前 `cardDispatchCmd.RunE` 的 `card` 已取得之后，因此日志使用 `card.ID`；target 必须已经过 `NormalizeTarget` 的值。不能在 callback 内复制 URL/path/body，也不能忘记 `defer done()`。

5. 在 `internal/agentd/cardstep.go` 的 `startCardStep` Dispatcher literal 中注入以下 callback；复用 `s.clientForTarget` 的现有连接池所有权：

   ```go
   Compensate: func(ctx context.Context, target, taskID string) error {
       cl, err := s.clientForTarget(target)
       if err != nil {
           s.log.Error("agentd 派发失败补偿创建 client 失败", "card", cardID, "target", target, "task", taskID, "cause", err)
           return err
       }
       s.log.Info("agentd 派发失败补偿开始", "card", cardID, "target", target, "task", taskID)
       if err := cl.StopAndReclaim(ctx, taskID); err != nil {
           s.log.Error("agentd 派发失败补偿失败", "card", cardID, "target", target, "task", taskID, "cause", err)
           return err
       }
       s.log.Info("agentd 派发失败补偿完成", "card", cardID, "target", target, "task", taskID)
       return nil
   },
   ```

   该 callback 位于当前 `startCardStep(cardID string, req proto.CardStepReq)` 的 Dispatcher literal 内，因此日志使用 `cardID`；不要新增 close，因为 `clientForTarget` 的既有契约是本地 client/远端池由 Server 持有。保留 `NormalizeTarget: s.CanonicalTarget`，让 helper 收到与 transport 相同的 target。

6. 在 `cmd/card_dispatch.go` 与 `internal/agentd/cardstep.go` 的 callback 旁补导出/非显然逻辑注释：CLI callback 负责 cleanup，agentd callback 借用 Server pool；两者都只回收已取得 task ID 的远端任务，不改变 `ViaTemplate` 的错误返回。

7. 只读核对 `cmd/card_node.go#runStepDispatch`：它仍直接调用现有 dispatch client，不添加 `Compensate` 或 Stop/Reclaim；若为通过测试而修改该文件，立即撤销并将原因记入台账。

8. 运行 Task 4 绿测与最小范围构建：

   ```bash
   gofmt -w cmd/card_dispatch.go cmd/card_dispatch_test.go cmd/dispatch_discipline_test.go internal/agentd/cardstep.go internal/agentd/ledgerapi_test.go internal/agentd/cardstep_discipline_test.go
   go test ./cmd -run '^(TestB351CardDispatchAssemblesStopAndReclaimCompensator|TestCardDispatch)' -count=1
   go test ./internal/agentd -run '^(TestB351AgentdStartCardStepAssemblesCompensator|TestStartCardStep|TestStepTransport)' -count=1
   go build ./...
   ```

   预期：B351 CLI/agentd 回归与 build exit 0；把三条命令的原始输出追加台账。实现者另可单独重跑 `go test ./cmd -run '^TestRepoContractGate$' -count=1` 作外部 gate 对照；若仍输出第 2 节已知 d_execution→d_orchestration dead-contract/dead-interface 原文，则单独记录，不把它归因于 B351，也不把它作为本卡唯一绿判据。

## 8. 明确不做的改动

- 不删除、重命名或远程清理已创建的 branch；失败尝试只增加独立轮次事实，下一次按 `PurposeRounds` 计算带编号的 branch 名。
- 不新增 HTTP endpoint、event type、CLI command、`card_tasks` 列或 ledger→client import。
- 不在 Transport、`RecordDispatchAndLinkTask`、`cmd/card_node.go#runStepDispatch` 或后台 goroutine 中复制 Stop/Reclaim；唯一补偿入口是 `ViaTemplate` 失败出口调用的注入 `DispatchCompensator`。
- 不把补偿错误覆盖为 ViaTemplate 的原始账本错误，不把未知 HTTP 错误降级成旧 agentd 不支持。

## 9. 序列化边界与接缝覆盖

### 手写序列化/投影清单

1. `internal/ledger/store.go#ddlStatements`：PG `TIMESTAMPTZ` 与 SQLite `TEXT` 的 DDL 投影；Task 1 parity 测试逐方言检查表、列、外键、索引。
2. `internal/ledger/dispatch_rounds.go#Store.RecordDispatchRound`：Go 参数到 SQL `card_id,purpose,created_at`；Task 1 从真实 Store 写入并由 `PurposeRounds` 读回，purpose 非空、计数 0/1/2 分别可判定。
3. `internal/ledger/events.go#Store.PurposeRounds`：`card_tasks` 成功挂账投影加新表失败轮次投影；Task 1 断言 implement/review/integration 的 3/1/0，区分字段没有行和值为零。
4. `internal/client/client.go#Client.Reclaim`：Go `force bool` 到 JSON `{"force":true}`；Task 2、Task 4 的真实 httptest handler 解码 body 并断言 JSON 布尔值。
5. `cmd/card_dispatch.go` 与 `internal/agentd/cardstep.go`：target/taskID 从失败出口投影到 `StopAndReclaim(ctx, taskID)`，Task 4 真实入口断言 task ID、请求顺序和 target client 路径。

没有新增跨语言边界；没有将 `card_tasks` 行编码成失败轮次。缺失行返回计数 0，实际一行返回 1，重复行返回 2，测试不得使用无法区分缺失和零值的字符串响应。

### 接缝清单双向检查

- C1 声明缝：`Store.RecordDispatchRound(cardID,purpose)`。锁定测试：`TestB351PurposeRoundsAddsFailedRoundsByPurpose` 入口直接调用该方法，并由 `PurposeRounds` 穿过 SQL 边界验证累计。
- C2 声明缝：`Store.PurposeRounds(cardID,purpose)`。同一 Task 1 测试从该对外方法读回成功+失败计数；`ReviewRounds` 只作既有调用面回归。
- C3 声明缝：`Client.StopAndReclaim(ctx,taskID)`。锁定测试：`TestB351StopAndReclaimOrderAndSoftErrors` 入口调用该方法，真实 HTTP handler 锁顺序和 force。
- C4 声明缝：`Dispatcher.ViaTemplate(ctx,card,req)` 的 WriteGate 失败出口。锁定测试：扩展既有 `TestViaTemplateStopsSnapshotAfterWriteGateCloses`，从该入口断言 round、一次 callback、无 TaskLink。
- C5 声明缝：`Dispatcher.ViaTemplate` 的 `RecordDispatchAndLinkTask` 失败出口。锁定测试：扩展既有 `TestB2336DispatchFailureRollsBackSnapshot`，从同一入口断言 round、一次 callback、原始错误和无新增 TaskLink。
- C6 组装缝：`cmd/cardDispatchCmd.RunE` 生产 Dispatcher。锁定测试：`TestB351CardDispatchAssemblesStopAndReclaimCompensator` 经过 CLI RunE 与真实 HTTP。
- C7 组装缝：`Server.startCardStep` 生产 Dispatcher。锁定测试：`TestB351AgentdStartCardStepAssemblesCompensator` 从 `startCardStep` 捕获的 Dispatcher 进入 `ViaTemplate`，经真实 client HTTP 验证。

测试→缝：上述每支测试的入口均是 C1–C7 的声明方法或从 C6/C7 组装缝穿过 C4/C5；没有内部锁替代缝级断言。缝→测试：C1–C7 各至少一支锁定测试，C6/C7 不能由 Task 3 的 callback 单测替代。任何测试 fixture 改名必须保持入口关系，并在台账记录真实符号。

## 10. 缺陷族对抗审查与真机清单

### 缺陷族结论

- 数据持久化/迁移：两方言均声明表、外键、时间列、`(card_id,purpose)` 索引；schema 测试和 parity 测试覆盖重复启动下 `IF NOT EXISTS`。
- 事务/回滚：失败轮次用独立 `mutate` 事务；`RecordDispatchAndLinkTask` 原子失败不留下成功快照/TaskLink，随后 round 可读；已有 B2336 回滚测试保留。
- 计数语义：成功挂账与失败耗费相加，不使用 max/覆盖；purpose 精确隔离；重复失败行累计。
- 错误语义：ViaTemplate 原始落账错误保留 `%w`；补偿错误不覆盖；client 只有冻结的 404/409/unsupported 软分支，未知 task 不靠文本匹配吞掉。
- 调用时序/幂等：Stop 只在有 task ID 且落账失败后调用；Stop 404 不 Reclaim，Stop 409 必 Reclaim，Reclaim 只调用一次；成功落账与 Transport error 不补偿。
- 依赖注入/装配：CLI 与 agentd 都显式设置 `Compensate`；nil hook 只记录 warning，不 panic；本地 target 复用现有 client 语义。
- 资源/上下文：CLI callback `defer done()`；agentd 不关闭 Server 持有的 pool；ctx 原样传到 Stop/Reclaim；不新增 timeout 或后台 goroutine。
- 观测性：入口、Stop/Reclaim 前后、失败 round、补偿成功/失败各有结构化日志并带 card/target/task/purpose；无 `print`。
- 协议兼容：不增 endpoint/event/CLI；Reclaim 请求体保留 JSON `force: true`；旧 agentd 的 unsupported 仅在 sentinel 上软成功。
- 图与声明：已用 codegraph 查 `ViaTemplate` flow、callers、`PurposeRounds` callers、`startCardStep` 通道树；未命中 `targetClient`、`DispatchCompensator`、`CountRounds` 等符号的原因及债务已在台账记录，执行后若新增边需由协调者更新图视图。

### 边界型真机清单

1. SQLite：打开真实 Store 后可见新表/索引，写 round 后重读得到正确累计。
2. PostgreSQL：若 CI 提供真实 PostgreSQL，则运行同一 schema/parity/round 测试；本地未提供连接时只报告未验证，不以 SQLite 结果代替。
3. 远端 HTTP：真实 httptest 收到 `POST /api/tasks/<id>/stop`，随后 `POST /api/tasks/<id>/reclaim`，body 中 `force` 为 JSON `true`。
4. Stop 状态：404 不发 Reclaim；409 发 Reclaim；非 404/409 错误返回。
5. Reclaim 状态：404 与 `ErrReclaimUnsupported` 返回 nil；其他 HTTP 错误返回。
6. CLI：从 `cardDispatchCmd.RunE` 进入的远端失败链路使用 callback；target cleanup 发生。
7. agentd：从 `Server.startCardStep` 进入的远端失败链路使用 callback；client pool 所有权不被 callback 破坏。
8. 原始错误：WriteGate/ledger 落账错误仍可 `errors.Is`，补偿错误不替换它。

## 11. 测试范围、日志与注释交付清单

Task 1 只跑 `./internal/ledger`；Task 2 只跑 `./internal/client`；Task 3 只跑 `./internal/ledgerstep`；Task 4 只跑 `./cmd`、`./internal/agentd` 与 `go build ./...`。实现完成后由协调者统一执行全量测试；单 task 不承担全量判定。

每个新增或改动的生产入口都必须在提交前完成：

- `dispatch_rounds.go` 文件头职责/边界、`RecordDispatchRound` 参数/返回/注意事项注释。
- `compensate.go` 文件头职责/边界、`StopAndReclaim` 参数/返回/软错误注意事项注释。
- `PurposeRounds` 的成功+失败计数原因注释。
- `Dispatcher.Compensate` 与失败 helper 的“仅已创建远端任务、错误保留、独立计数”注释。
- CLI/agentd callback 的入口、client 创建失败、补偿前后日志；每条错误分支有 card/target/task 上下文。

提交前跑 `gofmt`、触及包测试、`go build ./...`、`git diff --check`；保存每条命令原文和结果到 B351 台账。不得用 `go test ./...` 的部分输出冒充全量通过。

## 12. 五项计划自审

1. Spec 覆盖：用户故事 1 由 Task 3/4 覆盖（落账失败后 Stop→Reclaim）；用户故事 2 由 Task 1/3 覆盖（失败轮次计数与后续编号来源）；用户故事 3 由 Task 1/2/4 覆盖（软错误、未知错误、CLI/agentd 真实组装）。测试决策中的 no new channel、no card_tasks、保留 branch、Stop+Reclaim(force) 均在 Task 3/4 与 OOS 断言中落点。
2. 占位符扫描：本计划不使用待定项、泛化错误处理语句或跨 task 指代；测试复用既有 fixture 的位置均逐条列明断言和要照抄的文件，且不改变声明缝入口。复用既有 fixture 的测试代码不复制 harness 实现，均已显式声明允许例外、精确文件与逐条 pass/fail 断言；计划没有要求执行者创建未存在的 helper 或生产 API。
3. 跨 task 签名：Task 1 Produces 的 `RecordDispatchRound`/`PurposeRounds` 与 Task 3 Consumes 逐字一致；Task 2 Produces 的 `StopAndReclaim` 与 Task 3/4 Consumes 逐字一致；`DispatchCompensator` 的 `context.Context,string,string` 与所有 callback 逐字一致。
4. 上下文预算：四个 task 文件集均为既有包内有界集合；Task 4 的 `cmd/card_node.go` 只读核对，不把 direct path 拉入实现范围。
5. 图覆盖：已执行 context/flow/who-calls/tree/sym/resolve；未覆盖符号债务留在台账，不用 grep 结论冒充图结果。计划引用的关键声明缝均有精确文件和符号，执行者遇到图缺口按源码核对并记录。

## 13. 收口判据

本计划节点只提交计划文档和同批台账，不写实现代码，不派发任何 executor，不调用 handoff CLI。完成条件是：`docs/superpowers/plans/b351-plan.md` 落盘；台账含基线命令原文、图查证、源代码事实与计划自审；占位符扫描、`git diff --check`、计划解析/锚点检查真实跑通；两文件同一提交；提交后工作树干净。
