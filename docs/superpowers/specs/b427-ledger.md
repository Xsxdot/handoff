# B427 台账

阶段状态：**代码与自动化通过，真实手机性能待验**。被测源码 `77292b7e`；独立复审见 [b427-review.md](b427-review.md)。未部署或合并。

## 2026-10-07 用户范围与授权

- 用户最初指出：桌面端速度快，移动端工作项慢，会话快。
- 用户更正：列表与详情「两个都慢，大概要两秒+的时间才能加载出来」。这为用户观察，当前没有真实手机请求/渲染分解证据。
- 协调者提出四项：详情不拉全历史；复用已有列表数据消除重复首拉/轮询；工作流版本独立保存结果避免单个 404 导致整批重取；手机复测列表、详情与返回，若仍慢再依据证据优化 SQL。
- 用户回复「OK，开始吧」。spec r1 标为 2026-10-07 已批准，范围仅上述前端修复与验收方向；尚未授权 SQL 或新增端点。

## 2026-10-07 协调者前置排查（转录，非文档执行者亲跑）

- 本机接口样本：HTTP 200，当前列表约 20 张/11KB，`all=1` 约 433 张/262KB；接口约 0.1–0.3 秒。读数用于证明响应规模与本机耗时，不作为手机两秒问题已复现/已修复证据。
- 工作流历史版本样本有 3 个旧版本返回 404；现有整批 `Promise.all` 会令成功项不落缓存，后续卡轮询继续重取。
- SuperDev 57017 不可达；真实手机网络与渲染耗时尚未复现。受管入口验证不足不能声明手机性能通过。
- 图查询：`Store.ListCards`、`CardsPage`、`usePoll`、`CardDrawer` 命中；`flow CardsPage` 为 degraded。精确 `listCards`、`SessionSidebar`、`loadSessions`、`useMobileNav` 未命中，属于本次图覆盖债。
- 新鲜环境探测（协调者）：`adb devices -l` 成功、列表为空；SuperDev `list_services(handoff)` 重试仍 `57017 connect refused`。真实手机路径保留未验证；不请求用户改变判据，也不阻塞可继续完成的代码/构建/独立审阅工作。
- 基线测试（协调者）：`npm test -- src/app/cards/CardsPage.test.tsx src/app/cards/CardDrawer.test.tsx src/app/shell/Shell.test.tsx` → `2 files pass / 1 fail`、`198 passed / 1 failed (199)`、`9.38s`。失败为 `Shell.test.tsx:1609` 移动项目工作项导航用例仍期望列表主面项目 combobox，B426 S4 已将筛选移至详情；实现阶段按已批准形态更新测试入口并保留实际过滤断言，不回退生产形态。
- 主 checkout 的 B426 正在施工；当前 B427 独立分支使用已提交 S1/S3/S4 基线，只改隔离工作树，不接触主 checkout 未提交修改。

## 2026-10-07 spec/plan 落盘核对（文档执行者亲跑）

- `pwd` → `/Users/xushixin/.codex/worktrees/4d8b/handoff`；`git branch --show-current` → `codex/b427-performance`；`git rev-parse HEAD` → `babd141ef5779ca17272cd184d888cbf6546260b`。
- 阅读 spec/plan 节点 skill；本任务只拥有三份 B427 文档，不改代码、不提交、不操作 handoff 账本。
- `web/package.json`：`typecheck=tsc -b`、`test=vitest run`、`build=tsc -b && vite build`。只核实脚本，未运行测试或构建。
- 基线源码核对：`CardsPage.tsx:162` 的历史范围条件含 `cardDeepLink !== ''`；`:201` 在 `includeArchived/cardDeepLink` 变化刷新；`:296` 使用整批 `Promise.all`；`:309` 只在列表含目标 ID 时恢复深链。
- 基线源码核对：`Shell.tsx:131` 已有 tasks，`:218-219` 已有 cards/decisions，`:1183` 条件挂载移动 CardsPage；`useMobileNav.ts:255-277` 的 openCard 写 `/cards?card`；`CardDrawer.tsx:350-364` 已有单卡详情请求与错误状态。
- 阅读 B426 台账确认 S1/S3/S4 已变更移动筛选、看板列映射与抽屉/队列形态；B427 保留该基线，不以旧原型词表回退。

