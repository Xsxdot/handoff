# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# B102 并线实现计划：把 w4 控制台线并回 main

> **For agentic workers:** 本计划的执行方式由上面的「执行纪律」段落规定。步骤用 `- [ ]` 复选框便于跟踪。

**Goal:** 产出一条 `integration/w4-main` 分支，把 `origin/w4-delivery`（控制台全套）合进 `main`（B92/B93/B99 修复 + 发布链路），两边能力一个不丢，Go 与前端全绿，控制台端点在真进程上可达。

**Architecture:** 一次 `git merge`，14 个文件冲突、23 个冲突块，外加 57 个文件的机械改名。合并方向、冲突口径、撞号处置见 spec：`docs/superpowers/specs/2026-08-15-b102-w4-main-lineage-merge-design.md`（本分支起点已含该文件，**开工前先完整读一遍**）。

**Tech Stack:** Go 1.26（模块 `github.com/Xsxdot/handoff`）、React + TypeScript + Vite（`web/`）、vitest。

## Global Constraints

- **合并方向固定**：`main` 为宿主，合入 `origin/w4-delivery`。不得反向。
- **模块路径固定为 `github.com/Xsxdot/handoff`**。合并后 `go.mod` 必须是这一行，任何 `.go` 文件里
  不得残留 `github.com/xushixin/handoff`。
- **冲突默认「两边都要」**。只有在两边实现同一件事时才取其一，且取的那边必须能通过另一边的测试。
  参见 spec §2.1。
- **两边已有的日志调用一条都不能丢。** 合并后 `internal/agentd/`、`internal/executor/`、
  `internal/manager*` 里出现在冲突块两侧的每一条 `s.log.*` / `a.log.*` / `m.log.*` 调用都要在。
  本计划不新增日志：这是一次合并，没有新增 I/O、没有新增行为，新增日志只会制造噪音。
- **不合并进 `main`，不 `git push` 到 `main`，不动任何 tag。** 只交 `integration/w4-main` 分支。
- **不改任何功能行为。** 发现的 bug 追加到 `docs/superpowers/backlog.md` 记账，不顺手修。
- **不动 `~/.handoff`**（这台机器正在服役的 agentd 数据目录）。Task 5 的冒烟必须用临时目录与临时端口。
- **不删除任何一条 backlog 条目**，包括看起来重复的。
- 每个 task 完成即 commit，提交信息前缀用 `merge(b102):`。

---

### Task 1: 合并 + 机械改名 + 解 13 个代码/文档冲突

**Files:**
- Create: 无
- Modify（冲突文件，13 个；`docs/superpowers/backlog.md` 留给 Task 3）：
  `README.md`、`cmd/agentd.go`、`cmd/project.go`、`cmd/status_test.go`、`cmd/tasks.go`、
  `internal/agentd/server.go`、`internal/agentd/workspace.go`、`internal/agentd/workspace_test.go`、
  `internal/client/client.go`、`internal/config/config.go`、`internal/config/config_test.go`、
  `internal/prochost/footprint.go`、`internal/prochost/footprint_test.go`
- Modify（机械改名，约 57 个 `.go` 文件）：全部 import `github.com/xushixin/handoff` 的文件

**Interfaces:**
- Consumes: 无（第一个 task）
- Produces: 一条能通过 `go build ./...` 的 `integration/w4-main` 分支；后续所有 task 都在它之上

- [ ] **Step 1: 确认现场，并取到对面分支**

分支与工作树由 handoff 派发时建好了，**不要自己再建**：

```bash
cd "$(git rev-parse --show-toplevel)"
git branch --show-current                # 期望 integration/w4-main
git log --oneline -1                     # 期望 85f8e825d 或其后代
git fetch --all --prune
git rev-parse origin/w4-delivery         # 期望 84013dd7950023990dc5c67b87d844644bdd2d3d
```

分支名不是 `integration/w4-main`，或 `origin/w4-delivery` 取不到：**停下发工单**，不要将就着开工。

