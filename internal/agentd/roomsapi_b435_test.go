// roomsapi_b435_test.go —— B435 ⑤ 投递投影：GET /api/rooms/{id}/messages 的行级 no_recipient
// （契约 C-501–C-508；O1 plan Q10 甲）。
//
// 观察点：真 HTTP handler（httptest，经 ledgerGet 带鉴权）+ 真 Store + 真 collab 匹配函数。断言读
// 原始 body（map[string]json.RawMessage 区分缺键与空串），不经 Go 结构体回读。
package agentd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b435HistoryRows 经真 HTTP 读一页房间历史，返回信封顶层键与各行原始键表（按 seq 索引）。
func b435HistoryRows(t *testing.T, env *ledgerEnv, roomID string) ([]string, map[int64]map[string]json.RawMessage) {
	t.Helper()
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms/"+roomID+"/messages")
	if code != http.StatusOK {
		t.Fatalf("GET 房间历史应 200，得 %d: %s", code, body)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("body 不是 JSON 对象: %v %s", err, body)
	}
	top := make([]string, 0, len(envelope))
	for k := range envelope {
		top = append(top, k)
	}
	sort.Strings(top)
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["messages"], &rows); err != nil {
		t.Fatalf("messages 不是数组: %v", err)
	}
	bySeq := map[int64]map[string]json.RawMessage{}
	for _, r := range rows {
		var seq int64
		if err := json.Unmarshal(r["seq"], &seq); err != nil {
			t.Fatal(err)
		}
		bySeq[seq] = r
	}
	return top, bySeq
}

func b435RowReason(t *testing.T, row map[string]json.RawMessage) (string, bool) {
	t.Helper()
	raw, has := row["no_recipient"]
	if !has {
		return "", false
	}
	var reason string
	if err := json.Unmarshal(raw, &reason); err != nil {
		t.Fatalf("no_recipient 不是字符串: %s", raw)
	}
	return reason, true
}

