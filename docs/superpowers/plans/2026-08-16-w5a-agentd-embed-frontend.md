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

# W5a：agentd 用 go:embed 托管前端 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 agentd 自己伺服 Web 控制台——`/console?ticket=…` 换完 cookie 302 到 `/` 之后能真正打开控制台，不再依赖 Vite dev server。

**Architecture:** 新增 `internal/webui` 包，用 build tag `embedweb` 切换「真实 embed」与「诚实 stub」两份实现，构建产物一律 gitignore（不提交占位、不提交产物）。agentd 在内层 mux（`s.auth` 之后）挂一个 SPA handler：命中真实文件就伺服，否则回落 `index.html`，但 `/api/*` 永不回落 HTML。release 流水线在需要 embed 的构建步骤前插入前端构建。

**Tech Stack:** Go 1.26.1（`io/fs`、`embed`、Go 1.22+ `ServeMux` 精确前缀优先）、`testing/fstest`（测试用假文件系统）、Node + Vite（前端构建）。

**Spec:** [W5 设计](../specs/2026-08-16-w5-embed-and-desktop-shell-design.md) —— 本 plan 覆盖其 §3、§6.1 与 §9「交付物①」。

## Global Constraints

- **构建标签是 `embedweb`**（精确值，release 流水线与代码必须一致）。
- **不提交任何构建产物或占位文件到仓库。** `web/dist/` 已被 `web/.gitignore:11` 忽略；本 plan 需新增忽略 `internal/webui/dist/`。理由：一跑 `npm run build` 工作区就脏，而 handoff 自己的 `dispatch` 硬要求工作区干净。
- **不带 `embedweb` 标签时 `go build ./...` 与 `go test ./...` 必须全绿。** 这是本 plan 的第一红线——`go:embed` 指向不存在的目录是编译期错误，任何让默认构建依赖 `dist/` 存在的写法都是错的。
- **`/api/*` 未命中绝不回落 HTML**，必须维持原有 JSON 错误。
- **SPA handler 挂内层 `mux`（`s.auth` 之后），不挂 `root`。** `/console` 仍是唯一免鉴权入口。
- 日志一律用 `s.log`（`*slog.Logger`），**禁止 `fmt.Printf`**。
- 新文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释。
- 前端构建命令是 `npm ci` + `npm run build`（后者 = `tsc -b && vite build`），产物在 `web/dist/`。

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/webui/webui.go` | 包文档 + 对外唯一入口 `FS()` 的声明位置说明。不含实现。 |
| `internal/webui/embed.go` | `//go:build embedweb`。`//go:embed all:dist` 真实产物。 |
| `internal/webui/stub.go` | `//go:build !embedweb`。返回只含一页诚实说明的 `fs.FS`。 |
| `internal/webui/webui_test.go` | 默认（无标签）下的行为：stub 可用、含 index.html、内容诚实。 |
| `internal/webui/embed_test.go` | `//go:build embedweb`。只在 release 构建路径跑：产物非空、含 index.html。 |
| `internal/agentd/webhandler.go` | SPA handler：文件命中 / 回落 / 缓存头。纯函数式，不碰 Server 状态。 |
| `internal/agentd/webhandler_test.go` | 用 `fstest.MapFS` 覆盖全部分支，不依赖真实产物。 |
| `internal/agentd/server.go` | 修改：注册 SPA handler；启动日志报告前端是否嵌入。 |
| `internal/agentd/auth.go` 或 `server.go` | 修改：401 对 HTML 请求返回说明页。 |
| `.github/workflows/release.yml` | 修改：插入前端构建，`-tags embedweb`。 |
| `.gitignore` | 修改：新增 `internal/webui/dist/`。 |
| `web/.nvmrc` | 新增：钉死 Node 版本，本地与 CI 共用同一来源（见 Task 5）。 |
| `scripts/build-release-local.sh` | 新增：本地等价构建，用于验证 Task 5 不靠「推到 CI 试试看」。 |

---

### Task 1: `internal/webui` 包与 build tag 双实现

**Files:**
- Create: `internal/webui/webui.go`
- Create: `internal/webui/embed.go`
- Create: `internal/webui/stub.go`
- Create: `internal/webui/webui_test.go`
- Create: `internal/webui/embed_test.go`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: 无（本 plan 的第一个 task）
- Produces: `webui.FS() fs.FS` —— 返回控制台静态资源的根文件系统，其中 `index.html` 必定存在。`webui.Embedded() bool` —— 报告当前二进制是否嵌入了真实产物（供启动日志与 Task 3 使用）。

- [ ] **Step 1: 写失败测试**

`internal/webui/webui_test.go`：

```go
package webui

import (
	"io/fs"
	"strings"
	"testing"
)

// 默认构建（不带 embedweb）下必须是 stub，且 stub 必须诚实——
// 不能是空白页，也不能假装成正常控制台。
func TestStubFSHasHonestIndex(t *testing.T) {
	if Embedded() {
		t.Fatal("默认构建不应报告已嵌入前端；本测试只在无 embedweb 标签时有意义")
	}
	b, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("stub 必须提供 index.html：%v", err)
	}
	body := string(b)
	for _, want := range []string{"未嵌入", "npm run dev"} {
		if !strings.Contains(body, want) {
			t.Errorf("stub 说明页缺少关键信息 %q，实际内容：%s", want, body)
		}
	}
}

// FS() 必须永远可用——调用方不该需要判空。
func TestFSNeverNil(t *testing.T) {
	if FS() == nil {
		t.Fatal("FS() 返回 nil，调用方将无法区分「没有产物」与「包坏了」")
	}
}
```

`internal/webui/embed_test.go`：

```go
//go:build embedweb

package webui

import (
	"io/fs"
	"testing"
)

// 只在 release 构建路径（-tags embedweb）跑：确认产物真的进来了。
// 这道门存在的理由：go:embed 一个只有 index.html 的空壳目录也能编译通过，
// 光「编译过了」不代表前端资源在里面。
func TestEmbeddedFSHasRealAssets(t *testing.T) {
	if !Embedded() {
		t.Fatal("带 embedweb 标签时 Embedded() 必须为 true")
	}
	if _, err := fs.ReadFile(FS(), "index.html"); err != nil {
		t.Fatalf("嵌入产物缺 index.html：%v", err)
	}
	n := 0
	if err := fs.WalkDir(FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("遍历嵌入产物失败：%v", err)
	}
	// vite 产物至少是 index.html + 一个 JS + 一个 CSS
	if n < 3 {
		t.Errorf("嵌入产物只有 %d 个文件，疑似嵌进了空壳目录", n)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/webui/...`
