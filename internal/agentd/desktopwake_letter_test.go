// desktopwake_letter_test.go —— B434 G2：来信全段（契约 §3.6、条 74–82、89、191、263–275，
// r10 第 8/9/12 条）。
//
// 职责：从声明缝进——消费循环 consumeAutomationEventsOnce（缝 5 调用方，真寻址）或
// wakeDesktopSeat（缝 4，喂真实账本事件）——读回真实落盘的来信断言段落形状、围栏、名册、
// 尾行与读失败不写信。不直调 desktopLetter / renderDesktopLetter。
// 边界：失败段、跨机、needs_human 唤醒映射在 desktopwake_fail_test / desktopwake_remote_test /
// desktopwake_needs_test；夹具 newDesktopSliceEnv / bindDesktopSeat / openDecisionSeq /
// mdFilesUnder 来自 desktopwake_slice_test.go。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab"
	collabclient "github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// desktopLettersIn 读出 home 信箱里全部信件全文，按文件名升序。
func desktopLettersIn(t *testing.T, home string) []string {
	t.Helper()
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("读信箱 %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读信 %s: %v", name, err)
		}
		out = append(out, string(data))
	}
	return out
}

// onlyDesktopLetter 断言信箱恰一封信并返回其全文。
func onlyDesktopLetter(t *testing.T, home string) string {
	t.Helper()
	letters := desktopLettersIn(t, home)
	if len(letters) != 1 {
		t.Fatalf("信箱应恰一封信，实得 %d 封：%q", len(letters), letters)
	}
	return letters[0]
}

// desktopFenceOpen 匹配围栏开栏行，取 author / seq / kind。
var desktopFenceOpen = regexp.MustCompile("^```handoff-quote author=(\\S+) seq=(\\d+) kind=(\\S+)$")

// letterFenceOpens 依序返回来信里每个开栏行的 (author, seq, kind)。
func letterFenceOpens(letter string) [][3]string {
	var out [][3]string
	for _, line := range strings.Split(letter, "\n") {
		if m := desktopFenceOpen.FindStringSubmatch(line); m != nil {
			out = append(out, [3]string{m[1], m[2], m[3]})
		}
	}
	return out
}

// letterOutsideFences 返回删去全部围栏块后剩下的行。
func letterOutsideFences(t *testing.T, letter string) []string {
	t.Helper()
	var outside []string
	in := false
	for _, line := range strings.Split(letter, "\n") {
		switch {
		case !in && strings.HasPrefix(line, desktopQuoteFenceOpen+" "):
			in = true
		case in && line == desktopQuoteFenceClose:
			in = false
		case !in:
			outside = append(outside, line)
		}
	}
	if in {
		t.Fatalf("来信有未闭合的围栏块：\n%s", letter)
	}
	return outside
}

// desktopSessionFixture 建会话（群主 user:tester）并拉卡进群，返回会话 id 与 collab 服务。
func desktopSessionFixture(t *testing.T, env *ledgerEnv, cardID string) (string, *collab.Service) {
	t.Helper()
	svc := env.srv.rooms
	session, err := svc.CreateSession("桌面端来信场", "user:tester", "user:tester")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if err := svc.JoinCard(session.ID, cardID, "user:tester"); err != nil {
		t.Fatalf("拉卡进群: %v", err)
	}
	return session.ID, svc
}

// rawEventAt 从账本读回 seq 对应的原始事件（与消费循环同一读面）。
func rawEventAt(t *testing.T, env *ledgerEnv, seq int64) proto.LedgerEvent {
	t.Helper()
	evs, err := env.srv.autoLedger.EventsFromAsc(nil, seq-1, 1)
	if err != nil || len(evs) != 1 || evs[0].Seq != seq {
		t.Fatalf("读回 seq %d: evs=%v err=%v", seq, evs, err)
	}
	return evs[0]
}

// localDesk 读回卡上的桌面端记录（wakeDesktopSeat 直调用）。
func localDesk(t *testing.T, env *ledgerEnv, cardID string) ledger.SeatDesktop {
	t.Helper()
	desk, ok, err := env.ledger.SeatDesktopOf(cardID)
	if err != nil || !ok {
		t.Fatalf("读桌面端记录 %s: ok=%v err=%v", cardID, ok, err)
	}
	return desk
}

