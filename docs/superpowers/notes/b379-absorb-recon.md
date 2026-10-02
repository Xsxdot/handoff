# B379 事实台账：积压视图 diff 对账侦察（2026-10-02，协调者本会话）

现状读数，由执行节点在本轮工作树复核。取证环境：handoff 仓 main@0e5db81b（会话起点），codegraph 二进制 `~/go/bin/codegraph`（charter 仓 graph 模块构建，`go version -m` 确认）。

## 1. 基线身份

- `codegraph/baseline.json` meta：`{"branch":"main","commit":"82e2a2fc…","scannedAt":"2026-09-14","generator":"handoff-executor/b233.26-full-rescan"}`。
- baseline.json 提交史（`git log --follow`）：09-14 两次（B233.26 刀3 全量重扫 + meta 改挂功能线），其后 09-18 吸收 B358.9、09-19 吸收 B370、09-28 吸收 B412——absorb 机制在正常使用，十二份积压是漏网之鱼。

## 2. 洞的端到端复现（/tmp/b379-repro，2026-10-02）

合成基线含 `n_old`（v1.go:10），合成 diff 的 `nodesAdded` 带同名 id、旧快照（v0.go:99）：

1. `codegraph validate` → issues 为空（ValidateDiff 对 nodesAdded 撞基线同 id 不设防）；
2. `codegraph absorb repro-stale` → `已併入视图 repro-stale：+1 节点`；
3. 吸收后基线 `n_old` = `{"file":"v0.go","line":99,…}`——**被分支时态旧快照覆盖**；
4. diff 文件被 absorb 删除——证据一并销毁。

源码定位：`charter 仓 graph/codegraph/validate.go` 的 ValidateDiff 只查容器引用与节点存在性（nodesModified/Deleted 需在基线，nodesAdded 只查其 container 闭合），无同 id 查重；`graph/codegraph/absorb.go:38-40` 对 NodesAdded 无条件 `out.Nodes[id] = n`；`graph/cli/cli.go` 的 absorb 命令闸序 = ValidateDiff → CheckEdges（源码级边校验）→ Absorb → 删 diff。

## 3. 十二份积压视图全量分类

工具：python3 遍历 `codegraph/diffs/*.json`，对照基线与当前 main 工作树。判据与原始计数：

| 视图 | nodesAdded 撞基线（其中锚漂移） | 死文件 | ValidateDiff 硬拒项 | 边未复现 |
|---|---|---|---|---|
| cards-B233.1-charter-2 | 26（7 漂移） | 0 | 无 | 缺边 2（两端均在基线） |
| cards-B233.2-charter | 149（58） | 0 | 无 | 缺边 2 + 缺 implements 2 |
| cards-B233.3-charter | 9（1） | 0 | 无 | 缺 implements 1 |
| cards-B233.4-charter | 43（7） | 0 | 无 | 缺边 15 |
| cards-B233.5-charter | 51（14） | 0 | 无 | 缺边 11 |
| cards-B233.6-charter | 1（5†） | 0 | 无 | 0 |
| cards-B358-charter | 125（16） | 0 | 无 | 0 |
| cards-B272-charter | 7（0） | 0 | 容器 k_dropdir_fn 领域 d_coordination_api 不在基线 | 0 |
| cards-B369-charter | 0 | 0 | 无 | 0 |
| cards-B374-charter | 0（1 漂移，来自 nodesModified） | 0 | 容器 k_collab_model 已在基线（同 label/kind/domain，同挂 d_collab） | 0 |
| cards-B395-charter-5 | 0（41 漂移，来自 nodesModified×47） | 0 | 无 | 0 |
| cards-B398-charter-12 | 0（106 漂移，来自 nodesModified×110） | 0 | 无 | 0 |

† B233.6 计数：1 个 nodesAdded 撞基线，其 nodesModified×8 中 5 条锚漂移。

四组结论：
- **取代组（7）**：B233.1-charter-2、B233.2–6、B358。分支先于 09-14 重扫合并，重扫已捕获全部内容（nodesAdded 全部撞基线即证）；合计 108 条锚漂移，吸收即回退再锚定。约 33 条分支时态边未被重扫复现——重扫是对同一份合并后代码的权威读数，边不存在是重扫的裁定，不是丢失。
- **修剪组（1）**：B374。除容器撞名外其余内容（9 nodesAdded、12 边、1 修改）未入基线且文件在 main 存在，属真待收。
- **改挂组（1）**：B272。7 节点为 dropdir 特性合法新增（internal/dropdir/dropdir.go 等，main 存在，分支 09-23 合并）；新容器领域须改挂。
- **直收组（3）**：B369（09-19 合并）、B395-charter-5、B398-charter-12（09-22/23 合并）。机械干净。

## 4. 领域归属读数（B272 改挂判据）

基线容器领域：`k_agentd_Server`→d_gateway、`k_agentd_fn`→d_gateway、`k_agentd_model`→d_gateway、`c_http`→d_gateway。d_coordination_api 在基线 domains 中不存在（B233.26 归域解散）。B272 的 k_dropdir_fn（dropdir 包级函数组）改挂 **d_gateway**。

## 5. 复核命令

分类可复跑：对照脚本要点 = 遍历 diffs/*.json 的 nodesAdded/nodesModified，按 `(file,line)` 与基线比对计锚漂移、按 `os.path.exists(file)` 计死文件、按 ValidateDiff 判据四查容器/节点引用。边复现核查：edgesAdded/implementsAdded 元组与基线 edges/implements 集合求差。
