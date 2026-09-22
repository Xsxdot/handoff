# B394 debug 计划：唤醒回声自激——根因、分流与修复任务

> 卡 B394 · 入口节点 charter:debug · spec `docs/superpowers/specs/b394.md`（已批准）
> 基线分支 `cards/B394-charter-2`，起手 HEAD `21dabcdc`（已按硬性第一步
> `git fetch origin cards/B233.1-charter-7 && git merge --no-edit -X ours origin/cards/B233.1-charter-7`
> → `Already up to date`）。凡引用行号者动手前重核，漂了以符号为准。
> 台账 `docs/superpowers/ledgers/2026-09-22-b394-plan-ledger.md`（含全部亲跑命令、
> 原始输出、图查询记录、真机账本只读读数）。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。

---

## 0. 待拍板清单（阻塞实施，交协调者裁决）

| # | 岔口 | 选项 | 影响 |
|---|------|------|------|
| **P1** | **`needs_cleared` 的收口形态** | (甲) **移出唤醒映射**（改实现 + 改契约）；(乙) 复现 B389 已删的「自生事件标 seen」补丁（只压协调者自己回合内产生的那条）；(丙) actor/来源比对（协调者清标要能出示席位身份） | 决定 T3 代码。**推荐 (甲)**：(丙) 的现有数据面不支持——清标走 `ledgerActor()`=`cli:<user>@<host>`，与真人清标**同串**（证据 §R3），无法区分；加 (丙) 需先给 `card needs --clear` 注入席位身份并改 `WakeEvent`/`Decision`，跨 CLI+agentd 两子系统，越 L2。 (乙) 是 b389 §3.5.3 显式删除过的补丁，且要按「同卡 + 类型 + 轮次」过滤，窗口内会误吞并发真人事件。 (甲) 落在 `internal/agentd` 单子系统内，且 b353 §6-4 已自带「过多时后续可收紧」的出口。 **代价（须知晓）**：真人清标也不再自动唤醒协调者（真人需另发寻址消息/重派）。 |
| **P2** | **兄弟回声 `decision_opened` 是否并入本卡？** | (甲) 本卡**只修 `needs_cleared`**，`decision_opened` 另立卡；(乙) 一并修 | 真机 B382 实测 `decision_opened` 也自回声（`17719` → `17725`「本轮唤醒为 decision #10 的回声」）。但 `decision_opened` 有**合法唤醒面**（真人开裁决要叫协调者），不能整支删除；要区分只能靠来源（同 P1 丙），属架构级。**推荐 (甲)**：spec §2「做」只列「账务动作（清标）」、§4 判据只谈清标；decision 面单列。 |
| **P3** | **契约修订的落点** | (甲) 新增 `docs/superpowers/specs/b394-contract.md` 增量 + 在 b358 规则 31 / b353 条目 40 就地加指针；(乙) 直接改 b358/b353 正文不新增增量 | 决定 T2。**推荐 (甲)**：与 b389-contract / b390-contract 的既有增量pattern一致，冻结物触碰留痕。 |

---

## 1. 根因（证据驱动，全部亲跑或亲读；原始输出见台账）

### R1（根因，决定性）：`automationWakeEvent` 把 `needs_cleared` 当唤醒源，而它是协调者自身账务动作的产物

`internal/agentd/wakeconsumer.go:206` 的 `automationWakeEvent` 是唯一唤醒判据。`:237-242`：

```go
237: 	case ledger.EvNeedsCleared,
238: 		ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
239: 		return keystone.WakeEvent{
240: 			Kind: keystone.WakeTaskTerminal, Card: ev.CardID,
241: 			Summary: fmt.Sprintf("%s: %s", ev.Type, truncateRunes(string(ev.Payload), 400)),
242: 		}, true, nil
```

`EvNeedsCleared` 返回 `yes=true` ⇒ 唤醒。它为何留（先读契约的结论）：

