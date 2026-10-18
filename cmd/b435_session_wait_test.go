// b435_session_wait_test.go —— B435 R：`session wait <session>` 作为会话驻场者收件（契约 §4.2 C-131–C-144、
// C-145（session 半边）/C-146、C-241；R plan Q10 乙）。
//
// 夹具：newB435WaitEnv（真 SQLite 账本 + 真 collab 匹配函数 + 真 cobra 入口）；驻场者经
// collab.Service.SitResident（`session sit` 同一入口）登记。
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

func (e *b435WaitEnv) sit(t *testing.T, identity string) proto.DeliveryAddress {
	t.Helper()
	if _, err := e.svc.SitResident(proto.SessionResident{Session: e.session, Identity: identity}); err != nil {
		t.Fatalf("sit %s: %v", identity, err)
	}
	return proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: e.session, Identity: identity}
}

func (e *b435WaitEnv) sendAs(t *testing.T, actor string, msg proto.RoomMessage) int64 {
	t.Helper()
	seq, err := e.svc.Send(e.session, msg, actor)
	if err != nil {
		t.Fatalf("%s 发言: %v", actor, err)
	}
	return seq
}

// C-131/C-132：旧 `session wait <member>` 形态（参数不是会话房间号）非 0 ≠124 且给形状提示；
// 对照：形如会话号但无驻场者走 C-133 分支（stderr 不同）。
func TestB435SessionWaitRejectsLegacyMember(t *testing.T) {
	e := newB435WaitEnv(t)
	for _, arg := range []string{"user:x", "cli:a#b", "agent:y", "web:h", "B12"} {
		_, stderr, err := runLedgerCLI(t, e.dir, "session", "wait", arg, "--timeout", "5s")
		b435NonZeroNot124(t, err, "session wait "+arg)
		if !strings.Contains(stderr, "形如 session:<n>") {
			t.Fatalf("旧形态 %q 应给会话号形状提示，stderr:\n%s", arg, stderr)
		}
		if strings.Contains(stderr, "handoff session sit") {
			t.Fatalf("旧形态 %q 不应落到无驻场者分支，stderr:\n%s", arg, stderr)
		}
	}
	_, stderr, err := runLedgerCLI(t, e.dir, "session", "wait", e.session, "--timeout", "5s")
	b435NonZeroNot124(t, err, "无驻场者的 session wait（对照）")
	if strings.Contains(stderr, "形如 session:<n>") || !strings.Contains(stderr, "handoff session sit") {
		t.Fatalf("会话号形态应走 C-133 分支，stderr:\n%s", stderr)
	}
}

// C-133：无驻场者立即非 0 ≠124、提示 session sit、不阻塞（长超时下用时有上界）。
func TestB435SessionWaitNoResidentFailsFast(t *testing.T) {
	e := newB435WaitEnv(t)
	for _, extra := range [][]string{{"--timeout", "10s"}, {"--since", "0", "--timeout", "10s"}, {"--follow", "--timeout", "10s"}} {
		started := time.Now()
		args := append([]string{"session", "wait", e.session}, extra...)
		_, stderr, err := runLedgerCLI(t, e.dir, args...)
		elapsed := time.Since(started)
		b435NonZeroNot124(t, err, fmt.Sprint("无驻场者 ", extra))
		if elapsed >= 2*time.Second {
			t.Fatalf("无驻场者应立即退出，用时 %s（%v）", elapsed, extra)
		}
		if !strings.Contains(stderr, "handoff session sit "+e.session) {
			t.Fatalf("stderr 应提示 handoff session sit，得:\n%s", stderr)
		}
	}
}

