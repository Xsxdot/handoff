# B74 不再假完成 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让「回合结束但模型没输出协议 trailer」不再产生 completed 事件——四个 executor 全覆盖，git 实况保留为结构化字段，工单作废理由说真话。

**Architecture:** 把四份各自漂移的兜底判定上提到 `internal/executor/turn/fallback.go` 共用一份；`executor.Result` 增加 `VoidReason` 字段、`failedPayload` 增加 `omitempty` 的 branch/commit，`handleResult` 透传而非硬编码；四个 adapter 各自换成调用共用判定，各配一个变异锚。

**Tech Stack:** Go 1.26 标准库，既有的 `internal/executor/turn` 共享包与 `internal/agentd` 状态机，测试用标准 `testing`。

## Global Constraints

- 依据 spec：`docs/superpowers/specs/2026-08-12-false-completion-and-cursor-durability-design.md` §3。
- **`!hasNew` 与 git 查询失败两条分支维持现状不动**（spec §3.1）：仍转 question。本 plan 只翻转 `hasNew` 那一条。
- **不做词法层清洗**（spec §3.4）：不清洗 `</｜｜DSML｜｜...>` 一类残留标记，不新增任何字符串过滤。
- **不动 `ParseTrailer`**（spec §9）：主档只看末行、兜底档只认 `{` 开头，两条规则都不改。
- **四处逐一变异锚**（spec §6）：每个 adapter 的接线各配一个可独立执行的变异检验，确保没有哪一处是测试覆盖不到的。
- 日志一律用各包已有的 `slog`，**禁止 `fmt.Printf` 充当日志**。
- 中文注释与日志；新文件必须有「职责 / 边界」头注释，导出函数必须有 doc 注释。
- 每个 task 结束时 `gofmt -l .` 无输出、`go vet ./...` 无输出。

### 与探针的关系

spec §3.5 的「证据层」依赖探针结论，**不在本 plan 内**。本 plan 交付 §3.1 / §3.2 / §3.3 三项，它们不依赖探针，可与 plan 探针并行推进。探针跑完若结论为「某 executor 加证据层」，届时另开一个小 plan，在本 plan 建立的 `turn.NoTrailerFailReason` 上追加内容即可——本 plan 已把文案收敛到一处，正是为此。

---

### Task 1: 把兜底判定上提到 `turn` 包

**Files:**
- Create: `internal/executor/turn/fallback.go`
- Create: `internal/executor/turn/fallback_test.go`
- Modify: `internal/executor/executor.go:72-80`（`Result` 增加 `VoidReason`，并加两个作废理由常量）

**Interfaces:**
- Consumes: `turn.TailRunes`（已有）
- Produces:
  - `func NoTrailerFailReason(branch, commit, text string) string`
  - `func NoTrailerResult(sessionID, branch, commit, text string) *executor.Result`
  - `executor.Result` 新字段 `VoidReason string`
  - 常量 `executor.VoidReasonExecutorGone` / `executor.VoidReasonTurnDiscipline`

**为什么上提而不是四处平行改**（spec §3.1 留给 plan 阶段的裁决，此处定为上提）：判定规则是协议层的事，四份副本各自漂移正是 B74 这类问题的温床。现状已经能看到漂移——opencode 的 summary 取回合末 200 字，grok/codex 取一句固定文案「（模型未输出收尾协议，按 git 新提交判定完成）」，同一个判定给审核者看的东西完全不同。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/turn/fallback_test.go`：

```go
package turn

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

func TestNoTrailerResultIsNotOK(t *testing.T) {
	r := NoTrailerResult("sess-1", "handoff/T1", "abc1234def", "干完了，已提交。")
	if r.OK {
		t.Fatal("无 trailer 的回合绝不能报 OK——这正是 B74 的假完成")
	}
}

func TestNoTrailerResultKeepsGitTruthStructured(t *testing.T) {
	r := NoTrailerResult("sess-1", "handoff/T1", "abc1234def", "干完了")
	if r.Branch != "handoff/T1" {
		t.Fatalf("branch 必须留在结构化字段里，got %q", r.Branch)
	}
	if r.CommitHash != "abc1234def" {
		t.Fatalf("commit 必须留在结构化字段里，got %q", r.CommitHash)
	}
	if r.SessionID != "sess-1" {
		t.Fatalf("sessionID 丢失，got %q", r.SessionID)
	}
}

func TestNoTrailerResultCarriesTurnDisciplineVoidReason(t *testing.T) {
	r := NoTrailerResult("s", "b", "c", "t")
	if r.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由必须说真话（executor 还活着），got %q", r.VoidReason)
	}
	if strings.Contains(r.VoidReason, "已终结") {
		t.Fatal("executor 并未终结，理由不得说它终结了")
	}
}

func TestNoTrailerFailReasonNamesAllThreeThings(t *testing.T) {
	reason := NoTrailerFailReason("handoff/T1", "abc1234def567890", "……最后这段是正文尾巴")
	// (a) 判定依据
	if !strings.Contains(reason, "未输出协议 trailer") {
		t.Errorf("缺判定依据: %s", reason)
	}
	// (b) git 实况：分支@commit
	if !strings.Contains(reason, "handoff/T1@") || !strings.Contains(reason, "abc1234") {
		t.Errorf("缺 git 实况: %s", reason)
	}
	// (c) 正文尾部片段
	if !strings.Contains(reason, "最后这段是正文尾巴") {
		t.Errorf("缺正文尾部: %s", reason)
	}
}

