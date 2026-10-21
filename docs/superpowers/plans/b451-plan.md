# B451 实现计划：人工清单改走审批链（硬升级不动）

- 状态：**待拍板**（出稿者：plan subagent；拍板者：主会话协调者）
- 输入：`docs/superpowers/specs/b451.md`（r1，2026-10-21 用户批准，L2，执行形态甲：本地盯）；台账 `docs/superpowers/specs/b451-ledger.md`
- 基线：分支 `cards/B451-charter-1` @ `60722bef`
- 读者：零上下文执行者。§8「核实读数」列出本稿引用的代码事实和出处，动手前可以复核。

---

## 待拍板

1. **Q1：Manager 路径的「有复用腿」怎么处理。** spec 测试决定写「『有复用腿』保持断言不变」，这条在 Manager 路径上做不到：
   - Manager 路径（claude/grok/agy/codex，以及 wire 测试里的 fake adapter）只在升级出口 `escalatePermissionReasoned` 里做复用判定（`manager.go:2129`）。Consult 出口在审批者可用时直接起 `consultApprover`（`manager.go:2093-2097`），**先问审批者**，到不了复用判定。
   - `TestManualChecklistReusesSameTaskHumanAllow`（`permgate_wire_test.go:403-458`）挂着已启用的审批者，并断言「复用放行不得经 Consult 审批模型」（`:441-445`）。人工清单改成 Consult 以后，这条断言**必然变红**。
   - OpenCode 路径（`approval.Client.Request`）复用在 Consult 之前（`approval/client.go:164-168`），那里「不经审批者」照旧成立。
   - **荐 甲**：Manager 路由不动。这样符合 spec 的「Consult 出口的既有路由不变」和「和其他 Consult 请求完全一样」：今天普通 Consult 请求在 Manager 路径上也是先问审批者、后复用。复用腿改写成：审批者判 escalate → 走升级出口 → 命中同任务人工 allow 复用 → 回传 `once`，`permission_reuse`=1，`permission_request`=0。S3 的「不建第二张工单」仍然锁住；去掉的只是「不咨询审批者」这一句。代价：Manager 路径上，已有人工 allow 的同指纹人工清单命令，会先多花一次审批链调用（10–15s）。
   - 乙：在 `handlePermission` 里对 `Rule==manual-gate` 的 Consult 先跑复用判定，再问审批者。缺点有三：编排层要认判据的规则名，判据结果就不再只靠 Action 路由；人工清单和其他 Consult 走的路不一样；改动超出 spec「Consult 出口既有路由不变」的边界。不荐。

## 0. 交付目标

本轮交付 spec S1–S5，单卡一轮，不扇出（L2）。

| 故事 | 本轮做到 |
|---|---|
| S1 heredoc 内联 Python 改工作树文件 | 判据结果 Consult/`manual-gate`。审批链批准后放行：不进 `waiting_answer`，没有 `permission_request` 事件，没有待答工单，有 `approver_decision`（approve）。Manager 路径和 OpenCode 路径各锁一支 |
| S2 其余形态 | 四种形态的全部识别变体都是 Consult/`manual-gate`，没有一支是 Escalate 或 AutoAllow |
| S3 链判 escalate、链不可用、链已停用、同任务已有人工 allow | 与普通 Consult 一致：前三种建待答工单；停用时工单理由点名停用；有人工 allow 的按 Q1 裁决复用，不建第二张工单 |
| S4 人工清单与硬升级同现 | 仍为 Escalate，理由和规则名等于**该硬升级单独出现时**的裁决（见 §7 偏差 1），人工清单的 Consult 截不走 |
| S5 人工清单与只读段复合 | 永不 AutoAllow，结果是 Consult/`manual-gate` |
| 提示词 | 审批者模板在「生产环境」那一行之后加一行，文字与 spec 实现决定 3 逐字一致 |

**留待后续，本轮不做**：执行者改用 edit/write 工具降噪（spec Out of Scope，落 roadmap）；三台在役 agentd 换版（finish 另请用户批准）；B383 spec 加撤销指针（finish 文档对齐）。

## 1. 改动边界

**只改下列文件：**

