# B163 实现计划：账本镜像的机器解析统一到 target 客户端池

- **Spec**：`docs/superpowers/specs/2026-08-22-b163-ledger-mirror-live-config.md`
- **级别**：L2（单子系统，不动跨进程 wire 契约）
- **分支**：`claude/b163-start-64c4be`
- **执行者假设**：对本仓库零上下文。下面每个 task 给出精确路径与完整代码块，照做即可。

## 0. 基线（本计划写下时已在本工作树实测，动手前请复跑确认仍成立）

| 命令 | 基线结果（2026-08-22） |
|---|---|
| `go build ./...` | 退出码 0 |
| `gofmt -l internal cmd` | 无输出 |
| `go test ./internal/ledgermirror/` | `ok ... 1.729s` |
| `go test ./internal/agentd/` | `ok ... 80.978s` |

**判据错的代价大于实现错**：任一条在基线上就不成立时，先停下来查为什么，不要继续往下写。

## 1. 背景（执行者需要知道的现状事实）

账本镜像子系统（`internal/ledgermirror`）把挂账 task 的事件从各执行机镜像进账本。
它今天自己持有「机器名 → 地址/令牌」这张表，由此有四条缺陷（spec §1 有完整读数）：

1. `cmd/agentd.go` 里 `if len(cfg.Targets) > 0` 才挂载它 —— 零机器起的 agentd 加了机器要重启；
2. `internal/ledgermirror/mirror.go:203` 起订时按值捕获 `config.Target`，重连循环
   （`mirror.go:259`）永远用那份旧 `Addr/Token`；
3. 注入的 targets 函数每轮 `config.Load(p)` 重读磁盘，与 `Server.conf()` 的原子快照两套真相；
4. `DefaultSource` 恒走 `client.New(addr, token)` 只认直连，而 relay 形态的 target
   `Addr` 恒为空（`internal/config/config.go:236` relay 与 addr 互斥）→ **relay 执行机的账本镜像
   永远连不上**。

同一个根因：没走已有的 target 客户端池 `internal/targetclient`。池已经解决了全部四件事：
它每次 `Names()/For()` 都现读配置快照（`internal/targetclient/pool.go:79/99`）、target 值变了
就关旧隧道重建（`pool.go:114-127`）、按形态选路直连或 relay（`internal/targetclient/targetclient.go:40-54`）。
任务镜像已经这么干了（`internal/agentd/mirror.go:98`），账本镜像没有。

**本轮把账本镜像也接到同一个池上**，外加一条用户可见的读时过滤。

---

## Task 1：账本镜像改吃 `Machines` 接口（搬家，行为等价）

**目标**：把「机器名 → 地址/令牌 → 自己拨号」换成「机器名 → 客户端（别人给）」。
本 task 不加新行为判据，只搬家；新行为在 Task 2/3。

### 1.1 判据先在基线跑

```bash
go test ./internal/ledgermirror/
```
期望 `ok`（基线 1.729s，4 个用例）。这四个用例就是本 task 的**搬家回归网**。

### 1.2 改 `internal/ledgermirror/mirror.go`

**(a) 包注释补一句边界**，把文件头的

```go
// 本包不依赖 internal/agentd；事件源经 Source 注入，生产实现为
// client.StreamEventsOnce 包装，测试不碰网络。
```

改成

```go
// 本包不依赖 internal/agentd，也不依赖 internal/targetclient：机器清单与
// 客户端经 Machines 接口注入（消费者侧接口），生产实现是 agentd 的
// target 客户端池，测试注入内存实现，全程不碰网络。
//
// 边界：本包不解析机器地址、不选传输形态（直连 / relay）、不管隧道生命周期
// —— 那三件事全归池。包内出现 config.Target 的 Addr/Token 字段读取即是回退。
```

**(b) 把 `Source` 的入参从「地址+令牌」上移到「客户端」**，替换现有 `Source` 与 `DefaultSource`：

