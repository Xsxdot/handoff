# B392 contract 台账

## 2026-09-24 开节点与边界

### `git status --short --branch` / `git log --oneline -3`

```text
## cards/B392-charter
nothing to commit, working tree clean
400ee3c4 docs(B392): 冻结 mobile bind 生产接线 spec
a8e60208 test(B380 recon): 冻结条 25 白名单补 drop.go——B272 并线晚于 B378 裁决的历史缺口
```

判断：本节点在 `cards/B392-charter`；有效基线/spec 显式基线为 `cards/B233.1-charter-7 @ a8e60208`。spec 头部状态行「已批准」已复核一致，无需回写。

### 现状查证（源码读数，图外回落到 file:line）

- 旧占位：`mobile/bind/session.go:20-30`（`errSessionsNotWired` / `notWiredSessions` / `sessions`）。
- 配对 Core 装配：`mobile/bind/bind.go:34-43`。
- 核侧：`internal/mobilecore/core.go:219`（Activate 带 ctx）、`:266`（Session）、`:279`（ActiveMachine）、`:333`（Origin）；DTO `internal/mobilecore/session.go:42-50`，兑换超时 `:35`。
- cookie 属性：`internal/agentd/authroutes.go#sessionCookie`（:343）、`internal/agentd/auth.go:28-29`。

### codegraph 读数（图外确认）

- `codegraph sym coreSessions` / `sym newCoreSessions` → 「不在图中」，退出 1（预期：`mobile` 前缀被 `scripts/codegraph-rescan/main.go:231` 排除）。
- `codegraph check` → 基线 fails=0。
- `codegraph validate` → 退出 1，2 个既存问题（B272 视图 `k_dropdir_fn` 引用不存在领域；B374 视图 `k_collab_model` 重复），均非本卡（本卡无视图）。
- `codegraph resolve --doc docs/superpowers/specs/b392-contract.md` → 退出 0，两锚 `n_client_Client_IssueAuthTicket`、`n_agentd_sessionCookie` 均 ok。

判断：本卡无根模块新符号 → target.json / best.json 不改、无视图 diff。

## 2026-09-24 Ticket 0 落地

- 新增 `mobile/bind/adapter.go`：`sessionCoreAPI` 窄消费面 + `coreSessions` 适配器（Activate→校验非空→Origin；ActiveMachine 校验→Session→校验非空→只返回 value；单一 `sync.Mutex`）。
- `mobile/bind/bind.go`：加 `liveCore = mobilecore.New(nil, log)`，`core coreAPI = liveCore`。
- `mobile/bind/session.go`：删 `errSessionsNotWired` / `notWiredSessions`；`sessions = newCoreSessions(liveCore)`。
- 新增 `mobile/bind/adapter_test.go`：顺序、失败闭合、错机拒绝、并发锁、单 Core 身份守卫。

### 本轮跑过的命令与结果

```text
go version → go1.26.1 linux/amd64
go build ./... (root) → exit 0
(cd mobile) go build ./... → exit 0
(cd mobile) go test ./... -count=1 → ok mobile; ok mobile/bind
(cd mobile) go test ./bind/ -race -count=1 → ok
(cd mobile) go vet ./... → exit 0（无输出）
(cd mobile) gofmt -l . → 无输出
```

### 变异证明（改动后立即还原）

```text
变异① Activate 失败路径 return "", nil：
  go test ./bind/ -run TestCoreSessionsSwitchMachineFailsClosed
  → FAIL adapter_test.go:128: Activate 失败必须返回 ("", err): origin="" err=<nil>

变异② 去掉 ActiveMachine==machine 检查（if false && …）：
  go test ./bind/ -run TestCoreSessionsSessionCookieRejectsWrongMachine
  → FAIL adapter_test.go:166: 错机必须返回 ("", err): value="sess-B" err=<nil>

变异③ 去掉适配器 mu.Lock/Unlock：
  go test ./bind/ -run TestCoreSessionsLockSerializesSwitchAndRead
  → FAIL adapter_test.go:219 切机竞态进入 Activate；:230 读到非 A 的 cookie: "sess-B"
```

三条在 double 层确定性打红；还原后 `go test ./...` 全绿。真实 Core 版本的生产守卫与变异归 implement（欠账 §8.1/§8.2）。变异期间一次还原命令因使用绝对 worktree 路径 `cd` 被协调者拦截；随后改用任务注入的 `$TMPDIR` 相对路径从备份还原，`adapter.go` 已确认回到无锁变异前状态（`grep mu.Lock` 两处、测试全绿）。

## 2026-09-24 交棒

- 产出：`docs/superpowers/specs/b392-contract.md`（冻结清单 19 条，其中 `[T0]` 14 条）。
- 欠账：真实 Core 竖切、生产守卫变异、gobind 真产物、README 调用顺序、真机证据、ctx 取消策略、图对齐。
- 交棒：breakdown。
