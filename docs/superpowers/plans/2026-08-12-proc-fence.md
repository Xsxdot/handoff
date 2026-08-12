# 进程围栏与资源耗尽归因 实现计划（Plan A / B73）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 executor 树再怎么扇出也顶不穿机器的进程表，并让撞墙时的报错在 agentd 日志、审核者、执行者三处都能被认出是「配额耗尽」而不是代码 bug。

**Architecture:** 机制是一条内核原语——shim 在拉起 executor 之前把**自己**的 `RLIMIT_NPROC` 软硬限一起压到 L，executor 及其全部后代继承这个配额；rlimit 跟进程走不跟进程组走，`setsid` 逃得出进程组也逃不出围栏。策略层（L 怎么算、余量怎么判读、EAGAIN 怎么翻译）集中在 `internal/prochost/fence.go`，全部走 sysctl / setrlimit / `/proc`，**一次 fork 都不做**——这套代码必须在机器已经 fork 不动的时候仍然可用。agentd 侧只做两件事：把 config 的策略注入一次，在四个进程创建点把 EAGAIN 翻译成人话。

**Tech Stack:** Go 1.x、`golang.org/x/sys/unix`（仓库已依赖）、既有 `internal/prochost` 原语（`enumProcs` / `procLimit` / `UIDUsage`）、`internal/store` + `Hub` 事件通道。

**Spec:** `docs/superpowers/specs/2026-08-12-proc-fence-and-registry-design.md`（本计划覆盖 §2、§3；§4 出生登记是 Plan B，不在本计划范围）。

## 依赖前提（先读，否则第一步就会踩空）

本计划**依赖 B69/B70 已交付的进程足迹原语**，它们在分支 `feat/b69-b70-proc-footprint`（收尾提交 `d06f9837`）上，**尚未合入 main**。本计划的分支必须以该分支为基线，否则 `enumProcs` / `procLimit` / `UIDUsage` / `enumProcsFn` 全部不存在。

开工前确认下面四样东西都在（缺任何一样就是基线选错了，停下来报告）：

```bash
git show --stat HEAD -- internal/prochost/footprint.go internal/prochost/procenum.go
grep -n "var enumProcsFn" internal/prochost/footprint.go
grep -n "func procLimit" internal/prochost/procenum_darwin.go
grep -n "func UIDUsage" internal/prochost/footprint.go
```

## Global Constraints

- **防线全链路零 fork。** 本计划新增/修改的代码里，围栏、归因、准入闸、水位判读四条路径上出现任何 `exec.Command` / `os/exec` 即为实现失败。理由写在 `internal/prochost/procenum.go` 的文件头边界里：2026-08-12 devbox 整机 fork 瘫痪时，连 `ps | wc -l` 都起不来，所有基于 exec 的诊断手段同时失效。
- **fail-open，不 fail-closed。** 读不到数、算不出 L、装不上围栏——一律降级为「本次无保护」并打日志，**绝不阻断业务**。防护装置故障不该变成拒绝服务。
- **不猜值。** 判不出结论时如实呈现「未知」，不得回退成 0 或编一个像模像样的结论。这是 B69 `Verdict` 三态立下的纪律，本计划全盘沿用。
- **日志用 `slog`（包内经 `log()`），禁止 `fmt.Printf`。** 每个实现 task 都带「加关键节点日志」与「加注释」两个 step，它们不是可选的（项目 CLAUDE.md 硬要求 + `instrumenting-code` skill）。
- **六闸门全绿才算完工**：`go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./... -count=1`、`go test -race ./internal/prochost/ ./internal/agentd/`、`GOOS=windows go build ./...`。
- **进程足迹要克制**：这台机器 2026-08-12 刚因每 uid 进程数耗尽整机瘫痪过。同时最多 3 个并发 subagent，不要在多个 subagent 里同时跑全量 `go test ./...`。

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/prochost/fence_unix.go` | 新建 | darwin/linux 的 setrlimit 原语（`setNprocLimit` / `getNprocLimit`） |
| `internal/prochost/fence_other.go` | 新建 | 其它平台一律 `errFenceNotSupported` |
| `internal/prochost/fence.go` | 新建 | 策略层：`SetFencePolicy` / `fenceLimit` / `applyFencePolicy` / `CheckAdmission` / `ExplainForkFailure` |
| `internal/prochost/fence_test.go` | 新建 | 策略层纯逻辑测试（经 `procLimitFn` / `enumProcsFn` 缝喂假数据） |
| `internal/prochost/fence_inherit_test.go` | 新建 | 真进程继承性测试（setsid 后仍带围栏） |
| `internal/prochost/prochost.go` | 改 | `Spec` 加 `NprocLimit`；`Start` 调 `applyFencePolicy` |
| `internal/prochost/shim.go` | 改 | `RunShim` 在 `cmd.Start()` 前安装围栏；spawn 失败时归因 |
| `internal/prochost/platform_unix.go` | 改 | `spawnDetached` 失败时归因 |
| `internal/agentd/admission.go` | 新建 | 准入闸：`ErrNoProcHeadroom` / `admissionFn` 缝 / `checkProcHeadroom` |
| `internal/agentd/workspace.go` | 改 | `quotaNote` 助手；`gitRun` / `RunCmd` 失败时归因；`RunCmd` 准入闸 |
| `internal/agentd/manager.go` | 改 | `Dispatch` 准入闸；`failedPayload` 加占用快照 + `newFailedPayload` |
| `internal/agentd/reconcile.go` | 改 | failed 事件改走 `newFailedPayload` |
| `internal/agentd/server.go` | 改 | `ErrNoProcHeadroom` 映射为 400 |
| `internal/agentd/watchdog.go` | 改 | 高水位 `resource_pressure` 事件（越线沿触发） |
| `internal/agentd/workspace_fence_test.go` | 新建 | 归因文案与准入闸测试（含 `fakeAdmission` 缝助手） |
| `internal/agentd/watchdog_fence_test.go` | 新建 | 高水位越线沿与失败快照测试 |
| `internal/proto/proto.go` | 改 | `EventTypeResourcePressure` 常量 |
| `internal/config/config.go` | 改 | `ProcFence` 配置段 |
| `cmd/agentd.go` | 改 | bootstrap 注入围栏策略 |
| `README.md` | 改 | `proc_fence` 配置说明 + 归因文案含义 + 派发指令资源纪律 |

---

## Task 1: 围栏平台原语（setrlimit）

**Files:**
- Create: `internal/prochost/fence_unix.go`
- Create: `internal/prochost/fence_other.go`
- Test: `internal/prochost/fence_test.go`（本 task 只加平台原语的用例，策略层用例在 Task 2 追加到同一文件）

**Interfaces:**
- Consumes: 无（本 task 是最底层原语）
- Produces: `func setNprocLimit(n int) error`、`func getNprocLimit() (int, error)`、`var errFenceNotSupported error` — 包内可见，Task 2/3 使用

- [ ] **Step 1: 写失败的测试**

创建 `internal/prochost/fence_test.go`：

```go
package prochost

import (
	"errors"
	"runtime"
	"testing"
)

// 围栏原语在受支持平台上必须能读到一个正数上限；不支持的平台必须明确报
// errFenceNotSupported 而不是返回 0——0 会被误读成「上限为零」。
func TestGetNprocLimitReportsPositiveOrNotSupported(t *testing.T) {
	n, err := getNprocLimit()
	switch runtime.GOOS {
	case "darwin", "linux":
		if err != nil {
			t.Fatalf("受支持平台读上限失败: %v", err)
		}
		if n <= 0 {
			t.Fatalf("上限应为正数，得到 %d", n)
		}
	default:
		if !errors.Is(err, errFenceNotSupported) {
			t.Fatalf("不支持的平台应返回 errFenceNotSupported，得到 %v", err)
		}
	}
}

