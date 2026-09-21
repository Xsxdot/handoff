// 房间 HTTP 面与收件箱测试（B156.2 C6）：六端点、错误映射逐哨兵、收件箱三源、
// Watchers 翻转正控、破坏性不受限、集成冒烟。全部从 HTTP 进入（spec 测试接缝
// 清单 #1 的调用方侧 = gateway 控制面），调用链穿过 collab 入站 api 门面。
//
// B358.4 红窗改写：下方标注「B358.4 红窗改写」的 10 支原以旧房间 user 发言成功
// 为断言/夹具，随 B358.1 S1 只读归档失效；按新会话语义就地改写（名字保留供
// 红窗核算逐支比对），断言意图逐支对应迁移，见 docs/superpowers/plans/b358.4-plan.md §5。
package agentd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// roomsLogCapture 记录 Server.log 的每条记录（含级别），供「噪声降 Debug」断言。
// 它不按级别过滤：断言读 Record.Level，直接证明降级，不靠 logx 的过滤行为。
type roomsLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *roomsLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *roomsLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.records = append(c.records, r.Clone())
	c.mu.Unlock()
	return nil
}
func (c *roomsLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *roomsLogCapture) WithGroup(string) slog.Handler      { return c }

// find 返回首条 Message 匹配的记录；不存在则 false。
func (c *roomsLogCapture) find(msg string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

// consoleMember 是测试环境里控制台已认证主体的成员标识（B358.9 换脸后）：
// newRoomsEnv 配 console_user="sycm"，各端点经 resolveConsoleIdentity 解析出
// user:sycm 作为 actor/member。消息 @ 该标识才进收件箱 mention 源。
const consoleMember = "user:sycm"

// newRoomsEnv 组装房间 HTTP 测试环境：真 SQLite 账本 + bug 工作流 + SetupAutomation
// （装配 collab.Service 与换绑端口）+ httptest 全链。
func newRoomsEnv(t *testing.T) *ledgerEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	seedAgentdLedger(t, env.ledger, "bug")
	SetupAutomationForTest(t, env.srv, env.ledger)
	// B358.9：房间/会话面换脸后必须有 console_user，否则全部端点 fail-closed。
	next := *env.srv.conf()
	next.ConsoleUser = "sycm"
	env.srv.cfg.Store(&next)
	return env
}

// inboxItems 拉取收件箱并解码 items。
func inboxItems(t *testing.T, env *ledgerEnv) []proto.InboxItem {
	t.Helper()
	var out struct {
		Items []proto.InboxItem `json:"items"`
	}
	code := env.getJSON(t, "/api/inbox", &out)
	if code != 200 {
		t.Fatalf("GET /api/inbox: %d", code)
	}
	return out.Items
}

func hasOrigin(items []proto.InboxItem, origin string) bool {
	for _, it := range items {
		if it.Origin == origin {
			return true
		}
	}
	return false
}

// itemKeys 序列化一条收件箱条目并返回 JSON 键集（金样本键集断言用）。
func itemKeys(t *testing.T, it proto.InboxItem) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}

// decodeRoomsPage 解 B374 分页信封（真实 HTTP body）。
func decodeRoomsPage(t *testing.T, body string) proto.RoomsPage {
	t.Helper()
	var page proto.RoomsPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("分页信封解码失败: %v（原文 %s）", err, body)
	}
	return page
}

// testRoomCursor 复刻契约 §3.2 的游标编码（base64url_nopad(json{a,r})），
// 供测试构造真实客户端拿到的续页游标；encodeRoomCursor 在 internal/collab 私有。
func testRoomCursor(t *testing.T, at time.Time, roomID string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"a": at.UnixNano(), "r": roomID})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestRoomsListLegacyRequestRejectedWith426 锁旧客户端阻断（F9）：query 里
// limit 与 cursor 双双缺席即旧客户端，必须 426 + 冻结文案，且不得夹带半页 rooms。
func TestRoomsListLegacyRequestRejectedWith426(t *testing.T) {
	env := newRoomsEnv(t)
	seedCard(t, env, "旧客户端卡")
	for _, path := range []string{"/api/rooms", "/api/rooms?project=p"} {
		code, body := ledgerGet(t, env.testAgentdEnv, path)
		if code != http.StatusUpgradeRequired {
			t.Fatalf("%s 旧客户端必须 426，实得 %d %s", path, code, body)
		}
		if !strings.Contains(body, roomsListLegacyMessage) {
			t.Fatalf("%s 426 文案漂移: %s", path, body)
		}
		if strings.Contains(body, `"rooms"`) {
			t.Fatalf("%s 426 不得夹带半页 rooms: %s", path, body)
		}
	}
}

