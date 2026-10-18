# B438 传输层打洞 spec 台账

- 2026-10-19 起点：worktree `objective-meitner-b695a6`，分支 `claude/objective-meitner-b695a6`，HEAD `c6619013`，工作树干净。`handoff card move B438 spec` → status spec。
- 2026-10-19 读码：relay 形态唯一传输入口 `internal/relay/dialer.go`（`NewDialer`/`ensureTunnel`/`DialContext`/`RawDialContext`），消费者 `internal/targetclient/targetclient.go:45`、`internal/mobilecore/core.go:65`；executor 侧 `internal/relay/listener.go`（`serveSession` 在 E2E 后按 `previewRawMagic` 分流），组装点 `cmd/agentd.go:268-273`。配置 `internal/config/config.go:197-204` RelayConfig、`:324` Target.Relay。
- 2026-10-19 读码（handoff-server，只读）：`internal/relay/server.go:243-336` handleConnect 每条 CONNECT 独立鉴权、`tunnel.OpenSession()` 开新流、Splice 逐字节计量；同一 node 多条并发 CONNECT 已被 `RawDialContext` 使用 → 信令可走普通 CONNECT，服务端零改动。
- 2026-10-19 依赖可得性：`go list -m -versions` 得 pion/ice/v4 至 v4.4.7、pion/sctp 至 v1.12.0、pion/transport/v3 至 v3.1.1、pion/stun/v3 至 v3.1.7。
- 2026-10-19 判断：候选不入库（运行时经 relay 交换）；relay 先通后台升级（甲）；可靠层 pion/sctp；真机 NAT 用 linux-01 netns 而非 KVM（mkvm 是 br0 桥接、不是 NAT，见 `~/.claude/infra/machines.md`；netns + MASQUERADE 可同时造端点无关与 `--random-fully` 对称两种）。
- 2026-10-19 协调会话批准 r1，并补两条并入正文：代理语义（UDP 不经 proxy，启动 INFO 提示，新增 S8）；缺省 STUN 列表放 config 缺省值、空列表只 host 候选零 STUN。
