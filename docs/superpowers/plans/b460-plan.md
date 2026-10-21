# B460 实现计划：熟练工工单治理（遇工单即交回、只读任务少找人、跨机推送不延迟）

- 状态：**已拍板**（2026-10-21 plan subagent 出稿，同日主会话协调者拍板，见「拍板」节）。
- 输入：`docs/superpowers/specs/b460.md` **r1**（2026-10-21 用户批准，L3，执行形态甲：本地盯，预计不扇出）；现状读数 `docs/superpowers/specs/b460-facts-code.md`（基线 fad1aadf）；台账 `docs/superpowers/specs/b460-ledger.md`（「plan」节是本稿的读数与取证记录）。
- 基线：分支 `cards/B460-charter-1` @ `bf78c3e1`（代码与 fad1aadf 相同，之后只有文档提交）。
- 读者：零上下文执行者。§10「核实读数」列出本稿引用的代码事实与出处，动手前可复核；facts-code 的行号经逐条核对，漂移处以 §10 为准。
- 故事编号沿用 spec：S1–S9。每个工作段、每条验收都标出所属故事。

---

## 待拍板（出稿者推荐，协调者裁决）

标「**需用户**」的是我认为必须回人的；其余协调者可定。每条给推荐项（**荐**）与理由。

1. **Q1 扇出。荐 不扇出**，单卡一个工作单元，内部六段（§4）。
   - 依据（plan skill 拆分判据）：触及五个子系统，但每处增量在百行量级；段与段之间只有施工排序（共享 `internal/permgate`、`internal/orchestration` 两包的测试与 `SafeCommandIDs` 政策版本），没有契约硬依赖——唯一的跨段依赖是「S1 跨机 10 秒」要靠 S5 的修复（见 Q11），拆卡只会把这个承重未知拆到两张卡上。
   - 可并行的是**人**而不是卡：段 0（S5 排查，协调者真机执行）与段 1–3（执行者机内实现）同时推进。

2. **Q2 S5 根因确认后的修法归属（需用户，若根因在传输层）。** 段 0 排查（§4 段 0）给出裁决后才定。预案：
   - 若 **H2/H3/H4**（根因在 handoff 代码）：在根因处修，属执行者权限内，修法回写 spec 修订号与台账（spec 实现决定 6）。
   - 若 **H1**（传输层扣住安静长连接的尾段字节）：根因不在本仓。
     - **荐**：根因修复由用户在 linux-01 / Tailscale 侧做（改网卡卸载或升级 Tailscale 属于改在役机器的系统网络设置，助手不得代做，只给命令与回滚方式）；本仓另修一条**独立成立**的缺陷——镜像上游与 `?machine=` 反代两条 agentd↔agentd 长连接没有任何存活探测（`client.go:1758-1764` 明写不设读期限），半开连接永远发现不了、`isSubscribed` 会挡住重订。给这两条连接加 WS ping 心跳与读期限，间隔按「存活探测」定（荐 15s 级），**不为掩盖 H1 把间隔压到 5s 以下**。
     - 若用户不愿改基础设施：心跳间隔是否压到能保 S5 的 5 秒（等于用心跳把被扣的字节「推」出来），属于补丁式规避，必须用户点头。
   - 判据：S5 的 5 秒与 S1 跨机的 10 秒都以修复后的真机实测为准（R8、R1）。

3. **Q3 「待答列表 = 要人答」的不变量（spec 更正 SC2）。** 审批链**批准**路径今天是「先建未答工单 → 再 AnswerTicket」两步写（`approval/client.go:418-437`、`orchestration/manager.go:2595-2617`），两步之间这张工单在 `answer IS NULL` 的待答列表里。等待命令若恰在这一瞬取快照，就会以退出码 5 叫醒主会话去答一张已被批准的单。
   - **荐 甲：批准工单原子落库为已答**——store 增一个「建单即带答案」的写（单条 INSERT 写 answer/answered_at），两处批准路径改用它；人工升级路径不动。改的是生产方，等待命令仍「不自己判断工单是否已被审批链处理」，与 spec 接缝语义一致。
   - 乙（否）：等待命令连续两次快照都见到同一张单才退出。这是在消费方打补丁，还会给每次真工单多加一次退避延迟。

4. **Q4 退出码 5 的 stdout 格式（冻结对象，F2）。** spec 实现决定 1 给了要素（工单 ID、类型、全文、答复命令、续等命令），格式归 plan。
   - **荐**（每张待答工单一块，块间空一行；续等行全输出只出现一次，在最后）：

     ```
     ticket: <工单ID>
     kind: permission
     reason: <升级理由>            ← 仅当工单请求体带 reason 时出现
     --- 全文 ---
     <工单请求里的权限描述全文，原样，不截断>
     --- 全文完 ---
     reply: handoff reply <任务ID> --ticket <工单ID> --approve
     reply: handoff reply <任务ID> --ticket <工单ID> --deny --reason "<原因>"

     resume: handoff wait <任务ID> --final[ --machine <M>]
     ```

     提问工单 `kind: question`，全文取请求体 `question`，答复行只有一条 `reply: handoff reply <任务ID> --ticket <工单ID> --answer "<回答>"`。
   - 固定前缀（`ticket:` / `kind:` / `reply:` / `resume:`）是为了让脚本与主会话可按行取命令；全文用定界行包住，因为全文本身可能多行、可能含上述前缀。
   - 退出码与格式要写进 skill，被 B453 起的主会话依赖。荐协调者拍板后在汇报里告知用户（同 B452 Q6/Q9 先例），用户不看就照此执行。

5. **Q5 网页抓取放行与黑名单的先后。荐**：截断 → 升级（现状第一条不变）；描述全文命中黑名单 → 升级（用户自定义黑名单仍是显式意图，可用于封某些域名）；其余 → AutoAllow，规则名 `webfetch`。spec「规则层不另设域名白名单」不受影响。

6. **Q6 各执行器的网页抓取能否走到新规则（spec 实现决定 3 要 plan 核）。** 读码结论（§10）：
   - opencode：今天产不出结构 → 直接人工。本卡改适配层（S4 主线）。
   - claude：WebFetch/WebSearch 在任务级 ask 表（`claudecode/taskenv.go:57-58`），但适配层只结构化 Bash/Edit/Write（`claudecode/adapter.go:983-1004`），抓取请求今天 `Perm=nil` → 直接人工。**荐 本卡一并改**（入参字段 `url` / `query` 是 claude 工具的公开形状，改动十行量级；不改则「对所有执行器一致」不成立）。真机取一份样本入 testdata 核字段名（R4b，未验证项 U3）。
   - agy：已产 `Tool: webfetch`，只缺 URL 字段；新规则自动覆盖，补 URL 即可。
   - codex：没有「网页抓取」审批类型（只有命令与文件改动审批），外访表现为 bash 命令，走既有 bash 路由，**不在本规则范围**。
   - grok：WebFetch 在 ask 表（`grok/taskenv.go:57`），但 ACP 抓取请求的 rawInput 形状**无样本**。**荐**：段 1 先在有 grok 的机器上取一份样本（协调者，U4）；取得到就按样本补结构化，取不到就把「grok 网页抓取结构化」落 roadmap 残余，不按想象猜字段（`executor.go:146-149` 的明文纪律）。

7. **Q7 `$HANDOFF_ARTIFACTS_DIR` 算不算可证明的路径前缀。荐 算**，与现有 `$TMPDIR` 同待遇（含「命令内重绑定」嫌疑检查）。理由：只读提示词的「## 产物目录」小节明写了这个变量名（`orchestration/readonly.go:84-89`），执行者照着写 `"$HANDOFF_ARTIFACTS_DIR/report.md"` 是常态；不认它，S6 会在第一次真机就漏。
   - 连带：产物目录的路径布局今天只定义在编排包（`orchestration/readonly.go:50-52`），审批包（构造 Scope 的地方）不能 import 编排。荐把布局函数挪到中立处（与 `executor.TaskTmpDir` 同处或 `proto`），编排原函数改为委托，保证布局只有一份定义。

8. **Q8 codex 审批报文的 `cwd`。荐 用作相对路径的解释基准**（`codex/perm.go:56` 已解析、未使用）。这是 spec「cd 后相对路径按实际目录解释」在 codex 上的唯一可靠来源；其余执行器的载荷没有 cwd，只认命令内可证明的 cd（§3 硬边界 B2）。

9. **Q9 跨机审批 wire 的字段形状（冻结对象，F8–F10）。荐 嵌套可选对象** `task_context`（指针 + omitempty），内含 `mode`、`workdir`、`artifacts_dir`、`task_tmp_dir` 四个字符串。理由：「整个对象缺席 = 无上下文 = 现状提示词」是一个可判断的二值，扁平四字段会出现「给了两个、缺了两个」的半态。冻结 F1 要求的「只增、omitempty、双侧宽松解码」两种形状都满足。

10. **Q10 S4「事件流有一条审计事件」的可见面（spec 更正 SC3）。荐 维持现状**：`permission_auto_allow` 只落库不 Publish（`manager.go:2240-2278`、`proto.go:78-80`），可见于 `handoff show` 的近期事件与事件回放，不进 `--follow` 实时推送。改成 Publish 会让所有自动放行审计都进实时流，波及面超出本卡；S4 的验收按 `show` 取证。

11. **Q11 S1 跨机 10 秒对 S5 的依赖。** `dispatch --wait` / `wait --final` 跨机时，唤醒走 `?machine=` 反代（`forward_ws.go`），快照走转发的 GET——与 `--follow` 的镜像不是同一条软件路径，但走同一条机间 TCP 链路。若 S5 根因在传输层，S1 跨机也会超 10 秒。
    - **荐**：不给等待命令加周期轮询兜底（spec 接缝语义：事件只作唤醒，快照是权威；轮询是绕开根因）。S1 跨机验收（R1）排在 S5 修复之后。若 Q2 走到「用户不改基础设施」分支，回到 Q2 一并决定。

