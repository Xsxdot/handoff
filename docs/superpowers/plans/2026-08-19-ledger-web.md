# Web 工作项两页 + 账本 HTTP API（Plan D / B156.1-看板）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** agentd 新增账本 HTTP API（web 的唯一账本通道），web-console 新增 `/cards`（工作项看板/列表/抽屉）与 `/flows`（工作流/派发模板管理）两页，dock 挂 ▤ 图标带「需要你」角标。**不重构现有 task 看板弹层**（执行域视图保留，spec §6 定案）。

**Architecture:** agentd 无条件打开账本库（把 Plan B 里「Targets>0 才开」的逻辑上提：库始终开，镜像子系统仍按 Targets 门控），Server 持有 `*ledger.Store`，API 是薄查询/动作层——全部业务在 ledger/ledgernode 库里，API 不长逻辑。**看板不说谎的 join**：卡的关联 task 实况从**镜像事件流推导**（每 (target,task) 最后一条 task_mirrored 的 task_type → 实况态），不跨机拨号——单一数据源、离线可用；账面「进行中」× 实况 failed → conflict 标记。前端照既有模式：路由进 `Shell.tsx` 中央 `<Routes>`、dock 图标进 `ProjectTree.tsx` 底部条（工单角标模式复刻）、数据用 `usePoll` 2.5s、纯逻辑 vitest 钉死。

**形态基准：** `prototypes/workbench-ledger/pages/{board,flows}.html`（fork 副本，base/README.md 已记「确认中」）。**信息优先级原则与「抽屉一处看」约束是验收项不是建议**：卡的一切信息只在详情抽屉，无独立弹层；裁决/等人合一为「需要你」就地筛选；保真信号默认沉默。

**Tech Stack:** Go（`internal/agentd` API 层）、React 19 + Vite + Tailwind v4 + react-router 7、vitest + testing-library（既有基座）。

**前置条件：** Plan A/A2/B 已合入（C 不是硬依赖：一键派发按钮先隐藏/禁用到 C 合入，见 Task 5）。基线全绿含 `cd web && npm test`。

---

## File Structure

```
internal/agentd/
  ledgerapi.go          // 账本 API handlers（薄层）
  ledgerapi_test.go     // httptest + 临时 SQLite 账本
internal/ledger/
  taskstate.go          // LatestTaskStates：镜像流推导 task 实况
  taskstate_test.go
cmd/agentd.go           // 账本库开库上提（无条件）；srv.SetLedger
web/src/api/
  ledger.ts             // 账本 API 客户端函数 + TS 类型
web/src/app/cards/
  CardsPage.tsx         // 页骨架：工具条 + 看板/列表切换 + 需要你筛选
  columns.ts            // 状态列映射 + 需要你过滤 + conflict 判定（纯函数）
  columns.test.ts
  CardItem.tsx          // 卡片（chips：附件/⊕并入/⚖/已验/blocked/⚑/conflict/⎇基线）
  CardDrawer.tsx        // 抽屉：流水线/验收/并入区/关系/timeline/评论框
  CardDrawer.test.tsx
  ListView.tsx          // 列表视图（跟随列 + 含归档）
web/src/app/flows/
  FlowsPage.tsx         // 工作流 tab + 模板 tab（版本列表/钉卡数/迁移）
web/src/app/shell/Shell.tsx   // 两条新路由
web/src/app/tree/ProjectTree.tsx  // dock ▤ 图标 + needs 角标
```

---

### Task 1: internal/ledger/taskstate.go——task 实况推导

**Files:**
- Create: `internal/ledger/taskstate.go`
- Test: `internal/ledger/taskstate_test.go`

- [ ] **Step 1: 测试**

```go
package ledger

import (
	"testing"
	"time"
)

func TestLatestTaskStates(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "卡")
	_ = s.LinkTask(c.ID, "mac-02", "T1", "implement", "t")
	for i, typ := range []string{"message", "state", "failed"} {
		_, _ = s.AppendMirroredEvent(c.ID, MirroredEvent{Target: "mac-02", Task: "T1",
			SourceSeq: int64(i + 1), Type: typ, Payload: []byte(`{}`), CreatedAt: time.Now()})
	}
	states, err := s.LatestTaskStates(c.ID)
	if err != nil || len(states) != 1 {
		t.Fatalf("states: %v %+v", err, states)
	}
	if states[0].LastType != "failed" {
		t.Fatalf("实况应取最后一条: %+v", states[0])
	}
	// 无镜像事件的挂账 task：LastType 空（未知，不编）
	_ = s.LinkTask(c.ID, "mac-02", "T2", "review", "t")
	states, _ = s.LatestTaskStates(c.ID)
	if len(states) != 2 {
		t.Fatalf("应两行: %+v", states)
	}
}
```

