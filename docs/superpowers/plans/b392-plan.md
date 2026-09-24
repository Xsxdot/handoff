# B392 实现计划：mobile bind 生产接线 SessionCookie / SwitchMachine（去掉 notWiredSessions）

读者：对代码库零上下文的执行者。级别 **L3 轻档**（spec §2.3；breakdown 定级）。
上游：spec `docs/superpowers/specs/b392.md`、contract `docs/superpowers/specs/b392-contract.md`、breakdown `docs/superpowers/specs/b392-breakdown.md`（均在本分支内，`cards/B392-spec @400ee3c4` 为合并目标）。
工作分支：`cards/B392-charter-5`。**只在本分支工作，不切分支、不改 git 配置、不 push。**
本节点台账：`docs/superpowers/ledgers/2026-09-24-b392-plan-ledger.md`。

> **第 2 版（review fail 修订）**：第 1 版把 spec §8.3 的「默认生产竖切」降格为「既有身份断言 + swap 隔离的真实 Core 竖切」，被 review 判 P0。本版按 review 指令改回：**新增默认生产竖切**——不调用 `swapCore`/`swapSessions`，直接用包级 `liveCore`（`DefaultDial`）走 `Pair → SwitchMachine → SessionCookie → Close`，用真实 Core + 本地 HTTP 对端，断言真实 cookie 值与归属；swap 隔离的真实 Core 竖切**保留**（spec §8.2 行为覆盖），但不再顶替默认生产竖切。P1 各项同时修订（见 §9 修订记录）。
>
> **P0 可行性已核**：`liveCore = mobilecore.New(nil, log)`（`mobile/bind/bind.go:40`）；`New` 在 `dial==nil` 时用 `DefaultDial`（`internal/mobilecore/core.go:106-115`）；`DefaultDial` 对直连形态机器走 `client.New(m.Addr, m.Token)`（`core.go:66-69`）。故默认 `liveCore` 能直连本地 HTTP 夹具，默认竖切可实现，**不需要也不允许把冻结契约改写成 swap 方案**。
>
> **生产代码已在 Ticket 0 落完**（contract §7.1）：`mobile/bind/adapter.go`（`coreSessions` 适配器）、`mobile/bind/bind.go`（`liveCore` 单实例）、`mobile/bind/session.go`（`sessions = newCoreSessions(liveCore)`，占位已删）均已在 HEAD。本实现轮**只加测试与交接文档**，禁止再改 `mobile/bind/*.go` 生产文件与任何 `internal/**`、`cmd/**`、`web/**`、`codegraph/**`（唯一例外：Task 4 允许改测试文件 `adapter_test.go`/`bind_test.go`）。

---

## 0. 基线复核（本节点亲跑，原始输出入台账）

命令与读数（本工作树 `go1.26.1 linux/amd64`；`mobile/` 是独立嵌套 module，需 `cd mobile`）：

```text
go version                                  → go1.26.1 linux/amd64
go build ./... (root)                       → exit 0
go list ./... | grep -c handoff/mobile      → 打印 0，grep 退出码 1（无匹配即 0，属预期，不是失败）
go list -deps ./... | grep -c handoff/mobile→ 打印 0，grep 退出码 1（同上）
(cd mobile) go build ./...                  → exit 0
(cd mobile) go test ./... -count=1          → ok github.com/Xsxdot/handoff/mobile 0.805s
                                              ok github.com/Xsxdot/handoff/mobile/bind 0.005s
(cd mobile) go test ./bind/ -race -count=1  → ok github.com/Xsxdot/handoff/mobile/bind 1.019s
(cd mobile) go vet ./...                    → exit 0（无输出）
(cd mobile) gofmt -l .                      → 无输出
```

> **grep -c 退出码**：`grep -c` 在无匹配时仍打印 `0` 但退出码为 1。收口判据以**打印的计数**为准（`0`），不得把退出码 1 当成命令失败；若要机器判，用 `test "$(go list ./... | grep -c handoff/mobile)" = 0`（命令替换不传退出码）。

- 现状守卫在场：`mobile/bind/adapter_test.go`（顺序/失败闭合/错机/锁/`TestDefaultRuntimeSharesOneCore`）、`mobile/bind/export_surface_test.go`（七函数逐字冻结、生产 import 禁令）、`mobile/bind/gobind_surface_test.go`（真 gobind 产物，缺 `gobind` 时 skip）。
- 现状欠账（必须在本轮补齐，contract §8.1–8.4）：**默认生产竖切**、真实 Core 竖切、生产守卫与四变异红、`mobile/README.md` 调用顺序。gobind 真产物与 AAR/XCFramework 属真机，归协调者（§4.4）。

### 0.1 生产守卫落法（本版修订）

spec §8.3 字面要求「不调用 `swapCore`/`swapSessions`、从默认生产组合直走完整链」。本版落为**两支并存、各司其职**：

1. **默认生产竖切（P0，Task 3）**：不 swap，直接用包级 `liveCore` 走 `Pair → SwitchMachine → SessionCookie → Close`，真实 Core（`DefaultDial`）+ 本地 HTTP 夹具，断言真实 cookie 值与归属。因 `liveCore` 是包级单例、`Close` 不可逆、配对会登记机器并起回环反代，故**在子进程里跑**（`-test.run` 限定，天然干净），父测试只起夹具、传地址、校验子进程退出。这是本卡「生产不是假绿」的承重守卫。
2. **身份守卫（既有，不 swap）**：`TestDefaultRuntimeSharesOneCore` 断言导出面 `core`/`sessions` 指向同一 `*mobilecore.Core`、`sessions` 具体类型 `*coreSessions`；作为**附加**装配断言，不顶替 #1。
3. **行为竖切（Task 1/2，swap 隔离）**：`realcore_test.go` 用**独立** `mobilecore.New` + 本地夹具，经 `swapCore`/`swapSessions` 注入导出面，覆盖 spec §8.2 的 A→B→A 归属/缓存/失败闭合/并发；swap 只为隔离，不碰 `liveCore`，也**不顶替** #1。

实现者按此写：**默认竖切（#1）必须在场**；不得在 `liveCore` 上直接配对后留在主测试进程（会污染同包测试），也不得用 swap 版竖切或纯身份断言顶替 #1。

---

## 1. 图覆盖债（本节点亲跑，非阻断）

- `codegraph --repo . sym coreSessions` → 退出 1，「符号不在图中，近似候选 []」。
- 原因：`mobile/` 前缀被扫描配方排除（嵌套 module，图外）；`Core.*` 是 S2 新符号，未进旧 baseline。spec §11 / contract §3 已记，**本卡不偷带全图重扫**，回落精确 `file:line`。
- `codegraph --repo . check` → 退出 0（`fails=[]`；warns 为基线既存：`best-dangling`（含 `k_mobilecore_*`）/`budget-raised`/`legacy`/`oversized-package`/`prefix-family`）。
- `codegraph --repo . validate` → 退出 1，**2 个既存问题**（`cards-B272-charter` 的 `k_dropdir_fn` 引用不存在领域；`cards-B374-charter` 的 `k_collab_model` 重复），均非本卡（本卡无视图）。
- `codegraph --repo . resolve --doc docs/superpowers/plans/b392-plan.md` → 退出 0，两锚 `internal/agentd/authroutes.go#sessionCookie`、`internal/client/client.go#Client.IssueAuthTicket` 均 `ok`。
- 本卡不新增根模块符号（只在图外 `mobile/bind` 加 `_test.go` 与 `README.md`）→ `target.json`/`best.json` 零改、无视图 diff。

