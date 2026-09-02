# feat/agy-executor 代码审查（round 2）

审查对象：`feat/agy-executor` @ `0c5e4b770`（相对 merge-base `d319f92d`）  
上一轮：`docs/superpowers/reviews/2026-08-28-agy-executor-review.md`（对照 `ded3030a4`）  
本轮增量：`ded3030a4..0c5e4b770`，30 文件 +1113 / −97  
全量：40 文件 +2890 / −33  
日期：2026-08-28  
结论：**不通过——round-1 骨架缺口大多已补，scheme 2 权限门还不能合**

Critical 7 / Important 4。`--skip-permissions` / `--print=` / `MarkRoot` / TMPDIR / Detect 都有实锤；新门把 manager 的 `"once"` 静默折成 `"deny"`，协调者批准（含 B249 自动放行）会让 `run_command` 全部硬拒，而且 `RespondPermission` 返回成功。

## 裁决表

| 维度 | 裁决 |
|---|---|
| plan 覆盖完整性 | 对照 round-1 findings：C2 / I2 / I4 已修；C1 / C3 / I1 / I3 / I5 / I6 / M1 部分 |
| scope drift | 无：`permission-hook` + `perm.sock` 是修 C1 的正当范围 |
| 架构法合规 | 通过：`cmd/permission_hook.go` 与 claude 的 `permission-mcp` 同形，组装在 agy adapter |
| 测试有牙 | 未验：权限路径金样内部 `"allow"`，不锁 `RespondPermission("once")` |
| 日志与注释覆盖 | 通过（映射错误本身不打日志，算 C1 的静默失败） |
| 序列化边界 | 缺：`stepIdx==0` 改用 UnixNano 当 PermissionID，幂等去重失效 |
| 冻结物触碰 | 无触碰 |

## Round-1 核销

| 项 | 状态 | 证据 |
|---|---|---|
| C1 skip-permissions | 部分 | `proc.go:138-150` 已去掉 flag；`TestAgyArgv` 不再金样它；批准词表没映射（见 C1） |
| C2 MarkRoot | 已修 | `adapter.go:204` Start 走 `prochost.ResolveMarkRoot`；`start_ordering_test.go:60-64` 钉住 managed worktree 下非空。冷恢复仍用 `req.MarkRoot`，与 claude 对称 |
| C3 Detect 四家 | 部分 | `toolchain/detect.go:108` `order` 含 agy；init/dispatch/agentd 错误串已加。`initflow.go:307` 「四家」文案还在（见 I3） |
| I1 `--print=` | 部分 | 已删，argv 与官方 stdin 会话一致。未设 `--print-timeout`（见 I1） |
| I2 TMPDIR | 已修 | Start `adapter.go:170-175` 与冷恢复 `resume.go:79-84` 都拼 `managedTaskTmpEnv`；`start_ordering_test.go:66-76` 断言 `TMPDIR=` |
| I3 产品名单 | 部分 | dispatch 帮助、README 两语、SKILL 描述/物料已含 agy。SKILL 选型段仍三家（见 I3） |
| I4 skill/pathenv | 已修 | `internal/skill/install.go:50` `.gemini/antigravity-cli/skills`；`pathenv.go:59` `.../bin` |
| I5 测试 | 部分 | 补了 `testdata/turn_success.jsonl`、`fallback_verdict_test.go`、`start_ordering_test.go`、`perm_test.go`。权限仍锁内部 `"allow"`（见 I2）；fallback 金样锁反了 B74（见 C5） |
| I6 Spend | 部分 | `mapMessage` 读 `init.model`；`mapResult` 发 `Spend`。会话累计按回合键求和（见 C7） |
| M1 注册表测试 | 部分 | `TestAdapterRegistryHasAlwaysAvailableExecutors` 已含 agy；oneshot 注释已对齐。`TestAdaptersForAlwaysAvailableKeepsAll` 仍漏（见 I3） |

## 缺陷族

