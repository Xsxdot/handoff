# B437 plan：共享审批链——一条链跨多机按顺序逐台尝试

> 卡 B437（L3，不扇出）· 节点 charter:plan · spec `docs/superpowers/specs/b437.md`（r2 已批准）
> 工作分支 `cards/B437-charter`（起手 HEAD `82d997c3`；本节点只在此分支提交）。任务上下文所列合并目标分支 `claude/agitated-bun-cd484f` 在本仓无本地引用（未验证）——合并目标以协调者为准，不影响本计划内容。本节点只出计划与同批台账，不写实现。
> 台账 `docs/superpowers/plans/b437-plan-ledger.md`（全部亲读/亲跑记录）。
> 凡引用行号者为 `82d997c3` 现状读数（基线图快照陈旧，行号全部对当前文件复核），动手前重核，漂了以符号为准。
> **未验证** = 本节点没亲自跑到结果；实现卡必须自己复核。读者假设：对 handoff 仓零上下文。

---

## 0. 待拍板清单

| # | 岔口 | 本计划的建议 | 影响 |
|---|---|---|---|
| **P1** | **「重复环」的判据是什么？** | **(甲) (machine, executor) 相同即拒**，model 不参与判重：同一台机器同一执行器出现两次，无论 model 异同都拒绝写入 | 选 (乙)「三元组全同才算重复」会放行 `(m,codex,gptA)/(m,codex,gptB)` 两环——同机同执行器跑两遍只是换模型名，这在 B405 语义里是候选内配置，不该用两条链环表达；且模型名今天无判据可校验（B203 OOS），放行后无法区分「有意两环」与「复制笔误」。**本计划按 (甲) 写** |
| P2 | 链读取要不要 TTL 缓存 | **不做缓存：每次裁决现读一次注册表，读失败回 last-good + WARN**（S6 语义） | 权限请求本身是秒级模型调用，一次毫秒级索引读不构成热点；TTL 缓存引入「改链后下一次裁决仍走旧链」的陈旧窗口，与 S1「下一次裁决就按新链走」直接冲突。真机清单 §9-7 若发现热点，加缓存是可逆的内部选择 |
| P3 | registry kind/id 命名 | kind=`approver_chain`，id=`default`（单链全队一份） | 纯内部命名（键值进共享账本，双端必须一致——冻结 F5）；拍板人可改名，改了 F5 同批更新 |

其余岔口本计划已按 spec 职权决断（§4 逐条带依据）；连接时限具体值是 spec 明文授权 plan 定的，见 §4.6，也列在 §10 升级面（改动须升级）。

---

## 1. 触及子系统清单（L3 增段①）

| 子系统 | 类型 | 理由 | 有界性核对 |
|---|---|---|---|
| `internal/orchestration` | 逻辑型 | 规则所有者：环/链类型 + 校验 + registry body 编解码（唯一实现）；Approver 候选泛化为环；链读取口与远程裁决口两个注入端口；单环裁决导出面（端点复用）；ApproverAttempt 加 `machine` | approver.go + 新链文件；契约面=§7 F1–F5 + 决断 §4；依赖仅 executor/config/envfile（现状不变） |
| `internal/approval` | 逻辑型 | ConsultDecision/ApproverAttempt 投影加 `machine`；payload 双投影之一（agentd-independent 边界保持） | client.go 三处类型；不加依赖 |
| `internal/agentd` | 逻辑型 | 新端点 handler（收窄接口：本机单环裁决）；env resolver 注入缝 | 新 approverapi 文件 + server.go 一行注册 + env 注入一行 |
| `internal/client` | 逻辑型 | `ApproverDecide` 方法 + 404 归一「对端不支持」（先例 client.go:526-569） | client.go 一方法 |
| `internal/proto` | 逻辑型 | `ApproverDecideReq/Resp` wire 类型（先例 proto/desktop_coordinator.go:69） | 新文件 |
| `cmd` | 逻辑型 | `handoff approver show/set/clear`；agentd 组装点接线（链读取口、远程裁决口、env 注入）；README approver 段补充 | 新 cmd/approver.go + cmd/agentd.go 接线段 |
| `internal/ledger` | **不触及** | registry 通用 KV 原语零改动（kind 只是字符串值）；设备表只读（ListDevices/DeviceTargets 既有方法） | — |
| `internal/config` / `internal/targetclient` / `internal/permgate` | **不触及** | config.Approver 段保留为回落链（S5/S6）；黑名单仍本机（spec OOS）；池零改动（远程裁决口适配器经 `srv.clientForTarget`，选路判据仍是池） | — |
| web / desktop / mobile | **不触及** | B160：审批链不进通用设置页（spec 实现决定）；approver_decision 事件加 `machine` 字段是加性 JSON，旧前端忽略未知键（B376/B405 同款先例） | — |

全部逻辑型（对端是自有 agentd，机内双组装点经真实 HTTP 可闭环）；多机行为验收走 §9 真机清单。单卡单轮不扇出（spec §首故事选择已定：五子系统改动量小，拆卡固定成本高于收益），无子卡级依赖 DAG。

---

## 2. 根因与基线事实（全部亲读/亲跑，原始输出见台账）

