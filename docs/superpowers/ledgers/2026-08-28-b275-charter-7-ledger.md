# B275 charter-7 执行台账

## 2026-08-28

- 节点基线：当前分支 `cards/B275-charter-7`，HEAD `a1bb4bb9`（`fix(b275): defer remote room attachment lookup`）；初始 `git status --short --branch` 工作树干净。
- 计划与 spec 已读取；本轮五项 review-5 finding 为 attach 缓存过期/失效、终态卡深链、计划修订、HTTP raw unread=0、`logRoom`/职责注释。
- 局部 Go 基线命令 `go test ./internal/proto ./internal/collab ./internal/agentd -count=1` 已启动；proto 输出 `ok github.com/Xsxdot/handoff/internal/proto 0.003s`，collab 输出 `ok github.com/Xsxdot/handoff/internal/collab 3.431s`；agentd 运行未在本轮结束前产出，随后以 Ctrl-C 退出码 130，未验证其结果。
- 局部 Web 基线命令 `cd web && npm test -- --run src/api/rooms.test.ts src/api/rooms.fetch.test.ts src/app/rooms/RoomPanel.test.tsx src/app/cards/CardsPage.test.tsx` 退出 127，原始错误：`sh: 1: vitest: not found`。
- 源码事实：`internal/agentd/roomsapi.go` 目前将远端 attach 写入 `roomAttachCache`，只按 5 秒刷新节流读取，没有缓存时间戳/TTL，也没有远端刷新失败时删除旧投影的失效语义。
- 源码事实：`web/src/app/cards/CardsPage.tsx` 默认 `fetchCards('')`，仅在 `cards` 已进入内存集合时按 `?card=` 设置抽屉；终态卡不在默认集合时深链没有可选卡。
- 源码事实：`proto.RoomSummary.Unread` 使用 `json:"unread"`（无 `omitempty`）；现有 agentd 测试主要 typed decode，尚缺 raw body 对 `unread:0` 的边界断言。
- 首轮 review-5 红测：Go attach 缓存测试因 `*proto.RoomAttach` 没有 `expiresAt` 字段而 `build failed`；该新接缝符号缺席不是 typo。
- 首轮 review-5 红测：Web `CardsPage.test.tsx` 终态深链失败，原始断言为 `Unable to find role="dialog" and name "工作项详情"`；RoomPanel 卡片入口日志断言失败，当前调用序列仅有 `rooms_request_*`、`inbox_request_*`、`room_opened`、`card_detail_request_*`，没有 `card_open_requested`。
- 空壳复测：为缓存条目补 `expiresAt` 元数据但暂不检查失效，`go test ./internal/agentd -run 'TestRoomsListWireIncludesZeroUnread|TestRoomsListDropsExpiredAndFailedRemoteAttachCache' -count=1` 退出 1，原始失败为 `roomsapi_test.go:403: 等待超时: 刷新失败删除旧 attach 缓存`；确认断言有牙后才进入实现。
- review-5 最小实现：`cachedRoomAttach` 现在按 TTL 删除过期条目，后台刷新失败调用 `invalidateRoomAttach` 删除旧条目；CardsPage 深链带 `all=1`，RoomPanel 卡片打开入口补 `logRoom`。
- review-5 接缝绿测：`gofmt -w internal/agentd/roomsapi.go internal/agentd/roomsapi_test.go internal/agentd/server.go && go test ./internal/agentd -run 'TestRoomsListWireIncludesZeroUnread|TestRoomsListDropsExpiredAndFailedRemoteAttachCache|TestRoomsListUsesBackgroundAttachCache' -count=1` 退出 0，原始输出 `ok github.com/Xsxdot/handoff/internal/agentd 0.624s`；`npm test -- --run src/app/cards/CardsPage.test.tsx src/app/rooms/RoomPanel.test.tsx` 退出 0，原始输出 `Test Files 2 passed (2)`、`Tests 17 passed (17)`、`Duration 1.54s`。
- 计划修订：`docs/superpowers/plans/b275-plan.md` 增加 review-5 r6，明确远端 attach TTL/刷新失败失效、终态深链 `all=1`、raw unread=0 及 `logRoom` 可执行验收。
- 变异自验一：唯一命中缓存过期判断 `if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt)`，临时改为 `now.Before(...)`；`go build ./...` 退出 0；随后 `go test ./internal/agentd -run '^TestRoomsListDropsExpiredAndFailedRemoteAttachCache$' -count=1` 退出 1，原始失败 `roomsapi_test.go:423: 过期 attach 不得从缓存返回: &{Target:relay TaskID:T-relay WorkDir:/relay/B1 Command:handoff attach T-relay}`；已恢复。
- 变异自验二：唯一命中终态深链 `cardDeepLink !== ''` 临时取反；`npm run typecheck` 退出 0；随后 `npm test -- --run src/app/cards/CardsPage.test.tsx` 退出 1，原始汇总 `Tests 1 failed | 6 passed (7)`，失败为 `Unable to find role="dialog" and name "工作项详情"`；已恢复。
- 变异自验三：唯一命中 `Unread int json:"unread"` 临时加 `omitempty`；`go build ./...` 退出 0；随后 `go test ./internal/agentd -run '^TestRoomsListWireIncludesZeroUnread$' -count=1` 退出 1，原始失败 `roomsapi_test.go:231: 房间行必须保留 unread:0，原文={"id":"P1","kind":"card","project":"p","title":"零未读卡","live":false,"read_only":false,"last_activity":"2026-08-27T20:41:25.309819747Z"}`；已恢复。
- 触及包回归：`gofmt ... && go test ./internal/proto ./internal/collab -count=1 && go test ./internal/agentd -run 'TestRoomsList(WireIncludesZeroUnread|DropsExpiredAndFailedRemoteAttachCache|UsesBackgroundAttachCache|UnreadAndAttachProjection|WithoutAttachmentKeepsAttachMissing|AttachTimeoutDoesNotBlockMainList|Endpoint)$' -count=1` 退出 0；原始输出为 proto `0.003s`、collab `3.167s`、agentd `1.283s`。
- Web 门禁：`npm test -- --run src/api/rooms.test.ts src/api/rooms.fetch.test.ts src/app/rooms/RoomPanel.test.tsx src/app/cards/CardsPage.test.tsx` 退出 0，原始输出 `Test Files 4 passed (4)`、`Tests 31 passed (31)`、`Duration 1.28s`；`npm run typecheck` 退出 0；`npm run lint` 退出 0，原始输出 `✖ 20 problems (0 errors, 20 warnings)`；`npm run build` 退出 0，原始输出 `✓ 1970 modules transformed`、`✓ built in 2.22s`，仅 chunk 大小 warning。
- agentd 全包回归：`go test ./internal/agentd -count=1` 退出 1，原始失败为 `--- FAIL: TestWSTruncationWarnsOnRealGap (30.16s)`、`ws_regression_round2_test.go:316: 等待 seq=21 时读失败: failed to get reader: context deadline exceeded`、最终 `FAIL github.com/Xsxdot/handoff/internal/agentd 160.249s`；未改该测试范围，需定向复跑记录。
- agentd 失败定向复跑：`go test ./internal/agentd -run '^TestWSTruncationWarnsOnRealGap$' -count=1` 退出 0，原始输出 `ok github.com/Xsxdot/handoff/internal/agentd 0.094s`。
- 全仓 Go 集成：`go test ./...` 退出 1；可见汇总显示 `ok github.com/Xsxdot/handoff/internal/agentd 143.296s` 及其余大多数包通过，最终 `FAIL` 来自 `github.com/Xsxdot/handoff/cmd`；执行器输出中间段截断，未把截断部分当作根因。
- cmd 失败定向复跑：`go test ./cmd -run 'TestRepoContractGate|TestMaybeInstallServiceLinuxRootInstalls' -count=1` 退出 1。原始错误包括 `契约违规 [dead-contract] 契约 d_cli→d_collab ...`、`契约违规 [dead-contract] 契约 d_gateway→d_collab ...`、`契约违规 [dead-entry] ... "collab 包级函数" ...`，以及 `托管失败：加载配置 /tmp/handoff.yaml: 写默认配置 /tmp/handoff.yaml: open /tmp/handoff.yaml: read-only file system`；未改范围外代码。
- race 局部回归：`go test -race ./internal/agentd -run 'TestRoomsList(WireIncludesZeroUnread|DropsExpiredAndFailedRemoteAttachCache|UsesBackgroundAttachCache|UnreadAndAttachProjection|WithoutAttachmentKeepsAttachMissing|AttachTimeoutDoesNotBlockMainList|Endpoint)$' -count=1` 退出 0，原始输出 `ok github.com/Xsxdot/handoff/internal/agentd 3.014s`。
- 全量编译与静态检查：`go build ./...` 退出 0；`go vet ./internal/proto ./internal/collab ./internal/ledger ./internal/agentd` 退出 0，均无输出。
- 全量 Web 测试：`npm test -- --run` 退出 0，原始输出 `Test Files 110 passed`、`Tests 1134 passed`、`Duration 17.76s`；测试过程中仍出现既有 `Not implemented: HTMLCanvasElement's getContext()` 提示。
- 提交：已在当前分支创建 `5211ebf2 fix(b275): expire remote room attach cache`；未 push。

