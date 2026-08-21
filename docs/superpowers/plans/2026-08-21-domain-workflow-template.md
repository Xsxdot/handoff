# L3「分域开发」内建工作流模板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把分域开发协议（spec §8.1）落成工作台的内建工作流模板：seed 一条 `domain` 工作流 + 三个派发 prompt 模板（拆解/契约冻结/集成），并修掉支撑它的一个引擎缺陷（非审阅裁决节点重跑撞分支名）。

**Architecture:** 全部走既有的「出厂 seed = 数据不是代码」机制——`EnsureDefaultTemplates` / `EnsureDefaultWorkflows` 各加条目，节点行为完全由既有能力开关（Dispatch/Verdict/Gate/OnFail/HumanBases）组合，**不新增任何节点类型或引擎概念**。唯一的引擎改动是把审阅轮已有的「分支按轮次挂号」推广到所有 purpose（Task 1），否则契约冻结/集成节点的第二轮必撞分支名。

**Tech Stack:** Go（database/sql + `s.q()` 占位符 + `s.mutate` 事务模式，slog via `log()`），测试 `go test`。无前端改动（工作流列表与节点编辑器是数据驱动的，新流自动出现）。

---

## 四项分域检查（全局规则 §3 强制，写在此处供审阅回溯）

1. **缺陷族对抗审查**：
   - *静默失败族*：prompt 变量只有 `{{TITLE}}/{{CARD}}/{{ACCEPT}}` 三个被替换，写了别的 `{{X}}` 会**原样送出不报错**——Task 2 的测试加变量白名单断言。seed 幂等靠「同名已存在不覆盖」，升级库中 `domain` 名字缺席 → 会被补种，已验证语义。
   - *门禁绕过族*：`PutWorkflow.validateNodes` 校验模板存在性；seed 顺序已由 `EnsureDefaultWorkflows` 先调 `EnsureDefaultTemplates` 保证（代码注释明写 11 处调用点曾全反）。
   - *假红测试族*：`defaults` 是 map，测试禁止断言迭代顺序，一律按名取。
   - *生命周期族*：无新运行态；节点中断形态与既有引擎一致（agentd 重启在飞集合清空，timeline 可见）。
   - *跨平台族*：无涉及。
2. **序列化边界设问**：本次**无新增 wire 字段**。两处既有投影必须有穿透断言：①`withStatesFromNodes` 把 Nodes 投影成 States/Gates——Task 3 测试走真实写读路径断言投影含聚合闸；②轮次挂号后的分支名要落进 `DispatchSnapshot` 并被 `WorkBranch` 读回——Task 1 测试断言这条链路。
3. **上下文预算检查**：文件集有界——`internal/ledger/{events,templates,workflows}.go` + 对应测试 + `internal/ledgerstep/dispatch.go` + 两份文档。无越界。
4. **域类型标注**：全部**逻辑域**（ledger store / ledgerstep，接缝对面是自有代码），测试机内闭环。真机走查（隔离 agentd + 控制台）**归审核者，不在派发范围**（见文末）。

---

## 背景速览（给零上下文执行者）

- 工作流与模板是**版本化数据**：`internal/ledger/workflows.go` 的 `EnsureDefaultWorkflows` 与 `internal/ledger/templates.go` 的 `EnsureDefaultTemplates` 幂等 seed，同名已存在不覆盖。
- 节点语义（`internal/ledgerstep/node.go` `RunOnce`）：`Dispatch` 无 `Verdict` = 派完即止、**不自动移列**（人工移动=拍板）；`Verdict` 通过后 `routeTo(Next)`，**目标列 Gate 拦住时优雅转等人**（不是错误）——聚合闸直接挂在集成列上即可。
- 分支名拼法（`internal/ledgerstep/dispatch.go`）：`<BranchPrefix>/<卡ID>-<Purpose>`。purpose 除 `review` 有特殊语义（复用工作分支+轮次后缀）外自由取值。三个新模板 purpose 互异（`breakdown`/`ticket0`/`integration`），各自成分支名。
- `WorkBranch` 返回**最后一次非 review 派发**的分支 → 终审节点自动审到集成分支，无需改动。
- 子卡聚合闸 `Gate.RequireChildrenDone`、递归深度护栏 `maxWorkflowNesting=3`、环检测均已存在（B156 已合入），本 plan 只是用它们。

