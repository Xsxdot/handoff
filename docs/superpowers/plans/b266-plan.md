# B266 实现计划：桌面端从浏览器打开工作项整页

读者：对仓库零上下文的执行者。依据已批准的 `docs/superpowers/specs/b266.md`；本节点读取的是 `origin/main` 的提交 `6630abeb2`，基线工作树为 `5e8826f7338fed52e8a45017f8555de0675fc2c8`。不要在实现分支补写或改动 spec。

范围只覆盖桌面 CardsPage 的按钮、WebKit raw message 与桌面系统浏览器打开。不要改 agentd HTTP/账本/工作流/IM/B265、不要改标题栏高度、不要把按钮放进 `DesktopTitleBar`、不要把按钮扩展到普通浏览器。

## 基线与已核对事实

### 基线判据

动手前已运行以下命令：

```sh
cd web
npx vitest run src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
```

实际结果：`Test Files  3 passed (3)`、`Tests  16 passed (16)`。

```sh
cd web
npm run typecheck
npx eslint src/app/cards/CardsPage.tsx src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.ts src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
```

实际结果：`npm run typecheck` 的 `tsc -b` 退出 0；定向 eslint 无输出、退出 0。

桌面包的可复用基线命令为：

```sh
cd desktop
go test ./internal/shell/... -run '^(TestResolve|TestNormalizeProjectDir|TestAwaitWebviewReady|TestConsoleURL|TestDefaultDeviceName|TestBuildForm|TestApplyAnswers)'
```

实际结果：`ok  github.com/Xsxdot/handoff/desktop/internal/shell  0.065s`。同样已运行但未通过的整包命令是 `go test ./internal/shell/...`；原始失败为既有同步夹具向 `/tmp/handoff` 写入时的 `open /tmp/.handoff-sync-2211604910: read-only file system`，不是本卡断言。实现后优先运行新增定向测试；具备 Wails 工具链的环境再运行装配构建：

```sh
cd desktop
wails3 task build
```

本节点运行该构建得到原始结果 `/bin/bash: line 1: wails3: command not found`，所以不把装配编译写成已验证；`desktop/README.md` 已规定该命令负责生成 `frontend/dist` 后再构建嵌套模块。

### 图查询与覆盖债

项目有 `codegraph/`，已按最优树词表运行：

```sh
GOMODCACHE=/root/.handoff/tmp/347d1589/chart-gomodcache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_web_cards
```

结果是 `view: baseline`、`truncated: false`、`unscannedEntries: 6`。最优领域是 `d_web_cards`（账本看板），但 `k_web_app_cards`、`k_web_app_cards_model`、`k_web_app_flows`、`k_web_app_flows_model` 今日实际落在 `d_web`，四项均为 misplaced。`CardsPage`、`DesktopTitleBar` 在图中没有图内调用方，不能据此断言没有调用方；这 6 个未扫描入口是覆盖债，交给实现后的 review 与真机验证。

已核对的图符号和源码窗口：

- `CardsPage`：`web/src/app/cards/CardsPage.tsx:64`，当前 `useSearchParams()` 只读 `project`，`useNavigate()` 只用于任务深链，顶栏健康点在 `ml-auto`。
- `isDesktopShell`：`web/src/app/lib/desktopShell.ts:29`，按 UA 中 `handoff-desktop` 判断。
- `requestTitlebarZoom`：`web/src/app/lib/desktopShell.ts:61`，通过 `window.webkit.messageHandlers.external.postMessage(string)` 发送 `wails:drag:doubleclick`。
- `DesktopTitleBar`：`web/src/app/shell/DesktopTitleBar.tsx:36`，高度为 `DESKTOP_TOP_INSET`，既有测试断言标题栏没有任何 button/link/input/select/textarea。
- `Shell`：`web/src/app/shell/Shell.tsx:679` 将 `/cards` 放进 `FullPageCover`；`FullPageCover` 在 `:838-840` 为整页覆盖层。因此 CardsPage 顶栏天然位于 28px `DesktopTitleBar` 下方，本卡不改 Shell 的布局。

### Wails 依赖行为依据

`desktop/go.mod` 钉的是 `github.com/wailsapp/wails/v3 v3.0.0-beta.8`。已读取该版本源码并记录出处：

- `pkg/application/application_options.go:96-98`：raw 回调精确签名为 `func(window Window, message string, originInfo *OriginInfo)`。
- `pkg/application/application.go:271-275`：`OriginInfo` 字段为 `Origin string`、`TopOrigin string`、`IsMainFrame bool`。
- `pkg/application/application.go:852-859`：`wails:` 消息走 Wails 内置分发，其他消息才进入 `Options.RawMessageHandler`。
- `pkg/application/webview_window_darwin.m:599-613`：Darwin 将 frame URL 的 `absoluteString` 放入 `OriginInfo.Origin`，所以它可能包含 `/console?ticket=...`，来源校验必须比较 scheme/host/有效端口，不能全串比较，也不能把完整来源写日志。
- `pkg/application/browser_manager.go:19-22`：`func (bm *BrowserManager) OpenURL(url string) error` 调用默认浏览器实现；`internal/browser/browser_darwin.go:7-17`、`browser_other.go:7-17`、`browser_windows.go:7-17` 分别以 `exec.Command("open", target)`、`exec.Command("xdg-open", target)`、`exec.Command("rundll32", "url.dll,FileProtocolHandler", target)` 传递单独参数。
- `pkg/application/urlvalidator.go:11-49` 的 `ValidateAndSanitizeURL` 只在 Wails runtime Browser 消息处理链中出现；`BrowserManager.OpenURL` 源码没有调用它。因此本卡在交给 `App.Browser.OpenURL` 前自行限制 http(s)、来源同源、无 userinfo、无控制字符，再由 Wails 平台实现打开。

