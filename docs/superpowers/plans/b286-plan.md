# B286 `card dispatch --step` 首态可见 + review 只读机械闸实施计划

状态：执行计划；规格 `docs/superpowers/specs/b286.md` 已批准，审查修订见 `docs/superpowers/reviews/b286-spec-review.md`。
法定产出物：`docs/superpowers/plans/b286-plan.md`。
事实台账：`docs/superpowers/specs/b286-ledger.md`；本节点已边查边追加基线命令、原始失败和源码事实。
有效基线：`origin/fix/b286-step-review` 与当前 HEAD 同为 `5cfa827464f4b736f02bd092efa2c42f8ba01d17`；实现只能在当前执行分支完成。

本卡只改 handoff 仓。C7 与 C8 没有共享抽象：C7 是 CLI 消费本机账本事件，C8 是账本节点运行器在裁决落账前消费已有 Diff。不得新增 HTTP、不得改变 `POST /api/cards/{id}/step` 的 202、不得把 step 变成同步等待整个 executor 回合、不得修改任务/工单状态机或 `Follow` 的排他游标、不得修改 charter 仓、不得写死 `linux-01`、不得把空 target 再判成错误。

## 1. 基线证据、图覆盖与执行边界

### 1.1 本节点已实跑的基线

所有命令都在未改实现代码的基线运行；实现者开始对应 task 前要重跑。命令若因依赖缓存目录失败，先保留原文并用下面同样的可写 `GOMODCACHE` 方式重跑，不能把未验证写成通过。

| 范围 | 命令 | 实跑结果 |
|---|---|---|
| C7 存量单测 | `env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./cmd -run 'TestCardDispatchStepReturnsImmediately' -count=1` | 退出 0；原始末行 `ok github.com/Xsxdot/handoff/cmd 0.141s` |
| C7 CLI 全包基线 | `env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./cmd -count=1` | 退出 0；原始输出 `ok github.com/Xsxdot/handoff/cmd 10.003s` |
| C8 存量回路 | `go test ./internal/ledgerstep -run 'TestReviewStepPassAndFailLoop|TestNodeStepWithoutProducesDoesNotInvokeOutputHooks' -count=1` | 退出 0；原始输出 `ok github.com/Xsxdot/handoff/internal/ledgerstep 0.233s` |
| C8 包基线 | `env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./internal/ledgerstep -count=1` | 退出 0；原始末行 `ok github.com/Xsxdot/handoff/internal/ledgerstep 6.494s` |

同一批未经可写缓存的 C7 命令曾失败，原始错误为：

```text
go: downloading github.com/Xsxdot/charter/graph v0.10.0
go: writing go.mod cache: open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.mod652271194.tmp: read-only file system
# github.com/Xsxdot/handoff/cmd
internal/agentd/codegraph.go:22:2: open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.lock: read-only file system
FAIL github.com/Xsxdot/handoff/cmd [setup failed]
FAIL
```

### 1.2 图与源码核对

仓内有 `codegraph/best.json`，最优领域词表中与本卡直接相关的是 `d_cli`、`d_ledger`、`d_orchestration`、`d_gateway`。已按平台不变量尝试 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_cli`、`d_execution`、`d_ledger`、`d_execution_contract`；四次均失败，原始错误为 `go: downloading github.com/Xsxdot/charter/graph v0.10.0`、`go: writing go.mod cache: open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.mod376605260.tmp: read-only file system`、`open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.lock: read-only file system`。PATH 中也未找到可执行 `codegraph`。这是图覆盖债；调用面以源码核对，不用空图结果证明不存在调用方。

已核对的当前源码事实：

- `cmd/card_dispatch.go:274-282` 的 `cardDispatchCmd` 在 `--step` 非空时调用 `runStepDispatch(cmd, id, cardDispatchStep)`，真实调用方不是 `cmd/card.go`。
- `cmd/card_node.go:22-50` 的当前签名是 `func runStepDispatch(cmd *cobra.Command, id, node string) error`；它目前只读取 `LocalEndpoint`、调用 `client.Client.CardStep`，收到 202 后在 stdout 打固定受理句。
- `internal/ledger/mirror.go:179-185` 的当前签名是 `func (s *Store) MaxSeq() (int64, error)`；`internal/ledger/follow.go:18-71` 的当前签名是 `func (s *Store) Follow(ctx context.Context, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error`。`Follow` 从 `fromSeq` 排他查询、按 seq 升序推进 cursor，并把回调错误返回给调用方。
- `internal/ledger/events.go:115-135` 的 `DispatchSnapshot` 已有 `Target`、`Branch`、`Base`、`BaseCommit`、`DisciplineName`、`Executor`、`Purpose`，没有 `local_base_branch`；本卡成功行不输出本地 ref/origin 标签。
- `internal/ledgerstep/node.go:133-310` 的当前 `RunOnce` 顺序为 Dispatch→Await→ParseVerdict→`RecordReviewVerdict`→清理本节点 needs→仅在 `Produces != nil` 时 Diff→路由；C8 必须把 review Diff 闸插入 `RecordReviewVerdict` 之前。
- `internal/ledgerstep/runner.go:378-401` 的 `diffNode` 调 `Client.Diff(ctx, taskID, "")`，再由 `ChangedPaths` 投影为仓内相对 POSIX 路径；C8 复用此注入依赖，不新造 diff 通道。
- `internal/ledger/types.go:161-180` 的 `NodeOverride.Purpose` 是节点级用途覆盖，`ledger.PurposeReview == "review"`；只认该字段，不按节点名或模板用途兜底。

### 1.3 任务 DAG 与精确接口

两个 task 可独立实现；先做 C7，再做 C8，最后由协调者执行两个包的合 main 门。每个 task 的文件集有界，除台账外不得越界。

#### Task 1：C7 CLI 首态

文件范围：生产 `cmd/card_node.go`；测试 `cmd/card_dispatch_test.go`；文档 `skills/handoff/SKILL.md` 当前 441 行和 607 行对应的两段。

Consumes：

```go
func openLedger() (*ledger.Store, error)
func (s *ledger.Store) MaxSeq() (int64, error)
func (s *ledger.Store) Follow(ctx context.Context, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(ledger.Event) error) error
func LocalEndpoint() (addr, token string, err error)
func client.New(addr, token string) *client.Client
func (c *client.Client) CardStep(ctx context.Context, cardID string, req proto.CardStepReq) error
```

Produces（仅供 `runStepDispatch` 使用的私有接口）：

```go
var stepFirstStateTimeout time.Duration
var stepFirstStatePollInterval time.Duration

type stepFirstState struct {
	Dispatch        *ledger.DispatchSnapshot
	FailureComment  string
}

func waitStepFirstState(ctx context.Context, st *ledger.Store, cardID string, watermark int64) (stepFirstState, error)
func writeStepDispatchResult(w io.Writer, cardID, node string, snap ledger.DispatchSnapshot)
```

空 target 的显示语义只在 `writeStepDispatchResult` 做：空 `Target` 显示「本机」；空 `Base` 显示「无起点分支」；空 `BaseCommit` 显示「无 sha」；非空 sha 取前七字节；`Branch` 是新分支，`Base` 是起点分支。成功行不出现「本地 ref」或「origin」标签。

#### Task 2：C8 review 只读闸

文件范围：生产 `internal/ledgerstep/node.go`；测试 `internal/ledgerstep/node_test.go`。真实 Diff JSON→路径投影的存量回归只复用 `internal/ledgerstep/runner_test.go:701-788` 的 `TestRunnerLocalClientUsesWaitAndDiffWire`，不在该文件增加第二套 HTTP 夹具。

Consumes：

```go
type NodeStep struct {
	St         *ledger.Store
	Node       ledger.NodeDef
	Dispatch   func(ctx context.Context, card ledger.Card, node ledger.NodeDef) (target, taskID string, err error)
	Await      func(ctx context.Context, target, taskID string) (message string, err error)
	Diff       func(ctx context.Context, target, taskID string) ([]string, error)
	WriteGate  func() bool
}

func (s *ledger.Store) RecordReviewVerdict(cardID, node string, pass bool, raw, actor string) error
func (s *ledger.Store) AddComment(cardID, body, kind, actor string) (ledger.Event, error)
func (n *NodeStep) haltForHuman(cardID, reason, body string) (Outcome, error)
```

Produces（私有路径判定，仅作为 `RunOnce` 内部实现，不是测试声明缝）：

```go
func isReviewLedgerPath(path string) bool
func reviewReadOnlyViolations(paths []string) []string
```

## 2. Task 1：C7 CLI 首态可见

### 2.1 基线和最小测试范围

1. 实现前重跑：

```bash
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./cmd -run 'TestCardDispatchStepReturnsImmediately' -count=1
```

预期退出 0，输出 `ok github.com/Xsxdot/handoff/cmd ...`。本 task 只跑 `./cmd` 的 card-step 命名测试，不跑全仓。

2. 测试使用现有 `newCardStepCLIEndpoint`、`runLedgerCLI` 和真实临时 `ledger.db`。mock HTTP 仍只返回 202；事件由同一个 DataDir 的真实 `ledger.Store` 写入，保证水位、事件 JSON 和 CLI stdout/stderr 穿过实际边界。

### 2.2 红测：从声明缝覆盖五种首态

在 `cmd/card_dispatch_test.go` 现有 import 基础上保留 `encoding/json`、`path/filepath`、`time`、`github.com/Xsxdot/handoff/internal/ledger`，追加下列完整测试辅助函数：

```go
func setStepFirstStateTestWindow(t *testing.T) {
	t.Helper()
	oldTimeout := stepFirstStateTimeout
	oldPoll := stepFirstStatePollInterval
	stepFirstStateTimeout = 40 * time.Millisecond
	stepFirstStatePollInterval = time.Millisecond
	t.Cleanup(func() {
		stepFirstStateTimeout = oldTimeout
		stepFirstStatePollInterval = oldPoll
	})
}

func createStepTestCard(t *testing.T, dir, title string) string {
	t.Helper()
	out, _, err := runLedgerCLI(t, dir, "card", "add", title, "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建 card step 测试卡: %v", err)
	}
	var card struct { ID string `json:"id"` }
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("解码 card step 测试卡: %v", err)
	}
	return card.ID
}

