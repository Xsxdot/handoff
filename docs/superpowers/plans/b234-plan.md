# B234：macOS can't assign requested address 与 PTY 收摊时序实现计划

## 0. 范围、输入与基线

本计划以已批准的 'docs/superpowers/specs/b234.md' r1 为唯一行为来源，基线是
'origin/fix/b234-macos-test-ports'，当前提交为 'a36ffb71'（包含 spec 提交）。本节点只产出
计划与台账，不实现代码；实施者只在当前分支工作，不切分支、不改 git 配置、不 push。

本卡只做两族行为：

1. 'internal/agentd' 测试 HTTP fixture 的 TCP 连接两端都使用 'SetLinger(0)'，并且只对
   测试二进制拨向 loopback 'httptest' 的 'Dial/DialContext' 重试
   'EADDRNOTAVAIL'/'can't assign requested address'，重试有上限并保留最终错误形状。
2. PTY 关闭链路的同步收口：'Engine.Close' 等现有唯一 reap；'Host.Close' 对 Open 会话等
   已保存的 'waitDone'，对 Adopt 会话等会话目录消失；'Host.Close' 的生产调用方
   DELETE、'shutdownPtySessions'、'CloseAll' 都观察到 shell/ptyhost 真正退出。

明确不做：生产 HTTP 'client.New' 默认 transport、'Client.Do'、WebSocket 帧/状态/协议字段、
DELETE 的 200/404 口径、'ptyShutdownWait' 与 'ptyCloseBudget' 数值、'CloseAll' 的超时返回
语义、PTY 字节转发语义。

### 0.1 已在基线复核的判据

以下命令均已在本计划节点亲自运行；原始输出与环境事实已逐条追加到
'docs/superpowers/plans/b234-plan-ledger.md'。Linux 绿不代表 macOS 绿。

| 变更面 | 基线命令 | 实际结果 |
|---|---|---|
| Engine/Host/hostproc/CloseAll | 'go test ./internal/ptyhost ./internal/ptyhost/engine ./internal/ptyhost/hostproc -run ''Test(ClientCloseRemovesSession\|CloseAllKillsLiveSessions\|CloseRemovesSession\|KillEndsProcessAndCleansDir)'' -count=1' | 退出 0；'ok github.com/Xsxdot/handoff/internal/ptyhost 0.025s'、'ok github.com/Xsxdot/handoff/internal/ptyhost/engine 0.006s'、'ok github.com/Xsxdot/handoff/internal/ptyhost/hostproc 0.008s' |
| agentd HTTP/WS | 'go test ./internal/agentd -run ''TestPtyWS(EchoRoundTrip\|Resize\|ResumeSince)|TestPtySessionListAndDelete'' -count=1' | 退出 0；'ok github.com/Xsxdot/handoff/internal/agentd 0.576s' |
| agentd 全测试二进制 | 'go test ./internal/agentd -count=1' | 退出 0；'ok  github.com/Xsxdot/handoff/internal/agentd 174.681s'；运行环境为 Linux，不是 macOS |
| service stop 接缝 | 'go test ./cmd -run ''TestService'' -count=1' | 退出 0；'ok github.com/Xsxdot/handoff/cmd 0.009s' |

实施者在每个 task 的第一步再次运行对应基线命令，若输出改变必须把实际原文写入台账；不能
把本计划中的预期当成已验证结果。

### 0.2 图与源码事实

仓内存在 'codegraph/best.json'。本节点已用
'go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . summary' 得到
'3636 节点 / 4735 边 / 20 领域'，并用 best 领域 id 查询 'd_gateway'、'd_sessions'。
context 输出存在 'truncated'/'fociTruncated' 预算字段；'who-calls Host.Close' 报告
'unscannedEntries: 6'，且混入 'conn.Close' 假边。因此下面的调用面以源码为准，图覆盖债
必须保留在实施台账，不能用“图中没有调用方”删掉测试。

已核对的真实签名与位置：

~~~go
func (h *ptyhost.Host) Close(id string) error // internal/ptyhost/client.go:284
func (h *engine.Engine) Close(id string) error // internal/ptyhost/engine/engine.go:299
func Run(specPath string) (runErr error) // internal/ptyhost/hostproc/hostproc.go:93
func CloseAll(root string, log *slog.Logger, budget time.Duration) (int, error) // internal/ptyhost/closeall.go:40
func (s *Server) shutdownPtySessions(ctx context.Context) // internal/agentd/ptyreclaim.go:90
func (s *Server) handleDeletePtySession(w http.ResponseWriter, r *http.Request) // internal/agentd/pty_api.go:244
~~~

### 0.3 实现顺序

Task A 先建立并迁移 HTTP fixture；Task B 修改 Engine 的单一 reap 等待；Task C 在 B 的绿色
结果上接通 Host/Open/Adopt 及三个生产关闭调用方。A 与 B 的文件集合不重叠，C 必须在 B
绿色后实施。

## 1. Task A：测试 HTTP fixture 的两端 linger、受限重试与 21 处迁移

### 1.1 文件范围

新增：

- 'internal/testhttp/server.go'
- 'internal/testhttp/server_test.go'
- 'internal/agentd/http_fixture_test.go'
- 'internal/agentd/no_direct_httptest_test.go'

修改以下 13 个已有测试文件中的 21 个直接构造点；只改 import、构造调用和清理注册，不改
已有 HTTP 断言：

- 'internal/agentd/cardstep_discipline_test.go'
- 'internal/agentd/cardstep_local_test.go'
- 'internal/agentd/diffbase_test.go'
- 'internal/agentd/forward_test.go'
- 'internal/agentd/hostguard_test.go'
- 'internal/agentd/integration_test.go'
- 'internal/agentd/ledgerapi_test.go'
- 'internal/agentd/machineupgrade_test.go'
- 'internal/agentd/mirror_test.go'
- 'internal/agentd/render_stream_test.go'
- 'internal/agentd/server_test.go'
- 'internal/agentd/w3a_testhelpers_test.go'
- 'internal/agentd/ws_regression_round2_test.go'

不迁移 'httptest.NewRequest'、'httptest.NewRecorder'，不修改生产包
'internal/client/client.go'。

### 1.2 Interfaces

'internal/testhttp' 对 'agentd' 与 'agentd_test' 的唯一新增接口如下，签名逐字固定：

~~~go
package testhttp

// DialContext is the context-aware dial signature wrapped by the fixture.
type DialContext func(context.Context, string, string) (net.Conn, error)

// MaxDialAttempts bounds retries for a single loopback address-allocation failure.
const MaxDialAttempts = 4

func NewServer(t testing.TB, handler http.Handler) *httptest.Server
func NewUnstartedServer(t testing.TB, handler http.Handler) *httptest.Server
func ConfigureClient(client *http.Client)
func RetryDialContext(base DialContext) DialContext
func CloseIdleConnections()
~~~

Consumes：标准库 'net/http/httptest.Server'、'http.Client'、'*http.Transport'、'net.Conn' 与
'context.Context'；调用方仍消费 '*httptest.Server'，所以现有结构体字段、'ts.URL'、'ts.Client()'
不变。

Produces：服务端 'Accept' 得到的每个 '*net.TCPConn' 调用 'SetLinger(0)'；被配置 client
拨向 loopback 的每个成功 '*net.TCPConn' 调用 'SetLinger(0)'；只有 loopback 且错误满足
'errors.Is(err, syscall.EADDRNOTAVAIL)' 或 'strings.Contains(err.Error(),
"can't assign requested address")' 时，最多调用 base dial 四次；最终返回最后一次原始
错误，不包一层改变错误文本或 'errors.Is' 链。

### 1.3 先红后绿：helper 的完整实现契约与测试

Task A 开始先运行 'go test ./internal/agentd -count=1'，确认表 0.1 的基线。然后新增
'server.go'，职责是测试 HTTP fixture 的连接生命周期；边界是只被测试代码导入，不改变
生产 client；文件头和每个导出函数必须写参数、返回值、连接范围和清理注意事项。

实现者按下列完整代码块落地；代码块中的每个符号都必须保留，不能把 retry 放到
'http.Client.Do' 或 'internal/client'：

~~~go
// server.go —— agentd 测试 HTTP fixture 的 TCP 连接收口与 loopback 拨号重试。
//
// 职责：包装 httptest 的服务端 Accept 与测试 client 的 DialContext，使临时端口
// 在 Darwin 上更快释放，并把极窄的 EADDRNOTAVAIL 重试限制在测试 fixture。
// 边界：本包只供测试导入；不修改生产 client.New、不重试 Client.Do；非 loopback
// 地址与非指定错误都只尝试一次。
package testhttp

import (
    "context"
    "errors"
    "net"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "syscall"
    "testing"
    "time"
)

type DialContext func(context.Context, string, string) (net.Conn, error)

const (
    MaxDialAttempts = 4
    dialRetryDelay = 10 * time.Millisecond
)

var setLinger = func(conn *net.TCPConn) error { return conn.SetLinger(0) }

var configuredTransports = struct {
    sync.Mutex
    set map[*http.Transport]struct{}
}{set: make(map[*http.Transport]struct{})}

// NewServer starts a linger-enabled httptest server, configures the default client and
// registers cleanup that closes the server before tracked idle transports.
func NewServer(t testing.TB, handler http.Handler) *httptest.Server {
    t.Helper()
    oldDefault := http.DefaultClient.Transport
    ts := httptest.NewUnstartedServer(handler)
    ts.Listener = &lingerListener{Listener: ts.Listener}
    ts.Start()
    ConfigureClient(http.DefaultClient)
    ConfigureClient(ts.Client())
    t.Cleanup(func() {
        ts.Close()
        CloseIdleConnections()
        http.DefaultClient.Transport = oldDefault
    })
    return ts
}