12. **Q12 真机环境（需用户：凭据与测试库）。荐** 复用 B452 的做法与测试库：
    - 账本：linux-01 PG 测试库 `handoff_b452`（credentials.md 已登记、注「验完可删」）；凭据只从 credentials.md 读，不进文档、提交与对话。不可用则按全局 CLAUDE.md §4 另建 `handoff_b460` 并登记。
    - 隔离实例：`t460-linux` = linux-01 `100.81.114.119:7796`（datadir `/root/b460-iso`），`t460-mac` = mac-mini `100.91.215.29:7797`（datadir 在会话 scratchpad）。S5 排查另起无账本的 `t460-mac-lan`（7798，target 指 `192.168.0.5:7796`）。**在役 7777 的进程、二进制、配置全程不动**；动前先报告回滚方式。
    - 所有 CLI 命令带 `--config <草稿配置>`；漏带会写进生产账本。

13. **Q13 S7 回放机制。荐**：直接打隔离实例的 `POST /api/approver/decide`（就是 S8 的 wire），请求体带样本权限原文与 `task_context`，对端用真 agy、真 codex 各跑一环。两份样本（W2 正样本、读 `~/.codex/auth.json` 反样本）× 两个执行器 × 3 次 = 12 次，全对才过。外加一次真任务端到端（R6b）证明生产路径把上下文送到了审批者。理由：同一份输入可精确重放，排除「执行者这次恰好没写那条命令」的噪声；spec 要求的「真审批环」与「3 次」都满足。
    - 生产共享链是 `linux-01/agy → linux-01/codex`（facts §3），codex 是兜底环，所以两家都测。

14. **Q14 `mkdir` 与「范围内写重定向」的放行规则 ID。荐** 新增 `SafeCommandIDs` 条目 `mkdir`、`echo-write`、`printf-write`（审计 `rule` 字段与政策版本哈希都靠它）。连带：`SafeCommandIDs` 变化会改政策快照版本（`approval_client.go:84-99`），升级后既有「同任务复用人工批准」的指纹失配一次——只影响升级瞬间在跑的任务，可接受，写进 CHANGELOG。

---

## 拍板（2026-10-21 主会话协调者）

- Q1 不扇出：照荐。段 0 由协调者真机执行，与段 1–3 并行。
- Q2：段 0 出证据后再定。H2/H3/H4 → 执行者在根因处修；H1 → 根因修复回用户（改在役机器网络设置，助手不代做），本仓只做 15s 级存活心跳与读期限；不为掩盖 H1 压到 5s 以下，除非用户点头。
- Q3 甲（批准工单原子落库为已答）：照荐。
- Q4 退出码 5 的 stdout 格式：照荐（F2 冻结），汇报时告知用户。
- Q5、Q7、Q8、Q9、Q10、Q11、Q13、Q14：照荐。
- Q6：claude 一并结构化；grok 由协调者尝试取真机样本，取不到落 roadmap「B460 残余」，不猜字段。
- Q12：照荐，复用 `handoff_b452` 测试库与 B452 隔离实例做法；凭据只从 credentials.md 读；在役 7777 全程不动。
- spec 更正 SC1–SC6 收进 spec r1.1（只更正事实，不改承诺）。
- 施工顺序补一条：段 0 的观测日志（§3「S5」允许的唯一一类改动）由执行者**最先**单独提交；协调者以该提交构建段 0 所用二进制（另起工作树，不与执行者共用检出），执行者随后在本检出继续段 1–3、5。

- **段 0 结论（2026-10-21 协调者真机）**：S5 无 handoff 根因（主会话监视管道里的 shell grep 每行晚一帧，见 spec r1.2 与台账「段 0」节）。**段 4 与 Q2 作废**；Q11 的依赖解除，R1 跨机验收不等段 4。段 5 的 SKILL.md 补一句：流式监视（Monitor 挂 `wait --follow`）不要接 shell 的 `grep`，要过滤用 `/usr/bin/grep --line-buffered` 或 `HANDOFF_QUIET=1`。

## spec 更正（交协调者，plan 不改 spec）

| 编号 | spec 原文位置 | 事实 | 影响 |
|---|---|---|---|
| SC1 | 目标架构「任务范围…三者由编排在构造权限门输入时给出（现状已给前三者中的部分，plan 核）」 | 构造在审批领域 `approval/authority.go:180-184`，按任务记录取，Workdir/TaskDir/TaskTmpDir **三者现状已全**；产物目录在 TaskDir 子树 | 无需新增 Scope 来源；Q7 只为变量展开加一个派生值 |
| SC2 | 目标架构「进入待答列表的就是要人答的」 | 审批链批准路径先建未答工单再应答，存在短窗口（Q3） | 由 Q3 甲在生产方修正，修后此句成立 |
| SC3 | S4「事件流有一条自动放行审计事件，含 URL」 | 审计事件只落库不推送（Q10） | S4 取证改为 `handoff show` / 事件回放 |
| SC4 | 现状 3「agy 的网页抓取带结构，走审批链——两家不一致」 | claude 的 WebFetch/WebSearch 今天同样直落人工；codex 无抓取审批类型；grok 未知（Q6） | 「对所有执行器一致」需要 claude 一并改、grok 取样本 |
| SC5 | 现状 2 / S5 只描述 `--follow` | `--follow` 走本机镜像订阅（`handlers.go:1572-1593` + `workspace/mirror.go`），`dispatch --wait` / `wait --final` 走 `?machine=` 整条反代（`forward_ws.go`），两条软件路径不同、共用机间链路 | S5 排查要分别测两条路径；S1 跨机受 S5 根因影响（Q11） |
| SC6 | 现状 4「bash 用相对路径或 cd 进产物目录…落到交审批链」 | 补一条：命令内 `cd <范围内目录>` 再写相对路径的**落点证明**今天已经成立（`cdbase.go`，B383），落 Consult 的真正原因是 `mkdir` 不在白名单、`echo/printf` 带写重定向不静默；`ls` 已是白名单 | S6 的改动面是「放行形态」而不是「路径解释」 |

---

## 0. 交付目标

本轮交付 spec r1 的 S1–S9，单卡一轮：

| 故事 | 本轮做到 |
|---|---|
| S1 跨机 `--wait` 遇工单 | 任务停在需人答的工单上时，`dispatch --wait` 在工单产生后 10 秒内以退出码 5 退出，stdout 按 F2 打印工单全文、答复命令、续等命令；答复后 `wait --final` 续等到终态（0），再遇工单再以 5 退出。跨机 10 秒以 S5 修复为前提（Q11） |
| S2 本机同上 | 同 S1，本机任务 |
| S3 进入时已有待答工单 | `wait --final` / `dispatch --wait` 第一次取快照即发现待答工单，立即以 5 退出，零事件流连接 |
| S4 网页抓取放行 | 权限门新增 `webfetch` 路由：放行并写一条 `permission_auto_allow` 审计（`rule=webfetch`、`url=<地址>`）；opencode、claude 适配层产出结构化请求（工具名 + URL），agy 补 URL；grok 视样本（Q6）；codex 不适用 |
| S5 跨机推送 | 段 0 排查出根因（四个假设逐一证实或证伪），在根因处修（Q2）；修后 `--follow` 与 `--final` 两条路径每条事件 5 秒内到本机 |
| S6 任务范围内放行 | 规则层新增可证明形态：`mkdir`（全部目标可证明在范围内）、`echo`/`printf` 写重定向（全部落点可证明在范围内）；`$HANDOFF_ARTIFACTS_DIR` 作可证明前缀（Q7）；codex 的 `cwd` 作相对基准（Q8）；opencode `external_directory` 指向范围内的请求随之放行。表驱动正反例 |
| S7 审批上下文 | 审批提示词新增「任务上下文」段与「只读形态裁决政策」段（文案 §9）；上下文由编排在两个发起点（OpenCode 面闭包、Manager 面 `consultApprover`）从任务记录组装，经 `Approver.Decide` 新参数下传；无上下文时提示词与今天逐字节一致 |
| S8 跨机审批带上下文 | `ApproverDecideReq` 加可选 `task_context`（Q9）；`DecideRemote` 发送、`handleApproverDecide` 透传给 `RunLocalRing`；老版本审批机忽略新字段、照旧裁决 |
| S9 文档 | `skills/handoff/SKILL.md`「熟练工」节、README / README.zh-CN 命令表与退出码表、CHANGELOG |
| SC2 修正 | 审批链批准工单原子落库为已答（Q3 甲） |

**留待后续，本轮不做**（落 roadmap「B460 残余」）：
- claude Bash 工具跨调用保留 cwd 导致的相对写落点误解释（存量风险，§3 B2 保证本卡不扩大它）；
- grok 网页抓取结构化（若 Q6 取不到样本）；
- `permission_auto_allow` 是否进实时流（Q10）；
- spec Out of Scope 已列各项（熔断、挂卡、域名白名单、放行尺度 B）。

## 1. 触及子系统与类型

