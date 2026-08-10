# 拆除 tmux：跨平台进程承载层（prochost + shim）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用纯 Go 的 `prochost` + shim 进程承载层替换 tmux + sh 脚本，让四个 executor adapter 的进程生命周期平台无关，同时把 `handoff attach` 改成流式 HTTP 客户端。

**Architecture:** 新建 `internal/prochost` 包提供三组平台原语（detached spawn / 文件锁存活判定 / 进程组回收）与 shim 主体逻辑；shim 由 `handoff _shim` 隐藏子命令承载，负责持锁、开落盘文件、持 FIFO 读端、spawn executor、wait 后补写退出哨兵。四个 adapter 的 `proc.go` 从「写 sh 脚本 + tmux new-session」改为「组 argv + prochost.Start」。观测侧新增 `GET /api/tasks/{id}/render` 流式 endpoint，CLI `attach` 从 execve 进 tmux 改为消费该流。

**Tech Stack:** Go 1.26.1、stdlib `syscall`（配 `//go:build unix` / `!unix`，沿用仓库既有三处平台切分套路：`opennonblock_*`、`workspace_procgroup_*`、`flock_*`）、stdlib `net/http`、cobra、slog。**不引入 `golang.org/x/sys`**——已实测 `Setsid`/`Mkfifo`/`Flock`/`Kill`/`ENXIO`/`EWOULDBLOCK`/`ESRCH` 在 darwin 与 linux 的 stdlib `syscall` 里全部可用。

**Spec:** `docs/superpowers/specs/2026-08-10-handoff-detmux-prochost-design.md`

## Global Constraints

- 本计划是 **A 期**：Unix（darwin/linux）实现完整落地，Windows 只留 build tag 骨架返回 `not implemented`。不引 `go-winio`、不做 Job Object、不搭 Windows CI。
- 验收硬门禁：`go test ./...` 全绿 **且** `GOOS=windows GOARCH=amd64 go build ./...` 全绿。后者当前因 `internal/executor/claudecode/proc.go:271` 的 `syscall.Mkfifo` 失败，Task 3 后必须通过。
- **不做旧格式兼容**：尚未发版，升级前清空在跑任务。Reap 只认新格式 `proc.json`，读到不认识的内容如实报错。不保留任何 `tmux kill-session` 遗留路径。
- 所有新建 Go 文件写文件头注释（职责 + 边界）；所有导出项写文档注释（参数/返回/注意）；复杂约束与边界条件写「为什么」中文注释。
- 关键节点必须用 `slog`（模块 logger）打结构化日志，**禁止 `fmt.Printf`**：进程启动/退出、锁获取/释放、外部调用前后、每个错误分支（带 cause 与上下文）、成功路径的结果。env 的**值绝不进日志，只打 key 名**（沿用 B19 既有纪律）。
- 每个任务遵循 red → green → refactor → 聚焦验证 → 回归验证（`go test ./...`）→ commit；不得合并多个任务后一次性补测试或日志。
- 保持现有 CLI REST/WS 线格式不变（除新增 render endpoint）；不改任务状态机语义。
- 归一化命名统一：`Handle`、`Spec`、`RunShim`、`proc.json`、`proc.lock`。四个 adapter 的持久化文件一律改名 `proc.json`（现为 `claude.json` / `serve.json`）。
- **面向审核者的文本一并改**：`Probe` 的 `Note`、adapter 追加的 progress 事件文本、
  `reconcile.go` 的残留提示，现在都写着「tmux 会话 X」「请手动 tmux kill-session」
  「请 tmux attach 查看现场」。这些是**人会读到的字**，留着就是错的指引。
  统一改成「执行者进程（pid N）」「handoff stop 回收」「handoff attach 查看现场」。
  实测非测试 Go 代码里共 111 处 tmux 代码/字符串引用，Task 7 的 grep 门禁兜底。
- **不重复造文件锁**：`internal/agentd/flock_unix.go` / `flock_other.go`（B34，commit `0411df9c`）已有一份 flock 原语。本计划把它上移进 `internal/prochost` 并让 agentd 的 `AcquireDataDirLock` 复用（Task 1），全项目只保留一份 flock 实现。`AcquireDataDirLock` 的公开签名与错误文案语义不变，`lock_test.go` 六条用例必须原样通过。

---

### Task 1: prochost 平台原语（Unix 实现 + Windows 骨架 + agentd flock 归并）

**Files:**
- Create: `internal/prochost/prochost.go`
- Create: `internal/prochost/platform_unix.go`
- Create: `internal/prochost/platform_other.go`
- Test: `internal/prochost/platform_test.go`
- Modify: `internal/agentd/lock.go`（改为复用 prochost 的锁，并清掉文案里的 tmux 表述）
- Delete: `internal/agentd/flock_unix.go`、`internal/agentd/flock_other.go`

**Interfaces:**
- Produces:
  - `type Spec struct { Argv []string; Dir string; Env []string; Stdout, Stderr, InputCh, LockPath, InfoPath string; Sentinel bool }`（全部带 json tag）
  - `type Handle struct { PID int; LockPath string }`（带 json tag `pid` / `lock_path`）
  - `func Alive(h Handle) bool`
  - `func Kill(h Handle) error`
  - `func CreateInputChannel(path string) error`
  - `func WaitInputReader(path string, timeout time.Duration) (time.Duration, error)`
  - 锁 API（供 prochost 自身与 `internal/agentd` 共用）：
    `func AcquireLock(path string) (*Lock, error)`、`func (*Lock) Release() error`、
    `var ErrLockHeld = errors.New("锁已被其他进程持有")`、
    `func IsLocked(path string) (bool, error)`、`func LockSupported() bool`
  - 包内平台缝：`func spawnDetached(argv []string, dir string) (int, error)`、`func killGroup(pid int) error`、`func createInputChannel(path string) error`、`func waitInputReader(path string, timeout time.Duration) (time.Duration, error)`、`func flockExclusiveNB(f *os.File) error`、`func isLockContended(err error) bool`、`const lockSupported bool`
- Consumes: 无（本包是最底层，不 import 任何 internal 包）
- 被改造方：`internal/agentd.AcquireDataDirLock` 的**公开签名与返回错误的语义不变**（`lock_test.go` 六条用例必须原样通过），只把内部的 flock 换成 `prochost.AcquireLock`

- [ ] **Step 1: 写失败测试**

创建 `internal/prochost/platform_test.go`：

```go
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestSpawnDetached|TestAlive|TestKillIsNoOp|TestCreateInputChannel' -v`
Expected: FAIL —— `undefined: spawnDetached` / `undefined: Alive` 等（包尚不存在，编译不过）

- [ ] **Step 3: 实现平台无关门面**

创建 `internal/prochost/prochost.go`：

```go
// Package prochost 提供跨平台的「detached 执行者进程」承载能力。
//
// 职责：
//   - Start：以脱离本进程的方式拉起 shim，由 shim 承载真正的 executor 进程
//   - Alive / Kill：基于文件锁的存活判定与按进程组的回收
//   - CreateInputChannel / WaitInputReader：executor 的输入通道（unix = FIFO）
//   - RunShim：shim 自身的主体逻辑（见 shim.go）
//
// 边界：
//   - 不认识 executor 的协议：只管进程起没起来、活没活着、怎么杀干净；
//     「协议层能不能用」由各 adapter 自己探活
//   - 不写任务状态、不碰 store：Handle 的持久化由调用方（adapter）负责
//   - 不解释 Spec.Argv：argv 由调用方组好，本包原样交给操作系统，不经任何 shell
//
// 为什么存活判定用文件锁而不是 pid：pid 会被操作系统复用，「进程存在」不等于
// 「我的那个进程存在」——历史上 workspace.go 就因此误杀过无关进程组。shim 全生命
// 周期持有 LockPath 的排他锁，内核在进程死亡时无条件释放，试锁失败即证明它还活着，
// 完全没有复用窗口。pid 只用于发信号，不参与存活语义。
package prochost

import (
	"fmt"
	"log/slog"
	"time"
)

// Spec 是一次执行者进程的启动描述，序列化后交给 shim。
//
// 字段说明：
//   - Argv: 完整命令行，[0] 必须是 exec.LookPath 解析后的绝对路径（shim 不做 PATH 查找）
//   - Dir: 子进程工作目录（任务仓库）
//   - Env: 完整环境变量（KEY=VALUE），由调用方合并完毕，shim 原样使用不再追加
//   - Stdout/Stderr: 子进程输出的追加落盘路径；两者可指向同一文件
//   - InputCh: 可选。非空时 shim 以 O_RDWR 持有该 FIFO 并作为子进程 stdin
//   - LockPath: shim 的存活锁路径
//   - InfoPath: shim 补写 child_pid 的 proc.json 路径
//   - Sentinel: true 时子进程退出后向 Stdout 追加 handoff_exit 哨兵行
//
// 注意：
//   - Env 的值可能含凭据，本结构会被序列化到 0600 的 spec.json；日志里只打 key 名
type Spec struct {
	Argv     []string `json:"argv"`
	Dir      string   `json:"dir"`
	Env      []string `json:"env"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	InputCh  string   `json:"input_ch,omitempty"`
	LockPath string   `json:"lock_path"`
	InfoPath string   `json:"info_path"`
	Sentinel bool     `json:"sentinel"`
}

// Handle 是一个已拉起的 shim 的句柄，可直接序列化进 adapter 的 proc.json。
//
// 字段说明：
//   - PID: shim 的进程 id，同时是它的进程组 id（Kill 按组发信号靠它）
//   - LockPath: 存活锁路径；Alive 只看它，不看 PID（见包注释的 why）
type Handle struct {
	PID      int    `json:"pid"`
	LockPath string `json:"lock_path"`
}

// log 返回包日志入口（运行时取 slog.Default()，跟随 agentd 的 logx 配置）。
func log() *slog.Logger { return slog.Default().With("mod", "prochost") }

// Alive 报告 Handle 对应的 shim 是否仍然活着。
//
// 判据：LockPath 上的排他锁被占用。锁由内核在进程死亡时释放，因此本判定
// 不存在 pid 复用误判，也不需要任何清理代码配合。
//
// 返回：LockPath 为空、文件不存在或探测出错时一律返回 false（保守：宁可判死
// 后走恢复流程，也不要把死进程当活的导致任务永远卡住）。
func Alive(h Handle) bool {
	if h.LockPath == "" {
		return false
	}
	locked, err := IsLocked(h.LockPath)
	if err != nil {
		log().Debug("探测存活锁失败，按已死处理", "lock", h.LockPath, "cause", err)
		return false
	}
	return locked
}

// Kill 终止 shim 及其全部后代（按进程组发送 SIGKILL）。
//
// 幂等：锁已空闲说明 shim 已死，直接返回 nil——**绝不对该 pid 发任何信号**，
// 因为它可能已被操作系统复用给毫不相干的进程（workspace.go 的历史教训：
// 旧实现 300 条成功命令误杀 114 次）。
//
// 返回：仅当「确认还活着但杀不掉」时返回错误。
func Kill(h Handle) error {
	if h.PID <= 0 {
		return nil
	}
	if !Alive(h) {
		log().Info("存活锁已释放，无需回收", "pid", h.PID, "lock", h.LockPath)
		return nil
	}
	log().Info("回收执行者进程组", "pid", h.PID)
	if err := killGroup(h.PID); err != nil {
		log().Error("回收执行者进程组失败", "pid", h.PID, "cause", err)
		return fmt.Errorf("回收进程组 %d: %w", h.PID, err)
	}
	return nil
}

// CreateInputChannel 幂等创建输入通道（unix 为 0600 命名管道）。
//
// 参数：path 为通道路径（通常是 <taskDir>/in.fifo）
//
// 返回：
//   - 已存在且确实是命名管道 → nil（复用）
//   - 已存在但是普通文件/目录 → 错误（残留物会让 shim 的 O_RDWR 打开语义完全改变，
//     必须显式失败而不是静默当管道用）
//   - Windows → not implemented（A 期）
func CreateInputChannel(path string) error { return createInputChannel(path) }

// WaitInputReader 等待输入通道上出现读端（shim 已执行到持有 FIFO 那一步）。
//
// 为什么必须等：写端以 O_WRONLY|O_NONBLOCK 打开 FIFO 时，POSIX 规定读端未就绪
// 直接失败（ENXIO，macOS 文案 "device not configured"）。Start 返回只代表 shim
// 已被 fork，不代表它已经打开了 FIFO——这个竞态在 tmux 时代真机复现过
// （8fca917 次生缺陷），换成 shim 后窗口更小但依然存在。
//
// 参数：
//   - path: 通道路径
//   - timeout: 等待上限；到点仍无读端返回错误
//
// 返回：等待耗时（调用方记日志）与错误。非「无读者」类错误立即返回，不重试。
func WaitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	return waitInputReader(path, timeout)
}
```

- [ ] **Step 4: 实现 Unix 平台原语**

创建 `internal/prochost/platform_unix.go`：

```go
//go:build unix

// platform_unix.go —— prochost 的 unix 平台原语。
//
// 职责：detached spawn（新会话 + 新进程组）、flock 加锁与撞锁判定、进程组回收、
// FIFO 输入通道。
//
// 边界：
//   - 只提供系统调用级能力，不含任何 handoff 业务语义；被 prochost.go / lock.go /
//     shim.go 调用
//   - 只用 stdlib syscall，不引 golang.org/x/sys——本仓库既有的三处平台切分
//     （opennonblock_*、workspace_procgroup_*、原 agentd/flock_*）都是这个套路，
//     且实测所需常量与函数在 darwin/linux 的 syscall 里齐备，多一个直接依赖不划算
package prochost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// lockSupported 标记本平台是否真的能加锁。
const lockSupported = true

// flockExclusiveNB 对一个已打开的文件取非阻塞独占锁。
//
// 注意：锁挂在「打开的文件描述」上而不是路径上。两个后果——同一进程内两次
// OpenFile 同一路径同样互斥（测试据此免起子进程）；`rm` 掉锁文件并不能解锁。
// （本函数由 internal/agentd/flock_unix.go 原样上移，行为不变。）
func flockExclusiveNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// isLockContended 判定错误是否为「锁已被他人持有」（LOCK_NB 下的 EWOULDBLOCK），
// 用于把撞锁与真正的 IO 故障分开——两者该给的错误信息完全不同。
func isLockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}

// spawnDetached 拉起 argv 并让它脱离当前进程的会话与进程组，返回其 pid。
//
// 为什么用 Setsid 而不是让子进程自己调 setsid(2)：子进程被 fork 出来时若已是
// 进程组组长，setsid(2) 会返回 EPERM。由父进程在 SysProcAttr 里声明最干净，
// 且一次系统调用同时拿到「新会话 + 新进程组（pgid == pid）」——后者是 Kill
// 能按组连坐全部后代的前提。
//
// 为什么 stdio 全部置 nil：Go 会把它们接到 /dev/null。子进程不能持有本进程的
// 任何 fd，否则 agentd 退出时管道破裂会波及它，detach 就名存实亡。
//
// 边界：本函数不脱离 cgroup——cgroup 归属由 fork 继承，setsid 改不了它。
// systemd 托管场景必须在 unit 里设 KillMode=process，否则 systemctl restart
// 仍会连坐（见 spec §3.3 与 Task 10 的 unit 模板）。
func spawnDetached(argv []string, dir string) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv 为空")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("拉起 %s: %w", argv[0], err)
	}
	pid := cmd.Process.Pid
	// 立刻 Release：本进程不做它的父亲，让它 reparent 给 init。
	// 不 Release 的话 Go 运行时会保留 wait 状态，agentd 退出时行为不确定。
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("释放子进程 %d: %w", pid, err)
	}
	return pid, nil
}

// killGroup 向 pid 所在进程组发送 SIGKILL（负 pid 表示按组发送）。
//
// 幂等：组已不存在时内核返回 ESRCH，视为已回收成功。
// 调用方（prochost.Kill）必须先确认存活锁仍被持有才可调用本函数——
// 对已回收的 pid 发信号有误杀被复用 pid 的风险。
func killGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("kill -9 -%d: %w", pid, err)
	}
	return nil
}

// createInputChannel 幂等创建 0600 命名管道（见 CreateInputChannel 的文档）。
func createInputChannel(path string) error {
	err := syscall.Mkfifo(path, 0o600)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("mkfifo %s: %w", path, err)
	}
	fi, serr := os.Stat(path)
	if serr != nil {
		return fmt.Errorf("stat %s: %w", path, serr)
	}
	// 残留的普通文件会让 shim 的 O_RDWR 打开变成普通文件读写，子进程 stdin
	// 立刻 EOF——症状是「executor 起来了但一句话都不回」，极难排查，必须显式失败
	if fi.Mode()&os.ModeNamedPipe == 0 {
		return fmt.Errorf("%s 已存在但不是命名管道", path)
	}
	return nil
}

