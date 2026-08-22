# 拆解提案：工作台自定义启动项（需求 B）

日期 2026-08-22 · 前置 [spec §B](2026-08-22-executor-timing-and-custom-launchers-design.md) + [契约冻结物](2026-08-22-custom-launchers-contract.md)（`ce79d234`） · 节点 `charter:breakdown`

**形态声明**：单上下文兜底（本会话有「未经用户要求不调 Agent」的约束）。**一切岔口一律「待拍板」、无自批**，出稿即停。

---

## 0. 岔口清单（**2026-08-22 已由用户拍板，四条全按出稿者倾向**）

| # | 岔口 | 选项 | 裁决 |
|---|---|---|---|
| **Q1** | 启动项怎么进 `PickKind` | (a) 模板字面量类型 `` `launcher:${string}` `` (b) 回调加第二个可选参数 (c) 另开一条 `onPickLauncher(name)` 回调 | **(c)** ✅，见 §5.1 |
| **Q2** | 工作台的启动项列表从哪来 | (a) BlankTab 挂载时按需拉 `/api/launchers` (b) 挂进 Shell 的项目树流（30s 轮询）一起下发 (c) Shell 层拉一次 + 手动刷新 | **(b)** ✅，见 §5.2 |
| **Q3** | 启动项开出的 tab 标题 | (a) 启动项名字（「跑测试」） (b) 沿用「终端 N」 | **(a)** ✅，见 §5.3 |
| **Q4** | 命令送进终端前要不要回显一行标记 | (a) 不回显，就像人敲的一样 (b) 回显一行 `▶ <启动项名>` | **(a)** ✅，见 §5.4 |

**拍板记录（三重闸门核对）**：

- **Q1 (c) 与 Q4 (a) 两条入闸，写进本节留痕**：
  - **Q1 (c)**：难逆转（`PickKind` 一旦变成开集，`hotkeyOf` 的穷举与 `pickItemsFor`
    的过滤就要开始解析字符串前缀，回头收窄要动三个组件）；无上下文会惊讶
    （后人看到「一个下拉菜单为什么要两条 onPick」会想合并）；真取舍（(a) 改动
    确实最小，被否掉的理由是它让闭集这个性质消失）。**决定：内置种类与启动项是
    两个正交的轴——闭集与会长的列表不是同一种东西，宁可多一条 prop。**
  - **Q4 (a)**：**反过来写不会有任何测试变红**——「要不要多回显一行」只活在
    流程描述里，没有它自己的判据，正是最容易被后人一次「顺手优化」无声推翻的
    那类裁决。**决定：写进终端的内容 = 命令原文 + `\n`，不多不少**（B1 的验收
    原文照此保留）。理由：spec 拍定的语义是「像人亲手敲进去一样」，回显 handoff
    自己的标记既破坏这个错觉，那行文本又会混进滚动历史被 `Ctrl-R` 搜到。
- **Q2、Q3 不入闸，不写拍板记录**：Q2 是与既有数据流对齐（不难逆转，换来源
  就是改一处 hook）；Q3 反过来写会有测试红（标题断言）。记它们只会让拍板记录
  退化成会话日记。

拍板已完成，**可以扇出**。

---

## 1. 触及子系统清单

| 子系统 | 类型 | 本次触及 | 派卡资格 |
|---|---|---|---|
| **d_host** 宿主进程与 PTY 域 | **边界型**（OS 进程、PTY、平台差异） | `InitCommand` 的就绪判据与写入 | 四条全过 ✅ |
| **d_controlplane** 控制面 API 域 | 逻辑型（对 TS 的 wire 是接缝） | 启动项 CRUD、会话创建接线、能力位三处投影 | 见 §1.1 |
| **d_web** Web 控制台域 | 逻辑型 | 管理块 + 工作台接入 | 四条全过 ✅ |
| **d_localint** 本机集成域 | 边界型 | **本次零卡** —— `launcher` 包与 `envfile.LoadFile` 已在 Ticket 0 完成并有测试 | 不适用 |
| **d_remote** 远端连接域 | **边界型**（对端 agentd 版本差异） | **零改动**：`forwardIfRequested` 原样转发请求体 | 不适用，但**真机验证归它**（§7 第 1 条） |
| **d_contract** 契约域 | 逻辑型 | 已在 contract 节点冻结 | 不适用 |

