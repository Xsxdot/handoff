# B37 Windows 原生执行机 实现计划

> **For agentic workers:** 本计划的执行纪律由派发时附在文件头部的「执行纪律块」给定，以它为准。步骤用 `- [ ]` 复选框跟踪。

**Goal:** 让一台 Windows 机器能作为 handoff 执行机被 dispatch 派活、真实跑完一个 plan，且进程治理语义与 unix 执行机对等。

**Architecture:** 平台缝隙绝大部分收在 `internal/prochost` 内部（新增 `platform_windows.go`）。核心是一个 Job Object：shim 自己建、自己持句柄，一次性拿到「连坐回收 + 每任务硬上限 + 内核强制的树包含关系」三件事；`killGroup` 因此退化成「只杀 shim，内核收掉整棵树」。`prochost.Spec` / `proc.json` / 四个 adapter 一行不改。

**Tech Stack:** Go 1.26，`golang.org/x/sys/windows`（已在 go.mod 中作为 indirect，本计划提升为 direct，**不新增任何模块**）。

**设计输入：** `docs/superpowers/specs/2026-08-17-windows-native-executor-design.md`。本计划与 spec 冲突时以 spec 为准，但 spec 已在第十一节记录真机实测订正，实现按订正后的结论。

---

## Global Constraints

以下约束适用于**每一个** task，不再逐条重复：

1. **执行机是 macOS，Windows 代码在此无法运行。** 本计划在 mac 上的验证手段只有两类：(a) 平台中立逻辑的单测；(b) `GOOS=windows GOARCH=amd64 go build ./...` 与 `GOOS=windows GOARCH=amd64 go vet ./...`。**Windows 运行期行为一律不得声称「已验证」**——真机 e2e 由审核者在 Windows 上做。写 ledger 时如实记「未验（待真机）」。
2. **每个 task 结束前必须跑完整门，全部无输出/全绿才算完成：**
   ```
   go build ./...
   go vet ./...
   go test ./... -count=1
   gofmt -l $(git ls-files '*.go')
   GOOS=windows GOARCH=amd64 go build ./...
   GOOS=windows GOARCH=amd64 go vet ./...
   ```
   **`gofmt` 那条不许跳过**——测试全绿不等于格式干净，历史上漏过。
3. **日志一律用项目现有入口**：`internal/prochost` 内用包级 `log()`，`internal/agentd` 内用包级 `log()` 或传入的 `*slog.Logger`。**禁止 `fmt.Printf` / `println` 作为日志手段。**
4. **注释与日志用中文**，与仓库现状一致。新文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释（参数、返回、注意事项）；非显然分支必须有「为什么」注释。
5. **不新增依赖**。`golang.org/x/sys` 从 indirect 提升为 direct 是唯一的 go.mod 改动；不得引入 `go-winio` 或任何其它模块。
6. 每个 task 完成即 commit，提交信息用各 task「Commit」步骤里给定的原文。

---

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/prochost/platform_windows.go`（新建） | Windows 平台原语：detached spawn、Job Object 容器、按 job 回收、`LockFileEx` 存活锁 |
| `internal/prochost/platform_other.go`（改 tag） | 收紧为 `!unix && !windows` |
| `internal/prochost/platform_unix.go`（改） | 增加进程容器钩子的 unix 实现（包住现有 `setNprocLimit`） |
| `internal/prochost/shim.go`（改） | 围栏安装点泛化为「安装进程容器」 |
| `internal/prochost/fence.go`（改） | 围栏值分模式：unix 走 `reserve_ratio`，Windows 走 `TaskHardLimit` |
| `internal/pathenv/pathenv.go`（改） | Windows 上跳过登录 shell 来源 |
| `cmd/agentd.go`（改） | Windows 上不注册 claude / grok；`SetFencePolicy` 补参 |
| `internal/initflow/initflow.go`、`form.go`（改） | 解禁 Windows 执行机角色 |
| `internal/agentd/workspace.go`（改） | `run` 的 `sh` 定位；`RemoveManagedWorktree` 重试退避 |
| `internal/executor/opencode/proc.go`（改） | 就绪超时错误带上解析到的 `bin` |
| `.github/workflows/ci.yml`（改） | `GOOS=windows` 门从 build 升级为 build + vet |

---

## Task 1: 测试 build tag 补齐与 CI 的 `GOOS=windows` vet 门

**为什么第一个做：** 后面每个 task 的验证都依赖 `GOOS=windows go vet ./...` 能跑。现在它大面积失败，先把这条防线立起来，后续每一步才有判据。

**Files:**
- Modify: 若干 `*_test.go`（由 Step 1 的实测清单确定，不得凭印象）
- Modify: `.github/workflows/ci.yml:46-50`
- Modify: `internal/prochost/windows_build_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `GOOS=windows GOARCH=amd64 go vet ./...` 干净通过；`TestWindowsVets` 门禁用例

- [ ] **Step 1: 先取得真实失败清单**

```bash
GOOS=windows GOARCH=amd64 go vet ./... 2>&1 | tee /tmp/winvet-before.txt
wc -l /tmp/winvet-before.txt
```

这份输出就是本 task 的工作清单。**不要凭 spec 里列的文件名去改**——那份名单来自 08-10，可能已经漂移。

- [ ] **Step 2: 逐文件判定后加 tag**

对清单里每个报错文件，先确认它**确实**是 unix-only（含 `syscall.Kill`、`syscall.SysProcAttr{Setpgid/Setsid}`、`syscall.Getpgid`、硬编码 `/bin/sh`、`os.Symlink`、`/tmp` 字面量断言之一），确认后在文件第一行加：

```go
//go:build unix
```

**不得盲加。** 盲加会把本可在 Windows 上跑的用例一起排除掉——那是用一个假的绿色换真的覆盖。若某文件只有个别用例是 unix-only，把那些用例拆到一个新的 `*_unix_test.go` 里，而不是给整个文件加 tag。

- [ ] **Step 3: 跑到干净**

```bash
GOOS=windows GOARCH=amd64 go vet ./...
```

预期：无任何输出。若仍有报错，回 Step 2 继续。

- [ ] **Step 4: 把 vet 门写成可执行断言**

在 `internal/prochost/windows_build_test.go` 末尾追加：

```go
// TestWindowsVets 断言整个模块在 GOOS=windows 下 vet 通过。
//
// 为什么 build 门不够：build 只看非测试代码，而 unix-only 的**测试**文件同样会
// 把 Windows 之路堵死——它们不加 build tag 时，任何人在 Windows 上跑 go test
// 都会先撞编译错误。B37 落地后真机 e2e 不可能每个 PR 跑，vet 门是唯一守得住的。
func TestWindowsVets(t *testing.T) {
	if testing.Short() {
		t.Skip("-short：跳过交叉 vet 门禁")
	}
	cmd := exec.Command("go", "vet", "./...")
	cmd.Env = append(cmd.Environ(), "GOOS=windows", "GOARCH=amd64")
	cmd.Dir = ".." + string('/') + ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("GOOS=windows go vet ./... 失败（Windows 之路被堵死）:\n%s", out)
	}
}
```

- [ ] **Step 5: 跑新用例确认它真的守着**

```bash
go test ./internal/prochost/ -run TestWindowsVets -v
```
预期：PASS。

再做一次变异验证：临时把某个已加 tag 的测试文件的 `//go:build unix` 删掉，重跑该用例，**必须 FAIL**；确认后还原。这一步是为了证明门禁不是恒绿的摆设。

- [ ] **Step 6: CI 升级**

把 `.github/workflows/ci.yml:46-50` 那段改成：

```yaml
        # 从 B36 起这就是硬约束，B86 之后 Windows 还要真发资产，两个 arch 都要过。
        # B37 起追加 vet：build 只看非测试代码，测试文件里的 unix-ism 同样能把
        # Windows 之路堵死，而那正是 B37 落地后最容易被静默弄坏的地方。
        run: |
          set -euo pipefail
          GOOS=windows GOARCH=amd64 go build ./...
          GOOS=windows GOARCH=arm64 go build ./...
          GOOS=windows GOARCH=amd64 go vet ./...
```

- [ ] **Step 7: 全量门**

