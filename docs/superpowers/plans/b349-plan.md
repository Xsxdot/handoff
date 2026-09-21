# B349+B352 实现计划：消费端 source identity 闸与自动化水位

状态：可执行实现计划（2026-09-09）
卡：B349（B352 并入，不另开实现卡）
基线：`cards/B233.1-charter-7 @ f2129a25`；本工作分支起点为拍板回写 `1e283551`
输入：`docs/superpowers/specs/b349.md`、`docs/superpowers/specs/b349-contract.md`、`docs/superpowers/specs/b349-breakdown.md`
台账：`docs/superpowers/ledgers/2026-09-09-b349-plan-ledger.md`

## 1. 目标、冻结边界与执行顺序

本轮只补消费端 source identity 校验与本机自动化 cursor 持久化：

1. `task_mirrored` 在自动化 wakeconsumer 与 `card wait` 两个消费点都同时通过 B353 `WaitDeliveryPolicy` 和本卡身份闸，身份事实来自事件所属卡、envelope node 对应的最新合格 `EvDispatched` 快照。
2. source 三列继续是账本列的唯一权威；不把 `source_target/source_task/source_seq` 复制进 envelope JSON，不改 mirror 层、不改 ledger schema、不新增 ledger 派生 API。
3. `snapshot.Attempt` 是 attempt 比较键，并强制 `snapshot.TaskID == snapshot.Attempt`；`source_seq` 是源序号，只保留和去重，不参与当前派发身份匹配。
4. `snapshot.Target == event.SourceTarget` 时通过；两边同时为 `""` 也通过，一空一非空拒绝。
5. 自动化 cursor 只属于当前 agentd，写 `<DataDir>/automation-cursor.json`；`SetupAutomation` 读回，内存 cursor 先更新再 Save，至少一次语义，不复用 room/wait cursor，不写共享账本。

F1–F6 已由协调者冻结，本计划不重新作产品决策。T0 已完成，不重开；实现只按以下同一轮内部顺序进行：

```text
T0 Ticket 0（已完成，1e283551）
  → T1 wakeconsumer 身份闸
  → T2 card wait 同闸
  → T3 automation cursor 装配与持久化
  → T4 全接缝回归、图锚和交接门禁
```

任何实现若要新增事件类型、HTTP 路径、ledger 派生查询、共享 cursor、TS 类型/交互或第二套任务类型集合，先停在当前 task 并退回 contract；不得在本轮自行扩大范围。

## 2. 基线判据与已核对的库行为

### 2.1 基线已亲跑命令

以下命令在动手前已于当前 HEAD 运行并记录在台账：

```text
go test ./internal/ledger/api -run '^TestFacadeEventsFromAscPreservesSourceIdentity$' -count=1
→ ok github.com/Xsxdot/handoff/internal/ledger/api 0.136s

go test ./internal/agentd -run '^TestCardDetailProjectsMirroredSourceIdentity$' -count=1
→ ok github.com/Xsxdot/handoff/internal/agentd 0.209s

go test ./internal/proto -count=1
→ ok github.com/Xsxdot/handoff/internal/proto 0.008s

go test ./internal/agentd -run 'Test(B349|B2336StaleAttempt|B353Automation)' -count=1
→ ok github.com/Xsxdot/handoff/internal/agentd 1.597s

go test ./cmd -run 'Test(B349|B353)CardWait' -count=1
→ ok github.com/Xsxdot/handoff/cmd 15.909s

go test ./internal/agentd -run 'Test(B349|AutomationCursor)' -count=1
→ ok github.com/Xsxdot/handoff/internal/agentd 0.176s
```

这些结果只证明 T0 和现有 B233.6/B353 夹具可运行；当前基线尚未实现 source identity 闸或 DataDir cursor，不得把它们写成已生效。

完整包基线 `go test ./internal/proto ./internal/ledger/api ./internal/agentd ./cmd -count=1` 与全量 `go test ./... -count=1` 的输出均包含既有 gate：

```text
--- FAIL: TestRepoContractGate (0.04s)
    graph_gate_test.go:38: 契约违规 [dead-contract] 契约 d_execution→d_orchestration 声明的方向没有活跃 call、implements 或组装点豁免边（期望在该方向看到至少一条跨子系统边）
    graph_gate_test.go:38: 契约违规 [dead-interface] 契约 d_execution→d_orchestration 声明的接口 "ApprovalClient" 在 d_execution 中不存在（无同名非 deleted 节点；期望在 d_execution 找到）
FAIL
FAIL github.com/Xsxdot/handoff/cmd 33.457s
```

实现阶段要重跑同一命令；若仍只有上述两项，原文记录为基线遗留，不为通过全量测试而修改本卡范围。若出现新增失败，按新增文件/测试上下文逐条处理。

### 2.2 已查证、实现时必须依赖的事实

- `internal/ledger/events.go:60-103` 的 `Store.EventsFromAsc` 按 `seq ASC`、`seq > fromSeq` 排他读取，每页上限由调用方传入；身份扫描必须用 500 分页直到返回不足一页，不能把首页截断当成没有当前快照。
- `internal/collab/cursor/cursor.go:105-124` 的同类文件介质使用 `filepath.Dir`、目录 `0700`、`.tmp` 文件 `0600`、同目录 `os.Rename`；本卡采用同一介质形态，但 owner 和文件名独立。
- `/usr/local/go/src/encoding/json/encode.go:107-110,352-362` 规定 `omitempty` 省略空字符串和整数 `0`；`/usr/local/go/src/encoding/json/encode.go:1176-1182` 按 struct tag 编码字段。source 三列缺席、显式空串、显式零值必须在 wire/HTTP/stdout/cursor 测试中按边界区分。
- `/usr/local/go/src/os/file.go:928-946` 显示 `os.WriteFile` 可能先截断并在多次系统调用中途失败；因此主 cursor 文件不能直接写，必须先写 `.tmp`，成功后同目录 rename。`/usr/local/go/src/os/file.go:433-438` 的 rename 行为受目标平台限制，验收不宣称跨平台绝对原子。

## 3. 文件范围与跨 task Interfaces

### 3.1 有界文件集

生产文件只允许是：

- `internal/agentd/wakeconsumer.go`
- `cmd/card_wait.go`
- `internal/agentd/automation_cursor.go`（新文件）
- `internal/agentd/server.go`

测试文件只允许是：

- `internal/agentd/wakeconsumer_test.go`
- `cmd/card_wait_test.go`

