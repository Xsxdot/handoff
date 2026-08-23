# 2026-08-23 B223 codegraph 全量重扫台账

## 范围与基线

- 规格：`docs/codegraph-scan-recipe.md` 已完整读取；扫描产物目标为 `codegraph/baseline.json`，不改 `codegraph/target.json`、`codegraph/domains/*.json`、源码或配方。
- 技能说明：已完整读取 `/root/.codex/skills/handoff/SKILL.md`；本回合不调用 handoff CLI、不派发、不启动 executor。
- 起始提交：`cc20130b1`（配方修订）；当前分支：`cards/B223-implement`。
- 起始工作树命令 `git status --short --branch` 原始输出：`## cards/B223-implement`。
- 起始基线统计命令原始输出：`domains 19 containers 237 nodes 3564 edges 4522 implements 7 projections 378 lifecycle 127`。
- 起始节点统计命令原始输出：`node kinds Counter({'func': 2739, 'model': 707, 'entry': 118})`；`modelKinds Counter({None: 707})`。

## 执行记录

（按 task、命令、原始结果、双裁决和提交范围即时追加。）

## T1：d_protocol 协议域

- 判断：盘点 `internal/proto/**` 后得到 4 个容器、114 个节点，其中 111 个 model；每个容器的全部成员均为协议包符号，因此按“整容器迁移、不做部分拆分”改挂 `d_protocol`。
- 改动：新增顶层 `d_protocol`，并将 `k_proto_Task`、`k_proto_TaskState`、`k_proto_fn`、`k_proto_model` 的 domain 全部改为 `d_protocol`；未改任何 proto 节点、边或其他业务容器。
- 命令 `python3 -m json.tool codegraph/baseline.json >/dev/null && python3 ...` 原始输出：`proto_nodes 114 proto_models 111`；`proto_container_domains {'k_proto_Task': 'd_protocol', 'k_proto_TaskState': 'd_protocol', 'k_proto_fn': 'd_protocol', 'k_proto_model': 'd_protocol'}`；`domains 20 d_protocol {...}`。
- 命令 `go run . graph validate --repo .` 原始输出：`containers 237`、`domains 20`、`edges 4522`、`nodes 3564`、`issues: null`、`edgeIssues: null`；完整输出已由命令亲自得到，另有 relay/proxy INFO 日志。
- 双裁决第 1 轮：规格符合——协议容器完整迁移，协议域为顶层叶子域，validate 零 issues；代码质量通过——变更局限于 baseline 的领域声明和 4 个容器归属，JSON 合法，无业务字段或源码变更。无需修复。
- 提交前命令 `git diff --check && git diff --stat && git status --short` 原始输出：`codegraph/baseline.json | 14 ++++++++++----`、`1 file changed, 10 insertions(+), 4 deletions(-)`、`M codegraph/baseline.json`、`?? docs/ledgers/2026-08-23-codegraph-b223-ledger.md`。
- 提交尝试命令 `git add codegraph/baseline.json docs/ledgers/2026-08-23-codegraph-b223-ledger.md && git commit -m 'data(codegraph): 全量重扫——新增协议域归属'` 失败原文：`fatal: Unable to create '/root/.handoff/repos/handoff/.git/worktrees/ff8a9ea0/index.lock': Read-only file system`。
- T1 首次提交命令在获准写入 Git 元数据后成功，原始输出：`[cards/B223-implement e489f2b3] data(codegraph): 全量重扫——新增协议域归属`、`2 files changed, 34 insertions(+), 4 deletions(-)`、`create mode 100644 docs/ledgers/2026-08-23-codegraph-b223-ledger.md`；因需把本台账行纳入提交而 amend，最终提交命令原始输出：`[cards/B223-implement b4090481] data(codegraph): 全量重扫——新增协议域归属`、`2 files changed, 35 insertions(+), 4 deletions(-)`、`create mode 100644 docs/ledgers/2026-08-23-codegraph-b223-ledger.md`。最终提交范围：`cc20130b1..b4090481`。
- 台账自引用修正事实：将提交哈希写入同一提交后再次 amend 会改变提交哈希；随后一次 amend 原始输出为 `[cards/B223-implement fa62b253] data(codegraph): 全量重扫——新增协议域归属`。T1 的最终提交哈希将在下一次提交台账时以 `git log` 实测值记录，避免自引用失真。

