# B393 implement 节点台账（三条修面落地）

卡 B393「唤醒执行链假设同一会话后来者能续上不成立」。本节点：charter implement，
按 `docs/superpowers/plans/b393-plan.md` 的 T0–T3 落地三条修面 + 红色回路转正。
基线分支起手 HEAD `1b2c6e6a`（当前分支 `cards/B393-charter-3`）。

## 0. 起手状态

```text
$ git status --short                 → （空）
$ git log --oneline -1               → 1b2c6e6a docs(B393): plan——resume 挂死根因...
$ go build ./...                     → BUILD_EXIT=0
$ codegraph --repo . sym RunTurn     → 命中 n_hostapi_Host_RunTurn（hostapi.go:66）
$ codegraph --repo . sym Resume      → 命中 n_agentd_coordinatorRunner_Resume（server.go:1198）
$ codegraph --repo . sym Wake        → 命中 n_keystone_Service_Wake（keystone.go:105）
```

## 1. 起手发现：agentd 测试包在 HEAD 已编译不过（先决阻塞）

跑 `go test ./internal/agentd/ -run ...` 直接 build failed：

```text
internal/agentd/scheddrain_test.go:271:78: undefined: ledger
internal/agentd/wakeconsumer_b358_test.go:204:106: undefined: ledger
...
FAIL	github.com/Xsxdot/handoff/internal/agentd [build failed]
```

归因（亲跑 git 取证）：B389 契约提交 `734caefc`（**是本 HEAD 的祖先**）把
`BindSeat/RebindSeat` 加了 `ledger.SeatBearing` 参数，但两处测试文件没补
`internal/ledger` import；补 import 的提交 `6f20dfce` 只在
`cards/B389-charter-7..9` 上（**不是本 HEAD 祖先**）。即本分支是 B389 契约的半成品合并。
不修这个，本卡任何 agentd 测试都跑不了。**最小机械修**：给两个测试文件补 import
（不改任何断言/生产面）。这是落地本卡的先决条件，已随本节点同批提交。

## 2. T0 红色回路转正（先红后绿）

### T0.1 真机 repro 脚本

新文件 `docs/superpowers/probes/b393-resume-hang.sh`。亲跑基线：

```text
$ B393_LIMIT=15 bash docs/superpowers/probes/b393-resume-hang.sh
sid=ses_f45bc164affebNHHlbcxaGAxC5 rc=124 dur=15s out_bytes=0 err_bytes=0
```

⇒ R1 复现坐实：CLI 回合跑完不 dispose、stdout 零字节、看门狗到点杀。
**边界（诚实声明）**：脚本只驱动 CLI 子进程，本仓的「有界等待」修面不在脚本路径
（脚本不走 coordinatorRunner），故脚本读数**修后仍是 rc=124**——这是 plan T0.1
已写明的边界（「本脚本只能复现 R1，不能断言本仓挂死」）。本仓挂死的有界返回由 T0.2
的 Go 回路证明。

### T0.2 挂死回路（缝 = agentd.coordinatorRunner.Resume）

新文件 `internal/agentd/b393_timeout_test.go`（PATH 注入长睡假 opencode）。

**首红 = 编译红**（符号缺席，证明修面符号不存在）：

```text
$ go test ./internal/agentd/ -run TestB393ResumeHangsBoundedByTimeout -count=1
internal/agentd/b393_timeout_test.go:46:10: undefined: coordWakeTurnTimeout
... [build failed]
```

**空壳落地后断言红**（加 `coordWakeTurnTimeout` 变量，不加行为）：

```text
$ go test ./internal/agentd/ -run TestB393ResumeHangsBoundedByTimeout -count=1
... INFO 协调者回合开始 ... timeout=30m0s ...
--- FAIL: TestB393ResumeHangsBoundedByTimeout (10.01s)
    b393_timeout_test.go:68: resume 在 1s 上界后仍未返回（无界等待，R3.1）
FAIL
```

⇒ 红因是「功能缺失」（hostapi 走 30m 缺省、10s 窗收不到返回），不是 typo。

## 3. T1 有限等待（有界返回）

改动：
- `internal/agentd/server.go`：新增 `var coordWakeTurnTimeout = 10 * time.Minute`
  （P2 拍板 10m：短于 hostapi 30m、给 5m 租约留半余量）+ `readyCtx()`；
- `Launch`/`Resume` 的 `context.WithCancel(context.Background())` → `readyCtx()`；
  并在起止/失败分支加 `slog.Default().Log(ctx, wakeRoundLogLevel, ...)`。
- `wakeRoundLogLevel = slog.LevelWarn` 落在 server.go（plan §5 写 scheddrain.go，
  但日志调用点在 server.go，同包等值；此为对 plan 的轻微落位偏差，已在 §7 声明）。

绿读：

```text
$ go test ./internal/agentd/ -run TestB393ResumeHangsBoundedByTimeout -count=1
ok  	github.com/Xsxdot/handoff/internal/agentd	1.009s
```

**变异自验**（`WithTimeout` 改回 `WithCancel`，先断言命中唯一 `count: 1`）：

```text
count: 1
--- FAIL: TestB393ResumeHangsBoundedByTimeout (10.01s)
    b393_timeout_test.go:68: resume 在 1s 上界后仍未返回（无界等待，R3.1）
```

