# B425 实现计划：ListViews 子目录盲区修复与 B233.28 漏网 diff 直收对账

> 卡 B425，spec `docs/superpowers/specs/b425.md`（2026-10-03 已批准，L2）。
> 执行载体：本地驱动（B379 同型跨仓卡先例，卡上已留回落说明）；本计划无派发系统依赖步骤。
> 事实读数全文：`docs/superpowers/notes/b425-recon.md`（台账，实现阶段续写 §6 工作记录）。

## 0. 执行者须知

- 本卡跨两仓：**charter 仓**（`~/workspace/charter`，codegraph 工具源码）与 **handoff 仓**（本仓，图数据与纪律消费方）。两仓各自建分支、各自提交，合并都归 finish 节点，执行者不自行合并。
- charter 仓根**无 go.mod**，`graph/` 是独立 Go 模块；构建安装命令：`cd ~/workspace/charter/graph && go install ./cmd/codegraph`（产物 `~/go/bin/codegraph`，B379 台账 §6.6 勘误先例）。
- charter 仓主检出线 `codex/charter-story-batches` 不含守卫且**不许动**——实现一律在独立 linked worktree 里做（§3.1）。
- TDD 铁律照 implement 纪律：新用例先红，红→绿→全量；台账边干边落，提交时 amend 收进同批提交。

## 1. 交付目标

本轮一次交付三个已批准故事（spec「全部必交用户故事」）：

- **S1**（charter 仓）：`ListViews` 显式拒绝 `diffs/` 子目录（fail-closed），真仓 `views`/`validate` 变红点名子目录。
- **S2**（handoff 仓）：`absorb cards/B233.28-charter` 直收漏网 diff；`views`/`validate`（完整性半边）/`check` 回绿，`--stale` 计数如实记录（非零退出是常态，不算失败），算术闭合。
- **S3**（charter 仓）：recon/contract 纪律条文补视图文件名扁平化约定；regen 落盘 + 账本 `discipline put` 新版同步。

## 2. 已核事实（spec 节点 2026-10-03 复核，详见台账 §1–§5）

- 盲区唯一点：`ListViews`（charter 仓 master `graph/codegraph/load.go:47`）`os.ReadDir` 非递归、`!e.IsDir()` 跳过目录；消费方仅 `validate`（`cli.go:170`）与 `views`（`cli.go:458`）。`check` 不列 diffs 目录。
- `LoadDiff`（`load.go:33`）`filepath.Join` 拼接——带斜杠 view 名可直接加载；`absorb`（`cli.go:375` 命令层：LoadDiff→ValidateDiff→Absorb→SaveGraph→os.Remove）不受盲区影响。`mergeEdges` 对基线中不存在的待删边静默跳过，不报错。
- 漏网 diff `codegraph/diffs/cards/B233.28-charter.json`（bad735a1，base b5ebef69）：nodesAdded 15 / nodesModified 77 / nodesDeleted 1 / edgesAdded 20 / edgesDeleted 39 / implementsAdded 0 / 无 containersAdded。对现行基线（5370 节点 / 6683 边）：新增零撞车、modified/deleted 全部在基线、待删边 39/39 仍在、待增边零重复零悬空、被删节点牵连边 4 条**且已全部含于 39 条待删边**（plan 节点复核实测，重叠 4/4）、待增边零触碰被删节点。**直收候选，无需外科编辑。**
- 产生侧条文缺口：`skills/recon/SKILL.md` 第 16/27 行、`skills/contract/SKILL.md` 第 25 行写 `codegraph/diffs/<分支>.json` 无扁平化约定。
- 装机二进制 `~/go/bin/codegraph` 当前为 B379 守卫版（vcs.revision=82c6c216）。

## 3. 第一段 S1：ListViews 拒子目录（charter 仓）

### 3.1 建分支（不动主检出线）

```bash
cd ~/workspace/charter && git fetch origin
git worktree add /tmp/b425-charter -b b425-listviews-guard origin/master
```

基点 = origin/master（82c6c216，含 B379 守卫，tag graph/v0.10.2）。

### 3.2 改动边界