```go
// Source 一条 per-task 事件订阅：从 fromSeq（排他）起回放 + 跟流，
// 阻塞直到 ctx 取消或连接终结。
//
// 参数：
//   - c: 该机器当前的客户端，由 Machines.For 现取；**每次重订阅都重新取**，
//     机器改了地址或令牌时池会重建客户端，旧实例不再被使用（B163 ②）
//   - fromSeq: 排他水位，调用方按本机已落账的最大 source_seq 传
//
// 注意：实现方不得关闭 c —— 隧道归池，进程退出时统一关。
type Source func(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error

// DefaultSource 生产事件源：client.StreamEventsOnce 的薄包装。
//
// 为什么带 MarkForwarded：这一跳是 agentd→agentd，标记让对端不再向外扇出
//（一跳封顶，A→B→A 不成环），与任务镜像的镜像订阅同款
//（internal/agentd/mirror.go 的 c.MarkForwarded().StreamEventsOnce）。
func DefaultSource(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	return c.MarkForwarded().StreamEventsOnce(ctx, taskID, fromSeq, onEvent)
}
```

**(c) 新增 `Machines` 接口与 `subscription` 类型**，插在 `Options` 定义之后：

```go
// Machines 是账本镜像对「有哪些机器、怎么连它」的唯一依赖。
//
// 为什么是消费者侧接口而不是直接 import targetclient：本包只需要「清单 +
// 取客户端」两件事，声明在这里既让测试能注入内存实现，也不给本包引入
// relay 传输栈的依赖。生产实现是 *targetclient.Pool，方法集逐字相同。
//
// 注意：For 不发任何网络请求，可以每轮对账现调；它的错误是**配置性**的
//（机器未登记 / 无端点 / relay token 熵不足），不是网络抖动——因此
// For 失败按「这台机器当前不可用」处理，而不是重试。
type Machines interface {
	// Names 返回当前登记的机器名，已排序。判据取自活配置快照。
	Names() []string
	// For 取某台机器的客户端；**调用方不负责关闭它**。
	For(name string) (*client.Client, error)
}

// subscription 是一条在飞订阅的登记：取消函数 + 它当时用的客户端实例。
//
// 为什么要记住客户端实例：它就是「这台机器的配置变没变」的判据。池在
// target 值不变时返回同一实例，变了则关掉旧隧道重建
//（internal/targetclient/pool.go 的 e.target == t 判等）。于是
// 实例不等 ⇒ 配置已变 ⇒ 必须退订重订；否则在飞订阅会拿旧 addr/token
// 无限重连下去（B163 ②，改地址后永不生效）。
type subscription struct {
	cancel context.CancelFunc
	client *client.Client
}
```

**(d) `Mirror` 结构体**：把 `targets func() map[string]config.Target` 换成 `machines Machines`，
把 `subs map[string]context.CancelFunc` 换成 `subs map[string]*subscription`：

```go
type Mirror struct {
	st       *ledger.Store
	machines Machines
	opt      Options
	log      *slog.Logger

	holding    atomic.Bool
	mu         sync.Mutex
	subs       map[string]*subscription
	conn       map[string]bool
	ended      map[string]bool
	wg         sync.WaitGroup
	wgMu       sync.Mutex
	runMu      sync.Mutex
	runStop    context.CancelFunc
	runDone    chan struct{}
	runStarted atomic.Bool
	stopAsked  atomic.Bool
}
```

**(e) `New` 换参数**：

```go
// New 构造。
//
// 参数：
//   - st: 账本库，镜像事件的落点
//   - machines: 活的机器清单与客户端来源；生产传 agentd 的 target 客户端池，
//     **必须与任务镜像共用同一个池实例**——两个池等于两套 relay 隧道，
//     relay 侧会看到重复的节点连接
//   - opt: 参数，零值取生产默认
func New(st *ledger.Store, machines Machines, opt Options) *Mirror {
	if opt.Tick == 0 {
		opt.Tick = 10 * time.Second
	}
	if opt.LeaseTTL == 0 {
		opt.LeaseTTL = 30 * time.Second
	}
	if opt.Source == nil {
		opt.Source = DefaultSource
	}
	if opt.Holder == "" {
		opt.Holder = "unknown"
	}
	return &Mirror{st: st, machines: machines, opt: opt,
		log:  slog.Default().With("subsystem", "ledgermirror"),
		subs: map[string]*subscription{}, conn: map[string]bool{}, ended: map[string]bool{}}
}
```

**(f) `reconcile` 整体替换**（这是本 task 的主体；Task 2/3 的行为也一并落在这份代码里，
但**判据测试在各自的 task 写**）：

