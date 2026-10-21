# B452 implement 交回物（2026-10-21，subagent）

分支 `cards/B452-charter-1`，基线 `cf0c0ad6`。plan：`docs/superpowers/plans/b452-plan.md`（拍板节覆盖待拍板；Q7=乙）。过程与原始读数见台账 `docs/superpowers/specs/b452-ledger.md` 的「implement（2026-10-21，subagent）」节，本文只留结论与指针。

## 1. 偏离与自主决定

### 1.1 协调者授权的既有测试改写（§6 可改写清单的补充）

| 测试 | 改写 | 授权 |
|---|---|---|
| `internal/orchestration/manager_test.go` `TestDispatchPassesEnvToAdapter` | env 断言由一项全等改为两项全等：原有项 + `HANDOFF_ARTIFACTS_DIR=<DataDir>/tasks/<id>/artifacts`，精确值 | 协调者授权改写（2026-10-21，第一次）：F17 + Q7 乙的预期契约变更 |
| 同上 `TestDispatchPersistsCarrierHome` | 同上（`HOME=<载体 HOME>` + 产物变量），两项全等 | 同上 |
| `internal/agentd/receiver_dispatch_test.go` `TestB23327OrdinaryRemoteBindingRejectedWithoutDispatch` → 改名 `TestB452OrdinaryRemoteBindingForwardsWithoutLocalDispatch` | 注释写明 B452 S3 用转发取代 B233.27 的 400 行为。远端地址从 TEST-NET `192.0.2.9:7777` 改为「起后即关」的本地 httptest 地址（`deadPeerAddr`）；断言改为 502 + 点名 `linux-01`；「本机 Dispatch 0 次」「零任务」两条原样保留；不断言「若请求已送达」后缀 | 协调者授权改写（2026-10-21，第二次），附收紧条件 |

按第二次授权的收紧条件，自加的 V3(c) 也改用 `deadPeerAddr`，并撤掉对该后缀的断言。审计结果：本卡新增测试里没有 `192.0.2.*`、`198.51.100.*`、`203.0.113.*` 地址。

### 1.2 协调者已认可的偏离

- **续接与重启恢复路径（`resumeForContinue`、`ResumeTask`）只在产物目录已存在时注入 `HANDOFF_ARTIFACTS_DIR`。** B452 之前派发的任务没有这个目录，这两条路径不会凭空建一个。派发路径则对所有任务都建目录并注入。
- **终态回调在 `Server.SetManager` 里接线。** 做法是可选接口断言 `SetTerminalNotifier`；接口定义在 orchestration，实现在 agentd（`Server.TaskTerminal` → `releaseTaskCarrierOccupancy`）。
- **保留期配置键放在 `internal/config`**：`artifacts.retention`，类型 `*time.Duration`，nil 表示 168h，0 表示不清理，omitempty 不落盘。
- **workspace 的 detached 模式要求必须给 `Base`**，并与分支类参数、自带工作树、原地模式全部互斥。
- **`cmd/agentd.go` 只加了接线。** `RecoverOnStartup` 之后调用 `mgr.ArchiveCompletedReadonly()` 做启动补归档，并启动 `RunArtifactsRetention`（每小时一次）。这两项放在启动恢复之后、对外服务之前。
- **env 组装点有三处**（`Dispatch`、`resumeForContinue`、`ResumeTask`），各自调用 `m.env.For` + `withCarrierHome`，没有统一入口。这次各加一行，没有为此重构。

### 1.3 其他自主决定（在 §6「执行者可自行决定」范围内，或属实现落位补充）