### 2.1 spec 现状读数复核（全部 ✓，当前行号）

1. `config.ApproverConfig`（config.go:223-229，注释 212 起）：executor 候选有序表（`ExecutorList` 标量/序列两形态，B376）、链级 `Model`、候选级 `Models`（B405）、`Timeout`（默认 60s，newDefaultConfig:524）、`Blacklist`。启动期校验（config.go:625-666）：timeout>0、候选非空串/不重复、models 键必须是候选成员、黑名单正则可编译。
2. OneShot 启动绑定：cmd/agentd.go:206-210 → `bindApproverOneShot`（:641-676）从 ads 逐候选绑，未注册/未实现 OneShot → WARN 跳过（Decide 时该候选按 error attempt 处理），全绑不上 → 启动失败。`executor.OneShotReq`（oneshot_cap.go:40-47）无任何机器概念。
3. 配置启动读一次：cmd/agentd.go:162 `NewApprover(cfg.Approver, …)` 构造后由 Manager 持有（manager.go:299-305），改链必须重启。
4. 跨机通道已备：`DeviceTableSource`（internal/agentd/devicesource.go，B439：Names/Target + 读失败回 last-good）+ `Pool.For(name)`（pool.go:151，不发网络）。

### 2.2 承重基线事实

- **单一汇合缝**：两条审批消费路径都收敛到 `m.approver.Decide(ctx, permission, summary)`——Manager.consultApprover（manager.go:2341-2447，非 OpenCode 执行器）与 approval.Client.consult 的 `Hooks.Decide`（approval_client.go:34-47，OpenCode 执行器）。**环泛化只动 Approver 一处，两条路径同时受益**。
- **per-attempt 审计是双投影**：consultApprover 对每个 attempt 各写一条 `approver_decision` 事件（manager.go:2383/2396）；payload 在 orchestration/contracts.go:174（`ApproverDecisionPayload`）与 approval/client.go:548（`approverDecisionPayload`，刻意复制避免 approval 反向 import orchestration）各有一份手写 JSON 投影；跨包经 `toApprovalAttempts` 投影（approval_client.go:57-67）。**machine 字段三处加类型、一处加投影**。
- **失败计数/停用机制已在**：`maxApproverFails=3`（manager.go:647）、`countApproverFail`（:2527）、`approver_disabled` 事件（:620/:2536）。S3 的「连续 3 次全败停用」零新机制，只补环级失败归类。
- **nonce 归属**：nonce 由**跑 OneShot 的那侧**生成并校验（randNonce approver.go:177-189、parseDecision 校验 :356、模板 :387-394 出现两次）。→ 对端端点自己生成 nonce、自己拼提示词、自己解析；wire 不含 nonce；提示词模板 + 裁决解析在 orchestration 保持唯一实现。parseDecision fail-closed（找 JSON 行、nonce 回显、decision 取值合法）。
- **grok EffortLow**：decideWithCandidate 对 `HarnessGrok` 设 `Limits.Effort=low`（approver.go:287-289）——单环裁决导出面必须保留该分支，否则对端裁决 grok 环时档位静默漂移。
- **registry 原语零改动可用**：`RegistryPut(kind,id,expectVersion,body,actor)` CAS（ledger/registry.go:31-77；expectVersion 0=新建，冲突 `ErrCASConflict`）、`RegistryGet`（:80，不存在 `ErrNotFound`）、`RegistryList`（:95）、`RegistryDelete`（:116）；域外禁直用 registry.go（文件头）→ 经 `ledgerapi.Facade`（api/registry.go:29-58）。CLI 直连账本先例：`openLedger()`（cmd/ledgercli.go:26-47，文件头写明有意不经本机 agentd HTTP）+ `ledgerActor()`。设备表读面：`ListDevices`/`DeviceTargets`/`DeviceTarget`（ledger/devices.go:183/:203/:221）。
- **旧版 404 是免费的降级信号**：agentd api mux 无兜底，未命中 /api 路径保持 ServeMux 原生 404/405、不被 SPA 吞（server.go:888-891、1024-1032 注释，B434 已依赖该形状）；client 侧 404 归一先例族：`ErrStatusUnsupported`/`ErrFootprintUnsupported`/`ErrReclaimUnsupported`（client.go:526-569）。
- **端点组装先例（B434）**：`POST /api/desktop/deliver`（server.go:997）——wire 类型在 internal/proto、独立注册函数、`withLedger` 只收真正依赖账本的路由（ledgerapi.go:77-87：无账本 503）。**本端点只跑本机 OneShot，不依赖账本 → 不挂 withLedger**；在 `s.auth` 之内（Bearer token），无需新鉴权。
- **时限先例**：client 拨号上限 `dialTimeout=10s`（client.go:54 包级 var，transport DialContext :283；`hc.Timeout=0` :327-329，超时全走 ctx）；探活预算 3s/5s（agentd/machines.go:32/:189）；**relay 冷隧道首建装不进 3s 探活预算**——Warm 预热因此存在（cmd/agentd.go:274-277）。→ 连接时限取 10s（§4.6）。
- **B439 判据复用**：本机环判据 = `cfg.IsSelfName`（selfname.go:86-91）；共享链读者只存在于「DSN+身份」机器（挂载门 cmd/agentd.go:489-496），无 DSN 机器天然走本地链（S5）。设备表自名行与 `Server.IsSelfMachine`（server.go:562）保证「按名找本机」有唯一判据，本卡不另立。
- **S5 回归钉在册**：orchestration/approver_test.go（B376 failover :896、B405 per-candidate model :1036、per-attempt 事件 :390、停用理由 :445）、approval/client_test.go（:532/:584）——基线已亲跑全绿（台账）。

