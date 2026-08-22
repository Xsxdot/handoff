# 需求 A 第二批实现计划 · T4 grok / T5 opencode

- 上游：`2026-08-22-executor-timing-contract.md`（契约冻结）、`2026-08-22-executor-timing-breakdown.md`（T1~T7）
- 本批范围：**T4（grok 补工具帧 + 打点）、T5（opencode 补工具帧 + 打点）**，外加第一批评审遗留一条
- 不在本批：T6（聚合与接线，d_ledger）、T7（TUI 展示，d_web）
- 基线提交：`27212adb`（第一批合入 feature 分支后的 HEAD）

> **第一批的验收尚未完成**（任务 `e61dd4a0-af0d-4229-b4cb-fa44381f1dcb` 停在
> `waiting_review`）。本批与它一起走 review/acceptance，不单独归档。

---

## 0. 基线核验（本轮实测，不引用早前的绿）

```
$ go test ./internal/executor/...
ok  	github.com/Xsxdot/handoff/internal/executor
ok  	github.com/Xsxdot/handoff/internal/executor/claudecode
ok  	github.com/Xsxdot/handoff/internal/executor/codex
ok  	github.com/Xsxdot/handoff/internal/executor/fake
ok  	github.com/Xsxdot/handoff/internal/executor/grok        2.843s
ok  	github.com/Xsxdot/handoff/internal/executor/opencode   21.332s
ok  	github.com/Xsxdot/handoff/internal/executor/rawtap
ok  	github.com/Xsxdot/handoff/internal/executor/turn
```

两个目标包在基线上全绿。**本批的每条验收命令都是上面这些包的子集**，没有引入
需要新环境的判据。

---

## 1. 协议与代码事实（每条带出处，禁止凭印象）

### 1.1 第一批留下的可复用件（`internal/executor/turn`）

| 符号 | 出处 | 契约 |
|---|---|---|
| `Segmenter` | `internal/executor/turn/timing.go:46` | 四个信号：`BeginTurn(turn int)` / `ToolStart(part, tool, detail)` / `ToolEnd(part) (time.Duration, []proto.TimingEntry)` / `EndTurn()`。全部 nil 安全 |
| `ToolStart` 幂等 | `timing.go:108`（`if _, dup := s.open[part]; !dup`） | **同 part 重复 start 不重复计数**——正是 opencode 反复发 `running` 所需 |
| `ToolEnd` 未配对 | `timing.go:139`（`if !ok { return -1, nil }`） | 返回 `-1, nil`，**不产 dur=0 的假条目** |
| `EndTurn` 幂等 | `timing.go:172` | 回合已收尾时返回 nil；开着的工具**不产条目**，缺口由聚合层 `Partial` 标出 |
| `FrameWriter.ToolCall(part, tool, input)` | `internal/executor/turn/frames.go:202` | input 走头尾截断 |
| `FrameWriter.ToolResult(part, status, output string, dur time.Duration)` | `frames.go:225` | **dur < 0 = 不知道**，帧上不带 `dur_ms`（`durMS` 见 `frames.go:243`） |
| `FrameWriter.Turn() int` | `frames.go`（第一批新增） | 回合号的唯一来源，**不得自建计数器** |

### 1.2 参考实现（第一批已落地的两家，本批照抄形态）

- claudecode：`ToolStart` 在写帧**之前** `adapter.go:693`；`ToolEnd` 在写帧
  **之前** `adapter.go:739`；`EndTurn` 在 `mapResult` 的**第一行** `adapter.go:752`；
  `reportTiming` `adapter.go:964`；`emit` 的 `case "usage":` 静默分支 `adapter.go:1001`。
- codex：`reportTiming` `adapter.go:620`；`BeginTurn` `adapter.go:321`/`:518`；
  `EndTurn` `adapter.go:787`；工具两端 `adapter.go:1153`/`:1163`。

**「打点必须在写帧之前」**（`claudecode/adapter.go:691` 的注释原文）：写帧要过一次
头尾截断与文件 IO，把那段时间算进工具耗时是在给工具记别人的账。本批两家照此办理。

### 1.3 grok（ACP）

- **今天不产任何工具帧**：全仓 `frames.ToolCall(` / `frames.ToolResult(` 的调用点
  只有 claudecode 与 codex 两家（`grep -rn "frames.ToolCall\|frames.ToolResult"
  internal/executor/` 实测）。T4 是**从无到有**，不是改造。
- 当前的拒绝理由写在 `internal/executor/grok/adapter.go:653-656`：
  「grok 的工具动作今天只有一行人读摘要（toolLine，带 200 截断），拿它当
  tool_call 帧的 input 会把『命令尾部可能藏着危险片段』复制进帧流」。
  **这条理由已不成立**：`rawInput` 是完整的 `json.RawMessage`
  （`adapter.go:707` 的解析结构体里就有它），`rawCommand`（`adapter.go:773`）
  正是拿它取**不截断**的命令。截断只发生在 `toolLine`（`adapter.go:761`）
  这一条人读摘要路径上。T4 用 `rawInput` 原文，绕开那个反对意见。
