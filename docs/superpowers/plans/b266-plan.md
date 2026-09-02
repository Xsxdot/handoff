# B266 增量实现计划：桌面端带会话打开工作项整页

读者：对仓库零上下文的执行者。依据已批准的 docs/superpowers/specs/b266.md 和本卡补充的验收增量。本节点只写计划，不写实现代码。

## 0. 起点、范围与冻结接口

### 0.1 起点

实现者必须以 cards/B266-charter-5 的提交 1a40bd82c409def43509c6f2f7b556c33db9a7d1 为代码起点做增量；当前执行分支 cards/B266-charter-6 的 HEAD 正是该提交。不要从没有按钮的 origin/main 重做。

该提交已经包含且本计划不重做：

- CardsPage 桌面 UA 按钮、普通浏览器隐藏、标题栏外布局；
- desktopShell.ts 的 handoff:open-browser: + 当前 origin/pathname/search raw wire；
- desktop/internal/shell/external_browser.go 的同源 http(s) 校验；
- desktop/main.go 的 Wails RawMessageHandler、Origin/TopOrigin 回退和 app.Browser.OpenURL；
- 真机已证明按钮能打开浏览器，但目标是无会话 /cards 并停在登录页。

本轮只补：同源校验通过后签发 IssueAuthTicket，打开带 ticket 和 next 的 /console；agentd 兑换成功后安全地 302 到 /cards path/query。

### 0.2 依据与明确排除

当前 checkout 的 origin/main 为 c23e53a0a20c8aeeddeb33a2d02c49e8d8c925be，B266 spec 文件存在，最后改动提交为 6630abeb；用户消息中的 d098dae7f 在本地对象库不存在，计划不伪造该 hash，按现有 origin/main 文件执行。

允许修改的源码/测试文件：

- desktop/internal/shell/external_browser.go
- desktop/internal/shell/external_browser_test.go
- desktop/internal/shell/handshake.go
- desktop/internal/shell/handshake_test.go
- desktop/main.go
- internal/agentd/authroutes.go
- internal/agentd/auth_test.go

本节点同时更新：

- docs/superpowers/plans/b266-plan.md
- docs/superpowers/ledgers/2026-08-29-b266-plan-ledger.md

不修改 web/src/app/cards/CardsPage.tsx、web/src/app/lib/desktopShell.ts、web/src/app/shell/DesktopTitleBar.tsx、web/src/app/shell/Shell.tsx、internal/client、internal/proto、store schema、server.go 路由注册或任何新的 HTTP endpoint。Wails E2E、真实机器 postMessage 穿透、系统浏览器真开不属于本卡计划项；B296 台账 hash 自洽问题忽略。

### 0.3 行为冻结

输入仍是已有前端 raw wire：

~~~text
handoff:open-browser:http://127.0.0.1:7777/cards?project=handoff
~~~

桌面侧处理顺序固定为：

1. 识别固定前缀；
2. 校验 target 和 sourceFrameURL 的 http(s) 同源、有效端口、无 userinfo；
3. 将已校验 target 投影为相对 next：/cards?project=handoff；
4. 调用 IssueAuthTicket；
5. 在 agentd 返回的 /console ticket query 上设置 URL query next；
6. 只把最终 /console URL 交给 app.Browser.OpenURL。

IssueAuthTicket 失败、配置未就绪、opener 缺失和 opener 失败都消费 raw message，绝不调用 opener(target)，绝不回退裸 /cards。

next 允许 /cards 或 /cards/ 前缀的相对 path/query；拒绝空值、/other、/cardsx、无首斜杠、// 网络路径、scheme、host、userinfo、fragment、反斜杠和控制字符。agentd 自己再次执行这条白名单，不能只信桌面端。

## 1. 基线与查图事实

### 1.1 本节点实跑基线

前端既有三缝回归：

~~~text
cd web
npx vitest run src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
~~~

输出：Test Files 3 passed (3)，Tests 20 passed (20)。

~~~text
cd web
npm run typecheck
npx eslint src/app/cards/CardsPage.tsx src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.ts src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
~~~

实际：tsc -b 退出 0；定向 eslint 无输出、退出 0。node_modules 由 npm ci --cache /root/.handoff/tmp/6597ae3a/npm-cache 安装到忽略目录，输出 added 290 packages、found 0 vulnerabilities，不得 staged。

桌面既有 shell 回归：

~~~text
cd desktop
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/shell/... -run '^(TestHandleExternalBrowserMessage|TestConsoleURL|TestDefaultDeviceName)$' -count=1
~~~

