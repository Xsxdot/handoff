# B382 plan：非派发实现卡的工作分支来源（人工登记）

> 卡 B382 · 入口节点 charter:plan · spec `docs/superpowers/specs/b382.md`（已批准，2026-09-22，裁决 #10 选 E）
> 基线分支 `cards/B382-charter`，起手 HEAD `e5e428a5`（本节点未做任何合并）。
> 凡引用行号者动手前重核，漂了以符号为准。
> 台账 `docs/superpowers/ledgers/2026-09-23-b382-plan-ledger.md`（含亲跑命令、原始输出、图查询读数）。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。
> codegraph 已安装并已使用；`flow WorkBranch` 基线 `degraded`（无 flows 段）已按纪律改读源码。

---

## 0. 待拍板清单（一项；本计划已按推荐项出稿，协调者不认可可在 T2 前否决）

| # | 岔口 | 选项 | 影响 |
|---|------|------|------|
| **P1** | **登记分支的 `WorkBranchInfo.Target` 取什么** | (甲) **取空串**（登记只记「哪条」，不记来源机）：任何带非空目标机的派发都会命中现状跨机闸（`dispatch.go:286`，`previousTarget="" != target`），要求登记分支已发布 origin；(乙) **登记时一并记目标机**：同机派发走本地起点，不需 push，但要给 `card work-branch` 加 `--target`，与 spec §5 的命令形状（`card work-branch <卡号> <分支>`）冲突 | **推荐 (甲)**，且是本计划唯一可行项：spec §5 已把命令形状定死为两个位置参数，登记载荷（spec §5「谁、何时、哪条」）不含机器；spec §6 接缝 3 明说「登记的分支未发布 origin 且目标机与上一台不同 → 拒发并给出 push 指路。无新缝」——「上一台」未知即取空，(甲) 正是这个语义。**代价（须知晓）**：登记分支哪怕在**同机**派发，只要目标机非空也要先在 origin 可见（这与 spec §4 故事 5 一致，且 spec 四张真机实例的分支都已在功能线上）。**若协调者欲选 (乙)**：T2 的 `WorkBranchInfo{Branch: branch, Target: ""}` 改成携带 target、`RegisterWorkBranch` 加 target 参数、CLI 加 flag——改动面多一处，且需回改 spec §5 命令形状，故非默认。 |

---

## 1. 问题与现状（证据驱动，全部亲读源码；原始读数见台账 §3）

### R1（根因）：「卡的工作分支」只有一个来源——非审阅 dispatched 快照

`Store.WorkBranch`（`internal/ledger/events.go:575-613`）单遍扫 `EvDispatched`，跳过
`purpose==PurposeReview`，取最后一条非审阅快照的 `Branch/Target/TaskID`；一条都没有时
在 `:610` 返回 `fmt.Errorf("卡 %s 没有非审阅的 dispatched 快照（还没派过实现轮？）: %w", cardID, ErrNotFound)`。
本节点已 grep 全仓：该文案**仅此一处**，且**无任何测试锁它**（台账 §3）。

于是「实现不是派发出去的 implement 轮做的卡」（人工本机改、跨机手工接续、从 PR 进来）
一律取不到工作分支：审阅轮在 `internal/ledgerstep/dispatch.go:325-327` 直接
`return zero, fmt.Errorf("审阅轮取工作分支: %w", workErr)`，卡停在 review 列无出口（spec §1 真机实例）。

### R2：判据收口点在账本，派发侧只消费

`WorkBranch` 的生产调用方（本节点 `codegraph who-calls n_ledger_Store_WorkBranch` 实测，
台账 §2）：`Dispatcher.ViaTemplate`（`dispatch.go:271`，取 base 与跨机判据）与
`NodeStep.RunOnce`（`node.go:430`，pass 后推 origin）。两处都只问账本要答案，不自己拼来源。
⇒ 按 spec §5「判据收口在账本层」，本卡只改账本查询 + 新增登记写入口，不动调用方逻辑。

### R3：事件是纯追加，新增事件类型零迁移

`card_events.type` 是 `TEXT NOT NULL`，无 CHECK/枚举（`internal/ledger/store.go:234-238`）；
事件常量词表在 `internal/ledger/types.go:48-101`，注释明说「追加式、不做回填」。
`appendEvent` 对 payload 做 `json.Marshal`、`EventsFromAsc` 做 `json.Unmarshal`
（`internal/ledger/events.go:19-58`、`:90-100`）。⇒ 新事件类型 `work_branch_registered` 与
既有 `work_branch_published` 同形，无需 schema 变更。

### R4：跨机闸现状（无需新缝）

`dispatch.go:286-303`：`hasWorkBranch && previousTarget != target` 时查
`WorkBranchPublished(cardID, workBranch)`，取不到即拒发：
`"工作分支只存在于创建它的那台机器：上次目标机 %q，本次目标机 %q；请先在上一台 git push origin %s（失败则 needs_human）。日常路径不使用 --base"`。
登记分支 `Target=""` ⇒ `previousTarget="" != target` ⇒ 只要分支不在 origin 就拒发并指路 push
（spec §6 接缝 3 断言，P1 甲）。**无新缝、无新机制**。

### R5：CLI 卡命令族的装配形状

`cmd/card.go`：`cardCmd`（`:19`）、子命令包级变量与 `init()`（`:528-567`）里
`cardCmd.AddCommand(...)`。新增命令函数放独立文件 `cmd/card_work_branch.go`，其 `init()`
只做一行 `cardCmd.AddCommand`（Go 允许同包多文件多个 `init()`）。CLI 测试基座
`runLedgerCLI`（`cmd/ledgercli_test.go:33`）可用。

---

## 2. 分流决定

| 事项 | 归属 | 处置 |
|---|---|---|
| 工作分支读取顺序「快照 → 登记 → 报错」 | 本卡 / `internal/ledger` | **T2**：新增 `EvWorkBranchRegistered` + `WorkBranchRegistration` + 重构 `WorkBranch`，抽出 `nonReviewDispatch` |
| 登记写入口（新建导出符号） | 本卡 / `internal/ledger` | **T2**：`Store.RegisterWorkBranch(cardID, branch, actor) (applied bool, err error)` |
| 报错文案指路登记命令 | 本卡 / `internal/ledger` | **T1**（红锚）+ T2：`WorkBranch` 文案改为含 `handoff card work-branch <卡> <分支>` |
| 审阅轮取用登记分支、跨机 origin 闸 | 本卡 / `internal/ledgerstep` | **T3**：只加测试锁，**零生产改动**（R2/R4：调用方逻辑不变） |
| CLI 登记入口 `handoff card work-branch <卡号> <分支>` | 本卡 / `cmd` | **T4**：新文件 `cmd/card_work_branch.go` |
| `WorkBranchInfo.Target` 取空（P1 甲） | 本卡 / `internal/ledger` | **T2**：登记返回 `WorkBranchInfo{Branch: branch}`（Target/TaskID 空） |
| 覆盖/清除/审计留痕 | 本卡 / `internal/ledger` | **T2**：最后一条胜；空串=清除（也落事件） |
| `base_branch` 语义、权限/裁决/路由 | 不在本卡 | spec §7；本计划不触碰 `SetCardBaseBranch` |
| 伪造 dispatched 快照 | 永不做 | spec §7：审计事件只能由真实派发产生 |
| 真机重放 | 协调者执行 | 见 §11；本 task 由协调者执行，不派发 |

---

## 3. 任务 DAG