| 文件 | 改什么 |
|---|---|
| `internal/permgate/manualgate.go` | `manualGateSegment` 命中后返回 `Action: Consult`（原来是 Escalate），`Rule` 保持 `RuleManualGate`。四句理由的尾巴从「保持人工裁决」改成「交审批链裁决」，前缀「人工清单：<形态>」不动。**形态识别一个字不改**：switch 各分支的判据、`hasPythonInlineFlag`、`hasRGExecuteFlag` 和 `rmTokens` 跳段规则都不动。文件头和函数注释同步改写，「为什么必须显式 Escalate」那段要改成新理由 |
| `internal/permgate/permgate.go`（`judgeBash`，`:225-322`） | 把人工清单检查从 `judgeCommand` 之前（`:292-298`）挪到 `judgeCommand` 之后、白名单之前。`judgeCommand` 结果不是 Consult 时原样返回（现状 `:302-304` 不动），然后检查人工清单，命中即返回，**不进** `safeCommandID` 和 `compoundSilent`。命中日志改词，例如「人工清单命中，交审批链裁决」。cd 基准不可证明出口、落点范围循环、rm 出口、heredoc 闭集出口一律不动 |
| `internal/orchestration/approver.go`（`approverPromptTemplate`，`:704-711`） | 在 `:709`「涉及生产环境…」之后插入 spec 实现决定 3 那一行，逐字照抄。该行不能含 `%`，否则会打乱 `fmt.Sprintf` 的四个 `%s`。模板 doc 注释加一段「2026-10-21 增补内联代码一行（B451）」 |
| 测试：`internal/permgate/manualgate_test.go`、`internal/permgate/selfcmd_test.go:185`、`internal/permgate/heredoc_test.go:68`、`internal/orchestration/permgate_wire_test.go:351-458`、`internal/orchestration/approver_test.go`（或同包新测试文件）、`internal/orchestration/approval_client_test.go`（新增一支） | 见 §3 |

**不许动**：`judgeCommand`、黑名单和执行包装器（`blacklist.go`）、`IsSelfCommand`、`closedHeredoc`/`heredocInScopeVerdict`、`rmInScopeVerdict`、`Judge` 的截断出口、`Verdict`/`Action` 类型、`approval.Authority.Judge`/`EscalateLogLevel`、`approval.Client.Request`、`Manager.handlePermission`/`consultApprover`/`escalatePermissionReasoned`，也不加配置开关。

**架构方向**：判据领域（permgate）产出 Verdict，编排领域只按 Action 路由，不认规则名。审批者提示词归 orchestration/approver。两处模板调用点（`approver.go:380` 本地链，`approver_chain.go:353` 单环 `RunLocalRing`，含「代他机裁决」端点）共用这一个常量，改一处两路都生效。

## 2. 工作顺序

1. **先锁承重假设：判定顺序的安全前提。** 不改代码，先在基线上跑 `go test ./internal/permgate/ -run 'ManualChecklist|Heredoc|SearchPattern' -count=1`，确认全绿。§8 已经读过源码、确认三件事，执行者复核：
   - rm 出口和 heredoc 闭集出口天然进不去人工清单形态。rm 出口要求每段都是 cd/rm（`rmtargets.go:148-153`）；heredoc 闭集要求消费者是裸 `cat`，其余段只能是 `cd X`，终止符后不许再有命令（`heredoc.go:84-118`、`:257-266`）。
   - `judgeCommand` 永不返回 AutoAllow（`blacklist.go:116`，函数体 `:133-165` 只有 Consult 和 Escalate 两种出口）。
   
   复核不成立时停下回主会话：这意味着挪顺序会把人工清单形态放进 AutoAllow。
2. **判据层（S2/S4/S5）**：先把 §3.1 的测试改写成新期望（会变红），再改 `manualgate.go` 和 `permgate.go`，转绿。
3. **编排层（S1/S3）**：按 Q1 的裁决改写 wire 测试的两条腿，再补 S1 的 OpenCode 路径腿和 S3 的三条腿。编排层**不改产品代码**，测试应在第 2 步的判据改动之后直接转绿；需要改产品代码才能转绿的，按 §4 升级。
4. **提示词**：加一行，补一支模板断言。
5. **本地全量**：见 §3.2。
6. **交回**：交回物首节写「偏离与自主决定」，对照 §4 逐条说明。
7. **真机**：由协调者执行，见 §3.3。