---

## 2. 测试范围声明（最小化）

- **Task 1**：`cd mobile && go test ./bind/ -run 'TestRealCoreSwitchAndCookieOwnership' -count=1`
- **Task 2**：`cd mobile && go test ./bind/ -run 'TestRealCoreFailsClosed|TestRealCoreGateSerializesSwitchAndRead' -count=1`
  另 `cd mobile && go test ./bind/ -race -run 'TestRealCoreGateSerializesSwitchAndRead|TestRealCoreConcurrentNoCrossMachine' -count=1`
- **Task 3**：`cd mobile && go test ./bind/ -run 'TestDefaultProductionVerticalSlice' -count=1`
- **Task 4**：`cd mobile && go test ./bind/ -run 'TestBindSessionNotPairedFailsClosed|TestCoreSessionsLockSerializesSwitchAndRead' -count=1`
- **Task 5**：`cd mobile && go test ./bind/ -run 'TestReadmeDocumentsShellCallOrder' -count=1`
- **Task 6**：变异命令逐条见该 task 步骤（各跑单支测试）。
- 每个 task 收尾：`cd mobile && go build ./...`。
- **全量测试不属任何单 task**：`cd mobile && go test ./... -count=1 && go test ./bind/ -race -count=1` 与根模块全量属收口/acceptance（见 §6），禁止塞进单 task。

---

## 3. 任务依赖 / DAG

```text
Task 1（realcore_test.go：夹具 + A→B→A 归属/缓存竖切，绿）
   ├─> Task 2（同文件：失败闭合 + HTTP gate 并发 + -race 附加）
   └─> Task 3（default_slice_test.go：子进程默认生产竖切，P0 承重守卫）  ← 最薄路径条
Task 4（bind_test.go 空态隔离 + adapter_test.go TryLock 清理）   独立
Task 5（README + readme_test.go 文档面）                          独立
Task 6（四变异复验，依赖 1/2/3/4 的测试在场）
```

**最薄路径条判定**：本卡要锁的全部行为（默认 `liveCore` 直走完整链、真实 `*mobilecore.Core` 穿过导出面返回目标机 cookie/origin）今天**已可达**——Ticket 0 已把 `sessions` 接到 `newCoreSessions(liveCore)`，且 `liveCore` 的 `DefaultDial` 能直连直连形态的本地夹具（`internal/mobilecore/core.go:66-69`）。故新测试写下去即应绿，不另设「点亮行为」的最薄路径 task；Task 1 排第一并为 Task 2/3 提供夹具。

---

## Task 1：真实 Core 竖切夹具与 A→B→A 归属（`mobile/bind/realcore_test.go`，绿）

**文件**：`mobile/bind/realcore_test.go`（新建，`_test.go`）。**不改任何生产 `.go`。**

**Interfaces**
- **Consumes**：
  - `mobilecore.New(dial DialFunc, log *slog.Logger) *Core`（`internal/mobilecore/core.go:107`）
  - `mobilecore.DialFunc` = `func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error)`（`core.go:35`）
  - `*mobilecore.Core` 的 `Pair`（`core.go:125`）、`Activate`（`core.go:219`）、`Session`（`core.go:266`）、`ActiveMachine`（`core.go:279`）、`Origin`（`core.go:333`）、`MachineNames`（`core.go:350`）、`Close`（`core.go:361`）
  - `client.New(addr, token string) *Client`（`internal/client/client.go:195`）
  - `proto.PairBundle`/`PairMachine`/`PairVersion`/`EncodePairBundle`（`internal/proto/pairing.go:47/64/28/111`）
  - `proto.AuthTicketResp{URL string; ExpiresAt time.Time}`（`internal/proto/auth.go:16`）
  - 测试缝：`swapCore`/`swapSessions`（`mobile/bind/fakes_test.go:103/109`）、`newCoreSessions`（`mobile/bind/adapter.go:35`）
  - 导出面：`Pair`/`SwitchMachine`/`SessionCookie`/`Close`（`mobile/bind/bind.go:47`、`session.go:37/24`、`bind.go:95`）
- **Produces**（包内 `_test.go`，供 Task 2/3/5 用）：
  - `type fakeAgentd struct{ machine string; mu sync.Mutex; tickets int; exchanges int; lastValue string; omitCookie bool; server *httptest.Server; entered chan struct{}; release chan struct{} }`
  - `func newFakeAgentd(machine string) *fakeAgentd`
  - `func (f *fakeAgentd) setOmitCookie(v bool)`
  - `func (f *fakeAgentd) gateConsole() (entered, release chan struct{})`
  - `func (f *fakeAgentd) stats() (tickets, exchanges int, lastValue string)`
  - `func (f *fakeAgentd) close()`
  - `type realChain struct{ core *mobilecore.Core; fixtures map[string]*fakeAgentd }`
  - `func newRealChain(t *testing.T, machines ...string) *realChain`
  - `func (rc *realChain) pair(t *testing.T, names ...string)`
  - 测试 `TestRealCoreSwitchAndCookieOwnership`

### 步骤

1. **写新文件头注释 + 夹具**。新建 `mobile/bind/realcore_test.go`，完整内容：

```go
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
```

2. **跑绿**（行为已在 HEAD 可达，写下即绿；本 task 不设「先红」步骤）：
   ```
   cd mobile && go test ./bind/ -run 'TestRealCoreSwitchAndCookieOwnership' -count=1
   cd mobile && go build ./...
   ```
   预期 `ok github.com/Xsxdot/handoff/mobile/bind`。若红，先判红因：夹具路由/HTTPServer 地址错（改夹具）与实际适配器/Core 缺陷（**记台账报协调者，不得改生产 `.go`**）分开。

3. **关键节点日志核对（无可观测性新增，但要复核）**：本 task 零生产代码改动，故**不新增日志**；既有日志已覆盖入口/成功/失败——绑定面 `session.go:27/31/38/44`、核侧兑换 `internal/mobilecore/session.go:70/73/95/100/109/113/117`。测试断言失败信息已带 got/want 上下文。**这一条是复核已满足，不是跳过。**

4. **注释核对**：新文件头已写职责+边界（步骤 1 已含）；`fakeAgentd`/`newFakeAgentd`/`gateConsole`/`stats`/`newRealChain`/`pair` 已带作用与边界注释。若实现中新增 helper，同样加注释。

