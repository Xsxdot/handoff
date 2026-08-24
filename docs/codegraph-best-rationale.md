# handoff `codegraph/best.json` 配方说明与拍板记录

> **状态：已批准（2026-08-24，C1.6，用户全案按推荐拍板）**
> 本文档是 best.json（理想树 + 容器归属映射）的论证与冻结记录，随 C1.6 一并
> 提交的还有 target.json 的 v3 迁移与契约面词表重写（roadmap 13e：换 best
> 词表必须同刀重写 target.contracts）。改 best.json 的结构树 = 改应然架构，
> 需重走 contract 节点；本文档随之修订。

草案：12 个顶层子系统、23 个领域（20 叶子）、233 个带文件容器全部归属。
方法：先问「这堆符号在产品里维护什么不变式」，再落容器；容器是最小归属单元，
所以扫描器造出的「包形容器」（如 k_agentd_fn）只能整体放，混杂之处如实记在
待拍板与未验证清单，不硬拆也不假装纯。

对照物：baseline 的 20 域是扫描器按包分的现状（待改进）；target.json 的 10 子系统
是按路径划的（被取代对象）。下文引用二者只作证据，不作答案。

---

## 一、顶层领域逐个论证

### d_orchestration 任务编排（logic）
**为什么是子系统**：handoff 的产品核心是「一次派发的完整生命周期」——pending →
running → waiting_* → 终态。状态机（proto.transitTable + CAS）、工单审批
（Approver）、事件留痕（store.events）与实时分发（Hub）、断线回收与对账
（reclaim/reconcile/watchdog）、SQLite 持久化（store）都在维护同一组不变式，
d_coordination_task.json 里人写的四条不变式（合法迁移、CAS、非终态统计、终态作废
工单）就是这个域的宪法。
**type=logic**：接缝对面全是自有代码——gateway 调它、它调 execution/workspace 的
门面。SQLite 经 database/sql 是持久化实现细节，不构成外部现实接缝（先例：
target.json 把同样持久化到 SQLite/PG 的 d_ledger 定为 logic）。
**store 放这里**：store 的包注释自述「任务、事件、工单、项目位置四张表……不含业务
规则」，它持久化的就是本域的状态。target.json 把 internal/store 路径分组进
d_ledger 是按「都是存储」归堆——那是层思维；ledger.Store 有自己的库、自己的 DSN、
包注释明言「不 import agentd」，两个 Store 不是一个职责。

### d_gateway 控制门面（boundary）
**为什么是子系统**：agentd.Server（187 节点）+ HTTP/WS 入口承担一件独立的事：
鉴权、路由、参数校验、响应编排、跨机转发。它服务所有领域，按「入口挂到它服务的
领域」的配方规则本应打散——但它是单容器，且其职责（协议翻译）本身自洽。
**type=boundary**：接缝对面是网络上的 CLI 与浏览器——外部现实；测试全靠 httptest
伪造网络这一事实印证。
**不变式**：未鉴权请求不得触达任何领域调用；响应契约与 proto 同源。
（是否并入 orchestration 见待拍板 2。）

### d_workspace 项目与工作区（boundary）
项目身份（projectid）、工作树准备/回收、分支跨机同步（localsync）、机器镜像
（Mirror）、启动项（launcher）在同一协作语义下闭环，d_workspace.json 的四条人写
不变式（选项互斥防注入、脏库不阻塞、文件白名单、fetch 不动 HEAD）是它的宪法。
**type=boundary**：接缝对面是 git 二进制与文件系统。
**注意**：本域最承重的实现（workspace.go 的 66 个函数，含声明锚点 PrepareWorkspace）
被扫描器装在 k_agentd_fn 混杂容器里，本稿把该容器放在 orchestration（待拍板 1），
所以本域图上偏薄——这是容器债，不是领域错。

