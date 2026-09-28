# B392 implement 台账

本节点按 `docs/superpowers/plans/b392-plan.md` v4.1（`cards/B392-charter-7@874b4c271`）执行。
分支：`cards/B392-charter-8`。只改 `_test.go`、`mobile/README.md` 与台账；生产 `mobile/bind/*.go` 仅 Task 6 临时变异。

## 2026-09-24 开工基线

### git status / log

```text
## cards/B392-charter-8
nothing to commit, working tree clean
874b4c27 plan(B392): 固化变异证据落账与备份丢失处理
b875087d plan(B392): 保留变异证据并收紧失败回收
fb83e04e plan(B392) v4.1: 变异脚本本体运行后 rm -f，不往仓内写临时文件
```

### 环境读数（本节点亲跑）

```text
go version → 见下方 Task 1 收尾
codegraph → /usr/local/bin/codegraph 在 PATH
TMPDIR=/root/.handoff/tmp/8623f7e9（纪律块：任务内临时目录优先 $TMPDIR；Task 6 按 plan §6.0 用工作树内 .b392-mut.*）
```

## 2026-09-24 Task 1–5 亲跑读数（原始）

```text
go version → go1.26.1 linux/amd64

Task 1:
(cd mobile) go test ./bind/ -run 'TestRealCoreSwitchAndCookieOwnership' -count=1
→ ok github.com/Xsxdot/handoff/mobile/bind 0.007s
(cd mobile) go build ./... → exit 0

Task 2:
(cd mobile) go test ./bind/ -run 'TestRealCoreFailsClosed|TestRealCoreGateSerializesSwitchAndRead' -count=1
→ ok github.com/Xsxdot/handoff/mobile/bind 0.009s
(cd mobile) go test ./bind/ -race -run 'TestRealCoreGateSerializesSwitchAndRead|TestRealCoreConcurrentNoCrossMachine' -count=1
→ ok github.com/Xsxdot/handoff/mobile/bind 1.165s（无 DATA RACE）
(cd mobile) go build ./... → exit 0

Task 3:
(cd mobile) go test ./bind/ -run 'TestDefaultProductionVerticalSlice' -count=1 -v
→ === RUN   TestDefaultProductionVerticalSlice
   --- PASS: TestDefaultProductionVerticalSlice (0.01s)
   PASS
   ok github.com/Xsxdot/handoff/mobile/bind 0.011s
(cd mobile) go build ./... → exit 0

Task 4:
(cd mobile) go test ./bind/ -run 'TestBindSessionNotPairedFailsClosed' -count=1
→ ok github.com/Xsxdot/handoff/mobile/bind 0.003s
(cd mobile) go test ./bind/ -run 'TestCoreSessionsLockSerializesSwitchAndRead' -count=1
→ ok github.com/Xsxdot/handoff/mobile/bind 0.003s
(cd mobile) go build ./... → exit 0

Task 5 红（写 README 前）:
(cd mobile) go test ./bind/ -run 'TestReadmeDocumentsShellCallOrder' -count=1
→ --- FAIL: TestReadmeDocumentsShellCallOrder (0.00s)
     readme_test.go:32: README 缺少调用顺序标记 "`SwitchMachine(target)`"
   FAIL	github.com/Xsxdot/handoff/mobile/bind	0.002s
   FAIL
   exit=1
  （红因核实：功能缺失——README 无该小节，非 typo）

Task 5 绿（写 README 后）:
(cd mobile) go test ./bind/ -run 'TestReadmeDocumentsShellCallOrder' -count=1
→ ok github.com/Xsxdot/handoff/mobile/bind 0.002s
(cd mobile) go build ./... → exit 0
```

判断：Task 1–4 行为已在 HEAD 可达，写即为绿（plan §3 声明，不设先红）；
Task 5 为文档面锁缝，先见断言红再最小实现转绿。生产 `.go` 零改动。

## 2026-09-24 Task 6 第 1 次尝试失败（包路径缺失，非守卫缺失）

### 原始输出（变异①）

```text
MUT_ROOT=./.b392-mut.loR0SR
=== 变异①：生产恢复 notWiredSessions 占位 ===
UNIQUE-HIT-OK mobile/bind/session.go 「var sessions sessionAPI = newCoreSessions(liveCore)」=1
BACKUP-OK mobile/bind/session.go sha256=c7ee28d4e8fbfb038fbc3d7ae29311ee78c8e2ef7bab3d3c4ffc49f7c879d8d5
MUT-BUILD-OK
testing: warning: no tests to run
PASS
ok  	github.com/Xsxdot/handoff/mobile	0.002s
MUT-NOT-RED[mut1]: 测试仍绿（exit 0），守卫缺失
RESTORED-OK mobile/bind/session.go sha256=c7ee28d4e8fbfb038fbc3d7ae29311ee78c8e2ef7bab3d3c4ffc49f7c879d8d5
TRAP-RESTORE-SUMMARY mobile/bind/session.go=restored mobile/bind/adapter.go=untouched
MUT-ROOT-KEPT ./.b392-mut.loR0SR（原始 rc=1；保留红输出与现场排障）
```

### 诊断（亲跑读数，非记忆）

- 报文显示包是 `github.com/Xsxdot/handoff/mobile`（无 `./bind/`），且 `warning: no tests to run`——plan §6.1–6.4 的 `run_expect_red` 剩余参数只有 `-run <pattern>`，函数体 `cd mobile && go test "$@"` 缺包路径 `./bind/`，落在 `mobile` 根包。
- 变异已由 trap 还原（`RESTORED-OK`，hash 与 backup 一致）；生产文件干净。
- 判定：**plan 参数笔误**（Task 2–5 的命令均显式 `./bind/`），不是守卫缺失。修正：`run_expect_red`/`run_capture` 的剩余参数补 `./bind/`。
- 保留现场 `./.b392-mut.loR0SR` 已诊断完毕，重跑前删除。

## 2026-09-24 B392 Task 6 原始变异输出（第 2 次，含 ./bind/ 包路径修正）

--- FAIL: TestDefaultRuntimeSharesOneCore (0.00s)
    adapter_test.go:302: 会话面生产装配不是 coreSessions: bind.notWiredSessions
