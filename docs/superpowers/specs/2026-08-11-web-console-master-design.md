# Web 控制台总方案

> 日期：2026-08-11
> 状态：设计已逐节确认
> 上游决策：ADR-0009「桌面控制台改为 agentd 托管的 Web UI，外加薄壳」（在归档分支 `archive/desktop-console-2026-08-11` 上）
> 前置能力：`docs/superpowers/specs/2026-08-11-agentd-browser-auth-design.md`（已实现）
> 视觉基准：`prototypes/desktop-console/` 的实现截图
> 工作分支：`handoff/web-console`（不合入 `main`）

本文是 Web 控制台的**总方案**，负责划定分期、锁定全局技术决策、并直接覆盖 W1 与 W2 的设计。
W3 与 W4 只在此定边界，各自另出 spec。

---

## 1. 形态与分期

### 1.1 形态

界面由 agentd 自己托管，**浏览器指向哪台 agentd 就管哪台**。没有中心服务器，没有跨机聚合。

想看 devbox 上的任务，就打开 devbox 那台 agentd 的 URL——它给出它自己的 UI 和它自己的 API，
天然同源、天然同版本。这正是 ADR-0009 选择 `loadURL` 而非壳内自带页面的理由：一份产物，永不漂移。

跨机汇总不是被放弃，而是被推迟到 W3——它需要机器注册表，而机器注册表本来就是 W3 的产物。

### 1.2 六期与依赖

| | 子项目 | 主体 | 后端新增 | 出口 |
|---|---|---|---|---|
| **W1** | 前端地基 | 接线 | 无（仅一个契约测试） | `web/` 骨架跑起来，带真实会话打通 API 与 WS |
| **W2** | 任务审核台 | 纯前端 | 无 | 看板 + 任务详情 + 事件流 + 工单审批 + diff + continue/done |
| **W3** | 项目与机器控制面 | 后端为主 | 大 | 项目树、机器列表、跨机汇总 |
| **W4** | 工作区资源 | 后端为主 | 大 | 文件浏览/编辑、终端、预览 |
| **W5** | 打包与发布 | 接线 | 小 | `go:embed` + 桌面薄壳 |
| **W6** | 移动端响应式 | 纯前端 | 无 | 手机上看板与审批可用 |

### 1.3 并行编排

```
阶段一   W1 ─────────┐            (串行)
阶段二              ├─ W2 前端  ‖  W3 后端 API
阶段三              ├─ W3 前端  ‖  W4 后端 API
阶段四              └─ W4 前端  ‖  W5 打包
```

同时在跑的任务不超过 2–3 个。上限来自审核者而非技术：每个任务一条后台 `wait`，
工单要逐条判，同时盯四五个任务会显著抬高误批概率——而工单里混着「装全局依赖」
「改 CI」这类必须升级给人的请求。

### 1.4 流程深浅

- **W1、W2** 由本文直接覆盖，不再单独 brainstorm。W1 是接线活；W2 的产品形态已由原型定死。
- **W3、W4 必须各自走 brainstorm → spec → plan**。它们要定项目 / 机器 / location 的数据模型与归属关系，
  那是产品决策（ADR-0002、0006、0008 已定了部分边界），不能外包给看不到 ADR 全貌的 executor。
- **W6** 保持推迟，但 W2 起就守一条便宜的纪律：布局用 flex/grid + 相对单位，不写死像素宽度。
  这让 W6 是调整而不是重写。

---

## 2. 全局技术决策

| 决策点 | 结论 | 理由 |
|---|---|---|
| 前端框架 | React 19 + Vite 6 + TypeScript | 与原型、与归档分支 renderer 同栈；W3 迁移时归档代码可直接搬 |
| UI 组件 | shadcn/ui（Radix + Tailwind）+ lucide-react | 默认观感即原型那套中性风；组件抄进仓库可自由改；与归档 renderer 的 `@/components/ui/*` 零成本对接；Radix 承担对话框/下拉/右键菜单的键盘与焦点行为 |
| 测试框架 | vitest | 与 Vite 同源，配置成本最低 |
| 分栏 | `react-resizable-panels` | 四区工作台的可拖拽分栏 |
| 类型契约 | fixture 驱动，不用代码生成器 | 见 §3.3 |
| `go:embed` | **推迟到 W5** | 见 §2.1 |
| 看板范围 | 单机 | 见 §1.1 |
| `waiting_answer` | 并入「进行中」列，卡片加标记 | 该状态在实际使用中是瞬时的——绝大多数工单审核者当场就答了。给它独立一列是给噪声让位 |

### 2.1 为什么推迟 go:embed

`go:embed` 要求被嵌目录在编译期存在。前端产物若不入库，任何没装 node 的环境
（含 CI、含日常 `go build ./...`）当场编译失败——而「既有构建与测试全绿」是本项目一贯的底线。

开发期改走 vite dev server + 反代（§3.2），编译期不再依赖前端产物，同时白得 HMR。

架构上没有损失：ADR-0009 定的「壳 `loadURL` 到 agentd 同源」是**发布形态**。
推迟 embed 只是把打包挪到有东西可打包的时候，不改任何架构决策。