// 非正数围栏值是调用方的 bug，必须当场拒绝：把 RLIMIT_NPROC 设成 0 会让
// 这个进程再也 fork 不出任何东西，是不可逆的自杀。
func TestSetNprocLimitRejectsNonPositive(t *testing.T) {
	for _, n := range []int{0, -1} {
		if err := setNprocLimit(n); err == nil {
			t.Fatalf("围栏值 %d 应被拒绝，却返回了 nil", n)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestGetNprocLimit|TestSetNprocLimitRejects' -v`
Expected: FAIL，`undefined: getNprocLimit` / `undefined: setNprocLimit` / `undefined: errFenceNotSupported`

- [ ] **Step 3: 写实现**

创建 `internal/prochost/fence_unix.go`：

```go
//go:build darwin || linux

// fence_unix.go —— 进程围栏的类 Unix 实现（setrlimit RLIMIT_NPROC）。
//
// 职责：
//   - setNprocLimit：把当前进程的 RLIMIT_NPROC 软硬限一起压到给定值
//   - getNprocLimit：读回当前软限（自检与测试用）
//
// 边界：
//   - 只动**调用者自己**的 rlimit，不影响同 uid 的其它进程；子孙靠继承拿到
//   - 不决定围栏值取多少（那是 fence.go 的策略层），不判断该不该装
//   - 不 fork：setrlimit 是纯系统调用，这是本包零 fork 约束的一部分
package prochost

import (
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

// setNprocLimit 把当前进程的 RLIMIT_NPROC 软硬限一起压到 n。
//
// 参数：n 为围栏值（该 uid 可同时存在的进程数上限），必须为正数
//
// 返回：n 非正数、或 setrlimit 失败时返回错误。调用方应降级为「本次无围栏」
// 并继续，绝不因为防护装置装不上就中断业务。
//
// 注意：
//   - **软硬限必须同设**。只压软限的话，被围住的进程一句 setrlimit 就能把
//     软限抬回硬限，围栏形同虚设；硬限只能降不能升（升需特权），两者同设
//     即构成一扇单向门，executor 及其全部后代都拆不掉。
//   - 限值随 fork/exec 继承，且 **setsid 不重置它**——这正是围栏能覆盖
//     「逃逸出进程组的后代」的原因（2026-08-12 真机验证：进程 setsid 到
//     pid==sid==pgid 的完全独立会话后，rlimit 原样保留）。按进程组回收
//     收不到的那些树，在这里一个也跑不掉。
//   - 计数口径是**整个 uid**而不是本进程树：内核 fork 时拿「调用者自己的
//     限值」比「该 uid 当前活着的进程总数」。这个语义正是围栏能自动跨任务
//     合成的原因——所有 executor 树设同一个 L，uid 总数就不会因它们的
//     fork 越过 L，不需要任何任务间协调。
//   - linux 的 RLIMIT_NPROC 把线程也算进去，而本包 enumProcs 只数进程，
//     两边口径不同，高水位判定在 linux 上会偏乐观。darwin（当前唯一的真实
//     部署平台）只数进程，无此偏差。差异是已知的，靠保留额的余量吸收。
func setNprocLimit(n int) error {
	if n <= 0 {
		return fmt.Errorf("围栏值必须为正数，得到 %d", n)
	}
	rl := unix.Rlimit{Cur: uint64(n), Max: uint64(n)}
	if err := unix.Setrlimit(unix.RLIMIT_NPROC, &rl); err != nil {
		return fmt.Errorf("setrlimit RLIMIT_NPROC=%d: %w", n, err)
	}
	return nil
}

// getNprocLimit 读当前进程的 RLIMIT_NPROC 软限。
//
// 返回：软限值；getrlimit 失败时返回错误。
//
// 注意：无限大（RLIM_INFINITY）会被钳到 math.MaxInt32——直接 int() 转换会
// 得到 -1，那个负数一路流下去会把「无上限」变成「上限为负」，比钳值危险得多。
func getNprocLimit() (int, error) {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &rl); err != nil {
		return 0, fmt.Errorf("getrlimit RLIMIT_NPROC: %w", err)
	}
	if rl.Cur > uint64(math.MaxInt32) {
		return math.MaxInt32, nil
	}
	return int(rl.Cur), nil
}
```

创建 `internal/prochost/fence_other.go`：

```go
//go:build !darwin && !linux

// fence_other.go —— 非 darwin/linux 平台的围栏占位实现。
//
// 职责：让本包在所有平台可编译，并把「这个平台没有围栏」如实告诉调用方
//
// 边界：不做任何降级模拟——没有围栏就是没有，装作装上了会让调用方
// 以为有保护而放心扇出，比明确没有更危险
package prochost

// setNprocLimit 在本平台无实现。
func setNprocLimit(int) error { return errFenceNotSupported }

// getNprocLimit 在本平台无实现。
func getNprocLimit() (int, error) { return 0, errFenceNotSupported }
```

`errFenceNotSupported` 定义在 Task 2 的 `fence.go`（平台无关）。本 task 为了让测试能跑，先在 `fence_unix.go` **同目录**新建一个最小的 `fence.go` 只放这一个 var：

```go
// fence.go —— 进程围栏的策略层（本 task 只落错误哨兵，策略在 Task 2 补齐）。
package prochost

import "errors"

// errFenceNotSupported 表示本平台没有进程围栏实现。
//
// 为什么要与 errNotSupported（进程枚举）分开：两者可以独立缺失，混用会让
// 「数得出但围不住」这种真实存在的状态没法表达。
var errFenceNotSupported = errors.New("本平台不支持进程围栏")
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/prochost/ -run 'TestGetNprocLimit|TestSetNprocLimitRejects' -v`
Expected: PASS（2 个用例）

Run: `GOOS=windows go build ./...`
Expected: 无输出（`fence_other.go` 的 build tag 生效）

- [ ] **Step 5: 加关键节点日志**

本 task 是纯原语，**刻意不打日志**：`setNprocLimit` 在 shim 里每进程只调一次，调用方（Task 3）在边界上记「围栏已安装 / 安装失败」比在这里记更有上下文；`getNprocLimit` 是自检读操作，高频调用打日志只会刷屏。在 `setNprocLimit` 的文档注释末尾补一句说明这个决定：

```go
//   - 本函数刻意不打日志：调用方（shim）在安装边界上统一记录围栏值与结果，
//     这里再记一遍等于同一件事写两次。
```

- [ ] **Step 6: 加注释自检**

确认：两个新文件都有文件头（职责 + 边界）；`setNprocLimit` / `getNprocLimit` 都有参数/返回/注意事项；「为什么软硬限必须同设」「setsid 不重置」「linux 线程口径差异」「RLIM_INFINITY 钳值」四条 why 都在。

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/fence_unix.go internal/prochost/fence_other.go internal/prochost/fence.go internal/prochost/fence_test.go
git commit -m "feat(prochost): 进程围栏平台原语——setrlimit 软硬限同设的单向门"
```

---

## Task 2: 围栏策略层（L 的算法与余量判读）

**Files:**
- Modify: `internal/prochost/fence.go`（Task 1 只放了错误哨兵，本 task 补齐策略）
- Test: `internal/prochost/fence_test.go`（追加）

**Interfaces:**
- Consumes: `procLimit()`（Task 0 依赖前提，B69 已有）、`enumProcsFn`（B69 已有的测试缝）
- Produces:
  - `func SetFencePolicy(disabled bool, reserveRatio float64)` — Task 8 在 agentd bootstrap 调用
  - `func fenceLimit() (int, error)` — Task 3 使用
  - `type Admission struct { Used, Limit int; Known bool }` 及方法 `Full() bool` / `NearFull() bool`
  - `func CheckAdmission() Admission` — Task 6/7 使用
  - `var procLimitFn = procLimit` — 测试缝

- [ ] **Step 1: 写失败的测试**

追加到 `internal/prochost/fence_test.go`：

```go
// withFakeProcs 把两个读数缝换成固定值，恢复交给 t.Cleanup。
// 与 B69 的 enumProcsFn 同款路数：判据测试必须喂固定快照，不能依赖真机。
func withFakeProcs(t *testing.T, used int, limit int, limitErr error) {
	t.Helper()
	oldEnum, oldLimit := enumProcsFn, procLimitFn
	procs := make([]procEntry, used)
	for i := range procs {
		procs[i] = procEntry{PID: i + 1, PGID: i + 1, StartedAt: 1}
	}
	enumProcsFn = func() ([]procEntry, error) { return procs, nil }
	procLimitFn = func() (int, error) { return limit, limitErr }
	t.Cleanup(func() { enumProcsFn, procLimitFn = oldEnum, oldLimit })
}

// withPolicy 临时改策略，恢复交给 t.Cleanup。
func withPolicy(t *testing.T, disabled bool, ratio float64) {
	t.Helper()
	oldD, oldR := fenceDisabled, fenceReserveRatio
	fenceDisabled, fenceReserveRatio = disabled, ratio
	t.Cleanup(func() { fenceDisabled, fenceReserveRatio = oldD, oldR })
}

// 正常机器：2666 的上限、10% 保留额 → 围栏 2400，救护车道 266。
func TestFenceLimitLeavesAmbulanceLane(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil)
	withPolicy(t, false, 0.1)
	got, err := fenceLimit()
	if err != nil {
		t.Fatalf("算围栏失败: %v", err)
	}
	if got != 2400 {
		t.Fatalf("围栏应为 2400，得到 %d", got)
	}
}

// 小机器：比例算出来的保留额低于下限时，下限接管——救护车道再窄
// 也要塞得下 agentd + sshd + 登录 shell。
func TestFenceLimitReserveFloorTakesOver(t *testing.T) {
	withFakeProcs(t, 0, 1000, nil)
	withPolicy(t, false, 0.1) // 10% = 100 < 200
	got, err := fenceLimit()
	if err != nil {
		t.Fatalf("算围栏失败: %v", err)
	}
	if got != 800 { // 1000 - 200
		t.Fatalf("围栏应为 800（下限 200 接管），得到 %d", got)
	}
}

// 上限小到留不出保留额：不设围栏，且**不是错误**——这台机器本来就没有
// 划分的余地，硬划会让 executor 一个进程都起不来。
func TestFenceLimitTooSmallDisablesFence(t *testing.T) {
	withFakeProcs(t, 0, 150, nil)
	withPolicy(t, false, 0.1)
	got, err := fenceLimit()
	if err != nil {
		t.Fatalf("上限过小不应报错，得到 %v", err)
	}
	if got != 0 {
		t.Fatalf("应返回 0（不设围栏），得到 %d", got)
	}
}

// 策略关闭时直接返回 0，不去读系统上限。
func TestFenceLimitDisabled(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil)
	withPolicy(t, true, 0.1)
	got, err := fenceLimit()
	if err != nil || got != 0 {
		t.Fatalf("策略关闭应返回 (0, nil)，得到 (%d, %v)", got, err)
	}
}

// 读数不可信时 Known=false，且 Full/NearFull 恒为 false——调用方据此
// fail-open。为「量不出来」而拒绝派发，代价远大于收益。
func TestCheckAdmissionUnknownFailsOpen(t *testing.T) {
	withFakeProcs(t, 100, 0, errNotSupported)
	withPolicy(t, false, 0.1)
	a := CheckAdmission()
	if a.Known {
		t.Fatalf("读不到上限时 Known 应为 false，得到 %+v", a)
	}
	if a.Full() || a.NearFull() {
		t.Fatalf("未知状态下 Full/NearFull 必须恒 false，得到 %+v", a)
	}
}

// 水位判定以**围栏值**为参考上限，不是系统上限：2400 的九成是 2160。
func TestCheckAdmissionWatermarkUsesFenceLimit(t *testing.T) {
	withPolicy(t, false, 0.1)
	withFakeProcs(t, 2159, 2666, nil)
	if a := CheckAdmission(); a.NearFull() {
		t.Fatalf("2159/2400 未到九成，不该判高水位: %+v", a)
	}
	withFakeProcs(t, 2160, 2666, nil)
	a := CheckAdmission()
	if !a.Known || a.Limit != 2400 {
		t.Fatalf("参考上限应为围栏值 2400，得到 %+v", a)
	}
	if !a.NearFull() {
		t.Fatalf("2160/2400 已达九成，应判高水位: %+v", a)
	}
	if a.Full() {
		t.Fatalf("2160/2400 还没满，不该判 Full: %+v", a)
	}
}

// EAGAIN + 高水位 = 确定归因；文案必须带真实数字（审核者要靠它一眼定性）。
func TestExplainForkFailureQuotaExhausted(t *testing.T) {
	withPolicy(t, false, 0.1)
	withFakeProcs(t, 2390, 2666, nil)
	note, quota := ExplainForkFailure(fmt.Errorf("fork/exec /bin/sh: %w", syscall.EAGAIN))
	if !quota {
		t.Fatalf("高水位下的 EAGAIN 应判为配额耗尽，得到 quota=false note=%q", note)
	}
	if !strings.Contains(note, "2390") || !strings.Contains(note, "2400") {
		t.Fatalf("归因文案必须带 used/limit 真实数字，得到 %q", note)
	}
}

// EAGAIN 但占用不高：**如实说不知道**。会说谎的诊断比没有诊断更糟——
// 这正是本次事故里「报错长得像 flaky 测试」把排障带偏 43 分钟的反面。
func TestExplainForkFailureLowUsageStaysHonest(t *testing.T) {
	withPolicy(t, false, 0.1)
	withFakeProcs(t, 800, 2666, nil)
	note, quota := ExplainForkFailure(fmt.Errorf("fork/exec /bin/sh: %w", syscall.EAGAIN))
	if quota {
		t.Fatalf("低占用不该判配额耗尽: %q", note)
	}
	if !strings.Contains(note, "未知") {
		t.Fatalf("低占用应如实说原因未知，得到 %q", note)
	}
}

// 非 EAGAIN 的错误一律不认领：返回空串，调用方据此不改写原错误。
func TestExplainForkFailureIgnoresUnrelated(t *testing.T) {
	note, quota := ExplainForkFailure(errors.New("permission denied"))
	if note != "" || quota {
		t.Fatalf("无关错误不该被认领，得到 (%q, %v)", note, quota)
	}
	if note, _ := ExplainForkFailure(nil); note != "" {
		t.Fatalf("nil 错误不该被认领，得到 %q", note)
	}
}
```

（测试文件需追加 import：`fmt`、`strings`、`syscall`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestFenceLimit|TestCheckAdmission|TestExplainForkFailure' -v`
Expected: FAIL，`undefined: fenceLimit` / `undefined: CheckAdmission` / `undefined: ExplainForkFailure` / `undefined: procLimitFn`

- [ ] **Step 3: 写实现**

把 `internal/prochost/fence.go` 补齐为：

```go
// fence.go —— 进程围栏的策略层：算围栏值、判读余量、翻译 EAGAIN。
//
// 职责：
//   - fenceLimit：由系统上限与保留额算出围栏值 L
//   - CheckAdmission：报告当前余量，供准入闸与高水位告警判读
//   - ExplainForkFailure：把 EAGAIN 翻译成「配额耗尽」还是「原因未知」
//
// 边界：
//   - 不安装围栏（那是 setNprocLimit + shim），不决定拒不拒发（那是 agentd 的策略）
//   - 不修改任何任务状态、不发事件
//   - **一律不 fork**：本文件所有读数走 sysctl / /proc。这套代码要在机器已经
//     fork 不动的时候仍然可用——2026-08-12 devbox 瘫痪时连 `ps | wc -l` 都起
//     不来，当时所有基于 exec 的诊断手段同时失效，正是这条约束的由来
package prochost

import (
	"errors"
	"fmt"
	"syscall"
)

// errFenceNotSupported 表示本平台没有进程围栏实现。
//
// 为什么要与 errNotSupported（进程枚举）分开：两者可以独立缺失，混用会让
// 「数得出但围不住」这种真实存在的状态没法表达。
var errFenceNotSupported = errors.New("本平台不支持进程围栏")

// 围栏策略。包级 var 而非常量：agentd 启动时由 config 经 SetFencePolicy 注入一次。
var (
	fenceDisabled     bool
	fenceReserveRatio = 0.1
)

// fenceReserveFloor 是保留额的下限。
//
// 为什么要有下限：保留额的用途是「救护车道」——保证 agentd、sshd、登录 shell、
// 一次 ps 永远起得来。比例在小机器上会算出个位数，那样的车道等于没有。
const fenceReserveFloor = 200

// fenceWatermarkRatio 是「贴着上限」的判定线：达到参考上限的九成即为高水位。
//
// 为什么是九成而不是满：满了才告警等于没告警——那时已经在撞墙了。九成留出
// 的余量足够审核者收到事件、看一眼、决定要不要收敛。
const fenceWatermarkRatio = 0.9

// procLimitFn 是读系统上限的测试缝（与 enumProcsFn 同款路数）。
// **生产路径恒为 procLimit**，非测试代码不得赋值。
var procLimitFn = procLimit

// SetFencePolicy 注入围栏策略，由 agentd 启动时按 config 调用一次。
//
// 参数：
//   - disabled: true 时完全不装围栏（逃生开关）
//   - reserveRatio: 保留额占系统上限的比例；不在 (0,1) 区间时保留默认值 0.1
//
// 注意：本函数只改包级策略，不会影响已经拉起的 shim——它们的围栏在 fork 那
// 一刻就定死了，改策略只对之后启动的任务生效。
func SetFencePolicy(disabled bool, reserveRatio float64) {
	fenceDisabled = disabled
	if reserveRatio > 0 && reserveRatio < 1 {
		fenceReserveRatio = reserveRatio
	}
	log().Info("进程围栏策略已设定", "disabled", fenceDisabled,
		"reserve_ratio", fenceReserveRatio)
}

// fenceLimit 算出应安装的围栏值 L。
//
// 返回：
//   - L > 0: 应安装的围栏值
//   - L == 0 且 err == nil: 策略关闭，或系统上限小到留不出保留额——两种都是
//     「本次不设围栏」的正常结论，**不是错误**
//   - err != nil: 读不到系统上限
//
// 取法是「贴天花板留救护车道」，不是「给 executor 节流」：保留额只要够
// agentd/sshd/登录 shell 活着即可。压得更低不增加安全性，只会让 executor 更
// 早撞墙、让审核者更容易把配额问题误判成代码问题——一个会误导的防护比没有
// 防护更糟。
func fenceLimit() (int, error) {
	if fenceDisabled {
		return 0, nil
	}
	limit, err := procLimitFn()
	if err != nil {
		log().Warn("读不到系统进程上限，本次不设围栏", "cause", err)
		return 0, err
	}
	reserve := int(float64(limit) * fenceReserveRatio)
	if reserve < fenceReserveFloor {
		reserve = fenceReserveFloor
	}
	if reserve >= limit {
		log().Warn("系统进程上限过小，留不出保留额，本次不设围栏",
			"limit", limit, "reserve", reserve)
		return 0, nil
	}
	return limit - reserve, nil
}

// fenceReference 返回余量判读的参考上限：围栏已启用时为 L，否则退回系统上限。
//
// 为什么参考上限不能恒用系统上限：装了围栏之后，executor 的实际天花板是 L，
// 拿 2666 去算水位会让「已经贴着围栏」显示成「才用了九成的九成」，高水位
// 告警永远不触发。
func fenceReference() (int, error) {
	l, err := fenceLimit()
	if err != nil {
		return 0, err
	}
	if l > 0 {
		return l, nil
	}
	return procLimitFn()
}

// Admission 是一次余量判读的结果。
//
// 字段说明：
//   - Used: 当前 uid 的进程数
//   - Limit: 参考上限（围栏值或系统上限）
//   - Known: 读数是否可信；false 时 Used/Limit 无意义，不得据此做任何判断
type Admission struct {
	Used  int  `json:"used"`
	Limit int  `json:"limit"`
	Known bool `json:"known"`
}

// Full 报告余量是否已经耗尽。读数不可信时恒为 false（fail-open）。
func (a Admission) Full() bool { return a.Known && a.Used >= a.Limit }

// NearFull 报告是否已达高水位（参考上限的九成）。读数不可信时恒为 false。
func (a Admission) NearFull() bool {
	return a.Known && float64(a.Used) >= float64(a.Limit)*fenceWatermarkRatio
}

// CheckAdmission 零 fork 读一次当前余量。
//
// 返回：Admission；任何一步读不到数都返回零值（Known=false）。
//
// 注意：读不到数时**不报错也不猜 0**，而是让调用方 fail-open 照常放行。
// 为「量不出来」而拒绝派发，会让 handoff 在不支持的平台上彻底不能用，
// 代价远大于收益——防护装置故障不该变成拒绝服务。
func CheckAdmission() Admission {
	procs, err := enumProcsFn()
	if err != nil {
		log().Debug("余量判读失败（枚举进程），按未知处理", "cause", err)
		return Admission{}
	}
	ref, err := fenceReference()
	if err != nil || ref <= 0 {
		log().Debug("余量判读失败（参考上限不可用），按未知处理", "cause", err, "ref", ref)
		return Admission{}
	}
	return Admission{Used: len(procs), Limit: ref, Known: true}
}

// ExplainForkFailure 判读一个进程创建失败是否为配额耗尽，并给出可读归因。
//
// 参数：err 为任意一次 fork/exec 返回的错误（nil 安全）
//
// 返回：
//   - note: 面向人的归因文案；空串表示这个错误与配额无关，调用方不必改写它
//   - quota: 是否**确定**为配额耗尽
//
// 三条分支，对应 2026-08-12 事故的教训——当时的
// `fork/exec /bin/sh: resource temporarily unavailable` 埋在测试输出里，长得
// 像 flaky 测试，把排障方向带偏了整整 43 分钟：
//   - 非 EAGAIN：不认领，返回空串
//   - EAGAIN 且占用贴着参考上限：确定归因，quota=true，文案带真实数字
//   - EAGAIN 但占用不高、或读不出占用：**如实说不知道**，quota=false。
//     宁可说「原因未知」也不能猜一个像模像样的结论
func ExplainForkFailure(err error) (note string, quota bool) {
	if err == nil || !errors.Is(err, syscall.EAGAIN) {
		return "", false
	}
	a := CheckAdmission()
	if !a.Known {
		log().Warn("进程创建失败（EAGAIN），但读不到当前占用，无法归因")
		return "进程创建失败（EAGAIN），且读不到当前进程占用，原因未知", false
	}
	if a.NearFull() {
		log().Error("进程配额耗尽", "used", a.Used, "limit", a.Limit)
		return fmt.Sprintf("进程配额耗尽（当前 uid %d/%d），命令未执行；"+
			"这不是代码问题，请降低并发后重试", a.Used, a.Limit), true
	}
	log().Warn("进程创建失败（EAGAIN），但占用不高，原因未知",
		"used", a.Used, "limit", a.Limit)
	return fmt.Sprintf("进程创建失败（EAGAIN），但当前占用仅 %d/%d，"+
		"不像配额问题，原因未知", a.Used, a.Limit), false
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/prochost/ -run 'TestFenceLimit|TestCheckAdmission|TestExplainForkFailure' -v`
Expected: PASS（9 个用例）

Run: `go test ./internal/prochost/ -count=1`
Expected: 全绿（B69 的既有用例不受影响）

- [ ] **Step 5: 加关键节点日志**

实现里已就位，逐条对照确认：
- `SetFencePolicy` 注入时 Info（disabled + ratio）——策略是排障时第一个要问的东西
- `fenceLimit` 两条降级分支各一条 Warn（读不到上限 / 上限过小），带上具体数字
- `CheckAdmission` 两条未知分支各一条 Debug（高频调用，不能用 Info 刷屏）
- `ExplainForkFailure` 三条分支各一条：配额耗尽 Error、占用不高 Warn、读不出占用 Warn

- [ ] **Step 6: 加注释自检**

确认：文件头有职责 + 边界（含「一律不 fork」及其 2026-08-12 由来）；`SetFencePolicy` / `fenceLimit` / `fenceReference` / `CheckAdmission` / `ExplainForkFailure` / `Admission` 及两个方法全部有文档注释；四条 why 在位——为什么保留额要有下限、为什么水位线是九成、为什么参考上限不能恒用系统上限、为什么读不出数要 fail-open。

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/fence.go internal/prochost/fence_test.go
git commit -m "feat(prochost): 围栏策略层——L 的算法、余量判读、EAGAIN 归因"
```

---

## Task 3: 围栏下发与安装

**Files:**
- Modify: `internal/prochost/prochost.go`（`Spec` 加字段；`Start` 调 `applyFencePolicy`）
- Modify: `internal/prochost/fence.go`（新增 `applyFencePolicy`）
- Modify: `internal/prochost/shim.go`（`RunShim` 安装围栏）
- Test: `internal/prochost/fence_inherit_test.go`（新建，真进程继承性）
- Test: `internal/prochost/fence_test.go`（追加两条填充用例）

**Interfaces:**
- Consumes: `setNprocLimit`（Task 1）、`fenceLimit`（Task 2）
- Produces: `Spec.NprocLimit int`（`json:"nproc_limit,omitempty"`）— 无下游 task 依赖，但 Plan B 的 roster 会复用同一条 Spec 通道

- [ ] **Step 1: 写失败的测试**

创建 `internal/prochost/fence_inherit_test.go`：

```go
//go:build darwin || linux

package prochost

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const fenceHelperEnv = "HANDOFF_FENCE_HELPER"

// TestHelperFenceParent 扮演 shim：装上围栏，再以 setsid 拉起一个孙进程，
// 把孙进程看到的软限打到 stdout。生产里这两步分别由 RunShim 与 executor 完成。
func TestHelperFenceParent(t *testing.T) {
	if os.Getenv(fenceHelperEnv) != "parent" {
		t.Skip("非 helper 调用")
	}
	want, _ := strconv.Atoi(os.Getenv("HANDOFF_FENCE_VALUE"))
	if err := setNprocLimit(want); err != nil {
		os.Stdout.WriteString("SETFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperFenceChild")
	cmd.Env = append(os.Environ(), fenceHelperEnv+"=child")
	// Setsid=true 让子进程 pid==sid==pgid，完全脱离本进程的会话与进程组——
	// 精确复刻 opencode Bash 工具对每条命令做的那件事
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		os.Stdout.WriteString("CHILDFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	os.Stdout.Write(out)
	os.Exit(0)
}

// TestHelperFenceChild 是被 setsid 拉起的孙进程：报告自己的会话身份与软限。
func TestHelperFenceChild(t *testing.T) {
	if os.Getenv(fenceHelperEnv) != "child" {
		t.Skip("非 helper 调用")
	}
	lim, err := getNprocLimit()
	if err != nil {
		os.Stdout.WriteString("GETFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	sid, _ := syscall.Getsid(0)
	os.Stdout.WriteString("PID=" + strconv.Itoa(os.Getpid()) +
		" SID=" + strconv.Itoa(sid) +
		" LIMIT=" + strconv.Itoa(lim) + "\n")
	os.Exit(0)
}

// 围栏必须穿透 setsid：这是整个方案的地基。地基塌了，按进程组收不到的那些
// 逃逸树就同样不受围栏约束，B73 等于没做。
//
// 为什么必须在子进程里压限值而不是在测试进程里：setNprocLimit 同压软硬限，
// 是不可逆的单向门——在测试进程里压一次，之后所有用例（以及 go test 自己
// 要 fork 的编译/测试二进制）都会跟着受限。
func TestFenceSurvivesSetsid(t *testing.T) {
	const want = 4096
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperFenceParent")
	cmd.Env = append(os.Environ(), fenceHelperEnv+"=parent",
		"HANDOFF_FENCE_VALUE="+strconv.Itoa(want))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper 执行失败: %v (输出 %q)", err, out)
	}
	line := strings.TrimSpace(string(out))
	if !strings.Contains(line, "LIMIT="+strconv.Itoa(want)) {
		t.Fatalf("setsid 后的孙进程应继承围栏 %d，实际输出 %q", want, line)
	}
	// 同时确认它真的逃逸了（pid==sid），否则这条用例证明不了任何事
	var pid, sid int
	for _, f := range strings.Fields(line) {
		k, v, _ := strings.Cut(f, "=")
		n, _ := strconv.Atoi(v)
		switch k {
		case "PID":
			pid = n
		case "SID":
			sid = n
		}
	}
	if pid == 0 || pid != sid {
		t.Fatalf("孙进程未真正脱离会话（pid=%d sid=%d），本用例不成立: %q", pid, sid, line)
	}
}

```

下面两条是纯逻辑用例，**放 `internal/prochost/fence_test.go`**（无 build tag，
所有平台都跑）——它们只经缝喂假数据，不碰真进程：

```go
// Start 必须自己把围栏值填进 Spec：四个 adapter 各自构造 Spec，交给它们填
// 等于四处都可能漏，而漏掉的后果是这个任务完全没有保护、且没人看得出来。
func TestApplyFencePolicyFillsSpec(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil) // 见 fence_test.go
	withPolicy(t, false, 0.1)
	var spec Spec
	applyFencePolicy(&spec)
	if spec.NprocLimit != 2400 {
		t.Fatalf("Spec 应被填入围栏值 2400，得到 %d", spec.NprocLimit)
	}
}

// 策略关闭时字段保持 0——0 是「不设围栏」的约定值，shim 据此跳过安装。
func TestApplyFencePolicyDisabledLeavesZero(t *testing.T) {
	withFakeProcs(t, 0, 2666, nil)
	withPolicy(t, true, 0.1)
	spec := Spec{NprocLimit: 999} // 故意预置脏值，确认会被覆盖成 0
	applyFencePolicy(&spec)
	if spec.NprocLimit != 0 {
		t.Fatalf("策略关闭时围栏值应为 0，得到 %d", spec.NprocLimit)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestApplyFencePolicy|TestFenceSurvivesSetsid' -v`
Expected: FAIL，`undefined: applyFencePolicy` / `unknown field NprocLimit in struct literal Spec`

> `TestFenceSurvivesSetsid` 只依赖 Task 1 的原语，它在本 task 之前就该是绿的；
> 之所以放在这里，是因为它验的是**整个方案的地基**——地基塌了，按进程组收不到
> 的那些逃逸树同样不受围栏约束，B73 等于没做。它和实现代码放在一起才不会被
> 后续改动悄悄绕过。如果编译修好后它单独 FAIL，**停下来查 Task 1，不要往下走**。

- [ ] **Step 3: 写实现**

`internal/prochost/prochost.go` —— `Spec` 结构体末尾加字段（在 `Sentinel` 之后）：

```go
	// NprocLimit 是这棵执行者进程树的围栏值（RLIMIT_NPROC 软硬限）。
	//
	// 0 = 不设围栏。为什么用零值而不是指针：这个字段没有「对端没发」与
	// 「对端发了 0」的区分需求——两者都表示不装，语义完全一致。
	//
	// omitempty + 零值语义同时保证了滚动升级安全：新版 agentd 写出的
	// spec.json 被旧版 shim 读到时该字段被忽略（旧 shim 不认识），新版 shim
	// 读到升级前的 spec.json 得到 0 则跳过安装——两个方向都不会出事。
	NprocLimit int `json:"nproc_limit,omitempty"`
```

并在 `Spec` 的字段说明注释块里补一行：

```go
//   - NprocLimit: 执行者树的进程数围栏（0 = 不设）；由 Start 按策略算出，调用方不填
```

`internal/prochost/fence.go` —— 新增填充函数：

```go
// applyFencePolicy 按当前策略把围栏值写进 spec。
//
// 参数：spec 为待下发的进程规格，本函数**就地修改**其 NprocLimit 字段
//
// 为什么由 prochost 自己填而不是让 adapter 填：四个 adapter 各自构造 Spec，
// 交给它们填等于四处都可能漏，而漏掉的后果是这个任务完全没有围栏保护、
// 且日志里看不出任何异常——一个静默失效的防护装置。
//
// 注意：算不出围栏值时字段置 0（不设围栏）并打 Warn，**绝不阻断拉起**。
func applyFencePolicy(spec *Spec) {
	l, err := fenceLimit()
	if err != nil {
		log().Warn("算不出进程围栏值，本次不设围栏", "cause", err)
		spec.NprocLimit = 0
		return
	}
	spec.NprocLimit = l
}
```

`internal/prochost/prochost.go` —— `Start` 在 `json.Marshal(spec)` **之前**插入：

```go
	applyFencePolicy(&spec)
```

`internal/prochost/shim.go` —— 在 `cmd := exec.Command(...)` **之前**（打开 stdout/stderr 之后）插入：

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

并在 `shim.go` 文件头的「职责」列表里加一行：

```go
//   - 在 spawn executor 之前安装进程围栏（RLIMIT_NPROC），executor 全树继承
```

以及在「边界」列表里加一行：

```go
//   - 不决定围栏值取多少：那是 prochost 策略层（fence.go）的事，shim 只负责装
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/prochost/ -run 'TestFenceSurvivesSetsid|TestApplyFencePolicy' -v`
Expected: PASS（3 个用例）。`TestFenceSurvivesSetsid` 的输出里孙进程 `LIMIT=4096` 且 `PID==SID`。

Run: `go test ./internal/prochost/ -count=1` 与 `go test -race ./internal/prochost/`
Expected: 全绿

- [ ] **Step 5: 加关键节点日志**

已就位，确认三条：`applyFencePolicy` 算不出 L 时 Warn；`RunShim` 安装成功 Info（带围栏值）、失败 Warn（带值与 cause）、未下发 Info。**围栏值必须出现在日志里**——事故复盘时第一个要问的就是「当时围栏是多少」。

- [ ] **Step 6: 加注释自检**

确认：`Spec.NprocLimit` 有字段注释（含零值语义与双向升级安全的 why）；`applyFencePolicy` 有参数/why/注意；`RunShim` 的插入段有「为什么必须在 spawn 之前」的 why；`shim.go` 文件头的职责与边界都已更新。

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/prochost.go internal/prochost/fence.go internal/prochost/shim.go internal/prochost/fence_inherit_test.go internal/prochost/fence_test.go
git commit -m "feat(prochost): 围栏随 Spec 下发，shim 在 spawn 前安装（实测穿透 setsid）"
```

---

## Task 4: 四个进程创建点接入归因

**Files:**
- Modify: `internal/prochost/platform_unix.go`（`spawnDetached`）
- Modify: `internal/prochost/shim.go`（`cmd.Start()` 失败路径）
- Modify: `internal/agentd/workspace.go`（`gitRun`、`RunCmd`）
- Test: `internal/agentd/workspace_fence_test.go`（新建）

**Interfaces:**
- Consumes: `prochost.ExplainForkFailure`（Task 2）
- Produces: 无新导出；改的是四处错误文案

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/workspace_fence_test.go`：

```go
package agentd

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// errFakeEAGAIN 模拟一次真实的 fork 失败：错误链里挂着 syscall.EAGAIN，
// 外层文案与 exec 包实际产出的一致。
var errFakeEAGAIN = fmt.Errorf("fork/exec /bin/sh: %w", syscall.EAGAIN)

// 归因只改文案、不改错误语义：非配额类失败必须原样上抛，调用方的
// errors.Is / 退出码判断一个都不能被影响。
func TestRunCmdNonQuotaErrorUnchanged(t *testing.T) {
	dir := t.TempDir()
	// 一条正常失败的命令：退出码非零，但不是 fork 失败
	out, code, err := RunCmd(context.Background(), dir, "exit 3")
	if err != nil {
		t.Fatalf("命令非零退出不应返回错误，得到 %v", err)
	}
	if code != 3 {
		t.Fatalf("退出码应为 3，得到 %d（输出 %q）", code, out)
	}
}

// 归因文案必须能出现在返回的 error 里——审核者看到的是这个字符串，
// 只写日志等于没归因（日志在执行机上，审核者手边没有）。
func TestForkFailureNoteReachesCaller(t *testing.T) {
	note := quotaNote(errFakeEAGAIN)
	if note == "" {
		t.Fatalf("EAGAIN 应产出归因文案")
	}
	if !strings.Contains(note, "配额") && !strings.Contains(note, "未知") {
		t.Fatalf("归因文案应给出结论或明确说未知，得到 %q", note)
	}
}
```

> 说明：`quotaNote` 见 Step 3——把归因收成 agentd 包内一个小助手，
> 避免四处重复同一段 if。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestRunCmdNonQuota|TestForkFailureNote' -v`
Expected: FAIL，`undefined: quotaNote` / `undefined: errFakeEAGAIN`

- [ ] **Step 3: 写实现**

`internal/agentd/workspace.go` —— 在 `gitRun` 之前加助手：

```go
// quotaNote 把一次进程创建失败翻译成归因文案；与配额无关时返回空串。
//
// 为什么在 agentd 侧包一层而不是四处直接调 prochost.ExplainForkFailure：
// 归因文案要同时进日志和进返回给审核者的 error，收在一处才不会两边写法漂移。
func quotaNote(err error) string {
	note, _ := prochost.ExplainForkFailure(err)
	return note
}
```

`gitRun` 的失败分支改为：

```go
	if err != nil {
		if note := quotaNote(err); note != "" {
			log().Error("git 调用失败（进程配额）", "repo", repo, "args", args,
				"note", note, "cause", err)
			return outBuf.String(), errBuf.String(), fmt.Errorf("%s: %w", note, err)
		}
		log().Error("git 调用失败", "repo", repo, "args", args,
			"stderr", truncateRunes(errBuf.String(), 500), "cause", err)
	}
```

`RunCmd` 的 `cmd.Start()` 失败分支改为：

```go
	if err := cmd.Start(); err != nil {
		if note := quotaNote(err); note != "" {
			log().Error("run 命令启动失败（进程配额）", "repo", repo,
				"cmd", truncateRunes(cmdline, 200), "note", note, "cause", err)
			return "", -1, fmt.Errorf("%s: %w", note, err)
		}
		log().Error("run 命令启动失败", "repo", repo, "cmd", truncateRunes(cmdline, 200),
			"cause", err)
		return "", -1, err
	}
```

`internal/prochost/platform_unix.go` —— `spawnDetached` 的失败分支补归因（包内直接调）：

```go
	if err != nil {
		if note, _ := ExplainForkFailure(err); note != "" {
			log().Error("拉起 shim 失败（进程配额）", "note", note, "cause", err)
			return 0, fmt.Errorf("%s: %w", note, err)
		}
		// ……保持原有错误返回
	}
```

`internal/prochost/shim.go` —— `cmd.Start()` 失败分支：

```go
	if err := cmd.Start(); err != nil {
		if note, _ := ExplainForkFailure(err); note != "" {
			l.Error("拉起执行者进程失败（进程配额）", "bin", spec.Argv[0],
				"note", note, "fence", spec.NprocLimit, "cause", err)
			return fmt.Errorf("%s: 拉起 %s: %w", note, spec.Argv[0], err)
		}
		l.Error("拉起执行者进程失败", "bin", spec.Argv[0], "cause", err)
		return fmt.Errorf("拉起 %s: %w", spec.Argv[0], err)
	}
```

> **注意 shim 这一处的特殊性**：这里的 EAGAIN 很可能是**围栏自己造成的**
> （uid 占用已经 ≥ L，shim 连 executor 都 fork 不出来）。日志里必须带
> `fence` 字段，否则排障的人会以为是系统上限满了，去查错的方向。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestRunCmdNonQuota|TestForkFailureNote' -v`
Expected: PASS（2 个用例）

Run: `go test ./internal/agentd/ ./internal/prochost/ -count=1`
Expected: 全绿

- [ ] **Step 5: 加关键节点日志**

已就位。逐条确认：四个点各有一条**独立的**「（进程配额）」Error 日志分支，与原有的普通失败日志分开——排障时可以直接 grep「进程配额」把这类失败一网打尽。shim 那条额外带 `fence` 字段。

- [ ] **Step 6: 加注释自检**

确认：`quotaNote` 有文档注释（含「为什么收成一处」）；shim 那处有「这里的 EAGAIN 很可能是围栏自己造成的」的 why 注释；四处改动都保持 `%w` 包装原错误（归因只加前缀，不吞错误链）。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_fence_test.go internal/prochost/platform_unix.go internal/prochost/shim.go
git commit -m "feat(agentd,prochost): 四个进程创建点接入 EAGAIN 归因，文案随 error 上抛"
```

---

## Task 5: 准入闸（Dispatch 与 run）

**Files:**
- Modify: `internal/agentd/manager.go`（`Dispatch` 开头）
- Modify: `internal/agentd/workspace.go`（`RunCmd` 开头）
- Test: `internal/agentd/workspace_fence_test.go`（追加）

**Interfaces:**
- Consumes: `prochost.CheckAdmission()` / `Admission.Full()` / `Admission.NearFull()`（Task 2）
- Produces: `var ErrNoProcHeadroom = errors.New(...)` —— agentd 包内哨兵，路由层据它返回 400

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/workspace_fence_test.go`：

```go
// 满额时拒发，且错误里必须带数字——「余量不足」四个字对排障毫无价值，
// 「2450/2400」才有。
func TestAdmissionRejectsWhenFull(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{Used: 2450, Limit: 2400, Known: true})
	defer restore()
	err := checkProcHeadroom("dispatch")
	if err == nil {
		t.Fatalf("满额应拒发")
	}
	if !errors.Is(err, ErrNoProcHeadroom) {
		t.Fatalf("应为 ErrNoProcHeadroom，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "2450") || !strings.Contains(err.Error(), "2400") {
		t.Fatalf("拒发文案必须带 used/limit，得到 %q", err.Error())
	}
}

// 高水位但没满：放行，不拦。拦在这里等于把「快满了」当成「满了」，
// 会把还能正常完成的任务无谓地挡掉。
func TestAdmissionPassesAtHighWatermark(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{Used: 2300, Limit: 2400, Known: true})
	defer restore()
	if err := checkProcHeadroom("dispatch"); err != nil {
		t.Fatalf("高水位不该拒发，得到 %v", err)
	}
}

// 读不出数：放行（fail-open）。为「量不出来」而拒绝派发，会让 handoff 在
// 不支持的平台上彻底不能用。
func TestAdmissionFailsOpenWhenUnknown(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{})
	defer restore()
	if err := checkProcHeadroom("dispatch"); err != nil {
		t.Fatalf("读数未知时必须放行，得到 %v", err)
	}
}
```

`fakeAdmission` 助手（同文件）：

```go
// fakeAdmission 替换准入判读缝，返回恢复函数。
func fakeAdmission(a prochost.Admission) func() {
	old := admissionFn
	admissionFn = func() prochost.Admission { return a }
	return func() { admissionFn = old }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestAdmission -v`
Expected: FAIL，`undefined: checkProcHeadroom` / `undefined: ErrNoProcHeadroom` / `undefined: admissionFn`

- [ ] **Step 3: 写实现**

`internal/agentd/workspace.go`（或新建 `internal/agentd/admission.go`，二选一，推荐后者以免 workspace.go 继续膨胀）——新建 `internal/agentd/admission.go`：

```go
// admission.go —— 开工前的进程余量准入闸。
//
// 职责：
//   - checkProcHeadroom：余量已耗尽时拒绝开工，并给出带数字的理由
//
// 边界：
//   - 不安装围栏、不回收进程：只做一次只读判读
//   - **不承担拦截职责**。真正拦住事故的是围栏（进程起不来），本闸换来的是
//     一句人能看懂的话，以及「A 任务吃满时 B 任务得到解释而不是莫名撞墙」。
//     2026-08-12 两个任务开工时余量都是好的，这个闸拦不住它们——别高估它
package agentd

import (
	"errors"
	"fmt"

	"github.com/xushixin/handoff/internal/prochost"
)

// ErrNoProcHeadroom 表示进程余量已耗尽，本次开工被拒。
//
// 路由层靠 errors.Is 认它并返回 400——这是环境问题不是请求格式问题，
// 但 4xx 能让审核者立刻知道「不用重试，先腾地方」。
var ErrNoProcHeadroom = errors.New("进程余量不足")

// admissionFn 是余量判读的测试缝。**生产路径恒为 prochost.CheckAdmission**。
var admissionFn = prochost.CheckAdmission

// checkProcHeadroom 在开工前判读一次进程余量。
//
// 参数：op 为动作名（"dispatch" / "run"），只用于日志与错误文案
//
// 返回：余量耗尽时返回包装 ErrNoProcHeadroom 的错误（文案带 used/limit
// 真实数字）；其余情况一律 nil。
//
// 注意：
//   - 高水位（九成）**放行**并打 Warn：拦在这里等于把「快满了」当「满了」，
//     会把还能正常完成的任务无谓挡掉
//   - 读数不可信时**放行**（fail-open）：为「量不出来」而拒绝派发，会让
//     handoff 在不支持的平台上彻底不能用，代价远大于收益
func checkProcHeadroom(op string) error {
	a := admissionFn()
	if !a.Known {
		log().Debug("进程余量未知，放行", "op", op)
		return nil
	}
	if a.Full() {
		log().Error("进程余量耗尽，拒绝开工", "op", op, "used", a.Used, "limit", a.Limit)
		return fmt.Errorf("%w：当前 %d/%d，请等待在跑的任务结束或先回收残留",
			ErrNoProcHeadroom, a.Used, a.Limit)
	}
	if a.NearFull() {
		log().Warn("进程余量已达高水位，仍放行", "op", op, "used", a.Used, "limit", a.Limit)
		return nil
	}
	log().Debug("进程余量充足", "op", op, "used", a.Used, "limit", a.Limit)
	return nil
}
```

> **与 spec §3.4 的一处有意偏离**：spec 写「`used ≥ 0.9L`：放行，但 stderr 警告
> + 事件记录」。这里只打服务端 Warn 日志，**不在准入闸里发事件**——高水位事件
> 由 Task 6 的看门狗统一按「越线沿一次」发射。若准入闸也发，一轮密集派发会
> 产生 N 条重复事件，把审核者的会话刷爆，反而淹掉真正要处置的工单。审核者
> 得到的信息量不减（同一条 `resource_pressure` 事件），噪声大减。

`Manager.Dispatch` 开头（参数校验之后、任何副作用之前）插入：

```go
	// 准入闸必须排在建任务行、建 worktree 之前：拒发要干干净净，
	// 不能留下一个建了一半的任务等人收
	if err := checkProcHeadroom("dispatch"); err != nil {
		return nil, err
	}
```

`RunCmd` 开头（`context.WithTimeout` 之前）插入：

```go
	if err := checkProcHeadroom("run"); err != nil {
		return "", -1, err
	}
```

路由层（`server.go` 的 dispatch handler 与 run handler）把 `ErrNoProcHeadroom` 映射为 400：按该文件既有的错误分诊写法追加一条 `errors.Is` 分支（与 `ErrDirtyWorktree` 等哨兵同款处理）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestAdmission -v`
Expected: PASS（3 个用例）

Run: `go test ./internal/agentd/ -count=1`
Expected: 全绿

- [ ] **Step 5: 加关键节点日志**

已就位：耗尽 Error（带 op/used/limit）、高水位 Warn、充足 Debug（每次 run 都会走，用 Info 会刷屏）、未知 Debug。

- [ ] **Step 6: 加注释自检**

确认：`admission.go` 有文件头（职责 + 边界，含「不承担拦截职责」的定位说明）；`ErrNoProcHeadroom` 有 why（为什么用 400）；`checkProcHeadroom` 有参数/返回/两条注意（高水位放行、fail-open）；`Dispatch` 插入点有「为什么必须排在副作用之前」的 why。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/admission.go internal/agentd/manager.go internal/agentd/workspace.go internal/agentd/server.go internal/agentd/workspace_fence_test.go
git commit -m "feat(agentd): 开工前进程余量准入闸，耗尽拒发并给出带数字的理由"
```

---

## Task 6: 高水位事件与失败快照

**Files:**
- Modify: `internal/proto/proto.go`（事件类型常量）
- Modify: `internal/agentd/watchdog.go`（越线沿触发）
- Modify: `internal/agentd/manager.go`（`failedPayload` 加占用快照 + 构造助手）
- Modify: `internal/agentd/reconcile.go`（改用构造助手）
- Test: `internal/agentd/watchdog_fence_test.go`（新建）

**Interfaces:**
- Consumes: `prochost.CheckAdmission()`（Task 2）、`admissionFn` 缝（Task 5）
- Produces: `proto.EventTypeResourcePressure`、`resourcePressurePayload{Used, Limit int}`、
  `func newFailedPayload(reason string) failedPayload`

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/watchdog_fence_test.go`：

```go
package agentd

import (
	"testing"

	"github.com/xushixin/handoff/internal/prochost"
	"github.com/xushixin/handoff/internal/proto"
)

// 越线时对每个活跃任务发一次，且只发一次——事件风暴会把审核者的
// 会话刷爆，反而淹掉真正要处置的工单。
func TestResourcePressureFiresOnceOnRisingEdge(t *testing.T) {
	st, hub := newTestStoreHub(t) // 复用 watchdog_test.go 既有骨架
	t1 := mustCreateTask(t, st, proto.TaskStateRunning)
	t2 := mustCreateTask(t, st, proto.TaskStateWaitingAnswer)

	restore := fakeAdmission(prochost.Admission{Used: 2200, Limit: 2400, Known: true})
	defer restore()

	var active bool
	active = scanPressure(st, hub, active, testLogger(t))
	if !active {
		t.Fatalf("越线后应置位")
	}
	assertEventCount(t, st, t1.ID, proto.EventTypeResourcePressure, 1)
	assertEventCount(t, st, t2.ID, proto.EventTypeResourcePressure, 1)

	// 第二轮仍在高水位：不重发
	active = scanPressure(st, hub, active, testLogger(t))
	assertEventCount(t, st, t1.ID, proto.EventTypeResourcePressure, 1)
}

// 回落后复位，再次越线要能再发——否则一次高水位之后这条告警就永久哑了。
func TestResourcePressureRearmsAfterRecovery(t *testing.T) {
	st, hub := newTestStoreHub(t)
	task := mustCreateTask(t, st, proto.TaskStateRunning)

	restore := fakeAdmission(prochost.Admission{Used: 2200, Limit: 2400, Known: true})
	active := scanPressure(st, hub, false, testLogger(t))
	restore()

	restore = fakeAdmission(prochost.Admission{Used: 100, Limit: 2400, Known: true})
	active = scanPressure(st, hub, active, testLogger(t))
	if active {
		t.Fatalf("回落后应复位")
	}
	restore()

	restore = fakeAdmission(prochost.Admission{Used: 2300, Limit: 2400, Known: true})
	defer restore()
	scanPressure(st, hub, active, testLogger(t))
	assertEventCount(t, st, task.ID, proto.EventTypeResourcePressure, 2)
}

// 失败事件必须带死亡时刻的占用快照：「死亡时 2390/2400」与「死亡时 300/2400」
// 一眼定性两个完全不同的方向，双向堵误判——既防把配额问题当代码 bug 查，
// 也防把代码 bug 甩锅给配额。
func TestFailedPayloadCarriesUsageSnapshot(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{Used: 2390, Limit: 2400, Known: true})
	defer restore()
	p := newFailedPayload("executor 进程消失")
	if p.FailReason != "executor 进程消失" {
		t.Fatalf("原因不该被改写，得到 %q", p.FailReason)
	}
	if p.ProcUsage == nil || p.ProcUsage.Used != 2390 || p.ProcUsage.Limit != 2400 {
		t.Fatalf("应附带占用快照，得到 %+v", p.ProcUsage)
	}
}

// 读不出数时快照留空而不是填 0：一个「0/0」的快照会被读成「死亡时机器很空闲」，
// 那是彻头彻尾的谎话，比没有快照更糟。
func TestFailedPayloadOmitsUnknownUsage(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{})
	defer restore()
	if p := newFailedPayload("x"); p.ProcUsage != nil {
		t.Fatalf("读数未知时不该附快照，得到 %+v", p.ProcUsage)
	}
}

// 读数未知：什么都不做，也不复位——把「量不出来」当成「回落了」会让
// 下一次真越线时因为状态错乱而漏报。
func TestResourcePressureUnknownIsNoop(t *testing.T) {
	st, hub := newTestStoreHub(t)
	task := mustCreateTask(t, st, proto.TaskStateRunning)
	restore := fakeAdmission(prochost.Admission{})
	defer restore()
	if got := scanPressure(st, hub, true, testLogger(t)); !got {
		t.Fatalf("未知读数不应复位置位状态")
	}
	assertEventCount(t, st, task.ID, proto.EventTypeResourcePressure, 0)
}
```

> `newTestStoreHub` / `mustCreateTask` / `assertEventCount` / `testLogger`：若
> `watchdog_test.go` 已有等价助手就直接复用，没有则在本文件补最小实现。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestResourcePressure -v`
Expected: FAIL，`undefined: scanPressure` / `undefined: proto.EventTypeResourcePressure`

- [ ] **Step 3: 写实现**

`internal/proto/proto.go` —— 在事件类型常量块末尾加：

```go
	// EventTypeResourcePressure 表示执行机的进程余量已达高水位（参考上限的九成）。
	//
	// 为什么必须是一类**唤醒**事件而不只是日志：日志在执行机上，审核者手边
	// 没有；而这条告警的全部价值就在于「在第一条 fork 失败出现之前」让审核者
	// 知道要收敛。2026-08-12 事故里没有任何前兆，第一个信号就是整机瘫痪。
	EventTypeResourcePressure EventType = "resource_pressure"
```

`internal/agentd/watchdog.go` —— 加 payload 与扫描函数：

```go
// resourcePressurePayload 是高水位事件的载荷：审核者要靠这两个数字判断
// 该不该收敛，只说「压力大」没有任何操作价值。
type resourcePressurePayload struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

// scanPressure 判读一次进程余量，越线沿时给每个活跃任务发一条高水位事件。
//
// 参数：
//   - active: 上一轮的置位状态（越线后为 true）
//   - 其余同 scanStalled
//
// 返回：本轮结束后的置位状态，由调用方持有并回传——**不用包级变量**，
// 那会让两个 agentd 实例（测试里常见）互相踩状态。
//
// 三条语义：
//   - 越线（!active && NearFull）：对每个活跃任务发一次，置位
//   - 已置位且仍在高水位：不重发。事件风暴会把审核者的会话刷爆，
//     反而淹掉真正要处置的工单
//   - 回落到水位线以下：复位，下次越线可再发
//
// 读数未知时**原样返回 active，什么都不做**：把「量不出来」当成「回落了」，
// 会让下一次真越线因为状态错乱而漏报——漏报一次高水位，就等于回到事故当天
// 那个「第一个信号是整机瘫痪」的处境。
func scanPressure(st *store.Store, hub *Hub, active bool, log *slog.Logger) bool {
	a := admissionFn()
	if !a.Known {
		log.Debug("进程余量未知，跳过高水位判读")
		return active
	}
	if !a.NearFull() {
		if active {
			log.Info("进程余量已回落到水位线以下", "used", a.Used, "limit", a.Limit)
		}
		return false
	}
	if active {
		return true // 仍在高水位，已告警过，不重发
	}
	tasks, err := st.ListTasks()
	if err != nil {
		log.Error("高水位告警读取任务列表失败", "cause", err)
		return false // 没发出去就不置位，下一轮重试
	}
	fired := 0
	for _, t := range tasks {
		// 终态任务不需要知道机器压力大——它们已经不会再 fork 任何东西了。
		// 用 IsTerminal 取反而不是枚举活跃态：新增状态时这里自动跟上
		if t.State.IsTerminal() {
			continue
		}
		evt, aerr := st.AppendEvent(t.ID, proto.EventTypeResourcePressure,
			resourcePressurePayload{Used: a.Used, Limit: a.Limit})
		if aerr != nil {
			log.Error("追加高水位事件失败", "task", t.ID, "cause", aerr)
			continue
		}
		hub.Publish(evt)
		fired++
	}
	log.Warn("执行机进程余量达高水位，已告警活跃任务",
		"used", a.Used, "limit", a.Limit, "fired", fired)
	return true
}
```

`runWatchdog` 的循环体改为持有并传递状态：

```go
	pressure := false
	for {
		select {
		case <-ctx.Done():
			log.Info("看门狗退出", "cause", ctx.Err())
			return
		case <-ticker.C:
			scanStalled(st, hub, stallTimeout, log)
			pressure = scanPressure(st, hub, pressure, log)
		}
	}
```

并在 `RunWatchdog` 的文档注释里补一句：每轮除卡住判定外，还判读一次进程余量高水位。

`internal/agentd/manager.go` —— `failedPayload` 加字段并新增构造助手：

```go
// failedPayload 是 failed 事件的 payload。
type failedPayload struct {
	FailReason string `json:"fail_reason"`
	// ProcUsage 是任务失败时刻的进程占用快照；读不出数时为 nil。
	//
	// 为什么用指针 + omitempty 而不是零值：一个「0/0」的快照会被读成
	// 「死亡时机器很空闲」，那是彻头彻尾的谎话，比没有快照更糟。nil 表示
	// 「没测到」，与「测到了，很空闲」是两件事，必须能区分
	ProcUsage *prochost.Admission `json:"proc_usage,omitempty"`
}

// newFailedPayload 构造带占用快照的失败载荷。
//
// 参数：reason 为失败原因，原样保留不做改写
//
// 为什么所有 failed 事件都要走这里：三个发射点（Stop、executor 死亡对账、
// reconcile）各自构造等于三处都可能漏挂快照，而漏掉的那次恰好可能就是
// 配额事故那次。审核者拿到「死亡时 2390/2400」与「死亡时 300/2400」，
// 一眼就能定性两个完全不同的排查方向。
func newFailedPayload(reason string) failedPayload {
	p := failedPayload{FailReason: reason}
	if a := admissionFn(); a.Known {
		p.ProcUsage = &a
	}
	return p
}
```

把三个发射点改为走助手。三处都只设了 `FailReason`，所以是直接替换：

| 位置 | 改前 | 改后 |
|---|---|---|
| `manager.go:1137` | `failedPayload{FailReason: "审核者主动中止（handoff stop）"}` | `newFailedPayload("审核者主动中止（handoff stop）")` |
| `manager.go:2443` | `failedPayload{FailReason: r.FailReason}` | `newFailedPayload(r.FailReason)` |
| `reconcile.go:177` | `failedPayload{FailReason: reason}` | `newFailedPayload(reason)` |

```bash
grep -rn "failedPayload{" internal/agentd/*.go   # 改完后除助手内部应无残留
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestResourcePressure|TestFailedPayload' -v`
Expected: PASS（5 个用例）

Run: `go test ./internal/agentd/ -count=1` 与 `go test -race ./internal/agentd/`
Expected: 全绿

- [ ] **Step 5: 加关键节点日志**

已就位：越线 Warn（带 used/limit/fired）、回落 Info、未知 Debug、读任务列表失败 Error、单任务追加失败 Error。

两条刻意不打日志的决定，在代码里各留一句说明：
- 「仍在高水位」分支——那会是每分钟一条的噪声
- `newFailedPayload`——它只是构造载荷，失败本身在三个发射点各自已有日志；
  在这里再记一遍等于同一件事写四次

- [ ] **Step 6: 加注释自检**

确认：`EventTypeResourcePressure` 有 why（为什么必须是唤醒事件）；`resourcePressurePayload` 有 why（为什么必须带数字）；`scanPressure` 有参数/返回/三条语义/未知处理的 why；状态由调用方持有的理由已写明；`failedPayload.ProcUsage` 有「为什么用指针而不是零值」的 why；`newFailedPayload` 有「为什么三处都要走这里」的 why。

同时确认**没有**把新事件加进 `internal/client/client.go:122` 的不唤醒清单——它必须唤醒审核者。

- [ ] **Step 7: 提交**

```bash
git add internal/proto/proto.go internal/agentd/watchdog.go internal/agentd/manager.go internal/agentd/reconcile.go internal/agentd/watchdog_fence_test.go
git commit -m "feat(agentd): 高水位事件与失败事件占用快照，双向堵住配额误判"
```

---

## Task 7: 配置段与启动接线

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/agentd.go`
- Modify: `README.md`
- Test: `internal/config/config_test.go`（追加）

**Interfaces:**
- Consumes: `prochost.SetFencePolicy`（Task 2）
- Produces: `config.ProcFenceConfig{Disabled bool; ReserveRatio float64}`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/config/config_test.go`：

```go
// 缺省值：不禁用、保留比 0.1。这两个默认值是安全侧的——不写配置的用户
// 也应该被围栏保护。
func TestProcFenceDefaults(t *testing.T) {
	cfg, err := loadFromString(t, "listen: 127.0.0.1:7777\ntoken: t\n")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.ProcFence.Disabled {
		t.Fatalf("默认不应禁用围栏")
	}
	if cfg.ProcFence.ReserveRatio != 0.1 {
		t.Fatalf("默认保留比应为 0.1，得到 %v", cfg.ProcFence.ReserveRatio)
	}
}

// 显式配置生效。
func TestProcFenceExplicit(t *testing.T) {
	cfg, err := loadFromString(t, "listen: 127.0.0.1:7777\ntoken: t\n"+
		"proc_fence:\n  disabled: true\n  reserve_ratio: 0.25\n")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if !cfg.ProcFence.Disabled || cfg.ProcFence.ReserveRatio != 0.25 {
		t.Fatalf("显式配置未生效: %+v", cfg.ProcFence)
	}
}
```

> `loadFromString`：复用 `config_test.go` 既有的临时文件加载助手；没有就补一个
> 最小实现（写 t.TempDir() 下的 config.yaml 再 Load）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestProcFence -v`
Expected: FAIL，`cfg.ProcFence undefined`

- [ ] **Step 3: 写实现**

`internal/config/config.go` —— `Config` 结构体加字段：

```go
	// ProcFence 是 executor 进程围栏配置。默认启用、保留 10%。
	ProcFence ProcFenceConfig `yaml:"proc_fence,omitempty"`
```

新增类型：

```go
// ProcFenceConfig 描述 executor 进程围栏（RLIMIT_NPROC）的策略。
//
// 字段说明：
//   - Disabled: true 时完全不装围栏。逃生开关，正常不该用——2026-08-12 的
//     整机 fork 瘫痪就是无围栏状态下发生的
//   - ReserveRatio: 保留给 agentd/sshd/登录 shell 的名额占系统上限的比例；
//     0 或越界时取默认 0.1。这是「救护车道」的宽度，不是给 executor 的节流
//     旋钮——调小它不增加安全性，只会让 executor 更早撞墙
//
// 注意：yaml tag 必须写全。strict 解码器（KnownFields）按 tag 匹配键名，
// 不加 tag 时 yaml.v3 会把 ReserveRatio 映射成 reserveratio，与 README 里的
// reserve_ratio 对不上（RepoRoot 同款教训）。
type ProcFenceConfig struct {
	Disabled     bool    `yaml:"disabled"`
	ReserveRatio float64 `yaml:"reserve_ratio"`
}
```

`Load` 的补默认值段落里追加：

```go
	// 保留比缺省 0.1：不写配置的用户也应该被围栏保护，默认必须在安全侧
	if cfg.ProcFence.ReserveRatio <= 0 || cfg.ProcFence.ReserveRatio >= 1 {
		cfg.ProcFence.ReserveRatio = 0.1
	}
```

`cmd/agentd.go` —— 在 `slog.SetDefault(logger)` **之后**、`RecoverOnStartup` 之前插入：

```go
		// 围栏策略必须在任何 executor 被拉起之前注入：Start 算 L 时读的就是
		// 这两个包级值，晚一步就会有任务在默认策略下开工
		prochost.SetFencePolicy(cfg.ProcFence.Disabled, cfg.ProcFence.ReserveRatio)
```

`README.md` —— 两处新增。

**其一，配置段落**新增 `proc_fence`，覆盖三件事：字段含义与默认值、
「保留额是救护车道不是节流旋钮」的取值指导、以及归因文案（`进程配额耗尽（当前 uid
X/Y）`）出现时该怎么读。

**其二，新增一小节「派发指令里预埋一句资源纪律」**（spec §3.3）。这是本计划里
唯一一条**约定而非机制**的防线，落点在文档而不是代码——派发指令是审核者派发时
自己写的，代码里没有模板可改；executor 内部工具的报错文案我们更是改写不到
（opencode 内部）。原文照录，让审核者可以直接复制：

> 若见 `resource temporarily unavailable`：这是机器进程配额耗尽，不是你的代码
> bug——立即停止并行操作、收敛后报告审核者，不要重试，不要改代码。

并写明它的定位：**软约束，由 §3.2 兜底**。执行者可能没读、可能读了不照做；
真正的纠偏靠审核者——他手里有 `resource_pressure` 事件和失败事件里的占用快照，
两者都是机制。这一句只是让执行者有机会自己先反应过来，省一轮往返。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -run TestProcFence -v`
Expected: PASS（2 个用例）

Run: `go test ./... -count=1`
Expected: 全绿

- [ ] **Step 5: 加关键节点日志**

`SetFencePolicy` 自身已在 Task 2 打了 Info。此处额外确认 agentd 启动日志能反映围栏
状态——在 `logger.Info("agentd 服务启动", ...)` 那行追加两个字段：

```go
		logger.Info("agentd 服务启动", "addr", cfg.Listen, "data_dir", cfg.DataDir,
			"default_executor", cfg.Executor.Default,
			"proc_fence_disabled", cfg.ProcFence.Disabled,
			"proc_fence_reserve_ratio", cfg.ProcFence.ReserveRatio)
```

- [ ] **Step 6: 加注释自检**

确认：`ProcFenceConfig` 有类型注释 + 两个字段的说明（含「逃生开关，正常不该用」
与「不是节流旋钮」）+ yaml tag 的 why；`Load` 的补默认值处有「默认必须在安全侧」
的 why；`cmd/agentd.go` 插入点有「为什么必须在拉起任何 executor 之前」的 why。

- [ ] **Step 7: 提交**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/agentd.go README.md
git commit -m "feat(config,agentd): proc_fence 配置段与启动接线，默认启用保留 10%"
```

---

## Task 8: 变异检验

**Files:**
- 无新增文件；逐条改动既有实现后立即还原

**Interfaces:**
- Consumes: Task 1–7 的全部实现与用例
- Produces: 变异检验记录（写进完工报告）

> 这是 B47 立下的验证纪律：**用例存在**不等于**用例有效**。逐条把实现改错，
> 确认指定用例真的 FAIL，再还原。只在报告里声称做过是不合格的。

- [ ] **Step 1: 变异 1——保留额下限失效**

改 `fence.go`：把 `if reserve < fenceReserveFloor { reserve = fenceReserveFloor }` 整段删掉。
Run: `go test ./internal/prochost/ -run TestFenceLimitReserveFloorTakesOver -v`
Expected: **FAIL**（围栏算成 900 而非 800）
还原后 Run: `git diff --exit-code`，Expected: 无输出

- [ ] **Step 2: 变异 2——水位参考上限退回系统上限**

改 `fence.go` 的 `fenceReference`：让它无条件 `return procLimitFn()`。
Run: `go test ./internal/prochost/ -run TestCheckAdmissionWatermarkUsesFenceLimit -v`
Expected: **FAIL**（参考上限成了 2666，2160 不再判高水位）
还原后 Run: `git diff --exit-code`，Expected: 无输出

- [ ] **Step 3: 变异 3——低占用时也认领为配额耗尽**

改 `fence.go` 的 `ExplainForkFailure`：把 `if a.NearFull()` 改成 `if true`。
Run: `go test ./internal/prochost/ -run TestExplainForkFailureLowUsageStaysHonest -v`
Expected: **FAIL**（低占用被误判为配额耗尽——这正是「会说谎的诊断」）
还原后 Run: `git diff --exit-code`，Expected: 无输出

- [ ] **Step 4: 变异 4——未知读数改为 fail-closed**

改 `admission.go` 的 `checkProcHeadroom`：把 `if !a.Known { return nil }` 改成
`if !a.Known { return ErrNoProcHeadroom }`。
Run: `go test ./internal/agentd/ -run TestAdmissionFailsOpenWhenUnknown -v`
Expected: **FAIL**
还原后 Run: `git diff --exit-code`，Expected: 无输出

- [ ] **Step 5: 变异 5——高水位事件重复发送**

改 `watchdog.go` 的 `scanPressure`：删掉 `if active { return true }` 这条早返回。
Run: `go test ./internal/agentd/ -run TestResourcePressureFiresOnceOnRisingEdge -v`
Expected: **FAIL**（第二轮又发了一条，计数变 2）
还原后 Run: `git diff --exit-code`，Expected: 无输出

- [ ] **Step 6: 变异 6——围栏只压软限**

改 `fence_unix.go`：`rl := unix.Rlimit{Cur: uint64(n), Max: unix.RLIM_INFINITY}`。
Run: `go test ./internal/prochost/ -run TestFenceSurvivesSetsid -v`
Expected: 本用例**仍 PASS**（继承性不受影响）——**这是一条已知的检验缺口**：
现有用例覆盖不到「围栏能不能被拆掉」，而那正是软硬限同设的全部意义。
先还原变异，把用例补上，再重跑。

追加到 `internal/prochost/fence_inherit_test.go`：

```go
// TestHelperFenceRaise 扮演被围住的 executor：装上围栏后试图把限值抬回去。
func TestHelperFenceRaise(t *testing.T) {
	if os.Getenv(fenceHelperEnv) != "raise" {
		t.Skip("非 helper 调用")
	}
	want, _ := strconv.Atoi(os.Getenv("HANDOFF_FENCE_VALUE"))
	if err := setNprocLimit(want); err != nil {
		os.Stdout.WriteString("SETFAIL " + err.Error() + "\n")
		os.Exit(0)
	}
	// 抬回：软硬限同设为两倍。硬限只能降不能升（升需特权），所以这一步
	// 在正确实现下必然失败；只压软限的实现下则会成功
	if err := setNprocLimit(want * 2); err != nil {
		os.Stdout.WriteString("RAISE_DENIED\n")
	} else {
		os.Stdout.WriteString("RAISE_OK\n")
	}
	os.Exit(0)
}

// 围栏必须是单向门：被围住的进程不能把限值抬回去。只压软限的话，executor
// 一句 setrlimit 就能拆掉围栏，整个方案形同虚设——而继承性用例察觉不到这点。
func TestFenceCannotBeRaisedBack(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperFenceRaise")
	cmd.Env = append(os.Environ(), fenceHelperEnv+"=raise",
		"HANDOFF_FENCE_VALUE=4096")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper 执行失败: %v (输出 %q)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "RAISE_DENIED" {
		t.Fatalf("围栏应拆不掉，helper 报告 %q", got)
	}
}
```

Run: `go test ./internal/prochost/ -run TestFenceCannotBeRaisedBack -v`
Expected: PASS（无变异时）

重新施加变异 6，Run 同一条命令，Expected: **FAIL**（helper 报告 `RAISE_OK`）
还原后 Run: `git diff --exit-code -- internal/prochost/fence_unix.go`，Expected: 无输出

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/fence_inherit_test.go
git commit -m "test(prochost): 补单向门用例——围栏被拆掉时必须有用例失败"
```

---

## Task 9: 真机烟测与收尾

**Files:**
- Modify: `docs/superpowers/backlog.md`（B73 行验收回填）
- Create: `docs/superpowers/notes/2026-08-12-proc-fence-smoke.md`（烟测记录）

**Interfaces:**
- Consumes: Task 1–8 的全部实现
- Produces: 烟测证据、六闸门输出

- [ ] **Step 1: 六闸门**

逐条跑，把**实际输出**贴进完工报告（不要只写「全绿」）：

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./... -count=1
go test -race ./internal/prochost/ ./internal/agentd/
GOOS=windows go build ./...
```

- [ ] **Step 2: 起隔离实例**

**不要占用这台 devbox 的默认实例**（7777 端口、`~/.handoff` 数据目录上有别人在跑的
任务）。起独立端口 + 独立 DataDir 的实例，并记下 7777 那个 agentd 的 pid，收尾时确认
前后一致。

- [ ] **Step 3: 构造「围栏必然撞墙」的条件**

**不要用 fork 炸弹**。把隔离实例的 `proc_fence.reserve_ratio` 设成 `0.99`：
系统上限 2666 → 保留 2639 → 围栏 27，而机器上本来就有三百多个进程，于是
任何 executor 都必然撞墙。这样既能稳定复现，又完全不影响机器
（围栏只作用于该实例拉起的 shim，agentd 自己和别的任务不受影响）。

- [ ] **Step 4: 验证归因四件套**

逐条记录**原始输出**：

1. **准入闸拒发**：向隔离实例 dispatch 一个任务 → 期望 400，报文含
   `进程余量不足：当前 XXX/27`。
2. **shim 撞墙归因**：把 `reserve_ratio` 调到刚好让准入闸放行、但 shim 起
   executor 时撞墙的值 → 期望 `agentd.log` 里有带 `note` 与 `fence` 字段的
   「拉起执行者进程失败（进程配额）」Error。
3. **高水位事件**：调到活跃任务存在且占用达九成 → 期望审核者侧收到
   `resource_pressure` 事件，payload 带真实 used/limit。
4. **恢复正常**：把 `reserve_ratio` 改回 0.1、重启隔离实例 → 期望任务能正常
   dispatch 并跑完，`agentd.log` 里有「进程围栏已安装 limit=2400」。

- [ ] **Step 5: 验证围栏真的装上了**

在一个正常跑起来的任务里，用 `handoff run <task> 'ulimit -u; ulimit -Hu'`
读 executor 树看到的软硬限——期望**两个都等于 2400**（而不是 2666/4000）。

> 这一条是整个 Plan A 最有说服力的一次验收：它直接证明了 executor 及其后代
> 确实活在围栏里。

- [ ] **Step 6: 确认没有误伤**

- 7777 那个 agentd 的 pid 与 Step 2 记下的一致
- `ps -u $(whoami) | wc -l` 与开工前处于同一量级
- 隔离实例的任务目录与 worktree 已清理

- [ ] **Step 7: 写烟测记录并回填 backlog**

把 Step 1/4/5/6 的原始输出整理进
`docs/superpowers/notes/2026-08-12-proc-fence-smoke.md`。

`docs/superpowers/backlog.md` 的 B73 行：状态改 `✅ done(已验)`，`Spec` 列填
本计划的 spec 链接，`验收` 列填「六闸门全绿（贴命令与结果）；六条变异检验各自
FAIL 用例名与还原确认；真机烟测：准入拒发/shim 撞墙归因/高水位事件/恢复正常
四条 + `ulimit -u` 实测 2400；无原型/流程图，自动免除对照 08-12」。

**注意**：B72 仍是 `💡 idea`，本计划**不动它**——出生登记是 Plan B。

- [ ] **Step 8: 提交**

```bash
git add docs/superpowers/notes/2026-08-12-proc-fence-smoke.md docs/superpowers/backlog.md
git commit -m "docs: 进程围栏真机烟测记录与 B73 验收回填"
```

---

## 完工报告要包含

- 9 个 task 的完成情况
- 六闸门的**实际输出**
- 六条变异检验各自的 FAIL 用例名与还原确认（含 Task 8 Step 6 那条已知缺口是怎么补上的）
- 真机烟测的四条归因验证 + `ulimit -u` 实测值
- 一句提醒：README 新增的「派发指令资源纪律」那段是否值得镜像进用户全局的
  `handoff` skill（`~/.claude/skills/handoff/`）。**不要自己去改它**——那在仓库
  之外，属于用户的全局配置，由用户决定
- 任何你认为计划写错了、或实现时不得不偏离计划的地方，以及为什么
