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
