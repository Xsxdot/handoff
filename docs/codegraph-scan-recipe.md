# 代码图扫描配方（派发给 AI executor 的 plan 模板）

## 用法

1. 复制本文档为一次性 plan，替换 <项目名>；
2. handoff dispatch --target <机器> --new-worktree --new-branch codegraph-scan-<日期> --executor codex <plan 文件>；
3. 回来后 handoff graph validate --repo . 通过才算扫描合格。

## 产物

仅新增或更新 codegraph/baseline.json（全量重扫）或
codegraph/diffs/<视图名>.json（分支增量），不改任何源码文件。

基线文件描述扫描时刻的完整图；增量文件描述某个 branch/plan 相对基线的变化。
视图名使用文件名去掉 .json 的部分，消费方从 codegraph/diffs/ 发现视图，不读取
基线顶层的历史兼容字段。

## Schema（必须严格遵守）

字段名、JSON 类型和可选性必须与仓库 internal/codegraph/types.go 一致。不得增加
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
| kind | string | 否 | 容器类别 |
| entry | boolean | 是 | 是否为入口容器，默认 false |
| domain | string | 是 | 所属领域 ID，必须是叶子领域；整图无 domains 段时可省 |

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
| unscanned | boolean | 是 | 入口尚未追链时为 true，默认 false |

tests 中每个 TestRef 字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| name | string | 否 | 测试函数名 |
| file | string | 否 | 测试文件和测试函数的声明行号（func 行），如 pkg/x_test.go:41 |
| snippet | string | 是 | 测试片段 |

### codegraph/diffs/<视图名>.json

顶层对象字段：

| 字段 | 类型 | 可选 | 说明 |
| --- | --- | --- | --- |
| view | string | 否 | 分支/计划视图标签，如 branch:x |
| base | string | 是 | 相对的基线提交 |
| summary | string | 是 | 变化摘要 |
| nodesAdded | Record<string, Node> | 是 | 新增节点的完整定义 |
| nodesModified | Record<string, Node> | 是 | 修改节点的完整新定义，可带 signatureOld |
| nodesDeleted | string[] | 是 | 被删除的基线节点 ID |
| edgesAdded | [string, string][] | 是 | 新增调用关系 |
| edgesDeleted | [string, string][] | 是 | 删除调用关系 |
| implementsAdded | [string, string][] | 是 | 新增接口满足关系 [实现节点 id, 接口节点 id] |
| implementsDeleted | [string, string][] | 是 | 删除接口满足关系 [实现节点 id, 接口节点 id] |

未发生变化的字段可以省略（消费方按空 map/空数组处理）。nodesModified 中必须
提供修改后的完整 Node，不要只写修改字段；删除的节点只写 ID，删除节点的旧定义
由基线提供。

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
- **容器只能挂叶子领域**：挂在中间层的容器会静默从图里消失，`handoff graph validate`
  会把它报成错误。
- **入口容器（CLI/HTTP/WS）挂到它服务的领域**上，不要单独成领域——入口是领域的对外
  门面，不是独立的一层。
- **summary 必填，且必须是真话**：一句话说清这个领域**为谁解决什么问题**。领域卡正面
  只显示这一句。「X 包提供其专门的基础能力」这类套模板的话等于没写——它是切分失败的
  证据，不是可接受的兜底。写不出一句话，就说明这块不该单独成域，并进别处。
- **desc 写内部逻辑**：这个领域内部怎么组织、有哪些关键类型、对外靠什么方式协作。
- **领域之间的连线不用手写**：消费方按跨领域的调用边自动聚合，只要 container.domain
  归属正确，连线与「对外开放接口」清单就是对的。
- 接口类型建 model 节点；扫到 `var _ Iface = (*Impl)(nil)`、方法集满足或显式注入处，
  为每个实现产一条 implements 边；接口节点归**使用方**的容器/域，实现节点归提供方
  （消费者侧接口惯例，spec §3）。

## 硬纪律（历次扫描验证过的坑）

- 容器按 struct 一级：Go 方法按 receiver 归 pkg.Receiver 容器，自由函数归
  pkg（包级函数），model 归 pkg 实体；入口分 CLI/HTTP/WS 三容器。
- 所有入口必须全量盘点；没追链的标 unscanned: true——宁缺毋滥。
- file/line/signature 必须与真实代码一致（line 指函数或类型定义行）。
- tests 找同包 *_test.go 里直接测到该函数的；找不到就 []，不编造。
- 链路追到导出方法级；承重的未导出函数（如 RunE 主函数）也入图；纯工具小函数不入。
- 节点的 file 必须是仓库内相对路径，不能写机器绝对路径；源码正文不进入 JSON。
- 边两端必须是已定义节点；节点的 container 必须是已定义容器。
- diff 的修改/删除只能引用基线已有节点；新增边的端点必须来自基线或同一 diff 的新增节点。
- 每个容器必须带 domain 且必须挂叶子领域；domains 段与容器归属要么全有、要么全无
  （半套数据会让消费方一半降级一半不降级）。
- 领域的 parent 必须指向已定义领域，且父链不能成环。
- 收尾自检：python3 -m json.tool 验证 JSON 合法性 + 引用完整性脚本（或直接
  handoff graph validate --repo .（零 issues），再 handoff graph domains --repo . 目视领域树是否符合真实架构，
  并抽查 5 个节点的 file:line。
