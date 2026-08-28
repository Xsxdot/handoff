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

## 来自 B229 部署前置的实测（2026-08-25，协调者本机核过；同日更正，见下）

> **更正（2026-08-25 晚）：本节原来的结论是错的，且错在最贵的方向——它会让人为升级去等一个根本不必等的窗口。**
> 原结论：「在飞的 charter 任务跨不过一次 agentd 升级，只能重新 dispatch。」
> **事实：一次 `systemctl restart` 不会让执行者消失，在飞任务照常跑完。**
> 打脸的经过：一个 peer 会话在 linux-01 上重启了 agentd，事后发现同机有一条 17:27:42 派出的
> charter 任务（`discipline_name=charter-implement`，任务目录确实没有 `discipline.md`）——
> 正是本节判定「跨不过去」的那一类。它没事：`render.log` 与 `frames.jsonl` 在重启后继续增长、
> `handoff resume` 报 `executor_gone: false`、shim 的 PPID 是 1 且启动时间早于新 agentd。
>
> **机制（本仓早就写着，我们没人去看）**：执行者由 agentd 经 shim 以**独立会话**拉起，
> 目的就是活过 agentd 的重启与升级；`deploy/handoff-agentd.service` 里 `KillMode=process`
> 的注释原文写着「是本项目的硬要求，不是可选优化」，并解释 systemd 默认的 `control-group`
> 会连坐执行者；`internal/agentd/killmode.go#WarnIfKillModeUnsafe` 还在启动时自检并打日志
> 「systemd KillMode 配置正确，agentd 重启不会连坐执行者」。重启后 agentd 按 `proc.json`
> + `proc.lock` 存活锁重新接管。**这是设计使然，不是侥幸。**

- **`discipline.md` 判据本身没错，错的是它的定义域**：它管的是**冷恢复**那条路。只有执行者
  **真的没了**（shim 死了、机器重启、unit 被改成 `KillMode=control-group`、或部署形态本就不
  保活）时，`Continue` 才会撞 `ErrTaskNotRunning` 落到 `resumeForContinue`，那时才需要读回
  首派落盘的纪律正文；有名字而无正文即拒绝续接（`internal/agentd/manager.go:1301-1313`）。
  **平常一次 `systemctl restart` 走不到那里。**
  判据仍然是一行 `ls <DataDir>/tasks/<id>/discipline.md`，只是它回答的问题变成了
  「**万一执行者真没了**，这个任务还能不能续接」。
- **两条前提必须一并记住，它们是条件不是背景**：① unit 里 `KillMode=process`；② 执行者经
  shim 以独立会话拉起。换一台机器、换一种部署形态，前提可能不成立。**macOS 上那两台是
  launchd，未验证**——不要把 linux 的结论推广过去。
- **升级窗口的真实条件**因此比原先宽得多：不需要「执行机上零在飞 charter 任务」。
- 旧 agentd 不写 `discipline.md` 这条实测依然成立（B229 前的 main `8cb707294` 里 `manager.go`
  对 `disciplineFileName|discipline.md` 0 命中，`disciplineFileName` 随 `5585ecc2a` 才进来；
  本机四个旧任务目录抽查也都没有它）。它只是不再蕴含原来那个结论。
- **这条链是怎么错的**：三个会话接力**推**出来的，每一环各自为真——旧 agentd 不写文件（实测）、
  新代码在有名无正文时拒绝续接（读代码）——缺的是没人去验的那一环：**重启到底会不会让
  executor 消失**。链条上每个环节为真，不代表结论为真。而正确答案本来就写在
  `deploy/handoff-agentd.service` 的注释里，我们谁都没去读那个文件。
- **可做的改进（本期不做）**：升级时把 `discipline_name` 已知、正文缺失的在飞任务按名字回填
  一份 `discipline.md`，让它们在**真冷恢复**时也能续接。风险是回填的是「升级后的最新版」而非
  首派那一版，与 B229 §2.5.2「续接必须看到与会话开始时同一份世界」冲突——要做得先想清楚
  这个矛盾，不是顺手补个文件。