### d_execution 任务执行（boundary，3 子域）
「把任务真正跑起来」是一个子系统：契约（Adapter 接口 + turn 回合协议）、四家
适配器、进程承载（prochost）共同维护「每个任务的事件收敛为统一回合流、进程树
必可回收」。**type=boundary**：对面是模型 CLI 进程与 OS 进程原语。
子域按职责差异切三块：contract 是对编排域的承诺面（adapters→contract 168 条边
是域内实现向自家契约收敛，健康）；adapters 是差异吸收层；host 是 OS 原语层。
baseline 在这里有三个互相重叠的顶层（d_executor 平铺 47 容器、d_execution 带一个
空的 _adapters 子域、_host），本稿合并为一棵树。

### d_sessions 终端会话（boundary）
PTY 会话生命周期、实时订阅、回放缓冲、磁盘落点（sessdir）、跨 agentd 重启收养。
对面是 shell/PTY 进程。与 execution_host 的关系是**复用**（6 条单向边借用 prochost
的锁与凭据原语），不是同职责——执行承载服务任务，会话服务人（见待拍板 3）。

### d_transport 跨机连接（boundary，2 子域）
协调者到执行机的通道：channel（client + targetclient.Pool 的目标路由与复用）与
tunnel（relay 懒拨号 + proxycfg）。对面是网络。沿用 baseline 的两个子域——这是
现状里少数职责切分正确的地方。

### d_protocol 协议契约（logic）
wire 类型唯一定义处 + transitTable。扫描配方明文论证过它必须自成领域（111 个
proto model 塞进任务域的反例）。对面是自有代码的编译期依赖。

### d_ledger 卡片账本（logic）
需求卡、类型化关系、工作流、裁决、事件镜像（ledgermirror）、节点执行编排
（ledgerstep）。包注释自带边界宣言：不 import agentd/executor、与执行域只有弱引用
表、凭据只在协调机。这是全仓边界纪律最好的域，原样保留。

### d_cli 协调者命令面（logic）
cmd 树 + main。按配方「入口挂到它服务的领域」本应打散，但 c_cli/k_cmd_fn 是单
容器，且 CLI 的文本输出契约（可脚本化）本身是一件事。**type=logic**：主体工作是
把意图翻译成自有 client/ledger 调用（88 条 cli→ledger 边显示 card 族命令直接操作
账本库，与账本「凭据只在协调机」的设计一致）。

### d_web Web 控制台（logic，6 子域）
浏览器侧的协调者工作台。50 个容器平铺（现状）掩盖了五种互不相干的职责，故细切：
contract（API 镜像层）/ command(任务指挥) / workbench（操作现场）/ cards（账本
看板）/ admin（机器、项目、设置、换版、代码图诊断页）/ shell（壳与共用，含
webui 的 Go 侧资产打包——控制台的交付形态跨语言，这正是「领域不是目录」）。
子域间 160 条边中 admin→shell 40、admin→contract 25 都是页面→共用件/契约层的
单向消费。

### d_policy 运行策略与配置（logic）
「本机如何运转」的决策集：config（可信配置）、permgate（放行判据）、discipline
（纪律块）、envfile/pathenv（环境注入）、initflow（首次配置问答）、logx。这些
规则决定行为但不执行动作，对面是消费它的自有代码。

### d_maintenance 安装与换版（boundary）
「把对的版本落到机器上」：toolchain 探测、release 下载校验、selfupdate/upgrade
判据与执行、skill 安装、service（systemd/launchd/Windows 托管）。对面是发布服务器、
文件系统与系统服务管理器。
baseline 把 policy 与 maintenance 供在 d_runtime 母域下——但二者互相只有 2 条边，
「都跟本机有关」不是职责；拆成两个顶层，母域取消。

---

## 二、相对现状（baseline）的分组改动

只列改动，未列即沿用现状归属。