- `docs/superpowers/specs/b353-contract.md:90` 条目 40 冻结「`needs_cleared` 产生 `keystone.WakeTaskTerminal`」；
- `docs/superpowers/specs/b353.md:104` 明写「本期把后两者（decision_opened/answered）补上，并**保持** `needs_cleared`」；
- `docs/superpowers/specs/b353-contract.md:125` §6-4 拍板「**过多时后续可收紧**；被否掉的方案是只保留 needs/decision opened，显式不做本期提前收紧」——B394 就是那次收窄；
- `docs/superpowers/specs/b358-contract.md:396` 规则 31「系统结构事件（入群/移出/归档/席位变更/needs_human）不唤醒任何人」**列了 `needs_human` 未列 `needs_cleared`**；
- `docs/superpowers/specs/b389-contract.md:193` §3.5.1 只把 `EvNeedsHuman`/`EvSeatBearingMissing` 移出映射，`needs_cleared` 原样留。

### R2（触发链）：协调者回合内清标 → 新事件 seq 落在本批 `maxProcessed` 之后 → 下一轮消费重读并唤醒

消费循环（`consumeAutomationEventsOnce`，`wakeconsumer.go:499`）在轮首一次性读事件、算出
`maxProcessed`，随后**同步**跑唤醒回合（`wakeCoordinatorRoundRaw`，`:674`）。回合内协调者
执行 `handoff card needs <id> --clear` 落的 `needs_cleared`（seq 更大），**不在**本轮
`automationSeen` 里、也大于 `maxProcessed` ⇒ 下一轮轮询读到它 → 唤醒 → 又清 → 又醒。

**红色回路亲跑**（`$TMPDIR` 隔离副本，走真实 `consumeAutomationEventsOnce`）：

```text
$ go test ./internal/agentd/ -run TestB394ClearNeedsDoesNotWake -count=1
--- FAIL: TestB394ClearNeedsDoesNotWake (0.26s)
    b394_echo_redtest2_test.go:30: 清标不应起新唤醒轮：processed=1 resumes=1（今天预期红）
FAIL

$ go test ./internal/agentd/ -run TestB394EchoLoopTwoSteps -count=1 -v
    b394_echo_redtest_test.go:81: 清标自身起轮数=3 resumes=3
--- PASS: TestB394EchoLoopTwoSteps (0.33s)
```

### R3（actor 不可判别）：清标的 actor 与真人清标同串，现有数据面无法做「自回声」来源比对

三种清标来源的 actor（亲读源码）：

- CLI `card needs --clear` → `ledgerActor()` = `cli:<USER>@<host>`（`cmd/ledgercli.go:44`，`cmd/card_records.go:87`）；
- Web 抽屉清标 → `web:<RemoteAddr>`（`internal/agentd/ledgerapi.go:589`）；
- 环节自动撤标 → `node:<节点名>`（`internal/ledgerstep/node.go:371`，`ClearNeedsHumanFrom`）。

协调者会话在回合内清标用的是 CLI 路径 ⇒ `cli:<user>@<host>`，**不携带席位身份**
（`cmd/card_seat.go` 的 `cli:<cli>#<session_id>` 只用于 bind/rebind/step/房间发送）。
且 `keystone.WakeEvent`（`internal/keystone/keystone.go:32-36`）**无 actor 字段**，
`Decide` 只看 kind + attach 态。⇒ 想「只压自回声、放行真人清标」需先改数据面（P1 丙）。

### R4（失败重打，分开归因）：`17717/17727` 的 `needs_human` 是 keystone 唤醒失败的产物，不是回声

真机 B382 账本只读读数（`handoff card show B382`，原始行见台账 §2.3）：

