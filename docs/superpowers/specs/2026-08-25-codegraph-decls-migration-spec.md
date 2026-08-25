# Spec：handoff 宿主侧——声明词表迁最优树 id + 宿主响应加 decls 段（C1.11）

> **状态：待用户批准（2026-08-25 出稿）**
> 级别/档位：**L3 轻档**（对接 C1.10 冻结的跨仓 wire 契约；宿主侧新增可选响应键，不改端点、不改鉴权）
> 来源：C1.10（查看器三期，charter 项目卡）contract 轮检查点的**跨仓拆分**，形态与 C1.4 → C1.7 那次同款。

## 问题陈述

C1.10 的契约第 36 条冻结了「迁移文件重写与 `ValidateDecls` 改动必须同一提交」。这条**不可满足**，因为两侧分属不同仓：

- `ValidateDecls` 在 **charter**：`graph/codegraph/decls.go`；
- 声明文件在 **handoff**：`codegraph/domains/*.json`。

机制上必然如此——`LoadDomainDecls(repoRoot)` 读的是 `repoRoot/codegraph/domains/*.json`（charter `graph/codegraph/decls.go:19`），即**被分析项目**的数据。charter 是工具，handoff 是被分析对象。一次派发跨不了两个仓。

原条款的**意图**（不允许出现「校验与数据互相矛盾」的窗口）在拆分后完整保住，机制是 handoff 钉 charter/graph 版本：charter 侧改动在 handoff 升版前对 handoff **不可见**，窗口由本卡的**单个提交**闭合。

## 现状读数（事实核查，2026-08-25，协调者本机实跑）

| 读数 | 值 | 出处 |
|---|---|---|
| best 词表域数 | **23** | `codegraph/best.json#domains` |
| 现有声明文件 | **2** 份 | `codegraph/domains/{d_coordination_task,d_workspace}.json` |
| `d_workspace` 是否 best id | **是**（顶层域，label「项目与工作区」） | `best.json#domains` |
| `d_coordination_task` 是否 best id | **否**；对应 best 域是 `d_orchestration`（label「任务编排」） | 同上 |
| 宿主是否已加载声明 | **是**，但只喂给 `Check` 算 `report`，响应无 `decls` 键 | `internal/agentd/codegraph.go:86` |
| 声明加载所在分支 | 嵌在 `best != nil` **且** target 加载成功之内 | `internal/agentd/codegraph.go:77~99` |
| 迁移后声明覆盖 | 仍是 **2/23**（文件数不变） | 推论 |

### `d_workspace.json`：键已经是对的

