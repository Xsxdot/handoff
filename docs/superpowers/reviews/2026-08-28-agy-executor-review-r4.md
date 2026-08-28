# feat/agy-executor 代码审查（round 4）

审查对象：`feat/agy-executor` @ `0dcf7107a`（相对 merge-base `d319f92d`）  
上一轮：`docs/superpowers/reviews/2026-08-28-agy-executor-review-r3.md`（对照 `5eeaa7ef4` / notes `15aa22490`）  
本轮增量：`15aa22490..0dcf7107a`，11 文件 +358 / −40  
全量：44 文件 +3924 / −37  
日期：2026-08-28  
结论：**接近可合——round-3 正确性项已落地，「ALL resolved」不成立。剩余是工作区 exclude 边角，不是 Scanner/sandbox 那级阻断**

Important 1 / Minor 1。大帧 Decoder、去掉 `--sandbox`、Resume 先 Listen、`DenyReasonInBand`、断连 reap 都有测试实锤。剩下：`info/exclude` 盖不住已跟踪的 `hooks.json`；`.agents/` 排除过宽且失败静默。hook `timeout` 仍是 86400。

## 裁决表

| 维度 | 裁决 |
|---|---|
| plan 覆盖完整性 | 对照 round-3：C1/C2/I1/I3/I4/M1 已修；C3、I2 部分 |
| scope drift | 无 |
| 架构法合规 | 通过 |
| 测试有牙 | 部分：大帧 / 断连 reap / 空 reason / argv / 未跟踪 porcelain 有牙。Resume 成功路径不锁 `permSrv`；已跟踪 hooks.json 无测试 |
| 日志与注释覆盖 | 缺：`ensureGitExclude` 失败直接 return，不打日志 |
| 序列化边界 | 无新缺口 |
| 冻结物触碰 | 无触碰 |

## Round-3 核销

| 项 | 状态 | 证据 |
|---|---|---|
| C1 Scanner 64KiB | 已修 | `perm.go:99-103` `json.NewDecoder`；失败打 Error 并回 deny；`perm_test.go:222-272` ~130KB `CodeContent` 进 `onAsk`。线协议仍是一连接一对象，与 Decoder 无错位 |
| C2 `--sandbox` vs TMPDIR | 已修 | `proc.go:145-151` argv 已无 `--sandbox`；`proc_test.go` 金样锁死；README 写明不传是为了任务外 `TMPDIR`/`GOCACHE` 可写 |
| C3 hooks.json 弄脏工作区 | 部分 | 未跟踪路径：先 `ensureGitExclude` 再写文件，空仓库 porcelain 为空。已跟踪文件 exclude 无效（见 I1）；`.agents/` 过宽（见 I2） |
| I1 冷恢复先起进程 | 已修 | `resume.go:52-63` 探活/`startProc` 之前 `newPermServerFn`；失败 `Alive:false`。冷路径随后 `WriteTaskEnv` 再 `startProc`，与 Start 同序 |
| I2 24h hook 墙 | 部分 | `taskenv.go:74` 仍 `Timeout: 86400`。断连 reap 已做：`perm.go:128-141` EOF 从 pending 删除；断连后 `Respond("allow")` 必须报错。超时杀 hook 走同一 reap，后续批准变 `delivery_failed` |
| I3 Windows 口径 | 已修 | `README.md:535` 「输入通道可用；权限门取决于 PreToolUse 是否触发」。仍无真机证据，文档已降级，不单开 |
| I4 DenyReasonInBand | 已修 | `adapter.go:294` 返回 true；空 reject 走「协调者拒绝了本次操作」；manager 跳过 B50 |
| M1 空 defer | 已修 | 改为断连 reap 协程 |

## 缺陷族

| 族 | 结论 |
|---|---|
| 生命周期 / 状态机中断 | 有风险：86400s 仍杀 hook，但 pending 已摘、不再静默写死连接；冷恢复已先 Listen |
| 静默失败 / 误导报错 | 有风险：`ensureGitExclude` 失败无日志，未跟踪 hooks.json 会回到 409 |
| 跨平台假设 | 残余：Windows hook 未真机，文档已改口 |
| 假红 / 假绿测试 | 大帧/断连/空 reason/argv/porcelain 有牙。Resume 成功路径不锁 `permSrv`；断连用例 `Sleep(50ms)`，peer close 后 Encode 的 EPIPE 也能让断言通过 |
| 门禁绕过 | 无新绕过：OS sandbox 已拿掉，门只剩 PreToolUse（与 README 一致） |
| 序列化边界 | 无新缺口 |
| 枚举新值过既有白名单 | 无新缺口 |

## Findings

### Important

#### I1. 已跟踪的 `hooks.json` 仍会 409

文件：`internal/executor/agy/taskenv.go:88`

`WriteTaskEnv` 把带本机 `handoff` 绝对路径和 `perm.sock` 的 `handoff-safety-gate` 写进 `workdir/.agents/hooks.json`，并依赖 `ensureGitExclude` 让 `git status --porcelain` 变空。`info/exclude` 只作用于未跟踪文件。把 `.agents/hooks.json` 先 `git add`/`commit`，再写入 exclude 并修改文件，porcelain 仍是 `M .agents/hooks.json`。`ensureCleanWorktree` 据此 409。

这正是 `TestWriteTaskEnvMergesExistingHooks` 覆盖的「仓库里已有 hooks.json」路径——文件若已被跟踪，第一次 dispatch 会改脏工作区，后续 dispatch 被拦；`git add` 对已跟踪文件不受 exclude 约束，任务提交可能带上本机 sock/二进制路径。`TestWriteTaskEnvGitExcludeCleanStatus` 只覆盖从未跟踪过 `.agents/` 的空仓库。

建议：已跟踪则不要就地改文件（写入 `taskDir` 或任务结束后 restore），或改前检测 `git ls-files`，跟踪则拒绝/拷贝到未跟踪旁路。补测试：先提交 `.agents/hooks.json` 再 `WriteTaskEnv`，断言 porcelain 为空或明确失败，且 `git diff` 不含 `perm.sock`。

### Minor

#### M1. `.agents/` 排除过宽，失败静默

文件：`internal/executor/agy/taskenv.go:47`

在 `.agents/hooks.json` 之外还 `ensureGitExclude(workdir, ".agents/")`。gitignore 语义下 `.agents/` 会忽略该目录下所有未跟踪文件。规则写入 `git rev-parse --git-path info/exclude`：普通仓库是 `.git/info/exclude`；linked worktree 返回的是主仓 **common** `info/exclude`。对 worktree 写 common exclude 才能让 porcelain 变干净，但副作用是 `--new-worktree` 任务会永久改写主仓本地 exclude，主 checkout 的 `.agents/` 也会被藏掉；任务结束不回滚。注释写「仅对本地工作树生效」与 common-dir 行为不符。`ensureGitExclude` 在 `rev-parse`/打开失败时直接 `return`，不打日志，exclude 没写上时 C3 的脏工作区会静默回来。

建议：只排除 `.agents/hooks.json`；不要 `.agents/`。失败打 Error。若必须写 exclude，应避免在一次性 worktree 任务里留下永久主仓副作用，或任务结束时删除本次追加的行。
