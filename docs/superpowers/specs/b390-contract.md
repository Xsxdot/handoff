# B390 契约修订：准入失败豁免条款 + 方法集漂移收口

**上游状态：已批准**（源 spec `docs/superpowers/specs/b390.md` 头部「上游状态：已批准」已核实；本节点为 B390 复评 fail 后的**契约回写轮**）
**级别：L2 单子系统**（agentd 消费循环 + scheduling 准入；本节点只改文档，不落码）
**入口节点：contract**（角色：契约落地与冻结；本节点产出物 = 本文件）
**复评 2 纠正轮（2026-09-21）**：复评 2（任务 `2dbd292b`，judge=fail）指出 §5.2/§8-3 冒充 §4-41 已锁、§6-5「反过来写不会变红」理据与事实不符。本轮就地更正 §2.4、§4、§6-4、§7、§8 与 `b389-contract.md` §5.2/§6-5/§8-1/§8-3/§8-4/§8-5：§4-41 统一表述为「无独立夹具、实现节点欠账」；拍板记录理据改为「改已冻结失败路径语义、需修订号与可判条目」（非「测不出来」）。
**有效基线：`cards/B390-charter-5` @ `a65baefc`**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（`codegraph/best.json`）
**冻结状态：随本提交冻结**（本节点触及的三份既有契约就地修订，不新增跨域方向、不新增符号，`codegraph/target.json` / `best.json` 无增量——见 §5）
**台账：** `docs/superpowers/ledgers/2026-09-21-b390-contract-ledger.md`

本轮起因：B390 复评（review 节点，judge=fail）两条发现 F1/F2（卡上 note 有全文）。本节点**只回写文档、不落码**，把 B390 已实现的「准入失败=排队态」从 plan 层临时裁决升格为 B389 契约的**显式豁免条款**并加可判 pass/fail 的冻结条目，同时收口 `SchedulingClient` 方法集之漂移，并把 B390 spec §3 的措辞与豁免条款对齐。

---

## 1. 复评发现与回写范围（触发锚）

复评任务 `c38f5111-2738-4544-9f34-28aa78c8d46b`（`handoff.db` 事件 seq 46974，`type=completed`）summary 原文：

> B390复评：T1/T2/T3机制真实承重（自变异四处复红）、测试与回归全绿；判fail——T2准入失败路径与冻结的B389契约§3.2.5/§4-31冲突且未回写，T3接口增量突破b233.14冻结方法集未更新。

| # | 复评发现 | 本节点回写动作 | 落点 |
| --- | --- | --- | --- |
| **F1** | 准入失败路径「不 `CompleteWake` + 每轮重试」与冻结的 B389 §3.2.5（「无论成功失败都要 `CompleteWake`」）/§4-31（失败进退避）冲突，且 `b389-contract.md`/`b390.md` 无修订号 | 给 B389 契约加**准入失败豁免条款**，写明准入失败=排队态、退避规则收窄为仅辖非准入失败；新增冻结条目 §4-37–42；补三重闸门拍板记录 §6-5；b390 spec §3 措辞对齐 | `b389-contract.md`、`b390.md`、本文件 |
| **F2** | T3 新增 `SchedulingClient.RunningCounts`（第 20 方法）突破 `b233.14-contract` 冻结的 18 方法闭包，该契约未更新 | 方法集 18→20，注明漂移起始（B389 加第 19、B390 加第 20）与本次收口；调用点表、接口代码块、§4-1 条款同步 | `b233.14-contract.md`、本文件 |

复评同时确认（**非**回写对象，仅记账）：T1 桩由 `LaunchAdmit` 改覆写 `AdmitSeatCarrier` 属 P1 合线后的被迫修正，方向正确；T2 弃内存重试表改认领挡水位由协调者 P2 乙授权、机制承重（自变异复红证实）；F3（T1 断言 (c) 被 (a) 吸收）属计划原设计，不阻塞。

---

## 2. F1：准入失败豁免条款（B389 契约修订轮 2）

### 2.1 语义冻结

准入失败 = 协调者小队准入返回 `scheduling.ErrNoSlot`（包装为 `coordinatorAdmissionError`）。它是**可恢复排队态，不是失败态**，与账本故障/承载缺失/席位非法等**非准入失败态**分流：