- [ ] **Step 2: 起合并，确认冲突面与预期一致**

```bash
git merge --no-commit --no-ff origin/w4-delivery
git diff --name-only --diff-filter=U | sort
```

期望恰好这 14 行（顺序无关）：

```
README.md
cmd/agentd.go
cmd/project.go
cmd/status_test.go
cmd/tasks.go
docs/superpowers/backlog.md
internal/agentd/server.go
internal/agentd/workspace.go
internal/agentd/workspace_test.go
internal/client/client.go
internal/config/config.go
internal/config/config_test.go
internal/prochost/footprint.go
internal/prochost/footprint_test.go
```

**若实际清单与此不同**（多出或少了文件），说明两边分支在本计划写就之后又动过：
**停下，发工单把实际清单交审核者裁决**，不要自行推进。

- [ ] **Step 3: 机械改名 import 路径**

```bash
grep -rl "github.com/xushixin/handoff" --include="*.go" . \
  | xargs sed -i '' 's#github.com/xushixin/handoff#github.com/Xsxdot/handoff#g'
grep -rn "github.com/xushixin/handoff" --include="*.go" . ; echo "剩余: $?"
head -1 go.mod
```

`grep` 必须无输出（`echo` 打印 `剩余: 1`），`go.mod` 首行必须是 `module github.com/Xsxdot/handoff`。

改名会把冲突文件里的冲突标记也一起扫过——这是无害的，标记是 `<<<<<<<` 不含模块路径。

- [ ] **Step 4: 逐个解冲突（13 个文件，backlog.md 除外）**

对每个冲突文件，按这个顺序做，一个文件一个文件来：

1. `git log --oneline main..origin/w4-delivery -- <文件>` 和
   `git log --oneline origin/w4-delivery..main -- <文件>` 看两边各自改了什么、为什么改。
2. 打开冲突块，判断这一处是「两边加了不同的东西」还是「两边改了同一件事」。
3. **前者一律都留**；后者才取舍，且要在提交信息里写明取了哪边、为什么。
4. 删干净 `<<<<<<<` / `=======` / `>>>>>>>` 三种标记。

四个文件有明确的已知要点，必须照办：

- **`internal/agentd/server.go`（4 块，是路由表）**：main 侧与 w4 侧注册的路由**全都要在**。
  漏注册一条的后果就是一个 405——本次并线要修的正是一个 405（`/api/projects/tree`）。
  解完之后，在文件里搜一遍这些路径必须都出现：`/api/projects/tree`、`/api/machines`、
  `/api/pty`、`/api/tasks`、`/api/status`。
- **`internal/config/config.go`（3 块）与 `config_test.go`（2 块）**：main 侧的
  `proc_fence_task_budget`、`proc_fence_task_hard_limit`、`proc_fence_reserve_ratio`
  与 w4 侧的 pty / auth 相关字段**都要保留在合并后的结构体里**，两边的默认值逻辑都要在。
  `config_test.go` 两边的用例都要留下。
- **`internal/prochost/footprint.go` + `footprint_test.go`（各 1 块）——本次最危险的一处**：
  这个包由 w4 线创建，main 线的 B93 在它之上加了每任务点名依赖的 `Footprint`。
  解错**不会编译失败**，只会让 B93 的两档点名静默失效。所以这一处解完之后，
  必须能说清楚 `Footprint` 的签名与返回语义在合并后与 main 侧一致；
  最终由 Task 2 的四条 B93 用例钉死。
- **`README.md`（2 块）**：两边各自新增的章节都留，不要为了「看起来整齐」删掉任何一边的说明。

- [ ] **Step 5: 在每处语义调和点写明「为什么这么解」**

凡是第 4 步里做了「取舍」（不是「都留」）的地方，在该处代码上方补一行中文注释，写明：
两边原本各是什么、取了哪边、为什么另一边可以不要。格式沿用仓库既有风格：

