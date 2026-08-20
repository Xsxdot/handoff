# agentd 侧 target client 池 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 agentd 的六处扇出认识 relay 形态的开发机，控制台不再把中继机器一律显示成「已断开」。

**Architecture:** 新建 `internal/targetclient` 包收拢「按 Target 形态选路」这唯一判据：`New` 是一次性工厂（CLI 用），`Pool` 是常驻复用池（agentd 用，一台机器一条 relay 隧道，全子系统共用）。六个调用点全部改走池，`cmd/root.go` 重构到同一个工厂。

**Tech Stack:** Go 1.x、`internal/relay`（yamux + E2E 隧道）、`internal/client`（agentd HTTP/WS 客户端）、React + TypeScript（控制台）。

设计依据：[docs/superpowers/specs/2026-08-20-agentd-target-client-pool-design.md](../specs/2026-08-20-agentd-target-client-pool-design.md)

## Global Constraints

- 日志一律用 `slog`（`s.log` / `m.log` / 包内 `slog.Default()`），**禁止 `fmt.Printf`**。
- 令牌、credential **绝不进日志**——只记 target 名、node 名、relay URL。
- 中文注释写「为什么」，新文件必须有文件头注释（职责 + 边界），导出函数必须有文档注释。
- `client.New` 的签名不改（二十多个调用点）。
- 每个 task 结束时 `gofmt -l .` 必须为空（既有教训：executor 的 ledger 会漏 gofmt）。
- 提交信息用中文，结尾带 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/targetclient/targetclient.go`（新建） | 选路工厂 `New` + `ErrNoEndpoint`；唯一知道「relay 还是直连」的地方 |
| `internal/targetclient/pool.go`（新建） | `Pool`：缓存、失效、`Names`、`Close` |
| `internal/targetclient/warm.go`（新建） | `Pool.Warm` 预热循环与逐台退避 |
| `internal/client/client.go`（改） | `initErr` 毒化：地址无 host 的 client 每个请求都明确报错 |
| `internal/relay/dialer.go`（改） | 导出 `Ensure(ctx)`，供预热主动建隧道 |
| `internal/proto/projects.go`（改） | `Machine.Relay` 字段 |
| `internal/agentd/{machines,mirror,projectfanout,pty_api,machineupgrade}.go`（改） | 六个调用点改走池 |
| `internal/agentd/server.go`（改） | Server 持池 + `Pool()` / `CloseTargets()` |
| `internal/agentd/nodirectclient_test.go`（新建） | 守卫：agentd 包内不许再出现 `client.New(` |
| `cmd/root.go`、`cmd/agentd.go`（改） | CLI 重构到工厂；agentd 接线、起预热、退出关池 |
| `web/src/app/machines/machineEndpoint.ts`（新建） | 展示端点文案：addr 优先，否则「中继 · <node>」 |

---

### Task 1: `client` 地址无 host 时毒化

**Files:**
- Modify: `internal/client/client.go`（`Client` 结构体、`NewWithWSTiming`、`do`、`doStream`、`streamOnce`）
- Modify: `internal/client/update.go:91`（`postUpdate`）
- Test: `internal/client/poisoned_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `Client.initErr` 内部字段与 `(*Client).checkInit() error` 内部方法。外部行为：`client.New("")` 之后任意请求返回含「地址不含主机名」的错误，**不再**返回 `no Host in request URL`。

- [ ] **Step 1: 写失败的测试**

创建 `internal/client/poisoned_test.go`：

```go
// 本文件锁死「地址缺 host 的 client 必须当场自曝」这一条。
//
// 为什么值得一个专门的测试文件：空地址曾静默退化成 baseURL="http:"，请求 URL
// 变成 http:/api/status，错误文案是 "http: no Host in request URL"——一个配置
// 缺失被伪装成网络故障，排查要从错误文案一路倒推到字符串裁剪。
package client

import (
	"context"
	"strings"
	"testing"
)

// TestEmptyAddrPoisonsClient：空地址 → 请求明确报「地址不含主机名」。
func TestEmptyAddrPoisonsClient(t *testing.T) {
	_, err := New("", "tok").Status(context.Background())
	if err == nil {
		t.Fatal("空地址必须报错")
	}
	if !strings.Contains(err.Error(), "地址不含主机名") {
		t.Fatalf("错误文案要指向根因，实得: %v", err)
	}
	if strings.Contains(err.Error(), "no Host in request URL") {
		t.Fatalf("不许再把配置缺失伪装成网络错误: %v", err)
	}
}

// TestNormalAddrNotPoisoned：正常地址不受影响（毒化不能误伤）。
func TestNormalAddrNotPoisoned(t *testing.T) {
	if err := New("127.0.0.1:7777", "tok").checkInit(); err != nil {
		t.Fatalf("正常地址不该被毒化: %v", err)
	}
	if err := New("http://127.0.0.1:7777/", "tok").checkInit(); err != nil {
		t.Fatalf("带 scheme 与尾斜杠的地址不该被毒化: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client/ -run 'TestEmptyAddrPoisonsClient|TestNormalAddrNotPoisoned' -v`
Expected: 编译失败，`c.checkInit undefined`

- [ ] **Step 3: 加 `initErr` 字段与 `checkInit`**

在 `Client` 结构体末尾（`cursorRootErr` 之后）加：

```go
	// initErr 非空表示这个 client 从构造起就不可用（地址不含主机名）。
	//
	// 为什么毒化而不是让 New 返回 error：New 有二十多个调用点，加返回值的波及
	// 远大于收益。而不管它的代价是实打实的——空地址会被归一化成
	// baseURL="http:"，请求 URL 退化成 http:/api/status，报出来的是
	// "no Host in request URL"，把「这台机器是 relay 形态、压根没有 addr」这个
	// 配置事实伪装成了网络故障。
	initErr error
```

在 `New` 系列函数下方加：

```go
// checkInit 在发请求前查毒化标记。
//
// 返回：initErr 原样返回；未毒化时 nil。
func (c *Client) checkInit() error { return c.initErr }
```

- [ ] **Step 4: 在 `NewWithWSTiming` 里种下毒化**

把 `NewWithWSTiming` 的构造段改成：

```go
func NewWithWSTiming(addr, token string, initial, max, stableAfter time.Duration) *Client {
	raw := addr
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	base := strings.TrimRight(addr, "/")
	c := &Client{
		baseURL: base,
		token:   token,
		hc: &http.Client{
			// ... 原有 Transport 构造原样保留，不动 ...
		},
		wsInitialBackoff: initial,
		wsMaxBackoff:     max,
		wsStableAfter:    stableAfter,
	}
	// 地址缺 host 时当场毒化：raw 为空会一路变成 base="http:"（TrimRight 把两个
	// 斜杠一起削掉），后续每个请求都报 no Host——那个文案指不出真正的原因。
	if u, err := url.Parse(base); err != nil || u.Host == "" {
		c.initErr = fmt.Errorf("agentd 地址不含主机名（原始地址 %q）：relay 形态的机器没有 addr，应经 targetclient 选路而不是直连构造", raw)
		slog.Default().Error("client 构造时地址不含主机名，该实例已毒化", "raw_addr", raw, "base_url", base)
	}
	return c
}
```

`import` 补 `net/url`（`fmt`、`strings`、`log/slog` 已在）。

- [ ] **Step 5: 四个请求入口都先查毒化**

在下面四个函数体的**第一行**插入同一段：

```go
	if err := c.checkInit(); err != nil {
		return nil, err
	}
```

- `internal/client/client.go` 的 `do`
- `internal/client/client.go` 的 `doStream`
- `internal/client/update.go` 的 `postUpdate`

`streamOnce` 的返回值只有 `error`，那里插：

```go
	if err := c.checkInit(); err != nil {
		return err
	}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/client/ -run 'TestEmptyAddrPoisonsClient|TestNormalAddrNotPoisoned' -v`
Expected: PASS

- [ ] **Step 7: 跑全包回归**

Run: `go test ./internal/client/`
Expected: PASS（毒化只对无 host 地址生效，既有用例全用真实 httptest 地址）

- [ ] **Step 8: 加关键节点日志**

Step 4 里已加毒化时的 Error 日志（带 raw_addr 与 base_url，**不带 token**）。确认：
- 毒化发生点有 Error 日志且带上下文
- 未毒化的正常路径**不加**日志（构造函数是热路径，正常构造打日志会刷屏）

- [ ] **Step 9: 加注释**

确认 `initErr` 字段注释写清了「为什么毒化而不是改签名」，`checkInit` 有文档注释，`poisoned_test.go` 有文件头注释。

- [ ] **Step 10: gofmt 与提交**

```bash
gofmt -l internal/client/ && go test ./internal/client/
git add internal/client/
git commit -m "$(cat <<'EOF'
fix(client): 地址不含主机名时当场毒化而非静默退化

空地址会被归一化成 baseURL="http:"，请求 URL 退化成 http:/api/status，
报 "no Host in request URL"——把配置缺失伪装成网络故障。改为构造时检出
并毒化，每个请求入口先查，错误文案直接指向 relay target 没有 addr。

不改 New 的签名：二十多个调用点，加返回值的波及远大于收益。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `relay.Dialer.Ensure` 导出方法

**Files:**
- Modify: `internal/relay/dialer.go`
- Test: `internal/relay/dialer_test.go`（追加）

**Interfaces:**
- Consumes: 无
- Produces: `func (d *Dialer) Ensure(ctx context.Context) error` —— 主动建立（或复用）隧道，成功返回 nil。Task 5 的预热循环调它。

- [ ] **Step 1: 写失败的测试**

追加到 `internal/relay/dialer_test.go`：

```go
// TestEnsureOnClosedDialerFails：已关闭的 Dialer 必须拒绝建隧道。
//
// why：预热循环会对每台机器反复调 Ensure，池 Close 之后它可能还在跑最后一轮。
// 这一条锁死「关了就是关了」，不会因为预热而复活一条隧道。
func TestEnsureOnClosedDialerFails(t *testing.T) {
	d := NewDialer("wss://example.invalid/relay", "cred", "node", "token", "", slog.Default())
	_ = d.Close()
	if err := d.Ensure(context.Background()); err == nil {
		t.Fatal("已关闭的 Dialer 不该还能建隧道")
	}
}
```

（若该文件尚未 import `context` / `log/slog`，补上。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/relay/ -run TestEnsureOnClosedDialerFails -v`
Expected: 编译失败，`d.Ensure undefined`

- [ ] **Step 3: 加 `Ensure`**

在 `Transport()` 上方插入：

```go
// Ensure 主动建立（或复用）relay 隧道，不发任何业务请求。
//
// 参数：
//   - ctx: 控制本次建隧道的时限；超时/取消原样返回
//
// 返回：
//   - nil 表示隧道已就绪（本次新建或复用现有）；Dialer 已 Close 时恒返回错误
//
// 为什么需要它：协调者侧的预热要把「隧道通没通」与「对端 agentd 活没活」分成
// 两个判据。借一次业务请求（如 GET /api/status）代劳会把两者搅在一起——隧道
// 建好但对端没起时，那次请求失败，预热无从判断该不该重试。
func (d *Dialer) Ensure(ctx context.Context) error { return d.ensureTunnel(ctx) }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/relay/ -run TestEnsureOnClosedDialerFails -v`
Expected: PASS

- [ ] **Step 5: 跑全包回归**

Run: `go test ./internal/relay/`
Expected: PASS

- [ ] **Step 6: 加关键节点日志**

不加新日志：`ensureTunnel` 内部已有完整的拨号/CONNECT/拒绝日志链（`relay ws dialing`、`relay connect sent`、`relay connect rejected`），`Ensure` 只是薄壳，再加一层是重复噪音。**这条判断要写进 Ensure 的注释**，否则下一个人会以为漏了。

- [ ] **Step 7: 加注释**

Step 3 的文档注释已覆盖参数、返回、存在理由。补一行说明「不另加日志，内部 ensureTunnel 已有完整日志链」。

- [ ] **Step 8: gofmt 与提交**

```bash
gofmt -l internal/relay/ && go test ./internal/relay/
git add internal/relay/
git commit -m "$(cat <<'EOF'
feat(relay): Dialer 导出 Ensure，供协调者预热隧道

预热要把「隧道通没通」与「对端活没活」分成两个判据，不能借业务请求代劳。
Ensure 是现成 ensureTunnel 的薄壳，不另加日志（内部已有完整拨号日志链）。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `targetclient.New` 选路工厂

**Files:**
- Create: `internal/targetclient/targetclient.go`
- Test: `internal/targetclient/targetclient_test.go`

**Interfaces:**
- Consumes: `config.Target`（`Addr`/`Token`/`Relay`/`Credential`/`Node`）、`client.New`、`client.NewRelay`、`relay.NewDialer`、`relay.CheckTokenEntropy`
- Produces:
  - `var ErrNoEndpoint = errors.New("target 既没有 addr 也没有 relay")`
  - `func New(name string, t config.Target, log *slog.Logger) (*client.Client, func(), error)` —— 第二个返回值是清理函数，恒非 nil（直连形态为 no-op）

- [ ] **Step 1: 写失败的测试**

创建 `internal/targetclient/targetclient_test.go`：

```go
package targetclient

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestNewDirect：直连 target → 直连 client，清理函数是 no-op。
func TestNewDirect(t *testing.T) {
	c, cleanup, err := New("mac-02", config.Target{Addr: "10.0.0.2:7777", Token: "tok"}, slog.Default())
	if err != nil {
		t.Fatalf("直连 target 不该失败: %v", err)
	}
	defer cleanup()
	if got := c.BaseURL(); got != "http://10.0.0.2:7777" {
		t.Fatalf("baseURL = %q，要 http://10.0.0.2:7777", got)
	}
}

// TestNewRelay：relay target → relay-backed client（baseURL 恒为 loopback 占位）。
//
// why 断言 baseURL 而不是断言「能连上」：连得上要真 relay 服务端，而这里要锁的是
// **选路走对了没有**——relay 分支的 baseURL 是 http://localhost（经隧道直达对端
// 的 hostGuard，loopback 名恒在白名单内），直连分支不可能产出这个值。
func TestNewRelay(t *testing.T) {
	tgt := config.Target{
		Relay:      "wss://relay.example.com/relay",
		Credential: "cred",
		Node:       "linux-01",
		Token:      "0123456789abcdef0123456789abcdef",
	}
	c, cleanup, err := New("linux-01", tgt, slog.Default())
	if err != nil {
		t.Fatalf("relay target 不该失败: %v", err)
	}
	defer cleanup()
	if got := c.BaseURL(); got != "http://localhost" {
		t.Fatalf("baseURL = %q，relay 形态要 http://localhost", got)
	}
}

// TestNewNoEndpoint：既无 addr 又无 relay → ErrNoEndpoint，且错误里点名是哪台。
//
// why 要点名：这个错误会原样显示在控制台的机器卡片上，不点名等于让人去猜。
func TestNewNoEndpoint(t *testing.T) {
	_, _, err := New("broken", config.Target{Token: "tok"}, slog.Default())
	if !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("要 ErrNoEndpoint，实得 %v", err)
	}
	if !contains(err.Error(), "broken") {
		t.Fatalf("错误要点名 target，实得 %v", err)
	}
}

