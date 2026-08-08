// 本文件是第二轮外部代码审阅确认的 WS 推流缺陷回归测试
// （docs/superpowers/reviews/2026-08-08-mvp-code-review-round2.md 第三节）。
//
// 职责：
//   - 锁定 N-1（待写缓冲有上限，对端不读不得撑爆 agentd 内存）、
//     N-2（乱序迟到事件不得被静默丢弃）、N-3（截断诊断按连续性而非最大值判定）
//
// 边界：
//   - 白盒测试（package agentd）：需要注入小阈值复现边界路径，故直接读写 Server
//     的 replayLimit/liveLimit 字段，不经 NewServer 的生产默认值
//   - 只测服务端推流语义；客户端 cursor 行为在 internal/client 测
package agentd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

const wsTestToken = "ws-test-token"

// wsTestEnv 是 WS 推流测试环境：真实 store + 真实 Server + httptest。
type wsTestEnv struct {
	srv  *Server
	ts   *httptest.Server
	st   *store.Store
	logs *strings.Builder
	mu   sync.Mutex
}

// logged 返回迄今为止的服务端日志文本（供「告警是否触发」类断言）。
func (e *wsTestEnv) logged() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logs.String()
}

func (e *wsTestEnv) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logs.Write(p)
}

// newWSTestEnv 组装 WS 测试环境并注册清理。
func newWSTestEnv(t *testing.T) *wsTestEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	env := &wsTestEnv{st: st, logs: &strings.Builder{}}
	logger := slog.New(slog.NewTextHandler(env, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &config.Config{Token: wsTestToken, DataDir: t.TempDir()}
	env.srv = NewServer(cfg, st, logger)
	env.ts = httptest.NewServer(env.srv.Handler())
	t.Cleanup(env.ts.Close)
	return env
}

// dialWS 建立一条 WS 事件流连接。
func (e *wsTestEnv) dialWS(t *testing.T, taskID string, fromSeq int64) *websocket.Conn {
	t.Helper()
	url := strings.Replace(e.ts.URL, "http://", "ws://", 1) +
		"/ws/events?task=" + taskID + "&from_seq=" + strconv.FormatInt(fromSeq, 10)
	conn, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + wsTestToken}},
	})
	if err != nil {
		t.Fatalf("WS 拨号: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// seedTask 建一个 running 任务，供事件挂载。
func (e *wsTestEnv) seedTask(t *testing.T, id string) {
	t.Helper()
	createRunningTask(t, e.st, id)
}

// appendAndPublish 落库一条 progress 事件并广播（与生产同序）。
func (e *wsTestEnv) appendAndPublish(t *testing.T, taskID, text string) proto.Event {
	t.Helper()
	ev, err := e.st.AppendEvent(taskID, proto.EventTypeProgress, map[string]string{"text": text})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	e.srv.hub.Publish(ev)
	return ev
}

// TestWSLiveBufferBounded 验证实时待写缓冲有上限（N-1）。
//
// 上一轮修 P0-1 时，排空器把订阅通道的事件无上限地收进内存切片。对端「连着不读」
// （合盖的笔记本、黑洞路由——正是本产品的头号场景）时写循环阻塞任意久，缓冲随
// 事件流无限增长：探针实测一个不读的客户端 + 2 万条事件即让 agentd 堆增长 88MB，
// 且连接不断则永不回收。
//
// 修复后越限即断开：事件都已落库，客户端凭 cursor 重连可完整补拉，断开是无损的。
func TestWSLiveBufferBounded(t *testing.T) {
	env := newWSTestEnv(t)
	env.srv.liveLimit = 32 // 注入小阈值，免造几万条事件
	const taskID = "task-ws-overflow"
	env.seedTask(t, taskID)

	conn := env.dialWS(t, taskID, 0)
	// 故意不读：让服务端写循环因 TCP 背压阻塞，事件在排空器侧堆积
	payload := strings.Repeat("x", 8<<10)
	for range 400 {
		env.appendAndPublish(t, taskID, payload)
	}

	// 越限后服务端应主动断开；客户端表现为读到错误而非无限等待
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var readErr error
	for readErr == nil {
		_, _, readErr = conn.Read(ctx)
	}
	if ctx.Err() != nil {
		t.Fatalf("对端不读时服务端未断开连接（缓冲无上限，内存会随事件流无限增长）")
	}
	if !strings.Contains(env.logged(), "待写缓冲越限") {
		t.Errorf("越限断开应有可观测告警，实际日志未出现；日志尾部：%s",
			tailStr(env.logged(), 400))
	}
}

// TestWSOutOfOrderPublishNotDropped 验证乱序迟到的事件不被静默丢弃（N-2）。
//
// 落库与广播是两步，watchdog 的 stalled 扫描与 mediate 的事件中介并发发布时，
// 可能出现「seq 大的先广播、seq 小的后广播」。修复前 writeLiveBatch 只按
// `seq <= lastWrittenSeq` 跳过，于是后到的低 seq 事件被永久丢弃且客户端 cursor
// 已越过它——牺牲品恰好是兜底唤醒的 stalled。
//
// 修复后服务端断开连接，客户端凭 cursor 重连时由重放按 seq 序完整补齐。
func TestWSOutOfOrderPublishNotDropped(t *testing.T) {
	env := newWSTestEnv(t)
	const taskID = "task-ws-reorder"
	env.seedTask(t, taskID)

	conn := env.dialWS(t, taskID, 0)
	// 30s 而非 10s：同一 ctx 要先后完成多次往返（探活/收 high/探测断开），
	// 全量并行负载下首步就可能耗去大半预算，10s 会让探测在超时上误报
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 探活往返：Append+Publish 一条 progress 并等它到客户端。为什么要这一步——
	// websocket.Dial 在 Accept 后即返回，而服务端的 Subscribe 在其后异步执行；
	// 若两步 Append+Publish 抢在订阅建立前，事件会被 hub 按「无订阅者」丢弃、
	// 再由重放按序补出，此时 low 重放后成为「真重复」（seq<=maxReplayed）被跳过，
	// 连接不会因乱序断开——测试将永久超时。探活往返保证订阅/排空/写循环全部就绪。
	probe, err := env.st.AppendEvent(taskID, proto.EventTypeProgress, map[string]string{"text": "探活"})
	if err != nil {
		t.Fatalf("AppendEvent probe: %v", err)
	}
	env.srv.hub.Publish(probe)
	waitEventSeq(t, ctx, conn, probe.Seq)

	// 先落库两条，再反序广播（复现并发发布的交错）
	low, err := env.st.AppendEvent(taskID, proto.EventTypeStalled, map[string]string{"text": "低 seq"})
	if err != nil {
		t.Fatalf("AppendEvent low: %v", err)
	}
	high, err := env.st.AppendEvent(taskID, proto.EventTypeQuestion, map[string]string{"text": "高 seq"})
	if err != nil {
		t.Fatalf("AppendEvent high: %v", err)
	}
	env.srv.hub.Publish(high)
	// 等高 seq 落到客户端，确保 lastWrittenSeq 已推进到 high
	waitEventSeq(t, ctx, conn, high.Seq)
	env.srv.hub.Publish(low)

	// 服务端必须断开而不是静默吞掉低 seq 事件
	var readErr error
	for readErr == nil {
		_, _, readErr = conn.Read(ctx)
	}
	if ctx.Err() != nil {
		t.Fatalf("乱序迟到事件被静默丢弃：连接仍在，seq=%d 永远送不到", low.Seq)
	}

	// 重连（cursor 仍在 low 之前）应能拿到被跳过的那条
	conn2 := env.dialWS(t, taskID, low.Seq-1)
	got := waitEventSeq(t, ctx, conn2, low.Seq)
	if got.Type != proto.EventTypeStalled {
		t.Errorf("重连补齐的事件类型错误：%s", got.Type)
	}
}

// TestWSTruncationWarnsOnRealGap 验证重放截断留下的真实缺口会触发告警（N-3）。
//
// 修复前诊断条件是 `storeMax > lastWrittenSeq`——比的是最大值。任何一条 seq 高于
// storeMax 的实时事件都会把 lastWrittenSeq 顶过缺口，于是缺口真实存在而告警被
// 抑制。修复后按「客户端已连续收到的 seq」判定，中段缺口无法被后续事件掩盖。
func TestWSTruncationWarnsOnRealGap(t *testing.T) {
	env := newWSTestEnv(t)
	env.srv.replayLimit = 5 // 注入小阈值，制造重放截断
	const taskID = "task-ws-truncate"
	env.seedTask(t, taskID)

	// 连接前先落库 20 条：重放只覆盖最旧 5 条，(5, 20] 是缺口
	for range 20 {
		if _, err := env.st.AppendEvent(taskID, proto.EventTypeProgress, map[string]string{"text": "历史"}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	conn := env.dialWS(t, taskID, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 连接后再产一条实时事件（seq=21）：它高于 storeMax，正是修复前掩盖缺口的那类事件
	fresh := env.appendAndPublish(t, taskID, "新事件")
	waitEventSeq(t, ctx, conn, fresh.Seq)

	if !strings.Contains(env.logged(), "补发窗口截断且缺口未由实时流补齐") {
		t.Errorf("重放截断留下真实缺口 (5, 20] 却未告警——诊断被高 seq 实时事件掩盖；日志尾部：%s",
			tailStr(env.logged(), 600))
	}
}

// TestWSTruncationGapCountedPerTask 验证缺口规模按「本任务的事件条数」核对，
// 而不是按 seq 跨度。
//
// seq 由 AUTOINCREMENT **全局**分配：其他任务在中间落库会让本任务的 seq 出现
// 空洞。任何以 seq 连续性/跨度为依据的诊断，在多任务部署下都会把别人的 seq 算成
// 自己的缺口——要么虚报规模，要么每连必告警变成噪音。
//
// 这里 20 条本任务事件与 20 条其他任务事件交错落库，重放上限 5：
// 缺口是本任务未被重放的 15 条，而 seq 跨度约 30。
func TestWSTruncationGapCountedPerTask(t *testing.T) {
	env := newWSTestEnv(t)
	env.srv.replayLimit = 5
	const taskID = "task-ws-gapcount"
	const other = "task-ws-other"
	env.seedTask(t, taskID)
	env.seedTask(t, other)

	// 交错落库：本任务 20 条，其他任务 20 条，seq 相互穿插
	for range 20 {
		if _, err := env.st.AppendEvent(other, proto.EventTypeProgress, map[string]string{"text": "别的任务"}); err != nil {
			t.Fatalf("AppendEvent other: %v", err)
		}
		if _, err := env.st.AppendEvent(taskID, proto.EventTypeProgress, map[string]string{"text": "本任务"}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	conn := env.dialWS(t, taskID, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fresh := env.appendAndPublish(t, taskID, "新事件")
	waitEventSeq(t, ctx, conn, fresh.Seq)

	logs := env.logged()
	if !strings.Contains(logs, "补发窗口截断且缺口未由实时流补齐") {
		t.Fatalf("重放被截断且缺口未补齐，应告警；日志尾部：%s", tailStr(logs, 600))
	}
	// 缺口 = 本任务 20 条中未被重放的 15 条；若按 seq 跨度算会得到约 30
	if !strings.Contains(logs, "gap_total=15") {
		t.Errorf("缺口规模应按本任务事件条数核对（期望 gap_total=15），实际日志：%s",
			tailStr(logs, 600))
	}
}

// waitEventSeq 读 WS 直到收到指定 seq 的事件。
func waitEventSeq(t *testing.T, ctx context.Context, conn *websocket.Conn, want int64) proto.Event {
	t.Helper()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("等待 seq=%d 时读失败: %v", want, err)
		}
		var ev proto.Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("解析事件: %v", err)
		}
		if ev.Seq == want {
			return ev
		}
	}
}

// tailStr 取字符串末尾 n 个字节（日志断言失败时输出上下文用）。
func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