Expected: FAIL，编译错误 `undefined: Embedded` / `undefined: FS`

- [ ] **Step 3: 写实现**

`internal/webui/webui.go`：

```go
// Package webui 持有 Web 控制台的静态资源，并按构建标签在两种形态间切换。
//
// 职责：
//   - 对外提供唯一入口 FS()，返回控制台静态资源的根文件系统。
//   - 通过 Embedded() 报告当前二进制是否嵌入了真实构建产物。
//
// 边界：
//   - 不做 HTTP 伺服、不判断路由、不设缓存头——那些是 internal/agentd 的事。
//   - 不负责生成产物：产物由 `npm run build` 产出到 web/dist/，再由 release
//     流水线拷进本包的 dist/ 目录。本包只负责嵌入。
//
// 为什么要两份实现：go:embed 指向不存在的目录是**编译期错误**。若无条件
// embed，任何没有先跑前端构建的机器上 `go build ./...` 与 `go test ./...`
// 都会整片失败。而把产物或占位文件提交进仓库会让 `npm run build` 之后工作区
// 变脏，与 handoff 自己「dispatch 要求工作区干净」的硬约束冲突。故用构建标签
// embedweb 分开：默认不嵌，release 才嵌。
package webui
```

`internal/webui/stub.go`：

```go
//go:build !embedweb

package webui

import (
	"io/fs"
	"testing/fstest"
)

// stubIndex 是未嵌入前端时伺服的说明页。
//
// 它必须**诚实**：既不能是空白页（用户会以为服务坏了），也不能假装成正常
// 控制台（用户会以为前端有 bug）。把真实原因和两条出路直接写在页面上，
// 是这里唯一不会把人引向错误排查方向的做法。
const stubIndex = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>handoff：前端未嵌入</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.7">
<h1>此二进制未嵌入前端构建产物</h1>
<p>agentd 本身工作正常，只是这份二进制是用默认标签构建的，不含 Web 控制台。</p>
<h2>两条出路</h2>
<ul>
<li>用 Release 版二进制（release 流水线以 <code>-tags embedweb</code> 构建，含前端）。</li>
<li>开发时在 <code>web/</code> 下跑 <code>npm run dev</code>，用 Vite dev server 访问控制台。</li>
</ul>
</body></html>
`

// stubFS 只含一个 index.html。用 fstest.MapFS 而不是自己实现 fs.FS：
// 它是标准库里现成的只读内存实现，语义与 embed.FS 一致。
var stubFS fs.FS = fstest.MapFS{
	"index.html": &fstest.MapFile{Data: []byte(stubIndex)},
}

// FS 返回控制台静态资源的根文件系统。永不返回 nil。
//
// 默认构建下返回只含一页说明的 stub，见 stubIndex。
func FS() fs.FS { return stubFS }

// Embedded 报告当前二进制是否嵌入了真实的前端构建产物。
//
// 默认构建下恒为 false。调用方（如 agentd 启动日志）应据此告诉运维
// 「这份二进制有没有前端」——否则前端打不开时无从判断是构建问题还是运行问题。
func Embedded() bool { return false }
```

`internal/webui/embed.go`：

```go
//go:build embedweb

package webui

import (
	"embed"
	"io/fs"
)

// distFS 是 release 构建时嵌入的前端产物。
//
// all: 前缀是必须的：vite 产物里可能有以 . 或 _ 开头的文件，
// 不加 all: 时 go:embed 会静默跳过它们，症状是页面缺资源而构建全绿。
//
//go:embed all:dist
var distFS embed.FS

// FS 返回控制台静态资源的根文件系统。永不返回 nil。
//
// release 构建下返回嵌入产物的 dist 子树（即 index.html 位于根）。
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// 只有 dist 目录不存在才会走到这里，而那种情况 go:embed 在编译期
		// 就会失败。这里 panic 是为了让「不可能发生」真的发不出去，
		// 而不是静默返回一个空 FS 让页面 404。
		panic("webui: 嵌入产物缺少 dist 子目录: " + err.Error())
	}
	return sub
}

// Embedded 报告当前二进制是否嵌入了真实的前端构建产物。
//
// release 构建下恒为 true。
func Embedded() bool { return true }
```

`.gitignore` 追加：

```
internal/webui/dist/
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/webui/...`
Expected: PASS（2 个用例）

Run: `go build ./... && go vet ./... && gofmt -l internal/webui`
Expected: 全部无输出

- [ ] **Step 5: 加关键节点日志**

本 task 是纯数据包，**没有 I/O、没有错误分支、没有状态变更**，按 instrumenting-code 的适用边界属于不需要日志的一类。前端是否嵌入这一事实的**日志在 Task 3 的 agentd 启动路径上打**（那里才有 logger）。

本步骤的实际动作：确认 `internal/webui` 里没有任何 `fmt.Printf` / `println`，且 `FS()` 的 panic 分支带了原因字符串（已在 Step 3 中写入）。

- [ ] **Step 6: 加注释**

确认三个文件均已具备（Step 3 的代码里已包含，本步骤是自检）：
- `webui.go` 的 package 注释写明职责、边界、**以及为什么要两份实现**
- `stub.go` 的 `stubIndex` 注释解释「为什么必须诚实」
- `embed.go` 的 `//go:embed all:dist` 上方解释 `all:` 前缀为何必须
- `FS()` / `Embedded()` 各有 doc 注释，说明返回值与调用方该拿它做什么

- [ ] **Step 7: 提交**

```bash
git add internal/webui .gitignore
git commit -m "feat(webui): 按 build tag 切换前端 embed 与诚实 stub

go:embed 指向不存在的目录是编译期错误，无条件 embed 会让任何没先跑
前端构建的机器上 go build/go test 整片失败；而提交产物或占位文件会让
npm run build 之后工作区变脏，与 dispatch 要求工作区干净冲突。
故用 embedweb 标签分开：默认 stub，release 才嵌。"
```

---

### Task 2: SPA handler（纯逻辑，用假文件系统测全分支）

