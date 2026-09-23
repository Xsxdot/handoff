# B380 plan：关单镜像断流修复——文档收口（S1）

> 卡 B380 · 入口节点 charter:plan · contract `docs/superpowers/specs/b380-contract.md`（冻结 `2a5378ab`）
> breakdown `docs/superpowers/specs/b380-breakdown.md`（已拍板）· spec `docs/superpowers/specs/b380.md`
> 基线分支 `cards/B233.1-charter-7`；本节点工作分支 `cards/B380-charter-3`，起手 HEAD `64b4c455`。
> 凡引用行号者动手前重核，漂了以符号/引文为准。
> 台账 `docs/superpowers/ledgers/2026-09-23-b380-plan-ledger.md`（含亲跑命令、原始输出、图查询记录）。
> **本节点只出计划，不写实现。**「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。

---

## 0. 待拍板清单

**无新增岔口。** 本卡的四项岔口 P1–P4 已由协调者在 breakdown §8 逐项拍板并回填：

| 编号 | 裁定 | 对 plan 的约束 |
|---|---|---|
| P1 | **甲：单轮闭合，不再开实现子卡** | 运行时代码（三发布点 + `OpenTickets` 终态关单）已随 contract Ticket 0 冻结落地，本 plan **不含任何 Go 代码 task**；只做「复核其在场」的门禁。 |
| P2 | **乙：S1 并入 implement 节点收尾，不单独建卡** | 本 plan 的唯一实现 task 就是 S1 文档同步；有界文件集照 breakdown §3.1④：`skills/handoff/SKILL.md` + `docs/roadmap.md`（+ 可选 `README.md`）。 |
| P3 | **甲：spec 落点维持 `origin/cards/B380-charter-1@117bbd5a`；合并时由协调者把 spec 两提交并进合并目标** | 执行者**不得**搬运 spec、不得改 `docs/superpowers/specs/b380.md`（本工作树里它不存在）；该动作归协调者 finish。 |
| P4 | **甲：不补机内端到端回归** | 不新增 `internal/agentd`/`internal/ledgermirror` 集成测试；跨机端到端归协调者真机清单（§11）。 |

执行者若认为需要越出上表（例如扩到 `README.zh-CN.md`），**停下提问，不自作主张**（发现见 §7 残余）。

---

## 1. 问题与现状（证据驱动；原始读数见台账）

### R1：运行时代码已由 contract Ticket 0 冻结落地，本卡剩余的唯一实现面是文档同步

spec §1 的四环根因链已由 contract §1 冻结三处代码增量（本节点已亲验在位，见 R4）：

- C-1a 人工 reply 发布：`internal/orchestration/facade.go#Manager.AnswerTicket`
- C-1b Manager 中介审批者发布：`internal/orchestration/manager.go#Manager.approvePermission`
- C-1c approval 面审批者发布（经既有 `Hooks.Hub`）：`internal/approval/client.go#Client.consult`
- C-2 投影层终态关单：`internal/ledger/taskstate.go#Store.OpenTickets`

⇒ P1=甲，本卡**没有可扇出的代码实现 task**。plan 的代码侧工作只剩「复核冻结实现在场且绿」（T1 门禁）。

### R2：spec §5 明确要求事件交付性文档随代码同步

spec §5 原文（`git show 117bbd5a:...b380.md`）：**「`proto.go` 事件类型注记随代码修订……README / handoff skill 的事件表若标注了交付性，实现时逐一核对同步。」** `proto.go` 注记已随 contract 骨架修订（本节点已读，见 R4）；README 与 handoff skill 尚未同步——这是 S1 的契约依据。

### R3：现状文档把 `ticket_answered` 与「只入库」事件并列，C-1 后该措辞不再准确

- `skills/handoff/SKILL.md:94-98`：把 `progress / approver_decision / approver_disabled / tickets_voided / ticket_answered / permission_auto_allow / permission_reuse` **七类**并列写「**不会**唤醒 `wait`（只入库）」。C-1 后 `ticket_answered` **会 Publish 进实时流**（供账本镜像关单），「只入库」对它已失真。
- `README.md:433-439`：把「same seven audit types」并列描述为在应用消费端被过滤（交付过滤）。该语义本身仍准确（`WaitDeliveryPolicy` 冻结 `false`），但 `ticket_answered` 现在**发布但不可交付**，需要一句澄清以防被读成「只入库」。
- `docs/roadmap.md`：spec §7 / contract §5 的弃选项「镜像 watermark 逐 seq 连续性对账」**尚未落任何条目**（本节点亲跑 `grep -nE '逐 seq|watermark.*对账' docs/roadmap.md` → 无命中，exit 1）。
- `README.zh-CN.md:247`（`progress`、审批链审计事件只入库不唤醒）：同族表述，但**不在** breakdown §3.1④ 圈定的有界文件集内；本 plan 不动，记入 §7 残余。

