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