```go
// 合并 B102：main 侧按 uid 汇总、w4 侧按 pid 汇总，这里取 main 侧——
// B93 的每任务点名依赖「同一个 uid 下的总量」，按 pid 汇总拿不到这个口径。
```

**注释解释「为什么」，不复述代码。** 只是「两边都留」的地方**不要**加注释，那是噪音。

- [ ] **Step 6: 确认编译通过**

```bash
go build ./... && echo BUILD_OK
```

期望：`BUILD_OK`。**此时测试可能是红的，那是 Task 2 的事**，不要为了让测试变绿在这一步改行为。

- [ ] **Step 7: 提交**

```bash
git add -A
git commit -m "merge(b102): 合入 w4-delivery，改名 import 路径，解 13 处代码冲突"
```

---

### Task 2: Go 全绿，并点名确认 main 线七条修复用例

**Files:**
- Modify: 由测试结果决定（预期集中在 Task 1 解过冲突的那 13 个文件）
- Test: 全仓库既有测试，不新增用例

**Interfaces:**
- Consumes: Task 1 产出的可编译分支
- Produces: `go test -count=1 ./...` 0 FAIL 的分支

- [ ] **Step 1: 跑全量测试，把失败清单落到文件**

```bash
go vet ./... 2>&1 | tee /tmp/b102-vet.txt
go test -count=1 ./... 2>&1 | tee /tmp/b102-test.txt
grep -E "^(FAIL|--- FAIL)" /tmp/b102-test.txt | sort -u
```

- [ ] **Step 2: 逐条修红**

**修的必须是合并本身造成的错**——漏留的字段、漏注册的路由、被取舍掉的分支。
**不允许**为了让测试变绿而删用例、改断言、加 `t.Skip`。
如果某条用例在合并后确实不再成立（两边实现同一件事、取舍后语义变了），
那是**需求取舍**：发工单说明是哪条用例、两边原本各断言什么、为什么现在冲突，等审核者裁决。

- [ ] **Step 3: 重跑直到全绿**

```bash
go build ./... && go vet ./... && go test -count=1 ./... 2>&1 | tail -40
```

期望：0 FAIL。

- [ ] **Step 4: 点名确认 main 线七条修复用例存在且通过**

漏任何一条即为合并事故（B92/B93/B99 是刚在真机上复验过的修复，不能在并线里丢掉）：

```bash
go test -count=1 -run 'TestTurnFailureKeepsEventChannelOpen|TestSendRefusesOnClosedChannel' -v ./internal/executor/grok/ ./internal/executor/codex/
go test -count=1 -run 'TestHandleResultSweepsProcsOnFail|TestHandleResultSweepsProcsOnSuccess|TestScanTaskProcsWarnsOnceAtBudget|TestScanTaskProcsRearmsAfterFallback|TestDoneIsIdempotentOnCompleted' -v ./internal/agentd/
```

期望：每条都出现 `--- PASS`，且**没有任何一条报 "no tests to run"**。
`no tests to run` 意味着用例在合并中丢了——**这比 FAIL 更严重，必须停下修回来**。

- [ ] **Step 5: 确认 w4 线的文件都还在**

```bash
ls internal/agentd/{projecttree,projectfanout,machines,pty_api,pty_ws,workspacefiles,forward,forward_ws,mirror,auth,authroutes,eventframes,frames_stream,hostguard,projectjoin,taskroute,tasksfanout,workspaceprobe}.go
ls -d internal/ptyhost web
```

