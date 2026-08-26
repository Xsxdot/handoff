# 台账：B156.2.8（C8 d_web·控制台房间面）plan 节点

> 本文件随 plan 节点边干边追加；与产出物 docs/superpowers/plans/b156.2.8-plan.md 同批提交。
> 分支：cards/B156.2.8-charter；HEAD=82b49cb2（C6+C7 已合入功能线，本地==远程已核）。

## 2026-08-26 现状基线复核

- L1 `git log --oneline -1` → 82b49cb2（merge C6+C7）。
- L2 `git merge-base --is-ancestor 82b49cb26 HEAD` → BASE_OK。
- L3 `git status --porcelain` → 空（工作树干净）。
- L4 `go run . graph check --repo . --view cards-B156.2-charter-4` → fails=[] EXIT=0，warns=96（warn 不作判据）。
- L5 `go run . graph validate --repo .` → EXIT=0。
- L6 `go run . graph domains --repo .` → 顶层域含 d_web（children: d_web_admin/cards/command/contract/shell/workbench）。
- L7 `go run . graph context --repo . d_web` → 领域声明缺失（codegraph/domains/d_web.json 不存在，warning 提示 roadmap 1a）；packages 列出 web/src 各目录。
- L8 亲读 `codegraph/best.json`：containers 260 个；`k_web_app_board/overlay/task → d_web_command`，`k_web_api_rooms(_model) → d_web_contract`（charter 轮已冻）。`d_web_command` 存在且为叶子域。
- L9 亲读 `codegraph/baseline.json`：containers 263 个（含 label/kind/domain 全量结构）；web 容器样例 `k_web_app_board` label="/app/board组件与函数" kind="React 组件/函数"；`k_web_app_board_model` label="/app/board 类型" kind="TypeScript 模型"。**web 容器节点数为 0**（前端不扫描成图节点）。
- L10 亲读 charter/graph@v0.8.0：`merge.go:84` ContainersAdded 合并入视图容器集；`absorb.go:35` 吸收；`best.go:127-133` best.json containers 校验「容器→叶子域引用」，不校验容器存在于图。**结论：C8 图动作 = best.json containers 两行 + 视图 diff containersAdded 两枚，零节点。**

## 2026-08-26 消费面事实（C6 路由与响应形状，亲读）

- L11 `internal/agentd/ledgerapi.go:49-54` 六行注册：GET /api/rooms、GET /api/rooms/{id}/messages、POST messages、POST read、GET /api/inbox、POST /api/cards/{id}/rebind。
- L12 `internal/agentd/roomsapi.go` 响应信封：list `{"rooms":[...]}`、messages `{"messages":[...]}`、send `{"seq":N}`、read `{"ok":true}`、inbox `{"items":[...]}`。actor 服务端注入 `web:<host>`，kind 服务端固定 user，POST body 只收 body/refs/mentions。
- L13 `roomsapi.go:25` collabErr 注释：History 的 ErrNoRoom→404 特例由 handleRoomMessages 自处理，但 C4 合规 History 对不存在房间返回 200 空列表——前端按「不存在→200 空列表」设计，不指望 404。
- L14 `internal/proto/rooms.go` RoomMessage/InboxItem/RoomSummary wire 字段亲读；RoomSummary 无 LastMessage、无 carrier。**carrier 在 proto/agentd/collab 的 wire 面零字段**（grep 全仓非测试：driver_carrier 只在 ledger Store SQL 与 CLI rebind）。

## 2026-08-26 前端现状（亲读）