```text
17716 needs_cleared |actor= cli:root@handoff | {}
17717 needs_human   |actor= keystone          | {"reason": "协调者唤醒失败：resume 与重建均不可用"}
17719 decision_opened |actor= cli:root@handoff | {"body": "B382 修复路线裁决：..."}
17720 needs_human   |actor= keystone          | {"reason": "协调者唤醒失败：resume 与重建均不可用"}
17722 comment |「清等人标记后在 #17720 立刻被 keystone 重新打上…不再反复清标」
17725 comment |「本轮唤醒为 decision #10 的回声，无新输入。」
17726 needs_cleared |actor= cli:root@handoff | {}
17727 needs_human   |actor= keystone          | {"reason": "协调者唤醒失败：resume 与重建均不可用"}
17728 comment |「本轮唤醒 = 上轮 needs_cleared #17726 的自回声」
```

⇒ `needs_cleared`（`17726`→唤醒）与 `decision_opened`（`17719`→`17725`）双双实测自回声；
`needs_human` 是 keystone 在唤醒回合失败后重打的产物。**修回声 ≠ 修失败重打**：本卡只断
「清标→唤醒」这条边，`needs_human` 已被 B389 移出唤醒映射，不再引发新轮；失败重打
（resume/重建不可用）归 B393 面，本卡不动（spec §2「不做」）。

---

## 2. 分流决定

| 根因 | 归属 | 处置 |
|---|---|---|
| R1 回声缝（`needs_cleared` 在唤醒映射） | 本卡，L2 / `internal/agentd` | **T3**：移出唤醒映射 + **T2** 契约收窄 |
| R2 自生事件回灌（seq 在 `maxProcessed` 后） | 上一条的机制解释 | 随 T3 一并解决（去掉唤醒源即无回灌） |
| R3 actor 不可判别 → 无法做来源比对 | 跨子系统（CLI 注入席位 + keystone 字段） | **回 spec 重新定级**；本计划记为独立发现（P1 丙） |
| R4 keystone 失败重打 | 另一归因（B393） | 不在本卡；`needs_human` 已不唤醒，链自然断 |
| 兄弟回声 `decision_opened` | 另卡（P2） | 记独立发现，不并入 T1–T4 |
| 真机重放验收（spec §4-4） | 本 task 由协调者执行，不派发 | 见 §12 |

> 遵 spec §「一两行小修顺手修掉；架构级修复回本 spec 重新定级，不许在排查现场顺手动架构」。

---

## 3. 任务 DAG

```
T1（红色回路转正：TestB394ClearNeedsDoesNotWake 落仓，今天红）
  └→ T2（契约收窄：b394-contract 增量 + b358 规则31 / b353 条目40 指针）
        └→ T3（实现：automationWakeEvent 移出 EvNeedsCleared，T1 转绿）
              └→ T4（更新既有期望 + 不误伤回归 + 变异复验）

（独立）来源比对（P1 丙）、decision_opened 回声（P2）→ 回 spec / 另立卡，不在本 DAG
```

次序承重：T1 的回路必须先红，才能证明 T3 的护栏是它转绿的原因；T4 的既有期望更新
依赖 T3 改完 `wakeconsumer.go`（否则期望先改会红）；T4 的变异复验依赖 T3 的护栏在场。

---

## 4. 基线事实（实现卡共享，动手前复核）

**亲跑读数（原始输出见台账 §5）**：

- `go build ./...` → `BUILD_EXIT=0`；`go vet ./internal/agentd/` → `VET_EXIT=0`。
- `go test ./internal/agentd/ -run TestB353AutomationMapsCardActionEvents -count=1 -v`
  → `--- PASS`（`wakeconsumer_test.go:543` 断言 `processed==3`）。

**库行为事实（带出处）**：

- 消费轮每轮读取上限 500（`wakeconsumer.go:512` `EventsFromAsc(nil, from, 500)`）；
  轮首一次性读事件、轮内同步跑唤醒回合（`:512` / `:674`）——这是 R2 的机制出处。
- `needs_cleared` payload 恒为 `{}`（`internal/ledger/events.go:457`、`:491`）。

**现状签名与调用面（`codegraph sym` 命中，行号以实源为准；图覆盖债见台账 §1）**：