// NewUnstartedServer returns a linger-enabled server whose caller may replace Listener or
// modify Config before Start. The caller must call ConfigureClient(ts.Client()) after Start
// because httptest creates its client inside Start.
func NewUnstartedServer(t testing.TB, handler http.Handler) *httptest.Server {
    t.Helper()
    oldDefault := http.DefaultClient.Transport
    ts := httptest.NewUnstartedServer(handler)
    ts.Listener = &lingerListener{Listener: ts.Listener}
    ConfigureClient(http.DefaultClient)
    t.Cleanup(func() {
        ts.Close()
        CloseIdleConnections()
        http.DefaultClient.Transport = oldDefault
    })
    return ts
}

// ConfigureClient replaces only a *http.Transport's dial hooks. A nil Transport is cloned
// from http.DefaultTransport so the process-global default is never mutated; a non-*http.Transport
// is intentionally left unchanged because it has no safe TCP hook.
func ConfigureClient(client *http.Client) {
    if client == nil {
        return
    }
    tr, ok := client.Transport.(*http.Transport)
    if client.Transport == nil {
        defaultTransport, defaultOK := http.DefaultTransport.(*http.Transport)
        if !defaultOK {
            return
        }
        tr = defaultTransport.Clone()
        client.Transport = tr
        ok = true
    }
    if !ok {
        return
    }
    if defaultTransport, defaultOK := http.DefaultTransport.(*http.Transport); defaultOK && tr == defaultTransport {
        tr = defaultTransport.Clone()
        client.Transport = tr
    }
    configuredTransports.Lock()
    if _, exists := configuredTransports.set[tr]; exists {
        configuredTransports.Unlock()
        return
    }
    configuredTransports.set[tr] = struct{}{}
    configuredTransports.Unlock()

    baseContext := tr.DialContext
    baseDial := tr.Dial
    if baseContext == nil {
        if baseDial != nil {
            baseContext = func(_ context.Context, network, addr string) (net.Conn, error) {
                return baseDial(network, addr)
            }
        } else {
            dialer := &net.Dialer{}
            baseContext = dialer.DialContext
        }
    }
    tr.Dial = nil
    tr.DialContext = RetryDialContext(DialContext(baseContext))
}

// RetryDialContext retries only the two Darwin loopback address-allocation errors and
// only up to MaxDialAttempts. A successful loopback connection gets client-side linger.
func RetryDialContext(base DialContext) DialContext {
    if base == nil {
        dialer := &net.Dialer{}
        base = dialer.DialContext
    }
    return func(ctx context.Context, network, addr string) (net.Conn, error) {
        var last error
        for attempt := 0; attempt < MaxDialAttempts; attempt++ {
            conn, err := base(ctx, network, addr)
            if err == nil {
                return prepareClientConn(conn, addr)
            }
            last = err
            if !isLoopbackAddr(addr) || !retryableAddressError(err) || attempt+1 == MaxDialAttempts {
                return nil, err
            }
            timer := time.NewTimer(dialRetryDelay)
            select {
            case <-ctx.Done():
                if !timer.Stop() {
                    <-timer.C
                }
                return nil, ctx.Err()
            case <-timer.C:
            }
        }
        return nil, last
    }
}

// CloseIdleConnections closes tracked idle transports and the standard default transport.
// It is intentionally separate from request cancellation.
func CloseIdleConnections() {
    if tr, ok := http.DefaultTransport.(*http.Transport); ok {
        tr.CloseIdleConnections()
    }
    configuredTransports.Lock()
    all := make([]*http.Transport, 0, len(configuredTransports.set))
    for tr := range configuredTransports.set {
        all = append(all, tr)
    }
    configuredTransports.Unlock()
    for _, tr := range all {
        tr.CloseIdleConnections()
    }
}

type lingerListener struct{ net.Listener }

func (l *lingerListener) Accept() (net.Conn, error) {
    conn, err := l.Listener.Accept()
    if err != nil {
        return nil, err
    }
    tcp, ok := conn.(*net.TCPConn)
    if !ok {
        conn.Close()
        return nil, errors.New("testhttp: httptest listener returned non-TCP connection")
    }
    if err := setLinger(tcp); err != nil {
        conn.Close()
        return nil, err
    }
    return tcp, nil
}

func prepareClientConn(conn net.Conn, addr string) (net.Conn, error) {
    if !isLoopbackAddr(addr) {
        return conn, nil
    }
    tcp, ok := conn.(*net.TCPConn)
    if !ok {
        conn.Close()
        return nil, errors.New("testhttp: loopback dial returned non-TCP connection")
    }
    if err := setLinger(tcp); err != nil {
        conn.Close()
        return nil, err
    }
    return tcp, nil
}

func isLoopbackAddr(addr string) bool {
    host, _, err := net.SplitHostPort(addr)
    if err != nil {
        return false
    }
    host = strings.Trim(host, "[]")
    if strings.EqualFold(host, "localhost") {
        return true
    }
    ip := net.ParseIP(host)
    return ip != nil && ip.IsLoopback()
}

func retryableAddressError(err error) bool {
    return errors.Is(err, syscall.EADDRNOTAVAIL) || strings.Contains(err.Error(), "can't assign requested address")
}
~~~

在 'internal/testhttp/server_test.go' 复用包内既有 test hook（不新增生产导出 hook）。完整锁缝
代码如下；这些测试不使用 't.Parallel'，因为它们替换包内 'setLinger' 变量：

~~~go
package testhttp

import (
    "context"
    "errors"
    "io"
    "net"
    "net/http"
    "strings"
    "sync/atomic"
    "syscall"
    "testing"
)

func TestNewServerSetsLingerOnAcceptAndDefaultClientDial(t *testing.T) {
    var calls atomic.Int32
    old := setLinger
    setLinger = func(*net.TCPConn) error {
        calls.Add(1)
        return nil
    }
    t.Cleanup(func() { setLinger = old })
    ts := NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _, _ = io.WriteString(w, "ok")
    }))
    resp, err := http.DefaultClient.Get(ts.URL)
    if err != nil {
        t.Fatalf("default client GET: %v", err)
    }
    defer resp.Body.Close()
    if body, err := io.ReadAll(resp.Body); err != nil || string(body) != "ok" {
        t.Fatalf("body=%q err=%v", body, err)
    }
    if got := calls.Load(); got < 2 {
        t.Fatalf("SetLinger 调用次数=%d，Accept 与 client Dial 两侧都必须调用", got)
    }
}

type addressUnavailable struct{}

func (addressUnavailable) Error() string { return "can't assign requested address" }
func (addressUnavailable) Unwrap() error { return syscall.EADDRNOTAVAIL }

func TestRetryDialContextIsBoundedAndPreservesError(t *testing.T) {
    var calls atomic.Int32
    dial := RetryDialContext(func(context.Context, string, string) (net.Conn, error) {
        calls.Add(1)
        return nil, addressUnavailable{}
    })
    _, err := dial(context.Background(), "tcp", "127.0.0.1:1")
    if err == nil {
        t.Fatal("地址不可用时必须返回最终错误")
    }
    if got := calls.Load(); got != MaxDialAttempts {
        t.Fatalf("dial 次数=%d，期望上限 %d", got, MaxDialAttempts)
    }
    if !errors.Is(err, syscall.EADDRNOTAVAIL) || !strings.Contains(err.Error(), "can't assign requested address") {
        t.Fatalf("最终错误丢失原始形状: %v", err)
    }
}

func TestRetryDialContextDoesNotRetryNonLoopback(t *testing.T) {
    var calls atomic.Int32
    dial := RetryDialContext(func(context.Context, string, string) (net.Conn, error) {
        calls.Add(1)
        return nil, addressUnavailable{}
    })
    _, err := dial(context.Background(), "tcp", "192.0.2.1:1")
    if err == nil || calls.Load() != 1 {
        t.Fatalf("非 loopback 必须一次返回原始错误，calls=%d err=%v", calls.Load(), err)
    }
}
~~~

先运行：

~~~text
go test ./internal/testhttp -run 'Test(NewServer|RetryDialContext)' -count=1
~~~

### 1.4 迁移 21 个调用点与清理顺序

普通调用从 'httptest.NewServer' 改为 'testhttp.NewServer(t, handler)'，并删除同一构造点旁
的 't.Cleanup(ts.Close)'；helper 已统一注册“先关 server、再关 tracked idle transport、再
恢复 DefaultClient.Transport”。保留 '*httptest.Server' 字段类型、'ts.URL'、'ts.Client()'
和所有既有响应断言。

逐文件映射如下：

| 文件 | 需迁移的构造语句 | 额外动作 |
|---|---|---|
| 'cardstep_discipline_test.go' | 两处 'httptest.NewServer(http.HandlerFunc(...))' | 传入当前 't'，删除直接 Close 清理 |
| 'cardstep_local_test.go' | 'h.ts = httptest.NewServer(...)' | 改为 'h.ts = testhttp.NewServer(t, ...)' |
| 'diffbase_test.go' | 'httptest.NewServer(srv.Handler())' | 改 helper并删除重复 Close |
| 'forward_test.go' | 四处 'remote := httptest.NewServer(...)' | 远端 fixture 也必须 linger |
| 'hostguard_test.go' | 'newHostTestEnv' 内一处 | helper 保持签名，内部改造 |
| 'integration_test.go' | 'newIntegEnvCfg' 一处、启动失败测试一处 | 两处传当前 't'；三处 'client.New' 仅由新测试注入 retry |
| 'ledgerapi_test.go' | 一处 | 改 helper并删除重复 Close |
| 'machineupgrade_test.go' | 一处返回 server 的 helper | 让 helper 接收现有 't' |
| 'mirror_test.go' | 'newMirrorHTTPEnv' 一处、'countedMirrorTarget' 一处 | 两个 helper 都已有 't' |
| 'render_stream_test.go' | 一处 | 改 helper并删除重复 Close |
| 'server_test.go' | 一处 | 黑盒包导入 'internal/testhttp'，保留返回类型 |
| 'w3a_testhelpers_test.go' | 一处 | 删除直接 't.Cleanup(ts.Close)'，PTY cleanup 仍按既有顺序 |
| 'ws_regression_round2_test.go' | 普通一处、Unstarted 一处 | 非零分支保留 'sockBufListener'，'Start()' 后立即 'ConfigureClient(ts.Client())' |

