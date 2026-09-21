# B390 contract 节点台账（2026-09-21，复评 fail 后的契约回写轮）

**产出物**：`docs/superpowers/specs/b390-contract.md`（法定路径）＋三处就地回写（`b389-contract.md`、`b233.14-contract.md`、`b390.md`）。
**工作树**：`/root/.handoff/worktrees/9afba15b`，分支 `cards/B390-charter-5`，起手 HEAD `a65baefc`（工作树干净）。
**职责**：只改文档、不落码。把 B390 已实现的「准入失败=排队态」从 plan 层临时裁决升格为 B389 契约显式豁免条款（F1），并收口 `SchedulingClient` 方法集漂移（F2）。

## 起手状态

```
$ git rev-parse --abbrev-ref HEAD   → cards/B390-charter-5
$ git log --oneline -1              → a65baefc feat(B390): 名额键残留观察面——GET /api/squads 与 squad list 暴露运行计数
$ git status --short                → （空）
$ echo $TMPDIR                      → /root/.handoff/tmp/9afba15b
```

## 复评原文锚（触发依据）

复评任务 `c38f5111-2738-4544-9f34-28aa78c8d46b`，`handoff.db`（`/root/.handoff/handoff.db`）事件 seq **46974**，`type=completed`，summary 原文：

```
B390复评：T1/T2/T3机制真实承重（自变异四处复红）、测试与回归全绿；判fail——T2准入失败路径与冻结的B389契约§3.2.5/§4-31冲突且未回写，T3接口增量突破b233.14冻结方法集未更新。
```

读取命令（只读）：
```
$ sqlite3 /root/.handoff/handoff.db "select payload from events where task_id='c38f5111-2738-4544-9f34-28aa78c8d46b' and type='completed';"
```

复评 final_text 关键裁决：F1（major/阻塞）冻结物未回写；F2（minor）b233.14 方法集漂移；F3（minor）T1 断言 (c) 被 (a) 吸收，属计划原设计不阻塞；T1 桩改 `AdmitSeatCarrier` 判为按裁决正确偏离；T2 弃内存重试表改认领挡水位由协调者 P2 乙授权、机制承重。

## 查证记录（读到的代码事实，本轮读数）

- `internal/agentd/wakeconsumer.go` 常量组 `automationStallEscalateAfter = 3`（`:42`）；`admissionStalled`（`:47-50`）→ `errors.As(&coordinatorAdmissionError) && errors.Is(scheduling.ErrNoSlot)`；`recordAdmissionStall`（`:59-80`）去重守卫 `if attempts != automationStallEscalateAfter { return }`（`:69`），达阈值 `s.ledger.MarkNeedsHuman`（`:74`）；`clearWakeStall`（`:84-88`）。
- 失败分支 `internal/agentd/wakeconsumer.go:659-671`：`if admissionStalled(wakeErr) { s.recordAdmissionStall(card); ...; continue }`——**不** `completeWakeBatch`、**不**标 seen。
- 非准入失败分支 `:676-690`：`clearWakeStall` + `completeWakeBatch` + `recordWakeBackoff` + 标 seen + `continue`（B389 §3.5.4 终局路径原样）。
- `Server.automationStall map[string]int`（`internal/agentd/server.go:202`，注释明写「**不是**重试载体——重试靠认领挡水位」）；初始化 `server.go:295`。
- `CursorWatermark`（`internal/ledger/wakeclaim.go:142-156`）读 `WakeClaimsBefore`（`:117-135`，`SELECT DISTINCT seq ... WHERE seq < ? AND done_at IS NULL`）——在飞认领挡水位。
- B389 契约待回写条款：§3.2 第 5 步（原文 `b389-contract.md:141`）「处理结果无论成功失败都要 `CompleteWake`」；§3.5.4（`:196`）退避；§4-31（`:237`）「单卡失败后进入退避」。
- F2：`b233.14-contract.md:73` / `:187` 写「18 个方法的闭包」；实际 `internal/agentd/scheduling_client.go` 方法集 20：`AdmitSeatCarrier`（`:38`，自 `6f20dfce`/B389）、`RunningCounts`（`:51`，自 `a65baefc`/B390）。生产调用点 `internal/agentd/scheddrain.go:454`、`internal/agentd/schedapi.go:87`。
- 编译期断言 `var _ SchedulingClient = (*scheduling.Service)(nil)`（`internal/agentd/scheduling_client.go:64`）持续锁方法集。

