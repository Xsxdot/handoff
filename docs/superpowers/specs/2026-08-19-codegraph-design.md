# 代码图（codegraph）设计

2026-08-19 · 经 brainstorm + 原型走查确认（形态基准：fork 副本 `prototypes/codegraph/`）

## 1. 问题与目标

后端调用关系（入口 → 各层方法 → 数据实体）目前只存在于代码本身。三个消费场景都在为此反复付费：

1. **人写后端代码**：想快速看清某条链路的来龙去脉，只能逐文件追。
2. **写 plan / 审 plan**：预估一次改动的影响面（改/加/删波及哪些入口和下游），靠人脑追链，易漏。
3. **coding agent**：回答"这个方法谁在调、调用链到哪"要 5~10 轮 Grep/Read、几十 KB 原文进上下文；反向查调用方（grep 方法名）假阳性多且看不出传递性影响。

目标：把调用图沉淀为**入库的结构化数据**，人经控制台浏览，agent 经 CLI 查询。一期只做浏览与查询；plan 集成（影响面预览）留二期，但一期的数据与查询设计必须为它铺路。

## 2. 归属与边界（已拍板）

**在 handoff 仓库内实现，不建独立项目。** 理由：扫描编排依赖 handoff 派发 executor；二期 plan 集成的消费场景全在 handoff 协调者回路；独立项目的基础设施成本（守护进程、安装升级、项目注册）handoff 已有现成。

但按"可拆"的方式做，三层边界是**硬约束**：

| 层 | 落点 | 依赖 |
|----|------|------|
| 数据契约 | `codegraph/*.json`，落在**被扫描项目自己的仓库里**，随分支走 | 不依赖 handoff——任何工具可读 |
| agent 查询 | `handoff graph` CLI 子命令 | **直读本地仓库 JSON，不经 agentd**，离线可用 |
| 人看的 UI | handoff Web 控制台新页（dock 图标 ⌘） | agentd（本来就托管控制台、知道项目注册表） |

将来若需独立（开源给非 handoff 用户），抽的只是后两层的壳，数据契约原地不动。

## 3. 数据模型

### 3.1 graph.json schema（已被真实扫描验证）

`codegraph/baseline.json`，顶层 `{meta, containers, nodes, edges, diffs}`：

- `meta`：`project / branch / commit / scannedAt / generator`。
- `containers`：分组盒子，`{label, kind, entry?}`。**容器粒度是 struct（类）一级**：
  Go 按方法 receiver 归容器（`pkg.Receiver`，从 signature 用
  `^func\s+\(\s*\w+\s+\*?([A-Za-z_][A-Za-z0-9_]*)\s*\)` 推导）；无 receiver 的
  自由函数归 `pkg（包级函数）`容器；model 归 `pkg 实体`容器。入口容器按类型分三个：
  CLI 命令 / HTTP API / 长连接（WS），带 `entry: true`。
- `nodes`：三种 kind。
  - `entry`：`name`（如 `handoff dispatch`、`POST /api/tasks`）、`file`、`line`、`summary`；
    未追踪调用链的标 `unscanned: true`。
  - `func`：`name / file / line / signature / params[[名,类型,说明]] / returns / summary /
    tests[{name, file, snippet}]`。**不存源码**——控制台与 CLI 按 `file:line` 实时读，
    这同时是保鲜校验的抓手（见 §6）。
  - `model`：`fields[[名,类型,说明]]`，用「创建/读取它的方法 → model」的边挂进图。
- `edges`：`[caller, callee]` 方法级调用对，两端必须存在于 nodes。
- 节点 id：入口 `e_<名>`、方法 `n_<名>`、model `m_<名>`，全局唯一。

### 3.2 基准 + 差异生命周期

- **基准**：`codegraph/baseline.json` 随 main 演进。
- **差异**：每个分支 / plan 只存一份 diff（`nodesAdded / nodesModified / nodesDeleted /
  edgesAdded / edgesDeleted`），不复制基准。
- **渲染时合并**：查看某分支/plan 视角 = 加载基准 + 叠加该 diff（`mergeView`），
  节点/边带 `status` 染色（绿加 / 琥珀改 / 红虚线删；modified 详情里新旧签名对照）。
- **分支合并时**：该分支的 diff 折进 baseline，diff 文件删除。挂在
  `finishing-a-development-branch` 收尾清单里执行。

**diff 即变更范围声明**：一个 plan 的 diff 本质是"一组节点 + 一组边"。这既是渲染素材，
也是二期 plan 影响面视图的输入——"看 plan 影响面" = "查该组节点的并集子图"（§5 的
并集查询就是为此设计的同一条路径）。

## 4. 数据生成：AI 扫描 + 入库维护

