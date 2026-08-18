# B105 每任务进程归属：摆脱采样时机 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 `Footprint`/`Sweep` 增加第三条**不依赖采样时机**的归属判据，把「工具壳只活 1–2 秒就退出」那半边盲区补上。

**Architecture:** 新增平台原语 `attributes(pid, TaskCred) (bool, error)`：Linux 读 `/proc/<pid>/environ` 里的 `HANDOFF_TASK_ID`，macOS 读 `proc_pidinfo` 拿 cwd 与任务 worktree 比对，Windows 返回 `errNotSupported`（归属已由 B37 的 Job Object 从源头消解）。标记由 `prochost.Start` 统一注入，`classify` 的成员集从「pgid ∪ roster」变成「pgid ∪ roster ∪ mark」。

**Tech Stack:** Go；darwin 用 stdlib `syscall.Syscall6(336, …)`（`x/sys/unix` 不包装 `proc_pidinfo`）；linux 读 `/proc`；**不引入任何新模块，不引入 cgo**。

**Spec:** `docs/superpowers/specs/2026-08-18-task-process-attribution-design.md`

## Global Constraints

- **一律不得 fork**。禁止 `ps` / `lsof` / 任何 `exec.Command`。这套代码要在机器已经 fork 不动时仍然可用（`procenum.go` 包注释）。**测试代码不受此限**——测试里起子进程是必须的。
- **不引入 cgo，不引入新模块**。本仓库依赖纯 Go 交叉编译（`GOOS=windows` / `GOOS=linux` 都要能从 macOS 编出来）。
- **不改 `Footprint` / `Sweep` / `CountGroup` / `UIDUsage` 的签名**，调用方零改动。
- **不删 roster**。B103 的名册累积与终态清扫保留，本轮只**增加**一条来源。
- **判不出结论时不得猜值**：返回 `errNotSupported` 让调用方降级，不得返回 `false` 冒充「不属于」，也不得返回空集冒充「没有残留」。
- **执行机是 macOS**：Linux 与 Windows 的**运行期**行为一律不得声称「已验证」，只能声称「编译通过 / 单测通过」。Linux 真机验收由审核者执行。
- **完工门六条，一条都不许跳**（`gofmt` 那条尤其）：
  ```
  go build ./...
  go vet ./...
  go test ./... -count=1
  gofmt -l $(git ls-files '*.go')
  GOOS=windows GOARCH=amd64 go build ./...
  GOOS=windows GOARCH=amd64 go vet ./...
  ```
- **日志用 `log()`（包内 slog 封装），禁止 `fmt.Printf`**。
- 环境变量名 **`HANDOFF_TASK_ID`**（单数，不是 `HANDOFF_TASK_IDS`——链式方案已在 spec §7.4 明确不做）。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/prochost/taskmark.go`（新建） | 平台无关契约：`TaskCred` 类型、`attributesFn` 测试缝、`markMembers` 批量判定、`applyTaskMark` 注入 |
| `internal/prochost/taskmark_darwin.go`（新建） | darwin 实现：`proc_pidinfo` 读 cwd，含偏移量运行期自检 |
| `internal/prochost/taskmark_linux.go`（新建） | linux 实现：读 `/proc/<pid>/environ` |
| `internal/prochost/taskmark_other.go`（新建） | 非 darwin/linux：一律 `errNotSupported` |
| `internal/prochost/prochost.go`（改） | `Spec` 加 `TaskID`/`MarkRoot`；`Handle` 加同名两字段；`Start` 里调 `applyTaskMark` 并回填 `Handle` |
| `internal/prochost/footprint.go`（改） | `classify` 并入第三条来源；`Footprint` 日志分列；`Sweep` 增加标记段回收 |
| `internal/executor/{claudecode,codex,grok,opencode}/proc.go`（改） | 各自组装 `Spec` 时填 `TaskID` 与 `MarkRoot` |
| `cmd/agentd.go`（改） | 启动期播报本平台是否具备标记能力（一条，不刷屏） |

---

## Task 1: 平台契约与降级路径

**Files:**
- Create: `internal/prochost/taskmark.go`
- Create: `internal/prochost/taskmark_other.go`
- Create: `internal/prochost/taskmark_darwin.go`（**桩**，Task 2 替换）
- Create: `internal/prochost/taskmark_linux.go`（**桩**，Task 3 替换）
- Modify: `internal/prochost/prochost.go`（`Spec` 加 `TaskID` / `MarkRoot` 两个字段）
- Test: `internal/prochost/taskmark_test.go`

> **为什么 Task 1 就要碰这些**：`taskmark.go` 里 `attributesFn = attributes` 引用的
> `attributes` 只在平台文件里定义，而 `applyTaskMark` 要读 `Spec.TaskID`——
> 只建 `taskmark_other.go`（build tag `!darwin && !linux`）的话，本任务在 darwin
> 与 linux 上**根本编译不过**。所以两个平台桩与两个 Spec 字段是 Task 1 的编译前置，
> 必须一起落，它们都不改变任何行为。

**Interfaces:**
- Consumes: 无（本任务是地基）
- Produces:
  - `type TaskCred struct { TaskID string; MarkRoot string }`
  - `func markMembers(cred TaskCred, procs []procEntry) (members []int, supported bool)`
  - `var attributesFn = attributes`（包级测试缝）
  - `func attributes(pid int, cred TaskCred) (bool, error)`（各平台实现）

- [ ] **Step 1: 写失败测试**

在 `internal/prochost/taskmark_test.go`：

```go
package prochost

import (
	"errors"
	"testing"
)

// TestMarkMembersUnsupportedReportsNotSupported 钉住「平台不支持」必须表达为
// supported=false，而不是空集——空集会被上层当成「确实没有成员」。
func TestMarkMembersUnsupportedReportsNotSupported(t *testing.T) {
	old := attributesFn
	defer func() { attributesFn = old }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }

	members, supported := markMembers(TaskCred{TaskID: "t1"}, []procEntry{{PID: 10}, {PID: 11}})
	if supported {
		t.Fatalf("平台不支持时 supported 必须为 false")
	}
	if len(members) != 0 {
		t.Fatalf("平台不支持时不得返回成员，实得 %v", members)
	}
}

// TestMarkMembersEmptyCredIsNoop 钉住凭据为空时判据整个不参与：
// 这是「仅托管 worktree 可杀」与「升级前 proc.json 无字段」两条降级的共同出口。
func TestMarkMembersEmptyCredIsNoop(t *testing.T) {
	old := attributesFn
	defer func() { attributesFn = old }()
	called := 0
	attributesFn = func(pid int, cred TaskCred) (bool, error) { called++; return true, nil }

	members, supported := markMembers(TaskCred{}, []procEntry{{PID: 10}})
	if called != 0 {
		t.Fatalf("凭据为空时不应调用平台原语，实调 %d 次", called)
	}
	if supported || len(members) != 0 {
		t.Fatalf("凭据为空应表达为不可用：supported=%v members=%v", supported, members)
	}
}

