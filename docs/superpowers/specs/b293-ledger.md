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

## breakdown 开工（cards/B293-charter-4 @ 3921d60e）

- 2026-08-29 上游核：spec 头部「已批准」；契约头部「随本提交冻结」+ 冻结提交 `3921d60e`（`git log -1` 原文 `contract(B293): freeze isolated HOME and four-state carrier`）。
- 2026-08-29 法定产出：`docs/superpowers/specs/b293-breakdown.md`。轻档：序贯单元，不 card split；岔口一律待拍板。
- 2026-08-29 图：`codegraph/best.json` 顶层 15 域。本卡触及 `d_scheduling`(logic) / `d_execution`(boundary，子域 host+adapters) / `d_gateway`(boundary) / `d_web`(logic) / `d_protocol`(logic) / `d_maintenance`(toolchain) / 条件触及 `d_ledger`+`d_transport`(DispatchOpts 透传)。`k_agentd_Server`→d_gateway，`k_agentd_Manager`→d_orchestration，`k_hostapi_*`→d_execution_host，`k_toolchain_*`→d_maintenance，`k_web_app_settings`→d_web_admin（parent d_web）。
- 2026-08-29 `codegraph --view cards-B293-charter-3 sym n_scheduling_PutCarrier`：domain d_scheduling，file scheduling.go:133，status added。`sym ProbeHome` → `n_hostapi_Host_ProbeHome` d_execution_host probe.go:68。`entity CarrierView` twins proto↔web/src/api/scheduling.ts；`projScanned: false`（警告勿当序列化边界清单）。无 view 时 `sym scheduling.Service.PutCarrier` 报「不在图中」——Ticket 0 符号只在卡片视图。
- 2026-08-29 包规模（亲自数）：`internal/agentd` 顶层非测试源文件 70、行 23147（递归 74 文件 / 23167 行）——命中实例化清单 20,000 行红线与「≥40 文件无子包」。scheduling 3 源+3 测；hostapi 5 源+1 测；codex 14 源；grok 13 源。
- 2026-08-29 API 事实：`PutCarrier` 仍 `if !c.Healthy { c.Healthy = true }`（scheduling.go:140-142）；`admitInto` 仍跳过 `!carrier.Healthy`（:354-356）。`ApplyDetect` / `ProbeHome` / `WakeHome` Ticket 0 空壳。`handleCarrierDetect` 读载体后调 `ApplyDetect(name, DetectEvidence{}, "")`，不调 WakeHome、不填 Version。`WakeRequest`/`HomeWakeReq` 无 Credential。`credRelPathFor` 未导出，仅 `internal/toolchain/detect.go` 包内使用。`target.json` 有 `d_execution → d_protocol` 无 `d_execution → d_maintenance`。
- 2026-08-29 API 事实：协调者拉起 `scheddrain.go` 已写 `carrier.HomeDir` 进 SessionSpec。`startCardStep` 只用 Binding 的 Target/Executor/Model，不读 HomeDir。`DispatchOpts` / `client.Dispatch` 手搭 map / `dispatchRequest` / `DispatchReq` 均无 home_dir。执行机 `Manager.Dispatch` 无编制账本。
- 2026-08-29 判断：冻结 §5 条 56（main_home_sync 拷贝）无 Host 方法也无 HTTP 路径；条 52 小队派发消费 HomeDir 无传输字段。两处都是新接缝，拆解不私加，提案退回 contract。凭据表复用不得另造，且不能 hostapi import toolchain。
- 2026-08-29 项目缺陷族清单 `2026-08-21-handoff-instantiation-checklist.md` §3：五族 + 序列化边界 + 枚举白名单 + webview 候选族。顶部**没有**「基线版本：charter@<commit>」——对不上基线标注，本拆解仍按项目清单加严答题（含承重安全属性）。
- 2026-08-29 形态权威存在：`prototypes/b293-carrier-home/pages/settings.html` 四态药丸、检测/运行、默认 HOME、三类探测提示。
- 2026-08-29 本轮未碰 handoff CLI、未起新 executor、未写实现代码。
- 2026-08-29 grok `7fc06468` 出稿后 ACP `context canceled`，零提交；工作树残留未跟踪 `b293-breakdown.md` 与契约 §12 / 台账草稿。协调者回收并拍板。
- 2026-08-29 拍板：岔口一 A（WakeRequest.Credential，退回 contract）、二 A（组装点注入，不改 target）、三 B（圈文件集，不插竖切）、四 A（进程 HOME=载体 HomeDir + grokhome 仍在）、五 空 status 当 pending 不扫库、六 A（POST /api/tasks home_dir，退回 contract）。
- 2026-08-29 退回原因（API 事实，协调者复核）：`WakeRequest` 无 Credential（`internal/hostapi/probe.go`）；`HomeWakeReq` 无 Credential（`internal/proto/scheduling.go`）；`DispatchOpts`/`DispatchReq`/`client.Dispatch` 手搭 map 均无 home_dir；`startCardStep`/`stepTransport` 不读 `carrier.HomeDir`；`scheddrain` 拉起半边已写 HomeDir。