期望：全部存在，无 `No such file`。

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "merge(b102): Go 全绿；点名确认 B92/B93/B99 七条用例仍在且通过"
```

---

### Task 3: backlog 合表——保留两线原 ID，加一列「线」

**Files:**
- Modify: `docs/superpowers/backlog.md`（Task 1 遗留的最后一个冲突文件）

**Interfaces:**
- Consumes: Task 2 的分支
- Produces: 一份两线条目齐全、每行带「线」列的 backlog

- [ ] **Step 1: 取出两边各自的版本**

```bash
git show main:docs/superpowers/backlog.md > /tmp/backlog-main.md
git show origin/w4-delivery:docs/superpowers/backlog.md > /tmp/backlog-w4.md
grep -c '^| B' /tmp/backlog-main.md /tmp/backlog-w4.md
```

- [ ] **Step 2: 合成一张表**

规则：

1. **每一行保留它原本的 ID，不重编号。**
2. 表格新增一列「线」，取值 `main` 或 `w4`，插在「ID」列之后。
   两边共有、内容一致的历史条目（分叉点之前的）标 `共同`。
3. 两边的行都要在，**一条都不能删**，包括看起来重复的——真重复也留着，在「备注」里
   注明疑似与哪一条重复，交审核者判断。
4. 排序：按「线」分组不排序，直接**先放 `共同`，再放 `main`，再放 `w4`**，
   每组内按原文件里的先后顺序。不要重排原有顺序。

- [ ] **Step 3: 在表头上方写明歧义与新号起点**

在 `## Backlog` 标题之下、表格之上插入这段（照抄，不要改写）：

```markdown
> **编号说明（B102 并线遗留）**：`B80`–`B93` 这段号在 main 线与 w4 线**各有一套、各指不同的事**。
> 并线时选择保留两边原 ID 而不重编号（重编号要改的交叉引用遍及 specs/、plans/、notes/ 与提交信息，
> 改漏一处就是一条死链）。代价是：**这段号单独出现时不唯一，必须连「线」列一起看**。
> **新条目从 B103 起编号**，此后全局唯一。
```

- [ ] **Step 4: 自查**

```bash
grep -c '^| B' docs/superpowers/backlog.md      # 应 = 两边行数之和（去掉重复表头）
grep -n '<<<<<<<\|=======\|>>>>>>>' docs/superpowers/backlog.md ; echo "冲突标记: $?"
grep -n 'B103' docs/superpowers/backlog.md
```

期望：条目数对得上、`冲突标记: 1`（无残留）、能搜到 `B103` 那句说明。

- [ ] **Step 5: 提交**

```bash
git add docs/superpowers/backlog.md
git commit -m "merge(b102): backlog 合表——两线原 ID 保留，新增「线」列，新号从 B103 起"
```

---

### Task 4: 前端全绿

**Files:**
- Modify: 由结果决定，预期集中在 `web/src/api/types.ts`（若 `internal/proto` 的字段名被 main 侧改过）
- Test: `web/` 既有 vitest 用例，不新增

**Interfaces:**
- Consumes: Task 3 的分支
- Produces: `vitest` / `tsc -b` / `vite build` 三件套全绿的分支

- [ ] **Step 1: 装依赖并跑三件套**

```bash
cd web
npm ci
npx vitest run 2>&1 | tail -20
npx tsc -b 2>&1 | tail -20
npx vite build 2>&1 | tail -10
```

- [ ] **Step 2: 修红**

前端的红大概率来自**后端结构体字段名在 main 侧变过**，而 `web/src/api/types.ts` 还是 w4 侧的写法。
修的口径：**以合并后的 Go 结构体的 JSON tag 为准改前端类型**，不要反过来改 Go。
每改一处，在该字段旁补一行注释说明它对应哪个 Go 结构体，例如：

```ts
// 对应 proto.MachineStatus.Ok（Go 侧 json tag "ok"）
ok: boolean
```

**不允许**用 `any`、`@ts-ignore`、`eslint-disable` 让它变绿。真改不动就发工单。

- [ ] **Step 3: 确认 B94 的四条用例还在**

```bash
cd web
npx vitest run src/app/homedock src/app/tree src/app/workbench 2>&1 | tail -20
```

期望：全部 PASS，且用例总数不少于 `origin/w4-delivery` 上的数量。对比一下：

```bash
git stash list >/dev/null; git show origin/w4-delivery:web/src/app/homedock/HomeDock.test.tsx | grep -c "it("
```

