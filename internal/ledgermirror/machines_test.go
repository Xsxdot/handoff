// 本文件锁死账本镜像「跟随活配置」的三条判据：运行期新增机器即起订、
// 机器配置变更即退订重订、机器消失即退订；外加一条编译期断言，钉死
// 生产实现（target 客户端池）满足 Machines。
//
// why：这三条过去都不成立——机器清单是启动快照、在飞订阅按值捕获
// config.Target、relay 形态因为拿 addr 拨号而永远连不上（B163）。
package ledgermirror

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

// 生产实现必须是 target 客户端池：池按 target 形态选路（直连 / relay），
// 账本镜像因此对 relay 形态的执行机也成立——这正是 B163 ④ 修掉的缺陷。
// 这条断言把「Pool 满足 Machines」钉在编译期，签名漂移当场编译失败。
var _ Machines = (*targetclient.Pool)(nil)

type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeMachines 是 Machines 的内存实现：清单与客户端可在测试中随时替换，
// 用来模拟控制台运行期增 / 改 / 删机器。
//
// 语义约定（与池一致）：换一个**新的客户端实例**表示「这台机器的 addr/token
// 变了，池已重建客户端」；移除表示「机器被删」。
type fakeMachines struct {
	mu      sync.Mutex
	clients map[string]*client.Client
}

func newFakeMachines() *fakeMachines {
	return &fakeMachines{clients: map[string]*client.Client{}}
}

// machinesWith 造一个已登记若干机器的 fake，每台一个独立客户端实例。
func machinesWith(t *testing.T, names ...string) *fakeMachines {
	t.Helper()
	f := newFakeMachines()
	for i, n := range names {
		f.set(n, client.New(fmt.Sprintf("127.0.0.1:%d", 9000+i), "tok"))
	}
	return f
}

func (f *fakeMachines) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.clients))
	for n := range f.clients {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (f *fakeMachines) For(name string) (*client.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clients[name]
	if !ok {
		return nil, fmt.Errorf("target %s 未在配置中登记", name)
	}
	return c, nil
}

// set 登记或替换一台机器的客户端（替换 = 配置被改，池重建了实例）。
func (f *fakeMachines) set(name string, c *client.Client) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients[name] = c
}

// remove 删掉一台机器（= 控制台删机器）。
func (f *fakeMachines) remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.clients, name)
}

// srcCall 是事件源被调用一次的记录。
type srcCall struct {
	client *client.Client
	from   int64
	ctx    context.Context
}

// recordingSource 造一个记录每次调用并保持阻塞的事件源。
// replay 非空时，第一次连接会把这些事件喂给 onEvent（用来推高水位）。
func recordingSource(calls chan<- srcCall, replay []proto.Event) Source {
	return func(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		calls <- srcCall{client: c, from: fromSeq, ctx: ctx}
		for _, e := range replay {
			if e.Seq <= fromSeq {
				continue
			}
			e.TaskID = taskID
			if err := onEvent(e); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
}

// waitCall 等一次事件源调用；超时即失败（判据必须落在确定的信号上，
// 不用 sleep 猜时序）。
func waitCall(t *testing.T, calls <-chan srcCall, why string) srcCall {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatalf("等不到事件源调用：%s", why)
		return srcCall{}
	}
}

// linkedCard 建一张卡并挂一条 target/task。
func linkedCard(t *testing.T, s *ledger.Store, target, task string) string {
	t.Helper()
	c, err := s.CreateCard(ledger.NewCard{Title: "卡", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}
	if err := s.LinkTask(c.ID, target, task, "implement", "t"); err != nil {
		t.Fatalf("LinkTask: %v", err)
	}
	return c.ID
}

// runMirror 起一个短 tick 的镜像并登记收尾。
func runMirror(t *testing.T, s *ledger.Store, mach Machines, src Source) *Mirror {
	t.Helper()
	m := New(s, mach, Options{Holder: "test", Tick: 50 * time.Millisecond,
		LeaseTTL: time.Second, Source: src})
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); m.Stop() })
	return m
}

// TestMirrorSubscribesMachineAddedAtRuntime：起于零机器，控制台加机器后
// **不重启**即起订（B163 ①的包内那一半）。
func TestMirrorSubscribesMachineAddedAtRuntime(t *testing.T) {
	s := testLedger(t)
	linkedCard(t, s, "later-box", "T1")
	mach := newFakeMachines()
	calls := make(chan srcCall, 4)
	runMirror(t, s, mach, recordingSource(calls, nil))

	select {
	case c := <-calls:
		t.Fatalf("零机器时不应有任何订阅，却起了一条：%+v", c)
	case <-time.After(300 * time.Millisecond):
	}

	want := client.New("127.0.0.1:9001", "tok")
	mach.set("later-box", want)

	got := waitCall(t, calls, "运行期新增的机器应在一个对账周期内起订")
	if got.client != want {
		t.Fatalf("订阅用的客户端 = %p，期望机器源给出的那个 %p", got.client, want)
	}
}

