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