### 1.1 d_controlplane 的派卡资格与第三条回答

架构法第三条**判据 2 命中**：`internal/agentd` 是 61 文件平铺包、无子包。回答义务履行如下：

**能圈出有界文件集**，本次触及**四个文件**：

| 文件 | 改动 |
|---|---|
| `internal/agentd/launchers_api.go`（**新建**） | GET/PUT 两个端点，形态照 `env.go` |
| `internal/agentd/pty_api.go` | 会话创建接线（env 解析、400 拒绝、`InitCommand` 透传） |
| `internal/agentd/server.go`（`:671` 一带 + 路由注册两行） | 自报能力位 |
| `internal/agentd/machines.go`（`:103` 与 `:153`） | 能力位的本机填充与远端搬运 |

新建的 `launchers_api.go` 恰好是「哪张卡碰到哪个家族、升格随卡走」的正例：它不往平铺包里再塞一个概念，而是照 `env.go` / `discipline.go` 已有的「一个配置面一个文件」形态落。**竖切欠账（[实例化清单 §6](2026-08-21-handoff-instantiation-checklist.md) 第 2 条）仍在，本次不预支。**

判据 1（前缀家族 ≥5 源文件）与判据 3（>2~3 万行，本包 19,870）均不命中。

---

## 2. 契约增量核对（对照冻结物逐条）

| 冻结条目 | 越界？ |
|---|---|
| `Launcher{Name, EnvFile, Command, EnvMissing}`，Name 即身份、无 id | 不越界 |
| `EnvFile` 与 `Command` 至少一个非空 | 不越界，`launcher.Validate` 已实现并穷举测试 |
| GET/PUT `/api/launchers[?machine=]`，PUT 整段替换 | 不越界 |
| `CreatePtySessionReq.EnvFile` 不存在 → **400 拒绝，不降级** | 不越界 |
| `InitCommand` 在交互 shell 内部执行，不进 argv | 不越界 |
| `LaunchersSupported` 三态：nil 按**不支持**处置 | 不越界。Q1~Q4 都不触碰它 |
| 能力位四处投影链 | 不越界。B3 卡覆盖三处写入端，B4/B5 覆盖读取端 |
| env 叠加顺序：`sessionEnv()` 在前，启动项文件在后（后者覆盖） | 不越界 |
| 命令原文不进日志 | 不越界，写进 B2/B3 验收 |
| 就绪判据：首字节输出 或 3s 兜底 | 不越界，B1 的主体 |

**需要新接缝吗？** 有一处**候选**，但查证后判定不需要：

> Q2 的选项 (b)「启动项挂进项目树流一起下发」看起来要动 `ProjectTreeResp` 的线格式——那会是一次契约变更。**但不必**：工作台读的是 `Machine`（`GET /api/machines`，已带全部能力位），启动项列表可以由 Shell 侧**另发一次** `GET /api/launchers` 并按同一节奏刷新，不改任何既有响应的形状。选 (b) 时按这个形态实现，**不扩 `ProjectTreeResp`**。

**结论：本次拆解不越界，不退回 contract。**

---

## 3. 子卡清单与依赖 DAG

```
┌──────────────────────────────┐        ┌──────────────────────────────┐
│ B1  ptyhost：InitCommand      │        │ B2  /api/launchers CRUD      │
│     就绪判据 + 写入            │        │     （d_controlplane）        │
│     （d_host · 边界型）        │        └───────────────┬──────────────┘
└───────────────┬──────────────┘                        │
                │                                        │
                ▼                                        ▼
┌──────────────────────────────┐        ┌──────────────────────────────┐
│ B3  会话创建接线 + 能力位三处   │        │ B4  web：api client +        │
│     （d_controlplane）        │        │     机器详情页管理块（d_web） │
└───────────────┬──────────────┘        └───────────────┬──────────────┘
                └────────────────┬───────────────────────┘
                                 ▼
                  ┌──────────────────────────────┐
                  │ B5  web：工作台接入（d_web）  │
                  └───────────────┬──────────────┘
                                  ▼
                  ┌──────────────────────────────┐
                  │ B6  真机验收 · 协调者执行     │
                  └──────────────────────────────┘
```

