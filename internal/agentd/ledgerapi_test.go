package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

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
	return ledgerPostWithClient(t, env, http.DefaultClient, path, body)
}

// ledgerPostWithClient 同 ledgerPost，但由调用方提供 HTTP 客户端。
//
// 为什么需要它：只有调用方持有的 Transport 才看得见本次连接的**客户端源地址**，
// 而服务端的 actor 回退正是由 r.RemoteAddr 推出来的。用 http.DefaultClient 就
// 拿不到这个地址，只能拿服务端监听地址去猜——那是端口巧合，不是判据。
func ledgerPostWithClient(t *testing.T, env *testAgentdEnv, cl *http.Client, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.ts.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
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

// TestMigrateAPIProjectsFromTo 迁移响应必须带 from/to/event——穿真实 HTTP
// handler 断言，Store 有值不代表 wire 上有值（ChildrenTotal 教训）。
func TestMigrateAPIProjectsFromTo(t *testing.T) {
	env := newLedgerEnv(t)
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: "迁移投影", Project: "p", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
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
			Version  *int    `json:"workflow_version"`
		} `json:"from"`
		To *struct {
			Workflow *string `json:"workflow"`
			Status   *string `json:"status"`
			Version  *int    `json:"workflow_version"`
		} `json:"to"`
		Event *struct {
			Type *string `json:"type"`
		} `json:"event"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码响应: %v body=%s", err, body)
	}
	if resp.OK == nil || !*resp.OK {
		t.Fatalf("响应 ok 键缺失或为假：%s", body)
	}
	if resp.From == nil || resp.To == nil || resp.Event == nil {
		t.Fatalf("响应缺 from/to/event：%s", body)
	}
	if resp.From.Workflow == nil || *resp.From.Workflow != "triage" {
		t.Fatalf("from.workflow 不对：%s", body)
	}
	if resp.To.Workflow == nil || *resp.To.Workflow != "bug" ||
		resp.To.Status == nil || *resp.To.Status != "进行中" {
		t.Fatalf("to 不对：%s", body)
	}
	if resp.To.Version == nil {
		t.Fatal("to.workflow_version 键缺失——版本必须回给调用方（契约拍板记录②）")
	}
	if resp.Event.Type == nil || *resp.Event.Type != "workflow_migrated" {
		t.Fatalf("event.type 不对：%s", body)
	}
}

// TestMigrateAPIRejectsInFlightWith409 在飞时 handler 把 Store 的错误翻成 409，
// 不自己做检查（契约拍板记录④：handler 只翻错误码）。
func TestMigrateAPIRejectsInFlightWith409(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "在飞 409")
	if err := env.ledger.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Target: "acc", TaskID: "T-1", Branch: "cards/" + card.ID + "-T-1",
		Purpose: ledger.PurposeImplement, Template: "feature-impl", Actor: "test",
	}); err != nil {
		t.Fatalf("写派发事件: %v", err)
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/migrate",
		`{"workflow":"bug","status":"进行中"}`)
	if code != http.StatusConflict {
		t.Fatalf("在飞迁移应 409，实得 %d body=%s", code, body)
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

func TestPatchCardBaseBranch(t *testing.T) {
	env := newLedgerEnv(t)
	card := seedCard(t, env, "基线 API")
	code, body := ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID,
		`{"base_branch":"cards/api-base"}`)
	if code != http.StatusOK {
		t.Fatalf("写基线 code=%d body=%s", code, body)
	}
	got, err := env.ledger.GetCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := env.ledger.EffectiveBaseBranch(card.ID)
	if err != nil || got.BaseBranch != "cards/api-base" || effective != "cards/api-base" {
		t.Fatalf("基线未写入: card=%+v effective=%q err=%v", got, effective, err)
	}

	if code, body = ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID, `{}`); code != http.StatusOK {
		t.Fatalf("空 patch code=%d body=%s", code, body)
	}
	if code, body = ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID, `{"priority":"高"}`); code != http.StatusOK {
		t.Fatalf("仅改 priority code=%d body=%s", code, body)
	}
	got, _ = env.ledger.GetCard(card.ID)
	if got.BaseBranch != "cards/api-base" {
		t.Fatalf("省略 base_branch 却改变基线: %q", got.BaseBranch)
	}

	parent := seedCard(t, env, "父卡基线")
	if err := env.ledger.SetCardBaseBranch(parent.ID, "parent/api-base", "test"); err != nil {
		t.Fatal(err)
	}
	child := seedChildCard(t, env, parent.ID, "子卡基线")
	if code, body = ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+child.ID, `{"base_branch":"child/api-base"}`); code != http.StatusOK {
		t.Fatalf("子卡写基线 code=%d body=%s", code, body)
	}
	if code, body = ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+child.ID, `{"base_branch":""}`); code != http.StatusOK {
		t.Fatalf("清除子卡基线 code=%d body=%s", code, body)
	}
	childGot, _ := env.ledger.GetCard(child.ID)
	childEffective, _ := env.ledger.EffectiveBaseBranch(child.ID)
	if childGot.BaseBranch != "" || childEffective != "parent/api-base" {
		t.Fatalf("清除后未继承父卡: card=%+v effective=%q", childGot, childEffective)
	}
}

func TestPatchCardBaseBranchErrorsAndPartialOrder(t *testing.T) {
	env := newLedgerEnv(t)
	missingCode, missingBody := ledgerPatch(t, env.testAgentdEnv, "/api/cards/B205-missing",
		`{"base_branch":"cards/missing"}`)
	if missingCode != http.StatusNotFound {
		t.Fatalf("未知卡应 404，实得 %d body=%s", missingCode, missingBody)
	}

	card := seedCard(t, env, "冻结基线")
	first := ledger.DispatchSnapshot{Target: "acc", TaskID: "T-first", Branch: "cards/api-first",
		Purpose: ledger.PurposeImplement, Template: "feature-impl", Actor: "test"}
	if err := env.ledger.RecordDispatch(card.ID, first); err != nil {
		t.Fatal(err)
	}
	events, err := env.ledger.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var firstAt time.Time
	for _, event := range events {
		if event.Type == ledger.EvDispatched {
			firstAt = event.CreatedAt
			break
		}
	}
	if firstAt.IsZero() {
		t.Fatal("未找到首次派发时间")
	}
	code, body := ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+card.ID,
		`{"base_branch":"cards/rejected"}`)
	if code != http.StatusConflict || !strings.Contains(body, "cards/api-first") ||
		!strings.Contains(body, firstAt.Format(time.RFC3339Nano)) {
		t.Fatalf("冻结错误应 409 且带首次出处: code=%d body=%s", code, body)
	}
	got, _ := env.ledger.GetCard(card.ID)
	if got.BaseBranch != "" {
		t.Fatalf("冻结拒绝后卡被改写: %q", got.BaseBranch)
	}

	partial := seedCard(t, env, "冻结部分成功")
	if err := env.ledger.RecordDispatch(partial.ID, ledger.DispatchSnapshot{
		Target: "acc", TaskID: "T-partial", Branch: "cards/partial-first",
		Purpose: ledger.PurposeImplement, Template: "feature-impl", Actor: "test",
	}); err != nil {
		t.Fatal(err)
	}
	code, body = ledgerPatch(t, env.testAgentdEnv, "/api/cards/"+partial.ID,
		`{"title":"标题已改","base_branch":"cards/rejected"}`)
	if code != http.StatusConflict || !strings.Contains(body, "cards/partial-first") {
		t.Fatalf("同时 patch 冻结基线应 409: code=%d body=%s", code, body)
	}
	partialGot, _ := env.ledger.GetCard(partial.ID)
	if partialGot.Title != "标题已改" || partialGot.BaseBranch != "" {
		t.Fatalf("部分顺序不符: %+v", partialGot)
	}
}

func TestPatchCardBaseBranchWithoutLedger(t *testing.T) {
	env := newTestAgentdEnv(t)
	code, body := ledgerPatch(t, env, "/api/cards/B205-missing", `{"base_branch":"cards/no-ledger"}`)
	if code != http.StatusServiceUnavailable || strings.Contains(body, `"ok":true`) || !strings.Contains(body, "账本") {
		t.Fatalf("无账本应 503 且不假报成功: code=%d body=%s", code, body)
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

func TestCardsListWireCarriesBaseFrozen(t *testing.T) {
	encoded, err := json.Marshal(ledgerCardViewWire(ledger.CardView{}, false, 0))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if raw, ok := fields["base_frozen"]; !ok || string(raw) != "false" {
		t.Fatalf("CardView wire 缺 base_frozen=false：%s", encoded)
	}
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
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {}

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"待审阅"}`)
	if code != http.StatusAccepted {
		t.Fatalf("应 202，实得 %d（%s）", code, body)
	}
}