// S3/S4b 机内：一根一子两卡均 pull 席位 + 驻场者。人类无 @ → 驻场者（根卡不收）；@子卡 → 子卡
// （驻场者不收）；agent: 无 @ → 谁都不收；reply_to 驻场者 → 驻场者收。
func TestB435SessionWaitResidentDefaultHit(t *testing.T) {
	e := newB435WaitEnv(t)
	root := e.card(t)
	e.pull(t, root, "root")
	child, err := e.st.SplitCard(root, "子卡", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.JoinCard(e.session, child.ID, "user:tester"); err != nil {
		t.Fatal(err)
	}
	e.pull(t, child.ID, "child")
	if err := ledgerapi.New(e.st).AddSessionMember(e.session, "agent:x", "user:tester"); err != nil {
		t.Fatal(err)
	}
	e.sit(t, "user:r")
	m1 := e.send(t, "大家好")                         // 人类无 @ → 驻场者
	m2 := e.send(t, "@子卡", child.ID)                // @子卡 → 子卡
	e.sendAs(t, "agent:x", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "机器人无 @"}) // → 无人
	m4 := e.sendAs(t, "user:r", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@根卡", Mentions: []string{root}})
	m5 := e.sendAs(t, "user:tester", proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "回驻场者", ReplyTo: m4})

	hit := func(since int64) proto.SessionWake {
		t.Helper()
		out, _, err := runLedgerCLI(t, e.dir, "session", "wait", e.session, "--since", fmt.Sprint(since), "--timeout", "5s")
		if err != nil {
			t.Fatalf("session wait --since %d: %v", since, err)
		}
		return decodeSessionWake(t, out)
	}
	if w := hit(m1 - 1); w.Hit.Seq != m1 || w.Session != e.session {
		t.Fatalf("人类无 @ 应交付驻场者 seq=%d，得 %+v", m1, w)
	}
	// m1 之后第一条命中是 m5：m2（@子卡）、agent 无 @、m4（@根卡）都不命中驻场者。
	if w := hit(m1); w.Hit.Seq != m5 {
		t.Fatalf("m1 之后驻场者首个命中应是 reply_to 驻场者的 m5=%d，得 %+v", m5, w)
	}
	cardFirst := func(card string, since int64) []int64 {
		t.Helper()
		out, _, err := runLedgerCLI(t, e.dir, "card", "wait", card, "--since", fmt.Sprint(since), "--timeout", "500ms")
		if err != nil && b435ExitCode(err) != ExitTimeout {
			t.Fatalf("card wait %s: %v", card, err)
		}
		return b435Seqs(t, b435RoomLines(t, out))
	}
	// 同一 m1 不进根卡（默认收件人已改到驻场者，C-219 先于 C-220）；根卡首个房间命中是 m4。
	if got := cardFirst(root, m1-1); len(got) != 1 || got[0] != m4 {
		t.Fatalf("根卡首个房间命中应是 m4=%d（m1 归驻场者），得 %v", m4, got)
	}
	if got := cardFirst(child.ID, m1); len(got) != 1 || got[0] != m2 {
		t.Fatalf("子卡应收 @子卡 m2=%d，得 %v", m2, got)
	}
	if got := cardFirst(child.ID, m2); len(got) != 0 {
		t.Fatalf("m2 之后子卡不应再收（agent 无 @ / @根卡 / 回驻场者），得 %v", got)
	}
}

// C-135/C-136：交付后持久游标行以驻场者地址键 resident:<session>/<identity> 写入，裸身份零新行。
func TestB435SessionWaitCursorKeyIsAddress(t *testing.T) {
	e := newB435WaitEnv(t)
	addr := e.sit(t, "user:r")
	seq := e.send(t, "@user:r 看这条", "user:r")
	if _, _, err := runLedgerCLI(t, e.dir, "session", "wait", e.session, "--timeout", "5s"); err != nil {
		t.Fatalf("session wait: %v", err)
	}
	key := proto.DeliveryCursorKey(addr)
	if key != "resident:"+e.session+"/user:r" {
		t.Fatalf("地址游标键编码漂移: %q", key)
	}
	if got, ok := e.cursor(t, key); !ok || got != seq {
		t.Fatalf("地址游标行应为 %d，得 %d ok=%v", seq, got, ok)
	}
	if _, ok := e.cursor(t, "user:r"); ok {
		t.Fatal("不得以裸身份 user:r 写游标行（C-136）")
	}
}

