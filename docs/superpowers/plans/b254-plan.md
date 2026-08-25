# B254 实现计划：`WorkBranch` 跳过零产出失败轮

## 0. 输入、边界与已验证基线

规格：`docs/superpowers/specs/2026-08-25-b254-workbranch-skip-empty-rounds.md`。

本卡只改 `d_ledger` 内的账本判定及其 `d_ledgerstep` 调用方文案，不改 agentd、client、
wire、`card dispatch` flag 集或执行机 git。唯一声明接缝是：

> `internal/ledger/events.go#Store.WorkBranch`；调用方为
> `internal/ledgerstep/dispatch.go` 的跨目标机闸与合并节点的工作分支读取。

当前分支/起点已在台账记录；代码图存在但 `Store.WorkBranch` 不在 baseline 图节点中，
`graph context d_ledger` 已真实成功，`graph chain/sym/who-calls` 对该符号均真实报错
未命中。本计划把该符号列为图覆盖债，不把图未命中误写成“没有调用方”。

### 0.1 已在基线跑过的判据

动手前已真实执行：

```sh
go test ./internal/ledger ./internal/ledgerstep -run 'TestWorkBranch|TestViaTemplateSecondRoundGetsNumberedBranch|TestViaTemplateContinuationUsesLocalWorkBranch|TestViaTemplateRejectsCrossTargetBeforeTransport|TestViaTemplateNodePurposeTakesReviewPath' -count=1 -v
```

真实结果为退出码 0；`internal/ledger` 输出 `--- PASS: TestWorkBranchSkipsReviewRounds`，
包结果为 `ok github.com/Xsxdot/handoff/internal/ledger 0.142s`；
`internal/ledgerstep` 的四个既有 `ViaTemplate` 用例均 `PASS`，包结果为
`ok github.com/Xsxdot/handoff/internal/ledgerstep 0.326s`。台账保存了命令和原始结果尾部。

这条命令是本卡实现前的行为基线，不是实现后的完成结论。实现后必须重新执行各 task
的最小命令，再由 Task 3 执行合并门禁。

### 0.2 账本事实与本卡判定口径

现有生产事实已经足够，不新增事件字段：

1. `DispatchSnapshot` 的 `TaskID`、`Branch`、`Purpose`、`Target` 来自
   `EvDispatched` payload。
2. `card_tasks` 的 `TaskLink` 是老快照 `Purpose` 缺省时的 task→purpose 回落表。
3. `AppendMirroredEvent` 将执行机原始事件的 `task_type` 和原始 `payload` 写入协调者
   `EvTaskMirrored`；因此 `WorkBranch` 可以只读协调者账本，不拨号、不运行 git。
4. 账本现有回放口径把 `failed`/`archived` 当作 task 收口，把 `completed`/
   `turn_failed` 当作仍待裁决的 waiting_review；本卡沿用此口径。
5. `completed`、`turn_failed`、带快照的 `failed` payload 都可能含
   `branch`/`commit`；`archived` payload 不含提交信息，所以归档前历史事件必须一起回放。

资格判定写成一个纯账本回放规则：对一条非审阅 dispatched 快照，只有同时满足以下条件
才会把它当作工作分支指针：

- `TaskID` 能在 `EvTaskMirrored.source_task` 中关联到完整回放；
- 该 task 的最后一个镜像 `task_type` 是 `failed` 或 `archived`；
- 从该 task 的全部镜像 payload 中找到至少一条 `branch` 与快照 `Branch` **逐字相等**、
  且 `commit` 经去首尾空白后非空的结果。

缺少镜像、仍是 `completed`/`turn_failed`、`TaskID` 缺失、payload 解码失败、字段缺失、
commit 为空或 branch 不相等，全部保守地保留该快照。这样“没有产出”的跳过只发生在
账本已经能证明 task 已收口且没有该分支提交时，不会把镜像滞后误判为零产出。

## 1. Task 1：在 `Store.WorkBranch` 内完成账本回放判定

### 1.1 文件范围

只允许改：

- `internal/ledger/events.go`
- `internal/ledger/events_test.go`

不新增导出 API，不改 `DispatchSnapshot`、`WorkBranchInfo`、事件 JSON 形状。

### 1.2 Interfaces

Consumes：

```go
func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error)
func (s *Store) TasksOf(cardID string) ([]TaskLink, error)
type DispatchSnapshot struct {
    Target string
    TaskID string
    Branch string
    Purpose string
}
const EvDispatched = "dispatched"
const EvTaskMirrored = "task_mirrored"
const PurposeReview = "review"
type Event struct {
    Seq int64
    CardID string
    Type string
    Payload json.RawMessage
    SourceTask string
}
type mirroredTaskPayload struct {
    TaskType string `json:"task_type"`
    Payload json.RawMessage `json:"payload"`
}
```

Produces（签名保持不变）：

```go
type WorkBranchInfo struct {
    Branch string
    Target string
}

func (s *Store) WorkBranch(cardID string) (WorkBranchInfo, error)
```