// TestCardStepSecondReturns409 同卡第二个环节 409 并说清冲突原因。
func TestCardStepSecondReturns409(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	release := holdCardStep(t, env.srv, card.ID)
	defer release()

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"待审阅"}`)
	if code != http.StatusConflict {
		t.Fatalf("应 409，实得 %d", code)
	}
	if !strings.Contains(body, card.ID) || !strings.Contains(body, "正在运行") {
		t.Fatalf("409 要说清是哪张卡的什么在跑：%s", body)
	}
}

// TestCardStepAcceptsImplementWithoutInlineFile implement 不因节点名被拒绝；
// 冻结请求没有调用方本地文件字段，规范 actor 请求应进入同一异步 runner。
func TestCardStepAcceptsImplementWithoutInlineFile(t *testing.T) {
	env := newLedgerEnv(t)
	seedImplementCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	runnerCh := make(chan *ledgerstep.StepRunner, 1)
	env.srv.runStepFn = func(_ context.Context, runner *ledgerstep.StepRunner, _, _ string) {
		runnerCh <- runner
	}
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"implement","actor":"cli:u@h#123"}`)
	if code != http.StatusAccepted {
		t.Fatalf("implement 应 202，实得 %d（%s）", code, body)
	}
	select {
	case runner := <-runnerCh:
		if runner.Session != "cli:u@h#123" || runner.Dispatcher.Actor != "cli:u@h#123" {
			t.Fatalf("actor 未落到 runner 双位置：session=%q dispatcher=%q", runner.Session, runner.Dispatcher.Actor)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("implement 202 后未启动 runner")
	}
}

