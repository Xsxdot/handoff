// projectnotify_test.go —— B435 O1 系统通知（契约 C-243–C-245、C-231/C-238；plan Q6 甲 / Q7 乙 / Q8 甲）。
//
// 观察点：真账本 + 真 collab + 真 consumeAutomationEventsOnce + fake runner（queueTraceRunner.resumes
// = keystone 回合次数）。一律经消费循环驱动，不直调通知函数；「零条」断言都在同一夹具里配「一条」对照。
// N1：同进程两个 Server 的 holder 相同，「两实例」只做顺序双轮；「他机在飞」以预占 other-holder 认领模拟。
package agentd

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
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// 段 0b：系统身份 @驻场者 的普通消息落账后跑一轮消费 → 零 keystone Wake（C-231），且
// Recipients 对它恰返回驻场者地址、Default=false（C-214 显式分支）。对照：同会话 @push 卡恰一次 Wake，
// 证明本夹具里消费循环确实能唤醒。
func TestB435NotifyShapeRoutesToResidentWithoutWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	pushCard := createCoordCard(t, env)
	b435BindPush(t, env, pushCard, "notify-0b")
	session, err := env.srv.rooms.CreateSession("项目房间前提", proto.SystemNotifierIdentity, proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.srv.rooms.JoinCard(session.ID, pushCard, "user:tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.SitResident(proto.SessionResident{Session: session.ID, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, env)

	msg := proto.RoomMessage{Room: session.ID, Kind: proto.RoomMsgUser, Body: "卡 B1 新建", Mentions: []string{"agent:org"}}
	sendSessionMessage(t, env.srv.rooms, session.ID, proto.SystemNotifierIdentity, msg.Body, msg.Mentions, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("@驻场者 的系统消息不得 keystone 唤醒: %v", got)
	}
	d, err := env.srv.rooms.Recipients(msg, proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatal(err)
	}
	want := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: session.ID, Identity: "agent:org"}
	if d.Default || len(d.Addresses) != 1 || d.Addresses[0] != want {
		t.Fatalf("应恰驻场者地址且非默认分支，得 %+v", d)
	}

	sendSessionMessage(t, env.srv.rooms, session.ID, "agent:org", "叫 push 卡", []string{pushCard}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], pushCard) {
		t.Fatalf("对照：@push 卡应恰一次唤醒: %v", got)
	}
}

const b435NotifyProject = "handoff"
const b435Organizer = "agent:org"

type b435NotifyEnv struct {
	*ledgerEnv
	runner *queueTraceRunner
	room   string
}

// newB435NotifyEnv 建「项目 handoff 有项目房间（群主 agent:handoff）」的夹具；withResident 时坐下组织者。
// 先排干夹具自带事件，再建房间与驻场者并再排干——之后的事件全部是用例自己的。
func newB435NotifyEnv(t *testing.T, withResident bool) *b435NotifyEnv {
	t.Helper()
	env, runner := newNoPTYAutomationEnv(t)
	seedAgentdLedger(t, env.ledger, "bug")
	drainAutomation(t, env)
	room, _, err := env.ledger.EnsureProjectRoom(b435NotifyProject, proto.SystemNotifierIdentity, "test")
	if err != nil {
		t.Fatalf("建项目房间: %v", err)
	}
	if withResident {
		if _, err := env.srv.rooms.SitResident(proto.SessionResident{Session: room.ID, Identity: b435Organizer}); err != nil {
			t.Fatalf("坐下组织者: %v", err)
		}
	}
	drainAutomation(t, env)
	return &b435NotifyEnv{ledgerEnv: env, runner: runner, room: room.ID}
}

