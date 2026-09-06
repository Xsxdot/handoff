# Handoff 基础任务与公司运作流程图

日期：2026-09-06。配套：[基础模型草案](/Users/xushixin/workspace/handoff/docs/superpowers/specs/2026-09-06-foundation-domain-model-draft.md)。

**阅读边界：这是建议的目标流程。** 已确认的是“统一通过载体，支持默认载体”。自动建载体、统一名称等是本轮新增建议；权限默认表与 push 归属仍待确定。图中的行为顺序不等于已冻结的函数签名、事务方案或现有代码。

最新范围调整：用户已明确将首次配置暂缓，图 1 保留供后续讨论，不作为当前实现入口。当前基础能力关系与审批 client/harness 分工见[基础设施边界草案](/Users/xushixin/workspace/handoff/docs/superpowers/specs/2026-09-06-foundation-infrastructure-boundaries-draft.md)。

先看图 0 的整体，再按编号进入领域细节。每张图标出责任、输入、输出和异常去向；领域细节服从同一任务事实，不各建一套任务状态机。

## 图 0：整体脉络——公司如何接活与交回结果

```mermaid
flowchart TD
  U["董事长：确定目标、范围和保留决策"] --> C["CEO / 协调者：组织本次工作"]
  U --> D["直接委托一项任务"]
  C --> B["项目台账与工作流：业务事项、依赖、节点"]
  B --> H{"当前是人工步骤还是任务步骤"}
  H -->|人工步骤| U
  H -->|任务步骤| D
  SET["首次使用与配置助手｜图 1"] --> RES["员工与部门名册：载体、小队、默认载体"]
  RES --> R["统一名称解析与资源准入｜图 2"]
  D --> R
  R --> CONTRACT["任务有效约定与指令组装｜图 3"]
  CONTRACT --> RUN["员工在任务工作区执行｜图 5"]
  RUN --> PERM["操作授权与审批｜图 4"]
  PERM -->|决定送达| RUN
  RUN -->|需要决策| C
  C -->|明确回复或调整委托| RUN
  RUN --> RESULT["结果核实、保留与交付｜图 6"]
  RESULT --> C
  RESULT -->|普通派发结果| U
  C -->|继续或验收| B
  B --> WF["工作流路由与后续触发｜图 7"]
  WF -->|下一步| H
  WF -->|业务结束| END["董事长收到结论、证据与交付状态"]
```

部门把任务分配给具体员工；部门本身不是执行环境。CEO 代表协调职责，可以来自外部调用，也可以由平台拉起；平台内拉起时同样需要载体与明确授权。这里没有新增“部门经理”实体。

## 图 1：首次使用、发现新引擎与配置

责任：运行维护建立基础运行条件；资源域持有载体登记与默认选择；setup skill、CLI、设置页是同一配置能力的入口。

```mermaid
flowchart TD
  START["首次使用 / 显式刷新 / 已启用的本机发现"] --> BOOT["读取已有配置，确认基础服务与登记能力可用"]
  BOOT --> READY{"基础服务可访问"}
  READY -->|否| FIX["给出具体缺项；配置助手通过正式能力补齐"]
  FIX --> BOOT
  READY -->|是| SCAN["发现受支持的引擎与运行档案"]
  SCAN --> FOUND{"发现可登记的档案"}
  FOUND -->|否| NEED["说明未安装、路径缺失等实际原因"]
  NEED --> ASSIST["按用户选择安装、登录或连接执行机"]
  ASSIST --> SCAN
  FOUND -->|是| EACH["逐项按机器、引擎、HOME 查重"]
  EACH --> EX{"已有对应载体或显式忽略记录"}
  EX -->|已有| KEEP["复用身份；保留用户配置"]
  EX -->|显式忽略| SKIP["保持忽略，不重新登记"]
  EX -->|新发现| REG["通过统一登记入口建立基础载体"]
  REG --> NAME{"名称可用"}
  NAME -->|否| ALT["确定性生成可用名称；不覆盖同名资源"]
  ALT --> REG
  NAME -->|是| CHECK["记录发现证据，按说明的方式验证可用性"]
  KEEP --> CHECK
  CHECK --> STATE["展示已验证可用 / 待验证 / 需处理的原因"]
  STATE --> DEF{"已有默认选择"}
  SKIP --> DEF
  DEF -->|是| RETAIN["保留原默认；不可用则明确提示"]
  DEF -->|否| ONE{"存在明确用户选择或唯一已验证可用载体"}
  ONE -->|是| SAVE["设置默认载体并记录来源"]
  ONE -->|否| PICK["只询问本次需要的默认选择；没有可用项则先修复"]
  PICK --> CHECK
  RETAIN --> DONE["展示可派发对象、默认值与未完成项"]
  SAVE --> DONE
```