// C-138/C-139（序列化边界）：积压行经真 stdout，键集与金样本一致、member = 驻场身份。
func TestB435SessionWaitBacklogMemberIsResident(t *testing.T) {
	e := newB435WaitEnv(t)
	e.sit(t, "agent:resi")
	e.send(t, "@agent:resi 一", "agent:resi")
	e.send(t, "无 @ 的人类发言") // 默认收件人 → 驻场者
	out, _, err := runLedgerCLI(t, e.dir, "session", "wait", e.session, "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	line := b435SingleLine(t, out)
	golden, err := os.ReadFile(filepath.Join("..", "internal", "proto", "testdata", "session_backlog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b435JSONKeys(t, line), b435JSONKeys(t, strings.TrimSpace(string(golden))); got != want {
		t.Fatalf("积压行键集应同金样本: got=%s want=%s", got, want)
	}
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(line), &backlog); err != nil {
		t.Fatal(err)
	}
	if backlog.Type != "session_backlog" || backlog.Member != "agent:resi" || len(backlog.Hits) != 2 {
		t.Fatalf("积压行 member 应为驻场身份且含两命中: %+v", backlog)
	}
}

// C-141：SessionWake.unread = ListSessions(驻场身份) 中该会话的 Unread（同一投影）。
func TestB435SessionWaitUnreadIsResidentProjection(t *testing.T) {
	e := newB435WaitEnv(t)
	e.sit(t, "user:r")
	first := e.send(t, "@user:r 一", "user:r")
	e.send(t, "@user:r 二", "user:r")
	out, _, err := runLedgerCLI(t, e.dir, "session", "wait", e.session, "--since", fmt.Sprint(first-1), "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	wake := decodeSessionWake(t, out)
	svc, st, err := openRoomService()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	summaries, err := svc.ListSessions("user:r")
	if err != nil {
		t.Fatal(err)
	}
	want := -1
	for _, s := range summaries {
		if s.ID == e.session {
			want = s.Unread
		}
	}
	if want < 1 || wake.Unread != want {
		t.Fatalf("unread=%d，want ListSessions(user:r) 的 %d", wake.Unread, want)
	}
}

// C-134 + 生命周期（R plan Q10 乙）：wait 以启动时驻场者为地址；运行中 stand + 另一身份 sit 后，
// 旧进程不输出属于新地址的消息，并在下一拍以非 0（≠124，C-133 同档）退出，游标未动；新驻场者收得到。
func TestB435SessionWaitStartupAddressFixed(t *testing.T) {
	e := newB435WaitEnv(t)
	oldAddr := e.sit(t, "user:a")
	oldInterval := sessionWaitPollInterval
	sessionWaitPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { sessionWaitPollInterval = oldInterval })
	var out syncBuffer
	c := &cobra.Command{Use: "wait"}
	c.SetOut(&out)
	c.SetContext(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runSessionWait(c, e.session, 0, false, 10*time.Second, false) }()
	time.Sleep(200 * time.Millisecond) // 让旧进程走过积压、进入实时轮询
	if err := e.svc.StandResident(e.session, "user:a"); err != nil {
		t.Fatal(err)
	}
	e.sit(t, "user:b")
	seq := e.send(t, "给新驻场者的无 @ 消息")
	select {
	case err := <-errCh:
		b435NonZeroNot124(t, err, "驻场者变更后的旧 session wait")
	case <-time.After(5 * time.Second):
		t.Fatal("驻场者变更后旧 session wait 未退出（静默挂起）")
	}
	if s := out.String(); s != "" {
		t.Fatalf("旧进程不得输出: %q", s)
	}
	if _, ok := e.cursor(t, proto.DeliveryCursorKey(oldAddr)); ok {
		t.Fatal("旧地址游标不得推进")
	}
	got, _, err := runLedgerCLI(t, e.dir, "session", "wait", e.session, "--timeout", "5s")
	if err != nil {
		t.Fatalf("新驻场者 wait: %v", err)
	}
	var backlog proto.SessionBacklog
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &backlog); err != nil || backlog.Member != "user:b" ||
		len(backlog.Hits) != 1 || backlog.Hits[0].Hit.Seq != seq {
		t.Fatalf("新驻场者应收到 seq=%d: %+v err=%v", seq, backlog, err)
	}
}

// C-228 消费侧：匹配读驻场者失败 → 非 0 ≠124，游标未推进（不得当未命中越过）。
func TestB435SessionWaitReadErrorIsNotMiss(t *testing.T) {
	e := newB435WaitEnv(t)
	addr := e.sit(t, "user:r")
	e.send(t, "@user:r 读失败", "user:r")
	resetWaitHooks(t)
	broken := false
	sessionWaitBeforeJudge = func(from, to int64) {
		if broken {
			return
		}
		broken = true
		// 候选已读出、判定之前把驻场者表换成缺列同名视图：Matches 读驻场者失败。
		e.exec(t, `DROP TABLE session_residents`)
		e.exec(t, `CREATE VIEW session_residents AS SELECT 'x' AS session`)
	}
	_, _, err := runLedgerCLI(t, e.dir, "session", "wait", e.session, "--timeout", "5s")
	b435NonZeroNot124(t, err, "匹配读失败的 session wait")
	if !broken {
		t.Fatal("判定同步点未触发")
	}
	if _, ok := e.cursor(t, proto.DeliveryCursorKey(addr)); ok {
		t.Fatal("读失败不得推进游标")
	}
}

// C-241：collab 包外非测试源码零 MessageWakeTargets 调用（AST 选择子扫描；docs 与 collab 除外）。
func TestB435MessageWakeTargetsNotCalledOutsideCollab(t *testing.T) {
	root := ".."
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "docs") {
				return filepath.SkipDir
			}
			if filepath.ToSlash(path) == "../internal/collab" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "MessageWakeTargets" {
					hits = append(hits, path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(hits)
	if len(hits) != 0 {
		t.Fatalf("collab 包外不得调用 MessageWakeTargets（C-241），命中: %v", hits)
	}
}
