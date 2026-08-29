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
