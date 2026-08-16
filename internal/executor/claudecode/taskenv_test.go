package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTaskEnvGeneratesSettingsAndMCP(t *testing.T) {
	dir := t.TempDir()
	settingsPath, mcpPath, prompt, err := WriteTaskEnv(dir, "T-1", "计划正文", "/tmp/x/perm.sock", "/usr/local/bin/handoff")
	if err != nil {
		t.Fatalf("WriteTaskEnv: %v", err)
	}

	// prompt 必须带上计划正文（回合纪律部分由 turn 包保证，此处只校验透传）
	if !strings.Contains(prompt, "计划正文") {
		t.Errorf("prompt 未包含计划正文: %q", prompt)
	}

	// settings.json：ask 覆盖危险模式，deny 必须为空（why 见 spec §5.4）
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Ask   []string `json:"ask"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	readJSON(t, settingsPath, &settings)
	if len(settings.Permissions.Deny) != 0 {
		t.Errorf("deny 必须留空（黑名单归 manager 升级协调者），实际 %v", settings.Permissions.Deny)
	}
	for _, want := range []string{"Write", "Edit", "Bash(rm:*)", "Bash(sudo:*)", "Bash(git push:*)", "Bash(curl:*)", "Bash(wget:*)"} {
		if !contains(settings.Permissions.Ask, want) {
			t.Errorf("ask 缺少危险模式 %q（少一条就是静默放行）", want)
		}
	}
	if !contains(settings.Permissions.Allow, "Bash") {
		t.Errorf("allow 应兜底放行 Bash，实际 %v", settings.Permissions.Allow)
	}
	for _, want := range []string{"Write", "Edit"} {
		if contains(settings.Permissions.Allow, want) {
			t.Errorf("allow 不得再放行 %q——那等于写仓库外路径不经任何人（B27）", want)
		}
	}

	// mcp.json：裁决工具指向 handoff 二进制 + 本任务 socket
	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	readJSON(t, mcpPath, &mcp)
	srv, ok := mcp.MCPServers["handoff"]
	if !ok {
		t.Fatalf("mcp.json 缺少 handoff server: %+v", mcp)
	}
	if srv.Command != "/usr/local/bin/handoff" {
		t.Errorf("command 应为 handoff 二进制绝对路径，实际 %q", srv.Command)
	}
	if !contains(srv.Args, "/tmp/x/perm.sock") {
		t.Errorf("args 应携带任务 socket 路径，实际 %v", srv.Args)
	}

	// 含策略的文件必须 0600
	for _, p := range []string{settingsPath, mcpPath} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s 权限应为 0600，实际 %v", filepath.Base(p), fi.Mode().Perm())
		}
	}
}

func TestWriteTaskEnvIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := WriteTaskEnv(dir, "T-1", "a", "/s", "/bin/handoff"); err != nil {
		t.Fatal(err)
	}
	// 重复调用覆盖而非报错：Start 失败重试时必须能安全重来
	if _, _, _, err := WriteTaskEnv(dir, "T-1", "b", "/s", "/bin/handoff"); err != nil {
		t.Fatalf("重复调用应幂等: %v", err)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestWriteEditNotInAllow 锁死 B27：写文件类工具不得回到 allow 表。
func TestWriteEditNotInAllow(t *testing.T) {
	for _, r := range allowRules {
		if r == "Write" || r == "Edit" {
			t.Fatalf("%s 不得出现在 allowRules——那等于写仓库外路径不经任何人（B27）", r)
		}
	}
}