1. **不 `CompleteWake`、不标 seen**：`(card,seq)` 认领行留在飞（`done_at IS NULL`），挡住终局前缀水位（`CursorWatermark` 读 `WakeClaimsBefore`，`DISTINCT seq` 聚合，任一行在飞即挡）。游标不越过该 seq。
2. **下一轮自然重试**：节拍 = 既有 `automationPollInterval = 2s` 轮询本身；同持有者续期认领，下一轮重读到同 seq 并再次尝试准入。**不设秒级退避**。
3. **恰一次 `needs_human`**：同卡连续准入失败计数达阈值（实现取 `automationStallEscalateAfter = 3`）落**恰一条** `needs_human`；去重判据为「次数 == 阈值」（第 4 次及以后不再落）；成功或遇非准入错误即清零计数。
4. **非准入失败不受影响**：仍走 §3.2 第 5 步完成 `CompleteWake`、标 seen、进退避（§3.5.4 / §4-31），不断整轮——即 B390 spec §3 所说的「不动 B389 已收口的单卡**非准入**失败路径」。

### 2.2 落地签名（现状代码事实，带符号锚）

| 符号 | 出处 | 作用 |
| --- | --- | --- |
| `admissionStalled(err error) bool` | `internal/agentd/wakeconsumer.go:47` | 只认 `coordinatorAdmissionError` + `scheduling.ErrNoSlot`，其它错误一律 false |
| `Server.recordAdmissionStall(card string) int` | `internal/agentd/wakeconsumer.go:59` | 累计连续失败计数；`attempts != automationStallEscalateAfter` 即返回（去重），达阈值落一次 `needs_human` |
| `Server.clearWakeStall(card string)` | `internal/agentd/wakeconsumer.go:84` | 成功/非准入失败清零计数 |
| `automationStallEscalateAfter = 3` | `internal/agentd/wakeconsumer.go:42` | 升级阈值 |
| `Server.automationStall map[string]int` | `internal/agentd/server.go:202` | 连续失败计数（内存态；非重试载体，重试载体是认领挡水位） |
| 准入失败分支 `continue` | `internal/agentd/wakeconsumer.go:659-671` | `admissionStalled` 为真时不 `completeWakeBatch`、不标 seen、`continue` |

> 行号为**本轮读数**（HEAD `a65baefc`）。上表**除 `Server.automationStall` 外均为本卡新增符号，不在 baseline 图中**（`codegraph sym` 退出码 1、无近似候选），故用 `file:line` 而非 `file#Symbol` 锚；属**图覆盖债**，图重建后应改回符号锚（见 §5）。

### 2.3 新增原子冻结条目（B389 契约 §4，逐条独立 pass/fail）

| # | 断言 | 现有测试载体 |
| --- | --- | --- |
| 37 | 准入失败（`ErrNoSlot`）时不 `CompleteWake`：同卡同 seq 的认领行失败后仍 `done_at IS NULL` | `internal/agentd/b390_wake_stall_test.go#TestB390WakeStallRedLoop`（保留认领 ⇒ 挡水位 ⇒ 下轮重试） |
| 38 | 准入失败后认领挡水位：游标不越过该 seq，下一轮重读到同 seq 并**再次尝试准入**（N 轮背靠背准入被尝试 ≥ N 次） | 同上，断言 `stub.admitCount() >= passes` |
| 39 | 连续达阈值恰落**一次** `needs_human`：阈值前不落、第 3 次恰一条、第 4 次及以后不新增 | 同上，断言 `b390NeedsHumanCount == 1` |
| 40 | 非准入失败不落 `needs_human`、不按节拍重试：走终局路径，N 轮内准入只被尝试 1 次 | `#TestB390NonAdmissionErrorDoesNotStall`（`ErrNoHealthy` 桩）+ `#TestB389WakeBackoffSkipsSameSeqNextRound`（真实 `Resume` 失败支） |
| 41 | 连续计数清零：一次成功后再遇准入失败，`needs_human` 从 1 重新累计到阈值再落一次 | `#Server.clearWakeStall` 逻辑；当前无独立夹具，标**实现节点欠账**（见 §4） |
| 42 | 准入持续失败仍正常收尾本轮：单卡不阻断同批其他卡，轮末返回（不 panic、不空转死循环） | `#TestB390WakeStallRedLoop` 三轮跑完不挂；`#TestB389WakeBackoffSkipsSameSeqNextRound` 同批不全断 |

