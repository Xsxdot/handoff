# B438 plan 台账

> 节点 charter:plan（L3 出稿者 = 本地 subagent；拍板者 = 协调会话 local_30aaf694）。
> 只记亲跑命令与原始输出摘要。spike 源码在会话 scratch（`…/scratchpad/p2pspike/`，非入库，会话结束即失），关键输出已摘录于此。

## 起点

- 2026-10-19 worktree `objective-meitner-b695a6`，分支 `claude/objective-meitner-b695a6`，HEAD `f5e1d443`（spec r1 已批准提交），工作树干净。
- 输入：spec `docs/superpowers/specs/b438.md`（r1 已批准）+ 台账；charter plan / defect-families / architecture-law 三份 skill 正文全读。项目无独立缺陷族清单（grep `缺陷族` 只命中历史 plan），按基线五族 + 追加设问。

## 基线亲跑

```
$ go build ./... && echo BUILD_OK
BUILD_OK
$ go test ./internal/relay/ ./internal/targetclient/ ./internal/config/ ./internal/mobilecore/
ok  internal/relay 0.915s / ok internal/targetclient 6.048s / ok internal/config 0.878s / ok internal/mobilecore 0.889s
$ (cd mobile && go test ./...)
ok  github.com/Xsxdot/handoff/mobile 0.818s / ok github.com/Xsxdot/handoff/mobile/bind 10.356s
$ (cd mobile && go test ./bind/ -run 'TestGomobileSurfaceHasNoSkips|TestBindExportedSurfaceIsFrozen' -v)
--- PASS: TestBindExportedSurfaceIsFrozen
--- SKIP: TestGomobileSurfaceHasNoSkips   gobind_surface_test.go:38: 未找到 gobind
```
- 本机（darwin）无 `~/go/bin/gobind`、无 `~/Library/Android/sdk`、`xcode-select -p` = CommandLineTools（无完整 Xcode）→ B438.2 的 gomobile 双平台产物本机跑不了，需先定位工具链机器。

## 依赖事实（scratch 临时 module，未动仓库 go.mod）

