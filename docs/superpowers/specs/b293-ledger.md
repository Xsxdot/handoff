# B293 spec / contract 台账

> 前半为 spec 节点台账；contract 节点从「contract 开工」段起追加。

- 2026-08-29 开卡：`handoff card move B293 spec`，基线 `acc/b156.2-156.3`。
- 2026-08-29 现状读数：`PutCarrier` 在 `internal/scheduling/scheduling.go` 把 `Healthy=false` 强行改 `true`；`admitInto` 已跳过 `!Healthy`。协调者拉起在 `internal/agentd/scheddrain.go` 把 `carrier.HomeDir` 写入 `SessionSpec`；执行者任务路径未消费该字段（codex 丢 `CODEX_HOME` 复用 `~/.codex`，grok 任务级 `grokhome` 软链 `~/.grok/auth.json`）。设置页 HOME 手填，原型占位 `~/.handoff/homes/…`。codegraph CLI 不在 PATH，领域读 `codegraph/best.json` 的 `d_scheduling` / `d_execution` / `d_web` / `d_keystone`。
- 2026-08-29 裁决：改 HOME 路径后探测为「目录非空且无该 CLI 凭据」→ 不覆盖、不自动创建；提示「目录非空且未见凭据」；允许保存但标不健康；运行按钮仍复制命令。用户选方案 1。
- 2026-08-29 裁决：用户同意可重复「检测」按钮。健康位不够：创建后为「未上线」；创建后自动检测一次，成功→「已上线」，失败保持未上线等用户点检测。上线后状态仍会变（限额、网络失败等）。「限额中」必须是单独状态（已知可恢复）；自动限额查询（限额中↔已上线）归后续卡，本期把状态位留出来。`Healthy bool` 不再并列为第二真相，改为一等状态字段。派发只放行「已上线」。
- 2026-08-29 裁决：已上线后网络失败等走单独「不可达」，不退回未上线、不继续冒充已上线。用户选方案 1。闭环状态：未上线 / 已上线 / 限额中 / 不可达。未上线=还没登录或还没建好；不可达=曾经上线、现在探不到。凭据失效仍回未上线（动作是复制命令登录）。探测写入点：创建（未上线）+ 检测（含创建后自动一次）。派发只读。限额自动查询后续卡。
- 2026-08-29 形态：fork `prototypes/b293-carrier-home/`（gitignore 临时工作区），改设置页自动化分区。
- 2026-08-29 用户确认原型形态「可以」，授权落 spec。
- 2026-08-29 spec 落盘 `docs/superpowers/specs/2026-08-29-b293-isolated-home-carrier-status-design.md`。定级 L3 轻档。批准：状态机与形态已在对话确认。

## contract 开工（cards/B293-charter-3 @ 8fcced3f）

- 2026-08-29 上游 spec 头部状态行已是「已批准」，无需代写。
- 2026-08-29 本卡 L3 **轻档**：直通竖切按纪律块归重档，本节点只落空壳与直通镜像，编译必须过。
- 2026-08-29 法定产出路径：`docs/superpowers/specs/b293-contract.md`。
- 2026-08-29 查证：`PutCarrier` 在 `internal/scheduling/scheduling.go` 把 `!Healthy` 翻 `true`；`admitInto` 跳过 `!Healthy`；`CarrierInput` 不含 healthy；`schedapi.go` 登记端点明文「不做跨机转发」。
- 2026-08-29 查证：凭据相对路径权威在 `internal/toolchain/detect.go` 的 `credRelPath`/`credRelPathFor`：opencode `.local/share/opencode/auth.json`、grok `.grok/auth.json`、codex `.codex/auth.json`；claude 无文件判据；Windows 上 opencode 无文件判据。
- 2026-08-29 查证：`hostapi.buildEnv` 在 `HomeDir` 非空时覆写 `HOME` 且赢过 `req.Env` 同名行；`DefaultTurnTimeout=30m`。codex `droppedEnvKeys` 丢 `CODEX_HOME`；grok `EnsureAuthLink` 软链 `~/.grok/auth.json`。协调者拉起已把 `carrier.HomeDir` 写入 `SessionSpec`（`scheddrain.go`）。
- 2026-08-29 查证：`defaultDataDir()` = `filepath.Join(UserHomeDir, ".handoff")`；`RepoRoot` 空则 `<DataDir>/repos`。本卡默认 HOME 用户可见串是 `~/.handoff/home/<名>`，不跟可改的 DataDir。
- 2026-08-29 查证：图基线有 `n_scheduling_Service_Admit` 等，**无** `PutCarrier` / `handleCarrierPut` / `admitInto`（图覆盖债）。`k_web_api_scheduling*` 在 baseline 有、best.json 未归属。
- 2026-08-29 查证：`forwardIfRequested`（`internal/agentd/forward.go`）是 `?machine=` 一跳搬运，带 `X-Handoff-Forwarded` 防环；探测必须走它，检测写状态不能整段转发（registry 在协调机）。
- 2026-08-29 Ticket 0 落盘：`internal/scheduling/status.go`、`internal/hostapi/probe.go`、`internal/agentd/hostprobe.go`；`SetupAutomation` 装配 `s.hostAPI`。PutCarrier/admitInto 行为未改。
- 2026-08-29 编译：`go build ./...` 退出码 0。`go vet` 触碰包 0。`gofmt -l` 空。
- 2026-08-29 测试：`go test ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/agentd/ -count=1` ok（agentd 63.8s）。含 `TestDefaultHomeDir`/`TestRunCommand`/`TestCarrierStatusLabels`/`TestCarrierRunCommandThroughWire`。
- 2026-08-29 fixture：`go test ./internal/proto/ -run TestContractFixtures -update` 新增 HomeProbeReq/Resp、HomeWakeReq/Resp、CarrierDetectResp、CarrierRunCommandResp。
- 2026-08-29 vitest：**未验证**（工作树无 `web/node_modules`）。
- 2026-08-29 图：validate issues=null；check --view cards-B293-charter-3 fails=[]；resolve --doc 17 锚 0 坏。best.json 补挂 k_web_api_scheduling*。
- 2026-08-29 本轮未碰 handoff CLI、未起新 executor。
- 2026-08-29 放弃：把 detect 塞进 PutCarrier（与 CAS 写绑超时进程）；给 detect 套 forwardIfRequested（会写错机账本）；默认 HOME 跟 DataDir（已登记串会漂）。