// TestCardStepLegacyActorFallback preserves the old dashboard body while making its actor explicit internally.
func TestCardStepLegacyActorFallback(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	runnerCh := make(chan *ledgerstep.StepRunner, 1)
	env.srv.runStepFn = func(_ context.Context, runner *ledgerstep.StepRunner, _, _ string) {
		runnerCh <- runner
	}
	// 期望值必须是**本次连接的客户端源地址**（服务端 r.RemoteAddr 的来源），
	// 不是 httptest 服务端的监听地址：两者只在内核恰好把相邻端口先后分给监听
	// 套接字与连接套接字时才相等——Linux 上常成立、macOS 上恒不成立。原先拿
	// env.ts.URL 当期望值是在赌端口巧合，本机 6/6 必红。
	var localAddr string
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err == nil {
				localAddr = conn.LocalAddr().String()
			}
			return conn, err
		},
	}
	defer transport.CloseIdleConnections()
	code, body := ledgerPostWithClient(t, env.testAgentdEnv, &http.Client{Transport: transport},
		"/api/cards/"+card.ID+"/step", `{"step":"待审阅"}`)
	if code != http.StatusAccepted {
		t.Fatalf("legacy 请求应 202，实得 %d（%s）", code, body)
	}
	if localAddr == "" {
		t.Fatal("未记录到客户端源地址，期望值无从构造")
	}
	select {
	case runner := <-runnerCh:
		want := "web:" + localAddr
		if runner.Session != want || runner.Dispatcher.Actor != want {
			t.Fatalf("legacy actor = session %q dispatcher %q, want %q", runner.Session, runner.Dispatcher.Actor, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("legacy 202 后未启动 runner")
	}
}

// TestCardStepRejectsEmptyActor rejects an explicit empty actor instead of using the legacy fallback.
func TestCardStepRejectsEmptyActor(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	called := make(chan struct{}, 1)
	env.srv.runStepFn = func(_ context.Context, _ *ledgerstep.StepRunner, _, _ string) { called <- struct{}{} }
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", `{"step":"待审阅","actor":""}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "actor") {
		t.Fatalf("显式空 actor 应 400，实得 %d（%s）", code, body)
	}
	select {
	case <-called:
		t.Fatal("显式空 actor 不应启动 runner")
	default:
	}
}

