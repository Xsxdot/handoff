# B358 integrate 台账（2026-09-12）

执行者：charter:integrate 节点。依据：charter:integrate + charter:recon 纪律块 + 协调者任务清单（七件）。分支 `cards/B233.1-charter-7` @ `00fef135`（implement(B366)）。

## 一、执行流水与关键读数

### recon 图对账（第 1 件，最大活）

- 改动面来源：八张子卡台账符号清单（B358.1 §四第 4 条、B358.2 §九、B358.3 §七、B358.4 图符号清单、B358.5 §四.2、B358.6 图符号清单、B358.7 文档无符号、B366 符号清单）+ breakdown §6。
- 基线图现状核查：`m_proto_RoomMessage`/`n_collab_Service_Send`/`n_collab_room_VerifyWriter`/`n_collab_room_KindAllowed`/`n_collab_room_Resolve`/`m_collab_room_Room`/`n_agentd_automationWakeEvent`/`n_agentd_Server_consumeAutomationEventsOnce`/`n_agentd_Server_registerLedgerRoutes` 在基线；八张卡的全部新增生产符号、web 新组件族、cmd/HTTP 新入口不在基线。
- **先例核对**：cards-B233.5-charter.json 有 web 节点修改先例（m_web_api_types_Task）→ 本轮 web 符号入图有据；cmd 命令 = entry 节点（c_cli，97 支）+ `xxxCmd.RunE` 函数节点 → session 命令族照办；HTTP 入口 file 指注册面（先例 e_http_get_api_rooms → internal/agentd/ledgerapi.go）→ 会话七端点照办。
- 补齐明细（+86 节点 / +12 修改 / 11 删除 / 10 处锚行修正 / 1 containersAdded）：见集成报告 §1.1。
- 过程缺陷两起当场修：①k_web_api_rooms 容器基线缺失 → validate 7 归因 → containersAdded 补齐（兄弟容器 k_web_api_rooms_model 同 domain d_web 先例）；②契约 diff 的 m_collab_client_LedgerClient 漏 ListAllCards → 16 方法全量核对补齐。
- 自检：`codegraph validate --view cards-B358-charter` 本视图 **0 归因**（总 issues 134→132，只减不增）；`codegraph check --view cards-B358-charter` fails 恰 6 条与基线逐行一致（只多不少）；sym 抽查 6 发全命中（Go/web/entry/proto 四类各抽）。
- **棘轮判定：无提请**——check 数字与基线全同（11/17/49/3/23），无接缝直调下降；target.json 零改动。
- 边未回灌的记账：cmd→collab.cursor（d_cli→d_collab 预算 0）、sessionsapi→Facade（d_gateway→d_ledger 澄清明文）——补真边会新增 check fail，归 absorb/finish 与预算裁决同批，recon 不擅动。

### 全量核算（第 2 件）

```
$ go build ./...                          → 退出 0
$ go test ./... -count=1                  → 53 包 ok；6 包红（归因见下）
$ cd web && npx vitest run                → 128 files / 1358 tests 全绿
$ cd web && npm run typecheck             → tsc -b 退出 0
$ go test ./internal/agentd -count=1      → --- FAIL 恰 1 支：TestLegacyNodeEventSequenceUnchanged（B362 golden，禁触原红）
$ go test ./cmd -count=1                  → --- FAIL 恰 2 支：TestRepoContractGate、TestServePermissionHookDenyWithReasonAndStep0
$ go test ./cmd -run TestRepoContractGate -count=1 -v → 违规恰 6 条，与 B358.4/B358.5/B366 台账基线逐字一致
```

红窗外归因（均非接缝缺陷，实证过程）：
- `internal/client` TestProductionHTTPClientCallersAreGatewayOnly：主仓磁盘残留 `.claude/worktrees/*`、`.worktrees/*` 被全仓守卫扫入（守卫只跳 .git/vendor/node_modules/web）。实证：`git worktree add /tmp/b358-prebase 7c8bc65f` 后该测试 **ok**；守卫测试代码 7c8bc65f..HEAD 零改动。worktree 用后即删。
- `internal/executor/agy` 4 支：socket 路径 116 字节 > 107 上限，预基线 worktree **同红**（环境性存量）。
- `internal/executor/opencode` 1 支、`internal/hostapi` 2 支：单包复跑**全绿**（全量并发下的时序翻抖）。
- 判定：红窗恰等、无多无少、无漂移——**不熔断**。

### 契约回写（第 3 件）