图中“逐项”处理完发现集合后再确定默认值，不能每发现一个就抢占默认。基本档案默认沿用主 HOME，不自动复制凭据或建立小队。多引擎只需一次偏好选择；新发现不改旧默认。待验证不是“已上线”，引擎可启动也不证明已满足任务的全部权限限制。

setup skill 可处理复杂配置与修复；日常简单派发无需每次重跑它。它展示并调用程序提供的配置能力，不成为另一份配置事实。

## 图 2：交给谁——统一名称解析、准入与绑定

输入：任务目标与输入、可选接收者名称、调用者身份。输出：任务关联的实际载体绑定与容量占用。工作流就绪条件在进入此图前由应用判断。

```mermaid
flowchart TD
  IN["收到任务请求"] --> REQ["建立可关联的请求身份；重复请求返回已有处理结果"]
  REQ --> AUTH{"调用者有权派发到该资源范围"}
  AUTH -->|否| DENY["拒绝并说明授权缺项"]
  AUTH -->|是| HAS{"是否给出接收者名称"}
  HAS -->|否| DEFAULT["读取默认载体"]
  HAS -->|是| LOOKUP["从统一命名空间解析资源"]
  DEFAULT --> TYPE{"解析结果"}
  LOOKUP --> TYPE
  TYPE -->|不存在或旧数据重名| INVALID["明确报错，不猜类型或回退到裸环境"]
  TYPE -->|载体| SINGLE["形成单一载体候选"]
  TYPE -->|小队| GROUP["按小队选择规则形成成员候选"]
  GROUP --> FIT["检查候选可用性与任务所需能力"]
  SINGLE --> FIT
  FIT --> AVAILABLE{"有符合要求的载体"}
  AVAILABLE -->|否| WHY["报告不可用或能力不匹配原因"]
  AVAILABLE -->|是| SLOT["统一准入：载体容量；有小队时同时检查成员配额"]
  SLOT --> CAP{"获得容量占用"}
  CAP -->|否| WAIT["按请求策略排队或返回忙；不创建第二套执行落点"]
  WAIT -->|队列重新获调度| FIT
  CAP -->|是| BIND["记录实际载体身份、来源和本次运行设置"]
  BIND --> NEXT["准备有效约定与工作区｜图 3"]
  NEXT --> LAUNCH{"启动结果"}
  LAUNCH -->|成功| RUN["进入任务运行；占用随实际执行生命周期管理"]
  LAUNCH -->|失败| RELEASE["记录失败，释放本次占用，保留可诊断事实"]
  WAIT -->|取消排队| CANCEL["取消请求并留下状态"]
```

待定实现细节包括：队列是否默认启用、预约如何持久化及恢复、取消与启动竞态怎样解决。必须守住的行为是：已选载体、实际启动环境和容量计数一致；重复请求不启动两份；恢复不重复计数。工作流可以提供优先级等调度输入，但调度域无需读取卡的具体状态。

## 图 3：告诉员工什么——任务约定与指令组装

责任：任务调配记录有效约定，工作区域提供范围与基线，策略域确定权限和纪律，引擎适配器翻译已确定内容。

```mermaid
flowchart TD
  INPUT["普通任务输入 / 已组装的节点输入"] --> GOAL["目标、输入材料、结果要求"]
  CARRIER["已绑定载体与能力"] --> PREP["确定工作区、基线与合法作用范围"]
  GOAL --> PREP
  PREP --> POLICY["解析平台边界、项目默认和本次授权"]
  POLICY --> PERM["得到有效权限及其来源"]
  DISC["选定角色纪律及版本"] --> RESOLVE["解析正文；未指定时使用明确的基础行为"]
  PERM --> CHECK["检查任务要求、权限、纪律和引擎能力是否冲突"]
  RESOLVE --> CHECK
  PROTO["统一提问与结果协议"] --> CHECK
  CHECK --> OK{"可满足且无未解决冲突"}
  OK -->|否| STOP["指出冲突或缺项；回到调用者，不静默扩大权限"]
  OK -->|是| COMPOSE["单一组装点：协议、权限说明、纪律、任务输入"]
  COMPOSE --> SNAP["保存有效值、来源、版本和实际指令"]
  SNAP --> TRANSLATE["适配器生成原生会话配置与指令"]
  TRANSLATE --> START["启动执行"]
  SNAP --> VIEW["用户可查看本任务的生效说明"]
  SNAP --> RESUME["继续与恢复复用本次约定"]
  RESUME --> CHANGE{"确需改变约定"}
  CHANGE -->|否| TRANSLATE
  CHANGE -->|是| REAUTHORIZE["核对变更授权，保存新版本并说明变化"]
  REAUTHORIZE --> CHECK
```

