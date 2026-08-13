# agentd loopback 辅助监听（B85）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** listen 绑单网卡 IP 时 agentd 追加 `127.0.0.1:同端口` 辅助监听，CLI 本机模式确定性改拨 loopback，使「只暴露一块网卡」成为可用的安全第三档。

**Architecture:** 一个共享判定函数（`internal/config`）统一 CLI 与 agentd 的口径；`Shutdown.Serve` 改为多地址绑定（任一失败即启动失败），同一个 `http.Server` 挂多个 listener，优雅关停零改动；status 新增 `ListenAux` 外露辅址。Spec：[docs/superpowers/specs/2026-08-13-loopback-aux-listen-design.md](../specs/2026-08-13-loopback-aux-listen-design.md)。

**Tech Stack:** Go（标准库 net/http、net），无新依赖。

## Global Constraints

- 日志一律走项目日志器（agentd 侧 `*slog.Logger` 实例，config/cmd 包用 `slog` 默认 logger），**禁止 `fmt.Printf` 当日志**（CLI 面向用户的输出除外）。
- 注释规范：新文件顶部写「职责 + 边界」；导出函数写参数/返回/注意事项；复杂逻辑用中文写「为什么」。
- 每个 task 完成即 commit；提交信息中文、说清做了什么。
- 验收门（每个 task 的测试步骤都要过，最后 Task 6 全量再跑）：`go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./...` 全绿。
- 模块路径 `github.com/xushixin/handoff`。
- **决策已锁定，不要再权衡**（spec §4）：只做辅助监听（主监听绑不上仍启动失败）；辅助监听绑失败 fail-fast；CLI 确定性改写不做先试后退。

---

### Task 1: 共享判定函数 `config.ClassifyListen`

**Files:**
- Create: `internal/config/listenclass.go`
- Test: `internal/config/listenclass_test.go`

**Interfaces:**
- Consumes: 无（纯函数，仅标准库 `net`）
- Produces: `type ListenClass int`；常量 `ListenLoopback` / `ListenWildcard` / `ListenSingle`；`func ClassifyListen(listen string) (cls ListenClass, loopback string)`——Task 2/3/4 都调它

- [ ] **Step 1: 写失败测试**

`internal/config/listenclass_test.go`：

```go
// ClassifyListen 的表驱动测试：三档归类与 loopback 变体推导（B85）。
package config

import "testing"

func TestClassifyListen(t *testing.T) {
	cases := []struct {
		name, in string
		cls      ListenClass
		lo       string
	}{
		{"loopback v4", "127.0.0.1:7777", ListenLoopback, "127.0.0.1:7777"},
		{"loopback v4 非 .1", "127.0.0.2:7777", ListenLoopback, "127.0.0.2:7777"},
		{"loopback v6", "[::1]:7777", ListenLoopback, "[::1]:7777"},
		{"localhost", "localhost:7777", ListenLoopback, "localhost:7777"},
		{"通配 v4", "0.0.0.0:7777", ListenWildcard, "127.0.0.1:7777"},
		{"通配 v6", "[::]:7777", ListenWildcard, "127.0.0.1:7777"},
		{"空 host", ":7777", ListenWildcard, "127.0.0.1:7777"},
		{"单网卡 v4", "100.64.0.5:9999", ListenSingle, "127.0.0.1:9999"},
		{"单网卡 v6", "[fd7a:115c::1]:7777", ListenSingle, "127.0.0.1:7777"},
		{"主机名", "myhost.local:7777", ListenSingle, "127.0.0.1:7777"},
		{"缺端口", "127.0.0.1", ListenLoopback, "127.0.0.1"},
		{"乱码", "!!!", ListenLoopback, "!!!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cls, lo := ClassifyListen(c.in)
			if cls != c.cls || lo != c.lo {
				t.Fatalf("ClassifyListen(%q) = (%v, %q), want (%v, %q)",
					c.in, cls, lo, c.cls, c.lo)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestClassifyListen -v`
Expected: FAIL（`ListenClass` / `ClassifyListen` 未定义，编译错误）

- [ ] **Step 3: 实现**

`internal/config/listenclass.go`：

