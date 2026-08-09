// probe_live_test.go —— 权限策略优先级 live 探针（默认跳过，需真实 claude + 网络）。
//
// 职责：
//   - 验证任务级 settings 的 ask 是否压过同文件内的 allow
//   - 验证任务级 settings 的 ask 是否压过「用户级」settings 的 allow
//
// 边界：
//   - 只做探针不做断言性能/成本；结论人工抄进 spec §5.4 与 README
//   - 默认 t.Skip：CI 与常规 go test ./... 不得因为缺 claude/网络而红
package claudecode

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProbePermissionPrecedence 探针：ask 能否压过 allow（同文件内 / 跨来源）。
//
// 运行方式：HANDOFF_LIVE_CLAUDE=1 go test ./internal/executor/claudecode/ -run Probe -v
//
// 注意：
//   - 会真实调用 claude（haiku）产生费用，故默认跳过
//   - 用临时 HOME 构造可控的「用户级 settings」，不依赖执行者本机的个人配置
func TestProbePermissionPrecedence(t *testing.T) {
	if os.Getenv("HANDOFF_LIVE_CLAUDE") != "1" {
		t.Skip("live 探针：设 HANDOFF_LIVE_CLAUDE=1 手动运行。2026-08-09 在 devbox 实测：临时 HOME 与真实 HOME 均报 Not logged in（apiKeySource=none），探针待人工 `claude /login` 后重跑")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude 未安装")
	}

	cases := []struct {
		name         string
		userAllow    []string // 写进临时 HOME 的用户级 settings.allow
		taskAllow    []string // 写进任务级 settings.allow
		taskAsk      []string // 写进任务级 settings.ask
		settingSrc   string
		wantAskFired bool // 期望：裁决工具被调用（= ask 生效）
	}{
		{
			name:         "同文件内 ask 压过 allow",
			taskAllow:    []string{"Bash"},
			taskAsk:      []string{"Bash(rm:*)"},
			settingSrc:   "",
			wantAskFired: true,
		},
		{
			name:         "任务级 ask 压过用户级 allow",
			userAllow:    []string{"Bash"},
			taskAsk:      []string{"Bash(rm:*)"},
			settingSrc:   "user",
			wantAskFired: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeSettings(t, filepath.Join(home, ".claude", "settings.json"), c.userAllow, nil)
			taskSettings := filepath.Join(dir, "settings.json")
			writeSettings(t, taskSettings, c.taskAllow, c.taskAsk)

			markerLog := filepath.Join(dir, "asked.log")
			mcpPath := writeProbeMCP(t, dir, markerLog)

			args := []string{
				"-p", "--input-format", "stream-json", "--output-format", "stream-json",
				"--verbose", "--model", "haiku",
				"--setting-sources", c.settingSrc,
				"--settings", taskSettings,
				"--mcp-config", mcpPath, "--strict-mcp-config",
				"--permission-prompt-tool", "mcp__probe__ask",
			}
			cmd := exec.Command("claude", args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "HOME="+home)
			cmd.Stdin = strings.NewReader(
				`{"type":"user","message":{"role":"user","content":` +
					`"Use the Bash tool to run exactly: rm -rf /tmp/handoff-probe-victim"}}` + "\n")
			out, err := cmd.Output()
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					t.Fatalf("claude 运行失败: %v\nstderr: %s\nstdout: %s", err, ee.Stderr, out)
				}
				t.Fatalf("claude 运行失败: %v\nstdout: %s", err, out)
			}

			_, statErr := os.Stat(markerLog)
			askFired := statErr == nil
			t.Logf("askFired=%v（期望 %v）；claude 输出 %d 字节", askFired, c.wantAskFired, len(out))
			if askFired != c.wantAskFired {
				t.Errorf("优先级与预期不符：askFired=%v want=%v —— 按 spec §5.4 的处置分支改写 settings 形态",
					askFired, c.wantAskFired)
			}
		})
	}
}

// writeSettings 写一个只含 permissions 段的 settings.json（allow/ask 可空）。
func writeSettings(t *testing.T, path string, allow, ask []string) {
	t.Helper()
	type perms struct {
		Allow []string `json:"allow,omitempty"`
		Ask   []string `json:"ask,omitempty"`
	}
	b, err := json.Marshal(struct {
		Permissions perms `json:"permissions"`
	}{perms{Allow: allow, Ask: ask}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeProbeMCP 生成探针用的极简 stdio MCP server（命中即 touch markerLog 并放行）。
func writeProbeMCP(t *testing.T, dir, markerLog string) string {
	t.Helper()
	script := `#!/usr/bin/env python3
import sys, json
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
TOOL = {"name":"ask","description":"probe","inputSchema":{"type":"object","properties":{},"required":[]}}
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    req = json.loads(line)
    m = req.get("method")
    if m == "initialize":
        send({"jsonrpc":"2.0","id":req["id"],"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"probe","version":"0"}}})
    elif m == "tools/list":
        send({"jsonrpc":"2.0","id":req["id"],"result":{"tools":[TOOL]}})
    elif m == "tools/call":
        open(` + "`" + `MARKER` + "`" + `, "a").write("asked\n")
        send({"jsonrpc":"2.0","id":req["id"],"result":{"content":[{"type":"text","text":json.dumps({"behavior":"allow"})}]}})
    elif "id" in req:
        send({"jsonrpc":"2.0","id":req["id"],"result":{}})
`
	script = strings.Replace(script, "`MARKER`", `"`+markerLog+`"`, 1)
	scriptPath := filepath.Join(dir, "probe_mcp.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"mcpServers": map[string]any{
		"probe": map[string]any{"command": "python3", "args": []string{scriptPath}},
	}}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}
