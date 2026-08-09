package claudecode

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
)

// 凭据缺失 → 判死不存活（manager 按不存活走 failed 恢复路径）。
func TestResumeMissingProcInfo(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	out, err := a.Resume(executor.ResumeReq{TaskID: "T-1", TaskDir: dir, RepoPath: "/repo", SessionID: "sess-1"})
	if out.Alive {
		t.Fatalf("凭据缺失应判死不存活，实际 alive=%v", out.Alive)
	}
	// err 由 readProcInfo 产生，manager 对 err!=nil 与 Alive=false 同路处理（都转 failed）；
	// 这里只断言结局：不存活
	if err == nil {
		t.Log("凭据缺失返回 (不存活, nil) 也可（manager 同路）")
	}
}

// 关键路径：tmux 会话还在（窗口 1 的 tail 撑着）但 claude 已退 → 必须判死。
// 这是本 adapter 最容易误判为存活的场景，opencode 靠 HTTP 探活兜住，我们靠哨兵。
func TestResumeSessionAliveButProcessExited(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, outFileName)
	if err := os.WriteFile(out, []byte(`{"type":"handoff_exit","code":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 桩掉 tmux 探活让它返回 true（会话确实还活着，是 tail 吊着）
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	defer func() { tmuxHasSession = old }()

	out2, err := a.Resume(executor.ResumeReq{TaskID: "T-1", TaskDir: dir, RepoPath: "/repo", SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if out2.Alive {
		t.Fatal("claude 已退（哨兵在）但 tmux 会话还在，必须判死")
	}
}

// 进程存活 → 从 offset 续读，已消费回合不重放。
func TestResumeContinuesFromOffset(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	// 已消费的内容（offset 之前）：不得重放
	consumed := `{"type":"system","subtype":"init","session_id":"sess-1"}` + "\n"
	// 待续读的内容：一个 finish 回合
	rest := `{"type":"result","subtype":"success","result":"{\"branch\":\"hb\",\"commit\":\"c0ffee\",\"summary\":\"x\"}"}` + "\n"
	out := filepath.Join(dir, outFileName)
	if err := os.WriteFile(out, []byte(consumed+rest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeProcInfo(dir, &procInfo{
		TmuxSession: "handoff-abcdef01", SessionID: "sess-1", Offset: int64(len(consumed)),
	}); err != nil {
		t.Fatal(err)
	}
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	defer func() { tmuxHasSession = old }()

	out2, err := a.Resume(executor.ResumeReq{TaskID: "T-1", TaskDir: dir, RepoPath: "/repo", SessionID: "sess-1"})
	if err != nil || !out2.Alive {
		t.Fatalf("Resume 应判活并续读: alive=%v err=%v", out2.Alive, err)
	}
	r := a.lookup("T-1")
	if r == nil {
		t.Fatal("恢复后运行态缺失")
	}
	select {
	case ev := <-r.evCh:
		if ev.Type != "result" || ev.Result == nil || !ev.Result.OK || ev.Result.Branch != "hb" {
			t.Fatalf("续读应产出 finish result，实际 %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("续读 2s 内未产出事件")
	}
	// offset 之前的行不得重放：不应再有 init 之类的旧事件
	select {
	case ev := <-r.evCh:
		t.Fatalf("offset 前的行被重放了，得到 %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
	r.runCancel()
}

// TestWriteRunScriptUsesResumeFlag 冷恢复的启动脚本必须用 --resume 而不是
// --session-id：后者是「建一个这个 id 的新会话」，语义完全相反，写错的表现是
// 「日志说恢复成功、模型却什么都不记得」——最难查的一类 bug。
func TestWriteRunScriptUsesResumeFlag(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRunScript(dir, StartProcReq{TaskID: "t1", TaskDir: dir,
		SessionID: "sess-1", SettingsPath: "/s.json", MCPPath: "/m.json", Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "--resume sess-1") {
		t.Fatalf("冷恢复脚本应含 --resume，实际:\n%s", b)
	}
	if strings.Contains(string(b), "--session-id") {
		t.Fatalf("冷恢复脚本不应含 --session-id（语义相反）")
	}
}

// TestResumeColdRotatesOutJSONL 冷恢复后是全新的输出流，旧 offset 无意义——
// 不轮转的话 tailer 从旧 offset 续读新文件，会把新会话的开头当成旧内容跳过。
func TestResumeColdRotatesOutJSONL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, outFileName), []byte(`{"type":"system","subtype":"init","session_id":"old"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rotateOutJSONL(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.1.jsonl")); err != nil {
		t.Fatalf("旧 out.jsonl 应轮转为 out.1.jsonl，实际: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, outFileName)); !os.IsNotExist(err) {
		t.Fatalf("轮转后新 out.jsonl 尚不应存在（由冷恢复启动脚本创建），实际: %v", err)
	}
}

// TestResumeColdStartsResumeProcess 冷恢复必须用 --resume 起新进程（StartProcReq
// 带 Resume=true），且轮转 out.jsonl 后 offset 归零。
func TestResumeColdStartsResumeProcess(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	repo := t.TempDir() // 工作区须真实存在（§6 约束 5：冷恢复不重建 worktree）
	// 哨兵在 → 判死；claude.json 提供 tmux 会话名
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, outFileName), []byte(`{"type":"handoff_exit","code":0}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := startProc
	startProc = func(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) {
		if !req.Resume {
			t.Fatal("冷恢复必须 Resume=true（--resume 语义）")
		}
		if req.SessionID != "sess-1" || req.Model != "sonnet" || req.RepoPath != repo {
			t.Fatalf("冷恢复入参不对: %+v", req)
		}
		return &Proc{TmuxSession: "handoff-abcdef01", TaskDir: dir, SessionID: "sess-1"}, nil
	}
	defer func() { startProc = old }()
	// 桩掉 tmux 探活（会话已不在，判死）
	oldHas := tmuxHasSession
	tmuxHasSession = func(string) bool { return false }
	defer func() { tmuxHasSession = oldHas }()
	// 桩掉 perm.sock（长临时路径超 unix sun_path 上限）
	oldPerm := newPermServerFn
	newPermServerFn = func(sockPath string, log *slog.Logger, onAsk func(permAsk)) (*permServer, error) {
		return &permServer{}, nil
	}
	defer func() { newPermServerFn = oldPerm }()

	out, err := a.Resume(executor.ResumeReq{TaskID: "T-1", TaskDir: dir,
		RepoPath: repo, SessionID: "sess-1", Model: "sonnet", Cold: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Alive {
		t.Fatalf("冷恢复应成功: %+v", out)
	}
	if out.Mode != executor.ResumeModeCold {
		t.Fatalf("冷恢复应 Mode=cold，实际 %s", out.Mode)
	}
	r := a.lookup("T-1")
	if r == nil {
		t.Fatal("恢复后运行态缺失")
	}
	if r.startOffset != 0 {
		t.Fatalf("冷恢复后 offset 应归零，实际 %d", r.startOffset)
	}
	r.runCancel()
}

// TestResumeColdMutualExclusion 并发两次冷恢复只允许一次真的去拉进程——
// 两个 claude 进程抢同一个会话是数据损坏级别的后果（spec §6 约束 1）。
func TestResumeColdMutualExclusion(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	repo := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, outFileName), []byte(`{"type":"handoff_exit","code":0}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var starts int32
	old := startProc
	startProc = func(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) {
		atomic.AddInt32(&starts, 1)
		time.Sleep(50 * time.Millisecond) // 拉长窗口，让第二个必然撞进来
		return &Proc{TmuxSession: "handoff-abcdef01", TaskDir: dir, SessionID: "sess-1"}, nil
	}
	defer func() { startProc = old }()
	oldHas := tmuxHasSession
	tmuxHasSession = func(string) bool { return false }
	defer func() { tmuxHasSession = oldHas }()
	oldPerm := newPermServerFn
	newPermServerFn = func(sockPath string, log *slog.Logger, onAsk func(permAsk)) (*permServer, error) {
		return &permServer{}, nil
	}
	defer func() { newPermServerFn = oldPerm }()

	req := executor.ResumeReq{TaskID: "T-1", TaskDir: dir, RepoPath: repo,
		SessionID: "sess-1", Model: "sonnet", Cold: true}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.Resume(req) }()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&starts); n != 1 {
		t.Fatalf("并发冷恢复应只拉起一次进程，实际 %d 次", n)
	}
}