复原后复绿。

## 4. T2 可见性（≥WARN + 账本一行）

### T2.1 hostapi driver 回合起止升到 Warn

新文件 `internal/hostapi/b393_visible_test.go`。首跑红：

```text
$ go test ./internal/hostapi/ -run TestB393WakeRoundVisibleAtWarn -count=1
--- FAIL: TestB393WakeRoundVisibleAtWarn (0.00s)
    b393_visible_test.go:36: warn 级别下「回合开始」不可见（driver.go:127 是 Info）
```

改动 `internal/hostapi/driver.go`：新增 `const wakeRoundLogLevel = slog.LevelWarn`，
`:127`「协调者回合开始」与 `:170`「协调者回合完成」改 `log().Log(ctx, wakeRoundLogLevel, ...)`。
绿：

```text
ok  	github.com/Xsxdot/handoff/internal/hostapi	0.004s
```

**变异自验**（开始行级别改回 Info，命中唯一 `count: 1`）：复现红；复原复绿。

### T2.3 账本在途事件（WakeRoundEvent）

新文件 `internal/agentd/b393_wakeround_event_test.go`：首红为编译红
`undefined: WakeRoundEvent`；补类型后 round-trip 绿。
`internal/agentd/wakeconsumer.go` 新增导出 `WakeRoundDedupePrefix`、`WakeRoundEvent`
（`Phase/Session/Err/DurationMs`，指针区分缺失 vs 零）、`ptrInt64`。

新文件 `internal/agentd/b393_wakeround_ledger_test.go`（缝 = `wakeCoordinatorRound`）：
首跑行为红——

```text
--- FAIL: TestB393WakeRoundWritesLedgerEvent (0.23s)
    b393_wakeround_ledger_test.go:65: 应恰一行 start 在途读数，实得 0（全部注释相位: map[]）
```

改动 `internal/agentd/scheddrain.go`：`wakeCoordinatorRound` 回合前 `EnsureComment`
写 `wake_round:start`、成功后写 `wake_round:end:<session>`（失败只 Warn，不覆盖结果），
并把回合起止/失败升到 `wakeRoundLogLevel`。绿（`go test ./internal/agentd/ -run TestB393` → ok）。

**变异自验**（start 的 dedupe 前缀改掉，命中唯一 `count: 1`）：

```text
--- FAIL: TestB393WakeRoundWritesLedgerEvent (0.16s)
    b393_wakeround_ledger_test.go:65: 应恰一行 start 在途读数，实得 0（全部注释相位: map[end:1]）
```

复原后复绿。

## 5. T3 退化链（重建真可用）

### T3.0 先验证（必做）

隔离 HOME 的 provider 配置只有空 schema：

```text
$ cat /root/.handoff/home/opencode/.config/opencode/opencode.jsonc
{ "$schema": "https://opencode.ai/config.json" }
$ cat /root/.config/opencode/opencode.jsonc
{ "$schema": ..., "plugin":["opencode-cmd-provider"], "model":"commandcode/...",
  "provider": { "commandcode": { "models": {...} } } }
```

亲跑隔离 HOME 起回合（真机）：

```text
$ HOME=/root/.handoff/home/opencode opencode run --print-logs --log-level ERROR \
    -m commandcode/deepseek/deepseek-v4.1-flash -- "reply ok" </dev/null
ERROR ... ProviderModelNotFoundError: Model not found: commandcode/deepseek/deepseek-v4.1-flash.
```

⇒ R3.0 坐实：隔离 HOME 缺 provider/model 定义，重建会「快速失败」在起点。
（另：B382 卡的事件流可见 needs_human「resume 与重建均不可用」原文，与根因吻合。）

### T3.1 投影 provider 配置

`internal/orchestration/coordinator_home.go`：新增 `projectCoordinatorProviderConfig`
（只写白名单内单文件 `.config/opencode/opencode.jsonc`，绝不整树同步），在 `Prepare`
的 `copyMissingCoordinatorCredential` 之后调用；并把该路径加进
`rejectCoordinatorHomeSymlinks` 白名单。

**真机取证校正了 plan 的「缺失才写」判据**（本节点重要发现）：plan T3.1 草案写的是
「缺失才写，绝不覆盖已有」。但真机生产隔离 HOME 的 `opencode.jsonc` **已存在一份裸壳**
（只有 `$schema`，创建于 2026-08-29，非本次改动）：

```text
$ cat /root/.handoff/home/opencode/.config/opencode/opencode.jsonc
{ "$schema": "https://opencode.ai/config.json" }
$ stat -c '%n %y' .../home/opencode/.config/opencode/opencode.jsonc
  ... 2026-08-29 18:24:52
```

按「文件存在即跳过」，本卡要修的失败路径（重建 → ProviderModelNotFoundError）**不会被修好**。
故实现改为**内容感知**：`definesProvider(data)` 字节扫描（含 `"provider"` 或 `"model"`），
只有隔离侧语义为空（裸壳）才投影覆盖；已有真实 provider/model 定义则保留（兑现 plan
「隔离侧可能已有更精确的定义」的原意）。