// TestMarkMembersSkipsPerPIDFailure 钉住单个 pid 读失败不影响整批——
// 进程在枚举与读取之间退出是常态，不是异常。
func TestMarkMembersSkipsPerPIDFailure(t *testing.T) {
	old := attributesFn
	defer func() { attributesFn = old }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) {
		switch pid {
		case 10:
			return true, nil
		case 11:
			return false, errors.New("no such process")
		default:
			return false, nil
		}
	}

	members, supported := markMembers(TaskCred{TaskID: "t1"},
		[]procEntry{{PID: 10}, {PID: 11}, {PID: 12}})
	if !supported {
		t.Fatalf("有 pid 读成功时 supported 应为 true")
	}
	if len(members) != 1 || members[0] != 10 {
		t.Fatalf("应只归属 pid=10，实得 %v", members)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run TestMarkMembers -count=1`
Expected: FAIL，`undefined: TaskCred` / `undefined: markMembers` / `undefined: attributesFn`

- [ ] **Step 3: 写 `Spec` 的两个字段与两个平台桩（编译前置）**

在 `internal/prochost/prochost.go` 的 `Spec` 里，紧跟 `NprocLimit` 之后加两个字段
（注释原文见 Task 4 Step 3，此处一次写到位，Task 4 不再重复加）：

```go
	TaskID   string `json:"task_id,omitempty"`
	MarkRoot string `json:"mark_root,omitempty"`
```

再落两个平台桩。`internal/prochost/taskmark_darwin.go`：

```go
//go:build darwin

// taskmark_darwin.go —— darwin 的任务标记实现。
//
// **本文件当前是 Task 1 为满足编译而落的桩**，真实实现由 Task 2 整体替换
// （含本文件头注释）。桩期间 darwin 上标记判据不参与，归属退回 pgid + roster。
package prochost

func attributes(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }
```

`internal/prochost/taskmark_linux.go` 同构，build tag 换成 `//go:build linux`，
文件头注释里的 Task 2 换成 Task 3。

**桩必须返回 `errNotSupported`，绝不能返回 `(false, nil)`。** 差别是承重的：
`errNotSupported` 的含义是「这条判据不可用」，调用方据此降级回 pgid + roster——
那是 spec §8 设计好的一档；而 `(false, nil)` 的含义是「读到了，且这个进程不属于
本任务」，那是一个我们并没有得出的结论，会让 `Sweep` 无声地漏杀。

- [ ] **Step 4: 写 `taskmark_other.go`**

```go
//go:build !darwin && !linux

// taskmark_other.go —— 非 darwin/linux 的任务标记空实现。
//
// 一律返回 errNotSupported 而不是 false：false 的含义是「读到了，且不属于」，
// 与「这个平台我们读不了」是两回事——后者必须让调用方降级为 pgid + roster，
// 而不是据此认定进程不属于任务（那会让清扫漏掉真正的残留）。
//
// Windows 走这条：归属问题已由 B37 的 Job Object 从源头消解（内核容器连坐回收），
// 不需要事后判定。
package prochost

func attributes(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }
```

- [ ] **Step 5: 写 `taskmark.go`**

```go
// taskmark.go —— 任务标记：不依赖采样时机的进程归属判据（平台无关契约）。
//
// 职责：
//   - 定义 TaskCred：一次归属判定所需的全部凭据
//   - 声明平台原语 attributes 的契约，并提供批量判定 markMembers
//   - applyTaskMark：在 Start 处把标记注入执行者的环境
//
// 边界：
//   - 只回答「这个进程属不属于这个任务」，不发信号、不做存活判定——
//     回收是 footprint.go 的 Sweep 的事
//   - **实现一律不得 fork**（同 procenum.go 的硬约束）
//   - 与 pgid / roster 是**并列**的第三条来源，不替代它们：pgid 覆盖同组，
//     roster 覆盖采到过的逃逸后代，标记覆盖「壳活得太短、一次都没采到」的那批
package prochost

import (
	"os"
	"path/filepath"
)

// TaskMarkEnvKey 是注入执行者环境的标记变量名。
//
// 三平台都注入。macOS 上它对 Apple 平台二进制不可读（内核屏蔽），因此不作判据，
// 但人工用 `ps -E` 排障时仍然有用。
const TaskMarkEnvKey = "HANDOFF_TASK_ID"

// TaskCred 是一次归属判定所需的全部凭据，由 Handle 投影而来。
//
// 两个字段各自的零值都表示「对应判据不可用」，不是「判据通过」——这与 Handle
// 的 omitempty 降级纪律是同一条：升级前写下的 proc.json 没有这些字段，读出空串
// 就该退回 pgid + roster，而不是拿一个空凭据去匹配。
type TaskCred struct {
	// TaskID 是本任务的 UUID。linux 判据拿它与进程 environ 里的
	// TaskMarkEnvKey 比对。
	TaskID string
	// MarkRoot 是 cwd 判据的比对根，**已做符号链接解析**的绝对路径。
	//
	// 空串表示本任务不允许用 cwd 归属。agentd 只在托管 worktree 形态下填它——
	// 「仅托管 worktree 可杀」这条边界因此是数据决定的，不存在「某处忘了检查」
	// 的可能。托管 worktree 在 DataDir/worktrees 下，是 handoff 自建自删的目录，
	// 人类没有理由待在里面；而共享主仓里一定有用户自己的编辑器与 shell，
	// 拿 cwd 去杀会打掉它们。
	MarkRoot string
}

// empty 报告凭据是否完全不可用（两条判据都没有比对依据）。
func (c TaskCred) empty() bool { return c.TaskID == "" && c.MarkRoot == "" }

// attributesFn 是平台原语的测试缝（包级 var 而非直接调用），与 enumProcsFn /
// aliveFn / killGroupFn 同款路数：判据测试要喂固定结论，不能依赖真实进程。
var attributesFn = attributes

// markMembers 在一次进程快照里筛出属于 cred 的成员。
//
// 参数：cred 为任务凭据；procs 为一次进程快照（与其它判据共用，避免重复枚举）
//
// 返回：
//   - members: 归属本任务的 pid
//   - supported: 本平台是否具备标记判定能力。**false 时 members 必然为空，
//     且调用方必须理解为「这条判据不可用」而非「没有成员」**
//
// 注意：
//   - 单个 pid 读失败（进程刚退出、权限不足）只跳过该条，不影响整批——进程在
//     枚举与读取之间消失是常态
//   - 成功路径刻意不打日志：Footprint 被 handoff status 按任务高频调用，
//     每个 pid 记一行会把 agentd.log 淹掉。汇总由调用方在边界上打一次
func markMembers(cred TaskCred, procs []procEntry) (members []int, supported bool) {
	if cred.empty() {
		return nil, false
	}
	for _, p := range procs {
		ok, err := attributesFn(p.PID, cred)
		if err != nil {
			if isNotSupported(err) {
				// 平台整体不具备该能力：没必要把剩下几百个 pid 再问一遍
				return nil, false
			}
			// 单个进程读不到：跳过。这是常态，降到 Debug 避免刷屏
			log().Debug("读任务标记失败，跳过该进程", "pid", p.PID, "cause", err)
			continue
		}
		supported = true
		if ok {
			members = append(members, p.PID)
		}
	}
	return members, supported
}

// isNotSupported 判别「本平台不具备该能力」这一类错误。
//
// 为什么单独抽一个函数：darwin 的实现会在运行期自检失败后也退回这个语义
// （见 taskmark_darwin.go 的偏移量自检），判别点集中在一处才不会漏。
func isNotSupported(err error) bool { return err == errNotSupported }

// applyTaskMark 把任务标记注入 spec.Env。
//
// 为什么放在 Start 而不是各 adapter：Start 是四个 adapter 的唯一汇流点，
// 在这里注入既让 adapter 零改动，也保证 Handle.TaskID 与实际注入的环境变量
// 出自同一处赋值、不可能对不上。紧邻的 applyFencePolicy 是同款先例。
//
// 注意：spec.TaskID 为空时什么都不做——没有 id 就没有可比对的标记，
// 注入一个空值只会让判据把所有没有该变量的进程都算成命中。
func applyTaskMark(spec *Spec) {
	if spec.TaskID == "" {
		return
	}
	spec.Env = append(spec.Env, TaskMarkEnvKey+"="+spec.TaskID)
}

// resolveMarkRoot 把一个工作目录路径归一成可用于 cwd 比对的形态。
//
// 参数：dir 为任务工作区目录；managed 表示它是否为 agentd 托管的 worktree
//
// 返回：可比对的绝对路径；不允许用 cwd 归属时返回空串
//
// 注意：**必须做符号链接解析**。内核返回的 cwd 是解析后的（macOS 上
// /var/... 会变成 /private/var/...），直接拿未解析的路径去比会得到一个
// 看起来很干净的假阴性——判据静默失效而没有任何报错。
func resolveMarkRoot(dir string, managed bool) string {
	if !managed || dir == "" {
		return ""
	}
	resolved, err := filepathEvalSymlinks(dir)
	if err != nil {
		log().Warn("解析 worktree 路径失败，本任务不启用 cwd 归属",
			"dir", dir, "cause", err)
		return ""
	}
	return resolved
}

// filepathEvalSymlinks 是 filepath.EvalSymlinks 的测试缝：单测要构造解析失败。
var filepathEvalSymlinks = filepath.EvalSymlinks
```

本文件的 import 是 `"os"` 与 `"path/filepath"`（`os` 供 Task 7 的
`MarkCapability` 用，`path/filepath` 供上面的测试缝用）。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/prochost/ -run TestMarkMembers -count=1`
Expected: PASS（3 个用例）

- [ ] **Step 7: 加关键节点日志**

本任务的日志点已内联在 Step 4 的代码里，逐条核对：
- `markMembers` 单 pid 读失败 → `log().Debug("读任务标记失败，跳过该进程", "pid", …, "cause", err)`
- `resolveMarkRoot` 解析失败 → `log().Warn("解析 worktree 路径失败，本任务不启用 cwd 归属", "dir", …, "cause", err)`
- **成功路径刻意不在此处打**：汇总日志由 Task 5 在 `Footprint` 边界上打一次（`markMembers` 会被高频调用，逐条记会淹掉 agentd.log）

确认没有用 `fmt.Printf`：`grep -n "fmt.Print" internal/prochost/taskmark*.go` 应无输出。

- [ ] **Step 8: 加注释**

确认 Step 4 的文件头注释（职责 + 边界）已写；`TaskCred` 两个字段各自的零值语义已写；`markMembers` 的 `supported=false ≠ 空集` 已写；`resolveMarkRoot` 的符号链接解析原因已写；`applyTaskMark` 的「为什么在 Start」已写。

- [ ] **Step 9: Commit**

```bash
git add internal/prochost/taskmark.go internal/prochost/taskmark_other.go internal/prochost/taskmark_darwin.go internal/prochost/taskmark_linux.go internal/prochost/prochost.go internal/prochost/taskmark_test.go
git commit -m "feat(prochost): 任务标记的平台契约与降级路径"
```

---

## Task 2: darwin 实现（cwd + 偏移量运行期自检）

**Files:**
- Modify: `internal/prochost/taskmark_darwin.go`（**整体替换 Task 1 落的桩**，含文件头注释）
- Test: `internal/prochost/taskmark_darwin_test.go`

**Interfaces:**
- Consumes: `TaskCred`（Task 1）
- Produces: `func attributes(pid int, cred TaskCred) (bool, error)`（darwin 版）

- [ ] **Step 1: 写失败测试**

`internal/prochost/taskmark_darwin_test.go`：

```go
//go:build darwin

package prochost

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestCwdOffsetSelfCheckPasses 钉住偏移量自检本身是有效的：
// 它必须能读出本进程真实的 cwd。自检失效时整条判据会降级，
// 那时下面的归属测试会全部「正确地」返回 not supported——
// 所以先单独把自检钉住，否则后面的绿是假绿。
func TestCwdOffsetSelfCheckPasses(t *testing.T) {
	if !cwdReadable() {
		t.Fatalf("cwd 偏移量自检未通过：本机上 proc_pidinfo 读不出自己的 cwd")
	}
	got, err := cwdOf(os.Getpid())
	if err != nil {
		t.Fatalf("读自身 cwd 失败: %v", err)
	}
	want, _ := os.Getwd()
	wantResolved, _ := filepath.EvalSymlinks(want)
	if got != wantResolved {
		t.Fatalf("自身 cwd 不符：实得 %q 期望 %q", got, wantResolved)
	}
}

// TestAttributesByCwd 验真实进程：worktree 内的（含 setsid 出去的）命中，
// worktree 外的不命中。
//
// 对照组必须先命中，再断言目标——先证明扫描此刻没失灵，否则「没捞到」
// 与「读取机制坏了」在输出上完全一样（B37 spec §12.5 的教训）。
func TestAttributesByCwd(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("解析临时目录失败: %v", err)
	}
	cred := TaskCred{TaskID: "task-1", MarkRoot: resolved}

	start := func(dir string, setsid bool) *exec.Cmd {
		c := exec.Command("/bin/sleep", "30")
		c.Dir = dir
		if setsid {
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}
		if err := c.Start(); err != nil {
			t.Fatalf("起子进程失败: %v", err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
		return c
	}

	inside := start(resolved, false)
	insideSetsid := start(resolved, true)
	outside := start(os.TempDir(), false)
	time.Sleep(300 * time.Millisecond)

	// 前置断言：对照组（确知在 worktree 内）必须命中
	if ok, err := attributes(inside.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("对照组未命中，本轮测量无效：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(insideSetsid.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("setsid 出去的子进程应命中：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(outside.Process.Pid, cred); err != nil || ok {
		t.Fatalf("worktree 外的进程不应命中：ok=%v err=%v", ok, err)
	}
}

// TestAttributesEmptyMarkRootNeverMatches 钉住 MarkRoot 为空时一律不命中——
// 这是「仅托管 worktree 可杀」在平台层的最后一道防线。
func TestAttributesEmptyMarkRootNeverMatches(t *testing.T) {
	ok, err := attributes(os.Getpid(), TaskCred{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if ok {
		t.Fatalf("MarkRoot 为空时不得命中任何进程")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestCwdOffset|TestAttributes' -count=1`
Expected: FAIL，`undefined: cwdReadable` / `undefined: cwdOf`

- [ ] **Step 3: 写实现**

`internal/prochost/taskmark_darwin.go`：

```go
//go:build darwin

// taskmark_darwin.go —— darwin 的任务标记实现：按 cwd 归属。
//
// 为什么是 cwd 而不是环境变量：macOS 对 Apple 平台二进制的 environ 做了屏蔽，
// 非 root 读 /bin/sleep、/bin/zsh 的环境变量恒为空——而工具壳正是 zsh、
// 泄漏的正是 sleep 与编译进程，环境变量方案恰好看不见最需要看见的那一类
// （spec §4.1 实测：全表 environ 可读率对平台二进制为 0，cwd 为 99.9%）。
//
// 为什么不用 x/sys/unix：它不包装 proc_pidinfo。cgo 不是选项（本仓库依赖
// 纯 Go 交叉编译），故走 stdlib 的 syscall.Syscall6。该路径在 Go 文档里标注
// deprecated，因此本文件带运行期自检，失效即整条判据降级（见 cwdReadable）。
//
// 边界：只读 cwd，不发信号、不判存活；不得 fork。
package prochost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	// sysProcInfo 是 darwin 的 proc_info 系统调用号。
	sysProcInfo = 336
	// callPIDInfo 对应 PROC_INFO_CALL_PIDINFO。
	callPIDInfo = 2
	// flavorVnodePath 对应 PROC_PIDVNODEPATHINFO，返回 proc_vnodepathinfo。
	flavorVnodePath = 9
	// vipPathOffset 是 cwd 字符串在 proc_vnodepathinfo 里的偏移。
	//
	// **这个值是实测出来的，不是照头文件推算的**：按 struct vinfo_stat 手算
	// 会得到别的数。它由 cwdReadable() 在运行期用「能否读出本进程自己的 cwd」
	// 反证，对不上就整条判据降级，绝不拿一个可能错位的解析结果去归属进程。
	vipPathOffset = 152
	// vnodePathBufSize 是一次调用的缓冲区大小（两个 vnode_info_path，各含
	// MAXPATHLEN 的路径）。
	vnodePathBufSize = 4096
)

// cwdOf 读出 pid 的当前工作目录（不 fork）。
//
// 返回：内核给出的**已解析**绝对路径；读不到时返回错误。
//
// 注意：进程刚退出、或对方是本 uid 之外的进程时会失败，这是常态，
// 调用方跳过该条即可，不该据此认定「不属于本任务」。
func cwdOf(pid int) (string, error) {
	buf := make([]byte, vnodePathBufSize)
	got, _, errno := syscall.Syscall6(sysProcInfo, callPIDInfo, uintptr(pid),
		flavorVnodePath, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return "", fmt.Errorf("proc_pidinfo(vnodepath) pid=%d: %w", pid, errno)
	}
	if got == 0 || int(got) <= vipPathOffset {
		return "", fmt.Errorf("proc_pidinfo(vnodepath) pid=%d 返回 %d 字节，不足以含路径", pid, got)
	}
	b := buf[vipPathOffset:got]
	if i := indexZero(b); i >= 0 {
		b = b[:i]
	}
	if len(b) == 0 {
		return "", fmt.Errorf("proc_pidinfo(vnodepath) pid=%d 路径为空", pid)
	}
	return string(b), nil
}

// indexZero 返回第一个 0 字节的下标；没有则返回 -1。
func indexZero(b []byte) int {
	for i := range b {
		if b[i] == 0 {
			return i
		}
	}
	return -1
}

// cwdSelfCheck 缓存偏移量自检结果：只做一次，结果全进程复用。
var cwdSelfCheck struct {
	once sync.Once
	ok   bool
}

// cwdReadable 报告本机上 cwd 判据是否可用。
//
// 判据：拿本进程试一次——用 syscall 读出的 cwd 必须等于 os.Getwd() 的解析结果。
//
// 为什么要这道自检：vipPathOffset 是实测值，而 syscall.Syscall6 在 darwin 上是
// deprecated 路径。两者任一在未来失效，解析出来的都会是垃圾字符串——而垃圾
// 字符串「恰好不等于任何 MarkRoot」，判据会**静默退化成永远不命中**，没有任何
// 报错。有了自检，失效表现为整条判据降级回 pgid + roster（spec §8 第四档），
// 那是设计好的行为，不是新的失败模式。
func cwdReadable() bool {
	cwdSelfCheck.once.Do(func() {
		want, err := os.Getwd()
		if err != nil {
			log().Warn("取本进程 cwd 失败，停用 cwd 归属判据", "cause", err)
			return
		}
		wantResolved, err := filepath.EvalSymlinks(want)
		if err != nil {
			log().Warn("解析本进程 cwd 失败，停用 cwd 归属判据", "cwd", want, "cause", err)
			return
		}
		got, err := cwdOf(os.Getpid())
		if err != nil {
			log().Warn("proc_pidinfo 自检失败，停用 cwd 归属判据，归属退回 pgid+roster",
				"cause", err)
			return
		}
		if got != wantResolved {
			log().Warn("proc_pidinfo 自检结果与 os.Getwd 不符，停用 cwd 归属判据",
				"got", got, "want", wantResolved, "offset", vipPathOffset)
			return
		}
		cwdSelfCheck.ok = true
		log().Info("cwd 归属判据可用", "offset", vipPathOffset)
	})
	return cwdSelfCheck.ok
}

// attributes 判定 pid 是否属于 cred 所描述的任务（darwin：按 cwd）。
//
// 返回：
//   - true: 该进程的 cwd 落在 cred.MarkRoot 之内
//   - errNotSupported: 本机自检未通过（见 cwdReadable），调用方应降级
//   - 其它错误: 该 pid 读不到（多为刚退出），调用方跳过该条
//
// 注意：cred.MarkRoot 为空即一律不命中——「仅托管 worktree 可杀」在这里落地。
// 本判据**不抗 cd**：进程 cd 出任务目录后就脱钩，这是 macOS 侧结构性的覆盖上限。
func attributes(pid int, cred TaskCred) (bool, error) {
	if cred.MarkRoot == "" {
		return false, nil
	}
	if !cwdReadable() {
		return false, errNotSupported
	}
	cwd, err := cwdOf(pid)
	if err != nil {
		return false, err
	}
	return underRoot(cwd, cred.MarkRoot), nil
}

// underRoot 判定 path 是否等于 root 或在 root 之下。
//
// 为什么不用 strings.HasPrefix 单独判：/a/bc 会被 /a/b 的前缀匹配命中，
// 那是另一个目录。必须带上分隔符。
func underRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/prochost/ -run 'TestCwdOffset|TestAttributes' -count=1 -v`
Expected: PASS（3 个用例）。`TestCwdOffsetSelfCheckPasses` 必须真的 PASS 而不是 skip——它 FAIL 说明本机偏移量不对，此时**不要改测试去迁就**，要按 Step 3 注释里的方法重新实测偏移量。

- [ ] **Step 5: 加关键节点日志**

日志点已内联在 Step 3，逐条核对：
- 自检成功 → `log().Info("cwd 归属判据可用", "offset", …)`（**成功路径必须有一行**，否则无从区分「判据在用」与「判据早就降级了」）
- 自检三种失败各一条 Warn，均带 cause 或 got/want
- 单 pid 读失败不在此处记（由 Task 1 的 `markMembers` 统一降级到 Debug，避免每个 pid 一行）

- [ ] **Step 6: 加注释**

确认文件头注释写清了「为什么是 cwd 不是环境变量」（带实测依据）、「为什么不用 x/sys/unix」、「为什么带运行期自检」；`vipPathOffset` 的「实测值，非推算」已写；`attributes` 的「不抗 cd」边界已写；`underRoot` 的前缀陷阱已写。

- [ ] **Step 7: Commit**

```bash
git add internal/prochost/taskmark_darwin.go internal/prochost/taskmark_darwin_test.go
git commit -m "feat(prochost): darwin 按 cwd 归属，含偏移量运行期自检"
```

---

## Task 3: linux 实现（environ）

**Files:**
- Modify: `internal/prochost/taskmark_linux.go`（**整体替换 Task 1 落的桩**，含文件头注释）
- Test: `internal/prochost/taskmark_linux_test.go`

**Interfaces:**
- Consumes: `TaskCred`、`TaskMarkEnvKey`（Task 1）
- Produces: `func attributes(pid int, cred TaskCred) (bool, error)`（linux 版）

- [ ] **Step 1: 写失败测试**

`internal/prochost/taskmark_linux_test.go`：

```go
//go:build linux

package prochost

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestAttributesByEnviron 验真实进程：带标记的（含 setsid 出去的）命中，
// 不带标记的不命中。
//
// 对照组必须先命中，再断言其余——先证明读取此刻没失灵（B37 spec §12.5）。
func TestAttributesByEnviron(t *testing.T) {
	cred := TaskCred{TaskID: "task-abc"}

	start := func(marked, setsid bool) *exec.Cmd {
		c := exec.Command("/bin/sleep", "30")
		c.Env = []string{"PATH=/usr/bin:/bin"}
		if marked {
			c.Env = append(c.Env, TaskMarkEnvKey+"=task-abc")
		}
		if setsid {
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}
		if err := c.Start(); err != nil {
			t.Fatalf("起子进程失败: %v", err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
		return c
	}

	marked := start(true, false)
	markedSetsid := start(true, true)
	plain := start(false, false)
	time.Sleep(300 * time.Millisecond)

	if ok, err := attributes(marked.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("对照组未命中，本轮测量无效：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(markedSetsid.Process.Pid, cred); err != nil || !ok {
		t.Fatalf("setsid 出去的子进程应命中：ok=%v err=%v", ok, err)
	}
	if ok, err := attributes(plain.Process.Pid, cred); err != nil || ok {
		t.Fatalf("无标记的进程不应命中：ok=%v err=%v", ok, err)
	}
}

// TestAttributesRejectsDifferentTaskID 钉住并发隔离：另一个任务的标记不得命中。
func TestAttributesRejectsDifferentTaskID(t *testing.T) {
	c := exec.Command("/bin/sleep", "30")
	c.Env = []string{"PATH=/usr/bin:/bin", TaskMarkEnvKey + "=task-other"}
	if err := c.Start(); err != nil {
		t.Fatalf("起子进程失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
	time.Sleep(300 * time.Millisecond)

	ok, err := attributes(c.Process.Pid, TaskCred{TaskID: "task-abc"})
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if ok {
		t.Fatalf("另一个任务的标记不得命中")
	}
}

// TestAttributesEmptyTaskIDNeverMatches 钉住 TaskID 为空时一律不命中——
// 否则「没有该变量的进程」会被空串前缀匹配成命中。
func TestAttributesEmptyTaskIDNeverMatches(t *testing.T) {
	ok, err := attributes(os.Getpid(), TaskCred{})
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if ok {
		t.Fatalf("TaskID 为空时不得命中")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run（在 linux 上，或 `GOOS=linux go vet ./internal/prochost/` 先确认能编）：
`go test ./internal/prochost/ -run TestAttributes -count=1`
Expected: FAIL，`undefined: attributes`（linux 版）

> **执行机是 macOS，跑不了 linux 用例。** 本任务的验证到「`GOOS=linux GOARCH=amd64 go vet ./...` 干净」为止，**不得声称 linux 运行期已验证**。真机运行由审核者在 Linux 上补。

- [ ] **Step 3: 写实现**

`internal/prochost/taskmark_linux.go`：

```go
//go:build linux

// taskmark_linux.go —— linux 的任务标记实现：按注入的环境变量归属。
//
// 为什么是环境变量而不是 cwd：/proc/<pid>/environ 对同 uid 可读，macOS 那条
// 针对平台二进制的屏蔽在 linux 不存在（spec §4.3 非 root 实测）。环境变量比
// cwd 强在两处：进程 cd 走了也跟得住（构建脚本 cd 到别处再编译是常态），
// 并发下不依赖目录独占（两个任务指到同一个 --worktree 也不会串）。
// 因此 linux 上**所有任务形态**都能准确归属，不像 macOS 只限托管 worktree。
//
// 边界：只读 environ，不发信号、不判存活；不得 fork。
package prochost

import (
	"bytes"
	"fmt"
	"os"
)

// attributes 判定 pid 是否属于 cred 所描述的任务（linux：按 environ）。
//
// 返回：
//   - true: 该进程的 environ 里 TaskMarkEnvKey 的值等于 cred.TaskID
//   - 错误: 该 pid 的 environ 读不到（进程已退出，或不是本 uid 的进程）
//
// 注意：
//   - cred.TaskID 为空即一律不命中——否则「根本没有该变量的进程」会被
//     空值匹配成命中，把整台机器的进程都归给任务
//   - environ 反映的是进程**启动时**的环境块，正是我们要的：它由 execve 传递，
//     不受 setsid / reparent 影响，也不随进程后续行为改变
//   - 本判据依赖执行者不清洗环境变量。opencode 实测透传；其余三家未逐一验证
//     （spec §12 已记账）
func attributes(pid int, cred TaskCred) (bool, error) {
	if cred.TaskID == "" {
		return false, nil
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return false, fmt.Errorf("读 /proc/%d/environ: %w", pid, err)
	}
	want := []byte(TaskMarkEnvKey + "=" + cred.TaskID)
	for _, kv := range bytes.Split(raw, []byte{0}) {
		if bytes.Equal(kv, want) {
			return true, nil
		}
	}
	return false, nil
}
```

- [ ] **Step 4: 跑门确认能编**

Run: `GOOS=linux GOARCH=amd64 go build ./... && GOOS=linux GOARCH=amd64 go vet ./...`
Expected: 两条都无输出

- [ ] **Step 5: 加关键节点日志**

本文件**刻意不打日志**，理由写进注释：`attributes` 会被 `markMembers` 对每个 pid 调用一次（全表数百次、且 `handoff status` 高频触发），在这里记任何一行都会淹掉 agentd.log。失败由 `markMembers` 统一降级到 Debug 记一次，汇总由 `Footprint` 在边界上记一次——与 `rosterMembers`「成功路径刻意不打日志」是同一条纪律。

在 `attributes` 的注释里补上这句「为什么本文件不打日志」。

- [ ] **Step 6: 加注释**

确认文件头写清了「为什么是环境变量不是 cwd」（带 macOS 对比与两条强于 cwd 的理由）、「linux 上所有任务形态都适用」；`attributes` 的 TaskID 空值陷阱、environ 的不可变语义、以及「依赖执行者透传、其余三家未验」已写。

- [ ] **Step 7: Commit**

```bash
git add internal/prochost/taskmark_linux.go internal/prochost/taskmark_linux_test.go
git commit -m "feat(prochost): linux 按注入环境变量归属"
```

---

## Task 4: 凭据落地（Spec/Handle 字段与 Start 接线）

**Files:**
- Modify: `internal/prochost/prochost.go`（`Spec` 加 2 字段、`Handle` 加 2 字段、`Start` 加 1 行并回填）
- Modify: `internal/executor/claudecode/proc.go:178`
- Modify: `internal/executor/codex/proc.go:218`
- Modify: `internal/executor/grok/proc.go:264`
- Modify: `internal/executor/opencode/proc.go:217`
- Test: `internal/prochost/taskmark_test.go`（追加）

**Interfaces:**
- Consumes: `applyTaskMark(*Spec)`、`resolveMarkRoot(dir string, managed bool) string`、`TaskMarkEnvKey`（Task 1）
- Produces:
  - `Spec.TaskID string`、`Spec.MarkRoot string`
  - `Handle.TaskID string`、`Handle.MarkRoot string`
  - `func (h Handle) cred() TaskCred`

- [ ] **Step 1: 写失败测试**

追加到 `internal/prochost/taskmark_test.go`：

```go
// TestApplyTaskMarkInjectsEnv 钉住注入发生在 Start 这一层，且值就是 TaskID。
func TestApplyTaskMarkInjectsEnv(t *testing.T) {
	spec := &Spec{TaskID: "task-xyz", Env: []string{"PATH=/bin"}}
	applyTaskMark(spec)

	var found string
	for _, kv := range spec.Env {
		if strings.HasPrefix(kv, TaskMarkEnvKey+"=") {
			found = strings.TrimPrefix(kv, TaskMarkEnvKey+"=")
		}
	}
	if found != "task-xyz" {
		t.Fatalf("未注入标记或值不对：Env=%v", spec.Env)
	}
}

// TestApplyTaskMarkNoopWithoutTaskID 钉住无 id 时什么都不注入——
// 注入一个空值会让 linux 判据把没有该变量的进程都算成命中。
func TestApplyTaskMarkNoopWithoutTaskID(t *testing.T) {
	spec := &Spec{Env: []string{"PATH=/bin"}}
	applyTaskMark(spec)
	if len(spec.Env) != 1 {
		t.Fatalf("不该注入任何东西：Env=%v", spec.Env)
	}
}

// TestResolveMarkRootOnlyForManaged 钉住「仅托管 worktree 可杀」的数据侧闸门。
func TestResolveMarkRootOnlyForManaged(t *testing.T) {
	dir := t.TempDir()
	if got := resolveMarkRoot(dir, false); got != "" {
		t.Fatalf("非托管形态必须返回空串，实得 %q", got)
	}
	got := resolveMarkRoot(dir, true)
	want, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Fatalf("托管形态应返回解析后的路径：实得 %q 期望 %q", got, want)
	}
}

// TestHandleCredProjection 钉住 Handle → TaskCred 的投影不丢字段。
func TestHandleCredProjection(t *testing.T) {
	h := Handle{TaskID: "t1", MarkRoot: "/tmp/wt"}
	c := h.cred()
	if c.TaskID != "t1" || c.MarkRoot != "/tmp/wt" {
		t.Fatalf("投影丢字段：%+v", c)
	}
	if (Handle{}).cred().empty() != true {
		t.Fatalf("空 Handle 的凭据应为 empty（升级前的 proc.json 就是这个形态）")
	}
}
```

测试文件需要 `import ("path/filepath"; "strings")`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestApplyTaskMark|TestResolveMarkRoot|TestHandleCred' -count=1`
Expected: FAIL，`unknown field TaskID in struct literal of type Spec`

- [ ] **Step 3: 补 `Handle` 字段（`Spec` 的两个字段 Task 1 已加，此处只核对）**

`Spec` 的 `TaskID` / `MarkRoot` 已在 Task 1 Step 3 落地，**核对注释是否为下面这段原文，缺什么补什么，不要重复添加字段**：

```go
	// TaskID 是本任务的 UUID，Start 据它注入 HANDOFF_TASK_ID 环境变量，
	// 并回填进 Handle 供归属判定使用。
	//
	// omitempty + 零值语义：为空则不注入、不参与归属判定。旧版 shim 读到新版
	// spec.json 会忽略该字段；新版 shim 读到旧 spec.json 得到空串则判据不参与——
	// 两个方向都不会出事，与 NprocLimit 同款滚动升级纪律。
	TaskID string `json:"task_id,omitempty"`

	// MarkRoot 是 cwd 归属判据的比对根（已解析符号链接的绝对路径）。
	//
	// **只在托管 worktree 形态下由调用方填写**：空串即本任务不启用 cwd 归属。
	// 把「仅托管 worktree 可杀」编码进数据而不是运行时再判一次，是为了让这条
	// 边界没有「某处忘了检查」的可能。
	MarkRoot string `json:"mark_root,omitempty"`
```

在 `Handle` 里，紧跟 `RosterPath` 之后加同名两字段（注释指向 `Spec` 的说明并强调零值降级）：

```go
	// TaskID / MarkRoot 是归属判定的凭据，由 Start 从 Spec 原样带过来。
	//
	// omitempty + 零值语义：升级前写下的 proc.json 没有这两个字段，读出空串即
	// 跳过标记判据、只走 pgid + roster——与 StartedAt / RosterPath 缺失时同一条
	// 纪律，老任务不会因为升级就被动手。
	TaskID   string `json:"task_id,omitempty"`
	MarkRoot string `json:"mark_root,omitempty"`
```

在 `Handle` 定义之后加投影方法：

```go
// cred 把 Handle 投影成一次归属判定所需的凭据。
//
// 为什么要单独一层而不是让判据直接吃 Handle：判据只需要这两个字段，
// 传整个 Handle 会让 taskmark.go 依赖 PID/LockPath 这些与它无关的东西，
// 单测也得构造无关字段。
func (h Handle) cred() TaskCred {
	return TaskCred{TaskID: h.TaskID, MarkRoot: h.MarkRoot}
}
```

- [ ] **Step 4: 在 `Start` 里接线**

`internal/prochost/prochost.go:250` 附近，在 `applyFencePolicy(&spec)` 下一行加：

```go
	applyTaskMark(&spec)
```

并在函数末尾的 `return Handle{...}` 里补两个字段：

```go
	return Handle{
		PID:        pid,
		LockPath:   spec.LockPath,
		StartedAt:  startedAt,
		RosterPath: roster,
		TaskID:     spec.TaskID,
		MarkRoot:   spec.MarkRoot,
	}, nil
```

- [ ] **Step 5: 四个 adapter 填凭据**

**注意 `prochost.Spec` 不是在 adapter 的 `Start` 里构造的**，而是在只吃基本类型的
helper 里（opencode/codex/grok 各自的 `serveSpec`、claudecode 的 `startProc`）。
所以填法是**在 helper 返回之后赋值**，不是给 helper 加参数：

1. **`Spec.TaskID` 多数情况不用改签名。** 例如 opencode 的
   `StartServe(ctx, repoPath, taskID, taskDir, configPath, env, log)` 里 `taskID`
   已经是参数，在 `spec := serveSpec(...)` 之后直接加一行：
   ```go
   	spec.TaskID = taskID
   ```
   其余三家逐个看入口函数：已有 `taskID`（或等价物）就直接用；确实没有，才给那个
   入口函数加一个 `taskID string` 参数。

2. **`Spec.MarkRoot` 只给入口函数加一个参数。** 入口函数指 adapter 的 `Start`
   直接调的那个：opencode/codex/grok 是各自的 `StartServe`，claudecode 是
   `startProc`（给 `StartProcReq` 加一个 `MarkRoot` 字段即可）。在同一个调用点：
   ```go
   	spec.MarkRoot = markRoot
   ```
   值在 adapter 的 `Start` 里算——那里 `req.Task` 在作用域内：
   ```go
   	prochost.ResolveMarkRoot(req.Task.Workdir(), req.Task.WorktreeManaged)
   ```

3. **明确不要做的：不要给 `serveSpec` 本身加参数。** 它是纯 spec 构造器，改它的
   签名会连带 churn 掉一批已有断言测试（如 opencode 的
   `TestServeSpecPutsPasswordInEnvNotArgv`），而收益为零——构造之后赋值效果完全一样。

4. **别漏 resume 路径**：`internal/executor/claudecode/resume.go:114` 也构造
   `StartProcReq`，同样要填这两个值，否则恢复出来的执行者没有归属凭据。

为此需要把 Task 1 的 `resolveMarkRoot` **导出**为 `ResolveMarkRoot`（adapter 在包外）。
同步改 Task 1 的实现与测试里的名字。

> `req.Task.Workdir()` 在 `WorkDir` 为空时回退 `RepoPath`（原地模式），而原地模式下
> `WorktreeManaged` 必为 false，`ResolveMarkRoot` 因此返回空串——正确，不必额外判空。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/prochost/ ./internal/executor/... -count=1`
Expected: PASS（含四个 adapter 既有的 spec 断言测试；若某个 adapter 的测试逐字段断言了 `prochost.Spec`，补上新字段的期望值）

- [ ] **Step 7: 加关键节点日志**

在 `Start` 里，`applyTaskMark(&spec)` 之后加一行 Debug，记录本任务启用了哪几条判据——排障时第一个要问的就是「这个任务当初有没有凭据」：

```go
	log().Debug("任务标记凭据已就位", "task_id", spec.TaskID,
		"mark_root", spec.MarkRoot, "env_injected", spec.TaskID != "")
```

- [ ] **Step 8: 加注释**

确认 `Spec`/`Handle` 新字段的零值语义与滚动升级纪律已写；`cred()` 的「为什么单独一层」已写；`ResolveMarkRoot` 导出后的文档注释仍完整（含符号链接解析的原因）。

- [ ] **Step 9: Commit**

```bash
git add internal/prochost/prochost.go internal/prochost/taskmark.go internal/prochost/taskmark_test.go internal/executor/claudecode/proc.go internal/executor/codex/proc.go internal/executor/grok/proc.go internal/executor/opencode/proc.go
git commit -m "feat(prochost): 任务标记凭据落地到 Spec/Handle 并在 Start 注入"
```

---

## Task 5: `classify` 并入第三条来源与分列日志

**Files:**
- Modify: `internal/prochost/footprint.go`（`Footprint` 第三段、日志分列）
- Test: `internal/prochost/footprint_test.go`（追加）

**Interfaces:**
- Consumes: `markMembers(TaskCred, []procEntry) ([]int, bool)`（Task 1）、`Handle.cred()`（Task 4）
- Produces: 无新导出符号；`Footprint` 行为变更（成员集变成三段并集）

- [ ] **Step 1: 写失败测试**

追加到 `internal/prochost/footprint_test.go`：

```go
// TestFootprintIncludesMarkOnlyMembers 钉住本条需求的核心价值：
// 标记判据要捞回 pgid 与 roster 都看不见的那批进程。
func TestFootprintIncludesMarkOnlyMembers(t *testing.T) {
	restore := stubEnum([]procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000}, // shim 自己
		{PID: 101, PGID: 100, StartedAt: 1100}, // 同组，pgid 能看见
		{PID: 200, PGID: 200, StartedAt: 1200}, // setsid 逃逸，只有标记看得见
	})
	defer restore()
	defer stubAlive(false)()

	oldAttr := attributesFn
	defer func() { attributesFn = oldAttr }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) {
		return pid == 200 || pid == 101, nil
	}

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	members, v, err := Footprint(h)
	if err != nil || v != VerdictOK {
		t.Fatalf("判定应通过：v=%v err=%v", v, err)
	}
	if !containsPID(members, 200) {
		t.Fatalf("标记独有的成员 200 未被捞回：members=%v", members)
	}
	if countPID(members, 101) != 1 {
		t.Fatalf("同时被 pgid 与标记命中的 101 必须去重，members=%v", members)
	}
}

// TestFootprintMarkRespectsStartedAtFloor 钉住时间下界对标记成员照样施加——
// 枚举与发信号之间的 pid 复用窗口，这道护栏不能因为换判据就撤（B47）。
func TestFootprintMarkRespectsStartedAtFloor(t *testing.T) {
	restore := stubEnum([]procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000},
		{PID: 300, PGID: 300, StartedAt: 500}, // 比 shim 还早
	})
	defer restore()
	defer stubAlive(false)()

	oldAttr := attributesFn
	defer func() { attributesFn = oldAttr }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return pid == 300, nil }

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	members, _, _ := Footprint(h)
	if containsPID(members, 300) {
		t.Fatalf("比 shim 更早启动的进程不得因标记命中而入选：members=%v", members)
	}
}

// TestFootprintDegradesWhenMarkUnsupported 钉住平台不支持时不影响既有两段。
func TestFootprintDegradesWhenMarkUnsupported(t *testing.T) {
	restore := stubEnum([]procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000},
		{PID: 101, PGID: 100, StartedAt: 1100},
	})
	defer restore()
	defer stubAlive(false)()

	oldAttr := attributesFn
	defer func() { attributesFn = oldAttr }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	members, v, err := Footprint(h)
	if err != nil || v != VerdictOK {
		t.Fatalf("平台不支持标记不该让判定失败：v=%v err=%v", v, err)
	}
	if !containsPID(members, 101) {
		t.Fatalf("pgid 那段必须照常工作：members=%v", members)
	}
}
```

若 `footprint_test.go` 里尚无 `stubEnum` / `stubAlive` / `containsPID` / `countPID`，按该文件既有的辅助函数命名风格补上；**先读该文件**，已有等价物就直接复用，不要重复造。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run TestFootprint -count=1`
Expected: FAIL（`TestFootprintIncludesMarkOnlyMembers` 报 200 未被捞回）