- 扫描 = handoff 派发任务（codex 类执行器），产出/更新 `codegraph/*.json` 并 commit。
  扫描计划模板沉淀为 skill/配方，核心纪律已验证有效：**所有入口必须全量盘点**
  （查不完的标 `unscanned`，宁缺毋滥）；`file:line/signature` 必须与真实代码一致；
  测试关联找不到就空数组，不编造；链路追到导出方法级，承重的未导出函数（如 RunE
  命令主函数）也入图。
- 增量维护：分支开发期间由该分支的执行者（或专门的增量扫描任务）维护 diff 文件。
- 真实基线：`codegraph-scan-json` 分支 commit `60b944f5`，102 入口（96 unscanned）、
  80 func、7 model、132 edges，六条链（dispatch/pull/continue/wait/status +
  POST /api/tasks）已验证 schema 与渲染全通。

## 5. 交互形态（已确认基准）

主视图**「树+图」**，辅助视角**时序图**。（走查过程中的地铁图/调用树/自由节点图为
试验品，不进真实功能。）

三栏：左树 320px 固定 + 中间画布自适应 + 右详情 340px 固定。

- **左树**：已扫描入口各一棵调用树，逐级展开钻取（展开状态保持），行内带差异标
  （加/改/删徽章）与测试数；循环引用显示 `↻` 截断。
- **中间画布（竖向焦点子图）**：只画焦点集合的邻域——上游（谁调用它）在上、焦点
  居中、下游（它调用谁）在下，按 BFS 最短路距离分层，层内按邻居平均位置排序减少
  交叉。**层级下拉默认「上下 2 级」**（1/2/3/全部），大图的远端层级默认不画。
- **焦点操作**：单击任意节点（树或图）= 换焦点只看它的链；**⌘/Ctrl+单击 = 加入/移出
  焦点集合**，多焦点时多源 BFS 取**并集**渲染（改 N 个方法看合并影响面、找共同汇流点），
  顶部 chips 逐个可移出；**◀ 后退 / 前进 ▶** 在焦点历史（记录整个集合）间穿梭，
  语义同浏览器历史（新选择截断"前进"分支）。
- **画布操控**：空白处拖动平移；滚轮/触控板双指平移；**ctrl/⌘+滚轮以光标为不动点
  缩放**（0.3×–2.5×，换焦点保留倍率）。换焦点自动把新焦点居中（入口贴顶不浪费上半屏）。
- **右详情（常显，跟随焦点）**：职责 / 签名（modified 时新旧对照）/ 参数表 / 返回 /
  字段表（model）/ 关联测试（可展开片段）/ 被谁调用与调用了（可跳转）/ 源码折叠区
  （按 file:line 实时读）。
- 未扫描入口收纳（"…未扫描入口 · N"），默认隐藏可开关。
- 视图切换：基准 / 各分支 / 各 plan 下拉，差异染色叠加。

## 6. agent 查询接口（一期一等公民）

`handoff graph` 子命令族，**直读 cwd 仓库的 `codegraph/*.json`**，输出 JSON：

- `handoff graph chain <入口|节点>`：该节点的下游链（含 file:line/签名/测试指针）。
- `handoff graph who-calls <节点> [<节点>...]`：上游调用方；多节点 = 并集（与 UI 的
  ⌘ 多选、plan diff 渲染同一条查询语义）。
- `--depth N` 与 UI 层级一致，默认收紧；`--view <分支|plan>` 叠加 diff。
- 未扫描区域在输出中显式标注——**"图里没有上游"和"没扫过"必须可区分**，防 agent
  据此写出漏影响面的 plan。
- MCP 接入二期再包一层，语义复用 CLI。

## 7. 保鲜机制

过期的图比没有图更糟（agent 信了就省了验证）。两道防线：

1. **廉价 stale 检测**：节点不存源码，校验时按 `file:line` 读真实文件比对签名，
   对不上即标 stale；CLI 输出与 UI 详情都要透出 stale 标记，提示回退 grep/重扫。
2. **流程钩子**：分支合并时折 diff 进基准（`finishing-a-development-branch` 清单项）；
   brainstorm/plan 前若基线过旧（commit 落后过多）提示重扫。

## 8. 分期

- **一期（本 spec）**：schema 定稿、扫描配方 skill 化、控制台"树+图 + 时序图"页、
  `handoff graph` CLI、stale 检测。浏览与查询 only。
- **二期**：plan 集成（plan diff 的产出约定、影响面预览、审核回路联动）、MCP、
  更多语言/项目接入。

## 9. 测试策略

- schema 校验器（引用完整性：edges 两端存在、container 存在、diff 引用的节点存在）
  作为 CLI `graph validate` 子命令 + 单测。
- mergeView 合并逻辑、多源 BFS/深度截断、并集查询：纯函数单测。
- stale 检测：构造 file:line 漂移的仓库夹具。
- UI 按 base 原型对照验收（`prototypes/base/README.md` 的确认基准行）。