**真机端到端对照**（把主配置投影进一个干净隔离 HOME 后起回合）：

```text
$ HOME=$TMPDIR/b393-home opencode run --format json -m commandcode/deepseek/deepseek-v4.1-flash \
    -- "reply ok" </dev/null
RC=0  out_bytes=921   （step_finish reason=stop）
```

对比未投影的隔离 HOME（§5 T3.0）：`ProviderModelNotFoundError` exit=1。
⇒ 投影确实把「重建快速失败」扭转成「回合可跑」。

新文件 `internal/orchestration/b393_provider_config_test.go`：
- 内部锁 `TestB393PrepareProjectsProviderConfig`（未导出纯函数，plan §7 已登记内部锁理由），
  含三条断言：裸壳被投影覆盖 / 真实定义被保留 / 逐字节相等；
- 缝级 `TestB393PrepareViaSupplierProjectsProviderConfig`（走 `supplier.Prepare` 返回值）。

首红 = 编译红 `undefined: projectCoordinatorProviderConfig`；实现后绿。
**变异自验 A**（把 Prepare 调用改成 `_ = projectCoordinatorProviderConfig`，命中唯一 `count: 1`）：

```text
--- FAIL: TestB393PrepareViaSupplierProjectsProviderConfig (0.00s)
    ... 供给缝未投影 provider 配置（R3.3）: ... no such file or directory
```

**变异自验 B**（把守卫改回 plan 原语义「文件存在即跳过」，命中唯一 `countA: 1`）：

```text
        want={"plugin":["x"],"provider":{"commandcode":{}}}
FAIL
```

⇒ 证明「裸壳必须被覆盖」这条断言有牙。复原后复绿。

### T3.3 降级理由可行动

`internal/keystone/keystone.go`：`rebuildAfterResumeFailure` 的 `MarkNeedsHuman`
理由改为含两条失败原因摘要（`truncateCause` 截断到 200 字符），并附人工处置指引。

新文件 `internal/keystone/b393_degrade_test.go`（缝 = `keystone.Service.Wake`，
复用 `slice_test.go` 夹具）。**注意**：初版断言只查「含 resume/重建 字样」，
结果**立即绿**——常量理由也含这两个词，断言没牙。改判据为「含两条真实失败原因摘要
（fakeRunner 的 "resume 不可用" 与 "拉起不可用"）」后，首跑才红：

```text
--- FAIL: TestB393DegradeRecordsActionableReason (0.10s)
    b393_degrade_test.go:65: 理由缺 resume 失败原因摘要："协调者唤醒失败：resume 与重建均不可用"
```

实现后绿。**变异自验**（把两个 `truncateCause(...)` 参数换成 `"", ""`，命中唯一 `count: 1`）：

```text
--- FAIL: TestB393DegradeRecordsActionableReason (0.145s)
    b393_degrade_test.go:65: 理由缺 resume 失败原因摘要：
    "协调者唤醒失败：resume 与重建均不可用（resume: ; 重建: ）——请人工处置：..."
```

复原后复绿。

## 6. 收尾验证（全量编译 + 触及包全量测试）

```text
$ go build ./...                                        → BUILD_EXIT=0
$ go vet ./...                                          → VET_EXIT=0
$ gofmt -l internal/agentd internal/hostapi internal/orchestration internal/keystone
                                                        → （空）
$ go test ./internal/agentd/ -count=1 -timeout 600s     → ok  179.910s
$ go test ./internal/hostapi/ ./internal/keystone/ -count=1 -timeout 600s
                                                        → ok hostapi 0.654s / ok keystone 0.356s
$ go test ./internal/orchestration/ -count=1 -timeout 900s → ok  65.436s
```

## 7. 对 plan 的落位偏差（声明）

1. `wakeRoundLogLevel` 落在 `internal/agentd/server.go`（plan §5 列在 scheddrain.go）：
   日志调用点在 server.go，同包等值常量，不跨包。功能等价。
2. T2.3 start 事件的 `Session` 用 `nil`（plan 示例写 `&sessionID`）：resume 前会话未定，
   且 resume 失败会重建出新会话；写死旧 session 反而不实。序列化边界已由 round-trip
   测试覆盖「缺失 vs 零」。
3. T0.1 脚本修后仍 `rc=124`：CLI 挂死是第三方 Bun 构建，本仓按 P1(甲) 只做有界等待对冲；
   脚本只驱动 CLI、不走 coordinatorRunner，故其读数不体现本仓修复——边界 plan 已写明。
4. **T3.1 判据修正（重要，非纯落位）**：plan 草案「缺失才写」照搬到生产会 no-op
   （真机隔离 HOME 已有裸壳 opencode.jsonc）→ 本卡要修的失败路径修不好。实现改为
   内容感知（裸壳才投影覆盖）。详见 §5 T3.1。

## 8. 未验证/图覆盖债

- `codegraph check` 未在本节点跑（plan §9 约定归合并前视图 diff 复核）。
- 未在本机跑「真机 B389 §4.3 验收链」（spec §4.5：由协调者执行，不派发）。
- B382 当时协调者 HomeDir 是否确为 `/root/.handoff/home/opencode`：本节点验证了
  「隔离 HOME 形状缺 provider 定义」这一**通用事实**（真机亲跑），未逐条回读 B382
  当次承载记录（账本在远端 postgres，`handoff card seat bearing` 无 show 子命令）。