- [ ] **Step 3: 实现第三段**

改 `internal/prochost/footprint.go` 的 `Footprint`，把原来的第二段扩成两段，并按来源统计：

```go
func Footprint(h Handle) (members []int, v Verdict, err error) {
	procs, err := enumProcsFn()
	if err != nil {
		log().Error("足迹枚举失败", "pid", h.PID, "cause", err)
		return nil, VerdictNoCredential, err
	}
	members, v = classify(h, procs, aliveFn(h))
	byPgid := len(members)
	var byRoster, byMark int
	markSupported := false
	if v == VerdictOK {
		// 第二、三段：判定放弃时不并入——那时 members 必须为空是 classify
		// 的契约，不能被这里破坏
		seen := make(map[int]bool, len(members))
		for _, p := range members {
			seen[p] = true
		}
		for _, p := range rosterMembers(h, procs) {
			byRoster++
			if !seen[p] {
				members = append(members, p)
				seen[p] = true
			}
		}
		var marked []int
		marked, markSupported = markMembers(h.cred(), procs)
		for _, p := range marked {
			// 时间下界对标记成员照样施加：标记读的是活状态，枚举与发信号
			// 之间仍有 pid 复用窗口（B47 的教训不因换判据而失效）
			if startedAtOf(procs, p) < h.StartedAt {
				continue
			}
			byMark++
			if !seen[p] {
				members = append(members, p)
				seen[p] = true
			}
		}
	}
	log().Debug("足迹判定完成", "pid", h.PID, "verdict", string(v),
		"members", len(members), "by_pgid", byPgid, "by_roster", byRoster,
		"by_mark", byMark, "mark_only", len(members)-byPgid-byRoster,
		"mark_supported", markSupported)
	return members, v, nil
}

// startedAtOf 在快照里查 pid 的启动时刻；查不到返回 0（会被时间下界排除）。
//
// 为什么返回 0 而不是报错：pid 来自同一份快照，查不到只可能是调用方传错，
// 返回 0 会让它被下界规则挡掉——宁可漏一个也不放一个身份不明的进 members。
func startedAtOf(procs []procEntry, pid int) int64 {
	for _, p := range procs {
		if p.PID == pid {
			return p.StartedAt
		}
	}
	return 0
}
```