// waitInputReader 轮询探测 FIFO 读端（见 WaitInputReader 的文档）。
func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	deadline := time.Now().Add(timeout)
	start := time.Now()
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			f.Close()
			return time.Since(start), nil
		}
		// ENXIO 之外的错误（管道缺失、权限）重试无意义，立即失败
		if !errors.Is(err, syscall.ENXIO) {
			return time.Since(start), fmt.Errorf("探测 %s 读端: %w", path, err)
		}
		if time.Now().After(deadline) {
			return time.Since(start), fmt.Errorf("%s 在 %s 内未出现读端", path, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

- [ ] **Step 5: 实现非 unix 平台骨架**

创建 `internal/prochost/platform_other.go`：

```go
//go:build !unix

// platform_other.go —— prochost 的非 unix 平台原语（A 期骨架）。
//
// 文件名用 _other 而非 _windows：Go 会把 _windows 后缀当成隐式 GOOS 约束，
// 与 //go:build !unix 相与后只覆盖 windows，plan9/js 等非 unix 平台会编译失败。
// 仓库既有的 flock_other.go / opennonblock_other.go 也是这个命名。
//
// 职责：让 prochost 在 GOOS=windows 下编译通过。
//
// 边界与两类退化（**两类语义不同，别混为一谈**）：
//   - **进程类原语**（spawnDetached / killGroup / createInputChannel /
//     waitInputReader）返回 errNotImplemented：拉不起来就是拉不起来，
//     绝不能静默假装成功
//   - **锁原语**（flockExclusiveNB / isLockContended / lockSupported）退化为
//     「加锁恒成功、永不撞锁、lockSupported=false」：这是从
//     internal/agentd/flock_other.go 原样上移的既有决定（B34），调用方据
//     LockSupported() 打 Warn 明说保护未生效，而不是假装锁住了。改成报错会让
//     agentd 的 DataDir 单实例锁在 Windows 上直接启动失败，那是行为退化不是改进
//
// B 期（独立立项）补齐：
//   - spawnDetached → CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS + Job Object
//   - killGroup → Job Object 的 TerminateJobObject
//   - createInputChannel → \\.\pipe\ 命名管道（go-winio）
//   - 锁原语 → LockFileEx（语义与 flock 一致：进程死亡内核释放）
//
// 为什么 A 期只留骨架：Windows 上四个 executor CLI 的可用性尚未验证，
// 进程层写完也无法端到端验收，违背本项目「每个 adapter 都真机端到端」的纪律。
package prochost

import (
	"errors"
	"os"
	"time"
)

// errNotImplemented 是 A 期非 unix 平台进程类原语的统一返回。
var errNotImplemented = errors.New("prochost: 本平台的进程承载尚未实现（A 期只提供骨架，见 B 期计划）")

// lockSupported 标记本平台是否真的能加锁。
const lockSupported = false

// flockExclusiveNB 非 unix 平台无 flock，空操作（见文件头「两类退化」）。
func flockExclusiveNB(*os.File) error { return nil }

// isLockContended 非 unix 平台永远撞不上锁——因为根本没加锁。
func isLockContended(error) bool { return false }

func spawnDetached(argv []string, dir string) (int, error) { return 0, errNotImplemented }

func killGroup(pid int) error { return errNotImplemented }

func createInputChannel(path string) error { return errNotImplemented }

func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	return 0, errNotImplemented
}
```

- [ ] **Step 6: 实现平台无关的锁门面**

创建 `internal/prochost/lock.go`：

```go
// lock.go —— 基于文件锁的进程存活凭据。
//
// 职责：
//   - AcquireLock：抢占一个路径上的排他锁，返回持锁句柄（持有到 Release 或进程退出）
//   - IsLocked：探测某个路径上的锁是否被人持有（prochost.Alive 的判据）
//   - LockSupported：报告本平台是否真的能加锁
//
// 边界：
//   - 平台原语在 platform_unix.go / platform_other.go，本文件只做逻辑与错误语义
//   - 不写 PID、不做进程探活、不提供 --force：flock 由内核在进程终止时释放
//     （正常退出/panic/SIGKILL/掉电皆然），不存在陈旧锁
//   - 不跨机器：flock 是本机语义
//
// 本文件由 internal/agentd/flock_unix.go / flock_other.go（B34）上移而来，
// 因为拆 tmux 后「文件锁」同时是 agentd 单实例保护与 executor 存活判定的基础，
// 两处各写一份是重复造轮子。agentd 侧的 AcquireDataDirLock 现在是本 API 的调用方。
package prochost

import (
	"errors"
	"fmt"
	"os"
)

// ErrLockHeld 表示锁已被其他进程持有（与真正的 IO 故障区分开：两者该给用户的
// 信息完全不同，调用方靠 errors.Is 判别，禁止按错误文本判）。
var ErrLockHeld = errors.New("锁已被其他进程持有")

// Lock 是一个已持有的文件锁，直到 Release 或进程退出。
type Lock struct{ f *os.File }

// LockSupported 报告本平台是否真的能加锁。
//
// 为什么要暴露：非 unix 平台上加锁是空操作，调用方需要据此打 Warn 明说
// 「保护未生效」，而不是让人误以为锁住了。
func LockSupported() bool { return lockSupported }

// AcquireLock 对 path 取非阻塞排他锁（文件不存在则以 0600 创建）。
//
// 返回：
//   - 持锁句柄；调用方须持有到不再需要为止
//   - 锁已被他人持有时返回包装了 ErrLockHeld 的错误
//   - 其他失败（打不开、文件系统不支持 flock）返回普通错误
//
// 注意：锁挂在「打开的文件描述」上，不在路径上——`rm` 掉锁文件不能解锁；
// 锁 fd 也不会被子进程继承（Go 的 exec 只传 ExtraFiles），因此锁精确代表本进程。
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件 %s: %w", path, err)
	}
	if err := flockExclusiveNB(f); err != nil {
		f.Close()
		if isLockContended(err) {
			return nil, fmt.Errorf("抢占锁 %s: %w", path, ErrLockHeld)
		}
		return nil, fmt.Errorf("给 %s 加锁: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release 释放锁。重复调用安全（第二次直接返回 nil）。
//
// 生产侧可有可无——进程退出内核即释放；保留它是为了 defer 的习惯写法，
// 以及让测试能验证「释放后可重新获取」。
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close() // 关闭 fd 即释放 flock，无需显式 LOCK_UN
	l.f = nil
	if err != nil {
		return fmt.Errorf("释放锁 %s: %w", l.f.Name(), err)
	}
	return nil
}

// IsLocked 报告 path 上的排他锁当前是否被某个进程持有。
//
// 实现：试着非阻塞抢锁——抢到说明没人持有（随即释放），撞锁说明有人持有。
// 文件不存在视为无人持有（返回 false，且**不建文件**：探测不应有副作用）。
func IsLocked(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("打开锁文件 %s: %w", path, err)
	}
	defer f.Close()
	if err := flockExclusiveNB(f); err != nil {
		if isLockContended(err) {
			return true, nil
		}
		return false, fmt.Errorf("试锁 %s: %w", path, err)
	}
	return false, nil // 抢到了说明本来没人持有；defer 的 Close 会解锁
}
```

> `Release` 里 `l.f = nil` 之后又在错误分支引用 `l.f.Name()` 会 panic——实现时
> 先把 `path := l.f.Name()` 存下来再置 nil（`internal/agentd/lock.go` 的既有实现
> 正是这么写的，照它的顺序来）。

- [ ] **Step 7: 让 agentd 的 DataDir 锁复用 prochost，删掉重复实现**

Run:
```bash
rm internal/agentd/flock_unix.go internal/agentd/flock_other.go
```

改写 `internal/agentd/lock.go`：
- import 加 `"errors"` 与 `"github.com/xushixin/handoff/internal/prochost"`
- `DataDirLock` 的字段 `f *os.File` 换成 `l *prochost.Lock`
- `flockSupported` 的引用换成 `prochost.LockSupported()`
- 加锁段换成：
```go
	l, err := prochost.AcquireLock(path)
	if err != nil {
		if errors.Is(err, prochost.ErrLockHeld) {
			log.Error("数据目录已被另一个 agentd 占用，拒绝启动",
				"data_dir", dataDir, "path", path)
			return nil, fmt.Errorf(lockHeldMsg, dataDir)
		}
		// 非撞锁的加锁失败（如打不开文件、文件系统不支持 flock）：这是环境问题，
		// 与「已被占用」是两码事，不能套用那段指引文案误导用户
		log.Error("获取单实例锁失败", "path", path, "cause", err)
		return nil, err
	}
```
- `Release` 委托给 `l.l.Release()`，日志保持原样
- **把 `lockHeldMsg` 里的 tmux 表述改掉**：现文案含「同一批 worktree 与 tmux 会话」，
  拆掉 tmux 后这句话就是错的。改为「同一批 worktree 与执行者进程」

Run: `go test ./internal/agentd/ -run 'Lock' -v`
Expected: PASS —— `lock_test.go` 的六条用例（含 `TestAcquireDataDirLockErrorIsActionable`）
原样通过，说明公开行为没变

- [ ] **Step 8: 运行测试确认通过**

Run: `go test ./internal/prochost/ -run 'TestSpawnDetached|TestAlive|TestKillIsNoOp|TestCreateInputChannel' -v`
Expected: PASS（4 条全过）

- [ ] **Step 9: 加关键节点日志**

确认 `prochost.go` 已包含（Step 3 的代码里已写，此步是核对）：
- `Alive`：探测出错时 Debug（带 lock 路径 + cause）——高频调用，不用 Info 免刷屏
- `Kill`：锁已释放走「无需回收」Info（带 pid/lock）；确认存活后 Info「回收执行者进程组」；`killGroup` 失败 Error（带 pid + cause）

`lock.go` **有意不打日志**：它是被 agentd 与 prochost 两处调用的纯原语，
日志由各自调用方带着自己的上下文打（agentd 打 data_dir，prochost.Kill 打 pid）——
在原语层打会变成没有上下文的重复行。`internal/agentd/lock.go` 归并后的四条日志
（不支持锁 Warn / 打开失败 Error / 撞锁 Error / 取得锁 Info）必须一条不少地保留。

补充：`spawnDetached` 成功后不在本层打日志（调用方 `Start` 打，带任务上下文更有用）。
用 `slog`，**禁止 `fmt.Printf`**。

- [ ] **Step 10: 加注释自检**

逐项确认（Step 3–7 的代码已写全，此步是核对）：
- 四个新文件（prochost.go / platform_unix.go / platform_other.go / lock.go）都有文件头注释（职责 + 边界）
- `lock.go` 文件头写明「由 agentd 上移而来、为什么要归并」
- `platform_other.go` 文件头写明「两类退化语义不同」与 `_other` 命名的 why
- `internal/agentd/lock.go` 的 `lockHeldMsg` 已无 tmux 表述
- 包注释含「为什么存活判定用文件锁而不是 pid」
- `spawnDetached` 含「为什么用 Setsid」「为什么 stdio 置 nil」「不脱离 cgroup」三条 why
- `createInputChannel` 含「残留普通文件为何必须显式失败」的症状描述
- `waitInputReader` 含 ENXIO 竞态的 why
- Windows 骨架含「为什么 A 期只留骨架」

- [ ] **Step 11: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./internal/prochost/`
Expected: `go test ./...` 全绿（**含 `internal/agentd` 的 lock 用例**——它们验证归并后
公开行为没变）；Windows 侧本任务只要求 prochost 包能编译，其余包在 Task 3 收口

- [ ] **Step 12: Commit**

```bash
git add internal/prochost internal/agentd/lock.go
git rm internal/agentd/flock_unix.go internal/agentd/flock_other.go
git commit -m "feat(prochost): 跨平台进程承载原语（detach/文件锁存活/进程组回收/FIFO），agentd 单实例锁归并复用"
```

---

### Task 2: shim 主体与 Start 门面

**Files:**
- Create: `internal/prochost/shim.go`
- Create: `cmd/shim.go`
- Test: `internal/prochost/shim_test.go`
- Modify: `internal/prochost/prochost.go`（追加 `Start`）

**Interfaces:**
- Consumes: Task 1 的 `Spec`、`Handle`、`spawnDetached`、`AcquireLock`、`waitInputReader`
- Produces:
  - `func Start(spec Spec, selfExe string) (Handle, error)` —— 写 spec.json（0600）后 detached 拉起 `selfExe _shim --spec <path>`，返回 Handle
  - `func RunShim(specPath string) error` —— shim 主体，由 `handoff _shim` 调用
  - `const SentinelPrefix = "\"type\":\"handoff_exit\""` —— 死亡哨兵的类型标记，adapter 扫描 out.jsonl 时复用
  - `func ChildPID(infoPath string) (int, error)` —— 读 proc.json 里 shim 补写的 child_pid（诊断用）

- [ ] **Step 1: 写失败测试**

创建 `internal/prochost/shim_test.go`：

```go
package prochost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSpec 把 spec 落到 dir/spec.json 并返回路径（测试辅助）。
func writeSpec(t *testing.T, dir string, s Spec) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("序列化 spec 失败: %v", err)
	}
	p := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("写 spec 失败: %v", err)
	}
	return p
}

// baseSpec 造一个最小可用 spec：子进程按 script 跑一段 sh。
func baseSpec(dir, script string) Spec {
	return Spec{
		Argv:     []string{"/bin/sh", "-c", script},
		Dir:      dir,
		Env:      []string{"PATH=/usr/bin:/bin"},
		Stdout:   filepath.Join(dir, "out.jsonl"),
		Stderr:   filepath.Join(dir, "err.log"),
		LockPath: filepath.Join(dir, "proc.lock"),
		InfoPath: filepath.Join(dir, "proc.json"),
		Sentinel: true,
	}
}

// waitFile 等文件内容出现 want，超时即失败（测试辅助）。
func waitFile(t *testing.T, path, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), want) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 %s 出现 %q 超时，实得 %q", path, want, b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunShimWritesSentinelWithExitCode(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `printf 'hello\n'; exit 7`)
	specPath := writeSpec(t, dir, spec)

	if err := RunShim(specPath); err != nil {
		t.Fatalf("RunShim 返回错误: %v", err)
	}
	got, err := os.ReadFile(spec.Stdout)
	if err != nil {
		t.Fatalf("读 stdout 失败: %v", err)
	}
	if !strings.Contains(string(got), "hello") {
		t.Fatalf("子进程 stdout 未落盘，实得 %q", got)
	}
	// 哨兵必须带真实退出码——它是 adapter 判死的唯一可靠信号
	if !strings.Contains(string(got), `"type":"handoff_exit"`) ||
		!strings.Contains(string(got), `"code":7`) {
		t.Fatalf("哨兵缺失或退出码不对，实得 %q", got)
	}
}

func TestRunShimRecordsChildPID(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `exit 0`)
	specPath := writeSpec(t, dir, spec)
	if err := os.WriteFile(spec.InfoPath, []byte(`{"handle":{"pid":1,"lock_path":"x"}}`), 0o600); err != nil {
		t.Fatalf("预写 proc.json 失败: %v", err)
	}

	if err := RunShim(specPath); err != nil {
		t.Fatalf("RunShim 返回错误: %v", err)
	}
	pid, err := ChildPID(spec.InfoPath)
	if err != nil {
		t.Fatalf("读 child_pid 失败: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("shim 必须补写真实 child_pid，实得 %d", pid)
	}
	// 补写不能破坏 proc.json 里已有的字段（adapter 先写 handle，shim 后补 child_pid）
	b, _ := os.ReadFile(spec.InfoPath)
	if !strings.Contains(string(b), `"lock_path":"x"`) {
		t.Fatalf("shim 补写 child_pid 时抹掉了已有字段，实得 %q", b)
	}
}

func TestRunShimHoldsInputChannelAndFeedsChild(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "in.fifo")
	if err := CreateInputChannel(fifo); err != nil {
		t.Fatalf("建 fifo 失败: %v", err)
	}
	// 子进程从 stdin 读一行就回显退出；stdin 必须是 shim 持有的 fifo
	spec := baseSpec(dir, `read line; printf 'got:%s\n' "$line"`)
	spec.InputCh = fifo
	specPath := writeSpec(t, dir, spec)

	done := make(chan error, 1)
	go func() { done <- RunShim(specPath) }()

	// shim 持有读端后，写端才能以 O_NONBLOCK 打开（否则 ENXIO）
	if _, err := WaitInputReader(fifo, 3*time.Second); err != nil {
		t.Fatalf("shim 未在时限内持有 fifo 读端: %v", err)
	}
	f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("打开 fifo 写端失败: %v", err)
	}
	if _, err := f.WriteString("ping\n"); err != nil {
		t.Fatalf("写 fifo 失败: %v", err)
	}
	f.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunShim 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunShim 未在 5s 内结束——子进程可能没读到 stdin")
	}
	got, _ := os.ReadFile(spec.Stdout)
	if !strings.Contains(string(got), "got:ping") {
		t.Fatalf("子进程未从 fifo 收到输入，实得 %q", got)
	}
}

func TestRunShimRefusesWhenLockAlreadyHeld(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `exit 0`)
	specPath := writeSpec(t, dir, spec)
	// 先占住锁，模拟「同一任务已有 shim 在跑」——绝不能起第二个
	// （两个 executor 抢同一会话是数据损坏级后果，见 claudecode Resume 的冷恢复互斥）
	held, err := AcquireLock(spec.LockPath)
	if err != nil {
		t.Fatalf("预占锁失败: %v", err)
	}
	defer held.Release()

	if err := RunShim(specPath); err == nil {
		t.Fatal("锁已被持有时 RunShim 必须失败，实得 nil")
	}
}

// TestHelperShimStarter 不是测试：被 TestSentinelWrittenAfterParentDeath 以子进程
// 调用，负责 Start 一个 shim 后立刻退出，制造「agentd 已离线」。
func TestHelperShimStarter(t *testing.T) {
	if os.Getenv(helperEnv) != "shimstarter" {
		t.Skip("非 helper 调用")
	}
	dir := os.Getenv("PROCHOST_TEST_DIR")
	spec := baseSpec(dir, `sleep 1; exit 3`)
	specPath := writeSpec(t, dir, spec)
	h, err := Start(spec, os.Args[0], "-test.run", "^TestHelperShimEntry$", "-test.v=false")
	if err != nil {
		os.Stderr.WriteString("Start 失败: " + err.Error())
		os.Exit(2)
	}
	_ = specPath
	os.Stdout.WriteString(strconv.Itoa(h.PID))
	os.Exit(0)
}