```
$ go mod init spike; go get github.com/pion/ice/v4@v4.4.7 github.com/pion/sctp@v1.12.0 github.com/pion/transport/v3@latest github.com/pion/stun/v3@latest
go: added github.com/pion/ice/v4 v4.4.7 / pion/sctp v1.12.0 / pion/transport/v3 v3.1.1 / pion/transport/v5 v5.1.1
      pion/stun/v3 v3.1.7 / pion/stun/v4 v4.0.1 / pion/turn/v5 v5.1.2 / pion/dtls/v3 v3.1.9 / pion/mdns/v2 v2.2.2
      pion/logging v0.2.4 / pion/randutil v0.1.0 / wlynxg/anet v0.0.5 / golang.org/x/net v0.49.0 / x/time v0.14.0 ...
```
- **ice/v4@v4.4.7 用的是 transport/v5 + stun/v4，不是 transport/v3**：`ice/v4@v4.4.7/go.mod` require `pion/stun/v4 v4.0.1`、`pion/transport/v5 v5.0.1`、`pion/turn/v5 v5.1.2`；`agent_options.go:15-16` import `stun/v4`、`transport/v5`。sctp@v1.12.0 go.mod require `transport/v5 v5.1.1`。→ vnet 必须取 `github.com/pion/transport/v5/vnet`；v3 与 ice v4.4.7 的 `transport.Net` 类型不兼容。（与派单「transport/v3」不符，已按实测写 plan。）
- ice 构造：`agent.go:201 NewAgentWithOptions(opts ...AgentOption)`；`AgentConfig` 已 Deprecated（`agent_config.go:79`）。注入网络 `agent_options.go:523 WithNet(transport.Net)`；未注入时 `agent.go:472 stdnet.NewNet()`。STUN `:357 WithUrls([]*stun.URI)`（解析 `stun/v4 uri.go:140 ParseURI`）；候选类型 `:945 WithCandidateTypes`；网络类型 `:874 WithNetworkTypes`；mDNS `:536 WithMulticastDNSMode`（缺省 `agent.go:441` = QueryOnly）；日志 `:1024 WithLoggerFactory`；STUN 收集超时 `:483 WithSTUNGatherTimeout`。
- 超时缺省（`agent_config.go:17-49`）：CheckInterval 200ms、KeepaliveInterval 2s、DisconnectedTimeout 5s、FailedTimeout 25s、STUNGatherTimeout 5s、srflx 提名最短等待 500ms、MaxBindingRequests 7。初始 checking 截止 = disconnected+failed（`agent.go:741-753 initialCheckingTimeout`），到点置 Failed（`agent.go:686-688`）。断链判定 `agent.go:906-941`：选中对 LastReceived 超 disconnectedTimeout → Disconnected，超 disconnected+failed → Failed。
- **`AwaitConnect` 不因 Failed 返回**：`transport.go:31-41` 只等 `loop.Done()` / `ctx.Done()`（返回 `ErrCanceledByCaller`）/ `onConnected`。→ 放弃时限必须由调用方 ctx + 监听 `OnConnectionStateChange(Failed)` 共同保证（spike S2 实测佐证）。
- Dial/Accept：`transport.go:55 Dial(ctx, remoteUfrag, remotePwd) (*Conn, error)`（controlling）、`:80 Accept(...)`（controlled）。`*ice.Conn` 满足 net.Conn（`transport.go:127-301`），`Conn.Close()` = `agent.Close()`（`:261`）。
- 收集完成判定：`agent_handlers.go:23-25` 「When the gathering process complete the last candidate is nil」；`gather.go:132 GatherCandidates` 无 OnCandidate 处理器时报 `ErrNoOnCandidateHandler`、重复调用报 `ErrMultipleGatherAttempted`。
- 关闭：`agent.go:1585 Close()` 不等 goroutine；`:1592 GracefulClose()`「waits for any goroutines it started」，回调内调用须另起 goroutine。→ 泄漏断言需 GracefulClose。
- 候选串：`candidate_base.go:655 Marshal()`；**实测串内含 `ufrag <本端ufrag>`**（见 spike 输出），故日志只能记 `Type()` 与地址族，不能记候选串。
- sctp：`association.go:409 Config{LoggerFactory, Name, NetConn, BlockWrite, MaxMessageSize, ...}`；`ClientWithOptions/ClientContext(ctx, ...)`（`:481/:525`，ctx 只管建立）、`ServerWithOptions`（`:454`）；选项 `association_options.go:35 WithLoggerFactory / :56 WithNetConn / :69 WithBlockWrite（缺省 false）/ :128 WithMaxMessageSize`。`OpenStream(id, ppi)`（`:2804`，不发包）/`AcceptStream()`（`:2820`，对端首个 DATA 到达才返回）。缺省 MaxMessageSize 65536（`:84`），超出 `stream.go:314-316` 报 `ErrOutboundPacketTooLarge`。
- **`*sctp.Stream` 不满足 net.Conn**（无 LocalAddr/RemoteAddr），且是**消息语义**：`stream.go:121-156` Read 读一整条消息，缓冲不够返回 `io.ErrShortBuffer`（`reassembly_queue.go:575/618`，消息不出队）。`Stream.Close()` 只关写方向（`stream.go:466`）。→ 必须包一层字节流适配（读侧 64KiB 消息缓冲、写侧按 MaxMessageSize 切片、Close 连带 association 与 ICE）。
- vnet（transport/v5）：`vnet/nat.go:35-40 EndpointIndependent / EndpointAddrDependent / EndpointAddrPortDependent`；`NATType{Mode, MappingBehavior, FilteringBehavior}`（`:61`）；`RouterConfig{CIDR, StaticIPs, NATType, LoggerFactory}`（`router.go:47`）；`Router.AddChunkFilter`（`:397`）可计数/丢包。造法参考 `ice/v4@v4.4.7/connectivity_vnet_test.go:48-180`（wan 根路由 + 两 LAN 路由带 NATType + wanNet 上 `turn.NewServer` 当 STUN）。
- STUN 服务：`turn/v5@v5.1.2/examples/stun-only-server/main.go` 只需 `turn.NewServer(ServerConfig{PacketConnConfigs:[{PacketConn}]})`，无 AuthHandler 即纯 STUN；vnet 内把 PacketConn 换成 `wanNet.ListenPacket`。turn/v5 已是 ice/v4 的依赖，不新增 module。
- Android 接口枚举：`transport/v5@v5.1.1/stdnet/net.go:14,43,51` 用 `github.com/wlynxg/anet` 的 `anet.Interfaces()` / `InterfaceAddrsByInterface`（golang/go#40569 的社区绕法）→ pion 侧已处理 Android 11+ netlink 受限；B438.2 不需自写枚举，但仍需模拟器取证。
- yamux 旧 listener 判别：`hashicorp/yamux@v0.1.2/session.go:561-563` 头版本 ≠0 → `ErrInvalidVersion` 退出 recvLoop（不发 GoAway），会话关闭。新魔数首字节 `'h'`(0x68) 必触发；请求总长 >12 字节，不会卡在读头。
- 仓库现有依赖：根 `go.mod` 无 pion、无 golang.org/x/net；x/crypto v0.55、x/sys v0.47（均高于 pion 所需 v0.48/v0.41，MVS 取高，无降级）。