## 本轮亲跑读数（原始输出）

```
$ go test ./internal/agentd/ -run 'TestB390|TestB389WakeBackoff' -count=1 -v
=== RUN   TestB390NonAdmissionErrorDoesNotStall
--- PASS: TestB390NonAdmissionErrorDoesNotStall (0.51s)
=== RUN   TestB390WakeStallRedLoop
--- PASS: TestB390WakeStallRedLoop (0.26s)
=== RUN   TestB389WakeBackoffSkipsSameSeqNextRound
--- PASS: TestB389WakeBackoffSkipsSameSeqNextRound (0.30s)
PASS
ok  	github.com/Xsxdot/handoff/internal/agentd	1.080s

$ go build ./...
BUILD_EXIT=0

$ codegraph --repo . check
CHECK_EXIT=0
```

`codegraph sym` 覆盖债（退出码 1，无近似候选）：`AdmitSeatCarrier`、`RunningCounts`、`admissionStalled`、`recordAdmissionStall`、`clearWakeStall`、`ClaimWake`、`CompleteWake`、`WakeClaimsBefore`、`CursorWatermark` —— 均本卡/前卡新增，baseline 图未重建；已回落 grep 唯一生产命中并逐处记入 `b390-contract.md` §2.2/§3/§5。

## 回写动作清单（四处）

1. `docs/superpowers/specs/b390-contract.md`（本节点产出物，新建）：复评锚、F1 豁免条款语义+签名+冻结条目 §4-37–42+拍板记录、F2 漂移收口、图/冻结物、自检、亲跑读数、欠账。
2. `docs/superpowers/specs/b389-contract.md`（就地修订轮 2）：
   - 头部加「修订轮 2（2026-09-21，B390 复评 F1 回写）」一行（修订号+依据）；
   - §3.2 第 5 步改为「非准入失败与成功都 `CompleteWake`；准入失败例外：不 complete、不标 seen、保留认领挡水位、按节拍重试、恰一次 needs_human」；
   - §3.5 新增第 5 条「准入失败的排队态语义」（不 complete/挡水位/2s 节拍重试/恰一次 needs_human 去重/非准入失败不受影响）；
   - §4-31 改为「单卡**非准入**失败后进入退避」，并注明准入失败走 §4-38；
   - §4 新增冻结条目 37–42（不 complete、挡水位、恰一次 needs_human + 去重、非准入不误伤、计数清零、轮正常收尾）；
   - 新增 §5.2 修订轮 2 实测证据；§6 新增拍板记录第 5 项（含「反过来写不会变红」说明）；§8 更新金样本/三重闸门表述。
3. `docs/superpowers/specs/b233.14-contract.md`（就地修订）：§1 签名表+调用点表各补两行（`AdmitSeatCarrier`/`RunningCounts`，标「漂移项」）；§2.1 接口代码块补两方法；§1 闭包句与 §4-1 的「18」改「20」；新增 §11「方法集漂移收口（2026-09-21，B390 复评 F2 回写）」注明漂移起始（19=B389 `6f20dfce`、20=B390 `a65baefc`）与本次收口。
4. `docs/superpowers/specs/b390.md`（就地修订）：§3「不做」把「不动 B389 已收口的单卡失败路径」改为「不动单卡**非准入**失败路径」，并加边界说明——准入失败豁免是**经契约修订后的显式例外**，不是偷偷改；给出 B389 修订轮 2 与 §4-37–42 锚。

## 提交事实（命令原文与提交时读数）

