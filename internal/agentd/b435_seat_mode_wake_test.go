// b435_seat_mode_wake_test.go —— B435 消费循环按匹配函数地址与席位模式分流（契约 §4.4
// C-229–C-237、§4.5 C-315–C-324、C-328 agentd 半边）。
//
// 观察点：真账本 + 真 collab 匹配函数 + 真 consumeAutomationEventsOnce + fake runner
// （queueTraceRunner.resumes = keystone 回合次数）。「零 Wake」断言都在同一夹具里配一个
// 「恰一 Wake」对照，防「什么都不唤醒」的稳定假绿。
//
// 判别夹具：coordinate 来源 + SQL 直写 driver_mode=pull 的席位——来源词说「可唤醒」、
// 模式说「不唤醒」，只有按 mode 判定的实现能过（C-318：判定只读 mode，来源只作记录）。
package agentd

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func b435ExecSQL(t *testing.T, env *ledgerEnv, stmt string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开原始账本: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("执行 %q: %v", stmt, err)
	}
}

// b435BindPush 坐下一个 push 席位（coordinate 来源 + 本机承载，可被 keystone Resume）。
func b435BindPush(t *testing.T, env *ledgerEnv, card, sid string) string {
	t.Helper()
	identity := "cli:opencode#" + sid
	if err := env.ledger.BindSeatMode(card, identity, proto.SeatSourceCoordinate, proto.SeatModePush,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("坐 push 席位: %v", err)
	}
	return identity
}

// b435BindPull 坐下一个 pull 席位（card bind 同款：bind 来源、零承载）。
func b435BindPull(t *testing.T, env *ledgerEnv, card, sid string) string {
	t.Helper()
	identity := "cli:codex#" + sid
	if err := env.ledger.BindSeatMode(card, identity, proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatalf("坐 pull 席位: %v", err)
	}
	return identity
}

// b435ForceMode 把席位模式直写成 mode（判别夹具：来源词不变）。
func b435ForceMode(t *testing.T, env *ledgerEnv, card, mode string) {
	t.Helper()
	b435ExecSQL(t, env, `UPDATE cards SET driver_mode = ? WHERE id = ?`, mode, card)
}

func b435Session(t *testing.T, env *ledgerEnv, cards ...string) string {
	t.Helper()
	svc := env.srv.rooms
	session, err := svc.CreateSession("B435 席位模式场", "user:tester", "user:tester")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	for _, c := range cards {
		if err := svc.JoinCard(session.ID, c, "user:tester"); err != nil {
			t.Fatalf("拉卡 %s 进群: %v", c, err)
		}
	}
	return session.ID
}

func b435Resumes(runner *queueTraceRunner) []string {
	_, resumes, _ := runner.snapshot()
	return resumes
}

func b435WokeCard(briefing, card string) bool {
	return strings.Contains(briefing, "\n- 卡号："+card+"\n")
}

func b435Consume(t *testing.T, env *ledgerEnv) {
	t.Helper()
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
}

// C-318：三个文件非测试源码不出现 DriverSource ==/!= 比较（写入/ValidateSeat 传参不在禁令内）。
func TestB435SeatSourceComparisonGuard(t *testing.T) {
	cmp := regexp.MustCompile(`DriverSource\s*[!=]=|[!=]=\s*[\w.]*DriverSource\b`)
	for _, path := range []string{"../keystone/keystone.go", "wakeconsumer.go", "scheddrain.go"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s: %v", path, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if cmp.MatchString(line) {
				t.Errorf("%s:%d 按来源词判定席位（应改读 driver_mode）：%s", path, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// C-230 + 对照 C-229：同会话 push 卡被 @ 恰一次 Wake；pull 卡被 @ 零 Wake。
func TestB435PullSeatRoomHitZeroWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	pushCard, pullCard := createCoordCard(t, env), createCoordCard(t, env)
	b435BindPush(t, env, pushCard, "push-1")
	b435BindPush(t, env, pullCard, "pull-1")
	b435ForceMode(t, env, pullCard, "pull")
	sessionID := b435Session(t, env, pushCard, pullCard)
	drainAutomation(t, env)

	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "叫 pull 卡", []string{pullCard}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("pull 席位命中不得 keystone 唤醒: %v", got)
	}
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "叫 push 卡", []string{pushCard}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], pushCard) {
		t.Fatalf("push 席位命中应恰一次唤醒该卡: %v", got)
	}
}