- [ ] **Step 4: 提交**

```bash
cd "$(git rev-parse --show-toplevel)"
git add -A
git commit -m "merge(b102): 前端三件套全绿"
```

---

### Task 5: 端点存在性冒烟——真起一个 agentd

**Files:**
- Create: `docs/superpowers/notes/2026-08-15-b102-merge-ledger.md`（ledger 收口，见 Step 4）
- Modify: 无

**Interfaces:**
- Consumes: Task 4 的分支
- Produces: 「405 已消失」的实证；这是本条 backlog 的直接目标

- [ ] **Step 1: 起一个临时 agentd**

**必须用临时 datadir 与临时端口。不要动 `~/.handoff`**——那是这台机器正在服役的数据目录，
碰它会和正在跑的 agentd 抢单实例锁。

```bash
cd "$(git rev-parse --show-toplevel)"
go build -o /tmp/b102-handoff .
TMPD=$(mktemp -d /tmp/b102-datadir.XXXXXX)
cat > "$TMPD/config.yaml" <<'EOF'
listen: 127.0.0.1:17877
token: b102smoketoken
datadir: __TMPD__
repo_root: __TMPD__/repos
EOF
sed -i '' "s#__TMPD__#$TMPD#g" "$TMPD/config.yaml"
/tmp/b102-handoff agentd --config "$TMPD/config.yaml" > "$TMPD/agentd.log" 2>&1 &
SMOKE_PID=$!
sleep 3
echo "pid=$SMOKE_PID"; tail -5 "$TMPD/agentd.log"
```

- [ ] **Step 2: 打三个端点，断言都不是 405**

```bash
for p in "/api/projects/tree?scope=all" "/api/machines" "/api/tasks"; do
  code=$(curl -s -o /tmp/b102-body.json -w '%{http_code}' \
    -H "Authorization: Bearer b102smoketoken" "http://127.0.0.1:17877$p")
  echo "$p -> $code"
done
```

期望：三行都**不是 405**（200 是理想值；因为是空数据目录，其它 2xx/4xx 也可接受，
**唯独 405 表示路由没注册，那就是合并把路由弄丢了，必须回 Task 1 补**）。

- [ ] **Step 3: 收干净现场**

```bash
kill $SMOKE_PID 2>/dev/null; sleep 1
ps -p $SMOKE_PID >/dev/null && kill -9 $SMOKE_PID
rm -rf "$TMPD" /tmp/b102-handoff /tmp/b102-body.json
ls -d "$TMPD" 2>&1 | head -1     # 期望 No such file
```

**这一步不能跳。** 留着一个临时 agentd 在跑，会让这台机器上出现第二个 agentd 进程。

- [ ] **Step 4: 写 ledger 并提交**

新建 `docs/superpowers/notes/2026-08-15-b102-merge-ledger.md`，文件头写清职责与边界，正文含：

- 每个 task 的完成时间与 commit 范围
- 每一处「取舍型」冲突解法：文件、两边原本各是什么、取了哪边、理由
- Task 2 Step 4 七条点名用例的实际输出（PASS 行原文）
- Task 5 Step 2 三个端点的实际状态码
- 过程中发现但**没有修**的问题（按全局约束，这些只记账）

```bash
git add docs/superpowers/notes/2026-08-15-b102-merge-ledger.md
git commit -m "merge(b102): 冒烟通过——三个控制台端点均非 405；ledger 收口"
```

- [ ] **Step 5: 最终自检**

```bash
git log --oneline main..integration/w4-main
git status --short          # 期望干净
grep -rn "github.com/xushixin/handoff" --include="*.go" . ; echo "残留: $?"
go build ./... && go vet ./... && go test -count=1 ./... 2>&1 | grep -cE "^(FAIL|--- FAIL)"
```

期望：5 个以上提交、工作区干净、`残留: 1`、失败计数 `0`。

**做完停下等审核者。不要 `git push` 到 `main`，不要合并进 `main`。**
