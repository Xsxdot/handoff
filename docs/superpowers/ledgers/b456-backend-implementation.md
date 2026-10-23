# B456 后端实现记录

## 偏离与自主决定

- 按主会话冻结澄清从 plan r1.2 收口到 r1.3：远端 mirror snapshot 只有与同 task ledger lifecycle 时间可比且更新时才可覆盖；`FetchedAt` 不作跨时钟排序。未更改 r4 的六态和冻结 wire。
- 按主会话 F8 裁决，票据候选超过 500 时不增加 wire/分页；collab SessionDetail 直接返回可行动错误“会话待处理事项超过本次读取上限，请先通过全局待处理入口处理或拆分会话”，HTTP 走既有 unavailable/error 路径。票据截断不再混入 `MoreActivity`。
- opt-in harness 的 P1 采用真实 manager/fake executor `Dispatch`，真实 permission/question/Finish producer 与 HTTP reply；P2 completed、P3 wake-failure 仍是明确注明的 ledger/mirror fixture。无生产 agentd、线上端口或 PG 被启动/触碰。

## 完成内容

- `internal/ledger` 增加限定当前会话 card IDs 的有界事实查询，并沿 `ledger/api` 与 `collab/client` 暴露。普通 comment 在 SQL 层按 `wake_round:` 排除；固定保留最新 dispatch、当前席位绑定、当前 dispatch 原始镜像和最新 lifecycle 镜像；task lifecycle 额外保留只关联当前 `(target, task)`，不会按历史 task 分区无界增长。超出常规窗口显式 `truncated`。
- 未决票据只读权威 `open_ticket_projection`，按卡范围查询且只返回引用。超出 500 项则 SessionDetail 失败关闭，避免漏票造成不可达动作；`MoreActivity` 仅表示动态截断。
- `collab` 纯投影处理 resident 作者、executor/协调者独立性、空 target 精确关联、生命周期状态、wake 当前席位/dispatch 边界、票据和事件摘要。普通 progress 仅作 recent activity，不改变生命周期基准。`delivery_failed`、`turn_failed`、`failed` 显示公开原因摘要并截断，不读取完整执行输出。
- agentd 组装本机 manager task snapshot；只有本机 `GetTask` 明确 `ErrNotFound` 时读单条既有 mirror snapshot，不调用全量列表或远端 fanout，并校验 target。`Task.UpdatedAt` 仅用于同 task snapshot 与 ledger lifecycle 冲突排序，不作为 since 或心跳；快照无法可靠排序时不复活旧状态。
- `internal/store`/`orchestration` 增加既有 `mirror_tasks` 的单 task 只读 Get；`SessionRoleBasis` 和排序元数据均为 `json:"-"`。真实 mux HTTP JSON 生成共享金样 `web/src/api/testdata/SessionDetailB456.json`，其中包含 roles、recent、ticket_refs 和缺省 since。
- opt-in browser harness 默认 Skip，启用后使用临时 SQLite/httptest，manifest 权限 0600。真实 P1 task 由 `/api/tasks/{taskID}` 读到全文 pending permission 后保持未答，供浏览器操作；fake 脚本顺序为 permission gate #1、question、permission gate #2、Finish；浏览器先批准第一门、回答 question，再拒绝第二门并填理由，确保拒绝理由的真实引导消息不会被下一道 question 消费；harness 通过 `Manager.EventsFromAsc` 从本地 events 表读取并镜像至 ledger，发布 manifest 前的真实 `SessionDetail` 断言确认 executor `recent` 与 `ticket_refs` 均含当前 permission。其它可视角色的种子等级记录于 manifest。

## 红绿与验证证据

- F7 唯一承重变异已执行，不再重复：临时关闭远端 snapshot 新旧闸，测试可编译但 `TestSessionRoleStatusHTTPKeepsLifecycleTimeAcrossProgress` 失败，断言实际 executor=`running`、coordinator=`unknown`，而预期 `idle/waiting` 且保留原 since。原始 stdout：`/tmp/b456-f7-red.log`。立即恢复源码闸后同测试通过，stdout：`/tmp/b456-f7-green.log`（`ok .../internal/agentd 0.581s`）。主会话之后只做稳定实现 fresh green，不执行额外 mutation。
- F8 红绿承重入口：真实 SQLite mirror event→`open_ticket_projection` 写入 501 个 question ticket；真实 `/api/sessions/{id}` 返回 HTTP 500 并包含“超过本次读取上限”和“全局待处理入口”。`go test ./internal/agentd -run 'TestSessionRoleTicketLimitFailsClosedOverHTTP|TestSessionRoleStatusHTTPWire|TestSessionRoleStatusHTTPKeepsLifecycleTimeAcrossProgress' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 1.178s`。
- genuine producer two-gate harness smoke:`B456_BROWSER_HARNESS=1 B456_BROWSER_HARNESS_HOLD_SECONDS=1 B456_BROWSER_HARNESS_MANIFEST=/tmp/b456-session-status-smoke.json go test ./internal/agentd -run '^TestB456BrowserHarness$' -count=1 -v` → `PASS`。更早一轮 smoke 因断言等待 `completed`（manager 正确产出 `waiting_review`）失败，修正为生产状态后通过；不存在用户数据影响。
- browser harness 已再次启动并处于 600 秒 hold：manifest `/tmp/b456-session-status-manifest.json`，临时 URL/测试 token/session/task 见该 0600 manifest。该进程由本次 `go test` 启动，主会话可结束该测试进程以停止。
- `go test ./internal/collab -run 'TestApplySessionTask|TestTaskSnapshot|TestTaskMirrorRecent|TestSessionRole' -count=1` → 先前通过；`go test ./internal/ledger -run 'TestSessionRoleActivity' -count=1` → 先前通过；store/orchestration/ledger-api/proto/collab 定向用例及 `go build ./...` → 先前通过。`cmd/session_test.go` 的旧顶层键集测试已按批准加入 `role_status` 断言，`go test ./cmd -run '^TestSessionDetailThreeSections$' -count=1` → 通过。
- 本轮 harness/F8 之后将再执行 `go build ./...` 与相关定向测试；未运行全仓库复核或新 mutation。

## 未验证与边界

- 需要主会话在有效 hold 内完成浏览器真实页面验收；opt-in 现场不是生产环境。当前 P1 第一门 permission pending，后续 question 和第二门拒绝/理由均待页面交互；P2 completed、P3 wake failure 为 fixture，不能将全部卡片称作生产端到端。
- PostgreSQL 方言执行未验证；由主会话按计划做最终新鲜全量与 review。镜像状态不是进程心跳；缺少可靠源时间时不能声称实时。
