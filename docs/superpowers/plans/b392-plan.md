# B392 实现计划：mobile bind 生产接线 SessionCookie / SwitchMachine（去掉 notWiredSessions）

读者：对代码库零上下文的执行者。级别 **L3 轻档**（spec §2.3；breakdown 定级）。
上游：spec `docs/superpowers/specs/b392.md`、contract `docs/superpowers/specs/b392-contract.md`、breakdown `docs/superpowers/specs/b392-breakdown.md`（均在本分支内，`cards/B392-spec @400ee3c4` 为合并目标）。
工作分支：`cards/B392-charter-4`。**只在本分支工作，不切分支、不改 git 配置、不 push。**
本节点台账：`docs/superpowers/ledgers/2026-09-24-b392-plan-ledger.md`。

> **生产代码已在 Ticket 0 落完**（contract §7.1）：`mobile/bind/adapter.go`（`coreSessions` 适配器）、`mobile/bind/bind.go`（`liveCore` 单实例）、`mobile/bind/session.go`（`sessions = newCoreSessions(liveCore)`，占位已删）均已在 HEAD。本实现轮**只加测试与交接文档**，禁止再改 `mobile/bind/*.go` 生产文件与任何 `internal/**`、`cmd/**`、`web/**`、`codegraph/**`。

---

## 0. 基线复核（动手前本节点已跑，原始输出入台账）

命令与读数（本工作树 `go1.26.1 linux/amd64`；`mobile/` 是独立嵌套 module，需 `cd mobile`）：

```text
go build ./... (root)                 → exit 0
go list ./... | grep -c handoff/mobile          → 0
go list -deps ./... | grep -c handoff/mobile    → 0
(cd mobile) go build ./...            → exit 0
(cd mobile) go test ./... -count=1    → ok github.com/Xsxdot/handoff/mobile 0.378s
                                        ok github.com/Xsxdot/handoff/mobile/bind 0.006s
(cd mobile) go test ./bind/ -race -count=1 → ok github.com/Xsxdot/handoff/mobile/bind 1.017s
(cd mobile) go vet ./...              → exit 0（无输出）
(cd mobile) gofmt -l .                → 无输出
```

- 现状守卫在场：`mobile/bind/adapter_test.go`（顺序/失败闭合/错机/锁/`TestDefaultRuntimeSharesOneCore`）、`mobile/bind/export_surface_test.go`（七函数逐字冻结、生产 import 禁令）、`mobile/bind/gobind_surface_test.go`（真 gobind 产物，缺 `gobind` 时 skip）。
- 现状欠账（必须在本轮补齐，contract §8.1–8.4）：真实 Core 竖切、生产守卫与四变异红、`mobile/README.md` 调用顺序。gobind 真产物与 AAR/XCFramework 属真机，归协调者（§7）。

### 0.1 生产守卫落法裁决（照 breakdown P3=A + 协调者口径，不重裁）

spec §8.3 字面要求「不调用 `swapCore`/`swapSessions`、从默认生产组合直走完整链」。breakdown P3 甲与协调者 2026-09-24 口径已把这件拆成两半，本轮照此实现：

1. **默认生产身份守卫**（不 swap）＝既有 `mobile/bind/adapter_test.go` 的 `TestDefaultRuntimeSharesOneCore`：断言导出面 `core` 与 `sessions` 指向**同一个** `*mobilecore.Core`（`liveCore`），`sessions` 的具体类型是 `*coreSessions`。生产若恢复占位或另起第二个 Core，这支测试红。
2. **真实行为竖切**（独立 Core，swap 注入导出面）＝本轮新增 `realcore_test.go`：自建独立 `mobilecore.New(dial, log)` + 本地 fake agentd，经 `swapCore`/`swapSessions` 注入导出面后调 `Pair`/`SwitchMachine`/`SessionCookie`/`Close`，走完整真实链。swap 只为隔离，不碰 `liveCore`。

二者合起来满足 §8.3 的意图（生产不是占位 / 真实链能跑），且不污染包级 `liveCore`（P3 甲明确弃「在 `liveCore` 上配对再收尾」的次序脆弱方案）。实现者按此写，**不要**在 `liveCore` 上配对。

---

## 1. 图覆盖债（本节点亲跑，非阻断）