## T2：modelKind

- 命令 `python3 ...` 首次统计失败原文：`TypeError: unsupported operand type(s) for -: 'dict' and 'set'`；该失败未用于判定结果。
- 修正统计命令原始输出：`models 707 lifecycle_model_ids 53 lifecycle_entries 127`；`entity∩proto 8 [...]`；`entity∩web_api 0 []`；`proto 111 web_api 119 remaining 432`；`classification_counts {'entity': 53, 'dto': 222, 'config_or_fallback': 432}`。
- 判定：生命周期引用的 53 个 model 标 `entity`（优先级 1）；其余 proto 111 个和 `web/src/api/**` 的 119 个中未被生命周期命中的标 `dto`（优先级 2）；能直接证明从配置/env 载入的 `internal/config/**` 11 个、`internal/envfile/envfile.go:KV` 1 个和 `internal/pathenv/pathenv.go:Options` 1 个标 `config`；其他 419 个未命中前述证据，按配方兜底标 `dto`。
- 配置证据命令 `rg -n ...` 原始结果确认 `internal/config/config.go` 注释写明“读取 ~/.handoff/config.yaml（或指定路径）并解析为 Config”，`Load` 使用 YAML strict decoder；`internal/envfile/envfile.go` 注释写明解析 dotenv 为 KV，生产使用 `os.LookupEnv`；`internal/pathenv/pathenv.go` 注释写明 `Options.ExtraDirs` 是 `config.path_dirs`。其余模型没有在本轮取得“从配置文件或 env 装载”的直接证据，按 dto 兜底。
- T2 变更方法：为 707 个 `kind: model` 节点逐一添加枚举值；未向非 model 节点添加字段，未改 lifecycle、节点定义、容器或边。
- 命令 `python3 -m json.tool ... && python3 ...` 原始输出：`modelKind_counts Counter({'dto': 641, 'entity': 53, 'config': 13})`、`missing_modelKind 0`、`non_model_with_modelKind 0`、`dto_with_lifecycle 0`、`entity_without_lifecycle 0`。
- 命令 `go run . graph validate --repo .` 原始输出：`containers 237`、`domains 20`、`edges 4522`、`nodes 3564`、`issues: null`、`edgeIssues: null`；另有 relay/proxy INFO 日志。
- 双裁决第 1 轮：规格符合——四级优先级逐节点落地，生命周期 53 个均为 entity、无 dto 生命周期矛盾，707 个 model 全覆盖；代码质量通过——只新增 canonical `modelKind`，JSON/validate 均通过，无非 model 污染。无需修复。
- 双裁决第 1 轮后发现的 minor：首批自动 patch 使 model 节点 key 与 `kind` 行缩进各减少一级；`git diff --numstat` 原始输出为 `2121 1414 codegraph/baseline.json`。该项进入修复回路（影响 diff 可读性）。
- 修复：按 707 个 model 节点逐一恢复原有缩进；修复后命令 `git diff --check && git diff --numstat && python3 -m json.tool ... && python3 ...` 原始输出：`707 0 codegraph/baseline.json`、`12 0 docs/ledgers/2026-08-23-codegraph-b223-ledger.md`、`modelKind_counts Counter({'dto': 641, 'entity': 53, 'config': 13})`、`missing_modelKind 0 non_model_with_modelKind 0`。
- 修复后命令 `go run . graph validate --repo .` 原始输出：`containers 237`、`domains 20`、`edges 4522`、`nodes 3564`、`issues: null`、`edgeIssues: null`；另有 relay/proxy INFO 日志。
- 双裁决第 2 轮：规格符合——T2 全部要求仍满足；代码质量通过——diff 仅为 707 行新增 `modelKind`，无格式漂移，validate 零 issues。无需继续修复。
- T2 提交前命令 `git diff --check && git diff --stat && git status --short` 原始输出：`codegraph/baseline.json | 707 +++++++++++++++++++++++`、`docs/ledgers/2026-08-23-codegraph-b223-ledger.md | 16 +`、`2 files changed, 723 insertions(+)`、`M codegraph/baseline.json`、`M docs/ledgers/2026-08-23-codegraph-b223-ledger.md`。
- T2 提交命令在获准写入 Git 元数据后成功，原始输出：`[cards/B223-implement a4ed153d] data(codegraph): 全量重扫——补齐 modelKind`、`2 files changed, 724 insertions(+)`。提交范围：`fa62b253..a4ed153d`（T1 哈希以当前 `git log` 为准，T1 自引用历史记录保留）。

