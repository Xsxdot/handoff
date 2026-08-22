# B171：耗时打点——权限审批等待归 other 实现计划


**Goal:** 在不改变 `proto.TimingEntry` 形状、帧格式或存储契约的前提下，把权限请求从进入等待到权限应答成功之间的墙钟时间从工具段扣出，使三分法中的差额准确归入 `other`。

**Architecture:** `internal/executor/turn` 维护通用的「等人窗口」状态；四家 adapter 只在各自权限门的挂起入口调用 `PauseWaiting(part)`，在外部权限应答成功后调用 `Resume(part)`。`Segmenter` 仍只产 `api`、`tool`、`turn` 条目，`other` 仍由已有聚合层按 `TotalMS - APIMS - ToolSpanMS` 求差额。opencode 的权限载荷已带 `tool.callID`，仅补充该既有字段的映射，不能发明新的 proto 字段。

**Tech Stack:** Go、`proto.TimingEntry`、四家现有 adapter、现有 fake-clock/permission/SSE/ACP harness、结构化 `slog`。

**Spec:** `docs/superpowers/specs/2026-08-22-b171-timing-approval-window.md`（权威版本：`origin/main` 提交 `d843c48c9`；本计划不修改 spec）。

## 基线读数与验收前提

本计划写作前已执行 `git fetch origin`，并用 `git show origin/main:<path>` 读取权威实现。当前 charter 分支基点是 `7dec3118`，尚未包含下面列出的 timing 文件；实现者必须在协调者指定的、包含 `origin/main` timing 实现的基线工作树上执行任务，不得把本 charter 分支误当实现基线。

已核对的现状：

- `origin/main:internal/executor/turn/timing.go:33-60` 的 `openTool` 只有 `tool/detail/start`，`Segmenter` 以一把 mutex 保护所有状态；`:101-165` 的 `ToolStart/ToolEnd` 生成工具条目，`:168-198` 的 `EndTurn/closeTurnLocked` 会丢弃未闭合工具；`:216-227` 的 turn 条目是回合墙钟分母。
- `origin/main:internal/executor/claudecode/adapter.go:676-699` 在写帧前调用 `ToolStart`，`:703-744` 在写结果帧前调用 `ToolEnd`，`:871-894` 的 `onPermissionAsk` 发权限事件，`:371-422` 的 `RespondPermission` 只有底层 `perm.sock` 应答成功后才代表等待结束。
- `origin/main:internal/executor/codex/adapter.go:1031-1078` 的 `reqCommandApproval` 与 `reqFileChangeApproval` 是权限门，`:1080-1090` 的 `reqPermissionsApproval` 是沙箱放宽请求，不属于本卡；`origin/main:internal/executor/codex/perm.go:271-310` 在 `r.cli.Reply` 成功后才算裁决送达。
- `origin/main:internal/executor/grok/adapter.go:1115-1149` 的 `OnPermission` 使用 `toolCallId` 上报权限，`OnAskQuestion` 紧随其后但不属于本卡；`origin/main:internal/executor/grok/perm.go:104-145` 的 `RespondPermission` 在 ACP `Reply` 成功后才结束等待。
- `origin/main:internal/executor/opencode/adapter.go:1244-1351` 当前解析 `permission.asked` 的 `id/sessionID/metadata`，但没有消费其已有的 `tool.callID`；`:1551-1608` 用 `callID` 作为工具配对键，`:579-637` 的 `RespondPermission` 在 HTTP 应答成功后才结束等待。
- `origin/main:internal/store/timing_agg.go:113-176` 已按 `OffsetMS + DurMS` 求工具区间并以差额计算 `OtherMS`；本卡不改它。`origin/main:internal/proto/timing.go:55-97` 已固定 `OffsetMS`、`ToolMS`、`ToolSpanMS`、`OtherMS` 的语义，不能增加等待段类型。

基线判据已在 `origin/main` 快照执行：

```text
$ go test ./internal/executor/turn ./internal/executor/claudecode ./internal/executor/codex ./internal/executor/grok ./internal/executor/opencode
ok   github.com/Xsxdot/handoff/internal/executor/turn
ok   github.com/Xsxdot/handoff/internal/executor/codex
ok   github.com/Xsxdot/handoff/internal/executor/grok
ok   github.com/Xsxdot/handoff/internal/executor/opencode
```

同一命令中 claudecode 在本 handoff 沙箱的深路径临时目录下失败，原文为 `裁决 socket 路径过长（114 字节，上限 107）`；将 `TestStart*`/`TestClaudeToolTimingPaired` 的临时根目录配置到可写的短路径后，预期该包为 `ok`。这是测试环境前置条件，不是本卡代码判据。每个实现 task 仍须先在自己的指定基线用短且可写的临时根目录重跑对应命令，先确认基线结果，再写失败测试。