- [ ] **Step 2: 实现**

```go
// task 实况推导：从镜像事件流取每个挂账 task 的最后一条事件类型。
// 单一数据源（不跨机拨号），代价是实况滞后于镜像——滞后本身有
// MirrorHealth 显性化，看板不会拿陈旧实况冒充新鲜（信号分层）。
package ledger

import "fmt"

// TaskStateRow 挂账 task 的实况摘要。LastType 空 = 尚无镜像事件（未知）。
type TaskStateRow struct {
	Target, TaskID, Purpose, LastType string
	LastSeq                           int64
}

// LatestTaskStates 一张卡全部挂账 task 的实况。
func (s *Store) LatestTaskStates(cardID string) ([]TaskStateRow, error) {
	links, err := s.TasksOf(cardID)
	if err != nil {
		return nil, err
	}
	var out []TaskStateRow
	for _, l := range links {
		row := TaskStateRow{Target: l.Target, TaskID: l.TaskID, Purpose: l.Purpose}
		// task_type 存在镜像事件 payload；按 source_seq 取最后一条
		err := s.db.QueryRow(s.q(`SELECT payload, source_seq FROM card_events
			WHERE source_target = ? AND source_task = ?
			ORDER BY source_seq DESC LIMIT 1`), l.Target, l.TaskID).
			Scan(&row.LastType, &row.LastSeq) // 先扫 payload 再解，见下
		_ = err // sql.ErrNoRows = 无镜像事件，LastType 留空
		out = append(out, row)
	}
	return out, nil
}
```

**执行者注意**：payload 是 `{"task_type":"...","payload":...}` JSON——上面的 Scan 直接进 string 后要 `json.Unmarshal` 取 `task_type` 字段（写实现时展开，测试已断言 `failed`）。`fmt` 若未用则删 import。

- [ ] **Step 3: 跑测试 + Commit**

```bash
git add internal/ledger/
git commit -m "feat(ledger): LatestTaskStates——镜像流推导 task 实况（单一数据源，滞后由健康面显性化）"
```

---

### Task 2: agentd 账本 API

**Files:**
- Create: `internal/agentd/ledgerapi.go`
- Modify: `cmd/agentd.go`（开库上提 + `srv.SetLedger(lst)`）
- Modify: `internal/agentd/server.go`（路由注册 + `SetLedger`）
- Test: `internal/agentd/ledgerapi_test.go`

- [ ] **Step 1: 测试（httptest，覆盖读面 + 动作面 + 未配库降级）**

```go
package agentd

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestLedgerAPI(t *testing.T) {
	lst, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	defer lst.Close()
	_ = lst.EnsureDefaultWorkflows()
	c, _ := lst.CreateCard(ledger.NewCard{Title: "api 卡", Project: "p", Workflow: "bug", Actor: "t"})

	srv := newTestServer(t) // 既有测试基座；没有就照 server_test.go 现有构法
	srv.SetLedger(lst)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// GET /api/cards：视图数组，含派生标记字段
	body := authedGet(t, ts, "/api/cards?project=p")
	if !strings.Contains(body, c.ID) || !strings.Contains(body, `"needs"`) {
		t.Fatalf("cards: %q", body)
	}
	// GET /api/cards/{id}：detail = 卡+关系+事件+task 实况+有效基线
	body = authedGet(t, ts, "/api/cards/"+c.ID)
	for _, key := range []string{`"card"`, `"relations"`, `"events"`, `"task_states"`, `"effective_base_branch"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("detail 缺 %s: %q", key, body)
		}
	}
	// POST move：gate/CAS 错误透传为 409
	code, body := authedPost(t, ts, "/api/cards/"+c.ID+"/move", `{"to":"不存在的状态"}`)
	if code != 400 && code != 409 {
		t.Fatalf("坏转移应 4xx: %d %q", code, body)
	}
	if code, _ := authedPost(t, ts, "/api/cards/"+c.ID+"/move", `{"to":"进行中"}`); code != 200 {
		t.Fatalf("好转移应 200: %d", code)
	}
	// flows / decisions / health 三个读面有形
	for _, p := range []string{"/api/flows", "/api/decisions?open=1", "/api/ledger/health"} {
		if body := authedGet(t, ts, p); body == "" {
			t.Fatalf("%s 空", p)
		}
	}
}

