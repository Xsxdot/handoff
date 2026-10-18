package p2p

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/p2p/p2ptest"
)

type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func debugLogger(s *logSink) *slog.Logger {
	return slog.New(slog.NewTextHandler(s, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// pair 在两端各收集候选并连通，返回 controlling/controlled 两端的 Conn。
func pair(t *testing.T, w *p2ptest.World, stunURLs []string, budget time.Duration, sink *logSink) (*Conn, *Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	var log *slog.Logger
	if sink != nil {
		log = debugLogger(sink)
	}
	a, err := Gather(ctx, Config{STUN: stunURLs, Net: w.A, Logger: log}, Controlling)
	if err != nil {
		t.Fatalf("gather a: %v", err)
	}
	b, err := Gather(ctx, Config{STUN: stunURLs, Net: w.B, Logger: log}, Controlled)
	if err != nil {
		t.Fatalf("gather b: %v", err)
	}
	var bc *Conn
	var berr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		bc, berr = b.Connect(ctx, a.Local())
	}()
	ac, aerr := a.Connect(ctx, b.Local())
	if aerr == nil {
		// 应答方 AcceptStream 等首个 DATA（生产中由 E2E SecureClient 先写）。
		if _, err := ac.Write([]byte{0x42}); err != nil {
			t.Fatalf("first write: %v", err)
		}
	}
	wg.Wait()
	if aerr != nil || berr != nil {
		_ = a.Close()
		_ = b.Close()
		if aerr != nil {
			return nil, nil, aerr
		}
		return nil, nil, berr
	}
	one := make([]byte, 1)
	if _, err := io.ReadFull(bc, one); err != nil || one[0] != 0x42 {
		t.Fatalf("first read: %v %v", one, err)
	}
	t.Cleanup(func() { _ = ac.Close(); _ = bc.Close() })
	return ac, bc, nil
}

// S1 核心：两端端口受限锥 NAT 后经 srflx 连通，字节流语义正确。
func TestConnectThroughPortRestrictedNATs(t *testing.T) {
	w := p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.PortRestrictedCone)
	sink := &logSink{}
	start := time.Now()
	ac, bc, err := pair(t, w, []string{w.STUNURL()}, 12*time.Second, sink)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Logf("连通耗时 %v path=%+v", time.Since(start), ac.Path())
	if p := ac.Path(); p.LocalType != "srflx" && p.LocalType != "prflx" {
		t.Fatalf("跨 NAT 应选 srflx/prflx 候选，got %+v", p)
	}
	if !ac.Selectable() {
		t.Fatal("新建直连应可选")
	}
	// 字节流：> 64 KiB 单次写被切片，对端用小缓冲拼回原字节。
	payload := make([]byte, 300*1024)
	_, _ = rand.Read(payload)
	errc := make(chan error, 1)
	go func() { _, err := ac.Write(payload); errc <- err }()
	got := make([]byte, 0, len(payload))
	small := make([]byte, 1000)
	for len(got) < len(payload) {
		n, err := bc.Read(small)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		got = append(got, small[:n]...)
	}
	if err := <-errc; err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("字节流内容不一致")
	}
	// S6：日志里不得出现候选串与 ICE 凭据。
	logs := sink.String()
	if strings.Contains(logs, " typ ") || strings.Contains(logs, "ufrag ") || strings.Contains(logs, ac.agent.local.Pwd) {
		t.Fatalf("日志泄漏候选或凭据:\n%s", logs)
	}
}

// S2 核心：一端对称 NAT → 有界时间内早退 ErrICEFailed（不拖到预算末尾）。
func TestSymmetricNATFailsFast(t *testing.T) {
	w := p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.Symmetric)
	start := time.Now()
	_, _, err := pair(t, w, []string{w.STUNURL()}, 30*time.Second, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrICEFailed) {
		t.Fatalf("对称 NAT 应 ErrICEFailed，got %v", err)
	}
	if elapsed > 14*time.Second {
		t.Fatalf("放弃过慢: %v（预算 30s，须靠 Failed 早退）", elapsed)
	}
	t.Logf("放弃耗时 %v", elapsed)
}

// S3 核心：连通后路径中断 → Disconnected 期间不可选，随后 Dead。
func TestPathCutMarksDead(t *testing.T) {
	w := p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.PortRestrictedCone)
	ac, _, err := pair(t, w, []string{w.STUNURL()}, 12*time.Second, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	w.Cut(true)
	start := time.Now()
	select {
	case <-ac.Dead():
	case <-time.After(20 * time.Second):
		t.Fatal("断链 20s 未判死")
	}
	if ac.Selectable() {
		t.Fatal("已死直连仍可选")
	}
	t.Logf("判死耗时 %v", time.Since(start))
}

// S8：STUN 列表为空 → 只 host 候选，零 STUN 包；跨 NAT 因此连不通。
func TestEmptySTUNSendsNoSTUN(t *testing.T) {
	w := p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.PortRestrictedCone)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := Gather(ctx, Config{STUN: nil, Net: w.A}, Controlling)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, c := range a.Local().Candidates {
		if !strings.Contains(c, "typ host") {
			t.Fatalf("空 STUN 列表出现非 host 候选: %s", c)
		}
	}
	if n := w.STUNPackets(); n != 0 {
		t.Fatalf("空 STUN 列表仍发出 %d 个 STUN 包", n)
	}
}

func TestBadSTUNURLRejected(t *testing.T) {
	if _, err := Gather(context.Background(), Config{STUN: []string{"nope"}}, Controlling); err == nil {
		t.Fatal("非法 STUN URL 应报错")
	}
}

// 泄漏族：连通、关闭后 goroutine 回到世界搭好时的基线。
func TestCloseReleasesGoroutines(t *testing.T) {
	w := p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.PortRestrictedCone)
	time.Sleep(100 * time.Millisecond)
	base := runtime.NumGoroutine()
	ac, bc, err := pair(t, w, []string{w.STUNURL()}, 12*time.Second, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_ = ac.Close()
	_ = bc.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	t.Fatalf("关闭后 goroutine 未回收: base=%d now=%d\n%s", base, runtime.NumGoroutine(), buf[:n])
}
