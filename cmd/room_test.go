// room 命令族测试（B156.2 C7）：room list/read/send/inbox 的接线与输出契约。
// 入口全部是 CLI 命令（spec 测试接缝清单 #1 的 CLI 调用方），数据穿真 SQLite
// 账本（list/read/send）或 mock agentd（inbox 走 HTTP，breakdown 岔口三裁决）。
// 本文件 import internal/ledger(/proto) 仅存在于 _test.go：不构成生产跨域边。
// B358.5 红窗改写：下方标注「B358.5 红窗改写」的 6 支原以 room send 卡房间/
// 群房间成功为断言或夹具，随 B358.1 S1 只读归档失效；按新会话语义就地改写
// （名字保留供红窗核算逐支比对），断言意图逐支对应迁移，见
// docs/superpowers/plans/b358.5-plan.md §5。
package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// mustAddCard 建一张卡并返回 id（card add 首跑自动种 workflow/template）。
func mustAddCard(t *testing.T, dir, title string) string {
	t.Helper()
	out, _, err := runLedgerCLI(t, dir, "card", "add", title, "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("card add: %v", err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatalf("解析 card add 输出 %q: %v", out, err)
	}
	return c.ID
}

// TestRoomSendLandsRoomMessageWithUserKind（B358.5 红窗改写）：原断言「room send
// 卡房间 kind=user 落账成功」随 S1 只读归档失效。改锁两半：(a) 旧面报错形状
// （P7 保留报错——S1 归档语义经 CLI 呈现）；(b) kind=user 落账成功迁 session send
// 人形态，actor 沿用 cli:<user>@<host> 审计注入面，stdout 契约不变。
func TestRoomSendLandsRoomMessageWithUserKind(t *testing.T) {
	dir := t.TempDir()
	id := mustAddCard(t, dir, "房间发言卡")
	_, errb, err := runLedgerCLI(t, dir, "room", "send", id, "先停一下")
	if err == nil {
		t.Fatal("旧卡房间 room send 必须非 0（S1 只读归档，P7 保留报错形状）")
	}
	if !strings.Contains(errb, "只读") {
		t.Fatalf("报错应含只读语义（可行动）: %q", errb)
	}
	_, _, st, sessionID, actor := mustSendFixture(t, dir, "人形态发送场")
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "先停一下")
	if err != nil {
		t.Fatalf("session send: %v", err)
	}
	var resp struct {
		OK  bool  `json:"ok"`
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil || !resp.OK || resp.Seq <= 0 {
		t.Fatalf("session send stdout = %q err=%v", out, err)
	}
	// 会话消息是无卡事件 → 全流读（EventsFromAsc(nil,…)），按 seq 定位。
	events, err := st.EventsFromAsc(nil, 0, 1000)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var found *ledger.Event
	for i := range events {
		if events[i].Type == ledger.EvRoomMessage && events[i].Seq == resp.Seq {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("账本没有该 seq 的 room_message 行: %+v", events)
	}
	if !strings.HasPrefix(found.Actor, "cli:") || found.Actor != actor {
		t.Fatalf("actor 应沿用 cli:<user>@<host> 注入面（%q）: %q", actor, found.Actor)
	}
	var msg proto.RoomMessage
	if err := json.Unmarshal(found.Payload, &msg); err != nil {
		t.Fatalf("解 payload: %v", err)
	}
	if msg.Kind != proto.RoomMsgUser {
		t.Fatalf("人形态 kind 应为 user: %q", msg.Kind)
	}
	if msg.Body != "先停一下" {
		t.Fatalf("正文不符: %q", msg.Body)
	}
}

// TestRoomSendCoordinatorAcceptsExplicitSeatFlags（B358.5 红窗改写）：席位落账
// 断言迁 session send 成对 flag 形态。夹具：卡进群 + BindSeat——席位身份因
// 「会话内卡当前席位」入书写者集（0.2#3）。旧断言 kind=reply 随白名单废止
// 收敛为 kind=user（breakdown §3.6 ③）。
func TestRoomSendCoordinatorAcceptsExplicitSeatFlags(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "手填房间协调者卡")
	_, facade, st, sessionID, _ := mustSendFixture(t, dir, "协调者席位场")
	if err := facade.JoinCardToSession(sessionID, id, "user:tester"); err != nil {
		t.Fatalf("夹具拉卡: %v", err)
	}
	if err := st.BindSeat(id, "cli:claude#room-seat", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "协调者正文",
		"--cli", "claude", "--session", "room-seat")
	if err != nil {
		t.Fatalf("session send with explicit identity: %v", err)
	}
	var response struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &response); err != nil || response.Seq <= 0 {
		t.Fatalf("session send stdout = %q, err=%v", out, err)
	}
	events, err := st.EventsFromAsc(nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Seq != response.Seq {
			continue
		}
		if event.Actor != "cli:claude#room-seat" {
			t.Fatalf("session actor = %q, want cli:claude#room-seat", event.Actor)
		}
		var message proto.RoomMessage
		if err := json.Unmarshal(event.Payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.Kind != proto.RoomMsgUser || message.Body != "协调者正文" {
			t.Fatalf("session message = %+v", message)
		}
		return
	}
	t.Fatalf("没有找到 seq=%d 的 room_message", response.Seq)
}