纪律正文可以版本化复用，权限必须有可执行语义；两者不能用同一段文字互相冒充。原生引擎不能实施所需限制时，返回能力不匹配。具体如何证明原生适配一致，需要后续行为测试。

## 图 4：员工或 CEO 要做一个动作——统一授权

这是目标授权路径。当前原生直通和协调者 run 的缺口见基础模型第 6 节。此图不意味着现有引擎已经报告所有动作。

```mermaid
flowchart TD
  OP["动作来自执行者、协调者或平台交付"] --> ID["识别主体、任务、动作、目标资源和参数"]
  ID --> CONTEXT{"描述完整且能确定范围"}
  CONTEXT -->|否| HOLD["暂停动作并请求补充或升级处理"]
  CONTEXT -->|是| LOAD["读取本任务有效授权与政策版本"]
  LOAD --> LIMIT{"是否越过不可委托上限或明确禁止"}
  LIMIT -->|是| DENY["拒绝；需要变更政策时走授权变更流程"]
  LIMIT -->|否| REUSE{"存在当前仍适用的已授权决定"}
  REUSE -->|是| ALLOW["允许本次动作"]
  REUSE -->|否| RULE{"确定性规则结论"}
  RULE -->|允许| ALLOW
  RULE -->|拒绝| DENY
  RULE -->|需要裁决| CANAI{"该范围允许审批模型裁决"}
  CANAI -->|是| AI["审批模型在被委托范围内判断"]
  AI --> AIOUT{"给出有效允许决定"}
  AIOUT -->|是| ALLOW
  AIOUT -->|升级、失败或超时| HUMAN["交给有权处理此类请求的审批者"]
  CANAI -->|否| HUMAN
  HUMAN --> HUMANOUT{"审批结果"}
  HUMANOUT -->|允许本次范围| ALLOW
  HUMANOUT -->|拒绝| DENY
  HUMANOUT -->|未回复| PENDING["保持待审批；按显式取消或超时策略处理"]
  PENDING --> HUMAN
  PENDING -->|取消或超时终止| ABORT["记录终止；不执行动作"]
  ALLOW --> AUDIT["记录决定、授权来源、范围与有效期"]
  DENY --> AUDIT
  AUDIT --> DELIVER["将允许或拒绝决定送达实际执行端"]
  DELIVER --> SENT{"决定已送达"}
  SENT -->|否| RETRY["保持未送达事实并恢复投递；不宣称动作成功"]
  RETRY --> DELIVER
  SENT -->|是| OUT["允许则执行、拒绝则阻止；实际执行结果另行记录"]
```

原生快速允许只能来自同一有效策略的映射。复用授权必须仍适用于当前主体、动作、资源与政策；审批决定已送达也不等于命令执行成功。“董事长/CEO”是职责标签，实际授权需要可识别的主体和范围，不能靠 prompt 自称。

## 图 5：任务执行、提问、回合结束与恢复

责任：任务调配拥有任务事实；适配器提供原生事件；PTY/网络层提供连接事实。图中“等待调用者判断”对应产品语义，不预先强制修改当前状态枚举。

