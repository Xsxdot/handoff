# codegraph 图工具批次 · 交接文档

> 日期：2026-08-22（同日修订：新增刀 0 搬迁裁决，主卡改归 charter 仓）
> 来源：charter 套件六项优化讨论（裁决记录见 charter 会话备忘 `skill-optimization-backlog`）
> 用途：作为在 **charter 仓**开 `charter:spec` 对话的输入（刀 0 的 spec 已于 2026-08-22 在 charter 仓开出）。本文只交接背景、目标与待拍板问题，不做设计定稿——语义裁决归用户，签名落地归 contract 节点。

---

## 一、背景：为什么有这个批次

charter 架构法（`~/workspace/charter/skills/architecture-law/SKILL.md` 术语节）已把两级术语定死：

- **子系统** = 并行协作与契约冻结的单元（L3 扇出的对象）；
- **领域** = 子系统内部按业务概念聚合的模块（domain/model 意义上的 domain）。

但法条自己注明了一笔欠账：*「目标图（codegraph/target.json）的 domains 数组承载的是子系统清单（schema 字段名暂不迁移，语义以本条为准）」*。也就是说，**术语已定稿，schema 还在用旧词**——`domains` 字段装的其实是子系统。

2026-08-22 的 charter 套件优化讨论对六个议题做了裁决，其中三项的落点都在 handoff 的 graph 工具链上（其余：MVC 去栈化已完成于 charter 提交 0022b9f；分期规划挂起等真 PRD 项目试点；gokit 拆分明确搁置）。三项分开做会对同一个 schema 动多次手，故打包为本批次：

1. **schema 术语迁移**（还上面那笔账）；
2. **领域图**（模型/domain 的职责、字段、生命周期成图）；
3. **图 diff 对账 + fitness 判据**（补 `graph check` 的两个盲区）。

## 二、现状读数（2026-08-22 实测，非转述）

以下全部为当日实际命令输出，不是印象：

1. **`graph entity` 已有「字段/投影」半边，没有「生命周期」半边。**
   在 handoff 仓跑 `handoff graph entity Task`，输出四段：
   - `model`：实体定义——文件/行（已再锚定）、字段清单（名/类型/注释）、摘要、领域归属（锚 `internal/proto/proto.go#Task`）；
   - `twins`：跨语言孪生——Go `proto.Task` 自动关联 TS 侧 `web/src/api/types.ts#Task`，两侧字段并列；
   - `typed`：类型化投影点（如 `internal/agentd/store.go` 的 `scanTaskRow`）；
   - `handroll`：手搭 wire 形状点（如 `internal/agentd/ledgerapi.go#unlinkedRowsFor`）。

   输出中**没有任何**「谁创建 / 谁写状态 / 谁终结」的分类段，也没有状态机。今天要看 Task 的生命周期只能手工组合：`who-calls` 打构造点 + `sym` 找 State 字段再 grep 写入点。

2. **同一个词「领域」在两层图里指两级东西。**
   - handoff 仓 `codegraph/baseline.json`：2229 节点 / 3642 边 / **19 个「领域」**，粒度接近真·领域（`d_coordination_task`、`d_web`、`d_coordination_api`……）；

     > **同日后续（08-22 晚，`charter:finish` 收尾）**：这份读数取自重扫前的基线。
     > 当天晚些做了一次全量重扫（提交 `f588ebe11`，锚点 `e228dc6e1`），基线变成
     > **3664 节点 / 4748 边 / 237 容器 / 19 子系统**——领域数没变，节点多了 64%
     > （旧基线漏扫了 `internal/launcher` 整个包等大片代码）。上面的粒度判断不受影响，
     > 但**规模数字请以新基线为准**。副作用：完整基线让 `graph check` 由绿转红
     > （69 条未声明跨域方向 + 17 条预算偏低），已记 backlog B173，与第 1 刀的
     > `domains`→`subsystems` 改名有重叠，两者需一并规划。

   - handoff-server 仓 `codegraph/target.json`：`domains` 数组 4 条（`d_tunnel`/`d_account`/`d_admin`/`d_console`，带 boundary/logic 类型与 `contracts` 依赖方向）——按架构法判据这些是**子系统**。

   迁移时两层图必须一起理，否则 `domains` 在同一个仓里有两个意思。

