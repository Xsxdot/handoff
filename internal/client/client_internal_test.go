// client 包内部单元测试：直接覆盖 isPermanent/isPermanentStatus 的错误分类逻辑
// 与 cursor 并发写盘（L-3）。
//
// 为什么单独立文件而非并入 client_test.go：分类函数与 writeCursor 是 P0-2/L-3
// 修复的核心判定，外部测试包（client_test）无法访问未导出标识符，用 package
// client 白盒测试直接验证「哪些错误永久、哪些瞬时」与「并发写 cursor 无半写
// 内容」，与行为测试（TestWaitEvent*FailsFast）互为补充——行为测试证明
// 「不重试」，本文件证明「分类本身正确 / 写盘原子」。
package client

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestIsPermanent 覆盖分类函数对各类错误的判定：配置类（401/403/400）、
// 任务不存在（PolicyViolation close）为永久；网络类与正常关闭为瞬时。
func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"握手 401", &permanentError{op: "WS 拨号", code: http.StatusUnauthorized, cause: errors.New("x")}, true},
		{"握手 403", &permanentError{op: "WS 拨号", code: http.StatusForbidden, cause: errors.New("x")}, true},
		{"握手 400", &permanentError{op: "WS 拨号", code: http.StatusBadRequest, cause: errors.New("x")}, true},
		{"握手 500 瞬时", fmt.Errorf("WS 拨号失败 status=500: %w", errors.New("x")), false},
		{"网络错误瞬时", fmt.Errorf("WS 拨号失败: %w", io.ErrUnexpectedEOF), false},
		{"读取断流瞬时", fmt.Errorf("WS 读取: %w", io.ErrClosedPipe), false},
		{"PolicyViolation close", fmt.Errorf("WS 读取: %w", websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: "task not found"}), true},
		{"正常关闭瞬时", fmt.Errorf("WS 读取: %w", websocket.CloseError{Code: websocket.StatusNormalClosure}), false},
		{"GoingAway 瞬时", fmt.Errorf("WS 读取: %w", websocket.CloseError{Code: websocket.StatusGoingAway}), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanent(tc.err); got != tc.want {
				t.Fatalf("isPermanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsPermanentStatus 覆盖握手状态码判定：400/401/403 永久，其余瞬时。
func TestIsPermanentStatus(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusNotFound, true},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
		{http.StatusOK, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status %d", tc.code), func(t *testing.T) {
			if got := isPermanentStatus(tc.code); got != tc.want {
				t.Fatalf("isPermanentStatus(%d) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestPermanentErrorUnwrap 保证 permanentError 可被 errors.As 穿透到原始错误
// （外层错误链检查不因包装丢失信息）。
func TestPermanentErrorUnwrap(t *testing.T) {
	root := errors.New("handshake 401")
	pe := &permanentError{op: "WS 拨号", code: http.StatusUnauthorized, cause: root}
	if !errors.Is(pe, root) {
		t.Fatalf("permanentError 未透传 cause（errors.Is 失败）")
	}
}

// TestWriteCursorConcurrent 覆盖 L-3：两个 goroutine 并发写同一任务的 cursor 时，
// 任何时刻读到的内容都必须是「完整值」——旧实现固定 <path>.tmp 文件名，并发写
// 会互相截断/rename 对方写到一半的临时文件，读方可能拿到半截或空内容。
//
// 为什么直接读文件原文而非 readCursor：readCursor 把解析失败归一化为 0（从头
// 开始），会掩盖「读到半写内容」这一失败模式；直接 os.ReadFile + 逐字节校验，
// 任何非空但不在合法值集合（两写者的写序区间）内的内容都算失败。
//
// 时序敏感：-race 下跑 200 次迭代/写者 + 高频读循环，穷举交错窗口。
func TestWriteCursorConcurrent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &Client{}
	taskID := "t-concurrent"
	const iters = 200
	// 两个写者各写一段互不相交的合法值区间，读方按「内容 ∈ 合法集合」校验
	valid := func(n int64) bool { return (n >= 10001 && n <= 10000+iters) || (n >= 20001 && n <= 20000+iters) }

	p, err := cursorPath(taskID)
	if err != nil {
		t.Fatalf("cursorPath: %v", err)
	}
	errCh := make(chan error, 4)
	stop := make(chan struct{})

	// 读协程：写盘期间持续读原文，空内容/半截内容/非法值即失败
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(p)
			if err != nil {
				if os.IsNotExist(err) {
					continue // 首次写入前文件不存在，正常
				}
				errCh <- fmt.Errorf("读 cursor 失败: %w", err)
				return
			}
			if len(b) == 0 {
				errCh <- errors.New("读到空内容——目标文件被半写（rename 了未写完的临时文件）")
				return
			}
			n, perr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
			if perr != nil || !valid(n) {
				errCh <- fmt.Errorf("读到半截/非法内容 %q（期望完整合法 seq）", string(b))
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for _, base := range []int64{10001, 20001} {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < iters; i++ {
				if werr := c.writeCursor(taskID, base+i); werr != nil {
					errCh <- werr
					return
				}
			}
		}(base)
	}
	wg.Wait()
	close(stop)
	select {
	case err := <-errCh:
		t.Fatalf("并发写读失败: %v", err)
	default:
	}

	// 终值必须是某个写者的最大值（10200 或 20200）：并发下最后一次 rename
	// 属于哪个写者是不确定的，但必须是「写完整」的终值——绝不能是中间态
	final, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读最终 cursor: %v", err)
	}
	if got := strings.TrimSpace(string(final)); got != "10200" && got != "20200" {
		t.Fatalf("最终 cursor=%q, want 10200 或 20200（写者终值）", got)
	}
	// 临时文件全部被 rename/清理：目录不得残留 cursor-<task>-*.tmp
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(p), "cursor-"+taskID+"-*.tmp"))
	if err != nil {
		t.Fatalf("glob 残留临时文件: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("残留 %d 个临时文件: %v", len(leftovers), leftovers)
	}
}

// TestWriteCursorSweepsStaleTemps 验证陈旧的 cursor 临时文件会被清掉。
//
// 缺陷形态：writeCursor 用 CreateTemp + Rename 保证原子写，但进程在两步之间
// 被杀（Ctrl+C、机器重启、oom kill）就会留下一个 .tmp，而此后再没有任何代码
// 碰它——~/.handoff 里的 .tmp 只增不减。
//
// 只清「足够旧」的：同一任务可能有并发的 wait 进程正在写自己的临时文件，
// 按年龄设阈值才不会误删别人在途的写入。
func TestWriteCursorSweepsStaleTemps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:1", "")
	p, err := cursorPath("task-sweep")
	if err != nil {
		t.Fatalf("cursorPath: %v", err)
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	stale := filepath.Join(dir, "cursor-task-sweep-999.tmp")
	fresh := filepath.Join(dir, "cursor-task-sweep-111.tmp")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("7"), 0o600); err != nil {
			t.Fatalf("造临时文件: %v", err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("改 mtime: %v", err)
	}

	if err := c.writeCursor("task-sweep", 42); err != nil {
		t.Fatalf("writeCursor: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("陈旧的 .tmp 应被清理, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("在途的新 .tmp 不应被误删（可能是并发进程的写入）: %v", err)
	}
}
