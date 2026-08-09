package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 凭据缺失 → 判死不存活（manager 按不存活走 failed 恢复路径）。
func TestResumeMissingProcInfo(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	alive, err := a.Resume("T-1", dir, "/repo", "sess-1")
	if alive {
		t.Fatalf("凭据缺失应判死不存活，实际 alive=%v", alive)
	}
	// err 由 readProcInfo 产生，manager 对 err!=nil 与 alive=false 同路处理（都转 failed）；
	// 这里只断言结局：不存活
	if err == nil {
		t.Log("凭据缺失返回 (false, nil) 也可（manager 同路）")
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

	alive, err := a.Resume("T-1", dir, "/repo", "sess-1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if alive {
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

	alive, err := a.Resume("T-1", dir, "/repo", "sess-1")
	if err != nil || !alive {
		t.Fatalf("Resume 应判活并续读: alive=%v err=%v", alive, err)
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
