// B392 真实 Core 竖切（行为验收集合）：用独立真实 *mobilecore.Core + 本地 fake
// agentd 夹具穿过绑定导出面（Pair/SwitchMachine/SessionCookie/Close），证明生产
// 会话链不是 fake 假绿。
//
// 边界：
//   - 本文件只在测试内构造独立真实 Core，经 swapCore/swapSessions 注入导出面
//     （breakdown P3=A：独立 Core 只用于行为竖切，不碰包级 liveCore）。
//   - 默认生产装配（不 swap，直用 liveCore）的承重竖切见 default_slice_test.go。
//   - 网络对端可 fake，Core 与适配器是真的；夹具按机器/ticket 用真 Set-Cookie
//     签发可区分的 handoff_session，不预置与响应无关的假值。
//   - 禁止生产代码改动；只 import 测试允许的 internal/client、internal/proto。
package bind

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/mobilecore"
	"github.com/Xsxdot/handoff/internal/proto"
)

// fakeAgentd 是 B392 真实 Core 竖切用的对端夹具（独立实现，不复用
// internal/mobilecore 同包未导出 helper）：覆盖 /api/status（可达性探测）、
// /api/auth/tickets（签发一次性 ticket）、/console（302 + Set-Cookie）与
// /last-cookie（回读最后签发的值，供跨进程的默认生产竖切断言）。
// 每台机器一个实例；cookie 值 = "sess-" + ticket，ticket 含设备名与递增序号，
// 故 A/B 值天然可区分，切机/重兑换断言据此。
type fakeAgentd struct {
	machine    string
	mu         sync.Mutex
	tickets    int
	exchanges  int
	lastValue  string
	omitCookie bool
	server     *httptest.Server

	entered chan struct{} // gateConsole 设置：/console 进入时关闭
	release chan struct{} // gateConsole 设置：/console 等它关闭再回响应
}

func newFakeAgentd(machine string) *fakeAgentd {
	f := &fakeAgentd{machine: machine}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/auth/tickets", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DeviceName string `json:"device_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.tickets++
		seq := f.tickets
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proto.AuthTicketResp{
			URL:       "http://" + r.Host + "/console?ticket=" + url.QueryEscape(body.DeviceName) + "-" + strconv.Itoa(seq),
			ExpiresAt: time.Now().Add(time.Minute),
		})
	})
	mux.HandleFunc("/console", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.exchanges++
		value := "sess-" + r.URL.Query().Get("ticket")
		if !f.omitCookie {
			f.lastValue = value
		}
		omit := f.omitCookie
		entered, release := f.entered, f.release
		f.entered, f.release = nil, nil // 只拦一次
		f.mu.Unlock()
		if entered != nil {
			close(entered)
			<-release
		}
		if !omit {
			http.SetCookie(w, &http.Cookie{
				Name:     "handoff_session",
				Value:    value,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/last-cookie", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		v := f.lastValue
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(v))
	})
	f.server = httptest.NewServer(mux)
	return f
}

// setOmitCookie 让 /console 只回 302 不签 Set-Cookie（兑换失败反例）。
func (f *fakeAgentd) setOmitCookie(v bool) {
	f.mu.Lock()
	f.omitCookie = v
	f.mu.Unlock()
}

// gateConsole 让下一次 /console 请求在回 302 前先关闭 entered、再等 release 关闭。
// 返回本次的 entered/release；只拦一次（读到即清空），后续兑换不被拦。
func (f *fakeAgentd) gateConsole() (entered, release chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entered = make(chan struct{})
	f.release = make(chan struct{})
	return f.entered, f.release
}

// stats 返回已签发 ticket 数、已兑换次数与最后一次签发的 cookie 值。
func (f *fakeAgentd) stats() (tickets, exchanges int, lastValue string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tickets, f.exchanges, f.lastValue
}

func (f *fakeAgentd) close() { f.server.Close() }

