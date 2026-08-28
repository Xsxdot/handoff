# feat/agy-executor 代码审查（round 3）

审查对象：`feat/agy-executor` @ `5eeaa7ef4`（相对 merge-base `d319f92d`）  
上一轮：`docs/superpowers/reviews/2026-08-28-agy-executor-review-r2.md`（对照 `0c5e4b770` / notes `a91ade028`）  
本轮增量：`a91ade028..5eeaa7ef4`，20 文件 +531 / −88  
全量：43 文件 +3484 / −37  
日期：2026-08-28  
结论：**不通过——round-2 契约错误已实锤修好，权限路径仍有两处新正确性缺口，I4 工作区污染未收口**

Critical 3 / Important 4 / Minor 1。`once→allow`、fallback 极性、`step_0`、会话级 spend 都有测试实锤。剩下主风险换成：`bufio.Scanner` 64KiB 墙吞掉大文件写入，以及 `--sandbox` 与工作区外任务 `TMPDIR` 打架。

## 裁决表

| 维度 | 裁决 |
|---|---|
| plan 覆盖完整性 | 对照 round-2：C1/C2/C3/C5/C6/C7/I1/I3 已修；C4/I2/I4 部分 |
| scope drift | 无：改动都落在 round-2 建议范围内 |
| 架构法合规 | 通过 |
| 测试有牙 | 部分：once/reject、fallback、spend Key 有牙；冷恢复不锁 `permSrv`；Scanner 大帧无测试 |
| 日志与注释覆盖 | 缺：Scanner 失败关连接不打日志（见 C1） |
| 序列化边界 | 已修：PermissionID 稳定为 `step_<idx>`；spend 用会话级 Key 覆盖 |
| 冻结物触碰 | 无触碰 |

## Round-2 核销

| 项 | 状态 | 证据 |
|---|---|---|
| C1 once→deny | 已修 | `adapter.go:301-307` `once→allow`，其余 deny + `DenyGuidanceText`；`perm_test.go:76-139` 穿过 adapter |
| C2 只拦 run_command | 已修 | `taskenv.go:62` matcher 含写/改/联网；`adapter.go:342-370` 从 `TargetFile` 填 `Paths`；README 已改成「未挂钩走原生策略」 |
| C3 300s 墙 | 已修 | `taskenv.go:68` `Timeout: 86400`；测试锁 86400。仍有限时墙，见 I2 |
| C4 恢复失败仍判活 | 部分 | `WriteTaskEnv` / `newPermServer` 失败 → `Alive:false`。顺序仍是先 `startProc` 再 Listen（见 I1） |
| C5 fallback 反了 | 已修 | 有新提交 → `turn.NoTrailerResult`；无新提交有正文 → `question`。金样已改 |
| C6 UnixNano | 已修 | 始终 `step_%d`（含 0）；`permission_hook_test.go` 锁 `stepIdx:0` → `step_0` |
| C7 spend 求和 | 已修 | `{conversationID}-spend` 覆盖；两轮累计测试 InputTokens=250 而非 350 |
| I1 print-timeout/sandbox | 已修 flag | `--print-timeout 24h` + `--sandbox`。sandbox 与 TMPDIR 冲突见 C2 |
| I2 测试不锁 once | 部分 | once/reject 已锁。未锁：sock 缺失 hook 不放行；perm 失败 Start/Resume 不得判活 |
| I3 四家名单 | 已修 | initflow「五家」、init 探测表、KeepsAll、SKILL 选型均含 agy |
| I4 hooks 覆盖工作区 | 部分 | 会 merge + `strconv.Quote`。仍写进 workdir 且无 gitignore（见 C3） |

## 缺陷族

