# B374 breakdown 台账（房间列表强制分页 + attach 刷新限域 + 日志三件套）

节点：breakdown（上游 contract `docs/superpowers/specs/b374-contract.md` 重冻稿 @ `32756584`，头部「上游状态：已批准」「冻结状态：…同批冻结」）。
卡：B374　有效基线：未设置（工作分支 `cards/B374-charter-5`，merge-base origin/main `41a284746501c90aa31ee72e7389851f8c2acb62`；不假定 main）。
角色：handoff 派发形态——executor 出稿、本地协调者拍板；本稿岔口一律「待拍板」，不自行选定。
一行一条历史读数；命令与原始输出照抄，不做二次加工。

## 0. 落点与角色

1. 2026-09-15：本节点法定产出 `docs/superpowers/specs/b374-breakdown.md`（不带日期前缀）；台账本文件。不写实现代码、不建卡、不派发、不调 handoff CLI、不起新 executor。

## 1. 开工状态位与工作树

2. 2026-09-15：`git log --oneline -1` → `32756584 contract(B374): 重冻——错误映射/游标校验/图视图勘误 + 落地顺序拍板`；`git branch --show-current` → `cards/B374-charter-5`；`git status --porcelain` 无输出。
3. 2026-09-15：`git merge-base HEAD origin/main` → `41a284746501c90aa31ee72e7389851f8c2acb62`。
4. 2026-09-15：`sed -n '3p' docs/specs/2026-09-15-b374-rooms-pagination.md` → `状态：已批准（2026-09-15，用户审批） / 级别：L3轻 / 日期：2026-09-15`。spec 状态位核对通过。
5. 2026-09-15：`sed -n '3,8p' docs/superpowers/specs/b374-contract.md` → 头部含 `**上游状态：已批准**`、`**冻结状态：本提交随 codegraph/diffs/cards-B374-charter.json 与 Ticket 0 骨架同批冻结；codegraph/target.json 本轮无口径增量（见 §4）**`、`**本轮性质：contract 重冻（用户拍板 A）**`。契约冻结位核对通过。头部 `**有效基线：未设置**（工作分支 cards/B374-charter-3…）` 的分支字样与当前分支 `cards/B374-charter-5` 不一致（同一冻结提交 `32756584` 在两个分支上都可达），属分支标签漂移，非冻结正文问题——见产出物 §2.1。

## 2. 图事实查证（best.json / 分支视图）

6. 2026-09-15：`python3` 读 `codegraph/best.json` 顶层领域（`parent` 缺省）与类型：

```
d_orchestration  logic      任务编排
d_gateway        boundary   控制门面
d_workspace      boundary   项目与工作区
d_execution      boundary   任务执行
d_sessions       boundary   终端会话
d_transport      boundary   跨机连接
d_protocol       logic      协议契约
d_ledger         logic      卡片账本
d_collab         logic      协作房间
d_cli            logic      协调者命令面
d_web            logic      Web 控制台
d_policy         logic      运行策略与配置
d_maintenance    boundary   安装与换版
d_scheduling     logic      编制调度
d_keystone       logic      协调者 Keystone
```

7. 2026-09-15：`python3` 读 `codegraph/best.json` 的 `containers`（域归属）：

```
k_agentd_Server   -> d_gateway
k_agentd_fn       -> d_orchestration
k_agentd_model    -> d_orchestration
k_logx_fn         -> d_policy
k_logx_model      -> d_policy
k_logx_multiHandler -> d_policy
k_collab_Service  -> d_collab
k_collab_fn       -> d_collab
k_collab_model    -> d_collab
k_proto_model     -> d_protocol
k_web_api_rooms   -> d_web_contract
k_agentd_Mirror   -> d_workspace
```

`d_runtime_config` present: False；`d_sessions` present: True。→ 勘误落实：logx 归 `d_policy`，协作房间归 `d_collab`，`d_runtime_config` 不存在。

