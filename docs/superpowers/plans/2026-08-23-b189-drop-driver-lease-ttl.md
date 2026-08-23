# B189 实现计划：驱动认领保留、TTL 废除、转移显式

> 规格：`docs/superpowers/specs/2026-08-23-b189-drop-driver-lease-ttl.md`
> 当前工作分支：`cards/B189-charter`
> 计划节点只产出本文件；实现者按 Task 逐卡执行，最终收尾由协调者做全量验收。

## 目标与冻结契约

把驱动身份从“带 5 分钟 TTL 的租约”改成“显式持有的驱动归属”：首次认领仍用现有
CAS，非空且不是自己的 `driver_session` 永久拒绝普通认领；人通过显式 takeover 或
release 改变归属。`driver_heartbeat_at` 列、JSON 字段名和 schema 均保留，但它只表示
认领时刻，不再参与所有权判定，也不再被续租。

本计划不改 `ClaimCard` / `ClaimDriver` 的 CAS 形态，不改 B184 的 watchers 判据，不改
task 侧 executor 存活结论，不改 `driver_heartbeat_at` 的列名，不加 `claim --force`，
不引入 agentd 存活探测。

### 跨 task 接口冻结

| 方向 | 精确签名/值 | 说明 |
|---|---|---|
| ledger Produces | `func (s *Store) ClaimDriver(cardID, session string) error` | 空驱动或同会话放行；非空他会话一律 `ErrCASConflict`；认领时写 `driver_heartbeat_at` |
| ledger Produces | `func (s *Store) ClaimCard(id, to, expect, session string) error` | 保留原子状态 CAS + 驱动 CAS；非空他会话一律 `ErrCASConflict` |
| ledger Produces | `func (s *Store) ReleaseCard(id, session string) error` | 保留幂等语义；仅持有者清空驱动与认领时刻，非持有者无操作 |
| ledger Produces | `func (s *Store) TakeoverCard(id, session, actor string) error` | 在同一事务内把当前驱动替换为 `session`，写 `driver_takeover` 事件；`actor` 是事件操作者 |
| ledger Produces | `const EvDriverTakeover = "driver_takeover"` | 事件类型唯一字面量 |
| cmd Consumes | `ledger.Store.TakeoverCard(id, ledgerSession(), ledgerActor()) error` | `handoff card takeover <id>`，无二次确认 |
| cmd Consumes | `ledger.Store.ReleaseCard(id, ledgerSession()) error` | `handoff card release <id>`，输出 `{"ok":true}` |

当前仓库没有 `codegraph` 可执行文件；已用 `rg` 查证调用面，覆盖债记录如下：

- `ClaimDriver` 生产调用：`internal/ledgerstep/runner.go:87`；测试调用在
  `internal/ledger/tasks_test.go`、`internal/ledger/move_test.go`、`internal/ledgerstep/runner_test.go`。
- `ClaimCard` 生产调用：`cmd/card_dispatch.go:172`；事务实现为
  `internal/ledger/move.go:139`。
- `ReleaseCard` 生产调用：`cmd/card_dispatch.go:196`、`internal/ledgerstep/runner.go:95`。
- `HeartbeatDriver` 只有 `internal/ledger/tasks.go` 定义、`internal/ledgerstep/runner.go`
  的 B196 心跳装配和 `runner_test.go` 注入测试；移除 B196 心跳后不再有生产调用。

基线行为出处（计划引用的库/事务事实均来自仓内代码，而非记忆）：

- TTL 常量与 `ClaimDriver` 过期判定：`internal/ledger/tasks.go:13-15,96-115`；卡侧过期判定：
  `internal/ledger/move.go:139-159`。
- SQLite 单连接与 PG/SQLite 方言选择：`internal/ledger/store.go:49-65`；事务串行化入口：
  `internal/ledger/store.go:152-165`。
- 时间入库/出库：`internal/ledger/store.go:125-145`；事件 JSON marshal 和真实事件读回：
  `internal/ledger/events.go:17-55,60-102`。
- `cards` 表的 `driver_session`/`driver_heartbeat_at` schema 保留位置：
  `internal/ledger/store.go:202-209,265-272`。

## Task 1：账本废除 TTL，增加原子显式接管事件

### 文件范围