计划/台账文件是本文件与 `docs/superpowers/ledgers/2026-09-09-b349-plan-ledger.md`。T0 的 `internal/proto/ledger.go`、`internal/ledger/api/api.go`、`internal/agentd/ledgerapi.go` 及其已完成投影测试不重写。不得以“同目录”放宽生产集合；出现集合外生产改动，退回 breakdown 重新核边界。

### 3.2 Consumes / Produces 精确签名

| 入口 | Consumes | Produces / 约束 |
| --- | --- | --- |
| `internal/ledger/api/api.go#Facade.EventsFromAsc` | `func (f *Facade) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error)` | 已完成 T0 的三列直通；T1 只读该 Facade，不改签名。 |
| `internal/ledger/events.go#Store.EventsFromAsc` | `func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error)` | T1/T2 读取原始事件，使用 `fromSeq` 排他、`seq ASC` 分页。 |
| `internal/ledger/events.go#DispatchSnapshot` | 现有快照字段 `Target string`、`TaskID string`、`Node string`、`Attempt string` | T1/T2 只接受 `Node != ""`、`Attempt != ""`、`TaskID == Attempt` 的最新同 node 快照。 |
| `internal/client/delivery.go#WaitDeliveryPolicy` | `func WaitDeliveryPolicy(t proto.EventType) bool` | 唯一任务类型策略；身份闸通过后才调用，禁止复制集合。图未覆盖，源码锚已记录。 |
| `internal/agentd/wakeconsumer.go:54-92`（图覆盖债，现有私有函数 `Server.currentWorkflowAttempt`） | `func (s *Server) currentWorkflowAttempt(cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error)` | T1 输出事件所属卡/节点的最新合格快照；无匹配为 `false,nil`。 |
| `internal/agentd/wakeconsumer.go:97-130`（图覆盖债，现有私有函数 `Server.acceptsCurrentWorkflowAttempt`） | `func (s *Server) acceptsCurrentWorkflowAttempt(ev proto.LedgerEvent) (bool, error)` | T1 输出身份闸结果；合法不匹配为 `false,nil`，malformed/读账错误带 card/seq/type 上下文返回 error。 |
| `internal/agentd/wakeconsumer.go#automationWakeEvent` | `func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error)` | 只负责既有事件映射和 `WaitDeliveryPolicy`；不承载 source identity 判断。 |
| `internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce` | `func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error)` | 从 `autoLedger` 读 `proto.LedgerEvent`，身份通过后才进入 `automationWakeEvent`；同卡合并、attach 暂缓和现有 `keystone.Service.Decide(ev WakeEvent) Decision` 语义保持。T3 接管 cursor 落盘。 |
| `cmd/card_wait.go`（计划新增私有函数 `cardWaitCurrentWorkflowAttempt`） | `func cardWaitCurrentWorkflowAttempt(st *ledger.Store, cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error)` | T2 在 cmd 内独立扫描同一事件所属卡的最新合格快照；不新增 ledger API。 |
| `cmd/card_wait.go#cardWaitEventActionable` | `func cardWaitEventActionable(st *ledger.Store, ev ledger.Event) (bool, error)` | `task_mirrored` 先过身份闸再问策略；卡原生 needs/decision/真人房间事件仍不看 source 三列。 |
| `cmd/card_wait.go#runCardWait` | `func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error` | 继续使用 `Store.Follow(ctx, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error`；把同一个 `st` 传入分类器，stdout 仍编码原始 `ledger.Event`。 |
| `internal/agentd/automation_cursor.go`（计划新增私有函数 `newAutomationCursorStore`） | `func newAutomationCursorStore(path string) *automationCursorStore` | T3 产生仅供本机 agentd 使用的文件介质。 |
| `internal/agentd/automation_cursor.go#(*automationCursorStore).Load` | `func (s *automationCursorStore) Load() (int64, error)` | 缺文件返回 `0,nil`；读/JSON 错误原样返回，由 SetupAutomation 记日志并以 0 装配。 |
| `internal/agentd/automation_cursor.go#(*automationCursorStore).Save` | `func (s *automationCursorStore) Save(seq int64) error` | 写 `<DataDir>/automation-cursor.json.tmp` 后同目录 rename 主文件，JSON 只有 `seq`，目录/文件权限为 `0700/0600`。 |
| `internal/agentd/automation_cursor.go`（计划新增私有方法 `Server.advanceAutomationCursor`） | `func (s *Server) advanceAutomationCursor(seq int64) error` | 在 `automationMu` 下先推进内存，再 Save；Save 失败返回 error，内存不回滚。 |
| `internal/agentd/server.go#Server.SetupAutomation` | `func (s *Server) SetupAutomation(st *ledger.Store)` | 创建 cursor store、Load 水位并装配 `autoLedger`；Load 错误记可行动日志后以 0 继续。 |
| `internal/agentd/scheddrain.go#Server.StartAutomation` | `func (s *Server) StartAutomation(ctx context.Context)` | 本卡不改文件范围外的启动逻辑；不得在此读 cursor。 |

## 4. T0：Ticket 0 前置（已完成，不重开）

起点 `1e283551` 已包含以下行为和测试，后续 task 只消费其产物：

```go
type LedgerEvent struct {
	Seq          int64           `json:"seq"`
	CardID       string          `json:"card_id"`
	Type         string          `json:"type"`
	Actor        string          `json:"actor"`
	Payload      json.RawMessage `json:"payload"`
	SourceTarget string          `json:"source_target,omitempty"`
	SourceTask   string          `json:"source_task,omitempty"`
	SourceSeq    int64           `json:"source_seq,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}