---

## 3. W1：前端地基

### 3.1 目录与栈

仓库根下新建 `web/`：

```
web/
  src/
    api/            类型定义、fetch 客户端、WS 客户端
      testdata/     契约 fixture（由 Go 侧测试生成）
    components/ui/  shadcn 生成物
    app/            页面
  package.json
  package-lock.json （必须入库）
  README.md         （记录实际选定的版本与开发流程）
```

### 3.2 开发期反代

vite dev server 监听 5173，将三个前缀反代到 agentd（默认 `http://127.0.0.1:7777`，地址可由环境变量覆盖，不得写死）：

- `/api`
- `/ws`（需开启 WebSocket 反代）
- `/console`

反代 `/console` 是刻意设计，它让鉴权在开发期自然闭环：

```
浏览器访问 localhost:5173/console?ticket=…
  → vite 转给 agentd:7777/console
  → agentd 兑换 ticket、Set-Cookie、302 到 "/"
  → 浏览器跟到 5173 的 "/"，即 dev app
```

cookie 是 host-only 且**不按端口隔离**，因此 5173 后续的 `fetch` 与 `new WebSocket()` 都会带上它。

开发流程：起 agentd → `handoff console --print-url` 取 URL → 把端口 7777 换成 5173 打开 → 完成鉴权 → 正常开发。

### 3.3 类型契约

前后端要并行开发，需要一个**会自己报警**的共同基准：

1. **Go 侧**在 `internal/proto/` 下加一个测试，把对外类型的代表性样本 `json.Marshal` 后写入
   `web/src/api/testdata/<类型名>.json`，并**断言生成结果与已存文件逐字节一致**——
   字段改名、类型变化、新增字段都让它当场变红。提供显式的更新开关（如 `-update`），不得默默覆盖。
2. **前端**在 vitest 里读同一批 JSON，断言 TS 类型能解析且关键字段齐全。

**为什么不用代码生成器**：生成器只能保证「TS 类型与 Go 结构体长得一样」，
而 fixture 同时钉住**实际序列化结果**——`omitempty`、时间格式、指针字段是 `null` 还是缺席，
这些才是前后端真正会对不上的地方。且零新工具链。

### 3.4 W1 验收标准

带**真实 cookie 会话**完成三件事：

1. `GET /api/status` 拿到并渲染 agentd 版本与状态；
2. `GET /api/tasks` 拿到并渲染任务列表；
3. 对任意任务开 `/ws/events` 并收到至少一条事件。无任务时页面须明确显示「无任务，跳过 WS 验证」，不得假装成功。

界面只要能证明这三件事，不做任何业务界面。

**这三条怎么验**见 §7.3：executor 用 node 的 fetch + cookie jar 程序化跑完 ticket → cookie → 请求这条链路
（它所在的机器上就跑着一个 agentd，`handoff console --print-url` 可就地取得 ticket URL）；
页面渲染由审核者在本地真浏览器里肉眼确认。**不要求 executor 拥有浏览器。**

### 3.5 W1 边界

- 不做 `go:embed`；
- 不改任何 agentd 路由或既有 Go 业务代码，Go 侧唯一新增是 §3.3 的契约测试；
- `go build ./...` 与 `go test ./...` 保持全绿；
- 不碰 `.github/`；
- 不装全局依赖，依赖只进 `web/package.json`。

---

## 4. W2：任务审核台

### 4.1 两个界面

**任务看板**（顶层）与**任务详情 / 审核台**。数据全部来自今天已有的路由，后端零改动：

| 用途 | 路由 |
|---|---|
| 列表 | `GET /api/tasks` |
| 详情（含 `pending_tickets` 与近期事件） | `GET /api/tasks/{id}` |
| 实时事件 | `GET /ws/events?task=<id>&from_seq=N` |
| 批准 / 拒绝 / 回答 | `POST /api/tasks/{id}/reply` |
| 改动取证 | `GET /api/tasks/{id}/diff`、`GET /api/tasks/{id}/file`、`POST /api/tasks/{id}/run` |
| 回合正文 | `GET /api/tasks/{id}/render` |
| 推进 | `POST /api/tasks/{id}/continue`、`/done`、`/stop`、`/resume` |

### 4.2 看板列与状态机

| 状态机 | 看板列 |
|---|---|
| `pending` | 等待执行 |
| `running`、`waiting_answer` | 进行中（`waiting_answer` 的卡片加「等你答复」标记） |
| `waiting_review` | Review |
| `completed`、`failed` | 完成（`failed` 需视觉区分） |

### 4.3 实时性

`/ws/events` 是**按单个任务**订阅的，看板若照搬就得开 N 条连接。因此：

- **看板走轮询**：`GET /api/tasks`，2–3 秒一次。这是低频视图，轮询足够。
- **详情页开一条 WS**：拿实时事件。

整机级订阅（一条连接盯所有任务）是更好的终局，但那是后端改动，记入 backlog 留给 W3 一并处理。

### 4.4 cursor 归属