## 后续证据

实现、独立 review、全量 Web 检查与真实手机验收尚未发生；各节点获得结果后追加，不预写通过结论。

## 2026-10-07 实现节点：B427 前端数据消费（实现者亲跑）

- 用户阶段指令（协调者转达）：现在不方便连接手机，先完成代码和自动化验证。真机性能仍 pending，不以模拟视口或本机接口替代。
- 文档提交历史（协调者转达）：`git add` 三份 B427 文档后 `git commit -m 'docs(B427): record approved frontend performance fix and plan'` → `[codex/b427-performance 959973fb]`、`3 files changed, 141 insertions`、三份 create。该历史读数不追最终 HEAD。
- 实现前复核：`CardsPage` 的卡 URL 参数扩张 `all=1`、初挂额外 refresh、独立三条卡/决策/任务流，以及钉版本整批 `Promise.all` 均在批准基线存在。
- Task 1 有牙首红：`npm test -- src/app/cards/CardsPage.performance.test.tsx`（`/private/tmp/b427-card-red.log`，退出 1）中 mobile click/URL/close 的 `fetchCards` 断言 expected 1 / got 3；终态卡与不存在卡因未进入现役列表而不能打开 Drawer，分别出现 `Unable to find role="dialog"` 与 `card missing` 文本缺失。
- 首版 workflow 测试夹具问题：节点 chip 在无钉版本时也回落相同状态文本，不能证明成功缓存；另在 mount 后切 fake timers 未覆盖既有 real interval，network retry expected 2 / got 1 不算功能红。均纠正，不据此下生产结论。
- Task 3 有牙首红：改为 mount 前 fake timers、推进真实卡轮询，`/private/tmp/b427-card-red2.log`（退出 1）工作流请求 expected 2 / got 4。network retry 在正确计时器下基线已绿，作为既有语义回归保留。最终成功消费断言在真实 Drawer 的「转移状态」option 读取钉版本独有目标态，不借 chip fallback 宣称缓存成功。
- Task 2 有牙首红：`npm test -- src/app/shell/Shell.test.tsx -t 'B427 mobile'`（`/private/tmp/b427-shell-red.log`，退出 1）真实 Shell → CardsPage 首次切入工作项，卡请求 expected 1 / got 3。只 mock API 边界，没有 mock 掉生产 CardsPage。
- 最小实现：`card` URL 只选择目标，不改变历史范围；移除初挂额外 refresh、显式历史范围变化仍即时 refresh。终态卡由现有 Drawer 单卡详情恢复，成功回调共享 metadata，不增加父详情请求；Drawer request generation 与按 ID 的组件 key 防止旧 ID/关闭后的请求污染新目标。URL 从带 card 返回不带 card 时关闭 Drawer，桌面普通本地选择不受空 URL 参数清理。
- 最小实现：移动 Shell 把现有 cards/decisions/tasks 的完整 PollState 注入 CardsPage；local 卡/决策 hooks 使用既有 enabled 停止被取代的轮询，useTasks 增加可选 enabled（缺省仍启用）。不新增全局缓存、存储、端点或 SQL。独立工作项页面仍自行加载。
- 最小实现：钉版本请求各项独立结算，成功与 404 在当前页面数据所有者 lifetime 内记忆，401 按终止语义单独保留，网络/服务器故障下一卡快照可重试；pending 去重避免首批并发结果落盘间重取。Drawer 与列表共用钉版本定义，缺失时不猜最新版；Shell/standalone 所有者切换与卸载 guard 拦旧响应。当前 API 均为同源且无 target 输入，不建泛化 sourceKey。
- Task 1 阶段绿：`npm test -- src/app/cards/CardsPage.performance.test.tsx -t 'bounded detail'`（`/private/tmp/b427-card-task1-green.log`，退出 0）→ 4 passed / 2 skipped。Shell 阶段绿：`-t 'B427 mobile'`（`/private/tmp/b427-shell-green.log`，退出 0）→ 1 passed / 112 skipped。
- B426 继承漂移按批准计划更新：项目工作项入口先进入真实 Drawer 后读项目 combobox，再改为 other 项目、关闭 Drawer，断言其他项目卡出现且原 handoff 卡被过滤；未删除项目过滤行为断言。
- 触及迭代中的夹具修正：完整 Shell 文件 mock 调用累积需本组 `vi.clearAllMocks()`；其全局 afterEach 清除 setup 的 jest timer shim，fake timer 用例在组内补该已存在 shim；Drawer dialog 初挂仍 loading，钉版本 option 断言等待真实 action 按钮；初始 loading 用例等待 ledger health 门启用后该次 Shell 卡请求。它们分别造成 timeout、累计请求计数、loading 按钮缺失与 0 请求读数，不算新生产缺陷。
- 既有会话双跳的边界事实：`npm test -- src/app/shell/Shell.test.tsx -t 'B369.10 卡到会话双跳'`（`/private/tmp/b427-shell-session-isolated.log`，退出 1）在会话首拉未提交时点「驾驶会话」返回 `shell.session.for_card_missing { cardId: 'B1' }`，没有跳转。直接按 ID 恢复使 Drawer 可先于 ledger 健康门后的会话快照呈现。正常双跳测试现在等待本次真实会话流请求/提交后点击，只证明 ready 态；未证明 loading 时点击可自动等待，此边界未修、留独立 review 裁决，不能仅据等待后通过声称整个边界已验证。
- 新故事扩展绿：`npm test -- src/app/cards/CardsPage.performance.test.tsx src/app/shell/Shell.test.tsx -t B427`（`/private/tmp/b427-expanded3.log`，退出 0）→ 2 files passed、14 passed / 112 skipped。覆盖单次卡首拉、移动点卡/URL/关闭、终态/404深链、URL历史返回、迟到旧ID、桌面普通点击/显式历史开关、404不重取/成功Drawer状态、网络重试、workflow401终止、所有者切换迟到、Shell首次/返回单流、未知首拉、断线恢复与cards401留快照/停表。
- 全量类型检查第一次 `npm run typecheck`（`/private/tmp/b427-typecheck.log`，退出 2）发现本任务 fixture 漏 unlinked 与 Card/CardView union；随后修正。本轮 `npm run typecheck -- --force`（`/private/tmp/b427-typecheck-force.log`，退出 2）只剩继承 rooms 的 14 条诊断：SessionChat fixtures 缺 historyExpired、SessionSidebar 缺 expired 类型声明、SessionTab test 重复 ApiError import。协调者独立核实并在 `b9e8e721` 最小修正，未由实现者扩改 rooms。
- 收尾触及绿：`npm test -- src/app/cards/CardsPage.performance.test.tsx src/app/cards/CardsPage.test.tsx src/app/cards/CardDrawer.test.tsx src/app/shell/Shell.test.tsx src/app/data/usePoll.test.ts`（`/private/tmp/b427-touched4.log`，退出 0）→ **5 files passed / 225 tests passed**。该结果包含 B426 现有形态、项目过滤与 ready 态双跳回归。
- 没有运行服务启动、部署、handoff 命令或真机复测；实现者没有 commit。全量 Web test/build、独立 review/变异与卡账本交协调者。手机性能结论仍未验证。
- 实现者收尾全量编译：`npm run typecheck -- --force`（`/private/tmp/b427-typecheck-final.log`，退出 0）→ `tsc -b --force` 无诊断；`git diff --check` 退出 0。该次是在继承 rooms 修正后、最终 touched 用例之后亲跑。
- 协调者收尾（协调者转达亲跑结果）：全量 Web 测试 **141 files / 1731 tests passed、22.55s**；force typecheck 与 `npm run build` 退出 0，Vite 构建 1996 modules；`codegraph check --base babd141e` 退出 0、`fails=[]`，继承 warnings 保留。全量日志由协调者持有（`/private/tmp/b427-full-web.log`）。独立 review 尚待进行，以上不是最终手机验收。

