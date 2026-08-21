# 纪律块具名化与工作台 A 组补全

> 状态：spec（2026-08-20 讨论定案）。上游：[一期 spec](2026-08-18-workbench-phase1-design.md)
> §6、[交接文档](../ledgers/2026-08-19-b156.1-handoff.md) A 组、[账本可选化](2026-08-20-ledger-optional-and-layering-design.md) §7
> 的「明确不做」。产出**两份 plan、分两次派发**：先纪律块重构（纯后端），再 A 组 UI。

## 1. 背景：一个根因，四个症状

交接文档 A 组列了三条 Web 待办（按环节派发按钮、验收开关、子任务树 rollup）。
落 spec 时按判据⑦逐条核命令面，牵出第四条——**纪律块被注入了两遍，而且审阅那次
是两份互相矛盾的纪律**。它与 A 组耦合：A 组要抽的共用编排包，搬的正是出问题的
那段代码。

### 1.1 双重注入（已真机验证）

`dispatchViaTemplate`（`cmd/card_dispatch.go:142-150`）读模板指向的纪律块文件、
拼在 prompt 最前面；agentd 在 `Dispatch`（`internal/agentd/manager.go:684`）里
又按 executor 名注入一份内置块，且**无条件**——`DispatchReq` 里没有「调用方已自带」
的字段。两份都进最终 prompt。

复现真实合成路径（模板块 + 分隔线 + 模板正文 → 当 planContent 下传 → adapter 调
`turn.RenderPrompt(taskID, planContent, disciplineBlock)`）实跑的结果：

```
最终 prompt 长度 = 4541 字节
出现「# 审阅纪律」次数 = 1
出现「# 执行纪律」次数 = 1
出现「只读，不写」次数 = 1
出现「每个 task 完成即 commit」次数 = 1
```

两句直接对撞：模板的 `block-review.md` 第 1 条写「只读，不写。不要 `git add`、
不要 `git commit`」，内置 `single-context.md` 第 12 条写「每个 task 完成即
commit」。`review-generic` 模板那条注释记的「2026-08-19 真机实测出现过一次」
审阅者真提交了东西——成因即此。

口径说明：喂给 `RenderPrompt` 的是真实路径会产生的那两个入参，不是从一次活派发
里抓的；中间「plan 空时 prompt 即任务内容」这一跳已核代码，审阅路径确实不带 plan。

### 1.2 同一份正文存了两遍

`docs/superpowers/discipline/block-a.md` 与内置 `builtin/subagent.md` 实测**只差
4 行**给人看的「适用执行器」提示，正文一字不差；`block-b.md` 与
`builtin/single-context.md` 同理。`block-review.md` 则没有内置对应物。

### 1.3 A 组三条（Web 层，spec §6 写了但没实现）

原型 `prototypes/workbench-ledger/pages/board.html` 里三条的形态当初都画过并确认过，
真实页面没实现。原型即本轮的形态验收基准，不新建原型。

| 条目 | 原型形态 | 真实页面现状 |
|---|---|---|
| 环节动作 | 四按钮：派发实现 / 派发审阅 / 合入集成分支 / 转移状态 | 只有「转移状态…」 |
| 子任务 | 独立区块，列**直接子卡**，每行 id + 标题 + 状态 badge | 无此区，子卡只在关系区以 `split_from` 边出现 |
| 验收 | chip 三态 + 「标记已验…」按钮 + 判据 + 证据 | chip 两态 + 判据 + 证据，无按钮 |

## 2. 裁决原则

- **纪律块是「这一轮执行者扮演什么角色」的指令集**。执行者本身不知道自己要干
  什么，全靠纪律块告诉它。因此点名的权力属于**派发方**——这是本次抽象的全部
  意义：让派发能个性化地指定角色。
- 旧配置按 executor 键，是因为当时没有别的轴可键。有了角色轴之后，executor 轴
  退化为「派发没点名时的兜底」，**语义不变、位置降级**。
- **能力轴仍然真实**：codex/grok 读到「派 subagent」会转而扮协调者（已有实测），
  所以 `implement` 这一个名字内部仍要按 executor 能力分档。角色轴与能力轴正交，
  不能合并成一条。
- **执行域动词与既有配置零破坏**：不点名的派发走今天完全相同的路径，现存
  `config.yaml` 一行不用改。

## 3. 纪律块具名化

### 3.1 内置块按角色命名

| 名字 | 正文 | 与 executor 的关系 |
|------|------|--------------------|
| `implement` | 按 `defaultTier` 落到 `builtin/subagent.md` 或 `builtin/single-context.md` | 有关：能力轴 |
| `review` | 新增 `builtin/review.md`，正文取自现 `docs/superpowers/discipline/block-review.md` | 无关：审阅就是审阅 |