## 3. 验收方式

### 3.1 机内断言（执行者）

**判据层（`internal/permgate`）**

- **S2**：`TestJudgeManualChecklistEscalates` 改名，例如 `TestJudgeManualChecklistConsults`，期望改成 `Action==Consult && Rule==RuleManualGate`，`Reason` 含「交审批链裁决」。原有 18 个形态全部保留，另补 spec S1 的原形 `"python3 - <<'PY'\nopen('a.txt','w').write('x')\nPY"` 和 `python3 -`。
- **S2 的旁支**，以下三处期望从 Escalate 改成 Consult，规则名不变：
  - `selfcmd_test.go:185`「rg --pre 走人工门」：改为 Consult/`RuleManualGate`。
  - `heredoc_test.go:68`「python 消费正文走人工门」：改为 Consult。**spec 漏列了这一处**，同表 `:79-81` 的「不得 AutoAllow」断言保持。
  - 形态边界表 `TestJudgeManualChecklistShapeBoundaries` 不改期望，形态识别没变。注意它的 `wantEqual != 0` 把 AutoAllow（值为 0）当作「跳过」，新增断言不得沿用这种写法。
- **S4（新增反例表）**：每条断言 `Action==Escalate`，并且 `Rule`、`Reason` 等于同一个 gate 对**去掉人工清单段的参照命令**（或参照请求）判出的结果，用表里的参照列或在测试内计算都可以。不能写成「Rule != manual-gate」这种弱断言。至少包括：
  - 自指令：`python3 -c 'print(1)' && handoff card move B1 done`，参照 `handoff card move B1 done`，得 `RuleSelfCommand`。
  - heredoc 内联 Python 加自指令：命令行区 `python3 - <<'PY' && handoff card move B1 done`，正文 `print(1)`，终止符 `PY`。得 `RuleSelfCommand`。
  - 越界落点：`python3 -c 'print(1)' > /etc/x`，得「目标路径越出任务范围: …」，Rule 为空。
  - 黑名单加执行包装器：`psql -c 'drop table users'`，得黑名单规则名和「命中黑名单且含执行包装器…」。
  - eval：`eval "$X"; python3 -c 'print(1)'`，得「命令含 eval…」。
  - 截断：`Request{Tool:"bash", Command:"python3 -c 'print(1)'", Truncated:true}`，得截断理由。
  - rg --pre 加自指令：`rg --pre handoff dispatch -e x`，得 `RuleSelfCommand`。
- **S5（新增反例表）**：每条断言 `Action==Consult && Rule==RuleManualGate`，并显式断言 `Action != AutoAllow`。至少包括 `python3 -c 'print(1)' && ls`、`git checkout -- a.go; git status`、`rg --pre cat x . | head`、`psql -c 'select 1' | wc -l`，以及 `ls && python3 -`。
- **变异锚点**（供 review 做一次变异）：
  - 把人工清单检查挪回 `judgeCommand` 之前，S4 表必须变红。
  - 把它挪到 `compoundSilent` 之后，S5 表必须变红。
  - 把 Consult 改回 Escalate，S2 表必须变红。

**编排层（`internal/orchestration`）**。审批者桩用 `injectNonceForTest`/`extractNonceForTest`（`approver_test.go:54`、`:1064`）回显 nonce。不回显 nonce 的桩会被判 `Err`，走的是失败分支而不是 approve/escalate，测试会假绿。

- **S1 Manager 路径**：`TestManualChecklistEscalatesToGateTicketWithoutConsult`（`permgate_wire_test.go:354-399`）反转，改名，例如 `...ConsultsApprover`。命令用 spec S1 原形（heredoc 内联 Python），审批者桩回 approve。断言：
  - 桩被调用过；
  - adapter 收到 `manual-1:once`；
  - `approver_decision` 事件 1 条，decision=approve；
  - `permission_request` 事件 0 条；
  - 任务仍是 running，不是 `waiting_answer`；
  - 工单若存在，则已答 `allow`。审批者批准本身会建一张已答工单（`manager.go:2565-2587`），所以**不能**断言「GetTicket 报错」。

  `consultApprover` 是 goroutine，断言要等事件出现，不能只等固定时长。