```go
// listenclass.go —— listen 地址的三档归类与 loopback 变体推导（B85）。
//
// 职责：
//   - 把 listen 的 host 归为 loopback / 通配 / 单点三档
//   - 对通配与单点给出 "127.0.0.1:<同端口>" 的 loopback 变体地址
//
// 边界：
//   - 纯函数，不做网络请求、不校验地址可绑性
//   - CLI（cmd/root.go 拨号改写）与 agentd（cmd/agentd.go 辅助监听）共用同一
//     口径——两处一旦发散就会出现「CLI 改写了、agentd 没绑」的连接拒绝，判定
//     必须唯一，这正是本文件存在的理由
//   - 与 cmd/init.go 的 listenKind 语义不同（那是 init 交互的预选口径，端口也
//     参与归类），刻意不合并（spec §3.1）
package config

import "net"

// ListenClass 是 listen 地址的三档归类。
type ListenClass int

const (
	// ListenLoopback：host 已是回环（127.x/::1/localhost），或 listen 解析失败——
	// 错的 listen 让 net.Listen 自己去报，归类函数不抢这个错误。
	ListenLoopback ListenClass = iota
	// ListenWildcard：通配（0.0.0.0/::/空 host），监听面已含 loopback。
	ListenWildcard
	// ListenSingle：单网卡 IP 或主机名——需要辅助监听的档位。
	ListenSingle
)

// ClassifyListen 把 listen 的 host 归为三档，并推导 loopback 变体地址。
//
// 参数：
//   - listen: 形如 "host:port" 的监听地址
//
// 返回：
//   - cls: 三档归类；解析失败归 ListenLoopback（即调用方什么都不做）
//   - loopback: 通配/单点档为 "127.0.0.1:<同端口>"；loopback 档（含解析失败）
//     原样返回 listen，调用方可无条件使用返回值
func ClassifyListen(listen string) (cls ListenClass, loopback string) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ListenLoopback, listen
	}
	if host == "" {
		return ListenWildcard, net.JoinHostPort("127.0.0.1", port)
	}
	// localhost 不是 IP 字面量，ParseIP 会失败落进单点档，必须先特判
	if host == "localhost" {
		return ListenLoopback, listen
	}
	ip := net.ParseIP(host)
	switch {
	case ip == nil:
		// 主机名：解析结果不可预知，按单点对待（辅助监听兜底本机可用性）
		return ListenSingle, net.JoinHostPort("127.0.0.1", port)
	case ip.IsLoopback():
		return ListenLoopback, listen
	case ip.IsUnspecified():
		return ListenWildcard, net.JoinHostPort("127.0.0.1", port)
	default:
		return ListenSingle, net.JoinHostPort("127.0.0.1", port)
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -run TestClassifyListen -v`
Expected: PASS（12 个子用例全绿）

- [ ] **Step 5: 注释自检**

纯函数无 I/O、无错误分支、无状态变更——按 instrumenting-code 豁免日志；注释已含文件头（职责+边界）、导出类型/函数 doc、`localhost` 特判的 why。确认无遗漏即可。

- [ ] **Step 6: Commit**

```bash
git add internal/config/listenclass.go internal/config/listenclass_test.go
git commit -m "feat(b85): listen 地址三档归类与 loopback 变体推导"
```

---

### Task 2: agentd 双监听（Shutdown 多地址 + 启动接线）

**Files:**
- Modify: `internal/agentd/shutdown.go`（`Serve` 变多地址、`serveWithListener` → `serveWithListeners`）
- Modify: `internal/agentd/shutdown_test.go`（既有用例适配 + 2 个新用例）
- Modify: `cmd/agentd.go:177-193`（监听地址列表 + 启动日志 `listen_aux`）