`docs/superpowers/discipline/` 三个文件删除。`block-a/b.md` 里那 4 行「派错档的
代价」提示是给人看的，`~/.claude/CLAUDE.md` 与控制台「执行纪律」配置页都有，不留
第三份。

### 3.2 解析：两条路，互不干涉

```
派发点名了 name：
    DataDir/discipline/<name>.md 存在 → 用它
    否则 → 内置同名块（implement 再按 defaultTier 细分档位）
    既无文件、又无同名内置 → 拒发（"未知纪律块名字 <name>"）
    名字非法（含路径分隔符）/ 文件超限 / 文件存在但读不到 → 拒发
        （与今天 Resolver.For 的失败语义一致）

派发没点名：
    完全沿用今天的 Resolver.For(executor)，一行不改
    （机器级映射 → 显式空串关闭 → 按 executor 的内置默认）
```

`Source` 沿用现有两种前缀（`内置:` / `配置:`，见 `discipline.go:54` 与
`resolver.go:118`），不引入第三种。取值要能让人**一眼分辨走的是哪条路**——
这行 stderr 是审核取证要瞄的：

| 情形 | Source |
|---|---|
| 点名 + 文件覆盖 | `配置:<名字>` |
| 点名 + 内置（非 implement） | `内置:<名字>` |
| 点名 + 内置 implement | `内置:implement(<档位>)`，档位为 `subagent` 或 `single-context` |
| 不点名 | **一字不改**：`配置:<文件名>` 或 `内置:<档位>` |

`implement` 那行特意带上档位：只写 `内置:implement` 会把「派错档」这个历史上真出过
事的信息藏起来（codex 读到 subagent 版会转而扮协调者）。

**executor 轴的空串开关不作用于点名路径。**它是「这台机器给这个 executor 派任务时
不注入」的运维闸门，属 executor 轴；角色轴的点名是正确性需求（审阅必须只读），
两者不是同一件事。

覆盖机制不新增配置键：往 `DataDir/discipline/` 丢一个 `<名字>.md` 就是配置动作，
沿用现有的目录、文件名校验（含路径分隔符即拒）与大小上限。用户加自定义角色 =
丢一个 `bugfix.md`，派发时点名 `bugfix`。

### 3.3 `Resolver` 的接口增量

新增 `ByName(name string) (Block, error)`，与既有 `For(executor string)` 并列。
`For` 的实现与语义**一字不改**——它是兜底路径，改它等于改所有现存部署的行为。

### 3.4 名字必须持久化在 task 上（否则 continue/resume 会静默换块）

`m.discipline.For(execName)` 有三处调用点，后两处**只拿得到 execName**：

| 位置 | 函数 | 有没有派发上下文 |
|---|---|---|
| `manager.go:684` | `Dispatch` | 有 |
| `manager.go:1182` | `resumeForContinue` | **没有** |
| `manager.go:3229` | `ResumeTask` | **没有** |

若只在 `Dispatch` 处理点名，一次 `handoff continue` 或一次 agentd 重启后的
`ResumeTask`，审阅任务就会静默退回按 executor 的实现块——本 spec 要修的 bug 换个
方式复活，且更难查（首回合是对的）。

**所以 task 要持久化纪律块名字。**现有 `task.Discipline` 存的是 `discBlock.Source`
（人可读来源标注，如 `内置:single-context`），是展示用的，不能拿来重解析。新增
一个字段存名字（空 = 走 executor 兜底），三处调用点统一改成「有名字用名字、无名字
用 execName」。

### 3.5 `DispatchReq` 与模板

- `DispatchReq` 加 `Discipline string`（**名字**，不是路径也不是正文）。
- `TemplateDef.DisciplinePath` → `Discipline`（名字）。出厂模板：
  `feature-impl` → `implement`，`review-generic` → `review`。
- `cmd/card_dispatch.go` 删掉 `os.ReadFile(disciplinePath)` 与 prompt 拼接，
  改为把名字放进派发请求。`--discipline-override` 的语义从「覆盖路径」变成
  「覆盖名字」，帮助文案同步改。

### 3.6 老模板行不许静默降级

模板 def 存 JSON，`jsonUnmarshal`（`internal/ledger/workflows.go:145`）是宽松解码：
直接换字段名，老行会静默解成 `Discipline` 为空 → 退回 executor 兜底 → **审阅模板
悄悄拿到实现块**。

处置：`TemplateDef` 保留 `DisciplinePath string` 作废弃字段；读取时若 `Discipline`
为空而 `DisciplinePath` 非空，按 basename 去扩展名映射（`block-review.md` → `review`、
`block-a.md` → `implement`、`block-b.md` → `implement`），并打一行 Warn 注明该模板
用了废弃字段、建议 `template put` 重写。映射表只认这三个已知文件名，其他值映射为
空并 Warn——猜不出来就退回兜底，但**必须留声音**。