段 1–2：
- `orchestration.CheckMode` 导出，供 `handleDispatch` 早挡非法 mode（F2）；它包装的是 `workspace.ErrBadDispatchRequest`。
- `OrchestrationClient` 接口加了 `UpsertMirrorTask`，`SchedulingClient` 接口加了 `SelectWhere`（agentd 用的是消费面接口）。
- `handleDispatch` 先保留原始请求字节；转发时在原始 JSON 上改键（加冻结身份四键、`mode`、只读时加 `new_worktree:true`，删 `machine`/`here`），不经结构体重编码，未知键原样过。
- 载体直派且载体在他机时，受理机先读载体登记判在线，再用 `BindPhysical` + `EffectiveModel` 定冻结身份。受理机不做 `AdmitCarrier`，名额零写入。
- 防环：请求带 `X-Handoff-Forwarded`、本身没有 `carrier`，却仍判出落点在他机时，返回 400 `errReceiverNotCarrierMachine`，不出站。
- 段 0 探针 3 不改产品代码，用 hub 订阅 + goroutine 内 `Done` 近似；段 1 V4 又以真实 `handleResult` 路径复验过。
- 以下回归锁在基线上就是绿的，按设计如此（锁的是「不得因新功能改变」），不是缺失功能：permgate、approval 两支权限门锁（`Gate.Judge` 直打、`Authority.Judge`）；V4 的 turn_failed、Stop 留树、基线缺失 400、与外部 done 并发四条；V2 执行机侧组合锁 `TestB452CardlessPromptReachesPlanContent`。

段 3–4：
- **拒用守卫同时放进 `ledgerstep.PreflightDiscipline`。** CLI `card dispatch` 和 agentd 卡节点共用这个前置解析；只在 `ViaTemplate` 拦的话，`target=here` 会先被调用方当成目标机解析，报出一个无关的错。两处共用私有 `rejectSkilledForCard`，报同一句话。只对熟练工定义生效，既有模板行为不变。**协调者追认（2026-10-21）。**
- **`--template` 下定义点名的 discipline 经账本解析**，走 `resolveCardDispatchDiscipline`，与卡派发是同一个缝 1 入口；显式给 `--discipline-file` 时它覆盖定义。只读定义可以不点名 discipline，此时只注入平台层。
- **能力位核对复用缝 1 的那次 Status。** 做法是 `statusOnceClient` 缓存包装；`resolveBareDiscipline`、`resolveCardDispatchDiscipline` 的签名被锁定，没有改。
- 定义的 `model_by_target` 不用于熟练工派发：落点由 agentd 决定，`--model` 可以覆盖。
- 任务名缺省取模板名。`--template` 拒收 plan 文件，且必须带 `--prompt`。
- `wait --final` 的 `--timeout` 沿用 wait 的语义（0 表示不设上限）；30m 缺省只属于 `dispatch --wait`。`wait --machine` 只能与 `--final` 同用。
- `--wait` 期间收到 question / permission_request 时，在 stderr 提示一行，stdout 契约不变。
- 产物取回失败时，最终文本照样输出，再以退出码 1 报产物故障，并给出重试命令 `handoff artifacts <id> --machine <M>`。
- `WaitFinal` 选「先读状态再定起点」（§6 允许的两法之一）：
  - 快照显示回合已结束：取最近 100 条事件里的最后一条回合结果；窗口里没有就从 0 回放到水位补找。
  - 回合进行中：从水位起跟流。
  - 任何断线都回到重取快照。水位只留在内存，不写 `~/.handoff/cursors`。
- 客户端解包「零文件落地」的兑现方式：tar 流不能倒带，所以任何一条条目不合规，就整体删除目标目录。总量上限与服务端同为 512 MiB。

### 1.4 未做 / 留给后续

- **跨转发的自动补登记不做（Q13）。** CLI 识别到「执行机 <M>：」前缀加「项目未登记」时，跳过补登记，并点名去哪台机器登记。finish 时 roadmap 残余记一行。
- **502 文案的「若请求已送达…」后缀**照协调者裁决不断言（B448 残余），产品文案里仍然保留。
- **观测项**：小队裸派发改走 `SelectWhere`→`AdmitFrozen` 后，满员竞态得到 409，不再顺延到下一个成员（Q2，顺延归 B453）。`IgnitionRequest.Target` 仍然只进日志。
- **消费点审计**（plan §7「枚举新值过白名单」）：
  - 非测试代码里 `Branch == ""` 共 7 处：
    - `workspace.TaskDiffTarget`：空分支回退到 workdir HEAD，安全；
    - `handleBundle`：返回 400「无可同步」；
    - `cmd/pull.syncTaskBranch`、`localsync`：报错；
    - 其余 3 处只判断请求参数，不涉及任务。
  - `TaskStateWaitingReview` 的承重消费点是 `RecoverOnStartup`（只读任务随后由 `ArchiveCompletedReadonly` 补归档）和 `handleDone`。
  - `autoSyncAfterWait` 不在 `--final` 路径上。

