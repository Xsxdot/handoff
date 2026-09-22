# B398 执行台账（2026-09-23）

> 卡 B398 · 节点 charter:implement · 计划 `docs/superpowers/plans/b398-plan.md`（已批准）
> spec `docs/superpowers/specs/b398.md`（已批准）
> 工作分支 `cards/B398-charter-6`，起手 HEAD `e1bc3b34`（plan 提交）。
> 凡引用行号者动手前重核，漂了以符号为准。台账与产出物同批提交。

## 0. 起手事实

- `git status` → 工作树干净；`git log --oneline -1` → `e1bc3b34 plan(B398): ...`
- 分支 `cards/B398-charter-6`。`codegraph` 已安装（`/usr/local/bin/codegraph`）。

## 1. T1 红回路（新建测试文件 + 亲跑红）

**动作**：新建 `internal/agentd/b398_frozen_target_test.go`，落计划 T1 的三条缝测试
（接缝 3 E2E、接缝 2 新发送方、接缝 2 旧发送方回归锁）+ `b398CardEvents` 辅助。

```text
$ go test ./internal/agentd/ -run 'TestB398FrozenCarrierLocalAliasDispatches|TestB398ReceiverUsesFrozenTargetForFrozenAdmission|TestB398ReceiverLegacyTargetFallback' -count=1 -timeout 180s
EXIT=1
--- FAIL: TestB398FrozenCarrierLocalAliasDispatches (0.44s)
    b398_frozen_target_test.go:43: 本机别名冻结派发应形成 1 条 task，实得 0；卡事件=
          card_created {"title":"小队流测试卡0","workflow":"sqflow","workflow_version":1}
          comment {"body":"本节点派发失败：\n派发: dispatch: 状态码 400: {\"error\":\"scheduling: 小队角色不符: 冻结物理身份与载体登记不一致\"}","kind":"普通","refs":null}
          needs_human {"reason":"派发失败"}
--- FAIL: TestB398ReceiverUsesFrozenTargetForFrozenAdmission (0.26s)
    b398_frozen_target_test.go:69: 带冻结字段的冻结派发应 200，实得 400（{"error":"scheduling: 小队角色不符: 冻结物理身份与载体登记不一致"}
FAIL
```

- `TestB398ReceiverLegacyTargetFallback` 未出现在失败列表（基线即绿，回归锁成立）。
- 红的原因与 plan §4 一致：`NormalizeTarget` 把冻结机器名折成本机空串，接收端
  `AdmitFrozen` 拿空串比对登记名 `linux-01`，报 `ErrRoleMismatch`。

## 2. T2 实现（七处改动 + 测试追加）

改动逐处对照 plan §6 T2：

1. `internal/ledgerstep/dispatch.go` `DispatchOpts` 新增 `FrozenTarget string`；
2. `internal/ledgerstep/dispatch.go` `Dispatcher` 新增 `FrozenTarget string`；
3. `ViaTemplate` 透传 `FrozenTarget: d.FrozenTarget` + 「目标已归一」「按模板派发」
   「模板派发完成」三处日志加 `frozen_target` 读数；
4. `internal/agentd/cardstep.go`：装配点 `FrozenTarget: binding.Target`、
   `dispatchStep` 镜像 `FrozenTarget: opts.FrozenTarget`、`stepTransport` 两处日志加读数、
   `startCardStep` 装配日志加 `frozen_target`（**不改**选路逻辑）；
5. `internal/client/client.go` `DispatchOpts` 新增 `FrozenTarget`，
   `opts.FrozenTarget != ""` 时写 wire `frozen_target`；
6. `internal/agentd/handlers.go` `dispatchRequest` 新增 `FrozenTarget *string`，
   Carrier 分支有冻结字段用它建 Binding、缺席回落 `req.Target`（显式空串不静默回落）；
7. `cmd/card_dispatch.go`：`dispatchRequest.frozenTarget` + `cliTransport` /
   `dispatchTransportWithOpts` 两处投影。

测试追加：`b398_frozen_target_test.go` 补接缝 1（`TestB398AssemblySeparatesFrozenIdentityFromRoute`）
与空值分辨（`TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim`）；
`internal/client/client_test.go` 补 `TestDispatchSerializesFrozenTargetPresence`；
`cmd/card_dispatch_test.go` 补 `TestCliTransportForwardsFrozenTarget`。

## 3. 绿读数（亲跑）

```text
$ go build ./...
BUILD_EXIT=0

$ go vet ./internal/agentd/ ./internal/ledgerstep/ ./internal/client/ ./cmd/
VET_EXIT=0

$ go test ./internal/agentd/ -run 'TestB398' -count=1 -timeout 180s -v
--- PASS: TestB398FrozenCarrierLocalAliasDispatches
--- PASS: TestB398ReceiverUsesFrozenTargetForFrozenAdmission
--- PASS: TestB398ReceiverLegacyTargetFallback
--- PASS: TestB398AssemblySeparatesFrozenIdentityFromRoute
--- PASS: TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim
ok  	github.com/Xsxdot/handoff/internal/agentd	1.113s

$ go test ./internal/client/ -run TestDispatchSerializesFrozenTargetPresence -count=1
ok  	github.com/Xsxdot/handoff/internal/client	0.005s

$ go test ./cmd/ -run TestCliTransportForwardsFrozenTarget -count=1
ok  	github.com/Xsxdot/handoff/cmd	0.005s

$ go test ./internal/ledgerstep/ ./internal/client/ ./cmd/ -count=1 -timeout 600s
ok  	github.com/Xsxdot/handoff/internal/ledgerstep	14.913s
ok  	github.com/Xsxdot/handoff/internal/client	13.955s
ok  	github.com/Xsxdot/handoff/cmd	69.075s

$ go test ./internal/agentd/ -count=1 -timeout 600s
ok  	github.com/Xsxdot/handoff/internal/agentd	202.235s
```