**B3 依赖 B1** 不是代码依赖，是**契约依赖**：契约 §3.1 第 2 点写死了「能力位与实现同生同死，不允许先上报 true、下一版补实现」。B3 里那行 `LaunchersSupported = &true` 只有在 B1 落地后才诚实。

B2 与 B1 互不依赖，可并行。

---

### B1 · `ptyhost` 的 `InitCommand`（d_host · 边界型）

**①契约引用**：`OpenOptions.InitCommand`（不含换行，实现补；空 = 不写；**不进 argv**）；就绪判据 = PTY 主端首字节输出 或 `initCommandReadyWait = 3s` 兜底，以先到者为准。

**②意图与为什么**：让启动项的命令像人亲手敲进去一样执行——命令跑完人还能接着用这个终端，Ctrl-C 只杀命令不杀会话。

观测点是现成的：`Engine.Open` 之后的 `go h.pump(s)` 读循环（[engine.go:78](internal/ptyhost/engine/engine.go:78)）是唯一看得见「shell 有动静了」的地方。

**不能走 argv**：`startPty` 起的是 `exec.Command(shell, "-l")` 的 login shell（[platform_unix.go:44](internal/ptyhost/engine/platform_unix.go:44)），改成 `sh -lc cmd` 会让会话在命令退出时结束。

**超时不是失败**：兜底到点照样写（内核输入缓冲一直在，字节不会丢），只打一条 Debug。

**③验收**（边界型 → 机内验契约形状，行为走真机）：
- `InitCommand` 为空时，`Open` 的行为与今天**逐字节相同**（兼容性守卫，反向断言必须配正面断言：非空时确实写了）；
- 用一个可控的假 shell（如 `cat`，它不产出任何输出）验证**兜底路径**：3s 后命令仍被写入——这条锁的是「超时不是失败」；
- 用一个立刻产出 banner 的假 shell 验证**首字节路径**：命令在远早于 3s 时被写入；
- 写入内容 = 命令原文 + `\n`，**不多不少**（反向：不带任何前缀标记——Q4 若拍板选 (b) 则此条改为断言前缀）；
- 平台：`platform_other.go` 侧（非 unix）保持编译且行为一致（`ptySupported=false` 时 `Open` 本就直接返回 `ErrNotSupported`，**不需要为它单独实现**——这是一条要在验收里写明的「无需改动」结论，不是遗漏）。

**④入口指针**：`internal/ptyhost/engine/engine.go:78`（Open/pump）、`platform_unix.go:44`（startPty）、`internal/ptyhost/types.go:40`（OpenOptions）。

---

### B2 · `/api/launchers` 的 CRUD（d_controlplane · 逻辑型）

**①契约引用**：`LaunchersResp` / `LaunchersReq`（整段替换）；PUT 的五档 400 校验；`Launcher.EnvMissing` 只在 GET 时算、PUT 时忽略客户端送来的值；`launcher.Item ↔ proto.Launcher` 的换算归本层。

**②意图与为什么**：把机器级的启动项配置做成一个配置面，形态与 `env.go` 一致（同一个心智模型：一个配置面一个文件、整段替换、保存时一次性校验、跨机由 `forwardIfRequested` 透传）。

**`EnvMissing` 必须在本层算**：`launcher.Item` 刻意不落盘这个派生字段（落盘就有两个真相）。GET 时对每条 `EnvFile` 做一次存在性检查。

**命令原文不进日志**：只记条数与「是否带命令」。启动项的 `Command` 可能含凭据。