---

### Task 1: 非审阅裁决节点的重跑分支挂号

**Files:**
- Modify: `internal/ledger/events.go`（`ReviewRounds` 附近，~line 430）
- Modify: `internal/ledgerstep/dispatch.go`（`ViaTemplate` 分支拼名处，~line 105-127）
- Test: `internal/ledger/events_test.go`、`internal/ledgerstep/dispatch_test.go`

**为什么**：`cards/<卡>-<purpose>` 是固定拼法。同一张卡从同一 purpose 模板第二次派发时，目标机上第一轮分支还在，git 拒绝创建同名分支（审阅轮 2026-08-19 真机实测过 `fatal: a branch named ... already exists`，当时只给 review 修了）。`domain` 流的契约冻结（MaxRounds 2）与集成（MaxRounds 2）都可能重跑，不修则第二轮必死。

- [ ] **Step 1: 写失败测试（ledger 侧 PurposeRounds）**

在 `internal/ledger/events_test.go` 末尾追加：

```go
// TestPurposeRoundsCountsPerPurpose 轮数按 purpose 分开数——分支挂号靠它，
// 混数会让不同节点的重跑互相污染编号。
func TestPurposeRoundsCountsPerPurpose(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "数轮次")
	_ = s.LinkTask(c.ID, "acc", "T-1", "ticket0", "test")
	_ = s.LinkTask(c.ID, "acc", "T-2", "ticket0", "test")
	_ = s.LinkTask(c.ID, "acc", "T-3", PurposeReview, "test")
	for purpose, want := range map[string]int{"ticket0": 2, PurposeReview: 1, "integration": 0} {
		got, err := s.PurposeRounds(c.ID, purpose)
		if err != nil {
			t.Fatalf("PurposeRounds(%s): %v", purpose, err)
		}
		if got != want {
			t.Fatalf("purpose %s 应数出 %d，实得 %d", purpose, want, got)
		}
	}
}
```

（`seedStore`/`mk` 是 events_test.go 既有 helper；若签名不符以文件内现状为准，只改调用形态、不改断言。）

- [ ] **Step 2: 跑测确认红**

Run: `go test ./internal/ledger -run TestPurposeRoundsCountsPerPurpose -v`
Expected: FAIL（`s.PurposeRounds undefined`，编译错即为红）

- [ ] **Step 3: 实现 PurposeRounds，ReviewRounds 委托给它**

`internal/ledger/events.go`，替换现有 `ReviewRounds`（保留其注释里「与 CountRounds 不是一回事」的说明，挪到 PurposeRounds 上）：

```go
// PurposeRounds 数该卡已派出的指定 purpose 轮数（只增不减，用于给重跑轮的
// 分支编号，避免 <prefix>/<卡>-<purpose> 固定拼法第二轮撞名）。
// 与 ledgerstep 的 CountRounds 不是一回事：那个数的是「裁决回合」（人工重置
// 会清零，用于封顶），这个数的是「派过几次」（只增不减，用于起名）。
func (s *Store) PurposeRounds(cardID, purpose string) (int, error) {
	links, err := s.TasksOf(cardID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, link := range links {
		if link.Purpose == purpose {
			count++
		}
	}
	return count, nil
}

// ReviewRounds 已派出的审阅轮数——PurposeRounds 在 review 上的特例，保留旧名
// 不动既有调用方。
func (s *Store) ReviewRounds(cardID string) (int, error) {
	return s.PurposeRounds(cardID, PurposeReview)
}
```

- [ ] **Step 4: 跑测确认绿 + 既有测试不红**

Run: `go test ./internal/ledger -run "TestPurposeRounds|TestWorkBranch|ReviewRounds" -v`
Expected: PASS 全部

- [ ] **Step 5: 写失败测试（dispatch 侧分支挂号）**

在 `internal/ledgerstep/dispatch_test.go` 末尾追加（`fmt` 若未导入则补）：