--- FAIL: TestDefaultProductionVerticalSlice (0.00s)
    default_slice_test.go:79: 默认生产竖转子进程失败: exit status 1
        === RUN   TestDefaultProductionVerticalSlice
        2026/09/24 19:10:32 INFO 绑定面收到配对请求 payload_bytes=228
        2026/09/24 19:10:32 INFO 机器配对在线 machine=A origin=http://127.0.0.1:38325
        2026/09/24 19:10:32 INFO 机器配对在线 machine=B origin=http://127.0.0.1:34347
        2026/09/24 19:10:32 INFO 绑定面配对完成 machines=2
        2026/09/24 19:10:32 INFO 绑定面切机 machine=A
        2026/09/24 19:10:32 ERROR 绑定面切机失败 machine=A cause=会话未接线
            default_slice_test.go:114: 切机 A: 会话未接线
        2026/09/24 19:10:32 INFO 绑定面已关闭
        --- FAIL: TestDefaultProductionVerticalSlice (0.00s)
        FAIL
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.006s
FAIL
--- FAIL: TestCoreSessionsSessionCookieRejectsWrongMachine (0.00s)
    adapter_test.go:158: 错机必须返回 ("", err): value="sess-B" err=<nil>
2026/09/24 19:10:32 INFO 绑定面收到配对请求 payload_bytes=227
2026/09/24 19:10:32 INFO 机器配对在线 machine=A origin=http://127.0.0.1:45155
2026/09/24 19:10:32 INFO 机器配对在线 machine=B origin=http://127.0.0.1:37899
2026/09/24 19:10:32 INFO 绑定面配对完成 machines=2
2026/09/24 19:10:32 INFO 绑定面切机 machine=A
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:45155
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:45155 cookie_name=handoff_session secure=false
2026/09/24 19:10:32 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:45155 cookie_name=handoff_session
2026/09/24 19:10:32 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:45155
2026/09/24 19:10:32 INFO 绑定面切机 machine=B
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:37899
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:37899 cookie_name=handoff_session secure=false
2026/09/24 19:10:32 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:37899 cookie_name=handoff_session
2026/09/24 19:10:32 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:37899
--- FAIL: TestRealCoreSwitchAndCookieOwnership (0.00s)
    realcore_test.go:231: 切到 B 后取 A 必须失败: value="sess-handoff-mobile-B-1" err=<nil>
2026/09/24 19:10:32 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:10:32 INFO 机器配对在线 machine=A origin=http://127.0.0.1:45739
2026/09/24 19:10:32 INFO 机器配对在线 machine=B origin=http://127.0.0.1:41795
2026/09/24 19:10:32 INFO 绑定面配对完成 machines=2
2026/09/24 19:10:32 INFO 绑定面切机 machine=A
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:45739
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:45739 cookie_name=handoff_session secure=false
2026/09/24 19:10:32 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:45739 cookie_name=handoff_session
2026/09/24 19:10:32 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:45739
2026/09/24 19:10:32 INFO 绑定面切机 machine=B
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:41795
2026/09/24 19:10:32 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:41795 cookie_name=handoff_session secure=false
2026/09/24 19:10:32 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:41795 cookie_name=handoff_session
2026/09/24 19:10:32 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:41795
--- FAIL: TestRealCoreGateSerializesSwitchAndRead (0.00s)
    realcore_test.go:446: 切到 B 后 SessionCookie(A) 必须失败，不得返回值: value="sess-handoff-mobile-B-1"
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.007s
FAIL
--- FAIL: TestCoreSessionsSwitchMachineFailsClosed (0.00s)
    --- FAIL: TestCoreSessionsSwitchMachineFailsClosed/activate_失败 (0.00s)
        adapter_test.go:120: Activate 失败必须返回 ("", err): origin="" err=<nil>
2026/09/24 19:10:33 INFO 绑定面收到配对请求 payload_bytes=167
2026/09/24 19:10:33 INFO 机器配对在线 machine=A origin=http://127.0.0.1:35473
2026/09/24 19:10:33 INFO 绑定面配对完成 machines=1
2026/09/24 19:10:33 INFO 绑定面切机 machine=ghost
2026/09/24 19:10:33 INFO 绑定面切机完成 machine=ghost origin=""
2026/09/24 19:10:33 INFO 绑定面收到配对请求 payload_bytes=236
2026/09/24 19:10:33 INFO 机器配对在线 machine=A origin=http://127.0.0.1:42925
2026/09/24 19:10:33 WARN 配对机器登记失败，标离线待补配 machine=offline cause="夹具没有机器 \"offline\""
2026/09/24 19:10:33 INFO 绑定面配对完成 machines=2
2026/09/24 19:10:33 INFO 绑定面切机 machine=offline
2026/09/24 19:10:33 INFO 绑定面切机完成 machine=offline origin=""
2026/09/24 19:10:33 INFO 绑定面收到配对请求 payload_bytes=181
2026/09/24 19:10:33 INFO 机器配对在线 machine=nocookie origin=http://127.0.0.1:33089
2026/09/24 19:10:33 INFO 绑定面配对完成 machines=1
2026/09/24 19:10:33 INFO 绑定面切机 machine=nocookie
2026/09/24 19:10:33 INFO 程序化兑换 ticket→cookie 开始 machine=nocookie origin=http://127.0.0.1:33089
2026/09/24 19:10:33 WARN 兑换响应没有会话 cookie machine=nocookie cookie_name=handoff_session
2026/09/24 19:10:33 INFO 绑定面切机完成 machine=nocookie origin=""
2026/09/24 19:10:33 INFO 绑定面收到配对请求 payload_bytes=167
2026/09/24 19:10:33 INFO 机器配对在线 machine=A origin=http://127.0.0.1:38403
2026/09/24 19:10:33 INFO 绑定面配对完成 machines=1
2026/09/24 19:10:33 INFO 绑定面切机 machine=A
2026/09/24 19:10:33 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:38403
2026/09/24 19:10:33 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:38403 cookie_name=handoff_session secure=false
2026/09/24 19:10:33 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:38403 cookie_name=handoff_session
2026/09/24 19:10:33 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:38403
2026/09/24 19:10:33 INFO 绑定面已关闭
2026/09/24 19:10:33 INFO 绑定面切机 machine=A
2026/09/24 19:10:33 INFO 绑定面切机完成 machine=A origin=""
--- FAIL: TestRealCoreFailsClosed (0.00s)
    --- FAIL: TestRealCoreFailsClosed/未配对 (0.00s)
        realcore_test.go:288: 未配对切机必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/离线 (0.00s)
        realcore_test.go:298: 离线切机必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/兑换无_cookie (0.00s)
        realcore_test.go:309: 兑换无 cookie 必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/Close_后 (0.00s)
        realcore_test.go:325: Close 后切机必须 ("", err): value="" err=<nil>
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.010s
FAIL
2026/09/24 19:10:34 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:10:34 INFO 机器配对在线 machine=A origin=http://127.0.0.1:41561
2026/09/24 19:10:34 INFO 机器配对在线 machine=B origin=http://127.0.0.1:37951
2026/09/24 19:10:34 INFO 绑定面配对完成 machines=2
2026/09/24 19:10:34 INFO 绑定面切机 machine=A
2026/09/24 19:10:34 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:41561
2026/09/24 19:10:34 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:41561 cookie_name=handoff_session secure=false
2026/09/24 19:10:34 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:41561 cookie_name=handoff_session
2026/09/24 19:10:34 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:41561
2026/09/24 19:10:34 INFO 绑定面切机 machine=B
2026/09/24 19:10:34 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:37951
2026/09/24 19:10:34 ERROR 绑定面取会话 cookie 失败 machine=A cause="当前活动机器不是 \"A\"，拒绝返回其会话 cookie"
2026/09/24 19:10:34 ERROR 绑定面取会话 cookie 失败 machine=B cause="当前活动机器不是 \"B\"，拒绝返回其会话 cookie"
2026/09/24 19:10:34 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:37951 cookie_name=handoff_session secure=false
2026/09/24 19:10:34 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:37951 cookie_name=handoff_session
2026/09/24 19:10:34 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:37951
--- FAIL: TestRealCoreGateSerializesSwitchAndRead (0.01s)
    realcore_test.go:433: 切机 B 完成后 SessionCookie(B) 应逐字返回 B 真实值: got="" want="sess-handoff-mobile-B-1" err=当前活动机器不是 "B"，拒绝返回其会话 cookie
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.017s
FAIL
--- FAIL: TestCoreSessionsLockSerializesSwitchAndRead (0.00s)
    adapter_test.go:202: Session 阻塞期间适配器锁未被持有：切机与读会互相穿插
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.003s
FAIL

