// session 命令族测试（B358.3 只落 wait 子命令）。
// 入口全部是 CLI 命令（spec 接缝清单的 CLI 调用方），数据穿真 SQLite 账本；
// 夹具用 collab.Service 直发消息（S5 落 session send 前没有 CLI 发言面）。
// 本文件 import internal/collab(/room) 仅存在于 _test.go：不构成生产跨域边。
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/config"
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

func TestSessionWaitDefaultRecoversBacklogAndDoesNotRepeat(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	first, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 第一条", Mentions: []string{"user:sy"}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 第二条", Mentions: []string{"user:sy"}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--timeout", "300ms")
	if err != nil {
		t.Fatalf("积压应立即输出: %v", err)
	}
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &backlog); err != nil {
		t.Fatal(err)
	}
	if backlog.Type != "session_backlog" || backlog.Member != "user:sy" || backlog.ToSeq != second || len(backlog.Hits) != 2 || backlog.Hits[0].Hit.Seq != first {
		t.Fatalf("积压摘要错: %+v", backlog)
	}
	seq, err := st.SessionDeliveryCursor("user:sy")
	if err != nil || seq != second {
		t.Fatalf("交付水位=%d err=%v", seq, err)
	}
	_, _, err = runLedgerCLI(t, dir, "session", "wait", "user:sy", "--timeout", "300ms")
	codeErr := &exitCodeError{}
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("重挂无新消息应超时而非重复: %v", err)
	}
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 第三条", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	out, _, err = runLedgerCLI(t, dir, "session", "wait", "user:sy", "--timeout", "300ms")
	if err != nil {
		t.Fatalf("第三条应补收: %v", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &backlog); err != nil || len(backlog.Hits) != 1 || backlog.Hits[0].Hit.Seq <= second {
		t.Fatalf("续收错: %+v err=%v", backlog, err)
	}
}

type sessionFailWriter struct{}

func (sessionFailWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("stdout unavailable") }

func TestSessionWaitFailedOutputDoesNotAdvanceDelivery(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	mustWaitConfig(t, dir)
	c := &cobra.Command{Use: "wait"}
	c.SetOut(sessionFailWriter{})
	if err := runSessionWait(c, "user:sy", 0, false, 0, false); err == nil {
		t.Fatal("stdout 失败应返回错误")
	}
	seq, err := st.SessionDeliveryCursor("user:sy")
	if err != nil || seq != 0 {
		t.Fatalf("失败后水位=%d err=%v", seq, err)
	}
}

func TestSessionWaitExplicitSinceDoesNotAdvanceDelivery(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0"); err != nil {
		t.Fatal(err)
	}
	seq, err := st.SessionDeliveryCursor("user:sy")
	if err != nil || seq != 0 {
		t.Fatalf("手工回放不得推进水位: %d err=%v", seq, err)
	}
}

func TestSessionWaitFollowBacklogThenRealtime(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	first, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 积压", Mentions: []string{"user:sy"}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	mustWaitConfig(t, dir)
	oldInterval := sessionWaitPollInterval
	sessionWaitPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { sessionWaitPollInterval = oldInterval })
	var out syncBuffer
	c := &cobra.Command{Use: "wait"}
	c.SetOut(&out)
	c.SetContext(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runSessionWait(c, "user:sy", 0, false, 300*time.Millisecond, true) }()
	waitOutputLines(t, &out, 1, 5*time.Second)
	second, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 实时", Mentions: []string{"user:sy"}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	waitOutputLines(t, &out, 2, 5*time.Second)
	if err := <-errCh; err == nil {
		t.Fatal("空闲超时应退出")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(lines[0]), &backlog); err != nil || backlog.ToSeq != first {
		t.Fatalf("首行积压: %+v err=%v", backlog, err)
	}
	var wake proto.SessionWake
	if err := json.Unmarshal([]byte(lines[1]), &wake); err != nil || wake.Hit.Seq != second {
		t.Fatalf("次行实时: %+v err=%v", wake, err)
	}
	seq, err := st.SessionDeliveryCursor("user:sy")
	if err != nil || seq != second {
		t.Fatalf("实时后水位=%d err=%v", seq, err)
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
// （runSessionWait/sessionWaitMatch/buildSessionWake）的 AST 级禁令——同文件续写的 session
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
	// 委托链：入口 → 核心循环 → 候选页 → 唯一判定（B409 U5 分层）。
	chain := []struct {
		name      string
		callsNext string
	}{
		{"runSessionWait", "sessionWaitRun"},
		{"sessionWaitRun", "sessionWaitPage"},
		{"sessionWaitPage", "sessionWaitMatch"},
		{"sessionWaitMatch", ""},
		{"buildSessionWake", ""},
	}
	for _, link := range chain {
		name := link.name
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
	for i := range chain {
		link := &chain[i]
		ast.Inspect(waitFuncs[link.name], func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "MessageWakeTargets" {
					callsAddressing = true
				}
				if call, ok := node.Fun.(*ast.Ident); ok && link.callsNext != "" && call.Name == link.callsNext {
					link.callsNext = "" // 标记：委托边成立
				}
			case *ast.SelectorExpr:
				if node.Sel.Name == "RoomMsgUser" || node.Sel.Name == "Mentions" {
					t.Errorf("%s 出现 kind 门控/mentions 直读形状 %q——通道内不得重造寻址判定（条 42）", link.name, node.Sel.Name)
				}
			}
			return true
		})
	}
	if !callsAddressing {
		t.Fatalf("通道未经 MessageWakeTargets——命中判定唯一入口被绕过（条 42）")
	}
	for _, link := range chain {
		if link.callsNext != "" {
			t.Fatalf("%s 未委托 %s——通道分层被绕过（判定入口唯一性靠链路维持）", link.name, link.callsNext)
		}
	}
}

