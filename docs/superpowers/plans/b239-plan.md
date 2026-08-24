# B239 实现计划：把「认领」一分为二——归属锁（人尺度）+ 运行锁（运行尺度）

> 上游 spec：`docs/superpowers/specs/2026-08-24-b239-claim-lock-split.md`（已批准）
> 契约冻结物：`docs/superpowers/specs/b239-contract.md`（§2–§6 + Ticket 0 骨架 + target.json/diffs 视图，提交 c7565808）
> 拆解稿：`docs/superpowers/specs/b239-breakdown.md`（三条岔口已由协调者 2026-08-25 拍板，裁决原文已回写该稿「待拍板岔口」节）
> 台账：`docs/superpowers/ledgers/ledger-b239-breakdown.md`（边干边追加；已按拍板裁决④挪入该目录）
> 分支：`cards/B239-charter-4`　基线：`2230c548`
> 档位：L3 / 轻档——**单执行者序贯消化，不扇出**；跨卡审计因无扇出不触发（无跨执行器子卡 plan 集合），以文末自审三查 + 独立上下文代审替代，代审结论标注「待拍板」。

---

## 零、开工前必读

### 0.1 判据基线复核记录（plan 轮已亲自跑过，2026-08-25，HEAD=2230c548）

实现者开工时须原样重跑以下命令并核对结果一致；不一致即停下提问，不许带着漂移的基线动手：

| 命令 | 基线结果 |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./internal/ledger ./internal/ledgerstep ./cmd ./internal/agentd` | exit 0 |
| `go test ./internal/ledger/ ./internal/ledgerstep/` | ok 12.629s / ok 5.568s |
| `go test ./cmd/ -run 'TestCardDispatch|TestResolveCardDispatchTemplate'` | ok 1.971s |
| `go test ./internal/agentd/ -run 'TestStartCardStep|TestRequiresInlineLocalFile'` | ok 0.945s |

调用面 grep 实测与契约 §1.1 名单逐一吻合（全文在台账）：`.ClaimCard(` 生产调用点仅 `cmd/card_dispatch.go:213`；ClaimDriver=定义 tasks.go:92-94 + 唯一调用 runner.go:97；`.ReleaseCard(` 三处（runner.go:103、card_driver.go:26、card_dispatch.go:240）；`ledgerSession()` 生产引用七处＋测试两处；StatusDoing 非测试命中五处；`"web:"+r.RemoteAddr` 七处行号 :89/:367/:386/:446/:522/:542/:693。go version go1.26.1 linux/amd64。

### 0.2 与拆解稿的三处显式偏差（均为执行完整性修正，不触契约冻结物）

| # | 偏差 | 为什么 |
|---|---|---|
| 1 | **U0 与 U4 合并为 Task A** | ClaimCard 收窄为 `(id, owner)` 后 `card_dispatch.go:213` 必然编译失败；且 `TestCardDispatchClaimAndSnapshot:159,162` 断言的正是被本卡废除的行为（认领转「进行中」、重复派发报「认领」错）。U0 无法「只改一行保绿」——硬拆必产生红色中间提交。合并后有界文件集 = 两单元并集，仍圈得出；断言覆盖不变（A 承接 1–12、33–37、39）。 |
| 2 | **node.go 进入 Task D 有界文件集**（拆解稿预期不动） | 写权 gate 的结构性收口需要 NodeStep 持闸（契约 §2.3 明文「达成机制归 plan」）；改动为纯增量字段＋守卫行，见 D2。 |
| 3 | **执行序 A→B→C→D**（拆解稿序 U0→U1→U2→U3→U4） | C（agentd 注入 RunHolder）先于 D（编排缝消费它），保证拒绝放行逻辑落地时生产装配已就位；B 先于 C（徽标测试要用 AcquireRunLock 真行为造夹具）；A 最先（其余任务的编译依赖新签名）。 |

### 0.3 全局纪律

- **测试范围声明**：每个 task 只跑触及包（各步骤写明 `-run` 表达式）；全量四包测试只属于收尾 Task E。
- **红绿模板只套在锁缝断言的步骤上**（下述各【红】【绿】步）；机械换符、日志、注释、纯映射步骤不配独立红绿周期。
- 各代码块内的 slog 调用即「关键节点日志」步骤、注释即「注释」步骤——均已写全，照抄不许删；禁 print。
- PG 冒烟由 `LEDGER_TEST_PG_DSN` 门控且清理段会删数据（store_pg_test.go:2,38）——**严禁指向生产卡账本**（postgres://…:54322）。全部判据在 SQLite 上闭环；PG 同构由 store.go 两分支 DDL 文本对照（:253-256 vs :319-322）承证；真 PG 冒烟归验收轮专用测试库。
- 本计划零派发、零 handoff CLI 写命令；breakdown 真机清单七条全部归协调者，不在实现验收内。
- 库行为出处（判据依据，均已亲读）：注入时钟 store.go:41-47（`s.now` 包内可直设，mirror_test.go:52 先例）；mutate 单事务串行 store.go:156-185；时间编解码 tval/toTime store.go:124-146；TTL 租约先例 mirror.go:94-127 AcquireMirrorLease；AddComment events.go:163 / MarkNeedsHuman events.go:264（reason 必填）/ RecordReviewVerdict :149 / ClearNeedsHumanFrom :296；事件常量 EvComment/EvNeedsHuman/EvDriverTakeover types.go:58/:60/:65；错误哨兵 ErrNotFound/ErrCASConflict/ErrBadState 经 fmt %w wrap 后 errors.Is 可识别（move.go 既有用法同款）；HTTP 错误映射 ledgerErr ledgerapi.go:67-78（ErrCASConflict→409 已有，无需新增哨兵）。

### 0.4 Interfaces 总表（跨任务签名逐字对照，实现不得改拼）

```go
// ── Task A 产出（internal/ledger 门面 + cmd 面）──────────────────────
func (s *Store) ClaimCard(id, owner string) error            // 收窄（旧四参删除）
func (s *Store) ReleaseCard(id, session string) error        // 签名不变，语义反转
//   删除：func (s *Store) ClaimDriver(cardID, session string) error
//   删除：cmd 包 func ledgerSession() string

// ── Task B 产出（Ticket 0 空壳填肉，签名与契约 §2.2 逐字一致）────────
const (
    RunLockTTL           = 5 * time.Minute   // internal/ledger/runlock.go 包级
    RunLockRenewInterval = 2 * time.Minute
)
func (s *Store) AcquireRunLock(cardID, node, holder string, ttl time.Duration) (RunLock, bool, error)
func (s *Store) RenewRunLock(cardID, holder string, ttl time.Duration) (bool, error)
func (s *Store) ReleaseRunLock(cardID, holder string) error
func (s *Store) RunLockOf(cardID string) (RunLock, bool, error)
func (s *Store) AllRunLocks() ([]RunLock, error)

// ── Task C 消费 B 的 AcquireRunLock/AllRunLocks/RunLock.ExpiresAt；
//    产出 StepRunner.RunHolder 装配 run:<host>#<pid>#<unixnano>、徽标新判据、hostOnly helper

// ── Task D 消费 B 三锁方法 + A 新签名 + B 两常量；产出两个注入缝：
type StepRunner struct {
    // …既有字段不动…
    RenewBeat <-chan time.Time // 续租节拍源（岔口三裁决）：nil = 生产 time.Ticker(RunLockRenewInterval)
}
type NodeStep struct {
    // …既有字段不动…
    WriteGate func() bool      // 卡写闸：nil = 不设闸；false = 拒绝该次卡写
}
var ErrWriteGateClosed = errors.New("运行锁已失去写权") // internal/ledgerstep 包级哨兵
```

---

## Task A：归属锁面改造 + CLI 归属链路（断言 1–12、33–37、39）

**文件集**：`internal/ledger/move.go`、`internal/ledger/tasks.go` ／ `internal/ledger/move_test.go`、`internal/ledgerstep/runner.go`（仅 :97 机械换符）、`internal/ledgerstep/runner_test.go`（仅 ：252 一处换符保编译）、`cmd/card_driver.go`、`cmd/card_dispatch.go`、`cmd/card_node.go`、`cmd/ledgercli.go` ／ `cmd/card_dispatch_test.go`。

**最薄路径条声明**：本卡要锁的缝 1 行为今天从缝上调用是错的（非持有者释放假成功、认领转状态、pid 身份）——Task A 就是点亮行为的第一条最薄路径，红步写下去必红，不依赖任何前置 task。

### A0 基线复核

重跑 0.1 五条命令，结果一致才继续；不一致 → 停下提问（单行 JSON 工单）。

### A1 【红】归属语义测试先行

把 `internal/ledger/move_test.go` 的 `TestClaimCardIsAtomic` 整体替换为以下两个测试（原子性论证随状态解耦作废，旧注释一并删）：

```go
// TestClaimCardOwnershipSemantics 归属锁全集（b239-contract.md §3 断言 1–7）。
// 归属是人尺度：不随时间流逝转移（8-23 decision #1）、不改状态列、幂等重入。
func TestClaimCardOwnershipSemantics(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "归属", Project: "p", Workflow: "bug", Actor: "t"})
	before, _ := s.GetCard(c.ID)

	// 断言 1：卡不存在 → ErrNotFound
	if err := s.ClaimCard("B99999", "cli:a@h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	// 空 owner 拒绝：不许静默清空归属
	if err := s.ClaimCard(c.ID, ""); err == nil {
		t.Fatalf("空 owner 应被拒")
	}
	// 首次认领成功
	if err := s.ClaimCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("首次认领: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	// 断言 6：状态列一字节都不动（钉 B237/B213 根因）
	if got.Status != before.Status {
		t.Fatalf("认领不得改状态列: before=%q after=%q", before.Status, got.Status)
	}
	// 断言 7：driver_session=owner 且 driver_heartbeat_at=本次认领时刻
	if got.DriverSession != "cli:a@h" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("认领应写归属与认领时刻: %+v", got)
	}
	// 认领本身不落任何事件（契约 §2.1 规则 7）
	events, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
	for _, e := range events {
		if e.Type == EvDriverTakeover || e.Type == EvStatusMoved {
			t.Fatalf("认领不得落事件: %+v", e)
		}
	}

	// 把认领时刻做旧到 30 天前，再让他主来认领：
	// 断言 3+4：无论对方认领时刻多久以前照拒且点名持有者（防 TTL 从归属侧回流）
	old := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(old), c.ID); err != nil {
		t.Fatalf("做旧认领时刻: %v", err)
	}
	err := s.ClaimCard(c.ID, "cli:b@h")
	if !errors.Is(err, ErrCASConflict) || !strings.Contains(err.Error(), "cli:a@h") {
		t.Fatalf("他主持有应拒且点名持有者: %v", err)
	}
	// 断言 5：同 owner 重入幂等成功（换进程重试路径依赖它）
	if err := s.ClaimCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("同 owner 重入应幂等: %v", err)
	}
	// 断言 2：终态卡拒绝（这层今天由 moveCardTx 顺带提供，解耦后必须显式补回）
	_ = s.MoveCard(c.ID, StatusDone, "", "t")
	if err := s.ClaimCard(c.ID, "cli:c@h"); !errors.Is(err, ErrBadState) {
		t.Fatalf("终态卡认领应 ErrBadState: %v", err)
	}
}