**③验收**（逻辑型 → 机内闭环）：
- PUT 五档拒绝各一条：名字空 / 名字重复 / 两者都空 / 文件名含分隔符 / env 文件不存在；每条断言**错误文本里说得出是哪一条启动项**（错误会原样成为 400 响应体，只报「不合法」等于没报）；
- PUT 合法 → 返回保存后的最新 `LaunchersResp`（界面直接拿它刷新，与 `handleEnvMapping` 同款）；
- GET：`EnvFile` 指向不存在的文件时 `env_missing=true`；存在时 `false`（**成对断言**，单独一条在字段常量化后照样绿）；
- **PUT 时客户端送来的 `env_missing` 被忽略**（送 `true` 存下去再 GET 回来仍按磁盘现状算）；
- **日志断言**：跑一次带命令的 PUT，断言日志里**不含命令原文**。这是反面断言，配一条正面断言（日志里有条数）；
- 跨机：`?machine=` 走 `forwardIfRequested`，沿既有测试形态。

**④入口指针**：`internal/agentd/env.go:287`（`handleEnvMapping`，逐条照抄的原型）、`internal/agentd/server.go:502`（路由注册处）、`internal/launcher/`（已完成）。

---

### B3 · 会话创建接线 + 能力位三处（d_controlplane · 逻辑型）

**①契约引用**：`CreatePtySessionReq.EnvFile` / `InitCommand`；env 叠加顺序（`sessionEnv()` 在前，启动项文件在后）；`EnvFile` 不存在 → **400 拒绝且不创建会话**；能力位四处投影链里的**三个写入端**。

**②意图与为什么**：`s.pty.Open(...)` 是单一调用点（[pty_api.go:125](internal/agentd/pty_api.go:125)），接线爆炸半径 = 1。

**叠加顺序不可颠倒**：`sessionEnv()` 里是 `os.Environ()` + `TERM` + `env_forward`，都是「这台机器的缺省环境」；用户选一份 env 文件恰恰是为了覆盖缺省。反过来叠会让选文件这个动作在最需要它的场景下失效。

**能力位三处**（漏一处 = 断掉的投影链，而控制台读的恰恰是最后一处）：
- `server.go:671` 一带：自报；
- `machines.go:103`：本机就地填；
- `machines.go:153`：远端 `fillFromStatus` 原样搬运（**包括 nil**）。

**③验收**：
- 四种组合（都不带 / 只带 env / 只带命令 / 都带）各一条；**「都不带 == 今天的行为」必须是一条独立断言**，它是兼容性的唯一守卫；
- `env_file` 不存在 → 400，且**没有任何会话被创建**（反向断言，配一条正面断言：合法文件时 `s.pty.List()` 确实多了一个）；
- `env_file` 含路径分隔符 → 400，错误文本透传 `envfile.ErrBadName` 的中文原文；
- 叠加顺序：env 文件里定义一个 `sessionEnv()` 也会给的变量（如 `TERM`），断言最终环境里是**文件里那个值**；
- 能力位：`/api/status`、本机 `Machine`、远端 `Machine` 三处各一条断言；**远端那条必须覆盖「对端没上报 → nil」**（`fillFromStatus` 的既有语义是原样搬运包括 nil）；
- **B1 未完成前不得把 `LaunchersSupported` 置 true**（契约 §3.1 第 2 点）——这条写成 DAG 依赖，不是验收项。

**④入口指针**：`internal/agentd/pty_api.go:42`（`sessionEnv`）、`:100`（handler 入口，第一行是 `forwardIfRequested`）、`:125`（`Open` 调用点）、`internal/agentd/machines.go:103,153`、`internal/agentd/server.go:671`、`internal/envfile`（`LoadFile` 已完成）。

---

### B4 · web：api client + 机器详情页管理块（d_web · 逻辑型）

**①契约引用**：TS `Launcher` / `LaunchersResp` / `LaunchersReq`；`env_missing` 是**必有键**不是可选（前端靠「缺键」与「false」的区别判断服务端认不认识它）。

**②意图与为什么**：管理入口与 `MachineEnv` / `MachineExecutor` / `MachineDiscipline` 并列成第四块（[MachineDetail.tsx:71](web/src/app/machines/MachineDetail.tsx:71)）——这是「配置属于机器」这条产品语言的物理表达。

新建 `MachineLaunchers.tsx`，形态照 `MachineEnv.tsx`（156 行，是本块的尺寸参照）。Env 文件下拉的数据源是既有的 `fetchEnv(machine)`。

