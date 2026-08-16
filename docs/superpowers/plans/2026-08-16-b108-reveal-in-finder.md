# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# B108 Reveal in Finder（本机半边）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让文件树右键菜单的 `Reveal in Finder` 在「浏览器与 agentd 同在一台 macOS 上」时真的可用，其余情形按互斥的三条理由置灰。

**Architecture:** 新增一个不支持转发的 `POST /api/workspaces/reveal` 端点，复用既有工作树白名单，执行 `open -R`；平台支持度作为第三个能力位（继 `pty_supported` 之后）经 `/api/machines` 上报给前端；前端把既有的 `usePtySupport` 泛化成 `useMachineCaps`，FileTree 据此按四分支决定置灰与理由。

**Tech Stack:** Go 1.26（`net/http` 方法路由、`os/exec`、`path/filepath`）、React 19 + TypeScript + Vitest + Testing Library。

**Spec:** `docs/superpowers/specs/2026-08-16-b108-reveal-in-finder-design.md`（**每个 task 开始前先读它对应的小节**）

## Global Constraints

- **不引入任何新依赖**（Go 与前端都是）。
- **错误原文必须透传到前端**，不得吞成「操作失败」——与 B107 五个端点同一条纪律。
- 所有新增/修改的 Go 拒绝分支都要打 `Warn` 且带 `status`；成功路径要打 `Info`（`instrumenting-code`：成功路径不静默）。禁止 `fmt.Printf`。
- 新文件必须有文件头注释（职责 + 边界）；导出符号必须有 doc 注释；非显然的边界条件要有中文「为什么」注释。
- **能力位三态纪律逐字沿用 `PtySupported`**：`nil` = 对端没上报（**不许当成 false**），`false` = 平台不支持，`true` = 支持。
- **前端对 `null` 的反应是放行**，不是禁用（`usePtySupport` 注释里已写死的纪律）。
- 中文文案凡 spec 里给了原文的，**逐字照抄**，不要润色。
- 提交前 `gofmt -l .` 必须为空，`go vet ./...` 干净。

---

### Task 1: 能力位 `reveal_supported` 上线

**Files:**
- Modify: `internal/proto/status.go`（`StatusResp` 结构体，`PtySupported` 字段之后）
- Modify: `internal/proto/projects.go`（`Machine` 结构体，`PtySupported` 字段之后）
- Modify: `internal/agentd/server.go:377-379`（`handleStatus` 装配）
- Modify: `internal/agentd/machines.go:93-96`（`localMachine`）与 `machines.go:134`（`fillFromStatus`）
- Modify: `internal/proto/contract_fixture_test.go`（`statusSample` 补字段）
- Test: `internal/agentd/machines_test.go`（新增用例）

**Interfaces:**
- Consumes: 无（本 task 是起点）
- Produces:
  - `proto.StatusResp.RevealSupported *bool`（JSON `reveal_supported,omitempty`）
  - `proto.Machine.RevealSupported *bool`（JSON `reveal_supported,omitempty`）
  - `agentd.revealSupportedOS bool`（包级 var，Task 2 会用同一个，本 task 先建）

**为什么两个结构体都加**（不要只加 `Machine`）：`fillFromStatus` 是远程机器能力位的唯一来源，只加 `Machine` 会让远程行永远是 `nil`——而 `nil` 在三态里的含义是「对端没上报」，那是**假话**（我们探到了，只是没搬）。

- [ ] **Step 1: 写失败用例**

在 `internal/agentd/machines_test.go` 追加：

```go
// TestLocalMachineReportsRevealSupported 断言本机探活会带上 reveal 能力位，
// 且它等于当前平台的实际支持度（不是恒 true）。
func TestLocalMachineReportsRevealSupported(t *testing.T) {
	s := newTestServer(t)
	m := s.localMachine(context.Background())
	if m.RevealSupported == nil {
		t.Fatal("本机探活没带 reveal_supported，前端三态门会退化成一律放行")
	}
	if *m.RevealSupported != revealSupportedOS {
		t.Fatalf("reveal_supported=%v，与平台实际支持度 %v 不符", *m.RevealSupported, revealSupportedOS)
	}
}

// TestFillFromStatusCarriesRevealSupported 断言远程机器的能力位被原样搬运，
// 包括 nil——探到了但对端没这个字段，结论就是「没上报」。
func TestFillFromStatusCarriesRevealSupported(t *testing.T) {
	yes := true
	var m proto.Machine
	fillFromStatus(&m, &proto.StatusResp{RevealSupported: &yes})
	if m.RevealSupported == nil || !*m.RevealSupported {
		t.Fatalf("true 没被搬运过来：%v", m.RevealSupported)
	}

	var m2 proto.Machine
	fillFromStatus(&m2, &proto.StatusResp{})
	if m2.RevealSupported != nil {
		t.Fatalf("对端没上报时应保持 nil，实际 %v", *m2.RevealSupported)
	}
}
```