```
T1（红锚·缝级，账本包）：无快照无登记时 WorkBranch 文案须指路登记命令。
    今天编译得过、跑起来红（现状文案不含 work-branch）。
      └→ T2（实现·缝级，账本包）：事件类型 + 载荷 + WorkBranch 重构 + RegisterWorkBranch，
             T1 转绿；补接缝 1①②③ 与接缝 2 的账本级用例。
            ├→ T3（锁·缝级，ledgerstep）：只加测试——审阅轮取登记分支为 base；
            │      登记分支未发布 origin 跨机拒发 + push 指路；已发布放行。
            └→ T4（实现·CLI）：cmd/card_work_branch.go + CLI 级端到端用例。
                  └→ T5（收口）：不误伤回归 + 变异复验。
```

- **最薄路径条**：T1 是 spec §6 承重缝 `Store.WorkBranch` 上「无来源时报错指路登记」的最薄
  可跑路径，**今天编译得过且会红**（本节点以同形临时原型实跑命中，台账 §1.1）。T1 转绿即
  该行为点亮。
- **为什么 T1 只锁文案、不锁「登记后审阅可跑」**：登记写入口是新符号，基线不存在；写引用它的
  用例在基线**不可编译**，无从红。故红锚由只用既有符号的文案用例承担；「登记后审阅可跑」由
  T2（账本级）与 T3（`ViaTemplate` 缝级）覆盖。此点在 §12 显式声明为「红基线声明」。
- **次序承重**：T1 的红必须先出现，才能证明 T2 的重构是它转绿的原因；T5 的变异复验依赖登记在场。

---

## 4. 基线事实（实现卡共享，动手前复核；原始输出见台账 §1）

**亲跑读数（本节点，HEAD `e5e428a5`）**：

- `go version` → `go1.26.1 linux/amd64`。
- `go build ./...` → `BUILD_EXIT=0`。
- `go vet ./internal/ledger/ ./internal/ledgerstep/ ./cmd/` → `VET_EXIT=0`。
- `go test ./internal/ledger/ ./internal/ledgerstep/ -count=1 -timeout 600s` →
  `ok internal/ledger 26.005s`、`ok internal/ledgerstep 18.556s`，`EXIT=0`。
- `go test ./cmd/ -run 'TestCardPrefixEndToEnd|TestOpenLedgerFallbackSQLite' -count=1` → `ok 0.242s`。
- `go test ./cmd/ -run TestRepoContractGate -count=1` → `ok 0.067s`（新增同域符号不造新跨域边）。
- **红锚基线读数**（同形临时原型实跑，跑完已删，原文见台账 §1.1）：
  `go test ./internal/ledger/ -run TestZZB382TmpErrorText -count=1`
  → `FAIL ... error text lacks registration pointer: 卡 P1 没有非审阅的 dispatched 快照（还没派过实现轮？）: ledger: 记录不存在`。

**现状签名与调用面（直读源码；行号为现状读数）**：

- `Store.WorkBranch(cardID string) (WorkBranchInfo, error)` — `internal/ledger/events.go:575`。
- `WorkBranchInfo{Branch, Target, TaskID string}` — `internal/ledger/events.go:143`。
- `DispatchSnapshot`（`Purpose/Branch/Target/TaskID`）— `internal/ledger/events.go:115-139`。
- `RecordDispatch` — `internal/ledger/events.go:150`；`RecordWorkBranchPublished` — `:624`；
  `WorkBranchPublished` — `:637`。
- `appendEvent(tx, sink, cardID, typ, actor, payload any)` — `internal/ledger/events.go:19`。
- `mutate(func(tx *sql.Tx, sink *eventSink) error) error` — `internal/ledger/store.go`（包内事务入口）。
- `getCardTx(s, tx, cardID)` — 包内（`internal/ledger/cards.go` 等共用）。
- `PurposeReview` / `PurposeImplement` — `internal/ledger/types.go`（导出常量）。
- `ErrNotFound` — `internal/ledger`（导出）。
- `log()` — `internal/ledger`（包内结构化 logger）。
- `Dispatcher.ViaTemplate(ctx, c, TemplateDispatch) (DispatchResult, error)` — `internal/ledgerstep/dispatch.go:234`；
  审阅拒发点 `:325-327`；跨机闸 `:286-303`；base 取 `workInfo.Branch` `:360-365`。
- `cmd.openLedger() (*ledger.Store, error)` — `cmd/ledgercli.go:29`；`cmd.ledgerActor() string` — `:44`。

**既有测试夹具（复用，签名逐字）**：

- `seedStore(t *testing.T) *Store` — `internal/ledger/cards_test.go:12`（建显式测试工作流的库）。
- `mk(t *testing.T, s *Store, title string) Card` — `internal/ledger/relations_test.go:8`。
- `dispatchTestCard(t *testing.T) (*ledger.Store, ledger.Card)` — `internal/ledgerstep/dispatch_test.go:48`
  （含模板 `feature-impl`/`review-generic` 与工作流 `bug`）。
- `runLedgerCLI(t *testing.T, dir string, args ...string) (string, string, error)` — `cmd/ledgercli_test.go:33`。

---

## 5. 接口契约（执行者只看本 task，故这里列全）

### Consumes（本卡要用的既有签名，一字不改）

```go
// internal/ledger（包内）
func (s *Store) appendEvent(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any) (int64, error)
func (s *Store) mutate(fn func(tx *sql.Tx, sink *eventSink) error) error
func getCardTx(s *Store, tx *sql.Tx, cardID string) (Card, error)
func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error)
func (s *Store) TasksOf(cardID string) ([]TaskLink, error)
func log() *slog.Logger
// 导出
const PurposeReview = "review"
const PurposeImplement = "implement"
var ErrNotFound error
type DispatchSnapshot struct { /* … Purpose, Branch, Target, TaskID … */ }

// cmd（包内）
func openLedger() (*ledger.Store, error)
func ledgerActor() string
```

### Produces（本卡新增，跨 task 逐字对齐）

```go
// internal/ledger/types.go（事件常量追加）
const EvWorkBranchRegistered = "work_branch_registered"

// internal/ledger/events.go
type WorkBranchRegistration struct {
	Branch string `json:"branch"`
}
func (s *Store) nonReviewDispatch(cardID string) (WorkBranchInfo, bool, error)      // 包内 helper
func (s *Store) lastWorkBranchRegistration(cardID string) (string, error)           // 包内 helper
func (s *Store) RegisterWorkBranch(cardID, branch, actor string) (applied bool, err error)
func (s *Store) WorkBranch(cardID string) (WorkBranchInfo, error)                    // 签名不变，行为改

// cmd/card_work_branch.go（新文件）
var cardWorkBranchCmd *cobra.Command

// 测试（新文件）
// internal/ledger/b382_work_branch_test.go
func TestB382WorkBranchErrorPointsToRegistration(t *testing.T)   // T1，红锚（缝 1 断言②）
func TestB382WorkBranchReturnsRegisteredBranch(t *testing.T)     // T2，缝 1 断言①
func TestB382WorkBranchSnapshotWinsOverRegistration(t *testing.T) // T2，缝 1 断言③
func TestB382RegisterLastWinsAndClear(t *testing.T)              // T2，缝 2
func TestB382RegisterAuditTrail(t *testing.T)                    // T2，缝 2（谁/何时/哪条）
func TestB382RegisterClearLeavesAudit(t *testing.T)              // T2，缝 2 边界
func TestB382WorkBranchRegistrationJSONRoundTrip(t *testing.T)   // T2，内部锁（序列化边界）
// internal/ledgerstep/b382_work_branch_dispatch_test.go
func TestB382ReviewUsesRegisteredBranch(t *testing.T)            // T3，缝 1 调用方
func TestB382ReviewWithoutAnySourcePointsToRegistration(t *testing.T) // T3，缝 1 断言②调用点
func TestB382ReviewRegisteredBranchNotPublishedRejects(t *testing.T)  // T3，缝 3
func TestB382ReviewRegisteredBranchAfterPublishAllows(t *testing.T)   // T3，缝 3
func TestB382SnapshotStillWinsAtDispatch(t *testing.T)           // T3，回归锁
// cmd/card_work_branch_test.go
func TestB382CardWorkBranchCLIEndToEnd(t *testing.T)             // T4，CLI × 账本边界
```