- `codegraph --repo . sym coreSessions` / `sym newCoreSessions` / `sym Core.Activate` / `sym Core.Pair` / `sym agentd.sessionCookie` → 全部「不在图中」（退出 1）。原因：`mobile/` 前缀被扫描配方排除（嵌套 module，图外）；`Core.*` 是 S2 新符号，未进旧 baseline。spec §11 / contract §3 已记，**本卡不偷带全图重扫**，回落精确 `file:line`。
- `codegraph --repo . check` → 退出 0（`fails=[]`）。
- `codegraph --repo . validate` → 退出 1，**2 个既存问题**（`cards-B272-charter` 的 `k_dropdir_fn` 引用不存在领域；`cards-B374-charter` 的 `k_collab_model` 重复），均非本卡（本卡无视图）。
- `codegraph --repo . resolve --doc docs/superpowers/specs/b392-contract.md` → 退出 0，两锚 `n_client_Client_IssueAuthTicket`、`n_agentd_sessionCookie` 均 `ok`。
- 本卡不新增根模块符号（只在图外 `mobile/bind` 加 `_test.go` 与 `README.md`）→ `target.json`/`best.json` 零改、无视图 diff。

---

## 2. 测试范围声明（最小化）

- **Task 1**：`cd mobile && go test ./bind/ -run 'TestRealCoreSwitchAndCookieOwnership' -count=1`
- **Task 2**：`cd mobile && go test ./bind/ -run 'TestRealCoreFailsClosed|TestRealCoreConcurrentNoCrossMachine' -count=1`
- **Task 3**：`cd mobile && go test ./bind/ -run 'TestBindSessionNotPairedFailsClosed' -count=1`
- **Task 4**：`cd mobile && go test ./bind/ -run 'TestReadmeDocumentsShellCallOrder' -count=1`
- **Task 5**：变异命令逐条见该 task 步骤（各跑单支测试）。
- 每个 task 收尾：`cd mobile && go build ./...`。
- **全量测试不属任何单 task**：`cd mobile && go test ./... -count=1 && go test ./bind/ -race -count=1` 与根模块全量属收口/acceptance（见 §6），禁止塞进单 task。

---

## 3. 任务依赖 / DAG

```text
Task 1（realcore_test.go：夹具 + 归属/缓存竖切，红绿）  ← 最薄路径条
   └─> Task 2（同文件：失败闭合 + -race 并发）
Task 3（bind_test.go：旧空态测试隔离收口）   独立
Task 4（README + readme_test.go 文档面）     独立
Task 5（四变异复验，依赖 1/2/3 的测试在场）
```

**最薄路径条判定**：本卡要锁的行为（真实 `*mobilecore.Core` 穿过导出面返回目标机 cookie/origin）今天**已可达**——Ticket 0 已把 `sessions` 接到 `newCoreSessions(liveCore)`，适配器与 Core 均在位。故新测试写下去即应绿，不另设「点亮行为」的最薄路径 task；Task 1 排第一并覆盖该真实行为。

---

## Task 1：真实 Core 竖切夹具与 A→B→A 归属（`mobile/bind/realcore_test.go`，红绿）

**文件**：`mobile/bind/realcore_test.go`（新建，`_test.go`）。**不改任何生产 `.go`。**

**Interfaces**
- **Consumes**：
  - `mobilecore.New(dial DialFunc, log *slog.Logger) *Core`（`internal/mobilecore/core.go:107`）
  - `mobilecore.DialFunc` = `func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error)`（`core.go:35`）
  - `*mobilecore.Core` 的 `Pair`（`core.go:125`）、`Activate`（`:219`）、`Session`（`:266`）、`ActiveMachine`（`:279`）、`Origin`（`:333`）、`MachineNames`（`:350`）、`Close`（`:361`）
  - `client.New(addr, token string) *Client`（`internal/client/client.go:195`）
  - `proto.PairBundle`/`PairMachine`/`PairVersion`/`EncodePairBundle`（`internal/proto/pairing.go:24/40/58/111`）
  - `proto.AuthTicketResp{URL string; ExpiresAt time.Time}`（`internal/proto/auth.go:16`）
  - 测试缝：`swapCore`/`swapSessions`（`mobile/bind/fakes_test.go:103/109`）、`newCoreSessions`（`mobile/bind/adapter.go:35`）
  - 导出面：`Pair`/`SwitchMachine`/`SessionCookie`/`Close`（`mobile/bind/bind.go:47`、`session.go:24/37`、`bind.go:95`）
- **Produces**（包内 `_test.go`，供 Task 2/5 用）：
  - `type fakeAgentd struct{ machine string; mu sync.Mutex; tickets int; exchanges int; lastValue string; omitCookie bool; server *httptest.Server }`
  - `func newFakeAgentd(machine string) *fakeAgentd`
  - `func (f *fakeAgentd) setOmitCookie(v bool)`
  - `func (f *fakeAgentd) stats() (tickets, exchanges int, lastValue string)`
  - `func (f *fakeAgentd) close()`
  - `type realChain struct{ core *mobilecore.Core; fixtures map[string]*fakeAgentd }`
  - `func newRealChain(t *testing.T, machines ...string) *realChain`
  - `func (rc *realChain) pair(t *testing.T, names ...string)`
  - 测试 `TestRealCoreSwitchAndCookieOwnership`