## Task 1 — CardsPage 桌面可见性与 WebKit 消息

**锁缝：** spec 接缝 1（`isDesktopShell` / CardsPage 渲染）、接缝 2（点击发出当前页 URL 宿主消息）和接缝 4 的前端半边（标题栏之外、浏览器布局不变）。

**允许修改的文件：**

- `web/src/app/lib/desktopShell.ts`
- `web/src/app/lib/desktopShell.test.ts`
- `web/src/app/cards/CardsPage.tsx`
- `web/src/app/cards/CardsPage.test.tsx`

不要修改 `web/src/app/shell/DesktopTitleBar.tsx`、`web/src/app/shell/Shell.tsx` 或任何 API 文件。

**Consumes：**

- `navigator.userAgent: string`，由既有 `isDesktopShell(ua?: string): boolean` 判断桌面壳。
- `window.location.origin: string`、`window.location.pathname: string`、`window.location.search: string`。
- `window.webkit?.messageHandlers?.external?.postMessage: (msg: string) => void`。
- CardsPage 既有 `useSearchParams(): URLSearchParams`、`useNavigate(): NavigateFunction` 以及健康点顶栏 DOM。

**Produces：**

```ts
export const OPEN_BROWSER_MESSAGE_PREFIX: string = 'handoff:open-browser:'

export function requestOpenCurrentPageInBrowser(): boolean
```

`requestOpenCurrentPageInBrowser` 不接受 URL 参数，避免调用方把任意地址送入宿主；它只拼接 `${window.location.origin}${window.location.pathname}${window.location.search}`，用 `OPEN_BROWSER_MESSAGE_PREFIX + currentURL` 通过已有 `external.postMessage` 发送。没有 bridge 时返回 `false` 且不抛异常；发送成功返回 `true`。协议字面量必须与 Task 2 的 Go 常量逐字符相同：`handoff:open-browser:`。

**最小测试范围：**

```sh
cd web
npx vitest run src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
npm run typecheck
npx eslint src/app/cards/CardsPage.tsx src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.ts src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
```

不跑全量 Vitest；不把浏览器真实打开交给 jsdom。

### 基线复核（已于动手前完成）

已先运行上面的前端三文件 Vitest，结果为 `Test Files  3 passed (3)`、`Tests  16 passed (16)`；`npm run typecheck` 的 `tsc -b` 退出 0；定向 eslint 无输出、退出 0。下面的红测不能以改变这些既有判据为代价。

### Step 1 — 先写前端锁缝红测并跑红

在 `web/src/app/lib/desktopShell.test.ts` 的既有 import 中加入 `OPEN_BROWSER_MESSAGE_PREFIX` 与 `requestOpenCurrentPageInBrowser`，并追加以下完整测试块。它从声明缝入口调用 `requestOpenCurrentPageInBrowser`，检查 bridge 缺失、当前路径和 query 都在真实 wire 字符串里：

```ts
describe('requestOpenCurrentPageInBrowser', () => {
  const bridge = () => (window as unknown as { webkit?: unknown })

  afterEach(() => {
    delete bridge().webkit
    window.history.replaceState({}, '', '/')
  })

  it('发送固定协议前缀和当前页面的 path/query', () => {
    const postMessage = vi.fn()
    bridge().webkit = { messageHandlers: { external: { postMessage } } }
    window.history.replaceState({}, '', '/cards?project=handoff')

    expect(requestOpenCurrentPageInBrowser()).toBe(true)
    expect(OPEN_BROWSER_MESSAGE_PREFIX).toBe('handoff:open-browser:')
    expect(postMessage).toHaveBeenCalledWith(
      `${OPEN_BROWSER_MESSAGE_PREFIX}${window.location.origin}/cards?project=handoff`,
    )
  })

  it('没有 external bridge 时返回 false 且不抛异常', () => {
    expect(requestOpenCurrentPageInBrowser()).toBe(false)
  })
})
```

运行上面的 Vitest 命令；此时应因新导出不存在而红。测试未变红时，先检查它确实调用了新入口和固定 wire 字符串；禁止弱化为直接调用内部拼接函数。

### Step 2 — 实现 bridge helper

在 `web/src/app/lib/desktopShell.ts` 的既有 `WebkitBridge` 与 `requestTitlebarZoom` 附近增加以下完整代码。`OPEN_BROWSER_MESSAGE_PREFIX` 必须是唯一协议名；不要复用 `wails:` 前缀，因为 Wails 会把 `wails:` 消息拦给内置窗口处理而不进入 raw handler。

