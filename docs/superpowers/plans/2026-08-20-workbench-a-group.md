# 工作台 A 组三条 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让看板抽屉能派发环节（审阅 / 合并）、能看直接子卡、能写验收，把三条只在
CLI 上存在的能力接到界面上。

**Architecture:** 环节编排从 `cmd/` 搬进**既有的** `internal/ledgerstep`，只收显式依赖
（账本 store、仓路径、派发传输、actor）；CLI 与 agentd 各自注入自己的那份。环节在
agentd 的 goroutine 里异步跑，`POST /api/cards/{id}/step` 立即返回 202，界面靠已有的
卡事件流看进展。

**Tech Stack:** Go 1.21+、SQLite（`internal/ledger`）、`log/slog`；前端 React + TypeScript
+ Vitest + Testing Library。无新依赖。

## Global Constraints

- **本 plan 建立在「纪律块具名化」已合入的基础上**（`docs/superpowers/plans/2026-08-20-discipline-naming.md`）。
  依赖它的三处产出：`dispatchViaTemplate` 不再读纪律块文件、`TemplateDef.Discipline`
  存的是角色名、`DispatchSnapshot.DisciplineName`。**开工前先确认这三处已在分支上**，
  不成立就停下报告，别按旧形态实现。
- **编排包用既有的 `internal/ledgerstep`，不新建 `internal/cardstep`。**
  spec §4 写的是新建，那是写 spec 时没核实基线——`internal/ledgerstep/verdict.go`
  的包注释已经写明它是「审阅/合并环节的**唯一实现**，主会话（经 CLI）与**看板按钮
  （经 Plan D API）共用**」，注入点模式（`RunReview` / `Objective` / `DoMerge` 函数字段
  + `wire.go` 生产装配）也已经建好。再开一个包等于造第二个真相源，正是要避免的东西。
- **不做「派发实现」按钮。** 交接文档这条叫「按**环节**派发」，实现派发不是环节，
  且它通常要挂 plan 文件——浏览器里没有那个文件。实现派发留 CLI。
- **子任务只一层，不递归、不聚合。** 孙卡从子卡抽屉再往下点。
- **同一张卡同时只允许一个环节在飞**，重复请求 409。在飞集合是 agentd 进程内状态，
  重启即清空——本轮不做恢复。
- 后端日志一律 `slog`（`slog.Default()` 或既有 `s.log` / `logger`），**禁止 `fmt.Printf`**。
  前端不留 `console.log`。
- 新文件写文件头注释（职责 + 边界）；导出函数写 doc 注释（参数、返回、注意事项）；
  非显然分支写「为什么」的中文注释。
- web 写动作的 actor 沿用既有约定 `"web:" + r.RemoteAddr`（见 `handleCardMove`）。
- 本 plan **不调用 handoff CLI、不起 agentd 进程**——需要真机驱动 handoff 的验收在附一，
  由审核者执行。

---

### Task 1: `ChildrenOf` 与 `CardBrief`

**Files:**
- Modify: `internal/ledger/cards.go`（加 `CardBrief` 类型与 `ChildrenOf` 方法）
- Test: `internal/ledger/cards_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: 无
- Produces:
  - `type CardBrief struct { ID, Title, Status string }`（json tag `id` / `title` / `status`）
  - `func (s *Store) ChildrenOf(cardID string) ([]CardBrief, error)`

**先读懂再动**：`internal/ledger/cards.go:85` 已经有一条
`SELECT id FROM cards WHERE parent_id = ?`，但它住在 `nextChildID` 里面、只为给子卡
分配点号位（B157 → B157.1）。**别去改它**——它的返回是 id 列表且只喂给一个号段算法，
改成通用查询会把两件事绑死。新写一个方法。

`idx_cards_parent` 索引已存在（`internal/ledger/store.go:198`），新方法直接受益，
不需要加索引。

- [ ] **Step 1: 写失败的测试**

在 `internal/ledger/cards_test.go` 追加（`newTestStore(t)` 是本包已有的建库辅助）：

```go
// TestChildrenOfReturnsDirectChildrenSorted 只返回直接子卡，按 id 排序。
func TestChildrenOfReturnsDirectChildren(t *testing.T) {
	s := newTestStore(t)
	root := mk(t, s, "根卡")
	childB := mustChild(t, s, root.ID, "子卡 B")
	childA := mustChild(t, s, root.ID, "子卡 A")
	grand := mustChild(t, s, childA.ID, "孙卡")

	got, err := s.ChildrenOf(root.ID)
	if err != nil {
		t.Fatalf("ChildrenOf: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应有 2 个直接子卡，实得 %d：%+v", len(got), got)
	}
	// 孙卡不该出现：本方法只一层，递归是抽屉里再点一次的事
	for _, brief := range got {
		if brief.ID == grand.ID {
			t.Fatalf("孙卡 %s 不该出现在直接子卡里", grand.ID)
		}
	}
	if got[0].ID > got[1].ID {
		t.Fatalf("应按 id 排序，实得 %s, %s", got[0].ID, got[1].ID)
	}
	byID := map[string]CardBrief{got[0].ID: got[0], got[1].ID: got[1]}
	if byID[childA.ID].Title != "子卡 A" || byID[childB.ID].Title != "子卡 B" {
		t.Fatalf("标题没带出来：%+v", got)
	}
	if byID[childA.ID].Status == "" {
		t.Fatalf("状态没带出来：%+v", got)
	}
}