**Files:**
- Create: `internal/agentd/webhandler.go`
- Create: `internal/agentd/webhandler_test.go`

**Interfaces:**
- Consumes: `webui.FS() fs.FS`（Task 1）
- Produces: `newSPAHandler(fsys fs.FS, log *slog.Logger) http.Handler` —— 返回一个 handler，命中真实文件就伺服，否则回落 `index.html`。**不处理 `/api`**，那由路由层保证（Task 3）。

- [ ] **Step 1: 写失败测试**

`internal/agentd/webhandler_test.go`：

```go
package agentd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// spaTestFS 模拟一份 vite 产物：index.html + 一个带 hash 的 JS + 一个静态图。
// 用 fstest.MapFS 而不是真实 dist：测试不能依赖「先跑过 npm run build」。
func spaTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte("<!doctype html><div id=root></div>")},
		"assets/app-a1b2c3d4.js":  &fstest.MapFile{Data: []byte("console.log(1)")},
		"assets/app-a1b2c3d4.css": &fstest.MapFile{Data: []byte("body{}")},
		"favicon.svg":             &fstest.MapFile{Data: []byte("<svg/>")},
	}
}

func spaGet(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

// 命中真实文件时必须原样伺服，不能回落。
func TestSPAServesRealFile(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	resp := spaGet(t, h, "/assets/app-a1b2c3d4.js")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，want 200", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "console.log(1)" {
		t.Errorf("内容 = %q，want 真实文件内容", b)
	}
}

// 深链接（客户端路由）必须回落 index.html，否则刷新页面就 404。
func TestSPAFallsBackToIndexForDeepLink(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	for _, path := range []string{"/", "/tasks", "/tasks/abc-123", "/projects/x/machines/y"} {
		resp := spaGet(t, h, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s 状态码 = %d，want 200", path, resp.StatusCode)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		if string(b) != "<!doctype html><div id=root></div>" {
			t.Errorf("%s 未回落到 index.html，实际 = %q", path, b)
		}
	}
}

// index.html 必须 no-cache：否则换版后浏览器拿着旧 index 去引用
// 已经不存在的 hash 资源，表现为白屏，且用户清缓存前无法自愈。
func TestSPAIndexIsNoCache(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	for _, path := range []string{"/", "/tasks/abc"} {
		got := spaGet(t, h, path).Header.Get("Cache-Control")
		if got != "no-cache" {
			t.Errorf("%s 的 Cache-Control = %q，want no-cache", path, got)
		}
	}
}

// 带 hash 的资源可以长缓存：文件名变了内容才变，这是 vite 的产物契约。
func TestSPAHashedAssetIsImmutable(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	got := spaGet(t, h, "/assets/app-a1b2c3d4.js").Header.Get("Cache-Control")
	if got != "public, max-age=31536000, immutable" {
		t.Errorf("hash 资源的 Cache-Control = %q，want 长缓存", got)
	}
}

// 不带 hash 的静态文件不能长缓存——它换了内容名字不变。
func TestSPAUnhashedAssetIsNotImmutable(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	got := spaGet(t, h, "/favicon.svg").Header.Get("Cache-Control")
	if got == "public, max-age=31536000, immutable" {
		t.Errorf("favicon.svg 不带 hash，不该被长缓存")
	}
}

// 非 GET/HEAD 不该被 SPA 吞掉——那多半是打错了路由的写请求，
// 回落一个 200 的 HTML 会让调用方以为成功了。
func TestSPARejectsNonGet(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tasks", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 状态码 = %d，want 405", rec.Code)
	}
}

// 目录穿越必须被拒。fs.FS 本身不接受 .. ，但回落逻辑不能把它变成 200。
func TestSPARejectsTraversal(t *testing.T) {
	h := newSPAHandler(spaTestFS(), testLogger(t))
	resp := spaGet(t, h, "/../../etc/passwd")
	b, _ := io.ReadAll(resp.Body)
	if string(b) == "console.log(1)" {
		t.Fatal("穿越请求拿到了真实文件")
	}
}

// index.html 缺失是 stub 都不该出现的状态，但一旦出现必须是 500 而不是空 200：
// 空 200 会让浏览器显示白页，运维完全看不出发生了什么。
func TestSPAMissingIndexIs500(t *testing.T) {
	h := newSPAHandler(fstest.MapFS{}, testLogger(t))
	resp := spaGet(t, h, "/")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("index 缺失时状态码 = %d，want 500", resp.StatusCode)
	}
}
```

> **测试脚手架的既有事实（已核实，照用，不要新造）**：
>
> - 测试文件必须声明 `package agentd`（**内部**测试包）——`newSPAHandler` 未导出，放进 `agentd_test` 编译不过。
> - `testLogger(t *testing.T) *slog.Logger` 定义在 `internal/agentd/watchdog_fence_test.go:152`，**带 `t` 参数**。
> - 注意 `internal/agentd` 下同时存在两套 env 脚手架，**别拿错**：`newTestEnv` / `testEnv` 在 `server_test.go`，属 `package agentd_test`，本 plan **用不到**；内部包用的是 `newTestAgentdEnv` 与 `newHostTestEnv`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestSPA' -v`
Expected: FAIL，编译错误 `undefined: newSPAHandler`

- [ ] **Step 3: 写实现**

`internal/agentd/webhandler.go`：