**Interfaces:**
- Consumes: `config.ClassifyListen`（Task 1）
- Produces: `func (s *Shutdown) Serve(srv *http.Server, cleanup func(), addrs ...string) error`——不传 addrs 时回退 `srv.Addr`（既有单测 `TestServeReturnsListenError` 不用改）；测试 seam `serveWithListeners(lns []net.Listener, srv *http.Server, cleanup func()) error`

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/shutdown_test.go` 追加（import 需补 `"strings"`）：

```go
// 双监听：两个地址都应答，Trigger 后两个一起停收。
//
// why 这条重要：B85 的辅助监听挂在同一个 http.Server 上，靠 srv.Shutdown
// 关掉全部 listener——若第二个 listener 没被追踪，关停后它还在收连接，
// 本机 CLI 会打到一个正在退出的进程上。
func TestServeMultipleListeners(t *testing.T) {
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NewServeMux()}
	sd := NewShutdown(quietLogger())
	done := make(chan error, 1)
	go func() { done <- sd.serveWithListeners([]net.Listener{ln1, ln2}, srv, func() {}) }()

	waitListening(t, ln1.Addr().String())
	waitListening(t, ln2.Addr().String())
	sd.Trigger("test")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅关停应返回 nil，得到 %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve 未在 10s 内返回")
	}
	for _, a := range []string{ln1.Addr().String(), ln2.Addr().String()} {
		if c, err := net.DialTimeout("tcp", a, time.Second); err == nil {
			c.Close()
			t.Fatalf("关停后 %s 仍在接受连接", a)
		}
	}
}