- **只动** `graph/codegraph/load.go` 的 `ListViews` 与其测试文件（既有测试文件在哪就在哪补；没有就新建 `graph/codegraph/load_listviews_test.go` 或并入现有 load 测试）。
- 行为：遍历 `diffs/` 顶层条目时，**任何**子目录条目（含空目录）即返回 error；报文含目录名与修复指引（视图文件必须平铺顶层；视图名取分支名时把 `/` 拍平为 `-`）。ListViews 不读子目录内部，报不出文件名——报文口径到目录名为止（与 spec S1 v2 对齐）。顶层平铺 `.json` 行为逐字节不变。`LoadDiff` 不动。
- 不动 absorb/validate/views 的命令层——两消费者经 ListViews 自动继承拒绝行为。

### 3.3 顺序（TDD）

1. **红**：新用例先写——临时仓造 `codegraph/diffs/cards/x.json`，断言 ListViews 返回 error 且报文含 `cards` 与扁平化指引；再断言空子目录同样报错；顶层平铺文件的既有断言保持绿。
2. **绿**：实现子目录检查（改动预期 ≤10 行）。
3. **全量**：`cd ~/workspace/charter/graph && go test ./... -count=1` 全绿。
4. **变异验牙**：摘除子目录检查分支（如 `if false`）→ 新用例转红 → 还原回绿。
5. **提交**：单提交，形如 `feat(B425): ListViews 拒绝 diffs 子目录——视图平铺不变量 fail-closed`。
6. **重建安装**（提交后，使 vcs.revision 钉住守卫提交）：`cd ~/workspace/charter/graph && go install ./cmd/codegraph`；`go version -m ~/go/bin/codegraph` 留台账（revision=本提交、modified=false）。
7. **真机红**：在 handoff 仓 **B425 工作树**（`~/.handoff/worktrees/manual/B425`，后续 absorb 也只在这里做）跑 `~/go/bin/codegraph views` 与 `validate` → 均非零退出、报错点名子目录 `cards`——留原始输出进台账（这是 S1 真机证据，也是 S2 的起点状态；主检出因同一文件在场也会红，views/validate 只读无碍，但 absorb 严禁跑在主检出）。

### 3.4 S1 验收证据

新用例红→绿原文、变异验牙原文、全量 go test 计数、go version -m 读数、真机红两条命令原文——全部落台账 §6。

### 3.5 S1 决策权限

报错文案措辞、测试用例的组织方式可自行定；**改动边界不可越**（只动 load.go ListViews + 测试）；若发现既有测试因此行为需要调整，改动须逐条与「顶层平铺行为不变」判据对照并在台账记理由。

## 4. 第二段 S2：直收对账（handoff 仓，分支 cards/B425-charter-1）

### 4.1 前置检查（不满足就停）

- S1 新二进制已安装（`go version -m` 指向 S1 提交）；
- handoff 仓 `cards/B425-charter-1` 工作树干净（在 `~/.handoff/worktrees/manual/B425` 里操作）；
- `~/go/bin/codegraph views` 在本仓报错点名子目录 `cards`（S1 真机红态在场）。

### 4.2 步骤

1. `~/go/bin/codegraph absorb cards/B233.28-charter` → 断言退出 0，报文含 `cards/B233.28-charter` 与节点增量（+15）；diff 文件被工具删除。
2. `rmdir codegraph/diffs/cards`（此时必为空；非空即停、升级协调者）。
3. 四命令读数（handoff 仓根，原始输出落台账）：
   - `views` → 视图数组为空、退出 0（输出为 pretty-print 多行 JSON，做语义断言，不逐字节比对）；
   - `validate` → 完整性零 issue 退出 0；
   - `validate --stale` → 计数如实记录（预读数 1165，可能上升——分支时态锚覆盖，B379 裁决 1 读法，不做数据干预；非零退出是常态，不算失败）；
   - `check` → 退出 0 且 `fails: []`。
