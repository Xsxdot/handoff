package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRunScriptContent(t *testing.T) {
	dir := t.TempDir()
	req := StartProcReq{
		RepoPath: "/repo", TaskID: "abcdef0123", TaskDir: dir,
		SessionID: "sess-1", Model: "opus",
		SettingsPath: filepath.Join(dir, "settings.json"),
		MCPPath:      filepath.Join(dir, "mcp.json"),
	}
	path, err := writeRunScript(dir, req)
	if err != nil {
		t.Fatalf("writeRunScript: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)

	for _, want := range []string{
		"exec 3<>",                    // fifo 两端永久持有
		"--input-format stream-json",  // 双向流
		"--output-format stream-json", // 事件流
		"--include-partial-messages",  // 文本增量（实况流式）
		"--permission-prompt-tool mcp__handoff__ask",
		"--setting-sources user,project",
		"--session-id sess-1",
		"--model opus",
		"handoff_exit", // 死亡哨兵
		"tee -a",       // out.jsonl 落盘
	} {
		if !strings.Contains(script, want) {
			t.Errorf("启动脚本缺少 %q:\n%s", want, script)
		}
	}
	// exec claude 会让 sh 被替换掉，哨兵永远写不出来
	if strings.Contains(script, "exec claude") {
		t.Error("claude 一行不得用 exec（否则死亡哨兵写不出来）")
	}
	// stderr 必须单独落盘，混进 stdout 会污染 jsonl 解析
	if !strings.Contains(script, "exec 2>>") {
		t.Error("缺少 stderr 重定向，out.jsonl 会被污染")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("启动脚本权限应 0600，实际 %v", fi.Mode().Perm())
	}
}

func TestWriteRunScriptOmitsEmptyModel(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRunScript(dir, StartProcReq{TaskDir: dir, SessionID: "s", Model: ""})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	// model 为空 = 用 claude 自身默认模型，不能写出裸 --model
	if strings.Contains(string(b), "--model") {
		t.Errorf("model 为空时不应出现 --model:\n%s", b)
	}
}

// 以下三个 env 用例与 opencode proc_test.go:143/175/195 同构：Env 不读也能编译通过，
// 症状是用户配的代理/密钥静默失效，只有钉子测试能拦住。
func TestRunScriptInjectsEnvFirst(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRunScript(dir, StartProcReq{
		TaskDir: dir, SessionID: "s",
		Env: []string{"HTTPS_PROXY=http://127.0.0.1:7890", "PATH=/usr/bin:/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)

	proxyIdx := strings.Index(s, "export HTTPS_PROXY='http://127.0.0.1:7890'")
	if proxyIdx < 0 {
		t.Fatalf("脚本缺少注入的 HTTPS_PROXY export 行:\n%s", s)
	}
	if !strings.Contains(s, "export PATH='/usr/bin:/bin'") {
		t.Errorf("脚本缺少注入的 PATH export 行:\n%s", s)
	}
	// 顺序是硬要求：export 必须在 claude 那一行之前，否则进程拿不到
	claudeIdx := strings.Index(s, "claude -p")
	if claudeIdx < 0 || proxyIdx > claudeIdx {
		t.Errorf("env 注入行应排在 claude 命令之前，实际 proxy=%d claude=%d", proxyIdx, claudeIdx)
	}
	// fifo 的 exec 3<> 同样要在 claude 之前，env 插入不得把它挤到后面去
	if fifoIdx := strings.Index(s, "exec 3<>"); fifoIdx < 0 || fifoIdx > claudeIdx {
		t.Errorf("exec 3<> 应仍在 claude 之前，实际 fifo=%d claude=%d", fifoIdx, claudeIdx)
	}
}

// 钉住「Go 侧已展开过一次，shell 不得再展开第二次」
func TestRunScriptQuotesEnvValues(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRunScript(dir, StartProcReq{
		TaskDir: dir, SessionID: "s",
		Env: []string{"LITERAL=$NOT_EXPANDED", "WITHSPACE=a b", "BROKEN_NO_EQUALS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, path))
	if !strings.Contains(s, "export LITERAL='$NOT_EXPANDED'") {
		t.Errorf("含 $ 的值必须被单引号包裹:\n%s", s)
	}
	if !strings.Contains(s, "export WITHSPACE='a b'") {
		t.Errorf("含空格的值必须被单引号包裹:\n%s", s)
	}
	// 不含 = 的畸形条目直接跳过，绝不能拼出语法错误的行把整个脚本毁掉
	if strings.Contains(s, "BROKEN_NO_EQUALS") {
		t.Errorf("形如 KEY=VALUE 之外的条目应跳过:\n%s", s)
	}
}

func TestRunScriptWithoutEnvIsUnchangedInShape(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRunScript(dir, StartProcReq{TaskDir: dir, SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, path))
	if strings.Contains(s, "export ") {
		t.Errorf("无 env 时不应出现任何 export 行:\n%s", s)
	}
	if !strings.Contains(s, "exec 3<>") || !strings.Contains(s, "handoff_exit") {
		t.Errorf("无 env 时脚本形状不应改变:\n%s", s)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProcExitedDetectsSentinel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, outFileName)

	if err := os.WriteFile(out, []byte(`{"type":"system","subtype":"init"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exited, _ := procExited(out); exited {
		t.Error("无哨兵时不应判死")
	}

	f, err := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"handoff_exit","code":3}` + "\n")
	f.Close()

	exited, code := procExited(out)
	if !exited || code != 3 {
		t.Errorf("哨兵应判死且带退出码，得到 exited=%v code=%d", exited, code)
	}
}

func TestWriteInputRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := &Proc{TaskDir: dir}
	if err := p.ensureFIFO(); err != nil {
		t.Skipf("mkfifo 不可用（平台限制）: %v", err)
	}
	// 读端常开，模拟启动脚本的 exec 3<>
	rd, err := os.OpenFile(filepath.Join(dir, fifoFileName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	if err := p.WriteInput("你好"); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	buf := make([]byte, 256)
	n, err := rd.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	line := string(buf[:n])
	if !strings.Contains(line, `"type":"user"`) || !strings.Contains(line, "你好") {
		t.Errorf("fifo 内容不符: %q", line)
	}
}
