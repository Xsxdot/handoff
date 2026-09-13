# B366 implement 台账（2026-09-12，恢复执行）

前一次执行因平台故障死在基线阶段、零产出；本台账从头记。分支 cards/B233.1-charter-7 @ afb74ccd。

## 事实流水

- L1 读定 spec `docs/superpowers/specs/b366.md`（L1 spec 即 plan）+ charter:implement 纪律块。
- L2 关键探路：`collab.Service` 无 `AddSessionMember` 方法（`internal/collab/service.go`/`sessions.go` 全量 grep 证实）；任务描述所写 `validSessionMemberIdentity` 不存在，既有校验函数为 `sessionsapi.go#validSessionOwner`（user:/agent: 前缀口径），沿用之。三层冻结面：Store `internal/ledger/sessions.go:236`、Facade `internal/ledger/api/api.go:141`（`translateNotFound` → `client.ErrNotFound`）、接口 `internal/collab/client/client.go:60`。
- L3 gateway 调用面裁定：`Server.autoLedger *ledgerapi.Facade`（server.go:178，与 `collab.New(facade)` 同一实例，server.go:2669-2691）＝ spec「Service/LedgerClient.AddSessionMember」的 LedgerClient 实现；有界文件集不含 collab 包，故不加 Service 转发方法，handler 直调 facade（sessionsapi.go 头注「账本能力经既有直调面触达」同款）。
- L4 身份语义裁定（任务 vs spec 合读）：body `{identity}` 可空＝以调用者身份加入（服务端注入 actor，前端不自报）；body 塞非空 identity 时仍按 `validSessionOwner` 口径校验（违者 400＝「拒绝」），但成员身份恒取服务端注入 actor（＝「忽略」）——依据 roomsapi.go:8 明文「actor/成员标识服务端注入，不经请求体」+ 任务「测试钉死：请求体塞 identity 被忽略/拒绝的反例」；多人管理式加员 OOS（spec §2.4）。

## 收尾全量证据（改动全部落地后复跑）

- `go build ./...` → 通过。
- `go test ./internal/agentd -count=1` → FAIL 恰 1 支 `TestLegacyNodeEventSequenceUnchanged`（B362 golden，红窗未增减）。
- `go test ./cmd/... -count=1` → FAIL 恰 2 支 `TestRepoContractGate` + `TestServePermissionHookDenyWithReasonAndStep0`（未增减）。
- `TestRepoContractGate -v` 违规清单与基线 6 条逐字一致（diff 比对 GATE_LIST_IDENTICAL）——闸读仓内基线图，本卡零 codegraph 改动。
- `go test ./internal/collab/... ./internal/ledger/... ./internal/proto/...` → 5 包全 ok（收口前核对，改动未触这些包）。
- `go test ./internal/agentd -run 'TestSession' -count=1` → ok（spec 验收行；含新支 TestSessionMemberAddEndpoint 与既有 6 支会话端点测试）。
- web `npx vitest run` 全量 → 128 文件 1358 tests 全绿（基线 1353 + 新增 5 恰合：rooms.fetch +2、SessionChat +2、Breadcrumb +1）。
- web `npm run typecheck`（tsc -b）→ 0 错。
- gofmt：触及的三个 Go 文件格式零差（wakeconsumer.go / wakeconsumer_b358_test.go 的既存不齐为基线遗留、禁触未动）。

## 符号清单（本卡新增/修改符号）

- Go 新增：`agentd.handleSessionMemberAdd`（sessionsapi.go，POST /api/sessions/{id}/members）；`agentd.sessionErr` 扩展 `client.ErrNotFound`→404 一案（既有六端点无该哨兵路径，行为不变）。
- Go 注册：`registerLedgerRoutes` 增一行（ledgerapi.go）。
- Go 测试：`TestSessionMemberAddEndpoint`（sessionsapi_test.go，七分支）。
- TS 新增：`addSessionMember`（rooms.ts）；SessionChat 组件 `sendErrorStatus`/`joining` 状态与 `joinSelf` 处理器 + 403 一键按钮（SessionChat.tsx）；`breadcrumbSegments` home 分支放行 tail（Breadcrumb.tsx）。
- TS 测试：rooms.fetch.test.ts `addSessionMember (B366)` 两支；SessionChat.test.tsx 两支；Breadcrumb.test.tsx home 支按 spec 翻转 + 会话 tab 支。