// C-231 + 判定表第 5 行先于第 6 行：有驻场者时人类无 @ 零 Wake，根卡也不被唤醒；
// 对照：撤掉驻场者后同类消息唤醒根卡恰一次。
func TestB435ResidentAddressZeroWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	root := createCoordCard(t, env)
	b435BindPush(t, env, root, "root-1")
	sessionID := b435Session(t, env, root)
	b435ExecSQL(t, env, `INSERT INTO session_residents (session, identity, sat_at) VALUES (?, ?, ?)`,
		sessionID, "user:boss", time.Now().UTC().Format(time.RFC3339Nano))
	drainAutomation(t, env)

	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "无 @ 的人类发言", nil, 0)
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "@驻场者", []string{"user:boss"}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("驻场者地址不得 keystone 唤醒，根卡也不得被唤醒: %v", got)
	}
	b435ExecSQL(t, env, `DELETE FROM session_residents WHERE session = ?`, sessionID)
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "驻场者走了再说一句", nil, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], root) {
		t.Fatalf("无驻场者时人类无 @ 应默认唤醒根卡恰一次: %v", got)
	}
}

// C-232：房间消息唤醒不沿 parent 上走——@pull 子卡零 Wake，push 父卡不被唤醒；对照 @父卡恰一次。
func TestB435RoomWakeDoesNotWalkUp(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	parent := createCoordCard(t, env)
	b435BindPush(t, env, parent, "parent-1")
	child := splitCoordChild(t, env, parent)
	b435BindPull(t, env, child, "child-1")
	empty := splitCoordChild(t, env, parent)
	sessionID := b435Session(t, env, parent, child, empty)
	drainAutomation(t, env)

	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "@pull 子卡", []string{child}, 0)
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "@空座子卡", []string{empty}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("@子卡不得冒泡唤醒父卡: %v", got)
	}
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "@父卡", []string{parent}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], parent) {
		t.Fatalf("@父卡应恰一次唤醒父卡: %v", got)
	}
}

// C-235：卡自动化事件起始卡为 pull 席位 → 不起回合、不上走（push 父卡不被唤醒）。
func TestB435AutomationEventPullOriginNoRound(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	parent := createCoordCard(t, env)
	b435BindPush(t, env, parent, "parent-2")
	child := splitCoordChild(t, env, parent)
	b435BindPush(t, env, child, "child-2")
	b435ForceMode(t, env, child, "pull")
	drainAutomation(t, env)
	appendMirroredForConsumer(t, env.ledger, child, "child-task", "permission_request", 1, `{"ticket_id":"tk-pull"}`)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("pull 起始卡不得起回合、不得上走: %v", got)
	}
}

// C-236：空座子卡的自动化事件沿 parent 上走到 push 祖先。
func TestB435AutomationEventWalksToPushAncestor(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	parent := createCoordCard(t, env)
	b435BindPush(t, env, parent, "parent-3")
	child := splitCoordChild(t, env, parent)
	drainAutomation(t, env)
	appendMirroredForConsumer(t, env.ledger, child, "child-task", "permission_request", 1, `{"ticket_id":"tk-empty"}`)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], parent) {
		t.Fatalf("空座子卡事件应由 push 父卡接手恰一次: %v", got)
	}
}

// C-237：上走遇 pull 祖先跳过、继续上走到 push 祖先。
func TestB435AutomationEventSkipsPullAncestor(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	grand := createCoordCard(t, env)
	b435BindPush(t, env, grand, "grand-4")
	parent := splitCoordChild(t, env, grand)
	b435BindPush(t, env, parent, "parent-4")
	b435ForceMode(t, env, parent, "pull")
	child := splitCoordChild(t, env, parent)
	drainAutomation(t, env)
	appendMirroredForConsumer(t, env.ledger, child, "child-task", "permission_request", 1, `{"ticket_id":"tk-skip"}`)
	b435Consume(t, env)
	got := b435Resumes(runner)
	if len(got) != 1 || !b435WokeCard(got[0], grand) {
		t.Fatalf("应跳过 pull 父卡、由 push 祖父卡接手恰一次: %v", got)
	}
}

// C-319：queue_release 合成唤醒（无账本事件）对 pull 席位直接放行：零结果、无错误、零回合。
func TestB435QueueReleasePullPasses(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	card := createCoordCard(t, env)
	b435BindPush(t, env, card, "queue-5")
	b435ForceMode(t, env, card, "pull")
	result, err := env.srv.wakeCoordinatorRoundID(context.Background(), card,
		[]keystone.WakeEvent{{Kind: keystone.WakeQueueRelease, Card: card}}, nil, "")
	if err != nil || result.Woke {
		t.Fatalf("pull 席位 queue_release 应放行（零结果无错误），得 %+v %v", result, err)
	}
	if launches, resumes, _ := runner.snapshot(); launches != 0 || len(resumes) != 0 {
		t.Fatalf("pull 席位 queue_release 不得起回合: launches=%d resumes=%v", launches, resumes)
	}
}