## 2026-09-24 Task 6 第 2 次成功（./bind/ 包路径修正后）

### 四变异判定（run_expect_red 硬断言全过）

| 变异 | 逐名 FAIL | 红证据 | 结果 |
|---|---|---|---|
| ① 恢复 notWiredSessions | TestDefaultProductionVerticalSlice + TestDefaultRuntimeSharesOneCore | 默认生产竖转子进程失败 / 会话面生产装配不是 coreSessions: bind.notWiredSessions | MUT-RED-OK |
| ② 去活动机检查 | RejectsWrongMachine + RealCoreSwitchAndCookieOwnership + RealCoreGate | 错机必须返回 value="sess-B" / 切到 B 后取 A 必须失败 / 切到 B 后 SessionCookie(A) 必须失败 | MUT-RED-OK |
| ③ 失败路径 `return "", nil` | SwitchMachineFailsClosed + RealCoreFailsClosed | Activate 失败必须返回 ("", err): origin="" err=<nil>；未配对/离线/无cookie/Close 后同模式 | MUT-RED-OK |
| ④ 去适配器互斥 | TestCoreSessionsLockSerializesSwitchAndRead（唯一确定性红） | Session 阻塞期间适配器锁未被持有 | MUT-RED-OK |
| ④ 附加闸 `-race` gate | run_capture 仅记录 | exit=1（本次恰红：SessionCookie(B) 被错机拒绝打断），不断言 | MUT-CAPTURE exit=1 |

原始红输出已整段追加本台账「Task 6 原始变异输出（第 2 次）」节。

### 还原与清理（亲跑）

```text
RESTORED-OK mobile/bind/session.go sha256=c7ee28d4e8fbfb038fbc3d7ae29311ee78c8e2ef7bab3d3c4ffc49f7c879d8d5
RESTORED-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221
TRAP-RESTORE-SUMMARY mobile/bind/session.go=restored mobile/bind/adapter.go=restored
MUT-ROOT-CLEANED ./.b392-mut.L6tGPi
(cd mobile && go test ./... -count=1) → ok mobile 0.190s; ok mobile/bind 0.081s
PROD-FILES-CLEAN（git diff --name-only 不含 adapter.go/bind.go/session.go）
```

## 2026-09-24 收口验证（亲跑原始）

```text
(cd mobile) go build ./...                    → exit 0
(cd mobile) go test ./... -count=1            → ok mobile 0.157s; ok mobile/bind 0.084s
(cd mobile) go test ./bind/ -race -count=1    → ok mobile/bind 2.525s（无 DATA RACE）
(cd mobile) go vet ./...                      → exit 0（无输出）
(cd mobile) gofmt -l .                        → 首跑报 bind/readme_test.go；gofmt -w 后无输出
(cd mobile) go test ./bind/ -run TestReadme... → ok 0.003s（gofmt 后复跑）

go build ./... (root)                         → exit 0
test "$(go list ./... | grep -c handoff/mobile)" = 0       → 通过
test "$(go list -deps ./... | grep -c handoff/mobile)" = 0 → 通过
codegraph --repo . check                      → 退出 0，fails=[]（warns 41 条基线既存）
codegraph --repo . resolve --doc docs/superpowers/plans/b392-plan.md
  → 退出 0；两锚 sessionCookie(:343) / Client.IssueAuthTicket(:1267) 均 ok

git status --short（收口前）
  M mobile/README.md
  M mobile/bind/adapter_test.go
  M mobile/bind/bind_test.go
  ?? docs/superpowers/ledgers/2026-09-24-b392-implement-ledger.md
  ?? mobile/bind/default_slice_test.go
  ?? mobile/bind/readme_test.go
  ?? mobile/bind/realcore_test.go
无 .b392-mut.* 残留
```

### 未验项（如实）

- gobind 真产物（`TestGomobileSurfaceHasNoSkips`）：本机未装钉版 gobind，**未验**（缺工具 skip ≠ 绿）。
- 根模块全量测试：按 plan §6 归 acceptance，本节点未跑。
- 真机 AAR/XCFramework / 平台 cookie jar：归协调者，**未验**。

### 自审

- ①无未跑结论写入；②未碰 handoff CLI、未起新 executor。
- 生产 `.go` 仅 Task 6 临时变异，已 hash+cmp 还原并 PROD-FILES-CLEAN。
- 永久改动仅：`mobile/bind/*_test.go`（新建/改测试）、`mobile/README.md`、本台账。

## 2026-09-24 B392 charter-9 实现审查 findings 修复（基于 charter-8@ee5be416）

分支：cards/B392-charter-9。只改 `_test.go` 与本台账；生产 `mobile/bind/*.go` 仅 Task 6 临时变异。

### 修复项

1. `default_slice_test.go` 父进程断言从「含 PASS」收紧为：含 `=== RUN   TestDefaultProductionVerticalSlice` 且含 `--- PASS: TestDefaultProductionVerticalSlice` 且不含 `SKIP`（拒绝 no-tests/skip）。
2. `adapter_test.go`：`sessionCoreDouble` 增共享 `callSeq`（Activate/Origin 按发生顺序 append），`TestCoreSessionsSwitchMachineSequence` 断言 `callSeq == [Activate:A, Origin:A]`（相对顺序 Activate 先于 Origin），不只数各自切片。
3. `realcore_test.go` 并发补充测试：增 `successReads atomic.Int64`，成功读 +1，结束断言 `successReads > 0`；主 gate / TryLock 证据不动。

