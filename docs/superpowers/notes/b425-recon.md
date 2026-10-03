# B425 事实台账：子目录积压 diff 对账侦察（2026-10-03，协调者本会话）

## 1. 基线身份

- handoff 仓 main @642bfa0e（B379 finish 后）；本卡分支 `cards/B425-charter-1`，工作树 `~/.handoff/worktrees/manual/B425`。
- 现行基线 `codegraph/baseline.json`：5370 节点 / 6683 边，sha256 前 8 位 `5c4d260c`（python3 实测，2026-10-03）。
- 装机二进制 `~/go/bin/codegraph`：B379 §7.14 读数 vcs.revision=82c6c216 modified=false，本卡未重装前沿用。
- charter 仓 master @82c6c216（含 B379 守卫，tag graph/v0.10.2）；工作检出线 `codex/charter-story-batches` **不含**守卫——本卡源码读数一律取 `git show master:…`，实现须从 master 拉分支。

## 2. 盲区机制读数（charter 仓 master:graph/，2026-10-03）

- `ListViews`（`graph/codegraph/load.go:47`）：`os.ReadDir` 只扫 `diffs/` 顶层，`!e.IsDir()` 过滤——子目录条目直接跳过，非递归。这是盲区的唯一点。
- 消费方两处：`validate` 全视图校验循环（`graph/cli/cli.go:170`，ListViews → LoadDiff → ValidateDiff）；`views` 命令（`cli.go:458`）。
- `check`（`cli.go:249`）读 target/best + `graphLoadView`，**不列 diffs 目录**——它对子目录 diff「不可见」是因为内容从未被吸收、不进基线，与 ListViews 无关。
- `LoadDiff`（`load.go:33`）：`filepath.Join(diffs, view+".json")`——view 名带斜杠**可直接定位子目录文件**。`absorb <view>`（`cli.go:375` 命令层：LoadDiff → ValidateDiff → Absorb → SaveGraph → `os.Remove(diffPath)`）与 `--view` 叠加（`cli.go:84`）都不吃 ListViews，不受盲区影响——对账动作本身不被工具阻塞。
- `mergeEdges`（`graph/codegraph/absorb.go`）：deleted 边进 drop 集合，基线里不存在的边静默不删，**不报错**——基线漂移不会让吸收失败。
- 真仓复现（handoff 仓根，2026-10-03）：`diffs/cards/B233.28-charter.json` 在场，`codegraph views` → `{"views":[]}` 退出 0；`validate` 退出 0；`check` 退出 0。盲区在真数据上成立。

## 3. 漏网 diff 对账读数（cards/B233.28-charter，2026-10-03）

- 文件：`codegraph/diffs/cards/B233.28-charter.json`，提交 bad735a1（2026-09-15，「graph(B233.28): 对账补齐任务面/项目位置切门面视图」，author xsx），view 名 `cards/B233.28-charter`（带斜杠），base `b5ebef69`。
- 内容：nodesAdded 15 / nodesModified 77 / nodesDeleted 1（`n_agentd_Server_resumeIfIdle`）/ edgesAdded 20 / edgesDeleted 39 / implementsAdded 0 / 无 containersAdded。
- 对现行基线（python3 核对）：
  - nodesAdded ∩ 基线：**0/15**——零撞车，B379 守卫（ValidateDiff 拒 nodesAdded 撞基线）不会拒收；
  - nodesModified 77/77 全部 ∈ 基线；nodesDeleted ∈ 基线；
  - edgesDeleted 39/39 仍存在于现行基线；edgesAdded 20/20 无一已存在、端点零悬空（基线∪增∪改闭合）；
  - 牵连边：现行基线中触及 `n_agentd_Server_resumeIfIdle` 的边 4 条（absorb 删节点时一并剔除）；
  - 预期算术：节点 5370 + 15 − 1 = **5384**；边 6683 − 39 + 20 − 4 = **6660**。
- 结论：**直收候选**（B379 四类处置中最轻一类），无需外科编辑。
- 价值：15 个编排门面方法节点（gateway facade.go 的 Manager 方法）从未入图，吸收补真实覆盖。

## 4. 产生侧根因

