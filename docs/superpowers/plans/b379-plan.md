# B379 实现计划：视图 diff 对陈旧基线的吸收防线与存量对账

> **卡**：B379 · **级别**：L2 · **spec**：`docs/superpowers/specs/b379.md`（2026-10-02 用户授权定稿）
> **事实台账**：`docs/superpowers/notes/b379-absorb-recon.md`——**§1–5 是侦察定稿，§6 是 plan 节点复核勘误（2026-10-02），动手前必读 §6**：它修正了 B272/B358 的计数读法、charter 仓主线名与构建命令，本计划全部以 §6 修正后的事实为准。
> **工作分支**：handoff 仓 `cards/B379-charter-1`（已检出，spec/台账已提交）；charter 仓 `b379-absorb-guard`（第一段第一步自建）。
> **读者假设**：零上下文执行者。全卡两段**有序**：第一段工具守卫（charter 仓），第二段存量对账（handoff 仓）；第二段的第一步前置是第一段的二进制已重建安装。

---

## 0. 执行者须知

- 涉及两个仓：**charter 仓** `~/workspace/charter`（工具源码，Go 模块在 `graph/` 子目录）；**handoff 仓** `~/workspace/handoff`（数据与对账对象，`codegraph/baseline.json` + `codegraph/diffs/*.json`）。
- codegraph 二进制：`~/go/bin/codegraph`。第一段会重建它；**第二段起所有命令一律写全路径 `~/go/bin/codegraph`**，防止 PATH 命中旧版二进制（旧版没有新守卫，静默放过腐化 diff）。验版本：`go version -m ~/go/bin/codegraph`。
- codegraph 命令的运行目录语义：一律在**目标仓根**执行（工具以 `.` 为仓根）；`absorb` 的视图参数 = `codegraph/diffs/` 下文件名去掉 `.json`。
- **台账纪律**：每确立一个事实（提交 hash、跑过的命令与原始输出、吸收报文原文、S3 取证原文、放弃的尝试、做出的判断），**当步**追加进台账 `## 7. 工作记录` 段，随批提交。禁止攒到最后补记。
- 本计划的现状行号会漂：一切以符号名（函数名/键名/视图名）定位，行号仅作我方 2026-10-02 读数参考。

## 1. 交付目标

| 故事 | 内容 | 落点 |
|---|---|---|
| S1 吸收防线 | `nodesAdded` 撞基线同 id 的视图被 absorb 显式拒绝（报文含节点 id 与「只接受新节点」语义），基线字节不变；干净视图照常併入并删 diff | 第一段，charter 仓 |
| S2 存量清零 | 十二份积压 diff 按四组处置完毕；`views` 不再列出它们；`validate --stale` 与 `check` 零问题退出 0；基线含直收组内容 | 第二段，handoff 仓 |
| S3 退役无损抽查 | 取代组 33 条未复现边抽 3 条对 main 源码取证，结论如实记台账 | 第二段第一步（**先于删除**） |

**本期不做**（spec Out of Scope，见到也不碰）：diff 基线指纹/扫描侧漂移防线、codegraph CLI 内建扫描、33 条未复现边全量追查、target.json/best.json 任何修订、十二份以外的新积压。

## 2. 已核事实（plan 节点 2026-10-02 复核，详见台账 §6）

1. **守卫落点**：charter 仓 `graph/codegraph/validate.go` 的 `ValidateDiff` 函数——diff 闸的唯一收口。同函数内已有 containersAdded 版先例（「新增容器 %s 已存在于基线，containersAdded 只接受新容器」），新判据是它的节点版，报文句式对齐。
2. **absorb 闸序**（`graph/cli/cli.go` 的 absorb 子命令）：LoadGraph → LoadDiff → `ValidateDiff` → `CheckEdges` → `Absorb` → SaveGraph → 删 diff。两道闸的拒绝都发生在删 diff **之前**，被拒视图的 diff 文件保留、基线不落盘。
3. **CheckEdges 的覆盖面**：只源码校验 `d.EdgesAdded`，implementsAdded 边不经源码校验（台账 §6.7）。
4. **charter 仓主线**：无本地 main，默认分支 `origin/master`（台账 §6.5）；`graph/` 是独立 Go 模块（仓内唯一），构建/测试/安装命令见 §3。
5. **十二份分类复核结论**：B272 七个 nodesAdded **零撞基线**（无需修剪节点）；B374 容器撞名一个、九个 nodesAdded 零撞；直收组三份零撞零硬拒；取代组七份每份都有撞基线 nodesAdded（新守卫下原样吸收一概被拒）。B358 的 spec 计数已勘误（42/125 撞基线），**删除处置不变**，依据改写为台账 §6.2 三条。

