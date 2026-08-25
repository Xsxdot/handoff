# Roadmap（仓库级残余账本）

一行一条的有序期队列。每条注明来源（哪份 spec / 哪个缺口）。排序判据两行：
先骨架后血肉；每期以可真机验收为界。下一期开工 = 从这里取条目重走 spec 门。

推迟项只写在某份 spec 的 Out of Scope 里会成孤儿——所以「本期不做、后续要做」
的都要落到这里。「永不做」不落这里。

## 队列

1. **完工提交号落账**：账本今天不记任务的完工提交（全仓 `Commit` 只命中 `tx.Commit()`），
   于是「卡的工作分支现在指向哪个提交」答不出来。补上它之后，节点派发的起点可以传
   提交号而不是分支名，复用既有的 commit 解析路径（本地有就不拉），比分支名形态更
   可靠。来源：`specs/2026-08-23-b192-node-base-continuation.md` 的弃选方案 D。
2. **派发时自动 push 工作分支到 origin**：能让 charter 流的节点跨机接续（今天跨机
   一律拒发并指路）。是外部可见的写动作，且与合并环节的 push 语义重叠，需要单独
   定性。来源：同上 spec 的弃选方案 C 与 Out of Scope。
3. **派发前校验附件在目标基线上可达**：基线修对之后，这条从「主要修法」降级为
   安全网——抓「基线设了、但附件仍不在那条分支上」的剩余情形。先让基线对，再看
   还漏什么。来源：`specs/2026-08-23-card-baseline-at-worktree-creation.md`
   的弃选一与 Out of Scope。
4. **迁流时也能设卡基线**：`workflow migrate`（领取即跨流）处再开一个写入口。
   等「开工作树时挂卡」跑一段时间，看人是否真的会先迁流后建树再定。来源：同上
   spec 的弃选三。
5. **卡与工作树双向可见**：从工作树看「这棵树上挂着哪些卡」。上条 spec 本期只做
   单向（卡知道自己的基线）。来源：同上 spec 的 Out of Scope。

## 来自 C1.11 spec（2026-08-25，声明迁移欠账）

- **d_protocol invariant 欠账**：补回“任务状态只能沿 `transitTable` 登记的迁移边变化，completed 无后继而 failed 可重试回 running。”，守护测试为 `TestCanTransit`。该承诺从 `d_coordination_task` 迁出，本期不伪挂到 `d_orchestration`。
- **d_protocol stateMachine 欠账**：补回原声明中的 12 条协议迁移及其 `internal/proto/proto.go#transitTable` 锚点：
  `pending → running`、`pending → failed`、`running → waiting_answer`、`running → waiting_review`、`running → completed`、`running → failed`、`waiting_answer → running`、`waiting_answer → failed`、`waiting_review → running`、`waiting_review → completed`、`waiting_review → failed`、`failed → running`。本期不修改 `internal/proto/proto.go` 或 `internal/proto/proto_test.go`，也不把这些迁移伪挂到 `d_orchestration`。

## 来自 B207 spec（2026-08-23）

- **推平 breakdown / implement / integrate / contract / recon 五份纪律块的两机差异**。
  本机这五份各只落后权威源 1 行（尾部空行），linux-01 更旧；不构成已知行为缺陷，
  但同一角色名在两机解析出两份正文，且无处可查。来源：B207 spec 的 Out of Scope。
  排在**工作流纪律块重构**之后——重构会决定纪律块是否还以「每机一份磁盘副本」的
  形态存在；现在手工推平只是把病往后拖一轮。

## 来自 B207 spec 的划界（2026-08-23，归工作流纪律块重构）

- 任务契约层与执行器适配层的所有权、选择键、是否废除内置。
- 去掉 `Resolver.For` 在「配置里没这个 key」时的**静默**内置兜底——它是「内置
  plan-writing 从未被用上却四轮无人察觉」的根子。