3. **`graph check` 只拦违法边，不查漏建。**
   check 的语义是「实际跨域边 ⊆ target.json 声明的契约面，违规即非零退出」。它回答「不许建的缝建了没有」，不回答「**说好要建的缝建成没有**」——contract 节点冻结的目标图增量（新缝、新入口）实现后没人逐条核对成真。这是 charter 流程终审侧的一个真缺口。

## 三、目标：刀 0 + 四刀，顺序即依赖

### 第 0 刀：搬迁（2026-08-22 已裁决，先于一切）

codegraph 从 handoff 剥离，落 **charter 仓嵌套 Go module**。裁决要点：

- **形态**：`charter/graph/`（module `github.com/Xsxdot/charter/graph`），`internal/codegraph` 整目录 + `cmd/graph.go` 搬入，二进制名 `codegraph`。可行性实证：该包零内部依赖、零第三方依赖、纯 stdlib（`internal/codegraph/types.go:9-11` 包注释明写「必须能原样搬进任何工具」），零 CGO、平台相关逻辑仅 3 行——「CLI 适配平台」经查不构成障碍。
- **为什么进 charter 仓而非独立仓**：四刀全是「法 + 工具」联动改（术语迁移同时动架构法与 schema），同仓才有原子提交，异仓每次联动都要跨仓同步；且 charter 已是每台机必装（插件 symlink），安装只多一条命令。
- **handoff 侧**：保留前端页面与 agentd 的 2 条只读 API（改 import 新 module，单向依赖 handoff → charter/graph）；`handoff graph` 保留为**薄别名**委托同一 module（标 deprecated）——凡装有 handoff 的机器零新增安装，存量 plan 不断。
- **分发三通道**（解「设备无 Go」问题）：有 Go → `go install`；无 Go 有 handoff → `handoff graph` 别名（handoff 预编译分发链本来就不需要目标机有 Go）；两者皆无 → charter 仓预编译六平台二进制 + install 脚本（`CGO_ENABLED=0` 交叉编译，模式照抄 handoff `install.sh` 简化版）。
- **顺序**：先搬家后动 schema——刀 1~4 直接落新家，省一轮跨仓同步。

### 第 1 刀：schema v2 术语迁移（地基，先行）

- `target.json` 的 `domains` → `subsystems`；`meta.version` 升 2；
- baseline/diff 视图侧的「领域」命名与两级术语对齐（子系统下挂领域，还是维持单级+归属标注，待 spec 裁决）；
- 出 `migrate` 子命令，存量仓（handoff、handoff-server 等）机械迁移。

**为什么先行**：后三刀全部建立在新 schema 上；且消费者越晚越多，迁移成本单调上升。这也是本批次存在的首要理由。

### 第 2 刀：领域图

每个领域（model/domain 意义）一份图，内容按「机械层/声明层」切开——**只声明代码看不出来的，能派生的绝不手抄**：

- **机械层（扫描器新增，进 entity 输出）**：创建点（返回该类型的构造函数/工厂）、状态写点（对 `.State` 类字段的写入位置）。静态分析拿得到，加 creator/writer 边或 entity 输出加段。
- **声明层（人工声明，配符号锚）**：职责一句话、不变式、生命周期（从哪创建到哪终结，锚用 `file#Symbol`）、状态机（合法迁移表）。`resolve`/`validate` 负责保鲜，坏锚即非零退出。

**为什么**：字段/投影半边已有（现状读数 1），缺的恰是语义半边；手抄字段表与行号锚同病（第一次加字段就漂），所以边界必须切在「机械可派生」线上。消费方四个：charter spec 的事实调查、plan 的序列化边界设问、debug 的「这个状态谁写的」、review/integrate 的对账。

### 第 3 刀：图 diff 对账（anti-漏建）

新增对账能力：contract 冻结时的目标图**增量**（新增子系统/缝/入口）vs 实现后实测图，逐条核对成真，漏建即非零退出。形态待拍板（新子命令如 `graph reconcile`，或 `check` 的一个模式）。

