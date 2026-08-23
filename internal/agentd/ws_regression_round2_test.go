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
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

const wsTestToken = "ws-test-token"

// wsTestEnv 是 WS 推流测试环境：真实 store + 真实 Server + httptest。
type wsTestEnv struct {
	srv  *Server
	ts   *httptest.Server
	st   *store.Store
	logs *strings.Builder
	mu   sync.Mutex
	// truncationDiagnosed 接收截断诊断完成信号；缓冲避免服务端诊断回调阻塞。
	truncationDiagnosed chan string
	// sockBuf>0 时钉住两端 socket 缓冲（服务端发送 / 客户端接收），
	// 让「对端不读」类用例的 TCP 背压不再取决于运行机器的默认值。
	sockBuf int
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

// newWSTestEnv 组装 WS 测试环境并注册清理（socket 缓冲用系统默认）。
func newWSTestEnv(t *testing.T) *wsTestEnv {
	t.Helper()
	return newWSTestEnvWithSockBuf(t, 0)
}

// newWSTestEnvWithSockBuf 同 newWSTestEnv，但把服务端**发送**缓冲钉成 sockBuf
// 字节（0=系统默认），并让 dialWS 把客户端**接收**缓冲钉成同一个值。
//
// 为什么要能钉住：只有「对端不读」类用例才需要它。那类用例的前提是写循环真的
// 被 TCP 背压挡住，而挡不挡得住取决于内核缓冲能吞下多少——那是机器属性，不是
// 被测对象的属性。不钉住，判据就悬在运行机器的 SO_SNDBUF/SO_RCVBUF 默认值上。
func newWSTestEnvWithSockBuf(t *testing.T, sockBuf int) *wsTestEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	env := &wsTestEnv{
		st:                  st,
		logs:                &strings.Builder{},
		truncationDiagnosed: make(chan string, 4),
		sockBuf:             sockBuf,
	}
	logger := slog.New(slog.NewTextHandler(env, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &config.Config{Token: wsTestToken, DataDir: t.TempDir()}
	env.srv = NewServer(cfg, st, logger)
	env.srv.onTruncationDiagnosed = func(verdict string) {
		env.truncationDiagnosed <- verdict
	}
	if sockBuf <= 0 {
		env.ts = httptest.NewServer(env.srv.Handler())
	} else {
		// 换掉 httptest 自带的 listener：accept 出来的每条连接都在交给 http.Server
		// 之前把发送缓冲调小。SetWriteBuffer 是 net 包的可移植封装（底层 SO_SNDBUF），
		// 不用 syscall——GOOS=windows go vet ./... 连测试文件一起看，platform split
		// 会在那道门上现形。
		ts := httptest.NewUnstartedServer(env.srv.Handler())
		ts.Listener = &sockBufListener{Listener: ts.Listener, writeBuf: sockBuf}
		ts.Start()
		env.ts = ts
	}
	t.Cleanup(env.ts.Close)
	return env
}

// sockBufListener 给每条 accept 到的 TCP 连接钉住发送缓冲。
type sockBufListener struct {
	net.Listener
	writeBuf int
}

func (l *sockBufListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		// 设不上就让它保持默认——退化成「缓冲未钉住」，而依赖它的用例会因此
		// 失败并报出原因，不会静默假绿
		_ = tc.SetWriteBuffer(l.writeBuf)
	}
	return c, nil
}

// dialWS 建立一条 WS 事件流连接。env.sockBuf > 0 时同时钉住客户端接收缓冲。
func (e *wsTestEnv) dialWS(t *testing.T, taskID string, fromSeq int64) *websocket.Conn {
	t.Helper()
	url := strings.Replace(e.ts.URL, "http://", "ws://", 1) +
		"/ws/events?task=" + taskID + "&from_seq=" + strconv.FormatInt(fromSeq, 10)
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + wsTestToken}},
	}
	if e.sockBuf > 0 {
		buf := e.sockBuf
		opts.HTTPClient = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				if tc, ok := c.(*net.TCPConn); ok {
					_ = tc.SetReadBuffer(buf)
				}
				return c, nil
			},
		}}
	}
	conn, _, err := websocket.Dial(context.Background(), url, opts)
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
//
// 本用例的前提是「写循环真的被 TCP 背压挡住」，而挡不挡得住取决于内核缓冲能吞下
// 多少字节——**那是运行机器的属性，不是被测对象的属性**。用系统默认值时这条判据
// 是悬空的：macOS 默认约 800KB，本用例发的 3.2MB 稳稳撑爆它；而 Linux runner 上
// 自动调优后的收发缓冲可以超过 3.2MB，服务端一次也不阻塞就把 400 条全写完，
// live 永远到不了 liveLimit，用例读满 400 条后干等到超时——2026-08-20 main 上那次
// 偶发红就是这个形状（同一提交在 release 的 verify 里却是绿的）。
// 在 macOS 上把两端缓冲调到 4MB 可以稳定复现。
//
// 所以这里把两端 socket 缓冲钉成 8KB：能吞下的字节数从「机器说了算」变成「用例
// 说了算」，3.2MB 相对它有两个数量级的余量，任何机器上结论都一样。
// 早前那次把超时从 10s 放宽到 3 倍（B125）治的是症状——服务端已经无事可做时，
// 等再久也等不到断开。
func TestWSLiveBufferBounded(t *testing.T) {
	env := newWSTestEnvWithSockBuf(t, 8<<10)
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
	ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 10*time.Second))
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
	ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 30*time.Second))
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
// B125：本用例的等待期限走 wsDeadline 而非写死的 10s——它等的是建连 + 重放 5 条 +
// 一条实时事件，整包并行时会与其他用例争 goroutine。这是负载缓解不是根治，
// 若仍偶发翻红，按「WS 用例分包」处理，不要继续调倍数。
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
	ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 10*time.Second))
	defer cancel()

	// 连接后再产一条实时事件（seq=21）：它高于 storeMax，正是修复前掩盖缺口的那类事件
	fresh := env.appendAndPublish(t, taskID, "新事件")
	waitEventSeq(t, ctx, conn, fresh.Seq)

	select {
	case verdict := <-env.truncationDiagnosed:
		if verdict != "warned" {
			t.Fatalf("截断诊断跑完了但判定是 %q，期望 warned；日志尾部：%s", verdict, tailStr(env.logged(), 600))
		}
	case <-ctx.Done():
		t.Fatalf("等截断诊断完成超时；日志尾部：%s", tailStr(env.logged(), 600))
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 10*time.Second))
	defer cancel()
	// 等服务端补发出重放首条（seq=2：本任务第一个事件）：收到它即保证服务端已
	// 越过 storeMax 捕获点（补发写入发生在捕获**之后**）。此后追加的 fresh 不会
	// 混入缺口统计——否则负载下 fresh 先落库会让 storeMax=41、gap_total 变 16，
	// 与「缺口=未重放的 15 条」的断言不稳（偶发红）。
	waitEventSeq(t, ctx, conn, 2)
	fresh := env.appendAndPublish(t, taskID, "新事件")
	waitEventSeq(t, ctx, conn, fresh.Seq)

	select {
	case verdict := <-env.truncationDiagnosed:
		if verdict != "warned" {
			t.Fatalf("截断诊断跑完了但判定是 %q，期望 warned；日志尾部：%s", verdict, tailStr(env.logged(), 600))
		}
	case <-ctx.Done():
		t.Fatalf("等截断诊断完成超时；日志尾部：%s", tailStr(env.logged(), 600))
	}
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