// TestReleaseCardOwnershipSemantics 归属释放反转（断言 8–11）。
// 今天非持有者释放是静默 no-op + CLI 假成功——这是本卡核心行为反转点。
func TestReleaseCardOwnershipSemantics(t *testing.T) {
	s := seedStore(t)
	// 断言 8：卡不存在 → ErrNotFound（今天静默 nil）
	if err := s.ReleaseCard("B99999", "cli:x@h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	c, _ := s.CreateCard(NewCard{Title: "释放", Project: "p", Workflow: "bug", Actor: "t"})
	// 断言 9：无主卡释放幂等成功（空转不是错误）
	if err := s.ReleaseCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("无主卡释放应幂等成功: %v", err)
	}
	// 断言 10：持有者本人释放 → 两字段清空
	if err := s.ClaimCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("认领: %v", err)
	}
	if err := s.ReleaseCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("本人释放: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.DriverSession != "" || !got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("本人释放应清空两个字段: %+v", got)
	}
	// 断言 11：非持有者 → 可见失败、含持有者、归属未被改动
	_ = s.ClaimCard(c.ID, "cli:a@h")
	err := s.ReleaseCard(c.ID, "cli:b@h")
	if !errors.Is(err, ErrCASConflict) || !strings.Contains(err.Error(), "cli:a@h") {
		t.Fatalf("非持有者释放应可见失败并点名持有者: %v", err)
	}
	after, _ := s.GetCard(c.ID)
	if after.DriverSession != "cli:a@h" {
		t.Fatalf("失败的释放不得动归属: %q", after.DriverSession)
	}
}
```

跑红（期待编译失败＝红，旧四参签名对不上两参调用；贴原始输出进台账）：

```
go test ./internal/ledger/ -run 'TestClaimCardOwnershipSemantics|TestReleaseCardOwnershipSemantics'
```

### A2 【绿】账本侧最小实现

`internal/ledger/move.go` 用下面两个函数整体替换旧 `ClaimCard`（:140-165）与旧 `ReleaseCard`（:167-201 及其 doc 注释）：

```go
// ClaimCard 认领卡的归属锁（人尺度）。
//
// 参数：id 卡号；owner 人尺度持有者身份（cli:<user>@<host> 档，不带 pid——
// pid 由运行锁承担，见 b239-contract.md §2.1）。
// 返回：成功 nil。规则（一次 mutate 事务内完成判定+写入）：
//   - 卡不存在 → ErrNotFound；
//   - 卡处于终态（已完成/终止）→ ErrBadState（解耦后显式补回，否则裸 dispatch
//     能给终止卡认领）；
//   - owner 为空串 → 参数错误（不许静默清空归属）；
//   - 已有非空他主且 ≠ owner → wrap ErrCASConflict 且报文含持有者标识；
//     无论对方认领时刻多近或多久以前照拒——归属不因时间流逝转移（8-23 decision #1）；
//   - 同主重入 → 幂等成功。
//
// 注意：认领不改状态列、不落任何事件（B239：认领与状态 CAS 彻底解耦）。
func (s *Store) ClaimCard(id, owner string) error {
	log().Info("开始认领归属", "card", id, "owner", owner)
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		if owner == "" {
			// 规则 3 与其余判定同事务：不许静默清空归属。
			log().Warn("认领被拒：owner 为空", "card", id)
			return fmt.Errorf("认领被拒：owner 为空")
		}
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("认领: 卡 %s: %w", id, err)
		}
		if card.Status == StatusDone || card.Status == StatusClosed {
			log().Warn("认领被拒：终态卡", "card", id, "status", card.Status)
			return fmt.Errorf("卡 %s 已处于终态 %s: %w", id, card.Status, ErrBadState)
		}
		if card.DriverSession != "" && card.DriverSession != owner {
			log().Warn("认领被拒：他主持有", "card", id,
				"holder", card.DriverSession, "claimer", owner)
			return fmt.Errorf("卡 %s 已由 %s 认领: %w", id, card.DriverSession, ErrCASConflict)
		}
		// 认领时刻沿用旧 move.go 写法（wall clock；契约 §2.1 规则 6「沿用现状写法」）
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_heartbeat_at = ? WHERE id = ?`),
			owner, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("认领写归属: %w", err)
		}
		return nil
	})
	if err != nil {
		log().Warn("认领归属失败", "card", id, "owner", owner, "cause", err)
		return err
	}
	log().Info("归属已认领", "card", id, "owner", owner)
	return nil
}

// ReleaseCard 释放归属锁。
//
// 参数：id 卡号；session 调用方的人尺度归属身份（与 ClaimCard 的 owner 同档同值）。
// 返回：规则：
//   - 卡不存在 → ErrNotFound（B239 行为变更点：今天静默 nil）；
//   - 无主卡 → 幂等成功（空转不是错误）；
//   - 自己持有 → 清空 driver_session 与 driver_heartbeat_at；
//   - 他主持有 → wrap ErrCASConflict 且报文含当前持有者，归属不被改动
//     （B239 行为变更点：今天静默 no-op，CLI 打印 {"ok":true} 假成功）。
//
// 人要的是「到底放了没有」的确认，所以只有这里不对称地报错；
// 运行锁的非持有者释放是 no-op（失去信号在 RenewRunLock），两边职责不同。
func (s *Store) ReleaseCard(id, session string) error {
	log().Info("开始释放归属", "card", id, "session", session)
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("释放: 卡 %s: %w", id, err)
		}
		switch {
		case card.DriverSession == "":
			log().Info("释放无操作：卡无主", "card", id)
			return nil
		case card.DriverSession == session:
			if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = '', driver_heartbeat_at = ?
				WHERE id = ?`), s.tval(time.Time{}), id); err != nil {
				return fmt.Errorf("释放归属: %w", err)
			}
			log().Info("归属已释放", "card", id, "session", session)
			return nil
		default:
			log().Warn("释放被拒：非持有者", "card", id,
				"holder", card.DriverSession, "caller", session)
			return fmt.Errorf("卡 %s 由 %s 持有：%s 无权释放: %w",
				id, card.DriverSession, session, ErrCASConflict)
		}
	})
	if err != nil {
		log().Warn("释放归属失败", "card", id, "session", session, "cause", err)
		return err
	}
	return nil
}
```

`internal/ledger/tasks.go`：整体删除 `ClaimDriver`（:92-117 含 doc 注释）——它与收窄版 ClaimCard 完全同义，双符号等价入口是拍板 §5.1 明令消灭的后漂移源。`TakeoverCard` 一字不动。

跑绿（只跑触及包；此时 ledgerstep/cmd 未适配会编译失败，属预期，先不提交）：

```
go test ./internal/ledger/
```

### A3 机械换符（保编译，不做行为改造）

1. `internal/ledgerstep/runner.go:97`：`r.St.ClaimDriver(cardID, session)` 改为 `r.St.ClaimCard(cardID, session)`，其下一行日志文案「认领节点驱动失败」改「认领归属失败」。defer ReleaseCard 与其余流程一字不动（那是 Task D 的活）。
2. `internal/ledgerstep/runner_test.go:244`：`st.ClaimDriver(card.ID, "session-holder")` 改 `st.ClaimCard(card.ID, "cli:holder@h")`；同函数两处断言字符串 `"session-holder"` 同步改 `"cli:holder@h"`（Task D 再把它扩成断言 27 完整形）。

### A4 cmd 生产面（契约 §2.4 四行变更 + ledgerSession 删除）

`cmd/card_driver.go` 整体重写为：

```go
// card_driver.go 把账本里的驱动归属生命周期接出 CLI。
// 边界：只调用 ledger.Store 的原子操作，不改变卡状态、不探测会话存活、不经 agentd。
// B239：归属身份降为人尺度 ledgerActor()（不带 pid）；release 在非持有者时
// 从静默假成功反转为可见失败——CLI 退出码非零、stderr 含当前持有者。
package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
)

var cardReleaseCmd = &cobra.Command{
	Use:   "release <id>",
	Short: "主动交还卡的驱动归属（非持有者会失败并告知持有者）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, owner := args[0], ledgerActor()
		slog.Default().Info("CLI 释放归属入口", "card", id, "owner", owner)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 释放归属打开账本失败", "card", id, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.ReleaseCard(id, owner); err != nil {
			// 库层 wrap 了 ErrCASConflict 并带持有者标识：cobra 把它打到 stderr，
			// main 以非零退出——这就是「可见失败」的人机两面。
			slog.Default().Warn("CLI 释放归属失败", "card", id, "owner", owner, "cause", err)
			return fmt.Errorf("释放卡 %s 的归属: %w", id, err)
		}
		slog.Default().Info("CLI 释放归属完成", "card", id, "owner", owner)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardTakeoverCmd = &cobra.Command{
	Use:   "takeover <id>",
	Short: "显式接管卡的驱动归属（归属落到人名下）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, owner, actor := args[0], ledgerActor(), ledgerActor()
		slog.Default().Info("CLI 接管归属入口", "card", id, "owner", owner, "actor", actor)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 接管归属打开账本失败", "card", id, "cause", err)
			return err
		}
		defer st.Close()
		if err := st.TakeoverCard(id, owner, actor); err != nil {
			slog.Default().Warn("CLI 接管归属失败", "card", id, "actor", actor, "cause", err)
			return fmt.Errorf("接管卡 %s 的归属: %w", id, err)
		}
		slog.Default().Info("CLI 接管归属完成", "card", id, "owner", owner)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardCmd.AddCommand(cardReleaseCmd, cardTakeoverCmd)
}
```

`cmd/card_dispatch.go` 裸 dispatch 主流程（现 ：203-243 区段，从 `actor := ledgerActor()` 到 `return json.NewEncoder(...)`）替换为：

```go
		actor := ledgerActor()
		card, err := st.GetCard(id)
		if err != nil {
			return err
		}
		// 守卫改判归属锁（B213/B237 消灭点）：他主才拒、报文含持有者；
		// 无驱动的卡照常放行（story 8）。真正的门仍在 ClaimCard 库层，
		// 这里只是提前报错美化（提示与门分层，见 breakdown 缺陷族 5）。
		if card.DriverSession != "" && card.DriverSession != actor {
			return fmt.Errorf("卡 %s 已由 %s 认领: %w", id, card.DriverSession, ledger.ErrCASConflict)
		}
		// 认领归属，不转状态列（story 7，charter 流从此跑得通）；库层同样判他主/终态。
		if err := st.ClaimCard(id, actor); err != nil {
			return fmt.Errorf("认领失败: %w", err)
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		dispatcher := &ledgerstep.Dispatcher{St: st, Transport: cliTransport, Actor: actor}
		result, err := dispatcher.ViaTemplate(ctx, card, ledgerstep.TemplateDispatch{
			Template:           templateName,
			Target:             cardDispatchTarget,
			PlanPath:           cardDispatchPlan,
			DisciplineOverride: cardDispatchDiscipline,
			ExecutorOverride:   cardDispatchExecutor,
			ModelOverride:      cardDispatchModel,
			Extra:              cardDispatchExtra,
		})
		if err != nil {
			// 回滚只退归属（没有状态转移要回退了，MoveCard 回退删除）；
			// 归属不带 pid，同一人换个进程也能自己清掉。
			_ = st.ReleaseCard(id, actor)
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
```

同时把该文件头注释第 3 行「派发即认领（CAS 进『进行中』就是 claim…）」改为「派发即认领归属（不动状态列；运行互斥归账本运行锁）」。`cmd/card_node.go:40`：`Actor: ledgerSession(),` → `Actor: ledgerActor(),`。`cmd/ledgercli.go`：删除 `ledgerSession` 函数与 doc 注释（:56-62）。

### A5 【红→绿】cmd 面测试迁移

`cmd/card_dispatch_test.go`：

1. 重写 `TestCardDispatchClaimAndSnapshot`（:115-170）为：

```go
func TestCardDispatchClaimAndSnapshot(t *testing.T) {
	dir := t.TempDir()

	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "update", c.ID, "--accept", "测试全绿"); err != nil {
		t.Fatal(err)
	}

	var gotPrompt, gotProject string
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		gotPrompt, gotProject = prompt, project
		return "T-fake-1", nil
	})
	defer restore()

	out, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", "implement")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out, "T-fake-1") {
		t.Fatalf("输出应含 task id: %q", out)
	}
	if strings.Contains(gotPrompt, "# 执行纪律") || !strings.Contains(gotPrompt, "要派的卡") ||
		!strings.Contains(gotPrompt, "测试全绿") {
		t.Fatalf("prompt 拼装: %q", gotPrompt)
	}
	if gotProject != "demo" {
		t.Fatalf("派发未带 project: %q", gotProject)
	}

	// 直开同一 SQLite 文件核结构事实（openLedger 回退路径就是 DataDir/ledger.db）
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 断言 36：裸 dispatch 成功后卡停留在原状态列（待办），不再被推去「进行中」。
	if card.Status != ledger.StatusTodo {
		t.Fatalf("裸 dispatch 不得挪列，实际 %q", card.Status)
	}
	// 断言 39 行为面：新写入的归属是人尺度 cli:<user>@<host>，不含 pid 词形。
	if card.DriverSession == "" || strings.Contains(card.DriverSession, "#") ||
		!strings.HasPrefix(card.DriverSession, "cli:") {
		t.Fatalf("归属应为人尺度身份: %q", card.DriverSession)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "dispatched") || !strings.Contains(show, "discipline_name") {
		t.Fatalf("快照事件缺失: %q", show)
	}
}
```

2. 新增两条（追加文件末尾；import 补 `"path/filepath"`）：

```go
// 断言 35：他主持有的卡拒绝且报文含持有者（对照组：无驱动卡放行已在上面覆盖）。
func TestCardDispatchGuardFollowsOwnership(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "守卫卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimCard(c.ID, "cli:other@remote-host"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	st.Close()
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		t.Fatal("他主持有时不应走到派发")
		return "", nil
	})
	defer restore()
	_, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02")
	if err == nil || !strings.Contains(err.Error(), "cli:other@remote-host") {
		t.Fatalf("他主持有应拒且点名持有者: %v", err)
	}
}