跑 Global Constraints 第 2 条的六条命令，全部通过。

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "test(windows): 补齐 unix-only 测试的 build tag，CI 加 GOOS=windows vet 门"
```

---

## Task 2: prochost Windows 平台文件与 `LockFileEx` 存活锁

**Files:**
- Create: `internal/prochost/platform_windows.go`
- Modify: `internal/prochost/platform_other.go`（第 1 行 build tag）
- Modify: `go.mod`

**Interfaces:**
- Consumes: 无
- Produces（Windows 编译单元内的包级符号，Task 3 会把其中三个进程原语替换成真实现）：
  - `const lockSupported = true`
  - `func flockExclusiveNB(f *os.File) error`
  - `func isLockContended(err error) bool`
  - `func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error)`
  - `func killGroup(pid int) error`
  - `func killProc(pid int) error`
  - `func createInputChannel(path string) error`
  - `func waitInputReader(path string, timeout time.Duration) (time.Duration, error)`
  - `var errNotImplemented error`
  - `const defaultFenceHardLimitMode = true`（Task 4 消费）

- [ ] **Step 1: 收紧 `platform_other.go` 的 build tag**

把 `internal/prochost/platform_other.go` 第 1 行从

```go
//go:build !unix
```

改为

```go
//go:build !unix && !windows
```

并在文件头注释里，把「B 期（独立立项）补齐」那一段改成：

```go
// Windows 的实现已移到 platform_windows.go（B37）。本文件现在只覆盖
// plan9/js 等既非 unix 也非 windows 的平台，那里没有任何实现计划。
```

- [ ] **Step 2: 新建 `platform_windows.go`（本 task 只落锁原语，进程原语先留未实现）**

创建 `internal/prochost/platform_windows.go`：

```go
//go:build windows

// platform_windows.go —— prochost 的 Windows 平台原语。
//
// 职责：detached spawn、Job Object 进程容器、按 job 回收、LockFileEx 存活锁。
//
// 边界：
//   - 只提供系统调用级能力，不含任何 handoff 业务语义
//   - 用 golang.org/x/sys/windows，不引 go-winio。为什么与 platform_unix.go
//     「不引 x/sys」的结论相反：unix 上 stdlib syscall 就够（Flock/Mkfifo/Kill
//     都在），不引是零成本；而 Windows 的 stdlib syscall 里 CreateNamedPipe /
//     LockFileEx / CreateJobObject 一个都没有，「只用 stdlib」的实际含义是在本仓库
//     里重写一份 x/sys。同一条原则（用最小够用的东西）在两个平台导出相反结论
//   - 输入通道（命名管道）不在本轮范围：它只在 claude 路径上，而 claude 在
//     Windows 上根本不注册（见 cmd/agentd.go 的 defaultAdapters）
package prochost

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// errNotImplemented 是本平台尚未实现的原语的统一返回。
//
// 本轮之后它只剩输入通道两个原语在用，且**实际不可达**：调用它们的唯一路径是
// claude adapter，而 Windows 上 claude 不进注册表，dispatch 在门口就被拒了。
// 这个不可达是被注册层挡出来的，不是碰巧——改注册表时要连带想到这里。
var errNotImplemented = errors.New("prochost: 本平台的进程承载尚未实现")

// lockSupported 标记本平台是否真的能加锁。
//
// Windows 的字节区间锁随句柄关闭而释放，而进程终止时句柄由系统关闭，因此
// 「内核在进程死亡时无条件释放」这条不变量与 unix 的 flock 一致——prochost
// 那套「不写 PID、不做进程探活、不提供 --force」的设计前提在本平台同样成立。
const lockSupported = true

// defaultFenceHardLimitMode 标记本平台的围栏值取自 TaskHardLimit 而非 reserve_ratio。
//
// 为什么 Windows 走另一套：reserve_ratio 的前提是「存在一个每用户进程数上限，
// 我们保留其中一部分」，而 Windows 没有 RLIMIT_NPROC 式的每用户硬上限（进程数
// 受内存与句柄约束）。详见 spec 11.6。
const defaultFenceHardLimitMode = true

// flockExclusiveNB 对一个已打开的文件取非阻塞独占锁。
//
// 锁 1 个字节即可：本包只用它做「有没有人持有」的存在性判据，不做区间互斥。
func flockExclusiveNB(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped))
}

// isLockContended 判定错误是否为「锁已被他人持有」。
//
// LOCKFILE_FAIL_IMMEDIATELY 下撞锁返回 ERROR_LOCK_VIOLATION(33)。必须与真正的
// IO 故障分开：撞锁是正常语义（对方还活着），IO 故障说明存活判据本身不可信。
func isLockContended(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error) {
	return 0, errNotImplemented
}

func killGroup(pid int) error { return errNotImplemented }

func killProc(pid int) error { return errNotImplemented }

// createInputChannel / waitInputReader 见文件头：只在 claude 路径上，本轮不做。
func createInputChannel(path string) error { return errNotImplemented }