// wsDeadline 返回 WS 用例的等待期限：基准值放宽到 3 倍，并在 -timeout 余额
// 不足时收窄，但绝不低于 base。
//
// 为什么不是写死的 10s（B125）：本文件的用例等的是「建连 + 重放 N 条 + 一条
// 实时事件」，这条链路在整包并行下要与其他用例争 goroutine。实测单独跑 3 次
// 全过、全量第一遍撞满 10.01s——它是负载的函数，写死的数字治不了。
//
// 上限 3 倍 base 的理由：真挂住的用例不该拖满整个 -timeout，否则「哪个用例挂了」
// 只能靠猜。下限 base 的理由：-timeout 很短时若按余额收窄到 base 以下，会比
// 改动前更容易翻红。
//
// 局限：这是**负载缓解，不是根治**。根治要把 WS 用例与重负载用例隔开（分包或
// t.Parallel() 分组）。若本文件的用例仍偶发翻红，按分包处理，不要继续调这个倍数。
func wsDeadline(t *testing.T, base time.Duration) time.Duration {
	t.Helper()
	limit := 3 * base
	if dl, ok := t.Deadline(); ok {
		if quarter := time.Until(dl) / 4; quarter > base && quarter < limit {
			limit = quarter
		}
	}
	return limit
}

// TestWSDeadlineStaysWithinBounds 钉住 wsDeadline 的两条不变量（B125）：
// 结果永不低于 base（否则比修改前更容易翻红），也永不超过 base 的 3 倍
// （否则一个真挂住的用例会拖满整个 -timeout，把「哪个用例挂了」变成猜）。
func TestWSDeadlineStaysWithinBounds(t *testing.T) {
	const base = 10 * time.Second
	got := wsDeadline(t, base)
	if got < base {
		t.Errorf("wsDeadline 低于 base：got=%v base=%v", got, base)
	}
	if got > 3*base {
		t.Errorf("wsDeadline 超过 3 倍 base：got=%v base=%v", got, base)
	}
}

// TestWSClosesNormallyOnArchive 验证订阅被 hub 关闭（任务归档）时，
// 服务端以 StatusNormalClosure 收尾，而不是把连接晾着。
//
// 缺陷形态：transit 只改状态不发事件，跟随端拿不到任何「没有下文了」的信号，
// 会一直挂到空闲超时——那是一个会把协调者引向「agentd 失联」的假线索。
func TestWSClosesNormallyOnArchive(t *testing.T) {
	env := newWSTestEnv(t)
	const id = "task-archive-ws"
	env.seedTask(t, id)
	conn := env.dialWS(t, id, 0)

	// 等订阅真正建立：websocket.Dial 在 Accept 后即返回，服务端的 Subscribe
	// 在其后异步执行（本文件 :164 已记过这条时序）
	waitWatchers(t, env.srv.hub, id, 1)
	env.srv.hub.CloseTask(id)

	for {
		_, _, err := conn.Read(context.Background())
		if err == nil {
			continue // 归档前排队的事件，读干净
		}
		if got := websocket.CloseStatus(err); got != websocket.StatusNormalClosure {
			t.Fatalf("归档关闭码 = %v, want %v（err=%v）",
				got, websocket.StatusNormalClosure, err)
		}
		return
	}
}

// waitWatchers 轮询等待订阅数达到期望值（订阅是异步建立的）。
func waitWatchers(t *testing.T, hub *Hub, taskID string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Watchers(taskID) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 watchers=%d 超时，当前 %d", want, hub.Watchers(taskID))
}