## 偏差清单

1. 任务文所写「沿用既有 `validSessionMemberIdentity` 口径」：该符号不存在，实际沿用 `validSessionOwner`（sessionsapi.go，同口径 user:/agent: 统一记法）——记为任务文的符号名笔误，非行为偏差。
2. gateway 调用面：`collab.Service` 无 `AddSessionMember` 转发方法且 collab 包在本卡有界文件集外，故 handler 直调 `Server.autoLedger`（= SetupAutomation 装配的 ledger/api.Facade，与 s.rooms 同一实例，即 spec「LedgerClient.AddSessionMember」的实现面）；未给 Service 加转发方法（那将是契约增补，越界）。
3. 身份语义合读裁定：body `{identity}` 可空＝以服务端注入 actor 自加入；塞非空 identity 仍按统一记法校验（违者 400＝「拒绝」），成员恒取服务端注入值（＝「忽略」）——依据 roomsapi.go 门禁纪律「成员标识服务端注入，不经请求体」+ 任务「被忽略/拒绝的反例」双动词；spec 六分支（含前缀校验 400、actor 注入反例）全覆盖。管理式替人加员 OOS（spec §2.4）。
4. SessionDetail 头部未加按钮：任务「与/或」取 SessionChat 一处（403 文案唯一现场）；SessionDetail 无「自己是否成员」投影，常驻按钮会失真。后续如需详情页入口，另立小卡。
5. 无其余偏差；禁触面（wakeconsumer*/cmd/**、internal 其他面、无关脏文件）零触碰。

## 命令与原始输出（历史读数）

- 基线 ①`go build ./...` → 通过（exit 0）。
- 基线 ②`go test ./internal/agentd` → FAIL 恰 1 支 `TestLegacyNodeEventSequenceUnchanged`（scheddispatch_test.go:493；经 2026-09-12-b358.5-plan-ledger.md L39 证实即 B362 golden，禁触）——失败集合与红线吻合。
- 基线 ③`go test ./cmd/...` → FAIL 恰 2 支 `TestRepoContractGate` + `TestServePermissionHookDenyWithReasonAndStep0`——与红线吻合。
- 基线 ④`TestRepoContractGate -v` 违规清单（6 条，收口比对基准）：[dead-interface] OrchestrationClient；[over-budget] d_cli->d_workspace 11>9；d_gateway->d_workspace 17>1；d_orchestration->d_workspace 49>19；d_workspace->d_orchestration 3>2；d_workspace->d_protocol 23>3。闸读仓内基线图（codegraph.LoadGraph），不重扫源码，本卡不碰 codegraph/。
- 探路核实：Store.AddSessionMember 不落事件（幂等零事件副作用，spec 语义）且 identity=="" 才报错；Facade 层 translateNotFound 把账本 ErrNotFound 翻成 client.ErrNotFound；sessionErr 现只认 collab.ErrNoRoom→404、ledger.ErrBadState→409。

- 红 ⑤`sessionsapi_test.go` 落 `TestSessionMemberAddEndpoint`（七分支：403 前态→空体自加入→幂等→发言链通→塞 identity/actor 被忽略→前缀 400→不存在 404 含 id→已归档 409 含「已归档」），`go test ./internal/agentd -run TestSessionMemberAddEndpoint -count=1` → FAIL：`sessionsapi_test.go:410: 空体自加入应 200: 404 404 page not found`——路由未接红 404，红因=功能缺失非 typo。

- 绿 ⑥`sessionsapi.go`：`sessionErr` 映射表补 `client.ErrNotFound`→404（facade 直调面 translateNotFound 出站哨兵，collab.ErrNoRoom 本就是它的入站翻译，同族；既有六端点无该哨兵路径，行为不变）+ `handleSessionMemberAdd`（body 可空/空 identity＝以 roomUserActor 自加入；塞非空 identity 仍按 validSessionOwner 校验违者 400，但成员恒服务端注入＝塞值被忽略；包回会话 id 走 sessionErr）+ 头注七端点；`ledgerapi.go` 注册行 `POST /api/sessions/{id}/members`（withRooms 守卫）。
- 绿 ⑦`go test ./internal/agentd -run TestSessionMemberAddEndpoint -count=1` → PASS。
- 变异自验（先 grep 断言命中唯一：`!validSessionOwner(identity)` 1 处；`actor := s.roomUserActor(r)` 5 处故用注释行锚定）：M1 守卫置 false → 编译过、④ 前缀 400 支红；M2a `actor := req.Identity` → 编译过、① 自加入支红（500 空身份）；M2b 精修（空体回落服务端、塞值被采纳）→ 编译过、③ 反例支红（line 448「body 塞 identity 被忽略」，user:mallory 入列）。三发均还原，还原后 `-run 'TestSession'` 全绿（ok）。
- 红窗核对 ⑧`go test ./internal/agentd -count=1` 全量 → FAIL 恰 1 支 `TestLegacyNodeEventSequenceUnchanged`（B362 golden，未增减）；`go test ./internal/collab/... ./internal/ledger/... ./internal/proto/... -count=1` → 5 包全 ok。

- 红 ⑨web 侧测试先落三处：`rooms.fetch.test.ts` 增 `addSessionMember (B366)` 两支（空体 {} identity 不出键=前端不自报身份；显式 identity 出键=端点形状镜像）；`SessionChat.test.tsx` 增两支（B366 链：403→一键→清横幅→再发送成功；一键只在 403 出现的 500 判别支）；`Breadcrumb.test.tsx` 既有「home tail 不参与」断言按 spec 翻转（该断言锁的正是 bug）+ 增会话 tab 支。`vitest run` 三文件 → 6 failed | 20 passed：`addSessionMember is not a function`（编译红，新缝首红）、join 按钮找不到（断言红）、`expected ['home'] to equal ['bash']` / `['会话 · 架构物理化']`（断言红）。500 判别支在批量跑中红是前支中断污染，单跑即绿，实现后批量自消。
- 绿 ⑩`rooms.ts` 增 `addSessionMember(id, identity='')`（identity 缺省不出键）；`SessionChat.tsx` 增 `sendErrorStatus` 判 403 + `joinSelf`（空体加入、成功清横幅、403 才出按钮 data-testid=session-join-self）；`Breadcrumb.tsx` home 分支改 `return [tail ?? base.label]`（既有无 tail home 场显示 label='home' 不变）。三文件复跑 → 26 passed 全绿。web 侧变异义务由红阶段本身履行：红支全部打在改动前代码上（原 bug 即变异）。
- 落位说明：SessionDetail 头部不加按钮——任务「SessionChat 发送 403 与/或 SessionDetail 头部」取与的一半，403 可行动文案的唯一现场在 SessionChat footer；SessionDetail 不掌握「自己是否成员」的投影，常驻按钮会说话不算话。

- 收口：台账随产出物同批单提交（提交当时的 HEAD hash 不回写台账，收口判据=工作树内本卡有界文件全部入库且无未触及的越界改动）。
- 提交 ⑪`git add <有界文件集 9 文件 + 本台账> && git commit -m "implement(B366): 补员端点 + 控制台加入会话 + 面包屑修（恢复用户故事 1）"` → `[cards/B233.1-charter-7 34e4d74c] 10 files changed, 342 insertions(+), 17 deletions(-)`。本行是 amend 前的历史读数：随后按 implement 纪律把本条补进台账并 amend 一次，hash 34e4d74c 因 amend 失效（git 事实，非漏记）；未触及的既存脏文件（node_modules vitest 缓存、Composer.test.tsx、.commandcode/、b353-probe、design-qa*）不入栈。
