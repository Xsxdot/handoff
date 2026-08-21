# B167 跨流迁移实现计划（契约冻结后·轻档单轮）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在已冻结的契约骨架上补齐 B167 的行为——在飞门禁、迁移事件留痕、API 完整投影、Web 迁移入口。

**Architecture:** 契约已由 Ticket 0 冻结（类型、签名、端点、TS 接口、fixture 全部就位且编译绿）。本轮**只填行为，不动契约形状**——任何需要改契约的发现都要停下来报告，不许自行改形状。

**Tech Stack:** Go（`database/sql` + `s.q()` 占位符 + `s.mutate` 事务、slog via `log()`）、React/TS（vitest + @testing-library/react）。

---

## 前置：四项分域检查的落点

本需求的四项分域检查**已在拆解阶段完成**，结论在
`docs/superpowers/specs/2026-08-21-b167-workflow-migration-breakdown.md`：

- 缺陷族对抗审查 → §4（八节，逐族）
- 序列化边界设问 → §4.2（逐字段列了产生/消费链）
- 上下文预算检查 → §5
- 域类型标注 → §1

**契约拍板记录在该文档末尾**——与 §2 提案冲突处以拍板记录为准。本 plan 不重复，实现时按它办。

## 现状（已由 Ticket 0 落地，不要重做）

- ✅ `MigrateCardWorkflow(cardID, toWorkflow string, toVersion int, toStatus, actor string) error` 新签名 + 防悬空校验 + 版本 0 解析最新
- ✅ `triage` 流 seed（`待办 → 定性中 → 已定性`，纯人工三列）
- ✅ 建卡 `workflow` 为空时账本解析为 `triage`
- ✅ `proto` 侧账本 wire DTO + fixture + 前端镜像断言（两层防线已验证有牙）
- ✅ `POST /api/cards/{id}/migrate` 路由与请求校验
- ✅ CLI `workflow migrate <card-id> --workflow --column --version --yes`
- ✅ `web/src/api/ledger.ts` 的 `migrateCard()`
- 🔨 `CardStepInFlight` 是 stub（恒返回 `false, nil`）
- 🔨 迁移事件仍写 `EvComment`，未用 `EvWorkflowMigrated`
- 🔨 API 响应只有 `{ok, id}`，未投影 `from`/`to`/`event`
- 🔨 Web 无迁移入口（只有 API 函数）

---

### Task 1: 在飞态派生查询

**Files:**
- Modify: `internal/ledger/taskstate.go`（`CardStepInFlight` стub 处）
- Test: `internal/ledger/taskstate_test.go`

**语义**（拍板记录④）：「在飞」= 该卡存在一个已镜像的任务，其生命周期尚未到达终态。
终态事件类型是 `archived` 与 `failed`；`completed` / `turn_failed` **不算终态**——
它们对应 `waiting_review`，基准文档 §3 语义 4 明确把「等裁决」算作在飞。

镜像事件形状（`internal/ledger/mirror.go` 写入端）：`card_events` 里 `type = EvTaskMirrored`，
`payload` 是 `{"task_type":"<原始事件类型>","payload":<原文>}`，任务身份在 `source_task` 列。
**沿用 `OpenTicketCounts` 的单遍扫描 + 回放写法，不要另造。**

- [ ] **Step 1: 写失败测试**

在 `internal/ledger/taskstate_test.go` 末尾追加（helper 以文件内现状为准）：

```go
// TestCardStepInFlightReplaysTaskLifecycle 在飞判定按任务生命周期回放：
// 派发后未见终态=在飞；archived/failed 才收口。completed/turn_failed 对应
// waiting_review，基准语义把「等裁决」算在飞，不许当终态。
func TestCardStepInFlightInFlightUntilTerminal(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "在飞判定")
	if got, err := s.CardStepInFlight(c.ID); err != nil || got {
		t.Fatalf("没派过任务应不在飞，实得 %v err=%v", got, err)
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "dispatched")
	if got, _ := s.CardStepInFlight(c.ID); !got {
		t.Fatal("派发后未见终态应判在飞")
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "completed")
	if got, _ := s.CardStepInFlight(c.ID); !got {
		t.Fatal("completed 对应 waiting_review，仍算在飞")
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "archived")
	if got, _ := s.CardStepInFlight(c.ID); got {
		t.Fatal("archived 是终态，应收口")
	}
}

// TestCardStepInFlightPerTask 多任务各自回放，一个收口不影响另一个。
func TestCardStepInFlightPerTask(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "多任务在飞")
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "dispatched")
	mirrorTaskEvent(t, s, c.ID, "acc", "T-2", "dispatched")
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "archived")
	if got, _ := s.CardStepInFlight(c.ID); !got {
		t.Fatal("T-2 未收口，整卡仍应判在飞")
	}
	mirrorTaskEvent(t, s, c.ID, "acc", "T-2", "failed")
	if got, _ := s.CardStepInFlight(c.ID); got {
		t.Fatal("两个任务都收口了，应判不在飞")
	}
}
```

