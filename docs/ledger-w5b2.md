# W5b-2 台账：图形化首次引导 + 内嵌二进制释出

范围：8 个 task。分支 handoff/w5b2-onboarding。
恢复现场以本 ledger + git log 为准。

## 进度

- 2026-08-17 Task 1（initflow 下沉迁移）实现完成，待提交。协调者基线校验：搬迁前 `go test ./cmd/...` PASS 用例名集合 184 行、0 FAIL；搬迁后 `./cmd/... ./internal/initflow/...` 186 行、0 FAIL；diff 仅多两个名字——`TestAskAllCanceled`（从 cmd 的 TestInitCanceledDoesNotWrite 拆出的直接钉 AskAll 段，移入 initflow）与 `TestInitflowHasNoUILayerDeps`（新增边界守卫），无任何用例丢失。实现者适配：`maybeInstallService` 依赖的 `installService`（cmd/service.go:97，CLI 层）无法被 initflow 反向 import（会成环），改为 initflow 包级 `var InstallService func(w, cfgPath)` 缝，由 cmd/service.go 的 init() 注入；MaybeInstallService 内对 nil 兜底不 panic。Step 6 手工 CLI 探测被协调者判定作废（管道 stdin 下走非交互分支，askAll 不被调用，验不到东西），以上述用例名集合等价性替代。

## Minor 总账

（终审统一 triage）

## 真机走查

（Task 7 完成后逐条记录）