### 步骤

1. **写新文件头注释 + 夹具**。新建 `mobile/bind/realcore_test.go`，完整内容：

```go
// B392 真实 Core 竖切：用真实 *mobilecore.Core + 本地 fake agentd 夹具穿过绑定
// 导出面（Pair/SwitchMachine/SessionCookie/Close），证明生产会话链不是 fake 假绿。
//
// 边界：本文件只在测试内构造独立真实 Core，经 swapCore/swapSessions 注入导出面
// （breakdown P3=A：独立 Core 只用于行为竖切，不碰包级 liveCore；生产身份守卫另由
// mobile/bind/adapter_test.go 的 TestDefaultRuntimeSharesOneCore 承担）。网络对端可 fake，Core 与
// 适配器是真的；夹具按机器/ticket 用真 Set-Cookie 签发可区分的 handoff_session，
// 不预置与响应无关的假值。
//
// 禁止：本文件不得出现生产代码改动；只 import 测试允许的 internal/client、internal/proto。
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
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/mobilecore"
	"github.com/Xsxdot/handoff/internal/proto"
)

// fakeAgentd 是 B392 真实 Core 竖切用的对端夹具（独立实现，不复用
// internal/mobilecore 同包未导出 helper）：覆盖 /api/status（可达性探测）、
// /api/auth/tickets（签发一次性 ticket）、/console（302 + Set-Cookie）。
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
		f.mu.Unlock()
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
	f.server = httptest.NewServer(mux)
	return f
}

// setOmitCookie 让 /console 只回 302 不签 Set-Cookie（兑换失败反例）。
func (f *fakeAgentd) setOmitCookie(v bool) {
	f.mu.Lock()
	f.omitCookie = v
	f.mu.Unlock()
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

// TestRealCoreSwitchAndCookieOwnership 是 B392 §8.2 的真实 Core 竖切 1–5：
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
```

2. **跑绿**：
   ```
   cd mobile && go test ./bind/ -run 'TestRealCoreSwitchAndCookieOwnership' -count=1
   cd mobile && go build ./...
   ```
   预期 `ok github.com/Xsxdot/handoff/mobile/bind`。若红，先判红因：夹具路由/HTTPServer 地址错（改夹具）与实际适配器/Core 缺陷（**记台账报协调者，不得改生产 `.go`**）分开。

3. **关键节点日志核对（无可观测性新增，但要复核）**：本 task 零生产代码改动，故**不新增日志**；既有日志已覆盖入口/成功/失败——绑定面 `session.go:27/31/38/41/44`、核侧 `core.go:261`、`session.go:70/95/113`。测试断言失败信息已带 got/want 上下文。**这一条是复核已满足，不是跳过。**

4. **注释核对**：新文件头已写职责+边界（步骤 1 已含）；每个导出测试 helper（`newFakeAgentd`/`newRealChain`/`pair`）已带作用与边界的注释。若实现中新增 helper，同样加注释。

**不得触碰**：`mobile/bind/adapter.go`、`bind.go`、`session.go`、`types.go`、`export_surface_test.go`、`gobind_surface_test.go`、`fakes_test.go`、`adapter_test.go`、`internal/**`。

---

## Task 2：失败闭合与 `-race` 并发（同文件续写）

**文件**：`mobile/bind/realcore_test.go`（续加两个测试函数）。

**Interfaces**
- **Consumes**：Task 1 的 `newRealChain`/`realChain.pair`/`realChain.fixtures`/`fakeAgentd.setOmitCookie`；导出面 `SwitchMachine`/`SessionCookie`/`Close`。
- **Produces**：`TestRealCoreFailsClosed`、`TestRealCoreConcurrentNoCrossMachine`。

### 步骤

1. 在 `mobile/bind/realcore_test.go` 末尾追加：

```go
// TestRealCoreFailsClosed 锁 B392 §8.2-6/7：未配对、离线、兑换无 cookie、
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

// TestRealCoreConcurrentNoCrossMachine 锁 B392 §8.2-8：并发 SwitchMachine /
// SessionCookie 在 -race 下通过，且绝不把 B 的 cookie 返回给 A。
// 读成功时 cookie 值必带本机标记 "-<machine>-"（A/B 值可区分）。
func TestRealCoreConcurrentNoCrossMachine(t *testing.T) {
	rc := newRealChain(t, "A", "B")
	rc.pair(t, "A", "B")
	if _, err := SwitchMachine("A"); err != nil {
		t.Fatalf("前置切机 A: %v", err)
	}
	if _, err := SwitchMachine("B"); err != nil {
		t.Fatalf("前置切机 B: %v", err)
	}

	const workers, iters = 8, 20
	errCh := make(chan string, workers*iters)
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
				if !strings.Contains(v, "-"+m+"-") {
					errCh <- fmt.Sprintf("SessionCookie(%s) 返回了错机 cookie: %q", m, v)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}
```