## 来自 C1.11 finish 的残余（2026-08-25）

- **声明覆盖停在 2/23**：迁移后 `codegraph/domains/` 仍只有 `d_orchestration` 与 `d_workspace` 两份声明，而 best 有 23 个域。这是本期的**诚实结论**不是缺口遗忘——spec 明确拒绝为凑覆盖率生成空承诺。补齐其余 21 个域的声明是**本期不做、后续要做**，每份都要有真实的职责句、带测试锚的不变式，写不出来就别写。
- **`d_workspace.json` 的职责句可能有跨域残留**：键已是 best 顶层 id 所以本期零改动，但其职责文本里的「工作台启动项配置」一类表述可能实际属 `d_policy` / `d_web_workbench`。要动它得重走一次归属核对，不是文字润色。
- **C1.11 的两条真机项由 CI 结论兜底，未单独验**：(a) Windows/macOS 文件系统对 `codegraph/domains/` 存在性、权限、大小写的差异——本次只在 macOS 上实测；(b) CI runner 上 desktop tidy 门通过后，其后的 Windows 交叉编译、Windows vet、install.sh 三步是否**真的执行而非 skipped**。两条都会在本次合并推上去后由 CI 自然给出结论，届时若为 skipped 需回头处理。
- **前端消费 `decls` 未实现**：宿主已供上 wire，域页如何渲染声明归 C1.10 的后续，本卡不冒充完成。

## 来自 B227 spec 的残余（2026-08-25，协调者本机实测）

前置：B227 本期选定「扩 codex 可写域到 git 公共目录 + 堵住 agentd 侧的 hooks 触发」。
以下四条是它显式推迟的，逐条带前置条件与不做的理由。判据与原始读数在
`docs/superpowers/ledgers/2026-08-25-b227-spec-ledger.md`。

- **换用 codex 支持 deny 的权限声明面（`permissions`），把 hooks 与 config 从可写域里
  deny 出去。**这是唯一能同时做到「git 完全好用」与「关掉 hooks 逃逸链」的方案——
  实测 `permissions` 的 entries 支持 `read/write/deny` + priority，而现用的
  `sandboxPolicy` 面**表达不出 deny**（台账 §3）。**前置未知项：执行机上的 codex 版本**
  （本机是 0.147.0，linux-01 未查——本轮唯一活任务是他人的回合，借用会干扰）。
  两个面**互斥**（二进制原文 `permissions` cannot be combined with `sandboxPolicy`），
  且 `permissions.filesystem` 另有约束不能在此定义 profile，所以这不是「换个字段名」，
  要把整套安全姿态迁过去 + 加版本门 + 双轨回落。做之前先把执行机版本钉死。

- **改用「沙箱内追加权限」档（`with_additional_permissions`）替代现有的提权档。**
  codex 的 per-command 覆盖有三档，今天 handoff 只见到 `require_escalated`，
  而它的语义是**整条命令完全脱离沙箱**（二进制原文 `for unsandboxed execution`）。
  第三档是沙箱内追加本次所需权限、不脱沙箱，但需要开启 `features.exec_permission_approvals`。
  价值：让 B227 覆盖不到的其余提权场景（装依赖、出网）不再等价于无沙箱执行。

  **前置比想象中大：handoff 侧今天读不到也回发不了档位**（2026-08-25 核过代码，
  由 B249 协调者首先指出、本会话独立复核）：`internal/executor/codex/perm.go` 的
  `commandApproval` 字段全集是 itemId / threadId / turnId / command / cwd / commandActions，
  **不含任何沙箱或提权档位字段**；`decisionFor` 的全部逻辑是 `once` → `accept`、其余 `decline`，
  回发报文只有一个 `decision` 键。所以要用第三档，这两处都得改：**先能读到请求带的是哪一档，
  再能回发一个「只批沙箱内执行」的裁决**。这比「开个 feature flag」大得多。

  **这条同时解释了今天审批的性质**：协调者与审批链的每一次 allow 都是**盲批**——
  不知道自己批的是沙箱内还是脱沙箱，也没有「只批沙箱内」这个选项。B227 落地后
  绝大多数盲批实例（codex 的 git 本地写）会在源头消失，但**其余场景仍是盲批**，
  这条 roadmap 才是根治。

  推论（对审批判据类的卡）：只要 handoff 读不到档位，「白名单自动放行」与「人工批准」
  在沙箱语义上**等价**——不会更危险，但也不会更安全。