- `internal/ledger/tasks.go`
- `internal/ledger/move.go`
- `internal/ledger/types.go`
- `internal/ledger/tasks_test.go`
- `internal/ledger/move_test.go`

不改 `internal/ledger/store.go` 的 DDL，不改 `internal/ledger/cards.go` 的列扫描顺序；
只在 `internal/ledger/types.go` 给保留的字段补语义注释，明确列名是兼容名。

### Interfaces

Consumes：`getCardTx(s *Store, tx *sql.Tx, id string) (Card, error)`、
`(*Store).mutate(func(*sql.Tx, *eventSink) error) error`、
`(*Store).appendEvent(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any) (int64, error)`。

Produces：上表五个 ledger 接口；删除
`func (s *Store) HeartbeatDriver(cardID, session string) error`，不得保留兼容空实现。

### 现状判据先跑

在修改任何文件前，于基线执行：

~~~sh
go test ./internal/ledger -count=1
~~~

已实跑结果：

~~~text
ok  github.com/Xsxdot/handoff/internal/ledger  12.418s
~~~

现有 `TestDriverLease` 会验证“旧心跳可抢占”，这是实现前必须先改红的旧契约。

### 步骤 1：先写并跑红测试

复用 `internal/ledger/cards_test.go` 的 `seedStore(t)` 和
`internal/ledger/relations_test.go` 的 `mk(t, s, title)`；不新造数据库夹具。把
`internal/ledger/tasks_test.go` 的 `TestDriverLease` 改成下列完整断言，并给
`tasks_test.go` 增加 `encoding/json` import：

~~~go
func TestDriverClaimDoesNotExpire(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("claim A: %v", err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(old), card.ID); err != nil {
		t.Fatalf("做旧认领时刻: %v", err)
	}
	if err := s.ClaimDriver(card.ID, "session-B"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("旧认领时刻也必须拒绝他会话: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.DriverSession != "session-A" {
		t.Fatalf("冲突认领不得改写驱动: %q", got.DriverSession)
	}
	if err := s.ClaimDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("同会话重入必须放行: %v", err)
	}
}
~~~

在 `TestClaimCardIsAtomic` 中把现有“第二个会话”认领断言整体替换为以下完整断言，确保
卡侧不是只修了 `ClaimDriver`；不要在原断言后再次声明同名 `err`；给
`move_test.go` 增加 `time` import：

~~~go
	old := time.Now().Add(-24 * time.Hour)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(old), c.ID); err != nil {
		t.Fatalf("做旧认领时刻: %v", err)
	}
	err := s.ClaimCard(c.ID, StatusDoing, StatusTodo, "sess-B#2")
	if !errors.Is(err, ErrCASConflict) || !strings.Contains(err.Error(), "sess-A#1") {
		t.Fatalf("旧认领时刻仍须点名原驱动并拒绝: %v", err)
	}
~~~

在 `internal/ledger/tasks_test.go` 增加下面两个测试，覆盖 release 幂等/权限和事件真实
序列化边界；第二个测试用 `*string` 区分 payload 键缺失与空字符串；同时把该文件的
import 增加 `encoding/json`：

~~~go
func TestReleaseCardOnlyOwnerCanClearAndOtherCanClaim(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.ReleaseCard(card.ID, "session-B"); err != nil {
		t.Fatalf("非持有者 release 应为无操作: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if got.DriverSession != "session-A" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("非持有者不得清空驱动或认领时刻: session=%q at=%v", got.DriverSession, got.DriverHeartbeatAt)
	}
	if err := s.ReleaseCard(card.ID, "session-A"); err != nil {
		t.Fatalf("owner release: %v", err)
	}
	if err := s.ClaimDriver(card.ID, "session-B"); err != nil {
		t.Fatalf("release 后应可被新会话认领: %v", err)
	}
}

func TestTakeoverCardWritesDriverAndRoundTripsPayload(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimDriver(card.ID, "session-old"); err != nil {
		t.Fatalf("claim old: %v", err)
	}
	if err := s.TakeoverCard(card.ID, "session-new", "cli:test@example"); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读接管后卡: %v", err)
	}
	if got.DriverSession != "session-new" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("接管必须写新驱动与认领时刻: session=%q at=%v", got.DriverSession, got.DriverHeartbeatAt)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	var found Event
	for _, event := range events {
		if event.Type == EvDriverTakeover {
			found = event
		}
	}
	if found.Type != EvDriverTakeover || found.Actor != "cli:test@example" {
		t.Fatalf("接管事件类型/actor 错误: %+v", found)
	}
	var payload struct {
		From *string `json:"from"`
		To   *string `json:"to"`
	}
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("解码接管 payload: %v", err)
	}
	if payload.From == nil || payload.To == nil || *payload.From != "session-old" || *payload.To != "session-new" {
		t.Fatalf("接管 payload 必须保留 from/to 字段: %+v", payload)
	}
}
~~~

