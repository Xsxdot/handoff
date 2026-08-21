# 组装点死配置告警（dead-assembly）实现计划

> **For agentic workers:** 本计划由单上下文执行者逐 task 实现，步骤用 `- [ ]` 勾选跟踪。

**Goal:** 给 `handoff graph check` 补一条与 `dead-rule` 对称的 `dead-assembly` warn——`target.json` 的 `assembly` 条目在视图里找不到任何节点文件时报出来，堵掉「组装点写错文件名零信号」这个静默失败缺口。

**Architecture:** 纯增量。在 `internal/codegraph/check.go` 的 `Check` 里多收一份「视图中出现过的全部节点文件」集合，在既有 dead-rule 循环之后加一段 assembly 比对。不新增类型、不改任何既有判定语义、不影响 `Fails`（因而不影响闸门退出码）。

**Tech Stack:** Go 1.26，标准库；测试用 `internal/codegraph` 既有的 `mkView` / `assertKinds` 表驱动夹具。

## 背景：这个缺口是怎么被发现的

2026-08-21 的双轨对照实验里，两个执行者分别重写 `codegraph/target.json`。其中一方发现旧文件的 `assembly` 写着 `["main.go", "cmd/main.go"]`，而 **`cmd/main.go` 在仓库里根本不存在**——它是从 spec 示例抄来的死配置，在基准里躺了整整一轮没有被任何机制发现。另一方不但没发现，还为这个不存在的文件写了一句肯定的用途说明。

根因不在执行者，在机制：`ValidateTarget` 完全不看 `Assembly`，`Check` 只把它当 set 做 caller 文件比对。`paths` 规则写错至少还有 `dead-rule` warn 兜底，`assembly` 写错则**零信号**。本卡就是把这条对称性补上。

## Global Constraints

- **只动 `internal/codegraph` 一个包**（外加本计划要求的图 diff 产物）。不改 `cmd/`、不改 `codegraph/target.json`、不改 `cmd/graph_gate_test.go`。
- 新 warn **只能进 `Warns`，绝不能进 `Fails`**：仓库当前的 `assembly` 是 `["main.go"]`，而 `main.go` 在 baseline 里没有节点（扫描未覆盖），落 fail 会当场把闸门打红。
- 新增的 `Finding.Kind` 取值定为 **`dead-assembly`**（与既有 `dead-rule` 同构命名）。
- `internal/codegraph` 的包边界**禁止打日志、禁止 I/O**（见 `types.go` 与 `check.go` 头注释）。本卡的可观测性落在 `Report` 上，不要引入 logger。
- 注释用中文，写「为什么」不写「做了什么」。

## 文件清单

- 修改：`internal/codegraph/check.go`（`Finding` 文档注释 + `Check` 内两处）
- 修改：`internal/codegraph/check_test.go`（新增一个测试函数，需要补 `strings` import）

---

### Task 1: dead-assembly warn

**Files:**
- Modify: `internal/codegraph/check.go`
- Test: `internal/codegraph/check_test.go`

**Interfaces:**
- Consumes: 既有 `Target.Assembly []string`、`View.Nodes`、`Finding`、`Report`
- Produces: `Report.Warns` 中新增 Kind 取值 `dead-assembly`；无新导出符号

- [ ] **Step 1: 写失败测试**

在 `internal/codegraph/check_test.go` 末尾追加。注意文件顶部 import 目前只有 `"testing"`，要改成 `import ("strings"; "testing")`：