- 纪律块与工作流同源同版本：今天工作流在账本里有不可变版本，它点名的正文在磁盘上
  没有版本，回答不了「某台机器上跑的是哪一版纪律」。
- 执行者往卡 timeline 写入的窄通道（只写 note、不碰任务不派发），并把它从「不许调
  handoff CLI」的禁令里豁免。今天台账文件不是设计选择，是禁令逼出来的唯一出口。
- 台账文件与卡 timeline 的职责边界；无卡的普通派发靠什么恢复现场。
6. **建树挂卡的故障注入验收**：目标 agentd/网络在 git 建树成功后、协调者逐卡写入前
   断开或重启时，树与卡的最终状态、响应可行动性、是否留下需人工清的手工树。
   B205 本期只做降级版（人工构造逐项失败验响应形状），不做真实注入。来源：
   `specs/2026-08-23-b205-breakdown.md` §4 第 3 条。
7. **Windows/非 Unix 目标上的建树行为复核**：git worktree 路径、分支占用、错误原文、
   `?machine` 取消行为，都不从 Linux fixture 推断。来源：同上 §4 第 4 条。
8. **WKWebView 下的建树弹层复核**：桌面端表单行为与混合结果展示。B205 本期只验真实
   浏览器那半。来源：同上 §4 第 6 条。
9. **卡基线是本地分支时，派发要求它已推到 origin**。B205 真机验收实测：建树产生的分支
   只在本地，从卡派发时按 origin 解析基线并 400——`基线分支 "acc/w1" 从远端 "origin"
   补拉失败（fatal: couldn't find remote ref acc/w1）`。报文可行动，push 后即通过，
   但「建树挂卡 → 后续派发从这个基线起」这条故事在「还没推分支」那一步是断的，
   要靠用户自己想到。可选方向：建树时提示未推、或派发前自动 push、或对同机派发走
   本地 ref 解析（B192 的 `LocalBaseBranch` 已有先例，但那是给节点续接用的）。
   来源：B205 真机验收发现 A（2026-08-23）。
10. **账本关闭时带 `card_ids` 的建树是静默丢弃**。响应既不含 `card_results`，也不含
    「你请求的挂卡没做」的任何信号，调用方无法区分「挂好了没回结果」与「被丢了」。
    B205 的判据只要求「不伪造 card_results」，故本期通过；前端也不受影响（卡列表 503，
    选择器不渲染）。要治的是 API 层的诚实性，不是 UI。来源：B205 真机验收发现 B。

## 来自 B203 spec（2026-08-23）

- **卡级/持久的执行器绑定**（「这张卡以后都用 grok」）：B203 只做「这一次」的一次性
  覆盖。真要做持久绑定，先回答「卡级值与工作流节点定义谁优先」——那是新的语义，
  不是 flag 的延伸。来源：`specs/2026-08-23-b203-dispatch-executor-model-override.md`
  Out of Scope。
- **执行器自报可用模型**：模型名今天无判据可校验（agentd 明写不认识任何执行器的模型
  名单），是「换执行器忘换模型 → 第一个事件 400」这族故障唯一挡不住的一半。要挡住，
  前提是 executor adapter 自己上报可用模型。来源：同上，弃选 A。

## 来自 B201 spec（2026-08-23）

- **一个节点产出多份文档**（如 contract 节点同时产契约文档与目标图 diff）：B201 本期
  单 kind 单路径，`produces` 扩成数组是向后兼容的改动，真有需要时再做。来源：
  `specs/2026-08-23-b201-node-output-attach.md` Out of Scope。

## 来自 B219 spec（2026-08-23）

- **让「写失败时降级」这族用例在任何身份下都真跑**（不靠 mode 位造前提，改用注入错误
  或天然失败）：B219 本期选的是「前提不成立就跳过」，覆盖靠非 root 的 CI 兜住。要真跑，
  得逐处重新设计造失败的手段，而 `cursordir` 那处的注释已明示它要的形状是
  「MkdirAll 成功而 CreateTemp 失败」——换成 ENOTDIR 会打到另一条代码路径。前置是给
  生产代码留可注入的写失败接缝。来源：`specs/2026-08-23-b219-permission-precondition-helper.md`
  弃选二。