### 2.3 图覆盖债

基线图快照陈旧（meta commit 18061f2c、scannedAt 2026-09-14，落后 B405/B439 合入），`validate --stale` 报 1546 条行漂移。本轮 sym 查询命中 Approver；其余接缝（devices、desktop/deliver、selfname、ledgercli、dialTimeout）直接读当前文件核实。实现卡动这些缝时以 §2 行号 + 符号名双锚，行漂以符号为准。

### 2.4 基线绿（本节点亲跑）

`go build ./...` 退出 0；`go test ./internal/orchestration/ -run 'Approver|Consult|Decide'`、`./internal/approval/`、`./internal/config/` 全 ok。原始输出见台账。

---

## 3. 故事映射与行为闭环（L3 增段②）

每条一行闭环「触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任」。实现顺序见 §5。

| 故事 | 行为闭环 | 集成与回归负责人 |
|---|---|---|
| S1 全队配一次链 | 用户 `handoff approver set -f 链.yaml`（有 DSN 机器）→ 校验（形状 + 设备表机器名，CLI）→ registry CAS 写入（kind/actor 见 §4.8）→ 各机 agentd 下一次裁决现读 → `handoff approver show` 任意机器同链同版本；改链不重启即生效；反例：未知名拒绝并点名哪一环 + 列设备表现有名字；CAS 冲突拒绝提示重读 | 实现卡（机内双库竖切）+ 协调者真机（§9-4） |
| S2 任务在 A、首环在 B：B 裁决 | A 的任务发出权限请求 → A 的 Approver 按链逐环 → 他机环经远程裁决口 POST /api/approver/decide → B 用**自己的**执行器环境跑本机 OneShot → 回 approve/escalate/error → A 放行或转下一环；审计事件 `machine=linux-01, executor=codex, model=…, elapsed_ms` | 实现卡（双组装点真实 HTTP 竖切，§6-1）+ 协调者真机（§9-1） |
| S3 前一环不行试下一环 | 首环失败（不可达/404/无执行器/超时/裁决无效）→ 每次失败各记一条 attempt（机器名 + 失败五分类）→ 下一环；某环干净裁决即停；全败 → 照旧升级人工（fail-closed），连续 3 次全败照旧 approver_disabled（机制已在，manager.go:647/2527） | 实现卡（表驱动 §6-3）+ 协调者真机（§9-2/§9-3/§9-5） |
| S4 本机环就地跑 | 环机器名 `cfg.IsSelfName` → 不经网络，本地 OneShot（解析缝 §4.9）→ 审计 `machine=<自名>`；反例同今天的本机失败语义 | 实现卡 |
| S5 无共享链照旧 | 单机/纯执行机 config.yaml `approver:` → 行为与今天逐点一致（候选、模型、时限、黑名单）；回归锁 = 既有审批测试全绿（§6-5） | 实现卡（回归责任） |
| S6 共享链生效后本地键让位 | 有 DSN 且共享链已设 → 裁决按共享链；启动 WARN 一次「本地审批链键已被共享链取代，可删除」；运行中账本读不到 → last-good 共享链继续；从未读到 → 回落本地链（无则直接升级人工），WARN 一次（进程内一次） | 实现卡（§6-4） |

---

## 4. 设计决定与接缝语义（本计划职权内的决断，逐条带依据）

### 4.1 环/链类型与校验（orchestration，一处定义）

- `ApproverRing{Machine, Executor string; Model string}`、`ApproverChain{Timeout time.Duration; Rings []ApproverRing}`。
- 校验导出面（CLI 写入与 body 解码共用）：链非空、环内 machine/executor 非空、(machine, executor) 不重复（P1 甲）、timeout 省略取 60s 且显式值必须 >0、executor ∈ `executor.SupportedHarnesses()`。
  - 依据：executor 名字集合是**代码级**事实（与装机无关），错字执行器在任何机器都不会成功——B405 对 models 错字键的启动期硬拒是同一条纪律（config.go:646-657 的 why）；「对端装没装」才是装机级事实，留给裁决时判（spec 实现决定）。
  - 「机器名在不在设备表」**不是** orchestration 校验的一部分：orchestration 不读设备表。校验函数接受调用方供给的机器名集合（`knownMachines []string` 参数），CLI 传设备表现有名（ledger/devices.go:203）。测试用 nil/自定义集合注入。
- registry body = `ApproverChain` 的 JSON 编解码也在 orchestration（规则所有者）；body 键加性演进（§7 F5）。

### 4.2 链读取口（orchestration 端口 + cmd 适配器）

