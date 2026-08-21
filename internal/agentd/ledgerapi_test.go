package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
)

func ledgerGet(t *testing.T, env *testAgentdEnv, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func ledgerPost(t *testing.T, env *testAgentdEnv, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.ts.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func ledgerPut(t *testing.T, env *testAgentdEnv, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, env.ts.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func ledgerPatch(t *testing.T, env *testAgentdEnv, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, env.ts.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func ledgerDelete(t *testing.T, env *testAgentdEnv, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, env.ts.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

type ledgerEnv struct {
	*testAgentdEnv
	ledger *ledger.Store
}

func newLedgerEnv(t *testing.T) *ledgerEnv {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	env := newTestAgentdEnv(t)
	env.srv.SetLedger(st)
	return &ledgerEnv{testAgentdEnv: env, ledger: st}
}

func seedCard(t *testing.T, env *ledgerEnv, title string) ledger.Card {
	t.Helper()
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: title, Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return card
}

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

func TestMigrateCardRouteUsesExplicitTargetShape(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "迁移骨架")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/migrate", `{"workflow":"domain","status":"拆解"}`)
	if code != http.StatusOK {
		t.Fatalf("迁移骨架应可调用账本: %d %s", code, body)
	}
	var response struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		From  any    `json:"from"`
		To    any    `json:"to"`
		Event any    `json:"event"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("迁移响应不是 JSON: %v (%s)", err, body)
	}
	if !response.OK || response.ID != card.ID || response.From == nil || response.To == nil || response.Event == nil {
		t.Fatalf("迁移响应缺契约字段: %s", body)
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

// TestAttachContractViaAPI 穿真实 HTTP handler 验证 domain 的 contract 闸附件
// 能从 Web 通道写入账本并读回；CLI 能写不代表浏览器这条接缝也能过。
func TestAttachContractViaAPI(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "domain 契约附件")
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/attachments",
		`{"kind":"contract","path":"docs/superpowers/specs/contract.md"}`)
	if code != http.StatusOK {
		t.Fatalf("挂 contract 附件 code = %d, body = %s", code, body)
	}
	got, err := env.ledger.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读回卡: %v", err)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Kind != "contract" ||
		got.Attachments[0].Path != "docs/superpowers/specs/contract.md" {
		t.Fatalf("contract 附件没挂上: %+v", got.Attachments)
	}
}

// TestAttachmentKindsCoverDefaultWorkflowGates 出厂工作流新增闸 kind 时，
// 必须同步登记 Web 白名单；否则 CLI 与 Web 行为分裂，闸在 Web 永远无法满足。
func TestAttachmentKindsCoverDefaultWorkflowGates(t *testing.T) {
	env := newLedgerEnv(t)
	names, err := env.ledger.ListWorkflowNames()
	if err != nil {
		t.Fatalf("列出厂工作流: %v", err)
	}
	seen := map[string]string{}
	for _, name := range names {
		wf, err := env.ledger.GetWorkflow(name, 0)
		if err != nil {
			t.Fatalf("读取工作流 %s: %v", name, err)
		}
		for _, node := range wf.Def.Nodes {
			if kind := node.Gate.RequireAttachment; kind != "" {
				seen[kind] = name + "/" + node.Name
			}
		}
	}
	for kind, location := range seen {
		if !attachmentKinds[kind] {
			t.Fatalf("工作流 %s 使用的附件 kind %q 未登记到 attachmentKinds", location, kind)
		}
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

func seedChildCard(t *testing.T, env *ledgerEnv, parentID, title string) ledger.Card {
	t.Helper()
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: title, Project: "p", Workflow: "bug", Parent: parentID, Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func TestLedgerAPI(t *testing.T) {
	lst, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lst.Close() })
	if err := lst.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	if err := lst.EnsureDefaultTemplates(); err != nil {
		t.Fatal(err)
	}
	card, err := lst.CreateCard(ledger.NewCard{Title: "api 卡", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}

	env := newTestAgentdEnv(t)
	env.srv.SetLedger(lst)

	code, body := ledgerGet(t, env, "/api/cards?project=p")
	if code != http.StatusOK || !ledgerContainsAll(body, card.ID, `"needs"`, `"open_tickets"`, `"unlinked"`) {
		t.Fatalf("cards: %d %q", code, body)
	}
	code, body = ledgerGet(t, env, "/api/cards/"+card.ID)
	if code != http.StatusOK {
		t.Fatalf("detail status: %d %q", code, body)
	}
	for _, key := range []string{`"card"`, `"relations"`, `"events"`, `"task_states"`, `"effective_base_branch"`} {
		if !ledgerContainsAll(body, key) {
			t.Fatalf("detail 缺 %s: %q", key, body)
		}
	}
	code, body = ledgerPost(t, env, "/api/cards/"+card.ID+"/move", `{"to":"不存在的状态"}`)
	if code != http.StatusBadRequest && code != http.StatusConflict {
		t.Fatalf("坏转移应 4xx: %d %q", code, body)
	}
	if code, body = ledgerPost(t, env, "/api/cards/"+card.ID+"/move", `{"to":"进行中"}`); code != http.StatusOK {
		t.Fatalf("好转移应 200: %d %q", code, body)
	}
	for _, path := range []string{"/api/flows", "/api/decisions?open=1", "/api/ledger/health"} {
		code, body := ledgerGet(t, env, path)
		if code != http.StatusOK || body == "" {
			t.Fatalf("%s: %d %q", path, code, body)
		}
	}
}

// TestCardsListWireCarriesChildrenCounts 锁住 CardView → HTTP wire 的接缝：
// ledger.ListCards 正确不够，手搭响应 map 也必须把子卡计数传给看板。
func TestCardsListWireCarriesChildrenCounts(t *testing.T) {
	env := newLedgerEnv(t)
	parent := seedCard(t, env, "父卡")
	childA := seedChildCard(t, env, parent.ID, "子卡 A")
	childB := seedChildCard(t, env, parent.ID, "子卡 B")
	for _, child := range []ledger.Card{childA, childB} {
		if err := env.ledger.MoveCard(child.ID, ledger.StatusDone, "", "test"); err != nil {
			t.Fatalf("完结子卡 %s: %v", child.ID, err)
		}
	}

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards?project=p")
	if code != http.StatusOK {
		t.Fatalf("列表 code=%d body=%s", code, body)
	}
	var resp struct {
		Cards []struct {
			ID            string `json:"id"`
			ChildrenTotal *int   `json:"children_total"`
			ChildrenDone  *int   `json:"children_done"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解析列表: %v body=%s", err, body)
	}
	for _, card := range resp.Cards {
		if card.ID != parent.ID {
			continue
		}
		if card.ChildrenTotal == nil || card.ChildrenDone == nil {
			t.Fatalf("父卡 wire 缺 children_total/children_done: %s", body)
		}
		if *card.ChildrenTotal != 2 || *card.ChildrenDone != 2 {
			t.Fatalf("父卡 wire 子卡计数错误: total=%d done=%d body=%s",
				*card.ChildrenTotal, *card.ChildrenDone, body)
		}
		return
	}
	t.Fatalf("列表找不到父卡 %s: %s", parent.ID, body)
}

func TestLedgerAPIWithoutLedger(t *testing.T) {
	env := newTestAgentdEnv(t)
	code, body := ledgerGet(t, env, "/api/cards")
	if code != http.StatusServiceUnavailable || !ledgerContainsAll(body, "账本") {
		t.Fatalf("降级: %d %q", code, body)
	}
}

// TestCardDetailReturnsChildren 抽屉是「卡的一切只在一处看」的那一处，
// 子任务随详情给，不新开端点。
func TestCardDetailReturnsChildren(t *testing.T) {
	env := newLedgerEnv(t)
	parent := seedCard(t, env, "父卡")
	child := seedChildCard(t, env, parent.ID, "子卡")

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+parent.ID)
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

// TestCardAcceptRecordsEvidence 验收写入口落事件。
func TestCardAcceptRecordsEvidence(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "待验卡")

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/accept",
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
		code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/accept", bad)
		if code != http.StatusBadRequest {
			t.Fatalf("body=%s 应 400，实得 %d（%s）", bad, code, body)
		}
	}
}

// TestCardAcceptUnknownCard404 卡不存在走既有的错误翻译。
func TestCardAcceptUnknownCard404(t *testing.T) {
	env := newLedgerEnv(t)
	code, _ := ledgerPost(t, env.testAgentdEnv, "/api/cards/B-不存在/accept", `{"evidence":"x"}`)
	if code != http.StatusNotFound {
		t.Fatalf("应 404，实得 %d", code)
	}
}

// TestCardStepReturns202 受理即返回 202——环节要跑几十分钟，
// 200 会让前端以为它已经做完了。
func TestCardStepReturns202(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "demo")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {}

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"review"}`)
	if code != http.StatusAccepted {
		t.Fatalf("应 202，实得 %d（%s）", code, body)
	}
}

// TestCardStepSecondReturns409 同卡第二个环节 409 并说清冲突原因。
func TestCardStepSecondReturns409(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "demo")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	release := holdCardStep(t, env.srv, card.ID)
	defer release()

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"review"}`)
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
	seedCardWithProject(t, env.srv, "demo")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"implement"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("应 400，实得 %d", code)
	}
}

