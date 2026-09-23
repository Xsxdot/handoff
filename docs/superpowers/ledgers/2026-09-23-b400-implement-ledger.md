# B400 implement 台账（2026-09-23）

卡 B400 · implement 节点 · 依 `docs/superpowers/plans/b400-plan.md` 执行 T1→T4。
分支 `cards/B400-charter-3`。所有命令在本工作树亲跑；临时文件只写 `$TMPDIR`。
P1 岔口按 plan 推荐甲（fail-open）出稿，未另行拍板。

---

## §1 起手核对

```text
$ git branch --show-current
cards/B400-charter-3
$ git log --oneline -3
61059b9b plan(B400): 新卡首个节点的基线护栏——实现计划 + 台账
54585a62 spec(B400) 批准回写: 已批准（2026-09-23）
d16472a5 spec(B400) 修订: 补豁免通道/无附件前提/远端树约束（独立审核后）
$ git status --short
（clean）
$ go version
go version go1.26.1 linux/amd64
$ which codegraph
/usr/local/bin/codegraph
```

- 协调者指示：本机未按 plan 声明安装/使用 codegraph 探测，plan 已声明本线不记图覆盖债、
  按 spec §6 接缝清单直读源码；实现节点同样**不查图**（避免重复同一请求）。
- 夹具签名复核（plan §4）：`newLedgerEnv`/`ledgerPost`/`seedAgentdLedger`/
  `seedDisciplineOnLedger`/`waitFor`/`cardStepInFlight`、`dispatchTestCard`/
  `seedLedgerStepStore`、`newOriginAndClone`/`commitOnOrigin`/`gitProbe` 均在仓内命中。
- 亲读 `internal/ledgerstep/dispatch.go`（`WorkBranch` `:201-202`、
  `resolveDefaultBase := base == ""` `:296`）与 `internal/agentd/cardstep.go`
  （Dispatcher 装配 `:199-218`、`runStepFn` `:243-247`）——与 plan 现状一致。

## §2 T1 红锚

落 `internal/agentd/b400_first_dispatch_test.go`（plan T1 全文照抄，无占位）。

```text
$ go test ./internal/agentd/ -run TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase -count=1 -timeout 120s
... INFO 模板派发目标已归一 card=B1 template=feature-impl raw_target="" target="" frozen_target=""
... INFO 按模板派发 card=B1 node=impl ... branch=cards/B1-implement base="" resolve_default_base=true ...
... WARN 模板派发传输失败 card=B1 ... cause="Transport 不应被触达：基线护栏缺失"
... WARN 派发失败，转等人 node=impl card=B1 cause="派发: Transport 不应被触达：基线护栏缺失"
--- FAIL: TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase (2.96s)
    b400_first_dispatch_test.go:161: 护栏未在 Transport 前拦下（transportCalled=1）
FAIL
FAIL	github.com/Xsxdot/handoff/internal/agentd	2.966s
FAIL
EXIT=1
```

红原因核实：非 typo——断言 `transportCalled=0` 失败于 `transportCalled=1`，即护栏缺席
（`resolveDefault_base=true`、`base=""` 下直达 Transport），与 plan §6 预期同形。功能缺失红。

## §3 T2 决策段（ledgerstep）

TDD 序：

1. 落 `internal/ledgerstep/b400_base_guard_test.go` → **编译红**（确认非拼写错）：
```text
$ go test ./internal/ledgerstep/ -run 'TestB400' -count=1 -timeout 120s
internal/ledgerstep/b400_base_guard_test.go:35:3: unknown field ProbeBaseAttachments in struct literal of type Dispatcher
...
internal/ledgerstep/b400_base_guard_test.go:178:20: undefined: ErrBaseProbeUnavailable
FAIL github.com/Xsxdot/handoff/internal/ledgerstep [build failed]
```
2. 落空壳（字段 + 哨兵 + 恒放行 `guardFirstDispatchBase` + 调用点占位）→ **断言红**：
```text
--- FAIL: TestB400FirstDispatchRejectsMissingAttachment (0.12s)
    b400_base_guard_test.go:49: 默认线缺附件应拒发，实得 nil
--- FAIL: TestB400ProbeErrorRejects (0.15s)
    b400_base_guard_test.go:169: 探针失败应 fail-closed 拒发并保留 cause，实得 <nil>
FAIL github.com/Xsxdot/handoff/internal/ledgerstep
```
   失败原因核实：功能缺失（空壳恒放行），非 typo。
3. 落正式实现（`baseAttachmentPaths` + `guardFirstDispatchBase` + `resolveDefaultBase` 后调用）。
   **过程中发现 plan 夹具缺口**：`b400CardWithSpec` 挂附件后仍返回 CreateCard 时的卡快照，
   `Attachments` 为空 ⇒ 护栏永远看到无附件、假放行。修法：`AttachFile` 后 `GetCard` 重读再返回。
   （同修 `TestB400ExplicitBaseSkipsGuard` 的重读。）此为测试夹具 bug，不动生产逻辑。
4. 跑绿 + 静态：
```text
$ go test ./internal/ledgerstep/ -run 'TestB400' -count=1 -timeout 120s
ok  	github.com/Xsxdot/handoff/internal/ledgerstep	0.873s
$ go build ./...
BUILD=0
$ go vet ./internal/ledgerstep/
VET=0
```

P1 按推荐甲（`ErrBaseProbeUnavailable` fail-open + Warn）出稿，用户未另行拍板。

## §4 T3 workspace 树读取 + agentd 生产探针

TDD 序：