> **序列化边界**：本卡**新增一个数据字段**——登记事件 payload `{"branch":"<分支>"}`。
> 它从 `RegisterWorkBranch` 的 `json.Marshal`（经 `appendEvent`）产生，到
> `lastWorkBranchRegistration` 的 `json.Unmarshal` 消费；两端都在账本包内，但**穿过 SQLite
> 的 TEXT 存储**。锁法：`TestB382RegisterAuditTrail` 走真实事件流读写（人/时/条三要素），
> `TestB382WorkBranchRegistrationJSONRoundTrip` 以属性锁 encode∘decode 恒等（含空串）。
> 无跨语言另一侧、无 DTO/线格式改动、无新命令字段。

---

## 6. 任务详情

### T1 红锚：无来源时 WorkBranch 文案指路登记（先红）

**动作**：新建 `internal/ledger/b382_work_branch_test.go`，内容如下（完整，无占位）。
本节点已用**同形临时原型**在基线实跑（跑完已删，原文见台账 §1.1），确认编译得过、且因
文案不含登记命令而红——实现卡落正式文件后复跑一次确认同一红读数，再进 T2。

```go
// b382_work_branch_test.go —— B382 人工登记工作分支的账本回归。
//
// 职责：锁住「工作分支的来源顺序：非审阅 dispatched 快照 → 人工登记 → 报错」，
// 以及登记写入口的审计/覆盖/清除语义。缝见 docs/superpowers/specs/b382.md §6。
// 边界：只跑 SQLite 账本（newTestStore/seedStore 基座），不碰 git、不碰 agentd。
package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestB382WorkBranchErrorPointsToRegistration 是红锚（spec §6 接缝 1 断言②）：
// 卡既无快照也无登记时，WorkBranch 的报错必须指路登记命令，且保留 ErrNotFound 根因。
// 基线现状文案是「…没有非审阅的 dispatched 快照（还没派过实现轮？）」，不含
// 「work-branch」——本用例在基线可编译且红（原文见 2026-09-23-b382-plan-ledger.md §1.1）。
func TestB382WorkBranchErrorPointsToRegistration(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "无快照无登记")
	_, err := s.WorkBranch(card.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实得 %v", err)
	}
	for _, want := range []string{"work-branch", card.ID, "登记"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错应指路登记（缺 %q）：%v", want, err)
		}
	}
}
```

- **Interfaces**
  - Consumes：`seedStore`、`mk`（§4 夹具）、`Store.WorkBranch`、`ErrNotFound`（既有）。
  - Produces：本文件（T2 追加其余用例）。
- **步骤**
  1. 落文件。
  2. 跑红（**实现卡的第一步**）：
     `go test ./internal/ledger/ -run TestB382WorkBranchErrorPointsToRegistration -count=1 -timeout 120s`
     → 预期 FAIL：`报错应指路登记（缺 "work-branch"）：卡 … 没有非审阅的 dispatched 快照（还没派过实现轮？）…`
     把原文抄进实现台账。
- **测试范围声明**：只跑 `./internal/ledger/`（本 task 只新增测试文件）。
- **日志与注释**：文件头写职责/边界/缝；红锚函数写 why（红锚为何锁文案，见 §3 最薄路径条）。

### T2 实现（缝级）：账本的事件类型 + 查询顺序 + 登记写入口

**文件**：改 `internal/ledger/types.go`、`internal/ledger/events.go`；追加测试到 T1 文件。

#### 改动一：`internal/ledger/types.go` —— 事件常量（追加，放在 `EvWorkBranchPublished` 附近，`types.go:53` 之后）

```go
	// EvWorkBranchRegistered 人工登记某卡的工作分支来源（B382）。只在卡上还没有
	// 非审阅 dispatched 快照时被 WorkBranch 采纳；有快照时快照为权威，本事件只留痕。
	// 载荷见 WorkBranchRegistration；branch 为空串表示清除登记。追加式事件，老读者
	// 忽略未知类型即在（与 work_branch_published 同形）。
	EvWorkBranchRegistered = "work_branch_registered"
```

#### 改动二：`internal/ledger/events.go` —— 载荷类型（放在 `WorkBranchInfo` 之后，`events.go:147` 附近）

```go
// WorkBranchRegistration 人工登记工作分支事件的载荷。
// Branch 为空串表示清除登记（清除也落事件，留审计痕迹）。
type WorkBranchRegistration struct {
	Branch string `json:"branch"`
}
```

#### 改动三：`internal/ledger/events.go` —— 用下列整块替换现有 `WorkBranch`（含其注释，现状 `:570-613`）

```go
// nonReviewDispatch 返回卡最后一条**非审阅** dispatched 快照，并报告卡上是否
// 存在过这样的快照。老快照没有 purpose 字段时回落到挂账表按 task_id 查用途。
// 它是 WorkBranch 与 RegisterWorkBranch 共用的唯一用途判定，避免两处漂移。
func (s *Store) nonReviewDispatch(cardID string) (WorkBranchInfo, bool, error) {
	events, err := s.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		return WorkBranchInfo{}, false, fmt.Errorf("读卡 dispatched 事件: %w", err)
	}
	links, err := s.TasksOf(cardID)
	if err != nil {
		return WorkBranchInfo{}, false, err
	}
	purposeOf := map[string]string{}
	for _, link := range links {
		purposeOf[link.TaskID] = link.Purpose
	}
	info := WorkBranchInfo{}
	has := false
	for _, event := range events {
		if event.Type != EvDispatched {
			continue
		}
		var snapshot DispatchSnapshot
		if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
			continue
		}
		purpose := snapshot.Purpose
		if purpose == "" {
			purpose = purposeOf[snapshot.TaskID]
		}
		if purpose == PurposeReview {
			continue
		}
		has = true
		if snapshot.Branch != "" {
			info = WorkBranchInfo{Branch: snapshot.Branch, Target: snapshot.Target, TaskID: snapshot.TaskID}
		}
	}
	return info, has, nil
}

// lastWorkBranchRegistration 返回卡最后一条 work_branch_registered 事件的
// branch；无登记或最后一条是清除（空串）都返回空串（最后一条胜，spec §5）。
func (s *Store) lastWorkBranchRegistration(cardID string) (string, error) {
	events, err := s.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		return "", fmt.Errorf("读卡工作分支登记事件: %w", err)
	}
	branch := ""
	for _, event := range events {
		if event.Type != EvWorkBranchRegistered {
			continue
		}
		var payload WorkBranchRegistration
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		branch = strings.TrimSpace(payload.Branch)
	}
	return branch, nil
}

// RegisterWorkBranch 人工登记（或覆盖、清除）卡的工作分支来源（B382）。
//
// 参数：cardID 卡号；branch 工作分支名，空串（含纯空白）= 清除登记；actor 审计身份。
// 返回：applied 表示本次登记是否落在**生效范围**内（卡上还没有任何非审阅 dispatched
// 快照）。有快照时登记仍落事件留痕，但 WorkBranch 不采纳——返回 applied=false，
// 由调用方提示；本方法不因此报错（spec §5「不报错、不进判据」）。
//
// 为什么允许覆盖且最后一条胜：人工可能登记错分支，重新登记即可纠正；清除同样留事件
// （空 branch），事件流因此完整记录「谁、何时、登记成哪条」。
//
// 注意：applied 只是调用方的提示位，不参与 WorkBranch 的裁决；WorkBranch 每次读都按
// 「快照存在即为权威」重新判定，故与写入并发时 applied 陈旧不会改变查询结果。
func (s *Store) RegisterWorkBranch(cardID, branch, actor string) (bool, error) {
	branch = strings.TrimSpace(branch)
	_, hasSnapshot, err := s.nonReviewDispatch(cardID)
	if err != nil {
		return false, err
	}
	err = s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("登记工作分支: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvWorkBranchRegistered, actor,
			WorkBranchRegistration{Branch: branch})
		return err
	})
	if err != nil {
		return false, err
	}
	applied := !hasSnapshot
	log().Info("工作分支登记已落账", "card", cardID, "branch", branch,
		"applied", applied, "actor", actor)
	return applied, nil
}

// WorkBranch 卡的工作分支：来源顺序为**非审阅 dispatched 快照 → 人工登记 → 报错**。
//
// 审阅只读、跑在工作分支上不新开分支，所以「卡的分支」的答案必须跳过审阅轮；人工在
// 本机实现（没有任何 dispatched 快照）的卡没有快照来源，B382 补一条人工登记来源。
// 快照一旦存在即为权威——此时登记被忽略（spec §3 生效范围裁定）。报错指路登记命令
// （B382 文案），而不是旧文「还没派过实现轮？」。老快照没有 purpose 字段时回落到挂账
// 表按 task_id 查用途。
func (s *Store) WorkBranch(cardID string) (WorkBranchInfo, error) {
	var zero WorkBranchInfo
	info, hasSnapshot, err := s.nonReviewDispatch(cardID)
	if err != nil {
		return zero, err
	}
	if hasSnapshot && info.Branch != "" {
		return info, nil
	}
	if !hasSnapshot {
		branch, err := s.lastWorkBranchRegistration(cardID)
		if err != nil {
			return zero, err
		}
		if branch != "" {
			return WorkBranchInfo{Branch: branch}, nil
		}
	}
	return zero, fmt.Errorf("卡 %s 还没有工作分支：既没有非审阅的 dispatched 快照，也没有人工登记。"+
		"本机人工实现的卡请先登记后重试：handoff card work-branch %s <分支>: %w",
		cardID, cardID, ErrNotFound)
}
```

