# B380 contract 节点台账（2026-09-23）

基线：`cards/B233.1-charter-7`，本工作树 HEAD `e5e428a5`。节点：contract（charter）。
产出：`docs/superpowers/specs/b380-contract.md` + Ticket 0 骨架（同提交冻结）。

## 事实流水

- **spec 落点核对**：`docs/superpowers/specs/b380.md` 不在本工作树、也不在 `cards/B233.1-charter-7`（`git show` 报 `fatal: path ... does not exist`）。经协调者指示命中 `origin/cards/B380-charter-1`（`git ls-remote origin 'refs/heads/*380*'`），`git show origin/cards/B380-charter-1:docs/superpowers/specs/b380.md` 读到全文 108 行。spec 头部「上游状态：已批准（2026-09-23 四点裁定）」，一致。
- **基线关系**：`git merge-base --is-ancestor 0dfa9e74 e5e428a5` → exit 0（spec 基 `0dfa9e74` 是本工作树 HEAD 的祖先）；`e5e428a5` 是 `0dfa9e74` 的祖先 → exit 1。故 spec 的现状行号在本基线上有效（个别漂移已按当前文件复核）。
- **三个写入点复核**：`grep -rn EventTypeTicketAnswered --include=*.go internal/ cmd/ | grep -v _test.go` → 仅 3 处非测试：`internal/orchestration/facade.go:125`、`internal/orchestration/manager.go:2577`、`internal/approval/client.go:430`。与 spec §1 一致，无第四处。
- **C-1c 偏差**：spec 称 approval Client「无 hub 触点、经 Hooks 新增发布函数」；查现状 `internal/approval/client.go#Hooks` 已有 `Hub AnswerHub`（:62-77）、`AnswerHub` 含 `Publish`（:25-28）、生产装配 `internal/orchestration/approval_client.go:37 Hub: m.hub`、`escalate`（client.go:491）已用 `c.hooks.Hub.Publish`。→ 复用 `Hooks.Hub`，不新增字段。语义意图（nil 安全/旧装配不破坏）不变。
- **消费面复核**：`WaitDeliveryPolicy`（`internal/client/delivery.go:23`，`ticket_answered` :29 为 false）；`automationWakeEvent` 经同策略过滤（`internal/agentd/wakeconsumer.go`）；`OpenTickets` 消费方 `cmd/card_wait.go:357` + `internal/agentd/ledgerapi.go:198`（`OpenTicketCounts`）。
- **落地改动**（本提交冻结）：
  - `internal/orchestration/facade.go` `AnswerTicket`：append 成功补 `m.hub.Publish(evt)`；同步修订函数注释（原「只 AppendEvent 不 hub.Publish」）。
  - `internal/orchestration/manager.go` `approvePermission`：同上。
  - `internal/approval/client.go` `consult`：`c.hooks.Hub != nil` 时 `Publish`。
  - `internal/ledger/taskstate.go` `OpenTickets`：`case evTicketsVoided, "completed", "failed", "archived":` 清该任务全部未决 key。
  - `internal/proto/proto.go`：`EventTypeTicketAnswered` 注记改述为「会 Publish，但客户端不可交付」。
- **编译**：`go build ./...` → exit 0（本轮）。
- **红/绿**：`git stash push -- <5 个源文件>` 后跑新测试 → 三段全 FAIL（原文见 contract §4）；`git stash pop` 后 `go build` + 三段测试 → 全 ok。测试：`Taskstate_test` 两例 + `b380_publish_test.go` 两例 + `client_test.go` 两例。
- **回归**：`go test ./internal/ledger/... ./internal/orchestration/... ./internal/approval/... ./internal/client/... ./internal/ledgermirror/... ./internal/agentd/... ./cmd/... -count=1` → 唯一 FAIL 为 `internal/client` 的 `TestProductionHTTPClientCallersAreGatewayOnly`（报 `internal/agentd/drop.go`）。`git stash push -u` 清空本卡全部改动后单跑该测试 → **仍 FAIL**，确认基线既有红、非本卡引入。
- **图**：`codegraph --repo . check` → exit 0。本分支未引入新符号（只改函数体），故无 `codegraph/diffs/` 视图，合法。`codegraph validate` 报 2 条他分支视图问题（`cards-B272-charter`、`cards-B374-charter`），非本卡。
- **图覆盖债**：`codegraph sym` 未命中 `Manager.AnswerTicket` / `Store.OpenTickets` / `EventTypeTicketAnswered`（`codegraph resolve` 可解析）。写入 contract §7。
- **拍板记录**：命中一条（不做 watermark 逐 seq、改用投影层终态关单兜底），承 spec §3.3；「复用 Hooks.Hub」不入（未命中难逆转）。

## 提交事实（历史读数）

```
$ git add docs/superpowers/specs/b380-contract.md docs/superpowers/ledgers/2026-09-23-b380-contract-ledger.md \
    internal/approval/client.go internal/approval/client_test.go \
    internal/ledger/taskstate.go internal/ledger/taskstate_test.go \
    internal/orchestration/facade.go internal/orchestration/manager.go internal/orchestration/b380_publish_test.go \
    internal/proto/proto.go
$ git commit -q -m "contract(B380): 关单镜像契约冻结 + Ticket 0 骨架（三发布点 + OpenTickets 终态关单）"
$ git log --oneline -1
e2d39434 contract(B380): 关单镜像契约冻结 + Ticket 0 骨架（三发布点 + OpenTickets 终态关单）
```

随后按纪律 amend 一次把本条台账收进同批提交（hash 会变，属 git 事实）。