**不得触碰**：`mobile/bind/adapter.go`、`bind.go`、`session.go`、`types.go`、`export_surface_test.go`、`gobind_surface_test.go`、`fakes_test.go`、`adapter_test.go`、`bind_test.go`、`internal/**`。

---

## Task 2：失败闭合、HTTP gate 并发与 `-race`（同文件续写，绿）

**文件**：`mobile/bind/realcore_test.go`（续加）。

**Interfaces**
- **Consumes**：Task 1 的 `newRealChain`/`realChain.pair`/`realChain.fixtures`/`fakeAgentd.setOmitCookie`/`fakeAgentd.gateConsole`/`fakeAgentd.stats`；导出面 `SwitchMachine`/`SessionCookie`/`Close`。
- **Produces**：`TestRealCoreFailsClosed`、`TestRealCoreGateSerializesSwitchAndRead`、`TestRealCoreConcurrentNoCrossMachine`（`-race` 附加）。

### 步骤

1. 在 `mobile/bind/realcore_test.go` 末尾追加：

```go
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

// TestRealCoreGateSerializesSwitchAndRead 是真实 Core 的并发验收集合（spec §8.2-8）：
// 用可控 HTTP gate 在**真实链**上确定性制造「切机在途」交错。B 的 /console 兑换
// 被 gate 卡住时，Core 已清空会话罐且适配器锁仍被切机持有；此时并发发起的
// SessionCookie(B)/SessionCookie(A) 都被锁挡到切机完成之后：
//   - SessionCookie(B) 成功返回 B 的**真实** cookie（至少一次成功读取）；
//   - SessionCookie(A) 必须失败，且绝不返回 B 的值（不串机）。
// 说明：去适配器锁的**确定性**打红由同包 TryLock 测试承担（真实链上「校验活动机
// 与读 cookie」之间没有可注入的网络缝，无法在真实 Core 上确定性复现 TOCTOU）。
// 本测试与 TryLock 测试互为补充，不可互相替代。
func TestRealCoreGateSerializesSwitchAndRead(t *testing.T) {
	rc := newRealChain(t, "A", "B")
	rc.pair(t, "A", "B")
	if _, err := SwitchMachine("A"); err != nil {
		t.Fatalf("前置切机 A: %v", err)
	}

	entered, release := rc.fixtures["B"].gateConsole()

	switchDone := make(chan switchOutcome, 1)
	go func() {
		origin, err := SwitchMachine("B")
		switchDone <- switchOutcome{origin, err}
	}()
	<-entered // 确定性：B 的 /console 兑换已在途（active 已被清空，锁仍被持有）

	readB := make(chan readOutcome, 1)
	go func() {
		v, err := SessionCookie("B")
		readB <- readOutcome{v, err}
	}()
	readA := make(chan readOutcome, 1)
	go func() {
		v, err := SessionCookie("A")
		readA <- readOutcome{v, err}
	}()

	close(release) // 放行 B 的兑换
	sw := <-switchDone
	if sw.err != nil {
		t.Fatalf("切机 B: %v", sw.err)
	}
	if !strings.HasPrefix(sw.origin, "http://127.0.0.1:") {
		t.Fatalf("切机 B 应返回 loopback origin: %q", sw.origin)
	}

	_, _, wantB := rc.fixtures["B"].stats()

	rb := <-readB
	if rb.err != nil || rb.value == "" || rb.value != wantB {
		t.Fatalf("切机 B 完成后 SessionCookie(B) 应逐字返回 B 真实值: got=%q want=%q err=%v",
			rb.value, wantB, rb.err)
	}
	if !strings.Contains(rb.value, "-B-") || strings.Contains(rb.value, "-A-") {
		t.Fatalf("SessionCookie(B) 归属错误（应含 -B- 不含 -A-）: %q", rb.value)
	}
	ra := <-readA
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
   cd mobile && go test ./bind/ -run 'TestRealCoreFailsClosed|TestRealCoreGateSerializesSwitchAndRead' -count=1
   cd mobile && go test ./bind/ -race -run 'TestRealCoreGateSerializesSwitchAndRead|TestRealCoreConcurrentNoCrossMachine' -count=1
   cd mobile && go build ./...
   ```
   预期全 `ok`，`-race` 无 `DATA RACE`。

3. **关键节点日志核对**：同 Task 1 步骤 3（零生产改动、既有日志覆盖），测试失败信息带 machine/value 上下文。

4. **注释核对**：三个测试函数头已写「锁什么、为什么」，gate 测试注明「真实链无可注入缝、去锁变异归 TryLock」与合法失败分支。

---

## Task 3：默认生产竖切（子进程，P0 承重守卫）（`mobile/bind/default_slice_test.go`，绿）

**意图**：spec §8.3 的「不调用 `swapCore`/`swapSessions`、从默认生产组合直走完整链」在本 task 落成真正的验收集合。使用包级 `liveCore`（真实 Core，`DefaultDial` 直连）与本地 HTTP 夹具，断言真实 cookie 值与归属。

**文件**：`mobile/bind/default_slice_test.go`（新建，`_test.go`）。**不改任何生产 `.go`。**

**Interfaces**
- **Consumes**：Task 1 的 `newFakeAgentd`/`fakeAgentd.server`/`fakeAgentd.close`；导出面 `Pair`/`SwitchMachine`/`SessionCookie`/`Close`；`proto.PairBundle`/`PairMachine`/`PairVersion`/`EncodePairBundle`。
- **Produces**：`TestDefaultProductionVerticalSlice`（父：起夹具 + spawn 子进程）、`runDefaultProductionVerticalSlice`（子：走默认 `liveCore`）、`fetchLastCookie`。

### 步骤

1. **写文件**。新建 `mobile/bind/default_slice_test.go`，完整内容：