- orchestration 定义端口：`Current(ctx) (ApproverChain, error)` 语义的窄接口（命名实现卡定）；`Approver` 增加可选注入；nil = 无共享链，恒用构造期本地配置链（S5 现状路径一字不改）。
- cmd 适配器：setupLedger 挂载门通过后（DSN+身份，cmd/agentd.go:489-496）经 `ledgerapi.Facade.Get(P3 kind, P3 id)` 读 body → orchestration 解码。装配时机晚于 NewManager（manager 在 :234、账本在 :246 才开）→ 沿 `SetLiveConfig` 先例（cmd/agentd.go:236）给 Manager/Approver 加装配期 setter 注入，不重构构造顺序。
- **启用门（本计划发现的关键现状缺口，必改）**：`shouldConsultApprover`（manager.go:2301-2313）在 `m.approver == nil` 时恒 false——而 `NewApprover` 对空配置返回 nil（approver.go:150-152）。若不加门，「全队只配一条链」在**本地 `approver:` 为空的机器上永远不生效**，S1 的核心承诺（配一次、其它机器什么都不配）直接落空。处置：
  - **共享挂载机（DSN+身份，判据纯 config 可在 cmd/agentd.go:162 处先知）恒构造 Approver**，链读取口等经 setter 后补注入；空配置返 nil 的 `NewApprover` 语义**保留**（S5 回归钉 approver_test.go:71），共享机构造走新增路径。
  - `Approver` 增加可用性判据 `Available()`（命名实现卡定）：本地链非空（今天语义）**或**链读取口最近一次读到的共享链非空；`shouldConsultApprover` 改为 `approver != nil && approver.Available() && !disabled`。approval.Hooks.ShouldConsult 绑同一个函数（approval_client.go:41），OpenCode 路径同享。
  - 效果：DSN 机器无任何链 → 门 false → 与今天一样直接按判据理由升级（零新事件、零计数，S5 逐字一致）；启动后第一次 `approver set` → 下一次权限请求门读到共享链 → 进裁决，**首设也不重启即生效**；本机环 OneShot 也按需经 §4.9 缝解析。
  - 门读与裁决读是两次注册表读（P2 已论证量级无虞）；门 false 时除一次读外零副作用。
- **每次裁决现读 + last-good（P2）**：读到即更新内存 last-good；读失败（含 body 解码失败）→ 用 last-good 继续 + WARN（S6「运行中读不到」）；从未读到过 → 回落本地链（有则用，无则每请求直接升级人工），进程内 WARN 一次（once 语义，重启后可再 WARN）。账本恢复后下一次裁决自动切回共享链（无粘滞）。
- S6 启动 WARN：组装点注入链读取口后、服务对外前，若共享链可读且本地 `approver.executor` 非空 → WARN 一次「本地审批链键已被共享链取代，可删除」。
- `approver clear`（CLI 删注册表行）→ 各机下一次裁决读到 ErrNotFound → **权威的「无共享链」结论：丢弃 last-good** → 回落本地链（静默，clear 是用户明示动作，回落即预期结果）。clear 与「读失败」区分开：读失败（含解码失败）持 last-good 继续用；ErrNotFound 是数据结论不是故障，清 last-good。**不区分这两者会让 clear 之后的机器永远跑在已删除的链上**——S6 断言（§6-4）钉住。

### 4.3 远程裁决口（orchestration 端口 + cmd 适配器）

- orchestration 定义端口：输入（环、permission、taskSummary、时限）→ 输出（decision approve/escalate/error、reason、model、elapsed_ms、error_kind）。nil = 他机环一律按候选失败处理（不该发生在生产：组装点必接；测试注入 fake）。
- cmd 适配器实现：`srv.clientForTarget(ring.Machine)`（server.go:617，本机名短路已被 S4 前置拦截，理论上不会命中本机分支）→ `client.ApproverDecide(ctx, req)`。池取不到客户端（设备表无此名）→ 归「不可达」，Reason 写明「设备表无此名」。
- 发起方施加裁决时限：ctx 截止 = 链 timeout（decideWithCandidate 同款 `context.WithTimeout`，approver.go:290）——**不信任对端自己收口**（r2）；HTTP 请求中断会沿 ctx 取消传播到对端 OneShot 的进程终止（`executor.OneShot` 契约「ctx 取消必须打断实际进程」，oneshot_cap.go:56）。

### 4.4 端点（agentd，收窄接口）

- 路由：`POST /api/approver/decide`，注册进 api mux（auth 之内）；**不挂 withLedger**（不读账本，无 DSN 机器也能当他机环）。独立注册函数（沿 registerLedgerRoutes/registerPushRoutes 惯例，避免 Handler 主表再膨胀）。
- handler 职责：解码请求体 → 校验（§7 F1 必填性）→ `providers.Get(executor)` + OneShot 断言（拿不到 → 应答 `error`/`no_executor`，200）→ env：组装点注入与本机 Manager 同一份 `*envfile.Resolver`（沿「审批者与执行者共用同一份 env」纪律，approver.go:91-95 的 why；setter 注入，cmd 组装点 envRes 在 :157 已构造）→ 调 **orchestration 导出的单环裁决函数**（模板/nonce/解析唯一实现，含 grok EffortLow 分支）→ 应答 200 + §7 F2。
- 边界（spec §目标架构）：端点不碰任务、不建工单、不写事件（审计归发起方）；不转发（无 byTask/forward 包装，天然一跳）；请求体没有 prompt 字段——「不把对端变成通用 LLM 代跑口」由 wire 形状保证（§7 F4）。
- 对端自己的审批链配置与本端点**无关**：端点只执行「请求点名的执行器」这一环，不递归走对端的链。