```go
// 组装点死配置：assembly 里写了视图中不存在的文件，必须报 dead-assembly warn。
// 这是与 dead-rule 对称的一条——在此之前 assembly 写错文件名完全没有信号，
// 一条不存在的 "cmd/main.go" 能在基准里躺过整轮而无人发现。
func TestCheckDeadAssembly(t *testing.T) {
	nodes := map[string][2]string{
		"main": {"main", "cmd/main.go"}, "b1": {"b.Facade", "b/f.go"},
	}
	tg := &Target{
		Meta: TargetMeta{Version: 1},
		Domains: []TargetDomain{
			{ID: "d_cmd", Type: "logic", Paths: []string{"cmd/**"}},
			{ID: "d_b", Type: "logic", Paths: []string{"b/**"}},
		},
		Assembly: []string{"cmd/main.go", "cmd/ghost.go"},
	}
	rep := Check(tg, mkView(nodes, [][2]string{{"main", "b1"}}, nil))

	var hits []Finding
	for _, w := range rep.Warns {
		if w.Kind == "dead-assembly" {
			hits = append(hits, w)
		}
	}
	// 恰好一条：存在的 cmd/main.go 不该报，不存在的 cmd/ghost.go 必须报。
	// 断言条数而不只断言「有」，是为了挡住「把所有 assembly 都报一遍」这种实现。
	if len(hits) != 1 {
		t.Fatalf("dead-assembly 应恰好 1 条，实际 %d 条: %+v", len(hits), rep.Warns)
	}
	if !strings.Contains(hits[0].Detail, "cmd/ghost.go") {
		t.Fatalf("dead-assembly 应指向 cmd/ghost.go，实际: %s", hits[0].Detail)
	}
	if len(rep.Fails) != 0 {
		t.Fatalf("dead-assembly 只能是 warn，不能进 fails: %+v", rep.Fails)
	}
}

// 节点被标记 deleted 时，该文件不算「视图里存在」——组装点仍应报死配置。
// 边界条件：deleted 节点只为渲染保留，不代表当前分支里还有这个文件。
func TestCheckDeadAssemblyIgnoresDeletedNodes(t *testing.T) {
	tg := &Target{
		Meta:     TargetMeta{Version: 1},
		Domains:  []TargetDomain{{ID: "d_cmd", Type: "logic", Paths: []string{"cmd/**"}}},
		Assembly: []string{"cmd/gone.go"},
	}
	v := mkView(map[string][2]string{"g": {"main", "cmd/gone.go"}}, nil, nil)
	n := v.Nodes["g"]
	n.Status = "deleted"
	v.Nodes["g"] = n

	rep := Check(tg, v)
	found := false
	for _, w := range rep.Warns {
		if w.Kind == "dead-assembly" {
			found = true
		}
	}
	if !found {
		t.Fatalf("deleted 节点不应让组装点算「命中」，实际 warns: %+v", rep.Warns)
	}
}
```

- [ ] **Step 2: 跑测试确认它是红的**

```bash
go test -run 'TestCheckDeadAssembly' ./internal/codegraph/
```

预期：两个用例都 FAIL，报 `dead-assembly 应恰好 1 条，实际 0 条` 之类。

**如果此时是绿的，停下来报 BLOCKED**——说明这个能力已经存在，或者测试没有真的在验它（假红测试族）。

- [ ] **Step 3: 实现**

改动一：`Check` 开头的归域循环里多收一份全量文件集合。把现有的

```go
	nodeDomain := make(map[string]string, len(v.Nodes))
	outside := map[string]bool{}
	fileHit := map[string]bool{} // 供死规则检测：哪些文件被规则命中过
	for id, n := range v.Nodes {
		if n.Status == "deleted" {
			continue
		}
		d := t.DomainOf(n.File)
```

改成

```go
	nodeDomain := make(map[string]string, len(v.Nodes))
	outside := map[string]bool{}
	fileHit := map[string]bool{}  // 供死规则检测：哪些文件被规则命中过
	allFiles := map[string]bool{} // 供组装点死配置检测：视图里出现过的全部文件（含图外）
	for id, n := range v.Nodes {
		if n.Status == "deleted" {
			continue
		}
		allFiles[n.File] = true
		d := t.DomainOf(n.File)
```

改动二：在既有的 dead-rule 循环**之后**、`sortFindings(rep)` **之前**插入：

```go
	// 组装点死配置：assembly 条目在视图里找不到任何节点文件。
	// 与 dead-rule 对称——在此之前 assembly 写错文件名是零信号：ValidateTarget
	// 完全不看 Assembly，Check 只把它当 set 做 caller 比对，于是一条并不存在的
	// "cmd/main.go" 能在基准里躺过整轮而无人发现（2026-08-21 双轨对照实测）。
	// 只报 warn 不报 fail：扫描未覆盖的入口文件（如当前的 main.go）本就没有节点，
	// 落 fail 会把「基线覆盖不全」误判成「契约违规」。
	for _, f := range t.Assembly {
		if !allFiles[f] {
			rep.Warns = append(rep.Warns, Finding{Kind: "dead-assembly",
				Detail: fmt.Sprintf("组装点 %q 未命中视图中任何节点文件", f)})
		}
	}
```

- [ ] **Step 4: 跑测试确认转绿**

```bash
go test -run 'TestCheckDeadAssembly' ./internal/codegraph/
go test ./internal/codegraph/... ./cmd/
```

