# W5b-2 台账：图形化首次引导 + 内嵌二进制释出

范围：8 个 task。分支 handoff/w5b2-onboarding。
恢复现场以本 ledger + git log 为准。

## 进度

- 2026-08-17 Task 1（initflow 下沉迁移）完成，commit 149ad5eb。审查双 APPROVED。协调者基线校验：搬迁前 `go test ./cmd/...` PASS 用例名集合 184 行、0 FAIL；搬迁后 `./cmd/... ./internal/initflow/...` 186 行、0 FAIL；diff 仅多两个名字——`TestAskAllCanceled`（从 cmd 的 TestInitCanceledDoesNotWrite 拆出的直接钉 AskAll 段，移入 initflow）与 `TestInitflowHasNoUILayerDeps`（新增边界守卫），无任何用例丢失。实现者适配（审查裁决合理）：`maybeInstallService` 依赖的 `installService`（cmd/service.go:97，CLI 层）无法被 initflow 反向 import（会成环），改为 initflow 包级 `var InstallService func(w, cfgPath)` 缝，由 cmd/service.go 的 init() 注入，MaybeInstallService 内 nil 兜底不 panic——这反而把「CLI 注 installService、GUI 注自己的实现」落成显式契约。Step 6 手工 CLI 探测被协调者判定作废（管道 stdin 下走非交互分支，askAll 不被调用，验不到东西），以上述用例名集合等价性替代。Minor 记账 3 条：M19 TestInitCanceledDoesNotWrite 内仍保留直接钉 AskAll 段（与 initflow 的 TestAskAllCanceled 重复，复制而非拆分，冗余无害）；M20 InstallService==nil 兜底只打印用户提示未记 slog 日志，桌面壳漏接无从排查，建议补 slog.Warn；M21 HostGOOS/HostGeteuid 是计划清单外导出（合理必要，initflow.go:275 已注释理由）。

- 2026-08-17 Task 2（事件驱动 Prompter）完成，commit dc9c1ac7。审查双 APPROVED。实现者适配：newTestConfig 返回 `&config.Config{}` 需补 import config；AskAll 协调者分支实际问 3 个问题（角色 Select + sync.auto Confirm + targets Input 空答结束），answers 为 `["coordinator","true",""]`（plan 原稿只有 2 个，plan 明示按实际数补齐、不许改 AskAll）。Minor 记账 2 条：M22 无用例喂非法 Select 答案，变异测试不生效（把非法答案静默取 def 现有 6 条用例全查不出来，可后续补非法值用例）；M23 `cd desktop && go test ./...` 根包因 `go:embed all:frontend/dist` 前端未构建 setup failed——父提交 149ad5eb 上完全相同，既有环境问题非本 task 引入（对应 W5b-1 记账 M5）。

## Minor 总账

（终审统一 triage）

## 真机走查

（Task 7 完成后逐条记录）