**③验收**（逻辑型 → 机内闭环；webview 差异见 §6.3）：
- 表单校验（前端侧，与服务端同规则但**不替代**它）：名字空 / 两者都空 → 保存按钮给出实话，不发请求；
- `env_missing=true` 的条目在列表里有可见标注；
- 保存后用返回的 `LaunchersResp` 刷新（不本地乐观更新——服务端可能规整了数据）；
- 该机 `launchers_supported !== true` 时**整块不渲染**（不画一个存不进去的表单）；
- 契约夹具已在 contract 节点落地（`LaunchersResp` 进了 fixture + `contract.test.ts`），本卡**不重复造**。

**④入口指针**：`web/src/app/machines/MachineEnv.tsx`（形态原型）、`MachineDetail.tsx:71`、`web/src/api/client.ts:363`（`fetchEnv` 一族的写法）。

---

### B5 · web：工作台接入（d_web · 逻辑型）

**①契约引用**：TS `Launcher`；`CreatePtySessionReq.env_file` / `init_command`；`Machine.launchers_supported`（**nil 按不支持处置**）。

**②意图与为什么**：空白 tab 与 `+` 菜单在内置三项之下列出当前基准目录所在机器的启动项。两处必须用**同一份**判断——`pickItemsFor` 今天就是为此存在的（[BlankTab.tsx:59](web/src/app/workbench/BlankTab.tsx:59) 的注释：「两处分别写就会出现『面板里没有终端、+ 菜单里却有』」）。

三条既有纪律必须继承：
- **终端不可用时一并摘掉启动项，不置灰**（置灰是在承诺「以后能用」，用户会反复点它）；
- **home 基准下也展示**（`pickItemsFor` 今天在 home 只留终端；启动项就是终端，同类）；
- **不分配快捷键**（数量不定，分配规则会立刻变成一张要维护的表）。

`launchers_supported !== true` 的机器一律不展示——这是契约 §3.1 的第一道防线，也是本卡最容易写错的一行（写成 `!== false` 就把 nil 放行了，而那正是旧版远端 agentd 的取值）。

**③验收**：
- `pickItemsFor` 的纯函数测试：内置三项 + N 条启动项；`terminalUnavailable` 非空时**两者都摘掉**；home 基准下启动项保留；
- `launchers_supported` 三态各一条：`true` 展示 / `false` 不展示 / **`undefined` 不展示**（第三条是本卡的核心判据，且它与 `pty_supported` 的处置相反）；
- 点一条启动项 → `createPtySession` 收到带 `env_file` / `init_command` 的请求（断言请求体，不是断言 UI）；
- 空白 tab 与 `+` 菜单列出的启动项**完全一致**（同一份 `pickItemsFor` 的两个消费者）；
- Q3/Q4 拍定后各补一条断言。

**④入口指针**：`web/src/app/workbench/BlankTab.tsx:22,59`、`TabBar.tsx:84`、`WorkbenchPage.tsx:82,136`、`TerminalTab.tsx:59`（`ptyBase` —— 新字段要在这里汇合）。

---

### B6 · 真机验收 —— **本卡由协调者执行，不派发**

见 §7。**其中第 1 条要驱动一台旧版 agentd**，与执行者纪律的「不派发、不起新的 executor 进程」冲突，派出去等于没验。

---

## 4. 岔口详述

### 5.1 Q1 · 启动项怎么进 `PickKind`

`PickKind` 今天是三个字符串字面量的闭集（[BlankTab.tsx:20](web/src/app/workbench/BlankTab.tsx:20)），`pick` / `startFromEmpty` / `hotkeyOf` 三处都在 switch 它。

- **(a) 模板字面量类型**：`PickKind = 'terminal' | 'newfile' | 'tui' | \`launcher:${string}\``。改动最小，但它让「闭集」这个性质消失——`hotkeyOf` 的穷举、`pickItemsFor` 的过滤都要开始解析字符串前缀，而字符串里嵌数据是后面一切「名字里带冒号怎么办」的源头。
- **(b) 回调加第二参**：`onPick(kind, launcherName?)`。签名变松，调用方可以传一个 `'terminal'` 加一个名字，类型系统不拦。
- **(c) 另开一条回调**：`onPickLauncher(name: string)` 与 `onPick(kind)` 并列。多一条 prop 要穿过 BlankTab → TabBar → WorkbenchPage，但**两个轴保持正交**：内置种类是闭集，启动项是一张会长的列表，它们本来就不是同一种东西。