// —— B358.5 追加：session 六子命令缝级测试（create/list/detail/archive/join/
// leave/send）。入口全部是 CLI 命令（spec §6 缝 #1 的 CLI 调用方），数据穿真
// SQLite 账本；成员扩张用 facade.AddSessionMember（gateway 夹具 mustConsoleSession
// 同款先例——成员扩张无 CLI/HTTP 面，B358.4 拍板 2）。断言：§2.1/§2.3 逐命令
// stdout 形状、退出码契约（成功 0 / 用法与存在性错误非 0）、--json roundtrip
// 回冻结 DTO 键集（breakdown §3.6 缺陷族 6）。

func TestSessionMessageMentionsGoldens(t *testing.T) {
	got := sessionMessageMentions("@agent:main @user:sy @B233.16 @agent:main @agent: @B23x @unknown email@agent:main", []string{"@user:sy", "agent:main"})
	want := []string{"@user:sy", "agent:main", "B233.16"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("mentions=%v want %v", got, want)
	}
	// 显式输入只能在 Send 写入边界剥一次；@@ 不得变成有效寻址。
	got = sessionMessageMentions("", []string{"@@B1", "@@agent:main"})
	if fmt.Sprint(got) != fmt.Sprint([]string{"@@B1", "@@agent:main"}) {
		t.Fatalf("双 @ 提前归一化: %v", got)
	}
	got = sessionMessageMentions("@agent:a\u0085b", nil)
	if fmt.Sprint(got) != fmt.Sprint([]string{"agent:a"}) {
		t.Fatalf("Go 空白分隔漂移: %v", got)
	}
	// r2 金样（与桌面 sessionModel.ts#extractSessionMentions 同一语法）：
	// 尾随标点不修剪——"user:sy," 是合法名字（名字语法允许标点，作为字面
	// 寻址键原样落 mentions）；"B233.16。" 带尾随句号不是合法卡号，不寻址。
	got = sessionMessageMentions("@user:sy, 看这里 @B233.16。", nil)
	if fmt.Sprint(got) != fmt.Sprint([]string{"user:sy,"}) {
		t.Fatalf("尾随标点金样漂移: %v", got)
	}
	// r2 金样：大小写原样保留——大写前缀 User: 不是合法统一记法（不寻址）；
	// 名字本体的大小写是合法名字的一部分（agent:Main 照常寻址）。
	got = sessionMessageMentions("@User:Sy 抄送 @agent:Main", nil)
	if fmt.Sprint(got) != fmt.Sprint([]string{"agent:Main"}) {
		t.Fatalf("大小写金样漂移: %v", got)
	}
}

func TestSessionSendBodyMentionWakesTarget(t *testing.T) {
	dir := t.TempDir()
	_, _, st, sessionID, _ := mustSendFixture(t, dir, "正文提及场")
	before, err := st.MaxSeq()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "@agent:main 请处理"); err != nil {
		t.Fatal(err)
	}
	events, err := st.EventsFromAsc(nil, before, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("发言事件数=%d", len(events))
	}
	var msg proto.RoomMessage
	if err := json.Unmarshal(events[0].Payload, &msg); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(msg.Mentions) != "[agent:main]" {
		t.Fatalf("正文 @ 未落 mentions: %v", msg.Mentions)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "wait", "agent:main", "--since", fmt.Sprint(before))
	if err != nil {
		t.Fatal(err)
	}
	wake := decodeSessionWake(t, out)
	if wake.Hit.Seq != events[0].Seq {
		t.Fatalf("主 agent 未收到正文 @：%+v", wake)
	}
}