- **执行机以非 root 用户跑测试**：能一次性关掉整族「负向权限断言在 root 下不成立」的
  问题（含未来新增用例），比逐条打守卫更根治。但它改的是执行机运行方式而非测试，牵涉
  用户、仓库权限、`GOCACHE`/`GOPATH` 落点，且 agentd 自身仍是 root、两者要共存。
  来源：同上，弃选三。

## 来自 B185 spec（2026-08-23）

- **任务层的终态事件丢失**：agentd 重启会丢掉 executor 的终态事件，任务冻在
  `running`（2026-08-23 实测一次冻了 76 分钟），而 `resume` 的对账补回对 codex
  不支持，只能 `--force`。B185 的卡环节恢复**依赖**这一层，但不修它——环节恢复
  重新 attach 时若任务侧终态确实丢了，仍然只能转等人。来源：
  `specs/2026-08-23-b185-step-runner-in-agentd.md` Out of Scope。
- **编排者活性探测**：B189（`15dfc51f8`）刻意废除了驱动租约的 TTL 过期语义与心跳
  协程，理由是「驱动方是有人在后面的会话，不是会静默死亡的进程」。B185 因此把
  恢复限定在「进程自己确知的死亡」（agentd 重启）。若将来要恢复覆盖到别的死法，
  **必须先推翻 B189 的裁决**，不能顺手加回 TTL。来源：同上，弃选 A。

## 来自 B230 spec（2026-08-24）

- **charter 三样东西的可复现落点**：现役 charter 执行流的工作流定义 v7（12 列 4 门）、
  `charter-default` 模板 v4、7 个 `charter-*` 纪律块，今天只活在一台机器的
  `~/.handoff/ledger.db` 与 `~/.handoff/discipline/`，没有任何可复现记录——换机/重装/
  新人接入都得靠记忆手搓。落点是 **charter 仓**（那里已是纪律块的单一真源
  `scripts/regen_discipline.py`），三样同源安装；**不是** handoff 的出厂种子（B230 已
  论证否决：那会把私人方法论焊进通用引擎）。**这是 B230 发现的真问题的另一半，
  project=charter，须另建卡。** 来源：`specs/2026-08-24-b230-factory-seed-teardown.md`
  问题陈述后半句。
- **卡的条件边（B230 卡里的④）**：定级判决落成卡级字段（`Card` 结构现无级别字段，
  `internal/ledger/types.go:98`）、门按级差异化、L1/L2/L3 跳边自动化（现靠人工 move）。
  与 B230 本期的「缺省值对齐」不同类风险——它改到派发主路径且是行为变更。前置是先有
  级别字段这个载体。来源：同上 Out of Scope。
- **领活池查询能力**：B230 决议「spec 完稿按级跳执行列」后，领活池从「可一眼扫的单一
  列」变成「执行列里未派发过的卡」（`dispatched` 事件存否）。现役活卡个位数时肉眼够用，
  规模上来要给 `card list` 加「未派发」筛选。来源：同上，「已定性列不做」一节的已知代价。
- **终态卡指向已删工作流**：B230 删掉 `bug`/`triage` 死行后，26 + 2 张终态卡的
  `workflow_name` 指向不存在的流。本期靠「终态卡不再 move」兜住（读卡路径不解析工作流），
  长期要么保留 tombstone 定义，要么在读取路径上显式容忍。来源：同上 Out of Scope。

## 来自 B156.1 spec（2026-08-24，一期收尾）

- **⑩双协调机对等 / ⑭基线分支并行两条判据的复活条件**：一期设计 §9 的这两条本期按
  「无实际使用场景」裁掉（读数：`mirror_lease` 从未换过 holder、镜像源只有 linux-01
  一台执行机；104/107 张卡 `base_branch` 为空）。**真出现第二台协调机指向同一账本库、
  或真开始用集成分支基线时，两条原样复活**，届时按 B156.1 spec 的 A/B/C 分类法重走。
  来源：`specs/2026-08-24-b156.1-phase1-closeout.md` 的 D 类与 Out of Scope。