若 `mirrorTaskEvent` 这个 helper 不存在就在测试文件里写一个：按
`internal/ledger/mirror.go` 的写入形状插一行 `EvTaskMirrored` 事件
（`payload = {"task_type":"<type>","payload":null}`，带 `source_target` / `source_task`）。
**必须走真实写入路径 `s.AppendMirroredEvent(cardID, MirroredEvent{...})`**
（`internal/ledger/mirror.go:32`，已核实存在），不要直接 INSERT 绕过写入端——
绕过就测不到写入端与读取端的形状是否一致（这正是 wire 类缺陷的温床）。

- [ ] **Step 2: 跑测确认红**

Run: `go test ./internal/ledger -run TestCardStepInFlight -v`
Expected: FAIL（stub 恒返回 false，第二个断言起就该红）

- [ ] **Step 3: 实现派生**

替换 `CardStepInFlight` 的 stub 体：单遍扫 `EvTaskMirrored`（按 `seq ASC`），
按 `source_task` 分组回放；见到 `archived` / `failed` 即把该任务标记收口，
其余事件表示该任务仍活。任一任务未收口即返回 `true`。
保留原 doc 注释里「镜像滞后即在飞判定滞后，不另设真相源」那句。

- [ ] **Step 4: 跑测确认绿**

Run: `go test ./internal/ledger -run TestCardStepInFlight -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

- 判定为在飞时打 Debug（带 card、未收口的 task id）——高频只读路径，用 Debug 不用 Info
- 扫描/解码失败打 Error 带 card 与 cause
用 `log()`，禁止 `fmt.Printf`。

- [ ] **Step 6: 加注释**

doc 注释补：终态判据（archived/failed）与「为什么 completed 不算终态」（对应
waiting_review，基准语义 4 把等裁决算在飞）——这是最容易被后人改错的地方。

- [ ] **Step 7: Commit**

```bash
git add internal/ledger/taskstate.go internal/ledger/taskstate_test.go
git commit -m "feat(b167): 在飞态从镜像事件流派生，不另设真相源"
```

---

### Task 2: 迁移事务内的在飞门禁 + 事件留痕

**Files:**
- Modify: `internal/ledger/workflows.go`（`MigrateCardWorkflow`）
- Test: `internal/ledger/workflows_test.go`

**两条语义一起落**（基准文档 §3 语义 3 与 4）：门禁在**事务内**做（拍板记录④：
两个入口因此共享同一道门）；事件用 `EvWorkflowMigrated`，payload 必须能回答
「从哪条流哪列 → 哪条流哪列、谁干的」。

- [ ] **Step 1: 写失败测试**

```go
// TestMigrateRejectsInFlight 卡有环节在飞时拒绝迁移——门禁在事务内，
// 所以 CLI 与 HTTP 两个入口共享同一道门（契约拍板记录④）。
func TestMigrateRejectsInFlight(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "在飞拒迁")
	mirrorTaskEvent(t, s, c.ID, "acc", "T-1", "dispatched")
	err := s.MigrateCardWorkflow(c.ID, "bug", 0, StatusDoing, "test")
	if !errors.Is(err, ErrStepInFlight) {
		t.Fatalf("在飞时应拒绝迁移并包 ErrStepInFlight，实得 %v", err)
	}
	// 拒绝必须是「没动」，不是「动了一半」
	got, _ := s.GetCard(c.ID)
	if got.WorkflowName == "bug" {
		t.Fatal("被拒的迁移不该改动卡")
	}
}

// TestMigrateWritesMigrationEvent 迁移落 EvWorkflowMigrated，payload 能回答
// 从哪到哪——审计链要能解释「这张卡为什么换了流程」。
func TestMigrateWritesMigrationEvent(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "迁移留痕") // 建卡默认 triage
	if err := s.MigrateCardWorkflow(c.ID, "bug", 0, StatusDoing, "tester"); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	events, err := s.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.Type != EvWorkflowMigrated {
			continue
		}
		found = true
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("解码迁移事件: %v", err)
		}
		for _, k := range []string{"from_workflow", "from_status", "to_workflow", "to_status", "to_version"} {
			if _, ok := p[k]; !ok {
				t.Fatalf("迁移事件缺字段 %q: %v", k, p)
			}
		}
		if p["to_workflow"] != "bug" || p["from_workflow"] != "triage" {
			t.Fatalf("迁移事件的来去不对: %v", p)
		}
		if e.Actor != "tester" {
			t.Fatalf("操作者应留痕，实得 %q", e.Actor)
		}
	}
	if !found {
		t.Fatal("没有 EvWorkflowMigrated 事件")
	}
}