## 4. 共用编排包 `internal/ledgerstep`

> **2026-08-20 订正**：本节原写「新建 `internal/cardstep`」，那是写 spec 时没核实
> 基线。共用编排包**已经存在**——`internal/ledgerstep/verdict.go` 的包注释写明它是
> 「审阅/合并环节的**唯一实现**，主会话（经 CLI）与**看板按钮（经 Plan D API）
> 共用**」，注入点模式（`RunReview` / `Objective` / `DoMerge` 函数字段 + `wire.go`
> 生产装配）也已建好。再开一个包等于造第二个真相源，正是本节要避免的东西。
> 下文的 `internal/cardstep` 一律读作 `internal/ledgerstep`；形态与依赖注入的设计
> （4.2）与异步约束（4.3）不变。

### 4.1 为什么要抽

`cmd/card_node.go` 的头注释自己写着「看板动作按钮也应调用这一实现，保持单一编排
真相源」，但编排整个住在 `cmd/` 里，agentd 够不着。两处硬耦合：

- `os.ReadFile(disciplinePath)` 按 CWD 解析——CLI 在仓根才对（§3 之后此项消失）
- `dispatchTransportWithOpts` 是 CLI 的传输层

### 4.2 形态

把 `dispatchViaTemplate` 与 `runStepDispatch` 搬进 `internal/ledgerstep`，只收显式
依赖：账本 store、仓路径、派发传输函数、actor。调用方各自注入：

| 调用方 | 仓路径 | 传输 |
|---|---|---|
| CLI | `--repo`（缺省 CWD） | 现有 `dispatchTransportWithOpts` |
| agentd | 卡的 `Project` 解析到的项目登记路径 | agentd 自己的 client |

agentd 侧解析不到项目登记时**拒绝并说清**（「卡 B12 的项目 `foo` 未在本机登记，
先 `handoff project add`」），不猜路径。

### 4.3 异步是硬约束

审阅环节要等 task 跑到终态（几分钟到几十分钟），HTTP 请求扛不住。
`POST /api/cards/{id}/step` 立即返回 202，环节在 agentd 的 goroutine 里跑，界面靠
已有的卡事件流看进展（抽屉本来就会 reload）。

**同一张卡同时只允许一个环节在飞**，重复请求返回 409 并说明「B12 的 review 环节
正在运行」。在飞集合是 agentd 进程内状态：agentd 重启后集合清空，此时卡上留下的是
一次没有终态事件的环节——与今天 CLI 跑到一半被 Ctrl-C 的形态一致，人从 timeline
看得出来，本轮不做恢复。

## 5. A 组三条

### 5.1 A1 环节动作按钮

抽屉「环节动作」区变成三个按钮：`⇆ 派发审阅`、`⇣ 合入集成分支`、`→ 转移状态…`。

**不做「派发实现」。**交接文档这条叫「按**环节**派发」，实现派发不是环节，且它
通常要挂 plan 文件——浏览器里没有那个文件。实现派发留 CLI。原型画了四个按钮，
本条是对原型的**收窄**，理由写在此处备查。

按钮点击 → `POST /api/cards/{id}/step`，body `{"step":"review"|"merge"}`。
返回 202 后按钮进入禁用态并显示「已发起，进展见下方 Timeline」；409 时原地显示
冲突原因。

### 5.2 A2 子任务区

`handleCardDetail` 多返回 `children`：**直接子卡**（`parent = <id>`）的
`{id, title, status}` 列表，按 id 排序。不新开端点——抽屉「卡的一切只在一处看」
那条已经这么办了（`decisions`、`needs` 都是随详情给的）。

**需要新加一个 store 方法** `ChildrenOf(cardID) ([]CardBrief, error)`。仓里现有的
`SELECT id FROM cards WHERE parent_id = ?`（`internal/ledger/cards.go:85`）住在
`nextChildID` 里面、只为分配点号子位（B157 → B157.1），不是可复用的查询，别去改它。
`idx_cards_parent` 索引已存在（`store.go:198`），新方法直接受益。

抽屉在「关系」区之后加一区「子任务」，每行 id（可点击跳转，复用 `onOpenCard`）+
标题 + 状态 badge；`children` 为空时整区不渲染。

**只一层，不递归、不聚合**：这是原型确认过的形态，孙卡从子卡抽屉再往下点。
底层 `Store.Subtree`（`internal/ledger/events.go:275`）走的是全后代 + 并入成员，
本条**不用它**——它的语义（含并入成员）与「子任务」不是一回事。