最小红色测试命令：

~~~sh
go test ./internal/ledger -run 'TestDriverClaimDoesNotExpire|TestReleaseCardOnlyOwnerCanClearAndOtherCanClaim|TestTakeoverCardWritesDriverAndRoundTripsPayload|TestClaimCardIsAtomic' -count=1
~~~

预期：旧 `expired` 逻辑会让 stale 认领断言失败；`TakeoverCard` 尚不存在时测试编译失败。

### 步骤 2：最小实现

1. 在 `internal/ledger/types.go` 事件常量组加入：

~~~go
	EvDriverTakeover = "driver_takeover"
~~~

把 `Card.DriverHeartbeatAt` 的注释改为：

~~~go
	DriverHeartbeatAt time.Time `json:"driver_heartbeat_at,omitempty"` // 兼容列名；语义是认领时刻，不是续租心跳
~~~

2. 在 `internal/ledger/tasks.go` 删除 `driverLeaseTTL` 和 `HeartbeatDriver`，把
`ClaimDriver` 完整替换为：

~~~go
// ClaimDriver 认领驱动权：现驱动为空或为己才可得；非空的他会话永不因时间流逝自动释放。
// driver_heartbeat_at 保留兼容列名，但这里只在认领成功时写认领时刻。
func (s *Store) ClaimDriver(cardID, session string) error {
	log().Info("开始认领驱动", "card", cardID, "session", session)
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		card, err := getCardTx(s, tx, cardID)
		if err != nil {
			return fmt.Errorf("认领驱动: 卡 %s: %w", cardID, err)
		}
		if card.DriverSession != "" && card.DriverSession != session {
			log().Warn("驱动认领被拒", "card", cardID, "holder", card.DriverSession, "claimer", session)
			return fmt.Errorf("卡 %s 正由 %s 驱动: %w", cardID, card.DriverSession, ErrCASConflict)
		}
		if _, err = tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_heartbeat_at = ? WHERE id = ?`),
			session, s.tval(time.Now()), cardID); err != nil {
			return fmt.Errorf("写驱动: %w", err)
		}
		return nil
	})
	if err != nil {
		log().Warn("认领驱动失败", "card", cardID, "session", session, "cause", err)
		return err
	}
	log().Info("驱动已认领", "card", cardID, "session", session)
	return nil
}

// TakeoverCard 显式替换卡的驱动归属，并在同一事务写可审计事件。
// 参数：id 卡号；session 新驱动会话；actor 发起接管的人/入口标识。
// 注意：这是有意覆盖现有驱动的独立动作，不读取认领时刻，也不自动改变卡状态。
func (s *Store) TakeoverCard(id, session, actor string) error {
	log().Info("开始接管驱动", "card", id, "session", session, "actor", actor)
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("接管驱动: 卡 %s: %w", id, err)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_heartbeat_at = ? WHERE id = ?`),
			session, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("接管写驱动: %w", err)
		}
		if _, err := s.appendEvent(tx, sink, id, EvDriverTakeover, actor,
			map[string]string{"from": card.DriverSession, "to": session}); err != nil {
			return fmt.Errorf("接管落事件: %w", err)
		}
		return nil
	})
	if err != nil {
		log().Warn("接管驱动失败", "card", id, "session", session, "actor", actor, "cause", err)
		return err
	}
	log().Info("驱动已接管", "card", id, "session", session, "actor", actor)
	return nil
}
~~~

3. 在 `internal/ledger/move.go` 的 `ClaimCard` 删除 `expired := ...` 和 `!expired`，只保留下列冲突判定；其余事务顺序、状态 CAS、`ReleaseCard` SQL 和返回错误不改：

~~~go
		if card.DriverSession != "" && card.DriverSession != session {
			log().Warn("认领被拒：卡已有驱动", "card", id, "holder", card.DriverSession, "claimer", session)
			return fmt.Errorf("卡 %s 已被 %s 认领: %w", id, card.DriverSession, ErrCASConflict)
		}
