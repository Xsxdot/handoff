# B413 实现计划：usePoll 轮询续帧 transition 化——修下钻 transition 饿死

- **卡**：B413（高优 bug，L2）· 分支 `fix/B413-poll-transition-starve` · 基线 origin/main `a86e6c35`
- **spec**：`docs/superpowers/specs/b413.md`（已批准 2026-09-28，方案冻结）；台账 `docs/superpowers/specs/b413-ledger.md`
- **读者假设**：零上下文 implement 执行者。输入只需三样：本计划、spec、两个目标文件（§4 白名单）。
- **本计划版本对应的现状**：worktree HEAD `cbcf12ba`（spec 定稿提交）下核实的读数，行号以此为准。

## 0. 目标故事与执行者范围

**S1（spec §4，唯一故事）一句话**：任何 390×844（compact）下钻路径在存在慢轮询源（离线机器拖慢 `/api/pty/sessions?scope=all` 到 ~3s）时，点击下钻后 URL 前进且 React transition 必须提交（mobile-home 卸载、detail-bar 出现、UI 跟随），全程不得依赖整页刷新恢复；桌面（≥1024）路径不回归。

**范围声明**：执行者交付「T1–T3 全绿、可走查的分支」。**真机 S1 走查（spec §4 的主走/补走/桌面反例三段）归协调者/用户**——执行者不做真机走查、不在 S1 判据上签 pass，只保证 §6 指引所需要素就绪。jsdom 复现不了并发渲染让出窗口（spec §7 已声明），单测证据是回归锁不是 S1 验收。

## 1. 冻结项（spec 已拍板，不得翻案）

1. usePoll 轮询续帧（`await request` 之后）的**全部**状态写入包 `React.startTransition`（非紧急数据语义）；不做逐字段紧急性分类。
2. 用户主动 `refresh()`（`setNonce`，事件处理器上下文）**保持 sync**，不随续帧降级。
3. `PollState` 公开签名、轮询节奏参数、三条实时性纪律（document.hidden 停表、断线保留最后数据、401 落终止态不再重试）**零改动**。
4. 消费方组件零改动；router（App.tsx BrowserRouter）与 agentd 零改动；不升级 react-router。
5. **降级口子（备选，不默认做）**：若真机验收发现终止态横幅（断线/会话失效）transition 化后有感知问题（如闪烁、迟滞不可接受），仅将 401/断线两处写入恢复 sync——启用此口子必须回协调者裁决（§7），执行者不得自行启用。

## 2. 已核实的事实底座（行号基于 HEAD `cbcf12ba`）

- **续帧写入点**（`web/src/app/data/usePoll.ts`）：
  - `:85-86` 成功路径：`setData(v)` + `setDisconnected(false)`（同一续帧，需同帧一致提交）；
  - `:92` 401 终止态：`setSessionExpired(true)`（`:91` 的 `stopTimer()` 是定时器控制、非 React 状态写入，**留在 transition 外**）；
  - `:96-97` 断线路径：`setDisconnected(true)` + `setErrorText(errorMessage(err))`；
  - `:52` `refresh` 的 `setNonce`——事件处理器上下文，保持 sync（冻结项 2）；
  - ref 写入（`:80-81`、`:99`）非 React 状态，不涉及 lane，不动。
- **测试环境（已实跑验证）**：worktree web 需先 `npm ci`（本计划核实时空树已装齐）；`npx vitest run src/app/data/usePoll.test.ts` 既有 8 条全绿；全量基线 `npx vitest run` **140 文件 / 1701 用例全绿**（~25s）；`npm run typecheck`（tsc -b）绿。node v23.11.0 对 web engines 要求报 EBADENGINE warning，非阻塞。
- **回归锁断言形态已探针验证**（临时测试文件实跑后删除，未留任何文件）：`vi.mock('react')` 捕获式透传在 React 19.1 + vitest 4.1.10 + RTL 16.3.2 + jsdom 30 可落地；捕获断言对现状代码 3 红（红点正在「写入未经 startTransition」）、透传与 refresh-sync 反例 3 绿（含捕获回调 flush 落地的机制自证）。
- **真机走查要素**：dev server `npm run dev`（vite，缺省 5173、被占自动顺延——卡 note 里的 5174 即顺延结果），反代默认 `http://127.0.0.1:7777` 本机 agentd（`AGENTD_URL` 可覆盖）；登录走 `/console?ticket=…`（agentd 主令牌经 `POST /api/auth/tickets` 签发，`internal/agentd/authroutes.go:4`）。详见 §6。

## 3. 任务分解（TDD：每 task 先测试后实现；T1 完成态整体是红的，属 TDD 预期）

### T1 回归锁（`web/src/app/data/usePoll.test.ts`）