## 3. 第一段：吸收守卫（charter 仓）

### 3.1 建分支

```bash
cd ~/workspace/charter
git fetch origin
git switch -c b379-absorb-guard origin/master
```

注意：仓内当前检出的是 `codex/charter-story-batches`（工作树干净，切走安全，不必切回）。分支基点必须是 `origin/master`，不是当前检出分支。

### 3.2 改动边界

**准动（仅此三个文件）**：
- `graph/codegraph/validate.go`——仅在 `ValidateDiff` 函数体内新增判据；
- `graph/codegraph/validate_test.go`——新判据用例；
- `graph/codegraph/absorb_test.go`——分层边界用例（见 3.3 第 3 步）。

**不准动**：`graph/codegraph/absorb.go`（spec 决策：Absorb 保持纯函数不动，守卫单点在 ValidateDiff）、`graph/cli/cli.go`、`types.go`、其余一切文件。**不是本卡的修复一律不做。**

**判据内容（行为级）**：`d.NodesAdded` 中任一 id 已存在于基线 `g.Nodes` → 报一条 issue，文本 `新增节点 %s 已存在于基线，nodesAdded 只接受新节点`（%s = 节点 id），使 ValidateDiff 整体拒绝该 diff。判据**不涉及** NodesModified/NodesDeleted——它们已有「须在基线」判据，语义不同（spec 已定，不重新设计）。

### 3.3 顺序（TDD）

1. **红**：`validate_test.go` 先写用例——撞基线 id 的 diff 断言 issues 含该节点 id 与「只接受新节点」字样；全新 id 的 diff 断言不新增该类 issue。跑 `cd ~/workspace/charter/graph && go test ./codegraph/`，新用例必须先红。
2. **绿**：`ValidateDiff` 加判据转绿；`cd ~/workspace/charter/graph && go test ./...` 全绿（charter 仓仅 graph 一个 Go 模块，全量在此跑）。
3. **分层边界用例**：`absorb_test.go` 补一条用例，钉住「Absorb 不做查重、不拒绝，守卫单点在 ValidateDiff」——这是 spec「Absorb 保持纯函数不动」决策的回归锚。
4. **重建安装**：`cd ~/workspace/charter/graph && go install ./cmd/codegraph`（注意：graph 是独立模块，仓根跑 `go install ./graph/...` 不可运行——台账 §6.6 勘误）。装完 `go version -m ~/go/bin/codegraph` 确认构建时间晚于本次提交。
5. 提交（charter 仓提交信息跟随该仓惯例）；台账记提交 hash 与测试输出。

### 3.4 第一段验收（S1 证据）

单测之外，**合成仓端到端**（真实入口是 absorb 命令，mock 不算数）：

1. 在 `/tmp` 重建台账 §2 的复现场景：合成仓基线含节点 `n_old`（锚 v1.go:10），diff 的 nodesAdded 带同名 id（旧锚 v0.go:99），其余字段使 diff 其余判据通过。**只准在 /tmp 合成仓做实验，禁止拿真实 handoff 基线做守卫实验。**
2. 拒绝例：`~/go/bin/codegraph validate`（合成仓根）→ 退出非 0，issues 含「新增节点 n_old 已存在于基线」；记录 `shasum -a 256 codegraph/baseline.json` → `~/go/bin/codegraph absorb <视图名>` → 退出非 0，报文含节点 id 与「只接受新节点」语义 → 复核 sha256 **不变**、diff 文件**仍在**。
3. 照常例：同合成仓另造一份干净 diff（全新 id 的合法新节点）→ absorb 成功，输出併入报文，diff 文件被删，基线含新节点。
4. 报文原文、sha256 前后值、命令输出全部落台账 §7。

### 3.5 第一段决策权限

- charter 仓 `go test ./...` 出现**与本卡无关的既有红** → 停，升级协调者，不顺手修、不跳过。
- 判据落点、报文句式、Absorb 不动——spec 已定，不重新设计；测试用例的组织方式、合成仓的搭建细节，执行者自便。
- 其余任何超出 §3.2 文件清单的改动冲动 → 停，升级。

---

## 4. 第二段：存量对账（handoff 仓，分支 cards/B379-charter-1）

### 4.0 前置检查（不满足就停）

- `go version -m ~/go/bin/codegraph` 显示第一段守卫之后的构建；
- handoff 仓在 `cards/B379-charter-1`，`git status` 干净（`.zcodeignore` 是他人未跟踪文件：**不提交、不动**）；
- 命令全部在 handoff 仓根执行。