```ts
// OPEN_BROWSER_MESSAGE_PREFIX 是 CardsPage 发给桌面宿主的原始消息前缀。
// 必须避开 wails:：Wails 会把 wails: 消息交给自己的窗口手势处理器，不会送到
// RawMessageHandler。URL 作为同一条字符串的后缀传输，桌面侧再做同源校验。
export const OPEN_BROWSER_MESSAGE_PREFIX = 'handoff:open-browser:'

// requestOpenCurrentPageInBrowser 请求桌面宿主用系统浏览器打开当前整页。
// 参数：无；URL 只能从当前 window.location 产生，调用方不能注入任意地址。
// 返回：找到并发出 external bridge 时为 true，否则为 false。
// 注意：它不导航、不改变当前页面状态；浏览器分支没有 bridge 时是安静空操作。
export function requestOpenCurrentPageInBrowser(): boolean {
  const bridge = window as unknown as WebkitBridge
  const post = bridge.webkit?.messageHandlers?.external?.postMessage
  if (!post) return false
  const currentURL = `${window.location.origin}${window.location.pathname}${window.location.search}`
  post.call(bridge.webkit!.messageHandlers!.external, `${OPEN_BROWSER_MESSAGE_PREFIX}${currentURL}`)
  return true
}
```

### Step 3 — 把按钮接在 CardsPage 顶栏右侧

在 `web/src/app/cards/CardsPage.tsx` 增加：

```ts
import { isDesktopShell, requestOpenCurrentPageInBrowser } from '../lib/desktopShell'
```

在 `CardsPage` 的现有 `useNavigate()` 附近增加桌面可见性快照：

```ts
  const showOpenInBrowser = isDesktopShell()
```

把现有顶栏中从健康点开始的代码替换为以下完整 JSX。按钮紧挨右侧健康点但位于其前面；`ml-auto` 只在桌面转移到按钮，浏览器分支仍给健康点保留原有 `ml-auto`，因此普通浏览器布局不变：

```tsx
        {showOpenInBrowser && (
          <button
            type="button"
            aria-label="从浏览器打开"
            title="从浏览器打开当前工作项页"
            onClick={() => { requestOpenCurrentPageInBrowser() }}
            className="ml-auto rounded-md border px-2.5 py-1 text-xs"
          >
            从浏览器打开
          </button>
        )}
        <span className={`${showOpenInBrowser ? '' : 'ml-auto'} flex items-center gap-1 text-[11px] ${healthStale ? 'text-amber-700' : 'text-green-600'}`} title={healthStale ? `${healthLabel}——该机器的事件已停止镜像，卡上的 task 实况可能是陈的` : '镜像正常'}>{healthStale ? healthLabel : '●'}</span>
```

不要把 `view`、`needsOnly`、`workflow`、`search` 或 `includeArchived` 新编码进 query：基线只有 `project` 是既有 URL 查询参数，本卡按 spec 发送当前地址的 path/query，不发明新的路由状态契约；点击也不调用 `navigate`，所以桌面仍停在 `/cards`。

实现后补齐 `web/src/app/cards/CardsPage.test.tsx` 的 import 和以下完整 describe。沿用文件已有的 `renderPage`、ledger mocks 与 `DesktopTitleBar` 独立测试；用真实 UA 判定与真实 bridge spy，不 mock `isDesktopShell`，并用 `window.location.origin` 避免把 jsdom 端口写死：

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
```

```tsx
const DESKTOP_UA = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 handoff-desktop'
const BROWSER_UA = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 Safari/605.1.15'

function setUA(ua: string): void {
  Object.defineProperty(navigator, 'userAgent', { value: ua, configurable: true })
}

function bridge(): { webkit?: unknown } {
  return window as unknown as { webkit?: unknown }
}

describe('CardsPage 从浏览器打开', () => {
  afterEach(() => {
    delete bridge().webkit
    setUA(BROWSER_UA)
    window.history.replaceState({}, '', '/')
  })

  it('桌面 UA 显示按钮，点击发送当前 /cards query 且不离开页面', async () => {
    setUA(DESKTOP_UA)
    window.history.replaceState({}, '', '/cards?project=handoff')
    const postMessage = vi.fn()
    bridge().webkit = { messageHandlers: { external: { postMessage } } }

    renderPage('/cards?project=handoff')
    const button = screen.getByRole('button', { name: '从浏览器打开' })
    expect(button).toHaveClass('ml-auto')
    expect(screen.getByTitle('镜像正常')).not.toHaveClass('ml-auto')

    fireEvent.click(button)

    expect(postMessage).toHaveBeenCalledTimes(1)
    expect(postMessage).toHaveBeenCalledWith(
      `handoff:open-browser:${window.location.origin}/cards?project=handoff`,
    )
    expect(window.location.pathname + window.location.search).toBe('/cards?project=handoff')
    expect(screen.getByText('工作项')).toBeInTheDocument()
  })

  it('普通浏览器不渲染按钮且健康点仍占右侧', async () => {
    setUA(BROWSER_UA)
    renderPage('/cards?project=handoff')

    expect(screen.queryByRole('button', { name: '从浏览器打开' })).toBeNull()
    expect(screen.getByTitle('镜像正常')).toHaveClass('ml-auto')
  })
})
```

### Step 4 — 跑 Task 1 绿测并检查标题栏回归

运行 Task 1 的最小测试、typecheck、定向 eslint。预期三份 Vitest 文件全绿，新增桌面 UA 用例能找到按钮并收到包含 `/cards?project=handoff` 的完整字符串，普通 UA 查询不到按钮，既有 `DesktopTitleBar.test.tsx` 的“标题栏里一个可点元素都不能有”继续通过。失败时保留实际输出，不能改测为 `queryByText` 等更弱断言。

### Step 5 — 前端注释与可观测性

保留 `desktopShell.ts` 文件头对“外链页面 + UA + external bridge”的职责/边界说明，并为新导出的常量和函数保留上面的参数、返回值、协议避让注释；在 `CardsPage` 的 URL 发送处补一句为什么只取 origin/pathname/search、为什么不 `navigate`。前端没有本卡专用结构化 logger，不新增 `console.log`/`console.warn`；宿主接收、拒绝、调用系统浏览器、调用失败的结构化日志由 Task 2 统一负责。

## Task 2 — 桌面 raw handler 的同源校验、系统打开与 Wails 接线

**锁缝：** spec 接缝 3（薄壳同源 URL 打开、非同源/非 http(s) 拒绝）和接缝 2 的宿主半边。此 task 不新增 agentd HTTP，不把 Wails 依赖导入 `desktop/internal/shell`。

**允许修改的文件：**

- `desktop/internal/shell/external_browser.go`（新增，完整文件）
- `desktop/internal/shell/external_browser_test.go`（新增，完整文件）
- `desktop/main.go`（仅 `application.Options` 装配块）

**Consumes：**

- `message: string`：Wails `Options.RawMessageHandler` 收到的 raw message。
- `sourceFrameURL: string`：主程序从 `application.OriginInfo.Origin` 取值；为空时回退 `TopOrigin`。该值可能是带路径/query 的完整 frame URL。
- `open: func(string) error`：生产中为 `app.Browser.OpenURL`，测试中为 spy。
- `log: *slog.Logger`：生产中为包级 `logger`，记录结构化入口、拒绝、打开前后和错误分支。

**Produces：**

```go
const ExternalBrowserMessagePrefix = "handoff:open-browser:"

