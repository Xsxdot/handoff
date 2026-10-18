# B437 实现台账（implement 节点）

- 分支：`cards/B437-charter-3`；起手 HEAD `d960a88e`（plan 提交）
- 工作树：`/root/.handoff/worktrees/53e5b631`；TMPDIR=`/root/.handoff/tmp/53e5b631`
- 图查询：`codegraph --repo . sym m_orchestration_Approver` 命中 `internal/orchestration/approver.go:78`；其余接缝（devices/registry/selfname/clientForTarget/dialTimeout）直接读当前文件核实。基线图快照陈旧，行号一律以当前文件为准（与 plan §2.3 同债）。
- 本轮未触碰 handoff CLI、未起新 executor、未派发；真机验证不在本轮。

## 偏离与自主决定

本节是本节点交回物的法定第一节。逐条列出与已批准 plan / spec 不一致之处、本节点自行拍板的内容与理由，并对照 plan §10「决策权限」。

1. **RunLocalRing 与 decideWithCandidate 平行（偏离 plan §4.9「汇合」）**——plan §4.9 要求「两条路径在 decideWithCandidate 内汇合成同一执行逻辑」：本地配置链候选与共享链本机环共用同一执行函数。实现未照此做，而是新增 `orchestration.RunLocalRing`（`approver_chain.go`），由 `decideRing` 调用；`decideWithCandidate`（`approver.go`）仍是本地配置链（`decideLocalChain`）专用。两者**平行**，不是同函数汇合。
   - **为何仍同源**：提示词模板 `approverPromptTemplate`、nonce 生成 `randNonce`、裁决解析 `parseDecision`、grok `EffortLow` 档位、env 解析（`env.For`）、fail-closed 语义全部复用同一份实现/常量；差异只在「模型解析来源」（本地链 `modelFor(name)` vs 环 `ring.Model`）与审计投影函数（`decideWithCandidate` vs `ringOutcomeAttempt`）。因此两端裁决语义不会因版本漂移而分叉。
   - **S5 为何不受影响**：本地配置链路径（`Decide` → `decideLocalChain` → `decideWithCandidate`）一字未改，B376/B405 既有回归钉原样全绿（见下方亲跑）。
   - **权限对照**：plan §10 允许「§4 未涉及的局部私有函数」调整，但「两路汇合」是 §4.9 明写的结构，属偏离，如实披露。风险：两份执行逻辑未来可能各自演化；当前共用模板/nonce/解析，漂移面仅限模型解析与投影，由 S2 竖切（`TestS2VerticalSliceRemoteRingDecide`）与对端端点测试覆盖。**未回 spec 定性**，留待 review 裁决是否需要合并。

2. **新增测试缝 `approverDecideCaller` + `remoteApproverDecider.clientFor`（自主拍板，落 §10 授权内）**——为确定性地钉住「拨号超时形态的 net.Error」失败分类（无法在秒级内真实复现 10s 拨号超时），给 `remoteApproverDecider` 增加可注入的调用面缝：`clientFor` 为 nil 时走生产路径 `srv.clientForTarget`，测试注入故障替身。属 §10 授权的「内部命名 / §4 未涉及的局部私有函数 / fake 位置」，不改对外语义。

3. **本轮修复带来的变化（review r1 fail + 协调者真机发现）**：
   - **(2) 端点不再硬编码 60s 封顶**：`handleApproverDecide` 调 `RunLocalRing` 的 timeout 由 `DefaultApproverChainTimeout`(60s) 改为 `0`——对端只依赖 `r.Context()`（发起方链级时限经 HTTP 取消传播），不再掐断用户配置的链级时限。原先的 60s 封顶是实现引入的隐藏护栏，非 §4.6 承诺，删除属授权内。
   - **(3) DecideRemote 超时判据收紧**：由 `errors.Is(err, context.DeadlineExceeded) || errors.Is(cctx.Err(), context.DeadlineExceeded)` 改为仅 `cctx.Err() == context.DeadlineExceeded`。理由：net 拨号超时错误在部分路径也满足 `errors.Is(..., context.DeadlineExceeded)`，真机同一黑洞环（拨号 10s 超时）并发 4 次曾 2 次判「超时」2 次判「不可达」。改后只以整体截止（链 timeout 到点）判超时，其余传输错误（含拨号超时）一律判不可达——**这是回归到 plan §4.6 的判据，不是改 §4.6**。
   - **(4) 新增 `TestDecideRemoteFailureClassification`**：五分类表驱动 + 拨号超时回归钉（含设备表无此名、404、no_executor、超时、invalid_output、未知 error_kind→internal），每例断言分类与转下一环。
   - **(5) F5 registry body 改逐键精确键集断言**：`TestApproverChainBodyRoundTrip` 由 `strings.Contains` 改为 `approverBodyKeys` + `assertApproverBodyKeys`（复用 proto 侧 `assertKeys` 形态），断言顶层 `{rings,timeout}`、ring0 `{executor,machine,model}`、ring1 `{executor,machine}`（model omitempty）。
   - 修复均落在 plan §10 授权范围（内部实现与测试）；未改 F1–F5 语义、未改五分类常量、未动 fail-closed、未动 S6 回落顺序。

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

