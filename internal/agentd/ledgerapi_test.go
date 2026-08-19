package agentd

import (
	"bytes"
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
	if !ledgerContainsAll(body, `"decisions"`, "就地重试还是直接退化？", "立即退化") {
		t.Fatalf("详情应含本卡裁决与候选项: %q", body)
	}
	if strings.Contains(body, "项目级的那条不该串进来") {
		t.Fatalf("项目级裁决不该出现在卡详情里: %q", body)
	}
}