语义产出为“最近一条有资格的非审阅快照”；审阅快照和零产出终态快照都不覆盖已经
选中的较早快照。无资格快照时继续返回既有 `ErrNotFound` 包装错误。

### 1.3 步骤 1：先复核基线和测试夹具

执行者先执行 §0.1 的基线命令；预期是它真实退出 0，且既有审阅跳过、同机接续、
跨机拒发与分支挂号测试均为 `PASS`。若结果不是该输出，原样写入台账并停止本 task，
不得把失败归因给尚未实现的代码。

测试范围只限 `internal/ledger`；本 task 不跑全仓测试。

### 1.4 步骤 2：写接缝级失败测试并跑红

在 `internal/ledger/events_test.go` 的 import 中加入 `time`，并追加以下完整测试辅助
函数和用例。辅助函数只负责通过真实 `LinkTask`、`RecordDispatch`、
`AppendMirroredEvent` 造账本数据；每条断言的入口都是 `Store.WorkBranch`，属于声明接缝，
不是只测内部 map。

```go
func recordB254WorkRound(t *testing.T, s *Store, c Card, target, taskID, branch, purpose string) {
	t.Helper()
	if err := s.LinkTask(c.ID, target, taskID, purpose, "test"); err != nil {
		t.Fatalf("LinkTask(%s): %v", taskID, err)
	}
	if err := s.RecordDispatch(c.ID, DispatchSnapshot{
		Template: "feature-impl", Target: target, TaskID: taskID, Branch: branch,
		Purpose: purpose, Actor: "test",
	}); err != nil {
		t.Fatalf("RecordDispatch(%s): %v", taskID, err)
	}
}

func mirrorB254WorkEvent(t *testing.T, s *Store, c Card, target, taskID, typ, payload string, seq int64) {
	t.Helper()
	if _, err := s.AppendMirroredEvent(c.ID, MirroredEvent{
		Target: target, Task: taskID, SourceSeq: seq, Type: typ,
		Payload: []byte(payload), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("AppendMirroredEvent(%s,%d): %v", typ, seq, err)
	}
}

func TestWorkBranchSkipsZeroOutputTerminalRounds(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		payload string
	}{
		{name: "failed fields missing", typ: "failed", payload: `{"fail_reason":"executor gone"}`},
		{name: "failed commit explicitly empty", typ: "failed", payload: `{"branch":"cards/P1-empty","commit":""}`},
		{name: "failed commit on another branch", typ: "failed", payload: `{"branch":"cards/other","commit":"abc123"}`},
		{name: "archived without prior result", typ: "archived", payload: `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := seedStore(t)
			c := mk(t, s, "零产出")
			branch := "cards/" + c.ID + "-empty"
			taskID := "T-empty-" + tc.name
			recordB254WorkRound(t, s, c, "mac-02", taskID, branch, PurposeImplement)
			mirrorB254WorkEvent(t, s, c, "mac-02", taskID, tc.typ, tc.payload, 1)
			if _, err := s.WorkBranch(c.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("零产出终态轮应被跳过并返回 ErrNotFound，实得 %v", err)
			}
		})
	}
}

func TestWorkBranchKeepsProducedFailedRound(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "失败但有产出")
	branch := "cards/" + c.ID + "-failed"
	recordB254WorkRound(t, s, c, "mac-02", "T-produced-failed", branch, PurposeImplement)
	mirrorB254WorkEvent(t, s, c, "mac-02", "T-produced-failed", "failed",
		`{"fail_reason":"turn stopped","branch":"`+branch+`","commit":"abc123"}`, 1)
	got, err := s.WorkBranch(c.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != branch || got.Target != "mac-02" {
		t.Fatalf("有产出失败轮必须保留分支和目标机，实得 %+v", got)
	}
}

func TestWorkBranchKeepsProducedArchivedRound(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "完成后归档")
	branch := "cards/" + c.ID + "-archived"
	recordB254WorkRound(t, s, c, "mac-02", "T-produced-archived", branch, PurposeImplement)
	mirrorB254WorkEvent(t, s, c, "mac-02", "T-produced-archived", "completed",
		`{"branch":"`+branch+`","commit":"def456"}`, 1)
	mirrorB254WorkEvent(t, s, c, "mac-02", "T-produced-archived", "archived", `{}`, 2)
	got, err := s.WorkBranch(c.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != branch || got.Target != "mac-02" {
		t.Fatalf("归档前有产出的轮必须保留分支和目标机，实得 %+v", got)
	}
}