> 注意：`strings`、`json`、`fmt`、`errors` 已在 `events.go` 顶部导入（现状 `:6-15`），无需新增 import。

#### 改动四：追加下列用例到 `internal/ledger/b382_work_branch_test.go`（同文件，接 T1）

```go
// TestB382WorkBranchReturnsRegisteredBranch 接缝 1 断言①（承重）：无快照 +
// 有登记 → 返回登记的分支；Target/TaskID 为空（登记不携带来源机，见 plan §0 P1）。
func TestB382WorkBranchReturnsRegisteredBranch(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "人工实现")
	if _, err := s.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	got, err := s.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != "feat/b382-work" || got.Target != "" || got.TaskID != "" {
		t.Fatalf("登记分支读数 = %+v", got)
	}
}

// TestB382WorkBranchSnapshotWinsOverRegistration 接缝 1 断言③（生效范围裁定）：
// 有非审阅快照时登记被忽略（applied=false），WorkBranch 返回快照。
func TestB382WorkBranchSnapshotWinsOverRegistration(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "两者都有")
	if err := s.RecordDispatch(card.ID, DispatchSnapshot{
		Template: "feature-impl", Target: "mac-02", TaskID: "T-impl",
		Branch: "cards/B382-implement", Purpose: PurposeImplement, Actor: "test",
	}); err != nil {
		t.Fatalf("落快照: %v", err)
	}
	applied, err := s.RegisterWorkBranch(card.ID, "feat/should-ignore", "cli:u@h")
	if err != nil {
		t.Fatalf("登记: %v", err)
	}
	if applied {
		t.Fatal("有快照时登记不应生效（applied 应为 false）")
	}
	got, err := s.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != "cards/B382-implement" || got.Target != "mac-02" || got.TaskID != "T-impl" {
		t.Fatalf("快照应胜出，实得 %+v", got)
	}
}

// TestB382RegisterLastWinsAndClear 接缝 2：再次登记最后一条胜；空串清除。
func TestB382RegisterLastWinsAndClear(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "覆盖与清除")
	for _, b := range []string{"feat/one", "feat/two"} {
		if _, err := s.RegisterWorkBranch(card.ID, b, "cli:u@h"); err != nil {
			t.Fatalf("登记 %s: %v", b, err)
		}
	}
	if got, err := s.WorkBranch(card.ID); err != nil || got.Branch != "feat/two" {
		t.Fatalf("最后一条应胜出：got=%+v err=%v", got, err)
	}
	if _, err := s.RegisterWorkBranch(card.ID, "", "cli:u@h"); err != nil {
		t.Fatalf("清除登记: %v", err)
	}
	if _, err := s.WorkBranch(card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("清除后应 ErrNotFound，实得 %v", err)
	}
}

// TestB382RegisterAuditTrail 接缝 2：登记事件流可查「谁/何时/哪条」，穿过 appendEvent
// 的真实 JSON 序列化与 SQLite 存取边界。
func TestB382RegisterAuditTrail(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "审计留痕")
	if _, err := s.RegisterWorkBranch(card.ID, "feat/audit", "cli:alice@host"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	var found Event
	for _, e := range events {
		if e.Type == EvWorkBranchRegistered {
			found = e
		}
	}
	if found.Seq == 0 {
		t.Fatal("未找到 work_branch_registered 事件")
	}
	if found.Actor != "cli:alice@host" {
		t.Fatalf("actor（谁）= %q", found.Actor)
	}
	if found.CreatedAt.IsZero() {
		t.Fatal("created_at（何时）不应为零值")
	}
	var payload struct {
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("解码登记载荷: %v", err)
	}
	if payload.Branch != "feat/audit" {
		t.Fatalf("payload.branch（哪条）= %q", payload.Branch)
	}
}

// TestB382RegisterClearLeavesAudit 接缝 2 边界：清除也留痕（空 branch 的登记事件）。
func TestB382RegisterClearLeavesAudit(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "清除留痕")
	if _, err := s.RegisterWorkBranch(card.ID, "feat/x", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	if _, err := s.RegisterWorkBranch(card.ID, "", "cli:u@h"); err != nil {
		t.Fatalf("清除: %v", err)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	regs := 0
	for _, e := range events {
		if e.Type == EvWorkBranchRegistered {
			regs++
		}
	}
	if regs != 2 {
		t.Fatalf("清除应留下第二条登记事件，实得 %d 条", regs)
	}
}

// TestB382WorkBranchRegistrationJSONRoundTrip 序列化边界（内部锁声明见 §10）：
// 载荷 encode∘decode 恒等，含空串（清除）分支；并断言 branch 键恒在场（字段缺失与
// 空值在本语义下同为「清除」，故不引入指针类型，但用 map 探键区分「键在且为空」）。
func TestB382WorkBranchRegistrationJSONRoundTrip(t *testing.T) {
	for _, branch := range []string{"feat/x", ""} {
		raw, err := json.Marshal(WorkBranchRegistration{Branch: branch})
		if err != nil {
			t.Fatalf("marshal %q: %v", branch, err)
		}
		var back WorkBranchRegistration
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal %q: %v", branch, err)
		}
		if back.Branch != branch {
			t.Fatalf("roundtrip %q -> %q", branch, back.Branch)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal map: %v", err)
		}
		if _, ok := m["branch"]; !ok {
			t.Fatalf("载荷必须恒带 branch 键：%s", raw)
		}
	}
}
```