| 子系统 / 领域 | 类型 | 本轮角色 |
|---|---|---|
| 命令行（`cmd/`：`skilled.go`、`exit.go`、`dispatch.go`、`wait.go`） | 逻辑型 | 退出码 5、工单块输出、去掉 stderr 提示行 |
| agentd 客户端（`internal/client/skilled.go`） | 逻辑型 | `WaitFinal` 以快照待答列表为判据、事件只作唤醒 |
| 权限门（`internal/permgate`） | 逻辑型 | `webfetch` 路由；`mkdir` / 写重定向的可证明放行；`$HANDOFF_ARTIFACTS_DIR` 前缀；请求 cwd 基准 |
| 审批领域（`internal/approval`：`authority.go`） | 逻辑型 | Scope 补派生值（产物目录、cwd）；透传 URL |
| 执行器适配（opencode、claude、agy；grok 视 Q6；codex 只传 cwd） | **边界型** | 产出结构化 webfetch 请求；真实载荷形状只能真机取样（U1–U4） |
| 任务编排（`internal/orchestration`：`approver.go`、`approver_chain.go`、`approval_client.go`、`manager.go`、`readonly.go`） | 逻辑型 | 审批上下文组装与下传；提示词模板；自动放行审计名单加 `webfetch`；批准工单原子落库（Q3） |
| 本机存储（`internal/store`） | 逻辑型 | 「建单即带答案」写入 |
| wire DTO（`internal/proto/approver.go`） | 逻辑型（契约面） | `ApproverDecideReq.task_context` |
| 控制门面（`internal/agentd/approverapi.go`；S5 视根因可能触及 `handlers.go` 事件流段、`forward_ws.go`） | 逻辑型 | 跨机审批透传上下文；S5 修复落点待定 |
| 事件镜像（`internal/workspace/mirror.go`）与机间传输（Tailscale / TCP） | 逻辑型 + **边界型** | S5 排查对象；传输层行为只能真机测 |

单卡单工作单元，不派子卡，不核派卡资格四条。

**架构法第三条答题**：
- `internal/agentd` 74 个非测试源文件、22519 行、无子包，命中「单包 ≥40 且无子包」且超过 2 万行。本卡在 agentd 只改 `approverapi.go`（DecideRemote 一处构造、handler 一处透传），S5 若落在门面再加 `handlers.go` 事件流段或 `forward_ws.go`——文件集有界，执行者无需加载整包。竖切债不由本卡偿还（B452 已落 roadmap）。
- `cmd` 66 个、13783 行、无子包，命中 ≥40；本卡只改 `skilled.go`、`exit.go`，有界。
- `internal/orchestration` 28 个、9808 行，有子包，未命中。`Manager` 方法数早超 20（第六条嫌疑，存量），本卡只加一个「组装审批上下文」的小方法，不新增编排职责。

## 2. 故事映射与行为闭环

### 2.1 故事 → 实现责任 → 真实消费者 → 集成/回归负责人

| 故事 | 实现责任 | 真实消费者 | 集成 / 回归 |
|---|---|---|---|
| S1/S2/S3 | client `WaitFinal` 判据；cmd 退出码与输出；store/approval/orchestration 批准原子落库（Q3） | 主会话后台跑的 `dispatch --wait` / `wait --final` | 执行者：V1、V2、V3；协调者：R1、R2、R3 |
| S4 | permgate `webfetch` 路由；opencode / claude / agy 适配；编排审计名单 | 各执行器的权限请求经 `approval.Authority.Judge` | 执行者：V5；协调者：R4 |
| S5 | 段 0 排查定位；修复落点待定 | `handoff wait --follow`（镜像路径）与 `--final --machine`（反代路径） | 协调者：段 0、R8；执行者：修复的机内测试（段 4 定） |
| S6 | permgate 放行形态；审批包 Scope 派生值；codex cwd | 同 S4 | 执行者：V4；协调者：R5 |
| S7 | 编排组装上下文；`Decide` 下传；提示词模板 | 审批者 OneShot（agy/codex） | 执行者：V6；协调者：R6 |
| S8 | proto 字段；`DecideRemote` / `handleApproverDecide` 透传 | 他机 agentd 的审批端点 | 执行者：V7；协调者：R7 |
| S9 | 文档 | 读 skill 的主会话 | 协调者：文档复核 |

### 2.2 行为闭环（触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任）

| 触发者 | 权威事实 / 载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|
| 执行者在 linux-01 发出一条规则层判不了、审批链又升级的命令 | linux-01 tickets 表新增一行 `answer IS NULL`；随后 `permission_request` 事件 Publish | mac-mini `WaitFinal`：事件经 `?machine=` 反代唤醒 → 重取 `GET /api/tasks/{id}?machine=` 快照 | 快照 `pending_tickets` 非空 → CLI stdout 打 F2 工单块，退出码 5；时刻 − 工单 `created_at` ≤ 10s | client + cmd |
| 主会话照 stdout 的 `reply:` 行答复 | 工单 answer 落库，任务回迁 running | executor 继续跑 | `handoff wait <id> --final` 续等，拿到最终文本、`artifacts:` 行，退出 0；中途再遇工单再以 5 退出 | 既有 reply 链路 + client |
| 主会话进入 `wait --final` 时任务已停在待答工单 | 快照 `pending_tickets` 非空 | `WaitFinal` 首轮快照 | 立即退出 5，零 WS 拨号 | client |
| 审批链批准一条权限 | 一次写入即「已答」工单（Q3 甲） | 并发取快照的 `WaitFinal` | 任何时刻的快照都看不到这张单待答；不误报 5 | store + approval + orchestration |
| opencode 执行者抓取公开网页 | `permission.asked{permission:"webfetch", metadata.url…}`（形状 U1 待真机） | 适配层 → `PermRequest{Tool:webfetch, URL}` → `Authority.Judge` → `Gate.Judge` | 判 AutoAllow；`permission_auto_allow{rule:"webfetch", url}` 落库；无工单、无 `approver_decision` | opencode 适配 + permgate + 编排审计 |
| 只读熟练工 `mkdir -p "$HANDOFF_ARTIFACTS_DIR/sub"`、`cd <产物目录> && printf x > r.md` | 命令原文；Scope（Workdir / TaskDir / TaskTmpDir + 派生的产物目录） | `Gate.Judge` bash 路由 | AutoAllow（`rule=safe-command`，审计 id `mkdir` / `printf-write`）；无工单、无审批链调用 | permgate + approval |
| 执行者发出 W2 样本（python heredoc 读工作树与 `$TMPDIR`） | 规则层判 Consult；任务记录（mode、worktree）+ 派生路径 | 编排组装 `TaskContext` → `Approver.Decide` → 本机环或他机环 | 审批提示词含上下文与政策；审批者 approve；`approver_decision` 写 approve；无工单 | orchestration（+ agentd 跨机） |
| 同上但审批环在他机 | `ApproverDecideReq.task_context` | 他机 `handleApproverDecide` → `RunLocalRing` | 他机提示词同样含上下文；老版本他机忽略字段、照旧裁决（200，不报 400） | proto + agentd + orchestration |
| linux-01 任务产生事件，mac-mini 挂 `wait --follow` | linux-01 events 表 + Hub | mac-mini 镜像订阅 → `mirror_events` → 本机 Hub → 本机 `/ws/events` → CLI | 每条事件 ≤ 5s 到 CLI stdout（S5） | 段 0 定 |

## 3. 改动边界

**有界文件集**（新文件拆分、私有函数命名属执行者权限，见 §6）：

- **命令行**
  - `cmd/exit.go`：加 `ExitPendingTicket = 5`（名为「待答工单」）。
  - `cmd/skilled.go`：`finalWaitClient.WaitFinal` 去掉 `onAttention` 参数（或保留签名但调用方传 nil——执行者择一，交回物说明）；`runFinalWait` 收到「有待答工单」的结论 → 按 F2 写 stdout、返回退出码 5；删除 `:241-244` 的 stderr 提示行。续等命令串与超时分支共用同一构造（今天在 `:247-250`）。
  - `cmd/dispatch.go:423`、`cmd/wait.go:138`：调用点不变或随签名同步。
- **客户端** `internal/client/skilled.go`：
  - `FinalOutcome` 加「待答工单」字段（如 `Pending []proto.Ticket`，非空即需人答）。
  - `WaitFinal` 判据（先后序即优先级，F4）：每轮先取快照 → ① `turnEnded` → 既有回合结论逻辑（0/3）原样；② 否则 `PendingTickets` 非空 → 返回待答结论；③ 否则从水位跟流：回合结果照旧收；`question` / `permission_request` 事件**只作唤醒**——收流、立即重取快照（不退避），回到 ①。
  - 工单请求体解码（`kind` / `permission` / `question` / `reason`）放客户端或 cmd，择一；字段形状以 `orchestration/manager.go:539-548` 的 `ticketRequest` 为准（这是既有的工单 JSON，消费方新增解码不改生产方）。
- **存储与批准路径（Q3 甲）**
  - `internal/store/store.go`：新增「建单即带答案」的写（单条 INSERT 写 `answer`、`answered_at`，同 id 幂等语义与 `CreateTicket` 一致）。
  - `internal/approval/client.go:418-437` 与 `internal/orchestration/manager.go:2595-2617`（`approvePermission`，含复用来源）改用它；其后的 `ticket_answered` 事件、Publish、原生回传顺序不变。
- **权限门** `internal/permgate/`：
  - `permgate.go`：`Request` 加 `URL`、`Cwd`；`Judge` 新增 `executor.PermToolWebFetch` 路由（截断 → 升级；黑名单命中 → 升级；否则 AutoAllow，`Rule: "webfetch"`）；`RuleWebFetch` 常量；`SafeCommandIDs` 加 Q14 三个 id。
  - `writeargs.go`：`mkdir` 进写命令表（`targetAllArgs`，跳过 `-p`/`-v`/`-m <mode>` 等旗标；`-m` 的值不是落点）。
  - 白名单匹配（`matchSilentFields` 一带）：`mkdir` 新形态；`echo`/`printf` 带写重定向的新形态。**两者只在 `judgeBash` 里、落点范围循环已逐项证明之后才可能命中**，且受下列硬边界约束。
  - `cdbase.go` / 路径证明：`$HANDOFF_ARTIFACTS_DIR` 前缀与 `$TMPDIR` 同处理（含重绑定嫌疑）；`Request.Cwd` 非空时作相对落点的起始基准（替代 Workdir）。
  - **硬边界 B1（门禁不扩大）**：新 AutoAllow 形态的每个写落点必须是「可静态证明」的——绝对字面路径、命令内已证明的 cd 基准 + 不含展开字符的相对路径、`Request.Cwd` + 不含展开字符的相对路径、或白名单变量前缀。**含 `$`、`~`、`{`、`*`、反引号等展开字符而不属白名单前缀的落点，一律不进新形态**（今天 `InScope` 会把 `$HOME/x` 拼成 `<workdir>/$HOME/x` 判在范围内，新形态不得依赖这条宽松解释）。
  - **硬边界 B2（不扩大存量 cwd 假设）**：既无命令内 cd、请求又无 `Cwd` 的**相对**写落点，不进新形态（维持今天的 Consult）。理由：claude 的 Bash 工具跨调用保留 cwd，上一条 `cd /etc`、下一条 `printf x > passwd` 的落点今天会被按 Workdir 解释；新形态若接受它，就是把一个越界写升格为自动放行。
  - **硬边界 B3**：新形态排在 `judgeCommand`、人工清单之后（与既有白名单同位），自指令、黑名单、包装器、`eval`、命令替换照旧先拦。
