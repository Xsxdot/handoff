# B395 implement 节点台账

> 卡 B395 · 节点 charter:implement · spec `docs/superpowers/specs/b395.md` · plan `docs/superpowers/plans/b395-plan.md`
> 基线分支 `cards/B395-charter-3`，起手 HEAD `b354d236`。
> 本文件是仓内台账，与本节点产出物同批提交。
> 凡命令与原始输出逐条记；未亲自跑到的结果写「未验证」。

## 0. 硬性第一步

```
$ which codegraph
/usr/local/bin/codegraph
$ git branch --show-current
cards/B395-charter-3
$ git rev-parse HEAD
b354d2364d844a53ec3ab7a8e5fdfabb75e71096
$ git status --porcelain
（无输出，工作树干净）
```

## 1. 图查询（先图后 grep）

```
$ codegraph --repo . sym mapPermissionAsked
命中 n_opencode_Adapter_mapPermissionAsked adapter.go:1542
$ codegraph --repo . sym rediscoverPendingQuestions
命中 n_opencode_Adapter_rediscoverPendingQuestions resume.go
$ codegraph --repo . sym ListPendingQuestions
命中 n_opencode_API_ListPendingQuestions api.go
$ codegraph --repo . sym PauseWaiting
命中 n_turn_Segmenter_PauseWaiting timing.go
$ codegraph --repo . sym acceptForeign
命中 n_opencode_Adapter_acceptForeign adapter.go:1431
$ codegraph --repo . sym subscribeLoop
命中 n_opencode_runState_subscribeLoop adapter.go:1089
```

**图覆盖债**（本节点未逐条 sym，按源码 file:line 给出）：`sseEvent`、`mapEvent`、`RespondPermission`、`newRun`、`emit`、`lookup`、`ListPendingPermissions`（新增符号，实现后补查）。

## 2. 既有夹具复核（grep 原文）

- `adapterTestPassword` = `adapter_test.go:32`
- `quietLog` = `adapter_test.go:35` / `api_test.go:30`
- `captureLog` = `adapter_test.go:63`
- `newFakeServer` = `adapter_test.go:144`（含 `/permissions/` 应答记录 `perms()`）
- `permissionAskedEvent` = `adapter_test.go:356`
- `startFakeRun` = `adapter_test.go:432`
- `waitEventType` = `adapter_test.go:461`
- `newTestAdapter` = `reconcile_internal_test.go:40`
- `drainOne` = `reconcile_internal_test.go:70`
- `promptFileName` = `taskenv.go:96`
- `ListPendingQuestions` 形态参照 = `api.go:702-727`
- `onReconnect` 接线点 = `adapter.go:1110-1119`
- 重启恢复接线点 = `resume.go:190-193`
- `reconcile.go` 过期注释 = `reconcile.go:10-13`

（以下各节随做随记。）

## 3. TDD 首红（编译红）

测试文件先落：
- `internal/executor/opencode/b395_permission_recover_test.go`（T1.4 四条）
- `internal/executor/opencode/b395_regression_test.go`（T2 两条）

```
$ go test ./internal/executor/opencode/ -run 'TestB395|TestPendingPermission' -count=1
# github.com/Xsxdot/handoff/internal/executor/opencode [github.com/Xsxdot/handoff/internal/executor/opencode.test]
internal/executor/opencode/b395_permission_recover_test.go:132:4: a.rediscoverPendingPermissions undefined (type *Adapter has no field or method rediscoverPendingPermissions)
internal/executor/opencode/b395_permission_recover_test.go:152:11: undefined: PendingPermission
internal/executor/opencode/b395_permission_recover_test.go:158:10: undefined: PendingPermission
internal/executor/opencode/b395_permission_recover_test.go:161:15: undefined: PendingPermissionTool
internal/executor/opencode/b395_permission_recover_test.go:167:10: undefined: PendingPermission
internal/executor/opencode/b395_permission_recover_test.go:170:15: undefined: PendingPermissionTool
internal/executor/opencode/b395_permission_recover_test.go:176:10: undefined: PendingPermission
internal/executor/opencode/b395_permission_recover_test.go:179:15: undefined: PendingPermissionTool
internal/executor/opencode/b395_permission_recover_test.go:186:13: undefined: permissionAskedProps
internal/executor/opencode/b395_permission_recover_test.go:216:12: undefined: permissionAskedProps
internal/executor/opencode/b395_permission_recover_test.go:216:12: too many errors
FAIL	github.com/Xsxdot/handoff/internal/executor/opencode [build failed]
EXIT=1
```

核实：全部是 `undefined`（符号缺席），非拼写错——符号名与 plan §5 Produces 逐字一致。首红通过。

## 4. TDD 断言红（空壳落地后）

空壳落点：`permission_recover.go`（permissionAskedProps 返回 nil、rediscover 空转）、
`api.go`（PendingPermission/PendingPermissionTool/ListPendingPermissions 空壳）。