func appendStepDispatchForTest(t *testing.T, dir, cardID string, snap ledger.DispatchSnapshot) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开 card step 测试账本: %v", err)
	}
	defer st.Close()
	snap.Actor = "node:test"
	if err := st.RecordDispatch(cardID, snap); err != nil {
		t.Fatalf("写 dispatched 测试事件: %v", err)
	}
}

func appendStepDispatchFailureForTest(t *testing.T, dir, cardID, body string) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开 card step 失败账本: %v", err)
	}
	defer st.Close()
	if _, err := st.AddComment(cardID, body, "普通", "node:进行中"); err != nil {
		t.Fatalf("写派发失败 comment: %v", err)
	}
	if err := st.MarkNeedsHuman(cardID, "派发失败", "node:进行中"); err != nil {
		t.Fatalf("写派发失败 needs_human: %v", err)
	}
}
```

先把现有 `TestCardDispatchStepReturnsImmediately` 替换为下方完整版本，并新增四个测试。实现前运行红测；红的原因应是生产侧尚无 `stepFirstStateTimeout`、首态等待和格式化行为，不能删掉断言来让测试先绿。

```go
func TestCardDispatchStepReturnsImmediately(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	cardID := createStepTestCard(t, dir, "即时返回卡")
	out, _, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err != nil { t.Fatalf("card dispatch --step: %v", err) }
	for _, want := range []string{cardID, "进行中", "已受理", "handoff card wait " + cardID} {
		if !strings.Contains(out, want) { t.Fatalf("stdout = %q, want %q", out, want) }
	}
	if !strings.Contains(out, "首态未到") { t.Fatalf("无新首态时 stdout 必须说明短等结束: %q", out) }
	if strings.Contains(out, "Outcome") || strings.Contains(out, "T-") {
		t.Fatalf("stdout 不应包含旧 Outcome/task id：%q", out)
	}
}

func TestCardDispatchStepReportsNewDispatchFailure(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	cardID := createStepTestCard(t, dir, "新派发失败卡")
	appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
		TaskID: "old-task", Branch: "cards/old", Base: "old-base",
		BaseCommit: "oldcommit123456789012345678901234567890", DisciplineName: "old-discipline",
	})
	const comment = "本节点派发失败：\n工作分支跨机：cause-42"
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		appendStepDispatchFailureForTest(t, dir, cardID, comment)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, stderr, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err == nil { t.Fatal("水位之后的派发失败首态必须使 CLI 非零退出") }
	if !strings.Contains(stderr, comment) {
		t.Fatalf("stderr 必须包含 haltForHuman comment 正文 %q，实际 %q", comment, stderr)
	}
	if strings.Contains(out+stderr, "oldcomm") {
		t.Fatalf("不得把水位之前旧 dispatched 的短号打印成这次结果: out=%q stderr=%q", out, stderr)
	}
}

func TestCardDispatchStepReportsNewDispatchSnapshot(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	cardID := createStepTestCard(t, dir, "新派发成功卡")
	appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
		TaskID: "old-task", Branch: "cards/old", Base: "old-base",
		BaseCommit: "oldcommit123456789012345678901234567890", DisciplineName: "old-discipline",
	})
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
			Target: "", TaskID: "new-task", Branch: "cards/new", Base: "main",
			BaseCommit: "1234567890abcdef1234567890abcdef12345678", DisciplineName: "charter-implement",
		})
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, stderr, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err != nil { t.Fatalf("card dispatch --step: %v stderr=%q", err, stderr) }
	for _, want := range []string{cardID, "进行中", "本机", "cards/new", "main", "1234567", "charter-implement"} {
		if !strings.Contains(out, want) { t.Fatalf("成功首态 stdout = %q，缺少 %q", out, want) }
	}
	for _, forbidden := range []string{"oldcomm", "目标机未定", "本地 ref", "origin"} {
		if strings.Contains(out, forbidden) { t.Fatalf("成功首态 stdout 不应包含 %q: %q", forbidden, out) }
	}
}

func TestCardDispatchStepFormatsEmptyBaseCommit(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	cardID := createStepTestCard(t, dir, "空基线首态卡")
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = cardStepBody(t, r)
		appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
			Target: "", TaskID: "empty-base-task", Branch: "cards/empty", Base: "",
			BaseCommit: "", DisciplineName: "charter-review",
		})
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中")
	if err != nil { t.Fatalf("card dispatch --step: %v", err) }
	for _, want := range []string{"无起点分支", "无 sha", "cards/empty", "charter-review"} {
		if !strings.Contains(out, want) { t.Fatalf("空基线首态 stdout = %q，缺少 %q", out, want) }
	}
}