func HandleExternalBrowserMessage(log *slog.Logger, message, sourceFrameURL string, open func(string) error) bool
```

返回值含义：不是该协议前缀时返回 `false`，让 main 记录“未识别 raw message”；一旦是该协议，即使 URL 被拒绝或系统打开失败也返回 `true`，表示该消息已消费，不让它落入别的逻辑。允许调用的 target 必须是 http/https、含 host、无 userinfo、无控制字符，并与来源 frame 的 scheme/hostname/有效端口同源；通过后只把规范化 URL交给 `open`。

**最小测试范围：**

```sh
cd desktop
go test ./internal/shell/... -run '^TestHandleExternalBrowserMessage$'
```

再以同一包命令加上已通过的基线 regex，避免既有 `/tmp/handoff` 同步夹具遮蔽本卡结果：

```sh
cd desktop
go test ./internal/shell/... -run '^(TestHandleExternalBrowserMessage|TestResolve|TestNormalizeProjectDir|TestAwaitWebviewReady|TestConsoleURL|TestDefaultDeviceName|TestBuildForm|TestApplyAnswers)'
```

具备 Wails CLI 和构建依赖后，还必须运行 `cd desktop && wails3 task build`，以编译 `desktop/main.go` 的 RawMessageHandler 接线；构建产物不得提交。

### 基线复核（已于动手前完成）

已先运行不触发既有 `/tmp/handoff` 同步夹具的桌面 shell 基线 regex，结果为 `ok  github.com/Xsxdot/handoff/desktop/internal/shell  0.065s`。整包 `go test ./internal/shell/...` 也已运行，但在既有 `TestSyncOnOpenOrderIsLoadBearing` 处因 `open /tmp/.handoff-sync-2211604910: read-only file system` 失败；该环境失败不改变 B266 的定向判据。Wails 装配基线已执行但当前 runner 返回 `/bin/bash: line 1: wails3: command not found`，不能写成已通过。

### Step 1 — 写桌面接缝红测并跑红

新建 `desktop/internal/shell/external_browser_test.go`，完整内容如下。第一条用例喂入与前端实际发送形状相同的完整 raw 字符串，并使用带一次性 ticket 的来源 frame URL，证明来源比较忽略路径/query 而不泄露它；负向表逐条断言 opener 没有被调用。所有断言均从导出的 `HandleExternalBrowserMessage` 声明缝进入，不直接测内部解析函数。

```go
// 本文件覆盖桌面 raw message 到系统浏览器的接缝：协议字面量、来源同源校验、
// 非 http(s) 拒绝与 opener 失败消费。它不启动真实浏览器。
package shell_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