func TestSessionSendExplicitDoubleAtDoesNotWakeTarget(t *testing.T) {
	dir := t.TempDir()
	_, _, st, sessionID, _ := mustSendFixture(t, dir, "显式双前缀")
	before, err := st.MaxSeq()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "测试", "--mention", "@@agent:main"); err != nil {
		t.Fatal(err)
	}
	events, err := st.EventsFromAsc(nil, before, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("落账消息: %v err=%v", events, err)
	}
	var msg proto.RoomMessage
	if err := json.Unmarshal(events[0].Payload, &msg); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(msg.Mentions) != "[@agent:main]" {
		t.Fatalf("双前缀应只剥一次: %v", msg.Mentions)
	}
	_, _, err = runLedgerCLI(t, dir, "session", "wait", "agent:main", "--since", fmt.Sprint(before), "--timeout", "100ms")
	var timedOut *exitCodeError
	if !errors.As(err, &timedOut) || timedOut.code != ExitTimeout {
		t.Fatalf("双前缀不得唤醒主 agent: %v", err)
	}
}

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
// （cards 空 → omitempty 缺键：缺失≠零值分辨）。
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
// （契约条 10）+ 已属他会话非 0 且含「已属会话」（ErrBadState 文案）+ 不存在卡/
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
// （契约条 6，Store 级短路）+ 详情 archived=true + 不存在会话非 0 且含 id。
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

// —— B365：wait --follow 常驻形态 + send --reply-to 发送半边 ——

// syncBuffer 是 goroutine 直调 runSessionWait 时并发安全的输出收集器
// （test 二进制默认开 -race，bytes.Buffer 裸并发必炸）。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitOutputLines 轮询等待 stdout 出现 n 行（follow 形态的同步缝：首个命中行
// 出现即证明 runSessionWait 已进读循环——信号注册在循环之前，此后发 SIGTERM
// 不会落回默认处置杀死测试进程）。
func waitOutputLines(t *testing.T, out *syncBuffer, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		// 空串不是一行：先排空态再数行（Count("",...)+1 的 0+1 恒真陷阱）。
		if s := out.String(); s != "" && strings.Count(strings.TrimRight(s, "\n"), "\n")+1 >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 %d 行输出超时（within=%s）: %q", n, within, out.String())
}

// mustWaitConfig 让直调面（不经 runLedgerCLI 的 flag 树）拿到与 CLI 同款
// DataDir 配置；openRoomService 经 configPath 读它（先例
// TestOpenLedgerIgnoresRetiredEnabledFlag 的 configPath 直设）。
func mustWaitConfig(t *testing.T, dir string) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.yaml")
	c := &config.Config{
		Listen: "127.0.0.1:0", Token: "t", DataDir: dir, StallTimeout: 2 * time.Hour,
		Ledger: config.LedgerConfig{Enabled: true},
	}
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	configPath = cfgPath
}

// TestSessionWaitFollowOutputsEachHit 锁 follow 常驻形态的输出契约（spec §2.1）：
// 两条预置命中（--since 0 首轮轮询全见）各自输出一行 SessionWake——一次性原语
// 会在首个命中后退出，follow 必须两行都出；之后无新命中，--timeout 作为空闲
// 上限到点以 124 退出（非 0 退出不影响已输出行）。
func TestSessionWaitFollowOutputsEachHit(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	seq1, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 第一条", Mentions: []string{"user:sy"}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 第二条", Mentions: []string{"user:sy"}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--follow", "--since", "0", "--timeout", "300ms")
	codeErr := &exitCodeError{}
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("follow 空闲超时应 124: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("follow 应逐行输出两命中，实得 %d 行: %q", len(lines), out)
	}
	var seqs []int64
	for _, line := range lines {
		var wake proto.SessionWake
		if err := json.Unmarshal([]byte(line), &wake); err != nil {
			t.Fatalf("SessionWake 解码: %v 原文=%s", err, line)
		}
		if wake.Session != sessionID {
			t.Fatalf("session=%s want %s", wake.Session, sessionID)
		}
		seqs = append(seqs, wake.Hit.Seq)
	}
	if seqs[0] != seq1 || seqs[1] != seq2 {
		t.Fatalf("两行命中 seq 应升序 %d,%d，实得 %v", seq1, seq2, seqs)
	}
}

// TestSessionWaitFollowTimeoutIsIdleNotTotal 锁 follow 下 --timeout 的空闲语义
// （spec §2.1 字面：任意两命中帧之间的最大间隔）：第二条命中在「总时长」假想
// 死线之后才落账、但仍在第一条命中的空闲窗内——总时长语义（WithTimeout 整体
// 掐死）下第二条永不输出，空闲语义下两行全出、空闲窗重新计满后才 124。轮询
// 节奏压到 20ms（sessionWaitPollInterval var 测试缝），收尾还原。
func TestSessionWaitFollowTimeoutIsIdleNotTotal(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	mustWaitConfig(t, dir)
	oldInterval := sessionWaitPollInterval
	sessionWaitPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { sessionWaitPollInterval = oldInterval })
	send := func(body string) {
		t.Helper()
		if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: body, Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
			t.Fatal(err)
		}
	}
	var out syncBuffer
	c := &cobra.Command{Use: "wait"}
	c.SetOut(&out)
	c.SetContext(context.Background())
	start := time.Now()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runSessionWait(c, "user:sy", 0, true, 600*time.Millisecond, true)
	}()
	// 第一条在 400ms 落账、下个 20ms tick 即见（< 600ms 总时长死线）——两种
	// 语义下都输出，作为第二条的空闲窗锚点。
	time.Sleep(400 * time.Millisecond)
	send("@user:sy 第一条")
	waitOutputLines(t, &out, 1, 5*time.Second)
	// 第二条在 ~800ms 落账：> 600ms（总时长语义此刻已 124 收场），但距第一条
	// 命中帧 ~400ms < 600ms（空闲窗仍开着）——空闲语义必须输出它。
	time.Sleep(400 * time.Millisecond)
	send("@user:sy 第二条")
	waitOutputLines(t, &out, 2, 5*time.Second)
	select {
	case err := <-errCh:
		codeErr := &exitCodeError{}
		if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
			t.Fatalf("空闲上限到点应 124: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("空闲上限到点未退出")
	}
	if elapsed := time.Since(start); elapsed < 800*time.Millisecond {
		t.Fatalf("第二条命中在总时长死线后仍输出 ⇒ 空闲语义；实耗 %s 过短反而可疑", elapsed)
	}
}

