<!-- 来源：熟练工 W1（code-investigate，mimo@linux-01，任务 9b8651d8，基线 fad1aadf）产物 report.md 原样入仓。现状读数，执行节点复核。 -->

# handoff 只读调查报告（B460 取证）

基线分支 HEAD：`fad1aadf`（工作树只读，未改动）。下文 `文件:行` 均为本工作树内路径。

## 1. `dispatch --wait` 与 `wait --final`

- 等待实现在 `internal/client/skilled.go:159` `WaitFinal`：**轮询快照 + 跟事件流**混合。`cmd/skilled.go:231` `runFinalWait` 调用它；`cmd/dispatch.go:421` `--wait` 按任务落点 `task.Target` 按机跟随（F14），`cmd/wait.go:136` `--final` 经 `--machine` 按机跟随。`cmd/wait.go:241` `onAttention` 回调在 question/permission_request 到达时**只打 stderr 提示行**（`cmd/skilled.go:241-244`），继续等，不退出。
- 退出码定义在 `cmd/exit.go:20-28`：0 完成 / 3 `ExitTaskFailed`（turn_failed/failed/未完成就归档，`cmd/skilled.go:257-260`）/ 4 `ExitAffinityRefused`（422，`cmd/skilled.go:202-216`）/ 124 `ExitTimeout`（`cmd/skilled.go:245-254`，超时任务照跑）。一次性 `wait` 的 124 在 `cmd/wait.go:166-176`。
- `reply` 续跑：`internal/agentd/handlers.go:339-367` 落库应答→唤醒 hub→`ResumeIfIdle` 回迁 running（`internal/orchestration/facade.go:152-180`）；executor 侧 `waitPermission`（`internal/orchestration/manager.go:2891-2922`）收到答案后先回迁 running 再 `RespondPermission(once/reject)`，任务继续跑。`WaitFinal` 循环重取快照，自然观察到续跑后的终态。
- 最小改动落点（推测）：`internal/client/skilled.go:249-268` 的 `streamOnceOn` 回调里 question/permission_request 今天只调 `onAttention`；加一个专用退出（如在 `cmd/exit.go` 新增 `ExitAttention`）并让 `WaitFinal` 返回可辨识的哨兵错误，`runFinalWait` 收到后打印工单（已有 `ev` 全量）并以该码退出。`--wait` 默认行为是否改，需 B460 spec 定（只列落点，不设计）。

## 2. 权限请求流水线

- opencode 结构化提取在 `internal/executor/opencode/adapter.go:1790-1826`：只有 `metadata.command != ""`（bash 形态，含 external_directory 越界目录）或有路径时才产出 `PermRequest`；**webfetch 等两者皆无 → `req == nil` → `Perm == nil`**（`adapter.go:1810-1815` 有 warn）。
- 分类在 `internal/approval/authority.go:160-208` `Judge`：`Perm == nil` → 直接 `Escalate`，理由 **`adapter 未提供结构化权限载荷`**（`authority.go:166-172`）。这就是 webfetch 走后者直落人工的原因（opencode 侧 webfetch 无 command/paths 可提，判据看不到结构，按 fail-closed 交人；agy 的 `read_url_content/search_web` 反而产出 `Tool: webfetch` 结构见 `internal/executor/agy/adapter.go:391-406`，走 Consult 路径——两家行为不同）。
- `审批模型未形成允许` 在 `internal/approval/client.go:396-406`：审批者返回干净 `escalate`（`!dec.Approve && dec.Err == nil`）时升级人工的理由。审批者调用失败则为 `审批者调用失败`（`client.go:396-400`）。OpenCode 新路径与 Manager 旧路径（`internal/orchestration/manager.go:2087-2140` `handlePermission` → `judgePermission`/`escalatePermission`）共用同一权威 `Judge`（`manager.go:2204-2211`）。
- claude/codex/grok/agy 共用同一 `permgate.Gate.Judge` 与同一升级/工单/事件形状（Manager `handlePermission` 不分执行器；OpenCode 面经 `approval.Client`，其余经 Manager 中介）。差异只在 adapter 侧的提取与原生回传。

## 3. 审批链与 B452 产物目录