func TestHandleExternalBrowserMessage(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const sourceFrameURL = "http://127.0.0.1:7777/console?ticket=secret"
	const targetURL = "http://127.0.0.1:7777/cards?project=handoff"

	if shell.ExternalBrowserMessagePrefix != "handoff:open-browser:" {
		t.Fatalf("ExternalBrowserMessagePrefix = %q", shell.ExternalBrowserMessagePrefix)
	}

	t.Run("同源 cards query 通过 wire 串交给 opener", func(t *testing.T) {
		var opened string
		consumed := shell.HandleExternalBrowserMessage(
			log,
			"handoff:open-browser:http://127.0.0.1:7777/cards?project=handoff",
			sourceFrameURL,
			func(url string) error { opened = url; return nil },
		)
		if !consumed {
			t.Fatal("同协议消息必须被消费")
		}
		if opened != targetURL {
			t.Fatalf("opener URL = %q, want %q", opened, targetURL)
		}
	})

	t.Run("未知协议不消费", func(t *testing.T) {
		called := false
		consumed := shell.HandleExternalBrowserMessage(log, "other:message", sourceFrameURL, func(string) error {
			called = true
			return nil
		})
		if consumed {
			t.Fatal("未知协议不应被消费")
		}
		if called {
			t.Fatal("未知协议不应调用 opener")
		}
	})

	invalid := []struct {
		name   string
		target string
		source string
	}{
		{name: "javascript scheme", target: "javascript:alert(1)", source: sourceFrameURL},
		{name: "file scheme", target: "file:///tmp/cards", source: sourceFrameURL},
		{name: "ftp scheme", target: "ftp://127.0.0.1:7777/cards", source: sourceFrameURL},
		{name: "different host", target: "http://evil.example/cards", source: sourceFrameURL},
		{name: "different port", target: "http://127.0.0.1:7778/cards", source: sourceFrameURL},
		{name: "different scheme", target: "https://127.0.0.1:7777/cards", source: sourceFrameURL},
		{name: "userinfo", target: "http://user@127.0.0.1:7777/cards", source: sourceFrameURL},
		{name: "missing source", target: targetURL, source: ""},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			consumed := shell.HandleExternalBrowserMessage(
				log,
				shell.ExternalBrowserMessagePrefix+tc.target,
				tc.source,
				func(string) error { called = true; return nil },
			)
			if !consumed {
				t.Fatal("已识别协议的拒绝消息仍必须被消费")
			}
			if called {
				t.Fatal("被拒绝 URL 不得交给 opener")
			}
		})
	}

	t.Run("opener 错误仍被消费", func(t *testing.T) {
		called := false
		consumed := shell.HandleExternalBrowserMessage(
			log,
			shell.ExternalBrowserMessagePrefix+targetURL,
			sourceFrameURL,
			func(string) error { called = true; return errors.New("browser unavailable") },
		)
		if !consumed {
			t.Fatal("opener 失败的协议消息仍必须被消费")
		}
		if !called {
			t.Fatal("有效 URL 必须尝试调用 opener")
		}
	})
}
```

运行 `go test ./internal/shell/... -run '^TestHandleExternalBrowserMessage$'`；在 helper 尚不存在的基线预期编译失败。若出现既有 sync 测试失败，确认命令带了精确 `-run`，不要放宽为整包成功。

### Step 2 — 实现可测试的协议与 URL 校验

新建 `desktop/internal/shell/external_browser.go`，完整内容如下。实现不 import Wails，因而同一包普通 `go test` 能锁住桌面安全缝；`effectivePort` 把 http/https 的默认端口纳入 same-origin 判定；日志只记录字节数、scheme/host/path，不记录带 ticket 的来源完整 URL或完整 query。

```go
// 本文件负责把控制台外链页面发来的 raw message 安全地交给系统浏览器。
//
// 职责：识别 handoff:open-browser: 协议、校验目标与发送 frame 的 http(s) 同源，
//       再调用注入的 opener。
// 边界：不 import Wails、不启动浏览器、不导航 webview；main.go 只负责把
//       application.OriginInfo 与 app.Browser.OpenURL 接进来。
package shell

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// ExternalBrowserMessagePrefix 是前端 desktopShell.ts 与 Wails raw handler 共用的
// 协议前缀。不要改成 wails: 开头，否则 Wails 会先走内置窗口消息分发。
const ExternalBrowserMessagePrefix = "handoff:open-browser:"

// HandleExternalBrowserMessage 消费一条从外链控制台送来的系统浏览器请求。
//
// 参数：
//   - log：结构化日志入口；传 nil 时使用 slog.Default()
//   - message：WebKit external handler 传入的原始字符串
//   - sourceFrameURL：Wails OriginInfo.Origin，可能是带 path/query 的完整 frame URL
//   - open：系统浏览器 opener；生产实现为 app.Browser.OpenURL，测试注入 spy
//
// 返回：非本协议消息为 false；本协议消息无论校验拒绝或 opener 失败均为 true。
// 注意：只允许目标和 sourceFrameURL 的 scheme、hostname、有效端口相同，且目标必须
//       是 http(s) URL；来源/目标 URL 的完整内容不得写日志，避免泄露 ticket。
func HandleExternalBrowserMessage(log *slog.Logger, message, sourceFrameURL string, open func(string) error) bool {
	if !strings.HasPrefix(message, ExternalBrowserMessagePrefix) {
		return false
	}
	if log == nil {
		log = slog.Default()
	}
	log.Info("收到从控制台打开系统浏览器请求", "message_bytes", len(message))

	rawTarget := strings.TrimPrefix(message, ExternalBrowserMessagePrefix)
	target, err := validateExternalBrowserURL(rawTarget, sourceFrameURL)
	if err != nil {
		log.Warn("拒绝从控制台打开系统浏览器请求", "cause", err)
		return true
	}
	if open == nil {
		log.Error("系统浏览器 opener 未装配", "scheme", target.Scheme, "host", target.Hostname(), "path", target.EscapedPath())
		return true
	}

	log.Debug("调用系统浏览器", "scheme", target.Scheme, "host", target.Hostname(), "path", target.EscapedPath())
	if err := open(target.String()); err != nil {
		log.Error("调用系统浏览器失败", "scheme", target.Scheme, "host", target.Hostname(), "path", target.EscapedPath(), "cause", err)
		return true
	}
	log.Info("已调用系统浏览器", "scheme", target.Scheme, "host", target.Hostname(), "path", target.EscapedPath())
	return true
}