- 事件形状（`internal/executor/grok/testdata/updates.jsonl` 第 3、4 行）：
  ```
  {"sessionUpdate":"tool_call","toolCallId":"call-1-0","title":"run_terminal_command","rawInput":{"command":"echo hi","description":"say hi"}}
  {"sessionUpdate":"tool_call_update","toolCallId":"call-1-0","status":"completed","title":"Execute `echo hi`"}
  ```
  配对键 = `toolCallId`；**工具名取 `tool_call` 的 `title`**（`tool_call_update`
  的 title 会变成人读句子「Execute \`echo hi\`」，不能拿它当工具名）。
- **⚠ 这份 testdata 是手写夹具，不是真机抓包**（同目录另有 `perm_*.json` 才是
  真机取样，见 `adapter.go:786` 的注释「字段取舍全部来自 Task 1 的真机取样」）。
  因此 `status` 的**真实取值集合**与 `tool_call_update` 是否带 `content`（工具输出）
  **列入真机清单，不作为结论写死**。代码按「认识 `completed`/`failed`，其余一律
  不算终态」写，不认识的状态只是不产 tool_result 帧，回合照跑。
- 分流点：`onSessionUpdate`（`adapter.go:908`）持 `r.turnMu`，先 `acc.feedRaw(raw)`
  再用 `updateFrameFields(raw)` 单独解析出帧字段（`adapter.go:915`）。
  **既有先例就是「累积器之外再解析一次」**，T4 沿用它加一个 `updateToolFields`。
- 回合边界：`BeginTurn` 在 `adapter.go:211`（Start）与 `:300`（Send）；回合收尾
  唯一入口是 `finishTurn`（`adapter.go:516`），它有**三条出口**
  （`res.Err`→518、`stopReason != end_turn`→540、正常路径），故 `EndTurn`
  必须放在函数**第一行**。
- `emit`（`adapter.go:380`）无 `switch ev.Type`，非阻塞（`select` 带 `default`），
  `evCh` 缓冲 64（`adapter.go:167`）。**不需要加日志分支**。

### 1.4 opencode（SSE）

- 同样**今天不产任何工具帧**。`adapter.go:1511` 的注释「工具调用本身由
  mapToolPart 以完整的 tool_call 帧上报」引用了一个**根本不存在的函数**
  （`grep -rn mapToolPart internal/` 只命中这行注释）。这是一条陈注释，T5 让它
  变成真的。
- **`frameKind("tool") == kindSkip` 必须保持不变**：那管的是 tool part 的
  *文本增量*（工具入参的流式拼装）不产 text 帧，与工具帧是两条路。
  `opencode/frames_test.go:18` 钉着它，**不许改这个断言**。
- 事件形状（`internal/executor/opencode/testdata/spike5-events.jsonl` 第 285/287/299/301/303 行，
  **真机抓包**）：
  ```
  part: {type:"tool", tool:"bash", callID:"call_00_…", id:"prt_…", messageID:"msg_…",
         state:{status:"pending", input:{}, raw:""}}
  part: {…, state:{status:"running", input:{"command":"echo spike-hi"}, time:{start:…}}}
  part: {…, state:{status:"running", metadata:{output:"spike-hi\n"}, …}}   ← 会重复多条
  part: {…, state:{status:"completed", input:{…}, output:"spike-hi\n",
         metadata:{output:…,exit:0,truncated:false}, title:"echo spike-hi",
         time:{start:…, end:…}}}
  ```
- 四条推论：
  1. **`running` 会重复到达**（输出边长边发），所以 tool_call 帧必须**只在首见时写一次**，
     tool_result 帧只在终态写一次——需要一个 per-run 的阶段表。
  2. 配对键用 `callID`（权限事件 `permission.asked` 的 `tool.callID` 也用它，
     见上引第 289 行），`callID` 为空时回落 `part.id`。
  3. `state.output` 只在终态出现；`state.input` 是对象，取 `json.RawMessage` 原文。
  4. **`state.time.start` 在重试后会跳变**（第 287 行 `1786156464643` → 第 299 行
     `1786156478355`，中间隔着一次权限审批），它记的是「最后一次尝试」的起点，
     **不含审批等待**。
- **拍板（口径一致性）：`dur_ms` 一律取 `Segmenter` 的墙钟，不用 opencode 自报的
  `state.time`。** 理由：claudecode/codex 的工具耗时都含权限审批等待，取
  `state.time` 会让 opencode 的同一条命令看起来比别家快，而这个差是**口径差不是
  性能差**——契约文档 §2.1 的三分法要的是可比。被否方案：用 `state.time` 更"准确"；
  否掉是因为跨执行者可比性优先于单家精确性，且第一批已按墙钟落了两家。
- 终态取值：`completed` / `error`。`error` 的出处是 `adapter.go:1701` 的注释
  「opencode 收到 reject 直接终结回合，只留 **error 状态**的 tool part、零文本」
  ——这是代码里已有的行为事实，不是猜测。
- 分流点：`mapPartUpdated`（`adapter.go:1449`）已在解析 `part`，且已在
  `r.partTypes[key] = p.Type` 处认得 `type == "tool"`（`adapter.go:1469`）。
  T5 **扩既有解析结构体**，不新增第二次 `json.Unmarshal`。
- 回合边界：`BeginTurn` 在 `adapter.go:377`（Start）与 `:477`（Send）；回合收尾
  唯一入口是 `mapIdle`（`adapter.go:1696`），它有**四条 return 出口**，故
  `EndTurn` 放函数第一行。
- **`emit`（`adapter.go:935`）的 `switch` 缺 `case "usage"`**，`Type:"usage"` 今天
  落进 `default:` 打 `Info("adapter 产出未知事件")`。今天每回合只有 2 条
  （`adapter.go:1406`/`:1414`）所以没人注意；加上耗时条目会变成几十条/回合的刷屏。
  T5 补上这个 case（照抄 claudecode `adapter.go:1001` 的注释理由）。
- **⚠ 测试陷阱**：opencode 的 `emit` 是**阻塞**的
  （`select { case r.evCh <- ev: ; case <-r.stopCh: }`，`adapter.go:959`），
  `evCh` 缓冲只有 16（`adapter.go:254`）。喂事件而不排空通道的测试**会死锁**。
  T5 的测试必须开一个 goroutine 持续排空 `evCh`。（claudecode 的
  `timing_test.go` 靠 16 缓冲 + 事件少而侥幸没撞上，别照抄那个形态。）

---

## Task 1 · T4：grok 补工具帧 + 打点

**Interfaces**

- Consumes：`turn.Segmenter`（1.1 表）、`turn.FrameWriter.ToolCall/ToolResult/Turn`
- Produces：包内 `updateToolFields(raw []byte) (toolUpdate, bool)`、
  `(*Adapter).reportTiming(r *runState, entries []proto.TimingEntry)`

### 步骤

**1.1 先写失败测试**（新建 `internal/executor/grok/timing_test.go`）

```go
package grok

import (
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestGrokToolTimingPaired 钉住「工具调用的两端都喂给了段切分器」。
//
// 它不验时长算得对不对（那是 turn 包的事），只验**信号有没有喂对**：
// 一次配对的 tool_call / tool_call_update(completed) 必须产出 tool 条目，
// 且 tool_result 帧上带 dur_ms。
func TestGrokToolTimingPaired(t *testing.T) {
	a := New(nil)
	r := newTestRun(t, a, "timing-paired")

	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	h := &acpHandler{a: a, r: r}
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"run_terminal_command","rawInput":{"command":"echo hi"}}}`))
	// 留出可观测的毫秒间隔，避免真实耗时被 Duration.Milliseconds 截成 0
	time.Sleep(2 * time.Millisecond)
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","title":"Execute ` + "`echo hi`" + `"}}`))
	a.reportTiming(r, r.seg.EndTurn())

	timings := drainTimings(r)
	var tool *proto.TimingEntry
	for i := range timings {
		if timings[i].Kind == proto.TimingKindTool {
			tool = &timings[i]
		}
	}
	if tool == nil {
		t.Fatal("配对的工具调用必须产出 tool 条目")
	}
	if tool.Label != "run_terminal_command" {
		t.Errorf("Label 应取 tool_call 的 title（不是 update 的人读句子），实得 %q", tool.Label)
	}
	if tool.DurMS <= 0 {
		t.Errorf("配对成功时耗时应为正，实得 %d", tool.DurMS)
	}
	if !hasKind(timings, proto.TimingKindAPI) {
		t.Error("工具开始时必须收掉当前模型段，缺 api 条目")
	}

	frames := readFrames(t, r)
	var call, result *proto.Frame
	for i := range frames {
		switch frames[i].Type {
		case proto.FrameToolCall:
			call = &frames[i]
		case proto.FrameToolResult:
			result = &frames[i]
		}
	}
	if call == nil || result == nil {
		t.Fatalf("工具两端都要产帧，实得 call=%v result=%v", call, result)
	}
	if call.Tool != "run_terminal_command" {
		t.Errorf("tool_call 帧的工具名应取 title，实得 %q", call.Tool)
	}
	// 帧里存 rawInput 全文，不是 toolLine 的 200 字摘要（这正是 W4a 当初的反对理由）
	if !strings.Contains(call.Input, `"echo hi"`) {
		t.Errorf("tool_call 帧应含 rawInput 原文，实得 %q", call.Input)
	}
	if result.Status != "ok" {
		t.Errorf("completed 应映射为 ok，实得 %q", result.Status)
	}
	if result.DurMS <= 0 {
		t.Errorf("配对成功时帧上应带正的 dur_ms，实得 %d", result.DurMS)
	}
	if call.DurMS != 0 {
		t.Errorf("tool_call 帧不带耗时（那时还不知道），实得 %d", call.DurMS)
	}
}