// 断言 5 的 CLI 面：同人重入幂等放行（归属不带 pid，换进程仍是同一个人，
// 这正是旧 pid 身份做不到的——旧测试断言的「重复派发必失败」随 pid 一起废除）。
func TestCardDispatchSameOwnerReentryIdempotent(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "重入卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := 0; i < 2; i++ {
		restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
			n++
			return fmt.Sprintf("T-reentry-%d", n), nil
		})
		if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
			"--template", "feature-impl", "--target", "mac-02"); err != nil {
			t.Fatalf("同人第 %d 次派发应放行: %v", i+1, err)
		}
		restore()
	}
}
```

3. `TestCardDispatchFailureReleasesLease` 保持主体不动（回滚只退归属后其判据依旧成立），在 `show` 断言处补一行（断言 37）：

```go
	if !strings.Contains(show, `"status":"待办"`) && !strings.Contains(show, `"Status":"待办"`) {
		t.Fatalf("派发失败回滚不得动状态列: %q", show)
	}
```

4. `TestCardDispatchStepUsesPIDActor`（:439-463）改名 `TestCardDispatchStepUsesActorIdentity`，断言改为：

```go
	if actor := cardStepString(t, got, "actor"); actor != ledgerActor() || strings.Contains(actor, "#") {
		t.Fatalf("wire actor = %q, want 人尺度 ledgerActor %q（不含 pid）", actor, ledgerActor())
	}
```

5. 追加 release/takeover CLI 测试：

```go
// 断言 33：release 非持有者 → 可见失败（返回 err 即 main 的非零退出）且
// stderr 含当前持有者；持有者本人 → {"ok":true}。
func TestCardReleaseRejectsNonHolderAndSucceedsForOwner(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "释放卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimCard(c.ID, "cli:someone-else@far-away"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	st.Close()
	_, stderr, err := runLedgerCLI(t, dir, "card", "release", c.ID)
	if err == nil {
		t.Fatal("非持有者 release 必须失败（这里是假成功的反转点）")
	}
	combined := stderr + err.Error()
	if !strings.Contains(combined, "cli:someone-else@far-away") {
		t.Fatalf("失败报文必须点名持有者: stderr=%q err=%v", stderr, err)
	}
	// 本人路径：takeover 到自己再 release，应成功打印 {"ok":true}（story 6 闭环）
	if _, _, err := runLedgerCLI(t, dir, "card", "takeover", c.ID); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	stdout, _, err := runLedgerCLI(t, dir, "card", "release", c.ID)
	if err != nil || !strings.Contains(stdout, `{"ok":true}`) {
		t.Fatalf("持有者 release 应成功: %q %v", stdout, err)
	}
}