- 纪律条文：charter 仓 `skills/recon/SKILL.md` 第 16/27 行与 `skills/contract/SKILL.md` 第 25 行均写 `codegraph/diffs/<分支>.json`，**未规定分支名扁平化**。
- 事故形态：B233.28 执行者照字面把分支名 `cards/B233.28-charter` 当文件名 → 带斜杠落成子目录；其余卡执行者自行拍平（既有视图全是 `cards-B*-charter` 扁平形态，如 B379 吸收的 `cards-B374-charter`）。
- 判定：纪律条文缺口，非个案手误——条文不改，带斜杠分支名会重演。

## 5. 复核命令要点

```bash
git -C ~/workspace/charter show master:graph/codegraph/load.go   # ListViews/LoadDiff 读数
git -C ~/workspace/charter grep -n ListViews master -- graph/    # 消费方两点
~/go/bin/codegraph views          # 真仓盲区复现：[] 但子目录有文件
python3 - <<'PY'                  # 撞车/闭合/算术核对（§3 读数来源）
import json
d = json.load(open('codegraph/diffs/cards/B233.28-charter.json'))
b = json.load(open('codegraph/baseline.json'))
PY
```

## 6. 工作记录

### 6.0 plan 节点零上下文复核与勘误（2026-10-03，独立复核 subagent + 协调者验算）

- 复核裁决：需修订，两条阻塞——**A1/E1**（S3 原写「regen 同步产物入同一提交」不可满足：`scripts/regen_discipline.py` 只写 `~/.handoff/discipline/charter-story-*.md` 活体目录，不入任何仓；账本权威副本需 `handoff discipline put`，计划未给授权）与 **B1**（S2 边算术 6660 重复扣减）。建议项 A2/A3/B2/B3/B4 一并采纳。
- **B1 验算（协调者亲手复核，python3）**：`edgesDeleted` 39 条与被删节点 `n_agentd_Server_resumeIfIdle` 牵连边重叠 **4/4**（牵连边共 4 条全部在待删集内）；`edgesAdded` 20 条零触碰被删节点、待增∩待删=0。故 `mergeEdges` 实际结果 = 6683 − 39 + 20 = **6664**。spec v2 与 plan 同步勘误。
- **纪律块面事实**：`handoff discipline list` 现存旧族七块（charter-recon v1 等），无 story 族块入账；`~/.handoff/discipline/charter-recon.md`（1174 字节）落后 skills/recon 正文（1206 字节）——存量滞后，S3 put 新版顺带治愈。story 族只随 regen 落盘，安装另走 charter_provision，本卡不碰。
- 其余抽验 8 项全属实（行号/计数/路径/分支状态/装机二进制版本）。

### 6.1 S1：ListViews 拒子目录（charter 仓，2026-10-03）

- TDD 红：新用例先红——`TestListViewsRejectsSubdir`（「diffs 下有子目录应当报错——视图必须平铺顶层，静默跳过就是盲区」）与 `TestListViewsRejectsEmptySubdir`（「空子目录也应当报错」）双 FAIL。
- 绿：`load.go` ListViews 加子目录检查（报文：「视图目录 codegraph/diffs 下发现子目录 %s：视图 diff 必须平铺在顶层（视图名取分支名时把 / 拍平为 -）」）。全量 `go test ./... -count=1`：cli / codegraph / webui 三包 ok，gofmt -l 零输出。
- 变异验牙：`sed 's/if e.IsDir() {/if false {/'` 摘除检查 → 两新用例 FAIL；还原 → ok。测试真实钉住行为。
- 提交：`0f93d4f1`（b425-listviews-guard，2 files）。
- **构建偏差记录**：先在 linked worktree（/tmp/b425-charter）构建——Go 在 `.git` 为文件的 worktree 里 VCS stamping 静默不生效（强制 `-buildvcs=true` 亦无戳无报错，实测）；改沿 B379 §7.5 先例把 charter 主检出切到 `b425-listviews-guard` 构建。中途一次误装：worktree 占用分支致 switch 失败而链式 install 照跑，装出 `codex/charter-story-batches@ffd60ed1` 的错分支二进制（无守卫无修复）；删 worktree 后重装纠正。最终 `go version -m`：`vcs.revision=0f93d4f1cb573c54c930243777da485caadee173`，`vcs.modified=false`。worktree 已删，主检出停在 b425-listviews-guard（S3 同分支续做，finish 切回 codex/charter-story-batches）。
- 真机红（B425 工作树，2026-10-03）：`views` 与 `validate` 均退出 1，报文同上点名 `cards`——S1 真机证据成立，S2 起点状态确认。

