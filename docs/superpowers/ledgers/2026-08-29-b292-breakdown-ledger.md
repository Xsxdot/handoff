# B292 breakdown 台账（2026-08-29）

本台账记录本节点亲自执行的命令、原始结果和判断；实现不在本节点落地。

## 开工与上游状态

- 2026-08-29：读取 `/root/.codex/skills/handoff/SKILL.md`；本节点为 handoff executor，不调用 handoff CLI、不派发子任务、不启动新的 executor。
- 2026-08-29：`git status --short --branch` 原始输出为 `## cards/B292-charter-2`；工作树干净。
- 2026-08-29：`git log -6 --oneline --decorate` 原始输出首行为 `8e350e00 (HEAD -> cards/B292-charter-2, origin/cards/B292-charter, cards/B292-charter) contract(B292): freeze squad member concurrency`；当前提交为契约冻结提交。
- 2026-08-29：读取 spec `docs/superpowers/specs/2026-08-29-b292-squad-member-concurrency-design.md`（112 行）与 contract `docs/superpowers/specs/b292-contract.md`（247 行）；spec 头部为 `状态：已批准（用户，2026-08-29）`，contract 头部为 `上游状态：已批准`、`冻结状态：本提交随 codegraph/target.json 与 codegraph/diffs/cards-B292-charter.json 冻结`，两项状态位均已回写，结论：可进入 breakdown。
- 2026-08-29：尝试 `git diff --stat acc/b156.2-156.3..HEAD`；原始报错为 `fatal: ambiguous argument 'acc/b156.2-156.3..HEAD': unknown revision or path not in the working tree.`；改用存在的 `origin/acc/b156.2-156.3` 作为基线，未据失败命令作结论。

## 代码图与基线事实

- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --help` 运行成功；确认可用子命令含 `domains`、`check`、`resolve`、`sym`、`validate`。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . domains` 运行成功；`best.json` 顶层（无 parent）领域为 `d_collab`、`d_coordination`、`d_execution`、`d_keystone`、`d_ledger`、`d_protocol`、`d_runtime`、`d_scheduling`、`d_sessions`、`d_transport`、`d_web`、`d_workspace`。B292 实际触及顶层 `d_scheduling`、`d_coordination`、`d_protocol`、`d_web`；契约文字中的 `d_gateway`/`d_orchestration`/`d_cli` 是现有 target/子域别名，不新增图方向。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . views` 原始输出包含 `cards-B292-charter`；当前 B292 视图存在。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . check --view cards-B292-charter` 退出码 0，原始 JSON 顶层为 `"fails": []`；输出同时含既有 warnings（legacy、container-misplaced、oversized-package 等），本节点不把 warnings 写成失败。
- 2026-08-29：`codegraph sym` 实测命中 `m_scheduling_SquadMember`（`internal/scheduling/scheduling.go`，anchor `ok`，status `added`，domain `d_scheduling`）、`m_proto_SquadMember`（`internal/proto/scheduling.go`，anchor `ok`，status `added`，domain `d_protocol`）、`m_web_api_scheduling_SquadMember`（`web/src/api/scheduling.ts`，anchor `ok`，status `added`，domain `d_web`）。
- 2026-08-29：用 `file#Symbol` 查询调度服务方法、agentd 清队/HTTP handler、CLI 和 Web 页面均返回原始错误 `Error: 符号 "..." 不在图中（图未覆盖或名字有误）；近似候选: []`；判断：breakdown 只把这些作为文件+符号现状指针，并单列图覆盖债，不伪造可解析锚。

## 冻结骨架与欠账事实