func TestCardDispatchStepExecutorWithoutTargetUsesLocalFirstState(t *testing.T) {
	setStepFirstStateTestWindow(t)
	dir := t.TempDir()
	var got map[string]json.RawMessage
	cardID := createStepTestCard(t, dir, "只覆盖执行器卡")
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = cardStepBody(t, r)
		appendStepDispatchForTest(t, dir, cardID, ledger.DispatchSnapshot{
			Target: "", TaskID: "executor-only-task", Branch: "cards/executor-only", Base: "main",
			BaseCommit: "abcdef0123456789abcdef0123456789abcdef01", DisciplineName: "charter-implement",
		})
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	out, _, err := runLedgerCLI(t, dir, "card", "dispatch", cardID, "--step", "进行中", "--executor", "codex")
	if err != nil { t.Fatalf("只覆盖 executor 的 card dispatch --step: %v", err) }
	if _, present := got["target"]; present { t.Fatalf("空 target 应保持缺席语义，wire 不应凭空写目标机：%v", got) }
	if gotExecutor := cardStepString(t, got, "executor"); gotExecutor != "codex" { t.Fatalf("executor = %q, want codex", gotExecutor) }
	if !strings.Contains(out, "本机") || strings.Contains(out, "目标机未定") { t.Fatalf("只覆盖 executor 仍应显示本机而非版本错文案: %q", out) }
}
```

红测范围只触及 `./cmd`。五支测试全部从 `runLedgerCLI` 进入 `cardDispatchCmd`，不直接调用等待 helper；事件通过真实 `ledger.Store` 写入。成功/失败测试都先写旧 `dispatched`，确保断言的是排他水位而不是「扫最后一条快照」。

### 2.3 最小实现：POST 前水位、同账本 Follow、两类首态

在 `cmd/card_node.go` 替换 import block，并替换 `runStepDispatch`；在同文件加入以下两个私有类型/函数。下面代码是完整新增/替换块，不能改成 task WS、HTTP 长轮询或 POST 后取水位：

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)
```

```go
var (
	stepFirstStateTimeout      = 20 * time.Second
	stepFirstStatePollInterval = 100 * time.Millisecond
)

var errStepFirstState = errors.New("card step 首态已到")

type stepFirstState struct {
	Dispatch       *ledger.DispatchSnapshot
	FailureComment string
}

func waitStepFirstState(ctx context.Context, st *ledger.Store, cardID string, watermark int64) (stepFirstState, error) {
	var observed stepFirstState
	logger := slog.Default().With("card", cardID, "watermark", watermark)
	waitCtx, cancel := context.WithTimeout(ctx, stepFirstStateTimeout)
	defer cancel()
	err := st.Follow(waitCtx, func() ([]string, error) {
		return []string{cardID}, nil
	}, watermark, stepFirstStatePollInterval, func(event ledger.Event) error {
		logger.Debug("短等收到卡事件", "seq", event.Seq, "type", event.Type)
		switch event.Type {
		case ledger.EvComment:
			var payload struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				logger.Warn("短等忽略无法解码的 comment", "seq", event.Seq, "cause", err)
				return nil
			}
			if strings.HasPrefix(payload.Body, "本节点派发失败：\n") {
				observed.FailureComment = payload.Body
			}
		case ledger.EvDispatched:
			var snapshot ledger.DispatchSnapshot
			if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
				logger.Warn("短等忽略无法解码的 dispatched", "seq", event.Seq, "cause", err)
				return nil
			}
			observed.Dispatch = &snapshot
			logger.Info("短等发现 dispatched 首态", "seq", event.Seq,
				"target", snapshot.Target, "branch", snapshot.Branch, "base", snapshot.Base,
				"base_commit", snapshot.BaseCommit, "discipline", snapshot.DisciplineName)
			return errStepFirstState
		case ledger.EvNeedsHuman:
			var payload struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				logger.Warn("短等忽略无法解码的 needs_human", "seq", event.Seq, "cause", err)
				return nil
			}
			if payload.Reason != "派发失败" {
				return nil
			}
			logger.Info("短等发现派发失败首态", "seq", event.Seq,
				"reason", payload.Reason, "has_comment", observed.FailureComment != "")
			return errStepFirstState
		}
		return nil
	})
	if errors.Is(err, errStepFirstState) {
		return observed, nil
	}
	return observed, err
}

func writeStepDispatchResult(w io.Writer, cardID, node string, snap ledger.DispatchSnapshot) {
	target := snap.Target
	if target == "" {
		target = "本机"
	}
	base := snap.Base
	if base == "" {
		base = "无起点分支"
	}
	baseCommit := snap.BaseCommit
	if baseCommit == "" {
		baseCommit = "无 sha"
	} else if len(baseCommit) > 7 {
		baseCommit = baseCommit[:7]
	}
	_, _ = fmt.Fprintf(w,
		"卡 %s 的节点 %s 已派发；目标机：%s；新分支：%s；起点分支：%s；起点短号：%s；纪律块：%s\n",
		cardID, node, target, snap.Branch, base, baseCommit, snap.DisciplineName)
}

func runStepDispatch(cmd *cobra.Command, id, node string) error {
	if cardDispatchPlan != "" {
		slog.Default().Warn("card step 拒绝本地 plan", "card", id, "node", node,
			"plan", cardDispatchPlan)
		return fmt.Errorf("card dispatch --step 不接受 --plan：调用方本地文件不会被上传")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	st, err := openLedger()
	if err != nil {
		slog.Default().Warn("打开本机账本失败", "card", id, "node", node, "cause", err)
		return err
	}
	defer st.Close()
	watermark, err := st.MaxSeq()
	if err != nil {
		slog.Default().Warn("读取 card step POST 前水位失败", "card", id, "node", node, "cause", err)
		return err
	}
	slog.Default().Info("记录 card step POST 前水位", "card", id, "node", node, "watermark", watermark)
	addr, token, err := LocalEndpoint()
	if err != nil {
		slog.Default().Warn("读取本机 agentd 端点失败", "card", id, "node", node, "cause", err)
		return err
	}
	cl := client.New(addr, token)
	req := proto.CardStepReq{
		Step: node, Target: cardDispatchTarget, Executor: cardDispatchExecutor,
		Model: cardDispatchModel, Extra: cardDispatchExtra, Actor: ledgerActor(),
	}
	slog.Default().Info("CLI 提交卡节点", "card", id, "node", node, "agentd", cl.BaseURL(),
		"target", req.Target, "executor", req.Executor, "model", req.Model,
		"has_extra", strings.TrimSpace(req.Extra) != "", "actor", req.Actor,
		"watermark", watermark)
	if err := cl.CardStep(ctx, id, req); err != nil {
		slog.Default().Warn("CLI 卡节点未受理", "card", id, "node", node, "cause", err)
		return err
	}
	slog.Default().Info("CLI 卡节点已收到 202，开始短等首态", "card", id, "node", node,
		"watermark", watermark, "timeout", stepFirstStateTimeout)
	state, waitErr := waitStepFirstState(ctx, st, id, watermark)
	if waitErr != nil {
		if ctx.Err() != nil {
			slog.Default().Warn("card step 首态等待被调用方取消", "card", id, "node", node,
				"cause", waitErr)
			return fmt.Errorf("等待 card step 首态: %w", waitErr)
		}
		if errors.Is(waitErr, context.DeadlineExceeded) {
			slog.Default().Info("card step 首态短等超时，仍按已受理返回", "card", id, "node", node,
				"watermark", watermark, "timeout", stepFirstStateTimeout)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"卡 %s 的节点 %s 已受理，首态未到；进展见 handoff card wait %s\n", id, node, id)
			return nil
		}
		slog.Default().Warn("读取 card step 首态失败", "card", id, "node", node, "cause", waitErr)
		return fmt.Errorf("读取 card step 首态: %w", waitErr)
	}
	if state.Dispatch != nil {
		slog.Default().Info("CLI 输出 card step dispatched 决议", "card", id, "node", node)
		writeStepDispatchResult(cmd.OutOrStdout(), id, node, *state.Dispatch)
		return nil
	}
	if state.FailureComment == "" {
		slog.Default().Error("派发失败首态缺少 comment 正文", "card", id, "node", node)
		return fmt.Errorf("card dispatch --step 派发失败首态缺少 comment 正文")
	}
	slog.Default().Warn("CLI 输出 card step 派发失败 comment", "card", id, "node", node)
	_, _ = fmt.Fprint(cmd.ErrOrStderr(), state.FailureComment)
	return fmt.Errorf("card dispatch --step 发现派发失败首态")
}
```