输出：ok github.com/Xsxdot/handoff/desktop/internal/shell 0.002s。

agentd 既有认证/路由回归：

~~~text
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/agentd -run '^(TestTicketToCookieHappyPath|TestTicketSingleUseOverHTTP|TestExpiredTicketRejected|TestConsoleRouteRegistered|TestDeepLinkRouteFallsBack)$' -count=1
~~~

输出：ok github.com/Xsxdot/handoff/internal/agentd 3.553s。

Wails 装配门：

~~~text
cd desktop
wails3 task build
~~~

原始输出：/bin/bash: line 1: wails3: command not found。实现后仍运行该命令；不可用时台账保留原文并写“未验证”，不能写 pass。

### 1.2 已核对的接口

- internal/client/client.go:1231：
  func (c *Client) IssueAuthTicket(ctx context.Context, deviceName string) (*proto.AuthTicketResp, error)
- desktop/internal/shell/handshake.go:47：
  func ConsoleURL(ctx context.Context, ep Endpoint, deviceName string) (string, error)
- desktop/internal/shell/endpoint.go:59：
  func Resolve(path string) (Endpoint, ConfigState, error)
- internal/agentd/authroutes.go:132：
  func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request)
- internal/agentd/server.go:615 已注册 GET /console，保持注册不动。

### 1.3 查图与覆盖债

按项目不变量运行 codegraph context d_web_cards，结果 truncated=false、unscannedEntries=6、best 领域无实体/边；4 个 web 容器实际在 d_web。sym CardsPage 定位到 web/src/app/cards/CardsPage.tsx:64；sym consoleURL 定位到 func consoleURL(r *http.Request, ticket string) string；sym handleConsole 定位到 Server.handleConsole；flow consoleURL 返回 degraded=true、steps=[]，不能当流程图；who-calls consoleURL 显示 handleIssueTicket 和 POST /api/auth/tickets。

下列两个完整命令的 sym 查询均返回：

~~~text
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym HandleExternalBrowserMessage
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym ConsoleURL
Error: 符号 "HandleExternalBrowserMessage" 不在图中（图未覆盖或名字有误）
Error: 符号 "ConsoleURL" 不在图中（图未覆盖或名字有误）
~~~

新增 desktop shell raw handler/握手是图覆盖债；6 个未扫描入口不能证明没有消费者，调用面以源码和本计划文件清单为准。

## 2. Task 1：桌面 shell 认证打开

### 2.1 文件与接口

修改五个文件：

- desktop/internal/shell/external_browser.go
- desktop/internal/shell/external_browser_test.go
- desktop/internal/shell/handshake.go
- desktop/internal/shell/handshake_test.go
- desktop/main.go

新增精确接口：

~~~go
func ConsoleURLWithNext(ctx context.Context, ep Endpoint, deviceName, next string) (string, error)

func HandleExternalBrowserMessage(
    log *slog.Logger,
    message, sourceFrameURL string,
    issue func(next string) (string, error),
    open func(string) error,
) bool
~~~

ConsoleURLWithNext 先校验 next，再复用 ConsoleURL，也就是既有 IssueAuthTicket；用 net/url 的 Query.Set 编码 next，保留 ticket，返回带凭据的 /console URL但不记录它。

HandleExternalBrowserMessage 的 issue 只在同源且 next 白名单通过后调用；issue 返回最终带 ticket/next 的 console URL，open 接收该最终 URL。非协议返回 false；已识别协议即使拒绝、签票失败、opener 缺失或 opener 失败也返回 true。

### 2.2 Step 1：重跑基线、写红测、跑红

先运行 1.1 的桌面 shell 命令。然后把既有 external_browser_test.go 的四参数调用改成五参数，并替换测试主体为下列锁缝断言；复用既有 slog、opener spy，不直接调用私有 validator。