func TestNoTrailerFailReasonClampsLongBody(t *testing.T) {
	long := strings.Repeat("长", 5000)
	reason := NoTrailerFailReason("b", "c", long)
	if len([]rune(reason)) > 400 {
		t.Fatalf("失败原因未截断，长度 %d 符文——它会进事件 payload 与审核者视野",
			len([]rune(reason)))
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/executor/turn/ -run NoTrailer -v`
Expected: 编译失败，`NoTrailerResult` / `NoTrailerFailReason` / `executor.VoidReasonTurnDiscipline` 未定义

- [ ] **Step 3: 写实现**

创建 `internal/executor/turn/fallback.go`：

```go
// fallback.go —— 「回合结束但没有协议 trailer」时的共用裁决。
//
// 职责：
//   - 构造无 trailer 且 git 有新提交时的回合结果（一律 OK=false）
//   - 构造该结果的失败原因文案，保证判定依据 / git 实况 / 正文尾部三者齐全
//
// 边界：
//   - 不查 git（那是 GitTurnStatus），不判断 hasNew：调用方把结论传进来
//   - 不处理 !hasNew 与 git 查询失败两条分支：它们仍转 question，由各 adapter
//     自行处置（各家的空文本守卫与原生提问抑制不同，强行统一会丢掉这些差异）
//
// 为什么这段判定必须共用：四个 adapter 曾各写一份，已经漂移——opencode 的
// summary 取回合末 200 字，grok/codex 取一句固定文案，同一个判定给审核者看的
// 东西完全不同。四份副本各自漂移正是 B74 这类问题的温床。
package turn

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// shortCommitLen 是失败原因里 commit 的展示长度。
//
// 为什么截短：失败原因会整条进事件 payload 并展示给审核者，40 位全长 hash
// 挤掉的是正文尾部——而正文尾部才是审核者判断「这回合到底干到哪儿」的依据。
const shortCommitLen = 7

// noTrailerTailRunes 是失败原因里保留的正文尾部长度。
const noTrailerTailRunes = 200

// NoTrailerFailReason 构造「回合未输出协议 trailer」的失败原因文案。
//
// 参数：
//   - branch, commit: GitTurnStatus 查到的 git 实况
//   - text: 回合正文全文（本函数负责截尾）
//
// 返回：一条同时包含判定依据、git 实况、正文尾部的文案
//
// 注意：三者缺一，审核者就得回去翻日志——这条要求来自 spec §3.2，不是格式偏好。
func NoTrailerFailReason(branch, commit, text string) string {
	short := commit
	if len(short) > shortCommitLen {
		short = short[:shortCommitLen]
	}
	return fmt.Sprintf("回合结束但未输出协议 trailer；git 实况 %s@%s（相对回合起点有新提交）；回合末尾：%s",
		branch, short, TailRunes(text, noTrailerTailRunes))
}

// NoTrailerResult 构造「无 trailer 但 git 有新提交」时的回合结果。
//
// 参数：
//   - sessionID: executor 会话标识，供续接与归档
//   - branch, commit: GitTurnStatus 查到的 git 实况
//   - text: 回合正文全文
//
// 返回：OK=false 的结果，git 实况保留在结构化字段里
//
// 为什么是 OK=false 而不是 OK=true：模型没有宣布完成，handoff 不替它宣布。
// 翻转不给审核者增加任何一次操作——OK 与 !OK 都落到 waiting_review，
// 而 done 与 continue 在该状态下都合法。变的只是那条事件从「已完成，摘要如下」
// （邀请审核者不看 diff 就 done）变成「有新提交，但模型未按纪律宣布完成」
// （要求看一眼）。代价为零，收益是不再有假完成。
//
// 注意：Branch/CommitHash 必须继续填。翻转若把 git 实况降级成一段自由文本，
// 审核者与任何下游都无法再结构化地取用它。
func NoTrailerResult(sessionID, branch, commit, text string) *executor.Result {
	return &executor.Result{
		OK:         false,
		Branch:     branch,
		CommitHash: commit,
		SessionID:  sessionID,
		FailReason: NoTrailerFailReason(branch, commit, text),
		VoidReason: executor.VoidReasonTurnDiscipline,
	}
}
```

修改 `internal/executor/executor.go` 的 `Result` 与其后：

```go
// Result 是一次执行回合的终态结果（OK 或 FailReason 二选一）。
type Result struct {
	Branch     string // executor 工作分支名（如 handoff/T1）
	CommitHash string // 回合收尾 commit 的哈希
	SessionID  string // executor 会话标识（如 opencode session id），供续接与归档
	Summary    string // 执行摘要（给审核者看的完成说明）
	OK         bool   // true=正常完成；false=失败（见 FailReason）
	FailReason string // OK=false 时的失败原因/日志尾部
	// VoidReason 是本次失败导致挂起工单被作废时写进审计事件的理由。
	// 空表示沿用缺省 VoidReasonExecutorGone——绝大多数失败路径（进程退出、
	// 看门狗判死）确实是 executor 没了，不必逐个填。
	VoidReason string
}

// 挂起工单被作废时写进审计事件的理由。
//
// 为什么要区分：作废行为本身在两种情形下相同（回合已结束，挂起工单无论如何
// 都该作废），但审计记录必须说真话。历史上零文本回合那条 FailReason 明写
// 「executor 仍在线，可 continue 续接重试」，却被同一次调用标记成
// 「executor 已终结」——审计与事实互相矛盾。
const (
	// VoidReasonExecutorGone 用于 executor 真的没了：进程退出、连接终止、看门狗判死。
	VoidReasonExecutorGone = "executor 已终结"
	// VoidReasonTurnDiscipline 用于 executor 活着但没守回合纪律：无 trailer、零文本。
	VoidReasonTurnDiscipline = "回合未按纪律收尾，executor 仍在线"
)
```

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/turn/ -count=1 -v`
Expected: 五条新测试 PASS，`turn` 包既有测试全部保持 PASS

- [ ] **Step 5: 加关键节点日志**

本 task 的两个函数都是**纯函数**，按 `instrumenting-code` 的适用边界（无 I/O、无状态变更、无外部调用）不加日志——日志加在调用点，由 Task 3–6 各自负责，那里有 taskID 上下文可带。这条豁免写进 `fallback.go` 的边界注释：

```go
//   - 纯函数：不打日志。判定结果由各 adapter 在调用点记录（那里才有 taskID）
```

- [ ] **Step 6: 加注释**

- `fallback.go` 文件头：职责两条 + 边界三条（含「纯函数不打日志」）+ **为什么必须共用**（四份已漂移的实证）。
- `NoTrailerResult`：**为什么是 `OK=false`**（翻转不要钱的完整论证）+ **为什么 Branch/CommitHash 必须继续填**。
- `NoTrailerFailReason`：参数/返回 + 三者缺一的后果。
- `shortCommitLen`：为什么截短（挤掉的是正文尾部）。
- `Result.VoidReason` 字段 + 两个常量的「为什么要区分」（含历史矛盾的实证）。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/executor/... -count=1
git add internal/executor/turn/fallback.go internal/executor/turn/fallback_test.go internal/executor/executor.go
git commit -m "feat(turn): 无 trailer 回合的共用裁决，一律不宣布完成

四个 adapter 曾各写一份兜底判定并已漂移（opencode 取回合末 200 字，
grok/codex 取一句固定文案）。上提到 turn 包一份：OK=false，git 实况留在
结构化字段，失败原因同时含判定依据/git 实况/正文尾部。
Result 增加 VoidReason，让工单作废的审计理由能说真话。"
```

---

### Task 2: agentd 侧透传 git 实况与作废理由

**Files:**
- Modify: `internal/agentd/manager.go:351-354`（`failedPayload`）
- Modify: `internal/agentd/manager.go:2434-2443`（`handleResult` 的 `!OK` 分支）
- Create: `internal/agentd/handleresult_notrailer_test.go`

**Interfaces:**
- Consumes: `executor.Result.VoidReason`、`executor.VoidReasonExecutorGone`（Task 1）
- Produces:
  - `failedPayload` 新增 `Branch` / `CommitHash` 两个 `omitempty` 字段
  - `handleResult` 的 `!OK` 分支透传 branch/commit 与作废理由

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/handleresult_notrailer_test.go`。参照本包既有测试构造 `Manager` 与 `store` 的方式（`manager_test.go` 里已有 helper，复用它，不要另起一套）：

```go
package agentd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

// lastEventOfType 取任务最后一条指定类型的事件；没有则 t.Fatal。
func lastEventOfType(t *testing.T, m *Manager, taskID string, typ string) proto.Event {
	t.Helper()
	evs, err := m.st.ListEvents(taskID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if string(evs[i].Type) == typ {
			return evs[i]
		}
	}
	t.Fatalf("未找到 %s 事件，共 %d 条", typ, len(evs))
	return proto.Event{}
}

func TestFailedPayloadCarriesGitTruth(t *testing.T) {
	m, taskID := newManagerWithRunningTask(t) // 复用 manager_test.go 的既有 helper
	m.handleResult(taskID, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, Branch: "handoff/T1", CommitHash: "abc1234def",
		FailReason:  "回合结束但未输出协议 trailer；git 实况 handoff/T1@abc1234；回合末尾：干完了",
		VoidReason:  executor.VoidReasonTurnDiscipline,
	}})

	ev := lastEventOfType(t, m, taskID, string(proto.EventTypeFailed))
	var p failedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Branch != "handoff/T1" {
		t.Fatalf("branch 未透传到 failed payload，got %q", p.Branch)
	}
	if p.CommitHash != "abc1234def" {
		t.Fatalf("commit 未透传到 failed payload，got %q", p.CommitHash)
	}
}

func TestFailedPayloadOmitsGitTruthWhenAbsent(t *testing.T) {
	m, taskID := newManagerWithRunningTask(t)
	m.handleResult(taskID, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "executor 进程退出 code=1",
	}})

	ev := lastEventOfType(t, m, taskID, string(proto.EventTypeFailed))
	raw := string(ev.Payload)
	// omitempty 必须真的生效：绝大多数 failed（崩溃、看门狗判死）没有 git 实况，
	// 空字段出现在 payload 里会让下游以为「查过 git 且分支是空」
	if strings.Contains(raw, `"branch"`) || strings.Contains(raw, `"commit"`) {
		t.Fatalf("无 git 实况时不该出现 branch/commit 字段: %s", raw)
	}
}

func TestVoidReasonComesFromResultNotHardcoded(t *testing.T) {
	m, taskID := newManagerWithPendingTicket(t) // 复用既有 helper：造一张挂起工单
	m.handleResult(taskID, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "回合结束但未输出协议 trailer",
		VoidReason: executor.VoidReasonTurnDiscipline,
	}})

	ev := lastEventOfType(t, m, taskID, string(proto.EventTypeTicketsVoided))
	var p ticketsVoidedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由被硬编码覆盖，got %q", p.Reason)
	}
	if strings.Contains(p.Reason, "已终结") {
		t.Fatal("executor 还活着，审计不得记它已终结")
	}
}

func TestVoidReasonDefaultsToExecutorGone(t *testing.T) {
	m, taskID := newManagerWithPendingTicket(t)
	// 绝大多数失败路径不填 VoidReason（进程退出、看门狗判死确实是 executor 没了）
	m.handleResult(taskID, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "executor 进程退出 code=1",
	}})

	ev := lastEventOfType(t, m, taskID, string(proto.EventTypeTicketsVoided))
	var p ticketsVoidedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != executor.VoidReasonExecutorGone {
		t.Fatalf("未填时应回落到缺省理由，got %q", p.Reason)
	}
}
```

**实现前先读 `internal/agentd/manager_test.go`**，确认 `newManagerWithRunningTask` / `newManagerWithPendingTicket` 这两个 helper 的真实名字与签名；本包已有构造 Manager 的做法，**照用，不要另起一套**。若名字不同，改本测试去对齐既有 helper，而不是新写一份 setup。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/agentd/ -run 'FailedPayload|VoidReason' -v`
Expected: `TestFailedPayloadCarriesGitTruth` FAIL（`p.Branch` 为空，字段不存在）、`TestVoidReasonComesFromResult` FAIL（理由是硬编码的「executor 已终结」）

- [ ] **Step 3: 写实现**

修改 `failedPayload`（manager.go:351-354）：

```go
// failedPayload 是 failed 事件的 payload。
//
// Branch/CommitHash 为 omitempty：绝大多数 failed（executor 崩溃、看门狗判死）
// 没有 git 实况，让空字段出现在 payload 里会让下游以为「查过 git 且分支是空」。
// 只有回合纪律类失败（无 trailer 但有新提交）才带这两个字段——它们是
// 审核者判断「这回合到底干到哪儿」的唯一结构化依据，降级成 FailReason 里的
// 一段自由文本就没法再结构化取用了。
type failedPayload struct {
	FailReason string `json:"fail_reason"`
	Branch     string `json:"branch,omitempty"`
	CommitHash string `json:"commit,omitempty"`
}
```

修改 `handleResult` 的 `!OK` 分支（manager.go:2434-2443）：

```go
	} else {
		// 挂起工单一并作废（U-3）：与 RecoverOnStartup 的重启恢复路径同语义。
		// 不作废的话 attach 仍向审核者展示可操作的挂起项，而工单已无人消费——
		// 一旦 reply，工单被消耗、中继失败返回 502，任务落进不可恢复状态。
		//
		// 理由由 result 侧提供而非硬编码：作废行为在两种情形下相同，但审计
		// 必须说真话。executor 真死传 VoidReasonExecutorGone，回合纪律类失败
		// （无 trailer、零文本）传 VoidReasonTurnDiscipline——后者 executor 还活着。
		voidReason := r.VoidReason
		if voidReason == "" {
			voidReason = executor.VoidReasonExecutorGone
		}
		voidTicketsWithAudit(m.st, taskID, voidReason, m.log)
		m.log.Warn("回合以失败收尾", "task", taskID, "reason", r.FailReason,
			"branch", r.Branch, "commit", r.CommitHash, "void_reason", voidReason)
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{
			FailReason: r.FailReason, Branch: r.Branch, CommitHash: r.CommitHash,
		})
	}