func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	return 0, errNotImplemented
}
```

- [ ] **Step 3: 在 `platform_unix.go` 与 `platform_other.go` 补上同名常量**

两个文件各加一行（放在 `lockSupported` 旁边）：

```go
// defaultFenceHardLimitMode 见 fence.go：本平台走 reserve_ratio，不用 TaskHardLimit。
const defaultFenceHardLimitMode = false
```

- [ ] **Step 4: 把 x/sys 提升为 direct**

```bash
go mod tidy
git diff go.mod
```

预期：`golang.org/x/sys` 从 `require (... // indirect)` 块移入 direct require 块，**`go.sum` 不变**。若 `go.sum` 有变化，停下来报告——那说明引入了预期外的东西。

- [ ] **Step 5: 编译门**

```bash
GOOS=windows GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go vet ./...
go build ./... && go vet ./...
```
预期：全部无输出。

- [ ] **Step 6: 全量门**

跑 Global Constraints 第 2 条的六条命令。注意 `go test ./... -count=1` 在 mac 上跑的是 darwin 编译单元，**本 task 的新代码一行都没被执行**——这是预期的，不要因为测试全绿就认为 Windows 锁已验证。

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(prochost): Windows 平台文件与 LockFileEx 存活锁"
```

---

## Task 3: Windows 进程承载——detached spawn、Job Object 容器、按 job 回收

**为什么这三件事在同一个 task：** `killGroup` 的正确性完全依赖 job 存在（它只杀 shim，靠 `KILL_ON_JOB_CLOSE` 连坐收树）。审阅者无法在没有 job 的前提下批准 `killGroup`，所以拆开没有意义。

**Files:**
- Modify: `internal/prochost/platform_windows.go`
- Modify: `internal/prochost/platform_unix.go`
- Modify: `internal/prochost/platform_other.go`
- Modify: `internal/prochost/shim.go:98-107`

**Interfaces:**
- Consumes: Task 2 的 `platform_windows.go` 骨架
- Produces:
  - `func installProcessContainer(nprocLimit int) error` —— 三个平台各一份实现，由 `shim.go` 在 spawn 之前调用一次
  - `spawnDetached` / `killGroup` / `killProc` 的 Windows 真实现

- [ ] **Step 1: 定义进程容器钩子的 unix 实现**

在 `internal/prochost/platform_unix.go` 末尾追加：

```go
// installProcessContainer 在 spawn 执行者之前，把当前进程（shim）放进本平台的
// 「进程容器」里，使执行者全树继承其约束。
//
// 参数：nprocLimit 为围栏值（执行者树的进程数上限）；<=0 表示不设围栏。
//
// 返回：error 非 nil 时 shim 必须放弃拉起执行者。
//
// unix 的容器就是 RLIMIT_NPROC——rlimit 随 fork 继承，所以装在 shim 上等于装在
// 整棵树上。**装不上不阻断**：防护装置故障不该变成拒绝服务，这是 B73 定的语义，
// 本次泛化不改变它（与 Windows 侧相反，见 platform_windows.go 的同名函数）。
func installProcessContainer(nprocLimit int) error {
	if nprocLimit <= 0 {
		log().Info("本任务未设进程围栏", "reason", "spec 未下发围栏值")
		return nil
	}
	if err := setNprocLimit(nprocLimit); err != nil {
		log().Warn("安装进程围栏失败，本任务无围栏保护", "limit", nprocLimit, "cause", err)
		return nil
	}
	log().Info("进程围栏已安装", "limit", nprocLimit)
	return nil
}
```

- [ ] **Step 2: 定义进程容器钩子的 other 实现**

在 `internal/prochost/platform_other.go` 末尾追加：

```go
// installProcessContainer 本平台无实现。
//
// 实际不可达：本平台的 spawnDetached 同样返回未实现，shim 根本起不来。
func installProcessContainer(int) error { return errNotImplemented }
```

- [ ] **Step 3: 把 shim 的围栏安装点换成容器钩子**

把 `internal/prochost/shim.go` 中这一段（现约 98-107 行）：

```go
	// 围栏必须在 spawn 之前装：rlimit 随 fork 继承，装晚一步 executor 就在
	// 围栏外面了。装不上不阻断——防护装置故障不该变成拒绝服务
	if spec.NprocLimit > 0 {
		if ferr := setNprocLimit(spec.NprocLimit); ferr != nil {
			l.Warn("安装进程围栏失败，本任务无围栏保护", "limit", spec.NprocLimit, "cause", ferr)
		} else {
			l.Info("进程围栏已安装", "limit", spec.NprocLimit)
		}
	} else {
		l.Info("本任务未设进程围栏", "reason", "spec 未下发围栏值")
	}
```

替换为：

```go
	// 进程容器必须在 spawn 之前装：unix 的 rlimit 随 fork 继承、Windows 的 job
	// 成员身份随 CreateProcess 继承，装晚一步执行者就在容器外面了。
	//
	// **两个平台的失败语义刻意不同**，由各自的实现决定，不要在这里统一：
	// unix 上容器只是围栏，装不上仍可跑（Warn 后返回 nil）；Windows 上容器是
	// job，而 job 是 killGroup 唯一的回收手段，建不起来就必须硬失败——否则
	// 这个任务将永远杀不干净。
	if cerr := installProcessContainer(spec.NprocLimit); cerr != nil {
		l.Error("安装进程容器失败，放弃拉起执行者", "limit", spec.NprocLimit, "cause", cerr)
		return fmt.Errorf("安装进程容器: %w", cerr)
	}
```

- [ ] **Step 4: 确认 unix 侧行为未变**

```bash
go test ./internal/prochost/ -count=1 -v -run 'Fence|Shim'
```
预期：全部 PASS，与改动前一致。这一步是回归锚——泛化不得改变 unix 行为。

- [ ] **Step 5: 写 Windows 的三个进程原语与容器实现**

把 `platform_windows.go` 里 Task 2 留下的三个桩替换成下面的实现，并追加 `installProcessContainer`。同时补上 import：`fmt`、`os/exec`、`syscall`。

```go
// jobHandle 是 shim 持有的 Job Object 句柄。
//
// 为什么是包级 var 而不是返回给调用方：这个句柄**必须活到 shim 进程结束**，
// KILL_ON_JOB_CLOSE 的语义正是「最后一个句柄关闭时收掉全部成员」。交给调用方
// 持有就会出现「有人 defer Close 了它」的可能，而那等于当场杀掉执行者。
var jobHandle windows.Handle

// installProcessContainer 建 Job Object、设限制、把 shim 自己放进去。
//
// 参数：nprocLimit 为围栏值（执行者树的进程数上限）；<=0 表示不设进程数上限。
//
// 返回：任何一步失败都返回 error——见 shim.go 调用点的注释，Windows 上容器建不
// 起来意味着没有任何回收能力。
//
// 三处关键取舍：
//   - **job 无条件建**，即便 nprocLimit<=0。job 的首要用途是 KILL_ON_JOB_CLOSE
//     连坐回收，围栏只是搭车；照搬 unix 那个 `if limit > 0` 的闸门会把回收能力
//     一起跳过（spec 4.4.1）
//   - **job 必须归 shim 自己持有**。若由 agentd 建并持句柄，agentd 一重启句柄就
//     关，KILL_ON_JOB_CLOSE 当场收掉执行者，B36 的招牌属性「执行者活过 agentd
//     重启」当场失效（spec 4.2）
//   - **ActiveProcessLimit 要 +1**。它计的是 job 内进程数，而 shim 自己也在 job
//     里；策略层算出的值语义是「执行者树的进程数」（spec 4.4.3）
func installProcessContainer(nprocLimit int) error {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log().Error("创建 Job Object 失败", "cause", err)
		return fmt.Errorf("创建 Job Object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if nprocLimit > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = uint32(nprocLimit + 1)
	}
	if _, err := windows.SetInformationJobObject(h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		log().Error("设置 Job Object 限制失败", "limit", nprocLimit, "cause", err)
		return fmt.Errorf("设置 Job Object 限制: %w", err)
	}
	self, err := windows.GetCurrentProcess()
	if err != nil {
		windows.CloseHandle(h)
		log().Error("取当前进程句柄失败", "cause", err)
		return fmt.Errorf("取当前进程句柄: %w", err)
	}
	if err := windows.AssignProcessToJobObject(h, self); err != nil {
		windows.CloseHandle(h)
		log().Error("把 shim 放进 Job Object 失败", "cause", err)
		return fmt.Errorf("assign shim 进 Job Object: %w", err)
	}
	jobHandle = h // 故意不 Close：见 jobHandle 的注释
	if nprocLimit > 0 {
		log().Info("进程容器已安装", "kind", "job_object",
			"active_process_limit", nprocLimit+1, "fence", nprocLimit)
	} else {
		log().Info("进程容器已安装", "kind", "job_object", "active_process_limit", "未设",
			"reason", "spec 未下发围栏值")
	}
	return nil
}

// spawnDetached 以脱离本进程的方式拉起 shim，返回其 pid。
//
// 参数：
//   - argv: 完整命令行，argv[0] 必须是绝对路径（本函数不做 PATH 查找）
//   - dir: shim 的工作目录
//   - shimLog: shim 自身 stdout/stderr 的落盘文件
//
// 返回：shim 的 pid；error 非 nil 时没有进程被拉起。
//
// **CREATE_BREAKAWAY_FROM_JOB 是招牌属性在 Windows 上的承重点。** agentd 常常自己
// 就跑在别人的 job 里（计划任务会放，Windows OpenSSH 的会话也会放——后者已实测：
// 经 ssh 起的 agentd 在会话结束时被连坐杀掉）。若外层 job 带 KILL_ON_JOB_CLOSE，
// agentd 一停，shim 就跟着被外层 job 收掉，「执行者活过 agentd 重启」当场失效。
// 所以先尝试脱离父 job；被拒时回落并**大声说明**降级了什么。
func spawnDetached(argv []string, dir string, shimLog *os.File) (int, error) {
	const baseFlags = windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		cmd.Stdout = shimLog
		cmd.Stderr = shimLog
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		return cmd, cmd.Start()
	}
	cmd, err := start(baseFlags | windows.CREATE_BREAKAWAY_FROM_JOB)
	if err != nil {
		// 父 job 不允许 breakaway 时 CreateProcess 返回 ERROR_ACCESS_DENIED。
		// 这不是致命错误，但降级的后果必须让人看见。
		log().Warn("脱离父 job 失败，回落为不脱离；本机上执行者不保证活过 agentd 重启",
			"bin", argv[0], "cause", err)
		cmd, err = start(baseFlags)
		if err != nil {
			log().Error("拉起 shim 失败", "bin", argv[0], "dir", dir, "cause", err)
			return 0, fmt.Errorf("拉起 shim %s: %w", argv[0], err)
		}
		log().Info("shim 已拉起（未脱离父 job）", "pid", cmd.Process.Pid, "bin", argv[0])
		return cmd.Process.Pid, nil
	}
	log().Info("shim 已拉起（已脱离父 job）", "pid", cmd.Process.Pid, "bin", argv[0])
	return cmd.Process.Pid, nil
}

// killGroup 回收 shim 及其全部后代。
//
// 参数：pid 为 shim 的 pid。
//
// 实现上只杀 shim 一个进程——shim 的 job 句柄随其进程终止而关闭，
// KILL_ON_JOB_CLOSE 让内核收掉 job 内剩下的全部成员。所以一个裸 pid 就够，
// 不需要 OpenJobObject（它恰好是 x/sys/windows 唯一缺的那个函数）。
//
// 与 unix 的一处刻意差异：unix 上 shim 死后执行者被 init 收养继续跑（存活锁已释放
// → Alive() 报 false，模型与现实不符，是现存的一处 wart）；Windows 上整棵树跟着死，
// **现实与模型反而对得上**。这是变好不是变差，别当 bug 修回去。
func killGroup(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		log().Error("打开 shim 进程句柄失败", "pid", pid, "cause", err)
		return fmt.Errorf("打开进程 %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		log().Error("终止 shim 失败", "pid", pid, "cause", err)
		return fmt.Errorf("终止进程 %d: %w", pid, err)
	}
	log().Info("已终止 shim，job 将连坐收掉整棵树", "pid", pid)
	return nil
}

// killProc 终止单个进程（名册点名清扫用）。
//
// 与 killGroup 的区别只在语义：这里的 pid 是一个具体后代而非 shim，不期待连坐。
func killProc(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("打开进程 %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("终止进程 %d: %w", pid, err)
	}
	return nil
}
```

import 块最终形态：

```go
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)
```

- [ ] **Step 6: 编译门**

```bash
GOOS=windows GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go vet ./...
```
预期：无输出。`unsafe.Pointer` 的用法会被 vet 检查，若报错说明结构体传递写法有问题，必须改对而不是加 `//nolint`。

- [ ] **Step 7: 全量门 + 如实记账**