## T3：文件级完整性与漏建入口

- 盘→图自检命令按 `target.json` 的 10 个 subsystem path 加 `main.go` assembly，源码范围取 `.go/.ts/.tsx`，排除测试文件（Go `*_test.go`、TS `*.test.*`）、`internal/**/testdata/**`、`web/src/test/setup.ts` 和声明文件；理由是当前图把测试作为 `tests` 引用而不是源码节点，且配方的 cmd 统计已明确以 50 个非测试源码文件为盘点口径。
- 自检原始输出：`prod target 472 graph 449 zero 23`；`cmd disk 50 cmd graph 41`；cmd 差集为 `card_driver.go card_import.go card_minb.go card_prefix.go card_records.go decision.go graph.go template.go workflow.go`。
- 23 个零节点生产文件完整清单：`cmd/card_driver.go`、`cmd/card_import.go`、`cmd/card_minb.go`、`cmd/card_prefix.go`、`cmd/card_records.go`、`cmd/decision.go`、`cmd/graph.go`、`cmd/template.go`、`cmd/workflow.go`、`internal/agentd/opennonblock_other.go`、`internal/agentd/opennonblock_unix.go`、`internal/discipline/platform.go`、`internal/executor/codex/taskenv.go`、`internal/ledger/card_prefixes.go`、`internal/ledgerstep/output.go`、`internal/ptyhost/errors.go`、`internal/ptyhost/supported_other.go`、`internal/ptyhost/supported_unix.go`、`internal/webui/webui.go`、`main.go`、`web/src/app/machines/bindingRow.ts`、`web/src/components/ui/card.tsx`、`web/src/main.tsx`。
- 处置判断：9 个 cmd 文件及 `card.go`/`card_dispatch.go`/`card_wait.go` 中漏建的 Cobra entry 全部补 entry；`decision/template/workflow/graph` 补命令入口；`discipline/platform.go`、`ledger/card_prefixes.go`、`ledgerstep/output.go` 补真实函数节点；`main.go` 与 `web/src/main.tsx` 补入口节点；`web/src/components/ui/card.tsx` 补 6 个真实组件函数节点。其余 8 个文件只含纯常量、静态策略/错误哨兵、包文档或 UI 类名常量：`opennonblock_{other,unix}.go`、`codex/taskenv.go`、`ptyhost/errors.go`、`ptyhost/supported_{other,unix}.go`、`webui.go`、`bindingRow.ts`，按配方允许的零节点解释路径保留并在交付报告逐个说明。
- T3 变更后自检原始输出：`prod target 472 graph 464 zero 8 outside 0`；8 个零节点文件为 `internal/agentd/opennonblock_other.go`、`internal/agentd/opennonblock_unix.go`、`internal/executor/codex/taskenv.go`、`internal/ptyhost/errors.go`、`internal/ptyhost/supported_other.go`、`internal/ptyhost/supported_unix.go`、`internal/webui/webui.go`、`web/src/app/machines/bindingRow.ts`；`cmd_files 50 50`、`cmd_zero []`、`card_entries 24`；新增容器为 `c_main`（domain `d_coordination_cli`）和 `c_web_main`（domain `d_web`）。
- T3 变更后命令 `go run . graph validate --repo .` 原始输出：`containers 239`、`domains 20`、`edges 4522`、`nodes 3619`、`issues: null`、`edgeIssues: null`、`unscannedEntries: 7`；另有 relay/proxy INFO 日志。
- 双裁决第 1 轮：规格符合——9 个 cmd 文件与 24 个 card entry 全部入图，除明确解释的 8 个静态/文档文件外盘图差集清零，新增节点引用已定义容器且 validate 零 issues；代码质量通过——只补真实入口/函数/组件和必要入口容器，没有凭空补边，JSON 合法。无需修复。
- T3 提交前命令 `git diff --check && git diff --stat && git status --short` 原始输出：`codegraph/baseline.json | 507 +++++++++++++++++++++++`、`docs/ledgers/2026-08-23-codegraph-b223-ledger.md | 11 +`、`2 files changed, 518 insertions(+)`、`M codegraph/baseline.json`、`M docs/ledgers/2026-08-23-codegraph-b223-ledger.md`。
- T3 提交命令在获准写入 Git 元数据后成功，原始输出：`[cards/B223-implement f149c818] data(codegraph): 全量重扫——补齐漏建文件与入口`、`2 files changed, 519 insertions(+)`。提交范围：`a4ed153d..f149c818`。

