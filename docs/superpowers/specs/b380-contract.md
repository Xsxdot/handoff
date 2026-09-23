# B380 契约增量：关单镜像断流修复（ticket_answered 发布 + 终态关单）

- **上游 spec**：`docs/superpowers/specs/b380.md`，状态行「上游状态：**已批准**」（审批者 2026-09-23，四点裁定）——已核对，一致。
- **级别**：L2 单子系统（`internal/orchestration` + `internal/approval` 发布点 + `internal/ledger` 投影面）。
- **基线**：`cards/B233.1-charter-7`（本工作树 HEAD `e5e428a5`）。
- **本节点产出**：本文档 + Ticket 0 骨架（三个发布点接线 + `OpenTickets` 终态关单规则 + 锁死其语义的金样本测试），随本提交冻结。
- **图**：有图。本分支**未引入新符号**（只改既有函数体），故合法无 `codegraph/diffs/<分支>.json`；`codegraph check` 绿（见 §6）。

## 0. 根因链（承 spec §1，逐环带现状出处）

| # | 环节 | 现状出处 |
|---|---|---|
| 1 | `ticket_answered` 三个写入点只 `AppendEvent` 不 `hub.Publish` | `internal/orchestration/facade.go`（基线 :125）、`internal/orchestration/manager.go#Manager.approvePermission`（基线 :2577）、`internal/approval/client.go#Client.consult`（基线 :430） |
| 2 | 账本镜像经 `/ws/events` 只承载 Publish 的事件，历史补发仅在订阅建立时一次 | `internal/agentd/handlers.go`（:1451 起） |
| 3 | 镜像水位 = `MAX(source_seq)`，缺口以下的关单事件永久丢失 | `internal/ledger/mirror.go#Store.MirrorWatermark`（:104） |
| 4 | `OpenTickets()` 按镜像事件重放，开了关不掉 → 幽灵未决单 | `internal/ledger/taskstate.go`（:147） |

## 1. 契约条目（精确到可编译）

### C-1 发布 `ticket_answered` 进 hub 实时流（三个写入点）

`ticket_answered` 事件**会 Publish**，但**客户端不可交付**（两者正交，见 C-3）。发布收口在「`AppendEvent` 成功且本次真的追加」之后；发布失败/无事件不影响应答主流程。

#### C-1a 人工 reply 路径

- **签名（不变）**：`func (m *Manager) AnswerTicket(ctx context.Context, taskID, ticketID, answer string) (applied bool, err error)` —— `internal/orchestration/facade.go`（基线 :107）。
- **行为增量（可编译片段）**：

  ```go
  evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeTicketAnswered,
      TicketAnsweredPayload{TicketID: ticketID, Answer: answer})
  if aerr != nil {
      m.log.Warn("追加工单答复事件失败", "task", taskID, "ticket", ticketID, "cause", aerr)
  } else {
      m.hub.Publish(evt)
  }
  ```

- **hub 触点在位**：`Manager.hub *Hub`（`internal/orchestration/manager.go` 构造 `NewManager(st, hub, ...)` 注入），同包既有 `m.hub.Publish(evt)` 用法先例十余处。
- **消费方**：`internal/ledgermirror`（订阅 `/ws/events` 的实时流）。

#### C-1b Manager 中介审批者路径

- **签名（不变，未导出）**：`func (m *Manager) approvePermission(taskID, ticketID, permID, permission, fp, reason, source string)` —— `internal/orchestration/manager.go#Manager.approvePermission`（:2554）。
- **行为增量（可编译片段）**：

  ```go
  if evt, err := m.st.AppendEvent(taskID, proto.EventTypeTicketAnswered,
      TicketAnsweredPayload{TicketID: ticketID, Answer: "allow"}); err != nil {
      m.log.Warn("审批者批准：追加工单答复事件失败", "task", taskID, "ticket", ticketID, "cause", err)
  } else {
      m.hub.Publish(evt)
  }
  ```

- `source` 取 `"approver"`（实时裁决）或 `"reuse"`（命中既有人工批准复用），两条都经本函数，故一个发布点覆盖两路。

#### C-1c approval 包审批者批准路径（经既有 `Hooks.Hub`）

- **导出面不变**：`approval.Client.consult` 未导出；`approval.Hooks` 结构**不新增字段**。
- **行为增量（可编译片段）**（`internal/approval/client.go#Client.consult`，基线 :430）：

  ```go
  if evt, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypeTicketAnswered, ticketAnsweredPayload{
      TicketID: ticketID,
      Answer:   "allow",
  }); err != nil {
      c.logger().Warn("审批者批准：追加工单答复事件失败", "task", c.taskID, "ticket", ticketID, "cause", err)
  } else if c.hooks.Hub != nil {
      c.hooks.Hub.Publish(evt)
  }
  ```

