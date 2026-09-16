# B374 implement 节点台账

节点：implement（plan `docs/superpowers/plans/b374-plan.md`，冻结契约 `docs/superpowers/specs/b374-contract.md` §5 重编号 F1–F22 为准）。
卡：B374　工作分支：`cards/B374-charter-7`　基点：`c2c856f70`（plan 提交）。
一行一条历史读数；命令与原始输出照抄，不做二次加工。

## 0. 落点与基线

1. 2026-09-16：`git status --short --branch` → `## cards/B374-charter-7`（干净）。`git log --oneline -3` 头部 `c2c856f7 docs(B374): plan for pagination, attach refresh scoping and log triple`。
2. 2026-09-16：`go build ./...` → 退出 0，无输出。
3. 2026-09-16：`go test ./internal/agentd/ -run 'TestRooms' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 5.539s`。
4. 2026-09-16：`go test ./internal/collab/ ./internal/logx/ -count=1` → 两包 `ok`（collab 4.668s、logx 0.002s）。
5. 2026-09-16：`codegraph sym n_agentd_Server_handleRoomsList --repo .` → `anchor=moved`、`container=k_agentd_Server`、`file=internal/agentd/roomsapi.go:93`；`codegraph sym n_collab_Service_ListRoomsPage --repo . --view cards-B374-charter` → `anchor=ok`、`service.go:333`；`n_collab_trimRoomPage` 同视图 → `anchor=ok`、`service.go:397`；`n_logx_parseLevel` → `domain=d_runtime_config`（C-4 已知覆盖债，不修）。均与 plan §1 基线一致。

## 1. T1 gateway（426 / 真解析 / 限域调用点 / INFO→Debug）

6. 2026-09-16：先写失败测试（新增 `roomsLogCapture`、`decodeRoomsPage`、`testRoomCursor`、4 支新测试；P-3 清单 13 处无参请求改 `limit=50`，`:623`（未装配 503）保持无参）。首红为编译红 `roomsapi_test.go:142:30: undefined: roomsListLegacyMessage`（符号缺席）；补空壳 const 后转断言红。
7. 2026-09-16：断言红原文（补 shell 后）：
   - `TestRoomsListLegacyRequestRejectedWith426`：`/api/rooms 旧客户端必须 426，实得 200 {...}`。
   - `TestRoomsListPaginationHTTP`：`limit=0 应取默认 50 且 has_more=true: len=207 has_more=false`。
   - `TestRoomsListAttachRefreshLimitedToPageRooms`：`夹具失效：远端卡必须不在首页`（基线全量返回）。
   - `TestRoomsListAttachLogsAtDebug`：`高频 attach INFO 必须降 Debug，实得 INFO`。
8. 2026-09-16：实现 `parseRoomsListParams` 真解析、`handleRoomsList` 三路分流（parse 错 400 直回 / Legacy 426 / ErrInvalidCursor 400 / 其余 500）、`enrichRoomAttachments` 收窄 `pageLinks`、`startRoomAttachRefresh(pageLinks)`、5 处高频 Info→Debug。`go test ./internal/agentd/ -count=1` → `ok ... 201.220s`。

9. 2026-09-16：变异自验（脚本两段判定：先 `go build ./...` 通过再数 `^--- FAIL`；命中唯一 `count(old)==1` 前置断言）：
   - legacy 探测取反 `&& `→`!= && !=` → 编译 PASS，`^--- FAIL`=1（426 测试红）。
   - 426 状态码 `StatusUpgradeRequired`→`StatusOK` → 编译 PASS，FAIL=1。
   - `房间 attach 投影完成` Debug→Info → 编译 PASS，FAIL=1。
   - `pageLinks` 收窄改回全量 `for _, link := range links` → 编译 PASS，FAIL=1。
   - `if n > roomsListMaxLimit` → `> roomsListMaxLimit*10` → 编译 PASS，FAIL=0（该路无对应断言命中；限上限由 collab 侧 `TestListRoomsPageLimitClamp` 锁）。