// TestRoomsListPaginationHTTP 经真实 HTTP 一次覆盖 F5–F8、F10：limit 收敛
// 与非法参数的 400（不得经 roomsListErrorStatus 假绿成 500）。
func TestRoomsListPaginationHTTP(t *testing.T) {
	env := newRoomsEnv(t)
	for i := 0; i < 205; i++ {
		seedCard(t, env, fmt.Sprintf("分页卡-%03d", i))
	}
	// F7：limit<=0 取默认 50
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=0")
	if code != http.StatusOK {
		t.Fatalf("limit=0: %d %s", code, body)
	}
	page := decodeRoomsPage(t, body)
	// 205 卡 + project:p + global = 207 条扁平序；首页 50 条。
	if len(page.Rooms) != 50 || !page.HasMore {
		t.Fatalf("limit=0 应取默认 50 且 has_more=true: len=%d has_more=%v", len(page.Rooms), page.HasMore)
	}
	// F5：limit 缺席但带合法游标（非 legacy）→ 默认 50。
	last := page.Rooms[len(page.Rooms)-1]
	cursor := testRoomCursor(t, last.LastActivity, last.ID)
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/rooms?cursor="+url.QueryEscape(cursor))
	if code != http.StatusOK {
		t.Fatalf("cursor-only: %d %s", code, body)
	}
	if got := decodeRoomsPage(t, body); len(got.Rooms) != 50 {
		t.Fatalf("limit 缺席应取默认 50，实得 %d", len(got.Rooms))
	}
	// F6：limit>200 取上限 200
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=500")
	if code != http.StatusOK {
		t.Fatalf("limit=500: %d %s", code, body)
	}
	page = decodeRoomsPage(t, body)
	if len(page.Rooms) != 200 || !page.HasMore {
		t.Fatalf("limit=500 应取上限 200 且 has_more=true: len=%d", len(page.Rooms))
	}
	// F8：limit 非整数 → 400（不得 500）
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=abc"); code != http.StatusBadRequest {
		t.Fatalf("limit=abc 必须 400（若经 roomsListErrorStatus 会假绿成 500）: %d %s", code, body)
	}
	// F10：非法游标 → 400
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50&cursor=!!!not-base64!!!"); code != http.StatusBadRequest {
		t.Fatalf("非法游标必须 400: %d %s", code, body)
	}
}

// TestRoomsListLimitConstants 锁 gateway 侧 limit 常量与契约 §3.2/§3.3 同值。
func TestRoomsListLimitConstants(t *testing.T) {
	if roomsListDefaultLimit != 50 || roomsListMaxLimit != 200 {
		t.Fatalf("gateway limit 常量漂移: %d/%d", roomsListDefaultLimit, roomsListMaxLimit)
	}
}

// TestRoomsListAttachRefreshLimitedToPageRooms 锁刷新限域（F17/F18）：第 1 页
// 之外的远端挂账不得触发任何远端 RPC；翻到含该房间的页后才允许发生。
func TestRoomsListAttachRefreshLimitedToPageRooms(t *testing.T) {
	env := newRoomsEnv(t)
	// 先建远端挂账卡（时间最早 → 沉到扁平序末段），再建 55 张卡把首页占满。
	// B358 P5 后旧卡房间只读归档，禁止再向卡房间 Send 刷 LastActivity——卡无消息时
	// LastActivity 回退卡 UpdatedAt，创建时间序即活动序；sleep 确保远端卡严格最早。
	remoteCard := seedCard(t, env, "远端卡")
	if err := env.ledger.LinkTask(remoteCard.ID, "relay", "T-relay", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 55; i++ {
		seedCard(t, env, fmt.Sprintf("占位卡-%02d", i))
	}
	var mu sync.Mutex
	calls := 0
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task":            map[string]any{"id": "T-relay", "repo_path": "/repo", "work_dir": "/relay/B1"},
			"pending_tickets": []any{}, "recent_events": []any{},
		})
	}))
	env.srv.conf().Targets = map[string]config.Target{
		"relay": {Addr: remote.URL, Token: "remote-token"},
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != http.StatusOK {
		t.Fatalf("首页: %d %s", code, body)
	}
	page := decodeRoomsPage(t, body)
	for _, r := range page.Rooms {
		if r.ID == remoteCard.ID {
			t.Fatalf("夹具失效：远端卡必须不在首页: %s", body)
		}
	}
	time.Sleep(200 * time.Millisecond) // 给后台刷新一个起跑窗口
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 0 {
		t.Fatalf("首页之外的房间不得触发远端 RPC，实得 %d", got)
	}
	// 翻到含远端卡的那一页后，远端 RPC 才允许发生。
	last := page.Rooms[len(page.Rooms)-1]
	cursor := testRoomCursor(t, last.LastActivity, last.ID)
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50&cursor="+url.QueryEscape(cursor)); code != http.StatusOK {
		t.Fatalf("续页: %d %s", code, body)
	}
	eventually(t, 2*time.Second, "续页触发远端 attach", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 0
	})
}

// TestRoomsListAttachLogsAtDebug 锁高频 attach 日志降 Debug（契约 §3.4①）：
// 捕获 handler 不过滤级别，直接断言 Record.Level。
func TestRoomsListAttachLogsAtDebug(t *testing.T) {
	t.Setenv("HANDOFF_LOG_LEVEL", "info")
	env := newRoomsEnv(t)
	cap := &roomsLogCapture{}
	env.srv.log = slog.New(cap)
	seedCard(t, env, "降级卡")
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50"); code != http.StatusOK {
		t.Fatalf("列表: %d %s", code, body)
	}
	if _, ok := cap.find("会话列表响应成功"); !ok {
		t.Fatal("成功路径不得静默：会话列表响应成功缺失")
	}
	rec, ok := cap.find("房间 attach 投影完成")
	if !ok {
		t.Fatal("逐次 attach 汇总日志缺失")
	}
	if rec.Level != slog.LevelDebug {
		t.Fatalf("高频 attach INFO 必须降 Debug，实得 %s", rec.Level)
	}
}

