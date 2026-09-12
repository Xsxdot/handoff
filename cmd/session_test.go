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
// 判定，无成员集合形状）。形态照抄 TestB3583WakePathSourceGuard。
func TestSessionWaitSourceGuard(t *testing.T) {
	raw, err := os.ReadFile("session.go")
	if err != nil {
		t.Fatalf("读 session.go 源: %v", err)
	}
	src := string(raw)
	for _, banned := range []string{
		"proto.RoomMsgUser", ".Mentions",
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
	var runWait *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "runSessionWait" {
			runWait = d
		}
		return true
	})
	if runWait == nil {
		t.Fatalf("runSessionWait 不存在——通道本体缺失")
	}
	callsAddressing := false
	ast.Inspect(runWait, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "MessageWakeTargets" {
				callsAddressing = true
			}
		}
		return true
	})
	if !callsAddressing {
		t.Fatalf("通道未经 MessageWakeTargets——命中判定唯一入口被绕过（条 42）")
	}
}