2. **跑绿**：
   ```
   cd mobile && go test ./bind/ -run 'TestRealCoreFailsClosed|TestRealCoreConcurrentNoCrossMachine' -count=1
   cd mobile && go test ./bind/ -run 'TestRealCoreConcurrentNoCrossMachine' -race -count=1
   cd mobile && go build ./...
   ```
   预期两支 `ok`，`-race` 无 `DATA RACE`。

3. **关键节点日志核对**：同 Task 1 步骤 3（零生产改动、既有日志覆盖），测试失败信息带 machine/value 上下文。

4. **注释核对**：两个测试函数头已写「锁什么、为什么」，并发测试注明 marker 语义与合法失败分支。

---

## Task 3：旧空态测试隔离收口（`mobile/bind/bind_test.go`）

**背景（P3=A）**：现状 `bind_test.go:89 TestBindSessionFailIsClosed` 直接用默认 `sessions`（包着 `liveCore`），靠「共享 `liveCore` 无任何机器」才通过；注释仍写「S2 未接线时」已过期。一旦有测试在 `liveCore` 上配对成功就次序耦合、结果漂移。改为**显式注入一个空真实 Core**，语义=「未配对即失败」，不依赖全局空态。

**文件**：`mobile/bind/bind_test.go`（只改测试）。

**Interfaces**
- **Consumes**：`mobilecore.New`（`core.go:107`）、`newCoreSessions`（`adapter.go:35`）、`swapSessions`（`fakes_test.go:109`）、包级 `log`（`bind.go:35`）、导出面 `SessionCookie`/`SwitchMachine`。
- **Produces**：`TestBindSessionNotPairedFailsClosed`（替换 `TestBindSessionFailIsClosed`）。

### 步骤

1. import 块（`bind_test.go:3-6`）加：
```go
	"github.com/Xsxdot/handoff/internal/mobilecore"
```
（现状只 import `reflect`、`testing`。）

2. 把 `bind_test.go:89-97` 整段：

```go
// TestBindSessionFailIsClosed：S2 未接线时不得静默成功（空 cookie + error）。
func TestBindSessionFailIsClosed(t *testing.T) {
	if _, err := SessionCookie("devbox"); err == nil {
		t.Fatal("核侧会话入口未接线时必须返回错误，不得静默成功")
	}
	if _, err := SwitchMachine("devbox"); err == nil {
		t.Fatal("核侧会话入口未接线时切机必须返回错误")
	}
}
```

替换为：

```go
// TestBindSessionNotPairedFailsClosed：用显式注入的空真实 Core（无任何配对）
// 锁绑定导出面对未配对机器的失败闭合，不依赖包级 liveCore 的空态（B392 P3=A）。
func TestBindSessionNotPairedFailsClosed(t *testing.T) {
	empty := mobilecore.New(nil, log)
	defer empty.Close()
	defer swapSessions(newCoreSessions(empty))()

	if v, err := SessionCookie("devbox"); err == nil || v != "" {
		t.Fatalf("未配对取 cookie 必须 (\"\", err): value=%q err=%v", v, err)
	}
	if v, err := SwitchMachine("devbox"); err == nil || v != "" {
		t.Fatalf("未配对切机必须 (\"\", err): value=%q err=%v", v, err)
	}
}
```

3. **跑绿**：
   ```
   cd mobile && go test ./bind/ -run 'TestBindSessionNotPairedFailsClosed' -count=1
   cd mobile && go build ./...
   ```

4. **注释核对**：已在新测试头写清「为什么不依赖 liveCore」；无生产改动、无新日志。

---

## Task 4：交接文档 `mobile/README.md` + 文档面回归（红绿）

**文件**：`mobile/README.md`（新增小节）、`mobile/bind/readme_test.go`（新建，`_test.go`）。

**Interfaces**
- **Consumes**：`packageDir(t)`（`mobile/bind/gobind_surface_test.go:112`）。
- **Produces**：README 新增「切机与会话调用顺序（壳侧）」小节；`TestReadmeDocumentsShellCallOrder`。

### 步骤

1. **先写失败测试（红）**。新建 `mobile/bind/readme_test.go`：

