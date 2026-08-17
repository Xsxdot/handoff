# W5b-2 台账：图形化首次引导 + 内嵌二进制释出

范围：8 个 task。分支 handoff/w5b2-onboarding。
恢复现场以本 ledger + git log 为准。

## 进度

- 2026-08-17 Task 1（initflow 下沉迁移）完成，commit 149ad5eb。审查双 APPROVED。协调者基线校验：搬迁前 `go test ./cmd/...` PASS 用例名集合 184 行、0 FAIL；搬迁后 `./cmd/... ./internal/initflow/...` 186 行、0 FAIL；diff 仅多两个名字——`TestAskAllCanceled`（从 cmd 的 TestInitCanceledDoesNotWrite 拆出的直接钉 AskAll 段，移入 initflow）与 `TestInitflowHasNoUILayerDeps`（新增边界守卫），无任何用例丢失。实现者适配（审查裁决合理）：`maybeInstallService` 依赖的 `installService`（cmd/service.go:97，CLI 层）无法被 initflow 反向 import（会成环），改为 initflow 包级 `var InstallService func(w, cfgPath)` 缝，由 cmd/service.go 的 init() 注入，MaybeInstallService 内 nil 兜底不 panic——这反而把「CLI 注 installService、GUI 注自己的实现」落成显式契约。Step 6 手工 CLI 探测被协调者判定作废（管道 stdin 下走非交互分支，askAll 不被调用，验不到东西），以上述用例名集合等价性替代。Minor 记账 3 条：M19 TestInitCanceledDoesNotWrite 内仍保留直接钉 AskAll 段（与 initflow 的 TestAskAllCanceled 重复，复制而非拆分，冗余无害）；M20 InstallService==nil 兜底只打印用户提示未记 slog 日志，桌面壳漏接无从排查，建议补 slog.Warn；M21 HostGOOS/HostGeteuid 是计划清单外导出（合理必要，initflow.go:275 已注释理由）。

- 2026-08-17 Task 2（事件驱动 Prompter）完成，commit dc9c1ac7。审查双 APPROVED。实现者适配：newTestConfig 返回 `&config.Config{}` 需补 import config；AskAll 协调者分支实际问 3 个问题（角色 Select + sync.auto Confirm + targets Input 空答结束），answers 为 `["coordinator","true",""]`（plan 原稿只有 2 个，plan 明示按实际数补齐、不许改 AskAll）。Minor 记账 2 条：M22 无用例喂非法 Select 答案，变异测试不生效（把非法答案静默取 def 现有 6 条用例全查不出来，可后续补非法值用例）；M23 `cd desktop && go test ./...` 根包因 `go:embed all:frontend/dist` 前端未构建 setup failed——父提交 149ad5eb 上完全相同，既有环境问题非本 task 引入（对应 W5b-1 记账 M5）。

- 2026-08-17 Task 3（embedbin 双形态）完成，commit e3419b7d。审查双 APPROVED。带标签侧编译失败实测 `pattern handoff: no matching files found`（缺席即编译期失败的前提实证）；假产物 1000 字节被 1MB 门槛拦下、已删、工作区干净。审查裁决 embed.go 的 Open 对 embed.FS.Open 失败 panic 合理（go:embed 编译期保证，与 webui.FS 对不可达分支 panic 的先例一致，不改）。Minor 记账 3 条：M24 stub.go 导出了 plan 未要求的 ErrNotEmbedded 哨兵错误（良性增量，调用方可 errors.Is，可改回内联错误）；M25 embed_test.go 的 defer rc.Close() 未检查 Close 错误（测试场景可接受）；M26 .gitignore 的 `handoff` 模式同时匹配同名目录（无实害）。

- 2026-08-17 Task 4（三态释出逻辑）完成，commit 3636cdfb。审查双 APPROVED。实现者决策：版本比较另写 compareVersion/parseVersion（internal/selfupdate 的 cmpVersion 未导出，不值得为此改导出面；语义与 clicheck.go 逐行一致，无第三方依赖）；「existing 空 + embedVer 空」走 DecisionInstall（没有既有安装则承重不适用，embedbin 不可用由调用方查 Available() 兜底）；os.Lstat 检查悬空符号链接（Stat 会跟随悬空链返回 ENOENT 放行 rename 覆盖链接本身）；chmod 在 rename 前避免「已可见但无权限」窗口。调试中自己修了一个 compareVersion 方向写反的 bug（cmp>=0 → UseExisting、cmp<0 → NotifyOutdated）。Minor 记账 4 条：M27 existing 非空 + embedVer 空 → UseExisting 分支无直接测试覆盖（逻辑正确）；M28 TOCTOU：Lstat 检查与 Rename 之间并发新建 dst 会被 Unix rename 静默覆盖（桌面单机上下文可忽略）；M29 release.go 注释「本模块可 import 根模块 internal 包」表述稍绕（desktop 是独立 module 需 require/replace，起决定作用的是「cmpVersion 未导出」）；M30 已存在分支同时 logger.Error + 返回错误轻微重复（符合项目惯例）。

## Minor 总账

（终审统一 triage）

## 真机走查

（Task 7 完成后逐条记录）