~~~go
func TestHandleExternalBrowserMessage(t *testing.T) {
    var logs bytes.Buffer
    log := slog.New(slog.NewTextHandler(&logs, nil))
    const source = "http://127.0.0.1:7777/console?ticket=source-secret"
    const target = "http://127.0.0.1:7777/cards?project=handoff"
    const browserURL = "http://127.0.0.1:7777/console?ticket=issued-secret&next=%2Fcards%3Fproject%3Dhandoff"

    if shell.ExternalBrowserMessagePrefix != "handoff:open-browser:" {
        t.Fatalf("prefix = %q", shell.ExternalBrowserMessagePrefix)
    }

    t.Run("同源先签票再打开 console", func(t *testing.T) {
        var gotNext, opened string
        consumed := shell.HandleExternalBrowserMessage(
            log, shell.ExternalBrowserMessagePrefix+target, source,
            func(next string) (string, error) {
                gotNext = next
                return browserURL, nil
            },
            func(url string) error {
                opened = url
                return nil
            },
        )
        if !consumed {
            t.Fatal("协议消息必须被消费")
        }
        if gotNext != "/cards?project=handoff" {
            t.Fatalf("issue next = %q", gotNext)
        }
        if opened != browserURL {
            t.Fatalf("opened = %q, want %q", opened, browserURL)
        }
    })

    invalid := []struct {
        name, target, source string
    }{
        {"javascript", "javascript:alert(1)", source},
        {"file", "file:///tmp/cards", source},
        {"ftp", "ftp://127.0.0.1:7777/cards", source},
        {"host", "http://evil.example/cards", source},
        {"port", "http://127.0.0.1:7778/cards", source},
        {"scheme", "https://127.0.0.1:7777/cards", source},
        {"userinfo", "http://user@127.0.0.1:7777/cards", source},
        {"source missing", target, ""},
        {"wrong path", "http://127.0.0.1:7777/other", source},
        {"prefix lookalike", "http://127.0.0.1:7777/cardsx", source},
        {"backslash", "http://127.0.0.1:7777/cards\\evil", source},
        {"fragment", "http://127.0.0.1:7777/cards#fragment", source},
        {"encoded control", "http://127.0.0.1:7777/cards?x=%01", source},
    }
    for _, tc := range invalid {
        t.Run(tc.name, func(t *testing.T) {
            issued, opened := false, false
            consumed := shell.HandleExternalBrowserMessage(
                log, shell.ExternalBrowserMessagePrefix+tc.target, tc.source,
                func(string) (string, error) {
                    issued = true
                    return browserURL, nil
                },
                func(string) error {
                    opened = true
                    return nil
                },
            )
            if !consumed {
                t.Fatal("已识别的拒绝消息必须被消费")
            }
            if issued || opened {
                t.Fatalf("拒绝消息不应签票/打开：issued=%v opened=%v", issued, opened)
            }
        })
    }

    t.Run("签票失败不回退裸 target 且日志不泄露", func(t *testing.T) {
        opened := false
        consumed := shell.HandleExternalBrowserMessage(
            log, shell.ExternalBrowserMessagePrefix+target, source,
            func(string) (string, error) {
                return "", errors.New("agentd unavailable ticket=issued-secret")
            },
            func(string) error {
                opened = true
                return nil
            },
        )
        if !consumed || opened {
            t.Fatalf("签票失败结果 consumed=%v opened=%v", consumed, opened)
        }
        if strings.Contains(logs.String(), "issued-secret") ||
            strings.Contains(logs.String(), target) ||
            strings.Contains(logs.String(), source) {
            t.Fatalf("日志泄露 URL/ticket：%q", logs.String())
        }
    })

    t.Run("opener 失败仍消费且日志不泄露", func(t *testing.T) {
        logs.Reset()
        consumed := shell.HandleExternalBrowserMessage(
            log, shell.ExternalBrowserMessagePrefix+target, source,
            func(string) (string, error) { return browserURL, nil },
            func(string) error { return errors.New("browser unavailable ticket=issued-secret") },
        )
        if !consumed {
            t.Fatal("opener 失败消息必须被消费")
        }
        if strings.Contains(logs.String(), "issued-secret") ||
            strings.Contains(logs.String(), target) ||
            strings.Contains(logs.String(), source) {
            t.Fatalf("日志泄露 URL/ticket：%q", logs.String())
        }
    })
}
~~~

在 handshake_test.go 追加下列真实 HTTP 序列化锁；复用现有 httptest Server 形态：

~~~go
func TestConsoleURLWithNextPreservesTicketAndEscapesNext(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/api/auth/tickets" {
            t.Fatalf("path = %q", r.URL.Path)
        }
        w.Header().Set("Content-Type", "application/json")
        _, _ = io.WriteString(w,
            "{\"url\":\"http://127.0.0.1:7777/console?ticket=deadbeef\",\"expires_at\":\"2030-01-01T00:00:00Z\"}")
    }))
    defer ts.Close()

    ep := Endpoint{Addr: strings.TrimPrefix(ts.URL, "http://"), Token: "tok"}
    got, err := ConsoleURLWithNext(context.Background(), ep, "我的 mac", "/cards?project=handoff")
    if err != nil {
        t.Fatalf("ConsoleURLWithNext: %v", err)
    }
    parsed, err := url.Parse(got)
    if err != nil {
        t.Fatalf("parse URL: %v", err)
    }
    if parsed.Query().Get("ticket") != "deadbeef" {
        t.Fatalf("ticket changed: %q", parsed.Query().Get("ticket"))
    }
    if parsed.Query().Get("next") != "/cards?project=handoff" {
        t.Fatalf("next = %q", parsed.Query().Get("next"))
    }
    if strings.Contains(got, "project=handoff") {
        t.Fatalf("next 未编码: %q", got)
    }
}
~~~