### R4：冻结实现在场 + 全绿（本节点亲验，原始输出见台账）

| 事实 | 命令 | 本节点读数 |
|---|---|---|
| 发布点 a | `grep -n "TicketAnswered\|hub.Publish\|AppendEvent" internal/orchestration/facade.go` | `:127` `AppendEvent(...EventTypeTicketAnswered...)`、`:133` `m.hub.Publish(evt)` |
| 发布点 b | `grep -n "func (m \*Manager) approvePermission\|EventTypeTicketAnswered" internal/orchestration/manager.go` | `:2554` 函数、`:2577` `AppendEvent` |
| 发布点 c | `grep -n "ticketAnsweredPayload\|Hooks.Hub\|EventTypeTicketAnswered\|func (c \*Client) consult" internal/approval/client.go` | `:314` `consult`、`:430` `AppendEvent`、`:551` 私有 payload |
| 投影面 | `grep -n "func (s \*Store) OpenTickets\|case evTicketsVoided" internal/ledger/taskstate.go` | `:147`、`:188` `case evTicketsVoided, "completed", "failed", "archived":` |
| 注释 | `sed -n '95,120p' internal/proto/proto.go` | `EventTypeTicketAnswered` 注记已改述为「**会 Publish**（B380）……但在客户端不可交付」 |
| 编译 | `go build ./...` | `BUILD_EXIT=0` |
| 投影测试 | `go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1` | `ok ... 0.613s` |
| 发布测试 a/b | `go test ./internal/orchestration/ -run TestB380 -count=1` | `ok ... 0.307s` |
| 发布测试 c | `go test ./internal/approval/ -run TestB380 -count=1` | `ok ... 0.101s` |
| 图门禁 | `codegraph --repo . check` | `EXIT=0`（`assignedContainers 338 = viewContainers 338`） |

---

## 2. 分流决定

| 事项 | 归属 | 处置 |
|---|---|---|
| 三发布点 + `OpenTickets` 终态关单（Go） | 已冻结（contract Ticket 0） | **零改动**：T1 只复核在场且绿；执行者**不得**改任何 `internal/**` 生产代码。 |
| `skills/handoff/SKILL.md` 交付性措辞 | 本卡 / 文档面（图外） | **T2**：把 `ticket_answered` 从「只入库」七类里拆出，写「不唤醒但会 Publish 进实时流供账本镜像关单」。 |
| `README.md` 七类 audit types 段 | 本卡 / 文档面 | **T2**：追加一句澄清（发布给账本镜像、仍不可交付），不改「filters」语义。 |
| `docs/roadmap.md` 弃选项落账 | 本卡 / 文档面 | **T2**：新增 B380 小节（watermark 逐 seq 对账 + `tickets_voided` 发布语义两条）。 |
| `proto.go` 注记 | 已完成（contract） | **零改动**。 |
| `README.zh-CN.md:247` | **不在本卡有界文件集** | **不做**，记 §7 残余；要动须另走流程。 |
| spec 文件落点/合并 | 协调者 | 见 P3；执行者不碰。 |
| 真机/跨机端到端 | 协调者 | 见 §11；**本 task 由协调者执行，不派发**。 |

---

## 3. 任务 DAG

```
T1（门禁·无改动）：复核冻结实现在场 + 三包子集测试绿 + 图门禁绿
      └→ T2（实现·文档面）：三处文档同步（SKILL.md / README.md / roadmap.md），
            断言脚本预写（下写会红：SKILL/README/roadmap 当前无目标措辞）
            └→ T3（收口）：断言脚本转绿 + 变异复验（回退 SKILL 措辞应复红）+ 回归
```

