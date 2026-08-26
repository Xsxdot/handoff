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
## 2026-08-26 实现轮 T8.0 基线

- M1 `npm ci`（web/）→ EXIT=0，0 vulnerabilities。
- M2 `npx tsc -b` → EXIT=0。
- M3 `npx vitest run src/api/rooms.test.ts` → 1 passed / 5 tests，EXIT=0。

## 2026-08-26 实现轮 T8.1 fetch 五端点 + 常量

- M4 写失败测试 `rooms.fetch.test.ts`（plan 代码块 A）→ 首红 7/7 `TypeError: fetchX is not a function`（编译红，符号缺席，非 typo）。
- M5 空壳落地（五函数全返回空/恒值）→ 断言红 7/7（URL/body/解码断言全有牙，非编译红）。
- M6 全量实现 + `constants.ts` → 红 1 残留：`Body has already been read`——plan 代码块 A 的 `mockResolvedValue(jsonResp(...))` 两次调用返回同一 Response，body 只能读一次。**plan 测试夹具机械缺陷**：改为 `mockImplementation(() => Promise.resolve(jsonResp(...)))`（每次新 Response）。断言意图不变。
- M7 绿：`npx vitest run src/api/rooms.fetch.test.ts src/api/rooms.test.ts` → 12 passed（EXIT=0）；`npx tsc -b` → EXIT=0。
- M8 变异自验（fetchRooms project 查询串）：变体①删 project 参数 → TS6133 unused 编译失败，**该发不算数**；变体②恒带 `?project=` → TSC EXIT=0 + vitest 1 红（`expected '/api/rooms?project=' to be '/api/rooms'`，AssertionError）→ 还原 → 12 passed。测试有牙。

## 2026-08-26 实现轮 T8.4 图登记（岔口九）

- M9 T8.4 基线图闸（改动前）：`go run . graph check --repo . --view cards-B156.2-charter-4` → EXIT=0，`"fails": []`，`bestCoverage` = `assignedContainers:258 viewContainers:258 crossDomainEdges:1039 misplacedSkipped:114`（warns 数为 96 不作判据）；`go run . graph validate --repo .` → EXIT=0。
- M10 **现状域比对（判据二要求落地前比对一次）**：baseline.json 全部 web 容器 `domain` = `d_web`（含同批先例 `k_web_app_board`/`k_web_app_board_model`/`k_web_app_task`/`k_web_app_overlay` 全部 `d_web`）；`k_web_app_rooms`/`k_web_app_rooms_model` 在 baseline 不存在（新增容器）。
- M11 best.json `containers` 加两键：`k_web_app_rooms: d_web_command`、`k_web_app_rooms_model: d_web_command`（应然域，与同批 `k_web_app_board` 等 best 归属一致）。
- M12 **视图 diff containersAdded 的 domain 填现状域 `d_web`**（不是 plan 代码块 P 写的应然域 `d_web_command`）——判据二硬约束：填应然域会静默消掉一条 misplaced 债。gap.go `containerAlignment` 已读源码核实（chart/graph@v0.8.0 codegraph/gap.go:32-48）：视图域不在 best 词表 → alignSkipped；在词表但与 best 归属不同 → alignMisplaced warn。新增 web 容器无节点（前端不扫描，L9），`hasLiveNode` 为 false，`bestGapFindings` 直接 continue，既不产生 warn 也不影响 misplacedSkipped 计数——但 domain 值本身决定视图语境，填 `d_web` 保持与 baseline 同批容器一致的现状域口径。
- M13 改后图闸（--view cards-B156.2-charter-4）：`graph check` → EXIT=0 `"fails": []` warns=98（较基线 96 增 2 条 best-dangling：`k_web_app_rooms`、`k_web_app_rooms_model`——新容器无节点，informational 非 fails）；`bestCoverage` = `assignedContainers:258 viewContainers:258 crossDomainEdges:1039 misplacedSkipped:114`（与改动前一致）；`graph validate` → EXIT=0。
- M14 计数自检：best.json `k_web_app_rooms`/`k_web_app_rooms_model` == `d_web_command d_web_command`；diff `containersAdded` 含两键且 domain=`d_web`（现状域），label/kind 与 baseline 同批 `k_web_app_board` 惯例一致（`/app/rooms组件与函数` React 组件/函数；`/app/rooms 类型` TypeScript 模型）。
- M15 **plan 事实勘误**：plan L9/D7 声称「web 容器图节点数为 0（前端不扫描成节点），本卡零节点登记」——**与实跑不符**：baseline.json 实际有 715 个 web 节点（`k_web_app_board` 21、`k_web_app_cards` 29、`k_web_app_task` 55、`k_web_app_shell` 6 等，均带真实 file/line）。charter-2 视图 diff 也登记过 web 模型节点（`m_web_api_rooms_RoomMessage/InboxItem/RoomSummary`）。本卡按 plan 与协调者本轮 scope（仅容器归属登记）落地，`k_web_app_rooms(_model)` 无节点→ 2 条 best-dangling warn；rooms 页面/fetch 节点登记留待后续重扫或专门卡，记 notes。