```go
// reconcile 对账挂账表与当前登记机器，建立和收掉订阅，并刷新健康行。
//
// 三条判据都取自活配置（经 Machines）：机器在不在、客户端是不是同一个实例、
// 该机器本轮取不取得到客户端。任何一条不成立都收掉对应订阅——镜像宁可少订，
// 也不能拿旧凭据连下去（那正是 B163 ② 的形态：改了地址却永不生效）。
func (m *Mirror) reconcile(ctx context.Context) {
	names := m.machines.Names()
	registered := make(map[string]bool, len(names))
	for _, n := range names {
		registered[n] = true
	}
	links, err := m.st.AllTaskLinks()
	if err != nil {
		m.log.Warn("读挂账表失败", "err", err)
		return
	}
	want := map[string]ledger.TaskLink{}
	for _, link := range links {
		if !registered[link.Target] {
			continue
		}
		want[link.Target+"/"+link.TaskID] = link
	}

	// 现取客户端：只对本轮真有挂账的机器取一次（For 不发网络请求）。
	// 取不到就当这台机器本轮不可用——For 的错误是配置性的，重试没有意义。
	clients := map[string]*client.Client{}
	for _, link := range want {
		if _, ok := clients[link.Target]; ok {
			continue
		}
		c, err := m.machines.For(link.Target)
		if err != nil {
			m.log.Warn("取机器客户端失败，本轮跳过该机器", "target", link.Target, "err", err)
			continue
		}
		clients[link.Target] = c
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for key, sub := range m.subs {
		link, ok := want[key]
		switch {
		case !ok:
			m.dropSubLocked(key, sub, "机器或挂账已不在")
			// 挂账/机器都没了，终态记忆一并忘掉：将来它再回来时按新的一轮处理
			delete(m.ended, key)
		case clients[link.Target] == nil:
			m.dropSubLocked(key, sub, "本轮取不到该机器的客户端")
		case clients[link.Target] != sub.client:
			m.dropSubLocked(key, sub, "机器配置已变更，退订重订")
		}
	}
	for key, link := range want {
		if ctx.Err() != nil {
			return
		}
		if m.ended[key] {
			continue
		}
		if _, ok := m.subs[key]; ok {
			continue
		}
		c := clients[link.Target]
		if c == nil {
			continue
		}
		subCtx, cancel := context.WithCancel(ctx)
		m.subs[key] = &subscription{cancel: cancel, client: c}
		m.wgMu.Lock()
		m.wg.Add(1)
		m.wgMu.Unlock()
		go m.subscribe(subCtx, link, c)
		m.log.Info("起订", "sub", key, "target", link.Target)
	}

	hasSub := map[string]bool{}
	alive := map[string]bool{}
	for key := range want {
		separator := strings.IndexByte(key, '/')
		if separator < 0 {
			continue
		}
		target := key[:separator]
		hasSub[target] = true
		if m.conn[key] {
			alive[target] = true
		}
	}
	for _, name := range names {
		if hasSub[name] && !alive[name] {
			continue
		}
		if err := m.st.TouchMirrorHealth(name, 0); err != nil {
			m.log.Warn("touch 健康失败", "target", name, "err", err)
		}
	}
}

// dropSubLocked 退订一条并清掉连接状态。**调用方必须持有 m.mu**。
//
// 注意：不动 m.ended —— 那是「这条挂账已终态」的记忆，与「这条订阅为什么被收掉」
// 是两件事；误删会让已归档的 task 在下一轮被重新订阅。
func (m *Mirror) dropSubLocked(key string, sub *subscription, reason string) {
	sub.cancel()
	delete(m.subs, key)
	delete(m.conn, key)
	m.log.Info("退订", "sub", key, "reason", reason)
}
```

**(g) `subscribe` 换签名**：`target config.Target` → `c *client.Client`，
并把 Source 调用改成传客户端。只改这两处，重连循环其余一字不动：

```go
// subscribe 单 task 常驻订阅：watermark 起拉、断线退避重连、事件过滤后幂等落账。
//
// 参数 c 是起订那一刻的客户端实例；它对应的机器配置若在运行期被改，reconcile
// 会取消本 ctx 并用新实例重起一条（见 subscription 的注释），**本函数不自行换客户端**。
func (m *Mirror) subscribe(ctx context.Context, link ledger.TaskLink, c *client.Client) {
```

内部把