| 族 | 结论 |
|---|---|
| 生命周期 / 状态机中断 | 有风险：hook 86400s 仍会杀掉短命进程；超时 pending 靠写死连接才 `delivery_failed`。冷恢复先 `startProc` 再 Listen 是 fail-closed 窗口，不是放行 |
| 静默失败 / 误导报错 | 有风险：Scanner 超限关连接不打日志、不回 deny；sandbox 挡住 TMPDIR 时 `go test` 看起来像命令本身坏了 |
| 跨平台假设 | 有风险：`strconv.Quote` 是 POSIX 词法；官方 sandbox 只写 macOS/Linux；Windows README 仍标「可用 / PreToolUse」，无真机证据 |
| 假红 / 假绿测试 | 残余：once/reject、fallback、spend Key 有牙。冷恢复不锁 `permSrv`；Scanner 大帧无测试 |
| 门禁绕过 | 有风险：skip-permissions 已不在。新绕过面是 Scanner 吞掉大写入（门根本看不到） |
| 序列化边界 | 无：UnixNano 已去掉；spend 用会话级 Key 覆盖 |
| 枚举新值过既有白名单 | 无：Detect / init / KeepsAll / SKILL 选型均已穿 agy |

## Findings

### Critical

#### C1. 大写入进不了审批链

文件：`internal/executor/agy/perm.go:98`

`handle` 用 `bufio.NewScanner(conn)` 读一行 ask 帧，默认 `MaxScanTokenSize=64KiB`。官方 PreToolUse 会把完整 `toolCall.args` 交给 hook（`write_to_file` 含 `CodeContent`），permission-hook 又原样塞进 `input` 转发。超过约 64KiB 的写入：`Scan` 失败走 `conn.Close(); return`，不打日志、不回 `deny`、不进 `onAsk`。hook 侧 `exchange` 读裁决失败后按秒重试，直到 86400s 墙。协调者看不到工单，B249 范围内写入自动放行也走不到。claude 同款服务端用的是 `json.NewDecoder`（`internal/executor/claudecode/perm.go:131`），没有这条行上限。

建议：与 claude 对齐，改用 `json.NewDecoder(bufio.NewReader(conn)).Decode`；失败时回 `deny` 并打 Error。补一条 >64KiB `CodeContent` 的 ask 必须产出 permission 事件的测试。

#### C2. `--sandbox` 与任务 TMPDIR 冲突

文件：`internal/executor/agy/proc.go:149`

本轮无条件加了 `--sandbox`。官方 sandbox 文档写明终端命令只能写「指定工作区 + 必要系统路径」（macOS `sandbox-exec` / Linux `nsjail`）。同一 adapter 却把 `TMPDIR`/`GOTMPDIR`/`GOCACHE` 指到任务目录下的私有临时路径（`adapter.go:43-48`，Start `adapter.go:170-175`，冷恢复 `resume.go:79-84`），该路径在 `~/.handoff/tasks/<id>/tmp`，不在 agy 的 cwd/工作区里。结果是 `go test`、编译器、npm 写缓存会在 hook `allow` 之后被 OS 沙箱拒绝——与已修过的 B118（codex 沙箱排除 TMPDIR）同一类。README 把 `--sandbox` 写成「第二道防线」，没写任务临时目录不可写。

建议：不要无配置地开 `--sandbox`，或把任务 tmp 配进 sandbox 可写根；至少在 argv 注释和 README 写明「开 sandbox 时 `go test` 可能 operation not permitted」。补一条断言：若保留 `--sandbox`，必须同时保证 TMPDIR 对终端命令可写。

#### C3. hooks.json 弄脏工作区

文件：`internal/executor/agy/taskenv.go:81`

`WriteTaskEnv` 仍把 `handoff-safety-gate` 写进 `workdir/.agents/hooks.json`（0644），不进任务 gitignore，也不在 README 声明会改工作区。`ensureCleanWorktree`（`internal/agentd/workspace.go:662-676`）把未跟踪文件算脏并 409。`--new-worktree` 任务结束会回收 worktree，无事；未托管原地任务会留下 `.agents/hooks.json`（内含本机 `handoff` 绝对路径和 `perm.sock`），下一发 dispatch 被「工作区不干净」挡住，或被 `git add` 进仓库。Start 就绪超时 rollback 也不删这份文件。

建议：写入前把 `.agents/hooks.json` 追加进该工作区 gitignore（或文档+任务结束时恢复/删除）；README「各 executor 须知」写明会改工作区。测试：干净仓库 `WriteTaskEnv` 后 `git status --porcelain` 为空，或明确断言 gitignore 规则。