## 全局约束

- 只修改本计划各 task 明列的文件；不修改 `docs/superpowers/specs/2026-08-22-b171-timing-approval-window.md`，不修改 proto、数据库 schema、聚合公式、控制台或历史数据。
- `PauseWaiting`/`Resume` 是通用「等人」信号，当前只接权限门；提问工单、grok `OnAskQuestion`、codex `reqPermissionsApproval` 均保持现状。
- `part` 必须沿用既有工具调用配对键：Claude `tool_use_id`、Codex `itemId`、Grok `toolCallId`、opencode `tool.callID`。缺少配对键时 fail-closed：记录结构化 warning，不猜另一个工具，也不暂停/恢复任意工具。
- 等待窗口只在真正进入等待时暂停；权限回发失败时不 `Resume`，等待状态保留以便重试；批准和拒绝都以底层回发成功作为恢复点。
- 既有 `reportTiming` 是唯一 `AdapterEvent` 出口：`func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry)`。新增信号必须通过它产生 `AdapterEvent{Type: "usage", Timing: &e}`，不能新造事件类型。
- 每个 adapter 入口、外部应答前后、成功恢复、缺失映射和错误分支都要沿用本包的结构化 logger；`Segmenter` 是无 I/O 的纯状态机，不引入 logger 依赖，但新导出方法、字段和非显然收口逻辑必须补注释。
- 每个 task 只跑触及包的测试；全量 `go test ./...` 只在末尾协调者验收 task 执行。

## Task 1：Segmenter 增加通用等人窗口

**Files:**

- `internal/executor/turn/timing.go`
- `internal/executor/turn/timing_test.go`

**Interfaces**

Consumes:

```go
func (s *Segmenter) ToolStart(part, tool, detail string) []proto.TimingEntry
func (s *Segmenter) ToolEnd(part string) (time.Duration, []proto.TimingEntry)
func (s *Segmenter) EndTurn() []proto.TimingEntry
```

Produces:

```go
func (s *Segmenter) PauseWaiting(part string) []proto.TimingEntry
func (s *Segmenter) Resume(part string) []proto.TimingEntry
```

`PauseWaiting`/`Resume` 成功时至多各产一条现有 `Kind=turn` 条目；未知 part、回合外信号、重复 Pause、未处于等待的 Resume 和 nil receiver 均返回 nil，不产生假工具条目。

### 步骤

- [ ] **基线先跑。** 在包含 `origin/main` timing 实现的指定工作树中执行：

  ```bash
  go test ./internal/executor/turn -run 'TestSegmenter(SimpleTurn|ConcurrentTools|UnpairedTool|EndTurnIdempotent|NilSafe)$'
  ```

  预期：现有测试全部 `PASS`，且 `PauseWaiting`/`Resume` 尚不存在；若现有测试不是绿，先停在本 task，不把基线问题混入实现。