```go
// readme_test.go 锁 B392 §9.8 交接文档：mobile/README.md 必须明写壳侧会话消费
// 顺序与失败不导航、以及「Go 只返回 cookie 值，不操作平台 cookie store」的职责边界。
package bind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadmeDocumentsShellCallOrder 断言 README 含 §4.4 调用顺序标记（按出现
// 位置有序）与 cookie 属性/失败语义。顺序标记缺失或倒序即红。
func TestReadmeDocumentsShellCallOrder(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(packageDir(t), "..", "README.md"))
	if err != nil {
		t.Fatalf("读 mobile/README.md: %v", err)
	}
	doc := string(raw)
	order := []string{
		"`SwitchMachine(target)`",
		"清除该 loopback host",
		"`SessionCookie(target)`",
		"写回 cookie jar",
		"导航到第 1 步返回的 origin",
	}
	pos := -1
	for _, marker := range order {
		idx := strings.Index(doc, marker)
		if idx < 0 {
			t.Fatalf("README 缺少调用顺序标记 %q", marker)
		}
		if idx < pos {
			t.Fatalf("README 调用顺序标记 %q 次序错误", marker)
		}
		pos = idx
	}
	for _, want := range []string{
		"不得导航",
		"不操作平台 cookie store",
		"Path=/",
		"HttpOnly=true",
		"SameSite=Lax",
		"Secure=false",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("README 缺少 %q", want)
		}
	}
}
```

2. **跑红**：
   ```
   cd mobile && go test ./bind/ -run 'TestReadmeDocumentsShellCallOrder' -count=1
   ```
   预期 FAIL：`README 缺少调用顺序标记 "`SwitchMachine(target)`"`。

3. **最小实现**：在 `mobile/README.md` 的「## 绑定面（壳的契约面）」小节结束处（现状第 38 行 `...逐字钉住上表签名。` 之后、「## 构建」之前）插入：

````markdown
## 切机与会话调用顺序（壳侧）

壳切换到一个目标机时，必须严格按以下顺序调用绑定面，任一步失败都**不得导航**：

1. `SwitchMachine(target)` —— 成功返回后核内已清旧会话并为目标机重兑换，壳拿到目标 loopback 源。
2. 清除该 loopback host 的全部旧 cookie：按名字 `handoff_session` 删除，**不区分端口**（cookie 不按端口隔离，须覆盖该 host 的所有端口）。
3. `SessionCookie(target)` —— 返回目标机当前有效会话的 cookie 值。
4. 把返回值**写回 cookie jar**：host-only `handoff_session`，`Path=/`、`HttpOnly=true`、`SameSite=Lax`、`Secure=false`，不设持久 `Max-Age/Expires`（进程内会话 cookie；服务端到期仍是最终权威）。
5. **导航到第 1 步返回的 origin**。

任一步失败（切机失败、清 jar 失败、取 cookie 失败、写入失败）都不得导航；清 jar / 注入失败由壳呈现可行动错误，可重试。**Go 绑定层只返回 cookie 值，不操作平台 cookie store**——`WKHTTPCookieStore` / `CookieManager` 的写入是壳的职责；绑定面不返回 token / origin 冒充 cookie。
````

4. **跑绿**：
   ```
   cd mobile && go test ./bind/ -run 'TestReadmeDocumentsShellCallOrder' -count=1
   cd mobile && go build ./...
   ```

5. **注释核对**：新测试文件头已写职责；README 为文档面，无需代码日志。

---

## Task 5：四变异复验（口径可红，原始红输出入台账，改动后立即还原）

**文件**：临时改 `mobile/bind/session.go` / `mobile/bind/adapter.go`，复跑后**逐条还原**（用 `$TMPDIR` 备份，禁用仓内临时路径）。不产生提交。

> 判据：每条变异**必须**让指定测试红。若某条不变红，说明对应守卫缺失——**停下来报协调者，不得改测试放过**。

### 步骤

1. **变异①：生产恢复占位（守卫红）**
   - 备份 `mobile/bind/session.go` 到 `$TMPDIR`。临时把 `session.go:20`
     `var sessions sessionAPI = newCoreSessions(liveCore)`
     改成 `var sessions sessionAPI = notWiredSessions{}`，并在同文件临时追加：
     ```go
     type notWiredSessions struct{}
     func (notWiredSessions) SessionCookie(string) (string, error) { return "", errSessionsNotWired }
     func (notWiredSessions) SwitchMachine(string) (string, error) { return "", errSessionsNotWired }
     var errSessionsNotWired = errors.New("会话未接线")
     ```
     （import 块临时加 `"errors"`。）
   - 跑：`cd mobile && go test ./bind/ -run 'TestDefaultRuntimeSharesOneCore' -count=1`
   - 预期 FAIL（类型断言不是 `*coreSessions`：`会话面生产装配不是 coreSessions: bind.notWiredSessions`）。**保存原始输出**。
   - 还原 `session.go`（`diff` 应空）。