// TestGrokUnknownToolStatusIsNotTerminal 钉住「不认识的状态不算终态」。
//
// grok 的 status 真实取值集合尚未真机确认（testdata/updates.jsonl 是手写夹具），
// 所以这里锁的是**保守方向**：不认识就不产 tool_result 帧、不收工具段，
// 回合照跑。开着的工具由 EndTurn 丢弃、由聚合层的 Partial 标出。
func TestGrokUnknownToolStatusIsNotTerminal(t *testing.T) {
	a := New(nil)
	r := newTestRun(t, a, "timing-unknown-status")
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	h := &acpHandler{a: a, r: r}
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call","toolCallId":"c1","title":"x","rawInput":{}}}`))
	h.onSessionUpdate([]byte(`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"in_progress"}}`))

	for _, f := range readFrames(t, r) {
		if f.Type == proto.FrameToolResult {
			t.Fatal("非终态不得产 tool_result 帧")
		}
	}
	for _, e := range drainTimings(r) {
		if e.Kind == proto.TimingKindTool {
			t.Fatal("非终态不得收工具段")
		}
	}
}
```

配套的测试辅助（同文件）：

```go
// newTestRun 造一个只带 frames/seg 的最小运行态：本文件的测试不碰 ACP 连接。
func newTestRun(t *testing.T, a *Adapter, id string) *runState {
	t.Helper()
	taskDir := t.TempDir()
	fw, err := turn.WriterFor(taskDir, a.log)
	if err != nil {
		t.Fatalf("WriterFor: %v", err)
	}
	r := &runState{
		taskID: id, taskDir: taskDir,
		evCh: make(chan executor.AdapterEvent, 256),
		acc:  newTurnAccumulator(), pending: map[string]pendingPerm{},
		frames: fw, seg: turn.NewSegmenter(nil),
	}
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	r.textPart = r.frames.NextPart()
	return r
}

// drainTimings 取走通道里全部耗时条目（非阻塞排空）。
func drainTimings(r *runState) []proto.TimingEntry {
	var out []proto.TimingEntry
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Timing != nil {
				out = append(out, *ev.Timing)
			}
		default:
			return out
		}
	}
}

func hasKind(es []proto.TimingEntry, k proto.TimingKind) bool {
	for _, e := range es {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// readFrames 读回本任务已落盘的帧。
//
// 路径常量 turn.FramesFileName 是导出的，别自己拼文件名。
func readFrames(t *testing.T, r *runState) []proto.Frame {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.taskDir, turn.FramesFileName))
	if err != nil {
		t.Fatalf("读 frames.jsonl: %v", err)
	}
	var out []proto.Frame
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var f proto.Frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("解析帧 %q: %v", line, err)
		}
		out = append(out, f)
	}
	return out
}
```

> 上面这份 `readFrames` 与 `internal/executor/claudecode/timing_test.go` 里的读帧
> 段落是同一套做法（同一份文件格式、同一个 taskDir 布局）。**不要另行发明路径推导**
> ——`turn.FramesFileName` 是导出常量。
>
> import 需要：`encoding/json`、`os`、`path/filepath`、`strings`、`testing`、`time`、
> `github.com/Xsxdot/handoff/internal/executor`、`.../internal/executor/turn`、
> `.../internal/proto`。

跑红：

```bash
go test ./internal/executor/grok/ -run 'TestGrokToolTiming|TestGrokUnknownToolStatus' 2>&1 | tail -20
```

预期：编译失败（`runState` 没有 `seg` 字段、没有 `reportTiming`）。**编译失败也算红**，
但必须确认失败原因是缺这些符号，不是别的。

**1.2 最小实现**

(a) `internal/executor/grok/adapter.go` 的 `runState`（`:113` 附近，紧挨 `frames`）：

```go
	frames   *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
	seg      *turn.Segmenter   // 耗时段切分器；与 frames 同款 nil 安全约定
	// toolNames 记 toolCallId -> 工具名（取自 tool_call 的 title）。
	// 为什么要存：tool_call_update 的 title 是给人读的句子（"Execute `echo hi`"），
	// 拿它当工具名会让统计里出现无数个只出现一次的"工具"。会话级不清空——
	// toolCallId 在会话内唯一，跨回合残留不会串味。
	toolNames map[string]string
```

(b) `Start` 里构造运行态处（`:165` 的 `r := &runState{…}`）补两个字段：

```go
		acc: newTurnAccumulator(), pending: map[string]pendingPerm{},
		toolNames: map[string]string{},
```
并在 `r.frames = fw` 那个块之后加一行（**与 frames 分开**：Segmenter 的构造不会失败）：

```go
	// 段切分器不依赖文件 IO，构造不会失败，与 frames 的 nil 兜底无关
	r.seg = turn.NewSegmenter(nil)
