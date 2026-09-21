// wakeconsumer_b358_test.go —— B358.3 唤醒路径改接寻址的缝级测试。
//
// 职责：经真实 ledger Facade + 真会话/席位夹具走 consumeAutomationEventsOnce，
// 锁住「唤醒 = 寻址命中」的会话半边（条 29/30/31）与按 target 分流形状
// （条 47/48/50）。观察点是 fake keystone 的 resumes——briefing 里以
// "- [message] <Summary>" 列出唤醒事件，可同时断言 Kind 与命中条正文。
// 边界：不复制 collab 寻址规则（唯一入口 MessageWakeTargets，守卫见本文件
// TestB3583WakePathSourceGuard）；旧席位回复行为受 plan 岔口#1 约束（默认按契约）。
package agentd

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/proto"
)

// mustWakeSessionFixture 建会话、拉卡进群并预绑定席位，返回 (会话id, 群主svc)。
// 群主记法按 P4 统一外部会话身份（user:tester），同时是 @外部身份 反例的对照串。
func mustWakeSessionFixture(t *testing.T, env *ledgerEnv, cardID string) (string, *collab.Service) {
	t.Helper()
	svc := env.srv.rooms
	session, err := svc.CreateSession("唤醒对账场", "user:tester", "user:tester")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if err := svc.JoinCard(session.ID, cardID, "user:tester"); err != nil {
		t.Fatalf("拉卡进群: %v", err)
	}
	prebindConsumerSession(t, env, cardID) // 席位坐进卡（coordinate 来源，可被 keystone Resume）
	return session.ID, svc
}

// drainAutomation 先把夹具 setup 期间产生的全部事件消费掉（结构事件、席位事件
// 推进游标），让后续断言只面对测试显式落的消息。
func drainAutomation(t *testing.T, env *ledgerEnv) {
	t.Helper()
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("排水消费: %v", err)
	}
}

// sendSessionMessage 以指定身份向会话发言，返回事件 seq（门面回填 Room 与无卡落账）。
func sendSessionMessage(t *testing.T, svc *collab.Service, sessionID, actor, body string, mentions []string, replyTo int64) int64 {
	t.Helper()
	seq, err := svc.Send(sessionID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: body, Mentions: mentions, ReplyTo: replyTo,
	}, actor)
	if err != nil {
		t.Fatalf("会话发言: %v", err)
	}
	return seq
}