### 4.5 client 方法与错误归一

- `client.ApproverDecide(ctx, proto.ApproverDecideReq) (proto.ApproverDecideResp, error)`，沿 do/httpError 现有骨架。
- 404 → `ErrApproverUnsupported`（沿 ErrFootprintUnsupported 先例，client.go:526-569：Debug 级日志、预期结论非异常）；其余非 2xx 与传输错误原样上抛，由远程裁决口适配器按候选失败处置（§4.6 分类）。
- 不带 `X-Handoff-Forwarded`：本端点对端永不转发，无需封顶头（forward.go:8 的封顶只约束会转发的路由）。

### 4.6 时限（r2 修订落地；连接时限值 = spec 授权 plan 定）

- **裁决时限**：链文件 `timeout`（省略默认 60s，校验 >0），发起方施加于整个远程调用（本机环即 OneShot 调用本身，同今天）。不进 wire。
- **连接时限**：**10s = 复用既有 client 拨号上限 `dialTimeout`（client.go:54）**，不新增旋钮、不进链文件。依据：(a) 结构上已与裁决时限独立——拨号段受 transport Dialer Timeout 约束（client.go:283），应答等待受 ctx 截止约束，两者本就分开，r2 的「分开」诉求现状已满足；(b) 不能取 3s 探活预算——relay 冷隧道首建装不进 3s（cmd/agentd.go:274-277 预热存在的理由），取 3s 会把 relay 环的首次裁决判成不可达；(c) Tailscale 黑洞对端在 10s 内被 Dialer Timeout 掐断，「拨号挂起白等 60s」的原始痛点消除。
- **失败五分类判据**（写进 attempt.Reason，可测试的确定性判据，不是错误串猜测）：
  - `不可达`：传输错误且整体截止未到（dial refused/reset/拨号超时——拨号上限 10s < 裁决时限省略值 60s，拨号失败恒先于整体截止）；
  - `不支持`：404（ErrApproverUnsupported）；
  - `对端无此执行器`：应答 decision=error + error_kind=no_executor；
  - `超时`：整体截止已到（ctx DeadlineExceeded 或耗时 ≥ 链 timeout）；
  - `裁决无效`：应答 decision=error + error_kind=invalid_output，或应答体无法解码/非预期形状。
  - 其余（5xx、error_kind=internal、未知 error_kind）按候选失败原样记 Reason（不硬塞五分类；error_kind 未知值按向前兼容归 internal，§7 F2）。
- 链文件 timeout 校验不设下限（保持与本地 approver.timeout 同纪律：>0 即可）；用户把 timeout 配得比 10s 短时，挂起拨号会在截止处被判「超时」而非「不可达」——审计语义诚实，不为此加特判。

### 4.7 审计（事件 payload）

- 三处类型加 `Machine string \`json:"machine,omitempty"\``：orchestration ApproverAttempt（approver.go:47）、orchestration ApproverDecisionPayload（contracts.go:174）、approval.ApproverAttempt（approval/client.go:33）+ payload 投影（approval/client.go:548）+ `toApprovalAttempts`（approval_client.go:57）。
- 语义：`machine` = 本 attempt 环的机器名；空 = 本地配置链路径（旧形态不变，S5 消费者零感知）。`Model`/`ElapsedMS` 沿用既有语义：他机环取**对端回报**的 model 与 elapsed（发起方不重算）。
- 序列化边界自查：payload 是手写 JSON 投影、事件落本机任务事件库（无跨语言投影、无编码转换）——「区分字段缺失与零值」由 omitempty 语义承担（machine 缺失 = 旧事件/本地链），双投影漏改由 §6-6 断言钉住。

### 4.8 CLI（cmd/approver.go）

- `handoff approver set -f <链.yaml>`：读文件 → yaml 解码（time.Duration 原生支持 `60s`，已亲测）→ orchestration 校验（机器名集合来自本机 `openLedger()` 的设备表）→ `RegistryGet` 取当前版本（存在则 expect=version，不存在 expect=0）→ `RegistryPut`（actor=`ledgerActor()`）；`ErrCASConflict` → 拒绝并提示重读（S1）。**无 DSN → 拒绝**：`openLedger()` 在无 DSN 时回退本机 SQLite，写进去的「共享链」全队不可见——fail-closed 拒绝并提示「共享审批链需要 config.yaml 的 ledger.dsn」。
- `handoff approver show`：有 DSN → 读注册表打印链 + 版本号（JSON 或表格沿 machines 先例）；无共享链 → 明说「未设共享链」+ 本地 config approver 概要（可观测，不拒读）。
- `handoff approver clear`：CAS 删除（expect=当前版本）；无 DSN 拒绝。
- README.md approver 段（:296/:465 两处）补共享链说明（S1 用户可见面）。