// TestChildrenOfEmptyForLeaf 叶子卡返回空切片而不是错误——
// 「没有子卡」是正常态，抽屉据此整区不渲染。
func TestChildrenOfEmptyForLeaf(t *testing.T) {
	s := newTestStore(t)
	leaf := mk(t, s, "叶子卡")
	got, err := s.ChildrenOf(leaf.ID)
	if err != nil {
		t.Fatalf("ChildrenOf: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("叶子卡应为空，实得 %+v", got)
	}
}

// TestChildrenOfUnknownCardErrors 卡不存在要报错（映射 404），
// 不能与「有卡但没子卡」都返回空——那样前端分不出「打错 id」和「真没有」。
func TestChildrenOfUnknownCardErrors(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ChildrenOf("B-不存在"); err == nil {
		t.Fatal("未知卡应报错")
	}
}
```

`mk(t, s, title)` 是本包测试已有的建卡辅助（`internal/ledger/events_test.go:125` 在用）。
`mustChild` 若不存在就在测试文件里补一个小辅助——**先 grep 现成的建子卡写法**
（`CreateCard` 的 parent 参数怎么传），照抄那个形态，别猜签名。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ledger/ -run TestChildrenOf -count=1`
Expected: **编译失败**，`s.ChildrenOf undefined`、`CardBrief` 未定义。

- [ ] **Step 3: 实现**

在 `internal/ledger/cards.go` 追加：

```go
// CardBrief 是卡的最小展示三元组：抽屉「子任务」区一行需要的全部。
//
// 为什么不直接返回 Card：子任务区只渲染 id + 标题 + 状态徽标，把整张卡
// （含判据、附件、驱动租约）塞进详情响应，是给一个只读列表付整卡的序列化代价。
type CardBrief struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ChildrenOf 返回该卡的**直接**子卡（parent_id = cardID），按 id 升序。
//
// 参数：cardID 父卡 id。
// 返回：直接子卡的最小三元组；卡不存在时返回错误（上层映射 404）。
//
// 注意：**只一层，不递归**。要全后代请看 Subtree——但那个语义不一样
// （含 merged_into 的并入成员），不是「子任务」。
//
// 为什么卡不存在要报错而不是返回空：空切片是「这张卡没有子卡」的合法答案，
// 与「你给的 id 根本不存在」混成同一个响应，前端就只能靠猜。
func (s *Store) ChildrenOf(cardID string) ([]CardBrief, error) {
	if _, err := s.GetCard(cardID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(s.q(
		`SELECT id, title, status FROM cards WHERE parent_id = ? ORDER BY id`), cardID)
	if err != nil {
		return nil, fmt.Errorf("读子卡 %s: %w", cardID, err)
	}
	defer rows.Close()
	children := make([]CardBrief, 0, 4)
	for rows.Next() {
		var brief CardBrief
		if err := rows.Scan(&brief.ID, &brief.Title, &brief.Status); err != nil {
			return nil, fmt.Errorf("扫描子卡 %s: %w", cardID, err)
		}
		children = append(children, brief)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读子卡 %s: %w", cardID, err)
	}
	return children, nil
}
```

`s.q(...)` 是本包既有的 SQL 方言适配（`Subtree` 里在用），照用。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledger/ -count=1`
Expected: PASS，全包绿。

- [ ] **Step 5: 加关键节点日志**

`internal/ledger` 是叶子层，其文件头纪律写明**方法错误 return 前不打日志，由调用方
带上下文记录**（`internal/store/projects.go:13` 同款）。**本方法遵此不打日志**——
这是判断不是跳过：日志在 Task 2 的 handler 层补。

逐条确认：三条错误路径（GetCard、Query、Scan/Err）都用 `%w` 或带上下文的
`fmt.Errorf` 包装，调用方拿得到真因。

- [ ] **Step 6: 加注释**

- `CardBrief` 的 doc 注释：含「为什么不直接返回 Card」
- `ChildrenOf` 的 doc 注释：参数、返回、**「只一层不递归」**、
  「为什么卡不存在要报错」、以及不要误用 `Subtree` 的提示

- [ ] **Step 7: 顺带修掉 `Subtree` 那句误导人的注释**

`internal/ledger/events.go:275` 的注释写着「多路 wait 与**看板 rollup** 共用」，
但全仓唯一调用方是 `cmd/card_wait.go:59`，看板 rollup 并不存在。那半句是遗留的愿景
描述——写 spec 时差点据此得出「子任务区该用 Subtree」的错误结论。删掉后半句：

```go
// Subtree 返回卡树成员 id 集：root + 全部后代（parent 链）+ 并入成员
// （merged_into 指向集内任一成员的卡）。多路 wait 用。
//
// 注意：它的语义**含并入成员**，与抽屉「子任务」区不是一回事——
// 那里要的是直接子卡，走 ChildrenOf。
```

验证这句话仍然为真：

```bash
grep -rn '\.Subtree(' --include='*.go' .
```
Expected: 只有 `cmd/card_wait.go` 一处（外加 `internal/ledger` 内部的定义与测试）。
若出现第三处调用方，**停下来报告**——注释可能不是遗留而是我漏看了。

- [ ] **Step 8: 提交**

```bash
git add internal/ledger/cards.go internal/ledger/cards_test.go internal/ledger/events.go
git commit -m "feat(ledger): 加 ChildrenOf 取直接子卡，并订正 Subtree 的误导注释"
```

---

### Task 2: 详情返回 `children` 与抽屉「子任务」区

**Files:**
- Modify: `internal/agentd/ledgerapi.go:178-200`（`handleCardDetail` 多返回 `children`）
- Modify: `web/src/api/ledger.ts`（`CardBrief` 类型 + `CardDetail.children`）
- Modify: `web/src/app/cards/CardDrawer.tsx`（「关系」区之后加「子任务」区）
- Test: `internal/agentd/ledgerapi_test.go`、`web/src/app/cards/CardDrawer.test.tsx`

**Interfaces:**
- Consumes: Task 1 的 `ledger.CardBrief`、`(*ledger.Store).ChildrenOf`
- Produces: 详情响应多一个键 `children`；前端 `CardDetail.children?: CardBrief[] | null`

**先读懂再动**：`handleCardDetail` 的既有形态是「**卡的一切只在一处看**」——
`decisions`、`needs` 都是随详情给的，各自带着「为什么要随详情给」的注释。
**不新开端点**，照这个形态加。

注意它对每个附加查询都用 `x, _ := s.ledger.Xxx(id)` 吞掉错误——这是刻意的：
主查询 `GetCard` 已经决定了 404/200，附加信息拿不到时降级成空而不是让整个抽屉打不开。
**照这个形态写，但要补日志**（见 Step 5）。

- [ ] **Step 1: 写失败的后端测试**

在 `internal/agentd/ledgerapi_test.go` 追加（`ledgerGet` / `env` 是本文件已有的辅助）：

```go
// TestCardDetailReturnsChildren 抽屉是「卡的一切只在一处看」的那一处，
// 子任务随详情给，不新开端点。
func TestCardDetailReturnsChildren(t *testing.T) {
	env := newLedgerEnv(t)
	parent := seedCard(t, env, "父卡")
	child := seedChildCard(t, env, parent.ID, "子卡")

	code, body := ledgerGet(t, env, "/api/cards/"+parent.ID)
	if code != http.StatusOK {
		t.Fatalf("code=%d body=%s", code, body)
	}
	var resp struct {
		Children []ledger.CardBrief `json:"children"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解析详情: %v，body=%s", err, body)
	}
	if len(resp.Children) != 1 || resp.Children[0].ID != child.ID {
		t.Fatalf("children 不对: %+v", resp.Children)
	}
	if resp.Children[0].Title == "" || resp.Children[0].Status == "" {
		t.Fatalf("children 少字段: %+v", resp.Children)
	}
}
```

`newLedgerEnv` / `seedCard` / `seedChildCard`：**先 grep `internal/agentd/ledgerapi_test.go`
里现成的建环境与建卡写法**（`ledgerapi_test.go:73` 附近就有），照抄那个形态，别新造。
现成的没有建子卡的辅助时，在测试里直接调 `env.ledger.CreateCard(...)`（照抄 Task 1
测试里确认过的 parent 传法）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestCardDetailReturnsChildren -count=1`
Expected: FAIL —— `children` 为空（响应里根本没有这个键）。

- [ ] **Step 3: 实现后端**

`handleCardDetail` 里加一行查询与一个响应键：

```go
	// 子任务随详情给：抽屉是「卡的一切只在一处看」的那一处，为一个只读列表
	// 单开端点会让抽屉多打一次网络往返，还得自己处理它的 loading 与失败态
	children, err := s.ledger.ChildrenOf(id)
	if err != nil {
		// 与 relations/decisions 同款降级：主查询已经决定了 200，
		// 附加信息拿不到时给空列表，不能让整个抽屉打不开
		s.log.Warn("读子卡失败，详情降级为无子任务", "card", id, "cause", err)
		children = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"card": card, "relations": relations, "events": events,
		"task_states": taskStates, "effective_base_branch": base,
		"decisions": decisions, "needs": needs, "children": children,
	})
```

- [ ] **Step 4: 写失败的前端测试**

`web/src/app/cards/CardDrawer.test.tsx` 追加：

```tsx
describe('抽屉里的子任务', () => {
  it('有直接子卡时列出来，点 id 能跳转', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B156', Title: '父卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '',
      children: [
        { id: 'B156.1', title: '子卡一', status: '待办' },
        { id: 'B156.2', title: '子卡二', status: '已完成' },
      ],
    })
    const opened: string[] = []
    render(<CardDrawer id="B156" onClose={() => {}} onOpenCard={(id) => opened.push(id)} />)
    expect(await screen.findByText(/子任务/)).toBeInTheDocument()
    expect(screen.getByText('子卡一')).toBeInTheDocument()
    expect(screen.getByText('已完成')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'B156.1' }))
    expect(opened).toEqual(['B156.1'])
  })

  it('没有子卡时整区不渲染——空区块比没有区块更吵', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B160', Title: '叶子卡', Status: '待办', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B160" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('叶子卡')
    expect(screen.queryByText(/子任务/)).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 5: 跑前端测试确认它失败**

Run: `cd web && npx vitest run src/app/cards/CardDrawer.test.tsx`
Expected: FAIL —— 找不到「子任务」。

（`node_modules` 不在就先 `npm install`。**别用 `npx vitest run … && echo ok`
这类写法判成败**——`&&` 会绑到管道里的最后一个命令上，看着成功其实什么都没跑。
直接看命令自己的退出码。）

- [ ] **Step 6: 实现前端**

`web/src/api/ledger.ts`：

```ts
// CardBrief 是子任务区一行的最小三元组，字段名与 Go 侧 ledger.CardBrief 一字不差。
export interface CardBrief {
  id: string
  title: string
  status: string
}
```

`CardDetail` 加一个**可选**字段：

```ts
  // children 是直接子卡（只一层）。可选而非必填：抽屉对每个列表都用 `?? []`
  // 防御性读取，标成必填只会逼着六处与子任务无关的测试 mock 补一个空数组。
  children?: CardBrief[] | null
```

`CardDrawer.tsx` 在「关系」区之后、「关联执行（task）」之前插入：

```tsx
            {(detail.children ?? []).length > 0 && (
              <section className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">子任务</h3>
                {(detail.children ?? []).map((child) => (
                  <div key={child.id} className="mb-1 flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs">
                    <button type="button" className="font-mono underline" onClick={() => onOpenCard(child.id)}>{child.id}</button>
                    <span className="min-w-0 flex-1 truncate">{child.title}</span>
                    <span className="ml-auto rounded-full border px-1.5 py-0.5 text-[10px] text-muted-foreground">{child.status}</span>
                  </div>
                ))}
              </section>
            )}
```

顶部 import 补 `CardBrief` 类型（仅当文件里直接引用了它；上面的写法靠推导，不需要）。

- [ ] **Step 7: 跑两端测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Run: `cd web && npx vitest run`
Expected: 都 PASS。

- [ ] **Step 8: 加关键节点日志**

- 后端：`ChildrenOf` 失败时的 `s.log.Warn`（Step 3 已含），带 card 与 cause。
  **这条是叶子层不打日志的那笔账在此处结清**——`internal/ledger` 按纪律不打，
  handler 层必须补，否则「子任务区为什么空着」永远查不到。
- 前端：**不加**日志。浏览器里没有可采集的日志通道，失败态靠已有的 `error`
  渲染呈现（详情整体失败已有 `role="alert"` 的错误块）。这是判断不是跳过。

- [ ] **Step 9: 加注释**

- `handleCardDetail` 里新增查询上方的两段注释（Step 3 已给出）：「为什么随详情给」
  与「为什么降级不报错」
- `web/src/api/ledger.ts` 的 `CardBrief` 与 `children` 字段注释（Step 6 已给出），
  后者要写清**为什么是可选**

- [ ] **Step 10: 提交**

```bash
git add internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go \
  web/src/api/ledger.ts web/src/app/cards/CardDrawer.tsx web/src/app/cards/CardDrawer.test.tsx
git commit -m "feat(cards): 详情带出直接子卡，抽屉加子任务区"
```

---

### Task 3: 验收写入口与三态 chip

**Files:**
- Modify: `internal/agentd/ledgerapi.go`（注册并实现 `POST /api/cards/{id}/accept`；
  文件头注释订正）
- Modify: `web/src/api/ledger.ts`（`acceptCard`）
- Modify: `web/src/app/cards/CardDrawer.tsx`（验收区加写入口；chip 改三态）
- Test: `internal/agentd/ledgerapi_test.go`、`web/src/app/cards/CardDrawer.test.tsx`

**Interfaces:**
- Consumes: 既有的 `(*ledger.Store).RecordAcceptance(cardID string, verified bool, evidence, actor string) error`
- Produces: `POST /api/cards/{id}/accept`，body `{"evidence":"..."}`，成功 `{"ok":true}`；
  前端 `acceptCard(id, evidence)`

**先读懂再动**：`RecordAcceptance` 自己**不校验证据非空**（`internal/ledger/events.go:214`
只是落一条事件）。「已验必须带证据」这条规则今天由 CLI 的 `card accept` 守着。
新端点必须**自己守**同一条规则并返回 400——不能只靠前端不让空提交，那样 curl 一下
就能落一条没有证据的「已验」。

**「标记未验」不做 UI**，留 CLI 的 `--unverified`：它是补记动作，不是日常。
所以本端点固定 `verified=true`，body 里不收这个字段。

- [ ] **Step 1: 写失败的后端测试**

```go
// TestCardAcceptRecordsEvidence 验收写入口落事件。
func TestCardAcceptRecordsEvidence(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "待验卡")

	code, body := ledgerPost(t, env, "/api/cards/"+card.ID+"/accept",
		`{"evidence":"真机跑了 3 轮，日志见 render.log"}`)
	if code != http.StatusOK {
		t.Fatalf("code=%d body=%s", code, body)
	}
	evs, err := env.ledger.EventsFromAsc([]string{card.ID}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range evs {
		if event.Type != ledger.EvAcceptanceRecorded {
			continue
		}
		found = true
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["verified_on_real_machine"] != true {
			t.Fatalf("应记为已验: %+v", payload)
		}
		if payload["evidence"] != "真机跑了 3 轮，日志见 render.log" {
			t.Fatalf("证据没落对: %+v", payload)
		}
	}
	if !found {
		t.Fatal("缺 acceptance_recorded 事件")
	}
}

// TestCardAcceptRejectsEmptyEvidence 空证据 400——「已验必须带证据」
// 这条规则必须由后端守。只靠前端不让空提交的话，curl 一下就能落一条
// 没有证据的「已验」，而验收记录正是事后唯一能复查的东西。
func TestCardAcceptRejectsEmptyEvidence(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "待验卡")
	for _, bad := range []string{`{"evidence":""}`, `{"evidence":"   "}`, `{}`} {
		code, body := ledgerPost(t, env, "/api/cards/"+card.ID+"/accept", bad)
		if code != http.StatusBadRequest {
			t.Fatalf("body=%s 应 400，实得 %d（%s）", bad, code, body)
		}
	}
}

// TestCardAcceptUnknownCard404 卡不存在走既有的错误翻译。
func TestCardAcceptUnknownCard404(t *testing.T) {
	env := newLedgerEnv(t)
	code, _ := ledgerPost(t, env, "/api/cards/B-不存在/accept", `{"evidence":"x"}`)
	if code != http.StatusNotFound {
		t.Fatalf("应 404，实得 %d", code)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestCardAccept -count=1`
Expected: FAIL —— 路由未注册，返回 404（`TestCardAcceptUnknownCard404` 可能假绿，
另两条必红）。

- [ ] **Step 3: 实现后端**

注册路由（`registerLedgerRoutes` 里，`note` 之后）：

```go
	api.HandleFunc("POST /api/cards/{id}/accept", s.withLedger(s.handleCardAccept))
```

实现（放在 `handleCardNote` 之后）：

```go
// handleCardAccept 记一条「已真机验」验收，body 只收证据。
//
// 为什么不收 verified 字段：标记**未**验是补记动作而不是日常，留 CLI 的
// `card accept --unverified`。界面上只提供「标记已验」这一个方向，语义更窄也更难误点。
//
// 为什么空证据必须在这里拦：RecordAcceptance 自己不校验，「已验必须带证据」
// 这条规则今天只由 CLI 守着。只靠前端不让空提交的话，curl 一下就能落一条
// 没有证据的「已验」——而验收记录正是事后唯一能复查的东西。
func (s *Server) handleCardAccept(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Evidence string `json:"evidence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	id := r.PathValue("id")
	evidence := strings.TrimSpace(req.Evidence)
	if evidence == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "标记已验必须带证据（与 CLI card accept 同规则）",
		})
		return
	}
	actor := "web:" + r.RemoteAddr
	if err := s.ledger.RecordAcceptance(id, true, evidence, actor); err != nil {
		s.log.Error("记验收失败", "card", id, "actor", actor, "cause", err)
		ledgerErr(w, err)
		return
	}
	s.log.Info("已记验收", "card", id, "actor", actor, "evidence_bytes", len(evidence))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