> `mark_only` 用减法算是**近似**（三段有重叠时会偏小）。若要精确，在并入标记
> 段时单独计一个 `markOnly++`（仅当 `!seen[p]` 时递增）并直接打它。**实现时用
> 精确版**：这个数是本条需求价值的直接度量，不该是个约等于。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/prochost/ -count=1`
Expected: PASS（含既有全部用例——第三段不得改变既有行为）

- [ ] **Step 5: 加关键节点日志**

Step 3 已把分列日志写进 `Footprint`。补两点：
- `mark_supported=false` 时，**不要**在这里 Warn（Footprint 高频调用会刷屏）；平台能力播报归 Task 7 在启动期做一次
- 确认 `mark_only` 用的是精确计数而非减法

- [ ] **Step 6: 加注释**

更新 `Footprint` 的文档注释：把「**members 是两段并集**」改成三段，写明第三段是标记判据、它覆盖的是「壳活得太短、一次都没被采样到」那批，并写明 `Sweep` 必须同步（报的数与杀的范围必须一致，否则 `handoff footprint` 就是句骗人的话——B70）。

- [ ] **Step 7: Commit**

```bash
git add internal/prochost/footprint.go internal/prochost/footprint_test.go
git commit -m "feat(prochost): 足迹并入任务标记这条来源并按来源分列日志"
```

---

## Task 6: `Sweep` 的标记段回收

**Files:**
- Modify: `internal/prochost/footprint.go`（新增 `markKill`，接进 `Sweep`）
- Test: `internal/prochost/footprint_test.go`（追加）

**Interfaces:**
- Consumes: `markMembers`、`Handle.cred()`、`killProcFn`、`startedAtOf`（Task 5）
- Produces: `func markKill(h Handle, procs []procEntry) (killed int)`

- [ ] **Step 1: 写失败测试**

```go
// TestSweepKillsMarkOnlyMembers 钉住 Sweep 与 Footprint 报的是同一批：
// 标记独有的成员必须真的被杀，否则 handoff footprint 数出来的就是句空话（B70）。
func TestSweepKillsMarkOnlyMembers(t *testing.T) {
	defer stubEnum([]procEntry{
		{PID: 100, PGID: 100, StartedAt: 1000},
		{PID: 200, PGID: 200, StartedAt: 1200}, // 标记独有
	})()
	defer stubAlive(false)()
	defer stubKillGroup(nil)()

	var killedPIDs []int
	defer stubKillProc(func(pid int) error { killedPIDs = append(killedPIDs, pid); return nil })()

	oldAttr := attributesFn
	defer func() { attributesFn = oldAttr }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return pid == 200, nil }

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	if _, _, err := Sweep(h); err != nil {
		t.Fatalf("清扫不应报错：%v", err)
	}
	if !containsPID(killedPIDs, 200) {
		t.Fatalf("标记独有的成员 200 未被回收：killed=%v", killedPIDs)
	}
}