### 4.9 本机环 OneShot 解析缝

- 共享链的环集合运行期可变，启动期一次性绑定表（`shots` map，cmd/agentd.go:641-676）跟不上 → Approver 增加可选解析缝 `localShot func(name string) (executor.OneShot, error)`（命名实现卡定），组装点注入 `providers.Get(name)` + OneShot 断言（providers 注册表保留全部执行器，agentd.go:202-232 注释）。
- 解析次序：`shots` 命中优先（本地配置链既有绑定，B376/B405 语义不动），未命中走 localShot 缝（共享链本机环）。两条路径在 decideWithCandidate 内汇合成同一执行逻辑；解析失败（未注册/未实现 OneShot）= 该环 error attempt（与今天「未绑定 OneShot」同语义，approver.go:266-270）。

---

## 5. 工作顺序（单卡单轮，不扇出；spec §首故事选择）

```
A  S2 竖切（承重未知）：环泛化 + 启用门（§4.2，共享机恒构造）+ 单环裁决导出面
     + 端点 + client 方法 + 审计 machine
     —— 链读取口用测试替身注入（fake 链 = 一条含他机环的链）
B  S1 共享链存取：registry kind + CLI show/set/clear + 组装点链读取口注入 + S6 启动 WARN
C  S3/S4 收尾：远程失败归类落 §4.6 五分类 + last-good/回落分支 + S5 回归全绿
```

- A → B：**类型依赖**（B 复用 A 的环/链类型与解码），同卡内顺序施工，无跨卡硬依赖。
- B → C：**施工排序**（C 的 last-good/回落需要 B 的链读取口落地；C 的五分类需要 A 的远程裁决口）。
- 每段完成即有可检查结果：A = 双组装点竖切测试绿；B = CLI 对真实（测试）库 set/show/clear + CAS 冲突绿；C = 表驱动失败归类 + 回归全绿。

---

## 6. 断言清单（spec 测试决定落地）

1. **S2 竖切（承重未知验证）**：两个 agentd 组装点（各自 fake OneShot、各自 token）经**真实 HTTP**（httptest.NewServer 形状——sessionsapi_test.go:4 的「空壳冒充接线」禁令）：发起方注入 fake 链（首环 = 他机名）→ 权限请求 → 对端 fake 裁决 approve → 断言：(a) 发起方任务事件含 approver_decision 且 `machine=对端名、executor/model/elapsed_ms` 来自对端回报；(b) 对端收到的请求体**没有 prompt 字段**、permission/task_summary 原样；(c) 对端未写任何事件。
2. **链校验表驱动**：空链、(machine,executor) 重复（含 model 异同两态）、executor 非法名、machine 空串、未知机器名（knownMachines 注入）、timeout ≤0 / 省略 → 各自错误点名哪一环。
3. **环失败归类表驱动**：远程裁决口适配器注入故障替身——不可达/404（ErrApproverUnsupported）/no_executor/超时（对端挂起 > 截止）/invalid_output → 各归 §4.6 分类、转下一环；干净裁决即停不试后续环。
4. **S6 回落顺序与启用门**：链读取口注入 + 共享链已设 → 本地键让位（Decide 按共享链）+ 启动 WARN 一次；读失败 → last-good 继续；从未读到 → 本地链（有）/直接升级人工（无）+ WARN 一次；clear（ErrNotFound）→ **丢弃 last-good** 回本地链，不滞留死链。启用门：本地 approver 为空的共享机在链首设后**不重启**即开始裁决（首设即生效）；DSN 机器无任何链 → 不进 consult、升级理由与今天逐字一致（S5 延伸钉）。
5. **S5 回归锁**：§2.2 点名的既有审批测试（orchestration/approver_test.go、approval/client_test.go 全量）原样全绿——本地配置链路径行为零变化。
6. **端点断言**：无 token 401；providers 无该执行器/未实现 OneShot → error/no_executor；对端 fake 返回不可解析输出 → error/invalid_output；端点 handler 全程零 AppendEvent。
7. **序列化边界**：(a) 旧结构解码带 machine 的新事件不失败、machine 缺失（旧事件）读出空串——缺失与零值在本 wire 里同义且文档化（§4.7）；(b) registry body 经 **SQLite 与 PG 两种后端**真实写读往返恒等（沿 ledger 双后端测试形态，ddl_parity_test.go 同族）；(c) §7 F4 的 JSON 键名冻结断言（marshal 逐键比对）。
8. **CLI 对真实库**：set → show 同链同版本（测试内两把 store 句柄模拟两机视角）；并发 set 第二把 expect 旧版本 → ErrCASConflict 文案提示重读；未知名 → 拒绝并列出设备表现有名字；无 DSN → set/clear 拒绝、show 报「未设共享链」。

---

## 7. 冻结断言（条件节——新增对外兼容承诺）

**本卡新增一个跨进程 wire 契约（agentd↔agentd 代他机裁决）+ 一份跨机共享注册表 body**。以下 5 条一条 = 一支可独立判 pass/fail 的断言；实现选择（私有命名、handler 内部结构）不入。