// TestB434DesktopLetterRoomMessageHit 锁故事 3 的来信（条 74–76、78、263、r10 第 8 条）：
// 群里 @卡 + reply_to 席位旧帖 → 消费循环恰写一封信：命中条 1000 字全文（含 ``` 已转义）
// 在 room_message 围栏、引用条独立围栏、未读数等于 ListSessions(席位) 的投影、尾行带
// 会话分句、名册 = 命中作者、引用作者。
func TestB434DesktopLetterRoomMessageHit(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := desktopSessionFixture(t, env, cardID)
	seat := bindDesktopSeat(t, env, cardID, "letter-cli", "local_letter")
	refSeq := sendSessionMessage(t, svc, sessionID, seat, "原帖：```旧```", nil, 0)
	drainAutomation(t, env)
	body := "请看 ```rm -rf /``` 之后" + strings.Repeat("长", 1000)
	hitSeq := sendSessionMessage(t, svc, sessionID, "user:tester", body, []string{cardID}, refSeq)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("@卡 应恰唤醒一次：processed=%d err=%v", processed, err)
	}
	letter := onlyDesktopLetter(t, home)
	lines := strings.Split(letter, "\n")
	if want := desktopAuthorRosterPrefix + "user:tester" + desktopAuthorRosterSep + seat; lines[1] != want {
		t.Fatalf("条 263：名册 = %q，want %q", lines[1], want)
	}
	heading := fmt.Sprintf("## 会话消息 seq %d（会话 %s）", hitSeq, sessionID)
	if !slices.Contains(lines, heading) {
		t.Fatalf("缺段标题 %q：\n%s", heading, letter)
	}
	opens := letterFenceOpens(letter)
	wantOpens := [][3]string{
		{"user:tester", fmt.Sprint(hitSeq), desktopQuoteKindRoomMessage},
		{seat, fmt.Sprint(refSeq), desktopQuoteKindRoomMessage},
	}
	if !slices.Equal(opens, wantOpens) {
		t.Fatalf("条 75/76：开栏行 = %v，want %v", opens, wantOpens)
	}
	if !strings.Contains(letter, escapeDesktopQuoteBody(body)+"\n```\n") {
		t.Fatalf("条 74：命中条 %d 字全文应原样（转义后）在围栏内，不截断：\n%s", len([]rune(body)), letter)
	}
	if !strings.Contains(letter, fmt.Sprintf("引用 seq %d：\n", refSeq)) ||
		!strings.Contains(letter, escapeDesktopQuoteBody("原帖：```旧```")+"\n```\n") {
		t.Fatalf("条 76：缺引用骨架或引用正文：\n%s", letter)
	}
	summaries, err := svc.ListSessions(seat)
	if err != nil {
		t.Fatal(err)
	}
	unread := -1
	for _, s := range summaries {
		if s.ID == sessionID {
			unread = s.Unread
		}
	}
	if want := fmt.Sprintf("会话 %s 里你还有 %d 条未读（含本条）。", sessionID, unread); unread < 1 || !slices.Contains(lines, want) {
		t.Fatalf("条 78：未读骨架应为 %q：\n%s", want, letter)
	}
	wantTail := fmt.Sprintf("按分诊表处置；读全文：handoff card show %s；会话消息：handoff room read %s。处置完不必挂订阅，有事会再叫醒你。", cardID, sessionID)
	if lines[len(lines)-2] != wantTail {
		t.Fatalf("r10 第 8 条：尾行 = %q，want %q", lines[len(lines)-2], wantTail)
	}
	if got := spy.calls(); len(got) != 1 {
		t.Fatalf("opener 应恰一次：%v", got)
	}
}

// TestB434DesktopLetterNoUsableReply 锁条 77：无 reply_to、或 reply_to 指向非会话消息（不可
// 解析）时无引用骨架、无引用围栏。
func TestB434DesktopLetterNoUsableReply(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replyTo func(env *ledgerEnv, cardID string) int64
	}{
		{"无reply_to", func(*ledgerEnv, string) int64 { return 0 }},
		{"reply_to指向裁决事件", func(env *ledgerEnv, cardID string) int64 { return openDecisionSeq(t, env, cardID, "非会话消息") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, _, home := newDesktopSliceEnv(t)
			cardID := createCoordCard(t, env)
			sessionID, svc := desktopSessionFixture(t, env, cardID)
			replyTo := tc.replyTo(env, cardID) // 先于坐下：空座期的裁决被排水消费，不进信箱
			drainAutomation(t, env)
			bindDesktopSeat(t, env, cardID, "noref-cli", "local_noref")
			drainAutomation(t, env)
			sendSessionMessage(t, svc, sessionID, "user:tester", "看一下", []string{cardID}, replyTo)
			if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
				t.Fatalf("消费: %v", err)
			}
			letter := onlyDesktopLetter(t, home)
			if strings.Contains(letter, "引用 seq") || len(letterFenceOpens(letter)) != 1 {
				t.Fatalf("条 77：不可解析的引用不得出骨架或围栏：\n%s", letter)
			}
		})
	}
}

