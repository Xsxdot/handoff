# 工作台 Web 写操作 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让浏览器能独立完成一张卡的全生命周期——建卡、改卡、挂附件、按节点推进、在卡抽屉里答工单、编辑整条工作流——不再需要切回 CLI。

**Architecture:** 后端补齐卡片写 API（建卡 / 改元信息 / 验收判据 / 附件增删），前端 `api/ledger.ts` 补对应客户端；卡抽屉从「只读 + 三个写按钮」扩成可编辑面板；节点执行按钮由**卡钉住的工作流节点**动态生成，不再写死 review/merge；工作流页从只读改成可编辑并调用 `PUT /api/flows/{name}`；卡抽屉的「关联执行」行接上已有的 `fetchTaskDetail`/`replyTicket`/`TicketsPanel`——`byTask` 中间件已经把任务路由透明代理到属主机器，这一段**不需要任何新后端**。

**Tech Stack:** Go 1.22+（`internal/agentd`、`internal/ledger`）、React 18 + TypeScript + Vite、vitest + @testing-library/react、Tailwind。

## Global Constraints

- **前置依赖已就位（协调者 2026-08-21 实测）**：节点化工作流后端已合入本分支——`internal/ledger/types.go` 有 `NodeDef`、`internal/agentd/ledgerapi.go` 有 `PUT /api/flows/{name}` 与 `GET /api/disciplines`、测试 helper `ledgerPut` 已存在（Task 1 不必再补，补了会重复声明编译错）。错误响应用既有的 `ledgerErr(w, err)`（把账本哨兵错误映射成 HTTP 码）或直接 `writeJSON(w, code, map[string]string{"error": ...})`。**开工前仍自己确认一遍；对不上就停下来报告，不要自己把后端补一遍。**
- **不碰 main 分支。**
- 后端日志一律 `s.log`（agentd）/ 包内 `log()`（ledger），**禁止 `fmt.Printf`**；前端不留 `console.log`。
- 每个 task 结束：后端 `gofmt -l .` 无输出；前端 `npm run lint`、`npm run typecheck`、`npm test` 全绿。
- 前端测试 mock 沿用 `vi.mock('../../api/ledger', async (importOriginal) => ({...}))` 的既有写法，不要换风格。
- **卡的基线分支只在建卡时定，之后不可改**（`ledger.Store` 没有对应的 mutator，本 plan 也不加——改基线会让已经派出去的任务与卡的说法对不上）。界面上要显示成只读并说明这一点。


## 基线实测（2026-08-21，起点 777971b）

判据在派发前已在基线上跑过一遍，你看到的红如果超出这个范围，就是本次改动引入的：

- `go build ./...`、`go vet ./...`、`gofmt -l .` —— 干净
- `go test ./...` —— **43 个包 ok，0 FAIL**
- `cd web && npm test` —— **92 个测试文件、941 个用例全绿**

**一个已知的既有 flake，不要去追它：** `internal/agentd` 的 `TestPtyWSResumeSince`
偶发报 `TempDir RemoveAll cleanup: ... directory not empty`。这不是断言失败，是测试
进程收尾与 Go 的 TempDir 清理之间的竞态；单独 `-run TestPtyWS -count=1` 连跑三次全绿。
**它与本 plan 的改动毫无关系。** 撞上了就重跑一次；不要改它、不要把它写进你的报告
当成本次引入的问题，也不要因为它而认为「全量测试没过」。


---

### Task 1: 后端卡片写 API

**Files:**
- Modify: `internal/agentd/ledgerapi.go`
- Test: `internal/agentd/ledgerapi_test.go`

**Interfaces:**
- Consumes: `ledger.Store.CreateCard(NewCard{Title,Project,Priority,Parent,Workflow,BaseBranch,Actor}) (Card, error)`、`UpdateCardMeta(id,title,priority,actor) error`、`SetAcceptance(id,criteria,actor) error`、`AttachFile(id,kind,path,actor) error`、`DetachFile(id,path,actor) error`、既有 `s.withLedger`、`writeJSON`、`s.actorOf`（用 `grep -n "web:" internal/agentd/ledgerapi.go` 找到现有的 actor 取法，照抄，不要自己发明）
- Produces: `POST /api/cards`、`PATCH /api/cards/{id}`、`POST /api/cards/{id}/attachments`、`DELETE /api/cards/{id}/attachments`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/ledgerapi_test.go`（`ledgerPut` 由前一份 plan 加过；若不在则照 `ledgerPost` 补，另需一个 `ledgerPatch` 与 `ledgerDelete`，形状同 `ledgerPut` 只换 method）：

```go
func TestCreateCardViaAPI(t *testing.T) {
	env := newLedgerEnv(t)
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards",
		`{"title":"浏览器建的卡","project":"p","workflow":"bug","priority":"高","base_branch":"feat/x"}`)
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码: %v（原文 %s）", err, body)
	}
	if got.ID == "" {
		t.Fatalf("响应没带卡号: %s", body)
	}
	card, err := env.ledger.GetCard(got.ID)
	if err != nil {
		t.Fatalf("读回新卡: %v", err)
	}
	if card.Title != "浏览器建的卡" || card.Priority != "高" || card.BaseBranch != "feat/x" {
		t.Fatalf("字段没落全: %+v", card)
	}
}

func TestCreateCardRejectsEmptyTitle(t *testing.T) {
	env := newLedgerEnv(t)
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards", `{"title":"  ","project":"p","workflow":"bug"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("空标题应 400，实际 %d: %s", code, body)
	}
}

func TestPatchCardUpdatesMetaAndAcceptance(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "原标题")
	code, body := ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID,
		`{"title":"新标题","priority":"低","acceptance_criteria":"测试全绿且 gofmt 干净"}`)
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	got, err := env.ledger.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if got.Title != "新标题" || got.Priority != "低" {
		t.Fatalf("元信息没改: %+v", got)
	}
	if got.AcceptanceCriteria != "测试全绿且 gofmt 干净" {
		t.Fatalf("验收判据没改: %q", got.AcceptanceCriteria)
	}
}

func TestPatchCardOmittedFieldsUntouched(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "原标题")
	if err := env.ledger.SetAcceptance(card.ID, "原判据", "t"); err != nil {
		t.Fatalf("预置判据: %v", err)
	}
	// 只给 priority：标题与判据都不该被清空。缺字段 ≠ 置空是这个接口最容易写错的地方。
	code, body := ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID, `{"priority":"高"}`)
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	got, _ := env.ledger.GetCard(card.ID)
	if got.Title != "原标题" {
		t.Fatalf("没给 title 却把标题改了: %q", got.Title)
	}
	if got.AcceptanceCriteria != "原判据" {
		t.Fatalf("没给 acceptance_criteria 却把判据清了: %q", got.AcceptanceCriteria)
	}
	if got.Priority != "高" {
		t.Fatalf("priority 没生效: %q", got.Priority)
	}
}