- **同仓并行任务互相踩踏的收窄。**B227 让 git 公共目录整个可写，意味着同一仓库的两个
  在飞任务能互改对方的索引与引用（`objects`/`refs`/`packed-refs` 都是共享的，落点分布见台账 §5）。
  本期接受这个代价（与原地模式同档，且不新增权限类别），但**同仓并行派发正是本产品的卖点**，
  值得单独一张卡。可能的方向：worktree 私有目录收窄到自己那一份 + 对象库/引用仍共享，
  但那只解决一半，另一半要靠上一条的 deny 能力。

- **纪律块层面的兜底：告知执行者 git 元数据被拒时应提权重跑。**成本近零，覆盖 B227
  未触及的边角情形。**不进本期是因为它会让 B227 的验收判据变模糊**——一旦纪律块也在
  影响行为，就分不清「沙箱修好了」还是「模型这次听话了」。等 B227 真机验收落定后再加。

- **临时 hooks 目录的启动清扫（B227 review 留的 minor，本期不做）。**`gitExec` 每次调用
  `os.MkdirTemp` 建一个空目录并 `defer os.RemoveAll`；agentd 被 SIGKILL 时该次调用的目录会遗留。
  **不进本期的理由是量级**：SIGKILL 只发生一次、只泄漏一个空目录，且落在 TMPDIR 下由系统清理；
  为它加一条启动清扫要动生产代码并重走一轮 implement→review，代价大于收益。
  真要做，形态是 agentd 启动时扫 `TMPDIR/handoff-empty-hooks-*` 并删掉，注意别误删同机
  另一个在跑的 agentd 实例的目录（判据得比前缀更强，比如带 pid 且校验该 pid 不存活）。

- **执行机上装 codegraph（B227 图对账暴露）。**linux-01 没有 codegraph 二进制，
  图对账节点跑到 `validate` / `sym` 时是 `command not found`，节点仍判 pass 并如实记了原始错误，
  但**「视图与真代码对不对得上」这一步实际是空的**。B227 这轮由协调者本机补跑（六个节点
  anchor 全 ok、check 零新增违规），但下一张卡不会自动有人补。
  形态有两条：给执行机装 codegraph，或让 recon 纪律块在工具缺失时判 `needs_human` 而不是 pass
  ——后者更稳，因为前者会随新执行机接入反复失效（参见 opencode 不在非登录 shell PATH 的旧坑）。

## 来自 B249 spec（2026-08-25，权限判据降噪）

- **评估下线廉价模型审批者**：B249 的白名单只吃掉可枚举的安全形态，长尾仍交给 approver。
  等白名单稳定运行一段、Consult 计数显著下降后再评估是否整个下线（省一次模型调用与
  6.2s 中位延迟）。**前置条件是下一条**——没有计数就没有「显著下降」的判据。
  来源：`specs/2026-08-25-b249-permission-noise-reduction.md` 的 Out of Scope。
- **权限出口的持续计数与看板**：今天 AutoAllow 逐次只有 Debug 日志（默认不开）、
  Consult/Escalate 只能靠扫 `agentd.log` 事后归因，一次取证要写一个子 agent 跑 15 分钟。
  应有常驻的三出口计数（manager 已有 `aaCount` 可扩），并在控制台呈现。
  它是上一条的前置，也是「判据改动到底降了多少噪」的唯一判据。来源：同上 spec。