// TestMirrorResubscribesWhenMachineClientChanges：机器配置变更（池重建客户端）
// 后，在飞订阅必须换到新实例，且从水位续拉（B163 ②）。
func TestMirrorResubscribesWhenMachineClientChanges(t *testing.T) {
	s := testLedger(t)
	linkedCard(t, s, "mac-02", "T1")
	old := client.New("127.0.0.1:9001", "old")
	mach := newFakeMachines()
	mach.set("mac-02", old)
	calls := make(chan srcCall, 4)
	// 第一次连接落一条 seq=7 的事件，把水位推到 7
	runMirror(t, s, mach, recordingSource(calls,
		[]proto.Event{{Seq: 7, Type: "message", Payload: []byte(`{"text":"hi"}`)}}))

	first := waitCall(t, calls, "首轮应起订")
	if first.client != old || first.from != 0 {
		t.Fatalf("首轮订阅 = (client %p, from %d)，期望 (%p, 0)", first.client, first.from, old)
	}

	// 等水位真的落库再换实例：判据要落在确定信号上，不能靠 sleep 猜
	deadline := time.Now().Add(5 * time.Second)
	for {
		wm, err := s.MirrorWatermark("mac-02", "T1")
		if err != nil {
			t.Fatalf("MirrorWatermark: %v", err)
		}
		if wm == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("首轮事件未落账，水位 = %d", wm)
		}
		time.Sleep(20 * time.Millisecond)
	}

	next := client.New("127.0.0.1:9002", "new")
	mach.set("mac-02", next)

	second := waitCall(t, calls, "机器配置变更后应退订重订")
	if second.client != next {
		t.Fatalf("重订用的客户端 = %p，期望新实例 %p（旧实例 %p 说明还在用旧凭据）",
			second.client, next, old)
	}
	if second.from != 7 {
		t.Fatalf("重订应从水位 7 续拉，实得 %d", second.from)
	}
	select {
	case <-first.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("旧订阅未被取消：它会拿旧 addr/token 一直重连下去")
	}
}

// TestMirrorDropsSubWhenMachineRemoved：控制台删机器后订阅立刻收掉。
func TestMirrorDropsSubWhenMachineRemoved(t *testing.T) {
	s := testLedger(t)
	linkedCard(t, s, "gone-box", "T1")
	mach := machinesWith(t, "gone-box")
	calls := make(chan srcCall, 4)
	var logs safeBuf
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	runMirror(t, s, mach, recordingSource(calls, nil))

	first := waitCall(t, calls, "首轮应起订")
	mach.remove("gone-box")
	select {
	case <-first.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("机器已删，订阅却还活着")
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "reason=机器或挂账已不在") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "reason=机器或挂账已不在") {
		t.Fatalf("机器消失的退订原因不正确，日志=%s", logs.String())
	}
}

// TestMirrorSubscribesRelayMachine：relay 形态的机器照常起订。
//
// why：relay target 按契约没有 addr（config.Target 的 relay 与 addr 互斥），
// 旧实现拿 addr 拨号必然拨空 —— relay 执行机的账本镜像永远连不上（B163 ④）。
// 现在客户端由机器源给出，包内不碰 addr，本用例把这一点钉住。
func TestMirrorSubscribesRelayMachine(t *testing.T) {
	s := testLedger(t)
	linkedCard(t, s, "relay-box", "T1")
	relayTarget := config.Target{
		Relay:      "wss://relay.example/relay",
		Credential: "cred",
		Node:       "n1",
		Token:      strings.Repeat("ab", 20), // relay 要求 ≥32 位十六进制高熵 token
	}
	c, cleanup, err := targetclient.New("relay-box", relayTarget, nil)
	if err != nil {
		t.Fatalf("构造 relay 客户端: %v", err)
	}
	t.Cleanup(cleanup)
	mach := newFakeMachines()
	mach.set("relay-box", c)
	calls := make(chan srcCall, 4)
	runMirror(t, s, mach, recordingSource(calls, nil))

	got := waitCall(t, calls, "relay 形态的机器应照常起订")
	if got.client != c {
		t.Fatalf("订阅用的客户端 = %p，期望 relay 客户端 %p", got.client, c)
	}
}
