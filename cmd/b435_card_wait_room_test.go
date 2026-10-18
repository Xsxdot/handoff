// b435_card_wait_room_test.go —— B435 `card wait` 房间命中段（契约 §4.1 C-101–C-129、
// C-145 本文件半边、C-146、C-148、C-150；C-328 cmd 半边）。
//
// 夹具：真 SQLite 账本 + 真 collab 匹配函数（roomServiceFor）+ 真 cobra `card wait`。
// 卡由 CLI `card add` 建，pull 席位由 Store.BindSeatMode（card bind 同款）坐下，会话消息
// 由 collab.Service.Send 落账。席位地址游标键 = 席位身份原样（C-112）。
package cmd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

type b435WaitEnv struct {
	dir     string
	st      *ledger.Store
	svc     *collab.Service
	session string
}

func newB435WaitEnv(t *testing.T) *b435WaitEnv {
	t.Helper()
	dir := t.TempDir()
	// 先跑一次 CLI 建配置与账本（configPath 指向本目录）。
	createCardWaitFixture(t, dir)
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := roomServiceFor(st)
	sess, err := svc.CreateSession("B435 card wait", "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	return &b435WaitEnv{dir: dir, st: st, svc: svc, session: sess.ID}
}

// card 建卡并拉进会话。
func (e *b435WaitEnv) card(t *testing.T) string {
	t.Helper()
	id := createCardWaitFixture(t, e.dir)
	if err := e.svc.JoinCard(e.session, id, "user:tester"); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *b435WaitEnv) pull(t *testing.T, card, sid string) string {
	t.Helper()
	identity := "cli:codex#" + sid
	if err := e.st.BindSeatMode(card, identity, proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	return identity
}

func (e *b435WaitEnv) push(t *testing.T, card, sid string) string {
	t.Helper()
	identity := "cli:opencode#" + sid
	if err := e.st.BindSeatMode(card, identity, proto.SeatSourceCoordinate, proto.SeatModePush,
		ledger.SeatBearing{Carrier: "car", Machine: "local"}); err != nil {
		t.Fatal(err)
	}
	return identity
}

func (e *b435WaitEnv) send(t *testing.T, body string, mentions ...string) int64 {
	t.Helper()
	seq, err := e.svc.Send(e.session, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: body, Mentions: mentions}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func (e *b435WaitEnv) cursor(t *testing.T, key string) (int64, bool) {
	t.Helper()
	seq, ok, err := e.st.SessionDeliveryCursorRowContext(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return seq, ok
}

func (e *b435WaitEnv) exec(t *testing.T, stmt string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(e.dir, "ledger.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("执行 %q: %v", stmt, err)
	}
}

// b435RoomLines 取 stdout 中 type=room_message 的事件行（原文）。
func b435RoomLines(t *testing.T, out string) []string {
	t.Helper()
	var lines []string
	for _, line := range cardWaitActionableLines(out) {
		var ev ledger.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stdout 行不是 ledger.Event: %v %q", err, line)
		}
		if ev.Type == ledger.EvRoomMessage {
			lines = append(lines, line)
		}
	}
	return lines
}

func b435Seqs(t *testing.T, lines []string) []int64 {
	t.Helper()
	var out []int64
	for _, line := range lines {
		var ev ledger.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev.Seq)
	}
	return out
}

func b435ExitCode(err error) int {
	var codeErr *exitCodeError
	if errors.As(err, &codeErr) {
		return codeErr.code
	}
	if err != nil {
		return 1
	}
	return 0
}

// C-145（本文件半边）/C-116：card_wait.go 不自带判定，至少一处调 Matches。
func TestB435CardWaitSourceGuard(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "card_wait.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "MessageWakeTargets", "AddressesCard", "IsHumanAuthor", "Mentions":
			t.Errorf("card_wait.go %s 出现 .%s——房间命中判定只经 collab.Service.Matches", fset.Position(sel.Pos()), sel.Sel.Name)
		case "Matches":
			matches++
		}
		return true
	})
	if matches == 0 {
		t.Fatal("card_wait.go 未调用 Matches——房间命中段缺失或绕过匹配函数")
	}
}