// TestCardStepRejectsEmptyStep rejects both an absent step and an explicit empty step.
func TestCardStepRejectsEmptyStep(t *testing.T) {
	for _, body := range []string{`{"actor":"cli:u@h#1"}`, `{"step":"","actor":"cli:u@h#1"}`} {
		t.Run(body, func(t *testing.T) {
			env := newLedgerEnv(t)
			seedCardWithProject(t, env.srv, "handoff")
			card, err := env.ledger.GetCard("B1")
			if err != nil {
				t.Fatal(err)
			}
			called := make(chan struct{}, 1)
			env.srv.runStepFn = func(_ context.Context, _ *ledgerstep.StepRunner, _, _ string) { called <- struct{}{} }
			code, response := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step", body)
			if code != http.StatusBadRequest || !strings.Contains(response, "step") {
				t.Fatalf("空 step 应 400，实得 %d（%s）", code, response)
			}
			select {
			case <-called:
				t.Fatal("空 step 不应启动 runner")
			default:
			}
		})
	}
}

// TestCardStepIgnoresUnknownFields locks the deliberately loose JSON decoding contract.
func TestCardStepIgnoresUnknownFields(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	runnerCh := make(chan *ledgerstep.StepRunner, 1)
	env.srv.runStepFn = func(_ context.Context, runner *ledgerstep.StepRunner, _, _ string) { runnerCh <- runner }
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step",
		`{"step":"待审阅","actor":"cli:u@h#1","future_optional":"x","plan_path":"local.md"}`)
	if code != http.StatusAccepted {
		t.Fatalf("未知字段不应拒绝，实得 %d（%s）", code, body)
	}
	select {
	case runner := <-runnerCh:
		if runner.Session != "cli:u@h#1" || runner.Dispatcher.Actor != "cli:u@h#1" {
			t.Fatalf("未知字段请求 actor 丢失：%+v", runner)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未知字段请求未启动 runner")
	}
}