- **「裁决解析失败」22 次**：B176 修过 codex 的 verdict 块提取，三天运行里仍发生 22 次，
  是 `needs_human` 的第二高频原因。与同期的「移列失败」不同类——那个是流定义没打开
  `produces` 开关（配置缺口，B156.1 本期修），这个要查 adapter 的提取逻辑。须另建卡。
  来源：同上 spec 的 Out of Scope。
- **工作台蓝图二/三/四期**：B156.2（协作层）、B156.3（自动化层）、B156.4（蓝图域）仍在
  「待办」，B156 父卡按 epic 规则「子卡全完成才能标完成」动不了。**一期收尾不等于蓝图
  收尾**。来源：同上 spec 的 Out of Scope。

## 来自 B156.1 执行期（2026-08-24，一期收尾实测）

- **charter v9 的自动挂端到端未验**：B238 那轮验的是 v8 的路径约定（带 `{{DATE}}`），
  三条判据全中；随后按 B201 的既有约定纠正成 v9（去 `{{DATE}}`、contract/breakdown 落
  `docs/superpowers/specs/`、breakdown 的 kind 由 `doc` 改 `breakdown`）后**没有再验一次**。
  同一条代码路径、只改了模板字符串，但按 B156.1 自己的标准（行为事实要真跑）这欠着。
  **下一张走 plan 节点的卡就是它的验证**——回来先看附件的 actor 是不是 `node:plan`。
- **判据⑥ 的 `--step` 并发认领未验**：裸 `card dispatch` 那条路径已红并落卡 B237
  （认领硬编码「进行中」）。`--step` 走 StepRunner 是另一条代码路径，本期没造过并发冲突。
- **判据⑤ 的三元组比对未验到**：`fake` executor 不产生 `task_mirrored` 事件，而三元组
  （source_target/source_task/source_seq）是镜像事件才有的字段。要完整验需要真 executor
  在 subtree wait 挂起期间跑一轮。旁证已有（B238 单卡 wait 收到过镜像事件；判据⑦ 的
  461 条零重复），但没有一次实验同时满足四个条件。
- **判据③ 的第二条要不要改**：原判据要求「blocker 终止后下游新增 needs_human 事件」，
  实现选的是派生视图（`internal/ledger/derived.go:106` 动态计算，不落事件）。功能等价
  且派生更合理（blocker 可 revive，落事件反而要撤销历史），但**本期未改判据去迁就实现**，
  按红记在卡上。判据是否改口径留用户裁决。
- **`deploy/workflows/charter-v4.json` 与账本 v9 已不一致**：那份文件的 README 仍写
  「待应用」，而其 breakdown kind（`doc`）与 implement 门（`require_attachment: plan`）
  都已被 v9 取代。归 C6（charter 定义的可复现落点）处理，本期不越界改。
- **linux-01 上的 `cards/B240.1-charter` 空分支**：判据①⑤ 的 fake 探针 stop 后保留了分支
  （stop 删 worktree、保留分支），当时无活着的 worktree 可作删除通道。下次在该机有活
  worktree 时顺手 `git branch -D`。
## 来自 B231 扫描补职责面（2026-08-24）

- **符号级漏建残余 33 个**：`cmd/`+`internal/` 范围 1993 个函数/方法声明里 33 个不在图中
  （1.7%），其中 6 个是导出符号：`internal/client/client.go#CardStep`、
  `internal/ledgerstep/runner.go#ResolveNode`、`internal/executor/turn/text.go#FinalText`、
  `internal/executor/turn/timing.go#PauseWaiting`/`Resume`、
  `internal/config/config.go#PlatformInvariantsEnabled`。本轮只把判据写进配方
  （「符号级完整性自检」），补建留给下一次重扫。来源：B231 协调者验收实测。