## 2. 故事 → 提交 → 测试

| 段 | 提交 |
|---|---|
| 0 | `41b73f48` test(B452): 段 0 三条承重假设探针（双 agentd 冻结派发、detached 树、goroutine 内 Done） |
| 1 | `f4759f73` feat(B452): 段 1 只读形态与产物目录（执行机侧） |
| 1 | `a4139f18` feat(B452): 派发路径注入 HANDOFF_ARTIFACTS_DIR（F17），协调者授权改写两支 env 全等断言 |
| 2 | `35cb501a` feat(B452): 段 2 跨机路由、机器亲和、按机跟随与产物接口 |
| 2 | `0f9a9b45` test(B452): 协调者授权改写 B23327 普通远端派发用例为转发语义，死地址改用起后即关的本地服务 |
| 3 | `129bdfc2` feat(B452): 段 3 熟练工 CLI——template 字段与校验、dispatch --template/--wait/--here/--machine、wait --final、handoff artifacts、退出码 3/4 |
| 4 | `d2c5d4b7` docs(B452): 段 4 熟练工示例定义 code-investigate 与 SKILL.md「熟练工」一节 |

| 故事 | 提交 | 机内验收（测试） |
|---|---|---|
| S1 定义字段与校验（V1） | 129bdfc2 | `ledger`：`TestB452TemplateDefSkilledRoundTrip`、`TestB452LegacyTemplateDefBytesUnchanged`、`TestB452TemplateDefValidate`、`TestB452IsSkilledTargetHere`；`cmd`：`TestB452TemplatePutSkilledRules`；`ledgerstep`：`TestB452ViaTemplateRejectsSkilledDef`、`TestB452PreflightRejectsSkilledDef` |
| S2 按名派发（V2） | 129bdfc2 | `ledgerstep`：`TestB452RenderCardlessPrompt`、`TestB452RenderTemplatePlaceholders`；`cmd`：`TestB452DispatchTemplateBody`、`TestB452DispatchTemplateOverridesAndAffinity`、`TestB452DispatchTemplateRejectsBeforeNetwork`（含模板不存在零请求）、`TestB452ReadonlyDirtyNoticeAndBaseCommit`；执行机侧：`orchestration` `TestB452CardlessPromptReachesPlanContent` |
| S3 跨机转发（V3、V8） | 41b73f48、35cb501a、0f9a9b45 | `agentd`：`TestB452PairFrozenDispatchOverRealHTTP`、`TestB452ForwardCarrierToRemoteMachine`(a)、`TestB452ForwardSquadPicksRemoteMember`(b)、`TestB452ForwardUnreachableRemote502`(c)、`TestB452ForwardedOrdinaryRequestNeverFansOut`(d)、`TestB452ForwardReadonlyToOldAgentdRefused`(e)、`TestB452ForwardRelaysRemoteErrorWithPrefix`(f)、`TestB452ForwardWireCarriesFrozenIdentityAndMode`、`TestB452TaskJSONOmitsEmptyMode`(V8)、`TestB452OrdinaryRemoteBindingForwardsWithoutLocalDispatch`；`cmd`：`TestB452DispatchForwardedProjectErrorSkipsAutoRegister`（Q13） |
| S4 只读形态（V4） | 41b73f48、f4759f73、35cb501a | `workspace`：`TestB452ProbeDetachedWorktreeLifecycle`、`TestB452PrepareDetachedWorktree`、`TestB452PrepareDetachedRejectsBranchParamsAndNonManaged`；`store`：`TestB452TaskModeRoundTrip`、`TestB452OpenMigratesModeColumnOnLegacyDB`；`orchestration`：`TestB452ProbeDoneFromGoroutineAfterCompletedNoDeadlock`、`TestB452ReadonlyDispatchDetachedNoBranch`、`TestB452ReadonlyCompletedAutoArchives`、`TestB452ReadonlyTurnFailedStaysForReview`、`TestB452ReadonlyAutoArchiveRacesExternalDone`、`TestB452ArchiveCompletedReadonlyOnStartup`、`TestB452ReadonlyRejectsBranchParamsAndBadMode`、`TestB452ReadonlyBaseCommitMissingRejected`、`TestB452ReadonlyStopRetainsWorktree`；门面：`agentd` `TestB452ReadonlyForwardedCompletesAndReleasesSlot` |
| S5 等结果回文（V5） | 129bdfc2 | `client`：`TestB452WaitFinalCompletedInStreamByMachine`（含不写 cursor）、`TestB452WaitFinalReconnectsAfterDrop`、`TestB452WaitFinalLastTurnAfterContinue`、`TestB452WaitFinalNonCompletion`、`TestB452WaitFinalReplaysWhenResultBeyondWindow`、`TestB452WaitFinalDeadline`、`TestB452AttachOnNotFoundIsPermanent`；`cmd`：`TestB452DispatchWaitCompletedStdoutIsExactlyFinalText`（全等断言）、`TestB452DispatchWaitNonCompletionExit3`、`TestB452DispatchWaitTimeoutExit124NoStop`、`TestB452WaitFinalLastTurnAndFlagConflicts` |
| S7 机器亲和（V6） | 35cb501a、129bdfc2 | `scheduling`：`TestB452SelectWherePredicateFilters`；`agentd`：`TestB452AffinityRefusals`(a)(b)(d)、`TestB452AffinityMachineForwardsAndFullIs409`(c)(e)；`cmd`：`TestB452DispatchAffinityRefusedExit4`、`TestB452DispatchTemplateRejectsBeforeNetwork`（`--here`×`--target`、`--here`×`--machine`）、`TestB452DispatchTemplateOverridesAndAffinity`（flag 覆盖定义 target）、`TestB452DispatchCapabilityBitRequired`（F13） |
| S8 产物通道（V7） | f4759f73、a4139f18、35cb501a、129bdfc2 | `orchestration`：`TestB452ArtifactsDirAndPromptSection`、`TestB452ResumePathsInjectArtifactsEnv`、`TestB452SweepExpiredArtifacts`；`config`：`TestB452ArtifactsRetentionParse`；权限门锁：`permgate` `TestB452ArtifactsDirWithinTaskDirScope`、`approval` `TestB452AuthorityArtifactsWriteAutoAllowHomeEscalates`；`agentd`：`TestB452ArtifactsEndpoint`（204/200/符号链接/`?machine=`/413）、`TestB452ArtifactsEndpointUnknownTask404`、`TestB452EventsWSFollowsMachine`（F14）；`client`：`TestB452ArtifactsDownloadStatuses`、`TestB452ExtractArtifacts`；`cmd`：`TestB452ArtifactsCommand`、`TestB452DispatchWaitPrintsArtifactsLine` |
| S6 示例与 skill | d2c5d4b7 | `cmd` `TestB452SkilledExampleDefinitionPuts`（真 `template put` 入仓 JSON）；`internal/skill` 与根包测试（SKILL.md 经 go:embed）绿。真机组合由 R7 验 |