## 9. 提交事实（历史读数）

```text
$ git add -A && git commit -q -m "fix(B393): 唤醒执行链有界等待 + 可见性 + 隔离 HOME provider 供给"
$ git log --oneline -1
0bba54b4 fix(B393): 唤醒执行链有界等待 + 可见性 + 隔离 HOME provider 供给
```

提交后本台账补记本节并 amend 一次收进同批；amend 换 hash 属 git 事实，收口判据是工作树干净。

---

# 第二轮复评修复（B393 复评，MAJOR-1/2 + MINOR×2）

基线 = 上一批提交 `60532ffd`（即本节点起手 HEAD）。只修复评两条 major + 两条 minor，
不重做已通过部分（五条红色回路未动）。命令原始输出见下。

## R0. 起手与图覆盖债

```text
$ git branch --show-current            → cards/B393-charter-4
$ go build ./...                       → BUILD_EXIT=0
$ go test ./internal/agentd/ -run TestB393 -count=1   → ok 1.25s（基线绿）
$ codegraph --repo . sym wakeCoordinatorRound → 命中 n_agentd_Server_wakeCoordinatorRound（scheddrain.go:336）
```

本轮 `codegraph sym` 未命中（图覆盖债，回落 grep）：`definesProvider`、
`WakeRoundEvent`、`DriverLeaseTTL`、`projectCoordinatorProviderConfig`。

## R1. MAJOR-1：失败路径落 phase="fail" 账本终态行

新测试 `internal/agentd/b393_wakeround_fail_test.go`（缝 = `wakeCoordinatorRound`）：
resume 与重建双失败时，账本须落恰一行 phase=fail（带 Err 摘要），同卡重复失败不追加。

首跑红（功能缺失，非 typo）：

```text
$ go test ./internal/agentd/ -run TestB393WakeRoundFailureWritesFailEvent -count=1
--- FAIL: TestB393WakeRoundFailureWritesFailEvent (0.22s)
    b393_wakeround_fail_test.go:87: 失败态应恰一行 phase=fail，实得 0（全部注释: [{Phase:start Session:<nil> Err:<nil> DurationMs:<nil>}]）
FAIL
```

实现：`internal/agentd/scheddrain.go` 失败分支落 `EnsureComment(card, "wake_round:fail", ...)`，
Err 取 `truncateRunes(err.Error(), 400)`；写失败只 Warn 不覆盖唤醒结果。

绿 + 变异自验（`Phase: "fail"` 改 `"end"`，起点 grep 断言命中唯一 `count: 1`）：

```text
$ go test ./internal/agentd/ -run TestB393 -count=1        → ok 1.50s（绿）
变异后：
--- FAIL: TestB393WakeRoundFailureWritesFailEvent (0.22s)
    b393_wakeround_fail_test.go:87: 失败态应恰一行 phase=fail，实得 0（全部注释: [... {Phase:end ...}]）
复原后：ok 1.50s
```

## R2. MAJOR-2：上界与驱动租约自洽（选项 a）

新测试 `internal/agentd/b393_timeout_invariant_test.go`：断言 `coordWakeTurnTimeout`
严格 < `ledger.DriverLeaseTTL`。

首跑红：

```text
$ go test ./internal/agentd/ -run TestB393WakeTurnTimeoutWithinLease -count=1
--- FAIL: TestB393WakeTurnTimeoutWithinLease (0.00s)
    b393_timeout_invariant_test.go:26: 回合上界 10m0s 必须严格短于驱动租约 5m0s（挂死回合须在租约到期前判失败）
FAIL
```

选择 (a)：把上界由租约导出——`var coordWakeTurnTimeout = ledger.DriverLeaseTTL / 2`
（=2m30s，严格短于 5m，另一半作强杀+错误上抛余量）。注释改写为与事实自洽，并写明
为何够用（健康回合秒级到分钟级；本分支未接 ClaimWake/RenewDriverLease，超过租约的
回合无续租兜底，必须自缚于租约内）。`server.go` 补 `ledger` 已在 import（同批既有）。

绿 + 变异自验（改回 10m，命中唯一 `count: 1`）：

```text
$ go test ./internal/agentd/ -run TestB393 -count=1        → ok 1.51s（绿）
变异后：--- FAIL: ... 回合上界 10m0s 必须严格短于驱动租约 5m0s
复原后：go build ./... → EXIT=0；coordWakeTurnTimeout = ledger.DriverLeaseTTL / 2
```

## R3. MINOR：definesProvider 不把注释/字符串当定义

新测试 `internal/orchestration/b393_defines_provider_test.go`：8 条边界（裸壳、
行/块注释提词、字符串值是词、真实键、键前有注释）。

首跑红：

```text
$ go test ./internal/orchestration/ -run TestB393DefinesProviderBoundaries -count=1
--- FAIL: TestB393DefinesProviderBoundaries (0.00s)
    b393_defines_provider_test.go:34: 行注释提provider: definesProvider=true, want false
FAIL
```