| 族 | 结论 |
|---|---|
| 生命周期 / 状态机中断 | 有风险：hook 300s 墙、perm.sock 恢复失败仍缺席、pending 连接与工单超时后对不上 |
| 静默失败 / 误导报错 | 有风险：批准被折成 deny 且 RespondPermission 返回成功；无 trailer 且无新提交时 FailReason 仍写「有新提交」 |
| 跨平台假设 | 有风险：agy 在 Windows 注册且 perm.sock 走 AF_UNIX（与 claude 同款）；hooks.json 是 shell 字符串，PreToolUse 在 Windows/macOS 上无本分支真机证据 |
| 假红 / 假绿测试 | 有风险：权限金样绕过 adapter 契约词表；fallback 金样锁反了 B74 |
| 门禁绕过 | 有风险：skip-permissions 已不在；新绕过是 hook 只匹配 `run_command` + 用户级 settings.json allow 对未挂钩工具仍生效 |
| 序列化边界 | 有风险：`stepIdx==0` 改成 UnixNano，hook 重拉后同一工具换 PermissionID |
| 枚举新值过既有白名单 | 残余：Detect/dispatch/README 已穿；initflow 文案、init 探测表测试、SKILL 选型、KeepsAll 测试未穿 |

## Findings

### Critical

#### C1. 批准变成硬拒

文件：`internal/executor/agy/adapter.go:293-301`，`internal/executor/agy/perm.go:144-146`

`executor.Adapter.RespondPermission` 的契约值是 `"once"` / `"reject"`（`internal/executor/executor.go:213`；manager 在 `waitPermission` / `autoAllowPermission` / `approvePermission` 一律这么调）。claude 在 `claudecode/adapter.go:440-442` 把 `once→allow`、其余 `→deny`。agy 把 `decision` 原样交给 `permServer.Respond`，而 `perm.go` 只接受 `allow|deny`，其它一律改成 `deny`。

结果：协调者 `--approve`、审批者放行、B249 白名单自动放行（`manager.go:1997` 的 `"once"`）全部在 socket 上变成硬拒绝；`RespondPermission` 仍返回 nil，manager 记「已送达」，协调者看不到失败。hook 侧只会看到合法的 `deny`，也不会报错。这条比 round-1 的 skip-permissions 更阴，因为它看起来像接上了审批链。

建议：与 claude 对齐：在 `Adapter.RespondPermission` 里把 `once→allow`、其余 `→deny`，拒绝理由走 `turn.DenyGuidanceText` 写入 `permDecision.Message`。补一条穿过 adapter 的测试：假 permServer + `RespondPermission(..., "once", "")` 断言写出 `behavior=allow`；`"reject"` 断言 `deny`。

#### C2. hook 只拦 `run_command`

文件：`internal/executor/agy/taskenv.go:50-64`

`hooks.json` 的 PreToolUse matcher 只有精确的 `run_command`。官方工具表还包括 `write_to_file` / `replace_file_content` / `multi_replace_file_content` / `read_url_content` / `search_web` / `invoke_subagent` / `ask_permission` 等；matcher 支持 `"run_command|write_to_file|..."` 或 `"*"`。未挂钩工具走 agy 原生策略：工作区内读写自动放行（路径判据是 agy 的 workspace=cwd，不是 handoff 的三根），工作区外/联网默认 Ask，headless 下 Ask 是 soft-deny，协调者看不见。用户 `~/.gemini/antigravity-cli/settings.json` 的 `permissions.allow` 对未挂钩工具仍然生效，且本分支没有 claude 那种任务级 settings 去 Ask 覆盖用户 allow。README「所有敏感工具均经 Handoff 审批链」不成立。

`permTextAndRequest` 也只解析 `run_command` 的 `CommandLine`；即便补上 write matcher，不抽 `TargetFile` 进 `Perm.Paths`，B249 的范围内写入自动放行也走不了 `PermToolWrite` 路由。

建议：matcher 至少覆盖写文件与联网类工具（或 `"*"` 再按工具名分流）。写类从 `TargetFile`/`AbsolutePath` 填 `PermToolWrite`/`Edit` + `Paths`，让 B249 三根范围生效。至少在 README 把「未挂钩工具 = agy 默认 / 用户 settings」写成和 codex 同级的权限模型警告。`--sandbox` 可作防波堤，替代不了 permgate。

#### C3. hook 300 秒墙

文件：`internal/executor/agy/taskenv.go:59`