运行：

~~~text
cd desktop
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/shell/... -run '^(TestHandleExternalBrowserMessage|TestConsoleURLWithNext|TestConsoleURL|TestDefaultDeviceName)$' -count=1
~~~

红判据：现有 handler 参数不足或 ConsoleURLWithNext 未定义导致真实编译/测试失败；首个原始红输出追加台账，不能把预期红写成已验证通过。

### 2.3 Step 2：最小实现

在 handshake.go 既有 ConsoleURL 后加入以下完整导出函数；新增 errors、net/url import。先校验 next，是为了不白白消费一次性 ticket。

~~~go
// ConsoleURLWithNext 换一张 ticket，并把受限的工作项相对路径编码进 /console。
// next 只允许 /cards 或 /cards/ 前缀的 path/query；返回 URL 含 ticket，不能记录。
func ConsoleURLWithNext(ctx context.Context, ep Endpoint, deviceName, next string) (string, error) {
    if err := validateCardsNext(next); err != nil {
        return "", errors.New("工作项跳转路径非法")
    }
    base, err := ConsoleURL(ctx, ep, deviceName)
    if err != nil {
        return "", err
    }
    parsed, err := url.Parse(base)
    if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
        return "", errors.New("agentd 返回的控制台 URL 非法")
    }
    query := parsed.Query()
    query.Set("next", next)
    parsed.RawQuery = query.Encode()
    parsed.Fragment = ""
    return parsed.String(), nil
}
~~~

在 external_browser.go 中保留已有同源函数，并使导出 handler 完整符合下列控制流：

~~~go
func HandleExternalBrowserMessage(
    log *slog.Logger,
    message, sourceFrameURL string,
    issue func(next string) (string, error),
    open func(string) error,
) bool {
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
        log.Warn("拒绝从控制台打开系统浏览器请求",
            "result", "invalid_target", "cause", err)
        return true
    }
    next, err := nextFromTarget(target)
    if err != nil {
        log.Warn("拒绝从控制台打开系统浏览器请求",
            "result", "invalid_next", "cause", err)
        return true
    }
    if issue == nil {
        log.Error("打开系统浏览器失败",
            "result", "issuer_unconfigured", "path", target.EscapedPath())
        return true
    }
    if open == nil {
        log.Error("打开系统浏览器失败",
            "result", "opener_unconfigured", "path", target.EscapedPath())
        return true
    }

    browserURL, err := issue(next)
    if err != nil || browserURL == "" {
        log.Error("打开系统浏览器失败",
            "result", "issue_auth_ticket_failed", "path", target.EscapedPath())
        return true
    }
    log.Debug("调用系统浏览器",
        "result", "opening", "path", target.EscapedPath())
    if err := open(browserURL); err != nil {
        log.Error("调用系统浏览器失败",
            "result", "open_failed", "path", target.EscapedPath())
        return true
    }
    log.Info("已调用系统浏览器",
        "result", "opened", "path", target.EscapedPath())
    return true
}
~~~

日志不写 issue/open error 原文、browserURL、target.String()、sourceFrameURL；path 不带 query，可保留用于排障。

加入下列包内投影/白名单函数；它们只生成 path/query，不把完整 target 交给 opener：

~~~go
func nextFromTarget(target *url.URL) (string, error) {
    if target.Fragment != "" {
        return "", errors.New("目标 URL 不允许 fragment")
    }
    next := target.EscapedPath()
    if next == "" {
        next = "/"
    }
    if target.RawQuery != "" {
        next += "?" + target.RawQuery
    }
    if err := validateCardsNext(next); err != nil {
        return "", err
    }
    return next, nil
}