// C-228 消费侧：匹配函数读账本失败 → 本轮报错、水位不越过该消息。
func TestB435RecipientsErrorHoldsWatermark(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	root := createCoordCard(t, env)
	b435BindPush(t, env, root, "root-6")
	sessionID := b435Session(t, env, root)
	drainAutomation(t, env)
	seq := sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "无 @ 人类发言", nil, 0)
	// 让默认收件人分支的驻场者读失败（表不存在 = 账本读错误，不是「无驻场者」）。
	b435ExecSQL(t, env, `DROP TABLE session_residents`)
	_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err == nil {
		t.Fatal("匹配函数读失败时消费轮必须报错")
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	_, seen := env.srv.automationSeen[seq]
	env.srv.automationMu.Unlock()
	if cursor >= seq || seen {
		t.Fatalf("读失败的消息不得被越过或标 seen：cursor=%d seq=%d seen=%v", cursor, seq, seen)
	}
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("读失败时不得唤醒: %v", got)
	}
}

// §5.1 生命周期：push 席位换绑为 pull 后同类消息零 Wake（对照：换绑前恰一次）。
func TestB435PushToPullRebindStopsWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	card := createCoordCard(t, env)
	old := b435BindPush(t, env, card, "push-7")
	sessionID := b435Session(t, env, card)
	drainAutomation(t, env)
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "换绑前", []string{card}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 {
		t.Fatalf("换绑前 @push 卡应恰一次: %v", got)
	}
	if err := env.ledger.RebindSeatMode(card, "cli:codex#self-7", proto.SeatSourceBind, proto.SeatModePull, old, ledger.SeatBearing{}); err != nil {
		t.Fatalf("换绑为 pull: %v", err)
	}
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "换绑后", []string{card}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 {
		t.Fatalf("换绑为 pull 后不得再唤醒: %v", got)
	}
}

// C-328 agentd 半边（Q4 预授权甲：与 cmd/b435_seat_mode_bidirectional_test.go 同一份 seed）：
// 会话内 push 卡 P 与 pull 卡 Q；消息 M1 @Q → Wake 0；消息 M2 @P → Wake 1。
// seed：两卡同一会话；P 坐 push（coordinate + 承载）、Q 坐 pull（card bind 同款）；
// 作者 user:tester；M1 正文「给 pull 席位」@Q；M2 正文「给 push 席位」@P。
func TestB435SeatModeBidirectionalWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	p, q := createCoordCard(t, env), createCoordCard(t, env)
	b435BindPush(t, env, p, "bidi-push")
	b435BindPull(t, env, q, "bidi-pull")
	sessionID := b435Session(t, env, p, q)
	drainAutomation(t, env)
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "给 pull 席位", []string{q}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("pull 席位命中：keystone Wake 应为 0，得 %v", got)
	}
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "给 push 席位", []string{p}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], p) {
		t.Fatalf("push 席位命中：keystone Wake 应为 1，得 %v", got)
	}
}

// C-324：agentd 拉起与 rebind --launch 写 push。
func TestB435CoordinatorLaunchAndRebindWritePush(t *testing.T) {
	env, runner := newNoPTYCoordEnv(t)
	seedCoordinatorSquad(t, env)
	card := createCoordCard(t, env)
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`); code != 200 {
		t.Fatalf("拉起失败：%d %s", code, body)
	}
	got, err := env.ledger.GetCard(card)
	if err != nil || got.DriverMode != string(proto.SeatModePush) {
		t.Fatalf("拉起后应写 push: %v %+v", err, got)
	}
	// 先把席位换成 pull，再 rebind --launch：必须写回 push。
	if err := env.ledger.RebindSeatMode(card, "cli:codex#self-8", proto.SeatSourceBind, proto.SeatModePull, got.DriverSession, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	runner.launchID = "sess-rebound-8"
	if code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card+"/coordinator/rebind", `{"mode":"launch"}`); code != 200 {
		t.Fatalf("rebind --launch 失败：%d %s", code, body)
	}
	got, err = env.ledger.GetCard(card)
	if err != nil || got.DriverMode != string(proto.SeatModePush) {
		t.Fatalf("rebind --launch 后应写 push: %v %+v", err, got)
	}
}

