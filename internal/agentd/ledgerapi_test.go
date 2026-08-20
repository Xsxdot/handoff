package agentd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
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