// b435NewCard 在项目 project 建一张 bug 流程卡，返回卡号与 card_created 事件 seq。
func (e *b435NotifyEnv) newCard(t *testing.T, project string) (string, int64) {
	t.Helper()
	card, err := e.ledger.CreateCard(ledger.NewCard{Title: "通知测试卡", Project: project, Workflow: "bug", Actor: "user:tester"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card.ID, e.lastSeq(t, card.ID, ledger.EvCardCreated)
}

func (e *b435NotifyEnv) lastSeq(t *testing.T, cardID, typ string) int64 {
	t.Helper()
	events, err := e.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == typ {
			return events[i].Seq
		}
	}
	t.Fatalf("卡 %s 无 %s 事件", cardID, typ)
	return 0
}

// notices 读项目房间里 agent:handoff 写下的全部消息（升序）。
func (e *b435NotifyEnv) notices(t *testing.T) []proto.LedgerEvent {
	t.Helper()
	history, err := e.srv.rooms.History(e.room, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []proto.LedgerEvent
	for _, ev := range history {
		if ev.Actor == proto.SystemNotifierIdentity {
			out = append(out, ev)
		}
	}
	return out
}

func (e *b435NotifyEnv) rawDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", e.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// claim 读 wake_claims 中 (notify:<typ>, seq) 一行：exists、done、holder。
func (e *b435NotifyEnv) claim(t *testing.T, typ string, seq int64) (bool, bool, string) {
	t.Helper()
	var holder string
	var done sql.NullString
	err := e.rawDB(t).QueryRow(`SELECT holder, done_at FROM wake_claims WHERE card = ? AND seq = ?`,
		"notify:"+typ, seq).Scan(&holder, &done)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return true, done.Valid, holder
}

func (e *b435NotifyEnv) notifyClaimRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.rawDB(t).QueryRow(`SELECT COUNT(*) FROM wake_claims WHERE card LIKE 'notify:%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *b435NotifyEnv) cursor() int64 {
	e.srv.automationMu.Lock()
	defer e.srv.automationMu.Unlock()
	return e.srv.automationCursor
}

func (e *b435NotifyEnv) expireClaim(t *testing.T, typ string, seq int64) {
	t.Helper()
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := e.rawDB(t).Exec(`UPDATE wake_claims SET lease_until = ? WHERE card = ? AND seq = ?`,
		past, "notify:"+typ, seq); err != nil {
		t.Fatal(err)
	}
}

// assertNotice 逐键断言一条通知的原始载荷（C-243/C-244 + Q7 乙锚 + Q14 甲 kind）。
func assertNotice(t *testing.T, ev proto.LedgerEvent, cardID string, srcSeq int64, bodyHas ...string) {
	t.Helper()
	if ev.Actor != proto.SystemNotifierIdentity {
		t.Fatalf("通知作者应为 %s，得 %s", proto.SystemNotifierIdentity, ev.Actor)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if raw, has := payload["by_system"]; has {
		t.Fatalf("通知载荷不得出 by_system 键（缺键 = false；绝非 null/true），得 %s", raw)
	}
	if string(payload["mentions"]) != `["`+b435Organizer+`"]` {
		t.Fatalf("mentions 应恰为 [驻场者]，得 %s", payload["mentions"])
	}
	if string(payload["kind"]) != `"user"` {
		t.Fatalf("kind 应为 user，得 %s", payload["kind"])
	}
	var refs []string
	if err := json.Unmarshal(payload["refs"], &refs); err != nil {
		t.Fatalf("refs 解码: %v（%s）", err, payload["refs"])
	}
	want := map[string]bool{cardID: false, "seq:" + strconv.FormatInt(srcSeq, 10): false}
	for _, r := range refs {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("refs 缺 %q，得 %v", k, refs)
		}
	}
	var body string
	if err := json.Unmarshal(payload["body"], &body); err != nil {
		t.Fatal(err)
	}
	for _, s := range append([]string{cardID, b435NotifyProject}, bodyHas...) {
		if !strings.Contains(body, s) {
			t.Fatalf("正文应含 %q，得 %q", s, body)
		}
	}
}

func TestB435NotifyCardCreatedExactlyOne(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	cardID, seq := e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	got := e.notices(t)
	if len(got) != 1 {
		t.Fatalf("card_created 应恰一条通知，得 %d", len(got))
	}
	assertNotice(t, got[0], cardID, seq, "新建")
	if exists, done, _ := e.claim(t, ledger.EvCardCreated, seq); !exists || !done {
		t.Fatalf("notify:card_created 认领应已收尾: exists=%v done=%v", exists, done)
	}
	if r := b435Resumes(e.runner); len(r) != 0 {
		t.Fatalf("通知不得 keystone 唤醒: %v", r)
	}
	// 无回环：通知本身回到消费循环 → 驻场者地址零 Wake，且不再生通知。
	b435Consume(t, e.ledgerEnv)
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("再跑两轮仍应恰一条，得 %d", n)
	}
}

func TestB435NotifyStatusMovedExactlyOne(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	cardID, _ := e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	if err := e.ledger.MoveCard(cardID, ledger.StatusDoing, "", "user:tester"); err != nil {
		t.Fatalf("移列: %v", err)
	}
	seq := e.lastSeq(t, cardID, ledger.EvStatusMoved)
	b435Consume(t, e.ledgerEnv)
	got := e.notices(t)
	if len(got) != 2 {
		t.Fatalf("建卡 + 换列应各一条，得 %d", len(got))
	}
	assertNotice(t, got[1], cardID, seq, "换列", ledger.StatusDoing)
	if exists, done, _ := e.claim(t, ledger.EvStatusMoved, seq); !exists || !done {
		t.Fatalf("notify:status_moved 认领应已收尾: exists=%v done=%v", exists, done)
	}
}

func TestB435NotifyNeedsHumanExactlyOne(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	cardID, _ := e.newCard(t, b435NotifyProject)
	b435BindPush(t, e.ledgerEnv, cardID, "needs-1") // 有可唤醒的 push 席位，零 Wake 才有意义
	b435Consume(t, e.ledgerEnv)
	if err := e.ledger.MarkNeedsHuman(cardID, "要人拍板", "test"); err != nil {
		t.Fatal(err)
	}
	seq := e.lastSeq(t, cardID, ledger.EvNeedsHuman)
	processed, _, err := e.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费: %v", err)
	}
	if processed != 0 {
		t.Fatalf("needs_human 不得被消费为唤醒（C-238），processed=%d", processed)
	}
	if r := b435Resumes(e.runner); len(r) != 0 {
		t.Fatalf("needs_human 不得唤醒协调者: %v", r)
	}
	got := e.notices(t)
	if len(got) != 2 {
		t.Fatalf("建卡 + needs_human 应各一条，得 %d", len(got))
	}
	assertNotice(t, got[1], cardID, seq, "需要人", "要人拍板")
}

// C-245 反面：有项目房间无驻场者 / 项目无项目房间 → 零条、零 notify 认领、游标照常推进；同夹具坐下驻场者后一条。
func TestB435NotifyNoResidentZero(t *testing.T) {
	e := newB435NotifyEnv(t, false)
	if err := e.ledger.SetCardPrefix("b435-no-room", "NR"); err != nil {
		t.Fatal(err)
	}
	_, seq := e.newCard(t, b435NotifyProject)
	_, otherSeq := e.newCard(t, "b435-no-room")
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("无驻场者应零条，得 %d", n)
	}
	if n := e.notifyClaimRows(t); n != 0 {
		t.Fatalf("不适用的事件不得认领，得 %d 行", n)
	}
	if c := e.cursor(); c < seq || c < otherSeq {
		t.Fatalf("游标应照常推进越过 %d/%d，得 %d", seq, otherSeq, c)
	}
	if _, err := e.srv.rooms.SitResident(proto.SessionResident{Session: e.room, Identity: b435Organizer}); err != nil {
		t.Fatal(err)
	}
	cardID, seq2 := e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	got := e.notices(t)
	if len(got) != 1 {
		t.Fatalf("对照：坐下后应恰一条，得 %d", len(got))
	}
	assertNotice(t, got[0], cardID, seq2, "新建")
}

// 事件留在窗口内（在飞的卡唤醒认领挡住水位，每轮重读）：同一 Server 再跑两轮、换一个新 Server 再跑一轮 → 仍恰一条。
// 新 Server = 进程重启（handled 集清空），由已终局认领兜住（拆解 §5.3「收尾后、标水位前重启」）。
func TestB435NotifySecondRoundNoResend(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	block := e.cursor() // 夹具最后一条已消费事件的 seq：在其上挂他机在飞认领，挡住水位
	if got, err := e.ledger.ClaimWake(block, "B-block", "other-holder", time.Hour); err != nil || !got {
		t.Fatalf("预占挡水位认领: %v %v", got, err)
	}
	e.srv.automationMu.Lock()
	e.srv.automationCursor = block - 1
	e.srv.automationMu.Unlock()
	_, seq := e.newCard(t, b435NotifyProject)
	for i := 0; i < 3; i++ {
		b435Consume(t, e.ledgerEnv)
		if c := e.cursor(); c >= seq {
			t.Fatalf("前提：水位应被挡在 %d 之前，得 %d", seq, c)
		}
	}
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("同一 Server 三轮应恰一条，得 %d", n)
	}
	fresh := b435SecondServer(t, e.ledgerEnv)
	if _, _, err := fresh.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("重启后（新 Server）重读同窗口应仍恰一条，得 %d", n)
	}
}

// b435SecondServer 起同账本的第二个 Server 实例（独立 DataDir、游标从 0 起、handled 集为空）。
func b435SecondServer(t *testing.T, env *ledgerEnv) *Server {
	t.Helper()
	backend, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	srv := NewServer(&config.Config{Token: testToken, DataDir: t.TempDir()}, backend, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetLedger(env.ledger)
	SetupAutomationForTest(t, srv, env.ledger)
	return srv
}

// P7 乙 / N1：同账本两个 Server 顺序各跑一轮 → 恰一条。
func TestB435NotifyTwoServersSequentialExactlyOne(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	second := b435SecondServer(t, e.ledgerEnv)
	e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	if _, _, err := second.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("两实例顺序各一轮应恰一条，得 %d", n)
	}
}

// Q6 甲核心：他机在飞（预占 other-holder 未过期认领）→ 本机让过：零条、游标不越过、handled 不记；
// 租约过期后本机接管 → 恰一条。
func TestB435NotifyOtherHolderInFlight(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	cardID, seq := e.newCard(t, b435NotifyProject)
	if got, err := e.ledger.ClaimWake(seq, "notify:"+ledger.EvCardCreated, "other-holder", time.Hour); err != nil || !got {
		t.Fatalf("预占他机认领: %v %v", got, err)
	}
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("他机在飞应让过，得 %d 条", n)
	}
	if c := e.cursor(); c >= seq {
		t.Fatalf("在飞认领应挡住水位（< %d），得 %d", seq, c)
	}
	if e.srv.projectNotify.isHandled(seq) {
		t.Fatal("让过不得记 handled")
	}
	e.expireClaim(t, ledger.EvCardCreated, seq)
	b435Consume(t, e.ledgerEnv)
	got := e.notices(t)
	if len(got) != 1 {
		t.Fatalf("租约过期接管后应恰一条，得 %d", len(got))
	}
	assertNotice(t, got[0], cardID, seq, "新建")
	if exists, done, holder := e.claim(t, ledger.EvCardCreated, seq); !exists || !done || holder != e.srv.wakeClaimHolder() {
		t.Fatalf("接管者应收尾认领: exists=%v done=%v holder=%s", exists, done, holder)
	}
	if c := e.cursor(); c < seq {
		t.Fatalf("收尾后游标应越过 %d，得 %d", seq, c)
	}
}

// Q7 乙：前任「已 Send 未收尾」后崩溃（项目房间已有带同锚的通知 + 过期的他人认领）→ 接管者幂等锚命中：
// 消息数仍 1，认领被本机收尾。
func TestB435NotifyTakeoverAfterPeerSentNoResend(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	cardID, seq := e.newCard(t, b435NotifyProject)
	if got, err := e.ledger.ClaimWake(seq, "notify:"+ledger.EvCardCreated, "other-holder", time.Hour); err != nil || !got {
		t.Fatalf("预占他机认领: %v %v", got, err)
	}
	if _, err := e.srv.rooms.Send(e.room, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "前任已发的通知", Mentions: []string{b435Organizer},
		Refs: []string{cardID, "seq:" + strconv.FormatInt(seq, 10)},
	}, proto.SystemNotifierIdentity); err != nil {
		t.Fatalf("模拟前任已发: %v", err)
	}
	e.expireClaim(t, ledger.EvCardCreated, seq)
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("幂等锚命中不得重发，得 %d 条", n)
	}
	if exists, done, holder := e.claim(t, ledger.EvCardCreated, seq); !exists || !done || holder != e.srv.wakeClaimHolder() {
		t.Fatalf("接管者应收尾认领: exists=%v done=%v holder=%s", exists, done, holder)
	}
}

// failingSendLC 让通知 Send 落账失败（瞬时错误；其余方法透传真 Facade）。
type failingSendLC struct {
	client.LedgerClient
}

func (failingSendLC) RecordRoomMessage(string, proto.RoomMessage, string) (int64, error) {
	return 0, errors.New("b435 注入：落账瞬时失败")
}

// Q8 甲「其余错误」：Send 落账瞬时失败 → 零条、认领留在飞、游标不越过、不记 handled；恢复后下一轮恰一条。
func TestB435NotifyTransientFailureRetries(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	real := e.srv.rooms
	e.srv.SetRooms(collab.New(failingSendLC{LedgerClient: e.srv.autoLedger}))
	cardID, seq := e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("落账失败应零条，得 %d", n)
	}
	if exists, done, _ := e.claim(t, ledger.EvCardCreated, seq); !exists || done {
		t.Fatalf("瞬时失败认领应留在飞: exists=%v done=%v", exists, done)
	}
	if c := e.cursor(); c >= seq {
		t.Fatalf("在飞认领应挡住水位（< %d），得 %d", seq, c)
	}
	if e.srv.projectNotify.isHandled(seq) {
		t.Fatal("瞬时失败不得记 handled")
	}
	e.srv.SetRooms(real)
	b435Consume(t, e.ledgerEnv)
	got := e.notices(t)
	if len(got) != 1 {
		t.Fatalf("恢复后应恰一条，得 %d", len(got))
	}
	assertNotice(t, got[0], cardID, seq, "新建")
	if _, done, _ := e.claim(t, ledger.EvCardCreated, seq); !done {
		t.Fatal("恢复后认领应已收尾")
	}
}

// 认领之前的账本读失败（读项目房间绑定失败）同属「其余错误」：本机取认领挡住水位、不记 handled；恢复后恰一条。
func TestB435NotifyPreClaimReadFailureHoldsWatermark(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	db := e.rawDB(t)
	for _, stmt := range []string{`ALTER TABLE project_rooms RENAME TO project_rooms_b435`,
		`CREATE VIEW project_rooms AS SELECT 1 AS broken`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	cardID, seq := e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("读失败应零条，得 %d", n)
	}
	if exists, done, _ := e.claim(t, ledger.EvCardCreated, seq); !exists || done {
		t.Fatalf("读失败应持认领挡水位: exists=%v done=%v", exists, done)
	}
	if c := e.cursor(); c >= seq {
		t.Fatalf("水位应被挡在 %d 之前，得 %d", seq, c)
	}
	for _, stmt := range []string{`DROP VIEW project_rooms`, `ALTER TABLE project_rooms_b435 RENAME TO project_rooms`} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	b435Consume(t, e.ledgerEnv)
	got := e.notices(t)
	if len(got) != 1 {
		t.Fatalf("恢复后应恰一条，得 %d", len(got))
	}
	assertNotice(t, got[0], cardID, seq, "新建")
}

// 持有认领后事件变为不适用（驻场者请下）→ 本机收尾自己的认领、零条、游标越过——在飞认领不得永久挡水位
// （台账 L4.2 的停摆形状）。
func TestB435NotifyHeldClaimReleasedWhenNoLongerApplicable(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	real := e.srv.rooms
	e.srv.SetRooms(collab.New(failingSendLC{LedgerClient: e.srv.autoLedger}))
	_, seq := e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	if exists, done, _ := e.claim(t, ledger.EvCardCreated, seq); !exists || done {
		t.Fatalf("前提：认领在飞: exists=%v done=%v", exists, done)
	}
	e.srv.SetRooms(real)
	if err := e.srv.rooms.StandResident(e.room, b435Organizer); err != nil {
		t.Fatal(err)
	}
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("驻场者已请下应零条，得 %d", n)
	}
	if _, done, _ := e.claim(t, ledger.EvCardCreated, seq); !done {
		t.Fatal("不再适用时应收尾本机认领")
	}
	if c := e.cursor(); c < seq {
		t.Fatalf("收尾后游标应越过 %d，得 %d", seq, c)
	}
}

// Q8 甲「确定性拒绝」/ Q11 甲：项目房间已归档 → 零条、认领收尾、游标推进、Warn 日志带只读原因。
func TestB435NotifyArchivedRoomGivesUp(t *testing.T) {
	e := newB435NotifyEnv(t, true)
	if err := e.srv.rooms.ArchiveSession(e.room, "user:tester"); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, e.ledgerEnv)
	var buf bytes.Buffer
	e.srv.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cardID, seq := e.newCard(t, b435NotifyProject)
	b435Consume(t, e.ledgerEnv)
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("归档房间应零条，得 %d", n)
	}
	if exists, done, _ := e.claim(t, ledger.EvCardCreated, seq); !exists || !done {
		t.Fatalf("确定性拒绝应收尾认领: exists=%v done=%v", exists, done)
	}
	if c := e.cursor(); c < seq {
		t.Fatalf("游标应越过 %d，得 %d", seq, c)
	}
	logs := buf.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, collab.ErrReadOnly.Error()) ||
		!strings.Contains(logs, cardID) {
		t.Fatalf("应有 Warn 日志带只读原因与卡号:\n%s", logs)
	}
}

// 源码守卫：通知器不做寻址判定（不调 MessageWakeTargets/AddressesCard/IsHumanAuthor、不读 .Mentions
// 选择子——复合字面量键 `Mentions:` 是 KeyValueExpr，不在禁令内），也不经 keystone 唤醒任何人。
func TestB435ProjectNotifySourceGuard(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "projectnotify.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 projectnotify.go: %v", err)
	}
	for _, imp := range f.Imports {
		if strings.Contains(imp.Path.Value, "/keystone") {
			t.Errorf("projectnotify.go 不得 import keystone（通知不唤醒）: %s", imp.Path.Value)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "MessageWakeTargets", "AddressesCard", "IsHumanAuthor":
			t.Errorf("%s 调用了 %s——寻址判定唯一入口在 collab", fset.Position(sel.Pos()), sel.Sel.Name)
		case "Mentions":
			t.Errorf("%s 读了 .Mentions——通知器不解析寻址", fset.Position(sel.Pos()))
		case "keystone", "Wake":
			t.Errorf("%s 触达 keystone/Wake——通知不唤醒", fset.Position(sel.Pos()))
		}
		return true
	})
}