2. **变异②：去掉活动机检查（串机红）**
   - 备份 `mobile/bind/adapter.go`。把 `adapter.go:42`
     `if a.core.ActiveMachine() != machine {` 改成 `if false && a.core.ActiveMachine() != machine {`。
   - 跑：`cd mobile && go test ./bind/ -run 'TestCoreSessionsSessionCookieRejectsWrongMachine' -count=1`
     （double 层确定性红）与 `cd mobile && go test ./bind/ -run 'TestRealCoreSwitchAndCookieOwnership' -count=1`（真实 Core 层红）。
   - 预期两支 FAIL（`错机必须返回 ("", err): value="sess-B" err=<nil>` / A 取到 B 值）。**保存原始输出**。还原。

3. **变异③：失败路径改 `return "", nil`（失败语义红）**
   - 备份 `mobile/bind/adapter.go`。把 `adapter.go:62-64`
     ```go
     if err != nil {
         return "", err
     }
     ```
     （`SwitchMachine` 中 `Activate` 之后那段）改成 `return "", nil`（保留后续 `if ck.Value == ""` 判断不达）。
   - 跑：`cd mobile && go test ./bind/ -run 'TestCoreSessionsSwitchMachineFailsClosed' -count=1` 与
     `cd mobile && go test ./bind/ -run 'TestRealCoreFailsClosed' -count=1`。
   - 预期两支 FAIL（`Activate 失败必须返回 ("", err): origin="" err=<nil>` 等）。**保存原始输出**。还原。

4. **变异④：去掉适配器互斥（并发红）**
   - 备份 `mobile/bind/adapter.go`。删掉 `SessionCookie` 与 `SwitchMachine` 各自的
     `a.mu.Lock()` 与 `defer a.mu.Unlock()`（`adapter.go:40-41`、`59-60`）。
   - 跑：`cd mobile && go test ./bind/ -run 'TestCoreSessionsLockSerializesSwitchAndRead' -count=1`
     （`TryLock` 握手确定性红）与
     `cd mobile && go test ./bind/ -race -run 'TestRealCoreConcurrentNoCrossMachine' -count=1`（附加闸）。
   - 预期 `TestCoreSessionsLockSerializesSwitchAndRead` FAIL（`Session 阻塞期间适配器锁未被持有`）。**保存原始输出**。还原。

5. **还原核验**：四条全部还原后跑 `cd mobile && go test ./... -count=1` 全绿；`git status --short` 只应有本节点产出物（`docs/superpowers/plans/b392-plan.md`、`docs/superpowers/ledgers/2026-09-24-b392-plan-ledger.md`），生产 `.go` 与测试文件**零改**。

---

## 4. 五项检查（实现类 plan 出稿自审）

### 4.1 缺陷族对抗审查

| 族 | 设问 | 结论 |
|---|---|---|
| 生命周期 / 状态机中断 | 适配器 `mu` 跨 Core 网络兑换（最长 `exchangeTimeout=5s`，`internal/mobilecore/session.go:35`）；`Close` 与 `Activate` 并发 | 契约冻结语义，非缺陷；`Activate` 兑换后重取 `c.mu` 检查 `closed`（`core.go:252-256`），`Session`/`Origin` 同样先查（`:269/:336`）。`TestRealCoreFailsClosed/Close 后` 锁导出面层拒绝；夹具随 `t.Cleanup` 关停，无孤儿端口/goroutine |
| 静默失败 / 误导报错 | 有没有 `("", nil)`？测试自身会不会假绿？ | 适配器每条失败路径 `return "", err`（`adapter.go:42-51/62-71`）；本卡断言逐条断 `err==nil || v!=""` 即红。夹具 cookie 值来自真 `Set-Cookie`，非预置假值（spec §8.2 末） |
| 跨平台假设 | 夹具/真 Core 是否引入平台相关面 | 测试只用 `net/http/httptest` 与 `sync`，平台中立。平台 cookie jar 属真机（§4.4/§7） |
| 假红 / 假绿测试 | 判据钉行为还是计数？有无反面断言？ | 判据全为行为断言（归属、错机拒绝、缓存不重兑换、失败闭合、并发不串机），无计数型判据。变异四条各自可红（Task 5）。同机缓存用「tickets/exchanges 不增 + 值不变」双断言，非仅计数 |
| 门禁绕过 | 是否放松回环门禁 / 绕过 cookie 闸 | 零生产改动，`export_surface_test.go` 的七函数与无 Token/Dial 断言继续绿；`TestBindProductionHasNoProtocolLogic` 继续绿（新 `_test.go` 不在其扫描范围，生产 import 边界未变） |
| 序列化边界 | 新字段/手写投影？ | 无新字段。既有链 `Core.exchangeTicket` → `toSessionCookie` → 适配器只取 `.Value`（`adapter.go:52`）→ 导出 string。`TestRealCoreSwitchAndCookieOwnership` 断言 `SessionCookie` 逐字等于夹具 `Set-Cookie.Value`，穿真实 HTTP 序列化边界（详见 4.2） |
| 枚举新值过白名单 | 新枚举？ | 无新枚举值（cookie 名/`SameSite=Lax`/DTO 字段均不变） |
| 承重安全属性有测试锁住 | 「一罐只装一机/不串机」「Close 后拒绝」有能变红的测试吗？ | 有：`TestRealCoreSwitchAndCookieOwnership`（错机拒绝、A/B 值可区分）+ `TestRealCoreConcurrentNoCrossMachine`（`-race`）+ `TestRealCoreFailsClosed`（Close 后）；变异②③④在 Task 5 实测打红 |