```go
// 本文件持有 Web 控制台静态资源的伺服逻辑。
//
// 职责：
//   - 把 internal/webui 提供的 fs.FS 伺服出去：命中真实文件就发文件，
//     未命中就回落 index.html（客户端路由的深链接需要这个）。
//   - 按「文件名是否带 hash」决定缓存策略。
//
// 边界：
//   - **不处理 /api、/ws、/console**。那三条由路由层用更精确的模式抢走
//     （Go 1.22 ServeMux 精确前缀优先），本 handler 只兜未知路径。
//     这个边界是承重的：若 /api 未命中回落成 HTML，前端会把 HTML 当 JSON
//     解析，报错信息与真实原因完全无关，排查成本极高。
//   - 不做鉴权。本 handler 挂在 s.auth 之内，到这里的请求已经通过鉴权。
package agentd

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// hashedAsset 匹配 vite 产物的带 hash 文件名，如 app-a1b2c3d4.js。
//
// 为什么用文件名判断而不是按目录：vite 的 assets/ 目录里既有带 hash 的
// 构建产物，也可能有原样拷贝的 public/ 静态文件。按目录会把后者也长缓存，
// 而它们换了内容名字不变，用户会拿着一年不过期的旧文件。
var hashedAsset = regexp.MustCompile(`-[0-9a-zA-Z_]{8,}\.[0-9a-z]+$`)

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNone      = "no-cache"
)

// newSPAHandler 返回伺服单页应用的 handler。
//
// 参数：
//   - fsys: 静态资源根文件系统，index.html 必须位于根。通常来自 webui.FS()。
//   - log:  日志器，用于记录回落与异常。不可为 nil。
//
// 行为：
//   - GET/HEAD 命中真实文件 → 200 + 文件内容 + 按 hash 决定的缓存头
//   - GET/HEAD 未命中 → 200 + index.html + no-cache（客户端路由接管）
//   - 其它方法 → 405
//   - index.html 本身缺失 → 500
//
// 注意：本 handler 假定调用方已保证 /api、/ws、/console 不会落到这里。
func newSPAHandler(fsys fs.FS, log *slog.Logger) http.Handler {
	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// 非读方法落到 SPA，多半是路由写错了。回落 200 HTML 会让调用方
			// 误以为写成功，所以这里明确拒绝。
			log.Warn("SPA handler 收到非读请求，已拒绝",
				"method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			serveIndex(w, r, fsys, log)
			return
		}

		// fs.ValidPath 会挡掉 ..、绝对路径等非法形态；不合法直接当未命中走回落，
		// 而不是报错——攻击者不该从错误码里读出目录结构的任何信息。
		if !fs.ValidPath(name) {
			log.Debug("SPA 收到非法路径，按未命中处理", "path", r.URL.Path)
			serveIndex(w, r, fsys, log)
			return
		}

		info, err := fs.Stat(fsys, name)
		if err != nil || info.IsDir() {
			// 未命中真实文件 = 客户端路由的深链接，回落 index.html。
			log.Debug("SPA 未命中静态文件，回落 index.html", "path", r.URL.Path)
			serveIndex(w, r, fsys, log)
			return
		}

		if hashedAsset.MatchString(name) {
			w.Header().Set("Cache-Control", cacheImmutable)
		} else {
			w.Header().Set("Cache-Control", cacheNone)
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndex 发送 index.html，并显式设置 no-cache。
//
// 为什么 index.html 必须 no-cache：它引用的 JS/CSS 文件名带 hash，换版后
// hash 会变。若 index.html 被缓存，浏览器会拿着旧 index 去请求已经不存在的
// 资源，表现为白屏，且在用户手工清缓存之前无法自愈。
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS, log *slog.Logger) {
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		// 这是「不该发生」的状态：stub 和真实产物都必定含 index.html。
		// 但一旦发生，必须响亮地失败——空 200 只会让浏览器显示白页，
		// 运维从现象上完全看不出根因。
		log.Error("控制台 index.html 缺失，这份二进制的前端资源不完整",
			"path", r.URL.Path, "err", err)
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "console assets missing", http.StatusInternalServerError)
			return
		}
		http.Error(w, "console assets unreadable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cacheNone)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(b); err != nil {
		log.Debug("写 index.html 响应失败（客户端多半已断开）", "err", err)
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestSPA' -v`
Expected: PASS（8 个用例）

Run: `go test ./internal/agentd/ -count=1`
Expected: 全包通过（确认没打破既有用例）

- [ ] **Step 5: 加关键节点日志**

本 task 的日志已在 Step 3 的实现中就位，本步骤是按 instrumenting-code 清单逐条自检：

- [ ] 错误分支带上下文 + 原因：`serveIndex` 的 index 缺失分支打 `Error`，带 `path` 与 `err`
- [ ] 非预期输入有记录：非读方法打 `Warn`，带 `method`/`path`/`remote_addr`
- [ ] 高频路径降级到 Debug：回落与非法路径都是 `Debug`（每次深链接刷新都会走，打 Info 会淹没日志）
- [ ] 无 `fmt.Printf` / `println`

**注意成功路径**：静态文件命中是极高频路径，**刻意不打日志**——这是 instrumenting-code「循环内高频日志降级」的同类判断。「这份二进制有没有前端」这个真正需要被看见的事实，在 Task 3 的启动日志里打一次。

- [ ] **Step 6: 加注释**

自检（Step 3 已写入）：
- 文件头注释写明职责与**边界**，且明确写出「不处理 /api」这条边界**为什么是承重的**
- `hashedAsset` 正则上方解释「为什么按文件名而不按目录判断」
- `serveIndex` doc 注释解释「为什么 index.html 必须 no-cache」
- 405 分支、非法路径分支、index 缺失分支各有「为什么这样处理」的注释

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/webhandler.go internal/agentd/webhandler_test.go
git commit -m "feat(agentd): SPA handler——文件命中直发，未命中回落 index.html

缓存策略按文件名是否带 hash 分档：带 hash 的长缓存，index.html 与
不带 hash 的静态文件 no-cache（换版后旧 index 引用已消失的 hash 资源
会白屏，且用户清缓存前无法自愈）。

