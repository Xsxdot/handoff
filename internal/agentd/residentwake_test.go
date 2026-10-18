// residentwake_test.go —— B435 O2 驻场者门铃信桥（契约 C-234、C-403/404、C-407–C-412、C-415–C-418；
// O2 plan Q1–Q3 甲）。
//
// 观察点：真账本 + 真 collab 匹配函数 + 真 consumeAutomationEventsOnce（从不直调门铃入口）+ 临时信箱
// （Server.desktopHome = t.TempDir()）+ 本文件自建 opener spy。判据钉行为：信箱目录里的文件名与字节、
// spy 收到的 URL 与次数、session_delivery_cursors 行集合、账本 MaxSeq、自动化水位读回、keystone 回合数。
// 每个「不按铃」断言配同夹具「按铃」对照，防「什么都不做」的稳定假绿。
//
// 边界：不引用 B434 测试夹具 desktopOpenerSpy / newDesktopPeer（它们在 X 要拆的文件里，plan §2.1）；
// 复用 K0 保留夹具 newNoPTYAutomationEnv、b435Session、b435ExecSQL、sendSessionMessage、drainAutomation。
package agentd

import (
	"bytes"
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

const (
	o2Resident       = "agent:org"
	o2DesktopSession = "local_o2-organizer-1"
	o2CLISession     = "o2-cli-session-1"
)

// o2OpenerSpy 记录 continue 深链调用；err 非 nil 时每次返回它（模拟 open 失败）。
type o2OpenerSpy struct {
	mu   sync.Mutex
	urls []string
	err  error
}

func (s *o2OpenerSpy) open(link string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urls = append(s.urls, link)
	return s.err
}

func (s *o2OpenerSpy) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

// o2LogBuffer 是并发安全的日志缓冲（消费循环与测试读取可能并发）。
type o2LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *o2LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *o2LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// o2CaptureLog 把 srv 的日志改写进缓冲（Debug 级），返回缓冲。
func o2CaptureLog(srv *Server) *o2LogBuffer {
	b := &o2LogBuffer{}
	srv.log = slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return b
}

// o2Wire 给 srv 装临时信箱与 spy，返回二者。
func o2Wire(t *testing.T, srv *Server) (string, *o2OpenerSpy) {
	t.Helper()
	home := t.TempDir()
	spy := &o2OpenerSpy{}
	srv.desktopHome, srv.desktopOpen = home, spy.open
	return home, spy
}

// o2SitResident 直写驻场者行（R 写面的落库形状；machine 空串表示不写）。
func o2SitResident(t *testing.T, env *ledgerEnv, session, identity, desktop, cli, machine string) {
	t.Helper()
	b435ExecSQL(t, env, `INSERT INTO session_residents (session, identity, desktop_session, cli_session, machine, sat_at)
		VALUES (?, ?, ?, ?, ?, ?)`, session, identity, desktop, cli, machine, time.Now().UTC().Format(time.RFC3339Nano))
}

func o2Hostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil || h == "" {
		t.Skipf("取不到 hostname，本机判据 hostname 分支无从构造: %v", err)
	}
	return h
}

// o2Letter 是信箱目录里的一封信。
type o2Letter struct {
	name string
	body string
}

