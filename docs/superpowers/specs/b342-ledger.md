# B342 台账

- 2026-09-05 用户：opencode 可能到期未续费；本机 grok、linux-01 agy/codex；测试谁当协调者/执行者无所谓；先前 OOS 第二条（TUI 自动 card bind）不做，其余自主推进。
- 2026-09-05 现网 `GET /api/squads`：grok@mac-02 pending，last_error 未实装；muse online；leader/runner 都是 muse。
- 2026-09-05 linux-01：agy 1.1.26 `/root/.local/bin/agy`；codex 0.149.1 `/usr/local/bin/codex`。本机 grok `/Users/sycm/.grok/bin/grok`。
- 2026-09-05 建卡 B342/B343/B344。B342 定级 L1。opencode 不可靠，本会话 grok 本地实现，不派 muse。
- 2026-09-05 `codegraph sym WakeHome` 摘要仍写「不是 RunTurn」——图覆盖债，源码 `runWake` 走 `detectTurn`→`RunTurn`。
- 2026-09-05 测试先红：三支 UnsupportedCLI 都因仍走 detectTurn 失败。改 runWake + lookPathWithFallback 后 `go test ./internal/hostapi/ -count=1` ok 2.176s。
- 2026-09-05 变异 `if false && !supportedCLIs[cli]`：`go build ./internal/hostapi/` 0；`TestWakeHomeUnsupportedCLIPathReadySkipsTurn` 红（仍走 detectTurn）。还原 if，未用 checkout。