实现要点和固定顺序：

1. `openLedger` 与 `MaxSeq` 必须在 `cl.CardStep` 之前；`watermark` 是唯一首态边界。不得在 POST 后重新取水位，不得调用 task WS，不得扫卡上最后一条历史 `dispatched`。
2. `Follow` 的成员函数只返回当前卡号，`fromSeq` 使用 `watermark`，轮询间隔使用 `stepFirstStatePollInterval`。评论只缓存正文；`needs_human` 只有 reason 精确等于「派发失败」才终止。认领拒绝、运行锁占用和其它 needs reason 保持短等超时语义，本卡不扩展为 CLI 复述。
3. `haltForHuman` 的真实生产顺序是先写 comment、再写 `needs_human`；因此失败首态从上一个事件缓存 comment 正文。没有正文时 fail-closed 返回非零并记录上下文，不编造错误原因。
4. `CardStep` 的 404/400/409、纪律探活/拒发闸 400 和端点失败在 202 之前直接返回，不能进入短等。只有自身窗口的 `context.DeadlineExceeded` 才输出「已受理，首态未到」并返回 nil；调用方 context 取消和账本读错误返回非零。
5. 生产新增/改动函数头注释必须写明参数、返回、账本所有权、POST 前水位和超时边界；`Follow` 回调的 JSON 解码错误、账本打开/MaxSeq、HTTP、等待、格式化各错误分支保留 card/node/seq 或 watermark 上下文。成功的 202、发现 dispatched、发现派发失败、超时都用 `slog` 记录，不使用 `fmt.Println` 或裸 print。

### 2.4 文档同步

在 `skills/handoff/SKILL.md` 用下列完整新段替换当前 441 行起的三行 `--step` 说明，保留其后「本机卡派发省略 `--target`」段不变：

```markdown
- **`--step` 提交后先短等首态**：CLI 只把请求交给本机 agentd，HTTP 仍是 202，编排仍在
  agentd 里异步运行。CLI 在 POST 前记下本机账本 seq 水位，最多短等约 20 秒，只看这次水位
  之后的卡事件：看到 `dispatched` 就在 stdout 打出目标机、新分支 `branch`、起点分支
  `base`、起点短号和纪律块名；看到 reason=`派发失败` 的 `needs_human` 就把卡上
  `haltForHuman` 的 comment 正文写到 stderr 并以非零退出。HTTP 202 之前的 404/400/409、
  纪律探活/拒发闸 400 仍当场失败。短等窗口内没有这两类首态时，stdout 打「已受理，首态未到；
  进展见 handoff card wait」，退出 0；命令返回值不带回合结论。不要用 task WS 或卡上历史
  `dispatched` 推断这一次派发。CLI 与 agentd 必须同批升级。
```

在 `skills/handoff/SKILL.md` 用下列完整表格行替换当前 607 行，表格列和其余排障表保持不变：

```markdown
| `card dispatch --step` 已受理后短等超时、卡上仍无 `dispatched`/`派发失败` 首态 | 202 只代表请求已受理；编排仍在 agentd 异步运行，正常首态可能在约 20 秒窗口外；入口认领拒绝/运行锁占用会在卡上 comment + `needs_human` 留痕，ViaTemplate 派发失败也会落卡 | stdout 的「已受理，首态未到；进展见 card wait」是正常短等超时，跟 `card wait`；若短等捕获 reason=`派发失败`，stderr 会有卡上 comment 正文且命令非 0。认领/运行锁问题先读卡上 comment 的 holder/reason；需要接管时用 `card takeover`，不要把 agentd.log 当成唯一证据。 |
```

这两处只修仓内 handoff skill；不改仓外用户级 skill，不改 charter-review skill，不改文档中其它账本模式句。

### 2.5 Task 1 绿测、回归和验收

实现、注释和日志完成后依次运行：

```bash
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache gofmt -w cmd/card_node.go cmd/card_dispatch_test.go
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./cmd -run 'TestCardDispatchStep(ReturnsImmediately|ReportsNewDispatchFailure|ReportsNewDispatchSnapshot|FormatsEmptyBaseCommit|ExecutorWithoutTargetUsesLocalFirstState)$' -count=1
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./cmd -run 'Test(CardDispatchStep|CardStep)' -count=1
```

每支命令必须实际退出 0。行为验收逐条为：

- 水位前的旧 `dispatched` 不被当成本次成功；水位后的 `needs_human` reason=`派发失败` 使命令非 0，stderr 包含完整 comment 正文，且不出现旧快照短号。
- 水位后的 `dispatched` 成功行在 stdout 同时显示「本机」或目标名、新分支 `Branch`、起点 `Base`、sha 前七字节和 `DisciplineName`；空 Base/空 sha 使用固定无值文案；成功行不含 `origin`、`本地 ref` 或「目标机未定」。
- mock 仅回 202 且账本水位后没有两类首态时，stdout 含「已受理」「首态未到」「handoff card wait」，退出 0；`TestCardDispatchStepReturnsImmediately` 真实锁住这些字面。
- 只给 `--executor` 时 wire 仍保留 executor、target 仍是缺席/空值本机语义，输出不回滚到「目标机未定」。
- 现有 `CardStep` 线格式、`--plan` 拒绝和未知节点错误继续通过；本 task 不改变 HTTP 202 或客户端 Transport。

### 2.6 Task 1 的序列化边界、缺陷族和接缝覆盖

序列化边界清单：

| 边界 | 产生→消费 | 锁点 |
|---|---|---|
| `DispatchSnapshot` | `ledger.Store.RecordDispatch` marshal → `ledger.Event.Payload` → `waitStepFirstState` unmarshal → `writeStepDispatchResult` | `TestCardDispatchStepReportsNewDispatchSnapshot` 用真实 Store 写入并断言 `Target` 空值、`Branch`、`Base`、`BaseCommit`、`DisciplineName` 穿到 stdout；旧快照和新快照 seq 有明确先后 |
| 派发失败证据 | `AddComment` 的 `{body}` JSON + `MarkNeedsHuman` 的 `{reason}` JSON → `Follow` 先读 comment 后读 needs → stderr | `TestCardDispatchStepReportsNewDispatchFailure` 断言 comment 正文与非零；不把 reason 三字替代正文 |
| 现有请求 wire | `proto.CardStepReq` → `client.CardStep` HTTP JSON → mock handler | `TestCardDispatchStepExecutorWithoutTargetUsesLocalFirstState` 区分 target 缺键和 executor 有值；本卡不新增请求字段 |

缺陷族对抗结论：