// TestSessionWaitFollowGracefulExitOnSignal 锁主动退出 0（spec §2.1）：SIGINT
// 到达后 follow 订阅以 err=nil 收场（CLI 退出码 0 的同一路径）。同步缝：首个
// 命中行出现在 stdout 之后才发信号——signal.NotifyContext 的注册严格先于读循环，
// 此时注册必已生效（TDD 不可测的窗口已被同步点消除）。
func TestSessionWaitFollowGracefulExitOnSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("向自身进程投递信号仅 unix 有实现（GOOS=windows 仍须过 vet，TestWindowsVets 门）")
	}
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 信号前命中", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	mustWaitConfig(t, dir)
	var out syncBuffer
	c := &cobra.Command{Use: "wait"}
	c.SetOut(&out)
	c.SetContext(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runSessionWait(c, "user:sy", 0, true, 10*time.Second, true)
	}()
	waitOutputLines(t, &out, 1, 5*time.Second)
	// os.Interrupt 走跨平台 API（syscall.Kill 无 Windows 面， vet 门红线）；
	// runSessionWait 的 NotifyContext 同时注册 SIGINT/SIGTERM，同一路径。
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("定位自身进程: %v", err)
	}
	if err := self.Signal(os.Interrupt); err != nil {
		t.Fatalf("发 SIGINT: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("follow 收到 SIGINT 应主动退出 0（err=nil），实得 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT 后 follow 未退出")
	}
	if n := strings.Count(strings.TrimRight(out.String(), "\n"), "\n") + 1; n != 1 {
		t.Fatalf("退出前应恰输出一行命中，实得 %d 行: %q", n, out.String())
	}
}

// TestSessionSendReplyToFlag 锁 send 回复锚发送半边（spec §2.2）：--reply-to
// 透传 RoomMessage.ReplyTo 落账（payload 含 reply_to 键）；0 与缺省等价
// （omitempty 不落键）；负值用法错且零落账；端到端——被回复原作者的 wait 收到
// 带 referenced 的唤醒载荷（接收侧 ResolveDelivery 隐式寻址，冻结判定）。
func TestSessionSendReplyToFlag(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID, _ := mustSendFixture(t, dir, "回复锚场")
	target, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "被回复的上下文"}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	// 带锚发送（CLI actor 已由夹具坐进成员）。
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "带锚回复", "--reply-to", fmt.Sprintf("%d", target))
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session send --reply-to: err=%v out=%q", err, out)
	}
	// 0 与缺省等价 + 缺省。
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "零值锚", "--reply-to", "0"); err != nil {
		t.Fatalf("send --reply-to 0: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "缺省锚"); err != nil {
		t.Fatalf("send 缺省: %v", err)
	}
	// 负值用法错 + 零落账。
	before, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "负值", "--reply-to", "-1"); err == nil {
		t.Fatal("负 --reply-to 必须拒绝")
	}
	after, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("负值拒绝必须零落账: before=%d after=%d", len(before), len(after))
	}
	// 落账核对：带锚含键且值正确；无锚两类键不落（omitempty）。
	byBody := map[string]string{}
	for _, ev := range after {
		if ev.Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		byBody[msg.Body] = string(ev.Payload)
		if msg.Body == "带锚回复" && msg.ReplyTo != target {
			t.Fatalf("带锚落账 ReplyTo=%d, want %d", msg.ReplyTo, target)
		}
	}
	if raw, ok := byBody["带锚回复"]; !ok || !strings.Contains(raw, `"reply_to"`) {
		t.Fatalf("带锚消息 payload 应含 reply_to 键: %q ok=%v", raw, ok)
	}
	for _, absent := range []string{"零值锚", "缺省锚"} {
		raw, ok := byBody[absent]
		if !ok {
			t.Fatalf("消息 %q 未落账: %+v", absent, byBody)
		}
		if strings.Contains(raw, "reply_to") {
			t.Fatalf("无锚消息 %q payload 不应含 reply_to 键: %s", absent, raw)
		}
	}
	// 端到端：原作者 user:tester 的 wait 命中回复消息，referenced 指回被回复 seq。
	out, _, err = runLedgerCLI(t, dir, "session", "wait", "user:tester", "--since", "0")
	if err != nil {
		t.Fatalf("session wait 原作者: %v", err)
	}
	wake := decodeSessionWake(t, out)
	if wake.Hit.Body != "带锚回复" {
		t.Fatalf("原作者应被回复隐式寻址命中: %+v", wake.Hit)
	}
	if wake.Referenced == nil || wake.Referenced.Seq != target || wake.Referenced.Body != "被回复的上下文" {
		t.Fatalf("referenced 引用条漂移: %+v", wake.Referenced)
	}
}