// TestNewRelayLowEntropyToken：relay 形态的弱 token 必须被前置拒绝。
//
// why：token 在 relay 形态下额外充当 E2E 的 PSK 源，弱 token 等于隧道没有端到端
// 保护。这道闸在 CLI 侧本来就有，收进工厂后不能丢。
func TestNewRelayLowEntropyToken(t *testing.T) {
	tgt := config.Target{
		Relay: "wss://relay.example.com/relay", Credential: "cred",
		Node: "linux-01", Token: "123",
	}
	if _, _, err := New("linux-01", tgt, slog.Default()); err == nil {
		t.Fatal("弱 token 必须被拒")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/targetclient/ -v`
Expected: 包不存在，编译失败

- [ ] **Step 3: 给 `client.Client` 加 `BaseURL()` 读取器**

测试要断言选路结果，而 `baseURL` 是私有字段。在 `internal/client/client.go` 的 `checkInit` 旁加：

```go
// BaseURL 返回这个 client 的基址。
//
// 用途：调用方判定「选路选对了没有」——relay 形态恒为 http://localhost（经隧道
// 直达对端），直连形态是 http://<addr>。只读，不暴露 token。
func (c *Client) BaseURL() string { return c.baseURL }
```

- [ ] **Step 4: 写工厂实现**

创建 `internal/targetclient/targetclient.go`：

```go
// 本包收拢「按 target 形态选路构造 agentd 客户端」这唯一判据。
//
// 职责：
//   - New：一次性工厂，按 Target 是 relay 还是直连造出对应 client（CLI 用）
//   - Pool（见 pool.go）：常驻复用池，一台机器一条 relay 隧道（agentd 用）
//
// 边界：
//   - 不做任何网络请求：New 只构造，隧道由 Dialer 惰性建立或由 Warm 预热
//   - 不碰 client 的上层语义：MarkForwarded / NoRedirect 等一律由调用方链式调用
//   - 不读配置文件：调用方给什么 Target 就按什么造
//
// 为什么要有这个包：选路判据曾经存在两份——CLI 有 relay 分支，agentd 侧六处扇出
// 一处都没有，于是 relay 机器在控制台一律显示「已断开」。判据只留一份，才不会
// 有第二份从来没被写出来。
package targetclient

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/relay"
)

// ErrNoEndpoint 表示这个 target 既没有 addr 也没有 relay，无从构造客户端。
//
// config.Target.Validate 早就写着「direct target addr 不能为空」，这里是同一条
// 不变式在扇出侧的落点——扇出侧过去从没问过它。
var ErrNoEndpoint = errors.New("target 既没有 addr 也没有 relay")

// New 按 Target 形态选路，构造一个一次性的 agentd 客户端。
//
// 参数：
//   - name: target 名，只用于日志与错误文案（会原样显示给用户）
//   - t: target 配置；t.IsRelay() 为真走 relay 隧道，否则直连 t.Addr
//   - log: 日志器；nil 时用 slog.Default()
//
// 返回：
//   - client: 可直接链式 MarkForwarded()/NoRedirect()
//   - cleanup: **恒非 nil**，调用方 defer 它即可（直连形态是 no-op）
//   - err: ErrNoEndpoint（无端点）或 relay token 熵不足
//
// 注意：
//   - 不发任何网络请求；relay 隧道由 Dialer 首次用到时惰性建立
//   - 常驻场景不要用它——每次调用都会新建一条 relay 隧道，用 Pool
func New(name string, t config.Target, log *slog.Logger) (*client.Client, func(), error) {
	if log == nil {
		log = slog.Default()
	}
	noop := func() {}
	if t.IsRelay() {
		// relay 形态下 token 额外充当 E2E 的 PSK 源，弱 token = 隧道没有端到端
		// 保护。这道闸必须在建隧道之前。
		if err := relay.CheckTokenEntropy(t.Token); err != nil {
			log.Error("relay target 的 token 熵不足，拒绝构造", "target", name, "node", t.Node)
			return nil, noop, fmt.Errorf("target %s: %w", name, err)
		}
		d := relay.NewDialer(t.Relay, t.Credential, t.Node, t.Token, "", log)
		log.Info("target 走 relay 传输", "target", name, "node", t.Node, "relay_url", t.Relay)
		return client.NewRelay(d, t.Token), func() { _ = d.Close() }, nil
	}
	if t.Addr == "" {
		log.Error("target 无端点，既没有 addr 也没有 relay", "target", name)
		return nil, noop, fmt.Errorf("target %s: %w", name, ErrNoEndpoint)
	}
	log.Debug("target 走直连传输", "target", name, "addr", t.Addr)
	return client.New("http://"+t.Addr, t.Token), noop, nil
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/targetclient/ -v`
Expected: 全部 PASS

- [ ] **Step 6: 加关键节点日志**

核对 Step 4 的实现，四条都在：
- relay 选路成功：Info，带 target/node/relay_url（**不带 token/credential**）
- 直连选路成功：Debug（扇出热路径，Info 会刷屏）
- token 熵不足：Error，带 target/node
- 无端点：Error，带 target

- [ ] **Step 7: 加注释**

确认文件头注释写了职责 + 边界 + 「为什么要有这个包」，`New` 与 `ErrNoEndpoint` 都有文档注释。

- [ ] **Step 8: gofmt 与提交**

```bash
gofmt -l internal/targetclient/ internal/client/ && go test ./internal/targetclient/ ./internal/client/
git add internal/targetclient/ internal/client/
git commit -m "$(cat <<'EOF'
feat(targetclient): 新增按 target 形态选路的一次性工厂

选路判据过去存在两份：CLI 有 relay 分支，agentd 侧六处扇出一处都没有。
收进独立包，判据只留一份。附 client.BaseURL() 只读器供选路断言。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `targetclient.Pool` 缓存与失效

**Files:**
- Create: `internal/targetclient/pool.go`
- Test: `internal/targetclient/pool_test.go`

**Interfaces:**
- Consumes: Task 3 的 `New`、`ErrNoEndpoint`
- Produces:
  - `func NewPool(conf func() *config.Config, log *slog.Logger) *Pool`
  - `func (p *Pool) For(name string) (*client.Client, error)` —— 调用方**不**负责关闭
  - `func (p *Pool) Names() []string` —— 已排序
  - `func (p *Pool) Close() error`

- [ ] **Step 1: 写失败的测试**

创建 `internal/targetclient/pool_test.go`：

```go
package targetclient

import (
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func confOf(targets map[string]config.Target) func() *config.Config {
	c := &config.Config{Targets: targets}
	return func() *config.Config { return c }
}

// TestPoolReusesClient：同名两次 For 拿到同一个实例。
//
// why 这条最要紧：不复用等于每轮探活都新建一条 relay 隧道（WSS + CONNECT + E2E
// 握手），30s 一轮的循环会把对端和 relay 一起打爆。
func TestPoolReusesClient(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{
		"mac-02": {Addr: "10.0.0.2:7777", Token: "tok"},
	}), slog.Default())
	defer p.Close()

	a, err := p.For("mac-02")
	if err != nil {
		t.Fatalf("For 失败: %v", err)
	}
	b, err := p.For("mac-02")
	if err != nil {
		t.Fatalf("第二次 For 失败: %v", err)
	}
	if a != b {
		t.Fatal("同名两次 For 必须复用同一实例")
	}
}

// TestPoolRebuildsOnTargetChange：target 配置变了就重建。
//
// why 用整体比较而不是逐字段比：逐字段比会在 relay 加字段时漏掉新字段，
// 而漏掉的表现是「改了配置不生效」——最难查的那一类。
func TestPoolRebuildsOnTargetChange(t *testing.T) {
	targets := map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "old"}}
	p := NewPool(confOf(targets), slog.Default())
	defer p.Close()

	a, _ := p.For("mac-02")
	targets["mac-02"] = config.Target{Addr: "10.0.0.2:7777", Token: "new"}
	b, err := p.For("mac-02")
	if err != nil {
		t.Fatalf("改配置后 For 失败: %v", err)
	}
	if a == b {
		t.Fatal("target 配置变更后必须重建 client")
	}
}

// TestPoolUnknownName：配置里没有的名字 → 报错，不造 client。
func TestPoolUnknownName(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{}), slog.Default())
	defer p.Close()
	if _, err := p.For("ghost"); err == nil {
		t.Fatal("未登记的机器不该造出 client")
	}
}

// TestPoolNoEndpointPropagates：无端点的 target 把 ErrNoEndpoint 透出去。
func TestPoolNoEndpointPropagates(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{"broken": {Token: "tok"}}), slog.Default())
	defer p.Close()
	if _, err := p.For("broken"); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("要 ErrNoEndpoint，实得 %v", err)
	}
}

// TestPoolNamesFollowsLiveConfig：Names 跟随活快照，新增的机器立刻可见。
//
// why：Mirror 过去拿的是启动时的静态 cfg，控制台运行期加的机器要重启才被镜像。
// 这条锁死「加了就看得见」。
func TestPoolNamesFollowsLiveConfig(t *testing.T) {
	targets := map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "t"}}
	p := NewPool(confOf(targets), slog.Default())
	defer p.Close()

	if got := p.Names(); !reflect.DeepEqual(got, []string{"mac-02"}) {
		t.Fatalf("Names = %v", got)
	}
	targets["linux-01"] = config.Target{Addr: "10.0.0.3:7777", Token: "t"}
	if got := p.Names(); !reflect.DeepEqual(got, []string{"linux-01", "mac-02"}) {
		t.Fatalf("Names 要跟随活快照且排序，实得 %v", got)
	}
}

// TestPoolDropsRemovedTarget：target 被删 → 从池里移出。
func TestPoolDropsRemovedTarget(t *testing.T) {
	targets := map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "t"}}
	p := NewPool(confOf(targets), slog.Default())
	defer p.Close()

	if _, err := p.For("mac-02"); err != nil {
		t.Fatalf("For 失败: %v", err)
	}
	delete(targets, "mac-02")
	if _, err := p.For("mac-02"); err == nil {
		t.Fatal("已删除的机器不该还能拿到 client")
	}
	if n := p.size(); n != 0 {
		t.Fatalf("已删除的机器要从池里移出，实得 %d 条", n)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/targetclient/ -run TestPool -v`
Expected: 编译失败，`NewPool undefined`

- [ ] **Step 3: 写 Pool 实现**

创建 `internal/targetclient/pool.go`：

```go
// 本文件实现 agentd 常驻侧的 target 客户端复用池。
//
// 职责：
//   - 按 target 名缓存客户端与其 relay 隧道，一台机器一条隧道、全子系统共用
//   - 配置变更时失效重建，target 删除时关掉并移出
//   - Names 提供「当前有哪些机器」的唯一判据（活快照）
//
// 边界：
//   - 不探活：拿到 client 之后怎么用、算不算可达，由调用方决定
//   - 不预热：预热在 warm.go，两者刻意分开（隧道通没通 ≠ 对端活没活）
//   - 调用方**不**负责关闭 For 返回的 client：隧道归池，进程退出时统一 Close
package targetclient

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
)

// entry 是一台机器的缓存条目。
//
// 存 target 原值是为了做失效判定：config.Target 全是 string 字段（可比较），
// 整体 != 比较能覆盖每一个字段——逐字段比会在 relay 将来加字段时漏掉新字段，
// 而漏掉的表现是「改了配置不生效」，属于最难查的一类。
type entry struct {
	target  config.Target
	client  *client.Client
	cleanup func()
}

// Pool 是按 target 名缓存的客户端池。
//
// 并发安全：全部字段访问都在 mu 保护下；conf 由调用方保证并发安全
//（agentd 侧传的是 Server.conf，读的是 atomic 快照）。
type Pool struct {
	conf func() *config.Config
	log  *slog.Logger

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
}

// NewPool 构造复用池。
//
// 参数：
//   - conf: 取当前配置快照的函数；**每次 For/Names 都会现调**，池因此跟随活配置
//   - log: 日志器；nil 时用 slog.Default()
func NewPool(conf func() *config.Config, log *slog.Logger) *Pool {
	if log == nil {
		log = slog.Default()
	}
	return &Pool{conf: conf, log: log, entries: make(map[string]*entry)}
}

// Names 返回当前配置里全部 target 名，已排序。
//
// 排序是为了让 UI 列表与日志顺序稳定：每次刷新都跳序会让人以为数据在变。
func (p *Pool) Names() []string {
	targets := p.conf().Targets
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// For 取一台机器的客户端，必要时构造或重建。
//
// 参数：
//   - name: target 名，必须已在配置里登记
//
// 返回：
//   - client: **调用方不负责关闭**——隧道归池所有，进程退出时由 Close 统一关
//   - err: 机器未登记、池已关闭、或 New 的选路错误（ErrNoEndpoint / token 熵不足）
//
// 注意：不发任何网络请求。relay 隧道由 Dialer 惰性建立或由 Warm 预热。
func (p *Pool) For(name string) (*client.Client, error) {
	t, ok := p.conf().Targets[name]
	if !ok {
		// 配置里没有了：连带把可能残留的缓存条目关掉，否则一条隧道会一直挂着
		p.drop(name)
		p.log.Warn("请求未登记机器的客户端", "target", name)
		return nil, fmt.Errorf("target %s 未在配置中登记", name)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("target 客户端池已关闭")
	}
	if e, ok := p.entries[name]; ok {
		if e.target == t {
			return e.client, nil
		}
		// 配置变了：旧隧道用的是旧 token/节点，必须关掉重建
		p.log.Info("target 配置变更，重建客户端", "target", name)
		e.cleanup()
		delete(p.entries, name)
	}

	c, cleanup, err := New(name, t, p.log)
	if err != nil {
		return nil, err
	}
	p.entries[name] = &entry{target: t, client: c, cleanup: cleanup}
	p.log.Info("target 客户端已建立并入池", "target", name, "relay", t.IsRelay(), "pool_size", len(p.entries))
	return c, nil
}

// drop 关掉并移出一条缓存条目；不存在时无副作用。
func (p *Pool) drop(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[name]
	if !ok {
		return
	}
	e.cleanup()
	delete(p.entries, name)
	p.log.Info("target 已从池中移出", "target", name, "pool_size", len(p.entries))
}

// size 返回池内条目数，仅供测试断言。
func (p *Pool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Close 关掉池内全部客户端与隧道，之后 For 一律报错。
//
// 注意：relay.Dialer.Close 是终态（closed 标志阻止重连），所以池关了就不会
// 再复活——这符合进程退出语义，不要在运行期调它来「清一下缓存」。
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for name, e := range p.entries {
		e.cleanup()
		p.log.Debug("关闭 target 客户端", "target", name)
	}
	n := len(p.entries)
	p.entries = make(map[string]*entry)
	p.log.Info("target 客户端池已关闭", "closed", n)
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/targetclient/ -v`
Expected: 全部 PASS

- [ ] **Step 5: 跑一次竞态检测**

Run: `go test ./internal/targetclient/ -race`
Expected: PASS，无 race 报告

- [ ] **Step 6: 加关键节点日志**

核对 Step 3，六条都在：入池（Info，带 relay 位与池大小）、配置变更重建（Info）、移出（Info）、未登记（Warn）、逐条关闭（Debug）、池关闭汇总（Info）。**复用命中不打日志**——那是每轮扇出都会走的热路径。

- [ ] **Step 7: 加注释**

确认文件头有职责 + 边界，`entry.target` 有「为什么整体比较」的注释，`Close` 有「终态、不要当清缓存用」的注释。

- [ ] **Step 8: gofmt 与提交**

```bash
gofmt -l internal/targetclient/ && go test ./internal/targetclient/ -race
git add internal/targetclient/
git commit -m "$(cat <<'EOF'
feat(targetclient): 加常驻复用池，一台机器一条隧道

按 target 名缓存，配置整体比较失效重建，删除即移出。Names 提供「当前有
哪些机器」的唯一判据，跟随活快照——Mirror 的静态 cfg 问题由此解决。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: `Pool.Warm` 预热循环

**Files:**
- Create: `internal/targetclient/warm.go`
- Test: `internal/targetclient/warm_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Dialer.Ensure`、Task 4 的 `Pool`
- Produces:
  - `func (p *Pool) Warm(ctx context.Context)` —— 阻塞直到 ctx 取消
  - 内部缝：`p.ensure func(ctx context.Context, name string) error`，测试替换它以避开真 relay

- [ ] **Step 1: 写失败的测试**

创建 `internal/targetclient/warm_test.go`：

```go
package targetclient

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestWarmOnlyTouchesRelayTargets：预热只碰 relay 机器。
//
// why：直连没有隧道可预热，对它调 Ensure 纯属空转；更要紧的是别让直连机器的
// 「预热失败」进日志——那会造出一个不存在的故障。
func TestWarmOnlyTouchesRelayTargets(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{
		"direct": {Addr: "10.0.0.2:7777", Token: "t"},
		"relayed": {Relay: "wss://r.example.com/relay", Credential: "c",
			Node: "n", Token: "0123456789abcdef0123456789abcdef"},
	}), slog.Default())
	defer p.Close()

	var mu sync.Mutex
	var touched []string
	p.ensure = func(ctx context.Context, name string) error {
		mu.Lock()
		defer mu.Unlock()
		touched = append(touched, name)
		return nil
	}
	p.warmTick = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	p.Warm(ctx)

	mu.Lock()
	defer mu.Unlock()
	for _, name := range touched {
		if name != "relayed" {
			t.Fatalf("预热碰了非 relay 机器: %v", touched)
		}
	}
	if len(touched) == 0 {
		t.Fatal("relay 机器一次都没被预热")
	}
}