- **Interfaces**
  - Consumes：`seedStore`、`mk`、`DispatchSnapshot`、`RecordDispatch`、`Event`、`EvWorkBranchRegistered`、
    `WorkBranchRegistration`、`WorkBranch`、`RegisterWorkBranch`、`ErrNotFound`、`PurposeImplement`。
  - Produces：见 §5。
- **步骤**
  1. 判据先在基线跑（复核）：T1 已红（实现卡第一步的原文）。
  2. 改 `internal/ledger/types.go`（改动一）。
  3. 改 `internal/ledger/events.go`（改动二 + 改动三）。
  4. 追加 T2 用例到 `internal/ledger/b382_work_branch_test.go`。
  5. 跑绿：`go test ./internal/ledger/ -run 'TestB382' -count=1 -timeout 120s` → 预期全 PASS（含 T1）。
  6. 不误伤（本 task 的重点）：`go test ./internal/ledger/ -count=1 -timeout 300s` →
     预期 `ok`（尤其 `TestWorkBranchSkipsReviewRounds` 必须仍绿——重构保序）。
  7. 静态检查：`go build ./...`、`go vet ./internal/ledger/`（预期 `EXIT=0`）。
- **加关键节点日志**：`RegisterWorkBranch` 成功路径一条 Info（card/branch/applied/actor，成功路径不静默）；
  `nonReviewDispatch` / `lastWorkBranchRegistration` / `WorkBranch` 是纯读，不刷屏（既有 `EventsFromAsc`
  出错已带包装）。`applied=false` 的提示由 CLI 输出（T4）。
- **加注释**：新载荷类型、两个 helper、`RegisterWorkBranch`、`WorkBranch` 的「为什么」（来源顺序、
  生效范围、最后一条胜、applied 不参与裁决）已写全。
- **测试范围声明**：只跑 `./internal/ledger/`。

### T3 锁（缝级）：审阅轮取登记分支 + 跨机 origin 闸（零生产改动）

**文件**：新建 `internal/ledgerstep/b382_work_branch_dispatch_test.go`（**不改任何生产代码**）。

```go
// b382_work_branch_dispatch_test.go —— B382 派发侧回归。
//
// 职责：锁住「登记的工作分支被审阅轮取用为基线」（spec §6 接缝 1 调用方），以及
// 「登记分支未发布 origin 时跨机拒发并指路 push」（接缝 3，复用现状 WorkBranchPublished）。
// 缝：Dispatcher.ViaTemplate——审阅轮取工作分支与跨机 origin 闸都在此。
// 边界：不碰真 git（Transport 注入桩）、不启 agentd；生产逻辑零改动，本文件只加锁。
package ledgerstep

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// TestB382ReviewWithoutAnySourcePointsToRegistration 锁接缝 1 断言②的调用点：
// 卡无快照无登记时，审阅轮拒发文案指路登记命令（不是「还没派过实现轮？」）。
func TestB382ReviewWithoutAnySourcePointsToRegistration(t *testing.T) {
	st, card := dispatchTestCard(t)
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(context.Context, DispatchOpts) (string, string, error) {
		return "T-should-not", "", nil
	}}
	_, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Node: "review"})
	if err == nil || !strings.Contains(err.Error(), "work-branch") {
		t.Fatalf("无快照无登记的审阅轮应指路登记，实得 %v", err)
	}
}

// TestB382ReviewUsesRegisteredBranch 锁接缝 1 调用方：卡无快照、有登记，审阅轮
// （目标机为空 ⇒ 不触发跨机闸）应以登记分支为 base，并切一次性审阅分支、走本地起点。
func TestB382ReviewUsesRegisteredBranch(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(_ context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-b382-review", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Node: "review"}); err != nil {
		t.Fatalf("登记后审阅轮应可派发，实得 %v", err)
	}
	if got.Base != "feat/b382-work" {
		t.Fatalf("审阅基线应为登记分支，实得 %q", got.Base)
	}
	if want := "cards/" + card.ID + "-review-1"; got.Branch != want {
		t.Fatalf("审阅分支应为 %q，实得 %q", want, got.Branch)
	}
	if !got.LocalBaseBranch {
		t.Fatal("目标机为空（同机）时登记分支应走本地起点")
	}
}

// TestB382ReviewRegisteredBranchNotPublishedRejects 接缝 3：登记分支未发布 origin、
// 目标机非空（与「未知上一台」不同机）→ 拒发并指路 git push，且不触达 Transport。
func TestB382ReviewRegisteredBranchNotPublishedRejects(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	transportCalls := 0
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(context.Context, DispatchOpts) (string, string, error) {
		transportCalls++
		return "T-should-not", "", nil
	}}
	_, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Target: "mac-02", Node: "review"})
	if err == nil {
		t.Fatal("登记分支未发布 origin 时应拒发")
	}
	for _, want := range []string{"git push origin", "feat/b382-work"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("拒发文案缺 %q：%v", want, err)
		}
	}
	if transportCalls != 0 {
		t.Fatalf("拒发不得触达 Transport，调用次数=%d", transportCalls)
	}
}

// TestB382ReviewRegisteredBranchAfterPublishAllows 接缝 3 正例：登记分支已发布
// origin 后，跨目标机审阅可派发，且不再走本地-only 基线。
func TestB382ReviewRegisteredBranchAfterPublishAllows(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	if err := st.RecordWorkBranchPublished(card.ID, "feat/b382-work", "", "", "tester"); err != nil {
		t.Fatalf("发布登记分支: %v", err)
	}
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(_ context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-b382-review", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Target: "mac-02", Node: "review"}); err != nil {
		t.Fatalf("已发布后跨机审阅应放行，实得 %v", err)
	}
	if got.Base != "feat/b382-work" || got.LocalBaseBranch {
		t.Fatalf("跨机接续：base=%q local=%v，want feat/b382-work / false", got.Base, got.LocalBaseBranch)
	}
}

// TestB382SnapshotStillWinsAtDispatch 回归锁：有快照的卡派发路径不变，登记不改读数。
func TestB382SnapshotStillWinsAtDispatch(t *testing.T) {
	st, card := dispatchTestCard(t)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Template: "feature-impl", Target: "mac-02", TaskID: "T-impl",
		Branch: "cards/" + card.ID + "-implement", Purpose: ledger.PurposeImplement, Actor: "test",
	}); err != nil {
		t.Fatalf("落快照: %v", err)
	}
	if _, err := st.RegisterWorkBranch(card.ID, "feat/ignored", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	wb, err := st.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if wb.Branch != "cards/"+card.ID+"-implement" {
		t.Fatalf("快照应胜出，实得 %q", wb.Branch)
	}
}
```

- **Interfaces**
  - Consumes：`dispatchTestCard`、`Dispatcher`、`TemplateDispatch`、`DispatchOpts`、
    `Store.RegisterWorkBranch`、`Store.WorkBranch`、`Store.RecordWorkBranchPublished`、
    `ledger.DispatchSnapshot`、`ledger.PurposeImplement`。
  - Produces：本文件五个用例。
- **步骤**
  1. 落文件。
  2. 跑绿：`go test ./internal/ledgerstep/ -run 'TestB382' -count=1 -timeout 120s` → 预期全 PASS。
     若 `TestB382ReviewRegisteredBranchNotPublishedRejects` 失败，先核对错误原文是否仍含
     `git push origin`（现状 `dispatch.go:297`；文案漂了以符号为准，不得改测试迁就）。
  3. 不误伤：`go test ./internal/ledgerstep/ -count=1 -timeout 300s` → 预期 `ok`。
  4. 静态检查：`go build ./...`、`go vet ./internal/ledgerstep/`（预期 `EXIT=0`）。