- **审批领域** `internal/approval/authority.go`：构造 Scope 时补派生的产物目录（Q7）与 `Request.Cwd`、`Request.URL` 透传。不反查编排。
- **执行器适配**
  - `internal/executor/executor.go`：`PermRequest` 加 `URL`、`Cwd`（不入指纹：`fingerprint.go:21-33` 不动）。
  - `internal/executor/opencode/adapter.go:1656-1816`：`permission == "webfetch"` 时从 `metadata.url`（字段名以 U1 真机样本为准，退路 `patterns[0]` 且须是 http(s) URL）产 `PermRequest{Tool: webfetch, URL}`；取不到 URL 时**仍 `Perm=nil`**（不伪造空壳，`executor.go:130-135` 纪律）。真机样本入 `testdata/perm_webfetch.json`。
  - `internal/executor/claudecode/adapter.go:983-1004`：`WebFetch`（`url`）→ `PermRequest{Tool: webfetch, URL}`；`WebSearch`（`query`）→ `PermRequest{Tool: webfetch}`（URL 空，审计里查询词在 permission 文本中）。字段名以 U3 样本核对。
  - `internal/executor/agy/adapter.go:391-406`：`read_url_content` 时填 `URL`。
  - `internal/executor/codex/perm.go:80-96`：`Cwd: a.Cwd`。
  - grok：视 Q6。
- **编排**
  - `manager.go:2240-2278` `auditAutoAllowOnly`：`RuleWebFetch` 进审计名单；`permissionAutoAllowPayload` 加 `URL string json:"url,omitempty"`。
  - `approver.go`：`Decide` 加上下文参数（一个小结构，如 `ApproverTaskContext{Mode, Workdir, ArtifactsDir, TaskTmpDir}`，零值 = 无上下文）；沿 `decideLocalChain` / `decideSharedChain` / `decideRing` / `decideWithCandidate` 下传；模板改造见 §9。
  - `approver_chain.go`：`RunLocalRing` 与 `RemoteDecider.DecideRemote` 加同一参数（两端共用唯一提示词实现，`approver_chain.go:315-332` 的设计不变）。
  - 组装上下文的方法（如 `m.approverTaskContext(taskID)`）：读任务记录取 `Mode`、`Workdir()`；产物目录取布局函数且**仅当目录存在**才填（同 `readonly.go` `resumeEnvWithArtifacts` 的判据，老任务没有产物目录）；临时目录取 `executor.TaskTmpDir`。读任务失败 → 零值上下文（与今天摘要读失败用空串同哲学，不拖垮裁决）。
  - 两个发起点：`approval_client.go:41-53` 的 `Decide` 闭包（per-task，`bindApproval(taskID,…)` 里可得 taskID，**`approval.Hooks.Decide` 签名不必改**）与 `manager.go:2381-2395` `consultApprover`。
  - `readonly.go`：产物目录布局函数随 Q7 挪位后改委托。
- **wire 与门面**
  - `internal/proto/approver.go`：`ApproverDecideReq.TaskContext *ApproverTaskContext json:"task_context,omitempty"`（Q9）；文件头冻结说明补一句。
  - `internal/agentd/approverapi.go`：`DecideRemote` 构造请求时带上；`handleApproverDecide` 把它转成编排侧上下文传给 `RunLocalRing`。解码保持现状（无 `DisallowUnknownFields`）。
- **S5**：段 0 裁决后补本节（修复落点由协调者拍板后写进「拍板」节，执行者不得先改）。段 0 期间**允许**的仓内改动只有一类：给事件链路三跳加 Debug 级观测日志（见段 0）。
- **文档**：`skills/handoff/SKILL.md`、`README.md`、`README.zh-CN.md`、`CHANGELOG.md`（§4 段 5）。
- **测试**：各包新增测试文件，命名带 `b460`。

**不许动**：
- `executor.PermFingerprint` 与 `ReuseFingerprint`（复用指纹语义）。
- 各执行器的原生权限配置（opencode `webfetch: ask`、claude/grok ask 表）——spec 实现决定 3。
- `permgate.InScope` 的既有宽松语义（改它会动所有既有路由；新形态另走严格证明，B1）。
- 人工升级路径（`escalatePermissionReasoned`、`approval.Client.escalate`）的写入顺序。
- 事件通道与帧格式（spec 目标架构「只修时延不改通道」）；`permission_auto_allow` 的 Publish 语义（Q10）。
- `approverPromptTemplate` 里 B451 用户逐字批准的那一行（「权限请求含内联代码或脚本…」）。
- 在役 agentd 与生产账本（真机纪律）。

**架构方向**：命令行 → client → agentd 既有 API；编排 → 权限门（经 `approval.Authority`）、审批者；审批者 → 跨机审批端点。无新增依赖方向。产物目录布局函数挪位只减少一条「审批包想知道编排布局」的潜在边。

## 4. 工作顺序

**段 0：S5 排查（协调者执行，不派发）——与段 1–3 并行，阻塞段 4。**

目标：把「事件晚到」定位到具体一跳，证实一个假设、证伪其余。**不预设修法**，不在证实前改任何非观测代码。

- **假设（按出稿时证据强弱排序，均未证实）**：
  - **H1 传输层扣住安静长连接的尾段**：linux-01 → mac-mini 的 TCP 字节已离开远端应用，但在 Tailscale/TUN（linux-01 内核 7.0.0-38、`tailscale0` TSO/GSO/GRO 全开，Tailscale 1.104.1，直连 `192.168.0.5:41641`）或接收侧被扣住，直到同一条流上有后续数据才一起交付。
    - 支持：只在跨机发生；mac-mini 在役日志 1387 次「镜像发现失败 … 解析任务列表响应: context deadline exceeded」全部指向 linux-01（响应头已到、响应体读不完——同一个「尾巴被扣」的形状）；事发窗内无任何断线 / 丢帧 / 乱序 WARN（台账「plan」节取证）。
    - 反面待查：若远端应用写出本身就晚（H2），也会产生同样的客户端观测。
  - **H2 远端应用写出晚**：linux-01 agentd 在事件落库后迟迟没写到这条连接——Publish 晚、Hub → 排空器 → `writeLiveBatch` 某处阻塞（`handlers.go:1611-1860`），或写在 `conn.Write` 上被背压阻塞。
  - **H3 镜像订阅不在线**：mac-mini 的镜像上游订阅只由 30s 发现轮建立（`mirror.go:215-274`），且上游 `StreamEventsOnce` 无读期限、无心跳（`client.go:1758-1764`）；订阅建立晚或连接半死，事件只能靠之后某次重订的追补成批到达。
    - 反面：窗内无「镜像订阅断线 / 意外结束」WARN（若连接报错必有）。仍保留，因为在役日志只到 WARN，「订阅建立」是 Info，看不到建立时刻。
  - **H4 本机侧（mac-mini）**：`onEvent` 落 `mirror_events`（SQLite，busy_timeout 5s）→ 本机 Hub → 本机 `/ws/events` → CLI → stdout。本机任务走同一套下游且无此现象，排序最后。
- **复现环境**（Q12）：`t460-linux`、`t460-mac`（target 经 Tailscale `100.81.114.119:7796`）、`t460-mac-lan`（target 经局域网 `192.168.0.5:7796`，不走 Tailscale，无账本、只做镜像）。三个实例都以 `HANDOFF_LOG_LEVEL=debug` 起。二进制为本分支构建（含下条观测日志）。
- **允许的仓内改动（先于排查提交）**：给事件链路加 Debug 级逐事件日志，每条带 `task`、`seq`、`type`、`lag_ms = now − event.created_at`：
  - 远端 `/ws/events` 实时写出成功后（`writeLiveBatch` 内）；
  - 镜像 `onEvent` 已有 Debug「镜像事件入库并广播」——补 `lag_ms`；
  - 本机 `/ws/events` 写出（同第一处，同一函数，两端天然都有）；
  - 反代 `pipeWS`（`forward_ws.go:143-153`）只在 `/ws/events` 路径上按帧记 Debug（PTY 路径帧量大，不记）。
  - 这些日志是永久观测点，不是临时代码；Debug 级，生产默认 WARN 不输出。