## 2026-08-26 实现轮 T8.5 收尾三档读数（判据八）

- M16 三档读数（web/ 下，先 `npm ci` EXIT=0 0 vulnerabilities）：
  - `npx tsc -b` → EXIT=0（/tmp/t84_tsc.log）。
  - `npx vitest run`（web 全量，无路径参数）→ EXIT=0，`Test Files  113 passed (113)` / `Tests  1139 passed (1139)`（/tmp/t84_vitest_full.log）。
  - `npm run lint`（eslint .）→ EXIT=1，`✖ 19 problems (1 error, 18 warnings)`。**1 error 为本卡范围外基线既有红**：`web/src/app/flows/NodeEditor.test.tsx:50` `'view' is never reassigned. Use 'const' instead (prefer-const)`——该文件最后改动 commit `e8f94b76`（feat charter），本卡（B156.2.8）未触碰此文件（`git log d897323a~1..HEAD -- src/app/flows/NodeEditor.test.tsx` 为空）。18 warnings 均为既有（react-refresh/only-export-components、react-hooks/exhaustive-deps 等），无本卡引入。按判据八不修范围外红。
  - 本卡文件（rooms 页面/测试、constants.ts、rooms.ts、Shell.tsx、ProjectTree.tsx）在 eslint 输出中零 error；Shell.tsx:201、ProjectTree.tsx fast-refresh 为既有 warning（git blame 属前序 commit，非本卡引入）。

## 2026-08-26 charter-5 轮：T8.4 容器节点补登记 + 空容器拆除（协调者本轮指令）

- M17 **协调者基线复跑（改动前，cards/B156.2.8-charter-5 @ 2003b4e7）**：`go run . graph check --repo . --view cards-B156.2-charter-4` → EXIT=0 fails=[]，warns=98（anchor-off-domain 2 / best-dangling 4 / container-misplaced 51 / legacy 34 / oversized-package 2 / prefix-family 5）；best-dangling 4 条 = `k_collab_model` / `k_web_api_rooms` / `k_web_app_rooms` / `k_web_app_rooms_model`（后两条为本卡新增，即 M13 记的 96→98）。`graph validate` → EXIT=0。
- M18 **协调者事实复验**：`grep -rn "export function\|export const" web/src/app/rooms/*.tsx web/src/app/rooms/*.ts` → 恰 4 个导出符号：RoomsListPage(tsx:53) / RoomDetailPage(tsx:52) / InboxPage(tsx:68) / COLLAB_POLL_MS(constants.ts:4)。`grep -nE "^(export )?(interface|type) "` rooms 目录 → 空（EXIT=1），**rooms 确实零类型声明**，与协调者①「k_web_app_rooms_model 结构上恒空」一致。baseline 复核：`k_web_app_*` 486 节点 kind 只有 func(364)/model(122)，const/var 0 个；web model 非 export 签名 27 个（导出与否不是过滤条件）——「无节点」判定成立。
- M19 **改动**：best.json 删 `k_web_app_rooms_model`（保留 `k_web_app_rooms: d_web_command`）；视图 diff `containersAdded` 删 `k_web_app_rooms_model`；`nodesAdded` 补 3 个 func 节点（容器 `k_web_app_rooms`，id 前缀 n_，order=行号，签名/行号亲读源文件）：`n_web_app_rooms_RoomsListPage`(53)、`n_web_app_rooms_RoomDetailPage`(52)、`n_web_app_rooms_InboxPage`(68)。COLLAB_POLL_MS 常量按「k_web_app_* 无 const/var 节点」先例不建模。
- M20 **改后图闸**：`graph check --view cards-B156.2-charter-4` → EXIT=0 fails=[]，**warns=97**（anchor-off-domain 2 / best-dangling **2** / container-misplaced **52** / legacy 34 / oversized-package 2 / prefix-family 5）。best-dangling 剩 `k_collab_model`/`k_web_api_rooms`（本卡外既有债，与协调者预期 4→2 一致）。container-misplaced 51→52：`k_web_app_rooms` 补上活节点后进入 `containerAlignment`（gap.go:81），视图域 d_web vs best 应然域 d_web_command → alignMisplaced，与其同批兄弟 `k_web_app_board/cards/task/shell/overlay` 同一族（现状域 vs 应然域债，判据二不填应然域故必现）。bestCoverage = assignedContainers:259 viewContainers:259（258→259：k_web_app_rooms 成为活容器被计入）crossDomainEdges:1039 misplacedSkipped:114。`graph validate` → EXIT=0 issues=null。
