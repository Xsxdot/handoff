# B405 implement 台账（2026-09-23）

节点：implement。工作分支 `cards/B405-charter-3`（不切分支）。基线含 spec/plan。
产出物：`internal/config/config.go`、`internal/config/config_test.go`、`internal/orchestration/approver.go`、`internal/orchestration/approver_test.go`、`internal/orchestration/contracts.go`、`internal/orchestration/manager.go`、`internal/orchestration/approval_client.go`、`internal/approval/client.go`、`internal/orchestration/approval_client_test.go` 及本台账。

## 图覆盖债

- `codegraph sym decideWithCandidate` → `符号 "decideWithCandidate" 不在图中`；近似候选 `[]`。回落读源码。
- 图 `view: baseline` 签名陈旧：`codegraph sym ApproverConfig` 返回 `Executor string`（早于 B376 ExecutorList）；`codegraph sym Approver` 返回 `shot executor.OneShot` 单 shot 形态。现状签名以工作树源码为准。
- `codegraph flow n_orchestration_Approver_Decide` → `degraded=true, steps=0`，`missing: 基线没有 flows 段`。回落读源码 approver.go。
- 未命中符号记入本节，未用 grep 冒充查图。

## 读数（亲自跑到的）

- `go build ./...` → exit 0（implement 开工前基线复核）。
- 工作树护卫：`test -f docs/superpowers/specs/b405.md` → ok；approver.go 含 `shots map[string]executor.OneShot`。

## Task 1

- 1a 加 `Models map[string]string \`yaml:"models,omitempty"\`` 后 `go build ./...` → BUILD_OK。
- config 缝测试 4 条追加；`go test ./internal/config/ -count=1 -run 'TestLoadApprover|TestApproverModels'` → 红 1 条：`TestLoadApproverModelsRejectsNonCandidate ... approver.models 含非候选键应在启动期被拒绝`（断言红，功能缺失）。
- 实现 1b/1c/1d 后同命令 → `ok`；`go test ./internal/config/ -count=1` → `ok`；`go build ./...` → BUILD_OK。
- Decide 缝测试首红：`undefined: ApproverModelDefaultLabel`（新缝符号编译红，核实非拼写错）。
- 落常量壳后断言红 3 条：`codex OneShotReq.Model = ""，期望 gpt-6-luna`；`attempt 模型 = ""/""，期望均为 chain-m`；`attempt.Model 应显式记 "默认"`。`TestDecideChainModelRegression` 绿（存量回归，符合 plan 预期）。
- 实现 1e/1f/1g/1i/1j 后：`go test ./internal/config/ -count=1` → `ok`；`go test ./internal/orchestration/ -count=1 -run 'TestDecide|TestApprover|TestConsultApprover|TestNilApprover'` → `ok (2.103s)`；`go build ./...` → BUILD_OK。

## Task 2

- 2a/2d 结构体字段（contracts.go ApproverDecisionPayload.Model、approval.ApproverAttempt/ConsultDecision/approverDecisionPayload.Model）落地后 `go build ./...` → BUILD_OK。
- Task 2 缝测试断言红（非编译红）：
  - `TestConsultApproverWritesPerAttemptEventsManagerPath` → `codex 未指定模型，payload.Model 应显式记 "默认"，得到 ""`
  - `TestApprovalClientPerCandidateModelReachesEventPayload` → `approver_decision payload model = <nil>，期望 gpt-6-luna`
- 实现 2b/2c/2d 后：两缝测试 `ok`；`go test ./internal/orchestration/ -count=1 -run 'TestConsultApprover|TestApprovalClient'` → `ok (1.162s)`；`go test ./internal/approval/ -count=1` → `ok (1.437s)`；`go build ./...` → BUILD_OK。

## 变异自验（cp 快照恢复，非 git checkout）

事故记录：首轮变异并行跑且用 `git checkout -- <file>` 恢复，checkout 还原到 HEAD 而非变异前状态，两次冲掉未提交的 config.go/approver.go 改动；已按原计划重新落盘并全量复测绿。后续变异改为文件级 clean 快照 + `cp` 恢复，串行执行。

| 变异 | 命中唯一 | 编译 | 结果 |
|---|---|---|---|
| modelFor 去掉 `m != ""`（空串直接生效） | count=1 | 过 | FAIL 1：`TestDecideCandidateEmptyEntryFallsBackToChain` |
| validate 禁用错字键检查 | count=1 | 过 | FAIL 1：`TestLoadApproverModelsRejectsNonCandidate` |
| req.Model 绕过 modelFor 用链级 | count=1 | 过 | FAIL 1：`TestDecidePerCandidateModel` |
| 去掉「默认」标签回填 | count=1 | 过 | FAIL 2：`TestDecidePerCandidateModel` + `TestDecideNoModelAnywhereUsesDefaultLabel` |
| manager per-attempt payload 丢 Model | count=1 | 过 | FAIL 1：`TestConsultApproverWritesPerAttemptEventsManagerPath` |
| toApprovalAttempts 丢 Model | count=1 | 过 | FAIL 1：`TestApprovalClientPerCandidateModelReachesEventPayload` |
| approval per-attempt payload 丢 Model | count=1 | 过 | FAIL 1：`TestApprovalClientPerCandidateModelReachesEventPayload` |
| approval else payload 丢 Model | count=1 | 过 | **FAIL 0（存活）**：else 分支仅 Attempts 为空时到达；B376 后真实 Decide 恒有 attempts，该赋值为防御性死路径，plan 测试清单未声明专属锁 |
| Decide 干净裁决丢 final Model | count=1 | 过 | FAIL 1：`TestDecidePerCandidateModel` 最终生效 Model |

恢复后：`go build ./...` BUILD_OK；三包触及测试 ok。

## 收尾自审

- 全量 `go build ./...` → BUILD_OK（亲自跑到）。
- 触及包测试：`go test ./internal/config/` ok；`go test ./internal/orchestration/ -run 'TestDecide|TestApprover|TestConsultApprover|TestNilApprover|TestApprovalClient'` ok；`go test ./internal/approval/` ok。
- 与 plan Interfaces：Produces 逐字对齐（Models 字段、ApproverModelDefaultLabel、modelFor、ApproverAttempt.Model、ApproverDecision.Model、两 payload json model,omitempty）。
- 未碰 handoff CLI、未起新 executor。
- 全量 `go test ./...` 按 plan 归 acceptance，本节点未跑（未验证）。
- `gofmt -l internal/` 报 `internal/agentd/cardstep.go`、`internal/ledger/types.go`——本卡未触碰的存量文件（不在 git diff 中），不改。

## 提交

- `git add` 上述 9 个代码/测试文件 + 本台账 → `git commit -m "feat(B405): 审批链候选级模型——approver.models 解析校验 + 逐候选选值与观测"` → `553425f5`（收口前 amend 一次收台账，hash 会变属预期）。
