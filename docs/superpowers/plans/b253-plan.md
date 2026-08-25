# B253 实现计划：card wait 建连先输出卡快照

> 上游规格：docs/superpowers/specs/2026-08-25-b253-card-wait-snapshot.md（已批准）
> 基线：deb3d461（claude/archive-247-close-252-254-4fe606 的合并目标；当前执行分支 cards/B253-charter）
> 产出范围：只改 cmd 的卡等待命令及其命令级测试；不改 ledger API、事件回放、task wait 或 --step 202 响应。
> 台账：docs/superpowers/ledgers/2026-08-25-b253-plan-ledger.md

## 0. 开工前已核对的事实

### 0.1 规格与现状

规格把唯一接缝定为 cmd/card_wait.go#runCardWait 的 stdout 逐行 JSON。当前实现的实际顺序是：

1. openLedger()；
2. GetCard(cardID)；
3. MaxSeq() 得到 start；
4. 首次 checkDone()；
5. Store.Follow(ctx, members, start, 2*time.Second, onEvent)，事件回调直接 json.Encoder.Encode(ledger.Event)。

因此 start 之前的卡状态没有任何 stdout 表示。实现要在第 3 步之后、第 4 步之前输出建连快照；这样后续 Follow 仍以同一个 start 为排他水位，快照期间新落的事件仍能由现有跟随逻辑读取。

internal/ledger.Store.NeedsOf(cardID string) (string, error) 按 needs_human / needs_cleared 的 seq 顺序回放，返回当前生效的最近原因；无标记或已清除返回空串。Store.Subtree(rootID string) ([]string, error) 返回根、所有 parent 后代和并入成员，但当前集合来自 map，顺序不稳定；快照输出会单独排序，动态跟随成员的原有重算语义不变。

### 0.2 基线判据实跑结果

以下命令已经在未改实现的基线上实跑，结果写入台账；实现者开工时必须重跑并核对：

~~~text
go test ./cmd/ -run 'TestCardWaitSubtreeExitsWhenAllDone|TestBacklogSummaryLineIsSingleJSON' -count=1
原始输出：ok  github.com/Xsxdot/handoff/cmd  2.139s
exit 0

go test ./cmd/ -run 'TestCardWait|TestBacklogSummaryLineIsSingleJSON|TestWaitRejectsCardFlag' -count=1
原始输出：ok   github.com/Xsxdot/handoff/cmd  2.177s
exit 0

go vet ./cmd
原始 stdout：空
exit 0
~~~

本卡实现判据只触及 cmd 包，不能把未跑过的全仓测试写成基线事实；全仓测试不归本 task。

### 0.3 代码图与覆盖债

仓内有 codegraph/，已按最优树词表查询 d_cli 与 d_ledger。两次 context 都报告领域声明缺失（codegraph/domains/d_cli.json、codegraph/domains/d_ledger.json）；d_cli 有 fociTruncated.total=87, shown=5，d_ledger 有 fociTruncated.total=57, shown=5。chain runCardWait --with-source 只确认了 runCardWait → openLedger、runCardWait → Store.GetCard，并报告 unscannedEntries=6；who-calls Store.NeedsOf 当前无调用者节点，不能解释为无调用面。相关查询、原始告警和后续源码核对已记台账，本计划不把图空边当作调用面结论。

## 1. 文件边界与接口

### 1.1 有界文件集

生产文件：cmd/card_wait.go。

测试文件：cmd/card_wait_test.go。