```go
// default_slice_test.go 是 B392 §8.3 的**默认生产竖切**（P0 承重守卫）：
// **不调用 swapCore/swapSessions**，直接用包级默认 liveCore（DefaultDial）
// 走 Pair → SwitchMachine → SessionCookie → Close，用真实 Core + 本地 HTTP
// 对端夹具，断言真实 cookie 值与归属。
//
// 为什么要子进程：liveCore 是包级单例，Close 不可逆、配对会登记机器并起回环
// 反代——若在主测试进程里跑会污染同包其它测试、并使结果依赖测试次序。故真正的
// 竖切放进**子进程**（-test.run 只跑本测试，liveCore 天然干净）；父进程只起真实
// HTTP 对端夹具、把夹具地址经环境变量交给子进程、校验子进程退出。
//
// 禁止以 swap 版竖切或纯身份断言替代本文件（review P0）。
package bind

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

const (
	envDefaultSliceChild = "B392_DEFAULT_SLICE_CHILD"
	envAgentdAURL        = "B392_AGENTD_A_URL"
	envAgentdBURL        = "B392_AGENTD_B_URL"
)

// TestDefaultProductionVerticalSlice 是默认生产竖切入口：父进程起夹具并 fork
// 子进程；子进程用默认 liveCore 跑 runDefaultProductionVerticalSlice。
func TestDefaultProductionVerticalSlice(t *testing.T) {
	if os.Getenv(envDefaultSliceChild) == "1" {
		runDefaultProductionVerticalSlice(t)
		return
	}
	fa := newFakeAgentd("A")
	defer fa.close()
	fb := newFakeAgentd("B")
	defer fb.close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestDefaultProductionVerticalSlice$", "-test.v")
	cmd.Env = append(os.Environ(),
		envDefaultSliceChild+"=1",
		envAgentdAURL+"="+fa.server.URL,
		envAgentdBURL+"="+fb.server.URL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("默认生产竖转子进程失败: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("子进程未报告 PASS:\n%s", out)
	}
}

// runDefaultProductionVerticalSlice 在子进程内用默认 liveCore 走完整链
// Pair → SwitchMachine → SessionCookie（再 Close 收尾）。前置：两个夹具地址
// 经环境变量传入。全程不调用 swapCore/swapSessions。
func runDefaultProductionVerticalSlice(t *testing.T) {
	urlA, urlB := os.Getenv(envAgentdAURL), os.Getenv(envAgentdBURL)
	if urlA == "" || urlB == "" {
		t.Fatalf("缺夹具地址: A=%q B=%q", urlA, urlB)
	}
	defer func() { _ = Close() }() // 子进程内关掉 liveCore 的回环反代

	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version: proto.PairVersion,
		Machines: []proto.PairMachine{
			{Name: "A", Token: "tok-A", Addr: urlA},
			{Name: "B", Token: "tok-B", Addr: urlB},
		},
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("编码配对 bundle: %v", err)
	}
	// 关键：不调用 swapCore/swapSessions——走生产默认装配（liveCore）。
	if err := Pair(payload); err != nil {
		t.Fatalf("配对: %v", err)
	}

	originA, err := SwitchMachine("A")
	if err != nil {
		t.Fatalf("切机 A: %v", err)
	}
	if !strings.HasPrefix(originA, "http://127.0.0.1:") {
		t.Fatalf("切机 A 应返回 loopback origin，实得 %q", originA)
	}
	gotA, err := SessionCookie("A")
	if err != nil {
		t.Fatalf("取 A cookie: %v", err)
	}
	wantA := fetchLastCookie(t, urlA)
	if gotA == "" || gotA != wantA {
		t.Fatalf("SessionCookie(A) 必须逐字等于夹具 Set-Cookie: got=%q want=%q", gotA, wantA)
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
	gotB, err := SessionCookie("B")
	if err != nil {
		t.Fatalf("取 B cookie: %v", err)
	}
	wantB := fetchLastCookie(t, urlB)
	if gotB == "" || gotB != wantB || gotB == gotA {
		t.Fatalf("SessionCookie(B) 归属错误: gotB=%q wantB=%q gotA=%q", gotB, wantB, gotA)
	}
}

// fetchLastCookie 向夹具回读它最后签发的 handoff_session 值——证明绑定面返回的
// 值确实来自真实 Set-Cookie 响应，不是测试预置的假值。
func fetchLastCookie(t *testing.T, base string) string {
	t.Helper()
	resp, err := http.Get(base + "/last-cookie")
	if err != nil {
		t.Fatalf("读夹具 last-cookie: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读夹具 last-cookie 正文: %v", err)
	}
	return strings.TrimSpace(string(b))
}
```

2. **跑绿**（Ticket 0 已把 `liveCore` 接成真实会话；默认竖切写下去即绿）：
   ```
   cd mobile && go test ./bind/ -run 'TestDefaultProductionVerticalSlice' -count=1
   cd mobile && go build ./...
   ```
   预期 `ok github.com/Xsxdot/handoff/mobile/bind`。若红：先看子进程 stderr（父测试已把 `CombinedOutput` 打进失败信息）——多半是夹具地址/`DefaultDial` 直连路径问题，**不得改生产 `.go`，不得改成 swap 方案**。

3. **关键节点日志核对**：本 task 零生产改动；父测试失败信息含子进程完整输出，子测试失败信息带 got/want 与 machine。既有生产日志覆盖入口/成功/失败（同 Task 1 步骤 3）。

4. **注释核对**：文件头写清「为什么子进程」「不 swap」「禁止用身份断言替代」；三个函数各有作用/前置注释。

---

## Task 4：旧空态测试隔离收口 + TryLock 失败路径清理（改测试）

### 4.1 `mobile/bind/bind_test.go`（空态隔离，P3=A）

**背景**：现状 `bind_test.go:89 TestBindSessionFailIsClosed` 直接用默认 `sessions`（包着 `liveCore`），靠「共享 `liveCore` 无任何机器」才通过；注释仍写「S2 未接线时」已过期。改为**显式注入一个空真实 Core**，语义=「未配对即失败」，不依赖全局空态。

**Interfaces**
- **Consumes**：`mobilecore.New`（`core.go:107`）、`newCoreSessions`（`adapter.go:35`）、`swapSessions`（`fakes_test.go:109`）、包级 `log`（`bind.go:35`）、导出面 `SessionCookie`/`SwitchMachine`。
- **Produces**：`TestBindSessionNotPairedFailsClosed`（替换 `TestBindSessionFailIsClosed`）。

**步骤**

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

4. **注释核对**：新测试头已写「为什么不依赖 liveCore」；无生产改动、无新日志。

### 4.2 `mobile/bind/adapter_test.go`（TryLock 失败路径清理，review P1c）

**问题**：`TestCoreSessionsLockSerializesSwitchAndRead` 在去锁变异（Task 6 变异④）下于 `t.Fatal` 提前返回，但读者 goroutine 仍阻塞在 `<-d.sessionGate`，永久泄漏。改用 `defer`（带已关闭检测）保证任何失败路径都放行该 goroutine。

**步骤**

1. 把 `adapter_test.go:178-222` 的 `TestCoreSessionsLockSerializesSwitchAndRead` 整段替换为：