4. 算术与抽验（python3，逐项落台账）：节点 5370+15−1=5384；边 6683−39+20=**6664**（牵连边 4/4 已含于待删集，不重复扣减——plan 节点复核勘误；若与实测不符，逐项归因后升级，不许硬凑）；抽验 `n_orchestration_Manager_ListTasks` 等至少 3 个新增门面节点 ∈ 基线且容器/域闭合（闭合性同时由 validate 完整性零 issue 全局覆盖）、`n_agentd_Server_resumeIfIdle` ∉ 基线；记录 baseline meta.branch/commit 戳（absorb 时点 HEAD，不手工改）。
5. 提交：baseline.json 变更 + diff 文件删除 + 台账续写，单提交。

### 4.3 S2 验收证据

absorb 报文原文、rmdir 前后目录状态、四命令原始输出、算术闭合与抽验结果——落台账 §6。

### 4.4 S2 决策权限

四命令读数与算术全部符合预期时按步提交；absorb 非零退出、算术不符、rmdir 非空——任一出现即停，原文留台账，升级协调者。**任何情况下不许手改 baseline.json。**

## 5. 第三段 S3：纪律条文扁平化勘误（charter 仓，同 S1 分支）

1. `skills/recon/SKILL.md`（第 16/27 行）与 `skills/contract/SKILL.md`（第 25 行）：视图文件名条文补一句——视图名/文件名取分支名时必须扁平化（`/`→`-`，例：`cards/B233.28-charter` → `cards-B233.28-charter`），理由一行（ListViews 只认顶层平铺，子目录即报错，B425）。**入库交付物仅此两文件**，单提交。
2. **regen 落盘**：先读 `scripts/regen_discipline.py` 确认行为再运行——它把 skills/ 正文生成纪律块写到 `~/.handoff/discipline/charter-story-*.md`（活体目录，**不入任何仓**，无同步产物可提交）。运行输出（块名+字节数）落台账。
3. **账本权威副本同步**（B229 起权威在 handoff 账本；此处唯一例外于 §6.3 的「不动账本」）：`handoff discipline put` 更新 `charter-recon` 与 `charter-contract` 两块的新版本（内容 = 对应 skills/ 正文去 frontmatter；脚本 compose_map 中两块均为单体构成）。put 前先 `handoff discipline get <块名>` 留旧版读数；put 后 `handoff discipline list` 确认版本递增。附注：现存 `charter-recon` v1 落后 skills 正文（1206 vs 1174 字节，存量滞后）——本步顺带治愈，属预期收益，输出落台账。story 试点族块只随 regen 落盘、不入账（脚本自述其安装另走 charter_provision，本卡不碰）。

## 6. 全局禁止事项

1. 不手改 `baseline.json`（absorb 是唯一合法入口）；不动 `target.json`/`best.json`。
2. 不动 charter 仓主检出线 `codex/charter-story-batches`（一切实现走 `/tmp/b425-charter` 工作树）。
3. 不动 agentd、卡账本与 handoff 业务代码——本卡 handoff 侧只动 baseline.json、diffs 子目录、docs；**唯一例外**：S3 的纪律块账本同步走 `handoff discipline put`（§5.3，纪律块版本化不可变，只增不改）。
4. 不自行合并任何分支（两仓合并归 finish，由协调者提请用户授权）。
5. 台账只追加不改写；amend 收口一次，不 chase hash。

## 7. 验收对账表

| 故事 | 判据（钉行为） | 证据落点 |
|---|---|---|
| S1 | 子目录在场 → ListViews 报错；摘除检查 → 测试红；顶层平铺行为不变 | 台账 §6：测试原文/变异/全量/真机红 |
| S2 | absorb 退出 0；四命令读数符合 §4.2（`--stale` 如实记录）；算术闭合逐项归因；抽验命中 | 台账 §6：原始输出 + python 读数 |
| S3 | 两处条文含扁平化约定；regen 落盘输出在案；账本两块版本递增且正文含新条文 | 台账 §6：get/list 原文 |

## 8. 完成定义

三段各自单提交于 `b425-listviews-guard`（charter）与 `cards/B425-charter-1`（handoff），S1/S2/S3 证据落台账 §6，新二进制已安装（revision=S1 提交），两工作树干净。此后交协调者 review（独立审计两轴）/acceptance；两仓合并与 tag（graph/v0.10.3）+ go.mod bump 归 finish，执行者不自行合并。
