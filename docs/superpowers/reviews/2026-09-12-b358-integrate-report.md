# B358 集成报告（integrate 节点，2026-09-12）

- 父卡：B358（会话即工作单元）。子卡 B358.1–.7 + B366 全部完结后集成收口。
- 分支：`cards/B233.1-charter-7`（集成分支即工作分支，全部子卡工作直接落在本分支，无独立子分支合并动作）。
- 集成起点：`00fef135`（implement(B366)）。
- 执行者：charter:integrate 节点。红线：并入主线与归档留协调者裁决，本文到「集成分支就绪 + 报告」为止。

---

## 一、recon 图对账（charter:recon 纪律）

**改动面来源**：八张子卡台账的「符号清单/图覆盖债」节（B358.1–.7、B366）+ breakdown §6 图覆盖债声明。

### 1.1 视图 diff 补齐（`codegraph/diffs/cards-B358-charter.json`）

按 B233.6 契约冻结的既有条目样例格式补齐，锚行号逐一在树上实测（grep 函数/类型声明行）：

| 项 | 数量 | 明细 |
|---|---|---|
| 新增 nodesAdded | +86 | Go 33（proto SessionWake/SessionCite、collab sessionWriters/sendToSession/consumeRoomMentions/sessionNodes/structEventFields/sessionTimeline、agentd decodeRoomMessageForWake/validSessionOwner/sessionErr/automationWakeEvents/roomMessageWakeEvents/七 handler、cmd runSessionWait/buildSessionWake/validSessionMemberIdentity + 8 个 RunE）+ 入口 16（CLI `session` 根+8 子命令 9 支、HTTP 会话七端点 7 支，均标 `unscanned: true`）+ web 37（rooms.ts 七函数+11 类型、sessionModel 九符号、SessionSidebar/SessionChat/SessionDetail/SessionTab/NewSessionDialog/JoinCardDialog、sessionBase、Shell 三内部函数） |
| 新增/替换 nodesModified | +12 | Service.Send（行 80→86、执法路径编排摘要）、room.VerifyWriter（终形签名+signatureOld）、room.Resolve（59）、m_collab_room_Room（41、删 ParentSession 字段）、agentd automationWakeEvent（148）、consumeAutomationEventsOnce（315）、registerLedgerRoutes（30）、m_collab_client_LedgerClient（**补 ListAllCards 漏项**，接口 16 方法全量核对，行 19→26）、TS RoomMessage（+reply_to）、dedupKey（95，修正基线 mangled 签名）、tabTitle（579）、breadcrumbSegments（25，B366 home tail 放行）、Shell（114） |
| nodesDeleted | 11 | `n_collab_room_KindAllowed`（B358.1 删除，树上 grep 零命中）+ 10 个 web 退役节点（RoomPanel.tsx / roomPanelModel.ts 全族，B358.6） |
| 既有条目锚行修正 | 10 | ledger/sessions.go 五方法（ArchiveSession 137→140、JoinCardToSession 158→168、LeaveCardToSession 194→204、AddSessionMember 226→236、SessionOfCard 257→267——B358.1 拍板①短路扩行使行号后移）；proto/sessions.go 五 DTO（SessionTimelineEvent 117→123、SessionDetail 128→134、三个 Payload 135/142/147→141/148/153——补签轮 R3 词表扩行使行号后移） |
| containersAdded | 1 | `k_web_api_rooms`（TypeScript 函数组，domain d_web——基线缺失该容器，best.json 有声明，兄弟容器先例 B233.5） |
| 不入图（记账不补） | — | ①10 个 cmd flag 载体变量（sessionCreateOwner 等）——按扫描配方「包级函数/receiver 方法/struct」口径非符号；② TabContent/WorkbenchPageProps 的 union/字段增量——基线对这两节点不记 fields，节点形状无可表达变更；③ lifecycle writer 关系（ArchiveSession 写 Archived 等）——沿用契约轮「只记 creator」的视图惯例；④ 测试符号——按图惯例挂 `tests` 数组、不立节点，本轮沿契约条目惯例未附；⑤ 调用边——见 1.4。 |