```
$ go test ./internal/executor/opencode/ -run 'TestB395|TestPendingPermission' -count=1
--- FAIL: TestB395ReconnectRecoversPendingPermission (5.00s)
    b395_permission_recover_test.go:102: 重连后未重新发现挂起权限 per_lost（B395 红：权限丢失后回合永挂）
--- FAIL: TestPendingPermissionAskedPropsRoundTrip (0.00s)
    --- FAIL: TestPendingPermissionAskedPropsRoundTrip/metadata.command_存在（真实_perm_bash_形态） (0.00s)
        b395_permission_recover_test.go:192: 未产出 permission 事件
    --- FAIL: TestPendingPermissionAskedPropsRoundTrip/metadata_缺失_→_退回_patterns（区分缺失与零值） (0.00s)
        b395_permission_recover_test.go:192: 未产出 permission 事件
    --- FAIL: TestPendingPermissionAskedPropsRoundTrip/metadata_为空对象_→_结构提取不出，仍带兜底描述（不_panic） (0.00s)
        b395_permission_recover_test.go:192: 未产出 permission 事件
--- FAIL: TestPendingPermissionPropsProjection (0.00s)
    b395_permission_recover_test.go:224: tool 键必须保留，实得 
--- FAIL: TestB395Reconnect404DegradesGracefully (3.01s)
    b395_regression_test.go:87: 旧版端点缺失时应保留可见告警（降级而非静默）
FAIL
FAIL	github.com/Xsxdot/handoff/internal/executor/opencode	8.039s
EXIT=1
```

失败原因均为功能缺失（空壳不产出事件/不调端点），非断言 typo。
未红说明：`TestB395RediscoverFiltersNonTaskSession` 与 `TestB395LivePermissionFlowNotRegressed`
在空壳下暂绿——前者断言「不重放」（空转恰好满足），后者是正常流程反例回归（与 T1 无关的绿色基线），
均不替代上述缝级红。断言红齐，进入实现。

## 5. 实现落点（TDD 绿）

- `api.go`：`PendingPermission` / `PendingPermissionTool` / `ListPendingPermissions`（照抄 ListPendingQuestions 形态）
- `permission_recover.go`（新）：`permissionAskedProps` + `rediscoverPendingPermissions`
- `adapter.go:1110-1120`：onReconnect 接线（Warn 更新 + `go a.rediscoverPendingPermissions`）
- `resume.go:190-196`：重启恢复接线
- `reconcile.go:10-13`：过期注释改为 B395 分流说明（只改注释）

测试断言修正（1 处，记录在案）：
`TestB395RediscoverFiltersNonTaskSession` 原断言「drainOne 不得有事件」，但
`acceptForeign` 对陌生会话会另发「认亲失败」progress 工单（可观测性副作用，非权限重放）。
改为只盯 `Type=="permission"`——语义仍是「不得重放别会话权限」，与 plan 意图一致。

```
$ go test ./internal/executor/opencode/ -run 'TestB395|TestPendingPermission' -count=1
ok  	github.com/Xsxdot/handoff/internal/executor/opencode	3.090s
EXIT=0

$ go test ./internal/executor/opencode/ -run 'TestB395' -count=1
ok  	github.com/Xsxdot/handoff/internal/executor/opencode	3.087s
EXIT=0

$ go test ./internal/executor/opencode/ -run 'TestStartToPermissionFlow|TestReconnectWarnsLostPermission|TestOpencodePermissionWaitNotToolTime|TestOpencodePermissionReplyFailureKeepsWaitingWindow|TestReplaySpike3Permission|TestReplaySpike5Classifies' -count=1
ok  	github.com/Xsxdot/handoff/internal/executor/opencode	1.552s
EXIT=0
```

## 6. 变异自验（两段判定）

锚唯一性：
```
$ grep -c 'go a\.rediscoverPendingPermissions(context\.Background(), r\.taskID)' internal/executor/opencode/adapter.go
1
```

变异（注释掉 onReconnect 接线一行）：
```
$ go build ./internal/executor/opencode/
BUILD_EXIT=0                          ← 第一段：编译过
$ go test ./internal/executor/opencode/ -run 'TestB395ReconnectRecoversPendingPermission' -count=1
--- FAIL: TestB395ReconnectRecoversPendingPermission (5.00s)
    b395_permission_recover_test.go:102: 重连后未重新发现挂起权限 per_lost（B395 红：权限丢失后回合永挂）
MUT_EXIT=1                            ← 第二段：行为断言红
```
还原后无 `MUTATION` 残留，复绿。

## 7. task 收尾检查

```
$ gofmt -l internal/executor/opencode/
（空，FMT_CLEAN）
$ go build ./...
BUILD_ALL_EXIT=0
$ go vet ./internal/executor/opencode/
VET_EXIT=0
$ go test ./internal/executor/opencode/ -count=1
ok  	github.com/Xsxdot/handoff/internal/executor/opencode	21.266s
PKG_TEST_EXIT=0
```

全量测试按三段律留给集成/收尾节点，本节点不跑。
真机断连验收（spec §4.5）按 plan 由协调者执行，不派发。

## 8. 提交（历史读数）

```
$ git add docs/superpowers/ledgers/2026-09-22-b395-implement-ledger.md \
    internal/executor/opencode/adapter.go \
    internal/executor/opencode/api.go \
    internal/executor/opencode/reconcile.go \
    internal/executor/opencode/resume.go \
    internal/executor/opencode/permission_recover.go \
    internal/executor/opencode/b395_permission_recover_test.go \
    internal/executor/opencode/b395_regression_test.go
$ git commit -m "fix(B395): SSE 断连/重启后经 GET /permission 重问未决权限——回合不再永挂"
[cards/B395-charter-3 aed4ae7a] fix(B395): SSE 断连/重启后经 GET /permission 重问未决权限——回合不再永挂
 8 files changed, 748 insertions(+), 10 deletions(-)
$ git rev-parse HEAD
aed4ae7abf1a1548898dcb3fe1b1547e1297dce4
```

随后将本台账追加 amend 进同批提交（amend 换 hash 是 git 既定事实；收口判据是工作树干净，不 chase hash）。
