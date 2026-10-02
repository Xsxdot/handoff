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

### 7.7 S3 抽样取证（第二段步骤 1，删除前）

33 条未复现边先全量复算（python3：五份 B233.x 的 edgesAdded/implementsAdded 元组对基线 edges/implements 集合求差）= 2+4+1+15+11 = 33，与 §6.4 分布逐份一致。抽样 3 条、跨三份视图，逐条对当前 main 源码取证（取证树 = `cards/B379-charter-1` 工作树，即 main@0e5db81b + 本卡 docs 提交，源码与 main 一致）：

**S3-1**（来源 cards-B233.1-charter-2，同边亦在 B233.4/B233.5 未复现清单中）：边元组 `n_opencode_Adapter_authorizeNativePermission -> m_executor_ApprovalClient`。
取证：`internal/executor/opencode/adapter.go:831` `func (a *Adapter) authorizeNativePermission`，函数体内 `:840` `executor.Authorize(ctx, r.approval, executor.ApprovalRequest{…})`，`r.approval` 字段类型即 `executor.ApprovalClient`（`:301` `approval executor.ApprovalClient`）；另 `:868/:879/:883` 三处 `r.approval.Acknowledge(…)`。**结论：关系真实存在。**
基线对照：基线记有该函数到 ApprovalAck/ApprovalRef/ApprovalRequest/Authorize 等 9 条边，独缺到接口类型 ApprovalClient 这条——重扫边提取对 func→接口类型边漏采。

**S3-2**（来源 cards-B233.5-charter）：边元组 `n_agentd_Server_resolveReceiver -> n_scheduling_Service_DefaultCarrier`。
取证：`internal/agentd/receiver_bind.go:17` `func (s *Server) resolveReceiver(…)`，`:24` `defaultName, defErr := s.scheduling.DefaultCarrier()`；被调方 `internal/scheduling/scheduling.go:421` `func (s *Service) DefaultCarrier()`。**结论：调用关系真实存在。**
基线对照：基线记 resolveReceiver → m_scheduling_ResolvedReceiver / n_scheduling_ResolveLookup，独缺这条跨包方法调用——重扫漏采。

**S3-3**（来源 cards-B233.2-charter）：implements 元组 `m_executor_StaticProvider -> m_executor_Provider`。
取证：`internal/executor/capability.go:99` `type StaticProvider struct`，`:105` `func (p StaticProvider) Name() string`、`:108` `func (p StaticProvider) Report() CapabilityReport`，与 `Provider` 接口（`:71-74`：`Name() string` + `Report() CapabilityReport`）方法集完全吻合。**结论：实现关系真实存在。**
基线对照：基线 implements 记 agy/claudecode/codex/fake/grok/opencode 六个 Adapter → Provider，独缺 StaticProvider——重扫漏采。

**抽样结论（如实）**：3/3 抽样边在现行 main 源码中**真实存在**。33 条未复现边不得表述为「均不存在」；「重扫是权威读数」在边维度存在保真度缺口（func→接口边、跨包方法调用、非热路径 implements 三族漏采各中一条）。按计划 §4.1.3 判为「重扫保真度缺口」数据点（与 spec Out of Scope「33 条全量追查」同族）：**本卡处置不变**——删除依据（§6.2 三条：撞基线守卫必拒、吸收=回退、B358 旧快照腐化）不依赖「边不存在」；删除丢失的是这批边的图记录（覆盖债），归将来对 main 的新鲜扫描。S3 故事结论**不写**「退役不丢现实存在的关系」的全称肯定，取证原文升级协调者裁决结论措辞。

### 7.8 步骤 2+3：删取代组、修剪并吸收 B374

- 取代组 7 份 `git rm` 与 §7.7 同批提交（amend 收进一个提交，hash 以收尾清单为准）。
- B374 修剪前置复验：diff `containersAdded` 仅 `k_collab_model`，与基线容器逐键同内容（`{"label":"collab 实体","kind":"实体","domain":"d_collab"}`），d_collab 在基线 domains——§6.4 前提成立。外科编辑：单行 JSON 内精确替换（断言命中唯一）删去该条目，`containersAdded` 留空对象；其余键字节不动（nodesAdded 9、nodesModified 1、edgesAdded 12、edgesDeleted 1、base 41a28474 原样）。
- 吸收：`~/go/bin/codegraph absorb cards-B374-charter` → 退出 0：

