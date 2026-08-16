package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestBuildTreeAllMergesAndReportsAbsence 是 §5.3 硬约束的核心断言：
// 一台可达一台不可达，两台都必须出现在 machines 里，失败那台 ok=false 带原文。
func TestBuildTreeAllMergesAndReportsAbsence(t *testing.T) {
	remote := newTestAgentdEnv(t)
	remoteRepo := initGitRepoWithOrigin(t, "git@github.com:x/handoff.git")
	if err := remote.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff-dev", Path: remoteRepo,
		OriginURL: "git@github.com:x/handoff.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("远端登记失败: %v", err)
	}

	localRepo := initGitRepoWithOrigin(t, "git@github.com:x/handoff.git")
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken,
		Targets: map[string]config.Target{
			"devbox": {Addr: remote.ts.URL, Token: testToken},
			"nas":    {Addr: "http://127.0.0.1:1", Token: testToken},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := local.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff", Path: localRepo,
		OriginURL: "git@github.com:x/handoff.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("本机登记失败: %v", err)
	}

	tree := local.srv.buildTreeAll(context.Background())

	// 三台都要在 machines 里（本机 + devbox + nas），nas 必须 ok=false 带原因
	if len(tree.Machines) != 3 {
		t.Fatalf("machines 数 = %d，期望 3：%+v", len(tree.Machines), tree.Machines)
	}
	for _, m := range tree.Machines {
		if m.Name == "nas" {
			if m.Ok || m.Error == "" {
				t.Errorf("不可达的机器必须 ok=false 且 error 非空：%+v", m)
			}
		}
		if m.FetchedAt.IsZero() {
			t.Errorf("每台都要有 fetched_at：%+v", m)
		}
	}
	// 同一个 origin 在两台机器上 → 同一个项目下两个 location，machine 互不相同
	if len(tree.Projects) != 1 {
		t.Fatalf("同 origin 必须归并成一个项目，实得 %d 个", len(tree.Projects))
	}
	seen := map[string]bool{}
	for _, l := range tree.Projects[0].Locations {
		if seen[l.Machine] {
			t.Errorf("同项目下 machine 不得重复：%q", l.Machine)
		}
		seen[l.Machine] = true
	}
	if !seen[""] || !seen["devbox"] {
		t.Errorf("本机与 devbox 的 location 都要在：%+v", tree.Projects[0].Locations)
	}
}

// TestTreeAllDegradesWhenForwarded 断言：带转发头时 scope=all 降级为仅本机。
func TestTreeAllDegradesWhenForwarded(t *testing.T) {
	remote := newTestAgentdEnv(t)
	remoteRepo := initGitRepoWithOrigin(t, "git@github.com:x/remote.git")
	if err := remote.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "cccc666677778888", Name: "remote-only", Path: remoteRepo,
		OriginURL: "git@github.com:x/remote.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("远端登记失败: %v", err)
	}
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 带转发头请求 scope=all：必须降级为仅本机——防环优先于范围，不扇出
	var resp proto.ProjectTreeResp
	req, _ := http.NewRequest(http.MethodGet, local.ts.URL+"/api/projects/tree?scope=all", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(forwardedHeader, "1")
	rresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET scope=all(带转发头): %v", err)
	}
	defer rresp.Body.Close()
	if err := json.NewDecoder(rresp.Body).Decode(&resp); err != nil {
		t.Fatalf("解码响应: %v", err)
	}
	if rresp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rresp.StatusCode)
	}
	if resp.Machines != nil {
		t.Errorf("带转发头的请求 machines 栏必须缺席（不扇出），实得 %+v", resp.Machines)
	}
	for _, p := range resp.Projects {
		if p.ProjectID == "cccc666677778888" {
			t.Error("带转发头的请求不得包含远端数据（不扇出）")
		}
	}
}