// TestMarkKillReverifiesBeforeSignal 钉住发信号前必须复验标记。
//
// 枚举与发信号之间进程可能已退出且 pid 被复用；标记是活读的，
// 复验一次的成本是一个 syscall，而误杀的代价是打掉用户的 shell（B47）。
func TestMarkKillReverifiesBeforeSignal(t *testing.T) {
	procs := []procEntry{{PID: 200, PGID: 200, StartedAt: 1200}}
	var killedPIDs []int
	defer stubKillProc(func(pid int) error { killedPIDs = append(killedPIDs, pid); return nil })()

	oldAttr := attributesFn
	defer func() { attributesFn = oldAttr }()
	calls := 0
	attributesFn = func(pid int, cred TaskCred) (bool, error) {
		calls++
		// 第一次（筛选）命中，第二次（杀前复验）不再命中 ⇒ pid 已易主
		return calls == 1, nil
	}

	h := Handle{PID: 100, StartedAt: 1000, TaskID: "t1"}
	killed := markKill(h, procs)
	if killed != 0 || len(killedPIDs) != 0 {
		t.Fatalf("复验不通过时不得发信号：killed=%d pids=%v", killed, killedPIDs)
	}
	if calls != 2 {
		t.Fatalf("应恰好复验一次：attributes 调用 %d 次", calls)
	}
}