```

(c) `Start` 的 `BeginTurn` 之后（`:211` 那行的紧邻下一行）：

```go
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
```

(d) `Send` 的 `BeginTurn` 之后（`:300` 那行的紧邻下一行）：同一行代码。

(e) `finishTurn`（`:516`）的**第一行**：

```go
func (a *Adapter) finishTurn(r *runState, res ACPResult) {
	// 回合收尾在最前面：本函数有三条出口（res.Err、stopReason 异常、正常路径），
	// 放在开头是唯一能同时覆盖三条的位置。EndTurn 幂等，重复触发无害。
	a.reportTiming(r, r.seg.EndTurn())
	if res.Err != nil {
```

(f) 新增 `reportTiming`（放在 `emit` 之后，`:395` 附近，**与 codex 版逐字一致**
只改日志前缀）：

```go
// reportTiming 把段切分器产出的条目逐条经 usage 事件上报。
//
// 为什么走 usage 而不是新事件类型：Usage（当前占用）、Spend（累计消耗）与
// Timing（耗时）是同一次模型调用结束时的三样产物；拆成两个事件，两者之间
// 就能插进一次 agentd 重启（契约文档 §6.3 的拍板记录）。
//
// entries 为空是常态（不是错误），静默返回。
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry) {
	for i := range entries {
		e := entries[i]
		if !a.emit(r, executor.AdapterEvent{Type: "usage", Timing: &e}) {
			a.log.Debug("grok 耗时条目未能上报（事件通道已终止或已满）",
				"task", r.taskID, "key", e.Key, "remaining", len(entries)-i)
			return
		}
	}
}
```

(g) 改写 `updateFrameKind` 的文档注释（`:651-658`）——**旧注释是一条已被推翻的
结论，留着就是骗下一个人**：

```go
// updateFrameKind 把 ACP 的 sessionUpdate 类型归类成帧类型。
//
// 为什么 tool_call / tool_call_update 归 updateNone：它们不产**正文/思维链**帧。
// 工具帧走另一条路（updateToolFields → onSessionUpdate 的工具分支），
// 那条路拿的是 rawInput 原文而不是 toolLine 的 200 字摘要，所以当初
// 「拿人读摘要当帧 input 会丢掉命令尾部」的反对理由在那里不成立。
//
// 未知类型一律 updateNone。
func updateFrameKind(sessionUpdate string) updateKind {
```

`frames_test.go:6` 的 `TestUpdateFrameKind` 断言**一字不改**（`tool_call` 仍是
`updateNone`，因为它说的是正文帧归类）。

(h) 新增工具字段解析（放在 `updateFrameFields` 之后，`:757` 附近）：

```go
// toolUpdate 是一条 session/update 里的工具动作字段。
//
// 与 feedRaw / updateFrameFields 同款做法：累积器是纯累积器，工具分流在它的
// 调用方，故这里再解析一次同一份消息（三处解析同一套字段名，改字段要一起改）。
type toolUpdate struct {
	Kind     string          // "tool_call" | "tool_call_update"
	ID       string          // toolCallId，回合内的配对键
	Title    string          // tool_call 时是工具名；tool_call_update 时是人读句子
	Status   string          // 仅 tool_call_update 携带
	RawInput json.RawMessage // 完整入参（不截断）
	Output   string          // 工具输出；ACP 的 content 数组拼出来，可能为空
}

// updateToolFields 从一条原始 session/update 消息里取工具动作字段。
//
// 返回 ok=false 表示这条不是工具动作（含解析失败、非 session/update、
// 缺 toolCallId）——调用方据此跳过，绝不 panic。
//
// 注意 Output：ACP 的 tool_call_update 可以带 content 数组，但 grok 实际发不发、
// 发什么形状**尚未真机确认**（本仓 testdata/updates.jsonl 是手写夹具）。
// 解析不到就留空串，帧上的 output 为空——诚实的空好过编一个值。
func updateToolFields(raw []byte) (toolUpdate, bool) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Update struct {
				Kind     string          `json:"sessionUpdate"`
				ID       string          `json:"toolCallId"`
				Title    string          `json:"title"`
				Status   string          `json:"status"`
				RawInput json.RawMessage `json:"rawInput"`
				Content  []struct {
					Content struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"content"`
			} `json:"update"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Method != "session/update" {
		return toolUpdate{}, false
	}
	u := msg.Params.Update
	if u.Kind != "tool_call" && u.Kind != "tool_call_update" {
		return toolUpdate{}, false
	}
	if u.ID == "" {
		return toolUpdate{}, false // 没有配对键就没法配对，跳过
	}
	var sb strings.Builder
	for _, c := range u.Content {
		sb.WriteString(c.Content.Text)
	}
	return toolUpdate{Kind: u.Kind, ID: u.ID, Title: u.Title,
		Status: u.Status, RawInput: u.RawInput, Output: sb.String()}, true
}

// toolResultStatus 把 ACP 的工具状态映射成帧上的 status。
//
// 返回 ok=false 表示**不是终态**：不产 tool_result 帧、不收工具段。
// 不认识的状态一律按非终态处置——猜错方向的代价不对称：把非终态当终态会
// 提前收段并留下一个永远配不上的 start；把终态当非终态只是少一条条目，
// 由聚合层的 Partial 如实标出。grok 的真实状态取值集合见真机清单。
func toolResultStatus(status string) (string, bool) {
	switch status {
	case "completed":
		return "ok", true
	case "failed", "error":
		return "error", true
	default:
		return "", false
	}
}
```

(i) `onSessionUpdate`（`:908`）在既有帧分流之后、`h.r.turnMu.Unlock()` 之前插入
工具分流：

```go
	if tu, ok := updateToolFields(raw); ok {
		h.a.mapToolUpdate(h.r, tu)
	}
	h.r.turnMu.Unlock()
```

(j) 新增 `mapToolUpdate`（放在 `onSessionUpdate` 之后）：

```go
// mapToolUpdate 把一条工具动作落成帧 + 打点。调用方必须持有 r.turnMu。
//
// 两端各一次：tool_call 产 tool_call 帧并开工具段；终态的 tool_call_update
// 产 tool_result 帧并收工具段。中间态（in_progress 等）只更新不了什么，跳过。
//
// 打点必须在写帧**之前**：写帧要过一次头尾截断与文件 IO，把那段时间算进
// 工具耗时是在给工具记别人的账。
func (a *Adapter) mapToolUpdate(r *runState, tu toolUpdate) {
	if tu.Kind == "tool_call" {
		// 工具名取 tool_call 的 title 并登记：tool_call_update 的 title 是
		// 人读句子，两者不可混用（见 runState.toolNames）
		r.toolNames[tu.ID] = tu.Title
		a.reportTiming(r, r.seg.ToolStart(tu.ID, tu.Title, rawCommand(tu.RawInput)))
		// 帧里存 rawInput 全文（只受头尾截断约束），不是 toolLine 的 200 字摘要
		if err := r.frames.ToolCall(tu.ID, tu.Title, string(tu.RawInput)); err != nil {
			a.log.Warn("写 tool_call 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
		return
	}
	status, terminal := toolResultStatus(tu.Status)
	if !terminal {
		return // 中间态：没有可落的结果，也不能收段
	}
	// 先取耗时再写帧：dur 来自 Segmenter 里记的 tool_call 时刻，
	// 没配上时是 -1（不知道），帧上就不带 dur_ms
	dur, entries := r.seg.ToolEnd(tu.ID)
	if err := r.frames.ToolResult(tu.ID, status, tu.Output, dur); err != nil {
		a.log.Warn("写 tool_result 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
	a.reportTiming(r, entries)
	delete(r.toolNames, tu.ID)
}
```

> `rawCommand(tu.RawInput)` 当 Detail：它取 `rawInput.command` 的**完整**命令
> （`adapter.go:773`），Segmenter 自己按 `DetailRunes=200` 头尾截断
> （`turn/timing.go:110`）。非命令类工具没有 `command` 字段，Detail 为空——
> 那正是"不知道"的诚实表达，不要拿 title 顶上（title 是 Label，两栏一样等于白占一栏）。

`toolNames` 目前只在 `mapToolUpdate` 内自用（Segmenter 已经记了 Label）。
**如果实现完发现它没有第二个读者，就删掉它**——一个只写不读的 map 是噪音。
写这条是因为它可能在 `tool_call_update` 先于 `tool_call` 到达时有用；
真实现时若确认 ACP 保证顺序，直接删，并在 ledger 里记一句。

(k) import 补 `strings`（若尚未引入）与 `github.com/Xsxdot/handoff/internal/proto`。

**1.3 跑绿**

```bash
go test ./internal/executor/grok/ 2>&1 | tail -20
```

**测试范围声明（最小化）**：本 task 只跑 `./internal/executor/grok/`。
`render_golden_test.go` 也在这个包里，它锁的是 render.log 的黄金输出——
本 task **一字不动 render.log 那两股**（`feedRaw` 未改），若它翻红说明改错了地方，
不要去改黄金文件。全量测试不属于本 task。

**1.4 日志**

已在上面的代码里：`reportTiming` 的 Debug（上报失败）、两处写帧失败的 Warn。
**不给每次工具打 Info**：一个回合几十条，逐条 Info 就是刷屏（同 claudecode
`adapter.go:1002` 的理由）。

**1.5 注释**

已在上面的代码里：`toolUpdate`/`updateToolFields`/`toolResultStatus`/`mapToolUpdate`
四处导出面或非显然逻辑都写了「为什么」；`updateFrameKind` 的陈旧结论已改写；
`runState.toolNames` 与 `seg` 两个新字段都带边界说明。
新建的 `timing_test.go` 需补文件头注释（职责：钉住 grok 的工具信号有没有喂对；
边界：不验时长算法，那是 turn 包的事）。

**1.6 提交**

```bash
gofmt -l internal/executor/grok/ && go test ./internal/executor/grok/ && git add -A && git commit -m "feat(grok): 补工具帧与耗时打点"
```

---

## Task 2 · T5：opencode 补工具帧 + 打点

**Interfaces**

- Consumes：同 Task 1
- Produces：包内 `(*Adapter).mapToolPart(...)`、`(*Adapter).reportTiming(...)`、
  `toolStage` 枚举

### 步骤

**2.1 先写失败测试**（新建 `internal/executor/opencode/timing_test.go`）

```go
package opencode

// TestOpencodeToolTimingPaired 钉住「工具 part 的两端都喂给了段切分器」，
// 并钉住 opencode 特有的两条：running 重复到达只算一次、只有终态产结果帧。
func TestOpencodeToolTimingPaired(t *testing.T) {
	a := New(nil)
	r := a.newRun("timing-paired", t.TempDir(), t.TempDir())
	// ⚠ opencode 的 emit 是阻塞的、evCh 只有 16：不排空就会死锁（见计划 §1.4）
	timings := make(chan proto.TimingEntry, 256)
	done := collectTimings(r, timings)

	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	const base = `"id":"prt_1","messageID":"msg_1","type":"tool","tool":"bash","callID":"call_1"`
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"pending","input":{}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"running","input":{"command":"echo hi"}}}}`)
	// 留出可观测的毫秒间隔，避免真实耗时被 Duration.Milliseconds 截成 0
	time.Sleep(2 * time.Millisecond)
	// running 重复到达（真机会发很多条，输出边长边发）
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"running","input":{"command":"echo hi"}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"completed","input":{"command":"echo hi"},"output":"hi"}}}`)
	// 终态重复到达也不许再产一条
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"completed","input":{"command":"echo hi"},"output":"hi"}}}`)
	a.reportTiming(r, r.seg.EndTurn())

	closeEventsForTest(r)
	<-done
	close(timings)

	var tools []proto.TimingEntry
	for e := range timings {
		if e.Kind == proto.TimingKindTool {
			tools = append(tools, e)
		}
	}
	if len(tools) != 1 {
		t.Fatalf("一次工具调用应恰好产一条 tool 条目，实得 %d 条", len(tools))
	}
	if tools[0].Label != "bash" {
		t.Errorf("Label 应取 part.tool，实得 %q", tools[0].Label)
	}
	if tools[0].DurMS <= 0 {
		t.Errorf("配对成功时耗时应为正，实得 %d", tools[0].DurMS)
	}

	var calls, results []proto.Frame
	for _, f := range readFrames(t, r) {
		switch f.Type {
		case proto.FrameToolCall:
			calls = append(calls, f)
		case proto.FrameToolResult:
			results = append(results, f)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("重复的 running 不得重复产 tool_call 帧，实得 %d 条", len(calls))
	}
	if len(results) != 1 {
		t.Fatalf("重复的终态不得重复产 tool_result 帧，实得 %d 条", len(results))
	}
	if results[0].Status != "ok" || results[0].DurMS <= 0 {
		t.Errorf("终态帧应是 ok 且带正的 dur_ms，实得 %q / %d", results[0].Status, results[0].DurMS)
	}
	if !strings.Contains(results[0].Output, "hi") {
		t.Errorf("tool_result 帧应带 state.output，实得 %q", results[0].Output)
	}
}

// TestOpencodeToolTextDeltaStillSkipped 钉住既有不变式没被本次改动破坏：
// tool part 的**文本增量**照旧不产 text 帧（工具帧走另一条路）。
func TestOpencodeToolTextDeltaStillSkipped(t *testing.T) {
	if got := frameKind("tool"); got != kindSkip {
		t.Fatalf("tool part 的文本增量必须继续不产帧，实得 %v", got)
	}
}

// TestOpencodeErrorToolStatus 钉住被拒终止留下的 error 状态 tool part
// 产的是 error 结果帧（adapter.go mapIdle 的注释描述的正是这个现场）。
func TestOpencodeErrorToolStatus(t *testing.T) {
	a := New(nil)
	r := a.newRun("timing-error", t.TempDir(), t.TempDir())
	done := collectTimings(r, nil) // 排空 goroutine 不是可选的，理由见 §1.4

	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
	const base = `"id":"prt_2","messageID":"msg_2","type":"tool","tool":"bash","callID":"call_2"`
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"running","input":{"command":"rm -rf /"}}}}`)
	feedPart(t, a, r, `{"part":{`+base+`,"state":{"status":"error","input":{"command":"rm -rf /"},"output":"权限被拒"}}}`)
	a.reportTiming(r, r.seg.EndTurn())

	closeEventsForTest(r)
	<-done

	var results []proto.Frame
	for _, f := range readFrames(t, r) {
		if f.Type == proto.FrameToolResult {
			results = append(results, f)
		}
	}
	if len(results) != 1 {
		t.Fatalf("error 是终态，应恰好产一条 tool_result 帧，实得 %d 条", len(results))
	}
	if results[0].Status != "error" {
		t.Errorf("error 状态应映射为 error，实得 %q", results[0].Status)
	}
}