出稿者倾向 **(c)**。反方理由成立：三个组件各多一条 prop，是实打实的样板。

### 5.2 Q2 · 启动项列表从哪来

- **(a) BlankTab 挂载时按需拉**：最省状态；代价是每开一个空白 tab 一次请求，且跨机时要带 `machine` 参数——而 BlankTab 今天不认识「机器」这个概念（它只拿到 `BaseDir`，里面有 `machine` 字段，够用）。
- **(b) 挂进 Shell 层，与项目树同节奏刷新**（30s）：与既有数据流形态一致，BlankTab 变成纯展示。**注意不扩 `ProjectTreeResp`**（见 §2 的核对）——另发一次 `GET /api/launchers`，只是刷新节奏搭同一班车。
- **(c) 拉一次 + 手动刷新**：最省请求；代价是在机器详情页改完启动项，回到工作台看不到，得手动刷——而那正是用户改完之后第一个动作。

出稿者倾向 **(b)**。

### 5.3 Q3 · tab 标题

(a) 用启动项名字：用户配了「跑测试」，tab 上就写「跑测试」，这是他起这个名字的全部意义。
(b) 沿用「终端 N」：与内置终端一致，但那样启动项的名字只在选择菜单里出现一次，开出来就没了。

出稿者倾向 (a)。

### 5.4 Q4 · 要不要回显一行标记

(a) 不回显：spec 拍定的语义是「像人亲手敲进去一样」，回显一行 handoff 自己的标记会破坏这个错觉，而且那行文本会混进终端的滚动历史、被 `Ctrl-R` 搜到。
(b) 回显 `▶ <名字>`：出问题时能一眼看出「这个终端是启动项开的、跑的是这条命令」。

出稿者倾向 (a)。**注意它影响 B1 的一条验收**（「写入内容 = 命令原文 + `\n`，不多不少」），拍反了要同步改那一条。

---

## 6. 缺陷族对抗审查

按[实例化清单 §3](2026-08-21-handoff-instantiation-checklist.md)：通用五族 + 项目两条追加设问 + 第六族（webview / 平台差异）。

### 6.1 生命周期 / 状态机中断

**问**：命令写入途中 agentd 重启会怎样？孤儿资源谁回收？

**答**：`Engine` 的会话是**进程内内存态**——agentd 重启后 PTY 会话本就全部消失（这是既有行为，不是本次引入）。等待首字节的那个 goroutine 随进程一起没了，不留孤儿。

**一个真实的新窗口**：`Open` 已返回、会话已入表、但命令**还没写进去**的那 0~3 秒里，前端已经拿到 200 并开始 attach。此时用户可能已经在敲字——他敲的字与我们要写的命令会**交错**。

处置：**接受它，并把窗口压到最小**（首字节到达即写，通常远快于人手）。不做「写完再返回」——那会让创建会话的请求阻塞最多 3 秒，而 3 秒的空白 tab 比一次极小概率的交错更糟。

→ 并入 B1 验收（首字节路径必须远早于 3s）。**未验证**：真实 shell 从 exec 到首字节的实际延迟 → §7 第 4 条。

### 6.2 静默失败 / 误导报错

**问**：每条错误路径的传播契约？存在「报成功但没做」的窗口吗？

**答**：**存在，而且是本需求的头号风险**——见 6.6 第 1 条（旧版远端 agentd）。除它之外三条：

1. `env_file` 不存在 → **400 且不创建会话**（契约已写死）。这条是「失效引用不静默」的主体；
2. `launcher.Save` 校验失败 → 400，**且磁盘上不留文件**（已有测试锁住）；
3. `InitCommand` 写入 PTY 失败（fd 已关等）→ 只打 Warn，会话照常可用。**不回滚会话**：一个能用的终端比一个因为命令没送进去就被销毁的终端有用。

→ 并入 B1/B2/B3 验收。

### 6.3 跨平台假设 / webview 差异（项目第六族）

**问**：本改动哪些假设在其他平台不成立？

**答**：