- [ ] **先写红测试。** 在现有 `fakeClock`、`newTestClock`、`pick` harness（`internal/executor/turn/timing_test.go:10-42`）上加入以下完整判据；不使用真实 sleep：

  ```go
  func TestSegmenterPauseWaitingMovesWindowToOther(t *testing.T) {
      c := newTestClock()
      s := NewSegmenter(c.now)

      all := runSeq(s, c,
          func(s *Segmenter, _ *fakeClock) []proto.TimingEntry { return s.BeginTurn(1) },
          func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
              c.add(2 * time.Second)
              return s.ToolStart("t1", "Bash", "go test ./...")
          },
          func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
              c.add(3 * time.Second)
              return s.PauseWaiting("t1")
          },
          func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
              c.add(60 * time.Second)
              return s.Resume("t1")
          },
          func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
              c.add(5 * time.Second)
              _, entries := s.ToolEnd("t1")
              return entries
          },
          func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
              c.add(2 * time.Second)
              return s.EndTurn()
          },
      )

      tools := pick(all, proto.TimingKindTool)
      if len(tools) != 1 {
          t.Fatalf("应恰好有一条工具段，实得 %d", len(tools))
      }
      if tools[0].DurMS != 8000 || tools[0].OffsetMS != 2000 {
          t.Fatalf("工具段应只含暂停前 3s 与恢复后 5s，dur=%d offset=%d",
              tools[0].DurMS, tools[0].OffsetMS)
      }
      var total, api, tool int64
      for _, e := range all {
          switch e.Kind {
          case proto.TimingKindTurn:
              if e.DurMS > total {
                  total = e.DurMS
              }
          case proto.TimingKindAPI:
              api += e.DurMS
          case proto.TimingKindTool:
              tool += e.DurMS
          }
      }
      if total != 72000 || api != 4000 || tool != 8000 {
          t.Fatalf("总账应为 total=72000 api=4000 tool=8000，实得 %d/%d/%d",
              total, api, tool)
      }
      if other := total - api - tool; other != 60000 {
          t.Fatalf("审批等待应进入 other，实得 %dms", other)
      }
  }

  func TestSegmenterWaitingLifecycleIsNilSafe(t *testing.T) {
      var nilSegmenter *Segmenter
      if got := nilSegmenter.PauseWaiting("t1"); got != nil {
          t.Fatalf("nil PauseWaiting 应为空，实得 %v", got)
      }
      if got := nilSegmenter.Resume("t1"); got != nil {
          t.Fatalf("nil Resume 应为空，实得 %v", got)
      }

      c := newTestClock()
      s := NewSegmenter(c.now)
      s.BeginTurn(1)
      c.add(time.Second)
      s.ToolStart("t1", "Bash", "echo hi")
      if got := s.PauseWaiting("missing"); got != nil {
          t.Fatalf("未知 part 不应产生条目：%v", got)
      }
      s.PauseWaiting("t1")
      c.add(2 * time.Second)
      if got := s.PauseWaiting("t1"); got != nil {
          t.Fatalf("重复 Pause 不应重复打开窗口：%v", got)
      }
      s.Resume("t1")
      c.add(3 * time.Second)
      if got := s.Resume("t1"); got != nil {
          t.Fatalf("重复 Resume 不应重复扣时：%v", got)
      }
      c.add(time.Second)
      dur, _ := s.ToolEnd("t1")
      if dur != 4*time.Second {
          t.Fatalf("连续多次信号后的工具耗时应为 1s+3s，实得 %s", dur)
      }

      s.BeginTurn(2)
      s.ToolStart("t2", "Bash", "echo pending")
      c.add(time.Second)
      s.PauseWaiting("t2")
      c.add(30 * time.Second)
      s.EndTurn()
      if dur, entries := s.ToolEnd("t2"); dur != -1 || entries != nil {
          t.Fatalf("回合终止应收口未闭窗口且丢弃未结束工具，dur=%s entries=%v", dur, entries)
      }
  }
  ```

  预期：新增测试先因未定义方法或未扣除等待而 `FAIL`。

- [ ] **最小实现。** 在 `openTool` 增加带注释的 `waiting bool`、`waitingSince time.Time`、`waitingMS time.Duration`。新增私有 `finishWaitingLocked(ot *openTool, now time.Time)`：仅在 `waiting` 为真时累加正的 `now.Sub(waitingSince)`，清掉 waiting 标志；时钟回退按 0 处理，防止负数。

  `PauseWaiting` 与 `Resume` 的实现必须遵循以下代码语义（方法注释写明参数、返回和 nil/未知 part 行为）：

  ```go
  func (s *Segmenter) PauseWaiting(part string) []proto.TimingEntry {
      if s == nil || part == "" { return nil }
      s.mu.Lock()
      defer s.mu.Unlock()
      if s.turn == 0 { return nil }
      ot, ok := s.open[part]
      if !ok || ot.waiting { return nil }
      now := s.now()
      ot.waiting, ot.waitingSince = true, now
      return []proto.TimingEntry{s.turnEntryLocked(now)}
  }

  func (s *Segmenter) Resume(part string) []proto.TimingEntry {
      if s == nil || part == "" { return nil }
      s.mu.Lock()
      defer s.mu.Unlock()
      if s.turn == 0 { return nil }
      ot, ok := s.open[part]
      if !ok || !ot.waiting { return nil }
      now := s.now()
      finishWaitingLocked(ot, now)
      return []proto.TimingEntry{s.turnEntryLocked(now)}
  }
  ```

  `ToolEnd` 先用当前时刻调用 `finishWaitingLocked`，再计算 `dur := now.Sub(ot.start) - ot.waitingMS`，小于零时钳为零；`OffsetMS` 继续取 `ot.start.Sub(s.turnStart)`，这样已有聚合器用 `OffsetMS + DurMS` 计算的工具跨度会把等待窗口留给 other。`closeTurnLocked` 在丢弃 `open` 前遍历所有 open tool 并收口 waiting，保证只 Pause 不 Resume 的回合不会留下计时状态；仍不为没有 ToolEnd 的工具伪造工具条目。

- [ ] **跑绿并检查并发。** 执行：

  ```bash
  go test ./internal/executor/turn -run 'TestSegmenter(PauseWaitingMovesWindowToOther|WaitingLifecycleIsNilSafe)$'
  go test -race ./internal/executor/turn
  ```

  预期：两条新增测试和全部既有测试 `PASS`；`-race` 无报告。任务只触及 turn 包，不跑全量。

