# B356 实现计划

读者：当前分支本地实现（用户指定不另开工作分支、不远程派发）。级别 L2。spec：`docs/superpowers/specs/b356.md`。

## 基线复核（动手前）

- `cmd/card_wait.go` `runCardWait`：`start, err := st.MaxSeq()` 后直接 `st.Follow(..., start, ...)`，无快照。
- `internal/agentd/wakeconsumer.go` `consumeAutomationEventsOnce`：`pendingByCard[ev.CardID]`，随后 `wakeCoordinatorRound(ctx, card, evs)`。
- `internal/ledger/taskstate.go` `OpenTicketCounts` 已按镜像流回放未决工单，但只返回计数。
- `TestCardWaitSubtreeExitsWhenAllDone` 只锁 status_moved 成员 id，不锁工单。
- `TestAutomationEventMappingThroughConsumer` 只锁本卡有 coordinate 席位。

## 测试范围

- 触及包：`go test ./cmd/ -run 'TestCardWait' -count=1`
- 触及包：`go test ./internal/agentd/ -run 'TestAutomation' -count=1`
- 触及包：`go test ./internal/ledger/ -run 'TestOpenTicket' -count=1`
- task 收尾：`go build ./...`

全量测试归用户要求的真机任务，不在本 plan 单 task 内。

## Task 1：账本给出未决工单明细（CLI 快照的数据面）

文件：`internal/ledger/taskstate.go`、`internal/ledger/taskstate_test.go`

Consumes：现有 `OpenTicketCounts` 扫描。
Produces：`Store.OpenTickets() ([]OpenTicket, error)`；`OpenTicketCounts` 改为对它计数，行为不变。

`OpenTicket` 字段：`CardID, Target, TaskID, TicketID, TaskType string`；`Payload json.RawMessage`（创建那条镜像的原 payload）。

NeedsReasons：导出 `Store.NeedsReasons() (map[string]string, error)` = 现 `needsMap()`。

步骤：

1. 扩展 `TestOpenTicketCounts`：创建两单后断言 `OpenTickets` 含 `ticket_id=q1/q2` 与 payload；答 q1 后只剩 q2。
2. 跑红（`OpenTickets` undefined 或空）。
3. 最小实现：扫描循环改为记下创建时的 `OpenTicket`，答复/作废按现逻辑删。
4. `OpenTicketCounts` 对结果计数。
5. 日志：扫描失败已有 Error；成功路径不在热循环打 Info。导出函数写文档注释。

## Task 2：`card wait` 建连吐 `card_snapshot`

文件：`cmd/card_wait.go`、`cmd/card_wait_test.go`

Consumes：`Store.Subtree` / `OpenTickets` / `NeedsReasons` / `MaxSeq` / `Follow`。
Produces：stdout 第一行 JSON `type=card_snapshot`（产品字段见 spec）。

步骤：

1. 写 `TestCardWaitSubtreeSnapshotIncludesChildTicket`：父+子，子卡先镜像 `permission_request` `ticket_id=tk-child`，再 `card wait 父 --subtree --timeout 2s`。断言 stdout 第一行 `type=card_snapshot`，`actionable` 含 `tk-child` 与子卡 id；已答复工单不在列；无欠单时 `actionable` 为空数组仍有快照行。
2. 跑红。
3. `runCardWait` 在 Follow 前：`members()` → 过滤 `OpenTickets`/`NeedsReasons` → `enc.Encode(snapshot)`。`from_seq` 用即将 Follow 的 `start`。
4. 日志：Info「card wait 建连快照已输出」带 card/subtree/members/actionable/needs/from_seq；members 失败 Error。
5. 注释：快照为什么不回放事件（B253 同因：成员集动态；欠单用派生视图）。

## Task 3：空座工单冒泡到 coordinate 祖先

文件：`internal/agentd/wakeconsumer.go`、`internal/agentd/wakeconsumer_test.go`

Consumes：`s.ledger.GetCard`、`proto.SeatSourceCoordinate` / `SeatSourceBind`。
Produces：`consumeAutomationEventsOnce` 把空座事件归到祖先 coordinate 卡再 `wakeCoordinatorRound`。

`resolveWakeCard(cardID string) string`：

- 事件卡有 coordinate 席位 → 返回自己。
- 事件卡是 bind → 返回 ""（现状：bind 靠 CLI wait）。
- 事件卡空座 → 沿 `ParentID` 上走；遇到 bind 继续；遇到合法 coordinate 返回该祖先；否则 ""。
- 环检测 + 最多 32 步。

步骤：

1. 测试：
   - `TestAutomationWakeBubblesChildTicketToParentCoordinate`：父 coordinate、子空座、子卡 `permission_request` → Resume 一次且 briefing 含 permission_request；父卡 id 出现在 wake 路径（briefing 卡号=父）。
   - `TestAutomationWakeDoesNotBubbleWhenChildHasCoordinateSeat`：子也 coordinate → Resume 打在子，父 briefing 不出现子工单。
   - `TestAutomationWakeDoesNotWakeBindParent`：父 bind、子空座 → processed 可 >0（标记已见）但 Resume=0。
2. 跑红。
3. 在 `yes` 分支用 `resolveWakeCard(wake.Card)` 替换分组键，并 `wake.Card = target`；target=="" 则标记 seen（与今日空座消费后不再重试一致）。
4. 日志：冒泡 Info `from,to`；空链 Debug；GetCard 失败 Warn。注释写清「为什么 bind 不冒泡、为什么改分组键不改账本」。

## 缺陷族

- 生命周期：快照只读派生，进程重启再挂 wait 再吐一次（与 task `backlog_summary` 同形）。唤醒仍走现有 cursor/seen。
- 静默失败：快照行即使空数组也出现；members 失败直接返回，不进 Follow 装死。
- 跨平台：stdout JSON 无路径假设。
- 假红假绿：断言 `ticket_id` 与子卡 `card_id`，禁止只断言 `task_mirrored` 字样。反面：已答复工单不得出现。唤醒三条互为反面。
- 门禁：只读账本 + 现有席位，无新写路径。
- 序列化边界：`TestCardWaitSubtreeSnapshotIncludesChildTicket` 穿过 `json.Encoder` 真实 stdout 行，用解码后的结构断言字段缺失 vs 空数组。

## 真机（本卡用户指定，实现后由当前会话做）

当前分支构建后更新本机与 linux-01，再派真实任务：父卡 `card wait --subtree` 应在建连行看到子卡工单，coordinate 父卡应被子卡工单叫醒。