// 断言 12 的 CLI 面：takeover 后归属落到人尺度身份，payload from/to 形状不变。
func TestCardTakeoverAssignsHumanIdentity(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "接管卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ClaimCard(c.ID, "cli:prev@h1"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "takeover", c.ID); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	card, err := st.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.DriverSession != ledgerActor() {
		t.Fatalf("takeover 后归属应是本 CLI 人尺度身份: %q want %q", card.DriverSession, ledgerActor())
	}
	events, err := st.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type != ledger.EvDriverTakeover {
			continue
		}
		var payload struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("payload 解码: %v", err)
		}
		if payload.From != "cli:prev@h1" || payload.To != ledgerActor() {
			t.Fatalf("takeover payload from/to = %q/%q", payload.From, payload.To)
		}
		found = true
	}
	if !found {
		t.Fatal("takeover 必须落 driver_takeover 事件")
	}
}
```

跑绿：

```
go test ./cmd/ -run 'TestCardDispatch|TestCardRelease|TestCardTakeover|TestResolveCardDispatchTemplate'
go test ./internal/ledger/ ./internal/ledgerstep/
```

### A6 断言 39 口径一（grep 执法）

```
grep -rn "ledgerSession" --include="*.go" cmd/ internal/ | grep -v _test.go
```

期待零输出。命令与输出原文记台账。

### A7 提交

`gofmt -l` 触碰文件为空；`go build ./... && go vet ./internal/ledger ./internal/ledgerstep ./cmd ./internal/agentd` 通过后提交：
`feat(b239): 归属锁面——ClaimCard 收窄/ReleaseCard 反转/删 ClaimDriver/CLI 人尺度身份（断言 1-12,33-37,39）`

---

## Task B：运行锁面实现（断言 13–23 + payload 金样本）

**文件集**：`internal/ledger/runlock.go` ／ `internal/ledger/runlock_test.go`（新）。

### B1 【红】运行锁语义测试先行

新建 `internal/ledger/runlock_test.go`，完整内容：

```go
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// rlCard 造一张挂 bug 流的测试卡（card_run_locks 的 FK 目标）。
func rlCard(t *testing.T, s *Store) Card {
	t.Helper()
	c, err := s.CreateCard(NewCard{Title: "运行锁", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func countEvents(t *testing.T, s *Store, cardID string) int {
	t.Helper()
	events, err := s.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

// 断言 13/14/17/18/19：取得、拒绝、首取无事件、同 holder 重入刷租期、续租。
func TestAcquireRunLockBasics(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	// 断言 13：卡不存在 → ErrNotFound
	if _, _, err := s.AcquireRunLock("B99999", "n", "run:h1#1#1", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	before := countEvents(t, s, c.ID)
	lock, acquired, err := s.AcquireRunLock(c.ID, "implement", "run:h1#1#1", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("首取: acquired=%v err=%v", acquired, err)
	}
	if lock.Holder != "run:h1#1#1" || lock.Node != "implement" || !lock.ExpiresAt.After(lock.AcquiredAt) {
		t.Fatalf("首取行不符: %+v", lock)
	}
	// 断言 17：首次取得不产生任何卡事件（长回合高频派发不被噪声淹没）
	if got := countEvents(t, s, c.ID); got != before {
		t.Fatalf("首取不得落卡事件: %d → %d", before, got)
	}
	// 断言 14：租期内他人取得被拒，acquired=false，返回现存行素材且原行不动
	other, acquired2, err := s.AcquireRunLock(c.ID, "review", "run:h2#2#2", time.Minute)
	if err != nil || acquired2 {
		t.Fatalf("租期内他人应被拒: acquired=%v err=%v", acquired2, err)
	}
	if other.Holder != "run:h1#1#1" || other.Node != "implement" {
		t.Fatalf("被拒应返回现存行（谁在跑/哪个节点/到期）: %+v", other)
	}
	// 逐字段比较而非结构体相等：time.Time 出库后 location 不同，== 会假红。
	after, ok, _ := s.RunLockOf(c.ID)
	if !ok || after.Holder != lock.Holder || after.Node != lock.Node ||
		!after.AcquiredAt.Equal(lock.AcquiredAt) || !after.ExpiresAt.Equal(lock.ExpiresAt) {
		t.Fatalf("被拒路径不得改原行: %+v vs %+v", after, lock)
	}
	// 断言 18：同 holder 重入刷新 expires_at 且不动 acquired_at（假时钟精确断言，
	// 包内直设 s.now——mirror_test.go:52 先例；零真实等待）
	cur := time.Now()
	s.now = func() time.Time { return cur }
	re, acq3, err := s.AcquireRunLock(c.ID, "implement", "run:h1#1#1", 2*time.Minute)
	if err != nil || !acq3 {
		t.Fatalf("同 holder 重入: %v %v", acq3, err)
	}
	if !re.AcquiredAt.Equal(lock.AcquiredAt) || !re.ExpiresAt.Equal(cur.Add(2*time.Minute)) {
		t.Fatalf("重入应只刷租期: 取得时刻 %+v 租期 %+v want %+v",
			re.AcquiredAt, re.ExpiresAt, cur.Add(2*time.Minute))
	}
	// 断言 19：续租为真 → expires_at=now+ttl；非持有者 → false 且无副作用
	okRenewed, err := s.RenewRunLock(c.ID, "run:h1#1#1", 3*time.Minute)
	if err != nil || !okRenewed {
		t.Fatalf("持有者续租: %v %v", okRenewed, err)
	}
	row, _, _ := s.RunLockOf(c.ID)
	if !row.ExpiresAt.Equal(cur.Add(3 * time.Minute)) {
		t.Fatalf("续租后 expires_at=%v want %v", row.ExpiresAt, cur.Add(3*time.Minute))
	}
	foreigner, err := s.RenewRunLock(c.ID, "run:other#9#9", time.Minute)
	if foreigner || err != nil {
		t.Fatalf("非持有者续租应 false,nil: %v %v", foreigner, err)
	}
	row2, _, _ := s.RunLockOf(c.ID)
	if !row2.ExpiresAt.Equal(row.ExpiresAt) {
		t.Fatalf("非持有者续租不得有副作用: %+v vs %+v", row2, row)
	}
}

// 断言 15+16：注入时钟推过租期 → 他人取得成功、覆盖四字段、落抢占事件；
// payload 金样本 from/to/reason 三键恰一（契约 §4.7 欠账销账处，序列化边界回归）。
func TestAcquireRunLockPreemptsExpiredWithGoldPayload(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	base := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	cur := base
	s.now = func() time.Time { return cur }
	if _, acq, err := s.AcquireRunLock(c.ID, "implement", "run:old#1#1", time.Minute); err != nil || !acq {
		t.Fatalf("预占: %v %v", acq, err)
	}
	cur = base.Add(2 * time.Minute) // 注入时钟推过租期，不真等 5 分钟
	lock, acq2, err := s.AcquireRunLock(c.ID, "review", "run:new#2#2", 5*time.Minute)
	if err != nil || !acq2 {
		t.Fatalf("过期后应可取得: %v %v", acq2, err)
	}
	if lock.Holder != "run:new#2#2" || lock.Node != "review" ||
		!lock.AcquiredAt.Equal(cur) || !lock.ExpiresAt.Equal(cur.Add(5*time.Minute)) {
		t.Fatalf("抢占应覆盖四个字段: %+v", lock)
	}
	events, err := s.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *Event
	for i := range events {
		if events[i].Type == EvDriverTakeover {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatal("过期抢占必须落 driver_takeover 事件（card wait 的唯一通道）")
	}
	if found.Actor != "run:new#2#2" {
		t.Fatalf("抢占事件 actor 应为新 holder: %q", found.Actor)
	}
	// 金样本：payload 恰好三键，from/to 值精确、reason 非空人读短句。
	// 缺一键或多余一键即红——web 泛化渲染按 reason 直读，payload 形状是看板 wire 的一部分。
	var payload map[string]string
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("payload 解码: %v", err)
	}
	if len(payload) != 3 {
		t.Fatalf("payload 键数=%d，want 恰好 from/to/reason 三键: %v", len(payload), payload)
	}
	if payload["from"] != "run:old#1#1" || payload["to"] != "run:new#2#2" {
		t.Fatalf("payload from/to = %q/%q", payload["from"], payload["to"])
	}
	if payload["reason"] == "" {
		t.Fatal("payload.reason 必须是非空人读短句")
	}
}

// 断言 20+21：释放即时生效（不必等租期）；非持有者释放 no-op 且不动他人行。
func TestReleaseRunLockSemantics(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	if _, acq, err := s.AcquireRunLock(c.ID, "n", "run:a#1#1", time.Minute); err != nil || !acq {
		t.Fatalf("取得: %v %v", acq, err)
	}
	if err := s.ReleaseRunLock(c.ID, "run:b#2#2"); err != nil {
		t.Fatalf("非持有者释放应返回 nil: %v", err)
	}
	row, ok, _ := s.RunLockOf(c.ID)
	if !ok || row.Holder != "run:a#1#1" {
		t.Fatalf("非持有者释放不得动他人行: ok=%v holder=%q", ok, row.Holder)
	}
	if err := s.ReleaseRunLock(c.ID, "run:a#1#1"); err != nil {
		t.Fatalf("持有者释放: %v", err)
	}
	if _, ok, _ := s.RunLockOf(c.ID); ok {
		t.Fatal("释放后行应消失")
	}
	if _, acq, _ := s.AcquireRunLock(c.ID, "n2", "run:c#3#3", time.Minute); !acq {
		t.Fatal("释放后必须立即可被任何人取得，不必等租期")
	}
}

// 断言 22：读面不过滤过期——负 TTL 造过期行（确定性，不依赖时钟推进），
// 存在性 ≠ 在跑，过滤责任在消费侧。
func TestRunLockReadsReturnExpiredRowsAsIs(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	if _, acq, err := s.AcquireRunLock(c.ID, "n", "run:old#1#1", -time.Minute); err != nil || !acq {
		t.Fatalf("负 TTL 造过期行: %v %v", acq, err)
	}
	row, ok, err := s.RunLockOf(c.ID)
	if err != nil || !ok {
		t.Fatalf("过期行仍应原样返回: ok=%v err=%v", ok, err)
	}
	if row.Holder != "run:old#1#1" || !row.ExpiresAt.Before(time.Now()) {
		t.Fatalf("应为未过滤的过期行: %+v", row)
	}
	all, err := s.AllRunLocks()
	if err != nil || len(all) != 1 || all[0].CardID != c.ID {
		t.Fatalf("AllRunLocks 应含过期行: %+v %v", all, err)
	}
}

// 断言 23：card_run_locks 存在且 PK=card_id（SQLite 行为实证；PG 同构由
// store.go :253-256 与 :319-322 两分支 DDL 文本对照承证，真 PG 冒烟门控留给验收专用库）。
func TestCardRunLocksPKIsCardID(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	insert := func(holder string) error {
		return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
			now := time.Now()
			_, err := tx.Exec(s.q(`INSERT INTO card_run_locks
				(card_id, node, holder, acquired_at, expires_at) VALUES (?,?,?,?,?)`),
				c.ID, "n", holder, s.tval(now), s.tval(now.Add(time.Minute)))
			return err
		})
	}
	if err := insert("run:first#1#1"); err != nil {
		t.Fatalf("首插应成功（表存在）: %v", err)
	}
	if err := insert("run:second#2#2"); err == nil {
		t.Fatal("同卡第二行必须撞 PK(card_id)")
	} else if _, ok, _ := s.RunLockOf(c.ID); !ok {
		t.Fatal("撞 PK 后首行不得被动")
	}
}
```

跑红：

```
go test ./internal/ledger/ -run 'TestAcquireRunLock|TestReleaseRunLock|TestRunLockReads|TestCardRunLocksPK'
```

期待：大面积红（方法体直返零值：acquired 恒 false、ErrNotFound 分支缺失）；`TestCardRunLocksPKIsCardID` 可能已绿（DDL 已随 Ticket 0 落地——它锁的是结构事实不是新行为）。逐条贴输出进台账。

### B2 【绿】runlock.go 实现

`internal/ledger/runlock.go`：文件头注释与 `RunLock` 类型保留不动；import 补 `"database/sql"`、`"errors"`、`"fmt"`；在类型定义后加常量，然后整体替换五个方法体：

```go
// 租期与续租间隔常量（位置：拆解稿 §二钉定，协调者 2026-08-25 附带裁决①确认不否决）。
// 数值出处：2026-08-23 B196 在真卡真回合上实测的心跳节奏（间隔 2 分 00 秒，
// 远小于 TTL），spec 实现决定 2——不是新猜的数字。
const (
	RunLockTTL           = 5 * time.Minute
	RunLockRenewInterval = 2 * time.Minute
)

// runLockPreemptReason 抢占事件的人读短句。web 泛化渲染按 reason 优先级直读
// （contract §1.2 末行），它必须是能独立看懂的一句话。
const runLockPreemptReason = "上一轮运行的租期已过，本轮编排接管这张卡的运行锁"

// AcquireRunLock 取得运行锁。无行、持有者是自己、或已过期（一律 s.timeNow() 注入时钟判定）→
// 得到锁；他主持有且未过期 → acquired=false 并原样返回现存行（谁在跑、哪个节点、
// 租期到几点——story 2 报文素材），不算错误。过期即抢占：覆盖四字段并同事务落
// EvDriverTakeover（payload from/to/reason）。首次取得不落事件（拍板 §5.3）。
// 全部判定+写入+事件在同一 mutate 事务内（无 TOCTOU 窗口）。
func (s *Store) AcquireRunLock(cardID, node, holder string, ttl time.Duration) (RunLock, bool, error) {
	var out RunLock
	acquired := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("取得运行锁: 卡 %s: %w", cardID, err)
		}
		now := s.timeNow()
		var row RunLock
		var acquiredAt, expiresAt any
		err := tx.QueryRow(s.q(`SELECT card_id, node, holder, acquired_at, expires_at
			FROM card_run_locks WHERE card_id = ?`), cardID).
			Scan(&row.CardID, &row.Node, &row.Holder, &acquiredAt, &expiresAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			lock := RunLock{CardID: cardID, Node: node, Holder: holder,
				AcquiredAt: now, ExpiresAt: now.Add(ttl)}
			if _, err := tx.Exec(s.q(`INSERT INTO card_run_locks
				(card_id, node, holder, acquired_at, expires_at) VALUES (?,?,?,?,?)`),
				cardID, lock.Node, lock.Holder, s.tval(lock.AcquiredAt), s.tval(lock.ExpiresAt)); err != nil {
				return fmt.Errorf("首建运行锁: %w", err)
			}
			log().Info("运行锁首取", "card", cardID, "node", node, "holder", holder, "ttl", ttl.String())
			out, acquired = lock, true
			return nil
		case err != nil:
			return fmt.Errorf("读运行锁: %w", err)
		}
		row.AcquiredAt, row.ExpiresAt = toTime(acquiredAt), toTime(expiresAt)
		switch {
		case row.Holder == holder:
			// 同一运行重入（同一轮编排内 holder 恒定）：只刷租期；
			// acquired_at 与 node 都不动（契约规则 3 只授权刷 expires_at，不超集扩权）。
			row.ExpiresAt = now.Add(ttl)
			if _, err := tx.Exec(s.q(`UPDATE card_run_locks SET expires_at = ? WHERE card_id = ?`),
				s.tval(row.ExpiresAt), cardID); err != nil {
				return fmt.Errorf("刷新运行锁: %w", err)
			}
			out, acquired = row, true
			return nil
		case row.ExpiresAt.After(now):
			// 他主在租期内：不算错误，不加改动地交出现存行。
			log().Info("运行锁被他方持有", "card", cardID, "holder", row.Holder,
				"expires_at", row.ExpiresAt.Format(time.RFC3339))
			out = row
			return nil
		default:
			// 过期抢占：覆盖四字段；抢占事件与覆盖写同事务，无半截态。
			prev := row.Holder
			row.Node, row.Holder, row.AcquiredAt, row.ExpiresAt = node, holder, now, now.Add(ttl)
			if _, err := tx.Exec(s.q(`UPDATE card_run_locks SET node = ?, holder = ?, acquired_at = ?, expires_at = ? WHERE card_id = ?`),
				node, holder, s.tval(now), s.tval(row.ExpiresAt), cardID); err != nil {
				return fmt.Errorf("抢占运行锁: %w", err)
			}
			if _, err := s.appendEvent(tx, sink, cardID, EvDriverTakeover, holder,
				map[string]string{"from": prev, "to": holder, "reason": runLockPreemptReason}); err != nil {
				return fmt.Errorf("抢占落事件: %w", err)
			}
			log().Info("运行锁过期接管", "card", cardID, "from", prev, "to", holder)
			out, acquired = row, true
			return nil
		}
	})
	if err != nil {
		return RunLock{}, false, err
	}
	return out, acquired, nil
}

// RenewRunLock 续租：只有当前持有者可续（WHERE 同时钉 card_id 与 holder）。
// 返回 false = 已失去（被抢、行消失或从未持有）——「失去写权」的权威信号，
// 调用方必须停止对这张卡的一切写。走 mutate 串行（契约 §1.2：全部账本写单事务）。
func (s *Store) RenewRunLock(cardID, holder string, ttl time.Duration) (bool, error) {
	renewed := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`UPDATE card_run_locks SET expires_at = ?
			WHERE card_id = ? AND holder = ?`), s.tval(s.timeNow().Add(ttl)), cardID, holder)
		if err != nil {
			return fmt.Errorf("续租运行锁: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读续租影响行数: %w", err)
		}
		renewed = n > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return renewed, nil
}

// ReleaseRunLock 释放运行锁（回合结束的尽力而为清理）。非持有者/无行都是
// 成功 nil：清理不该在日志里炸出假警报，失去信号的权威通道在 RenewRunLock。
// 不对称说明见 contract §2.2：归属释放的非持有者是可见失败（人要确认），
// 运行释放的非持有者是 no-op（被抢者的 defer 不该制造第二现场噪声）。
func (s *Store) ReleaseRunLock(cardID, holder string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`DELETE FROM card_run_locks WHERE card_id = ? AND holder = ?`),
			cardID, holder)
		if err != nil {
			return fmt.Errorf("释放运行锁: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log().Info("运行锁已释放", "card", cardID, "holder", holder)
		}
		return nil
	})
}