// TestRoomSendCoordinatorKindUsesGrokHostSession（B358.5 红窗改写）：原断言
// 「room send --kind reply 经 GROK_SESSION_ID 环境出示席位」随旧面只读失效。
// 会话面身份按 flag 显式出示（b358.5-plan §2.2 定稿点 2——环境席位键不参与
// session send，与 session wait 的显式 member 对称）；grok 宿主值保留作 flag
// 值，clearSeatSourceEnv 证明 flag 单独足够。落账 actor=cli:grok#grok-room、
// kind=user、body 逐字一致。
func TestRoomSendCoordinatorKindUsesGrokHostSession(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "grok 宿主房间协调者卡")
	_, facade, st, sessionID, _ := mustSendFixture(t, dir, "grok 席位场")
	if err := facade.JoinCardToSession(sessionID, id, "user:tester"); err != nil {
		t.Fatalf("夹具拉卡: %v", err)
	}
	if err := st.BindSeat(id, "cli:grok#grok-room", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "grok 宿主回复",
		"--cli", "grok", "--session", "grok-room")
	if err != nil {
		t.Fatalf("session send with grok identity flags: %v", err)
	}
	var resp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil || resp.Seq <= 0 {
		t.Fatalf("session send stdout = %q, err=%v", out, err)
	}
	events, err := st.EventsFromAsc(nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Seq != resp.Seq {
			continue
		}
		if event.Actor != "cli:grok#grok-room" {
			t.Fatalf("session actor = %q, want cli:grok#grok-room", event.Actor)
		}
		var message proto.RoomMessage
		if err := json.Unmarshal(event.Payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.Kind != proto.RoomMsgUser || message.Body != "grok 宿主回复" {
			t.Fatalf("session message = %+v", message)
		}
		return
	}
	t.Fatalf("没有找到 seq=%d 的 room_message", resp.Seq)
}

func TestRoomSendUserRejectsSeatFlagsWithoutSideEffects(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "user 禁用 flag 卡")
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.EventsFromAsc([]string{id}, 0, 100)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	if _, _, err := runLedgerCLI(t, dir, "room", "send", id, "不应落账", "--kind", "user", "--cli", "grok"); err == nil {
		t.Fatal("kind=user 带身份 flag 必须失败")
	}
	st, err = ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	after, err := st.EventsFromAsc([]string{id}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("user flag 拒绝不应增加事件: before=%d after=%d", len(before), len(after))
	}
	card, err := st.GetCard(id)
	if err != nil || card.DriverSession != "" || card.DriverSource != "" {
		t.Fatalf("user flag 拒绝不应改变席位: %+v, err=%v", card, err)
	}
}