func TestLedgerAPIWithoutLedger(t *testing.T) {
	srv := newTestServer(t) // 不 SetLedger
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// 未配库：503 + 明确文案，不 panic 不 404（前端据此显示引导横幅）
	if code, body := authedGetCode(t, ts, "/api/cards"); code != 503 || !strings.Contains(body, "账本") {
		t.Fatalf("降级: %d %q", code, body)
	}
}
```

（`authedGet/authedPost/newTestServer` 若既有测试文件已有等价物则复用；没有则在本文件写最小版：带 Bearer token 的 http 调用 + 读 body。）

- [ ] **Step 2: 实现 ledgerapi.go**

```go
// 账本 HTTP API：web 看板的唯一账本通道。薄层——业务全在
// internal/ledger，此处只做解码/调用/编码与错误翻译。写动作只有
// move/answer/note 三个（一键派发等 Plan C 动作按钮在前端禁用到
// C 合入后另开 handler）。错误翻译：ErrNotFound→404、ErrCASConflict/
// ErrGateBlocked/ErrBadState→409、其余→500；未配库→503。
package agentd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// SetLedger 注入账本库（agentd 启动时；nil = 未配置，API 降级 503）。
func (s *Server) SetLedger(st *ledger.Store) { s.ledger = st }

// registerLedgerRoutes 在 api 子 mux 上挂账本路由（server.go 的路由
// 注册段调用；与其他 /api 叶子并列）。
func (s *Server) registerLedgerRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/cards", s.withLedger(s.handleCardsList))
	api.HandleFunc("GET /api/cards/{id}", s.withLedger(s.handleCardDetail))
	api.HandleFunc("POST /api/cards/{id}/move", s.withLedger(s.handleCardMove))
	api.HandleFunc("POST /api/cards/{id}/note", s.withLedger(s.handleCardNote))
	api.HandleFunc("GET /api/flows", s.withLedger(s.handleFlows))
	api.HandleFunc("GET /api/decisions", s.withLedger(s.handleDecisions))
	api.HandleFunc("POST /api/decisions/{id}/answer", s.withLedger(s.handleDecisionAnswer))
	api.HandleFunc("GET /api/ledger/health", s.withLedger(s.handleLedgerHealth))
}

func (s *Server) withLedger(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ledger == nil {
			http.Error(w, `{"error":"账本库未配置（config.ledger.dsn 或单机回退）"}`, http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	}
}

func ledgerErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ledger.ErrCASConflict), errors.Is(err, ledger.ErrGateBlocked),
		errors.Is(err, ledger.ErrBadState), errors.Is(err, ledger.ErrBadMerge),
		errors.Is(err, ledger.ErrCycle):
		code = http.StatusConflict
	}
	http.Error(w, `{"error":`+strconvQuote(err.Error())+`}`, code)
}

func (s *Server) handleCardsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	views, err := s.ledger.ListCards(ledger.CardFilter{
		Project: q.Get("project"), Status: q.Get("status"), BaseBranch: q.Get("base_branch"),
		Needs: q.Get("needs") == "1", Blocked: q.Get("blocked") == "1",
		IncludeTerminal: q.Get("all") == "1",
	})
	if err != nil {
		ledgerErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(views))
	for _, v := range views {
		// conflict：账面进行中 × 最后镜像实况 failed（看板不说谎，蓝图 §3.7）
		conflict := false
		if v.Status == ledger.StatusDoing {
			states, err := s.ledger.LatestTaskStates(v.ID)
			if err == nil {
				for _, st := range states {
					if st.LastType == "failed" {
						conflict = true
					}
				}
			}
		}
		out = append(out, map[string]any{
			"id": v.ID, "title": v.Title, "status": v.Status, "priority": v.Priority,
			"project": v.Project, "parent": v.ParentID, "base_branch": v.BaseBranch,
			"attachments": v.Attachments, "following": v.Following,
			"blocked": v.Blocked, "blocked_by": v.BlockedBy, "needs": v.NeedsReason,
			"open_decisions": v.OpenDecisions, "conflict": conflict,
		})
	}
	writeJSON(w, map[string]any{"cards": out})
}