```
$ git add docs/superpowers/specs/b390-contract.md docs/superpowers/specs/b389-contract.md docs/superpowers/specs/b233.14-contract.md docs/superpowers/specs/b390.md docs/superpowers/ledgers/2026-09-21-b390-contract-ledger.md
$ git commit -q -m "contract(B390): 复评 F1/F2 回写——准入失败豁免条款 + SchedulingClient 方法集 18→20 收口"
$ git log --oneline -1
a3e90f5b contract(B390): 复评 F1/F2 回写——准入失败豁免条款 + SchedulingClient 方法集 18→20 收口

$ git show --stat HEAD
 docs/superpowers/ledgers/2026-09-21-b390-contract-ledger.md |  92 +++++++++++++++
 docs/superpowers/specs/b233.14-contract.md                  |  27 ++++-
 docs/superpowers/specs/b389-contract.md                     |  45 ++++++-
 docs/superpowers/specs/b390-contract.md                     | 130 +++++++++++++++++++++
 docs/superpowers/specs/b390.md                              |   4 +-
 5 files changed, 289 insertions(+), 9 deletions(-)
```

本段在提交后追加，随后 amend 一次收进同批提交；amend 会换 hash——这是 git 的事实，收口判据是工作树干净。

## 欠账（显式）

- §4-41（连续计数清零）当前无独立夹具，归实现/plan 后续补。
- 图覆盖债：上述新符号未入 baseline 图，待重建。
- 本节点不落码；实现已在 `ab7d8df2`/`a65baefc` 全绿，后续实现改动须重走 contract 节点。

---

## 复评 2 纠正轮（2026-09-21，工作树 `cards/B390-charter-6`，起手 HEAD `d2f15a59`）

**触发**：复评 2 任务 `2dbd292b-f926-4e14-abeb-60ddad8dc671`，`handoff.db` 事件 `type=completed`，judge=**fail**，两条发现：

```
major: b389-contract §5.2(line367)与§8-3(line390)宣称§4-37–42(含§4-41)由b390_wake_stall_test.go承载/§4-41属该回路,
       与b390-contract §2.3/§4/§8「§4-41无独立夹具」自相矛盾;实测删除全部clearWakeStall后
       TestB390WakeStallRedLoop仍ok,§4-41确无牙——冻结物间冲突,属「不许冒充已锁」
minor: b389-contract §6-5与b390-contract §2.4断言「反过来写不会变红/补回CompleteWake后现有测试不自动红」,
       与事实不符:在准入分支补回completeWakeBatch即令TestB390WakeStallRedLoop报RED(a)/(b)/RETRY(d)
```

复评原文锚：任务 `2dbd292b` 的 `completed` 事件（只读读取命令：
`sqlite3 /root/.handoff/handoff.db "select payload from events where task_id='2dbd292b-f926-4e14-abeb-60ddad8dc671' and type='completed';"`）。

**本轮变异实测（隔离副本 `$TMPDIR/b390-mut`，`cp -a` 自 HEAD `d2f15a59`；仓内工作树未改）**

```
$ cp -a /root/.handoff/worktrees/ac9787f8 $TMPDIR/b390-mut && cd $TMPDIR/b390-mut   # d2f15a59

# 变异 A：准入分支补回 completeWakeBatch（wakeconsumer.go admissionStalled 分支）
$ go build ./internal/agentd/    → BUILD_EXIT=0
$ go test ./internal/agentd/ -run 'TestB390WakeStallRedLoop' -count=1 -v
    b390_wake_stall_test.go:131: RED(a) 准入持续失败时未按节拍重试：3 轮只尝试 1 次（want ≥ 3）
    b390_wake_stall_test.go:135: RED(b) 准入持续失败未落成 needs_human（账本无卡级可见事件）
    b390_wake_stall_test.go:143: RETRY(d) 停摆等人应恰落一次，实得 0
--- FAIL: TestB390WakeStallRedLoop (0.21s)   TEST_EXIT=1
→ 证 §4-37/38/39 有牙；并证 §6-5/§2.4 原「反过来写不会变红」理据为误。

# 变异 B：删除 wakeconsumer.go:676 与 :692 两处 clearWakeStall 调用（`_ = card` 占位）
$ go build ./internal/agentd/    → BUILD_EXIT=0
$ go test ./internal/agentd/ -run 'TestB390' -count=1 -v
--- PASS: TestB390NonAdmissionErrorDoesNotStall (0.22s)
--- PASS: TestB390WakeStallRedLoop (0.24s)
ok  github.com/Xsxdot/handoff/internal/agentd  0.463s   TEST_EXIT=0
→ 证 §4-41 无独立夹具（删全部 clearWakeStall 不红）。
```

