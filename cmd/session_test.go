// session 命令族测试（B358.3 只落 wait 子命令）。
// 入口全部是 CLI 命令（spec 接缝清单的 CLI 调用方），数据穿真 SQLite 账本；
// 夹具用 collab.Service 直发消息（S5 落 session send 前没有 CLI 发言面）。
// 本文件 import internal/collab(/room) 仅存在于 _test.go：不构成生产跨域边。
package cmd

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// mustWaitFixture 开真账本、建会话并附成员，返回 (svc, facade, st, 会话id)。
// 发言身份用群主 user:tester（显式成员，Send 执法通过）。成员写入走 Facade
// 的 AddSessionMember（collab.Service 未包它，测试直调接口实现——测试文件
// import ledgerapi 的既有先例同款）。
func mustWaitFixture(t *testing.T, dir string) (*collab.Service, *ledgerapi.Facade, *ledger.Store, string) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("开夹具账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	svc := collab.New(facade)
	session, err := svc.CreateSession("通道对账场", "user:tester", "user:tester")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if err := facade.AddSessionMember(session.ID, "user:sy", "user:tester"); err != nil {
		t.Fatalf("加成员: %v", err)
	}
	return svc, facade, st, session.ID
}

func decodeSessionWake(t *testing.T, out string) proto.SessionWake {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout 必须恰一行 JSON，实得 %d 行: %q", len(lines), out)
	}
	var wake proto.SessionWake
	if err := json.Unmarshal([]byte(lines[0]), &wake); err != nil {
		t.Fatalf("SessionWake 解码: %v 原文=%s", err, lines[0])
	}
	return wake
}

func TestSessionWaitOutputsHitJSON(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	seq, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 看这条", Mentions: []string{"user:sy"}}, "user:tester")
	if err != nil {
		t.Fatalf("夹具发言: %v", err)
	}
	// --since 0 = 从流头扫（夹具事件在前，逐值推进游标零输出），首个命中即出。
	out, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0")
	if err != nil {
		t.Fatalf("session wait: %v", err)
	}
	wake := decodeSessionWake(t, out)
	if wake.Session != sessionID {
		t.Fatalf("session=%s want %s", wake.Session, sessionID)
	}
	if wake.Hit.Seq != seq || wake.Hit.Room != sessionID || wake.Hit.Actor != "user:tester" || wake.Hit.Body != "@user:sy 看这条" {
		t.Fatalf("hit 引用条漂移: %+v", wake.Hit)
	}
	if wake.Unread < 1 {
		t.Fatalf("unread=%d，应含命中条（≥1）", wake.Unread)
	}
	if wake.Referenced != nil {
		t.Fatalf("无引用锚时 referenced 必须省键: %+v", wake.Referenced)
	}
}

func TestSessionWaitReferencedAndUnread(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	m1, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "被引用的上下文"}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "user:sy 的到场发言"}, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 请看上文", Mentions: []string{"user:sy"}, ReplyTo: m1}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0")
	if err != nil {
		t.Fatalf("session wait: %v", err)
	}
	wake := decodeSessionWake(t, out)
	if wake.Referenced == nil {
		t.Fatalf("reply_to 有效时 referenced 不得省键: %s", out)
	}
	if wake.Referenced.Seq != m1 || wake.Referenced.Body != "被引用的上下文" || wake.Referenced.Actor != "user:tester" {
		t.Fatalf("referenced 引用条漂移: %+v", wake.Referenced)
	}
	if wake.Unread != 3 {
		t.Fatalf("unread=%d，want 3（与 ListSessions(member) 同一投影，含命中条）", wake.Unread)
	}
}

func TestSessionWaitDefaultSinceOnlyWaitsForNewEvents(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 启动前就有的消息", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	_, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--timeout", "300ms")
	codeErr := &exitCodeError{}
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("缺省 --since 应从流尾只等新事件，短超时内不得命中历史消息: err=%v", err)
	}
}