实现：`coordinator_home.go` 改为 `stripJSONCComments`（剥 // 与 /* */，字符串字面量
内注释符原样保留）+ `hasConfigObjectKey`（只认 `"key"` 后接 `:` 的键位）。移除不再
使用的 `bytes` import。

绿 + 变异自验（退回「不剥注释」，命中唯一 `count: 1`）：

```text
$ go test ./internal/orchestration/ -run TestB393 -count=1 → ok 0.005s（绿）
变异后：--- FAIL: ... 块注释提model: definesProvider=true, want false
复原后：ok 0.005s
```

## R4. MINOR：hostapi 超时日志打实际生效外层上界

新测试 `internal/hostapi/b393_timeout_bound_test.go`（缝 = `Host.RunTurn`）：
`req.Timeout=45m` 但外层 ctx 只有 300ms 时，超时日志与错误不得报 45m。

首跑红：

```text
$ go test ./internal/hostapi/ -run TestB393TimeoutLogsEffectiveOuterBound -count=1
--- FAIL: TestB393TimeoutLogsEffectiveOuterBound (0.30s)
    b393_timeout_bound_test.go:48: 错误报了自身 req 上界 45m0s，而非外层 ctx 生效上界: hostapi: 回合超时（上界 45m0s），已终止进程树: context deadline exceeded
FAIL
```

实现：`driver.go` 新增 `effectiveTurnTimeout(ctx, timeout)`（取 req 上界与外层 ctx
剩余期限的更早者），开始日志/超时日志/超时错误统一用它。

绿 + 变异自验（超时错误改回 `timeout`，命中唯一 `count: 1`）：

```text
$ go test ./internal/hostapi/ -run TestB393 -count=1       → ok 0.306s（绿）
变异后：--- FAIL: ... 错误报了自身 req 上界 45m0s...
复原后：ok 0.309s
```

## R5. 收尾验证（触及包全量 + 全量编译）

```text
$ gofmt -l internal/agentd internal/hostapi internal/orchestration → （空）
$ go vet ./internal/agentd/ ./internal/hostapi/ ./internal/orchestration/ → VET_EXIT=0
$ go test ./internal/hostapi/ ./internal/keystone/ -count=1 → ok hostapi 0.964s / ok keystone 0.300s
$ go test ./internal/orchestration/ -count=1               → ok 64.690s
$ go test ./internal/agentd/ -count=1 -timeout 600s        → ok 180.843s
$ go build ./...                                           → BUILD_EXIT=0
```

## R6. 提交事实（历史读数）

```text
$ git add -A && git commit -q -m "fix(B393): 复评修复——失败态落账 + 上界自缚租约 + 判据硬化"
$ git log --oneline -1
（见本批提交；提交后本节 amend 一次收进同批，收口判据是工作树干净）
```



---

# 第三轮复评修复（B393：wake_round 键按轮次 + 失败出口含转交路径）

基线：起手 HEAD `5a1d70cb`，先并 `origin/cards/B233.1-charter-7`（fast-forward 到
`c5505a44`，含 B396 修复 `198a10c8`）。分支 `cards/B393-charter-8`。
只修两处缺口，不动路由/承载语义、不改 needs_cleared、不动线上状态。

## R0. 基线确认与图覆盖债

```text
$ git log --oneline -5
c5505a44 docs(B395): spec——SSE 断连丢权限工具等待窗口致回合永挂（debug 入口）
198a10c8 fix(B396): waitForTurnEnd 认 archived 为终态——归档后释放卡槽位与运行锁
5b12c7b0 plan(B396): 陈旧环节运行位不自愈——根因/分流 + archived 收口修复计划
5a1d70cb docs(B396): spec——陈旧运行锁不自愈（debug 入口，先根因后修）
d39b8b32 docs(B394): 台账补记提交 hash 读数
$ git merge-base --is-ancestor 198a10c8 HEAD && echo in-HEAD → in-HEAD
$ grep -n 'transferCoordinatorWake|wakeCoordinatorRoundRaw' internal/agentd/scheddrain.go
350:transferCoordinatorWake ... 408:wakeCoordinatorRoundRaw ... ✓
$ go build ./... → BUILD_EXIT=0
$ codegraph --repo . sym transferCoordinatorWake → MISS（图覆盖债）
$ codegraph --repo . sym wakeCoordinatorRoundRaw → MISS（图覆盖债）
$ codegraph --repo . sym WakeRoundEvent → MISS（图覆盖债）
$ codegraph --repo . sym nextWakeRoundID → MISS（图覆盖债，新符号）
$ codegraph --repo . sym writeWakeRoundFail → MISS（图覆盖债，新符号）
$ codegraph --repo . sym EnsureComment → 命中 n_ledger_Store_EnsureComment（events.go:299）
```

## R1. 红测试（先红后绿）

新文件 `internal/agentd/b393_wakeround_roundkey_test.go`：
- `TestB393WakeRoundKeysGroupPerRound`：同卡两轮 → 两行 start + 两行 end，键互异；
- `TestB393TransferWakeFailureWritesLocalFail`：对端 502 → 本机恰一行 fail 且 Err 含机器名；
- `TestB393TransferNoTargetWritesLocalFail`：目标未登记 → 同上。