> `newTestServer` 是本包既有的测试夹具；若签名不同，照本文件里其它用例的既有用法调整，**不要新造一个夹具**。

- [ ] **Step 2: 跑用例确认失败**

Run: `go test ./internal/agentd/ -run 'RevealSupported' -v`
Expected: FAIL，编译错误 `m.RevealSupported undefined` / `undefined: revealSupportedOS`

- [ ] **Step 3: 实现**

`internal/proto/status.go`，在 `PtySupported` 字段之后：

```go
	// RevealSupported 报告本机 agentd 是否支持「在访达中显示」（B108）。
	//
	// 三态与 PtySupported 逐字相同：
	//   缺席(nil) = 对端 agentd 太老，没上报这个字段——**不许当成 false**
	//   false     = 平台不支持（只有 macOS 有 `open -R` 这个语义）
	//   true      = 支持
	//
	// 注意：这只是**平台**支持度。真能不能揭示还要看调用方是不是从回环来的
	//（远程浏览器点了会在 agentd 那台机器的桌面上弹窗，没人看得见），那一层
	// 由端点自己判，不进能力位——它是每请求的属性，不是机器的属性。
	RevealSupported *bool `json:"reveal_supported,omitempty"`
```

`internal/proto/projects.go`，在 `Machine.PtySupported` 之后：

```go
	// RevealSupported 是这台机器的「在访达中显示」能力位，探活时从它的
	// StatusResp 投影而来。三态与 PtySupported 同一纪律。
	RevealSupported *bool `json:"reveal_supported,omitempty"`
```

`internal/agentd/reveal.go`（本 task 只建这一行 + 文件头，端点在 Task 2 写进同一文件）：

```go
// 本文件实现「在访达中显示」（Reveal in Finder，B108）：平台能力位与
// POST /api/workspaces/reveal 端点。
//
// 职责：
//   - 报告本平台是否支持这个动作（经 /api/status 与 /api/machines 上报）
//   - 校验「调用方在本机、路径在工作树内」后执行 `open -R`
//
// 边界：
//   - **不接 ?machine= 转发**。转发正是这个端点要拒绝的那件事——在别人的
//     机器上弹一个没人看的 Finder 窗口。理由见 spec §3.2
//   - 不做任何写操作；这是一个只读的、给人看的动作
package agentd

import "runtime"

// revealSupportedOS 是本平台是否支持「在访达中显示」。
//
// 为什么是 var 而不是 const：**唯一理由是测试缝**。写成 const 的话 false 分支
// 只有在非 macOS 机器上才跑得到，等于永远不测——与 hostguard.go 的
// nicRefreshGap / localIPsFn 同一条理由。
var revealSupportedOS = runtime.GOOS == "darwin"
```

`internal/agentd/server.go` 的 `handleStatus`，紧跟 `resp.PtySupported = &ptyOK` 之后：

```go
	revealOK := revealSupportedOS
	resp.RevealSupported = &revealOK
```

`internal/agentd/machines.go` 的 `localMachine`，紧跟 `m.PtySupported = &ptyOK` 之后：

```go
	revealOK := revealSupportedOS
	m.RevealSupported = &revealOK
```

`internal/agentd/machines.go` 的 `fillFromStatus`，紧跟 `m.PtySupported = st.PtySupported` 之后：

```go
	m.RevealSupported = st.RevealSupported
```

`internal/proto/contract_fixture_test.go` 的 `statusSample`：给 `RevealSupported` 一个 `&yes`（沿用该函数里 `PtySupported` 的写法）。

- [ ] **Step 4: 跑用例确认通过**

Run: `go test ./internal/proto/ ./internal/agentd/ -count=1`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

本 task 不新增分支，只在结构体上加字段——**不加日志**。
（`handleStatus` / `localMachine` 的既有 Info 日志已覆盖这两条路径的进入与完成；为一个字段赋值再补一行日志属于噪声。**这一条是有意的判断，不是遗漏**。）

- [ ] **Step 6: 加注释**

上面 Step 3 的每段代码都已带注释。额外补一处：`internal/agentd/server.go` 顶部
`Handler()` 的路由清单注释里，在 `GET /api/workspaces/search` 那行之后插入：

```
//   - POST /api/workspaces/reveal       在本机访达中显示工作树内条目（不支持 ?machine= 转发）
```

（路由在 Task 2 注册，但清单一次写全，免得两个 task 各改一次同一段注释。）

- [ ] **Step 7: 提交**

```bash
git add internal/proto/status.go internal/proto/projects.go internal/proto/contract_fixture_test.go internal/agentd/reveal.go internal/agentd/server.go internal/agentd/machines.go internal/agentd/machines_test.go
git commit -m "feat(b108): 能力位 reveal_supported 上线（status + machines 三态）"
```

---

### Task 2: `POST /api/workspaces/reveal` 端点

