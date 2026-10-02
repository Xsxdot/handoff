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

## 6. plan 节点复核与勘误（2026-10-02，plan 执行者会话）

复核环境：handoff 仓 cards/B379-charter-1@10be7c17（= 0e5db81b + 本卡 spec/台账提交；`git diff 0e5db81b..HEAD -- codegraph/baseline.json` 为空，基线与 §1–5 侦察时逐字一致）。以下事实全部本会话亲手重验。

### 6.1 B272 计数勘误（无碍处置）

§3 表该行「7（0）」按列头（nodesAdded 撞基线）会误读成 7 条撞基线。实测：B272 nodesAdded 共 7 条，撞基线 **0** 条——与 §3 四组结论「7 节点为 dropdir 特性合法新增」一致。7 个 id：`e_http_post_api_drop`、`n_agentd_Server_handleDropPut`、`n_agentd_Server_forwardDropIfRequested`、`n_dropdir_Dir`、`n_dropdir_Put`、`m_proto_DropPutResp`、`n_web_api_client_uploadDropFile`。结论：改挂组只需改容器领域，无需修剪任何节点，与新守卫不冲突。

### 6.2 B358 计数勘误（承重，处置不变）

§3 表「125（16）」与 spec「全部 nodesAdded 已存在于基线、其中 108 条锚已漂移」对 B358 不成立。实测（按 id 对基线 nodes）：B358 nodesAdded 125 条，撞基线 **42** 条（其中锚漂移 7），其余 83 条不在基线。七份合计 nodesAdded 撞基线 321、nodesAdded 锚漂移 86（§3 各行括号数似混入 nodesModified 漂移，B233.6 脚注即明说如此）。

时间线取证（git log）：B358.4 implement 09-13 11:10（4162505f，sessionsapi.go 建档）→ B233.26 全量重扫 09-14 10:45（92995010）→ **B358.9「会话身份本质重做」合入 09-18 16:00（1f46cc1e，T3 CLI 收敛等）→ cards-B358.9-charter-4 吸收 09-18 16:04（4e1e40c3）**。即 cards-B358-charter 是重做前旧快照：其未撞余量一属重做前旧面（如 `n_cmd_sessionWaitCmd_RunE`，基线经 B358.9 吸收录得的是重做后新面 `n_cmd_sessionsCmd_RunE` 等），二属 main 在册但基线未录的内容（见 6.3）。

**删除处置维持，依据改写为三条**：① 七份视图每份都有撞基线 nodesAdded，新守卫落地后原样吸收一概被拒；② 重扫（09-14）是合并后代码的权威读数，旧视图吸收=回退（§3 原判据，成立）；③ B358 余量吸收会把重做前节点注入重做后基线，恰是本卡要防的腐化本体。spec 引述的「全部撞基线即证」作废，以本节为准。

### 6.3 基线覆盖债线索（不阻塞本卡，另卡候选）

main 工作树在册但基线 0 节点的文件（B358 余量所在）：internal/agentd/sessionsapi.go（`validSessionOwner` 真身在 :35，grep 实证）、web/src/app/rooms/sessionModel.ts 等。修复途径是将来对 main 的新鲜扫描，与 spec Out of Scope「约 33 条未复现边」同族（扫描配方保真度/覆盖），不属本卡。

### 6.4 其余计数复核（与 §3 一致）

- 七份「边未复现」合计 33：B233.1 缺边 2；B233.2 缺边 2 + 缺 implements 2；B233.3 缺 implements 1；B233.4 缺边 15；B233.5 缺边 11；B233.6、B358 均 0（B358 无任何 edgesAdded/implementsAdded 条目）。
- B374：containersAdded 仅 k_collab_model，与基线容器同 label「collab 实体」/kind「实体」/domain d_collab；nodesAdded 9 条撞 0；nodesModified 中 1 条锚漂移（n_agentd_Server_handleRoomsList）。
- 直收组：B369 nodesAdded 29 撞 0；B395-charter-5 nodesAdded 5 撞 0；B398-charter-12 nodesAdded 0。三份均无 ValidateDiff 硬拒项。
- B272 容器：containersAdded 仅 k_dropdir_fn，domain=d_coordination_api；基线 domains 无 d_coordination_api、有 d_gateway（§4 判据数据复验成立）。

### 6.5 charter 仓主线勘误