- **事件缝事实**：`Hooks.Hub AnswerHub`（`internal/approval/client.go#Hooks` :62-77），`AnswerHub` 含 `Publish(proto.Event)`（:25-28）；生产装配 `Hub: m.hub`（`internal/orchestration/approval_client.go#Manager.bindApproval` :37）；同包 `escalate` 已用 `c.hooks.Hub.Publish(evt)` 发 `permission_request`（client.go:491）。
- **与 spec 的落地偏差（记录，非语义变更）**：spec §3.1.3/§5 设计「经 `Hooks` 新增一个可选发布函数」。落地查证现状代码后发现 `Hooks.Hub` 早就是 nil 安全的事件发布缝且生产已注入（`approval_client.go:37`），`escalate` 已用它发布。新增第二个发布触点会形成双发布面（同一语义两把钥匙），故**复用 `Hooks.Hub.Publish`**。spec 的语义意图（可选依赖、nil 时不 panic 只入库、旧装配零破坏）逐条满足。反转此决定（改回新增函数）不会有任何测试变红——此处选择复用是为了不制造第二发布面（属实现级取舍，见 §5）。

### C-2 投影层终态关单（存量自愈）

- **签名（不变）**：`func (s *Store) OpenTickets() ([]OpenTicket, error)` —— `internal/ledger/taskstate.go`（:147）。
- **行为增量（可编译片段）**：重放到某 `(card_id, source_target, source_task)` 的 `task_mirrored` 且 `payload.task_type ∈ {completed, failed, archived}` 时，删除该任务名下全部未决 key。与既有 `tickets_voided` 分支合并为同一删除循环：

  ```go
  case evTicketsVoided, "completed", "failed", "archived":
      for key := range open {
          if key.cardID == cardID && key.target == target && key.taskID == taskID {
              delete(open, key)
          }
      }
  ```

- **重放序**：查询 `ORDER BY seq ASC`（`OpenTickets` :148-149），语义为「终态之后该任务不再有未决单」，与任务侧不变式（终态即无挂起工单，`VoidTicketsWithAudit` 收口）一致。
- **临界语义（禁止合并）**：`completed` 在**订阅在飞**视角仍算「在飞」——`mirrorTaskTerminal` 只收 `archived/failed`（taskstate.go:22-24），因为 `completed` 对应 `waiting_review`、还会再来事件。**两处语义不同**：`mirrorTaskTerminal` 管「订阅该退不退出」，C-2 管「工单还能不能合法未决」。不许把 C-2 的集合抄进 `mirrorTaskTerminal`（会在 `waiting_review` 提前退订）。
- **消费方**：`cmd/card_wait.go#encodeCardWaitSnapshot`（:357，`st.OpenTickets()`）、`internal/agentd/ledgerapi.go` 卡详情计数（:198 → `OpenTicketCounts`，taskstate.go:213）。
- **存量自愈**：B369 等已完成任务的 `completed/archived` 镜像早已在卡流，升级后重放即关全部幽灵单，无需回填迁移。

### C-3 明示冻结（不改动面，防实现漂移）

- B233.1 词表 17 事件**不增不改**；`ticket_answered` 字面值与 payload schema 逐字不变：`TicketAnsweredPayload{TicketID string "ticket_id"; Answer string "answer"}`（`internal/orchestration/contracts.go#TicketAnsweredPayload` :115-118；approval 包内同形私有 `ticketAnsweredPayload`）。
- `WaitDeliveryPolicy` 保持不变——`ticket_answered` 仍返回 `false`（`internal/client/delivery.go#WaitDeliveryPolicy` :23-34）：发布它**不唤醒** `card wait` / `handoff wait` / `agentd` 唤醒消费（`automationWakeEvent` 经同一策略过滤）。
- `MirrorWatermark` 语义不变（`MAX(source_seq)`，`internal/ledger/mirror.go#Store.MirrorWatermark` :104）。
- 镜像协议、事件信封（`{"node","attempt","task_type","payload"}`，`internal/ledger/mirror.go` :62）不变。
- 不发布 `tickets_voided`（spec §3.3）。
- `proto.go` 事件注记随代码修订：`ticket_answered` 改述为「会 Publish，但客户端不可交付」（本次随骨架一并落地）。