// feedPart 在 turnMu 下喂一条 message.part.updated 载荷。
func feedPart(t *testing.T, a *Adapter, r *runState, js string) {
	t.Helper()
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	a.mapPartUpdated(r, json.RawMessage(js))
}

// collectTimings 起一个 goroutine 持续排空 evCh，把耗时条目转投 out
//（out 为 nil 时只排空）。返回的通道在通道关闭、排空结束后关闭。
//
// **排空不是可选的**：opencode 的 emit 阻塞在 evCh 上、缓冲只有 16，
// 不排空的测试会死锁（见计划 §1.4）。
func collectTimings(r *runState, out chan<- proto.TimingEntry) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range r.evCh {
			if out != nil && ev.Type == "usage" && ev.Timing != nil {
				out <- *ev.Timing
			}
		}
	}()
	return done
}

// closeEventsForTest 关掉事件通道让排空 goroutine 退出。
//
// 走 closeOnce 而不是直接调 closeEvents：adapter.go 的注释写明关闭权唯一
// 归 subscribeLoop 的 defer（adapter.go:967），而本测试没起订阅循环。
// 用 closeOnce.Do 与生产路径（adapter.go:780）同款，重复调用也安全。
func closeEventsForTest(r *runState) {
	r.closeOnce.Do(r.closeEvents)
}
```

> `r.closeEvents()`：先 `grep -n "closeEvents\|close(r.evCh)" internal/executor/opencode/adapter.go`
> 确认包内既有的关通道方法名再用，**不要新增一个**。

跑红：

```bash
go test ./internal/executor/opencode/ -run 'TestOpencodeToolTiming|TestOpencodeErrorToolStatus' 2>&1 | tail -20
```

**2.2 最小实现**

(a) `runState`（`adapter.go:181` 附近）：

```go
	frames      *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
	seg         *turn.Segmenter   // 耗时段切分器；与 frames 同款 nil 安全约定
	// toolStages 记 callID -> 推进阶段。
	//
	// 为什么必须有：opencode 的 message.part.updated 对同一个 tool part 会
	// **反复**发 status:"running"（输出边长边发，见 testdata/spike5-events.jsonl
	// 第 299/301 行），终态也可能因 SSE 重放再来一次。没有这张表，一次工具调用
	// 会产出 N 条 tool_call 帧。
	//
	// 会话级不清空：callID 在会话内唯一，与 partTypes 同款理由（A-4）。
	toolStages map[string]toolStage