- 2026-08-29：`git diff --stat origin/acc/b156.2-156.3..HEAD` 原始统计为 `26 files changed, 620 insertions(+), 103 deletions(-)`；变更包含 scheduling、agentd 测试/登记投影、proto、CLI、Web 和 B292 图/契约文档。
- 2026-08-29：读取 `internal/scheduling/scheduling.go`；实见 `SquadMember{Carrier, MaxConcurrency}`、`Squad{Members []SquadMember}`、`Service.Admit`/`LaunchAdmit`/`Release`，成员键形状为 `squad/<队>/<载体>`、物理键为 `carrier/<载体>`，`admitInto` 在健康成员中逐个尝试并把 `ErrNoSlot` 与 `ErrNoHealthy` 分流。该代码为当前 Ticket 0/契约骨架事实，不等同于本节点实现验收已通过。
- 2026-08-29：读取 `internal/agentd/scheddrain.go`；实见 `drainQueuesOnce` 在协调者拉起任意错误回填后 `return processed, nil`，因此 B292 的“仅 ErrNoSlot 回填并继续本轮”仍是实现欠账；`launchCoordinatorRound` 通过 `coordinatorAdmissionError` 包装准入错误。
- 2026-08-29：读取 `cmd/squad.go`；实见 `--member` 仍是 `StringSlice`，`squadMemberInputs` 只生成 `{carrier}`，`formatSquadMembers` 已可展示 `/政策位`；判断：CLI 成员政策位字符串语法未冻结，必须在稿首列为待拍板实现选择。
- 2026-08-29：读取 `web/src/app/settings/SchedulingPage.tsx`；实见小队弹窗只有成员勾选，`squadDraft` 保留成员对象但没有每成员并发输入；判断：Web 每成员输入是 contract §8 明列欠账，必须进入后续实现卡验收。
- 2026-08-29：读取 `internal/agentd/schedapi.go`、`internal/proto/scheduling.go`、`web/src/api/scheduling.ts` 与现有相关测试；实见 Go/HTTP/TS 成员对象形状和 `omitempty` 投影已存在，`handleSquadPut`/`squadView` 是手写投影边界，contract fixture 与 Web fixture 是消费边界。

## 节点产物与验证（待收口）

- 2026-08-29：已按 apply_patch 创建法定产出 `docs/superpowers/specs/b292-breakdown.md`；`wc -l` 实际读数为 445 行，结构扫描命中待拍板、触及子系统、派卡资格、契约增量核对、四段式入口、缺陷族/追加设问和真机清单。
- 2026-08-29：已按 apply_patch 为 `docs/superpowers/specs/b292-contract.md` §10 增加只记录图层级/契约别名归属的修订行；不改变冻结语义、不新增接缝。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b292-breakdown.md --view cards-B292-charter` 退出码 0；原始 JSON 返回 24 个锚点，状态为 `ok` 或 `moved`，无错误对象/坏锚。模型锚 `SquadMember`/`Squad`/`SquadView`/`SquadInput` 为 `ok`，方法和入口锚为 `ok` 或 `moved`。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate --view cards-B292-charter` 退出码 0；原始 JSON 为 `containers:269`、`domains:23`、`edges:4796`、`nodes:3808`、`unscannedEntries:6`，`edgeIssues:null`、`issues:null`，视图列表含 `cards-B292-charter`。
- 2026-08-29：`rg -n '^## |^### |^#### |待拍板|触及子系统|派卡资格|契约增量核对|缺陷族|追加设问|真机清单|入口符号|有界文件集' docs/superpowers/specs/b292-breakdown.md` 退出码 0；原始输出命中稿首 P1/P2/P3、四个顶层领域、§1–§5、B292-I0 四段式和真机清单。
- 2026-08-29：`git diff --check` 退出码 0，无输出；`git status --short --branch` 原始输出为当前分支加 1 个已修改 contract、2 个预期新增 breakdown/ledger 文件；工作树内无其它路径。
- 2026-08-29：`git add docs/superpowers/specs/b292-contract.md docs/superpowers/specs/b292-breakdown.md docs/superpowers/ledgers/2026-08-29-b292-breakdown-ledger.md && git diff --cached --check && git diff --cached --stat && git diff --cached --name-status` 退出码 0；原始统计为 `3 files changed, 492 insertions(+)`，仅列上述 3 个预期文件，暂存区空白检查无输出。
- 2026-08-29：`git add docs/superpowers/ledgers/2026-08-29-b292-breakdown-ledger.md && git diff --cached --check && git commit -m "docs(b292): add concurrency breakdown"` 退出码 0；原始输出为 `[cards/B292-charter-2 75287d6b] docs(b292): add concurrency breakdown`、`3 files changed, 493 insertions(+)`，未 push。
- 2026-08-29：`git add docs/superpowers/ledgers/2026-08-29-b292-breakdown-ledger.md && git diff --cached --check && git diff --cached --stat && git commit -m "chore(ledger): close B292 breakdown"` 退出码 0；原始输出为 `1 file changed, 1 insertion(+)`、`[cards/B292-charter-2 d7067bb9] chore(ledger): close B292 breakdown`，未 push。
- 2026-08-29：最终只读复核 `git status --short --branch && git log -4 --oneline --decorate && git show --stat --oneline d7067bb9 && git show --stat --oneline 75287d6b && git diff --check` 退出码 0；原始状态为 `## cards/B292-charter-2`，HEAD 为 `d7067bb9`，其前一提交为 `75287d6b`，diff check 无输出，工作树干净。
- 2026-08-29：再次运行 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b292-breakdown.md --view cards-B292-charter` 退出码 0；原始 JSON 返回 24 个锚点，全部为 `ok` 或 `moved`，无坏锚。
- 2026-08-29：`git add docs/superpowers/ledgers/2026-08-29-b292-breakdown-ledger.md && git diff --cached --check && git commit -m "chore(ledger): record B292 breakdown closeout"` 退出码 0；原始输出为 `[cards/B292-charter-2 5675a905] chore(ledger): record B292 breakdown closeout`、`1 file changed, 3 insertions(+)`，未 push。
- 2026-08-29：随后 `git status --short --branch && git log -5 --oneline --decorate && git diff --check` 退出码 0；原始输出首行为 `## cards/B292-charter-2`，当时 HEAD 为 `93266226`，diff check 无输出；该状态核对发生在最后一笔台账收口前，分支和工作树范围未变。