## contract 补冻开工（cards/B293-charter-5 @ 6fb74fce）

- 2026-08-29 上游核：当前分支 `cards/B293-charter-5`，`git log -1` 为 `6fb74fce docs(B293): breakdown 出稿并拍板，退回 contract 补两条接缝`；spec 头部为「已批准」，breakdown 头部为「已拍板」；工作树开工时干净。
- 2026-08-29 查证：`WakeRequest`/`HomeWakeReq`/`handleHomeWake` 尚无 Credential；派发链 `ledgerstep.DispatchOpts`、`client.DispatchOpts`、`agentd.dispatchRequest`、`agentd.DispatchReq` 与 `client.Dispatch` 手搭 map 尚无 home_dir；`startCardStep` 不读取载体 HomeDir，`scheddrain` 已满足协调者拉起半边。
- 2026-08-29 判断：第 52 条采用 `*string` 可空字段，nil 表示 JSON 字段缺席，指向空串表示显式 `""`，非空指针表示载体 HomeDir；client 手搭 map 仅在指针非 nil 时写入 `home_dir`，避免 nil 被编码为 null。
- 2026-08-29 落地：为 WakeRequest/HomeWakeReq/TS wake 镜像补 Credential，并由 `handleHomeWake` 透传；为 ledgerstep→cmd→client→POST /api/tasks→agentd request/DispatchReq 补 HomeDir 字段与透传；不改 `startCardStep`、`Manager.Dispatch` 的行为，不实现 U2/U5 肉。
- 2026-08-29 尝试查图：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . check --view cards/B293-charter-5` 失败，原文：`Error: 读取视图 codegraph/diffs/cards/B293-charter-5.json: open codegraph/diffs/cards/B293-charter-5.json: no such file or directory`；`exit status 1`。现有视图文件命名为 `cards-B293-charter-3`，待生成本分支视图 diff 后复跑。
- 2026-08-29 图视图补齐：`codegraph validate` 输出 `issues: null`、退出码 0；`codegraph check --view cards-B293-charter-5` 输出 `fails: []`、退出码 0；既有 warns 保留。
- 2026-08-29 符号锚自检：修正契约表项为 `internal/scheduling/scheduling.go:133` 后，`codegraph resolve --doc docs/superpowers/specs/b293-contract.md --view cards-B293-charter-5` 退出码 0；输出无 `vanished`，本轮新增字段/签名锚均为 ok，其余既有锚为 ok/moved。
- 2026-08-29 验证：`go build ./...` 退出码 0；`go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/` 退出码 0；`gofmt -l` 无输出；`git diff --check` 退出码 0。
- 2026-08-29 验证：`go test ./internal/hostapi/ ./internal/ledgerstep/ -count=1` 为 hostapi 0.313s、ledgerstep 7.336s 均 ok；`go test ./internal/agentd/ -count=1` 为 `ok github.com/Xsxdot/handoff/internal/agentd 248.787s`；均退出码 0。
- 2026-08-29 验证：`go test ./internal/proto/ -run TestContractFixtures -update` 退出码 0，随后非 update fixture test 为 `ok .../internal/proto 0.005s`；`go test ./internal/client/ -run TestDispatchSerializesHomeDirThreeStates -count=1` 为 `ok .../internal/client 0.008s`；均退出码 0。
- 2026-08-29 验证：补加 `TestHomeWakeReqOmitsEmptyCredential` 锁定 `credential` 空值因 `omitempty` 缺席；`go test ./internal/proto/ -run 'TestContractFixtures|TestHomeWakeReqOmitsEmptyCredential' -count=1` 为 `ok .../internal/proto 0.003s`，退出码 0。
- 2026-08-29 验证：`go test ./... -run '^$' -count=1` 全仓测试包编译检查通过，退出码 0；各包输出 `[no tests to run]` 或无测试文件。
- 2026-08-29 判断：按冻结清单原子化纪律，将第 52 条拆为发送、缺席/空值语义、不得覆写三条；将第 56 条拆为供给时序、不得搬技能/规则、claude 无文件空操作三条；原条号保留为主断言。
- 2026-08-29 收口：契约正文、`codegraph/target.json`、`cards-B293-charter-5` 视图 diff、Ticket 0 字段/透传骨架、fixture/测试与本台账随本提交冻结；不 push。
- 2026-08-29 判断：`web/node_modules` 不存在，vitest 未验证；契约声明该欠账，不把 TS 测试伪报为通过。

## implement 开工（cards/B293-charter-7 @ 492523a7）

- 2026-08-29 基线核：`git status --short --branch` 输出 `## cards/B293-charter-7`；`git rev-parse HEAD` 输出 `492523a72684962e5e9c096f4490b0c0cee32036`；工作树无变更。
- 2026-08-29 U1 基线：`go test ./internal/scheduling/ -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/scheduling 3.386s`，退出码 0。
- 2026-08-29 U1 首红：新增 `TestPutCarrierLifecyclePreservesOrResetsStatus`、`TestApplyDetectUsesPriorityAndPreviousReachability`、`TestAdmissionRequiresOnlineCarrierAndSeparatesNoHealthyFromNoSlot` 后，定向 `go test ./internal/scheduling/ -run 'TestPutCarrierLifecyclePreservesOrResetsStatus|TestApplyDetectUsesPriorityAndPreviousReachability|TestAdmissionRequiresOnlineCarrierAndSeparatesNoHealthyFromNoSlot' -count=1` 退出码 1；原始关键输出为 `新建状态 = "online"/"caller must not set state"，want pending/empty` 与 `scheduling: 载体检测写状态尚未接线`。判断：红因是四态保存/检测行为缺失，不是测试拼写错误。
- 2026-08-29 U1 实现：移除 scheduling.Carrier Healthy，PutCarrier 按 expect/HOME 规则维护 pending 与旧状态，ApplyDetect 以 registry 版本 CAS 写入四态，admitInto 仅接受 StatusOnline；同步更新 scheduling fixture/read test。
- 2026-08-29 U1 定向绿：`gofmt -w internal/scheduling/scheduling.go internal/scheduling/status.go internal/scheduling/scheduling_test.go internal/scheduling/registry_read_test.go && go test ./internal/scheduling/ -run 'TestPutCarrierLifecyclePreservesOrResetsStatus|TestApplyDetectUsesPriorityAndPreviousReachability|TestAdmissionRequiresOnlineCarrierAndSeparatesNoHealthyFromNoSlot' -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/scheduling 0.905s`，退出码 0。
- 2026-08-29 U1 变异尝试：确认 `case ev.Quota:` 在 `status.go` 唯一命中后临时改为 `case ev.Quota && ev.NeedLogin:`；`go build ./...` 未通过，原始报错为 `internal/agentd/schedapi.go:270:48: c.Healthy undefined (type scheduling.Carrier has no field or method Healthy)`，因此该发不计入变异结论，已恢复原代码。待 U3 移除 agentd 旧投影后重做可编译变异。
- 2026-08-29 U2 首红：新增 ProbeHome/WakeHome 声明缝测试后，`go test ./internal/hostapi/ -run 'TestProbeHome|TestWakeHome' -count=1` 编译失败，原始报错为 `undefined: userHomeDir` 与 `undefined: NewWithCredentialPathFor`。判断：这是新测试缝尚未声明的允许首红，未进入行为结论。
- 2026-08-29 U2 基线：`go test ./internal/hostapi/ ./internal/toolchain/ -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/hostapi 0.315s`、`ok github.com/Xsxdot/handoff/internal/toolchain 0.002s`，退出码 0。
- 2026-08-29 U2 断言红：补齐 `Host` 注入缝但保留 ProbeHome/WakeHome 空壳后，`go test ./internal/hostapi/ -run 'TestProbeHome|TestWakeHome' -count=1` 退出码 1；原始关键输出为 `hostapi: 协调者会话承载尚未接线`，覆盖不存在/主 HOME 同步/波浪号/供给/occupied/超时六条入口断言。
- 2026-08-29 U2 实现：ProbeHome 以目标进程展开 `~` 并只读判定三态；WakeHome 支持空目标的 main_home_sync 单文件供给、私密权限、无 prompt `--version` 唤起、四类退出映射与上下文超时；toolchain 增加 `CredRelPathFor` 包装，SetupAutomation 在组装点注入。
- 2026-08-29 U2 局部绿：`gofmt -w internal/hostapi/probe.go internal/agentd/server.go internal/toolchain/detect.go && go test ./internal/hostapi/ ./internal/toolchain/ -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/hostapi 0.398s`、`ok github.com/Xsxdot/handoff/internal/toolchain 0.002s`，退出码 0；`go vet ./internal/hostapi/ ./internal/toolchain/` 与触及文件 `gofmt -l` 检查均退出码 0/无输出。
- 2026-08-29 U3 基线尝试：`go test ./internal/proto/ ./internal/agentd/ -run 'TestContractFixtures|TestCarrierRunCommandThroughWire|TestHomeProbe|TestHomeWake|TestCarrierDetect' -count=1` 中 proto 输出 `ok github.com/Xsxdot/handoff/internal/proto 0.005s`，agentd 编译失败，原始报错为 `internal/agentd/schedapi.go:270:48: c.Healthy undefined (type scheduling.Carrier has no field or method Healthy)`；判断：这是 U1 删除 Healthy 后 U3 旧投影尚未同步的已知跨 task 编译红。
- 2026-08-29 U3 TDD 纠偏红：暂撤回检测编排后，使用不依赖 PTY 根目录的最小 Handler 夹具执行 `go test ./internal/agentd/ -run 'TestCarrierDetectThroughHandlerWritesCoordinatorState|TestCarrierDetectRemoteWakesOnlyHostAndWritesLocalRegistry|TestCarrierDetectUnknownRemoteOutcomeFailsClosed|TestHostWakeAndProbeForwardPreserveCredentialAndRejectUnknownMachine' -count=1` 退出码 1；原始关键输出为 `检测回执 = {Name:c1 Status:pending LastError: Version:0}，want online/version2/c1`、`远程检测路径 = ""，want /api/host/wake`、`未知 outcome 应 502 ... 200 ... status pending`。判断：Handler 测试确实穿过真实路由并因 detect 编排缺失变红；未知机器转发分支通过。
- 2026-08-29 U3 实现：CarrierView/TS 侧移除 Healthy，carrierView 保留 status/last_error/version；detect handler 按本机/远程 WakeHome 编排并只在协调机 ApplyDetect，四种 outcome 白名单未知即 502；HomeProbe/Wake 日志不记录 credential。
- 2026-08-29 U3 定向绿：`gofmt -w internal/agentd/schedapi.go && go test ./internal/agentd/ -run 'TestCarrierDetectThroughHandlerWritesCoordinatorState|TestCarrierDetectRemoteWakesOnlyHostAndWritesLocalRegistry|TestCarrierDetectUnknownRemoteOutcomeFailsClosed|TestHostWakeAndProbeForwardPreserveCredentialAndRejectUnknownMachine' -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/agentd 0.827s`，退出码 0。
- 2026-08-29 U3/U5 边界基线：`go test ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/grok/ -count=1`；ledgerstep 输出 `ok .../internal/ledgerstep 10.752s`、client 输出 `ok .../internal/client 10.110s`、codex 输出 `ok .../internal/executor/codex 6.050s`、grok 输出 `ok .../internal/executor/grok 1.442s`；agentd 退出码 1，耗时 211.964s。原始失败包括 `TestCardStepAdmittedRoundReleasesCapacity: 小队 "sq1" 准入被拒: scheduling: 小队内没有已上线且有空的载体`、`TestCoordStatusEndpoint`、`TestCoordLaunchFailureReleasesCapacityAndKeeps502`，以及 scheddrain/wakeconsumer 的旧 fixture 均因 `小队内没有已上线且有空的载体` 失败；该基线结果不伪报为全绿，失败测试文件不在 U5 封闭修改集合内。
- 2026-08-29 U5 接缝首红：新增 `TestViaTemplateCarriesHomeDirPointer` 后，`go test ./internal/ledgerstep/ -run TestViaTemplateCarriesHomeDirPointer -count=1` 退出码 1，原始报错 `unknown field HomeDir in struct literal of type Dispatcher`；确认是 Dispatcher→Transport 接缝缺字段而非测试拼写错误。
- 2026-08-29 U5 Dispatcher 接线：为 `ledgerstep.Dispatcher` 增加可空 HomeDir 原指针，并在 `ViaTemplate` 写入 `DispatchOpts`；定向三态测试输出 `ok github.com/Xsxdot/handoff/internal/ledgerstep 0.360s`，退出码 0。
- 2026-08-29 U5 codex 首红：新增 `TestServeSpecKeepsCodexHomeWhenCarrierHomeIsPresent` 后定向测试退出码 1；原始断言为 `载体 HOME 非空时必须保留 CODEX_HOME`，实际 env 仅含当前进程 HOME 与 `HOME=~/.handoff/home/exec`，无 CODEX_HOME。
- 2026-08-29 U5 codex/grok 接线：Manager 非空 carrier HOME 删除 env 文件旧 HOME 后追加唯一 `HOME=`；codex 仅在该 HOME 非空时移除 CODEX_HOME 的 dropped 规则；grok 增加带 authority-home 的内部路径，并由 StartServe 从 env HOME 选择隔离 `.grok/auth.json`，保留旧导出调用者语义。
- 2026-08-29 U5 局部绿：`go test ./internal/executor/codex/ ./internal/executor/grok/ -run 'TestServeSpecShape|TestServeSpecKeepsCodexHomeWhenCarrierHomeIsPresent' -count=1` 通过；随后 `go test ./internal/ledgerstep/ ./internal/client/ ./internal/executor/codex/ ./internal/executor/grok/ -count=1` 输出 ledgerstep `ok ... 19.419s`、client `ok ... 13.918s`、codex `ok ... 6.299s`、grok `ok ... 1.767s`，退出码 0。
- 2026-08-29 U1–U3 定向回归：`go test ./internal/agentd/ -run 'TestDispatchPassesEnvToAdapter|TestCarrierDetectThroughHandlerWritesCoordinatorState|TestCarrierDetectRemoteWakesOnlyHostAndWritesLocalRegistry|TestCarrierDetectUnknownRemoteOutcomeFailsClosed|TestHostWakeAndProbeForwardPreserveCredentialAndRejectUnknownMachine' -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/agentd 7.144s`，退出码 0；`go test ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ -count=1` 输出 scheduling `ok ... 11.349s`、hostapi `ok ... 0.358s`、proto `ok ... 0.006s`，退出码 0。
- 2026-08-29 U5 收尾检查：`go build ./...` 退出码 0；`go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/grok/` 退出码 0；指定 `gofmt -l` 与 `git diff --check` 均无输出、退出码 0；`web/node_modules absent`，U4 vitest 仍未验证。
- 2026-08-29 变异自验：`case ev.Quota:` 唯一命中次数为 1。第一发等价变异 `case ev.Quota && ev.NeedLogin:` 在 `go build ./...` 通过后，定向 ApplyDetect 行为仍通过，判定为未打中该测试的 quota-only 分支，不计测试存活结论；立即恢复并改用语义变异 `case ev.Quota && !ev.NeedLogin:`。
- 2026-08-29 变异自验有效发：语义变异先经 `go build ./...` 通过，再跑 `go test ./internal/scheduling/ -run TestApplyDetectUsesPriorityAndPreviousReachability -count=1`；退出码 1，原始断言为 `结果 = "pending"/"quota"，want "quota"/"quota"`（quota_beats_login），证明测试拦住优先级破坏；已恢复 `case ev.Quota:`。