func TestAttachAndDetachViaAPI(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "带附件的卡")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/attachments",
		`{"kind":"plan","path":"docs/superpowers/plans/x.md"}`)
	if code != http.StatusOK {
		t.Fatalf("挂附件 code = %d, body = %s", code, body)
	}
	got, _ := env.ledger.GetCard(card.ID)
	if len(got.Attachments) != 1 || got.Attachments[0].Kind != "plan" {
		t.Fatalf("附件没挂上: %+v", got.Attachments)
	}
	code, body = ledgerDelete(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/attachments",
		`{"path":"docs/superpowers/plans/x.md"}`)
	if code != http.StatusOK {
		t.Fatalf("摘附件 code = %d, body = %s", code, body)
	}
	got, _ = env.ledger.GetCard(card.ID)
	if len(got.Attachments) != 0 {
		t.Fatalf("附件没摘掉: %+v", got.Attachments)
	}
}

func TestAttachRejectsBadKind(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "带附件的卡")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/attachments",
		`{"kind":"随便什么","path":"a.md"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("非法 kind 应 400，实际 %d: %s", code, body)
	}
}
```

- [ ] **Step 2: 跑测试确认它红**

Run: `go test ./internal/agentd/ -run 'TestCreateCard|TestPatchCard|TestAttach' -v`
Expected: 404 / 编译失败。

- [ ] **Step 3: 注册路由**

在 `internal/agentd/ledgerapi.go` 的路由块里，`GET /api/cards/{id}` 附近追加：

```go
	api.HandleFunc("POST /api/cards", s.withLedger(s.handleCardCreate))
	api.HandleFunc("PATCH /api/cards/{id}", s.withLedger(s.handleCardPatch))
	api.HandleFunc("POST /api/cards/{id}/attachments", s.withLedger(s.handleCardAttach))
	api.HandleFunc("DELETE /api/cards/{id}/attachments", s.withLedger(s.handleCardDetach))
```

- [ ] **Step 4: 实现 handler（含注释）**

**注意：仓库里没有 `writeErr` 这个 helper。** `internal/agentd/ledgerapi.go` 现有的错误响应写法是
`writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})`（解析失败时 error 值固定写
`"bad json"`）。下面的代码里凡出现 `writeErr(w, code, err)`，一律照这个写法展开；
嫌重复就在本文件里抽一个带 doc 注释的小 helper：

```go
// writeErr 按本文件既有约定写错误响应：{"error": "<原因>"}。
//
// 抽出来只是省重复，语义与散落各处的 writeJSON(w, code, map[string]string{"error": ...}) 完全一致。
func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
```


在文件末尾追加：

```go
// attachmentKinds 是允许的附件类型。收窄成白名单不是洁癖：附件 kind 是
// 「进入某一列的门槛」的判据（Gate.RequireAttachment），拼错一个字母会让门
// 永远过不去，而界面上看着附件明明挂着——那种问题极难自查。
var attachmentKinds = map[string]bool{"spec": true, "plan": true, "doc": true}

// handleCardCreate 建卡。
//
// 请求体：title（必填）、project（必填）、workflow（必填）、priority、parent、
// base_branch。响应：{"id": "<新卡号>"}。
//
// 注意：**base_branch 只在这里能设**，建完不可改（改基线会让已经派出去的
// 任务与卡的说法对不上）。子卡不传 base_branch 时自动继承父卡的有效基线。
func (s *Server) handleCardCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title      string `json:"title"`
		Project    string `json:"project"`
		Workflow   string `json:"workflow"`
		Priority   string `json:"priority"`
		Parent     string `json:"parent"`
		BaseBranch string `json:"base_branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("标题不能为空"))
		return
	}
	if body.Project == "" || body.Workflow == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("project 与 workflow 都是必填"))
		return
	}
	actor := s.ledgerActor(r)
	card, err := s.ledger.CreateCard(ledger.NewCard{
		Title: strings.TrimSpace(body.Title), Project: body.Project,
		Priority: body.Priority, Parent: body.Parent,
		Workflow: body.Workflow, BaseBranch: body.BaseBranch, Actor: actor,
	})
	if err != nil {
		s.log.Warn("建卡失败", "title", body.Title, "workflow", body.Workflow, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("已建卡", "card", card.ID, "title", card.Title,
		"workflow", card.WorkflowName, "version", card.WorkflowVersion,
		"base_branch", card.BaseBranch, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]any{"id": card.ID})
}

// handleCardPatch 改卡的标题 / 优先级 / 验收判据。
//
// **缺字段 = 不动该字段，不是置空。** 三个字段都用 *string 收，靠指针区分
// 「没给」与「给了空串」——用值类型收会让「只改优先级」把标题和判据一起清掉。
func (s *Server) handleCardPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title              *string `json:"title"`
		Priority           *string `json:"priority"`
		AcceptanceCriteria *string `json:"acceptance_criteria"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	card, err := s.ledger.GetCard(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	actor := s.ledgerActor(r)
	if body.Title != nil || body.Priority != nil {
		// UpdateCardMeta 收的是最终值而不是「改动」，所以没给的那一半要用现值补齐。
		title, priority := card.Title, card.Priority
		if body.Title != nil {
			if strings.TrimSpace(*body.Title) == "" {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("标题不能改成空"))
				return
			}
			title = strings.TrimSpace(*body.Title)
		}
		if body.Priority != nil {
			priority = *body.Priority
		}
		if err := s.ledger.UpdateCardMeta(id, title, priority, actor); err != nil {
			s.log.Warn("改卡元信息失败", "card", id, "cause", err)
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.log.Info("已改卡元信息", "card", id, "title", title, "priority", priority, "actor", actor)
	}
	if body.AcceptanceCriteria != nil {
		if err := s.ledger.SetAcceptance(id, *body.AcceptanceCriteria, actor); err != nil {
			s.log.Warn("写验收判据失败", "card", id, "cause", err)
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.log.Info("已写验收判据", "card", id, "bytes", len(*body.AcceptanceCriteria), "actor", actor)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCardAttach 给卡挂一个附件（同 path 幂等）。kind 只认 spec|plan|doc。
func (s *Server) handleCardAttach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if !attachmentKinds[body.Kind] {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("附件 kind 只认 spec|plan|doc，收到 %q", body.Kind))
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("附件路径不能为空"))
		return
	}
	actor := s.ledgerActor(r)
	if err := s.ledger.AttachFile(id, body.Kind, strings.TrimSpace(body.Path), actor); err != nil {
		s.log.Warn("挂附件失败", "card", id, "kind", body.Kind, "path", body.Path, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("已挂附件", "card", id, "kind", body.Kind, "path", body.Path, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCardDetach 摘掉卡上指定路径的附件（不存在也返回 ok，摘除天然幂等）。
func (s *Server) handleCardDetach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if body.Path == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("附件路径不能为空"))
		return
	}
	actor := s.ledgerActor(r)
	if err := s.ledger.DetachFile(id, body.Path, actor); err != nil {
		s.log.Warn("摘附件失败", "card", id, "path", body.Path, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("已摘附件", "card", id, "path", body.Path, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
```