- L15 `web/src/api/rooms.ts` 63 行：类型镜像全量冻结（RoomMsgKind/RoomMessage/InboxItem/RoomSummary/RoomHistoryItem），**无 fetch 函数**（文件尾注释「fetch 函数随实现节点接线」）。
- L16 `web/src/api/testdata/RoomsFixture.json` 5 case：escalation-full / user-minimal / inbox-decision / inbox-ticket / inbox-mention。**无 RoomSummary 用例**（金样本缺口，C8 fetch 测试用内联样本补齐并记录）。
- L17 `web/src/api/ledger.ts:304-305` `answerDecision(id:number, answer:string)` → POST /api/decisions/{id}/answer。既有答复通道，收件箱复用。
- L18 `web/src/app/data/usePoll.ts` 签名 `usePoll<T>(fetcher, intervalMs, opts?)`；`usePoll.test.ts` 用 vi.useFakeTimers + advanceTimersByTimeAsync。
- L19 页面先例：CardsPage.tsx `const POLL_MS = 2500` 页内常量 + `usePoll(() => fetchCards(...), POLL_MS)` + 项目 `<select>` 客户端筛选；CardsPage.test.tsx 用 `vi.mock('../../api/ledger')` + MemoryRouter。
- L20 测试 fetch 桩先例：`client.test.ts:21-41` `vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(...)))`。
- L21 `web/src/app/shell/Shell.tsx`：ledgerEnabled 门控 `/cards`、`/flows`（:481-486）；`fullPageRoute` 数组 :391；ProjectTree props 传 `onOpenCards`→`navigate('/cards')`（:454）。
- L22 `web/src/app/tree/ProjectTree.tsx` 底部 dock 段 :757-836：ledgerEnabled 门控的图标按钮（工作项/流程）+ 常驻（看板/工单/代码图/设置）；lucide-react 图标。
- L23 lucide-react ^1.31.0：仓库已用图标集合 = {Activity AlertTriangle ArrowLeft ArrowUpRight Bot Check ChevronDown ChevronRight Columns Eye MessageSquareText Plus Search SlidersHorizontal TerminalSquare X}（grep 实采）。**收件箱/会话按钮只挑已证明存在的图标**：MessageSquareText（会话）、AlertTriangle（收件箱）。
- L24 `web/.gitignore` 忽略 node_modules；本工作树 web/node_modules 不存在——实现轮先 `npm ci`（web/）再跑 tsc/vitest。
- L25 `web/src/app/lib/format.ts` 有 `formatRelative(iso, now)`（RFC3339 → 相对时间）。
- L26 布局陷阱成文（plan 分栏依据）：jsdom 看不见布局——组件断言只锁数据流/交互语义，布局进真机清单。

## 2026-08-26 关键裁决（填补级，plan 内可见）

- D1 carrier 无 wire 字段（L14）：澄清一「房间面原样展示」的展示面落在 `RoomSummary.bound_session`（不透明标识原样展示、零解析）；carrier 本体本期无展示数据源，非 C8 可加字段（改形状回 contract）。计划按 bound_session 不透明展示落地，缺口写入 notes。
- D2 RoomSummary 不在金样本（L16）：rooms.fetch.test.ts 用内联 RoomSummary 样本（含 last_activity RFC3339 串直通断言），不动冻结的 RoomsFixture.json。
- D3 会话列表项目筛选用客户端过滤（CardsPage 同构，单流全量轮询）：fetchRooms(project) 参数仍在 fetch 层测试（端点契约），页面选择客户端过滤避免双流。
- D4 只读房禁写守卫在 handler 内（`if (summary?.read_only) return`），发送按钮不因只读 disabled——已知陷阱二要求「真触发提交/点击再断言无 POST fetch」，disabled 会让点击到不了 handler、断言空转；输入框 disabled 仅作 UX。
- D5 房间页历史：`usePoll` 只轮询最新一页（limit=200），「更早」按钮按 before=最老seq-1 手动叠加；打开房间即已读 = 到新 maxSeq 置一次 `markRoomRead(id, maxSeq)`。
- D6 A.6 轮询常量：`COLLAB_POLL_MS = 5000` 落 `web/src/app/rooms/constants.ts`，三页面共用；断言经 mock usePoll 检查 interval 参数 == 常量。
- D7 图登记（L10）：`codegraph/best.json` containers 加 `k_web_app_rooms`/`k_web_app_rooms_model → d_web_command`；`codegraph/diffs/cards-B156.2-charter-4.json` containersAdded 加两枚（沿用既有视图文件，不另起）。

## 未验证（留给实现轮/真机）

- 本工作树无 web/node_modules，`npx tsc -b` / vitest 未实跑（L24）——实现轮基线步骤含 `npm ci`。
- 布局/滚动/dock 可达 → 真机清单。