## plan 开工（cards/B293-charter-6 @ 6a0fb082）

- 2026-08-29 基线核：`git status --short --branch` 输出 `## cards/B293-charter-6`；`git rev-parse HEAD` 输出 `6a0fb082eded6a6b18aa9f2eb3fc543a15f4daa9`；工作树无变更。
- 2026-08-29 形态核：`find prototypes/b293-carrier-home -maxdepth 4 -type f -print` 找到 `pages/settings.html`、`README.md`、`shared/styles.css`。
- 2026-08-29 图：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_scheduling` 成功，返回 `view=baseline`、`d_scheduling`、`n_scheduling_New`，并警告 `领域声明缺失：codegraph/domains/d_scheduling.json`；`d_execution_host`、`d_execution_adapters`、`d_gateway`、`d_protocol`、`d_web_admin`、`d_web_contract`、`d_ledger`、`d_transport_channel` context 均实际运行，返回对应领域上下文并保留领域声明缺失/未扫描入口警告。
- 2026-08-29 图符号核：`cards-B293-charter-3` 视图定位 `n_scheduling_PutCarrier`=`func (s *Service) PutCarrier(c Carrier, expect int) error`、`n_scheduling_admitInto`=`func (s *Service) admitInto(q Squad, req IgnitionRequest) (Binding, error)`、`n_scheduling_Service_ApplyDetect`=`func (s *Service) ApplyDetect(name string, ev DetectEvidence, detail string) (Carrier, error)`、`n_hostapi_Host_ProbeHome`=`func (h *Host) ProbeHome(ctx context.Context, req ProbeRequest) (ProbeReply, error)`、`n_hostapi_Host_WakeHome`=`func (h *Host) WakeHome(ctx context.Context, req WakeRequest) (WakeReply, error)`、`n_agentd_Server_handleHomeProbe`、`n_agentd_Server_handleHomeWake`、`n_agentd_Server_handleCarrierDetect`、`n_agentd_Server_SetupAutomation`；`cards-B293-charter-5` 视图定位 `n_agentd_Server_startCardStep`、`n_agentd_Server_handleDispatch`、`n_client_Client_Dispatch`。
- 2026-08-29 图覆盖债：用 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --view cards-B293-charter-3 flow n_scheduling_PutCarrier`（及同批其余关键符号）实际失败，原始输出为 `Error: unknown command "flow" for "codegraph"`、`Run 'codegraph --help' for usage.`、`exit status 1`；本计划对缺失流程视图改读源码，且不把 `chain` 冒充 `flow`。
- 2026-08-29 图上游核：`who-calls n_scheduling_Service_ApplyDetect` 返回 `n_agentd_Server_handleCarrierDetect`；`who-calls n_hostapi_Host_ProbeHome` 返回 `n_agentd_Server_handleHomeProbe`；`who-calls n_hostapi_Host_WakeHome` 返回 `n_agentd_Server_handleHomeWake`；`who-calls n_agentd_Server_startCardStep` 返回 `n_agentd_Server_handleCardStep`；`who-calls n_client_Client_Dispatch` 返回 `n_cmd_dispatchCmd_RunE` 与 `e_cli_dispatch`；各结果带 `unscannedEntries=6` 警告。
- 2026-08-29 基线测试：`go test ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/client/ ./internal/ledgerstep/ -count=1` 原始输出为 `ok github.com/Xsxdot/handoff/internal/scheduling 0.855s`、`ok github.com/Xsxdot/handoff/internal/hostapi 0.337s`、`ok github.com/Xsxdot/handoff/internal/proto 0.010s`、`ok github.com/Xsxdot/handoff/internal/client 9.525s`、`ok github.com/Xsxdot/handoff/internal/ledgerstep 8.611s`，退出码 0。
- 2026-08-29 基线测试：`go test ./internal/agentd/ -count=1` 原始输出为 `ok github.com/Xsxdot/handoff/internal/agentd 190.287s`，退出码 0。
- 2026-08-29 基线编译：`go build ./...` 退出码 0；`go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/` 退出码 0；均无标准输出。
- 2026-08-29 Web 基线：检查输出 `web/node_modules absent`；`web/package.json` 的测试脚本为 `vitest run`，本轮 vitest 未验证。
- 2026-08-29 U5 调用面核：ledgerstep.Dispatcher 的 HomeDir *string 是把载体值写入 DispatchOpts 的唯一模板装配点；stepTransport 已将该指针传给 client，后续 Client.Dispatch 已将非 nil 值写入 /api/tasks。Manager.Dispatch 在 ad.Start 前构造 executor.StartReq{Env: envKVs}，因此执行侧计划在该边界追加非空 HOME=，不扩张 StartReq。
- 2026-08-29 U5 adapter 核：codex serveSpec 当前总是用 droppedEnvKeys 丢掉 CODEX_HOME；grok StartServe 当前调用 EnsureAuthLink(homeDir)，而 EnsureAuthLink 经 authorityAuthPath 读取宿主 os.UserHomeDir() 的 .grok/auth.json。计划将两者都改为“非空载体 HOME 才覆盖”且保持 grokhome 任务目录存在，并以测试缝验证。
- 2026-08-29 U5 远程检测判断：internal/agentd/forward.go#forwardJSON 已能按 target client 搬运 JSON、添加防环头并保留目标状态码；handleCarrierDetect 应构造 /api/host/wake 子请求调用它，再在协调机调用 ApplyDetect，不能把 detect 请求自身交给 forwardIfRequested。
- 2026-08-29 plan 落盘：docs/superpowers/plans/b293-plan.md 共 586 行，包含 U1–U5 的有界文件、Interfaces、基线命令、行为化验收、日志/注释、缺陷族、序列化边界、接缝双向覆盖和协调者真机清单。
- 2026-08-29 plan 自审：rg -n "TBD|同 Task|适当的错误处理|TODO|FIXME" docs/superpowers/plans/b293-plan.md 无输出；git diff --check 无输出；计划中仅 TypeScript 泛型使用尖括号，无计划占位标记。
- 2026-08-29 plan 判断：U1–U5 依 breakdown 轻档顺序合并为单一序贯实现计划；U2 的 CLI argv 保留 contract §9 的实现票授权边界，未凭记忆补造外部 CLI 行为；Web Vitest 因 web/node_modules 缺失继续标未验证。
- 2026-08-29 最终审计：`rg -n \"Healthy|healthy\" internal/scheduling internal/agentd/schedapi.go web/src/app/settings web/src/api` 退出码 0；命中仅为 ErrNoHealthy 哨兵、测试断言和迁移说明，未命中实现读取。`rg -n \"home_dir|status|last_error|credential|ProbeKind|WakeOutcome\"` 退出码 0，声明接缝均可见。
- 2026-08-29 最终 Go 闸门：`go build ./...` 退出码 0；`go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/grok/` 退出码 0；指定 `gofmt -l` 与 `git diff --check` 无输出且退出码 0。
- 2026-08-29 Web 验证状态：再次检查 `web/node_modules` 不存在；按计划不安装依赖，`SchedulingPage.test.tsx`/`contract.test.ts` 的 Vitest 结果未验证。
- 2026-08-29 U3 过滤补验：`go test ./internal/agentd/ -run 'TestCarrierRunCommandThroughWire|TestCarrierDetect' -count=1 -v` 中 detect 三测均 PASS；原有 `TestCarrierRunCommandThroughWire` 原始输出为 `PTY 测试根目录不可用 ... read-only file system`，随后 `--- SKIP`，命令退出码 0，故该测试本身未验证。新增无 PTY 的 `TestCarrierRunCommandThroughDirectHandler` 通过真实 Handler，输出 `PASS`、`ok .../internal/agentd 0.136s`，退出码 0。