// TestMigrateLeavesChildrenAlone 子卡不随父卡迁（基准语义 5）。
func TestMigrateLeavesChildrenAlone(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "父卡")
	child := mustChild(t, s, parent.ID, "子卡") // mustChild 建的是 bug 流子卡
	if err := s.MigrateCardWorkflow(parent.ID, "feature", 0, StatusTodo, "test"); err != nil {
		t.Fatalf("迁父卡: %v", err)
	}
	got, err := s.GetCard(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowName != "bug" {
		t.Fatalf("子卡不该随父卡迁，实得 %q", got.WorkflowName)
	}
}
```

（`json` / `errors` 未导入则补。`mustChild` 在 `cards_test.go` 已存在。）

- [ ] **Step 2: 跑测确认红**

Run: `go test ./internal/ledger -run "TestMigrateRejectsInFlight|TestMigrateWritesMigrationEvent|TestMigrateLeavesChildrenAlone" -v`
Expected: 前两条 FAIL；第三条**可能已经绿**（迁移只 UPDATE 本卡）——**若它一开始就绿，
如实记进 ledger 说明「该语义由现有实现天然满足，本测试是回归网」，不要为了「让它先红」去改实现。**

- [ ] **Step 3: 实现**

在 `MigrateCardWorkflow` 的 `s.mutate` 内、防悬空校验**之前**加在飞门禁：
调 `CardStepInFlight`（事务内读，若签名不接 tx 则抽一个 `cardStepInFlightTx` 内部版，
公开方法委托给它——**不要在事务外读**，那会有 TOCTOU 窗口）。
拒绝时包 **`ErrStepInFlight`**（`internal/ledger/ledger.go:28` 已定义，且 `ledgerErr` 已把它映射成 409——**这是一个已声明但还没有生产者的哨兵，本 task 就是它的第一个生产者**，不要改用 `ErrBadState`）并给可读原因（哪个 task 还没收口）。

把结尾的 `EvComment` 换成 `EvWorkflowMigrated`，payload 至少含
`from_workflow`/`from_version`/`from_status`/`to_workflow`/`to_version`/`to_status`。
**`from_*` 要在 UPDATE 之前读出来**——UPDATE 之后原值就没了。

- [ ] **Step 4: 跑测确认绿 + 包全量**

Run: `go test ./internal/ledger`
Expected: PASS（既有测试不许红；`MigrateCardWorkflow` 的老用例若断言了 EvComment，
改成断言新事件类型——**但不要削弱它原本在守的东西**）

- [ ] **Step 5: 加关键节点日志**

- 门禁拒绝：Warn，带 card、未收口 task、目标流列
- 迁移成功：Info，带 from/to 全部六个字段
- `from_*` 读取失败：Error 带 cause

- [ ] **Step 6: 加注释**

在门禁处写「为什么在事务内」（TOCTOU + 两入口共享同一道门，指向契约拍板记录④）；
在 `from_*` 读取处写「为什么必须在 UPDATE 之前」。

- [ ] **Step 7: Commit**

```bash
git add internal/ledger/workflows.go internal/ledger/workflows_test.go
git commit -m "feat(b167): 迁移事务内在飞门禁与 EvWorkflowMigrated 事件留痕"
```

---

### Task 3: API 完整投影与错误码

**Files:**
- Modify: `internal/agentd/ledgerapi.go`（`handleCardMigrate`，TODO(B167-A) 处）
- Modify: `internal/ledger/workflows.go`（若需返回迁移结果，见下）
- Test: `internal/agentd/ledgerapi_test.go`

**契约形状已冻结**：`proto.MigrateCardResp` 有 `from`/`to`/`event`，
`proto.WorkflowLocation` / `WorkflowMigration` 已定义。本 task 把它填满。

Store 侧当前 `MigrateCardWorkflow` 只返回 `error`。要投影 from/to/event，
**优先方案**：让它返回 `(ledger.WorkflowMigration, error)`——形状 Ticket 0 已冻结，
这不是改契约是填契约。同步更新 CLI 调用点（丢弃返回值即可）。

- [ ] **Step 1: 写失败测试**

```go
// TestMigrateAPIProjectsFromTo 迁移响应必须带 from/to/event——穿真实 HTTP
// handler 断言，Store 有值不代表 wire 上有值（ChildrenTotal 教训）。
// 用指针接字段：区分「键缺失」与「值为零」，两者症状完全不同。
func TestMigrateAPIProjectsFromTo(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "迁移投影") // 默认 triage
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/migrate",
		`{"workflow":"bug","status":"进行中"}`)
	if code != http.StatusOK {
		t.Fatalf("迁移 code=%d body=%s", code, body)
	}
	var resp struct {
		OK   *bool `json:"ok"`
		From *struct {
			Workflow *string `json:"workflow"`
			Status   *string `json:"status"`
			Version  *int    `json:"version"`
		} `json:"from"`
		To *struct {
			Workflow *string `json:"workflow"`
			Status   *string `json:"status"`
			Version  *int    `json:"version"`
		} `json:"to"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码响应: %v body=%s", err, body)
	}
	if resp.From == nil || resp.To == nil {
		t.Fatalf("响应缺 from/to：%s", body)
	}
	if resp.From.Workflow == nil || *resp.From.Workflow != "triage" {
		t.Fatalf("from.workflow 不对：%s", body)
	}
	if resp.To.Workflow == nil || *resp.To.Workflow != "bug" ||
		resp.To.Status == nil || *resp.To.Status != "进行中" {
		t.Fatalf("to 不对：%s", body)
	}
	if resp.To.Version == nil {
		t.Fatal("to.version 键缺失——版本必须回给调用方（契约拍板记录②）")
	}
}

// TestMigrateAPIRejectsInFlightWith409 在飞时 handler 把 Store 的错误翻成 409，
// 不自己做检查（契约拍板记录④：handler 只翻错误码）。
func TestMigrateAPIRejectsInFlightWith409(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "在飞 409")
	mirrorTaskEvent(t, env.ledger, card.ID, "acc", "T-1", "dispatched")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/migrate",
		`{"workflow":"bug","status":"进行中"}`)
	if code != http.StatusConflict {
		t.Fatalf("在飞迁移应 409，实得 %d body=%s", code, body)
	}
}
```

- [ ] **Step 2: 跑测确认红**

Run: `go test ./internal/agentd -run TestMigrateAPI -v`
Expected: FAIL（响应目前只有 ok+id）

- [ ] **Step 3: 实现**

Store 侧改返回 `(WorkflowMigration, error)`（更新 CLI 调用点）；handler 把它投影进
`proto.MigrateCardResp`。**`ledgerErr` 已把 `ErrStepInFlight` 映射成 409**（`internal/agentd/ledgerapi.go:71`，已核实），
所以 handler 什么都不用做——Task 2 一旦返回该哨兵，409 自动成立。
**不要在 handler 里自己判在飞**（拍板记录④）。

- [ ] **Step 4: 跑测确认绿 + 两包全量**

Run: `go test ./internal/agentd ./internal/ledger ./cmd`
Expected: PASS

- [ ] **Step 5: 加关键节点日志 + 注释**

handler 成功路径 Info（card、from/to）；翻 409 时 Warn 带原因。
注释写明「handler 不做在飞检查，只翻 Store 的错误码」并指向拍板记录④——
防止后人"顺手"在 handler 里补一个检查，那会让两个入口再次分裂。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go internal/ledger/workflows.go cmd/workflow.go
git commit -m "feat(b167): 迁移 API 投影 from/to/event，在飞翻 409"
```

---

### Task 4: Web 迁移入口

**Files:**
- Modify: `web/src/app/cards/CardItem.tsx` 或卡抽屉组件（入口按钮）
- Create: `web/src/app/cards/MigrateDialog.tsx`
- Test: `web/src/app/cards/MigrateDialog.test.tsx`

**形态约束**：这是**对话框不是新页面**，复用既有对话框样式（参照建卡对话框
`web/src/app/cards/` 下的现有实现）。不新增路由、不改页面布局。

**必须做到**：目标流下拉从 `GET /api/flows` 取（不硬编码流名）；选定流后落点列下拉
从该流的 `states` 取（**显式选，不做同名列猜测**——基准语义 1）；提交调既有
`migrateCard()`；409 的错误文案原样显示给用户（不要吞成"迁移失败"）。

- [ ] **Step 1: 写失败测试**

```tsx
// 迁移对话框：目标流与落点列都必须显式选，且落点列跟随所选流刷新——
// 「自动映射同名列」是基准语义 1 明令禁止的猜测。
it('落点列随所选工作流刷新，且不预选同名列', async () => {
  // 渲染对话框，mock /api/flows 返回两条流（列名不同）
  // 选流 A → 断言列下拉的选项 = 流 A 的 states
  // 切到流 B → 断言列下拉的选项 = 流 B 的 states，且当前值被清空（不是沿用同名）
})

it('409 的服务端文案原样展示', async () => {
  // mock migrateCard 抛 409 带中文原因
  // 断言该原因文本出现在 DOM 里，而不是被吞成通用文案
})
```

（测试骨架照 `web/src/app/flows/NodeEditor.childrengate.test.tsx` 的现有写法补全，
用直接查询 input/select 元素，**不要**通过 label 的 `for` 属性绕一层——
仓内已知 `controlID` 有重复 id 缺陷（B169），绕 label 会读到错的元素。）

- [ ] **Step 2: 跑测确认红**

Run: `cd web && npx vitest run src/app/cards/MigrateDialog.test.tsx`
Expected: FAIL（组件不存在）

- [ ] **Step 3: 实现对话框 + 入口**

- [ ] **Step 4: 跑测确认绿 + 前端全量**

Run: `cd web && npx vitest run && npx tsc --noEmit`
Expected: 全绿 + 类型干净

- [ ] **Step 5: 加注释**

组件头注释写职责与边界（"只负责收集目标流/列并提交，不做落点合法性判断——
那是账本的事"）；在"切流清空列值"处写为什么（禁止同名列猜测，基准语义 1）。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/cards/
git commit -m "feat(b167): Web 迁移对话框，目标流与落点列显式选"
```

---

### Task 5: 全量回归与门禁绕过验收

**Files:** 无新增，只跑与记录

- [ ] **Step 1: 全量**

Run: `go test ./... && gofmt -l . && cd web && npx vitest run && npx tsc --noEmit`
Expected: 全绿。**已知偶发红**：`internal/agentd` 的 `TestPtyWSEchoRoundTrip`
（TempDir 清理竞态，与本需求无关，已另立任务）——命中就单跑复验并如实记进 ledger，
不要为它改代码。

- [ ] **Step 2: 门禁绕过链路的集成测试**

拆解文档 §4.4 的结论要有一条测试守住。在 `internal/ledger` 写：

```go
// TestMigrateCannotBypassGate 迁移不能用来跳过目标流的 gate：
// 卡缺 contract 附件 → 迁到无闸流 → 再迁回有 contract 闸的列，最后一步仍须被拒。
// 这是拆解 §4.4 的结论落成的回归网。
func TestMigrateCannotBypassGate(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "绕闸尝试")
	// domain/契约冻结 需要 contract 附件
	if err := s.MigrateCardWorkflow(c.ID, "domain", 0, "契约冻结", "test"); err == nil {
		t.Fatal("缺 contract 附件不该能迁进契约冻结列")
	}
	if err := s.MigrateCardWorkflow(c.ID, "bug", 0, StatusDoing, "test"); err != nil {
		t.Fatalf("迁到无闸流应允许（场景 B 降级）: %v", err)
	}
	if err := s.MigrateCardWorkflow(c.ID, "domain", 0, "契约冻结", "test"); err == nil {
		t.Fatal("绕一圈回来仍须被目标 gate 拒绝")
	}
}
```

**注意**：若迁移当前**不执行目标列 gate**，这条会红——那说明门禁没落，属于本 task
要补的实现（在 `MigrateCardWorkflow` 的落点校验里加 gate 检查），不是把测试改软。

- [ ] **Step 3: 补 gate 检查（若 Step 2 红）**

- [ ] **Step 4: 全量复跑 + Commit**

```bash
git add -A
git commit -m "test(b167): 门禁绕过链路回归网——迁移不能跳过目标列 gate"
```

---

## 审核者本地验收清单（不派发）

> 需要驱动 handoff 自身，归审核者：

1. 隔离 agentd + 控制台：建卡不选流 → 落 `triage`；走迁移对话框迁到 `bug/进行中`；
   timeline 能看到迁移事件并回答「从哪到哪」。
2. 在飞拒绝的**双通道一致性**（本需求的承重判据）：造一张有未收口任务的卡，
   分别用 Web 与 CLI 迁移，**两者都必须被拒且原因一致**。
3. 变异复验：把 Task 2 的在飞门禁临时注释掉，确认 `TestMigrateRejectsInFlight`
   与真机双通道都转红。