- **加关键节点日志**：无新生产代码 ⇒ 无新日志；测试内不引日志断言（避免脆）。
- **加注释**：文件头写职责/边界/缝；每条用例头写锁住的断言编号。
- **测试范围声明**：只跑 `./internal/ledgerstep/`。

### T4 实现（CLI）：`handoff card work-branch <卡号> <分支>`

**文件**：新建 `cmd/card_work_branch.go`；新建 `cmd/card_work_branch_test.go`。

#### 改动五：`cmd/card_work_branch.go`（新文件）

```go
// card work-branch 命令：人工登记/覆盖/清除某卡的工作分支来源（B382）。
//
// 职责：把「本机人工实现、没有 dispatched 快照」的卡的工作分支写进账本事件流，
// 让 review 等后续节点能从该分支起。用户可见名：handoff card work-branch <卡号> <分支>。
// 边界：只写账本事件；有非审阅快照时登记留痕但不生效（快照是权威来源），此处只提示不报错。
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var cardWorkBranchCmd = &cobra.Command{
	Use:   "work-branch <卡号> <分支>",
	Short: "登记/覆盖/清除人工工作分支（本机人工实现的卡；分支传空串清除）",
	Long: "登记某卡的工作分支来源，供 review 等后续节点从该分支起。\n" +
		"卡上已有非审阅 dispatched 快照时，快照是权威来源，本次登记只留痕不生效。\n" +
		"分支传空串表示清除登记。",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		applied, err := st.RegisterWorkBranch(args[0], args[1], ledgerActor())
		if err != nil {
			return err
		}
		if !applied {
			if _, err := fmt.Fprintln(cmd.ErrOrStderr(),
				"提示：卡上已有非审阅 dispatched 快照，登记已留痕但不生效（快照是工作分支的权威来源）"); err != nil {
				return fmt.Errorf("输出登记提示: %w", err)
			}
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardCmd.AddCommand(cardWorkBranchCmd)
}
```

#### 改动六：`cmd/card_work_branch_test.go`（新文件）

```go
package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// TestB382CardWorkBranchCLIEndToEnd 穿过真实 CLI、账本 SQLite 与命令输出边界：
// add → work-branch → WorkBranch 读回；空串清除；有快照时命令成功、stderr 提示、
// WorkBranch 仍返回快照。
func TestB382CardWorkBranchCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "B382 人工实现卡", "--project", "demo")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("解 add 输出 %q: %v", out, err)
	}

	if _, _, err := runLedgerCLI(t, dir, "card", "work-branch", created.ID, "feat/b382-cli"); err != nil {
		t.Fatalf("work-branch: %v", err)
	}
	func() {
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			t.Fatalf("打开账本: %v", err)
		}
		defer st.Close()
		if wb, err := st.WorkBranch(created.ID); err != nil || wb.Branch != "feat/b382-cli" {
			t.Fatalf("登记后 WorkBranch=%+v err=%v", wb, err)
		}
	}()

	if _, _, err := runLedgerCLI(t, dir, "card", "work-branch", created.ID, ""); err != nil {
		t.Fatalf("清除: %v", err)
	}
	func() {
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			t.Fatalf("打开账本: %v", err)
		}
		defer st.Close()
		if _, err := st.WorkBranch(created.ID); err == nil {
			t.Fatal("清除后 WorkBranch 应报错")
		}
	}()

	// 有快照时：命令成功（不报错）、stderr 有提示、WorkBranch 仍返回快照。
	func() {
		st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			t.Fatalf("打开账本: %v", err)
		}
		defer st.Close()
		if err := st.RecordDispatch(created.ID, ledger.DispatchSnapshot{
			Template: "feature-impl", Target: "mac-02", TaskID: "T-cli",
			Branch: "cards/" + created.ID + "-implement", Purpose: ledger.PurposeImplement, Actor: "test",
		}); err != nil {
			t.Fatalf("落快照: %v", err)
		}
	}()
	_, errOut, err := runLedgerCLI(t, dir, "card", "work-branch", created.ID, "feat/should-ignore")
	if err != nil {
		t.Fatalf("有快照时登记不应报错: %v", err)
	}
	if !strings.Contains(errOut, "不生效") {
		t.Fatalf("有快照时应在 stderr 提示不生效，实得 %q", errOut)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("重开账本: %v", err)
	}
	defer st.Close()
	if wb, err := st.WorkBranch(created.ID); err != nil || wb.Branch != "cards/"+created.ID+"-implement" {
		t.Fatalf("快照应胜出，实得 %+v err=%v", wb, err)
	}
}
```

- **Interfaces**
  - Consumes：`openLedger`、`ledgerActor`、`cardCmd`、`runLedgerCLI`、`ledger.Open`、
    `Store.RegisterWorkBranch`、`Store.WorkBranch`、`Store.RecordDispatch`。
  - Produces：`cardWorkBranchCmd`、CLI 用例。
- **步骤**
  1. 判据先在基线跑（复核）：本节点已确认 `card add` 在 `runLedgerCLI` 基座上可跑
     （`cmd/card_prefix_test.go` 同形），无需另建基线。
  2. 落 `cmd/card_work_branch.go`。
  3. 落 `cmd/card_work_branch_test.go`。
  4. 跑绿：`go test ./cmd/ -run 'TestB382CardWorkBranchCLIEndToEnd' -count=1 -timeout 120s` → 预期 PASS。
  5. 不误伤：`go test ./cmd/ -count=1 -timeout 600s` → 预期 `ok`（含 `TestRepoContractGate`；
     本卡命令仍在 `d_cli`→`d_ledger` 既有依赖方向内，不造新跨域边）。
  6. 静态检查：`go build ./...`、`go vet ./cmd/`（预期 `EXIT=0`）。
- **加关键节点日志**：无新生产日志（账本层已记 Info）；`applied=false` 走 stderr 提示（成功路径不静默，
  spec §5「不报错」照旧满足）。
- **加注释**：文件头写职责/边界；`Use/Short/Long` 说明空串清除与「有快照不生效」。
- **测试范围声明**：只跑 `./cmd/`。

### T5 收口：不误伤回归 + 变异复验

**不误伤回归（跑既有，不新增）**：

```text
go test ./internal/ledger/ -count=1 -timeout 300s
go test ./internal/ledgerstep/ -count=1 -timeout 300s
go test ./cmd/ -count=1 -timeout 600s
```

本节点已在基线实跑 `internal/ledger` + `internal/ledgerstep` 全量绿（台账 §1）；`./cmd/` 全量
**本节点未跑**，实现卡必须自己跑到结果并抄原文进台账（本计划不替它写结论）。

**变异复验（手动，不留代码）**：

1. 把 T2 改动三中 `if !hasSnapshot { … 登记回落 … }` 整段**临时注释掉** →
   `go test ./internal/ledger/ -run TestB382WorkBranchReturnsRegisteredBranch -count=1`
   → **预期重新红**（登记不再被采纳）；撤回临改。
2. 把 `if hasSnapshot && info.Branch != ""` 改成 `if info.Branch != ""`（即允许登记参与快照存在时）→
   `go test ./internal/ledger/ -run TestB382WorkBranchSnapshotWinsOverRegistration -count=1`
   → **预期红**（快照不再绝对优先）；撤回临改。
3. 把 T1 红锚对应的新文案改回旧文案 →
   `go test ./internal/ledger/ -run TestB382WorkBranchErrorPointsToRegistration -count=1`
   → **预期红**；撤回临改。
4. 确认工作树只剩正式改动（`git status --short` 无临时文件）。

- **测试范围声明**：`./internal/ledger/ ./internal/ledgerstep/ ./cmd/`。

---

## 7. 缺陷族对抗审查（逐族设问）