// TestWarmBacksOffPerTarget：一台机器失败不影响另一台的节奏。
//
// why：一台长期离线的机器如果能把全局退避拖长，另一台刚上线的机器就要陪着等——
// 退避必须各算各的。
func TestWarmBacksOffPerTarget(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{
		"bad": {Relay: "wss://r.example.com/relay", Credential: "c",
			Node: "bad", Token: "0123456789abcdef0123456789abcdef"},
		"good": {Relay: "wss://r.example.com/relay", Credential: "c",
			Node: "good", Token: "0123456789abcdef0123456789abcdef"},
	}), slog.Default())
	defer p.Close()

	var mu sync.Mutex
	counts := map[string]int{}
	p.ensure = func(ctx context.Context, name string) error {
		mu.Lock()
		counts[name]++
		mu.Unlock()
		if name == "bad" {
			return errors.New("节点离线")
		}
		return nil
	}
	p.warmTick = 10 * time.Millisecond
	p.warmBackoffInitial = 500 * time.Millisecond // 远大于 tick：bad 会被跳过

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	p.Warm(ctx)

	mu.Lock()
	defer mu.Unlock()
	if counts["good"] < 3 {
		t.Fatalf("正常机器要每轮都预热，实得 %d 次", counts["good"])
	}
	if counts["bad"] > 2 {
		t.Fatalf("失败机器要退避，实得 %d 次", counts["bad"])
	}
}