8. 2026-09-15：`codegraph resolve --repo . --view cards-B374-charter --doc <探针>`（20 个 `file#Symbol` 锚）退出码 0，逐条 anchor：`Server.handleRoomsList=ok`、`Server.enrichRoomAttachments=moved`、`Server.startRoomAttachRefresh=moved`、`parseRoomsListParams=ok`、`roomsListErrorStatus=ok`、`Server.withRooms=moved`、`Server.Handler=moved`、`Service.ListRoomsPage=ok`、`trimRoomPage=ok`、`encodeRoomCursor=ok`、`decodeRoomCursor=ok`、`Service.listRooms=ok`、`Service.ListRoomsForMember=moved`、`logx.Setup=ok`、`parseLevel=ok`、`multiHandler.Handle=ok`、`fetchRooms=moved`、`RoomPanel=ok`、`RoomsPage=ok`、`RoomSummary=moved`、`Mirror.discoverOnce=ok`。**无视图时不识别**：同一探针 `codegraph resolve --repo . --doc <探针>` 退出码 1（view 必须显式给）。
9. 2026-09-15：`codegraph sym --repo . n_logx_Setup` → `"domain": "d_runtime_config"`、`"container": "k_logx_fn"`（baseline 残留域 id，与 best.json 的 `k_logx_fn→d_policy` 不一致；以 best.json 为准，不改图）。`--view cards-B374-charter` 同输出。
10. 2026-09-15：`codegraph validate --repo . --view cards-B374-charter` → `"issues": null`、`"edgeIssues": null`；`codegraph check --repo . --view cards-B374-charter` → `"fails": []`；`go test ./cmd/ -run '^TestRepoContractGate$' -count=1` → `ok github.com/Xsxdot/handoff/cmd 0.043s`。
11. 2026-09-15：`codegraph resolve --repo . --doc docs/superpowers/specs/b374-contract.md` 退出码 0，anchor 计数 `{moved: 11, ok: 9}`，无坏锚。

## 3. 现状代码事实（用于子卡入口指针与缺陷族）

12. 2026-09-15：`internal/agentd/roomsapi.go` 现状：`parseRoomsListParams(_ *http.Request)`（:79）恒返回 `{Limit: roomsListDefaultLimit}`；`handleRoomsList`（:93）经 `parseRoomsListParams` → `ListRoomsPage` → `enrichRoomAttachments` → `writeJSON`；`roomsListErrorStatus`（:123）`init` 起真实映射；`enrichRoomAttachments`（:151）读**全量** `AllTaskLinks()` 并 `startRoomAttachRefresh(links)`（:217）；`startRoomAttachRefresh`（:223）对全量远端挂账 fan-out（workers=16）。
13. 2026-09-15：`internal/agentd/roomsapi.go` 的 `s.log.Info` 共 8 处：`:114`（列表响应成功）、`:205`/`:211`（逐房间 attach 投影）、`:218`（attach 投影完成，带 `links` 数）、`:278`（逐挂账后台刷新成功）、`:283`（后台刷新完成）、`:411`（房间消息已发送）、`:507`（收件箱已聚合）。
14. 2026-09-15：`internal/collab/service.go` 现状：`ListRoomsForMember`（:316）全量；`ListRoomsPage`（:333）直通镜像；`trimRoomPage`（:397）直通镜像，注释写「(LastActivity,roomID) 为兜底比较」——**该注释与 P6/契约 §3.2 规则 3 冲突，是待修的代码注释缺陷**。
15. 2026-09-15：`internal/logx/logx.go` 现状：`parseLevel`（:46）默认分支 `return slog.LevelInfo`；`Setup`（:31）`:33` 无条件挂 stderr TextHandler、`:36` 带 logPath 时追加文件 JSONHandler；包注释 `:8` 写「不管理日志轮转」。无任何 rotate/maxSize/maxBackups 符号（`grep -rn "rotate\|Rotation\|maxSize\|maxBackups" internal/logx/` 仅命中注释 :8）。
16. 2026-09-15：`internal/service/launchd.go:118-119` 把 `StandardOutPath` 与 `StandardErrorPath` 同值写成 `spec.LogPath`；`internal/service/systemd.go`/`windows.go` 不重定向 stdout/stderr。契约 P2 维持不改 manager。
17. 2026-09-15：`web/src/api/rooms.ts:85` `fetchRooms = (project = ''): Promise<RoomSummary[]>` 解包 `rooms` 数组；唯一生产调用方 `web/src/app/rooms/RoomPanel.tsx:174` `const result = await fetchRooms()`，`:355` `visible.map` 全量渲染。`web/src/app/rooms/RoomPanel.test.tsx`、`web/src/app/shell/Shell.test.tsx`、`web/src/app/rooms/pollInterval.test.tsx`、`web/src/api/rooms.fetch.test.ts` 均 mock/断言现行签名与端点。