- **`platform_other.go`（非 unix）**：`ptySupported = false`，`Open` 直接返回 `ErrNotSupported`——`InitCommand` 永远走不到。**无需为它实现**，但这条结论要写进 B1 验收（否则下一个人会以为是漏了）。
- **Windows 执行机**：PTY 在该平台不支持，启动项自然不出现（`pty_supported=false` 已经把终端入口摘掉了）。**无新增假设。**
- **webview**：本次前端只有表单、列表与文本渲染，**不碰剪贴板 / cookie / 拖放 / 下载**——[已实证的三类 webview 差异](2026-08-21-handoff-instantiation-checklist.md)均不适用。**结论：无，因为本次前端改动的表面不触及任何在 WKWebView 与 Chromium 之间有过实证差异的 API。**

### 6.4 假红 / 假绿测试

**问**：判据是不是中途副产物？反面断言配对了吗？

**答**：本需求的反面断言**密度异常高**，逐条都配了正面断言：

| 反面断言 | 配对的正面断言 |
|---|---|
| 都不带字段 → 行为与今天相同 | 带字段时环境/输入确实变了 |
| `env_file` 不存在 → 不创建会话 | 合法时确实创建了 |
| 日志里不含命令原文 | 日志里有条数 |
| `launchers_supported=undefined` → 不展示 | `=true` → 展示 |
| 校验失败 → 磁盘无文件 | 成功 → 读得回来 |

**最大的假绿风险不在这些，在 6.6 第 1 条**：旧版 agentd 的行为无法在本仓的测试里复现（要真的跑一个旧二进制），所以它**只能**靠真机项。任何在机内「验证了兼容性」的测试都是在验证一个我们自己编的旧版本。

### 6.5 门禁绕过

**问**：新增的写路径过没过权限门？

**答**：**新增了两条写路径**，逐条核：

1. `PUT /api/launchers` —— 与 `PUT /api/env/mapping` 同一道门（主令牌鉴权 + `forwardIfRequested` 的跨机透传）。**不新增门，也不绕过**。
2. `POST /api/pty/sessions` 的两个新字段 —— 这条路径本就等价于主令牌（`resolvePtyBase` 的注释写得很直白：「控制台会话在能力上等价于主令牌，终端里一条 `cd ~` 就出去了」）。**注入 env 与执行命令不扩大这条路径的能力上界**：能开终端的人本来就能 `source` 任何文件、跑任何命令。

**唯一真正的新增暴露**：`env_file` 让调用方能**指名读取** `<DataDir>/env/` 下任意文件的内容并注入。但 `resolvePath` 的纯文件名约束把范围锁死在那一个目录，而那个目录里的东西本来就是给 executor 注入用的、同一个令牌通过 `GET /api/env/file` 就能读到全文。**结论：不扩大能力上界。**

### 6.6 序列化边界（项目追加设问一）

**问**：新字段从产生到消费的每一处手写序列化/投影都列进文件清单并加断言了吗？

**答**：**这是本需求最高风险的一族**，两条链路：

**链路 A —— 能力位（四处投影，三处是手写赋值）**

| 环节 | 手写？ | 断言归属 |
|---|---|---|
| `s.pty.Supported()` → `StatusResp.LaunchersSupported`（`server.go:671`） | **是** | B3 |
| 本机 `Machine`（`machines.go:103`） | **是** | B3 |
| 远端 `fillFromStatus`（`machines.go:153`，原样搬运含 nil） | **是** | B3（必须覆盖 nil） |
| TS 两处 interface | 手写 | B4/B5 + 契约夹具 |

> **这条链路已经咬过一次**：本文档的契约稿初版只写了 `StatusResp` 一处，落 Ticket 0 时才发现先例是四处（见[契约文档 §7](2026-08-22-custom-launchers-contract.md) 的第二次订正）。**同一个形状在同一个需求里出现两次，说明它不是偶然。**

**链路 B —— 会话创建请求**

| 环节 | 手写？ | 断言归属 |
|---|---|---|
| 前端组请求体（`ptyBase` + 展开） | **是**，且**历史上已漏过一个字段**（`rel`） | B5 |
| `forwardIfRequested` 转发 | 否（原样字节） | — |
| `proto.CreatePtySessionReq` → `ptyhost.OpenOptions` | **是** | B3 |
| 契约夹具 | 已在 contract 节点落地（`CreatePtySessionReq` 进了 fixture） | — |