## 3. 先红后绿证据摘要

每段的编译红、断言红原文都在台账里，这里只按段列要点：

- **段 0**：三条承重假设的探针直接通过，都没有改产品代码。
- **段 1**：
  - detached 先红：临时短路后跑出 `detached 结果 = {Branch:handoff/b452aaaa …}`；
  - store 先红：`GetTask Mode="" want "readonly"`；
  - 编排先红：`工作区不干净`、`产物目录应可写`、`branch × readonly 应 ErrBadWorkspaceReq，得 <nil>`；
  - 启动补归档与保留期清扫：用 `return 0` 变异取红；
  - F17 注入行的变异复核：去掉注入行后三支测试红。
- **段 2**：
  - 竖切测试先于实现跑，11 支全红，例如 `A 受理跨机载体直派应 200，得 400 …接收机不是载体登记机`、`StatusPolicyViolation … task not found`；
  - `SelectWhere` 在壳实现下先红；
  - 产物接口用变异取红：摘路由得 404、关闭 `?machine=` 分支得 task not found。
- **段 3**：
  - ledger：编译红 `unknown field Receiver`，桩实现下 7 条断言红；
  - put：先红 `三者必填`；
  - ledgerstep：编译红；守卫缺位时 receiver、mode、target=here 三条都得 `<nil>`，其中 target=here 真的走到了 Transport；`PreflightDiscipline` 先红；
  - client：编译红，另有三处变异红（正序取首回合、流不带 machine、解包失败不删目录）；
  - cmd：编译红，另有五处变异红：`--wait` 的 JSON 打到 stdout（plan 变异锚点之一）、422 不映射、能力位不核、转发错误照走补登记、同机判据关掉。