func TestWorkBranchSkipsZeroOutputAndUsesPreviousProducedRound(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "跳过后回到上一轮")
	oldBranch := "cards/" + c.ID + "-implement"
	recordB254WorkRound(t, s, c, "mac-02", "T-produced", oldBranch, PurposeImplement)
	mirrorB254WorkEvent(t, s, c, "mac-02", "T-produced", "completed",
		`{"branch":"`+oldBranch+`","commit":"old123"}`, 1)
	mirrorB254WorkEvent(t, s, c, "mac-02", "T-produced", "archived", `{}`, 2)

	emptyBranch := "cards/" + c.ID + "-implement-2"
	recordB254WorkRound(t, s, c, "linux-01", "T-empty", emptyBranch, PurposeImplement)
	mirrorB254WorkEvent(t, s, c, "linux-01", "T-empty", "failed",
		`{"fail_reason":"credential revoked"}`, 1)

	got, err := s.WorkBranch(c.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != oldBranch || got.Target != "mac-02" {
		t.Fatalf("零产出新轮不能覆盖上一条有产出轮，实得 %+v", got)
	}
}

func TestWorkBranchKeepsLiveTurnFailedRound(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "仍待裁决")
	branch := "cards/" + c.ID + "-live"
	recordB254WorkRound(t, s, c, "mac-02", "T-live", branch, PurposeImplement)
	mirrorB254WorkEvent(t, s, c, "mac-02", "T-live", "turn_failed",
		`{"fail_reason":"可继续","branch":"`+branch+`","commit":""}`, 1)
	got, err := s.WorkBranch(c.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != branch || got.Target != "mac-02" {
		t.Fatalf("turn_failed 仍是 waiting_review，不能当零产出终态跳过，实得 %+v", got)
	}
}
```

马上只跑新增接缝测试：

```sh
go test ./internal/ledger -run 'TestWorkBranch(SkipsZeroOutputTerminalRounds|KeepsProducedFailedRound|KeepsProducedArchivedRound|SkipsZeroOutputAndUsesPreviousProducedRound|KeepsLiveTurnFailedRound)$' -count=1 -v
```

红判据是当前实现仍把零产出 dispatched 快照当作指针：四个零产出子用例至少会以
`ErrNotFound` 断言失败；“跳过后使用上一轮”与 `turn_failed` 保护用例也必须在新判定
接线前失败。若该命令在未改生产代码时全绿，立即把原始输出记入台账并停止，检查测试
是否真的通过 `Store.WorkBranch` 入口，不得改成直喂内部 helper。

### 1.5 步骤 3：最小实现

在 `internal/ledger/events.go` 中，保留现有 `WorkBranchInfo` 和公开签名，替换
`WorkBranch` 前的私有辅助区及 `WorkBranch` 函数为以下完整代码块。它复用同包已有的
`mirroredTaskPayload`，不添加旁路查询；`*string` 指针专门区分 JSON 字段缺失和显式空值，
两者都按“没有可证明的提交”处理。

```go
type workBranchTaskState struct {
	lastType         string
	producedBranches map[string]struct{}
	projectionBroken bool
}

type workBranchResultPayload struct {
	Branch *string `json:"branch"`
	Commit *string `json:"commit"`
}

func scanWorkBranchTaskStates(events []Event) map[string]workBranchTaskState {
	states := make(map[string]workBranchTaskState)
	for _, event := range events {
		if event.Type != EvTaskMirrored || event.SourceTask == "" {
			continue
		}
		state := states[event.SourceTask]
		if state.producedBranches == nil {
			state.producedBranches = make(map[string]struct{})
		}
		var mirrored mirroredTaskPayload
		if err := json.Unmarshal(event.Payload, &mirrored); err != nil {
			state.projectionBroken = true
			state.lastType = ""
			log().Warn("回放工作分支 task 镜像失败", "task", event.SourceTask,
				"seq", event.Seq, "cause", err)
			states[event.SourceTask] = state
			continue
		}
		state.lastType = mirrored.TaskType
		if mirrored.TaskType == "" {
			state.projectionBroken = true
		}
		if len(mirrored.Payload) == 0 || string(mirrored.Payload) == "null" {
			states[event.SourceTask] = state
			continue
		}
		var result workBranchResultPayload
		if err := json.Unmarshal(mirrored.Payload, &result); err != nil {
			state.projectionBroken = true
			log().Warn("解码工作分支 task 结果失败", "task", event.SourceTask,
				"seq", event.Seq, "task_type", mirrored.TaskType, "cause", err)
			states[event.SourceTask] = state
			continue
		}
		if result.Branch != nil && result.Commit != nil &&
			strings.TrimSpace(*result.Branch) != "" &&
			strings.TrimSpace(*result.Commit) != "" {
			state.producedBranches[*result.Branch] = struct{}{}
		}
		states[event.SourceTask] = state
	}
	return states
}

func keepsWorkBranchSnapshot(snapshot DispatchSnapshot, states map[string]workBranchTaskState) bool {
	if snapshot.TaskID == "" {
		// 老快照无法关联 task 结局；保留是唯一不会静默丢产出的选择。
		log().Debug("工作分支快照缺 task_id，保留保护", "branch", snapshot.Branch)
		return true
	}
	state, ok := states[snapshot.TaskID]
	if !ok || state.projectionBroken {
		log().Debug("工作分支 task 结局未知，保留保护", "task", snapshot.TaskID,
			"branch", snapshot.Branch, "known", ok, "projection_broken", state.projectionBroken)
		return true
	}
	if state.lastType != "failed" && state.lastType != "archived" {
		return true
	}
	if _, produced := state.producedBranches[snapshot.Branch]; produced {
		return true
	}
	log().Info("跳过零产出工作分支快照", "task", snapshot.TaskID,
		"branch", snapshot.Branch, "target", snapshot.Target, "terminal", state.lastType)
	return false
}