### 6.2 S2：cards/B233.28-charter 直收对账（handoff 仓，2026-10-03）

- absorb：`~/go/bin/codegraph absorb cards/B233.28-charter` → 退出 0：`已併入视图 cards/B233.28-charter：+15 节点 ~77 -1，基线 5384 节点 @a5dad5690603d993a1ecbea70679d20851c7a539`。diff 文件被工具删除，`rmdir codegraph/diffs/cards` 成功。工作树变更恰两处：baseline.json 改、cards/B233.28-charter.json 删。
- 四命令读数（B425 工作树）：`views` → `{"views": []}` 退出 0；`validate` → `issues: null`（完整性零 issue）、nodes 5384、edges 6664，退出 0；`validate --stale` → 退出 1「发现 0 个完整性问题、1174 个失鲜节点」（1165 → 1174，+9 为 B233.28 分支时态锚覆盖保鲜所致，B379 裁决 1 读法：Absorb 忠实于 diff，归 roadmap 重扫，不做数据干预）；`check` → 退出 0，`fails: []`，warns 28（legacy 提示，非失败）。
- **算术闭合（命中验算）**：节点 5370+15−1=5384 ✓；边 6683−39+20=6664 ✓（plan 节点勘误值被工具实测精确命中——原 6660 错式若未勘误将在此停机）。
- **逐点名验证**：`git show main:codegraph/baseline.json` 与吸收后基线的节点 id 集合差恰为新增 15 个 `n_orchestration_Manager_*` 门面方法（AnswerTicket/CountEvents/EventsFrom/EventsFromAsc/GetProjectLocationByName/GetTask/LatestEvent/ListMirrorTasks/ListProjectLocations/ListTasks/MirrorEventsFrom/MirrorTaskTarget/PendingTickets/ResumeIfIdle/UpdateProjectLocation）+ 移除 `n_agentd_Server_resumeIfIdle`——逐字清单强于「至少 3 个」判据。（勘误注记：spec S2 抽验样例曾猜 `n_orchestration_Manager_GetProjectLocation`，真名是 `GetProjectLocationByName`，猜测错误非数据缺失，已改逐点名验证。）
- 基线戳：meta.branch=`cards/B425-charter-1`、meta.commit=a5dad569（absorb 时点 HEAD，工具既有行为，不手工改）。

### 6.3 S3：纪律条文扁平化勘误与账本同步（charter 仓 + handoff 账本，2026-10-03）

- 条文勘误（b425-listviews-guard @9ae3fb51）：`skills/recon/SKILL.md` 红线条与 `skills/contract/SKILL.md` 骨架符号入图条各补一句——视图名/文件名取分支名必须扁平化（`/`→`-`）+ 理由（ListViews 只认顶层平铺，带斜杠落子目录即报错，B425）。注：本分支（origin/master 基）条文行号与 story-batches 检出线不同（contract 为 :24 非 :25），以分支实际为准。
- **计划外发现（不动、记台账）**：`scripts/regen_discipline.py` 的 `prefix` 参数未接入写盘路径（代码写死 `f"{out}/charter-{name}.md"`，docstring 却称写 charter-story-*）——即 regen 实际更新旧族文件、story 族盘上文件（Sep 28 旧产）静默滞留。属 charter 仓工具缺陷，roadmap 候选，本卡不修。
- **账本同步（含一次误 put 的更正）**：先误把陈旧 story 族文件 put 为 charter-recon v2 / charter-contract v4（内容无新条文）；查明后以 regen 新产的正确文件重 put——`charter-recon` → **v3**、`charter-contract` → **v5**，`discipline get` 验证正文含扁平化条文。版本化不可变，v2/v4 留史不删。`discipline get charter-recon`（旧 v1）put 前读数：0 处扁平化（旧版滞后确认，本次顺带治愈）。