改写 `b393_wakeround_fail_test.go` 第二段断言：同卡两轮失败应**两行** fail
（旧断言「重复失败不得追加行」正是被修的跨轮吞行失明）。

首跑红（全部断言红，非编译红——红因是功能缺失）：

```text
--- FAIL: TestB393WakeRoundFailureWritesFailEvent (0.25s)
    同卡两轮失败应两行 phase=fail（键按轮次分组），实得 1
    （日志原文：说明评论幂等跳过：同键已存在 dedupe_key=wake_round:fail）
--- FAIL: TestB393WakeRoundKeysGroupPerRound (0.28s)
    同卡两轮应两行 start，实得 1（键未按轮次分组=只看到第一轮）
    （日志原文：说明评论幂等跳过：同键已存在 dedupe_key=wake_round:start）
--- FAIL: TestB393TransferWakeFailureWritesLocalFail (0.19s)
    转交对端失败应恰一行本机 phase=fail，实得 0（rows=[]）
--- FAIL: TestB393TransferNoTargetWritesLocalFail (0.23s)
    转交取客户端失败应恰一行本机 phase=fail，实得 0（rows=[]）
FAIL
```

## R2. 缺口 (1)：wake_round 键按轮次

`internal/agentd/wakeconsumer.go`：
- `nextWakeRoundID()` = UnixNano + 进程内 `atomic.Uint64` 序号（只用墙钟同纳秒撞键、
  只用序号重启后撞历史键，两者拼接）；
- `WakeRoundDedupePrefix` 注释改键形：`wake_round:{start|end|fail}:<roundID>`。

`internal/agentd/scheddrain.go` `wakeCoordinatorRoundRaw`：
- start/end/fail 三键都拼同一 `roundID`（一轮一组）；start/end 写失败仍 Warn 不覆盖结果。

## R3. 缺口 (2)：失败出口审计（含转交路径 + 落行自身失败）

新增 `writeWakeRoundFail(card, roundID, reason)`：Marshal 失败 / 账本未装配 / EnsureComment
失败三条落行自身失败分支都带 card/round_id 上下文的日志（Error/Warn），不覆盖唤醒结果。

失败出口逐条：
| 出口 | 落行 | 理由 |
|---|---|---|
| GetCard 失败 | 不落 | EnsureComment 同要读卡，落行必败 |
| ValidateSeat / SeatBearingOf / 缺承载路径失败 | 落 | |
| 远端+无 raws（转交前置） | 落，Err 含机器名 | |
| `transferCoordinatorWake` 取客户端失败 | 落，Err 含机器名 | 复评明确要求 |
| `transferCoordinatorWake` 对端非 2xx | 落，Err 含机器名 | 复评明确要求 |
| resolveCoordinatorSquad / Carrier / ParseSeat / Normalize | 落 | |
| `AdmitSeatCarrier` 准入失败 | **不落** | 2s 节拍重试会刷屏；B390 recordAdmissionStall 承担可见性 |
| keystone.Wake 失败 | 落（同 roundID） | MAJOR-1 既有 |
| 重建后 Encode/Rebind（含 CAS 冲突） | 落（同 roundID） | end 之后的失败也留痕 |

## R4. 绿 + 变异自验

```text
$ go test ./internal/agentd/ -run 'TestB393' -count=1 → ok 2.247s
```

变异三发（每发先断言 `count==1` 唯一命中，再改语义；编译均过）：

1. start 键改回固定 `":start"`：
```text
count: 1
--- FAIL: TestB393WakeRoundKeysGroupPerRound (0.22s)
    同卡两轮应两行 start，实得 1（键未按轮次分组=只看到第一轮）
```
⇒ 复现「只看到第一轮」失明。复原。

2. 去掉转交 CoordinatorWake 失败分支的 writeWakeRoundFail：
```text
count: 1
--- FAIL: TestB393TransferWakeFailureWritesLocalFail (0.18s)
    转交对端失败应恰一行本机 phase=fail，实得 0（rows=[]）
```
复原。

3. 去掉转交 clientForTarget 失败分支的 writeWakeRoundFail：
```text
count: 1
--- FAIL: TestB393TransferNoTargetWritesLocalFail (0.16s)
    转交取客户端失败应恰一行本机 phase=fail，实得 0（rows=[]）
```
复原。

复原后复绿：`ok github.com/Xsxdot/handoff/internal/agentd 2.103s`。

## R5. 收尾验证（触及包）

```text
$ gofmt -l internal/agentd → （空，首轮曾报 roundkey_test.go 已 gofmt -w）
$ go vet ./internal/agentd/ → VET_EXIT=0
$ go build ./... → BUILD_EXIT=0
$ go test ./internal/agentd/ -count=1 -timeout 600s → ok 184.501s
```

## R6. 图覆盖债（本节点新增）

`codegraph sym` 未命中：`transferCoordinatorWake`、`wakeCoordinatorRoundRaw`、
`WakeRoundEvent`、`nextWakeRoundID`、`writeWakeRoundFail`。回退 grep 取源码。

## R7. 提交事实（历史读数）

```text
$ git add -A && git commit -q -m "fix(B393): wake_round 键按轮次分组 + 失败出口审计含转交路径落行"
$ git log --oneline -1
4ec6de43 fix(B393): wake_round 键按轮次分组 + 失败出口审计含转交路径落行
```