func TestSessionWaitMemberExactMatch(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@User:Sy 大小写不同", Mentions: []string{"User:Sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	// --since 0 必须显式给：缺省 --since = 流尾不扫历史消息，夹具消息不可观测，
	// 逐字相等（条 39）锁不住（plan 原夹具漏 --since，见台账偏差 2）。
	_, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0", "--timeout", "300ms")
	codeErr := &exitCodeError{}
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("member 与 target 必须逐字相等（条 39）: err=%v", err)
	}
}

func TestSessionWaitIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 只读核对", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	before, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0"); err != nil {
		t.Fatalf("session wait: %v", err)
	}
	after, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("订阅只读（条 45）：事件数 %d → %d", len(before), len(after))
	}
	if _, statErr := os.Stat(filepath.Join(dir, "room-cursors.json")); !os.IsNotExist(statErr) {
		t.Fatalf("订阅不得推进未读游标（条 45），cursor 文件 stat=%v", statErr)
	}
}

func TestSessionWaitInvalidFlags(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--timeout", "-1s"); err == nil {
		t.Fatal("负 --timeout 必须拒绝")
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "-5"); err == nil {
		t.Fatal("负 --since 必须拒绝")
	}
}

// TestSessionWaitSourceGuard 是 R1 通道的源码级守卫（契约条 42：命中判定
// 唯一入口是 collab.Service.MessageWakeTargets；通道内不得出现第二份寻址
// 判定，无成员集合形状）。B358.5 岔口 1 批准后收窄：kind 门控（RoomMsgUser）
// 与 mentions 直读（.Mentions）两类禁令从文件级子串改为 wait 通道两函数
//（runSessionWait/buildSessionWake）的 AST 级禁令——同文件续写的 session
// send 合法引用 proto.RoomMsgUser 构造发言、不在 wait 路径；广播帮手名仍是
// 文件级禁令。结构体字面量键（Mentions: …）不是 SelectorExpr，天然豁免。
func TestSessionWaitSourceGuard(t *testing.T) {
	raw, err := os.ReadFile("session.go")
	if err != nil {
		t.Fatalf("读 session.go 源: %v", err)
	}
	src := string(raw)
	for _, banned := range []string{
		"broadcastToMembers", "fanOutToMembers", "notifyAllMembers", "wakeAllMembers",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("session.go 出现禁令形状 %q——通道内不得重造寻址判定（条 42）", banned)
		}
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "session.go", src, 0)
	if err != nil {
		t.Fatalf("解析 session.go: %v", err)
	}
	waitFuncs := map[string]*ast.FuncDecl{}
	for _, name := range []string{"runSessionWait", "buildSessionWake"} {
		ast.Inspect(f, func(n ast.Node) bool {
			if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == name {
				waitFuncs[name] = d
			}
			return true
		})
		if waitFuncs[name] == nil {
			t.Fatalf("%s 不存在——通道本体缺失", name)
		}
	}
	callsAddressing := false
	for _, name := range []string{"runSessionWait", "buildSessionWake"} {
		ast.Inspect(waitFuncs[name], func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "MessageWakeTargets" {
					callsAddressing = true
				}
			case *ast.SelectorExpr:
				if node.Sel.Name == "RoomMsgUser" || node.Sel.Name == "Mentions" {
					t.Errorf("%s 出现 kind 门控/mentions 直读形状 %q——通道内不得重造寻址判定（条 42）", name, node.Sel.Name)
				}
			}
			return true
		})
	}
	if !callsAddressing {
		t.Fatalf("通道未经 MessageWakeTargets——命中判定唯一入口被绕过（条 42）")
	}
}

// —— B358.5 追加：session 六子命令缝级测试（create/list/detail/archive/join/
// leave/send）。入口全部是 CLI 命令（spec §6 缝 #1 的 CLI 调用方），数据穿真
// SQLite 账本；成员扩张用 facade.AddSessionMember（gateway 夹具 mustConsoleSession
// 同款先例——成员扩张无 CLI/HTTP 面，B358.4 拍板 2）。断言：§2.1/§2.3 逐命令
// stdout 形状、退出码契约（成功 0 / 用法与存在性错误非 0）、--json roundtrip
// 回冻结 DTO 键集（breakdown §3.6 缺陷族 6）。