// TestRoomsListPageEnvelopeWireShapes 穿过真实 writeJSON 序列化边界断言信封形状
// （F1–F4 重编号）：rooms/has_more 恒出键（含 false）、has_more=false 时
// next_cursor 必须缺席。
func TestRoomsListPageEnvelopeWireShapes(t *testing.T) {
	env := newRoomsEnv(t)
	seedCard(t, env, "信封卡")
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != http.StatusOK {
		t.Fatalf("列表: %d %s", code, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["rooms"]; !ok {
		t.Fatalf("rooms 恒出键: %s", body)
	}
	hasMore, ok := raw["has_more"]
	if !ok {
		t.Fatalf("has_more 恒出键（含 false）: %s", body)
	}
	if string(hasMore) != "false" {
		t.Fatalf("单页 has_more 应为 false: %s", body)
	}
	if _, ok := raw["next_cursor"]; ok {
		t.Fatalf("has_more=false 时 next_cursor 必须缺席: %s", body)
	}
}

func TestRoomsListEndpoint(t *testing.T) {
	// B358.4 红窗改写：原夹具「向卡房间 user 发言」随 S1 只读归档失效。
	// 改锁会话语义下的等价行为——(a) 列表投影 preview 的正面锚迁到会话列表
	// （写入面现在只在会话房间）；(b) /api/rooms 旧读面对质保留（S1：读史不断）。
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡A")
	session := mustConsoleSession(t, env, "列表预览场")
	if _, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "hi"}, consoleMember); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Sessions []proto.SessionSummary `json:"sessions"`
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/sessions")
	if code != 200 {
		t.Fatalf("GET /api/sessions: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var found *proto.SessionSummary
	for i := range out.Sessions {
		if out.Sessions[i].ID == session.ID {
			found = &out.Sessions[i]
		}
	}
	if found == nil {
		t.Fatalf("会话应出现在列表: %+v", out.Sessions)
	}
	if found.Preview == nil || found.Preview.Body != "hi" || found.Preview.Seq <= 0 || found.Preview.CreatedAt.IsZero() {
		t.Fatalf("真实 HTTP /api/sessions 应保留 preview 正文、seq、created_at: %+v", found.Preview)
	}
	// 旧房间读面对质保留：卡房间行仍在 /api/rooms（无需发言即可读）。
	var rooms struct {
		Rooms []proto.RoomSummary `json:"rooms"`
	}
	if code := env.getJSON(t, "/api/rooms?project=p&limit=50", &rooms); code != 200 {
		t.Fatalf("GET /api/rooms: %d", code)
	}
	roomFound := false
	for _, room := range rooms.Rooms {
		if room.ID == card.ID {
			roomFound = true
		}
	}
	if !roomFound {
		t.Fatalf("旧卡房间读面应保留: %+v", rooms.Rooms)
	}
}

func TestRoomsListUnreadAndAttachProjection(t *testing.T) {
	// B358.4 红窗改写：未读/已读半边迁会话语义（写入面只在会话房间）；
	// attach 投影半边不依赖发言，卡房间挂账断言原样保留。
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "未读迁移场")
	if _, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "一"}, consoleMember); err != nil {
		t.Fatal(err)
	}
	second, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "二"}, consoleMember)
	if err != nil {
		t.Fatal(err)
	}
	// attach 半边（原样）：本机挂账卡 → /api/rooms 行带 attach。
	card := seedCard(t, env, "可挂账卡")
	if err := env.st.CreateTask(&proto.Task{ID: "T1", RepoPath: "/repo", WorkDir: "/work/B1"}); err != nil {
		t.Fatal(err)
	}
	if err := env.ledger.LinkTask(card.ID, "", "T1", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Sessions []proto.SessionSummary `json:"sessions"`
		Rooms    []proto.RoomSummary    `json:"rooms"`
	}
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/sessions")
	if code != 200 {
		t.Fatalf("GET /api/sessions: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Sessions {
		if s.ID == session.ID && s.Unread != 2 {
			t.Fatalf("两条未读消息应投影 unread=2: %+v", s)
		}
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != 200 {
		t.Fatalf("GET /api/rooms: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var roomFound *proto.RoomSummary
	globalFound := false
	for i := range out.Rooms {
		if out.Rooms[i].ID == card.ID {
			roomFound = &out.Rooms[i]
		}
		if out.Rooms[i].Kind == "global" {
			globalFound = true
			if out.Rooms[i].Unread < 0 {
				t.Fatalf("global unread 必须在线: %+v", out.Rooms[i])
			}
		}
	}
	if roomFound == nil {
		t.Fatalf("卡房间应出现在列表: %+v", out.Rooms)
	}
	if !globalFound {
		t.Fatal("global 房间应出现在列表")
	}
	if roomFound.Attach == nil || roomFound.Attach.Target != "" || roomFound.Attach.TaskID != "T1" ||
		roomFound.Attach.WorkDir != "/work/B1" || roomFound.Attach.Command != "handoff attach T1" {
		t.Fatalf("本机挂账 attach 投影错误: %+v", roomFound.Attach)
	}
	// 已读：POST /api/rooms/{session}/read 到最新 seq → 会话行 unread=0。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/read",
		fmt.Sprintf(`{"upto_seq":%d}`, second)); code != 200 {
		t.Fatalf("POST /read: %d %s", code, body)
	}
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/sessions")
	if code != 200 {
		t.Fatalf("GET /api/sessions after read: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Sessions {
		if s.ID == session.ID && s.Unread != 0 {
			t.Fatalf("已读后 unread 应为 0: %+v", s)
		}
	}
}

func TestRoomsListWithoutAttachmentKeepsAttachMissing(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "无挂账卡")
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != 200 {
		t.Fatalf("GET /api/rooms: %d %s", code, body)
	}
	var out struct {
		Rooms []json.RawMessage `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	for _, raw := range out.Rooms {
		var room struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &room); err != nil {
			t.Fatal(err)
		}
		if room.ID == card.ID && strings.Contains(string(raw), `"attach"`) {
			t.Fatalf("无挂账卡不应出现 attach: %s", raw)
		}
	}
}

func TestRoomsListWireIncludesZeroUnread(t *testing.T) {
	env := newRoomsEnv(t)
	seedCard(t, env, "零未读卡")
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?project=p&limit=50")
	if code != http.StatusOK {
		t.Fatalf("GET /api/rooms: %d %s", code, body)
	}
	var out struct {
		Rooms []json.RawMessage `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rooms) == 0 {
		t.Fatal("/api/rooms 应返回至少一行")
	}
	for _, raw := range out.Rooms {
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		unread, ok := fields["unread"]
		if !ok {
			t.Fatalf("房间行必须保留 unread:0，原文=%s", raw)
		}
		if unread != float64(0) {
			t.Fatalf("零未读房间的 raw unread 应为 0，原文=%s", raw)
		}
	}
}

// TestRoomsListAttachTimeoutDoesNotBlockMainList 锁住附加投影的降级边界：远端
// 任务详情是非承重信息，慢目标只能在短超时内放弃，不能把 /api/rooms 卡住。
func TestRoomsListAttachTimeoutDoesNotBlockMainList(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "慢目标卡")
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	env.srv.conf().Targets = map[string]config.Target{
		"slow": {Addr: remote.URL, Token: "remote-token"},
	}
	if err := env.ledger.LinkTask(card.ID, "slow", "T-slow", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/rooms?limit=50", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("慢 attach 不应阻断主列表: status=%d elapsed=%s", resp.StatusCode, elapsed)
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("慢 attach 超过主列表短超时: elapsed=%s", elapsed)
	}
}

// TestRoomsListUsesBackgroundAttachCache 锁住 relay 目标的非承重边界：首次列表
// 不能等待远端任务详情；后台刷新完成后，下一次列表应直接命中缓存而不再拨远端。
func TestRoomsListUsesBackgroundAttachCache(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "后台 attach 卡")
	if err := env.ledger.LinkTask(card.ID, "relay", "T-relay", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	calls := 0
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			once.Do(func() { close(started) })
			<-release
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task":            map[string]any{"id": "T-relay", "repo_path": "/repo", "work_dir": "/relay/B1"},
				"pending_tickets": []any{}, "recent_events": []any{},
			})
			return
		}
		http.Error(w, "unexpected second remote lookup", http.StatusInternalServerError)
	}))
	env.srv.conf().Targets = map[string]config.Target{
		"relay": {Addr: remote.URL, Token: "remote-token"},
	}

	startedAt := time.Now()
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	firstElapsed := time.Since(startedAt)
	if code != http.StatusOK {
		t.Fatalf("首次列表应成功: %d %s", code, body)
	}
	if firstElapsed >= 100*time.Millisecond {
		t.Fatalf("首次列表不应等待 relay attach: elapsed=%s", firstElapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("后台 attach 刷新未开始")
	}
	close(release)

	eventually(t, 2*time.Second, "缓存命中 relay attach", func() bool {
		code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
		if code != http.StatusOK {
			return false
		}
		var out struct {
			Rooms []proto.RoomSummary `json:"rooms"`
		}
		if json.Unmarshal([]byte(body), &out) != nil {
			return false
		}
		for _, summary := range out.Rooms {
			if summary.ID == card.ID {
				return summary.Attach != nil && summary.Attach.Target == "relay" && summary.Attach.WorkDir == "/relay/B1"
			}
		}
		return false
	})
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("缓存命中后不应再次拨 relay，实际请求 %d 次", calls)
	}
}