// 辅助地址绑不上必须整体启动失败（B85 决策：与主监听同等对待），
// 且错误报文要指明是哪个地址。
func TestServeAuxBindFailureFailsFast(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	srv := &http.Server{Handler: http.NewServeMux()}
	sd := NewShutdown(quietLogger())
	err = sd.Serve(srv, func() {}, "127.0.0.1:0", occupied.Addr().String())
	if err == nil {
		t.Fatal("辅助地址被占应启动失败")
	}
	if !strings.Contains(err.Error(), occupied.Addr().String()) {
		t.Fatalf("错误应指明绑不上的地址，got %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestServeMultipleListeners|TestServeAuxBindFailureFailsFast' -v`
Expected: FAIL（`serveWithListeners` 未定义 / `Serve` 参数不符，编译错误）

- [ ] **Step 3: 改造 shutdown.go**

`Serve` 与 seam 替换为（保留原有全部注释语义，函数 doc 更新）：

```go
// Serve 绑定 addrs 上的全部监听并阻塞，直到停机或任一监听失败。
//
// 参数：
//   - srv: 已配置好 Handler 的 HTTP 服务；addrs 为空时回退用 srv.Addr（单监听）
//   - cleanup: 停机时跑一次的清理闭包（关数据库、释放锁等）。**由调用方决定顺序**
//   - addrs: 监听地址列表（B85 双监听：主地址 + 可选的 loopback 辅址）
//
// 返回：
//   - nil 表示优雅关停完成（进程应 exit 0，管理器据此重新拉起）
//   - 非 nil 表示监听/启动失败（进程应 exit 1）。**任一地址绑不上都是启动失败**
//     （B85 决策：辅助监听与主监听同等对待，「第三档 = 两个监听都在」恒成立）
func (s *Shutdown) Serve(srv *http.Server, cleanup func(), addrs ...string) error {
	if len(addrs) == 0 {
		addrs = []string{srv.Addr}
	}
	lns := make([]net.Listener, 0, len(addrs))
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			// 已绑上的要收回：错误退出前不放掉，端口会占到进程结束，
			// 下一次启动尝试（管理器拉起）反而被自己挡住
			for _, held := range lns {
				held.Close()
			}
			// 端口被占是最常见的启动失败，报文里带上地址，别让用户去日志里找
			return fmt.Errorf("监听 %s: %w", a, err)
		}
		s.log.Debug("监听建立", "addr", ln.Addr().String())
		lns = append(lns, ln)
	}
	return s.serveWithListeners(lns, srv, cleanup)
}

// serveWithListeners 是 Serve 的可测形态：监听器由调用方给。
//
// 拆出来的理由：单测要在随机可用端口上跑（net.Listen ":0"），而 Serve
// 从地址串里取地址、拿不到实际分配的端口。测试拿着 listener 才能知道
// 该往哪儿探活。
//
// 多 listener 挂同一个 http.Server：net/http 自己追踪所有经 Serve 注入的
// listener，srv.Shutdown 会把它们全部关掉——优雅关停无需感知监听个数。
func (s *Shutdown) serveWithListeners(lns []net.Listener, srv *http.Server, cleanup func()) error {
	// 信号与进程内触发汇到同一个 Shutdown 上：signal.Notify 收到就转成一次 Trigger
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		s.Trigger("signal:" + sig.String())
	}()

	// 缓冲 = listener 数：每个 serve goroutine 恰好投递一次，谁都不会阻塞在投递上
	errCh := make(chan error, len(lns))
	for _, ln := range lns {
		go func(ln net.Listener) {
			// Serve 正常收到 Shutdown 时返回 ErrServerClosed，那是预期信号不是错误
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}(ln)
	}

	select {
	case err := <-errCh:
		// 还没触发停机，就有 Serve 返回了——第一个事件定性，不等其余
		if err != nil {
			s.log.Error("HTTP 服务异常退出", "cause", err)
			return fmt.Errorf("HTTP 服务: %w", err)
		}
		// 极少见：外部直接关了 srv。当作优雅关停处理，但要留痕
		s.log.Warn("HTTP 服务在未触发停机的情况下正常返回")
		cleanup()
		return nil
	case <-s.fired:
	}

	reason := s.Reason()
	s.log.Info("开始优雅关停", "reason", reason, "grace", ShutdownGrace)
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// 超时不算失败：在途请求没等完也要继续收尾，否则数据库永远关不掉。
		// 但必须留痕——它意味着有请求被硬断了
		s.log.Warn("等待在途请求超时，继续收尾", "cause", err, "grace", ShutdownGrace)
	}
	cleanup()
	s.log.Info("优雅关停完成", "reason", reason)
	return nil
}
```

既有用例适配：`grep -n serveWithListener internal/agentd/shutdown_test.go`，把所有 `sd.serveWithListener(ln, srv, ...)` 改为 `sd.serveWithListeners([]net.Listener{ln}, srv, ...)`（`TestServeReturnsNilOnGracefulShutdown` 至少一处；`TestServeReturnsListenError` 走 `Serve` 不用改）。

- [ ] **Step 4: cmd/agentd.go 接线**

替换 `cmd/agentd.go:177-179` 的启动日志与 `:193` 的 Serve 调用（`internal/config` 已随 `cfg` 在 import 里，若无则补）：

```go
// B85：listen 绑单网卡 IP 时追加 loopback 辅助监听，本机 CLI 恒走 127.0.0.1
//（spec §3.2）。任一地址绑不上都启动失败——辅助监听与主监听同等对待
listenAddrs := []string{cfg.Listen}
var listenAux string
if cls, aux := config.ClassifyListen(cfg.Listen); cls == config.ListenSingle {
	listenAux = aux
	listenAddrs = append(listenAddrs, aux)
}
startAttrs := []any{"addr", cfg.Listen, "data_dir", cfg.DataDir, "default_executor", cfg.Executor.Default,
	"proc_fence_disabled", cfg.ProcFence.Disabled,
	"proc_fence_reserve_ratio", cfg.ProcFence.ReserveRatio}
// 无辅助监听时不打 listen_aux 字段：两档常规配置的启动日志保持不变
if listenAux != "" {
	startAttrs = append(startAttrs, "listen_aux", listenAux)
}
logger.Info("agentd 服务启动", startAttrs...)
```

（中间的优雅关停注释块与 `sd := agentd.NewShutdown(logger)` 等行不动）

```go
return sd.Serve(newAgentdHTTPServer(cfg.Listen, srv.Handler()), wdCancel, listenAddrs...)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestServe|TestTrigger' -v && go build ./...`
Expected: 全 PASS（含适配后的既有用例），build 干净

- [ ] **Step 6: 日志与注释自检**

- 每个监听建立有 Debug（`监听建立 addr=...`）；任一绑失败错误带地址与 cause；启动 Info 在辅助监听时带 `listen_aux`——关键节点齐
- `Serve`/`serveWithListeners` doc 已更新为多地址语义；「已绑上的要收回」「缓冲 = listener 数」「第一个事件定性」三处 why 注释在位

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/shutdown.go internal/agentd/shutdown_test.go cmd/agentd.go
git commit -m "feat(b85): agentd 双监听——单网卡 listen 追加 loopback 辅助监听，任一绑失败即启动失败"
```

---

### Task 3: CLI 确定性改写（本机拨号地址决议）

**Files:**
- Modify: `cmd/root.go:133-136`（Endpoints 本机行）、`cmd/root.go:176-185`（TargetEndpoint 本机模式）
- Test: `cmd/root_test.go`

**Interfaces:**
- Consumes: `config.ClassifyListen`（Task 1）
- Produces: `func localDialAddr(listen string) string`（cmd 包内，返回带 `http://` 前缀的本机拨号地址）；`TargetEndpoint`/`Endpoints` 对外签名不变

- [ ] **Step 1: 写失败测试**

在 `cmd/root_test.go` 追加：

```go
// TestTargetEndpointLocalRewrite 覆盖 B85 的确定性改写：本机模式下 host 非
// loopback（单网卡 IP / 通配）一律改拨 127.0.0.1 同端口；显式 --agentd 不改写。
func TestTargetEndpointLocalRewrite(t *testing.T) {
	resetFlags(t)
	targetName = ""

	t.Run("单网卡 IP 改拨 loopback", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"100.64.0.5:9999\"\ntoken: \"tok\"\n")
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		addr, _, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://127.0.0.1:9999" {
			t.Fatalf("addr=%q, want http://127.0.0.1:9999（单网卡档靠辅助监听兜底）", addr)
		}
	})

	t.Run("通配也改拨 loopback", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"0.0.0.0:9999\"\ntoken: \"tok\"\n")
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		addr, _, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://127.0.0.1:9999" {
			t.Fatalf("addr=%q, want http://127.0.0.1:9999（拨 0.0.0.0 能通只是协议栈宽容）", addr)
		}
	})

	t.Run("显式 --agentd 不改写", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"100.64.0.5:9999\"\ntoken: \"tok\"\n")
		if err := rootCmd.PersistentFlags().Set("agentd", "http://100.64.0.5:9999"); err != nil {
			t.Fatalf("Set agentd flag: %v", err)
		}
		addr, _, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://100.64.0.5:9999" {
			t.Fatalf("addr=%q, 显式 --agentd 指明了端点就该照拨", addr)
		}
	})

	t.Run("Endpoints 本机行同样改写", func(t *testing.T) {
		configPath = writeTestConfig(t, "listen: \"100.64.0.5:9999\"\ntoken: \"tok\"\n")
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		eps, err := Endpoints("")
		if err != nil {
			t.Fatalf("Endpoints: %v", err)
		}
		if eps[0].Addr != "http://127.0.0.1:9999" {
			t.Fatalf("本机行 addr=%q, want http://127.0.0.1:9999（与 TargetEndpoint 同口径）", eps[0].Addr)
		}
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestTargetEndpointLocalRewrite -v`
Expected: FAIL（前两个与第四个子用例 addr 仍为配置原值）

- [ ] **Step 3: 实现 localDialAddr 并接线**

`cmd/root.go` 新增（放在 `Endpoints` 之前；import 需有 `"log/slog"`，`internal/config` 已在）：

```go
// localDialAddr 决议本机模式的拨号地址：host 非 loopback（通配或单网卡 IP）
// 一律改拨 127.0.0.1 同端口（B85 确定性改写，不做先试后退）——单网卡档靠
// agentd 的 loopback 辅助监听兜底，通配档 loopback 本就在监听面里。
//
// 参数：
//   - listen: 配置里的监听地址（可能带也可能不带 scheme；带 scheme 时
//     SplitHostPort 解析失败，归 loopback 档原样保留）
//
// 返回：
//   - 带 http:// 前缀的拨号地址
//
// 已知代价（spec §3.3，接受）：新 CLI + 旧 agentd（无辅助监听）且 listen 为
// 单网卡 IP 时本机命令连接拒绝，升级 agentd 即愈。
func localDialAddr(listen string) string {
	if cls, lo := config.ClassifyListen(listen); cls != config.ListenLoopback {
		// Debug 留痕：连接拒绝排障时第一个要回答的就是「它到底拨了哪」
		slog.Debug("本机拨号地址改写", "listen", listen, "dial", lo)
		listen = lo
	}
	if !strings.Contains(listen, "://") {
		listen = "http://" + listen
	}
	return listen
}
```

`Endpoints` 中（`cmd/root.go:133-136`）：

```go
	local := localDialAddr(cfg.Listen)
```

（删掉原来的 `if !strings.Contains(local, "://")` 两行补 scheme 逻辑，由 localDialAddr 统一做）

`TargetEndpoint` 中（`cmd/root.go:176-185`），else 分支改为：

```go
		// 地址优先级：显式 --agentd 优先（用户指明了别的端点）；未显式指定时
		// 由 localDialAddr 决议——loopback 照拨，通配/单网卡改拨 127.0.0.1
		//（B85，单网卡档靠 agentd 辅助监听兜底）
		if rootCmd.PersistentFlags().Changed("agentd") {
			addr = agentdURL
		} else {
			addr = localDialAddr(cfg.Listen)
		}
```

同时更新 `TargetEndpoint` 函数 doc 里「地址取 …… cfg.Listen（与 agentd 实际监听一致）」一句为「地址由 localDialAddr 决议（loopback 照拨，通配/单网卡改拨 127.0.0.1，B85）」。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestTargetEndpoint' -v`
Expected: 全 PASS（含既有 `TestTargetEndpointLocalAuth`——loopback 配置行为不变）

- [ ] **Step 5: 日志与注释自检**

- 改写动作有 Debug 留痕（排障回答「拨了哪」）；无新增错误分支
- `localDialAddr` doc 含参数/返回/已知代价；`TargetEndpoint` doc 已同步

- [ ] **Step 6: Commit**

```bash
git add cmd/root.go cmd/root_test.go
git commit -m "feat(b85): CLI 本机模式确定性改拨 loopback（显式 --agentd 不改写）"
```

---

### Task 4: status 语义（ListenAux 外露与呈现）

**Files:**
- Modify: `internal/proto/status.go:111-136`（StatusResp 加字段）
- Modify: `internal/agentd/status.go:66-75`（填充）
- Modify: `cmd/status.go`（renderStatus 加「监听」行）
- Test: `internal/agentd/status_test.go`、`cmd/status_test.go`

**Interfaces:**
- Consumes: `config.ClassifyListen`（Task 1）
- Produces: `proto.StatusResp.ListenAux string`（`json:"listen_aux,omitempty"`）——CLI 与 `--json` 消费方都靠它

- [ ] **Step 1: 写失败测试（服务端填充）**

在 `internal/agentd/status_test.go` 追加：

```go
// Listen 为单网卡 IP 时 ListenAux 必须给出 loopback 变体；Listen 保持
// cfg.Listen 不变（身份/配对语义，消费方不该看到列表）。loopback 配置恒为空。
func TestStatusListenAux(t *testing.T) {
	cfg := &config.Config{
		Token:    testToken,
		DataDir:  t.TempDir(),
		Listen:   "100.64.0.5:7777",
		Executor: config.ExecutorConfig{Default: "stub"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := newTestEnvWithCfg(t, cfg, logger)
	mgr := agentd.NewManager(env.st, env.srv.Hub(),
		map[string]executor.Adapter{"stub": &probeStub{alive: true}}, cfg, nil, nil, logger)

	st, err := mgr.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Listen != "100.64.0.5:7777" {
		t.Fatalf("Listen=%q, 应保持 cfg.Listen 原值", st.Listen)
	}
	if st.ListenAux != "127.0.0.1:7777" {
		t.Fatalf("ListenAux=%q, want 127.0.0.1:7777", st.ListenAux)
	}

	loop, err := newTestManager(t).Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if loop.ListenAux != "" {
		t.Fatalf("loopback 配置 ListenAux=%q, 应为空", loop.ListenAux)
	}
}
```

- [ ] **Step 2: 写失败测试（CLI 呈现）**

在 `cmd/status_test.go` 追加（对照 `TestRenderStatusMarksUnattended` 的直调形态；import 需有 `bytes`/`strings`）：

```go
// 有辅助监听时 status 文本带「监听」行；没有时不出现——两档常规配置输出不变。
func TestRenderStatusShowsListenAux(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, "http://127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		Listen: "100.64.0.5:7777", ListenAux: "127.0.0.1:7777",
		TaskCounts: map[string]int{},
	})
	if !strings.Contains(buf.String(), "监听     100.64.0.5:7777（辅 127.0.0.1:7777）") {
		t.Fatalf("输出缺监听行：\n%s", buf.String())
	}

	buf.Reset()
	renderStatus(&buf, "http://127.0.0.1:7777", proto.BuildInfo{}, &proto.StatusResp{
		Listen: "127.0.0.1:7777", TaskCounts: map[string]int{},
	})
	if strings.Contains(buf.String(), "监听") {
		t.Fatalf("无辅助监听时不该有监听行：\n%s", buf.String())
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestStatusListenAux -v; go test ./cmd/ -run TestRenderStatusShowsListenAux -v`
Expected: 两处 FAIL（`ListenAux` 未定义，编译错误）

- [ ] **Step 4: 实现**

`internal/proto/status.go` 在 `Listen` 字段后加：

```go
	// ListenAux 是 loopback 辅助监听地址（B85）：Listen 为单网卡 IP 时 agentd
	// 额外监听 "127.0.0.1:<同端口>"，本机 CLI 的确定性改写拨的就是它。
	// 空 = 无辅助监听（Listen 为 loopback/通配，或对端是老 agentd）。
	ListenAux string `json:"listen_aux,omitempty"`
```

`internal/agentd/status.go` 在 `resp := &proto.StatusResp{...}` 之后加（`internal/config` 已在 import，若无则补）：

```go
	// B85：单网卡监听时把 loopback 辅址外露给消费方；Listen 保持 cfg.Listen
	// 不变——它是身份/配对语义，不该变成列表
	if cls, aux := config.ClassifyListen(m.cfg.Listen); cls == config.ListenSingle {
		resp.ListenAux = aux
	}
```

`cmd/status.go` renderStatus 在「数据」行之后加：

```go
	// 只在有辅助监听时打这一行：两档常规配置的输出保持不变（B85）
	if st.ListenAux != "" {
		fmt.Fprintf(w, "监听     %s（辅 %s）\n", st.Listen, st.ListenAux)
	}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestStatus -v && go test ./cmd/ -run TestStatus -v && go test ./cmd/ -run TestRenderStatus -v`
Expected: 全 PASS（含既有 status 用例不回归）

- [ ] **Step 6: 日志与注释自检**

- 填充是纯派生（无 I/O、无错误分支），按 instrumenting-code 豁免日志；ListenAux 字段注释、填充与呈现处的 why 注释在位

- [ ] **Step 7: Commit**

```bash
git add internal/proto/status.go internal/agentd/status.go cmd/status.go internal/agentd/status_test.go cmd/status_test.go
git commit -m "feat(b85): status 外露 loopback 辅址（listen_aux）并在 CLI 呈现"
```

---

### Task 5: 文档更新（README + init 注释）

**Files:**
- Modify: `README.md:102`（三档改写）、`README.md:104`（红线开头一句）、`README.md:186`（配置示例注释）
- Modify: `cmd/init.go:311-312`（askListen 注释）、`cmd/init.go:319`（手填档 Label）

**Interfaces:**
- Consumes: 无
- Produces: 无（纯文档/文案）

- [ ] **Step 1: README「连接远程执行机」三档改写**

`README.md:102` 整段替换为：

```markdown
执行机的 `listen` 分三档：

- **`127.0.0.1:7777`（默认）**：仅本机。本机自用保持默认。
- **单网卡 IP（如 Tailscale 的 `100.x.y.z:7777`）**：只把 agentd 暴露给这一块网卡，安全面比 `0.0.0.0` 小。agentd 会自动追加一个 `127.0.0.1:同端口` 的辅助监听，本机命令始终走 loopback，不随网卡状态起伏。已知限制：该 IP 不在时（组网工具掉线期间重启、开机早于组网工具）agentd 起不来，托管形态下由 launchd/systemd 反复拉起，等 IP 回来自动就绪。
- **`0.0.0.0:7777`**：全网卡，接受任意网卡方向的远程派发。
```

`README.md:104` 红线段开头一句改为：

```markdown
**安全红线：把 agentd 暴露到网卡上（后两档）之前，确认这台机器没有直接暴露在公网。**
```

（其余内容不动）

- [ ] **Step 2: README 配置示例注释**

`README.md:186` 改为：

```markdown
listen: "127.0.0.1:7777"      # 监听三档：仅本机（默认）/ 单网卡 IP（自动补 loopback 辅助监听）/ "0.0.0.0:7777" 全网卡（见「连接远程执行机」）
```

- [ ] **Step 3: init.go 注释与手填档旁注**

`cmd/init.go:311-312` askListen 注释改为：

```go
// askListen 问监听三档。探到的网卡 IP 不主动写进 listen——绑单网卡时本机 CLI
// 靠 loopback 辅助监听兜底（B85），但 DHCP / Tailscale 一变（IP 不在）agentd
// 依旧起不来，预选仍只给 loopback / 全网卡两档，单网卡留给手填。IP 只出现在
// 配对片段。
```

`cmd/init.go:319` 手填档选项改为：

```go
		{Value: listenCustom, Label: "手填（如绑单个网卡 IP，本机命令自动走辅助监听）"},
```

- [ ] **Step 4: 验证**

Run: `go build ./... && go test ./cmd/ -run TestInit -v`
Expected: build 干净，init 既有用例全绿（用例断言的是 `cfg.Listen` 值，不断言 Label；若有 Label 断言随之更新）

- [ ] **Step 5: Commit**

```bash
git add README.md cmd/init.go
git commit -m "docs(b85): README 监听三档与 init 文案对齐辅助监听"
```

---

### Task 6: 全量回归 + 真机烟测

**Files:**
- Create: `docs/superpowers/notes/2026-08-13-loopback-aux-smoke.md`（烟测记录）

**Interfaces:**
- Consumes: 前五个 task 的全部产物
- Produces: 验收证据（backlog 验收列引用）

- [ ] **Step 1: 全量回归**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... && go test -race ./internal/agentd/ ./cmd/
```

Expected: gofmt 无输出，其余全绿

- [ ] **Step 2: 真机烟测——正常路径**

```bash
mkdir -p /tmp/b85/data
go build -o /tmp/b85/handoff .
IP=$(ipconfig getifaddr en0)
printf 'listen: "%s:7877"\ntoken: "b85-smoke"\ndata_dir: "/tmp/b85/data"\n' "$IP" > /tmp/b85/config.yaml
/tmp/b85/handoff agentd --config /tmp/b85/config.yaml &
```

断言（写进烟测记录）：
1. 启动日志含 `listen_aux=127.0.0.1:7877`；
2. `/tmp/b85/handoff status --config /tmp/b85/config.yaml` 退出码 0，首行 addr 为 `http://127.0.0.1:7877`（确定性改写生效），输出含 `监听     <IP>:7877（辅 127.0.0.1:7877）`；
3. 从局域网另一台机（或本机 `curl http://$IP:7877/api/status -H "Authorization: Bearer b85-smoke"`）经网卡 IP 可达。

- [ ] **Step 3: 真机烟测——辅址被占 fail-fast**

```bash
kill %1; nc -l 127.0.0.1 7877 &
/tmp/b85/handoff agentd --config /tmp/b85/config.yaml; echo "exit=$?"
kill %1
```

断言：进程启动失败（exit≠0），错误报文含 `监听 127.0.0.1:7877`。

- [ ] **Step 4: 写烟测记录并清理**

`docs/superpowers/notes/2026-08-13-loopback-aux-smoke.md` 记录上述三条断言的实测输出（日志行原文粘贴）；`rm -rf /tmp/b85`。

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/notes/2026-08-13-loopback-aux-smoke.md
git commit -m "test(b85): loopback 辅助监听真机烟测记录"
```