- **最薄路径条（免除声明）**：本卡要锁的运行时行为（三发布点发布 + 终态关单）**今天从声明缝调用已得到断言的预期结果**（R4：三支冻结算例本轮全绿）。故按「写下去就会绿的才免除」，不设点亮该行为的最薄路径 task。S1 是纯文档面，无运行时代码可红。
- **次序承重**：T1 是 T2 的入场门（代码不在场则整卡前提失效，须停下提问）；T3 的变异复验依赖 T2 的文档改动在场。

---

## 4. 基线事实（实现卡共享；原始输出见台账）

**亲跑读数（本节点，工作树 `64b4c455`，go1.26.1 linux/amd64）**：见 R4 表。三段测试命令逐条单独跑（未用 `-p N` 并行多包，避开 B233.16 记录的多包争抢假红）。

**文档基线 grep（本节点亲跑）**：

- `grep -n '只入库' skills/handoff/SKILL.md` → `96:...（只入库）...`（唯一命中）。
- `grep -nE '逐 seq|watermark.*对账' docs/roadmap.md` → 无命中，exit 1。
- `grep -n 'published to the live' README.md` → 无命中。
- `grep -n 'ticket_answered' README.md` → `435:...ticket_answered...`；`README.zh-CN.md:247` 不点名该事件。

**既有测试（S1 不改代码，故为回归基线）**：

- `TestOpenTicketsTerminalMirrorClosesAll`、`TestOpenTicketsTerminalBeforeAnswerDoesNotResurrect` — `internal/ledger/taskstate_test.go:174/213`。
- `TestB380AnswerTicketPublishes`、`TestB380ApprovePermissionPublishes` — `internal/orchestration/b380_publish_test.go:40/68`。
- `TestB380ConsultApprovalPublishesTicketAnswered`、`TestB380ConsultApprovalNilHubNoPanic` — `internal/approval/client_test.go:655/681`。

**已知既有红（非本卡引入，基线即红，不修）**：`go test ./internal/client/ -run TestProductionHTTPClientCallersAreGatewayOnly`（报 `internal/agentd/drop.go`，B272 遗留）；`codegraph validate` 的两条他分支视图问题（`cards-B272-charter`、`cards-B374-charter`）。来源：contract §6。

---

## 5. 接口契约

### Consumes（S1 只用文件路径与纯文本，无 Go 符号）

| 消费物 | 精确路径 | 用途 |
|---|---|---|
| handoff skill 交付性段 | `skills/handoff/SKILL.md:94-98` | 改写「七类只入库」句 |
| README 事件表段 | `README.md:433-439` | 追加澄清句 |
| roadmap 尾部 | `docs/roadmap.md`（追加到文件末尾的 `## 来自 …` 序列） | 新增 B380 小节 |
| 冻结的代码事实 | R4 表所列文件/行 | T1 门禁复核依据 |

### Produces（本卡新增的仓内文件/文本）

```text
docs/superpowers/plans/b380-plan.md                         （本节点，已落）
docs/superpowers/ledgers/2026-09-23-b380-plan-ledger.md      （本节点台账）
（implement 节点产出：）
skills/handoff/SKILL.md        —— 六类只入库 + ticket_answered 单独段（§6 T2 改动一）
README.md                      —— 追加 published-to-live 澄清句（§6 T2 改动二）
docs/roadmap.md                —— 新增「来自 B380 spec」小节（§6 T2 改动三）
docs/superpowers/ledgers/2026-09-23-b380-implement-ledger.md （实现轮台账，命名随实现节点）
```

**无新增 Go 符号、无新字段、无 DTO/wire/JSON tag 变化、无新命令。**

---

## 6. 任务详情

### T1 门禁：复核冻结实现在场（无改动）

**动作**：逐条跑 R4 表的命令，逐条把原始输出抄进实现轮台账。

**步骤**：

1. `go build ./...` → 预期 `BUILD_EXIT=0`。
2. `go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1` → 预期 `ok`。
3. `go test ./internal/orchestration/ -run TestB380 -count=1` → 预期 `ok`。
4. `go test ./internal/approval/ -run TestB380 -count=1` → 预期 `ok`。
5. `codegraph --repo . check` → 预期 `EXIT=0`。
6. `grep -n "hub.Publish" internal/orchestration/facade.go` 等 R4 的四个 grep → 预期命中非空。

**失败处置**：任一条与预期不符，或冻结实现片段缺失 ⇒ **停下提问，不自行补实现**（P1=甲：代码已冻结，缺失是上游事故，执行者无权重开代码面）。请求格式：单行 JSON `{"ask":"..."}`。

