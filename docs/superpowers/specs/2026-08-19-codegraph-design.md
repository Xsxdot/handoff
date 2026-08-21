# 代码图（codegraph）设计

2026-08-19 · 经 brainstorm + 原型走查确认（形态基准：fork 副本 `prototypes/codegraph/`）
2026-08-21 · 形态定稿修订：主形态收敛为**领域图三级下钻**（领域全景 → 嵌套子领域 → 叶子领域树+图），
时序图等其余图型全部裁掉；schema 新增 domains 段（§3.1）。

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
- `domains`（2026-08-21 新增）：领域段，**扫描产出、人可在入库后改**。
  `{<领域id>: {label, kind, summary, desc, parent?}}`——`summary` 职责一句话、`desc` 内部
  逻辑介绍、`parent` 缺省为顶层领域（有 parent 即嵌套子领域，层级不限深）。containers
  增加 `domain` 字段，归属到**叶子领域**。领域怎么切、切几层是扫描 AI 的产出物：默认
  按包一层，大包（如 agentd）按职责再切子领域。原型阶段领域由容器包名推导 + 内置元信息
  演示，真实契约必须显式入库——领域间连线、对外接口表都从这份归属聚合而来。
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

## 5. 交互形态（已确认基准，2026-08-21 定稿）

单一主线**「领域图」**：领域全景 → 领域内部（可嵌套）→ 方法详情，三级下钻。
走查过程中的全局树+图、地铁图、时序图、调用树、自由节点图均为试验品，**不进真实功能**；
树+图形态保留下来，作为**叶子领域的内部视图**。

- **领域全景（默认视图）**：主图只有领域卡与领域间调用连线。
  - 领域卡：label + 角色 + 职责一句话 + 统计（类数/方法数/对外接口数/入口 已扫描:总数）
    + 差异徽标（+加/~改/-删）。布局为确定性力导向自由分布（椭圆斥力 + 调用边弹簧 +
    纵向重力，不用随机数——同一份数据每次打开位置一致），可拖、可一键重新布局。
  - 点领域卡 → 右详情：职责 / 内部逻辑介绍 / **对外开放接口**（被其他领域调用的方法，
    每个标注调用方领域，可点击下钻聚焦）/ 对外入口清单（CLI/HTTP/WS）/ 「进入领域内部」。
  - 领域间连线：线宽 = 调用处数，中点标签「N 处调用」；点线（或标签）→ 右详情逐对列出
    **谁调用了谁的接口**（caller → callee，带类归属与差异标，点任一端下钻聚焦）。
- **进入领域（下钻）**：
  - 有子领域 → 再出一层领域全景：本级子领域卡互连；本级之外的领域缩成**虚线占位卡**
    （保留调入/调出边，点击横跳过去），调用关系不因下钻丢失。层级不限深。
  - 叶子领域 → **树+图视图**：左树的根 = 本域已扫描入口 + 对外接口（被域外调用的方法）；
    中间竖向焦点子图（上游在上/焦点居中/下游在下，BFS 最短路分层，层内按邻居均位排序
    减少交叉）；右详情常显。链上撞到域外节点**不截断**：图中显示虚线卡（标「◇ xx 领域」）、
    树中显示 `↗ 方法 · xx 领域` 行——点击都横跳到对方叶子领域并把该方法设为焦点。
  - 面包屑 `领域全景 ▸ agentd ▸ core` 逐级返回，任一祖先可点。
- **树+图交互（叶子领域内）**：单击换焦点只看它的链；**⌘/Ctrl+单击 = 加入/移出焦点
  集合**，多焦点多源 BFS 取并集渲染（chips 逐个可移出）；层级下拉默认「上下 2 级」
  （1/2/3/全部）；空白拖动平移，ctrl/⌘+滚轮以光标为不动点缩放（0.3×–2.5×）；
  ◀ 后退 / 前进 ▶ 焦点历史（记录整个集合，新选择截断"前进"分支）。
- **方法/实体详情（右栏，全层级共用）**：职责 / 签名（modified 时新旧对照）/ 参数表 /
  返回 / 字段表（model）/ 关联测试（可展开片段）/ 被谁调用与调用了（跳转跨领域时自动
  横跳）/ 源码折叠区（按 file:line 实时读）。
- 视图切换：基准 / 各分支 / 各 plan 下拉，差异染色叠加（绿加 / 琥珀改 / 红虚线删），
  领域卡上带该领域的加/改/删计数徽标。未扫描入口不进图，在领域卡与详情里以计数呈现
  （「已扫描/总数」两个数一起给——只给总数会被读成全扫过了）。
  **「点徽标直接跳到改动处」留到二期**：它的正主是 plan 影响面视图（一次改动的节点集
  合作为一个整体来看），一期先不做半套。

## 6. agent 查询接口（一期一等公民）

`handoff graph` 子命令族，**直读 cwd 仓库的 `codegraph/*.json`**，输出 JSON：

- `handoff graph domains`：领域树（label / 职责 / 成员统计 / 对外接口数），agent 定位
  该从哪个领域下手时的第一跳。
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

- **一期（本 spec）**：schema 定稿（含 domains 段）、扫描配方 skill 化（领域切分入
  产出契约）、控制台**领域图**页（三级下钻）、`handoff graph` CLI、stale 检测。
  浏览与查询 only。
- **二期**：plan 集成（plan diff 的产出约定、影响面预览、审核回路联动）、MCP、
  更多语言/项目接入。

## 9. 测试策略

- schema 校验器（引用完整性：edges 两端存在、container 存在、diff 引用的节点存在）
  作为 CLI `graph validate` 子命令 + 单测。
- mergeView 合并逻辑、多源 BFS/深度截断、并集查询：纯函数单测。
- stale 检测：构造 file:line 漂移的仓库夹具。
- UI 按 base 原型对照验收（`prototypes/base/README.md` 的确认基准行）。