| 缺陷族 | 设问与结论 |
|---|---|
| 生命周期/状态机中断 | POST 仍由 `CardStep` 负责，短等由带父 context 的 `Follow` 负责；自身超时只输出受理，取消/账本错误非零；不创建后台 goroutine，不改变 agentd 状态机。 |
| 静默失败/误导报错 | 202 后的 dispatched/派发失败首态立即可见；失败打印 comment 原文并非只打印 reason；旧事件被 POST 前水位排除；HTTP 同步拒绝继续原样返回。 |
| 跨平台假设 | 只消费账本事件和 POSIX-independent 字符串，不读本机文件系统、不拼 OS 路径、不依赖 hostname；空 target 是账本快照语义「本机」，不是机器名。 |
| 假红测试 | C7 测试从真实 `cardDispatchCmd`→`runStepDispatch` 进入，mock 只替换 HTTP 202，事件经真实 ledger JSON 写入；no-event 测试用可注入短窗口，不用固定 sleep。 |
| 门禁绕过 | `CardStep` 之前的 404/400/409/探活 400 仍由客户端错误路径挡住；短等不重新派发、不绕过纪律探活、不回退本地直跑。 |
| 序列化边界 | 三处真实 marshal/unmarshal/HTTP 边界列在上表；空字符串和字段缺席用 raw map 分开断言；没有新增 EventType 或请求枚举。 |
| 新增枚举值白名单 | 不新增事件/任务状态值；只比较既有 `EvComment`、`EvDispatched`、`EvNeedsHuman` 和固定 reason 文本。 |
| webview / 平台表现差异 | 本 task 无 Web 页面、浏览器 API 或平台 UI，不适用。 |

接缝双向矩阵：

| 声明缝 | 测试入口→缝 | 缝→测试 |
|---|---|---|
| `runStepDispatch` ← `cardDispatchCmd` (`cmd/card_dispatch.go:280`) | 上述五支测试全由 `runLedgerCLI(... "card", "dispatch", ..., "--step", ...)` 进入 `cardDispatchCmd`；没有直接调用 `waitStepFirstState` 的内部锁替代 | `TestCardDispatchStepReturnsImmediately` 锁超时；`TestCardDispatchStepReportsNewDispatchFailure` 锁失败非零/水位隔离；`TestCardDispatchStepReportsNewDispatchSnapshot` 锁成功字段；`TestCardDispatchStepFormatsEmptyBaseCommit` 锁空值；`TestCardDispatchStepExecutorWithoutTargetUsesLocalFirstState` 锁 B271 本机语义 |

没有内部锁测试顶替声明缝；`setStepFirstStateTestWindow` 和 ledger 写入辅助函数只提供夹具，不单独宣称行为覆盖。

## 3. Task 2：C8 review 只读机械闸

### 3.1 基线和最小测试范围

1. 实现前重跑：

```bash
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./internal/ledgerstep -run 'TestReviewStepPassAndFailLoop|TestNodeStepWithoutProducesDoesNotInvokeOutputHooks' -count=1
```

预期退出 0，输出 `ok github.com/Xsxdot/handoff/internal/ledgerstep ...`。本 task 只跑 `./internal/ledgerstep` 的命名测试和包测试。

2. `NodeStep.RunOnce` 是真实声明缝；测试复用 `nodeLedger(t)`、`nodePassMessage()` 和既有 `seedLedgerStepStore`，每支测试都从 `RunOnce` 进入。不得把白名单 helper 作为唯一接缝。真实 `Client.Diff` JSON→`ChangedPaths` 的存量边界由 `TestRunnerLocalClientUsesWaitAndDiffWire` 保留锁定：该测试的任务 diff HTTP 接口返回真实 JSON，断言 `runner.diffNode()` 产出 `docs/out.md` 且 `diffHits == 1`；本卡不复制第二套 HTTP 夹具。

### 3.2 红测：purpose、Diff、落账与路由

在 `internal/ledgerstep/node_test.go` import 中追加 `encoding/json` 和 `errors`。加入下面完整测试辅助函数和五支测试：

```go
func newReviewReadOnlyStep(t *testing.T, st *ledger.Store, card ledger.Card,
	diff func(context.Context, string, string) ([]string, error)) *NodeStep {
	t.Helper()
	return &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "review-guard", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
			Override: ledger.NodeOverride{Purpose: ledger.PurposeReview},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "target", "task-review-guard", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		Diff: diff,
	}
}

func reviewPassValues(t *testing.T, st *ledger.Store, cardID string) []bool {
	t.Helper()
	events, err := st.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatalf("读 review_verdict 事件: %v", err)
	}
	var values []bool
	for _, event := range events {
		if event.Type != ledger.EvReviewVerdict {
			continue
		}
		var payload struct { Pass *bool `json:"pass"` }
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("解码 review_verdict: %v", err)
		}
		if payload.Pass == nil {
			t.Fatalf("review_verdict 缺 pass 键: %s", event.Payload)
		}
		values = append(values, *payload.Pass)
	}
	return values
}

func TestNodeStepReviewPurposeAllowsEmptyDiff(t *testing.T) {
	st, card := nodeLedger(t)
	called := false
	step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
		called = true
		return nil, nil
	})
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("空 diff 应 pass: err=%v out=%+v", err, out)
	}
	if !called {
		t.Fatal("purpose=review 且 pass 时必须调用 Diff，即使 Diff 返回空列表")
	}
	values := reviewPassValues(t, st, card.ID)
	if len(values) != 1 || !values[0] {
		t.Fatalf("空 diff 的 review_verdict = %v，want [true]", values)
	}
}

func TestNodeStepReviewPurposeAllowsLedgerPaths(t *testing.T) {
	paths := []string{
		"docs/superpowers/ledgers/foo.md", "docs/ledgers/foo.md",
		"docs/superpowers/ledgers", "docs/ledgers",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			st, card := nodeLedger(t)
			step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
				return []string{path}, nil
			})
			out, err := step.RunOnce(context.Background(), card.ID)
			if err != nil || out.Action != ActionPass {
				t.Fatalf("白名单路径 %q 应 pass: err=%v out=%+v", path, err, out)
			}
			values := reviewPassValues(t, st, card.ID)
			if len(values) != 1 || !values[0] {
				t.Fatalf("白名单路径 %q 的 review_verdict = %v，want [true]", path, values)
			}
		})
	}
}

func TestNodeStepReviewPurposeRejectsOutOfBoundsPaths(t *testing.T) {
	st, card := nodeLedger(t)
	step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
		return []string{"docs/ledgers/allowed.md", "docs/ledgers-extra/bad.md", "internal/old.go", "internal/new.go"}, nil
	})
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionContinue {
		t.Fatalf("越界 diff 应按 on_fail 继续: err=%v out=%+v", err, out)
	}
	got, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读越界 review 卡: %v", err)
	}
	if got.Status != ledger.StatusDoing {
		t.Fatalf("越界 review 应路由到 OnFail 进行中，实际 %q", got.Status)
	}
	values := reviewPassValues(t, st, card.ID)
	if len(values) != 1 || values[0] {
		t.Fatalf("越界 review_verdict 必须只有 pass=false，实际 %v", values)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatalf("读越界 review 事件: %v", err)
	}
	commentFound := false
	for _, event := range events {
		if event.Type != ledger.EvComment { continue }
		var payload struct { Body string `json:"body"` }
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("解码越界 comment: %v", err)
		}
		if strings.Contains(payload.Body, "docs/ledgers-extra/bad.md") &&
			strings.Contains(payload.Body, "internal/old.go") &&
			strings.Contains(payload.Body, "internal/new.go") {
			commentFound = true
		}
	}
	if !commentFound { t.Fatal("越界 review 必须写普通评论并列出每条越界路径") }
}

func TestNodeStepReviewPurposeDiffFailureDoesNotRecordVerdict(t *testing.T) {
	cases := []struct {
		name string
		diff func(context.Context, string, string) ([]string, error)
	}{
		{name: "nil diff", diff: nil},
		{name: "diff error", diff: func(context.Context, string, string) ([]string, error) {
			return nil, errors.New("diff backend unavailable")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, card := nodeLedger(t)
			step := newReviewReadOnlyStep(t, st, card, tc.diff)
			beforeEvents, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
			if err != nil { t.Fatalf("读 Diff 失败前事件: %v", err) }
			beforeRounds := CountRounds(beforeEvents, "review-guard")
			out, err := step.RunOnce(context.Background(), card.ID)
			if err != nil || out.Action != ActionNeedsHuman {
				t.Fatalf("Diff 失败必须 needs_human: err=%v out=%+v", err, out)
			}
			if !strings.Contains(out.Reason, "读取审阅改动失败") {
				t.Fatalf("Reason = %q，缺少审阅 diff 失败语义", out.Reason)
			}
			if values := reviewPassValues(t, st, card.ID); len(values) != 0 {
				t.Fatalf("Diff 失败不得写 review_verdict，实际 %v", values)
			}
			reason, err := st.NeedsOf(card.ID)
			if err != nil { t.Fatalf("读 needs_human: %v", err) }
			if reason != "读取审阅改动失败" { t.Fatalf("needs_human reason = %q", reason) }
			got, err := st.GetCard(card.ID)
			if err != nil { t.Fatalf("读 Diff 失败卡: %v", err) }
			if got.Status != ledger.StatusTodo { t.Fatalf("Diff 失败不得路由到 OnFail，status=%q", got.Status) }
			afterEvents, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
			if err != nil { t.Fatalf("读 Diff 失败后事件: %v", err) }
			if afterRounds := CountRounds(afterEvents, "review-guard"); afterRounds != beforeRounds {
				t.Fatalf("Diff 失败不得增加裁决轮次: before=%d after=%d", beforeRounds, afterRounds)
			}
		})
	}
}

func TestNodeStepNameReviewWithoutPurposeKeepsLegacyNoDiff(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "target", "legacy-review-task", nil
		},
		Await: func(context.Context, string, string) (string, error) { return nodePassMessage(), nil },
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("Name=review 且 purpose 空的存量行为必须 pass: err=%v out=%+v", err, out)
	}
}

func TestNodeStepImplementPurposeDoesNotRunReviewGate(t *testing.T) {
	st, card := nodeLedger(t)
	called := false
	step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
		called = true
		return []string{"internal/production.go"}, nil
	})
	step.Node.Override.Purpose = ledger.PurposeImplement
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("purpose=implement 不应触发 review 闸: err=%v out=%+v", err, out)
	}
	if called {
		t.Fatal("purpose=implement 不应调用 Diff")
	}
	values := reviewPassValues(t, st, card.ID)
	if len(values) != 1 || !values[0] {
		t.Fatalf("purpose=implement 的裁决应保持 pass=true，实际 %v", values)
	}
}
```