- **Interfaces**：Consumes = R4 表；Produces = 台账原始输出。
- **测试范围声明**：只跑上述三条子集命令 + `go build`（全量测试不属于本 task）。
- **日志/注释步骤**：**不适用**，T1 零代码改动（无可观测性面）。这是本 task 的唯一豁免理由：它不改任何文件。
- **红绿周期**：**不适用**，见 §3「最薄路径条（免除声明）」。

### T2 实现（文档面）：三处交付性文档同步

> **本 task 是纯文档改动，无 Go 生产代码。** 下述「关键节点日志」「导出函数注释」两步骤按纪律对实现类 task 的要求**不适用**——它们锚定的是可观测运行时代码，本 task 不产生任何运行时行为。豁免理由：改动物是三份 Markdown 的叙述文本，不存在 logger 与导出函数。此豁免在 §12 显式声明。

#### 改动一：`skills/handoff/SKILL.md:94-98`（交付性措辞）

**改前（现状原文，5 行）**：

```text
`progress` / `approver_decision` / `approver_disabled` / `tickets_voided` /
`ticket_answered` / `permission_auto_allow` / `permission_reuse` 七类事件**不会**唤醒
`wait`（只入库）。任务流的集合外全是可动作事件，包括 `delivery_failed`、`stalled`、
`approval_dropped`、`archived` 和压力告警；`delivery_failed` 要去
`handoff resume <task>`。审计事件仍可在 `show` 的事件历史里对质。
```

**改后（逐字替换为）**：

```text
`progress` / `approver_decision` / `approver_disabled` / `tickets_voided` /
`permission_auto_allow` / `permission_reuse` 六类事件**不会**唤醒 `wait`（只入库）。
`ticket_answered` 同样**不会**唤醒 `wait`，但它**会 Publish 进实时流**（B380）：
它要进事件流供账本镜像投影关单，只是客户端不可交付——「不唤醒」不等于「只入库」。
任务流的集合外全是可动作事件，包括 `delivery_failed`、`stalled`、
`approval_dropped`、`archived` 和压力告警；`delivery_failed` 要去
`handoff resume <task>`。审计事件仍可在 `show` 的事件历史里对质。
```

**为什么**：`ticket_answered` 的「不唤醒」由客户端交付策略（`WaitDeliveryPolicy`）承接，与「是否 Publish」正交；C-1 后它进实时流供镜像关单，继续与 `tickets_voided` 等真·只入库事件并列会误导排障者去 `show` 找「为什么没入库」。

#### 改动二：`README.md:433-439`（追加澄清句）

**改前（现状原文，7 行）**：

```text
Task wait/follow filters the same seven audit types at the application consumer:
`progress`, `approver_decision`, `approver_disabled`, `tickets_voided`,
`ticket_answered`, `permission_auto_allow`, and `permission_reuse`. Every other
existing task event—including `delivery_failed`, `stalled`, `approval_dropped`,
`archived`, and pressure alerts—is actionable; `delivery_failed` means run
`handoff resume <task>`.
```

**改后（在末行后追加一段，前七行逐字不动）**：

```text
Task wait/follow filters the same seven audit types at the application consumer:
`progress`, `approver_decision`, `approver_disabled`, `tickets_voided`,
`ticket_answered`, `permission_auto_allow`, and `permission_reuse`. Every other
existing task event—including `delivery_failed`, `stalled`, `approval_dropped`,
`archived`, and pressure alerts—is actionable; `delivery_failed` means run
`handoff resume <task>`. Note `ticket_answered` is still published to the live
event stream so the ledger mirror can close tickets (B380); it stays
non-deliverable to wait — filtered here, not at the source.
```

**为什么**：「filters the same seven audit types at the application consumer」描述的是**交付过滤**，本身正确；但 `ticket_answered` 已不再是「只在 store 里」的审计类，加一句点明「发布给镜像、仍不可交付、过滤在消费端而非源头」，与 `proto.go` 注记和 SKILL.md 措辞三处一致。breakdown §3.1③ 判据：README **不得**出现「`ticket_answered` 只入库」等价表述。

#### 改动三：`docs/roadmap.md`（追加文件末尾的 B380 小节）

**动作**：在文件末尾（当前最后一段是「来自 B400 spec / review」小节）之后另起一节，追加：

