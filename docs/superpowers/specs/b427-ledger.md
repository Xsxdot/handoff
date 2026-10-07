# B427 台账

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