新增 'no_direct_httptest_test.go'，用源码扫描锁住所有 21 处迁移；它是附加内部锁，合法理由
是运行时请求构造不出“未被调用的残留源码”。完整代码：

~~~go
// no_direct_httptest_test.go —— 防止 agentd 测试绕过 B234 fixture helper。
// 职责：扫描 internal/agentd 的 Go 测试源码，禁止直接构造 NewServer/NewUnstartedServer。
// 边界：只审源码迁移纪律，不替代真实 HTTP/WS/client.New 拨号接缝测试。
package agentd

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestNoDirectHttptestServers(t *testing.T) {
    err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
        if walkErr != nil {
            return walkErr
        }
        if entry.IsDir() || !strings.HasSuffix(path, ".go") {
            return nil
        }
        body, err := os.ReadFile(path)
        if err != nil {
            return err
        }
        text := string(body)
        for _, forbidden := range []string{"httptest.New" + "Server(", "httptest.NewUnstarted" + "Server("} {
            if index := strings.Index(text, forbidden); index >= 0 {
                line := 1 + strings.Count(text[:index], "\n")
                return fmt.Errorf("%s:%d 仍直接调用 %s，请改用 internal/testhttp", path, line, forbidden)
            }
        }
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }
}
~~~

### 1.5 三条真实拨号接缝回归

新增 'internal/agentd/http_fixture_test.go'，从三条入口各发起一次 loopback 失败拨号，
不把 helper 纯函数当替代：

~~~go
// http_fixture_test.go —— B234 三条 agentd 测试拨号路线都受同一上限约束。
// 入口：http.DefaultClient.Do、websocket.Dial、internal/client.Client.HTTPClient().Do。
// 边界：只替换测试对象 transport；不修改 internal/client 的生产默认配置。
package agentd

import (
    "context"
    "errors"
    "net"
    "net/http"
    "strings"
    "syscall"
    "testing"

    handoffclient "github.com/Xsxdot/handoff/internal/client"
    "github.com/Xsxdot/handoff/internal/testhttp"
    "github.com/coder/websocket"
)

type b234AddressUnavailable struct{}

func (b234AddressUnavailable) Error() string { return "can't assign requested address" }
func (b234AddressUnavailable) Unwrap() error { return syscall.EADDRNOTAVAIL }

func assertB234RetryRoute(t *testing.T, run func(testhttp.DialContext) error) {
    t.Helper()
    calls := 0
    failing := testhttp.DialContext(func(context.Context, string, string) (net.Conn, error) {
        calls++
        return nil, b234AddressUnavailable{}
    })
    err := run(testhttp.RetryDialContext(failing))
    if err == nil {
        t.Fatal("loopback address unavailable must return an error")
    }
    if calls != testhttp.MaxDialAttempts {
        t.Fatalf("dial calls=%d, want retry upper bound %d", calls, testhttp.MaxDialAttempts)
    }
    if !errors.Is(err, syscall.EADDRNOTAVAIL) || !strings.Contains(err.Error(), "can't assign requested address") {
        t.Fatalf("final error lost address-allocation shape: %v", err)
    }
}

func TestHTTPFixtureDialRoutesReachRetryLimit(t *testing.T) {
    t.Run("http.DefaultClient", func(t *testing.T) {
        assertB234RetryRoute(t, func(dial testhttp.DialContext) error {
            old := http.DefaultClient.Transport
            tr := &http.Transport{DialContext: dial}
            http.DefaultClient.Transport = tr
            t.Cleanup(func() {
                tr.CloseIdleConnections()
                http.DefaultClient.Transport = old
            })
            _, err := http.DefaultClient.Get("http://127.0.0.1:1/")
            return err
        })
    })
    t.Run("websocket.Dial default client", func(t *testing.T) {
        assertB234RetryRoute(t, func(dial testhttp.DialContext) error {
            old := http.DefaultClient.Transport
            tr := &http.Transport{DialContext: dial}
            http.DefaultClient.Transport = tr
            t.Cleanup(func() {
                tr.CloseIdleConnections()
                http.DefaultClient.Transport = old
            })
            _, _, err := websocket.Dial(context.Background(), "ws://127.0.0.1:1/", nil)
            return err
        })
    })
    t.Run("client.New custom transport", func(t *testing.T) {
        assertB234RetryRoute(t, func(dial testhttp.DialContext) error {
            client := handoffclient.New("http://127.0.0.1:1", "")
            tr, ok := client.HTTPClient().Transport.(*http.Transport)
            if !ok {
                t.Fatalf("client.New transport type=%T, want *http.Transport", client.HTTPClient().Transport)
            }
            tr.Dial = nil
            tr.DialContext = dial
            _, err := client.HTTPClient().Get("http://127.0.0.1:1/")
            return err
        })
    })
}
~~~

'websocket.Dial' 的 nil options 必须保留，实测依赖源码规定它回落 'http.DefaultClient'；
'client.New' 只在测试里替换它已经创建的 transport。先跑：

~~~text
go test ./internal/agentd -run 'TestHTTPFixtureDialRoutesReachRetryLimit|TestNoDirectHttptestServers' -count=1
~~~

### 1.6 Task A 日志、注释、绿测与验收

- 新文件头和五个导出符号按 1.3 完成注释；retry 失败不静默吞错，最终保留 base dial
  错误；纯测试包不引入 print。
- 21 个调用点的成功/错误断言保持原样；'CloseIdleConnections' 只放在 server close 之后。
- 运行范围只触及 'internal/testhttp' 与 'internal/agentd'：

~~~text
go test ./internal/testhttp -count=1
go test ./internal/agentd -run 'TestHTTPFixtureDialRoutesReachRetryLimit|TestNoDirectHttptestServers|TestPtySessionListAndDelete|TestPtyWS(EchoRoundTrip|Resize|ResumeSince)' -count=1
go test ./internal/agentd -count=1
~~~

三条真实路由都必须到 retry 上限且错误同时满足 'errors.Is(EADDRNOTAVAIL)' 和指定文案；负扫描
与 agentd 全包都必须退出 0。任何失败只记录原始输出，不能推断为“网络原因”。

## 2. Task B：Engine.Close 等唯一 reap，hostproc 清理等待真实退出

### 2.1 文件范围与接口

修改：

- 'internal/ptyhost/engine/engine.go'
- 'internal/ptyhost/engine/initcmd_test.go'
- 'internal/ptyhost/engine/engine_test.go'（只在已有 Close 测试需要补断言时改）
- 'internal/ptyhost/hostproc/hostproc.go'
- 'internal/ptyhost/hostproc/hostproc_test.go'

不修改 'termGrace = 2 * time.Second'、'waitExitCode'、进程组信号实现或 wire 协议。

接口保持：

~~~go
func (h *Engine) Open(opt ptyhost.OpenOptions) (ptyhost.Session, error)
func (h *Engine) Close(id string) error
func Run(specPath string) (runErr error)
~~~

Consumes：已有 'session.reap' 对 'cmd.Wait' 的唯一拥有权、'terminatePty'/'killPty' 的进程组
信号函数、'hostproc.Run' defer。

Produces：'Engine.Close' 从会话表同步摘除后发送 SIGTERM；宽限期内等 'reap' 完成；超时对
同一进程组发送 SIGKILL 后继续等同一 'reap'；只有 reap 已落 exit code、关闭订阅和 PTY
文件后才返回。不得新增第二次 'cmd.Wait'，不得把等待重新放到后台 goroutine。

### 2.2 先红后绿：可观察的 late marker 回路

Task B 第一动作先运行：

~~~text
go test ./internal/ptyhost/engine ./internal/ptyhost/hostproc -run 'Test(Close|Kill|CloseRemovesSession|KillEndsProcessAndCleansDir)' -count=1
~~~

在已有 'internal/ptyhost/engine/initcmd_test.go' 的 'openAndCollect' harness 上新增测试。
复用理由：它已经从 'Engine.Open'→'Attach'→真实 PTY 输出进入，无法由更短的内部 fake 构造
shell 收摊时序；断言 ready 文件、旧实现不得提前返回、释放 FIFO 后 late、会话从列表消失。
此复用例外在计划末尾再次声明。

先新增两个测试辅助函数（放在 'initcmd_test.go'，不使用固定 'time.Sleep'）：

~~~go
func waitFile(path string, within time.Duration) bool {
    deadline := time.Now().Add(within)
    ticker := time.NewTicker(10 * time.Millisecond)
    defer ticker.Stop()
    for {
        if _, err := os.Stat(path); err == nil {
            return true
        }
        if time.Now().After(deadline) {
            return false
        }
        <-ticker.C
    }
}

func releaseFIFO(t *testing.T, path string) {
    t.Helper()
    done := make(chan error, 1)
    go func() {
        f, err := os.OpenFile(path, os.O_WRONLY, 0)
        if err != nil {
            done <- err
            return
        }
        _, writeErr := f.WriteString("release\n")
        closeErr := f.Close()
        if writeErr != nil {
            done <- writeErr
            return
        }
        done <- closeErr
    }()
    select {
    case err := <-done:
        if err != nil {
            t.Fatalf("释放 shell trap FIFO: %v", err)
        }
    case <-time.After(time.Second):
        t.Fatal("shell trap 未打开 release FIFO")
    }
}
~~~