- **S1 OpenCode 路径（新增）**：仿 `TestApprovalClientDoesNotInvokeOpenCodeSessionAPI`（`approval_client_test.go:411-470`），用真实 gate `newTestGate(t)`，经 `m.bindApproval(...).Request` 送同一条 heredoc 内联 Python 命令。断言：
  - `Status==ApprovalAllow && Rule=="approver"`；
  - 桩被调用过；
  - `approver_decision` approve 1 条；
  - `permission_request` 0 条。
  
  真机用的是 opencode，这条是它的机内对应物。
- **S3 Manager 路径**，命令都用同一条人工清单形态：
  - (a) 桩回 escalate：待答工单存在，`permission_request` 1 条，任务进 `waiting_answer`，`approver_decision` escalate 1 条。
  - (b) 链不可用（审批者为 nil，或 `Available()==false`）：建工单，`approver_decision` 0 条。
  - (c) 本任务已停用（白盒置 `m.apDisabled[taskID]=true`，先例见 `approver_test.go:497`）：工单请求体 `reason` 含「审批链已停用」，桩没被调用。
- **S3 复用腿**：按 Q1 的裁决改写 `TestManualChecklistReusesSameTaskHumanAllow`。荐甲的写法见 Q1：桩回 escalate，复用回传 `manual-2:once`，`permission_reuse`=1，`permission_request`=0，`permission_auto_allow`=0。
- **提示词**：新增一支，经 `Decide` 捕获 prompt（照 `TestDecidePromptContainsContext`，`approver_test.go:108-116`）。断言：
  - prompt 含新增行全文；
  - 该行位于「涉及生产环境」行之后、「涉及调用 handoff 自身 CLI」行之前；
  - prompt 不含 `%!`（Sprintf 参数错位的标志）；
  - 仍能从 prompt 抠出 nonce。

  现有测试里**没有**断言模板逐行内容或「nonce 出现两次」的用例，见 §7 偏差 4。不回归靠的是：既有 nonce 抽取测试保持绿，加上 `go vet` 的 printf 检查。

### 3.2 运行入口

- 触及包：`go test ./internal/permgate/ ./internal/orchestration/ ./internal/approval/ -count=1`
- 全量：`go test ./...`、`go vet ./internal/permgate/ ./internal/orchestration/`、`gofmt -l internal/`（必须为空）。
- 已知的环境性假红（不是回归）：`cmd` 的 `TestSessionSendActorResolution` 要用 `env -u CLAUDE_CODE_SESSION_ID` 复跑；`internal/client` 的 `TestProductionHTTPClientCallersAreGatewayOnly` 在嵌套 worktree 下会红。先按这两条复核，再判回归。

### 3.3 真机清单（**本 task 由协调者执行，不派发**）

环境：mac-mini，**隔离 agentd**。规则如下：
- 分支构建的二进制放草稿路径，不覆盖 `~/.local/bin/handoff`。
- 独立 `--config`：独立 listen 端口、独立 datadir、测试账本（本地库或测试 DSN，不碰共享 PG 和镜像租约）。
- 审批者配置为**本地链**，不读共享链。
- 在役 agentd 的进程和二进制全程不动；需要回滚在役机器的，先报告，不擅自执行。

步骤：

1. 准备一个草稿 git 仓库作为任务工作树，派一个 opencode 任务，指令两步：
   - ① 用 `python3 - <<'PY' … PY` 改工作树内的 `notes.txt`；
   - ② 用 `python3 - <<'PY' … PY` 写 `os.path.join(os.path.expanduser('~'), 'b451-probe.txt')`。路径在 Python 里拼出来，不在命令文本里写字面的家目录绝对路径，目的是让请求落到审批者而不是落点范围循环。
2. **期望**：
   - ① 隔离 agentd 日志有「人工清单命中，交审批链裁决」；`handoff show <task>` 里有 `approver_decision` approve（带 executor 和 machine），没有 `permission_request`，任务没进 `waiting_answer`，`notes.txt` 已改。
   - ② 有 `approver_decision` escalate，有 `permission_request`，有一张待答工单。人工答 reject 后，`~/b451-probe.txt` 不存在。