```go
// TestViaTemplateSecondRoundGetsNumberedBranch 非审阅模板重跑时分支按轮次
// 挂号：首轮无后缀（存量 cards/<卡>-implement 命名不变），第二轮 -2。
// 尾部断言穿序列化边界：挂号分支要落进 dispatched 快照并被 WorkBranch 读回，
// 否则终审会审到第一轮的旧分支。
func TestViaTemplateSecondRoundGetsNumberedBranch(t *testing.T) {
	st, card := dispatchTestCard(t)
	var branches []string
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		branches = append(branches, opts.Branch)
		return fmt.Sprintf("T-impl-%d", len(branches)), nil
	}}
	for i := 0; i < 2; i++ {
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("第 %d 轮 ViaTemplate: %v", i+1, err)
		}
	}
	want := []string{"cards/" + card.ID + "-implement", "cards/" + card.ID + "-implement-2"}
	for i := range want {
		if branches[i] != want[i] {
			t.Fatalf("第 %d 轮分支应为 %q，实得 %q", i+1, want[i], branches[i])
		}
	}
	wb, err := st.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if wb != want[1] {
		t.Fatalf("WorkBranch 应读回最新挂号分支 %q，实得 %q", want[1], wb)
	}
}
```

- [ ] **Step 6: 跑测确认红**

Run: `go test ./internal/ledgerstep -run TestViaTemplateSecondRound -v`
Expected: FAIL（第二轮分支仍是无后缀名）

- [ ] **Step 7: 实现挂号**

`internal/ledgerstep/dispatch.go`，在 `if tpl.Def.Purpose == ledger.PurposeReview { ... }` 块后加 `else` 分支：

```go
	} else {
		// 非审阅节点的重跑同样会撞分支名——同一张卡第二次从同 purpose 模板
		// 派发时，目标机上第一轮分支还在，git 拒绝创建同名分支（与审阅轮
		// 2026-08-19 真机实测同一形态）。解法与审阅一致：按「同 purpose 已派
		// 几次」挂号。首轮保持无后缀，存量卡的分支命名不变。
		rounds, err := d.St.PurposeRounds(c.ID, tpl.Def.Purpose)
		if err != nil {
			return zero, fmt.Errorf("取 %s 轮次: %w", tpl.Def.Purpose, err)
		}
		if rounds > 0 {
			branch = fmt.Sprintf("%s-%d", branch, rounds+1)
			slog.Default().Info("重跑轮分支挂号", "card", c.ID,
				"purpose", tpl.Def.Purpose, "round", rounds+1, "branch", branch)
		}
	}
```

- [ ] **Step 8: 跑测确认绿 + 两包全量**

Run: `go test ./internal/ledgerstep ./internal/ledger`
Expected: PASS（既有审阅挂号测试必须仍绿——review 走的是原路径，未动）

- [ ] **Step 9: 自检日志与注释**（instrumenting-code 清单）
  - 挂号动作有 Info 日志（Step 7 已含，带 card/purpose/round/branch 上下文）
  - `PurposeRounds` 错误路径带上下文（`TasksOf` 的错误已含卡 ID）
  - 两个新导出方法有 doc 注释、非显然分支有「为什么」注释（上方代码已含）

- [ ] **Step 10: Commit**

```bash
git add internal/ledger/events.go internal/ledger/events_test.go internal/ledgerstep/dispatch.go internal/ledgerstep/dispatch_test.go
git commit -m "fix(ledgerstep): 非审阅裁决节点重跑分支按 purpose 轮次挂号，防第二轮撞名"
```

---

### Task 2: 分域三模板 seed（含实现类裁决输出契约）

**Files:**
- Modify: `internal/ledger/templates.go`（`reviewVerdictContract` 之后加常量；`EnsureDefaultTemplates` 的 defaults map 加三项）
- Test: `internal/ledger/templates_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/ledger/templates_test.go` 末尾追加：