## T4：验收与终审

- 相对起点统计命令 `git show cc20130b1:codegraph/baseline.json | python3 ...` 原始输出：`domains 19 -> 20 delta 1`、`containers 237 -> 239 delta 2`、`nodes 3564 -> 3619 delta 55`、`edges 4522 -> 4522 delta 0`、`implements 7 -> 7 delta 0`、`projections 378 -> 378 delta 0`、`lifecycle 127 -> 127 delta 0`；node kinds 为 `func 2739 -> 2754`、`model 707 -> 707`、`entry 118 -> 158`。
- `go run . graph domains --repo .` 已实跑：`d_protocol` 显示 `models: 111`、`subsystems: ["d_contract"]`；`d_coordination_task` 显示 `models: 83`；`d_web` 显示 `c_web_main` 及 `entries: 1`。
- `go run . graph validate --repo .` 已实跑：`containers 239`、`domains 20`、`edges 4522`、`nodes 3619`、`issues: null`、`edgeIssues: null`、`unscannedEntries: 7`。该版本输出没有 `entitiesWithoutLifecycle` 字段；独立图统计已实跑为 `entity count 53`、`entity_without_lifecycle 0`。
- `go run . graph check --repo .` 已实跑：`fails: []`；报告包含 19 条 `legacy` warn 与 1 条 `outside-file` warn（`图外文件（目标图未覆盖）: main.go`）。再次过滤 `anchor-off|outside-file|legacy` 的实跑输出只有 19 个 `legacy`、1 个 `outside-file`，没有 `anchor-off-*`；当前依赖版本源码也没有该 warning 类型（`rg` 只找到 `legacy/outside-file/dead-rule` 注释）。因此判据 8 的 `anchor-off-* = 14` 在当前工具版本中未验证，不能伪报达标；`outside-file main.go` 因配方要求盘点 assembly、而 check 仅按 target subsystem paths 判定，予以报告，不改 target。
- 五点 `file:line` 抽查实跑：`m_proto_Task: graph=internal/proto/proto.go:220 source='type Task struct {'`；`e_cli_card_add: graph=cmd/card.go:29 source='var cardAddCmd = &cobra.Command{'`；`n_ledger_Store_cardPrefixTx: graph=internal/ledger/card_prefixes.go:22 source='func (s *Store) cardPrefixTx(tx *sql.Tx, project string) (string, error) {'`；`n_web_components_ui_Card: graph=web/src/components/ui/card.tsx:5 source='const Card = React.forwardRef<'`；`e_web_main: graph=web/src/main.tsx:6 source="createRoot(document.getElementById('root')!).render("`。
- 终审发现：baseline `meta` 仍为上一轮 `branch=main`、`commit=8c37058c17417c42283a9f0076e262a6e9c13bef`、`scannedAt=2026-08-22`，与本轮现状扫描不符；决定在 T4 唯一修复波更新为当前分支、T3 完成提交 `f149c818` 和 `2026-08-23`，保留 generator 语义但改为本轮标识。
- T4 修复：仅更新 baseline `meta.branch`、`meta.commit`、`meta.scannedAt`、`meta.generator`；未改 target/domains/源码。
- 修复后全量验收原始输出：`validate` 为 `containers 239`、`domains 20`、`edges 4522`、`nodes 3619`、`issues: null`、`edgeIssues: null`、`unscannedEntries: 7`；`check` 为 `fails 0`、`warn_count 20`、19 个 `legacy` + 1 个 `outside-file`、`anchor_off []`；`domains` 摘要为 `[('d_coordination_task', 83, ['d_controlplane', 'd_ledger', 'd_localint'], 0), ('d_protocol', 111, ['d_contract'], 0), ('d_web', 254, ['d_web'], 1)]`。
- 修复后统计原始输出：`meta {'project': 'handoff', 'branch': 'cards/B223-implement', 'commit': 'f149c818', 'scannedAt': '2026-08-23', 'generator': 'codex-codegraph-b223-rescan'}`；`counts {'domains': 20, 'containers': 239, 'nodes': 3619, 'edges': 4522, 'implements': 7, 'projections': 378, 'lifecycle': 127}`；`d_protocol_models 111 d_coordination_task_models 83 entity 53`；`files 472 464 8 cmd 50 50`；`modelKinds Counter({'dto': 641, 'entity': 53, 'config': 13})`。
- 相对起点终审原始输出：仅 `domains` 新增 1、`containers` 新增 2/变更 4、`nodes` 新增 55/变更 707（仅新增 modelKind）、edges/implements/projections/lifecycle 均相等；无删除节点、容器或领域。T4 双裁决第 1 轮：规格符合——判据 1–7、9–10 实测满足，判据 8 因当前工具无 `anchor-off-*` 如实标未验证；代码质量通过——最终 diff 只含本次 meta 修正与台账，未触碰禁止文件或源码。无需第二轮修复。
- T4 提交前命令 `git diff --check && git diff --stat && git status --short && git diff -- codegraph/baseline.json` 原始输出：`codegraph/baseline.json | 8 ++++----`、`docs/ledgers/2026-08-23-codegraph-b223-ledger.md | 14 ++++++++++++++`、`2 files changed, 18 insertions(+), 4 deletions(-)`、工作树仅 `M codegraph/baseline.json` 与 `M docs/ledgers/2026-08-23-codegraph-b223-ledger.md`；baseline diff 仅四个 meta 字段从上一轮值更新为本轮值。
- T4 提交命令在获准写入 Git 元数据后成功，原始输出：`[cards/B223-implement 787f1de4] data(codegraph): 全量重扫——验收并更新扫描元数据`、`2 files changed, 19 insertions(+), 4 deletions(-)`。提交范围：`f149c818..787f1de4`。
- 收口判断：T1–T4 的实现提交均已完成；当前仅需提交本台账新增的 T4 提交记录，之后不再修改扫描产物。
- 收口提交前命令 `git diff --check && git diff --stat && git status --short --branch` 原始输出：`docs/ledgers/2026-08-23-codegraph-b223-ledger.md | 2 ++`、`1 file changed, 2 insertions(+)`、`## cards/B223-implement`、`M docs/ledgers/2026-08-23-codegraph-b223-ledger.md`。
