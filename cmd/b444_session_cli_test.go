package cmd

// b444_session_cli_test.go —— B444 P2 CLI 半边：`session create --project/--card/--squad`（HTTP 编排）、
// `session rename`（--agent 本地 / 缺省经 agentd）、`session resident launch`（plan §3 P2 CLI 项）。
// HTTP 形态用 stub 锁命令侧契约（请求路径与体、成功渲染、服务端错误透传）；--agent 改名走真账本，
// 驻场者身份的权限与 CAS 由 collab 唯一入口执法（这里验 CLI 接线确实经过它）。

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/collab"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB444SessionCreateInProjectPostsToAgentd(t *testing.T) {
	dir := t.TempDir()
	var gotPath, gotMethod, gotBody string
	stubSquadAgentd(t, dir, http.StatusOK,
		`{"id":"session:7","title":"登录页白屏 等 2 张","owner":"user:sy","archived":false,"home_project":"handoff",`+
			`"cards":["B1","B2"],"created_at":"2026-10-19T00:00:00Z","updated_at":"2026-10-19T00:00:00Z",`+
			`"resident":{"identity":"agent:coord","carrier":"c1","carrier_machine":"linux-01"}}`,
		func(r *http.Request, body string) { gotPath, gotMethod, gotBody = r.URL.Path, r.Method, body })
	out, _, err := runLedgerCLI(t, dir, "session", "create", "--project", "handoff", "--card", "B1", "--card", "B2", "--squad", "coord")
	if err != nil {
		t.Fatalf("项目内建会话: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/sessions" {
		t.Fatalf("请求形状不符: %s %s", gotMethod, gotPath)
	}
	var req proto.SessionCreateReq
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatal(err)
	}
	if req.Project != "handoff" || req.Squad != "coord" || strings.Join(req.Cards, ",") != "B1,B2" || req.Title != "" || req.Owner != "" {
		t.Fatalf("请求体不符: %+v", req)
	}
	var resp proto.SessionCreateResp
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil || resp.ID != "session:7" ||
		resp.HomeProject != "handoff" || resp.Resident == nil || resp.Resident.Identity != "agent:coord" {
		t.Fatalf("stdout 应为 SessionCreateResp 一行: %q err=%v", out, err)
	}
}

func TestB444SessionCreateResidentErrorHintsRetry(t *testing.T) {
	dir := t.TempDir()
	stubSquadAgentd(t, dir, http.StatusOK,
		`{"id":"session:8","title":"新会话","owner":"user:sy","archived":false,"home_project":"handoff",`+
			`"created_at":"2026-10-19T00:00:00Z","updated_at":"2026-10-19T00:00:00Z","resident":{"error":"载体登录失效"}}`, nil)
	_, errOut, err := runLedgerCLI(t, dir, "session", "create", "--project", "handoff", "--squad", "coord")
	if err != nil {
		t.Fatalf("首轮失败服务端仍 200，CLI 应成功退出: %v", err)
	}
	if !strings.Contains(errOut, "载体登录失效") || !strings.Contains(errOut, "session resident launch session:8") {
		t.Fatalf("stderr 应给出原因与重试命令: %q", errOut)
	}
}

func TestB444SessionCreateFlagValidation(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "session", "create", "--card", "B1"); err == nil || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("--card 无 --project 应拒并指路: %v", err)
	}
	stubSquadAgentd(t, dir, http.StatusBadRequest, `{"error":"未登记协调者小队：先登记载体，再登记协调者小队（示例：handoff squad create --name coord）"}`, nil)
	_, errOut, err := runLedgerCLI(t, dir, "session", "create", "--project", "handoff")
	if err == nil || !strings.Contains(err.Error()+errOut, "squad create") {
		t.Fatalf("服务端 400 指路应透传: %v %q", err, errOut)
	}
}