```

`eventWire` 与 `ledgerEventWire` 已逐字段直通三列；`TestFacadeEventsFromAscPreservesSourceIdentity` 穿过 Facade；`TestCardDetailProjectsMirroredSourceIdentity` 穿过真实 `GET /api/cards/{id}`。本轮不得回退 source 投影、把 source 塞进 envelope，或修改 Web TypeScript 类型。

## 5. T1：wakeconsumer 当前派发身份闸

### 5.1 目标文件与边界

文件：`internal/agentd/wakeconsumer.go`、`internal/agentd/wakeconsumer_test.go`。
调用方接缝：`Server.consumeAutomationEventsOnce`，测试入口必须调用它；只测 `acceptsCurrentWorkflowAttempt` 返回值的 helper 测试不能替代接缝测试。
不改：`internal/ledger` schema、`AppendMirroredEvent`/`mirrorSkip`、`client.WaitDeliveryPolicy`、Keystone 唤醒规则。

### 5.2 红绿实现步骤

1. **基线判据（2–5 分钟）**：先运行 `go test ./internal/agentd -run 'Test(B349|B2336StaleAttempt|B353Automation)' -count=1`；基线原始结果为 `ok`，但当前 `TestB2336StaleAttemptDoesNotWake` 只锁 envelope attempt，不覆盖 source task/target，故新增测试必须另行变红。
2. **先写失败的调用方测试（2–5 分钟）**：在 `internal/agentd/wakeconsumer_test.go` 复用既有 `newNoPTYAutomationEnv`、`createCoordCard`、`prebindConsumerSession`、`appendWorkflowDispatchForConsumer`、`appendWorkflowMirroredForConsumer`；为双空 target 增加同文件测试辅助，只改变夹具输入，不增加生产入口。测试逐条断言如下：

   - 当前 `TaskID == Attempt == "attempt-current"`、快照 `Target == ""`、镜像 `SourceTask == "attempt-current"`、`SourceTarget == ""`、可动作 `question`，envelope node/attempt 同值，`consumeAutomationEventsOnce(context.Background())` 返回无 error、`processed == 1`，runner 只 Resume 一次。
   - 旧 attempt、`SourceTask` 错、`SourceTarget` 错、一空一非空、没有合格 `EvDispatched`、envelope node 缺失/null/空串、envelope attempt 缺失/null/空串分别调用同一 `consumeAutomationEventsOnce`；每例断言 `processed == 0`、runner 无新 Resume、账本事件仍可由 `EventsFromAsc` 读到，且后续追加合法事件仍可消费。
   - 同 node 追加两条 `EvDispatched`，后一条最大 seq 且 `TaskID != Attempt`：后一条不可作为当前身份；再追加一条 `TaskID == Attempt` 的更晚快照，断言只按该合格快照匹配。
   - 追加超过 500 条事件后再写合法派发快照和镜像，断言分页扫描到尾并 Wake；`SourceSeq` 与源序号不一致但其余身份一致，仍 Wake。
   - `task_type` 缺失或 envelope JSON 损坏，断言返回 error 且 error 同时包含 card、seq、`task_mirrored`；缺 `source_task` 是合法拒绝，返回 `false,nil`，不能终止后续事件消费。
   - `permission_auto_allow`、`permission_reuse` 经过身份匹配后仍不 Wake；`delivery_failed`、`question` 等可动作类型经过身份匹配后 Wake；证明 `WaitDeliveryPolicy` 仍是第二道闸。

   这些测试全部从 `consumeAutomationEventsOnce` 进入，属于接缝级断言；直接构造 helper 仅作为同一测试的诊断数据，不计作产品覆盖。

3. **运行红测试（2–5 分钟）**：运行 `go test ./internal/agentd -run '^TestB349AutomationSourceIdentity$' -count=1`；预期在当前未实现基线中失败，失败应落在 source task/target/快照条件之一，不能把“无测试可运行”当红或绿。
4. **改快照扫描（2–5 分钟）**：把 `currentWorkflowAttempt` 改成精确签名，并按以下完整控制流实现，保留每页读错误和 payload 解码错误的上下文：

   ```go
   func (s *Server) currentWorkflowAttempt(
       cardID, node string,
   ) (snapshot ledger.DispatchSnapshot, found bool, err error) {
       if s.autoLedger == nil {
           readErr := fmt.Errorf("卡 %s 节点 %s 读取当前 attempt：账本未装配", cardID, node)
           s.log.Error("当前 workflow attempt 读取失败：账本未装配", "card", cardID, "node", node, "cause", readErr)
           return ledger.DispatchSnapshot{}, false, readErr
       }
       const pageSize = 500
       from := int64(0)
       var latest ledger.DispatchSnapshot
       for {
           events, readErr := s.autoLedger.EventsFromAsc([]string{cardID}, from, pageSize)
           if readErr != nil {
               s.log.Error("读取当前 workflow attempt 失败", "card", cardID, "node", node,
                   "from_seq", from, "cause", readErr)
               return ledger.DispatchSnapshot{}, false,
                   fmt.Errorf("读取卡 %s 当前 attempt: %w", cardID, readErr)
           }
           for _, event := range events {
               if event.Seq > from {
                   from = event.Seq
               }
               if event.Type != ledger.EvDispatched {
                   continue
               }
               var candidate ledger.DispatchSnapshot
               if decodeErr := json.Unmarshal(event.Payload, &candidate); decodeErr != nil {
                   s.log.Error("当前 workflow attempt 的派发快照解码失败", "card", cardID,
                       "seq", event.Seq, "type", event.Type, "node", node, "cause", decodeErr)
                   return ledger.DispatchSnapshot{}, false,
                       fmt.Errorf("卡 %s 派发快照 seq=%d type=%s 解码: %w", cardID, event.Seq, event.Type, decodeErr)
               }
               if candidate.Node != node || candidate.Node == "" || candidate.Attempt == "" ||
                   candidate.TaskID != candidate.Attempt {
                   continue
               }
               snapshot = candidate
               found = true
           }
           if len(events) < pageSize {
               return snapshot, found, nil
           }
       }
   }
   ```

   上述代码块是实现控制流契约，不是可跳过的伪代码：循环必须真正继续到不足 500 条，`TaskID != Attempt` 必须过滤，返回值必须是完整 `ledger.DispatchSnapshot`。实现文件已有 `encoding/json`、`fmt`、`ledger` 和 logger 依赖；保持现有 import，不另造解析器。

5. **改身份闸并接回消费缝（2–5 分钟）**：保留 `decodeMirroredTaskEnvelope` 的 `*string` 缺失/null/空串区分；`acceptsCurrentWorkflowAttempt` 的完整决策骨架如下，错误包装沿现有格式保留：

   ```go
   func (s *Server) acceptsCurrentWorkflowAttempt(ev proto.LedgerEvent) (bool, error) {
       envelope, err := decodeMirroredTaskEnvelope(ev)
       if err != nil {
           s.log.Error("task_mirrored envelope 无法进入当前 attempt 闸", "card", ev.CardID,
               "seq", ev.Seq, "type", ev.Type, "cause", err)
           return false, fmt.Errorf("卡 %s task_mirrored seq=%d type=%s: %w", ev.CardID, ev.Seq, ev.Type, err)
       }
       if envelope.Node == nil || *envelope.Node == "" || envelope.Attempt == nil || *envelope.Attempt == "" {
           s.log.Info("task_mirrored 因缺失或空 workflow 身份跳过", "card", ev.CardID,
               "seq", ev.Seq, "type", ev.Type, "node_present", envelope.Node != nil,
               "attempt_present", envelope.Attempt != nil, "task_type", envelope.TaskType)
           return false, nil
       }
       current, found, err := s.currentWorkflowAttempt(ev.CardID, *envelope.Node)
       if err != nil {
           return false, err
       }
       if !found || current.TaskID != current.Attempt || current.Attempt != *envelope.Attempt ||
           ev.SourceTask != current.Attempt || ev.SourceTarget != current.Target {
           s.log.Info("task_mirrored 因 source identity 不匹配跳过", "card", ev.CardID,
               "seq", ev.Seq, "type", ev.Type, "node", *envelope.Node,
               "attempt", *envelope.Attempt, "current_attempt", current.Attempt,
               "source_task", ev.SourceTask, "source_target", ev.SourceTarget,
               "current_target", current.Target, "source_seq", ev.SourceSeq)
           return false, nil
       }
       s.log.Debug("task_mirrored 通过 source identity 闸", "card", ev.CardID,
           "seq", ev.Seq, "type", ev.Type, "node", *envelope.Node,
           "attempt", *envelope.Attempt, "source_task", ev.SourceTask,
           "source_target", ev.SourceTarget, "source_seq", ev.SourceSeq)
       return true, nil
   }
   ```

   直接比较字符串以保留双空通过、一空一非空拒绝；完全忽略 `ev.SourceSeq`。在 `consumeAutomationEventsOnce` 现有 `automationWakeEvent` 调用前调用身份闸；false 分支写入 `automationSeen` 并继续，error 分支原样返回。
6. **日志和注释（2–5 分钟）**：使用现有 `s.log` 结构化 logger，入口/读页/成功/拒绝/错误均带 `card`、`seq`（扫描时带 `dispatch_seq`）、`type`、`node`、envelope `attempt`、当前 `snapshot.Attempt`、`source_task`、`source_target`、`snapshot.Target`、`source_seq` 和 `reason`；malformed/读账错误用 Error，合法不匹配用 Info/Debug。更新 `currentWorkflowAttempt`、`acceptsCurrentWorkflowAttempt` 和消费分支注释，解释 source 列不进 envelope、`source_seq` 不参与身份以及旧快照只作审计的原因；禁止 `print`。
7. **运行绿测试（2–5 分钟）**：运行 `go test ./internal/agentd -run 'Test(B349|B2336StaleAttempt|B353Automation)' -count=1`，预期命中新增/迁移调用方测试并返回 `ok`；若显示 `no tests to run` 则判失败。此 task 测试范围只限 `./internal/agentd`。

### 5.3 T1 可判定验收

- current snapshot 的 `Node/Attempt/TaskID` 条件、最大 seq 和 500 分页全部由 `consumeAutomationEventsOnce` 调用方断言。
- 当前/旧/错 source/双空/一空一非空/无快照/缺 source task/source_seq 不参与均各有正反断言。
- `WaitDeliveryPolicy` 只在身份通过后被到达；审计类型不 Wake，可动作类型 Wake。
- malformed 返回带 card/seq/type 的 error；合法拒绝为 `false,nil` 且后续事件可消费。

## 6. T2：card wait 同一道身份闸

### 6.1 目标文件与边界

文件：`cmd/card_wait.go`、`cmd/card_wait_test.go`。
调用方接缝：`runCardWait` → 真实 `Store.Follow` → `cardWaitEventActionable` → `json.Encoder`；只测分类 helper 不能替代 runCardWait 测试。
不改：wait 命令/flag、HTTP endpoint、ledger 派生 API、`WaitDeliveryPolicy` 集合。

### 6.2 红绿实现步骤

1. **基线判据（2–5 分钟）**：先运行 `go test ./cmd -run 'Test(B349|B353)CardWait' -count=1`；当前结果为 `ok`，但 B353 的 `task_mirrored` 夹具仅写镜像事件，没有匹配 `RecordDispatch`，这些夹具在本卡必须先补当前快照，不能继续伪造“当前 attempt”。
2. **先写失败的真实 wait 测试（2–5 分钟）**：在 `cmd/card_wait_test.go` 复用 `createCardWaitFixture`、`runLedgerCLI` 和临时 ledger；每个可动作 `task_mirrored` 先写如下精确派发快照，再写镜像事件：

   ```go
   ledger.DispatchSnapshot{
       Target: target, TaskID: attempt, Node: node, Attempt: attempt,
       Branch: "cards/" + cardID + "-" + attempt,
       Purpose: ledger.PurposeReview, Actor: "test",
   }
   ```

   测试从 `runCardWait` 进入并逐条断言：

   - 事件所属卡为子卡、`--subtree` 根卡为父卡时，子卡自己的匹配快照才输出；父卡快照不能替子卡放行，也不能把子卡事件误拒。
   - 当前 attempt + `delivery_failed/question` 输出一行原始 `ledger.Event` JSON；旧 attempt、错 source task、错 source target、无当前派发快照、plain/缺 node/attempt/null/空身份镜像均不输出且保留账本。
   - 当前快照与 source target 同为空时输出；一空一非空不输出；source_seq 与源序号不一致但其它身份一致时仍输出。
   - `permission_auto_allow`、`permission_reuse` 在身份通过后仍不输出；`delivery_failed` 等可动作事件输出；needs/decision/真人 room message 没有 source 列也继续输出。
   - 损坏 payload 返回包含 card/seq/type 的错误；不是静默过滤。每行 stdout 经 `json.Unmarshal` 读回 `ledger.Event`，断言 `SourceTarget`、`SourceTask`、`SourceSeq` 的缺席/空/非零边界与原始账本一致。

   以上均从 `runCardWait` 穿过 `Store.Follow` 和 `json.Encoder`，测试入口覆盖声明缝；不另写只喂 helper 的替代测试。

3. **运行红测试（2–5 分钟）**：运行 `go test ./cmd -run '^TestB349CardWaitSourceIdentity$' -count=1`；预期当前基线在旧 attempt、无快照或错 target 场景中错误输出，必须真实失败后才实现。
4. **新增事件所属卡扫描（2–5 分钟）**：在 `cmd/card_wait.go` 增加精确函数，并按 `Store.EventsFromAsc([]string{cardID}, from, 500)` 分页到尾，只处理 `EvDispatched`、同 node 非空、Attempt 非空、TaskID 等于 Attempt 的最新 seq 快照；payload 解码/读账错误返回包含 card/seq/type 的 error。此函数只在 cmd 内使用，不下沉到账本。函数的控制流与 T1 相同，但输入是 `*ledger.Store`、错误日志使用 cmd 的 `slog`，不得调用 agentd 私有方法。

   ```go
   func cardWaitCurrentWorkflowAttempt(
       st *ledger.Store, cardID, node string,
   ) (snapshot ledger.DispatchSnapshot, found bool, err error) {
       const pageSize = 500
       from := int64(0)
       for {
           events, readErr := st.EventsFromAsc([]string{cardID}, from, pageSize)
           if readErr != nil {
               slog.Error("card wait 读取当前 workflow attempt 失败", "card", cardID,
                   "node", node, "from_seq", from, "cause", readErr)
               return ledger.DispatchSnapshot{}, false,
                   fmt.Errorf("card %s 当前派发快照读取: %w", cardID, readErr)
           }
           for _, event := range events {
               if event.Seq > from {
                   from = event.Seq
               }
               if event.Type != ledger.EvDispatched {
                   continue
               }
               var candidate ledger.DispatchSnapshot
               if decodeErr := json.Unmarshal(event.Payload, &candidate); decodeErr != nil {
                   slog.Error("card wait 派发快照解码失败", "card", cardID,
                       "seq", event.Seq, "type", event.Type, "node", node, "cause", decodeErr)
                   return ledger.DispatchSnapshot{}, false,
                       fmt.Errorf("card %s 派发快照 seq=%d type=%s 解码: %w", cardID, event.Seq, event.Type, decodeErr)
               }
               if candidate.Node == node && candidate.Node != "" && candidate.Attempt != "" &&
                   candidate.TaskID == candidate.Attempt {
                   snapshot = candidate
                   found = true
               }
           }
           if len(events) < pageSize {
               return snapshot, found, nil
           }
       }
   }
   ```

5. **改分类器和主缝（2–5 分钟）**：把分类器改为 `func cardWaitEventActionable(st *ledger.Store, ev ledger.Event) (bool, error)`；卡原生四类和真人 room 分支保持原行为。`EvTaskMirrored` 分支按以下完整顺序执行，错误带 card/seq/type 返回：

   ```go
   case ledger.EvTaskMirrored:
       var envelope struct {
           Node     *string         `json:"node"`
           Attempt  *string         `json:"attempt"`
           TaskType string          `json:"task_type"`
           Payload  json.RawMessage `json:"payload"`
       }
       if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
           return false, fmt.Errorf("card %s task_mirrored seq=%d type=%s 解包: %w", ev.CardID, ev.Seq, ev.Type, err)
       }
       if envelope.TaskType == "" || len(envelope.Payload) == 0 {
           return false, fmt.Errorf("card %s task_mirrored seq=%d type=%s 缺 task_type/payload", ev.CardID, ev.Seq, ev.Type)
       }
       if envelope.Node == nil || *envelope.Node == "" || envelope.Attempt == nil || *envelope.Attempt == "" {
           slog.Info("card wait task_mirrored 因缺失或空 workflow 身份跳过", "card", ev.CardID,
               "seq", ev.Seq, "type", ev.Type, "task_type", envelope.TaskType)
           return false, nil
       }
       snapshot, found, err := cardWaitCurrentWorkflowAttempt(st, ev.CardID, *envelope.Node)
       if err != nil {
           return false, err
       }
       if !found || snapshot.TaskID != snapshot.Attempt || snapshot.Attempt != *envelope.Attempt ||
           ev.SourceTask != snapshot.Attempt || ev.SourceTarget != snapshot.Target {
           slog.Info("card wait task_mirrored 因 source identity 不匹配跳过", "card", ev.CardID,
               "seq", ev.Seq, "type", ev.Type, "node", *envelope.Node,
               "attempt", *envelope.Attempt, "current_attempt", snapshot.Attempt,
               "source_task", ev.SourceTask, "source_target", ev.SourceTarget,
               "current_target", snapshot.Target, "source_seq", ev.SourceSeq)
           return false, nil
       }
       return client.WaitDeliveryPolicy(proto.EventType(envelope.TaskType)), nil
   ```

   在 `runCardWait` 的 Follow 回调传入已经打开的 `st`；`subtree` 只影响 members 集合，不改 `e.CardID`。
6. **日志和注释（2–5 分钟）**：继续使用 `slog`/现有入口 logger；在快照读开始、每页错误、无快照、身份拒绝、策略拒绝、成功 Encode、Encode 错误处记录 `wait_card`、事件 `card`、`seq`、`type`、node、attempt、当前 Attempt、source task/target/seq、快照 target 和 `reason`。补充注释解释事件所属卡与 wait 根卡的区别、为什么 source identity 通过后才调用策略；禁止 `print`。
7. **运行绿测试（2–5 分钟）**：运行 `go test ./cmd -run 'Test(B349|B353)CardWait' -count=1`；必须返回 `ok` 且命中新增/迁移测试，不能以 `no tests to run` 通过。此 task 测试范围只限 `./cmd`。

### 6.3 T2 可判定验收

- 每支可动作镜像夹具都有匹配 `RecordDispatch`；旧的“只有 AppendMirroredEvent”夹具不再代表当前身份。
- `runCardWait` 真实输出路径覆盖事件所属卡、subtree、双空/一空 target、旧/错/无快照、source_seq 忽略和策略白名单。
- 卡原生事件不因缺 source 三列过滤；错误 payload 保留上下文；stdout 序列化不丢 source 列。

## 7. T3：本机自动化 cursor 装配与至少一次水位

### 7.1 目标文件与精确私有形状

文件：新建 `internal/agentd/automation_cursor.go`；修改 `internal/agentd/server.go`、`internal/agentd/wakeconsumer.go`；测试只改 `internal/agentd/wakeconsumer_test.go`。
不改：`internal/collab/cursor`、ledger schema、共享 ledger、`$HOME/.handoff/cursors`、协调者 wait cursor、mirror watermark、CLI/HTTP surface。

新文件头必须写“职责：保存本机自动化消费 seq；边界：不保存 automationSeen、不写共享账本、不承担 room/wait cursor”的注释。文件中的私有形状固定为：

```go
type automationCursorDisk struct {
	Seq int64 `json:"seq"`
}