## 2026-08-28 charter-8 review-6

- 节点基线：当前分支 `cards/B275-charter-8`，HEAD `f29afa60`；初始工作树无短状态输出。
- 环境命令 `cd web && npm ci` 退出 0；原始输出尾部为 `added 290 packages, and audited 291 packages in 2s`、`found 0 vulnerabilities`。
- 环境基线命令 `cd web && npm test -- --run` 退出 0；原始输出尾部为 `Test Files 110 passed (110)`、`Tests 1134 passed (1134)`、`Duration 15.70s`；过程中有既有 `Not implemented: HTMLCanvasElement's getContext() method: without installing the canvas npm package` 提示。
- review-6 Go 测试新增后，`gofmt -w internal/agentd/roomsapi_test.go && go test ./internal/agentd -run '^TestRoomsListDropsExpiredAndFailedRemoteAttachCache$' -count=1` 退出 0；原始输出 `ok github.com/Xsxdot/handoff/internal/agentd 0.185s`。
- review-6 Web 顺序测试首轮红测真实触发新断言：`npm test -- --run src/app/rooms/RoomPanel.test.tsx` 输出 `1 failed | 10 passed (11)`，原始断言为期望 `['logRoom', 'onOpenCard']`、实际含 10 个初始化日志后再有 `onOpenCard`；测试夹具随后收窄为只记录 `card_open_requested`。
- review-6 Web 顺序测试修正后，`npm test -- --run src/app/rooms/RoomPanel.test.tsx` 退出 0；原始输出 `Test Files 1 passed (1)`、`Tests 11 passed (11)`、`Duration 1.14s`。
- task 收尾 Go 编译 `go build ./...` 退出 0，无输出。
- task 收尾 agentd 定向测试 `go test ./internal/agentd -run 'TestRoomsList(WireIncludesZeroUnread|DropsExpiredAndFailedRemoteAttachCache|UsesBackgroundAttachCache|UnreadAndAttachProjection|WithoutAttachmentKeepsAttachMissing|AttachTimeoutDoesNotBlockMainList|Endpoint)$' -count=1` 退出 0；原始输出 `ok github.com/Xsxdot/handoff/internal/agentd 1.678s`。
- task 收尾 Web typecheck `npm run typecheck` 退出 0；原始输出仅为 npm 脚本头与 `tsc -b`。
- task 收尾 Web 触及测试 `npm test -- --run src/api/rooms.test.ts src/api/rooms.fetch.test.ts src/app/rooms/RoomPanel.test.tsx src/app/cards/CardsPage.test.tsx` 退出 0；原始输出 `Test Files 4 passed (4)`、`Tests 32 passed (32)`、`Duration 2.42s`。
- 收尾全量 Web `npm test -- --run` 退出 0；原始输出 `Test Files 110 passed (110)`、`Tests 1135 passed (1135)`、`Duration 30.38s`；过程中有既有 canvas `getContext()` 未实现提示。
- 收尾全仓 Go `go test ./...` 退出 1；原始汇总为 `ok github.com/Xsxdot/handoff/internal/agentd 160.872s` 及其余包多数 `ok`，末尾为 `FAIL`；`cmd` 失败随后以定向命令取得完整原始报错。
- 全仓 Go 失败定向复跑 `go test ./cmd -run 'TestRepoContractGate|TestMaybeInstallServiceLinuxRootInstalls' -count=1` 退出 1；原始报错为 `契约违规 [dead-contract] 契约 d_cli→d_collab ...`、`契约违规 [dead-contract] 契约 d_gateway→d_collab ...`、`契约违规 [dead-entry] ... "collab 包级函数" ...`、`legacy 命中: map[...]，warn 102 条`，以及 `托管失败：加载配置 /tmp/handoff.yaml: 写默认配置 /tmp/handoff.yaml: open /tmp/handoff.yaml: read-only file system`；未修改 cmd 范围外代码。
- 变异自验（Go）：确认过期判断文本唯一命中 1 处；临时取反后 `go build ./...` 退出 0，相关用例真实变红，原始失败为 `roomsapi_test.go:448: 过期 attach 不得从缓存返回: &{Target:relay TaskID:T-relay WorkDir:/relay/B1 Command:handoff attach T-relay}`；随后已恢复实现。
- 变异自验（Web）：确认 `card_open_requested` 调用文本唯一命中 1 处；临时交换 `onOpenCard` 与 `logRoom` 后 `npm run typecheck` 退出 0，单用例真实变红，原始断言实际为 `["onOpenCard", "logRoom"]`、期望 `["logRoom", "onOpenCard"]`；随后已恢复实现。
- 恢复临时变异后的最终 Go 编译 `go build ./...` 退出 0，无输出；agentd 定向测试再次退出 0，原始输出 `ok github.com/Xsxdot/handoff/internal/agentd 2.206s`。
- 恢复临时变异后的 Web typecheck `npm run typecheck` 退出 0；lint `npm run lint` 退出 0，原始汇总 `✖ 20 problems (0 errors, 20 warnings)`；build `npm run build` 退出 0，原始输出 `✓ 1970 modules transformed`、`✓ built in 5.50s`，仅既有 chunk 大小 warning。
- 恢复临时变异后的全量 Web `npm test -- --run` 退出 0；原始输出 `Test Files 110 passed (110)`、`Tests 1135 passed (1135)`、`Duration 19.82s`；仍有既有 canvas `getContext()` 未实现提示。
- 提交：当前分支已创建 `d2a8bef0 test(b275): cover room attach invalidation and card log order`；未 push。