~~~

4. 更新 `ReleaseCard` 的导出注释，删除“等满 5 分钟 TTL”的解释，保留“只动自己那份、
非持有者无操作、派发失败回滚必须调用”的事实。该函数不得增加事件、不得改成报错，
因为 `cmd/card_dispatch.go` 和 `StepRunner.Run` 的失败回滚依赖其幂等性。

### 步骤 3：日志与注释检查

入口日志带 `card/session/actor`；冲突、读卡、SQL、事件落账错误均带上下文，成功
认领/接管带结构化 `slog`；不得使用 `fmt.Print*` 作为日志。新事件 payload 的
`from/to` 产生点和消费测试必须在 `tasks.go`、`events.go`、`tasks_test.go` 可追溯；
“同一事务写卡 + 写事件”的原因写在 `TakeoverCard` 函数注释。

### 步骤 4：跑绿与 Task 1 验收

~~~sh
go test ./internal/ledger -run 'TestDriverClaimDoesNotExpire|TestReleaseCardOnlyOwnerCanClearAndOtherCanClaim|TestTakeoverCardWritesDriverAndRoundTripsPayload|TestClaimCardIsAtomic' -count=1
go test ./internal/ledger -count=1
~~~

验收必须逐条成立：旧认领时刻的 `ClaimDriver`/`ClaimCard` 都拒绝他会话；同会话重入
放行；release 后新会话立即可认领；takeover 后 `driver_session` 改变且真实事件 JSON
解码得到非空 `from`/`to`；非持有者 release 不报错且不清主。

## Task 2：删除节点流续租协程，保留回合内显式认领与释放

### 文件范围

- `internal/ledgerstep/runner.go`
- `internal/ledgerstep/runner_test.go`

不改 `internal/ledgerstep/node.go`、`internal/ledgerstep/dispatcher.go`、`cmd/card_node.go`、
`internal/agentd/cardstep.go`；`Session` 的装配契约仍由 B196 保留。

### Interfaces

Consumes：`(*Store).ClaimDriver(cardID, session string) error`、
`(*Store).ReleaseCard(cardID, session string) error`、
`(*NodeStep).RunOnce(ctx context.Context, cardID string) (Outcome, error)`。

Produces：`func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error)`
仍在纯人工节点跳过认领、可执行节点认领后运行、函数退出释放；`StepRunner` 只保留
`Session string` 作为驱动身份，不再暴露 `Heartbeat`、`HeartbeatInterval`。

### 现状判据先跑

在修改前执行：

~~~sh
go test ./internal/ledgerstep -count=1
~~~

已实跑结果：

~~~text
ok  github.com/Xsxdot/handoff/internal/ledgerstep  5.194s
~~~

### 步骤 1：为删除写红色静态断言

这是删除性 refactor；运行时心跳正是要删除的行为，不能写一个继续依赖已删除注入字段
的测试。先执行下列静态断言作为失败测试，基线必须因现有 B196 实现命中而退出 1：

~~~sh
if rg -n 'Heartbeat|startDriverHeartbeat|defaultDriverHeartbeatInterval' internal/ledgerstep/runner.go; then
	exit 1
fi
~~~

基线预期命中 `Heartbeat` 字段、`defaultDriverHeartbeatInterval`、`startDriverHeartbeat`；
这一步的红是明确指出待删除符号。同步从 `internal/ledgerstep/runner_test.go` 删除完整的
`TestRunnerHeartbeatsDuringLongRun`，因为它直接注入待删除的 `Heartbeat` 和
`HeartbeatInterval`；保留
`TestRunnerClaimsDriverWithoutChangingNodeStatusAndReleasesAfterRun`、
`TestRunnerRejectsActiveDriverAndReportsHolder`、`TestRunnerReleasesDriverAfterDispatchFailure`。

### 步骤 2：最小实现

1. `runner.go` 删除 `time` import、`StepRunner.Heartbeat`、
`StepRunner.HeartbeatInterval`、`defaultDriverHeartbeatInterval` 和完整的
`startDriverHeartbeat` 方法；在 `StepRunner` 的 `Session` 注释中写明它是一次运行的
驱动标识，不是续租 token。
2. 把 `Run` 的可执行节点收口替换为下列完整片段；这保留“先让节点回合退出、再释放
归属”的顺序，避免回合未退出时先把卡交给另一个驱动：