- `automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error)`
  — `internal/agentd/wakeconsumer.go:206`；
- `Server.consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error)`
  — `internal/agentd/wakeconsumer.go:499`（唯一调用者 `runAutomationPass`）。

---

## 5. 任务详情

### T1 红色回路转正：清标不得起新的唤醒轮（先红）

**动作**：新建测试文件 `internal/agentd/wakeconsumer_b394_test.go`，内容如下（完整，无占位）：

```go
// wakeconsumer_b394_test.go —— B394 回声自激的缝级回归回路。
//
// 职责：锁住「清标（needs_cleared）不是唤醒源」——清标是注意力平面的状态翻转，
// 与 needs_human 同族；若当唤醒源，协调者自己的账务动作会把它自己叫醒（清→重打乒乓）。
// 缝：Server.consumeAutomationEventsOnce（真实 ledger Facade + 真实 keystone + 假 runner），
// 不直调 automationWakeEvent。
// 边界：不复制 keystone briefing/重建规则；不测 card wait 展示面（另有既有测试）。
package agentd

import (
	"context"
	"testing"
)

// TestB394ClearNeedsDoesNotWake 锁 B394 冻结条目 1：needs_cleared 不唤醒协调者。
func TestB394ClearNeedsDoesNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	// 前置：needs_human 不唤醒（B389 已收口）。同时证明本回路不是「什么都没醒」的假绿——
	// 后面 decision_opened 支（T4）会给正例。
	if err := env.ledger.MarkNeedsHuman(cardID, "协调者回合失败兜底", "keystone"); err != nil {
		t.Fatal(err)
	}
	if processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil || escalated || processed != 0 {
		t.Fatalf("needs_human 不应唤醒：processed=%d escalated=%v err=%v", processed, escalated, err)
	}

	// 被测行为：清标（actor 用协调者 CLI 身份，与真机 cli:root@handoff 同串）不得起新唤醒轮。
	if err := env.ledger.ClearNeedsHuman(cardID, "cli:root@handoff"); err != nil {
		t.Fatal(err)
	}
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated {
		t.Fatalf("清标消费轮失败：processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	if processed != 0 {
		t.Fatalf("清标不得唤醒协调者（回声自激）：processed=%d", processed)
	}
	if _, resumes, _ := runner.snapshot(); len(resumes) != 0 {
		t.Fatalf("清标不得起 Resume 回合（回声自激）：resumes=%v", resumes)
	}
}
```

- **Interfaces**
  - Consumes（既有测试夹具，签名逐字）：`newNoPTYAutomationEnv(t *testing.T) (*ledgerEnv, *queueTraceRunner)`（`wakeconsumer_test.go:30`）、`createCoordCard(t *testing.T, env *ledgerEnv) string`（`coordapi_test.go:146`）、`prebindConsumerSession(t *testing.T, env *ledgerEnv, cardID string)`（`wakeconsumer_test.go:361`）、`(*queueTraceRunner).snapshot() (int, []string, []string)`（`scheddrain_test.go:53`）。
  - Produces：`TestB394ClearNeedsDoesNotWake`（本文件）。
- **步骤**
  1. 落文件。
  2. 跑红：`go test ./internal/agentd/ -run TestB394ClearNeedsDoesNotWake -count=1`
     → 预期 `--- FAIL ... 清标不得唤醒协调者（回声自激）：processed=1`（本节点已在副本实跑，原文见台账 §3）。把原文抄进实现台账。
- **测试范围声明**：只跑 `./internal/agentd/`（本 task 只新增测试文件）。

### T2 契约收窄：`needs_cleared` 移出唤醒映射（文档）

**动作**：新建 `docs/superpowers/specs/b394-contract.md`（增量，照 b389/b390-contract 形），并就地加指针。

**新建文件内容（完整）**：

