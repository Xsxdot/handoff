# feat/agy-executor 代码审查

审查对象：`feat/agy-executor`（`d48f48ab`，相对 merge-base `d319f92d` 单提交）  
对照：`origin/main` merge-base；兄弟执行器 `internal/executor/{claudecode,codex,grok,opencode}`  
范围：18 文件，+1703 / −4  
日期：2026-08-28  
结论：**不通过——adapter 骨架能跑，还不能当一等执行者合入**

Critical 3 / Important 6 / Minor 1。合入前主风险不在 stream-json 解析器，而在权限门被整段关掉、进程围栏打不上标、产品入口仍是「四家」。

## 裁决表

| 维度 | 裁决 |
|---|---|
| plan 覆盖完整性 | 不适用：分支上没有挂 spec/plan，按「把 agy 加为一等执行者」这个意图审 |
| scope drift | 有（该做的没做齐）：注册表 / oneshot / 恢复 / 探活 / Reap 做了；init 探测、dispatch 帮助、README、skill、权限模型、围栏、任务临时目录没跟上兄弟执行器 |
| 架构法合规 | 通过：新包 `internal/executor/agy` 与 claudecode 同级竖切，组装点在 `cmd/agentd.go` |
| 测试有牙 | 未验：缝级证据缺失。单测锁内部 helper 和自拟 JSON；`TestAgyArgv` 还把 skip-permissions 锁成金样 |
| 日志与注释覆盖 | 缺：`RespondPermission` 注释写「按普通续接/确认处理」，实现是空 `return nil` |
| 序列化边界 | 本次无新 proto 字段；`ParseUsage` 只填 `ContextTokens`，Spend / 实际模型名未出站 |
| 冻结物触碰 | 无触碰 |

## 总体评价

`feat/agy-executor` 把 Antigravity CLI（`agy`）按 claudecode 的 prochost + `out.jsonl` tailer 骨架做成了注册表里的新执行器。adapter 内部的回合分类、trailer、git 兜底与 claude 同形，stream-json 的 `init` / `step_update` / `result` 嵌套形状也和官方 headless 文档一致。

已接线：`cmd/agentd.go` 注册表（全平台，含 Windows）、`--executor` 解析走注册表（`dispatch --executor agy` 能发）、oneshot、热/冷恢复、探活、Reap、Web/桌面缺省执行者与纪律绑定（走 `Manager.ExecutorNames()`）、config `executor.default`（自由字符串，无枚举）、Windows 输入通道（`prochost.CreateInputChannel`，与 claude 同款）。

未接线、兄弟执行器有而本分支没有：

- `internal/toolchain.Detect` / `handoff init` 探测表与选项
- `dispatch --executor` 帮助、`agentd` 未知名错误串
- README「各 executor 须知」与 `skills/handoff/SKILL.md`
- `internal/skill` 落点（无 `~/.gemini/...`）、`pathenv` 已知安装目录
- 任务级 `taskenv` / 权限 MCP / `--sandbox`
- `managedTaskTmpEnv`
- `spend.go` / `ActualModel`
- claude 同级的 `testdata/*.jsonl`、`fallback_verdict_test`、`start_ordering_test`

纪律档位表已随 B229 删除，agy 会出现在纪律 UI 绑定里，不再有「未登记 → single-context」这条静默降级；本分支也没有给 agy 单独登记角色正文。

## 缺陷族

| 族 | 结论 |
|---|---|
| 生命周期 / 状态机中断 | 有风险：`MarkRoot` 空导致托管 worktree 扫杀认不出 agy 进程；冷恢复会带上 `req.MarkRoot`，第一段寿命却没打标 |
| 静默失败 / 误导报错 | 有风险：`init` 把手填的 `agy` 说成「派发时会报未注册」（注册表已有）；`executor.default=agy` 时启动探测表里没有它，装没装都不会 WARN |
| 跨平台假设 | 无，因为 spawn/fifo 走 prochost，agy 在 darwin/linux/windows 都注册 |
| 假红 / 假绿测试 | 有风险：单测锁内部 helper 与自拟 JSON，没有真协议抓包、没有 `MarkRoot`/权限/argv 超时、没有兜底分类 |
| 门禁绕过 | 有风险：`--dangerously-skip-permissions` 让 write/exec 完全不进 permgate |
| 序列化边界 | 无新 proto 字段；`ParseUsage` 只填 `ContextTokens` |
| 枚举新值过既有白名单 | 有风险：Detect / init / dispatch 帮助 / README / SKILL / skill 落点 / pathenv 仍是四家名单 |

## Findings

### Critical

#### C1. `--dangerously-skip-permissions` 绕过整条权限门

文件：`internal/executor/agy/proc.go:141`，`internal/executor/agy/adapter.go:237-244`