- **（判据背景，防重走）** 2026-08-25 对 linux-01 三天 1377 次判定的全量取证结论：
  人被叫醒 246 次、批 238 次、拒 6 次且**6 次全与安全无关**（都是方法论纠偏）；
  「容器管不住」的网络副作用维度三天是空集（0 curl / kubectl / psql / aws / ssh /
  git push）。**再有人提「靠 OS 隔离来减少权限打扰」，先复读这组数字**——降噪的杠杆
  在判据不在隔离，隔离的价值在 B227 与进程清扫，两件事不要再捆在一起论证。

## 来自 B249 执行过程（2026-08-25，非 spec 预见的残余）

- **卡基线在首派那刻冻结，依赖卡后落地就再也进不来**。B249 15:54 首派 contract 时
  B248 尚未落地，于是 B249 分支上一直是旧正则，implement、三轮 review、全量测试
  **三关全绿且无一报错**——review 只审本卡改动，测试不测另一张卡的行为，三方合并还能
  保住对方的改动。症状只有一个：两卡改动从未在同一份代码上一起跑过，而 B248 spec 明写
  「必须同轮落地，先放宽后收紧的中间态是净减安全」。协调者在 acceptance 阶段用
  `git merge-base --is-ancestor` 才查出来，本地合并后重做验收（21/21 行为判据绿）。
  **可做的**：`card dispatch` 或 finish 阶段对「卡 spec 里点名的前置卡」做一次祖先检查。
  尚未取号，需要时 `handoff card add`。
- **contract 节点给 `codegraph/target.json` 写 entries 时用了函数符号名**
  （`executor.TaskTmpDir`），而该字段的语义是**容器 Label**（判定见 charter
  `graph/codegraph/check.go`：逐条比对 `Container.Label`，无跳过分支）。后果是必然在
  review 阶段撞 dead-entry、多烧一整轮 implement + review。基线里其余 9 条 entries
  全是容器级形态（`ledger.Store`、`proto 实体`、`ptyhost 实体` 等），无一函数符号。
  **归属 charter 仓 = C13**（2026-08-25 建卡，待办）：contract 节点 skill 该给出这条判据，
  改完跑 `scripts/regen_discipline.py` 同步纪律块。不是 handoff 仓的改动。
- **（已有卡，不重复取号）** 审批者对沙箱层级完全失明 = **B252**；`card --attach`
  按 path 去重忽略 kind = **B250，已于 2026-08-25 合入 main**（附件身份改 (kind, path)）；
  执行者隔离层 = **B247**。
- **（已修，留痕）** `skills/handoff/SKILL.md` 排障表原写「驱动权泄漏 CLI 侧今天无解」，
  B239 后 `card takeover` 已可用、`card release` 也不再是静默 no-op，本轮实测确认并改正。

## 来自 B273 spec（2026-08-27，本期不做）

- **生产侧不再发没有 `final_text` 的半成品 `completed`**：今天同一处发射点会先发残缺再补全文，是为了重启丢终态时能补回报文。消费侧 B273 用「等带 final_text 的 completed + 秒级宽限」止血。改生产侧等于重议那条补报文决议，另开卡。来源：B273 spec Out of Scope / 源卡 B243。
- **裁决块 notes 改走围栏 / heredoc**：执行者不再把自由文本塞进裸 JSON 字符串。B273 先做解析容错。来源：B273 spec / 源卡 B242 弃选 a。
- **trailer 允许 `commit` 为空、由 agentd 填 HEAD**：只读节点完全不提交。要核 completed/turn_failed 对空 commit 的容忍度。B273 只改铁律原文，schema 不动。来源：B273 spec / 源卡 B244 弃选 c。
- **只读节点变异自验走 `git archive` 写进角色纪律**：B229.1 review 执行者摸出来的正当出口。属于角色纪律正文，不是回合协议铁律。来源：B273 spec / 源卡 B244 旁证。

## 来自 B276 spec（2026-08-28，本期不做）