预期：全部 PASS。`cmd` 的 `TestRepoContractGate` 仍应 PASS（它只对 `Fails` 报错，warn 只进日志）。

- [ ] **Step 5: 补注释（可观测性）**

本包禁止打日志，可观测性契约是 `Report` 本身，因此这一步的落点是**让新 Finding 自带足够上下文**并把新取值登记进类型文档：

把 `check.go` 顶部 `Finding` 的文档注释里这一行

```go
// warn 侧：legacy（预算内直调计数）/ outside-file（图外文件）/ dead-rule（规则未命中任何节点）
```

改成

```go
// warn 侧：legacy（预算内直调计数）/ outside-file（图外文件）/ dead-rule（规则未命中任何节点）/
// dead-assembly（组装点条目未命中任何节点文件）
```

自查：新 warn 的 `Detail` 必须带上出问题的**具体路径**（上面的实现已带），否则报了等于没报。

- [ ] **Step 6: 跑一次真数据，把行为记进 ledger**

```bash
go run . graph check
```

预期：退出码 **0**（不能变红），warn 数从 27 变成 **28**——多出来的正是 `main.go`（当前 baseline 未扫到根入口文件）。把实际输出的 warn 数与那条 dead-assembly 的原文抄进 ledger。

**如果 warn 数不是 28 或退出码非 0，如实记录实际值，不要替它归因，也不要为了凑数去改 `target.json`。**

- [ ] **Step 7: 提交**

```bash
git add internal/codegraph/check.go internal/codegraph/check_test.go
git commit -m "feat(codegraph): 组装点死配置告警——assembly 写错文件名不再零信号"
```

---

### Task 2: 产出本分支图 diff 视图并做增量对照

**Files:**
- Create: `codegraph/diffs/branch-dead-assembly.json`

**Interfaces:**
- Consumes: `codegraph/baseline.json`（基线）、Task 1 的代码改动
- Produces: 一份分支视图 diff，供 `graph check --view` 与后续（协调者执行的）`graph absorb` 消费

> 这个 task 是本次派发的**主要验证目标**：增量链路（分支扫描 → 合成视图 → 对照）此前只有单元测试，从未在真实分支上跑过。

- [ ] **Step 1: 读扫描配方**

读 `docs/codegraph-scan-recipe.md`，重点是 `### codegraph/diffs/<视图名>.json` 一节的字段表与「硬纪律」一节。**严格按 schema 写**，不要自创字段。

- [ ] **Step 2: 扫描本次触及的文件，写出 diff**

本次只触及 `internal/codegraph/check.go`（`check_test.go` 是测试，按配方测试不入节点、只作为节点的 `tests` 引用）。

**先核一个已经查明的事实，它决定 diff 怎么写**：`codegraph/baseline.json` 里**没有 `internal/codegraph/check.go` 的任何节点**。这份基线是在对照机制合入之前扫的，`check.go` / `target.go` / `absorb.go` 全都不在图里。你可以自己复核：

```bash
python3 -c "import json;g=json.load(open('codegraph/baseline.json'));print([k for k,v in g['nodes'].items() if v['file'].startswith('internal/codegraph/check')])"
```

预期输出 `[]`。所以：

- `check.go` 的函数节点一律进 **`nodesAdded`**，不是 `nodesModified`——基线里没有的东西谈不上「修改」。
- `summary` 必须**如实说明**这批节点是「基线补扫」而非本分支新写的代码：本分支只改了 `Check` 的行为，`inList` / `ruleHitsAny` / `sortFindings` 等是随文件一起首次入图。把这句写进 summary，否则将来 absorb 回灌后没人分得清哪些是本卡的产出。

按配方产出 `codegraph/diffs/branch-dead-assembly.json`：

- `view` 填 `branch:dead-assembly`
- `base` 填本分支起点提交的短号
- `nodesAdded`：`check.go` 里的全部函数节点，按配方给出完整 Node 定义（`file` / `line` / `signature` / `summary` 等）。节点 id 与 container 的命名规范照配方与基线里既有 `internal/codegraph` 节点的写法**保持一致**，不要另起一套。
- `edgesAdded`：这些新节点之间、以及它们指向基线中既有节点的调用边。**`check.go` 的调用全部落在 `internal/codegraph` 包内**，因此预期不产生跨域边。
- 没有内容的字段（`nodesDeleted` / `implementsAdded` 等）**省略即可**，不要写空壳占位。

- [ ] **Step 3: 校验 diff 引用完整性**

