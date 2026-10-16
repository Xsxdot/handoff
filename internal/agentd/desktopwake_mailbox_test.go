// desktopwake_mailbox_test.go —— B434 G2：信箱写入补强（契约 §3.6、条 67–70；拆解稿 r4 ③；
// V review m3 时钟缝与临时文件残留清理）。
//
// 职责：经声明缝（消费循环 / wakeDesktopSeat）锁「同会话同纳秒或并发写信不覆盖、并发读者
// 永不读到半封信、崩溃残留的 tmp 按年龄清理」。时钟用 Server.desktopNow 固定。
// 边界：来信内容见 desktopwake_letter_test；夹具来自 desktopwake_slice_test / _letter_test。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// desktopTestClock 是可推进的固定时钟，装到 Server.desktopNow。
type desktopTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *desktopTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *desktopTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fixDesktopClock 把 env 的桌面端时钟固定在一个纳秒精度的时刻并返回它。
func fixDesktopClock(env *ledgerEnv) *desktopTestClock {
	c := &desktopTestClock{now: time.Unix(0, 1_760_000_000_123_456_789)}
	env.srv.desktopNow = c.Now
	return c
}

// TestB434DesktopMailboxSameInstantTwoLetters 锁条 68（拆解稿 r4 ③）：时钟固定在同一纳秒，
// 同一 CLI 会话连续两轮唤醒写出两个不同文件，均可读回且各含本轮 seq；第二封序号顺延 +1。
func TestB434DesktopMailboxSameInstantTwoLetters(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	clock := fixDesktopClock(env)
	cardID := createCoordCard(t, env)
	const cliID = "same-instant"
	bindDesktopSeat(t, env, cardID, cliID, "local_same-instant")
	var seqs []int64
	for i := 0; i < 2; i++ {
		seqs = append(seqs, openDecisionSeq(t, env, cardID, fmt.Sprintf("第 %d 问", i+1)))
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
			t.Fatalf("第 %d 轮消费: %v", i+1, err)
		}
	}
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := clock.Now().UnixNano()
	want := []string{fmt.Sprintf("%s.%019d.md", cliID, base), fmt.Sprintf("%s.%019d.md", cliID, base+1)}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("条 68：同纳秒两封信应为 %v，实得 %v", want, names)
	}
	for i, name := range want {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(data), fmt.Sprintf(" seq=%d kind=", seqs[i])) {
			t.Fatalf("第 %d 封应含 seq %d：err=%v\n%s", i+1, seqs[i], err, data)
		}
	}
}

// TestB434DesktopMailboxConcurrentWritersAndReaders 锁条 68/69：时钟固定，8 个写者并发经
// wakeDesktopSeat 给同一 CLI 会话写信，同时一个读者反复列目录读最终名——读者从不见到不完整
// 的信；结束时恰 8 封、各含自己的 seq。建议带 -race 跑。
func TestB434DesktopMailboxConcurrentWritersAndReaders(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	fixDesktopClock(env)
	cardID := createCoordCard(t, env)
	seat := bindDesktopSeat(t, env, cardID, "concurrent", "local_concurrent")
	desk := localDesk(t, env, cardID)
	const writers = 8
	seqs := make([]int64, writers)
	for i := range seqs {
		seqs[i] = openDecisionSeq(t, env, cardID, fmt.Sprintf("并发 %d", i))
	}
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	finalName := regexp.MustCompile(`^concurrent\.[0-9]{19}\.md$`)
	var stop atomic.Bool
	readerErr := make(chan error, 1)
	go func() {
		defer close(readerErr)
		for !stop.Load() {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if !finalName.MatchString(e.Name()) {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					continue // 读到与删除竞态的名字可忽略；本测试无删除者
				}
				s := string(data)
				if !strings.HasPrefix(s, proto.DesktopWakeMarker+" ") || !strings.HasSuffix(s, "有事会再叫醒你。\n") {
					readerErr <- fmt.Errorf("读到不完整的信 %s：%q", e.Name(), s)
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = env.srv.wakeDesktopSeat(context.Background(), cardID, seat, desk, nil,
				[]proto.LedgerEvent{rawEventAt(t, env, seqs[i])}, fmt.Sprintf("rid-%d", i))
		}(i)
	}
	wg.Wait()
	stop.Store(true)
	if err := <-readerErr; err != nil {
		t.Fatalf("条 69：%v", err)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("写者 %d: %v", i, err)
		}
	}
	letters := desktopLettersIn(t, home)
	if len(letters) != writers {
		t.Fatalf("条 68：%d 个并发写者应得 %d 封信，实得 %d", writers, writers, len(letters))
	}
	for _, seq := range seqs {
		hits := 0
		for _, l := range letters {
			if strings.Contains(l, fmt.Sprintf(" seq=%d kind=", seq)) {
				hits++
			}
		}
		if hits != 1 {
			t.Fatalf("seq %d 应恰在一封信里，实得 %d", seq, hits)
		}
	}
}

// TestB434DesktopMailboxSweepsStaleTmp 锁临时文件残留清理（V review 遗留项，plan 定，不改冻结面）：
// 一封信成功落盘后，本会话超过一小时的 tmp 被删；本会话新 tmp 与他会话旧 tmp 保留。
func TestB434DesktopMailboxSweepsStaleTmp(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	bindDesktopSeat(t, env, cardID, "sweep", "local_sweep")
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	files := map[string]bool{ // 名字 → 期望保留
		".sweep.0000000000000000001.md.tmp": false, // 本会话、旧 → 删
		".sweep.0000000000000000002.md.tmp": true,  // 本会话、新 → 留
		".other.0000000000000000001.md.tmp": true,  // 他会话、旧 → 留
	}
	for name, keep := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("半封"), 0o600); err != nil {
			t.Fatal(err)
		}
		if !keep || strings.HasPrefix(name, ".other.") {
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	openDecisionSeq(t, env, cardID, "触发写信")
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, keep := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		if exists := err == nil; exists != keep {
			t.Errorf("%s：存在=%v，want %v", name, exists, keep)
		}
	}
	if len(desktopLettersIn(t, home)) != 1 {
		t.Fatal("应恰一封新信")
	}
}

// TestB434DesktopMailboxUnsafeKeySentinel 锁契约 r12 第 6 条：writeDesktopLetter 对不安全信箱键
// （空、含 '/' 或 '\'、以 '.' 开头）返回包 errDesktopMailboxKeyUnsafe 的错误（G3 收件机据此映射
// 400），且不碰文件系统；HOME 为空不是信箱键问题，不得被归为该哨兵。
func TestB434DesktopMailboxUnsafeKeySentinel(t *testing.T) {
	for _, key := range []string{"", "a/b", `a\b`, ".hidden"} {
		home := t.TempDir()
		_, err := writeDesktopLetter(home, key, "信", time.Unix(0, 1))
		if !errors.Is(err, errDesktopMailboxKeyUnsafe) {
			t.Fatalf("信箱键 %q 应返回 errDesktopMailboxKeyUnsafe，实得 %v", key, err)
		}
		if _, statErr := os.Stat(filepath.Join(home, proto.DesktopWakeDirRel)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("信箱键 %q 被拒时不得建信箱目录：%v", key, statErr)
		}
	}
	if _, err := writeDesktopLetter(" ", "ok-key", "信", time.Unix(0, 1)); err == nil || errors.Is(err, errDesktopMailboxKeyUnsafe) {
		t.Fatalf("HOME 为空应报错但不是信箱键哨兵，实得 %v", err)
	}
}