用 fstest.MapFS 覆盖全部分支，测试不依赖先跑过 npm run build。"
```

---

### Task 3: 挂进路由栈 + 启动日志报告嵌入状态

**Files:**
- Modify: `internal/agentd/server.go`
- Create: `internal/agentd/webroute_test.go`

**Interfaces:**
- Consumes: `newSPAHandler`（Task 2）、`webui.FS()` / `webui.Embedded()`（Task 1）
- Produces: 无新导出符号。行为契约：`GET /` 及一切未被更精确模式匹配的路径由 SPA handler 处理；`/api/*` 未命中仍返回 JSON。

- [ ] **Step 1: 写失败测试**

`internal/agentd/webroute_test.go`：

```go
package agentd

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
)

// consoleTestEnv 建一个带有效会话 cookie 的环境。
//
// 复用本包既有脚手架，不新造鉴权：newHostTestEnv 建 Server + httptest.Server，
// mustSession 直接往 store 里写一条会话并返回对应的 cookie 值（比走
// ticket 兑换流程短，且既有用例都这么做，见 auth_test.go:62）。
func consoleTestEnv(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	cookie := mustSession(t, srv.st, "sess-console", time.Now().Add(time.Hour), false)
	return ts, cookie
}

// 路由注册本身必须有测试覆盖。
//
// 这道门的由来：B108 复盘时发现「把路由注册整行注释掉，internal/agentd
// 全包测试依然全绿」——所有用例都在直接调 handler 函数，没有一条走完整
// 路由栈。本文件一律经 ts.URL 发请求，覆盖注册这一环。
func TestConsoleRouteRegistered(t *testing.T) {
	ts, cookie := consoleTestEnv(t)
	resp := getWithCookie(t, ts, "/", cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / 状态码 = %d，want 200（路由没注册？）", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / 的 Content-Type = %q，want text/html", ct)
	}
}

// 深链接经完整路由栈也要回落，而不是被别的 handler 抢走。
func TestDeepLinkRouteFallsBack(t *testing.T) {
	ts, cookie := consoleTestEnv(t)
	resp := getWithCookie(t, ts, "/tasks/00000000-0000-0000-0000-000000000000", cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("深链接状态码 = %d，want 200", resp.StatusCode)
	}
}

// 承重：/api 未命中必须是 JSON，不能被 SPA 回落成 HTML。
// 否则前端把 HTML 喂给 JSON.parse，报错与真实原因完全无关。
func TestUnknownAPIPathStaysJSON(t *testing.T) {
	ts, cookie := consoleTestEnv(t)
	resp := getWithCookie(t, ts, "/api/no-such-endpoint", cookie)
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if strings.Contains(strings.ToLower(body), "<!doctype html") ||
		strings.Contains(strings.ToLower(body), "<html") {
		t.Fatalf("/api 未命中被回落成 HTML，body = %q", body)
	}
	if resp.StatusCode == http.StatusOK {
		t.Errorf("/api 未命中状态码 = 200，want 4xx")
	}
}

// /console 仍必须是免鉴权入口，不能被 SPA 抢走。
func TestConsoleTicketRouteNotShadowed(t *testing.T) {
	ts, _ := consoleTestEnv(t)
	// 不带 cookie 直接打 /console（无 ticket）：应由 handleConsole 处理，
	// 表现为 4xx（ticket 缺失），而不是 SPA 的 200 HTML。
	resp, err := noRedirectClient(ts).Get(ts.URL + "/console")
	if err != nil {
		t.Fatalf("请求 /console 失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("/console 无 ticket 却返回 200，疑似被 SPA handler 抢走")
	}
}
```

> 已核实的既有签名（照用，别改）：
> - `newHostTestEnv(t *testing.T, cfg *config.Config) (*Server, *httptest.Server, *strings.Builder)` —— `hostguard_test.go:28`，第三个返回值是日志缓冲，本 task 用不到
> - `mustSession(t *testing.T, st *store.Store, id string, expiresAt time.Time, revoked bool) string` —— `auth_test.go:24`，返回 cookie 值
> - `getWithCookie(t, ts, path, cookie) *http.Response` —— `auth_test.go:44`
> - `noRedirectClient(ts) *http.Client` —— `auth_test.go:196`
> - `hostTestToken` 常量 —— `hostguard_test.go:25`
>
> 记得补 `net/http/httptest` 的 import（上面的 `consoleTestEnv` 返回类型用到了）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestConsoleRouteRegistered|TestDeepLinkRouteFallsBack|TestUnknownAPIPathStaysJSON|TestConsoleTicketRouteNotShadowed' -v`
Expected: `TestConsoleRouteRegistered` FAIL（404，路由未注册）

- [ ] **Step 3: 写实现**

在 `internal/agentd/server.go` 的路由注册处，向**内层 `mux`** 注册 SPA handler（不是 `root`）：

```go
	// /api 与 /ws 的未命中兜底：**必须显式注册，不能指望 mux 自动兜住**。
	//
	// Go 1.22 的 ServeMux 确实按模式精确度选择，但「更精确的模式胜出」
	// 只在那个模式**存在**时才成立。agentd 目前注册的 40 条 /api 路由
	// 全是精确路径或带 {id} 的参数路径，**没有一条 "/api/" 前缀兜底**。
	// 因此在补上下面这两行之前，GET /api/no-such 唯一匹配得上的是 "/"，
	// 会直接进 SPA handler 拿到 200 HTML——正是本 plan 标为承重的失败模式：
	// 前端把 HTML 喂给 JSON.parse，报错与真实原因完全无关。
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "接口不存在"})
	})
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "接口不存在"})
	})

	// 控制台静态资源兜底：一切未被更精确模式匹配的路径都到这里。
	//
	// 挂内层 mux 而不是 root：控制台页面本身要求 cookie，走 s.auth；
	// /console 是唯一免鉴权入口（ticket 本身就是它的凭据），它注册在 root 上。
	mux.Handle("/", newSPAHandler(webui.FS(), s.log))
```

> **实现者注意**：上面两条兜底注册是本 task 的必要组成，不是可选项。
>
> 派发前已核实的仓库现状（Go 1.26.1，本机实跑确认）：
> - `/api` 侧共 **40 条**注册，全为精确路径或带 `{id}` 的参数路径，**无 `/api/` 兜底**
> - `/ws` 侧只有两条：`GET /ws/events` 与 `GET /ws/pty`，均为**方法 + 精确路径**
>
> 因此新增 `/ws/` 兜底不会抢走它们——更精确的路径模式胜出，且那两条还带方法限定。
> 但仍请注册前自行复核一遍（`grep -oE 'mux\.Handle(Func)?\("[A-Z]* ?/ws[^"]*"' internal/agentd/*.go`），
> **不要因为 plan 这么写就当成已验证事实**；若发现已存在同名模式，不要重复注册
> （Go 的 mux 对同一模式重复注册会 panic），改为确认它返回的是 JSON。
>
> `writeJSON(w http.ResponseWriter, status int, v any)` 是本包既有函数（`server.go:1714`），直接用。

并在 agentd 启动日志处补一行嵌入状态（放在既有的启动完成日志附近）：

```go
	// 这份二进制有没有前端，是「控制台打不开」时第一个要排除的可能。
	// 不打这一行的话，运维只能靠猜：是构建时漏了 -tags embedweb，
	// 还是运行时路由坏了，两者现象完全一样。
	s.log.Info("控制台前端", "embedded", webui.Embedded())
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: 全包通过，含 4 个新用例

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: 全部无输出

- [ ] **Step 5: 变异验证（确认测试真的会咬）**

依次做以下四处改动，各自跑 `go test ./internal/agentd/ -count=1`，**每一处都必须让测试变红**；验证后全部还原：

| # | 变异 | 应该变红的用例 |
|---|---|---|
| 1 | 把 `mux.Handle("/", …)` 整行注释掉 | `TestConsoleRouteRegistered` |
| 2 | 把注册目标从 `mux` 改成 `root` | `TestConsoleTicketRouteNotShadowed` 或鉴权相关用例 |
| 3 | `serveIndex` 里的 `Cache-Control` 改成 `cacheImmutable` | `TestSPAIndexIsNoCache` |
| 4 | `newSPAHandler` 里去掉方法判断（让 POST 也回落） | `TestSPARejectsNonGet` |

任一变异跑完仍全绿 = 那条测试没有咬住，必须补测试后重验。**把四次变异的实际结果（红/绿 + 哪些用例失败）写进 ledger**，不要只写「已验证」。

- [ ] **Step 6: 加关键节点日志**

- [ ] 启动日志报告 `embedded` 状态（Step 3 已写）
- [ ] 确认没有为高频静态请求新增 Info 级日志
- [ ] 无 `fmt.Printf`

- [ ] **Step 7: 加注释**

- [ ] 注册处的注释说明**为什么挂内层 mux 而不是 root**
- [ ] 注册处的注释说明 Go 1.22 精确前缀优先如何保证 `/api` 不被吞
- [ ] 启动日志上方注释说明**为什么这一行值得打**

- [ ] **Step 8: 提交**

```bash
git add internal/agentd/server.go internal/agentd/webroute_test.go
git commit -m "feat(agentd): 把控制台挂进路由栈，启动日志报告嵌入状态

SPA handler 挂内层 mux（s.auth 之后），/console 仍是唯一免鉴权入口。
Go 1.22 精确前缀优先保证 /api、/ws 不会被回落成 HTML。

新测试一律经完整路由栈（env.ts.URL）发请求，覆盖路由注册这一环——
B108 复盘发现注释掉注册行全包测试仍全绿，那个盲区在这里补上。"
```

---

### Task 4: 未鉴权的 HTML 请求返回说明页

**Files:**
- Modify: `internal/agentd/server.go`（`auth` 中间件的两个 401 出口）
- Create: `internal/agentd/authpage_test.go`

**Interfaces:**
- Consumes: 无
- Produces: 无新导出符号。行为契约：401 响应按 `Accept` 分流——HTML 请求得说明页，其余维持原有 JSON。

- [ ] **Step 1: 写失败测试**

`internal/agentd/authpage_test.go`：

```go
package agentd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// getNoAuth 不带任何凭据发一个请求，用指定的 Accept。
func getNoAuth(t *testing.T, ts *httptest.Server, path, accept string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := noRedirectClient(ts).Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败：%v", path, err)
	}
	return resp
}

// 浏览器直接打开 agentd 根地址（没有 cookie）时，裸 401 会让人以为服务坏了。
// 给 HTML 请求一个说明页，写清怎么拿入口。
func TestUnauthenticatedHTMLGetsGuidancePage(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	resp := getNoAuth(t, ts, "/", "text/html,application/xhtml+xml")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，want 401（状态码不能因为返回 HTML 就变）", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q，want text/html", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "handoff console") {
		t.Errorf("说明页没写清怎么拿入口，body = %q", b)
	}
}

// 承重：非 HTML 请求（CLI、fetch）必须维持原有 JSON 401，
// 否则所有调用方的错误处理都会因为拿到 HTML 而失效。
func TestUnauthenticatedJSONStaysJSON(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	for _, accept := range []string{"application/json", "*/*", ""} {
		resp := getNoAuth(t, ts, "/api/tasks", accept)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Accept=%q 状态码 = %d，want 401", accept, resp.StatusCode)
		}
		if strings.Contains(strings.ToLower(string(b)), "<html") {
			t.Errorf("Accept=%q 拿到了 HTML，want JSON：%s", accept, b)
		}
	}
}