```markdown
# B394 契约增量：needs_cleared 移出唤醒映射（回声自激收口）

**上游状态：已批准**（源：B394 spec §2 + 用户 2026-09-22 建卡决定）
**级别：L2 单子系统**（`internal/agentd` 唤醒判据面；不动 keystone 与路由）
**本增量触碰的冻结物**：`b358-contract.md` 规则 31（扩清单）、`b353-contract.md` 条目 40（取代）。

## 1. 判据收口

1. `automationWakeEvent` 把 `EvNeedsCleared` **移出唤醒映射**（返回 `yes=false`）；`needs_cleared` 不再是唤醒源。
   理由：清标是 `needs_human` 的状态翻转（注意力平面），不是可动作事实；唤醒它使协调者自己的
   账务动作把自己叫醒（B382 真机 17726 needs_cleared → 新一轮唤醒 → 17727 keystone 重打 needs_human）。
2. **保留**两条展示通路：`card wait` 对 `needs_cleared` 仍编码 stdout（b353 条目 15）；
   会话列表 `needsHumanByCard` 仍按 `needs_cleared` 翻灭标签。以这两条既有测试仍绿为准。
3. 不并入 keystone 的失败重打（17717/17727 是唤醒失败产物，另一归因）。
4. `EvDecisionOpened`/`EvDecisionAnswered` 的唤醒行为**本卡不动**（其合法唤醒面=真人开裁决；
   自回声需来源区分，另立卡/另审）。

## 2. 原子冻结条目（每条独立 pass/fail）

1. `automationWakeEvent(EvNeedsCleared)` 返回 `yes=false`；消费轮 `processed` 不因 needs_cleared 增加。
2. `needs_cleared` 事件仍被消费轮标 seen、游标照推（不堵流）。
3. `card wait` 对 `needs_cleared` 仍编码 stdout 行（b353 条目 15 不回归）。
4. 会话列表 `needsHumanByCard` 清白标后 `NeedsHuman` 翻 false（b358.2 测试不回归）。
5. 正常唤醒源不回归：`task_mirrored`（策略真）、真人寻址 `room_message`、`decision_opened`/`decision_answered` 仍能唤醒。
6. 变异复验：把 `EvNeedsCleared` 放回唤醒映射，`TestB394ClearNeedsDoesNotWake` 重新变红。
```

**就地指针（两处，逐字改）**：

- `docs/superpowers/specs/b358-contract.md:396`，把：

  ```
  31. 系统结构事件（入群/移出/归档/席位变更/needs_human）不唤醒任何人。
  ```

  改为：

  ```
  31. 系统结构事件（入群/移出/归档/席位变更/needs_human/needs_cleared）不唤醒任何人。
      （needs_cleared 由 B394 于 2026-09-22 补入；见 `b394-contract.md`。）
  ```

- `docs/superpowers/specs/b353-contract.md:90`，把：

  ```
  40. `needs_cleared` 产生 `keystone.WakeTaskTerminal`。
  ```

  改为：

  ```
  40. ~~`needs_cleared` 产生 `keystone.WakeTaskTerminal`。~~
      **（B394 取代，2026-09-22）`needs_cleared` 不再产生唤醒**：清标是 needs_human 的状态翻转，
      唤醒它会形成协调者自回声（清→重打的乒乓）。展示通路（条目 15）不变。见 `b394-contract.md`。
  ```

- **Interfaces**：无代码接口（纯文档）。
- **步骤**
  1. 落 `b394-contract.md`。
  2. 改两处指针（用 `git log -S` 确认 b389-contract 未另立相反条目：本节点已查 `b389-contract.md` 无 `needs_cleared` 命中）。
- **测试范围声明**：无（文档 task 不跑测试）。

### T3 实现：`automationWakeEvent` 移出 `EvNeedsCleared`

**文件**：`internal/agentd/wakeconsumer.go`，改 `:237-242`。

**改前**：

```go
	case ledger.EvNeedsCleared,
		ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
		return keystone.WakeEvent{
			Kind: keystone.WakeTaskTerminal, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", ev.Type, truncateRunes(string(ev.Payload), 400)),
		}, true, nil
```