## 4. 承接欠账的现状查证（欠账 8 / 9）

18. 2026-09-15：欠账 8（镜像发现超时定性）代码事实：`internal/agentd/mirror.go:47-48` `mirrorDiscoverBudget = 3 * time.Second`；`:186` `context.WithTimeout(ctx, mirrorDiscoverBudget)` 对**全部 target** 共享一个 3s 预算；`:214` 单台失败只 `m.log.Warn("镜像发现失败", ...)` 并计入 `unreachable`。`grep -rn "context deadline exceeded" internal/ cmd/` 无硬编码命中——该串是 Go 标准库超时错误的运行时文本，非代码常量。**「`linux-01 context deadline exceeded` 是刷新风暴次生还是独立故障」属行为事实，机内不能定性**（需真机/长跑），见产出物 §6 真机清单。
19. 2026-09-15：欠账 9（b358 §4.4 文档修订）可达性查证：`git cat-file -e HEAD:docs/superpowers/specs/b358-contract.md` → 不存在；`origin/main`、merge-base `41a28474` 亦不存在。b358 系列文档只在 `5318092f`（B358/B361 沿革）等分支可达。且在该分支 `git show 5318092f:docs/superpowers/specs/b358-contract.md` 的 `§4.4 房间形态与只读`（条目 32–35）**不含「列表全量」字面**；`b358.md` 的 `§4.4` 是「详情页投影」。全仓 grep「列表全量」只在：`docs/superpowers/specs/b156.2-breakdown.md:39`、`docs/superpowers/plans/b156.2.8-plan.md:1449`。→ 欠账 9 的目标文档/章节在**本工作树不可达且引用有歧义**，作为待拍板岔口 P-1 交协调者。
20. 2026-09-15：`grep -rn "ListSessions\|IsSessionRoom\|sessionTimeline" internal/collab/*.go` 无命中；`ls internal/collab/*.go` 只有 `service.go` + 测试文件——b358 会话模型（`session:<n>`）**未合入本分支**，进一步佐证欠账 9 的修订对象不在本工作树。

## 5. 会打红的既有耦合（实现前必须消化，机内已核）

21. 2026-09-15：`grep -nE '"/api/rooms"|"/api/rooms\?project' internal/agentd/roomsapi_test.go` 共 **14 处**列表请求，全部无 `limit`/`cursor`：行 87、123、168、185、211、254、309、325、375、396、411、457、623、859。其中 623（`TestRoomsEndpoints503WithoutLedger`）走 `withRooms` 未装配 503 分支（`internal/agentd/server.go:2476`），**其余 13 处**在 F9 legacy 426 落地后将命中 426。这 13 处是「实现 T1 时必须同步改测试请求形态」的硬耦合。
22. 2026-09-15：变异实测（F19 默认级别改 warn 的既有测试影响）。把 `internal/logx/logx.go` 的 `return slog.LevelInfo` 临时改为 `return slog.LevelWarn`，`go test ./internal/logx/ -count=1 -v` 原始输出：