### 4.2 序列化边界设问（逐处列全）

`handoff_session` 的**值**从产生到消费的唯一链路，本卡新增断言覆盖最后一段：

1. 夹具（测试对端）`/console` 构造 `http.Cookie{Name:"handoff_session", Value:"sess-"+ticket}` 走 `Set-Cookie` 响应头 —— **真 wire 序列化**。
2. `internal/mobilecore.exchangeTicket` 解析 `resp.Cookies()` 选中 `handoff_session`、非空校验 → `toSessionCookie` 投影 `SessionCookie`（`internal/mobilecore/session.go:103-135`）——既有 Core 测试覆盖，本卡不重测。
3. 适配器 `coreSessions.SessionCookie` 只取 `.Value` 返回 string（`adapter.go:45-52`）——**本卡新增真实链断言**：`TestRealCoreSwitchAndCookieOwnership` 断言 `SessionCookie(machine)` 逐字等于夹具 `stats().lastValue`，且 A/B 值不同、切机后旧机失败。
4. gomobile 映射为 Java/Kotlin `String`/ObjC `NSString` —— 形状由 `gobind_surface_test.go` 钉（真产物归真机/CI）。
5. 壳写平台 cookie jar（属性 `Path/HttpOnly/SameSite/Secure` 由壳硬编码，不由 Go 返回）——真机接缝（§4.4/§7）。

**可空类型区分「字段缺失 vs 值为零」**：本卡不新增可空字段；Core 对「无 `handoff_session`」与「值为空」分别报错（`internal/mobilecore/session.go:107-118`），适配器再对 `.Value==""` 兜底报错（`adapter.go:49/65`），不塌成零值。夹具 `omitCookie=true` 制造「缺 cookie」路径并由 `TestRealCoreFailsClosed/兑换无 cookie` 断言 error。无 roundtrip 属性测试需求（无新编码格式）。

### 4.3 上下文预算检查

有界文件集：`mobile/bind/realcore_test.go`（新）、`mobile/bind/readme_test.go`（新）、`mobile/bind/bind_test.go`（改测试）、`mobile/README.md`（文档）+ 本 plan/台账。圈得出来，无需竖切卡。

### 4.4 类型标注（边界型子系统行为验收，真机清单，归协调者）

`mobile/bind` 含边界型面（平台 cookie jar，对面是 Android/iOS 壳现实）。机内不可判，须真机/CI（spec §9.5/§9.9，breakdown §6）。**这些步骤由协调者执行，不派发**：

1. gobind 真产物：在装钉版 `gobind`（`gobindPinned = v0.0.0-20260908204917-8b95e45f8d3e`，`gobind_surface_test.go:18`）的机器跑 `cd mobile && go test ./bind/ -run TestGomobileSurfaceHasNoSkips -count=1`，确认无 `skipped function/field`；本 linux 工作树缺 gobind → **SKIP（非绿）**，不得以退出 0 冒充。
2. 重建 Android AAR / iOS XCFramework，真机验证切机后 API 与 WebSocket 带新 cookie、旧机 cookie 不再发送、无 cookie 仍 401。
3. 真机验证壳按 README 顺序清该 loopback host 全部端口旧 cookie、写 host-only cookie 后导航不被旧 cookie 拉回。
4. 壳清 jar/注入失败 UX：不导航 + 可行动错误 + 可重试。
5. 前置环境按 `mobile/README.md:42-80` 核对（JDK 21、NDK `30.0.16248370`、SDK platform 36）；缺环境报具体前置，不得误报代码失败。

### 4.5 接缝覆盖（双向，对照 spec §8.1 接缝清单）