官方 hook `timeout` 单位是秒，缺省 30；这里写成 `300`（5 分钟）。permission-hook 对 sock 不可用是无限重试（fail-closed，设计对），但 agy 会在 timeout 后杀掉这个短命进程。本产品的 `waiting_answer` 经常远超 5 分钟。超时后：hook 无 JSON 退出（官方未定义此时 allow 还是 deny）、`perm.sock` 里那条 pending conn 还挂着；协调者稍后批准会 `Respond` 到死连接或「工单未处于待裁决状态」，manager 却可能已经把工单标成已送达。claude 的 permission-mcp 是跟会话同寿命的 stdio 进程，没有这条墙。

建议：把 timeout 提到与协调者等待同量级（小时），或官方允许 0/省略表示不超时。hook 被杀后 adapter 必须把对应 pending 摘掉并让 manager 走 delivery_failed。补测试：超时/断连路径不得把 allow 写到已死连接还返回成功。

#### C4. 恢复路径 perm 失败仍判活

文件：`internal/executor/agy/resume.go:91-119`

冷/热恢复与 Start 不对称。(1) 冷恢复 `WriteTaskEnv` 的错误被 `_, _, _ =` 丢掉，hooks.json 可能仍是旧二进制路径或根本没写上；(2) `startProc` 在 `newPermServer` 之前，agy 进程已能打出 PreToolUse，此时 sock 还不在；(3) `newPermServer` 失败只 Warn，`r.permSrv` 留空，任务仍 `Alive:true`。claude 同路径失败就 drop 任务。Start 对 perm 失败是硬失败（`adapter.go:181-184`）。恢复后无 perm 时，hook 重试直到 C3 的 300s 墙；之后原生 Ask 在 headless 里 soft-deny，shell 全哑火。

建议：与 Start/claude 对齐：先 Listen perm.sock，再（冷）重写 hooks.json（错误要失败），再 startProc。perm 起不来就 `Alive:false` 交协调者。冷恢复测试应断言 hooks.json 命令行含当前 sock、且 `permSrv != nil`。

#### C5. fallback 相对 B74 反了

文件：`internal/executor/agy/adapter.go:579-598`

有新提交时本应 `turn.NoTrailerResult`（`OK:false`，模型没宣布完成 handoff 不替它宣布，`turn/fallback.go:60-62`）；agy 改成 `OK:true` +「根据 git 提交历史自动收尾」。无新提交时本应有正文就 `question`、零文本才失败；agy 一律失败，且失败理由用的是 `NoTrailerFailReason`，该函数文案写死「相对回合起点有新提交」——无提交的回合会向协调者撒谎。`fallback_verdict_test.go:58-59` 把「有新提交必须 OK」锁成金样，删对行为会先红这条测试。

建议：有新提交走 `turn.NoTrailerResult`；无新提交且有正文走 `question`，零文本走带 `VoidReasonTurnDiscipline` 的失败。改测试，不要金样 `OK:true`。

#### C6. `stepIdx==0` 换成 UnixNano

文件：`cmd/permission_hook.go:68-71`

官方 PreToolUse `stepIdx` 是 0-based，「当前轨迹步」合法值包含 0。代码把 `stepIdx==0` 当成缺省，改用 `time.Now().UnixNano()` 当 `tool_use_id`。Go 的 `int` 零值与「字段缺失」无法区分。后果：该请求的 PermissionID 在 hook 进程重启（超时重拉、agentd 恢复后 hook 重连）时会变，manager 按 `taskID:permID` 的幂等去重失效，协调者可能被同一工具唤醒两次，或旧工单永远答不掉新连接。

建议：始终用 `step_<stepIdx>`（外加 `conversationId` 或 tool 名若需要防碰撞）。不要用时钟。重连同 id 时 `perm.go` 已有换连接逻辑，与 claude 一样依赖稳定 id。

#### C7. Spend 把会话累计当回合增量

文件：`internal/executor/agy/spend.go:21-33`

