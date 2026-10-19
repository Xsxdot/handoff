// R-K1 (a‴) 组装点测试（B435 用户机补验后用户裁决，2026-10-19）：agentd 组装点必须把读活配置的
// console_user 注入 collab 项目房间书写者集——人经控制台 API 在项目房间发言须成功（此前 403
// 「书写者与房间身份不符」）。删掉 setupLedger 里的 rooms.SetProjectRoomHuman 本测试即红。
package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func TestSetupLedgerInjectsConsoleHumanIntoProjectRoomWriters(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", Token: testToken, DataDir: dir,
		StallTimeout: 2 * time.Hour, ConsoleUser: "xsx",
	}
	taskStore, err := store.Open(filepath.Join(dir, "tasks.db"))
	if err != nil {
		t.Fatalf("开任务库: %v", err)
	}
	t.Cleanup(func() { taskStore.Close() })
	srv := agentd.NewServer(cfg, taskStore, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stop, err := setupLedger(cfg, srv, taskStore, ctx, discardLogger(), ledgerBoot{})
	if err != nil {
		t.Fatalf("setupLedger: %v", err)
	}
	t.Cleanup(stop)

	// 夹具：经第二个连接在同一 SQLite 账本上建项目房间并坐下组织者（项目房间创建唯一入口）。
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("开夹具账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fixture := collab.New(ledgerapi.New(st))
	pr, _, err := fixture.EnsureProjectRoom("p", proto.SystemNotifierIdentity)
	if err != nil {
		t.Fatalf("建项目房间: %v", err)
	}
	if _, err := fixture.SitResident(proto.SessionResident{Session: pr.ID, Identity: "agent:org"}); err != nil {
		t.Fatalf("sit: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/rooms/"+pr.ID+"/messages",
		strings.NewReader(`{"body":"组织者在吗"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("人本人在项目房间发言应 200，得 %d %s", resp.StatusCode, body)
	}
	var out struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Seq <= 0 {
		t.Fatalf("响应应带 seq，得 %s（%v）", body, err)
	}
	ev, ok, err := st.EventBySeq(out.Seq)
	if err != nil || !ok || ev.Actor != "user:xsx" {
		t.Fatalf("读回 seq=%d：ok=%v err=%v actor=%q，actor 应为 user:xsx", out.Seq, ok, err, ev.Actor)
	}
}