提交后本节 amend 一次收进同批；amend 换 hash 属 git 事实，收口判据是工作树干净。

---

# 第四轮复评修复（B393 复评 6d85425a：队列重试刷屏 + 口径修订）

基线 = 起手 HEAD `7ac818a9`（分支 `cards/B393-charter-10`）。只修三条 finding，范围不外扩。

## R0. 起手与图覆盖债

```text
$ git branch --show-current            → cards/B393-charter-10
$ go build ./...                       → BUILD_EXIT=0
$ codegraph --repo . sym wakeCoordinatorRound → 命中 n_agentd_Server_wakeCoordinatorRound
$ codegraph --repo . sym drainIgnitionRequest → 命中 n_agentd_Server_drainIgnitionRequest
$ codegraph --repo . sym EnsureComment         → 命中 n_ledger_Store_EnsureComment
$ codegraph --repo . sym nextWakeRoundID/writeWakeRoundFail/wakeCoordinatorRoundRaw → MISS（图覆盖债，回落 grep）
```

## R1. 负向测试先红（finding 2）

扩展 `internal/agentd/b393_wakeround_fail_test.go`：新增
`TestB393WakeRoundQueueRetryDoesNotFloodFailRows`（经 drainIgnitionRequest 5 次出队失败）。

```text
$ go test ./internal/agentd/ -run TestB393WakeRoundQueueRetryDoesNotFloodFailRows -count=1
--- FAIL: TestB393WakeRoundQueueRetryDoesNotFloodFailRows (0.23s)
    b393_wakeround_fail_test.go:153: 队列 5 次重试应恰一行 phase=fail（一轮一组，重试不新增行），实得 5
FAIL
```

⇒ 断言红（功能缺失），非编译红；既有 TestB393WakeRoundFailureWritesFailEvent 同轮绿。

## R2. 实现（finding 1：一轮一组为上限）

- `server.go`：新增 `wakeQueueRounds map[string]string` + `wakeQueueRoundsMu`（card → 未收尾队列轮 roundID）。
- `scheddrain.go`：
  - 新增 `retainWakeQueueRound(card)` / `clearWakeQueueRound(card)`；
  - `drainIgnitionRequest` 出队前 retain、成功后 clear，失败回填保留；
  - 抽出 `wakeCoordinatorRoundID(..., roundID)`：roundID 非空沿用、空串新开；
  - `wakeCoordinatorRoundRaw` / `wakeCoordinatorRound` 委托且新开一轮；
  - D4/ValidateSeat/SeatBearingOf/缺承载/resolveSquad/读载体/ParseSeat/Normalize/本机 Wake
    全部改用入参 roundID（不再每出口 `nextWakeRoundID()`）；
  - `transferCoordinatorWake` 增加 `roundID` 参数，空串才自生成。

绿：

```text
$ go test ./internal/agentd/ -run TestB393 -count=1 → ok 2.602s
```

**变异自验**（retainWakeQueueRound 去掉「已有则复用」分支，先断言 `count: 1` 唯一命中）：

```text
count: 1
--- FAIL: TestB393WakeRoundQueueRetryDoesNotFloodFailRows (0.42s)
    队列 5 次重试应恰一行 phase=fail（一轮一组，重试不新增行），实得 5
```

⇒ 测试有牙。复原后复绿。

## R3. 口径修订回写（finding 3）

- `wakeconsumer.go` WakeRoundDedupePrefix 注释：修订号 r2 + 「一轮」定义（队列路径同
  IgnitionRequest 共享 roundID；非队列每次新开一轮）；推翻「每次唤醒恰一行」。
- `docs/superpowers/plans/b393-plan.md` §5/T2.3 实现卡注意：同款修订号与定义。

## R4. 图覆盖债（本节点）

`codegraph sym` 未命中：`nextWakeRoundID`、`writeWakeRoundFail`、`wakeCoordinatorRoundRaw`。
回退 grep 取源码。

## R5. 提交事实（历史读数）

```text
$ git add -A && git commit -q -m "fix(B393): 队列唤醒一轮一组——重试复用 roundID 不刷屏 + 口径修订 r2"
$ git log --oneline -1
779a1994 fix(B393): 队列唤醒一轮一组——重试复用 roundID 不刷屏 + 口径修订 r2

提交后本台账补记本节并 amend 一次收进同批；amend 换 hash 属 git 事实，
收口判据是工作树干净。
```

---

# 第五轮复评修复（review-4 f354b830：请求身份键 + 冻结口径回写 + clear 测试锁）

基线 = 起手 HEAD `e81b34a1`（分支 `cards/B393-charter-11`）。只收 review-4 三条 finding，范围不外扩。

## R0. 起手与图覆盖债

```text
$ git branch --show-current            → cards/B393-charter-11
$ git log --oneline -1                 → e81b34a1 fix(B393): 队列唤醒一轮一组…
$ go build ./...                       → BUILD_EXIT=0
$ go test ./internal/agentd/ -run TestB393WakeRound -count=1 → ok 1.062s（基线绿）
$ codegraph --repo . sym drainIgnitionRequest → 命中 n_agentd_Server_drainIgnitionRequest
$ codegraph --repo . sym EnsureComment         → 命中 n_ledger_Store_EnsureComment
$ codegraph --repo . sym retainWakeQueueRound/clearWakeQueueRound/wakeQueueRoundKey
                                              → MISS（图覆盖债，回落 grep）
```