**锁什么**：「续帧写入必须经 startTransition、无直接泄漏、refresh 保持 sync」。jsdom 无法直接观测 lane 调度（spec §7 承认真机权威），故取**行为等价断言形态——写路捕获锁**。等价论证：紧急（sync）续帧写的可观测特征 = 续帧一跑状态立即落地、且不经 startTransition；捕获锁对两个泄漏方向都红——漏包（某写入在 transition 外）⇒ captured 为空或 data 立即变更；错包（refresh 被误降级）⇒ refresh 反例断言红。真机的让出窗口问题由 S1 走查兜底，锁只负责拦住「有人拆掉 transition 包装」的回归。

**怎么写**（探针已验证全部机制可落地）：

1. 文件级 mock（放在文件顶部，`vi.mock` 自动提升）：

```ts
const { captured, mockState } = vi.hoisted(() => ({
  captured: [] as Array<() => void>,
  mockState: { captureOnly: false },
}))

vi.mock('react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react')>()
  return {
    ...actual,
    startTransition: (fn: () => void) => {
      if (mockState.captureOnly) { captured.push(fn); return }
      return actual.startTransition(fn)
    },
  }
})
```

   注意：`captured`/`mockState` 必须经 `vi.hoisted` 声明——mock 工厂早于模块体执行，普通模块级变量会 TDZ。
2. mock 落地后**既有 8 条用例必须原样全绿**（透传模式 = 语义等价的证据；探针已单点验证，若整组有红按 §7「测试基建意外」处理）。
3. 新增捕获组用例（`captureOnly = true`，`beforeEach` 归零 captured 与开关；沿用文件内既有 fake-timers 模式：`vi.useFakeTimers()` + `await act(async () => { await Promise.resolve() })` 冲微任务 + `vi.advanceTimersByTimeAsync` 推 tick；setup.ts 已有 jest 全局桩，waitFor 可用）：
   - **首拉+定时续拉捕获**：captureOnly 下首拉与 tick 各产生 ≥1 个捕获回调（合计 ≥2），期间 `result.current.data` 保持 `null`（无直接泄漏）；然后 `act(() => { captured.splice(0).forEach((fn) => fn()) })`，data 落地为最后一次写入的值；
   - **401 终止态捕获**：fetcher reject `ApiError(401, …)`，captured ≥1 且 `sessionExpired` 仍 false，flush 后 `sessionExpired === true`；
   - **断线捕获**：fetcher reject `ApiError(0, …)`，captured ≥1，flush 后 `disconnected === true`（「断线保留 data」的语义由既有透传用例继续背，本用例只锁写入路径）；
   - **refresh 保持 sync（反例）**：captureOnly 下 `act(() => { result.current.refresh() })` 后 captured 数量不变（nonce 写入没进捕获队列 = sync lane）。
   - 断言粒度自决权：捕获回调个数用 `>=` 不用 `===`（锁「每处写入都进 transition」，不锁回调拆并粒度——见 §7 填补级）。

**验证**：`npx vitest run src/app/data/usePoll.test.ts` → 新增捕获组用例**对现状代码必须红**（红在捕获数量/无泄漏断言，探针实测 3 红），既有 8 条透传全绿。红不了说明锁没锁住，先修锁再进 T2。

### T2 实现（`web/src/app/data/usePoll.ts`）

**包装写点清单**（三处包，两处明确不包）：

| 位置 | 现状 | 改法 |
|---|---|---|
| `:85-86` 成功路径 | `setData(v)`、`setDisconnected(false)` 逐个裸落 | 两行进**同一个** `startTransition` 回调（单 lane 一次提交，数据与断线标志同帧一致） |
| `:92` 401 终止态 | `setSessionExpired(true)` 裸落 | 单独包一个 `startTransition`；`:91` `stopTimer()` 留在包外（定时器控制不是渲染数据） |
| `:96-97` 断线路径 | `setDisconnected(true)`、`setErrorText(errorMessage(err))` 裸落 | 两行进**同一个** `startTransition` 回调；`errorMessage(err)` 可在包外先算好再进回调（填补级自决） |
| `:52` refresh | `setNonce((n) => n + 1)` | **不动**（事件处理器紧急更新，冻结项 2） |
| ref 写入 `:80-81`/`:99` | ref 赋值 | **不动**（非 React 状态） |

实现注意：`stopped` 守卫（`:84`/`:88` 的提前 return）留在 transition 外——它是控制流不是状态写入；import 增加命名导出 `startTransition`（React 19）；`finally` 清槽、三条实时性纪律代码一律不碰；**成功对/断线对的两个 setter 不得拆进两个不同提交**（同对同回调最稳，拆开即拆帧）。

**验证**：`npx vitest run src/app/data/usePoll.test.ts` → T1 的红全绿（捕获组 + 透传既有组 + refresh 反例全数通过）；`npm run typecheck` 绿。

### T3 全量回归

- `npm run typecheck`（编译全量：tsc -b 全仓类型）；
- `npx vitest run`（集成全量：web 全量，基线 140 文件/1701 用例 + 新增用例，全绿）；
- 关键结构面套件重点确认原样绿：`src/app/shell/Shell.test.tsx`、`src/app/tree/ProjectTree.test.tsx`、`src/app/tree/MobileProjectDetail.test.tsx`、`src/app/shell/useMobileNav.test.tsx`、`src/app/rooms/pollInterval.test.tsx`、`src/app/data/*`——消费方零改动（冻结项 4），任何消费方套件红都说明动到了消费方语义，按 §7 必须回协调者，不得顺手改消费方。