**族 1 生命周期/状态机中断**
- 新增的是追加事件 + 纯读查询，无 goroutine、锁、文件句柄、工作树；`RegisterWorkBranch`
  在一个 `mutate` 事务内完成「取卡校验 + 追加事件」，失败整体回滚，不留半状态。
- `WorkBranch` 只读，不写任何数据结构；重构抽出 `nonReviewDispatch` 保持既有单遍语义
  （先扫 dispatched、再按需扫登记），不引入状态。
- 覆盖/清除都是追加：没有「删除事件」这种状态切换，事件流始终可审计（族 2 也受益）。

**族 2 静默失败 / 误导报错**
- 无来源时**不静默**：文案改为指路 `handoff card work-branch <卡> <分支>` 并保留 `ErrNotFound`
  （T1 断言三关键词）。
- 有快照登记不生效**不静默**：CLI stderr 提示「已留痕但不生效」（T4 断言 `不生效`）；账本 Info 日志带 `applied`。
- 反例保护：清除（空串）在无快照时不被当成「登记成功但无分支」——`lastWorkBranchRegistration`
  返回空串，`WorkBranch` 继续报错指路（`TestB382RegisterLastWinsAndClear` 锁定）。

**族 3 跨平台假设**
- 纯 SQLite/PG 事件存取 + 字符串处理，无平台分支代码。**无新增平台假设**。

**族 4 假红 / 假绿测试**
- T1 红锚已在基线实跑命中（台账 §1.1），非「写了就绿」。
- T2 同时含 ①登记被采纳 ②快照胜出 ③覆盖/清除 ④审计三要素 ⑤清除留痕；删掉回落会让 ①红，
  放宽快照优先级会让 ②红（T5 变异复验给可红证据）。
- T3 走真实 `Dispatcher.ViaTemplate`（含跨机闸），只替换 Transport 观测，不是只测映射函数；
  正例（已发布放行）与反例（未发布拒发）成对，避免「恒拒发」假绿。
- T4 穿过真实 CLI `Execute()` + SQLite 文件，不是包装函数直调。

**族 5 门禁绕过**
- 登记只填「缺来源」，不提供任何绕过快照的通道：有快照时 `WorkBranch` 仍返回快照
  （T2 ②、T3 回归锁）。
- 登记不绕过跨机 origin 闸，反而受其约束（T3 反例）；`SetCardBaseBranch` 冻结点不动（本卡不改）。
- 无新命令旁路，唯一入口就是 `card work-branch`（spec §5）。

**追加设问一：序列化边界**——新增字段只有登记事件 payload `branch`（Go → JSON → SQLite TEXT →
JSON → Go）。T2 `TestB382RegisterAuditTrail` 穿过真实存取边界断言「谁/何时/哪条」；
`TestB382WorkBranchRegistrationJSONRoundTrip` 属性锁 encode∘decode 恒等（含空串）。
无 DTO/投影/线格式/跨语言另一侧改动。

**追加设问二：枚举新值过既有白名单**——新事件类型 `work_branch_registered` 入 `card_events.type`，
该列无 CHECK/白名单（`store.go:234-238`）；消费者按类型精确匹配，未知类型自然忽略
（`derived.go`/`timeline` 均如此）。**无风险**。

**追加设问三：承重安全属性有测试锁住**——承重属性是「来源顺序快照→登记→报错」与「快照绝对优先」，
由 T1、T2①②③、T3 正反例共同锁定；变异复验（T5）给三处可红证据。

**残余风险（明示，不藏）**：
- 登记分支 `Target=""`（P1 甲）⇒ 任何带非空目标机的派发都要求分支已在 origin；同机但目标机非空也要 push。
  这是 spec §4 故事 5 / §6 接缝 3 的既定语义，不是漏洞，但实现卡不得顺手改成 (乙)（会越出 spec §5 命令形状）。
- `node.go:429-469` 的 pass 后推 origin：登记分支 `info.Target==""` 时会用**当前轮 target** 作为 pushTarget
  去推登记分支——若该机没有该分支会 push 失败并 `haltForHuman`。属 spec 未覆盖面，本卡不加逻辑，完成后回写 roadmap。
- 只登记、从未发布 origin、且所有派发目标机都为空时，分支只在目标机本地，跨机会话会失败——由现状闸兜住。
- `ReviewRounds`/`PurposeRounds` 的策略不变：登记不改变轮次挂号，第二轮 review 仍不会撞名。

---

## 8. 上下文预算检查

有界文件集（圈得出）：
`internal/ledger/types.go`、`internal/ledger/events.go`、`internal/ledger/b382_work_branch_test.go`（新）、
`internal/ledgerstep/b382_work_branch_dispatch_test.go`（新）、`cmd/card_work_branch.go`（新）、
`cmd/card_work_branch_test.go`（新）、`docs/superpowers/ledgers/2026-09-23-b382-plan-ledger.md`（本节点）。
不越出 `internal/ledger`、`internal/ledgerstep`（只加测试）、`cmd`。**通过**。

## 9. 类型标注 / 边界型子系统

本卡是非 wire 边界型子系统（单进程内的账本事件 + 字符串判据 + CLI 位置参数），不是跨语言序列化边界。
用户可见行为验收以显式真机清单给出（§11，本 task 由协调者执行，不派发）。
CLI 的位置参数与输出（`{"ok":true}` / stderr 提示）由 T4 用例在 `runLedgerCLI` 边界锁住。

## 10. 接缝覆盖（双向，对照 spec §6 接缝清单）

spec 的接缝 = ① 承重缝 `Store.WorkBranch`（现状符号，断言①②③）；② 登记写入口
`Store.RegisterWorkBranch`（新建导出符号，断言：事件流可查/最后一条胜/空串清除）；
③ 跨机判据复用现状 `WorkBranchPublished`（断言：登记分支未发布 origin 且目标机与上一台不同
→ 拒发 + push 指路，无新缝）。

- **测试 → 缝**：
  - T1 `TestB382WorkBranchErrorPointsToRegistration` 入口 = `Store.WorkBranch` ⇒ 缝①（断言②）。
  - T2 `TestB382WorkBranchReturnsRegisteredBranch` / `TestB382WorkBranchSnapshotWinsOverRegistration`
    入口 = `Store.WorkBranch` ⇒ 缝①（断言①/③）。
  - T2 `TestB382RegisterLastWinsAndClear` / `TestB382RegisterAuditTrail` / `TestB382RegisterClearLeavesAudit`
    入口 = `Store.RegisterWorkBranch` ⇒ 缝②。
  - T2 `TestB382WorkBranchRegistrationJSONRoundTrip` 入口 = 纯 JSON 编解码 ⇒ **内部锁**（见下）。
  - T3 五支测试入口 = `Dispatcher.ViaTemplate`，经其取工作分支（缝①调用方）与跨机闸（缝③）；
    `TestB382ReviewRegisteredBranchNotPublishedRejects` / `...AfterPublishAllows` ⇒ 缝③。
  - T4 `TestB382CardWorkBranchCLIEndToEnd` 入口 = `runLedgerCLI`（`handoff card work-branch`，
    缝②的生产调用方），并回读 `Store.WorkBranch`（缝①）⇒ 落在缝②与缝①上。
- **缝 → 测试**：
  - 缝①断言① ← T2 `TestB382WorkBranchReturnsRegisteredBranch`；断言② ← T1 + T3
    `TestB382ReviewWithoutAnySourcePointsToRegistration`；断言③ ← T2
    `TestB382WorkBranchSnapshotWinsOverRegistration` + T3 `TestB382SnapshotStillWinsAtDispatch`。
  - 缝② ← T2 三支 + T4 CLI 端到端。
  - 缝③ ← T3 正反例两支。