```

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/agentd/ -count=1 && go test -race ./internal/agentd/`
Expected: 四条新测试 PASS，`internal/agentd` 既有测试全部保持 PASS

- [ ] **Step 5: 加关键节点日志**

- **失败收尾**：新增 `m.log.Warn("回合以失败收尾", "task", ..., "reason", ..., "branch", ..., "commit", ..., "void_reason", ...)`。这是本 task 的核心可观测点——今天 `!OK` 分支只有 `voidTicketsWithAudit` 内部那条工单日志，失败本身在 agentd.log 里没有独立一行，出问题时只能靠 executor 侧的日志倒推。
- 成功分支（`r.OK`）**保持现状**：本 plan 不动它，避免把无关改动混进来。
- 既有的「追加 result 事件失败」Error、「回迁 waiting_review 失败」Error 保持不动。

- [ ] **Step 6: 加注释**

- `failedPayload`：**为什么 omitempty 是必需的**（空字段会让下游误以为查过 git）+ 为什么这两个字段必须结构化。
- `handleResult` 的 `!OK` 分支：保留既有的 U-3 作废理由注释，新增「**理由由 result 侧提供而非硬编码**」一段，写清两种情形的分别。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/agentd/ -count=1
git add internal/agentd/manager.go internal/agentd/handleresult_notrailer_test.go
git commit -m "feat(agentd): failed 事件保留 git 实况，工单作废理由不再硬编码