// realChain 是「独立真实 Core + 本地夹具 + 注入绑定导出面」的组合。
// swapCore/swapSessions 把导出面切到独立 Core，测试结束自动还原。
type realChain struct {
	core     *mobilecore.Core
	fixtures map[string]*fakeAgentd
}

// newRealChain 起独立真实 Core 并把导出面切到它。machines 每项新建一个夹具；
// 未列入 machines 的机器名由 dial 返回错误 → 核标离线（用于离线反例）。
func newRealChain(t *testing.T, machines ...string) *realChain {
	t.Helper()
	fixtures := make(map[string]*fakeAgentd, len(machines))
	for _, m := range machines {
		fixtures[m] = newFakeAgentd(m)
	}
	dial := func(_ context.Context, _ proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error) {
		f, ok := fixtures[m.Name]
		if !ok {
			return nil, nil, fmt.Errorf("夹具没有机器 %q", m.Name)
		}
		return client.New(f.server.URL, m.Token), func() {}, nil
	}
	core := mobilecore.New(dial, slog.Default())
	restoreCore := swapCore(core)
	restoreSessions := swapSessions(newCoreSessions(core))
	t.Cleanup(func() {
		restoreSessions()
		restoreCore()
		_ = core.Close()
		for _, f := range fixtures {
			f.close()
		}
	})
	return &realChain{core: core, fixtures: fixtures}
}

// pair 用直连形态 bundle 调导出面 Pair；未建夹具的机器托管占位 Addr，
// dial 会返回错误 → 核标离线（部分 bundle，不整单失败）。
func (rc *realChain) pair(t *testing.T, names ...string) {
	t.Helper()
	ms := make([]proto.PairMachine, 0, len(names))
	for _, n := range names {
		addr := "http://127.0.0.1:1"
		if f := rc.fixtures[n]; f != nil {
			addr = f.server.URL
		}
		ms = append(ms, proto.PairMachine{Name: n, Token: "tok-" + n, Addr: addr})
	}
	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version:   proto.PairVersion,
		Machines:  ms,
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("编码配对 bundle: %v", err)
	}
	if err := Pair(payload); err != nil {
		t.Fatalf("配对: %v", err)
	}
}

// TestRealCoreSwitchAndCookieOwnership 是 spec §8.2 的真实 Core 竖切 1–5：
// 经导出面 Pair → SwitchMachine → SessionCookie 走真实链，锁 origin/cookie
// 归属、错机拒绝、切回重兑换、同机缓存。cookie 值逐字等于夹具 Set-Cookie，
// 不用预置假值。
func TestRealCoreSwitchAndCookieOwnership(t *testing.T) {
	rc := newRealChain(t, "A", "B")
	rc.pair(t, "A", "B")

	originA, err := SwitchMachine("A")
	if err != nil {
		t.Fatalf("切机 A: %v", err)
	}
	if !strings.HasPrefix(originA, "http://127.0.0.1:") {
		t.Fatalf("切机 A 应返回 loopback origin，实得 %q", originA)
	}
	_, _, wantA := rc.fixtures["A"].stats()
	gotA, err := SessionCookie("A")
	if err != nil {
		t.Fatalf("取 A cookie: %v", err)
	}
	if gotA != wantA || gotA == "" {
		t.Fatalf("SessionCookie(A) 应逐字等于夹具 Set-Cookie.Value: got=%q want=%q", gotA, wantA)
	}

	originB, err := SwitchMachine("B")
	if err != nil {
		t.Fatalf("切机 B: %v", err)
	}
	if originB == originA {
		t.Fatalf("切到 B 后 origin 应改变: %q", originB)
	}
	if v, err := SessionCookie("A"); err == nil || v != "" {
		t.Fatalf("切到 B 后取 A 必须失败: value=%q err=%v", v, err)
	}
	_, _, wantB := rc.fixtures["B"].stats()
	gotB, err := SessionCookie("B")
	if err != nil {
		t.Fatalf("取 B cookie: %v", err)
	}
	if gotB != wantB || gotB == gotA {
		t.Fatalf("B cookie 应等于夹具 B 值且与 A 不同: gotB=%q wantB=%q gotA=%q", gotB, wantB, gotA)
	}

	// 切回 A：重新兑换，值与首次不同（ticket 序号递增）。
	if _, err := SwitchMachine("A"); err != nil {
		t.Fatalf("切回 A: %v", err)
	}
	_, _, wantA2 := rc.fixtures["A"].stats()
	gotA2, err := SessionCookie("A")
	if err != nil {
		t.Fatalf("切回后取 A cookie: %v", err)
	}
	if gotA2 != wantA2 || gotA2 == gotA {
		t.Fatalf("切回 A 应重新兑换出新值: got=%q want=%q 首次=%q", gotA2, wantA2, gotA)
	}

	// 同机重复切机：命中 Core 缓存，不再领票/兑换。
	ticketsBefore, exchBefore, _ := rc.fixtures["A"].stats()
	if _, err := SwitchMachine("A"); err != nil {
		t.Fatalf("同机再切: %v", err)
	}
	ticketsAfter, exchAfter, _ := rc.fixtures["A"].stats()
	if ticketsAfter != ticketsBefore || exchAfter != exchBefore {
		t.Fatalf("同机重复切机应命中缓存: tickets %d→%d exchanges %d→%d",
			ticketsBefore, ticketsAfter, exchBefore, exchAfter)
	}
	if v, err := SessionCookie("A"); err != nil || v != wantA2 {
		t.Fatalf("同机缓存后 cookie 应不变: value=%q err=%v want=%q", v, err, wantA2)
	}
}