### 1.2 validate 读数

```
$ codegraph validate --view cards-B358-charter
本视图归因问题：0（总 issues 132，全部为基线声明/其他视图存量；补图前总数 134——本视图 nodesDeleted
顺带消解 2 条既有悬空引用，只减不增）
```

### 1.3 check 读数（与基线 6 条逐行比对）

```
$ codegraph check --view cards-B358-charter
fails 恰 6 条，与各子卡 Task 0 基线逐字一致（只多不少 = 通过）：
  [dead-interface] d_gateway→d_orchestration "OrchestrationClient"
  [over-budget] d_cli->d_workspace 11/9 · d_gateway->d_workspace 17/1 ·
                d_orchestration->d_workspace 49/19 · d_workspace->d_orchestration 3/2 ·
                d_workspace->d_protocol 23/3
```

### 1.4 棘轮提请

**无**。集成接线未让任何接缝的存量直调下降——check 六条 fail 的数字与基线全同（11/17/49/3/23），`legacyHits` 37 边 map 亦无下降。`target.json` 本轮零改动（纪律照办）。

备注（留人，不擅改）：新增跨包 import 两处——`cmd → collab/cursor`（B358.3 组装点绑定，plan 裁定 7 申报）、`agentd/sessionsapi → ledger/api.Facade 经 Server.autoLedger`（契约 §2.2 B366 F3 澄清的明文形状）。图边未回灌（见 1.1 不入图⑤）：d_cli→d_collab 契约预算 0，若在 absorb 前补真边会使 check 新增 fail，边回灌须与预算裁决同批，归 finish/absorb 机制，不在 recon 擅动。

### 1.5 sym 抽查

`codegraph sym --view cards-B358-charter` 抽查 6 发全命中：`n_collab_Service_sessionWriters`、`n_agentd_Server_handleSessionMemberAdd`、`e_cli_session_wait`、`n_web_api_rooms_addSessionMember`、`n_web_app_rooms_SessionTab`、`m_proto_SessionWake`。

---

## 二、全量核算（三段律全量的法定位置）

### 2.1 编译与包组

| 命令 | 读数 |
|---|---|
| `go build ./...` | 退出 0 |
| `go test ./... -count=1` | 53 包 ok；失败 6 包（逐包归因见 2.3） |
| `cd web && npx vitest run` | **128 files / 1358 tests 全绿** |
| `cd web && npm run typecheck` | tsc -b 退出 0 |

### 2.2 红窗最终态核算（逐支比对）

| 窗 | 终态要求 | 实测 | 判定 |
|---|---|---|---|
| `go test ./internal/agentd` | 恰 {B362 golden} 1 支 | 恰 `TestLegacyNodeEventSequenceUnchanged` 1 支（B362 禁触面，原红因漂移） | ✓ 恰等 |
| `go test ./cmd` | 恰 2 支 | 恰 `TestRepoContractGate` + `TestServePermissionHookDenyWithReasonAndStep0` | ✓ 恰等 |
| TestRepoContractGate | 6 条逐行一致 | 1 dead-interface + 5 over-budget，数字全同基线（见 1.3） | ✓ 逐行一致 |

多/少/漂移：无。**红窗核算通过，不熔断。**

### 2.3 红窗外失败的归因（全量 `./...` 首次暴露，非接缝缺陷）