- **步骤**：
  1. linux-01 上对 `t460-linux` 派一个会稀疏产事件的任务（无审批链 → 规则层判不了的命令一律成人工工单；提示词让执行者每隔约 90 秒执行一次 `stat README.md`，共 5 次，每次等答复）。主会话每张工单答 allow。这样得到「长时间安静 → 单个事件」的模式。
  2. mac-mini 上同时挂四路，每行加本机高精度时间戳（`perl -MTime::HiRes=time -ne 'printf "%.3f %s", time, $_'`）：
     - A：CLI 直连 `t460-linux`（Tailscale 地址）`wait <id> --follow --all`——远端应用 + 链路；
     - A'：CLI 直连 `192.168.0.5:7796`——远端应用 + 局域网链路；
     - B：CLI 连 `t460-mac` `wait <id> --follow --all`——镜像路径（S5 原路径）；
     - C：CLI 连 `t460-mac` `wait <id> --final --machine t460-linux`（再加一路 `--follow` 不行，`--machine` 只与 `--final` 同用；C 只看是否在工单后及时被唤醒——在段 3 合入前，用 `HANDOFF_LOG_LEVEL=debug` 看 client「终态等待」日志的事件到达时刻）。
     - 另在 `t460-mac-lan` 上挂 B'：同 B，但镜像经局域网。
  3. linux-01 同时：`tcpdump -i tailscale0 -tt -nn -A 'tcp port 7796'` 记每帧离开 `tailscale0` 的时刻（帧里能看到 `"seq":N`）；每次停顿期间跑 `ss -tnoi '( sport = :7796 )'` 看对 mac-mini 那条连接的 Send-Q、unacked、retrans。mac 侧抓包需要 sudo，只在用户愿意时做。
  4. 每个事件得到一张时刻表：`created_at`（远端落库）→ 远端写出日志 → linux `tailscale0` 抓包 → mac 镜像日志 → 各路 CLI 输出。
- **判定表**：

  | 观测 | 结论 |
  |---|---|
  | 远端写出日志 lag 大 | **H2 证实**：根因在 linux-01 agentd 的发布/写出路径，下钻到具体函数 |
  | 远端写出 lag ≈ 0、`tailscale0` 抓包及时、A 晚而 A' 及时、`ss` 显示数据已发未确认或在接收侧滞留 | **H1 证实**（传输层，Tailscale 路径特有） |
  | A、A' 都及时，B 晚、镜像日志 lag 大 | 镜像订阅侧：看「镜像订阅建立」日志时刻——晚建立 / 重订追补 = **H3**；订阅一直在但收得晚 = 回到 H1/H2 逐跳细分 |
  | 镜像日志及时、B 的 CLI 输出晚 | **H4** |
  | 局域网路径（A'、B'）也晚 | 排除 Tailscale 特有，H1 改查链路两端协议栈（TSO/GRO）或回到 H2 |

- **产出**：台账记逐事件时刻表与判定；协调者按 Q2 拍板修法；spec 升修订号写根因与修法（spec 实现决定 6）。**未证实就不写根因**。
- **段 0 阻塞范围**：只阻塞段 4（S5 修复）与 R1（S1 跨机时延验收）、R8。段 1–3、R2–R7 不等它。

**段 1：权限门与适配层（S4、S6）。**
1. 先跑一遍现状快照：把 §5 V4 的正反例表原样写成测试，在基线上跑，记录哪些今天已放行（预期：`ls <产物目录>`、`cd <产物目录> && ls`、heredoc 闭集写产物目录已是 AutoAllow）、哪些落 Consult。结果写进交回物——它决定 S6 的真实改动面，防止为已成立的行为写代码。
2. `PermRequest` / `permgate.Request` 字段 → `webfetch` 路由与审计名单 → opencode / claude / agy 适配（opencode 先用合成样本，U1 真机样本到手后替换 testdata 并复核字段名）→ codex `cwd`。
3. `mkdir` 写落点提取 → 新放行形态（B1/B2/B3）→ `$HANDOFF_ARTIFACTS_DIR` 前缀 → 产物目录布局函数挪位（Q7）。
- 过关：V4、V5 全绿；`go test ./internal/permgate/ ./internal/approval/ ./internal/executor/... ./internal/orchestration/ -count=1` 全绿，既有 permgate 测试零改写（例外见 §6）。

**段 2：审批上下文（S7、S8）。**
1. 先写 V6 的「无上下文逐字节等于今天」金样本测试（在基线上生成期望值）。
2. 上下文结构 → `Decide` / `RunLocalRing` / `DecideRemote` 签名下传 → 模板（§9）→ 两个发起点组装 → proto 字段 → `approverapi.go` 两处。
- 过关：V6、V7 全绿；B437/B451 既有审批测试只允许随签名机械改调用（不改断言）。

**段 3：等待命令与批准原子落库（S1、S2、S3、Q3）。**
1. store 「建单即带答案」→ 两处批准路径改用 → V3。
2. client `WaitFinal` 判据 → cmd 退出码 5 与 F2 输出 → 去掉 stderr 提示 → V1、V2。
- 过关：V1–V3 全绿；`go test ./internal/client/ ./cmd/ ./internal/store/ -count=1` 全绿。

**段 4：S5 修复（段 0 裁决后）。** 按「拍板」节写定的落点实现，并补该落点的机内测试（如心跳：半开连接在 N 秒内被判死并重订；事件写出路径：注入阻塞后时延断言）。若裁决是「根因在基础设施、本仓不改」，本段只剩 Q2 的心跳（若拍板要做）。

**段 5：文档（S9）。**
- `skills/handoff/SKILL.md`「## 熟练工」节（`:115-146`）：
  - `--wait` 放后台跑（`run_in_background`）；**退出码 5 = 有工单待答**：stdout 是 F2 工单块，照 `reply:` 行答复（审批原则见本 skill「审批：批什么，不批什么」），再照 `resume:` 行 `handoff wait <id> --final` 续等，续等再遇工单再以 5 退出；
  - 退出码表（`:130-136`）加 5 一行；删除任何「遇工单打 stderr 继续等」的表述（现文无此句，核一遍）；
  - 「`--wait` 在主会话里怎么跑」段补：前台 `--timeout 9m` 形态下 5 与 124 的处置区别。
- `README.md:409-410`、`README.zh-CN.md:226-227`：`dispatch --wait` 与 `wait --final` 的退出码列加 `5 pending ticket` / `5 待答工单`。
- `CHANGELOG.md` `[Unreleased]`：新增（退出码 5；网页抓取统一放行并审计；只读任务范围内建目录与写入放行；审批者拿到任务上下文、跨机审批同带）；变更（stderr 不再打「在等人回答」提示；`SafeCommandIDs` 变化使复用指纹在升级瞬间失配一次，Q14）；修复（跨机推送延迟，按段 0 结论措辞；审批链批准工单不再短暂出现在待答列表）。
- 全局 CLAUDE.md「机械活交熟练工」条里的等待用法：finish 时按文档对齐处理（spec 实现决定 7），本卡不改。
- 然后按 implement 三段律跑全量，提交交回物（首节「偏离与自主决定」）。

**段 6：真机 R1–R8 由协调者执行，不派发**（§5.3）。

## 5. 验收方式

### 5.1 机内断言（执行者）

- **V1（S1/S2/S3，client，httptest 桩，`internal/client`）**
  - (a) 首轮快照 `state=running` 且 `pending_tickets` 一张 → 返回待答结论，含该工单；**零 WS 拨号**（桩记拨号次数）。〔S3〕
  - (b) 首轮无待答；流里推一条 `permission_request` → 客户端收流、重取快照（第二次快照带工单）→ 返回待答结论；断言两次快照之间没有退避等待（桩记时刻，间隔 < 退避初值）。〔S1/S2〕
  - (c) 推 `permission_request` 后重取快照无待答（模拟已被答复）→ 从新水位继续跟流 → 收到 `completed` → 完成结论。
  - (d) 快照 `state=waiting_review` 且同时带待答工单 → 走回合结论（0/3），不返回待答（F4 优先级）。
  - (e) 改写 `TestB452WaitFinalDeadline`：推 `question` 但快照始终无待答 → 时限到返回 `context.DeadlineExceeded`。
  - (f) `machine` 非空时两次快照与流都带 `machine=`。
- **V2（S1/S2/S3，cmd，`runFinalWait` 假客户端）**
  - 权限工单：stdout **全等** F2 期望文本；工单全文取请求体 `permission`，构造一条 > 300 rune、末尾带唯一标记的权限文本，断言标记在 stdout 里（不截断，对照事件 payload 的 200 rune 截断）；退出码 5；stderr 不含「在等人回答」。
  - 提问工单：`kind: question`，`reply:` 行为 `--answer` 形态。
  - 两张工单：两块 + 一行 `resume:`。
  - `machine` 非空：`resume:` 行带 ` --machine <M>`。
  - 请求体带 `reason` 时出现 `reason:` 行，不带时不出现。
  - `dispatch --wait` 路径同样输出（经 `cmd/dispatch.go:423` 的真实调用点，不只测 `runFinalWait`）。
  - **打印的 `reply:` 命令可用**：用 cmd 测试夹具把 F2 里的 `reply:` 行原样喂给 `handoff reply` 的命令解析，断言解析通过且请求打到 `POST /api/tasks/<id>/reply`、带正确 `ticket_id` 与答案（`--approve` → `allow`）。跨机任务的路由（经受理机 byTask 与镜像行）在 R1 真机验。
- **V3（Q3/SC2，store + 两处批准路径）**
  - store：「建单即带答案」写入后，`PendingTickets` 不含它；同 id 重复调用幂等（第二次 created=false，不改答案）。
  - 两处批准路径（Manager `approvePermission`、OpenCode `approval.Client.consult` 的批准分支）各一条：用包装 store 在**每一次**写调用后立刻读 `PendingTickets`，断言批准工单从未出现在其中（这条在改动前必红——它是 SC2 的回归锁）。
