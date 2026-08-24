# B230 实施计划（实况回填）

- **卡**：B230　**级别**：L2　**spec**：`docs/superpowers/specs/2026-08-24-b230-factory-seed-teardown.md`
- **状态**：第 0 节已实现并合入卡基线；Task 1–5 待做
- **由来**：本文档由协调者**回填**。plan 轮（task `0eae5e21`）越轨——未产出计划文档，
  直接把实现干完（commit `277d780f` + `e07c821f`）。implement 列的门以「缺 plan 附件」
  拦下，门是对的。按 product-backlog 越轨条款：协调者逐行审 → 人工裁决 → 回填实况
  plan 补门 → 卡上记越轨（取证与 findings 见卡 seq 1930）。
- **基线**：`claude/kai-b230-da3f0d`，已 ff 合入 plan 轮的两个提交。**下一轮从此分支起步**
  ——原任务分支 `cards/B230-charter` 的内容已在其中，不要再从旧基线拉。

## 第 0 节：已实现（实况记录，非待办）

逐行审过，符合 spec，**不要重做**：

| 文件 | 已做什么 |
|---|---|
| `internal/ledger/workflows.go` | 删 `EnsureDefaultWorkflows`（-125 行，含 4 条出厂流 defaults 与补版逻辑）|
| `internal/ledger/templates.go` | 删 `EnsureDefaultTemplates`（-72 行，含 5 个出厂模板与 legacy 升级逻辑）|
| `internal/ledger/cards.go` | `prepareCard` 缺省流解析改为三态：零流报错指路 / 唯一流自动取 / 多流报错并列出可选流名；四处结构化日志 |
| `cmd/card_dispatch.go` | 新增 `resolveCardDispatchTemplate`，模板缺省同构三态；`--template` 缺省值由 `feature-impl` 改为空 |
| `cmd/workflow.go` | 删「出厂三条恒在」硬编码，全量走 `ListWorkflowNames` |
| `cmd/agentd.go` `cmd/ledgercli.go` | 删 4 处 seed 调用点 |
| `cmd/card.go` `cmd/card_import.go` | `--workflow` 帮助文案改为「空=账本唯一流自动解析」|
| 12 个 `_test.go` + 3 个新夹具文件 | 依赖种子的测试改为显式夹具 |

**已核**：`--step` 分支在 `RunE` 里于模板解析**之前** `return runStepDispatch(...)`，
所以工作流节点派发不受模板三态影响（曾疑为致命回归，实测证伪）。

**基线复核**（2026-08-24 本机实测，`/tmp/b230-verify` 独立 worktree 检出 `e07c821f`）：
`gofmt -l .` 无输出；`go build ./...`、`go vet ./...` 通过；
`ledger` / `ledgerstep` / `discipline` / `cmd` / `proto` / `ledgermirror` 六包 ok；
**`internal/agentd` FAIL** —— 即 Task 1。

## Task 1：修 `TestMigrateAPIProjectsFromTo`（本轮引入的真红）

**现象**（基线实测原文）：

    --- FAIL: TestMigrateAPIProjectsFromTo (0.03s)
        ledgerapi_test.go:215: 建卡: 建卡缺少工作流：账本中有多条工作流，请显式指定 --workflow（可选：bug、domain、feature、triage）

**根因**：`internal/agentd/ledgerapi_test.go:213` 建卡时不给 `Workflow`，撞上本轮新增的
多流歧义拒绝——`seedAgentdLedger` 装了 4 条流。这是本轮改动自己引入的，不是环境红。

**步骤**：

1. 跑红固化现场：`go test ./internal/agentd/ -run TestMigrateAPIProjectsFromTo`，
   确认失败原文与上面一致（**先跑红再改**，避免改的是另一条路径）。
2. 改 `internal/agentd/ledgerapi_test.go:213`：

   ```go
   // 显式声明起点流：本轮起账本不再有出厂流，缺省解析在多流时按歧义拒绝。
   // 取 triage 而非 bug，让 migrate 的 from/to 落在两条不同的流上——
   // 本测试断言的正是 from/to 投影，同流同列会让断言失去分辨力。
   card, err := env.ledger.CreateCard(ledger.NewCard{
       Title: "迁移投影", Project: "p", Workflow: "triage", Actor: "test",
   })
   ```

3. 跑绿：`go test ./internal/agentd/ -run TestMigrateAPIProjectsFromTo`。
4. **全包复跑**：`go test ./internal/agentd/...`——同一包里可能不止这一处裸建卡。
   还有失败的按同一手法逐个显式化，**不要**改回缺省行为来迁就测试。
5. 提交。

**验收**：`go test ./internal/agentd/...` 全绿。

## Task 2：删净方法论正文（spec 的「不预设方法论」没做完）

**问题**：种子函数删了，但它们的正文还留在生产文件 `internal/ledger/templates.go` 里，
生产代码**零引用**（已 grep 核实），只有测试夹具在用——等于方法论正文换了个调用者继续活着。

**要删的 7 个包级常量**（`internal/ledger/templates.go`，行号为 `e07c821f` 读数）：