### 4.1 步骤 1：S3 抽样取证（必须在步骤 2 删除之前）

1. 取证对象：取代组五份 B233.x 视图（B233.1/2/3/4/5）的 `edgesAdded`/`implementsAdded` 与基线 `edges`/`implements` 集合求差，共 33 条（分布 2/4/1/15/11；B233.6 与 B358 零边，见台账 §6.4）。抽 **3 条**，至少跨两份视图，逐条记录「来源视图 + 边元组」。
2. 逐条对**当前 main 源码**读调用点/实现点取证：该调用/实现关系现在是否真实存在（给出文件与函数级证据）。
3. **两种结论都合法，都如实落台账**：
   - 关系不存在（预期主流）→ 支持退役无损；
   - 关系确实存在 → 记证据，该条判为「重扫保真度缺口」数据点（与 spec Out of Scope 同族），本卡处置不变，但 S3 的故事结论**不得**写「退役不丢现实存在的关系」的全称肯定——把取证原文升级协调者裁决结论措辞。
4. 执行者**不得**为凑「确不存在」的预期改写取证结论。

### 4.2 步骤 2：删取代组（7 份）

```bash
git rm codegraph/diffs/cards-B233.1-charter-2.json \
       codegraph/diffs/cards-B233.2-charter.json \
       codegraph/diffs/cards-B233.3-charter.json \
       codegraph/diffs/cards-B233.4-charter.json \
       codegraph/diffs/cards-B233.5-charter.json \
       codegraph/diffs/cards-B233.6-charter.json \
       codegraph/diffs/cards-B358-charter.json
```

不吸收、不重放、不修剪（取代组无「部分保留」语义——spec 已定整份删除；B358 计数勘误不改变处置，依据见台账 §6.2）。与步骤 1 的台账更新同批提交。

### 4.3 步骤 3：修剪组 B374

1. 编辑 `codegraph/diffs/cards-B374-charter.json`：**仅删** `containersAdded` 键下的 `"k_collab_model"` 一个条目（基线已有同内容同领域容器：label「collab 实体」/kind「实体」/domain d_collab，台账 §6.4 复验）。其余一切键不动——9 个 nodesAdded（`m_proto_RoomsPage`、`n_collab_Service_ListRoomsPage`、`n_collab_trimRoomPage`、`n_collab_encodeRoomCursor`、`n_collab_decodeRoomCursor`、`m_collab_roomCursor`、`n_agentd_parseRoomsListParams`、`m_agentd_roomsListParams`、`n_agentd_roomsListErrorStatus`）、nodesModified、edges、summary（纯展示字符串）、base 全部原样。
2. `~/go/bin/codegraph absorb cards-B374-charter` → 预期併入报文（+9 节点），diff 文件被工具删除。
3. 被拒时走 §4.6 处置协议；台账记吸收报文原文。

### 4.4 步骤 4：改挂组 B272

1. 编辑 `codegraph/diffs/cards-B272-charter.json`：**仅改** `containersAdded["k_dropdir_fn"]["domain"]` 的值，`d_coordination_api` → `d_gateway`。判据（spec 已定，台账 §4/§6.4 复验）：重扫后 agentd 包既存容器（k_agentd_Server / k_agentd_fn / k_agentd_model / c_http）全部归 d_gateway，dropdir 是 agentd HTTP drop 面的包级函数组，随包归属；d_coordination_api 在基线 domains 已不存在。其余一切不动（七个 nodesAdded 零撞基线，台账 §6.1）。
2. `~/go/bin/codegraph absorb cards-B272-charter`；台账记报文原文。

### 4.5 步骤 5：直收组原样吸收（3 份）

B369、B395-charter-5、B398-charter-12 逐份 `~/go/bin/codegraph absorb <视图名>`，**不做任何编辑**。三份的 nodesModified 锚更新属正常保鲜（分支晚于重扫合并），不是腐化。台账逐份记报文原文。

### 4.6 CheckEdges 误拒处置协议（spec 测试决定段，显式协议）

CheckEdges 按当前源码逐条校验 diff 的 `edgesAdded`；若 absorb 报「含 N 条不可能真实的调用边，拒绝併入」：