- **6 个零节点容器**：`k_codegraph_Target`/`k_codegraph_fn`/`k_codegraph_model`/
  `k_svc_Server`/`k_svc_model`/`k_web_model`，是 `internal/agentd/testdata/codegraph-repo`
  被 target 排除后留下的残骸，B231 之前就存在。`check` 只统计有节点的容器
  （`assignedContainers 233 = viewContainers 233`），所以它们对执法完全隐形。
  配方已加「零节点容器不入图」，清理随下一次重扫。来源：同上。
- **6 个非 card 族 CLI 父入口仍标 `unscanned`**：`e_cli_decision`/`graph`/`template`/
  `workflow`/`project`/`service`。B231 的禁用兜底只覆盖 card 族，这几个按 plan 允许保留；
  它们不参与预算重定标。来源：`plans/2026-08-24-b231-scan-plan.md` 本轮特别要求第 3 条。
- **终态事件丢失第二次实测**（叠加到「来自 B185 spec」那条）：2026-08-24 B231-2 重扫
  的 codex 任务在最后一次提交获批后静默 **76 分钟**（与 2026-08-23 那次同样是 76 分钟），
  executor 进程仍存活、`resume` 报「当前 executor 不支持会话对账」，最终靠
  `resume --force` 收口。同一形态第二次出现，不再是偶发。
- **基线合并即滞后（B228 的实测刻度）**：B231-2 扫描基准是 `4454c5cc`，合并进 main 的
  同一刻，main 已比它多出 35 个源码文件的改动（B156.1 那批，+587/−846）。也就是说
  「全量重扫」的新鲜度只在扫描那一刻成立，走完流程就已经旧了。这不是本刀的缺陷，
  是 B228（基线滞后主线）的量化证据：靠人工整轮重扫追主线在结构上追不上，
  出路是流程副产物保鲜（每张卡的视图 diff 随合并 absorb）而非周期性重扫。

## 来自 C1.9 查看器二期收尾（2026-08-24）

- **原型基准站 codegraph 页与真实前端在下钻形态上漂移**：二期形态确认走的是轻量 fork
  `prototypes/codegraph-phase2/pages/codegraph2.html`（294 行，只演示嵌套递归），
  而 `prototypes/base/pages/codegraph.html` 是 2916 行的一期真数据版。C1.9 合入后
  真实前端已是嵌套同构下钻，base 那页没跟上。直接拿 294 行的 mock 覆盖 2916 行的
  真数据页是降级，所以本次刻意不回灌，改记为债：下次动这页时按真实前端重镜像
  （或反过来，承认 base 只镜像首层全景、下钻形态以真实页面为准）。来源：C1.9 收尾
  基准回灌一步的裁决。

## 来自「修图」实测（2026-08-24，B233 前置排查）

- **容器粒度在制造假读数（已重定性进 B232，升高优先级）**：跨理想域边 1336 条里
  **780 条（58%）**有一端落在 ≥50 节点的大杂烩容器；把 `k_agentd_fn` 的 211 个包级函数
  按多数邻居域重判，**49 个压倒性属于 d_gateway**，重判后 `d_gateway→d_orchestration`
  **319→142（−55%）**。结论：主动脉直调债多到一半可能是粒度假象。**图信不过之前，
  按图排 B233 的迁移顺序等于按噪声排**——B232 是 B233 的前置而非附属。
- **阴性结果一（防重走）**：「类型与其方法分属不同域」全图只有 6 例（98 个同包配对里
  92 例正确，全部 6 例来自 `k_agentd_model`）。模拟修正后跨域边 1336→1336、双向环
  5 对不变——**线索听起来严重，实际不是杠杆**。
- **阴性结果二（防误改）**：`d_workspace` 的成员（Mirror / projectIndex /
  runOutputBuffer / DirtyWorktreeError）按耦合看像该在 orchestration，但 d_workspace
  的域声明原文就含「机器镜像发现」——**归属是对的，best.json 不该动**。