红测范围只触及 `./internal/ledgerstep`。`TestNodeStepReviewPurposeRejectsOutOfBoundsPaths` 从 `RunOnce` 真实走到 `RecordReviewVerdict`、`AddComment`、`routeTo`，断言落账布尔、事件顺序和 on-fail 路由；`TestNodeStepReviewPurposeDiffFailureDoesNotRecordVerdict` 覆盖 nil 与错误两种分支，并用 `reviewPassValues` 断言零条裁决事件；`TestNodeStepNameReviewWithoutPurposeKeepsLegacyNoDiff` 是接缝负例而不是白名单 helper 内部锁。

### 3.3 最小实现：在 RecordReviewVerdict 前插入 review Diff 闸

在 `internal/ledgerstep/node.go` 保留现有 import（`strings` 已存在），在 `RunOnce` 的 `ParseVerdict` 成功、salvage comment 完成之后、现有 `if err := n.gatedWrite("裁决落账")` 之前插入下方完整代码。并在文件中加入下方两个私有 helper；不要移动现有 produces 精确匹配块，不要把闸挂在 `Produces != nil` 条件上：

```go
var reviewLedgerPathPrefixes = [...]string{
	"docs/superpowers/ledgers",
	"docs/ledgers",
}

func isReviewLedgerPath(path string) bool {
	for _, prefix := range reviewLedgerPathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func reviewReadOnlyViolations(paths []string) []string {
	violations := make([]string, 0)
	for _, path := range paths {
		if !isReviewLedgerPath(path) {
			violations = append(violations, path)
		}
	}
	return violations
}
```

```go
	// review 节点的只读闸必须在 RecordReviewVerdict 前取 Diff：Diff 失败不应
	// 消耗轮次，越界则把内存 verdict 改为 fail 后走已有 on_fail 路由。
	if verdict.Pass && n.Node.Override.Purpose == ledger.PurposeReview {
		logger.Info("开始校验 review 节点只读改动", "target", target, "task", taskID)
		if n.Diff == nil {
			err := fmt.Errorf("review 节点 diff 依赖未装配")
			logger.Error("读取审阅改动失败", "target", target, "task", taskID, "cause", err)
			return n.haltForHuman(cardID, "读取审阅改动失败",
				"本节点无法确认审阅轮是否只读：\n"+err.Error())
		}
		changedPaths, diffErr := n.Diff(ctx, target, taskID)
		if diffErr != nil {
			logger.Warn("读取审阅改动失败", "target", target, "task", taskID, "cause", diffErr)
			return n.haltForHuman(cardID, "读取审阅改动失败",
				"本节点无法确认审阅轮是否只读：\n"+diffErr.Error())
		}
		violations := reviewReadOnlyViolations(changedPaths)
		if len(violations) == 0 {
			logger.Info("review 节点只读改动通过", "target", target, "task", taskID,
				"changed_paths", changedPaths)
		} else {
			verdict.Pass = false
			body := "审阅节点检测到白名单外改动，按 fail 处理；越界路径：\n" +
				strings.Join(violations, "\n")
			if err := n.gatedWrite("审阅只读违规留痕"); err != nil {
				logger.Warn("审阅只读违规留痕被写闸拒绝", "target", target, "task", taskID,
					"paths", violations, "cause", err)
				return Outcome{}, err
			}
			if _, err := n.St.AddComment(cardID, body, "普通", n.actor()); err != nil {
				logger.Warn("审阅只读违规评论写入失败", "target", target, "task", taskID,
					"paths", violations, "cause", err)
				return Outcome{}, err
			}
			logger.Warn("review 节点只读闸未通过，按 fail 路由", "target", target, "task", taskID,
				"out_of_scope_paths", violations)
		}
	}

	if err := n.gatedWrite("裁决落账"); err != nil {
		return Outcome{}, err
	}
	if err := n.St.RecordReviewVerdict(cardID, n.Node.Name, verdict.Pass, verdict.Raw, n.actor()); err != nil {
		return Outcome{}, err
	}
```

上面的最后两个 `if` 是现有代码的原样上下文，替换时不得重复保留旧副本。固定行为如下：

