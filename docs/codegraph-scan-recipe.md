# 代码图扫描配方（派发给 AI executor 的 plan 模板）

## 用法

1. 复制本文档为一次性 plan，替换 <项目名>；
2. handoff dispatch --target <机器> --new-worktree --new-branch codegraph-scan-<日期> --executor codex <plan 文件>；
3. 回来后执行 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate` 通过才算扫描合格。

## 产物

仅新增或更新 codegraph/baseline.json（全量重扫）或
codegraph/diffs/<视图名>.json（分支增量），不改任何源码文件。

扫描写面只有上述图产物。`codegraph/target.json`、`codegraph/domains/*.json` 和
`codegraph/best.json` 都是人工维护的应然/归属声明，扫描期间一律只读，禁止创建、
覆盖或顺手补全。

基线文件描述扫描时刻的完整图；增量文件描述某个 branch/plan 相对基线的变化。
视图名使用文件名去掉 .json 的部分，消费方从 codegraph/diffs/ 发现视图，不读取
基线顶层的历史兼容字段。

## 扫描范围与完整性

全量基线以项目根 `codegraph/target.json` 的 `subsystems[].paths` 为准：匹配这些路径
的源码文件必须逐文件盘点，不能只扫描从 CLI/HTTP/WS 入口静态可达的符号，也不能
因为包被适配器、注册表或测试间接调用就跳过。target 中没有覆盖的源码目录可以
不入图，但必须在交付说明中列出。增量扫描同样要覆盖视图触及路径下的完整文件集。

对 Go 目录，按下文容器与节点规则盘点包级函数、receiver 方法、struct 和必要的
协议主路径私有函数；对 TypeScript/React 目录，至少盘点计划明确的类型、导出函数
和组件，容器用模块路径分组。静态可达性只用于补充调用边，不能用来决定节点是否
存在。

对每一个出现在节点 `file` 上的目录都建立 `packages` 条目：Go 包的 `summary` 只转录
该包源码 doc 注释，无注释则写空串；TypeScript/React 目录也必须建条目，但其
`summary` 固定为空串，不编造包职责。包目录键必须使用仓库内相对路径，并与节点
`file` 的目录部分同构。**转录只取注释正文，不带注释符**：Go 的 `//`
前缀、TS/JSDoc 的 `/**` 与 `*/` 一律剥掉再写入 `summary`（2026-08-24 实测：5 个 TS 节点
把 `/** … */` 整段抄进了 summary，消费方要么显示出注释符，要么各自再写一遍剥离逻辑）。类型与函数节点的 `summary` 同样从源码 doc 注释转录；有
doc 注释的 `model` 与 `func` 必须 100% 收录，不能以静态可达性或调用链为由跳过。

**文件级完整性自检（必做，B220 后新增）**：交付前必须逐目录比对「盘上有多少源码
文件」与「图里有多少文件出现在某个节点的 `file` 上」，**两个数字必须相等**，不等就
逐个列出差集并说明每个文件为何零节点（合法情形只有：纯常量/类型别名文件、
generated 文件、被 target 排除的目录）。

这条不是形式主义：**工具查不出漏建**。`stale` 是图→盘（拿图里的 file:line 去核实），
`check` 的 `outside-file` 只看已经在图里的文件，**没有任何判据是盘→图方向的**，所以
扫描没访问过的文件对 validate / check / stale 三者完全不可见，会静默至今。
2026-08-23 实测：`cmd/` 下 50 个源码文件里 9 个在图中零节点（其中 7 个在上次全量
扫描时就已存在），`handoff card` 族 24 个命令一个 entry 节点都没有——而 validate 全绿。
后果是下游的目标图 gap 数字少报约 18%。

**符号级完整性自检（必做，B231 后新增）**：文件级自检**罩不住漏建符号**——一个文件
只要有 1 个节点就通过文件级比对，哪怕它 90% 的声明都没入图。所以交付前还要做第二
道：对每个在图内的生产源码文件，比对「文件里的函数/方法声明数」与「图中该文件名
下的节点数」，差集逐条说明（合法情形只有：平台条件编译的同名实现、测试辅助包、
target 排除目录）。注意 receiver 方法在图里的 `name` 是 `Receiver.Method`，比对时要按
短名归一，否则会把整批方法误判成漏建。
2026-08-24 实测（B231 重扫后）：文件级自检通过的前提下，`cmd/`+`internal/` 范围内
1993 个声明仍有 33 个（1.7%）不在图中，其中 6 个是导出符号
（`client.CardStep`、`ledgerstep.ResolveNode`、`turn.FinalText` 等）。

**零节点容器不入图**：容器是「一组节点」的分组，成员为空的容器不是事实，只是上一轮
扫描的残骸。交付前清掉 `nodes` 为空的容器条目（2026-08-24 实测：6 个空容器
`k_codegraph_*`/`k_svc_*`/`k_web_model` 自 fixture 目录被 target 排除后一直挂着，
`check` 因只统计有节点的容器而完全看不见它们）。

## Schema（必须严格遵守）

字段名、JSON 类型和可选性必须与 codegraph canonical 家 charter 仓 `graph/codegraph/types.go`（github.com/Xsxdot/charter/graph，本仓 go.mod 钉版）一致。不得增加
未在下表出现的业务字段，也不得把源码正文塞进节点。

### codegraph/baseline.json

顶层对象字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| meta | Meta | 图的来源信息 |
| domains | Record&lt;string, Domain&gt; | 领域段，key 是领域 ID；可嵌套 |
| containers | Record<string, Container> | 分组盒子，key 是容器 ID |
| nodes | Record<string, Node> | 节点，key 是节点 ID |
| edges | [string, string][] | 调用关系 [caller, callee] |
| implements | [string, string][] | 接口满足关系 [实现节点 id, 接口节点 id] |
| projections | Projection[] | 数据实体投影关系；可选，缺省为空 |
| lifecycle | LifecycleRef[] | 生命周期关系 [creator/writer 节点对 model 的创建或状态写入] |
| packages | Record&lt;string, Package&gt; | 包目录到包 doc 摘要；可选，缺省时不得写入该键 |
| flows | Record&lt;承重函数节点 id, Flow&gt; | 承重函数的有序控制流；C12 additive-only。只覆盖承重函数，禁止给全部节点建 flow |

packages 的 value 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| summary | string | 否 | 包源码 doc 注释的一句话转录；无注释或 TS/React 目录为空串，禁止生成式概括 |

`packages` 的 key 必须是图中至少一个节点 `file` 的目录；悬空 key 会被
`codegraph validate` 判为硬错误。反过来，有目录没有条目不由 validate 执法，必须
由本配方的文件级完整性自检逐目录发现并说明。

meta 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| project | string | 项目名 |
| branch | string | 扫描分支 |
| commit | string | 扫描基准提交 |
| scannedAt | string | 扫描日期/时间 |
| generator | string | 扫描器或 executor 标识 |

domains 的 value 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| label | string | 否 | 领域展示名（如 agentd、store） |
| kind | string | 否 | 一两个词的角色说明（如 执行机守护进程、存储） |
| summary | string | 是 | 职责一句话，领域卡正面就显示它 |
| desc | string | 是 | 内部逻辑介绍，点开领域详情时读 |
| parent | string | 是 | 父领域 ID；缺省即顶层领域 |

containers 的 value 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| label | string | 否 | 展示名称 |
| kind | 受控八值 | 否 | 容器类别，见下节词表。未知值必须显式报错，禁止静默改写成「函数组」 |
| entry | boolean | 是 | 是否为入口容器，默认 false |
| domain | string | 是 | 所属领域 ID，必须是叶子领域；整图无 domains 段时可省 |

容器 `kind` 受控词表（八值，C12）：

```
类型方法 | 函数组 | 实体 | TypeScript 模型
React 组件/函数 | 入口 | TypeScript 函数组 | TypeScript 实体
```

「兜底桶」= `{函数组, TypeScript 函数组}` 两值。判据引用兜底桶时以词表为准，不以字符串前缀为准。词表外的 kind 是扫描错误，不是可降级的自由字符串。

nodes 的 value 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| kind | "entry" \| "func" \| "model" | 否 | 节点类别 |
| container | string | 否 | 所属容器 ID |
| order | integer | 是 | 容器/入口内稳定排序值 |
| name | string | 否 | 节点名称 |
| file | string | 否 | 仓库内相对文件路径 |
| line | integer | 否 | 1 基函数/类型定义行 |
| signature | string | 是 | 当前签名 |
| signatureOld | string | 是 | 仅 diff 的 nodesModified 使用，旧签名 |
| params | string[][] | 是 | 参数项 [名, 类型, 说明] |
| returns | string | 是 | 返回值描述 |
| summary | string | 是 | 职责摘要 |
| tests | TestRef[] | 是 | 直接关联测试 |
| fields | string[][] | 是 | model 专用字段 [名, 类型, 说明] |
| modelKind | "entity" \| "dto" \| "config" | 是 | **仅 kind=="model" 有意义**，判据见下节；空＝未分种 |
| channel | "cli" \| "http" \| "ws" \| "web" | 是 | **仅 kind=="entry" 有意义**（C12）。入口的对外通道，按注册面填写，禁止靠 id 前缀或名字形状猜完却不写字段。非 entry 不得带此键 |
| unscanned | boolean | 是 | 入口尚未追链时为 true，默认 false |
| projScanned | boolean | 是 | 该节点的投影关系已盘点过时为 true，默认 false |

`channel` 取值：Cobra/CLI 子命令 = `cli`；HTTP 路由/handler = `http`；WebSocket 端点 = `ws`；前端页面/壳入口 = `web`。每个 entry 必须有且仅有一个 channel；缺席等于没扫完。

tests 中每个 TestRef 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| name | string | 否 | 测试函数名 |
| file | string | 否 | 测试文件和测试函数的声明行号（func 行），如 pkg/x_test.go:41 |
| snippet | string | 是 | 测试片段 |

lifecycle 中每个 LifecycleRef 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| who | string | 否 | 真实创建点或状态写入点的节点 ID |
| model | string | 否 | 被创建或被写入的 model 节点 ID |
| kind | "creator" \| "writer" | 否 | 生命周期关系类型 |
| field | string | 是 | writer 真正写入的状态类字段名；creator 不填 |

Projection 是一个三元组 `[投影点节点 id, model 节点 id, kind]`：

| kind | 含义 |
| --- | --- |
| typed | 类型可见的投影（签名/字段里能看到该 model 类型） |
| handroll | 手搭 map/字面量拼装，类型系统不可见 |
| twin | 跨语言孪生的 model↔model 关系（如 Go struct ↔ TS interface） |

**跨语言关联一律走 projections 的 twin，不走 edges**（硬纪律里那条「跨语言禁止调用边」
的正面出口就是它）。

### flows（C12，承重函数的有序控制流）

`edges` 是无序二元组，表达「谁能调到谁」。`flows` 表达「这个函数内部按什么次序、在什么条件下走」——分支、循环、返回。两段不互相代替：有边无 flow 时查看器降级为机械可达序列并标明「无次序无分支」；禁止把 BFS 邻居序列写进 `flows.steps` 冒充流程图。

Flow 形状：

```
"flows": {
  "<承重函数节点 id>": {
    "steps": [ FlowStep, ... ]
  }
}
```

FlowStep 字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| id | 是 | 该函数内唯一（如 s1、s2） |
| order | 是 | 源码出现顺序，从 1 起 |
| kind | 是 | `call` / `branch` / `loop` / `return`，四值之外非法 |
| to | `call` 必填 | 被调节点 id，必须是已定义节点 |
| cond | `branch`/`loop` 必填 | **源码条件原文**，不做归一化 |
| line | 是 | 该步骤对应的源码行 |
| then / else | `branch` 必填 | 子步骤 id 列表 |
| body | `loop` 必填 | 循环体步骤 id 列表 |
| iface | 否 | 为真表示 `to` 是接口方法、本调用点是动态分派。实现清单从 `implements` 段 join，**禁止在 flows 里复制实现清单** |

**只给承重函数建 flow**，不要给全部节点建。承重函数 = 下列并集：

1. 所有 `kind=="entry"` 的节点（入口 handler；Cobra 分组命令零出边的仍建一条空 steps 或只有 return 的 flow，并在交付说明列出——不要省略键让查看器当「没扫到」）；
2. 入口的第一跳真实 handler（entry 的直接 callee，不含兜底桶噪声）；
3. 跨域入缝符号：被其他领域节点调用、且自身容器 kind **不是**兜底桶的 func/方法；
4. 编排单元：跨域出边 ≥ 3 的非兜底桶函数（一次调用里把几块域串起来的那种）。

兜底桶（`函数组` / `TypeScript 函数组`）且可被 ≥10 个程序入口到达的符号是噪声，**不建 flow**。拿不准就宁缺：缺席走查看器降级，假流程比没有更糟。

步骤纪律：

- `call` 的 `to` 必须已在 `nodes` 里；标准库/第三方调用不入 step（与边纪律相同）。
- 接口方法调用标 `iface: true`，`to` 写接口节点，不猜具体实现。
- `branch`/`loop` 的 `cond` 抄源码，不要改写成「如果失败」。
- 不要把测试、日志、纯格式化小函数展开成 step。
- 入口只建一跳是上轮的已知病（162 入口里 137 个出边=1）。本轮 entry 的 flow 与 edges 都必须沿 handler 往下走到编排层，不能在 `cmd.RunE → 一个函数` 处停住。

### codegraph/diffs/<视图名>.json

顶层对象字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| view | string | 否 | 分支/计划视图标签，如 branch:x |
| base | string | 是 | 相对的基线提交 |
| summary | string | 是 | 变化摘要 |
| containersAdded | Record<string, Container> | 是 | **本分支新建的容器**（新包/新类型）。没有它，分支上新建的入口容器进不了视图——`ValidateDiff` 会拒收引用未知容器的新节点。容器必须带 `domain` 且该领域**必须已存在于基线**（diff 不能新增领域，见下） |
| nodesAdded | Record<string, Node> | 是 | 新增节点的完整定义 |
| nodesModified | Record<string, Node> | 是 | 修改节点的完整新定义，可带 signatureOld |
| nodesDeleted | string[] | 是 | 被删除的基线节点 ID |
| edgesAdded | [string, string][] | 是 | 新增调用关系 |
| edgesDeleted | [string, string][] | 是 | 删除调用关系 |
| implementsAdded | [string, string][] | 是 | 新增接口满足关系 [实现节点 id, 接口节点 id] |
| implementsDeleted | [string, string][] | 是 | 删除接口满足关系 [实现节点 id, 接口节点 id] |
| projectionsAdded | Projection[] | 是 | 新增投影关系 |
| projectionsDeleted | Projection[] | 是 | 删除投影关系 |
| lifecycleAdded | LifecycleRef[] | 是 | 新增生命周期关系 |
| lifecycleDeleted | LifecycleRef[] | 是 | 删除生命周期关系 |

未发生变化的字段可以省略（消费方按空 map/空数组处理）。nodesModified 中必须
提供修改后的完整 Node，不要只写修改字段；删除的节点只写 ID，删除节点的旧定义
由基线提供。

### 摘要抓取红线

`packages.summary`、`model` 节点的 `summary` 和 `func` 节点的 `summary` 都是源码
doc 注释的事实转录（允许紧缩，不得改变原意），不是扫描器根据实现自行生成的职责
概括。无 doc 注释就写空串，不能从名称、字段、调用边或静态可达性推断摘要；生成式
概括是红线，因为它会让图里出现源码不存在的意图。对有 doc 注释的类型与函数，必须
逐项保留其摘要，收录率为 100%。

## model 分种（modelKind）

`kind: "model"` 今天把两样东西混在一起：真实体（有创建点、有状态被写）与传输/配置
结构。2026-08-23 实测本仓 707 个 model 里只有 53 个有生命周期——其余 654 个是 wire
类型、DTO、配置结构，它们把实体表、`codegraph entity` 查询与查看器的实体徽标全淹了。

**判据按优先级从上往下，命中即止**：

| 序 | 判据 | 结论 |
| --- | --- | --- |
| 1 | 该 model 在 `lifecycle` 段有 creator 或 writer | **entity** |
| 2 | proto/wire 生成物，或位于跨进程传输结构目录（如 `internal/proto/**`、`web/src/api/**`） | **dto** |
| 3 | 构造后只读、从配置文件或 env 装载 | **config** |
| 4 | 以上都不是 | **dto**（兜底） |

兜底选 dto 而不是留空，因为先验强烈：默认就是 DTO。**留空只用于「这一轮没判」**，
不是「判不出」——空值语义是「未分种」，消费方不会把它当实体，但也不会当 DTO。

三条会被 `codegraph validate` 判成硬 issue（自相矛盾类）：取值不在枚举内；
`modelKind` 挂在非 model 节点上；标了 `dto` 却在 `lifecycle` 段有 writer 条目。
**标了 `entity` 却没有 lifecycle 条目不报错**，只在 validate 的
`entitiesWithoutLifecycle` 里计数——那是补标进度表。

## 怎么切领域

领域是这张图的**主视图**：人先看领域全景（领域之间怎么调），再下钻到领域内部。
切得对不对直接决定这张图有没有用，所以它是扫描的一等产物，不是附属信息。

- **领域是职责，不是目录**。切分依据是「这块代码为谁解决什么问题」，不是「它放在
  哪个包」。包与领域是多对多：一个领域可以横跨多个包，一个大包也可以拆进多个领域
  （容器粒度是 struct，拆得开）。
- **层绝不能当领域**。controller / service / dao / model 这类分层目录，api / internal /
  pkg 这类可见性目录，各自成域就会得到一张「层与层之间互相调用」的图——那是架构模板，
  不是这个系统的样子。**同一件业务的 controller、service、dao 属于同一个领域。**
  这条是硬约束：按包切在分层项目里必然踩中它。
- **顶层按子系统分组，每个子系统一句话职责**。先问「这个系统由哪几块组成、各自
  对外承担什么」，得到 5~12 个顶层领域；再把实现它们的包/类作为子领域或容器挂下去。
  超过约 12 个顶层领域，全景就退化成包清单，失去了「先看领域之间怎么调」的意义。
- **子领域用来拆大领域内部的职责差异**：一个领域内部明显分层（对外接口层 / 业务核心 /
  适配层）或承担多件不相干的事时，用 parent 切子领域。层数不限，但**别为了切而切**——
  没有职责差异的拆分只会多一次点击。
- **只含一个容器的领域是信号**：多半该并进它的上级或邻居。整张图里这种领域占多数时，
  说明切分依据用错了（十有八九是按包切的）。
- **容器只能挂叶子领域**：挂在中间层的容器会静默从图里消失，`codegraph validate`
  会把它报成错误。
- **入口容器（CLI/HTTP/WS）挂到它服务的领域**上，不要单独成领域——入口是领域的对外
  门面，不是独立的一层。
- **summary 必填，且必须是真话**：一句话说清这个领域**为谁解决什么问题**。领域卡正面
  只显示这一句。「X 包提供其专门的基础能力」这类套模板的话等于没写——它是切分失败的
  证据，不是可接受的兜底。写不出一句话，就说明这块不该单独成域，并进别处。
- **desc 写内部逻辑**：这个领域内部怎么组织、有哪些关键类型、对外靠什么方式协作。
- **领域之间的连线不用手写**：消费方按跨领域的调用边自动聚合，只要 container.domain
  归属正确，连线与「对外开放接口」清单就是对的。
- **wire 类型不进业务领域**。proto/协议生成物是「跨进程契约」这一件事，不是任务、
  不是工作区、不是任何一块业务。把 111 个 proto model 挂进任务生命周期域，那个域的
  实体数会从 83 变成 194，而多出来的一百多个跟任务生命周期毫无关系——2026-08-23
  实测本仓就是这个状态。协议契约**自成一个领域**（`d_protocol`），它服务的是
  「谁和谁之间说什么话」，与业务领域平级。
- 接口类型建 model 节点；扫到 `var _ Iface = (*Impl)(nil)`、方法集满足或显式注入处，
  为每个实现产一条 implements 边；接口节点归**使用方**的容器/域，实现节点归提供方
  （消费者侧接口惯例，spec §3）。

### 扫描产出的是现状，不是应然

**`codegraph/target.json`、`codegraph/domains/*.json` 与 `codegraph/best.json` 都不是
扫描产出物，一个字都不要动。** target 记的是「架构应该长成什么样」，domains 记
职责/不变式/状态机锚，best 记人工确认的容器归属；baseline 才记「今天实际长成什么
样」。扫描者去「顺手补全」这些人工声明，等于把现状抄成应然或擅自改变归属，迁移
差异与善后审计都会失去意义。

扫描者能写的只有：`codegraph/baseline.json` 与 `codegraph/diffs/<视图>.json`。

## 生命周期产出纪律

生命周期关系单独放在 baseline 顶层 `lifecycle` 段，不能塞进 `edges` 或把普通调用边
改写成生命周期边。`who` 与 `model` 必须是图中已定义的节点，`model` 必须是 model 节点；
增量只在 diff 的 `lifecycleAdded` / `lifecycleDeleted` 中声明关系变化。

- **creator** 必须是“返回该 model 类型”的真实构造点：从函数返回类型、方法签名或明确的
  类型构造结果能证明它产出该 model，才记录 `kind: "creator"`。仅因函数名含
  `new`/`create`/`parse`、分配内存、填充 map，或调用链上看起来像初始化，就不能裸名撞库。
- **writer** 必须是对状态类字段的真实写入点：能在源码中确认对该 model 的状态字段发生赋值、
  更新或持久化写入，才记录 `kind: "writer"` 并填写实际 `field`。读取、比较、校验、广播
  事件或普通业务调用不是 writer。
- 必须先按 receiver 类型、返回类型和字段归属确认 `who`/`model` 对应的节点；同名函数、
  同名字段不是证据。定不出就宁缺毋滥，不产出该条关系。

## 硬纪律（历次扫描验证过的坑）

- **连边前必须确认 callee 就是被调的那个符号**（B173 假边事故后新增；08-22 全量重扫
  按裸名撞库产生了 106 条假边，如 `os.ReadFile` 被连到 `agentd.ReadFile`、TSX 本地
  `save()` 被连到 Go 未导出的 `config.save`）：
  - 带限定符的调用按限定符解析：`os.ReadFile`、`json.NewDecoder(...).Decode` 是标准库，
    标准库与第三方符号不入图、也不连边；
  - Go 跨包边必须满足「调用方包 import 了被调方包」；方法调用先定 receiver 类型再归属，
    定不出类型的方法调用**宁缺毋滥（不连边）**；
  - 同名不是证据：常用名（save/close/match/Decode/ReadFile）在多个包里都有定义，
    名字相同只说明撞名，不说明调用；
  - 跨语言（TS↔Go）禁止调用边：前端调后端走 HTTP，不是函数调用；wire 类型关联走
    projections/twins，不走 edges；
  - 收尾自检的 `codegraph validate` 含机械门控（跨包无 import / 跨语言的边直接
    非零退出），扫描产物过不了门控就是不合格。
- 容器按 struct 一级：Go 方法按 receiver 归 pkg.Receiver 容器，自由函数归
  pkg（包级函数），model 归 pkg 实体；入口分 CLI/HTTP/WS 三容器。
  **本轮不要按服务领域拆入口容器**（那会打乱 `best.json` 的容器归属）。入口
  属于哪个子系统改由 `channel` + 节点名/路径表达，不改容器拓扑。
- 所有入口必须全量盘点；没追链的标 unscanned: true——宁缺毋滥。
- **每个源码文件至少要有一个节点，或在交付说明里解释为何零节点**。工具查不出漏建
  （没有盘→图方向的判据），只有这条自检拦得住，见「扫描范围与完整性」。
- file/line/signature 必须与真实代码一致（line 指函数或类型定义行）。
- tests 找同包 *_test.go 里直接测到该函数的；找不到就 []，不编造。
- 链路追到导出方法级；承重的未导出函数（如 RunE 主函数）也入图；纯工具小函数不入。
- 节点的 file 必须是仓库内相对路径，不能写机器绝对路径；源码正文不进入 JSON。
- 边两端必须是已定义节点；节点的 container 必须是已定义容器。
- diff 的修改/删除只能引用基线已有节点；新增边的端点必须来自基线或同一 diff 的新增节点。
- 每个容器必须带 domain 且必须挂叶子领域；domains 段与容器归属要么全有、要么全无
  （半套数据会让消费方一半降级一半不降级）。
- 领域的 parent 必须指向已定义领域，且父链不能成环。
- lifecycle 的 `who`、`model` 必须引用已定义节点；`model` 必须为 model kind，`kind` 只能是
  creator 或 writer，writer 的 `field` 必须是被实际写入的状态类字段。定不出证据时不填关系。
- 收尾自检：python3 -m json.tool 验证 JSON 合法性 + 引用完整性脚本（或直接
  `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate`（零 issues），再执行
  `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . domains` 目视领域树是否符合真实架构，
  并抽查 5 个节点的 file:line。
- **C12 键自检（validate 罩不住）**：旧版 `codegraph validate` 会忽略未知键
  `flows`/`channel` 并照样全绿。交付前必须用下面这段核对 JSON 文件本身，不能只看
  validate 退出码：

  ```
  python3 - <<'PY'
  import json, sys
  g = json.load(open("codegraph/baseline.json"))
  flows = g.get("flows") or {}
  nodes = g["nodes"]
  entries = [(i, n) for i, n in nodes.items() if n.get("kind") == "entry"]
  missing_ch = [i for i, n in entries if not n.get("channel")]
  bad_ch = [i for i, n in entries if n.get("channel") not in (None, "cli", "http", "ws", "web")]
  dangling = [fid for fid, fl in flows.items() if fid not in nodes]
  print("flows", len(flows), "entries", len(entries),
        "missing_channel", len(missing_ch), "packages", len(g.get("packages") or {}))
  if not flows:
      sys.exit("FAIL: baseline 没有 flows（C12 本轮必产）")
  if missing_ch:
      sys.exit("FAIL: entry 缺 channel " + ",".join(missing_ch[:8]))
  if dangling:
      sys.exit("FAIL: flows 键不是已定义节点 " + ",".join(dangling[:8]))
  PY
  ```