- `b358-contract.md` §9：标题下标注「已由 plan〈b358.1/2/3 实现计划〉吸收（2026-09-12）——本区销账」，原七条落点撤除留指针。Edit 工具落地。
- `b358-contract.md` §2.2：末尾增「**B366 review F3 澄清**：gateway 对 §3.5 无 Service 包装的冻结 LedgerClient 面（如 AddSessionMember）允许直调账本薄门面 Facade 并引用其哨兵——d_gateway→d_ledger 边 entry 明文形状，不构成越层。」（协调者给定文本逐字）。其余内容零改动。

### 顺手修（第 4 件）

- `internal/agentd/sessionsapi.go:7`：「s.ledger 既有直调面触达」→「s.autoLedger 既有薄门面触达」（B366 F2）。grep 确认新文案 1 命中/旧文案 0 命中；gofmt -l 空；`go build ./internal/agentd` 过。

### 生产闭环对账（第 5 件）

- breakdown §4 十八行逐行三态判定，17 行闭环成立、1 行缺口（#6 reply_to 生产触发者缺位，B365 在途）——逐行证据表见集成报告 §五。
- 本轮新增实证：`grep -rn "ReplyTo" internal/ cmd/ --include="*.go" | grep -v _test` 确认生产零写者（仅 proto 定义与消费方）；TS 发送面 `sendRoomMessage` 无 reply_to 来源；S6 三元素（session-unread 徽章 / 空座·还没配人 / totalUnread）grep 实测在产。

### 接缝缺陷记账 + 回旋镖（第 6 件）

- 缺陷六起逐个记录（现象/根因/接缝归属/去向）：reply_to 缺口（→B365）、补员面（→B366 已闭合）、面包屑 I-1（→B366 已闭合）、视图 diff 漏 ListAllCards（本轮修）、锚行漂移 10 处（本轮修）、全量环境红 4 处（披露留协调者）。
- 回旋镖七条：plan 变异字面可执行率低（7/15+ 需等价重做）、plan 自家代码内部矛盾 4 处、变异红落点预言弱（3 例更早断言咬合）、红窗清单漏项 1、审查 findings 集中在文档/措辞面而行为缺陷零逃逸、耗时维度无数据（模板改进建议）、Bash 沙盒丢 perl -pi 的流程性发现。明细见集成报告 §七。

## 二、有界文件集核对

受控五类全部落地，越界零：

```
M  codegraph/diffs/cards-B358-charter.json          （recon 补齐）
M  docs/superpowers/specs/b358-contract.md          （仅 §9 + §2.2 两处）
M  internal/agentd/sessionsapi.go                   （仅头注一行）
?? docs/superpowers/reviews/2026-09-12-b358-integrate-report.md   （本报告）
?? docs/superpowers/ledgers/2026-09-12-b358-integrate-ledger.md   （本台账）
```

基线脏文件未卷入：`node_modules/.vite/vitest/*results.json`、`web/src/app/task/Composer.test.tsx`、`.commandcode/`、`docs/superpowers/plans/b353-probe-follow-harness.md`、`web/design-qa*`。target.json / baseline.json / best.json / 业务代码零触碰。

## 三、交棒声明（孤儿标记）

- **下一步：acceptance**。真机清单 **8 条**（breakdown §5）**全部未执行**，登记如下防孤儿：
  1. R1 通道真机推醒（真实主 agent 外部会话被 @ 后经 session wait 醒来）——未执行
  2. 真协调者回合被 @卡号 拉起 + 载荷最小化现场核对——未执行
  3. 换绑真机链（B307 rebind 后旧席位拒发不唤醒）——未执行
  4. PG 真库：sessions/session_cards DDL 幂等迁移 + 无卡事件不进 Follow 多路 wait——未执行
  5. 成员状态生产诚实性（真账本上全 last_active/empty，无「在线」）——未执行
  6. 旧房间归档真机读数（326 旧卡房间拒发、读史可对质、pointer 续落）——未执行
  7. 控制台走查 W1–W6（对照 sessions.html）——未执行
  8. 未读游标并发（CLI 与 gateway 双进程 room-cursors.json tmp+rename）——未执行
- 并入主线、absorb（含视图 nodesDeleted 的真实删除）、归档：留协调者裁决。

## 四、单提交

```
$ git add codegraph/diffs/cards-B358-charter.json \
    docs/superpowers/specs/b358-contract.md \
    internal/agentd/sessionsapi.go \
    docs/superpowers/reviews/2026-09-12-b358-integrate-report.md \
    docs/superpowers/ledgers/2026-09-12-b358-integrate-ledger.md
$ git commit -m "integrate(B358): 七子卡+B366 集成——图对账/全量核算/契约回写/闭环对账"
```

最终 hash 以交付报文为准（台账不 chase hash；收口判据 = 受控五文件入库、工作树余量仅基线既有脏文件）。