## B292 plan 节点读数

- 2026-08-29：当前执行树 `git status --short --branch` 原始输出为 `## cards/B292-charter-3`；`git log -8 --oneline --decorate` 首行为 `d43c51f8 (HEAD -> cards/B292-charter-3, cards/B292-charter-2) chore(ledger): finalize B292 ledger`；工作树干净，当前分支未切换。
- 2026-08-29：读取法定输入 `docs/superpowers/specs/2026-08-29-b292-squad-member-concurrency-design.md`（112 行）、`docs/superpowers/specs/b292-contract.md`（256 行）、`docs/superpowers/specs/b292-breakdown.md`（445 行）；确认 spec 已批准、contract 已冻结、breakdown 仍声明待拍板且把 P1/P2/P3 作为实现轮前置岔口。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_scheduling` 退出码 0；返回 best 领域 `d_scheduling`、包 `internal/schedclient`/`internal/scheduling`、入口 `func New(repo schedclient.Registry) *Service`，并警告 6 个未扫描入口及 `d_scheduling` 声明缺失；本计划保留函数 file:line 现状锚并不把空查询当无调用方。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_coordination` 退出码 1，原始错误为 `Error: 领域 "d_coordination" 不在最优树词表中；context 已按最优树词表取声明，最优树领域候选: d_cli, d_collab, d_execution, d_execution_adapters, d_execution_contract, d_execution_host, d_gateway, d_keystone, d_ledger, d_maintenance, d_orchestration, d_policy, d_protocol, d_scheduling, d_sessions, d_transport, d_transport_channel, d_transport_tunnel, d_web, d_web_admin, d_web_cards, d_web_command, d_web_contract, d_web_shell, d_web_workbench, d_workspace。"d_coordination" 是现状视图领域 id，它在 best.json 里没有任何最优树领域归属`；结论：计划使用 best 词表的 `d_gateway`/`d_orchestration`/`d_cli` 与 `d_web`，不伪造 `d_coordination` 上下文。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_protocol` 退出码 0；返回协议领域 `internal/proto`，该上下文的链节点为通用协议模型，未命中 B292 专用函数；计划仍以 contract 的 file:line 与已验证成员 DTO 节点作为精确边界。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_web` 退出码 0；实际容器大多当前位于 `d_web`，best 归属分散到 `d_web_shell`/`d_web_contract`/`d_web_admin` 等，响应报告 `misplaced`/`misplacedSkipped` 与声明缺失；计划只圈定 B292 列出的 Web API/设置页文件，不扩大为整个 Web 域。
- 2026-08-29：读取 `codegraph/diffs/cards-B292-charter.json`，实见当前视图新增/修改的是 scheduling、Go proto、Web scheduling 成员 DTO 与 `Service.Release` 成员计数键骨架；读取 contract/breakdown 现状后确认 `drainQueuesOnce` 的 ErrNoSlot 回填继续本轮、CLI `--member` 政策语法、Web 每成员输入仍需实现轮补齐。
- 2026-08-29：图命令 `--help` 实测退出码 0，确认 `context`、`chain`、`sym`、`who-calls` 可用；`sym m_scheduling_Squad` 命中 `internal/scheduling/scheduling.go:77` 且状态 `modified`，`sym n_scheduling_Service_Admit` 命中签名 `func (s *Service) Admit(req IgnitionRequest) (Binding, error)`（anchor=moved），`sym n_scheduling_Service_LaunchAdmit` 命中签名 `func (s *Service) LaunchAdmit(squadName string) (Binding, error)`（anchor=moved）。
- 2026-08-29：图查询 `sym internal/agentd/scheddrain.go#drainQueuesOnce` 退出码 1，原始错误为 `Error: 符号 "internal/agentd/scheddrain.go#drainQueuesOnce" 不在图中（图未覆盖或名字有误）；近似候选: []。确认图未覆盖时回落 grep，并把该符号记入本节点产出物的「图覆盖债」小节`；计划对清队入口使用已实读的 file:line，不把图空结果当无调用方。
- 2026-08-29：图 `chain ... --with-source` 对 `Admit`、`LaunchAdmit`、`Release`、`Squad` 均退出码 0，均只返回焦点节点且 `edges:null`，并报告 `unscannedEntries:6`；`who-calls` 对 `Service.Release` 和 `SquadMember` 亦仅返回焦点、无 edges，并报告相同债务，计划把这些调用面按 contract/breakdown 的实读清单再用 grep 核对。
- 2026-08-29：基线 `go test ./internal/scheduling -count=1` 退出码 0，原始输出 `ok  github.com/Xsxdot/handoff/internal/scheduling  0.550s`。
- 2026-08-29：基线首次并行执行的 agentd 命令在 30 秒工具窗口内未返回输出，未据此判通过；单独重跑 `go test ./internal/agentd -run 'Scheduling|Automation|Queue' -count=1` 退出码 0，原始输出 `ok  \\tgithub.com/Xsxdot/handoff/internal/agentd\\t3.512s`。
- 2026-08-29：基线 `go test ./internal/proto ./internal/agentd -run 'ContractFixtures|Squad|Scheduling' -count=1` 退出码 0，原始输出仅含 `ok  github.com/Xsxdot/handoff/internal/proto  0.025s`；agentd 无匹配测试输出，不能据此宣称 agentd HTTP 覆盖已通过。
- 2026-08-29：基线 `go test ./cmd -run 'Squad' -count=1` 退出码 0，原始输出 `ok  github.com/Xsxdot/handoff/cmd  0.109s`。
- 2026-08-29：基线 `cd web && npm run typecheck` 退出码 127，原始输出为 `> web@0.0.0 typecheck`、`> tsc -b`、`sh: 1: tsc: not found`；基线 `cd web && npm test -- --run src/app/settings/SchedulingPage.test.tsx src/api/scheduling.fetch.test.ts src/api/contract.test.ts` 退出码 127，原始输出为 `> web@0.0.0 test`、`> vitest run --run ...`、`sh: 1: vitest: not found`，未把缺依赖冒写成测试失败。
- 2026-08-29：在 `web` 目录执行 `npm ci` 退出码 0，原始输出为 `added 290 packages, and audited 291 packages in 2s`、`found 0 vulnerabilities`；后续 Web 基线可复跑，安装产物属于 gitignore 范围，不纳入计划产出。
- 2026-08-29：依赖安装后基线 `cd web && npm run typecheck` 退出码 0，原始输出为 `> web@0.0.0 typecheck`、`> tsc -b`，无错误。
- 2026-08-29：依赖安装后基线 `cd web && npm test -- --run src/app/settings/SchedulingPage.test.tsx src/api/scheduling.fetch.test.ts src/api/contract.test.ts` 退出码 0，原始摘要为 `Test Files 3 passed (3)`、`Tests 49 passed (49)`、Vitest `v4.1.10`。
- 2026-08-29：基线 `go build ./...` 退出码 0、无输出；基线 `go vet ./internal/scheduling ./internal/agentd ./internal/proto ./cmd` 退出码 0、无输出。
- 2026-08-29：`git merge-base HEAD origin/acc/b156.2-156.3` 原始输出为 `5d06fce027031e3f6202953e02d4883f9de05c4b`，`git rev-parse origin/acc/b156.2-156.3` 为 `8fcced3f0c28f17e0c728a4dcc0b43eba1e20b05`；`git diff --name-only origin/acc/b156.2-156.3..HEAD` 显示当前卡分支已含 B292 骨架与历史 B293/其它改动，计划实现文件集严格沿 `b292-breakdown.md` 列表，不触碰这些无关路径。
- 2026-08-29：实读 `internal/scheduling/scheduling.go` 当前实现：公开 `Admit`/`LaunchAdmit` 保持契约签名，`admitInto` 逐个健康成员尝试，成员键为 `squad/<队>/<载体>`、物理键为 `carrier/<载体>`；`drainQueuesOnce` 当前在协调者拉起任意错误回填后立即 `return processed, nil`（`internal/agentd/scheddrain.go:85-124`），这是 P2 要改的唯一运行时欠账。
- 2026-08-29：实读 `internal/agentd/schedapi.go:119-163`、`internal/proto/scheduling.go:23-61`、`internal/client/squads.go:33-76`、`web/src/api/scheduling.ts:18-130`；成员对象的 Go/HTTP/TS 字段已是 `carrier` 与可选 `max_concurrency`，PUT/GET URL 与 Client 方法签名不变；`putSquad` 当前直传 members，需由实现轮锁 0/缺席字段和真实投影。
- 2026-08-29：实读 `cmd/squad.go:37-176`：create/set 仍用 `StringSliceVar` 的重复 `--member`，当前 `squadMemberInputs([]string) []proto.SquadMember` 只生成 carrier，未注册 `--max-concurrency`；实现轮需在发 HTTP 前解析 P1 语法并将非法值拒绝。
- 2026-08-29：实读 `web/src/app/settings/SchedulingPage.tsx:1-218` 与 `prototypes/b292-squad-concurrency/pages/settings.html:233-304,439-500`；原型要求小队卡成员 chip 显示政策位、弹窗每个载体一行勾选+政策输入且展示 CLI 默认/显式模型，当前 React 弹窗只有勾选；实现轮需保持保存失败 modal/draft。
- 2026-08-29：实读现有测试夹具：`internal/scheduling/scheduling_test.go:newCASFixture`、`internal/agentd/scheddrain_test.go:setupNoPTYSquadEnv/seedQueueCoordinator`、`internal/agentd/schedapi_test.go:newSchedEnv/schedReq`、`cmd/squad_test.go:stubSquadAgentd/setStub/runLedgerCLI`、`web/src/app/settings/SchedulingPage.test.tsx` 的 user-event 页面入口；计划声明并复用这些 package-specific harness，不以 helper 直测替代接缝测试。

