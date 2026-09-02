# B303 spec 台账

- 2026-09-02：开卡。来源 B300 真机：Grok 默认不发 OSC 52，`GROK_OSC52_SINK=1` 后复制成功。卡 note 原方向：桌面创建终端时沿本机 agentd→目标 agentd→PTY 注入；不改 `~/.zshrc`；不影响浏览器/CLI 普通终端与生产 agentd。
- 2026-09-02：现状。`CreatePtySessionReq` 只有 `env_file` / `init_command`，无通用 env。`handleCreatePtySession` 先 `forwardIfRequested`；`forwardJSON` 只搬 Authorization / Content-Type / `X-Handoff-Forwarded`，**不搬 User-Agent**。目标机 `pty.Open` Env = `sessionEnv()` + launcher env。桌面判定 `isDesktopShell`（UA `handoff-desktop`）只在前端。Windows PTY = `ErrNotSupported`。
- 2026-09-02：岔口 1。init_command 打 export（L2、旧 agentd 可用、屏幕可见）vs fork 时写进程环境（需目标机升级）。用户先选 init_command。
- 2026-09-02：岔口 2。用户否决「打到屏幕上」，改口：创建终端时作为默认参数写死。
- 2026-09-02：岔口 3。写死范围：全部 agentd PTY vs 只限桌面。用户选全部（浏览器工作台里的 Grok 一并生效）。定级 L1：不改 wire、不改前端，Env 末尾钉一颗变量。
- 2026-09-02：「不影响生产 agentd」落实为：开发期不动 7777，用隔离实例验收；合入后目标机升级才生效。系统 Terminal.app 不经本 API，不被改。
- 2026-09-02：用户批准 spec（原话「自主推进吧，不用再问我了」）。头部回写已批准。L1 快道进入 implement。