- 调用：`internal/orchestration/approver.go:272-277` `Decide` 先读共享链（`effectiveChain`，`approver.go:301-346`），命中走 `decideSharedChain`（`approver.go:423-468`，逐环，`linux-01/agy → linux-01/codex` 即 rings），否则走本地 `decideLocalChain`（`approver.go:373-421`）。环注入点 `SetChainReader/SetRemoteDecider/SetLocalShot/SetSelfMachine`（`approver.go:121-147`）；跨机端点 `POST /api/approver/decide`（`internal/agentd/approverapi.go:42`，wire 见 `internal/proto/approver.go:11-52`）。
- 给模型的上下文只有两样：权限原文 + `task.PlanSummary`（`internal/approval/client.go:334-341` 取摘要后调 `Decide(ctx, ev.Text, summary)`；模板 `internal/orchestration/approver.go:710-718` 四个 `%s` = 摘要、权限、nonce×2）。**不含任务形态 readonly、工作树路径、HANDOFF_ARTIFACTS_DIR、允许路径**（事实：模板与调用点均无这些参数）。
- B452 放行：`internal/orchestration/readonly.go:177-189` `withArtifactsDir` 注入 env、`191-201` 续接/重启恢复注入、`203-206` 建 0700 目录；`manager.go:946-947,1049` 派发/启动时建目录并注入。提示词 `## 产物目录` 小节只给 readonly（`readonly.go:84-89`）。**permgate 产品代码零改动**：产物目录 `<DataDir>/tasks/<id>/artifacts` 落在 `Scope.TaskDir` 子树内，写它自动 AutoAllow，锁在 `internal/permgate/b452_artifacts_scope_test.go:13-29`。
- bash 的 cwd 在产物目录内：判据不跟踪 executor cwd，按命令文本解析（`permgate.go:225-271` + `cdbase.go` cd 逐项证明；相对路径按 `scope.Workdir` 拼接见 `internal/permgate/path.go:39-50`）。cwd=产物目录但写相对路径时，按 Workdir 解释→落 Consult（非 AutoAllow）；写产物目录**绝对路径**才 AutoAllow。`external_directory`：opencode 把越界目录填 `req.Paths`（`permgate.go:209-217` 注释），归属判定覆盖。

## 4. 规则层自动放行

- 唯一入口 `internal/permgate/permgate.go:160-173` `Judge`：`AutoAllow` 只出自写文件路由（`judgeFileWrite:179-201`，路径全在 Workdir/TaskDir/TaskTmpDir 内 + 文本不过黑名单；`len(Paths)==0` 直接 Escalate）与 bash 路由（`judgeBash:225-328`：rm-in-scope、heredoc 闭集、安全命令白名单 `safeCommandID`、复合只读 `compoundSilent`；白名单 id 表 `SafeCommandIDs:331-341`）。
- 维度：工具名（归一化见 `internal/executor/executor.go:116-163`）+ 命令模式（含引号/包装器/重定向识别）+ 路径范围。**不能按 readonly 区分**（事实：`Request`（`permgate.go:55-66`）与 `Scope`（`68-77`）均无形态字段，`Judge` 签名无 mode 参数）。

## 5. 执行器权限配置下发

- opencode：有。`WritePermissionConfig`（`internal/executor/opencode/taskenv.go:52-82`）：edit=allow，external_directory/webfetch=ask，bash=分级表且 `*` 强制 ask（`84-91`）；经 `OPENCODE_CONFIG=<taskDir>/opencode.json` 注入 serve（`internal/executor/opencode/proc.go:212-229`，保留键见 `49-52`）。`adapter.go:426` 启动时生成。
- codex：**刻意不写任何 config 文件**（`internal/executor/codex/taskenv.go:8-10`），全部协议级：`sandbox=workspace-write` + `approvalPolicy=on-request` + `approvalsReviewer=user`（`adapter.go:397-411`，每回合重钉 `491-505`），`sandboxPolicy.writableRoots=[taskTmp, gitCommonDir, gitDir]`（`adapter.go:152-171`），one-shot 走 `--sandbox read-only`（`oneshot_argv.go:8`）。
- grok：任务级 `GROK_HOME` + `config.toml` 钉 `permission_mode="default"`（`internal/executor/grok/taskenv.go:119-145`），ask 表含 Write/Edit/Bash 危险形/WebFetch（`47-58`），allow 为空（`60-67`）；`GROK_HOME`/`GROK_AGENT_SECRET` 为保留 env（`proc.go:128-131,259-265`）。
- claude：任务级 `settings.json`（ask 危险表 + allow 前缀白名单，`internal/executor/claudecode/taskenv.go:48-118`）经 `--settings` 下发（`proc.go:240`）。
- agy：任务 HOME `settings.json` 原生 allow `command(*)`（`internal/executor/agy/taskenv.go:41-43`，只消 soft-deny，不跳过 PreToolUse）+ `hooks.json` 的 `handoff-safety-gate`（`149-175`，matcher `*`，deny 仍是否决门）。

## 6. 截断