func TestB3583NoAddressingDoesNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	env.srv.automationMu.Lock()
	seenBefore := len(env.srv.automationSeen)
	env.srv.automationMu.Unlock()
	sendSessionMessage(t, svc, sessionID, "user:tester", "群里无寻址的普通发言", nil, 0)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 0 {
		t.Fatalf("无寻址会话消息不应唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("无寻址不得 Resume: %v", resumes)
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	seenAfter := len(env.srv.automationSeen)
	env.srv.automationMu.Unlock()
	if seenAfter != seenBefore+1 {
		t.Fatalf("无寻址消息必须标 seen：seen %d → %d", seenBefore, seenAfter)
	}
	maxSeq, err := env.ledger.MaxSeq()
	if err != nil {
		t.Fatal(err)
	}
	if cursor != maxSeq {
		t.Fatalf("游标未推进到流尾：cursor=%d maxSeq=%d", cursor, maxSeq)
	}
}

func TestB3583MentionCardWakesItsSeatOnce(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	sendSessionMessage(t, svc, sessionID, "user:tester", "请看这张卡", []string{cardID}, 0)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 1 {
		t.Fatalf("@卡号应恰一次唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("应恰一次 Resume，实得 %d", len(resumes))
	}
	// briefing 形状：- [message] <Summary>——Kind=WakeMessage 且 Summary=命中条正文。
	if !contains(resumes[0], "[message]") || !contains(resumes[0], "请看这张卡") {
		t.Fatalf("briefing 缺 WakeMessage kind 或命中条正文: %s", resumes[0])
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && strings.Contains(haystack, needle)
}

func TestB3583NonMemberAndMissingCardTargetsDoNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	// 空座卡：进群但没配人。
	emptyCard := createCoordCard(t, env)
	if err := svc.JoinCard(sessionID, emptyCard, "user:tester"); err != nil {
		t.Fatalf("空座卡拉进会话: %v", err)
	}
	sendSessionMessage(t, svc, sessionID, "user:tester", "@空座", []string{emptyCard}, 0)
	sendSessionMessage(t, svc, sessionID, "user:tester", "@不存在的卡", []string{"B99999"}, 0)
	sendSessionMessage(t, svc, sessionID, "user:tester", "@外部非成员", []string{"user:alice"}, 0)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 0 {
		t.Fatalf("空座/不存在卡/外部身份命中都不得 keystone 唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("零 Wake 反例失败: %v", resumes)
	}
	// 外部命中的未读承载（条 47「未读照记」：外部命中零动作，未读由事件+游标
	// 天然承载）。夹具共落 3 条会话消息（@空座、@不存在的卡、@外部非成员），
	// user:alice 无游标 → 三条全未读。plan 原文断言 want 1 与其自身夹具矛盾
	// （同一投影下 Task 4 TestSessionWaitReferencedAndUnread 也是 3 条=3），
	// 执行者按投影事实对齐为 3，见台账偏差清单第 1 条；断言仍锁「外部分支
	// 不得写游标/吞事件破坏未读」。
	summaries, err := svc.ListSessions("user:alice")
	if err != nil {
		t.Fatal(err)
	}
	unread := 0
	for _, s := range summaries {
		if s.ID == sessionID {
			unread = s.Unread
		}
	}
	if unread != 3 {
		t.Fatalf("@外部身份的会话未读数=%d，want 3（未读照记，含命中条）", unread)
	}
}

func TestB3583ReplyToSeatAuthorWakesItsCard(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	seat, err := env.ledger.GetCard(cardID)
	if err != nil || seat.DriverSession == "" {
		t.Fatalf("读席位: %v", err)
	}
	original := sendSessionMessage(t, svc, sessionID, seat.DriverSession, "协调者的现场汇报", nil, 0)
	sendSessionMessage(t, svc, sessionID, "user:tester", "收到，请继续", nil, original)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 1 {
		t.Fatalf("回复席位作者应唤醒该卡一次，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 || !contains(resumes[0], "收到，请继续") {
		t.Fatalf("reply 命中未唤醒或缺命中条: %v", resumes)
	}
}

func TestB3583ReplyToReboundOldSeatDoesNotWake(t *testing.T) {
	// plan 岔口#1 的默认断言（按契约条 47 字面：旧席位 ≠ 当前 driver_session
	// → 不命中）。协调者若拍板翻转（breakdown 读法），本测试与
	// roomMessageWakeEvents 的席位历史归映射一起改，见 plan §4。
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	oldSeat, err := env.ledger.GetCard(cardID)
	if err != nil || oldSeat.DriverSession == "" {
		t.Fatalf("读旧席位: %v", err)
	}
	original := sendSessionMessage(t, svc, sessionID, oldSeat.DriverSession, "换绑前的汇报", nil, 0)
	newIdentity, err := proto.EncodeSeatIdentity("opencode", "rebound-session-1")
	if err != nil {
		t.Fatalf("编码新席位: %v", err)
	}
	// RebindSeat 四参：expect = 当前席位（CAS 语义与 MoveCard 同款）。
	if err := env.ledger.RebindSeat(cardID, newIdentity, proto.SeatSourceCoordinate, oldSeat.DriverSession, ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	sendSessionMessage(t, svc, sessionID, "user:tester", "回复换绑前的旧消息", nil, original)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 0 {
		t.Fatalf("旧席位 target 不命中（契约条 47 默认），processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("旧席位不得唤醒: %v", resumes)
	}
}

func TestB3583ReboundCardMentionHitsNewSeat(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	oldSeat, err := env.ledger.GetCard(cardID)
	if err != nil {
		t.Fatal(err)
	}
	newIdentity, err := proto.EncodeSeatIdentity("opencode", "rebound-session-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.RebindSeat(cardID, newIdentity, proto.SeatSourceCoordinate, oldSeat.DriverSession, ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	sendSessionMessage(t, svc, sessionID, "user:tester", "换绑后再叫这张卡", []string{cardID}, 0)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 1 {
		t.Fatalf("换绑后 @卡号 应唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("应恰一次 Resume: %v", resumes)
	}
	if contains(resumes[0], oldSeat.DriverSession) {
		t.Fatalf("旧席位身份不得出现在唤醒载荷（旧席位身份不再命中）: %s", resumes[0])
	}
	if !contains(resumes[0], "换绑后再叫这张卡") {
		t.Fatalf("briefing 缺命中条: %s", resumes[0])
	}
}

func TestB3583MixedHitsNeverMergeOrBroadcast(t *testing.T) {
	// 条 48：席位命中与外部命中互不合并、互不广播。
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	sendSessionMessage(t, svc, sessionID, "user:tester", "叫卡上的协调者，同时知会外部人", []string{cardID, "user:alice"}, 0)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 1 {
		t.Fatalf("混合命中下席位唤醒恰一次，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("外部命中不得产生第二个 keystone 调用（不广播）: %v", resumes)
	}
	if !contains(resumes[0], "叫卡上的协调者") {
		t.Fatalf("席位唤醒缺命中条: %s", resumes[0])
	}
}

func TestB3583StructureEventsNeverWake(t *testing.T) {
	// 条 31/50：结构事件逐值零唤醒，且不得被当成 room_message 消费。
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	// 逐值显式生产（次序受生命周期约束：归档必须最后——归档会话拒绝 Join/Leave）：
	// 移出卡（session_card_left）→ 新卡进群（session_card_joined）→ 空座坐下
	// （driver_seat_bound，BindSeat 只吃空座）→ 换绑（driver_takeover）→
	// 归档（session_archived）。
	if err := svc.LeaveCard(sessionID, cardID, "user:tester"); err != nil {
		t.Fatalf("移出: %v", err)
	}
	freshCard := createCoordCard(t, env)
	if err := svc.JoinCard(sessionID, freshCard, "user:tester"); err != nil {
		t.Fatalf("进群: %v", err)
	}
	if err := env.ledger.BindSeat(freshCard, "cli:opencode#seat-bound", proto.SeatSourceCoordinate, ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("坐下: %v", err)
	}
	if err := env.ledger.RebindSeat(freshCard, "cli:opencode#seat-rebound", proto.SeatSourceCoordinate, "cli:opencode#seat-bound", ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	if err := svc.ArchiveSession(sessionID, "user:tester"); err != nil {
		t.Fatalf("归档: %v", err)
	}
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 0 {
		t.Fatalf("结构事件零唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("结构事件不得 Resume: %v", resumes)
	}
	events, err := env.ledger.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"session_archived", "session_card_left", "session_card_joined", "driver_seat_bound", "driver_takeover"} {
		found := false
		for _, ev := range events {
			if ev.Type == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("结构事件 %s 应原样在账（只是不唤醒）", want)
		}
	}
}

func TestB3583CardRoomMessagesDoNotWake(t *testing.T) {
	// 裁定 1：广播形状删除后，非会话房间的 room_message 一律不唤醒
	// （旧卡房间只读归档，生产仅剩 pointer；此断言锁「删除」本身）。
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	drainAutomation(t, env)

	appendUserMessage(t, env.ledger, cardID, "旧卡房间的人类消息", false)
	appendPointerMessage(t, env.ledger, cardID)
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated || processed != 0 {
		t.Fatalf("卡房间消息不唤醒，processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("卡房间消息不得 Resume: %v", resumes)
	}
}

// TestB3583WakePathSourceGuard 是唤醒路径的源码级守卫（先例
// pointer_gate_test.go#TestPointerRouteAbsentFromSource、
// delivery_gate_test.go#TestWakePathSourceUsesAddressing）。
// 扇出禁令：唤醒必须以寻址命中为必要条件，写不出别的形状——
//  1. 广播形状函数 decodeHumanRoomMessage 必须已删（breakdown §3.4 ③）；
//  2. 「Kind==user 即唤醒」的旧闸不得回潮（条 30）；
//  3. agentd 不得本地解析 mentions（条 42/契约 §9：唯一入口在 collab）；
//  4. 会话分流函数体必须调用 MessageWakeTargets（AST 级，条 42）。
//
// 为什么是随 go test 复现的读源码测试而不是 graph check：新增真实调用而不写进
// 视图 diff 时，闸门眼里它不存在（B156.3 决定性实验）。
func TestB3583WakePathSourceGuard(t *testing.T) {
	raw, err := os.ReadFile("wakeconsumer.go")
	if err != nil {
		t.Fatalf("读 wakeconsumer.go 源: %v", err)
	}
	src := string(raw)
	for _, banned := range []string{
		"decodeHumanRoomMessage",
		"proto.RoomMsgUser",
		".Mentions",
		"broadcastToMembers", "fanOutToMembers", "notifyAllMembers", "wakeAllMembers",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("wakeconsumer.go 出现禁令形状 %q——广播回潮或绕过寻址唯一入口", banned)
		}
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "wakeconsumer.go", src, 0)
	if err != nil {
		t.Fatalf("解析 wakeconsumer.go: %v", err)
	}
	var dispatch *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "roomMessageWakeEvents" {
			dispatch = d
		}
		return true
	})
	if dispatch == nil {
		t.Fatalf("roomMessageWakeEvents 不存在——会话消息寻址分流入口缺失")
	}
	callsAddressing := false
	ast.Inspect(dispatch, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "MessageWakeTargets" {
				callsAddressing = true
			}
		}
		return true
	})
	if !callsAddressing {
		t.Fatalf("会话分流未经 MessageWakeTargets——寻址判定唯一入口被绕过（条 42）")
	}
}
