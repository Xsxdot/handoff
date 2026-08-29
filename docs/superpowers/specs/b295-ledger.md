# B295 spec 台账

- 2026-08-29 开卡：`handoff card add` → B295，priority 高，`card move spec`，基线 `acc/b156.2-156.3`。发现自 B293，不 reopen。
- 2026-08-29 用户原话：不要用之前的检测登录逻辑，通过发一个简单的消息来检测。
- 2026-08-29 现状：`WakeHome`/`runWake` 固定 `args := []string{"--version"}`（`internal/hostapi/probe.go`）；B293 契约条 36 禁 `RunTurn`。
- 2026-08-29 用户纠正：不是重新实现发消息。协调者拉起已走 `coordinatorRunner` → `RunTurn`。
- 2026-08-29 用户纠正：载体不分协调者/执行者。角色在小队上；检测挂载体。
- 2026-08-29 读 B156.3 spec §3/§4.1：小队有角色、不混编；载体一种实体、可入多队；进程承载与角色无关，禁止另写一套。
- 2026-08-29 spec 落盘 `docs/superpowers/specs/2026-08-29-b295-carrier-detect-via-turn-design.md`。定级 L3 轻档。待用户批准。
- 2026-08-29 用户：30s 不一定回得来 → 检测超时改 **3 分钟**（废止 B293 条 34 的 30s）。问「凭据供给之后」是否等于凭据来自主 HOME。答：是，且仅 `main_home_sync` + 隔离目录为空才拷；`standalone` 不拷。spec 方案第 2 条已改写，去掉「时限到了之后调 RunTurn」的歧义。
- 2026-08-29 用户批准 spec，并问是否必须 L3。重判 **L2**：改动在 `Host.WakeHome` 内部改调同包 `RunTurn`；gateway detect 形状不变；无新 HTTP/DTO/依赖方向。先前 L3 是把「改 B293 冻结条文」误判成跨子系统契约。
- 2026-08-29 基线 `go test ./internal/hostapi/ -count=1` → `ok github.com/Xsxdot/handoff/internal/hostapi 1.769s`。plan 落盘 `docs/superpowers/plans/b295-plan.md`。
- 2026-08-29 implement 本地（未派发）。先改测试后实现：`TestWakeHome` 首红含 `--version` 仍在 argv、超时返回 error、grok 被标 ready。改 `runWake` 调 `RunTurn` + `classifyTurnError` 后 `go test ./internal/hostapi/ -count=1` → `ok 2.290s`。`DefaultDetectTimeout=3m`，`DetectPrompt=ping`。B293 契约条 34/36 文案已改。真机未验。
- 2026-08-29 变异：`Prompt: DetectPrompt` 唯一命中 1，改为 `Prompt: "--version"`，`go build ./internal/hostapi/` 退出 0；`TestWakeHomeSuppliesMainCredentialBeforeTurn` 红，原文 `检测不得跑 --version: "run\n--format\njson\n--\n--version"`。还原后该测绿；`go build ./...` 退出 0。
