# agentd 单实例保护（B34）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 一个 DataDir 同时只接纳一个 agentd，第二个必须在碰到任何数据之前失败，并给出指向 `handoff status` 的可行动指引。

**Architecture:** 在 `<DataDir>/agentd.lock` 上做 `flock(LOCK_EX|LOCK_NB)`，持有的 `*os.File` 存活到进程结束。锁在 `cmd/agentd.go` 的 `os.MkdirAll(cfg.DataDir)` 与 `store.Open` 之间获取——那是唯一能保证「撞锁时什么都没动过」的位置。平台原语（flock 调用与 errno 判定）用 `//go:build unix` / `!unix` 分文件隔离，逻辑与日志集中在一个平台无关的文件里。

**Tech Stack:** Go 1.26.1，`syscall`（标准库，**零新依赖**），cobra，`log/slog`。

## Global Constraints

以下逐条抄自 spec `docs/superpowers/specs/2026-08-10-agentd-single-instance-design.md`，每个 task 都隐含包含：

- **零新依赖**：只用标准库 `syscall`。不引入 `github.com/gofrs/flock` 等任何第三方锁库。
- **不做仓库级互斥**：agentd 不是 repo-scoped，`proto.Task.RepoPath` 是每任务字段，启动时没有「仓库」这个键可锁。任何试图锁 RepoPath 的实现都是错的。
- **不写 PID、不做进程探活、不提供 `--force`**：flock 由内核在进程终止时释放，**陈旧锁这个状态不存在**。任何「读锁文件里的 PID 再 `kill -0` 判断」的代码都是在重新引入已被消除的状态。
- **不报锁的持有者是谁**：撞锁错误只指向 `handoff status`，不试图从锁文件读出对方身份。
- **锁的获取位置**：`MkdirAll(DataDir)` 之后、`store.Open` 之前、`logx.Setup` 之后。三个约束缺一不可，理由见 spec §3。
- **日志用 `log/slog`**，禁止 `fmt.Printf`。成功路径也要打日志（`instrumenting-code`：不留静默的成功路径）。
- **注释用中文**，新文件写文件头（职责+边界），导出方法写 doc 注释，非显然分支写「为什么」。

## 与 spec 的一处偏差（已声明）

spec §5 的文件表把锁的实现记为**一个** `internal/agentd/lock.go`。本计划拆成三个：`lock.go`（逻辑与日志）+ `flock_unix.go` / `flock_other.go`（平台原语）。

理由：`syscall.Flock` 只在 unix 存在，不加 build tag 会让非 unix 编译直接失败，而 `internal/agentd` 包里已经有两组同样形态的 `!unix` 退化文件（`opennonblock_other.go`、`workspace_procgroup_other.go`）——沿用既有套路比破例更省事。拆开后逻辑仍集中在一个文件，平台差异被压到两个各约 10 行的小文件里。spec 的其余部分不受影响。

---

### Task 1: DataDir 单实例锁原语

**Files:**
- Create: `internal/agentd/lock.go`
- Create: `internal/agentd/flock_unix.go`
- Create: `internal/agentd/flock_other.go`
- Test: `internal/agentd/lock_test.go`

**Interfaces:**
- Consumes: 无（本 task 是最底层）
- Produces:
  - `func agentd.AcquireDataDirLock(dataDir string, log *slog.Logger) (*agentd.DataDirLock, error)`
  - `type agentd.DataDirLock struct{ ... }`（字段不导出）
  - `func (*agentd.DataDirLock) Release() error`
  - Task 2 只用这三个。

**背景（实现者必读）**：flock 挂在**打开的文件描述**（open file description）上，不挂在路径上。两个后果：① 同一个进程里两次 `os.OpenFile` 同一路径，第二次照样撞锁——**所以本 task 的测试不需要起子进程**；② `rm agentd.lock` 不能解锁。这两条都已在本机实测确认。

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/lock_test.go`：

```go
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'DataDirLock' -v`
Expected: 编译失败，`undefined: agentd.AcquireDataDirLock`

- [ ] **Step 3: 写平台原语（unix）**

创建 `internal/agentd/flock_unix.go`：

```go
//go:build unix