// C-106/C-107/C-113：命中行就是该事件的 ledger.Event 原样 JSON，card_id 为空串。
func TestB435CardWaitRoomHitLine(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	e.pull(t, card, "hit-1")
	seq := e.send(t, "看一下这张卡", card)
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--timeout", "5s")
	if err != nil {
		t.Fatalf("card wait: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"card_snapshot"`) {
		t.Fatalf("stdout 应为快照 + 一行命中，得 %q", out)
	}
	ev, ok, err := e.st.EventBySeq(seq)
	if err != nil || !ok {
		t.Fatalf("读命中事件: %v %v", ok, err)
	}
	var want bytes.Buffer
	if err := json.NewEncoder(&want).Encode(ev); err != nil {
		t.Fatal(err)
	}
	if got := lines[1] + "\n"; got != want.String() {
		t.Fatalf("命中行必须逐字节等于事件原样 JSON:\n got %s\nwant %s", got, want.String())
	}
	if !strings.Contains(lines[1], `"card_id":""`) || !strings.Contains(lines[1], `"type":"room_message"`) {
		t.Fatalf("命中行应是 card_id 为空的 room_message: %s", lines[1])
	}
}

// C-102–C-104 + Q3 甲：--since N 两段都从 seq>N 读；不读游标（大游标值仍输出）、不写游标；
// 快照 from_seq = N。
func TestB435CardWaitSinceExclusiveReadOnly(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	identity := e.pull(t, card, "since-1")
	first := e.send(t, "第一条", card)
	second := e.send(t, "第二条", card)
	if err := e.st.AdvanceSessionDeliveryCursor(identity, second+100); err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--since", strconv.FormatInt(first, 10), "--timeout", "5s")
	if err != nil {
		t.Fatalf("card wait --since: %v", err)
	}
	if got := b435Seqs(t, b435RoomLines(t, out)); len(got) != 1 || got[0] != second {
		t.Fatalf("--since %d 应只输出 seq>%d 的 %d，得 %v", first, first, second, got)
	}
	snap := firstJSONLine(t, out)
	if from, _ := snap["from_seq"].(float64); int64(from) != first {
		t.Fatalf("--since 时快照 from_seq 应为 %d，得 %v", first, snap["from_seq"])
	}
	if seq, _ := e.cursor(t, identity); seq != second+100 {
		t.Fatalf("--since 不得写游标：%d", seq)
	}
}

// C-118/C-120/C-121：积压逐条 seq 升序先出，再出实时；无摘要行。
func TestB435CardWaitBacklogBeforeLive(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	identity := e.pull(t, card, "backlog-1")
	b1 := e.send(t, "积压一", card)
	b2 := e.send(t, "积压二", card)
	var live int64
	writer := make(chan error, 1)
	go func() {
		time.Sleep(700 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(e.dir, "ledger.db"))
		if err != nil {
			writer <- err
			return
		}
		defer st.Close()
		seq, err := roomServiceFor(st).Send(e.session, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "实时", Mentions: []string{card}}, "user:tester")
		live = seq
		if err == nil {
			time.Sleep(2500 * time.Millisecond)
			err = moveCardWaitFixtureToDone(st, card)
		}
		writer <- err
	}()
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--follow", "--timeout", "8s")
	if werr := <-writer; werr != nil {
		t.Fatal(werr)
	}
	if err != nil {
		t.Fatalf("card wait --follow: %v", err)
	}
	if got := b435Seqs(t, b435RoomLines(t, out)); len(got) != 3 || got[0] != b1 || got[1] != b2 || got[2] != live {
		t.Fatalf("应依次输出积压 %d,%d 与实时 %d，得 %v\n%s", b1, b2, live, got, out)
	}
	if seq, ok := e.cursor(t, identity); !ok || seq != live {
		t.Fatalf("游标应推进到最后输出的 %d，得 %d %v", live, seq, ok)
	}
}