| # | 断言 | 产 → 消 |
|---|---|---|
| F1 | `POST /api/approver/decide` 请求体 JSON：`permission`（必填非空）、`executor`（必填非空）、`task_summary`（可空）、`model`（可空）；**不存在 prompt 类字段**——对端机外任何一方都无法经此端点注入任意提示词 | 发起方 agentd → 对端 agentd（新旧混跑期双向） |
| F2 | 应答 200 JSON：`decision` ∈ {approve, escalate, error}；`reason`（字符串）；`model`（对端单环裁决的模型标注，未指定 = 「默认」占位——B405 惯例，标注在对端完成、发起方原样入审计）；`elapsed_ms`（对端侧该环耗时）；`error_kind` 仅 decision=error 时出现，∈ {no_executor, invalid_output, internal}；未知 error_kind 按 internal 处理。decision=error 恒表示**该环失败**，发起方必须转下一环，绝不解释成整链失败 | 对端 → 发起方 |
| F3 | 旧版降级：对端无此路由回 404 → 发起方归一为「对端不支持」候选失败（ErrApproverUnsupported）；其余任何非 2xx / 传输错误同为该环失败、不中断链、不产生 HTTP 层重试 | 发起方 |
| F4 | 字段加性演进：F1/F2/F5 的字段只增不改名不删不改性；双侧解码用宽松 JSON（未知键忽略）；新增字段 omitempty——旧版读新版请求/应答/body 必须解码成功。键名集合由 marshal 逐键断言钉住（金样本测试） | 双端 |
| F5 | 共享链落 registry：kind=`approver_chain`、id=`default`（P3）；body = ApproverChain JSON（timeout + rings[{machine,executor,model}]）；CAS 写入（expectVersion）；kind/id 与 body 顶层键名跨二进制一致（全队任意版本读写同一行） | CLI 写 → 各机 agentd 读 |

**依赖行为查证（纪律块查证三条）**：① ServeMux 未命中 /api 路径保持原生 404 不被 SPA 吞——server.go:888-891 与 1024-1032 注释钉住，且 client.go:640-658 的 reclaim 404 消歧先例**依赖同一行为在产线已验证**；② stdlib `json.Unmarshal` 忽略未知键——stdlib 事实，F4 的金样本断言会实测钉住；③ registry CAS 版本语义——registry.go:31-77 亲读。无第三方库新行为依赖。

**无其它新增冻结对象**：Dispatch wire、proto.Task 语义、设备表 schema、mirror_lease 全部不动（复用既有契约及版本）；`machine` 审计字段为加性 JSON（旧消费者忽略），不入冻结。

---

## 8. 缺陷族对抗审查（L3 增段④）

| 缺陷族 | 设问 | 本计划的结论/锁点 |
|---|---|---|
| 静默失效 | 新链路有没有「不报错但没按预期裁决」的形态？ | 每次环尝试必落一条 approver_decision（成功失败都落，manager.go:2383 既有纪律 + machine 新字段）；S6 三分支（共享链/last-good/本地链）各有一次可见 WARN；clear 的 ErrNotFound = 权威清 last-good（§4.2，否则 clear 后机器永远跑死链）——不误报也不漏报 |
| 启用门误判 | 本地 approver 为空的机器会不会永远走不到共享链？或者反过来，无链机器突然多出裁决失败噪音？ | §4.2 启用门：共享机恒构造 + `Available()` 门（本地链非空 ∨ last-good 共享链非空）——无链机器门 false，与今天逐字一致（§6-4 延伸钉）；首设链下一次请求即生效，无需重启 |
| 双投影漂移 | machine 字段三处类型一处投影，漏改一处会怎样？ | 漏改的路径 machine 静默为空（audit 看似旧路径）——§6-6a 断言两侧路径都断言 machine 非空（他机环时）；漏改编译期不报错（同构类型复制是既定边界成本），断言是唯一防线，实现卡不得省 |
| 超时分类错位 | 拨号失败会不会被记成「超时」、对端慢会不会被记成「不可达」？ | §4.6 判据以「整体截止是否已到」二分，不猜错误串；拨号上限(10s) < 默认裁决时限(60s) 保证拨号失败先于截止。用户把 timeout 配 <10s 时挂起拨号记「超时」——语义诚实，文档说明，不加特判 |
| 并发 | 两机同时 set 链？对端同时接多个裁决？ | CAS 拒后写者（F5，ErrCASConflict 文案提示重读，§6-8）；对端并发 = 各自独立 OneShot 进程，与今天本机多任务并发裁决同构，无新共享状态 |
| 序列化 | body/事件跨 PG/SQLite、跨版本往返丢字段？ | F4 金样本键名断言 + §6-7b 双后端真实往返；approver_decision 落本机事件库（无跨机投影），machine 加性 omitempty |
| 兼容/回退 | 新旧混跑团队？ | 旧版对端 = 404 → 不支持 → 下一环（F3）——混跑期为可见降级（audit 记「不支持」），不静默；新版发起方 + 旧版链写方不存在（链只有新版 CLI 能写）；旧版发起方不认识新端点（它根本不发起远程裁决）——降级方向单向安全 |
| 权限/安全 | 端点会不会被滥用成通用 LLM 代跑口？ | wire 无 prompt 字段（F1）+ 提示词模板只在对端机内拼接（orchestration 唯一实现）+ Bearer token（对端 token 即权限边界，与今日 agentd API 同一面）；对端审计只落自身 slog 日志（spec：不写事件） |
| 资源 | 每权限请求一读注册表、对端并发 OneShot 会不会成热点？ | P2 已论证（毫秒级索引读 vs 秒级模型调用）；对端 OneShot 进程数 = 并发裁决数，受 prochost 围栏既有约束；真机 §9-7 复核量级（未验证，需真机） |
| 身份/旧数据 | 链里残留已删机器名？ | 写链时校验设备表（S1 反例），但设备表事后删除不追改链——该环按「不可达（设备表无此名）」逐请求失败转下一环（§4.3），fail-closed 不静默放行；不为此做链自动重写（OOS：健康探测/跳过已在 roadmap） |