## implement 收口（cards/B293-charter-7）

- 2026-08-29 最终局部测试：`go test ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/executor/codex/ ./internal/executor/grok/ -count=1` 退出码 0；原始输出为 scheduling `ok ... 1.981s`、hostapi `ok ... 0.354s`、proto `ok ... 0.003s`、ledgerstep `ok ... 8.080s`、client `ok ... 9.487s`、codex `ok ... 6.010s`、grok `ok ... 1.408s`。
- 2026-08-29 最终 agentd 定向测试：`go test ./internal/agentd/ -run 'TestDispatchPassesEnvToAdapter|TestCarrierDetectThroughHandlerWritesCoordinatorState|TestCarrierDetectRemoteWakesOnlyHostAndWritesLocalRegistry|TestCarrierDetectUnknownRemoteOutcomeFailsClosed|TestHostWakeAndProbeForwardPreserveCredentialAndRejectUnknownMachine|TestCarrierRunCommandThroughDirectHandler' -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/agentd 1.504s`，退出码 0。
- 2026-08-29 最终 Go 闸门：`go build ./...`、`go vet ./internal/scheduling/ ./internal/hostapi/ ./internal/proto/ ./internal/ledgerstep/ ./internal/client/ ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/grok/`、指定变更 Go 文件的 `gofmt -l`、`git diff --check` 均无输出且退出码 0。
- 2026-08-29 最终四态审计：`rg -n 'Healthy|healthy' internal/scheduling internal/agentd/schedapi.go web/src/app/settings web/src/api` 退出码 0；命中仅为 `ErrNoHealthy` 哨兵、测试断言与迁移说明，未命中实现读取。
- 2026-08-29 变更范围核：`git diff --name-only` 仅列出计划 U1–U5 文件与本台账；计划文件无 diff；新增测试为 `internal/agentd/hostprobe_test.go`、`internal/hostapi/probe_test.go`，均属对应 task 的测试接缝。
- 2026-08-29 Web 验证：检查输出 `web/node_modules absent`；未安装依赖，`SchedulingPage.test.tsx` 与 `contract.test.ts` 的 Vitest 结果未验证。
- 2026-08-29 全量 agentd 结果保留失败事实：先前 `go test ./internal/agentd/ -count=1` 退出码 1，失败集中在未纳入 U5 封闭修改集合的旧 fixture（隐含 Healthy 的 cardstep/scheddrain/wakeconsumer 等测试）；本轮不修改计划外测试，handoff verdict 不将该结果伪报为全绿。
- 2026-08-29 提交：已按计划创建实现提交，未 push；提交前暂存检查与 `git diff --cached --check` 均退出码 0。