```mermaid
flowchart TD
  START["启动已绑定载体中的执行会话"] --> RUN["任务运行中"]
  RUN --> EVENT{"观察到什么事件"}
  EVENT -->|进度或输出| LOG["记录输出与进度"]
  LOG --> RUN
  EVENT -->|操作审批| PERM["进入统一授权｜图 4"]
  PERM -->|决定送达| RUN
  EVENT -->|需要业务决策| ASK["记录具体问题；本回合暂停或结束"]
  ASK --> ANSWER["有权调用者作答"]
  ANSWER --> RESPOND["RespondAsk：携带原问题身份交回适配层"]
  RESPOND --> WAITMODE{"原问题的等待方式"}
  WAITMODE -->|原生工具仍挂起| NATIVE["回复原生问题，不开启第二回合"]
  NATIVE --> RUN
  WAITMODE -->|提问回合已结束| CONTINUE["使用本任务约定和关联答案开启下一回合"]
  CONTINUE --> RUN
  EVENT -->|有效回合结果| RESULT["保存执行者声明与系统观察"]
  RESULT --> REVIEW["等待调用者判断；结果处理见图 6"]
  REVIEW --> DECIDE{"调用者决定"}
  DECIDE -->|继续修改| CONTINUE
  DECIDE -->|接收或交付结果| COLLECT["核实、保留或交付｜图 6"]
  DECIDE -->|结束任务| CLOSE["满足保留条件后归档｜图 6"]
  EVENT -->|断连、进程异常或重启后发现失联| RECOVER["核对持久化事实与真实进程或会话"]
  RECOVER --> EXISTS{"执行实例是否仍存在"}
  EXISTS -->|存在| ATTACH["重连并恢复事件消费，避免重复启动"]
  ATTACH --> RUN
  EXISTS -->|已消失| RESUMABLE{"有兼容的恢复方式"}
  RESUMABLE -->|是| RESTORE["按既有任务约定恢复会话与容量占用"]
  RESTORE --> RUN
  RESUMABLE -->|否| FAILED["记录可诊断失败；交由调用者决定重试或结束"]
  FAILED --> REVIEW
  EVENT -->|取消请求| STOP["停止实际执行并确认占用结束"]
  STOP --> KEEP["保留已有结果，记录取消，释放本次资源"]
```

回合失败可以需要继续处理；进程失联不能直接当作业务完成。图 2 的容量占用在这里随真实执行保有与释放；等待业务验收是否继续占用执行资源，需要结合引擎可恢复性定稿，而不能简单等同于卡状态。

Ask 与审批升级通过派发事件流上浮，工作流订阅并转发；Ask 经 RespondAsk 回答，审批仍经审批 client 裁决。不增加独立交互领域，详见[基础能力中的 Ask 定义与时序图](/Users/xushixin/workspace/handoff/docs/superpowers/specs/2026-09-06-foundation-infrastructure-boundaries-draft.md)。

## 图 6：工作报告、结果保留、push 与归档

责任：任务保存结果事实；工作区能力核实文件与 Git；调用者/工作流判断业务接受；交付执行主体待定。push 可以用于跨机审查前的代码传递，不能等同于业务验收或合并。

```mermaid
flowchart TD
  REPORT["收到回合结果或结束请求"] --> READ["读取执行者声明和系统观察"]
  READ --> KIND{"本次结果类型"}
  KIND -->|代码| CODE["核对仓库、准确 commit、分支和实际改动"]
  KIND -->|文档或文件| FILE["核对路径、最终文件存在性和可读取位置"]
  KIND -->|只读分析| TEXT["保留报告与证据；无需伪造新提交"]
  CODE --> VALID{"结果可定位并满足保留约定"}
  FILE --> VALID
  TEXT --> VALID
  VALID -->|否| MISSING["报告缺失项；保留现场，要求补齐或明确放弃"]
  VALID -->|是| RECORD["保存结果引用与核实事实，交回调用者"]
  RECORD --> CHOICE{"下一项动作"}
  CHOICE -->|继续工作| CONTINUE["回到任务下一回合｜图 5"]
  CHOICE -->|业务验收| ACCEPT["普通调用者或工作流按业务标准验收"]
  ACCEPT -->|未通过| CONTINUE
  ACCEPT -->|通过| ACCEPTED["记录业务接受；不自动代表已推送或合并"]
  ACCEPTED --> CHOICE
  CHOICE -->|要求 push 或其他交付| AUTHORIZE["按明确交付目标进入统一授权｜图 4"]
  AUTHORIZE --> GRANTED{"交付授权结果"}
  GRANTED -->|允许| WHO{"执行主体：待确定"}
  GRANTED -->|拒绝或仍在等待| BLOCKED["不执行交付；保留结果和授权状态"]
  WHO -->|平台统一执行| PLATFORM["共享 Git 交付能力执行"]
  WHO -->|执行者执行| WORKER["执行者在授权范围内执行"]
  PLATFORM --> VERIFY["核实目标远端与准确 commit"]
  WORKER --> VERIFY
  VERIFY --> DELIVERED{"交付事实成立"}
  DELIVERED -->|否| PRESERVE["记录失败并保留结果，供重试或人工处理"]
  DELIVERED -->|是| RECEIPT["记录交付位置、commit 和执行结果"]
  RECEIPT --> CHOICE
  CHOICE -->|归档| CLOSE{"满足本任务保留和必要交付条件"}
  CLOSE -->|否| MISSING
  CLOSE -->|是| CLEAN["归档任务，回收可释放工作区和资源"]
  CLEAN --> END["归档后仍可定位被保留的结果与交付事实"]
```