// mixedDesktopLetter 在一张本机桌面端卡上落齐五种事件并经 wakeDesktopSeat（缝 4）写成一封信：
// 会话消息（命中 user:tester）、permission_request 与 question 工单（actor mirror）、completed
// 终态、裁决（cli:sycm@host）、needs_human（actor 带空格，验清洗）。返回卡号、信、各 seq 与 actor。
func mixedDesktopLetter(t *testing.T) (cardID, letter string, kinds map[int64]string, actors []string, bodies []string) {
	t.Helper()
	env, _, _, home := newDesktopSliceEnv(t)
	cardID = createCoordCard(t, env)
	sessionID, svc := desktopSessionFixture(t, env, cardID)
	seat := bindDesktopSeat(t, env, cardID, "mixed-cli", "local_mixed")
	roomBody := "群消息正文 ```x```"
	roomSeq := sendSessionMessage(t, svc, sessionID, "user:tester", roomBody, []string{cardID}, 0)
	permSeq := appendMirroredForConsumer(t, env.ledger, cardID, "perm", "permission_request", 1, `{"cmd":"rm -rf /tmp/x"}`)
	askSeq := appendMirroredForConsumer(t, env.ledger, cardID, "ask", "question", 1, `{"text":"要不要合？"}`)
	doneSeq := appendMirroredForConsumer(t, env.ledger, cardID, "done", "completed", 1, `{"text":"跑完了"}`)
	decSeq := openDecisionSeq(t, env, cardID, "先合 G1 还是 V？")
	if err := env.ledger.MarkNeedsHuman(cardID, "人来看一下 ```y```", "cli:ops person"); err != nil {
		t.Fatal(err)
	}
	needSeq := rawEventAt(t, env, mustMaxSeq(t, env)).Seq
	var raws []proto.LedgerEvent
	for _, seq := range []int64{roomSeq, permSeq, askSeq, doneSeq, decSeq, needSeq} {
		raws = append(raws, rawEventAt(t, env, seq))
	}
	if _, err := env.srv.wakeDesktopSeat(context.Background(), cardID, seat, localDesk(t, env, cardID),
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID}}, raws, "rid-mixed"); err != nil {
		t.Fatalf("wakeDesktopSeat: %v", err)
	}
	kinds = map[int64]string{
		roomSeq: desktopQuoteKindRoomMessage, permSeq: desktopQuoteKindTicket, askSeq: desktopQuoteKindTicket,
		doneSeq: desktopQuoteKindEvent, decSeq: desktopQuoteKindEvent, needSeq: desktopQuoteKindNeedsHuman,
	}
	actors = []string{"user:tester", "mirror", "cli:sycm@host", "cli:ops person", "cli:ops_person"}
	bodies = []string{roomBody, `{"cmd":"rm -rf /tmp/x"}`, `{"text":"要不要合？"}`, "跑完了", "先合 G1 还是 V？", "人来看一下 ```y```"}
	return cardID, onlyDesktopLetter(t, home), kinds, actors, bodies
}