- [ ] **日志、注释与边界复核。** `Segmenter` 不接 logger，保持纯状态机边界；在新方法和 `openTool` 字段注释中明确这是为了保持 proto 不变而在采集时扣除等待。核对 Pause/Resume 不创建 `other` 条目、不修改 `TimingEntry` 字段、不触碰帧或 store。

**Task 1 completion evidence:** 记录上述两个新测试、既有 turn 测试和 `go test -race` 的原文结果；只有全部为 `PASS` 才进入 adapter tasks。

## Task 2：claudecode 权限门接入 Pause/Resume

**Files:**

- `internal/executor/claudecode/adapter.go`
- `internal/executor/claudecode/timing_test.go`

**Interfaces**

Consumes:

```go
func (a *Adapter) onPermissionAsk(r *runState, ask permAsk)
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, reason string) (err error)
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry)
func (s *turn.Segmenter) PauseWaiting(part string) []proto.TimingEntry
func (s *turn.Segmenter) Resume(part string) []proto.TimingEntry
```

Produces:

```go
executor.AdapterEvent{Type: "usage", Timing: *proto.TimingEntry}
executor.AdapterEvent{Type: "permission", PermissionID: ask.ToolUseID, ...}
```

### 步骤

- [ ] **基线先跑。** 在短且可写的临时根目录下执行：

  ```bash
  go test ./internal/executor/claudecode -run 'TestClaudeToolTimingPaired|TestPermServerAskThenRespond'
  ```

  预期：两条既有测试 `PASS`；本 handoff 沙箱的深路径会复现 `裁决 socket 路径过长`，必须先改变测试临时根目录而不是修改 socket 逻辑。

- [ ] **先写红测试。** 复用 `internal/executor/claudecode/timing_test.go` 的 `installPersistentFakeClaude`、`shortTestDir`、`waitForClaudeResult`，以及 `perm_test.go` 的 `newPermServer`/`dialAsk` harness；新增最小权限等待回归。断言逐条为：

  1. `appendActionSummary` 先让 `tool_use_id=toolu-wait-1` 进入 Segmenter；
  2. `onPermissionAsk` 产生恰好一条 `permission` 事件，并在其前通过 `reportTiming` 产生 Pause 的 turn 事件；
  3. fake clock/可控时钟让等待窗口经过 60 秒；
  4. 底层 `perm.sock` 成功收到 `RespondPermission(..., "once", ...)` 后才出现 Resume 的 turn 事件；
  5. 随后 `mapUserMessage` 的 tool result 产出一条 `TimingKindTool`，`DurMS` 不含 60 秒；
  6. 同一条回合条目的最大 `DurMS` 减去 API 和 tool 时长等于 60 秒；
  7. 底层 socket 回发失败时没有 Resume，重试成功后只 Resume 一次；
  8. 既有 `frames.jsonl` 的 tool_call/tool_result 结构与 `dur_ms` 投影仍满足原断言，未增加字段。

  这里必须照抄现有 harness 的事件消费方式，不另造 socket、运行态或事件通道；由于该包的真实进程/socket 夹具形态不同于 turn 包，采用计划纪律允许的“既有 harness + 逐条 pass/fail 断言”例外。

- [ ] **最小实现。** `onPermissionAsk` 在现有 fail-closed 解析和文本兜底完成后、`a.emit(... Type: "permission" ...)` 前加入：

  ```go
  a.log.Info("claude 权限等待开始", "task", r.taskID, "perm", ask.ToolUseID)
  a.reportTiming(r, r.seg.PauseWaiting(ask.ToolUseID))
  ```

  `RespondPermission` 把 `return r.perm.Respond(...)` 改为先保存底层返回值；只有无错误时记录“权限等待结束”并调用 `a.reportTiming(r, r.seg.Resume(permID))`，底层错误原样返回且不 Resume。保留现有 once/deny 映射、拒绝理由和 deferred 错误日志。

- [ ] **跑绿。** 执行：

  ```bash
  go test ./internal/executor/claudecode -run 'TestClaudeToolTimingPaired|TestClaudePermissionWaitNotToolTime|TestPermServerAskThenRespond'
  go test -race ./internal/executor/claudecode -run 'TestClaudePermissionWaitNotToolTime|TestPermServerAskThenRespond'
  ```

  预期：新增回归、既有 timing/permission 测试均 `PASS`，无 race。只跑 claudecode 包。

- [ ] **日志与注释复核。** 新增日志包含 task、perm、成功/失败上下文；在 Pause/Resume 接缝旁注明必须在权限事件前暂停、必须在 socket 成功后恢复，避免把写帧或网络失败时间归错。

**Task 2 completion evidence:** 保存两条测试命令的原文输出；socket 回发失败分支必须被测试覆盖。

## Task 3：codex 两类权限门接入 Pause/Resume