- **B261 方向 2：轮次进行中把新判据送到当前执行者**。今天 `--extra` 只在 dispatch 注入，`continue` 只在 `waiting_review`。来源：B276 spec / 源卡 B261。
- **B261 方向 3：纪律块要求执行者收尾回读卡上的判据**。来源：同上。
- **B258 服务端分码**：工单未注册 vs 已消耗拆成不同 HTTP 状态，不再共用 404「工单不存在」。来源：B276 spec / 源卡 B258。
- **B258 镜像延迟**：任务已 `waiting_answer`、卡流数分钟零事件。来源：同上。
- **B211 release CI 资产 hash 硬门**（管发布物，管不住手工 `go build`）。来源：B276 spec / 源卡 B211 弃选 C。
- **charter 仓刀 0 别名销账**：handoff 侧已删除 `handoff graph`；charter 契约 §4 / charter skill / charter roadmap 第 6 条仍写别名观察期。来源：B276 spec r1 / 审查 Issue 3。

## 来自 B277 finish（2026-08-28）

- **升 charter/graph 以挂 `flow`/`tree`**：本卡锁 `github.com/Xsxdot/charter/graph v0.9.0`，该 tag 无 `flow` 子命令。C17 已合入 charter master，发新 graph tag 后 handoff `go.mod` 升级，模块内 `go run github.com/Xsxdot/charter/graph/cmd/codegraph flow` 才可用。与 charter roadmap 第 56、第 1i、第 6 条同族。来源：B277 implement 真机、C17 finish。
- **TS/React flows 不做**：用户 2026-08-28 裁掉。查看器对 `.ts`/`.tsx` 入缝保持 degraded。来源：B277 spec Out of Scope。
- **扫描器跳过/空流程**：`n_ledger_Store_EnsureDefaultTemplates`、`n_ledger_Store_EnsureDefaultWorkflows` 解析失败跳过；6 个键空 `steps`（无图内 call/可视控制流）；接口实现闭包第 2 轮后仍有 6 个二阶候选按计划上限未展开。来源：B277 扫描报告。
- **全函数 CFG / SSA**：不在本卡。来源：B277 spec OOS，charter roadmap 27/32/53。

## 来自 B278 spec（2026-08-28，本期不做）

- **B235 不同名分支自动合并**：卡 `base_branch` 与工作分支历史分叉时，dispatch 自动 merge。B278 只警告。来源：B278 spec / 源卡 B235。
- **B235 `card dispatch --step --base`**：显式覆盖节点起点。今天卡派发没有这条主路径。来源：同上。
- **B260 HTTP `task_states` / `relations` 蛇形化**：Web 已按 PascalCase 冻结（`proto.TaskStateRow` 注释 + `web/src/api/ledger.ts`）。要改得连 Web 一起改。来源：B278 spec / 源卡 B260。
- **B251 存量带日期文件改名**：历史 spec 文件保持原名。来源：B278 spec / 源卡 B251。
- **仓外 product-backlog skill 日期禁令**：`~/.grok/skills/product-backlog/SKILL.md` 仍只写「不要自己起描述性文件名」。B278 只改仓内 `skills/handoff/SKILL.md` 与 prompt。来源：B278 spec r1 / 审查 Important 8。

## 来自 B271 spec（2026-08-28，本期不做）

- **`resume --force` 给节点能认的终态**：今天只把 `task.state` 推到 `waiting_review` 并打 `progress`，不发 `completed`，charter 节点 `waitForTurnEnd` 不会自动过。不要用假 `completed` 冒充执行器。来源：B271 spec / B268 续。
- **执行器写完终稿不发 `completed`**（B268 现场 grok）。属执行器卡，不是本机镜像身份。来源：同上。
- **真远端镜像断线 vs 卡流滞后的可观测性**：现场容易把任务流还在走、卡流停住当成同一个 bug。本卡只拆本机自订。来源：B271 spec Out of Scope。

## 来自 B234 spec（2026-08-28，本期不做）

- **把测试 HTTP helper 迁到全仓其余 `httptest.NewServer`**（executor / release / cmd）。B234 只收口 `internal/agentd`。来源：B234 spec 族一弃选 / OOS。
- **Darwin 全量 `go test ./...` 在未迁包上仍可能以族一形状伪装成业务断言**。识别纪律：同一次跑里有没有 `can't assign requested address`。来源：B234 spec / B193 note。
