// TargetEndpoint 的本机/远程端点换算测试：覆盖无 --target 时用本地配置 token
// 认证（服务端无条件 Bearer，无 token 的本机调用必然 401）、--agentd 显式优先、
// token 缺失报错、--target 的 targets 表解析与未定义报错。
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestConfig 写一份测试配置文件并返回路径。
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	return p
}

// resetFlags 恢复包级 flag 状态（agentdURL/targetName/configPath 与 --agentd 的
// Changed 标记），保证用例之间互不污染。
func resetFlags(t *testing.T) {
	t.Helper()
	oldAgentd, oldTarget, oldConfig := agentdURL, targetName, configPath
	t.Cleanup(func() {
		agentdURL, targetName, configPath = oldAgentd, oldTarget, oldConfig
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	})
}

// TestTargetEndpointLocalAuth 覆盖本机模式（无 --target）：
// token 必须来自本地配置，地址取配置 Listen（未显式 --agentd）或显式 --agentd。
func TestTargetEndpointLocalAuth(t *testing.T) {
	cfgPath := writeTestConfig(t, `listen: "127.0.0.1:9999"
token: "local-tok"
targets:
  devbox:
    addr: "10.0.0.1:7777"
    token: "remote-tok"
`)
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	agentdURL = "http://127.0.0.1:7777"

	t.Run("无 target 用本地配置 token 与 listen", func(t *testing.T) {
		rootCmd.PersistentFlags().Lookup("agentd").Changed = false
		addr, token, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://127.0.0.1:9999" {
			t.Fatalf("addr=%q, want http://127.0.0.1:9999（配置 Listen 补 scheme）", addr)
		}
		if token != "local-tok" {
			t.Fatalf("token=%q, want local-tok（本机认证必须带配置 token）", token)
		}
	})

	t.Run("显式 --agentd 优先于配置 listen", func(t *testing.T) {
		agentdURL = "http://192.168.1.10:7777"
		if err := rootCmd.PersistentFlags().Set("agentd", agentdURL); err != nil {
			t.Fatalf("Set agentd flag: %v", err)
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://192.168.1.10:7777" {
			t.Fatalf("addr=%q, want 显式 --agentd 优先", addr)
		}
		if token != "local-tok" {
			t.Fatalf("token=%q, want local-tok", token)
		}
	})

	t.Run("配置 token 为空返回错误", func(t *testing.T) {
		emptyCfg := writeTestConfig(t, "listen: \"127.0.0.1:7777\"\ntoken: \"\"\n")
		configPath = emptyCfg
		_, _, err := TargetEndpoint()
		if err == nil || !strings.Contains(err.Error(), "token") {
			t.Fatalf("token 为空应报错, got %v", err)
		}
	})
}

// TestTargetEndpointRemote 覆盖远程模式（--target）：
// 从配置 Targets 表换算 addr/token；未定义的 target 报错。
func TestTargetEndpointRemote(t *testing.T) {
	cfgPath := writeTestConfig(t, `listen: "127.0.0.1:9999"
token: "local-tok"
targets:
  devbox:
    addr: "10.0.0.1:7777"
    token: "remote-tok"
`)
	resetFlags(t)
	configPath = cfgPath

	t.Run("target 已定义换算成功", func(t *testing.T) {
		targetName = "devbox"
		addr, token, err := TargetEndpoint()
		if err != nil {
			t.Fatalf("TargetEndpoint: %v", err)
		}
		if addr != "http://10.0.0.1:7777" {
			t.Fatalf("addr=%q, want http://10.0.0.1:7777", addr)
		}
		if token != "remote-tok" {
			t.Fatalf("token=%q, want remote-tok", token)
		}
	})

	t.Run("target 未定义报错", func(t *testing.T) {
		targetName = "ghost"
		_, _, err := TargetEndpoint()
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("未定义 target 应报错, got %v", err)
		}
	})
}
