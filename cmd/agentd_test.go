// agentd 命令测试：HTTP server 超时配置（P1-3）。
//
// 覆盖：newAgentdHTTPServer 的四个超时字段全部非零——这是「防 slowloris / 防
// 半死连接挂起」的配置级守卫；另断言 WriteTimeout ≥ agentd.RunCmdTimeout——
// handleTaskRun 同步执行 RunCmd，写超时小于命令执行上限会把长审阅命令掐断
// （退出码 124 契约无法兑现，见 cmd/agentd.go newAgentdHTTPServer 注释）。
// http.Server 超时行为本身由 net/http 保证，httptest 用自己的 server 无法覆盖，
// 故只做配置存在性断言（why 见 P1-3 修法）。
package cmd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// 注册表必须认识全部执行者名：dispatch --executor <name> 的路由前提。
//
// 为什么每个名字都要断言而不是只断言数量：B2（claude）与 B3（grok）是并行开发的
// 两条分支，各自往注册表里加了一行，合并时 cmd/agentd.go 这一处必然冲突——手工
// 解冲突时漏掉任一行都不会编译报错，症状要拖到「派发时报未注册」才暴露。
func TestAdapterRegistryHasAllExecutors(t *testing.T) {
	ads := defaultAdapters(slog.Default())
	for _, want := range []string{"opencode", "claude", "grok", "fake"} {
		if _, ok := ads[want]; !ok {
			names := make([]string, 0, len(ads))
			for n := range ads {
				names = append(names, n)
			}
			t.Fatalf("adapter 注册表缺 %s，实际注册: %v", want, names)
		}
	}
}

func TestNewAgentdHTTPServerTimeouts(t *testing.T) {
	s := newAgentdHTTPServer("127.0.0.1:0", http.NewServeMux())
	if s.Addr != "127.0.0.1:0" {
		t.Errorf("Addr=%q, want 127.0.0.1:0", s.Addr)
	}
	if s.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout 必须非零（防 slowloris），实际 %v", s.ReadHeaderTimeout)
	}
	if s.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout 必须非零（请求体读取上限），实际 %v", s.ReadTimeout)
	}
	if s.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout 必须非零（响应写入上限），实际 %v", s.WriteTimeout)
	}
	if s.WriteTimeout < agentd.RunCmdTimeout {
		t.Errorf("WriteTimeout %v 必须 >= run 路由执行上限 %v（否则长审阅命令被掐断）",
			s.WriteTimeout, agentd.RunCmdTimeout)
	}
	if s.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout 必须非零（keep-alive 空闲回收），实际 %v", s.IdleTimeout)
	}
}

// TestConfiguredMachinesFromConfigSecretRefOnly 验证 targets 投影为 ConfiguredMachine
// 时只带 secret_ref 引用（config.targets.<name>.token），不落 token 值。
func TestConfiguredMachinesFromConfigSecretRefOnly(t *testing.T) {
	cfg := &config.Config{Targets: map[string]config.Target{
		"devbox": {Addr: "http://10.0.0.5:7777", Token: "super-secret-token", DisplayName: "开发机"},
	}}
	configured, err := configuredMachinesFromConfig(cfg)
	if err != nil {
		t.Fatalf("configuredMachinesFromConfig: %v", err)
	}
	if len(configured) != 1 {
		t.Fatalf("configured = %d, want 1", len(configured))
	}
	cm := configured[0]
	if cm.ConfigKey != "devbox" || cm.Endpoint != "http://10.0.0.5:7777" || cm.DisplayName != "开发机" {
		t.Fatalf("configured = %+v", cm)
	}
	if cm.Kind != controlplane.MachineKindRemote {
		t.Fatalf("kind = %s, want remote", cm.Kind)
	}
	if cm.SecretRef != "config.targets.devbox.token" {
		t.Fatalf("secret_ref = %q, want config.targets.devbox.token", cm.SecretRef)
	}
}

func TestWireWorkspaceResourcesActivatesProductionServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("wired"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	local, err := st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.ResolveWorkspaceForPath(context.Background(), local.ID, root, root)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "token", Targets: map[string]config.Target{}}
	server := agentd.NewServer(cfg, st, logger)
	closeResources := wireWorkspaceResources(server, st, cfg, logger)
	defer closeResources()
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/workspaces/"+workspace.ID+"/entries", nil)
	req.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource route status = %d", resp.StatusCode)
	}
}