调用者决定只保留本地结果时，必要交付条件不包含 push。已发布过某个分支名不足以证明本次 commit 已送到目标。清理策略需显式考虑未提交文件和只存在工作区中的材料，不能只凭“有 commit 字段”放行清理。

## 图 7：工作流如何只使用基础能力

责任：卡与工作流应用拥有业务状态、依赖、节点、轮次和路由。基础任务拥有运行、授权与结果，图 2—6 为共同能力。

```mermaid
flowchart TD
  TRIGGER["人工请求或协调事件触发"] --> READ["读取卡、工作流版本、当前节点与依赖事实"]
  READ --> LOCK["取得节点操作权，核对重复请求和当前状态"]
  LOCK --> READY{"业务前置条件满足"}
  READY -->|否| WAIT["记录阻塞原因，等待依赖或人工输入"]
  READY -->|是| TYPE{"步骤种类"}
  TYPE -->|人工| HUMAN["展示需要确认的业务材料"]
  HUMAN --> DECISION["记录有权人的明确决定"]
  DECISION --> ROUTE
  TYPE -->|执行任务| INPUT["组装目标、前序材料、纪律选择与接收者名称"]
  INPUT --> DISPATCH["调用共享派发｜图 2—3"]
  DISPATCH --> LINK["可靠关联节点尝试与任务身份"]
  LINK --> LINKED{"关联记录成功"}
  LINKED -->|否| REPAIR["保留任务身份并恢复关联；不重派一个未知重复任务"]
  REPAIR --> LINK
  LINKED -->|是| TASK["共享执行、交互、权限与结果｜图 4—6"]
  TASK --> RESULT["应用读取任务结果与可取得的产物"]
  RESULT --> PASS{"满足节点业务判据"}
  PASS -->|否| RETRY{"工作流允许继续或回退"}
  RETRY -->|是| NEXTTRY{"记录失败，按节点规则选择继续或新尝试"}
  NEXTTRY -->|继续原任务| CONT["调用共享任务继续能力"]
  CONT --> TASK
  NEXTTRY -->|新任务| NEWROUND["记录新尝试及其与旧任务的关系"]
  NEWROUND --> INPUT
  RETRY -->|否| HANDOFF["明确需要人处理，附阻塞点与证据"]
  PASS -->|是| RECORD["记录节点结果和业务附件关系"]
  RECORD --> ROUTE["按工作流规则选择后续节点"]
  ROUTE --> DONE{"业务流程结束"}
  DONE -->|是| END["给出业务完成证据与未交付项"]
  DONE -->|否| NEXT["记录下一步；交给后续触发或协调者决定推进"]
  NEXT --> TRIGGER
```

“可靠关联”可以通过预先身份、请求幂等或恢复机制实现，具体方案待契约阶段确定。要求是不能在派发已发生、关联写入失败后失去实际任务。工作流可以定义验收和交付要求，但必须调用共享权限、结果和交付能力；最终节点仍有未满足的必需交付时，不能报整体完成。

## 图与现有代码的对账入口

| 图 | 首先追踪的现有实现 | 当前主要差距 |
| --- | --- | --- |
| 1 | cmd/init.go、internal/initflow、internal/toolchain、scheduling.PutCarrier | init 设置 executor.default，载体登记另行进行 |
| 2 | client.Dispatch、Manager.Dispatch、admitSquadStep、scheduling.acquire | 普通派发绕过载体；小队计数后仍可覆盖落点 |
| 3 | discipline.ResolveDispatch/Compose、Manager、executor/turn、各 adapter | 多处指令来源；角色文本与固定协议可能冲突 |
| 4 | taskenv、permgate、Manager.handlePermission、handleTaskRun | 原生直通、审批范围与协调者命令尚未统一 |
| 5 | Manager.Dispatch/Continue/Done、事件消费与恢复、sessions | 任务、回合、会话和资源占用需明确对账 |
| 6 | NodeStep、ledger.AttachFile/WorkBranchPublished、workspace | 附件与交付证据不足，发布记录未精确到 commit |
| 7 | ledgerstep.Dispatcher/Runner/NodeStep、keystone | 应用仍拼运行字段；单节点执行与整体自动推进分开 |

源码链接与取证限制集中在配套模型文档，避免图集维护另一份现状事实。后续应围绕这些图逐项消除会改变可观察行为的歧义，再冻结首段契约。