failedPayload 增加 omitempty 的 branch/commit：翻转判定后 git 实况若降级成
FailReason 里的自由文本，审核者与下游就无法结构化取用。作废理由改由 result
提供并缺省回落——历史上零文本回合的 FailReason 写着「executor 仍在线」，
却被同一次调用记成「executor 已终结」，审计与事实互相矛盾。
新增一条失败收尾的 Warn：此前 !OK 分支在 agentd.log 里没有独立一行。"
```

---

### Task 3: opencode 接线与变异锚

**Files:**
- Modify: `internal/executor/opencode/adapter.go:1680-1702`（`fallbackClassify`）
- Create: `internal/executor/opencode/fallback_verdict_test.go`

**Interfaces:**
- Consumes: `turn.NoTrailerResult`（Task 1）
- Produces: opencode 的 `fallbackClassify` 在 `hasNew` 时发 `result{OK:false}`

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/opencode/fallback_verdict_test.go`。用本包既有的 adapter 测试骨架（`adapter_test.go` 里已有构造 `Adapter` + `runState` + 收集 `emit` 事件的做法，**照用**）：

```go
package opencode

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

func TestFallbackWithNewCommitDoesNotDeclareCompletion(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234def", true /*hasNew*/)
	a.fallbackClassify(r, "我把 Task 5 的三个方案列出来了，你选哪个？")

	if len(events) != 1 {
		t.Fatalf("应恰好发一条事件，got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "result" {
		t.Fatalf("有新提交时应发 result，got %q", ev.Type)
	}
	if ev.Result.OK {
		t.Fatal("无 trailer 的回合绝不能报 OK——这正是 B74 的假完成")
	}
	if ev.Result.Branch != "handoff/T1" || ev.Result.CommitHash != "abc1234def" {
		t.Fatalf("git 实况未留在结构化字段: branch=%q commit=%q",
			ev.Result.Branch, ev.Result.CommitHash)
	}
	if !strings.Contains(ev.Result.FailReason, "未输出协议 trailer") {
		t.Fatalf("失败原因缺判定依据: %s", ev.Result.FailReason)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由不对: %q", ev.Result.VoidReason)
	}
}

func TestFallbackWithoutNewCommitStillAsks(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	a.fallbackClassify(r, "A/B/C 三选一，你定？")

	if len(events) != 1 || events[0].Type != "question" {
		t.Fatalf("无新提交时应转 question（本 plan 不动这条分支），got %+v", events)
	}
}

func TestFallbackWithGitErrorStillAsks(t *testing.T) {
	a, r, events := newAdapterWithGitError(t)
	a.fallbackClassify(r, "有个问题要确认")

	if len(events) != 1 || events[0].Type != "question" {
		t.Fatalf("git 查询失败时应转 question（本 plan 不动这条分支），got %+v", events)
	}
}
```

**`newAdapterWithFakeGit` / `newAdapterWithGitError` 的实现方式**：`fallbackClassify` 走 `a.gitTurnStatus(r)`（adapter.go:1708 的薄封装）。最省事的做法是**造一个真实的临时 git 仓库**——`t.TempDir()` + `git init` + 一次初始提交作为 `r.startCommit`，`hasNew=true` 时再补一次提交。本包已有测试若已经这么做，直接复用它的 helper；若没有，把这两个 helper 写在本文件里并加注释说明为什么用真仓库（`GitTurnStatus` 是真跑 `git` 子进程，桩不了）。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/executor/opencode/ -run Fallback -v`
Expected: `TestFallbackWithNewCommitDoesNotDeclareCompletion` FAIL（`ev.Result.OK` 为 true）；另外两条 PASS（那两条分支本来就对，它们在这里是**回归护栏**）

- [ ] **Step 3: 写实现**

替换 `internal/executor/opencode/adapter.go` 的 `fallbackClassify`：

```go
// fallbackClassify 是「模型未按纪律输出协议 trailer」的兜底分类。
//
// why（兜底分类规则）：回合结束但 turn.ParseTrailer 判 none。此时拿 git 实况裁决：
//   - 相对回合起点有新 commit → 发 result{OK:false}（B74）。**不替模型宣布完成**：
//     模型没说完成，handoff 就不说。翻转不给审核者加任何一次操作——OK 与 !OK
//     都落 waiting_review，done 与 continue 在那里都合法；变的只是事件从
//     「已完成，摘要如下」变成「有新提交，但模型未按纪律宣布完成」。
//   - 没有新 commit / git 查询失败 → 把回合全文交审核者裁决（question），流程不卡死。
func (a *Adapter) fallbackClassify(r *runState, text string) {
	a.log.Warn("回合未输出协议 trailer，走 git 兜底", "task", r.taskID,
		"turn_tail", turn.TailRunes(text, 120))
	branch, commit, hasNew, err := a.gitTurnStatus(r)
	if err != nil || !hasNew {
		if err != nil {
			a.log.Error("git 兜底查询失败", "task", r.taskID, "cause", err)
		}
		a.log.Info("兜底判定无新提交，转提问交审核者裁决", "task", r.taskID, "has_new", hasNew)
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
		return
	}
	a.log.Warn("兜底判定有新提交，但模型未宣布完成，转失败交审核者裁决",
		"task", r.taskID, "branch", branch, "commit", commit)
	a.emit(r, executor.AdapterEvent{Type: "result",
		Result: turn.NoTrailerResult(r.session, branch, commit, text)})
}
```

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/opencode/ -count=1 && go test -race ./internal/executor/opencode/`
Expected: 三条新测试 PASS；本包既有测试全部保持 PASS（**若有既有测试断言这条路径产出 `OK:true`，那正是被修掉的行为——改断言，并在 commit message 里点名**）

- [ ] **Step 5: 变异锚**

手工执行，验证测试真的守住了这条：

```bash
cd internal/executor/opencode && cp adapter.go /tmp/adapter.go.bak
```

把 `turn.NoTrailerResult(...)` 那行临时改回：

```go
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: true, Branch: branch, CommitHash: commit,
		Summary: turn.TailRunes(text, 200), SessionID: r.session,
	}})
```

```bash
go test ./internal/executor/opencode/ -run Fallback
```

Expected: **FAIL**（`TestFallbackWithNewCommitDoesNotDeclareCompletion` 变红）。确认变红后还原：

```bash
cp /tmp/adapter.go.bak internal/executor/opencode/adapter.go && go test ./internal/executor/opencode/ -run Fallback
```

Expected: PASS。

**变异不变红就是测试没覆盖到**，此时停下来修测试，不要继续往下做。

- [ ] **Step 6: 加关键节点日志**

- **有新提交但模型未宣布完成**：新增 `a.log.Warn("兜底判定有新提交，但模型未宣布完成，转失败交审核者裁决", "task", ..., "branch", ..., "commit", ...)`。今天这条路径**在 agentd 侧完全静默**——`fallbackClassify` 只在入口打一条 Warn，走到宣布完成时不再有日志，spec §2.3 那 59 次只能靠两条日志做减法才数得出来。补上这条之后可以直接 grep。
- 入口的「回合未输出协议 trailer，走 git 兜底」Warn 保持不动（它是 §2.3 那次频率实测的依据，改文案会让历史日志对不上）。
- 「兜底判定无新提交」Info、「git 兜底查询失败」Error 保持不动。