- **「按耦合找错归属」的探测器不能当裁判**：25 个「嫌疑」里多数是正常的契约/消费关系
  （如 `k_turn_fn` 属 d_execution_contract 却有 143 条边通向 d_execution_adapters——
  那正是契约包该有的样子）。探测器只配当候选生成器，判决要读代码 + 读域声明。
- **裸名匹配又咬了一次**：本轮第一版分析用裸类型名跨包配对，把 `codex.Client` 配到
  `internal/client` 的 `Client`，得出「18 例分裂」的假数；按（包目录, 类型名）重配后
  真值是 6。与 B173 的 106 条假边同一个坑，**任何跨包按名字配对的分析都要先带包路径**。

## 来自 B229 spec 的残余（2026-08-24）

- **③ 执行器附注层的实现**：本轮只定语义——机器级 `Discipline` 映射从「不点名时的兜底」
  改为「任何路径都追加的执行器附注」，空串 = 无附注。实测三台在役机器该映射**全空**
  （本机 `discipline: {}`，mac-02 与 linux-01 bindings 全 `mode:default`），从引入至今
  无人使用，无需求即不造。真需求（要专门调教某个 agent）出现时从这个已定语义长出来，
  不另起概念。来源：B229 spec Out of Scope。
- **其余策略类配置的同步**：`Executor`（缺省执行者/模型）、`Approver`、`StallTimeout`、
  `Sync.Auto`、`Terminal.Auto`、`PlatformInvariants` 同属「该同步而今天没同步」，但每项
  都要单独回答「机器级覆盖怎么表达」。B229 只做纪律块。来源：同上。
- **工作流引用的其余名字仍不同步**：`executor` 名与 `target`（机器）名今天指向每机
  config.yaml。工作流形状在账本里全网一致，这两个名字在不同机器上仍可指向不同的东西
  ——「工作流完整同步」B229 之后仍只完成了纪律块那一半。来源：同上。
- **`launchers.json` 的同步定性未判**：其 `Command` 可能含凭据（`internal/agentd/launchers_api.go:11`
  已为此立下「命令原文绝不进日志」的纪律），能不能同步、怎么同步需单独判。来源：同上。
- **linux-01 到账本库可达性未实测**：该机四个任务均已 completed 且 worktree 回收，
  `handoff run` 返 400，本轮没造探针。用户口头确认可达。B229 方案不依赖此事实
  （执行机不连库是采纳方案的性质之一），但下次在该机有活 worktree 时顺手验一次。
  来源：同上。

## 来自 B239 spec（2026-08-24，「认领」一分为二）

- **把节点编排搬到常驻机（笔记本退化为纯遥控器）**：编排今天长在协调者本机的 agentd
  里，合盖即挂起、重启即丢在飞节点。搬到常驻 agentd 能根治这一族，但牵扯控制台、
  鉴权、配置与部署形态，是另一个量级的活。来源：`specs/2026-08-24-b239-claim-lock-split.md`
  的 Out of Scope（用户在 spec 对话里点名问过"为什么跑在本机"）。
- **Web 控制台加驱动释放/接管入口与运行锁展示**：今天 Web 只读 `driver_session` 做展示
  （`web/src/app/cards/CardDrawer.tsx:369`），没有释放/接管入口，运行锁落账后也该在
  卡抽屉里显示「哪个节点正在哪台机器上跑、租期到几点」。来源：同上 spec 的 Out of Scope。
- **（已有卡，不重复取号）** 编排状态持久化可恢复 = **B225**；B239 已于 2026-08-25
  合入 main，它从「永久挡死」降级为「丢一轮」——但**各机 agentd 重新构建重启后才生效**。

## 来自 C1.7 finish 的基准回灌（2026-08-25）

