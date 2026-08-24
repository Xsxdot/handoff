# Spec：handoff 宿主侧挂载 charter viewer（同源静态挂载 + iframe 薄壳 + fullPageRoute 修复）

> 状态：**待用户批准**（2026-08-24 出稿）
> 级别与档位：**L3 轻档**（对接一个已冻结的跨仓契约：charter `graph/webui.FS()` 与两条只读 API 的同源前提；handoff 侧无并行子系统可扇出）→ contract → breakdown → 单轮 implement → review → acceptance → finish
> 卡：`C1.7`（父卡 `C1` 代码图批次二；来源是 C1.4 实现计划的 T4，因跨仓另开卡承载）
> 来源：`docs/roadmap.md` 第 11 条；C1.4 plan `docs/plans/2026-08-23-codegraph-webui-extraction-plan.md#T4`

## 问题陈述

C1.4 把查看器源码搬进了 charter（`graph/webui`，dist 提交入仓、`webui.FS()` 是唯一边界），
此后 C1.3 一期、C1.9 二期都长在 charter 那一侧。**但宿主 handoff 从来没接过去**：
`handoff/web/src/app/codegraph/` 里那 19 个文件仍是搬迁前的旧副本，控制台 `/codegraph` 渲染的还是它。

于是今天有三件事同时成立：

1. **控制台看到的代码图页是过时形态**——旧副本没有理想树全景、没有 gap 执法读数、没有二期的嵌套下钻/迁移视角/欠账读数。这些形态已经合进 charter master 并真数据走查过，却只能通过开发用的 vite 5174 通道看见。
2. **一份查看器被维护两遍**——同一套 `DetailPanel`/`FocusGraph`/`domains.ts` 在两个仓里各存一份（handoff 侧 19 文件与 charter 侧同名文件已分叉），C1.4 消灭重复的目的没有兑现。
3. **`/codegraph` 被工作台外壳挤压**——`fullPageRoute` 白名单只有 `/cards`、`/flows`、`/settings`、`/machines`（`handoff/web/src/app/shell/Shell.tsx:391`），所以进代码图页时右栏 280px 文件树与顶部面包屑仍然挂着；这个 bug 从 C1.4 立卡时就记在案，一行修复。

## 现状读数（事实核查，2026-08-24，contract 节点须对当轮工作树复核）

| 读数 | 值 | 出处 |
|---|---|---|
| handoff 已依赖 charter graph | `github.com/Xsxdot/charter/graph v0.6.0`，无 `replace` | `handoff/go.mod:7` |
| charter master 相对 `graph/v0.6.0` 的 webui 增量 | 19 文件 / +1383 −123（二期：`besttree.ts` +324、`BestScopePanorama.tsx` 等） | `git diff --stat graph/v0.6.0 master -- graph/` |
| **v0.6.0 里没有二期** | 成立——二期随 C1.9 合入 master（`aab28fc1`）在 tag 之后 | 同上 |
| dist 是提交物 | `graph/webui/dist/**` 在版本控制内；CI 有「Reject stale committed dist」重建比对门 | `.github/workflows/ci.yml:32-41` |
| 宿主静态挂载现成件 | `newSPAHandler(fsys fs.FS, log)` 已存在，处理文件命中/深路径回退/405/index 缺失 | `handoff/internal/agentd/webhandler.go:45-58` |
| 宿主根挂载 | `mux.Handle("/", newSPAHandler(webui.FS(), s.log))`（handoff 自有 `internal/webui`，与 charter 的 `graph/webui` 是两个独立包） | `handoff/internal/agentd/server.go:542` |
| 两条只读 API | `GET /api/projects/{name}/codegraph`、`…/codegraph/source`，注册在 auth 之内 | `handoff/internal/agentd/server.go:509-510` |
| viewer 侧项目参数 | 只从自身 URL 读 `?project=`，不含项目选择器 | `charter/graph/webui/src/app/codegraph/CodegraphPage.tsx:9,28` |
| viewer 侧取数 | `fetch('/api/projects/{name}/codegraph', { credentials: 'same-origin' })`——**同源是硬前提**，不带 Authorization | `charter/graph/webui/src/api/client.ts` |
| viewer 静态资源 base | `base: './'`（相对），可挂任意同源子路径 | `charter/graph/webui/vite.config.ts` |
| handoff 旧副本 | `web/src/app/codegraph/` 19 个文件，`Shell.tsx:53,491` 引用 `CodegraphPage` | 实读 |
| 入口按钮 | `ProjectTree` 的 `onOpenCodegraph` → `navigate('/codegraph')`，不改 | `handoff/web/src/app/shell/Shell.tsx:461` |

## 方案（含弃选与理由）