func TestFlowGetReturnsNodes(t *testing.T) {
	env := newLedgerEnv(t)
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/flows/feature")
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var got struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
		Nodes   []struct {
			Name     string `json:"name"`
			Dispatch bool   `json:"dispatch"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码: %v（原文 %s）", err, body)
	}
	if got.Name != "feature" || got.Version < 1 || len(got.Nodes) == 0 {
		t.Fatalf("响应不完整: %+v", got)
	}
}

func TestFlowPutCreatesNewVersion(t *testing.T) {
	env := newLedgerEnv(t)
	payload := `{"nodes":[{"name":"待办","next":"进行中"},{"name":"进行中"}]}`
	code, body := ledgerPut(t, env.testAgentdEnv, "/api/flows/feature", payload)
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var got struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码: %v（原文 %s）", err, body)
	}
	if got.Version < 2 {
		t.Fatalf("应发出新版本，得到 v%d", got.Version)
	}
}

func TestFlowPutRejectsBadNodes(t *testing.T) {
	env := newLedgerEnv(t)
	// Next 指向不存在的节点：校验应在 Store 层拦下，HTTP 翻成 400 而不是 500。
	code, body := ledgerPut(t, env.testAgentdEnv, "/api/flows/feature",
		`{"nodes":[{"name":"A","next":"查无此节点"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d（想要 400），body = %s", code, body)
	}
}

func TestDisciplinesListIncludesBuiltins(t *testing.T) {
	env := newLedgerEnv(t)
	code, body := ledgerGet(t, env.testAgentdEnv, "/api/disciplines")
	if code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var got struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码: %v（原文 %s）", err, body)
	}
	for _, want := range []string{"implement", "review", "spec-draft", "plan-writing", "finishing"} {
		found := false
		for _, name := range got.Names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("纪律块清单缺 %q: %v", want, got.Names)
		}
	}
}

func TestCardStepAcceptsNodeName(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "节点名透传")
	// 占住环节槽位，挡住真派发——本用例只验「节点名不再被白名单拦掉」。
	release := holdCardStep(t, env.srv, card.ID)
	defer release()
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"待审阅"}`)
	if code == http.StatusBadRequest && strings.Contains(body, "review|merge") {
		t.Fatalf("节点名仍被写死的白名单拦掉: %s", body)
	}
	if code != http.StatusConflict {
		t.Logf("注意：槽位已占用时预期 409，实际 %d（%s）", code, body)
	}
}

// TestLedgerHealthReportsDisabled 账本未挂载时 health 必须 200 + enabled:false。
// 为什么不能用 503：这个端点是前端做入口门控的探针，503 与「网络错」
// 无法区分，前端就只能靠猜。其余 /api/cards* 仍走 withLedger 的 503。
func TestLedgerHealthReportsDisabled(t *testing.T) {
	env := newTestAgentdEnv(t) // 不调 SetLedger
	code, body := ledgerGet(t, env, "/api/ledger/health")
	if code != http.StatusOK {
		t.Fatalf("health 应 200，实际 %d body=%s", code, body)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解析 health 报文: %v body=%s", err, body)
	}
	if got["enabled"] != false {
		t.Fatalf("enabled 应为 false，实际报文: %s", body)
	}
}

func ledgerContainsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

// 卡详情要带上该卡的裁决：抽屉是「卡的一切信息只在一处看」的那一处，
// 挂卡的请示以前只在 timeline 里剩一行原文，候选项与答复入口都没有。
func TestCardDetailCarriesDecisions(t *testing.T) {
	lst, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lst.Close() })
	if err := lst.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	card, err := lst.CreateCard(ledger.NewCard{Title: "有请示的卡", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lst.OpenDecision(card.ID, "就地重试还是直接退化？", []string{"重试三次", "立即退化"}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := lst.OpenDecision("", "项目级的那条不该串进来", nil, "t"); err != nil {
		t.Fatal(err)
	}

	env := newTestAgentdEnv(t)
	env.srv.SetLedger(lst)
	code, body := ledgerGet(t, env, "/api/cards/"+card.ID)
	if code != http.StatusOK {
		t.Fatalf("detail status: %d %q", code, body)
	}
	if !ledgerContainsAll(body, `"needs"`) {
		t.Fatalf("详情应带等人原因字段（看板有角标，抽屉得说得出为什么）: %q", body)
	}
	if !ledgerContainsAll(body, `"decisions"`, "就地重试还是直接退化？", "立即退化") {
		t.Fatalf("详情应含本卡裁决与候选项: %q", body)
	}
	if strings.Contains(body, "项目级的那条不该串进来") {
		t.Fatalf("项目级裁决不该出现在卡详情里: %q", body)
	}
}