| 失败 | 归因 | 证据 |
|---|---|---|
| `internal/client` TestProductionHTTPClientCallersAreGatewayOnly | **环境性存量**：全仓源码守卫扫进主仓磁盘残留的 `.claude/worktrees/*`、`.worktrees/*` 旧副本（b156/b163/b227/b250 时代）；守卫只跳过 .git/vendor/node_modules/web | 干净 worktree @ 预基线 7c8bc65f 复跑 **ok**；守卫测试代码在 7c8bc65f..HEAD 零改动；B358 触及面不含该包 |
| `internal/executor/agy` 4 支（perm 族） | **环境性存量**：macOS TMPDIR 全路径 116 字节超 unix socket 107 上限，`newPermServer` bind 即败 | 预基线 7c8bc65f worktree 复跑**同红**（同根因）；B358 零触及 |
| `internal/executor/opencode` TestCoordinatorCancelTurnUsesSessionID | 并发时序翻抖 | 单包复跑 **ok** |
| `internal/hostapi` 2 支（WakeHome 族） | 真进程 1s 超时的时序翻抖 | 单包复跑 **ok**（全量并发负载下超时） |

处置建议（留协调者，不越界动手）：主仓 `.claude/worktrees/`、`.worktrees/` 残留副本做一次卫生清理（`git worktree prune` + 盘点删除），agy 套件的 socket 路径上限与 TMPDIR 长度的兼容另议。

---

## 三、契约回写（协调者已裁决的机械项，两处）

1. **§9 销区**（`docs/superpowers/specs/b358-contract.md`）：附区标题下标注「**已由 plan〈b358.1/2/3 实现计划〉吸收（2026-09-12）——本区销账**」，原七条实现级落点撤除（已分别落进三份实现计划），留指针行。契约其余内容零改动。
2. **§2.2 补澄清**（B366 review F3）：入站门面段末尾增一行——「**B366 review F3 澄清**：gateway 对 §3.5 无 Service 包装的冻结 LedgerClient 面（如 AddSessionMember）允许直调账本薄门面 Facade 并引用其哨兵——d_gateway→d_ledger 边 entry 明文形状，不构成越层。」

## 四、顺手修（B366 review F2）

`internal/agentd/sessionsapi.go:7` 文件头注「账本能力经 `s.ledger` 既有直调面触达」→「账本能力经 `s.autoLedger` 既有薄门面触达」。一行注释、零行为；gofmt 干净、`go build ./internal/agentd` 过、grep 确认新旧文案各 1/0 命中。

---

## 五、生产闭环对账（breakdown §4 逐行三态）

三态口径：**已验证** = 支撑测试已在各子卡 review/implement 验证，且本轮全量核算中实测绿（agentd/cmd/collab/proto 包内全部 B358 测试 + web 1358 支均在绿侧）；**缺口** = 触发者/载体/消费者任一格在生产无着落。