## spike 亲跑（vnet 两 NAT + vnet STUN；ice v4.4.7 + sctp v1.12.0）

配置：`WithNet(vnet.Net)`、`WithUrls([stun:1.2.3.4:3478])`、`WithNetworkTypes([UDP4])`、`WithCandidateTypes([host,srflx])`、`WithMulticastDNSMode(Disabled)`、`WithDisconnectedTimeout(5s)`、`WithFailedTimeout(5s)`、`WithSTUNGatherTimeout(3s)`；连通 ctx 12s；WAN 路由 ChunkFilter 计 STUN 包、可一键丢弃全部包（断链）。

```
=== S1a full-cone both + STUN + sctp echo ===
gather a=0s cands=[... 192.168.0.1 5510 typ host ufrag TbzojGpUnIpNAkyW ... 27.1.1.1 49152 typ srflx raddr 0.0.0.0 rport 5194 ufrag TbzojGpUnIpNAkyW ...]
connect err=<nil> elapsed=1s selected(a)=local=host remote=prflx
sctp small-buffer read n=60000 err=short buffer
sctp big-buffer read n=60000 err=<nil> equal=true
sctp echo n=60000 err=<nil> equal=true
sctp write 65537 err=outbound packet larger than maximum message size: 65536
*sctp.Stream implements net.Conn: false
*ice.Conn implements net.Conn: true
sctp throughput (vnet, blockwrite) ~247.4 MB/s
stun packets through WAN: 2
goroutines before=1 after-close=1
=== S1b port-restricted(EI map, APD filter) both + STUN + sctp ===
  [b] state=Connected at +600ms / [a] state=Connected at +600ms
connect err=<nil> elapsed=600ms selected(a)=local=srflx remote=srflx
sctp echo n=60000 err=<nil> equal=true
goroutines before=1 after-close=1
=== S2 symmetric vs port-restricted + STUN (expect fail) ===
  [a] state=Failed at +10.1s / [b] state=Failed at +10.1s
connect err=connecting canceled by caller elapsed=12s selected(a)=none
goroutines before=1 after-close=1
=== S3 port-restricted both, connect then cut path ===
connect err=<nil> elapsed=600ms selected(a)=local=srflx remote=srflx
  path cut at +0
  [a] state=Disconnected at +6.2s / [b] state=Disconnected at +6.2s
  [b] state=Failed at +10.2s / [a] state=Failed at +10.2s
=== S8 empty STUN list across NAT (expect fail, zero STUN) ===
gather a=0s cands=[... 192.168.0.1 5544 typ host ufrag jtuOKrkqicfHvYLg generation 0]
  [a] state=Failed at +10.1s
connect err=connecting canceled by caller elapsed=12s
stun packets through WAN: 0
=== yamux over sctp byte-adapter over ICE (port-restricted NATs) ===
8 concurrent yamux streams x 1MiB echo: ok=8/8 elapsed=75ms
```