再新增锁缝测试：

~~~go
func TestCloseWaitsForReapAndLateTrap(t *testing.T) {
    home := t.TempDir()
    ready := filepath.Join(home, "b234-ready")
    release := filepath.Join(home, "b234-release")
    late := filepath.Join(home, "b234-late")
    h, sess, _ := openAndCollect(t, ptyhost.OpenOptions{
        Shell: "/bin/sh", BasePath: home, BaseKind: "home",
        Env: append(os.Environ(), "HOME="+home),
        InitCommand: `mkfifo "$HOME/b234-release"; trap 'cat "$HOME/b234-release" >/dev/null; printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"`,
    })
    if !waitFile(ready, 3*time.Second) {
        t.Fatal("InitCommand 未建立 ready marker，测试前提不成立")
    }
    closeDone := make(chan error, 1)
    go func() { closeDone <- h.Close(sess.ID) }()
    early := false
    var earlyErr error
    select {
    case earlyErr = <-closeDone:
        early = true
    case <-time.After(250 * time.Millisecond):
    }
    if early {
        releaseFIFO(t, release)
        if earlyErr != nil {
            t.Fatalf("提前返回的 Close 错误: %v", earlyErr)
        }
        if !waitFile(late, time.Second) {
            t.Fatal("提前返回的 Close 后 EXIT trap 仍未写入 late marker")
        }
        t.Fatal("Engine.Close 在 EXIT trap 写入 late marker 前返回")
    }
    releaseFIFO(t, release)
    select {
    case err := <-closeDone:
        if err != nil {
            t.Fatalf("等待 reap 的 Close: %v", err)
        }
    case <-time.After(3 * time.Second):
        t.Fatal("释放 trap 后 Close 未等待 reap 返回")
    }
    if _, err := os.Stat(late); err != nil {
        t.Fatalf("Close 返回后 late marker 不存在: %v", err)
    }
    if _, ok := h.Get(sess.ID); ok {
        t.Fatal("Close 返回后会话仍在 Engine 列表")
    }
}
~~~

补齐 imports：'path/filepath'、'os' 已有则不重复；保留现有空命令、首字节、兜底写入测试。
先运行该单测并把实际输出落台账。基线若意外退出 0，不改入口、不把它写成红；只记录实际
结果，实施后的同一断言仍必须通过。

### 2.3 Engine 最小实现

在 'session' 增加私有完成通道；这是 reap 与 Close 的内部接口，不新增包外 API：

~~~go
type session struct {
    mu       sync.Mutex
    meta     ptyhost.Session
    f        *os.File
    cmd      *exec.Cmd
    buf      *ring
    subs     map[*subscriber]struct{}
    exited   bool
    exitCode *int
    exitedDone chan struct{}
    firstOut chan struct{}
    firstOutOnce sync.Once
}
~~~

'Engine.Open' 的 session literal 增加 'exitedDone: make(chan struct{})'。替换 'reap' 为下面
完整函数：先在锁内落状态并关闭订阅，再关 PTY 文件，最后关闭完成通道；关闭通道的时刻就是
Close 可以安全返回的时刻。注释必须说明已有 reap 是唯一 'cmd.Wait' 所有者。

~~~go
// reap 是每个 shell 唯一的 cmd.Wait 所有者。
//
// exitedDone 只在 exit code、订阅通道和 PTY 文件都收口后关闭；Engine.Close 只能等
// 这个通道，禁止另起 cmd.Wait，否则 os/exec 的 Wait 竞态会把 exit code 与清理顺序拆开。
func (h *Engine) reap(s *session) {
    code := waitExitCode(s.cmd)
    s.mu.Lock()
    s.exited = true
    s.exitCode = &code
    for sub := range s.subs {
        close(sub.ch)
    }
    s.subs = map[*subscriber]struct{}{}
    s.mu.Unlock()
    _ = s.f.Close()
    close(s.exitedDone)
    h.log.Info("终端会话已退出", "session", s.meta.ID, "pid", s.meta.PID, "exit_code", code)
}
~~~

替换 'Engine.Close' 为：

~~~go
// Close 显式关闭会话：整组 SIGTERM，宽限 termGrace 后 SIGKILL，并等待已有 reap。
//
// 会话表仍在发信号前摘除，保持 List/第二次 Close 的同步语义；但函数返回前必须
// 观察 exitedDone，确保 shell 的 EXIT trap、exit code、订阅关闭和 PTY 文件关闭已经完成。
func (h *Engine) Close(id string) error {
    h.mu.Lock()
    s, ok := h.sess[id]
    if ok {
        delete(h.sess, id)
    }
    remain := len(h.sess)
    h.mu.Unlock()
    if !ok {
        return ptyhost.ErrNoSession
    }
    started := time.Now()
    if err := terminatePty(s.cmd); err != nil {
        h.log.Error("终止终端会话失败", "session", id, "pid", s.meta.PID, "phase", "sigterm", "err", err)
    }
    sigkill := false
    timer := time.NewTimer(termGrace)
    defer timer.Stop()
    select {
    case <-s.exitedDone:
    case <-timer.C:
        sigkill = true
        h.log.Warn("终端会话在宽限期内未退出，强制终止", "session", id,
            "pid", s.meta.PID, "grace", termGrace)
        if err := killPty(s.cmd); err != nil {
            h.log.Error("强制终止终端会话失败", "session", id, "pid", s.meta.PID, "phase", "sigkill", "err", err)
        }
        <-s.exitedDone
    }
    h.log.Info("终端会话已关闭", "session", id, "pid", s.meta.PID,
        "sessions", remain, "sigkill", sigkill, "elapsed", time.Since(started))
    return nil
}
~~~

关键日志必须包含 session、pid、phase、sigkill、elapsed；成功路径不能静默，SIGTERM 和 SIGKILL
错误不能只在返回值中丢失。不要改 'termGrace'，不要保留原来后台
'time.Sleep(termGrace)' 的 goroutine。

### 2.4 hostproc 接缝与测试

'hostproc.go' 只更新 Run defer 与 'stopNow' 的注释/日志上下文，不新增第二次 kill 或 Wait。
保留 'Run(specPath string) (runErr error)' 签名和现有顺序：先 stopNow，再 eng.Close，再删
socket/锁/目录；eng.Close 已等待同一 reap。'Run' 的关键日志带 session、engine_id、phase、
错误/耗时。

Run defer 的关键段按下面完整代码替换，后续 socket、log、lock、目录清理保持原顺序；
'stopNow' 注释明确它只关闭 listener/连接：

~~~go
defer func() {
    if srv != nil {
        srv.stopNow()
    }
    if eng != nil && engineID != "" {
        closeStarted := time.Now()
        log.Info("ptyhost 收摊：等待 Engine reap", "session", spec.ID,
            "engine_id", engineID, "phase", "engine_close")
        if err := eng.Close(engineID); err != nil && !errors.Is(err, ptyhost.ErrNoSession) {
            log.Warn("ptyhost 收摊时关闭会话失败", "session", spec.ID,
                "engine_id", engineID, "phase", "engine_close", "elapsed", time.Since(closeStarted), "err", err)
        } else {
            log.Info("ptyhost 收摊：Engine reap 已完成", "session", spec.ID,
                "engine_id", engineID, "phase", "engine_close", "elapsed", time.Since(closeStarted))
        }
    }
    if ln != nil {
        _ = ln.Close()
    }
    _ = os.Remove(sessdir.SockPath(spec.Root, spec.ID))
    if !cleaned {
        log.Info("ptyhost 收摊完成", "session", spec.ID, "engine_id", engineID, "phase", "socket_close")
    }
    if err := logFile.Close(); err != nil && runErr == nil {
        runErr = fmt.Errorf("关闭 ptyhost 日志 %s: %w", logPath, err)
    }
    if err := lock.Release(); err != nil && runErr == nil {
        runErr = fmt.Errorf("释放 ptyhost 会话锁 %s: %w", lockPath, err)
    }
    if err := sessdir.Remove(spec.Root, spec.ID); err != nil && runErr == nil {
        runErr = fmt.Errorf("清理 ptyhost 会话目录 %s: %w", sessdir.Dir(spec.Root, spec.ID), err)
    }
    cleaned = true
}()
~~~

把已有 'TestKillEndsProcessAndCleansDir' 升级为真实 late marker 断言：'Spec.Env' 增加
'HOME=<t.TempDir()>'，'InitCommand' 使用
'trap ''printf late > "$HOME/b234-late"'' EXIT; : > "$HOME/b234-ready"'；轮询 ready 后
发送 CtrlKill；等待 Run 返回；逐条断言 Run 为 nil、late 文件存在、session 目录 'os.IsNotExist'。
轮询用 ticker，不加入固定 'time.Sleep'。保留 meta、Attach、socket 既有断言。

测试范围：

~~~text
go test ./internal/ptyhost/engine ./internal/ptyhost/hostproc -run 'Test(CloseWaitsForReapAndLateTrap|CloseRemovesSession|KillEndsProcessAndCleansDir)' -count=1
go test ./internal/ptyhost/engine ./internal/ptyhost/hostproc -count=1
~~~

Task B 绿判据：新 Engine seam 不再在释放 FIFO 前返回；'late' 存在；hostproc Run 返回后目录
不存在；'rg -n ''cmd\.Wait\('' internal/ptyhost/engine' 只有已有 'platform_unix.go' 的
'waitExitCode' 使用，Engine 不新增第二个 Wait。实际命令输出必须落台账。