// mustSendFixture 开真账本、建会话（owner=user:tester，群主即初始成员——契约
// 条 3）并把 CLI 审计 actor（ledgerActor()=cli:<user>@<host>）坐进显式成员：
// 人形态 session send 的书写者执法（0.2#3）要求它在 Members。返回
// (svc, facade, st, 会话id, actor)。
func mustSendFixture(t *testing.T, dir string, title string) (*collab.Service, *ledgerapi.Facade, *ledger.Store, string, string) {
	t.Helper()
	actor := ledgerActor()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("开夹具账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	svc := collab.New(facade)
	session, err := svc.CreateSession(title, "user:tester", "user:tester")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if err := facade.AddSessionMember(session.ID, actor, "user:tester"); err != nil {
		t.Fatalf("坐进 CLI actor: %v", err)
	}
	return svc, facade, st, session.ID, actor
}

// TestSessionCreateCommand 锁：create 200 路径 stdout=Session JSON 一行（id 形如
// session:<n>）+ 审计 actor 注入面不动（EvSessionCreated 的 actor=ledgerActor()，
// 不是 owner——两字段不混用，B358.4 拍板① 的 CLI 半边）+ owner 统一记法前缀
// 校验反例四发（机器位 web: / 裸名 / 空名 / 缺失）+ 空标题拒绝。
func TestSessionCreateCommand(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "session", "create", "需求对齐", "--owner", "user:tester")
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	var session proto.Session
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &session); err != nil {
		t.Fatalf("create stdout 应为 Session JSON 一行: %q err=%v", out, err)
	}
	if !strings.HasPrefix(session.ID, "session:") || session.Owner != "user:tester" || session.Title != "需求对齐" {
		t.Fatalf("create 投影漂移: %+v", session)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc(nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	created := false
	for _, ev := range events {
		if ev.Type != ledger.EvSessionCreated {
			continue
		}
		created = true
		if ev.Actor != ledgerActor() {
			t.Fatalf("建会话审计 actor 应注入面不动 %q，实得 %q", ledgerActor(), ev.Actor)
		}
	}
	if !created {
		t.Fatal("EvSessionCreated 应已落账")
	}
	for _, bad := range []struct{ name, owner string }{
		{"机器位 web:", "web:1"}, {"裸名", "mallory"}, {"空名", "user:"},
	} {
		if _, _, err := runLedgerCLI(t, dir, "session", "create", "x", "--owner", bad.owner); err == nil {
			t.Fatalf("owner %s 必须拒绝（统一记法前缀校验）", bad.name)
		}
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "create", "x"); err == nil {
		t.Fatal("缺 --owner 必须拒绝")
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "create", "  ", "--owner", "user:tester"); err == nil {
		t.Fatal("空标题必须拒绝")
	}
}

// TestSessionListColumnsAndUnread 锁：表头七列（§2.3 字面）+ 两行都在 +
// --member user:tester 下两条消息的会话 Unread=2、另一场 0 + --json 每行可
// Unmarshal 回 proto.SessionSummary（冻结 DTO roundtrip，缺陷族 6）且有消息行
// 键集恰 {id,kind,title,owner,archived,unread,needs_human,last_activity,preview,members}
//（cards 空 → omitempty 缺键：缺失≠零值分辨）。
func TestSessionListColumnsAndUnread(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, idA, _ := mustSendFixture(t, dir, "对账场A")
	_, _, _, idB, _ := mustSendFixture(t, dir, "对账场B")
	for _, body := range []string{"第一条", "第二条"} {
		if _, err := svc.Send(idA, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: body}, "user:tester"); err != nil {
			t.Fatalf("夹具发言 %q: %v", body, err)
		}
	}
	out, _, err := runLedgerCLI(t, dir, "session", "list", "--member", "user:tester")
	if err != nil {
		t.Fatalf("session list: %v", err)
	}
	for _, col := range []string{"ID", "标题", "群主", "归档", "未读", "需要你", "最近活动"} {
		if !strings.Contains(out, col) {
			t.Fatalf("表头缺列 %q: %q", col, out)
		}
	}
	unreadOf := func(line string) string {
		// 行式 = ID\t标题\t群主\t归档\t未读\t…（tabwriter 空格对齐；「需要你」空列
		// 被 Fields 折叠，但未读在第 5 列不受影响）。
		fields := strings.Fields(line)
		if len(fields) < 5 {
			t.Fatalf("行字段不足: %q", line)
		}
		return fields[4]
	}
	var lineA, lineB string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.Contains(line, idA) {
			lineA = line
		}
		if strings.Contains(line, idB) {
			lineB = line
		}
	}
	if lineA == "" || lineB == "" {
		t.Fatalf("两场会话都应在列表: %q", out)
	}
	if got := unreadOf(lineA); got != "2" {
		t.Fatalf("两条无 @ 消息 → 未读列应 2，实得 %q（行 %q）", got, lineA)
	}
	if got := unreadOf(lineB); got != "0" {
		t.Fatalf("无消息会话未读列应 0，实得 %q（行 %q）", got, lineB)
	}
	// --json：每行一条 SessionSummary，roundtrip 回冻结 DTO；有消息行键集字面。
	outJSON, _, err := runLedgerCLI(t, dir, "session", "list", "--json", "--member", "user:tester")
	if err != nil {
		t.Fatalf("session list --json: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(outJSON), "\n")
	if len(lines) != 2 {
		t.Fatalf("--json 应每会话一行恰两行: %q", outJSON)
	}
	for _, line := range lines {
		var summary proto.SessionSummary
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			t.Fatalf("--json 行应 Unmarshal 回 SessionSummary: %v 原文=%s", err, line)
		}
	}
	var withMsg map[string]json.RawMessage
	for _, line := range lines {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatal(err)
		}
		var idOnly struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(line), &idOnly)
		if idOnly.ID == idA {
			withMsg = probe
		}
	}
	wantKeys := 10
	if len(withMsg) != wantKeys {
		t.Fatalf("有消息行键数应 %d（含 preview/members，缺 cards）: %d %v", wantKeys, len(withMsg), withMsg)
	}
	for _, k := range []string{"id", "kind", "title", "owner", "archived", "unread", "needs_human", "last_activity", "preview", "members"} {
		if _, ok := withMsg[k]; !ok {
			t.Fatalf("有消息行缺键 %q", k)
		}
	}
	if _, ok := withMsg["cards"]; ok {
		t.Fatalf("无卡会话 cards 应缺键（omitempty）: %v", withMsg["cards"])
	}
}