charter 仓**无本地 main**；origin/HEAD → **origin/master**（远端 git@github.com:Xsxdot/charter.git）。当前检出分支 codex/charter-story-batches（工作树干净），origin/master..HEAD 的 graph/ 差异仅 cli_test.go 一个文件——validate.go / absorb.go / cli.go 与 master 逐字一致，守卫落点事实对 master 成立。工作分支应 `git fetch origin && git switch -c b379-absorb-guard origin/master`。

### 6.6 构建命令勘误

charter 仓根**无 go.mod**，graph/ 是独立 Go 模块（module github.com/Xsxdot/charter/graph，go 1.26.1，本会话 `go build ./...` 通过；仓内仅此一个 Go 模块）。spec 所写 `go install ./graph/cmd/codegraph` 在仓根不可运行；正确命令：`cd ~/workspace/charter/graph && go install ./cmd/codegraph`（产物 ~/go/bin/codegraph）。意图不变：重建并安装使守卫生效。

### 6.7 工具面事实（plan 引用）

- graphAbsorbCmd 闸序 = LoadGraph → LoadDiff → ValidateDiff → CheckEdges（**仅 edgesAdded**，implements 边不经源码校验）→ Absorb → SaveGraph → 删 diff；ValidateDiff/CheckEdges 拒绝发生在删 diff 之前，被拒视图的 diff 文件保留。
- CheckEdges（edgegate.go）：`CheckEdges(repoRoot string, nodes map[string]Node, edges []Edge) []EdgeIssue`。
- Diff.summary 是纯字符串展示字段（types.go），修剪/改挂编辑不触及；Diff.base 记录视图基底提交。
- absorb 运行目录 = 仓根（graphRepo="."），view 参数 = diffs/ 文件名去 .json；吸收后 meta.commit/branch 取当前 HEAD（工具默认行为，不干预）。

### 6.8 S3 取证对象修正

spec S3「约 33 条未复现边」全部来自 B233.1–.5 五份（B233.6/B358 零边）。抽样在删除前做；若时序错过，可从本分支 git 历史读被删 diff。

### 6.9 复核命令要点