// switchOutcome / readOutcome 是并发测试的返回载荷。
type switchOutcome struct {
	origin string
	err    error
}
type readOutcome struct {
	value string
	err   error
}

// TestRealCoreFailsClosed 锁 spec §8.2-6/7：未配对、离线、兑换无 cookie、
// 导出面 Close 后，SwitchMachine 与 SessionCookie 都必须返回 error，绝不
// 空串 + nil。
func TestRealCoreFailsClosed(t *testing.T) {
	t.Run("未配对", func(t *testing.T) {
		rc := newRealChain(t, "A")
		rc.pair(t, "A")
		if v, err := SwitchMachine("ghost"); err == nil || v != "" {
			t.Fatalf("未配对切机必须 (\"\", err): value=%q err=%v", v, err)
		}
		if v, err := SessionCookie("ghost"); err == nil || v != "" {
			t.Fatalf("未配对取 cookie 必须 (\"\", err): value=%q err=%v", v, err)
		}
	})
	t.Run("离线", func(t *testing.T) {
		rc := newRealChain(t, "A")
		rc.pair(t, "A", "offline")
		if v, err := SwitchMachine("offline"); err == nil || v != "" {
			t.Fatalf("离线切机必须 (\"\", err): value=%q err=%v", v, err)
		}
		if v, err := SessionCookie("offline"); err == nil || v != "" {
			t.Fatalf("离线取 cookie 必须 (\"\", err): value=%q err=%v", v, err)
		}
	})
	t.Run("兑换无 cookie", func(t *testing.T) {
		rc := newRealChain(t, "nocookie")
		rc.fixtures["nocookie"].setOmitCookie(true)
		rc.pair(t, "nocookie")
		if v, err := SwitchMachine("nocookie"); err == nil || v != "" {
			t.Fatalf("兑换无 cookie 必须 (\"\", err): value=%q err=%v", v, err)
		}
		if v, err := SessionCookie("nocookie"); err == nil || v != "" {
			t.Fatalf("兑换失败后取 cookie 必须 (\"\", err): value=%q err=%v", v, err)
		}
	})
	t.Run("Close 后", func(t *testing.T) {
		rc := newRealChain(t, "A")
		rc.pair(t, "A")
		if _, err := SwitchMachine("A"); err != nil {
			t.Fatalf("前置切机: %v", err)
		}
		if err := Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if v, err := SwitchMachine("A"); err == nil || v != "" {
			t.Fatalf("Close 后切机必须 (\"\", err): value=%q err=%v", v, err)
		}
		if v, err := SessionCookie("A"); err == nil || v != "" {
			t.Fatalf("Close 后取 cookie 必须 (\"\", err): value=%q err=%v", v, err)
		}
	})
}