## 修复轮（review r1 fail + 协调者真机发现）亲跑命令与原始输出

本轮改动：`internal/agentd/approverapi.go`（端点去 60s 封顶、DecideRemote 超时判据、测试缝）、
`internal/agentd/approverapi_test.go`（端点封顶探针 + 五分类表驱动 + 拨号超时回归钉）、
`internal/orchestration/approver_chain_test.go`（F5 body 逐键精确键集断言）。

先红后绿（回归钉首红，见 (4) 拨号超时子例；端点封顶首红见 (2)）：

```
$ go test ./internal/agentd/ -run 'TestApproverDecideEndpointDoesNotCapChainTimeout' -count=1 -v -timeout 90s
--- FAIL: TestApproverDecideEndpointDoesNotCapChainTimeout (0.74s)
    approverapi_test.go:318: 端点不得对 OneShot 施加自身封顶（应只依赖 r.Context()，不掐断用户配置的链级时限）

$ go test ./internal/agentd/ -run 'TestDecideRemoteFailureClassification' -count=1 -v -timeout 25s
    approverapi_test.go:475: 应分类 unreachable，得到 timeout（reason="dial tcp 100.64.0.1:443: i/o timeout"）
（其余子例当时已过；拨号超时子例即 (3) 的回归钉）
```

修复后绿：

```
$ go build ./...
（退出 0，无输出）
$ go vet ./...
（退出 0，无输出）

$ go test ./internal/agentd/ -run 'Approver|S2VerticalSlice|DecideRemote' -count=1 -v -timeout 180s
--- PASS: TestApproverDecideEndpointAuthAndValidation (0.81s)
--- PASS: TestApproverDecideEndpointNoExecutor (0.20s)
--- PASS: TestApproverDecideEndpointInvalidOutputAndApprove (0.21s)
--- PASS: TestS2VerticalSliceRemoteRingDecide (0.20s)
--- PASS: TestApproverDecideEndpointDoesNotCapChainTimeout (0.10s)
--- PASS: TestDecideRemoteFailureClassification (1.56s)
--- PASS: TestAutomationApproverDisabledFallsBackToWake (0.42s)
PASS
ok  	github.com/Xsxdot/handoff/internal/agentd	3.497s

# 触及子系统全量（本轮未改 approval/client/proto/config 源码，随包回归）
$ go test ./internal/orchestration/ ./internal/approval/ ./internal/client/ ./internal/proto/ ./internal/config/ -count=1
ok  	github.com/Xsxdot/handoff/internal/orchestration	57.869s
ok  	github.com/Xsxdot/handoff/internal/approval	2.799s
ok  	github.com/Xsxdot/handoff/internal/client	14.662s
ok  	github.com/Xsxdot/handoff/internal/proto	0.011s
ok  	github.com/Xsxdot/handoff/internal/config	0.025s
```

未验证（本轮）：真机五分类（协调者在役 agentd 上复核）；`go test ./cmd/`、`go test ./internal/agentd/ -count=1` 全量本轮未重跑（仅跑 Approver 相关子集），cmd 无源码改动。

## 提交事实

```
$ git add -A && git commit -q -m "fix(b437): 修复轮——端点去 60s 封顶、DecideRemote 超时判据只认整体截止、补五分类回归钉与 F5 逐键断言"
$ git log --oneline -1
36a22943 fix(b437): 修复轮——端点去 60s 封顶、DecideRemote 超时判据只认整体截止、补五分类回归钉与 F5 逐键断言
```

（本条提交事实随后经一次 amend 收进同批提交；amend 会换 hash，最终 HEAD 见收尾行，不把 amend 后的 HEAD 回写本台账。）