```go
// TestDefaultDomainTemplates 分域三模板的 seed 形状。purpose 必须互异——
// 分支名由 purpose 拼出，相同会在同一张卡上撞名。变量白名单断言防静默失败：
// prompt 里写了不受支持的 {{X}} 不会报错，会原样送到执行者面前。
func TestDefaultDomainTemplates(t *testing.T) {
	s := newTestStore(t) // store_test.go 的公共构造
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ discipline, purpose string }{
		"domain-breakdown":   {"spec-draft", "breakdown"},
		"domain-ticket0":     {"implement", "ticket0"},
		"domain-integration": {"implement", "integration"},
	}
	for name, want := range cases {
		tpl, err := s.GetTemplate(name, 0)
		if err != nil {
			t.Fatalf("取 %s: %v", name, err)
		}
		if tpl.Def.Executor != "codex" || tpl.Def.BranchPrefix != "cards" {
			t.Fatalf("%s 执行者/分支前缀不对: %+v", name, tpl.Def)
		}
		if tpl.Def.Discipline != want.discipline || tpl.Def.Purpose != want.purpose {
			t.Fatalf("%s 角色/purpose 不对: 想要 %+v 实得 %s/%s",
				name, want, tpl.Def.Discipline, tpl.Def.Purpose)
		}
		stripped := strings.NewReplacer(
			"{{TITLE}}", "", "{{CARD}}", "", "{{ACCEPT}}", "").Replace(tpl.Def.Prompt)
		if strings.Contains(stripped, "{{") {
			t.Fatalf("%s prompt 含不受支持的模板变量（会原样送出）:\n%s", name, tpl.Def.Prompt)
		}
	}
	// 带 Verdict 节点引用的两个模板必须携带裁决输出契约，否则报文里没有
	// handoff-verdict block，节点永远解析失败转等人。
	for _, name := range []string{"domain-ticket0", "domain-integration"} {
		tpl, _ := s.GetTemplate(name, 0)
		if !strings.Contains(tpl.Def.Prompt, "handoff-verdict") {
			t.Fatalf("%s 缺裁决输出契约", name)
		}
	}
	// 拆解节点不裁决，prompt 里出现契约会诱导 spec-draft 角色多输出一个假裁决块。
	tpl, _ := s.GetTemplate("domain-breakdown", 0)
	if strings.Contains(tpl.Def.Prompt, "handoff-verdict") {
		t.Fatal("domain-breakdown 不该带裁决契约")
	}
}
```

（import 需含 `strings`；若文件的 store 构造惯例不同，以 `TestTemplateVersioningAndDefaults` 的现状为准。）

- [ ] **Step 2: 跑测确认红**

Run: `go test ./internal/ledger -run TestDefaultDomainTemplates -v`
Expected: FAIL（`模板 domain-breakdown v0: 未找到`）

- [ ] **Step 3: 实现——常量与三模板**

`internal/ledger/templates.go`，在 `reviewVerdictContract` 之后追加：

```go
// implVerdictContract 实现类裁决节点（契约冻结/集成）的输出契约。与
// reviewVerdictContract 的区别只在收尾：实现节点要正常 commit，收尾行按
// 纪律块输出 branch/commit/summary；裁决块写在同一条最终报文里、收尾行之前
// （ParseVerdict 全文扫 fenced block 取最后一个，两者共存不冲突）。
const implVerdictContract = "\n回合结束时，在最终报文里输出你的自检裁决，格式为一个 fenced code block，" +
	"语言标记 handoff-verdict，内容是 JSON：\n" +
	"```handoff-verdict\n" +
	`{"verdict":"pass"或"fail","findings":[{"severity":"major"或"minor","summary":"一句话","file":"可选路径"}],"notes":"可选"}` +
	"\n```\n" +
	"只输出一个该 block；解析不到会转人工，不要省略。\n" +
	"pass 的唯一依据是你真实跑到的结果（编译/测试输出原文）；没跑到结果不许写 pass。\n" +
	"你正常 commit；收尾行照纪律块输出 branch/commit/summary，裁决块放在收尾行之前的同一条报文里。"