1. 触发条件是 `n.Node.Override.Purpose == ledger.PurposeReview && verdict.Pass`。`Name == "review"`、模板 `Purpose == review` 或 `Produces == nil` 都不能单独触发。
2. `Diff == nil` 或返回 error 均调用 `haltForHuman(cardID, "读取审阅改动失败", ...)`，不写普通裁决、不写 `review_verdict`、不增加回合、不走 `OnFail`；comment 包含错误原文。
3. 空列表、`docs/superpowers/ledgers`、`docs/superpowers/ledgers/` 下路径、`docs/ledgers`、`docs/ledgers/` 下路径均放行。判断用 POSIX 字符串精确等于或带尾斜杠前缀，不做 OS 分隔符转换。任何其它路径，包括 `*_test.go`、生产代码、skill、workflow 和 rename 任意越界一侧，都进入 violations；不去重、不排序，按 Diff 返回顺序原样列评论。
4. 越界时只在内存中把 `verdict.Pass` 改为 false，先写一条普通评论，再调用既有 `RecordReviewVerdict(..., false, ...)`，然后让现有 `ClearNeedsHumanFrom` 和 `routeTo(OnFail)` 继续执行。不得先落 `pass=true` 再补一条 false，也不得调用 `haltForHuman`。
5. 新 helper 旁写注释说明白名单尾斜杠边界和不转换路径的原因；`RunOnce` 注释补充「review Diff 闸在裁决落账前」。每条错误和成功分支带 node/card/target/task/paths 的 `slog` 结构化字段；禁止 print。

### 3.4 Task 2 绿测、回归和验收

实现后运行：

```bash
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache gofmt -w internal/ledgerstep/node.go internal/ledgerstep/node_test.go
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./internal/ledgerstep -run 'TestNodeStepReviewPurpose|TestNodeStepNameReviewWithoutPurposeKeepsLegacyNoDiff|TestReviewStepPassAndFailLoop|TestNodeStepWithoutProducesDoesNotInvokeOutputHooks' -count=1
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./internal/ledgerstep -count=1
```

每支命令必须实际退出 0。行为验收逐条为：

- `purpose=review` 的空 Diff 和四种台账路径形态 pass，并各写一个 `review_verdict.pass=true`；真实 `Diff` 函数被调用。
- 混合 Diff 中的生产/测试路径不放行：普通评论逐条列出越界路径，卡到 `OnFail`，事件流只有一条 `review_verdict` 且 `pass=false`，不存在先 true 后 false。
- Diff nil 和 Diff error 均为 `ActionNeedsHuman`，reason 是「读取审阅改动失败」，comment 保留原文，零条 `review_verdict`，卡不移动到 `OnFail`。
- `Purpose=implement` 即使 Diff 返回生产路径也继续 pass 且不调用 Diff；`Name=review` 但 `Override.Purpose=""` 且 Diff=nil 继续 pass；`TestReviewStepPassAndFailLoop` 和 `TestNodeStepWithoutProducesDoesNotInvokeOutputHooks` 继续绿，且 legacy 节点不调用 Diff。
- `TestRunnerLocalClientUsesWaitAndDiffWire` 继续以真实任务 diff HTTP JSON→`ChangedPaths` 断言 `docs/out.md` 和 `diffHits==1`；C8 不新增或绕过 Client.Diff 传输。

### 3.5 Task 2 的序列化边界、缺陷族和接缝覆盖

本 task 不新增 wire 字段、EventType、TaskState 或跨语言 DTO；仍须锁住新增的「已有字段从路径产生到事件落账」行为：

| 边界 | 产生→消费 | 锁点 |
|---|---|---|
| Diff 路径投影 | 真实 `Client.Diff` JSON → `runner.diffNode` → `ChangedPaths` → `NodeStep.Diff` | 存量 `TestRunnerLocalClientUsesWaitAndDiffWire` 真实 HTTP JSON 断言；它保证路径已剥 `a/`/`b/` 且为仓内 POSIX 相对路径 |
| review verdict | `ParseVerdict` 的内存 `Pass` → 越界闸改为 false → `RecordReviewVerdict` marshal 的 `{pass}` → `EventsFromAsc` unmarshal | `TestNodeStepReviewPurposeRejectsOutOfBoundsPaths` 用 `*bool` 区分缺键/false，断言事件序列没有 true；空/台账测试断言 true |
| 只读违规 comment | violations 列表 → `AddComment` 的 `{body}` JSON → `EventsFromAsc` → comment body | 越界测试从事件 payload 解码并同时查找两条越界路径，不能只断言内存字符串 |

缺陷族对抗结论：

| 缺陷族 | 设问与结论 |
|---|---|
| 生命周期/状态机中断 | Diff 闸只在 `Await` 和 ParseVerdict 成功后运行；Diff 错误用既有 `haltForHuman`，不留下半个 verdict；越界沿既有 false/on-fail 路由；不启新 goroutine、不改变运行锁。 |
| 静默失败/误导报错 | review pass 不再静默跳过 Diff；nil/error 明确 needs-human 且 comment 有 cause；越界评论列全部路径，落账布尔与路由一致。 |
| 跨平台假设 | 白名单只接受协议已定义的仓内 POSIX 路径，以字符串前缀+尾斜杠边界判断，不调用 `filepath`，避免 Windows 分隔符改变规则。 |
| 假红测试 | 行为测试从 `NodeStep.RunOnce` 声明缝进入，使用真实 ledger 事件读回；真实 Client.Diff JSON 边界保留既有 HTTP harness；不以只测 `isReviewLedgerPath` 代替接缝。 |
| 门禁绕过 | 闸发生在 `RecordReviewVerdict` 之前；越界不能先记 true；Diff 失败不能靠 `Produces` 为空绕过；既有 WriteGate 在新 comment 和旧裁决写入点均有效。 |
| 序列化边界 | `*bool` 断言区分字段缺失与 false；真实 Diff JSON、comment JSON、review_verdict JSON 各有入口或存量断言；无新枚举。 |
| 新增枚举值白名单 | 不新增枚举；复用 `ledger.PurposeReview`、`ledger.EvReviewVerdict` 和 `ActionNeedsHuman/ActionContinue/ActionPass`，所有 switch 保持既有词表。 |
| webview / 平台表现差异 | 无 Web 页面、浏览器 API 或平台 UI，不适用。 |

接缝双向矩阵：

| 声明缝 | 测试入口→缝 | 缝→测试 |
|---|---|---|
| `NodeStep.RunOnce` ← `StepRunner.Run` (`internal/ledgerstep/runner.go:249`) | `TestNodeStepReviewPurposeAllowsEmptyDiff`、`TestNodeStepReviewPurposeAllowsLedgerPaths`、`TestNodeStepReviewPurposeRejectsOutOfBoundsPaths`、`TestNodeStepReviewPurposeDiffFailureDoesNotRecordVerdict`、`TestNodeStepNameReviewWithoutPurposeKeepsLegacyNoDiff`、`TestNodeStepImplementPurposeDoesNotRunReviewGate` 均直接调用 `step.RunOnce`；它们不把私有 helper 当入口 | 空 Diff、白名单四边界、越界 false/on-fail、Diff nil/error、Name review purpose 空、Purpose implement 六组覆盖完整；存量 `TestRunnerLocalClientUsesWaitAndDiffWire` 锁真实 Diff 投影 |

`reviewReadOnlyViolations` 仅作为 RunOnce 内部映射，不设独立测试入口；从声明缝构造直接断言路径白名单的完整行为，满足「内部锁不能顶替接缝」约束。

## 4. 跨 task 收口、真机清单与自审

### 4.1 协调者执行的跨 task 总闸

下面的命令只在 Task 1 与 Task 2 都完成后由协调者执行；它们不拆成任一 task 的局部判据，也不派发驱动派发系统自身的检查：

```bash
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./cmd -count=1
env GOMODCACHE=/root/.handoff/tmp/1031b0e7/gomodcache go test ./internal/ledgerstep -count=1
git diff --check
```