**主方案：照 C1.4 plan T4 全文执行**——同源静态挂载 + iframe 薄壳 + 白名单一行。
该 plan 已过 review 与协调者拍板，接缝代码、负向检查、真机清单都是现成的，本 spec 不重写它，
只**更正三处已被时间推翻的前提**：

### 更正一：升的版本不是 `v0.4.0`，是一个**新发的 tag**（建议 `graph/v0.7.0`）

plan 写于 C1.4 期间，那时目标是 v0.4.0。今天 handoff 已在 v0.6.0，而**二期 webui 不在任何 tag 里**——
它只在 charter master 上。所以本刀必须**先从 charter master 发一个新 tag**，handoff 再钉它；
钉 v0.6.0 等于把控制台换成一期形态，白干一半。

发 tag 是外部动作（push 到 GitHub、写 module proxy），按 C1.4 T3 的先例**归协调者执行、不派发**，
且必须在 handoff 侧改 `go.mod` 之前完成、`go mod download` 可验。

### 更正二：验收判据从「等价搬迁、不改像素」改为「换代 + 逐屏可用」

plan T4.4 第 5 条写「逐屏比较……唯一预期差异是项目下拉消失」。**这条今天不成立**：
charter 侧已经跑过一期与二期，形态本就该不同。改为：

- 换代判据：`/codegraph` 渲染的是**理想树全景 + 执法/欠账读数 + 嵌套下钻 + 迁移清单**（二期形态），不是旧的现状域全景；
- 可用判据：详情面板、源码窗口、焦点历史、视图（分支 diff）选择器逐项仍可用，读数与 `codegraph check` CLI 同口径；
- 挤压判据：进 `/codegraph` 时右栏文件树与顶部面包屑均不出现，退出后照常恢复。

**弃选**：为了保住「等价搬迁」而把 handoff 钉在 v0.6.0——那是为了让一句旧判据成立而故意交付旧形态。

### 更正三：删除范围按当日实际文件数核，不照抄「19 个文件」

plan 的负向检查写死了「`web/src/app/codegraph` 只剩 1 个文件」，这条判据形式不变；
但 contract 节点须按当轮工作树重数一遍文件清单，不照抄数字。

### 关于顺序（本刀与三期查看器 C1.10）

C1.10（三期）还会再改 `graph/webui`，届时需要**再发一次 tag + 再升一次 `go.mod`**（一行 + `go mod tidy`）。
**裁决：仍按用户给定顺序先做本刀**——理由是本刀落地后，三期的真机走查就发生在真控制台里，
而不是 vite 5174 的旁路通道；多付的一次升版成本（约十分钟）换的是三期全程的验收保真度。

## 用户故事

1. 作为控制台用户，我点项目树的「代码图」，看到的是与 `codegraph check` 同口径的理想树全景与欠账读数，而不是一个停在几周前的旧页面。
2. 作为控制台用户，我在代码图页不再被右栏文件树和顶部面包屑挤掉三分之一横向空间。
3. 作为维护者，查看器只有一份源码（charter `graph/webui`），handoff 侧只剩一个几十行的 iframe 薄壳；改形态只改一处。
4. 作为控制台用户，我登录后 iframe 里的取数照常带 handoff 的会话 cookie，浏览器网络面里没有第二个端口、没有 CORS、没有 Authorization 头。
5. 作为跨机用户，`?machine=` 转发语义不变——viewer 请求的仍是宿主那两条 API。

## 契约语义与接缝（L3）

**对接的既有契约（本刀不改其语义，只兑现它）：**

- **`webui.FS() fs.FS`**：charter 单方面冻结的边界——只给文件系统，不给 handler、不给路由、不给鉴权。宿主必须自己包 handler，且**必须包在 auth 之内**：viewer 的静态资源不是公开资源。
- **同源前提**：viewer 用 `credentials: 'same-origin'` 取数，所以挂载路径必须与 API 同源同 mux；一旦改成独立端口或跨源，viewer 立刻取不到数——这是接不接得上的分界线，不是优化项。
- **`?project=` 是宿主→viewer 的唯一入参**：宿主负责把当前工作台项目名 percent-encode 进 iframe src；viewer 不做项目选择、不读宿主状态。
- **两条只读 API 的路径参数 `{name}` 不动**（C1.4 已裁定，roadmap 11d）。

**本刀新增的接缝（语义定在此，签名归 contract）：**