// TestWarmStopsOnContextCancel：ctx 取消后立刻返回。
func TestWarmStopsOnContextCancel(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{}), slog.Default())
	defer p.Close()
	p.warmTick = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Warm(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Warm 没有返回")
	}
}
```

- [ ] **Step 2: 给 Pool 加预热用的字段**

在 `internal/targetclient/pool.go` 的 `Pool` 结构体里追加：

```go
	// 预热参数与缝。warmTick/warmBackoff* 生产用包级默认，测试注入毫秒级值；
	// ensure 生产为 nil（走 realEnsure），测试替换以避开真 relay 服务端。
	warmTick           time.Duration
	warmBackoffInitial time.Duration
	warmBackoffMax     time.Duration
	ensure             func(ctx context.Context, name string) error
```

`NewPool` 里补默认值（`import` 补 `context`、`time`）：

```go
	return &Pool{
		conf: conf, log: log, entries: make(map[string]*entry),
		warmTick:           warmTick,
		warmBackoffInitial: warmBackoffInitial,
		warmBackoffMax:     warmBackoffMax,
	}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/targetclient/ -run TestWarm -v`
Expected: 编译失败，`p.Warm undefined`

- [ ] **Step 4: 写预热实现**

创建 `internal/targetclient/warm.go`：

```go
// 本文件实现 relay 隧道的后台预热。
//
// 职责：周期性地对每台 relay 机器主动建隧道，让探活拿到的是一条已经就绪的通道
//
// 边界：
//   - **预热只保证隧道，不代表可达**：隧道通了但对端 agentd 没起，机器照样是
//     「已断开」。两个判据不合并——合并会让「网络不通」和「服务没起」这两种
//     完全不同的故障显示成同一句话
//   - 不碰直连机器：它们没有隧道可预热
//   - 不占探活预算：探活只有 3s，而首次建隧道要 WSS 拨号 + CONNECT + E2E 握手
package targetclient

import (
	"context"
	"time"
)

// 预热节奏。tick 与 mirrorDiscoveryTick 同量级：预热是补漏，不是心跳。
const (
	warmTick           = 30 * time.Second
	warmBackoffInitial = 1 * time.Second
	warmBackoffMax     = 60 * time.Second
)

// warmState 是单台机器的退避状态。
//
// 退避**各算各的**：一台长期离线的机器不能把其余机器的重试节奏一起拖慢。
type warmState struct {
	backoff time.Duration
	nextAt  time.Time
}

// Warm 跑预热循环，阻塞直到 ctx 取消。
//
// 参数：
//   - ctx: 生命周期；取消即返回
//
// 注意：
//   - 只对 relay 形态的 target 生效
//   - 单台失败按 1s→60s 指数退避，退避期内跳过该台，不影响其余
//   - 新增的机器由下一轮扫到；删除的机器自然不再出现在 Names() 里
func (p *Pool) Warm(ctx context.Context) {
	p.log.Info("relay 隧道预热循环启动", "tick", p.warmTick.String())
	states := make(map[string]*warmState)
	t := time.NewTicker(p.warmTick)
	defer t.Stop()

	p.warmOnce(ctx, states)
	for {
		select {
		case <-ctx.Done():
			p.log.Info("relay 隧道预热循环退出", "reason", "上下文取消")
			return
		case <-t.C:
			p.warmOnce(ctx, states)
		}
	}
}

// warmOnce 跑一轮预热：对每台处于「可以重试」状态的 relay 机器建一次隧道。
func (p *Pool) warmOnce(ctx context.Context, states map[string]*warmState) {
	targets := p.conf().Targets
	now := time.Now()
	warmed, skipped, failed := 0, 0, 0

	for _, name := range p.Names() {
		t := targets[name]
		if !t.IsRelay() {
			continue // 直连没有隧道可预热
		}
		if st, ok := states[name]; ok && now.Before(st.nextAt) {
			skipped++
			continue
		}
		// 每台独立限时：一台黑洞机器不能把整轮拖住
		callCtx, cancel := context.WithTimeout(ctx, p.warmTick)
		err := p.ensureTunnel(callCtx, name)
		cancel()
		if err != nil {
			failed++
			st := states[name]
			if st == nil {
				st = &warmState{backoff: p.warmBackoffInitial}
				states[name] = st
			} else {
				st.backoff *= 2
				if st.backoff > p.warmBackoffMax {
					st.backoff = p.warmBackoffMax
				}
			}
			st.nextAt = now.Add(st.backoff)
			p.log.Warn("relay 隧道预热失败，等待后重试", "target", name, "node", t.Node,
				"backoff_ms", st.backoff.Milliseconds(), "cause", err)
			continue
		}
		warmed++
		if _, had := states[name]; had {
			// 恢复了：清掉退避，下一轮回到正常节奏
			delete(states, name)
			p.log.Info("relay 隧道预热恢复", "target", name, "node", t.Node)
		}
	}
	p.log.Debug("relay 隧道预热完成一轮", "warmed", warmed, "skipped", skipped, "failed", failed)
}

// ensureTunnel 对一台机器建隧道；测试可用 p.ensure 替换掉真实拨号。
func (p *Pool) ensureTunnel(ctx context.Context, name string) error {
	if p.ensure != nil {
		return p.ensure(ctx, name)
	}
	return p.realEnsure(ctx, name)
}

// realEnsure 是生产实现：取出（必要时构造）该机器的 Dialer 并建隧道。
func (p *Pool) realEnsure(ctx context.Context, name string) error {
	if _, err := p.For(name); err != nil {
		return err
	}
	p.mu.Lock()
	e, ok := p.entries[name]
	p.mu.Unlock()
	if !ok || e.dialer == nil {
		// 直连条目没有 dialer；warmOnce 已过滤过，走到这里说明配置刚变形态，
		// 下一轮自然纠正，不当失败处理
		return nil
	}
	return e.dialer.Ensure(ctx)
}
```

- [ ] **Step 5: 让 entry 记住 Dialer**

`realEnsure` 需要 Dialer。改 `internal/targetclient/pool.go` 的 `entry`：

```go
type entry struct {
	target config.Target
	client *client.Client
	// dialer 只在 relay 形态非 nil；预热要拿它主动建隧道。
	dialer  *relay.Dialer
	cleanup func()
}
```

并把 `New` 拆出一个内部构造，让 Pool 拿得到 Dialer。在 `targetclient.go` 里加：

```go
// newWithDialer 与 New 同源，额外把 relay Dialer 交给调用方（池要用它预热）。
// New 是它的薄壳：外部调用方不该拿到 Dialer，那是池的内部事务。
func newWithDialer(name string, t config.Target, log *slog.Logger) (*client.Client, *relay.Dialer, func(), error) {
	if log == nil {
		log = slog.Default()
	}
	noop := func() {}
	if t.IsRelay() {
		if err := relay.CheckTokenEntropy(t.Token); err != nil {
			log.Error("relay target 的 token 熵不足，拒绝构造", "target", name, "node", t.Node)
			return nil, nil, noop, fmt.Errorf("target %s: %w", name, err)
		}
		d := relay.NewDialer(t.Relay, t.Credential, t.Node, t.Token, "", log)
		log.Info("target 走 relay 传输", "target", name, "node", t.Node, "relay_url", t.Relay)
		return client.NewRelay(d, t.Token), d, func() { _ = d.Close() }, nil
	}
	if t.Addr == "" {
		log.Error("target 无端点，既没有 addr 也没有 relay", "target", name)
		return nil, nil, noop, fmt.Errorf("target %s: %w", name, ErrNoEndpoint)
	}
	log.Debug("target 走直连传输", "target", name, "addr", t.Addr)
	return client.New("http://"+t.Addr, t.Token), nil, noop, nil
}

// New 按 Target 形态选路，构造一个一次性的 agentd 客户端。
// （文档注释保持 Task 3 写的那份，函数体换成下面这行）
func New(name string, t config.Target, log *slog.Logger) (*client.Client, func(), error) {
	c, _, cleanup, err := newWithDialer(name, t, log)
	return c, cleanup, err
}
```

`Pool.For` 里改为调 `newWithDialer` 并填 `dialer` 字段（`pool.go` 的 import 补 `"github.com/Xsxdot/handoff/internal/relay"`；`warm.go` 不需要它——它只碰 `e.dialer.Ensure`）。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/targetclient/ -v -race`
Expected: 全部 PASS

- [ ] **Step 7: 加关键节点日志**

核对：循环启动（Info，带 tick）、循环退出（Info，带原因）、单台失败（Warn，带 target/node/退避/cause）、恢复（Info）、每轮汇总（Debug——30s 一轮，Info 会在长期运行时刷屏）。**成功预热单条不打 Info**，汇总里有计数。

- [ ] **Step 8: 加注释**

确认 `warm.go` 文件头写清「预热只保证隧道，不代表可达」这条边界——这是整个设计里最容易被后人合并掉的判据。

- [ ] **Step 9: gofmt 与提交**