```

以及枚举（放在 `partFrameKind` 附近）：

```go
// toolStage 是一个 tool part 的推进阶段，用于去重（见 runState.toolStages）。
type toolStage int

const (
	toolStageNone    toolStage = iota // 没见过
	toolStageStarted                  // 已产 tool_call 帧、已开工具段
	toolStageDone                     // 已产 tool_result 帧、已收工具段
)
```

(b) `newRun`（`:250`）补：

```go
		toolStages:      map[string]toolStage{},
```
并在 `r.frames = fw` 之后加：

```go
	// 段切分器不依赖文件 IO，构造不会失败，与 frames 的 nil 兜底无关
	r.seg = turn.NewSegmenter(nil)
```

(c) 两处 `BeginTurn`（`:377`、`:477`）之后各加：

```go
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
```

(d) `mapIdle`（`:1696`）的**第一行**：

```go
func (a *Adapter) mapIdle(r *runState, raw json.RawMessage) {
	// 回合收尾在最前面：本函数有四条出口（被拒空回合、零文本、trailer 三分支），
	// 放在开头是唯一能同时覆盖全部的位置。EndTurn 幂等，重复触发无害。
	a.reportTiming(r, r.seg.EndTurn())
	text := r.turnText()
```

> 锁序核对：`mapIdle` 由 `resolveIdle`（`:1664`）持 `turnMu` 调用，`reportTiming`
> → `emit` 只取 `emitMu`，不回取 `turnMu`——与 `adapter.go:1016` 注释里已经
> 写明的既有结论一致（"emit 只取 emitMu，不回取 turnMu，故持锁 emit 不会死锁"）。

(e) `emit`（`:935`）的 `switch` 补一个 case（放在 `case "result":` 之后、
`default:` 之前）：

```go
	case "usage":
		// 不打 Info：用量/耗时事件频率高（一个回合几十到几百条），逐条打入口
		// 日志就是刷屏。落库结果的日志在 manager 的 handleUsage/handleSpend/
		// handleTiming 里打（那里是 Debug，且只在真落库时打）。
```

> 这不是顺手优化：`Type:"usage"` 今天落进 `default:` 打
> "adapter 产出未知事件"（`:951`），加上耗时条目后会变成每回合几十条错误措辞的
> Info。**它是本次改动引入的问题的修复，不是扩大范围。**

(f) 新增 `reportTiming`（与 claudecode 版逐字一致，只改日志前缀），放在 `emit` 之后。

(g) `mapPartUpdated`（`:1449`）扩解析结构体并加分流：

```go
	var pu struct {
		Part struct {
			ID        string `json:"id"`
			MessageID string `json:"messageID"`
			Type      string `json:"type"`
			Text      string `json:"text"`
			// 以下四个只在 type=="tool" 时有意义（见 mapToolPart）
			Tool   string `json:"tool"`
			CallID string `json:"callID"`
			State  struct {
				Status string          `json:"status"`
				Input  json.RawMessage `json:"input"`
				Output string          `json:"output"`
			} `json:"state"`
		} `json:"part"`
	}
```

在 `r.partTypes[key] = p.Type` 之后、`isText` 计算之前插入：

```go
	if p.Type == "tool" {
		// 工具帧与耗时打点走这一路；tool part 的**文本增量**照旧不产帧
		// （frameKind("tool") == kindSkip，两条路不相干）
		a.mapToolPart(r, p.Tool, toolPartKey(p.CallID, p.ID),
			p.State.Status, p.State.Input, p.State.Output)
	}
```

(h) 新增（放在 `mapPartUpdated` 之后）：

```go
// toolPartKey 取工具调用的配对键：优先 callID，缺失时回落 part.id。
//
// 为什么优先 callID：permission.asked 事件里的 tool.callID 用的就是它
// （testdata/spike5-events.jsonl 第 289 行），拿同一个键才对得上审批与调用。
func toolPartKey(callID, partID string) string {
	if callID != "" {
		return callID
	}
	return partID
}

// mapToolPart 把一次工具调用的推进落成帧 + 打点。调用方必须持有 r.turnMu。
//
// 参数：tool 是工具名（part.tool）；key 是配对键；status/input/output 取自
// part.state。
//
// 去重是本函数的主要职责：opencode 对同一个 tool part 会反复发 running
// （输出边长边发），终态也可能因 SSE 重放再来一次。toolStages 保证
// tool_call 帧与 tool_result 帧各恰好一条。
//
// 打点必须在写帧**之前**：写帧要过一次头尾截断与文件 IO，把那段时间算进
// 工具耗时是在给工具记别人的账。
//
// 为什么 dur 取 Segmenter 的墙钟而不是 part.state.time：state.time 记的是
// 「最后一次尝试」的起点，重试时会跳变、且不含权限审批等待（真机抓包实测：
// 同一个 callID 的 time.start 在一次审批前后从 …464643 跳到 …478355）。
// claudecode/codex 的工具耗时都含审批等待，取 state.time 会让 opencode 的
// 同一条命令看起来比别家快——那是**口径差不是性能差**（计划 §1.4 的拍板）。
func (a *Adapter) mapToolPart(r *runState, tool, key, status string,
	input json.RawMessage, output string) {
	if key == "" {
		return // 没有配对键就没法配对，跳过
	}
	if r.toolStages[key] == toolStageNone {
		r.toolStages[key] = toolStageStarted
		a.reportTiming(r, r.seg.ToolStart(key, tool, toolDetail(input)))
		// 帧里存 state.input 全文（只受头尾截断约束）
		if err := r.frames.ToolCall(key, tool, string(input)); err != nil {
			a.log.Warn("写 tool_call 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
	}
	st, terminal := toolResultStatus(status)
	if !terminal || r.toolStages[key] == toolStageDone {
		return
	}
	r.toolStages[key] = toolStageDone
	// 先取耗时再写帧：没配上时 dur 是 -1（不知道），帧上就不带 dur_ms
	dur, entries := r.seg.ToolEnd(key)
	if err := r.frames.ToolResult(key, st, output, dur); err != nil {
		a.log.Warn("写 tool_result 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
	a.reportTiming(r, entries)
}

// toolResultStatus 把 opencode 的工具状态映射成帧上的 status。
//
// 返回 ok=false 表示**不是终态**（pending/running/未知）：不产 tool_result 帧、
// 不收工具段。error 是真实存在的终态——权限被拒会终结回合并只留 error 状态的
// tool part（见 mapIdle 的注释）。
//
// 不认识的状态一律按非终态处置，理由的不对称性与 grok 版一致：把非终态当终态
// 会提前收段并留下一个永远配不上的 start；把终态当非终态只是少一条条目，
// 由聚合层的 Partial 如实标出。
func toolResultStatus(status string) (string, bool) {
	switch status {
	case "completed":
		return "ok", true
	case "error":
		return "error", true
	default:
		return "", false
	}
}

// toolDetail 从工具入参里取一句给人看的凭据（进 TimingEntry.Detail）。
//
// 优先 input.command（命令类工具），没有就用整个 input 的紧凑 JSON。
// 截断由 Segmenter 按 DetailRunes 负责，这里不截——两处都截会截两次。
func toolDetail(input json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
	}
	if len(input) > 0 && json.Unmarshal(input, &in) == nil && in.Command != "" {
		return in.Command
	}
	return string(input)
}
```

(i) 把 `adapter.go:1511` 那条陈注释（"工具调用本身由 mapToolPart 以完整的
tool_call 帧上报"）核对一遍——现在 `mapToolPart` 真的存在了，注释可以留，
但要确认措辞与新实现一致。

**2.3 跑绿**

```bash
go test ./internal/executor/opencode/ 2>&1 | tail -20
```

**测试范围声明（最小化）**：只跑 `./internal/executor/opencode/`。这个包里有
`render_golden_test.go`、`replay_probe_test.go`、`replay_spike_test.go` 三个回放类
测试——它们喂的正是本 task 新接的那些真机事件，**它们翻红是本 task 的责任**
（大概率是 emit 阻塞或帧数变化），必须查，不许改夹具绕过。
包耗时约 21s，属正常。全量测试不属于本 task。

**2.4 日志 / 2.5 注释**：同 Task 1，已内联在代码里。新文件 `timing_test.go` 补文件头注释。

**2.6 提交**

```bash
gofmt -l internal/executor/opencode/ && go test ./internal/executor/opencode/ && git add -A && git commit -m "feat(opencode): 补工具帧与耗时打点"
```

---

## Task 3 · 收口：第一批评审遗留 + 四家一致性复核

**3.1** `internal/executor/claudecode/start_ordering_test.go` 的评审遗留：
跳过 Timing 事件的 `for { select {} }` 循环把 10s 超时放在**每次迭代**里，
总等待时间因此可以远超 10s（一个不停产事件的 bug 会让它挂到 `go test` 的
全局 10 分钟超时才死，而那时报的是"panic: test timed out"，指不到这里）。
改成循环外取一次 deadline：

当前实现（`start_ordering_test.go:62-75`）：

```go
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Timing != nil {
				continue
			}
			if ev.Type != "progress" || ev.SessionID != "sess-fake" {
				t.Fatalf("首个非耗时事件应是带假执行者 session_id 的 init，实际 %+v", ev)
			}
			goto initReceived
		case <-time.After(10 * time.Second):
			t.Fatal("10s 内未收到 init progress 事件")
		}
	}
```

改成（只动两行：循环外取一次死线，`case` 换成读它）：

```go
	// 死线取一次放在循环外：放进 select 里就是**每次迭代**各 10s，
	// 一个不停产 usage 事件的回归会把本用例挂到 go test 的全局超时才死，
	// 而那时报的是 "panic: test timed out"，指不到这里。
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "usage" && ev.Timing != nil {
				continue
			}
			if ev.Type != "progress" || ev.SessionID != "sess-fake" {
				t.Fatalf("首个非耗时事件应是带假执行者 session_id 的 init，实际 %+v", ev)
			}
			goto initReceived
		case <-deadline:
			t.Fatal("10s 内未收到 init progress 事件")
		}
	}