func (s *Server) handleCardDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.ledger.GetCard(id)
	if err != nil {
		ledgerErr(w, err)
		return
	}
	rels, _ := s.ledger.RelationsOf(id)
	evs, _ := s.ledger.EventsFromAsc([]string{id}, 0, 500)
	states, _ := s.ledger.LatestTaskStates(id)
	base, _ := s.ledger.EffectiveBaseBranch(id)
	writeJSON(w, map[string]any{"card": c, "relations": rels, "events": evs,
		"task_states": states, "effective_base_branch": base})
}

func (s *Server) handleCardMove(w http.ResponseWriter, r *http.Request) {
	var req struct{ To, Expect string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	if err := s.ledger.MoveCard(r.PathValue("id"), req.To, req.Expect, "web:"+r.RemoteAddr); err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleCardNote(w http.ResponseWriter, r *http.Request) {
	var req struct{ Body, Kind string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	if req.Kind == "" {
		req.Kind = "普通"
	}
	ev, err := s.ledger.AddComment(r.PathValue("id"), req.Body, req.Kind, "web:"+r.RemoteAddr)
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, ev)
}

func (s *Server) handleFlows(w http.ResponseWriter, r *http.Request) {
	names, err := s.ledger.ListWorkflowNames()
	if err != nil {
		ledgerErr(w, err)
		return
	}
	var flows []map[string]any
	for _, n := range names {
		wf, err := s.ledger.GetWorkflow(n, 0)
		if err != nil {
			continue
		}
		flows = append(flows, map[string]any{"name": wf.Name, "version": wf.Version, "def": wf.Def})
	}
	tnames, _ := s.ledger.ListTemplateNames()
	var tpls []map[string]any
	for _, n := range tnames {
		tp, err := s.ledger.GetTemplate(n, 0)
		if err != nil {
			continue
		}
		tpls = append(tpls, map[string]any{"name": tp.Name, "version": tp.Version, "def": tp.Def})
	}
	writeJSON(w, map[string]any{"workflows": flows, "templates": tpls})
}

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	ds, err := s.ledger.ListDecisions(r.URL.Query().Get("open") == "1")
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"decisions": ds})
}

func (s *Server) handleDecisionAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	id, err := strconvAtoi64(r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"error":"bad id"}`, http.StatusBadRequest)
		return
	}
	if err := s.ledger.AnswerDecision(id, req.Answer, "web:"+r.RemoteAddr); err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleLedgerHealth(w http.ResponseWriter, r *http.Request) {
	rows, err := s.ledger.MirrorHealth()
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"mirror": rows})
}
```

（`writeJSON`/`strconvQuote`/`strconvAtoi64`：server.go 已有等价 helper 就用现成的，没有就在本文件写三行版。`Server` 结构体加 `ledger *ledger.Store` 字段。`cmd/agentd.go`：把 Plan B 的开库段移出 `len(cfg.Targets)>0` 门（库始终开，镜像仍门控），开库后 `srv.SetLedger(lst)`。）

- [ ] **Step 3: 跑测试 + Commit**

Run: `go test ./internal/agentd/ -run TestLedgerAPI -v` → PASS

```bash
git add internal/agentd/ cmd/
git commit -m "feat(agentd): 账本 HTTP API——薄查询/动作层，错误翻译 404/409/503，conflict join 镜像实况"
```

---

### Task 3: 前端 api/ledger.ts + cards/columns.ts（纯逻辑先行）

**Files:**
- Create: `web/src/api/ledger.ts`
- Create: `web/src/app/cards/columns.ts`
- Test: `web/src/app/cards/columns.test.ts`

- [ ] **Step 1: ledger.ts（类型 + 端点函数，照 client.ts 的 request<T> 模式）**