## 2026-10-07 协调者新鲜复跑与变异

- 用户明确回复「现在不方便，先完成代码和自动化验证」，同意先交代码/自动化阶段；真机仍是本卡最终验收欠项，不将其转为模拟器验收。
- 继承编译问题以 `b9e8e721` 独立修正；SessionSidebar 补现有 expired 可选类型、旧 SessionChat 8个 fixtures 补 false、SessionTab 重复 import 删除。3个 rooms 文件亲跑 52/52 通过。
- `npm test`（`/private/tmp/b427-full-web.log`，exit 0）：141 files passed，1731 tests passed，22.55s。已有 jsdom Canvas 提示不造成失败。
- `npm run typecheck -- --force`（exit 0），`npm run build`（`/private/tmp/b427-build.log`，exit 0）：1996 modules transformed，built in 2.25s；既有大 chunk 提示保留。
- `codegraph check --base babd141e`（exit 0）：fails=[]，继承 warns 保留；本次没有重扫图，不能据此宣称新增前端符号已入图。
- 承重变异唯一命中 `fetchCards(includeArchived ? 'all=1' : '')`，恢复旧 cardDeepLink 扩历史条件，强制编译通过。第一轮行为2红8绿揭示移动点卡测试只检查当刻；补上真实2.5秒轮询断言后再次变异（`/private/tmp/b427-mutation2-*.log`）：compile exit0，behavior exit1/3 failed+7 passed（移动点击后轮询、终态深链、不存在ID），finally按原字节恢复后 exit0/10 passed。通过生产 CardsPage→fetchCards 接缝，非字符串锁。
- 全量1731通过发生在补2.5秒测试断言前，生产源码此后未改；该测试文件补强后10/10新鲜复跑通过。独立 review 交棒后若有生产修正，须重跑全量。