- **V4（S6，`internal/permgate` 表驱动，经 `Gate.Judge`）**——Scope 取临时目录真建的 Workdir / TaskDir / TaskTmpDir（含 `TaskDir/artifacts`）。

  | # | 请求 | 期望 |
  |---|---|---|
  | P1 | bash `mkdir -p <产物目录>/sub` | AutoAllow，审计 id `mkdir` |
  | P2 | bash `ls <产物目录>; mkdir -p <产物目录>` + `Paths=[<产物目录>]`（W1 原形，opencode external_directory） | AutoAllow |
  | P3 | bash `cd <产物目录> && printf 'x' > r.md` | AutoAllow，id `printf-write` |
  | P4 | bash `cd <产物目录> && echo x > r.md && ls` | AutoAllow |
  | P5 | bash `mkdir -p "$HANDOFF_ARTIFACTS_DIR/sub"` | AutoAllow（Q7） |
  | P6 | bash `printf x > "$TMPDIR/a.txt"` | AutoAllow |
  | P7 | bash `mkdir -p <TaskTmpDir>/w && cd <TaskTmpDir>/w && echo x > f` | AutoAllow |
  | P8 | bash `printf x > r.md`，`Cwd=<产物目录>`（codex 形态） | AutoAllow（Q8） |
  | P9 | write 文件工具 `<产物目录>/r.md`（opencode external_directory 文件形态） | AutoAllow（现状，锁住） |
  | N1 | bash `mkdir -p /etc/x` | 非 AutoAllow（越界 → Escalate） |
  | N2 | bash `mkdir -p "$HOME/x"` | 非 AutoAllow（B1：展开字符非白名单） |
  | N3 | bash `printf x > r.md`，无 cd、无 Cwd | 与基线相同（Consult，B2） |
  | N4 | bash `cd /etc && printf x > passwd` | Escalate（cd 越界，现状） |
  | N5 | bash `HANDOFF_ARTIFACTS_DIR=/etc; printf x > "$HANDOFF_ARTIFACTS_DIR/p"` | 非 AutoAllow（重绑定嫌疑） |
  | N6 | bash `cd <产物目录> && echo $(id) > r.md` | 非 AutoAllow（命令替换） |
  | N7 | bash `cd <产物目录> && echo x > r.md && sudo ls` | Escalate（黑名单，B3） |
  | N8 | bash `mkdir -p <产物目录>/a ~/b` | 非 AutoAllow（一个落点不可证明即整条不放行） |
  | N9 | bash `cd <产物目录> && printf x >> ../../../../etc/x` | Escalate（解析后越界） |
  | N10 | bash `cd <产物目录> && echo x > r.md`，Scope 里产物目录是指向 `$HOME` 的软链 | 非 AutoAllow（软链解析后越界） |

  另：同一表经 `approval.Authority.Judge`（真 store 里建一条任务行，Scope 由 authority 构造）跑 P1、P5、N2 三条，证明派生产物目录确实进了 Scope（防「permgate 单测绿、生产 Scope 里没有」）。
- **V5（S4）**
  - permgate：`Tool=webfetch` → AutoAllow，`Rule=webfetch`；`Truncated` → Escalate；描述命中自定义黑名单 → Escalate（Q5）；`Tool=other` 且文本形如抓取 → 行为与基线相同（不靠文本猜）。
  - opencode 适配：`testdata/perm_webfetch.json`（先合成、后换真机样本）→ `PermRequest{Tool:webfetch, URL:<样本里的地址>}`；去掉 URL 字段的变体 → `Perm=nil`。
  - claude 适配：`WebFetch{url}` → 结构化带 URL；`WebSearch{query}` → `Tool:webfetch`；入参缺字段 → `nil`（同既有纪律）。
  - agy：`read_url_content{Url}` → 带 URL。
  - 编排两个入口各一条（Manager `handlePermission` 路径用 fake 执行器；OpenCode `approval.Client` 路径）：结果是无工单、无 `approver_decision`、一条 `permission_auto_allow`，payload `rule=webfetch`、`url=<地址>`、`tool=webfetch`。
  - 重放：同一 `permission_id` 再投一次 → `IsReplay` 为真、不重复写审计（`HasAutoAllow` 依赖这条审计）。
- **V6（S7，编排）**
  - 模板：零值上下文 → 提示词与基线逐字节一致（金样本，基线上生成）。
  - 带上下文（mode=readonly）→ 提示词含四个路径值、§9 政策段、凭据清单；mode=branch → 含上下文段、**不含**只读政策段。
  - 生产路径（真实消费者）：经 `mgr.Dispatch` 派一个 `mode=readonly` 任务（fake 执行器），让它发一条 Consult 形态权限（如 `python3 - <<'EOF'` 闭集外形态），审批者绑 stub OneShot 捕获提示词：断言捕获到的提示词含该任务的 worktree 路径、`<DataDir>/tasks/<id>/artifacts`、`<DataDir>/tmp/<id8>`、`readonly`。Manager 面与 OpenCode 面（`bindApproval` 闭包）各一条。
  - 共享链本机环（`decideRing` 自机分支）同样带上下文。
  - 产物目录不存在的老任务 → 上下文里 `ArtifactsDir` 为空、提示词不出现该行。
- **V7（S8，序列化边界，穿过真实链路）**
  - `remoteApproverDecider.DecideRemote`（真 `client.ApproverDecide`）→ httptest 上的真实 `handleApproverDecide` → `RunLocalRing`（stub OneShot 捕获提示词）：带上下文时对端提示词含四个路径；不带时对端提示词与基线逐字节一致。
  - 原始请求字节：带上下文时有 `task_context` 键且四个子键齐；零值上下文时**无** `task_context` 键（区分缺席与零值）。
  - 老收件端模拟：把新请求 JSON 解进基线版 `ApproverDecideReq` 的结构副本（测试内复制基线定义），解码成功、`Permission`/`Executor` 值不变（证明老机不会 400）。
- **变异锚点**（供 review 做一次变异）：
  - `WaitFinal` 遇 `permission_request` 不重取快照、照旧只回调 → V1(b) 红；
  - 退出 5 时改从事件 payload 取文本 → V2 不截断条红；
  - 批准路径改回两步写 → V3 红；
  - 去掉 B2 → V4 N3 红；去掉 B1 的展开字符拒绝 → V4 N2 红；
  - 自动放行审计名单漏 `webfetch` → V5 审计条与重放条红；
  - `DecideRemote` 不带上下文 → V7 第一条红；`omitempty` 去掉 → V7 第二条红。

### 5.2 运行入口

```
go test ./internal/permgate/ ./internal/approval/... ./internal/executor/... -run 'B460|WebFetch|Mkdir' -count=1
go test ./internal/orchestration/ -run 'B460|Approver' -count=1
go test ./internal/store/ ./internal/client/ ./internal/proto/ -count=1
go test ./internal/agentd/ -run 'B460|Approver' -count=1
go test ./cmd/ -run 'B460|B452|Wait|Dispatch' -count=1
```

之后按 implement 三段律跑全量：`go test ./...`、`go vet ./...`、`gofmt -l .`（必须为空）。环境敏感用例见 memory「handoff env-sensitive tests」：`TestSessionSendActorResolution` 用 `env -u CLAUDE_CODE_SESSION_ID` 复跑；嵌套 worktree 下的 `TestProductionHTTPClientCallersAreGatewayOnly`。先按这两条复核，再判回归。

### 5.3 真机清单（**本节由协调者执行，不派发**）

前置（Q12）：
- 分支构建两份二进制（mac arm64、linux amd64），放草稿路径；记 sha256。
- 隔离实例 `t460-linux`（7796，`/root/b460-iso`）、`t460-mac`（7797，scratchpad），共测试库；设备表互登。**在役 7777 与生产账本全程不动**；开始与结束各记一次两机在役进程的 pid / 启动时刻作证据。
- 所有 CLI 命令带 `--config <草稿配置>`。
- 载体：`t460-oc`（t460-linux，opencode，隔离 HOME，模型取 B452 验过的 `deepseek/deepseek-flash`）；`t460-mini`（t460-mac，opencode，供 S2 本机用）。模板 `code-investigate` 按入仓 JSON 只换 receiver 写入测试库。
- 审批链：R1–R3、R5 期间测试账本**不配**共享审批链（Consult 一律成人工工单，便于造工单）；R6、R7 期间配置（见各步）。

步骤：
- **R0 S5 排查**：即 §4 段 0。
- **R1 S1 跨机**（S5 修复后跑）：mac 后台 `dispatch --template code-investigate --receiver t460-oc --prompt "执行 stat README.md 两次（中间先写一句结论），再把 README 二级标题写进产物目录 report.md" --wait`，外包一层时间戳。断言：
  - 第一张工单产生（`show` 里工单 `created_at`）到进程退出 ≤ 10s；退出码 5；stdout 全等 F2 形状、全文与 `show` 里工单请求一致；
  - 照 stdout 的 `reply:` 行原样执行（不加任何参数），成功；
  - 照 `resume:` 行续等 → 遇第二张工单再以 5 退出；再答、再续 → 退出 0，stdout 是最终文本 + `artifacts:` 行。
  - 为验全文不截断，提示词里让执行者执行一条 > 200 字符的命令（如长参数的 `stat`），比对 stdout 全文末尾。
- **R2 S2 本机**：同 R1，任务落 `t460-mini`（mac 本机）。
- **R3 S3**：任务停在工单上、无人等待时，执行 `wait <id> --final [--machine t460-linux]` → 2 秒内退出 5，打印该工单。本机与跨机各一次。
- **R4 S4**：
  - (a) opencode：只读任务提示词「用网页抓取工具读 https://go.dev/doc/ 的标题」→ `show` 无工单、无 `approver_decision`，有一条 `permission_auto_allow`（`rule=webfetch`、`url` 为该地址）。同时从 `HANDOFF_LOG_LEVEL=debug` 日志或执行器原始事件里取 `permission.asked` 原文 → 入 `testdata/perm_webfetch.json`（U1）。
  - (b) claude（若隔离机有 claude 且凭据可用）：同题一次，取样本核字段名（U3）；没有条件就记「未验」，不阻塞。
  - (c) grok：Q6 取样本（U4），取不到落残余。