// TestSessionDetailThreeSections 锁：三段段头（成员:/节点:/时间线:）逐列可读 +
// owner 行 human + 进群卡号在文 + timeline 含 created/card_joined + --json
// roundtrip 回 proto.SessionDetail（顶层键恰 {summary,timeline}——nodes 无
// task_mirrored 事件缺键）+ 不存在会话非 0 且 stderr 含会话 id（mapSessionError
// 丢 id，CLI 边界包回——0.2#4）。
func TestSessionDetailThreeSections(t *testing.T) {
	dir := t.TempDir()
	_, facade, _, sessionID, _ := mustSendFixture(t, dir, "详情三块场")
	card := mustAddCard(t, dir, "详情进群卡")
	if err := facade.JoinCardToSession(sessionID, card, "user:tester"); err != nil {
		t.Fatalf("夹具拉卡: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "detail", sessionID)
	if err != nil {
		t.Fatalf("session detail: %v", err)
	}
	for _, sec := range []string{"成员:", "节点:", "时间线:"} {
		if !strings.Contains(out, sec) {
			t.Fatalf("详情缺段 %q: %q", sec, out)
		}
	}
	if !strings.Contains(out, "user:tester") || !strings.Contains(out, "human") {
		t.Fatalf("成员块应含 owner 行（human）: %q", out)
	}
	if !strings.Contains(out, card) {
		t.Fatalf("进群卡号应在文（空座成员行/时间线行）: %q", out)
	}
	if !strings.Contains(out, "created") || !strings.Contains(out, "card_joined") {
		t.Fatalf("时间线应含 created 与 card_joined 行: %q", out)
	}
	outJSON, _, err := runLedgerCLI(t, dir, "session", "detail", sessionID, "--json")
	if err != nil {
		t.Fatalf("session detail --json: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(outJSON)), &raw); err != nil {
		t.Fatalf("--json 应为 SessionDetail JSON 一行: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("顶层键应恰 {summary,timeline}（nodes 缺键）: %v", raw)
	}
	if _, ok := raw["summary"]; !ok {
		t.Fatal("缺 summary 键")
	}
	if _, ok := raw["timeline"]; !ok {
		t.Fatal("缺 timeline 键")
	}
	var detail proto.SessionDetail
	if err := json.Unmarshal([]byte(strings.TrimSpace(outJSON)), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Summary.ID != sessionID || len(detail.Summary.Cards) != 1 || detail.Summary.Cards[0].CardID != card {
		t.Fatalf("详情投影漂移: %+v", detail.Summary)
	}
	kinds := map[string]bool{}
	for _, ev := range detail.Timeline {
		kinds[ev.Kind] = true
	}
	if !kinds["created"] || !kinds["card_joined"] {
		t.Fatalf("--json timeline 缺 created/card_joined: %v", kinds)
	}
	_, errb, err := runLedgerCLI(t, dir, "session", "detail", "session:999")
	if err == nil {
		t.Fatal("不存在会话 detail 必须非 0")
	}
	if !strings.Contains(errb, "session:999") {
		t.Fatalf("报错应含会话 id（可行动）: %q", errb)
	}
}

// TestSessionJoinLifecycle 锁：join 成功 {"ok":true} + 同会话重复 join 幂等 0
//（契约条 10）+ 已属他会话非 0 且含「已属会话」（ErrBadState 文案）+ 不存在卡/
// 会话非 0。
func TestSessionJoinLifecycle(t *testing.T) {
	dir := t.TempDir()
	_, _, _, sessionID, _ := mustSendFixture(t, dir, "拉卡场")
	_, _, _, otherID, _ := mustSendFixture(t, dir, "另一场")
	card := mustAddCard(t, dir, "进群卡")
	out, _, err := runLedgerCLI(t, dir, "session", "join", sessionID, card)
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session join: err=%v out=%q", err, out)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "join", sessionID, card); err != nil {
		t.Fatalf("同会话重复 join 应幂等 0（契约条 10）: %v", err)
	}
	_, errb, err := runLedgerCLI(t, dir, "session", "join", otherID, card)
	if err == nil {
		t.Fatal("已属他会话的卡必须非 0")
	}
	if !strings.Contains(errb, "已属会话") {
		t.Fatalf("报错应可行动（含「已属会话」）: %q", errb)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "join", sessionID, "B99999"); err == nil {
		t.Fatal("不存在卡必须非 0")
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "join", "session:999", card); err == nil {
		t.Fatal("不存在会话必须非 0")
	}
}

// TestSessionLeaveLifecycle 锁：leave 成功 + 详情 cards 清空 + 不在会话内重复
// leave 幂等 0（契约条 11）+ 不存在会话非 0。
func TestSessionLeaveLifecycle(t *testing.T) {
	dir := t.TempDir()
	_, _, _, sessionID, _ := mustSendFixture(t, dir, "移出场")
	card := mustAddCard(t, dir, "移出卡")
	if _, _, err := runLedgerCLI(t, dir, "session", "join", sessionID, card); err != nil {
		t.Fatalf("夹具 join: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "leave", sessionID, card)
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session leave: err=%v out=%q", err, out)
	}
	outJSON, _, err := runLedgerCLI(t, dir, "session", "detail", sessionID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var detail proto.SessionDetail
	if err := json.Unmarshal([]byte(strings.TrimSpace(outJSON)), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Summary.Cards) != 0 {
		t.Fatalf("移出后详情 cards 应空: %+v", detail.Summary.Cards)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "leave", sessionID, card); err != nil {
		t.Fatalf("不在会话内重复 leave 应幂等 0（契约条 11）: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "leave", "session:999", card); err == nil {
		t.Fatal("不存在会话 leave 必须非 0")
	}
}

// TestSessionSendNegativePaths 锁 send 全部负例：用法/执法负例一段（收口断言
// 零落账），归档负例一段（归档夹具本身落 EvSessionArchived，故零落账收口只
// 罩前一段——段序不能倒）。(a) 席位 flag 单只 → 用法错（缺陷族 4 成对反例，
// 两向）；(b) 空正文；(c) 自报席位不在书写者集 → ErrNotWriter（缺陷族 5 反例：
// 无冒充直通 flag，席位必须是会话内卡当前席位）；(d) 不存在会话 → 含 id；
// (e) 归档后 send 含「只读」、join 含「已归档」（哨兵差异：ErrReadOnly vs
// ErrBadState——breakdown §3.6 ③ 的 join 半边判据字面修订见岔口 4）。
func TestSessionSendNegativePaths(t *testing.T) {
	dir := t.TempDir()
	_, facade, st, sessionID, _ := mustSendFixture(t, dir, "发送负例场")
	card := mustAddCard(t, dir, "负例卡")
	clearSeatSourceEnv(t)
	before, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "x", "--cli", "grok"); err == nil {
		t.Fatal("只给 --cli 必须拒绝（成对 flag）")
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "x", "--session", "sid"); err == nil {
		t.Fatal("只给 --session 必须拒绝（成对 flag）")
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "   "); err == nil {
		t.Fatal("空正文必须拒绝")
	}
	_, errb, err := runLedgerCLI(t, dir, "session", "send", sessionID, "冒名", "--cli", "grok", "--session", "notseated")
	if err == nil {
		t.Fatal("自报席位不在书写者集必须非 0")
	}
	if !strings.Contains(errb, "书写者") {
		t.Fatalf("报错应含书写者语义（ErrNotWriter）: %q", errb)
	}
	_, errb, err = runLedgerCLI(t, dir, "session", "send", "session:999", "x")
	if err == nil {
		t.Fatal("不存在会话 send 必须非 0")
	}
	if !strings.Contains(errb, "session:999") {
		t.Fatalf("报错应含会话 id（可行动）: %q", errb)
	}
	// 用法/执法负例零落账收口（归档前的全部负例）。
	after, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("用法与执法负例全程零落账: before=%d after=%d", len(before), len(after))
	}
	// 归档负例段：归档夹具本身落 EvSessionArchived（不参与零落账收口）。
	if err := facade.ArchiveSession(sessionID, "user:tester"); err != nil {
		t.Fatalf("夹具归档: %v", err)
	}
	_, errb, err = runLedgerCLI(t, dir, "session", "send", sessionID, "归档后发言")
	if err == nil {
		t.Fatal("归档会话 send 必须非 0")
	}
	if !strings.Contains(errb, "只读") {
		t.Fatalf("报错应含只读语义（ErrReadOnly）: %q", errb)
	}
	_, errb, err = runLedgerCLI(t, dir, "session", "join", sessionID, card)
	if err == nil {
		t.Fatal("归档会话 join 必须非 0")
	}
	if !strings.Contains(errb, "已归档") {
		t.Fatalf("报错应可行动（含「已归档」，ErrBadState）: %q", errb)
	}
}

// TestSessionArchiveCommand 锁：archive 成功 {"ok":true} + 重复归档幂等 0
//（契约条 6，Store 级短路）+ 详情 archived=true + 不存在会话非 0 且含 id。
func TestSessionArchiveCommand(t *testing.T) {
	dir := t.TempDir()
	_, _, _, sessionID, _ := mustSendFixture(t, dir, "归档场")
	out, _, err := runLedgerCLI(t, dir, "session", "archive", sessionID)
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session archive: err=%v out=%q", err, out)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "archive", sessionID); err != nil {
		t.Fatalf("重复归档应幂等 0（契约条 6）: %v", err)
	}
	outJSON, _, err := runLedgerCLI(t, dir, "session", "detail", sessionID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var detail proto.SessionDetail
	if err := json.Unmarshal([]byte(strings.TrimSpace(outJSON)), &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.Summary.Archived {
		t.Fatalf("归档后 summary.archived 应 true: %+v", detail.Summary)
	}
	_, errb, err := runLedgerCLI(t, dir, "session", "archive", "session:999")
	if err == nil {
		t.Fatal("不存在会话 archive 必须非 0")
	}
	if !strings.Contains(errb, "session:999") {
		t.Fatalf("报错应含会话 id（可行动）: %q", errb)
	}
}