// TestSessionSendAgentIdentity 锁 B358.9 接缝 #5：主 agent 以启动身份出示发言。
//
// 今天 `--cli/--session` 只产席位串 cli:<cli>#<session>，而写权是字符串等值——
// agent:<启动身份> 成员因此发不出话。新增 `--agent <启动身份>` 后：
//   - 落账 actor 恰为 agent:<启动身份>（不是席位串、不是 ledgerActor）；
//   - 该身份已在成员集（AddSessionMember 坐入）时发言成功；
//   - 三形态互斥（--agent 与 --cli/--session 并用拒绝）；
//   - 非法启动身份（含冒号/空白）fail-closed、零落账。
func TestSessionSendAgentIdentity(t *testing.T) {
	dir := t.TempDir()
	_, facade, st, sessionID, _ := mustSendFixture(t, dir, "主 agent 场")
	if err := facade.AddSessionMember(sessionID, "agent:opencode", "user:tester"); err != nil {
		t.Fatalf("坐入 agent:opencode: %v", err)
	}
	// 合法出示：落账 actor=agent:opencode，stdout {"ok":true,"seq":n}。
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "主 agent 自理", "--agent", "opencode")
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session send --agent: err=%v out=%q", err, out)
	}
	events, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Body == "主 agent 自理" {
			found = true
			if ev.Actor != "agent:opencode" {
				t.Fatalf("主 agent 发言 actor 应为 agent:opencode，实得 %q", ev.Actor)
			}
			if strings.HasPrefix(ev.Actor, "cli:") {
				t.Fatalf("主 agent 发言不得落成席位串: %q", ev.Actor)
			}
		}
	}
	if !found {
		t.Fatal("主 agent 消息未落账")
	}

	// 三形态互斥：--agent 与席位对并用拒绝。
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "x",
		"--agent", "opencode", "--cli", "grok", "--session", "s1"); err == nil {
		t.Fatal("--agent 与 --cli/--session 并用必须拒绝")
	}
	// 非法启动身份 fail-closed + 零落账。
	before, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"a:b", "a b"} {
		if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "x", "--agent", bad); err == nil {
			t.Fatalf("--agent %q 非法启动身份必须拒绝", bad)
		}
	}
	after, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("互斥/非法负例必须零落账: before=%d after=%d", len(before), len(after))
	}
}

// TestSessionMemberNotationSingleDefinition 锁 B358.9 契约 §5 H 组条 42（CLI 半边）：
// 统一记法规则只在 proto 一处定义，cmd 侧不得再内联 CutPrefix 前缀判定。
func TestSessionMemberNotationSingleDefinition(t *testing.T) {
	raw, err := os.ReadFile("session.go")
	if err != nil {
		t.Fatalf("读 session.go: %v", err)
	}
	src := string(raw)
	for _, banned := range []string{`CutPrefix(owner, "user:")`, `CutPrefix(identity, "user:")`,
		`CutPrefix(owner, "agent:")`, `CutPrefix(identity, "agent:")`} {
		if strings.Contains(src, banned) {
			t.Fatalf("cmd/session.go 仍内联统一记法规则 %q——应收敛到 proto.ValidateMemberIdentity", banned)
		}
	}
}

// TestSessionSendAgentEmptyFallsThrough 锁条 28：--agent 名为空串等价无 flag，
// 走 ledgerActor 默认形态（不冒充主 agent、不报错）。
func TestSessionSendAgentEmptyFallsThrough(t *testing.T) {
	dir := t.TempDir()
	_, _, _, sessionID, actor := mustSendFixture(t, dir, "空 agent 场")
	if _, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, "人话", "--agent", ""); err != nil {
		t.Fatalf("空 --agent 应等价无 flag: %v", err)
	}
	if actor != ledgerActor() {
		t.Fatalf("夹具 actor 漂移: %q", actor)
	}
}