```
=== RUN   TestSetupWritesJSONToFile
    logx_test.go:24: 文件日志缺少消息: 
--- FAIL: TestSetupWritesJSONToFile (0.00s)
=== RUN   TestSetupLevelFilter
--- PASS: TestSetupLevelFilter (0.00s)
=== RUN   TestSetupEmptyLogPath
--- PASS: TestSetupEmptyLogPath (0.00s)
FAIL
FAIL	github.com/Xsxdot/handoff/internal/logx	0.001s
FAIL
```

恢复后 `git status --porcelain internal/logx/logx.go` 无输出。→ F19 落地必打红 `TestSetupWritesJSONToFile`（它以 Info 写文件并断言落盘），处置为待拍板 P-5。
23. 2026-09-15：变异实测（F20 单写）。临时改为「带 logPath 时不挂 stderr TextHandler」，`go test ./internal/logx/ -count=1` → `ok github.com/Xsxdot/handoff/internal/logx 0.003s`——**现有测试对 F20 无牙**（去掉任一路都绿）。恢复后工作树干净。→ F20 必须新写一支能变红的单写断言（产出物 T4 验收）。
24. 2026-09-15：`ls web/node_modules` → `No such file or directory`；`cd web && npm test` 原始输出 `sh: 1: vitest: not found`。web 侧 vitest/tsc **本轮不可执行**（未验证，不写结论），归真机清单。

## 6. 机内可闭环验证（本节点亲跑，作为交稿依据）

25. 2026-09-15：`go build ./...` → `GO_BUILD_EXIT=0`。
26. 2026-09-15：`go test ./internal/collab/ ./internal/proto/ -count=1` → `ok github.com/Xsxdot/handoff/internal/collab 4.058s`、`ok github.com/Xsxdot/handoff/internal/proto 0.004s`。
27. 2026-09-15：`go test ./internal/agentd/ -run 'TestRooms|TestListRooms' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 6.593s`。
28. 2026-09-15：`go test ./internal/logx/ -count=1` → `ok github.com/Xsxdot/handoff/internal/logx 0.002s`（变异前基线）。

## 7. 未验证，需真机（机内造不出的行为事实）

29. 2026-09-15：已在产出物 §6 汇总 8 条；本轮未亲跑的判据（实现级命令、web vitest/tsc、366 房间真机、attach RPC 计数、launchd 单写与轮转实况、旧桌面端 426 文案、镜像发现超时定性、b358 文档可达性）均标「未验证，需真机」，不写成结论。

## 8. 出稿自检与回写

30. 2026-09-15：`codegraph resolve --repo . --view cards-B374-charter --doc docs/superpowers/specs/b374-breakdown.md` 退出码 0，9 锚无坏；首跑曾报 `server.go#Server.withRooms` → `file_missing`（相对路径锚），修为 `internal/agentd/server.go#Server.withRooms` 后复跑 `ok`。
31. 2026-09-15：`codegraph resolve --repo . --doc docs/superpowers/specs/b374-contract.md` 退出码 0，20 锚 `{ok:9, moved:11}` 无坏（回写 §9 后复跑，新增 4 条边界澄清未引入坏锚）。
32. 2026-09-15：`gofmt -l internal/agentd/roomsapi.go internal/collab/service.go internal/logx/logx.go internal/proto/rooms.go` 无输出；`go build ./...` → `BUILD=0`；`go test ./cmd/ -run '^TestRepoContractGate$' -count=1` → `ok ... 0.041s`；图 `issues=null` `edgeIssues=null` `fails=[]`。
33. 2026-09-15：边界澄清 C-1..C-4 回写 `docs/superpowers/specs/b374-contract.md §9`（不退回 contract，不改冻结正文）。

## 9. 提交（历史读数）

34. 2026-09-15：本节点提交命令与原始输出见下条；提交后 amend 一次收进同批（含本条）。收口判据是工作树干净，不 chase hash。