顺带修掉一句会误导人的注释：`Subtree` 的文档注释写着「多路 wait 与**看板 rollup**
共用」，但全仓唯一调用方是 `cmd/card_wait.go:59`，看板 rollup 并不存在。那半句是
遗留的愿景描述，落 spec 时差点把「子任务区该用 Subtree」这个错误结论带进来。删掉
后半句，只留「多路 wait 用」。

### 5.3 A3 验收写入口

`POST /api/cards/{id}/accept`，body `{"evidence":"..."}`，转调已有的
`RecordAcceptance(id, true, evidence, actor)`。**已验必须带证据由后端守**（空证据
返回 400），与 CLI `card accept` 同源同规则；前端只是不让空提交。

验收区加「标记已验…」按钮 → 展开证据输入框（多行）+ 确认/取消。成功后 reload。

chip 改三态，与原型对齐：

| 条件 | 显示 |
|---|---|
| `verified == true` | 已验 |
| 未验且 `status == 已完成` | 待真机验 |
| 其余未验 | 未验 |

「标记未验」不做 UI，留 CLI 的 `--unverified`——它是补记动作，不是日常。

## 6. 明确不做

- **不做「派发实现」按钮**（§5.1 已述理由）。
- **不动 `Resolver.For(executor)` 的语义**——它是所有现存部署的兜底路径。
- **不给在飞环节做重启恢复**（§4.3）。
- **不做递归子树与聚合数字**（§5.2）。
- **不改 `RecordAcceptance` 的 store 语义**，只补 HTTP 入口。
- **不动 `card link` / 关系边**——`discovered_from` / `relates` 的 CLI 入口是
  B167，另立条目。
- **不合 main**：本 spec 的产出仍落在 `feat/b156-workbench-ledger` 上。

## 7. 验收判据

### 纪律块（第一份 plan）

① 派发一次 `review-generic` 模板的卡，最终 prompt 里「# 审阅纪律」出现 1 次、
   「# 执行纪律」出现 **0** 次；「只读，不写」1 次、「每个 task 完成即 commit」**0** 次。
   （对照：本 spec §1.1 记的基线是 1/1/1/1。）
② 不点名的普通 `handoff dispatch` 行为与改动前逐字一致：同一 executor 拿到同一份
   内置块，stderr 那行「纪律块: 内置:xxx」不变。
③ 机器级 `discipline: {grok: ""}` 时：不点名的派发不注入（现行为不变）；点名
   `review` 的派发**仍然注入** review 块。
④ 点名的任务经 `handoff continue` 与 agentd 重启后的 `ResumeTask`，重解析出的仍是
   同一个名字对应的块（不退回 executor 兜底）。这条要真机验，不能只看单测。
⑤ 老模板行（`discipline_path: "docs/superpowers/discipline/block-review.md"`、无
   `discipline` 字段）读取后解析到 `review`，且日志有一行 Warn。未知路径值映射为空
   并 Warn。
⑥ `DataDir/discipline/review.md` 存在时覆盖内置，Source 显示为文件来源。
⑦ `docs/superpowers/discipline/` 已删除，全仓 grep `block-a.md|block-b.md|block-review.md`
   无非文档命中。

### A 组（第二份 plan）

⑧ 抽屉「环节动作」区有且只有三个按钮；点「派发审阅」后 202、按钮禁用、Timeline
   出现派发事件；同卡再点返回 409 且原因可读。
⑨ 卡的项目未在本机登记时，点按钮得到明确错误（含「先 handoff project add」），
   不是 500 也不是静默失败。
⑩ 有子卡的卡：抽屉出现「子任务」区，行数 = 直接子卡数，点 id 能跳转；无子卡的卡
   整区不渲染。**孙卡不出现在父卡的该区里**（造一条 B→B.1→B.1.1 的链验，
   B 的该区应只有 1 行）。并入成员**也不出现**在该区（它有自己的「并入本卡」区）。
⑪ 「标记已验…」提交带证据 → `card show` 事件流出现 `acceptance_recorded`
   （verified=true），chip 变「已验」；证据留空提交返回 400 且不落事件。
⑫ chip 三态：新建卡显示「未验」、`已完成` 未验显示「待真机验」、已验显示「已验」。
⑬ CLI `card dispatch --step review|merge` 与按钮走**同一份编排代码**：
   `cmd/` 下不再有 `dispatchViaTemplate` / `runStepDispatch` 的第二份实现
   （grep 验证）。

### 全量门（两份 plan 各自都要过）

⑭ `gofmt -l` 无输出、`go build/vet/test ./...`、web `tsc --noEmit` 0 错 + vitest
   全绿。基线上本来就红的环境敏感项如实记账，不改无关模块。
