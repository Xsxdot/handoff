# codegraph 目标图与契约对照机制（graph check）设计

日期：2026-08-21
状态：已评审（brainstorm 定稿）
前置：`2026-08-19-codegraph-design.md`（基线+diff 查看器）、`2026-08-21-domain-partitioned-dev-protocol-design.md`（分域开发协议）

## 1. 背景与定位

分域开发协议要求：域卡圈得出有界文件集、契约面可冻结、域依赖可排 DAG、域类型有标注。对 handoff 自身 baseline 的实测表明，**事后扫描图**做不到这些（归属靠扫描者判断会漂、契约冻结停留在文档、宽接缝无人看守）。本机制把图的角色从「事后文档」翻转为「事前基准」：

- **目标图 `codegraph/target.json`**：Ticket 0 的产出物，人写的基准——域划分、域类型、允许的跨域契约面。
- **实际图 `codegraph/baseline.json` + `codegraph/diffs/<view>.json`**：扫描产出的客观事实，只含节点与边。
- **`handoff graph check`**：把事实机械套在基准上对照。契约冻结从此是可执行判据，不是文档里的一句话。

三个既有落地物各归其位：配方管「怎么扫」，baseline 管「实际是什么」，target 管「应该是什么」。

## 2. 关键决策记录

| 决策点 | 结论 | 弃选与理由 |
|---|---|---|
| 契约面声明粒度 | **入口级**：声明跨域调用只能进对方哪些容器/接口 | 方向级拦不住接缝变宽；函数级要预知全部签名，基准会被实现反向牵着走 |
| 存量宽接缝 | **legacy 预算棘轮**：存量直调计数设上限，只减不增，超限 fail | 第一天全拦会大面积报红逼人绕闸；只报不拦则永不收窄 |
| 域归属判定 | **目标图内路径规则，机械归域**：assignments 例外 > paths 规则 > 图外 | 扫描者自行归域会漂（workspace.go 归错域实证）；逐文件清单维护劝退 |
| 对照挂点 | **CLI + L3 域流模板**：域卡验收步骤与集成节点消费，不动 agentd | 纯 CLI 无消费闭环会腐烂；agentd 自动挂 review 回路留二期 |
| web 端范围 | **只做兼容**：查看器忽略 `kind`，不炸即可 | implements 折叠展开与对照可视化拆出去单独立卡（届时走原型先行） |

## 3. schema 变更（实际图侧）

- `edges[].kind: "call" | "implements"`，缺省 `"call"`。存量 baseline 不迁移，旧数据天然合法。
- `implements` 边方向：**实现 → 接口**。接口节点归**使用方**的域（Go 消费者侧接口惯例），实现节点归提供方的域。跨域 implements 边即「provider 实现了 consumer 的回调接口」，是接缝声明的一部分（见 §4 contracts.interfaces）。
- 组装点（main/module 的 wire 代码）在目标图 `assembly` 中标出，其出边豁免契约检查——绑定关系只存在于组装点，是依赖注入的本义。
- 同步改动：扫描配方增补「识别接口满足关系并产 implements 边」；`graph validate` 增加 kind 取值校验。

## 4. 目标图格式 `codegraph/target.json`

```json
{
  "meta": { "version": 1, "project": "handoff" },
  "domains": [
    { "id": "d_execution", "name": "执行运行", "type": "boundary",
      "paths": ["internal/executor/**"] },
    { "id": "d_coordination", "name": "协作控制", "type": "logic",
      "paths": ["internal/agentd/**", "cmd/**"], "note": "待竖切" }
  ],
  "assignments": [
    { "path": "internal/agentd/mirror.go", "domain": "d_workspace" }
  ],
  "assembly": ["cmd/main.go"],
  "contracts": [
    { "from": "d_coordination", "to": "d_runtime",
      "entries": ["config.Facade", "selfupdate.Updater"],
      "interfaces": [],
      "legacyBudget": 45 }
  ]
}
```

字段语义：

- `domains[].type: "logic" | "boundary"`——协议第四条要求的域类型标注落点。逻辑域接缝对面是自有代码，测试可闭环；边界域机内只验契约形状，行为验收走显式真机清单。域卡派发时据此分流验收方式。
- `domains[].paths`：glob 归域规则。**归域三级优先**：`assignments` 例外文件 > `paths` 规则 > 无匹配即「图外」。图外文件出现在扫描 diff 里时 warn，不静默——逼着目标图跟上现实。
- `contracts[]`：一条 = 一个允许的依赖方向 `from → to`。
  - `entries`：允许 call 边进入的 to 域容器清单，**按容器名匹配**（配方的规范形 `pkg.Receiver` / `pkg` 函数组），不按扫描生成的容器 id——目标图是人写的，名字稳定可读，id 是扫描产物不该被人引用。
  - `interfaces`：允许 to 域跨域实现的 from 域接口清单（回调契约面，implements 边方向与 call 相反，但同属这条接缝的声明）。
  - `legacyBudget`：该接缝上「不走声明入口的存量直调边」计数上限。超出 fail；缺省或 0 即无豁免全部硬拦。
- 域边界大体沿文件边界走是本机制的前提。路径规则切不开的包（如现状 `internal/agentd`）整包归一个域并 `note` 标「待竖切」——**写不出归属规则 = 圈不出有界文件集**，与协议的上下文预算检查同构，机制把该竖切的地方直接暴露出来。

