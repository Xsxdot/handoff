# B469 审批批次台账

规格草稿见 [b469-approval-batch-spec.md](b469-approval-batch-spec.md)。基线 `a79b42867`（2026-10-23），只读核对，未改实现、未运行测试。

## 核实读数

| 主题 | 证据 | 结论 |
|---|---|---|
| 规则/审批现状 | `README.zh-CN.md:337-339`；`internal/permgate/permgate.go:142-200`；`internal/orchestration/approver.go:690-711` | 静态判据→模型→人工；模型没有拒绝权，危险/不确定请求升级。尚无带条件、来源、撤销能力的用户规则实体。 |
| 已有上下文 | `internal/orchestration/approver_context.go:26-102,128-151`；`internal/agentd/approverapi.go:155-223`；`internal/proto/approver.go:18-42` | B460/B465 已提供任务形态、工作树、产物/临时目录、readonly 政策并跨机传递，B470 应复用。 |
| 规则链非用户规则 | `internal/orchestration/approver_chain.go:33-74`；`cmd/approver.go:1-12,86-135,191-220`；`internal/config/config.go:698` | `approver` 配置/`approver set/show/clear` 管模型候选链和本地黑名单；共享审批链存账本，但不是用户 allow 规则。账本 CAS 可作持久化先例。 |
| 任务批准路径/UI | `internal/approval/authority.go:160-205`；`internal/approval/client.go:320-454`；`web/src/app/task/TicketsPanel.tsx:35-145` | Authority 判据后交审批 client；自动模型批准以已答 gate 工单落库。真实工单 UI 有逐单批准/拒绝，没有保存或管理长期规则入口。 |
| B469路径 | `internal/agentd/residentpush.go:1-24,85-118,150-190`；`internal/agentd/server.go:1588-1600,1647-1707`；`internal/hostapi/driver.go:90-103,158-200` | 驻场续轮经 residentpush/keystone/coordinatorRunner 到 hostapi；hostapi 只摘文本及 error 事件，CLI 权限事件未接入 Manager 审批链。必须保留 coordinator 身份。 |
| B463循环误判 | `internal/permgate/selfcmd.go:77-116,130-190`；`docs/superpowers/specs/b461-ledger.md:37-43`；`docs/superpowers/specs/b453-ledger.md:477` | handoff 自指令防护对搜索中的 handoff 文本有特例；真实循环搜索曾被误报。按 shell 语义修；旧 agy 故障暂缓。 |
| 限制覆盖风险 | `internal/permgate/manualgate.go:1-70`；`README.zh-CN.md:337-339` | 有必须人工升级规则；用户准许“明确授权规则”覆盖指定限制，但当前尚未指定哪些限制可覆盖，需用户裁决。 |

## 仅保留的待核项

- hostapi 是否能从每个支持的 CLI 中稳定取得完整权限请求及交互式 allow/deny 结果，要由 B469 计划复核；当前 `ocEvent.Error` 仅证明解析错误事件，不足以定义完整权限 DTO。
- 共享账本 actor 是否等同全局规则的授权管理员，当前证据未证明；若存储沿用账本，计划需确定创建/撤销权限检查。

## 草稿裁决

用户已定：当前 Codex 协调者和 gpt-6-luna 子 agent 推进；允许跨项目全局规则；明确授权可覆盖指定必须人工限制。其余范围与设计均为待审方案建议，不视为用户批准。Spec 尚待批准，唯一产品承重问题见 spec §「用户决策与唯一待裁决问题」。

## 2026-10-23 用户裁决补充

- 所列人工审批限制均允许用户逐项创建明确规则覆盖，无规则命中的人工限制转人工；用户指示继续推进。
- 实现计划不得由 luna 出稿，至少 sol；协调者选 gpt-6.1-sol。
- 前文待批准/待裁决条目为历史记录，以 spec r1.0 为准。

## 2026-10-23 r1.1 修订（取代 r1.0 范围结论）

输入：B469 #33001 全量独立审查、#33005 用户转录裁决与 B463/B458/B470 最新 #33006–33008。用户范围原话「1，原来的工单没人收，2. 同意 3. 补进去」；技术细节未视为用户批准。当前修订只读代码、改文档，未运行服务或实验。

### 当前源码复核（覆盖早前漂移读数）