```go
		err = m.opt.Source(ctx, target.Addr, target.Token, link.TaskID, wm, func(e proto.Event) error {
```

改成

```go
		err = m.opt.Source(ctx, c, link.TaskID, wm, func(e proto.Event) error {
```

**(h) `stopAllSubs` 跟着改类型**（`cancel` → `sub.cancel()`）：

```go
func (m *Mirror) stopAllSubs(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.subs) > 0 {
		m.log.Info("停全部订阅", "n", len(m.subs), "reason", reason)
	}
	for key, sub := range m.subs {
		sub.cancel()
		delete(m.subs, key)
		delete(m.conn, key)
	}
}
```

**(i) 收拾 import**：`config` 若在本文件不再被引用就删掉它的 import（编译器会告诉你）。

### 1.3 改现有测试夹具（`internal/ledgermirror/mirror_test.go`）

**只改夹具构造，四个用例的断言一字不改。** 三处 `Source` 的签名与三处 `New` 的第二参：

- `TestMirrorFlowsLinkedTaskEvents`：
  `fake := func(ctx context.Context, addr, token, taskID string, fromSeq int64, ...)` →
  `fake := func(ctx context.Context, _ *client.Client, taskID string, fromSeq int64, ...)`；
  `New(s, func() map[string]config.Target {...}, ...)` →
  `New(s, machinesWith(t, "mac-02"), ...)`（helper 见 Task 2 的 1.4）。
- `TestMirrorLeaseExclusive`：`blockSrc` 改为
  `func(ctx context.Context, _ *client.Client, _ string, _ int64, _ func(proto.Event) error) error`；
  `targets` 改成 `newFakeMachines()`（零机器，与原 `return nil` 等价）。
- `TestMirrorNoTouchWhenDisconnected`：`failSrc` 同上换签名；
  `New(s, machinesWith(t, "dead-box", "idle-box"), ...)`。
- `TestMirrorStopBeforeRun`：`New(s, newFakeMachines(), Options{Holder: "test"})`。

### 1.4 跑测试（范围：只跑触及包）

```bash
go test ./internal/ledgermirror/
```

### 1.5 夹具没被削牙的证明（必做，不是可选）

改夹具会让测试**在断言一字未改的情况下**失去牙齿。做一次装饰性变异确认网还在：
把 `subscribe` 里 `m.opt.Source(...)` 那一行整句注释掉、直接 `err = nil`，
复跑 `go test ./internal/ledgermirror/` —— `TestMirrorFlowsLinkedTaskEvents`
**必须变红**（「镜像未按期落账」）。确认后**改回来**再复跑绿。
把红/绿两次的输出尾部各贴进 ledger。

### 1.6 提交

```bash
git add -A && git commit -m "refactor(ledgermirror): 机器解析改吃 Machines 接口，事件源改收客户端"
```

---

## Task 2：机器配置变更即退订重订（B163 ②）

**代码在 Task 1 已落**，本 task 补判据测试。先写测试跑红（把 Task 1 的判等临时改成
`false` 即可造红，见 2.4），再恢复跑绿。

### 2.1 判据先在基线跑

```bash
go test ./internal/ledgermirror/ -run TestMirror
```
期望 `ok`（Task 1 之后的 4 个用例）。

### 2.2 新建 `internal/ledgermirror/machines_test.go`

```go
// 本文件锁死账本镜像「跟随活配置」的三条判据：运行期新增机器即起订、
// 机器配置变更即退订重订、机器消失即退订；外加一条编译期断言，钉死
// 生产实现（target 客户端池）满足 Machines。
//
// why：这三条过去都不成立——机器清单是启动快照、在飞订阅按值捕获
// config.Target、relay 形态因为拿 addr 拨号而永远连不上（B163）。
package ledgermirror

import (
	"context"
	"fmt"
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
	runMirror(t, s, mach, recordingSource(calls, nil))

	first := waitCall(t, calls, "首轮应起订")
	mach.remove("gone-box")
	select {
	case <-first.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("机器已删，订阅却还活着")
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
```

### 2.3 跑测试（范围：只跑触及包）

```bash
go test ./internal/ledgermirror/
```
期望 8 个用例全绿。

### 2.4 变异复验（必做，逐条把红/绿输出贴进 ledger）