## 2. 依赖库既成行为核对（承重，逐条带出处）

- `Hub.Publish(ev proto.Event)` **无返回值**，持 `h.mu` 扇出，慢订阅者走 `select-default` 丢弃并 Warn（`internal/orchestration/hub.go#Hub.Publish` :167-184）。→ 发布不阻塞应答主流程，与 spec §5「发布失败不影响主流程」一致；调用方无需处理错误。
- `Store.AppendEvent(taskID, typ, payload) (proto.Event, error)`（`internal/store/store.go#Store.AppendEvent` :778）返回的 `Event` 已带 `Seq/TaskID`，满足 `Hub.Publish` 的前置（hub.go 注释：「Seq/TaskID 需已由调用方 store 落库后赋值」）。
- `/ws/events` **先订阅 hub 实时流、再补发 store 历史**，补发只在订阅建立时一次（`internal/agentd/handlers.go` :1451 起）。→ 这是「审计类事件不进实时流即永久丢失」的机制出处，也是 C-1 的必要性来源。
- `AppendMirroredEvent` 按 `(source_target, source_task, source_seq)` 幂等（`internal/ledger/mirror.go` :34-45）。→ C-2 重放/重复镜像不会重复计数或误删。
- 发布点与 `AppendEvent` 的**顺序**被测试锁定：先落库、后发布；append 失败不发布（幂等重答 `applied=false` 不 append 也不发布）。

## 3. 对侧常量查执法（谁发出、谁消费）

- **`ticket_answered` 写入点**：全仓非测试 `grep EventTypeTicketAnswered` 仅命中 3 处，即 C-1a/b/c，**无第四处**。→ 三个发布点收全。
- **消费方**：
  - `WaitDeliveryPolicy`（delivery.go:23）→ `card wait` / `handoff wait` / `wakeconsumer.automationWakeEvent`（wakeconsumer.go，经同策略）过滤，**零唤醒**；
  - `OpenTickets`（taskstate.go:147）→ `card wait` 快照欠单 + 卡详情计数（`OpenTicketCounts`）。
- **`task_type` 字面值 `completed/failed/archived`**：由镜像信封写入（`internal/ledger/mirror.go` :62，值来自远端任务事件类型）；消费方为 `taskstate.go`（`mirrorTaskTerminal` :22、`OpenTickets` :147、`CardStepInFlight` :296）。**无销毁常量、无假注释常量**；`tickets_voided` 与 `ticket_answered` 均在 `WaitDeliveryPolicy` 假集合内（`delivery.go:29`），非漂移。

## 4. 可执行冻结（金样本测试；本轮 红→绿 都跑过）

红 = 只去掉本卡源改动（`taskstate.go`/`facade.go`/`manager.go`/`client.go`/`proto.go`），测试文件保留；绿 = 带本卡改动。

| 测试 | 缝 | 命令 | 红 | 绿 |
|---|---|---|---|---|
| `TestOpenTicketsTerminalMirrorClosesAll` | `Store.OpenTickets`（completed/failed/archived 各一子例） | `go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1` | FAIL | ok |
| `TestOpenTicketsTerminalBeforeAnswerDoesNotResurrect` | `Store.OpenTickets`（终态先于答复的 seq 颠倒） | 同上 | FAIL | ok |
| `TestB380AnswerTicketPublishes` | `Manager.AnswerTicket`（hub 订阅者收 `ticket_answered`；幂等重答不发布） | `go test ./internal/orchestration/ -run TestB380 -count=1` | FAIL | ok |
| `TestB380ApprovePermissionPublishes` | `Manager.approvePermission` | 同上 | FAIL | ok |
| `TestB380ConsultApprovalPublishesTicketAnswered` | `approval.Client` 审批者批准（真字 `ticket_id`+`answer=allow`） | `go test ./internal/approval/ -run TestB380 -count=1` | FAIL | ok |
| `TestB380ConsultApprovalNilHubNoPanic` | `Hooks.Hub == nil` 只入库不 panic；旧装配零破坏 | 同上 | —（现状即绿） | ok |

红证据原文（去掉源改动后）：