跑六条命令。ledger 里对本 task 写明：**Windows 运行期行为未验证**（mac 上跑不了），验证仅限编译与 vet。

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat(prochost): Windows 进程承载——detached spawn、Job Object 容器、按 job 回收"
```

---

## Task 4: 围栏值分平台——Windows 取 `TaskHardLimit`

**背景（spec 11.6）：** `fenceLimit()` 依赖 `procLimit()`，而后者是 procenum 的一部分，在 Windows 上返回未实现。结果是即便 job 能设 `ActiveProcessLimit`，也**算不出该设多少**，围栏静默缺席。解法不是回头做 procenum，而是承认 `reserve_ratio` 模型在 Windows 不适用。

**Files:**
- Modify: `internal/prochost/fence.go`
- Modify: `cmd/agentd.go:71`
- Test: `internal/prochost/fence_test.go`

**Interfaces:**
- Consumes: Task 2 的 `defaultFenceHardLimitMode` 常量（三个平台各一份）
- Produces:
  - `func SetFencePolicy(disabled bool, reserveRatio float64, taskHardLimit int)` —— **签名变更**，第三个参数是新的
  - 包级测试缝 `var fenceHardLimitMode = defaultFenceHardLimitMode`

- [ ] **Step 1: 写失败的测试**

在 `internal/prochost/fence_test.go` 追加：

```go
// TestFenceLimitHardLimitMode 钉住 Windows 模式下的围栏值来源。
//
// 为什么要有这条：Windows 没有 RLIMIT_NPROC 式的每用户上限，reserve_ratio 那套
// 算不出数（procLimit 返回未实现），照搬会让围栏静默缺席——真机日志实测过
// 「读不到系统进程上限，本次不设围栏」。本用例把「换一套取值来源」钉死。
func TestFenceLimitHardLimitMode(t *testing.T) {
	oldMode, oldHard, oldDisabled := fenceHardLimitMode, fenceTaskHardLimit, fenceDisabled
	t.Cleanup(func() {
		fenceHardLimitMode, fenceTaskHardLimit, fenceDisabled = oldMode, oldHard, oldDisabled
	})
	fenceHardLimitMode, fenceDisabled = true, false

	// 取 TaskHardLimit 原值，不做保留比例换算
	fenceTaskHardLimit = 1200
	got, err := fenceLimit()
	if err != nil || got != 1200 {
		t.Fatalf("硬上限模式应取 TaskHardLimit 原值：got=%d err=%v，want=1200 nil", got, err)
	}

	// TaskHardLimit==0 是「不启用该档」的合法表达，不是错误
	fenceTaskHardLimit = 0
	got, err = fenceLimit()
	if err != nil || got != 0 {
		t.Fatalf("TaskHardLimit=0 应为不设围栏且不报错：got=%d err=%v，want=0 nil", got, err)
	}

	// 全局关掉围栏优先于一切
	fenceDisabled, fenceTaskHardLimit = true, 1200
	got, err = fenceLimit()
	if err != nil || got != 0 {
		t.Fatalf("fenceDisabled 应优先：got=%d err=%v，want=0 nil", got, err)
	}
}