// TestHelperShimEntry 不是测试：作为 Start 拉起的「shim 可执行体」入口，
// 把 --spec 后面的路径交给 RunShim。生产里这个角色由 handoff _shim 承担。
func TestHelperShimEntry(t *testing.T) {
	if os.Getenv(helperEnv) != "shimentry" {
		t.Skip("非 helper 调用")
	}
	if err := RunShim(os.Getenv("PROCHOST_TEST_SPEC")); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestSentinelWrittenAfterParentDeath(t *testing.T) {
	dir := t.TempDir()
	spec := baseSpec(dir, `sleep 1; exit 3`)
	specPath := writeSpec(t, dir, spec)

	// 用 helper 进程扮演 agentd：Start 出 shim 后立刻退出
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHelperShimEntry$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		helperEnv+"=shimentry", "PROCHOST_TEST_SPEC="+specPath)
	h, err := startWith(cmd, spec)
	if err != nil {
		t.Fatalf("拉起 shim 失败: %v", err)
	}
	t.Cleanup(func() { _ = Kill(h) })

	// shim 在跑：锁必须被持有
	if !Alive(h) {
		t.Fatal("shim 刚起来，Alive 必须为 true")
	}
	// 子进程 1s 后退出；此刻没有任何「agentd」在 waitpid——哨兵必须由 shim 写出。
	// 这是 shim 存在的根本理由：agentd 离线期间 executor 退出的退出码不能丢。
	got := waitFile(t, spec.Stdout, `"type":"handoff_exit"`, 10*time.Second)
	if !strings.Contains(got, `"code":3`) {
		t.Fatalf("哨兵退出码不对，实得 %q", got)
	}
	// shim 退出后锁被内核释放，Alive 必须回到 false
	deadline := time.Now().Add(3 * time.Second)
	for Alive(h) {
		if time.Now().After(deadline) {
			t.Fatal("shim 已完成但 Alive 仍为 true")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

> 实现提示：上面 `TestHelperShimStarter` 里引用的 `strconv` 与 `startWith` 是本测试文件
> 需要补的两处——`strconv` 加进 import；`startWith(cmd, spec)` 是测试专用小helper，
> 直接 `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}; cmd.Start()` 后返回
> `Handle{PID: cmd.Process.Pid, LockPath: spec.LockPath}` 并 `cmd.Process.Release()`。
> 之所以测试不直接用 `Start`，是因为生产的 `Start` 固定拼 `_shim --spec`，而测试
> 二进制的入口是 `-test.run`；`Start` 自身的 argv 拼装由 Step 3 的单测覆盖。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestRunShim|TestSentinelWritten' -v`
Expected: FAIL —— `undefined: RunShim` / `undefined: Start` / `undefined: ChildPID`

- [ ] **Step 3: 实现 shim 主体**

创建 `internal/prochost/shim.go`：

```go
// shim.go —— shim 进程的主体逻辑。
//
// 职责：
//   - 持有存活锁（整个生命周期），作为 prochost.Alive 的唯一判据
//   - 打开 stdout/stderr 追加落盘文件；InputCh 非空时以 O_RDWR 持有 FIFO 读端
//   - spawn 真正的 executor，把 child_pid 补写进 proc.json
//   - wait 子进程，退出后向 stdout 追加 handoff_exit 哨兵
//
// 边界：
//   - 不认识 executor 协议、不解析输出：只做搬运与收尸
//   - 不写任务状态、不连 agentd：shim 与 agentd 之间只有文件（锁、proc.json、日志）
//
// 为什么必须有 shim（而不是 agentd 直接 detach executor）：退出哨兵需要一个
// 常驻父进程 waitpid 才能拿到。agentd 重启后，reparent 给 init 的 executor
// 已经没法被 waitpid，「agentd 离线期间 executor 退出」的退出码就永远丢了——
// 那正是恢复流程最需要知道的事。shim 用一个极轻的进程换回这个语义。
//
// 为什么 shim 以 O_RDWR 打开 FIFO：只读打开会在写端全部关闭时收到 EOF，
// executor 的 stdin 随即关闭；O_RDWR 让 shim 自己同时是写端，FIFO 永不 EOF。
// 这是旧 sh 脚本 `exec 3<> in.fifo` 的等价手法。
package prochost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// SentinelPrefix 是死亡哨兵行的类型标记，adapter 扫 stdout 判死时匹配它。
const SentinelPrefix = `"type":"handoff_exit"`

// RunShim 是 shim 进程的入口：读 spec、持锁、拉起 executor、收尸写哨兵。
//
// 参数：specPath 为 Start 落盘的 spec.json 路径
//
// 返回：
//   - 锁已被持有（同任务已有 shim 在跑）、spec 不可读、子进程拉不起来时返回错误
//   - 子进程本身以非零码退出**不算错误**：那是正常业务结果，经哨兵传达
//
// 注意：本函数会阻塞到子进程退出，调用方（handoff _shim）随后即可退出。
func RunShim(specPath string) error {
	spec, err := readSpec(specPath)
	if err != nil {
		return err
	}
	l := log().With("lock", spec.LockPath)

	// 存活锁必须最先拿：拿不到说明同任务已有 shim 在跑，起第二个会让两个 executor
	// 抢同一会话（数据损坏级后果，与 claudecode 冷恢复互斥同一道理）
	lock, err := AcquireLock(spec.LockPath)
	if err != nil {
		l.Error("抢占存活锁失败，同任务可能已有 shim 在跑", "cause", err)
		return fmt.Errorf("shim 抢锁: %w", err)
	}
	defer lock.Release()

	stdout, err := openAppend(spec.Stdout)
	if err != nil {
		l.Error("打开 stdout 落盘文件失败", "path", spec.Stdout, "cause", err)
		return err
	}
	defer stdout.Close()
	stderr := stdout
	if spec.Stderr != "" && spec.Stderr != spec.Stdout {
		stderr, err = openAppend(spec.Stderr)
		if err != nil {
			l.Error("打开 stderr 落盘文件失败", "path", spec.Stderr, "cause", err)
			return err
		}
		defer stderr.Close()
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if spec.InputCh != "" {
		// O_RDWR 而非 O_RDONLY：见文件头注释的 why（FIFO 永不 EOF）
		fifo, ferr := os.OpenFile(spec.InputCh, os.O_RDWR, 0)
		if ferr != nil {
			l.Error("打开输入通道失败", "path", spec.InputCh, "cause", ferr)
			return fmt.Errorf("打开输入通道 %s: %w", spec.InputCh, ferr)
		}
		defer fifo.Close()
		cmd.Stdin = fifo
	}

	// env 只打 key 名：值可能含凭据（代理 URL 里的 user:pass、API key）
	l.Info("shim 拉起执行者进程", "bin", spec.Argv[0], "dir", spec.Dir,
		"env_keys", envKeys(spec.Env), "input_ch", spec.InputCh != "")
	if err := cmd.Start(); err != nil {
		l.Error("拉起执行者进程失败", "bin", spec.Argv[0], "cause", err)
		return fmt.Errorf("拉起 %s: %w", spec.Argv[0], err)
	}
	childPID := cmd.Process.Pid
	if err := recordChildPID(spec.InfoPath, childPID); err != nil {
		// 只是诊断信息，写不进去不值得杀掉已经起来的执行者
		l.Warn("补写 child_pid 失败，不影响执行", "info", spec.InfoPath, "cause", err)
	}
	l.Info("执行者进程已启动", "child_pid", childPID)

	code := 0
	if werr := cmd.Wait(); werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			code = ee.ExitCode()
		} else {
			l.Error("等待执行者进程失败", "child_pid", childPID, "cause", werr)
			code = -1
		}
	}
	if spec.Sentinel {
		if _, err := fmt.Fprintf(stdout, "{%s,\"code\":%d}\n", SentinelPrefix, code); err != nil {
			// 哨兵写不出去 = adapter 永远发现不了死亡，这是必须 Error 的严重情况
			l.Error("写死亡哨兵失败，恢复流程将无法判死", "child_pid", childPID, "cause", err)
		}
	}
	l.Info("执行者进程已退出", "child_pid", childPID, "code", code, "sentinel", spec.Sentinel)
	return nil
}

// ChildPID 读取 proc.json 里 shim 补写的 child_pid（诊断用）。
func ChildPID(infoPath string) (int, error) {
	m, err := readInfoMap(infoPath)
	if err != nil {
		return 0, err
	}
	v, ok := m["child_pid"].(float64)
	if !ok {
		return 0, fmt.Errorf("%s 缺 child_pid 字段", infoPath)
	}
	return int(v), nil
}

// readSpec 读取并校验 spec.json。
func readSpec(path string) (Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("读 spec %s: %w", path, err)
	}
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return Spec{}, fmt.Errorf("解析 spec %s: %w", path, err)
	}
	if len(s.Argv) == 0 || s.LockPath == "" || s.Stdout == "" {
		return Spec{}, fmt.Errorf("spec %s 字段不完整（argv/lock_path/stdout 必填）", path)
	}
	return s, nil
}

// openAppend 以追加模式打开落盘文件（不存在则以 0600 创建）。
func openAppend(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开 %s: %w", path, err)
	}
	return f, nil
}

// recordChildPID 把 child_pid 合并进已存在的 proc.json。
//
// 为什么是「合并」而不是「覆盖」：proc.json 由 adapter 先写（Handle、session_id 等），
// shim 只补一个字段。整份覆盖会把 adapter 写的恢复凭据抹掉，重启后无法恢复。
func recordChildPID(infoPath string, pid int) error {
	m, err := readInfoMap(infoPath)
	if err != nil {
		m = map[string]any{}
	}
	m["child_pid"] = pid
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("序列化 proc.json: %w", err)
	}
	if err := os.WriteFile(infoPath, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", infoPath, err)
	}
	return nil
}

// readInfoMap 把 proc.json 读成松散 map（便于只改一个字段而不认识其余结构）。
func readInfoMap(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	return m, nil
}

// envKeys 提取 KEY=VALUE 列表里的 key（日志用；值绝不出现在日志里）。
func envKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, kv := range env {
		if i := indexByte(kv, '='); i > 0 {
			keys = append(keys, kv[:i])
		}
	}
	return keys
}

// indexByte 是 strings.IndexByte 的本地别名，避免为一处调用引入 strings 依赖。
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 4: 实现 Start 门面**

在 `internal/prochost/prochost.go` 末尾追加：

```go
// Start 以 detached 方式拉起 shim，由 shim 承载 spec 描述的执行者进程。
//
// 参数：
//   - spec: 启动描述；LockPath/InfoPath/Stdout 必填，Argv[0] 必须是绝对路径
//   - selfExe: handoff 自身可执行文件路径（os.Executable() 的结果）
//   - extraArgs: 附加给 selfExe 的参数；生产传空，测试用它指向测试二进制的 shim 入口
//
// 返回：
//   - Handle（PID 为 shim 的 pid，同时是进程组 id）；spec 落盘失败或 fork 失败时返回错误
//
// 注意：
//   - 返回只代表 shim 已被 fork，**不代表它已持锁或已打开 FIFO**。调用方若要
//     投递输入，必须先 WaitInputReader；若要判存活，必须轮询 Alive 而非立即断言
//   - spec.json 以 0600 落在 InfoPath 同目录：它含完整 env（可能有凭据），
//     权限不能放宽
func Start(spec Spec, selfExe string, extraArgs ...string) (Handle, error) {
	specPath := filepath.Join(filepath.Dir(spec.InfoPath), "spec.json")
	b, err := json.Marshal(spec)
	if err != nil {
		return Handle{}, fmt.Errorf("序列化 spec: %w", err)
	}
	if err := os.WriteFile(specPath, b, 0o600); err != nil {
		return Handle{}, fmt.Errorf("写 spec %s: %w", specPath, err)
	}
	argv := append([]string{selfExe}, extraArgs...)
	argv = append(argv, "_shim", "--spec", specPath)
	pid, err := spawnDetached(argv, spec.Dir)
	if err != nil {
		log().Error("拉起 shim 失败", "spec", specPath, "cause", err)
		return Handle{}, err
	}
	log().Info("shim 已拉起", "pid", pid, "bin", spec.Argv[0], "spec", specPath)
	return Handle{PID: pid, LockPath: spec.LockPath}, nil
}
```

同时把 `prochost.go` 的 import 补成 `encoding/json` / `fmt` / `log/slog` / `os` / `path/filepath` / `time`。

- [ ] **Step 5: 实现 `handoff _shim` 子命令**

创建 `cmd/shim.go`：

```go
// 本文件实现隐藏子命令 handoff _shim：执行者进程的承载壳。
//
// 职责：
//   - 解析 --spec，把控制权交给 prochost.RunShim（阻塞到执行者退出）
//
// 边界：
//   - 不做任何业务判断：全部逻辑在 prochost.RunShim 里，本文件只是 cobra 包装
//   - 不面向用户：Hidden=true，不出现在 help 里。它由 agentd 自己拉起，
//     人手动跑没有意义（缺 spec.json 就什么都做不了）
package cmd

import (
	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/prochost"
)

// shimSpecPath 是 --spec 的绑定变量。
var shimSpecPath string

// shimCmd 是执行者进程的承载壳，由 agentd 经 prochost.Start 拉起。
var shimCmd = &cobra.Command{
	Use:    "_shim",
	Short:  "执行者进程承载壳（内部使用）",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return prochost.RunShim(shimSpecPath)
	},
}

func init() {
	shimCmd.Flags().StringVar(&shimSpecPath, "spec", "", "spec.json 路径（必填）")
	_ = shimCmd.MarkFlagRequired("spec")
	rootCmd.AddCommand(shimCmd)
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/prochost/ -run 'TestRunShim|TestSentinelWritten' -v`
Expected: PASS（5 条全过，含 `TestSentinelWrittenAfterParentDeath`）

- [ ] **Step 7: 加关键节点日志**

核对 `shim.go` 已包含（Step 3 已写）：
- 抢锁失败 Error（带 lock + cause）
- stdout/stderr/FIFO 打开失败各自 Error（带 path + cause）
- 拉起前 Info（bin/dir/**env_keys**/是否有输入通道）——值不入日志
- 拉起失败 Error；启动成功 Info（child_pid）
- 哨兵写失败 Error（明确写「恢复流程将无法判死」）
- 退出 Info（child_pid + code + sentinel）——**成功路径也有日志，不留静默**

核对 `Start` 已包含：拉起失败 Error、成功 Info（pid/bin/spec）。

- [ ] **Step 8: 加注释自检**

- `shim.go` 文件头含职责 + 边界 + 两条 why（为什么必须有 shim、为什么 O_RDWR 开 FIFO）
- `RunShim` 文档注释写清「子进程非零退出不算错误」
- `recordChildPID` 含「为什么是合并而不是覆盖」
- `Start` 文档注释写清「返回不代表已持锁/已开 FIFO」
- `cmd/shim.go` 文件头含「为什么 Hidden」

- [ ] **Step 9: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./internal/prochost/ ./cmd/`
Expected: `go test` 全绿；Windows 编译中 `./cmd/` 可能仍因 claudecode 依赖失败，
prochost 必须通过（cmd 的 Windows 编译在 Task 7 收口）

- [ ] **Step 10: Commit**

```bash
git add internal/prochost cmd/shim.go
git commit -m "feat(prochost): shim 主体与 Start 门面，agentd 离线期间退出码不再丢失"
```

---

### Task 3: claude adapter 迁移到 prochost

**Files:**
- Modify: `internal/executor/claudecode/proc.go`（整体重写：删 sh 脚本/tmux，改 prochost）
- Modify: `internal/executor/claudecode/reap.go`
- Modify: `internal/executor/claudecode/resume.go`（存活判据换 prochost.Alive）
- Modify: `internal/executor/claudecode/probe.go`（重建 Proc 的字段 + 审核者可见的 Note 文本）
- Modify: `internal/executor/claudecode/adapter.go`（Proc 字段适配 + 两处「tmux attach」文案）
- Test: `internal/executor/claudecode/proc_test.go`、`internal/executor/claudecode/reap_test.go`、`internal/executor/claudecode/start_ordering_test.go`（缝替换）

**Interfaces:**
- Consumes: `prochost.Spec`、`prochost.Handle`、`prochost.Start`、`prochost.Alive`、`prochost.Kill`、`prochost.CreateInputChannel`、`prochost.WaitInputReader`、`prochost.SentinelPrefix`
- Produces:
  - `type Proc struct { Handle prochost.Handle; TaskDir, SessionID string }`
  - `func StartProc(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error)`（签名不变）
  - `func (p *Proc) Alive() bool` / `func (p *Proc) Kill() error` / `func (p *Proc) WriteInput(text string) error`（签名不变）
  - `type procInfo struct { Handle prochost.Handle; ChildPID int; SessionID string; Offset int64 }`，文件名常量 `procInfoFileName = "proc.json"`
  - 包级测试缝 `var startProcHost = prochost.Start`（替代 `tmuxLaunch`/`tmuxKill`/`tmuxHasSession` 三个缝）

- [ ] **Step 1: 写失败测试**

在 `internal/executor/claudecode/proc_test.go` 追加（并删除引用 `tmuxLaunch`/`tmuxKill`/`tmuxHasSession` 的旧用例）：

```go
// TestClaudeArgvHasNoShell 钉死「argv 直传、不经任何 shell」：
// 旧实现把命令拼进 sh 脚本，值里的 $ 会被二次展开、引号会改变语义。
// 换成 argv 后这类问题从根上消失——但只有断言 argv 结构才能防止回退。
func TestClaudeArgvHasNoShell(t *testing.T) {
	argv := claudeArgv(StartProcReq{
		SessionID:    "sess-1",
		Model:        "opus",
		SettingsPath: "/tmp/a b/settings.json", // 含空格：旧实现必须引号转义，新实现天然安全
		MCPPath:      "/tmp/mcp.json",
	})
	if argv[0] != "claude" {
		t.Fatalf("argv[0] 必须是 claude，实得 %q", argv[0])
	}
	for _, a := range argv {
		if strings.Contains(a, "'") || strings.Contains(a, "\\") {
			t.Fatalf("argv 不应含 shell 引号/转义残留: %q", a)
		}
	}
	joined := strings.Join(argv, "\x00")
	for _, want := range []string{
		"--session-id\x00sess-1", "--model\x00opus",
		"--settings\x00/tmp/a b/settings.json",
		"--permission-prompt-tool\x00mcp__handoff__ask",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv 缺 %q，实得 %v", want, argv)
		}
	}
	// Resume 与非 Resume 语义相反，写错的表现是「日志说恢复成功、模型什么都不记得」
	rargv := claudeArgv(StartProcReq{SessionID: "sess-1", Resume: true})
	rjoined := strings.Join(rargv, "\x00")
	if !strings.Contains(rjoined, "--resume\x00sess-1") ||
		strings.Contains(rjoined, "--session-id") {
		t.Fatalf("Resume=true 必须用 --resume 且不带 --session-id，实得 %v", rargv)
	}
}

// TestStartProcWritesProcInfoBeforeSpawn 钉死写前置时序：
// proc.json 必须在 Start 之前落盘，否则 Start 成功但进程记录缺失时 Reap 无据可查。
func TestStartProcWritesProcInfoBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	var infoExistedAtSpawn bool
	orig := startProcHost
	startProcHost = func(spec prochost.Spec, selfExe string, extra ...string) (prochost.Handle, error) {
		_, err := os.Stat(filepath.Join(dir, procInfoFileName))
		infoExistedAtSpawn = err == nil
		return prochost.Handle{PID: 4242, LockPath: spec.LockPath}, nil
	}
	t.Cleanup(func() { startProcHost = orig })

	// FIFO 读端由假 shim 不会打开，把等待超时调到毫秒级快速走完
	origTimeout := fifoReaderTimeout
	fifoReaderTimeout = 10 * time.Millisecond
	t.Cleanup(func() { fifoReaderTimeout = origTimeout })

	_, _ = StartProc(context.Background(), StartProcReq{
		RepoPath: dir, TaskID: "abcdefgh12", TaskDir: dir, SessionID: "s1",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !infoExistedAtSpawn {
		t.Fatal("proc.json 必须在拉起 shim 之前落盘（写前置时序）")
	}
}

// TestProcAliveDelegatesToLock 钉死「存活只看锁」：
// 旧实现看 tmux has-session，而第二窗口的 tail -f 会吊着会话导致假存活。
func TestProcAliveDelegatesToLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "proc.lock")
	p := &Proc{Handle: prochost.Handle{PID: os.Getpid(), LockPath: lock}, TaskDir: dir}

	if p.Alive() {
		t.Fatal("锁无人持有时必须判死")
	}
	// 写入死亡哨兵不应影响「锁被持有 = 活着」之外的判定顺序：
	// claude 的完整判据是「锁被持有 且 无哨兵」，这里只验证锁这一半
	if err := os.WriteFile(filepath.Join(dir, outFileName),
		[]byte(`{"type":"handoff_exit","code":0}`+"\n"), 0o600); err != nil {
		t.Fatalf("造哨兵失败: %v", err)
	}
	if p.Alive() {
		t.Fatal("有哨兵时必须判死")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run 'TestClaudeArgv|TestStartProcWritesProcInfo|TestProcAliveDelegates' -v`
Expected: FAIL —— `undefined: claudeArgv` / `undefined: startProcHost` / `Proc.Handle` 不存在

- [ ] **Step 3: 重写 proc.go**

替换 `internal/executor/claudecode/proc.go` 的文件头注释与实现要点：

文件头改为：
```go
// proc.go —— Claude Code 进程生命周期管理。
//
// 职责：
//   - 组 claude 的 argv（headless 双向流式），经 prochost 以 detached 方式拉起
//   - 经命名管道 in.fifo 投递指令；stdout 落 out.jsonl，stderr 落 claude.log
//   - 死亡判定（out.jsonl 末尾的 handoff_exit 哨兵 + 存活锁）与凭据持久化（proc.json）
//
// 边界：
//   - 不解析事件：out.jsonl 的解析在 stream.go
//   - 不做权限裁决：socket 服务端在 perm.go
//   - 不关心进程怎么脱离 agentd：那是 prochost 的事
//
// 为什么进程经 prochost 而不是 agentd 直接 fork：agentd 重启或崩溃时子进程
// 若未脱离会话会被一并回收，正在执行的任务无辜中断；prochost 的 shim 以新会话
// 拉起并持有存活锁，生命周期与 agentd 解耦——agentd 重启后靠 Alive() 探测重连。
```

删除：`writeRunScript`、`tmuxArgs`、`startRenderTailWindow`、`tmuxKill`、`tmuxHasSession`、
`tmuxLaunch`、`ensureFIFO`、`waitFIFOReader`、`sentinelPrefix`（改用 `prochost.SentinelPrefix`），
以及 `shellq` 与 `syscall` 的 import。

常量改为：
```go
const (
	fifoFileName     = "in.fifo"    // Send 投递 stream-json user message
	outFileName      = "out.jsonl"  // claude stdout 原样落盘（adapter 按 offset 续读）
	stderrFileName   = "claude.log" // claude stderr，启动失败/死亡诊断来源
	renderFileName   = "render.log" // 模型回合文本增量（render 流式 endpoint 的数据源）
	procInfoFileName = "proc.json"  // 恢复凭据：prochost.Handle / session_id / offset
	lockFileName     = "proc.lock"  // shim 存活锁（prochost.Alive 的唯一判据）
	sockFileName     = "perm.sock"  // 权限裁决 socket
)
```

新增 `claudeArgv`：
```go
// claudeArgv 组 claude 的完整 argv。
//
// 为什么返回 argv 而不是命令串：argv 直接交给 execve，不经任何 shell——
// 旧实现把它拼进 sh 脚本，路径里的空格要引号、值里的 $ 会被二次展开，
// 这类问题在 argv 形态下从根上不存在。
//
// 注意：Resume=true 用 --resume（载入既有会话），false 用 --session-id
// （新建该 id 的会话）。两者语义相反，写错的表现是「日志说恢复成功、模型
// 却什么都不记得」——测试 TestClaudeArgvHasNoShell 钉死了这条。
func claudeArgv(req StartProcReq) []string {
	argv := []string{
		"claude", "-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose", "--include-partial-messages",
	}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.Resume {
		argv = append(argv, "--resume", req.SessionID)
	} else {
		argv = append(argv, "--session-id", req.SessionID)
	}
	argv = append(argv, "--setting-sources", "user,project")
	argv = append(argv, "--settings", req.SettingsPath)
	argv = append(argv, "--mcp-config", req.MCPPath)
	argv = append(argv, "--permission-prompt-tool", "mcp__handoff__ask")
	return argv
}
```

`Proc` 与测试缝：
```go
// Proc 描述一个运行中的 claude 进程句柄。
//
// 字段说明：
//   - Handle: prochost 句柄（shim pid + 存活锁路径），存活与回收都靠它
//   - TaskDir: 任务目录（fifo/out.jsonl/claude.log/proc.json 都在其中）
//   - SessionID: claude --session-id（agentd 生成，写进 proc.json 供恢复）
type Proc struct {
	Handle    prochost.Handle
	TaskDir   string
	SessionID string
}

// startProcHost 是 prochost.Start 的测试缝（替代旧的 tmuxLaunch 缝）：
// 测试替换它断言 spec 内容与写前置时序，绕开真实 shim 与 claude 二进制。
var startProcHost = prochost.Start
```

`StartProc` 主体改为：
```go
func StartProc(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) {
	l := log.With("task", req.TaskID)
	if len(req.Env) > 0 {
		// 只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）
		l.Info("注入 env 变量到 claude 进程", "keys", envKeys(req.Env), "count", len(req.Env))
	}
	fifoPath := filepath.Join(req.TaskDir, fifoFileName)
	if err := prochost.CreateInputChannel(fifoPath); err != nil {
		l.Error("创建输入通道失败", "path", fifoPath, "cause", err)
		return nil, err
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		l.Error("claude 未安装", "cause", err)
		return nil, fmt.Errorf("claude 未安装: %w", err)
	}
	selfExe, err := os.Executable()
	if err != nil {
		l.Error("取 handoff 自身路径失败（shim 无法拉起）", "cause", err)
		return nil, fmt.Errorf("取自身可执行路径: %w", err)
	}
	argv := claudeArgv(req)
	argv[0] = bin // LookPath 解析结果：prochost 不做 PATH 查找

	lockPath := filepath.Join(req.TaskDir, lockFileName)
	infoPath := filepath.Join(req.TaskDir, procInfoFileName)
	// 写前置：proc.json 必须先于进程存在，否则 Start 成功而记录缺失时 Reap 无据可查。
	// Handle 此刻 PID 未知，先占位；Start 返回后补真实 pid
	if err := writeProcInfo(req.TaskDir, &procInfo{
		Handle: prochost.Handle{LockPath: lockPath}, SessionID: req.SessionID,
	}); err != nil {
		l.Error("写恢复凭据失败", "cause", err)
		return nil, err
	}
	spec := prochost.Spec{
		Argv: argv, Dir: req.RepoPath, Env: append(os.Environ(), req.Env...),
		Stdout: filepath.Join(req.TaskDir, outFileName),
		Stderr: filepath.Join(req.TaskDir, stderrFileName),
		InputCh: fifoPath, LockPath: lockPath, InfoPath: infoPath,
		Sentinel: true, // claude 没有 HTTP 探活面，哨兵是唯一可靠的死亡信号
	}
	l.Info("启动 claude 执行者", "bin", bin, "repo", req.RepoPath, "resume", req.Resume)
	handle, err := startProcHost(spec, selfExe)
	if err != nil {
		l.Error("拉起 claude 执行者失败", "cause", err)
		return nil, err
	}
	p := &Proc{Handle: handle, TaskDir: req.TaskDir, SessionID: req.SessionID}
	// 等 shim 在 in.fifo 上建立读端：Start 返回只代表 shim 已 fork，
	// 而 WriteInput 以 O_NONBLOCK 打开 fifo，读端未就绪会 ENXIO（见 prochost 的 why）
	elapsed, err := prochost.WaitInputReader(fifoPath, fifoReaderTimeout)
	if err != nil {
		l.Error("shim 未在时限内打开 in.fifo", "cause", err, "log_tail", claudeLogTail(req.TaskDir))
		// shim 已起来，必须自行回收：调用方 rollback 依赖 r.proc，而这里返回 nil
		if kerr := p.Kill(); kerr != nil {
			l.Warn("回收读端未就绪的执行者失败，可能需人工清理", "cause", kerr)
		}
		return nil, err
	}
	l.Debug("claude in.fifo 读端就绪", "wait", elapsed)
	// 补写真实 pid（写前置时只有 LockPath）
	if err := writeProcInfo(req.TaskDir, &procInfo{
		Handle: handle, SessionID: req.SessionID,
	}); err != nil {
		l.Warn("回写恢复凭据失败，重启恢复将不可用", "cause", err)
	}
	l.Info("claude 执行者已就位", "shim_pid", handle.PID)
	return p, nil
}
```

`Alive` / `Kill` / `procInfo`：
```go
// Alive 检查 claude 是否仍然存活：存活锁被持有 且 out.jsonl 无死亡哨兵。
//
// 为什么两条都要：锁只证明 shim 活着；claude 自己退出后 shim 会写哨兵再退出，
// 两者之间有一个极短窗口锁还在但 claude 已死，哨兵兜住它。
// （旧实现这里的第一条是 tmux has-session，而第二窗口的 tail -f 会一直吊着会话，
// 导致 claude 早死了会话还在——换成锁之后这个假存活来源被连根拔掉。）
func (p *Proc) Alive() bool {
	if p == nil || !prochost.Alive(p.Handle) {
		return false
	}
	exited, _ := procExited(filepath.Join(p.TaskDir, outFileName))
	return !exited
}

// Kill 终止 claude 及其后代（按进程组），幂等。
func (p *Proc) Kill() error {
	if p == nil {
		return nil
	}
	return prochost.Kill(p.Handle)
}

// procInfo 是恢复凭据的持久化形态，agentd 重启后凭它探活与续读。
//
// 注意：ChildPID 由 shim 补写（本包只读不写），整份覆盖时会丢——
// 因此 writeProcInfo 只在启动路径调用两次，之后一律由 stream 层按字段更新 Offset。
type procInfo struct {
	Handle    prochost.Handle `json:"handle"`
	ChildPID  int             `json:"child_pid,omitempty"`
	SessionID string          `json:"session_id"`
	Offset    int64           `json:"offset"`
}
```

`readProcInfo` 的完整性校验改为 `pi.Handle.LockPath == "" || pi.SessionID == ""`。
`procExited` 里 `sentinelPrefix` 换成 `prochost.SentinelPrefix`。
新增本包的 `envKeys`（与 prochost 的同名函数不共享，避免为日志导出 API）。

- [ ] **Step 4: 改 reap.go**

```go
// Reap 在没有内存运行态时按 proc.json 兜底回收 executor 侧资源。
//
// 回收顺序：读 proc.json 拿 Handle → prochost.Kill（内部先试锁，锁空闲直接成功）。
//
// 为什么不再有「确定性命名兜底」：旧实现在 proc.json 缺失时退到 tmux 会话名
// handoff-<id8>，因为会话名可由 taskID 推导。锁+pid 无法从 taskID 推导，
// proc.json 缺失就是真的无据可查——如实报错交审核者，不猜。
//
// 返回：Handle 对应的进程本就不在时返回 nil——目标是「确保它没了」，
// 不是「确保我杀了它」。
func (a *Adapter) Reap(taskID, taskDir string) error {
	pi, err := readProcInfo(taskDir)
	if err != nil {
		a.log.Error("读恢复凭据失败，无法兜底回收", "task", taskID, "cause", err)
		return fmt.Errorf("兜底回收任务 %s: %w", taskID, err)
	}
	a.log.Info("兜底回收 executor 资源", "task", taskID, "shim_pid", pi.Handle.PID)
	if err := prochost.Kill(pi.Handle); err != nil {
		a.log.Error("兜底回收失败", "task", taskID, "shim_pid", pi.Handle.PID, "cause", err)
		return err
	}
	a.log.Info("兜底回收完成", "task", taskID)
	return nil
}
```

同步更新 `reap_test.go`：把 `tmuxKill` 缝的断言改为「proc.json 缺失时报错」+
「Handle 锁空闲时返回 nil 且不发信号」两条。

- [ ] **Step 5: 改 resume.go / probe.go / adapter.go 的适配点**

Run: `grep -rn 'TmuxSession\|tmuxHasSession\|tmuxKill\|tmuxLaunch\|shellq\|tmux' internal/executor/claudecode/ | grep -v _test`
把每一处改成 `Handle` / `prochost.Alive` / `prochost.Kill`。逐文件：

- `resume.go`：「进程是否还活着」的判定统一走
  `(&Proc{Handle: pi.Handle, TaskDir: taskDir}).Alive()`；三条日志里的
  `"tmux", pi.TmuxSession` 改成 `"shim_pid", pi.Handle.PID`。
- `probe.go`：重建 `Proc` 的字段换成 `Handle`；`ProbeOutcome.Note` 从
  「claude 执行器已不在（tmux 会话 %s）」改成「claude 执行器已不在（进程 pid %d）」。
  Note 是判死后直接呈给审核者的一句话理由，写着一个已不存在的概念等于误导。
- `adapter.go`：「哨兵后回收 tmux 会话失败」改为「哨兵后回收执行者进程失败」；
  权限描述缺失时的兜底文案「请 tmux attach 查看现场」改成「请 handoff attach 查看现场」。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/executor/claudecode/ -v`
Expected: PASS（含新增 3 条与全部既有用例）

- [ ] **Step 7: 加关键节点日志**

核对（Step 3 已写）：`StartProc` 的 env 注入 Info（**只打 key**）、创建输入通道失败 Error、
LookPath 失败 Error、写前置失败 Error、拉起前 Info（bin/repo/resume）、拉起失败 Error、
FIFO 读端超时 Error（带 claude.log 尾部）、回收失败 Warn、就位 Info（shim_pid）。
`Reap` 的读凭据失败 Error、回收前 Info、回收失败 Error、**完成 Info**（成功路径不静默）。

- [ ] **Step 8: 加注释自检**

- 文件头已改（去掉 tmux 表述，why 改成 prochost）
- `claudeArgv` 含「为什么返回 argv」与 Resume 语义相反的 why
- `StartProc` 的写前置时序有 why 注释
- `Alive` 含「为什么锁与哨兵两条都要」并保留旧假存活问题的历史说明
- `Reap` 含「为什么不再有确定性命名兜底」
- `procInfo` 含 ChildPID 由 shim 补写的边界说明

- [ ] **Step 9: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: `go test` 全绿；**Windows 编译此刻应首次通过**（`syscall.Mkfifo` 阻断随本任务消失）。
若仍失败，输出错误并在本任务内修掉。

- [ ] **Step 10: Commit**

```bash
git add internal/executor/claudecode
git commit -m "refactor(claudecode): 迁移到 prochost，删除 tmux 与 sh 启动脚本"
```

---

### Task 4: opencode adapter 迁移到 prochost

**Files:**
- Modify: `internal/executor/opencode/proc.go`
- Modify: `internal/executor/opencode/resume.go`
- Modify: `internal/executor/opencode/probe.go`（重建 Proc 的字段 + 审核者可见的 Note 文本）
- Modify: `internal/executor/opencode/reap.go`（去掉确定性命名兜底）
- Modify: `internal/executor/opencode/adapter.go`（Proc 字段适配 + 三处「tmux attach」文案）
- Test: `internal/executor/opencode/proc_test.go`、删除 `internal/executor/opencode/proc_script_unix_test.go`

**Interfaces:**
- Consumes: Task 1/2 的 prochost API
- Produces:
  - `type Proc struct { Handle prochost.Handle; Port int; Password string; ServeLogPath string }`
  - `func StartServe(ctx context.Context, repoPath, taskID, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error)`（签名不变）
  - `func (p *Proc) Alive() bool` / `func (p *Proc) Kill() error`（签名不变）
  - 测试缝 `var startProcHost = prochost.Start`

- [ ] **Step 1: 写失败测试**

在 `internal/executor/opencode/proc_test.go` 追加：

```go
// TestServeSpecPutsPasswordInEnvNotArgv 钉死安全边界：
// 密码必须走 env，绝不能出现在 argv 里——argv 经 /proc/<pid>/cmdline 本机全局可读。
// 旧实现靠「密码写进 0600 脚本、argv 只有脚本路径」达成，换成 prochost 后
// 由 Spec.Env 承担，这条断言防止有人图省事把密码拼进 argv。
func TestServeSpecPutsPasswordInEnvNotArgv(t *testing.T) {
	spec := serveSpec("/repo", "/task", "/task/cfg.json", 12345, "s3cr3t",
		[]string{"HTTPS_PROXY=http://u:p@h:8080"})
	for _, a := range spec.Argv {
		if strings.Contains(a, "s3cr3t") {
			t.Fatalf("密码绝不能进 argv: %v", spec.Argv)
		}
	}
	var gotPass, gotCfg, gotProxy bool
	for _, kv := range spec.Env {
		switch kv {
		case "OPENCODE_SERVER_PASSWORD=s3cr3t":
			gotPass = true
		case "OPENCODE_CONFIG=/task/cfg.json":
			gotCfg = true
		case "HTTPS_PROXY=http://u:p@h:8080":
			gotProxy = true
		}
	}
	if !gotPass || !gotCfg || !gotProxy {
		t.Fatalf("env 缺项 pass=%v cfg=%v proxy=%v: %v", gotPass, gotCfg, gotProxy, spec.Env)
	}
	// handoff 自身注入的变量必须排在 env 文件之后，才能覆盖同名键（B19 protectedEnvKeys 纪律）
	passIdx, proxyIdx := -1, -1
	for i, kv := range spec.Env {
		if strings.HasPrefix(kv, "OPENCODE_SERVER_PASSWORD=") {
			passIdx = i
		}
		if strings.HasPrefix(kv, "HTTPS_PROXY=") {
			proxyIdx = i
		}
	}
	if passIdx < proxyIdx {
		t.Fatalf("handoff 注入变量必须排在 env 文件之后以取得覆盖优先级，pass=%d proxy=%d", passIdx, proxyIdx)
	}
	// argv 必须是 opencode serve 的原样形态
	if strings.Join(spec.Argv, " ") != "opencode serve --port 12345 --hostname 127.0.0.1" {
		t.Fatalf("argv 形态不对: %v", spec.Argv)
	}
	if !spec.Sentinel {
		// opencode 有 HTTP 探活面，但哨兵能区分「崩了」与「端口暂时不通」，仍然要
		t.Fatal("Sentinel 必须为 true")
	}
}

// TestOpencodeAliveNeedsBothLockAndHTTP 钉死两层判定。
func TestOpencodeAliveNeedsBothLockAndHTTP(t *testing.T) {
	p := &Proc{Handle: prochost.Handle{PID: os.Getpid(),
		LockPath: filepath.Join(t.TempDir(), "proc.lock")}, Port: 1}
	if p.Alive() {
		t.Fatal("锁无人持有时必须判死，不应再去探 HTTP")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/opencode/ -run 'TestServeSpec|TestOpencodeAlive' -v`
Expected: FAIL —— `undefined: serveSpec`、`Proc.Handle` 不存在

- [ ] **Step 3: 实现**

在 `internal/executor/opencode/proc.go`：

删除 `writeServeScript`、`shellQuote`、`serveTmuxArgs`、`startRenderTailWindow`、
`tmuxKill`、`tmuxHasSession`，以及 `shellq` import。常量删 `serveScriptFileName`，
加 `procInfoFileName = "proc.json"`、`lockFileName = "proc.lock"`。

新增 `serveSpec`：
```go
// serveSpec 组 opencode serve 的启动描述。
//
// 为什么密码走 Env 而不是 argv：进程 argv 本机全局可读（/proc/<pid>/cmdline），
// 密码进 argv 等于对同机任何用户公开。旧实现靠「密码写进 0600 启动脚本、
// 只把脚本路径给 tmux」达成同样效果；换成 prochost 后由 Spec.Env 承担
// （spec.json 同样 0600）。TestServeSpecPutsPasswordInEnvNotArgv 钉死这条。
//
// 为什么 handoff 注入的变量排在 env 文件之后：切片靠后者覆盖前者，
// 用户 env 文件里若定义了 OPENCODE_* 保留键，必须被 handoff 自己的值压过
// （B19 protectedEnvKeys 纪律，调用方另有 Warn 提示）。
func serveSpec(repoPath, taskDir, configPath string, port int, password string, env []string) prochost.Spec {
	serveLog := filepath.Join(taskDir, serveLogFileName)
	full := append(os.Environ(), env...)
	full = append(full,
		"OPENCODE_SERVER_PASSWORD="+password,
		"OPENCODE_CONFIG="+configPath,
	)
	return prochost.Spec{
		Argv:     []string{"opencode", "serve", "--port", strconv.Itoa(port), "--hostname", "127.0.0.1"},
		Dir:      repoPath,
		Env:      full,
		Stdout:   serveLog,
		Stderr:   serveLog, // serve 的 stdout/stderr 合并落一份，与旧 tee -a 行为一致
		LockPath: filepath.Join(taskDir, lockFileName),
		InfoPath: filepath.Join(taskDir, procInfoFileName),
		Sentinel: true,
	}
}
```

`Proc` 加 `Handle prochost.Handle` 字段，去掉 `TmuxSession`。
`StartServe` 的 tmux 段替换为：
```go
	bin, err := exec.LookPath("opencode")
	if err != nil {
		log.Error("opencode 未安装", "cause", err)
		return nil, fmt.Errorf("opencode 未安装: %w", err)
	}
	selfExe, err := os.Executable()
	if err != nil {
		log.Error("取 handoff 自身路径失败（shim 无法拉起）", "cause", err)
		return nil, fmt.Errorf("取自身可执行路径: %w", err)
	}
	spec := serveSpec(repoPath, taskDir, configPath, port, password, env)
	spec.Argv[0] = bin
	// 写前置：proc.json 先于进程落盘，Reap 才永远有据可查
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: prochost.Handle{LockPath: spec.LockPath}, Port: port, Password: password,
	}); err != nil {
		log.Error("写恢复凭据失败", "cause", err)
		return nil, err
	}
	log.Info("启动 opencode serve", "port", port, "bin", bin, "repo", repoPath)
	handle, err := startProcHost(spec, selfExe)
	if err != nil {
		log.Error("拉起 opencode serve 失败", "port", port, "cause", err)
		return nil, err
	}
	p := &Proc{Handle: handle, Port: port, Password: password,
		ServeLogPath: filepath.Join(taskDir, serveLogFileName)}
	if err := writeProcInfo(taskDir, &procInfo{
		Handle: handle, Port: port, Password: password,
	}); err != nil {
		log.Warn("回写恢复凭据失败，重启恢复将不可用", "cause", err)
	}
```
就绪轮询段保持不变（`p.probeHTTP()`），超时分支的 `_ = p.Kill()` 保持不变。

`Alive` / `Kill`：
```go
// Alive 检查 serve 是否仍然存活：存活锁被持有 且 端口有 HTTP 应答。
//
// 两者缺一即视为死亡。锁证明 shim 还在，HTTP 证明 serve 本身还在应答——
// serve 崩了但 shim 尚未收尸的窗口由 HTTP 这条兜住。
func (p *Proc) Alive() bool {
	if !prochost.Alive(p.Handle) {
		return false
	}
	return p.probeHTTP()
}

// Kill 终止 serve 及其后代（按进程组），幂等。
func (p *Proc) Kill() error { return prochost.Kill(p.Handle) }
```

`procInfo` 结构加 `Handle prochost.Handle`，去掉 tmux 字段，文件名常量指向 `proc.json`。

删除 `internal/executor/opencode/proc_script_unix_test.go`（它测的是已删除的 sh 脚本）。

**同任务内一并改掉 probe / reap / adapter 三处**（它们读同一份 procInfo，留在原地会编译不过，
而且其中两处的文本是审核者会读到的）：

1. `probe.go`：重建 `Proc` 的字段从 `TmuxSession` 换成 `Handle`；`ProbeOutcome.Note`
   与两条 Info 日志里的「tmux 会话 %s」改成「执行者进程 pid %d」（取 `pi.Handle.PID`）。
   Note 是判死后直接呈给审核者的一句话理由，写着一个已经不存在的概念等于误导。
2. `reap.go`：删掉「确定性命名兜底」分支——tmux 会话名可由 taskID 推导，锁路径与 pid
   不能，proc.json 缺失就是真的无据可查，如实报错。回收改为 `prochost.Kill(pi.Handle)`。
3. `adapter.go`：`Proc` 字段引用适配；把追加进事件流的文案里的
   「tmux attach 查看现场」改成「handoff attach 查看现场」、
   「手动杀掉 tmux 会话」改成「handoff stop 回收」。


- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/opencode/ -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

核对：LookPath 失败 Error、取自身路径失败 Error、写前置失败 Error、启动前 Info
（port/bin/repo）、拉起失败 Error（带 port）、就绪 Info（既有，保留 ready_ms）、
就绪超时 Error（既有，保留 serve.log 尾部）、回写凭据失败 Warn。
env 注入的 Info 保持既有实现（**只打 key 名 + protectedEnvKeys 的 Warn**）。

- [ ] **Step 6: 加注释自检**

- 文件头去掉 tmux 表述，why 改为 prochost（保留「为什么不放 agentd 子进程」的原意）
- `serveSpec` 含两条 why（密码走 env、注入变量的覆盖顺序）
- `Alive` 含「为什么锁与 HTTP 两条都要」
- 删掉的 `shellQuote` 相关注释一并清理

- [ ] **Step 7: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: 全绿

- [ ] **Step 8: Commit**

```bash
git add internal/executor/opencode
git commit -m "refactor(opencode): 迁移到 prochost，密码改由 Spec.Env 承载"
```

---

### Task 5: grok adapter 迁移到 prochost

**Files:**
- Modify: `internal/executor/grok/proc.go`
- Modify: `internal/executor/grok/taskenv.go`（如其中含 serve 脚本生成则一并删除）
- Modify: `internal/executor/grok/resume.go`
- Modify: `internal/executor/grok/probe.go`
- Modify: `internal/executor/grok/reap.go`
- Modify: `internal/executor/grok/adapter.go`
- Test: `internal/executor/grok/proc_test.go`

**Interfaces:**
- Consumes: Task 1/2 的 prochost API
- Produces:
  - `type Proc struct { Handle prochost.Handle; Port int; Secret string; ServeLogPath string }`
  - `func StartServe(ctx context.Context, repoPath, taskID, taskDir, model string, env []string, log *slog.Logger) (*Proc, error)`（签名不变）
  - `func (p *Proc) Alive() bool` / `func (p *Proc) Kill() error` / `func (p *Proc) WSURL() string` / `func (p *Proc) LogTail() string`（签名不变）
  - 测试缝 `var startProcHost = prochost.Start`

- [ ] **Step 1: 写失败测试**

在 `internal/executor/grok/proc_test.go` 追加：

```go
// TestGrokSpecKeepsSecretOutOfArgv 钉死安全边界（与 opencode 同源）：
// secret 必须走 env——argv 本机全局可读。旧实现同时排除了 tmux -e
// （show-environment 会把它暴露给任何能连上 tmux server 的本机用户），
// 现在 tmux 没了，但「不进 argv」这条依然是硬约束。
func TestGrokSpecKeepsSecretOutOfArgv(t *testing.T) {
	spec := serveSpec("/repo", "/task", "grok-4", 23456, "t0psecret", nil)
	for _, a := range spec.Argv {
		if strings.Contains(a, "t0psecret") {
			t.Fatalf("secret 绝不能进 argv: %v", spec.Argv)
		}
	}
	var found bool
	for _, kv := range spec.Env {
		if strings.HasSuffix(kv, "=t0psecret") {
			found = true
		}
	}
	if !found {
		t.Fatalf("secret 必须经 env 传入，实得 %v", spec.Env)
	}
	if spec.LockPath == "" || spec.InfoPath == "" {
		t.Fatal("LockPath/InfoPath 必填")
	}
}

// TestGrokAliveNeedsLockFirst 钉死：锁判死后不再做网络探测（省一次超时等待）。
func TestGrokAliveNeedsLockFirst(t *testing.T) {
	p := &Proc{Handle: prochost.Handle{PID: os.Getpid(),
		LockPath: filepath.Join(t.TempDir(), "proc.lock")}, Port: 1}
	if p.Alive() {
		t.Fatal("锁无人持有时必须直接判死")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/grok/ -run 'TestGrokSpec|TestGrokAlive' -v`
Expected: FAIL —— `undefined: serveSpec`、`Proc.Handle` 不存在

- [ ] **Step 3: 实现**

先 `grep -n "grok" internal/executor/grok/*.go | grep -i "serve\|script"` 定位现有 serve
命令的确切 argv 与环境变量名，**原样搬进** `serveSpec`——命令形态与变量名不得改动，
本任务只换承载方式。

删除 grok 侧的 `WriteServeScript` / `serveTmuxArgs` / `startRenderTailWindow` /
`tmuxKill` / `tmuxHasSession` 与 `shellq` import；新增：

```go
// serveSpec 组 grok agent serve 的启动描述。
//
// 为什么 secret 走 Env 而不是 --secret：进程 argv 本机全局可读
// （/proc/<pid>/cmdline），这是 opencode 侧 P0-4 划定的安全边界，本 adapter 原样继承。
// （旧实现另需排除 tmux -e——show-environment 会把它暴露给任何能连上 tmux server
// 的本机用户；tmux 拆掉后这条威胁消失，但「不进 argv」依然成立。）
func serveSpec(repoPath, taskDir, model string, port int, secret string, env []string) prochost.Spec {
	serveLog := filepath.Join(taskDir, serveLogName)
	full := append(os.Environ(), env...)
	full = append(full, grokSecretEnvKey+"="+secret) // 变量名沿用既有常量，不得改动
	return prochost.Spec{
		Argv:     grokServeArgv(model, port), // 沿用既有命令形态，原样搬运
		Dir:      repoPath,
		Env:      full,
		Stdout:   serveLog,
		Stderr:   serveLog,
		LockPath: filepath.Join(taskDir, lockFileName),
		InfoPath: filepath.Join(taskDir, procInfoFileName),
		Sentinel: true,
	}
}
```

`Proc` 加 `Handle prochost.Handle`、去掉 `TmuxSession`；`StartServe` 的 tmux 段
替换为 LookPath + os.Executable + 写前置 writeProcInfo + `startProcHost(spec, selfExe)`
+ 回写 Handle（形态与 Task 4 Step 3 完全一致，逐字照搬那段并把 opencode 换成 grok）。

`Alive` / `Kill`：
```go
// Alive 检查 grok serve 是否仍然存活：存活锁被持有 且 HTTP 端口有应答。
//
// 为什么第一条是锁而不是端口：锁是本地文件操作、微秒级，端口探测要走网络栈
// 且失败时要等超时。锁判死就没必要再探端口了。
// （旧实现第一条是 tmux has-session，而会话里第二窗口的 tail -f 会一直活着、
// serve 早死了会话依然存在——那个假存活来源已随 tmux 一起消失。）
func (p *Proc) Alive() bool {
	if !prochost.Alive(p.Handle) {
		return false
	}
	return p.probeHTTP()
}

// Kill 终止 grok serve 及其后代（按进程组），幂等。
func (p *Proc) Kill() error { return prochost.Kill(p.Handle) }
```

`procInfo` 加 `Handle`、去 tmux 字段、文件名改 `proc.json`。

**同任务内一并改掉 probe / reap / adapter 三处**（它们读同一份 procInfo，留在原地会编译不过，
而且其中两处的文本是审核者会读到的）：

1. `probe.go`：重建 `Proc` 的字段从 `TmuxSession` 换成 `Handle`；`ProbeOutcome.Note`
   与两条 Info 日志里的「tmux 会话 %s」改成「执行者进程 pid %d」（取 `pi.Handle.PID`）。
   Note 是判死后直接呈给审核者的一句话理由，写着一个已经不存在的概念等于误导。
2. `reap.go`：删掉「确定性命名兜底」分支——tmux 会话名可由 taskID 推导，锁路径与 pid
   不能，proc.json 缺失就是真的无据可查，如实报错。回收改为 `prochost.Kill(pi.Handle)`。
3. `adapter.go`：`Proc` 字段引用适配；把追加进事件流的文案里的
   「tmux attach 查看现场」改成「handoff attach 查看现场」、
   「手动杀掉 tmux 会话」改成「handoff stop 回收」。


- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/grok/ -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

核对：LookPath 失败 Error、取自身路径失败 Error、写前置失败 Error、启动前 Info
（port/bin/repo/model）、拉起失败 Error、就绪 Info、就绪超时 Error（带 serve.log 尾部）、
回写凭据失败 Warn。env 注入 Info **只打 key 名**（沿用既有）。

- [ ] **Step 6: 加注释自检**

- 文件头去 tmux；`serveSpec` 含 secret 走 env 的 why（含 tmux -e 的历史说明）
- `Alive` 含「为什么先看锁再探端口」
- 删除的脚本相关注释一并清理

- [ ] **Step 7: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: 全绿

- [ ] **Step 8: Commit**

```bash
git add internal/executor/grok
git commit -m "refactor(grok): 迁移到 prochost，secret 改由 Spec.Env 承载"
```

---

### Task 6: codex adapter 迁移到 prochost

**Files:**
- Modify: `internal/executor/codex/proc.go`
- Modify: `internal/executor/codex/taskenv.go`（删除其中的 sh 脚本生成）
- Modify: `internal/executor/codex/resume.go`
- Modify: `internal/executor/codex/probe.go`
- Modify: `internal/executor/codex/reap.go`
- Modify: `internal/executor/codex/adapter.go`（含「codex tmux 会话回收失败」事件文案）
- Test: `internal/executor/codex/proc_test.go`

**Interfaces:**
- Consumes: Task 1/2 的 prochost API
- Produces:
  - `type Proc struct { Handle prochost.Handle; Port int; ServeLogPath string }`（codex 无 Secret 字段，沿用现状）
  - `func StartServe(ctx context.Context, repoPath, taskID, taskDir string, env []string, log *slog.Logger) (*Proc, error)`（签名不变）
  - `func (p *Proc) Alive() bool` / `func (p *Proc) Kill() error` / `func (p *Proc) WSURL() string` / `func (p *Proc) LogTail() string`（签名不变）
  - 测试缝 `var startProcHost = prochost.Start`

- [ ] **Step 1: 写失败测试**

在 `internal/executor/codex/proc_test.go` 追加：

```go
// TestCodexSpecArgvIsListenForm 钉死 codex 的启动形态：
// `codex app-server --listen ws://127.0.0.1:<port>` 是协议契约的一部分，
// 端口拼错或少了 --listen 会让 WS JSON-RPC 完全连不上。
func TestCodexSpecArgvIsListenForm(t *testing.T) {
	spec := serveSpec("/repo", "/task", 34567, []string{"HTTPS_PROXY=http://p:1"})
	joined := strings.Join(spec.Argv, " ")
	if !strings.Contains(joined, "app-server") || !strings.Contains(joined, "--listen") {
		t.Fatalf("argv 必须是 codex app-server --listen 形态，实得 %v", spec.Argv)
	}
	if !strings.Contains(joined, "34567") {
		t.Fatalf("argv 必须带上分配到的端口，实得 %v", spec.Argv)
	}
	// 代理必须透传：codex 从非交互上下文启动，继承不到 shell 里的代理变量，
	// 漏配的症状极具迷惑性（会话建得起来、show 显示 running，但一个 token 都不产）
	var gotProxy bool
	for _, kv := range spec.Env {
		if kv == "HTTPS_PROXY=http://p:1" {
			gotProxy = true
		}
	}
	if !gotProxy {
		t.Fatalf("env 文件的代理变量必须透传，实得 %v", spec.Env)
	}
	if spec.LockPath == "" || spec.InfoPath == "" {
		t.Fatal("LockPath/InfoPath 必填")
	}
}

// TestCodexAliveNeedsLockFirst 钉死锁优先的两层判定。
func TestCodexAliveNeedsLockFirst(t *testing.T) {
	p := &Proc{Handle: prochost.Handle{PID: os.Getpid(),
		LockPath: filepath.Join(t.TempDir(), "proc.lock")}, Port: 1}
	if p.Alive() {
		t.Fatal("锁无人持有时必须直接判死")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'TestCodexSpec|TestCodexAlive' -v`
Expected: FAIL —— `undefined: serveSpec`、`Proc.Handle` 不存在

- [ ] **Step 3: 实现**

先 `grep -n "app-server\|listen" internal/executor/codex/*.go | grep -v _test` 定位现有
argv 与端口拼装，**原样搬运**。删除 codex 侧的脚本生成、`tmuxKill`、`tmuxHasSession`、
`startRenderTailWindow` 与 `shellq` import；新增：

```go
// serveSpec 组 codex app-server 的启动描述。
//
// 为什么 env 必须完整透传：agentd 从非交互上下文启动，继承不到 shell 里的代理变量。
// executor 机需要代理才能连 OpenAI 时漏配的症状极具迷惑性——会话建得起来、
// 回合发得出去、handoff show 显示 running，但模型一个 token 都不产，
// 只有 serve.log 里刷 failed to refresh available models（见 README codex 章节）。
func serveSpec(repoPath, taskDir string, port int, env []string) prochost.Spec {
	serveLog := filepath.Join(taskDir, serveLogName)
	return prochost.Spec{
		Argv:     codexServeArgv(port), // 沿用既有命令形态，原样搬运
		Dir:      repoPath,
		Env:      append(os.Environ(), env...),
		Stdout:   serveLog,
		Stderr:   serveLog,
		LockPath: filepath.Join(taskDir, lockFileName),
		InfoPath: filepath.Join(taskDir, procInfoFileName),
		Sentinel: true,
	}
}
```

`Proc` 加 `Handle prochost.Handle`、去 `TmuxSession`；`StartServe` 的 tmux 段替换为
LookPath + os.Executable + 写前置 + `startProcHost(spec, selfExe)` + 回写 Handle
（与 Task 4 Step 3 同形态，逐字照搬并把 opencode 换成 codex）。

`Alive` / `Kill`：
```go
// Alive 检查 codex app-server 是否仍然存活：存活锁被持有 且 WS 端口可连。
//
// 为什么第一条是锁：本地文件操作、微秒级；端口探测要走网络栈且失败要等超时。
// 锁判死就不必再探端口。
func (p *Proc) Alive() bool {
	if !prochost.Alive(p.Handle) {
		return false
	}
	return p.probePort()
}

// Kill 终止 codex app-server 及其后代（按进程组），幂等。
func (p *Proc) Kill() error { return prochost.Kill(p.Handle) }
```

`procInfo` 加 `Handle`、去 tmux 字段、文件名改 `proc.json`。

**同任务内一并改掉 probe / reap / adapter 三处**（它们读同一份 procInfo，留在原地会编译不过，
而且其中两处的文本是审核者会读到的）：

1. `probe.go`：重建 `Proc` 的字段从 `TmuxSession` 换成 `Handle`；`ProbeOutcome.Note`
   与两条 Info 日志里的「tmux 会话 %s」改成「执行者进程 pid %d」（取 `pi.Handle.PID`）。
   Note 是判死后直接呈给审核者的一句话理由，写着一个已经不存在的概念等于误导。
2. `reap.go`：删掉「确定性命名兜底」分支——tmux 会话名可由 taskID 推导，锁路径与 pid
   不能，proc.json 缺失就是真的无据可查，如实报错。回收改为 `prochost.Kill(pi.Handle)`。
3. `adapter.go`：`Proc` 字段引用适配；把追加进事件流的文案里的
   「tmux attach 查看现场」改成「handoff attach 查看现场」、
   「手动杀掉 tmux 会话」改成「handoff stop 回收」。


- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

核对：LookPath 失败 Error、取自身路径失败 Error、写前置失败 Error、启动前 Info
（port/bin/repo）、拉起失败 Error、就绪 Info、就绪超时 Error（带 serve.log 尾部）、
回写凭据失败 Warn。既有的「清理 ~/.codex 建议」WARN 保持不动。

- [ ] **Step 6: 加注释自检**

- 文件头去 tmux（现为「tmux 托管、探活、恢复凭据落盘」，改为「prochost 托管…」）
- `serveSpec` 含代理透传的 why（含那段迷惑性症状描述）
- `Alive` 含「为什么先看锁」

- [ ] **Step 7: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: 全绿

- [ ] **Step 8: Commit**

```bash
git add internal/executor/codex
git commit -m "refactor(codex): 迁移到 prochost，删除 tmux 与 sh 启动脚本"
```

---

### Task 7: 删除 shellq、改 reconcile 兜底提示、清理 tmux 残留、加 Windows 编译门禁

**Files:**
- Delete: `internal/shellq/shellq.go`、`internal/shellq/`（整包）
- Modify: `cmd/dispatch.go`（osascript 弹终端的 shell 拼接改为本地私有函数）
- Modify: `internal/agentd/reconcile.go`（删掉确定性会话名兜底 + 残留提示事件文案）
- Test: `cmd/dispatch_test.go`、`internal/agentd/reconcile_test.go`
- Create: `internal/prochost/windows_build_test.go`（编译门禁的说明性测试）

**Interfaces:**
- Consumes: Task 3–6 完成后 shellq 只剩 `cmd/dispatch.go` 一个消费者
- Produces: `func appleScriptQuote(s string) string`（`cmd` 包私有，仅供 osascript 拼接）

- [ ] **Step 1: 确认 shellq 的剩余消费者**

Run:
```bash
grep -rn "shellq" --include="*.go" . | grep -v "^./internal/shellq/"
```
Expected: 只剩 `cmd/dispatch.go`（若还有 executor 侧引用，说明 Task 3–6 有遗漏，先回去补完）

- [ ] **Step 2: 写失败测试**

在 `cmd/dispatch_test.go` 追加：

```go
// TestAppleScriptQuoteEscapes 钉死 osascript 命令串的引号转义。
//
// 这是 shellq 删除后 cmd 包唯一还需要的引号能力：do script 的参数是
// AppleScript 字符串字面量，attach 命令里若含空格或引号，不转义会让整条
// do script 语法错误、终端窗口弹不出来。
func TestAppleScriptQuoteEscapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`handoff attach T1`, `'handoff attach T1'`},
		{`a'b`, `'a'\''b'`},
	}
	for _, c := range cases {
		if got := appleScriptQuote(c.in); got != c.want {
			t.Fatalf("appleScriptQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./cmd/ -run TestAppleScriptQuote -v`
Expected: FAIL —— `undefined: appleScriptQuote`

- [ ] **Step 4: 实现并删除 shellq**

在 `cmd/dispatch.go` 加入（把原 `shellq.Quote` 的实现搬进来）：

```go
// appleScriptQuote 把字符串包成单引号 shell 字面量（内含单引号转义为 '\''）。
//
// 为什么留在 cmd 包而不是抽公共包：拆掉 tmux 后全项目只剩这一处需要 shell 引号
// ——osascript 的 do script 参数。为一个调用点维护一个包不划算，且抽出去容易
// 被误当成「通用 shell 拼接工具」重新用起来，那正是我们刚拆掉的东西。
func appleScriptQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

把 `cmd/dispatch.go` 里的 `shellq.Quote` 调用改为 `appleScriptQuote`，删除 shellq import。

Run:
```bash
rm -rf internal/shellq
grep -rn "shellq" --include="*.go" .
```
Expected: 第二条命令无输出

- [ ] **Step 5: 改 reconcile.go 的兜底回收提示**

`stopExecutor` 现在自己算一个 `session := "handoff-" + shortID(taskID)`，只为了写日志
与残留提示。会话名没了，这段必须改：

```go
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	m.log.Info("executor 无内存运行态，按恢复凭据兜底回收", "task", taskID)
	if rerr := rp.Reap(taskID, taskDir); rerr != nil {
		m.log.Error("兜底回收失败，留事件提示人工", "task", taskID, "cause", rerr)
		evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{
			// 给审核者的是「下一步做什么」，不是「出了什么错」——旧文案让人去
			// tmux kill-session，那个命令现在不存在了，照做只会更困惑
			Text: fmt.Sprintf("executor 进程可能残留，请先 handoff status 确认，"+
				"再 handoff stop %s 回收（原因：%v）", taskID, rerr),
		})
		...
	}
	m.log.Info("按恢复凭据兜底回收成功", "task", taskID)
```

`shortID` 若在本文件已无其他调用方，一并删除；仍被别处使用则保留但改掉它的
doc comment（现写着「与三个 adapter 的 tmux 会话命名规则一致」）。

Run: `grep -n "shortID" internal/agentd/*.go` 先确认调用方再决定删留。
Run: `go test ./internal/agentd/ -v`
Expected: PASS。若 `reconcile_test.go` 断言了旧的事件文案，同步改断言——
**改断言的同时要确认新文案确实更可行动**，不要为了让测试过而把文案改回去。

- [ ] **Step 6: 清理 tmux 残留**

Run:
```bash
grep -rn "tmux" --include="*.go" . | grep -v _test.go
```
Expected: **无输出**。有残留就逐条清掉（注释里的历史说明允许保留，但必须是
「旧实现曾经…」这种明确的过去式表述，不能读起来像现状）。

Run:
```bash
grep -rn "tmux" --include="*_test.go" .
```
把仍在断言 tmux 行为的测试删除或改写；测试文件里作为历史背景的注释可保留。

- [ ] **Step 7: 加 Windows 编译门禁测试**

创建 `internal/prochost/windows_build_test.go`：

```go
// windows_build_test.go —— Windows 交叉编译门禁。
//
// 职责：把「GOOS=windows 必须能编译」从口头约定变成可执行的断言。
//
// 边界：只验证能编译，不验证能运行——Windows 运行时实现是 B 期的事
// （见 spec §7）。本用例在 -short 下跳过：它要跑一次完整交叉编译，约数秒。
package prochost

import (
	"os/exec"
	"testing"
)

// TestWindowsCrossCompiles 断言整个模块在 GOOS=windows 下编译通过。
//
// 为什么这条测试值得存在：A 期的全部价值就是「架构上平台无关、Windows 实现留空」。
// 没有门禁的话，任何人往 adapter 里加一个 syscall.Xxx 都会悄悄把 Windows 之路
// 重新堵死，而 CI 全绿——本项目此前正是这样卡在 syscall.Mkfifo 上的。
func TestWindowsCrossCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("-short：跳过交叉编译门禁")
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Env = append(cmd.Environ(), "GOOS=windows", "GOARCH=amd64")
	cmd.Dir = ".."  + string('/') + ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("GOOS=windows go build ./... 失败（Windows 之路被堵死）:\n%s", out)
	}
}
```

- [ ] **Step 8: 运行测试确认通过**

Run: `go test ./cmd/ -run TestAppleScriptQuote -v && go test ./internal/prochost/ -run TestWindowsCrossCompiles -v`
Expected: PASS（两条）

- [ ] **Step 9: 加注释自检**

- `appleScriptQuote` 含「为什么留在 cmd 包」的 why
- `windows_build_test.go` 有文件头（职责 + 边界）与「为什么这条测试值得存在」

（本任务不新增运行时代码路径，无新日志点。）

- [ ] **Step 10: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: 全绿

- [ ] **Step 11: Commit**

```bash
git add -A internal cmd
git commit -m "refactor: 删除 shellq 与全部 tmux 残留，兜底回收提示改指向 handoff stop"
```

---

### Task 8: render 流式 endpoint（agentd 侧）

**Files:**
- Create: `internal/agentd/render_stream.go`
- Modify: `internal/agentd/server.go`（注册路由）
- Test: `internal/agentd/render_stream_test.go`

**Interfaces:**
- Consumes: 任务目录布局（`<DataDir>/tasks/<id>/render.log`），`Manager` 的任务查询
- Produces:
  - HTTP `GET /api/tasks/{id}/render?offset=<int>&follow=<0|1>&tail=<int>`
  - 响应头 `X-Handoff-Render-Size: <当前文件字节数>`、`Content-Type: text/plain; charset=utf-8`
  - `func (s *Server) handleTaskRender(w http.ResponseWriter, r *http.Request)`
  - `const renderHeartbeat = 20 * time.Second`、`const renderPollInterval = time.Second`

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/render_stream_test.go`：

```go
package agentd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newRenderServer 起一个只为 render endpoint 服务的最小 Server，
// 并在其 DataDir 下造出 tasks/<id>/render.log。
func newRenderServer(t *testing.T, taskID, content string) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	taskDir := filepath.Join(dir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("建任务目录失败: %v", err)
	}
	renderPath := filepath.Join(taskDir, "render.log")
	if err := os.WriteFile(renderPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写 render.log 失败: %v", err)
	}
	s := newTestServerWithDataDir(t, dir) // 复用本包既有测试辅助；无则按 server_test.go 的建法照搬
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, renderPath
}

func TestRenderReturnsFromOffset(t *testing.T) {
	ts, _ := newRenderServer(t, "t1", "0123456789")
	resp, err := http.Get(ts.URL + "/api/tasks/t1/render?offset=4")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	// 响应头必须带当前文件大小：客户端断线后凭「已收字节数」续传要用它对齐
	if got := resp.Header.Get("X-Handoff-Render-Size"); got != "10" {
		t.Fatalf("X-Handoff-Render-Size = %q, want \"10\"", got)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "456789" {
		t.Fatalf("按 offset 截取错误，实得 %q", b)
	}
}

func TestRenderTailStartsNearEnd(t *testing.T) {
	ts, _ := newRenderServer(t, "t1", strings.Repeat("x", 100)+"TAIL")
	resp, err := http.Get(ts.URL + "/api/tasks/t1/render?tail=4")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "TAIL" {
		t.Fatalf("tail 未从尾部回溯，实得 %q", b)
	}
}

func TestRenderFollowStreamsAppends(t *testing.T) {
	ts, renderPath := newRenderServer(t, "t1", "head")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/tasks/t1/render?offset=0&follow=1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("读首段失败: %v", err)
	}
	if string(buf) != "head" {
		t.Fatalf("首段错误: %q", buf)
	}
	// follow=1 时连接不关：追加内容必须继续流出来
	f, err := os.OpenFile(renderPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("打开 render.log 失败: %v", err)
	}
	if _, err := f.WriteString("MORE"); err != nil {
		t.Fatalf("追加失败: %v", err)
	}
	f.Close()

	more := make([]byte, 4)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(resp.Body, more); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("读追加段失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow 模式未在 5s 内送出追加内容")
	}
	if string(more) != "MORE" {
		t.Fatalf("追加段错误: %q", more)
	}
}

func TestRenderMissingFileIsEmptyNot404(t *testing.T) {
	ts, renderPath := newRenderServer(t, "t1", "")
	if err := os.Remove(renderPath); err != nil {
		t.Fatalf("删 render.log 失败: %v", err)
	}
	resp, err := http.Get(ts.URL + "/api/tasks/t1/render")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	// 任务刚 dispatch、模型还没吐第一个字时 render.log 尚不存在。
	// 这不是错误——attach 必须能连上并等着，而不是报 404 让人以为任务不对
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("render.log 不存在时应返回 200 空内容，实得 %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Handoff-Render-Size"); got != "0" {
		t.Fatalf("X-Handoff-Render-Size = %q, want \"0\"", got)
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) != 0 {
		t.Fatalf("应为空内容，实得 %q", b)
	}
}

func TestRenderRejectsUnknownTask(t *testing.T) {
	ts, _ := newRenderServer(t, "t1", "x")
	resp, err := http.Get(ts.URL + "/api/tasks/" + strconv.Itoa(999) + "/render")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知任务应 404，实得 %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestRender -v`
Expected: FAIL —— 路由不存在，全部返回 404

- [ ] **Step 3: 实现**

创建 `internal/agentd/render_stream.go`：

```go
// render_stream.go —— 任务实况（render.log）的流式读取接口。
//
// 职责：
//   - 按 offset / tail 截取 render.log 并写出；follow=1 时持续追送增量
//   - 通过响应头告知客户端当前文件大小，供断线续传对齐
//
// 边界：
//   - 不解析内容：render.log 是模型回合文本的原样增量，本文件只做字节搬运
//   - 不做轮转/清理：render.log 随任务目录在归档时一起走
//   - 不是事件流：结构化事件走 /ws/events，本接口只服务「人要看的实况」
//
// 为什么用轮询而不是 fsnotify：单文件、1s 粒度、任务数量级在个位数，
// 轮询 stat 的成本可以忽略；换 fsnotify 要多一个依赖和一套跨平台差异，
// 而它换来的延迟改善对「人在看文本」这个场景毫无意义。
package agentd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// renderPollInterval 是 follow 模式下探测文件增长的间隔。
	renderPollInterval = time.Second
	// renderHeartbeat 是无新内容时发送保活字节的间隔：中间设备（代理、NAT）
	// 常在 30–60s 空闲后断开连接，20s 心跳留足余量。
	renderHeartbeat = 20 * time.Second
	// renderDefaultTail 是不带任何参数时从尾部回溯的字节数：跟实况而不刷屏。
	renderDefaultTail = 4 << 10
)

// handleTaskRender 流式输出任务的 render.log。
//
// 查询参数：
//   - offset: 起始字节偏移；与 tail 互斥，两者都不给时按 renderDefaultTail 回溯
//   - tail:   从文件尾部回溯的字节数
//   - follow: 1 表示到达文件尾后不关闭连接，持续追送增量
//
// 响应：200 + text/plain 流；响应头 X-Handoff-Render-Size 为响应开始时的文件大小。
//
// 注意：
//   - render.log 尚不存在时返回 200 空内容而非 404——任务刚 dispatch、模型还没
//     吐第一个字是完全正常的状态，attach 应该连上等着，而不是报错让人以为任务不对
//   - 客户端断开（Ctrl+C）时 r.Context() 被取消，本函数随即返回，不留 goroutine
func (s *Server) handleTaskRender(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, ok := s.taskRepoOrErr(w, taskID); !ok {
		return // taskRepoOrErr 已写 404
	}
	renderPath := filepath.Join(s.dataDir, "tasks", taskID, "render.log")

	size := renderSize(renderPath)
	offset, err := renderStartOffset(r, size)
	if err != nil {
		s.log.Warn("render 请求参数非法", "task", taskID, "cause", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	follow := r.URL.Query().Get("follow") == "1"

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Handoff-Render-Size", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	s.log.Info("render 流开始", "task", taskID, "offset", offset, "size", size, "follow", follow)
	sent, err := streamRender(r.Context(), w, flusher, renderPath, offset, follow)
	if err != nil && !errors.Is(err, context.Canceled) {
		// 客户端主动断开是正常收尾，不是错误；其余情况要能查
		s.log.Error("render 流中断", "task", taskID, "sent", sent, "cause", err)
		return
	}
	s.log.Info("render 流结束", "task", taskID, "sent", sent)
}

// renderSize 返回 render.log 当前字节数；文件不存在时返回 0（见 handleTaskRender 的注意）。
func renderSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// renderStartOffset 依据 offset/tail 参数算出起始偏移。
//
// 优先级：显式 offset > tail > renderDefaultTail。
// offset 超过当前大小时钳到大小（不报错：文件可能刚被归档重建，钳住即可继续 follow）。
func renderStartOffset(r *http.Request, size int64) (int64, error) {
	q := r.URL.Query()
	if v := q.Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("offset 非法: %q", v)
		}
		if n > size {
			return size, nil
		}
		return n, nil
	}
	back := int64(renderDefaultTail)
	if v := q.Get("tail"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("tail 非法: %q", v)
		}
		back = n
	}
	if size <= back {
		return 0, nil
	}
	return size - back, nil
}

// streamRender 从 offset 起把 path 的内容写到 w；follow 为真时持续追送增量。
//
// 返回：已发送字节数与终止原因（客户端断开时返回 ctx.Err()）。
//
// 注意：文件不存在时不报错——follow 模式下等它出现即可（任务刚起、模型还没吐字）。
func streamRender(ctx context.Context, w io.Writer, flusher http.Flusher,
	path string, offset int64, follow bool) (int64, error) {
	var sent int64
	lastBeat := time.Now()
	for {
		n, err := copyFrom(w, path, offset)
		if err != nil {
			return sent, err
		}
		if n > 0 {
			offset += n
			sent += n
			lastBeat = time.Now()
			if flusher != nil {
				flusher.Flush()
			}
		}
		if !follow {
			return sent, nil
		}
		// 心跳：长时间无新内容时发一个换行保活。用换行而非注释语法，
		// 因为本接口是纯文本流不是 SSE，客户端直接打印，多一个空行无害
		if n == 0 && time.Since(lastBeat) >= renderHeartbeat {
			if _, err := w.Write([]byte("\n")); err != nil {
				return sent, err
			}
			if flusher != nil {
				flusher.Flush()
			}
			lastBeat = time.Now()
		}
		select {
		case <-ctx.Done():
			return sent, ctx.Err()
		case <-time.After(renderPollInterval):
		}
	}
}

// copyFrom 把 path 从 offset 起的全部剩余内容拷到 w，返回拷贝字节数。
//
// 文件不存在返回 (0, nil)：follow 模式下这是「还没开始产出」的正常状态。
func copyFrom(w io.Writer, path string, offset int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("打开 %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("定位 %s 到 %d: %w", path, offset, err)
	}
	n, err := io.Copy(w, f)
	if err != nil {
		return n, fmt.Errorf("读 %s: %w", path, err)
	}
	return n, nil
}
```

在 `internal/agentd/server.go` 的 `Handler()` 里，紧挨 `GET /api/tasks/{id}/diff` 之后加：
```go
	mux.HandleFunc("GET /api/tasks/{id}/render", s.handleTaskRender)
```

补 `render_stream.go` 的 `context` import。若 `Server` 尚无 `dataDir` 字段，
按 `server.go` 既有取任务目录的方式对齐（`grep -n "tasks" internal/agentd/server.go`）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run TestRender -v`
Expected: PASS（5 条）

- [ ] **Step 5: 加关键节点日志**

核对（Step 3 已写）：
- 参数非法 Warn（带 task + cause）
- 流开始 Info（task/offset/size/follow）
- 流中断 Error（带 task/sent/cause）——客户端主动断开除外，那是正常收尾
- **流结束 Info（task/sent）**——成功路径不静默

- [ ] **Step 6: 加注释自检**

- 文件头含职责 + 边界 + 「为什么轮询不用 fsnotify」
- `handleTaskRender` 文档注释含参数表与「文件不存在为何返回 200 而非 404」
- `renderStartOffset` 含优先级与「offset 超界为何钳住而不报错」
- `streamRender` 心跳段含 why

- [ ] **Step 7: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: 全绿

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/render_stream.go internal/agentd/render_stream_test.go internal/agentd/server.go
git commit -m "feat(agentd): 新增 render 流式 endpoint，任务实况可经 HTTP 续传跟随"
```

---

### Task 9: client 流式方法与 CLI attach 重写

**Files:**
- Modify: `internal/client/client.go`（新增 `RenderStream`）
- Modify: `cmd/attach.go`（整体重写：删 execve/ssh/tmux）
- Test: `internal/client/client_test.go`、`cmd/attach_test.go`

**Interfaces:**
- Consumes: Task 8 的 `GET /api/tasks/{id}/render`
- Produces:
  - `func (c *Client) RenderStream(ctx context.Context, taskID string, offset int64, tail int64, follow bool) (io.ReadCloser, int64, error)` —— 返回流、当前文件大小、错误
  - `func runAttach(cmd *cobra.Command, cli *client.Client, taskID string) error`（签名不变，实现全换）
  - CLI 新增 flag：`--all`（从头放，等价 offset=0）、`--no-follow`（放完即退）

- [ ] **Step 1: 写失败测试**

在 `internal/client/client_test.go` 追加：

```go
// TestRenderStreamPassesParamsAndReturnsSize 钉死请求参数与响应头解析。
func TestRenderStreamPassesParamsAndReturnsSize(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("X-Handoff-Render-Size", "4096")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("live"))
	}))
	defer ts.Close()

	rc, size, err := New(ts.Listener.Addr().String(), "tok").
		RenderStream(context.Background(), "T1", 0, 512, true)
	if err != nil {
		t.Fatalf("RenderStream 失败: %v", err)
	}
	defer rc.Close()
	if size != 4096 {
		t.Fatalf("size = %d, want 4096", size)
	}
	if !strings.Contains(gotQuery, "follow=1") || !strings.Contains(gotQuery, "tail=512") {
		t.Fatalf("查询参数缺失: %q", gotQuery)
	}
	b, _ := io.ReadAll(rc)
	if string(b) != "live" {
		t.Fatalf("流内容 = %q", b)
	}
}

// TestRenderStreamSurfacesHTTPError 钉死错误路径：404 必须变成明确错误而不是空流。
func TestRenderStreamSurfacesHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "任务不存在", http.StatusNotFound)
	}))
	defer ts.Close()
	_, _, err := New(ts.Listener.Addr().String(), "tok").
		RenderStream(context.Background(), "nope", 0, 0, false)
	if err == nil {
		t.Fatal("404 时必须返回错误")
	}
}
```

在 `cmd/attach_test.go`：**删除**全部断言 `ssh -t … tmux attach` 与 `execveFn` 的用例，
新增：

```go
// TestRunAttachStreamsToStdout 钉死 attach 的新语义：
// 从 agentd 的 render endpoint 取流并原样打印，不再 exec 任何外部命令。
func TestRunAttachStreamsToStdout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/render") {
			w.Header().Set("X-Handoff-Render-Size", "5")
			w.Write([]byte("hello"))
			return
		}
		w.Write([]byte(`{"task":{"id":"T1","target":""}}`))
	}))
	defer ts.Close()

	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	c.SetContext(context.Background())
	cli := client.New(ts.Listener.Addr().String(), "tok")
	if err := runAttach(c, cli, "T1"); err != nil {
		t.Fatalf("runAttach 失败: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("实况未打印到 stdout，实得 %q", out.String())
	}
}

// TestRunAttachRemoteNeedsNoSSH 钉死跨平台收益：
// 远程 target 不再拼 ssh 命令——复用 agentd 连接即可，因此 Windows 审核者也能用。
func TestRunAttachRemoteNeedsNoSSH(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/render") {
			w.Header().Set("X-Handoff-Render-Size", "2")
			w.Write([]byte("ok"))
			return
		}
		w.Write([]byte(`{"task":{"id":"T1","target":"devbox"}}`))
	}))
	defer ts.Close()

	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	c.SetContext(context.Background())
	if err := runAttach(c, client.New(ts.Listener.Addr().String(), "tok"), "T1"); err != nil {
		t.Fatalf("远程 target 的 attach 失败: %v", err)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("远程实况未打印，实得 %q", out.String())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/client/ -run TestRenderStream -v && go test ./cmd/ -run TestRunAttach -v`
Expected: FAIL —— `undefined: RenderStream`；attach 测试因仍走 execve 而失败

- [ ] **Step 3: 实现 client.RenderStream**

在 `internal/client/client.go` 追加：

```go
// RenderStream 打开任务实况（render.log）的流式读取。
//
// 参数：
//   - taskID: 目标任务
//   - offset: 起始字节偏移；>0 时优先于 tail（用于断线续传）
//   - tail:   从尾部回溯的字节数（offset<=0 时生效；两者都为 0 时由服务端取默认值）
//   - follow: 是否在到达文件尾后继续等待增量
//
// 返回：
//   - 流（调用方负责 Close）、响应开始时的文件字节数、错误
//
// 注意：
//   - 本方法**不设读超时**：follow 模式下长时间无输出是正常的（模型在思考）。
//     取消靠 ctx——CLI 把 Ctrl+C 接到 ctx 上
//   - 非 200 一律转成错误并读走响应体，避免连接泄漏
func (c *Client) RenderStream(ctx context.Context, taskID string,
	offset, tail int64, follow bool) (io.ReadCloser, int64, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	} else if tail > 0 {
		q.Set("tail", strconv.FormatInt(tail, 10))
	}
	if follow {
		q.Set("follow", "1")
	}
	path := "/api/tasks/" + taskID + "/render"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	resp, err := c.doStream(ctx, http.MethodGet, path)
	if err != nil {
		return nil, 0, fmt.Errorf("render 流请求: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, 0, c.httpError("render 流", resp)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("X-Handoff-Render-Size"), 10, 64)
	return resp.Body, size, nil
}
```

若 `Client` 现有的 `do` 会读完并关闭 body，需新增 `doStream`：与 `do` 同样带鉴权头，
但**使用不带 Timeout 的 http.Client**（follow 长连接不能被整体超时掐断），
且不消费 body。参考 `c.do` 的实现照搬鉴权与地址拼装部分。

- [ ] **Step 4: 重写 cmd/attach.go**

文件头改为：
```go
// 本文件实现 handoff attach 子命令：在终端跟随任务实况。
//
// 职责：
//   - 从 agentd 的 render 流式接口取任务实况，原样打印到 stdout，Ctrl+C 退出
//
// 边界：
//   - 不解析实况内容：render.log 是模型回合文本原样增量，这里只做搬运
//   - 不连 executor、不碰任务目录：一切经 agentd 的 HTTP 接口
//
// 为什么不再 exec 外部命令：旧实现用 syscall.Exec 换进程进 tmux（本机），
// 或 ssh -t <host> tmux attach（远程）。tmux 拆除后实况改由 agentd 落盘 +
// 流式吐出，attach 退化成一个普通 HTTP 客户端——顺带拿到三个收益：
// 远程不再需要 ssh（复用 agentd 连接与鉴权，配置里的 user 字段对 attach 不再必要）、
// Windows 审核者可用（syscall.Exec 在 Windows 上直接返回 EWINDOWS）、
// 断线可凭已收字节数续传。
```

删除 `attachCommandFor`、`execveFn`、`syscall` 与 `os` 的 execve 相关 import。
**保留 `sshHostFromTarget`**——`cmd/pull.go` 仍在用它（git-over-ssh）；把它的文档
注释里「attach/pull 共用的唯一换算点」改为「pull 的 ssh 目标换算点」。

新增 flag 与实现：
```go
// attachAll 表示从头播放全部实况（--all）；默认只从尾部回溯 attachDefaultTail。
var attachAll bool

// attachNoFollow 表示放完当前内容即退出（--no-follow），不等待后续增量。
var attachNoFollow bool

// attachDefaultTail 是默认回溯字节数：跟上实况又不至于把历史全刷一遍。
const attachDefaultTail = 4 << 10

// runAttach 连上任务的实况流并打印到 stdout，直到流结束或用户 Ctrl+C。
//
// 参数：
//   - cli: 已按 target 解析好 endpoint 的客户端
//   - taskID: 目标任务
//
// 返回：
//   - 连接失败（任务不存在、鉴权失败）时返回错误；用户主动中断（ctx 取消）返回 nil
//
// 注意：
//   - target 解析沿用既有规则（显式 --target → 任务自身记录的 target → 本机），
//     但换算结果只用于选 agentd endpoint，不再用于拼 ssh 命令
func runAttach(cmd *cobra.Command, cli *client.Client, taskID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background() // 裸 cobra 命令（测试）Context() 返回 nil
	}
	tail := int64(attachDefaultTail)
	if attachAll {
		tail = 0
	}
	follow := !attachNoFollow
	rc, size, err := cli.RenderStream(ctx, taskID, 0, tail, follow)
	if err != nil {
		return err
	}
	defer rc.Close()
	slog.Debug("attach 实况流已连接", "task", taskID, "size", size, "follow", follow)
	// 原样搬运：实况是给人看的文本，不做任何加工
	n, err := io.Copy(cmd.OutOrStdout(), rc)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("attach 实况流中断", "task", taskID, "printed", n, "cause", err)
		return fmt.Errorf("读实况流: %w", err)
	}
	slog.Debug("attach 实况流结束", "task", taskID, "printed", n)
	return nil
}
```

在 attach 命令的 `init()` 里注册两个 flag：
```go
	attachCmd.Flags().BoolVar(&attachAll, "all", false, "从头播放全部实况（默认只回溯末尾 4KB）")
	attachCmd.Flags().BoolVar(&attachNoFollow, "no-follow", false, "放完当前内容即退出，不等待后续增量")
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/client/ -run TestRenderStream -v && go test ./cmd/ -v`
Expected: PASS

- [ ] **Step 6: 加关键节点日志**

核对：`runAttach` 的连接成功 Debug（task/size/follow）、流中断 Error（task/printed/cause，
排除用户主动取消）、结束 Debug（task/printed）。用 `slog`，**禁止 `fmt.Printf`**
（实况内容本身经 `cmd.OutOrStdout()` 输出，那是数据不是日志）。

- [ ] **Step 7: 加注释自检**

- `attach.go` 文件头含职责 + 边界 + 「为什么不再 exec 外部命令」（含三个收益）
- `RenderStream` 文档注释含「为什么不设读超时」
- `sshHostFromTarget` 的注释已更新为「pull 专用」
- `runAttach` 文档注释说明 target 解析规则的新用途

- [ ] **Step 8: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: 全绿

- [ ] **Step 9: Commit**

```bash
git add internal/client cmd/attach.go cmd/attach_test.go
git commit -m "feat(attach): 改为消费 agentd render 流，去掉 execve 与 ssh+tmux 依赖"
```

---

### Task 10: systemd 单元模板、KillMode 检测与文档更新

**Files:**
- Create: `deploy/handoff-agentd.service`
- Create: `internal/agentd/killmode.go`
- Test: `internal/agentd/killmode_test.go`
- Modify: `README.md`
- Modify: `cmd/agentd.go`（启动期调用检测）
- Modify: `docs/superpowers/backlog.md`

**Interfaces:**
- Consumes: 无（独立诊断能力）
- Produces:
  - `func WarnIfKillModeUnsafe(log *slog.Logger)` —— agentd 启动期调用；检测到自身在
    systemd 托管下且 KillMode 非 process 时打 WARN
  - `func killModeFromCgroup(readFile func(string) ([]byte, error), unitLookup func(string) (string, error)) (unit, mode string, ok bool)` —— 可测缝

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/killmode_test.go`：

```go
package agentd

import (
	"errors"
	"testing"
)

func TestKillModeDetectsUnsafeUnit(t *testing.T) {
	readFile := func(string) ([]byte, error) {
		return []byte("0::/system.slice/handoff-agentd.service\n"), nil
	}
	lookup := func(unit string) (string, error) {
		if unit != "handoff-agentd.service" {
			t.Fatalf("unit 解析错误: %q", unit)
		}
		return "control-group", nil
	}
	unit, mode, ok := killModeFromCgroup(readFile, lookup)
	if !ok {
		t.Fatal("systemd 托管场景必须识别出来")
	}
	if unit != "handoff-agentd.service" || mode != "control-group" {
		t.Fatalf("unit=%q mode=%q", unit, mode)
	}
}

func TestKillModeSilentWhenNotUnderSystemd(t *testing.T) {
	// 非 systemd（macOS、docker、直接 shell 起）：cgroup 文件不存在或不含 .service
	readFile := func(string) ([]byte, error) { return nil, errors.New("no such file") }
	if _, _, ok := killModeFromCgroup(readFile, nil); ok {
		t.Fatal("非 systemd 场景不应报告 unit——误报会让 macOS 用户每次启动都看到无关警告")
	}
	readFile2 := func(string) ([]byte, error) { return []byte("0::/user.slice/session-3.scope\n"), nil }
	if _, _, ok := killModeFromCgroup(readFile2, nil); ok {
		t.Fatal("cgroup 路径不含 .service 时不应报告 unit")
	}
}

func TestKillModeSafeWhenProcess(t *testing.T) {
	readFile := func(string) ([]byte, error) {
		return []byte("0::/system.slice/handoff-agentd.service\n"), nil
	}
	lookup := func(string) (string, error) { return "process", nil }
	_, mode, ok := killModeFromCgroup(readFile, lookup)
	if !ok || mode != "process" {
		t.Fatalf("ok=%v mode=%q", ok, mode)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestKillMode -v`
Expected: FAIL —— `undefined: killModeFromCgroup`

- [ ] **Step 3: 实现检测**

创建 `internal/agentd/killmode.go`：

```go
// killmode.go —— systemd KillMode 的启动期自检。
//
// 职责：
//   - 判断 agentd 是否运行在 systemd unit 下；若是，读取其 KillMode
//   - KillMode 非 process 时打 WARN，提示执行者会随 agentd 重启一并被杀
//
// 边界：
//   - 只提示不阻断：用户可能有意用 control-group（例如希望重启即清场）
//   - 不修改任何配置：改 unit 是部署侧的事，agentd 无权也不应代劳
//   - 非 Linux / 非 systemd 环境一律静默：macOS 与 docker 下报这个警告是纯噪声
//
// 为什么这件事必须提示：拆掉 tmux 后，「执行者活过 agentd 重启」依赖 shim 脱离
// agentd 的进程树。setsid 做到了会话与进程组的脱离，但**改不了 cgroup 归属**
// ——cgroup 由 fork 继承。systemd 默认 KillMode=control-group 会在 restart 时
// 向整个 cgroup 发信号，shim 与执行者一并被杀，目标①直接落空。
// 这不是本次改动引入的退化：tmux 时代同样如此（tmux server 若由 agentd 首次
// 拉起也在同一 cgroup 里），只是从没被显式说明过。
package agentd

import (
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// selfCgroupPath 是当前进程的 cgroup 描述文件（cgroup v2 统一层级）。
const selfCgroupPath = "/proc/self/cgroup"

// killModeFromCgroup 解析当前进程所属的 systemd unit 及其 KillMode。
//
// 参数（两个函数参数是测试缝，生产分别传 os.ReadFile 与 systemctlKillMode）：
//   - readFile: 读 /proc/self/cgroup
//   - unitLookup: 按 unit 名查 KillMode
//
// 返回：
//   - unit: unit 名；mode: KillMode 值；ok: 是否确实在 systemd unit 下
//   - 非 Linux、非 systemd、cgroup 路径不含 .service 时 ok=false（静默）
func killModeFromCgroup(readFile func(string) ([]byte, error),
	unitLookup func(string) (string, error)) (unit, mode string, ok bool) {
	b, err := readFile(selfCgroupPath)
	if err != nil {
		return "", "", false // 非 Linux 或读不到：静默
	}
	// cgroup v2 行形如 "0::/system.slice/handoff-agentd.service"
	for _, line := range strings.Split(string(b), "\n") {
		idx := strings.LastIndex(line, "/")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[idx+1:])
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		unit = name
		break
	}
	if unit == "" {
		return "", "", false
	}
	if unitLookup == nil {
		return unit, "", true
	}
	mode, err = unitLookup(unit)
	if err != nil {
		return unit, "", true // unit 认出来了但查不到 mode：仍算 systemd 场景
	}
	return unit, mode, true
}

// systemctlKillMode 用 systemctl show 查某个 unit 的 KillMode。
func systemctlKillMode(unit string) (string, error) {
	out, err := exec.Command("systemctl", "show", "-p", "KillMode", "--value", unit).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// WarnIfKillModeUnsafe 在 agentd 启动期检查 systemd KillMode 并按需告警。
//
// 注意：
//   - 只打日志，绝不阻断启动（见文件头边界）
//   - 非 systemd 环境完全静默——macOS 开发机是主要使用场景，不能有噪声
func WarnIfKillModeUnsafe(log *slog.Logger) {
	unit, mode, ok := killModeFromCgroup(os.ReadFile, systemctlKillMode)
	if !ok {
		log.Debug("未在 systemd unit 下运行，跳过 KillMode 自检")
		return
	}
	if mode == "process" {
		log.Info("systemd KillMode 配置正确，agentd 重启不会连坐执行者", "unit", unit, "kill_mode", mode)
		return
	}
	log.Warn("systemd KillMode 非 process：agentd 重启会连同执行者一起杀掉，"+
		"正在跑的任务会中断。请在 unit 里设 KillMode=process（模板见 deploy/handoff-agentd.service）",
		"unit", unit, "kill_mode", mode)
}
```

在 `cmd/agentd.go` 的启动流程里（`os.MkdirAll(cfg.DataDir, 0o700)` 之后）加一行：
```go
	agentd.WarnIfKillModeUnsafe(slog.Default())
```

- [ ] **Step 4: 写 systemd unit 模板**

创建 `deploy/handoff-agentd.service`：

```ini
# handoff agentd 的 systemd unit 模板。
#
# 安装：
#   sudo cp deploy/handoff-agentd.service /etc/systemd/system/
#   sudo systemctl daemon-reload && sudo systemctl enable --now handoff-agentd
#
# 把 User / ExecStart 的路径改成你自己的。

[Unit]
Description=handoff agentd (executor host)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=CHANGEME  # 字面用户名；%i 只在模板 unit 里有值，非模板 unit 上为空 → 重置为 root
ExecStart=/usr/local/bin/handoff agentd --executor=opencode
Restart=on-failure
RestartSec=3

# KillMode=process 是本项目的硬要求，不是可选优化。
#
# 为什么：执行者进程由 agentd 经 shim 以独立会话拉起，目的是让它活过 agentd 的
# 重启与升级。setsid 让 shim 脱离了 agentd 的会话与进程组，但**改不了 cgroup 归属**
# ——cgroup 由 fork 继承。systemd 默认的 KillMode=control-group 会在 stop/restart
# 时向整个 cgroup 发信号，shim 与执行者一并被杀，正在跑的任务全部中断。
#
# 设为 process 后 systemd 只终止 agentd 主进程，执行者继续跑；agentd 重启后
# 靠存活锁探测重新接管。代价是 agentd 异常退出时执行者不会被自动清理——
# 这正是我们要的行为，回收由 handoff stop / done 显式完成。
KillMode=process

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run TestKillMode -v`
Expected: PASS（3 条）

- [ ] **Step 6: 更新 README**

改三处：

1. 架构图里删掉 tmux，改为 shim：
```
                                        │  ├─ executor adapter          │
                                        │  │   ├─ opencode serve（shim）  │
                                        │  │   ├─ claude -p（shim）       │
                                        │  │   ├─ grok agent serve（shim）│
                                        │  │   └─ codex app-server（shim）│
```
并把「桌面终端窗口 tmux attach ↑」改为「handoff attach（render 流）↑」。

2. 「handoff agentd」的说明里，把「executor 生命周期（tmux 内拉起/续接/回收）」
   改为「executor 生命周期（经 shim 以独立会话拉起/续接/回收）」。

3. 新增一节「部署到 systemd」，内容为：unit 模板路径 + `KillMode=process` 是硬要求
   及其一句话理由（setsid 不改 cgroup 归属）+ 不设时 agentd 会在启动日志里 WARN。

4. `handoff attach` 的命令说明表格项改为：「在终端跟随任务实况（render 流）」，
   flag 列写 `--all`（从头播放）、`--no-follow`（放完即退）。删除任何提到
   「需要 ssh」「tmux」的表述；`targets` 配置说明里的 `user` 字段改为「pull 用的
   远程 ssh 用户名」（不再提 attach）。

- [ ] **Step 7: 更新 backlog**

在 `docs/superpowers/backlog.md` 加两行：
- 本项目（拆 tmux / prochost A 期）标为 done 并回写验收证据位置
- 新增 B 期条目：「prochost Windows 实现」，状态 todo，前置条件写「A 期四 adapter
  真机验收通过」，范围写「命名管道、DETACHED_PROCESS + Job Object、Windows CI、
  四 executor CLI 在 Windows 的可用性验证、loginpath 的 Windows 等价物」，
  链接到本 spec 的 §7。

- [ ] **Step 8: 加关键节点日志**

核对 `killmode.go`：非 systemd Debug（不刷屏）、配置正确 Info（unit + kill_mode）、
配置不安全 Warn（unit + kill_mode + 明确后果与修复方式）。

- [ ] **Step 9: 加注释自检**

- `killmode.go` 文件头含职责 + 边界 + 「为什么这件事必须提示」（含 setsid 改不了 cgroup
  的技术说明与「tmux 时代同样如此」的诚实说明）
- `killModeFromCgroup` 文档注释标明两个函数参数是测试缝
- `WarnIfKillModeUnsafe` 含「非 systemd 完全静默」的 why
- unit 模板里 `KillMode=process` 上方有完整的 why 注释块

- [ ] **Step 10: 回归验证**

Run: `go test ./... && GOOS=windows GOARCH=amd64 go build ./... && gofmt -l . | grep -v '^desktop/' `
Expected: 测试全绿、Windows 编译通过、gofmt 无输出

- [ ] **Step 11: Commit**

```bash
git add deploy internal/agentd/killmode.go internal/agentd/killmode_test.go cmd/agentd.go README.md docs/superpowers/backlog.md
git commit -m "feat(deploy): systemd unit 模板与 KillMode 自检，README/backlog 同步拆 tmux 后的形态"
```

---

## 真机端到端验收（全部任务完成后执行，不可省略）

按项目既有纪律（B2 的七项清单）执行。**四个 executor 各跑一遍完整清单**，
一个都不能免——`agentd 重启续接` 是本次改动风险最高的路径。

对每个 `--executor` ∈ {opencode, claude, grok, codex}：

- [ ] ① `handoff dispatch` 派发一个真实小任务，`handoff show` 显示 running
- [ ] ② `handoff attach` 能看到模型文本实况实时增长；Ctrl+C 退出后再 attach 能继续看
- [ ] ③ 触发一次权限升级，`handoff wait` 收到工单且文本完整（`event_truncated=false`）
- [ ] ④ `handoff reply --approve` 后命令**真的执行了**（产出可验证的提交）
- [ ] ⑤ **任务执行中途 `systemctl restart handoff-agentd`（或 kill agentd 进程）**：
      执行者进程必须存活（`ps` 可见、`proc.lock` 仍被持有），agentd 重启后日志出现
      「执行器存活，重建订阅继续消费 alive=true」，随后 `handoff continue` 能产出真实提交
- [ ] ⑥ **agentd 离线期间让执行者退出**（重启 agentd 前先让任务跑到自然结束或 kill 执行者）：
      `out.jsonl` / `serve.log` 末尾必须出现 `handoff_exit` 哨兵且退出码正确
- [ ] ⑦ 远程 target 的 `handoff attach` 可用（不需要 ssh，走 agentd 连接）
- [ ] ⑧ `handoff stop` 后执行者进程组**全灭**（`ps` 查不到任何残留子进程），`proc.lock` 释放
- [ ] ⑨ `handoff diff` + `handoff done` 归档正常
- [ ] ⑩ env 注入生效（如 `HANDOFF_ENV_PROBE=ok` 在执行者环境里可见）

记录每项的实际输出到 backlog 的完工回写里，与 B2 同规格。

---

## Self-Review

**1. Spec 覆盖检查**

| Spec 章节 | 覆盖任务 |
|---|---|
| §2 被替换清单：tmux 四处调用 | Task 3/4/5/6 |
| §2：sh 启动脚本 | Task 3/4/5/6 |
| §2：in.fifo + Mkfifo | Task 1（原语）+ Task 3（消费） |
| §2：shellq | Task 7 |
| §2：tmux 第二窗口 tail -f | Task 3–6 删除 + Task 8 替代 |
| §2：attach 的 syscall.Exec + ssh | Task 9 |
| §2：假存活判据 | Task 3–6 的 Alive 重写 |
| §3.1 三原语接口 | Task 1 |
| §3.2 shim 四职责 | Task 2 |
| §3.3 systemd KillMode | Task 10 |
| §4.1 proc.json 写前置 | Task 3–6 各自的 Step 3 |
| §4.2 两层存活判定 | Task 3–6 的 Alive |
| §4.3 Reap | Task 3 Step 4（claude）+ Task 4/5/6 Step 3 的 probe/reap/adapter 小节 + Task 7 Step 5（reconcile 侧的确定性命名兜底） |
| §5.1 render endpoint | Task 8 |
| §5.2 CLI attach 重写 | Task 9 |
| §5.3 桌面端衔接 | Task 8 的 endpoint 即交付物；桌面端消费属其自身 plan |
| §6 测试与验收 | 各任务的 Step + 末尾真机清单 |
| §7 范围外（B 期） | Task 10 Step 7 写进 backlog |
| 范围决策：不做升级兼容 | Task 3 Step 4 / Task 7 Step 5 均无遗留路径 |
| 范围决策：Windows 骨架 + 编译门禁 | Task 1 Step 5 + Task 7 Step 7 |

无遗漏。

**2. Placeholder 扫描**：无 TBD/TODO；每个代码步骤都给了可直接落地的代码块。
两处「照搬」是有意的且不构成信息缺失：
- Task 5/6 的启动段引用 Task 4 Step 3——那段代码在 Task 4 完整写出且结构完全同形，
  同时 Task 5/6 各自给出了 `serveSpec` 的完整实现与差异点（secret / 端口 / argv 全不同）
- Task 4/5/6 的 probe/reap/adapter 小节内容一致——因为四个 adapter 这三处的改法确实
  逐字相同，各自的差异（字段名、文案）在小节里点名了

**3. 类型一致性**：`prochost.Handle{PID, LockPath}`、`prochost.Spec` 的字段名在 Task 1
定义后于 Task 2–6 一致使用；锁 API 统一为 `AcquireLock` / `(*Lock).Release` /
`IsLocked` / `LockSupported` / `ErrLockHeld`（Task 1 定义，Task 2 的 shim 与
`internal/agentd/lock.go` 消费）；`startProcHost` 缝名四个 adapter 统一；
`procInfoFileName = "proc.json"` / `lockFileName = "proc.lock"` 四个 adapter 包统一
（与 `internal/agentd` 里既有的 `lockFileName = "agentd.lock"` 不同包，无冲突）；
`prochost.SentinelPrefix` 在 Task 2 定义、Task 3 消费；`RenderStream` 的签名在
Task 9 Step 3 定义、Step 4 消费，参数顺序一致。

**4. 写计划期间对代码做的两处实测校正**（写进这里以免实现者重走弯路）：
- **不引 `golang.org/x/sys`**：初稿打算把它升为直接依赖，实测 stdlib `syscall` 在
  darwin/linux 下已提供全部所需（`Setsid`/`Mkfifo`/`Flock`/`Kill`/`ENXIO`/
  `EWOULDBLOCK`/`ESRCH`），且仓库既有三处平台切分都用 stdlib。少一个直接依赖。
- **flock 不重写**：B34（commit `0411df9c`，三个提交前）刚落地一份 flock 原语在
  `internal/agentd`。初稿在 prochost 里另写一份，是重复造轮子。改为上移进 prochost
  并让 `AcquireDataDirLock` 复用（Task 1 Step 6/7）。

**5. 已知的实现期待补项**（不是 placeholder，是留给实现者的明确动作）：
- Task 2 Step 1 的测试文件需补 `strconv` import 与 `startWith` 小 helper（已写明做法）
- Task 5/6 的 `grokServeArgv` / `codexServeArgv` / `grokSecretEnvKey` 需从现有代码
  原样搬运（Step 3 首行写明用 grep 定位，且明确「命令形态与变量名不得改动」）
- Task 8 的 `newTestServerWithDataDir` 若本包无此辅助，按 `server_test.go` 既有建法照搬
- Task 7 Step 5 的 `shortID` 删留取决于是否还有别的调用方（步骤里写了先 grep 再决定）