`agyArgv` 无条件加上 `--dangerously-skip-permissions`。官方 headless 文档写明：该 flag 会把所有工具（含写文件和跑命令）自动批准；默认 headless 只自动放行工作区内读写，shell 是 Ask 并 soft-deny。agy 的 stream-json 没有 claude 那种 permission 事件/MCP hook，所以不能靠 `RespondPermission` 补洞——本分支也没有任务级 `settings.json` allow/deny、没有 `--sandbox`、没有 `taskenv.go`。结果是 `git push`、`rm -rf /`、写 `~/.ssh`、`handoff dispatch` 自指令全部在执行器里直接发生，permgate / 审批链 / 自指令闸一次都看不见。

`RespondPermission` 是空实现，注释写「如收到响应按普通续接/确认处理」，实际既不续接也不确认。codex 的「工作区内 OS 沙箱代批」至少写进了 README；agy 这条更宽的绕过完全没文档。

建议：不要全局 skip。按官方建议写任务级 `permissions.allow`（工作区写 + 已知安全命令）并启用 `--sandbox`；其余保持 Ask/deny。若协议暂时接不上工单，必须在 README「各 executor 须知」把这条写成和 codex 同级的权限模型警告，并说明越界写/破坏性命令不会出现 `permission_request`。同时改掉 `RespondPermission` 的假注释：要么实现、要么写明「本执行器不产权限工单」。

#### C2. `Start` 把 `MarkRoot` 写死成空串

文件：`internal/executor/agy/adapter.go:149`，`internal/executor/agy/resume.go:82`

claude/grok/codex/opencode 一律传 `prochost.ResolveMarkRoot(req.Task.Workdir(), req.Task.WorktreeManaged)`。`prochost` 的归属判据在 MarkRoot 为空时永不命中，托管 worktree 的扫杀/围栏因此看不见 agy 拉起的后代。冷恢复路径会把 manager 给的 `req.MarkRoot` 传下去，于是「agentd 重启之后」的进程有标、「第一次 Start 的进程」没有标——同一任务两段寿命行为不一致。

建议：与 claude 对齐：`MarkRoot: prochost.ResolveMarkRoot(req.Task.Workdir(), req.Task.WorktreeManaged)`。补一条测试钉住 StartProc 的 spec.MarkRoot 在 managed worktree 下非空。

#### C3. `toolchain.Detect` / `handoff init` 仍是四家

文件：`internal/toolchain/detect.go:108`

`order` 仍是 `opencode / claude / grok / codex`，没有 `agy`。连带：`handoff init` 探测表和选择器（`cmd/init.go`、`internal/initflow/initflow.go:163`）列不出 agy；`warnIfNotReady` 在名单外会打印「不在已知的四家里……派发时会报未注册」——这句是错的，agentd 注册表已经有 agy；`logExecutorDetection`（`cmd/agentd.go:319`）只遍历 Detect 结果，`executor.default=agy` 时即使二进制不在 PATH 也不会 WARN。这是典型的新枚举值没穿旧白名单：adapter 注册了，产品入口当它不存在。

建议：把 `agy` 加进 `order`。凭证没有可靠文件判据时走 `StateAuthUnknown`（与 claude 相同，配置在 `~/.gemini/antigravity-cli/`，不宜拿 settings.json 当登录证明）。同步改 `detect_test.go`、`cmd/init_test.go`、`initflow` 文案（「四家」→ 实际名单）。

### Important

#### I1. `--print=` 与官方 stdin 会话冲突

文件：`internal/executor/agy/proc.go:144`

官方 headless 文档「Stream prompts from stdin」的 argv 是 `agy --input-format stream-json --output-format stream-json`，并在 Common mistakes 里写明：streaming 模式只从 stdin 读 prompt，命令行 `-p`/`--print` 的 prompt 会被丢掉。本分支额外传了 `--print=`。同一文档把 `--print-timeout` 默认写成 5m。`--print=` 至少是多余且与官方禁止项同形；若它把会话送进 print mode，长实现回合会被 5 分钟墙砍死，失败会表现为进程退出/意外中断，而不是超时原因。`TestAgyArgv` 把这组 argv 锁成金样，改对协议会先红这条测试。

建议：按官方 stdin 会话去掉 `--print=`。若仍要防交互 TUI，查 agy 是否在非 TTY + 仅 input/output-format 下保持 headless。长任务路径显式传足够大的 `--print-timeout`，或用真机确认该 flag 不作用于 stdin 会话后再写进注释。

#### I2. 未接 `managedTaskTmpEnv`

文件：`internal/executor/agy/adapter.go:142`，`internal/executor/agy/resume.go:79`

claude/grok/opencode/codex 的 Start/冷恢复都会 `managedTaskTmpEnv` + `ensureTaskTmp`，把 `TMPDIR`/`GOTMPDIR`/`GOCACHE` 指到任务私有目录。agy 把 `req.Env` 原样下发，进程用共享 `/tmp`。在 C1 的 skip-permissions 下，临时文件、编译缓存、脚本落盘都不进任务目录，回收和 B249 的「任务临时目录」边界对 agy 无效。冷恢复同样没补。