func TestB444SessionRenameAgentUsesLocalLedger(t *testing.T) {
	dir := t.TempDir()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := collab.New(ledgerapi.New(st))
	session, err := svc.CreateSession(proto.SessionUntitledTitle, "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SitResident(proto.SessionResident{Session: session.ID, Identity: "agent:coord",
		DriverMode: proto.ResidentDriverPush, PushCLI: "opencode", PushSession: "ses_1", Carrier: "c1", CarrierMachine: "linux-01"}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	if _, _, err := runLedgerCLI(t, dir, "session", "rename", session.ID, "冒名", "--agent", "other"); err == nil {
		t.Fatal("非驻场者 --agent 改名应失败")
	}
	out, _, err := runLedgerCLI(t, dir, "session", "rename", session.ID, "排查登录", "--agent", "coord", "--expect-title", proto.SessionUntitledTitle)
	if err != nil {
		t.Fatalf("驻场者 CAS 改名: %v", err)
	}
	var renamed proto.Session
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &renamed); err != nil || renamed.Title != "排查登录" {
		t.Fatalf("stdout 应为改名后的 Session: %q err=%v", out, err)
	}
	_, _, err = runLedgerCLI(t, dir, "session", "rename", session.ID, "再改", "--agent", "coord", "--expect-title", proto.SessionUntitledTitle)
	if err == nil || !strings.Contains(err.Error(), "保留") {
		t.Fatalf("标题已非占位名时 CAS 应拒并提示保留: %v", err)
	}
	st, err = ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.GetSession(session.ID)
	if err != nil || got.Title != "排查登录" {
		t.Fatalf("账本标题应为驻场者起的名: %+v err=%v", got, err)
	}
	events, err := st.EventsFromAsc(nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		if ev.Type == ledger.EvSessionRenamed {
			n++
			if ev.Actor != "agent:coord" {
				t.Fatalf("改名事件 actor 应为 agent:coord，得 %q", ev.Actor)
			}
		}
	}
	if n != 1 {
		t.Fatalf("应恰一条改名事件，得 %d", n)
	}
}

func TestB444SessionRenameWithoutAgentPostsToAgentd(t *testing.T) {
	dir := t.TempDir()
	var gotPath, gotBody string
	stubSquadAgentd(t, dir, http.StatusOK,
		`{"id":"session:3","title":"人起的名","owner":"user:sy","archived":false,"created_at":"2026-10-19T00:00:00Z","updated_at":"2026-10-19T00:00:00Z"}`,
		func(r *http.Request, body string) { gotPath, gotBody = r.URL.Path, body })
	if _, _, err := runLedgerCLI(t, dir, "session", "rename", "session:3", "人起的名", "--expect-title", "新会话"); err != nil {
		t.Fatalf("控制台身份改名: %v", err)
	}
	var req proto.SessionRenameReq
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/sessions/session:3/rename" || req.Title != "人起的名" || req.ExpectTitle != "新会话" {
		t.Fatalf("改名请求不符: path=%s body=%+v", gotPath, req)
	}
}

func TestB444SessionResidentLaunchPostsSquad(t *testing.T) {
	dir := t.TempDir()
	var gotPath, gotBody string
	stubSquadAgentd(t, dir, http.StatusOK, `{"identity":"agent:coord","carrier":"c1","carrier_machine":"linux-01"}`,
		func(r *http.Request, body string) { gotPath, gotBody = r.URL.Path, body })
	out, _, err := runLedgerCLI(t, dir, "session", "resident", "launch", "session:3", "--squad", "coord")
	if err != nil {
		t.Fatalf("叫驻场者: %v", err)
	}
	var req proto.SessionResidentLaunchReq
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil || req.Squad != "coord" ||
		gotPath != "/api/sessions/session:3/resident/launch" {
		t.Fatalf("请求不符: path=%s body=%s err=%v", gotPath, gotBody, err)
	}
	if !strings.Contains(out, `"identity":"agent:coord"`) {
		t.Fatalf("stdout 应为驻场结果: %q", out)
	}
	stubSquadAgentd(t, dir, http.StatusConflict, `{"error":"会话 session:3 已有驻场者 agent:coord"}`, nil)
	if _, errOut, err := runLedgerCLI(t, dir, "session", "resident", "launch", "session:3"); err == nil ||
		!strings.Contains(err.Error()+errOut, "已有驻场者") {
		t.Fatalf("409 应透传: %v %q", err, errOut)
	}
}
