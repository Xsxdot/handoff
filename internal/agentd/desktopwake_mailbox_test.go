// desktopwake_mailbox_test.go —— 信箱写入底座的保留行为（b434-contract.md 保留条 67–70、81–82；
// V review m3 时钟缝与临时文件残留清理；契约 r10 第 11 条 / r12 第 6、10 条）。
//
// 职责：经现行唯一生产调用方——O2 驻场者门铃（consumeAutomationEventsOnce →
// ringResidentDoorbells → residentDoorbellAttempt）——锁「同会话同纳秒或并发写信不覆盖、
// 并发读者永不读到半封信、信箱目录 0o700、崩溃残留的 tmp 按年龄清理」，以及
// writeDesktopLetter 的不安全键哨兵与 desktopMailboxHome 的 HOME 规则。时钟用 Server.desktopNow 固定。
// 边界：B435 X 退役 B434 卡级桌面端席位后由原「卡级来信」载体改坐到门铃载体（plan b435.5 段 1、
// H4：改坐前后同一组变异同红，读数见 b435.5-impl-ledger.md）；门铃自身的语义锁在 residentwake_test.go。
package agentd

import (
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

// fixDesktopClock 把 env 的桌面端时钟固定在一个纳秒精度的时刻并返回它。
func fixDesktopClock(env *ledgerEnv) *desktopTestClock {
	c := &desktopTestClock{now: time.Unix(0, 1_760_000_000_123_456_789)}
	env.srv.desktopNow = c.Now
	return c
}

// TestB434DesktopMailboxSameInstantTwoLetters 锁条 68（同纳秒顺延）与条 70（信箱目录 0o700）：
// 时钟固定在同一纳秒，同一驻场者连续两轮门铃写出两个不同文件，各是本轮的门铃信；第二封序号 +1。
func TestB434DesktopMailboxSameInstantTwoLetters(t *testing.T) {
	env, _, sessionID, _, home, _ := o2ResidentEnv(t)
	clock := fixDesktopClock(env)
	var seqs []int64
	for i := 0; i < 2; i++ {
		seqs = append(seqs, sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", fmt.Sprintf("第 %d 问", i+1), nil, 0))
		o2Consume(t, env.srv)
	}
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("信箱目录 %s 未建（条 70）: %v", dir, err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("信箱目录权限 = %v，want 0700（条 70）", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := clock.Now().UnixNano()
	want := []string{fmt.Sprintf("%s.%019d.md", o2CLISession, base), fmt.Sprintf("%s.%019d.md", o2CLISession, base+1)}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("条 68：同纳秒两封信应为 %v，实得 %v", want, names)
	}
	for i, name := range want {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != o2WantLetter(sessionID, 1, seqs[i], seqs[i]) {
			t.Fatalf("第 %d 封应是 seq %d 的门铃信：err=%v\n%s", i+1, seqs[i], err, data)
		}
	}
}

// TestB434DesktopMailboxConcurrentWritersAndReaders 锁条 68/69：时钟固定，8 个写者并发经门铃
// 写信路径（residentDoorbellAttempt）给同一 CLI 会话各写 6 封，同时一个读者反复列目录读最终名——
// 读者读到的每封都与某封预期信逐字相等（从不见半封）；结束时恰 48 封、各恰一次。建议带 -race 跑。
// 为什么每个写者写多封：「半封」只在建名到写完之间的窗口可见，窗口极短；多封放大命中概率
// （48 < desktopLetterNameRetries=64，同纳秒顺延不会撞上限）。
func TestB434DesktopMailboxConcurrentWritersAndReaders(t *testing.T) {
	env, _, sessionID, _, home, _ := o2ResidentEnv(t)
	fixDesktopClock(env)
	resident := proto.SessionResident{Session: sessionID, Identity: o2Resident,
		DesktopSession: o2DesktopSession, CLISession: o2CLISession}
	addr := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: sessionID, Identity: o2Resident}
	const writers, perWriter = 8, 6
	wantLetters := map[string]int{}
	batches := make([][]*residentDoorbellBatch, writers)
	for i := range batches {
		for k := 0; k < perWriter; k++ {
			b := &residentDoorbellBatch{addr: addr}
			seq := int64(9000 + i*perWriter + k)
			b.add(seq)
			batches[i] = append(batches[i], b)
			wantLetters[o2WantLetter(sessionID, 1, seq, seq)] = 0
		}
	}
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	finalName := regexp.MustCompile(`^` + regexp.QuoteMeta(o2CLISession) + `\.[0-9]{19}\.md$`)
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
				if _, ok := wantLetters[string(data)]; !ok {
					readerErr <- fmt.Errorf("读到不完整的信 %s：%q", e.Name(), data)
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	rounds := make([]residentDoorbellRound, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for _, b := range batches[i] {
				env.srv.residentDoorbellAttempt(fmt.Sprintf("k-%d", i), b, resident, env.srv.desktopClock(), &rounds[i])
			}
		}(i)
	}
	wg.Wait()
	stop.Store(true)
	if err := <-readerErr; err != nil {
		t.Fatalf("条 69：%v", err)
	}
	for i, r := range rounds {
		if r.rung != perWriter || r.undelivered != 0 || r.unopened != 0 {
			t.Fatalf("写者 %d 应恰 %d 次按铃成功，得 %+v", i, perWriter, r)
		}
	}
	letters := o2Letters(t, home)
	if len(letters) != writers*perWriter {
		t.Fatalf("条 68：%d 个并发写者应得 %d 封信，实得 %d", writers, writers*perWriter, len(letters))
	}
	for _, l := range letters {
		if _, ok := wantLetters[l.body]; !ok {
			t.Fatalf("出现非预期信件 %s：%q", l.name, l.body)
		}
		wantLetters[l.body]++
	}
	for body, n := range wantLetters {
		if n != 1 {
			t.Fatalf("每封应恰一次，%q 出现 %d 次", body, n)
		}
	}
}

// TestB434DesktopMailboxSweepsStaleTmp 锁临时文件残留清理（V review 遗留项）：一封门铃信成功落盘后，
// 本会话超过一小时的 tmp 被删；本会话新 tmp 与他会话旧 tmp 保留。
func TestB434DesktopMailboxSweepsStaleTmp(t *testing.T) {
	env, _, sessionID, _, home, _ := o2ResidentEnv(t)
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	self := "." + o2CLISession
	files := map[string]bool{ // 名字 → 期望保留
		self + ".0000000000000000001.md.tmp": false, // 本会话、旧 → 删
		self + ".0000000000000000002.md.tmp": true,  // 本会话、新 → 留
		".other.0000000000000000001.md.tmp":  true,  // 他会话、旧 → 留
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
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "触发写信", nil, 0)
	o2Consume(t, env.srv)
	for name, keep := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		if exists := err == nil; exists != keep {
			t.Errorf("%s：存在=%v，want %v", name, exists, keep)
		}
	}
	if len(o2Letters(t, home)) != 1 {
		t.Fatal("应恰一封新信")
	}
}

// TestB434DesktopMailboxUnsafeKeySentinel 锁契约 r12 第 6 条：writeDesktopLetter 对不安全信箱键
// （空、含 '/' 或 '\'、以 '.' 开头）返回包 errDesktopMailboxKeyUnsafe 的错误（门铃据此不重试），
// 且不碰文件系统；HOME 为空不是信箱键问题，不得被归为该哨兵。
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

// TestB434DesktopMailboxHomeRule 锁契约 r12 第 10 条「信箱 HOME 规则唯一定义」：
// desktopMailboxHome 在 desktopHome 非空时原样返回，为空时取 os.UserHomeDir()。
func TestB434DesktopMailboxHomeRule(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	home, _ := o2Wire(t, env.srv)
	if got, err := env.srv.desktopMailboxHome(); err != nil || got != home {
		t.Fatalf("desktopHome=%q 时应原样返回，实得 %q err=%v", home, got, err)
	}
	env.srv.desktopHome = ""
	want, wantErr := os.UserHomeDir()
	if got, err := env.srv.desktopMailboxHome(); got != want || (err == nil) != (wantErr == nil) {
		t.Fatalf("desktopHome 为空时应取 os.UserHomeDir()=%q（err=%v），实得 %q err=%v", want, wantErr, got, err)
	}
}