- [ ] **Step 7: 加注释**

- `fallbackClassify` 的 doc 注释重写：两条分支分列，`hasNew` 那条写清**为什么不替模型宣布完成**以及**为什么翻转不要钱**。
- 保留原注释里仍然成立的部分（「流程不卡死」）。删掉已不成立的部分（「认定干完了（result OK…）」）——留着比没有更糟。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/executor/opencode/ -count=1
git add internal/executor/opencode/
git commit -m "fix(opencode): 无 trailer 但有新提交时不再宣布完成（B74）

改用 turn.NoTrailerResult：OK=false，git 实况留结构化字段。翻转不给审核者
加任何一次操作——两种结果都落 waiting_review，done 与 continue 都合法；
变的只是事件从「已完成，摘要如下」（邀请不看 diff 就 done）变成「有新提交，
但模型未按纪律宣布完成」（要求看一眼）。
补一条 Warn：此前这条路径在 agentd 侧完全静默，只能靠两条日志做减法数出来。
变异锚：把判定改回 OK:true，TestFallbackWithNewCommitDoesNotDeclareCompletion 变红。"
```

---

### Task 4: claudecode 接线与变异锚

**Files:**
- Modify: `internal/executor/claudecode/adapter.go:649-680`（`fallbackClassify`）
- Create: `internal/executor/claudecode/fallback_verdict_test.go`

**Interfaces:**
- Consumes: `turn.NoTrailerResult`（Task 1）
- Produces: claudecode 的 `fallbackClassify` 在 `hasNew` 时发 `result{OK:false}`

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/claudecode/fallback_verdict_test.go`。**与 Task 3 的三条同构，但必须逐字重写**（claudecode 的 `fallbackClassify` 多一条零文本守卫，且它在 `!hasNew` 分支内部），并**多一条**守住零文本守卫不被破坏：

```go
package claudecode

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

func TestFallbackWithNewCommitDoesNotDeclareCompletion(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234def", true /*hasNew*/)
	a.fallbackClassify(r, "我把 Task 5 的三个方案列出来了，你选哪个？")

	if len(events) != 1 {
		t.Fatalf("应恰好发一条事件，got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "result" {
		t.Fatalf("有新提交时应发 result，got %q", ev.Type)
	}
	if ev.Result.OK {
		t.Fatal("无 trailer 的回合绝不能报 OK——这正是 B74 的假完成")
	}
	if ev.Result.Branch != "handoff/T1" || ev.Result.CommitHash != "abc1234def" {
		t.Fatalf("git 实况未留在结构化字段: branch=%q commit=%q",
			ev.Result.Branch, ev.Result.CommitHash)
	}
	if !strings.Contains(ev.Result.FailReason, "未输出协议 trailer") {
		t.Fatalf("失败原因缺判定依据: %s", ev.Result.FailReason)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由不对: %q", ev.Result.VoidReason)
	}
}

func TestFallbackWithoutNewCommitStillAsks(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	a.fallbackClassify(r, "A/B/C 三选一，你定？")

	if len(events) != 1 || events[0].Type != "question" {
		t.Fatalf("无新提交时应转 question（本 plan 不动这条分支），got %+v", events)
	}
}

func TestFallbackZeroTextGuardStillFires(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	a.fallbackClassify(r, "   \n  ")

	if len(events) != 1 || events[0].Type != "result" {
		t.Fatalf("零文本应发 result 而非空工单，got %+v", events)
	}
	if events[0].Result.OK {
		t.Fatal("零文本是故障报告")
	}
	// 零文本守卫的 FailReason 明写 executor 仍在线——作废理由必须与它一致
	if events[0].Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("零文本时 executor 还活着，作废理由应为纪律类，got %q",
			events[0].Result.VoidReason)
	}
}

func TestFallbackWithGitErrorStillAsks(t *testing.T) {
	a, r, events := newAdapterWithGitError(t)
	a.fallbackClassify(r, "有个问题要确认")

	if len(events) != 1 || events[0].Type != "question" {
		t.Fatalf("git 查询失败时应转 question（本 plan 不动这条分支），got %+v", events)
	}
}
```

helper 的构造方式同 Task 3：`GitTurnStatus` 真跑 `git` 子进程，桩不了，用 `t.TempDir()` 里的真实临时仓库。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/executor/claudecode/ -run Fallback -v`
Expected: `TestFallbackWithNewCommitDoesNotDeclareCompletion` FAIL（`OK` 为 true）、`TestFallbackZeroTextGuardStillFires` FAIL（`VoidReason` 为空）

- [ ] **Step 3: 写实现**

替换 `internal/executor/claudecode/adapter.go` 的 `fallbackClassify` 尾部两处：

```go
		// 空文本守卫：文本为空时 question 产出的是一张空工单，审核者收到一个
		// 没有内容的问题。零文本是故障报告，不是问题（与 opencode mapIdle、
		// grok finishTurn 的空回合处置对称）
		if strings.TrimSpace(text) == "" {
			a.log.Warn("回合零文本且无新提交，转失败结果交审核者", "task", r.taskID)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.session,
				Result: &executor.Result{OK: false, SessionID: r.session,
					FailReason: "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试",
					// 与上一行的 FailReason 保持一致：executor 还活着，
					// 审计不得记它已终结（spec §3.3）
					VoidReason: executor.VoidReasonTurnDiscipline}})
			return
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
		return
	}
	a.log.Warn("兜底判定有新提交，但模型未宣布完成，转失败交审核者裁决",
		"task", r.taskID, "branch", branch, "commit", commit)
	a.emit(r, executor.AdapterEvent{Type: "result",
		Result: turn.NoTrailerResult(r.session, branch, commit, text)})
}
```

并把该函数的 doc 注释里「认定干完了（result OK…）」改写成与 opencode 一致的两条分支说明。

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/claudecode/ -count=1`
Expected: 四条新测试 PASS；本包既有测试全部保持 PASS

- [ ] **Step 5: 变异锚**

```bash
cp internal/executor/claudecode/adapter.go /tmp/cc-adapter.go.bak
```

把 `turn.NoTrailerResult(...)` 那行临时改回：

```go
	a.emit(r, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: true, Branch: branch, CommitHash: commit,
		Summary: turn.TailRunes(text, 200), SessionID: r.session,
	}})
```

```bash
go test ./internal/executor/claudecode/ -run Fallback
```

Expected: **FAIL**。还原后重跑：

```bash
cp /tmp/cc-adapter.go.bak internal/executor/claudecode/adapter.go && go test ./internal/executor/claudecode/ -run Fallback
```

Expected: PASS。变异不变红就停下来修测试。

- [ ] **Step 6: 加关键节点日志**

- 新增「兜底判定有新提交，但模型未宣布完成」Warn，带 branch/commit（理由同 Task 3：这条路径今天在 agentd 侧静默）。
- 零文本守卫的既有 Warn 保持不动。
- 入口 Warn、「兜底判定无新提交」Info、「git 兜底查询失败」Error 全部保持不动。

- [ ] **Step 7: 加注释**