func TestRoomsListDropsExpiredAndFailedRemoteAttachCache(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "失效 attach 卡")
	if err := env.ledger.LinkTask(card.ID, "relay", "T-relay", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task":            map[string]any{"id": "T-relay", "repo_path": "/repo", "work_dir": "/relay/B1"},
				"pending_tickets": []any{}, "recent_events": []any{},
			})
			return
		}
		http.Error(w, "remote attach unavailable", http.StatusBadGateway)
	}))
	env.srv.conf().Targets = map[string]config.Target{
		"relay": {Addr: remote.URL, Token: "remote-token"},
	}

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != http.StatusOK {
		t.Fatalf("首次列表应成功: %d %s", code, body)
	}
	key := roomAttachCacheKey(ledger.TaskLink{Target: "relay", TaskID: "T-relay"})
	eventually(t, 2*time.Second, "首次后台刷新写入缓存", func() bool {
		mu.Lock()
		gotCalls := calls
		mu.Unlock()
		env.srv.roomAttachMu.RLock()
		_, cached := env.srv.roomAttachCache[key]
		env.srv.roomAttachMu.RUnlock()
		return gotCalls == 1 && cached
	})

	// 让一个仍未过期的旧投影进入刷新失败路径；失败后不能继续把它当成可执行目标。
	env.srv.roomAttachMu.Lock()
	entry := env.srv.roomAttachCache[key]
	entry.expiresAt = time.Now().Add(time.Minute)
	env.srv.roomAttachLastRefresh = time.Now().Add(-roomAttachRefreshInterval)
	env.srv.roomAttachMu.Unlock()
	code, body = ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
	if code != http.StatusOK {
		t.Fatalf("刷新中的列表仍应成功: %d %s", code, body)
	}
	eventually(t, 2*time.Second, "刷新失败删除旧 attach 缓存", func() bool {
		mu.Lock()
		gotCalls := calls
		mu.Unlock()
		env.srv.roomAttachMu.RLock()
		_, cached := env.srv.roomAttachCache[key]
		env.srv.roomAttachMu.RUnlock()
		return gotCalls == 2 && !cached
	})
	assertAttachMissing := func(stage string) {
		t.Helper()
		code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50")
		if code != http.StatusOK {
			t.Fatalf("%s GET /api/rooms 应成功: %d %s", stage, code, body)
		}
		var out struct {
			Rooms []json.RawMessage `json:"rooms"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s GET /api/rooms 解码失败: %v", stage, err)
		}
		for _, raw := range out.Rooms {
			var summary struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &summary); err != nil {
				t.Fatalf("%s 房间行解码失败: %v", stage, err)
			}
			if summary.ID == card.ID && strings.Contains(string(raw), `"attach"`) {
				t.Fatalf("%s 失败后房间 attach 必须缺席: %s", stage, raw)
			}
		}
	}
	assertAttachMissing("刷新失败后")

	// TTL 是独立的失效闸：即使后台刷新尚未开始，过期投影也不能被列表消费。
	env.srv.storeRoomAttach(ledger.TaskLink{Target: "relay", TaskID: "T-relay"}, &proto.RoomAttach{
		Target: "relay", TaskID: "T-relay", WorkDir: "/relay/B1", Command: "handoff attach T-relay",
	})
	env.srv.roomAttachMu.Lock()
	entry = env.srv.roomAttachCache[key]
	entry.expiresAt = time.Now().Add(-time.Second)
	env.srv.roomAttachCache[key] = entry
	env.srv.roomAttachMu.Unlock()
	if got := env.srv.cachedRoomAttach(ledger.TaskLink{Target: "relay", TaskID: "T-relay"}); got != nil {
		t.Fatalf("过期 attach 不得从缓存返回: %+v", got)
	}
	assertAttachMissing("TTL 过期后")
}

// TestRoomsListTTFB200Cards 是真实账本规模的 HTTP 首字节验收：200+ 卡的主
// 列表必须在 2 秒内开始响应。事件读取次数的确定性守卫在 collab 包测试中。
func TestRoomsListTTFB200Cards(t *testing.T) {
	env := newRoomsEnv(t)
	for i := 0; i < 205; i++ {
		seedCard(t, env, fmt.Sprintf("性能卡-%03d", i))
	}
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/rooms?limit=50", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/rooms: status=%d", resp.StatusCode)
	}
	t.Logf("本机真实 HTTP/SQLite /api/rooms TTFB=%s cards=205", elapsed)
	if elapsed >= 2*time.Second {
		t.Fatalf("200+ 卡列表首字节超时: elapsed=%s", elapsed)
	}
}

func TestRoomMessagesEndpoint(t *testing.T) {
	// B358.4 红窗改写：夹具卡房间 → 会话房间；「写侧受守、读侧宽容」正面锚
	// 与否定锚断言逐条照抄（条数与内容都断言，不只断言非空）。
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "历史场")
	first, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "第一条"}, consoleMember)
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "第二条"}, consoleMember)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Messages []proto.LedgerEvent `json:"messages"`
	}
	code := env.getJSON(t, "/api/rooms/"+session.ID+"/messages?limit=10", &out)
	if code != 200 {
		t.Fatalf("GET messages: %d", code)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("历史应恰好两条: %+v", out.Messages)
	}
	seqs := map[int64]bool{}
	bodies := map[string]bool{}
	for _, ev := range out.Messages {
		seqs[ev.Seq] = true
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		bodies[msg.Body] = true
	}
	if !seqs[first] || !seqs[second] {
		t.Fatalf("两条消息的 seq 都应返回: %d %d (%v)", first, second, seqs)
	}
	if !bodies["第一条"] || !bodies["第二条"] {
		t.Fatalf("两条消息正文都应返回: %v", bodies)
	}
	// 读侧宽容的否定锚（原样）：不存在的房间 → 200 且零条。
	var empty struct {
		Messages []proto.LedgerEvent `json:"messages"`
	}
	code = env.getJSON(t, "/api/rooms/NO-SUCH/messages", &empty)
	if code != 200 {
		t.Fatalf("GET 不存在房间应 200: %d", code)
	}
	if len(empty.Messages) != 0 {
		t.Fatalf("不存在房间历史应零条: %+v", empty.Messages)
	}
}

func TestRoomSendEndpoint(t *testing.T) {
	// B358.4 红窗改写：POST 发言成功断言从卡房间（S1 起只读）迁到会话房间。
	// 会话消息是无卡事件 → 读账本改全流读（0.2#6）；actor 服务端注入断言原样。
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "发送场")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages", `{"body":"你好"}`)
	if code != 200 {
		t.Fatalf("POST messages: %d %s", code, body)
	}
	events, err := env.ledger.EventsFromAsc(nil, 0, 100)
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
		if msg.Body == "你好" {
			found = true
			if ev.Actor != consoleMember {
				t.Fatalf("actor 应服务端注入解析人名 %q: %q", consoleMember, ev.Actor)
			}
		}
	}
	if !found {
		t.Fatalf("消息未落账: %+v", events)
	}
	// 写侧受守补断言：不存在的会话房间 → 400（ErrNoRoom→400 既有映射对
	// 会话房间同样成立；与存活的 TestRoomSendErrMapping 同族）。
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/session:999/messages", `{"body":"x"}`); code != 400 {
		t.Fatalf("不存在会话房间发言应 400: %d", code)
	}
}

// TestRoomSendReplyToEndpoint 锁 B365 回复锚发送半边：POST /api/rooms/{id}/messages
// 请求体增可选 reply_to，逐字映射 RoomMessage.ReplyTo（接收侧 ResolveDelivery
// 隐式寻址原作者的判定不在本 handler）。断言：带锚发送落账 payload 含 reply_to
// 且值即被回复 seq；--reply_to 0 与缺省等价——wire omitempty 下不落键；负值 400。
func TestRoomSendReplyToEndpoint(t *testing.T) {
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "回复锚场")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages", `{"body":"被回复的上下文"}`)
	if code != 200 {
		t.Fatalf("POST 基准消息: %d %s", code, body)
	}
	var first struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal([]byte(body), &first); err != nil {
		t.Fatalf("解码 seq 响应: %v", err)
	}
	// 带锚发送：200 且落账 ReplyTo == 基准 seq。
	if code, body = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages",
		fmt.Sprintf(`{"body":"回复","reply_to":%d}`, first.Seq)); code != 200 {
		t.Fatalf("POST 带锚消息: %d %s", code, body)
	}
	// 0 与缺省等价（都不落 reply_to 键）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages",
		`{"body":"零值锚","reply_to":0}`); code != 200 {
		t.Fatalf("POST reply_to=0 应等价缺省: %d", code)
	}
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages",
		`{"body":"缺省锚"}`); code != 200 {
		t.Fatalf("POST 缺省消息: %d", code)
	}
	// 负值 400（参数错，不触账本）。
	if code, _ = ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/messages",
		`{"body":"x","reply_to":-1}`); code != 400 {
		t.Fatalf("负 reply_to 应 400: %d", code)
	}
	events, err := env.ledger.EventsFromAsc(nil, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	type payloadRow struct {
		msg proto.RoomMessage
		raw string
	}
	byBody := map[string]payloadRow{}
	for _, ev := range events {
		if ev.Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		byBody[msg.Body] = payloadRow{msg: msg, raw: string(ev.Payload)}
	}
	// 带锚：值与键都在（逐字映射）。
	hit, ok := byBody["回复"]
	if !ok {
		t.Fatalf("带锚消息未落账: %+v", byBody)
	}
	if hit.msg.ReplyTo != first.Seq {
		t.Fatalf("带锚消息 ReplyTo=%d, want %d", hit.msg.ReplyTo, first.Seq)
	}
	if !strings.Contains(hit.raw, `"reply_to"`) {
		t.Fatalf("带锚消息 payload 应含 reply_to 键: %s", hit.raw)
	}
	// 无锚（缺省 / 0 同形）：值 0 且键不落（wire omitempty）。
	for _, absent := range []string{"被回复的上下文", "零值锚", "缺省锚"} {
		row, ok := byBody[absent]
		if !ok {
			t.Fatalf("消息 %q 未落账: %+v", absent, byBody)
		}
		if row.msg.ReplyTo != 0 {
			t.Fatalf("消息 %q ReplyTo 应 0: %+v", absent, row.msg)
		}
		if strings.Contains(row.raw, "reply_to") {
			t.Fatalf("无锚消息 %q payload 不应含 reply_to 键: %s", absent, row.raw)
		}
	}
}

func TestRoomReadEndpoint(t *testing.T) {
	// B358.4 红窗改写：夹具卡房间 → 会话房间；MarkRead 水位清零断言照抄。
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "已读场")
	seq, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, consoleMember)
	if err != nil {
		t.Fatal(err)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+session.ID+"/read",
		fmt.Sprintf(`{"upto_seq":%d}`, seq))
	if code != 200 {
		t.Fatalf("POST read: %d %s", code, body)
	}
	// 读尽后未读应 0（MarkRead 到当前最大 seq → 水位语义清空）。
	if n, err := env.srv.rooms.Unread(consoleMember, session.ID); err != nil || n != 0 {
		t.Fatalf("读尽后未读应 0: %v %d", err, n)
	}
}

func TestRoomSendErrMapping(t *testing.T) {
	env := newRoomsEnv(t)
	// 不存在的房间 → 400（Send 的 ErrNoRoom 契约义务，写侧受守）
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/NO-SUCH/messages", `{"body":"x"}`); code != 400 {
		t.Fatalf("不存在房间应 400: %d", code)
	}
	// 绑定者本人以 user 发言 → 403（ErrNotWriter）
	card := seedCard(t, env, "卡E")
	if err := env.ledger.BindSeat(card.ID, "cli:codex#web", proto.SeatSourceBind, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.Send(card.ID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "x"}, "cli:codex#web"); err == nil {
		t.Fatal("绑定者发言应被 collab 拒绝")
	}
	// 终态卡房间 → 409（ErrReadOnly）
	parent := seedCard(t, env, "父")
	child := seedChildCard(t, env, parent.ID, "子")
	if err := env.ledger.MoveCard(child.ID, ledger.StatusDone, "", "test"); err != nil {
		t.Fatal(err)
	}
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+child.ID+"/messages", `{"body":"x"}`); code != 409 {
		t.Fatalf("终态房间应 409: %d", code)
	}
	// 空正文 → 400（handler 卫生检查）
	if code, _ := ledgerPost(t, env.testAgentdEnv, "/api/rooms/"+parent.ID+"/messages", `{"body":"  "}`); code != 400 {
		t.Fatalf("空正文应 400: %d", code)
	}
}

func TestCardRebindCASConflict(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡F")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/rebind",
		`{"to_session":"cli:codex#b","carrier":"cli","expect":"WRONG"}`)
	if code != http.StatusNotFound {
		t.Fatalf("旧 rebind 路由应移除: %d %s", code, body)
	}
}

func TestRoomsEndpoints503WithoutLedger(t *testing.T) {
	env := newTestAgentdEnv(t)
	code, _ := ledgerGet(t, env, "/api/rooms")
	if code != 503 {
		t.Fatalf("未挂账本应 503: %d", code)
	}
}

func TestInboxThreeSources(t *testing.T) {
	env := newRoomsEnv(t)
	// B358.4 红窗改写：mention 源夹具从 project 群房间迁会话房间（旧房间只读）。
	// 发送者用夹具群主（真成员）——发送者若是 @ 目标本人（consoleMember），
	// sendToSession 的回复即清提及会把刚落账的 @ 立刻消费掉，mention 源必空。
	session := mustConsoleSession(t, env, "收件箱三源场")
	// decision 源：卡级 + 项目级 open 裁决
	card := seedCard(t, env, "卡G")
	if _, err := env.ledger.OpenDecision(card.ID, "一句话：契约语义冲突", []string{"a", "b"}, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ledger.OpenDecision("", "项目级裁决", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	// mention 源：会话房间 @ 用户
	if _, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "改动影响 B145", Mentions: []string{consoleMember}}, sessionFixtureOwner); err != nil {
		t.Fatal(err)
	}
	// ticket 源：等待人工任务上的未答复工单
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "t1:p1", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate","permission":"x"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	for _, origin := range []string{proto.InboxOriginDecision, proto.InboxOriginTicket, proto.InboxOriginMention} {
		if !hasOrigin(items, origin) {
			t.Fatalf("收件箱应含 %s 源: %+v", origin, items)
		}
	}
}

func TestInboxDecisionOpenAllIncludingProjectLevel(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "卡H")
	if _, err := env.ledger.OpenDecision(card.ID, "卡级裁决", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ledger.OpenDecision("", "项目级裁决", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	decCount := 0
	hasProjectLevel := false
	for _, it := range items {
		if it.Origin != proto.InboxOriginDecision {
			continue
		}
		decCount++
		if it.CardID == "" {
			hasProjectLevel = true
		}
	}
	if decCount != 2 || !hasProjectLevel {
		t.Fatalf("decision 源应含 open 全量（含项目级）: %+v", items)
	}
}

func TestInboxTicketExcludesDrivenTask(t *testing.T) {
	env := newRoomsEnv(t)
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "t1:p1", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 无人驱动：上浮
	if items := inboxItems(t, env); !hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("无人驱动应上浮工单: %+v", items)
	}
	// 订阅翻转 Watchers=1：排除（内建正控——真实 hub 订阅，非 mock 返回值）
	ch, unsub := env.srv.hub.Subscribe("t1")
	defer unsub()
	_ = ch
	if items := inboxItems(t, env); hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("Watchers>0 应排除工单: %+v", items)
	}
	// 退订回 0：重新上浮
	unsub()
	if items := inboxItems(t, env); !hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("退订后应重新上浮工单: %+v", items)
	}
}

func TestInboxDestructiveTicketFloatsWithWatchers(t *testing.T) {
	env := newRoomsEnv(t)
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "t1:p1", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 审批者升级信号（D-destructive，台账 L10）：approver_decision escalate
	if _, err := env.st.AppendEvent("t1", proto.EventTypeApproverDecision, orchestration.ApproverDecisionPayload{
		TicketID: "t1:p1", Decision: "escalate"}); err != nil {
		t.Fatal(err)
	}
	ch, unsub := env.srv.hub.Subscribe("t1")
	defer unsub()
	_ = ch
	if items := inboxItems(t, env); !hasOrigin(items, proto.InboxOriginTicket) {
		t.Fatalf("破坏性工单不受 Watchers 限制应上浮: %+v", items)
	}
}

func TestInboxMentionSource(t *testing.T) {
	env := newRoomsEnv(t)
	// B358.4 红窗改写：mention 源夹具从 project 群房间迁会话房间（旧房间只读）。
	// 发送者用夹具群主（真成员）——若 consoleMember 自己发，回复即清提及会把
	// 这条 @ 立刻消费掉，mention 源必空（与 TestInboxThreeSources 同一理由）。
	session := mustConsoleSession(t, env, "收件箱提及场")
	seq, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "改动影响 B145", Mentions: []string{consoleMember}}, sessionFixtureOwner)
	if err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	var mention *proto.InboxItem
	for i := range items {
		if items[i].Origin == proto.InboxOriginMention {
			mention = &items[i]
		}
	}
	if mention == nil {
		t.Fatal("mention 源应含未消费提及")
	}
	if mention.RefID != strconv.FormatInt(seq, 10) {
		t.Fatalf("mention RefID 应为消息 seq 十进制串: %q", mention.RefID)
	}
	// 消费后消失
	if err := env.srv.rooms.Consume(seq, consoleMember); err != nil {
		t.Fatal(err)
	}
	if items := inboxItems(t, env); hasOrigin(items, proto.InboxOriginMention) {
		t.Fatalf("消费后 mention 应消失: %+v", items)
	}
}

func TestInboxRefIDShapes(t *testing.T) {
	env := newRoomsEnv(t)
	// B358.4 红窗改写：mention 源夹具从 project 群房间迁会话房间（旧房间只读，
	// 发送者用夹具群主，理由同 TestInboxThreeSources）；decision/ticket 夹具与
	// RefID 形状断言原样。
	session := mustConsoleSession(t, env, "收件箱引用形状场")
	card := seedCard(t, env, "卡I")
	dec, err := env.ledger.OpenDecision(card.ID, "一句话：X", nil, "coord")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "b", Mentions: []string{consoleMember}}, sessionFixtureOwner)
	if err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "tkt-42", TaskID: "t1", Kind: "ask",
		Request: json.RawMessage(`{"kind":"ask","question":"q"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	for _, it := range items {
		switch it.Origin {
		case proto.InboxOriginDecision:
			if it.RefID != strconv.FormatInt(dec.ID, 10) {
				t.Fatalf("decision RefID 应为十进制 id 串: %q", it.RefID)
			}
		case proto.InboxOriginTicket:
			if it.RefID != "tkt-42" {
				t.Fatalf("ticket RefID 应为 ticket id 原文: %q", it.RefID)
			}
		case proto.InboxOriginMention:
			if it.RefID != strconv.FormatInt(seq, 10) {
				t.Fatalf("mention RefID 应为消息 seq 十进制串: %q", it.RefID)
			}
		}
	}
}

func TestInboxGoldenKeyShapes(t *testing.T) {
	env := newRoomsEnv(t)
	// B358.4 红窗改写：mention 源夹具从 project 群房间迁会话房间（旧房间只读，
	// 发送者用夹具群主，理由同 TestInboxThreeSources）；金样本键集断言原样
	// （会话消息是无卡事件，mention 条 card_id 仍须省略）。
	session := mustConsoleSession(t, env, "收件箱键集场")
	card := seedCard(t, env, "卡J")
	if _, err := env.ledger.OpenDecision(card.ID, "一句话：X", nil, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "b", Mentions: []string{consoleMember}}, sessionFixtureOwner); err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "tkt-42", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// 金样本键集断言（contract_fixture_test.go 同款）：decision 带 card_id+payload；
	// ticket/mention omitempty 省略 card_id。
	for _, it := range inboxItems(t, env) {
		keys := itemKeys(t, it)
		if it.Origin == proto.InboxOriginDecision {
			if !keys["card_id"] || !keys["payload"] {
				t.Fatalf("decision 条目应带 card_id+payload: %v", keys)
			}
		} else if keys["card_id"] {
			t.Fatalf("空 card_id 必须省略: %v %+v", keys, it)
		}
	}
}

func TestInboxIntegrationSmoke(t *testing.T) {
	// 澄清三：httptest 一发穿 handler→真 SQLite→响应解码回 InboxItem/RoomSummary，
	// 与 testdata/RoomsFixture.json 孪生一致（键集形状，非逐字节值）。
	// B358.4 红窗改写：mention 源夹具从 project 群房间迁会话房间（旧房间只读，
	// 发送者用夹具群主，理由同 TestInboxThreeSources）；集成冒烟断言原样。
	env := newRoomsEnv(t)
	session := mustConsoleSession(t, env, "收件箱冒烟场")
	card := seedCard(t, env, "卡K")
	if _, err := env.ledger.OpenDecision(card.ID, "一句话：契约语义冲突", []string{"a", "b"}, "coord"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.rooms.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "b", Mentions: []string{consoleMember}}, sessionFixtureOwner); err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, env.st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingAnswer})
	if _, err := env.st.CreateTicket(&proto.Ticket{
		ID: "tkt-42", TaskID: "t1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	items := inboxItems(t, env)
	if len(items) < 3 {
		t.Fatalf("集成冒烟三源应齐: %+v", items)
	}
	for _, it := range items {
		keys := itemKeys(t, it)
		if !keys["origin"] || !keys["title"] || !keys["ref_id"] {
			t.Fatalf("InboxItem 基础键缺一: %v", keys)
		}
	}
	var rooms struct {
		Rooms []proto.RoomSummary `json:"rooms"`
	}
	if code := env.getJSON(t, "/api/rooms?limit=50", &rooms); code != 200 {
		t.Fatalf("GET /api/rooms: %d", code)
	}
	if len(rooms.Rooms) == 0 {
		t.Fatal("RoomSummary 列表应非空")
	}
}