- **挂载点**：宿主在 auth 内、根 SPA fallback 之前，把 charter 的 FS 挂在一个**专属子路径**下（plan 定为 `/codegraph/app/`），用 `StripPrefix` 对齐 FS 根。两棵静态树（handoff 自有 `internal/webui` 与 charter `graph/webui`）**不得混挂在同一前缀**——混挂会让深路径回退落到错误的 index.html。
- **薄壳职责边界**：`CodegraphFrame` 只算 src 并渲染 iframe；不请求 API、不读 token/cookie、不复制项目选择器。
- **版本坐标**：`go.mod` 钉精确 tag，**不得写 `replace`**——replace 能让本机绿而消费方红。

**接缝清单（三条，全部是既有符号 + 已知调用方）：**

1. `handoff/internal/agentd/server.go` 的 mux 装配（调用方：`newSPAHandler`、`s.auth`）——静态挂载的行为缝，既有 `webhandler_test.go` 的 harness 直接覆盖它（文件命中 / 深路径回退 / 405 / index 缺失）。
2. `handoff/web/src/app/codegraph/CodegraphFrame.tsx#CodegraphFrame`（调用方：`Shell.tsx` 的 `<Route path="/codegraph">`）——新建导出符号，导出到 Shell 一层。
3. `handoff/web/src/app/shell/Shell.tsx#fullPageRoute`（调用方：同文件的 Breadcrumb 条件 `:478` 与 FileTree 条件 `:567`）——**两处必须同时受它约束**，只改一处等于 bug 只修一半。

**边界型例外**：iframe 同源取数、未登录被 auth 拒、桌面薄壳渲染属于浏览器现实，Go/vitest 测不了，全部落真机清单，不许用 mock 抵账。

## 实现决定

- 静态挂载复用现成 `newSPAHandler`，不新写 handler、不加 CORS/postMessage。
- handoff 侧不新增前端测试：薄壳无业务逻辑（C1.4 contract 已裁决），既有 SPA/provider harness 保持绿即为回归判据。
- `internal/webui` 与两条 API handler 的错误文案/状态码零改动——`git diff --exit-code -- internal/webui` 作为负向检查。
- 发 tag 与打包换装归协调者，不派发。

## 测试决定（接缝清单）

- **缝 1（静态挂载）**：既有 `internal/agentd/webhandler_test.go` 断言保持绿；新增一支断言 `/codegraph/app/` 前缀命中 charter FS 的 index（与根 index 不是同一份内容），这是「两棵树没混挂」的缝级断言。
- **缝 2（薄壳）**：无逻辑，不新增用例；由 handoff `npm run typecheck && npm run build` 与结构负向检查（目录只剩 1 个文件、无 `fetchCodegraph` 残留引用）守。
- **缝 3（fullPageRoute）**：既有 Shell 测试若已覆盖白名单则补 `/codegraph` 断言；未覆盖则新增一支，断言进 `/codegraph` 时 Breadcrumb 与 FileTree **两者**都不渲染。
- **真机（不可用 mock 抵账）**：①登录态访问 `/codegraph/app/` 得 charter index，深路径回退同一 index，POST 得 405，未登录得鉴权响应而非 HTML；②iframe 取数自动带同源 cookie，网络面无 Authorization/CORS/第二端口；③项目名含空格/斜杠/中文时 `?project=` 还原正确；④二期形态逐屏可用且读数与 CLI 同口径；⑤`/codegraph` 页无文件树无面包屑；⑥agentd 换装重启后无孤儿进程、无半挂载路由。

## Out of Scope

**永不做：**
- 给 viewer 开第二个端口或跨源部署（直接违反 `same-origin` 取数前提）。
- 在宿主侧复制/分叉查看器组件（C1.4 消灭的就是这个）。

**本期不做、后续要做（落 roadmap）：**
- **两条只读 API 的响应体瘦身**（今天一次性返回整份 baseline + 全部 diff + stale + best/target/report，handoff 量级已达 MB 级）——roadmap 11b 既有条目，本刀不动。
- **404 语义结构化**（前端按中文文案匹配 404 分支）——roadmap 11a 既有条目。
- **`codegraph serve` 独立命令**（涉 charter graph 包「不发网络」不变式的修订）——roadmap 既有议题，不混入本刀。
- **`?machine=` 跨机转发下 viewer 的行为细化**（远端 agentd 版本落后时只返回老三样）——一期已实证向后兼容，本刀沿用，不新增能力。

## 备注

- **部署门**：本刀真机验收需要重新打包 handoff 二进制、换装 `~/.local/bin/handoff` 并重启 agentd。**agentd 是卡派发的执行面**——重启必须避开在飞派发，否则会打断正在跑的节点任务。无人值守推进时，此步须在无在飞任务的窗口执行。
- 图覆盖债：handoff 仓有代码图，本节点的宿主侧读数走的是直接读码（挂载装配与 Shell 白名单都是包内私有装配点，非导出符号，图查询命中率低）。