```ts
// 账本 API 客户端：/api/cards、/api/flows、/api/decisions、/api/ledger/health。
// 与 client.ts 同一 request<T> 底座；类型字段名与 Go 侧 wire map 一字不差。
import { request, postJSON } from './client' // request/postJSON 若未导出，先在 client.ts 导出（不改行为）

export interface CardView {
  id: string; title: string; status: string; priority: string; project: string
  parent: string; base_branch: string; attachments: { kind: string; path: string }[]
  following: string; blocked: boolean; blocked_by: string[]; needs: string
  open_decisions: number; conflict: boolean
}
export interface CardDetail {
  card: unknown; relations: { From: string; To: string; Type: string }[]
  events: LedgerEvent[]; task_states: { Target: string; TaskID: string; Purpose: string; LastType: string }[]
  effective_base_branch: string
}
export interface LedgerEvent { Seq: number; CardID: string; Type: string; Actor: string; Payload: unknown; CreatedAt: string }
export interface Decision { ID: number; CardID: string; Body: string; Options: string[] | null; Status: string; Answer: string }
export interface FlowsResp {
  workflows: { name: string; version: number; def: { states: string[]; gates?: Record<string, unknown> } }[]
  templates: { name: string; version: number; def: Record<string, unknown> }[]
}

export const fetchCards = (params: string) =>
  request<{ cards: CardView[] }>(`/api/cards?${params}`).then(r => r.cards ?? [])
export const fetchCardDetail = (id: string) => request<CardDetail>(`/api/cards/${id}`)
export const moveCard = (id: string, to: string) => postJSON(`/api/cards/${id}/move`, { to })
export const noteCard = (id: string, body: string, kind = '普通') => postJSON(`/api/cards/${id}/note`, { body, kind })
export const fetchFlows = () => request<FlowsResp>('/api/flows')
export const fetchDecisions = (openOnly: boolean) =>
  request<{ decisions: Decision[] }>(`/api/decisions${openOnly ? '?open=1' : ''}`).then(r => r.decisions ?? [])
export const answerDecision = (id: number, answer: string) => postJSON(`/api/decisions/${id}/answer`, { answer })
export const fetchLedgerHealth = () => request<{ mirror: { Target: string; UpdatedAt: string }[] }>('/api/ledger/health')
```

- [ ] **Step 2: columns.ts + 测试（产品契约钉死，照 board/columns.ts 的先例）**

```ts
// 工作项看板的纯逻辑：列 = 工作流状态序列（骨架+插入）；「需要你」
// 合一过滤 = 等人 ∪ open 裁决 ∪ conflict；被并卡（following 非空）不在
// 看板成列。这些是产品契约（原型走查确认），测试钉死防漂移。
import type { CardView } from '../../api/ledger'

export function boardColumns(workflowStates: string[]): string[] {
  return [...workflowStates, '终止'] // 终止收尾列折叠展示，其余按工作流序
}
export function cardsInColumn(cards: CardView[], status: string): CardView[] {
  return cards.filter(c => c.status === status && !c.following)
}
export function needsAttention(c: CardView): boolean {
  return Boolean(c.needs) || c.open_decisions > 0 || c.conflict
}
export function filterNeeds(cards: CardView[], on: boolean): CardView[] {
  return on ? cards.filter(needsAttention) : cards
}
```

```ts
import { describe, it, expect } from 'vitest'
import { boardColumns, cardsInColumn, filterNeeds, needsAttention } from './columns'
import type { CardView } from '../../api/ledger'

const card = (over: Partial<CardView>): CardView => ({
  id: 'B1', title: 't', status: '待办', priority: '中', project: 'p', parent: '',
  base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
  needs: '', open_decisions: 0, conflict: false, ...over,
})

describe('工作项看板契约', () => {
  it('被并卡不在看板成列（跟随只在列表/抽屉可见）', () => {
    const cs = [card({ id: 'B1' }), card({ id: 'B2', following: 'B1' })]
    expect(cardsInColumn(cs, '待办').map(c => c.id)).toEqual(['B1'])
  })
  it('需要你 = 等人 ∪ open 裁决 ∪ conflict', () => {
    expect(needsAttention(card({ needs: '审阅超轮' }))).toBe(true)
    expect(needsAttention(card({ open_decisions: 1 }))).toBe(true)
    expect(needsAttention(card({ conflict: true }))).toBe(true)
    expect(needsAttention(card({}))).toBe(false)
    expect(filterNeeds([card({}), card({ id: 'B2', needs: 'x' })], true)).toHaveLength(1)
  })
  it('列序 = 工作流状态序 + 终止收尾', () => {
    expect(boardColumns(['待办', '已出spec', '进行中', '待审阅', '待合并', '已完成']))
      .toEqual(['待办', '已出spec', '进行中', '待审阅', '待合并', '已完成', '终止'])
  })
})
```