// 分域开发协议的三个节点 prompt。协议正文见
// docs/superpowers/specs/2026-08-21-domain-partitioned-dev-protocol-design.md，
// 这里是协议 §7.0/§7.2/§7.4 的执行者视角改写——改协议先改 spec 再同步这里。
const domainBreakdownPrompt = "你是「分域开发」流程的拆解执行者。对卡 {{CARD}}（{{TITLE}}）做契约先行拆解，产出一份拆解文档，不写实现代码。\n" +
	"验收判据：{{ACCEPT}}\n\n" +
	"先读项目的实例化清单（契约层声明、领域清单与域文档、缺陷族清单；入口在项目文档目录或卡附件里；找不到就发工单问，不要凭目录结构猜域边界）。\n\n" +
	"拆解文档写进 docs/superpowers/specs/，文件名含卡号与 breakdown 字样，必含五节：\n" +
	"1. 触及域清单：每个域标类型——逻辑域（接缝对面是自有代码）/边界域（接缝对面是外部现实：目标机、PTY、webview、真实 git 等）。\n" +
	"2. 各域契约增量：契约层的精确 diff 提案，精确到可编译（类型/接口/字段签名写全）。\n" +
	"3. 子卡清单 + 依赖 DAG：每张子卡四段式——①契约引用 ②意图与为什么（不写实现步骤）③验收（逻辑域=域内测试闭环、邻域 mock 契约层接口；边界域=机内只验契约形状，行为验收写明「真机清单，归审核者执行」）④入口指针（相关文件路径）。\n" +
	"4. 缺陷族对抗审查：对每个触及域，按项目缺陷族清单逐族设问（没有项目清单时用通用五族：生命周期/状态机中断、静默失败/误导报错、跨平台假设、假红测试、门禁绕过），结论写进对应子卡的验收栏。凡新增数据字段，逐条回答序列化边界设问：从产生到消费之间每一处手写序列化/投影（手搭 map、DTO 转换、CLI 拼输出、跨语言契约另一侧）列进子卡文件清单并要求断言——「两端各自有测试」不等于「这条链路有测试」。\n" +
	"5. 上下文预算检查：每张子卡必须能圈出有界文件集；圈不出的域，先列竖切还债卡，再列功能卡。\n\n" +
	"红线：不写实现代码；不建卡、不派发、不调 handoff CLI——扇出归审核者。契约增量是提案不是决定，拍板在人。"

const domainTicket0Prompt = "你是「分域开发」流程的契约冻结执行者。卡 {{CARD}}（{{TITLE}}）的契约增量已由人拍板（见卡的 contract 附件与拆解文档）。\n" +
	"验收判据：{{ACCEPT}}\n\n" +
	"只做一件事：把拍板过的契约增量落地为可编译的骨架 commit——契约层 diff + 空实现 stub（返回零值或按项目惯例显式未实现）。涉及域的领域文档条目一并更新。\n" +
	"红线：不夹带任何实现逻辑——本 commit 之后所有域子卡基于它开工，你多写的每一行实现都在挤占域执行者的裁量。编译必须绿；既有测试不许转红。"

const domainIntegrationPrompt = "你是「分域开发」流程的集成执行者。卡 {{CARD}}（{{TITLE}}）的全部域子卡已完结，各域分支已合入本卡基线。\n" +
	"验收判据：{{ACCEPT}}\n\n" +
	"做两件事：\n" +
	"1. 调配层接线：跨域协作逻辑只允许出现在调配层；发现子卡里有越界的跨域逻辑，记进报文，不要顺手扩散。\n" +
	"2. 端到端测试：按拆解文档的验收判据补 e2e，全量测试跑绿。\n" +
	"边界域的真机清单不归你跑，在报文里列出留给审核者。"
```

`EnsureDefaultTemplates` 的 defaults map 加三项：

```go
		"domain-breakdown": {
			Executor: "codex", Purpose: "breakdown", BranchPrefix: "cards",
			// spec-draft 角色：只出文档不写代码，岔口发工单问——拆解的产出
			// 本来就是提案，拍板在人。
			Discipline: discipline.NameSpecDraft,
			Prompt:     domainBreakdownPrompt,
		},
		"domain-ticket0": {
			Executor: "codex", Purpose: "ticket0", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     domainTicket0Prompt + implVerdictContract,
		},
		"domain-integration": {
			Executor: "codex", Purpose: "integration", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     domainIntegrationPrompt + implVerdictContract,
		},
