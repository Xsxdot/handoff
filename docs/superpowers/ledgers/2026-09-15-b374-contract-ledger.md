# B374 contract 台账（房间列表强制分页 + attach 刷新限域 + 日志三件套）

节点：contract（上游 spec `docs/specs/2026-09-15-b374-rooms-pagination.md`，头部状态行「已批准（2026-09-15，用户审批）」——开工核对通过，见条目 1）。
卡：B374　有效基线分支：未设置（分支基线 `cards/B374-charter` @ `41a28474`，spec 提交在 `origin/main` @ `85840cf2`，本卡暂未定合并目标，见条目 2）。
一行一条历史读数；命令与原始输出照抄，不做二次加工。

## 0. 落点裁决与边界

1. 2026-09-15：上游状态位核对。`git show origin/main:docs/specs/2026-09-15-b374-rooms-pagination.md` 第 3 行原文 `状态：已批准（2026-09-15，用户审批） / 级别：L3轻 / 日期：2026-09-15`。规范路径 `docs/specs/2026-09-15-b374-rooms-pagination.md` 在本分支不存在（分支基线 `41a28474` 早于 spec 提交），经协调者指路改读 `origin/main`。
2. 2026-09-15：本卡未设置合并目标（无有效基线分支）。冻结提交只落本分支 `cards/B374-charter`，不假定 main。
3. 2026-09-15：spec 定级 **L3 轻档**。定级两问——跨 `d_gateway`（handler+刷新）/`d_sessions`（collab 列表语义）/`d_web`（懒加载）三契约面；动跨进程 wire `GET /api/rooms`。**轻档无直通竖切**（纪律块「轻档与 L2 无此步骤」），运行时最薄路径由 plan 节点的「最薄路径」承接。
4. 2026-09-15：本节点只落契约增量文档、Ticket 0 可编译空壳骨架、target.json 契约面、本分支视图 diff 与台账；完整分页/限域/日志实现归 implement 轮。

## 1. 现状查证（命令与原始输出）

### 1.1 上游状态位与落点

5. 2026-09-15：`ls docs/specs/` → `ls: cannot access 'docs/specs/': No such file or directory`；`git show origin/main:docs/specs/2026-09-15-b374-rooms-pagination.md` 读到 spec 全文 86 行。
6. 2026-09-15：`git fetch origin main` 原始输出 `41a28474..85840cf2  main -> origin/main`；`git merge-base HEAD origin/main` → `41a284746501c90aa31ee72e7389851f8c2acb62`（= 当前 HEAD），`git merge-base --is-ancestor HEAD origin/main` → yes。本分支落后 origin/main 两个提交（B356 文档提交），与代码事实无关的差异只有 `docs/roadmap.md` +2 与 spec 新增。

### 1.2 接缝现状签名（spec 接缝清单逐条）