## 3. Task C：Host Open/Adopt 等待、DELETE/Shutdown/CloseAll 与服务停止接缝

### 3.1 文件范围与接口

修改：

- 'internal/ptyhost/client.go'
- 'internal/ptyhost/client_test.go'
- 'internal/ptyhost/survive_test.go'
- 'internal/ptyhost/closeall_test.go'
- 'internal/agentd/ptyreclaim.go'（只更新 shutdown 注释/结构化日志）
- 'internal/agentd/ptyreclaim_test.go'
- 'cmd/service.go'（只更新 CloseAll 预算注释）
- 'cmd/service_pty_test.go'（新增 service stop→CloseAll 真实接缝测试）

'pty_api.go' 的生产 handler 不改状态码或路由；真实 DELETE 回归放在 'ptyreclaim_test.go'，从
env 的 HTTP 入口进入 handler。'pty_ws_test.go' 现有 cleanup 会自然使用修复后的 Close，不
新增重复 trap。

精确新增/修改接口：

~~~go
type clientSession struct {
    meta     sessdir.Meta
    waitDone <-chan error
}

func (h *Host) Open(opt OpenOptions) (Session, error)
func (h *Host) Close(id string) error
func (h *Host) Adopt(entries []sessdir.Entry)
func (h *Host) waitPtyhostExit(entry *clientSession, deadline time.Time) error
func (h *Host) remember(meta sessdir.Meta, waitDone <-chan error)
~~~

Consumes：Open 已启动的唯一 'waitDone <-chan error'、Adopt 的 'sessdir.Entry.Meta.PID' 和
会话目录、hostproc CtrlKill 后的 socket/目录清理。

Produces：Open 登记时保留 'waitDone'；Adopt 登记时 'waitDone == nil' 并用
'sessdir.Dir(h.root, id)' 消失作为跨进程退出事实；Close 在 kill 帧成功写出后等待上述
事实，控制帧 EOF/timeout 不能单独判成功；成功才 forget，进程失败/等待超时也必须 forget
并返回带 session/pid/wait path 的错误。

### 3.2 Host 客户端实现

Task C 第一动作先运行：

~~~text
go test ./internal/ptyhost -run 'Test(ClientOpenList|ClientCloseRemovesSession|SurviveAgentdClientRestart)' -count=1
go test ./internal/agentd -run 'TestPtySessionListAndDelete|TestGracefulShutdownKeepsPtySession' -count=1
go test ./cmd -run 'TestService' -count=1
~~~

在 'client.go' 常量区增加：

~~~go
// closeWait 必须大于 engine.termGrace（2s），给 hostproc 的 Run defer 留出返回并删目录
// 的短余量；CloseAll 与 agentd shutdown 的外层 2s 预算保持不变，超时由外层记录。
const closeWait = 3 * time.Second
~~~

把 'clientSession'、'remember' 和两个调用点改成：

~~~go
type clientSession struct {
    meta     sessdir.Meta
    waitDone <-chan error
}

// remember 登记静态元数据与 Open 子进程的唯一 Wait 通道。
//
// waitDone 只对当前进程直接启动的 Open 有值；Adopt 的跨进程会话必须传 nil，
// 由 waitPtyhostExit 观察会话目录，而不是错误地对非 child 调 exec.Cmd.Wait。
func (h *Host) remember(meta sessdir.Meta, waitDone <-chan error) {
    credential, credentialOK := prochost.ProcessCredentialForPID(meta.PID)
    h.mu.Lock()
    h.sessions[meta.ID] = &clientSession{meta: meta, waitDone: waitDone}
    if credentialOK {
        h.credentials[meta.ID] = credential
    } else {
        delete(h.credentials, meta.ID)
    }
    h.mu.Unlock()
}
~~~

'Open' 成功路径把 'h.remember(meta)' 改成 'h.remember(meta, waitDone)'；Open 注释改为
“启动路径不阻塞等待 ptyhost；成功登记后显式 Close 保留并等待该 Open child 的 waitDone”。
失败路径继续把同一通道交给 'cleanupDetached'，不能新增第二个 Wait。

'Adopt' 登记 literal 必须显式为：

~~~go
h.sessions[entry.ID] = &clientSession{meta: entry.Meta, waitDone: nil}
~~~

再新增以下等待函数。Open 路径只消费现有 'waitDone'；Adopt 路径只轮询目录，不用 PID 猜测、
不调用 'cmd.Wait'；每个失败分支带 root/id/pid/path 上下文：

~~~go
// waitPtyhostExit 等待本次 Close 触发的 ptyhost 真正退出。
//
// Open 的 waitDone 是唯一 child Wait 结果；Adopt 没有 child Wait，只能把 hostproc
// defer 的 sessdir.Remove 作为完成事实。deadline 由 Host.Close 统一提供。
func (h *Host) waitPtyhostExit(entry *clientSession, deadline time.Time) error {
    waitPath := "session_dir"
    if entry.waitDone != nil {
        waitPath = "cmd_wait"
        remaining := time.Until(deadline)
        if remaining <= 0 {
            return fmt.Errorf("等待 ptyhost %s(pid=%d) 退出超时: wait_path=%s", entry.meta.ID, entry.meta.PID, waitPath)
        }
        timer := time.NewTimer(remaining)
        defer timer.Stop()
        select {
        case err := <-entry.waitDone:
            if err != nil {
                return fmt.Errorf("ptyhost %s(pid=%d) 退出失败: wait_path=%s: %w", entry.meta.ID, entry.meta.PID, waitPath, err)
            }
            return nil
        case <-timer.C:
            return fmt.Errorf("等待 ptyhost %s(pid=%d) 退出超时: wait_path=%s", entry.meta.ID, entry.meta.PID, waitPath)
        }
    }
    ticker := time.NewTicker(10 * time.Millisecond)
    defer ticker.Stop()
    for {
        _, err := os.Stat(sessdir.Dir(h.root, entry.meta.ID))
        if errors.Is(err, os.ErrNotExist) {
            return nil
        }
        if err != nil {
            return fmt.Errorf("检查 ptyhost %s(pid=%d) 会话目录 %s: wait_path=%s: %w",
                entry.meta.ID, entry.meta.PID, sessdir.Dir(h.root, entry.meta.ID), waitPath, err)
        }
        if time.Now().After(deadline) {
            return fmt.Errorf("等待 ptyhost %s(pid=%d) 会话目录消失超时: path=%s wait_path=%s",
                entry.meta.ID, entry.meta.PID, sessdir.Dir(h.root, entry.meta.ID), waitPath)
        }
        <-ticker.C
    }
}
~~~

替换 'Host.Close' 为以下完整函数。control EOF/timeout 只作为“没有收到 ack”的上下文，不能
跳过 wait；真实 hostproc 关闭连接后，等 wait/目录完成仍可返回 200。其它控制读错误在等待
完成后返回，并且所有“已发送 kill”的路径先 forget：

~~~go
// Close 显式发送 kill 并等待 ptyhost/PTY 收摊后摘除本地登记。
//
// 参数：id 是已登记会话 id。
// 返回：控制写失败、控制读非 EOF/timeout、ptyhost 退出失败或等待超时均返回错误。
// 注意：EOF/timeout 不是收摊事实；只有 cmd_wait 或 session_dir wait 成功才可返回 nil。
func (h *Host) Close(id string) error {
    entry, ok := h.session(id)
    if !ok {
        return ErrNoSession
    }
    waitPath := "session_dir"
    if entry.waitDone != nil {
        waitPath = "cmd_wait"
    }
    started := time.Now()
    conn, err := h.dial(id, statWait)
    if err != nil {
        h.log.Warn("关闭 PTY 会话：连接失败", "session", id, "pid", entry.meta.PID, "wait_path", waitPath, "cause", err)
        return fmt.Errorf("连接 PTY 会话 %s 关闭: %w", id, err)
    }
    if err := conn.SetDeadline(time.Now().Add(statWait)); err != nil {
        conn.Close()
        h.log.Warn("关闭 PTY 会话：设置 deadline 失败", "session", id, "pid", entry.meta.PID, "wait_path", waitPath, "cause", err)
        return fmt.Errorf("设置 PTY 会话 %s 关闭超时: %w", id, err)
    }
    if err := wire.WriteControl(conn, wire.Control{Type: wire.CtrlKill}); err != nil {
        conn.Close()
        h.log.Error("关闭 PTY 会话：发送 kill 失败", "session", id, "pid", entry.meta.PID, "wait_path", waitPath, "cause", err)
        return fmt.Errorf("发送 PTY 会话 %s kill: %w", id, err)
    }
    _, _, _, controlErr := wire.ReadFrame(conn)
    conn.Close()
    if controlErr != nil {
        h.log.Warn("关闭 PTY 会话：未收到 control ack，继续等待进程事实", "session", id,
            "pid", entry.meta.PID, "wait_path", waitPath, "cause", controlErr)
    }
    waitErr := h.waitPtyhostExit(entry, time.Now().Add(closeWait))
    h.forget(id)
    if waitErr != nil {
        h.log.Error("关闭 PTY 会话：等待收摊失败", "session", id, "pid", entry.meta.PID,
            "wait_path", waitPath, "elapsed", time.Since(started), "control_error", controlErr, "cause", waitErr)
        return fmt.Errorf("等待 PTY 会话 %s 收摊: %w", id, waitErr)
    }
    if controlErr != nil && !errors.Is(controlErr, io.EOF) && !isTimeout(controlErr) {
        h.log.Error("关闭 PTY 会话：control ack 失败", "session", id, "pid", entry.meta.PID,
            "wait_path", waitPath, "elapsed", time.Since(started), "cause", controlErr)
        return fmt.Errorf("等待 PTY 会话 %s 收摊控制帧: %w", id, controlErr)
    }
    h.log.Info("ptyhost 会话已按请求关闭", "session", id, "pid", entry.meta.PID,
        "wait_path", waitPath, "elapsed", time.Since(started), "control_error", controlErr)
    return nil
}
~~~