```bash
gofmt -l internal/targetclient/ && go test ./internal/targetclient/ -race
git add internal/targetclient/
git commit -m "$(cat <<'EOF'
feat(targetclient): 加 relay 隧道后台预热循环

探活只有 3s 预算，而首次建隧道要 WSS 拨号 + CONNECT + E2E 握手。预热用
独立超时提前把隧道备好，单台失败按 1s→60s 各自退避。

预热只保证隧道、不代表可达：隧道通了但对端没起，机器照样显示已断开。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: `cmd/root.go` 重构到工厂

**Files:**
- Modify: `cmd/root.go:246-277`（`newTargetClientNamed`）
- Test: `cmd/target_client_test.go`（追加）

**Interfaces:**
- Consumes: Task 3 的 `targetclient.New`
- Produces: `newTargetClientNamed` 行为不变，选路逻辑不再自己写

- [ ] **Step 1: 写失败的测试**

追加到 `cmd/target_client_test.go`：

```go
// TestNamedTargetNoEndpointReportsClearly：无端点的 target 报清楚的错，
// 而不是造出一个注定失败的直连 client。
//
// why：这正是 relay 显示问题的镜像面——CLI 侧本来就不会走到这里，但重构后
// 两侧共用一个工厂，这条断言保证共用之后 CLI 的错误语义只会变好不会变差。
func TestNamedTargetNoEndpointReportsClearly(t *testing.T) {
	cfg := writeTestConfig(t, `listen: "127.0.0.1:7777"
token: "local-token"
targets:
  broken:
    token: "some-token"
`)
	resetFlags(t)
	configPath = cfg
	targetName = "broken"

	_, cleanup, err := newTargetClient()
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("无端点的 target 必须报错")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("错误要点名 target，实得 %v", err)
	}
}
```

（`writeTestConfig` 与 `resetFlags` 是该文件既有助手，见 `TestEndpointsPreserveRelayTransportForUpgrade`；`import` 补 `strings`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestNamedTargetNoEndpointReportsClearly -v`
Expected: FAIL —— 现状会造出一个 addr 为空的直连 client 并返回 nil error

- [ ] **Step 3: 重构 `newTargetClientNamed`**

把 `cmd/root.go` 里 `if !t.IsRelay() { ... }` 到函数末尾的整段替换为：

```go
	// 选路交给 targetclient：CLI 与 agentd 必须用同一个判据。判据存两份正是
	// relay 机器在控制台显示「已断开」的成因——agentd 那份从来没被写出来。
	return targetclient.New(name, t, slog.Default())
```

`import` 加 `"github.com/Xsxdot/handoff/internal/targetclient"`；若 `relay` 与 `client` 在本文件别处不再使用，删掉对应 import。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestNamedTargetNoEndpointReportsClearly -v`
Expected: PASS

- [ ] **Step 5: 跑 cmd 包回归**

Run: `go test ./cmd/`
Expected: PASS（既有的 relay/直连选路测试必须全绿——它们锁的正是重构前的行为）

- [ ] **Step 6: 加关键节点日志**

选路日志已在 `targetclient.New` 里（Task 3），这里**不再重复打**——重复日志会让「用了 relay」在一次派发里出现两遍，看日志的人会以为建了两条隧道。在替换处的注释里写明这一点。

- [ ] **Step 7: 加注释**

确认替换处有「为什么必须共用同一个判据」的注释。

- [ ] **Step 8: gofmt 与提交**

```bash
gofmt -l cmd/ && go test ./cmd/
git add cmd/root.go cmd/target_client_test.go
git commit -m "$(cat <<'EOF'
refactor(cmd): CLI 选路改走 targetclient 工厂

CLI 与 agentd 从此共用同一个 relay/直连判据。附一条断言：无端点的 target
要点名报错，而不是造出注定失败的直连 client。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: agentd 接线（Server 持池 + 预热 + 退出关池）

**Files:**
- Modify: `internal/agentd/server.go`（`Server` 结构体、`NewServer`）
- Modify: `cmd/agentd.go:215-240`
- Test: `internal/agentd/pool_wiring_test.go`

**Interfaces:**
- Consumes: Task 4/5 的 `targetclient.NewPool` / `Pool.Warm` / `Pool.Close`
- Produces:
  - `Server.pool *targetclient.Pool`（私有字段，包内六个调用点直接用）
  - `func (s *Server) Pool() *targetclient.Pool` —— 供 `cmd/agentd.go` 起预热、给 Mirror 注入
  - `func (s *Server) CloseTargets() error` —— 关池

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/pool_wiring_test.go`：

```go
// 本文件锁死「Server 恒持有一个可用的 target 客户端池」。
//
// why 值得单测：NewServer 有约 50 个调用点，池若靠外部注入，漏注入的那些路径
// 会在运行时空指针崩溃——而它们大多是测试路径，生产上要等到第一次扇出才炸。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestServerAlwaysHasPool：NewServer 出来就带池，无需任何注入。
func TestServerAlwaysHasPool(t *testing.T) {
	s := NewServer(&config.Config{Listen: "127.0.0.1:0"}, nil, testLogger(t))
	if s.Pool() == nil {
		t.Fatal("NewServer 必须自带 target 客户端池")
	}
	defer s.CloseTargets()
}

// TestServerPoolFollowsLiveConfig：池读的是活快照，不是构造时的那份。
func TestServerPoolFollowsLiveConfig(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:0", Targets: map[string]config.Target{}}
	s := NewServer(cfg, nil, testLogger(t))
	defer s.CloseTargets()

	next := *cfg
	next.Targets = map[string]config.Target{"mac-02": {Addr: "10.0.0.2:7777", Token: "t"}}
	s.cfg.Store(&next)

	names := s.Pool().Names()
	if len(names) != 1 || names[0] != "mac-02" {
		t.Fatalf("池要跟随活快照，实得 %v", names)
	}
}
```

（`testLogger(t)` 是本包既有助手，定义在 `internal/agentd/watchdog_fence_test.go:152`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestServerAlwaysHasPool -v`
Expected: 编译失败，`s.Pool undefined`

- [ ] **Step 3: Server 持池**

`internal/agentd/server.go` 的 `Server` 结构体里追加：

```go
	// pool 是对 target 的客户端复用池（探活/镜像/项目树/PTY/升级共用）。
	//
	// 为什么在 NewServer 里自建而不是靠注入：NewServer 有约 50 个调用点，
	// 靠注入必然漏，而漏掉的表现是运行时空指针。池的构造零成本（不发请求），
	// 自建没有代价。
	pool *targetclient.Pool
```

`NewServer` 里 `s.cfg.Store(cfg)` **之后**加（顺序要紧：池的 conf 回调会读快照）：

```go
	s.pool = targetclient.NewPool(s.conf, log)
```

在 `SetConfigPath` 附近加两个方法：

```go
// Pool 返回 target 客户端复用池。
//
// 用途：cmd/agentd.go 起预热循环、给 Mirror 注入同一个池——**必须是同一个**，
// 两个池等于两套隧道，relay 侧会看到重复的节点连接。
func (s *Server) Pool() *targetclient.Pool { return s.pool }

// CloseTargets 关掉池内全部客户端与 relay 隧道。
//
// 注意：只在进程退出路径调用。池关了就不再复活（relay.Dialer.Close 是终态）。
func (s *Server) CloseTargets() error { return s.pool.Close() }
```

`import` 加 `"github.com/Xsxdot/handoff/internal/targetclient"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestServerAlwaysHasPool|TestServerPoolFollowsLiveConfig' -v`
Expected: PASS

- [ ] **Step 5: `cmd/agentd.go` 起预热并在退出时关池**

在 relay listener 那段之后、看门狗之前插入：

```go
		// relay 隧道预热：探活只有 3s 预算，首次建隧道（WSS + CONNECT + E2E）
		// 装不进去。预热用独立超时提前备好，让探活只花在 /api/status 上。
		go srv.Pool().Warm(wdCtx)
		logger.Info("relay 隧道预热已启动")
```

在进程退出路径（`wdCtx` 取消之后、进程返回之前，与既有的收尾动作同处）加：

```go
		if err := srv.CloseTargets(); err != nil {
			logger.Warn("关闭 target 客户端池失败", "cause", err)
		}
```

- [ ] **Step 6: 编译并跑全量**

Run: `go build ./... && go test ./internal/agentd/ ./cmd/`
Expected: PASS

- [ ] **Step 7: 加关键节点日志**

核对：预热启动有 Info（`cmd/agentd.go`）、关池失败有 Warn。池自身的日志在 Task 4/5，这里不重复。

- [ ] **Step 8: 加注释**

确认 `pool` 字段注释写了「为什么自建不靠注入」，`Pool()` 注释写了「必须是同一个池」。

- [ ] **Step 9: gofmt 与提交**

```bash
gofmt -l internal/agentd/ cmd/ && go test ./internal/agentd/ ./cmd/
git add internal/agentd/ cmd/agentd.go
git commit -m "$(cat <<'EOF'
feat(agentd): Server 自带 target 客户端池，启动起预热、退出关池

池在 NewServer 里自建而不是靠注入：NewServer 有约 50 个调用点，靠注入
必然漏，漏掉的表现是运行时空指针。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: 探活改走池 + `Machine.Relay` 字段

**Files:**
- Modify: `internal/agentd/machines.go:106-131`（`probeMachines` 枚举、`probeRemote`）
- Modify: `internal/proto/projects.go:109-146`（`Machine`）
- Test: `internal/agentd/machines_relay_test.go`

**Interfaces:**
- Consumes: Task 7 的 `s.pool`
- Produces: `proto.Machine.Relay string`（JSON `relay,omitempty`）—— relay 节点名；直连机器为空

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/machines_relay_test.go`：

```go
// 本文件锁死 relay 形态机器在 GET /api/machines 上的两条：不再因为没有 addr
// 而被当成不可达，且带上可展示的中继身份。
package agentd

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestProbeRelayTargetDoesNotReportNoHost：relay 机器探活失败时，
// 失败原因必须是 relay 拨号的真实原因，不能是 "no Host in request URL"。
//
// why 这条是回归本尊：relay target 没有 addr，旧代码用 client.New("") 造出
// baseURL="http:"，请求 URL 退化成 http:/api/status。界面上显示的「已断开」
// 其实是请求压根没发出去。
func TestProbeRelayTargetDoesNotReportNoHost(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:0",
		Targets: map[string]config.Target{
			"linux-01": {
				Relay: "wss://127.0.0.1:1/relay", Credential: "cred",
				Node: "linux-01", Token: "0123456789abcdef0123456789abcdef",
			},
		},
	}
	s := NewServer(cfg, nil, testLogger(t))
	defer s.CloseTargets()

	m := s.probeRemote(context.Background(), "linux-01")
	if m.Reachable {
		t.Fatal("拨不通的 relay 不该判为可达")
	}
	if strings.Contains(m.Error, "no Host in request URL") {
		t.Fatalf("relay 机器不该再报 no Host：%s", m.Error)
	}
	if m.Relay != "linux-01" {
		t.Fatalf("relay 机器要带节点名，实得 %q", m.Relay)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestProbeRelayTargetDoesNotReportNoHost -v`
Expected: FAIL，`m.Relay` 字段不存在（编译失败）

- [ ] **Step 3: 加 `Machine.Relay`**

`internal/proto/projects.go` 的 `Machine` 里，`Addr` 之后插入：

```go
	// Relay 是这台机器的 relay 节点名；空=直连形态。
	//
	// 为什么需要它：relay 形态与 addr 互斥，中继机器的 Addr 恒为空，界面上
	// 那张卡片会一个身份标识都没有。前端在 Addr 为空时用它显示「中继 · <node>」。
	Relay string `json:"relay,omitempty"`
```

- [ ] **Step 4: `probeRemote` 改走池**

替换 `internal/agentd/machines.go` 的 `probeRemote` 前半段：