**Files:**

- `internal/executor/codex/adapter.go`
- `internal/executor/codex/perm.go`
- `internal/executor/codex/timing_test.go`

**Interfaces**

Consumes:

```go
func (h *handler) OnServerRequest(reqID json.RawMessage, method string, params json.RawMessage) bool
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, _ string) error
func (r *runState) appendItemFrame(method string, item *threadItem)
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry)
```

Produces:

```go
executor.AdapterEvent{Type: "usage", Timing: *proto.TimingEntry}
executor.AdapterEvent{Type: "permission", PermissionID: ap.ItemID or p.ItemID, ...}
```

### 步骤

- [ ] **基线先跑。** 执行：

  ```bash
  go test ./internal/executor/codex -run 'TestCodexToolTimingPaired|TestCodexTimingShapeMatchesClaude'
  ```

  预期：现有两个 timing 测试 `PASS`。

- [ ] **先写红测试。** 复用 `internal/executor/codex/timing_test.go` 的 `newTimingTestRun`、`timingTestClock`、`collectTimingTestEntries` 与 `readTimingTestFrames`。新增一个最小 command-approval 流程，逐条断言：

  1. `appendItemFrame(ntfItemStarted, item-1)` 先打开工具 part `item-1`；
  2. `handler.OnServerRequest(..., reqCommandApproval, ...)` 产生权限事件并调用 Pause；
  3. 时钟前进 60 秒；
  4. `RespondPermission(..., "once", ...)` 的 fake JSON-RPC 回答成功后调用 Resume；
  5. `ntfItemCompleted` 后仅有一条 `TimingKindTool`，其 `DurMS` 只包含等待前后活动时长，残差为 60 秒；
  6. 另跑 `reqFileChangeApproval` 的同样断言，证明两个权限门都接线；
  7. `reqPermissionsApproval` 只保留现有 fail-closed `Reply`，不产生 Pause/Resume；
  8. `Reply` 失败时没有 Resume；重试成功时不重复扣除窗口；
  9. 既有 tool_call/tool_result frame 的 `dur_ms` 断言保持通过。

  测试必须沿用现有 timing harness 的 fake websocket；不直接替测试调用 `Segmenter`，以确保 `OnServerRequest → RespondPermission → appendItemFrame → finishTurn` 的真实 adapter 接缝经过。

- [ ] **最小实现。** 在 `OnServerRequest` 的 `reqCommandApproval` 与 `reqFileChangeApproval` 分支中，完成 `r.note`/render 日志后、`a.emit(permission)` 前调用 `a.reportTiming(r, r.seg.PauseWaiting(itemID))`，并记录 task、perm、approval method。不要在 `reqPermissionsApproval` 分支调用它。

  在 `perm.go:RespondPermission` 中，保留 `take(permID)`、decision 映射和 `r.cli.Reply` 错误语义；只有 `Reply` 返回 nil 后记录“权限裁决已送达”并调用 `a.reportTiming(r, r.seg.Resume(permID))`。不要在 `take` 成功时提前恢复，因为网络回发失败必须仍算等待。

- [ ] **跑绿。** 执行：

  ```bash
  go test ./internal/executor/codex -run 'TestCodexToolTimingPaired|TestCodexTimingShapeMatchesClaude|TestCodexPermissionWaitNotToolTime'
  go test -race ./internal/executor/codex -run 'TestCodexPermissionWaitNotToolTime'
  ```

  预期：command/file approval 与失败重试断言均 `PASS`，无 race。

- [ ] **日志与注释复核。** 在两处权限门的 Pause 处标明 itemId 是既有工具 part；在 Resume 处标明 `Reply` 成功是唯一恢复点；保留 `reqPermissionsApproval` 的安全边界注释，避免未来把沙箱放宽误接成普通权限门。

**Task 3 completion evidence:** 测试输出必须同时证明 command 与 file-change 两路，且 permissions approval 未触发 timing 信号。

## Task 4：grok ACP 权限请求接入 Pause/Resume

**Files:**

- `internal/executor/grok/adapter.go`
- `internal/executor/grok/perm.go`
- `internal/executor/grok/timing_test.go`

**Interfaces**

Consumes:

```go
func (h *acpHandler) OnPermission(reqID, params json.RawMessage)
func (h *acpHandler) OnAskQuestion(reqID, params json.RawMessage)
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, _ string) (err error)
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry)
```

Produces:

```go
executor.AdapterEvent{Type: "usage", Timing: *proto.TimingEntry}
executor.AdapterEvent{Type: "permission", PermissionID: p.ToolCall.ToolCallID, ...}
```

### 步骤

