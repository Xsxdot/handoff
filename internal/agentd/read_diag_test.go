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