// TestRealCoreGateSerializesSwitchAndRead 是真实 Core 的**正向**并发覆盖（spec §8.2-8）：
// 用可控 HTTP gate 在真实链上制造「切机在途」，同时并发发起 SessionCookie(B)/SessionCookie(A)，
// 断言切机完成后 B 取到**真实** cookie、A 绝不拿到 B 的值。
//
// 定位（review P1a/P3 明示，不得越界声称）：
//   - 真实 Core 在「校验 ActiveMachine」与「读 Session」之间**没有可注入的网络/调用缝**，
//     无法在真实链上确定性复现 TOCTOU。故本测试**只作正向真实链覆盖**，不是去锁变异的
//     确定性证据。
//   - 去适配器锁的**唯一确定性打红**手段是同包 TryLock 测试
//     （`TestCoreSessionsLockSerializesSwitchAndRead`，Task 4.2）；`-race` 是附加闸。
//     **不得声称本测试会让变异④必红**（去锁后本场景 A 仍会因 active==B 而失败，本测试
//     很可能照样绿）。
//   - 本测试每个等待都用 select+超时，任一环节挂起都失败而非永久阻塞；夹具 gate 由
//     defer 安全放行，失败路径不互等。
func TestRealCoreGateSerializesSwitchAndRead(t *testing.T) {
	const step = 5 * time.Second
	rc := newRealChain(t, "A", "B")
	rc.pair(t, "A", "B")
	if _, err := SwitchMachine("A"); err != nil {
		t.Fatalf("前置切机 A: %v", err)
	}

	entered, release := rc.fixtures["B"].gateConsole()
	// release 安全关闭：无论测试在哪一步失败，都放行卡在 gate 里的 /console handler，
	// 不让夹具 goroutine 永久阻塞；已关闭时 no-op，失败不互等。
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	switchDone := make(chan switchOutcome, 1)
	go func() {
		origin, err := SwitchMachine("B")
		switchDone <- switchOutcome{origin, err}
	}()
	// 同步证据①：B 的 /console 兑换已进入 gate（切机确已在途）。
	select {
	case <-entered:
	case <-time.After(step):
		t.Fatal("超时：B 的 /console 兑换未进入 gate")
	}

	// 读请求的「goroutine 已启动」证据：每个读 goroutine 在**调用导出面之前** close
	// 自己的 started channel，测试收齐两个信号后才放行切机——证明放行时两个读调用
	// 已**即将发起**（goroutine 已调度到调用点）。
	// 边界（勿过度声称）：信号在调用**之前**发出，故它不证明导出面已持适配器锁、
	// 也不证明已进入 Core。真实 Core 无确定性 TOCTOU 注入缝，确定性去锁证据归
	// Task 4.2 的 TryLock 测试；本测试只作正向真实链覆盖。
	readBStarted := make(chan struct{})
	readAStarted := make(chan struct{})
	readB := make(chan readOutcome, 1)
	readA := make(chan readOutcome, 1)
	go func() {
		close(readBStarted)
		v, err := SessionCookie("B")
		readB <- readOutcome{v, err}
	}()
	go func() {
		close(readAStarted)
		v, err := SessionCookie("A")
		readA <- readOutcome{v, err}
	}()
	select {
	case <-readBStarted:
	case <-time.After(step):
		t.Fatal("超时：SessionCookie(B) 未启动")
	}
	select {
	case <-readAStarted:
	case <-time.After(step):
		t.Fatal("超时：SessionCookie(A) 未启动")
	}

	close(release) // 放行 B 的兑换（defer 兜底重复关闭）

	var sw switchOutcome
	select {
	case sw = <-switchDone:
	case <-time.After(step):
		t.Fatal("超时：切机 B 未完成")
	}
	if sw.err != nil {
		t.Fatalf("切机 B: %v", sw.err)
	}
	if !strings.HasPrefix(sw.origin, "http://127.0.0.1:") {
		t.Fatalf("切机 B 应返回 loopback origin: %q", sw.origin)
	}

	_, _, wantB := rc.fixtures["B"].stats()

	var rb readOutcome
	select {
	case rb = <-readB:
	case <-time.After(step):
		t.Fatal("超时：SessionCookie(B) 未返回")
	}
	if rb.err != nil || rb.value == "" || rb.value != wantB {
		t.Fatalf("切机 B 完成后 SessionCookie(B) 应逐字返回 B 真实值: got=%q want=%q err=%v",
			rb.value, wantB, rb.err)
	}
	if !strings.Contains(rb.value, "-B-") || strings.Contains(rb.value, "-A-") {
		t.Fatalf("SessionCookie(B) 归属错误（应含 -B- 不含 -A-）: %q", rb.value)
	}
	var ra readOutcome
	select {
	case ra = <-readA:
	case <-time.After(step):
		t.Fatal("超时：SessionCookie(A) 未返回")
	}
	if ra.err == nil {
		t.Fatalf("切到 B 后 SessionCookie(A) 必须失败，不得返回值: value=%q", ra.value)
	}
	if strings.Contains(ra.value, "-B-") {
		t.Fatalf("SessionCookie(A) 串到了 B 的值: %q", ra.value)
	}
}