func validateCardsNext(next string) error {
    if next == "" {
        return errors.New("next 为空")
    }
    if strings.IndexFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
        return errors.New("next 含控制字符")
    }
    if strings.Contains(next, "\\") || strings.HasPrefix(next, "//") {
        return errors.New("next 含禁止字符")
    }
    parsed, err := url.Parse(next)
    if err != nil || parsed.IsAbs() || parsed.Scheme != "" ||
        parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
        return errors.New("next 必须是无 host 的相对 path")
    }
    if strings.Contains(parsed.Path, "\\") ||
        strings.IndexFunc(parsed.Path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
        return errors.New("next path 含禁止字符")
    }
    for _, values := range parsed.Query() {
        for _, value := range values {
            if strings.Contains(value, "\\") ||
                strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
                return errors.New("next query 含禁止字符")
            }
        }
    }
    if parsed.Path != "/cards" && !strings.HasPrefix(parsed.Path, "/cards/") {
        return errors.New("next 不是 /cards 前缀")
    }
    return nil
}
~~~

### 2.4 Step 3：main.go 装配

在 application.New 前增加闭包；在已有 RawMessageHandler 中把它作为 issue 参数，并保留既有 Origin 优先、TopOrigin 回退。闭包完整形态：

~~~go
issueExternalBrowserURL := func(next string) (string, error) {
    ep, state, err := shell.Resolve("")
    if err != nil {
        logger.Error("读取 agentd 配置失败",
            "result", "resolve_failed", "cause", err)
        return "", err
    }
    if state != shell.StateConfigured {
        err := fmt.Errorf("agentd 尚未配置")
        logger.Error("打开系统浏览器失败",
            "result", "agentd_unconfigured", "state", state.String())
        return "", err
    }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    return shell.ConsoleURLWithNext(ctx, ep, shell.DefaultDeviceName(), next)
}

var app *application.App
app = application.New(application.Options{
    Name:        "handoff-desktop",
    Description: "handoff 控制台桌面壳",
    Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
    Mac: application.MacOptions{
        ApplicationShouldTerminateAfterLastWindowClosed: false,
    },
    RawMessageHandler: func(_ application.Window, message string, originInfo *application.OriginInfo) {
        sourceFrameURL := ""
        if originInfo != nil {
            sourceFrameURL = originInfo.Origin
            if sourceFrameURL == "" {
                sourceFrameURL = originInfo.TopOrigin
            }
        }
        if !shell.HandleExternalBrowserMessage(
            logger, message, sourceFrameURL,
            issueExternalBrowserURL, app.Browser.OpenURL,
        ) {
            logger.Debug("忽略未识别的原始宿主消息",
                "message_bytes", len(message))
        }
    },
})
~~~

这段必须合并现有 Options，不得覆盖窗口配置。app.Browser.OpenURL 是唯一系统浏览器出口；不使用 win.SetURL、app.Quit、agentd 新 API。Wails E2E 不补入计划。

### 2.5 Step 4：Task 1 绿测和注释/日志门

~~~text
cd desktop
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/shell/... -run '^(TestHandleExternalBrowserMessage|TestConsoleURLWithNext|TestConsoleURL|TestDefaultDeviceName)$' -count=1
gofmt -d internal/shell/external_browser.go internal/shell/external_browser_test.go internal/shell/handshake.go internal/shell/handshake_test.go
rg -n 'fmt\.Print|log\.Print|println|target\.String\(\)|browserURL|sourceFrameURL' internal/shell/external_browser.go desktop/main.go
~~~

测试必须真实退出 0；gofmt 无输出；rg 命中时逐项确认不是把 URL/ticket 放入生产日志。文件头和导出函数注释写清职责、边界、参数、返回、为什么先校验 next 和为什么不 fallback 裸 target。每个入口、拒绝、签票失败、opener 缺失/失败、成功路径都有 slog。

## 3. Task 2：agentd /console next redirect

### 3.1 文件与接口

修改：

- internal/agentd/authroutes.go
- internal/agentd/auth_test.go

保持 GET /console 注册、ticket 原子消费、会话建立、Set-Cookie、失败 401 全部不变。增加：

~~~go
func validConsoleNext(next string) bool
~~~

唯一允许条件：next 非空、无 raw/解码控制字符、无反斜杠、不以 // 开头；url.Parse 成功且无绝对 scheme、host、userinfo、fragment；parsed.Path 是 /cards 或 /cards/ 前缀。验证 query 解码值中的控制字符和反斜杠。它是浏览器直接构造 /console 时的最终 open-redirect 防线。

### 3.2 Step 1：基线、红测

先运行 1.1 的 agentd 命令。然后在 auth_test.go 复用 newHostTestEnv、issueTicket、noRedirectClient 追加下列完整测试；每支经真实 GET /console，不直接调用 validator：