// TestMarkKillSkipsWhenUnsupported 钉住平台不支持时安静返回 0，不影响前两段。
func TestMarkKillSkipsWhenUnsupported(t *testing.T) {
	oldAttr := attributesFn
	defer func() { attributesFn = oldAttr }()
	attributesFn = func(pid int, cred TaskCred) (bool, error) { return false, errNotSupported }

	var killedPIDs []int
	defer stubKillProc(func(pid int) error { killedPIDs = append(killedPIDs, pid); return nil })()

	killed := markKill(Handle{PID: 100, StartedAt: 1000, TaskID: "t1"},
		[]procEntry{{PID: 200, StartedAt: 1200}})
	if killed != 0 || len(killedPIDs) != 0 {
		t.Fatalf("不支持时不得发信号：killed=%d pids=%v", killed, killedPIDs)
	}
}
```

若 `stubKillGroup` / `stubKillProc` 在 `footprint_test.go` 里尚不存在，按该文件既有风格补（`killGroupFn` / `killProcFn` 都是包级 var 测试缝）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestSweepKillsMark|TestMarkKill' -count=1`
Expected: FAIL，`undefined: markKill`

- [ ] **Step 3: 写 `markKill`**

```go
// markKill 执行第三段清扫：按任务标记逐个回收 pgid 与名册都看不见的残留。
//
// 参数：
//   - h: 任务句柄（用 h.cred() 取归属凭据、h.StartedAt 做时间下界）
//   - procs: 一次进程快照（与前两段共用，避免重复枚举）
//
// 返回：killed 为**实际发出信号**的成员数
//
// 判据（比名册那段更强，因为标记是活读的）：
//  1. 标记命中，且
//  2. 启动时刻不早于 shim（比 shim 还早的不可能是它的后代），且
//  3. **发信号前立刻再读一次标记**——枚举与发信号之间进程可能已退出且 pid
//     被复用，复验一次只花一个 syscall，而误杀的代价是打掉用户的登录 shell
//     或 agentd 自己（B47 误杀 114 次的教训）
//
// 注意：
//   - 平台不支持标记是**正常形态**（Windows 走 Job Object，见 B37），安静返回 0
//   - 逐条发信号而不是按组：标记命中的进程各自 setsid 成组，按组会带走未经
//     校验的兄弟进程
//   - macOS 上只有托管 worktree 形态才会有 MarkRoot，非托管任务在这里天然返回 0
func markKill(h Handle, procs []procEntry) (killed int) {
	cred := h.cred()
	candidates, supported := markMembers(cred, procs)
	if !supported {
		log().Debug("本平台无任务标记能力或本任务无凭据，跳过标记回收", "pid", h.PID)
		return 0
	}
	if len(candidates) == 0 {
		return 0
	}
	var skippedOld, skippedRecheck int
	for _, pid := range candidates {
		if startedAtOf(procs, pid) < h.StartedAt {
			skippedOld++
			continue
		}
		ok, err := attributesFn(pid, cred)
		if err != nil {
			// 多为进程已退出：常态，不发信号即可
			log().Debug("杀前复验标记失败，跳过", "pid", pid, "cause", err)
			skippedRecheck++
			continue
		}
		if !ok {
			// 这条必须是 Warn：它是「我们差点杀错」的唯一现场记录，
			// 出现频率高本身就是个值得追的信号（同 rosterKill 的易主日志）
			log().Warn("杀前复验标记不再命中，拒绝发信号", "pid", pid, "task_id", cred.TaskID)
			skippedRecheck++
			continue
		}
		if kerr := killProcFn(pid); kerr != nil {
			log().Error("按标记回收进程失败", "pid", pid, "cause", kerr)
			continue
		}
		killed++
	}
	log().Info("标记回收完成", "pid", h.PID, "candidates", len(candidates),
		"killed", killed, "skipped_older_than_shim", skippedOld,
		"skipped_recheck", skippedRecheck)
	return killed
}
```