- 2026-08-29：按法定路径用 `apply_patch` 创建 `docs/superpowers/plans/b292-plan.md`；计划明确写入 P1=A 的 `--member carrier[:positive-int]`、P2=A 的 `drainQueuesOnce` 局部 deferred、P3=A 的 CLI/Web 非正值本地拒绝，并圈定 18 个实现文件与 S1–S7 接缝。
- 2026-08-29：计划自读核对：Task A–E 均列出精确文件集、Consumes/Produces 签名、基线门禁、锁缝入口、结构化日志/注释步骤；计划 §4 列全 MaxConcurrency 的 7 处手写序列化/投影边界，§5 列全真机未验证项。
- 2026-08-29：计划 `wc -l docs/superpowers/plans/b292-plan.md` 原始输出为 `532 docs/superpowers/plans/b292-plan.md`；`rg -n 'TBD|TODO|同 Task|适当错误处理' docs/superpowers/plans/b292-plan.md` 退出码 1、无输出，未命中占位词扫描。
- 2026-08-29：计划初次收口复核显示 baseline 表中的正则转义多写了一层反斜杠；已改为与实际执行命令一致的 `Scheduling|Automation|Queue` 与 `ContractFixtures|Squad|Scheduling`。同次 `git diff --check` 退出码 0、无输出，工作树仅有计划新增与本台账修改。
- 2026-08-29：修正视图名后重跑 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/plans/b292-plan.md --view cards-B292-charter`，退出码 0，原始 JSON 为 `{ "anchors": [] }`；计划使用的是行号/符号文字而非 Markdown 锚链接，未据空 anchors 宣称源码锚点覆盖通过。
- 2026-08-29：在计划 §1.3 增加关键入口 Markdown 锚点后再次运行同一 `resolve` 命令，退出码 0，原始 JSON 仍为 `{ "anchors": [] }`；工具未从该文档提取锚点，计划继续以显式文件路径/符号和已记图覆盖债为准，未冒写锚点通过。
- 2026-08-29：结构扫描 `rg -n '^## |^### |P1=A|P2=A|P3=A|Consumes / Produces|Task [A-E]|S[1-7]|序列化边界|缺陷族|真机清单|未验证|PopReadyFor|--max-concurrency' docs/superpowers/plans/b292-plan.md` 退出码 0；原始输出覆盖 §0–§6、P1/P2/P3、Task A–E、S1–S7、序列化/缺陷族/真机清单及拒绝旧 flag。未把该结构命中当成实现通过。
- 2026-08-29：自审发现 Task B 的目标控制流在非 `ErrNoSlot`、ignition 错误和未知 kind 返回前未显式 flush 已延期请求；已按 P2 回填责任修正文档代码块，使所有提前返回路径先 flush deferred，再保持原停止语义。
- 2026-08-29：最终占位扫描与空白检查命令 `rg -n 'TBD|TODO|同 Task|适当错误处理' docs/superpowers/plans/b292-plan.md; git diff --check; git diff --stat; git status --short --branch` 退出码 0；扫描无输出，diff check 无输出，状态原始输出为 `## cards/B292-charter-3` 加预期的 ledger 修改和 plan 未跟踪文件。
- 2026-08-29：计划 Task B 代码块、Task C–E 代码块与 §4–§6 自审内容读回无额外修改需求；单独 `git diff --check` 退出码 0、无输出。
- 2026-08-29：`git add docs/superpowers/plans/b292-plan.md docs/superpowers/ledgers/2026-08-29-b292-breakdown-ledger.md` 退出码 0；随后 `git diff --cached --check` 退出码 0、无输出；`git diff --cached --name-status` 原始输出仅为 ledger `M` 与法定 plan `A`，统计为 `2 files changed, 581 insertions(+)`。
- 2026-08-29：补记台账后重新暂存；`git diff --cached --check` 退出码 0、无输出，`git diff --cached --name-status` 仍仅列 ledger `M` 与法定 plan `A`，最新统计为 `2 files changed, 582 insertions(+)`。
- 2026-08-29：按计划初次执行 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/plans/b292-plan.md --view cards-B292-charter-3` 失败；原始错误为 `Error: 读取视图 codegraph/diffs/cards-B292-charter-3.json: open codegraph/diffs/cards-B292-charter-3.json: no such file or directory`，并输出 resolve 用法及 `exit status 1`。未据此宣称计划锚点通过；已将文档命令改为仓内已存在的 `cards-B292-charter` 视图。
- 2026-08-29：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . views` 退出码 0，原始 JSON 的可用视图包含 `cards-B292-charter`；因此计划收口使用该实际视图名。