// TestSessionSendActorResolution 单测 sessionSendActor 三形态决议（接缝 #5 的
// 可单测半边）：agent 优先于席位、席位优先于 ledgerActor，且非法 agent 名报错。
func TestSessionSendActorResolution(t *testing.T) {
	got, err := sessionSendActor("opencode", "", "", false)
	if err != nil || got != "agent:opencode" {
		t.Fatalf("agent 形态: got=%q err=%v", got, err)
	}
	got, err = sessionSendActor("", "grok", "s1", true)
	if err != nil || got != "cli:grok#s1" {
		t.Fatalf("席位形态: got=%q err=%v", got, err)
	}
	got, err = sessionSendActor("", "", "", false)
	if err != nil || got != ledgerActor() {
		t.Fatalf("人尺度形态: got=%q err=%v", got, err)
	}
	if _, err := sessionSendActor("bad:name", "", "", false); err == nil {
		t.Fatal("非法 agent 启动身份必须报错")
	}
}

// TestSessionSendMentionAtPrefixWakesSeat 锁 B391 修复的用户缝（spec §4.1）：
// `handoff session send <会话> --mention @<卡号>` 必须剥前缀、落账裸口径、
// 并唤醒该卡当前席位（`session wait <席位>` 收到命中）。
// 修复前红（存储 @B1、席位订阅收不到）；Task 1 实现后绿。
func TestSessionSendMentionAtPrefixWakesSeat(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	cardID := mustAddCard(t, dir, "B391 @前缀卡")
	_, facade, st, sessionID, _ := mustSendFixture(t, dir, "B391 @前缀场")
	if err := facade.JoinCardToSession(sessionID, cardID, "user:tester"); err != nil {
		t.Fatalf("拉卡进群: %v", err)
	}
	seat := "cli:claude#b391-seat"
	if err := st.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("配人: %v", err)
	}
	body := "@" + cardID + " 看这里"
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, body, "--mention", "@"+cardID)
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session send --mention @卡号: err=%v out=%q", err, out)
	}
	// 落账载荷为契约裸口径（True 序列化边界：CLI flag → payload → SQLite）。
	events, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Body != body {
			continue
		}
		found = true
		if len(msg.Mentions) != 1 || msg.Mentions[0] != cardID {
			t.Fatalf("落账 mentions 应剥 @ 前缀为 %q，实得 %q", cardID, msg.Mentions)
		}
	}
	if !found {
		t.Fatal("消息未落账")
	}
	// 端到端：席位订阅收到命中（@卡号 → 当前席位）。
	out, _, err = runLedgerCLI(t, dir, "session", "wait", seat, "--since", "0")
	if err != nil {
		t.Fatalf("session wait 席位: %v", err)
	}
	wake := decodeSessionWake(t, out)
	if wake.Session != sessionID || wake.Hit.Body != body {
		t.Fatalf("@卡号 未唤醒该卡当前席位: %+v", wake)
	}
}

// TestSessionSendMentionHelpMatchesAcceptedForms 锁 spec §4.4：--mention 帮助
// 措辞与真实接受的形态（可带 @、可裸写）一致。
func TestSessionSendMentionHelpMatchesAcceptedForms(t *testing.T) {
	flag := sessionSendCmd.Flags().Lookup("mention")
	if flag == nil {
		t.Fatal("找不到 --mention flag")
	}
	for _, want := range []string{"成员", "卡号", "@", "可选"} {
		if !strings.Contains(flag.Usage, want) {
			t.Fatalf("--mention 说明应含 %q，实得 %q", want, flag.Usage)
		}
	}
}

// —— B409 U5：有界候选读的席位一致性护栏与游标失败路径 ——
//
// 这些测试是有界候选读的回归护栏：席位换绑在确定性同步点注入（候选查询后、
// 最终判定前；以及版本读取后、候选查询前——后者用于构造 A→member→A 的 ABA，
// 使「只比较席位集合值」的实现逃过检测）。同步点由 session.go 的生产恒 nil
// 测试缝提供。

// mustSeatFixture 建卡并绑定到指定席位，返回卡号。
func mustSeatFixture(t *testing.T, st *ledger.Store, seat string) string {
	t.Helper()
	if _, err := st.PutWorkflow("charter", ledger.WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
		t.Fatal(err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "席位护栏卡", Project: "p", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeat(card.ID, seat, proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatalf("坐下 %s→%s: %v", card.ID, seat, err)
	}
	return card.ID
}

func resetWaitHooks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		sessionWaitBeforeQuery = nil
		sessionWaitBeforeJudge = nil
		sessionWaitBeforeAdvance = nil
	})
}