~~~go
func ticketURLWithNext(t *testing.T, ts *httptest.Server, next string) string {
    t.Helper()
    tk := issueTicket(t, ts, "桌面端")
    parsed, err := url.Parse(tk.URL)
    if err != nil {
        t.Fatalf("解析 ticket URL: %v", err)
    }
    query := parsed.Query()
    query.Set("next", next)
    parsed.RawQuery = query.Encode()
    return parsed.String()
}

func TestTicketNextRedirectsToCardsPathAndQuery(t *testing.T) {
    _, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
    resp, err := noRedirectClient(ts).Get(
        ticketURLWithNext(t, ts, "/cards?project=handoff"))
    if err != nil {
        t.Fatalf("兑换 ticket: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusFound {
        t.Fatalf("status = %d, want 302", resp.StatusCode)
    }
    if loc := resp.Header.Get("Location"); loc != "/cards?project=handoff" {
        t.Fatalf("Location = %q", loc)
    }
    cookieFound := false
    for _, cookie := range resp.Cookies() {
        if cookie.Name == sessionCookieName && cookie.Value != "" {
            cookieFound = true
        }
    }
    if !cookieFound {
        t.Fatal("合法 next 兑换没有会话 cookie")
    }
}

func TestTicketInvalidNextFallsBackToRoot(t *testing.T) {
    cases := []struct {
        name, next string
    }{
        {"empty", ""},
        {"other path", "/other"},
        {"prefix lookalike", "/cardsx"},
        {"missing slash", "cards"},
        {"network path", "//evil.example/cards"},
        {"absolute scheme", "https://evil.example/cards"},
        {"userinfo", "http://user@evil.example/cards"},
        {"backslash", "/cards\\evil"},
        {"fragment", "/cards#fragment"},
        {"control", "/cards?x=\x01"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            _, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
            resp, err := noRedirectClient(ts).Get(
                ticketURLWithNext(t, ts, tc.next))
            if err != nil {
                t.Fatalf("兑换 ticket: %v", err)
            }
            defer resp.Body.Close()
            if resp.StatusCode != http.StatusFound {
                t.Fatalf("status = %d, want 302", resp.StatusCode)
            }
            if loc := resp.Header.Get("Location"); loc != "/" {
                t.Fatalf("非法 next Location = %q", loc)
            }
        })
    }
}

func TestTicketNextDoesNotLogTargetOrTicket(t *testing.T) {
    _, ts, logs := newHostTestEnv(t, &config.Config{Token: hostTestToken})
    gotURL := ticketURLWithNext(t, ts, "/cards?ticket=next-secret")
    resp, err := noRedirectClient(ts).Get(gotURL)
    if err != nil {
        t.Fatalf("兑换 ticket: %v", err)
    }
    resp.Body.Close()
    if strings.Contains(logs.String(), "next-secret") ||
        strings.Contains(logs.String(), gotURL) ||
        strings.Contains(logs.String(), "ticket=") {
        t.Fatalf("日志泄露 next/ticket/完整 URL：%q", logs.String())
    }
}
~~~

运行：

~~~text
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/agentd -run '^(TestTicketNextRedirectsToCardsPathAndQuery|TestTicketInvalidNextFallsBackToRoot|TestTicketNextDoesNotLogTargetOrTicket|TestTicketToCookieHappyPath)$' -count=1
~~~

现状应在合法 next 用例上因 Location 仍为 / 而红；记录实际原文。旧 TestTicketToCookieHappyPath 必须继续锁缺省 next 的 /。

### 3.3 Step 2：实现白名单与 redirect

在 authroutes.go 的 handleConsole 前加入：

~~~go
func validConsoleNext(next string) bool {
    if next == "" {
        return false
    }
    if strings.IndexFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
        return false
    }
    if strings.Contains(next, "\\") || strings.HasPrefix(next, "//") {
        return false
    }
    parsed, err := url.Parse(next)
    if err != nil || parsed.IsAbs() || parsed.Scheme != "" ||
        parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
        return false
    }
    if strings.Contains(parsed.Path, "\\") ||
        strings.IndexFunc(parsed.Path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
        return false
    }
    for _, values := range parsed.Query() {
        for _, value := range values {
            if strings.Contains(value, "\\") ||
                strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
                return false
            }
        }
    }
    return parsed.Path == "/cards" || strings.HasPrefix(parsed.Path, "/cards/")
}
~~~

在既有 Set-Cookie 后、http.Redirect 前替换固定根跳转：

~~~go
http.SetCookie(w, sessionCookie(r, token, int(time.Until(sess.ExpiresAt).Seconds())))