// mustMaxSeq 返回账本当前最大 seq。
func mustMaxSeq(t *testing.T, env *ledgerEnv) int64 {
	t.Helper()
	seq, err := env.ledger.MaxSeq()
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// TestB434DesktopLetterKindByEventType 锁条 268（契约 r10 第 12 条命名测试）：围栏 kind 按
// 事件类型分派——room_message→room_message、permission_request/question→ticket、
// task 终态与 decision_*→event、needs_human→needs_human；工单与需要人段标题形状（条 79、191）。
func TestB434DesktopLetterKindByEventType(t *testing.T) {
	_, letter, kinds, _, _ := mixedDesktopLetter(t)
	got := map[int64]string{}
	for _, open := range letterFenceOpens(letter) {
		var seq int64
		fmt.Sscan(open[1], &seq)
		got[seq] = open[2]
	}
	for seq, want := range kinds {
		if got[seq] != want {
			t.Errorf("seq %d 的 kind = %q，want %q", seq, got[seq], want)
		}
	}
	if len(got) != len(kinds) {
		t.Errorf("开栏行 seq 集 = %v，want 恰 %d 个", got, len(kinds))
	}
	for _, re := range []string{`(?m)^## 工单 seq \d+（permission_request）$`, `(?m)^## 工单 seq \d+（question）$`,
		`(?m)^## 需要人 seq \d+$`, `(?m)^## task_mirrored seq \d+$`, `(?m)^## decision_opened seq \d+$`} {
		if !regexp.MustCompile(re).MatchString(letter) {
			t.Errorf("缺段标题 %s：\n%s", re, letter)
		}
	}
}

// TestB434DesktopLetterRosterMatchesQuotes 锁条 266（契约 r10 第 12 条命名测试）：名册身份集合
// 与全信围栏 author 集合双向相等，顺序 = 首次出现顺序，actor 中的空格被清洗为 "_"（条 272）。
func TestB434DesktopLetterRosterMatchesQuotes(t *testing.T) {
	_, letter, _, _, _ := mixedDesktopLetter(t)
	var quoteAuthors []string
	for _, open := range letterFenceOpens(letter) {
		if !slices.Contains(quoteAuthors, open[0]) {
			quoteAuthors = append(quoteAuthors, open[0])
		}
	}
	roster := strings.TrimPrefix(strings.Split(letter, "\n")[1], desktopAuthorRosterPrefix)
	if got := strings.Split(roster, desktopAuthorRosterSep); !slices.Equal(got, quoteAuthors) {
		t.Fatalf("名册 %v ≠ 围栏作者（首次出现序）%v", got, quoteAuthors)
	}
	if !slices.Contains(quoteAuthors, "cli:ops_person") || slices.Contains(quoteAuthors, "cli:ops person") {
		t.Fatalf("needs_human 打标者应按清洗值入册：%v", quoteAuthors)
	}
}

// TestB434DesktopLetterHeadingsHaveNoActor 锁条 275（契约 r10 第 12 条命名测试）与真实来信的
// 条 273/274：段标题行不含任何 actor（原值或清洗值）；删去围栏后不含任何第三方原文片段。
func TestB434DesktopLetterHeadingsHaveNoActor(t *testing.T) {
	_, letter, _, actors, bodies := mixedDesktopLetter(t)
	for _, line := range strings.Split(letter, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		// 身份形 actor（含 ":"）按子串查；"mirror" 这类裸词按词元查（"task_mirrored" 是事件类型，不是 actor）。
		tokens := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '（' || r == '）' })
		for _, actor := range actors {
			if (strings.Contains(actor, ":") && strings.Contains(line, actor)) || slices.Contains(tokens, actor) {
				t.Errorf("条 275：段标题 %q 含 actor %q", line, actor)
			}
		}
	}
	rest := strings.Join(letterOutsideFences(t, letter), "\n")
	for _, b := range bodies {
		for _, frag := range strings.Split(b, "\n") {
			if strings.TrimSpace(frag) != "" && strings.Contains(rest, frag) {
				t.Errorf("条 273：围栏外出现第三方原文片段 %q", frag)
			}
		}
	}
}