// ABA 护栏：卡席位 A→member→A 的两次翻转跨过候选查询与最终判定——页前后席位
// 集合值相等，只有单调 revision 能发现枚举与判定观察了不同状态。实现必须丢弃
// 该页并从同一 seq 下界重读，最终把 @卡号 消息恰好交付一次。
func TestSessionWaitABARebindDetectedAndRedelivered(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	const member = "cli:member#m1"
	const other = "cli:other#o1"
	cardID := mustSeatFixture(t, st, other)
	seq, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@" + cardID + " 到 member", Mentions: []string{cardID}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	resetWaitHooks(t)
	var queryFroms []int64
	queryCount, judgeCount := 0, 0
	sessionWaitBeforeQuery = func(from, to int64) {
		queryCount++
		queryFroms = append(queryFroms, from)
		if queryCount <= 2 {
			// 页 1/2 查询前：member 坐下（枚举在 member 状态下进行）。
			if err := st.RebindSeat(cardID, member, proto.SeatSourceBind, other, ledger.SeatBearing{}); err != nil {
				t.Errorf("ABA 换绑(member): %v", err)
			}
		}
	}
	sessionWaitBeforeJudge = func(from, to int64) {
		judgeCount++
		if judgeCount == 1 {
			// 页 1 判定前：回绑 other——ABA 第二次翻转，页前后集合值相等。
			if err := st.RebindSeat(cardID, other, proto.SeatSourceBind, member, ledger.SeatBearing{}); err != nil {
				t.Errorf("ABA 换绑(other): %v", err)
			}
		}
	}
	out, _, err := runLedgerCLI(t, dir, "session", "wait", member, "--timeout", "2s")
	if err != nil {
		t.Fatalf("ABA 后必须补收并交付: %v", err)
	}
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &backlog); err != nil {
		t.Fatalf("积压解码: %v 原文=%s", err, out)
	}
	if len(backlog.Hits) != 1 || backlog.Hits[0].Hit.Seq != seq {
		t.Fatalf("必须恰好交付一次且 seq 正确: %+v (want seq=%d)", backlog.Hits, seq)
	}
	if cursor, err := st.SessionDeliveryCursor(member); err != nil || cursor != seq {
		t.Fatalf("交付水位=%d err=%v want=%d", cursor, err, seq)
	}
	// 重读必须从同一未提交 seq 下界出发（契约第 13 条）。
	if queryCount < 3 {
		t.Fatalf("ABA 必须触发丢弃重读（query 次数=%d）", queryCount)
	}
	for i, from := range queryFroms {
		if from != queryFroms[0] {
			t.Fatalf("重读下界漂移 @%d：%v", i, queryFroms)
		}
	}
}

// 单向换绑护栏：页内席位单向变化同样必须丢弃重读，交付恰好一次。
func TestSessionWaitOneWayRebindStillDeliversOnce(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	const member = "cli:member#m1"
	const other = "cli:other#o1"
	cardID := mustSeatFixture(t, st, other)
	seq, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@" + cardID + " 单向", Mentions: []string{cardID}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	resetWaitHooks(t)
	queryCount := 0
	sessionWaitBeforeQuery = func(from, to int64) {
		queryCount++
		if queryCount == 1 {
			if err := st.RebindSeat(cardID, member, proto.SeatSourceBind, other, ledger.SeatBearing{}); err != nil {
				t.Errorf("单向换绑: %v", err)
			}
		}
	}
	out, _, err := runLedgerCLI(t, dir, "session", "wait", member, "--timeout", "2s")
	if err != nil {
		t.Fatalf("单向换绑后必须交付: %v", err)
	}
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &backlog); err != nil {
		t.Fatal(err)
	}
	if len(backlog.Hits) != 1 || backlog.Hits[0].Hit.Seq != seq {
		t.Fatalf("必须恰好交付一次: %+v", backlog.Hits)
	}
	if queryCount < 2 {
		t.Fatalf("换绑页必须被丢弃重读（query 次数=%d）", queryCount)
	}
}