// C-119（反面，Q2 甲）：换绑后新身份首次 wait（无游标行）不收换绑前的消息，收换绑后的。
func TestB435CardWaitLowerBoundAfterRebind(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	old := e.pull(t, card, "old-1")
	e.send(t, "换绑前", card)
	if err := e.st.RebindSeatMode(card, "cli:codex#new-1", proto.SeatSourceBind, proto.SeatModePull, old, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	after := e.send(t, "换绑后", card)
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--timeout", "5s")
	if err != nil {
		t.Fatalf("card wait: %v", err)
	}
	if got := b435Seqs(t, b435RoomLines(t, out)); len(got) != 1 || got[0] != after {
		t.Fatalf("新持有者只应收到换绑后的 %d，得 %v", after, got)
	}
}

// failAfterWriter 前 n 次 Write 成功，之后全部失败。
type failAfterWriter struct {
	mu sync.Mutex
	n  int
	w  bytes.Buffer
}

func (f *failAfterWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n <= 0 {
		return 0, errors.New("stdout 写失败（注入）")
	}
	f.n--
	return f.w.Write(p)
}

func runCardWaitDirect(t *testing.T, out io.Writer, card string, follow bool, timeout time.Duration) error {
	t.Helper()
	c := &cobra.Command{}
	c.SetOut(out)
	c.SetErr(io.Discard)
	c.SetContext(context.Background())
	return runCardWait(c, card, false, follow, timeout, 0, false)
}

// C-123：stdout 写失败 → 非 0，游标不推进。
func TestB435CardWaitStdoutFailNoAdvance(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	identity := e.pull(t, card, "stdout-1")
	e.send(t, "写不出去", card)
	err := runCardWaitDirect(t, &failAfterWriter{n: 1}, card, false, 5*time.Second)
	if code := b435ExitCode(err); code == 0 || code == ExitTimeout {
		t.Fatalf("stdout 写失败应非 0 且 ≠124，得 %v", err)
	}
	if _, ok := e.cursor(t, identity); ok {
		t.Fatal("stdout 写失败不得推进游标")
	}
}

// C-124：游标写失败 → 非 0（≠124），已写出的行允许下次重投。
func TestB435CardWaitCursorWriteFailNonZero(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	identity := e.pull(t, card, "cursor-1")
	seq := e.send(t, "游标写不进", card)
	e.exec(t, `CREATE TRIGGER b435_cursor_fail BEFORE INSERT ON session_delivery_cursors
		BEGIN SELECT RAISE(ABORT, 'b435 游标写失败注入'); END`)
	var out bytes.Buffer
	err := runCardWaitDirect(t, &out, card, false, 5*time.Second)
	if code := b435ExitCode(err); code == 0 || code == ExitTimeout {
		t.Fatalf("游标写失败应非 0 且 ≠124，得 %v", err)
	}
	if got := b435Seqs(t, b435RoomLines(t, out.String())); len(got) != 1 || got[0] != seq {
		t.Fatalf("游标写失败前该行已输出，得 %v", got)
	}
	if _, ok := e.cursor(t, identity); ok {
		t.Fatal("游标写失败后不应有游标行")
	}
}

// C-125：积压末尾的 seq 在实时轮询里可见时也只输出一次。
func TestB435CardWaitNoDuplicateSeq(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	e.pull(t, card, "dup-1")
	tail := e.send(t, "积压末尾", card)
	writer := make(chan error, 1)
	go func() {
		// 跨过至少两个 2s 节拍再收尾，让实时段多次观察 (tail, fence]。
		time.Sleep(4500 * time.Millisecond)
		st, err := ledger.Open(filepath.Join(e.dir, "ledger.db"))
		if err != nil {
			writer <- err
			return
		}
		defer st.Close()
		writer <- moveCardWaitFixtureToDone(st, card)
	}()
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--follow", "--timeout", "10s")
	if werr := <-writer; werr != nil {
		t.Fatal(werr)
	}
	if err != nil {
		t.Fatalf("card wait --follow: %v", err)
	}
	if got := b435Seqs(t, b435RoomLines(t, out)); len(got) != 1 || got[0] != tail {
		t.Fatalf("同一 seq 只应输出一次，得 %v", got)
	}
}

// C-115：push 席位 / 空座 / 卡不在任何会话 → 房间段零输出，卡事件段照常。
func TestB435CardWaitRoomSegmentSilentWhenNotPull(t *testing.T) {
	e := newB435WaitEnv(t)
	pushCard := e.card(t)
	e.push(t, pushCard, "silent-push")
	emptyCard := e.card(t)
	loose := createCardWaitFixture(t, e.dir)
	e.pull(t, loose, "silent-loose")
	e.send(t, "@push 卡", pushCard)
	e.send(t, "@空座卡", emptyCard)
	e.send(t, "@会话外卡", loose)
	for _, card := range []string{pushCard, emptyCard, loose} {
		writer := make(chan error, 1)
		go func(card string) {
			time.Sleep(500 * time.Millisecond)
			st, err := ledger.Open(filepath.Join(e.dir, "ledger.db"))
			if err != nil {
				writer <- err
				return
			}
			defer st.Close()
			writer <- st.MarkNeedsHuman(card, "卡事件照常", "test")
		}(card)
		out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--timeout", "6s")
		if werr := <-writer; werr != nil {
			t.Fatal(werr)
		}
		if err != nil {
			t.Fatalf("card wait %s: %v", card, err)
		}
		if rooms := b435RoomLines(t, out); len(rooms) != 0 {
			t.Fatalf("卡 %s 非 pull/不在会话，房间段应零输出，得 %v", card, rooms)
		}
		lines := cardWaitActionableLines(out)
		if len(lines) != 1 || !strings.Contains(lines[0], `"needs_human"`) {
			t.Fatalf("卡 %s 卡事件段应照常输出 needs_human，得 %q", card, out)
		}
	}
}

// C-114：--subtree 不扩展房间地址——@子卡 pull 席位不出现在父卡的 wait。
func TestB435CardWaitSubtreeDoesNotExpandAddress(t *testing.T) {
	e := newB435WaitEnv(t)
	parent := e.card(t)
	e.pull(t, parent, "sub-parent")
	child, err := e.st.SplitCard(parent, "子卡", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.JoinCard(e.session, child.ID, "user:tester"); err != nil {
		t.Fatal(err)
	}
	e.pull(t, child.ID, "sub-child")
	e.send(t, "@子卡", child.ID)
	parentHit := e.send(t, "@父卡", parent)
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", parent, "--subtree", "--timeout", "5s")
	if err != nil {
		t.Fatalf("card wait --subtree: %v", err)
	}
	if got := b435Seqs(t, b435RoomLines(t, out)); len(got) != 1 || got[0] != parentHit {
		t.Fatalf("--subtree 只应收父卡自身地址的 %d，得 %v", parentHit, got)
	}
}

// C-126：一次性模式积压命中输出后退出 0；C-129/C-228：匹配读失败 → 非 0 且 ≠124。
func TestB435CardWaitOneShotExitsOnRoomHit(t *testing.T) {
	e := newB435WaitEnv(t)
	card := e.card(t)
	e.pull(t, card, "oneshot-1")
	first := e.send(t, "一", card)
	e.send(t, "二", card)
	started := time.Now()
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--timeout", "10s")
	if err != nil {
		t.Fatalf("一次性 card wait: %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("积压命中后应立即退出，耗时 %s", time.Since(started))
	}
	if got := b435Seqs(t, b435RoomLines(t, out)); len(got) != 1 || got[0] != first {
		t.Fatalf("一次性模式只输出首个命中 %d，得 %v", first, got)
	}

	t.Run("匹配读失败非 0", func(t *testing.T) {
		e := newB435WaitEnv(t)
		card := e.card(t)
		e.pull(t, card, "oneshot-err")
		e.send(t, "无 @ 人类发言")
		// 打开账本的 ensureSchema 会 CREATE TABLE IF NOT EXISTS 重建被删的表；换成缺列的同名
		// 视图，建表语句跳过、驻场者读在查询期报错（匹配函数读失败）。
		e.exec(t, `DROP TABLE session_residents`)
		e.exec(t, `CREATE VIEW session_residents AS SELECT 'x' AS session`)
		_, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--timeout", "5s")
		if code := b435ExitCode(err); code == 0 || code == ExitTimeout {
			t.Fatalf("匹配读失败应非 0 且 ≠124，得 %v", err)
		}
	})
}

// C-117：候选页判定期间席位版本变化 → 本页作废重读（不输出、不推进）；持续不稳定 → 可见错误。
func TestB435CardWaitRevisionGuard(t *testing.T) {
	t.Run("换绑走后重读不输出", func(t *testing.T) {
		e := newB435WaitEnv(t)
		card := e.card(t)
		identity := e.pull(t, card, "rev-1")
		e.send(t, "页内换绑", card)
		calls := 0
		cardWaitBeforeJudge = func(from, to int64) {
			calls++
			if calls == 1 {
				if err := e.st.RebindSeatMode(card, "cli:codex#rev-other", proto.SeatSourceBind, proto.SeatModePull, identity, ledger.SeatBearing{}); err != nil {
					t.Errorf("页内换绑: %v", err)
				}
			}
		}
		t.Cleanup(func() { cardWaitBeforeJudge = nil })
		out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--timeout", "3s")
		if code := b435ExitCode(err); code != ExitTimeout {
			t.Fatalf("换绑走后无命中应超时 124，得 %v", err)
		}
		if rooms := b435RoomLines(t, out); len(rooms) != 0 {
			t.Fatalf("版本变化的页不得输出: %v", rooms)
		}
		if calls < 2 {
			t.Fatalf("版本变化后应重读该页，判定前钩子调用 %d 次", calls)
		}
		if _, ok := e.cursor(t, identity); ok {
			t.Fatal("不得推进旧身份游标")
		}
	})
	t.Run("持续不稳定可见错误", func(t *testing.T) {
		e := newB435WaitEnv(t)
		card := e.card(t)
		e.pull(t, card, "rev-2")
		other := createCardWaitFixture(t, e.dir)
		e.send(t, "页内席位一直变", card)
		cardWaitBeforeJudge = func(from, to int64) {
			if err := e.st.ClearSeat(other); err != nil {
				t.Errorf("制造版本变化: %v", err)
			}
		}
		t.Cleanup(func() { cardWaitBeforeJudge = nil })
		out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--timeout", "5s")
		if code := b435ExitCode(err); code == 0 || code == ExitTimeout {
			t.Fatalf("持续不稳定应可见错误（非 0 且 ≠124），得 %v", err)
		}
		if rooms := b435RoomLines(t, out); len(rooms) != 0 {
			t.Fatalf("未确认的页不得输出: %v", rooms)
		}
	})
}

// C-328 cmd 半边（Q4 预授权甲：与 internal/agentd/b435_seat_mode_wake_test.go
// TestB435SeatModeBidirectionalWake 同一份 seed）：会话内 push 卡 P 与 pull 卡 Q；作者
// user:tester；M1「给 pull 席位」@Q；M2「给 push 席位」@P。断言：card wait Q 输出 1 行
// （M1）；card wait P 输出 0 行房间命中。
func TestB435SeatModeBidirectionalCardWait(t *testing.T) {
	e := newB435WaitEnv(t)
	p, q := e.card(t), e.card(t)
	e.push(t, p, "bidi-push")
	e.pull(t, q, "bidi-pull")
	m1 := e.send(t, "给 pull 席位", q)
	e.send(t, "给 push 席位", p)
	out, _, err := runLedgerCLI(t, e.dir, "card", "wait", q, "--timeout", "5s")
	if err != nil {
		t.Fatalf("card wait pull 卡: %v", err)
	}
	if got := b435Seqs(t, b435RoomLines(t, out)); len(got) != 1 || got[0] != m1 {
		t.Fatalf("pull 席位：card wait 应输出 1 行（M1=%d），得 %v", m1, got)
	}
	out, _, err = runLedgerCLI(t, e.dir, "card", "wait", p, "--timeout", "2s")
	if code := b435ExitCode(err); code != ExitTimeout {
		t.Fatalf("push 席位：card wait 无命中应超时 124，得 %v", err)
	}
	if rooms := b435RoomLines(t, out); len(rooms) != 0 {
		t.Fatalf("push 席位：card wait 应输出 0 行，得 %v", rooms)
	}
}