需要 import `strings`（若尚未 import）。

`ledgerapi.go` 的文件头注释第一段现在写着「写动作只有 move/note/answer 三个；
派发、合并等动作由 CLI 承载」——**它已经不成立了**，改成：

```go
// 账本 HTTP API：web 看板的唯一账本通道。薄层——业务全在
// internal/ledger，此处只做解码/调用/编码与错误翻译。写动作：
// move/note/answer/accept 同步返回；step（审阅/合并环节）异步 202，
// 编排在 internal/ledgerstep。实现类派发仍只由 CLI 承载——它要挂 plan 文件，
// 浏览器里没有那个文件。
```

（`step` 那半句在 Task 6 才成立；本 task 先按最终形态写，Task 6 补上实现——
**若执行到此处时 Task 6 尚未完成，这段注释会短暂领先于代码**，这是刻意的：
文件头描述的是本文件的最终职责，Task 6 是同一分支上的后续提交。）

- [ ] **Step 4: 写失败的前端测试**

```tsx
describe('抽屉里的验收', () => {
  it('未验且已完成显示「待真机验」，标记已验要带证据', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B170', Title: '待验卡', Status: '已完成', Attachments: [], AcceptanceCriteria: '判据：全绿' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    const accept = vi.mocked(ledger.acceptCard).mockResolvedValue({ ok: true })
    render(<CardDrawer id="B170" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('待真机验')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /标记已验/ }))
    const box = screen.getByPlaceholderText(/证据/)
    // 空证据不许提交
    expect(screen.getByRole('button', { name: '确认' })).toBeDisabled()
    fireEvent.change(box, { target: { value: '真机跑了 3 轮' } })
    fireEvent.click(screen.getByRole('button', { name: '确认' }))
    await waitFor(() => expect(accept).toHaveBeenCalledWith('B170', '真机跑了 3 轮'))
  })

  it('未验且未完成显示「未验」——三态里这一态原来是缺的', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B171', Title: '进行中的卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B171" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('进行中的卡')
    expect(screen.getByText('未验')).toBeInTheDocument()
    expect(screen.queryByText('待真机验')).not.toBeInTheDocument()
  })
})
```

`vi.mock` 的工厂里要补 `acceptCard: vi.fn().mockResolvedValue({ ok: true })`。

- [ ] **Step 5: 跑前端测试确认它失败**

Run: `cd web && npx vitest run src/app/cards/CardDrawer.test.tsx`
Expected: FAIL —— `acceptCard` 不存在；「未验」找不到（当前是二态，未验一律显示
「待真机验」）。

- [ ] **Step 6: 实现前端**

`web/src/api/ledger.ts`：

```ts
// acceptCard 记一条「已真机验」。证据由后端强制非空（与 CLI card accept 同规则），
// 前端只是不让空提交，不是唯一的那道门。
export const acceptCard = (id: string, evidence: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/accept`, { evidence })
```

`CardDrawer.tsx`：

① 顶部 import 加 `acceptCard`。

② 组件里加状态：

```tsx
  const [acceptOpen, setAcceptOpen] = useState(false)
  const [acceptEvidence, setAcceptEvidence] = useState('')
  const [acceptBusy, setAcceptBusy] = useState(false)
  const [acceptError, setAcceptError] = useState('')