// TestB434DesktopLetterTailSessionClause 锁契约 r10 第 8 条：无会话消息时尾行省去整段
// 会话分句；两个会话的消息同信时按首次出现顺序各给一条命令，以「、」连接。
func TestB434DesktopLetterTailSessionClause(t *testing.T) {
	t.Run("无会话消息", func(t *testing.T) {
		env, _, _, home := newDesktopSliceEnv(t)
		cardID := createCoordCard(t, env)
		bindDesktopSeat(t, env, cardID, "tail-cli", "local_tail")
		openDecisionSeq(t, env, cardID, "只有裁决")
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(onlyDesktopLetter(t, home), "\n")
		want := fmt.Sprintf("按分诊表处置；读全文：handoff card show %s。处置完不必挂订阅，有事会再叫醒你。", cardID)
		if lines[len(lines)-2] != want {
			t.Fatalf("尾行 = %q，want %q", lines[len(lines)-2], want)
		}
	})
	t.Run("两个会话", func(t *testing.T) {
		env, _, _, home := newDesktopSliceEnv(t)
		cardID := createCoordCard(t, env)
		s1, svc := desktopSessionFixture(t, env, cardID)
		other, err := svc.CreateSession("另一场", "user:tester", "user:tester")
		if err != nil {
			t.Fatal(err)
		}
		seat := bindDesktopSeat(t, env, cardID, "tail2-cli", "local_tail2")
		seqB := sendSessionMessage(t, svc, other.ID, "user:tester", "另一场的话", nil, 0)
		seqA := sendSessionMessage(t, svc, s1, "user:tester", "本场的话", nil, 0)
		raws := []proto.LedgerEvent{rawEventAt(t, env, seqB), rawEventAt(t, env, seqA)}
		if _, err := env.srv.wakeDesktopSeat(context.Background(), cardID, seat, localDesk(t, env, cardID),
			nil, raws, "rid-tail"); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(onlyDesktopLetter(t, home), "\n")
		want := fmt.Sprintf("按分诊表处置；读全文：handoff card show %s；会话消息：handoff room read %s、handoff room read %s。处置完不必挂订阅，有事会再叫醒你。", cardID, other.ID, s1)
		if lines[len(lines)-2] != want {
			t.Fatalf("尾行 = %q，want %q", lines[len(lines)-2], want)
		}
	})
}

// TestB434DesktopLetterBubbledHeading 锁契约 r10 第 9 条：子卡事件冒泡到桌面端父卡时，段标题
// 追加「（卡 <子卡号>）」；首行仍是父卡。
func TestB434DesktopLetterBubbledHeading(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	parentID := createCoordCard(t, env)
	bindDesktopSeat(t, env, parentID, "bubble-cli", "local_bubble")
	childID := splitCoordChild(t, env, parentID)
	seq := openDecisionSeq(t, env, childID, "子卡裁决")
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	letter := onlyDesktopLetter(t, home)
	lines := strings.Split(letter, "\n")
	if !strings.Contains(lines[0], "卡 "+parentID+" 有") {
		t.Fatalf("首行应为父卡 %s：%q", parentID, lines[0])
	}
	want := fmt.Sprintf("## decision_opened seq %d（卡 %s）", seq, childID)
	if !slices.Contains(lines, want) {
		t.Fatalf("缺冒泡段标题 %q：\n%s", want, letter)
	}
}

// failingRoomsLedger 包住真实 collab 账本客户端，按开关让引用读（EventsFromAsc）或未读投影
// （ListSessions）失败——条 80 的读失败注入；账本层无可替换接口，collab.New 接受该接口。
type failingRoomsLedger struct {
	collabclient.LedgerClient
	failEvents, failSessions bool
}

func (f failingRoomsLedger) EventsFromAsc(ids []string, from int64, limit int) ([]proto.LedgerEvent, error) {
	if f.failEvents {
		return nil, errors.New("注入：读引用事件失败")
	}
	return f.LedgerClient.EventsFromAsc(ids, from, limit)
}

func (f failingRoomsLedger) ListSessions() ([]proto.Session, error) {
	if f.failSessions {
		return nil, errors.New("注入：列会话失败")
	}
	return f.LedgerClient.ListSessions()
}

// TestB434DesktopLetterReadFailureWritesNothing 锁条 80：读引用或未读投影失败 → 本批不写信、
// 零 opener，wakeDesktopSeat 返回「未投递」。
func TestB434DesktopLetterReadFailureWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		lc   func(collabclient.LedgerClient) failingRoomsLedger
	}{
		{"引用读失败", func(lc collabclient.LedgerClient) failingRoomsLedger {
			return failingRoomsLedger{LedgerClient: lc, failEvents: true}
		}},
		{"未读投影失败", func(lc collabclient.LedgerClient) failingRoomsLedger {
			return failingRoomsLedger{LedgerClient: lc, failSessions: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, spy, home := newDesktopSliceEnv(t)
			cardID := createCoordCard(t, env)
			sessionID, svc := desktopSessionFixture(t, env, cardID)
			seat := bindDesktopSeat(t, env, cardID, "fail-cli", "local_fail")
			refSeq := sendSessionMessage(t, svc, sessionID, seat, "原帖", nil, 0)
			hitSeq := sendSessionMessage(t, svc, sessionID, "user:tester", "回你", []string{cardID}, refSeq)
			raws := []proto.LedgerEvent{rawEventAt(t, env, hitSeq)}
			env.srv.SetRooms(collab.New(tc.lc(env.srv.autoLedger)))
			_, err := env.srv.wakeDesktopSeat(context.Background(), cardID, seat, localDesk(t, env, cardID), nil, raws, "rid-fail")
			if !desktopUndelivered(err) {
				t.Fatalf("读失败应返回未投递，实得 %v", err)
			}
			if files := mdFilesUnder(t, home); len(files) != 0 || len(spy.calls()) != 0 {
				t.Fatalf("条 80：读失败不得写信/开 opener：files=%v opener=%v", files, spy.calls())
			}
		})
	}
}