redirect := "/"
next := r.URL.Query().Get("next")
if next != "" {
    if validConsoleNext(next) {
        redirect = next
    } else {
        s.log.Warn("ticket 兑换 next 非法，回落根路径",
            "result", "invalid_next",
            "next_present", true,
        )
    }
}
http.Redirect(w, r, redirect, http.StatusFound)
~~~

缺省 next 不打 warning；非法 next 只打固定 result，不打 next/ticket/Location；既有成功日志继续只打 session/device/expires_at。更新文件头和 handleConsole 注释，写清“ticket 兑换后合法 next 或 /”。

### 3.4 Step 3：绿测、格式与日志门

~~~text
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/agentd -run '^(TestTicketNextRedirectsToCardsPathAndQuery|TestTicketInvalidNextFallsBackToRoot|TestTicketNextDoesNotLogTargetOrTicket|TestTicketToCookieHappyPath|TestTicketSingleUseOverHTTP|TestExpiredTicketRejected|TestConsoleRouteRegistered|TestDeepLinkRouteFallsBack)$' -count=1
gofmt -d internal/agentd/authroutes.go internal/agentd/auth_test.go
rg -n 'r\.URL\.String\(\)|ticket.*URL|next.*value|http\.Redirect' internal/agentd/authroutes.go
~~~

测试真实退出 0；合法 Location 精确为 /cards?project=handoff；所有非法表项精确回 /；ticket/cookie/过期 401 旧行为不变；gofmt 无输出。rg 命中 http.Redirect 是预期，必须确认它用的是已验证 redirect 变量；不得有完整 URL/ticket 日志。

## 4. 跨 task 接口、序列化边界与自审

### 4.1 逐字符接口核对

~~~text
ConsoleURLWithNext(ctx context.Context, ep Endpoint, deviceName, next string) (string, error)

HandleExternalBrowserMessage(
    log *slog.Logger,
    message, sourceFrameURL string,
    issue func(next string) (string, error),
    open func(string) error,
) bool

issueExternalBrowserURL := func(next string) (string, error)
app.Browser.OpenURL has type func(string) error
validConsoleNext(next string) bool
~~~

wire 必须是：

~~~text
existing CardsPage raw
  -> HandleExternalBrowserMessage
  -> next=/cards?project=handoff
  -> ConsoleURLWithNext -> IssueAuthTicket
  -> /console?ticket=固定测试票值&next=%2Fcards%3Fproject%3Dhandoff
  -> GET /console
  -> Location=/cards?project=handoff
~~~

### 4.2 序列化边界

1. 前端已有 origin/pathname/search 到 raw 字符串：本卡不改 producer，既有 20 项前端回归保留。
2. raw target 到 relative next：nextFromTarget 只拼 EscapedPath + RawQuery；handler 测试断言完整 /cards query。
3. ticket response URL 到 console query：ConsoleURLWithNext 用 URL.Query.Set；handshake 测试读回 ticket/next，区分缺失与空值。
4. OriginInfo.Origin/TopOrigin 到 sourceFrameURL：main 优先 Origin、空值回退 TopOrigin，不写完整来源。
5. /console query 到 redirect Location：agentd HTTP 测试经真实路由断言合法/非法结果和日志哨兵。

### 4.3 缺陷族对抗审查

| 缺陷族 | 结论与锁点 |
|---|---|
| 生命周期/状态机中断 | handler 识别消息后消费；签票失败不 open、不裸回退；每次点击重新签票；agentd 保留一次性/过期 401；桌面不导航。 |
| 静默失败/误导报错 | resolve/config、拒绝、签票、opener 缺失/失败、成功均有结构化日志；不宣称签票/打开成功。 |
| 跨平台假设 | 继续使用 Wails Origin/TopOrigin、App.Browser.OpenURL、scheme/hostname/effectivePort；Wails E2E 明确排除，不能拿 macOS 结果补它。 |
| 假红/假绿 | handler 入口测试 issue/open 顺序；handshake 穿真实 httptest；agentd 穿真实 GET /console；不以私有 validator 测试替代接缝。 |
| 门禁绕过 | raw 与 agentd 两次 /cards 相对路径白名单；无 open redirect；签票失败无 fallback；无新 HTTP endpoint。 |
| 序列化边界 | raw、URL query、HTTP Query/Location 各有 exact 断言；控制字符经编码再解码测试；日志不带敏感值。 |
| 新枚举值白名单 | 不新增 proto/store/state/kind 枚举，本族不适用。 |
| webview/平台边界 | frame URL 带 /console?ticket 时只比较 origin；不改 CardsPage、Shell、DesktopTitleBar；Wails E2E/真机穿透排除。 |