结论：
1. **两 NAT（端点无关映射，含 Linux MASQUERADE 等价的端口受限过滤）后 ICE 连通 + sctp 单流回显 + 其上 yamux 8 并发流：已验证（vnet）**。选中对 srflx↔srflx。
2. 对称 NAT 一端：ICE 在 disconnected+failed=10.1s 置 Failed，但 `Dial` 只在 ctx 到点（12s）才返回 → 必须监听 Failed 早退。
3. 断链：Disconnected 于 +6.2s、Failed 于 +10.2s（5s+5s 配置）→ S3「≤30s 回落」有余量。
4. 空 STUN 列表：只 host 候选、WAN 上 STUN 包 0。
5. `GracefulClose` 后 goroutine 回到基线（1→1）。
6. sctp 消息语义与 64KiB 上限实测成立，字节流适配层必需；`AcceptStream` 要等对端首个 DATA——E2E 发起方（SecureClient 先写 salt）恰好承担，角色须对齐：coordinator = ICE controlling = sctp client = OpenStream = E2E initiator。

未覆盖（标未验证）：真 Linux 内核 NAT 行为、E2E 叠加在适配层上（scratch 无法 import internal/relay；E2E 只依赖 net.Conn 字节流语义，适配层已证字节流语义）、真 STUN 公网服务器可达性、移动平台。

## 读码事实（handoff 仓，行号 @f5e1d443）