```

③ 加提交函数（照 `submitNote` / `submitMove` 的形态）：

```tsx
  const submitAccept = async () => {
    const evidence = acceptEvidence.trim()
    if (!evidence) return
    setAcceptBusy(true)
    setAcceptError('')
    try {
      await acceptCard(id, evidence)
      setAcceptOpen(false)
      setAcceptEvidence('')
      load()
    } catch (err) {
      // ApiError.message 是后端的规则原文（如「必须带证据」），逐字保留
      setAcceptError(errorMessage(err))
    } finally {
      setAcceptBusy(false)
    }
  }
```

④ chip 改三态。当前是 `{acceptanceInfo.verified ? '已验' : '待真机验'}`，
在 `acceptanceInfo` 附近加一个派生值：

```tsx
  // 验收 chip 三态：已验 / 待真机验（活干完了等验）/ 未验（还没干完）。
  // 原来只有两态，把「还在进行中的卡」也显示成「待真机验」——那会让看板上
  // 一片卡都像在等人验，真正等验的那几张反而看不出来
  const acceptanceLabel = acceptanceInfo.verified ? '已验' : status === '已完成' ? '待真机验' : '未验'
```

把验收区的 `<div className="mb-1.5 font-medium">` 内容换成 `{acceptanceLabel}`，
并在其后加写入口：

```tsx
                {!acceptanceInfo.verified && (
                  !acceptOpen ? (
                    <button type="button" onClick={() => setAcceptOpen(true)}
                      className="mt-2 rounded-md border px-2.5 py-1 text-xs hover:bg-accent">标记已验…</button>
                  ) : (
                    <div className="mt-2 space-y-1.5">
                      <textarea value={acceptEvidence} onChange={(event) => setAcceptEvidence(event.target.value)}
                        rows={3} placeholder="证据：怎么验的、在哪台机器、日志在哪"
                        className="w-full rounded border bg-background px-2 py-1 text-xs" />
                      <div className="flex gap-2">
                        <button type="button" disabled={acceptBusy || !acceptEvidence.trim()} onClick={() => void submitAccept()}
                          className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">确认</button>
                        <button type="button" onClick={() => { setAcceptOpen(false); setAcceptError('') }}
                          className="rounded-md border px-2.5 py-1 text-xs">取消</button>
                      </div>
                      {acceptError && <p role="alert" className="break-words text-xs text-destructive">{acceptError}</p>}
                    </div>
                  )
                )}
```

- [ ] **Step 7: 跑两端测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Run: `cd web && npx vitest run`
Expected: 都 PASS。

**注意**：头部那个 `{acceptanceInfo.verified && <span …>已验</span>}` 徽标仍在，
所以「已验」在页面上会出现两处。这是既有形态（头部徽标 + 验收区标题），
**不改**——但写测试断言时用 `getAllByText` 或更精确的选择器，别被它绊住。

- [ ] **Step 8: 加关键节点日志**

- 后端：成功 Info（带 card / actor / 证据字节数，**不打证据正文**——它可能很长
  且可能含路径）；失败 Error 带 cause（Step 3 已含）。
  **成功路径必须打**：验收是有外部含义的状态变更，「谁在什么时候记了这条」
  只靠事件表能查，但日志是排查时第一眼看的地方。
- 前端：不加日志（同 Task 2 的判断），失败靠 `role="alert"` 呈现。

- [ ] **Step 9: 加注释**

- `handleCardAccept` 的 doc 注释（Step 3 已给出）：两条「为什么」
- `ledgerapi.go` 文件头订正（Step 3 已给出）
- `acceptCard` 的注释（Step 6 已给出）：写清后端才是那道门
- `acceptanceLabel` 上方的注释（Step 6 已给出）：为什么要第三态

- [ ] **Step 10: 提交**

```bash
git add internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go \
  web/src/api/ledger.ts web/src/app/cards/CardDrawer.tsx web/src/app/cards/CardDrawer.test.tsx