**改后**（完整，替换上块）：

```go
	case ledger.EvNeedsCleared:
		// B394：清标是 needs_human 的状态翻转（注意力平面），与等人同族，不唤醒。
		// 唤醒它会让协调者自己的账务动作把自己叫醒（清→重打的回声乒乓，真机 B382
		// 17726 needs_cleared → 新一轮唤醒 → 17727 keystone 重打 needs_human）。
		// 展示通路（card wait / 会话列表 needsHumanByCard）不经本函数，不受影响。
		slog.Default().Debug("needs_cleared 不唤醒：清标是注意力平面状态翻转，防回声自激",
			"seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"reason", "needs_cleared_not_actionable")
		return keystone.WakeEvent{}, false, nil
	case ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
		return keystone.WakeEvent{
			Kind: keystone.WakeTaskTerminal, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", ev.Type, truncateRunes(string(ev.Payload), 400)),
		}, true, nil
```

- **Interfaces**
  - Consumes：`proto.LedgerEvent`（`internal/proto/ledger.go:120`）、`ledger.EvNeedsCleared`（`internal/ledger/types.go:64`）、`keystone.WakeEvent`（`internal/keystone/keystone.go:32`）。
  - Produces：无签名变更；`automationWakeEvent` 行为对 `EvNeedsCleared` 由 `yes=true` 变 `yes=false`。
- **步骤**
  1. 判据先在基线跑（复核）：T1 已红（原文在台账 §3）。
  2. 改代码块。
  3. 跑绿：`go test ./internal/agentd/ -run TestB394ClearNeedsDoesNotWake -count=1` → 预期 `--- PASS`（本节点已在副本实跑，原文见台账 §4）。
  4. 跑编译与静态检查：`go build ./...`、`go vet ./internal/agentd/`（预期 `EXIT=0`）。
- **加关键节点日志**：新分支加一条 `Debug`（如上），带 `seq`/`card`/`type`/`reason`——与
  `:214` 的 `task_mirrored` 策略过滤同款（`slog.Default().Debug`）。**成功路径不静默**：消费循环
  对新事件的既有 `Debug("自动化事件标记 seen", ... reason=not_actionable)`（`:599`）照常打。
  不新增 Error 分支（本路径非错误）。
- **加注释**：新分支的「为什么」（回声乒乓 + 来源证据 + 展示通路不受影响）已写进代码注释；
  函数头注释（`:202-205`）不动（它描述本函数边界，仍准确）。
- **测试范围声明**：只跑 `./internal/agentd/`。

### T4 更新既有期望 + 不误伤回归 + 变异复验

**文件**：`internal/agentd/wakeconsumer_test.go`，改 `:539-549`。

**改前**：

```go
	// B389 判据收口（契约 §3.5.1）：needs_human 移出唤醒清单——processed 4→3，
	// 但 needs_cleared/decision_opened/decision_answered 仍唤醒。
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 3 {
		t.Fatalf("卡原生动作消费 processed=%d escalated=%v err=%v，want 3/nil/false", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("卡原生动作应合并一次 Resume，实得 %d", len(resumes))
	}
	for _, want := range []string{"needs_cleared", "decision body", "answer"} {
```

**改后**（完整，替换上块）：

```go
	// B389 判据收口（契约 §3.5.1）移出 needs_human；B394（b394-contract 条目 1）再移出
	// needs_cleared——processed 4→3→2，只剩 decision_opened/decision_answered 唤醒。
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 2 {
		t.Fatalf("卡原生动作消费 processed=%d escalated=%v err=%v，want 2/nil/false", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("卡原生动作应合并一次 Resume，实得 %d", len(resumes))
	}
	// decision_opened/decision_answered 仍唤醒（B394 不动它们）；needs_cleared 不再唤醒。
	for _, want := range []string{"decision body", "answer"} {
```

**不误伤回归（跑既有测试，不新增）**：本 task 跑：

