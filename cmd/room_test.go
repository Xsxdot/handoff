// room 命令族测试（B156.2 C7）：room list/read/send/inbox 的接线与输出契约。
// 入口全部是 CLI 命令（spec 测试接缝清单 #1 的 CLI 调用方），数据穿真 SQLite
// 账本（list/read/send）或 mock agentd（inbox 走 HTTP，breakdown 岔口三裁决）。
// 本文件 import internal/ledger(/proto) 仅存在于 _test.go：不构成生产跨域边。
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

// TestRoomSendLandsRoomMessageWithUserKind 默认 --kind user 落账 + actor 沿用
// cli:<user>@<host> 约定：事件 actor 以 cli: 开头，payload 解回 RoomMessage 且
// kind==user、body 逐字一致。stdout 契约 = {"ok":true,"seq":<n>}。
func TestRoomSendLandsRoomMessageWithUserKind(t *testing.T) {
	dir := t.TempDir()
	id := mustAddCard(t, dir, "房间发言卡")
	out, _, err := runLedgerCLI(t, dir, "room", "send", id, "先停一下")
	if err != nil {
		t.Fatalf("room send: %v", err)
	}
	var resp struct {
		OK  bool  `json:"ok"`
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil || !resp.OK || resp.Seq <= 0 {
		t.Fatalf("room send stdout = %q err=%v", out, err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc([]string{id}, 0, 100)
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
	if !strings.HasPrefix(found.Actor, "cli:") {
		t.Fatalf("actor 应沿用 cli:<user>@<host> 约定: %q", found.Actor)
	}
	var msg proto.RoomMessage
	if err := json.Unmarshal(found.Payload, &msg); err != nil {
		t.Fatalf("解 payload: %v", err)
	}
	if msg.Kind != proto.RoomMsgUser {
		t.Fatalf("默认 kind 应为 user: %q", msg.Kind)
	}
	if msg.Body != "先停一下" {
		t.Fatalf("正文不符: %q", msg.Body)
	}
}

func TestRoomSendCoordinatorAcceptsExplicitSeatFlags(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "手填房间协调者卡")
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(id, "cli:claude#room-seat", proto.SeatSourceBind); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	out, _, err := runLedgerCLI(t, dir, "room", "send", id, "协调者正文", "--kind", "reply",
		"--cli", "claude", "--session", "room-seat")
	if err != nil {
		t.Fatalf("room send with explicit identity: %v", err)
	}
	var response struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &response); err != nil || response.Seq <= 0 {
		t.Fatalf("room send stdout = %q, err=%v", out, err)
	}
	st, err = ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc([]string{id}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Seq != response.Seq {
			continue
		}
		if event.Actor != "cli:claude#room-seat" {
			t.Fatalf("room actor = %q, want cli:claude#room-seat", event.Actor)
		}
		var message proto.RoomMessage
		if err := json.Unmarshal(event.Payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.Kind != "reply" || message.Body != "协调者正文" {
			t.Fatalf("room message = %+v", message)
		}
		return
	}
	t.Fatalf("没有找到 seq=%d 的 room_message", response.Seq)
}

// TestRoomSendCoordinatorKindUsesGrokHostSession 缝：room send --kind reply 的
// 席位来自 GROK_SESSION_ID（grok 宿主键分支），而非手填 --cli/--session。
// card bind 无 flag 落座 cli:grok#grok-room，随后 room send --kind reply 无
// flag 复用同一出示路径，落账 actor=cli:grok#grok-room、kind=reply、body 逐字一致。
func TestRoomSendCoordinatorKindUsesGrokHostSession(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("GROK_SESSION_ID", "grok-room")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "grok 宿主房间协调者卡")
	out, _, err := runLedgerCLI(t, dir, "card", "bind", id)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("card bind: out=%q err=%v", out, err)
	}
	out, _, err = runLedgerCLI(t, dir, "room", "send", id, "grok 宿主回复", "--kind", "reply")
	if err != nil {
		t.Fatalf("room send --kind reply with grok host session: %v", err)
	}
	var resp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil || resp.Seq <= 0 {
		t.Fatalf("room send stdout = %q, err=%v", out, err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc([]string{id}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Seq != resp.Seq {
			continue
		}
		if event.Actor != "cli:grok#grok-room" {
			t.Fatalf("room actor = %q, want cli:grok#grok-room", event.Actor)
		}
		var message proto.RoomMessage
		if err := json.Unmarshal(event.Payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.Kind != "reply" || message.Body != "grok 宿主回复" {
			t.Fatalf("room message = %+v", message)
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

// TestRoomSendCarriesRefAndMention --ref/--mention 可重复、进载荷。
func TestRoomSendCarriesRefAndMention(t *testing.T) {
	dir := t.TempDir()
	id := mustAddCard(t, dir, "引用卡")
	out, _, err := runLedgerCLI(t, dir, "room", "send", id, "正文",
		"--ref", "docs/x.md", "--ref", "B156", "--mention", "B145")
	if err != nil {
		t.Fatalf("room send: %v", err)
	}
	var resp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("解析 send 输出: %v", err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc([]string{id}, 0, 100)
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

// TestRoomReadStdoutSeqPrefix read 输出每行以 #<seq> 前缀（stdout 文本契约），
// --after 排他。
func TestRoomReadStdoutSeqPrefix(t *testing.T) {
	dir := t.TempDir()
	id := mustAddCard(t, dir, "历史卡")
	out, _, err := runLedgerCLI(t, dir, "room", "send", id, "第一条")
	if err != nil {
		t.Fatalf("room send: %v", err)
	}
	var resp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("解析 send 输出: %v", err)
	}
	read, _, err := runLedgerCLI(t, dir, "room", "read", id)
	if err != nil {
		t.Fatalf("room read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(read, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "#"+fmt.Sprint(resp.Seq)+"\t") {
		t.Fatalf("read 输出每行应以 #<seq> 前缀: %q", read)
	}
	after, _, err := runLedgerCLI(t, dir, "room", "read", id, "--after", fmt.Sprint(resp.Seq))
	if err != nil {
		t.Fatalf("room read --after: %v", err)
	}
	if strings.TrimSpace(after) != "" {
		t.Fatalf("--after 等于最新 seq 应排他返回空: %q", after)
	}
}

// TestRoomListSortedByActivity list 按最近活动降序（后发言的卡排前）。
func TestRoomListSortedByActivity(t *testing.T) {
	dir := t.TempDir()
	a := mustAddCard(t, dir, "卡A")
	b := mustAddCard(t, dir, "卡B")
	if _, _, err := runLedgerCLI(t, dir, "room", "send", a, "a1"); err != nil {
		t.Fatalf("send a: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "room", "send", b, "b1"); err != nil {
		t.Fatalf("send b: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "room", "list")
	if err != nil {
		t.Fatalf("room list: %v", err)
	}
	ia, ib := strings.Index(out, a), strings.Index(out, b)
	if ia < 0 || ib < 0 || ib > ia {
		t.Fatalf("list 应按最近活动降序（后发言的 %s 应排 %s 前）: %q", b, a, out)
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