// RunLockOf 读单卡运行锁行；第二个返回值 = 行是否存在。**不过滤过期**：
// 「是否正在跑」由消费侧按 ExpiresAt 与同一时钟判定（存在性≠在跑）。
// 读面不走 mutate 写事务。
func (s *Store) RunLockOf(cardID string) (RunLock, bool, error) {
	var lock RunLock
	var acquiredAt, expiresAt any
	err := s.db.QueryRow(s.q(`SELECT card_id, node, holder, acquired_at, expires_at
		FROM card_run_locks WHERE card_id = ?`), cardID).
		Scan(&lock.CardID, &lock.Node, &lock.Holder, &acquiredAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RunLock{}, false, nil
	}
	if err != nil {
		return RunLock{}, false, fmt.Errorf("读运行锁 %s: %w", cardID, err)
	}
	lock.AcquiredAt, lock.ExpiresAt = toTime(acquiredAt), toTime(expiresAt)
	return lock, true, nil
}

// AllRunLocks 全部运行锁行（看板批量判定用，岔口一方案 A 的取数落点），
// 按 card_id 序。同样不过滤过期。
func (s *Store) AllRunLocks() ([]RunLock, error) {
	rows, err := s.db.Query(`SELECT card_id, node, holder, acquired_at, expires_at
		FROM card_run_locks ORDER BY card_id`)
	if err != nil {
		return nil, fmt.Errorf("读全部运行锁: %w", err)
	}
	defer rows.Close()
	var out []RunLock
	for rows.Next() {
		var lock RunLock
		var acquiredAt, expiresAt any
		if err := rows.Scan(&lock.CardID, &lock.Node, &lock.Holder, &acquiredAt, &expiresAt); err != nil {
			return nil, err
		}
		lock.AcquiredAt, lock.ExpiresAt = toTime(acquiredAt), toTime(expiresAt)
		out = append(out, lock)
	}
	return out, rows.Err()
}
```

跑绿：

```
go test ./internal/ledger/ -run 'TestAcquireRunLock|TestReleaseRunLock|TestRunLockReads|TestCardRunLocksPK'
go test ./internal/ledger/
```

### B3 提交

`gofmt -l internal/ledger` 空；提交：
`feat(b239): 运行锁五方法落地——TTL 5min/续租 2min、过期抢占落 takeover 事件、payload 金样本（断言 13-23）`

---

## Task C：agentd 装配与徽标（断言 38 + 装配反面断言 + host 档）

**文件集**：`internal/agentd/cardstep.go`、`internal/agentd/ledgerapi.go` ／ `internal/agentd/cardstep_test.go`、`internal/agentd/ledgerapi_test.go`。

### C1 【红】徽标与装配测试先行

`internal/agentd/ledgerapi_test.go` 追加（按缺补 import：`encoding/json`、`net/http`、`time`、`github.com/Xsxdot/handoff/internal/ledger`）：

```go
// linkTaskFailed 给卡挂一个最新实况为 failed 的 task（taskstate_test.go:10 同款夹具路径）。
func linkTaskFailed(t *testing.T, st *ledger.Store, cardID string) {
	t.Helper()
	if err := st.LinkTask(cardID, "mac-02", "T-badge", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: "mac-02", Task: "T-badge", SourceSeq: 1, Type: "failed",
		Payload: []byte(`{}`), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func cardsConflictMap(t *testing.T, env *ledgerEnv) map[string]bool {
	t.Helper()
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("列表应 200: %d %s", code, body)
	}
	var resp struct {
		Cards []struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			Conflict bool   `json:"conflict"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range resp.Cards {
		out[c.ID] = c.Conflict
	}
	return out
}

// 断言 38：徽标 = 存在未过期运行锁 且 最新 task 态 failed；状态列不再参与判定
// （charter 流没有「进行中」，旧判据下徽标恒灭）。三组夹具全走真账本行为；
// 过期行用负 TTL 造（确定性，不依赖时钟推进）。
func TestCardsListConflictFollowsRunLockNotStatus(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "徽标判定卡")
	linkTaskFailed(t, env.ledger, card.ID)
	// 组 1：无运行锁（状态待办）→ conflict=false
	if got := cardsConflictMap(t, env); got[card.ID] {
		t.Fatal("无运行锁时不得亮徽标（旧 StatusDoing 判据已废除）")
	}
	// 组 2：未过期运行锁 + 最新 failed → conflict=true
	if _, acq, err := env.ledger.AcquireRunLock(card.ID, "review", "run:badge#1#1", 5*time.Minute); err != nil || !acq {
		t.Fatalf("造在飞锁: %v %v", acq, err)
	}
	if got := cardsConflictMap(t, env); !got[card.ID] {
		t.Fatal("未过期运行锁 + 最新 task failed 应亮徽标")
	}
	// 组 3：只剩过期运行锁 → false
	if err := env.ledger.ReleaseRunLock(card.ID, "run:badge#1#1"); err != nil {
		t.Fatal(err)
	}
	if _, acq, err := env.ledger.AcquireRunLock(card.ID, "review", "run:badge#2#2", -time.Minute); err != nil || !acq {
		t.Fatalf("造过期锁: %v %v", acq, err)
	}
	if got := cardsConflictMap(t, env); got[card.ID] {
		t.Fatal("仅剩过期运行锁不得亮徽标")
	}
	// 变异对照（收尾 Task E 执行）：把 StatusDoing 判据加回去 → 组 1 必红。
}
```

`internal/agentd/cardstep_test.go` 追加（import 补 `strings`）：

```go
// 装配级反面断言：生产构造点产出的 StepRunner 必须带非空 RunHolder（岔口二方案 A 形态）。
// 为什么必须从 startCardStep 观察：机内 StepRunner 测试都是手工塞 holder，
// 证明不了生产装配没漏传——这正是 breakdown 缺陷族 4「假绿温床」第一条的针对性附加锁。
func TestStartCardStepAssemblesRunHolder(t *testing.T) {
	s := newStepTestServer(t)
	seedCardWithProject(t, s, "demo")
	type captured struct{ holder string }
	ch := make(chan captured, 1)
	s.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		ch <- captured{runner.RunHolder}
	}
	if err := s.startCardStep("B1", proto.CardStepReq{Step: "review", Actor: "web:test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	var got captured
	select {
	case got = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("编排未被启动")
	}
	if got.holder == "" {
		t.Fatal("生产装配不得漏传 RunHolder（否则编排拒绝放行，--step 全灭）")
	}
	if !strings.HasPrefix(got.holder, "run:") || strings.Count(got.holder, "#") != 2 {
		t.Fatalf("holder 应为 run:<host>#<pid>#<unixnano> 形态: %q", got.holder)
	}
	waitFor(t, func() bool { return !cardStepInFlight(s, "B1") })
}

// 契约 §2.1：legacy fallback actor 收敛 host 档——RemoteAddr 带端口会让同一浏览器
// 两次点击拿到两个不同身份，第二次会被自己的旧归属挡住。收敛后形如 web:<host> 无端口。
func TestLegacyStepFallbackActorIsHostOnly(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "fallback host 卡")
	ch := make(chan string, 1)
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		ch <- runner.Session
	}
	// 故意不带 actor 字段：走 legacy fallback（handleCardStep 的 raw 键判缺分支）
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"review"}`)
	if code != http.StatusAccepted {
		t.Fatalf("legacy 请求应 202 受理: %d %s", code, body)
	}
	var session string
	select {
	case session = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("编排未被启动")
	}
	if session != "web:127.0.0.1" {
		t.Fatalf("fallback actor 应为 host 档 web:127.0.0.1（httptest 客户端恒来自环回），实得 %q", session)
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(card.ID) })
}
```

跑红（期待三处红：徽标仍判 StatusDoing→组 2 红；holder 空→装配红；fallback 带端口→host 红）：

```
go test ./internal/agentd/ -run 'TestCardsListConflictFollowsRunLockNotStatus|TestStartCardStepAssemblesRunHolder|TestLegacyStepFallbackActorIsHostOnly'
```

### C2 【绿】生产实现

`internal/agentd/cardstep.go`：`startCardStep` 的 runner 构造（:45-55）替换为：

```go
	host, _ := os.Hostname()
	runner := &ledgerstep.StepRunner{
		St: s.ledger, Session: req.Actor,
		// 运行标识（岔口二方案 A，协调者 2026-08-25 拍板）：每次 startCardStep
		// 现算一次、整轮恒定；人读第一眼看出机器与进程，取证不必再翻日志。
		RunHolder: fmt.Sprintf("run:%s#%d#%d", host, os.Getpid(), time.Now().UnixNano()),
		Dispatcher: &ledgerstep.Dispatcher{
			St: s.ledger, Transport: s.stepTransport, Actor: req.Actor,
		},
		Clients:  s.pool.For,
		Target:   req.Target,
		Executor: req.Executor,
		Model:    req.Model,
		Extra:    req.Extra,
	}
```

（import 补 `"os"`、`"time"`。）装配日志（:56-58）追加一个字段：`"run_holder", runner.RunHolder`。

`internal/agentd/ledgerapi.go` 徽标块：`handleCardsList` 中 `out := make(...)` 前插入批量取数，循环体替换（岔口一方案 A：AllRunLocks() 一次拉全量 + 内存 join；账本在远端 PG，逐卡查询是 N 次跨网往返）：

```go
	// 运行锁批量读取（岔口一方案 A）：一次拉全量建 map，列表页 O(1) 查询次数。
	// 读失败退化 false + 告警，不阻塞列表（对齐上方工单推导失败的既有形态）。
	runLocks, lockErr := s.ledger.AllRunLocks()
	if lockErr != nil {
		s.log.Warn("运行锁批量读取失败（冲突徽标退化为不显示，不阻塞列表）", "err", lockErr)
		runLocks = nil
	}
	activeLock := make(map[string]struct{}, len(runLocks))
	now := time.Now()
	for _, l := range runLocks {
		if l.ExpiresAt.After(now) { // 存在性≠在跑：过期过滤是消费侧责任
			activeLock[l.CardID] = struct{}{}
		}
	}
	out := make([]proto.CardView, 0, len(views))
	for _, view := range views {
		conflict := false
		// 新判据（story 9 / 断言 38）：有未过期运行锁 且 最新 task 态 failed。
		// 状态列从此与徽标无关——charter 流根本没有「进行中」这一列。
		if _, locked := activeLock[view.ID]; locked {
			states, stateErr := s.ledger.LatestTaskStates(view.ID)
			if stateErr != nil {
				s.log.Warn("task 实况推导失败（该卡徽标退化为不显示）", "card", view.ID, "err", stateErr)
			}
			for _, state := range states {
				if state.LastType == "failed" {
					conflict = true
					break
				}
			}
		}
		out = append(out, ledgerCardViewWire(view, conflict, tickets[view.ID]))
	}
```

同文件 ：446 fallback 行 `req.Actor = "web:" + r.RemoteAddr` 替换为：

```go
		req.Actor = "web:" + hostOnly(r.RemoteAddr)