```go
// probeRemote 探活一台远程机器。
func (s *Server) probeRemote(ctx context.Context, name string) proto.Machine {
	t := s.conf().Targets[name]
	m := proto.Machine{Name: name, Addr: t.Addr, Relay: t.Node, Executors: []string{}}
	start := time.Now()
	// 选路走池：relay 形态的机器没有 addr，直连构造会退化成一个没有 Host 的
	// URL——那正是它们曾经一律显示「已断开」的原因。
	c, err := s.pool.For(name)
	if err != nil {
		m.ProbeMs = time.Since(start).Milliseconds()
		s.log.Warn("机器探活：取客户端失败", "machine", name, "relay", t.IsRelay(), "cause", err)
		m.Error = err.Error()
		return m
	}
	// 注意：token 只进请求头，绝不进日志
	st, err := c.Status(ctx)
	m.ProbeMs = time.Since(start).Milliseconds()
	if err != nil {
		s.log.Warn("机器探活失败", "machine", name, "addr", t.Addr, "relay", t.Node,
			"probe_ms", m.ProbeMs, "cause", err)
		m.Error = err.Error()
		return m
	}
	m.Reachable = true
	fillFromStatus(&m, st)
	s.log.Debug("机器探活成功", "machine", name, "probe_ms", m.ProbeMs,
		"active_tasks", m.ActiveTasks)
	return m
}
```

- [ ] **Step 5: `probeMachines` 用 `Names()` 枚举**

把 `probeMachines` 里的：

```go
	names := make([]string, 0, len(s.conf().Targets))
	for name := range s.conf().Targets {
		names = append(names, name)
	}
	sort.Strings(names) // 顺序稳定：UI 列表不该每次刷新都跳
```

换成：

```go
	// 「有哪些机器」的判据只有一处：池。Names 已排序（顺序稳定：UI 列表不该
	// 每次刷新都跳）。
	names := s.pool.Names()
```

若 `sort` 在本文件别处不再使用，删掉 import。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestProbeRelayTargetDoesNotReportNoHost -v`
Expected: PASS

- [ ] **Step 7: 跑 agentd 包回归**

Run: `go test ./internal/agentd/`
Expected: PASS

- [ ] **Step 8: 加关键节点日志**

核对：取客户端失败（Warn，带 machine/relay 位/cause）、探活失败（Warn，带 machine/addr/relay/probe_ms/cause）、成功（Debug）。**token 不进日志**。

- [ ] **Step 9: 加注释**

确认 `probeRemote` 里有「为什么走池」的注释，`Machine.Relay` 有字段注释。

- [ ] **Step 10: gofmt 与提交**

```bash
gofmt -l internal/agentd/ internal/proto/ && go test ./internal/agentd/ ./internal/proto/
git add internal/agentd/ internal/proto/
git commit -m "$(cat <<'EOF'
fix(agentd): 探活改走 target 池，relay 机器不再一律显示已断开

relay target 没有 addr，client.New("") 造出的 baseURL 是 "http:"，请求
URL 退化成 http:/api/status——「已断开」其实是请求压根没发出去。

顺带给 proto.Machine 加 Relay 字段：中继机器的卡片过去没有任何身份标识。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: 新增机器探测改走工厂

**Files:**
- Modify: `internal/agentd/machines.go:190-205`（`handleAddMachine` 的探测段）
- Test: `internal/agentd/machines_addprobe_test.go`