func b435Keys(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// b435LedgerEventKeys 是 B435 前 LedgerEvent 的 wire 键集（同一事件经 History 在进程内取回再编码）。
func b435LedgerEventKeys(t *testing.T, env *ledgerEnv, roomID string, seq int64) string {
	t.Helper()
	history, err := env.srv.rooms.History(roomID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range history {
		if ev.Seq == seq {
			raw, err := json.Marshal(ev)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			return b435Keys(m)
		}
	}
	t.Fatalf("History 无 seq=%d", seq)
	return ""
}

// b435RawSessionRow 直写一条无卡 room_message（旧边界载荷，如 by_system:null），返回 seq。
func b435RawSessionRow(t *testing.T, env *ledgerEnv, payload string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := db.Exec(`INSERT INTO card_events (card_id, type, actor, payload, created_at) VALUES (NULL, ?, ?, ?, ?)`,
		ledger.EvRoomMessage, "user:tester", payload, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func b435NewSession(t *testing.T, env *ledgerEnv, cards ...string) string {
	t.Helper()
	s, err := env.srv.rooms.CreateSession("B435 投影", "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cards {
		if err := env.srv.rooms.JoinCard(s.ID, c, "user:tester"); err != nil {
			t.Fatal(err)
		}
	}
	return s.ID
}

// C-501–C-504 + Q10 甲：逐行出键当且仅当 Recipients 地址集为空，取值逐字 = 原因；有收件人行键集 = 旧
// LedgerEvent 键集；信封仍为 {"messages": [...]}。
func TestB435RoomHistoryNoRecipientProjection(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	svc := env.srv.rooms
	// S1：根卡恰一张且空座；agent:helper 为显式成员（agent: 无 @）。
	empty := createCoordCard(t, env)
	s1 := b435NewSession(t, env, empty)
	if err := env.ledger.AddSessionMember(s1, "agent:helper", "user:tester"); err != nil {
		t.Fatal(err)
	}
	unresolved := sendSessionMessage(t, svc, s1, "user:tester", "@空座卡", []string{empty}, 0)
	rootNoSeat := sendSessionMessage(t, svc, s1, "user:tester", "人类无 @", nil, 0)
	nonHuman := sendSessionMessage(t, svc, s1, "agent:helper", "agent 无 @", nil, 0)
	pointer, err := svc.Pointer(s1, proto.RoomMessage{Body: "指针行"})
	if err != nil {
		t.Fatal(err)
	}
	legacyNull := b435RawSessionRow(t, env, `{"room":"`+s1+`","kind":"user","body":"旧边界","by_system":null}`)
	// S2：两根卡，人类无 @。
	s2 := b435NewSession(t, env, createCoordCard(t, env), createCoordCard(t, env))
	needsMention := sendSessionMessage(t, svc, s2, "user:tester", "两根卡无 @", nil, 0)
	// S3：根卡有席位（默认收件人命中）；另有驻场者被显式 @。
	seated := createCoordCard(t, env)
	b435BindPush(t, env, seated, "proj-1")
	s3 := b435NewSession(t, env, seated)
	defaultHit := sendSessionMessage(t, svc, s3, "user:tester", "命中根卡席位", nil, 0)
	if _, err := svc.SitResident(proto.SessionResident{Session: s3, Identity: "agent:org"}); err != nil {
		t.Fatal(err)
	}
	explicitResident := sendSessionMessage(t, svc, s3, "user:tester", "@驻场者", []string{"agent:org"}, 0)

	want := map[string]map[int64]string{
		s1: {unresolved: string(proto.NoRecipientAddressedUnresolved), rootNoSeat: string(proto.NoRecipientRootNoSeat),
			nonHuman: string(proto.NoRecipientNonHumanUnaddressed), pointer: string(proto.NoRecipientStructural),
			legacyNull: string(proto.NoRecipientStructural)},
		s2: {needsMention: string(proto.NoRecipientNeedsMention)},
		s3: {defaultHit: "", explicitResident: ""},
	}
	for roomID, rows := range want {
		top, got := b435HistoryRows(t, env, roomID)
		if strings.Join(top, ",") != "messages" {
			t.Fatalf("%s：信封顶层键应只有 messages，得 %v", roomID, top)
		}
		for seq, reason := range rows {
			row, ok := got[seq]
			if !ok {
				t.Fatalf("%s：历史缺 seq=%d", roomID, seq)
			}
			gotReason, has := b435RowReason(t, row)
			if reason == "" {
				if has {
					t.Errorf("%s seq=%d 有收件人不得出键，得 %q", roomID, seq, gotReason)
				}
				if keys := b435Keys(row); keys != b435LedgerEventKeys(t, env, roomID, seq) {
					t.Errorf("%s seq=%d 不出键行的键集应等于旧 LedgerEvent 键集，得 %s", roomID, seq, keys)
				}
				continue
			}
			if !has || gotReason != reason {
				t.Errorf("%s seq=%d no_recipient 应为 %q，得 %q（has=%v）", roomID, seq, reason, gotReason, has)
			}
		}
	}
}

// failingMentionCardLC 让对某个 mention 的卡读失败（Recipients 显式分支读账本失败）；其余透传真 Facade。
type failingMentionCardLC struct {
	client.LedgerClient
	target string
}

func (f failingMentionCardLC) GetCard(id string) (proto.Card, error) {
	if id == f.target {
		return proto.Card{}, errors.New("b435 注入：卡读失败")
	}
	return f.LedgerClient.GetCard(id)
}

// C-507：某行 Recipients 读失败 → 该行不出键（不是任何 NoRecipientReason）、整页 200、Warn 带 room/seq/cause；
// 同页其余行照常投影。
func TestB435RoomHistoryRecipientsErrorRowOmitsKey(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	empty := createCoordCard(t, env)
	s := b435NewSession(t, env, empty)
	broken := sendSessionMessage(t, env.srv.rooms, s, "user:tester", "@读失败", []string{"BOOM9"}, 0)
	fine := sendSessionMessage(t, env.srv.rooms, s, "user:tester", "@空座卡", []string{empty}, 0)
	env.srv.SetRooms(collab.New(failingMentionCardLC{LedgerClient: env.srv.autoLedger, target: "BOOM9"}))
	var buf bytes.Buffer
	env.srv.log = slog.New(slog.NewTextHandler(&buf, nil))
	_, rows := b435HistoryRows(t, env, s)
	if reason, has := b435RowReason(t, rows[broken]); has {
		t.Fatalf("读失败行不得出键（不得投影为任何原因），得 %q", reason)
	}
	if reason, _ := b435RowReason(t, rows[fine]); reason != string(proto.NoRecipientAddressedUnresolved) {
		t.Fatalf("同页其余行应照常投影，得 %q", reason)
	}
	logs := buf.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "room="+s) ||
		!strings.Contains(logs, "seq="+strconv.FormatInt(broken, 10)) || !strings.Contains(logs, "b435 注入：卡读失败") {
		t.Fatalf("应有行级 Warn（room、seq、cause）:\n%s", logs)
	}
}

func b435CountRows(t *testing.T, env *ledgerEnv, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// C-506 / D-E：读时求值——@空座卡的旧消息先出 addressed_unresolved，补座后同一页再读不出键；读请求不落账、
// 不推进交付游标、不写认领。
func TestB435RoomHistoryReadTimeEvaluation(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	card := createCoordCard(t, env)
	s := b435NewSession(t, env, card)
	seq := sendSessionMessage(t, env.srv.rooms, s, "user:tester", "@空座卡", []string{card}, 0)
	before := [3]int{b435CountRows(t, env, "card_events"), b435CountRows(t, env, "session_delivery_cursors"), b435CountRows(t, env, "wake_claims")}
	_, rows := b435HistoryRows(t, env, s)
	if reason, _ := b435RowReason(t, rows[seq]); reason != string(proto.NoRecipientAddressedUnresolved) {
		t.Fatalf("补座前应 addressed_unresolved，得 %q", reason)
	}
	after := [3]int{b435CountRows(t, env, "card_events"), b435CountRows(t, env, "session_delivery_cursors"), b435CountRows(t, env, "wake_claims")}
	if before != after {
		t.Fatalf("读请求不得落账/推进游标/写认领: before=%v after=%v", before, after)
	}
	b435BindPush(t, env, card, "late-seat")
	_, rows = b435HistoryRows(t, env, s)
	if reason, has := b435RowReason(t, rows[seq]); has {
		t.Fatalf("补座后同一行应不出键（读时求值），得 %q", reason)
	}
}

// C-505 源码守卫：roomsapi.go 非测试源码不调 MessageWakeTargets/IsHumanAuthor、不读 .Mentions 选择子；
// handleRoomMessages 函数体调 Recipients（判定唯一来源）。
//
// .Mentions 的两处存量例外（基线 58dbecea 已有，均非寻址判定）：handleRoomSend 把请求体 mentions 原样交给
// collab.Send；collectInboxItems 调 collab.Service.Mentions 方法取收件箱提及源。例外按所在函数登记，其余
// 任何函数（含投影）新出现 .Mentions 即红。
var b435RoomsAPIMentionsAllowed = map[string]bool{"handleRoomSend": true, "collectInboxItems": true}

func TestB435RoomsAPISourceGuard(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "roomsapi.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 roomsapi.go: %v", err)
	}
	for _, d := range f.Decls {
		fn := ""
		if fd, ok := d.(*ast.FuncDecl); ok {
			fn = fd.Name.Name
		}
		ast.Inspect(d, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "MessageWakeTargets", "IsHumanAuthor":
					t.Errorf("%s 调用了 %s——投影判定唯一来源是 Recipients（C-505）", fset.Position(sel.Pos()), sel.Sel.Name)
				case "Mentions":
					if !b435RoomsAPIMentionsAllowed[fn] {
						t.Errorf("%s（%s）读了 .Mentions——投影不得自带寻址判定（C-505）", fset.Position(sel.Pos()), fn)
					}
				}
			}
			return true
		})
	}
	var handler *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "handleRoomMessages" {
			handler = fd
		}
	}
	if handler == nil {
		t.Fatal("handleRoomMessages 不存在")
	}
	calls := false
	ast.Inspect(handler, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Recipients" {
				calls = true
			}
			// d_gateway→d_collab 只经入站门面（target.json entries「collab 入站门面」，预算 0）：
			// 投影不得调 collab/room 包级函数（段 7 check 读数，台账 #16）。
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "room" {
					t.Errorf("%s handleRoomMessages 调了 room.%s——须经 collab 入站门面", fset.Position(call.Pos()), sel.Sel.Name)
				}
			}
		}
		return true
	})
	if !calls {
		t.Fatal("handleRoomMessages 未调用 Recipients（C-505）")
	}
}