// WorkBranch 卡的工作分支：最近一次**有资格的非审阅**派发所用的分支。
// 审阅跑在工作分支上不新开分支；已收口且没有同分支 commit 的轮次也不占指针。
// 结局和 commit 只从协调者账本的 EvTaskMirrored 回放，不能访问执行机 git。
// 老快照没有 purpose 时仍回落到挂账表按 task_id 查用途。
func (s *Store) WorkBranch(cardID string) (WorkBranchInfo, error) {
	var zero WorkBranchInfo
	log().Info("查询卡工作分支", "card", cardID)
	events, err := s.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		log().Error("读取卡 dispatched 事件失败", "card", cardID, "cause", err)
		return zero, fmt.Errorf("读卡 dispatched 事件: %w", err)
	}
	links, err := s.TasksOf(cardID)
	if err != nil {
		log().Error("读取卡挂账失败", "card", cardID, "cause", err)
		return zero, err
	}
	purposeOf := map[string]string{}
	for _, link := range links {
		purposeOf[link.TaskID] = link.Purpose
	}
	states := scanWorkBranchTaskStates(events)
	info := WorkBranchInfo{}
	for _, event := range events {
		if event.Type != EvDispatched {
			continue
		}
		var snapshot DispatchSnapshot
		if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
			log().Warn("解码工作分支 dispatched 快照失败", "card", cardID,
				"seq", event.Seq, "cause", err)
			continue
		}
		purpose := snapshot.Purpose
		if purpose == "" {
			purpose = purposeOf[snapshot.TaskID]
		}
		if purpose == PurposeReview || snapshot.Branch == "" {
			continue
		}
		if !keepsWorkBranchSnapshot(snapshot, states) {
			continue
		}
		info = WorkBranchInfo{Branch: snapshot.Branch, Target: snapshot.Target}
		log().Debug("采用卡工作分支快照", "card", cardID, "task", snapshot.TaskID,
			"branch", snapshot.Branch, "target", snapshot.Target, "purpose", purpose)
	}
	if info.Branch == "" {
		err := fmt.Errorf("卡 %s 没有有资格的非审阅 dispatched 快照（尚未派出实现轮，或最近轮零产出）: %w",
			cardID, ErrNotFound)
		log().Warn("卡没有可用工作分支", "card", cardID, "cause", err)
		return zero, err
	}
	log().Info("卡工作分支查询完成", "card", cardID, "branch", info.Branch, "target", info.Target)
	return info, nil
}
```

实现注意事项：

- `state.projectionBroken` 一旦发生解码错误就不清零；后续即便看到 terminal，也不能把
  不完整回放当成零产出放行。
- 只能用与快照 `Branch` 相等的 payload branch 计入产出；仅有 commit 或另一个分支的
  commit 都不够。
- `completed` 的提交要在 `archived` 之后仍可见，因此扫描 task 的全部镜像事件，不能
  只读最后一条 payload。
- 日志使用包内 `log()` 的结构化 slog；入口、账本读失败、JSON 解码分支、跳过分支、
  选择成功和无分支错误都带 card/task/seq/branch/target 上下文；不加 `fmt.Println`。

### 1.6 步骤 4：跑绿、格式化和 task 内验收

执行：

```sh
gofmt -w internal/ledger/events.go internal/ledger/events_test.go
go test ./internal/ledger -run 'TestWorkBranch(SkipsZeroOutputTerminalRounds|KeepsProducedFailedRound|KeepsProducedArchivedRound|SkipsZeroOutputAndUsesPreviousProducedRound|KeepsLiveTurnFailedRound|SkipsReviewRounds)$' -count=1 -v
```

预期是新增五组用例与既有 `TestWorkBranchSkipsReviewRounds` 全部 `PASS`，包结果为
`ok github.com/Xsxdot/handoff/internal/ledger ...`。这条预期只有在执行者真实跑到输出后
才能写进台账；失败时把原始错误写入台账，不替失败命名原因。

### 1.7 步骤 5：Task 1 注释与范围收口

逐项检查 `internal/ledger/events.go`：私有 helper 说明“为什么只从账本回放”；
`WorkBranch` 注释说明审阅和零产出跳过规则、旧快照 purpose 回落和无执行机 git；
测试 helper 注释说明它穿过 `RecordDispatch` 与 `AppendMirroredEvent` 的真实 JSON 边界。
运行 `git diff --check`，确认 Task 1 只触及本节两个文件。该 task 的行为验收为：

- 零产出 `failed`/`archived` 快照不返回、不覆盖前一条有产出快照；
- 同分支非空 commit 的失败轮和归档轮继续返回 `Branch` 与同源 `Target`；
- `turn_failed`、无镜像或坏镜像不被误判为零产出；
- review 既有跳过语义不变；
- 无任何执行机 git、origin、网络调用。

## 2. Task 2：跨目标机文案与 `ViaTemplate` 接缝回归

### 2.1 文件范围

只允许改：

- `internal/ledgerstep/dispatch.go`
- `internal/ledgerstep/dispatch_test.go`

不改 `cmd/card_dispatch.go` 的 flag；`card dispatch --base` 仍不提供。

### 2.2 Interfaces

Consumes：

```go
func (s *Store) WorkBranch(cardID string) (ledger.WorkBranchInfo, error)
type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, err error)
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)
```

Produces：

```go
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)
```

其既有行为保持：同目标机接续仍从 `workInfo.Branch` 起、review 仍走工作分支、跨目标机
仍在 `Transport`/`LinkTask`/`RecordDispatch` 之前拒绝；变化只有 `WorkBranch` 返回
`ErrNotFound` 的零产出轮会让本次派发重新成为首轮卡基线路径，以及错误文案不再建议
`push origin`/显式 `--base`。

### 2.3 步骤 1：Task 2 基线与最小范围

先重新执行 §0.1 基线命令；预期仍为两个包退出码 0。测试范围只限
`internal/ledgerstep`，依赖的 ledger 零产出测试由 Task 1 命令覆盖，不在本 task 重跑
全仓测试。

### 2.4 步骤 2：写接缝级失败测试并跑红

在 `internal/ledgerstep/dispatch_test.go` 的 import 中加入 `time`，追加以下真实镜像
辅助函数；将现有 `TestViaTemplateRejectsCrossTargetBeforeTransport` 整个函数替换为下方
同名实现，并追加零产出改派测试。它们不调用私有 `keepsWorkBranchSnapshot`，
而是通过 `ViaTemplate` → `Store.WorkBranch` → 账本镜像边界验证用户故事。

```go
func mirrorB254DispatchEvent(t *testing.T, st *ledger.Store, cardID, target, taskID string,
	seq int64, typ, payload string) {
	t.Helper()
	if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: target, Task: taskID, SourceSeq: seq, Type: typ,
		Payload: []byte(payload), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("AppendMirroredEvent(%s): %v", typ, err)
	}
}