```bash
handoff graph validate
```

（若 `handoff` 不在 PATH 里，用 `go run . graph validate`，并把这件事记进 ledger。）

预期：退出码 0。报引用错误就是 diff 里引了不存在的节点 id，修 diff 不要修校验器。

- [ ] **Step 4: 增量对照**

```bash
handoff graph check --view branch:dead-assembly
```

（同上，PATH 里没有就用 `go run . graph check --view branch:dead-assembly`。）

> 说明：执行纪律里「不要调用 handoff CLI」有一条明确例外——只读本地图数据的 `handoff graph` 子命令不在禁令内（它不碰任务、不派发、不触网）。本步骤正是在验证这条例外可用。

预期：退出码 0，`fails` 为空。把 `fails` / `warns` 的实际条数抄进 ledger。

**若报出 fail，不要改 `target.json` 让它变绿**——那是契约变更，属协调者决定。如实记录并继续。

- [ ] **Step 5: 提交**

```bash
git add codegraph/diffs/branch-dead-assembly.json
git commit -m "chore(codegraph): 本分支图 diff 视图 branch:dead-assembly"
```

---

## 不归执行者做的事（协调者本地执行）

- **`handoff graph absorb`**：回灌 baseline 必须在分支合并进 main **之后**做，否则 `meta.commit` 锚点会指向一个未进主线的提交。本次由协调者在合并后本地执行，**执行者不要碰 `codegraph/baseline.json`**。
- 合并决策、`target.json` 的任何改动（即契约变更）。

## 派发前自审

- **验收步骤归谁**：本计划的验收步骤只用到 `handoff graph` 只读子命令，与纪律块的例外一致，不需要执行者驱动 agentd、派发子任务或调用任何任务类 CLI。`absorb` 已显式划归协调者。
- **判据基线**：`go run . graph check` 当前实测退出码 0、warn 27 条（20 legacy + 7 dead-rule），已在 `806ca3ff6` 上跑过；Task 1 Step 6 的「28 条」由此推出（`main.go` 在基线里实测 0 个节点，故必然命中新 warn）。
- **基线覆盖缺口（已核实，写进计划避免执行者踩空）**：`codegraph/baseline.json` 是对照机制合入前扫的，`internal/codegraph/{check,target,absorb}.go` 与 `cmd/graph.go` 的新增部分都不在图里。Task 2 因此走 `nodesAdded` 而非 `nodesModified`。本卡**只补 `check.go` 一个文件**，不扩到整包补扫——那是独立的数据工作，扩进来会让本次派发的验收面失控。
- **序列化边界**：本次新增的是 `Finding.Kind` 的一个新取值，会经 `graphPrintJSON` 出现在 CLI 的 JSON 输出里。**已审查全部消费方**：`Kind` 没有任何穷举 switch 或白名单（grep 既有五个取值，命中的只有 check.go 自身的产出点与文档注释），web 端尚未消费对照结果（spec §9 列为范围外）。因此这次枚举加宽不存在既有白名单接缝。
- **域类型标注**：触及的 `internal/codegraph` 属**契约域，逻辑域**——接缝对面是自有代码，测试可机内闭环，无需真机清单。

## 缺陷族对抗审查

| 族 | 本卡的失败模式 | 处置 |
|---|---|---|
| 静默失败/误导报错 | **本卡要修的就是这一族。** 反向风险：新 warn 自己变成静默——`Detail` 不带路径就等于没报 | Step 5 明确要求 `Detail` 带具体路径；测试断言 `Detail` 含 `cmd/ghost.go` |
| 假红测试 | 测试没有真验到能力，实现前就是绿的 | Step 2 强制先跑到红，绿了就 BLOCKED |
| 门禁绕过 | 新 warn 误入 `Fails` 把闸门打红，或有人为了让它绿去改 `target.json` | 测试显式断言 `len(rep.Fails) == 0`；Task 2 Step 4 明令不许改 target |
| 生命周期/状态机中断 | 不适用：`Check` 是纯函数，无状态无生命周期 | — |
| 跨平台假设 | 路径比对假设正斜杠的仓库相对路径。Windows 上若扫描产出反斜杠会漏报 | **既有 `DomainOf` / `dead-rule` 有完全相同的假设，非本卡引入**。记账不处置，避免把范围扩到路径规范化 |

## 上下文预算

有界文件集：`internal/codegraph/check.go` + `internal/codegraph/check_test.go` + 一个新建的 diff JSON。圈得出，无需竖切。