## 5. `handoff graph check`

输入：`target.json` + `baseline.json` + `--view` 指定的分支 diff（无 diff 时纯 baseline，用于 main 全量体检）。复用 `internal/codegraph` 既有 load/merge 链合成当前视图。

流程：

1. **归域**：每个节点按其文件走三级优先归域。
2. **逐条检查跨域边**（caller 域 ≠ callee 域）：
   - caller 文件在 `assembly` → 豁免；
   - `kind=call`：callee 容器 ∈ 契约条目 `entries` → 合法；不在 entries 但该方向有契约条目 → 计入 legacy 命中；**该方向无契约条目 → fail（新依赖方向）**；
   - `kind=implements`：接口 ∈ 反向契约条目（`from=接口所在域, to=实现所在域`）的 `interfaces` → 合法，否则 fail。
3. **预算结算**：各接缝 legacy 命中数 > `legacyBudget` → fail；≤ 预算 → warn 并报当前值。
4. **输出**：fail 清单（新方向 / 越界 call / 越界 implements / 超预算）+ warn 清单（legacy 计数、图外文件、匹配不到任何存活文件的失效 paths 规则）。有 fail 退出码非零。

反静默约定（静默失败族对抗）：

- `target.json` 缺失或解析失败 → **硬报错退出，绝不静默通过**。
- paths 规则匹配不到任何存活文件 → warn（规则漂移信号）。

## 6. 分域协议集成

只改 L3 域流三模板的文本，不动 agentd 代码：

- **契约冻结模板**：产出物从「文档拍板」升级为「提交 `target.json` 的契约条目」——冻结即提交该文件。
- **拆解模板**：每张域卡必须引用 target 的域 id；验收步骤固定含「增量扫描触及文件产 diff → `graph check --view <分支视图>`，fail 即验收不过」。
- **集成模板**：整分支全量 check 作为终审判据之一。
- **契约变更流程**：改 `target.json` = 契约变更，走既有「合并决策留本地审核」纪律，不新增机制。

## 7. 图数据生命周期

| 文件 | 角色 | 寿命 |
|---|---|---|
| `target.json` | 基准（应该是什么） | 长命，只随契约变更演进 |
| `baseline.json` | main 的实际图快照 | 每次分支合并后更新，meta 钉 commit |
| `diffs/<view>.json` | 分支相对 baseline 的增量 | 短命，随分支生随分支灭 |

- **分支内**：executor 扫触及文件产出/更新 `diffs/<分支视图>.json`；check 读合成视图对照 target。归域一律走 target 路径规则，**不用** baseline.domains——diff 新增文件自动有归属（或图外 warn），merge 层「diff 不改领域划分」的既有约定与 check 互不干扰。查看器继续按 baseline.domains 渲染，二期展示增强时再统一坐标。
- **合并回 main（新增能力）**：`handoff graph absorb <view>`——diff 机械併入 baseline（added 加、modified 替换、deleted 剔除），刷新 meta.commit，删除该 diff 文件。挂点沿用 `prototypes/base/` 回流的同一钩子：`finishing-a-development-branch` 收尾清单加「本分支图 diff 是否 absorb 回 baseline」。baseline 保鲜靠流程副产物，不靠专项重扫；stale 检测兜底（file:line 锚点对不上会报）。
- **棘轮咬合**：重构收窄发生在分支上 → 该分支 check 报「命中 < 预算」→ 审核者在**同一分支**调低 `legacyBudget` 提交。预算调整与产生它的重构同变更集，审计自洽。

## 8. 测试与验收基调（细化归 plan）

- check 单测沿用 `internal/codegraph/testdata` 夹具仓模式，用例覆盖：合法边 / 越界 call / 新方向 / implements 两方向 / 预算刚好与超一 / 组装豁免 / 图外文件 / target 缺失硬报错 / 失效 paths 规则。
- `target.json` 解析是新的序列化边界，plan 阶段按四项分域检查加穿透测试（可空类型区分「字段缺失」与「零值」——`legacyBudget` 缺省=0=硬拦的语义尤其要钉住）。
- **终验收拿真数据**：给 handoff 自身写最小 target（六顶层域 + 现状契约条目 + 预算按实测填），跑 check 应报出已知宽接缝为 legacy 命中且无 fail——机制在真实仓库闭环。

## 9. 范围外（显式不做）

- web 端 implements 折叠展开、对照结果可视化（着色）——单独立卡，届时走原型先行。
- agentd 自动在 review 回路跑扫描+check——二期，待试点验证机制后再议。
- 竖切 `internal/agentd`——真实架构债，独立立项，届时正是用本机制+目标图来干的第一个大活。
- 重扫 baseline 补覆盖（前端入域、执行器适配器 implements 折叠收编）——机制落地后的数据工作。

## 10. 与协议试点（4️⃣）的关系

本机制的实现本身即分域协议 4️⃣ 试点候选：规模适中、跨 cmd/internal/codegraph/模板文本 三段、契约清晰。试点测量点（域卡平均上下文消耗、集成阶段新发现缺陷数）在其实施过程中采集；§8 的真数据终验收同时就是试点的测量现场。