```text
go test ./internal/agentd/ -count=1
go test ./cmd/ ./internal/collab/... ./internal/ledger/... ./internal/ledgerstep/ ./internal/keystone/ -count=1
```

本节点已在副本实跑：`internal/agentd` 仅 `TestB353AutomationMapsCardActionEvents` 红（即本 task 要改的那支），
其余包全绿（原文见台账 §4）。`cmd` 绿 ⇒ `card wait` 条目 15 不回归；`collab` 绿 ⇒ 会话列表
`needsHumanByCard` 不回归。

**变异复验（手动，不留代码）**：

1. 临时把 `ledger.EvNeedsCleared` 放回 T3 的唤醒 case（改回 `yes=true`）；
2. `go test ./internal/agentd/ -run TestB394ClearNeedsDoesNotWake -count=1` → **预期重新红**
   （本节点已证基线=红：台账 §3 原文 `processed=1`）；
3. 撤回临改，确认工作树只剩 T3 的正式改动。

- **Interfaces**：无新增；改既有断言期望值。
- **测试范围声明**：`./internal/agentd/ ./cmd/ ./internal/collab/... ./internal/ledger/... ./internal/ledgerstep/ ./internal/keystone/`。

---

## 6. 缺陷族对抗审查（defect-families 五族 + 追加设问）

**族 1 生命周期/状态机中断**
- 消费轮中途 agentd 重启：`needs_cleared` 已标 seen / 游标已推，不会因重启重复唤醒（既有
  `automationSeen` + 终局前缀水位机制，本卡不改）。T3 只删一个事件类型的唤醒，不新增运行时资源。
- **无其他**——不新增状态机状态、工单或进程。

**族 2 静默失败 / 误导报错**
- 删掉唤醒不是「静默失败」：清标本来就只是状态翻转，不唤醒是**期望结果**；且新分支有 `Debug` 日志
  （`reason=needs_cleared_not_actionable`），`card wait` 展示面仍输出该事件（可用 `show` 对质）。
- 反例保护：若误删了 `EvDecisionOpened`/`EvDecisionAnswered`，`TestB353AutomationMapsCardActionEvents`
  的 `want 2` 会红（T4 期望里只留 decision 两条）——不会把两种事件混为一谈。

**族 3 跨平台假设**
- 不引入平台假设；改动是纯 switch case 删除。**无**。

**族 4 假红 / 假绿测试**
- T1 回路走真实 `consumeAutomationEventsOnce` + 真实 ledger + 真实 keystone（假 runner 只记 Resume），
  非只测映射函数。
- T1 的「前置 needs_human 不唤醒」+ T4 的 `decision` 正例共同防「回路永远绿」的假绿：若有人把整个
  `automationWakeEvent` 改成恒不唤醒，T4 的 `want 2`/briefing 含 `decision body` 会红。
- 变异复验（T4 步骤）证明护栏可红。

**族 5 门禁绕过**
- 唤醒判据单一入口 `automationWakeEvent`（`:206`）；没有第二条把 needs_cleared 映射成唤醒的路径
  （亲证 `grep -rn EvNeedsCleared internal/agentd/ --include=*.go` 只此一处）。`card wait`
  （`cmd/card_wait.go:235`）与会话列表（`internal/collab/sessions.go:439`）是**展示**面，
  不经 `automationWakeEvent`，改其一不影响另一——T4 两包全绿即证。
- **无绕过面**。

**追加设问一：序列化边界**——本卡**不新增数据字段**，不改 `needs_cleared` payload（恒 `{}`，
`events.go:457/491`），不改任何 DTO/tag。故无新增手写投影点；唯一跨界是 `automationWakeEvent`
的 `ev.Type` 判断，已被 T1 缝级测试穿过真实 `LedgerEvent` 解码。**无风险，因为**删除的是
事件类型的分类，不是字段读写。

**追加设问二：枚举新值过既有白名单**——`EvNeedsCleared` 是既有常量（`types.go:64`），无新增枚举，
不触白名单。**无风险**。