```
已併入视图 cards-B374-charter：+9 节点 ~1 -0，基线 5329 节点 @678d27f4f6663942bf743b56cf25581069112f8c
```

diff 文件被工具删除，CheckEdges 12 条边全过（无误拒，§4.6 协议未触发）。

- **计划外发现（按 §4.8 不动、记台账、升级）**：`codegraph/diffs/cards/B233.28-charter.json`——git 跟踪（bad735a1，2026-09-15，`graph(B233.28): 对账补齐任务面/项目位置切门面视图`），view 名 `cards/B233.28-charter`，base b5ebef69，内容 15 节点增/77 节点改/1 节点删/20 边增/39 边删。它在 `diffs/cards/` **子目录**里，`ListViews` 只扫顶层 `diffs/*.json`，故 `codegraph views` 不列出、validate/check 也不校验它——十二份侦察（顶层 glob）与 S2 验收命令均不受影响，但它是一份未吸收也未入账的漏网视图。本卡不处置，升级协调者。

### 7.9 步骤 4：改挂并吸收 B272

改挂前置复验：`containersAdded` 仅 `k_dropdir_fn`（domain d_coordination_api），nodesAdded 7 个 id 与 §6.1 清单逐字一致。外科编辑：`"domain": "d_coordination_api"` → `"domain": "d_gateway"`（文件为两空格缩进多行 JSON，替换串全文件命中恰 1 次，`git diff --stat` 恰 1 行变更）。判据 = spec 实现决定：agentd 包既存容器全归 d_gateway、dropdir 随包归属。

- 吸收：`~/go/bin/codegraph absorb cards-B272-charter` → 退出 0：

```
已併入视图 cards-B272-charter：+7 节点 ~0 -0，基线 5336 节点 @55a914bc4a78067d722e1371f8a422b3b7507616
```

diff 文件被工具删除，CheckEdges 无误拒（§4.6 协议未触发）。

### 7.10 步骤 5：直收组三份原样吸收（不编辑）

- `~/go/bin/codegraph absorb cards-B369-charter` → 退出 0：

```
已併入视图 cards-B369-charter：+29 节点 ~0 -0，基线 5365 节点 @55a914bc4a78067d722e1371f8a422b3b7507616
```

- `~/go/bin/codegraph absorb cards-B395-charter-5` → 退出 0：

```
已併入视图 cards-B395-charter-5：+5 节点 ~47 -0，基线 5370 节点 @55a914bc4a78067d722e1371f8a422b3b7507616
```

- `~/go/bin/codegraph absorb cards-B398-charter-12` → 退出 0：

```
已併入视图 cards-B398-charter-12：+0 节点 ~110 -0，基线 5370 节点 @55a914bc4a78067d722e1371f8a422b3b7507616
```

三份的 nodesModified 锚更新（47/110 条）属正常保鲜，不是腐化。~47/~110 与台账 §3 的 nodesModified×47/×110 读数一致。吸收后 `codegraph/diffs/` 顶层仅剩计划外 `cards/` 子目录（§7.8）。步骤 4+5 的 baseline 变更同文件难以拆分，合入一个提交（计划 §4.7.6「建议每步一提交」为建议项，报文已逐份留痕）。

### 7.11 步骤 6：S2 终验（handoff 仓根，新二进制）

**① `~/go/bin/codegraph views`** → 退出 0：

```json
{
 "views": []
}
```

**② `~/go/bin/codegraph validate`（完整性半边）** → 退出 0，`issues: None`（零完整性问题）。

**`~/go/bin/codegraph validate --stale`** → **退出 1**：完整性 issues 仍为 0，但 stale（保鲜检测）1165 条——S2 计划判据「--stale 零 issue 退出 0」在卡内 scope 内不可达，事实与判定如下，**升级协调者裁决 S2 验收读法**：

- 预状态对照（临时 worktree @10be7c17，改动前基线 + 同一二进制复跑）：stale **1215** 条，退出 1——**该判据在本卡动手前就不成立**（基线 scannedAt 2026-09-14，源码漂移 18 天；与 spec 写判据时未实测预状态同族，属 plan 节点漏检项，与 §6 计数勘误同类）。
- 吸收净效应：1215 → 1165（**−50**）：73 条被直收组/修剪改挂组的 nodesModified 保鲜更新治好；**23 条新增**——构成：B374 nodesAdded 9/9 全部、B369 nodesAdded 10、B272 nodesAdded 2（handleDropPut/forwardDropIfRequested）、B398-charter-12 nodesModified 保鲜更新中的 2（n_agentd_Server_runStep、n_agentd_requiresInlineLocalFile）。原因一致：diff 携带的是**分支时态锚**，diff 生成（09-17~09-23）到吸收（10-02）之间 main 源码移动，锚行不再对上（报文「行内容与名字对不上（疑似代码已移动）」）。Absorb 忠实于 diff 内容，非腐化——这正是本卡主题（分支时态快照 vs 活基线）在接收侧的显影；全量修复途径是对 main 的新鲜扫描（spec Out of Scope，与 §6.3 覆盖债同族）。
- 两侧源码树相同（10be7c17 与 HEAD 的源码文件零差异，分支只动 docs/数据），stale 集合差异全部来自基线吸收，逐条归因见上。