- [ ] **基线先跑。** 执行：

  ```bash
  go test ./internal/executor/grok -run 'TestGrokToolTimingPaired|TestGrokUnknownToolStatusIsNotTerminal'
  ```

  预期：现有测试 `PASS`。

- [ ] **先写红测试。** 复用 `internal/executor/grok/timing_test.go` 的 `newACPTestRun`、`waitForTurnResult`、`drainTimings`、`readFrames` 和 `internal/executor/grok/perm_test.go` 的 ACP reply fake。新增权限回归，逐条断言：

  1. 真实 `tool_call`/`tool_call_update` 先打开与 `toolCallId` 同名的工具段；
  2. `OnPermission` 登记 pending 后产生 Pause 与 permission 事件；
  3. 可控时钟前进 60 秒；
  4. `RespondPermission` 的 ACP `Reply` 成功后产生一次 Resume；
  5. 工具终态后 tool `DurMS` 不含 60 秒，turn/API/tool 残差为 60 秒；
  6. ACP `Reply` 失败不 Resume，重试成功只恢复一次；
  7. `OnAskQuestion` 只走既有提问流程，不产生 Pause/Resume；
  8. 既有 tool_call/tool_result frame 与未知状态不产终态的断言保持通过。

  测试必须复用现有 ACP websocket，不把 `OnPermission` 替换成直接调用 Segmenter；这是 adapter 接线回归。

- [ ] **最小实现。** 在 `OnPermission` 的 `notePending` 成功路径、`h.a.emit(permission)` 前调用 `h.a.reportTiming(h.r, h.r.seg.PauseWaiting(p.ToolCall.ToolCallID))`，保留解析失败时立即 reject 的 fail-closed 行为。`OnAskQuestion` 不改。

  在 `perm.go:RespondPermission` 中仅在 `r.cli.Reply` 返回 nil 后调用 `a.reportTiming(r, r.seg.Resume(permID))`；拒绝清单登记和现有成功日志继续执行，Reply 错误路径不恢复。

- [ ] **跑绿。** 执行：

  ```bash
  go test ./internal/executor/grok -run 'TestGrokToolTimingPaired|TestGrokUnknownToolStatusIsNotTerminal|TestGrokPermissionWaitNotToolTime'
  go test -race ./internal/executor/grok -run 'TestGrokPermissionWaitNotToolTime'
  ```

  预期：权限等待、失败重试、提问不接线和原 timing 测试均 `PASS`。

- [ ] **日志与注释复核。** 新日志包含 task、toolCallId、ACP request id（可用时）、Pause/Resume 成功或失败；新注释明确 `OnPermission` 与 `OnAskQuestion` 是两条不同的人等接缝，本卡只接前者。

**Task 4 completion evidence:** 测试输出必须有权限路径和提问排除路径的明确断言。

## Task 5：opencode permission.asked 关联 tool.callID 并接入 Pause/Resume

**Files:**

- `internal/executor/opencode/adapter.go`
- `internal/executor/opencode/adapter_test.go`
- `internal/executor/opencode/timing_test.go`

**Interfaces**

Consumes:

```go
func (a *Adapter) mapPermissionAsked(r *runState, props json.RawMessage)
func (a *Adapter) mapToolPart(r *runState, tool, key, status string, input json.RawMessage, output string)
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, _ string) (err error)
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry)
```

Produces:

```go
executor.AdapterEvent{Type: "usage", Timing: *proto.TimingEntry}
executor.AdapterEvent{Type: "permission", PermissionID: pa.ID, ...}
```

### 步骤

- [ ] **基线先跑。** 执行：

  ```bash
  go test ./internal/executor/opencode -run 'TestOpencodeToolTimingPaired|TestStartToPermissionFlow|TestPermissionAskedMapping|TestPermissionRepliedIgnored'
  ```

  预期：现有工具配对、权限转发、权限回显忽略测试 `PASS`。