- 标记即 `executor.TruncationMarker = "…（已截断）"`（`internal/executor/executor.go:51`），追加方式 `turn.TruncateMarked`（`internal/executor/turn/text.go:36-42`）。
- 阈值三处：executor→manager 权限文本 64KB 防失控（`internal/executor/opencode/adapter.go:83-91`，`permTextHardLimit = 64<<10`）；`permission_request`/`approver_decision` **事件 payload 200 rune**（`internal/orchestration/manager.go:653-665` `permEventText`，超限 `TruncateRunes+标记`）；question 文本 8000 rune（`turn/text.go:16,74-80`，尾缀指 render.log）。
- 全文：工单存全文（`manager.go:2147-2148` 注释，`2170` `ticketRequest{Permission: ev.Text}` 未截断），经 `handoff show` 取（`turn/text.go:65-70` 注释写明事件截断、工单/任务目录有全文；`render.log` 存回合全文）。

## 7. 卡事件镜像

- `card dispatch` 经 `ledgerstep.Dispatcher.ViaTemplate`（`cmd/card_dispatch.go:406`）派发后落 dispatched 快照并**回链挂账** `RecordDispatchAndLinkTask`（`internal/ledger/events.go:172-179`，`TaskLink` 见 `internal/ledger/tasks.go:19`）；`ledgermirror` 子系统按 TaskLink 订阅各机任务事件流并以 `task_mirrored`（`source_target/source_task/source_seq` 三元组）写进 `card_events`（`internal/ledger/mirror.go:37-119`）。
- 裸 `dispatch --template` 只调 `client.Dispatch`（`internal/client/client.go:816-878`，`DispatchOpts:757-807` 无 card 字段）→ `/api/tasks`，**不建卡关联、不写 TaskLink、无人镜像**。这是两者差异的全部原因。
- 若加 `dispatch --card <id>`，需碰（只列落点）：`internal/client/client.go` `DispatchOpts`+`Dispatch` body、`cmd/dispatch.go` flag 与派发后挂账（复用 `ledgerstep.RecordDispatchAndLinkTask` 语义）、agentd `/api/tasks` 处理（`internal/agentd/dispatchroute.go`、`handlers.go`）、账本 `TaskLink` 投影（`internal/ledger/tasks.go`）、`ledgermirror` 订阅装配（`internal/ledger/ledgermirror/mirror.go`、`cmd/agentd.go:787-790`），以及 `card wait` 的唤醒消费（`internal/agentd/wakeconsumer.go:151-230`）。

## 8. 测试与文档

- 测试（行号为文件首行附近，代表用例名行以 `grep -n func Test` 为准，抽查）：
  - `cmd/b452_skilled_test.go:294` V5 `--wait`/`wait --final`；`cmd/b452_template_test.go:81` 模板正文含 receiver/mode/HANDOFF_ARTIFACTS_DIR；`cmd/b452_skilled_test.go:537` 无 artifacts_dir 即无产物。
  - `internal/orchestration/b452_readonly_test.go:117,347,391` 只读派发/产物目录注入/恢复；`b452_cardless_prompt_test.go:19` 无卡提示词形态。
  - `internal/permgate/b452_artifacts_scope_test.go:13` 产物目录 AutoAllow 锁。
  - `internal/client/b452_waitfinal_turn_test.go` 终态等待回合判据；`internal/orchestration/manager_test.go:1218` 工单存全文/事件截断；`internal/orchestration/permission_authority_seam_test.go` 重放幂等。
  - `internal/executor/opencode/regression_group_a_test.go:58` 64KB 截断标记；`taskenv_test.go:83,249` webfetch/external_directory ask。
  - `internal/agentd/b452_route_test.go:262` mode/artifacts_dir 三段解码；`b452_artifacts_api_test.go:76` 产物接口。
  - `cmd/card_wait_test.go` 镜像事件分类；`internal/agentd/wakeconsumer_test.go` task_mirrored 身份闸。
- roadmap（`docs/roadmap.md`）：B452 残余 `982-993`（`wait --final`/`dispatch --wait` 本回合判据残余窗口 `991`、最终文本协议尾注 `993`、跨机未登记 `990`）；审批链 `866`（cd+TMPDIR 升级人工）、`882-885`（黑名单不共享/只读展示/环健康）、`977`（内联 Python/heredoc 每次多一次审批链调用）；`504-509` 盲批论述、`531` 评估下线廉价模型审批者。
- specs：`docs/superpowers/specs/b452.md`（熟练工）、`docs/superpowers/specs/b451.md`（人工清单改走审批链）、`docs/superpowers/specs/b452-ledger.md`、`docs/superpowers/specs/b451-ledger.md`。查过：`docs/` 下无 B460 相关条目（grep `B460` 零命中）。

## 查不到

- `--wait` 遇工单即退出的专用退出码：现不存在（只有 0/3/4/124，`cmd/exit.go` 全量）。
- 审批提示词含 readonly/工作树/产物目录的证据：查过 `internal/orchestration/approver.go:690-718`、`internal/approval/client.go:320-343`、`internal/orchestration/approver_chain.go:1-120`，均无。
- dispatch wire 透传 card id：查过 `internal/client/client.go:757-878`，`DispatchOpts` 无 card 字段。