// token 未配置（fail-closed）分支同样要按 Accept 分流。
//
// 这一条单独立用例的理由：auth 中间件有**两个** 401 出口，上面两个用例都
// 只覆盖 sess == nil 那一个。只改一处的话，「token 未配置」时浏览器仍会
// 拿到裸 JSON——而那恰恰是最需要说明页的场景（运维刚装完还没配 token）。
func TestUnauthenticatedHTMLWhenTokenUnset(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: ""})
	resp := getNoAuth(t, ts, "/", "text/html")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("token 未配置分支的 Content-Type = %q，want text/html（另一个 401 出口漏改了？）", ct)
	}
}
```

> 注：`newHostTestEnv` 接受任意 `*config.Config`，因此 `Token: ""` 这条 fail-closed 路径可以直接构造，不需要额外脚手架。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestUnauthenticated' -v`
Expected: `TestUnauthenticatedHTMLGetsGuidancePage` FAIL（拿到 JSON）

- [ ] **Step 3: 写实现**

在 `internal/agentd/server.go` 里新增：

```go
// unauthorizedPage 是浏览器直接访问 agentd 而没有会话 cookie 时看到的页面。
//
// 为什么不直接返回裸 JSON 401：浏览器会把它当成一段纯文本显示，用户看到的是
// 一个孤零零的 {"error":"未授权"}，无从判断是自己没登录、还是服务坏了。
// 说明页把「怎么拿入口」直接写出来，是这里唯一不会把人引向错误排查方向的做法。
const unauthorizedPage = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>handoff：需要登录</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.7">
<h1>需要登录</h1>
<p>agentd 工作正常，但这个浏览器还没有有效会话。控制台入口需要一张一次性 ticket。</p>
<h2>怎么拿入口</h2>
<ul>
<li>命令行：<code>handoff console</code>，它会签一张 ticket 并给出可直接打开的链接。</li>
<li>桌面端：直接打开 handoff 桌面应用，它会自动完成这一步。</li>
</ul>
</body></html>
`