## 2. T2 collab（真裁剪 + 游标定位）

10. 2026-09-16：先写 `internal/collab/rooms_page_test.go` 六支 + `roomcursor_test.go` 属性测试。首红：`TestListRoomsPageLocatesByRoomIDNotIDOrder`（实得 `[B2 B1 B3]`）、`TestListRoomsPageFallbackSkipsSameInstant`（实得 `[B1 B2]`）、`TestListRoomsPageCursorRoomPresentVsRemoved`（首页 `[B000 B001 B002 B003]`）、`TestListRoomsPageLimitClamp`（limit=0 实得 251）。两支（`PaginatesFlatOrder`/`KeepsTerminalRoomsReachable`）在直通镜像下意外绿——各补「单页不得超过 limit」断言，使其在直通镜像下也红。
11. 2026-09-16：实现 `trimRoomPage` 真裁剪（主判据 roomID 位置、兜底 LastActivity.Before、不比 ID 序）、`ListRoomsPage` 成功路径 Debug 日志、删旧注释（C-3）。`go test ./internal/collab/ -count=1` → `ok ... 4.032s`。
12. 2026-09-16：变异自验：
   - 主判据改 `rooms[i].ID > roomID`（ID 序兜底）→ 编译 PASS，FAIL=1。
   - 兜底 `LastActivity.Before(at)` → `.After(at)` → 编译 PASS，FAIL=2。
   - `if limit <= 0` → `if limit < 0` → 编译 PASS，FAIL=1。

## 3. T4 logx（默认 warn / 单写 / 轮转）

13. 2026-09-16：先写 `level_internal_test.go`、`rotate_test.go`、`logx_test.go` 新增两支（P-5：`TestSetupWritesJSONToFile` 首行加 `t.Setenv("HANDOFF_LOG_LEVEL","info")`）。首红为编译红 `undefined: newRotatingHandler` / `logRotationMaxBytes` / `logRotationMaxBackups`（符号缺席）。补 `rotate.go` 空壳后转断言红（默认级别、单写均在基线多写/默认 info 下红）。
14. 2026-09-16：新建 `rotate.go`（rotationState + rotatingHandler），重写 `logx.go`（默认 warn、带 logPath 只挂文件 handler、删 `multiHandler`）。`grep -rn multiHandler internal/` → 无命中（退出 1）。`go test ./internal/logx/ -count=1` → `ok ... 0.003s`。
15. 2026-09-16：变异自验：
   - `default: return slog.LevelWarn` → `LevelInfo` → 编译 PASS，FAIL=2。
   - `newRotatingHandler(...); err == nil` → `err != nil` → 编译 PASS，FAIL=1（单写测试红）。
   - `logRotationMaxBackups = 4` → `5` → 编译 PASS，FAIL=1。
   - 单删 `os.Remove(...backups)`（保留其余）→ 编译 PASS，FAIL=1（`实得 6`，证明「第 6 份挤掉最旧」有牙）。
   - `if s.size <= s.maxBytes` → `<` → 编译 PASS，FAIL=0（等价变异：`<=` 与 `<` 在单条写入超阈值场景下行为一致，无法区分）。
   - 删 `os.Remove` + `backups-1`→`backups` 的组合变异 → 编译 PASS，FAIL=1。
   - 直接 `h = rh` → TextHandler 的变异**编译失败**（`rh` 未使用），按纪律不计入，改用 `err == nil`→`!= nil` 等价可编译变异。

## 4. T3 web（镜像 + 懒加载 + 426）