git commit -m "feat(cards): 加验收写入口，证据后端强制非空，chip 改三态"
```

---

### Task 4: 环节编排搬进 `internal/ledgerstep`（纯搬迁）

**Files:**
- Create: `internal/ledgerstep/dispatch.go`（`DispatchViaTemplate` 与它的类型）
- Create: `internal/ledgerstep/runner.go`（`StepRunner.Run`，即原 `runStepDispatch` 的编排）
- Modify: `cmd/card_dispatch.go`（删掉搬走的实现，改为装配 + 调用）
- Modify: `cmd/card_node.go`（改为装配 `StepRunner`）
- Move: `cmd/card_dispatch_test.go` 中**与模板派发相关**的用例 → `internal/ledgerstep/dispatch_test.go`

**Interfaces:**
- Consumes: 既有 `ledger.Store` 的 `GetTemplate` / `WorkBranch` / `ReviewRounds` /
  `EffectiveBaseBranch` / `LinkTask` / `RecordDispatch`；既有 `ReviewStep` / `MergeStep` /
  `NewDispatchReview` / `NewLocalObjective` / `NewLocalMerge`
- Produces:
  - `type DispatchOpts struct{ Prompt, Branch, Target, Project, Executor, Model, PlanB64, PlanName, Base, ExistingBranch, Discipline string; NewWorktree bool }`
  - `type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, err error)`
  - `type DispatchResult struct{ Card, Task, Target, Branch, Template string; TemplateVersion int; DisciplineName string }`
  - `type Dispatcher struct{ St *ledger.Store; Transport Transport; Actor string }`
  - `func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)`
  - `type TemplateDispatch struct{ Template, Target, PlanPath, DisciplineOverride string }`
  - `type StepRunner struct{ St *ledger.Store; RepoDir string; Dispatcher *Dispatcher; Endpoints func(target string) (addr, token string, err error); MainLine string }`
  - `func (r *StepRunner) Run(ctx context.Context, cardID, step string) (Outcome, error)`

**这是纯搬迁：行为一字不改。** 判断依据是「搬完之后，同一批测试原样通过」。
若搬迁过程中你想顺手改点什么——**别改**，记下来报告。混在搬迁 diff 里的行为改动
是审核最容易漏掉的东西。

**必须跟着搬过去的东西**（漏掉就等于把上一轮的回归网拆了）：
「纪律块具名化」那轮在 `cmd/card_dispatch_test.go` 里加的
`TestCardDispatchSendsDisciplineName` / `TestCardDispatchNoDisciplineInPrompt` /
`TestCardDispatchOverrideReplacesName` / `TestCardDispatchSnapshotRecordsDisciplineName`。
它们守的是「两份纪律块不再同时在场」这条判据，**断言一字不许改**，只改驱动方式
（从 `runLedgerCLI` 改为直接构造 `Dispatcher` 并注入假 `Transport`）。

`TestCardDispatchClaimAndSnapshot` 与 `TestCardDispatchFailureReleasesLease`
**留在 `cmd`**：它们守的是认领与租约，那是 CLI 层的语义。

- [ ] **Step 1: 先跑一遍基线，记下当前绿的范围**

```bash
go test ./cmd/ ./internal/ledgerstep/ -count=1
```

把通过的用例名记下来。搬完之后这批必须**原样**还在（可能换了包）。
这是纯搬迁的验收锚点——没有它，"行为不变" 只是一句声明。

- [ ] **Step 2: 建 `internal/ledgerstep/dispatch.go`**

文件头注释：

```go
// 模板派发的共用段：取模板 → 算分支与基线 → 拼 prompt → 经注入的 Transport
// 派发 → 回链挂账 → 落 dispatched 快照。
//
// 职责：把「一张卡按某个模板派出去」这件事收口成一处，CLI 与 agentd 共用。
// 边界：
//   - 不认领、不动卡状态——实现类派发在调用前自行 CAS 认领，环节派发不认领
//   - 不做网络——传输经 Transport 注入，本文件不知道对端是 HTTP 还是别的什么
//   - 不解析纪律块——只把角色名传下去，正文由 agentd 解析注入
package ledgerstep
```

把 `cmd/card_dispatch.go` 里 `dispatchViaTemplate` 的**函数体逐行搬过来**，只做
三类机械替换：

| 原 | 新 |
|---|---|
| `st` | `d.St` |
| `actor` | `d.Actor` |
| `dispatchTransportWithOpts(dispatchRequest{...})` | `d.Transport(ctx, DispatchOpts{...})` |
| `dispatchResult` / `dispatchRequest` | `DispatchResult` / `DispatchOpts`（字段名首字母大写） |
| 参数 `tplName, targetFlag, planPath, disciplineOverride` | `req.Template, req.Target, req.PlanPath, req.DisciplineOverride` |

**注释一并搬过去，一个字都不要丢**——尤其审阅分支那三段（为什么不复用固定名、
为什么不直接检出工作分支、为什么起点必须是工作分支的当前提交），它们各自对应
一次真机踩坑。

`Dispatcher.ViaTemplate` 的 doc 注释新写：

```go
// ViaTemplate 按模板把一张卡派出去。
//
// 参数：c 卡；req 模板名、目标机、可选 plan 路径与纪律块角色名覆盖。
// 返回：派发结果（含 task id、分支、模板版本、纪律块角色名）。
//
// 注意：**不含认领语义**。实现类派发在调用前自行 CAS 认领；环节派发
// （审阅/合并）刻意不认领——它们是待审阅卡上的动作，认领会把卡拉回进行中。
//
// req.PlanPath 按**调用方进程的 CWD** 解析。agentd 一侧永远传空串：
// 浏览器里没有 plan 文件，实现类派发也不从界面走。
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error) {
```

- [ ] **Step 3: 建 `internal/ledgerstep/runner.go`**

把 `cmd/card_node.go` 的 `runStepDispatch` 与 `reviewDispatchFn` 搬过来，去掉 cobra
与 JSON 编码（那是 CLI 的呈现，留在 CLI）：

```go
// 环节入口：把「跑一次 review / merge 环节」收口成一个方法，CLI 与看板
// 按钮共用同一份装配逻辑。
//
// 边界：只做装配与分发，决策在 node.go 的两个 Step；本文件不碰 HTTP、
// 不碰 cobra、不做输出编码——那些是各调用方自己的呈现层。
package ledgerstep

// StepRunner 环节执行的装配器。依赖全部显式注入，调用方各填各的：
//
//	         RepoDir              Dispatcher.Transport
//	CLI      --repo（缺省 CWD）   CLI 的 dispatch 通道
//	agentd   项目登记解析出的路径  agentd 自己的 client
type StepRunner struct {
	St         *ledger.Store
	RepoDir    string
	Dispatcher *Dispatcher
	Endpoints  func(target string) (addr, token string, err error)
	// MainLine 主线分支名，透传给 MergeStep；空则用它的缺省值。
	MainLine string
}

// Run 跑一次环节。
//
// 参数：cardID 卡；step 只认 "review" | "merge"。
// 返回：Outcome（下一步动作 + 裁决 + 理由）；step 不认识或环节内部失败时返回错误。
//
// 阻塞行为：**审阅环节会一直阻塞到被派出去的 task 跑到回合终态**
// （几分钟到几十分钟，executor 挂在 waiting_answer 时更久）。调用方要么
// 自己在 goroutine 里跑（agentd 就是这么做的），要么接受前台阻塞（CLI）。
func (r *StepRunner) Run(ctx context.Context, cardID, step string) (Outcome, error) {
	switch step {
	case "review":
		...
	case "merge":
		...
	}
	return Outcome{}, fmt.Errorf("环节只认 review|merge，收到 %q", step)
}
```

`review` 分支里的 `RunReview` 装配：

```go
		runner := &ReviewStep{
			St: r.St, Step: "review",
			RunReview: NewDispatchReview(r.St, r.reviewDispatch(), r.Endpoints),
		}
		return runner.RunOnce(ctx, cardID)
```

`r.reviewDispatch()` 是原 `reviewDispatchFn` 的搬迁版（把 `dispatchViaTemplate`
换成 `r.Dispatcher.ViaTemplate`，`cardDispatchTarget` 换成 `r.Dispatcher` 上的
目标机 —— **目标机原本来自 CLI 的 `--target` 全局变量**，搬迁后要变成显式字段。
把它加到 `TemplateDispatch` 已有的 `Target` 上，由 `reviewDispatch` 从
`StepRunner` 的新字段读；若 `StepRunner` 上没有合适的字段，加一个 `Target string`
并在 doc 注释里说明「空则用模板里的 target」——这与原逻辑一致，
见 `ViaTemplate` 里的 `if target == "" { target = tpl.Def.Target }`）。

`merge` 分支：

```go
		runner := &MergeStep{
			St:        r.St,
			Objective: NewLocalObjective(r.RepoDir, r.St),
			DoMerge:   NewLocalMerge(r.RepoDir, r.St),
			MainLine:  r.MainLine,
		}
		return runner.RunOnce(ctx, cardID)
```

- [ ] **Step 4: 改 CLI 侧为装配**

`cmd/card_dispatch.go`：删掉 `dispatchViaTemplate` 的实现与 `dispatchResult` /
`dispatchRequest` 类型；改为一个薄适配：

```go
// cliTransport 把 CLI 的派发通道适配成 ledgerstep.Transport。
//
// 保留 dispatchTransportWithOpts 这层间接：它是 cmd 包既有的测试缝
//（swapDispatchTransport / swapDispatchTransportWithOpts），认领与租约的
// 用例还挂在上面。
func cliTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, error) {
	return dispatchTransportWithOpts(dispatchRequest{ ... })
}
```

——**注意**：这意味着 `cmd` 里的 `dispatchRequest` **不删**，它降级成传输层的入参。
`ViaTemplate` 用的是 `ledgerstep.DispatchOpts`，两者字段一一对应。多一层结构体转换，
换来 cmd 既有测试缝不动。**若你判断更好的做法是让 `dispatchTransportWithOpts`
直接收 `ledgerstep.DispatchOpts`（省掉这层转换），可以那么做——但要在 ledger 里
写下理由，并确认 `swapDispatchTransport` 的四标量缝仍然可用。**

`cmd/card_node.go` 的 `runStepDispatch` 改为装配 + 编码：

```go
func runStepDispatch(cmd *cobra.Command, st *ledger.Store, id, step, actor string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	runner := &ledgerstep.StepRunner{
		St:      st,
		RepoDir: cardDispatchRepo,
		Dispatcher: &ledgerstep.Dispatcher{
			St: st, Transport: cliTransport, Actor: actor,
		},
		Endpoints: targetEndpoint,
		Target:    cardDispatchTarget,
	}
	outcome, err := runner.Run(ctx, id, step)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
}
```

`cmd/card_node.go` 的文件头注释订正——它原本写「看板动作按钮也**应**调用这一实现」，
现在这件事真的发生了：

```go
// card dispatch --step 的 CLI 装配层：构造 ledgerstep.StepRunner 并把结果编码成 JSON。
// 编排本身在 internal/ledgerstep——看板按钮（经 /api/cards/{id}/step）装配的是同一个
// StepRunner，只是注入不同的仓路径与传输，单一编排真相源由此落实。
```

- [ ] **Step 5: 搬测试**

按本 task 开头的清单，把四条纪律块相关用例搬进
`internal/ledgerstep/dispatch_test.go`，驱动方式改为直接构造：

```go
func TestViaTemplateSendsDisciplineName(t *testing.T) {
	st := newTestStore(t)
	// 建卡 + 落模板：照抄 internal/ledger 测试里现成的建法
	...
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		got = opts
		return "T-fake-1", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if got.Discipline != "implement" {
		t.Fatalf("请求里应带角色名 implement，实得 %q", got.Discipline)
	}
}
```

其余三条同法。**断言一字不改**——它们守的是「两份纪律块不再同时在场」。
特别是 `TestViaTemplateNoDisciplineInPrompt` 那四个 `strings.Contains` 检查，
逐字保留。

- [ ] **Step 6: 跑测试，与 Step 1 的基线逐条对照**

```bash
go test ./cmd/ ./internal/ledgerstep/ ./internal/ledger/ -count=1
go build ./...
```
Expected: 全绿，且 Step 1 记下的用例名**一条不少**（可能换了包）。少了任何一条 →
停下来，说明搬迁丢了东西。

- [ ] **Step 7: 加关键节点日志**

搬迁**不改**既有日志（`ReviewStep.RunOnce` / `MergeStep.RunOnce` 里已有 `slog`）。
新增的两处装配点各补一条 Info：

```go
	// StepRunner.Run 入口
	slog.Default().Info("进入环节", "card", cardID, "step", step, "repo_dir", r.RepoDir)
```

```go
	// Dispatcher.ViaTemplate 派发成功后
	slog.Default().Info("模板派发完成", "card", c.ID, "template", tpl.Name,
		"task", taskID, "target", target, "branch", snapshotBranch, "discipline", disciplineName)