## 2026-10-07 独立 review R1/R2 回实现（实现者亲跑）

- 协调者将 R1/R2 持久回账并重回 implement（R1 seq22755、R2 seq22757），授权本轮最小修复，不授权提交/部署。前文“会话首拉未完成点击”未修边界由此进入本轮正式修复。
- R1 首红：新增真实 Shell → CardsPage → Drawer 测试，让 `fetchSessions` 首拉保持 deferred、单卡详情先成功；`npm test -- src/app/shell/Shell.test.tsx -t 'B427 driver session readiness'`（`/private/tmp/b427-r1-red.log`，退出 1）→ `expect(element).toBeDisabled()` 失败，实际驾驶会话按钮 enabled，1 failed / 116 skipped。它直接证明此前快详情与未知会话查找的竞态，不用人为等待隐藏问题。
- R1 最小实现：Shell 用既有 `sessionsState.data !== null && !sessionsState.sessionExpired` 派生 ready，经 CardsPage 可选 bool seam 传给 Drawer（缺省 true 保留独立组件调用兼容）；行保留但 ready=false 时 disabled 且显示「驾驶会话（尚未就绪）」。Shell callback 也防御未就绪，日志分别记录 not_ready 与已知 missing。没有新增数据请求、计时器、排队或状态。已有断线快照允许沿既有语义导航；首拉网络错误与 401 无快照均维持“尚未就绪”，不误称正常 loading。
- R1 绿验证：deferred 期间点击不能调用 missing 路径或改变 URL；首拉成功后同一按钮 enabled，真实进入会话工作台并保留既有 `from=card-B1` 返回上下文，返回仍打开 B1 详情。另覆盖首拉网络失败/401 时行可见、disabled、点击不导航。
- R2 首红：新增真实 Shell 的 cards 首拉401与已成功快照后401两态 DOM 断言；`npm test -- src/app/shell/Shell.test.tsx -t 'B427 shared expiry presentation'`（`/private/tmp/b427-r2-red.log`，退出 1）→ 两条均 `Unable to find ... 会话已失效，请重新打开控制台`，2 failed / 117 skipped。
- R2 最小实现：CardsPage 仅从既有 cards/decisions/tasks 的 `sessionExpired` 合成渲染判据，复用 `SessionExpiredBanner`。无数据过期时不再显示“正在读取账本”或空列表；有数据时保留快照并明确显示“保留上次获取的数据，当前状态尚未确认。”不改变 usePoll 的401停表/恢复纪律，不新增认证状态。
- R1/R2 主红→绿：同一 Shell 文件按 `-t 'B427 driver session readiness|B427 shared expiry presentation'`（`/private/tmp/b427-r1r2-green.log`，退出 0）→ 3 passed / 116 skipped。扩展真实消费（含 standalone cards 首拉401、注入 decisions/tasks 各401、首拉会话网络/401与原有单流/恢复）：`npm test -- src/app/cards/CardsPage.performance.test.tsx src/app/shell/Shell.test.tsx -t B427`（`/private/tmp/b427-r1r2-expanded-green.log`，退出 0）→ 2 files passed、22 passed / 112 skipped。
- M1 测试隔离首红：`npm test -- src/app/cards/CardsPage.test.tsx -t B426`（`/private/tmp/b427-m1-red.log`，退出 1）→ 5 failed / 4 passed / 31 skipped。根因是 B426 的 `mockCharter` 未给 `fetchFlow` Promise、`renderCompactWithCards` 未给 `fetchCardDetail` Promise，依赖前序用例残留 mock。只补 helper 的准确 name/version 或 ID 对应 fixtures，未泛化测试框架/改生产。
- M1 隔离绿：同一命令（`/private/tmp/b427-m1-green.log`，退出 0）→ **9 passed / 31 skipped**。`git diff --check` 退出 0。
- 回实现收尾触及测试：`npm test -- src/app/cards/CardsPage.performance.test.tsx src/app/cards/CardsPage.test.tsx src/app/cards/CardDrawer.test.tsx src/app/shell/Shell.test.tsx src/app/data/usePoll.test.ts`（`/private/tmp/b427-r1r2-touched-final.log`，退出 0）→ **5 files passed / 233 tests passed**。本轮无 commit/handoff/服务启动/部署；停止文件写入交协调者执行全量编译、测试、构建、承重变异及独立复审。真实手机性能仍 pending。