| 变异 | 期望变红的用例 |
|---|---|
| `reconcile` 里 `case clients[link.Target] != sub.client:` 整个 case 删掉 | `TestMirrorResubscribesWhenMachineClientChanges` |
| `reconcile` 里 `case !ok:` 整个 case 删掉 | `TestMirrorDropsSubWhenMachineRemoved` |
| `reconcile` 开头把 `names := m.machines.Names()` 换成只在首轮取一次（模拟启动快照） | `TestMirrorSubscribesMachineAddedAtRuntime` |

每条变异后**必须改回**并复跑绿。任一条变异没能打红对应用例，说明该用例没有牙齿，
**重写测试，不许跳过**。

### 2.5 提交

```bash
git add -A && git commit -m "test(ledgermirror): 锁死跟随活配置三条判据 + relay 起订"
```

---

## Task 3：cmd 布线——拆启动闸，注入池

**本 task 没有单测缝**：`cmd/agentd.go` 是组装点，为可测抽一层不划算。
它的行为判据在 Task 6 的真机验收，**plan 不假装它被单测覆盖**。

### 3.1 判据先在基线跑

```bash
go build ./... && gofmt -l cmd
```
期望：退出码 0、无输出。

### 3.2 改 `cmd/agentd.go`

找到账本域那段里的这一块（现状 `if len(cfg.Targets) > 0 { ... } else { ... }`）：

```go
			if len(cfg.Targets) > 0 {
				host, _ := os.Hostname()
				lm := ledgermirror.New(lst, func() map[string]config.Target {
					// /api/machines 热改会原子替换配置快照；从配置文件读取使
					// 本子系统无需持有启动时的旧 targets 集合。
					current, err := config.Load(p)
					if err != nil {
						logger.Warn("读取镜像 targets 配置失败，沿用启动快照", "err", err)
						return cfg.Targets
					}
					return current.Targets
				}, ledgermirror.Options{Holder: host})
				go lm.Run(wdCtx)
				defer lm.Stop()
				logger.Info("账本镜像子系统已挂载", "holder", host)
			} else {
				logger.Info("账本镜像未启动：无已登记 target")
			}
```

整块替换成：

```go
			// 恒挂载：机器清单来自 target 客户端池的活配置读取，启动时没有机器
			// 不代表以后没有——留着 len(cfg.Targets)>0 的闸会让控制台新增的第一台
			// 机器永远等不到账本镜像（与上方任务镜像同一条纪律，B163 ①）。
			// 池必须与任务镜像共用同一个：两个池等于两套 relay 隧道。
			host, _ := os.Hostname()
			lm := ledgermirror.New(lst, srv.Pool(), ledgermirror.Options{Holder: host})
			go lm.Run(wdCtx)
			// 次序硬约束：订阅回调在写账本库，Stop 必须先于 lst.Close()。
			// defer 是 LIFO，本行注册在 defer lst.Close() 之后，因此先于它执行。
			defer lm.Stop()
			logger.Info("账本镜像子系统已挂载", "holder", host,
				"machines", len(srv.Pool().Names()))
```

**注意两件事**：

1. 上面那句 `logger.Info("事件镜像已启动", "targets", len(cfg.Targets), ...)` 里的
   `len(cfg.Targets)` 是**启动快照的计数**，会误导读日志的人以为镜像只认这些机器。
   把它改成 `len(srv.Pool().Names())` 并在同一行补 `"note", "运行期新增的机器无需重启"`。
2. 删掉不再使用的 import / 变量后必须 `go build ./...` 过；若 `config` 或 `p`
   在本文件别处仍被引用则原样保留（编译器是判据，不要凭印象删）。

### 3.3 编译与格式

```bash
go build ./... && gofmt -l cmd internal
```

### 3.4 提交

```bash
git add -A && git commit -m "fix(agentd): 账本镜像恒挂载并接到 target 客户端池"
```

---

## Task 4：任务汇总按活配置过滤镜像快照

### 4.1 判据先在基线跑

```bash
go test ./internal/agentd/ -run TestTasks
```
期望 `ok`（基线整包 80.978s，`-run TestTasks` 只跑几个用例，秒级）。

### 4.2 先写失败测试：`internal/agentd/tasksfanout_test.go` 追加