### 修复后触及包亲跑（原始）

```text
cd mobile && gofmt -l . → 无输出
cd mobile && go build ./... → BUILD-OK
cd mobile && go test ./bind/ -run 'TestDefaultProductionVerticalSlice|TestCoreSessionsSwitchMachineSequence|TestCoreSessionsLockSerializesSwitchAndRead|TestRealCoreConcurrentNoCrossMachine' -count=1 -v
→ --- PASS: TestCoreSessionsSwitchMachineSequence (0.00s)
   --- PASS: TestCoreSessionsLockSerializesSwitchAndRead (0.00s)
   --- PASS: TestDefaultProductionVerticalSlice (0.01s)
   --- PASS: TestRealCoreConcurrentNoCrossMachine (0.12s)
   PASS / ok github.com/Xsxdot/handoff/mobile/bind 0.125s
cd mobile && go test ./... -count=1
→ ok github.com/Xsxdot/handoff/mobile 0.183s
   ok github.com/Xsxdot/handoff/mobile/bind 0.086s
```

## 2026-09-24 B392 Task 6 原始变异输出（charter-9 重跑，MUT_ROOT=./.b392-mut.r4xIl1，./bind/ 已固定）

MUT_ROOT=./.b392-mut.r4xIl1
PWD=/root/.handoff/worktrees/8e8e1456
bash=5.2.21(1)-release
=== 变异①：生产恢复 notWiredSessions 占位 ===
UNIQUE-HIT-OK mobile/bind/session.go 「var sessions sessionAPI = newCoreSessions(liveCore)」=1
BACKUP-OK mobile/bind/session.go sha256=c7ee28d4e8fbfb038fbc3d7ae29311ee78c8e2ef7bab3d3c4ffc49f7c879d8d5
MUT-BUILD-OK
--- FAIL: TestDefaultRuntimeSharesOneCore (0.00s)
    adapter_test.go:311: 会话面生产装配不是 coreSessions: bind.notWiredSessions