func TestViaTemplateAllowsCrossTargetAfterZeroOutputFailure(t *testing.T) {
	st, card := dispatchTestCard(t)
	var dispatched []DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		dispatched = append(dispatched, opts)
		return fmt.Sprintf("T-zero-%d", len(dispatched)), nil
	}}
	first, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"})
	if err != nil {
		t.Fatalf("首轮 ViaTemplate: %v", err)
	}
	mirrorB254DispatchEvent(t, st, card.ID, "mac-02", first.Task, 1, "failed",
		`{"fail_reason":"codex refresh token revoked"}`)

	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "linux-01"}); err != nil {
		t.Fatalf("零产出失败轮后应允许改派另一台目标机: %v", err)
	}
	if len(dispatched) != 2 {
		t.Fatalf("应调用两次 Transport，实得 %d", len(dispatched))
	}
	second := dispatched[1]
	if second.Target != "linux-01" {
		t.Fatalf("改派目标机 = %q，want linux-01", second.Target)
	}
	if second.Base != "" || second.LocalBaseBranch || !second.ResolveDefaultBase {
		t.Fatalf("零产出轮后应从卡空基线重新开始：base=%q local=%v default=%v",
			second.Base, second.LocalBaseBranch, second.ResolveDefaultBase)
	}
}