7. 2026-09-15：`internal/agentd/roomsapi.go:53` `func (s *Server) handleRoomsList(w http.ResponseWriter, r *http.Request)`；现体：`project := r.URL.Query().Get("project")` → `member := s.roomUserActor(r)` → `s.rooms.ListRoomsForMember(project, member)` → `s.enrichRoomAttachments(r.Context(), rooms)` → `writeJSON(w, 200, map[string]any{"rooms": rooms})`。**无 limit/cursor 解析**。
8. 2026-09-15：`internal/agentd/roomsapi.go:84` `func (s *Server) enrichRoomAttachments(_ context.Context, rooms []proto.RoomSummary)`；`:150` `s.startRoomAttachRefresh(links)`，参数是 `AllTaskLinks()` 的**全量** links（`:88`），不区分页。
9. 2026-09-15：`internal/agentd/roomsapi.go:156` `func (s *Server) startRoomAttachRefresh(links []ledger.TaskLink)`；`:157-163` 从 links 抽 `link.Target != ""` 的远端项，对**全量远端挂账** fan-out（`:188` workers=16）。
10. 2026-09-15：`internal/collab/service.go:303` `func (s *Service) ListRoomsForMember(project, member string) ([]proto.RoomSummary, error)`；`:307` `func (s *Service) listRooms(project, member string) ([]proto.RoomSummary, error)` 全量组装（`:308` `ListAllCards` + `:313` `ReadAllEvents` + 排序 `:413-416`），**无页裁剪**。
11. 2026-09-15：`internal/collab/service.go:368-401` 终态卡房间只标 `ReadOnly`（`:329` `ReadOnly: isTerm || c.Following != ""`）不剪枝；排序把终态沉底（`sunk`）。
12. 2026-09-15：`internal/logx/logx.go:31` `func Setup(component, logPath string) *slog.Logger`；`:33` `slog.NewTextHandler(os.Stderr,...)` + `:36` `slog.NewJSONHandler(f,...)` 双路；`:35` `os.OpenFile(logPath, O_CREATE|O_APPEND|O_WRONLY, 0o600)`。包注释 `:8` 明说「不管理日志轮转」。
13. 2026-09-15：`cmd/agentd.go:69` `logger := logx.Setup("agentd", filepath.Join(cfg.DataDir, "agentd.log"))`，`:71` `slog.SetDefault(logger)`。
14. 2026-09-15：`cmd/service.go:83` `LogPath: filepath.Join(cfg.DataDir, "agentd.log")`；`internal/service/launchd.go:118-119` 把 `StandardOutPath` 与 `StandardErrorPath` **都**写成同一个 `spec.LogPath`——这就是双写的 manager 侧来源（spec §问题陈述）。`internal/service/systemd.go:76-101` 的 unitBody **不写** StandardOutput/StandardError（systemd 默认进 journal）；`internal/service/windows.go` 的 taskXML 也**不重定向** stdout/stderr（只把 LogPath 用于报错提示 `:262/:280`）。
15. 2026-09-15：`web/src/api/rooms.ts:85` `fetchRooms = (project = ''): Promise<RoomSummary[]> => request<{rooms: RoomSummary[]}>(...)`，**无 limit/cursor**；`:88` `.then((response) => response.rooms ?? [])` 只解包数组。
16. 2026-09-15：`web/src/app/rooms/RoomPanel.tsx:174` `const result = await fetchRooms()`（`loadRooms`，`:171-181`），经 `usePoll(loadRooms, COLLAB_POLL_MS)`（`:204`）整表轮询；`:355` 渲染 `visible.map(...)`，无滚动续载、无游标状态。
17. 2026-09-15：CLI 侧 `cmd/room.go:59` `svc.ListRooms(roomListProject)`——CLI 走 `ListRooms`（无 member）全量打印，不经 HTTP；本卡 wire 分页**不改变** CLI 列表语义（spec Out of Scope 未列 CLI，但语义2「刷新域=本页返回集合」只约束 attach 刷新，CLI 无 attach 刷新面）。

### 1.3 依赖库/平台既成行为（契约承重项）

