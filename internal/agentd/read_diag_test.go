// read_diag_test.go 锁 B409.6 (S4-U6) 的读取诊断闭环行为：同一次同步读在
// HTTP 外围、collab 投影与 ledger 查询三层日志共享同一服务端生成的
// operation_id；结果分类（success_nonempty/success_empty/error/canceled）、
// 状态码/响应字节与取消阶段可从日志对账。全部经真实 HTTP 入口驱动。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// diagLogCapture 复用 roomsLogCapture 的形态：记录全部级别，供跨层关联断言。
type diagLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *diagLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *diagLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.records = append(c.records, r.Clone())
	c.mu.Unlock()
	return nil
}
func (c *diagLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *diagLogCapture) WithGroup(string) slog.Handler      { return c }

// attrValue 返回记录里指定键的值。
func attrValue(r slog.Record, key string) (slog.Value, bool) {
	var found slog.Value
	var ok bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = a.Value
			ok = true
			return false
		}
		return true
	})
	return found, ok
}

// recordAttrs 把一条记录的全部属性展开为 map。
func recordAttrs(r slog.Record) map[string]string {
	out := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

// allText 把每条记录渲染为文本（含消息与属性），供哨兵扫描类断言。
func (c *diagLogCapture) allText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, r := range c.records {
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.Key + "=" + a.Value.String())
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

// find 返回首条 Message 匹配的记录；不存在则 false。
func (c *diagLogCapture) find(msg string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

// operationIDs 返回全部记录里出现过的 operation_id 值集合。
func (c *diagLogCapture) operationIDs() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int{}
	for _, r := range c.records {
		if v, ok := attrValue(r, "operation_id"); ok {
			out[v.String()]++
		}
	}
	return out
}

// newDiagEnv 组装带日志捕获的房间/会话读测试环境（真 SQLite 账本 + collab 装配 +
// console 身份）。agentd 的 s.log 与 collab/ledger 使用的 slog.Default() 都指向
// capture，跨层关联断言才能在同一个流里对账。capture 由调用方持有以便断言。
func newDiagEnv(t *testing.T, capture *diagLogCapture) *ledgerEnv {
	t.Helper()
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ledgerPath := t.TempDir() + "/ledger.db"
	st, err := ledger.Open(ledgerPath)
	if err != nil {
		t.Fatalf("开账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedAgentdLedger(t, st, "bug")
	env := newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, ConsoleUser: "sycm"},
		slog.New(capture))
	env.srv.SetLedger(st)
	newManagerForServer(t, env.srv, nil)
	SetupAutomationForTest(t, env.srv, st)
	return &ledgerEnv{testAgentdEnv: env, ledger: st, ledgerPath: ledgerPath}
}

// seedSessionRoom 建一场会话并落 count 条 user 消息，返回房间 id（session:<n>）。
func seedSessionRoom(t *testing.T, st *ledger.Store, count int) string {
	t.Helper()
	session, err := st.CreateSession("诊断金样", "user:sycm", "user:sycm")
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	for i := 0; i < count; i++ {
		if _, err := st.RecordRoomMessage("", proto.RoomMessage{
			Kind: proto.RoomMsgUser, Room: session.ID, Body: "诊断消息",
		}, "user:sycm"); err != nil {
			t.Fatalf("落消息 %d: %v", i, err)
		}
	}
	return session.ID
}

func diagGet(t *testing.T, env *ledgerEnv, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// TestRoomMessagesReadLogsCorrelatedOperationID 锁跨层关联：同一次
// GET /api/rooms/{id}/messages 在 HTTP 外围完成日志、collab 投影完成日志与
// ledger 查询完成日志里共享同一 operation_id，且本次请求只产生一个 id。
func TestRoomMessagesReadLogsCorrelatedOperationID(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)
	roomID := seedSessionRoom(t, env.ledger, 3)

	code, body := diagGet(t, env, "/api/rooms/"+roomID+"/messages?limit=10")
	if code != http.StatusOK {
		t.Fatalf("房间历史读取应 200: %d %s", code, body)
	}
	var payload struct {
		Messages []proto.LedgerEvent `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("解码响应: %v", err)
	}
	if len(payload.Messages) != 3 {
		t.Fatalf("应读回 3 条消息: %d", len(payload.Messages))
	}

	ids := capture.operationIDs()
	if len(ids) == 0 {
		t.Fatalf("读取链路日志缺少 operation_id：\n%s", capture.allText())
	}
	if len(ids) != 1 {
		t.Fatalf("同一次读取应只有一个 operation_id，得到 %v", ids)
	}
	opID := ""
	for id := range ids {
		opID = id
	}
	if !strings.HasPrefix(opID, "op-") {
		t.Fatalf("operation_id 应带 op- 前缀: %q", opID)
	}
	for _, msg := range []string{
		"房间历史 HTTP 请求完成", // HTTP 外围
		"房间历史读取完成",       // collab 投影
		"房间历史账本查询完成",      // ledger 查询
	} {
		rec, ok := capture.find(msg)
		if !ok {
			t.Fatalf("缺少日志 %q：\n%s", msg, capture.allText())
		}
		if v, ok := attrValue(rec, "operation_id"); !ok || v.String() != opID {
			t.Fatalf("日志 %q 缺 operation_id=%s：%s", msg, opID, capture.allText())
		}
	}
}

// mustCardDiag 建一张卡作 cards 读金样。
func mustCardDiag(t *testing.T, env *ledgerEnv) ledger.Card {
	t.Helper()
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: "诊断卡", Project: "handoff", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return card
}

// —— B409.6 Step 5：HTTP 外围收口（状态码/响应字节/结果分类/取消） ——

// TestReadDiagCompletionHasStatusBytesAndOutcome 锁外围收口行：真实写出后的
// status_code、response_bytes、outcome 与 elapsed 在同一 operation_id 下可查。
func TestReadDiagCompletionHasStatusBytesAndOutcome(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)
	roomID := seedSessionRoom(t, env.ledger, 2)

	code, body := diagGet(t, env, "/api/rooms/"+roomID+"/messages?limit=10")
	if code != http.StatusOK {
		t.Fatalf("应 200: %d", code)
	}
	rec, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺外围收口日志：\n%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["status_code"] != "200" {
		t.Fatalf("status_code=%q want 200", attrs["status_code"])
	}
	if got, err := strconv.Atoi(attrs["response_bytes"]); err != nil || got <= 0 || got != len(body) {
		t.Fatalf("response_bytes=%q 应等于实际响应体 %d", attrs["response_bytes"], len(body))
	}
	if attrs["outcome"] != "success_nonempty" {
		t.Fatalf("outcome=%q want success_nonempty", attrs["outcome"])
	}
	if attrs["route"] != "room_messages" {
		t.Fatalf("route=%q want room_messages（受限标签，不是原始 URL）", attrs["route"])
	}
	if _, ok := attrs["elapsed_ns"]; !ok {
		t.Fatalf("收口缺 elapsed_ns: %v", attrs)
	}
}

// TestReadDiagEmptyReadClassifiedSuccessEmpty 锁合法空读：200 + 0 行 = 
// success_empty，不伪造错误也不冒充非空。
func TestReadDiagEmptyReadClassifiedSuccessEmpty(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)
	roomID := seedSessionRoom(t, env.ledger, 0)

	code, body := diagGet(t, env, "/api/rooms/"+roomID+"/messages?limit=10")
	if code != http.StatusOK {
		t.Fatalf("空房间应 200: %d %s", code, body)
	}
	rec, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺外围收口日志：\n%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["outcome"] != "success_empty" || attrs["rows"] != "0" {
		t.Fatalf("合法空读分类错误: %v", attrs)
	}
}

// TestReadDiagHTTPFailureOutcomeNotSuccess 锁失败收口：DB 读失败 → 外围 500、
// outcome=error、error_class 可查，与 operation_id 对齐。
func TestReadDiagHTTPFailureOutcomeNotSuccess(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)
	roomID := seedSessionRoom(t, env.ledger, 1)
	if err := env.ledger.Close(); err != nil {
		t.Fatal(err)
	}

	code, _ := diagGet(t, env, "/api/rooms/"+roomID+"/messages?limit=10")
	if code != http.StatusInternalServerError {
		t.Fatalf("DB 失败应 500: %d", code)
	}
	rec, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺外围收口日志：\n%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["status_code"] != "500" || attrs["outcome"] != "error" {
		t.Fatalf("失败收口分类错误: %v", attrs)
	}
	if attrs["error_class"] == "" {
		t.Fatalf("失败收口缺 error_class: %v", attrs)
	}
}

// TestReadDiagCanceledOutcomeWithReason 锁取消收口：请求 context 取消 →
// outcome=canceled 且 cancel_reason 区分 context_canceled；不得记成泛化
// error 也不得谎报成功。
func TestReadDiagCanceledOutcomeWithReason(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)
	roomID := seedSessionRoom(t, env.ledger, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/rooms/"+roomID+"/messages?limit=10", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("已取消请求不应得到正常响应")
	}
	// 客户端断连下服务端可能已写部分响应；直接经 mux 调一次以确保收口稳定。
	muxReq := httptest.NewRequest(http.MethodGet, "/api/rooms/"+roomID+"/messages?limit=10", nil)
	muxReq.Host = "127.0.0.1" // httptest 默认 example.com 会被 hostGuard 拒掉
	muxReq.Header.Set("Authorization", "Bearer "+env.token)
	muxReq = muxReq.WithContext(ctx)
	recorder := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(recorder, muxReq)

	rec, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺外围收口日志：\n%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["outcome"] != "canceled" {
		t.Fatalf("outcome=%q want canceled: %v", attrs["outcome"], attrs)
	}
	if attrs["cancel_reason"] != "context_canceled" {
		t.Fatalf("cancel_reason=%q want context_canceled", attrs["cancel_reason"])
	}
}

// TestReadDiagRecorderPreservesFlusher 锁 recorder 接口面：包装不得吞掉
// Flusher 行为（既有 SSE/flush 语义依赖它）。
func TestReadDiagRecorderPreservesFlusher(t *testing.T) {
	rec := &readResponseRecorder{ResponseWriter: httptest.NewRecorder()}
	if _, ok := any(rec).(http.Flusher); !ok {
		t.Fatal("readResponseRecorder 必须实现 http.Flusher")
	}
	rec.Flush() // 不应 panic
}

// TestReadDiagCardsSeparatesLocalAndRemote 锁 cards 双面记账：本地主数据
// success 分类与远端摘要状态分开记（HTTP 200 下两者互不覆盖）。
func TestReadDiagCardsSeparatesLocalAndRemote(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)
	mustCardDiag(t, env)

	code, body := diagGet(t, env, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("cards 应 200: %d %s", code, body)
	}
	rec, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺外围收口日志：\n%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["route"] != "cards_list" || attrs["outcome"] != "success_nonempty" {
		t.Fatalf("本地 cards 收口错误: %v", attrs)
	}
	// 无配置 target 是合法 latest 空摘要：远端状态单独记录，不与本地混叠。
	if attrs["remote_status"] != "latest" {
		t.Fatalf("remote_status=%q want latest（无 target 合法空）", attrs["remote_status"])
	}
}

// TestCardsReadLogsCorrelatedOperationID 锁 cards 路由的跨层关联（review
// Important-1）：同一次 GET /api/cards 在 HTTP 外围收口行、ledger 列卡 SQL
// 阶段行与未决工单投影聚合 SQL 阶段行共享同一 operation_id。cards handler
// 必须把 r.Context() 传进 ListCardsContext / OpenTicketCountsContext，否则
// 这两行退化为 Background context、丢 id，与收口行拼不回同一次读取。
func TestCardsReadLogsCorrelatedOperationID(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)
	mustCardDiag(t, env)

	code, body := diagGet(t, env, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("cards 应 200: %d %s", code, body)
	}
	wrap, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺外围收口日志：\n%s", capture.allText())
	}
	wrapID, ok := attrValue(wrap, "operation_id")
	if !ok || wrapID.String() == "" {
		t.Fatalf("收口行缺 operation_id：%s", capture.allText())
	}
	ids := capture.operationIDs()
	if len(ids) != 1 {
		t.Fatalf("同一次读取应只有一个 operation_id，得到 %v", ids)
	}
	for _, msg := range []string{
		"列卡完成",         // ledger 列卡 SQL 阶段
		"聚合未决工单投影完成", // 未决工单聚合 SQL 阶段
	} {
		rec, found := capture.find(msg)
		if !found {
			t.Fatalf("缺少日志 %q：\n%s", msg, capture.allText())
		}
		if v, ok := attrValue(rec, "operation_id"); !ok || v.String() != wrapID.String() {
			t.Fatalf("日志 %q 缺 operation_id=%s：%s", msg, wrapID.String(), capture.allText())
		}
	}
}