**`s.ledgerActor(r)` 不一定存在**——用 `grep -n '"web:' internal/agentd/ledgerapi.go` 找到既有 handler 是怎么算 actor 的（形如 `"web:"+r.RemoteAddr`），照抄同一种写法；如果既有写法是内联的，就抽一个 `ledgerActor` 小函数出来给所有 handler 用，并给它写 doc 注释。

- [ ] **Step 5: 跑测试确认绿**

Run: `go test ./internal/agentd/ -v -count=1`

- [ ] **Step 6: 变异测试证明「缺字段 ≠ 置空」这道网有牙齿**

把 `handleCardPatch` 里的 `Title *string` 临时改成 `Title string`（并相应去掉 `!= nil` 判断，改成 `if body.Title != ""`），跑
`go test ./internal/agentd/ -run TestPatchCardOmittedFieldsUntouched`，确认**变红**；改回确认变绿。

- [ ] **Step 7: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l .`

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/ledgerapi.go internal/agentd/ledgerapi_test.go
git commit -m "feat(agentd): 卡片写 API——建卡/改元信息/验收判据/附件增删"
```

---

### Task 2: Web API 客户端与类型

**Files:**
- Modify: `web/src/api/ledger.ts`
- Test: `web/src/api/ledger.test.ts`（若不存在则新建）

**Interfaces:**
- Consumes: 既有 `request`/`postJSON`（`web/src/api/client.ts`）；Task 1 的四个新路由；节点化后端的 `GET /api/flows/{name}`、`PUT /api/flows/{name}`、`GET /api/disciplines`
- Produces: `createCard`、`patchCard`、`attachFile`、`detachFile`、`fetchFlow`、`putFlow`、`fetchDisciplineNames`；类型 `NodeDef`、`NodeOverride`、`FlowDetail`；`runCardStep` 的 step 参数放宽为 `string`

- [ ] **Step 1: 写失败的测试**

新建 `web/src/api/ledger.test.ts`：

```ts
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { attachFile, createCard, detachFile, patchCard, putFlow, runCardStep } from './ledger'

// 直接打桩 fetch：这一层要验的是「方法、路径、请求体」，不是渲染。
const calls: Array<{ url: string; init: RequestInit }> = []

beforeEach(() => {
  calls.length = 0
  vi.stubGlobal('fetch', vi.fn((url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    return Promise.resolve(new Response(JSON.stringify({ ok: true, id: 'B99' }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))
  }))
})
afterEach(() => { vi.unstubAllGlobals() })

const bodyOf = (index: number) => JSON.parse(String(calls[index].init.body))

describe('账本写操作的线格式', () => {
  it('建卡 POST /api/cards，字段名与 Go 侧一字不差', async () => {
    await createCard({ title: 'T', project: 'p', workflow: 'bug', priority: '高', base_branch: 'feat/x' })
    expect(calls[0].url).toContain('/api/cards')
    expect(calls[0].init.method).toBe('POST')
    expect(bodyOf(0)).toEqual({ title: 'T', project: 'p', workflow: 'bug', priority: '高', base_branch: 'feat/x' })
  })

  it('改卡用 PATCH，且只发调用方给的字段——缺字段在后端表示「不动」', async () => {
    await patchCard('B1', { priority: '低' })
    expect(calls[0].init.method).toBe('PATCH')
    expect(bodyOf(0)).toEqual({ priority: '低' })
    expect(Object.keys(bodyOf(0))).not.toContain('title')
  })

  it('挂附件 POST、摘附件 DELETE，路径都带卡号', async () => {
    await attachFile('B1', 'plan', 'docs/p.md')
    await detachFile('B1', 'docs/p.md')
    expect(calls[0].url).toContain('/api/cards/B1/attachments')
    expect(calls[0].init.method).toBe('POST')
    expect(calls[1].init.method).toBe('DELETE')
    expect(bodyOf(1)).toEqual({ path: 'docs/p.md' })
  })

  it('发工作流新版本用 PUT /api/flows/{name}', async () => {
    await putFlow('feature', [{ name: '待办', next: '进行中' }])
    expect(calls[0].url).toContain('/api/flows/feature')
    expect(calls[0].init.method).toBe('PUT')
    expect(bodyOf(0).nodes).toHaveLength(1)
  })

  it('卡号里的特殊字符要被编码，不能直接拼进 URL', async () => {
    await patchCard('B1/../admin', { priority: '低' })
    expect(calls[0].url).not.toContain('/../')
  })

  it('节点执行接受任意节点名，不再只认 review|merge', async () => {
    await runCardStep('B1', '收尾合并')
    expect(bodyOf(0)).toEqual({ step: '收尾合并' })
  })
})
```

- [ ] **Step 2: 跑测试确认它红**

Run: `cd web && npx vitest run src/api/ledger.test.ts`
Expected: FAIL，`createCard` 等未导出。

- [ ] **Step 3: 实现客户端（含注释）**

`web/src/api/ledger.ts`：先在 `postJSON, request` 的 import 里补上删改要用的底座。**先看 `client.ts` 里有没有 `patchJSON`/`deleteJSON`**（`grep -n "export function\|export const" src/api/client.ts`）；没有就在 `client.ts` 里照 `postJSON` 补两个，并写 doc 注释说明它们与 `postJSON` 只差 method。

然后追加：