3. 留证：两步的事件 JSON 摘录、日志行、`ls ~/b451-probe.txt` 的输出。都记进台账。
4. 收尾：停隔离 agentd，删草稿 datadir 和草稿仓库。

**未验证，需真机**：

- (a) opencode 对 heredoc 正文里拼出来的路径会不会仍然填 `Perm.Paths`。如果会，②就由落点范围循环硬升级，没有 `approver_decision`。这时安全结论成立，但「审批者 escalate」腿没被真机证明。记下实际走的出口，回主会话决定是否接受由 wire 测试 S3(a) 覆盖。
- (b) 本地链模型对②是否真判 escalate。如果它批准了，这是**阻塞级发现**：新提示词那一行没起作用。不改提示词，回主会话。提示词文字由用户逐字批准。
- (c) opencode 是否对每条 bash 都发权限请求，取决于隔离实例下发的执行器权限配置。
- (d) CLI 怎样指向隔离实例（端口、令牌）。由协调者按 handoff skill 处理。

## 4. 决策权限

执行者**可以自行决定**（交回物逐条列出）：
- 四句理由的具体措辞。约束：保留「人工清单：<形态>」前缀，含「交审批链裁决」。
- 命中日志的措辞和级别（沿用 Info）。
- `manualGateSegment` 内部闭包和变量的命名；测试函数的命名、表结构和帮手函数。
- S4 参照值是在表里写死还是在测试内计算。
- 顺手修触及测试文件里 `wantEqual` 零值跳过的写法，前提是不改原用例语义。

**必须升级回主会话**：
- 改动任何形态识别判据（哪些命令算人工清单）。
- 改 §1「不许动」清单里的任何符号，包括为了让测试转绿去改 Manager 或 `approval.Client` 的路由。
- 提示词那一行与 spec 不逐字一致（含标点）。
- §3.1 列名之外的既有测试变红，并且需要改它的断言才能转绿。
- 第 2 步承重假设复核不成立。
- 发现人工清单形态在新顺序下可以到达 AutoAllow。
- 新增配置开关或回退开关。

## 5. 缺陷族（与本卡相关的）

- **门禁绕过**：这是主风险，挪顺序可能让人工清单形态漏进 AutoAllow。覆盖它的有：S5 表、heredoc 表既有的「不得 AutoAllow」、三处变异锚点、§2 第 1 步的复核。同一规则的所有入口：bash 只有 `judgeBash` 一处挂人工清单；非 bash 路由走 `judgeCommand(req.Text)`，本来就没有人工清单（现状），本卡不扩面。两条消费路径（Manager、OpenCode Client）都只按 Action 路由，S1 两支各锁一条。
- **假红/假绿测试**：
  - 审批者桩不回显 nonce，会落进 `Err` 分支，结果「也建了工单」，S3(a) 假绿。必须断言 `approver_decision` 的 decision 是 escalate，不能是 error。
  - 「不建工单」不能写成「GetTicket 报错」（审批批准会建已答工单）。
  - S4 不能写成「≠ manual-gate」的弱断言。
- **静默失败/误导报错**：组合命令里人工清单段和硬升级同现时，现状的理由是「人工清单…保持人工裁决」，掩盖了真正的硬升级原因（比如自指令）。新顺序会把真因暴露出来，方向正确。审计面的变化见 §7 偏差 1。
- **生命周期/状态机中断**：无新增。Consult 的异步裁决、重读任务状态、晚到裁决回 reject，都是既有路径（`manager.go:2418-2434`），本卡不碰；人工清单形态只是换到这条既有路径上走。
- **跨平台假设**：无。纯字符串判据和模板，不涉及路径、进程或平台 API；真机只选 mac-mini，是因为那里有隔离环境条件。
- **承重安全属性有测试锁住**：「人工清单永不 AutoAllow」「硬升级优先于人工清单」这两条属性分别由 S5 表和 S4 表锁住，变异锚点保证它们能变红。
- **序列化边界、枚举新值**：无。没有新字段，也没有新规则名，`manual-gate` 是已有值。

