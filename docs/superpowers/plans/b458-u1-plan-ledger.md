# B458 / U1 计划出稿台账（2026-10-23）

状态：提案，待协调者拍板；未进入实现或验收。

| 事实/动作 | 证据 | 结论 |
|---|---|---|
| 已批准范围 | `docs/superpowers/specs/b469-approval-batch-spec.md` r1.4 与 `docs/superpowers/plans/b469-plan.md` r1.4 | cookie/CLI 主令牌显式规则管理与 reply 同级；不加 nonce/secret/WebAuthn；五类限制与永不可覆盖集不改；UI 实施前须原型确认。 |
| 规则持久 | `internal/ledger/registry.go:RegistryPut`，`internal/ledger/events.go:appendEvent`，`internal/ledger/api/registry.go` | 当前 Put 自有事务，只写快照；不能把两次 API 调用说成规则+审计同事务。仓内无 `internal/ledgerregistry` 包。 |
| 复用 | `internal/store/store.go:FindReusableGrant`、`internal/approval/authority.go:FindReuse`、`internal/approval/client.go:tryReuse`、`internal/proto/proto.go:Ticket` | 现查同任务/指纹/allow/已送达；两条审批腿独立查，Ticket 无规则来源。新增来源须穿过持久与转换链。 |
| 确认入口 | `internal/agentd/server.go:auth`、`internal/agentd/identity.go:resolveConsoleIdentity`、`cmd/reply.go`、`cmd/root.go:newTargetClient` | Bearer 主令牌与 cookie 同在 agentd 鉴权；CLI 身份可取 `user:<console_user>`，主令牌无会话设备名可为空，不能声称独立人类在场。 |
| 原例 Gate/Authority 观测 | `/tmp/b458-gate-probe.oA0eXV` 中 `HEAD` archive + 独立 `b458_probe.go`；Tool=Bash、Cwd/Workdir=副本根、目标为副本 `README.md`；`go run ./b458_probe.go` 输出 `Gate.Judge` 与 `Authority.Judge` 均为 `action=consult, rule="", reason="黑名单未命中"`；Authority 只在临时 SQLite 建隔离 task | 只证明生产判据和权威该输入的出口。真实 executor AdapterEvent、权限票、完整限制仍未验。若完整证明无特殊限制，严格条件 + `covers=[]` 是待拍板解释；未知脚本/目标不能借空覆盖授权。未改产品、未造票。 |
| 精确 wire 候选 | 本稿新增 rule JSON v1、HTTP create/read/revoke 与状态码、`CommitApprovalRuleCAS` 单事务、`Ticket.GrantSource` 和旧票 `legacy` 回填候选 | 均未实施/批准；首个多端施工前协调者核定，禁止用两次现有 Put/Append 伪原子。 |
| 图定位 | checkout 无 `codegraph/` | 图覆盖债：本轮无法 `codegraph sym/who-calls`，改用 `rg` 和源码。 |

下次更新条件：协调者逐项裁决计划稿首岔口，并把状态与裁决同批回写。Luna 实施的红绿、PG、双机、原型确认均须后续另记；本台账不预报通过。

## 根协调者草稿入库

本次只将 Sol 提案入库供下一段核定，不表示三个岔口已拍板、S1已实施或可直接派工。根协调者提交命令 `git commit -m "docs(approval): record r1.4 repair validation and next-slice proposal"` 原始输出 `[codex/approval-batch-b469 5c39c94b9] ...`、`21 files changed, 4699 insertions(+), 3 deletions(-)`；随后只 amend 一次收入此记录。
