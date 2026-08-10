// agentd 单实例锁测试：互斥、释放后可重入、跨 DataDir 不干扰、错误文案可行动。
//
// 为什么不起子进程：flock 挂在「打开的文件描述」上而非进程上，同一进程内
// 两次 OpenFile 同一路径同样互斥——本机实测确认。
package agentd_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/agentd"
)

// lockTestLogger 返回丢弃所有输出的 logger，免得单测日志灌进测试输出。
func lockTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAcquireDataDirLockCreatesLockFile(t *testing.T) {
	dir := t.TempDir()
	l, err := agentd.AcquireDataDirLock(dir, lockTestLogger())
	if err != nil {
		t.Fatalf("首次获取锁应成功，实得 %v", err)
	}
	defer l.Release()
	if _, err := os.Stat(filepath.Join(dir, "agentd.lock")); err != nil {
		t.Fatalf("锁文件应被创建：%v", err)
	}
}

func TestAcquireDataDirLockSecondFails(t *testing.T) {
	dir := t.TempDir()
	first, err := agentd.AcquireDataDirLock(dir, lockTestLogger())
	if err != nil {
		t.Fatalf("首次获取锁应成功，实得 %v", err)
	}
	defer first.Release()

	second, err := agentd.AcquireDataDirLock(dir, lockTestLogger())
	if err == nil {
		second.Release()
		t.Fatal("同一 DataDir 第二次获取锁必须失败")
	}
}

func TestAcquireDataDirLockErrorIsActionable(t *testing.T) {
	dir := t.TempDir()
	first, err := agentd.AcquireDataDirLock(dir, lockTestLogger())
	if err != nil {
		t.Fatalf("首次获取锁应成功，实得 %v", err)
	}
	defer first.Release()

	_, err = agentd.AcquireDataDirLock(dir, lockTestLogger())
	if err == nil {
		t.Fatal("应撞锁失败")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("错误信息应含 DataDir 路径 %q，实得 %q", dir, err.Error())
	}
	if !strings.Contains(err.Error(), "handoff status") {
		t.Errorf("错误信息应指向 handoff status，实得 %q", err.Error())
	}
}

func TestDataDirLockReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	first, err := agentd.AcquireDataDirLock(dir, lockTestLogger())
	if err != nil {
		t.Fatalf("首次获取锁应成功，实得 %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("释放锁应成功，实得 %v", err)
	}
	second, err := agentd.AcquireDataDirLock(dir, lockTestLogger())
	if err != nil {
		t.Fatalf("释放后应可重新获取，实得 %v", err)
	}
	second.Release()
}

func TestDataDirLockDifferentDirsDoNotConflict(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	la, err := agentd.AcquireDataDirLock(a, lockTestLogger())
	if err != nil {
		t.Fatalf("锁 A 应成功，实得 %v", err)
	}
	defer la.Release()
	lb, err := agentd.AcquireDataDirLock(b, lockTestLogger())
	if err != nil {
		t.Fatalf("锁 B 不应受锁 A 影响，实得 %v", err)
	}
	defer lb.Release()
}

func TestAcquireDataDirLockMissingDirIsReadable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created")
	_, err := agentd.AcquireDataDirLock(missing, lockTestLogger())
	if err == nil {
		t.Fatal("DataDir 不存在时应返回错误")
	}
	if !strings.Contains(err.Error(), "锁文件") {
		t.Errorf("错误应说明是打开锁文件失败，实得 %q", err.Error())
	}
}