```

- [ ] **Step 4: 跑测确认绿**

Run: `go test ./internal/ledger -run "TestDefaultDomainTemplates|TestDefaultTemplatesUseNames|TestTemplateVersioningAndDefaults" -v`
Expected: PASS 全部（既有默认模板测试若断言了模板总数，按现状加 3）

- [ ] **Step 5: 自检日志与注释**
  - seed 成功路径已有 `log().Info("seed 默认派发模板", ...)`，覆盖新模板，无需新增
  - 三个 prompt 常量与 implVerdictContract 均有「为什么」注释（Step 3 已含）

- [ ] **Step 6: Commit**

```bash
git add internal/ledger/templates.go internal/ledger/templates_test.go
git commit -m "feat(ledger): seed 分域三模板（拆解/契约冻结/集成）与实现类裁决输出契约"
```

---

### Task 3: `domain` 工作流 seed

**Files:**
- Modify: `internal/ledger/workflows.go`（`EnsureDefaultWorkflows` 的 defaults map）
- Test: `internal/ledger/workflows_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/ledger/workflows_test.go` 末尾追加（store 构造惯例以文件内现状为准）：

```go
// TestDefaultDomainWorkflow domain 流的 seed 形状：节点序列、三道闸、
// 能力开关矩阵。States/Gates 断言走真实写读路径——它们是 Nodes 的投影，
// 投影断了看板列渲染与 MoveCard 校验就断了（序列化边界检查）。
func TestDefaultDomainWorkflow(t *testing.T) {
	s := seedStore(t) // cards_test.go 的公共构造：newTestStore + EnsureDefaultWorkflows
	wf, err := s.GetWorkflow("domain", 0)
	if err != nil {
		t.Fatalf("取 domain 流: %v", err)
	}
	wantStates := []string{StatusTodo, "拆解", "契约冻结", "域实现", "集成", "终审", StatusDone}
	if len(wf.Def.States) != len(wantStates) {
		t.Fatalf("States 应为 %v，实得 %v", wantStates, wf.Def.States)
	}
	for i, want := range wantStates {
		if wf.Def.States[i] != want {
			t.Fatalf("States[%d] 应为 %q，实得 %q", i, want, wf.Def.States[i])
		}
	}
	nodes := map[string]NodeDef{}
	for _, n := range wf.Def.Nodes {
		nodes[n.Name] = n
	}
	// 拆解：只派发不裁决——人工移动到契约冻结即拍板动作。
	breakdown := nodes["拆解"]
	if !breakdown.Dispatch || breakdown.Verdict || breakdown.Template != "domain-breakdown" {
		t.Fatalf("拆解节点形状不对: %+v", breakdown)
	}
	// 契约冻结：进入门槛 = contract 附件（投影到 Gates 也要在——真实读路径断言）。
	freeze := nodes["契约冻结"]
	if freeze.Gate.RequireAttachment != "contract" || !freeze.Verdict || freeze.Template != "domain-ticket0" {
		t.Fatalf("契约冻结节点形状不对: %+v", freeze)
	}
	if wf.Def.Gates["契约冻结"].RequireAttachment != "contract" {
		t.Fatal("契约冻结的闸没投影进 Gates（看板 MoveCard 校验读的是这里）")
	}
	// 域实现：纯人工列——扇出子卡是驱动 handoff 自身的操作，归协调者。
	if nodes["域实现"].Dispatch {
		t.Fatal("域实现必须是纯人工列")
	}
	// 集成：聚合闸 + 裁决未过退回域实现。
	integ := nodes["集成"]
	if !integ.Gate.RequireChildrenDone || integ.OnFail != "域实现" || integ.Template != "domain-integration" {
		t.Fatalf("集成节点形状不对: %+v", integ)
	}
	if !wf.Def.Gates["集成"].RequireChildrenDone {
		t.Fatal("聚合闸没投影进 Gates")
	}
	// 终审：验收判据闸 + main 人工门 + finishing 覆盖，形状与 feature 待合并一致。
	final := nodes["终审"]
	if !final.Gate.RequireAcceptance || len(final.HumanBases) == 0 || final.HumanBases[0] != "main" {
		t.Fatalf("终审节点形状不对: %+v", final)
	}
	if final.Override.Discipline != "finishing" || final.Template != "review-generic" {
		t.Fatalf("终审应复用 review-generic + finishing 覆盖: %+v", final)
	}
}