// TestFenceLimitHardLimitModeIgnoresProcLimit 证明硬上限模式**根本不碰** procLimit。
//
// 这是本次改动的要害：Windows 上 procLimit 返回错误，只要 fenceLimit 还调它，
// 围栏就还是算不出来。用一个必然失败的 procLimitFn 把这条路钉死。
func TestFenceLimitHardLimitModeIgnoresProcLimit(t *testing.T) {
	oldMode, oldHard, oldFn := fenceHardLimitMode, fenceTaskHardLimit, procLimitFn
	t.Cleanup(func() {
		fenceHardLimitMode, fenceTaskHardLimit, procLimitFn = oldMode, oldHard, oldFn
	})
	fenceHardLimitMode, fenceTaskHardLimit = true, 800
	procLimitFn = func() (int, error) { return 0, errors.New("本平台不支持进程枚举") }

	got, err := fenceLimit()
	if err != nil || got != 800 {
		t.Fatalf("硬上限模式不得依赖 procLimit：got=%d err=%v，want=800 nil", got, err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/prochost/ -run TestFenceLimitHardLimitMode -v
```
预期：编译失败，`undefined: fenceHardLimitMode` / `undefined: fenceTaskHardLimit`。

- [ ] **Step 3: 实现**

在 `internal/prochost/fence.go` 的策略 var 块（现约 28-32 行）改成：

```go
// 围栏策略。包级 var 而非常量：agentd 启动时由 config 经 SetFencePolicy 注入一次。
var (
	fenceDisabled     bool
	fenceReserveRatio = 0.1
	// fenceTaskHardLimit 是每任务进程数硬上限（config.proc_fence.task_hard_limit）。
	// 只在 fenceHardLimitMode 下使用；0 = 不启用该档。
	fenceTaskHardLimit int
)

// fenceHardLimitMode 决定围栏值的来源，由平台常量初始化，测试可覆盖。
//
// false（unix）：系统上限 × 保留比例，围栏是「每用户进程数」的一部分。
// true（Windows）：直接取 TaskHardLimit。理由见 spec 11.6——Windows 没有每用户
// 进程数上限这个概念，没有东西可供「保留一部分」；而 job 的 ActiveProcessLimit
// 与 TaskHardLimit 本就是同一个语义（每任务硬上限），用它实现甚至比现有的
// roster 事后清扫更强：内核当场拒绝 fork，不用等下一次采样。
//
// **不取 TaskBudget**：那是「叫醒人」的告警线（config.go:209），拿它去填一个内核
// 强制的硬上限，等于把一条本该只发事件的警告变成硬失败。
var fenceHardLimitMode = defaultFenceHardLimitMode
```

把 `fenceLimit()` 开头改成：

```go
func fenceLimit() (int, error) {
	if fenceDisabled {
		return 0, nil
	}
	if fenceHardLimitMode {
		// 不碰 procLimit：本平台读不到系统上限，碰了就等于围栏静默缺席
		return fenceTaskHardLimit, nil
	}
	limit, err := procLimitFn()
```
（其余不变。）

`SetFencePolicy` 改成：

```go
// SetFencePolicy 注入围栏策略，由 agentd 启动时按 config 调用一次。
//
// 参数：
//   - disabled: 全局关闭围栏
//   - reserveRatio: 保留比例（只在 unix 的比例模式下生效）
//   - taskHardLimit: 每任务进程数硬上限（只在 Windows 的硬上限模式下生效；0=不启用）
//
// 注意：shim 是独立进程、从不调用本函数，它拿到的围栏值来自 spec.NprocLimit
// （agentd 侧 applyFencePolicy 算好后下发），所以本函数只影响 agentd 侧的计算。
func SetFencePolicy(disabled bool, reserveRatio float64, taskHardLimit int) {
	fenceDisabled = disabled
	if reserveRatio > 0 {
		fenceReserveRatio = reserveRatio
	}
	fenceTaskHardLimit = taskHardLimit
	log().Info("进程围栏策略已设定", "disabled", disabled, "reserve_ratio", reserveRatio,
		"task_hard_limit", taskHardLimit, "hard_limit_mode", fenceHardLimitMode)
}
```

> 注意：若现有 `SetFencePolicy` 函数体与上面不同（比如 `reserveRatio` 的赋值条件不一样），**保留现有语义**，只增加 `fenceTaskHardLimit` 赋值与日志字段。不要顺手改现有行为。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/prochost/ -run TestFenceLimit -v
```
预期：新增两条 PASS，原有 fence 用例仍然 PASS。

- [ ] **Step 5: 接线 + 告警档缺席的 Warn**

`cmd/agentd.go:71` 改成：

```go
		prochost.SetFencePolicy(cfg.ProcFence.Disabled, cfg.ProcFence.ReserveRatio,
			cfg.ProcFence.TaskHardLimit)
```

并在同一处紧随其后追加（`runtime` 已 import 则直接用，否则补 import）：

```go
		// TaskBudget 告警档依赖 roster 计数（RunWatchdog → procenum），而 Windows 上
		// procenum 未实现。job 的 ActiveProcessLimit 能接管 TaskHardLimit（硬上限），
		// 但接管不了「数到 N 就叫醒人」——job 只会在上限处拒绝，中间没有回调。
		// 静默缺席正是本项目反复在防的东西，所以这里必须留一条明说的 Warn。
		if runtime.GOOS == "windows" && cfg.ProcFence.TaskBudget > 0 {
			logger.Warn("本平台不支持进程枚举，每任务进程预算告警档不生效",
				"task_budget", cfg.ProcFence.TaskBudget,
				"note", "硬上限档由 Job Object 接管，仍然生效")
		}
```

- [ ] **Step 6: 全量门**

跑六条命令。特别确认 `go test ./... -count=1` 里 `cmd` 包的既有断言没被 `SetFencePolicy` 签名变更打挂——若有测试调用它，一并改签名。

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(prochost): 围栏值分平台，Windows 取 task_hard_limit 并明示告警档缺席"
```

---

## Task 5: `pathenv` 在 Windows 跳过登录 shell 来源

**背景（spec 11.4，真机实测）：** Windows OpenSSH 会在会话里设 `SHELL=c:\windows\system32\cmd.exe`。于是 `pathenv` 不降级，而是真去跑 `cmd.exe -l -i -c`，cmd 不认这些参数、直接打欢迎横幅，那段横幅被 `filepath.SplitList` 当成**一个目录**追加进 agentd 的 PATH（登录 shell 来源不做 `dirExists` 校验）。

**Files:**
- Modify: `internal/pathenv/pathenv.go`
- Test: `internal/pathenv/pathenv_test.go`

**Interfaces:**
- Consumes: 无
- Produces: 包级测试缝 `var loginShellGOOS = runtime.GOOS`

- [ ] **Step 1: 写失败的测试**

在 `internal/pathenv/pathenv_test.go` 追加：

```go
// TestLoginShellDirsSkippedOnWindows 钉住「Windows 上不跑登录 shell」。
//
// 真机实测（2026-08-17，Server 2025）：Windows OpenSSH 会设 SHELL=cmd.exe，
// 于是这段逻辑不降级而是真去执行 cmd.exe，把它的欢迎横幅当成一个目录塞进 PATH。
// Windows 没有「登录 shell 的 rc 链」这个概念，这个来源在该平台本就无意义。
func TestLoginShellDirsSkippedOnWindows(t *testing.T) {
	oldGOOS := loginShellGOOS
	t.Cleanup(func() { loginShellGOOS = oldGOOS })
	loginShellGOOS = "windows"

	t.Setenv("SHELL", "c:\\windows\\system32\\cmd.exe")
	got := loginShellDirs(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got != nil {
		t.Fatalf("Windows 上不应产生任何登录 shell 目录，got=%v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/pathenv/ -run TestLoginShellDirsSkippedOnWindows -v
```
预期：编译失败 `undefined: loginShellGOOS`。

- [ ] **Step 3: 实现**

在 `internal/pathenv/pathenv.go` 加包级缝（放在其它包级 var 附近）：

```go
// loginShellGOOS 是平台判定的测试缝。**生产路径恒为 runtime.GOOS**，
// 非测试代码不得赋值。为什么要缝：Windows 分支在 mac/linux 的 CI 上永远测不到。
var loginShellGOOS = runtime.GOOS
```

把 `loginShellDirs` 开头改成：

```go
func loginShellDirs(ctx context.Context, log *slog.Logger) []string {
	// Windows 没有「登录 shell 的 rc 链」这个概念，这个来源在该平台本就无意义。
	// 而且不能只靠 $SHELL 为空来跳过——Windows OpenSSH 会把它设成 cmd.exe，
	// 于是这段会真去执行 cmd 并把它的欢迎横幅当成目录塞进 PATH（真机实测）。
	if loginShellGOOS == "windows" {
		log.Debug("Windows 平台跳过登录 shell 的 PATH 解析", "reason", "该平台无 rc 链概念")
		return nil
	}
	shell := os.Getenv("SHELL")
```
（其余不变。）

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/pathenv/ -count=1 -v
```
预期：新用例 PASS，原有用例全部仍然 PASS。

- [ ] **Step 5: 全量门**

跑六条命令。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "fix(pathenv): Windows 跳过登录 shell 来源，避免 cmd 横幅污染 PATH"
```

---

## Task 6: executor 注册层——Windows 不注册 claude 与 grok

**背景（spec 7.3）：** 真机实测五个 executor 全注册，claude 与 grok 会一路放行到运行期才炸。claude 尤其危险：它的 `Start` 第一步是建 AF_UNIX 裁决 socket，而 Go 在 Windows 10 1803+ 支持 AF_UNIX，**socket 可能真的建得起来**，然后走到输入通道才失败——留下一个半启动状态。

**Files:**
- Modify: `cmd/agentd.go:285-294`
- Test: `cmd/agentd_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `func adaptersFor(goos string, logger *slog.Logger) map[string]executor.Adapter`

- [ ] **Step 1: 写失败的测试**

在 `cmd/agentd_test.go` 追加：

```go
// TestAdaptersForWindowsExcludesUnsupported 钉住 Windows 上的诚实拒绝。
//
// 为什么在注册层而不是 Start 里报错：handoff status 会如实显示这台机器支持哪些
// 执行器，协调者在派发前就看得见，而不是任务跑到一半转 failed。
//
// claude：输入通道（命名管道）与 AF_UNIX 裁决 socket 都不在本轮范围。
// grok：taskenv 用 os.Symlink，Windows 上需 SeCreateSymbolicLinkPrivilege。
func TestAdaptersForWindowsExcludesUnsupported(t *testing.T) {
	got := adaptersFor("windows", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, name := range []string{"claude", "grok"} {
		if _, ok := got[name]; ok {
			t.Errorf("Windows 上不应注册 %s", name)
		}
	}
	for _, name := range []string{"opencode", "codex", "fake"} {
		if _, ok := got[name]; !ok {
			t.Errorf("Windows 上应注册 %s", name)
		}
	}
}

// TestAdaptersForUnixKeepsAll 钉住非 Windows 平台一个都不能少。
func TestAdaptersForUnixKeepsAll(t *testing.T) {
	got := adaptersFor("darwin", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, name := range []string{"opencode", "claude", "grok", "codex", "fake"} {
		if _, ok := got[name]; !ok {
			t.Errorf("darwin 上应注册 %s", name)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./cmd/ -run TestAdaptersFor -v
```
预期：编译失败 `undefined: adaptersFor`。

- [ ] **Step 3: 实现**

把 `cmd/agentd.go` 的 `defaultAdapters` 改成：

```go
// defaultAdapters 返回本机 agentd 的 executor 注册表（name → Adapter）。
//
// 抽成函数而非内联字面量：注册表是 dispatch --executor 路由的唯一真相，
// 漏注册的症状是「派发时报未注册」而不是编译错误，值得一条断言守着
// （见 agentd_test.go 的 TestAdapterRegistryHasAllExecutors）。
func defaultAdapters(logger *slog.Logger) map[string]executor.Adapter {
	return adaptersFor(runtime.GOOS, logger)
}

// adaptersFor 按平台裁剪 executor 注册表。
//
// 参数：goos 取 runtime.GOOS；抽成参数是为了让 Windows 分支能在 mac/linux 的 CI 上测到。
//
// 为什么在注册层拒绝而不是等 Start 报错：status 会如实显示这台机器支持哪些执行器，
// 协调者派发前就看得见，而不是任务跑到一半转 failed。对 claude 尤其重要——它的
// Start 第一步是建 AF_UNIX 裁决 socket，而 Go 在 Windows 10 1803+ 支持 AF_UNIX，
// socket 可能真的建得起来，然后走到输入通道才炸，留下一个 socket 建了、进程没起、
// 清理路径没人走过的半启动状态。与其让它走到那里，不如在门口就不放行。
//
// codex 照常注册但**记为「未验」而非「支持」**：它与 opencode 同为零 unix-ism，
// 没有已知阻断，不注册是对它的诬告；但本轮验收门只跑 opencode，codex 在 Windows
// 上一次都没跑过。
func adaptersFor(goos string, logger *slog.Logger) map[string]executor.Adapter {
	ads := map[string]executor.Adapter{
		"opencode": opencode.New(logger),
		"codex":    codex.New(logger),
		"fake":     fake.New(nil),
	}
	if goos == "windows" {
		logger.Warn("本平台不注册部分执行器",
			"skipped", []string{"claude", "grok"},
			"claude_reason", "输入通道（命名管道）与 AF_UNIX 裁决 socket 未实现（B37 第二批）",
			"grok_reason", "taskenv 用 os.Symlink，Windows 上需要特权")
		return ads
	}
	ads["claude"] = claudecode.New(logger)
	ads["grok"] = grok.New(logger)
	return ads
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./cmd/ -run TestAdapters -count=1 -v
```
预期：新增两条 PASS。**注意既有的 `TestAdapterRegistryHasAllExecutors`**：它断言注册表含全部五个，在 mac 上跑仍应通过（`defaultAdapters` 走 darwin 分支）；若它直接断言字面量而与新结构冲突，改测试去调 `adaptersFor("darwin", …)`，不要削弱断言。

- [ ] **Step 5: 全量门**

跑六条命令。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(agentd): Windows 上不注册 claude 与 grok，改为注册层诚实拒绝"
```

---

## Task 7: `initflow` 解禁 Windows 执行机角色

**Files:**
- Modify: `internal/initflow/initflow.go`（`RoleOptions`、`DefaultRole`）
- Modify: `internal/initflow/form.go:210-215`
- Test: `internal/initflow/initflow_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `RoleOptions("windows")` 返回三个角色

- [ ] **Step 1: 写失败的测试**

在 `internal/initflow/initflow_test.go` 追加：

```go
// TestRoleOptionsWindowsHasExecutor 钉住 Windows 执行机角色已解禁。
//
// B37 之前 Windows 只给协调者，因为 prochost 在该平台全是 not implemented；
// 进程承载层落地后，那个限制连同它的提示文案一起失效。
func TestRoleOptionsWindowsHasExecutor(t *testing.T) {
	got := RoleOptions("windows")
	if len(got) != 3 {
		t.Fatalf("Windows 上应有三个角色选项，got=%d: %+v", len(got), got)
	}
	want := map[string]bool{RoleExecutor: false, RoleCoordinator: false, RoleBoth: false}
	for _, o := range got {
		if _, ok := want[o.Value]; !ok {
			t.Errorf("出现预期外的角色 %q", o.Value)
		}
		want[o.Value] = true
	}
	for v, seen := range want {
		if !seen {
			t.Errorf("缺少角色 %q", v)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/initflow/ -run TestRoleOptionsWindowsHasExecutor -v
```
预期：FAIL，`Windows 上应有三个角色选项，got=1`。

- [ ] **Step 3: 实现**

`RoleOptions` 改成：

```go
// RoleOptions 返回本平台可选的角色列表。
//
// 参数：goos 取 runtime.GOOS；抽成参数是为了让平台分支在任意 CI 上测得到
//（判据写死则 Windows 分支在 linux 的 CI 上永远测不到）。
//
// 返回：角色选项列表。
//
// 注意：B37 之前 Windows 只给协调者，因为 agentd 的进程承载层在该平台全是
// not implemented。进程承载层落地后三个角色一律可选，本函数不再分平台。
func RoleOptions(goos string) []Option {
	return []Option{
		{Value: RoleExecutor, Label: "执行机"},
		{Value: RoleCoordinator, Label: "协调者"},
		{Value: RoleBoth, Label: "两者"},
	}
}
```

> `goos` 参数保留（调用方与既有测试都传它），但不再用于分支。若 linter 抱怨未使用参数，命名为 `_ string` 会破坏可读性——保留 `goos` 并在 doc 注释里说明它现在只是签名兼容即可。

`DefaultRole` 里删掉这一段：

```go
	// 预选项必须落在 RoleOptions 给出的列表里：Windows 上那个列表只有协调者，
	// 预选成执行机会让 huh 拿一个不在列表里的值去匹配，选中项落空
	if goos == "windows" {
		return RoleCoordinator
	}
```

并把 `RoleOptions` doc 里那句「Windows 上无条件返回协调者，见 RoleOptions」从
`DefaultRole` 的注释中删除。

`internal/initflow/form.go` 里这一段整体删除：

```go
		slog.Info("Windows 平台：角色选项限定为协调者", "reason", "agentd 进程承载层未实现（B37）")
		roleNotice = "注意：Windows 上 handoff 只能当协调者——agentd 的进程承载层在非 unix 平台尚未实现（backlog B37），执行机角色跑不起来。"
```

连同它所在的 `if` 分支一起删掉（保留 `roleNotice` 变量本身，若它还有别的赋值点）。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/initflow/ -count=1 -v
```
预期：新用例 PASS。**既有测试里可能有断言「Windows 只有协调者」的用例**——那是本 task 要推翻的旧约束，把它们改成断言新行为，并在测试注释里写明为什么反转（B37 落地）。不要删掉用例了事。

- [ ] **Step 5: 全量门**

跑六条命令。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(initflow): 解禁 Windows 执行机角色，删除已失效的提示文案"
```

---

## Task 8: `handoff run` 在 Windows 上定位 Git 自带的 `sh`

**背景（spec D5）：** `workspace.go` 里 `sh -c` 是写死的，Windows 上直接 `executable file not found`。走 Git for Windows 自带的 `sh` 可以让现有 plan 里的 unix 风格验证命令一行不用改。代价是执行机必须装完整 Git for Windows——MinGit 不带 `sh`，且 `sh.exe` 默认不在 PATH 上（真机实测：默认安装只把 `Git\cmd` 加进 PATH）。

**Files:**
- Modify: `internal/agentd/workspace.go`（`RunCmd` 约 1477 行）
- Create: `internal/agentd/runshell.go`
- Test: `internal/agentd/runshell_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `func runShellCandidates(goos string) []string` —— 候选绝对路径（不含 PATH 查找）
  - `func resolveRunShell(goos string, lookPath func(string) (string, error), stat func(string) error) (string, error)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/runshell_test.go`：

```go
package agentd

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestResolveRunShellUnix 钉住非 Windows 平台行为不变：就是 sh。
func TestResolveRunShellUnix(t *testing.T) {
	got, err := resolveRunShell("darwin",
		func(string) (string, error) { return "/bin/sh", nil },
		func(string) error { return nil })
	if err != nil || got != "sh" {
		t.Fatalf("非 Windows 应恒为 sh：got=%q err=%v", got, err)
	}
}

// TestResolveRunShellWindowsPrefersPath 钉住 PATH 优先。
func TestResolveRunShellWindowsPrefersPath(t *testing.T) {
	got, err := resolveRunShell("windows",
		func(name string) (string, error) {
			if name == "sh" {
				return `C:\somewhere\sh.exe`, nil
			}
			return "", exec.ErrNotFound
		},
		func(string) error { return errors.New("不该走到 stat") })
	if err != nil || got != `C:\somewhere\sh.exe` {
		t.Fatalf("PATH 上有 sh 时应直接用：got=%q err=%v", got, err)
	}
}

// TestResolveRunShellWindowsFallsBackToKnownDir 钉住兜底目录。
//
// 这是真实用户机器上的常态：Git for Windows 默认安装只把 Git\cmd 加进 PATH，
// sh.exe 所在的 Git\bin 不在 PATH 上（真机实测）。
func TestResolveRunShellWindowsFallsBackToKnownDir(t *testing.T) {
	want := `C:\Program Files\Git\bin\sh.exe`
	got, err := resolveRunShell("windows",
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(p string) error {
			if p == want {
				return nil
			}
			return errors.New("不存在")
		})
	if err != nil || got != want {
		t.Fatalf("应回落到 Git 默认安装路径：got=%q err=%v，want=%q", got, want, err)
	}
}

// TestResolveRunShellWindowsAllMissing 钉住「全落空要给可行动的话」。
func TestResolveRunShellWindowsAllMissing(t *testing.T) {
	_, err := resolveRunShell("windows",
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(string) error { return errors.New("不存在") })
	if err == nil {
		t.Fatal("全落空必须报错，不得静默降级到 cmd/PowerShell")
	}
	if !strings.Contains(err.Error(), "Git for Windows") {
		t.Fatalf("错误必须指出装什么才能修：got=%v", err)
	}
}

// TestRunShellCandidatesOrder 钉住候选顺序：默认安装位置在前。
func TestRunShellCandidatesOrder(t *testing.T) {
	got := runShellCandidates("windows")
	if len(got) == 0 || got[0] != `C:\Program Files\Git\bin\sh.exe` {
		t.Fatalf("首选应是 64 位默认安装位置：got=%v", got)
	}
	if len(runShellCandidates("darwin")) != 0 {
		t.Fatal("非 Windows 不应有候选目录")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestResolveRunShell|TestRunShellCandidates' -v
```
预期：编译失败 `undefined: resolveRunShell` / `undefined: runShellCandidates`。

- [ ] **Step 3: 实现**

创建 `internal/agentd/runshell.go`：

```go
// runshell.go —— handoff run 的 shell 解析。
//
// 职责：为 RunCmd 选出执行 `-c <cmdline>` 的 shell 可执行文件路径。
//
// 边界：
//   - 只做解析，不执行、不拼参数（那是 RunCmd 的事）
//   - 不做 shell 方言转换：本文件的全部意义就是让协调者写的 unix 风格命令
//     在两个平台上是同一句话
package agentd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// runShellCandidates 返回本平台上 sh 的已知安装位置（绝对路径，按优先级）。
//
// 参数：goos 取 runtime.GOOS；抽成参数是为了让 Windows 分支在 mac/linux 上测得到。
//
// 返回：非 Windows 恒为空——那些平台上 sh 一定在 PATH 上，不需要兜底。
//
// 为什么必须有兜底：Git for Windows 的默认安装只把 Git\cmd 加进 PATH，而 sh.exe
// 住在 Git\bin，**默认不在 PATH 上**（真机实测）。只靠 LookPath 会在绝大多数正常
// 安装的机器上失败。这与 internal/pathenv 的「已知安装目录兜底」是同一个模式。
func runShellCandidates(goos string) []string {
	if goos != "windows" {
		return nil
	}
	var out []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		if env == "LOCALAPPDATA" {
			out = append(out, filepath.Join(root, "Programs", "Git", "bin", "sh.exe"))
			continue
		}
		out = append(out, filepath.Join(root, "Git", "bin", "sh.exe"))
	}
	// 环境变量缺失时的硬兜底：默认安装位置是确定的
	out = append(out, `C:\Program Files\Git\bin\sh.exe`, `C:\Program Files (x86)\Git\bin\sh.exe`)
	return dedupStrings(out)
}

// dedupStrings 按首次出现顺序去重。
func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// resolveRunShell 解析 run 命令要用的 shell。
//
// 参数：
//   - goos: 平台标识（生产路径传 runtime.GOOS）
//   - lookPath: PATH 查找函数（生产路径传 exec.LookPath）
//   - stat: 文件存在性判定（生产路径传 statFile）
//
// 返回：
//   - shell 的路径或名字，可直接交给 exec.CommandContext
//   - error: 只在 Windows 上找不到任何 sh 时返回，文案指出装什么能修
//
// 为什么找不到时**硬失败**而不是降级到 cmd 或 PowerShell：那会让协调者写的
// unix 风格命令（管道、$(…)、&&）以难以理解的方式半跑，排障成本远高于一条
// 明确的「请装 Git for Windows」。
func resolveRunShell(goos string, lookPath func(string) (string, error), stat func(string) error) (string, error) {
	if goos != "windows" {
		return "sh", nil
	}
	if p, err := lookPath("sh"); err == nil && p != "" {
		log().Info("run 的 shell 解析自 PATH", "sh", p)
		return p, nil
	}
	cands := runShellCandidates(goos)
	for _, c := range cands {
		if err := stat(c); err == nil {
			log().Info("run 的 shell 解析自已知安装目录", "sh", c)
			return c, nil
		}
	}
	log().Error("找不到 sh，run 命令无法执行", "candidates", cands)
	return "", fmt.Errorf("找不到 sh：请在本机安装完整的 Git for Windows"+
		"（MinGit 不带 sh），已查找 PATH 与 %v", cands)
}

// statFile 是 resolveRunShell 的生产存在性判据。
func statFile(p string) error {
	_, err := os.Stat(p)
	return err
}

// runShell 是 RunCmd 的调用入口，把生产依赖接上。
func runShell() (string, error) {
	return resolveRunShell(runtime.GOOS, exec.LookPath, statFile)
}
```

在 `internal/agentd/workspace.go` 的 `RunCmd` 里，把

```go
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
```

改成

```go
	sh, err := runShell()
	if err != nil {
		log().Error("run 命令的 shell 解析失败", "repo", repo, "cause", err)
		return "", -1, err
	}
	cmd := exec.CommandContext(ctx, sh, "-c", cmdline)
```

> 注意 `RunCmd` 的返回值命名是 `(stdout string, exitCode int, err error)`，函数体内已有若干 `err` 变量；若 `:=` 与命名返回值冲突，改用 `sh, serr := runShell()` 并判 `serr`。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run 'TestResolveRunShell|TestRunShellCandidates' -count=1 -v
```
预期：五条全 PASS。

- [ ] **Step 5: 确认既有 run 用例没被打挂**

```bash
go test ./internal/agentd/ -count=1
```
预期：全绿。mac 上 `resolveRunShell` 走 `goos != "windows"` 分支返回 `"sh"`，行为与改动前完全一致。

- [ ] **Step 6: 全量门**

跑六条命令。

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(agentd): run 在 Windows 上定位 Git 自带的 sh，找不到时给可行动错误"
```

---

## Task 9: `RemoveManagedWorktree` 重试退避

**背景（spec 6.1）：** `Kill` 的复核判据是 **shim 的存活锁**，而执行者子进程是被 `KILL_ON_JOB_CLOSE` 连坐杀掉的。shim 拆解时「锁文件句柄释放」与「job 连坐」是两个并列后果，内核不保证顺序。于是存在一个窗口：`Alive()` 已转 false、`Kill` 已返回，而执行者子进程仍活着——它的 cwd 是那棵 worktree，在 Windows 上等于一个不带 `FILE_SHARE_DELETE` 的目录句柄，`git worktree remove` 必然失败。

**注意：不要按 08-10 成本清单原文去「kill 后加等待」**——`Kill` 在 B47 之后已经是同步复核的（`prochost.go:180` 注释写明），那样会把等待加在一个已经等过的地方。

**Files:**
- Modify: `internal/agentd/workspace.go`（`RemoveManagedWorktree`，约 717-739 行）
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `var removeWorktreeAttempts = 5`（测试缝）
  - `var removeWorktreeBackoff = 400 * time.Millisecond`（测试缝）

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/workspace_test.go` 追加：

```go
// TestRemoveManagedWorktreeRetries 钉住重试语义。
//
// 为什么改函数内部而不是调用点：实际有四个调用点（workspace.go 的派发失败补偿、
// manager.go 的 done/stop/失配三处），改函数一处覆盖全部，也不会漏掉将来新增的。
//
// 为什么不去等子进程：child.pid 虽然有，但用 pid 等存活会重新引入 pid 复用误判
// ——那正是整个 prochost 用文件锁而非 pid 判存活的原因。
func TestRemoveManagedWorktreeRetries(t *testing.T) {
	oldAttempts, oldBackoff := removeWorktreeAttempts, removeWorktreeBackoff
	t.Cleanup(func() { removeWorktreeAttempts, removeWorktreeBackoff = oldAttempts, oldBackoff })
	removeWorktreeAttempts, removeWorktreeBackoff = 3, time.Millisecond

	calls := 0
	oldRun := worktreeRemoveFn
	t.Cleanup(func() { worktreeRemoveFn = oldRun })
	worktreeRemoveFn = func(ctx context.Context, repo, workdir string) (string, error) {
		calls++
		if calls < 3 {
			return "fatal: 'x' contains modified or untracked files", errors.New("exit 128")
		}
		return "", nil
	}

	if err := RemoveManagedWorktree(context.Background(), "/repo", "/wt"); err != nil {
		t.Fatalf("第三次应成功：%v", err)
	}
	if calls != 3 {
		t.Fatalf("应重试到成功为止：calls=%d want=3", calls)
	}
}

// TestRemoveManagedWorktreeExhausted 钉住耗尽后仍返回错误（调用方据此只 Warn 不阻断）。
func TestRemoveManagedWorktreeExhausted(t *testing.T) {
	oldAttempts, oldBackoff := removeWorktreeAttempts, removeWorktreeBackoff
	t.Cleanup(func() { removeWorktreeAttempts, removeWorktreeBackoff = oldAttempts, oldBackoff })
	removeWorktreeAttempts, removeWorktreeBackoff = 2, time.Millisecond

	calls := 0
	oldRun := worktreeRemoveFn
	t.Cleanup(func() { worktreeRemoveFn = oldRun })
	worktreeRemoveFn = func(ctx context.Context, repo, workdir string) (string, error) {
		calls++
		return "被占用", errors.New("exit 128")
	}

	err := RemoveManagedWorktree(context.Background(), "/repo", "/wt")
	if err == nil {
		t.Fatal("耗尽后必须返回错误")
	}
	if calls != 2 {
		t.Fatalf("应按 removeWorktreeAttempts 次数重试：calls=%d want=2", calls)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestRemoveManagedWorktree -v
```
预期：编译失败 `undefined: removeWorktreeAttempts` / `undefined: worktreeRemoveFn`。

- [ ] **Step 3: 实现**

把 `RemoveManagedWorktree` 改成：

```go
// removeWorktreeAttempts / removeWorktreeBackoff 是删 worktree 的重试参数。
//
// 为什么需要重试：Kill 的复核判据是 shim 的存活锁，而执行者子进程是被 job 的
// KILL_ON_JOB_CLOSE 连坐杀掉的。shim 拆解时「锁释放」与「连坐」是两个并列后果，
// 内核不保证顺序——于是存在一个窗口：Alive() 已转 false，而执行者仍活着，它的
// cwd 正是这棵 worktree（Windows 上等于一个不带 FILE_SHARE_DELETE 的目录句柄）。
//
// unix 上第一次就会成功（允许删除作为他人 cwd 的目录），重试是零代价；统一启用
// 是为了避免出现一条只在 Windows 上走过的路径。
//
// 是变量而非常量：测试要把它们调到毫秒级，否则每条用例都真等几秒。
var (
	removeWorktreeAttempts = 5
	removeWorktreeBackoff  = 400 * time.Millisecond
)

// worktreeRemoveFn 是单次 git worktree remove 的测试缝。
// **生产路径恒为下面的默认值**，非测试代码不得赋值。
var worktreeRemoveFn = func(ctx context.Context, repo, workdir string) (string, error) {
	_, stderr, err := gitRun(ctx, repo, "worktree", "remove", workdir)
	return stderr, err
}

// RemoveManagedWorktree 删除 agentd 管理的 worktree（git -C repo worktree remove workdir）。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期，内部再叠加 WorkspaceGitTimeout 作为兜底上限
//   - repo: 主仓库路径
//   - workdir: 待删除的 worktree 路径（必须为 Managed=true 的工作区）
//
// 返回：error 非 nil 表示重试耗尽仍未删掉；调用方按现状只 Warn 不阻断。
//
// 注意：
//   - 只删工作树不删分支（spec：任务分支保留供审阅/回滚）
//   - workdir 带未提交改动时 git 拒绝删除（错误带 stderr 原文返回）；是否降级
//     由调用方（Done 归档）决定——本函数不做清理性降级
//   - 失败会重试若干次，见 removeWorktreeAttempts 的注释
func RemoveManagedWorktree(ctx context.Context, repo, workdir string) error {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	log().Info("删除 managed worktree", "repo", repo, "workdir", workdir,
		"timeout", WorkspaceGitTimeout, "attempts", removeWorktreeAttempts)
	var lastStderr string
	var lastErr error
	for i := 1; i <= removeWorktreeAttempts; i++ {
		stderr, err := worktreeRemoveFn(ctx, repo, workdir)
		if err == nil {
			log().Info("managed worktree 已删除", "repo", repo, "workdir", workdir, "attempt", i)
			return nil
		}
		lastStderr, lastErr = stderr, err
		if i < removeWorktreeAttempts {
			// 常见成因是执行者进程还没被内核收干净、cwd 句柄仍钉着这棵树；
			// 退一步等它散场，比当场放弃留一棵残树划算
			log().Warn("删除 managed worktree 失败，稍后重试", "repo", repo, "workdir", workdir,
				"attempt", i, "of", removeWorktreeAttempts,
				"stderr", truncateRunes(stderr, 300), "cause", err)
			select {
			case <-ctx.Done():
				log().Error("删除 managed worktree 被取消", "repo", repo, "workdir", workdir,
					"attempt", i, "cause", ctx.Err())
				return fmt.Errorf("git worktree remove %s: %w", workdir, ctx.Err())
			case <-time.After(removeWorktreeBackoff):
			}
		}
	}
	log().Error("删除 managed worktree 失败（重试耗尽）", "repo", repo, "workdir", workdir,
		"attempts", removeWorktreeAttempts, "stderr", truncateRunes(lastStderr, 300), "cause", lastErr)
	return fmt.Errorf("git worktree remove %s: %s: %w", workdir, strings.TrimSpace(lastStderr), lastErr)
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run TestRemoveManagedWorktree -count=1 -v
```
预期：两条 PASS。

- [ ] **Step 5: 全量门**

跑六条命令。既有调用 `RemoveManagedWorktree` 的用例（done/stop/补偿路径）必须仍然全绿——**若某条用例因为重试而变慢到超时**，把该用例里的 `removeWorktreeAttempts` 也调小，不要改生产默认值。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "fix(agentd): RemoveManagedWorktree 加重试退避，兜住 job 连坐与锁释放的顺序竞态"
```

---

## Task 10: 注释订正与诊断改进（收尾）

**为什么单独一个 task：** 这几处都是「代码行为不变，但留在原样会误导后人做出错误决策」的注释与文案。放在最后做，是因为它们描述的正是前面九个 task 落地后的新事实。

**Files:**
- Modify: `internal/prochost/procenum_other.go`
- Modify: `internal/executor/opencode/proc.go`（就绪超时错误）
- Modify: `internal/agentd/lock.go`（自相矛盾的两行日志）

**Interfaces:**
- Consumes: Task 3（job 已承担回收职责）、Task 2（`lockSupported` 在 Windows 上为 true）
- Produces: 无新符号

- [ ] **Step 1: 改写 `procenum_other.go` 的注释**

把文件头注释改成：

```go
//go:build !darwin && !linux

// procenum_other.go —— 非 darwin/linux 的空实现。
//
// 一律返回 errNotSupported 而不是空集：调用方必须据此降级为「未知」，
// 而不是渲染出一个 0 让人误以为足迹是空的（见 procenum.go 的 why）。
//
// **Windows 上这个缺席的含义与其它平台不同，别照字面理解**：进程回收职责已由
// Job Object 承担（shim 持 KILL_ON_JOB_CLOSE 的 job，子进程无法自行逃逸），
// 所以这里返回未实现**不**意味着「进程可能逃逸没人管」。缺的只是「足迹观测」
// ——看不到某个任务开了多少进程，以及依赖计数的 TaskBudget 告警档。
// 硬上限档（TaskHardLimit）由 job 的 ActiveProcessLimit 接管，仍然生效。
// 详见 spec 2026-08-17-windows-native-executor-design.md 的 3.2 与 11.6.1。
package prochost
```

- [ ] **Step 2: opencode 就绪超时错误带上 `bin`**

在 `internal/executor/opencode/proc.go` 的就绪超时分支（约 183-190 行），把

```go
			return nil, fmt.Errorf("opencode serve 就绪超时（10s）: %s", stderrTail)
```

改成

```go
			// bin 必须进错误文本：Windows 上桌面 GUI 与 CLI 同名，LookPath 可能
			// 解析到 OpenCode.exe（桌面版），它起来不会 listen 这个端口，症状就是
			// 这里超时。只说「超时」会让人去查端口和配置，而真因是解析错了文件。
			return nil, fmt.Errorf("opencode serve 就绪超时（10s，bin=%s）: %s", bin, stderrTail)
```

同一分支的 `l.Error(...)` 追加 `"bin", bin` 字段。

- [ ] **Step 3: 修掉自相矛盾的两行启动日志**

`internal/agentd/lock.go` 里现在先 Warn「本平台不支持文件锁，单实例保护未生效」，紧接着 Info「已取得数据目录单实例锁」——第二行在锁不支持时不该说「已取得」。把成功日志改成条件文案：

```go
	if prochost.LockSupported() {
		log.Info("已取得数据目录单实例锁", "data_dir", dataDir, "path", path)
	} else {
		log.Info("数据目录锁文件已就位，但本平台不提供互斥保护",
			"data_dir", dataDir, "path", path)
	}
```

> 定位：把现有那条「已取得数据目录单实例锁」的 `log.Info` 换成上面这段。若该行在 `AcquireDataDirLock` 成功返回前的别处，按同样思路就地改，不要新增函数。

- [ ] **Step 4: 全量门**

跑六条命令。

- [ ] **Step 5: 整分支终审**

对本分支相对起点的完整 diff 做一次通读，逐条核对 Global Constraints：

- 每个新文件都有文件头注释（职责 + 边界）
- 每个新增导出函数都有 doc 注释（参数、返回、注意事项）
- 每个错误分支都有带上下文的日志（不是裸 `return err`）
- 成功路径也有日志（不存在静默成功）
- 没有 `fmt.Printf` / `println` 当日志
- 没有 `TODO` / 占位注释残留
- `gofmt -l $(git ls-files '*.go')` 无输出

有发现项就一次性全量修，再做一次范围复审。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "docs(prochost): 订正 Windows 语义注释，补 opencode/锁的诊断文案"
```

---

## 计划自审记录

**Spec 覆盖核对：**

| spec 条目 | 落在哪个 task |
|---|---|
| 4.1 进程容器抽象 | Task 3 Step 1/3/5 |
| 4.2 job 归 shim 持有 | Task 3 Step 5（`jobHandle` 注释 + `installProcessContainer`） |
| 4.3 killGroup 两步 | Task 3 Step 5 |
| 4.4.1 无条件建 job | Task 3 Step 5 |
| 4.4.2 失败语义相反 | Task 3 Step 1（unix 返回 nil）/ Step 5（windows 返回 error）/ Step 3（调用点注释） |
| 4.4.3 off-by-one | Task 3 Step 5（`nprocLimit + 1`） |
| 4.5 CREATE_BREAKAWAY_FROM_JOB | Task 3 Step 5（`spawnDetached`） |
| 4.6 刻意的行为差异 | Task 3 Step 5（`killGroup` doc 注释） |
| 五、LockFileEx 三原语 | Task 2 Step 2 |
| 六、initflow 角色 | Task 7 |
| 六、run 的 sh | Task 8 |
| 6.1 E2 修正版 | Task 9 |
| 3.5 opencode 错误带 bin | Task 10 Step 2 |
| 7.3 注册层诚实拒绝 | Task 6 |
| 7.4 注释订正 | Task 10 Step 1；`errNotImplemented` 注释在 Task 2 Step 2 |
| 7.5 build tag 调整 | Task 2 Step 1（`platform_other.go`）；`fence_other.go` 按 spec 订正**不动** |
| 8.3 测试 tag + CI vet 门 | Task 1 |
| 11.4 pathenv | Task 5 |
| 11.6 围栏取 TaskHardLimit | Task 4 |
| 11.6.1 TaskBudget 告警缺席的 Warn | Task 4 Step 5 |
| 11.8 锁日志自相矛盾 | Task 10 Step 3 |

**不在本计划内（spec 第十节第二批，已确认为非目标）：** 命名管道与 claude、AF_UNIX 裁决 socket、ConPTY、Windows Service 托管、`pathenv` 注册表解析、`handoff pull` 路径冒号、权限位 ACL、grok、`procenum` 的 Toolhelp32 实现、F3 的项目名派生分隔符。

**类型一致性核对：** `installProcessContainer(int) error` 在三个平台文件签名一致；`resolveRunShell` / `runShellCandidates` 在 Task 8 的测试与实现中签名一致；`SetFencePolicy` 三参在 Task 4 的实现与 `cmd/agentd.go` 调用点一致；`worktreeRemoveFn` 的签名 `(context.Context, string, string) (string, error)` 在 Task 9 的测试与实现中一致。

**已知的计划风险：** Task 1 的工作清单由实测产生而非预先枚举，其规模在开工前不可知；若 `GOOS=windows go vet ./...` 的失败面远大于 spec 2.5 的估计（十余个文件），先把清单报上来再动手，不要闷头改。