func TestViaTemplateRejectsCrossTargetBeforeTransport(t *testing.T) {
	st, card := dispatchTestCard(t)
	transportCalls := 0
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		transportCalls++
		return fmt.Sprintf("T-produced-%d", transportCalls), nil
	}}
	first, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"})
	if err != nil {
		t.Fatalf("首轮 ViaTemplate: %v", err)
	}
	mirrorB254DispatchEvent(t, st, card.ID, "mac-02", first.Task, 1, "completed",
		fmt.Sprintf(`{"branch":%q,"commit":"abc123"}`, first.Branch))
	mirrorB254DispatchEvent(t, st, card.ID, "mac-02", first.Task, 2, "archived", `{}`)
	before, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读拒发前事件: %v", err)
	}

	_, err = d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "linux-01"})
	if err == nil {
		t.Fatal("有产出轮跨目标机必须拒绝")
	}
	message := err.Error()
	for _, want := range []string{"工作分支归属于另一台执行机", "请回上次目标机继续", "mac-02", "linux-01"} {
		if !strings.Contains(message, want) {
			t.Fatalf("跨机拒绝缺少真实可走路径 %q，实得 %q", want, message)
		}
	}
	for _, invalid := range []string{"git push", "--base"} {
		if strings.Contains(message, invalid) {
			t.Fatalf("card dispatch 文案不应给无效出口 %q：%q", invalid, message)
		}
	}
	if transportCalls != 1 {
		t.Fatalf("跨机拒绝不得调用 Transport，调用次数=%d", transportCalls)
	}
	after, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读拒发后事件: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("跨机拒绝不得新增账本事件：before=%d after=%d", len(before), len(after))
	}
}
```

先跑：

```sh
go test ./internal/ledgerstep -run 'TestViaTemplate(AllowsCrossTargetAfterZeroOutputFailure|RejectsCrossTargetBeforeTransport)$' -count=1 -v
```

当前旧实现的红形必须是：零产出改派仍被跨目标机拒绝；旧跨机用例的文本断言因
`git push`/`--base` 仍出现而未能满足新负向断言。若测试未红，检查新用例是否真的写入
镜像事件并通过 `ViaTemplate`，不要改为直接调用账本 helper。

### 2.5 步骤 3：最小实现和文案

在 `internal/ledgerstep/dispatch.go` 现有跨目标机分支中，保留调用顺序、日志字段和
前置拒绝位置，只替换日志文案与返回错误为以下完整代码块：

```go
	if hasWorkBranch && (workInfo.Target == "" || workInfo.Target != target) {
		slog.Default().Warn("工作分支跨目标机，拒绝接续", "card", c.ID,
			"branch", workInfo.Branch, "previous_target", workInfo.Target, "target", target)
		message := fmt.Sprintf("工作分支归属于另一台执行机，不能跨机接续：上次目标机 %q，本次目标机 %q，分支 %q；请回上次目标机继续，card dispatch 不提供跨机接续出口",
			workInfo.Target, target, workInfo.Branch)
		if workInfo.Target == "" {
			message = fmt.Sprintf("工作分支缺少历史目标机归属，不能在本次目标机 %q 接续：分支 %q；请在创建该分支的原执行机继续，card dispatch 不提供跨机接续出口",
				target, workInfo.Branch)
		}
		return zero, errors.New(message)
	}