## 6. 需冻结的外部承诺

无新增冻结对象。复用的契约（均以基线 `60722bef` 为版本）：
- `permgate.Verdict{Action, Rule, Reason}` 与 `Action` 三值（`permgate.go:34-41`，AutoAllow 为零值）；
- `permgate.RuleManualGate = "manual-gate"`（`manualgate.go:25`）；
- `Manager.handlePermission` 的 Consult 出口路由（`manager.go:2090-2108`）；
- `approval.Client.Request` 的「复用 → Consult → 升级」顺序（`approval/client.go:164-190`）；
- `approverPromptTemplate` 的四个 `%s` 占位顺序：任务摘要、权限原文、nonce、nonce（`approver.go:690-711`）；
- 审批链契约（B437），版本不变。

## 7. 偏离与自主决定（plan 出稿者）

| # | 内容 | 是否在 spec 授权内 |
|---|---|---|
| 1 | **S4「理由与规则名同现状」的读法**。现状下组合命令（如人工清单 && `handoff card move`）先命中人工清单，理由和规则名是 `manual-gate`，**不是**硬升级的那个。本稿把 S4 的验收参照定为「同该硬升级单独出现时的理由与规则名」。这是 spec 实现决定 2「`judgeCommand` 判出 Escalate 时原样返回」的直接推论。可见后果是审计面变化：这类组合命令的 Rule 从 `manual-gate` 变成 `self-command`、黑名单规则名或空串，自指令升级的日志级别随之从 Info 变成 Warn（`approval/authority.go:221-226`） | 在授权内：spec 已定顺序；本条只澄清验收参照 |
| 2 | `judgeCommand` 返回 Consult 且带黑名单规则名时（仅引号内字面量命中），如果同时命中人工清单，最终裁决**整体换成**人工清单的 Consult：Rule=`manual-gate`，理由用人工清单的。黑名单规则名在 Verdict 上不保留，审批者收到的是 `ev.Text` 全文，不读 Reason | 在授权内：spec「命中即返回 Consult，规则名保留 manual-gate」 |
| 3 | 新增 S1 的 OpenCode 路径测试腿（`approval_client_test.go`）。spec 现状 1 只点了 Manager 的 Consult 出口，但真机执行器 opencode 走的是 `approval.Client.Request`，那是第二个消费者 | 在授权内：测试决定的编排层范围；只加测试，不改产品代码 |
| 4 | spec 测试决定说「提示词里 nonce 出现两次、编号对齐等既有断言不动」。基线没有断言模板逐行内容或 nonce 次数的测试，只有含权限原文/摘要（`approver_test.go:108-116`）和 nonce 抽取。本稿改为新增一支模板断言，加上 `go vet` 的 printf 检查 | 在授权内：补足，不放宽 |
| 5 | `heredoc_test.go:68` 列入必改测试（spec 漏列） | 在授权内：spec「S2 全部形态」的直接覆盖面 |
| 6 | 真机第②步要求路径在 Python 里拼出，并把 (a)–(d) 标为未验证 | 在授权内：真机设计细节 |
| 7 | Q1 列为待拍板，不自行决定 | 不适用 |

## 8. 核实读数（出稿时在基线 `60722bef` 上核对）

- `internal/permgate/manualgate.go`：
  - `RuleManualGate` 在 `:25`；`manualGateVerdict` 在 `:34-45`，按 `splitWriteSegments` 切段，`rmTokens` 判不死的段跳过。
  - `manualGateSegment` 在 `:48-84`，内含 escalate 闭包 `:49-51`，四句理由在 `:58`、`:64`、`:71`、`:74`、`:80`；`hasPythonInlineFlag` 在 `:96-106`。
  - spec 写的 `:48-82` 与此基本吻合。
- `internal/permgate/permgate.go`：
  - `Judge` 在 `:160-173`，截断出口在 `:161-164`，非 bash 路由走 `judgeCommand(req.Text)`。
  - `judgeBash` 在 `:225-322`，顺序：heredoc 剥离 `:230-234` → cd 基准不可证明则 Escalate `:240-244` → 落点范围循环 `:253-271` → rm 出口 `:281-284` → heredoc 闭集出口 `:288-291` → **人工清单 `:292-298`** → `judgeCommand` `:301-304` → 白名单 `:305-310` → 复合只读 `:314-319`。
  - spec 写的「`:292-298`，在 `judgeCommand` 之前」吻合。spec 实现决定 2 的顺序列表漏了 cd 基准出口（`:240-244`），但它在最前、不动，不影响结论。
