# B293 spec 台账

- 2026-08-29 开卡：`handoff card move B293 spec`，基线 `acc/b156.2-156.3`。
- 2026-08-29 现状读数：`PutCarrier` 在 `internal/scheduling/scheduling.go` 把 `Healthy=false` 强行改 `true`；`admitInto` 已跳过 `!Healthy`。协调者拉起在 `internal/agentd/scheddrain.go` 把 `carrier.HomeDir` 写入 `SessionSpec`；执行者任务路径未消费该字段（codex 丢 `CODEX_HOME` 复用 `~/.codex`，grok 任务级 `grokhome` 软链 `~/.grok/auth.json`）。设置页 HOME 手填，原型占位 `~/.handoff/homes/…`。codegraph CLI 不在 PATH，领域读 `codegraph/best.json` 的 `d_scheduling` / `d_execution` / `d_web` / `d_keystone`。
- 2026-08-29 裁决：改 HOME 路径后探测为「目录非空且无该 CLI 凭据」→ 不覆盖、不自动创建；提示「目录非空且未见凭据」；允许保存但标不健康；运行按钮仍复制命令。用户选方案 1。
- 2026-08-29 裁决：用户同意可重复「检测」按钮。健康位不够：创建后为「未上线」；创建后自动检测一次，成功→「已上线」，失败保持未上线等用户点检测。上线后状态仍会变（限额、网络失败等）。「限额中」必须是单独状态（已知可恢复）；自动限额查询（限额中↔已上线）归后续卡，本期把状态位留出来。`Healthy bool` 不再并列为第二真相，改为一等状态字段。派发只放行「已上线」。
- 2026-08-29 裁决：已上线后网络失败等走单独「不可达」，不退回未上线、不继续冒充已上线。用户选方案 1。闭环状态：未上线 / 已上线 / 限额中 / 不可达。未上线=还没登录或还没建好；不可达=曾经上线、现在探不到。凭据失效仍回未上线（动作是复制命令登录）。探测写入点：创建（未上线）+ 检测（含创建后自动一次）。派发只读。限额自动查询后续卡。
- 2026-08-29 形态：fork `prototypes/b293-carrier-home/`（gitignore 临时工作区），改设置页自动化分区。
- 2026-08-29 用户确认原型形态「可以」，授权落 spec。
- 2026-08-29 spec 落盘 `docs/superpowers/specs/2026-08-29-b293-isolated-home-carrier-status-design.md`。定级 L3 轻档。批准：状态机与形态已在对话确认。