| 事实 | 当前证据与纠正 |
|---|---|
| 工单路由 | `internal/agentd/wakeconsumer.go:215,502-540,746-750`：工单映射WakeTicket；origin pull空返回是card wait契约，空座上走；不能把全部空返回等同丢件。房间驻场不自动等于卡席位。 |
| pull不启回合 | `internal/keystone/keystone.go:116-138`：pull与空座no-op，不应一律改push；需补显式归属及可见待办。 |
| 复用 | `internal/approval/authority.go:248-266`：当前直接查同任务既有allow；新规则来源需贯穿grant写入/查询以校验撤销与期限。 |
| 凭据 | `internal/permgate/credential.go:75` 含`~/.handoff/config.yaml`；凭据guard只是AutoAllow→Consult，不是全体硬拒。 |
| 黑名单/包装器 | `internal/permgate/blacklist.go:38-47,49-85`：普通push与部署不是现有结构化ID；grep -c并非真实执行包装器，须语义解析。 |
| 人工清单 | `internal/permgate/manualgate.go:5-11,35-56`：B451后返回Consult，早前“硬升级”描述作废。 |
| 配置 | `internal/config/config.go:685-710`：候选列表空串/重复与models键校验，不是用户授权规则模型。 |
| 文档 | `README.zh-CN.md:375-377` 为权限分级/同任务复用说明，早前337行指向已漂移。 |
| 身份 | `internal/agentd/identity.go:34-60`：CLI也能解析user身份，device不证明人类。 |
| 历史 | `internal/ledger/registry.go:59-64` UPDATE覆盖当前值；`internal/ledger/events.go:18-27` 为事务事件追加原语，新规则事务公开面需实施前核定。 |
| 承载契约 | `internal/keysclient/keysclient.go:60-71` Runner无ctx；`internal/agentd/server.go:1581,1591-1707` 卡席位/驻场共用runner，不预定改签名。 |

### #33001 响应矩阵

| ID | 处理与落点 |
|---|---|
| C1 | 采纳用户#33005：spec S3恢复原执行者工单无人收；hostapi自身权限桥删除；明确归属、唯一责任、push/pull差异。 |
| C2 | spec限制分类表+求值顺序；普通push/deploy标本期新增分类，不能从不存在的旧ID交接。 |
| C3 | 用户同意永不可覆盖集；规则管理/账本handoff凭据含config.yaml先行；actor/device不是授权证明。 |
| I1 | S2补codex每环结果/未执行原因日志、help/env名误升；旧agy专项沿用暂缓并入roadmap。 |
| I2 | S4补四类可见原因、去重身份/重评、历史裁决、规则命中和有效期，全本期。 |
| I3 | 真正OOS已由协调者同步docs/roadmap.md；本批UI行为必交付，布局确认不是延期。 |
| I4 | spec生命周期明确规则来源grant穿过FindReuse校验，过期同撤销；独立人工同任务复用为待审推荐，UI标签不得误称单次。 |
| I5 | spec明确不可变事件历史投影；registry快照不算历史；规则CAS+审计同事务，失败不提交。 |
| I6 | spec/plan全部实验用测试账本+隔离实例，两机同样隔离，禁止生产共享账本。 |
| I7 | spec收件承重提案与plan稿首1明确原工单载体、显式责任、等待落盘退出回合释放准入、30min边界需真机。 |
| I8 | hostapi权限桥删除；仍核Runner共用路径，S3回归卡席位和驻场launch/resume，不自改冻结面。 |
| I9 | plan稿首2推荐固定版本mvdan/sh AST，待审；原误判段首do/替换内grep-c写进U2，未自称获用户批。 |
| I10 | plan三线：U2/S2先；U1/S1+任务S4后；U3原S3待P0归属/准入验证。规范化是规则硬依赖，文件冲突另标施工排序。 |
| M1 状态 | spec标题r1.1、用户原话/卡seq、范围已定与技术待审分开，无草稿/已全批混用。 |
| M2 nl原例 | S1与U1显式保留`nl -ba … | sed -n …`保存/命中/撤销/过期反例。 |
| M3 遗留 | #32980 needs_human与孤儿ses_e988…列U4，由协调者核现状处置；本次文档不谎称回收。 |
| M4 冻结 | plan将保存失败/保存后投递失败、撤销/在途、CAS/审计拆为可单独断言条目。 |
| M5 Runner | plan稿首6和既有契约节列Runner无ctx与RunTurn，变化需审查。 |
| M6 事实 | 本节复核manualgate、README375、config校验含义，覆盖r1.0漂移。 |

### 稿首六建议响应

1. 修改后采纳：承重实验辨launch/resume与auto/HOME全放行，不凭无permission事件宣称通过；测试账本全程隔离。新S3按原任务链验收。
2. 修改：U2规范化→U1规则匹配是硬依赖；共享文件才施工排序。
3. 采纳：CLI user身份不当人类证明，设备戳只候选；永不可覆盖集先行。
4. 采纳：新裁决见撤销、已投递不追杀现在定；过期及FindReuse明列，人工独立grant保留语义待审。
5. 采纳：UI行为保留且实施前原型确认；不以CLI代替UI验收。
6. 修改后采纳：冻结原子化，新增分类/例外/工单责任载体断言；准确wire待真实消费者形成并审查。