python3 遍历 diffs/*.json：撞基线按 id ∈ baseline.nodes；锚漂移按 (file,line) 二元组不等；边未复现按 edgesAdded/implementsAdded 元组 ∉ baseline.edges/implements 集合。charter 对照：`git -C ~/workspace/charter diff origin/master..HEAD --stat -- graph/`；构建：`cd ~/workspace/charter/graph && go build ./...`。

### 6.10 后续工作记录

implement 起的提交、命令与原始输出、吸收报文、S3 取证原文一律追加在「## 7. 工作记录」段（本节之后新起，不回写 §1–6 勘误定稿）。

## 7. 工作记录

> implement 执行者会话（2026-10-02）。逐事实当步追加。

### 7.1 第一段环境与分支（charter 仓）

- 环境核实：handoff 仓 `cards/B379-charter-1`@89fafbfc 工作树干净（仅他人未跟踪 `.zcodeignore`，不动）；charter 仓检出 `codex/charter-story-batches` 工作树干净；`~/go/bin/codegraph` 为 2026-08-24 旧构建（vcs.revision=b5c59799，无守卫）。
- 建分支：`cd ~/workspace/charter && git fetch origin && git switch -c b379-absorb-guard origin/master` → `Switched to a new branch 'b379-absorb-guard'`，基点 origin/master@01720d4d。

### 7.2 S1 TDD 红（新用例先红）

`validate_test.go` 新增 `TestValidateDiffRejectsAddedNodeConflict`（撞基线 id `n_do` 断言报文含 id 与「只接受新节点」）与 `TestValidateDiffAllowsGenuinelyNewNode`（全新 id 反向断言）。跑 `cd ~/workspace/charter/graph && go test ./codegraph/ -run 'TestValidateDiffRejectsAddedNodeConflict|TestValidateDiffAllowsGenuinelyNewNode' -v`：

```
=== RUN   TestValidateDiffRejectsAddedNodeConflict
    validate_test.go:230: nodesAdded 撞基线同 id 应报 n_do 与「只接受新节点」语义: []
--- FAIL: TestValidateDiffRejectsAddedNodeConflict (0.00s)
=== RUN   TestValidateDiffAllowsGenuinelyNewNode
--- PASS: TestValidateDiffAllowsGenuinelyNewNode (0.00s)
FAIL
```

红因为「功能缺失」（issues 为空），非 typo。

### 7.3 S1 TDD 绿

`ValidateDiff` 的 nodesAdded 循环内加判据（报文 `新增节点 %s 已存在于基线，nodesAdded 只接受新节点`）。`go test ./...`：

```
ok  	github.com/Xsxdot/charter/graph/cli	1.396s
?  	github.com/Xsxdot/charter/graph/cmd/codegraph	[no test files]
ok  	github.com/Xsxdot/charter/graph/codegraph	0.881s
ok  	github.com/Xsxdot/charter/graph/webui	1.258s
```

### 7.4 S1 分层边界用例与变异验牙

`absorb_test.go` 新增 `TestAbsorbDoesNotRejectDuplicateNodes`：断言 Absorb 对撞 id 的 nodesAdded 照常按 diff 覆盖（无 error 返回、无拒绝动作）——钉住「守卫单点在 ValidateDiff、Absorb 保持纯函数」的 spec 决策。变异验牙：临时把 `absorb.go` 的 NodesAdded 併入循环改为「撞 id 跳过」（模拟守卫被搬进 Absorb），该用例红：

```
--- FAIL: TestAbsorbDoesNotRejectDuplicateNodes (0.00s)
    absorb_test.go:78: Absorb 不做查重，撞 id 应按 diff 覆盖（守卫单点在 ValidateDiff）: {Kind:func … File:svc/server.go Line:4 …}
```

随后还原变异，`git diff --stat` 确认改动面恰为三个准动文件（validate.go +6、validate_test.go +28、absorb_test.go +16），全量复跑绿。

### 7.5 S1 提交与二进制重建

- charter 仓提交：`82c6c216` `feat(B379): ValidateDiff 拒绝 nodesAdded 撞基线同 id——吸收防线节点版（契约 §7-R1 容器版对齐）`（分支 `b379-absorb-guard`，3 files changed, 50 insertions）。
- 重建安装（提交后执行，使 vcs.revision 直接钉住守卫提交——比计划原序「先装后提交」的验证更强，判定为满足计划「确认构建时间晚于本次提交」的等价更强形式）：`cd ~/workspace/charter/graph && go install ./cmd/codegraph`，`go version -m ~/go/bin/codegraph`：

```
path	github.com/Xsxdot/charter/graph/cmd/codegraph
mod	github.com/Xsxdot/charter/graph	(devel)
build	vcs=git
build	vcs.revision=82c6c21602b29150d24c4274debabe497eff2ea3
build	vcs.time=2026-10-02T13:17:53Z
build	vcs.modified=false
```

### 7.6 S1 合成仓端到端（/tmp/b379-s1-e2e）

场景按台账 §2 重建：基线含 `n_old`（k_svc，v1.go:10），diff `repro-stale` 的 nodesAdded 带同名 id（v0.go:99）；合成仓为独立 git 仓（main@e83024cf，实验后 HEAD）。

**validate（新二进制）**：退出 1，issues 含：

```
[repro-stale] 新增节点 n_old 已存在于基线，nodesAdded 只接受新节点
Error: 发现 1 个完整性问题、0 个失鲜节点
```

**拒绝例**：`shasum -a 256 codegraph/baseline.json` → `06c5b72916af9d78e9e0684c428afd4b49cb97c6ace5caa6343b5a00a95baad7`；`~/go/bin/codegraph absorb repro-stale` → 退出 1：

```
Error: 视图 repro-stale 引用不完整，拒绝併入: [新增节点 n_old 已存在于基线，nodesAdded 只接受新节点]
```

复核：sha256 前后相同（`BASELINE_UNCHANGED=yes`）、`codegraph/diffs/` 下 `repro-stale.json` 仍在。

**照常例**：另造 `repro-clean`（全新 id `n_new`，k_svc，v2.go:5）→ `~/go/bin/codegraph absorb repro-clean` → 退出 0：

```
已併入视图 repro-clean：+1 节点 ~0 -0，基线 2 节点 @e83024cf4076e71672fcd9d507ab22c264daa4ff
```

复核：`repro-clean.json` 被工具删除（diffs/ 只剩 repro-stale.json）、基线 `n_new` = `{"file":"v2.go","line":5,…}`。

S1 判据全过：撞基线视图显式拒绝且基线字节不变、diff 保留；干净视图照常併入并删 diff。