- **两份已合分支的视图 diff 从没回灌**：`codegraph/diffs/cards-B192-charter.json` 与
  `cards-B205-charter.json` 的分支都已是 main 的祖先，diff 却还留在盘上——说明这两张卡
  的 finish 第 4 步（absorb）被跳过了。**不要顺手 absorb**：它们成文于 8-23，此后基线被
  C1.7 的回灌改过，内容是否仍与主线一致要先核；核过再 absorb，核不上按 recon 判据裁 fail
  查根因。来源：C1.7 finish 的基准回灌顺带发现。
- **C1.7 自己的图对账被跳过了一次**：分支删了十份文件、留了 42 个悬空符号在基线里，一路
  合进 main 没人拦。卡流的「图对账」列只在协调者记得点火时才走——**没有机械执法**。
  下一步值得让 finish 前的门检查「本分支删改过的文件是否在图里还有符号」。

## 来自 B229 部署前置的实测（2026-08-25，协调者本机核过）

- **在飞的 charter 任务跨不过一次 agentd 升级，只能重新 dispatch。**判据一行：
  `ls <DataDir>/tasks/<id>/discipline.md`，没有就跨不过去。链条三段都实测过：
  ① 旧 agentd **不写**这个文件——B229 前的 main（`8cb707294`）里 `manager.go` 对
  `disciplineFileName|discipline.md` 是 0 命中，`disciplineFileName` 随 `5585ecc2a`
  （b229.1）才进来；本机 `~/.handoff/tasks/*/` 抽查四个旧任务目录也都没有它。
  ② 旧 agentd **确实记了名字**——`discipline_name` 列早于 B229（`f4dc50057`），
  实测在飞任务 `fe509380` 的 `discipline_name` = `charter-contract`。
  ③ 新代码在「有名字 + 无正文」时**拒绝续接**（`internal/agentd/manager.go:1301-1313`），
  理由是冷恢复重建 executor 进程、纪律块是新进程里约束的唯一来源，空块会让一个
  点名 review 的任务失去「只读不写」。
  两条合起来：升级窗口一开，旧 agentd 派出的每个 charter 任务都落进拒绝分支。
  **注意这不只是「下一次点火失败」**——`continue` 在协议层不校验能力位，但跨重启
  会走 `resumeForContinue`，那条路才是被拒的地方。
- **可做的改进（本期不做）**：升级时把 `discipline_name` 已知、正文缺失的在飞任务
  按名字回填一份 `discipline.md`，让它们能跨过升级。风险是回填的是「升级后的最新版」
  而非首派那一版，与 B229 §2.5.2「续接必须看到与会话开始时同一份世界」冲突——
  要做得先想清楚这个矛盾，不是顺手补个文件。

## 来自 C1.11 finish 的残余（2026-08-25）

- **声明覆盖停在 2/23**：迁移后 `codegraph/domains/` 仍只有 `d_orchestration` 与 `d_workspace` 两份声明，而 best 有 23 个域。这是本期的**诚实结论**不是缺口遗忘——spec 明确拒绝为凑覆盖率生成空承诺。补齐其余 21 个域的声明是**本期不做、后续要做**，每份都要有真实的职责句、带测试锚的不变式，写不出来就别写。
- **`d_workspace.json` 的职责句可能有跨域残留**：键已是 best 顶层 id 所以本期零改动，但其职责文本里的「工作台启动项配置」一类表述可能实际属 `d_policy` / `d_web_workbench`。要动它得重走一次归属核对，不是文字润色。
- **C1.11 的两条真机项由 CI 结论兜底，未单独验**：(a) Windows/macOS 文件系统对 `codegraph/domains/` 存在性、权限、大小写的差异——本次只在 macOS 上实测；(b) CI runner 上 desktop tidy 门通过后，其后的 Windows 交叉编译、Windows vet、install.sh 三步是否**真的执行而非 skipped**。两条都会在本次合并推上去后由 CI 自然给出结论，届时若为 skipped 需回头处理。
- **前端消费 `decls` 未实现**：宿主已供上 wire，域页如何渲染声明归 C1.10 的后续，本卡不冒充完成。