16. 2026-09-16：`web/node_modules` 缺失，`npm ping` → `PONG`，`npm ci --no-audit --no-fund` → `added 290 packages in 2s`。
17. 2026-09-16：先改 `rooms.fetch.test.ts`（信封形状/limit/续载/缺键可分辨）、`RoomPanel.test.tsx`（15 处 mock 改信封形 + 3 支新用例）、`Shell.test.tsx`（1 处 mock 改信封）。首红：`TypeError: rooms.filter is not a function`、20 收敛为 21 failed（实现未改）。
18. 2026-09-16：实现 `rooms.ts` 的 `RoomsPage`/新 `fetchRooms`、`RoomPanel.tsx`（首屏 limit=50、`firstPageSignature` 重置、`loadMore`、`onRoomsScroll`、426 提示块、`room-list-scroll` testid）。
19. 2026-09-16：定向绿 `npx vitest run src/api/rooms.fetch.test.ts src/app/rooms/RoomPanel.test.tsx src/app/shell/Shell.test.tsx` → `Test Files 3 passed / Tests 82 passed`。`npx tsc -b` → 退出 0。
20. 2026-09-16：全量跑发现两支用例在全量并发下间歇红：根因是 `vi.clearAllMocks()` 不清 `mockResolvedValueOnce` 队列（上层用例泄漏）+ 5s 轮询抢占 once 值。修法：`beforeEach` 改 `vi.resetAllMocks()`；续载用例改按 `opts.cursor` 分派 `mockImplementation`，滚动在 `waitFor` 内重发，终止断言改数「带 cursor 的调用数」。全量 `npx vitest run` → `Test Files 124 passed (124) / Tests 1333 passed (1333)`，退出 0。

## 5. T5 欠账收口

21. 2026-09-16：`TestRoomsListPageEnvelopeWireShapes` 穿过真实 `writeJSON` 断言 rooms/has_more 恒出键（含 false）、next_cursor 缺席 → `ok`。
22. 2026-09-16：文档修订按 P-1 甲——`b156.2-breakdown.md:39` 原文后追加「B374 废止」注；`b156.2.8-plan.md:1449` 原文后追加「（B374 废止：会话列表已改为服务端分页。）」。两处均保留原决策句。
23. 2026-09-16：`git diff -- internal/agentd/mirror.go` → **空**（P-2：不改 mirror.go）。
24. 2026-09-16：欠账 8 机内通路独立性结论（只读证据）：镜像发现是独立循环（`mirror.go:34` tick=30s、`:48` budget=3s、`:130 Run` 周期调 `:183 discoverOnce`）；房间列表刷新通路 `handleRoomsList → enrichRoomAttachments → startRoomAttachRefresh`（`roomAttachMu` 门 + 5s 节流 + 10s 超时）。两者**无共享锁**（镜像不碰 `roomAttachMu`）、**无共享 goroutine**（镜像 goroutine 归 `Mirror.wg`，attach 归自身 `sync.WaitGroup`）、**无共享远程调用**（镜像 `pool.For(name).ListTasks`，attach `pool.For(target).Attach`）。结论：code-path 上镜像发现与房间列表刷新相互独立，`linux-01 context deadline exceeded` 不是刷新风暴的次生症状；真实归因（3s 预算耗尽/网络抖动/真机环境）归协调者真机清单。

## 6. 收口验证

25. 2026-09-16：`go build ./...` → 退出 0。
26. 2026-09-16：`go test ./internal/agentd/ ./internal/collab/ ./internal/logx/ ./internal/proto/ -count=1` → `ok agentd 197.158s`、`ok collab 4.628s`、`ok logx 0.008s`、`ok proto 0.011s`。
27. 2026-09-16：web 全量 `npx vitest run` → `Test Files 124 passed (124) / Tests 1333 passed (1333)`；`npx tsc -b` → 退出 0。

28. 2026-09-16：提交事实。`git add <19 文件> && git commit -m "feat(B374): server-side rooms pagination, attach refresh scoping, log triple"` 原始输出尾部：`5fea0faa feat(B374): server-side rooms pagination, attach refresh scoping, log triple`、`create mode 100644 docs/superpowers/ledgers/2026-09-16-b374-implement-ledger.md`（另 4 新文件）。本条随手 amend 收进同批提交；amend 会换 hash，不回写、不 chase，收口判据是工作树干净。
