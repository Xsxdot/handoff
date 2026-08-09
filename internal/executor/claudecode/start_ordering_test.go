package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

// TestStartWritesPromptBeforeWaitingReady 钉住 Start 的关键时序：**prompt 必须先于
// 等 init**。
//
// 为什么必须有这条测试（2026-08-09 真机 e2e 实测发现的错误假设）：`claude -p
// --input-format stream-json` **不会在启动时主动吐 system/init**——它要先收到
// 第一条输入消息才吐。旧实现「先等 init 再投 prompt」与这个契约互为死锁，任务
// 永远卡在就绪超时。单测的合成 out.jsonl fixture（testdata/turn_success.jsonl）
// 本身就假设 init 会自己来，抓不到这条；本测试的假执行者**模拟 claude 的真实
// 契约**：只有在检测到输入被写进 fifo 之后，才往 out.jsonl 追加 system/init。
// 旧代码在它下面必然超时失败，新代码通过——这是本修复的唯一护栏。
func TestStartWritesPromptBeforeWaitingReady(t *testing.T) {
	// 假 claude 进 PATH：run_claude.sh 以裸名 `claude` 调用，PATH 里先放假执行者
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "claude")
	fakeScript := `#!/bin/sh
# 假 claude：模拟真实契约——收到首条输入前不吐任何东西；读到输入后吐 system/init，
# 然后像真实 claude 一样保持进程存活（cat 持续消费 stdin，直到 fifo 写端关闭）
while IFS= read -r line; do
  if [ -n "$line" ]; then
    printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-fake"}'
    cat > /dev/null
    exit 0
  fi
done
`
	if err := os.WriteFile(fake, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	taskDir := shortTestDir(t)
	repoPath := shortTestDir(t)

	// 桩 tmux 启动：直接 `sh <script>` 后台执行，绕开真实 tmux server。
	// 等待 fifo 出现读者（脚本 exec 3<> 完成）再返回，否则 Start 的 WriteInput
	// 会因 O_NONBLOCK 无读者而 ENXIO 失败——这正是 tmux 启动的固有延迟
	var launched *exec.Cmd
	oldHas := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	oldLaunch := tmuxLaunch
	tmuxLaunch = func(session, repo, script string) error {
		cmd := exec.Command("/bin/sh", script)
		cmd.Dir = repo
		if err := cmd.Start(); err != nil {
			return err
		}
		launched = cmd
		fifo := filepath.Join(taskDir, fifoFileName)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
			if err == nil {
				f.Close()
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		return errors.New("假执行者未在 5s 内就绪（exec 3<> 未完成）")
	}
	t.Cleanup(func() {
		tmuxLaunch = oldLaunch
		tmuxHasSession = oldHas
		if launched != nil && launched.Process != nil {
			launched.Process.Kill()
			launched.Wait()
		}
	})

	a := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := a.Start(ctx, executor.StartReq{
		Task:        proto.Task{ID: "T-order", RepoPath: repoPath},
		PlanContent: "测试计划",
		TaskDir:     taskDir,
	})
	if err != nil {
		t.Fatalf("Start 应成功（prompt 先投、init 随后到达）：%v", err)
	}

	// 假执行者的 init 事件应已产出（session_id 与假 claude 写的一致）
	r := a.lookup("T-order")
	if r == nil {
		t.Fatal("Start 成功后运行态缺失")
	}
	select {
	case ev := <-r.evCh:
		if ev.Type != "progress" || ev.SessionID != "sess-fake" {
			t.Fatalf("init 事件应先到且带假执行者的 session_id，实际 %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到 init progress 事件")
	}

	// 收摊：Stop 幂等，运行态可能已被 streamLoop 随哨兵终结而注销
	_ = a.Stop("T-order")
}

// shortTestDir 建一个短路径目录（os.TempDir 下用短随机名），供 unix socket /
// 命名管道使用——macOS 的 sockaddr 路径上限 104 字节，t.TempDir() 因嵌入长测试名
// 会超限（bind/mkfifo 报 invalid argument）；随测试结束清理。
func shortTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("hc-%d", time.Now().UnixNano()%1e9))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
