# B385 台账：最后活跃换接账本可证实读数

执行节点：implement（charter）。产出与命令原始输出逐行追加；amend 换 hash 属
git 事实，收口判据是工作树干净，不 chase 文件内 hash。

## 2026-09-23 开工

- 基线 HEAD：`1aeae017fd42d19d3fc1258e6b035f19ea2b66f4`（分支 `cards/B385-charter`）。
- codegraph：`sym memberStatus` / `sym sessionMembers` 命中 `internal/collab/sessions.go:260/:222`；`who-calls` 确认调用链仅 `summarizeSession → sessionMembers → memberStatus`，上游 `ListSessions`/`SessionDetail` 均已持有全量 `events`。
- 图覆盖：新符号 `deriveMemberLastActive` / `normalizeLegacyFace` 施工前不在图（新建，预期未命中）。
- 取证：`pointerActor = "system:pointer"`（service.go:64）；agentd 系统行 actor 字面量 `"agentd"`（wakeconsumer.go:475 等）；镜像行 actor `"mirror"`（ledger/mirror.go:69）。三者为缝 2 系统排除集。

## TDD 红绿（缝 1/2 + 接线）

1. 先写 `lastactive_test.go` 三测；曾误先落生产代码——已删 `lastactive.go` 重来。
2. 编译红（原文）：`undefined: deriveMemberLastActive` / `undefined: normalizeLegacyFace`。
3. 空壳落地 → 断言红：`user:sy 应取会话内最大时刻 … 实得 0001-01-01`；`cli 命中: normalize(...)="cli:sycm@…" want "user:sycm"`。
4. 实现缝 1/2 → 三测绿：`ok github.com/Xsxdot/handoff/internal/collab 0.004s`。
5. 接线前写 `TestSessionMemberLastActiveFromLedger` + 重写 `TestSessionMemberStatusHonest` → 断言红：`有活动成员应报 last_active+真实时刻: … LastActive:0001-01-01`；`坐下卡事件应给出真实最后活跃时刻: … LastActive:0001-01-01`。
6. 改 `sessionMembers(session,byCard,events)` + `memberStatus(identity,lastActive)`（签名按 plan「改写函数体」；参数增加以携带会话范围推导读数，返回形状 `(string,time.Time)` 不变）→ 目标测绿。

## 变异自验（均先 unique 命中、编译过、行为变红）

| 变异 | 编译 | 行为断言 |
|---|---|---|
| M1 `isSystemActor` 去掉 `agentd` | ok | 先存活（targets 已盖）；补脏成员 `agentd` 承重断言后红：`系统组件事件不得记入 agentd 成员读数` |
| M2 去掉房间范围（`return false`） | **FAIL**（room import unused）——整块弃，换 M2b | — |
| M2b `SameRoom` 取反 | ok | 红：`user:sy 应取会话内最大时刻 … 0001-01-01` |
| M3 normalize 恒归一 | ok | 红：`本地段不等不归一: … want "cli:other@host"` |
| M4 `After`→`Before` | ok | 红：`… 实得 2026-09-23 10:00:00`（取到更小） |
| M5 `memberStatus` 恒零值 | ok | 红：`坐下卡事件应给出真实最后活跃时刻` |

## 收口检查（本节点）

- `go build ./...` → 无输出（过）。
- `go vet ./internal/collab/...` → 无输出（过）。
- `go test ./internal/collab/... -count=1` → `ok … collab 6.4s` / `ok … room` / `client/cursor no test files`。
- `git diff internal/proto/` → 空（wire 零改动）。
- `gofmt -l internal/collab/` → 空。


## 提交事实

- 命令：`git add internal/collab/sessions.go internal/collab/sessions_test.go internal/collab/lastactive.go internal/collab/lastactive_test.go docs/ledgers/2026-09-23-b385-last-active-zero-render.md && git commit -m "fix(B385): …"`
- 提交后将 ledger 自身 amend 进同批（换 hash 属 git 事实，不 chase）。
