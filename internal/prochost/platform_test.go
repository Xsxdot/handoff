package prochost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// helperEnv 是子进程 helper 的开关环境变量：值为 helper 名字。
// 用 os.Args[0] 重入测试二进制是 Go 标准做法（见 os/exec 的 TestHelperProcess），
// 这样不必先 go build handoff 就能拿到「真的另一个进程」。
const helperEnv = "PROCHOST_TEST_HELPER"

// TestHelperSpawner 不是测试：被 TestSpawnDetachedSurvivesParentDeath 以子进程方式
// 调用。它 spawnDetached 一个长命进程、把 pid 打到 stdout，然后立刻退出——用来制造
// 「父进程先死、被拉起的进程还在」这个必须用真进程才能验证的场景。
func TestHelperSpawner(t *testing.T) {
	if os.Getenv(helperEnv) != "spawner" {
		t.Skip("非 helper 调用")
	}
	pid, err := spawnDetached([]string{"/bin/sh", "-c", "sleep 30"}, os.TempDir())
	if err != nil {
		os.Stderr.WriteString("spawn 失败: " + err.Error())
		os.Exit(2)
	}
	os.Stdout.WriteString(strconv.Itoa(pid))
	os.Exit(0)
}

// TestHelperLocker 不是测试：被 TestAliveFollowsLock 以子进程方式调用。
// 它抢占 PROCHOST_TEST_LOCK 指向的锁并阻塞 30s，用来制造「锁被别的进程持有」。
func TestHelperLocker(t *testing.T) {
	if os.Getenv(helperEnv) != "locker" {
		t.Skip("非 helper 调用")
	}
	c, err := AcquireLock(os.Getenv("PROCHOST_TEST_LOCK"))
	if err != nil {
		os.Exit(2)
	}
	defer c.Release()
	os.Stdout.WriteString("locked")
	os.Stdout.Close()
	time.Sleep(30 * time.Second)
}

// runHelper 以子进程方式跑本测试二进制里的某个 helper，返回其 stdout。
func runHelper(t *testing.T, helper, testName string, extraEnv ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^"+testName+"$", "-test.v=false")
	cmd.Env = append(os.Environ(), helperEnv+"="+helper)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper %s 执行失败: %v (stdout=%q)", helper, err, out)
	}
	return string(out)
}

// alivePID 判断 pid 是否还存在（信号 0 探测，不实际发信号）。
func alivePID(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestSpawnDetachedSurvivesParentDeath(t *testing.T) {
	out := runHelper(t, "spawner", "TestHelperSpawner")
	pid, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("helper 未输出 pid，实得 %q", out)
	}
	t.Cleanup(func() { _ = killGroup(pid) })

	// helper 进程（模拟 agentd）已经退出，被它 spawnDetached 的进程必须还活着。
	// 这是 shim 存在的前提：agentd 崩溃不带走执行者。
	if !alivePID(pid) {
		t.Fatalf("父进程退出后被 detach 的进程 %d 也没了，detach 未生效", pid)
	}
	// 且它必须是独立进程组组长（pgid == pid），Kill 才能按组连坐
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("取 %d 的 pgid 失败: %v", pid, err)
	}
	if pgid != pid {
		t.Fatalf("被 detach 的进程必须是进程组组长，pid=%d pgid=%d", pid, pgid)
	}
}

func TestAliveFollowsLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "proc.lock")

	// 无人持锁：Alive 必须为 false
	if Alive(Handle{PID: os.Getpid(), LockPath: lock}) {
		t.Fatal("锁无人持有时 Alive 必须为 false")
	}

	// 让另一个进程持锁，Alive 必须翻成 true
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHelperLocker$", "-test.v=false")
	cmd.Env = append(os.Environ(), helperEnv+"=locker", "PROCHOST_TEST_LOCK="+lock)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("建 stdout 管道失败: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 locker helper 失败: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	buf := make([]byte, len("locked"))
	if _, err := stdout.Read(buf); err != nil {
		t.Fatalf("等 locker 就绪失败: %v", err)
	}
	if !Alive(Handle{PID: cmd.Process.Pid, LockPath: lock}) {
		t.Fatal("锁被其他进程持有时 Alive 必须为 true")
	}

	// 持锁进程死亡后内核释放锁，Alive 必须回到 false（不依赖任何清理代码）
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for Alive(Handle{PID: cmd.Process.Pid, LockPath: lock}) {
		if time.Now().After(deadline) {
			t.Fatal("持锁进程已死，Alive 仍为 true——内核未释放锁或判定有误")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKillIsNoOpWhenLockFree(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "proc.lock")
	// 造一个真实存在、但与本 Handle 无关的进程，模拟「pid 已被复用」
	victim := exec.Command("/bin/sh", "-c", "sleep 10")
	if err := victim.Start(); err != nil {
		t.Fatalf("启动 victim 失败: %v", err)
	}
	t.Cleanup(func() { _ = victim.Process.Kill(); _ = victim.Wait() })

	// 锁是空闲的 → 视为已死 → 绝不能对这个 pid 发信号
	if err := Kill(Handle{PID: victim.Process.Pid, LockPath: lock}); err != nil {
		t.Fatalf("锁空闲时 Kill 应直接成功，实得 %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !alivePID(victim.Process.Pid) {
		t.Fatal("锁空闲时 Kill 误杀了无关进程——防误杀纪律失效")
	}
}

func TestCreateInputChannelIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "in.fifo")
	if err := CreateInputChannel(p); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	if err := CreateInputChannel(p); err != nil {
		t.Fatalf("重复创建应幂等，实得 %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("创建出来的不是命名管道")
	}
	// 残留的普通文件必须被识别为错误，而不是当成管道复用
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatalf("造普通文件失败: %v", err)
	}
	if err := CreateInputChannel(plain); err == nil {
		t.Fatal("同名普通文件已存在时必须报错")
	}
}