## 2026-10-07 协调者 R1/R2 修复后全量复跑

- 最终生产源码与测试亲跑 `npm test`（`/private/tmp/b427-final-full-web.log`，exit0）：**141 files passed，1739 tests passed，26.68s**。
- `npm run typecheck -- --force`（`/private/tmp/b427-final-typecheck.log`，exit0），`npm run build`（`/private/tmp/b427-final-build.log`，exit0）：1996 modules，built in2.29s，既有大chunk提示保留。
- `codegraph check --base babd141e`（`/private/tmp/b427-final-graph.log`，exit0）：fails=[]、28条继承warns；未重扫，图覆盖债不核销。
- 另做真实 Shell 共享流承重变异：Shell 唯一 sharedData 注入换为 undefined（不删字段引用、不造编译红）；force compile exit0，`Shell.test.tsx -t 'B427 mobile shared ledger consumption'` exit1：首次切入卡请求 expected1/got2。finally恢复原字节后同一DOM测试 exit0/1 passed。日志 `/private/tmp/b427-shared-mutation-*.log`；覆盖实际 Shell→CardsPage 生产组装接缝。
- 上述最终全量对应修复后源码；后续变异已按原字节恢复，没有带入代码变化。独立复审待新提交核销 R1/R2/M1。真实手机仍未验，未部署/合并。

## 2026-10-07 独立复审与阶段交接

- 独立新上下文审阅者针对77292b7e复审，目标/架构证据双轴通过，R1/R2/M1核销，无新Critical/Important。审阅者亲跑B427+既有ready双跳23/23、B426隔离9/9，核读协调者1739全量与共享变异。完整维度表与核销指针持久保留b427-review.md。
- 本期代码/自动化阶段交接完成，遵从用户先完成代码与自动化的明确安排。卡进入acceptance并整体记未验，唯一剩余必交为同一Android手机/服务入口实测列表首次、点卡、返回再进入的请求范围/数量、网络等待与渲染时间；不声称两秒已解决。
- 保留codex/b427-performance本地分支与独立worktree，未部署/推送/合并，不修改主checkout B426改动；后续真实手机验收不得使用模拟视口或本机API时间替代。