```go
// TestCoreSessionsLockSerializesSwitchAndRead 证明适配器锁让「校验活动机 + 读 cookie」
// 与「切机」互不穿插。读请求阻塞在 Session() 期间，用同包 `TryLock` 做确定性握手：
// 锁必须被持有着（去掉适配器锁则 TryLock 成功，测试确定性打红，不靠定时器猜）。
// 同时保留 A/B 行为断言——读 A 期间切 B，不得把 B 的 cookie 交给调用者。
//
// 清理：无论断言在哪一步失败，defer 都必须放行阻塞在 Session() 里的读者
// goroutine——去锁变异时失败路径若不放行会永久泄漏一个阻塞 goroutine。
func TestCoreSessionsLockSerializesSwitchAndRead(t *testing.T) {
	d := newSessionCoreDouble()
	d.active = "A"
	d.sessionEntered = make(chan struct{})
	d.sessionGate = make(chan struct{})
	defer func() {
		select {
		case <-d.sessionGate: // 正常路径已显式 close
		default:
			close(d.sessionGate)
		}
	}()
	a := newCoreSessions(d)

	type readResult struct {
		value string
		err   error
	}
	readDone := make(chan readResult, 1)
	go func() {
		v, err := a.SessionCookie("A")
		readDone <- readResult{v, err}
	}()

	// sessionEntered 在 `Session()` 内、持锁路径上关闭，因此 observe 到它即代表
	// 读者已持适配器锁并阻塞在 Session()。
	<-d.sessionEntered

	// 确定性握手：阻塞期间适配器锁必须被持有；去掉锁则 TryLock 会成功。
	if a.mu.TryLock() {
		a.mu.Unlock()
		t.Fatal("Session 阻塞期间适配器锁未被持有：切机与读会互相穿插")
	}

	switchDone := make(chan error, 1)
	go func() {
		_, err := a.SwitchMachine("B")
		switchDone <- err
	}()

	close(d.sessionGate)
	r := <-readDone
	if r.err != nil {
		t.Fatalf("读 A 会话失败: %v（若无锁，切机已改活动机导致错机拒绝）", r.err)
	}
	if r.value != "sess-A" {
		t.Fatalf("读到非 A 的 cookie: %q（适配器锁未串行化切机与读）", r.value)
	}
	if err := <-switchDone; err != nil {
		t.Fatalf("切机 B 失败: %v", err)
	}
}
```

2. **跑绿**：
   ```
   cd mobile && go test ./bind/ -run 'TestCoreSessionsLockSerializesSwitchAndRead' -count=1
   cd mobile && go build ./...
   ```

3. **注释核对**：已在测试头写清清理理由；`adapter_test.go` 其余测试不动。

---

## Task 5：交接文档 `mobile/README.md` + 文档面回归（红→绿）

**说明**：README 小节当前**不存在**，故本 task 是真正的红→绿（文档面锁缝断言）。其余 task 行为已在 HEAD 可达，写即为绿，不套红绿模板。

**文件**：`mobile/README.md`（新增小节）、`mobile/bind/readme_test.go`（新建，`_test.go`）。

**Interfaces**
- **Consumes**：`packageDir(t)`（`mobile/bind/gobind_surface_test.go:112`）。
- **Produces**：README 新增「切机与会话调用顺序（壳侧）」小节；`TestReadmeDocumentsShellCallOrder`。

### 步骤

1. **先写失败测试（红）**。新建 `mobile/bind/readme_test.go`：

```go
// readme_test.go 锁 B392 §9.8 交接文档：mobile/README.md 必须明写壳侧会话消费
// 顺序、旧 cookie 的精确删除条件（name=handoff_session + host + Path=/，不区分
// 端口）、失败不导航，以及「Go 只返回 cookie 值，不操作平台 cookie store」的职责边界。
package bind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadmeDocumentsShellCallOrder 断言 README 含 §4.4 调用顺序标记（按出现
// 位置有序）、旧 cookie 删除条件、cookie 属性与失败语义。顺序标记缺失或倒序即红。
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
		"name=handoff_session + host + Path=/", // 旧 cookie 删除条件（含 host、Path）
		"不区分端口",                            // 覆盖该 host 所有端口
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
   预期 FAIL，首行形如：`readme_test.go:NN: README 缺少调用顺序标记 "`SwitchMachine(target)`"`（README 尚无该小节）。

3. **最小实现**：在 `mobile/README.md` 的「## 绑定面（壳的契约面）」小节结束处（现状第 38 行 `...逐字钉住上表签名。` 之后、「## 构建」之前）插入：

````markdown
## 切机与会话调用顺序（壳侧）

壳切换到一个目标机时，必须严格按以下顺序调用绑定面，任一步失败都**不得导航**：

1. `SwitchMachine(target)` —— 成功返回后核内已清旧会话并为目标机重兑换，壳拿到目标 loopback 源。
2. 清除该 loopback host 的全部旧 cookie：按 `name=handoff_session + host + Path=/` 删除，**不区分端口**（cookie 不按端口隔离，须覆盖该 host 的所有端口）。
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

## Task 6：四变异复验（口径可红，原始红输出入台账，改动后自动+显式还原）

**文件**：临时改 `mobile/bind/session.go` / `mobile/bind/adapter.go`，复跑后**逐条还原**。不产生提交。

> 判据：每条变异**必须**让指定测试红。若某条不变红，说明对应守卫缺失——**停下来报协调者，不得改测试放过**。

### 6.0 变异会话初始化（加固流程，review P1b）

在**一个 bash 会话**里先跑下列定义；`MUT_ROOT` 为该任务私有唯一临时目录（落在 `$TMPDIR`，不在仓内），`trap` 保证退出时自动还原，`m_restore` 用 `cmp` 核验还原一致。

```bash
MUT_ROOT="$(mktemp -d "${TMPDIR:?}/b392-mut.XXXXXX")"
echo "MUT_ROOT=$MUT_ROOT"
m_backup() {  # $1=相对路径；备份并记录 hash
  local f="$1" tag; tag="$(printf '%s' "$f" | tr '/' '_')"
  cp "$f" "$MUT_ROOT/$tag.bak"
  sha256sum "$f" | tee "$MUT_ROOT/$tag.sha"
}
m_restore() { # $1=相对路径；还原并 cmp 核验
  local f="$1" tag; tag="$(printf '%s' "$f" | tr '/' '_')"
  [ -f "$MUT_ROOT/$tag.bak" ] || return 0
  cp "$MUT_ROOT/$tag.bak" "$f"
  if cmp -s "$MUT_ROOT/$tag.bak" "$f"; then echo "RESTORED-OK $f"; else echo "RESTORE-FAILED $f" >&2; return 1; fi
}
# 退出时自动还原（finally）；每条变异结束也应显式 m_restore 一次并确认 RESTORED-OK。
trap 'm_restore mobile/bind/session.go; m_restore mobile/bind/adapter.go' EXIT
```

**每条变异的统一节奏**：① 唯一命中检查（目标文本出现次数必须恰为 1，否则中止）→ ② `m_backup` 备份并记 hash → ③ 应用变异 → ④ **先 build**（`cd mobile && go build ./...`，编译不过即中止）→ ⑤ 跑指定测试、`2>&1 | tee` 保存原始红输出 → ⑥ `m_restore` 还原并确认 `RESTORED-OK`。

### 6.1 变异①：生产恢复占位（默认生产竖切红）

- 唯一命中检查：`test "$(grep -c 'var sessions sessionAPI = newCoreSessions(liveCore)' mobile/bind/session.go)" = 1`
- 备份：`m_backup mobile/bind/session.go`
- 应用（加 `errors` import、追加占位类型、替换装配）：
  ```bash
  perl -0pi -e 's/^package bind\n\n/package bind\n\nimport "errors"\n\n/' mobile/bind/session.go
  perl -0pi -e 's/var sessions sessionAPI = newCoreSessions\(liveCore\)/var sessions sessionAPI = notWiredSessions{}/' mobile/bind/session.go
  cat >> mobile/bind/session.go <<'EOF'