// TestB434DesktopLetterNoLedgerSideEffects 锁条 81/82：一轮会话消息唤醒写信前后，账本最大 seq、
// 席位的 session 交付游标与会话未读数都不变。
func TestB434DesktopLetterNoLedgerSideEffects(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := desktopSessionFixture(t, env, cardID)
	seat := bindDesktopSeat(t, env, cardID, "side-cli", "local_side")
	drainAutomation(t, env)
	sendSessionMessage(t, svc, sessionID, "user:tester", "无副作用", []string{cardID}, 0)
	unreadOf := func() int {
		summaries, err := svc.ListSessions(seat)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range summaries {
			if s.ID == sessionID {
				return s.Unread
			}
		}
		return -1
	}
	beforeSeq, beforeUnread := mustMaxSeq(t, env), unreadOf()
	beforeCursor, err := env.ledger.SessionDeliveryCursor(seat)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	onlyDesktopLetter(t, home)
	afterCursor, err := env.ledger.SessionDeliveryCursor(seat)
	if err != nil {
		t.Fatal(err)
	}
	if after := mustMaxSeq(t, env); after != beforeSeq {
		t.Fatalf("条 81：写信不得落账本事件：max seq %d → %d", beforeSeq, after)
	}
	if afterCursor != beforeCursor {
		t.Fatalf("条 82：写信不得推进 session 交付游标：%d → %d", beforeCursor, afterCursor)
	}
	if after := unreadOf(); after != beforeUnread {
		t.Fatalf("写信不得改未读：%d → %d", beforeUnread, after)
	}
}

// TestB434DesktopLetterOneLetterPerBatch 锁条 89（多事件批）：同一轮一张卡上的三件事
// （两个裁决 + 一条 @）恰写一封信，首行计数为 3，含每个 seq（条 73）。
func TestB434DesktopLetterOneLetterPerBatch(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := desktopSessionFixture(t, env, cardID)
	bindDesktopSeat(t, env, cardID, "batch-cli", "local_batch")
	drainAutomation(t, env)
	seqs := []int64{
		openDecisionSeq(t, env, cardID, "第一问"),
		openDecisionSeq(t, env, cardID, "第二问"),
		sendSessionMessage(t, svc, sessionID, "user:tester", "也看看", []string{cardID}, 0),
	}
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	letter := onlyDesktopLetter(t, home)
	if !strings.Contains(strings.Split(letter, "\n")[0], "有 3 件新事") {
		t.Fatalf("首行计数应为 3：%q", strings.Split(letter, "\n")[0])
	}
	for _, seq := range seqs {
		if !strings.Contains(letter, fmt.Sprintf(" seq=%d kind=", seq)) {
			t.Errorf("条 73：信里缺 seq %d", seq)
		}
	}
	if got := spy.calls(); len(got) != 1 {
		t.Fatalf("条 90：一批恰一次 opener：%v", got)
	}
}

// TestB434DesktopSelfMentionWakesOwnSeat 锁现状（拆解稿 §3.5「自唤醒现状锁定」，真机 #18 观察）：
// 席位身份在群里 @ 自己的卡 → 写一封以自己为作者的信。要改成排除作者属 spec 外，另开卡。
func TestB434DesktopSelfMentionWakesOwnSeat(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := desktopSessionFixture(t, env, cardID)
	seat := bindDesktopSeat(t, env, cardID, "self-cli", "local_self")
	drainAutomation(t, env)
	sendSessionMessage(t, svc, sessionID, seat, "我 @ 自己", []string{cardID}, 0)
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if roster := strings.Split(onlyDesktopLetter(t, home), "\n")[1]; roster != desktopAuthorRosterPrefix+seat {
		t.Fatalf("自 @ 现状：应写一封作者为席位自身的信，名册 = %q", roster)
	}
}
