//go:build windows

package prochost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWindowsInputChannelNeverEOFs 是本 task 的核心判据，与 unix 侧
// TestOpenInputChannelNeverEOFs 同契约不同实现：投递两次，第二次必须也读得到。
//
// 为什么必须两次：命名管道客户端断开会让服务端侧 broken pipe，若实现把服务端
// 句柄直接当子进程 stdin，第一次投递能过、第二次就死——单次投递测不出来。
func TestWindowsInputChannelNeverEOFs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	r, cleanup, err := openInputChannel(path)
	if err != nil {
		t.Fatalf("打开输入通道失败: %v", err)
	}
	defer cleanup()

	if _, err := waitInputReader(path, 5*time.Second); err != nil {
		t.Fatalf("等待读端就绪失败: %v", err)
	}

	for i, want := range []string{"one\n", "two\n"} {
		if err := WriteInputChannel(path, []byte(want)); err != nil {
			t.Fatalf("第 %d 次投递失败: %v", i+1, err)
		}
		buf := make([]byte, 32)
		n, rerr := r.Read(buf)
		if rerr != nil {
			t.Fatalf("第 %d 次读失败（很可能是子进程 stdin 被 EOF 掉了）: %v", i+1, rerr)
		}
		if got := string(buf[:n]); got != want {
			t.Fatalf("第 %d 次读到 %q，想要 %q", i+1, got, want)
		}
	}
}

// TestWindowsWaitInputReaderTimesOutWithoutServer 钉住「服务端没建起来必须超时」。
// createInputChannel 在 Windows 上是 no-op，等待责任全压在这里——它若误报就绪，
// StartProc 会带着一个没有 stdin 的执行者继续往下走。
func TestWindowsWaitInputReaderTimesOutWithoutServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	_, err := waitInputReader(path, 300*time.Millisecond)
	if err == nil {
		t.Fatalf("服务端不存在时竟然报告读端已就绪")
	}
}

// TestWindowsWriteFailsWithoutServer 钉住承重语义：服务端不在时投递必须失败，
// 这是调用方判「执行者已不在」的依据。
func TestWindowsWriteFailsWithoutServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	err := WriteInputChannel(path, []byte("x\n"))
	if err == nil {
		t.Fatalf("服务端不存在时投递竟然成功了")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("错误里没带通道路径，排障定位不到: %v", err)
	}
}

// TestWindowsFirstInstanceRejectsSquatting 钉住抢占防护：同名管道已存在时，
// 第二次创建必须失败。这是安全判据不是健壮性判据——管道名被抢占意味着
// 别人能拿到执行者的 stdin，可以直接给模型下指令。
func TestWindowsFirstInstanceRejectsSquatting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	_, cleanup, err := openInputChannel(path)
	if err != nil {
		t.Fatalf("第一次创建失败: %v", err)
	}
	defer cleanup()

	_, cleanup2, err := openInputChannel(path)
	if err == nil {
		cleanup2()
		t.Fatalf("同名管道竟然允许第二次创建——抢占防护失效")
	}
}

// TestWindowsCreateInputChannelIsNoop 钉住 no-op 的契约：它成功不代表通道可用。
// 若哪天有人给它加了「创建服务端」的实现，这条会失败并提醒他：服务端归属
// 必须在 shim，agentd 侧建服务端会让 agentd 重启杀死执行者 stdin。
func TestWindowsCreateInputChannelIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("no-op 竟然失败: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("createInputChannel 在 Windows 上不该创建任何文件")
	}
	if _, err := waitInputReader(path, 200*time.Millisecond); err == nil {
		t.Fatalf("createInputChannel 之后读端不该就绪——服务端由 shim 建")
	}
}

// TestWindowsWriteRightAfterReadinessProbe 钉住 B128 真机验收抓到的那个缺陷：
// waitInputReader 的就绪探测本身是一次 CreateFile+Close，会消耗中继的一个
// 受理周期；紧接着的投递若不处理 ERROR_PIPE_BUSY，就会在中继绕回
// ConnectNamedPipe 之前失败，并被错误归因成「读端可能已不在」。
//
// 真机报文：投递首回合 prompt: 连接管道 …: All pipe instances are busy.
//
// 这个用例刻意**紧贴着探测之后立刻投递**，不加任何等待——加了等待就等于
// 把要复现的窗口睡过去，测了个寂寞。
func TestWindowsWriteRightAfterReadinessProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	r, cleanup, err := openInputChannel(path)
	if err != nil {
		t.Fatalf("打开输入通道失败: %v", err)
	}
	defer cleanup()

	if _, err := waitInputReader(path, 5*time.Second); err != nil {
		t.Fatalf("等待读端就绪失败: %v", err)
	}
	// 探测刚消耗掉一个受理周期，这里立刻投递
	if err := WriteInputChannel(path, []byte("right-after-probe\n")); err != nil {
		t.Fatalf("紧接就绪探测之后投递失败（ERROR_PIPE_BUSY 未被正确重试）: %v", err)
	}
	buf := make([]byte, 64)
	n, rerr := r.Read(buf)
	if rerr != nil {
		t.Fatalf("读失败: %v", rerr)
	}
	if got := string(buf[:n]); got != "right-after-probe\n" {
		t.Fatalf("读到 %q，想要 %q", got, "right-after-probe\n")
	}
}