这里 'controlErr' 在成功收摊时可以是 EOF/timeout，但它永远不能单独使 Close 成功；fake socket
保持会话目录时必须走 wait timeout 并返回错误。'forget' 放在 wait 结果之后、所有已发送
kill 的错误返回之前，满足“进程失败仍 forget and error”。

### 3.3 Open、Adopt、CloseAll 与 shutdown 的真实锁缝

#### 3.3.1 Open 路径

在 'internal/ptyhost/client_test.go' 增加真实 Open 回归：使用已有 'buildHandoff'、'shortRoot'
和 'testLog'，调用 'h.Open'（不是直接 Engine），'HOME' 指向 't.TempDir()'，'InitCommand' 为：

~~~text
trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"
~~~

逐条断言：Open 成功且 PID>0；ready 文件在 3 秒内出现；'h.Close' 返回 nil；late 文件已经
存在；'sessdir.Dir(root, id)' 不存在；'h.List()' 为空。这样锁的是 Open 保存 'waitDone' 的
声明缝，不能由 Adopt 夹具代替。

测试代码如下；轮询是可观测的文件出现，不用固定等待：

~~~go
func waitClientFile(path string, within time.Duration) bool {
    deadline := time.Now().Add(within)
    ticker := time.NewTicker(10 * time.Millisecond)
    defer ticker.Stop()
    for {
        if _, err := os.Stat(path); err == nil {
            return true
        }
        if time.Now().After(deadline) {
            return false
        }
        <-ticker.C
    }
}

func TestClientOpenCloseWaitsForPtyhostAndShell(t *testing.T) {
    root := shortRoot(t)
    home := t.TempDir()
    h := ptyhost.New(root, buildHandoff(t), testLog())
    sess, err := h.Open(ptyhost.OpenOptions{
        BasePath: home, BaseKind: "home", Shell: "/bin/sh",
        Env: append(os.Environ(), "HOME="+home), Cols: 80, Rows: 24,
        InitCommand: `trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"`,
    })
    if err != nil {
        t.Fatalf("Open: %v", err)
    }
    if sess.PID <= 0 {
        t.Fatalf("Open PID=%d，期望真实 ptyhost 子进程", sess.PID)
    }
    if !waitClientFile(filepath.Join(home, "b234-ready"), 3*time.Second) {
        t.Fatal("Open InitCommand 未建立 ready marker")
    }
    if err := h.Close(sess.ID); err != nil {
        t.Fatalf("Close: %v", err)
    }
    if _, err := os.Stat(filepath.Join(home, "b234-late")); err != nil {
        t.Fatalf("Close 返回后 late marker 不存在: %v", err)
    }
    if _, err := os.Stat(sessdir.Dir(root, sess.ID)); !os.IsNotExist(err) {
        t.Fatalf("Close 后会话目录仍存在: %v", err)
    }
    if list := h.List(); len(list) != 0 {
        t.Fatalf("Close 后 Host.List=%+v", list)
    }
}
~~~

#### 3.3.2 Adopt 路径与 HTTP DELETE

在 Unix 测试的 'internal/agentd/ptyreclaim_test.go' 加入以下完整辅助函数。它复用当前
'newTestAgentdEnv' 的真实 pty root 与 logger，直接运行现有 'hostproc.Run'，再走
'reclaimPtySessions'→'Host.Adopt'；marker 轮询用 ticker：

~~~go
func startReclaimablePty(t *testing.T, s *Server, id string) (late string, done <-chan error) {
    t.Helper()
    root := s.ptyRoot()
    home := t.TempDir()
    late = filepath.Join(home, "b234-late")
    ready := filepath.Join(home, "b234-ready")
    if err := sessdir.Create(root, id); err != nil {
        t.Fatal(err)
    }
    spec := hostproc.Spec{
        Root: root, ID: id, BasePath: home, BaseKind: "home", Cwd: home,
        Shell: "/bin/sh", Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm-256color"},
        Cols: 80, Rows: 24,
        InitCommand: `trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"`,
    }
    body, err := json.Marshal(spec)
    if err != nil {
        t.Fatal(err)
    }
    specPath := filepath.Join(sessdir.Dir(root, id), "spec.json")
    if err := os.WriteFile(specPath, body, 0o600); err != nil {
        t.Fatal(err)
    }
    result := make(chan error, 1)
    go func() { result <- hostproc.Run(specPath) }()
    deadline := time.Now().Add(3 * time.Second)
    ticker := time.NewTicker(10 * time.Millisecond)
    defer ticker.Stop()
    for {
        if _, err := sessdir.ReadMeta(root, id); err == nil {
            break
        }
        if time.Now().After(deadline) {
            t.Fatalf("ptyhost meta.json 未就绪: root=%s id=%s", root, id)
        }
        <-ticker.C
    }
    readyDeadline := time.Now().Add(3 * time.Second)
    readyTicker := time.NewTicker(10 * time.Millisecond)
    defer readyTicker.Stop()
    for {
        if _, err := os.Stat(ready); err == nil {
            break
        }
        if time.Now().After(readyDeadline) {
            t.Fatalf("InitCommand ready marker 未出现: %s", ready)
        }
        <-readyTicker.C
    }
    entries, err := sessdir.Scan(root)
    if err != nil {
        t.Fatal(err)
    }
    if err := s.reclaimPtySessions(); err != nil {
        t.Fatal(err)
    }
    found := false
    for _, entry := range entries {
        if entry.ID == id && entry.State == sessdir.StateLive {
            found = true
            break
        }
    }
    if !found {
        t.Fatalf("Scan 未得到 live 会话: %+v", entries)
    }
    if _, ok := s.pty.Get(id); !ok {
        t.Fatalf("reclaimPtySessions 后 Host 未登记 %s", id)
    }
    return late, result
}
~~~

补齐 imports：'context'、'encoding/json'、'os'、'path/filepath'、'runtime'、'time'、
'internal/ptyhost/hostproc'。在同文件新增两个真实入口测试：

~~~go
func TestReclaimedPtyDeleteWaitsForShell(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("windows 上 PTY 不支持")
    }
    env := newTestAgentdEnv(t)
    late, done := startReclaimablePty(t, env.srv, "b234-delete")
    req, err := http.NewRequest(http.MethodDelete, env.ts.URL+"/api/pty/sessions/b234-delete", nil)
    if err != nil {
        t.Fatal(err)
    }
    req.Header.Set("Authorization", "Bearer "+testToken)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        t.Fatalf("DELETE: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("DELETE status=%d, want 200", resp.StatusCode)
    }
    if _, err := os.Stat(late); err != nil {
        t.Fatalf("DELETE 返回后 late marker 不存在: %v", err)
    }
    select {
    case err := <-done:
        if err != nil {
            t.Fatalf("hostproc.Run: %v", err)
        }
    case <-time.After(3 * time.Second):
        t.Fatal("reclaimed hostproc 未退出")
    }
    if list := env.srv.pty.List(); len(list) != 0 {
        t.Fatalf("DELETE 后 Host.List=%+v", list)
    }
}

func TestShutdownPtySessionsWaitsForReclaimedShell(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("windows 上 PTY 不支持")
    }
    env := newTestAgentdEnv(t)
    late, done := startReclaimablePty(t, env.srv, "b234-shutdown")
    env.srv.shutdownPtySessions(context.Background())
    if _, err := os.Stat(late); err != nil {
        t.Fatalf("shutdownPtySessions 返回后 late marker 不存在: %v", err)
    }
    select {
    case err := <-done:
        if err != nil {
            t.Fatalf("hostproc.Run: %v", err)
        }
    case <-time.After(3 * time.Second):
        t.Fatal("shutdownPtySessions 后 hostproc 未退出")
    }
    if list := env.srv.pty.List(); len(list) != 0 {
        t.Fatalf("shutdownPtySessions 后 Host.List=%+v", list)
    }
}
~~~

这两支测试入口分别是 HTTP DELETE 与 'shutdownPtySessions'，并且都从 Adopt 进入；DELETE 的
200 口径与既有重复 DELETE 的 404 测试同时保留。已有 'TestGracefulShutdownKeepsPtySession'
不改，继续证明 Trigger/升级路径不调用 shutdown。

#### 3.3.3 CloseAll 与 cmd service stop

在 'internal/ptyhost/client_test.go' 的已有 'startClientHost' 中抽出
'startClientHostWithSpec(t *testing.T, spec hostproc.Spec) (root string, h *ptyhost.Host,
id string, done chan error)'，保留原有 'startClientHost' 作为无 InitCommand 的薄包装；抽取
后的完整行为仍是：shortRoot→sessdir.Create→json.Marshal→写 spec.json→后台
'hostproc.Run'→等 socket+meta→Scan→'h.Adopt'→注册 CtrlKill cleanup。新增
'startClientHostWithExitMarker' 用 HOME、ready/late InitCommand 调此 harness。

复用已有 harness 的理由：它是 ptyhost 包测试中唯一含短 socket 根、真实 hostproc、Scan、
Adopt 和 cleanup 的夹具；重新写第二套会丢掉 socket 路径预算。新增/修改测试的逐条断言：

- 'TestClientCloseRemovesSession'：Close 返回后 late 存在、'h.List()' 为空、done 在 3 秒
  内收到 nil。
- 'TestSurviveAgentdClientRestart'：保留 BEFORE backlog、AFTER 继续写入；h2.Close 返回后
  late 存在、目录消失、Scan 为空。
