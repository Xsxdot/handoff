# B301 台账

- 2026-08-30 卡 B301 L2，基线 `cards/B294-implement-6` @ `ec8a9181`。用户「OK，修吧，你自主推进」。不派 linux-01 Codex，不碰现役 7777。
- 隔离实例：本机 `127.0.0.1:7790` pid 后换 `97761` 二进制 `b301-iso2`；linux-01 iso `100.79.27.99:7791` pid `1105787`。现役 linux `435229:7777`、本机 `25057:7777` 未动。
- 第一红：U5 SOCKS `dial tcp 100.79.27.99:<port> connection refused`。根因 `directDialer` `JoinHostPort(targetHost, contentPort)`。改为 `directOwnerRawDial` → owner `GET /ws/preview-raw`，dest loopback 归一 `127.0.0.1`。
- 第二红：macOS 再点 `需要 xdotool`。Focus 拆 `unix && !darwin` / `darwin`（osascript System Events unix id）。
- 真机第一发：owner 已拨 `127.0.0.1:43223`（不再改写 Tailscale），但 SOCKS HTTP 空回复。日志「拨号成功」后 6ms「会话结束」。
- 根因 2：`PreviewProxy.handle` `dialCtx, cancel := WithTimeout; Dial; cancel()`。`DialPreviewRaw` 把 `websocket.NetConn` 绑在该 ctx 上，管道随 cancel 关掉。`TestPreviewRawWSSurvivesDialContextCancel` 先红 `failed to acquire lock: context canceled`，NetConn 改 `context.Background()` 后绿。relay `RawDialContext` 同修。
- 真机复测（iso2）：
  - linux `--path` session `216a929c` entry `http://localhost:41251/prototypes/base/index.html`。OpenPreview `{"opened":true}`。SOCKS `--noproxy '' --socks5-hostname` `-L` → 301 后 200，title `handoff 控制台 · 工作台`，54917 bytes。owner dest `127.0.0.1:41251`。
  - 再点 OpenPreview `{"opened":true}` 日志 `preview Chromium 聚焦成功` / `已有 preview 浏览器聚焦成功` pid=97817，无 xdotool。
  - `--port 5175` 只绑 `127.0.0.1`（Tailscale `100.79.27.99:5175` connection refused）。session `33236b2c`。SOCKS 200 title `b301-loop-port`。owner dest `127.0.0.1:5175`。
  - 本机 `--path` session `27df75d5` SOCKS `-L` 同样 title `handoff 控制台 · 工作台`。
- 变异（均 BUILD_OK，非编译红；`git checkout` 会抹未提交实现，改用文件备份还原）：
  1. `DialPreviewRaw` NetConn `Background`→`ctx`：`TestPreviewRawWSSurvivesDialContextCancel` 红 `context canceled`；还原绿。
  2. 归一条件 `localhost`→`not-localhost`：`TestPoolDialContextDirectDialsOwnerLoopback` 红 `owner dest="tcp localhost:…", want "tcp 127.0.0.1:…"`；还原绿。
  3. Darwin `LookPath("osascript")`→`xdotool`：`TestPreviewDarwinFocusDoesNotUseXdotool` 红 `darwin focus used xdotool`；还原绿。
- 触及包：`go test ./internal/agentd -run TestPreviewRawWS|TestPreviewDarwinFocus` ok；`./internal/targetclient ./internal/client ./internal/relay` ok；`go build ./...` ok。
- review（只读，7dedb64d）：Critical 无。Important：① Direct Dial 测试 owner/content 同为 127.0.0.1 可假绿；② NetConn/dial-ctx 契约未回写冻结物。已修：owner httptest 绑 `::1`（Darwin 无 127.0.0.2）；spec 修订 1 + 接缝 4；`JoinHostPort("::1")` 变异红 dest 不匹配后还原绿。
- 不合 main。B294 不 reopen。
