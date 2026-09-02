package agy

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
)

func TestAgyArgv(t *testing.T) {
	cases := []struct {
		name string
		req  StartProcReq
		want []string
	}{
		{
			name: "普通启动无模型",
			req:  StartProcReq{},
			want: []string{"agy", "--input-format", "stream-json", "--output-format", "stream-json", "--print-timeout", "24h"},
		},
		{
			name: "带模型",
			req:  StartProcReq{Model: "claude-3-5-sonnet"},
			want: []string{"agy", "--input-format", "stream-json", "--output-format", "stream-json", "--print-timeout", "24h", "--model", "claude-3-5-sonnet"},
		},
		{
			name: "恢复会话",
			req:  StartProcReq{SessionID: "sess-123", Resume: true},
			want: []string{"agy", "--input-format", "stream-json", "--output-format", "stream-json", "--print-timeout", "24h", "--conversation", "sess-123"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := agyArgv(c.req)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for _, arg := range got {
				if arg == "--dangerously-skip-permissions" {
					t.Fatalf("agy argv 禁止 --dangerously-skip-permissions: %v", got)
				}
			}
		})
	}
}

func TestWriteInput(t *testing.T) {
	tmpDir := t.TempDir()
	fifoPath := filepath.Join(tmpDir, fifoFileName)
	if err := prochost.CreateInputChannel(fifoPath); err != nil {
		t.Fatalf("创建通道失败: %v", err)
	}

	// 读端常开，模拟 shim 的 O_RDWR
	rd, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("打开读端失败: %v", err)
	}
	defer rd.Close()

	p := &Proc{TaskDir: tmpDir, SessionID: "s1"}
	if err := p.WriteInput("hello world"); err != nil {
		t.Fatalf("WriteInput 失败: %v", err)
	}

	buf := make([]byte, 256)
	n, err := rd.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("未读到写入的数据: err=%v, n=%d", err, n)
	}
}

func TestProcExited(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, outFileName)

	exited, code := procExited(outPath)
	if exited {
		t.Fatalf("文件不存在时不应判断为退出")
	}

	// 写入正常内容
	os.WriteFile(outPath, []byte("{\"event\":\"step_update\"}\n"), 0600)
	exited, _ = procExited(outPath)
	if exited {
		t.Fatalf("无哨兵时不应判断为退出")
	}

	// 追加哨兵
	os.WriteFile(outPath, []byte("{\"type\":\"handoff_exit\",\"code\":0}\n"), 0600)
	exited, code = procExited(outPath)
	if !exited || code != 0 {
		t.Fatalf("exited=%v code=%d, want true, 0", exited, code)
	}
}

func TestStartProcStub(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := t.TempDir()

	oldLook := lookAgyPath
	oldStart := startProcHost
	oldTimeout := fifoReaderTimeout
	defer func() {
		lookAgyPath = oldLook
		startProcHost = oldStart
		fifoReaderTimeout = oldTimeout
	}()

	fifoReaderTimeout = 10 * time.Millisecond
	lookAgyPath = func() (string, error) { return "/mock/agy", nil }
	var infoExistedAtSpawn bool
	startProcHost = func(spec prochost.Spec, selfExe string, extra ...string) (prochost.Handle, error) {
		_, err := os.Stat(filepath.Join(tmpDir, procInfoFileName))
		infoExistedAtSpawn = err == nil
		return prochost.Handle{PID: 1234, LockPath: spec.LockPath}, nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, _ = StartProc(context.Background(), StartProcReq{
		RepoPath:  repoDir,
		TaskID:    "T1",
		TaskDir:   tmpDir,
		SessionID: "s-1",
	}, logger)
	if !infoExistedAtSpawn {
		t.Fatal("proc.json 必须在拉起 shim 之前落盘")
	}
}
