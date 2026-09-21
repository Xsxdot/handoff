# B374 acceptance 台账（2026-09-16）

协调者本机复跑。工作树 `/Users/sycm/.handoff/repos/handoff/.worktrees/b374-charter-8`，HEAD `b03a66ba4`。

## 1. 复跑

1. `go build ./...` → 退出 0。
2. `go test ./internal/collab/ ./internal/proto/ ./internal/logx/ -count=1` → 三包 `ok`。
3. `go test ./internal/agentd/ -run 'TestRooms|TestListRooms' -count=1` → `ok`（3.099s）。
4. 本工作树无 `web/node_modules`：vitest/tsc **未验证**（linux-01 implement 节点曾跑绿 124 files / 1333 tests，不顶替本轮新鲜证据）。

## 2. 变异复验（可编译、命中唯一、还原）

5. `trimRoomPage` 兜底改回「首个 Before 切尾巴」：`go build ./internal/collab/` 通过；`TestListRoomsPageFallbackFiltersNonMonotonicFlatOrder` FAIL，输出 `[A Z]`。还原后 `ok`。
6. `parseRoomsListParams` 双缺席 `Legacy: true` → `false`：`go build ./internal/agentd/` 通过；`TestRoomsListLegacyRequestRejectedWith426` FAIL，`/api/rooms` 实得 200 且 body 含 `rooms`。还原后 `ok`。

## 3. 真机清单（未验证，留残余）

7. 366 房间首屏 ≤2s / card wait 秒级：未跑。
8. 真桌面端 426 呈现：未跑。
9. 真 Attach RPC 从 800+ 降到本页：未跑。
10. linux-01 镜像 `context deadline exceeded` 归因：未跑（机内仅证通路独立）。
11. launchd 下 `agentd.log` 单写与 100MB×5 轮转实况：未跑。
12. Windows 轮转改名句柄：未跑。

以上 7–12 写入 `docs/roadmap.md` 残余，不宣称 US1/US4/US5 已验。