~~~go
	if err := r.St.ClaimDriver(cardID, session); err != nil {
		logger.Warn("认领节点驱动失败", "session", session, "cause", err)
		return Outcome{}, fmt.Errorf("认领节点驱动: %w", err)
	}
	logger.Info("节点驱动已认领", "session", session)
	defer func() {
		if err := r.St.ReleaseCard(cardID, session); err != nil {
			logger.Warn("释放节点驱动失败", "session", session, "cause", err)
			return
		}
		logger.Info("节点驱动已释放", "session", session)
	}()
	return nodeStep.RunOnce(ctx, cardID)
~~~

3. 保留入口 `logger.Info("进入节点执行")`、节点读取失败、空 session 拒绝、纯人工节点
跳过认领、认领失败、释放失败和成功认领/释放日志；删除所有“续租启动/成功/失败/停止”
日志。导出的 `Run` 注释补一句“认领时刻不会自动续期；异常遗留归属由 takeover/release
显式处置”，解释为什么 defer 仍必须存在。

### 步骤 3：跑绿与 Task 2 验收

~~~sh
test -z "$(rg -n 'Heartbeat|startDriverHeartbeat|defaultDriverHeartbeatInterval' internal/ledgerstep/runner.go)"
go test ./internal/ledgerstep -count=1
~~~

验收逐条成立：纯人工节点不留下驱动；可执行节点运行期间仍能看到 `Session`，完成和
派发失败都清空驱动；他会话冲突仍返回持有者；`runner.go` 不再含心跳字段、方法、常量
和续租日志。旧 `driver_heartbeat_at` 只由账本认领/接管写入，不得被节点流后台更新。

## Task 3：接出 release/takeover CLI，并把状态措辞改为认领时刻

### 文件范围

- `cmd/card_driver.go`（新文件）
- `cmd/card_driver_test.go`（新文件）
- `cmd/status.go`
- `cmd/status_test.go`

`cmd/card.go` 不直接改实现；新文件通过自己的 `init()` 向既有 `cardCmd` 注册两个子命令，
避免把驱动生命周期逻辑塞进卡元数据命令。`cmd/card_dispatch.go` 的失败回滚不改。

### Interfaces

Consumes：Task 1 的 `TakeoverCard(id, session, actor) error` 和
`ReleaseCard(id, session) error`；既有 `openLedger() (*ledger.Store, error)`、
`ledgerSession() string`、`ledgerActor() string`、`printCardJSON` 不改签名。

Produces：

~~~go
var cardReleaseCmd *cobra.Command
var cardTakeoverCmd *cobra.Command
~~~

命令线格式：两个命令成功均向 stdout 输出一行 `{"ok":true}`；失败原样返回带卡号/会话
上下文的错误，非交互环境不得要求确认。

### 现状判据先跑

在修改前执行：

~~~sh
go test ./cmd -count=1
~~~

已实跑结果：

~~~text
ok  github.com/Xsxdot/handoff/cmd  6.480s
~~~

### 步骤 1：先写并跑红测试

新建 `cmd/card_driver_test.go`，复用既有 `runLedgerCLI(t, dir, args...)`，不要另写
配置/根命令 harness。完整测试如下：