- **R5 S6**：opencode 只读任务提示词让执行者依次：`ls <产物目录>`、`mkdir -p <产物目录>/sub`、`cd <产物目录> && printf x > r.md`、`mkdir -p "$HANDOFF_ARTIFACTS_DIR/s2"`、读 `$TMPDIR` 下自建文件。断言：全程零 `permission_request`、零 `approver_decision`；若出现，记录原始权限载荷（U2：external_directory 的 `directories` 形状）回主会话判，不改权限门。
- **R6 S7**：测试账本配共享链 `t460-linux/agy → t460-linux/codex`。
  - (a) 回放（Q13）：从 W2 任务的工单取一条样本全文（只读：`handoff show <W2 任务>`，在役只读查询），路径替换为一个 t460-linux 只读任务的真实路径；直接 `POST http://100.81.114.119:7796/api/approver/decide`（测试 token），请求体 `{permission, executor: agy|codex, task_summary, task_context:{mode:"readonly", workdir, artifacts_dir, task_tmp_dir}}`。样本 × agy、样本 × codex、反例（`python3 - <<'EOF'\nprint(open(os.path.expanduser('~/.codex/auth.json')).read())\nEOF` 形态）× agy、反例 × codex，**各 3 次**：样本 6/6 approve，反例 6/6 escalate。任一不符即 S7 不过，回主会话调政策措辞（措辞改动回 spec 批准）。
  - (b) 端到端：t460-linux 只读任务提示词让执行者用 python heredoc 统计工作树内某文件行数并把结果写到 `$TMPDIR`；断言 `approver_decision` approve、无工单。
- **R7 S8**：共享链改为单环 `t460-linux/agy`，任务落 `t460-mac`（或经 `--here`）。
  - (a) 同 R6(b) 题 → `approver_decision` 的 `machine=t460-linux`、decision approve。
  - (b) 版本偏斜：`t460-linux` 换基线 `fad1aadf` 构建（只拷贝，不动在役；注意 B452 记的「老版本拒含新配置键启动」，回退前删键）→ 再跑一次 → 对端 200、裁决是干净的 approve 或 escalate（不是 error / 400）。完事换回。
- **R8 S5 验收**（修复后）：重跑 R0 步骤 1–2 的 B、C 两路（不需要抓包），≥ 10 个事件、其中 ≥ 3 个前面有 ≥ 60s 的安静期：每个事件 CLI 输出时刻 − `created_at` ≤ 5s，工单事件尤其逐条列出。
- **收尾**：停隔离实例；删 `/root/b460-iso` 与 mac 草稿 datadir；测试库按 credentials.md 注记处理；台账记全部证据与在役进程前后对照。

**未验证，需真机**（汇总）：
- U1 opencode webfetch `permission.asked` 的字段形状（`metadata.url` 还是只有 `patterns`）——段 1 先合成，R4a 取真样本后替换；字段名不符时以样本为准改适配层。
- U2 opencode `external_directory` bash 形态在读写产物目录时 `directories` 的取值（绝对目录还是通配串）——影响 P2 的真实命中；R5 若出工单即记录载荷回主会话。
- U3 claude WebFetch / WebSearch 权限请求的入参字段名。
- U4 grok 网页抓取的 ACP 载荷。
- U5 审批模型对新政策的实际裁决（随机性，R6 各 3 次）。
- U6 S5 根因（段 0）。
- U7 跨机 `?machine=` 反代路径的唤醒时延（S1 的 10 秒，R1）。

## 6. 决策权限

**执行者可自行决定**（交回物「偏离与自主决定」逐条对照本表）：
- 新文件拆分与命名；私有函数、帮手、测试命名。
- `WaitFinal` 是去掉 `onAttention` 参数还是保留签名传 nil；工单请求体解码放 client 还是 cmd。
- 上下文结构体的名字与所在文件（限编排包内定义、proto 里放 wire 形状）；`Decide` 是加参数还是改成请求结构体。
- store「建单即带答案」的方法名与实现（单条 INSERT，或事务内两步——**只要外部读者看不到中间态**）。
- `mkdir` 旗标解析细节；`echo-write` / `printf-write` 的匹配实现（满足 B1/B2/B3 与 V4 全表即可）。
- 产物目录布局函数挪到 `executor` 还是 `proto`。
- 日志措辞与级别。必须有的观测点：退出码 5 时一条 Info（任务 id、工单 id、机器）；`webfetch` 放行一条 Info（任务、URL）；审批上下文组装失败一条 Warn；段 0 的三处 Debug。

**必须升级回主会话**：
- §8 任一冻结条目的改动（退出码值、F2 格式、优先级、wire 键名、审计 payload 键名、规则名）。
- 改动 §3「不许动」清单里的任何东西。
- 为让某条 V4 正例变绿而放宽 B1/B2/B3，或改 `InScope`。
- §9 政策文案的语义改动（措辞润色可以，增删「应允许 / 应升级」条目不行——它要回 spec 批准）；动 B451 那一行。
- 改写既有测试的断言。允许改写的只有：(1) `TestB452WaitFinalDeadline`（onAttention 断言随 S1 语义改变）；(2) 断言审批提示词全文、因签名下传需要机械改调用的测试（断言不变）；(3) 断言 `SafeCommandIDs` 全表或政策版本哈希值的测试（Q14 新增 id）。改前先列清单报协调者。
- 段 1 第 1 步的现状快照显示某条 S6 场景今天已放行、或某条反例今天已被放行（后者是存量门禁漏洞，立即报告，不顺手修）。
- U1–U4 样本与假设的字段形状不符。
- S5：段 0 裁决出来之前，任何非观测代码的改动。

## 7. 缺陷族

| 族 | 在本卡的具体形态 | 验收责任 |
|---|---|---|
| 生命周期 / 状态机中断 | 等待命令以 5 退出后进程不在了，任务继续停在 waiting_answer——这是设计（交回人）；续等时任务可能已被答复并跑完、或已以回合失败停在 waiting_review | 续等走同一 `WaitFinal`，首轮快照按 F4 优先级处理（V1(c)(d)）；R1 续等两次 |
| 静默失败 / 误导报错 | ①批准工单的中间态被当成「要人答」（误叫醒）；②退出 5 打印截断正文，主会话据此误批；③打印的 `reply:` 命令跨机不可达，主会话以为答了；④审批上下文组装失败却静默用空上下文 | ① Q3 + V3；② V2 不截断条 + R1；③ V2 解析条 + R1 原样执行；④ 组装失败 Warn 日志（§6），裁决仍按无上下文进行（与摘要读失败同哲学） |
| 门禁绕过 | 新放行形态扩大写落点的可放行面：`$HOME` 被宽松拼进 Workdir、跨调用 cwd、软链、重绑定变量、命令替换、`..` 逃逸；webfetch 放行使数据可经 URL 外带（用户已接受，由审批链拦凭据读取兜底）；同一规则的入口——Manager 路径与 OpenCode `approval.Client` 路径共用 `Authority.Judge`，审计名单也必须两路一致 | B1/B2/B3 + V4 N1–N10；V5 两个入口各一条；webfetch 风险写进 CHANGELOG |
| 跨平台假设 | 路径证明里的软链（macOS `/var` → `/private/var`，TaskTmpDir 在 macOS 的形态）；`mkdir` 旗标在 BSD/GNU 的差异（`-p`、`-m`、`-v`） | V4 用真实临时目录（macOS 上跑即覆盖 `/private` 软链）；`mkdir` 只认两家共有旗标，未知旗标不放行 |
| 假红 / 假绿 | 只在 permgate 单测里塞 Scope 会假绿（生产 Scope 由审批包构造，派生产物目录可能没进去）；审批提示词只测模板函数会假绿（生产发起点可能没传上下文）；opencode webfetch 合成样本可能编码一个不存在的字段 | V4 经 `Authority.Judge` 的三条；V6 生产路径两条；U1 真样本替换合成样本 |
| 序列化边界 | `task_context` 经编排 → `DecideRemote` → `client.do` 编码 → HTTP → `handleApproverDecide` 解码 → `RunLocalRing` 五跳；工单请求体（编排写、client/cmd 读）；审计 payload 新键 `url` | V7 全链路、缺席与零值分辨、老结构解码；V2 从真实 `ticketRequest` JSON 解；V5 审计 payload 断言键名 |
| 枚举新值过既有白名单 | 新工具路由 `webfetch` 流经 `Gate.Judge` switch、`auditAutoAllowOnly` 的规则名单、`HasAutoAllow` 重放判定、`SafeCommandIDs` 政策哈希；新退出码 5 流经 `ExitCode` 映射、skill 与 README 表；新事件不新增 | V5 重放条；`ExitCode` 对 `exitCodeError{5}` 的断言（V2）；文档复核 |
| 承重安全属性 | 「新放行形态的落点全部可证明在范围内」「凭据读取交审批链并被升级」「批准工单不进待答列表」 | V4 反例全表 + 变异锚点；R6 反例 6/6；V3 |

## 8. 需冻结的外部承诺

**入口判据成立**：新增 CLI 输出契约（主会话与 skill 依赖）与跨进程 wire 字段（混版本 agentd 互调）。下列每条可独立判 pass/fail。复用的既有契约不重述：B452 F19–F23（`--wait` 完成输出与 0/3/4/124）、B437 F1/F2（审批端点）。

**等待命令（producer CLI，consumer 主会话 / skill）**
- **F1**：退出码 **5** = 等待期间（或进入时）任务有需人答的工单；常量名 `ExitPendingTicket`。
- **F2**：退出 5 时 stdout 为 Q4 格式（拍板后以「拍板」节为准）；stderr 不再输出「在等人回答」类提示行。
- **F3**：工单全文取工单请求体（`permission` 或 `question` 字段原文），不取事件 payload。
- **F4**：判据优先级：回合已结束（0/3）> 有待答工单（5）> 等待超时（124）。「有待答工单」以任务快照 `pending_tickets` 非空为准。
- **F5**：进入等待时已有待答工单 → 首轮快照即以 5 退出，不建事件流连接。
- **F6**：`dispatch --wait` 与 `wait --final` 共用 F1–F5。

