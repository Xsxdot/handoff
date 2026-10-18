# B437 实现台账（implement 节点）

- 分支：`cards/B437-charter-3`；起手 HEAD `d960a88e`（plan 提交）
- 工作树：`/root/.handoff/worktrees/53e5b631`；TMPDIR=`/root/.handoff/tmp/53e5b631`
- 图查询：`codegraph --repo . sym m_orchestration_Approver` 命中 `internal/orchestration/approver.go:78`；其余接缝（devices/registry/selfname/clientForTarget/dialTimeout）直接读当前文件核实。基线图快照陈旧，行号一律以当前文件为准（与 plan §2.3 同债）。
- 本轮未触碰 handoff CLI、未起新 executor、未派发；真机验证不在本轮。

## 实现顺序（plan §5 A→B→C）

### A 段（S2 竖切）
- 新增 `internal/orchestration/approver_chain.go`：`ApproverRing/ApproverChain`、`ValidateApproverChain`、链文件/body 编解码、`ChainReader/RemoteDecider` 端口、`RunLocalRing`（提示词模板/nonce/解析唯一实现）、失败分类常量与 `ApproverFailReason`、`ReadChainWithTimeout`。
- `internal/orchestration/approver.go`：`ApproverAttempt.Machine`；Approver 增加 `chainReader/remoteDecider/localShot/selfMachine/lastGood/warn*` 与 setter；`Available()`；`Decide` 分支 `effectiveChain`→共享链否则本地链；`NewApproverForShared`（空本地配置也返非 nil）。
- `internal/orchestration/manager.go`：`shouldConsultApprover` 加 `Available()` 门。
- 审计双投影加 `machine`：`orchestration/contracts.go`、`approval/client.go`（类型+payload+投影）、`orchestration/approval_client.go`、`manager.go`。
- `internal/proto/approver.go`：`ApproverDecideReq/Resp`（F1/F2 键集）。
- `internal/client/coordinator.go`：`ApproverDecide` + `ErrApproverUnsupported`（F3 404 归一）。
- `internal/agentd/approverapi.go`：端点 `POST /api/approver/decide` + `ApproverRemoteDecider` 适配器 + `SetApproverEnv`；`server.go` 注册路由、加 `approverEnv` 字段。

### B 段（S1 存取）
- `cmd/approver.go`：`handoff approver show|set|clear`（DSN 门、设备表校验、CAS）；核心逻辑拆 `*WithStore` 便于对真实库测试。
- `cmd/approver_wiring.go`：`ledgerChainReader`（ErrNotFound→ErrApproverChainAbsent）、`warnLocalApproverSuperseded`（S6 启动 WARN）。
- `cmd/agentd.go`：共享机恒构造 Approver；`setupLedger` 后注入链读取口/远程裁决口/localShot/自名判据 + `SetApproverEnv`。
- `README.md`：中英 approver 段补共享链说明。

### C 段（S3/S4 收尾）
- 失败五分类（不可达/不支持/无执行器/超时/裁决无效）+ internal 兜底：`RemoteOutcome.ErrorKind` 由适配器分类、orchestration 翻人读理由。
- last-good / clear 回落 / 2s 独立读时限 / 限频 WARN：`effectiveChain` + `readChain` + `warnLimited`。
- S5 回归：既有审批测试全绿（见下）。

## 本次补充 P2 约束（2s 独立读时限 + 限频 WARN）落地
- `ApproverChainReadTimeout = 2s` 包级常量；`ReadChainWithTimeout` 把读放独立 goroutine + select，到点必返回（底层驱动不尊重 ctx 也不被拖住）。
- `effectiveChain`：读成功→更新 last-good；ErrApproverChainAbsent（clear）→清 last-good 回落本地链；其余错误→有 last-good 用之，否则回落本地链；各打限频 WARN。
- `warnLimited`：按 key 每分钟至多一条。
- 断言：`TestSharedChainReadTimeoutUsesLastGood`（挂起替身→Decide 约 2s 返回走 last-good、连续 3 次读超时只 1 条 WARN）。

## 亲跑命令与原始输出

```
$ go build ./...
（退出 0，无输出）

$ go vet ./...
（退出 0，无输出）

$ go test ./internal/orchestration/ -run 'Approver|Consult|Decide|Permission|Ticket|Late' -count=1
ok  github.com/Xsxdot/handoff/internal/orchestration  12.574s

$ go test ./internal/approval/ ./internal/config/ ./internal/proto/ -count=1
ok  github.com/Xsxdot/handoff/internal/approval  2.327s
ok  github.com/Xsxdot/handoff/internal/config  0.022s
ok  github.com/Xsxdot/handoff/internal/proto  0.010s

$ go test ./internal/orchestration/ -run 'ValidateApproverChain|DecodeApproverChain|ApproverChainBody|DecideSharedChain|AvailableGate|SharedChain|ShouldConsultApproverSharedGate' -count=1
ok  github.com/Xsxdot/handoff/internal/orchestration  6.013s
（TestSharedChainReadTimeoutUsesLastGood 占 6.00s：3 次 2s 封顶读）

$ go test ./internal/agentd/ -run 'ApproverDecide|S2VerticalSlice' -count=1 -v
--- PASS: TestApproverDecideEndpointAuthAndValidation
--- PASS: TestApproverDecideEndpointNoExecutor
--- PASS: TestApproverDecideEndpointInvalidOutputAndApprove
--- PASS: TestS2VerticalSliceRemoteRingDecide
ok  github.com/Xsxdot/handoff/internal/agentd  1.125s

$ go test ./internal/proto/ ./internal/client/ ./cmd/ -run 'Approver' -count=1
ok  github.com/Xsxdot/handoff/internal/proto  0.002s
ok  github.com/Xsxdot/handoff/internal/client  0.004s
ok  github.com/Xsxdot/handoff/cmd  0.693s

$ go test ./internal/orchestration/ -run 'ConsultApproverSharedChainMachineManagerPath' -count=1
ok  github.com/Xsxdot/handoff/internal/orchestration  0.136s

$ go test ./internal/approval/ -run 'Machine' -count=1
ok  github.com/Xsxdot/handoff/internal/approval  0.088s

# 触及子系统全量
$ go test ./internal/orchestration/ ./internal/approval/ ./internal/client/ ./internal/proto/ ./internal/config/ -count=1
ok  github.com/Xsxdot/handoff/internal/orchestration  59.527s
ok  github.com/Xsxdot/handoff/internal/approval  3.797s
ok  github.com/Xsxdot/handoff/internal/client  16.569s
ok  github.com/Xsxdot/handoff/internal/proto  0.010s
ok  github.com/Xsxdot/handoff/internal/config  0.024s

$ go test ./internal/agentd/ -count=1
ok  github.com/Xsxdot/handoff/internal/agentd  239.646s

$ go test ./cmd/ -count=1
ok  github.com/Xsxdot/handoff/cmd  92.961s
```

## 未验证项
- 真机（§9 全部场景）：不在本轮，协调者另起隔离实例做。
- registry body 经 **PG** 后端往返：本机无 PG；SQLite 往返已由 `TestApproverSetShowTwoMachines` 覆盖，PG 路径靠既有 `registry_pg_test.go`/`ddl_parity_test.go` 族（本轮未跑 PG）。
- relay 冷隧道连接时限（§9-6）、资源量级（§9-7）：未验证，需真机。

## 提交
见本批提交（工作树收口干净）。