```

**为什么这两条要有**：编排现在有两个调用方（CLI 与 agentd），出问题时第一个要
回答的问题是「这次是谁在跑、用的哪个仓路径」。没有这两行，agentd 侧的异步环节
在日志里完全隐形。

- [ ] **Step 8: 加注释**

- 两个新文件的文件头注释（Step 2、Step 3 已给出）：职责 + 边界
- `Dispatcher.ViaTemplate` 的 doc 注释（Step 2 已给出）：含「不含认领语义」与
  「PlanPath 按调用方 CWD 解析、agentd 传空」
- `StepRunner` 的字段表注释与 `Run` 的 doc 注释（Step 3 已给出）：
  **`Run` 必须写明阻塞行为**——审阅会阻塞到 task 终态，这是 Task 5 必须异步的根由
- `cmd/card_node.go` 文件头订正（Step 4 已给出）
- `cliTransport` 的注释（Step 4 已给出）：为什么保留这层间接
- 搬过来的行内注释**一个字都不要丢**

- [ ] **Step 9: 提交**

```bash
git add internal/ledgerstep/ cmd/card_dispatch.go cmd/card_node.go cmd/card_dispatch_test.go
git commit -m "refactor(ledgerstep): 环节编排从 cmd 搬进共用包，CLI 改为装配"
```

---

### Task 5: agentd 侧的环节装配与在飞集合

**Files:**
- Create: `internal/agentd/cardstep.go`（装配 `StepRunner` + 在飞集合 + 异步执行）
- Test: `internal/agentd/cardstep_test.go`

**Interfaces:**
- Consumes: Task 4 的 `ledgerstep.StepRunner` / `Dispatcher` / `Transport`；
  既有 `(*store.Store).GetProjectLocationByName(name) (proto.ProjectLocation, error)`；
  既有 `config.Config.Targets`
- Produces:
  - `func (s *Server) startCardStep(cardID, step, actor string) error`
  - 哨兵 `var errStepInFlight = errors.New("该卡已有环节在运行")`

**先读懂再动**，三件事：

① **仓路径不猜。** spec §4.2 定死：卡的 `Project` 解析到本机项目登记
（`s.st.GetProjectLocationByName`）；解析不到就**拒绝并说清**
（「卡 B12 的项目 `foo` 未在本机登记，先 `handoff project add`」）。
不要退回 agentd 的 CWD——那几乎一定是错的目录，而错在这里会让 merge 环节
往错误的仓库里 push。

② **目标机走 agentd 自己的配置。** `config.Config.Targets` 是 CLI 与 agentd 共用的
类型，agentd 侧从 `s.cfg.Load()` 的快照读。解析不到同样拒绝。

③ **异步是硬约束。** `StepRunner.Run` 的审阅分支会阻塞到 task 终态（几分钟到几十分钟），
HTTP 请求扛不住。所以 `startCardStep` 只做**同步的前置校验 + 占位**，然后起 goroutine，
立刻返回。

- [ ] **Step 1: 写失败的测试**

```go
// TestStartCardStepRejectsSecondInFlight 同一张卡同时只允许一个环节在飞。
// 为什么必须拦：两个 merge 环节并发跑同一个仓路径，会在同一个工作区里
// 互相踩 git 状态——而那一侧的失败信息只会是一句莫名其妙的 git 报错。
func TestStartCardStepRejectsSecondInFlight(t *testing.T) {
	s := newStepTestServer(t)
	release := s.holdCardStep("B1") // 测试辅助：直接往在飞集合里塞一个
	defer release()
	if err := s.startCardStep("B1", "review", "web:test"); !errors.Is(err, errStepInFlight) {
		t.Fatalf("第二个环节应被拒，实得 %v", err)
	}
}

// TestStartCardStepUnknownProjectRefuses 项目没在本机登记就拒绝，不猜路径。
// 猜错的代价：merge 环节会往错误的仓库 push——外部可见且不易撤回。
func TestStartCardStepUnknownProjectRefuses(t *testing.T) {
	s := newStepTestServer(t)
	seedCardWithProject(t, s, "B2", "从未登记的项目")
	err := s.startCardStep("B2", "merge", "web:test")
	if err == nil {
		t.Fatal("未登记项目应被拒")
	}
	if !strings.Contains(err.Error(), "未在本机登记") || !strings.Contains(err.Error(), "从未登记的项目") {
		t.Fatalf("错误要说清是哪个项目、该怎么办：%v", err)
	}
}

// TestStartCardStepBadStepRefuses 只认 review|merge。
func TestStartCardStepBadStepRefuses(t *testing.T) {
	s := newStepTestServer(t)
	seedCardWithProject(t, s, "B3", "demo")
	if err := s.startCardStep("B3", "implement", "web:test"); err == nil {
		t.Fatal("implement 不是环节，应被拒")
	}
}