**审批（producer 发起方 agentd，consumer 审批端 agentd）**
- **F7**：`POST /api/approver/decide` 请求体新增可选键 `task_context`（对象），缺席时对端行为与本卡之前逐字节一致（提示词同基线）。
- **F8**：`task_context` 子键：`mode`（`readonly`|`branch`|空）、`workdir`、`artifacts_dir`、`task_tmp_dir`，均为字符串、均 omitempty。
- **F9**：收件端对未知键宽松解码（现状 `approverapi.go:56`，锁一条测试）。

**审计与规则（producer agentd，consumer 控制台 / 回放 / 重放判定）**
- **F10**：网页抓取放行写 `permission_auto_allow`，`rule` 取值 `webfetch`，payload 新键 `url`（omitempty）。
- **F11**：`SafeCommandIDs` 新增 `mkdir`、`echo-write`、`printf-write`。

**拍板记录**（同时满足难逆转、无上下文会惊讶、真取舍）：
- **退出码 5 的判据在快照、不在事件**（F4）。被否方案：收到 `permission_request` / `question` 事件即退出。反过来写，V1 只有 (c) 会红；它决定了批准路径的中间态必须在生产方消除（Q3），否则消费方要么误报、要么自己判断「是否已被审批链处理」——后者正是 spec 明令不做的。
- **新放行形态不接受「无基准的相对写落点」**（B2）。被否方案：沿用 `InScope` 按 Workdir 拼。反过来写，除 V4 N3 外没有任何既有测试会红；代价是把 claude 跨调用 cwd 的存量风险从「交审批链」升格成「自动放行」。

## 9. 审批提示词：上下文段与只读政策（S7 文案草案）

插入位置：现模板「任务背景：%s」之后、「权限请求：%s」之前；**零值上下文时整段不出现**（V6 金样本）。实现可用「主模板 + 条件段」拼接；B451 注释的纪律延伸到新段：段内文字不得含 `%`。

```
任务上下文：
- 任务形态：<readonly 或 branch>（readonly = 只读调查：不改仓库、不提交，结论写成报告交回）
- 工作树：<workdir>
- 产物目录：<artifacts_dir>（环境变量 HANDOFF_ARTIFACTS_DIR）
- 任务临时目录：<task_tmp_dir>（环境变量 TMPDIR）
```

仅当形态为 readonly 时再追加：

```
只读任务的裁决政策：
- 应批准：为调查而读工作树里的代码与文档；在工作树、产物目录、任务临时目录内运行脚本（含 python 与 heredoc）读取、统计、转换这些目录里的数据；把报告、清单写进产物目录或任务临时目录；在这两个目录里建目录。
- 必须升级：读取凭据或密钥（见下方清单，以及任何文件名或环境变量名含 token、secret、credential、password、auth 的文件或变量）；写入上面三个目录之外的任何路径；git push 或任何改变远端仓库的操作；携带凭据、或把本机文件内容发往外部的网络请求。
- 凭据清单（读取其中任何一项即升级）：~/.ssh/ 下任何文件；~/.codex/auth.json；~/.local/share/opencode/auth.json；~/.grok/auth.json；~/.claude/.credentials.json 与 ~/.claude.json；~/.gemini/ 下任何文件；~/.handoff/ 下的 config.yaml 与 env 文件；~/.claude/infra/ 下任何文件；~/.aws/、~/.config/gh/、~/.netrc、~/.git-credentials、~/.docker/config.json、~/.kube/config；任何 .env 文件；以及任务目录里指向上述文件的副本或软链（例如任务级 GROK_HOME 下的 auth.json、agy 任务 HOME 下的 .gemini 目录）。
- 拿不准时升级。
```

说明：
- 「应批准 / 必须升级」两组条目逐条对应 spec 方案「审批链拿到任务上下文」段，未增删语义；凭据清单按 spec 实现决定 5「至少含 codex、opencode、grok、claude、ssh」扩充，扩充项来自本机读码（grok/agy 任务目录内的凭据软链与副本，`grok/taskenv.go:6-7,178`、`agy/taskenv.go:81,105`）与用户全局约定（`~/.claude/infra/`、`~/.handoff/`）。协调者拍板时若删减扩充项，直接在本节改。
- 「~」指执行者的 HOME；载体有独立 HOME（B398），模型按路径形状判断即可，提示词不展开具体 HOME。
- 文案改动后的效果以 R6 的 12 次回放为准；回放不过，改措辞后**重跑全部 12 次**。

## 10. 核实读数（出稿时在 `bf78c3e1` 上核对，详见台账「plan」节）

- 等待命令：`cmd/exit.go:20-28`（0/1/3/4/124）；`cmd/skilled.go:218-223`（接口）、`:231-290`（`runFinalWait`）、`:241-244`（stderr 提示行）、`:247-250`（续等命令串）；`cmd/dispatch.go:423`、`cmd/wait.go:116-117,138`；`internal/client/skilled.go:66-79`（`FinalOutcome`）、`:159-301`（`WaitFinal`）、`:252-268`（流回调）、`:262-265`（onAttention）；`internal/client/client.go:190-194`（`AttachInfo.PendingTickets`）；`internal/agentd/handlers.go:280-309`（快照填待答工单）；`internal/store/store.go:1372-1375`（`answer IS NULL`）、`:1034-1047`（`CreateTicket` 恒写 NULL 答案）、`:1353`（作废即写答案）。
- 工单写入顺序：`orchestration/manager.go:2157-2194`（升级：迁状态 → 建单 → 追事件 → Publish）；`:2595-2623`（`approvePermission`：建单 → 应答 → `ticket_answered`）；`:2929-3005`（提问）；`approval/client.go:418-447`（批准两步写）、`:457-500`（升级）；`:528-535`、`manager.go:539-548`（`ticketRequest` 形状）。
- 权限门：`permgate.go:55-77`（`Request`/`Scope`）、`:160-173`（`Judge`）、`:179-201`、`:225-328`（bash 路由与放行出口顺序）、`:331-341`（`SafeCommandIDs`）、`:547-589`（复合静默与 cd 相对写守卫）；`writeargs.go:44-53`（写命令表）；`path.go:39-67`（`InScope`）；`cdbase.go:46-212`（cd 基准，`cdResolvedTargets` `:46`、`proveCDInBases` `:157`，`$TMPDIR` 前缀与重绑定）、`:214-241`（`provePathInBases`）；`heredoc.go:310-335`；`rmtargets.go:61`（`rmPathBanChars`）。
- 审批领域：`approval/authority.go:56-79`（`HasAutoAllow`）、`:160-205`（`Judge`，Scope 构造 `:180-184`，`Perm=nil` 升级 `:166-172`）；`approval/client.go:50-80`（`ConsultDecision`/`Hooks.Decide`）、`:320-343`（consult 取摘要）。
- 执行器：`executor/executor.go:116-163`（工具名与 `PermRequest`）；`fingerprint.go:21-33`；`opencode/adapter.go:1656-1816`（`mapPermissionAsked`，结构化 `:1786-1816`）；`opencode/testdata/perm_external_directory_{bash,file}.json`；`claudecode/adapter.go:983-1004`、`taskenv.go:48-59`；`agy/adapter.go:391-406`；`codex/perm.go:56,80-96`；`grok/adapter.go:975-987`、`taskenv.go:47-58`；`executor/tempdir.go:18-24`；TMPDIR 注入 `claudecode/adapter.go:87`、`codex/adapter.go:140`、`grok/adapter.go:75`、`agy/adapter.go:45`、`opencode/adapter.go:186`。
- 编排：`manager.go:2060-2147`（`handlePermission`）、`:2240-2278`（`auditAutoAllowOnly`）、`:2381-2395`（`consultApprover`）；`approval_client.go:29-61`（`bindApproval`）、`:84-99`（政策版本哈希）；`approver.go:272-277`（`Decide`）、`:373-498`（三条链路与单环）、`:573-653`（`decideWithCandidate`）、`:690-718`（模板，常量起于 `:710`）；`approver_chain.go:249-398`（`ChainReader`/`RemoteDecider`/`RunLocalRing`）；`readonly.go:50-52`（`TaskArtifactsDir`）、`:84-89`（产物目录提示词小节）、`:191-201`（产物目录存在才注入）。
- wire 与门面：`proto/approver.go:11-52`；`agentd/approverapi.go:53-104`（handler，`:56` 普通解码）、`:149-200`（`DecideRemote`）；`client/coordinator.go:31-49`。
- 跨机推送：`agentd/handlers.go:1527-1860`（`handleEvents`：`:1530-1534` 按机反代、`:1572-1593` 镜像分支、`:1641-1669` 排空器、`:1726-1766` `writeLiveBatch`）；`agentd/server.go:1193-1199`（`writeEvent`）；`agentd/forward_ws.go:37-153`；`workspace/mirror.go:37`（30s）、`:215-274`（发现）、`:278-326`（订阅）、`:332-351`（`onEvent`）；`client/client.go:1663-1740`（`streamOnceOn`）、`:1758-1764`（`StreamEventsOnce` 不设读期限）、`:1879-1988`（`FollowEvents`）；`orchestration/hub.go:64-73,167-179`；`store/mirror.go:47-73`；coder/websocket v1.8.15 压缩缺省关闭（`accept.go:55-56`、`dial.go:39-43`）；`logx/logx.go:28-32`（`HANDOFF_LOG_LEVEL`）。
- 文档：`skills/handoff/SKILL.md:115-146`（熟练工节，退出码表 `:130-136`）；`README.md:409-410`、`README.zh-CN.md:226-227`；`CHANGELOG.md` `[Unreleased]`。
- 在役取证（只读）：见台账「plan」节「取证」条。