1. **逐条对当前 main 源码裁决**该边两端的关系是否真实存在（读调用点，证据记台账）；
2. 确属分支时态旧关系（源码中已不存在或已变形）→ 从该 diff 的 `edgesAdded` **剔除该条**，重跑 absorb，台账记「剔除边元组 + 裁决理由」。这是对账语义的一部分（与取代组同判据：重扫是权威读数），**不是绕闸**；
3. 源码中关系确凿存在却被拒（疑似判边器误杀）→ **停**：不改 `edgegate.go`、不硬改数据、不反复试探，把边元组与源码证据升级协调者。
4. implementsAdded 边不经 CheckEdges（台账 §6.7），不存在此协议的触发面；若 ValidateDiff 在别的判据上拒了保留组视图（预案外的拒因），同样停下升级——预案内只有 B374 容器撞名（已修剪）与 B272 领域（已改挂）。

### 4.7 步骤 6：S2 终验

全部吸收完成后，在 handoff 仓根：

1. `~/go/bin/codegraph views` → `views` 数组为空（不含十二份名单中任何一个）；
2. `~/go/bin/codegraph validate --stale` → 零 issue，退出 0；
   【裁决注记 v3】本判据误设（预状态 stale 已 1215 条）：按协调者裁决（台账 §7.12）改按完整性半边收口——`validate`（不带 --stale）零 issue 退出 0；stale 存量如实记录（预 1215 → 后 1165，净 -50），修复归 roadmap 新鲜全量扫描。
3. `~/go/bin/codegraph check` → 退出 0；
4. 基线内容抽验（python3/jq **只读**）：`k_dropdir_fn` 在 containers 且 `domain == "d_gateway"`；B374 九个节点 id（§4.3 清单）在 nodes；B272 七个节点 id（台账 §6.1 清单）在 nodes；
5. `git status`/`git diff --stat` 复核本段改动面**恰为**：diffs/ 下 12 个文件消失（7 git rm + 5 工具删）、baseline.json 变更（全部出自 absorb）、台账与计划文档更新——绝无 baseline 手改痕迹、绝无 target/best 变更；
6. 终验输出原文落台账 §7，随批提交（建议每个处置步骤一个提交，S2 终验单独一个）。

### 4.8 第二段决策权限

- 保留组视图吸收被拒且拒因不在 §4.6 预案内 → 停，升级，不自行扩修剪面；
- 发现十二份之外的新积压 diff → 不动（spec 定界），记台账后升级；
- baseline meta 的 commit/branch 由 absorb 按当前 HEAD 自动戳（分支名 cards/B379-charter-1）——工具既有行为，不干预、不手工改 meta；
- baseline.json 若出现任何非 absorb 产生的 diff → 停，自查后升级。

## 5. 全局禁止事项

1. **不许顺手重构**：两个仓都只做本计划列出的改动；「看到就想改」的一切记台账留给后续。
2. **不许动 target.json / best.json**（spec Out of Scope）。
3. **不许手改 baseline.json 的图内容**：基线的一切变更必须出自 absorb 命令；修剪/改挂只准编辑 diff JSON 本身；不许写「对账脚本」改基线（只读抽验脚本自便）。
4. **不许回写改史**：台账 §1–6 与 spec 是定稿物，勘误/工作记录只追加（§7）。
5. **不许越节点**：本计划不含任何实现代码；第一段的代码改动在 implement 纪律下进行（TDD、测试三段律、日志注释纪律照 charter:implement）。

## 6. 验收对账表

| 故事 | 命令（位置） | 通过判据 |
|---|---|---|
| S1 | `cd ~/workspace/charter/graph && go test ./...` | 全绿，含撞基线用例与 Absorb 分层边界用例 |
| S1 | /tmp 合成仓：`validate` → `absorb <撞名视图>` | 退出非 0；报文含节点 id 与「只接受新节点」；baseline sha256 不变；diff 文件仍在 |
| S1 | /tmp 合成仓：`absorb <干净视图>` | 退出 0；併入报文；diff 被删；基线含新节点 |
| S2 | handoff 仓根：`~/go/bin/codegraph views` | views 空表 |
| S2 | handoff 仓根：`~/go/bin/codegraph validate --stale`、`~/go/bin/codegraph check` | 零 issue，退出 0 |
| S2 | 只读抽验 baseline.json | k_dropdir_fn ∈ containers 且 domain=d_gateway；B374 九节点、B272 七节点在册 |
| S3 | 台账 §7 取证记录 | 3 条边元组 + 逐条源码证据 + 结论（含「关系存在」的如实记录与升级，若发生） |

## 7. 完成定义

第一段：三文件改动提交于 `b379-absorb-guard`，S1 证据落台账，二进制已重建安装。第二段：十二份清零，S2/S3 证据落台账，`cards/B379-charter-1` 工作树干净。此后交协调者 review/acceptance；两分支的合并归 finish 节点，执行者不自行合并。