**诚实差异**：§4-40 的 `nonAdmissionScheduling` 桩用 `ErrNoHealthy`，不覆盖认领后真实 `Resume` 失败那一支；后者由既有 `TestB389WakeBackoffSkipsSameSeqNextRound` 覆盖。两条合看才是 §4-40 全貌，单看任一条不完整。§4-41 当前**无夹具**（无「成功→再失败」的连续序列测试），列为实现节点欠账，不冒充已锁。

### 2.4 拍板记录（命中三重闸门：难逆转 + 无上下文会惊讶 + 真取舍）

准入失败不 `CompleteWake`、保留认领挡水位、按轮询节拍重试，且**去掉退避**——这三条各自都改了 B389 已冻结的失败路径语义，回改要动认领/游标/退避三处；后人看到失败分支不收尾认领会想「补上 `CompleteWake` 让它前进」，那恰好重开 B390 要修的静默停摆（B332 卡死 leader 名额后整条唤醒链死 18 小时）；被否方案：甲（失败即 `CompleteWake` + 退避 = 现状，导致「失败一次就再也不试」）、乙（内存重试表，跨重启丢，B390 plan 原案，被协调者 P2 裁决弃）。**记录依据（复评 2 更正）**：复评 2 在隔离副本实测，准入分支补回 `completeWakeBatch` 即令 `TestB390WakeStallRedLoop` 报 RED(a)/(b)/RETRY(d)，故本条**不是「反过来写不会变红」**；要记是因为它改的是 B389 **已冻结**的失败路径语义（§3.2 第 5 步 / §4-31），需修订号 + §4-37/38 可独立打勾的条目才能在修订面被追责，且只有专用夹具照得到、B389 既有回归对它无感。

---

## 3. F2：`SchedulingClient` 方法集漂移收口（B233.14 契约修订）

`b233.14-contract.md` §2.1/§4-1 冻结「方法集恰为 §1 表 18 个方法的闭包」。实际已漂到 20：

| # | 方法 | 漂移起始 | 生产调用点 | 依据 |
| --- | --- | --- | --- | --- |
| 19 | `AdmitSeatCarrier(squad, carrier string) (scheduling.Binding, error)` | `6f20dfce`（B389 第 2–7 片） | `internal/agentd/scheddrain.go:454` | B389 契约 §3.4 冻结载体准入 |
| 20 | `RunningCounts() (map[string]int, error)` | `a65baefc`（B390 T3） | `internal/agentd/schedapi.go:87` | B390 spec §4.3 名额键残留观察面 |

两处均为各卡 plan 层预授权并记入各自 implement 台账（B389 plan、B390 plan §Task 3），**非本节点新开能力**；编译期断言 `var _ SchedulingClient = (*scheduling.Service)(nil)`（`internal/agentd/scheduling_client.go:64`）持续锁方法集。

收口动作（已在 `b233.14-contract.md` 就地落地）：§1 签名表补两行、§1 生产调用点表补两行、§2.1 接口代码块补两方法、§4-1 与 §1 闭包句的「18」改「20」、新增 §11「方法集漂移收口」注明起始与依据。两方法均有生产消费点，非漂移常量；`SetDefaultCarrier` 的「疑似漂移」结论不变。

---

## 4. 移交 plan 附区（不计冻结条目）

以下实现级决定对契约对侧不可见，不占冻结条目名额：

- **§4-41 的独立夹具**：补一条「成功唤醒 → 再准入失败 → 重新累计到阈值」序列测试，锁 `clearWakeStall` 的重置语义。由后续 plan 吸收。
- **`automationStall` 的进程内存性**：重启后计数清零（安全方向：重新计时而非永久静默）；跨重启行为不属本卡。由 plan 记账。
- **`recordAdmissionStall` 在 `s.ledger == nil` 时**：仍会 `s.log.Warn` 并累计计数，但 `MarkNeedsHuman` 需非 nil 账本；当前装配路径下 `consumeAutomationEventsOnce` 已在入口拒 nil，未触及。由 plan 视情补防御。

## 5. 图与冻结物

- **无跨域方向增量、无新符号**：本轮只改既有契约文档措辞与条款，不引入代码。`codegraph/target.json`（51 条契约、`d_gateway→d_scheduling` 已含 `interfaces:["SchedulingClient"]`）**无增量**，`best.json` 不变，本分支**合法无视图 diff**（不造空文件）。
- **图覆盖债**（`codegraph sym` 亲跑读数）：`AdmitSeatCarrier` / `RunningCounts` / `admissionStalled` / `recordAdmissionStall` / `clearWakeStall` / `ClaimWake` / `CompleteWake` / `WakeClaimsBefore` / `CursorWatermark` 均未命中 baseline 图（新增符号，图未重建；`sym` 退出码 1，无近似候选）。本轮以 `grep` 复核唯一生产命中并逐处记入本文件 §2.2 / §3。图重建后应补。
- **无 Ticket 0**：本节点不落码，无骨架、无直通竖切（L2 由 plan 的最薄路径条承接）。