**Files:**
- Modify: `internal/agentd/reveal.go`（Task 1 建的文件，补 handler 与路径解析）
- Modify: `internal/agentd/server.go`（路由注册）
- Test: `internal/agentd/reveal_test.go`（新建）

**Interfaces:**
- Consumes: `revealSupportedOS`（Task 1）；`s.workspaceRootOrErr(w, r) (string, bool)`、`writeJSON(w, code, v)`、`s.log`（既有）
- Produces:
  - `var revealOpener func(ctx context.Context, abs string) error`（注入缝，Task 5 变异自验要用）
  - `func (s *Server) handleWorkspaceReveal(w http.ResponseWriter, r *http.Request)`

**四条拒绝的状态码（不要自行调整）：**

| 情形 | 码 | 报文 |
|---|---|---|
| `path` 不在白名单 / 缺 `path` | 400 | 由 `workspaceRootOrErr` 给 |
| 带 `machine=` | 400 | `不支持在远程机器上打开访达：那台机器的桌面前没有人` |
| 调用方非回环 | 409 | `你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上` |
| 平台不是 macOS | 501 | `这台机器的系统不支持在访达中显示（仅 macOS）` |
| `rel` 逃出工作树 | 400 | `路径逃逸被拒绝` |
| `open` 非零退出 | 500 | `open 失败: <stderr 原文>` |

**判定顺序**：`machine` → 平台 → 回环 → 白名单/路径。
把 `machine` 放最前，是因为它是**调用方用错了端点**，应该先于任何本机状态告诉他；
把路径校验放最后，是因为它最贵（要落盘 stat + EvalSymlinks）。

- [ ] **Step 1: 写失败用例**

新建 `internal/agentd/reveal_test.go`：

```go
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// revealCapture 换掉真的 open，记下收到的绝对路径。返回的 restore 必须 defer。
func revealCapture(t *testing.T) (*string, func()) {
	t.Helper()
	var got string
	prev := revealOpener
	revealOpener = func(_ context.Context, abs string) error {
		got = abs
		return nil
	}
	return &got, func() { revealOpener = prev }
}

// revealReq 造一条指向 root 的 reveal 请求。remote 为空时用回环地址。
func revealReq(root, rel, machine, remote string) *http.Request {
	u := "/api/workspaces/reveal?path=" + root + "&rel=" + rel
	if machine != "" {
		u += "&machine=" + machine
	}
	r := httptest.NewRequest(http.MethodPost, u, nil)
	if remote == "" {
		remote = "127.0.0.1:54321"
	}
	r.RemoteAddr = remote
	return r
}

func TestRevealHappyPath(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d，body=%s", w.Code, w.Body.String())
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(root, "a.txt"))
	if *got != want {
		t.Fatalf("open 收到 %q，期望 %q", *got, want)
	}
}

// TestRevealEmptyRel 断言空 rel 揭示工作树根本身——与 DeleteEntry 不同，
// 揭示根是正当操作，不能照抄「空串按非法名拒」。
func TestRevealEmptyRel(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "", "", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d，body=%s", w.Code, w.Body.String())
	}
	want, _ := filepath.EvalSymlinks(root)
	if *got != want {
		t.Fatalf("open 收到 %q，期望工作树根 %q", *got, want)
	}
}

func TestRevealRejectsMachine(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "devbox", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 %d，期望 400；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

func TestRevealRejectsNonLoopback(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", "100.73.238.21:54321"))
	if w.Code != http.StatusConflict {
		t.Fatalf("状态码 %d，期望 409；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

func TestRevealUnsupportedPlatform(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()
	prev := revealSupportedOS
	revealSupportedOS = false
	defer func() { revealSupportedOS = prev }()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", ""))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 %d，期望 501；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

func TestRevealPathEscape(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "../../etc/hosts", "", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 %d，期望 400；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("逃逸路径居然执行了 open：%q", *got)
	}
}

// TestRevealSymlinkEscape 断言工作树内的符号链接指向树外时被拒——这是
// EvalSymlinks 前缀校验存在的全部理由，纯字符串 Clean 挡不住它。
func TestRevealSymlinkEscape(t *testing.T) {
	s, root := newRevealServer(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "link.txt", "", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 %d，期望 400；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("越界符号链接居然执行了 open：%q", *got)
	}
}

// TestRevealOpenFails 断言 open 的失败原文透传，不吞成「操作失败」。
func TestRevealOpenFails(t *testing.T) {
	s, root := newRevealServer(t)
	prev := revealOpener
	revealOpener = func(context.Context, string) error {
		return errors.New("kLSNoExecutableErr")
	}
	defer func() { revealOpener = prev }()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", ""))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 %d，期望 500", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !contains(body["error"], "kLSNoExecutableErr") {
		t.Fatalf("错误原文被吞了：%q", body["error"])
	}
}
```