---

## 9. 真机清单（机内 mock 不能顶替的行为验收，全部「未验证，需真机」）

**纪律（spec 测试决定 + 本次补充）**：一律**隔离实例**——独立 datadir/端口/config/repo_root、`sync.auto=false`、连测试库或隔离 schema；**不碰共享账本与 mirror_lease**；**在役 agentd 二进制与进程绝不替换/重启**。协调者在真机上起隔离实例执行；本节点只写清单。

1. **S2**：隔离实例 A（mac-mini）任务触发权限请求，链首环 = 隔离实例 B（linux-01）的执行器 → B 裁决；A 的任务事件含 `machine=linux-01` 与对端回报的 model/elapsed。
2. **S3-停机**：停掉 B 的隔离实例进程 → 该环记「不可达」→ 转第二环；全环失败 → 升级人工；再连发 2 次全败 → `approver_disabled` 落事件。
3. **S3-旧版**：一台隔离实例跑升级前旧二进制作首环 → 记「不支持」→ 下一环。
4. **S1**：A 机 `approver set`，B 机 `approver show` 同链同版本；改链（加一环）后 B 机**不重启**，下一次裁决按新链走。
5. **连接时限**：首环指向黑洞机器名（Tailscale 掉线形态）→ 日志计时确认在 10s 内记「不可达」转下一环，不白等裁决时限。
6. **relay 形态环**（队内有 relay 机器时）：冷隧道（隔离实例刚起、未预热）首环裁决是否在连接时限内成功——**未验证，需真机**（§4.6 依据 (b) 的实证）。
7. **资源量级**：真实负载下每次权限请求一读注册表 + 对端并发 OneShot 进程数的开销观察；若发现热点，按 P2 备注升级加缓存。

---

## 10. 决策权限（交回物「偏离与自主决定」的对照基准）

- **实现卡可自行调整**：内部命名（端口接口名、setter 名、`localShot` 缝名、测试文件组织、fake 位置）；§4 未涉及的局部私有函数与遍历顺序；P2 缓存的**追加**（真机发现热点时升级讨论后可加，默认不加）；setter 注入 vs 构造参数重排（不改对外语义）；CLI show 的输出版式。
- **必须升级**：F1–F5 任一条语义变化（含 kind/id 改名——P3）；连接时限 10s 改值；失败五分类增删改；链校验规则变化（重复环判据 P1、executor 词法校验、timeout 默认值）；启用门语义变化（§4.2：共享机恒构造、`Available()` 判据、门 false 时的零副作用承诺）；任何削弱 fail-closed 的选择（error 环被解释成放行、404 被静默吞掉、端点接受 prompt 类字段）；`machine` 字段从加性变破坏性；S6 回落顺序变化。
- **本计划未自行拍板的**：P1–P3（§0）。其余 §4 各条均给出 spec 依据或本仓先例行号，拍板人推翻时只动对应段落的工作，不波及其它段。

---

## 11. 自审（五项法定）

1. **故事/步骤覆盖**：S1–S6 全部映射（§3）+ 首故事竖切（§5-A 对应 spec §首故事选择）；无孤儿故事；S5 是纯回归锁。
2. **待验证假设有动作与阻塞范围**：承重未知（单环裁决逻辑能否原样搬到端点后面、对端模型/耗时可否穿透回发起方审计）= §6-1 竖切第一优先验证；relay 冷隧道连接时限 = §9-6；资源量级 = §9-7。
3. **决策权限写清**：§10。
4. **L3 四件**：子系统清单（§1，含派卡资格核对——不扇出故不派）、故事映射+闭环（§3）、工作顺序与依赖分型（§5：类型依赖 vs 施工排序）、缺陷族审查（§8）。待拍板清单在稿首（§0）。
5. **冻结节有结论**：§7 五条 + 「无其它新增冻结对象」说明 + 依赖行为查证三条。

红线自查：本节点未写实现代码、未派发、未调用 handoff CLI、未起新 executor；所有结论均亲读/亲跑或标「未验证」；依赖输入（spec r2）已在场且已读；临时验证文件写 $TMPDIR。