1. **k_agentd_Hub：d_coordination_api → d_orchestration**。26 条反向边里 24 条是
   Manager→Hub.Publish/WaitAnswer——Hub 是任务事件总线，发布方是编排核心，订阅方
   才是网关。事件持久（store.events）与实时分发（Hub）本是同一职责的两半。此一改
   把现状最重的双向边 409/26 变成 319/2 的纯单向（见第三节）。
2. **k_service_\*（4 容器）：d_coordination_task → d_maintenance**。service 包自述
   「把 agentd 交给本平台的进程管理器托管」，与任务生命周期无关，是扫描器塞错。
3. **k_initflow_\*（3 容器）：d_coordination_task → d_policy**。首次配置问答产出的
   是 config，不是任务。
4. **k_webui_fn：d_coordination_task → d_web_shell**。webui 是控制台静态资产的
   打包形态，属于 Web 控制台产品，尽管它是 Go 代码（领域可以跨语言）。
5. **k_agentd_hostAllowlist：d_coordination_task → d_gateway**。hostguard 守的是
   请求来源，是门面职责。
6. **k_agentd_runOutputBuffer：d_coordination_task → d_workspace**。它缓冲的是
   工作区 run 的输出（节点在 workspace.go）。
7. **k_store_\*（3 容器）**：沿用 baseline 的编排归属，但与 target.json 的
   d_ledger 路径分组**显式冲突**，理由见 d_orchestration 论证。
8. **d_coordination 母域解散**：api→d_gateway 顶层、task→d_orchestration 顶层、
   cli→d_cli 顶层。「协作控制」母域把门面、核心、命令面三种职责焊死在一个屋檐下，
   却把同样属于协调面的 workspace/ledger 排除在外——它是按「都在 agentd 附近」画的，
   不是按职责。
9. **d_coordination_graph 取消**：代码图核心已外迁 charter 仓（go.mod 钉版），
   仓内只剩 CLI 命令与 agentd API 两处消费点（各随其容器归 cli/gateway）；
   k_codegraph_\* 三个容器已零节点（见待拍板 6）。
10. **d_executor（顶层）+ d_execution_adapters（空域）合并**为 d_execution 树的
    adapters 子域；turn/executor 根契约单独成 contract 子域（原先契约与四家适配
    混在一个平铺域里，承诺面和实现不分）。
11. **d_runtime 母域解散**：config→d_policy、maintenance→d_maintenance 各自顶层
    （改动理由见 d_maintenance 论证）。
12. **d_web 细切 6 子域**（原平铺 50 容器）。
13. **d_protocol / d_sessions / d_transport（含两子域）/ d_ledger / d_workspace
    容器归属原样保留**——这些是现状里职责与包恰好重合的部分。

---

## 三、边界成本自查

统计口径：4744 条边按「容器→叶域→顶层」聚合，跨顶层 995 条（21.0%）。
现状对照的 847/4744 是 20 个叶域口径，与本稿叶域口径 1336（23 叶域，切得更细
且入口独立成域）同量级；顶层口径才是子系统边界成本。前 10 对逐条判读：

| # | 领域对 | 边数（正/反） | 判读 |
|---|---|---|---|
| 1 | gateway → orchestration | 319 / 2 | 门面→核心的法定单向流。2 条反向是接线疣：`Manager.ResumeTask→Server.conf`（核心反读门面配置，建议把该配置下沉到编排侧）与 `NewServer→registerEventFrameHook`（组装点接线）。**不构成双向耦合** |
| 2 | orchestration → protocol | 102 / 0 | 契约消费，法定单向 |
| 3 | gateway → protocol | 91 / 0 | 同上 |
| 4 | cli → ledger | 88 / 0 | card 族命令直连账本库，单向；与账本「凭据只在协调机」设计一致 |
| 5 | cli → transport | 63 / 0 | 命令→client 拨号，单向 |
| 6 | gateway → policy | 38 / 0 | 门面读配置与纪律，单向 |
| 7 | cli → policy | 32 / 0 | 同上 |
| 8 | cli → protocol | 29 / 0 | 契约消费 |
| 9 | orchestration → execution | 27 / 0 | 编排驱动执行契约，法定单向 |
| 10 | transport → protocol | 25 / 0 | 契约消费 |