- **段 4**：示例定义的锁测写好即绿；它是对入仓文件的锁，不对应新功能的红。

## 4. 收尾检查与全量结果

- `go build ./...`：通过。`go vet ./...`：通过（`VET_OK`）。
- 对 `git diff --name-only cf0c0ad6 HEAD -- '*.go'` 跑 `gofmt -l`：输出为空。
- 全量 `env -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT go test ./... -count=1`：64 个包 `ok`，8 个包 no test files，只红 1 支：

  ```
  --- FAIL: TestProductionHTTPClientCallersAreGatewayOnly (1.06s)
      execution_test.go:176: 生产代码不得自取 HTTPClient 拼请求（…）: .claude/worktrees/agitated-bun-cd484f/internal/agentd/drop.go, … （名单全为 .claude/worktrees/* 嵌套工作树文件）
  FAIL	github.com/Xsxdot/handoff/internal/client	10.653s
  ```

  这是环境性失败（memory「handoff env-sensitive tests」已记）。复核方法：在 scratchpad 里 `git clone --branch cards/B452-charter-1` 一份不含嵌套工作树的干净副本（HEAD `d2c5d4b7`）。副本里 `go test ./internal/client/ -count=1` 得 `ok … 9.975s`，`-run TestProductionHTTPClientCallersAreGatewayOnly -v` 得 `--- PASS (0.02s)`。复核完副本已删除。
- PG 测试腿按约定保持跳过，没有取凭据。没有启停任何 agentd，没有碰远端机器，除测试夹具外没有写任何账本库。
- 工作树：除既有的 `.playwright-mcp/`（未跟踪）外干净；`docs/superpowers/specs/b434-ledger.md` 全程未动、未暂存。

## 5. 协调者真机需核（§5.3 R1–R8 与未验项 (a)–(d)）

- **R1 靶子与 S1**：两隔离实例 `/api/status` 带 `skilled_dispatch_supported:true`。入仓 JSON 只换 `receiver` 为 `t452-oc` 后 `template put` 进测试库，两端 `template show` 同版本。**注意：所有 CLI 都要带 `--config <草稿配置>`**——`dispatch --template` 现在会直接开账本（`openLedger`），漏带就读到生产账本。
- **R2 单机只读**：
  - stdout 应是最终文本，加一行 `artifacts: <原路径>`（同机判据：本机能 Stat 到 `artifacts_dir`）；
  - 派发中多一棵 detached 树，结束后消失；`git branch` 前后一致；
  - 任务 completed，事件 `completed` → `archived`，note 为「只读任务完成，自动归档」；名额回到 0。
  - 这里同时验 **(a)**、**(c)**、**(d)**：
    - **(a) 阻塞级**：若出现 `permission_request`，记录载荷、回主会话，不改权限门；
    - **(c)**：detached 树里 `rev-parse --abbrev-ref HEAD` 得 `HEAD`，且 trailer 被判为完成；
    - **(d)**：goroutine 内自动归档不与真实 opencode serve 互锁，serve 进程退出。