浏览器**不碰** `~/.handoff/cursor-*`——那是 CLI 审核者的本机状态。
前端进详情页时从 `from_seq=0` 拉，或从已知最大 seq 续；它是观察者，不参与 CLI 的游标账本。
这样浏览器与 CLI 可同时盯同一个任务，互不干扰。

### 4.5 会话失效

`watchSession` 会在会话被吊销时以 close code 1008（`StatusPolicyViolation`）关闭 WS。
前端必须识别该 code，落到「会话已失效，请重新打开控制台」，**不得无脑重连**——否则就是一场静默的重连风暴。

### 4.6 断线语义

保留最后拿到的列表元数据继续显示，所有操作按钮置为不可用，明确标注「已断开」。
**不称为「只读」**——只读暗示数据是新的，而它不是。

### 4.7 审批要当真

浏览器点「批准」与 CLI 敲 `reply --approve` 是同一件事，因此：

1. 权限原文**完整展示、不截断**；
2. `--deny` **必须填理由**才能提交——理由会回到模型手里，不填它就原地重试同样的操作；
3. `stop`、`done` 这类不可逆操作需二次确认。

### 4.8 错误展示

agentd 的错误消息是中文且信息量大（「任务不存在」「状态不符」「基线提交在任务仓库中不存在……请先在本地 git push」）。
前端**必须把原文透出来**，不得吞成一句「操作失败」——那些消息里带着解法，吞掉等于把用户送去看日志。

---

## 5. W3 / W4 的边界（本文只定边界，各自另出 spec）

**W3 项目与机器控制面**：machines / projects / locations / workspaces 的持久化与 API，
跨机任务汇总，以及 §4.3 提到的整机级事件订阅。归档分支 `codex/phase2-desktop-completion` 上的
`desktop/src/renderer/src/features/handoff/`（catalog-store、ProjectTree、ProjectCreateDialog、WorkbenchShell）
是这一期的前端起点，其 shadcn import 可由 CLI 重新生成。

**W4 工作区资源**：文件 REST（浏览、读取、编辑、冲突保护）、PTY 终端、浏览器预览。
ADR-0009 的两项未决——编辑器选型（Monaco 体积过大，需评估 CodeMirror 6）与临时文件落点
（建议 agentd 托管 `~/.handoff/scratch/`）——在 W4 的 spec 里裁决。

---

## 6. 并行纪律

**冲突面只有一处。** W2 只动 `web/`，W3 只动 `internal/`；前后端并行的天然优势是文件级几乎不重叠。
唯一共享面是 `internal/proto/` 与它生成的 `web/src/api/testdata/`。

**因此纪律收窄成一条**：并行期间 `internal/proto/` 由审核者独占。任何任务发现类型不够用，
**必须停下来提问**，由审核者改契约、提交、再让其他分支 rebase。
两个并行任务各自改 proto，冲突会攒到最后一刻集中爆掉——那是并行开发失败的经典死法。

**契约会自己报警**：§3.3 的 fixture 测试是双向的。后端改字段未同步 fixture → Go 测试红；
前端按旧字段写而 fixture 已更新 → vitest 红。任何一侧单方面漂移当场可知，不必等联调。

**合并顺序由审核者控制**，全部落在 `handoff/web-console`。每个任务分支回来先本地跑全量测试再合，冲突由审核者解。

---

## 7. 验证策略

### 7.1 Go 侧

§3.3 的契约 fixture 测试；既有 `go build ./...` / `go test ./...` 全绿是底线。

### 7.2 前端

vitest。至少钉死状态机映射、拒绝必须带理由、close code 1008 处理、契约解析四项。

### 7.3 真实闭环

分两半：

- **executor 侧自动验收**：ticket → cookie → 带 cookie 的 `fetch` 与 `WebSocket` 这条链路，
  用 node 的 fetch + cookie jar 可完整程序化跑完，不需要渲染引擎。这能自动化，也该自动化。
- **渲染与交互由审核者肉眼验**：分支回到本地后在真浏览器里过一遍。

**不引入 Playwright**：它会给仓库压上几百 MB 的浏览器依赖，而当前阶段需要它验的东西
（页面长什么样）恰恰是自动化最不擅长、人看一眼最快的部分。W4 有了终端与编辑器后可重新评估。

---

## 8. 本轮明确不做

- **跨机汇总与 agentd→agentd 转发**：留给 W3；相关的中转与信任模型见浏览器鉴权 spec §8。
- **移动端专门布局**：留给 W6，W2 起只守「不写死像素宽度」。
- **`go:embed` 与桌面薄壳**：留给 W5；薄壳选型（Tauri vs 裸 Electron）届时裁决。
- **测试 CI**：本仓库目前只有 `release.yml`，没有测试工作流。新建测试 CI 是独立的事，不塞进本轮。

---

## 9. 已知缺口

1. **整机级事件订阅**缺失，导致 W2 看板只能轮询（§4.3）。留给 W3。
2. **测试 CI 缺失**（§8）。前端与 Go 的测试目前都靠本地与 `handoff run` 执行。
3. **ADR-0003 与桌面 spec §41 的冲突**（原生 TUI 是否保留）仍未裁决，ADR-0009 已记录未解。
   本文不依赖该冲突的任一解，故不阻塞。