前十无一双向。其余需说明的两处小额往来：

- **orchestration ↔ workspace 19 / 2**：19 条正向里 12 条是 k_agentd_fn 混杂容器
  内部互调被口径放大（DirtyWorktreeError 的方法容器在 workspace、struct 在
  k_agentd_model），5 条是 Manager→projectid.FromOrigin 的正当契约调用；2 条反向
  （Mirror→共享 model、Error→自身 model）同为容器债。**k_agentd_fn 拆容器后此对
  预计缩到 <10 且纯单向**——这是待拍板 1 的量化依据。
- **policy ↔ transport 2 / 4**：config 校验时借 proxycfg.Validate/Redact（2），
  targetclient 读 config.Target（4）。config.Target 实为共享配置契约。量极小，
  长期解法是把 proxy 字符串判据搬进 policy 或把 Target 下沉为契约模型，不值当下动。

结论：本树画出的每条重边都是单向分层，没有需要「承认长期迁移目标」的切不动边界。
现状最重的病灶（409/26 双向）被 Hub 一步归位消解，剩余双向噪声全部可归因到
k_agentd_fn 一个混杂容器。

---

## 四、拍板记录（2026-08-24 全案按推荐批准）

裁决：以下 6 项全部按本稿默认（推荐）方案执行——
1 甲案（k_agentd_fn 整块归 orchestration，容器债待重扫拆分）；2 gateway 独立顶层；
3 sessions 与 execution_host 分开；4 permgate 归 policy；5 web 细切 6 子域；
6 僵尸容器留空、随下次重扫删除。原始岔口与取舍保留如下备查：

1. **k_agentd_fn（211 节点）放哪**。三向混杂：workspace 族 ~93、生命周期族 ~46、
   路由辅助 ~56。**甲案（本稿默认）放 orchestration**：跨顶层 995、矩阵最干净、
   与 baseline 现状一致少折腾；代价是 workspace 域被掏空、d_workspace.json 的锚点
   函数落在别人域里。**乙案放 workspace**：尊重人写声明的锚点、按多数派归属；
   代价是跨顶层升到 1123，且凭空造出 gateway→workspace 225 与
   orchestration↔workspace 97/33 两条脏边。**真解法与两案正交**：下次重扫把
   k_agentd_fn 按职责拆成 3 个容器，岔口自动消失——若近期有重扫计划，建议先拆再定。
2. **d_gateway 独立顶层 vs 并入 d_orchestration 作子域**。独立（本稿默认）：单容器
   Server 无法按配方打散到各被服务域，独立后 319/2 的门面→核心单向流在全景可见，
   鉴权/路由自成职责。并入：更贴配方「入口不成层」的字面，全景少一个域；代价是
   编排域吞下 187 节点的路由容器，「未鉴权不触达领域」的不变式没了自己的落点。
   取决于你要全景强调「门面纪律」还是「入口即门面」。
3. **d_sessions 与 d_execution_host 是否合并为一个进程宿主子系统**（target.json 的
   d_host 先例：prochost+ptyhost 同域）。分开（本稿默认）：一个服务任务、一个服务
   人，不变式不同（进程树必回收 vs 会话可收养回放），6 条 sessions→prochost 单向边
   是借原语不是同职责。合并：省一个顶层域，「detached 进程承载」统一叙事。
4. **permgate 归 d_policy 还是 d_orchestration**。归 policy（本稿默认）：判据与
   config 同源、无状态、可独立演进。归 orchestration：它的唯一业务消费方是
   Manager 的审批链，判据跟着审批走。两说都成立。