type notWiredSessions struct{}

func (notWiredSessions) SessionCookie(string) (string, error) {
	return "", errors.New("会话未接线")
}
func (notWiredSessions) SwitchMachine(string) (string, error) {
	return "", errors.New("会话未接线")
}
EOF
  ```
- 先 build：`cd mobile && go build ./...`
- 跑红（保存原始输出到 `$MUT_ROOT/mut1.red`）：
  ```bash
  (cd mobile && go test ./bind/ -run 'TestDefaultProductionVerticalSlice|TestDefaultRuntimeSharesOneCore' -count=1) 2>&1 | tee "$MUT_ROOT/mut1.red" || true
  ```
  预期两支 FAIL：默认竖切父测试报 `默认生产竖转子进程失败: exit status 1`；身份守卫报 `会话面生产装配不是 coreSessions: bind.notWiredSessions`。
- 还原：`m_restore mobile/bind/session.go`（须见 `RESTORED-OK`）。

### 6.2 变异②：去掉活动机检查（串机红）

- 唯一命中检查：`test "$(grep -cF 'if a.core.ActiveMachine() != machine {' mobile/bind/adapter.go)" = 1`
- 备份：`m_backup mobile/bind/adapter.go`
- 应用：`perl -0pi -e 's/if a\.core\.ActiveMachine\(\) != machine \{/if false \&\& a.core.ActiveMachine() != machine {/' mobile/bind/adapter.go`
- 先 build：`cd mobile && go build ./...`
- 跑红：
  ```bash
  (cd mobile && go test ./bind/ -run 'TestCoreSessionsSessionCookieRejectsWrongMachine|TestRealCoreSwitchAndCookieOwnership|TestRealCoreGateSerializesSwitchAndRead' -count=1) 2>&1 | tee "$MUT_ROOT/mut2.red" || true
  ```
  预期三支 FAIL（double 层 `错机必须返回 ("", err): value="sess-B" err=<nil>`；真实层 A 取到 B 值 / gate 测试串机）。
- 还原：`m_restore mobile/bind/adapter.go`。

### 6.3 变异③：失败路径改 `return "", nil`（失败语义红）

- 唯一命中检查：`test "$(grep -cF 'ck, err := a.core.Activate(context.Background(), machine)' mobile/bind/adapter.go)" = 1`
- 备份：`m_backup mobile/bind/adapter.go`
- 应用（只改 `Activate` 失败分支的返回值）：
  ```bash
  perl -0pi -e 's/(ck, err := a\.core\.Activate\(context\.Background\(\), machine\)\n\tif err != nil \{\n\t\treturn )"", err/$1"", nil/' mobile/bind/adapter.go
  grep -A2 'ck, err := a.core.Activate' mobile/bind/adapter.go   # 复核已变成 return "", nil
  ```
- 先 build：`cd mobile && go build ./...`
- 跑红：
  ```bash
  (cd mobile && go test ./bind/ -run 'TestCoreSessionsSwitchMachineFailsClosed|TestRealCoreFailsClosed' -count=1) 2>&1 | tee "$MUT_ROOT/mut3.red" || true
  ```
  预期两支 FAIL（`Activate 失败必须返回 ("", err): origin="" err=<nil>` 等）。
- 还原：`m_restore mobile/bind/adapter.go`。

### 6.4 变异④：去掉适配器互斥（并发锁红）

- 唯一命中检查：`test "$(grep -cF 'a.mu.Lock()' mobile/bind/adapter.go)" = 2` 且 `test "$(grep -cF 'defer a.mu.Unlock()' mobile/bind/adapter.go)" = 2`
- 备份：`m_backup mobile/bind/adapter.go`
- 应用：`perl -0pi -e 's/\ta\.mu\.Lock\(\)\n//g; s/\tdefer a\.mu\.Unlock\(\)\n//g' mobile/bind/adapter.go`
- 先 build：`cd mobile && go build ./...`
- 跑红（**确定性打红**归 TryLock 测试；`-race` gate 为附加闸）：
  ```bash
  (cd mobile && go test ./bind/ -run 'TestCoreSessionsLockSerializesSwitchAndRead' -count=1) 2>&1 | tee "$MUT_ROOT/mut4.red" || true
  (cd mobile && go test ./bind/ -race -run 'TestRealCoreGateSerializesSwitchAndRead' -count=1) 2>&1 | tee -a "$MUT_ROOT/mut4.red" || true
  ```
  预期 `TestCoreSessionsLockSerializesSwitchAndRead` FAIL：`Session 阻塞期间适配器锁未被持有：切机与读会互相穿插`。TryLock 失败路径由 Task 4.2 的 `defer` 放行，读者 goroutine 不再泄漏。
- 还原：`m_restore mobile/bind/adapter.go`。

### 6.5 还原核验

- 四条全部还原后（`RESTORED-OK` 各一次）跑：`cd mobile && go test ./... -count=1` 全绿；`cd mobile && git diff --name-only` **不得**列出 `mobile/bind/adapter.go`、`mobile/bind/bind.go`、`mobile/bind/session.go`（生产文件与 HEAD 逐字一致）。
- `git status --short` 里除本 task 自身新增/修改的测试与 README 外，**无生产 `.go` 变更**；变异临时文件在 `$TMPDIR` 下，不在仓内。生产文件还原一致性以 6.0 的 `cmp`/hash 为准，不以「git status 看起来对」为准。

---

## 4. 五项检查（实现类 plan 出稿自审）

### 4.1 缺陷族对抗审查

| 族 | 设问 | 结论 |
|---|---|---|
| 生命周期 / 状态机中断 | 适配器 `mu` 跨 Core 网络兑换（最长 `exchangeTimeout=5s`，`internal/mobilecore/session.go:35`）；`Close` 与 `Activate` 并发 | 契约冻结语义，非缺陷；`Activate` 兑换后重取 `c.mu` 检查 `closed`（`core.go:252-256`），`Session`/`Origin` 同样先查（`:269/:336`）。`TestRealCoreFailsClosed/Close 后` 锁导出面层拒绝；默认竖切子进程内 `defer Close()` 关回环；独立 Core 由 `t.Cleanup` 关停，夹具随 `t.Cleanup`/`defer` 关停，无孤儿端口/goroutine |
| 静默失败 / 误导报错 | 有没有 `("", nil)`？测试自身会不会假绿？ | 适配器每条失败路径 `return "", err`（`adapter.go:42-51/62-71`）；本卡断言逐条断 `err==nil || v!=""` 即红。默认竖切**不 swap**、真实 Core、夹具 cookie 值来自真 `Set-Cookie`（`fetchLastCookie` 回读对账），非预置假值 |
| 跨平台假设 | 夹具/真 Core 是否引入平台相关面 | 测试只用 `net/http/httptest`、`os/exec`、`sync`，平台中立。平台 cookie jar 属真机（§7.4/§4.4） |
| 假红 / 假绿测试 | 判据钉行为还是计数？有无反面断言？ | 判据全为行为断言（归属、错机拒绝、缓存不重兑换、失败闭合、gate 并发不串机），无计数型判据。默认竖切由子进程退出码定 pass/fail。变异四条各自可红（Task 6）；同机缓存用「tickets/exchanges 不增 + 值不变」双断言 |
| 门禁绕过 | 是否放松回环门禁 / 绕过 cookie 闸 | 零生产改动，`export_surface_test.go` 的七函数与无 Token/Dial 断言继续绿；`TestBindProductionHasNoProtocolLogic` 继续绿（新 `_test.go` 不在其扫描范围，生产 import 边界未变） |
| 序列化边界 | 新字段/手写投影？ | 无新字段。既有链 `Core.exchangeTicket` → `toSessionCookie` → 适配器只取 `.Value`（`adapter.go:52`）→ 导出 string。默认竖切与真实竖切均断言 `SessionCookie` 逐字等于夹具真实 `Set-Cookie.Value`，穿真实 HTTP 序列化边界（详见 4.2） |
| 枚举新值过白名单 | 新枚举？ | 无新枚举值（cookie 名/`SameSite=Lax`/DTO 字段均不变） |
| 承重安全属性有测试锁住 | 「一罐只装一机/不串机」「Close 后拒绝」有能变红的测试吗？ | 有：默认竖切（真实 cookie 归属 A/B 不同）+ 真实竖切（错机拒绝）+ `TestRealCoreGateSerializesSwitchAndRead`（gate 交错不串机）+ `TestRealCoreFailsClosed`（Close 后）+ 变异②③④实测打红 |
| 子进程隔离自身 | 默认竖切会不会污染同包测试/依赖次序？ | 默认竖切只在**子进程**里碰 `liveCore`（`-test.run` 限定）；主测试进程从不 Pair 默认核 → 无污染、不依赖测试次序。父测试只起夹具并校验子进程退出 |

### 4.2 序列化边界设问（逐处列全）

`handoff_session` 的**值**从产生到消费的唯一链路，本卡新增断言覆盖最后一段：

1. 夹具（测试对端）`/console` 构造 `http.Cookie{Name:"handoff_session", Value:"sess-"+ticket}` 走 `Set-Cookie` 响应头 —— **真 wire 序列化**。
2. `internal/mobilecore.exchangeTicket` 解析 `resp.Cookies()` 选中 `handoff_session`、非空校验 → `toSessionCookie` 投影 `SessionCookie`（`internal/mobilecore/session.go:103-135`）——既有 Core 测试覆盖，本卡不重测。
3. 适配器 `coreSessions.SessionCookie` 只取 `.Value` 返回 string（`adapter.go:45-52`）——**本卡新增两条真实链断言**：默认竖切（子进程）与真实竖切（swap 隔离）都断言 `SessionCookie(machine)` 逐字等于夹具真实签发值，且 A/B 值不同、切机后旧机失败。
4. gomobile 映射为 Java/Kotlin `String`/ObjC `NSString` —— 形状由 `gobind_surface_test.go` 钉（真产物归真机/CI）。
5. 壳写平台 cookie jar（属性 `Path/HttpOnly/SameSite/Secure` 由壳硬编码，不由 Go 返回）——真机接缝（§7.4/§4.4）。

**可空类型区分「字段缺失 vs 值为零」**：本卡不新增可空字段；Core 对「无 `handoff_session`」与「值为空」分别报错（`internal/mobilecore/session.go:107-118`），适配器再对 `.Value==""` 兜底报错（`adapter.go:49/65`），不塌成零值。夹具 `omitCookie=true` 制造「缺 cookie」路径并由 `TestRealCoreFailsClosed/兑换无 cookie` 断言 error。无 roundtrip 属性测试需求（无新编码格式）。

### 4.3 上下文预算检查

有界文件集：`mobile/bind/realcore_test.go`（新）、`mobile/bind/default_slice_test.go`（新）、`mobile/bind/readme_test.go`（新）、`mobile/bind/bind_test.go`（改测试）、`mobile/bind/adapter_test.go`（改测试）、`mobile/README.md`（文档）+ 本 plan/台账。圈得出来，无需竖切卡。

### 4.4 类型标注（边界型子系统行为验收，真机清单，归协调者）

`mobile/bind` 含边界型面（平台 cookie jar，对面是 Android/iOS 壳现实）。机内不可判，须真机/CI（spec §9.5/§9.9，breakdown §6）。**这些步骤由协调者执行，不派发**：

1. **gobind 钉版比对**：先确认工具链是钉版（`gobindPinned = v0.0.0-20260908204917-8b95e45f8d3e`，`gobind_surface_test.go:18`）：
   ```bash
   command -v gobind
   go version -m "$(command -v gobind)" | grep -F 'golang.org/x/mobile'
   ```
   期望输出含 `v0.0.0-20260908204917-8b95e45f8d3e`。版本不符 → 报「工具版本不符」并停，**不得降级判据**。随后跑：
   `cd mobile && go test ./bind/ -run TestGomobileSurfaceHasNoSkips -v -count=1`
   确认无 `SKIP`、无 `skipped function/field`。本 linux 工作树未装 gobind → **SKIP（非绿）**，不得以退出 0 冒充。
2. 重建 Android AAR / iOS XCFramework，真机验证切机后 API 与 WebSocket 带新 cookie、旧机 cookie 不再发送、无 cookie 仍 401。
3. 真机验证壳按 README 顺序清该 loopback host 全部端口旧 cookie（`name=handoff_session + host + Path=/`）、写 host-only cookie 后导航不被旧 cookie 拉回。
4. 壳清 jar/注入失败 UX：不导航 + 可行动错误 + 可重试。
5. 前置环境按 `mobile/README.md:42-80` 核对（JDK 21、NDK `30.0.16248370`、SDK platform 36）；缺环境报具体前置，不得误报代码失败。

### 4.5 接缝覆盖（双向，对照 spec §8.1 接缝清单）

spec §8.1 接缝清单：① `Pair → SwitchMachine → SessionCookie → Close`；② gomobile 形状；③ 平台 cookie jar。

**测试 → 缝**：
- `TestDefaultProductionVerticalSlice`（子进程，**不 swap**）：入口符号 = 导出面 `Pair`/`SwitchMachine`/`SessionCookie`/`Close`（缝①），穿默认 `liveCore` + `DefaultDial` 真实链。
- `TestRealCoreSwitchAndCookieOwnership` / `TestRealCoreFailsClosed` / `TestRealCoreGateSerializesSwitchAndRead` / `TestRealCoreConcurrentNoCrossMachine`：入口符号 = 导出面 `Pair`/`SwitchMachine`/`SessionCookie`/`Close`（缝①），穿独立真实 `*mobilecore.Core` + 真实适配器。
- `TestDefaultRuntimeSharesOneCore`（既有）：入口 = 包级 `core`/`sessions`（生产装配不变量，缝①的装配前置，附加非替代）。
- `TestReadmeDocumentsShellCallOrder`：入口 = `mobile/README.md`（交接文档面，spec §9.8）。
- `TestBindSessionNotPairedFailsClosed`：入口 = 导出面 `SessionCookie`/`SwitchMachine`（缝①，未配对分支）。

**缝 → 测试**：
- 缝①：Task 3（默认竖切）+ Task 1（归属/缓存/错机）+ Task 2（失败闭合/gate 并发/-race）+ Task 4（未配对）+ 既有 `TestCoreSessions*` 全锁。
- 缝②：既有 `TestBindExportedSurfaceIsFrozen` + `TestGomobileSurfaceHasNoSkips`（真产物归真机/CI，§4.4）。
- 缝③：真机接缝（§4.4），仓内 fake 不顶替。

**内部锁声明**：无。本卡全部断言入口在缝①/文档面上，无纯内部锁顶替缝级断言。

**退路同闸**：无改变入口符号的条件退路（Task 1/3 步骤里的红因分流是「夹具 vs 生产缺陷」的定位指引，不改变任何测试入口）。

---

## 5. 占位符扫描

无 TBD、无「加适当的错误处理」、无「同 Task N」指代。每个 task 有精确文件路径、完整可编译代码块、精确判据命令与预期。测试代码为完整片段；夹具**自建**（不复用 `internal/mobilecore` 同包未导出 helper，spec §8.2），符合「夹具形态因包而异」的正当出口——已在此自我声明。默认竖切的「子进程」形态亦为完整代码，非骨架。无其它骨架测试。

## 6. 收口验证（不属任何单 task；全量一次）

所有 task 完成后（收口/acceptance，不塞进单 task）：

```text
cd mobile && go build ./... && go test ./... -count=1 && go test ./bind/ -race -count=1 && go vet ./... && gofmt -l .
go build ./...                                     # 根
test "$(go list ./... | grep -c handoff/mobile)" = 0          # 期望计数 0（grep 退出码 1 属预期）
test "$(go list -deps ./... | grep -c handoff/mobile)" = 0    # 期望计数 0
codegraph --repo . check                            # 期望 fails=[] 退出 0
codegraph --repo . resolve --doc docs/superpowers/plans/b392-plan.md   # 本 plan 的 file#Symbol 锚全 ok
```

根全量测试（spec §9.7）与真机清单（§4.4）归 acceptance/协调者。

## 7. 自审三查

- **spec 覆盖**：§3.1 目标 → Task 3（默认生产竖切，去掉假绿）+ Task 1/2/4（真实 Core 行为、失败闭合、删占位隔离）；§8.1 接缝①→Task 1–4；§8.2 八项 → Task 1（1–5）、Task 2（6–8）；§8.3 默认生产竖切 + 四变异 → Task 3 + Task 6 + 身份守卫（既有）；§9.8 文档 → Task 5；§9.9 真机 → §4.4（协调者）。非目标（不改 ticket/wire/前端、不上移协议、不新增 DTO）由「零生产改动 + 禁止清单」兜住。
- **占位符扫描**：见 §5，通过。
- **跨 task 类型/签名一致性**：Task 1 `Produces` 的 `fakeAgentd`/`realChain`/`newRealChain`/`realChain.pair`/`stats`/`setOmitCookie`/`gateConsole` 与 Task 2/3/6 的 `Consumes` 逐字一致；`switchOutcome`/`readOutcome` 仅 Task 2 内用；`newCoreSessions(sessionCoreAPI)`、`swapCore(coreAPI)`、`swapSessions(sessionAPI)` 与现状签名逐字一致；导出面七函数零改。

## 8. 图 / 契约影响

- 本 plan 引用的已入图锚（收口用 `codegraph resolve --doc` 核验）：对侧 cookie 属性 `internal/agentd/authroutes.go#sessionCookie`；测试夹具真实领票调用 `internal/client/client.go#Client.IssueAuthTicket`。
- 零生产符号变更、零新跨域边（`mobile/bind` 图外）；`target.json`/`best.json` 不改、无视图 diff；`codegraph check` 保持 `fails=0`。
- 契约零改动：本卡兑现 contract §4/§5 已冻条目，不重开七函数或 wire。**默认竖切不 swap，正是 spec §8.3 的字面要求，未改写冻结契约。**
- 图覆盖债：`coreSessions`/`newCoreSessions`/`Core.Activate`/`Core.Pair` 仍未入图（§1），归 `docs/roadmap.md`「mobilecore 图覆盖债」，本卡不偷带全图重扫。