```go
// TestTasksScopeAllSkipsUnregisteredMachine 断言：控制台删掉一台机器后，
// 它遗留在镜像表里的任务不再出现在任务汇总里。
//
// why：镜像快照是副本不是真相，「有哪些机器」的判据只有一个——活配置。
// 库里的遗留行不删（删行是破坏性操作），读时过滤即可（B163）。
func TestTasksScopeAllSkipsUnregisteredMachine(t *testing.T) {
	env := seedTasksEnv(t) // cfg 里只登记了 devbox
	now := time.Now().UTC()
	if err := env.st.UpsertMirrorTask("deleted-box", proto.Task{
		ID: uuid.NewString(), Name: "已删机器的遗留任务", State: proto.TaskStateRunning,
		RepoPath: "/remote/gone", CreatedAt: now, UpdatedAt: now,
	}, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}

	var resp proto.TasksResp
	code := env.getJSON(t, "/api/tasks?scope=all", &resp)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if len(resp.Tasks) != 3 {
		t.Fatalf("任务数 = %d，期望 3（本机 2 + devbox 1，已删机器那条不算）：%+v",
			len(resp.Tasks), resp.Tasks)
	}
	for _, tv := range resp.Tasks {
		if tv.Machine == "deleted-box" {
			t.Fatalf("已删机器的任务仍在列表里：%+v", tv)
		}
	}
	for _, ms := range resp.Machines {
		if ms.Name == "deleted-box" {
			t.Fatalf("已删机器仍出现在 machines 行里：%+v", ms)
		}
	}
}
```

跑一次确认它**是红的**：

```bash
go test ./internal/agentd/ -run TestTasksScopeAllSkipsUnregisteredMachine
```
期望：`任务数 = 4，期望 3 ...`。红了才继续。

### 4.3 最小实现：`internal/agentd/tasksfanout.go`

把 `tasksAll` 里读镜像快照的那段改成（**一次 `s.conf()`，下面的 machines 行复用同一份快照**——
两次读可能跨越一次配置热改，那会让「任务过滤掉了、机器行还在」这种不自洽出现）：

```go
	// 有哪些机器，判据只有一个：活配置。已删机器遗留在镜像表里的快照不再展示
	//（副本不是真相；库里的行不删，删行是破坏性操作，读时判据正确就够）。
	targets := s.conf().Targets
	skipped := 0
	// target → 该机最新一条快照的时刻（机器应答行的数据新旧）
	fetched := map[string]time.Time{}
	for _, mt := range mirrors {
		if _, ok := targets[mt.Target]; !ok {
			skipped++
			continue
		}
		t := mt.Task
		t.Machine = mt.Target // 汇总方盖章：这条任务是从哪个 target 拉来的
		views = append(views, proto.TaskView{Task: t, Watchers: s.hub.Watchers(t.ID)})
		if prev, ok := fetched[mt.Target]; !ok || mt.FetchedAt.After(prev) {
			fetched[mt.Target] = mt.FetchedAt
		}
	}
	if skipped > 0 {
		s.log.Debug("任务汇总：跳过已删机器的镜像快照", "skipped", skipped)
	}
```

再把下面那句 `names := make([]string, 0, len(s.conf().Targets))` 与
`for name := range s.conf().Targets` 改成用同一个 `targets` 变量。

### 4.4 跑绿（范围：只跑触及包）

```bash
go test ./internal/agentd/ -run TestTasks
```

### 4.5 变异复验

把 `if _, ok := targets[mt.Target]; !ok { skipped++; continue }` 删掉，
`TestTasksScopeAllSkipsUnregisteredMachine` **必须变红**；改回复绿。红/绿输出贴 ledger。

### 4.6 提交

```bash
git add -A && git commit -m "fix(agentd): 任务汇总按活配置过滤已删机器的镜像快照"
```

---

## Task 5：日志与注释走查 + 整分支收口

### 5.1 日志走查（逐条对照，缺就补）

| 位置 | 必须有的日志 |
|---|---|
| `reconcile` 取客户端失败 | `Warn("取机器客户端失败，本轮跳过该机器", "target", ..., "err", ...)` |
| `reconcile` 退订 | `Info("退订", "sub", key, "reason", ...)`，reason 区分「机器或挂账已不在 / 本轮取不到该机器的客户端 / 机器配置已变更，退订重订」——**三种原因必须能从日志分辨**，否则真机排查时「为什么断了」无从判起 |
| `reconcile` 起订 | `Info("起订", "sub", key, "target", ...)` |
| cmd 挂载 | `Info("账本镜像子系统已挂载", "holder", ..., "machines", ...)` |
| 任务汇总跳过 | `Debug("任务汇总：跳过已删机器的镜像快照", "skipped", n)`（循环内高频，取 Debug） |