`newRevealServer(t)` 与 `contains` 由实现者按本包既有夹具写法补齐：
- `newRevealServer` 返回一个 `*Server` 与一个**已被白名单认可**的工作树目录（目录里有 `a.txt`）。**照抄 `workspacefiles_test.go` / `workspace_test.go` 里 B107 那批用例建工作树的既有做法**，不要新造一套。
- `contains` 若本包已有等价 helper 就直接用，否则 `strings.Contains`。

- [ ] **Step 2: 跑用例确认失败**

Run: `go test ./internal/agentd/ -run 'TestReveal' -v`
Expected: FAIL，`undefined: revealOpener` / `s.handleWorkspaceReveal undefined`

- [ ] **Step 3: 实现**

在 `internal/agentd/reveal.go` 追加（`import` 相应补 `context`/`net`/`net/http`/`os/exec`/`path/filepath`/`strings`/`time`/`bytes`）：

```go
// revealTimeout 是 `open` 的执行超时。open 只是把请求投给 Finder 就返回，
// 正常在毫秒级；5 秒是给「Finder 没在跑，要先拉起来」留的余量。
const revealTimeout = 5 * time.Second

// revealOpener 执行真正的揭示动作。是 var 而非直接调用，唯一理由是测试缝——
// 用例要断言「收到了哪个绝对路径」且不能真的弹窗。
var revealOpener = func(ctx context.Context, abs string) error {
	// 不经 sh -c：路径作为独立 argv 元素传入，路径里的空格/引号/$ 都不构成注入面
	cmd := exec.CommandContext(ctx, "open", "-R", abs)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// isLoopbackAddr 判断 RemoteAddr（"host:port"）是否来自回环。
//
// 为什么需要这一层：Host 白名单**不止回环**（回环三件套 + cfg.Listen 的 host +
// cfg.Web.AllowedHosts + 通配监听时本机网卡的非回环 IP，见 B104），所以
// 「请求没带 machine」**不等于**「人坐在这台机器前」。判不出来时返回 false
//（拒绝），不放行——放行的代价是在别人机器上弹窗，拒绝的代价只是一条错误提示。
func isLoopbackAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // 没有端口的形态（少见但不该因此放行）
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// revealTarget 把 rel 解析成一个可以交给 open 的绝对路径，并确认它没跑出工作树。
//
// 返回：解析后的绝对路径；越界或不存在时返回错误。
//
// 为什么这里用 EvalSymlinks 而不是 B107 §3.2 规定的 os.OpenRoot：
// os.Root 的产物是 **jail 内的 fd**，而这里要的是一个**交给外部进程的路径字符串**
//（`open -R /dev/fd/N` 没有意义）。所以红线在这里不适用——**这不是遗漏**。
//
// 代价是校验后、open 前存在残留 TOCTOU 窗口。可接受的依据只有这三条：
//  1. 动作是 reveal-only：不执行、不改写、不读内容，上限是「Finder 选中了另一个文件」
//  2. 利用它要先能在工作树内写符号链接，而有这能力的调用方本来就有 PTY 全权 shell
//  3. 后果发生在用户自己看得见的桌面上，不是一条静默通道
//
// **不要把这条让步反向套回 B107**：那边的动作是 RemoveAll/Rename，上限是静默
// 删掉仓库外的文件，两者不在同一个量级。
func revealTarget(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	abs, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.Clean(rel)))
	if err != nil {
		return "", err
	}
	if abs != realRoot && !strings.HasPrefix(abs, realRoot+string(filepath.Separator)) {
		return "", errors.New("路径逃逸被拒绝")
	}
	return abs, nil
}

// handleWorkspaceReveal 处理 POST /api/workspaces/reveal?path=&rel=。
//
// 在**本机**访达中显示工作树内的 rel 条目（B108）。rel 为空串表示工作树根本身
//——与删除端点不同，揭示根是正当操作。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel:  条目相对路径，可为空
//   - machine: **不支持**。带了就 400，理由见 spec §3.2
//
// 响应：200 返回 {"ok": true}。
func (s *Server) handleWorkspaceReveal(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("rel")
	machine := r.URL.Query().Get("machine")
	s.log.Info("工作树在访达中显示请求", "path", r.URL.Query().Get("path"),
		"rel", rel, "machine", machine, "remote", r.RemoteAddr)

	if machine != "" {
		s.log.Warn("在访达中显示被拒：不支持转发", "machine", machine, "status", http.StatusBadRequest)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "不支持在远程机器上打开访达：那台机器的桌面前没有人"})
		return
	}
	if !revealSupportedOS {
		s.log.Warn("在访达中显示被拒：平台不支持", "goos", runtime.GOOS, "status", http.StatusNotImplemented)
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "这台机器的系统不支持在访达中显示（仅 macOS）"})
		return
	}
	if !isLoopbackAddr(r.RemoteAddr) {
		s.log.Warn("在访达中显示被拒：调用方不在本机", "remote", r.RemoteAddr, "status", http.StatusConflict)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上"})
		return
	}
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	abs, err := revealTarget(root, rel)
	if err != nil {
		s.log.Warn("在访达中显示被拒：路径不可用", "root", root, "rel", rel,
			"status", http.StatusBadRequest, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), revealTimeout)
	defer cancel()
	if err := revealOpener(ctx, abs); err != nil {
		s.log.Error("在访达中显示失败", "abs", abs, "status", http.StatusInternalServerError, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "open 失败: " + err.Error()})
		return
	}
	s.log.Info("工作树在访达中显示完成", "root", root, "rel", rel, "abs", abs)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

`internal/agentd/server.go`，在 `GET /api/workspaces/search` 的注册行之后：

```go
	mux.HandleFunc("POST /api/workspaces/reveal", s.handleWorkspaceReveal)