// 空候选页也必须校验席位状态（契约第 7/11 条）：页查询时卡无席位，@卡号 寻址
// 落空——候选为空页；空页内（BeforeJudge 缝）把卡绑给监听 member，复读席位
// 版本必须发现变化：丢弃本页、从原下界重读，第二次查询该消息成为候选并交付。
// 夹具预置 @无席位卡号 的房间消息，使「空页跳过复读」的变异无法靠无输出蒙混
// ——没有这条消息时，空页复读与否行为全同，护栏咬不住（B409.5 review #1）。
// 版本无变化的空页确认后仍无输出、不推进水位（阶段一）。
func TestSessionWaitEmptyPageStillVerifiesSeat(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	const member = "cli:member#m1"
	cardID := mustSeatFixtureWithoutSeat(t, st)
	seq, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@" + cardID + " 空页换绑", Mentions: []string{cardID}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	resetWaitHooks(t)
	bindArmed := false
	judgeCount := 0
	var queryFroms []int64
	sessionWaitBeforeQuery = func(from, to int64) {
		queryFroms = append(queryFroms, from)
	}
	sessionWaitBeforeJudge = func(from, to int64) {
		judgeCount++
		if bindArmed {
			bindArmed = false
			if err := st.BindSeat(cardID, member, proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
				t.Errorf("空页坐下: %v", err)
			}
		}
	}

	// 阶段一：卡未绑定，候选为空页且版本无变化——空页确认后无输出、不推进水位。
	_, _, err = runLedgerCLI(t, dir, "session", "wait", member, "--timeout", "300ms")
	codeErr := &exitCodeError{}
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("空积压应超时而非输出: %v", err)
	}
	if judgeCount < 1 {
		t.Fatal("判定同步点未触发——空页护栏失效")
	}
	if cursor, err := st.SessionDeliveryCursor(member); err != nil || cursor != 0 {
		t.Fatalf("空页不得推进水位：%d err=%v", cursor, err)
	}

	// 阶段二（变异咬合点）：空页内把卡绑给监听 member。复读 revision 必须发现
	// 变化——丢弃空页、从原下界重读，第二次查询该消息成为候选并交付；
	// 「空页跳过复读」的变异在此不交付、超时 124 变红。
	bindArmed = true
	judgeCount = 0
	queryFroms = nil
	out, _, err := runLedgerCLI(t, dir, "session", "wait", member, "--timeout", "2s")
	if err != nil {
		t.Fatalf("空页复读 revision 后必须补收并交付: %v", err)
	}
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &backlog); err != nil {
		t.Fatalf("积压解码: %v 原文=%s", err, out)
	}
	if len(backlog.Hits) != 1 || backlog.Hits[0].Hit.Seq != seq {
		t.Fatalf("必须恰好交付一次且 seq 正确: %+v (want seq=%d)", backlog.Hits, seq)
	}
	if cursor, err := st.SessionDeliveryCursor(member); err != nil || cursor != seq {
		t.Fatalf("交付水位=%d err=%v want=%d", cursor, err, seq)
	}
	if judgeCount < 2 {
		t.Fatalf("空页必须触发复读重判（judge 次数=%d）", judgeCount)
	}
	// 重读必须从同一未提交 seq 下界出发（契约第 13 条）。
	for i, from := range queryFroms {
		if from != queryFroms[0] {
			t.Fatalf("重读下界漂移 @%d：%v", i, queryFroms)
		}
	}
}

// mustSeatFixtureWithoutSeat 建一张无席位卡（空页换绑护栏用）。
func mustSeatFixtureWithoutSeat(t *testing.T, st *ledger.Store) string {
	t.Helper()
	if _, err := st.PutWorkflow("charter", ledger.WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
		t.Fatal(err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "空页护栏卡", Project: "p", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return card.ID
}

// 游标写失败：stdout 已成功而游标写入失败必须可见报错，且允许下次重投
// （契约第 60/61 条）——第二次运行重新输出同一积压。
func TestSessionWaitCursorWriteFailureAllowsRepeat(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, sessionID := mustWaitFixture(t, dir)
	const member = "cli:member#m1"
	cardID := mustSeatFixture(t, st, member)
	seq, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@" + cardID + " 写失败重投", Mentions: []string{cardID}}, "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	resetWaitHooks(t)
	sessionWaitBeforeAdvance = func(m string, s int64) {
		// stdout 已写完、游标即将推进：此刻弄坏账本连接。
		if err := st.Close(); err != nil {
			t.Errorf("关闭夹具账本: %v", err)
		}
	}
	var out bytes.Buffer
	if err := sessionWaitRun(context.Background(), svc, st, &out, member, 0, false, 0, false); err == nil {
		t.Fatal("游标写失败必须报错")
	}
	if !strings.Contains(out.String(), "session_backlog") {
		t.Fatalf("stdout 应已成功输出积压: %q", out.String())
	}
	// 重开同一账本：水位未推进，允许重投。
	st2, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	if cursor, err := st2.SessionDeliveryCursor(member); err != nil || cursor != 0 {
		t.Fatalf("写失败后水位必须保持 0：%d err=%v", cursor, err)
	}
	svc2 := collab.New(ledgerapi.New(st2))
	var out2 bytes.Buffer
	if err := sessionWaitRun(context.Background(), svc2, st2, &out2, member, 0, false, 0, false); err != nil {
		t.Fatalf("重投: %v", err)
	}
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(strings.TrimSpace(out2.String())), &backlog); err != nil {
		t.Fatal(err)
	}
	if len(backlog.Hits) != 1 || backlog.Hits[0].Hit.Seq != seq {
		t.Fatalf("重投必须再次交付同一命中: %+v", backlog.Hits)
	}
	if cursor, err := st2.SessionDeliveryCursor(member); err != nil || cursor != seq {
		t.Fatalf("重投后水位=%d err=%v want=%d", cursor, err, seq)
	}
}

// 游标读错误不得当 0：共享水位读失败必须显式报错，不输出伪造的空 backlog
// （契约第 10/43 条的反向读面）。
func TestSessionWaitCursorReadErrorFailsExplicitly(t *testing.T) {
	dir := t.TempDir()
	svc, _, st, _ := mustWaitFixture(t, dir)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := sessionWaitRun(context.Background(), svc, st, &out, "user:sy", 0, false, 0, false)
	if err == nil || !strings.Contains(err.Error(), "交付水位") {
		t.Fatalf("游标读错必须显式失败: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("不得输出伪造 backlog: %q", out.String())
	}
}