全部用 `slog`（本仓库结构化 logger），**禁 `fmt.Printf`**。

### 5.2 注释走查

- 新文件 `internal/ledgermirror/machines_test.go` 头部有职责 + why（Task 2 的代码块已含）；
- `Machines` / `subscription` / `Source` / `DefaultSource` / `New` / `subscribe` /
  `dropSubLocked` 的注释都在（Task 1 的代码块已含），且写的是**为什么**而不是复述代码；
- `internal/ledgermirror/mirror.go` 包注释已加「不解析地址、不选形态」的边界句。

### 5.3 全量三段（implement 三段律的集成段）

```bash
go build ./...
gofmt -l . | grep -v node_modules
go vet ./...
go test ./...
```
期望：build 0；gofmt 无输出；vet 无输出；`go test ./...` 全绿。
**整包耗时基线约 2 分钟以上，`internal/agentd` 单包 81s，不要以为卡住了。**

### 5.4 提交

```bash
git add -A && git commit -m "chore(ledgermirror): 日志与注释走查收口"
```

---

## Task 6：真机验收（**本 task 由协调者本地执行，不派发**）

**为什么不派发**：它要驱动 handoff 自身（起 agentd 实例、建卡挂账、改机器配置），
与执行者纪律块的「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」直接冲突；
派出去等于没验。

环境要求（踩过的坑，别省）：

- 独立 DataDir 放 `/tmp` 下的**短路径**（PTY socket 路径有 104 字节上限，scratchpad 那种长路径会炸）；
- 独立端口（如 7799），**不要重启在跑的生产 agentd**；
- 配置：`ledger.enabled: true`，起手 `targets` 为**空**。

| # | 步骤 | 判据 |
|---|---|---|
| 1 | 零机器起 agentd | 日志有「账本镜像子系统已挂载 ... machines=0」，**不再有**「账本镜像未启动：无已登记 target」 |
| 2 | 不重启，加一台机器（用本机 `~/.handoff/config.yaml` 里 relay 形态的 `linux-01` 那份配置） | 一个对账周期内健康行出现该机器；`ledger.db` 的 `mirror_cursors` 出现它 |
| 3 | 建卡 + 挂一条 `linux-01` 上的真实任务 | `card_events` 出现 `task_mirrored` 事件——**这直接证伪 relay 缺陷**（旧实现在这里恒失败） |
| 4 | 改该机器的 token（改错再改回） | 日志出现「退订 ... reason=机器配置已变更，退订重订」；改回后事件继续进账，`card_events` 的 `source_seq` **无重复**（幂等 + 水位续拉） |
| 5 | 删该机器 | `/api/tasks?scope=all` 里它的任务消失，`machines` 行也没有它 |

取证纪律：红绿两侧用同一份探针；每条判据贴原始输出（日志行 / SQL 结果），不要只写「通过」。

---

## 自审

### spec 覆盖对账（逐条指到 task）

| spec 用户故事 | 落点 |
|---|---|
| ① 零机器起，加机器后不重启即镜像 | Task 3（布线）+ Task 2 的 `TestMirrorSubscribesMachineAddedAtRuntime` + Task 6 步骤 1/2 |
| ② 改地址/令牌后不重启即切换，水位续拉 | Task 1(f) 的判等 + Task 2 的 `TestMirrorResubscribesWhenMachineClientChanges` + Task 6 步骤 4 |
| ③ relay 形态照样进账本 | Task 1(b)(e) 走池 + Task 2 的 `TestMirrorSubscribesRelayMachine` 与编译期断言 + Task 6 步骤 3 |
| ④ 删机器后任务立刻消失 | Task 4 + Task 6 步骤 5 |
| ⑤ 镜像与控制台/CLI 永远同一份机器清单 | Task 3（删掉 `config.Load` 磁盘重读，改吃 `srv.Pool()` → `Server.conf()` 原子快照） |

spec §4 的 7 条实现决定：1→Task 1(c)(d)；2→Task 1(f)；3→Task 3；4→Task 1(b)；
5→Task 3（`srv.Pool()`）；6→Task 4；7→Task 3 的 defer 次序注释。