```ts
// NodeOverride 节点对模板的单字段覆盖；省略的字段 = 沿用模板。
// 字段名与 Go 侧 ledger.NodeOverride 一字不差。
export interface NodeOverride {
  executor?: string
  discipline?: string
  target?: string
  model?: string
}

// NodeDef 工作流的一个节点：看板的一列 + 卡走到这列时的执行规矩。
// 字段名与 Go 侧 ledger.NodeDef 一字不差。
export interface NodeDef {
  name: string
  template?: string
  override?: NodeOverride
  dispatch?: boolean
  verdict?: boolean
  carry_card_context?: boolean
  max_rounds?: number
  next?: string
  on_fail?: string
  gate?: { require_attachment?: string; require_acceptance?: boolean }
  human_bases?: string[]
}

export interface FlowDetail {
  name: string
  version: number
  nodes: NodeDef[]
  states: string[]
}

export interface NewCardReq {
  title: string
  project: string
  workflow: string
  priority?: string
  parent?: string
  base_branch?: string
}

// createCard 建卡。base_branch 只在建卡时能给，之后不可改。
export const createCard = (req: NewCardReq) =>
  postJSON<{ id: string }>('/api/cards', req)

// CardPatch 的三个字段**全部可选，缺席即「不动该字段」**（不是置空）。
// 调用方只放要改的键，别为了「补全」而把现值原样塞回去——那会在没改动的
// 字段上也落一条事件。
export interface CardPatch {
  title?: string
  priority?: string
  acceptance_criteria?: string
}

export const patchCard = (id: string, patch: CardPatch) =>
  patchJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}`, patch)

export const attachFile = (id: string, kind: string, path: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/attachments`, { kind, path })

export const detachFile = (id: string, path: string) =>
  deleteJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/attachments`, { path })

export const fetchFlow = (name: string) =>
  request<FlowDetail>(`/api/flows/${encodeURIComponent(name)}`)

// putFlow 发布该工作流的**下一个版本**。工作流不可变版本化——保存不是「改」，
// 已钉在老版本上的卡完全不受影响。
export const putFlow = (name: string, nodes: NodeDef[]) =>
  putJSON<{ name: string; version: number }>(`/api/flows/${encodeURIComponent(name)}`, { nodes })

export const fetchDisciplineNames = () =>
  request<{ names: string[] }>('/api/disciplines').then((response) => response.names ?? [])
```

把既有的 `runCardStep` 签名改为：

```ts
// runCardStep 发起一个卡环节。step 是**节点名**（= 看板列名），由卡钉住的
// 工作流决定，不再是写死的 review|merge。后端受理即返回——环节要跑几分钟到
// 几十分钟，这个 Promise resolve 只代表「收到了」，进展看卡的事件流。
export const runCardStep = (id: string, step: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/step`, { step })
```

`putJSON` 若 `client.ts` 里没有，一并补上（与 `patchJSON`/`deleteJSON` 同批）。

- [ ] **Step 4: 跑测试确认绿**

Run: `cd web && npx vitest run src/api/ledger.test.ts`

- [ ] **Step 5: 全量前端回归**

Run: `cd web && npm run lint && npm run typecheck && npm test`
Expected: 全绿。`runCardStep` 放宽签名不会破坏既有调用（`'review'`/`'merge'` 仍是合法 string）。

- [ ] **Step 6: Commit**

```bash
git add web/src/api
git commit -m "feat(web): 账本写操作 API 客户端与节点类型"
```

---

### Task 3: 建卡入口

**Files:**
- Create: `web/src/app/cards/NewCardDialog.tsx`
- Create: `web/src/app/cards/NewCardDialog.test.tsx`
- Modify: `web/src/app/cards/CardsPage.tsx`

**Interfaces:**
- Consumes: Task 2 的 `createCard`、`NewCardReq`；既有 `fetchFlows`（取工作流名列表）、`errorMessage`
- Produces: `<NewCardDialog open project workflows onClose onCreated />`

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/cards/NewCardDialog.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { NewCardDialog } from './NewCardDialog'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  createCard: vi.fn().mockResolvedValue({ id: 'B77' }),
}))

const props = { open: true, project: 'handoff', workflows: ['feature', 'bug'], onClose: () => {} }