| 常量 | 行 |
|---|---|
| `reviewVerdictContract` | 145 |
| `legacyReviewVerdictContract` | 158 |
| `implVerdictContract` | 171 |
| `legacyImplVerdictContract` | 183 |
| `domainBreakdownPrompt` | 195 |
| `domainTicket0Prompt` | ~205 |
| `domainIntegrationPrompt` | ~215 |

**步骤**：

1. `grep -rn '<常量名>' --include='*.go' internal cmd` 逐个确认生产侧零引用
   （测试侧命中不算引用者——它们由 Task 2 步骤 2 一并处理）。
2. 改 `internal/ledger/test_fixtures_test.go` 的 `seedTestTemplates`：把 5 个模板的
   `Prompt` 换成**最小假数据**，形如 `"实现 {{TITLE}}：{{ACCEPT}}"`（照抄同仓
   `internal/agentd/ledger_fixtures_test.go` 的 `seedAgentdLedger` 写法）。
   同时删掉其中的 legacy 升级分支（`reflect.DeepEqual` + legacy 识别）——那是出厂
   种子的存量兼容逻辑，测试夹具没有存量。
   **理由写进注释**：测试验的是「模板能存能取能组装」，不是「domain 方法论正文是什么」；
   夹具带着真实方法论正文，就是让测试替一个已不存在的世界作证。
3. 同理修 `internal/ledger/templates_test.go` 里引用这些常量的两处（74、79 行）。
4. 删掉 `internal/ledger/templates.go` 的 7 个常量及其注释块。
5. `go build ./... && go vet ./...`——常量若还有漏网引用，这一步会红。
6. 跑：`go test ./internal/ledger/...`。
7. 提交。

**验收**：`grep -rn 'domainBreakdownPrompt\|domainTicket0Prompt\|domainIntegrationPrompt\|VerdictContract' --include='*.go' internal cmd` 零命中；`go test ./internal/ledger/...` 全绿。

## Task 3：补 spec 判据第 3 条要求的零断言（当前完全缺失）

spec 判据明文要求「有测试断言『新开 Store 后账本零工作流、零模板』」。
全仓 `ListTemplateNames` 在测试里零命中——这条断言根本没写。它是本卡的**核心判据**：
没有它，将来谁把 seed 加回来都不会有测试拦。

**新增** `internal/ledger/seed_teardown_test.go`：

```go
package ledger

import "testing"

// TestOpenInstallsNoSeeds 锁死 B230 的核心结论：打开账本只建 schema，
// 不注入任何工作流与模板。handoff 是通用派发引擎，出厂不预设方法论——
// 谁把 seed 加回来，这条会红。
func TestOpenInstallsNoSeeds(t *testing.T) {
	st, err := Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatalf("打开账本: %v", err)
	}
	defer st.Close()

	workflows, err := st.ListWorkflowNames()
	if err != nil {
		t.Fatalf("列工作流: %v", err)
	}
	if len(workflows) != 0 {
		t.Fatalf("新账本不该有任何工作流，实得 %v", workflows)
	}

	templates, err := st.ListTemplateNames()
	if err != nil {
		t.Fatalf("列模板: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("新账本不该有任何模板，实得 %v", templates)
	}
}
```

**步骤**：

1. 落盘上述文件，跑绿：`go test ./internal/ledger/ -run TestOpenInstallsNoSeeds -v`。
2. **变异验证这条断言有牙齿**（不做就不算完成）：临时在 `Open` 后插一行
   `_, _ = s.PutWorkflow("x", WorkflowDef{Nodes: []NodeDef{{Name: StatusTodo}}})`，
   复跑必须**红**；确认后还原。变异前先提交，避免还原时撤掉真改动。
3. 提交。

两个签名已在基线钉死（`internal/ledger/templates.go:126` 与
`internal/ledger/workflows.go` 同形），均为 `() ([]string, error)`——上面的代码可直接落盘。

**验收**：`go test ./internal/ledger/ -run TestOpenInstallsNoSeeds` 绿，且步骤 2 的变异实测打红。

## Task 4：修 `TestAttachmentKindsCoverDefaultWorkflowGates` 的失效

**问题**：该测试（`internal/agentd/ledgerapi_test.go:479`）遍历账本里的工作流，
检查每个 gate 的 attachment kind 都在 Web 白名单里。种子退场后它遍历的是
`seedAgentdLedger` 硬编码的 `bug`/`feature`/`triage`——**全部已退役**。
名字与注释仍写「出厂工作流」，而出厂工作流已不存在：它现在守的是夹具自造的世界。

**顺带暴露的既有缺陷**（种子时代就有，不是本轮引入）：它只读
`node.Gate.RequireAttachment`（单数），不读 `RequireAttachmentAny`。
现役 charter 流的 `implement` 门用的正是后者（`["plan","breakdown"]`），
所以那道门的 kind 从来不在这个测试的视野里。

**步骤**：