1. 落 `internal/workspace/b400_base_tree_test.go` + T1 文件追加两支探针用例 → **编译红**：
```text
internal/agentd/b400_first_dispatch_test.go:186:36: env.srv.probeBaseAttachments undefined ...
internal/workspace/b400_base_tree_test.go:35:30: undefined: BaseTreeMissingPaths
FAIL ... [build failed]
```
2. 落空壳（`BaseTreeMissingPaths` 恒 empty-missing、`probeBaseAttachments` 恒 empty）+ 接线
   `ProbeBaseAttachments: s.probeBaseAttachments` → **断言红**（夹具 `writeAndCommit` 先补
   MkdirAll 才能提交嵌套路径；补后）：
```text
--- FAIL: TestB400BaseTreeMissingPaths/默认线：spec_在场
    resolved = ""，want "main"
--- FAIL: TestB400BaseTreeMissingPaths/非法路径按缺失处理
    missing = []，want [../secret]
--- FAIL: TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase
    护栏未在 Transport 前拦下（transportCalled=1）
--- FAIL: TestB400ProbeReportsMissingAndPresentPaths
    解析默认分支 = ""，want main
--- FAIL: TestB400ProbeSkipsWhenProjectNotRegistered
    未登记项目应返回 ErrBaseProbeUnavailable，实得 <nil>
```
3. 落正式实现（`internal/workspace/basetree.go`、`Server.probeBaseAttachments`、cardstep 接线）。
4. 跑绿 + 静态：
```text
$ go build ./...
BUILD=0
$ go test ./internal/workspace/ -run TestB400BaseTreeMissingPaths -count=1
ok  	github.com/Xsxdot/handoff/internal/workspace	0.201s
$ go test ./internal/agentd/ -run 'TestB400' -count=1 -timeout 120s
ok  	github.com/Xsxdot/handoff/internal/agentd	1.749s
$ go vet ./internal/workspace/ ./internal/agentd/ ./internal/ledgerstep/
VET=0
```
（T1 端到端此时转绿——护栏 + 生产探针 + workspace 树读取全链路在场。）

夹具修正（非生产代码）：`internal/workspace/gittesthelpers_test.go` 的 `writeAndCommit`
增加 `MkdirAll` 父目录，否则 `commitOnOrigin` 无法提交 plan 所需的嵌套路径。

## §5 T4 收口：不误伤回归 + 变异复验

### 不误伤回归（亲跑）

```text
$ go test ./internal/ledgerstep/ -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/ledgerstep	13.421s
$ go test ./internal/workspace/ -run 'TestResolve|TestPrepare|TestBase|TestLocalBase' -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/workspace	4.637s
$ go test ./internal/agentd/ -run 'TestStartCardStep|TestCardStep|TestB396|TestB398' -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/agentd	8.733s
```

收口前复跑（含 T1/T2/T3 全 B400）：

```text
$ go test ./internal/ledgerstep/ -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/ledgerstep	14.357s
$ go test ./internal/workspace/ -run 'TestResolve|TestPrepare|TestBase|TestLocalBase' -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/workspace	4.437s
$ go test ./internal/agentd/ -run 'TestStartCardStep|TestCardStep|TestB396|TestB398|TestB400' -count=1 -timeout 300s
ok  	github.com/Xsxdot/handoff/internal/agentd	9.690s
$ go build ./...
BUILD=0
$ go vet ./internal/ledgerstep/ ./internal/workspace/ ./internal/agentd/
VET=0
```

### 变异复验（手动，不留代码；命中唯一 + 编译过 + 行为先变红）

**MUT1**：摘掉 `ViaTemplate` 里 `guardFirstDispatchBase` 拒发（保留调用、错误不再上抛）。
```text
MUT1_COUNT=1
MUT1_APPLIED
MUT1_BUILD=0
--- FAIL: TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase (2.03s)
    护栏未在 Transport 前拦下（transportCalled=1）
$ go test ./internal/ledgerstep/ -run 'TestB400' | grep -c '^--- FAIL' → 2
MUT1_RESTORED → ok（0.865s）
```
⇒ 护栏调用承重（摘掉即红，且 ledgerstep 缝级两支拒发测试同步红）。

**MUT2**：`BaseTreeMissingPaths` 在场判定恒认为在场（恒不 append missing）。
```text
MUT2_COUNT=1
MUT2_APPLIED
MUT2_BUILD=0
--- FAIL: TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase (2.02s)
    护栏未在 Transport 前拦下（transportCalled=1）
MUT2_RESTORED → BUILD=0，全绿
```
⇒ workspace 判据承重。

`grep MUT1|MUT2` 收口：NO_MUTATION_LEFTOVER；`git status --short` 只剩正式改动。

## §6 产出与提交

- 法定文件（plan §8 有界集 + 夹具修正）：
  - `internal/ledgerstep/dispatch.go`
  - `internal/ledgerstep/b400_base_guard_test.go`（新）
  - `internal/workspace/basetree.go`（新）
  - `internal/workspace/b400_base_tree_test.go`（新）
  - `internal/workspace/gittesthelpers_test.go`（夹具：writeAndCommit 补 MkdirAll，支撑嵌套路径提交）
  - `internal/agentd/cardstep.go`
  - `internal/agentd/b400_first_dispatch_test.go`（新）
  - 本台账
- 与 plan 偏差（已明示）：T2/T3 夹具重读卡（AttachFile 后 GetCard）、writeAndCommit 补父目录——均为测试夹具缺口，不改生产语义；P1 取甲（fail-open）。
- 图覆盖债：本线按 plan 声明不查 codegraph（协调者指示），未记图覆盖债。