func validateExternalBrowserURL(rawTarget, sourceFrameURL string) (*url.URL, error) {
	if rawTarget == "" {
		return nil, errors.New("目标 URL 为空")
	}
	if strings.IndexFunc(rawTarget, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return nil, errors.New("目标 URL 含控制字符")
	}
	target, err := url.Parse(rawTarget)
	if err != nil {
		return nil, fmt.Errorf("目标 URL 无法解析: %w", err)
	}
	source, err := url.Parse(sourceFrameURL)
	if err != nil {
		return nil, errors.New("来源 frame URL 无法解析")
	}
	if !isHTTPURL(target) {
		return nil, errors.New("目标 URL 必须是带 host 的 http(s) 地址")
	}
	if !isHTTPURL(source) {
		return nil, errors.New("来源 frame URL 必须是带 host 的 http(s) 地址")
	}
	if target.User != nil || source.User != nil {
		return nil, errors.New("URL 不允许 userinfo")
	}
	if !sameOrigin(target, source) {
		return nil, errors.New("目标 URL 与来源 frame 不同源")
	}
	target.Scheme = strings.ToLower(target.Scheme)
	return target, nil
}

func isHTTPURL(candidate *url.URL) bool {
	scheme := strings.ToLower(candidate.Scheme)
	return (scheme == "http" || scheme == "https") && candidate.Hostname() != ""
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(candidate *url.URL) string {
	if port := candidate.Port(); port != "" {
		return port
	}
	switch strings.ToLower(candidate.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}
```

运行 Step 1 的 Go 红测至绿，再运行 Task 2 的带基线 regex 命令。必须逐条看到：同源 `/cards?project=handoff` 交给 opener；`javascript:`, `file:`, `ftp:`, 跨 host、跨 port、跨 scheme、userinfo、空来源都不调用 opener；未知前缀返回 false；opener 错误不 panic 且消息被消费。

### Step 3 — 在 `desktop/main.go` 装配 Wails raw handler

把当前 `app := application.New(application.Options{...})` 整块替换为下列完整代码；其它窗口尺寸、`MacTitleBarHidden`、`InvisibleTitleBarHeight`、事件与启动序列不动。预声明 `app` 是为了让回调安全捕获同一个 `App` 指针；回调只会在 `application.New` 返回并进入运行态后由 Wails 投递。`Origin` 为空时回退 `TopOrigin`，两者都为空时让 shell helper 明确拒绝。

```go
	var app *application.App
	app = application.New(application.Options{
		Name:        "handoff-desktop",
		Description: "handoff 控制台桌面壳",
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac: application.MacOptions{
			// 承重：关掉最后一个窗口时进程必须活着，托盘才谈得上常驻
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		// 外链控制台没有 Wails 前端 runtime 的公开 binding；这里只接收
		// desktopShell.ts 的固定 raw 协议，URL 的同源/http(s) 校验在 shell 包完成。
		RawMessageHandler: func(_ application.Window, message string, originInfo *application.OriginInfo) {
			sourceFrameURL := ""
			if originInfo != nil {
				sourceFrameURL = originInfo.Origin
				if sourceFrameURL == "" {
					sourceFrameURL = originInfo.TopOrigin
				}
			}
			if !shell.HandleExternalBrowserMessage(logger, message, sourceFrameURL, app.Browser.OpenURL) {
				logger.Debug("忽略未识别的原始宿主消息", "message_bytes", len(message))
			}
		},
	})
```

这里使用 `app.Browser.OpenURL`，不使用 `window.SetURL`，所以桌面 webview 的 `/cards` 路由和滚动位置不变；也不调用 `app.Quit`、`win.SetURL` 或任何 agentd API。raw handler 只处理 `handoff:open-browser:`，现有 `wails:drag:doubleclick` 仍由 Wails beta.8 内置分发处理。

### Step 4 — 编译装配并跑桌面绿测

先运行：

```sh
cd desktop
go test ./internal/shell/... -run '^TestHandleExternalBrowserMessage$'
go test ./internal/shell/... -run '^(TestHandleExternalBrowserMessage|TestResolve|TestNormalizeProjectDir|TestAwaitWebviewReady|TestConsoleURL|TestDefaultDeviceName|TestBuildForm|TestApplyAnswers)'
```

然后在有 Wails CLI 的环境按仓库 README 运行：

```sh
cd desktop
wails3 task build
```

预期：helper 两条命令退出 0；Wails Taskfile 成功生成并编译桌面程序。`desktop/frontend/dist/`、`desktop/bin/` 等构建产物保持被忽略，不加入提交。若环境仍没有 `wails3` 或整包被既有 `/tmp/handoff` 夹具阻断，台账记录原始错误，不把它改写成业务失败或通过。

### Step 5 — 桌面日志、注释和边界检查

确认新文件头、`HandleExternalBrowserMessage`、`validateExternalBrowserURL` 的注释完整说明职责、输入/返回/注意事项和“为什么比较有效端口而不是来源 URL 全串”。检查每个新分支都有结构化 slog：协议入口带 message 字节数，URL 拒绝带 cause，opener 缺失带 scheme/host/path，调用前后与调用失败带 scheme/host/path；不使用 `fmt.Print*`、`log.Print*` 或 `println`。来源完整 URL、query 与消息 payload 不写日志，避免把一次性 ticket 带出。

## 跨 task 接口逐字核对

Task 1 的 Produces 与 Task 2 的 Consumes 必须保持下列逐字符关系：

```text
web/src/app/lib/desktopShell.ts:
  OPEN_BROWSER_MESSAGE_PREFIX = 'handoff:open-browser:'
  requestOpenCurrentPageInBrowser(): boolean
  postMessage(`${OPEN_BROWSER_MESSAGE_PREFIX}${window.location.origin}${window.location.pathname}${window.location.search}`)

desktop/internal/shell/external_browser.go:
  ExternalBrowserMessagePrefix = "handoff:open-browser:"
  HandleExternalBrowserMessage(log *slog.Logger, message, sourceFrameURL string, open func(string) error) bool
```

Task 2 的 `main.go` 将 `sourceFrameURL` 取自 `OriginInfo.Origin`，空值回退 `TopOrigin`，将 `app.Browser.OpenURL` 作为 `open`；不存在新 HTTP endpoint、JSON DTO、账本字段或 Wails binding 名称。

## 五项自审与验收栏

### 1. spec 故事覆盖

| spec 故事 / 判据 | 负责 task | 锁点 |
|---|---|---|
| 桌面 `/cards` 右上角有按钮，点击系统浏览器打开同一 `/cards` 整页，桌面仍停留 | Task 1 + Task 2 | `CardsPage 从浏览器打开` 的桌面 UA 点击测试断言完整 path/query、原地址仍在；Go 同源用例断言 opener 收到同一 URL；Wails 装配使用 `App.Browser.OpenURL`，不导航 webview |
| Chrome/Safari 普通浏览器没有按钮，布局与今天一致 | Task 1 | 普通 UA `queryByRole` 为 null，健康点保留 `ml-auto`；`requestOpenCurrentPageInBrowser` 无 bridge 返回 false |
| 按钮在标题栏拖动区之外，第一次按下是点击而非拖窗 | Task 1 | `Shell.tsx` 既有 `/cards` → `FullPageCover` 位于 `DesktopTitleBar` 下方；按钮属于 CardsPage header；既有 `DesktopTitleBar.test.tsx` 继续断言标题栏无可点元素，未把按钮加入标题栏 |
| 仅同源 http(s) 打开，其他 URL 拒绝 | Task 2 | `HandleExternalBrowserMessage` 的同源/有效端口/http(s)/userinfo/控制字符校验与逐条负向表 |

### 2. 缺陷族对抗审查

依据 `docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md:78-89` 的通用五族、序列化边界族与 webview/平台族：

| 缺陷族 | 反问 | 本计划结论与锁点 |
|---|---|---|
| 生命周期 / 状态机中断 | 点击后是否导航、关窗、等待异步状态，导致桌面页消失或重复动作？ | 前端 helper 只发 raw message，不 `navigate`、不改 CardsPage state；每次点击只调用一次 bridge；Go opener 失败被消费并记录，不影响 webview。真机重复点击列入清单。 |
| 静默失败 / 误导报错 | bridge 缺失、来源缺失、URL 拒绝、系统 opener 失败是否都能解释？ | 前端无 bridge 返回 false；Go 入口、拒绝、nil opener、调用前后和错误均 slog，消息仍消费；不把“系统浏览器已打开”写在调用失败分支。 |
| 跨平台假设 | 一个 WKWebView 绿是否能推广到 Chromium/Windows/Linux？ | `Origin` 按 frame URL 解析而非 Darwin 全串；`App.Browser.OpenURL` 委托 Wails 三平台实现；真机清单分别覆盖 macOS WKWebView 与可用的 Linux/Windows 构建，Linux/Windows 不能用 macOS 结果代替。 |
| 假红测试 | 测试是否只测 helper 或合成事件，漏掉真实按钮到 bridge 的线？ | CardsPage 测试真实渲染、真实 UA、真实 `postMessage` spy、真实 click；Go 测试从 `HandleExternalBrowserMessage` 入口进入；两边都不直接测未导出的内部函数。 |
| 门禁绕过 | 是否绕开同源校验、引入新 HTTP/API 或把按钮藏进不可点击标题栏？ | 只有固定 raw prefix 进入 helper；非 http(s)/跨 origin 不调用 opener；无 agentd HTTP；DesktopTitleBar 与 28px 常量不改；`wails3 task build` 是 main 接线的装配门。 |
| 序列化边界 | 前端发出的字符串和 Go 消费的 prefix/URL 是否漂移，空值/零值是否混淆？ | 不引入 DTO；wire 是固定前缀+当前 URL 字符串。前端测试断言完整 `postMessage` 字符串，Go 测试用同一完整 literal 穿过 raw handler 并断言 opener URL，形成一条真实字符串边界回归；空来源与空目标分别有拒绝断言。 |
| 新增枚举值白名单 | 是否新增 state/kind 但忘了既有 switch？ | 不新增枚举、账本字段或 API 值；本族不适用。 |
| webview / 平台表现差异 | 标题栏吞点击、外链来源格式、系统打开方式是否只在单一环境成立？ | `FullPageCover` 与 `DesktopTitleBar` 源码/既有测试锁几何边界；Wails Origin 依赖源码已核对；三平台 opener 依赖出处已写明；必须执行下面的真机清单。 |

### 3. 序列化边界清单

1. `web/src/app/lib/desktopShell.ts`：`window.location.origin/pathname/search` 手工投影为 `handoff:open-browser:` + URL 字符串；`desktopShell.test.ts` 断言完整消息。
2. Wails `external.postMessage` → `Options.RawMessageHandler`：Wails 将非 `wails:` 字符串原样传给 Go；`external_browser_test.go` 用同一 wire literal 作为 handler 输入。
3. `desktop/main.go`：`OriginInfo.Origin`/`TopOrigin` 手工投影为 `sourceFrameURL`；Go 测试以带 `/console?ticket=secret` 的 source URL 验证 path/query 不参与 origin 判定且不被 opener/log 输出。
4. `desktop/internal/shell/external_browser.go`：raw suffix 解析为 `url.URL`，只把校验后的 `target.String()` 交给 `app.Browser.OpenURL`；成功用例断言 exact target，负向用例断言 opener 零调用。

### 4. 类型标注与真机清单

边界类型已钉死：前端 bridge 的 `postMessage(msg: string) => void`、Go raw callback 的 `message string`、`sourceFrameURL string`、`open func(string) error`、Wails `OriginInfo` 三字段；不要以 `unknown` 或类型断言绕过这些接口，`unknown` 只保留在已有的 `window as unknown as WebkitBridge` bridge 边界。

具备桌面工具链后，由协调者或实现者逐项在真实机器执行并记录结果：

1. macOS Wails：加载登录后的 `/cards?project=handoff`，UA 含 `handoff-desktop`；按钮出现在 `DesktopTitleBar` 的 28px 下方顶栏右侧，首次点击后系统默认浏览器出现同源 `/cards?project=handoff`，桌面 webview 仍显示该 CardsPage，滚动位置未被导航重置。
2. macOS Wails：点击按钮的坐标不在顶部 28px 原生拖动区；另做一次标题栏双击，既有 `wails:drag:doubleclick` 最大化/还原行为仍在，按钮没有拖窗副作用。
3. Chrome 与 Safari：同源 `/cards?project=handoff` 页面没有“从浏览器打开”按钮，健康点与既有控件保持右对齐，页面无 raw bridge 依赖报错。
4. 可用的 Linux Wails 与 Windows Wails：分别确认 frame source URL 能通过同源比较、默认浏览器被调用；同源之外的 host/port/scheme 不调用系统 opener。若平台工具链不可用，记录“未验证”及原始构建错误，不用另一平台结果代替。

### 5. 接缝双向覆盖

| spec 接缝 | 测试入口（测试 → 缝） | 反向覆盖（缝 → 测试） |
|---|---|---|
| `isDesktopShell` / CardsPage 渲染 | `CardsPage 从浏览器打开` → `renderPage` → `CardsPage`；真实 UA 分支 | 桌面按钮存在、普通浏览器不存在两条断言 |
| 点击 → 当前 URL raw message | `CardsPage 从浏览器打开` → `fireEvent.click(button)`；`requestOpenCurrentPageInBrowser` → `postMessage` | `desktopShell.test.ts` 断言固定 prefix/path/query；CardsPage 测试断言真实点击只发一次 |
| 薄壳校验 → 系统浏览器 | `TestHandleExternalBrowserMessage` → `HandleExternalBrowserMessage` → 注入 `open` | 同源成功、非 http(s)、跨 host/port/scheme、userinfo、空来源和 opener 错误均在同一声明缝锁住 |
| 标题栏回归 / 几何隔离 | `DesktopTitleBar.test.tsx` → `DesktopTitleBar` | 既有“标题栏里一个可点元素都不能有”继续通过；CardsPage 桌面按钮用 `ml-auto`，不改标题栏 |

没有内部锁替代声明缝断言：URL 解析的所有负向例都通过导出的 raw handler 入口；bridge 测试也不直接调用内部 URL 拼接函数。

## 收口门禁

实现者完成 Task 1、Task 2 后，在变更工作树运行：

```sh
cd web
npx vitest run src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
npm run typecheck
npx eslint src/app/cards/CardsPage.tsx src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.ts src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx

cd ../desktop
go test ./internal/shell/... -run '^(TestHandleExternalBrowserMessage|TestResolve|TestNormalizeProjectDir|TestAwaitWebviewReady|TestConsoleURL|TestDefaultDeviceName|TestBuildForm|TestApplyAnswers)'
```

在 Wails CLI 可用的 runner 再执行 `cd desktop && wails3 task build`。检查 `git diff --check`；检查改动只落在本计划允许的四个前端文件、三个桌面源文件及本计划/台账；检查 `desktop/frontend/dist` 与 `desktop/bin` 未被 staged。全量 Vitest、全仓 Go test、跨卡独立审计和真实机器验收不属于单个实现 task 的替代，由协调者在集成阶段执行。

## Spec 归属与跨卡审计边界

B266 的三个用户故事与四条测试接缝全部归属本计划的 Task 1/Task 2，B265、B266 之外的标题栏改造、B266 以外的浏览器新窗口按钮均不归属本计划。不存在需要 Task 1/Task 2 之间再发明别名的接口；跨卡冻结物逐条核对、其他卡 Produces/Consumes 逐字比对与全 spec 故事总归属由协调者在全部子卡 plan 齐稿后独立审计，本节点不宣称已完成该跨卡审计。