// flock_unix.go —— 单实例锁的平台原语（unix 实现）。
//
// 职责：把 flock(2) 的「非阻塞独占锁」与「锁已被占用」的 errno 判定，包成两个
// 平台无关的小函数供 lock.go 使用。
//
// 边界：不碰文件的打开与关闭，也不打日志——那些归 lock.go。
package agentd

import (
	"errors"
	"os"
	"syscall"
)

// flockSupported 标记本平台是否真的能加锁。
const flockSupported = true

// flockExclusiveNB 对一个已打开的文件取非阻塞独占锁。
//
// 注意：锁挂在「打开的文件描述」上而不是路径上。两个后果——同一进程内两次
// OpenFile 同一路径同样互斥（lock_test.go 据此免起子进程）；`rm` 掉锁文件
// 并不能解锁。
func flockExclusiveNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// isLockContended 判定错误是否为「锁已被他人持有」（LOCK_NB 下的 EWOULDBLOCK），
// 用于把撞锁与真正的 IO 故障分开——两者该给的错误信息完全不同。
func isLockContended(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}
```

- [ ] **Step 4: 写平台原语（非 unix 退化）**

创建 `internal/agentd/flock_other.go`（与包内既有的 `opennonblock_other.go` / `workspace_procgroup_other.go` 同一套路数，保证非 unix 平台照常编译）：

```go
//go:build !unix

// flock_other.go —— 单实例锁的平台原语（非 unix 退化实现）。
//
// 职责：让 lock.go 在没有 flock(2) 的平台上照常编译。
//
// 边界：**不提供任何保护**——加锁恒成功。调用方 lock.go 据 flockSupported
// 打 Warn 明说保护未生效，而不是假装锁住了。
package agentd

import "os"

// flockSupported 标记本平台是否真的能加锁。
const flockSupported = false

// flockExclusiveNB 非 unix 平台无 flock，空操作。
func flockExclusiveNB(*os.File) error { return nil }

// isLockContended 非 unix 平台永远撞不上锁——因为根本没加锁。
func isLockContended(error) bool { return false }
```

- [ ] **Step 5: 写锁的主体实现**

创建 `internal/agentd/lock.go`：

```go
// lock.go —— agentd 的 DataDir 单实例锁。
//
// 职责：
//   - AcquireDataDirLock：对 <DataDir>/agentd.lock 取非阻塞独占文件锁，
//     保证一个数据目录同时只被一个 agentd 接管
//   - 撞锁时给出可行动的错误（指向 handoff status），而不是一句「失败」
//
// 边界：
//   - 不做仓库级互斥：agentd 不是 repo-scoped，proto.Task.RepoPath 是每任务
//     字段，启动时没有「仓库」这个键可锁
//   - 不管陈旧锁：flock 由内核在进程终止时释放（正常退出/panic/SIGKILL/掉电
//     皆然），因此不写 PID、不做进程探活、不提供 --force 逃生口
//   - 不跨机器：flock 是本机语义，两台机器各跑各的 agentd 是 handoff 的正常形态
//   - 平台原语在 flock_unix.go / flock_other.go，本文件只做逻辑与日志
package agentd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// lockFileName 是 DataDir 下单实例锁文件的名字。
const lockFileName = "agentd.lock"

// lockHeldMsg 是撞锁时给用户看的完整文案（%s 填 DataDir）。
//
// 为什么不报「持有者是谁」：那要求往锁文件里写 PID 再读回，而读到的内容随时
// 可能是陈旧的（写入与读取之间对方进程可能已死）——为一条诊断信息重新引入
// 本已被 flock 消除的状态，不划算。指向 handoff status 是更可靠的答案。
//
// 为什么最后两行是重点：只说「被占了」等于把人堵在门口；给出下一步动作才有用。
const lockHeldMsg = `数据目录 %s 已被另一个 agentd 占用（` + lockFileName + `）。
同一个数据目录同时只能有一个 agentd——两个进程会抢同一份 SQLite、
同一批 worktree 与 tmux 会话，正是状态机最怕的失配。
先看现役那个是谁：handoff status
它能用就直接复用，不要再起一个。`

// DataDirLock 持有一个 DataDir 的独占权，直到 Release 或进程退出。
type DataDirLock struct {
	f   *os.File
	log *slog.Logger
}