### Important

#### I1. 冷恢复仍先起 agy 再听 sock

文件：`internal/executor/agy/resume.go:97`

冷恢复仍先 `startProc` 再 `newPermServer`。Start 路径（`adapter.go:177-205`）是先 Listen `perm.sock`、再写 hooks、再拉进程。窗口内 hook 会 fail-closed 重试，不是放行；但 agy 已能打出 PreToolUse 时 sock 还不在，与 round-2 C4 建议的顺序仍相反。`TestResumeCold` 只看 `Alive`/`startProc` 被调用。

建议：与 Start 对齐：Listen → `WriteTaskEnv`（失败即 `Alive:false`）→ `startProc`。冷恢复测试断言 hooks.json 命令含当前 sock 且 `permSrv != nil`；`newPermServerFn` 失败不得 `Alive:true`。

#### I2. 24h hook 墙仍有限

文件：`internal/executor/agy/taskenv.go:68`

hook `timeout` 从 300 提到 86400（秒）。官方单位是秒、缺省 30，没有「0=不超时」。`waiting_answer` 可以超过 24h；超时后 agy 杀掉短命 hook，pending 连接留在 map 里直到 `Respond` 写死连接才 `delivery_failed`。官方未定义缺 JSON 退出时 allow 还是 deny。claude 的 permission-mcp 跟会话同寿命，没有这堵墙。

建议：能省略 timeout 就省略；否则把超时后的 pending 摘掉并让 manager 走 `delivery_failed`。补测试：断连路径 `Respond("allow")` 必须报错，不得返回成功。

#### I3. Windows 仍标「可用 / PreToolUse 动态裁决」

文件：`README.md:535`

round-2 I4 要求：要么补 hook 真机证据，要么改成「输入通道可用，权限门取决于 PreToolUse 是否触发」。官方 issue（antigravity-cli #222/#528）曾报 Windows/macOS hook 不触发；本分支测试全部是本地 unix socket 桩，没有 PreToolUse 真机证据。若 hook 不触发，headless 默认工作区内写自动放行，Handoff 审批链看不见。`strconv.Quote`（`taskenv.go:66`）按 POSIX 引号，Windows cmd 词法未测。官方 sandbox 文档只列 macOS/Linux，Windows argv 仍传 `--sandbox`。

建议：Windows 行改为「输入通道可用；权限门取决于 PreToolUse 是否触发」。有真机证据再写「可用」。Windows 的 `command` 引号单独测；`--sandbox` 在不支持的平台不要硬传。

#### I4. 拒绝理由会说两遍

文件：`internal/executor/agy/adapter.go:305`

C1 把拒绝理由写进 `permDecision.Message`，hook 再映射为 PreToolUse stdout 的 `reason`（官方会展示给 agent）。这是与 claude 同款的同帧送达。但 agy `Adapter` 没有实现 `DenyReasonInBand() bool`（claude 在 `claudecode/adapter.go:478`）。manager `noteDenyGuidanceUnlessInBand`（`internal/agentd/manager.go:3418-3426`）会再挂一份 B50 带外注入，模型被同一条理由说两遍。`turn.DenyGuidanceText` 约定调用方保证 reason 非空；agy 在空理由的 `reject` 上仍调用，得到「原因：」空串。

建议：给 agy adapter 实现 `DenyReasonInBand() bool { return true }`，与 claude 对称。空理由走 claude 那句「协调者拒绝了本次操作」，不要调用 `DenyGuidanceText("")`。

### Minor

#### M1. `handle` 里空 defer

文件：`internal/executor/agy/perm.go:93`

`handle` 里有一个空 `defer`，注释写「退出的唯一路径是连接异常断开」。事实上 `onAsk` 返回后 goroutine 就退出，连接留在 `pending` 里等 `Respond` 关闭；断开时也没有人从 `pending` 摘掉。注释既复述了不存在的控制流，又没有回收逻辑。

建议：删空 defer；若要 reap 死连接，在 `onAsk` 之后阻塞读直到 EOF，再从 `pending` 删除并让后续 `Respond` 失败。