建议：抄 claude 的 `managedTaskTmpEnv` / `ensureTaskTmp`，Start 与冷恢复都拼到 Env 后面（用户 env 在前、隔离值在后，避免被覆盖）。

#### I3. 协调者可见名单没跟上注册表

文件：`cmd/dispatch.go:334`，`cmd/agentd.go:186`

`dispatch --executor` 帮助仍是 `opencode/claude/grok/codex/fake`；`cmd/agentd.go:186` 未知名错误仍是「支持 opencode/claude/grok/codex/fake」（同文件 427 行的 flag 帮助已经加了 agy）；`cmd/agentd.go:177` 注释仍写四家真实执行者；README.md / README.zh-CN.md 的命令表和「各 executor 须知」没有 agy；`skills/handoff/SKILL.md` 描述与执行器选型仍是四家。`dispatch --executor agy` 会成功（resolveExecutor 看注册表），但 `--help`、init、README、skill 都不会告诉协调者这个名字。

建议：凡对用户列出执行者名的地方与注册表对齐。README 补就绪判据（`agy -p "hi"`、凭证在 `~/.gemini/antigravity-cli/`）和权限模型（见 C1）。skill 描述、执行器选型、任务目录物料（agy 是 `agy.log` + `out.jsonl` + `proc.json`，与 claude 同形）一并改。

#### I4. skill 落点与 pathenv 没有 antigravity

文件：`internal/skill/install.go:45`，`internal/pathenv/pathenv.go:54`

`agentDirs` 只有 `.claude/skills`、`.codex/skills`、`.config/opencode/skills`、`.grok/skills`。agy/Antigravity 的 skills 扫描目录不在表里，`handoff skill install` 不会把协调者 skill 装到 agy。`homeRelDirs` 同样没有 antigravity 官方落点；agy 若只装在 Detect 看不到的目录，LookPath 失败会被报成「agy 未安装」。

建议：查 agy 实际 skills 根（常见是 `~/.gemini/antigravity-cli/` 一类）后加入 `agentDirs`；确认官方二进制目录后加入 `homeRelDirs`。

#### I5. 单测没锁调用方可见行为

文件：`internal/executor/agy/adapter_test.go:13`

`TestAdapterNotRunning` / `TestAdapterStop` 只碰空运行态；`TestStreamLoopEndTurn` 用自拟 JSON 走 `mapMessage`，不断 `streamLoop`、不测 `finish` trailer、不测 `status!=SUCCESS`、不测 tool、不测 `handoff_exit`；没有 `fallback_verdict_test`、没有 `testdata/*.jsonl` 真协议回放、没有 `start_ordering`、没有断言 `MarkRoot`/隔离 env/`--print-timeout`。`TestAgyArgv` 反而把 skip-permissions 和 `--print=` 锁成正确形态。claude 同级有 `testdata/turn_success.jsonl`、`fallback_verdict_test.go`、`start_ordering_test.go`、perm 样本。删掉 `mapResult` 的 trailer 分支或把 `MarkRoot` 继续留空，现有测试仍绿。

建议：至少入库一份官方文档里的 stream-json 样本（init + agent_response delta + tool `run_command` + result SUCCESS/ERROR），经 tailer→`mapMessage` 断言 question/result/usage；补 git 真仓库的 `fallbackClassify`；Start 桩 `startProcHost` 断言 MarkRoot 与 Env 含 TMPDIR。

#### I6. 用量只映射 `ContextTokens`

文件：`internal/executor/agy/usage.go:24`

`ParseUsage` 只映射 `ContextTokens = input + cache_read`，丢掉 `output_tokens` / `thinking_tokens`，也从不发 `Spend`。init 载荷里的 `model` 未读，`ActualModel` 永远为空。用量面板能显示占用，计费/实际模型名对 agy 任务是盲的。这不是正确性缺陷（零值规则守住了），但是四家一等执行器里唯一没有 spend 接线的。

建议：按官方 usage 对象补 `SpendEntry`（注意文档：result.usage 是会话累计，不是单回合增量）；从 `init.model` 或 `--model` 回填 `ActualModel`。

### Minor

#### M1. 始终可用名单与 oneshot 注释漏 `agy`

文件：`cmd/agentd_test.go:33`，`internal/executor/oneshot.go:18`

`TestAdapterRegistryHasAlwaysAvailableExecutors` 仍断言 `opencode, claude, codex, fake`，没有 `agy`。虽然另有 `TestAdaptersForRegistersAgyOnAllPlatforms`，但注释写明「漏掉任一行都不会编译报错，症状要拖到派发时报未注册」的契约测试是这一条。`TestAdaptersForAlwaysAvailableKeepsAll` 同样漏了。oneshot 注释还写「目前支持 opencode / claude / grok」，与同文件已加上的 agy 分支不一致。

建议：始终可用名单加上 `agy`；oneshot 注释改成与错误串一致。