```
--- FAIL: TestOpenTicketsTerminalMirrorClosesAll (…)
--- FAIL: TestOpenTicketsTerminalBeforeAnswerDoesNotResurrect (…)
FAIL	github.com/Xsxdot/handoff/internal/ledger
--- FAIL: TestB380AnswerTicketPublishes (2.08s)
    b380_publish_test.go:50: 2s 内未在 hub 实时流收到 ticket_answered
--- FAIL: TestB380ApprovePermissionPublishes (2.10s)
    b380_publish_test.go:83: 2s 内未在 hub 实时流收到 ticket_answered
FAIL	github.com/Xsxdot/handoff/internal/orchestration
--- FAIL: TestB380ConsultApprovalPublishesTicketAnswered (0.04s)
    client_test.go:671: 审批者批准应恰好发布一条 ticket_answered，实得 0（全部 []）
FAIL	github.com/Xsxdot/handoff/internal/approval
```

绿证据原文（带源改动，本节落盘时重跑）：

```
ok  	github.com/Xsxdot/handoff/internal/ledger
ok  	github.com/Xsxdot/handoff/internal/orchestration
ok  	github.com/Xsxdot/handoff/internal/approval
```

跨过空壳的可观测行为（三个 Publish + 终态删单）均有「能变红」的测试锁住，无「已实现但零测试」。

## 5. 拍板记录（三重闸门）

**命中一条：不改镜像 watermark，改用投影层终态关单兜底。**

1. **难逆转**：反转该决定 = 给镜像加缺口表 + 逐 seq 连续性对账 + 回拉策略，动 `internal/ledgermirror`、`internal/agentd` 订阅面、`internal/ledger` 三处；
2. **无上下文会惊讶**：后人看到 `OpenTickets` 在 `completed` 就清工单、且与 `mirrorTaskTerminal`（只收 archived/failed）集合不一致，会想「统一掉」——统一会破订阅在飞或破关单；
3. **真取舍**：被否掉的像样方案有三个（spec §3.3）：watermark 逐 seq 对账（根治整族但量级数倍）、修订 `tickets_voided` 发布语义（无增量收益）、card wait 快照侧按任务终态过滤（只治快照不治计数、正确性抄三处）。

**显式的「不做」**：`tickets_voided` 不发布；不做存量 `card_events` 回填迁移脚本（C-2 已自愈）；水mark 对账落 `docs/roadmap.md` 后续卡。

（C-1c 的「复用 `Hooks.Hub` 而非新增发布函数」**不入**拍板记录：未命中「难逆转」，见 §1 C-1c。）

## 6. 收尾自检证据（本轮新鲜）

- **编译**：`go build ./...` → exit 0（本轮）。
- **测试**：见 §4 红/绿原文。
- **图**：`codegraph --repo . check` → exit 0（新增代码未越出 target.json 契约面，无新增跨域边，故 `target.json` 无需变更、无需冻结改动）。
- **已知既有红（与本卡无关，基线即红）**：`go test ./internal/client/ -run TestProductionHTTPClientCallersAreGatewayOnly` 在**去掉本卡全部改动后仍 FAIL**（报 `internal/agentd/drop.go`，B272 遗留）；`codegraph validate` 报 2 条**他分支**视图问题（`cards-B272-charter` / `cards-B374-charter`）。二者均非本卡引入，不修。

## 7. 图覆盖债

`codegraph sym` 未命中，且 `codegraph resolve --doc` 对 `file#Symbol` 锚判为 `vanished`，故本文档这两处**只用普通路径、不带 `#Symbol` 锚**（最终 `resolve --doc` exit 0）：

- `Manager.AnswerTicket`（本卡发布点之一）——图未建该方法节点；
- `Store.OpenTickets`（本卡投影主缝）——图未覆盖；
- `EventTypeTicketAnswered`（事件常量）——图不建模事件常量。

未改用 grep 顶替图查询：上述符号经 `codegraph sym` 判定未命中后，仅以 grep 复核写入点数量（§3），并在本节点冻结物中显式记债。

## 8. 移交 plan 附区（实现级决定，不占冻结条目、不参与逐条打勾）

- **文档同步**：`README.md:435` 与 `skills/handoff/SKILL.md:95` 把七类放一起说「不唤醒 / 只入库」；C-1 后 `ticket_answered` 会 Publish 但仍不可交付，**「只入库」措辞对该事件不再准确**。plan 出稿时核对同步（`proto.go` 注记已在骨架内修订）。
- **实现形态**：发布分支用 `if err != nil { Warn } else { Publish }`，不提前 return；`approval` 侧多一层 `c.hooks.Hub != nil` 守卫（旧装配）。无导出符号变化、无新字段。
- **台账落点**：`docs/superpowers/ledgers/2026-09-23-b380-contract-ledger.md`（与本节点产出同批提交）。
