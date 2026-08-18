//go:build unix

package prochost

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWriteInputChannelDeliversToReader 钉住投递本身：有读端时字节必须到达。
func TestWriteInputChannelDeliversToReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	// O_RDWR 持有读端，模拟 shim 的行为（只读打开会在写端关闭时 EOF）
	r, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("持有读端失败: %v", err)
	}
	defer r.Close()

	if err := WriteInputChannel(path, []byte("hello\n")); err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	buf := make([]byte, 16)
	type readResult struct {
		n   int
		err error
	}
	readCh := make(chan readResult, 1)
	go func() {
		n, err := r.Read(buf)
		readCh <- readResult{n: n, err: err}
	}()
	var result readResult
	select {
	case result = <-readCh:
	case <-time.After(2 * time.Second):
		t.Fatal("读超时")
	}
	if result.err != nil {
		t.Fatalf("读失败: %v", result.err)
	}
	if got := string(buf[:result.n]); got != "hello\n" {
		t.Fatalf("读到 %q，想要 %q", got, "hello\n")
	}
}

// TestWriteInputChannelFailsWithoutReader 钉住承重语义：读端不在时必须立刻失败。
// 这是调用方判定「进程已不在」的唯一依据——若这里改成阻塞或静默成功，
// ErrTaskNotRunning 就再也不会被触发，任务会挂在一个死执行者上等到超时。
func TestWriteInputChannelFailsWithoutReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	err := WriteInputChannel(path, []byte("hello\n"))
	if err == nil {
		t.Fatalf("无读端时投递竟然成功了")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("错误里没带通道路径，排障时定位不到: %v", err)
	}
	_ = syscall.ENXIO // 文档性引用：unix 上根因是 ENXIO
}

// TestWriteInputChannelMissingPath 钉住通道根本不存在时的行为。
func TestWriteInputChannelMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.fifo")
	if err := WriteInputChannel(path, []byte("x")); err == nil {
		t.Fatalf("通道不存在时投递竟然成功了")
	}
}

// TestOpenInputChannelNeverEOFs 钉住整个输入通道设计的地基：
// 写端来了又走之后，读端**不得** EOF。
//
// 这条不是形式主义——unix 上靠 shim 自己 O_RDWR 同时持有写端来保证，
// Windows 上靠 shim 攥着匿名管道写端来保证，两边实现完全不同但契约相同。
// 少了这条，症状是「执行者跑完第一条指令后再也不响应」，极难归因。
func TestOpenInputChannelNeverEOFs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	r, cleanup, err := openInputChannel(path)
	if err != nil {
		t.Fatalf("打开输入通道失败: %v", err)
	}
	defer cleanup()

	// 第一次投递：写端开了又关
	if err := WriteInputChannel(path, []byte("one\n")); err != nil {
		t.Fatalf("第一次投递失败: %v", err)
	}
	buf := make([]byte, 32)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("第一次读失败: %v", err)
	}
	if got := string(buf[:n]); got != "one\n" {
		t.Fatalf("第一次读到 %q，想要 %q", got, "one\n")
	}

	// 第二次投递：若上一次写端关闭导致了 EOF，这里会读到 io.EOF 而不是数据
	if err := WriteInputChannel(path, []byte("two\n")); err != nil {
		t.Fatalf("第二次投递失败: %v", err)
	}
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("第二次读失败（写端关闭把读端 EOF 掉了）: %v", err)
	}
	if got := string(buf[:n]); got != "two\n" {
		t.Fatalf("第二次读到 %q，想要 %q", got, "two\n")
	}
}