```

文案不能提 `git push origin` 或 `--base`：本入口的 flag 集没有 `--base`，且规格明确把
补 flag 列为 out of scope。已知上次目标机时，文案陈述分支归属、当前与上次目标机、
分支名和“回上次目标机继续”路径；历史快照缺目标机时改为“回创建该分支的原执行机”，
不把空目标机伪装成可行动的机器名，也不承诺 origin 已存在、fetch 成功或自动迁移分支。

### 2.6 步骤 4：跑绿、日志/注释和范围收口

执行：

```sh
gofmt -w internal/ledgerstep/dispatch.go internal/ledgerstep/dispatch_test.go
go test ./internal/ledgerstep -run 'TestViaTemplate(AllowsCrossTargetAfterZeroOutputFailure|RejectsCrossTargetBeforeTransport|SecondRoundGetsNumberedBranch|ContinuationUsesLocalWorkBranch|NodePurposeTakesReviewPath)$' -count=1 -v
```

预期是五个测试入口均 `PASS`：零产出失败轮改派成功且 `Base==""`、
`LocalBaseBranch==false`、`ResolveDefaultBase==true`；有产出轮跨机在 Transport 前拒绝、
无新增账本事件、文案无无效出口；既有同机挂号/审阅回归保持。失败时原样写台账。

检查 `ViaTemplate` 的既有入口日志、Transport 前后和跨机拒绝 slog 均保留；新测试 helper
注释必须说明 `AppendMirroredEvent` 是为穿过真实账本 JSON 投影，而不是内部状态注入。
运行 `git diff --check`，确认 Task 2 只触及本节两个文件。

## 3. Task 3：协调者最终门禁与提交（本 task 由协调者执行，不派发）

### 3.1 Interfaces

Consumes：Task 1/2 的四个实现文件、两组缝级测试、台账原始输出和当前分支。

Produces：最终定向测试/静态检查/变异复验的真实结果，以及当前分支上不 push 的 commit。

### 3.2 定向最终门禁

实现 task 完成后由协调者亲自按顺序执行：

```sh
go test ./internal/ledger ./internal/ledgerstep -count=1
go test ./internal/ledger ./internal/ledgerstep -race -count=1
go vet ./internal/ledger ./internal/ledgerstep
gofmt -l internal/ledger/events.go internal/ledger/events_test.go internal/ledgerstep/dispatch.go internal/ledgerstep/dispatch_test.go
git diff --check
```

预期：两个 `go test` 命令退出码 0，且对应包输出 `ok`；`go vet` 退出码 0 且无输出；
`gofmt -l` 和 `git diff --check` 无输出。任何命令失败都把原始输出写台账，不改写成
“环境问题”或“应该通过”。本卡不把全仓测试分摊给任一实现 task。

### 3.3 行为变异复验

只做可恢复的工作树临时变异，逐项跑最小测试后用 `apply_patch` 恢复，再重跑相应包：

| 临时变异 | 必须变红的行为测试 |
| --- | --- |
| 让 `keepsWorkBranchSnapshot` 在 terminal 且无 commit 时返回 `true` | `TestWorkBranchSkipsZeroOutputTerminalRounds`、`TestWorkBranchSkipsZeroOutputAndUsesPreviousProducedRound`、`TestViaTemplateAllowsCrossTargetAfterZeroOutputFailure` |
| 把 `archived` 从 terminal 集合删掉 | `TestWorkBranchKeepsProducedArchivedRound` 或其归档回放断言失败 |
| 只要 commit 非空就不比较 payload branch | `TestWorkBranchSkipsZeroOutputTerminalRounds/failed commit on another branch` 失败 |
| 把 `turn_failed` 当 terminal | `TestWorkBranchKeepsLiveTurnFailedRound` 失败 |
| 无镜像或解码错误改成跳过 | `TestWorkBranchKeepsLiveTurnFailedRound`/既有无镜像接续回归失败 |
| 恢复旧跨机错误文案 | `TestViaTemplateRejectsCrossTargetBeforeTransport` 的正/负文案断言失败 |
| 把跨机判断移到 Transport 之后 | `TestViaTemplateRejectsCrossTargetBeforeTransport` 的 `transportCalls==1` 或事件数断言失败 |

如果某一变异无法安全施加，记录其真实失败输出并标“未验证”，不代替其他变异的结果；
不使用 `git reset --hard` 或 `git checkout --`。

### 3.4 提交前范围

```sh
git status --short --branch
git diff --name-only
git diff --check
git add docs/superpowers/plans/b254-plan.md docs/ledgers/2026-08-25-b254-ledger.md internal/ledger/events.go internal/ledger/events_test.go internal/ledgerstep/dispatch.go internal/ledgerstep/dispatch_test.go
git diff --cached --check
git commit -m "fix(ledger): skip zero-output work branches"
```

只允许上述六个实现/测试文件和本计划、台账进入提交；不 push、不切分支、不改 git 配置。
若本卡实际执行阶段没有修改实现文件，提交范围应只含计划与台账，不能为了满足文件清单
凭空创建代码。

## 4. 序列化边界清单

本卡不新增 wire 字段，但新增了对既有 task payload 的手写投影，必须穿过真实账本边界：

| 产生/存储/消费点 | 现有形态 | 计划断言 |
| --- | --- | --- |
| `internal/agentd/manager.go` completed/failed/turn_failed payload | `branch` + `commit`，字段可能缺失或为空 | Task 1 用指针字段区分缺失与显式空值；两者都不证明产出 |
| `internal/ledger/mirror.go#AppendMirroredEvent` | 原始 payload 包进 `{"task_type", "payload"}` 后 JSON 落 `card_events` | Task 1/2 通过 `AppendMirroredEvent` 写入，不手工调用 scanner |
| `internal/ledger/events.go#Store.EventsFromAsc` | 从持久化 JSON 读回 `Event.Payload`/`SourceTask` | `Store.WorkBranch` 入口测试断言最终 Branch/Target，不只断言中间 struct |
| `internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate` | 读取 `WorkBranch` 后决定跨机拒绝或卡基线 | Task 2 真实 `ViaTemplate` 断言 Transport 次数、Base 和副作用 |

字段边界反例必须保留：字段缺失、`commit:""`、branch 不相等、同分支非空 commit、
completed 后 archived、turn_failed。没有新增 map/DTO/CLI/跨语言投影；`cmd/card_dispatch.go`
不进入文件清单。

## 5. 缺陷族对抗审查与验收结论

| 缺陷族 | 设问 | 本计划结论与锁法 |
| --- | --- | --- |
| 生命周期/状态机中断 | 镜像滞后、task 仍 waiting_review、归档后历史 commit 是否丢失？ | 无镜像/坏镜像/`turn_failed`/`completed` fail-closed；archived 回放全部历史 payload；Task 1 四态覆盖。 |
| 静默失败/误导报错 | 是否会把不可走的 origin/`--base` 建议继续给 `card dispatch`？ | Task 2 正向断言上次/本次目标机和回退旧机路径，负向断言 `git push`、`--base` 缺席；WorkBranch 读错误和无资格状态均结构化日志+明确 `ErrNotFound`。 |
| 跨平台假设 | 是否读取执行机 git、依赖路径、时间或 OS 特性？ | 不访问执行机；镜像时间只用于事件写入，判定按 seq；字符串 branch 精确比较，不用 OS 路径。测试使用 `t.TempDir` 的既有账本夹具。 |
| 假红/假绿测试 | 测试是否能在旧实现错误时真实变红，是否绕过声明缝？ | Task 1/2 的每支测试入口都是 `Store.WorkBranch` 或 `Dispatcher.ViaTemplate`；零产出改派、跨机副作用、字段缺失/零值均是决定性反例；红绿命令先后写明。 |
| 门禁绕过 | 是否能通过 fallback、旧快照、缺 task_id 或 origin 状态绕过保护？ | 缺证据统一保留指针；跨机仍在 Transport 前拒绝；不加 `--base`，不查 origin，不造第二查询出口。 |
| 序列化边界 | 产生与消费之间的 hand-rolled projection 是否有真实穿线？ | `AppendMirroredEvent` → `EventsFromAsc` → `WorkBranch` → `ViaTemplate` 两条入口闭环；指针字段锁缺失与零值差别。 |
| 新枚举值白名单 | 是否新增 event type/status/purpose？ | 不新增枚举；仅消费既有 `failed`、`archived`、`completed`、`turn_failed` 字面值，来源与已有 `taskstate.go` 回放一致。 |
| 可观测性 | 成功、跳过、错误和外部调用前后是否可追踪？ | `WorkBranch` 入口/账本读/解码/跳过/选择/无分支全用 `log()`；`ViaTemplate` 保留已有入口、Transport 前后和跨机拒绝 slog；禁 print。 |