```

> **注意**：这一行**不要**加 `s.forwardIfRequested`。其它 `/api/workspaces/*` 都以它开头，
> 照抄会把整条设计推翻——这里就是要拒绝转发。

- [ ] **Step 4: 跑用例确认通过**

Run: `go test ./internal/agentd/ -run 'TestReveal' -count=1 -v`
Expected: 8 条全 PASS

再跑全量：`go test ./... -count=1`，Expected: 全绿

- [ ] **Step 5: 加关键节点日志**

已在 Step 3 的代码里就位，逐条对照确认：
- 进入：`"工作树在访达中显示请求"`（带 path/rel/machine/remote）
- 四条拒绝分支各一条 `Warn`，都带 `status` 与判定依据
- `open` 失败：`Error`，带 `abs` 与 `cause`
- 成功：`"工作树在访达中显示完成"`（成功路径不静默）

- [ ] **Step 6: 加注释**

已在 Step 3 就位。**逐项确认这几处存在且未被简化掉**：
- `reveal.go` 文件头的职责/边界（含「不接 machine 转发」）
- `revealSupportedOS` 为什么是 var
- `isLoopbackAddr` 为什么需要（Host 白名单不止回环）与「判不出来时拒绝」
- `revealTarget` 那段**完整的** B107 红线不适用论证 + 三条可接受依据 + 「不要反向套回 B107」

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/reveal.go internal/agentd/reveal_test.go internal/agentd/server.go
git commit -m "feat(b108): POST /api/workspaces/reveal（拒转发/限回环/仅 macOS/路径遏制）"
```

---

### Task 3: 前端能力位与 API 客户端

**Files:**
- Modify: `web/src/api/types.ts`（`Machine` 与 status 响应类型）
- Modify: `web/src/api/client.ts`（新增 `revealInFinder`）
- Rename+Modify: `web/src/app/data/usePtySupport.ts` → `web/src/app/data/useMachineCaps.ts`
- Rename+Modify: `web/src/app/data/usePtySupport.test.ts` → `web/src/app/data/useMachineCaps.test.ts`
- Modify: `web/src/app/shell/Shell.tsx`（两处调用点改名）
- Modify: `web/src/api/contract.test.ts`（补 `reveal_supported` 断言）

**Interfaces:**
- Consumes: Task 1 的 `reveal_supported` 线格式
- Produces:
  - `revealInFinder(path: string, rel: string): Promise<{ ok: boolean }>`（**没有 machine 参数**——签名本身就表达「不支持转发」）
  - `useMachineCaps(): { pty(machine): boolean|null; reveal(machine): boolean|null; error: string }`

- [ ] **Step 1: 写失败用例**

把 `usePtySupport.test.ts` 改名为 `useMachineCaps.test.ts`，既有用例改调 `useMachineCaps().pty(...)`，并追加：

```ts
it('reveal 能力位与 pty 各自独立，一次请求两张表', async () => {
  fetchMachines.mockResolvedValue({
    machines: [
      { name: '', pty_supported: true, reveal_supported: false },
      { name: 'devbox', pty_supported: true },
    ],
  })
  const { result } = renderHook(() => useMachineCaps())
  await waitFor(() => expect(result.current.reveal('')).toBe(false))
  expect(result.current.pty('')).toBe(true)
  // devbox 没上报 reveal → null（**不是 false**）
  expect(result.current.reveal('devbox')).toBeNull()
  expect(fetchMachines).toHaveBeenCalledTimes(1)
})
```

> 上面的 mock 形状请对齐既有用例里 `fetchMachines` 的用法（同文件已有），
> `renderHook`/`waitFor` 也照既有 import。

- [ ] **Step 2: 跑用例确认失败**

Run: `cd web && npx vitest run src/app/data/useMachineCaps.test.ts`
Expected: FAIL，`useMachineCaps is not defined`

- [ ] **Step 3: 实现**

`web/src/api/types.ts`，`Machine` 接口里 `pty_supported` 之后：

```ts
  // reveal_supported 三态同 pty_supported：缺席/null = 对端没上报（**不是**不支持）。
  // 注意它只是**平台**支持度——真能不能揭示还要看浏览器是不是和 agentd 在同一台
  // 机器上，那一层由 FileTree 用 location.hostname 判（spec §4.3）。
  reveal_supported?: boolean | null
```

status 响应类型（`pty_supported?: boolean` 那个接口）里同样补一行 `reveal_supported?: boolean`。

`web/src/api/client.ts`，`searchWorkspace` 之后：

```ts
// revealInFinder 在**本机**访达中显示工作树内 rel 条目（POST /api/workspaces/reveal）。
// rel 可为空串（揭示工作树根）。
//
// **故意没有 machine 参数**：远程条目不可能在本机访达里打开，端点对 ?machine=
// 一律 400。签名不给这个参数，就没有人能不小心传它。
export function revealInFinder(path: string, rel: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(
    `/api/workspaces/reveal?${workspaceQuery(path, rel)}`,
    { method: 'POST' },
  )
}
```

> `workspaceQuery(path, rel, machine?)` 的第三参可省——确认省略时它不会往查询串里
> 塞 `machine=`（若会，改成显式传 `undefined` 或补一个不带 machine 的分支）。

`useMachineCaps.ts`：由 `usePtySupport.ts` 改名而来，保留其**全部**既有注释与两条纪律
（**不轮询**；`cancelledRef`/`ranRef` 跨 effect run）。改动只有三处：
- 状态从一张表变成两张（`pty` / `reveal`），仍然只发**一次** `fetchMachines`
- 导出接口改名为 `MachineCaps`，两个查询函数 `pty(machine)` / `reveal(machine)`
- 文件头注释把「PTY 能力位」改成「每台机器的能力位（PTY / 在访达中显示）」，
  并保留「三态是这个 hook 存在的全部理由」那段

`Shell.tsx`：`const ptySupport = usePtySupport()` → `const caps = useMachineCaps()`；
`ptySupport.supported(machine)` → `caps.pty(machine)`（两处：`:109` 与 `:345`）。

`contract.test.ts`：在 `StatusResp：pty_supported 已上报` 那条旁边补一条
`reveal_supported 已上报`，断言 `status.reveal_supported` 为 `true`。

- [ ] **Step 4: 跑用例确认通过**

Run: `cd web && npx vitest run && npx tsc -b --force`
Expected: 全绿、0 error

- [ ] **Step 5: 加关键节点日志**

前端沿用既有做法：只有 `fetchMachines` 失败时 `console.warn`（原 hook 已有那行，
文案里的「PTY 三态门」改成「能力位三态门」）。**不新增其它日志**——这一层没有
新的失败模式，加了只是噪声。这是有意的判断。

- [ ] **Step 6: 加注释**

已在 Step 3 就位。确认 `revealInFinder` 的「故意没有 machine 参数」那段在，
以及 `useMachineCaps` 的文件头改写后仍保留原有的两条纪律说明。

- [ ] **Step 7: 提交**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/contract.test.ts web/src/app/data web/src/app/shell/Shell.tsx
git commit -m "feat(b108): 前端能力位 reveal_supported + usePtySupport 泛化为 useMachineCaps"
```

---

### Task 4: FileTree 的 `Reveal in Finder` 四分支

**Files:**
- Modify: `web/src/app/files/FileTree.tsx`（文件头注释、props、`revealReason`、菜单项）
- Modify: `web/src/app/shell/Shell.tsx`（给 FileTree 传能力位）
- Test: `web/src/app/files/FileTree.test.tsx`

**Interfaces:**
- Consumes: `revealInFinder`、`useMachineCaps`（Task 3）
- Produces: `FileTreeProps.revealSupported: boolean | null`（由 Shell 传 `caps.reveal('')`）

**判定顺序与文案（spec §4.3，逐字）：**

| 顺序 | 条件 | 结果 | 理由 |
|---|---|---|---|
| 1 | `base.machine !== ''` | 灰 | `远程目录无法在本机的访达中打开（machine: ${base.machine}）` |
| 2 | `location.hostname` 非回环 | 灰 | `你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上` |
| 3 | `revealSupported === false` | 灰 | `这台机器的系统不支持在访达中显示（仅 macOS）` |
| 4 | 其余（**含 null**） | 可点 | — |

第 1 条的文案**是现有的，逐字保留**（`FileTree.test.tsx:193` 已在断言它）。

- [ ] **Step 1: 写失败用例**

`FileTree.test.tsx`：把既有的 `Reveal in Finder 恒置灰` / `本机右键时 Reveal in Finder 说「暂未实现」`
两条替换成四条：

```tsx
it('远程目录：Reveal in Finder 置灰，理由点名 machine', async () => {
  // 沿用本文件既有的渲染 helper，base.machine 传 'devbox'
  // 断言 disabledReason 为 '远程目录无法在本机的访达中打开（machine: devbox）'
})

it('通过网络访问 agentd：置灰，理由说访达会开在 agentd 那台机器上', async () => {
  // 把 location.hostname stub 成 '100.73.238.21'，base.machine 传 ''
  // 断言 disabledReason 为 '你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上'
})

it('平台不支持：置灰，理由说仅 macOS', async () => {
  // location.hostname = 'localhost'，revealSupported={false}
  // 断言 disabledReason 为 '这台机器的系统不支持在访达中显示（仅 macOS）'
})

it('本机 + macOS：可点，点了调 revealInFinder', async () => {
  // location.hostname = 'localhost'，revealSupported={true}
  // 断言菜单项未 disabled；点击后 revealInFinder 以 (base.path, entry.rel) 被调用一次
})

it('能力位为 null 时照常放行（三态门不禁用）', async () => {
  // revealSupported={null} → 未 disabled
})

it('reveal 失败时把服务端原文透传到面板', async () => {
  // revealInFinder mock reject 一个带中文原文的错误
  // 断言该原文出现在 DOM 里（走既有 opError 面板那条路）
})
```

实现者按本文件既有的渲染 helper / mock 写法补齐骨架里的注释部分。
`location.hostname` 的 stub 方式对齐本仓库既有做法；没有先例就用
`vi.spyOn(window, 'location', 'get')` 或 `Object.defineProperty`，**并在用例里
`afterEach` 还原**。

- [ ] **Step 2: 跑用例确认失败**

Run: `cd web && npx vitest run src/app/files/FileTree.test.tsx`
Expected: FAIL（`revealSupported` prop 不存在 / 文案不符）

- [ ] **Step 3: 实现**

`FileTree.tsx` 文件头，把现有那段：

```
//   - Reveal in Finder 恒置灰：本机与远程都灰。为什么本机也灰——本期不做
//     任何一半形态（只做本机那半会留下「本机能点远程不能点」的割裂），且
//     它依赖 B108 尚未裁决的 Electron 去留前提
```

替换为：

```
//   - Reveal in Finder 只在「浏览器与 agentd 同在一台 macOS 上」时可点，
//     其余三种情形各给一条不同的置灰理由（B108，spec §4.3）。远程那条是
//     结构性的：在 mac-02 上 open -R 会在**没人看着**的桌面上弹窗
```

props 加一项：

```tsx
  // revealSupported 是本机 agentd 的「在访达中显示」平台能力位，三态。
  // null = 对端没上报，此时**放行**而不是禁用（见 useMachineCaps 的三态纪律）。
  revealSupported: boolean | null
```

`revealReason` 改写为按顺序判的四分支：

```tsx
  // revealReason 返回 Reveal in Finder 的置灰理由，空串表示可点。
  // 三条互斥的理由按代价从低到高判：machine 是纯前端已知，hostname 也是，
  // 平台位要等 /api/machines 回来——最后判它，免得能力表还没到就先灰一下再亮。
  const revealReason = (() => {
    if (base.machine) return `远程目录无法在本机的访达中打开（machine: ${base.machine}）`
    // Host 白名单不止回环（B104 会把本机网卡 IP 也放进来），所以「不是远程目录」
    // 不等于「浏览器和 agentd 在同一台机器上」。这里用页面自己的 host 判。
    if (!isLoopbackHost(window.location.hostname)) {
      return '你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上'
    }
    if (revealSupported === false) return '这台机器的系统不支持在访达中显示（仅 macOS）'
    return ''
  })()
```

模块级加：

```tsx
// isLoopbackHost 判断页面自己是不是从回环加载的。
//
// 已知不解的边缘：SSH 端口转发下 localhost 指向远程 agentd，这里会误判为本机，
// Finder 开在远程桌面。无解（隧道在设计上就是要让远程看起来像本地），后果也
// 有限——弹一个窗，而且是用户自己搭的隧道。如实记着，不假装挡住了。
function isLoopbackHost(host: string): boolean {
  return host === 'localhost' || host === '127.0.0.1' || host === '::1' || host === '[::1]'
}
```

菜单项：

```tsx
      {
        label: 'Reveal in Finder',
        onSelect: () => void revealEntry(entry.rel),
        disabled: revealReason !== '',
        disabledReason: revealReason,
      },
```

新增动作（放在 `copyEntry` 附近，沿用同一条错误处置路径）：

```tsx
  // revealEntry 在本机访达中显示条目。失败原文透传到 opError 面板——服务端
  // 的四条拒绝理由都写得比前端能猜的准，不要吞成「操作失败」。
  const revealEntry = async (rel: string) => {
    setMenu(null)
    try {
      await revealInFinder(base.path, rel)
    } catch (err) {
      setOpError(errorMessage(err))
    }
  }
```

`Shell.tsx`：给 `<FileTree ... revealSupported={caps.reveal('')} />`。

- [ ] **Step 4: 跑用例确认通过**

Run: `cd web && npx vitest run && npx tsc -b --force && npx vite build`
Expected: 全绿、0 error、build 通过

- [ ] **Step 5: 加关键节点日志**

前端这一层不加日志：失败已经进 `opError` 面板给用户看，比 console 更有用；
成功是一个用户当场就看见结果的动作（Finder 弹出来了）。**有意不加。**

- [ ] **Step 6: 加注释**

已在 Step 3 就位。确认三处：文件头那段改写、`revealReason` 的判定顺序注释、
`isLoopbackHost` 的「已知不解的边缘」。

- [ ] **Step 7: 提交**

```bash
git add web/src/app/files/FileTree.tsx web/src/app/files/FileTree.test.tsx web/src/app/shell/Shell.tsx
git commit -m "feat(b108): FileTree 的 Reveal in Finder 四分支（可点/三条置灰理由）"
```

---

### Task 5: 变异自验与 ledger

**Files:**
- Create: `docs/superpowers/notes/2026-08-16-b108-ledger.md`

**Interfaces:**
- Consumes: Task 1–4 的全部产出
- Produces: ledger 文件（审核者复验的起点）

本 task **不改产品代码**。它的产出是「用例真的咬住了」的证据。

- [ ] **Step 1: 逐条跑变异，记录 RED/GREEN**

四条变异，每条：改 → 跑 → 记结果 → **还原** → 再跑确认回绿。

| # | 变异 | 期望 |
|---|---|---|
| 1 | `handleWorkspaceReveal` 里删掉 `if machine != ""` 整块 | 至少 `TestRevealRejectsMachine` 红 |
| 2 | 删掉 `if !isLoopbackAddr(...)` 整块 | 至少 `TestRevealRejectsNonLoopback` 红 |
| 3 | `revealTarget` 里删掉 `HasPrefix` 越界判断 | 至少 `TestRevealSymlinkEscape` 红 |
| 4 | `revealSupportedOS` 改成 `= true`（恒真） | 至少 `TestRevealUnsupportedPlatform` 红 |

**四条里任何一条变异后仍全绿，就是用例没咬住——补用例，不要改结论，也不要把
这一条记成「符合预期」。**

变异 4 的注意点：它是 `var`，测试里也会改它。确认变异版本下 `TestRevealUnsupportedPlatform`
里那句 `revealSupportedOS = false` 之后**仍然**能触发 501——若不能（说明实现读的
不是这个 var），那就是实现有问题，不是用例有问题。

- [ ] **Step 2: 跑全量回归**

```bash
go build ./... && go vet ./... && gofmt -l . && go test -count=1 ./...
cd web && npx vitest run && npx tsc -b --force && npx vite build
```

把 Go 包数与「ok/FAIL」计数、前端用例数**原样**记进 ledger。
`go test` 的输出**写进文件再统计**，不要在管道里跑两遍分别 grep。

- [ ] **Step 3: 写 ledger**

`docs/superpowers/notes/2026-08-16-b108-ledger.md` 必须含：
- 每个 task 的完成时间与 commit 范围
- 每轮审查/修复的结论（含被搁置的 minor）
- Step 1 四条变异的**实际**输出（哪条用例红了、报错原文摘要）
- Step 2 的全量回归数字
- **实现者没做的事**：`open -R` 的真机弹窗从未被验证过（注入缝换掉了它），
  这一条留给审核者的真机复验（spec §7 第 1/2 条）。如实写，不要让 ledger
  读起来像「全都验过了」

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/notes/2026-08-16-b108-ledger.md
git commit -m "docs(b108): ledger 记四条变异自验与全量回归"
```

---

## Self-Review（写 plan 时已跑，结论留档）

**1. Spec 覆盖**：§3.1→T2、§3.2→T2、§3.3→T2+T4、§3.4→T1+T2、§3.5→T2、§3.6→T2、
§3.7→T2 Step 5、§4.1→T1+T3、§4.2→T3、§4.3→T4、§5.1→T2、§5.2→T4、§5.3→T5、
§6（不做的两项）→ 全 plan 无对应 task，**这是对的**、§7 → 审核者做，不进 plan。
无缺口。

**2. 占位符扫描**：无 TBD/TODO。Task 3/4 的用例骨架里留了「按既有 helper 补齐」的
指示——这是**有意的**：那些 helper 的确切签名只有在文件里才看得到，写死一个猜的
签名比让实现者照抄既有写法更容易出错。每处都指明了照抄哪个既有文件。

**3. 类型一致**：`revealSupportedOS`（T1 建、T2 用）、`revealOpener`（T2 建、T5 变异用）、
`revealInFinder(path, rel)`（T3 建、T4 用）、`useMachineCaps().reveal(machine)`（T3 建、
T4 经 Shell 用）、`FileTreeProps.revealSupported`（T4 建、Shell 传）——签名逐一对齐。