官方 stdin 文档写明 `result.usage` 是会话累计，不是单回合增量。`parseSpend` 用 `conversationID-turn-<numTurns>` 当 Key。store 对同 Key 覆盖、异 Key 求和。多回合会把累计值相加：turn1 in=100、turn2 in=220 → 面板 320。claude 刻意取本轮 usage 并用 result uuid 做键。`stream_test.go` 的 golden 只有一轮 result，锁不住这条。

建议：固定用会话级 Key（已有 `conversationID-spend` 分支）让后到的累计覆盖先到的；或自己做差分再按回合键入账。测试至少两轮累计 usage，断言 TaskCumulative 等于最后一轮而不是两轮之和。

### Important

#### I1. 未设 `--print-timeout` / `--sandbox`

文件：`internal/executor/agy/proc.go:138-150`

`--print=` 已按 round-1 I1 去掉，stdin 会话 argv 形状正确。官方仍把 `--print-timeout` 默认写成 5m；stdin 专节没写免责。长实现回合若仍吃这条墙，失败形态是进程退出/意外中断，不是超时原因。本轮也没有 `--sandbox`（官方独立 flag，默认 false）；hook `allow` 之后命令在无 OS 围栏下跑。

建议：真机确认 stdin 会话是否吃 `--print-timeout`；吃就显式传足够大的值并写进 `agyArgv` 注释。`--sandbox` 建议打开作为 permgate 之外的围栏，并在 README 权限模型里写清。

#### I2. 测试不锁 `"once"` → allow

文件：`internal/executor/agy/perm_test.go:59`

`TestPermServerAskAndRespond` 与 `cmd/permission_hook_test.go` 都直接对 socket 回 `"allow"`，不断 `Adapter.RespondPermission`、不测 `"once"`/`"reject"`、不测 deny 输出 `{"decision":"deny"}`、不测 sock 缺失时 hook 不放行。`start_ordering_test.go` 只 stat 了 hooks.json 存在，不断言 matcher/命令 quoting/`permSrv`。C1 这种契约词表错误在现有测试下全绿。

建议：至少加：Start 桩下 hooks.json 命令含 `permission-hook --sock <taskDir>/perm.sock`；`RespondPermission("once")` → hook stdout `decision=allow`；`"reject"` → `deny`；perm 失败时 Start/Resume 不得判活。

#### I3. 新枚举穿白名单的尾巴

文件：`internal/initflow/initflow.go:307`

`warnIfNotReady` 仍称「已知的四家」且名单无 agy（交互 Select 来自 Detect，agy 已在表里，这条对 agy 不再误报，但文案仍假）；`cmd/init_test.go:200-208` `TestInitPrintsDetectionTable` 不断言 agy；`cmd/agentd_test.go:190` `TestAdaptersForAlwaysAvailableKeepsAll` 仍漏 agy；`skills/handoff/SKILL.md:96-105` 执行器选型仍是三家。

建议：文案改成「不在 Detect 名单里」或列出实际五家；三处测试把 agy 加进必有集合；SKILL 选型补一行 agy 的适用场景。

#### I4. `hooks.json` 覆盖写入工作区

文件：`internal/executor/agy/taskenv.go:39-76`

`WriteTaskEnv` 把整份 `workdir/.agents/hooks.json` 覆盖写入（0644），不合并已有 named hook。这会：(1) 打掉用户/插件已有 hooks；(2) 在非 managed 共享 workdir 上让并发任务互相覆盖 sock 路径；(3) 把 `handoffBin` 与 `perm.sock` 路径写进可能被 `git add` 的工作区。claude 的 settings/mcp 写在 0700 的 taskDir，不进仓库。命令行是 `fmt.Sprintf("%s permission-hook --sock %s", handoffBin, sockPath)` 无引号，官方 `command` 经 shell 执行，路径含空格即裂。README 把 Windows agy 标成「可用」并声称 AF_UNIX 动态裁决；AF_UNIX 与 claude 同款可成立，但 PreToolUse 在 Windows/macOS 上曾有官方 issue 称不触发，本分支没有 hook 真机证据。

建议：生成前读并保留其它 named group；hooks.json 加进任务 gitignore 或文档里写明会改工作区；`command` 用 `strconv.Quote`（Windows 另测）。Windows 行要么补 hook 真机证据，要么改成「输入通道可用，权限门取决于 PreToolUse 是否触发」。