## 6. 本轮自检（法定产出核对）

1. **契约增量文档落盘**：本文件 + 三处就地回写（`b389-contract.md`、`b233.14-contract.md`、`b390.md`）；每个签名带 `file#Symbol` 符号锚。
2. **目标图**：无增量（§5）——只改既有契约文档措辞，不引入代码，`codegraph/target.json`/`best.json` 随本提交保持原状；本分支合法无视图 diff。`codegraph --repo . check` → `CHECK_EXIT=0`。
3. **Ticket 0 骨架**：无（只改文档）。
4. **可执行冻结**：本轮**无命中**（哈希/密钥派生/编码格式均不涉及）；新增冻结条目 §4-37–40、§4-42 由 B390 已有 Go 测试承载、本轮实跑核对读数见 §7；**§4-41 无夹具**（复评 2 变异实测无牙），列为欠账、不冒充已锁。
5. **三重闸门**：命中 1 项（§2.4 准入失败豁免），已补进 `b389-contract.md` §6-5；F2 为漂移收口、未命中三重闸门（单文档修订、对侧不可见）。

## 7. 本轮亲跑读数（原始输出）

**7.1 冻结条目承载测试（复评 2 纠正轮，工作树 `cards/B390-charter-6`，HEAD `d2f15a59`，只跑不改实现）**

```text
$ go test ./internal/agentd/ -run 'TestB390|TestB389WakeBackoff' -count=1 -v
--- PASS: TestB390NonAdmissionErrorDoesNotStall (0.26s)
--- PASS: TestB390WakeStallRedLoop (0.25s)
--- PASS: TestB389WakeBackoffSkipsSameSeqNextRound (0.23s)
PASS
ok  github.com/Xsxdot/handoff/internal/agentd  0.749s

$ go build ./...                     → BUILD_EXIT=0
$ codegraph --repo . check           → CHECK_EXIT=0
```

**7.2 §4-41 无牙变异实测（隔离副本 `$TMPDIR/b390-mut`，仓内工作树未改）**

```
# 变异 A：准入分支补回 completeWakeBatch
$ git checkout $TMPDIR/b390-mut（d2f15a59）；在 wakeconsumer.go admissionStalled 分支插入 s.completeWakeBatch(card, claimedSeqs)
$ go test ./internal/agentd/ -run 'TestB390WakeStallRedLoop' -count=1 -v
    b390_wake_stall_test.go:131: RED(a) 准入持续失败时未按节拍重试：3 轮只尝试 1 次（want ≥ 3）
    b390_wake_stall_test.go:135: RED(b) 准入持续失败未落成 needs_human（账本无卡级可见事件）
    b390_wake_stall_test.go:143: RETRY(d) 停摆等人应恰落一次，实得 0
--- FAIL: TestB390WakeStallRedLoop
→ §4-37/38/39 有牙；且证明「补回 completeWakeBatch 会变红」，§6-5 原「反过来写不会变红」理据为误。

# 变异 B：删除 wakeconsumer.go:676 与 :692 两处 clearWakeStall 调用（_ = card 占位）
$ go build ./internal/agentd/  → BUILD_EXIT=0
$ go test ./internal/agentd/ -run 'TestB390' -count=1 -v
--- PASS: TestB390NonAdmissionErrorDoesNotStall (0.22s)
--- PASS: TestB390WakeStallRedLoop (0.24s)
PASS
→ §4-41 无独立夹具：删掉全部 clearWakeStall 后 B390 两测试仍绿，故不宣称 §4-41 已锁。
```

（完整命令与原始输出见台账 `docs/superpowers/ledgers/2026-09-21-b390-contract-ledger.md`。）

## 8. 欠账（显式，不静默）

1. §4-41（连续计数清零）当前**无独立夹具**，归实现/plan 后续补（§4 移交区）。
2. 图覆盖债：§5 所列新符号未入 baseline 图，待图重建。
3. 本节点**不落码**：F1/F2 的实现已在 `ab7d8df2`/`a65baefc` 落地并全绿，本轮只补契约回写；实现若有进一步改动须重走 contract 节点。
