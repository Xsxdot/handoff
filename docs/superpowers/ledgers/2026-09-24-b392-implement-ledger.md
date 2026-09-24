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