- **R3 跨机**：
  - 受理机日志「dispatch 已转发到执行机」与执行机的任务行按任务 id 对上；
  - stdout 产物行指向 mac 临时目录 `handoff-artifacts-<id8>-…`，`shasum` 与执行机原文件一致；
  - 不带 machine 的 `handoff show <id>` 当即可见（镜像行即时登记）；
  - 名额只在执行机侧起落。
  - 这里同时验 **(b)**：`/ws/events?machine=` 能持续跟随 10 分钟以上的任务。
- **R4 跨机失败**：停掉 linux 实例后派发，应得 502 并点名 `t452-linux`，两边零新行。恢复后重派成功。
- **R5 亲和**：
  - `--here` 对他机载体、小队本机无成员：都应得退出码 4，文案以「机器亲和不满足：」开头，点名机器 / 列出成员@机器，两机零新行；
  - `--machine t452-linux`：应转发成功。
- **R6 产物**：`handoff artifacts <id> [--machine]` 再拉一次内容一致。配 `artifacts.retention: 2m` 后，`artifacts/` 应被删掉、任务目录其余文件保留。**清扫 ticker 间隔是 1h，只有启动时立即跑一轮**：plan 写的「重启后等 3 分钟」观察不到删除，要等任务终态超过 2m 之后再重启一次触发首轮（或观察下一个整点轮次）。之后 `handoff artifacts` 应得 stderr「任务无产物」、退出 0。
- **R7 S6 组合**：用入仓 JSON 原文（只换 receiver）问一个真实调查题；stdout 应是结论，产物里有 report.md。
- **R8 版本偏斜**：
  - 新 CLI 对老 agentd 发 `--template`：应因能力位缺席被 CLI 拒发，零 `POST /api/tasks`；
  - 新受理机转发只读请求到老执行机：应得 502「…版本过旧，不支持只读形态…」。
- 收尾提醒：生产账本的 `template put code-investigate`（receiver=mimo）留到 finish 换版后单独请示；roadmap 残余补一行「跨转发自动补登记（Q13）」。

## 6. 修复轮（2026-10-21）

独立审查两轴通过后，协调者给出六条裁决。按 implement 纪律执行：先红后绿，一条逻辑修复一个新提交，不 amend。原始读数见台账「implement 修复轮」小节。