```

上面第 59-61 行那段「死线放到 10s」的既有注释保留不动，本改动不碰时长。

**3.2** 四家一致性复核（**只读核对，不写新代码**）。逐条核，把结论写进 ledger：

| 核对项 | 命令 / 判据 |
|---|---|
| 四家都有 `reportTiming` | `grep -c "func (a \*Adapter) reportTiming" internal/executor/{claudecode,codex,grok,opencode}/adapter.go` → 四个 1 |
| 四家 `BeginTurn` 都紧跟 `frames.BeginTurn` | 逐个 `grep -n -A2 "frames.BeginTurn"` 目视 |
| 四家的回合收尾入口第一行都是 `EndTurn` | `mapResult` / `finishTurn`（codex）/ `finishTurn`（grok）/ `mapIdle` 各 `sed -n` 看头三行 |
| `turn` 包仍不认识任何具体 executor | `grep -nE "claude\|codex\|grok\|opencode" internal/executor/turn/timing.go` → **无输出**（P1 的出口闸门，第一批已过一次，本批再过一次） |

**3.3** 全包回归与格式：

```bash
gofmt -l internal/ && go vet ./internal/executor/... && go build ./... && go test ./internal/executor/... 2>&1 | tail -20
```

> `gofmt` 单列一步是因为**测试全绿 ≠ 格式干净**，而派发出去的活漏 gofmt 有前科。

**3.4** 写 ledger：`docs/superpowers/ledgers/2026-08-22-executor-timing-batch2-ledger.md`。
逐 task 记：改了哪些文件、跑了什么命令、输出是什么、遇到什么、`toolNames`
最后留没留（Task 1 (j) 的那个悬置决定）。
**没跑到结果不许写结论**；某项没验就写"未验证"，不要写"应该没问题"。

---

## 四项检查（出稿自审）

### 1. 缺陷族对抗审查

| 族 | 结论 |
|---|---|
| **生命周期 / 状态机中断** | 回合中途 agentd 重启：Segmenter 是进程内状态，重启即丢，开着的工具与未收的段**不产条目**——缺口由聚合层的 `Partial` 如实标出（`timing.go:172` 的既有设计）。幂等键全部内容派生（`turn/<turn>`、`tool/<turn>/<part>`、`api/<turn>/<n>`），重启后回合号由 `frames.Turn()` 从落盘帧恢复，键不会撞。**回合号跨重启的连续性列入真机清单**（第一批已列，本批不重复验）。孤儿资源：`toolStages`/`toolNames` 是内存 map，随运行态一起消失，无回收责任。 |
| **静默失败 / 误导报错** | 三条写路径各自的失败语义：写帧失败 → Warn 且不影响回合（与既有 text/reasoning 帧一致）；上报失败 → Debug 且**提前 return**（通道已终止时逐条重试只会刷日志）；解析失败 → 跳过且不 panic（executor 侧输出不可信，与 `feedRaw` 的既有宽容语义一致）。**没有"报成功但没做"的窗口**：本批不写库、不改状态机，只往两条既有的产出流各加一路。 |
| **跨平台假设** | 无。本批不碰路径、进程组、权限模型、webview；纯内存计算 + 既有帧文件的追加写。 |
| **假红 / 假绿测试** | ①判据不是中途副产物：验的是**落盘帧**与**通道里的条目**，两者都是终态产物。②反面断言只有两条（`TestGrokUnknownToolStatusIsNotTerminal` 的"不得产帧"、`TestOpencodeToolTextDeltaStillSkipped`），**它们是稳定假绿的温床**——补救是同一批里有正面断言（同一路径上产帧的用例）对照，一旦分流点被搬走，正面用例先红。③负载下：opencode 的 `emit` 阻塞 + 16 缓冲是**真实的死锁风险**，已在 §1.4 与 Task 2 的测试里正面处置（排空 goroutine）。④夹具里的行为假设：grok 的 `updates.jsonl` 是**手写夹具**，它编码的 `status:"completed"` 可能不是 grok 的真实取值——已显式标注并列入真机清单，代码按保守方向（不认识就不算终态）写，不靠夹具的正确性兜底。 |
| **门禁绕过** | 本批不新增写路径或执行路径，不触及权限门。工具帧记录的入参**不参与**任何审批判定（审批走 `permRequestFromToolCall`/`permission.asked`，两条独立通路）——写帧不会放行任何东西。 |

### 2. 序列化边界设问

本批的新数据字段是 `Frame.dur_ms` 与 `TimingEntry`，**两者都是第一批引入的**，
序列化链路（Go → jsonl → store → proto → TS）在第一批的契约夹具里已锁
（`internal/proto/contract_fixture_test.go`、`web/src/api/contract.test.ts`）。
本批只是新增两个**生产者**，不新增字段、不新增手写投影点。

新增的手写解析点有三处，全在**入向**（executor → handoff）：
`updateToolFields`（grok）、`mapPartUpdated` 的扩展结构体（opencode）、
`toolDetail`/`toolPartKey`。三处都有穿过真实解析的用例
（测试喂的是 JSON 原文而不是构造好的 struct），符合"穿过真实序列化边界"的要求。

**遗留风险**：grok 的 `content` 数组形状未真机确认 → 已进真机清单。

### 3. 上下文预算检查

Task 1 有界文件集：`internal/executor/grok/{adapter.go, timing_test.go}`（adapter.go 约 1100 行）。
Task 2：`internal/executor/opencode/{adapter.go, timing_test.go}`（adapter.go 约 1900 行）。
Task 3：只读核对 + 一个测试文件的局部改动。
三个 task 都圈得出有界文件集，**不需要插竖切卡**。
（`opencode/adapter.go` 1900 行接近架构法第三条的"单个可派发上下文单元"关注区，
但远未到 2~3 万行的拒发线，且本批只改其中两个函数。）

### 4. 类型标注

grok 与 opencode 都是**边界型**子系统（接缝对面是外部 executor 进程）。
机内只验契约形状（帧产没产、条目对不对），行为验收走下面的真机清单。

---

## 真机清单（**归协调者执行，不派发**）

派发出去的执行者被纪律块禁止起 executor 进程与调 handoff CLI，这些必须留本地：

1. **grok 的 `tool_call_update.status` 真实取值集合**：派一个会跑命令的小任务给
   grok，`handoff attach` 或读 `render.log` / 帧文件，确认终态字符串到底是
   `completed` 还是别的。若不是 `completed`，`toolResultStatus` 要补。
   *这条是本批最承重的未验证项*——夹具是手写的。
2. **grok 的 `tool_call_update` 带不带 `content`（工具输出）**：同一次派发里看
   tool_result 帧的 `output` 是不是空。空的话决定是补解析还是接受"grok 不带输出"。
3. **opencode 的重复 `running` 确实只产一条帧**：真机派发后数帧文件里
   `tool_call` 帧的条数与实际工具调用次数是否相等。
4. **四家的 `dur_ms` 口径可比**：同一条 `echo` 命令分别派给四家，比较帧上的
   `dur_ms` 量级。差一个数量级说明某家的打点位置错了。
5. **opencode 的审批等待确实被算进 tool 段**（§1.4 拍板的验证）：派一个需要审批的
   命令，故意等 30 秒再批，看 tool 条目的 `DurMS` 是否 ≈30s+。

---

## 自审三查

- **spec 覆盖**：breakdown 的 T4 → Task 1；T5 → Task 2；第一批评审遗留 → Task 3。
  T6/T7 显式不在本批。
- **占位符扫描**：全文无 TBD。两处刻意的"照抄既有实现"（Task 1 的 `readFrames`、
  Task 2 的 `reportTiming`）都指明了**源文件与理由**，且要求动手前先 `cat` 确认
  ——那不是占位符，是"别凭记忆抄"的纪律。Task 1 (j) 的 `toolNames` 是一个
  **显式的条件性删除决定**，判据与留痕要求都写清了。
- **跨 task 类型一致性**：两家各自的 `toolResultStatus` **同名不同包**，映射表
  刻意不同（grok 认 `failed`，opencode 认 `error`）——这是协议差异，不是重复代码，
  不要合并到 `turn` 包（合并就等于让 `turn` 包认识具体 executor，直接违反 P1 闸门）。