type automationCursorStore struct {
	path string
}

func newAutomationCursorStore(path string) *automationCursorStore
func (s *automationCursorStore) Load() (int64, error)
func (s *automationCursorStore) Save(seq int64) error
func (s *Server) advanceAutomationCursor(seq int64) error
```

`Server` 增加一个私有 `automationCursorStore *automationCursorStore` 引用；现有 `automationCursor int64` 与 `automationSeen map[int64]struct{}` 保持其 owner 和锁不变。

### 7.2 实现步骤

1. **基线判据（2–5 分钟）**：先运行 `go test ./internal/agentd -run 'Test(B349|AutomationCursor)' -count=1`；当前结果为 `ok`，但没有 DataDir 读回/Save 时序断言。T3 新测试必须从 `consumeAutomationEventsOnce` 覆盖生产缝，不能只读 `s.automationCursor` 内存字段。
2. **先写失败的重启/文件边界测试（2–5 分钟）**：在 `wakeconsumer_test.go` 复用 `newNoPTYLedgerEnv`、`newNoPTYAutomationEnv`、`createCoordCard`、`prebindConsumerSession` 和现有 `TestAutomationAttachDefersAndThenWakes` 的 runner 观察方式。新增调用方测试逐条断言：

   - 临时 DataDir 无文件时，`NewServer` + `SetupAutomation` 后 cursor 为 0；消费一轮后真实 `<DataDir>/automation-cursor.json` 存在且 JSON 只有 `seq`。
   - 同一 ledger/DataDir 创建新 Server，调用 `SetupAutomation` 后直接 `consumeAutomationEventsOnce(context.Background())`；已保存 seq 之前不再 Wake，追加更大 seq 的可动作事件后可以 Wake。
   - 读取文件字节解码为 `map[string]json.RawMessage`，唯一键是 `seq`；`seq:0` 的存在文件与缺文件分开断言；目录权限为 `0700`、主文件权限为 `0600`；`room-cursors.json` 未改变，测试不访问 `$HOME/.handoff/cursors`。
   - 将 `automation-cursor.json` 设为目录，使同目录 rename 失败，调用同一消费缝；断言内存 cursor 已更新、消费返回非 nil error、主 cursor 没有伪造新水位，重新装配仍可重试未落盘事件。
   - attach active 时消费返回现有暂缓语义，真实 cursor 文件不前移；解除 attach 后同一事件仍 Wake 并写入水位。
   - cursor 文件为损坏 JSON/不可读路径时，SetupAutomation 记录带 path/cause 的 Error 并以 0 启动；不能把坏文件当成已保存水位；文件不包含 `automationSeen`。

   测试入口覆盖 `SetupAutomation`、`consumeAutomationEventsOnce` 和真实文件；直接测试 `Load/Save` 只作为序列化边界附加锁，理由见第 10 节，不替代上述调用方断言。

3. **运行红测试（2–5 分钟）**：运行 `go test ./internal/agentd -run '^TestB349AutomationCursorPersistence$' -count=1`；预期当前基线因无 cursor store 而失败，必须有真实断言失败，不能用 `no tests to run`。
4. **实现文件介质（2–5 分钟）**：按以下完整控制流实现 `Load`/`Save`，不把任何其他字段写入 JSON：

   ```go
   func newAutomationCursorStore(path string) *automationCursorStore {
	   return &automationCursorStore{path: path}
   }

   func (s *automationCursorStore) Load() (int64, error) {
	   raw, err := os.ReadFile(s.path)
	   if os.IsNotExist(err) {
	       return 0, nil
	   }
	   if err != nil {
	       return 0, fmt.Errorf("读自动化 cursor 文件 %s: %w", s.path, err)
	   }
	   var disk automationCursorDisk
	   if err := json.Unmarshal(raw, &disk); err != nil {
	       return 0, fmt.Errorf("解析自动化 cursor 文件 %s: %w", s.path, err)
	   }
	   return disk.Seq, nil
	}

	func (s *automationCursorStore) Save(seq int64) error {
	   raw, err := json.Marshal(automationCursorDisk{Seq: seq})
	   if err != nil {
	       return fmt.Errorf("编码自动化 cursor: %w", err)
	   }
	   if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
	       return fmt.Errorf("创建自动化 cursor 目录 %s: %w", filepath.Dir(s.path), err)
	   }
	   tmp := s.path + ".tmp"
	   if err := os.WriteFile(tmp, raw, 0o600); err != nil {
	       _ = os.Remove(tmp)
	       return fmt.Errorf("写自动化 cursor 临时文件 %s: %w", tmp, err)
	   }
	   if err := os.Rename(tmp, s.path); err != nil {
	       _ = os.Remove(tmp)
	       return fmt.Errorf("替换自动化 cursor 文件 %s: %w", s.path, err)
	   }
	   return nil
	}

	func (s *Server) advanceAutomationCursor(seq int64) error {
	   s.automationMu.Lock()
	   defer s.automationMu.Unlock()
	   if seq <= s.automationCursor {
	       return nil
	   }
	   s.automationCursor = seq
	   if s.automationCursorStore == nil {
	       return fmt.Errorf("自动化 cursor 持久化未装配：seq=%d", seq)
	   }
	   if err := s.automationCursorStore.Save(seq); err != nil {
	       s.log.Error("自动化 cursor 保存失败", "seq", seq,
	           "path", s.automationCursorStore.path, "cause", err)
	       return fmt.Errorf("保存自动化 cursor seq=%d: %w", seq, err)
	   }
	   s.log.Info("自动化 cursor 已保存", "seq", seq, "path", s.automationCursorStore.path)
	   return nil
	}
	```

   保存失败时不把内存 cursor 回滚，不把失败写成成功；`.tmp` 写失败或 rename 失败都清理临时文件并保留原始错误包装。不要把“跨平台绝对原子”写入注释或日志。

5. **接入 SetupAutomation（2–5 分钟）**：在 `Server.SetupAutomation` 现有 `autoLedger` 装配处用 `filepath.Join(s.conf().DataDir, "automation-cursor.json")` 创建 store 并调用 Load；无文件得到 0，其他错误用 `s.log.Error` 带 `path`、`cause`，随后显式把内存 cursor 置 0 并继续现有服务装配。`StartAutomation` 不读文件、不复制装配逻辑。
6. **接入 cursor 推进时序（2–5 分钟）**：实现 `advanceAutomationCursor`，在 `automationMu` 下若 `seq` 不大于现有内存值则不写；否则先赋值内存，再调用 `automationCursorStore.Save(seq)`，Save 错误返回并带 `seq/path/cause`。替换 `consumeAutomationEventsOnce` 中现有两个直接赋值出口：正常批次末尾、唤醒失败（含自生 `needs_human` 标记之后）都调用它；attach early return、读账错误、malformed 解码错误、尚未推进 cursor 的出口不调用。若唤醒错误与 Save 错误同时发生，用 `errors.Join` 保留两条错误上下文并返回非 nil；不能报成功。
7. **日志和注释（2–5 分钟）**：入口记录 DataDir/path 与 loaded seq；Load 错误、Save 前后、Save 失败、正常推进、attach 暂缓、消费轮完成均使用结构化 logger，带 `from_cursor`、`to_cursor`、`path`、`event_count`、`cause`；注释解释“内存先于 Save”是为了 Save 前崩溃允许重复 Wake、cursor 后移不代表恰好一次，且 `automationSeen` 不落盘。新导出符号不存在；私有方法仍写参数/返回/失败语义注释，禁止 `print`。
8. **运行绿测试（2–5 分钟）**：运行 `go test ./internal/agentd -run 'Test(B349|AutomationCursor)' -count=1`；必须命中新增持久化、重启、Save 失败、损坏文件、attach 测试并返回 `ok`。此 task 测试范围只限 `./internal/agentd`。

### 7.3 T3 可判定验收

- 新 Server + 同 DataDir + SetupAutomation + 直接 consume 能恢复已保存 seq；文件不存在从 0，不能用 ledger MAX(seq) 跳过。
- JSON 只有 `seq`；缺失、显式 `0`、非零、损坏/读错均有区分；权限和 `.tmp`+rename 可观察。
- 内存先于 Save；Save 失败返回 error 且不伪称已保存；attach 暂缓不推进文件；`automationSeen` 不落盘。
- 共享 ledger、room cursor、协调者 wait cursor 不被写入。

## 8. T4：全接缝回归、图锚、范围与交接门禁

T4 只在 T1–T3 通过后执行，不新增生产路径。

### 8.1 回归命令与判据

按顺序运行并保存原始输出：

```text
go test ./internal/proto ./internal/ledger/api ./internal/agentd ./cmd -count=1
go test ./... -count=1
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b349-breakdown.md
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . check
git diff --check
git status --short --branch
git diff --name-only
```

判定规则：

- 四个触及包测试和新增 T1–T3 正反例必须 `ok`；全量测试若仍只出现基线已记录的 `TestRepoContractGate` 两项违规，原文记录并交协调者裁决，不能修改本卡范围以掩盖；出现其它失败则 fail。
- `resolve --doc` 退出 0，breakdown 中所有现有 file#Symbol 锚只能为 `ok`/`moved`；T1/T2/T3 新增私有符号属于实现期间覆盖债，不能在本计划节点伪造 codegraph diff。
- `validate` 必须保持退出 0；`check` 的基线原始失败为 `dead-contract d_execution→d_orchestration` 与 `dead-interface ApprovalClient`，实现不得新增第三类失败；原始结果逐字落台账。
- `git diff --check` 退出 0；`git diff --name-only` 只能出现第 3.1 节列出的生产/测试/计划/台账文件，不能出现上游 spec/contract/breakdown 改动、codegraph diff 或未声明生产文件。

### 8.2 T4 接缝覆盖双向核对

| 声明接缝 | 至少一支入口测试 | 断言 |
| --- | --- | --- |
| Facade/HTTP source 投影 | T0 已提交的 `TestFacadeEventsFromAscPreservesSourceIdentity`、`TestCardDetailProjectsMirroredSourceIdentity` | source 三列非空透传、卡原生零值、omitempty。 |
| wakeconsumer identity × consumer | T1 新增 `TestB349AutomationSourceIdentity`（入口 `consumeAutomationEventsOnce`）及迁移的 B233.6/B353 consumer tests | 当前/旧/错/双空/一空/无快照/缺 source task/source_seq 忽略。 |
| card wait identity × stdout | T2 新增 `TestB349CardWaitSourceIdentity`（入口 `runCardWait`）及 B353 follow tests | 事件所属卡、subtree、策略先后、stdout JSON 与错误上下文。 |
| cursor Setup/consume/文件 | T3 新增 `TestB349AutomationCursorPersistence`（入口 `SetupAutomation` + `consumeAutomationEventsOnce`）及 attach/Save failure tests | 新 Server 读回、至少一次、内存先 Save、attach 不推进、文件边界。 |

内部 `Load/Save` roundtrip/损坏文件测试只能附加，不能顶替三条产品接缝；合法理由是：只有直接构造文件介质才能把“文件缺失 vs `seq:0`、损坏 JSON、临时文件失败”与消费缝中的业务 Wake 结果分离，消费缝无法逐项观察这些文件解析/rename 分支。该理由与所有附加测试逐条对应，不能把 helper 测试当接缝绿灯。

### 8.3 序列化边界清单

实现阶段逐边界保留可失败断言：

1. `ledger.Event → eventWire → proto.LedgerEvent → Facade JSON`：T0 测试覆盖 source target/task/seq 非空、卡原生 `""/""/0`、缺失与显式空/零。
2. `ledger.Event → ledgerEventWire → GET /api/cards/{id} JSON`：T0 HTTP 测试覆盖同一组边界，确认不改 payload。
3. `task_mirrored` envelope JSON 与 `LedgerEvent.Source*`：T1/T2 断言 source 三列不进入 envelope；envelope node/attempt 缺失/null/空串与 source task 缺失/空串分开处理。
4. `EvDispatched` payload → `DispatchSnapshot`：T1/T2 断言 `TaskID != Attempt`、缺 Node/Attempt 不成为当前快照，分页尾部仍可见。
5. `ledger.Event → card wait json.Encoder → stdout`：T2 断言源三列缺席/空/非零与 payload 保真。
6. `automationCursorDisk{Seq} ↔ automation-cursor.json`：T3 断言 JSON 只有 `seq`，缺文件、显式 `0`、非零、损坏 JSON、Save 失败、`.tmp` 清理和权限。

### 8.4 缺陷族对抗审查

| 缺陷族 | 计划内防线 | 真机边界 |
| --- | --- | --- |
| 生命周期/状态机中断 | T3 新 Server 读回、Save 前失败、attach early return、取消沿既有 Follow/loop 路径回归；T1/T2 不新增 goroutine/第二订阅。 | 真 SIGKILL、DataDir 锁竞争、PG LISTEN/SQLite WAL、网络断线、真实 Keystone/mirror/executor 未验证，列真机清单。 |
| 静默失败/误导报错 | malformed envelope/payload、快照读错、Load/Save 错误均返回原始上下文并结构化记录；合法身份拒绝只留账本，不冒充成功。 | 外部服务错误文案与跨进程日志聚合未验证。 |
| 跨平台假设 | `filepath.Join`、0700/0600、tmp+rename 在当前 Go 测试覆盖；不宣称绝对原子。 | Unix/Windows rename、权限、DataDir lock 需真机。 |
| 假红/假绿 | 测试入口固定为 `consumeAutomationEventsOnce`/`runCardWait`/`SetupAutomation`；保留旧 attempt、无快照、双空、一空、subtree、Save 失败、新 Server 反例；命令不能以 no tests to run 通过。 | 并发派发换 attempt 与真实 wake/Encode TOCTOU 需真机。 |
| 门禁绕过 | 不新增 CLI/HTTP/ledger API；cursor 只能由 SetupAutomation/既有消费路径写；身份闸在策略调用和 Wake/Encode 之前。 | 快照读取到动作之间的 TOCTOU、多 agentd 争用需真机。 |
| 枚举/集合漂移 | `EvTaskMirrored`、`EvDispatched` 字面值不变；`WaitDeliveryPolicy` 是唯一集合；T1/T2 审计类型和可动作类型各有正反例。 | 外部执行器新增事件类型不在本卡范围。 |

### 8.5 类型标注的真机清单（全部“未验证，需真机”）

1. 真实目标 agentd/执行器产生 question、permission、delivery_failed、completed、failed，验证 source task/target 与当前快照一致时 wakeconsumer 与 `card wait --follow` 的动作，旧 attempt/错机器/缺 source task 只留账本。
2. 真实本机 target 为空的派发与镜像，验证双空 target 仍 Wake/输出，一空一非空拒绝。
3. 真实事件卡为 subtree 子卡、wait 根卡不同，验证快照查询不串卡。
4. agentd 重启覆盖已消费、未消费、Save 前中断、attach 暂缓窗口，验证 DataDir cursor 续拉、至少一次、坏文件/磁盘错误日志和实际权限。
5. SQLite 轮询、PG LISTEN、直连、relay、协调者重启/网络断线下不因通知延迟、游标重连或源重复错误 wake/stdout。
6. 真实 Keystone attach 与 Wake 竞态、并发换 attempt、多 agentd/target 命名，验证读快照后动作的 TOCTOU 是否符合产品接受语义。
7. 支持的 Unix/Windows 验证 `filepath.Join`、tmp+rename、0700/0600、DataDir lock；不把当前 Unix 单测外推为跨平台事实。
8. 真实 executor/外部服务事件生产链，不能以 fake transport、fake Keystone 或夹具 card_events 证明事件确实来自目标 task。

## 9. 用户故事与冻结项归属

| 用户故事/冻结项 | 具体 task 与断言 |
| --- | --- |
| 故事 1：旧 task 的 question/delivery_failed 不拉起新协调者；本机双空提问仍拉起 | T1 的 `consumeAutomationEventsOnce` source identity 正反例；T3 的 cursor 重启/至少一次例补本机重启语义。 |
| 故事 2：`card wait --follow` 同闸，旧/无快照/plain 不 stdout，当前可动作事件 stdout | T2 的 `runCardWait` / `Store.Follow` / encoder 真实测试，包含事件所属子卡与 subtree。 |
| 故事 3：重启续拉，不重复已保存 seq，不丢 cursor 后新事件，attach 暂缓可重试 | T3 的 SetupAutomation + 新 Server + consume、Save 失败和 attach 测试。 |
| 合约 #1–#16 | T0 已完成；T4 不重写，T1/T2 只验证 source 不进 envelope、T2 stdout 不丢列。 |
| 合约 #17–#38 | T1/T2 分别实现 current snapshot、双消费点身份闸、策略顺序、事件所属卡、无新命令/endpoint；T4 做字面值回归。 |
| 合约 #39–#50 | T3 实现独立 cursor、Setup 读回、内存先 Save、至少一次、attach 不推进、不复用其它 cursor；T4 检查文件集合。 |

## 10. 计划自审与已声明例外

- **spec 覆盖**：三条用户故事均指到 T1/T2/T3 的具体调用方测试；T4 只做交接门禁。
- **占位符扫描**：本计划不含未完成标记；代码块给出冻结签名与完整关键控制流，测试复用既有 harness 的地方逐条列出了 harness 文件、入口符号和 pass/fail 断言。
- **跨 task 签名一致性**：T1 消费 `proto.LedgerEvent`；T2 消费 `ledger.Event`；T3 仍消费既有 `consumeAutomationEventsOnce`，cursor 只由 `Server` 装配，不引入别名类型或跨包新接口。
- **上下文预算**：T1 圈 `wakeconsumer.go` + 同包测试；T2 圈 `card_wait.go` + 同包测试；T3 圈 cursor 新文件、server 装配、wakeconsumer 接线 + 同包测试；没有圈不出的触点，不插竖切卡。
- **类型标注**：d_orchestration/d_cli 为逻辑型，验收必须穿过消费调用方；DataDir/HTTP/文件权限为 boundary 行为，机内测 wire/文件形状，真实进程/目标 OS 在第 8.5 节明确“未验证，需真机”。
- **接缝双向**：所有产品测试入口都在第 8.2 节接缝清单；每条清单均有测试。内部 Load/Save 测试仅附加，且已声明“从声明缝构造不出文件解析/rename 分支”的唯一理由。
- **图覆盖债**：沿用 breakdown 的私有符号债；`WaitDeliveryPolicy` 的 `codegraph sym` 原始失败已写台账，计划按源码 `internal/client/delivery.go:23` 使用，不声称图已覆盖。
- **派发自审**：本计划没有驱动 handoff/派发系统的验收步骤；不派发、不调用 handoff CLI、不起新的 executor。

## 11. 实现交接的最终 pass 条件

只有以下事实全部真实满足才可把 implement 回合判 pass：

1. T1、T2、T3 各自的新增调用方测试命中并返回 `ok`，没有 `no tests to run`。
2. source identity 的六项条件、双空/一空 target、source_seq 忽略、分页到尾、事件所属卡/subtree、WaitDeliveryPolicy 顺序均由真实消费入口锁定。
3. cursor 文件读回、JSON/权限、内存先 Save、Save 失败、attach 暂缓、新 Server 重启续拉均由真实文件和真实消费入口锁定。
4. T4 的触及包回归、`git diff --check`、文件集合和 graph `resolve/validate` 满足 8.1；`codegraph check` 只保留已记录的基线两项，任何新增违规均 fail。
5. 真机清单仍明确标为“未验证，需真机”，不能用单测、fake transport、fake Keystone 或日志替代真实 agentd/executor/目标 OS 结论。