- [ ] **先写红测试。** 复用 `internal/executor/opencode/timing_test.go` 的 `startFakeRun`、`newFakeServer`、`feedPart`、`finishIdleForTimingTest`、`collectEvents`、`waitForResultEvents`、`readFrames`，以及 `adapter_test.go:339-376` 的 `permissionAskedEvent`/`permissionRepliedEvent` 构造器。新增/扩展回归，逐条断言：

  1. 先喂同一 `callID=call-1` 的 pending/running tool part，再喂含 `properties.tool.callID="call-1"` 的 `permission.asked`；
  2. `mapPermissionAsked` 产生 permission 事件、把 `perm-1 → call-1` 保存到运行态映射，并只 Pause 一次；重复推送同一 `permission.asked` 不重复 Pause；
  3. fake server 收到 `RespondPermission(..., "once", ...)` 的 HTTP 请求后才 Resume；等待窗口为 60 秒时工具 `DurMS` 不含该窗口；
  4. HTTP 回发失败不 Resume，重试成功只 Resume 一次；
  5. 同一回合的 turn/API/tool 条目残差为 60 秒，`frames.jsonl` 的 tool_call/tool_result `dur_ms` 与既有断言不变；
  6. 没有 `tool.callID` 的真实形状只发 permission 事件并记录 warning，不暂停未知 part，RespondPermission 成功后不恢复任意工具；
  7. `permission.replied` 仍被忽略，`question.asked` 不触发 timing；
  8. 使用 `testdata/perm_bash.json` 或 `spike5-events.jsonl` 的真实嵌套 `tool.callID` 做至少一条载荷边界回归，不能只测试平铺的自造字段。

  该包已有 `newAdapterWithRunForTest` 等运行态夹具；新增 `permTiming` 映射初始化必须同步更新该夹具。按计划纪律，测试正文复用包内 fake server/SSE 形态，以上逐条断言是可判 pass/fail 的完整测试规格。

- [ ] **最小实现。** 在 `runState` 增加有注释的、由 `sessMu` 保护的 `permTiming map[string]permissionTiming`；`permissionTiming` 至少含 `part string` 与 `paused bool`，在 `newRun` 及现有测试运行态构造器初始化。

  `mapPermissionAsked` 的局部载荷加入：

  ```go
  Tool struct {
      CallID string `json:"callID"`
  } `json:"tool"`
  ```

  `pa.Tool.CallID` 非空且该 permission id 首次出现时，登记 `permTiming[pa.ID] = permissionTiming{part: pa.Tool.CallID, paused: true}`，调用 `a.reportTiming(r, r.seg.PauseWaiting(pa.Tool.CallID))`，然后才 emit permission。重复 SSE 只保留已有映射，不重新 Pause。缺 callID 时不写伪造 part，记录带 task/perm 的 warning 后继续现有权限事件和结构化 `Perm` 逻辑。

  `RespondPermission` 保持现有 `permSession` 会话选择和 HTTP 调用；只有 `r.api.RespondPermission` 成功后，原子地把对应 `permissionTiming.paused` 置为 false，再调用 `a.reportTiming(r, r.seg.Resume(timing.part))` 并记录成功日志。HTTP 错误不改变 paused 状态。不得在 `permission.replied` SSE 回显处 Resume，因为那不是协调者应答成功的唯一出口。

- [ ] **跑绿。** 执行：

  ```bash
  go test ./internal/executor/opencode -run 'TestOpencodeToolTimingPaired|TestStartToPermissionFlow|TestPermissionAskedMapping|TestPermissionRepliedIgnored|TestOpencodePermissionWaitNotToolTime'
  go test -race ./internal/executor/opencode -run 'TestOpencodePermissionWaitNotToolTime|TestPermissionRepliedIgnored'
  ```

  预期：真实嵌套 callID、重复 asked、缺 callID、HTTP 失败重试和既有测试均 `PASS`。

- [ ] **日志与注释复核。** 新日志区分 permission id、tool callID、session；缺映射、暂停成功、恢复成功、HTTP 失败各有上下文。注释说明 `tool.callID` 是既有 opencode 载荷字段，新增的是内存映射，不是 proto/store 序列化字段。

**Task 5 completion evidence:** 保存真实 fixture 回归和 race 测试输出；确认 `question.asked` 与 `permission.replied` 都没有误接 timing。

## 序列化边界与缺陷族验收清单

本卡不新增数据字段，因此没有新的手写 JSON/proto/store 序列化边界；仍须沿现有链路验证行为：

1. `internal/executor/turn/timing.go:150-158` 生成原有 `proto.TimingEntry.DurMS/OffsetMS`；Task 1 断言等待前后工具条目和 turn/API 残差。
2. 四家 adapter 的 `reportTiming`（claudecode `:964`、codex `:620`、grok `:425`、opencode `:994`）把同一 `TimingEntry` 投影为既有 `usage` 事件；各 adapter 回归必须读取真实 `AdapterEvent.Timing`，不能只断言 Segmenter 私有状态。
3. 既有 `FrameWriter.ToolResult` 投影继续使用 `ToolEnd` 返回的 `time.Duration`；各 adapter timing harness 必须读取 `frames.jsonl`，断言 `tool_call` 不带 `dur_ms`、`tool_result` 仍带扣除等待后的 `dur_ms`。
4. `internal/store/timing_agg.go:152-176` 的差额公式不改；Task 1 的 `total-api-tool` 残差断言与 Task 6 的聚合回归共同证明等待落入 other，而不是把值为零误当字段缺失。没有跨语言另一侧，也没有新增 DTO、CLI 拼接或 JSON key。

缺陷族对抗结论（必须在实现后的验收记录中逐项勾选）：