```markdown
## 来自 B380 spec（2026-09-23，本期不做、后续要做）

- **镜像 watermark 逐 seq 连续性对账（补洞重拉）**：水位 = `MAX(source_seq)` 会跳过缺口，缺口以下的关单类审计事件永久丢失；B380 用投影层终态关单兜住可见后果，未根治整族。来源：`docs/superpowers/specs/b380.md` §3.3；`b380-contract.md` §5。
- **`tickets_voided` 发布语义修订**：投影层终态关单已覆盖其全部投影职责，多发布一种审计事件无增量收益。来源：`docs/superpowers/specs/b380.md` §3.3。
```

**为什么**：spec §7 / contract §5 明示「本期不做、后续要做」的弃选项必须落 `docs/roadmap.md`（否则成孤儿）。验收判据要求 `grep -nE '逐 seq|watermark.*对账' docs/roadmap.md` 命中。

- **Interfaces**
  - Consumes：§5 三份文档路径。
  - Produces：三份文档的新文本（逐字如改动一/二/三）。
- **步骤**
  1. 逐字落改动一（SKILL.md）。
  2. 逐字落改动二（README.md，只追加，不改前七行）。
  3. 逐字落改动三（roadmap.md，追加到末尾）。
  4. 跑 T3 的断言脚本 → 预期全命中。
- **测试范围声明**：无测试代码；判据是 T3 的 grep 断言脚本 + T1 的代码回归（因为本 task 不动代码，回归必须仍绿）。
- **关键节点日志 / 注释**：**不适用**（纯文档，无运行时与代码）。
- **有界文件集**：`skills/handoff/SKILL.md`、`README.md`、`docs/roadmap.md`——恰好三份，不得扩展。

### T3 收口：断言脚本 + 变异复验 + 回归

**断言脚本（实现轮落 `$TMPDIR`，不入仓；逐条可判 pass/fail）**：

```bash
set -e
cd <repo>
# 1. SKILL.md：ticket_answered 被单列，且「六类」措辞在场
grep -q '六类事件\*\*不会\*\*唤醒' skills/handoff/SKILL.md
grep -q '`ticket_answered` 同样\*\*不会\*\*唤醒' skills/handoff/SKILL.md
grep -q '会 Publish 进实时流' skills/handoff/SKILL.md
# 2. SKILL.md：没有任何一行把 ticket_answered 与「只入库」并列
! grep -nE '`ticket_answered`.*只入库' skills/handoff/SKILL.md
# 3. README.md：澄清句在场
grep -q 'published to the live' README.md
# 4. roadmap.md：弃选项条目在场
grep -qE '逐 seq|watermark.*对账' docs/roadmap.md
echo "B380_S1_ASSERT_OK"
```

预期：打印 `B380_S1_ASSERT_OK`，exit 0。

**变异复验（手动，不留代码）**：

1. 把改动一临时改回「七类……`ticket_answered`……（只入库）」→ 断言 1/2 重新红（`六类` 与单列句消失）。撤回。
2. 删除改动三的 roadmap 小节 → 断言 4 重新红。撤回。
3. 确认工作树只剩三份文档的正式改动（`git status` 无其它）。

**代码回归（确认 S1 不误伤）**：

```bash
go build ./...                                                          # 预期 exit 0
go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1        # 预期 ok
go test ./internal/orchestration/ -run TestB380 -count=1                # 预期 ok
go test ./internal/approval/ -run TestB380 -count=1                     # 预期 ok
```

- **测试范围声明**：三份文档 grep 断言 + 三包子集回归；不跑全量。
- **变异复验属显式步骤，不改任何测试的入口符号** ⇒ 无「条件退路」闸议题。

---

## 7. 缺陷族对抗审查（逐族正面回答）

覆盖面 = S1 三份文档（本 task 中唯一改动面）。运行时代码已冻结、上一轮 contract 已审，本 plan 不重复其缺陷族（R4 只复核在场）。

