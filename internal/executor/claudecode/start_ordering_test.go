package claudecode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/shellq"
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
	installFakeClaude(t)
	taskDir := shortTestDir(t)
	repoPath := shortTestDir(t)

	// 桩 tmux 启动：直接 `sh <script>` 后台执行，绕开真实 tmux server。
	// 桩只做「把脚本跑起来」这一件事——**不再替生产代码等 fifo 读端**：
	// 等读端是 StartProc 的职责（waitFIFOReader），桩越俎代庖只会把竞态藏进测试
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
		return nil
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

// TestStartWaitsForFIFOReader 钉住 StartProc 对「tmux 返回早于脚本执行」竞态的兜底。
//
// 契约（2026-08-09 真机 e2e 次生缺陷）：tmux new-session -d 一创建会话就返回，
// **不等会话内脚本执行到 exec 3<> in.fifo 那一行**；而 WriteInput 以
// O_WRONLY|O_NONBLOCK 打开 fifo，POSIX 规定读端未就绪时 open 直接失败
// （errno ENXIO，macOS 文案 "device not configured"）。StartProc 必须先确认读端
// 在位再返回，否则 adapter 随即投 prompt 必然 ENXIO 失败。本测试的桩**故意延迟
// 启动假 claude**（launchDelay），模拟 tmux 返回远早于脚本执行，断言 Start 仍
// 成功——等读端是生产代码自己的事（waitFIFOReader）。
//
// 自检：把 waitFIFOReader 的等待逻辑临时去掉，本测试必然失败（fifo 无读端、
// WriteInput 打开 ENXIO）——红，说明钉子有效。
func TestStartWaitsForFIFOReader(t *testing.T) {
	installFakeClaude(t)
	taskDir := shortTestDir(t)
	repoPath := shortTestDir(t)

	const launchDelay = 300 * time.Millisecond
	var launched *exec.Cmd
	oldHas := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	oldLaunch := tmuxLaunch
	tmuxLaunch = func(session, repo, script string) error {
		// 模拟 tmux new-session -d 立即返回、会话内脚本晚些才执行到 exec 3<>：
		// 外层 sh 先睡 launchDelay 再用 exec 替换成真正的启动脚本，读端因此晚到。
		// 注意延迟必须放在「启动脚本之后」——若桩在拉起前干等，StartProc 返回后
		// startRenderTailWindow 的 tmux 调用会白送几十毫秒让脚本跑到 exec 3<>，
		// 测试就测不到竞态了（这正是初版桩的坑）
		wrapped := "sleep " + fmt.Sprint(launchDelay.Seconds()) + "; exec /bin/sh " + shellq.Quote(script)
		cmd := exec.Command("/bin/sh", "-c", wrapped)
		cmd.Dir = repo
		if err := cmd.Start(); err != nil {
			return err
		}
		launched = cmd
		return nil
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
		Task:        proto.Task{ID: "T-race", RepoPath: repoPath},
		PlanContent: "测试计划",
		TaskDir:     taskDir,
	})
	if err != nil {
		t.Fatalf("读端晚到（延迟 %s）Start 仍应成功：%v", launchDelay, err)
	}

	// 收摊：与上一测试同款，Stop 幂等
	_ = a.Stop("T-race")
}

// installFakeClaude 把假 claude 放进 PATH 首位：run_claude.sh 以裸名 `claude`
// 调用，PATH 里先放假执行者。假执行者模拟真实契约——收到首条输入前不吐任何
// 东西；读到输入后吐 system/init，然后像真实 claude 一样保持进程存活（cat 持续
// 消费 stdin，直到 fifo 写端关闭）。
func installFakeClaude(t *testing.T) {
	t.Helper()
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