- **内部锁（附加，不顶替）**：
  - `TestB382WorkBranchRegistrationJSONRoundTrip` 入口不在任一缝上；缝级断言由 T2
    `TestB382RegisterAuditTrail`（穿过真实存取边界）承担。存在理由：**从声明缝构造不出
    「字段恒在场且空串可分辨」这条断言**——缝②的入口只产生非空 branch 的常规路径，无法在
    不直调编解码的前提下锁「键恒在场」。它是附加可观测性，不顶替任何缝级断言。
- **条件退路**：无（T5 变异复验是显式步骤，不改任何测试的入口符号）。

## 11. 真机清单（归协调者执行；本 task 由协调者执行，不派发）

1. 取一张 dispatched 快照 **0 次**的真卡（如 B373 形态）：`handoff card work-branch <卡> <分支>`，
   再 `handoff card dispatch <卡> --step <review 节点>`：
   - 分支已在 origin ⇒ **应放行**（202、review 起一次 `cards/<卡>-review-N` 分支）。
   - 分支不在 origin ⇒ **应拒发**，文案含 `git push origin <分支>`；push 后重试放行。
2. 登记错分支后重新登记同一卡：**最后一条胜**；`handoff card show <卡>` 的事件流能看到
   「谁、什么时候、登记成哪条」（actor + created_at + payload.branch）。
3. `handoff card work-branch <卡> ""` 清除后再派 review：**报错指路登记命令**
   （含 `handoff card work-branch`），不再是「还没派过实现轮？」。
4. 对一张**已有**非审阅 dispatched 快照的卡执行登记：命令**成功**、stderr 有「不生效」提示、
   `handoff card show` 看得到登记事件，但 review/后续节点仍走快照分支（快照权威）。
5. 观察 agentd.log：登记落账应见 `工作分支登记已落账 … applied=…`。

> 需要跑有 T2+T3+T4 改动的二进制，且依赖现场仓的分支现状；机内夹具验不了「线上那条登记分支
> 真的在 / 不在 origin」，故单列交协调者。

## 12. 占位符扫描

- 无 TBD / 「加适当的错误处理」/「同 Task N 而略」；T1~T4 的代码块与改动块均完整可抄。
- **例外声明（无）**：不依赖「形态因包而异」的夹具复用——T1/T2 复用 `seedStore`/`mk`，T3 复用
  `dispatchTestCard`，T4 复用 `runLedgerCLI`；签名已在 §4 逐字列出，且本节点亲读确认存在
  （`seedStore`/`mk`/`dispatchTestCard`/`runLedgerCLI` 均在基线跑通的测试包内）。
- **红基线声明**：T1 是唯一红锚（基线可编译、会红，已亲跑，台账 §1.1）；T2/T3/T4 的用例引用新增
  符号，基线不可编译故无红——这是「缝签名今天不存在」的必然，按「最薄路径条」由 T1 承担红证据。
- **内部锁声明**：仅 §10 列出的 `TestB382WorkBranchRegistrationJSONRoundTrip` 一支；理由见其注释与 §10。
- 条件退路：无。

## 13. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - §1 根因（工作分支单一来源=非审阅快照，人工实现卡取不到）→ §1 R1 取证 + T1/T2。
   - §3 采用方案（补人工登记来源；读序快照→登记→报错；卡命令面给登记入口）→ T2 + T4。
   - §3 生效范围（只在无快照时采纳；有快照忽略、不报错、不进判据）→ T2 `WorkBranch`
     的 `hasSnapshot && info.Branch != ""` 优先 + `RegisterWorkBranch` 返回 applied + T2 ②。
   - §3 弃选 A/A′/B/C 的结论 → 本计划不做：不动 `base_branch`、不加卡字段、不加 `--base/--branch`、
     不伪造快照（§2 分流表 + §7 族 5）。
   - §4 用户故事 1（登记后 review 能从该分支起）→ T3 `TestB382ReviewUsesRegisteredBranch`；
     故事 2（重新登记/空串清除）→ T2 `TestB382RegisterLastWinsAndClear`；故事 3（事件流看谁/何时/哪条）
     → T2 `TestB382RegisterAuditTrail`；故事 4（报错指路）→ T1 + T3
     `TestB382ReviewWithoutAnySourcePointsToRegistration`；故事 5（跨机 push 指路）→ T3 接缝③反例。
   - §5 实现决定（判据收口账本、生效范围、可覆盖追加、命令名、跨机不新增机制、报错文案）→ 见 §2 与
     T2/T3/T4；命令名逐字 `handoff card work-branch <卡号> <分支>`。
   - §6 接缝 1 断言①②③ → T1/T2①②③；接缝 2 → T2 三支 + T4；接缝 3 → T3 正反例；
     假缝禁令（无未导出无调用方的纯函数占名额）→ §10（仅一支内部锁，已声明）。
   - §7 Out of Scope（永不做伪造快照；后续做卡字段/可覆盖快照/来源机追踪/base_branch 变更）→ §2 分流表
     与 §7 族 5 确认不做。
   - 残余/roadmap → §7 残余风险 + §11 备注。
2. **占位符扫描**：见 §12，无占位。
3. **跨 task 类型/签名一致性**：
   - `RegisterWorkBranch(cardID, branch, actor string) (applied bool, err error)` 在 T2 定义；
     T4 CLI 调用 `st.RegisterWorkBranch(args[0], args[1], ledgerActor())` 两返回值逐字一致。
   - `EvWorkBranchRegistered` 常量在 `types.go`（T2 改动一），`events.go`（T2 改动三）与
     `b382_work_branch_test.go`（T2 用例）引用同一名字。
   - `WorkBranchRegistration{Branch string}` 在 T2 定义，`appendEvent` 编码与
     `lastWorkBranchRegistration` 解码对象一致。
   - `nonReviewDispatch` / `lastWorkBranchRegistration` 仅包内使用，未越包导出。
   - T3 只引用 `Store.RegisterWorkBranch` / `Store.WorkBranch` / `Store.RecordWorkBranchPublished`
     （全部 T2 或既有）；T3 零生产改动，不存在与 T2 的签名漂移。
   - T4 的 `cardWorkBranchCmd` 由本文件 `init()` 注册，不与其他文件的 `init()` 冲突（Go 多 init 合法）。

## 跨卡审计

**不适用**：本卡是 L2 单子系统单卡（spec §2），本节点只出一份 plan，无「多子卡 plan 集合」可供
跨卡逐条比对。冻结物对照（spec 签名/口径）与跨 task 签名一致性已在本计划 §13 自查。

## 图覆盖债（本节点偏差记录）

- `codegraph sym WorkBranch`、`who-calls n_ledger_Store_WorkBranch`、`sym RecordDispatch`、
  `context d_ledger` 均命中，**图覆盖无债**。
- `codegraph flow WorkBranch` 返回 `degraded=true steps=0`（基线无 flows 段）——按纪律改读源码，
  不记图覆盖债，记「flow 缺段」这一偏差。
- `codegraph sym cardUpdateCmd` **未命中**（图只覆盖 `cardUpdateCmd.RunE`，未覆盖命令变量节点）——
  记入图覆盖债；本计划据此对 CLI 装配直读 `cmd/card.go`。

> 未验证项：T1 的红锚**已验证**（同形原型实跑红，台账 §1.1）；T2~T4 测试的绿（本节点不写实现，未跑）；
> `./cmd/` 全量回归本节点未跑（只跑了 `TestCardPrefixEndToEnd`/`TestOpenLedgerFallbackSQLite`/
> `TestRepoContractGate` 三支与全量编译）；真机清单（§11）未跑。实现卡必须自跑并抄原文进台账。