**族 1 生命周期/状态机中断**：**无，因为**不触运行时代码；文档改动无进程/资源生命周期。
**族 2 静默失败 / 误导报错**：文档同步的**反面**正是本 task 要消除的「误导」——旧措辞让人以为 `ticket_answered` 只在 store 里。新措辞显式点明「会 Publish 但不可交付」，可行动面（排障者该去 `/ws/events` 还是 `show`）不再歧义。
**族 3 跨平台假设**：**无，因为**纯 Markdown 文本，无平台分支。
**族 4 假红 / 假绿测试**：本 task 的「测试」是 grep 断言脚本；假绿防护 = T3 变异复验（回退文档应复红）与 `! grep` 反面断言（防「只入库」与 `ticket_answered` 并列）。变异 1/2 给出可红证据。
**族 5 门禁绕过**：**无**，不新增写路径/执行路径/命令；`card update` 等门禁不受影响。
**族 6 序列化边界**：**无新增字段、无 DTO/wire/tag**；唯一跨界是文档文本本身，由 grep 判据锁住。
**族 7 枚举新值过既有白名单**：**无新枚举值**；`ticket_answered` 字面值不变。
**族 8 承重安全属性有测试锁住**：本 task 不引入安全属性。承重的运行时属性（发布 + 关单）由 contract 冻结的 6 支测试锁住（T1 复核）。
**族 9 webview / 平台差异**：**无，因为**不触 `d_web`/Wails/浏览器 API。

**残余风险（明示，不藏）**：

- **`README.zh-CN.md:247` 同族表述未同步**：该行写「`progress`、审批链审计事件只入库不唤醒」，不点名 `ticket_answered`、且不在 breakdown §3.1④ 的有界文件集内。本 plan 不动它（越界）；若协调者认为中文 README 也须同步，应在派发前把 `README.zh-CN.md` 显式加入文件集并同步判据，否则记为后续文档卡。**执行者不得自行扩围。**
- **文档措辞是非布尔验收**：grep 判据只锁关键短语在场/缺席，不能证明整段叙述在人读层面完全无歧义；这是文档类 task 的固有边界，不假装可机检。

---

## 8. 上下文预算检查

有界文件集（圈得出）：

- 实现改动：`skills/handoff/SKILL.md`、`README.md`、`docs/roadmap.md`（3 份）。
- 只读复核：R4 表所列 4 个 Go 文件（`facade.go`/`manager.go`/`client.go`/`taskstate.go`）+ `internal/proto/proto.go`。
- 本节点产出：`docs/superpowers/plans/b380-plan.md`、`docs/superpowers/ledgers/2026-09-23-b380-plan-ledger.md`。

不越出上述集合；**不触及 `internal/**` 生产代码**。**通过。**

---

## 9. 类型标注 / 边界型子系统

本 task 非边界型子系统（不产生 wire/HTTP/WS 面、无 DTO）。**不适用**真机清单作为行为验收（真机归 §11 卡级 acceptance）。

---

## 10. 接缝覆盖（对照 spec §6 接缝清单）

spec §6 只有两条缝，**都是运行时代码缝**：

- 缝 1 = `ledger.Store.OpenTickets`（投影主缝）；
- 缝 2 = 三个发布点（`Manager.AnswerTicket` / `Manager.approvePermission` / `approval.Client` 审批者批准）。

两条缝已由 contract 冻结的 6 支测试逐条锁住（T1 复核绿），**本 plan 不新增缝级断言**——因为本 task 不产生任何运行时代码。

- **测试 → 缝**：S1 的 grep 断言入口是文件文本，**不落在任何缝上**（文档不是调用符号）。这不构成「内部锁顶替缝级断言」，因为 S1 不是实现类 task：它无行为、无入口符号，从声明缝构造不出针对它的断言（**缝管运行时事件，文本同步不在缝的事件面上**）。此豁免在 §12 显式声明。
- **缝 → 测试**：缝 1、缝 2 各由 contract §4 的冻结算例锁住（T1 复核），S1 不解除任何锁。
- **内部锁**：无（无新测试）。
- **条件退路**：无（T3 变异是显式步骤）。

---

## 11. 真机清单（归协调者执行；**本 task 由协调者执行，不派发**）

承 breakdown §6 / contract §5，P4=甲（不补机内端到端回归），跨机行为一律真机：

1. **存量自愈**：升级到含 C-2 的构建后，对真实旧账本 `handoff card wait B369 --subtree`，首行 actionable 不再列已答工单；卡详情 `open_tickets` 计数与快照一致。
2. **运行中人工 reply 跨机**：远端 agentd 任务 `reply` 后数秒内，本机卡流出现 `task_mirrored(ticket_answered)`，`card wait` 不再当未决。
3. **三类审批者路径**：Manager 中介实时裁决、reuse 复用、approval 面（OpenCode）各一次，确认均落关单镜像。
4. **生产装配核对**：真实 agentd 进程里 `internal/orchestration/approval_client.go#Manager.bindApproval` 的 `Hub: m.hub` 生效（机内无断言，见 breakdown §5.4）。
5. **断流/慢订阅残余**：镜像断连期答单，重连后（水位跳缺口、本期不根治）确认 C-2 终态收口。
6. **wait 不唤醒**：真实 `card wait` / `handoff wait` 挂到 `ticket_answered` 时不发唤醒行（`WaitDeliveryPolicy` 冻结）。