## 4. 变异自验（亲跑，先确认编译过再数红）

全部变异用 python 断言 `count(old)==1` 命中唯一后替换；每发先确认测试真的跑了
（有行为断言输出），再数失败数。

| # | 变异 | 命中唯一 | 编译 | 结果 |
|---|---|---|---|---|
| 1 | `handlers.go` 删「有冻结字段用冻结字段」分支（回落 req.Target） | 是 | 过 | 3 FAIL（接缝3/2新/2空值） |
| 2 | `cardstep.go` 装配点删 `FrozenTarget: binding.Target` | 是 | 过 | 2 FAIL（接缝3/1） |
| 3 | `ledgerstep/dispatch.go` `ViaTemplate` 删 `FrozenTarget: d.FrozenTarget` | 是 | 过 | 2 FAIL（接缝3/1） |
| 4 | `client.go` 删 `body["frozen_target"]` 写入 | 是 | 过 | 1 FAIL（投影点1） |
| 5 | `cmd/card_dispatch.go` `cliTransport` 删 `frozenTarget` 投影 | 是 | 过 | 1 FAIL（投影点2/3） |
| 6 | `cmd/card_dispatch.go` `dispatchTransportWithOpts` 删 `FrozenTarget` 投影 | 是 | 过 | **存活**（见下） |

变异 6 存活暴露了 plan 测试的真实覆盖缺口：`TestCliTransportForwardsFrozenTarget`
用 `swapDispatchTransportWithOpts` 把内层 `dispatchTransportWithOpts` 整个替换掉，
只锁到 `cliTransport → dispatchRequest` 一段；`dispatchRequest → client.DispatchOpts`
这段手写投影丢字段时该测试照样绿。

**补锁**（未违背 TDD：这是对既有手写投影点的覆盖补强，先有可执行断言再确认红）：
新增 `TestCliTransportProjectsFrozenTargetOntoWire`，走**真实** `dispatchTransportWithOpts`
（不 swap），对本机 httptest 端点断言 HTTP body 里出现 `frozen_target="linux-01"`。

```text
# 补锁前（红线：真实投影被删，body 无 frozen_target）
$ go test ./cmd/ -run 'TestCliTransport' -count=1
--- FAIL: TestCliTransportProjectsFrozenTargetOntoWire (0.00s)
    card_dispatch_test.go:1424: 真实 HTTP body 缺少 frozen_target: map[base:"" ... target:""]
FAIL

# 恢复实现后（绿）
$ go test ./cmd/ -run 'TestCliTransport' -count=1 -v
--- PASS: TestCliTransportProjectsFrozenTargetOntoWire
--- PASS: TestCliTransportForwardsFrozenTarget
ok  	github.com/Xsxdot/handoff/cmd	0.006s
```

补锁后再变异 6（删内层投影）→ `TestCliTransportProjectsFrozenTargetOntoWire` FAIL，
缺口闭合。**结论：六处生产投影/透传点全部被断言覆盖；plan 原测试对第 6 点的覆盖声明
（§6(c) 只写一条）不足以拦内层删字段，本节点补一条穿真实网络边界的断言。**

## 5. 图查询记录（codegraph）

```text
$ codegraph --repo . sym ViaTemplate        → 命中 n_ledgerstep_Dispatcher_ViaTemplate（file=internal/ledgerstep/dispatch.go:164）
$ codegraph --repo . sym stepTransport      → 命中 n_agentd_Server_stepTransport（cardstep.go:387）
$ codegraph --repo . sym handleDispatch     → 命中 n_agentd_Server_handleDispatch（handlers.go:497）
$ codegraph --repo . sym DispatchOpts       → 命中 client 与 ledgerstep 两处同名类型（已用源码区分）
$ codegraph --repo . sym dispatchRequest    → 命中 m_agentd_dispatchRequest（handlers.go:447）
$ codegraph --repo . sym FrozenTarget       → 不在图中（新增符号，图未刷新），回落 grep 补调用面
```

### 图覆盖债

- `sym FrozenTarget`：本卡新增符号，基线图未覆盖（图不随工作树改动刷新）。
  已用 `grep -n FrozenTarget` 逐一核对七处产出点，记债。
- `codegraph flow ViaTemplate` 基线即 `degraded`（无 flows 段），按纪律读源码，
  未拿 chain 冒充。

## 6. 收尾自审

- 每条错误分支有带上下文日志？已有（`handlers.go` 冻结准入失败、`stepTransport`
  派发失败等原日志保留）；成功路径出口日志保留且新增 `frozen_target` 读数。
- 新文件有头注释？`b398_frozen_target_test.go` 有职责/边界/缝说明；导出字段带注释。
- 触及包测试绿、全量编译过？是（§3）。
- 与 plan 的 Interfaces 签名一致？是：`ledgerstep.DispatchOpts.FrozenTarget`、
  `client.DispatchOpts.FrozenTarget`、`handlers.dispatchRequest.FrozenTarget *string`、
  `cmd.dispatchRequest.frozenTarget`；`dispatchStep`/`stepTransport` 签名未变（编译期锁）。
- 未验证项：真机清单（plan §11）归协调者，本节点未跑；内容过滤是否有发生——
  本回合未遇 ContentFilterErr。