文件首行即 `"domain": "d_workspace"`，而 `d_workspace` 本就是 best 顶层域 id。所以 C1.10 契约第 29 条的「改为/**保持**」在本仓解析为**保持**——本文件**零改动**。其声明文本按第 29 条同样不机械改写。

### `d_coordination_task.json`：逐条已核过归属

按 C1.10 契约第 33/35 条「逐条检查守护代码是否属于 `d_orchestration`」，四条不变式与两组锚点的归属如下（容器 → best 域映射取自 `best.json#containers`）：

| 条目 | testRef / anchor | 守护代码 | best 域 | 去留 |
|---|---|---|---|---|
| 不变式①「状态只能沿 transitTable 登记的迁移边变化」 | `TestCanTransit` | `internal/proto/proto_test.go` 守 `internal/proto/proto.go` | **d_protocol** | **移出** |
| 不变式②「并发更新用旧状态做 CAS」 | `TestUpdateTaskStateCAS` | `internal/store/store_test.go` 守 `internal/store/store.go` | d_orchestration | 保留 |
| 不变式③「按工作目录统计活跃任务只纳入非终态」 | `TestActiveTasksByWorkDirOnlyNonTerminal` | 同上 | d_orchestration | 保留 |
| 不变式④「进入终态时挂起工单被作废并留审计痕迹」 | `TestTransitToTerminalVoidsPendingTickets` | `internal/agentd/ticketvoid_test.go` 守 `ticketvoid.go` | d_orchestration | 保留 |
| lifecycle `from`/`to` | `manager.go#Dispatch` / `manager.go#transit` | `internal/agentd/manager.go` | d_orchestration | 保留 |
| stateMachine 全部条目 | `internal/proto/proto.go#transitTable` | `internal/proto/proto.go` | **d_protocol** | **整段移出** |

即：**四条不变式留三条、lifecycle 整体保留、stateMachine 整段移出**。移出的两项（不变式①与整段 stateMachine）不得伪挂在新声明上（第 35 条），逐条落 `docs/roadmap.md`，记为「d_protocol 方向的声明欠账」。

## 方案（含弃选）

**单提交承载三件事**（顺序即依赖序）：

1. **升 charter/graph** 到 **`v0.8.0`**（C1.10 收尾时切出，2026-08-25 已推 GitHub，`go list -m github.com/Xsxdot/charter/graph@v0.8.0` 经 proxy 可解析）。desktop 模块**同提交** tidy——机制同 C1.7：`desktop/go.mod` 无直接 require charter/graph，靠 `replace github.com/Xsxdot/handoff => ../`（`desktop/go.mod:25`）经主模块间接解析，主模块一升 desktop 立刻落后。**必须先升根模块再 tidy**，否则 desktop 会按根模块旧值解回去（C1.7 plan 第 61 行实测同款陷阱）。
2. **声明迁移**：`d_coordination_task.json` → 单份 `d_orchestration.json`，按上表增删；`d_workspace.json` 不动。
3. **宿主响应加 `decls` 段**：把已加载的 `decls` 放进 response。

**为什么必须同一提交**：升版把 `ValidateDecls` 换成以 `best.Domains` 为主词表，此刻 `d_coordination_task` 立即非法；迁移与升版分开会留下一个 `codegraph validate` 必红的中间提交。这就是 C1.10 契约第 36 条实质要求的兑现点。

**弃选**：① 让 `ValidateDecls` 过渡期同时接受两套词表——那是补丁式修复，且用户已裁决 A（改成最优树 id），双词表会让「改成」变成「兼容」，永远收不了口。② 把 `d_coordination_task` 机械拆成七份子域声明——**声明是承诺，不能靠复制变出七份**（C1.10 契约第 30、37 条），覆盖读数宁可停在 2/23 也不生成空承诺。

## 契约语义与接缝（L3）

wire 侧**全部沿用 C1.10 契约第 2~11 条**，不新增语义。本 spec 只补一条 C1.10 契约漏掉的缺席情形：

- C1.10 契约第 7 条只写了「`codegraph/domains/` 目录缺席」一种缺席。实测宿主的声明加载嵌在 `best != nil` 且 target 加载成功之内（`codegraph.go:77~99`），所以实际有**三种**缺席：目录缺席 / best 缺席 / target 加载失败。三种都必须落到「响应不含 `decls` 键」的同一语义上，各配测试断言。
- 处置二选一，归实现轮裁量：把 `decls` 加载提到该分支之外（推荐，语义最干净），或保持嵌套但显式补齐另两种缺席的断言。**不许留成「读代码才知道」。**

接缝清单（每条写成「符号 + 调用方」）：

1. **`handleProjectCodegraph` 的响应装配**（`internal/agentd/codegraph.go#handleProjectCodegraph`，调用方是 `internal/agentd/server.go#registerRoutes` 注册的路由）——本卡主缝，`decls` 键的有/无与逐字段等同都在这里判。表驱动覆盖三种缺席 + 正常态。
2. **`codegraph validate` 的词表判据**（charter `graph/codegraph#ValidateDecls`，调用方 `graph/cli/cli.go`）——本仓侧的判据是「升版后跑 `codegraph validate` 得 0 issue」，属边界型接缝（对面是升版后的外部包），以命令输出为准。

## 用户故事

1. 作为架构维护者，我在控制台域页看到的职责/不变式来自 `codegraph/domains/`，且域 id 与最优树一致——点 `d_orchestration` 看到的是编排域的承诺，不再是旧词表的 `d_coordination_task`。
2. 作为维护者，我打开一个**没有**声明的域，看到的是声明空态与催稿位，不是传输失败也不是报错页。
3. 作为维护者，我跑 `codegraph validate` 得 0 issue；覆盖读数诚实地显示 **2/23**，不被空承诺注水。
4. 作为维护者，我升级 handoff 后 desktop 构建不掉队，CI 的「Windows 交叉编译门禁」与「install.sh 单测」照常执行而不是被前置失败置为 skipped。

## 实现决定

- `decls` 是 additive-only 的可选顶层键；宿主**不做领域映射、不补默认值、不改写字符串与数组顺序**（C1.10 契约第 4~6 条）。
- 迁移是**数据改写**，不是代码重构：除响应装配那几行外不动 `internal/agentd` 的其他逻辑。
- 被移出的不变式与 stateMachine 段落**删除并落 roadmap**，不注释掉、不留 TODO——注释掉的承诺仍会被读成承诺。

## 测试决定（接缝清单）

- **主缝**：`handleProjectCodegraph` 响应装配的表驱动测试——正常态（`decls` 与 `codegraph/domains/*.json` 逐字段等同）、三种缺席（各断言响应**无该键**而非 `null`）、单文件解析失败（断言告警并省略 `decls`，且整图响应仍为 200 而非 500）。
- **变异复验**：把「缺席时省略 `decls`」改成「缺席时发 `null`」，缺席那几支必须转红；把逐字段等同改成补一个默认值，正常态那支必须转红。
- **边界缝**：`codegraph validate` 升版后 0 issue，且**迁移前**在同一棵树上跑必须**非 0**——这条负向断言是「同一提交」的证据，缺了它无法证明中间态确实会红。
- 既有 `go test ./internal/agentd -count=1` 不红即宿主既有行为未破。

## Out of Scope

- **charter 侧的一切**：`ValidateDecls` 签名与判据、领域页纯函数与组件、级联抽屉、四个常量的钉值测试——全在 C1.10，本卡只消费其产物（一个 tag）。
- **补齐其余 21 个域的声明**：覆盖读数停在 2/23 是本期的诚实结论，不做凑数。**本期不做、后续要做**，落 roadmap。
- **`d_protocol` 方向的声明**：承接从 `d_coordination_task` 移出的不变式①与整段 stateMachine。**本期不做、后续要做**，落 roadmap。
- **`d_workspace.json` 的文本重写**：键已正确，文本按 C1.10 契约第 29 条不机械改写。若其职责句确有跨域残留（如「工作台启动项配置」可能属 `d_policy`/`d_web_workbench`），**本期不做**，落 roadmap。
- **前端消费**：域页如何渲染 `decls` 归 C1.10；本卡只保证 wire 侧供得上。
- **发 tag / 重打包 / 重启 agentd**：协调者的真机收口动作，不进实现轮。

## 备注

- 依赖：**被 C1.10 阻塞**（账本已挂 `C1.10 blocks C1.11`）。**2026-08-25 前置已全部满足**：C1.10 与 C1.7 均已「已完成」，`graph/v0.8.0` 已推且 proxy 可解析——这与 C1.7 等 `graph/v0.7.0` 是同一条前置，验法也相同（`git ls-remote --tags origin` + `go list -m <mod>@<tag>`）。
- 与 C1.7 的关系：两张卡都升 charter/graph 并同提交 tidy desktop，但版本不同（C1.7 → v0.7.0，本卡 → v0.8.0）。**C1.7 必须先合**，否则本卡的升版会踩在一个已被覆盖的 desktop 账上。