- [ ] **Step 4: 接进 `Sweep`**

`Sweep` 里现有三处调用 `rosterKill(h, procs)` / `rosterKill(h, rest)`。**每一处后面都要加上 `markKill`，并把两者之和计入返回值**：

1. `classify` 放弃分支（`return rosterKill(h, procs), v, nil`）→
   ```go
   		return rosterKill(h, procs) + markKill(h, procs), v, nil
   ```
2. 组内无残留分支（`return rosterKill(h, procs), VerdictOK, nil`）→
   ```go
   		return rosterKill(h, procs) + markKill(h, procs), VerdictOK, nil
   ```
3. 复核成功分支里的 `n := rosterKill(h, rest)` →
   ```go
   			n := rosterKill(h, rest) + markKill(h, rest)
   ```
   并把该分支的日志加上标记段的数（把 `"roster_killed", n` 改成分别记两个数：
   在上一行拆成 `rk := rosterKill(h, rest)` 与 `mk := markKill(h, rest)`，
   日志写 `"roster_killed", rk, "mark_killed", mk`，返回 `len(members)+rk+mk`）。

**三处一个都不能漏**——漏一处就意味着「某条路径上报了却没杀」，正是 B70 要防的。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/prochost/ -count=1`
Expected: PASS（含既有全部用例）

- [ ] **Step 6: 加关键节点日志**

Step 3/4 已内联。核对三条：
- 每次标记回收结束一条 Info，含 `candidates` / `killed` / 两个 skip 计数
- 「复验不再命中」必须是 **Warn**（差点杀错的现场）
- 平台不支持是 Debug（正常形态，不该有噪音）

- [ ] **Step 7: 加注释**

更新 `Sweep` 的文档注释：把「**两段判据**」改成三段，写明第三段是标记回收、它比名册段强在「活读 + 杀前复验」，以及 macOS 非托管任务在这一段天然是 no-op。

- [ ] **Step 8: Commit**

```bash
git add internal/prochost/footprint.go internal/prochost/footprint_test.go
git commit -m "feat(prochost): Sweep 增加按任务标记的第三段回收"
```

---

## Task 7: 启动期能力播报与文档

**Files:**
- Modify: `cmd/agentd.go`（启动期一条能力播报）
- Modify: `README.md`（每平台归属强度边界一节）
- Test: `cmd/agentd_test.go`（追加）

**Interfaces:**
- Consumes: `prochost.MarkCapability() (supported bool, reason string)`（本任务新增导出）
- Produces: `func MarkCapability() (bool, string)`

- [ ] **Step 1: 写失败测试**

追加到 `cmd/agentd_test.go`：

```go
// TestMarkCapabilityReported 钉住启动期必须播报归属能力——
// 静默缺席正是 B37 反复在防的东西：能力没了而日志一个字不说，
// 排障时会一路怀疑到别处去。
func TestMarkCapabilityReported(t *testing.T) {
	supported, reason := prochost.MarkCapability()
	if !supported && reason == "" {
		t.Fatalf("不支持时必须给出理由，否则日志等于没说")
	}
	if supported && reason != "" {
		t.Fatalf("支持时不该带理由：%q", reason)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestMarkCapability -count=1`
Expected: FAIL，`undefined: prochost.MarkCapability`

- [ ] **Step 3: 写 `MarkCapability`**

加到 `internal/prochost/taskmark.go`：

```go
// MarkCapability 报告本平台是否具备任务标记归属能力。
//
// 返回：
//   - supported: 是否可用
//   - reason: 不可用的原因（供启动期日志直接呈现）；可用时为空串
//
// 为什么要单独一个导出函数而不是让 agentd 自己试：能力判定的依据在包内
// （平台实现 + darwin 的运行期自检），暴露一个明确的问句比让调用方
// 拿一个假 pid 去试要诚实得多。
func MarkCapability() (supported bool, reason string) {
	// 用本进程当探针：它一定存在，且凭据给足以便走到平台实现里。
	_, err := attributesFn(os.Getpid(), TaskCred{TaskID: "capability-probe", MarkRoot: os.TempDir()})
	if err != nil && isNotSupported(err) {
		return false, "本平台不支持任务标记归属"
	}
	return true, ""
}
```

> darwin 上 `cwdReadable()` 自检不过时 `attributes` 返回 `errNotSupported`，
> 于是这里如实报不支持——自检与播报是同一条链，不会出现「说支持其实不支持」。

- [ ] **Step 4: 在 agentd 启动期播报**

在 `cmd/agentd.go` 已有的平台能力播报附近（B37 那批 Warn 旁边）加：

```go
	if supported, reason := prochost.MarkCapability(); supported {
		logger.Info("任务标记归属可用，进程归属不依赖采样时机")
	} else {
		logger.Warn("任务标记归属不可用，进程归属退回 pgid + 名册采样",
			"reason", reason,
			"note", "Windows 上这是预期形态：回收由 Job Object 进程容器承担")
	}
```

**只在启动期打一次，绝不进任何循环** —— B37 验收时 roster 采样每秒一条 WARN、单任务每天约 8.6 万行，把围栏与退出哨兵这些真正有用的行全淹掉，那条教训在这里复用。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ ./internal/prochost/ -count=1`
Expected: PASS

- [ ] **Step 6: 加关键节点日志**

即 Step 4 本身（支持与不支持各一条，成功路径也有——否则无从区分「在用」与「早就降级了」）。确认没有把它放进循环。

- [ ] **Step 7: 写文档**

在 `README.md` 里进程回收相关章节补一小节「每平台的归属强度边界」，如实写：

```markdown
### 进程归属的平台差异

handoff 判断「这个进程属于哪个任务」有三条来源：进程组（pgid）、后代名册
（采样得来）、任务标记。前两条对采样时机敏感——工具壳只活一两秒时会漏；
任务标记不依赖时机，但各平台强度不同：

| 平台 | 标记判据 | 边界 |
|---|---|---|
| Linux | 注入的 `HANDOFF_TASK_ID` 环境变量 | 全部任务形态可用；依赖执行者透传环境变量 |
| macOS | 进程的工作目录是否在任务 worktree 内 | **仅 `--new-worktree` 的托管任务**启用；进程 `cd` 出任务目录后脱钩 |
| Windows | 不适用 | 回收由 Job Object 进程容器承担，内核连坐，不需要事后判定 |

macOS 上不带 `--new-worktree` 的任务不启用该判据：那时任务跑在共享主仓里，
用户自己的编辑器与 shell 也在那个目录下，按工作目录归属会把它们一起清掉。
```

- [ ] **Step 8: Commit**

```bash
git add cmd/agentd.go cmd/agentd_test.go internal/prochost/taskmark.go README.md
git commit -m "feat(agentd): 启动期播报任务标记能力，README 记平台强度边界"
```

---

## Task 8: macOS 真机验收

**Files:**
- Create: `docs/superpowers/notes/2026-08-18-b105-acceptance.md`（验收记录）

**Interfaces:**
- Consumes: 前七个 task 的全部产出
- Produces: 验收记录文件

> **本任务不写产品代码。** 它的产出是一份如实的验收记录。
> 执行机是 macOS，因此**只做 macOS 半边**；Linux 真机验收由审核者执行，
> 本记录里如实标注「Linux 未验」。

- [ ] **Step 1: 跑完工六门**

```bash
go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go') && GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
```
Expected: 全部通过；`gofmt -l` 无输出。**任一不过就停下修，不要往下走。**

另外补一条 linux 交叉编译门（linux 实现只能这样验）：
```bash
GOOS=linux GOARCH=amd64 go build ./... && GOOS=linux GOARCH=amd64 go vet ./...
```

- [ ] **Step 2: 起一个本机 agentd（独立 DataDir，别碰你自己的 ~/.handoff）**

```bash
HANDOFF_DATA_DIR=/tmp/b105-accept ./handoff agentd --listen 127.0.0.1:17801
```
在 `agentd.log` 里确认启动期播报出现且**只出现一次**：
`任务标记归属可用，进程归属不依赖采样时机`

- [ ] **Step 3: 派一个会泄漏后台进程的任务**

用 `--new-worktree`（托管形态，macOS 上唯一启用 cwd 判据的形态），plan 内容只要求执行者跑一条 shell：

```
Run exactly this shell command and nothing else: `nohup sleep 300 >/dev/null 2>&1 & echo started`
```

- [ ] **Step 4: 先证明盲区真实存在**

在任务仍 `running`、且执行者已经跑完那条命令之后，**先**确认新捞回的成员确实是 pgid 与名册看不见的：在 `agentd.log` 里找 `足迹判定完成` 那行，记录 `by_pgid` / `by_roster` / `by_mark` / `mark_only` 四个数。

**判据：`mark_only ≥ 1`。** 若为 0，说明这次没造出盲区（`sleep` 恰好被名册采到了），**重跑 Step 3**，不要把 0 当成通过——那样验的是空气。

- [ ] **Step 5: 确认回收**

`handoff done <task>` 之后：
```bash
ps -ax | grep -c "[s]leep 300"
```
Expected: `0`。若非 0，记下 `agentd.log` 里 `标记回收完成` 那行的 `candidates` / `killed` / 两个 skip 计数原文，**如实记为未通过**，不要替它归因。

- [ ] **Step 6: 写验收记录**

`docs/superpowers/notes/2026-08-18-b105-acceptance.md`，逐条写：六门的实际输出、Step 2 的播报原文、Step 4 的四个数、Step 5 的进程数。

**没有亲自跑到结果的命令，不许写它的结论。** 跑了但失败，贴原始报错原文，不要替它归因；不确定就写「未验证」。Linux 半边一律写「未验证（执行机为 macOS）」。

- [ ] **Step 7: Commit**

```bash
git add docs/superpowers/notes/2026-08-18-b105-acceptance.md
git commit -m "docs(notes): B105 macOS 真机验收记录"
```

---

## 自审

**1. Spec 覆盖**

| Spec 章节 | 落在哪个 Task |
|---|---|
| §6 平台矩阵 / `TaskCred` / `attributes` 契约 | Task 1 |
| §6.1 darwin deprecated syscall 风险与缓解 | Task 2（运行期自检把风险变成自愈降级） |
| §6 Linux environ 判据 | Task 3 |
| §6 Windows `errNotSupported` | Task 1（`taskmark_other.go`） |
| §7.1 `Handle` 新字段与零值降级 | Task 4 |
| §7.2 三段并集 + `StartedAt` 下界 | Task 5 |
| §7.3 注入点在 `prochost.Start` | Task 4 |
| §7.4 任务套任务不做 | 无需 Task（本轮明确不做，已记入 spec §12） |
| §8 失败语义四档 | 平台不支持→Task 7 播报；单 pid 失败→Task 1；`Handle` 缺字段→Task 4；整体失败→Task 5 |
| §9 分列日志与 `mark_only` | Task 5 |
| §10 单元测试四条 + 平台测试 | Task 1（1/3/4 条）、Task 4（第 3 条 `MarkRoot` 空）、Task 2、Task 3 |
| §11 真机验收剧本 | Task 8（macOS 半边；Linux 归审核者） |
| §12 已知边界写进文档 | Task 7（README 平台差异表） |
| §13 第二批 | 不实现 |

无遗漏。

**2. 占位符扫描**：无 TBD / TODO / 「类似 Task N」/ 「加上适当的错误处理」。每个代码步骤都带可直接落地的代码块。

**3. 类型一致性**

- `TaskCred{TaskID, MarkRoot}` —— Task 1 定义，Task 2/3/4/5/6 使用，字段名一致
- `attributes(pid int, cred TaskCred) (bool, error)` —— Task 1 声明契约，Task 2/3 实现，签名一致
- `markMembers(cred TaskCred, procs []procEntry) ([]int, bool)` —— Task 1 定义，Task 5/6 使用
- `Handle.cred() TaskCred` —— Task 4 定义，Task 5/6 使用
- `startedAtOf(procs []procEntry, pid int) int64` —— Task 5 定义，Task 6 使用
- `ResolveMarkRoot`（导出）—— Task 1 定义为 `resolveMarkRoot`，**Task 4 Step 5 要求改名导出并同步改测试**，已在该步显式写明
- `MarkCapability() (bool, string)` —— Task 7 定义并使用
- `TaskMarkEnvKey` —— Task 1 定义，Task 3/4 使用

一处需要执行者注意的顺序依赖：Task 4 会把 Task 1 的 `resolveMarkRoot` 改名为
`ResolveMarkRoot`，Task 1 的测试要同步改。已在 Task 4 Step 5 写明。