- `fallbackClassify` doc 注释重写为两条分支分列，与 opencode 表述一致。
- 零文本守卫新增的 `VoidReason` 那行加注释：**为什么它必须与上一行的 FailReason 一致**（spec §3.3 点名的那处矛盾）。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/executor/claudecode/ -count=1
git add internal/executor/claudecode/
git commit -m "fix(claudecode): 无 trailer 但有新提交时不再宣布完成（B74）

同 opencode 换用 turn.NoTrailerResult。零文本守卫补上 VoidReason：它的
FailReason 明写「executor 仍在线，可 continue 续接重试」，此前却被记成
「executor 已终结」——spec §3.3 点名的那处矛盾。
变异锚：改回 OK:true 后 TestFallbackWithNewCommitDoesNotDeclareCompletion 变红。"
```

---

### Task 5: grok 接线与变异锚

**Files:**
- Modify: `internal/executor/grok/adapter.go:478-500`（`default:` 分支）
- Create: `internal/executor/grok/fallback_verdict_test.go`

**Interfaces:**
- Consumes: `turn.NoTrailerResult`（Task 1）
- Produces: grok 的 `default:` 分支在 `hasNew` 时发 `result{OK:false}`

**与前两个的形状差异**：grok 的兜底不是一个独立函数，而是 `finishTurn` 里 `switch kind` 的 `default:` 分支；它在 `hasNew` 之后还有 `askedViaTool` 抑制与零文本守卫两道闸。本 task 只改 `hasNew` 那一段，另外两道**原样保留**。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/grok/fallback_verdict_test.go`。因为兜底嵌在 `finishTurn` 里，测试走 `finishTurn`（本包 `adapter_test.go` 已有驱动它的做法，**照用**）：

```go
package grok

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

func TestNoTrailerWithNewCommitDoesNotDeclareCompletion(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234def", true /*hasNew*/)
	r.appendTurnText("我把 Task 5 的三个方案列出来了，你选哪个？")
	a.finishTurn(r, okOutcome())

	ev := lastEvent(t, events)
	if ev.Type != "result" {
		t.Fatalf("有新提交时应发 result，got %q", ev.Type)
	}
	if ev.Result.OK {
		t.Fatal("无 trailer 的回合绝不能报 OK——这正是 B74 的假完成")
	}
	if ev.Result.Branch != "handoff/T1" || ev.Result.CommitHash != "abc1234def" {
		t.Fatalf("git 实况未留在结构化字段: branch=%q commit=%q",
			ev.Result.Branch, ev.Result.CommitHash)
	}
	if !strings.Contains(ev.Result.FailReason, "未输出协议 trailer") {
		t.Fatalf("失败原因缺判定依据: %s", ev.Result.FailReason)
	}
	// 旧实现那句固定文案必须消失：它把 git 实况说成了完成的依据
	if strings.Contains(ev.Result.Summary, "按 git 新提交判定完成") {
		t.Fatalf("旧固定文案仍在: %q", ev.Result.Summary)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由不对: %q", ev.Result.VoidReason)
	}
}

func TestNoTrailerAskedViaToolStillSuppresses(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	r.appendTurnText("已调用一次提问工具；本回合结束。")
	r.markAskedViaTool()
	a.finishTurn(r, okOutcome())

	// 本 plan 不动这条：已走工具提问时兜底闭嘴，不补第二张工单
	for _, ev := range *events {
		if ev.Type == "question" {
			t.Fatalf("已走工具提问时不该补工单，却发了 %+v", ev)
		}
	}
}

func TestNoTrailerZeroTextStillFailsWithLiveExecutor(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	r.appendTurnText("   \n ")
	a.finishTurn(r, okOutcome())

	ev := lastEvent(t, events)
	if ev.Type != "result" || ev.Result.OK {
		t.Fatalf("零文本应发失败 result，got %+v", ev)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("零文本时 executor 还活着，作废理由应为纪律类，got %q",
			ev.Result.VoidReason)
	}
}

func TestNoTrailerWithoutNewCommitStillAsks(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	r.appendTurnText("A/B/C 三选一，你定？")
	a.finishTurn(r, okOutcome())

	if ev := lastEvent(t, events); ev.Type != "question" {
		t.Fatalf("无新提交时应转 question（本 plan 不动这条分支），got %q", ev.Type)
	}
}
```

**helper 说明**：`appendTurnText` / `markAskedViaTool` / `okOutcome` / `lastEvent` 是示意名。**实现前先读 `internal/executor/grok/adapter_test.go`**，用本包既有的真实名字与驱动方式改写本测试；`r.turnTextAndReset()` 与 `r.takeAskedViaTool()` 的写入侧在 `runState` 上，从既有测试里找它们怎么被喂进去。`okOutcome()` 需构造 `StopReason == "end_turn"` 的 outcome，否则会走更早的 `回合非正常收尾` 分支。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/executor/grok/ -run NoTrailer -v`
Expected: `TestNoTrailerWithNewCommitDoesNotDeclareCompletion` FAIL、`TestNoTrailerZeroTextStillFailsWithLiveExecutor` FAIL（`VoidReason` 为空）

- [ ] **Step 3: 写实现**

替换 `internal/executor/grok/adapter.go` `default:` 分支里的 `if hasNew` 段：

```go
	default:
		// 兜底：模型没守收尾纪律。唯一可信的是 git 实况——但**有新提交不等于
		// 干完了**，只等于「这回合动过代码」。模型没宣布完成，handoff 就不替它
		// 宣布（B74）：发 result{OK:false}，git 实况留在结构化字段，
		// 审核者在 waiting_review 里看一眼再决定 done 还是 continue。
		if hasNew {
			a.log.Warn("回合无收尾协议但有新提交，转失败交审核者裁决",
				"task", r.taskID, "branch", branch, "commit", commit)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
				Result: turn.NoTrailerResult(r.sessionID, branch, commit, text)})
			return
		}
```

零文本守卫那段（`default:` 分支末尾）补 `VoidReason`：

```go
		if strings.TrimSpace(text) == "" {
			a.log.Warn("回合零文本且无新提交，转失败结果交审核者", "task", r.taskID)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
				Result: &executor.Result{OK: false, SessionID: r.sessionID,
					FailReason: "回合结束但零文本产出；executor 仍在线，可 continue 续接重试",
					// 与上一行的 FailReason 保持一致：executor 还活着（spec §3.3）
					VoidReason: executor.VoidReasonTurnDiscipline}})
			return
		}
```

`askedViaTool` 抑制段**一行不动**。

同时检查本文件里其它 `emitFailed` 调用（如 `回合非正常收尾 stopReason=`）——那些是 executor 侧真出了问题但进程可能还在，**本 plan 不动它们**，它们走缺省的 `VoidReasonExecutorGone`。若发现某一处明显该是纪律类，记进 backlog 而不是顺手改。

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/grok/ -count=1`
Expected: 四条新测试 PASS；本包既有测试全部保持 PASS

- [ ] **Step 5: 变异锚**

```bash
cp internal/executor/grok/adapter.go /tmp/grok-adapter.go.bak
```

把 `turn.NoTrailerResult(...)` 那行临时改回：