**Interfaces:**
- Consumes: Task 3 的 `targetclient.New`
- Produces: 无新接口；行为变化是空 addr 的新增请求报「无端点」而非 `no Host`

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/machines_addprobe_test.go`：

```go
// 本文件锁死：新增开发机时空地址要被明确拒绝，而不是产出一个网络错误。
//
// why：控制台目前只能新增直连机器（AddMachineReq 没有 relay 字段），空 addr
// 一定是用户漏填。把它报成 "no Host in request URL" 等于让人去查网络。
package agentd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestAddMachineEmptyAddrRejectedClearly：空地址被校验或探测明确拒绝。
func TestAddMachineEmptyAddrRejectedClearly(t *testing.T) {
	s := NewServer(&config.Config{Listen: "127.0.0.1:0",
		Targets: map[string]config.Target{}}, nil, testLogger(t))
	defer s.CloseTargets()

	body, _ := json.Marshal(proto.AddMachineReq{Name: "ghost", Addr: "", Token: "tok"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/machines", bytes.NewReader(body))
	s.handleAddMachine(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空地址要 400，实得 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "no Host in request URL") {
		t.Fatalf("不该报网络错误：%s", rec.Body.String())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestAddMachineEmptyAddrRejectedClearly -v`
Expected: 视 `validateAddMachine` 现有行为而定——若它已拦空 addr 则测试直接通过，
此时**不要删测试**：它锁的是「不许出现 no Host 文案」这条长期约束。若未拦则 FAIL。

- [ ] **Step 3: 探测段改走工厂**

把 `handleAddMachine` 里的探测行：

```go
		if _, err := client.New(req.Addr, req.Token).NoRedirect().Status(ctx); err != nil {
```

换成：

```go
		// 走工厂而不是直连构造：空 addr 会被 ErrNoEndpoint 明确拒绝，不再退化成
		// 一个没有 Host 的 URL。控制台目前只能新增直连机器（AddMachineReq 没有
		// relay 字段），relay 形态的新增是另一张单。
		probeClient, cleanup, newErr := targetclient.New(req.Name,
			config.Target{Addr: req.Addr, Token: req.Token}, s.log)
		if newErr != nil {
			s.log.Warn("新增开发机：无法构造探测客户端", "name", req.Name, "cause", newErr)
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("探测 %s 失败：%v", req.Addr, newErr),
			})
			return
		}
		defer cleanup()
		if _, err := probeClient.NoRedirect().Status(ctx); err != nil {
```

`import` 补 `"github.com/Xsxdot/handoff/internal/targetclient"`（`config`、`fmt` 已在）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestAddMachineEmptyAddrRejectedClearly -v`
Expected: PASS

- [ ] **Step 5: 跑 agentd 包回归**

Run: `go test ./internal/agentd/`
Expected: PASS（既有的新增机器测试必须全绿）

- [ ] **Step 6: 加关键节点日志**

核对：构造失败 Warn（带 name/cause）、探测失败的既有 Warn 保留、探测通过的既有 Info 保留。

- [ ] **Step 7: 加注释**

确认替换处写了「为什么走工厂」以及「relay 形态的新增是另一张单」。

- [ ] **Step 8: gofmt 与提交**

```bash
gofmt -l internal/agentd/ && go test ./internal/agentd/
git add internal/agentd/
git commit -m "$(cat <<'EOF'
fix(agentd): 新增开发机的探测改走 targetclient 工厂

空 addr 从此被 ErrNoEndpoint 明确拒绝，不再退化成没有 Host 的 URL。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Mirror 改走池并瘦身

**Files:**
- Modify: `internal/agentd/mirror.go`（`Mirror` 结构体、`NewMirror`、`discoverOnce`、`subscribe`、`onEvent`、`refreshSnapshot`）
- Modify: `cmd/agentd.go:230-238`
- Test: `internal/agentd/mirror_pool_test.go`

**Interfaces:**
- Consumes: Task 7 的 `s.Pool()`
- Produces: `func NewMirror(pool *targetclient.Pool, st *store.Store, hub *Hub, log *slog.Logger) *Mirror`（**签名变更**：首参由 `*config.Config` 换成池）

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/mirror_pool_test.go`：

```go
// 本文件锁死 Mirror 跟随活配置：控制台运行期新增的机器无需重启即被镜像。
//
// why：Mirror 过去拿的是 NewMirror 时的静态 cfg，加一台机器要重启 agentd 才
// 会被发现——而「加完看不见」很容易被误当成对端故障去查。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestMirrorSeesTargetsAddedAtRuntime：运行期新增的 target 立刻进入枚举。
func TestMirrorSeesTargetsAddedAtRuntime(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:0", Targets: map[string]config.Target{}}
	s := NewServer(cfg, nil, testLogger(t))
	defer s.CloseTargets()

	m := NewMirror(s.Pool(), nil, NewHub(), testLogger(t))
	if got := len(m.machineNames()); got != 0 {
		t.Fatalf("初始应为 0 台，实得 %d", got)
	}

	next := *cfg
	next.Targets = map[string]config.Target{"linux-01": {Addr: "10.0.0.3:7777", Token: "t"}}
	s.cfg.Store(&next)

	names := m.machineNames()
	if len(names) != 1 || names[0] != "linux-01" {
		t.Fatalf("运行期新增的机器要立刻可见，实得 %v", names)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestMirrorSeesTargetsAddedAtRuntime -v`
Expected: 编译失败，`NewMirror` 首参类型不符 / `machineNames` 不存在

- [ ] **Step 3: Mirror 换持池**

`internal/agentd/mirror.go`：把 `Mirror` 结构体的 `cfg *config.Config` 字段换成：

```go
	// pool 同时提供两样东西：客户端与「有哪些机器」的判据。
	//
	// 为什么不再持 *config.Config：那是 NewMirror 时的静态快照，控制台运行期
	// 新增的机器要重启 agentd 才会被镜像——而「加完看不见」很容易被误当成
	// 对端故障去查。
	pool *targetclient.Pool
```

`NewMirror` 首参相应改为 `pool *targetclient.Pool` 并填字段。加一个内部方法：

```go
// machineNames 返回当前要镜像的机器名，已排序。判据只有一处：池。
func (m *Mirror) machineNames() []string { return m.pool.Names() }
```

- [ ] **Step 4: 三个调用点改走池，并删掉穿层的 Target 参数**

`discoverOnce`：枚举段换成 `names := m.machineNames()`；扇出体换成：

```go
			c, err := m.pool.For(name)
			if err != nil {
				results[i] = result{name: name, err: err}
				return
			}
			views, err := c.MarkForwarded().ListTasks(fanCtx)
			results[i] = result{name: name, views: views, err: err}
```

订阅启动处删掉 `t := m.cfg.Targets[r.name]`，改为 `go m.subscribe(subCtx, r.name, tv.Task.ID)`。

`subscribe`：签名去掉 `t config.Target`，请求段换成：

```go
		c, err := m.pool.For(machine)
		if err != nil {
			m.log.Warn("镜像订阅：取客户端失败", "task", taskID, "machine", machine, "cause", err)
			return
		}
		err = c.MarkForwarded().StreamEventsOnce(ctx, taskID, fromSeq,
			func(ev proto.Event) error { return m.onEvent(ctx, machine, taskID, ev) })
```

`onEvent`：签名去掉 `t config.Target`（函数体里本来就没用它——它存在的唯一理由是末端要造 client）。

`refreshSnapshot`：签名去掉 `t config.Target`，请求段换成：

```go
	c, err := m.pool.For(machine)
	if err != nil {
		m.log.Warn("镜像快照刷新：取客户端失败", "machine", machine, "cause", err)
		return
	}
	tasks, err := c.MarkForwarded().ListTasks(ctx)
	if err != nil {
		m.log.Warn("镜像快照刷新失败", "machine", machine, "cause", err)
		return
	}
```

修掉所有随之失配的调用点（`onEvent`/`refreshSnapshot` 的调用处）。若 `config` 与 `client` 在本文件不再使用，删掉 import。

- [ ] **Step 5: `cmd/agentd.go` 去掉启动闸**

把：

```go
		if len(cfg.Targets) > 0 {
			mirror := agentd.NewMirror(cfg, st, srv.Hub(), logger)
			go mirror.Run(wdCtx)
			logger.Info("事件镜像已启动", "targets", len(cfg.Targets), "tick", "30s")
		} else {
			logger.Info("未配置 targets，事件镜像未启动（无远程机器）")
		}
```

换成：

```go
		// 恒启动：镜像的机器清单现在来自活快照，启动时没有机器不代表以后没有。
		// 留着 len>0 的闸会让控制台新增的第一台机器永远等不到镜像。
		mirror := agentd.NewMirror(srv.Pool(), st, srv.Hub(), logger)
		go mirror.Run(wdCtx)
		logger.Info("事件镜像已启动", "targets", len(cfg.Targets), "tick", "30s")
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestMirrorSeesTargetsAddedAtRuntime -v`
Expected: PASS

- [ ] **Step 7: 跑全量回归**

Run: `go build ./... && go test ./internal/agentd/ ./cmd/`
Expected: PASS（既有的 mirror 测试要相应改掉 `NewMirror` 的调用形态）

- [ ] **Step 8: 加关键节点日志**

核对新增的两条：订阅取客户端失败（Warn，带 task/machine/cause）、快照刷新取客户端失败（Warn，带 machine/cause）。既有的发现/订阅/断线日志一条不动。

- [ ] **Step 9: 加注释**

确认 `pool` 字段注释写了「为什么不再持静态 cfg」，`cmd/agentd.go` 的替换处写了「为什么去掉启动闸」。

- [ ] **Step 10: gofmt 与提交**

```bash
gofmt -l internal/agentd/ cmd/ && go test ./internal/agentd/ ./cmd/
git add internal/agentd/ cmd/agentd.go
git commit -m "$(cat <<'EOF'
fix(agentd): 事件镜像改走 target 池，并跟随活配置

Mirror 过去持 NewMirror 时的静态 cfg，控制台加的机器要重启才被镜像。改
持池之后活快照是自然结果，穿过 subscribe/onEvent/refreshSnapshot 三层的
config.Target 参数一并删除——它存在的唯一理由是末端要造 client。

启动闸 len(cfg.Targets)>0 一并去掉：留着会让新增的第一台机器永远等不到。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: 项目树与 PTY 扇出改走池

**Files:**
- Modify: `internal/agentd/projectfanout.go:45-75`
- Modify: `internal/agentd/pty_api.go:180-210`
- Test: `internal/agentd/fanout_relay_test.go`

**Interfaces:**
- Consumes: Task 7 的 `s.pool`
- Produces: 无新接口

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/fanout_relay_test.go`：

```go
// 本文件锁死项目树与 PTY 两条扇出对 relay 机器的行为：失败要给 relay 的真实
// 原因，不能是 "no Host in request URL"。
package agentd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func relayOnlyServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Listen: "127.0.0.1:0",
		Targets: map[string]config.Target{
			"linux-01": {
				Relay: "wss://127.0.0.1:1/relay", Credential: "cred",
				Node: "linux-01", Token: "0123456789abcdef0123456789abcdef",
			},
		},
	}
	s := NewServer(cfg, nil, testLogger(t))
	t.Cleanup(func() { s.CloseTargets() })
	return s
}

// TestProjectTreeFanoutRelayError：项目树扇出对 relay 机器不报 no Host。
func TestProjectTreeFanoutRelayError(t *testing.T) {
	s := relayOnlyServer(t)
	out := s.buildTreeAll(context.Background())
	if len(out.Machines) == 0 {
		t.Fatal("relay 机器要出现在扇出结果里")
	}
	for _, m := range out.Machines {
		if strings.Contains(m.Error, "no Host in request URL") {
			t.Fatalf("不该报 no Host：%s", m.Error)
		}
	}
}

// TestPtyFanoutRelayError：PTY 扇出对 relay 机器不报 no Host。
func TestPtyFanoutRelayError(t *testing.T) {
	s := relayOnlyServer(t)
	// ptySessionsAll 收的是 *http.Request（它要从请求里取本机会话的上下文），
	// 不是裸 ctx；local 传 nil 表示本机没有会话。
	out := s.ptySessionsAll(httptest.NewRequest(http.MethodGet, "/api/pty/sessions", nil), nil)
	if len(out.Machines) == 0 {
		t.Fatal("relay 机器要出现在扇出结果里")
	}
	for _, m := range out.Machines {
		if strings.Contains(m.Error, "no Host in request URL") {
			t.Fatalf("不该报 no Host：%s", m.Error)
		}
	}
}
```

（两个扇出方法已确认存在且可直接测：`buildTreeAll` 在 [projectfanout.go:31](../../../internal/agentd/projectfanout.go)，`ptySessionsAll` 在 [pty_api.go:170](../../../internal/agentd/pty_api.go)。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestProjectTreeFanoutRelayError|TestPtyFanoutRelayError' -v`
Expected: FAIL —— 现状对 relay 机器报 `no Host in request URL`（Task 1 之后是毒化文案，同样不该出现）

- [ ] **Step 3: 项目树扇出改走池**

`internal/agentd/projectfanout.go`：枚举段换成 `names := s.pool.Names()`；扇出体换成：

```go
			st := proto.MachineStatus{Name: name, FetchedAt: time.Now().UTC()}
			c, err := s.pool.For(name)
			if err != nil {
				s.log.Warn("项目树扇出：取客户端失败", "machine", name, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			tree, err := c.MarkForwarded().ProjectTree(fanCtx)
			if err != nil {
				s.log.Warn("项目树扇出失败", "machine", name, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			st.Ok = true
			results[i] = result{status: st, tree: tree}
```

（原日志里的 `"addr", t.Addr` 去掉：relay 机器没有 addr，打空串是噪音；机器名已经足够定位。）

- [ ] **Step 4: PTY 扇出改走池**

`internal/agentd/pty_api.go` 同款替换：

```go
			st := proto.MachineStatus{Name: name, FetchedAt: time.Now().UTC()}
			c, err := s.pool.For(name)
			if err != nil {
				s.log.Warn("终端会话扇出：取客户端失败", "machine", name, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			resp, err := c.MarkForwarded().PtySessions(ctx)
			if err != nil {
				s.log.Warn("终端会话扇出失败", "machine", name, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			st.Ok = true
			results[i] = result{status: st, resp: resp}
```

枚举段同样换成 `s.pool.Names()`。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestProjectTreeFanoutRelayError|TestPtyFanoutRelayError' -v`
Expected: PASS

- [ ] **Step 6: 跑 agentd 包回归**

Run: `go test ./internal/agentd/`
Expected: PASS

- [ ] **Step 7: 加关键节点日志**

核对：两条扇出各有「取客户端失败」Warn 与「扇出失败」Warn，都带 machine 与 cause。成功路径沿用既有日志。

- [ ] **Step 8: 加注释**

在两处 `s.pool.For` 上方各写一行「为什么走池」（relay 机器没有 addr）。

- [ ] **Step 9: gofmt 与提交**

```bash
gofmt -l internal/agentd/ && go test ./internal/agentd/
git add internal/agentd/
git commit -m "$(cat <<'EOF'
fix(agentd): 项目树与 PTY 扇出改走 target 池

两条扇出对 relay 机器过去恒失败（控制台「项目目录数 0」即出自项目树这条）。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: 远程升级改走池

**Files:**
- Modify: `internal/agentd/machineupgrade.go:41-50`（`executeMachineUpgrade`）、`:138-146`（升级前探测）
- Test: `internal/agentd/machineupgrade_relay_test.go`

**Interfaces:**
- Consumes: Task 7 的 `s.pool`
- Produces: 无新接口

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/machineupgrade_relay_test.go`：

```go
// 本文件锁死升级前探测对 relay 机器的行为。
//
// 注意边界：这里只罩「选路走对了」，不罩「推 tar.gz 大包能不能过 yamux」——
// 后者要真 relay 环境，列在 spec §6 的真机验收里。
package agentd

import (
	"context"
	"strings"
	"testing"
)

// TestMachineUpgradeProbeRelayNoHost：升级前探测不再对 relay 机器报 no Host。
func TestMachineUpgradeProbeRelayNoHost(t *testing.T) {
	s := relayOnlyServer(t) // 复用 Task 11 的助手
	c, err := s.pool.For("linux-01")
	if err != nil {
		t.Fatalf("取客户端失败: %v", err)
	}
	_, statusErr := c.Status(context.Background())
	if statusErr == nil {
		t.Skip("拨到了真 relay，本用例只在拨不通时有意义")
	}
	if strings.Contains(statusErr.Error(), "no Host in request URL") {
		t.Fatalf("relay 机器不该报 no Host：%v", statusErr)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestMachineUpgradeProbeRelayNoHost -v`
Expected: 在 Task 7 之前会因 `s.pool` 不存在而编译失败；此时应 PASS（池已就位）——
**这条是护栏而非驱动**，真正驱动改动的是下面 Step 3/4 的编译错误与守卫测试（Task 13）。

- [ ] **Step 3: `executeMachineUpgrade` 改走池**

```go
func (s *Server) executeMachineUpgrade(ctx context.Context, m upgrade.Machine, target config.Target,
	rel release.Release, force bool, progress func(string)) upgrade.Result {
	if s.machineUpgradeInstaller == nil {
		return upgrade.Result{Verdict: upgrade.VerdictNeedsUpgrade, Status: upgrade.StatusFail,
			Reason: "远端升级下载器未就绪"}
	}
	// 选路走池：relay 机器没有 addr，直连构造会造出没有 Host 的 URL。
	// 注意 tar.gz 大包经 relay 走的是 yamux 流——这条路径没有先例，
	// 见 spec §6 的真机验收项。
	peer, err := s.pool.For(m.Name)
	if err != nil {
		s.log.Error("执行机升级：取客户端失败", "name", m.Name, "cause", err)
		return upgrade.Result{Verdict: upgrade.VerdictUnreachable, Status: upgrade.StatusFail,
			Reason: err.Error()}
	}
	return upgrade.RemoteOne(ctx, s.log, m, peer,
		s.machineUpgradeInstaller, rel, upgrade.Options{Force: force}, progress)
}
```

（`target` 参数若因此不再使用，保留签名——它是 `machineUpgradeRunner` 的缝，测试在替换它；用 `_ = target` 会掩盖意图，改为在文档注释里说明「target 仅为缝的签名保留，选路已收进池」。）

- [ ] **Step 4: 升级前探测改走池**

把 `client.New(target.Addr, target.Token).Status(probeCtx)` 换成：

```go
	peer, err := s.pool.For(name)
	if err != nil {
		s.log.Error("执行机升级：取客户端失败", "name", name, "cause", err)
		result := upgrade.Result{Verdict: upgrade.VerdictUnreachable, Status: upgrade.StatusFail,
			Reason: err.Error()}
		s.writeMachineUpgradeResult(w, http.StatusBadGateway,
			projectUpgradeMachine(name, nil, err), result, false)
		return
	}
	s.log.Info("开始探测执行机升级目标", "name", name, "relay", target.IsRelay())
	status, err := peer.Status(probeCtx)
```

（原日志里的 `"addr", target.Addr` 换成 `"relay", target.IsRelay()`：relay 机器的 addr 恒空。）

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestMachineUpgrade' -v`
Expected: PASS（既有的升级测试必须全绿——它们用的是直连 httptest 地址）

- [ ] **Step 6: 跑 agentd 包回归**

Run: `go test ./internal/agentd/`
Expected: PASS

- [ ] **Step 7: 加关键节点日志**

核对：两处「取客户端失败」都是 Error（升级是用户主动发起的操作，失败必须显眼）、带 name 与 cause；探测开始的 Info 带 relay 位。

- [ ] **Step 8: 加注释**

确认 `executeMachineUpgrade` 的注释写明了「tar.gz 经 relay 走 yamux 没有先例，见 spec §6」——这是本次唯一没被自动化测试罩住的路径，注释是它在代码里的唯一痕迹。

- [ ] **Step 9: gofmt 与提交**

```bash
gofmt -l internal/agentd/ && go test ./internal/agentd/
git add internal/agentd/
git commit -m "$(cat <<'EOF'
fix(agentd): 执行机升级改走 target 池

升级前探测与推包都不再直连构造。tar.gz 经 relay 走 yamux 流没有先例，
已在代码注释与 spec §6 里标为必须真机验收。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: 守卫——agentd 包内不许再直连构造

**Files:**
- Create: `internal/agentd/nodirectclient_test.go`

**Interfaces:**
- Consumes: 无（纯源码扫描）
- Produces: 无

- [ ] **Step 1: 写测试**

```go
// 本文件是一道机械守卫：internal/agentd 内不许再出现 client.New( 直连构造。
//
// 为什么需要它：relay 机器在控制台一律显示「已断开」这个 bug，**不会让任何
// 既有测试变红**——直连机器一切正常。下一个人新增第七处扇出时，照样可能顺手
// 写 client.New(t.Addr, t.Token)，而且照样一路绿灯合进去。
//
// 边界：这是字符串扫描，不是类型检查。它只回答「有没有人绕过池」，不回答
// 「走池的用法对不对」——后者由各调用点自己的用例负责。
package agentd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowDirectClientNew 是白名单：这些文件允许出现 client.New(。
//
// 目前为空——agentd 包内没有任何一处该直连构造。加白名单前先问一句：
// 这个调用点为什么不能走池？
var allowDirectClientNew = map[string]bool{}

// TestNoDirectClientNewInAgentd 扫描本包源码，发现直连构造即失败。
func TestNoDirectClientNewInAgentd(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") || allowDirectClientNew[name] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "client.New(") {
				t.Errorf("%s:%d 直连构造了 agentd 客户端：%s\n"+
					"agentd 侧一律走 s.pool.For(name)（internal/targetclient）。"+
					"直连构造对 relay 形态的机器恒失败——它们没有 addr，"+
					"client.New(\"\") 会退化成一个没有 Host 的 URL。",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
```

- [ ] **Step 2: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestNoDirectClientNewInAgentd -v`
Expected: PASS（Task 8-12 已清干净全部六处）

- [ ] **Step 3: 验证守卫真的有牙齿**

这一步是**决定性实验**，不能跳：临时在 `internal/agentd/machines.go` 里加一行
`var _ = client.New` 改成真实调用形态 `var _ = client.New("x", "y")`，重跑守卫，
确认它变红，然后撤销。

Run: `go test ./internal/agentd/ -run TestNoDirectClientNewInAgentd -v`
Expected: 先 FAIL（点名 machines.go 与行号），撤销后 PASS

- [ ] **Step 4: 加注释**

确认文件头写了「为什么需要它」与「它不罩什么」，白名单变量上写了「加白名单前先问一句」。

- [ ] **Step 5: gofmt 与提交**

```bash
gofmt -l internal/agentd/ && go test ./internal/agentd/
git add internal/agentd/nodirectclient_test.go
git commit -m "$(cat <<'EOF'
test(agentd): 加守卫，禁止包内直连构造 agentd 客户端

relay 显示问题不会让任何既有测试变红——直连机器一切正常。这道扫描守的是
「没人绕过池」，新增第七处扇出时会当场变红。已用装饰性变异验证它有牙齿。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: 控制台显示「中继 · <node>」

**Files:**
- Create: `web/src/app/machines/machineEndpoint.ts`
- Create: `web/src/app/machines/machineEndpoint.test.ts`
- Modify: `web/src/api/types.ts:191-205`（`Machine` 接口）
- Modify: `web/src/app/machines/MachineDetail.tsx:32`
- Modify: `web/src/app/machines/MachinesPage.tsx:376`

**Interfaces:**
- Consumes: Task 8 的 `Machine.relay`
- Produces: `export function machineEndpoint(machine: Pick<Machine, 'addr' | 'relay'>): string`

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/machines/machineEndpoint.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { machineEndpoint } from './machineEndpoint'

describe('machineEndpoint', () => {
  it('直连机器显示地址', () => {
    expect(machineEndpoint({ addr: '100.73.238.21:7777', relay: '' })).toBe('100.73.238.21:7777')
  })

  it('中继机器显示节点名', () => {
    expect(machineEndpoint({ addr: '', relay: 'linux-01' })).toBe('中继 · linux-01')
  })

  // 两者都有时以 addr 为准：addr 与 relay 在配置层互斥，真出现说明配置有问题，
  // 显示直连地址至少是可验证的那一个。
  it('两者都有时以地址为准', () => {
    expect(machineEndpoint({ addr: '10.0.0.1:7777', relay: 'x' })).toBe('10.0.0.1:7777')
  })

  // 本机的 addr 是 listen 地址，relay 恒空；两者皆空只发生在数据异常时，
  // 返回空串让调用方的 truncate 容器自然收成 0 高，不要塞占位符。
  it('两者都空时返回空串', () => {
    expect(machineEndpoint({ addr: '', relay: '' })).toBe('')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/machines/machineEndpoint.test.ts`
Expected: FAIL —— 模块不存在

- [ ] **Step 3: 加 TS 类型字段**

`web/src/api/types.ts` 的 `Machine` 接口，`addr` 之后加：

```ts
  // relay 节点名；空=直连形态。与 addr 互斥（配置层保证）。
  relay?: string
```

`AddMachineReq` **不动**——控制台目前只能新增直连机器。

- [ ] **Step 4: 写实现**

创建 `web/src/app/machines/machineEndpoint.ts`：

```ts
// 机器卡片上那行端点文案的唯一来源。
//
// 职责：把 Machine 的 addr / relay 两个互斥字段折成一行可展示文本
// 边界：只管展示，不判断可达性——「已断开」由 machine.reachable 单独渲染
//
// 为什么需要它：relay 形态的机器 addr 恒为空（配置层强制互斥），卡片上会一个
// 身份标识都没有——列表里两台中继机器长得完全一样。
export function machineEndpoint(machine: { addr: string; relay?: string }): string {
  if (machine.addr) return machine.addr
  if (machine.relay) return `中继 · ${machine.relay}`
  return ''
}
```

- [ ] **Step 5: 两处渲染改用它**

`web/src/app/machines/MachineDetail.tsx:32`：

```tsx
      <p className="mt-0.5 font-mono text-xs text-muted-foreground">{machineEndpoint(machine)}</p>
```

`web/src/app/machines/MachinesPage.tsx:376`：

```tsx
        <div className="truncate font-mono text-xs text-muted-foreground">{machineEndpoint(machine)}</div>
```

两个文件都 `import { machineEndpoint } from './machineEndpoint'`。

- [ ] **Step 6: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/machines/`
Expected: 全部 PASS

- [ ] **Step 7: 跑前端全量与类型检查**

Run: `cd web && npx tsc --noEmit && npx vitest run`
Expected: PASS

- [ ] **Step 8: 加注释**

确认 `machineEndpoint.ts` 有文件头（职责 + 边界 + 为什么），`types.ts` 的 `relay` 字段有注释。前端不加运行时日志——这是纯展示函数，无分支副作用。

- [ ] **Step 9: 提交**

```bash
git add web/src/
git commit -m "$(cat <<'EOF'
feat(web): 中继机器的卡片显示「中继 · <node>」

relay 形态的机器 addr 恒为空，卡片上过去一个身份标识都没有——列表里两台
中继机器长得完全一样。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: 全量回归与真机验收清单

**Files:**
- Modify: 无（只跑与记录）

**Interfaces:**
- Consumes: Task 1-14 全部
- Produces: 一份可交给审核者的验收结论

- [ ] **Step 1: 全量测试**

Run: `go build ./... && go test ./... && gofmt -l .`
Expected: 测试全绿，`gofmt -l` 输出为空

- [ ] **Step 2: 竞态检测**

Run: `go test ./internal/targetclient/ ./internal/agentd/ -race`
Expected: PASS，无 race 报告

- [ ] **Step 3: 前端全量**

Run: `cd web && npx tsc --noEmit && npx vitest run && npm run build`
Expected: 全绿

- [ ] **Step 4: 写下真机验收清单（不执行）**

把 spec §6 的六项照抄进本次的收尾报告，逐项标注状态。**第 6 项（agentd 冷启动
后首轮列表不出现「已断开」闪烁）与任何需要起停 agentd 的验证，由审核者本地
执行，不派发**——派发的纪律块禁止执行者驱动 handoff CLI 与起 agentd 进程。

清单：

1. 控制台机器列表：linux-01 显示已连接、版本、执行者、项目目录数非 0
2. 卡片显示「中继 · linux-01」
3. relay 机器上派一个任务，事件在控制台实时可见（镜像走通）
4. 项目树、PTY 会话列表能拉到 linux-01 的内容
5. **relay 机器的远程升级**（推包走 yamux）——本次唯一没有先例的路径
6. agentd 冷启动后首轮列表不出现「已断开」闪烁 ←（审核者本地执行）

- [ ] **Step 5: 报告**

如实写明：哪几项通过、哪几项未验证、未验证的原因。**没跑到结果不许写结论。**

---

## 附：给执行者的两条提醒

1. **Task 5 Step 5 会回头改 Task 3 的 `New`**。这是刻意的：先让 `New` 以最简形态通过它自己的测试，等池真的需要 Dialer 时再拆出 `newWithDialer`。不要为了「一次写对」在 Task 3 就引入 Dialer 返回值——那会让 Task 3 的测试为一个还没有消费者的接口服务。

2. **Task 12 Step 2 的「期望」是 PASS 而不是 FAIL**，这是刻意的：那条用例是护栏，真正驱动 Task 12 改动的是编译错误与 Task 13 的守卫。不要因为它一开始就绿而删掉它。