- 'TestCloseAllKillsLiveSessions'：调用 'ptyhost.CloseAll(root, testLog(), 3*time.Second)'，
  断言 'closed==1'、late 存在、done nil、目录消失；保留缺失 root=0,nil 测试。

'CloseAll' 的 2s 外层 budget 不改为异步；它仍可到点只记 Warn 并返回，后台 Close 继续
自己的 wait。'closePtySessionsForStop' 也要有真实入口测试，新建 'cmd/service_pty_test.go'：

~~~go
//go:build unix

// service_pty_test.go —— service stop 真实经过 closePtySessionsForStop→CloseAll。
// 职责：锁住显式 stop 的 PTY 收口和 2s 外层预算不改变。
// 边界：不启动真实 service manager，只直接调用 cmd 的 stop cleanup 函数。
package cmd

import (
    "bytes"
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/Xsxdot/handoff/internal/config"
    "github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
    "github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

func TestClosePtySessionsForStopWaitsForLateTrap(t *testing.T) {
    dataDir := t.TempDir()
    root := filepath.Join(dataDir, "ptys")
    home := t.TempDir()
    id := "b234-stop"
    if err := os.MkdirAll(root, 0o700); err != nil {
        t.Fatal(err)
    }
    if err := sessdir.Create(root, id); err != nil {
        t.Fatal(err)
    }
    spec := hostproc.Spec{
        Root: root, ID: id, BasePath: home, BaseKind: "home", Cwd: home,
        Shell: "/bin/sh", Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm-256color"},
        Cols: 80, Rows: 24,
        InitCommand: `trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"`,
    }
    body, err := json.Marshal(spec)
    if err != nil {
        t.Fatal(err)
    }
    specPath := filepath.Join(sessdir.Dir(root, id), "spec.json")
    if err := os.WriteFile(specPath, body, 0o600); err != nil {
        t.Fatal(err)
    }
    done := make(chan error, 1)
    go func() { done <- hostproc.Run(specPath) }()
    deadline := time.Now().Add(3 * time.Second)
    ticker := time.NewTicker(10 * time.Millisecond)
    for {
        if _, err := os.Stat(filepath.Join(home, "b234-ready")); err == nil {
            break
        }
        if time.Now().After(deadline) {
            ticker.Stop()
            t.Fatal("service stop fixture 未就绪")
        }
        <-ticker.C
    }
    ticker.Stop()
    cfgPath := filepath.Join(dataDir, "config.yaml")
    if err := config.Save(cfgPath, &config.Config{DataDir: dataDir}); err != nil {
        t.Fatal(err)
    }
    oldConfigPath := configPath
    configPath = cfgPath
    t.Cleanup(func() { configPath = oldConfigPath })
    var out bytes.Buffer
    closePtySessionsForStop(&out)
    select {
    case err := <-done:
        if err != nil {
            t.Fatalf("hostproc.Run: %v", err)
        }
    case <-time.After(5 * time.Second):
        t.Fatal("closePtySessionsForStop 后 hostproc 未退出")
    }
    if _, err := os.Stat(filepath.Join(home, "b234-late")); err != nil {
        t.Fatalf("stop cleanup 返回/hostproc 退出后 late marker 不存在: %v", err)
    }
    if _, err := os.Stat(sessdir.Dir(root, id)); !os.IsNotExist(err) {
        t.Fatalf("stop cleanup 后会话目录仍存在: %v", err)
    }
}
~~~

'cmd/service.go' 只补注释：'ptyCloseBudget=2s' 是 stop 调用方总预算，CloseAll 到点返回不等于
把 Host.Close 改回异步；调用方仍只打印 Warn、不阻断 service manager。

### 3.4 控制读负面接缝与测试

在 'internal/ptyhost/client_test.go' 增加 'TestCloseDoesNotTreatControlEOFAsSuccess'。完整构造：
'sessdir.Create(root,id)'、写可读 Meta、在 session socket 监听；'Host.Adopt' 登记显式
'StateLive' entry；fake server 读掉 CtrlKill 后立即关闭连接且不删除会话目录；调用
'h.Close(id)'。断言必须逐条是：Close 返回非 nil；错误包含 session id 或 wait timeout；
'h.List()' 为空；cleanup 关闭 listener 并删除 fake session directory。该测试从真实
'Host.Close' 入口进入，不能改成直接调用 wait helper。

测试代码如下：

~~~go
func TestCloseDoesNotTreatControlEOFAsSuccess(t *testing.T) {
    root := shortRoot(t)
    id := "b234-eof"
    if err := sessdir.Create(root, id); err != nil {
        t.Fatal(err)
    }
    meta := sessdir.Meta{
        ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
        Shell: "/bin/sh", CreatedAt: time.Now(), PID: os.Getpid(), ProtoVersion: wire.ProtoVersion,
    }
    if err := sessdir.WriteMeta(root, meta); err != nil {
        t.Fatal(err)
    }
    ln, err := net.Listen("unix", sessdir.SockPath(root, id))
    if err != nil {
        t.Fatal(err)
    }
    serverDone := make(chan struct{})
    go func() {
        conn, acceptErr := ln.Accept()
        if acceptErr == nil {
            _, _, _, _ = wire.ReadFrame(conn)
            _ = conn.Close()
        }
        close(serverDone)
    }()
    t.Cleanup(func() {
        _ = ln.Close()
        _ = sessdir.Remove(root, id)
    })
    h := ptyhost.New(root, "", testLog())
    h.Adopt([]sessdir.Entry{{ID: id, Meta: meta, State: sessdir.StateLive}})
    err = h.Close(id)
    if err == nil {
        t.Fatal("control EOF 且会话目录仍在时 Close 不得返回成功")
    }
    if !strings.Contains(err.Error(), id) && !strings.Contains(err.Error(), "超时") {
        t.Fatalf("Close 错误缺少 session/wait 上下文: %v", err)
    }
    if list := h.List(); len(list) != 0 {
        t.Fatalf("失败 Close 后登记未清除: %+v", list)
    }
    select {
    case <-serverDone:
    case <-time.After(time.Second):
        t.Fatal("fake control server 未消费 CtrlKill")
    }
}
~~~

### 3.5 Task C 日志、注释与最小绿测

- Host.Close 每个入口错误、control ack 缺失、wait 失败、成功路径都带 session、pid、
  wait_path、elapsed、cause；wait helper 注释说明 Open/Adopt 两个事实来源。
- 'ptyreclaim.go' 注释写明每个 Close 会等自身收摊，但 2s 总预算到点仍只 Warn；
  'service.go' 同步注明 CloseAll 的 2s 预算。
- 不改 'GracefulShutdownCleanup'，不把 Trigger、SIGTERM、升级路径接到 PTY kill；保留
  'TestGracefulShutdownKeepsPtySession'。
- 只跑触及包：

~~~text
go test ./internal/ptyhost -run 'Test(ClientOpenCloseWaitsForPtyhostAndShell|ClientCloseRemovesSession|SurviveAgentdClientRestart|CloseAllKillsLiveSessions|CloseDoesNotTreatControlEOFAsSuccess)' -count=1
go test ./internal/agentd -run 'Test(ReclaimedPtyDeleteWaitsForShell|ShutdownPtySessionsWaitsForReclaimedShell|PtySessionListAndDelete|GracefulShutdownKeepsPtySession)' -count=1
go test ./cmd -run 'Test(ClosePtySessionsForStopWaitsForLateTrap|Service)' -count=1
go test ./internal/ptyhost ./internal/agentd ./cmd -count=1
~~~

Task C 绿判据：Open、Adopt、CloseAll、shutdown、service stop 五条生产调用链都在 shell EXIT
trap 写入 late marker 后才报告成功；Adopt 目录消失且 Host 登记清空；control EOF/timeout fake
返回错误；DELETE 仍 200，重复 DELETE 仍 404；Trigger 仍不杀 PTY。

## 4. 序列化边界、类型清单与行为不变量

### 4.1 序列化边界

本卡不新增 wire 数据字段。现有 'proto.CreatePtySessionReq.InitCommand' 已在
'internal/proto/pty.go:47-64'，链路为：HTTP JSON decoder → 'handleCreatePtySession' →
'ptyhost.OpenOptions.InitCommand' → 'launchSpec.InitCommand' JSON → 'hostproc.Spec' JSON →
'engine.Open'。实施时禁止改字段名、'omitempty'、HTTP 投影或 WebSocket 帧。

手写边界与断言：

1. Task A：'httptest.Server'/'*http.Transport' 不是 wire 投影；'ts.Client'、DefaultClient、
   client.New transport 三条手工装配都由真实拨号测试覆盖。
2. Task B：InitCommand 只作为现有 'OpenOptions' fixture 输入；ready/late 是文件系统观测，
   不是新增协议字段；hostproc 'Spec' JSON 形状不变。
3. Task C：'CreatePtySessionReq' 的 'init_command' 真实 decode→Open→launchSpec→hostproc
   回归；DELETE 响应 map 仍只含现有 'ok'，状态码保持 200/404。

可空/零值没有新增字段，因此无需 roundtrip 新属性测试；不得把 'InitCommand:""' 的既有
omitempty 改成显式空键。

### 4.2 边界类型真机清单

实施后报告真实结果，不得凭代码阅读写 pass：

- TCP：服务端 Accept 与 client DialContext 各得到 '*net.TCPConn' 并调用 SetLinger(0)；
  loopback 判断覆盖 127.0.0.1、localhost、::1；非 loopback 不重试。
- 错误：两种精确错误任一匹配才重试；最终错误仍可 'errors.Is' 且文本不被 helper 改写；
  上限为 'testhttp.MaxDialAttempts'。
- Transport：DefaultClient、'httptest.Server.Client()'、'internal/client.New' 返回的
  '*http.Transport' 都实际走 wrapper；生产 'client.New' 源码没有变化。
- PTY：Open 使用 waitDone；Adopt 使用 session directory；Engine 使用单一 reap；hostproc
  Run 在删目录前完成 Engine.Close；'CloseAll'/'shutdown' 外层 2s 不变。
- 控制：EOF/timeout 不是进程退出事实；控制读异常、process wait 非零、wait 超时都带
  session/pid/path 返回错误并 forget；成功 Close 返回前 late marker 已存在。

## 5. 缺陷族对抗审查

| 缺陷族 | 反问 | 本计划的锁点与结论 |
|---|---|---|
| 端口/网络时序 | 只改服务端是否仍让 DefaultClient、WS、client.New 耗尽 Darwin 临时端口？ | Task A 同时锁 Accept/Dial linger，三条入口各到 retry 上限；只改 server 不满足。 |
| 错误匹配/范围泄漏 | 是否把所有连接错误或生产 Client.Do 都重试？ | loopback、两种精确错误、四次上限和负例锁住；不改生产 client。 |
| 双 Wait/进程竞态 | Engine.Close 是否另起 cmd.Wait 或 SIGKILL 后立即返回？ | exitedDone 只由已有 reap 关闭，Close 只等它；源码审计与 FIFO trap 共同锁住。 |
| Open/Adopt 混淆 | 重启后 Adopt 会不会错误等待非 child，或 EOF 就当成功？ | Open 保存 waitDone；Adopt 显式 nil 后轮询目录；fake EOF 必须等事实并超时返回错误。 |
| 清理竞态 | hostproc 是否在 shell/PTY 仍活时删目录，late 产物是否丢？ | Engine→hostproc、Open/Adopt、DELETE、shutdown、CloseAll、service stop 都要求 late、Run/目录事实。 |
| 预算/并发 | 为了等 PTY 是否改大/取消外层 2s，或让 Close 异步？ | closeWait=3s 只在单会话 Host；外层两个 2s 保持，超时只 Warn。 |
| 兼容性 | 是否改变 DELETE、WS、PTY frame、Trigger/升级存活语义？ | 既有 200/404、WS、Attach/backlog、Trigger 不杀测试保留。 |
| 观测/错误 | 成功路径是否静默、错误是否脱离 session/pid/path？ | helper 保留最终 dial 错误；Engine/Host/hostproc 关键节点结构化 slog。 |
| 测试 oracle | 是否只查 RemoveAll 或依赖固定 sleep，在修复前假绿？ | FIFO trap 让旧 Close 在 trap 前返回即红；late/Run/目录/登记逐条断言；轮询用 ticker。 |

## 6. 接缝双向覆盖与用户故事归属

### 6.1 接缝清单

1. HTTP fixture helper → 21 个 agentd server 构造点 → Accept/Dial linger 与 retry；真实入口为
   'http.DefaultClient.Do'、'websocket.Dial'、'handoffclient.New(...).HTTPClient().Do'。
2. 'Host.Close' → DELETE handler、'shutdownPtySessions'、'CloseAll'；Open 与 Adopt 两种
   wait path 各一支；控制 EOF/timeout 负面分支。
3. 'Engine.Close' → hostproc.Run defer；direct Engine.Close 的 FIFO red/green 与 hostproc
   Run 的 late/目录断言。
4. HOME/EXIT trap → HTTP 'CreatePtySessionReq.InitCommand'、direct Open、Adopt、CloseAll、
   service stop；late 文件是跨进程消费者。

### 6.2 测试→缝与缝→测试

| 接缝 | 入口测试 | 断言 |
|---|---|---|
| helper Accept/Dial | 'TestNewServerSetsLingerOnAcceptAndDefaultClientDial' | 一次真实 HTTP 请求让两侧调用 SetLinger |
| helper retry | 'TestRetryDialContextIsBoundedAndPreservesError'、'TestRetryDialContextDoesNotRetryNonLoopback' | loopback 到上限、错误保留；非 loopback 一次 |
| DefaultClient | 'TestHTTPFixtureDialRoutesReachRetryLimit/http.DefaultClient' | 真实 DefaultClient.Get 到上限 |
| websocket | 对应 websocket 子测试 | nil options 确实使用 DefaultClient，到上限 |
| client.New | 对应 client.New 子测试 | custom transport 到上限，未改生产代码 |
| 21 构造迁移 | 'TestNoDirectHttptestServers' + agentd 全包 | 源码无直接 NewServer/NewUnstartedServer；已有 fixture 行为不变 |
| Engine.Close | 'TestCloseWaitsForReapAndLateTrap' | FIFO 未释放时旧实现提前返回即红；释放后 late/reap/列表齐全 |
| hostproc.Run | 升级后的 'TestKillEndsProcessAndCleansDir' | Run nil、late、目录不存在 |
| Host Open | 'TestClientOpenCloseWaitsForPtyhostAndShell' | cmd_wait 路径；Close 后 late/目录/List |
| Host Adopt+DELETE | 'TestReclaimedPtyDeleteWaitsForShell' | reclaim→Adopt→HTTP DELETE；200、late、Run、登记 |
| shutdown | 'TestShutdownPtySessionsWaitsForReclaimedShell' | 真实 shutdown、late、Run、登记；Trigger 存量负例保留 |
| CloseAll | 升级后的 'TestCloseAllKillsLiveSessions' | Scan→Adopt→Close；closed=1、late、目录 |
| service stop | 'TestClosePtySessionsForStopWaitsForLateTrap' | closePtySessionsForStop→CloseAll，预算与真实收口 |
| control negative | 'TestCloseDoesNotTreatControlEOFAsSuccess' | fake EOF 且目录保留时 Close 超时错误、登记清除 |

每支测试的入口均在声明缝或调用链穿过声明缝；FIFO、静态扫描、waitFile 是附加内部锁，
不能顶替真实入口。静态扫描与 harness 的内部锁合法理由已在 1.4、2.2、3.3.3、3.4 写明。

### 6.3 用户故事归属

1. agentd macOS 测试不再因 loopback 临时端口偶发失败：Task A 1.3–1.6。
2. DefaultClient、WS、client.New 三路都受限重试且不改生产默认：Task A 1.5。
3. 显式 DELETE 后 shell 已退出、late 产物存在、状态码兼容：Task C 3.3.2。
4. agentd 重启后 Adopt 会话同样等 ptyhost/目录收口：Task C 3.3.2、3.3.3。
5. Engine 不重复 Wait，hostproc 不提前删目录：Task B 2.2–2.4。
6. shutdown 与 CloseAll 同步观察收口，但各自外层预算仍能超时退出：Task C 3.3.2–3.3.3。
7. Trigger/升级不误杀 PTY：Task C 保留 'TestGracefulShutdownKeepsPtySession'。

## 7. 上下文预算与跨任务签名审计

文件集有界：Task A 17 个文件（2 个 helper、2 个新测试、13 个迁移文件）；Task B 5 个
文件；Task C 8 个列出的已有/新增文件。Task A 与 B 无共享生产文件；Task C 只消费 B 的
行为，不复制 Engine wait 实现。

计划内新增签名逐字符对齐：

~~~text
Task B Produces: func (h *Engine) Close(id string) error
Task C Consumes: hostproc.Run defer calls eng.Close(engineID), no signature change

Task C Produces: func (h *Host) waitPtyhostExit(entry *clientSession, deadline time.Time) error
Task C Consumes: func (h *Host) Close(id string) error

Task A Produces: func RetryDialContext(base DialContext) DialContext
Task A Consumes: http.Transport.DialContext = RetryDialContext(DialContext(baseContext))
~~~

没有跨卡 A/B 计划；本卡为 L2，不触发 L3 独立跨卡审计。实施者不可把 'MaxDialAttempts'
改成另一个名称而只更新一侧，也不可把 'waitDone' 从 '<-chan error' 改成 'chan error'
而让邻接代码靠猜。

## 8. 占位符扫描、协调者门禁与收口

本计划不含未定项、模糊“适当错误处理”、任务编号替代步骤或未定义符号。允许的 harness 复用
例外仅两处：

1. 'openAndCollect'（'internal/ptyhost/engine/initcmd_test.go'）用于 Engine FIFO seam，
   因为它是现有真实 Engine→Attach 流；断言为 ready、旧实现不得提前返回、释放 FIFO、
   late、Get 消失。
2. 'startClientHost'（'internal/ptyhost/client_test.go'）用于 Host Adopt/CloseAll，
   因为它含短 socket 根、真实 hostproc、Scan、Adopt 和 cleanup；断言逐条列于 3.3.3。

实施阶段每个 task 遵循：基线命令→锁缝测试写下并跑红（若实际先绿则记录原文）→最小实现→
同一测试绿→包级测试绿→台账追加命令与原始输出。新增文件头、导出函数参数/返回/边界注释、
非显然逻辑的“为什么”注释与结构化日志必须随实现提交。

所有 task 完成后，下列步骤由协调者执行，不派发：

~~~text
git diff --check
go test ./internal/testhttp -count=1
go test ./internal/ptyhost ./internal/ptyhost/engine ./internal/ptyhost/hostproc ./internal/agentd ./cmd -count=1
go test ./...
go test ./internal/agentd -count=1   # 合 main 前在 macOS 本机执行并把无 EADDRNOTAVAIL 原文写台账
~~~

协调者只在亲自得到退出 0 原始输出、macOS 门真实复跑、台账含红/绿或实际先绿记录、接缝
双向清单满足后，复核本卡未改协议/预算/生产 client，执行 'git add' 与 'git commit'
（不 push）。本计划节点本身不把未执行的实现结果写成 pass。