// AcquireDataDirLock 对 <dataDir>/agentd.lock 取非阻塞独占锁。
//
// 参数：
//   - dataDir: 数据目录，调用方须保证它已存在（agentd 侧由 os.MkdirAll 保证）
//   - log: 日志入口，nil 时退回 slog.Default()
//
// 返回：
//   - 持有锁的句柄，调用方须一直持有到进程结束
//   - error: 已被另一个 agentd 占用时，错误文本是一段完整的可行动指引
//     （含 dataDir 与 `handoff status`），调用方直接原样返回即可，不要再包一层
//
// 注意：
//   - **必须在 store.Open 之前调用**。别指望端口冲突挡住第二个 agentd——
//     ListenAndServe 是 agentd 启动流程的最后一条语句，在它之前 RecoverOnStartup
//     已经对在役 agentd 的活执行器重建了订阅并写入状态迁移；SQLite 开了 WAL
//     也不拦多进程打开。破坏发生在撞端口之前
func AcquireDataDirLock(dataDir string, log *slog.Logger) (*DataDirLock, error) {
	if log == nil {
		log = slog.Default()
	}
	path := filepath.Join(dataDir, lockFileName)
	if !flockSupported {
		// 明说保护未生效，而不是让人误以为锁住了
		log.Warn("本平台不支持文件锁，agentd 单实例保护未生效", "data_dir", dataDir)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Error("打开单实例锁文件失败", "path", path, "cause", err)
		return nil, fmt.Errorf("打开锁文件 %s: %w", path, err)
	}
	if err := flockExclusiveNB(f); err != nil {
		f.Close()
		if isLockContended(err) {
			log.Error("数据目录已被另一个 agentd 占用，拒绝启动",
				"data_dir", dataDir, "path", path)
			return nil, fmt.Errorf(lockHeldMsg, dataDir)
		}
		// 非撞锁的加锁失败（如文件系统不支持 flock）：这是环境问题，
		// 与「已被占用」是两码事，不能套用那段指引文案误导用户
		log.Error("获取单实例锁失败", "path", path, "cause", err)
		return nil, fmt.Errorf("给 %s 加锁: %w", path, err)
	}
	log.Info("已取得数据目录单实例锁", "data_dir", dataDir, "path", path)
	return &DataDirLock{f: f, log: log}, nil
}