## R1. F1（major）：队列轮次键按 IgnitionRequest 身份分组

新文件 `internal/agentd/b393_wakeround_reqround_test.go`：
- `TestB393WakeRoundQueueGroupsPerRequest`：同卡 implement→review 两个不同请求
  各失败一次 → 应两行 fail、两行 start 键互异。

首跑断言红（功能缺失，非编译红）：

```text
$ go test ./internal/agentd/ -run TestB393WakeRoundQueueGroupsPerRequest -count=1
--- FAIL: TestB393WakeRoundQueueGroupsPerRequest (0.21s)
    b393_wakeround_reqround_test.go:62: 同卡两个不同请求应各落一行 phase=fail（键按请求身份分组），实得 1
FAIL
```

⇒ 红因：键按 card 分组，两请求共享 roundID，第二行 fail 被 EnsureComment 幂等吞掉。

实现：
- `scheddrain.go` 新增 `wakeQueueRoundKey(req)`（json.Marshal 整请求；Marshal
  失败兜底 `card\x00node`，不静默合并成 card 键）；
- `retainWakeQueueRound(req)` / `clearWakeQueueRound(req)` 改收整个请求、按键存取；
- `drainIgnitionRequest` 两处调用改传 `req`；
- `server.go` wakeQueueRounds 注释改「IgnitionRequest 身份键」并写明契约是
  「同一 IgnitionRequest 为一轮」不是「同一卡为一轮」。

绿：`go test ./internal/agentd/ -run TestB393 -count=1 → ok 3.022s`。

**变异自验 F1**（retain 里 `key := wakeQueueRoundKey(req)` → `key := req.Card`，先断言
`count=2` 唯一命中 retain 那一处语义）：

```text
count=2
mutated retain only
--- FAIL: TestB393WakeRoundQueueGroupsPerRequest (0.26s)
    同卡两个不同请求应各落一行 phase=fail（键按请求身份分组），实得 1
```

⇒ 复现按卡分组吞行。复原后复绿（既有 `TestB393WakeRoundQueueRetryDoesNotFloodFailRows`
同轮仍绿 = 负向锁保留：同请求 5 次重试仍恰一行 fail）。

## R2. F3（minor）：clearWakeQueueRound 成功收尾测试锁

同文件 `TestB393WakeQueueRoundClearsAfterSuccess`：两次成功 drain 同一请求 →
应各得一组 start/end（clear 后下一条开新一轮）。

**首红（先删 clear 取断言红，再恢复）**：

```text
mutated count=1
--- FAIL: TestB393WakeQueueRoundClearsAfterSuccess (0.24s)
    b393_wakeround_reqround_test.go:104: 成功收尾后下一条 IgnitionRequest 应开新一轮（两行 start），实得 1
```

⇒ 红因是功能缺失（clear 不在），非 typo。恢复 clear 后绿。

**变异复红（收尾再验）**（删 `s.clearWakeQueueRound(req)` 调用，命中唯一 `count=1`）：

```text
mutated clear count=1
--- FAIL: TestB393WakeQueueRoundClearsAfterSuccess (0.22s)
    成功收尾后下一条 IgnitionRequest 应开新一轮（两行 start），实得 1
```

复原后 `go test ./internal/agentd/ -run TestB393 -count=1 → ok 3.016s`。

## R3. F2（major 阻塞）：冻结口径回写 spec §4.3

`docs/superpowers/specs/b393.md` §4.3 第 3 条：把「一行账本事件」改为带
**修订号 r2** 的完整口径 + 理由（EnsureComment 幂等会吞行 / 无界新键会刷屏），
并写明队列路径键按**请求身份**分组、同卡换节点新开一轮。
同步收紧 `wakeconsumer.go` WakeRoundDedupePrefix 注释措辞（「同卡同一条」→
「同一 IgnitionRequest + wakeQueueRoundKey 请求身份」），避免再被读成按卡。

## R4. 收尾验证

```text
$ gofmt -l internal/agentd              → （空）
$ go build ./...                        → BUILD_EXIT=0
$ go vet ./internal/agentd/             → VET_EXIT=0
$ go test ./internal/agentd/ -count=1 -timeout 600s → ok 173.827s
$ go test ./internal/agentd/ -run TestB393 -count=1 → ok 3.016s
```

## R5. 图覆盖债（本节点新增）

`codegraph sym` 未命中：`wakeQueueRoundKey`、`retainWakeQueueRound`、
`clearWakeQueueRound`。回退 grep 取源码。

## R6. 提交事实（历史读数）

```text
$ git add -A && git commit -q -m "fix(B393): 队列轮次键按请求身份 + spec r2 回写 + clear 测试锁"
$ git log --oneline -1
b5090ae6 fix(B393): 队列轮次键按请求身份 + spec r2 回写 + clear 测试锁

提交后本台账补记本节并 amend 一次收进同批；amend 换 hash 属 git 事实，
收口判据是工作树干净。
```