5. **d_web 六子域 vs 平铺**。细切（本稿默认）理由见论证；平铺省 6 个域名额、
   避免页面级摇摆（如 overlay 里 Board 弹层与 Tickets 弹层其实横跨 command 两块）。
6. **6 个零节点僵尸容器未归属**（如实留空，会被判据报 container-unplaced）：
   k_codegraph_Target / k_codegraph_fn / k_codegraph_model（代码外迁 charter 仓）、
   k_svc_Server / k_svc_model（对应包已不存在）、k_web_model（空壳）。建议随下次
   重扫从图里删除，而不是给它们编一个归属。

## 五、未验证清单（依赖行为事实、本稿未跑通路）

- **Hub 只承载任务事件流**：改动 1 的论据来自 26 条调用边与包注释；若 Hub 运行时
  还广播 mirror/machine 等非任务事件，「事件总线属编排」的理由会被削弱为部分成立。
- **store 的 mirror_\*/workbench_\* 表的真实写入方**：推断为 Mirror 与 workbench
  API，未核实写入通路；若成立，长期看这几张表是 store 里的跨域寄存，可再议。
- **ledgermirror 的事件镜像通路**（挂账 task 事件→账本）：依包注释与命名推断，
  未跑真机验证。
- **web 六子域的页面归类**：依文件头注释与命名判断（如 data/ 是共用钩子、
  overlay 是全局弹层基座），未逐组件核对实际数据流。
- **k_agentd_fn 三族节点的 93/46/56 计数**：按文件名归族的静态估算，族边界
  （如 runshell.go、hostguard.go 算门面还是编排）有 ±10 浮动。


## 六、契约面重写（target.json v3，与本稿同刀）

- **v2→v3 机械迁移**：`codegraph migrate` 产出 meta v3 + assembly 骨架；
  `subsystems` 结构树按 C1.8 语义移出 target（结构住 best.json）。
- **contracts 按实测方向矩阵重写**：新树下跨顶层边 995 条、36 个方向，全部声明；
  旧 23 条契约里 22 条方向词表已死（dead-contract），仅 d_cli→d_ledger 存活。
- **entries 窄缝承接**：8 个方向承接旧契约的窄缝声明（proto 实体、ledger.Store、
  client.Client、ptyhost 实体等），经真机 check 验证全部存活（零 dead-entry）；
  393/995 条边流经窄缝，不计预算。
- **legacyBudget = 当前直调数**（棘轮起点）：总预算 602。三个方向纯窄缝零预算
  （execution/gateway/transport → protocol）。此后只许降不许升。
- **重定基线的棘轮说明**：本分支上 CLI `codegraph check` 会报 32 条 budget-raised
  ——棘轮拿 main 的旧词表契约当基准，新方向按预算 0 处理，属换词表的固有现象；
  合并进 main 后基准即本契约，自然消失。仓库执法闸 `TestRepoContractGate`（库级
  Check，无棘轮）本轮绿，同时修复 B226 的恒红。

## 七、冻结时真机读数（2026-08-24，baseline 3656 节点 / 233 容器）

- `codegraph validate`：0 issue（best 树 + 233 容器全归属合法）
- 库级 Check（执法闸口径）：**fails 0**；warns 104 =
  container-misplaced 50（gap 进度表的主刻度）+ legacy 33（直调在预算内，可收窄）
  + anchor-off-domain 2 / anchor-off-graph 12（domains/ 声明文件仍按 baseline
  词表锚定，其中 PrepareWorkspace 落在 k_agentd_fn 容器债里——见拍板 1）
  + oversized-package 2 + prefix-family 5
- bestCoverage：assignedContainers 233/233，crossDomainEdges 995，misplacedSkipped 115
- 还债入口：k_agentd_fn 拆容器（拍板 1 的真解法）、6 僵尸容器随重扫删除、
  container-misplaced 50 条逐个消化——均归 roadmap 1f 竖切还债线