// Release 释放锁。
//
// 生产侧可有可无——进程退出内核即释放；保留它是为了让测试能验证「释放后可重新
// 获取」，以及 defer 的习惯写法。重复调用是安全的（第二次直接返回 nil）。
func (l *DataDirLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	path := l.f.Name()
	err := l.f.Close() // 关闭 fd 即释放 flock，无需显式 LOCK_UN
	l.f = nil
	if err != nil {
		l.log.Warn("释放数据目录单实例锁失败", "path", path, "cause", err)
		return err
	}
	l.log.Debug("已释放数据目录单实例锁", "path", path)
	return nil
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'DataDirLock' -v`
Expected: 6 个用例全 PASS

- [ ] **Step 7: 核对日志覆盖（instrumenting-code 自检）**

对照 Step 5 的代码逐条确认，缺哪条补哪条：

- 成功路径有 Info：`已取得数据目录单实例锁`（不留静默的成功路径）
- 撞锁分支有 Error 且带 `data_dir` / `path` 上下文
- 非撞锁的加锁失败有独立 Error 且带 `cause`
- 打开锁文件失败有 Error 且带 `cause`
- 平台不支持有 Warn
- 释放成功 Debug、释放失败 Warn
- 全文件无 `fmt.Printf` / `println`

- [ ] **Step 8: 核对注释覆盖（instrumenting-code 自检）**

- 三个新文件都有文件头（职责 + 边界）
- `AcquireDataDirLock` / `Release` / `DataDirLock` 有 doc 注释
- 「为什么不报持有者」「为什么必须在 store.Open 之前」「锁挂在 fd 不挂在路径上」「非撞锁失败为何不套用指引文案」四处 why 注释都在
- 没有复述代码的废注释

- [ ] **Step 9: 全量验证并提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/agentd/
```
Expected: `gofmt -l .` 无输出，vet 无输出，测试全绿

```bash
git add internal/agentd/lock.go internal/agentd/flock_unix.go internal/agentd/flock_other.go internal/agentd/lock_test.go
git commit -m "feat(agentd): DataDir 单实例 flock 锁原语

flock 挂在打开的文件描述上，内核在进程终止时释放——不存在陈旧锁，
因此不写 PID、不做进程探活、不提供 --force。

平台原语用 build tag 隔离（同包内 opennonblock/procgroup 的既有套路），
逻辑与日志集中在 lock.go。"
```

---

### Task 2: 接线到 agentd 启动流程 + 文档收口

**Files:**
- Modify: `cmd/agentd.go`（在 `os.MkdirAll(cfg.DataDir, 0o700)` 与 `store.Open` 之间插入）
- Modify: `skills/handoff/SKILL.md:219-221`

**Interfaces:**
- Consumes: Task 1 的 `agentd.AcquireDataDirLock(dataDir string, log *slog.Logger) (*agentd.DataDirLock, error)` 与 `(*agentd.DataDirLock).Release() error`
- Produces: 无（终点 task）

- [ ] **Step 1: 插入锁的获取**

在 `cmd/agentd.go` 中找到这段（`MkdirAll` 之后紧接着就是 `store.Open`）：

```go
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
		}

		st, err := store.Open(filepath.Join(cfg.DataDir, "handoff.db"))
```

在两者之间插入：

```go
		// 单实例锁（B34）：一个 DataDir 同时只接纳一个 agentd。位置被三个约束
		// 同时定死，改动前务必读懂：
		//   - 必须在 MkdirAll 之后：首次运行 DataDir 还不存在，锁文件没处放
		//   - 必须在 store.Open 之前：那是「碰数据」的第一步，也是唯一能保证
		//     撞锁时什么都没动过的位置
		//   - 必须在 logx.Setup 之后：否则撞锁失败的日志无处可去
		//
		// 为什么不能指望端口冲突挡住第二个 agentd：ListenAndServe 是本函数
		// **最后**一条语句，在它之前 RecoverOnStartup 已经对在役 agentd 的活
		// 执行器重建了订阅并写入状态迁移；store.Open 开了 WAL 也不拦多进程
		// 打开。破坏发生在撞端口之前，所以锁必须卡在这里。
		lock, err := agentd.AcquireDataDirLock(cfg.DataDir, logger)
		if err != nil {
			// 不再包一层：撞锁时 err 本身就是一段完整的可行动指引，
			// 前面再缀「启动 agentd 失败:」只会把重点冲淡
			return err
		}
		defer lock.Release()
```

注意：`cmd/agentd.go` 已 import `github.com/xushixin/handoff/internal/agentd`，无需新增 import。

- [ ] **Step 2: 编译并跑全量测试**

Run: `go build ./... && gofmt -l . && go vet ./... && go test ./...`
Expected: 全部无输出/全绿

- [ ] **Step 3: 真机验证撞锁（这是本 task 的验收，代替单测）**

spec §6 明确说明：「锁必须在 `store.Open` 之前」这条**顺序**约束不写单测——要测它得真起两次 agentd 并断言数据库未被改动，成本远超收益。改用一次真机对照实验，它同时验证顺序与文案。

准备一个独立的临时 DataDir 与端口，**不碰你正在用的 `~/.handoff`**：

```bash
TD=$(mktemp -d) && mkdir -p "$TD/data" && printf 'listen: 127.0.0.1:7799\ntoken: b34locktest\ndatadir: %s/data\nstalltimeout: 2h0m0s\n' "$TD" > "$TD/config.yaml" && go build -o "$TD/handoff" . && echo "TD=$TD"
```

起第一个 agentd（后台）：

```bash
"$TD/handoff" --config "$TD/config.yaml" agentd > "$TD/first.out" 2>&1 &
```

等它起来后，再起第二个（前台）：

```bash
sleep 3; "$TD/handoff" --config "$TD/config.yaml" agentd; echo "退出码=$?"
```

Expected：第二个进程打印 spec §4 的四行文案（含 `$TD/data` 路径与 `handoff status`），`退出码=1`。**注意它必须是被锁挡下的，不是被端口挡下的**——若看到 `address already in use`，说明锁没生效或位置插错了。

- [ ] **Step 4: 断言第二个 agentd 什么都没动**

这是本 task 最关键的一条断言：撞锁必须发生在 `RecoverOnStartup` **之前**。

```bash
grep -c "启动恢复完成" "$TD/data/agentd.log"
```

Expected: `1`（只有第一个 agentd 跑过启动恢复）。若为 `2`，说明锁插在了 `RecoverOnStartup` 之后，**顺序错了，必须回到 Step 1 修正**。

- [ ] **Step 5: 收摊**

```bash
pkill -f "$TD/handoff" ; rm -rf "$TD"
```

- [ ] **Step 6: 改写 SKILL.md 的红线**

`skills/handoff/SKILL.md` 第 219-221 行目前是：

```markdown
**红线：查到有 agentd 在跑就复用它，绝不为同一个仓库起第二个 agentd。**
两个 agentd 抢同一个仓库和 worktree，正是状态机最怕的失配——而代码层面目前
没有单实例锁拦你。
```

改为：

```markdown
**红线：查到有 agentd 在跑就复用它，不要起第二个。**
两个 agentd 抢同一份数据目录、同一批 worktree 与 tmux 会话，正是状态机最怕的
失配。这条现在由代码兜底——同一个 DataDir 起第二个 agentd 会直接被文件锁挡下
并报错，什么都不会被改动。别把它当逃生口：它挡的是事故，不是流程。

**升级 agentd 要先停旧的再起新的。** 以前是新进程起来撞端口失败（但那时破坏
已经造成了），现在是新进程被锁挡在门外。好处是安全，代价是不能再指望「起个新
的把旧的顶掉」。
```

- [ ] **Step 7: 提交**

```bash
git add cmd/agentd.go skills/handoff/SKILL.md
git commit -m "feat(agentd): 启动时获取 DataDir 单实例锁

插在 MkdirAll 与 store.Open 之间——端口冲突拦不住第二个 agentd，
ListenAndServe 是最后一条语句，在它之前 RecoverOnStartup 已经重建了
订阅并写入状态迁移。

SKILL.md 红线随之改写：从靠自觉变成代码兜底，并补上「升级要先停旧的」
这条流程影响。"
```

---

### Task 3: 回写 backlog

**Files:**
- Modify: `docs/superpowers/backlog.md`（B34 行）

**Interfaces:**
- Consumes: Task 1 与 Task 2 的测试与真机验证结果
- Produces: 无

- [ ] **Step 1: 跑一次完整的验收证据**

```bash
go build ./... && gofmt -l . && go vet ./... && go test ./... 2>&1 | tail -25
```

记下 `go test ./...` 的实际结果（包数与是否全 ok）。**如实记录**：若有任何包 FAIL，停下来修，不要带着红灯回写 backlog。

- [ ] **Step 2: 更新 B34 行**

把 B34 行的状态从 `🔨 doing` 改为 `✅ done(已验)`，并在「验收」列填入**真实**证据，格式参照同表其他行。必须含：

1. `go test ./...` 的真实结果（如 `全绿（N 个包）`）
2. Task 1 的六条单测点名（互斥 / 释放后可重入 / 跨 DataDir 不干扰 / 错误含路径与 `handoff status` / 锁文件被创建 / DataDir 不存在报可读错误）
3. Task 2 Step 3–4 的真机结论：第二个 agentd 被**锁**挡下（不是被端口挡下）、退出码 1、`agentd.log` 中 `启动恢复完成` 计数为 1（实证撞锁发生在 RecoverOnStartup 之前）
4. 结尾 `；无原型/流程图，自动免除对照 08-10`

若 Task 2 Step 3–4 的真机验证**没做**或没通过，状态记 `✅ done(未验)` 并在验收列如实写明缺哪一条——不要为了好看编证据。

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/backlog.md
git commit -m "chore(backlog): B34 回写验收结论"
```

---

## 附：实现者最容易走偏的三点

1. **想去锁仓库路径**。agentd 启动时手里没有仓库这个键——`RepoPath` 是每个任务各自的字段。锁的对象只能是 DataDir。
2. **想加 `--force` 或读锁文件里的 PID 判断陈旧**。flock 不存在陈旧锁，这两样都是在重新引入已被消除的状态。用户已明确选择「不给逃生口，硬失败」。
3. **把锁插在 `store.Open` 之后或 `NewServer` 之后**。看起来「反正都在 ListenAndServe 之前」，但 `RecoverOnStartup` 会真的写数据。Task 2 Step 4 的 `grep -c "启动恢复完成"` 就是专门用来抓这个错的。