describe('建卡对话框', () => {
  it('填标题选工作流即可建卡，建成后把新卡号交给调用方', async () => {
    const ledger = await import('../../api/ledger')
    const onCreated = vi.fn()
    render(<NewCardDialog {...props} onCreated={onCreated} />)
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '新工作项' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    await waitFor(() => expect(vi.mocked(ledger.createCard)).toHaveBeenCalledWith(
      expect.objectContaining({ title: '新工作项', project: 'handoff', workflow: 'feature' }),
    ))
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('B77'))
  })

  it('标题为空时建卡按钮不可用——别把明知会 400 的请求发出去', () => {
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    expect(screen.getByRole('button', { name: '建卡' })).toBeDisabled()
  })

  it('基线分支标明「建卡后不可改」', () => {
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    expect(screen.getByText(/建卡后不可改/)).toBeInTheDocument()
  })

  it('后端报错时把原文显示出来，不是静默失败', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.createCard).mockRejectedValueOnce(new Error('project 与 workflow 都是必填'))
    render(<NewCardDialog {...props} onCreated={() => {}} />)
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: '建卡' }))
    expect(await screen.findByText(/project 与 workflow 都是必填/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它红**

Run: `cd web && npx vitest run src/app/cards/NewCardDialog.test.tsx`

- [ ] **Step 3: 实现对话框（含文件头注释）**

新建 `web/src/app/cards/NewCardDialog.tsx`：

```tsx
// NewCardDialog —— 建卡对话框。
//
// 职责：收集建一张卡所需的最小字段（标题/工作流/优先级/父卡/基线分支），
// 调 createCard，把新卡号交回调用方（通常用来立刻打开抽屉）。
//
// 边界：
//   - 不管建完之后干什么——打开抽屉、刷新列表都由调用方决定
//   - 不做项目选择：项目由调用方从当前视图上下文传进来
//   - **基线分支只在这里能填**：建卡后不可改（改基线会让已派出去的任务与卡
//     的说法对不上），所以表单上要写清这一点，而不是让人建完才发现改不了
import { useState } from 'react'
import { createCard } from '../../api/ledger'
import { errorMessage } from '../lib/format'

export function NewCardDialog({
  open, project, workflows, parent, onClose, onCreated,
}: {
  open: boolean
  project: string
  workflows: string[]
  parent?: string
  onClose: () => void
  onCreated: (id: string) => void
}) {
  const [title, setTitle] = useState('')
  const [workflow, setWorkflow] = useState(workflows[0] ?? 'feature')
  const [priority, setPriority] = useState('中')
  const [baseBranch, setBaseBranch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!open) return null

  const submit = async () => {
    const trimmed = title.trim()
    if (!trimmed) return
    setBusy(true)
    setError('')
    try {
      const result = await createCard({
        title: trimmed, project, workflow, priority,
        ...(parent ? { parent } : {}),
        ...(baseBranch.trim() ? { base_branch: baseBranch.trim() } : {}),
      })
      setTitle('')
      setBaseBranch('')
      onCreated(result.id)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4">
      <div className="w-full max-w-md rounded-lg border bg-background p-4 shadow-lg">
        <h2 className="text-base font-semibold">新建工作项</h2>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-title">标题</label>
        <input
          id="new-card-title" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          value={title} onChange={(e) => setTitle(e.target.value)} autoFocus
        />
        <div className="mt-3 grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-workflow">工作流</label>
            <select
              id="new-card-workflow" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={workflow} onChange={(e) => setWorkflow(e.target.value)}
            >
              {workflows.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-priority">优先级</label>
            <select
              id="new-card-priority" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={priority} onChange={(e) => setPriority(e.target.value)}
            >
              {['高', '中', '低'].map((level) => <option key={level} value={level}>{level}</option>)}
            </select>
          </div>
        </div>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-base">基线分支</label>
        <input
          id="new-card-base" className="mt-1 w-full rounded border px-2 py-1.5 font-mono text-sm"
          placeholder={parent ? '留空 = 继承父卡' : '留空 = 项目主线'}
          value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)}
        />
        <p className="mt-1 text-xs text-muted-foreground">
          这张卡的合并目标。<b>建卡后不可改</b>——已派出去的任务会按它工作。
        </p>
        {error !== '' && <p className="mt-3 rounded border border-amber-500/40 bg-amber-500/10 p-2 text-xs">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded border px-3 py-1.5 text-sm" onClick={onClose}>取消</button>
          <button
            type="button"
            className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
            disabled={busy || title.trim() === ''}
            onClick={() => void submit()}
          >建卡</button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 接进 CardsPage**

在 `CardsPage` 里：加 `const [newCardOpen, setNewCardOpen] = useState(false)`；工具栏加一个「+ 新建」按钮打开它；`onCreated` 里关闭对话框、刷新列表（复用页面已有的刷新方式，`grep -n "usePoll\|refresh" web/src/app/cards/CardsPage.tsx` 找现成的）、并把新卡号设成当前打开的抽屉 id。`workflows` 从页面已经在拉的 `fetchFlows()` 结果里取 `workflows.map(w => w.name)`；`project` 用页面当前的项目筛选值，为空时传第一个卡的项目或 `'handoff'`——**用 `grep -n "project" web/src/app/cards/CardsPage.tsx` 看页面现在怎么表达「当前项目」，跟着来，别新造一个概念。**

- [ ] **Step 5: 跑测试确认绿 + 全量前端回归**

Run: `cd web && npm run lint && npm run typecheck && npm test`

- [ ] **Step 6: Commit**

```bash
git add web/src/app/cards
git commit -m "feat(web): 建卡对话框与看板新建入口"
```

---

### Task 4: 抽屉里的编辑区

**Files:**
- Modify: `web/src/app/cards/CardDrawer.tsx`
- Test: `web/src/app/cards/CardDrawer.test.tsx`

**Interfaces:**
- Consumes: Task 2 的 `patchCard`、`attachFile`、`detachFile`
- Produces: 抽屉内的可编辑标题、优先级下拉、验收判据编辑、附件增删

- [ ] **Step 1: 写失败的测试**

追加到 `web/src/app/cards/CardDrawer.test.tsx`（把 `patchCard`/`attachFile`/`detachFile` 加进文件顶部那个 `vi.mock` 的返回对象里）：

```tsx
describe('抽屉里的编辑', () => {
  const detail = {
    card: { ID: 'B20', Title: '原标题', Status: '进行中', Priority: '中', Attachments: [], AcceptanceCriteria: '' },
    relations: [], events: [], task_states: [], effective_base_branch: 'feat/x', decisions: [],
  }

  it('改标题走 patchCard，只发 title 一个字段', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '改标题' }))
    fireEvent.change(screen.getByDisplayValue('原标题'), { target: { value: '改过的标题' } })
    fireEvent.click(screen.getByRole('button', { name: '保存标题' }))
    await waitFor(() => expect(vi.mocked(ledger.patchCard)).toHaveBeenCalledWith('B20', { title: '改过的标题' }))
  })

  it('写验收判据走 patchCard，只发 acceptance_criteria', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑判据' }))
    fireEvent.change(screen.getByPlaceholderText('这张卡怎样算做完了…'), { target: { value: '全量测试绿' } })
    fireEvent.click(screen.getByRole('button', { name: '保存判据' }))
    await waitFor(() => expect(vi.mocked(ledger.patchCard)).toHaveBeenCalledWith('B20', { acceptance_criteria: '全量测试绿' }))
  })

  it('挂附件要同时给 kind 与 path', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.change(await screen.findByPlaceholderText('docs/superpowers/plans/…'), {
      target: { value: 'docs/superpowers/plans/x.md' },
    })
    fireEvent.click(screen.getByRole('button', { name: '挂上' }))
    await waitFor(() => expect(vi.mocked(ledger.attachFile)).toHaveBeenCalledWith('B20', 'plan', 'docs/superpowers/plans/x.md'))
  })

  it('已挂的附件能摘掉', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      ...detail,
      card: { ...detail.card, Attachments: [{ Kind: 'plan', Path: 'docs/p.md' }] },
    } as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '摘掉 docs/p.md' }))
    await waitFor(() => expect(vi.mocked(ledger.detachFile)).toHaveBeenCalledWith('B20', 'docs/p.md'))
  })

  it('基线分支只读且注明不可改', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B20" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText('feat/x')).toBeInTheDocument()
    expect(screen.getByText(/建卡时定，不可改/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它红**

Run: `cd web && npx vitest run src/app/cards/CardDrawer.test.tsx`

- [ ] **Step 3: 实现编辑区（含注释）**

在 `CardDrawer` 里加状态与提交函数。要点：

- 三块编辑（标题 / 判据 / 附件）各自独立的 `editing` / `busy` / `error` 状态，互不影响——一处保存失败不该把另外两处的草稿清掉。
- **每次 `patchCard` 只发被改的那个键**：`patchCard(id, { title })`，不要把现值一起塞回去。后端「缺字段 = 不动」，多发一个字段就多落一条事件。
- 保存成功后调已有的 `load()` 重新拉详情，不要手工拼本地状态——账本是权威。
- 附件的 kind 用一个下拉（spec / plan / doc），默认 `plan`；摘除按钮的可访问名要是「摘掉 <路径>」，这样多个附件同时在场时测试与读屏都能区分。
- 基线分支一行渲染 `detail.effective_base_branch`，旁边小字「建卡时定，不可改」。

给这段加一条为什么的注释：

```tsx
        {/* 基线分支只读：卡建出来之后改基线，会让已经派出去、正按老基线工作的
            任务与卡的说法对不上——那种不一致在事后极难分辨是谁错了。要换基线
            就新建一张卡。 */}
```

- [ ] **Step 4: 跑测试确认绿 + 全量前端回归**

Run: `cd web && npm run lint && npm run typecheck && npm test`

- [ ] **Step 5: 变异测试**

把「只发被改的键」改成 `patchCard(id, { title, priority, acceptance_criteria })`（把现值一起塞回去），跑
`npx vitest run src/app/cards/CardDrawer.test.tsx`，确认改标题那条**变红**；改回确认变绿。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/cards
git commit -m "feat(web): 卡抽屉支持改标题/优先级/验收判据/附件增删"
```

---

### Task 5: 抽屉里的工单入口

**Files:**
- Modify: `web/src/app/cards/CardDrawer.tsx`
- Test: `web/src/app/cards/CardDrawer.test.tsx`

**Interfaces:**
- Consumes: 既有 `fetchTaskDetail(id)`、`replyTicket(id, req)`（`web/src/api/client.ts`）、`<TicketsPanel tickets disabled onReply bare />`（`web/src/app/task/TicketsPanel.tsx`）、`buildTicketAnswer`（`web/src/app/task/review.ts`）
- Produces: 「关联执行（task）」每行可展开，展开后显示该 task 的挂起工单并可作答

**为什么不需要后端改动：** `byTask` 中间件会把 `/api/tasks/{id}/...` 透明代理到该 task 的属主机器，所以远程 task 的工单在本机控制台上本来就读得到、答得了。这一条是**纯前端复用**。

- [ ] **Step 1: 写失败的测试**

追加到 `CardDrawer.test.tsx`：

```tsx
describe('抽屉里的工单入口', () => {
  const withTask = {
    card: { ID: 'B30', Title: '在跑的卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
    relations: [], events: [], effective_base_branch: '', decisions: [],
    task_states: [{ Target: 'linux-01', TaskID: 'task-abc', Purpose: 'implement', LastType: 'question', LastSeq: 9 }],
  }

  it('展开关联执行行能看到该 task 的挂起工单', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(withTask as never)
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-abc', state: 'waiting_answer' },
      tickets: [{ id: 'tk-1', kind: 'ask', request: '这里要用哪个基线？' }],
      events: [],
    } as never)
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /task-abc/ }))
    expect(await screen.findByText('这里要用哪个基线？')).toBeInTheDocument()
  })

  it('在抽屉里作答走 replyTicket，不用跳去任务页', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(withTask as never)
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-abc', state: 'waiting_answer' },
      tickets: [{ id: 'tk-1', kind: 'ask', request: '这里要用哪个基线？' }],
      events: [],
    } as never)
    vi.mocked(client.replyTicket).mockResolvedValue({ ok: true } as never)
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /task-abc/ }))
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'feat/x' } })
    fireEvent.click(screen.getByRole('button', { name: /提交|回答|发送/ }))
    await waitFor(() => expect(vi.mocked(client.replyTicket)).toHaveBeenCalledWith(
      'task-abc', expect.objectContaining({ ticket_id: 'tk-1' }),
    ))
  })

  it('没有挂起工单时说清楚，不留一片空白', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(withTask as never)
    vi.mocked(client.fetchTaskDetail).mockResolvedValue({
      task: { id: 'task-abc', state: 'running' }, tickets: [], events: [],
    } as never)
    render(<CardDrawer id="B30" onClose={() => {}} onOpenCard={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /task-abc/ }))
    expect(await screen.findByText(/没有等待处理的工单/)).toBeInTheDocument()
  })
})
```

文件顶部补一个 `vi.mock('../../api/client', ...)`，把 `fetchTaskDetail` 与 `replyTicket` 打成 `vi.fn()`。**`replyTicket` 的第二个参数已核实**（`web/src/api/types.ts:391`）：`ReplyRequest { ticket_id: string; answer: string }`——是 `ticket_id` 不是 `ticket`，上面的断言已按实测字段名写好。`buildTicketAnswer` 在 `web/src/app/task/review.ts:46`，`errorMessage` 在 `web/src/app/lib/format.ts:66`，`request`/`postJSON` 在 `web/src/api/client.ts:123/149`。

- [ ] **Step 2: 跑测试确认它红**

Run: `cd web && npx vitest run src/app/cards/CardDrawer.test.tsx -t 工单入口`

- [ ] **Step 3: 实现（含注释）**

把「关联执行（task）」那一行从 `<div>` 改成 `<button>`（可访问名里带上 TaskID），点击切换展开态；展开时 `fetchTaskDetail(task.TaskID)`，把 `tickets` 交给 `<TicketsPanel bare tickets={...} disabled={false} onReply={...} />`；`onReply` 里用 `buildTicketAnswer` 编码后调 `replyTicket(task.TaskID, {...})`，成功后重新 `fetchTaskDetail` 刷新该行。

加一条为什么的注释：

```tsx
              {/* 远程 task 的工单在这里也答得了：agentd 的 byTask 中间件会把
                  /api/tasks/{id}/* 透明代理到该 task 的属主机器。所以这一段
                  是纯前端复用，不需要任何新后端。 */}