| # | 行为（breakdown §4 简写） | 三态结论 | 证据（生产载体 → 测试锚） |
|---|---|---|---|
| 1 | 开会话说需求 → 列表新行、无人被唤醒 | 已验证（S4/S6/S3 卡 + 本轮实测） | handleSessionCreate/CreateSession；TestSessionsCreateEndpoint、TestB3583StructureEventsNeverWake；Sidebar 行渲染 |
| 2 | 拉卡进群 → 卡条 + 空座虚线、SessionOfCard、不唤醒 | 已验证（S4/S6/S3 + 本轮实测） | handleSessionJoinCard/JoinCardToSession；TestSessionJoinCardEndpoint；SessionChat 卡 chips `空座 · 还没配人`（树上 grep 实测在产） |
| 3 | 无寻址发言 → 全员未读+1、零 Wake | 已验证（S1/S3/S6 + 本轮实测） | Service.Send→EvRoomMessage；TestB3583NoAddressingDoesNotWake；session-unread 徽章（grep 实测在产） |
| 4 | @卡号（有席位）→ 协调者回合拉起 | 已验证（S3 卡，fake keystone 形状）；真机推醒事实归真机清单 #2 | TestB3583MentionCardWakesItsSeatOnce；ResolveDelivery+MessageWakeTargets |
| 5 | @空座/非成员 → 零 Wake/入未读不唤醒 | 已验证（S3 + 本轮实测） | TestB3583MixedHitsNeverMergeOrBroadcast、TestB3583NonMemberAndMissingCardTargetsDoNotWake |
| 6 | 人回复协调者消息（reply_to）→ 原作者寻址 | **缺口（席位作者唤醒半边机内已验）：生产无 reply_to 触发者——消费面全在（collab resolveMessageTarget、room.ResolveDelivery/IsAddressed、cmd buildSessionWake、TS 渲染），但无任何生产发送路径写 ReplyTo（树上 grep 实测：发送面 sendRoomMessage/session send 均无该字段来源）。去向：B365 在途，不冒充闭环** | TestB3583ReplyToSeatAuthorWakesItsCard（夹具构造事件） |
| 7 | 换绑后再 @卡号 → 新席位醒、旧席位剥权 | 已验证（S1/S3 + 本轮实测） | TestSessionRebindRevokesOldSeatWriter、TestB3583ReboundCardMentionHitsNewSeat、TestB3583ReplyToReboundOldSeatDoesNotWake |
| 8 | 推翻级 @主 agent → 命中条+引用条+未读数（R1） | 已验证（R1 处置已落地为 session wait 通道，机内形状全验）；真机推醒归真机清单 #1 | B358.3 Task 4 全族（OutputsHitJSON/ReferencedAndUnread/MemberExactMatch）；B358.7 纪律节命令形状对账一致 |
| 9 | 主 agent 判不了 @人 → 人被唤醒（R1） | 同上（session wait 面向外部身份 member） | TestSessionWaitMemberExactMatch（逐字相等+变异）、TestSessionWaitReferencedAndUnread |
| 10 | needs_human 亮起 → timeline 行 + 列表标签 | 已验证（S2/S6 + 本轮实测） | TestSessionTimelineNeedsHuman（含归属守卫变异）；SessionSummary.NeedsHuman；Sidebar needsOnly 过滤 |
| 11 | 卡收口或终止 → card_closed 行（R3） | 已验证（R3 处置落地 S2 + 本轮实测） | TestSessionTimelineSeatBoundAndCardClosed（含变异：普通转移不得记行） |
| 12 | 协调者入群（坐下）→ seat_bound 行（R2） | 已验证（R2 处置落地：BindSeat 同事务落 EvDriverSeatBound + S2 消费） | TestBindSeatFallsDriverSeatBoundEventInSameTransaction、TestSessionTimelineSeatBoundAndCardClosed |
| 13 | 换绑 → 仅本会话卡 seat_rebound 行 | 已验证（S2 归属修复 + 本轮实测） | TestSessionTimelineBelongsToOneSession（takeover 全量卡表偏差修复） |
| 14 | 归档会话 → 发言/拉卡被拒、UI 只读 | 已验证（S1/S4/S6 + 本轮实测） | TestSessionArchiveSendReadOnly、Store 幂等短路、TestSessionArchiveEndpoint（409「已归档」）、web archived 金样本 |
| 15 | 看成员状态 → 只报 last_active/empty 绝不报在线 | 已验证（S2/S6 + 本轮实测）；真机 #5 | TestSessionMemberStatusHonest（双变异）、web 词表闭包 online 反例变异、proto 四值词表 |
| 16 | 看协调者派发的任务节点 | 已验证（S2/S6 + 本轮实测） | TestSessionNodesAggregation（守卫变异）；SessionDetail nodes 渲染 |
| 17 | 会话 @ 提醒与清理 → 收件箱/未读、回复即清 | 已验证（S1/S3/S4 + 本轮实测） | TestMentionClearScopedToSessionRoom（双会话反例+变异）、TestInboxMentionSource 等 5 支收口 |
| 18 | 主 agent 定时巡场（拉）→ 标签/状态/节点可查 | 已验证（S4/S5/S7 + 本轮实测）；走查归真机 #7 | TestSessionListColumnsAndUnread、TestSessionDetailThreeSections；B358.7 新节命令形状对账逐条一致 |