18. 2026-09-15：`go.mod` 钉 `github.com/Xsxdot/charter/graph v0.10.0`；`golang.org/x/tools v0.48.0` 在 `go list -m all` 中（扫描器 `scripts/codegraph-rescan` 是独立嵌套 module，本分支/主线均无该目录，见条目 27）。
19. 2026-09-15：`codegraph/check.go`（v0.10.0 源码）`Check` 只对**跨域** call 边执法：`from==to` 跳过（`:84-85`）；`new-direction` 只对无契约方向的跨域边报 fail（`:94-97`）；`entry` 按被调方容器 `Label` 匹配（`:103`）；`dead-contract` 认 `liveDirections`（call ∪ implements ∪ 组装点豁免，`:89/:120/:176`）；`over-budget` 是 `LegacyHits > LegacyBudget`（`:184-188`）。**同域新增边不触发任何 fail**。
20. 2026-09-15：`codegraph/sym` 现状锚（原始读数）：`n_collab_Service_ListRoomsForMember` `anchor=ok`、`domain=d_collab`、`file=internal/collab/service.go:303`；`n_agentd_Server_handleRoomsList` `anchor=ok`、`file=internal/agentd/roomsapi.go:53`；`n_agentd_Server_enrichRoomAttachments` `:84`；`n_agentd_Server_startRoomAttachRefresh` `:156`；`n_logx_Setup` `:31`。
21. 2026-09-15：`codegraph --repo . check` 现状原始读数：`fails 0`、`warns 145`；`legacyHits` 含 `d_collab->d_protocol 1`、`d_gateway->d_protocol 1`（后者即 `k_agentd_Server` 域 d_gateway 与 proto 之间那条）。
22. 2026-09-15：基线边事实（`python3` 读 `codegraph/baseline.json`）：`n_agentd_Server_handleRoomsList→n_collab_Service_ListRoomsForMember` 既有；`n_collab_Service_ListRoomsForMember→n_collab_Service_listRooms` 既有；`n_agentd_Server_enrichRoomAttachments` 出边只有 `lookupRoomAttach`/`cachedRoomAttach`/`startRoomAttachRefresh`（全同域 d_gateway）。**gateway→proto 的 call 边当前为 0 条**（`legacyHits d_gateway->d_protocol 1` 由其它节点承载，非 rooms 面）。
23. 2026-09-15：`web/package.json` 有 `"test":"vitest run"`、`"typecheck":"tsc -b"`、`"lint":"eslint ."`；但本工作树 `web/node_modules` 不存在（`ls web/node_modules` 失败），web 测试/类型检查本轮**不可执行**（未验证，不写结论）。根 `node_modules` 存在。

### 1.4 对侧常量查执法（谁发出、谁消费）

24. 2026-09-15：`proto.RoomSummary`/`RoomPreview` 字段——`internal/proto/rooms.go` 唯一定义处；消费方 `web/src/api/rooms.ts`（TS 镜像）+ `internal/agentd/roomsapi.go` + `cmd/room.go`。分页信封是**新增键**（`next_cursor`/`has_more`），不动既有 `rooms` 数组形状 → web 老 `fetchRooms` 解包 `rooms` 仍兼容（旧客户端读到**被截断**的首屏，正是 spec 语义4要拦的场景）。
25. 2026-09-15：双写字面：`StandardOutPath`/`StandardErrorPath` 两个 key 由 `internal/service/launchd.go:118-119` 发出，值都取 `spec.LogPath`（`cmd/service.go:83` 置为 `agentd.log`）；`logx.Setup` 的 JSON handler 又写同文件。**谁消费**：launchd 只重定向、不解析；`agentd.log` 内容由 logx 与 stderr 文本两路共同产生。→ 单写修复有二选：agentd 侧不再同时写文件（去掉 JSON handler）或 manager 侧不再把 stderr 导向同一文件。契约冻结取**前者**（见 §3.4 与拍板记录 P2）。
26. 2026-09-15：`parseLevel`（`internal/logx/logx.go:46-57`）默认分支返回 `slog.LevelInfo`——spec 语义3「默认级别 warn」要求改动此默认，这是**唯一**级别默认来源，无第二处。
27. 2026-09-15：`codegraph/diffs/` 在本分支为空（`ls codegraph/diffs` 失败）；本分支当前无视图。历史合同视图命名先例：`codegraph/diffs/cards-B370-charter-4.json`（`view` 字段仍写 `cards/B370-charter-4`）。

## 2. 图覆盖债（未命中事项）