```

- [ ] **Step 4: 跑测试确认绿 + 全量前端回归**

Run: `cd web && npm run lint && npm run typecheck && npm test`

- [ ] **Step 5: Commit**

```bash
git add web/src/app/cards
git commit -m "feat(web): 卡抽屉里直接展开并答关联 task 的工单"
```

---

### Task 6: 节点执行按钮通用化

**Files:**
- Modify: `web/src/app/cards/CardDrawer.tsx`
- Modify: `web/src/app/cards/CardsPage.tsx`（把节点定义传进抽屉）
- Test: `web/src/app/cards/CardDrawer.test.tsx`

**Interfaces:**
- Consumes: Task 2 的 `NodeDef`、`fetchFlow`、放宽后的 `runCardStep(id, step: string)`
- Produces: `CardDrawer` 新增可选 prop `nodes?: NodeDef[]`；执行按钮按节点动态生成

- [ ] **Step 1: 写失败的测试**

追加到 `CardDrawer.test.tsx`：

```tsx
describe('抽屉里的节点执行按钮', () => {
  const nodes = [
    { name: '待办' },
    { name: '进行中', dispatch: true, template: 'feature-impl' },
    { name: '待审阅', dispatch: true, verdict: true, template: 'review-generic' },
    { name: '待合并', dispatch: true, verdict: true, template: 'review-generic', human_bases: ['main'] },
    { name: '已完成' },
  ]
  const detail = {
    card: { ID: 'B40', Title: '节点卡', Status: '待审阅', Attachments: [], AcceptanceCriteria: '' },
    relations: [], events: [], task_states: [], effective_base_branch: 'feat/x', decisions: [],
  }

  it('只给有 dispatch 能力的节点画按钮，纯人工列不画', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B40" onClose={() => {}} onOpenCard={() => {}} nodes={nodes as never} />)
    expect(await screen.findByRole('button', { name: '跑「待审阅」' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '跑「待合并」' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '跑「待办」' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '跑「已完成」' })).not.toBeInTheDocument()
  })

  it('点按钮把节点名原样发给后端', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B40" onClose={() => {}} onOpenCard={() => {}} nodes={nodes as never} />)
    fireEvent.click(await screen.findByRole('button', { name: '跑「待审阅」' }))
    await waitFor(() => expect(vi.mocked(ledger.runCardStep)).toHaveBeenCalledWith('B40', '待审阅'))
  })

  it('卡的基线在节点的人工清单里时，按钮要提前说明它不会自动跑', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      ...detail, effective_base_branch: 'main',
    } as never)
    render(<CardDrawer id="B40" onClose={() => {}} onOpenCard={() => {}} nodes={nodes as never} />)
    expect(await screen.findByTitle(/基线 main 在本节点的人工清单里/)).toBeInTheDocument()
  })

  it('没拿到节点定义时退回不画按钮，而不是画一堆写死的', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail as never)
    render(<CardDrawer id="B40" onClose={() => {}} onOpenCard={() => {}} />)
    await screen.findByText('节点卡')
    expect(screen.queryByRole('button', { name: /^跑「/ })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它红**

Run: `cd web && npx vitest run src/app/cards/CardDrawer.test.tsx -t 节点执行按钮`

- [ ] **Step 3: 实现（含注释）**

- `CardDrawer` 增可选 prop `nodes?: NodeDef[]`。
- 删掉写死的 review / merge 按钮与 `stepBusy: 'review' | 'merge' | null` 这类联合类型，改成 `stepBusy: string | null`。
- 按 `nodes.filter(n => n.dispatch)` 渲染按钮，名字 `跑「${n.name}」`，点击 `runCardStep(id, n.name)`。
- 基线命中 `n.human_bases` 时，按钮加 `title={`基线 ${base} 在本节点的人工清单里：点了也不会自动跑，会直接转「需要你」`}` 并降低视觉权重（不禁用——人可能就是想让它去打那面旗）。
- `CardsPage` 里用 `fetchFlow(workflowName)` 拿节点传下来。**注意：卡钉的是自己的工作流版本，而 `fetchFlow` 返回的是最新版**，两者可能不同。加注释说明这个已知缺口：

```tsx
  // 已知缺口：fetchFlow 拿的是工作流最新版，而卡钉的是建卡时那版。两者不同时，
  // 这里画出来的按钮可能多一个或少一个——真正的合法性由后端按卡钉的版本判，
  // 点了不存在的节点会拿到「节点 %q 不在卡 %s 的工作流 … 里」。先接受这个偏差：
  // 修它要给 /api/cards/{id} 的响应带上卡自己那版的节点，属于另一个改动。
```

- [ ] **Step 4: 跑测试确认绿 + 全量前端回归**

Run: `cd web && npm run lint && npm run typecheck && npm test`

- [ ] **Step 5: Commit**

```bash
git add web/src/app/cards
git commit -m "feat(web): 抽屉执行按钮按工作流节点动态生成，不再写死 review/merge"
```

---

### Task 7: 工作流编辑页

**Files:**
- Modify: `web/src/app/flows/FlowsPage.tsx`
- Create: `web/src/app/flows/NodeEditor.tsx`
- Create: `web/src/app/flows/NodeEditor.test.tsx`
- Create: `web/src/app/flows/FlowsPage.test.tsx`

**Interfaces:**
- Consumes: Task 2 的 `NodeDef`、`fetchFlow`、`putFlow`、`fetchDisciplineNames`；既有 `fetchFlows`（拿模板名列表）
- Produces: `<NodeEditor node templates disciplines onChange onRemove />`；`FlowsPage` 的工作流 tab 可编辑并保存

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/flows/NodeEditor.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { NodeEditor } from './NodeEditor'

const base = { name: '待审阅', dispatch: true, verdict: true, template: 'review-generic' }
const props = {
  templates: ['feature-impl', 'review-generic'],
  disciplines: ['implement', 'review', 'finishing'],
  nodeNames: ['待办', '进行中', '待审阅', '已完成'],
}

describe('节点编辑器', () => {
  it('能改执行者与纪律块覆盖，改动原样冒泡给上层', () => {
    const onChange = vi.fn()
    render(<NodeEditor node={base} {...props} onChange={onChange} onRemove={() => {}} />)
    fireEvent.change(screen.getByLabelText('纪律块'), { target: { value: 'finishing' } })
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      override: expect.objectContaining({ discipline: 'finishing' }),
    }))
  })

  it('关掉派发时，裁决/模板/轮次一并失效——它们没有派发就没有意义', () => {
    const onChange = vi.fn()
    render(<NodeEditor node={base} {...props} onChange={onChange} onRemove={() => {}} />)
    fireEvent.click(screen.getByLabelText('派发'))
    const next = onChange.mock.calls[0][0]
    expect(next.dispatch).toBe(false)
    expect(next.verdict).toBeFalsy()
    expect(next.max_rounds).toBeFalsy()
  })

  it('路由下拉的候选是别的节点名，不含自己', () => {
    render(<NodeEditor node={base} {...props} onChange={() => {}} onRemove={() => {}} />)
    const options = [...screen.getByLabelText('通过后去').querySelectorAll('option')].map((o) => o.textContent)
    expect(options).toContain('已完成')
    expect(options).not.toContain('待审阅')
  })

  it('纯人工列不显示模板与纪律块——避免让人以为配了会生效', () => {
    render(<NodeEditor node={{ name: '待办' }} {...props} onChange={() => {}} onRemove={() => {}} />)
    expect(screen.queryByLabelText('模板')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('纪律块')).not.toBeInTheDocument()
  })
})
```

新建 `web/src/app/flows/FlowsPage.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { FlowsPage } from './FlowsPage'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchFlows: vi.fn().mockResolvedValue({
    workflows: [{ name: 'feature', version: 3, def: { states: ['待办', '已完成'] } }],
    templates: [{ name: 'review-generic', version: 1, def: {} }],
  }),
  fetchFlow: vi.fn().mockResolvedValue({
    name: 'feature', version: 3, states: ['待办', '已完成'],
    nodes: [{ name: '待办', next: '已完成' }, { name: '已完成' }],
  }),
  fetchDisciplineNames: vi.fn().mockResolvedValue(['implement', 'review', 'finishing']),
  putFlow: vi.fn().mockResolvedValue({ name: 'feature', version: 4 }),
}))