不修改：internal/ledger/events.go、internal/ledger/derived.go、internal/ledger/types.go、internal/ledger/follow.go、cmd/wait.go、internal/client/*。

### 1.2 Consumes

以下签名是当前基线源码中的精确签名，实现不得改名或改参数：

~~~go
func openLedger() (*ledger.Store, error)
func (s *ledger.Store) GetCard(id string) (ledger.Card, error)
func (s *ledger.Store) MaxSeq() (int64, error)
func (s *ledger.Store) NeedsOf(cardID string) (string, error)
func (s *ledger.Store) Subtree(rootID string) ([]string, error)
func (s *ledger.Store) Follow(
	ctx context.Context,
	members func() ([]string, error),
	fromSeq int64,
	pollInterval time.Duration,
	onEvent func(ledger.Event) error,
) error
~~~

还消费 ledger.Card.ID、ledger.Card.Status、ledger.StatusDone、ledger.StatusClosed、cmd.OutOrStdout()、cmd.ErrOrStderr()、encoding/json.Encoder 和项目现有 log/slog / logx.Setup。

### 1.3 Produces

保留现有入口签名：

~~~go
func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error
~~~

新增仅供 cmd stdout 使用的私有线型：

~~~go
const cardSnapshotType = "card_snapshot"

type cardSnapshotLine struct {
	Type        string `json:"type"`
	CardID      string `json:"card_id"`
	Status      string `json:"status"`
	NeedsHuman  bool   `json:"needs_human"`
	NeedsReason string `json:"needs_reason"`
}
~~~

每个建连成员恰好输出一行，字段永远存在（包括 needs_human:false 和 needs_reason:""），字段含义固定如下：

~~~json
{"type":"card_snapshot","card_id":"B253","status":"进行中","needs_human":true,"needs_reason":"派发瞬时失败"}
~~~

needs_human 是 NeedsOf 返回值非空的布尔投影；needs_reason 是 NeedsOf 返回的当前生效原因，已清除或从未打标时为空串。快照不落账、不推进 seq、不参与 Follow 回调；之后的事件行仍是既有 ledger.Event JSON。

### 1.4 接缝覆盖清单

本计划只有规格声明的一条接缝：card wait stdout 线格式。

- 缝 → 测试：TestCardWaitEmitsSnapshotBeforeTimeout 通过 runLedgerCLI(..., "card", "wait", ...) 进入 Cobra 的 card wait，覆盖单卡首行及 needs 字段；TestCardWaitSubtreeExitsWhenAllDone 通过同一入口覆盖子树逐成员首行及快照后事件跟随。
- 测试 → 缝：两支测试的入口都是 runLedgerCLI → cardWaitCmd.RunE → runCardWait，不是直接调用内部快照 helper；两支均解析真实 stdout 的每一行。
- 内部锁：无。测试辅助函数只解析真实 CLI 输出，不替代声明缝断言。

## 2. Task 1：卡快照生产、逐行线格式与命令级回归

### 文件集

cmd/card_wait.go、cmd/card_wait_test.go。

### 步骤 1：重跑基线判据并记录原始输出

在任何编辑前运行 §0.2 的三条命令。若输出与台账不一致，停止实现并以单行 JSON 提问，不带着漂移基线继续。实现者必须把实际输出追加到 docs/superpowers/ledgers/2026-08-25-b253-plan-ledger.md。

测试范围仍只限 ./cmd/；本 task 不运行全仓测试作为单 task 判据。

### 步骤 2：【红】先写接缝测试，验证真实 stdout 而非内部对象

在 cmd/card_wait_test.go：

1. 将 import 整体整理为下方完整代码块；
2. 用下方完整代码替换现有 TestCardWaitSubtreeExitsWhenAllDone；
3. 在文件末尾追加下方完整的 cardWaitJSONLines、cardWaitSnapshot、readCardWaitSnapshot 和 TestCardWaitEmitsSnapshotBeforeTimeout；
4. 保留现有 TestWaitRejectsCardFlag 原文不动。

~~~go
import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)
~~~

~~~go
func TestCardWaitSubtreeExitsWhenAllDone(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "根卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建根卡: %v", err)
	}
	var root struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &root); err != nil {
		t.Fatalf("解根卡: %v", err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "split", root.ID, "子卡")
	if err != nil {
		t.Fatalf("拆子卡: %v", err)
	}
	var child struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &child); err != nil {
		t.Fatalf("解子卡: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond)
		st, err := ledger.Open(dir + "/ledger.db")
		if err != nil {
			t.Error(err)
			return
		}
		defer st.Close()
		for _, id := range []string{child.ID, root.ID} {
			if err := st.MoveCard(id, "进行中", "", "test"); err != nil {
				t.Error(err)
			}
			if err := st.MoveCard(id, "待审阅", "", "test"); err != nil {
				t.Error(err)
			}
			if err := st.MoveCard(id, "已完成", "", "test"); err != nil {
				t.Error(err)
			}
		}
	}()
	waitOut, _, waitErr := runLedgerCLI(t, dir, "card", "wait", root.ID, "--subtree", "--timeout", "15s")
	wg.Wait()
	if waitErr != nil {
		t.Fatalf("wait 应正常退出: %v", waitErr)
	}

	lines := cardWaitJSONLines(t, waitOut)
	if len(lines) < 3 {
		t.Fatalf("快照后至少应有事件行: %q", waitOut)
	}
	wantIDs := []string{root.ID, child.ID}
	sort.Strings(wantIDs)
	for i, id := range wantIDs {
		snapshot := readCardWaitSnapshot(t, lines[i])
		if snapshot.CardID != id {
			t.Fatalf("第 %d 条快照 card_id=%q, want %q", i, snapshot.CardID, id)
		}
		if snapshot.Status != "待办" {
			t.Fatalf("第 %d 条快照 status=%q, want 待办", i, snapshot.Status)
		}
		if snapshot.NeedsHuman || snapshot.NeedsReason != "" {
			t.Fatalf("无 needs 的快照=%+v, want false/空串", snapshot)
		}
	}
	for i, line := range lines[2:] {
		if _, ok := line["seq"]; !ok {
			t.Fatalf("快照之后第 %d 条不是事件行（缺 seq）: %v", i, line)
		}
		var typ string
		if err := json.Unmarshal(line["type"], &typ); err != nil {
			t.Fatalf("事件 type 解码: %v", err)
		}
		if typ == "card_snapshot" {
			t.Fatalf("事件段出现第二次快照: %v", line)
		}
	}
	if !strings.Contains(waitOut, child.ID) || !strings.Contains(waitOut, root.ID) {
		t.Fatalf("事件缺失: %q", waitOut)
	}
}

func cardWaitJSONLines(t *testing.T, out string) []map[string]json.RawMessage {
	t.Helper()
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}
	rows := strings.Split(trimmed, "\n")
	lines := make([]map[string]json.RawMessage, 0, len(rows))
	for i, row := range rows {
		if strings.TrimSpace(row) == "" {
			t.Fatalf("第 %d 行为空，stdout 必须一行一个 JSON", i)
		}
		var line map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row), &line); err != nil {
			t.Fatalf("第 %d 行不是合法 JSON: %v；原文=%q", i, err, row)
		}
		lines = append(lines, line)
	}
	return lines
}

type cardWaitSnapshot struct {
	Type        string
	CardID      string
	Status      string
	NeedsHuman  bool
	NeedsReason string
}

func readCardWaitSnapshot(t *testing.T, line map[string]json.RawMessage) cardWaitSnapshot {
	t.Helper()
	for _, key := range []string{"type", "card_id", "status", "needs_human", "needs_reason"} {
		if _, ok := line[key]; !ok {
			t.Fatalf("快照缺字段 %q: %v", key, line)
		}
	}
	var got cardWaitSnapshot
	if err := json.Unmarshal(line["type"], &got.Type); err != nil {
		t.Fatalf("快照 type 解码: %v", err)
	}
	if err := json.Unmarshal(line["card_id"], &got.CardID); err != nil {
		t.Fatalf("快照 card_id 解码: %v", err)
	}
	if err := json.Unmarshal(line["status"], &got.Status); err != nil {
		t.Fatalf("快照 status 解码: %v", err)
	}
	if err := json.Unmarshal(line["needs_human"], &got.NeedsHuman); err != nil {
		t.Fatalf("快照 needs_human 解码: %v", err)
	}
	if err := json.Unmarshal(line["needs_reason"], &got.NeedsReason); err != nil {
		t.Fatalf("快照 needs_reason 解码: %v", err)
	}
	if got.Type != "card_snapshot" {
		t.Fatalf("快照 type=%q, want card_snapshot", got.Type)
	}
	return got
}

func TestCardWaitEmitsSnapshotBeforeTimeout(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "瞬时失败卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("解卡: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "needs", card.ID, "派发瞬时失败"); err != nil {
		t.Fatalf("打 needs: %v", err)
	}

	waitOut, _, waitErr := runLedgerCLI(t, dir, "card", "wait", card.ID, "--timeout", "20ms")
	if waitErr == nil || !strings.Contains(waitErr.Error(), "wait --card 超时") {
		t.Fatalf("wait 应按既有超时语义退出，err=%v", waitErr)
	}
	lines := cardWaitJSONLines(t, waitOut)
	if len(lines) != 1 {
		t.Fatalf("无实时事件时应只输出一条建连快照，实得 %d 行: %q", len(lines), waitOut)
	}
	snapshot := readCardWaitSnapshot(t, lines[0])
	if snapshot.CardID != card.ID || snapshot.Status != "待办" {
		t.Fatalf("卡快照身份/状态=%+v, want card=%s status=待办", snapshot, card.ID)
	}
	if !snapshot.NeedsHuman || snapshot.NeedsReason != "派发瞬时失败" {
		t.Fatalf("卡快照 needs=%+v, want true/派发瞬时失败", snapshot)
	}
}
~~~

跑红命令：

~~~bash
go test ./cmd/ -run 'TestCardWaitEmitsSnapshotBeforeTimeout|TestCardWaitSubtreeExitsWhenAllDone' -count=1
~~~

此时尚未添加快照生产逻辑，判定条件是命令必须返回非零，且失败必须来自「单卡 stdout 没有快照」或同等真实断言；不要把预想的具体 Go 测试输出写入台账，执行者应把实际原始输出追加后再进入步骤 3。

### 步骤 3：【绿】加入线型、快照读取和可观测性

#### 3.1 增加类型与字段注释

在 cmd/card_wait.go 的 cardWaitTimeout 声明后加入以下完整代码。字段不使用 omitempty，以便消费者区分 needs_human:false / needs_reason:"" 与字段缺失；这也是步骤 2 的真实 stdout 断言所锁定的序列化约束。

~~~go
const cardSnapshotType = "card_snapshot"

// cardSnapshotLine 是 card wait 建连时输出的只读卡状态快照。
//
// 边界：它只描述建连时的卡状态，不是 ledger.Event，不落 card_events，
// 不推进 seq；Follow 进入后仍只输出 ledger.Event。needs_reason 始终出线，
// 让消费方能区分「没有当前原因」与「生产端漏了字段」。
type cardSnapshotLine struct {
	Type        string `json:"type"`
	CardID      string `json:"card_id"`
	Status      string `json:"status"`
	NeedsHuman  bool   `json:"needs_human"`
	NeedsReason string `json:"needs_reason"`
}
~~~

#### 3.2 替换 runCardWait

用下方完整函数替换 cmd/card_wait.go 当前的 runCardWait。同时在 import 列表增加 sort；已有 context、encoding/json、errors、fmt、log/slog、time 和既有 imports 保持不变。

~~~go
// runCardWait 账本单流多路 wait：先输出建连时每个成员的卡快照，再从当前
// seq 起跟子树事件（每行一个 JSON 对象到 stdout），全部成员达骨架终态
// （已完成/终止）即退出 0。快照不是事件，不改变 seq 或 Follow 游标；成员集
// 只有 Follow 期间继续按原语义动态重算。timeout 是总时长（0=不限），超时
// 退出码 124 与单 task wait 一致。
func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error {
	slog.SetDefault(logx.Setup("cli", ""))
	slog.Info("card wait 开始", "card", cardID, "subtree", subtree, "timeout", timeout.String())

	st, err := openLedger()
	if err != nil {
		slog.Error("card wait 打开账本失败", "card", cardID, "cause", err)
		return err
	}
	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			slog.Warn("card wait 关闭账本失败", "card", cardID, "cause", closeErr)
		}
	}()
	slog.Debug("card wait 账本已打开", "card", cardID)
	slog.Debug("card wait 读取根卡", "card", cardID)
	if card, getErr := st.GetCard(cardID); getErr != nil {
		slog.Error("card wait 读取根卡失败", "card", cardID, "cause", getErr)
		return getErr
	} else {
		slog.Debug("card wait 根卡已确认", "card", cardID, "status", card.Status)
	}

	members := func() ([]string, error) {
		if subtree {
			return st.Subtree(cardID)
		}
		return []string{cardID}, nil
	}
	slog.Debug("card wait 读取起始 seq", "card", cardID)
	start, err := st.MaxSeq()
	if err != nil {
		slog.Error("card wait 读取起始 seq 失败", "card", cardID, "cause", err)
		return err
	}
	slog.Debug("card wait 起始 seq 已确定", "card", cardID, "from_seq", start)

	ctx := cmd.Context()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	enc := json.NewEncoder(cmd.OutOrStdout())

	// 快照必须在首次 checkDone 前写出：已完成卡仍保留现有的提前退出与 stderr
	// 语义，但建连消费者先得到一次可解析的当前状态。
	slog.Debug("card wait 解析快照成员", "card", cardID, "subtree", subtree)
	snapshotIDs, err := members()
	if err != nil {
		slog.Error("card wait 解析快照成员失败", "card", cardID, "subtree", subtree, "cause", err)
		return fmt.Errorf("card wait 快照成员: %w", err)
	}
	sort.Strings(snapshotIDs)
	slog.Debug("card wait 快照成员已解析", "card", cardID, "members", snapshotIDs)
	for _, id := range snapshotIDs {
		slog.Debug("card wait 读取快照卡", "root", cardID, "card", id)
		card, getErr := st.GetCard(id)
		if getErr != nil {
			slog.Error("card wait 读取快照卡失败", "root", cardID, "card", id, "cause", getErr)
			return fmt.Errorf("card wait 快照卡 %s: %w", id, getErr)
		}
		slog.Debug("card wait 读取快照 needs", "root", cardID, "card", id)
		needsReason, needsErr := st.NeedsOf(id)
		if needsErr != nil {
			slog.Error("card wait 读取快照 needs 失败", "root", cardID, "card", id, "cause", needsErr)
			return fmt.Errorf("card wait 快照 needs %s: %w", id, needsErr)
		}
		line := cardSnapshotLine{
			Type:        cardSnapshotType,
			CardID:      card.ID,
			Status:      card.Status,
			NeedsHuman:  needsReason != "",
			NeedsReason: needsReason,
		}
		// needs_human 由当前生效原因投影而来；空串是「当前没有标记」，
		// 不是省略字段，避免脚本把缺字段误判为正常无阻塞。
		if encodeErr := enc.Encode(line); encodeErr != nil {
			slog.Error("card wait 写出快照失败", "root", cardID, "card", id, "cause", encodeErr)
			return fmt.Errorf("card wait 写出快照 %s: %w", id, encodeErr)
		}
		slog.Debug("card wait 快照行已输出", "root", cardID, "card", id,
			"status", card.Status, "needs_human", line.NeedsHuman)
	}
	slog.Info("card wait 建连快照已输出", "card", cardID, "members", len(snapshotIDs), "from_seq", start)

	allDone := errors.New("all-done")
	checkDone := func() (bool, error) {
		ids, membersErr := members()
		if membersErr != nil {
			slog.Error("card wait 重算成员失败", "card", cardID, "subtree", subtree, "cause", membersErr)
			return false, membersErr
		}
		for _, id := range ids {
			card, getErr := st.GetCard(id)
			if getErr != nil {
				slog.Error("card wait 检查终态读取卡失败", "root", cardID, "card", id, "cause", getErr)
				return false, getErr
			}
			if card.Status != ledger.StatusDone && card.Status != ledger.StatusClosed {
				return false, nil
			}
		}
		return true, nil
	}
	if done, checkErr := checkDone(); checkErr != nil {
		slog.Error("card wait 首次终态检查失败", "card", cardID, "cause", checkErr)
		return checkErr
	} else if done {
		slog.Info("card wait 建连时成员已全部完成", "card", cardID, "members", len(snapshotIDs))
		fmt.Fprintln(cmd.ErrOrStderr(), "子树已全部完成")
		return nil
	}

	slog.Debug("card wait 开始跟随", "card", cardID, "from_seq", start, "poll", (2 * time.Second).String())
	slog.Debug("card wait 调用 Follow", "card", cardID, "from_seq", start)
	err = st.Follow(ctx, members, start, 2*time.Second, func(e ledger.Event) error {
		if encodeErr := enc.Encode(e); encodeErr != nil {
			slog.Error("card wait 写出事件失败", "root", cardID, "card", e.CardID,
				"seq", e.Seq, "type", e.Type, "cause", encodeErr)
			return encodeErr
		}
		slog.Debug("card wait 事件已输出", "root", cardID, "card", e.CardID,
			"seq", e.Seq, "type", e.Type)
		if e.Type != ledger.EvStatusMoved {
			return nil
		}
		if done, checkErr := checkDone(); checkErr != nil {
			slog.Error("card wait 事件后终态检查失败", "card", cardID, "seq", e.Seq, "cause", checkErr)
			return checkErr
		} else if done {
			return allDone
		}
		return nil
	})
	slog.Debug("card wait 跟随返回", "card", cardID, "from_seq", start, "cause", err)
	switch {
	case errors.Is(err, allDone):
		slog.Info("card wait 全部成员完成", "card", cardID)
		fmt.Fprintln(cmd.ErrOrStderr(), "子树全部完成，wait 退出")
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("card wait 超时", "card", cardID, "timeout", timeout.String())
		return &exitCodeError{code: ExitTimeout, err: fmt.Errorf("wait --card 超时")}
	case err != nil:
		slog.Error("card wait 跟随失败", "card", cardID, "from_seq", start, "cause", err)
		return err
	default:
		slog.Info("card wait 正常结束", "card", cardID)
		return nil
	}
}
~~~

实现注意事项已在代码块中固定：

- MaxSeq 必须在快照前取得；快照期间新事件仍落在 from_seq=start 的 Follow 区间，避免建连窗口吞事件。
- 快照用 NeedsOf，不能调用 ListCards(CardFilter{...}) 取一组派生视图，因为 ListCards 会混入 blocker/open-decision 派生语义，而本规格只要求当前 needs_human。
- 快照成员排序只作用于初次 stdout；members 闭包继续原样交给 Follow，不把动态子树冻结成快照集合。
- 快照循环只调用 GetCard / NeedsOf，没有任何写账 API；不得把快照写成 Event 或调用 Record*。

### 步骤 4：格式化、绿测和最小回归

先只格式化触及文件：

~~~bash
gofmt -w cmd/card_wait.go cmd/card_wait_test.go
~~~

然后跑红测对应的绿测：

~~~bash
go test ./cmd/ -run 'TestCardWaitEmitsSnapshotBeforeTimeout|TestCardWaitSubtreeExitsWhenAllDone' -count=1
~~~

判定必须真实满足：

- TestCardWaitEmitsSnapshotBeforeTimeout 的 stdout 恰好一行，第一行 type=card_snapshot，card_id 与 status=待办正确，needs_human=true 且 needs_reason="派发瞬时失败"；等待仍按既有超时错误返回。
- 同一测试的 readCardWaitSnapshot 对五个 key 逐一做 presence 断言，因此 false/空串不能靠字段缺失假绿。
- TestCardWaitSubtreeExitsWhenAllDone 的前两行恰为按 card_id 排序的根/子卡快照，均为 needs_human=false 和空 needs_reason；第 3 行起必须是事件对象（有 seq，不能是 card_snapshot），并且原有全部完成后正常退出。

绿测后跑本 task 的最小包验收：

~~~bash
go test ./cmd/ -run 'TestCardWait|TestBacklogSummaryLineIsSingleJSON|TestWaitRejectsCardFlag' -count=1
go vet ./cmd
git diff --check
~~~

预期分别是：测试命令 exit 0 且输出 ok github.com/Xsxdot/handoff/cmd ...；go vet ./cmd exit 0；git diff --check stdout 为空且 exit 0。执行者须把真实原始输出写入台账，不得用计划中的预期文本冒充实跑结果。

### 步骤 5：注释、日志和范围复核

在提交前逐项检查实际 diff：

- cmd/card_wait.go 文件头继续说明职责边界；新增 cardSnapshotLine 注释说明「只读、非事件、不推进 seq、字段不省略」；runCardWait 注释更新为「快照后跟随」；排序、NeedsOf→bool 投影和 MaxSeq→快照→Follow 的时序均有 why 注释。
- 入口日志带 card/subtree/timeout；openLedger、根卡读取、MaxSeq、成员重算、快照卡读取、needs 读取、快照/事件写出、Follow 前后和所有错误分支均带卡号或 seq/type 上下文；成功的快照、事件、终态/超时路径均有 slog。不使用 fmt.Print/log.Print 取代结构化日志；stderr 上已有的人类退出提示保持不变。
- 生产 diff 只涉及 cmd/card_wait.go；测试 diff 只涉及 cmd/card_wait_test.go；没有 internal/ledger、internal/client、事件 schema、cursor 或其他命令文件改动。

## 3. 验收映射与对抗审查

### 3.1 Spec 故事逐条归属

| 规格要求 | 计划落点 | 可判定结果 |
| --- | --- | --- |
| 派发失败早于订阅时第一行可见 needs_human | Task 1 步骤 2 的 TestCardWaitEmitsSnapshotBeforeTimeout | 真实 card needs 先落账，再启动 card wait；stdout 第一行即快照且 true/原因正确 |
| 无人值守用同一行判断积压/未决事项 | Task 1 步骤 2/4 的快照 wire 解析 | type、card_id、status、needs_human、needs_reason 五字段始终出线，可按行解析 |
| --subtree 每成员一行 | TestCardWaitSubtreeExitsWhenAllDone | 根/子卡各一行，按 card id 稳定排序，事件从第 3 行开始 |
| 快照后跟随/退出/超时不变 | 同一测试的事件段和单卡测试的超时断言 | 事件仍由 Follow 输出，全部终态仍 exit 0，空闲超时仍走 ExitTimeout |
| 不做 cursor/回放、task wait、--step 202 | 文件边界与步骤 3.2 | 无新 flag、无 cursor、无 replay、无 cmd/wait.go/dispatch 响应改动 |

### 3.2 缺陷族对抗审查

| 缺陷族 | 设问 | 本计划结论与断言 |
| --- | --- | --- |
| 生命周期/状态机中断 | 快照是否会吞掉建连窗口内事件，或改变已完成提前退出/超时？ | MaxSeq 先于快照固定排他水位；Follow 仍从该水位动态读；快照在 checkDone 前输出但终态返回值和 stderr 分支不改；两支命令级测试覆盖事件后续与超时。 |
| 静默失败/误导报错 | 失败事件已有 needs 标记时，是否仍可能 stdout 0 字节；快照读/写错误是否被吞？ | NeedsOf 的错误、每个 GetCard 错误、JSON Encode 错误、Follow 错误均带上下文日志并返回；单卡测试在无实时事件、20ms 超时下断言 stdout 恰好 1 行，不接受“命令活着”作为通过。 |
| 跨平台假设 | 是否引入 shell、路径、排序或时间平台差异？ | 只新增标准库 sort 与 JSON 字段；测试使用既有 t.TempDir 和 runLedgerCLI，不依赖 shell、网络、操作系统路径分隔符；go vet ./cmd 纳入最小验收。 |
| 假红/假绿 | 测试是否只测内存 struct、只断言“不报错”、或把事件行误当快照？ | 两支测试都从 Cobra card wait 真实入口捕获 stdout；逐行 json.Unmarshal，五个快照 key 做 presence + 类型/值断言；子树测试还要求第 3 行起有 seq 且不是快照；超时测试必须同时满足错误语义和 wire 语义。 |
| 门禁绕过 | 新快照路径是否写账、改状态、绕过现有 Follow/终态门？ | 生产代码只读 GetCard/NeedsOf，不调用任何写 API；首次 checkDone、Follow、ExitTimeout 分支原样保留；代码审查逐项核对触及文件，现有 TestWaitRejectsCardFlag 保留。 |
| 新增枚举值白名单 | card_snapshot 是否误加入 ledger/proto 事件枚举或被既有过滤器消费？ | card_snapshot 仅是 cmd stdout 的 type 字符串，不进入 ledger.Event.Type、proto.EventType 或 internal/client switch；基线 grep 未发现同名注册点，计划文件边界禁止新增。 |
| webview/平台表现差异 | 是否有 Web/UI 线或浏览器 API？ | 不适用：本卡只触及 Go CLI stdout，明确无 Web/UI 代码。 |

### 3.3 序列化边界清单

新增字段从产生到消费的每个手写投影如下，步骤 2 的命令级测试穿过整条链：

1. 产生：cmd/card_wait.go#runCardWait 从 ledger.Card.Status 与 Store.NeedsOf 产生 cardSnapshotLine；
2. JSON 投影：json.NewEncoder(cmd.OutOrStdout()).Encode(cardSnapshotLine) 生成 stdout 单行；
3. 消费：cmd/card_wait_test.go#cardWaitJSONLines 对真实 stdout 做 json.Unmarshal，readCardWaitSnapshot 对五个字段做 key presence、类型和值断言。

这里没有 DTO、HTTP、跨语言孪生或持久化投影；needs_human:false 与 needs_reason:"" 由非可选 JSON 字段和测试 presence 断言区分字段缺失。roundtrip 属性测试不增加覆盖收益：唯一新增边界是固定五字段的单行 CLI JSON，命令级真实输出已覆盖正/空两态。

### 3.4 上下文预算与接缝双向检查

文件集固定为 2 个文件；没有跨包 API 变更，也没有需要竖切的横向依赖。两支测试入口均穿过 card wait 声明缝；唯一声明缝也均有至少一支缝级断言覆盖。没有内部锁测试，因此不存在以内部锁顶替接缝的情况。

## 4. 收尾顺序（由实现者执行）

1. 将每条实际命令及原始输出追加到 docs/superpowers/ledgers/2026-08-25-b253-plan-ledger.md；失败命令原文保留，不作未经验证的归因。
2. 复核 gofmt -w、最小 go test、go vet ./cmd、git diff --check 的真实结果与 §2 步骤 4 判据一致。
3. 只 git add docs/superpowers/plans/b253-plan.md docs/superpowers/ledgers/2026-08-25-b253-plan-ledger.md 及本卡实现者实际允许的 cmd/card_wait.go、cmd/card_wait_test.go（本计划节点自身不写实现文件），然后在当前分支提交；不 push、不切分支、不改 git 配置。
4. 本计划节点的完成证据是本计划文档与台账已提交；实现代码及实现级最终裁决不在本节点伪造。

## 5. 计划自审

- Spec 覆盖：两条用户故事、逐成员快照、字段形态、快照只输出一次、Follow/退出/超时不变和三项 out-of-scope 均在 §3.1/§1.3/§2.3 明确落到文件与测试。
- 占位符扫描：已逐项检查，未发现执行占位；步骤 2 的红测和步骤 3 的生产代码均给出完整代码块。
- 跨 task 类型/签名：本卡单 task，无跨卡 Produces/Consumes；所有 ledger 调用签名逐字列在 §1.2。
- 边界型类型清单：行为验收是 CLI 真入口、stdout 单行 JSON、五字段逐项存在/值、子树成员数与排序、事件后续、原有终态/超时语义和日志/错误分支。
- 平台不变量：本计划节点不派发、不调用 handoff CLI、不起 executor；只读图查询已实跑并记覆盖债。