## 9. 修订记录

- **2026-09-24 / 第 2 版（review fail 处置）**：
  - **P0**：`TestDefaultProductionVerticalSlice` 落地为**默认生产竖切**——不调用 `swapCore`/`swapSessions`，直用包级 `liveCore`（`DefaultDial` 直连）走 `Pair → SwitchMachine → SessionCookie → Close`，真实 Core + 本地 HTTP 夹具，断言真实 cookie 值与归属；用子进程隔离 `liveCore` 单例（避免污染同包测试）。`TestDefaultRuntimeSharesOneCore` 与 swap 隔离的真实 Core 竖切降为**附加/行为覆盖**，不顶替默认竖切。
  - **P1a**：真实 Core 并发改为 `TestRealCoreGateSerializesSwitchAndRead`——用夹具 `gateConsole` 在 `/console` 兑换处做**可控 HTTP gate**，确定性制造「切机在途」，含至少一次成功读取（B），并断言切机后取 A 失败、绝不串 B；`-race` 与双锁 TryLock 作补充（并说明真实链无可注入缝、去锁确定性红归 TryLock）。
  - **P1b**：Task 6 变异流程加固——任务私有唯一临时目录（`mktemp -d $TMPDIR`）、唯一命中检查、`trap` 自动还原 + 显式还原、备份 `sha256sum` + `cmp` 核验、每次变异先 `go build`。
  - **P1c**：Task 4.2 修正 `TestCoreSessionsLockSerializesSwitchAndRead` 失败路径——`defer` 放行阻塞在 `Session()` 的读者 goroutine，消除去锁变异时的 goroutine 泄漏。
  - **P1d**：README 旧 cookie 删除条件明确 `name=handoff_session + host + Path=/`、不区分端口，并由 `TestReadmeDocumentsShellCallOrder` 的 `"name=handoff_session + host + Path=/"` 与 `"不区分端口"` 标记锁定。
  - **P1e**：§4.4 gobind 验收增加 `go version -m "$(command -v gobind)"` 钉版比对（`gobindPinned`）；版本不符即停，不得降级。
  - **P1f**：修正 `grep -c` 退出码说明（无匹配打印 0、退出 1，属预期）；Task 6.5 的 `git status`/还原预期改为「生产 `.go` 与 HEAD 逐字一致（`git diff --name-only` 不含之），cmp/hash 为准」；Task 1/2/3 明确「行为已在 HEAD 可达、写即为绿」，仅 Task 5 保留红→绿。