**③ `~/go/bin/codegraph check`** → 退出 0，`fails: []`，仅 warns（container-unplaced k_dropdir_fn 等 legacy 提示，非失败）。

**④ 基线内容只读抽验（python3）**：

- `k_dropdir_fn` ∈ containers 且 `domain == "d_gateway"`：✓（`{"label":"dropdir（包级函数）","kind":"函数组","domain":"d_gateway"}`）；
- B374 九节点（§4.3 清单）全部在 nodes：✓；B272 七节点（§6.1 清单）全部在 nodes：✓；
- 节点数算术闭合：5320 → 5370（+9 B374、+7 B272、+29 B369、+5 B395、+0 B398）；边 6639 → 6683；
- meta.branch = `cards/B379-charter-1`（absorb 按当时 HEAD 戳，§4.8 工具既有行为）；meta.commit = 55a914bc4a78…（最后一次 absorb 时点的 HEAD；S2 收尾提交晚于它，hash 不同是 absorb 戳语义的必然，非漏记）。

**⑤ 改动面复核（`git diff --stat 10be7c17..HEAD`）**：恰为 baseline.json（唯一图内容变更，全部出自 5 次 absorb）、12 份 diff 文件删除（7 git rm + 5 工具删，numstat 全为 0 插入）、台账/plan/spec/roadmap 文档。`git diff 10be7c17..HEAD -- codegraph/target.json codegraph/best.json` 为空（未触碰）；`.zcodeignore` 保持未跟踪未动；工作树收口时干净。

**附带证据（S1 延伸）**：预状态 worktree 用新二进制跑 `validate`，十二份积压视图中取代组七份全部被新守卫点名（`[cards-B233.x-charter*] 新增节点 … 已存在于基线，nodesAdded 只接受新节点`，共 323 条 view issues）——守卫对真实积压的拒收行为在真数据上复验成立。

**终验裁决请求（升级协调者）**：S2 的「validate --stale 零 issue 退出 0」建议改读为「validate 完整性 issues 为零、退出 0；stale 为保鲜计数、如实记录（预 1215 → 后 1165）」，或由协调者另卡安排新鲜扫描后回归该判据。执行者未对 stale 做任何数据干预（手改基线属全局禁止事项 3）。

### 7.12 协调者裁决（2026-10-02，implement 收尾后、review 前）

1. **S2 stale 判据误设，按完整性判据收口**。预状态 stale 1215 条（本卡动手前已存在，plan 节点未实测预状态属判据误设）；吸收后 1165（净 -50：73 治愈、23 新增全部归因分支时态锚在 diff 生成→吸收间随 main 漂移，两侧源码树零差异已核实）。完整性半边（`validate` 零 issue、`check` 绿）通过。stale 存量归 roadmap「重扫配方保真度/覆盖债」既有条目，修复途径是对 main 的新鲜全量扫描。
2. **S3 结论措辞改写**：3/3 抽样边在现行 main 源码真实存在——重扫配方确有边提取缺口，不是「关系不存在」。退役处置仍成立（吸收=注入死锚的腐化本体，不解决缺口）。S3 故事结论按「抽样证实重扫配方边提取缺口（3/3 现存，台账 §7.7 取证原文）；退役处置不变；缺口归 roadmap 修复」收口，spec 原文「退役不丢现实存在的关系」的全称形式作废。
3. **漏网 diff B233.28 与工具盲区**：`codegraph/diffs/cards/B233.28-charter.json`（15 增/77 改/1 删/20 边增/39 边删，B233.28 卡已完成未吸收）因 ListViews 只扫顶层对 views/validate/check 不可见——数据积压与工具盲区两件事。按 spec OOS（十二份为界）本卡不动，开新卡处置；本卡收口不阻塞。