--- FAIL: TestDefaultProductionVerticalSlice (0.00s)
    default_slice_test.go:79: 默认生产竖转子进程失败: exit status 1
        === RUN   TestDefaultProductionVerticalSlice
        2026/09/24 19:51:22 INFO 绑定面收到配对请求 payload_bytes=228
        2026/09/24 19:51:22 INFO 机器配对在线 machine=A origin=http://127.0.0.1:40695
        2026/09/24 19:51:22 INFO 机器配对在线 machine=B origin=http://127.0.0.1:39031
        2026/09/24 19:51:22 INFO 绑定面配对完成 machines=2
        2026/09/24 19:51:22 INFO 绑定面切机 machine=A
        2026/09/24 19:51:22 ERROR 绑定面切机失败 machine=A cause=会话未接线
            default_slice_test.go:118: 切机 A: 会话未接线
        2026/09/24 19:51:22 INFO 绑定面已关闭
        --- FAIL: TestDefaultProductionVerticalSlice (0.00s)
        FAIL
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.009s
FAIL
MUT-RED-OK[mut1]: exit=1，逐名 FAIL 命中「TestDefaultProductionVerticalSlice TestDefaultRuntimeSharesOneCore」，证据命中 /默认生产竖转子进程失败|会话面生产装配不是 coreSessions/
RESTORED-OK mobile/bind/session.go sha256=c7ee28d4e8fbfb038fbc3d7ae29311ee78c8e2ef7bab3d3c4ffc49f7c879d8d5
=== 变异②：去掉活动机检查 ===
UNIQUE-HIT-OK mobile/bind/adapter.go 「if a.core.ActiveMachine() != machine {」=1
BACKUP-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221
MUT-BUILD-OK
--- FAIL: TestCoreSessionsSessionCookieRejectsWrongMachine (0.00s)
    adapter_test.go:167: 错机必须返回 ("", err): value="sess-B" err=<nil>
2026/09/24 19:51:22 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:51:22 INFO 机器配对在线 machine=A origin=http://127.0.0.1:39447
2026/09/24 19:51:22 INFO 机器配对在线 machine=B origin=http://127.0.0.1:38893
2026/09/24 19:51:22 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:22 INFO 绑定面切机 machine=A
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:39447
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:39447 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:39447 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:39447
2026/09/24 19:51:22 INFO 绑定面切机 machine=B
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:38893
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:38893 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:38893 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:38893
--- FAIL: TestRealCoreSwitchAndCookieOwnership (0.00s)
    realcore_test.go:232: 切到 B 后取 A 必须失败: value="sess-handoff-mobile-B-1" err=<nil>
2026/09/24 19:51:22 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:51:22 INFO 机器配对在线 machine=A origin=http://127.0.0.1:38089
2026/09/24 19:51:22 INFO 机器配对在线 machine=B origin=http://127.0.0.1:37195
2026/09/24 19:51:22 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:22 INFO 绑定面切机 machine=A
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:38089
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:38089 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:38089 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:38089
2026/09/24 19:51:22 INFO 绑定面切机 machine=B
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:37195
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:37195 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:37195 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:37195
--- FAIL: TestRealCoreGateSerializesSwitchAndRead (0.00s)
    realcore_test.go:447: 切到 B 后 SessionCookie(A) 必须失败，不得返回值: value="sess-handoff-mobile-B-1"
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.007s
FAIL
MUT-RED-OK[mut2]: exit=1，逐名 FAIL 命中「TestCoreSessionsSessionCookieRejectsWrongMachine TestRealCoreSwitchAndCookieOwnership TestRealCoreGateSerializesSwitchAndRead」，证据命中 /错机必须返回|取 A 必须失败|SessionCookie\(A\) 必须失败/
RESTORED-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221
=== 变异③：失败路径改 return "", nil ===
UNIQUE-HIT-OK mobile/bind/adapter.go 「ck, err := a.core.Activate(context.Background(), machine)」=1
BACKUP-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221
UNIQUE-HIT-OK mobile/bind/adapter.go 「return "", nil」=1
MUT-BUILD-OK
--- FAIL: TestCoreSessionsSwitchMachineFailsClosed (0.00s)
    --- FAIL: TestCoreSessionsSwitchMachineFailsClosed/activate_失败 (0.00s)
        adapter_test.go:129: Activate 失败必须返回 ("", err): origin="" err=<nil>
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=166
2026/09/24 19:51:23 INFO 机器配对在线 machine=A origin=http://127.0.0.1:34043
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=1
2026/09/24 19:51:23 INFO 绑定面切机 machine=ghost
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=ghost origin=""
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=236
2026/09/24 19:51:23 INFO 机器配对在线 machine=A origin=http://127.0.0.1:36935
2026/09/24 19:51:23 WARN 配对机器登记失败，标离线待补配 machine=offline cause="夹具没有机器 \"offline\""
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:23 INFO 绑定面切机 machine=offline
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=offline origin=""
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=181
2026/09/24 19:51:23 INFO 机器配对在线 machine=nocookie origin=http://127.0.0.1:34385
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=1
2026/09/24 19:51:23 INFO 绑定面切机 machine=nocookie
2026/09/24 19:51:23 INFO 程序化兑换 ticket→cookie 开始 machine=nocookie origin=http://127.0.0.1:34385
2026/09/24 19:51:23 WARN 兑换响应没有会话 cookie machine=nocookie cookie_name=handoff_session
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=nocookie origin=""
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=167
2026/09/24 19:51:23 INFO 机器配对在线 machine=A origin=http://127.0.0.1:36773
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=1
2026/09/24 19:51:23 INFO 绑定面切机 machine=A
2026/09/24 19:51:23 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:36773
2026/09/24 19:51:23 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:36773 cookie_name=handoff_session secure=false
2026/09/24 19:51:23 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:36773 cookie_name=handoff_session
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:36773
2026/09/24 19:51:23 INFO 绑定面已关闭
2026/09/24 19:51:23 INFO 绑定面切机 machine=A
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=A origin=""
--- FAIL: TestRealCoreFailsClosed (0.00s)
    --- FAIL: TestRealCoreFailsClosed/未配对 (0.00s)
        realcore_test.go:289: 未配对切机必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/离线 (0.00s)
        realcore_test.go:299: 离线切机必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/兑换无_cookie (0.00s)
        realcore_test.go:310: 兑换无 cookie 必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/Close_后 (0.00s)
        realcore_test.go:326: Close 后切机必须 ("", err): value="" err=<nil>
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.008s
FAIL
MUT-RED-OK[mut3]: exit=1，逐名 FAIL 命中「TestCoreSessionsSwitchMachineFailsClosed TestRealCoreFailsClosed」，证据命中 /必须 \("", err\)/
RESTORED-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221
=== 变异④：去掉适配器互斥 ===
UNIQUE-HIT-OK mobile/bind/adapter.go 「a.mu.Lock()」=2
UNIQUE-HIT-OK mobile/bind/adapter.go 「defer a.mu.Unlock()」=2
BACKUP-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221
MUT-BUILD-OK
--- FAIL: TestCoreSessionsLockSerializesSwitchAndRead (0.00s)
    adapter_test.go:211: Session 阻塞期间适配器锁未被持有：切机与读会互相穿插
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.003s
FAIL
MUT-RED-OK[mut4-trylock]: exit=1，逐名 FAIL 命中「TestCoreSessionsLockSerializesSwitchAndRead」，证据命中 /适配器锁未被持有/
2026/09/24 19:51:45 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:51:45 INFO 机器配对在线 machine=A origin=http://127.0.0.1:33609
2026/09/24 19:51:45 INFO 机器配对在线 machine=B origin=http://127.0.0.1:35363
2026/09/24 19:51:45 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:45 INFO 绑定面切机 machine=A
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:33609
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:33609 cookie_name=handoff_session secure=false
2026/09/24 19:51:45 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:33609 cookie_name=handoff_session
2026/09/24 19:51:45 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:33609
2026/09/24 19:51:45 INFO 绑定面切机 machine=B
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:35363
2026/09/24 19:51:45 ERROR 绑定面取会话 cookie 失败 machine=B cause="当前活动机器不是 \"B\"，拒绝返回其会话 cookie"
2026/09/24 19:51:45 ERROR 绑定面取会话 cookie 失败 machine=A cause="当前活动机器不是 \"A\"，拒绝返回其会话 cookie"
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:35363 cookie_name=handoff_session secure=false
2026/09/24 19:51:45 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:35363 cookie_name=handoff_session
2026/09/24 19:51:45 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:35363
--- FAIL: TestRealCoreGateSerializesSwitchAndRead (0.01s)
    realcore_test.go:434: 切机 B 完成后 SessionCookie(B) 应逐字返回 B 真实值: got="" want="sess-handoff-mobile-B-1" err=当前活动机器不是 "B"，拒绝返回其会话 cookie
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.016s
FAIL
MUT-CAPTURE[mut4-race]: exit=1（附加闸，不断言）
RESTORED-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221
=== 6.5 还原核验 ===
RESTORED-OK mobile/bind/session.go sha256=c7ee28d4e8fbfb038fbc3d7ae29311ee78c8e2ef7bab3d3c4ffc49f7c879d8d5
RESTORED-OK mobile/bind/adapter.go sha256=7a1aabed0177cf689c84dd7774f5403a76b2801d93f6ab7bc19baae2f6e49221

### RED FILE ./.b392-mut.r4xIl1/mut1.red

--- FAIL: TestDefaultRuntimeSharesOneCore (0.00s)
    adapter_test.go:311: 会话面生产装配不是 coreSessions: bind.notWiredSessions
--- FAIL: TestDefaultProductionVerticalSlice (0.00s)
    default_slice_test.go:79: 默认生产竖转子进程失败: exit status 1
        === RUN   TestDefaultProductionVerticalSlice
        2026/09/24 19:51:22 INFO 绑定面收到配对请求 payload_bytes=228
        2026/09/24 19:51:22 INFO 机器配对在线 machine=A origin=http://127.0.0.1:40695
        2026/09/24 19:51:22 INFO 机器配对在线 machine=B origin=http://127.0.0.1:39031
        2026/09/24 19:51:22 INFO 绑定面配对完成 machines=2
        2026/09/24 19:51:22 INFO 绑定面切机 machine=A
        2026/09/24 19:51:22 ERROR 绑定面切机失败 machine=A cause=会话未接线
            default_slice_test.go:118: 切机 A: 会话未接线
        2026/09/24 19:51:22 INFO 绑定面已关闭
        --- FAIL: TestDefaultProductionVerticalSlice (0.00s)
        FAIL
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.009s
FAIL

### RED FILE ./.b392-mut.r4xIl1/mut2.red

--- FAIL: TestCoreSessionsSessionCookieRejectsWrongMachine (0.00s)
    adapter_test.go:167: 错机必须返回 ("", err): value="sess-B" err=<nil>
2026/09/24 19:51:22 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:51:22 INFO 机器配对在线 machine=A origin=http://127.0.0.1:39447
2026/09/24 19:51:22 INFO 机器配对在线 machine=B origin=http://127.0.0.1:38893
2026/09/24 19:51:22 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:22 INFO 绑定面切机 machine=A
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:39447
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:39447 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:39447 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:39447
2026/09/24 19:51:22 INFO 绑定面切机 machine=B
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:38893
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:38893 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:38893 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:38893
--- FAIL: TestRealCoreSwitchAndCookieOwnership (0.00s)
    realcore_test.go:232: 切到 B 后取 A 必须失败: value="sess-handoff-mobile-B-1" err=<nil>
2026/09/24 19:51:22 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:51:22 INFO 机器配对在线 machine=A origin=http://127.0.0.1:38089
2026/09/24 19:51:22 INFO 机器配对在线 machine=B origin=http://127.0.0.1:37195
2026/09/24 19:51:22 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:22 INFO 绑定面切机 machine=A
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:38089
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:38089 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:38089 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:38089
2026/09/24 19:51:22 INFO 绑定面切机 machine=B
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:37195
2026/09/24 19:51:22 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:37195 cookie_name=handoff_session secure=false
2026/09/24 19:51:22 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:37195 cookie_name=handoff_session
2026/09/24 19:51:22 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:37195
--- FAIL: TestRealCoreGateSerializesSwitchAndRead (0.00s)
    realcore_test.go:447: 切到 B 后 SessionCookie(A) 必须失败，不得返回值: value="sess-handoff-mobile-B-1"
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.007s
FAIL

### RED FILE ./.b392-mut.r4xIl1/mut3.red

--- FAIL: TestCoreSessionsSwitchMachineFailsClosed (0.00s)
    --- FAIL: TestCoreSessionsSwitchMachineFailsClosed/activate_失败 (0.00s)
        adapter_test.go:129: Activate 失败必须返回 ("", err): origin="" err=<nil>
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=166
2026/09/24 19:51:23 INFO 机器配对在线 machine=A origin=http://127.0.0.1:34043
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=1
2026/09/24 19:51:23 INFO 绑定面切机 machine=ghost
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=ghost origin=""
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=236
2026/09/24 19:51:23 INFO 机器配对在线 machine=A origin=http://127.0.0.1:36935
2026/09/24 19:51:23 WARN 配对机器登记失败，标离线待补配 machine=offline cause="夹具没有机器 \"offline\""
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:23 INFO 绑定面切机 machine=offline
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=offline origin=""
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=181
2026/09/24 19:51:23 INFO 机器配对在线 machine=nocookie origin=http://127.0.0.1:34385
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=1
2026/09/24 19:51:23 INFO 绑定面切机 machine=nocookie
2026/09/24 19:51:23 INFO 程序化兑换 ticket→cookie 开始 machine=nocookie origin=http://127.0.0.1:34385
2026/09/24 19:51:23 WARN 兑换响应没有会话 cookie machine=nocookie cookie_name=handoff_session
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=nocookie origin=""
2026/09/24 19:51:23 INFO 绑定面收到配对请求 payload_bytes=167
2026/09/24 19:51:23 INFO 机器配对在线 machine=A origin=http://127.0.0.1:36773
2026/09/24 19:51:23 INFO 绑定面配对完成 machines=1
2026/09/24 19:51:23 INFO 绑定面切机 machine=A
2026/09/24 19:51:23 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:36773
2026/09/24 19:51:23 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:36773 cookie_name=handoff_session secure=false
2026/09/24 19:51:23 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:36773 cookie_name=handoff_session
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:36773
2026/09/24 19:51:23 INFO 绑定面已关闭
2026/09/24 19:51:23 INFO 绑定面切机 machine=A
2026/09/24 19:51:23 INFO 绑定面切机完成 machine=A origin=""
--- FAIL: TestRealCoreFailsClosed (0.00s)
    --- FAIL: TestRealCoreFailsClosed/未配对 (0.00s)
        realcore_test.go:289: 未配对切机必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/离线 (0.00s)
        realcore_test.go:299: 离线切机必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/兑换无_cookie (0.00s)
        realcore_test.go:310: 兑换无 cookie 必须 ("", err): value="" err=<nil>
    --- FAIL: TestRealCoreFailsClosed/Close_后 (0.00s)
        realcore_test.go:326: Close 后切机必须 ("", err): value="" err=<nil>
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.008s
FAIL

### RED FILE ./.b392-mut.r4xIl1/mut4-race.red

2026/09/24 19:51:45 INFO 绑定面收到配对请求 payload_bytes=228
2026/09/24 19:51:45 INFO 机器配对在线 machine=A origin=http://127.0.0.1:33609
2026/09/24 19:51:45 INFO 机器配对在线 machine=B origin=http://127.0.0.1:35363
2026/09/24 19:51:45 INFO 绑定面配对完成 machines=2
2026/09/24 19:51:45 INFO 绑定面切机 machine=A
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 开始 machine=A origin=http://127.0.0.1:33609
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 成功 machine=A origin=http://127.0.0.1:33609 cookie_name=handoff_session secure=false
2026/09/24 19:51:45 INFO 会话罐已切换 machine=A origin=http://127.0.0.1:33609 cookie_name=handoff_session
2026/09/24 19:51:45 INFO 绑定面切机完成 machine=A origin=http://127.0.0.1:33609
2026/09/24 19:51:45 INFO 绑定面切机 machine=B
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 开始 machine=B origin=http://127.0.0.1:35363
2026/09/24 19:51:45 ERROR 绑定面取会话 cookie 失败 machine=B cause="当前活动机器不是 \"B\"，拒绝返回其会话 cookie"
2026/09/24 19:51:45 ERROR 绑定面取会话 cookie 失败 machine=A cause="当前活动机器不是 \"A\"，拒绝返回其会话 cookie"
2026/09/24 19:51:45 INFO 程序化兑换 ticket→cookie 成功 machine=B origin=http://127.0.0.1:35363 cookie_name=handoff_session secure=false
2026/09/24 19:51:45 INFO 会话罐已切换 machine=B origin=http://127.0.0.1:35363 cookie_name=handoff_session
2026/09/24 19:51:45 INFO 绑定面切机完成 machine=B origin=http://127.0.0.1:35363
--- FAIL: TestRealCoreGateSerializesSwitchAndRead (0.01s)
    realcore_test.go:434: 切机 B 完成后 SessionCookie(B) 应逐字返回 B 真实值: got="" want="sess-handoff-mobile-B-1" err=当前活动机器不是 "B"，拒绝返回其会话 cookie
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.016s
FAIL

### RED FILE ./.b392-mut.r4xIl1/mut4-trylock.red

--- FAIL: TestCoreSessionsLockSerializesSwitchAndRead (0.00s)
    adapter_test.go:211: Session 阻塞期间适配器锁未被持有：切机与读会互相穿插
FAIL
FAIL	github.com/Xsxdot/handoff/mobile/bind	0.003s
FAIL
=== 收口验证 charter-9（亲跑原始）===
--- go version
go version go1.26.1 linux/amd64
--- (cd mobile) go build ./...
exit=0
--- (cd mobile) go test ./... -count=1
ok  	github.com/Xsxdot/handoff/mobile	0.154s
ok  	github.com/Xsxdot/handoff/mobile/bind	0.087s
--- (cd mobile) go test ./bind/ -race -count=1
ok  	github.com/Xsxdot/handoff/mobile/bind	2.287s
--- (cd mobile) go vet ./...
exit=0
--- (cd mobile) gofmt -l .
[]
gofmt-clean
--- go build ./... (root)
exit=0
--- grep mobile isolation
list=0 deps=0
isolation-ok
--- codegraph --repo . check
{
 "fails": [],
 "warns": [
  {
   "kind": "best-dangling",
   "from": "k_mobilecore_Core",
   "detail": "best.json 容器在当前视图中不存在非 deleted 节点"
  },
  {
   "kind": "best-dangling",
   "from": "k_mobilecore_fn",
   "detail": "best.json 容器在当前视图中不存在非 deleted 节点"
  },
  {
   "kind": "best-dangling",
   "from": "k_mobilecore_model",
   "detail": "best.json 容器在当前视图中不存在非 deleted 节点"
  },
  {
   "kind": "best-dangling",
   "from": "k_proto_PairBundle",
   "detail": "best.json 容器在当前视图中不存在非 deleted 节点"
  },
  {
   "kind": "best-dangling",
   "from": "k_proto_PairMachine",
   "detail": "best.json 容器在当前视图中不存在非 deleted 节点"
  },
  {
   "kind": "budget-raised",
   "from": "d_cli",
   "to": "d_ledger",
   "detail": "契约 d_cli→d_ledger 预算 4→11 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_cli",
   "to": "d_maintenance",
   "detail": "契约 d_cli→d_maintenance 预算 18→25 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_cli",
   "to": "d_policy",
   "detail": "契约 d_cli→d_policy 预算 32→35 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_cli",
   "to": "d_protocol",
   "detail": "契约 d_cli→d_protocol 预算 1→2 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_cli",
   "to": "d_transport",
   "detail": "契约 d_cli→d_transport 预算 10→12 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_gateway",
   "to": "d_execution",
   "detail": "契约 d_gateway→d_execution 预算 1→11 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_gateway",
   "to": "d_maintenance",
   "detail": "契约 d_gateway→d_maintenance 预算 7→25 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_gateway",
   "to": "d_policy",
   "detail": "契约 d_gateway→d_policy 预算 27→31 上涨"
  },
  {
   "kind": "budget-raised",
   "from": "d_policy",
   "to": "d_maintenance",
   "detail": "契约 d_policy→d_maintenance 预算 2→3 上涨"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_execution",
   "detail": "d_cli->d_execution 预算内直调 6/11（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_ledger",
   "detail": "d_cli->d_ledger 预算内直调 10/11（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_maintenance",
   "detail": "d_cli->d_maintenance 预算内直调 25/25（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_policy",
   "detail": "d_cli->d_policy 预算内直调 35/35（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_sessions",
   "detail": "d_cli->d_sessions 预算内直调 2/2（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_transport",
   "detail": "d_cli->d_transport 预算内直调 4/12（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_collab",
   "to": "d_protocol",
   "detail": "d_collab->d_protocol 预算内直调 1/1（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_gateway",
   "to": "d_execution",
   "detail": "d_gateway->d_execution 预算内直调 4/11（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_gateway",
   "to": "d_ledger",
   "detail": "d_gateway->d_ledger 预算内直调 10/17（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_gateway",
   "to": "d_maintenance",
   "detail": "d_gateway->d_maintenance 预算内直调 25/25（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_gateway",
   "to": "d_orchestration",
   "detail": "d_gateway->d_orchestration 预算内直调 76/76（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_gateway",
   "to": "d_policy",
   "detail": "d_gateway->d_policy 预算内直调 29/31（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_gateway",
   "to": "d_transport",
   "detail": "d_gateway->d_transport 预算内直调 4/9（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_ledger",
   "to": "d_protocol",
   "detail": "d_ledger->d_protocol 预算内直调 2/2（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_maintenance",
   "to": "d_protocol",
   "detail": "d_maintenance->d_protocol 预算内直调 1/1（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_orchestration",
   "to": "d_maintenance",
   "detail": "d_orchestration->d_maintenance 预算内直调 2/8（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_policy",
   "to": "d_maintenance",
   "detail": "d_policy->d_maintenance 预算内直调 3/3（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_policy",
   "to": "d_transport",
   "detail": "d_policy->d_transport 预算内直调 2/2（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_sessions",
   "to": "d_execution",
   "detail": "d_sessions->d_execution 预算内直调 6/6（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_transport",
   "to": "d_policy",
   "detail": "d_transport->d_policy 预算内直调 3/4（可收窄后调低预算）"
  },
  {
   "kind": "oversized-package",
   "detail": "目录 cmd 有 57 个图内源文件（阈值 40）且没有更深层目录——架构法第三条：必须回答「还能圈出有界文件集吗」"
  },
  {
   "kind": "oversized-package",
   "detail": "目录 internal/agentd 有 57 个图内源文件（阈值 40）且没有更深层目录——架构法第三条：必须回答「还能圈出有界文件集吗」"
  },
  {
   "kind": "prefix-family",
   "detail": "目录 cmd 下前缀族 \"card\" 有 10 个源文件（阈值 5）——架构法第三条：必须回答「还能圈出有界文件集吗」"
  },
  {
   "kind": "prefix-family",
   "detail": "目录 internal/agentd 下前缀族 \"preview\" 有 7 个源文件（阈值 5）——架构法第三条：必须回答「还能圈出有界文件集吗」"
  },
  {
   "kind": "prefix-family",
   "detail": "目录 internal/workspace 下前缀族 \"project\" 有 5 个源文件（阈值 5）——架构法第三条：必须回答「还能圈出有界文件集吗」"
  },
  {
   "kind": "prefix-family",
   "detail": "目录 web/src/app/machines 下前缀族 \"Machine\" 有 6 个源文件（阈值 5）——架构法第三条：必须回答「还能圈出有界文件集吗」"
  },
  {
   "kind": "prefix-family",
   "detail": "目录 web/src/app/workbench 下前缀族 \"terminal\" 有 5 个源文件（阈值 5）——架构法第三条：必须回答「还能圈出有界文件集吗」"
  }
 ],
 "legacyHits": {
  "d_cli->d_execution": 6,
  "d_cli->d_ledger": 10,
  "d_cli->d_maintenance": 25,
  "d_cli->d_policy": 35,
  "d_cli->d_sessions": 2,
  "d_cli->d_transport": 4,
  "d_collab->d_protocol": 1,
  "d_gateway->d_execution": 4,
  "d_gateway->d_ledger": 10,
  "d_gateway->d_maintenance": 25,
  "d_gateway->d_orchestration": 76,
  "d_gateway->d_policy": 29,
  "d_gateway->d_transport": 4,
  "d_ledger->d_protocol": 2,
  "d_maintenance->d_protocol": 1,
  "d_orchestration->d_maintenance": 2,
  "d_policy->d_maintenance": 3,
  "d_policy->d_transport": 2,
  "d_sessions->d_execution": 6,
  "d_transport->d_policy": 3
 },
 "bestCoverage": {
  "assignedContainers": 338,
  "viewContainers": 338,
  "crossDomainEdges": 1254,
  "misplacedSkipped": 0
 }
}
check_exit=0
--- codegraph --repo . resolve --doc
{
 "anchors": [
  {
   "ref": "internal/agentd/authroutes.go#sessionCookie",
   "file": "internal/agentd/authroutes.go",
   "line": 343,
   "anchor": "ok",
   "nodeId": "n_agentd_sessionCookie"
  },
  {
   "ref": "internal/client/client.go#Client.IssueAuthTicket",
   "file": "internal/client/client.go",
   "line": 1267,
   "anchor": "ok",
   "nodeId": "n_client_Client_IssueAuthTicket"
  }
 ]
}
resolve_exit=0
--- mut residue
none
--- git status
 M docs/superpowers/ledgers/2026-09-24-b392-implement-ledger.md
 M mobile/bind/adapter_test.go
 M mobile/bind/default_slice_test.go
 M mobile/bind/realcore_test.go
--- git diff --stat
 .../ledgers/2026-09-24-b392-implement-ledger.md    | 637 +++++++++++++++++++++
 mobile/bind/adapter_test.go                        |   9 +
 mobile/bind/default_slice_test.go                  |   8 +-
 mobile/bind/realcore_test.go                       |   6 +
 4 files changed, 658 insertions(+), 2 deletions(-)

### 未验项（charter-9 如实，不宣称 acceptance）

- gobind 真产物（`TestGomobileSurfaceHasNoSkips` / 钉版比对）：`command -v gobind` → gobind-not-installed，**未验**（缺工具 skip ≠ 绿）。
- Android AAR / iOS XCFramework 重建与真机 cookie 行为：本机无对应环境，**未验**（归协调者 §4.4）。
- 根模块全量测试：按 plan §6 归 acceptance，本节点未跑全量（仅根 `go build ./...` 已跑 exit=0）。
- 本回合**未宣称 acceptance 通过**。

### 自审（charter-9）

- ① 无未跑结论：所有写入台账的命令均有本回合原始输出；gobind/AAR/真机标未验。
- ② 未碰 handoff CLI、未起新 executor。
- Task 6 在任务工作树内唯一 `./.b392-mut.r4xIl1` 同一 Bash 进程执行（未用仓外脚本），`./bind/` 包路径固定，四变异 MUT-RED-OK，还原 RESTORED-OK，MUT-ROOT-CLEANED，PROD-FILES-CLEAN。
- 永久改动仅：`mobile/bind/default_slice_test.go`、`adapter_test.go`、`realcore_test.go`、本台账。

---

## 2026-09-24 charter-10 小修（基于 charter-9@de13b690）

分支：`cards/B392-charter-10`。基线 HEAD=`de13b690`。只改 `mobile/bind/realcore_test.go` 与本台账。

### 协调者补充要求（原文要点）

- 并发 worker 等待改为带 timeout 的 done 通道，避免无界 `wg.Wait`；
- 增加确定性的真实读活性证明或等价的 `successReads` 断言，不能让全部 SessionCookie 错误仍然通过；
- 不要删除或弱化 gate、TryLock、四变异证据；
- 补 implement ledger，触及包与 race/vet/gofmt 复跑；只改测试和台账，完成后提交。

### 改动（`mobile/bind/realcore_test.go`，仅 TestRealCoreConcurrentNoCrossMachine）

1. `wg.Wait()` → `done` 通道 + `select`/`time.After(30s)`；超时 `t.Fatalf`（带上下文），禁止无界等待。
2. 读活性两层：
   - 并发前置串行读：`SwitchMachine(A/B)` 后立刻 `SessionCookie` 必须成功且含 `-A-`/`-B-`（确定性，不依赖调度）；
   - 并发结束保留既有 `successReads atomic.Int64 > 0` 断言（防「全部错机拒绝」通过）。
3. 未动：`gateConsole`/`TestRealCoreGateSerializesSwitchAndRead`、`TryLock`/`TestCoreSessionsLockSerializesSwitchAndRead`、四变异证据与 adapter/default_slice 文件。

### 亲跑读数（原始输出）

```text
branch= cards/B392-charter-10
base HEAD= de13b6902037c1ca5a6ba3c15b02d866fab94f58
go version= go1.26.1 linux/amd64

=== build ===
(cd mobile) go build ./...
build_exit=0

=== test bind ===
(cd mobile) go test ./bind/ -count=1
ok  	github.com/Xsxdot/handoff/mobile/bind	0.136s
test_exit=0

=== race bind ===
(cd mobile) go test ./bind/ -race -count=1
ok  	github.com/Xsxdot/handoff/mobile/bind	2.390s
race_exit=0

=== vet ===
(cd mobile) go vet ./...
vet_exit=0

=== gofmt ===
(cd mobile) gofmt -l .
gofmt_exit=0   （无输出）

=== concurrent x5（抗 successReads 抖动）===
ok  	github.com/Xsxdot/handoff/mobile/bind	0.078s
ok  	github.com/Xsxdot/handoff/mobile/bind	0.073s
ok  	github.com/Xsxdot/handoff/mobile/bind	0.084s
ok  	github.com/Xsxdot/handoff/mobile/bind	0.065s
ok  	github.com/Xsxdot/handoff/mobile/bind	0.076s

=== gate+trylock+竖切回归 ===
(cd mobile) go test ./bind/ -count=1 -run 'TestRealCoreGateSerializesSwitchAndRead|TestCoreSessionsLockSerializesSwitchAndRead|TestRealCoreFailsClosed|TestRealCoreSwitchAndCookieOwnership|TestDefaultProductionVerticalSlice'
ok  	github.com/Xsxdot/handoff/mobile/bind	0.022s

=== git status（改前）===
 M mobile/bind/realcore_test.go

=== git diff --stat ===
 mobile/bind/realcore_test.go | 22 +++++++++++++++++++++-
 1 file changed, 21 insertions(+), 1 deletion(-)
```

### 自审（charter-10）

- ① 无未跑结论：上列命令均有本回合原始输出；未宣称 acceptance；未宣称根模块全量。
- ② 未碰 handoff CLI、未起新 executor、未改生产 `.go`/internal。
- gate、TryLock、四变异证据未删除未弱化（回归测试仍在场且绿）。
- 永久改动仅：`mobile/bind/realcore_test.go`、本台账。