// TestRoomSendCarriesRefAndMention（B358.5 红窗改写）：--ref/--mention 可重复、
// 进载荷断言迁 session send（写入面只在会话房间）；读账本改全流读。
func TestRoomSendCarriesRefAndMention(t *testing.T) {
	dir := t.TempDir()
	_, _, st, sessionID, _ := mustSendFixture(t, dir, "引用场")
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "正文",
		"--ref", "docs/x.md", "--ref", "B156", "--mention", "B145")
	if err != nil {
		t.Fatalf("session send: %v", err)
	}
	var resp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("解析 send 输出: %v", err)
	}
	events, err := st.EventsFromAsc(nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for i := range events {
		if events[i].Seq != resp.Seq {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(events[i].Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if len(msg.Refs) != 2 || msg.Refs[0] != "docs/x.md" || msg.Refs[1] != "B156" {
			t.Fatalf("refs 载荷漂移: %+v", msg.Refs)
		}
		if len(msg.Mentions) != 1 || msg.Mentions[0] != "B145" {
			t.Fatalf("mentions 载荷漂移: %+v", msg.Mentions)
		}
		return
	}
	t.Fatal("账本没有该 seq 的 room_message 行")
}

// TestRoomReadStdoutSeqPrefix（B358.5 红窗改写）：#<seq> 前缀与 --after 排他
// 断言照抄，夹具发言迁 session send、读面用 room read <session:n>（读侧对
// 会话房间同一 History 面）；新增旧房间读面对质保留断言（S1 P5：读史不断）。
func TestRoomReadStdoutSeqPrefix(t *testing.T) {
	dir := t.TempDir()
	cardID := mustAddCard(t, dir, "历史卡")
	_, _, _, sessionID, _ := mustSendFixture(t, dir, "历史场")
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "第一条")
	if err != nil {
		t.Fatalf("session send: %v", err)
	}
	var resp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("解析 send 输出: %v", err)
	}
	read, _, err := runLedgerCLI(t, dir, "room", "read", sessionID)
	if err != nil {
		t.Fatalf("room read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(read, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "#"+fmt.Sprint(resp.Seq)+"\t") {
		t.Fatalf("read 输出每行应以 #<seq> 前缀: %q", read)
	}
	after, _, err := runLedgerCLI(t, dir, "room", "read", sessionID, "--after", fmt.Sprint(resp.Seq))
	if err != nil {
		t.Fatalf("room read --after: %v", err)
	}
	if strings.TrimSpace(after) != "" {
		t.Fatalf("--after 等于最新 seq 应排他返回空: %q", after)
	}
	// 旧房间读面对质保留：卡房间读退出 0（空史合法——读侧宽容既有）。
	if _, _, err := runLedgerCLI(t, dir, "room", "read", cardID); err != nil {
		t.Fatalf("旧卡房间 room read 应可读（S1 P5 对质面）: %v", err)
	}
}

// TestRoomListSortedByActivity（B358.5 红窗改写）：活动降序断言迁 session list
// （旧 room list 不含会话行、且发言面只在会话——0.2 表 #20，旧列表无法经发言
// 复现排序场）。cmd 层呈现排序（b358.5-plan §2.1）：后发言的会话排前。
func TestRoomListSortedByActivity(t *testing.T) {
	dir := t.TempDir()
	_, _, _, idA, _ := mustSendFixture(t, dir, "卡A")
	_, _, _, idB, _ := mustSendFixture(t, dir, "卡B")
	if _, _, err := runLedgerCLI(t, dir, "session", "send", idA, "a1"); err != nil {
		t.Fatalf("send a: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", idB, "b1"); err != nil {
		t.Fatalf("send b: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "list")
	if err != nil {
		t.Fatalf("session list: %v", err)
	}
	ia, ib := strings.Index(out, idA), strings.Index(out, idB)
	if ia < 0 || ib < 0 || ib > ia {
		t.Fatalf("list 应按最近活动降序（后发言的 %s 应排 %s 前）: %q", idB, idA, out)
	}
}

// TestRoomInboxWalksAgentdHTTP inbox 走 agentd HTTP /api/inbox（岔口三裁决）：
// mock server 断言路径 + Bearer；输出每行一个 InboxItem JSON。
func TestRoomInboxWalksAgentdHTTP(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/inbox" {
			t.Errorf("非预期路径: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q, want Bearer %s", got, testToken)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			Items []proto.InboxItem `json:"items"`
		}{Items: []proto.InboxItem{
			{Origin: proto.InboxOriginDecision, Title: "推翻级简报", CardID: "B1", RefID: "7"},
			{Origin: proto.InboxOriginTicket, Title: "等待人工工单", RefID: "T-1"},
		}}); err != nil {
			t.Errorf("mock 编码: %v", err)
		}
	}))
	t.Cleanup(ts.Close)
	cfg := &config.Config{
		Listen: "127.0.0.1:0", Token: testToken, DataDir: dir, StallTimeout: 2 * time.Hour,
		Ledger: config.LedgerConfig{Enabled: true},
		Targets: map[string]config.Target{
			"mac-02": {Addr: strings.TrimPrefix(ts.URL, "http://"), Token: testToken},
		},
	}
	if err := config.Save(filepath.Join(dir, "config.yaml"), cfg); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "room", "inbox", "--target", "mac-02")
	if err != nil {
		t.Fatalf("room inbox: %v", err)
	}
	if !strings.Contains(out, `"origin":"decision"`) || !strings.Contains(out, `"title":"推翻级简报"`) || !strings.Contains(out, `"ref_id":"7"`) {
		t.Fatalf("inbox 输出缺 decision 条目: %q", out)
	}
	if !strings.Contains(out, `"origin":"ticket"`) || !strings.Contains(out, `"title":"等待人工工单"`) || !strings.Contains(out, `"ref_id":"T-1"`) {
		t.Fatalf("inbox 输出缺 ticket 条目: %q", out)
	}
}
