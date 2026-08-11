package prochost

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// shrinkBackoff 把复核退避换成微秒级，让复核路径的测试不真的等 1s。
// 用 t.Cleanup 还原，避免影响同包其它用例。
func shrinkBackoff(t *testing.T) {
	t.Helper()
	orig := killVerifyBackoff
	killVerifyBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { killVerifyBackoff = orig })
}

// stubAlive 把存活判定换成脚本化的返回序列（最后一个值会被重复使用）。
func stubAlive(t *testing.T, seq ...bool) {
	t.Helper()
	orig := aliveFn
	i := 0
	aliveFn = func(Handle) bool {
		v := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return v
	}
	t.Cleanup(func() { aliveFn = orig })
}

// stubKillGroup 换掉真实的 SIGKILL，返回一个记录调用次数的指针。
func stubKillGroup(t *testing.T, err error) *int {
	t.Helper()
	orig := killGroupFn
	n := 0
	killGroupFn = func(int) error { n++; return err }
	t.Cleanup(func() { killGroupFn = orig })
	return &n
}

func testHandle(t *testing.T) Handle {
	t.Helper()
	return Handle{PID: 4242, LockPath: filepath.Join(t.TempDir(), "shim.lock")}
}

// TestKillSkipsWhenLockFree 验证锁已释放时直接成功，且**绝不发信号**
// （对已回收的 pid 发信号有误杀被复用 pid 的风险，这是本包的历史教训）。
func TestKillSkipsWhenLockFree(t *testing.T) {
	stubAlive(t, false)
	n := stubKillGroup(t, nil)
	if err := Kill(testHandle(t)); err != nil {
		t.Fatalf("锁已释放时 Kill 应直接成功，got %v", err)
	}
	if *n != 0 {
		t.Fatalf("锁已释放却发了 %d 次信号", *n)
	}
}

// TestKillReturnsNilAfterProcessDies 验证复核探到已死即成功返回。
func TestKillReturnsNilAfterProcessDies(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, true, false) // 杀之前活着；第一次复核已死
	stubKillGroup(t, nil)
	if err := Kill(testHandle(t)); err != nil {
		t.Fatalf("复核探到已死时应返回 nil，got %v", err)
	}
}

// TestKillReportsStillAlive 验证走满复核窗口仍存活 → ErrStillAlive。
// 这是 B47 的核心：这个信号以前根本产生不出来。
func TestKillReportsStillAlive(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, true) // 恒活
	stubKillGroup(t, nil)
	err := Kill(testHandle(t))
	if !errors.Is(err, ErrStillAlive) {
		t.Fatalf("err = %v, want errors.Is(..., ErrStillAlive)", err)
	}
}

// TestKillPropagatesSignalFailure 验证「信号发送失败」与「进程没死」是两种错误，
// 不能混为一谈——只有后者值得惊动人。
func TestKillPropagatesSignalFailure(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, true)
	sentinel := errors.New("boom")
	stubKillGroup(t, sentinel)
	err := Kill(testHandle(t))
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want 包住 killGroup 的原始错误", err)
	}
	if errors.Is(err, ErrStillAlive) {
		t.Fatal("信号发送失败不应被报成 ErrStillAlive")
	}
}