## 4. 改动边界

**白名单（允许动，预计仅此两文件）**：

- `web/src/app/data/usePoll.ts`（T2 实现）
- `web/src/app/data/usePoll.test.ts`（T1 回归锁）

**禁改清单**：

- 一切消费方组件与 hooks（grep `usePoll` 消费方清单见台账：Shell.tsx、useTasks.ts、useProjectTree.ts、useMachines.ts、usePreviews、useUpdate.ts、BoardPage、CardsPage、CoordinatorPanel、SessionSidebar、SessionTab、MobileProjectDetail、MachinesPage 等）——包括「顺手改注释/格式」；
- `web/src/App.tsx`（router 挂载与 `useTransitions`）与 `web/src/main.tsx`；
- react-router-dom 版本（不升级、不迁移 data APIs）；
- `PollState` 接口签名、`intervalMs`/`timeoutMs`/`enabled` 参数语义、三条实时性纪律的任何一行；
- Go 侧一切文件（agentd 零改动）。

发现必须动白名单外文件才能实现时：停手，回协调者说明原因（§7），不得先斩后奏。

## 5. 三段律在本卡的具体对应（web 前端）

| 段 | 命令（在 `web/` 下） | 位置 |
|---|---|---|
| 编译全量 | `npm run typecheck`（tsc -b） | 每 task 收口 |
| 测试局部 | `npx vitest run src/app/data/usePoll.test.ts` | T1/T2 内循环 |
| 集成全量 | `npx vitest run`（web 全量） | T3 |

本卡无 Go 侧改动、无 wire 变化，无跨端集成段。提交与分支操作按 implement 通用纪律，本计划不另设。

## 6. 验收准备：真机 S1 走查指引（协调者/用户执行；执行者只需保证分支可走查）

1. **起 dev server**：worktree 下 `cd web && npm ci && npm run dev`——vite 缺省 5173（被占自动 +1，以启动输出打印的端口为准）；反代目标默认 `http://127.0.0.1:7777` 本机 agentd，异机用 `AGENTD_URL=http://host:port npm run dev` 覆盖。注意反代**不要加 changeOrigin**（Host/Origin 白名单闭环，`web/vite.config.ts` 头注释有完整原因）。
2. **登录**：浏览器开 `http://localhost:<port>/console?ticket=…`——agentd 主令牌经 `POST /api/auth/tickets` 签发一次性 ticket（与日常用 console 的入口一致）。
3. **离线源确认**（复现环境同款，卡 note 2026-09-27）：macbook-pro 处于离线（ping 100% loss），`/api/pty/sessions?scope=all` 实测拖到 ~3s（agentd.log `deadline_exceeded` 旁证）。不满足时先恢复该状态再走查，否则命中窗口不成立。
4. **走查内容**照 spec §4 执行：devtools 390×844 移动模拟 → 主走 mobile-home → 项目详情 → 「在此打开终端」下钻（URL 前进 `detail=1` 且 UI 跟随提交，全程不整页刷新）；补走会话/任务现场/文件/目录覆盖层下钻各一次；桌面反例 ≥1024 同路径。观察项（非门）：console 不再出现「Cannot update a component (Shell) while rendering a different component (ProjectTree)」。
5. **证据**：走查录屏或截图 + console 全程记录，落台账。

## 7. 执行中偏差分类

**填补级（执行者自决，落台账即可）**：

- 测试断言的形态微调：flush 方式、断言粒度（如 `>=` 改精确值）、用例命名与注释、`vi.hoisted` 结构等价写法；
- `errorMessage(err)` 计算位置（包外先算 vs 回调内计算）；
- 三处 transition 回调的拆并粒度（前提：每处写入都在 transition 内，且成功对/断线对各自保持同回调不拆帧）。

**必须回协调者（升级，不得自决）**：

- 动白名单外任何文件（含消费方「顺手修」）；
- 动 refresh 的 sync 语义、节奏参数、三条实时性纪律、`PollState` 签名（任何冻结项）；
- **启用降级口子**（终止态横幅恢复 sync）——这是 spec 冻结项 5 的裁决权；
- T3 出现任何消费方套件红；
- 测试基建意外：`vi.mock('react')` 透传在执行环境行为与探针结论不符（既有 8 条透传红）。先在台账记录差异并尝试等价替代形态（如 `vi.doMock` + 动态 import 的用例级隔离）；替代也不行则回协调者——**不得为可测性改生产代码结构**（写点是冻结面）。

## 8. 派发前自审

本计划无「驱动派发系统自身」的验收步骤：T1–T3 全部是本地 vitest/tsc，可派发；§6 真机走查已标注归协调者/用户，不进执行者 task 清单。