// TestCardStepPropagatesRequestFields verifies one request populates the same runner's fields and both actor locations.
func TestCardStepPropagatesRequestFields(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	runnerCh := make(chan *ledgerstep.StepRunner, 1)
	env.srv.runStepFn = func(_ context.Context, runner *ledgerstep.StepRunner, _, _ string) { runnerCh <- runner }
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step",
		`{"step":"待审阅","target":"linux-01","executor":"grok","model":"grok-model","extra":"本轮只修 F1","actor":"cli:u@h#1"}`)
	if code != http.StatusAccepted {
		t.Fatalf("规范请求应 202，实得 %d（%s）", code, body)
	}
	select {
	case runner := <-runnerCh:
		if runner.Target != "linux-01" || runner.Executor != "grok" || runner.Model != "grok-model" || runner.Extra != "本轮只修 F1" {
			t.Fatalf("请求覆盖未落到 runner：target=%q executor=%q model=%q extra=%q", runner.Target, runner.Executor, runner.Model, runner.Extra)
		}
		if runner.Session != "cli:u@h#1" || runner.Dispatcher.Actor != "cli:u@h#1" {
			t.Fatalf("actor 未落到 runner 双位置：session=%q dispatcher=%q", runner.Session, runner.Dispatcher.Actor)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("规范请求未启动 runner")
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

func TestFlowNodeProducesRoundTripsThroughHTTPWire(t *testing.T) {
	env := newLedgerEnv(t)
	payload := []byte("{\"nodes\":[{\"name\":\"legacy\"},{\"name\":\"breakdown\",\"produces\":{\"kind\":\"doc\",\"path\":\"docs/b201-breakdown.md\"}}]}")
	code, body := ledgerPut(t, env.testAgentdEnv, "/api/flows/feature", string(payload))
	if code != http.StatusOK {
		t.Fatalf("put code = %d, body = %s", code, body)
	}

	code, body = ledgerGet(t, env.testAgentdEnv, "/api/flows/feature")
	if code != http.StatusOK {
		t.Fatalf("get code = %d, body = %s", code, body)
	}
	var got struct {
		Nodes []struct {
			Name     string `json:"name"`
			Produces *struct {
				Kind string `json:"kind"`
				Path string `json:"path"`
			} `json:"produces"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解码 flow: %v（原文 %s）", err, body)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes 数量 = %d, want 2", len(got.Nodes))
	}
	if got.Nodes[0].Produces != nil {
		t.Fatalf("legacy 节点字段缺失必须保持 nil: %+v", got.Nodes[0].Produces)
	}
	if got.Nodes[1].Produces == nil ||
		got.Nodes[1].Produces.Kind != "doc" ||
		got.Nodes[1].Produces.Path != "docs/b201-breakdown.md" {
		t.Fatalf("produces wire round-trip 失败: %+v", got.Nodes[1].Produces)
	}
}

func TestLedgerNodeWirePreservesProduces(t *testing.T) {
	node := ledger.NodeDef{
		Name:     "breakdown",
		Produces: &ledger.NodeOutput{Kind: "doc", Path: "docs/b201-breakdown.md"},
	}
	raw, err := json.Marshal(ledgerNodeWire(node))
	if err != nil {
		t.Fatalf("编码节点 wire: %v", err)
	}
	var got struct {
		Produces *struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"produces"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("解码节点 wire: %v", err)
	}
	if got.Produces == nil || got.Produces.Kind != "doc" || got.Produces.Path != "docs/b201-breakdown.md" {
		t.Fatalf("wire 丢失 produces: %s", raw)
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

// TestCardStepRejectsUnknownCard 钉死「不存在的卡当场 404，而不是先回 202 再在
// 后台悄悄失败」。
//
// 为什么这条必须有：受理是异步的，卡不存在时 StepRunner 在 goroutine 里才发现，
// 那时既没有卡可以落事件，也没有响应可以带错——失败只剩 agentd 日志一处。
// 调用方拿到的是「已受理」，从此再无任何可看之处。B185 之前 CLI 在进程内跑
// runner，这种输入是当场报错的；异步化不应把它变成静默黑洞。
func TestCardStepRejectsUnknownCard(t *testing.T) {
	env := newLedgerEnv(t)
	called := make(chan struct{}, 1)
	env.srv.runStepFn = func(_ context.Context, _ *ledgerstep.StepRunner, _, _ string) { called <- struct{}{} }
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/B999999/step",
		`{"step":"review","actor":"cli:u@h#1"}`)
	if code != http.StatusNotFound {
		t.Fatalf("不存在的卡应 404，实得 %d（%s）", code, body)
	}
	select {
	case <-called:
		t.Fatal("不存在的卡不应启动 runner")
	default:
	}
}

// TestCardStepRejectsUnknownNode 钉死「节点名不在卡钉住的工作流里时当场 400」。
//
// 与卡不存在同一族：节点名打错今天也是先 202 再在后台失败，而节点解析失败发生在
// 驱动认领之前，卡上连一条事件都不会留。
func TestCardStepRejectsUnknownNode(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	called := make(chan struct{}, 1)
	env.srv.runStepFn = func(_ context.Context, _ *ledgerstep.StepRunner, _, _ string) { called <- struct{}{} }
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/B1/step",
		`{"step":"这个节点不存在","actor":"cli:u@h#1"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("未知节点应 400，实得 %d（%s）", code, body)
	}
	if !strings.Contains(body, "这个节点不存在") {
		t.Fatalf("400 报文应点名那个节点，实得 %s", body)
	}
	select {
	case <-called:
		t.Fatal("未知节点不应启动 runner")
	default:
	}
}

// TestCardStepRejectsTrailingGarbage 钉死「请求体带尾随内容时整体拒绝」。
//
// 为什么：json.Decoder.Decode 只吃第一个 JSON 值，尾随内容被静默丢弃——一个被
// 中途截断又重发的请求体会被当成合法请求受理。受理是有副作用的（认领驱动、
// 派发任务），不能建立在「只看了前半句」上。
func TestCardStepRejectsTrailingGarbage(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	called := make(chan struct{}, 1)
	env.srv.runStepFn = func(_ context.Context, _ *ledgerstep.StepRunner, _, _ string) { called <- struct{}{} }
	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/B1/step",
		`{"step":"review","actor":"cli:u@h#1"} {"step":"另一个"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("尾随内容应 400，实得 %d（%s）", code, body)
	}
	select {
	case <-called:
		t.Fatal("带尾随内容的请求不应启动 runner")
	default:
	}
}