- `internal/permgate/blacklist.go`：
  - `judgeCommand` 在 `:133-165`，doc 注释 `:116` 写「永不返回 AutoAllow」，函数体只返回 Consult 或 Escalate。
  - `execWrapperRx` 在 `:68-71`，其中通用执行标志 `-[ceE]`，所以 `psql -c`、`python -c` 带黑名单字样时属于「命中加包装器」，判 Escalate。eval 只在包装器分支内升级（`:150-159`，evalRx 在 `:155`）。
  - 推论：heredoc 内联 Python 的正文现在会被 `judgeCommand` 扫到（不命中闭集时 `judged` 是原文），正文里出现 `.eval(`、`sudo` 等字样会判成硬升级。这是 S4「硬升级不动」的正常后果，执行者不得为此开特例。
- `internal/permgate/heredoc.go`：
  - `closedHeredoc` 在 `:51-120`：终止符后不许有命令 `:84-86`，其余段只能是 `cd X` `:109-118`。
  - `validateHeredocCat` 在 `:257-300`，只认裸 `cat`（`:265-266`）。
  - `heredocInScopeVerdict` 在 `:310-335`，spec 写的 `:303-333` 与此基本吻合。
- `internal/permgate/rmtargets.go`：`rmInScopeVerdict` 在 `:84-162`，非 cd/rm 段直接否决（`:148-153`）。
- `internal/orchestration/manager.go`：
  - `handlePermission` 在 `:2057-2110`，Consult 分支 `:2090-2108`，起 goroutine `:2093-2097`。
  - `escalatePermissionReasoned` 在 `:2127-2164`，**复用判定只在这里**（`:2129-2131`）。
  - `consultApprover` 在 `:2351-2453`，`Decide(ctx, ev.Text, …)` 在 `:2365`；Err 分支升级在 `:2440`；**审批者 escalate 的升级出口在 `:2452`**（spec 写的 `:2440` 其实是 Err 分支，两处都是无理由的 `escalatePermission`，结论不变）。
  - `approvePermission` 在 `:2565-2610`，批准时会建工单并答 `allow`。
- `internal/approval/client.go`：
  - `Request` 在 `:122-191`，复用 `tryReuse` 在 Consult 之前（`:164-168`），Consult 在 `:170-173`，停用时改写理由在 `:182-190`。
  - `consult` 在 `:320`；审批者 escalate 的理由是「审批模型未形成允许」（`:406`），与 Manager 路径的无理由出口不同。两者都是现状、不动。spec S3 只描述了 Manager 路径。
- `internal/orchestration/approver.go`：
  - 模板 `:704-711`，「生产环境」行在 `:709`，自指令行在 `:710`。
  - 调用点有两处：`:380`（本地链）和 `approver_chain.go:353`（`RunLocalRing`）。依 `approver_chain.go:320` 的注释，「代他机裁决」端点也调它，所以共享链的远端环是在**环宿主机**上用它自己二进制的模板拼 prompt。部分换版期间，远端旧二进制的环不带新行。这是代码注释读数，属于 finish 换版时的观测项。
- 测试：
  - `permgate_wire_test.go` 无复用腿在 `:352-399`（函数始于 `:354`），复用腿在 `:401-458`（函数始于 `:403`）（spec 写的 `:352`、`:390` 有漂移）。
  - `selfcmd_test.go:185`；`heredoc_test.go:68`（spec 漏列）。
  - `manualgate_test.go` 共 83 行：S2 表 `:13-48`，形态边界表 `:52-83`，其中 `wantEqual` 用零值跳过，AutoAllow 刚好是零值。
  - `permgate_test.go:219-221` 只断言 rg --pre 不 AutoAllow，不受影响。
  - `blacklist_test.go:53` 直调 `judgeCommand`，不受影响。
