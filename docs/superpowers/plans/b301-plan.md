# B301 实现计划

卡：B301。基线 `cards/B294-implement-6` @ `ec8a9181`。TDD。禁止 CONNECT。只动预览 raw dial 与 Darwin 聚焦。

## 接缝覆盖闸

| 缝 | 测试 |
|---|---|
| `Pool.DialContext` direct | `TestPoolDialContextDirectDialsOwnerLoopback`：owner HTTP 在 `::1` 或 `127.0.0.2`（target host ≠ content host；Darwin 常绑不上 `127.0.0.2`），content 在 `127.0.0.1:contentPort`。`DialContext(...,"localhost:"+contentPort)` 必须拿到 content echo。改写 `JoinHostPort(targetHost, contentPort)` 必须红。 |
| 同上负例 | `TestPoolDialContextDirectNonLoopbackIsSentToOwner`：非 loopback dest 不得在协调者本机直拨；应送到 owner raw（owner mock 记录收到的 dest）。 |
| owner WS | `TestPreviewRawWSRequiresAuth` 无 Bearer → 非 101；`TestPreviewRawWSDialsLoopback` Bearer + localhost dest → 桥接到本机 listener。 |
| Dial 超时 ctx | `TestPreviewRawWSSurvivesDialContextCancel`：Dial 返回后立刻 cancel，pipe 仍能 echo。relay `RawDialContext` 的 NetConn 同样不得绑 dial ctx。 |
| Darwin Focus | `TestPreviewDarwinFocusDoesNotUseXdotool`（`//go:build darwin`）：`LookPath` 为 osascript，错误/命令不含 xdotool。 |

## 任务

### T1 Pool direct → owner raw WS

1. 红：上面 DialContext 测试。
2. `internal/relay`：导出 `WritePreviewRawRequest` / `ReadPreviewRawRequest` / `WritePreviewRawResponse` / `ReadPreviewRawResponse`（薄封装即可，勿复制编解码）。
3. `Client.DialPreviewRaw(ctx, network, addr) (net.Conn, error)`：对 `baseURL` 拨 `GET /ws/preview-raw`，`wsDialOptions()`（自有 HTTPClient，禁环境代理），二进制 NetConn，写 raw 请求、读 raw 响应，成功则把该 conn 交给调用方。
4. `directDialer` 改为使用该 client：loopback/`localhost` dest 归一成 `127.0.0.1:port` 再交给 owner；**禁止** `JoinHostPort(targetHost, port)`。
5. owner `handlePreviewRawWS` 挂 `GET /ws/preview-raw`（与 `/ws/previews` 同 mux，走 auth）。Accept 后 `relay.ReadPreviewRawRequest`，仅 `tcp/tcp4/tcp6`，`localhost`/`::1` → `127.0.0.1`，`net.Dialer` 拨、写 OK、`bridge`。无 previewOwner 也要能拨（这是 transport 能力，不依赖会话表）。
6. 日志：拨号开始/成功/失败带 target、addr、cause。

### T2 Darwin Focus

1. 从 `preview_launcher_unix.go` 去掉 `Focus` 里的 xdotool。
2. `preview_launcher_focus_unix.go`：`//go:build unix && !darwin`，原 xdotool 逻辑。
3. `preview_launcher_focus_darwin.go`：`osascript` + System Events `unix id is <pid>` 置 frontmost。pid 不在托管集合仍报错。
4. 测试见上。

## 验收

- 触及包测试绿：`./internal/targetclient` `./internal/client` `./internal/agentd` `./internal/relay`；`go build ./...`。
- 变异：`JoinHostPort(targetHost, port)` 若被加回，T1 红；Darwin Focus 改回 xdotool，T2 红。
- 真机：隔离实例（本机 7790 + linux-01 7791，**不碰 7777**）重跑 linux `--path prototypes/base` SOCKS 取到静态页标题。

## Out of Scope

不改 DTO、不加 CONNECT、不升级现役 linux-01 agentd、不修 Windows Focus。