**闭环结论**：18 行中 17 行闭环成立（每行触发者会写、载体在、消费者会读、结果可观察）；1 行（#6 reply_to）生产触发者缺位——按纪律如实标「缺口，B365 在途」，卡已在 backlog 在途，不属于「只活在测试里却冒充完成」的情形。契约错配暴露数：0（integrate 纪律第 2 件——本轮未发现任何「实现与契约语义相左」的错配，三处契约回写均为澄清/销账，非错配）。

---

## 六、接缝缺陷记账（integrate 纪律第 4 件）

| # | 缺陷 | 发现处 | 现象 | 根因 | 接缝归属 | 去向 |
|---|---|---|---|---|---|---|
| 1 | reply_to 无生产面 | B358.5 plan 阶段拍板立卡（本轮汇总复核坐实：树上 grep 生产码零写者） | 「人回复协调者消息」行为闭环无触发者 | spec/contract 冻结了 reply_to 的**消费面**（寻址/唤醒/渲染）与 CLI 的「不设入口」，但发送侧写面未落任何子卡的有界文件集 | spec 用户故事 6 ↔ S1/S5 文件集切分 | **B365 在途**（卡已立；不落 roadmap——有卡承载） |
| 2 | 控制台补员面缺位 | B358.7 review F1 指认 → B366 立卡修复 | 控制台发言 403（非成员）后无可行动自愈入口 | S6 有界文件集不含成员管理面；S4 显式不做项「不加 AddSessionMember 端点」 | S6↔S4（成员集权威在账本、控制台身份服务端注入） | **已闭合**（B366：POST /api/sessions/{id}/members + SessionChat 403 一键，七分支反例锁） |
| 3 | 会话 tab 面包屑显 'home' 不显标题（I-1） | B358.6 review（越界上报）→ B366 修复 | breadcrumbSegments 对 kind:'home' 硬编码单段，会话 tab 的 tail 被吞 | sessionBase 按 plan §2.4 落 kind:'home'，与 Breadcrumb 的 home 分支语义相撞 | shell Breadcrumb ↔ workbench sessionBase | **已闭合**（B366：home 分支放行 tail，`return [tail ?? base.label]`，既有无 tail 场不变） |
| 4 | 视图 diff 漏录 LedgerClient.ListAllCards | 本轮 recon（契约 diff 的 nodesModified 以完整节点替换，缺该方法字段） | 视图内 LedgerClient 接口比真实接口少一方法 | 契约轮补录时按「会话增量方法」口径遗漏既有方法（基线本就漏扫 ListAllCards，替换即放大） | contract ↔ recon（基线存量漏扫被 diff 放大） | **本轮已修**（16 方法全量核对后补齐） |
| 5 | 视图 diff 锚行漂移 10 处 | 本轮 recon「锚行号逐一实测」 | contract 冻结的 39 节点中 10 个行号与当前树差 3–10 行 | B358.1 拍板①短路扩集与补签轮 R3 词表扩行使后续符号行号后移；implement 无回写视图义务、validate 无盘→图方向判据 | contract（冻结时点）↔ implement（扩集） | **本轮已修**；根因是机制性的（工具查不出行号陈旧），流程测量数据记 §七 |
| 6 | 全量 `./...` 红窗外 4 处失败 | 本轮（各子卡 Task 0 均为包组口径，全量首跑） | client 守卫/agy perm/opencode/hostapi 红（明细见 §2.3） | 环境性：主仓磁盘残留 worktree 副本、macOS TMPDIR 超 socket 路径上限、真进程 1s 超时的并发翻抖 | 非接缝（机器卫生 + 测试环境健壮性） | 报告披露；worktree 卫生清理与 socket 路径兼容留协调者裁决，不属产品 roadmap 缺口 |

## 七、回旋镖对账（integrate 纪律第 5 件）