```go
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
				Result: &executor.Result{OK: true, Branch: branch, CommitHash: commit,
					SessionID: r.sessionID, Summary: "（模型未输出收尾协议，按 git 新提交判定完成）"}})
```

```bash
go test ./internal/executor/grok/ -run NoTrailer
```

Expected: **FAIL**。还原后重跑：

```bash
cp /tmp/grok-adapter.go.bak internal/executor/grok/adapter.go && go test ./internal/executor/grok/ -run NoTrailer
```

Expected: PASS。变异不变红就停下来修测试。

- [ ] **Step 6: 加关键节点日志**

- 新增「回合无收尾协议但有新提交，转失败交审核者裁决」Warn，带 branch/commit。
- 既有的「grok 回合收尾」Info（带 kind / has_new_commit / branch / asked_via_tool）保持不动——它已经是本 executor 最好的观测点。
- 「已走工具提问，兜底不再补工单」Info、零文本 Warn、「git 回合取证失败」Warn 全部保持不动。

- [ ] **Step 7: 加注释**

- `default:` 分支的注释重写：原文写「绝不替模型宣布完成」，紧接着的 `if hasNew` 却正是在替模型宣布完成——**注释与代码互相矛盾，这本身就是四份副本漂移的证据**。新注释必须写清「有新提交不等于干完了，只等于这回合动过代码」。
- 零文本守卫新增的 `VoidReason` 加一行 why。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/executor/grok/ -count=1
git add internal/executor/grok/
git commit -m "fix(grok): 无 trailer 但有新提交时不再宣布完成（B74）

原注释写着「绝不替模型宣布完成」，紧接着的 if hasNew 正是在替模型宣布完成——
注释与代码互相矛盾，本身就是四份副本漂移的证据。换用 turn.NoTrailerResult，
那句「（模型未输出收尾协议，按 git 新提交判定完成）」的固定文案随之消失。
零文本守卫补 VoidReason。askedViaTool 抑制一行不动。
变异锚：改回 OK:true 后 TestNoTrailerWithNewCommitDoesNotDeclareCompletion 变红。"
```

---

### Task 6: codex 接线与变异锚

**Files:**
- Modify: `internal/executor/codex/adapter.go:608-630`（`default:` 分支）
- Create: `internal/executor/codex/fallback_verdict_test.go`

**Interfaces:**
- Consumes: `turn.NoTrailerResult`（Task 1）
- Produces: codex 的 `default:` 分支在 `hasNew` 时发 `result{OK:false}`

**与 grok 同形**（同样嵌在 `switch kind` 的 `default:` 里，同样有 `askedViaTool` 抑制与零文本守卫），差别只在会话字段名是 `r.threadID` 而非 `r.sessionID`。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/codex/fallback_verdict_test.go`。**逐字重写，不要写「同 Task 5」**：

```go
package codex

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

func TestNoTrailerWithNewCommitDoesNotDeclareCompletion(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234def", true /*hasNew*/)
	r.appendTurnText("我把 Task 5 的三个方案列出来了，你选哪个？")
	a.finishTurn(r, okOutcome())

	ev := lastEvent(t, events)
	if ev.Type != "result" {
		t.Fatalf("有新提交时应发 result，got %q", ev.Type)
	}
	if ev.Result.OK {
		t.Fatal("无 trailer 的回合绝不能报 OK——这正是 B74 的假完成")
	}
	if ev.Result.Branch != "handoff/T1" || ev.Result.CommitHash != "abc1234def" {
		t.Fatalf("git 实况未留在结构化字段: branch=%q commit=%q",
			ev.Result.Branch, ev.Result.CommitHash)
	}
	if !strings.Contains(ev.Result.FailReason, "未输出协议 trailer") {
		t.Fatalf("失败原因缺判定依据: %s", ev.Result.FailReason)
	}
	if strings.Contains(ev.Result.Summary, "按 git 新提交判定完成") {
		t.Fatalf("旧固定文案仍在: %q", ev.Result.Summary)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由不对: %q", ev.Result.VoidReason)
	}
	// codex 的会话标识是 threadID，接线时最容易漏
	if ev.Result.SessionID == "" {
		t.Fatal("SessionID 丢失：codex 侧应传 r.threadID")
	}
}

func TestNoTrailerAskedViaToolStillSuppresses(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	r.appendTurnText("已调用一次提问工具；本回合结束。")
	r.markAskedViaTool()
	a.finishTurn(r, okOutcome())

	for _, ev := range *events {
		if ev.Type == "question" {
			t.Fatalf("已走工具提问时不该补工单，却发了 %+v", ev)
		}
	}
}

func TestNoTrailerZeroTextStillFailsWithLiveExecutor(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	r.appendTurnText("  \n ")
	a.finishTurn(r, okOutcome())

	ev := lastEvent(t, events)
	if ev.Type != "result" || ev.Result.OK {
		t.Fatalf("零文本应发失败 result，got %+v", ev)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("零文本时 executor 还活着，作废理由应为纪律类，got %q",
			ev.Result.VoidReason)
	}
}

func TestNoTrailerWithoutNewCommitStillAsks(t *testing.T) {
	a, r, events := newAdapterWithFakeGit(t, "handoff/T1", "abc1234", false /*hasNew*/)
	r.appendTurnText("A/B/C 三选一，你定？")
	a.finishTurn(r, okOutcome())

	if ev := lastEvent(t, events); ev.Type != "question" {
		t.Fatalf("无新提交时应转 question（本 plan 不动这条分支），got %q", ev.Type)
	}
}
```

helper 名同样是示意——**实现前先读 `internal/executor/codex/adapter_test.go`**，用本包既有的真实名字改写。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/executor/codex/ -run NoTrailer -v`
Expected: `TestNoTrailerWithNewCommitDoesNotDeclareCompletion` FAIL、`TestNoTrailerZeroTextStillFailsWithLiveExecutor` FAIL

- [ ] **Step 3: 写实现**

替换 `internal/executor/codex/adapter.go` `default:` 分支里的 `if hasNew` 段：

```go
	default:
		// 兜底：模型没守收尾纪律。唯一可信的是 git 实况——但**有新提交不等于
		// 干完了**，只等于「这回合动过代码」。模型没宣布完成，handoff 就不替它
		// 宣布（B74）：发 result{OK:false}，git 实况留在结构化字段，
		// 审核者在 waiting_review 里看一眼再决定 done 还是 continue。
		if hasNew {
			a.log.Warn("回合无收尾协议但有新提交，转失败交审核者裁决",
				"task", r.taskID, "branch", branch, "commit", commit)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
				Result: turn.NoTrailerResult(r.threadID, branch, commit, text)})
			return
		}
```

零文本守卫补 `VoidReason`：

```go
		if strings.TrimSpace(text) == "" {
			a.log.Warn("回合零文本且无新提交，转失败结果交审核者", "task", r.taskID)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
				Result: &executor.Result{OK: false, SessionID: r.threadID,
					FailReason: "回合结束但零文本产出；executor 仍在线，可 continue 续接重试",
					// 与上一行的 FailReason 保持一致：executor 还活着（spec §3.3）
					VoidReason: executor.VoidReasonTurnDiscipline}})
			return
		}