// TestRealCoreConcurrentNoCrossMachine 是 -race 附加闸（补充，不替代上面的 gate
// 测试）：并发 SwitchMachine/SessionCookie 在 -race 下通过，且成功读取的值必带
// 本机标记（A/B 值可区分），绝不把 B 的 cookie 返回给 A。
//
// 读活性两层：并发前先做确定性串行读（不依赖调度，证明链路活着）；并发结束后
// 断言 successReads > 0，防止「全部 SessionCookie 被错机拒绝」仍通过。
// worker 等待用 done 通道 + 超时，禁止无界 wg.Wait 挂死测试。
func TestRealCoreConcurrentNoCrossMachine(t *testing.T) {
	const workerTimeout = 30 * time.Second
	rc := newRealChain(t, "A", "B")
	rc.pair(t, "A", "B")
	if _, err := SwitchMachine("A"); err != nil {
		t.Fatalf("前置切机 A: %v", err)
	}
	if v, err := SessionCookie("A"); err != nil || !strings.Contains(v, "-A-") {
		t.Fatalf("并发前置串行读 A 应成功（确定性读活性）: value=%q err=%v", v, err)
	}
	if _, err := SwitchMachine("B"); err != nil {
		t.Fatalf("前置切机 B: %v", err)
	}
	if v, err := SessionCookie("B"); err != nil || !strings.Contains(v, "-B-") {
		t.Fatalf("并发前置串行读 B 应成功（确定性读活性）: value=%q err=%v", v, err)
	}

	const workers, iters = 8, 20
	errCh := make(chan string, workers*iters)
	var successReads atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				m := "A"
				if (i+j)%2 == 1 {
					m = "B"
				}
				if _, err := SwitchMachine(m); err != nil {
					errCh <- fmt.Sprintf("SwitchMachine(%s): %v", m, err)
					return
				}
				v, err := SessionCookie(m)
				if err != nil {
					continue // 另一个 worker 已切走，错机拒绝合法
				}
				successReads.Add(1)
				if !strings.Contains(v, "-"+m+"-") {
					errCh <- fmt.Sprintf("SessionCookie(%s) 返回了错机 cookie: %q", m, v)
					return
				}
			}
		}(i)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(workerTimeout):
		t.Fatalf("超时(%s)：并发 worker 未全部退出（禁止无界等待）", workerTimeout)
	}
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
	if successReads.Load() == 0 {
		t.Error("并发补充测试应至少有一次成功读取（全部被错机拒绝则覆盖为空）")
	}
}