// TestStartCardStepReleasesSlotOnFinish 环节跑完要把位子让出来，
// 否则一张卡审一次之后就再也审不了了——而且这个 bug 要等到第二次点才发现。
func TestStartCardStepReleasesSlotOnFinish(t *testing.T) {
	s := newStepTestServer(t)
	seedCardWithProject(t, s, "B4", "demo")
	done := make(chan struct{})
	s.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		close(done)
	}
	if err := s.startCardStep("B4", "review", "web:test"); err != nil {
		t.Fatalf("首次应放行: %v", err)
	}
	<-done
	waitFor(t, func() bool { return !s.cardStepInFlight("B4") })
	if err := s.startCardStep("B4", "review", "web:test"); err != nil {
		t.Fatalf("跑完之后应能再发起: %v", err)
	}
}
```

`newStepTestServer` / `seedCardWithProject` / `holdCardStep` / `cardStepInFlight` /
`waitFor` 是本 task 要写的测试辅助。**先 grep `internal/agentd/*_test.go` 里现成的
建 Server 辅助**（`ledgerapi_test.go` 的 `newLedgerEnv` 就建了带 ledger 的环境），
能复用就复用；`runStepFn` 是给 `Server` 加的一个可替换字段，**只为测试存在**——
在它的字段注释里写明这一点，并说明生产恒为默认实现。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestStartCardStep -count=1`
Expected: **编译失败**，`startCardStep` 未定义。

- [ ] **Step 3: 实现**

新建 `internal/agentd/cardstep.go`：

```go
// 看板环节动作的 agentd 侧装配：把一张卡的 review / merge 环节跑起来。
//
// 职责：
//   - 解析仓路径（项目登记）与目标机（配置），装配 ledgerstep.StepRunner
//   - 守「同一张卡同时只允许一个环节在飞」
//   - 起 goroutine 异步执行，HTTP 侧立刻返回
//
// 边界：
//   - 不做编排——那在 internal/ledgerstep，CLI 与本文件共用同一份
//   - 不做恢复：在飞集合是进程内状态，agentd 重启即清空。此时卡上留下的是
//     一次没有终态事件的环节，与 CLI 跑到一半被 Ctrl-C 的形态一致，人从
//     timeline 看得出来。本轮不做恢复是刻意的取舍，不是遗漏
//   - 不做实现类派发：那要挂 plan 文件，浏览器里没有
package agentd
```

在 `Server` 上加两个字段（**放在既有字段之后，跟上注释**）：

```go
	// cardStepMu / cardStepFlight 守「同一张卡同时只允许一个环节在飞」。
	// 进程内状态：重启即清空，见 cardstep.go 的边界说明。
	cardStepMu     sync.Mutex
	cardStepFlight map[string]bool
```

主实现：

```go
// errStepInFlight 表示该卡已有环节在跑，调用方应答 409。
var errStepInFlight = errors.New("该卡已有环节在运行")

// startCardStep 起一个卡环节。
//
// 参数：cardID 卡；step 只认 "review" | "merge"；actor 发起人（web:<addr>）。
// 返回：前置校验失败时返回错误（调用方翻成 400/404/409）；校验通过后
// **立刻返回 nil**，环节在后台 goroutine 里跑。
//
// 为什么必须异步：审阅环节会阻塞到被派出去的 task 跑到回合终态——几分钟到
// 几十分钟，executor 挂在 waiting_answer 时更久。HTTP 请求扛不住这个时长，
// 界面靠已有的卡事件流看进展。
//
// 注意：返回 nil **不代表环节成功**，只代表它启动了。成败落在卡的事件流上。
func (s *Server) startCardStep(cardID, step, actor string) error {
	if step != "review" && step != "merge" {
		return fmt.Errorf("环节只认 review|merge，收到 %q", step)
	}
	card, err := s.ledger.GetCard(cardID)
	if err != nil {
		return err
	}
	// 仓路径不猜：卡的项目必须在本机登记过。猜错的代价是 merge 往错误的
	// 仓库 push——外部可见且不易撤回，宁可拒绝并说清怎么办
	loc, err := s.st.GetProjectLocationByName(card.Project)
	if err != nil {
		return fmt.Errorf("卡 %s 的项目 %q 未在本机登记，先 handoff project add: %w",
			cardID, card.Project, err)
	}
	if !s.claimCardStep(cardID) {
		return fmt.Errorf("%w: %s 的 %s 环节正在运行", errStepInFlight, cardID, step)
	}
	runner := &ledgerstep.StepRunner{
		St:      s.ledger,
		RepoDir: loc.Path,
		Dispatcher: &ledgerstep.Dispatcher{
			St: s.ledger, Transport: s.stepTransport, Actor: actor,
		},
		Endpoints: s.targetEndpoint,
	}
	s.log.Info("环节已受理", "card", cardID, "step", step, "actor", actor, "repo_dir", loc.Path)
	go func() {
		defer s.releaseCardStep(cardID)
		s.runStepFn(context.Background(), runner, cardID, step)
	}()
	return nil
}

// runStep 是 runStepFn 的生产实现：跑环节并把结果记进日志。
//
// 为什么错误只进日志不往上抛：调用它的 goroutine 没有上游。环节的成败
// 由 ledgerstep 落进卡的事件流，那是界面看得见的地方；日志是排查时的第二现场。
func (s *Server) runStep(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
	outcome, err := runner.Run(ctx, cardID, step)
	if err != nil {
		s.log.Error("环节失败", "card", cardID, "step", step, "cause", err)
		return
	}
	s.log.Info("环节结束", "card", cardID, "step", step,
		"action", string(outcome.Action), "reason", outcome.Reason)
}
```

在飞集合的三个小方法（`claimCardStep` / `releaseCardStep` / `cardStepInFlight`），
以及 `stepTransport`（用 `internal/client` 派发，照抄
`internal/ledgerstep/wire.go` 里 `client.New(addr, token)` 的用法）与
`targetEndpoint`（从 `s.cfg.Load().Targets` 取，照抄
`cmd/card_dispatch.go:99` 的形态并把错误文案改成 agentd 语气）。

`runStepFn` 字段：

```go
	// runStepFn 是环节执行的落点，**只为测试可替换而存在**：生产恒为 s.runStep。
	// 环节要跑几十分钟且会真派 task，单测替换掉它才能验「在飞集合」这类装配逻辑。
	runStepFn func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string)
```

在 `NewServer` 里填默认值 `s.runStepFn = s.runStep`，并初始化
`s.cardStepFlight = map[string]bool{}`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1 -race`
Expected: PASS。**这一条必须带 `-race`**：在飞集合是本 task 唯一的共享可变状态，
goroutine 与 HTTP handler 并发访问它。

- [ ] **Step 5: 加关键节点日志**

已包含在实现里，逐条确认：

- 受理：Info，带 card / step / actor / repo_dir（**repo_dir 必须打**——
  「它到底在哪个仓库里跑的」是这类异步动作第一个要回答的问题）
- 环节结束：Info，带 action 与 reason
- 环节失败：Error，带 cause
- 前置拒绝三条（step 不认识 / 项目未登记 / 已有环节在飞）：**不打日志**，
  它们作为错误返回给 HTTP 层，由那里统一记（避免同一件事记两遍）。
  这是判断不是跳过。

- [ ] **Step 6: 加注释**

- `cardstep.go` 文件头（Step 3 已给出）：职责 + **三条边界**，
  其中「重启即清空、本轮不做恢复」要写明是刻意取舍
- `startCardStep` doc 注释（Step 3 已给出）：含「为什么必须异步」与
  「返回 nil 不代表成功」
- `runStep` doc 注释（Step 3 已给出）：含「为什么错误只进日志」
- `Server` 上三个新字段的注释（Step 3 已给出），`runStepFn` 要写明只为测试存在
- 仓路径解析处的行内注释（Step 3 已给出）：猜错的代价

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/cardstep.go internal/agentd/cardstep_test.go internal/agentd/server.go
git commit -m "feat(agentd): 卡环节异步执行，仓路径走项目登记，同卡单环节在飞"
```

---

### Task 6: `POST /api/cards/{id}/step` 与抽屉两个按钮

**Files:**
- Modify: `internal/agentd/ledgerapi.go`（注册并实现 `handleCardStep`）
- Modify: `web/src/api/ledger.ts`（`runCardStep`）
- Modify: `web/src/app/cards/CardDrawer.tsx`（「环节动作」区加两个按钮）
- Test: `internal/agentd/ledgerapi_test.go`、`web/src/app/cards/CardDrawer.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `(*Server).startCardStep`、`errStepInFlight`
- Produces: `POST /api/cards/{id}/step`，body `{"step":"review"|"merge"}`，
  成功 **202** `{"ok":true}`；前端 `runCardStep(id, step)`

- [ ] **Step 1: 写失败的后端测试**

```go
// TestCardStepReturns202 受理即返回 202——环节要跑几十分钟，
// 200 会让前端以为它已经做完了。
func TestCardStepReturns202(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCardWithProject(t, env, "demo")
	env.server.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {}

	code, body := ledgerPost(t, env, "/api/cards/"+card.ID+"/step", `{"step":"review"}`)
	if code != http.StatusAccepted {
		t.Fatalf("应 202，实得 %d（%s）", code, body)
	}
}

// TestCardStepSecondReturns409 同卡第二个环节 409 并说清冲突原因。
func TestCardStepSecondReturns409(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCardWithProject(t, env, "demo")
	release := env.server.holdCardStep(card.ID)
	defer release()

	code, body := ledgerPost(t, env, "/api/cards/"+card.ID+"/step", `{"step":"review"}`)
	if code != http.StatusConflict {
		t.Fatalf("应 409，实得 %d", code)
	}
	if !strings.Contains(body, card.ID) || !strings.Contains(body, "正在运行") {
		t.Fatalf("409 要说清是哪张卡的什么在跑：%s", body)
	}
}

// TestCardStepRejectsImplement implement 不是环节——它要挂 plan 文件，
// 浏览器里没有那个文件，只能走 CLI。
func TestCardStepRejectsImplement(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCardWithProject(t, env, "demo")
	code, _ := ledgerPost(t, env, "/api/cards/"+card.ID+"/step", `{"step":"implement"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("应 400，实得 %d", code)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestCardStep -count=1`
Expected: FAIL —— 路由未注册（404）。

- [ ] **Step 3: 实现后端**

注册：

```go
	api.HandleFunc("POST /api/cards/{id}/step", s.withLedger(s.handleCardStep))
```

实现：

```go
// handleCardStep 发起一个卡环节（审阅/合并），受理即 202。
//
// 为什么是 202 而不是 200：环节要跑几分钟到几十分钟，200 会让前端以为
// 它已经做完了。202 的语义正是「收到了，正在做」，界面据此把按钮置灰并
// 提示「进展见下方 Timeline」。
//
// 为什么不支持 implement：实现派发通常要挂 plan 文件，浏览器里没有那个文件。
// 它留在 CLI，这是交接文档「按**环节**派发」的字面含义。
func (s *Server) handleCardStep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Step string `json:"step"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	id := r.PathValue("id")
	actor := "web:" + r.RemoteAddr
	err := s.startCardStep(id, req.Step, actor)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	case errors.Is(err, errStepInFlight):
		s.log.Warn("环节被拒：已有在飞", "card", id, "step", req.Step, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ledger.ErrNotFound):
		ledgerErr(w, err)
	default:
		// 其余都是前置校验失败（环节名不认、项目未登记）：这些是调用方能改的，
		// 400 比 500 更准确，且错误原文里已经写了该怎么办
		s.log.Warn("环节被拒", "card", id, "step", req.Step, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}
```

- [ ] **Step 4: 写失败的前端测试**

```tsx
describe('抽屉里的环节动作', () => {
  it('派发审阅点一次即置灰并提示看 Timeline', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B180', Title: '待审卡', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    const run = vi.mocked(ledger.runCardStep).mockResolvedValue({ ok: true })
    render(<CardDrawer id="B180" onClose={() => {}} onOpenCard={() => {}} />)
    const button = await screen.findByRole('button', { name: /派发审阅/ })
    fireEvent.click(button)
    await waitFor(() => expect(run).toHaveBeenCalledWith('B180', 'review'))
    expect(await screen.findByText(/进展见下方 Timeline/)).toBeInTheDocument()
  })

  it('409 原地显示冲突原因，不吞掉后端文案', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B181', Title: '待审卡', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    vi.mocked(ledger.runCardStep).mockRejectedValue(new Error('B181 的 review 环节正在运行'))
    render(<CardDrawer id="B181" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /合入集成分支/ }))
    expect(await screen.findByText(/正在运行/)).toBeInTheDocument()
  })

  it('不提供「派发实现」——它要挂 plan 文件，浏览器里没有', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ID: 'B182', Title: '卡', Status: '待办', Attachments: [], AcceptanceCriteria: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '',
      decisions: [], needs: '', children: [],
    })
    render(<CardDrawer id="B182" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('卡')
    expect(screen.queryByRole('button', { name: /派发实现/ })).not.toBeInTheDocument()
  })
})
```

`vi.mock` 工厂补 `runCardStep: vi.fn().mockResolvedValue({ ok: true })`。

- [ ] **Step 5: 跑前端测试确认它失败**

Run: `cd web && npx vitest run src/app/cards/CardDrawer.test.tsx`
Expected: FAIL —— 找不到「派发审阅」按钮。

- [ ] **Step 6: 实现前端**

`web/src/api/ledger.ts`：

```ts
// runCardStep 发起一个卡环节。后端受理即 202——环节要跑几分钟到几十分钟，
// 这个 Promise resolve 只代表「收到了」，进展看卡的事件流。
export const runCardStep = (id: string, step: 'review' | 'merge') =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/step`, { step })
```

`CardDrawer.tsx` 的「环节动作」区，在「转移状态…」之前加两个按钮：

```tsx
  const [stepBusy, setStepBusy] = useState<'review' | 'merge' | null>(null)
  const [stepStarted, setStepStarted] = useState<'review' | 'merge' | null>(null)
  const [stepError, setStepError] = useState('')

  const startStep = async (step: 'review' | 'merge') => {
    setStepBusy(step)
    setStepError('')
    try {
      await runCardStep(id, step)
      // 受理即置灰：环节是异步的，再点一次只会撞 409。进展在 Timeline 上
      setStepStarted(step)
      load()
    } catch (err) {
      // 409 的冲突原因是后端写的原文（哪张卡的什么环节在跑），逐字显示
      setStepError(errorMessage(err))
    } finally {
      setStepBusy(null)
    }
  }
```

按钮：

```tsx
              <div className="mb-2 flex flex-wrap gap-2">
                <button type="button" disabled={stepBusy !== null || stepStarted !== null}
                  onClick={() => void startStep('review')}
                  className="rounded-md border px-2.5 py-1 text-xs hover:bg-accent disabled:opacity-50">⇆ 派发审阅</button>
                <button type="button" disabled={stepBusy !== null || stepStarted !== null}
                  onClick={() => void startStep('merge')}
                  className="rounded-md border px-2.5 py-1 text-xs hover:bg-accent disabled:opacity-50">⇣ 合入集成分支</button>
              </div>
              {stepStarted && <p className="mb-2 text-xs text-muted-foreground">已发起，进展见下方 Timeline。</p>}
              {stepError && <p role="alert" className="mb-2 break-words text-xs text-destructive">{stepError}</p>}
```

「转移状态…」按钮原样保留在其后。

- [ ] **Step 7: 跑两端测试确认通过**

Run: `go test ./internal/agentd/ -count=1 -race`
Run: `cd web && npx vitest run`
Expected: 都 PASS。

- [ ] **Step 8: 加关键节点日志**

- 后端：两条 Warn（409 与 400 分支，各带 card / step / cause）已在 Step 3 含。
  **受理成功不在这里打**——Task 5 的 `startCardStep` 已经打过「环节已受理」，
  同一件事记两遍只会让日志更难读。这是判断不是跳过。
- 前端：不加日志（同 Task 2）。

- [ ] **Step 9: 加注释**

- `handleCardStep` doc 注释（Step 3 已给出）：**两条「为什么」**（202 的理由、
  为什么不支持 implement）
- `default` 分支上方的行内注释（Step 3 已给出）：为什么是 400 不是 500
- `runCardStep` 的注释（Step 6 已给出）：resolve 只代表受理
- `startStep` 里两处行内注释（Step 6 已给出）

- [ ] **Step 10: 提交**

```bash
git add internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go \
  web/src/api/ledger.ts web/src/app/cards/CardDrawer.tsx web/src/app/cards/CardDrawer.test.tsx
git commit -m "feat(cards): 抽屉可发起审阅与合并环节，受理即 202"
```

---

### Task 7: 全量门与整分支终审

**Files:**
- Create: `docs/superpowers/ledgers/2026-08-20-workbench-a-group-execution.md`（执行账本）

**Interfaces:**
- Consumes: Task 1-6 的全部产出
- Produces: 无

- [ ] **Step 1: 格式与静态检查**

```bash
gofmt -l . | grep -v '^web/'
git diff --check
go build ./...
go vet ./...
```
Expected: 四条全部无输出。**`gofmt` 这条必跑**——测试全绿不等于格式干净。

- [ ] **Step 2: 全量测试**

```bash
go test ./... -count=1
go test ./internal/agentd/ ./internal/ledgerstep/ -count=1 -race
```

把**实际结果原文**记进账本：通过的包数、失败的包与用例名。基线上本来就红的
环境敏感项如实记「基线即红」，**不改无关模块**。

- [ ] **Step 3: 前端门**

```bash
cd web && npx tsc --noEmit
cd web && npx vitest run
```
Expected: tsc 退出 0；vitest 全绿。

`node_modules` 不在时 `npx tsc` 会「成功」得很像回事，**别用
`npx tsc --noEmit 2>&1 | tail -3 && echo ok` 这种写法**——`&&` 绑的是 `tail`，
它永远为真。先确认 `web/node_modules` 存在（不存在就 `npm install`），
再直接看 `npx tsc --noEmit` 自己的退出码。

- [ ] **Step 4: 红线审计**

逐条跑，每条都必须**无输出**：

```bash
grep -rn 'fmt.Printf' --include='*.go' internal/ledgerstep/ internal/agentd/cardstep.go internal/agentd/ledgerapi.go
```
（日志必须走 slog。）

```bash
grep -rn 'console\.log' web/src/app/cards/ web/src/api/ledger.ts
```
（前端不留调试输出。）

```bash
grep -rn 'dispatchViaTemplate\|runStepDispatch' --include='*.go' cmd/ | grep -v 'ledgerstep\.'
```
（编排已搬走，`cmd/` 里不该再有这两个旧标识符的定义；只剩装配调用。
若 `cmd/card_node.go` 的函数仍叫 `runStepDispatch`（CLI 入口，合理），
本条会命中它——那是可接受的，在账本里注明即可。）

```bash
grep -rn '派发实现' web/src/app/cards/
```
（不做这个按钮。）

- [ ] **Step 5: 逐条对判据自查**

对照 Global Constraints 与下列判据，逐条写下**实际结果**（不是「应该」）：

1. 抽屉能发起审阅与合并，受理返回 202，同卡第二次 409 且原文说清是哪张卡
2. 不存在「派发实现」按钮
3. 子任务区只列直接子卡，孙卡不出现，空时整区不渲染
4. 验收空证据被**后端**拒（400），不是只靠前端
5. 验收 chip 三态各自可达
6. 项目未在本机登记时环节被拒，错误里说清项目名与 `handoff project add`
7. 编排搬迁后 Task 4 Step 1 记下的用例**一条不少**
8. 「纪律块具名化」那四条用例的断言一字未改

**没跑到结果的不许写结论**；跑了但失败就贴原始报错原文。

- [ ] **Step 6: 自我双裁决**

对整分支 diff 做两次裁决并各写结论：

- **spec 符合性**：要求全实现、没有多做。特别检查：有没有新建
  `internal/cardstep`（Global Constraints 明令用既有的 `ledgerstep`）、
  有没有把「纯搬迁」的 Task 4 混进行为改动。
- **代码质量**：日志覆盖（每个错误分支带上下文与 cause、成功路径不静默）、
  注释覆盖（新文件头注释、导出函数 doc 注释、非显然分支的「为什么」）、
  无 `fmt.Printf` / `console.log`、`-race` 干净。

**特别自查一条**：既有 `_test.go` 里被你改动过的每一处，回答「它守的语义是什么、
本次是否仍成立」。改夹具能让红测试变绿，但网就不再罩它被写出来要罩的东西。

- [ ] **Step 7: 提交账本**

```bash
git add docs/superpowers/ledgers/2026-08-20-workbench-a-group-execution.md
git commit -m "docs(ledger): 工作台 A 组全量门与终审"
```

---

## 附一：审核者本地验收清单（**不派发**，协调者执行）

以下要真机驱动 handoff 自身（起 agentd、真派 task、看浏览器），与执行纪律块的
「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」**直接冲突**，
故意留在派发范围之外。

**A. 造靶子** —— 起隔离 agentd 实例（独立 DataDir + 端口、`ledger.enabled: true`），
**绝不重启 launchd 托管的生产 agentd**。用 `.claude/launch.json` 里的 `web-demo`
那条起前端指过去。

**B. 真机走查三条 UI** —— 子任务区（造父子卡，点 id 能跳转）；验收写入口
（空证据提交不动、填了能落事件、chip 由「待真机验」变「已验」）；环节按钮
（点「派发审阅」看到置灰 + Timeline 出新事件）。

**C. 判据：审阅环节端到端** —— 这是本轮最承重的一条。单测只能证明装配对了，
证明不了环节真能把 task 派出去、等到终态、解析出裁决。造一张有工作分支的卡，
点「派发审阅」，盯 Timeline 直到出现 `review_verdict`。

**D. 判据：409 与项目未登记** —— 连点两次「派发审阅」看第二次的提示；
把卡的项目改成未登记的名字，确认拒绝文案里有项目名与 `handoff project add`。

**E. 判据：合并环节往哪个仓库 push** —— merge 是外部可见且不易撤回的动作。
在隔离实例上确认它跑的是**项目登记解析出的路径**，不是 agentd 的 CWD。
用一个只有本地的假 origin 造靶子，别对着真仓库验。

**F. 清理** —— 停隔离实例，删掉临时 DataDir。

## 附二：本 plan 明确不做

- 不新建 `internal/cardstep`（用既有的 `internal/ledgerstep`，理由见 Global Constraints）。
- 不做「派发实现」按钮。
- 不做子任务的递归展开与 rollup 聚合。
- 不做「标记未验」的 UI（留 CLI 的 `--unverified`）。
- 不做 agentd 重启后的在飞环节恢复。
- 不改 `Subtree` 的实现（只订正它的注释）。
- 不合 main。