**追加设问三：承重安全属性有测试锁住**——本卡的承重属性是「回声不再自激」+「展示面不回归」：
前者 T1 锁，后者 T4 的 cmd/collab 全绿锁；变异复验给前者可红证据。

---

## 7. 上下文预算检查

有界文件集（圈得出）：`internal/agentd/wakeconsumer.go`、`internal/agentd/wakeconsumer_b394_test.go`（新）、
`internal/agentd/wakeconsumer_test.go`、`docs/superpowers/specs/b394-contract.md`（新）、
`docs/superpowers/specs/b358-contract.md`、`docs/superpowers/specs/b353-contract.md`。
不越出 `internal/agentd` 与 docs/。**通过**。

## 8. 类型标注 / 边界型子系统

非边界型子系统（单进程内决策表）。行为验收仍以显式真机清单给出（§12，本 task 由协调者执行）。

## 9. 接缝覆盖（双向，对照 spec 测试决定的接缝清单）

spec 的接缝 = **agentd 唤醒消费缝**（`Server.consumeAutomationEventsOnce`）。

- **测试 → 缝**：`TestB394ClearNeedsDoesNotWake`（T1）入口调用符号 = `env.srv.consumeAutomationEventsOnce(...)`，
  在缝上；它不直调 `automationWakeEvent`。T4 改的 `TestB353AutomationMapsCardActionEvents` 入口同缝。
- **缝 → 测试**：该缝被 T1 的缝级断言锁住（清标不唤醒）；T4 的既有测试同缝锁「decision 仍唤醒 + 展示不回归」。
- **内部锁**：无。不存在「入口不在缝上」的测试。**通过**。

## 10. 占位符扫描

- 无 TBD / 「加适当错误处理」/ 「同 Task N而略」；T1–T4 的代码块与文档块均完整可抄。
- **例外声明（无）**：本计划不依赖「形态因包而异」的夹具复用，全部测试代码写全，故不申请骨架测试例外。
- 条件退路：无（T4 的变异复验是显式步骤，不改变任何测试的入口符号）。

## 11. 跨 task 类型/签名一致性

- T1 Produces `TestB394ClearNeedsDoesNotWake`；T3 不改函数签名（只改行为），Consumes 逐字引用：
  `automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error)`；
  T4 改的断言函数入口 `consumeAutomationEventsOnce(ctx context.Context) (int, bool, error)` 与既有逐字一致。
- T2 文档改的两行在 T4 的测试注释里被逐字引用（b353 条目 40 / b358 规则 31），互不矛盾。

## 12. 真机清单（归协调者执行；本 task 由协调者执行，不派发）

1. 升级带 T3 改动的 agentd 到 B382 所在机器后，对 B382 重放「清标」动作
   （`handoff card needs B382 --clear`）：账本**不再**出现新的唤醒轮（无新的 `wake_round:start`
   注释、无新 Resume），`needs_cleared` 事件仍在账本且 `card wait`/会话列表仍可见（spec §4-4）。
2. 对照：在 B382 上发一条真人寻址消息（`handoff session` 房间 @该协调者席位），确认协调者**仍能**被唤醒
   （spec §4-3 不误伤）。

> 真机需要在跑有 T3 改动的 agentd 的机器上执行，且依赖 B382 的席位/承载现状；机内夹具验不了
> 「真机账本不再出现唤醒轮」，故单列交协调者。

## 图覆盖债（本节点发现，记入台账 §1）

- `codegraph context` 不接受中文领域词；最优树无「唤醒判据」领域，未命中。
- `codegraph sym/who-calls/chain` 对 `automationWakeEvent` / `consumeAutomationEventsOnce`
  返回的 line 与函数体是 B358.3 前**旧版**（基线快照陈旧）；行号与体一律以实读源码为准。
- `codegraph flow` degraded（基线无 flows 段），按纪律读源码，不用 chain 冒充 flow。