| # | 提交 | 改动 | 先红 / 变异红 |
|---|---|---|---|
| 1 | `c79e9d42` | `--template` 接受 plan 文件代替 `--prompt`。plan 文件内容即「## 本次任务」，组装结果走 `plan_b64`。**同给时的选择**：照裸派发语义两者合法，`--prompt` 原样走 `prompt`，由 agentd 既有规则拼成「## 附加指令（派发时提供）」接在最后。SKILL.md 与 flag 帮助同步。 | `cmd/b452_plan_file_test.go`：CLI 捕获的请求体喂给真实 `orchestration.NewManager(...).Dispatch`，断言执行器收到的 `PlanContent`。先红为「--template 不接 plan 文件」。 |
| 2 | （无代码） | `PreflightDiscipline` 的熟练工拒用守卫：**协调者追认（2026-10-21）**，已记入 1.3 节与台账。 | — |
| 3 | `f9901145` | `WaitFinal` 在 waiting_review 下，只有能证明结果属于本回合才下结论，否则退避重取快照，判据见下。completed / failed 下没有本回合结果，才判「未完成就归档」（退出码 3）。 | `internal/client/b452_waitfinal_turn_test.go` 四支：首回合落在「迁状态 → 落结果」之间、续接任务不得返回上一回合、亲见水位以内的结果不采信、结果后有写入时两次快照确认（不无限等）。四支先红。 |
| 4 | `8a3c7fb9` | `artifacts_dir` 为空即按无产物处理：不下载，stdout 只有最终文本，退出 0。`handoff artifacts` 同样适用。 | `TestB452DispatchWaitNoArtifactsDirMeansNoArtifacts`。先红：多出一行 `artifacts:`，且真去下载了。 |
| 5 | `d992bc38` | `SetManager` 的可选接口断言失败时，在启动时打一行 Error：「编排实现不支持终态回调接线：只读任务自动归档后不会归还载体/小队名额」。接线位置不变。 | `TestB452SetManagerWithoutNotifierLogsErrorOnce`，先红为空记录；同一测试反向断言真实编排实现不打这行。 |
| 6 | `dfb014cc` | 补四支直接锁：F14 带转发头时忽略 machine、不再转发；F6 亲和拒绝零出站（计数对端）；F2/F5 在 HTTP 层返回 400，零出站、零任务；F12 `/api/status` 原始 JSON 为 `true`。顺带 gofmt 了 `d992bc38` 漏格式化的测试文件。 | 四支在现码上即绿，属锁测；变异取红：去掉转发头判断、短路 `CheckMode` / `checkAffinityFields`、亲和谓词置 false、不上报能力位。 |

**第 3 条的判据，以及残余边界。** 读码确认两个事实：`handleResult` 先 `transitToReview` 再 `AppendEvent`；`Continue` 只迁到 running，不落任何事件。所以库里没有「回合起点」记录。waiting_review 下按以下三条判断结果是否属于本回合：

1. **本次等待亲见过「进行中」，且结果的 seq 大于亲见时的水位。** 这一条是确定性的。`dispatch --wait`，以及所有在运行中开始的等待，都走这条。
2. **结果的 `created_at` 不早于任务的 `updated_at`。** 迁入 waiting_review 必然先于本回合结果写库；上一回合的结果必然早于续接与迁入。这条只会假阴，不会假阳。
3. **兜底：隔一次退避的前后两次快照，都是 waiting_review，且最后一条结果的 seq 相同。** 用来兜住第 2 条的假阴——结果落库后，清扫作废工单之类的写入会把 `updated_at` 顶高，没有第 3 条就会无限等。

残余边界只有一种：首个快照就已是 waiting_review，且结果落库被拖慢、超过一次退避（生产 ≥1s）。此时第 3 条仍可能采信上一回合的结果。第 1、2 条不受影响。要彻底消除，需要编排在续接时落一个回合起点事件。这超出本卡改动边界，未做；如需要，记 roadmap。

**复跑结果：**
- `go build ./...` → `BUILD_OK`；`go vet ./...` → `VET_OK`；改动 Go 文件 `gofmt -l` → 空。
- `go test -race -run B452 -count=1` 三个包：
  - `ok github.com/Xsxdot/handoff/internal/orchestration 7.130s`
  - `ok github.com/Xsxdot/handoff/internal/agentd 7.512s`
  - `ok github.com/Xsxdot/handoff/internal/client 1.997s`
- 全量 `env -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT go test ./... -count=1`：63 个包 ok，8 个包 no test files，红 2 支：
  - `TestProductionHTTPClientCallersAreGatewayOnly`：环境性，名单全为 `.claude/worktrees/*`，同第 4 节。
  - **新出现** `--- FAIL: TestProbeReplayClassification/S3 … S3 回放未产出任何终结事件，实际事件 [usage progress usage]`（`internal/executor/opencode`）。本卡对 `internal/executor/` 零改动（`git diff --name-only cf0c0ad6 HEAD -- internal/executor/` 为 0 行）。单跑 `-run TestProbeReplayClassification -count=5` 得 `ok … 21.937s`，整包复跑得 `ok … 28.318s`。判为全量负载下的时序抖动，不是本卡回归；建议 review 复跑时留意。