```

`askedViaTool` 抑制段**一行不动**。

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/codex/ -count=1`
Expected: 四条新测试 PASS；本包既有测试全部保持 PASS

- [ ] **Step 5: 变异锚**

```bash
cp internal/executor/codex/adapter.go /tmp/codex-adapter.go.bak
```

把 `turn.NoTrailerResult(...)` 那行临时改回：

```go
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
				Result: &executor.Result{OK: true, Branch: branch, CommitHash: commit,
					SessionID: r.threadID, Summary: "（模型未输出收尾协议，按 git 新提交判定完成）"}})
```

```bash
go test ./internal/executor/codex/ -run NoTrailer
```

Expected: **FAIL**。还原后重跑：

```bash
cp /tmp/codex-adapter.go.bak internal/executor/codex/adapter.go && go test ./internal/executor/codex/ -run NoTrailer
```

Expected: PASS。变异不变红就停下来修测试。

- [ ] **Step 6: 加关键节点日志**

- 新增「回合无收尾协议但有新提交，转失败交审核者裁决」Warn，带 branch/commit。
- 既有的「codex 回合收尾」Info 保持不动。
- 「已走工具提问，兜底不再补工单」Info、零文本 Warn、「git 回合取证失败」Warn 全部保持不动。

- [ ] **Step 7: 加注释**

同 Task 5：`default:` 分支注释重写（原文同样是「绝不替模型宣布完成」紧跟一段替模型宣布完成的代码），零文本守卫的 `VoidReason` 加一行 why。

- [ ] **Step 8: 全量回归并提交**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -count=1
go test -race ./internal/agentd/ ./internal/executor/opencode/ ./internal/executor/claudecode/ ./internal/executor/grok/ ./internal/executor/codex/ ./internal/client/ ./cmd/
GOOS=windows GOARCH=amd64 go build ./...
```

四处变异锚**连做一遍**（四个 executor 各变异一次，各自变红后还原），确认没有哪一处是测试覆盖不到的。

```bash
git add internal/executor/codex/
git commit -m "fix(codex): 无 trailer 但有新提交时不再宣布完成（B74）

四个 executor 的最后一个。同 grok：注释与代码互相矛盾，换用
turn.NoTrailerResult，零文本守卫补 VoidReason，askedViaTool 抑制一行不动。
会话标识是 threadID 不是 sessionID，测试单独守住这一点。
四处变异锚全部验过：各自改回 OK:true 后对应测试变红，还原后变绿。"
```

- [ ] **Step 9: instrumenting-code 终检**

逐项过，任一不过回去修：

- [ ] 每个错误分支带上下文日志（四个 adapter 的 git 查询失败、`handleResult` 的三处 Error）
- [ ] 成功路径不静默（`handleResult` 的 `!OK` 分支新增了 Warn；四个 adapter 的兜底判定新增了 Warn）
- [ ] 无 `fmt.Printf` 充当日志：`grep -rn 'fmt.Print' internal/executor/ internal/agentd/ | grep -v _test` 无命中
- [ ] 新文件有头注释：`internal/executor/turn/fallback.go`
- [ ] 导出函数有 doc 注释：`NoTrailerResult`、`NoTrailerFailReason`、两个 VoidReason 常量
- [ ] 非显然分支有 why 注释：四处 `hasNew` 的翻转理由、`omitempty` 的必要性、`VoidReason` 缺省回落

- [ ] **Step 10: 更新 backlog**

把 `docs/superpowers/backlog.md` 的 B74 行从 `📋 specced` 推到 `✅ done(已验)`，`验收` 列填实际测试命令与通过数（如 `go test ./... (ok, N packages)`；四处变异锚已验），`原型/流程图` 为 `—` 故自动免除对照。

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B74 完成（四 executor 假完成翻转 + git 实况结构化 + 作废理由诚实）"
```

---

## Self-Review

**1. Spec coverage**

| spec 条目 | 落在哪 |
|---|---|
| §3.1 `hasNew` 翻转为 `OK:false` | Task 1（共用判定）+ Task 3/4/5/6（四处接线） |
| §3.1 `!hasNew` 与 git 出错维持现状 | Global Constraints + 四个 task 各一条回归护栏测试 |
| §3.1 四 executor 全覆盖（位置表） | Task 3/4/5/6 逐一对应 spec 的位置表 |
| §3.1 判定上提到 `turn` 共用（plan 阶段裁决） | Task 1，理由写在「为什么上提」小节 |
| §3.2 `failedPayload` 加 omitempty 的 branch/commit | Task 2 |
| §3.2 `FailReason` 三要素（依据/git 实况/正文尾） | Task 1 `NoTrailerFailReason` + `TestNoTrailerFailReasonNamesAllThreeThings` |
| §3.3 作废理由由 result 提供、透传 | Task 2 + 四处零文本守卫补 `VoidReason` |
| §3.4 不做词法清洗 | Global Constraints（全 plan 无任何字符串过滤） |
| §3.5 证据层 | **明确不在本 plan 内**，理由与后续接入点写在「与探针的关系」 |
| §6 四处变异锚 | Task 3/4/5/6 各一个 Step 5，Task 6 Step 8 连做一遍 |
| §6 `handleResult` 的 `!OK` 断言 | Task 2 的四条测试 |
| §6 全量回归命令 | Task 6 Step 8 |
| §8 验收 #1/#2/#3 | Task 3–6 / Task 2 / Task 2 |

**2. Placeholder scan**

无 TBD/TODO。三处「先读既有测试确认 helper 真名」（Task 2 Step 1、Task 5 Step 1、Task 6 Step 1）不是占位符而是**明确的指令**：本仓库四个 executor 包各有自己的测试骨架，凭空写一套新 setup 会与既有测试重复且更易错，所以要求对齐既有 helper，并写明了对齐的方向（改测试去就 helper，不是新写 setup）。四个 executor 的测试**逐字重写**而非「同 Task N」，因为 subagent 可能乱序读到。

每个实现类 task（1–6）都有「加关键节点日志」与「加注释」两个 step。Task 1 的日志 step 是**显式豁免**（纯函数），豁免理由与替代安排（日志加在调用点）写在 step 内，不是省略。

**3. Type consistency**

- `turn.NoTrailerResult(sessionID, branch, commit, text)` 的四参数顺序在 Task 1 定义，Task 3/4/5/6 的调用逐一核对：opencode/claudecode 传 `r.session`，grok 传 `r.sessionID`，codex 传 `r.threadID`——三种字段名来自各包既有代码，已逐一核实。
- `executor.VoidReasonTurnDiscipline` / `VoidReasonExecutorGone` 在 Task 1 定义，Task 2 与四处零文本守卫引用一致。
- `Result.VoidReason` 字段名在 Task 1 定义，Task 2 读作 `r.VoidReason`，一致。
- `failedPayload.Branch` / `.CommitHash` 的 JSON tag（`branch,omitempty` / `commit,omitempty`）与 `completedPayload` 的 tag（`branch` / `commit`）同名，下游按同一键取用——这是有意的，写在 Task 2 的注释里。