// wantsHTML 报告请求方是否更希望拿到 HTML。
//
// 判据刻意从严：只有 Accept 里**显式**出现 text/html 才算。浏览器地址栏发起的
// 导航一定带它；而 fetch/XHR、CLI、`*/*` 都不带，会走原有 JSON 分支——
// 那些调用方的错误处理都按 JSON 写的，给它们 HTML 会让整条错误链失效。
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// writeUnauthorized 按调用方偏好输出 401。
//
// 注意：无论走哪个分支，**状态码恒为 401**。不要因为返回了 HTML 就改成 200，
// 那会让监控与前端的鉴权拦截器同时失效。
func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, unauthorizedPage)
		return
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
}
```

然后把 `auth` 中间件里**两处** `writeJSON(w, http.StatusUnauthorized, …)` 都换成 `writeUnauthorized(w, r)`：

1. `s.cfg.Token == ""` 的 fail-closed 分支
2. `sess == nil` 的鉴权失败分支

**两处都要改**——只改一处会让「token 未配置」时浏览器仍拿到裸 JSON，而那恰恰是最需要说明的场景。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: 全包通过，含 3 个新用例

- [ ] **Step 5: 变异验证**

| # | 变异 | 应该变红的用例 |
|---|---|---|
| 1 | `wantsHTML` 恒返回 `true` | `TestUnauthenticatedJSONStaysJSON` |
| 2 | `wantsHTML` 恒返回 `false` | `TestUnauthenticatedHTMLGetsGuidancePage` **和** `TestUnauthenticatedHTMLWhenTokenUnset` |
| 3 | HTML 分支的状态码改成 200 | `TestUnauthenticatedHTMLGetsGuidancePage` |
| 4 | **只**改 `sess == nil` 那一处，保留 token 分支原样的 `writeJSON` | 只有 `TestUnauthenticatedHTMLWhenTokenUnset` 变红 |

变异 4 是本 task 最重要的一条：它专门验「两个 401 出口都改到了」。如果它跑完仍全绿，说明 `TestUnauthenticatedHTMLWhenTokenUnset` 没有真正咬住 token 分支，**必须先修测试再继续**，不要跳过。

把四次变异结果逐条写进 ledger（哪些用例红了、报文是什么），不要只写「已验证」。

- [ ] **Step 6: 加关键节点日志**

`auth` 中间件既有的两条日志（`Error` 的 token 未配置、`Warn` 的鉴权失败）保持不变，**不要因为这次改动删掉或降级**——它们带着 `remote_addr`/`method`/`path`/`reason`，是鉴权问题的唯一线索。自检：

- [ ] 两条既有日志仍在，字段未减
- [ ] 未新增高频日志（401 可能被扫描器高频触发，不要在此处加 Info）

- [ ] **Step 7: 加注释**

- [ ] `unauthorizedPage` 注释说明**为什么不用裸 JSON**
- [ ] `wantsHTML` 注释说明**判据为什么从严**（以及放宽会打断哪些调用方）
- [ ] `writeUnauthorized` 注释强调**状态码恒为 401**

- [ ] **Step 8: 提交**

```bash
git add internal/agentd/server.go internal/agentd/authpage_test.go
git commit -m "feat(agentd): 未鉴权的 HTML 请求返回说明页而非裸 JSON