**头号风险（重复强调，因为它无法在机内验）**：`handleCreatePtySession` 第一行就 `forwardIfRequested`，请求体**原样转发**，而 `encoding/json` **默认忽略未知字段**。新版控制台 → 旧版远端 agentd = 正常起终端 + 返回 200 + **变量和命令悄悄消失**。前端的 `launchers_supported === true` 判断是唯一防线，而那条判断的正确性依赖链路 A 一处不漏。

### 6.7 枚举新值过既有白名单（项目追加设问二）

**问**：新引入的枚举取值流经的每一处既有校验器/白名单/switch 都登记了吗？

**答**：

- **本次不新增任何枚举取值**。`Launcher` 全是自由文本 + 一个 bool；`CreatePtySessionReq` 的两个新字段也是自由文本。
- **反方向的检查**：`PickKind`（TS 侧的闭集枚举）**会**被 Q1 触碰。选 (a) 模板字面量时，`hotkeyOf`（`BlankTab.tsx:44`）、`pick`（`WorkbenchPage.tsx:82`）、`startFromEmpty`（`:136`）三处 switch **都要确认新取值不会掉进意外分支**——`startFromEmpty` 今天的 `else` 分支是「开一个空白 tab」，一条没被识别的启动项会静默变成空白 tab。选 (c) 时这个风险不存在（两个轴正交）。**这是 Q1 倾向 (c) 的第二个理由，写进 §5.1 的补充。**
- `BaseKind`（`"workspace"` / `"home"`）：不新增取值，`resolvePtyBase` 的分支不受影响 ✅

### 6.8 凭据边界（本次特有，一并回答）

`Launcher.Command` 可能含凭据（`API_KEY=xxx some-cmd` 是常见写法）。三条：

1. **不进日志**（B2/B3 各一条断言）；
2. **进 GET 响应**——这是刻意的诚实边界，与 `GET /api/env/file`（含值全文，仅编辑时调用）同源：用户要在界面上编辑它；
3. **落盘权限 0600**（已在 Ticket 0 实现并测试）。

---

## 7. 真机清单（归协调者执行，不派发）

d_host 与 d_remote 都是**边界型**，接缝对面是 OS 与对端 agentd 版本。以下四条是行为事实：

1. **新版控制台 → 旧版远端 agentd 的实际行为**（承 6.6 头号风险）——用一台跑旧版 agentd 的机器，确认：(i) 它的 `Machine.launchers_supported` 确实是缺席而非 false；(ii) 前端确实不展示该机的启动项；(iii) 手工绕过前端直发带 `env_file` 的请求时，旧端确实静默忽略（**确认危害的形状，而不是假设它**）。
2. **命令在真实 login shell 里执行后会话继续存在**，且 Ctrl-C 只杀命令不杀会话。
3. **rc 链读 stdin 的真实发生率**（承契约 §3.2 的残余风险）——在自己的 zsh/bash 配置上验一次；若命中，症状是命令原文被 rc 当输入吃掉。
4. **真实 shell 从 exec 到首字节的延迟**（承 6.1 的交错窗口）——量一下，确认它远小于人手反应时间。若不是（例如某些 rc 要跑 1 秒才出提示符），交错窗口的处置要重议。

---

## 8. 交稿自检

1. **产出四样齐全** ✅ —— 子系统清单每个带类型（§1）、契约核对逐条有结论（§2）、6 张子卡四段式且判据行为化（§3）、缺陷族逐族有答含两条「无，因为……」（§6）
2. **「待拍板」岔口集中列在稿首** ✅ —— §0，四条，无一自批
3. **「未验证，需真机」已汇总** ✅ —— §7，四条
4. **每张子卡的有界文件集核过** ✅ —— 架构法第三条判据 2 在 `internal/agentd` 上命中，回答见 §1.1（能圈出，四个文件，**不插竖切卡**）

**红线遵守**：未写实现代码、未建卡、未派发、未调派发工具。