```

并在文件底部加 helper（import 补 `"net"`）：

```go
// hostOnly 从 RemoteAddr 剥离端口。fallback actor 收敛 host 档（契约 §2.1）：
// 带端口的地址会让同一浏览器两次点击拿到两个「身份」，第二次会被自己的
// 旧归属挡住。解析失败原样返回（无端口形态本就合法）。
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
```

注意：其余六处 `"web:"+r.RemoteAddr`（:89/:367/:386/:522/:542/:693）是事件审计署名，**一字不动**（契约修订记录 §8 澄清二冻结范围）。

跑绿：

```
go test ./internal/agentd/ -run 'TestCardsList|TestStartCardStep|TestLegacyStepFallback|TestRequiresInlineLocalFile'
go test ./internal/agentd/
```

### C3 提交

`gofmt -l internal/agentd` 空；提交：
`feat(b239): agentd 装配 RunHolder + conflict 徽标改判运行锁 + fallback actor 收敛 host 档（断言 38）`

---

## Task D：编排缝改造（断言 24–32）

**文件集**：`internal/ledgerstep/runner.go`、`internal/ledgerstep/node.go`（纯增量：WriteGate 字段+守卫，见偏差 2）／ `internal/ledgerstep/runner_test.go`。

### D1 【红】编排入口行为测试先行

`internal/ledgerstep/runner_test.go`：

1. `dispatchRunner` harness（:13-19）补两个注入（后续所有新测试依赖；既有用例随之获得合法 holder 与手动节拍通道，零真实等待）：

```go
func dispatchRunner(t *testing.T, st *ledger.Store, transport func(context.Context, DispatchOpts) (string, error)) *StepRunner {
	t.Helper()
	return &StepRunner{
		St: st, Session: "session-runner", Target: "mac-02",
		RunHolder: "run:test-host#4242#1",
		RenewBeat: make(chan time.Time, 8),
		Dispatcher: &Dispatcher{St: st, Actor: "runner-actor", Transport: transport},
	}
}
```

2. 重写 `TestRunnerClaimsDriverWithoutChangingNodeStatusAndReleasesAfterRun`（:202 起）为断言 28 形态；重写 `TestRunnerReleasesDriverAfterDispatchFailure`（:265 起）为断言 29+32 形态；把 `TestRunnerRejectsActiveDriverAndReportsHolder`（:242 起）扩成断言 27 完整形；新增 24/25/26/30/31 五支。完整代码：

```go
// assertHaltOnCard 断言卡上出现 needs_human 事件 + 含 wantSub 的评论
// （「card wait 看得见」判据的机内形：card_wait 只 Follow 卡的事件流）。
func assertHaltOnCard(t *testing.T, st *ledger.Store, cardID, wantSub string) {
	t.Helper()
	events, err := st.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	comment, flagged := false, false
	for _, e := range events {
		switch e.Type {
		case ledger.EvComment:
			var p struct {
				Body string `json:"body"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && strings.Contains(p.Body, wantSub) {
				comment = true
			}
		case ledger.EvNeedsHuman:
			flagged = true
		}
	}
	if !comment {
		t.Fatalf("卡上应有含 %q 的评论事件: %+v", wantSub, events)
	}
	if !flagged {
		t.Fatal("卡上应被打等人标记")
	}
}

// 断言 24：节点解不开 → Run 返回错误 且 卡上先落 needs_human + 含原因原文的评论。
func TestRunnerUnknownNodeHaltsWithCardEvent(t *testing.T) {
	st, card := nodeLedger(t)
	runner := &StepRunner{St: st}
	_, err := runner.Run(context.Background(), card.ID, "查无此节点")
	if err == nil || !strings.Contains(err.Error(), "查无此节点") {
		t.Fatalf("节点解不开应报错并带节点名: %v", err)
	}
	assertHaltOnCard(t, st, card.ID, "查无此节点")
}

// 断言 25：Session 未设置 → 同样先落卡再返回错误。
func TestRunnerMissingSessionHaltsWithCardEvent(t *testing.T) {
	st, card := nodeLedger(t)
	runner := &StepRunner{St: st, Session: "", RunHolder: "run:x#1#1",
		Dispatcher: &Dispatcher{St: st, Actor: ""}}
	_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
	if err == nil {
		t.Fatal("会话未设置应报错")
	}
	assertHaltOnCard(t, st, card.ID, "会话未设置")
}

// 断言 26：运行锁被拒 → Run 返回错误且卡上落 needs_human + 评论，
// 评论与错误都点名 谁在跑 / 哪个节点 / 租期到几点（story 2 的主场景，
// B239 伤害面另一半的消灭点）。归属不得被认领。
func TestRunnerRunLockRefusalReportsWhoNodeExpiry(t *testing.T) {
	st, card := nodeLedger(t)
	if _, acq, err := st.AcquireRunLock(card.ID, ledger.StatusDoing,
		"run:other-host#7#7", ledger.RunLockTTL); err != nil || !acq {
		t.Fatalf("预占运行锁: %v %v", acq, err)
	}
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, error) {
		t.Fatal("运行锁被拒时不得派发")
		return "", nil
	})
	_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
	if err == nil || !strings.Contains(err.Error(), "run:other-host#7#7") ||
		!strings.Contains(err.Error(), ledger.StatusDoing) {
		t.Fatalf("报错应点名持有者与节点: %v", err)
	}
	assertHaltOnCard(t, st, card.ID, "run:other-host#7#7")
	got, _ := st.GetCard(card.ID)
	if got.DriverSession != "" {
		t.Fatalf("运行锁被拒时不得认领归属: %q", got.DriverSession)
	}
}

// 断言 27：归属被拒（他主）→ 先落卡再返回错误（契约 §2.3 步骤 4：
// 编排入口在 RunOnce 保护之外的失败必须与内部同形落卡）。
func TestRunnerOwnershipRefusalHaltsWithCardEvent(t *testing.T) {
	st, card := nodeLedger(t)
	if err := st.ClaimCard(card.ID, "cli:holder@h"); err != nil {
		t.Fatalf("预占归属: %v", err)
	}
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, error) {
		t.Fatal("归属被拒时不得派发")
		return "", nil
	})
	_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
	if err == nil || !strings.Contains(err.Error(), "cli:holder@h") {
		t.Fatalf("报错应点名持有者: %v", err)
	}
	assertHaltOnCard(t, st, card.ID, "cli:holder@h")
	stillHeld, _ := st.GetCard(card.ID)
	if stillHeld.DriverSession != "cli:holder@h" {
		t.Fatalf("冲突方不得改写归属: %q", stillHeld.DriverSession)
	}
}

// 断言 28：回合正常结束 → 运行锁行已消失（不必等租期），driver_session 保持
// 本轮 owner（归属持久化，不再随回合消亡——拍板 §5.2）。运行中锁行存在。
func TestRunnerKeepsOwnershipAndReleasesRunLockAfterRun(t *testing.T) {
	st, card := nodeLedger(t)
	started, finish := make(chan struct{}), make(chan struct{})
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, error) {
		close(started)
		<-finish
		return "T-driver", nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
		done <- err
	}()
	<-started
	claimed, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.DriverSession != runner.Session {
		t.Fatalf("运行中应记录归属 %q，实际 %q", runner.Session, claimed.DriverSession)
	}
	if claimed.Status != ledger.StatusTodo {
		t.Fatalf("编排不得挪状态列，实际 %q", claimed.Status)
	}
	lockRow, ok, err := st.RunLockOf(card.ID)
	if err != nil || !ok {
		t.Fatalf("运行中应有锁行: ok=%v err=%v", ok, err)
	}
	if lockRow.Holder != runner.RunHolder {
		t.Fatalf("锁行 holder 应为 RunHolder: %+v vs %q", lockRow, runner.RunHolder)
	}

	close(finish)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ok, _ := st.RunLockOf(card.ID); ok {
		t.Fatal("回合结束运行锁行应消失（defer ReleaseRunLock）")
	}
	released, _ := st.GetCard(card.ID)
	if released.DriverSession != runner.Session || released.DriverHeartbeatAt.IsZero() {
		t.Fatalf("归属应保持为本轮 owner 不随回合消亡: %+v", released)
	}
}

// 断言 29+32：失败路径结束 → 运行锁行同样消失；归属保持（终态断言表达
// 「编排全程不调 ReleaseCard」，不做 mock 计数——换实现不改需求不会无意义地红）。
func TestRunnerKeepsOwnershipAfterDispatchFailure(t *testing.T) {
	st, card := nodeLedger(t)
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, error) {
		return "", fmt.Errorf("目标机不可达")
	})
	outcome, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
	if err != nil {
		t.Fatalf("派发失败应转等人而不是 Run 错误: %v", err)
	}
	if outcome.Action != ActionNeedsHuman {
		t.Fatalf("派发失败应转等人: %+v", outcome)
	}
	if _, ok, _ := st.RunLockOf(card.ID); ok {
		t.Fatal("失败路径结束运行锁行也应消失")
	}
	got, _ := st.GetCard(card.ID)
	if got.DriverSession != runner.Session {
		t.Fatalf("失败路径归属同样保持: %q", got.DriverSession)
	}
}

// 断言 30：长回合期间节拍触发续租。判据钉在**库行 expires_at 推进**上
// （RunLockOf 前后读比较），不许落在 runner 内存字段（防假绿）。
// 零真实等待：节拍由测试手动注入。
func TestRunRenewsLockRowOnBeat(t *testing.T) {
	st, card := nodeLedger(t)
	started, release := make(chan struct{}), make(chan struct{})
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, error) {
		close(started)
		<-release
		return "T-beat", nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
		done <- err
	}()
	<-started
	before, ok, err := st.RunLockOf(card.ID)
	if err != nil || !ok {
		t.Fatalf("回合中应有锁行: ok=%v err=%v", ok, err)
	}
	runner.RenewBeat <- time.Time{}
	after, _, _ := st.RunLockOf(card.ID)
	if !after.ExpiresAt.After(before.ExpiresAt) {
		t.Fatalf("节拍后库行 expires_at 必须推进: before=%v after=%v",
			before.ExpiresAt, after.ExpiresAt)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// 断言 31：失去写权后，移列/裁决落账/挂附件/打撤等人标记全部不再发生；
// 说明性 comment 恰一条（按 body 标记计数——ViaTemplate 的挂账/快照评论不在
// 冻结禁写清单内、不受闸约束，见 D2 边界申报，所以不能按 EvComment 总数断言）。
// 确定性来自**写时判定**：受守写点各自当场续租一次，false 即拒——不依赖节拍
// 是否被处理，无竞态窗口。场景：Verdict 节点在取报文阶段失败转 haltForHuman
// （第一个受守写点），此时闸已因锁行被删而关闭 → 拒写并上抛 ErrWriteGateClosed。
func TestRunnerStopsCardWritesAfterLosingWriteGate(t *testing.T) {
	st, _ := nodeLedger(t)
	// bug 流的「进行中」节点没有 Verdict 能力，走不到任何受守写点；
	// 本测试当场写一条带 Verdict 的最小工作流并建钉住它的卡。
	if _, err := st.PutWorkflow("gateflow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "审阅"},
		{Name: "审阅", Dispatch: true, Verdict: true, Template: "feature-impl",
			Next: ledger.StatusDone, OnFail: ledger.StatusTodo},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "写闸卡", Project: "p", Workflow: "gateflow", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	runner := &StepRunner{
		St: st, Session: "session-runner", Target: "mac-02",
		RunHolder: "run:loser#9#9",
		RenewBeat: make(chan time.Time, 8),
		Dispatcher: &Dispatcher{St: st, Actor: "runner-actor",
			Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
				close(started)
				<-release // 派发已受理、回合仍在跑；返回的是 task id（不是报文）
				return "T-gate", nil
			}},
	}
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), card.ID, "审阅")
		done <- err
	}()
	<-started
	// 模拟被抢：删掉本运行的锁行 → RenewRunLock 从此恒 false（权威信号成立）
	holderRow, ok, err := st.RunLockOf(card.ID)
	if err != nil || !ok {
		t.Fatalf("读锁行: ok=%v err=%v", ok, err)
	}
	if err := st.ReleaseRunLock(card.ID, holderRow.Holder); err != nil {
		t.Fatal(err)
	}
	close(release)
	runErr := <-done
	// 取报文失败转 haltForHuman → 写闸拒绝（comment+needs_human 都不得落），
	// 错误链可 errors.Is 到哨兵。
	if !errors.Is(runErr, ErrWriteGateClosed) {
		t.Fatalf("失去写权后首个卡写应被闸拒绝: %v", runErr)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	explanatory := 0
	for _, e := range events {
		switch e.Type {
		case ledger.EvComment:
			var p struct {
				Body string `json:"body"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && strings.Contains(p.Body, "本轮运行锁已被接手") {
				explanatory++
			}
		case ledger.EvNeedsHuman, ledger.EvNeedsCleared:
			t.Fatalf("失去写权后不得再打/撤等人标记: %+v", e)
		case ledger.EvReviewVerdict:
			t.Fatalf("失去写权后不得落裁决: %+v", e)
		case ledger.EvStatusMoved:
			t.Fatalf("失去写权后不得移列: %+v", e)
		}
	}
	if explanatory != 1 {
		t.Fatalf("说明性 comment 应恰一条，实得 %d 条: %+v", explanatory, events)
	}
	final, _ := st.GetCard(card.ID)
	if final.Status != ledger.StatusTodo {
		t.Fatalf("不得移列: %q", final.Status)
	}
}
```

（import 补 `encoding/json` 与 `errors`。）跑红：

```
go test ./internal/ledgerstep/ -run 'TestRunner'
```

期待红（现流程无落卡、无运行锁、无续租、无写闸），逐条贴输出进台账。

### D2 【绿】node.go 写闸 + runner.go 编排重写

`internal/ledgerstep/node.go` 纯增量改动（四处）：import 补 `"errors"`；`ErrWriteGateClosed` 哨兵与 `WriteGate` 字段、守卫 helper：

```go
// ErrWriteGateClosed 写闸关闭哨兵：本轮运行锁已失去（续租返回 false），
// 本回合对这张卡的其余写动作一律拒绝。可 errors.Is 识别。
var ErrWriteGateClosed = errors.New("运行锁已失去写权")

type NodeStep struct {
	// …既有字段原样保留…
	// WriteGate 卡写闸（可选）。非 nil 时，RunOnce 内一切会写卡的路径先过闸：
	// 返回 false 即拒绝该次写（错误 wrap ErrWriteGateClosed 上抛）。
	// 生产由 StepRunner 注入「当场续租一次」的权威判定；nil = 不设闸
	// （直接构造 NodeStep 的既有调用方与测试零改动）。
	WriteGate func() bool
}

// gatedWrite 过闸：闸未设或放行 → nil；关闭 → 带 action 说明的包装错误。
// 结构性收口而非每写点手写 if：未来新增写点照抄一行守卫即可进同一道门
// （breakdown 缺陷族 5 缓解项）。
func (n *NodeStep) gatedWrite(action string) error {
	if n.WriteGate == nil || n.WriteGate() {
		return nil
	}
	return fmt.Errorf("%s被拒：%w", action, ErrWriteGateClosed)
}
```

五处守卫（每处一行，位置如下）：

1. `haltForHuman` 开头：

```go
func (n *NodeStep) haltForHuman(cardID, reason, body string) (Outcome, error) {
	if err := n.gatedWrite("等人留痕"); err != nil {
		// 写闸关闭时不留痕不打旗，但 Outcome 仍如实报告 needs_human——
		// 回合继续走到自然结束（远端任务不强杀），只是卡上不再多写。
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, err
	}
	…原有函数体不动…
```

2. `routeTo` 的 MoveCard 前：

```go
	if err := n.gatedWrite("移列"); err != nil {
		return err
	}
	return n.St.MoveCard(cardID, to, card.Status, n.actor())
```

3. `RunOnce` 中既有行 `if err := n.St.RecordReviewVerdict(cardID, n.Node.Name, verdict.Pass, verdict.Raw, n.actor()); err != nil {`（node.go:180）**之前插入**：

```go
	if err := n.gatedWrite("裁决落账"); err != nil {
		return Outcome{}, err
	}
```

4. `ClearNeedsHumanFrom` 调用前：

```go
	if err := n.gatedWrite("撤等人标记"); err != nil {
		return Outcome{}, err
	}
	if cleared, cerr := n.St.ClearNeedsHumanFrom(cardID, n.actor()); cerr != nil {
```

5. `RunOnce` 中既有行 `if attachErr := n.Attach(cardID, output.Kind, declaredPath, n.actor()); attachErr != nil {`（node.go:228）**之前插入**：

```go
	if err := n.gatedWrite("挂附件"); err != nil {
		return Outcome{}, err
	}
```

（第 4、5 处在正常控制流里位于第 3 处之后，闸关闭时实际不可达；仍加守卫是结构性完整性——四禁写族各自独立过闸，未来重排控制流不会漏出静默面。）

`internal/ledgerstep/runner.go`：`StepRunner` 结构体追加字段（放在 RunHolder 之后）：

```go
	// RenewBeat 续租节拍源（岔口三拍板：注入的是节拍本身，不是间隔数字）。
	// 生产 nil → 内部按 RunLockRenewInterval 起 time.Ticker；测试注入手动
	// channel，每收到一个信号触发一次 RenewRunLock。零真实等待可测。
	RenewBeat <-chan time.Time
```

`Run` 方法整体替换为：

```go
// Run 跑一次节点。
//
// 参数：cardID 卡；nodeName 节点名（= 看板的列名），从卡钉住的工作流版本里查。
// 返回：Outcome；入口失败（节点解不开/会话未设置/运行标识缺失/两把锁任一被拒）
// 一律**先落卡**（needs_human + 含原因原文的评论，与 RunOnce 内部 haltForHuman 同形）
// 再返回错误——card wait 只看卡的事件流，不落卡的失败对协调者不存在。
//
// 锁的生命周期（b239-contract.md §2.3 法定顺序）：nodeFor → 会话检查 → 运行锁
// 取得（拒绝即落卡返回）→ ClaimCard 认领持久归属 → 续租循环（节拍驱动）→
// defer ReleaseRunLock。归属不再随回合消亡（旧 defer ReleaseCard 已删）。
//
// 阻塞行为：Node.Verdict 时阻塞到 task 回合终态（分钟级）。续租循环随 ctx
// 取消或 Run 返回而停，不留泄漏 goroutine。
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error) {
	logger := slog.Default().With("card", cardID, "node", nodeName, "run_holder", r.RunHolder)
	logger.Info("进入节点执行")
	node, err := r.nodeFor(cardID, nodeName)
	if err != nil {
		logger.Warn("读取节点失败", "cause", err)
		return r.haltEntrypoint(cardID, nodeName, "节点解不开",
			fmt.Sprintf("本节点无法从卡钉住的工作流里解开：%s", err.Error()))
	}
	outputPath := ""
	nodeStep := &NodeStep{
		St:         r.St,
		Node:       node,
		Dispatch:   r.dispatchNode(&outputPath),
		Await:      r.awaitNode(),
		OutputPath: func() string { return outputPath },
		Diff:       r.diffNode(),
		Attach: func(cardID, kind, path, actor string) error {
			return r.St.AttachFile(cardID, kind, path, actor)
		},
	}
	if !node.Dispatch {
		// 纯人工列没有执行能力，不应因为被误点而留下驱动归属。
		// 维持现状：不取运行锁、不认领归属（契约 §2.3 明文）。
		logger.Info("纯人工节点跳过锁与认领")
		return nodeStep.RunOnce(ctx, cardID)
	}

	session := r.Session
	if session == "" && r.Dispatcher != nil {
		// 保持直接构造 StepRunner 的旧调用方可用；生产装配会显式传入会话，
		// 这里仅作为测试和内部调用的兼容兜底。
		session = r.Dispatcher.Actor
	}
	if session == "" {
		err := fmt.Errorf("节点归属会话未设置")
		logger.Error("节点执行被拒", "cause", err)
		return r.haltEntrypoint(cardID, nodeName, "会话未设置",
			"本节点执行被拒：发起方归属会话未设置。\n"+err.Error())
	}
	if r.RunHolder == "" {
		// 契约 §2.3：空值必须拒绝放行而不是静默退化。
		err := fmt.Errorf("运行标识未装配（RunHolder 为空）")
		logger.Error("运行锁路径拒绝放行", "cause", err)
		return r.haltEntrypoint(cardID, nodeName, "运行标识缺失",
			"本节点执行被拒："+err.Error())
	}

	lock, acquired, acqErr := r.St.AcquireRunLock(cardID, nodeName, r.RunHolder, ledger.RunLockTTL)
	if acqErr != nil {
		logger.Error("取得运行锁失败", "cause", acqErr)
		return Outcome{}, fmt.Errorf("取得运行锁: %w", acqErr)
	}
	if !acquired {
		// acquired=false 不是库层错误（第二返回值必须被消费的唯一执法点）：
		// 编排必须显式分支转落卡，否则回到 B239 的静默面。
		detail := fmt.Sprintf("卡正由 %s 运行节点 %s，租期到 %s",
			lock.Holder, lock.Node, lock.ExpiresAt.Format(time.RFC3339))
		logger.Warn("运行锁被拒", "lock_holder", lock.Holder,
			"lock_node", lock.Node, "expires_at", lock.ExpiresAt.Format(time.RFC3339))
		o, herr := r.haltEntrypoint(cardID, nodeName, "运行锁被他方占用",
			"本节点无法开跑："+detail+"。\n原因原文：AcquireRunLock 返回 acquired=false")
		if herr != nil {
			return Outcome{}, herr
		}
		return o, fmt.Errorf("运行锁被拒：%s", detail)
	}
	logger.Info("运行锁已取得", "expires_at", lock.ExpiresAt.Format(time.RFC3339))

	if err := r.St.ClaimCard(cardID, session); err != nil {
		logger.Warn("归属认领被拒", "session", session, "cause", err)
		o, herr := r.haltEntrypoint(cardID, nodeName, "归属认领被拒",
			fmt.Sprintf("以 %s 认领这张卡被拒：\n%s", session, err.Error()))
		if herr != nil {
			return Outcome{}, herr
		}
		return o, fmt.Errorf("认领归属: %w", err)
	}
	logger.Info("归属已认领", "session", session)

	// 续租循环：节拍驱动；失去写权时落一次性说明 comment 后退出循环。
	// 失去信号同时驱动 NodeStep 写闸（写时当场再判，见 gate），两者共用 noteLost 保证恰一条。
	done := make(chan struct{})
	finished := make(chan struct{})
	var lostOnce sync.Once
	noteLost := func() {
		lostOnce.Do(func() {
			body := fmt.Sprintf("本轮运行锁已被接手（holder=%s）：本回合自即刻起停止对这张卡的"+
				"移列、裁决、附件与等人标记写入；已在跑的远端任务继续等待并照常归档。", r.RunHolder)
			if _, cerr := r.St.AddComment(cardID, body, "普通", "node:"+nodeName); cerr != nil {
				logger.Warn("失去写权说明落卡失败", "cause", cerr)
			} else {
				logger.Info("失去写权说明已落卡")
			}
		})
	}
	beats := r.RenewBeat
	if beats == nil {
		ticker := time.NewTicker(ledger.RunLockRenewInterval)
		defer ticker.Stop()
		beats = ticker.C
	}
	go func() {
		defer close(finished)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-beats:
				ok, err := r.St.RenewRunLock(cardID, r.RunHolder, ledger.RunLockTTL)
				switch {
				case err != nil:
					logger.Warn("续租出错（下一节拍重试）", "cause", err)
				case ok:
					logger.Info("运行锁已续租", "ttl", ledger.RunLockTTL.String())
				default:
					noteLost()
					logger.Warn("续租被拒：本轮已失去对卡的写权", "holder", r.RunHolder)
					return
				}
			}
		}
	}()
	defer func() {
		if err := r.St.ReleaseRunLock(cardID, r.RunHolder); err != nil {
			logger.Warn("释放运行锁失败", "cause", err)
			return
		}
		logger.Info("运行锁已释放", "holder", r.RunHolder)
	}()
	defer func() { <-finished }() // 等循环退出再释放：末次续租与删除行不留竞态
	defer close(done)

	// 写闸：受守的卡写在动作前当场续租一次（false=已失去写权）。
	// 与节拍循环双保险：节拍负责保活与发现，写闸负责写前的权威判定。
	gate := func() bool {
		ok, err := r.St.RenewRunLock(cardID, r.RunHolder, ledger.RunLockTTL)
		if err != nil {
			logger.Warn("写闸续租判定出错（按失权处理，fail-closed）", "cause", err)
			return false
		}
		if !ok {
			noteLost()
		}
		return ok
	}
	nodeStep.WriteGate = gate
	return nodeStep.RunOnce(ctx, cardID)
}

// haltEntrypoint 编排入口失败的落卡三件套（AddComment + MarkNeedsHuman），
// 与 RunOnce 内部 haltForHuman 同形；署名统一 node:<请求的节点名>。
// 落卡动作自身失败 → 错误原样上抛（agentd 日志是第二现场），不吞、不再补写。
// 仅当落卡成功才返回 (needs_human Outcome, nil)；调用方据此决定是否再包原始原因。
func (r *StepRunner) haltEntrypoint(cardID, nodeName, reason, body string) (Outcome, error) {
	logger := slog.Default().With("card", cardID, "node", nodeName)
	if _, err := r.St.AddComment(cardID, body, "普通", "node:"+nodeName); err != nil {
		logger.Error("入口失败落卡：写评论失败", "reason", reason, "cause", err)
		return Outcome{}, fmt.Errorf("入口失败落卡（原始原因：%s）：%w", body, err)
	}
	if err := r.St.MarkNeedsHuman(cardID, reason, "node:"+nodeName); err != nil {
		logger.Error("入口失败落卡：打等人标记失败", "reason", reason, "cause", err)
		return Outcome{}, fmt.Errorf("入口失败打等人标记（原始原因：%s）：%w", body, err)
	}
	return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
}
```

**D2 边界申报（代审发现的两处契约字面差异，均不越冻结面）**：
① Session 兜底沿用现状形状（`Session=="" && Dispatcher!=nil` 时取 `Dispatcher.Actor`）——契约 §2.3 引用的正是这段现状代码（runner.go:86-96）；断言 25 以两者皆空触发拒绝路径，兜底分支本身不被新断言钉死。
② `Dispatcher.ViaTemplate` 内部的挂账评论（LinkTask→EvComment）与派发快照（RecordDispatch→EvDispatched）**不加闸**：契约 §2.3 步骤 5 的禁写清单是「移列、落裁决、挂附件、打撤等人标记」四族，挂账/快照不在其内；且生产时序上首次闸判定发生在派发受理之后。因此断言 31 的「恰一条说明 comment」按 body 标记（「本轮运行锁已被接手」）计数而非 EvComment 总数。

（import 变更：runner.go 补 `"sync"`；`"time"` 已有。）

跑绿＋并发卫生：

```
go test ./internal/ledgerstep/
go test -race ./internal/ledgerstep/
go vet ./internal/ledgerstep
```

### D3 提交

`gofmt -l internal/ledgerstep` 空；提交：
`feat(b239): 编排缝——入口失败落卡三件套、运行锁取用/续租/释放、写权 gate、defer 替换（断言 24-32）`

---

## Task E：收尾核验（不出子卡，全轮最后一站）

1. 全量门：
```
go build ./...
go vet ./internal/ledger ./internal/ledgerstep ./cmd ./internal/agentd
gofmt -l internal/ledger internal/ledgerstep internal/agentd cmd
go test ./internal/ledger/... ./internal/ledgerstep/... ./internal/agentd/... ./cmd/...
```
期待：build/vet exit 0、gofmt 空、四包全绿。原文贴台账。

2. **变异复验五点**（每点：改代码 → 跑指名测试 → 期待红 → 还原 → 复绿；五组输出原文贴台账）：
   - 把 ReleaseCard 他主分支改回「return nil」→ `TestReleaseCardOwnershipSemantics` 红（契约变异点①）；
   - 给 ClaimCard 加任何过期判据（如 heartbeat 超 TTL 即拒）→ `TestClaimCardOwnershipSemantics` 断言 3+4 段红（契约变异点②）;
   - 让 ClaimCard 顺带 `moveCardTx(id, StatusDoing,…)` → `TestClaimCardOwnershipSemantics` 断言 6 段红 + `TestCardDispatchClaimAndSnapshot` 红（契约变异点③）；
   - 删 AcquireRunLock 过期抢占分支（他主持有一律拒）→ `TestAcquireRunLockPreemptsExpiredWithGoldPayload` 红（breakdown 追加点）；
   - RenewRunLock 去掉 `AND holder = ?` → `TestAcquireRunLockBasics` 断言 19 段红（breakdown 追加点）。

3. 断言 38 变异对照：徽标块临时加回 `view.Status == ledger.StatusDoing &&` → `TestCardsListConflictFollowsRunLockNotStatus` 组 1 红 → 还原复绿。输出贴台账。

4. 断言 39 口径复核（A6 已做，此处复跑确认收尾态）：grep `ledgerSession` 生产零命中。

5. 台账补齐：39 条断言 → 测试符号映射表一行一条；真机清单七条标注「归协调者」。最终提交：
`chore(b239): 收尾核验——全量门绿 + 变异五点变红记录 + 台账`

---

## 五项检查（plan 出稿自审记录）

**1. 缺陷族对抗审查（对照项目清单，只记 plan 层新增机制的答案；子系统层答案见 breakdown §四）**

| 族 | 本计划机制层的回答 |
|---|---|
| 生命周期/状态机中断 | 续租 goroutine 退出三通道（ctx/done/失权）；defer 顺序 close(done)→wait finished→release，末次续租与删行无竞态；ticker 经 defer Stop，无泄漏 timer |
| 静默失败/误导报错 | AcquireRunLock false 分支显式转落卡（断言 26 是唯一执法点）；写闸 DB 出错 fail-closed 并告警；noteLost 落卡自身失败仅告警（说明尽力而为，不无限补写） |
| 跨平台假设 | 无新增路径/信号/webview 假设；时间全部经 store 抽象或注入时钟；hostname 只影响人读取证不影响判定 |
| 假红/假绿 | 断言 30 判据=库行推进（非内存字段）；断言 31 用写时判定消除竞态窗口；装配反面断言堵「测试塞 holder 生产漏传」；负 TTL 造过期行替代真实等待；断言 28/29 终态断言替代 mock 计数 |
| 门禁绕过 | 所有锁写路径仍走 mutate 单事务串行；TakeoverCard 是有意保留的可审计旁路（8-23 裁决钦点）；CLI 守卫只是美化，真门在库层 |

残余风险（诚实记账）：gate 通过与实际写之间理论上有微秒级 TOCTOU 窗口（跨两次 Store 调用无法原子）；真实抢占要恰好落在该窗内且需先等满 5 分钟租期，概率可忽略，spec 接受该级别（「续租失败即停止写」以检查点语义执行）。已在此声明，不假装有完美锁。

**2. 序列化边界设问**：手写投影清单=① runlock.go 两处内联 Scan（列序 card_id,node,holder,acquired_at,expires_at 与 SELECT 逐字对应，B1 测试逐字段断言）；② EvDriverTakeover payload map→JSON→web 泛化直读——**穿过真实序列化边界的回归=B1 金样本测试**（三键恰一+值精确，缺键/多键即红）；③ U2 评论 fmt.Sprintf 人读文本——断言 26/31 以子串断言覆盖。RunLock 结构不出进程（Go 直调，契约 §6），无 TS 孪生。roundtrip 属性测试收益低（唯一 wire 面已被金样本逐字节钉住，breakdown §四同结论）。

**3. 上下文预算**：Task A 10 文件 / B 2 文件 / C 4 文件 / D 3 文件 / E 0 文件，均圈得出有界集合。

**4. 类型标注**：边界型子系统任务=C（d_gateway boundary）。其行为验收为显式真机清单条目：机内闭环部分=断言 38 三组夹具+装配反面断言+host 档断言；真正外部现实（浏览器渲染、payload.reason 直读显示）=breakdown 真机清单 #4，归协调者。

**5. 接缝覆盖（双向）**：

- 测试→缝：A 的 ledger/cmd 测试入口=`Store.ClaimCard/ReleaseCard/TakeoverCard`（缝 1 符号）✓；B 入口=五个 Store 方法（缝 1 新符号）✓；C 徽标测试入口=HTTP `/api/cards`→handleCardsList→`AllRunLocks`（调用链穿过缝 1）✓；D 全部测试入口=`StepRunner.Run`（缝 2 符号）✓。
- 缝→测试：缝 1 被 A1/B1 至少各一支缝级断言锁住 ✓；缝 2 被 D1 九支测试锁住 ✓。
- **内部锁申报**（附加、不顶替）：C 的 `TestStartCardStepAssemblesRunHolder` 与 `TestLegacyStepFallbackActorIsHostOnly` 入口是 startCardStep/handleCardStep，不在两条声明缝上。理由（唯一合法形状）：从声明缝**构造不出**这两条断言——StepRunner.Run 的机内测试手工构造 runner（holder/session 由测试填），逻辑上永远证明不了生产构造点 startCardStep 传了非空值与人尺度身份；这是 breakdown 缺陷族 4 第一条点名的假绿温床，只能装配级附加锁。Task C 同时保有缝级断言（徽标测试穿过 AllRunLocks），附加锁不顶替它。
- 退路同闸自查：正文无条件退路会改测试入口符号的语句——无。

## 占位符扫描与自我声明

- 全文无 TBD/「适当的」「同 Task N」。每个实现步骤给出完整代码块或精确到行的替换指令。
- 测试复用既有 harness 的例外声明：cmd 包 CLI 级测试走既有 `runLedgerCLI`/`swapDispatchTransport`（文件名 `cmd/ledgercli_test.go`、`cmd/card_dispatch_test.go`），agentd 走 `newLedgerEnv`/`seedAgentdLedger`/`ledgerGet/Post`/`waitFor`（`internal/agentd/ledgerapi_test.go`、`ledger_fixtures_test.go`、`cardstep_test.go`）——以上均为指名照抄的既有 harness，断言逐条列全，符合正当出口条款。
- 内部锁申报见上节（两条，均已给构造不出理由）。

## 派发前自审

本计划所有验收命令都是本地 go/grep 动作，无一需要驱动 handoff 派发系统自身，无需标注「由协调者执行，不派发」的验收步骤；breakdown 真机清单七条已在 Task E 第 5 步整体移交协调者，不进入实现验收。

## 自审三查

1. **spec 覆盖**：用户故事 1←断言15/20、2←断言14/26、3←断言24–27、4←断言4、5←断言11/33、6←断言12/33(takeover-release 闭环)、7←断言36、8←断言34(guard 只判他主)、9←断言38、10←断言13–21/26——十条全落到具体断言号；契约 §3 断言 1–39 逐条分配：1–12→A、13–23→B、24–32→D、33–37/39→A、38→C；§4 七条欠账：1→A、2→B、3→D、4→A、5/6→C、7→B 金样本。
2. **占位符扫描**：见上节，含两项自我声明。
3. **跨 task 类型/签名一致性**：0.4 总表逐字核对——B 的五方法签名与契约 §2.2 冻结签名一致；D 消费 `ledger.RunLockTTL/RunLockRenewInterval/Acquire/Renew/ReleaseRunLock` 与 B 产出同名同拼；D 的 `haltEntrypoint` 为 runner 内部方法（非缝、非导出）；C 消费 `AllRunLocks/RunLock.ExpiresAt` 与 B 产出一致；A 的 `ClaimCard(id,owner)` 与 D/U2 消费一致。