**为什么**：补现状读数 3 的缺口。charter 侧 integrate（重档）/acceptance（轻档）将新增法定对账步骤消费它——但 charter 修法在工具落地之后（见 Out of Scope）。

### 第 4 刀：fitness 判据进 check

把架构法里可机械化的判据从节点内人工步骤下沉为 `graph check` 常驻检查（演进式架构的 fitness function 思路）：

- 前缀家族 ≥ 5 个源文件（架构法第三条）；
- 单包源文件 ≥ 40 且无子包（第三条）；
- `legacyBudget` 不得增长（已有字段，缺执法）。

**为什么**：判据数字已在法条里，人工核对既漏又贵；check 已是雏形，方向是「判据进管道，人只裁决机器裁不了的」。

## 四、定级预判

**L3 预判**：codegraph 的 JSON 格式是 handoff CLI 与各消费仓之间的**跨仓 wire 契约**，动 schema 即动契约层（charter spec 定级两问的第二问直接命中）。最终定级以 spec 收尾定稿为准，此处仅预判，勿当判决。

## 五、Out of Scope（建议，spec 可改）

- **charter 修法**：architecture-law 术语节销账、integrate/acceptance 补图 diff 对账条款、spec/plan 补领域图引用——全部在工具落地**之后**配套跟进（同 MVC 去栈化的配套模式），避免法条引用不存在的能力。
- **各项目仓的 target.json 迁移执行**：migrate 命令交付后作为机械收尾，不占本批设计预算。
- **另行立项的理念**（讨论认可未立项）：consumer-driven contract tests（边界型接缝）、property-based testing（序列化边界 roundtrip）、feature flag + trunk-based（多期合流）。
- **分期规划**（product-backlog 补节）与 **gokit 拆分**：前者挂起等真 PRD 试点，后者用户明确搁置。

## 六、待拍板问题清单（spec 对话逐个裁决）

1. **两层图的命名终局**：`target.json` 用 `subsystems` 后，baseline 的 19 个细粒度分组叫什么、要不要挂到子系统下成两级树？
2. **声明层载体**：内嵌 `target.json`，还是 `codegraph/domains/*.json` 每领域一文件？（后者利于按卡增量维护）
3. **声明层要不要执法（不变式与状态机一并裁决）**：只做 resolve 保鲜（轻）；还是声明的每条不变式/状态机配一支能变红的断言（中——与 defect-families「承重安全属性有测试锁住」同源，把该设问从安全属性扩展到领域不变式）；还是 check 静态校验「状态写点 ∈ 声明的迁移表」（重，可能误报）？
4. **creator/writer 边的扫描成本**：全量重扫 vs 与现有增量扫描（`codegraph-scan-recipe.md`）的配合方式。
5. **版本兼容策略**：CLI 兼容读 v1 一段时间，还是 migrate 强制一次性升？（刀 0 后此条扩展为：handoff 对 `charter/graph` module 的版本对齐策略——嵌套 module 打 `graph/vX.Y.Z` tag，handoff go.mod 钉版本升级）
6. **对账形态**：`graph reconcile` 新子命令 vs `check --target-delta` 模式。
7. **本批次内部要不要再分档**：四刀一张 L3 卡走重档并行，还是刀 1+2 一卡、刀 3+4 一卡串行？

## 七、交棒方式

刀 0 的 spec 已在 **charter 仓**开出（`docs/specs/2026-08-22-codegraph-extraction-spec.md`）；刀 1~4 在刀 0 合并后于 **charter 仓**继续开 `charter:spec`，以本文档为输入；语义裁决归用户。实现阶段按派发决策表默认走 handoff 派发（codex 自举）；schema 迁移触及 wire 契约两侧时注意 handoff-server 同步（`internal/wire` 是共享契约的服务端侧）。

**图覆盖债**：本文引用的符号锚（`internal/proto/proto.go#Task`、`web/src/api/types.ts#Task`、`internal/agentd/ledgerapi.go#unlinkedRowsFor`）出自 2026-08-22 的 entity 查询输出，spec 开工时跑 `handoff graph resolve --doc docs/2026-08-22-codegraph-batch-handover.md` 复核。