1. 改夹具意图：把 `seedAgentdLedger` 里的工作流换成**一条专为本测试构造的流**，
   显式覆盖两种 gate 形态——一个节点用 `RequireAttachment`，另一个用
   `RequireAttachmentAny`。已退役的 `bug`/`feature`/`triage` 名字保留与否不重要，
   重要的是**它必须同时含这两种形态**。改动会波及同包其他用 `bug` 流的测试，
   按 Task 1 的手法逐个显式化。
2. 改测试本体：遍历时两个字段都收集——

   ```go
   for _, node := range wf.Def.Nodes {
       if kind := node.Gate.RequireAttachment; kind != "" {
           seen[kind] = name + "/" + node.Name
       }
       for _, kind := range node.Gate.RequireAttachmentAny {
           seen[kind] = name + "/" + node.Name
       }
   }
   ```

3. 改名与注释：名字里的「DefaultWorkflow」已无所指，改为
   `TestAttachmentKindsCoverGateKinds`；注释重写为「工作流 gate 用到的每个
   attachment kind 都必须在 Web 白名单里，否则闸在 Web 永远无法满足」，
   并写明本测试的数据来自测试夹具而非出厂种子。
4. **变异验证**：临时从 Web 白名单里删掉一个 kind，复跑必须红；还原。
5. 跑：`go test ./internal/agentd/...`。
6. 提交。

**验收**：`go test ./internal/agentd/...` 全绿；步骤 4 的变异实测打红；
测试名与注释里不再出现「出厂工作流」。

## Task 5：命名与注释归位

1. **`cmd/b230_test.go` 改名**：内容并入既有的 `cmd/card_dispatch_test.go`
   （被测对象是 `resolveCardDispatchTemplate`），删掉 `b230_test.go`。
   按卡号命名的测试文件半年后没人知道它测什么。
2. **三处英文注释改中文**，与全仓惯例一致：
   - `internal/ledger/test_fixtures_test.go` 的 `seedTestTemplates` 头注释
   - `internal/agentd/ledger_fixtures_test.go` 的 `seedAgentdLedger` 头注释
   - `cmd/card_dispatch.go` 的 `resolveCardDispatchTemplate` 头注释
     （「参数/返回/注意事项」三段已有且写得对，只改语言）
3. 跑：`go test ./cmd/... ./internal/ledger/... ./internal/agentd/...`。
4. 提交。

**验收**：`ls cmd/b230_test.go` 不存在；三处注释为中文；上述三包全绿。

## 不派发的部分（协调者执行）

以下**由协调者执行，不派发**——它们要驱动 handoff 自身，与执行者纪律的
「不调派发 CLI」直接冲突，派出去等于没验：

- 本机 27 张 triage 活卡迁 charter；
- `feature` / `domain` / `bug` / `triage` 四条工作流死行的删除 SQL
  （备份：`~/.handoff/backups/ledger-pre-workflow-cleanup-20260824.db`）；
- 任何 `handoff` CLI / agentd 的运行时验证；
- `product-backlog` skill 的列语义对齐（归 `charter:finish`，清单见 spec 同名节）。

## 四项检查

1. **缺陷族对抗审查**：
   - *静默失效族*：Task 3 的零断言正是为它设的——seed 被加回来时要有东西会红。
     Task 4 的变异步骤同理，防「改夹具让测试失去牙齿」。
   - *边界值族*：缺省解析的三态（0 / 1 / N）已被 `cmd/b230_test.go` 与
     `internal/ledger/cards_test.go` 覆盖，且断言错误文案关键内容而非只断 `err != nil`。
   - *兼容性族*：删的是 seed 而非读取路径。`GetWorkflow` 在卡路径上只出现两处
     （`cards.go` 建卡、`move.go` 移列 gate 判定），`card show` / `card list` 不解析
     工作流，故终态卡在流定义删除后仍可读——**Task 1–5 不得改动这两处读取路径**。
2. **序列化边界设问**：本卡不新增数据字段，无跨进程投影新增。唯一穿 wire 的是
   Task 1 那条 migrate 响应的 from/to 投影，它本来就有穿真实 HTTP handler 的断言
   （`TestMigrateAPIProjectsFromTo` 的立意），Task 1 只是让它重新跑得起来。
3. **上下文预算**：5 个 task 各自圈得出有界文件集，最大的 Task 4 触及 2 个文件。
4. **类型标注**：本卡无边界型子系统改动，无真机清单。

## 自审三查

- **spec 覆盖**：spec 的 6 条用户故事——①②③由第 0 节的 `cards.go` 实现，
  ④由 `cmd/workflow.go` 实现，⑤由 `cmd/card_dispatch.go` 实现，
  ⑥属「不派发的部分」，归协调者。spec 判据第 3 条的断言由 Task 3 补齐。
- **占位符扫描**：无 TBD、无「同 Task N」、无「加适当的错误处理」。
  Task 2 的三个 prompt 常量行号标了 `~`，因删除操作按名字定位、行号仅供导航，
  不构成占位符。
- **跨 task 签名一致性**：5 个 task 无相互 Produces/Consumes 依赖；
  Task 1 与 Task 4 同改 `internal/agentd/ledgerapi_test.go`，**按序做**（1 → 4）避免冲突。