**本轮纠正动作（只改文档）**

1. `b389-contract.md` §5.2 载体段：删「§4-37–39、§4-41 的回路」，改为「§4-37/38/39（**不含 §4-41**）」并注明 §4-41 无独立夹具、变异实测无牙。
2. `b389-contract.md` §5.2 末尾新增「复评 2 纠正」块，贴变异命令与原始结果。
3. `b389-contract.md` §6-5、§8-4：把「反过来写不会变红」改为「改已冻结失败路径语义、需修订号与可判条目」，并注明补回 `completeWakeBatch` 即 RED(a)/(b)/RETRY(d)。
4. `b389-contract.md` §8-1（`file#Symbol` 出处行）：`b390_wake_stall_test.go` 标注由「§4-37–39/41 载体」改为「§4-37–39 载体；§4-41 无夹具」。
5. `b389-contract.md` §8-3：覆盖声明由「§4-37–42 由…承载」改为「§4-37–40、§4-42…；§4-41 无独立夹具」。
6. `b389-contract.md` §8-5：新增 §4-41 欠账条。
7. `b390-contract.md` §2.4、§6-4：同步改正「反过来写不会变红」理据与覆盖声明。
8. `b390-contract.md` 头部加「复评 2 纠正轮」一行；§7 补纠正轮亲跑读数与变异实测原始输出。

**本轮纠正后亲跑读数（工作树 `cards/B390-charter-6`，HEAD `d2f15a59`，只跑不改实现）**

```
$ go test ./internal/agentd/ -run 'TestB390|TestB389WakeBackoff' -count=1 -v
--- PASS: TestB390NonAdmissionErrorDoesNotStall (0.26s)
--- PASS: TestB390WakeStallRedLoop (0.25s)
--- PASS: TestB389WakeBackoffSkipsSameSeqNextRound (0.23s)
ok  github.com/Xsxdot/handoff/internal/agentd  0.749s   TEST_EXIT=0
$ go build ./...              → BUILD_EXIT=0
$ codegraph --repo . check    → CHECK_EXIT=0
```

**纠正后一致性自查（两份冻结物同一口径）**：`b389-contract.md` §5.2/§8-1/§8-3/§8-5 与 `b390-contract.md` §2.3/§4/§6-4/§8-1 对 §4-41 的表述统一为「无独立夹具、实现节点欠账，不冒充已锁」；§4-37–40、§4-42 统一为「由 `b390_wake_stall_test.go` 承载、实跑通过」；§6-5/§2.4 拍板理据统一为「改已冻结失败路径语义、需修订号与可判条目」，两处均不再有「反过来写不会变红」的断言式表述（§6-2 中该短语仅用于**否认**其适用于 §4-2 复合键，语义相反，保留）。

**复评 2 纠正轮提交事实（命令原文与提交时读数）**

```
$ git add docs/superpowers/specs/b390-contract.md docs/superpowers/specs/b389-contract.md docs/superpowers/ledgers/2026-09-21-b390-contract-ledger.md
$ git commit -q -m "contract(B390): 复评 2 纠正——§4-41 统一为无独立夹具欠账，拍板理据更正为改冻结语义"
$ git log --oneline -1
b2e2eea5 contract(B390): 复评 2 纠正——§4-41 统一为无独立夹具欠账，拍板理据更正为改冻结语义

$ git show --stat HEAD
 .../ledgers/2026-09-21-b390-contract-ledger.md | 65 ++++++++++++++++++++++
 docs/superpowers/specs/b389-contract.md        | 27 +++++++--
 docs/superpowers/specs/b390-contract.md        | 42 +++++++++++---
 3 files changed, 120 insertions(+), 14 deletions(-)
```

本段在提交后追加，随后 amend 一次收进同批提交；amend 会换 hash——这是 git 的事实，收口判据是工作树干净。