- **重复/重放：** 重复 ToolStart、重复 permission ask、重复 Pause/Resume、重复 EndTurn 都不得重复扣时或生成假工具条目。
- **乱序/缺失：** 未知 part、缺 tool.callID、Resume 先于 Pause、ToolEnd 先于 Resume 均不得产生负数；只 Pause 后 EndTurn 要收口并清理状态。
- **失败语义：** 底层 socket/JSON-RPC/ACP/HTTP 回发失败不 Resume；成功重试只恢复一次。
- **并发：** Segmenter 全部状态在现有 mutex 下更新；adapter 的 opencode 映射遵循既有 `sessMu` 锁序，不在 `turnMu` 下做网络 I/O。
- **边界归属：** Claude/Codex/Grok/opencode 只接权限门；提问、沙箱放宽和历史数据不改变。
- **协议/存储漂移：** `TimingEntry`、`AdapterEvent`、Frame JSON、store schema 和聚合公式的字段形状逐项保持原样。

## Task 6：最终集成与真机验收（本 task 由协调者执行，不派发）

本 task 驱动的是协调流程自身的验收，不交给 executor，也不修改代码。

**Files inspected:** `internal/executor/turn/`、`internal/executor/claudecode/`、`internal/executor/codex/`、`internal/executor/grok/`、`internal/executor/opencode/`、`internal/store/timing_agg.go`。

**Interfaces checked:**

```go
func (s *turn.Segmenter) PauseWaiting(part string) []proto.TimingEntry
func (s *turn.Segmenter) Resume(part string) []proto.TimingEntry
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry)
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, reason string) (err error)
```

### 验收步骤

- [ ] 在短且可写的临时根目录执行五包定向测试与 race 测试：

  ```bash
  go test ./internal/executor/turn ./internal/executor/claudecode ./internal/executor/codex ./internal/executor/grok ./internal/executor/opencode
  go test -race ./internal/executor/turn ./internal/executor/claudecode ./internal/executor/codex ./internal/executor/grok ./internal/executor/opencode
  ```

  预期：两条命令均 `PASS`，无 data race；若 claude 再出现 socket 路径错误，先修验收环境临时根目录，不改业务代码。

- [ ] 运行全量 `go test ./...`；全量失败必须逐条归因，不能用局部测试绿替代全量结果。

- [ ] 真机/回放清单逐条记录：

  - Claude：tool_use → 权限事件 → 等待至少 60 秒 → once/reject 成功回发 → tool_result；工具段不含等待，other 含等待。
  - Codex：command approval 与 file-change approval 各一条；`reqPermissionsApproval` 不改变计时。
  - Grok：`OnPermission` 计入等待；`OnAskQuestion` 不计入工具段暂停。
  - opencode：使用真实 `permission.asked.properties.tool.callID`，包含 SSE 重放与 `permission.replied` 回显；只在 HTTP 应答成功后恢复。
  - 四家：底层应答失败期间不恢复；回合终止时只 Pause 的窗口无负数、无悬挂状态。

- [ ] 复核序列化边界：`TimingEntry` JSON/proto 形状未变，frames 中只有已有 `dur_ms` 数值变化；`store/timing_agg.go` 的 `OtherMS` 为正且等于等待窗口，不能出现字段缺失与零值混淆。

- [ ] 对照“缺陷族验收清单”逐项写下 pass/fail，并把定向测试、race、全量测试、真机/回放结果作为本卡最终证据。

## 自审与占位符扫描

- spec 覆盖：问题中的工具窗归属、通用等人信号、四家权限门、nil 安全、未闭合收口、连续 Pause/Resume、proto 不变、提问/历史/展示 out of scope，分别落在 Task 1-5 与协调者 Task 6。
- 类型/签名核对：四家均消费同一对 `PauseWaiting(string) []proto.TimingEntry` 与 `Resume(string) []proto.TimingEntry`；各自 `RespondPermission` 的精确签名按 `origin/main` 逐字列出；opencode 仅消费已有 `tool.callID`。
- 测试例外声明：四家 adapter 的 permission fake/socket/SSE/ACP harness 已存在且包内形态不同，计划没有伪造一套跨包 harness；每个 task 已逐条列出断言、点名要复用的文件与 helper，并要求穿过 `AdapterEvent`/`frames.jsonl` 的真实边界。
- 上下文预算：Task 1 只圈 turn 两文件；Task 2-5 各圈一个 adapter 的生产文件与现有 timing/adapter 测试；Task 6 只由协调者执行，不扩大 executor 的实现文件集。
- 占位符扫描：全文不含 TBD、TODO、“同 Task N”或未给代码位置的“适当处理”式空步骤；所有失败分支、日志、注释、测试范围和预期结果均已写明。