28. 2026-09-15：`logx.Setup` **在图中**（`n_logx_Setup`，`k_logx_fn`，域 `d_runtime_config`），与 spec 备注「logx.Setup 未入代码图」不符——spec 备注基于旧扫描；本轮核实图已覆盖，覆盖债注销。`codegraph sym logx`（裸包名）查不到是查询串问题，非覆盖问题。
29. 2026-09-15：`codegraph sym 'collab.Service.ListRoomsForMember'` / `'agentd.Server.handleRoomsList'` 报「不在图中」——**点号全名不是合法查询串**；用节点 id（`n_...`）命中。属查询语法，非覆盖债。

## 3. 拍板记录（三重闸门）

> 每条同时满足：难逆转 / 无上下文会惊讶 / 真取舍。被否方案一并记。

### P1：attach 刷新限域按「本页房间 ID 集合」，而非「本页 links」

- **决定**：`enrichRoomAttachments` 与 `startRoomAttachRefresh` 的输入从「全量 `AllTaskLinks()`」收窄为「本页返回房间对应的 links」。
- **被否**：把限域放在 `startRoomAttachRefresh` 内按链接反查房间是否在页内——需要一个房间↔link 反向索引，且 cache key 是 `target\0taskID` 而非 room；复杂度更高。
- **为什么难逆转/会惊讶**：后人看 `enrichRoomAttachments` 会觉得「为何不一次把所有挂账都刷了，反正后台」——顺手「优化」成全量会悄悄把风暴带回来，且没有任何既有测试会变红（性能断言只在 200 卡 TTFB 且不数远端 RPC）。故必须留文档。

### P2：单写修复取「agentd 侧只写一处」而非「manager 侧不再重定向 stderr」

- **决定**：`logx.Setup` 在带 `logPath` 时不再把 JSON 文件 handler 与 stderr 文本 handler 同时挂上；即同一记录不再落两类 handler（修后落盘恰一次）。
- **被否**：改 `launchd.plist` 让 `StandardErrorPath` 指向另一文件或 `/dev/null`——那会丢掉 launchd 重定向下 agentd 启动早期（logx 之前的 panic/库输出）的 stderr，且 systemd/windows 平台行为不一致。
- **为什么难逆转/会惊讶**：`multiHandler` 的设计意图是「同一格式记录在每路输出完整一致」（`logx.go:59` 注释），去掉一路看起来像功能倒退；必须写明「双路在 launchd 同文件重定向下退化为双写」的因果。

### P3：旧客户端策略取「阻断升级提示」而非「降级首屏」

- **决定**：spec 语义4 二选一，冻结为**阻断提示**（默认）；新 agentd 检测到请求未带分页参数时，返回可行动的升级提示，不静默返回半页。
- **被否**：降级首屏（旧客户端拿到不带游标语义的一页）——静默半页正是 spec US5 要消除的「不明不白的半页数据」。
- **为什么难逆转/会惊讶**：后人会想「加个兼容分支让它先用着」；且「反过来写不会有任何测试变红」——兼容返回半页在功能测试里与正确返回无差异，只有旧客户端集成才暴露。必须显式记录。

### P4：legacy 探测靠「既无 limit 也无 cursor」的请求形态，不新增客户端版本头

- **决定**：old-client 判据 = 请求 query 里 `limit` 与 `cursor` **双双缺席**。命中即走 P3 的阻断（HTTP 426 + 可行动文案）。分页客户端的**首屏也显式带 `limit`**（新 web `fetchRooms` 首拉带 `limit=50`）。在已判定为分页客户端的请求内，`limit` 缺席（例如只带 `cursor`）取默认 50，`limit>200` 取 200。
- **被否**：新增 `X-Handoff-Client-Version` 头探测——外置桌面 app 改动面更大，且 spec 只要求发版提示，不要求版本协商。
- **为什么难逆转/会惊讶**：这是「反过来写不会有任何测试变红」的流程裁决——若把「缺 limit」也当作合法默认而非 legacy，新 web 首屏与旧客户端请求形态完全相同（都是无参），阻断与默认就互相吞掉，T2/T3 各自绿而集成必翻；无文档后人一次「顺手」就把旧客户端当新客户端返回首屏半页。故必须显式记录。