// o2Letters 读 home 信箱里全部最终名信件（跳过点开头的临时文件），按文件名排序。
func o2Letters(t *testing.T, home string) []o2Letter {
	t.Helper()
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("读信箱 %s: %v", dir, err)
	}
	var out []o2Letter
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("读信 %s: %v", e.Name(), err)
		}
		out = append(out, o2Letter{name: e.Name(), body: string(raw)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func o2AssertLetterName(t *testing.T, name, cli string) {
	t.Helper()
	if !regexp.MustCompile(`^` + regexp.QuoteMeta(cli) + `\.[0-9]{19}\.md$`).MatchString(name) {
		t.Fatalf("信件文件名应形如 %s.<19 位>.md（C-404），得 %q", cli, name)
	}
}

func o2RawDB(t *testing.T, env *ledgerEnv) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开原始账本: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// o2CursorRows 读 session_delivery_cursors 全部行（member|last_seq），排序后返回。
func o2CursorRows(t *testing.T, env *ledgerEnv) []string {
	t.Helper()
	rows, err := o2RawDB(t, env).Query(`SELECT member, last_seq FROM session_delivery_cursors ORDER BY member`)
	if err != nil {
		t.Fatalf("读交付游标: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var member string
		var last int64
		if err := rows.Scan(&member, &last); err != nil {
			t.Fatalf("扫交付游标: %v", err)
		}
		out = append(out, member+"|"+strconv.FormatInt(last, 10))
	}
	return out
}

func o2MaxSeq(t *testing.T, env *ledgerEnv) int64 {
	t.Helper()
	seq, err := env.ledger.MaxSeq()
	if err != nil {
		t.Fatalf("读账本 MaxSeq: %v", err)
	}
	return seq
}

func o2Cursor(srv *Server) int64 {
	srv.automationMu.Lock()
	defer srv.automationMu.Unlock()
	return srv.automationCursor
}

func o2Consume(t *testing.T, srv *Server) {
	t.Helper()
	if _, _, err := srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 段 0a：机内主链
// ---------------------------------------------------------------------------

// C-401/403/404/410/411/412 + C-231 不回归：人类无 @ 发言落到驻场者 → 本机恰一封两行门铃信（字节 =
// RenderResidentLetter）、不含正文哨兵、opener 恰一次 = continue 深链；交付游标行集合、账本 MaxSeq 不变，
// keystone 零回合。
func TestB435ResidentDoorbellMainChain(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	root := createCoordCard(t, env)
	b435BindPush(t, env, root, "o2-root-0a")
	sessionID := b435Session(t, env, root)
	o2SitResident(t, env, sessionID, o2Resident, o2DesktopSession, o2CLISession, o2Hostname(t))
	home, spy := o2Wire(t, env.srv)
	drainAutomation(t, env)

	const sentinel = "O2-SENTINEL-正文-7f3a"
	seq := sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "人类无 @ 发言 "+sentinel, nil, 0)
	cursorsBefore := o2CursorRows(t, env)
	maxBefore := o2MaxSeq(t, env)

	o2Consume(t, env.srv)

	letters := o2Letters(t, home)
	if len(letters) != 1 {
		t.Fatalf("应恰一封门铃信，得 %d: %+v", len(letters), letters)
	}
	o2AssertLetterName(t, letters[0].name, o2CLISession)
	want := proto.RenderResidentLetter(proto.ResidentLetter{Session: sessionID, Count: 1, MinSeq: seq, MaxSeq: seq})
	if letters[0].body != want {
		t.Fatalf("信件字节应等于 RenderResidentLetter（C-401）:\n得 %q\n应 %q", letters[0].body, want)
	}
	if strings.Contains(letters[0].body, sentinel) || strings.Contains(letters[0].body, "user:tester") {
		t.Fatalf("信中不得含正文或作者（C-403）: %q", letters[0].body)
	}
	wantURL, err := claudeContinueURL(o2DesktopSession)
	if err != nil {
		t.Fatal(err)
	}
	if got := spy.calls(); len(got) != 1 || got[0] != wantURL {
		t.Fatalf("opener 应恰一次且 URL = continue 深链（C-412），得 %v", got)
	}
	if after := o2CursorRows(t, env); strings.Join(after, ",") != strings.Join(cursorsBefore, ",") {
		t.Fatalf("写信不得推进交付游标（C-410）: 前 %v 后 %v", cursorsBefore, after)
	}
	if after := o2MaxSeq(t, env); after != maxBefore {
		t.Fatalf("写信不得落账本事件（C-411）: MaxSeq 前 %d 后 %d", maxBefore, after)
	}
	if launches, resumes, _ := runner.snapshot(); launches != 0 || len(resumes) != 0 {
		t.Fatalf("门铃不经 keystone（C-411/C-231）: launches=%d resumes=%v", launches, resumes)
	}
	if c := o2Cursor(env.srv); c < seq {
		t.Fatalf("门铃成功后自动化水位应越过该消息（%d），得 %d", seq, c)
	}

	// 再跑两轮：同一事件不重按（每进程至多一次）。
	o2Consume(t, env.srv)
	o2Consume(t, env.srv)
	if n := len(o2Letters(t, home)); n != 1 {
		t.Fatalf("同一命中不得重复按铃，得 %d 封", n)
	}
	if n := len(spy.calls()); n != 1 {
		t.Fatalf("同一命中 opener 不得重复调用，得 %d 次", n)
	}
}

// ---------------------------------------------------------------------------
// 段 0b：本机判据（H1：同账本两台 Server）
// ---------------------------------------------------------------------------

// o2Peer 是共享账本的一台 agentd：独立 store/配置/游标、本文件 spy 与临时信箱、假 keystone。
type o2Peer struct {
	srv    *Server
	spy    *o2OpenerSpy
	home   string
	runner *queueTraceRunner
}

// newO2Peer 起一台共享 st 账本的 agentd。selfName 非空 → 活配置 Targets[selfName] 指向自身监听
// （IsSelfTarget 分支）；otherName/otherAddr 非空 → Targets[otherName] 指向 otherAddr（非本机）。
func newO2Peer(t *testing.T, st *ledger.Store, selfName, otherName, otherAddr string) *o2Peer {
	t.Helper()
	backend, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(), Targets: map[string]config.Target{}}
	srv := NewServer(cfg, backend, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetLedger(st)
	newManagerForServer(t, srv, nil)
	srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	ts := testhttp.NewServer(t, srv.Handler())
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	if selfName != "" {
		cfg.Targets[selfName] = config.Target{Addr: ts.URL, Token: testToken}
	}
	if otherName != "" {
		cfg.Targets[otherName] = config.Target{Addr: otherAddr, Token: testToken}
	}
	SetupAutomationForTest(t, srv, st)
	runner := &queueTraceRunner{}
	srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, srv.autoLedger, attachLocator{}))
	p := &o2Peer{srv: srv, spy: &o2OpenerSpy{}, home: t.TempDir(), runner: runner}
	srv.desktopOpen, srv.desktopHome = p.spy.open, p.home
	return p
}

// o2RequestCounter 是假对端：记录收到的每个请求路径（断言「零出站转交」）。
type o2RequestCounter struct {
	mu    sync.Mutex
	paths []string
}

func (c *o2RequestCounter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.paths = append(c.paths, r.URL.Path)
	c.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (c *o2RequestCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

func o2CountEvents(t *testing.T, env *ledgerEnv, typ string) int {
	t.Helper()
	var n int
	if err := o2RawDB(t, env).QueryRow(`SELECT COUNT(*) FROM card_events WHERE type = ?`, typ).Scan(&n); err != nil {
		t.Fatalf("数 %s 事件: %v", typ, err)
	}
	return n
}

// C-415/C-416/C-417 + H1：驻场者 machine="o2-machine-b"（非 hostname）。A 的活配置把 o2-machine-b 指向
// 一个假对端（非本机）→ A 零信、零 opener、假对端零请求、零 needs_human；B 以 Targets[o2-machine-b]
// 自指（IsSelfTarget 分支）→ 恰一封、opener 一次。两台各自游标从 0 起、各跑真消费循环。
func TestB435ResidentDoorbellOnlyOnResidentMachine(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	const machineB = "o2-machine-b"
	if machineB == o2Hostname(t) {
		t.Skip("hostname 恰为夹具机器名，无法构造非本机")
	}
	sessionID := b435Session(t, env)
	o2SitResident(t, env, sessionID, o2Resident, o2DesktopSession, o2CLISession, machineB)
	counter := &o2RequestCounter{}
	fake := testhttp.NewServer(t, counter)
	a := newO2Peer(t, env.ledger, "", machineB, fake.URL)
	b := newO2Peer(t, env.ledger, machineB, "", "")
	if a.srv.IsSelfTarget(machineB) || !b.srv.IsSelfTarget(machineB) {
		t.Fatalf("夹具前提：A 对 %s 非本机、B 自指（A=%v B=%v）", machineB, a.srv.IsSelfTarget(machineB), b.srv.IsSelfTarget(machineB))
	}
	o2Consume(t, a.srv)
	o2Consume(t, b.srv)
	needsBefore := o2CountEvents(t, env, ledger.EvNeedsHuman)

	seq := sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "人类无 @ 发言（跨机）", nil, 0)
	o2Consume(t, a.srv)
	o2Consume(t, b.srv)

	if n := len(o2Letters(t, a.home)); n != 0 {
		t.Fatalf("非本机 A 不得写信（C-416），得 %d 封", n)
	}
	if n := len(a.spy.calls()); n != 0 {
		t.Fatalf("非本机 A 不得调 opener（C-416），得 %d 次", n)
	}
	if n := counter.count(); n != 0 {
		t.Fatalf("非本机 A 不得出站转交（C-416/C-417），假对端收到 %d 个请求: %v", n, counter.paths)
	}
	if after := o2CountEvents(t, env, ledger.EvNeedsHuman); after != needsBefore {
		t.Fatalf("非本机驻场者不得落 needs_human（C-416）: 前 %d 后 %d", needsBefore, after)
	}
	letters := o2Letters(t, b.home)
	if len(letters) != 1 {
		t.Fatalf("驻场者所在机 B 应恰一封（C-415 IsSelfTarget 分支），得 %d", len(letters))
	}
	want := proto.RenderResidentLetter(proto.ResidentLetter{Session: sessionID, Count: 1, MinSeq: seq, MaxSeq: seq})
	if letters[0].body != want {
		t.Fatalf("B 的信件字节不符: %q", letters[0].body)
	}
	if n := len(b.spy.calls()); n != 1 {
		t.Fatalf("B 的 opener 应恰一次，得 %d", n)
	}
	for _, p := range []*o2Peer{a, b} {
		if launches, resumes, _ := p.runner.snapshot(); launches != 0 || len(resumes) != 0 {
			t.Fatalf("门铃不经 keystone: launches=%d resumes=%v", launches, resumes)
		}
	}
}

// ---------------------------------------------------------------------------
// 段 3：源码守卫（C-234、C-410/411/417/418 的静态半边）
// ---------------------------------------------------------------------------

// TestB435ResidentBridgeSourceGuard 扫 residentwake.go 与 desktopwake.go 非测试源码：
//   - 两文件（C-234）：不调用 MessageWakeTargets / AddressesCard / IsHumanAuthor，不出现 .Mentions 选择子；
//   - residentwake.go：至少一处调用 Recipients 或 Matches（C-234/C-408 判定唯一入口）；不调用
//     transferCoordinatorWake（C-417）、不引用 keystone 包与 s.keystone（C-411）；对 s.ledger / s.autoLedger
//     只许读驻场者行 SessionResident（C-410/C-411：不推进交付游标、不写账本——白名单而非黑名单，
//     任何新增账本方法调用都先红）；对 s.rooms 只许 Recipients / Matches（不经 collab 发言落账）。
//
// 文件缺失即红（不跳过）：门铃文件被改名或删掉时守卫必须报警。
func TestB435ResidentBridgeSourceGuard(t *testing.T) {
	scan := func(path string) *ast.File {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s 失败（守卫对文件缺失报红）: %v", path, err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		return f
	}
	banned := map[string]bool{"MessageWakeTargets": true, "AddressesCard": true, "IsHumanAuthor": true}
	for _, path := range []string{"residentwake.go", "desktopwake.go"} {
		f := scan(path)
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if x.Sel.Name == "Mentions" {
					t.Errorf("%s 读了 .Mentions——判「给不给驻场者写信」只经 Recipients/Matches（C-234）", path)
				}
				if banned[x.Sel.Name] {
					t.Errorf("%s 引用 %s——判定唯一入口是匹配函数（C-234）", path, x.Sel.Name)
				}
			case *ast.Ident:
				if banned[x.Name] {
					t.Errorf("%s 引用 %s——判定唯一入口是匹配函数（C-234）", path, x.Name)
				}
			}
			return true
		})
	}

	f := scan("residentwake.go")
	for _, imp := range f.Imports {
		if strings.Contains(imp.Path.Value, "/internal/keystone") {
			t.Errorf("residentwake.go 引入 keystone 包——门铃不经 keystone（C-411）")
		}
	}
	allowed := map[string]map[string]bool{
		"ledger":     {"SessionResident": true},
		"autoLedger": {"SessionResident": true},
		"rooms":      {"Recipients": true, "Matches": true},
	}
	callsMatcher := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "transferCoordinatorWake" {
					t.Errorf("residentwake.go 调用 transferCoordinatorWake——驻场者地址从不经转交（C-417）")
				}
				return true
			}
			switch sel.Sel.Name {
			case "Recipients", "Matches":
				callsMatcher = true
			case "transferCoordinatorWake":
				t.Errorf("residentwake.go 调用 transferCoordinatorWake——驻场者地址从不经转交（C-417）")
			case "AdvanceSessionDeliveryCursor":
				t.Errorf("residentwake.go 推进交付游标——写信不推进 session_delivery_cursors（C-410）")
			}
			if inner, ok := sel.X.(*ast.SelectorExpr); ok {
				if list, guarded := allowed[inner.Sel.Name]; guarded && !list[sel.Sel.Name] {
					t.Errorf("residentwake.go 调用 %s.%s——门铃对账本/collab 只许读驻场者行与匹配函数（C-410/C-411）",
						inner.Sel.Name, sel.Sel.Name)
				}
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "keystone" {
				t.Errorf("residentwake.go 引用 .keystone——门铃不经 keystone（C-411）")
			}
		}
		return true
	})
	if !callsMatcher {
		t.Errorf("residentwake.go 未调用 Recipients 或 Matches——门铃触发只经匹配函数（C-234/C-408）")
	}
}