describe('工作流页可编辑', () => {
  it('保存调 putFlow 并把新版本号显示出来', async () => {
    const ledger = await import('../../api/ledger')
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(await screen.findByRole('button', { name: '保存为新版本' }))
    await waitFor(() => expect(vi.mocked(ledger.putFlow)).toHaveBeenCalledWith('feature', expect.any(Array)))
    expect(await screen.findByText(/v4/)).toBeInTheDocument()
  })

  it('保存前明说这是「发新版本」，老卡不受影响', async () => {
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    expect(await screen.findByText(/发布一个新版本.*已有的卡仍走各自钉住的版本/)).toBeInTheDocument()
  })

  it('后端拒绝时把真因显示出来，不是「保存失败」四个字', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.putFlow).mockRejectedValueOnce(new Error('节点 "A" 的 Next 指向不存在的节点 "B"'))
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(await screen.findByRole('button', { name: '保存为新版本' }))
    expect(await screen.findByText(/Next 指向不存在的节点/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它红**

Run: `cd web && npx vitest run src/app/flows/`

- [ ] **Step 3: 实现 NodeEditor（含文件头注释）**

新建 `web/src/app/flows/NodeEditor.tsx`：文件头注释写清职责与边界——

```tsx
// NodeEditor —— 单个工作流节点的编辑器。
//
// 职责：把一个 NodeDef 展开成一组控件（模板引用、单字段覆盖、能力开关、路由、
// 门槛、人工基线清单），改动整体冒泡给上层，本组件不持有节点数组。
//
// 边界：
//   - 不保存——保存是整条工作流一次性发新版本，那在 FlowsPage
//   - 不做跨节点校验（路由悬空、模板不存在）——那是后端 PutWorkflow 的职责，
//     前端重复实现一遍只会漂移；这里只把候选项限制成合法集合，降低出错概率
//   - **不预设节点类型**：界面上不出现「审阅节点」「合并节点」这样的词，
//     语义由用户开出来的开关组合决定
```

关键实现点：
- `dispatch` 关掉时，一次性把 `verdict`/`template`/`override`/`max_rounds`/`on_fail` 清掉再冒泡——留着它们既过不了后端校验，也会让人以为配了会生效。给这一段写「为什么」注释。
- `verdict` 为假时不显示 `max_rounds` 与 `on_fail`。
- `next`/`on_fail` 是下拉，候选 = `nodeNames` 去掉自己，外加一个「（停在本列）」的空值项。
- `human_bases` 用逗号分隔的输入框，旁边小字说明「卡的有效基线落在其中时本节点不自动跑，直接转『需要你』」。
- 每个控件都要有 `<label htmlFor>`，测试与读屏都靠它。

- [ ] **Step 4: 改造 FlowsPage（含注释）**

- 工作流 tab 的每张 `WorkflowCard` 加「编辑」按钮；进入编辑态后用 `fetchFlow(name)` 拉带 `nodes` 的完整定义（`fetchFlows` 的列表响应只有 `def.states`）。
- 编辑态里渲染节点列表 + 每个 `NodeEditor` + 「加一列」「上移/下移/删除」。
- 顶部固定一段说明：「保存会**发布一个新版本**，已有的卡仍走各自钉住的版本；要让老卡用新流程，用 `handoff workflow migrate` 显式迁。」
- 「保存为新版本」调 `putFlow(name, nodes)`，成功后显示新版本号并退出编辑态；失败把后端错误原文显示出来。
- 把 `WorkflowCard` / `TemplateCard` 里那句「只读 · 编辑请使用 CLI」——**工作流那张改掉**（现在能编辑了），**模板那张保留**（模板 CRUD 不在本 plan 范围）。

- [ ] **Step 5: 跑测试确认绿 + 全量前端回归**

Run: `cd web && npm run lint && npm run typecheck && npm test`

- [ ] **Step 6: 变异测试**

把 NodeEditor 里「关掉 dispatch 时连带清空 verdict/max_rounds」那段删掉，跑
`npx vitest run src/app/flows/NodeEditor.test.tsx`，确认对应用例**变红**；改回确认变绿。

- [ ] **Step 7: 后端构建产物同步**

`grep -rn "webui/dist\|embed" internal/webui/*.go | head` 看 Web 产物是怎么嵌进二进制的。若是 `go:embed dist`，跑一次 `cd web && npm run build` 确认能出产物，**但不要把 `dist/` 提交进 git**（先 `git status` 确认它在 `.gitignore` 里；不在就说明构建产物本来就入库，那按仓库现状办）。

- [ ] **Step 8: Commit**

```bash
git add web/src/app/flows
git commit -m "feat(web): 工作流页可编辑——节点配置、路由、能力开关、保存发新版本"
```

---

## 收尾自检（全部 task 完成后）

- [ ] `go build ./... && go vet ./... && go test ./... -count=1` 全绿；`gofmt -l .` 无输出
- [ ] `cd web && npm run lint && npm run typecheck && npm test` 全绿
- [ ] `grep -rn "编辑请使用 CLI" web/src/app/flows/` 只在模板那张卡上还有
- [ ] `grep -rn "'review' | 'merge'" web/src/ | grep -v "\.test\."` 无残留的写死联合类型

> 同上：`.test.tsx` 里为了断言「不再写死 review/merge」而出现这两个词是**正当的**，
> 不要为了让 grep 变干净就把字符串拆开拼。命中就在报告里说明为何正当。
- [ ] `grep -rn "console.log" web/src/` 没有新增
- [ ] 新建的每个 tsx 文件都有职责+边界的文件头注释；后端新导出函数都有 doc 注释
- [ ] 后端每个错误分支都带上下文并落 `s.log.Warn/Error`；成功路径落 `s.log.Info`
- [ ] 最后一条消息里如实报告：哪些 task 完成、跑了哪些命令、有没有未验证的部分