### 4.4 上下文预算、类型与接缝双向覆盖

Task 1 固定 5 个源码/测试文件，Task 2 固定 2 个；main.go 只改 raw handler 装配块。发现清单外文件必须先追加台账并停在协调者审阅，不默默扩散。

边界类型固定为：

- Wails callback：func(application.Window, string, *application.OriginInfo)；
- shell：string、func(string) (string,error)、func(string) error、bool；
- ConsoleURLWithNext：context.Context、Endpoint、string、(string,error)；
- agentd validator：string、bool。

接缝矩阵：

| 接缝 | 测试入口 | 锁点 |
|---|---|---|
| raw 同源 → next → issue → open | TestHandleExternalBrowserMessage → HandleExternalBrowserMessage | 同源成功、未知协议、http(s)/host/port/scheme/userinfo/source/path/lookalike/network/backslash/fragment/control 拒绝，签票失败不 open，opener 失败消费 |
| ticket URL query | TestConsoleURLWithNextPreservesTicketAndEscapesNext → ConsoleURLWithNext | 真实 IssueAuthTicket HTTP 请求、ticket 保留、next decode roundtrip、未记录凭据 |
| /console 合法 next | TestTicketNextRedirectsToCardsPathAndQuery → GET /console | 302 Location 精确 path+query、cookie 存在 |
| /console 缺省/非法 next | 既有 TestTicketToCookieHappyPath + TestTicketInvalidNextFallsBackToRoot | 全部 302 /，无外部 Location |
| 日志边界 | 两个入口的日志哨兵测试 | 不出现 source/target/issued ticket/next 值/回调 error 原文 |

TestConsoleURLWithNext 是唯一附加内部锁：手写 query 投影无法在不列入本卡的 Wails E2E 中到达；它不能替代 raw handler 和真实 /console 两条声明缝。其他测试均从声明缝入口进入。

## 5. 收口与提交

只跑触及包和已有前端回归，不跑全仓测试：

~~~text
cd desktop
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/shell/... -run '^(TestHandleExternalBrowserMessage|TestConsoleURLWithNext|TestConsoleURL|TestDefaultDeviceName)$' -count=1

cd ../
GOMODCACHE=/root/.handoff/tmp/6597ae3a/go-mod-cache GOCACHE=/root/.handoff/tmp/6597ae3a/go-cache go test ./internal/agentd -run '^(TestTicketNextRedirectsToCardsPathAndQuery|TestTicketInvalidNextFallsBackToRoot|TestTicketNextDoesNotLogTargetOrTicket|TestTicketToCookieHappyPath|TestTicketSingleUseOverHTTP|TestExpiredTicketRejected|TestConsoleRouteRegistered|TestDeepLinkRouteFallsBack)$' -count=1

cd web
npx vitest run src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx
npm run typecheck
npx eslint src/app/cards/CardsPage.tsx src/app/cards/CardsPage.test.tsx src/app/lib/desktopShell.ts src/app/lib/desktopShell.test.ts src/app/shell/DesktopTitleBar.test.tsx

cd ..
gofmt -d desktop/internal/shell/external_browser.go desktop/internal/shell/external_browser_test.go desktop/internal/shell/handshake.go desktop/internal/shell/handshake_test.go internal/agentd/authroutes.go internal/agentd/auth_test.go
git diff --check
~~~

具备工具链时再跑：

~~~text
cd desktop
wails3 task build
~~~

构建不可用时写原始错误和“未验证”；frontend/dist、desktop/bin 不 staged。收口检查只允许 7 个源码文件、计划、台账变化；禁止 Wails E2E/真机 postMessage 测试项、前端修改、agentd 新 endpoint、裸 URL fallback、完整 URL/ticket 日志。

本计划的测试复用例外仅为既有 httptest、newHostTestEnv、issueTicket、noRedirectClient、slog/opener spy；每条断言已在测试代码块和接缝矩阵逐条列全，不用骨架测试代替。

实现者收尾命令：

~~~text
git status --short
git diff --check
git add desktop/internal/shell/external_browser.go desktop/internal/shell/external_browser_test.go desktop/internal/shell/handshake.go desktop/internal/shell/handshake_test.go desktop/main.go internal/agentd/authroutes.go internal/agentd/auth_test.go
git commit -m "fix(desktop): open cards in browser with auth ticket"
~~~

本节点只提交计划和台账，不实现上述源码；实现代码由后续 implement 节点从 1a40bd82 增量完成。