预期三条命令均退出 0。前两条分别覆盖 C7 的 CLI/HTTP 接缝和 C8 的 ledgerstep 接缝；第三条检查实现与文档提交没有空白错误。全仓测试不归属任何单个 task，本卡不把它伪装成 task 局部判据。

### 4.2 C7 边界域真机清单

协调者须在合并前用当前构建的 CLI 和真实本机 agentd 执行下列清单，并把每项实际 stdout/stderr、退出码和卡上新增事件追加到 `docs/superpowers/specs/b286-ledger.md`；没有真实结果的项目写「未验证」：

1. 先执行 `go run . card list --json`，从结果取得一张已钉住可执行节点的真实卡号和节点名，再把这两个真实值分别传给 `go run . card show` 和 `go run . card dispatch --step`。成功首态必须在 stdout 同时出现目标（空 target 显示本机）、`branch`、`base`（空值显示无起点分支）、`base_commit` 的七位短值或无 sha、纪律块名；不得出现本地 ref 或 origin 标签。
2. 对显式 executor 目标重复派发，确认 stdout 的目标字段为该 executor；对空 executor 目标确认目标字段为本机语义；两次均核对 HTTP 仍为 202 受理且命令没有同步等待整轮执行。
3. 制造或复现 agentd 受理后短窗口内无事件的情况，确认 stdout 精确包含 `已受理，首态未到；进展见 card wait`，退出码为 0，且卡上没有被 CLI 伪造的首态事件。
4. 让 agentd 在 watermark 之后产生 `needs_human` 派发失败，确认 stderr 是 `haltForHuman` 的完整 comment body（首行 `本节点派发失败：`，后跟原始错误文本），退出码非 0，且 stdout 不把旧 watermark 之前的 dispatched 当成新结果。
5. 分别用不存在的卡号、未知节点名、进行中的重复派发、纪律探针拒绝，确认原有 404/400/409 同步拒绝仍保持；每个失败场景的原始输出都写入台账。

### 4.3 C8 边界域真机清单

协调者须在真实 ledgerstep 执行中逐项核对，并把事件序列和最终卡状态写入台账：

1. review override 且 diff 只包含 `docs/superpowers/ledgers/` 下文件时，确认先记录 `review_verdict.pass=true`，再继续原有成功路由；空 diff 也必须成功。
2. review override 且 diff 只包含 `docs/ledgers/` 下文件时，确认同样成功；相对路径必须按已剥离 `a/`、`b/` 前缀的形式检查。
3. review override 且 diff 含 `docs/superpowers/ledgers/` 之外的文件、恰好命中两个 ledger 目录名但带额外前缀、目录本身以外的测试/生产/skill/workflow 文件，确认没有 `review_verdict.pass=true`，只记录 `pass=false` 的普通失败 verdict，沿既有 `ClearNeedsHumanFrom`/`OnFail` 路由结束，不产生 haltForHuman。
4. review override 且 Diff 返回 error 或 nil 时，确认记录 `读取审阅改动失败` 的 haltForHuman，卡进入 needs-human，且没有 review verdict、没有 round increment。
5. 节点名为 `review` 但 `Node.Override.Purpose` 为空时，确认即使 Diff 为 nil 也仍按旧成功路径；无 Produces 的普通节点确认不会调用 output hooks。

### 4.4 spec 故事归属与接缝双向核对

| spec 故事 | 具体锁点 |
| --- | --- |
| C7 202 异步受理与无首态超时 | Task 1 的 `TestCardDispatchStepReturnsImmediately` |
| watermark 后的新 dispatched 首态及字段投影 | Task 1 的 `TestCardDispatchStepReportsNewDispatchSnapshot` |
| watermark 后的 needs-human 派发失败 | Task 1 的 `TestCardDispatchStepReportsNewDispatchFailure` |
| executor 目标与空目标本机语义 | Task 1 的 `TestCardDispatchStepReportsNewDispatchSnapshot`、`TestCardDispatchStepExecutorWithoutTargetUsesLocalFirstState` |
| 原有同步拒绝不变 | Task 1 的 `TestCardDispatchStepRejectsSynchronousFailure` |
| review 仅由 PurposeReview 触发 | Task 2 的 `TestNodeStepReviewPurposeAllowsEmptyDiff`、`TestNodeStepReviewPurposeAllowsLedgerPaths` 与 `TestNodeStepNameReviewWithoutPurposeKeepsLegacyNoDiff` |
| review ledger 路径白名单及空 diff | Task 2 的 `TestNodeStepReviewPurposeAllowsLedgerPaths`、`TestNodeStepReviewPurposeAllowsEmptyDiff` |
| 非白名单 diff 的普通失败路由 | Task 2 的 `TestNodeStepReviewPurposeRejectsOutOfBoundsPaths` |
| Diff nil/error 的 haltForHuman 与无 verdict | Task 2 的 `TestNodeStepReviewPurposeDiffFailureDoesNotRecordVerdict` |
| 普通节点无 Produces 不触发输出钩子 | Task 2 的既有 `TestNodeStepWithoutProducesDoesNotInvokeOutputHooks` |

接缝双向核对结果必须同时成立：Task 1 的每支测试入口都是 `cardDispatchCmd` 经 `runStepDispatch` 的 CLI 接缝；Task 2 的每支新增测试入口都是 `NodeStep.RunOnce` 经 `StepRunner.Run` 的 ledgerstep 接缝；反向逐条覆盖 `runStepDispatch` 的请求/事件/输出、`NodeStep.RunOnce` 的 verdict/diff/route 三组边界。内部映射 `reviewReadOnlyViolations` 不单独充当接缝锁。

### 4.5 序列化、缺陷族与计划自检

- C7 的序列化边界逐一锁定：`proto.CardStepReq` JSON 请求体由 `cardStepBody` 断言；ledger `dispatch`/`comment`/`needs_human` event payload 由真实 store 写入后读取；CLI stdout 是从事件 snapshot 手工投影的最后一层。测试分别区分空字符串（本机/无起点/无 sha）与非空值，`base_commit` 断言七位短值。
- C8 的序列化边界锁定 `RecordReviewVerdict` 写入的 `review_verdict.pass`：测试从真实 ledger 事件读取 `pass=true` 或 `pass=false`，并断言 nil/error 路径没有该事件；`ChangedPaths` 的 `a/`、`b/` 归一化由已有 runner harness 覆盖，不在本卡另造协议。
- 缺陷族对抗审查结论：并发/重复派发由同步 409 场景覆盖；旧 watermark 事件污染由旧 dispatched + 新 failure 场景覆盖；空值与短 sha 投影由 C7 stdout 断言覆盖；review 错误、nil、空 diff、目录前缀误放行、rename 任一侧越界由 Task 2 的表驱动断言覆盖；失败路由和事件顺序由 ledger 事件断言覆盖；文档契约由本计划和台账收口。
- 上下文预算有界：Task 1 仅涉及 `cmd/card_node.go`、`cmd/card_dispatch.go`、`cmd/card_dispatch_test.go`、`cmd/ledgercli_test.go`、`internal/ledger` 的既有 API 与 `skills/handoff/SKILL.md`；Task 2 仅涉及 `internal/ledgerstep/node.go`、`internal/ledgerstep/node_test.go` 及已有 `runner_test.go` harness。任何超出清单的依赖变化必须先回到协调者审查。
- 计划自检要求：执行 `rg -n '未使用计划占位文本' docs/superpowers/plans/b286-plan.md` 不得命中；逐节确认每个 task 都有基线命令、最小测试范围、日志、注释、完整测试代码、Consumes/Produces 精确签名；确认本文件只描述实现与验证，不写实现代码。