spec §8.1 接缝清单：① `Pair → SwitchMachine → SessionCookie → Close`；② gomobile 形状；③ 平台 cookie jar。

**测试 → 缝**：
- `TestRealCoreSwitchAndCookieOwnership` / `TestRealCoreFailsClosed` / `TestRealCoreConcurrentNoCrossMachine`：入口符号 = 导出面 `Pair`/`SwitchMachine`/`SessionCookie`/`Close`（缝①），穿真实 `*mobilecore.Core` + 真实适配器。
- `TestDefaultRuntimeSharesOneCore`（既有）：入口 = 包级 `core`/`sessions`（生产组装不变量，缝①的装配前置）。
- `TestReadmeDocumentsShellCallOrder`：入口 = `mobile/README.md`（交接文档面，spec §9.8）。
- `TestBindSessionNotPairedFailsClosed`：入口 = 导出面 `SessionCookie`/`SwitchMachine`（缝①，未配对分支）。

**缝 → 测试**：
- 缝①：Task 1（归属/缓存/错机）+ Task 2（失败闭合/并发）+ Task 3（未配对）+ 既有 `TestCoreSessions*` 全锁。
- 缝②：既有 `TestBindExportedSurfaceIsFrozen` + `TestGomobileSurfaceHasNoSkips`（真产物归真机/CI，§4.4）。
- 缝③：真机接缝（§4.4），仓内 fake 不顶替。

**内部锁声明**：无。本卡全部断言入口在缝①/文档面上，无纯内部锁顶替缝级断言。

**退路同闸**：无改变入口符号的条件退路（Task 1 步骤 2 的红因分流是「夹具 vs 生产缺陷」的定位指引，不改变任何测试入口）。

---

## 5. 占位符扫描

无 TBD、无「加适当的错误处理」、无「同 Task N」指代。每个 task 有精确文件路径、完整可编译代码块、精确判据命令与预期。测试代码为完整片段；夹具**自建**（不复用 `internal/mobilecore` 同包未导出 helper，spec §8.2），符合「夹具形态因包而异」的正当出口——已在此自我声明。无其它骨架测试。

## 6. 收口验证（不属任何单 task；全量一次）

所有 task 完成后（收口/acceptance，不塞进单 task）：

```text
cd mobile && go build ./... && go test ./... -count=1 && go test ./bind/ -race -count=1 && go vet ./... && gofmt -l .
go build ./...            # 根
go list ./... | grep -c handoff/mobile           # 期望 0
go list -deps ./... | grep -c handoff/mobile     # 期望 0
codegraph --repo . check    # 期望 fails=[] 退出 0
codegraph --repo . resolve --doc docs/superpowers/plans/b392-plan.md   # 本 plan 的 file#Symbol 锚全 ok
```

根全量测试（spec §9.7）与真机清单（§4.4）归 acceptance/协调者。

## 7. 自审三查

- **spec 覆盖**：§3.1 目标 → Task 1/2/3（真实 Core 接线行为、失败闭合、删占位守卫）；§8.1 接缝①→Task 1–3；§8.2 八项 → Task 1（1–5）、Task 2（6–8）；§8.3 四变异 → Task 5 + 身份守卫（既有 `TestDefaultRuntimeSharesOneCore`）；§9.8 文档 → Task 4；§9.9 真机 → §4.4（协调者）。非目标（不改 ticket/wire/前端、不上移协议、不新增 DTO）由「零生产改动 + 禁止清单」兜住。
- **占位符扫描**：见 §5，通过。
- **跨 task 类型/签名一致性**：Task 1 `Produces` 的 `fakeAgentd`/`realChain`/`newRealChain`/`realChain.pair`/`stats`/`setOmitCookie` 与 Task 2/5 的 `Consumes` 逐字一致；`newCoreSessions(sessionCoreAPI)`、`swapCore(coreAPI)`、`swapSessions(sessionAPI)` 与现状签名逐字一致；导出面七函数零改。

## 8. 图 / 契约影响

- 本 plan 引用的已入图锚（收口用 `codegraph resolve --doc` 核验）：对侧 cookie 属性 `internal/agentd/authroutes.go#sessionCookie`；测试夹具真实领票调用 `internal/client/client.go#Client.IssueAuthTicket`。
- 零生产符号变更、零新跨域边（`mobile/bind` 图外）；`target.json`/`best.json` 不改、无视图 diff；`codegraph check` 保持 `fails=0`。
- 契约零改动：本卡兑现 contract §4/§5 已冻条目，不重开七函数或 wire。
- 图覆盖债：`coreSessions`/`newCoreSessions`/`Core.Activate`/`Core.Pair` 仍未入图（§1），归 `docs/roadmap.md`「mobilecore 图覆盖债」，本卡不偷带全图重扫。