> 以上均需真机/跨机，机内夹具构造不出；由协调者在 acceptance 前执行（P4=甲）。

---

## 12. 占位符扫描

- 无 TBD / 「加适当的错误处理」/「同 Task N 而略」；改动一/二/三的改前改后文本均**逐字完整可抄**。
- **例外声明（无）**：不依赖「形态因包而异」的夹具复用——S1 无测试代码。
- **红基线声明**：S1 无代码红锚（文档面）；运行时行为的红→绿证据由 contract §4 承载，本节点只复核其绿（R4）。
- **实现类步骤缺项豁免声明**：
  - T1「关键节点日志 / 注释」不适用——**零代码改动**；
  - T2「关键节点日志 / 注释」不适用——**纯 Markdown 叙述文本，无 logger、无导出函数、无运行时行为**；
  - T3「缝级断言」不适用——**S1 无运行时代码，从 spec 两条缝构造不出针对文档文本的断言**（缝的事件面上没有「文本同步」这一事件）。
  以上三条是本 plan 对纪律「缺 3、4 即 plan failure」的唯一豁免，理由已逐条写明，非无声省略。
- **内部锁声明**：无新测试，故无内部锁。
- **条件退路**：无。

---

## 13. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - spec §1 四环根因 → contract Ticket 0 已冻结实现；本 plan T1 复核在场（R4）；
   - spec §2 级别/词表不动 → 本 plan 零代码、零词表改动；
   - spec §3.1 三发布点 → T1 复核（`facade.go`/`manager.go`/`client.go`）；
   - spec §3.2 投影层终态关单 → T1 复核（`taskstate.go:188`）；
   - spec §3.3 弃选项落 roadmap → **T2 改动三**；
   - spec §4 用户故事 1/2/3 → 运行时行为，归 T1 复核 + §11 真机；
   - spec §5「README / handoff skill 事件表随代码同步」→ **T2 改动一/二**（核心覆盖点）；
   - spec §5「`proto.go` 注记修订」→ 已随 contract 完成（R4 已读）；
   - spec §6 两条缝 → §10 声明由 contract 冻结算例锁住，S1 不新增；
   - spec §7 Out of Scope（watermark 对账 / `tickets_voided` 发布语义 / 回填脚本）→ **T2 改动三**（前两条落 roadmap；回填脚本已由 C-2 自愈，spec 明示不需要）。
2. **占位符扫描**：见 §12，无占位；三处豁免已逐条声明。
3. **跨 task 类型/签名一致性**：本 plan 无跨 task 的 Go 签名传递（S1 无代码）。三份文档的「改前原文」与本节点实读逐字一致（§6 引文取自 `sed`/`grep` 实读）；`docs/roadmap.md` 追加位置为文件末尾，不覆盖既有小节。

---

## 图覆盖债（本节点实测）

本节点按纪律先查图（`codegraph --repo . sym`）：

- `codegraph --repo . sym AnswerTicket` → 命中 `n_store_Store_AnswerTicket`（`internal/store/store.go`），**未**命中 `internal/orchestration` 的 `Manager.AnswerTicket`；
- `codegraph --repo . sym OpenTickets` → **未命中**（`Error: 符号 "OpenTickets" 不在图中；近似候选: []`）；
- `codegraph --repo . sym EventTypeTicketAnswered` → **未命中**。

与 contract §7 记录一致：`Manager.AnswerTicket` / `Store.OpenTickets` / `EventTypeTicketAnswered` 为图覆盖债，本 plan 对它们**只用普通路径、不带 `#Symbol` 锚**，未以 grep 顶替图查询（`sym` 已先跑）。`codegraph --repo . check` → `EXIT=0`。

> 未验证项：S1 三处文档改动（T2）与断言/变异（T3）本节点**未做**（只读 plan 节点）；T1 的代码复核**已亲跑到结果**（R4，原始输出见台账）。