## 6. 接缝双向覆盖、用户故事与上下文预算

### 6.1 接缝清单

| 声明接缝 | 测试入口 | 锁定行为 |
| --- | --- | --- |
| `internal/ledger/events.go#Store.WorkBranch` | `TestWorkBranchSkipsZeroOutputTerminalRounds`、`TestWorkBranchKeepsProducedFailedRound`、`TestWorkBranchKeepsProducedArchivedRound`、`TestWorkBranchSkipsZeroOutputAndUsesPreviousProducedRound`、`TestWorkBranchKeepsLiveTurnFailedRound`、既有 `TestWorkBranchSkipsReviewRounds` | 终态/产出资格、回放边界、覆盖顺序、审阅跳过 |
| 同一接缝的调用方 `internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate` | `TestViaTemplateAllowsCrossTargetAfterZeroOutputFailure`、`TestViaTemplateRejectsCrossTargetBeforeTransport`，以及既有同机/审阅接续测试 | 零产出后换目标机从卡基线放行；有产出跨机仍拒；拒绝无 Transport/账本副作用；文案可行动 |

测试→缝：Task 1 每支新增测试直接调用 `Store.WorkBranch`；Task 2 每支新增测试直接
调用 `Dispatcher.ViaTemplate`，调用链必穿 `Store.WorkBranch`。没有内部 helper 测试顶替
接缝测试。缝→测试：上表的两条接缝各有至少两支断言；dispatch 文案仍挂同一接缝，
不单独虚构第三条缝。

### 6.2 用户故事归属

- 故事 1（零产出失败后 `card dispatch --step --target` 改派放行、从卡基线起步）→
  Task 1 的零产出终态回放 + Task 2 `TestViaTemplateAllowsCrossTargetAfterZeroOutputFailure`。
- 故事 2（有产出跨机仍拒且报文是真实可走路径）→ Task 1 有产出保留 + Task 2
  `TestViaTemplateRejectsCrossTargetBeforeTransport`。
- 既有审阅跳过、正常轮最后覆盖、不访问执行机 git → Task 1 既有回归、Task 1
  `TestWorkBranchSkipsZeroOutputAndUsesPreviousProducedRound`、Task 3 的静态/变异门禁。
- 明确 out of scope 的 origin 自动放行与 `card dispatch --base` → 文件清单无 agentd/git/
  cmd flag 改动，Task 2 文案负向断言不引入这些出口。

### 6.3 上下文预算与边界型清单

Task 1 两个 ledger 文件，Task 2 两个 ledgerstep 文件，Task 3 只读门禁；每个实现 task
的文件集有界且无跨包重构。该卡是账本逻辑闭环，不新增 HTTP、wire、CLI stdout、浏览器
或真实执行机 git 边界；因此没有适用的真机清单。最终可验证事实是本地账本回放和
`ViaTemplate` 的 Transport 替身，不能外推为“真实凭据失效场景已在执行机复现”。

## 7. 占位符扫描与计划收口自审

本计划不含未决事项、跨任务骨架指代或未具体化的错误处理等未决占位符。测试代码复用
既有 `seedStore`/`mk`/`dispatchTestCard` 夹具，但每个复用点已给出完整新 helper、完整
断言和精确测试文件；不使用空壳测试例外。任何实现者想扩大文件范围、访问执行机 git、
补 `--base` 或改变未知状态的 fail-closed 口径，都必须先回到规格评审，不能在本计划内
自行推断。

计划出稿前自审：

- spec 每条用户故事均已指向具体 Task 与测试入口；
- `Store.WorkBranch` 的 task 终态、commit 存在性、分支同源关系均有来源和真实 JSON
  穿线断言；
- 所有新增测试入口均穿过声明接缝，且每条声明接缝都有至少一支接缝级断言；
- 每个实现 task 都有基线命令、最小测试范围、红绿步骤、日志步骤和注释步骤；
- 图覆盖债、缺失/零值、未知状态、错误文案和变异对抗均明确写入验收栏；
- 本节点只提交计划/台账产出，不在本节点实现代码。