## 4. Ticket 0 骨架与本轮读数

30. 2026-09-15：`internal/proto/rooms.go` 新增 `RoomsPage`（`rooms`+`next_cursor,omitempty`+`has_more`）。
31. 2026-09-15：`internal/collab/service.go` 新增 `roomsPageDefaultLimit/roomsPageMaxLimit`、`roomCursor`、`encodeRoomCursor`、`decodeRoomCursor`、`trimRoomPage`（直通镜像）、`ListRoomsPage`（直通镜像）；`ListRoomsForMember` 签名保持不变。
32. 2026-09-15：`internal/agentd/roomsapi.go` 新增 `roomsListDefaultLimit/roomsListMaxLimit`、`roomsListParams`、`parseRoomsListParams`（直通镜像）；`handleRoomsList` 改经 `ListRoomsPage` 回 `proto.RoomsPage` 信封。
33. 2026-09-15：`go build ./...` 退出码 0（原样输出 `EXIT=0`）。
34. 2026-09-15：`gofmt -l` 对三处改动文件无输出；`go vet ./internal/... ./cmd/` 退出码 0。
35. 2026-09-15：`go test ./internal/proto/ ./internal/collab/ -count=1` → 两包 `ok`；`go test ./internal/agentd/ -run 'TestRooms|TestInbox' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 6.794s`；`go test ./internal/collab/ ./internal/proto/ ./cmd/ -count=1` → 三包 `ok`（含 `cmd` 21.221s）。
36. 2026-09-15：可执行冻结——游标金样本 `TestEncodeRoomCursorGolden`、往返、空游标、非法游标、`TestListRoomsPageRejectsMalformedCursor` 共 5 支（`internal/collab/roomcursor_test.go`）`PASS`；信封金样本 `TestRoomsPageGoldenEnvelope`（`internal/proto/rooms_fixture_test.go`）`PASS`。
37. 2026-09-15：用历史重扫器（`git show 6f10ba7c:scripts/codegraph-rescan`，临时 module，仅用于取值，不落库）对工作树重扫，取新符号节点定义；本分支视图 `codegraph/diffs/cards-B374-charter.json` 落 `containersAdded {k_collab_model}`、8 新节点、1 改动节点（`n_agentd_Server_handleRoomsList`，旧签名入 `signatureOld`）、9 新边、1 删边（`handleRoomsList→ListRoomsForMember`）。
38. 2026-09-15：`codegraph validate` 退出码 0，`views` 含 `cards-B374-charter`；`codegraph --view cards-B374-charter check` 退出码 0、fails 空；无视图 `codegraph check` 亦 fails 空（本卡新边全落既有 entry，target 无口径增量）。`go test ./cmd/ -run '^TestRepoContractGate$' -count=1` → `ok`。
39. 2026-09-15：`codegraph resolve --doc docs/superpowers/specs/b374-contract.md` 退出码 0（坏锚 `codegraph/check.go#Check` 已改为非锚引用；`file_missing` 因该文件在模块缓存非本仓）。

## 5. 冻结提交（历史读数）

40. 2026-09-15：本节点冻结即提交，命令 `git add <8 文件> && git commit -F -`；`git log --oneline -1` 原始输出 `7ca9eb48 contract(B374): 房间列表强制分页 wire 冻结——Ticket 0 骨架 + 视图`；`git status --short` 提交后回显 `8 files changed, 851 insertions(+), 5 deletions(-)`（提交输出）。本条回填后 amend 一次收进同批提交——amend 会换 hash，收口判据是工作树干净，不 chase hash。
41. 2026-09-15：提交后复核 `git status --porcelain` 无输出（工作树干净）；`go build ./...` 退出码 0；`codegraph --view cards-B374-charter check` fails 0；`codegraph validate` issues null。
