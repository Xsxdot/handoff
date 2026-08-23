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
