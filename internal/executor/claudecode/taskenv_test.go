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
	settingsPath, mcpPath, prompt, err := WriteTaskEnv(dir, "T-1", "计划正文", "/tmp/x/perm.sock", "/usr/local/bin/handoff", "")
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
	// B149：allow 不再兜底放行裸 "Bash"，改为逐条前缀白名单。
	// 裸 "Bash" 匹配一切命令，等于 permgate 一条都看不到。
	if contains(settings.Permissions.Allow, "Bash") {
		t.Errorf("allow 不得含裸 Bash——它匹配一切命令，permgate 将完全失效（B149），实际 %v",
			settings.Permissions.Allow)
	}
	for _, want := range []string{"Bash(ls:*)", "Bash(git status:*)", "Bash(go test:*)"} {
		if !contains(settings.Permissions.Allow, want) {
			t.Errorf("allow 应放行项目工具链 %q，实际 %v", want, settings.Permissions.Allow)
		}
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

func TestWriteTaskEnvInjectsDiscipline(t *testing.T) {
	_, _, prompt, err := WriteTaskEnv(t.TempDir(), "T-1", "计划正文", "/tmp/perm.sock", "/bin/handoff", "# 执行纪律\n单上下文版内容")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "单上下文版内容") {
		t.Fatalf("纪律块未进入 claude prompt: %q", prompt)
	}
}

func TestWriteTaskEnvIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := WriteTaskEnv(dir, "T-1", "a", "/s", "/bin/handoff", ""); err != nil {
		t.Fatal(err)
	}
	// 重复调用覆盖而非报错：Start 失败重试时必须能安全重来
	if _, _, _, err := WriteTaskEnv(dir, "T-1", "b", "/s", "/bin/handoff", ""); err != nil {
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

// TestNoBareBashInAllow 锁死 B149：allowRules 里不得再出现裸 "Bash"。
//
// 裸 "Bash" 没有前缀参数、匹配一切 bash 命令，等于 claude 自己把每条命令都放行，
// handoff 的 permgate 一条都看不到——B128 真机验收第 4 条的现场就是这么来的
// （`echo x > /c/Users/.../outside.txt` 零权限请求、文件直接写成）。
func TestNoBareBashInAllow(t *testing.T) {
	for _, r := range allowRules {
		if r == "Bash" {
			t.Fatal(`allowRules 不得含裸 "Bash"——它匹配一切命令，permgate 将完全失效（B149）`)
		}
	}
}

// TestAllowListExcludesArbitraryExecution 锁死白名单的选条原则：
// 能任意执行代码、或能不经重定向就写到仓库外的命令，一条都不许进 allow。
//
// 这些形态**绕不过** B134 的落点判据——因为它们根本不用重定向：
// `python3 -c "open('/etc/x','w')"`、`sed -i ” s/x/y/ ~/.zshrc`、
// `find . -delete`、`env X=1 <任意命令>`、`npx <任意包>` 全是这一类。
// 它们必须落 ask 交 permgate 与审批链判，不能在这一层被静默放行。
func TestAllowListExcludesArbitraryExecution(t *testing.T) {
	banned := []string{
		"sh", "bash", "zsh", "eval", "xargs", // shell 与包装器
		"python", "python3", "node", "ruby", "perl", // 解释器
		"npx", "bunx", "npm install", "pip", // 包运行器/安装器
		"curl", "wget", // 外访（另在 askRules 收窄）
		"find", "sed", "awk", "env", "tee", "dd", // 各自能不经重定向写到仓库外
		"go run", "go generate", "go", // go 必须用二级前缀，不许整棵放行
	}
	for _, r := range allowRules {
		for _, b := range banned {
			if r == "Bash("+b+":*)" || r == "Bash("+b+")" {
				t.Fatalf("%q 不得出现在 allowRules——%s 能任意执行或不经重定向越界写（B149）", r, b)
			}
		}
	}
}

// TestAllowListCoversToolchain 白名单必须覆盖派发任务真正要跑的东西，
// 否则每条 go build / git commit 都要走一趟廉价模型，审批回路会被自己淹掉。
func TestAllowListCoversToolchain(t *testing.T) {
	must := []string{
		"Bash(ls:*)", "Bash(cat:*)", "Bash(grep:*)",
		"Bash(git status:*)", "Bash(git diff:*)", "Bash(git commit:*)",
		"Bash(go build:*)", "Bash(go test:*)", "Bash(go vet:*)", "Bash(gofmt:*)",
	}
	for _, m := range must {
		if !contains(allowRules, m) {
			t.Fatalf("%q 应在 allowRules 里——少它一条，这类高频命令每次都要惊动审批链", m)
		}
	}
}