- [ ] **Step 3: 跑测试 + Commit**

Run: `cd web && npm test -- --run columns` → PASS

```bash
git add web/src/api/ledger.ts web/src/app/cards/
git commit -m "feat(web): 账本 API 客户端 + 工作项列/需要你/跟随的产品契约（vitest 钉死）"
```

---

### Task 4: CardsPage + 抽屉 + 列表视图

**Files:**
- Create: `web/src/app/cards/{CardsPage,CardItem,CardDrawer,ListView}.tsx`
- Test: `web/src/app/cards/CardDrawer.test.tsx`

实现依据 = 原型 `pages/board.html` 的结构逐区搬运（原型是验收基准，不是灵感来源）。组件拆分与数据流：

- `CardsPage`：`usePoll(fetchCards, 2500)` + `usePoll(fetchDecisions(true), 2500)` + `fetchFlows()` 一次性取列定义；state：`view: 'board'|'list'`、`needsOnly`、`selected: string|null`（抽屉）。工具条：项目/工作流过滤、搜索、`⚑ 需要你 N` 单按钮（N = needsAttention 卡数 + 项目级 open 裁决数；点击就地过滤，筛选态下项目级裁决以琥珀细条出现在列区上方，再点还原）。
- `CardItem`：chips 序与显隐照原型 `chipHtml`：priority / `▤ spec`（title=path）/ `⊕ 并入 N` / `⚖ 裁决 N` / 已验·待真机验 / `⛓ blocked_by` / `⚑ needs` / `✕ 状态冲突` / `⎇ base_branch`（非空才显）/ project。**驱动正常不显示**（保真信号沉默）。`⊕` 点击 = 打开抽屉并滚动到并入区（不是独立面板）。
- `CardDrawer`：`fetchCardDetail(id)`；区块自上而下：状态流水线（当前态高亮）→ kv（项目/工作流@版本/附件/基线/驱动或跟随）→ 验收区 → **并入区（仅承载卡显示：成员行 = id+标题+已验/未验+跟随 badge+拆回按钮——拆回按钮先禁用带 tooltip「CLI: handoff card unmerge」，写动作一期 web 只开 move/note/answer）** → 关系区（双向分组，「承载着」不进关系区）→ 子任务 → 关联执行（task_states 行 + 跳转）→ 分层 timeline（comment 气泡 / 系统 meta 行 / task_mirrored 折叠成组；全部/评论/裁决/系统过滤）→ 评论框（`noteCard`，`#B\d+` 引用提示）。
- `ListView`：列 = ID/标题/状态（following 显示「跟随 X」）/验收/优先级/附件/备注 + 「含归档」checkbox（带 `all=1` 重查）。
- 状态转移：抽屉「转移状态…」按钮内联二次确认（点一下变确认态再点执行，spec §6），调 `moveCard`，409 时把服务端错误文案原样展示（gate 拒绝要说清缺什么）。

`CardDrawer.test.tsx`（关键契约：并入区只在承载卡出现、关系区无「承载着」）：

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CardDrawer } from './CardDrawer'

vi.mock('../../api/ledger', async orig => ({
  ...(await orig()),
  fetchCardDetail: vi.fn().mockResolvedValue({
    card: { ID: 'B147', Title: '承载卡', Status: '进行中', Attachments: [], AcceptanceCriteria: '' },
    relations: [
      { From: 'B144', To: 'B147', Type: 'merged_into' },
      { From: 'B147', To: 'B95', Type: 'blocks' },
    ],
    events: [], task_states: [], effective_base_branch: '',
  }),
}))