浏览器直接打开 agentd 会看到孤零零的 {\"error\":\"未授权\"}，无从判断
是没登录还是服务坏了。按 Accept 分流：显式要 text/html 的给说明页，
其余（fetch/CLI/*\/*）维持原有 JSON——那些调用方的错误处理都按 JSON 写的。

状态码恒为 401，不因返回 HTML 而变。auth 中间件的两个 401 出口都改了。"
```

---

### Task 5: 前端构建接进 release 流水线

**Files:**
- Modify: `.github/workflows/release.yml`
- Create: `scripts/build-release-local.sh`

**Interfaces:**
- Consumes: 前四个 task 的全部成果
- Produces: release 产物含前端；本地可复现的等价构建脚本

- [ ] **Step 1: 写本地等价构建脚本（这是本 task 的「测试」）**

CI 工作流无法单元测试，也不能靠「推个 tag 试试看」验证——那代价太高且污染 Release。改为：把 CI 会跑的构建步骤在本地等价复现，用它做验证门。

`scripts/build-release-local.sh`：

```bash
#!/usr/bin/env bash
# 在本地复现 release 流水线的构建步骤，用于在改 workflow 之前/之后验证
# 「前端构建 → go build -tags embedweb → 产物含前端」这条链路真的通。
#
# 职责：只构建与自检，不签名、不打包、不上传——那些是 CI 的事。
# 边界：不修改工作区里的任何被跟踪文件；产物落在 mktemp 出来的目录里。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

echo "==> 构建前端"
cd "$ROOT/web"
npm ci
npm run build
[ -f "$ROOT/web/dist/index.html" ] || { echo "前端产物缺 index.html" >&2; exit 1; }

echo "==> 把产物拷进 internal/webui/dist/"
rm -rf "$ROOT/internal/webui/dist"
cp -R "$ROOT/web/dist" "$ROOT/internal/webui/dist"

echo "==> 带 embedweb 标签构建"
cd "$ROOT"
CGO_ENABLED=0 go build -trimpath -tags embedweb -ldflags "-s -w" -o "$OUT/handoff" .

echo "==> 带标签跑 webui 测试（确认产物真进去了，不是空壳）"
CGO_ENABLED=0 go test -tags embedweb ./internal/webui/...

echo "==> 自检：工作区不能因为构建而变脏"
if ! git -C "$ROOT" diff --quiet || \
   [ -n "$(git -C "$ROOT" status --porcelain --untracked-files=normal | grep -v '^?? internal/webui/dist/' || true)" ]; then
  echo "构建让工作区变脏了——这会破坏 dispatch 的干净工作区前置条件" >&2
  git -C "$ROOT" status --short >&2
  exit 1
fi

echo "OK：产物 $( du -h "$OUT/handoff" | cut -f1 )，工作区干净"
```

- [ ] **Step 2: 跑脚本确认当前会失败或暴露问题**

Run: `bash scripts/build-release-local.sh`
Expected: 此时应当**通过**（前四个 task 已完成）。若失败，说明前面某个 task 有缺陷，先回头修，不要在本 task 里绕过。

特别关注最后那道「工作区不能变脏」的自检——如果它红了，说明 `.gitignore` 里 `internal/webui/dist/` 没生效，那是 Task 1 的缺陷。

- [ ] **Step 3: 改 release.yml**

三处改动：

**关于 Node 版本——必须钉死，且必须实测确认**（不要沿用本 plan 的数字当成已验证事实）：

仓库里**既没有 `web/.nvmrc` 也没有 `package.json` 的 `engines` 字段**（已核实），因此不钉版本时 runner 换默认版本会静默改变构建结果。已知事实：本地开发机跑的是 **Node v23.11.0 / npm 10.9.2**（构建在此版本下可用），依赖侧是 Vite `^6.3.5` + TypeScript `~5.8.3` + `@types/node` `^26.2.0`。

下面写的 `node-version: '24'` 是**待验证的选择**（取 v23 之上的 LTS 线）。本 task 必须实际确认它能构建成功：

- 若 CI 或本地用该版本 `npm ci && npm run build` 失败，**改成实测可用的版本并在提交信息里写明依据**，不要保留一个跑不通的数字。
- **不要用 `lts/*` 或省略版本**——它们会随时间漂移，某天静默改变构建结果，而这个变化不体现在任何一次提交里（与 §6.2 不用 `ubuntu-latest` 是同一条理由）。
- 顺手在 `web/.nvmrc` 里写下最终选定的版本，让本地与 CI 共用同一个来源。

**① `build-matrix` job（linux/windows 交叉编译）**：在 `- name: 交叉编译并打包` 之前插入前端构建，并给 `go build` 加 `-tags embedweb`。

```yaml
      - uses: actions/setup-node@v4
        with:
          node-version: '24'
          cache: npm
          cache-dependency-path: web/package-lock.json

      - name: 构建前端并放进 embed 目录
        run: |
          set -euo pipefail
          npm --prefix web ci
          npm --prefix web run build
          # 产物缺 index.html 时必须当场失败：让它继续跑下去只会得到一个
          # 编译通过但页面 404 的二进制，而那要等到用户手上才暴露
          test -f web/dist/index.html
          rm -rf internal/webui/dist
          cp -R web/dist internal/webui/dist
```

**② `build-darwin` job**：同样插入前端构建。**注意顺序**——前端构建必须在 `go build` 之前、`codesign` 之前。

**③ 两处 `go build`**：加 `-tags embedweb`。

**④ 在两个 build job 里各加一道验证**：

```yaml
      - name: 验证产物真的含前端
        run: CGO_ENABLED=0 go test -tags embedweb ./internal/webui/...
```

这一步是承重的：`go:embed` 一个只有 index.html 的空壳目录**也能编译通过**，光看构建成功不代表前端进去了。Task 1 的 `embed_test.go` 就是为这道门写的。

- [ ] **Step 4: 验证 workflow 改动**

由于无法在本地真跑 GitHub Actions，验证分三层：

1. Run: `bash scripts/build-release-local.sh` —— 确认构建链路本身成立
2. Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml'))"` —— 确认 YAML 合法
3. 人工逐条核对：每个 `go build` 都带了 `-tags embedweb`；每个 build job 都在 `go build` 之前构建了前端；`build-darwin` 里前端构建在 `codesign` 之前

Run: `grep -c 'tags embedweb' .github/workflows/release.yml`
Expected: 至少 4（两处 go build + 两处验证步骤）

- [ ] **Step 5: 加关键节点日志**

CI 步骤的「日志」即步骤名与失败信息。自检：

- [ ] 每个新增步骤都有中文 `name`，说明它在做什么
- [ ] 前端产物缺失时**当场失败**（`test -f web/dist/index.html`），而不是让后续步骤拿着空目录继续
- [ ] `set -euo pipefail` 在每个多行 `run` 里都有——缺了它，中间某条命令失败会被静默吞掉

- [ ] **Step 6: 加注释**

- [ ] `scripts/build-release-local.sh` 头部注释写明职责与边界（Step 1 已含）
- [ ] release.yml 里「验证产物真的含前端」步骤上方加注释，说明**为什么光构建成功不够**（空壳目录也能编译过）
- [ ] `build-darwin` 里说明**为什么前端构建必须在 codesign 之前**

- [ ] **Step 7: 提交**

```bash
git add .github/workflows/release.yml scripts/build-release-local.sh
git commit -m "ci: 前端构建接进 release，go build 带 -tags embedweb

两个 build job 各加：setup-node → npm ci && npm run build → 拷进
internal/webui/dist/ → go build -tags embedweb → 跑带标签的 webui 测试。

最后那步是承重的：go:embed 一个只有 index.html 的空壳目录也能编译通过，
光看构建成功不代表前端真的进去了。

新增 scripts/build-release-local.sh 在本地等价复现这条链路，
使 workflow 改动不必靠「推个 tag 试试看」来验证；脚本末尾还有一道
「构建不得让工作区变脏」的自检，守住 dispatch 的干净工作区前置条件。"
```

---

## 终审检查清单

全部 task 完成后，按 CLAUDE.md §5 逐项确认：

- [ ] `go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./... -count=1` 全绿
- [ ] `go test -tags embedweb ./internal/webui/...` 在本地跑过 `scripts/build-release-local.sh` 之后全绿
- [ ] **不带标签**时 `go build ./...` 与 `go test ./...` 全绿（第一红线）
- [ ] `bash scripts/build-release-local.sh` 通过，且末尾的工作区干净自检通过
- [ ] 所有新文件有文件头注释（职责 + 边界）
- [ ] 所有导出函数有 doc 注释
- [ ] 复杂逻辑与边界条件有「为什么」注释（非复述代码）
- [ ] 关键节点有日志，错误分支带上下文，无 `fmt.Printf`
- [ ] 全部变异验证结果（Task 3 四条、Task 4 五条）逐条记入 ledger，不是只写「已验证」
- [ ] 无跨层调用；`internal/webui` 不依赖 `internal/agentd`（依赖方向单向）

## 已知不在本 plan 范围内

- 桌面薄壳（W5b，另一份 plan，依赖本 plan 落地）
- 薄壳的构建链、签名公证、Release 资产（W5b）
- 内嵌 `handoff` 二进制（W5b）
- 扩到 `webkit2gtk-4.0` 的 Linux 老发行版（spec §7 非目标）