~~~go
package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestCardDriverCommandsTakeoverAndRelease(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "驱动卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "takeover", card.ID)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("takeover 应无确认且输出 ok: out=%q err=%v", out, err)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", card.ID)
	var snapshot struct {
		Card ledger.Card `json:"card"`
	}
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(show)), &snapshot) != nil || snapshot.Card.DriverSession == "" {
		t.Fatalf("takeover 后应有驱动会话: out=%q err=%v", show, err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "release", card.ID)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("release 应输出 ok: out=%q err=%v", out, err)
	}
	show, _, err = runLedgerCLI(t, dir, "card", "show", card.ID)
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(show)), &snapshot) != nil || snapshot.Card.DriverSession != "" || !snapshot.Card.DriverHeartbeatAt.IsZero() {
		t.Fatalf("release 后应清空驱动与认领时刻: out=%q err=%v card=%+v", show, err, snapshot.Card)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type != ledger.EvDriverTakeover {
			continue
		}
		found = true
		var payload struct {
			From *string `json:"from"`
			To   *string `json:"to"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("takeover payload: %v", err)
		}
		if payload.From == nil || payload.To == nil || *payload.To == "" {
			t.Fatalf("takeover payload 缺 from/to: %+v", payload)
		}
	}
	if !found {
		t.Fatal("CLI takeover 必须落 driver_takeover 事件")
	}
}
~~~

先跑：

~~~sh
go test ./cmd -run TestCardDriverCommandsTakeoverAndRelease -count=1
~~~

预期在命令注册前失败为 unknown command 或相应执行错误；测试必须实际红过后才进入实现。

### 步骤 2：最小实现

在 `cmd/card_driver.go` 写文件头职责/边界注释，并加入以下完整命令实现：

~~~go
// card_driver.go 把账本里的驱动归属生命周期接出 CLI。
// 边界：只调用 ledger.Store 的原子操作，不改变卡状态、不探测会话存活、不经 agentd。
package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
)

var cardReleaseCmd = &cobra.Command{
	Use:   "release <id>",
	Short: "主动交还卡的驱动归属",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id, session := args[0], ledgerSession()
		slog.Default().Info("CLI 释放驱动", "card", id, "session", session)
		if err := st.ReleaseCard(id, session); err != nil {
			slog.Default().Warn("CLI 释放驱动失败", "card", id, "session", session, "cause", err)
			return fmt.Errorf("释放卡 %s 的驱动: %w", id, err)
		}
		slog.Default().Info("CLI 释放驱动完成", "card", id, "session", session)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardTakeoverCmd = &cobra.Command{
	Use:   "takeover <id>",
	Short: "显式接管卡的驱动归属",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id, session, actor := args[0], ledgerSession(), ledgerActor()
		slog.Default().Info("CLI 接管驱动", "card", id, "session", session, "actor", actor)
		if err := st.TakeoverCard(id, session, actor); err != nil {
			slog.Default().Warn("CLI 接管驱动失败", "card", id, "session", session, "actor", actor, "cause", err)
			return fmt.Errorf("接管卡 %s 的驱动: %w", id, err)
		}
		slog.Default().Info("CLI 接管驱动完成", "card", id, "session", session, "actor", actor)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardCmd.AddCommand(cardReleaseCmd, cardTakeoverCmd)
}
~~~

修改 `cmd/status.go` 的渲染文案为：

~~~go
		} else if att.CardID != "" {
			line += fmt.Sprintf("  ⚠ 无人订阅（卡 %s 驱动 %s，认领于 %s）",
				att.CardID, att.Driver, claimAgeText(att.HeartbeatAge))
		}
~~~

并把原 `heartbeatAgeText` 完整改名为：

~~~go
// claimAgeText 把兼容字段 driver_heartbeat_at 的认领时刻年龄按整分钟展示；零值表示未知。
func claimAgeText(age time.Duration) string {
	if age <= 0 {
		return "未知"
	}
	return fmt.Sprintf("%dm 前", int(age/time.Minute))
}
~~~

`attendance` 的 `HeartbeatAge` 和 lookup 的 `heartbeatAt` 只在 status 包内部传递年龄，
不属于新增 wire 字段；如实现者同步改名，必须同步改 `cmd/status_test.go` 的全部调用，
不可留下一个“心跳”展示词。

### 步骤 3：日志、注释与测试更新

新文件头写职责/边界；每个 `RunE` 的入口、账本调用前后、错误分支和成功路径必须使用
结构化 `slog`，stdout 只输出机器 JSON。在 `cmd/status.go` 更新注释，说明 B184 判据
仍是 watchers，认领时刻只是补充展示。

在 `cmd/status_test.go` 保留 watchers/lookup 三态断言，仅把具体显示断言从
`心跳 12m 前` 改成 `认领于 12m 前`，零值从 `心跳 未知` 改成 `认领于 未知`；
不得删除 `无人订阅`、卡号、驱动会话和非 `无人值守` 反断言。

### 步骤 4：跑绿与 Task 3 验收

~~~sh
go test ./cmd -run 'TestCardDriverCommandsTakeoverAndRelease|TestAttendanceReportsCardDriverInsteadOfOrphan' -count=1
go test ./cmd -count=1
~~~

验收逐条成立：非 TTY 的 takeover 不需 `--yes` 且输出一行 `{"ok":true}`；takeover
覆盖原驱动并真实落 `driver_takeover` 的 `from/to`；release 只清当前 `ledgerSession`
对应的归属；状态仍以 watchers 判定“无人值守”，有卡驱动时只显示“无人订阅（卡…驱动…，
认领于…）”；零值显示“认领于 未知”。

## 序列化/投影边界清单

本卡不新增数据库列或跨语言协议字段，但新增 `driver_takeover` 事件及 `from/to` payload，
所以逐处锁定：

1. `internal/ledger/tasks.go#TakeoverCard`：手搭 `map[string]string{"from":..., "to":...}`，产生 payload。
2. `internal/ledger/events.go#appendEventAt`：真实 `json.Marshal(payload)` 写入 `card_events.payload`。
3. `internal/ledger/events.go#EventsFromAsc`：真实数据库读回后装入 `json.RawMessage`。
4. `internal/ledger/tasks_test.go#TestTakeoverCardWritesDriverAndRoundTripsPayload`：用 `*string` 解码并逐键断言。
5. `cmd/card_driver_test.go#TestCardDriverCommandsTakeoverAndRelease`：真实 CLI → SQLite → 事件读取链路，断言命令接线没有只更新内存。
6. `internal/ledger/cards.go#scanCard` 与 `cmd/status.go#renderStatusWithLookup`：既有
   `driver_heartbeat_at` 读出后拼展示文本；本卡只改最终词句，不增加另一套投影。

`driver_takeover` 不是状态值、附件 kind、purpose 或 workflow 状态，不经过既有白名单；
它只登记在 `internal/ledger/types.go` 的事件常量组，并由 `appendEvent` 接受任意事件类型。

## 缺陷族对抗审查（结论进入验收）

| 缺陷族 | 设问 | 计划结论与证据 |
|---|---|---|
| 生命周期/状态机中断 | 进程崩溃或人离开后谁回收？会不会自动双驱动？ | 不再自动回收；遗留归属保持可见，由 release/takeover 显式处置。`defer ReleaseCard` 仍覆盖节点正常/失败收口；Task 1 的旧时刻拒绝测试防止静默双驱动。 |
| 静默失败/误导报错 | 冲突是否点名持有者？接管是否可审计？展示是否还把认领时刻叫心跳？ | `ClaimDriver`/`ClaimCard` 仍返回 holder；`TakeoverCard` 与驱动写同事务；payload 穿真实 JSON 边界断言 `from/to`；CLI 和 status 的精确文案有回归测试。 |
| 跨平台假设 | pid/时间/终端行为是否新增平台分叉？ | 不新增平台 API；CLI 复用既有 `ledgerSession()`，takeover 不读 TTY、不确认；时间只写数据库既有 `time.Now()` 编码路径，SQLite/PG 方言由既有 `tval` 吸收。 |
| 假红测试 | 是否因没过 TTL 或“没报错”而假绿？ | stale 时间戳固定为 24 小时前；错误断言同时检查 sentinel、holder、驱动未改写；事件测试真实读回 JSON 并用可空指针区分缺字段；CLI 测试穿过真实根命令和 SQLite。删除心跳用静态失败断言，避免保留已删除字段的假测试。 |
| 门禁绕过 | 新写路径是否绕过账本事务或确认层？ | takeover/release 都只经 `openLedger` 与 Store 原子方法；takeover 按规格不设二次确认，不新增 force flag；stdout 仅机器结果，错误不吞。 |
| 新增枚举值白名单 | `driver_takeover` 是否要登记既有白名单？ | 已查事件类型使用面；事件类型没有校验白名单，唯一登记点是 `EvDriverTakeover`，测试按该常量匹配。 |

## Task 归属与 spec 覆盖

| 用户故事/规格条目 | 具体归属 |
|---|---|
| 半小时离开后仍归原驱动，不因时间被抢 | Task 1：`TestDriverClaimDoesNotExpire`、`TestClaimCardIsAtomic` 的 24 小时旧时刻断言 |
| 第二会话普通认领干净失败并点名持有者 | Task 1：`ClaimDriver`/`ClaimCard` 冲突断言；Task 2：节点流冲突回归 |
| 会话消失后人可显式接管并留下 from/to 审计 | Task 1：`TakeoverCard` 事务与真实 JSON roundtrip；Task 3：`card takeover` CLI 链路 |
| 看板/状态输出说认领时刻而非心跳 | Task 3：`cmd/status.go`、`cmd/status_test.go` 文案与零值断言 |
| 节点流不再续租，认领结束仍释放 | Task 2：删除协程/字段；既有三个 Run 回归测试与静态符号断言 |
| 派发失败回滚仍释放归属 | Task 1 保留 `ReleaseCard` 语义；Task 2 的 `TestRunnerReleasesDriverAfterDispatchFailure`；既有 `cmd/card_dispatch_test.go` |

## 任务边界、测试范围与最终验收

三个 task 的文件集均有界：Task 1 只触及 ledger 驱动/事件文件，Task 2 只触及
ledgerstep runner，Task 3 只触及 CLI 驱动命令/status；不把 `agentd`、前端或 schema
迁移带入本卡。每个 task 只跑上文列出的受影响包；全量测试不归任何单个 task。

Task 3 是本地集成边界（CLI → SQLite），没有远程网络/agentd 协议变更，因此不要求远端
真机。协调者在实现完成后执行下列显式本机清单：

1. 用最终构建的 CLI 在启用 ledger 的临时 `DataDir` 建一张 bug 卡。
2. 执行 `handoff --config <临时配置> card takeover <id>`，检查 stdout 恰为
   `{"ok":true}`，`card show` 中驱动非空，事件中有 `driver_takeover` 且 payload 有 `from/to`。
3. 执行 `handoff --config <临时配置> card release <id>`，检查 stdout 恰为
   `{"ok":true}`，`card show` 中 `driver_session` 为空且 `driver_heartbeat_at` 为零值。
4. 将数据库中的认领时刻改成 24 小时前，再用第二个 CLI 进程执行普通
   `card dispatch`/节点认领，确认仍返回原 holder；不得用等待 TTL 的方式验收。

最终验收（由协调者执行，不派发、不驱动 handoff CLI 的远程任务）：

~~~sh
go test ./... -count=1
go build ./...
go vet ./...
test -z "$(gofmt -l .)"
test -z "$(rg -n 'driverLeaseTTL|HeartbeatDriver|startDriverHeartbeat|defaultDriverHeartbeatInterval' internal/ledger internal/ledgerstep cmd --glob '*.go')"
~~~

最终变异检查必须逐条实际执行并记录红色结果后还原：

1. 临时把 `ClaimDriver` 的“非空他会话拒绝”改回按旧时刻放行，
   `go test ./internal/ledger -run TestDriverClaimDoesNotExpire -count=1` 必须失败。
2. 临时把 `ClaimCard` 同样改回 TTL 分支，
   `go test ./internal/ledger -run TestClaimCardIsAtomic -count=1` 必须失败。
3. 临时删除 `TakeoverCard` 的 `appendEvent`，
   `go test ./internal/ledger -run TestTakeoverCardWritesDriverAndRoundTripsPayload -count=1` 必须失败。
4. 临时删除 `StepRunner.Run` 的 `ReleaseCard`，
   `go test ./internal/ledgerstep -run TestRunnerReleasesDriverAfterDispatchFailure -count=1` 必须失败。
5. 临时把 status 输出词改回“心跳”，
   `go test ./cmd -run TestAttendanceReportsCardDriverInsteadOfOrphan -count=1` 必须失败。

### 计划自检

- spec 覆盖：四个用户故事和 B196 心跳清理条目均已指向具体 task、文件和测试。
- 占位符扫描：本文件所有 task 均给出精确路径、精确签名、可执行命令和完整新增函数/测试代码；测试夹具只复用已指认的 `seedStore`、`mk`、`runLedgerCLI`。
- 跨 task 类型/签名：Task 1 的 `TakeoverCard(id, session, actor) error` 与 Task 3 的调用逐字一致；Task 2 只消费既有 `ClaimDriver`/`ReleaseCard`，删除的心跳符号无消费者。
- 上下文预算：每张 task 的源码/测试文件集分别为 5、2、4 个，均可由单一执行者在包内闭环。
- 独立跨卡审计：本回合只有单上下文可用，冻结物逐条核对与跨卡签名比对结论标记为“待协调者在派发前复核”；已列出 B196 的 `StepRunner.Session` 保留边界，未假定未设置的基线分支。
