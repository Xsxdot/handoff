# B299：协调者续接必须带隔离 HOME

- **卡**：B299
- **级别与档位**：**L1**。改动在已有 `Runner.Resume` 适配与 `SessionRef` 加字段，HTTP/DTO/依赖方向不变。plan 即下面三行，验收靠测试一眼可核。
- **状态**：已批准（用户 2026-08-29：「修吧」）
- **基线**：`acc/b156.2-156.3`
- **发现自**：`docs/superpowers/notes/2026-08-29-real-machine-acceptance.md`。B156.3 已完成，不 reopen。
- **台账**：`docs/superpowers/specs/b299-ledger.md`

## 问题陈述

房间消息叫醒已绑定的协调者时，系统必须在**同一个隔离 HOME** 上 `run -s <session>` 续接。真机二次唤醒落的是「载体已更换：新载体承接同一协调者身份（重建四步已执行）」，说明 `Resume` 失败后每次都走重建。

现状读数：

- `launchCoordinatorRound` / `wakeCoordinatorRound` 都会把载体 `HomeDir` 写进 `SessionSpec`（`internal/agentd/scheddrain.go`）。
- `Launch` 把 spec 交给 `RunTurn`（HOME/Workdir/Model 齐全）。
- `Resume` 只把 `CLI` + `SessionID` 交给 `RunTurn`（`internal/agentd/server.go` `coordinatorRunner.Resume`）。会话文件在隔离 HOME，续接跑进 agentd 默认 HOME，找不到 session。
- `launchRound` 无论 `rebuild` 真假都返回 `Rebuilt: true`（`internal/keystone/keystone.go`）。竖切测试把这个误标当成了首次拉起的期望。
- 本机环节派发 `pool.For("local")` 因 `local` 不在 `config.Targets` 里失败，能力位变 nil，B229 拒发闸把本机点火误杀。真机提交已对本机写死 `true`，函数头注释仍写「探活失败按不支持」。

## 方案

1. `SessionRef` 补上续接环境：`HomeDir` / `Workdir` / `Model`。拉起时从 spec 写入；唤醒时用组装点刚解析的 spec 覆盖非空字段，再 `Resume`。适配器把这些字段映射进 `TurnRequest`。`Runner` 仍是 Launch/Resume 两个动作，不进派发状态机。
2. `Rebuilt` 只在 `rebuild==true` 时为真。首次开卡即绑为 false。
3. 本机目标（`local` / `本机` / 本机 hostname）不走远程 target 池。能力位与本机 `Status.DisciplinesSupported` 同源（当前二进制恒 true）。远程机仍探活，失败仍按不支持。

弃选：

- **改 `Runner.Resume` 签名加 spec 参数**：也能做，但要改所有假 Runner 与竖切铁律断言。加字段更小，身份（session id）和环境（HOME）本来就该在 ref 上一起走。
- **Resume 失败当成功**：否。重建链仍要在真失败时工作。
- **本卡扩 grok/codex `RunTurn`**：否。真机洞不在名单。

## 用户故事

1. 作为协调者，开卡拉起之后往房间发一条消息，系统续接同一个 session，不再刷「载体已更换」。
2. 作为协调者，首次拉起的回执 `rebuilt=false`；只有 resume 失败后的重建才是 true。
3. 作为协调者，对本机载体点火不会因为 target 池里没有 `local` 被纪律闸误拒。

## 测试决定

接缝一条：`keysclient.Runner.Resume`（调用方 `keystone.Service.Wake`；实现 `coordinatorRunner`）。

1. Launch 带隔离 HOME 后 Wake（Wake spec 可不带 HOME）→ Resume 的 ref 仍带 Launch 时的 HomeDir/Workdir/Model；首次拉起 `Rebuilt==false`。
2. Launch 后 Wake 带另一份 HomeDir → Resume 用 Wake 这份（当前载体）。
3. `coordinatorRunner` 把 ref 的 HomeDir/Workdir/Model/SessionID 映射进 `TurnRequest`。
4. 本机 `disciplineTargetCap("local")` 在 target 池未装配时仍为 true。

变异：Resume 映射漏掉 HomeDir，测试 1 或 3 必须红。把 `Rebuilt: rebuild` 改回 `true`，竖切/本卡测试必须红。

## 实现决定（L1 plan）

1. `SessionRef` 增 `HomeDir`/`Workdir`/`Model`；`launchRound` 写入；`Wake` 用当前 spec 覆盖非空字段后再 Resume；`coordinatorRunner.Resume` 映射到 `TurnRequest`；回合开始日志带 `home_dir`。
2. `launchRound` 的 `Rebuilt` 跟 `rebuild` 参数；竖切测试改断言首次拉起 false。
3. 本机目标不走 target 池，能力位与 Status 同源 true；注释改成这个理由。

## Out of Scope

- **永不做**：续接丢 HOME 后靠重建假装闭环。
- **本期不做**：把 `RunTurn` 接到 grok / claude / codex。
- **本期不做**：排队出队的新真机证据（验收笔记证据缺口，不是本洞代码）。
- **本期不做**：合 main。