### 占位符扫描

无 TBD、无「同 Task N」、无「加适当的错误处理」。所有测试代码完整给出，
**不使用**「断言列全 + 指认既有 harness」那条例外。

### 跨 task 签名一致性

| 签名 | 生产者 | 消费者 |
|---|---|---|
| `type Machines interface { Names() []string; For(name string) (*client.Client, error) }` | Task 1(c) | Task 2 的 `fakeMachines`、Task 3 的 `srv.Pool()`（`internal/targetclient/pool.go:79/99` 逐字相同） |
| `type Source func(ctx context.Context, c *client.Client, taskID string, fromSeq int64, onEvent func(proto.Event) error) error` | Task 1(b) | Task 1.3 改的三处夹具、Task 2 的 `recordingSource` |
| `func New(st *ledger.Store, machines Machines, opt Options) *Mirror` | Task 1(e) | Task 1.3 的四处、Task 2 的 `runMirror`、Task 3 的 cmd 布线 |
| `func (m *Mirror) subscribe(ctx context.Context, link ledger.TaskLink, c *client.Client)` | Task 1(g) | 仅 `reconcile` 内部调用 |

### 四项检查

**1. 缺陷族对抗审查**

| 族 | 设问 | 结论 |
|---|---|---|
| 并发 | 每轮 reconcile 都在 `m.mu` 下改 `subs`，`For` 在锁外调 —— 会不会锁内调用外部实现导致死锁？ | 已把取客户端全部提到加锁**之前**；锁内只做 map 增删与 cancel。cancel 不阻塞（只关 ctx） |
| 并发 | 退订与 `subscribe` goroutine 自己 `setConn` 是否竞态？ | `setConn` 也走 `m.mu`；退订只删 `conn[key]`，晚到的 `setConn` 会重新写入一个孤儿键——**已存在的既有行为**，不在本轮扩大，`stopAllSubs`/下一轮 reconcile 会清 |
| 状态机 | 「机器配置变更」退订会不会把已归档任务重新订起来？ | `dropSubLocked` **不动 `ended`**，只有「机器或挂账已不在」那一支显式 `delete(m.ended, key)`。Task 1(f) 的注释写明了这条 |
| 幂等 | 重订从水位续拉会不会重复落账？ | `AppendMirroredEvent` 幂等（既有能力，`TestMirrorFlowsLinkedTaskEvents` 已锁「恰 2 条」）；Task 2 的用例额外断言 `from == 7` |
| 资源 | 退订后 goroutine 会不会漏？ | `subscribe` 的 ctx 被 cancel 后退出并 `wg.Done`；`Stop` 仍等 `wg` |
| 配置 | `For` 报错时把订阅收掉，会不会因为一次抖动误杀？ | `For` 不发网络请求，错误是配置性的（未登记 / 无端点 / token 熵不足），不存在抖动；已写进 `Machines` 的注释 |
| 假绿 | 改夹具会不会让既有四个用例失去牙齿？ | Task 1.5 强制做装饰性变异复验，不做即视为未完成 |

**2. 序列化边界设问**：本轮**不新增任何跨进程/跨语言数据字段**——`Machines` 是进程内 Go 接口，
镜像事件的 wire 形状（`proto.Event`）与落账形状（`ledger.MirroredEvent`）一字未改。
唯一穿边界的变化是「哪些镜像任务出现在 `/api/tasks?scope=all` 的响应里」，
Task 4 的用例正是穿过真实 HTTP + JSON 序列化的（走 `env.getJSON`），不是直接调函数。

**3. 上下文预算**：每个 task 的文件集有界——Task 1/2 只碰 `internal/ledgermirror/`（3 个文件），
Task 3 只碰 `cmd/agentd.go` 一段，Task 4 只碰 `internal/agentd/tasksfanout{,_test}.go`。

**4. 类型标注**：账本镜像是**边界型子系统**（跨机、真网络），故 Task 6 的行为验收写成显式真机清单，
且明确标注了「`DefaultSource` 与 cmd 布线两处没有单测缝」——它们只由真机与代码审查把关。

### 派发前自审

**Task 1–5 可派发；Task 6 必须由协调者本地执行**（已在该 task 标题标注理由）。
派发时请把 Task 6 从派发件里剔除或保留标注，不得让执行者去跑它。