- `internal/relay/dialer.go:47 NewDialer(relayURL, credential, node, token, account string, log)`；`:267-342 ensureTunnel`（锁内建隧道，d.account 来自 CONNECT_OK）；`:348-368 DialContext` 先 ensureTunnel 再 `session.Open()`；`:62-133 RawDialContext` 每次独立 CONNECT+E2E+previewRawMagic；`:387-389 Transport()` 每次 new 一个 `http.Transport{DialContext, Proxy:nil}`（**keep-alive 缺省开、IdleConnTimeout 0=永不过期**）；`:392-425 Close` 终态。
- `internal/relay/listener.go:41 NewListener(relayURL, credential, node, token, account, handler, log)`；`:53-116 Run` 每条 relay session 起 goroutine 计入**本次 Run 的** `sessions` WaitGroup，退出前 `sessions.Wait()`；`:149-186 serveSession` E2E → `Peek(len(previewRawMagic))` → raw 或 app-yamux+`http.Serve`。
- `previewRawMagic = "handoff-preview-raw-v1"`（`dialer.go:39`，22 字节）。
- `internal/relay/e2e.go:35/41 SecureClient/SecureServer(ctx, raw, token, account, node)`；发起方先写 salt（`:81-88`）；`secureConn.Write` 按 ≤65519 明文切帧、每帧两次底层 Write（2 字节前缀 + ≤65535 密文，`:286-296`）。
- 假 relay `internal/relay/listener_test.go:147-202 startBridgeRelay`：`registered` 是容量 1 的 channel，**第一条 CONNECT 取走 executor session 后不放回 → 每次注册只支持一条 CONNECT**；无字节计数。竖切需新假 relay（多 CONNECT + 每 CONNECT 字节计数）。
- 构造调用方：`cmd/agentd.go:270 relay.NewListener`；`internal/targetclient/targetclient.go:45 relay.NewDialer`；`internal/mobilecore/core.go:65 relay.NewDialer`；测试 `internal/relay/{listener_test.go:32,34,88,90; dialer_test.go:23,35,67}`、`internal/client/{client_relay_test.go:11; execution_test.go:210,229,245}`。`targetclient.New` 调用方：`cmd/root.go:265`（CLI）、`cmd/upgrade.go:123`（升级 relay 一次性）、`internal/agentd/machines.go:226`（直连探测）；`NewPool`：`internal/agentd/server.go:347`（conf=活配置快照）。
- `internal/client/client.go:252-264 NewRelay` 直接用 `d.Transport()`。
- 配置：`internal/config/config.go:100 Proxy`（注释 83-99：作用范围**只有**更新链路 HTTP 与 git；「不作用于协调者↔agentd 链路」）；`internal/proxycfg/proxycfg.go` 包注释「协调者↔agentd 那条链路永不走代理」；`config.go:499-502` 启动 INFO `已配置出网代理`；`:504-...newDefaultConfig` 缺省字面量；`:534 applyComputedDefaults`；`:600 validate`；`:681-694 decodeStrict`（KnownFields + 已知键清单文案）；`EnvForward` 注释（三态：nil=内置默认、[]=显式空；「不要在 Load 里填默认值」否则 Save 落盘顶死旧 agentd）。
- relay WSS 拨号 `websocket.Dial(ctx, url, nil)`（dialer.go:75,282；listener.go:58）→ coder/websocket 缺省 http.Client → 只吃 `HTTPS_PROXY` 等环境变量，**不吃 config `proxy` 键**。与 spec「relay 的 WSS 照旧经代理」措辞不符（见 plan 稿首 P5）。
- 嵌套 module：`mobile/go.mod`、`desktop/go.mod` 均 `replace github.com/Xsxdot/handoff => ../`；`go list -deps ./internal/client` 含 `internal/relay` → 两个嵌套 module 都经 client 传递依赖 relay → 根仓引入 pion 后两处 go.mod/go.sum 必须 tidy（推断，implement 首步 `cd mobile && go build ./... ; cd desktop && go vet ./...` 证实）。mobile 现依赖图不含 `internal/config`。
- `mobile/bind/export_surface_test.go:20-34 wantBindSurface` 冻结 9 个导出函数。
- handoff-server（只读，HEAD `7fdb21e`）：`cmd/handoff-server/main.go:30-44` `-config` 读 yaml；**账本是 SQLite（`internal/ledger/store.go:14,226` modernc sqlite，配置 `db_path`），不是 PG** → 真机隔离 relay 无需建 PG 库，用临时目录 SQLite 文件、用完删目录。admin API：`Authorization: Bearer <admin_master_key>`（`adminapi/actor.go:76-87`）；路由 `adminapi/api.go:78-127`：`POST /admin/accounts`、`POST /admin/accounts/{id}/credentials {role,node}`、`POST /admin/accounts/{id}/slots {spec_id,valid_days}`、`POST /admin/slots/{id}/rebind {credential_id,node}`、`PUT /admin/slot-specs`（`docs/deploy/README.md:178-205` 有逐字跑通的 curl）、`GET /admin/slots/{id}/usage`；`toSlotResponse` 带 `used_bytes`（`slots.go:60`）。CONNECT 每条独立鉴权、无并发上限（grep `max|limit|concurrent` 仅命中注释）。计量刷盘周期 `meter_flush_interval`。
- linux-01 工具（只读探查）：`ip`/`iptables v1.8.10 (nf_tables)`/`nft`/`tcpdump`/`go1.26.1` 在；**`conntrack` 缺**；内核 7.0.0-38；`ip_forward=1`；无现存 netns。
- 2026-10-19 coder/websocket@v1.8.15 dial.go:75-76：DialOptions.HTTPClient 为 nil 时取 http.DefaultClient → relay WSS 只受环境变量代理影响，不受 config `proxy` 键影响（佐证 P5）。
- 2026-10-19 scratch module 内 `GOOS=linux GOARCH=amd64 go build -o /dev/null github.com/pion/turn/v5/examples/stun-only-server` → `STUN_BUILD_OK`（真机 STUN 二进制构建法已验证）。
- 2026-10-19 handoff-server `internal/billing/tier.go:42-62 Decide`：`UsedBytes < IncludedBytes` 即 TierNormal（全速），限速只在超额后 → 真机 slot 规格给 1 GiB 额度即不触发限速。
- 2026-10-19 `config.go:108` EnvForward 三态注释、`:116` 字段；`:159 PlatformInvariants *bool`（指针区分未配置/显式 false 的先例）。
- 2026-10-19 协调会话拍板 P1–P5 全部按建议，事实更正认可；状态行已回写。