plan 预测 vs 实测的系统性差异（预测错误是校准数据，不对账才是过失）：

1. **红窗预测**：plan §0.4 红窗清单（agentd 10 + cmd 6）实测全部命中；B358.1 抓到 plan §0.4 **漏项 1 处**（cursorfile_test.go 2 支，自主消化按同款夹具迁移）。终态：红窗 16 支全部收口绿、禁触 3 支（golden/gate/permission hook）保持原红——与拍板红线逐支吻合。
2. **plan 字面变异的可执行率**：八卡共 15+ 发变异，其中 **7 发 plan 字面形式编译不过或不可观测**（B358.1 发①、B358.3 变异 A/B、B358.4 变异①、B358.5 等价重做、B358.2 Task 5 原变异为 no-op、B358.3 EqualFold 缺 --since），全部按 implement 纪律替换为可编译可观测的等价语义变异重做并红绿验证。校准结论：plan 阶段写变异字面代码的收益率低，plan 应写「变异意图+可观测判据」，实现留给执行者。
3. **plan 自家测试代码的内部矛盾**：4 处（B358.2 Task 5 数据顺序 vs 验收栏、B358.3 want 1 vs 夹具 3、B358.4 404 文案声称 vs 码链、B358.4 夹具旧口径）——均为「plan 引用自己写的代码时没跑一遍」族。
4. **变异红落点预言**：B358.2 三例「plan 预言红在 A 断言、实测红在其前更早的 B 断言」——同一缺陷更早咬合，变异全部有效；plan 对首触断言的预言能力弱，无实质风险。
5. **审查 findings 分布**（台账可查者）：B358.1 review 2 项移交测试（B358.4 Task 3 收口）；B358.6 review I-1 面包屑（越界上报→B366 修）+ 基线表 2 处记录不精确；B358.7 review F1 逃生门不可达（Important，措辞两处修正）+ F2 占位符恢复（并更正台账两处不实陈述）；B366 review F2 头注（本轮顺手修）+ F3 契约澄清（本轮回写）。review 抓真问题的密度集中在文档面与门面措辞，生产行为缺陷零逃逸到集成层——契约先行兑现的直接证据。
6. **耗时维度**：各台账未记耗时预测，无法对账（下轮 plan 模板可补「预计 task 数」一项做校准）。
7. **流程性发现**：Bash 沙盒静默丢弃 `perl -pi` 原地写（B358.3 首例识别，变异假绿险情一起）——八卡全面改用 Edit/Write + grep 确认，本轮全程遵守。

---

## 八、交棒（集成完成 ≠ 完成）

- **下一步：acceptance**。真机清单 **8 条**待执行（breakdown §5 全量：R1 通道推醒 / 真协调者回合拉起 / 换绑真机链 / PG 真库 DDL+LISTEN / 成员状态诚实性 / 旧房间归档读数 326 房间 / 控制台走查 W1–W6 / 未读游标并发）。真机 #1、#2 同时覆盖 B358.3 台账的真机申报两项。
- 本轮全部核算均为机内读数；上述 8 条在 ledger 登记为「未执行」，孤儿已标记。
- 并入主线（merge/absorb/归档）留协调者裁决。吸收时注意：视图 diff 含 nodesDeleted（KindAllowed + 10 web 节点），absorb 会真实删除基线节点——预期行为。

## 九、本轮受控文件集

- `codegraph/diffs/cards-B358-charter.json`（视图 diff 补齐）
- `docs/superpowers/specs/b358-contract.md`（仅 §9 销区 + §2.2 一行）
- `internal/agentd/sessionsapi.go`（仅头注一行）
- 本报告 + 台账（`docs/superpowers/ledgers/2026-09-12-b358-integrate-ledger.md`）

越界零：target.json / baseline.json / best.json / 业务代码 / 基线脏文件（Composer.test.tsx、.commandcode/、b353-probe、design-qa*、vitest 缓存）均未触碰、不卷入提交。