## implement 复验修复（cards/B293-charter-7）

- 2026-08-29 复现：原 hostapi 两个 WakeHome 测试在本机 PATH 假 CLI 下均通过，但该形态仍可能命中协调者机真 `opencode`；按要求不把本机一次绿视为隔离证明。agentd 代表性复现命令 `go test ./internal/agentd/ -run 'TestCoordLaunchEndpointSuccess|TestAutomationQueueRestartReplay|TestAutomationFallbackResumeRebuildFailure' -count=1 -v` 退出码 1，原始失败为 `处理行数=1，want 3` 与 `resume/rebuild 双失败 processed=0 escalated=false err=协调者小队 coord 准入失败: scheduling: 小队内没有已上线且有空的载体`；其中 PTY 测试原始输出 `PTY 测试根目录不可用 ... read-only file system` 后 SKIP。
- 2026-08-29 hostapi 测试修复：测试替换包内 `commandContext`，以 `os.Args[0]` 绝对路径启动 `TestWakeHomeFakeProcess`；record 模式立即退出并记录 argv/HOME，block 模式等待计时器、由父 context 终止。中途纯 `select{}` 假进程首红，原始输出为 `fatal error: all goroutines are asleep - deadlock!`，已改为带计时器的阻塞进程。
- 2026-08-29 agentd 夹具修复：coordapi、scheddispatch、scheddrain、wakeconsumer 的旧 Healthy=true 语义载体补 `Status: scheduling.StatusOnline`，并经测试辅助函数调用真实 `ApplyDetect(Reachable:true)` 写回；未改生产准入语义。
- 2026-08-29 hostapi 复验：`go test ./internal/hostapi/ -run 'TestWakeHomeFakeProcess|TestWakeHomeSuppliesMainCredentialBeforeNoPromptCLI|TestWakeHomeOccupiedNeverOverwrites|TestWakeHomeHonorsTimeoutWithoutRunTurn' -count=1 -v` 输出四测 PASS，timeout 原始日志含 `唤起 CLI "opencode" 超时/取消 ... context deadline exceeded`，包结果 `ok .../internal/hostapi 0.050s`，退出码 0。
- 2026-08-29 agentd 夹具复验：`go test ./internal/agentd/ -run 'TestCoordLaunchEndpointSuccess|TestAutomationQueueRestartReplay|TestAutomationFallbackResumeRebuildFailure|TestAutomationWakeFailureAdvancesCursor' -count=1 -v` 输出 3 测 PASS、PTY 测试因 `read-only file system` SKIP，包结果 `ok .../internal/agentd 0.899s`，退出码 0。
- 2026-08-29 指定最终测试一：`go test ./internal/hostapi/ ./internal/scheduling/ -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/hostapi 0.362s`、`ok github.com/Xsxdot/handoff/internal/scheduling 1.619s`，退出码 0。
- 2026-08-29 指定最终测试二：`go test ./internal/agentd/ -count=1` 输出 `ok github.com/Xsxdot/handoff/internal/agentd 202.506s`，退出码 0。
- 2026-08-29 指定最终测试三：`go build ./...` 无标准输出，退出码 0。