// TestEnsureDefaultsKeepsUserDomainWorkflow 用户自建的同名流不被 seed 覆盖。
func TestEnsureDefaultsKeepsUserDomainWorkflow(t *testing.T) {
	s := newTestStore(t) // 不先 Ensure——用户的流要抢在 seed 之前写入
	if _, err := s.PutWorkflow("domain", WorkflowDef{States: []string{"甲", "乙"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	wf, err := s.GetWorkflow("domain", 0)
	if err != nil {
		t.Fatal(err)
	}
	if wf.Version != 1 || len(wf.Def.States) != 2 {
		t.Fatalf("用户的 domain 流被覆盖了: v%d %v", wf.Version, wf.Def.States)
	}
}
```

（`seedStore`/`newTestStore` 是 cards_test.go / store_test.go 的既有公共构造，已核对存在。）

- [ ] **Step 2: 跑测确认红**

Run: `go test ./internal/ledger -run "TestDefaultDomainWorkflow|TestEnsureDefaultsKeepsUserDomainWorkflow" -v`
Expected: FAIL（`工作流 domain v0: 未找到`；第二条在 seed 覆盖前也应因取不到而红——若它意外绿，先查 PutWorkflow 是否被 states-only def 拒绝）

- [ ] **Step 3: 实现——defaults map 加 domain 流**

`internal/ledger/workflows.go` `EnsureDefaultWorkflows` 的 defaults map 加：

```go
		// domain：分域开发协议（docs/superpowers/specs/2026-08-21-domain-
		// partitioned-dev-protocol-design.md §8.1）的执行形态。节点归属遵循
		// 工作台基准 §5：拆解草案与代码执行归执行者，拍板/扇出/合并归人。
		"domain": {
			Nodes: []NodeDef{
				{Name: StatusTodo, Next: "拆解"},
				// 拆解：只派发不裁决。产出（域清单/契约增量/子卡清单）的拍板
				// 归人——人工把卡移进契约冻结这一步就是拍板动作，附上拍板过
				// 的契约（kind=contract）才能过下一列的闸。
				{Name: "拆解", Next: "契约冻结",
					Dispatch: true, Template: "domain-breakdown", CarryCardContext: true},
				// 契约冻结：把拍板过的契约落成可编译骨架 commit。重跑分支已
				// 按 purpose 轮次挂号（Task 1），MaxRounds 2 不会撞分支名。
				{Name: "契约冻结", Next: "域实现",
					Gate:     Gate{RequireAttachment: "contract"},
					Dispatch: true, Verdict: true, Template: "domain-ticket0",
					CarryCardContext: true, MaxRounds: 2},
				// 域实现：纯人工列。扇出子卡是驱动 handoff 自身的操作（纪律块
				// 对执行者禁止），归协调者；子卡各绑自己的工作流并行走。
				{Name: "域实现", Next: "集成"},
				// 集成：聚合闸拦到全部直接子卡完结；裁决未过退回域实现补卡。
				{Name: "集成", Next: "终审", OnFail: "域实现",
					Gate:     Gate{RequireChildrenDone: true},
					Dispatch: true, Verdict: true, Template: "domain-integration",
					CarryCardContext: true, MaxRounds: 2},
				// 终审：整分支审阅 + 收尾合并，与 feature 流「待合并」同形；
				// 基线是 main 时不自动执行——外部可见动作留人工门。
				{Name: "终审", Next: StatusDone,
					Gate:     Gate{RequireAcceptance: true},
					Dispatch: true, Verdict: true, Template: "review-generic",
					Override:         NodeOverride{Discipline: discipline.NameFinishing},
					CarryCardContext: true, MaxRounds: 1,
					HumanBases: []string{"main"}},
				{Name: StatusDone},
			},
		},
```

- [ ] **Step 4: 跑测确认绿 + 包全量**

Run: `go test ./internal/ledger`
Expected: PASS（若既有测试断言了默认工作流总数/名单，按现状加 `domain`）

- [ ] **Step 5: 自检日志与注释**
  - seed 路径已有 `log().Info("seed 默认工作流", ...)` 与 `dispatch_nodes` 计数日志，自动覆盖新流
  - 每个节点的「为什么」注释已在 Step 3 代码中

- [ ] **Step 6: Commit**

```bash
git add internal/ledger/workflows.go internal/ledger/workflows_test.go
git commit -m "feat(ledger): seed 内建 domain 工作流——分域开发协议 §8.1 的执行形态"
```

---

### Task 4: 全量回归 + 文档对齐

**Files:**
- Modify: `docs/superpowers/specs/2026-08-21-domain-partitioned-dev-protocol-design.md`（§8.1 表）
- Modify: `docs/superpowers/specs/2026-08-21-workbench-workflow-baseline.md`（§2 表、§6 缺口清单）

- [ ] **Step 1: 全量测试 + gofmt**

Run: `go test ./... && gofmt -l .`
Expected: 全绿；gofmt 无输出（审核惯例必查项，测试绿 ≠ 格式干净）

- [ ] **Step 2: spec §8.1 对齐**

把 §8.1 的节点表替换为实现后的真实形态（拆解与契约冻结拆成两节点，中间人工拍板；下表照抄）：

```markdown
| 节点 | 能力配置 | 说明 |
|---|---|---|
| 拆解 | Dispatch=on（模板=domain-breakdown，spec-draft 角色）、无 Verdict | 产出域清单/契约增量/子卡清单提案；**人工把卡移进下一列即拍板**，Decision 闭环挂此节点 |
| 契约冻结 | Gate=contract 附件、Dispatch+Verdict（模板=domain-ticket0，判据=stub 编译绿）、MaxRounds 2 | 拍板过的契约落成可编译骨架 commit |
| 域实现 | 纯人工列 | 扇出子卡归协调者（自指红线）；子卡各绑自己的工作流并行走 |
| 集成 | Gate=聚合闸（全部直接子卡完结）、Dispatch+Verdict（模板=domain-integration）、OnFail=域实现 | 调配层接线 + e2e |
| 终审 | Gate=验收判据、Dispatch+Verdict（review-generic + finishing 覆盖）、HumanBases=[main] | 合并决策留人 |
```

并在表下补一行：「2026-08-21 落地时与首版的差异：拆解/契约冻结拆为两节点，人工拍板（移列+contract 附件）显式插在中间——归属依据见工作台基准 §5。」

- [ ] **Step 3: 基准文档对齐**

`2026-08-21-workbench-workflow-baseline.md`：
- §2 表 L3 行「现状」列：`🔨 协议 spec §8.1，模板待落地` → `✅ 内建 domain 流`
- §6 缺口清单「L3 分域流模板」行：`❌` → `✅ 内建 domain 流 + 三模板（domain-breakdown/ticket0/integration）`
- §6 缺口清单「子卡扇出/聚合闸/递归护栏」行若仍标实现中：改 `✅ 已合入`

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/specs/2026-08-21-domain-partitioned-dev-protocol-design.md docs/superpowers/specs/2026-08-21-workbench-workflow-baseline.md
git commit -m "docs(specs): 分域协议 §8.1 与工作台基准对齐 domain 流落地形态"
```

---

## 审核者本地验收清单（不派发——需要驱动 handoff 自身）

> 以下归审核者在本地执行，**不写进任何派发任务**（与纪律块「不调 handoff CLI」冲突）：

1. 起隔离 agentd（独立 DataDir + 端口，DataDir 放 /tmp 短路径），确认控制台工作流列表出现 `domain`，节点编辑器能打开它、三道闸勾选状态正确。
2. 建一张 domain 卡走形状验证：拆解列可派发（或 dry 形态确认按钮在）；无 contract 附件时移入契约冻结被拒；挂 contract 附件后可移入；子卡未完结时移入集成被聚合闸拦、报错文案可读。
3. 变异复验至少一处：把 seed 里集成节点的 `RequireChildrenDone` 临时改 false，确认 Task 3 测试转红，改回复绿。