describe('抽屉一处看', () => {
  it('承载卡显示并入区成员，关系区不重复「承载着」', async () => {
    render(<CardDrawer id="B147" onClose={() => {}} onOpenCard={() => {}} />)
    expect(await screen.findByText(/并入本卡/)).toBeInTheDocument()
    expect(screen.getByText('B144')).toBeInTheDocument()
    expect(screen.queryByText('承载着')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 1–4:** 按上述规格实现四组件 → `npm test -- --run CardDrawer` PASS → `npm run typecheck && npm run lint` 干净 → Commit

```bash
git add web/src/app/cards/
git commit -m "feat(web): /cards 工作项页——看板/列表/抽屉（并入区/需要你就地筛选/信息优先级），对照原型 board.html"
```

---

### Task 5: FlowsPage + 路由 + dock 图标

**Files:**
- Create: `web/src/app/flows/FlowsPage.tsx`
- Modify: `web/src/app/shell/Shell.tsx`（两条路由 + dock 回调）
- Modify: `web/src/app/tree/ProjectTree.tsx`（▤ 图标 + 角标）

- [ ] **Step 1: FlowsPage**：`fetchFlows()` 单次取；两 tab（工作流/派发模板）。工作流 tab：每条流一卡——状态序列可视（锚点态与插入态区分色）+ gate 标注 + 版本行；模板 tab：executor/target/纪律块 hash/per-target 模型覆盖表 + 版本行。全部只读（编辑走 CLI，页面标注命令提示）——对照原型 `pages/flows.html`。
- [ ] **Step 2: 路由**：`Shell.tsx` 的 `<Routes>` 里、`path="*"` 之前加：

```tsx
    <Route path="/cards" element={<CardsPage />} />
    <Route path="/flows" element={<FlowsPage />} />
```

- [ ] **Step 3: dock**：`ProjectTree.tsx` 底部条、工单按钮旁复刻同构按钮：

```tsx
  <button aria-label="工作项" title="工作项" onClick={onOpenCards} className="relative rounded-md p-1.5 ...">
    <SquareKanban className="size-4" />
    {cardNeedsCount > 0 && (
      <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-state-intervention px-1 text-center text-[10px] leading-4 text-white">
        {cardNeedsCount}
      </span>
    )}
  </button>
```

`onOpenCards = () => navigate('/cards')` 经 props 从 Shell 传入（照 onOpenBoard 的既有链路）；`cardNeedsCount` 由 Shell 的 cards poll 算 needsAttention 数 + 项目级 open 裁决数传下。lucide 图标名以库实际导出为准（`SquareKanban` 或 `LayoutList`）。

- [ ] **Step 4:** `npm test && npm run typecheck && npm run build` 全绿 → Commit

```bash
git add web/src/
git commit -m "feat(web): /flows 流程管理页 + Shell 路由 + dock ▤ 图标带需要你角标"
```

---

### Task 6: 终审 + 原型对照

- [ ] Go 侧：`gofmt -l internal/ ; go vet ./... ; go test ./...` 全绿。
- [ ] 前端：`cd web && npm test && npm run typecheck && npm run lint && npm run build` 全绿。
- [ ] **原型对照（审核者执行，不派发）**：起 agentd（隔离 DataDir+端口，别动本机生产实例——console-acceptance 既有纪律），浏览器并排开真实 `/cards` 与 `prototypes/workbench-ledger/pages/board.html`，逐区核对：需要你筛选态（含项目级细条）、承载卡 ⊕→抽屉并入区、列表跟随行、分层 timeline、保真信号沉默。通过后把 base/README.md 两行推进「已确认」并按 prototyping-in-brainstorm ④ 回流 base（归 finishing-a-development-branch 收尾）。
- [ ] Commit：`test(web): 账本两页整包终审`

---

## Self-Review 记录

1. **写动作最小面**：web 一期只开 move/note/answer 三个写动作（就地确认交互）；merge/unmerge/dispatch 按钮占位禁用带 CLI 提示——Plan C 合入后另开 handler 再点亮，避免 D 对 C 的硬依赖。
2. **conflict join 用镜像流不拨号**：是对 spec「实时 join」的实现裁决——单一数据源 + 滞后显性化（MirrorHealth），代价与理由写在 taskstate.go 头注释。
3. **已知妥协**：ledgerapi 对 detail 的子查询错误吞掉部分（rels/evs 失败返回空段而非 500）——看板宁缺区块不整页白屏；前端类型 `card: unknown`（Go Card 字段大写序列化，抽屉内做一层窄化——若要 snake_case 线格式，得给 Card 加 json tag，那是 Plan A 类型的改动，记账不做）。
4. **验收判据归属**：⑨（双端一致）⑪（裁决看板可答）⑫ 的看板侧、④（状态冲突亮灯）在本 plan 后可真机验；「事件流滞后」UI 亮灯依赖 MirrorHealth 的 60s 判定（CardsPage 顶部健康点，异常才展开——原型 health-dot 形态）。
