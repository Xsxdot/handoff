# handoff service start / stop / restart 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 `handoff service` 加 `start` / `stop` / `restart` 三个子命令，让「改完配置重启本机 agentd」和「暂时停掉 agentd」有可发现、有反馈的入口。

**Architecture:** 在 `internal/service.Manager` 上加 `Stop()` / `Restart()`，在 `Status` 上加 `Disabled`；三个平台（launchd / systemd / schtasks）各自实现，一律「先改状态、再复核真的到位」；`cmd/service.go` 挂三个子命令；桌面薄壳的 `EnsureRunning` 学会认 `Disabled` 从而不再复活被显式停掉的 agentd。

**Tech Stack:** Go 1.x、cobra、`log/slog`、标准库 `os/exec`。无新依赖。

**Spec:** [docs/superpowers/specs/2026-08-20-agentd-service-lifecycle-design.md](../specs/2026-08-20-agentd-service-lifecycle-design.md)

## Global Constraints

- **语言**：所有注释、日志文案、面向用户的输出一律中文，与本仓库既有代码一致。
- **日志**：一律 `m.log` / `slog.Default()`（`log/slog`）。**禁止** `fmt.Printf` 作为日志手段；`fmt.Fprintf(out, ...)` 只用于面向用户的命令输出，那不是日志。
- **注释**：新增导出方法必须有 doc 注释（参数、返回、注意事项）；每一处非显然的分支与顺序约束必须写「为什么」，不写「做了什么」。
- **不新增依赖**：不引入 XML 解析库、不引入第三方进程管理库。
- **不碰真系统**：所有单测经既有的 `run` / `writeFile` / `remove` / `stat` / `sleep` 缝注入，测试跑完机器上不得多出任何服务、不得真的调用 `launchctl` / `systemctl` / `schtasks`。
- **不代为安装**：`Start` / `Stop` / `Restart` 在单元没装时一律返回错误，绝不回落去 `Install`。
- **不读任务状态**：三个命令都不查 agentd 的任务列表、不依赖 agentd HTTP 可达（spec §3.4）。
- **每个 task 结束时必须**：`gofmt -l` 输出为空、`go build ./...` 通过、`go test ./...` 全绿，然后再 commit。

---

## 文件结构

| 文件 | 动作 | 职责 |
|------|------|------|
| `internal/service/service.go` | 修改 | `Manager` 加 `Stop()` / `Restart()`；`Status` 加 `Disabled` 字段 |
| `internal/service/launchd.go` | 修改 | launchd 的 Start 重写 + Stop/Restart 新增；Status 改判据（Installed 看 plist、Disabled 看 print-disabled）；新增 `stat` / `sleep` 两个缝 |
| `internal/service/systemd.go` | 修改 | systemd 的 Start 重写 + Stop/Restart 新增；Status 补 Disabled；权限提示抽 `sudoHint` |
| `internal/service/windows.go` | 修改 | schtasks 的 Start 重写 + Stop/Restart 新增；Status 补 Disabled；新增 `waitStopped` |
| `internal/service/launchd_test.go` | 修改 | 新增行为的命令序列、复核失败、未装硬拒 |
| `internal/service/systemd_test.go` | 修改 | 同上 |
| `internal/service/windows_test.go` | 修改 | 同上 |
| `cmd/service.go` | 修改 | 三个子命令；`status` 输出加两档 |
| `cmd/service_test.go` | 修改 | `fakeManager` 补两个方法；三个子命令的输出与退出码 |
| `desktop/internal/shell/lifecycle.go` | 修改 | `EnsureRunning` 认 `Disabled` 后不自愈 |
| `desktop/internal/shell/lifecycle_test.go` | 修改 | `fakeManager` 补两个方法；disabled 不自愈 |
| `desktop/main.go` | 修改 | 订正文件头那条已不成立的注释 |
| `README.md` / `README.zh-CN.md` | 修改 | 命令表与「怎么停 agentd」两处 |
| `CHANGELOG.md` | 修改 | Unreleased 小节 |

---

### Task 1: 扩接口与状态字段

把 `Stop` / `Restart` 加进 `Manager`，把 `Disabled` 加进 `Status`，三个平台先落桩（显式报「未实现」而不是静默成功），两处 `fakeManager` 补齐方法。**这一 task 结束时全仓编译通过、既有测试全绿**，后续三个平台 task 各自把桩换成真实现。

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/launchd.go`（文件末尾加桩）
- Modify: `internal/service/systemd.go`（文件末尾加桩）
- Modify: `internal/service/windows.go`（文件末尾加桩）
- Modify: `cmd/service_test.go:18-37`（`fakeManager`）
- Modify: `desktop/internal/shell/lifecycle_test.go:12-33`（`fakeManager`）
- Test: `internal/service/service_test.go`（新建）

**Interfaces:**
- Consumes: 无（第一个 task）
- Produces:
  - `service.Manager` 新增 `Stop() error`、`Restart() error`
  - `service.Status` 新增字段 `Disabled bool`
  - 包级哨兵错误 `var ErrNotInstalled = errors.New("服务单元未安装")`，供三平台在「没装」时包装返回，供上层用 `errors.Is` 判别

- [ ] **Step 1: 写失败的测试**

新建 `internal/service/service_test.go`：

```go
// service 包的跨平台契约测试：接口完整性与哨兵错误。
//
// 这里只钉住「三个平台都实现了完整接口」和「没装时的错误可判别」，
// 具体命令序列在各平台自己的测试里。
package service

import (
	"errors"
	"strings"
	"testing"
)

// 三个平台都必须实现完整的 Manager。少一个方法在这里就编译不过，
// 而不是等到某个平台上运行时才发现。
func TestAllManagersImplementInterface(t *testing.T) {
	var _ Manager = (*launchdManager)(nil)
	var _ Manager = (*systemdManager)(nil)
	var _ Manager = (*windowsManager)(nil)
}

// ErrNotInstalled 必须可被 errors.Is 判别：上层要靠它区分「没装」与
// 「装了但操作失败」，两者的处置完全不同（前者去 install，后者去查日志）。
func TestErrNotInstalledIsIdentifiable(t *testing.T) {
	wrapped := errNotInstalled("/some/unit")
	if !errors.Is(wrapped, ErrNotInstalled) {
		t.Fatalf("errNotInstalled 的返回必须能被 errors.Is 认出 ErrNotInstalled，得到: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "/some/unit") {
		t.Errorf("报错必须带上单元路径，否则用户不知道该去看哪个文件: %v", wrapped)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./internal/service/ -run 'TestAllManagersImplementInterface|TestErrNotInstalled' -v
```

Expected: 编译失败——`ErrNotInstalled` / `errNotInstalled` 未定义，且三个 manager 没有 `Stop` / `Restart`。

- [ ] **Step 3: 改 `internal/service/service.go`**

在 `Status` 结构体里，`Running` 之后加：

```go
	// Disabled 表示单元被显式停用（handoff service stop），自动拉起已关掉。
	//
	// 与「装了没跑」是两种状态，不能合并：前者的处置是 handoff service start，
	// 后者的处置是查日志找崩溃原因。合成一个布尔，status 就会给出错误的
	// 处置建议——把用户支去重装一个本来好好的单元。
	Disabled bool
```

在 `Manager` 接口里，`Start()` 之后加：

```go
	// Stop 停止一个**已安装**的单元，并关掉自动拉起，直到显式 Start。
	//
	// 「关掉自动拉起」是承重的：三个平台都配了「退出就拉起」（launchd
	// KeepAlive=true / systemd Restart=always / Windows 每分钟重复触发），
	// 只杀进程在任何一个平台上都停不住。且这个「关掉」必须跨重启生效，
	// 否则用户重启机器后会发现自己停掉的东西又回来了。
	//
	// 单元没装时返回包装了 ErrNotInstalled 的错误，**不代为安装**。
	Stop() error

	// Restart 重启一个**已安装**的单元，不改动单元定义本身。
	//
	// 语义与 systemctl restart 对齐：单元当前没在跑（含被 Stop 停住）时，
	// Restart 等价于 Start——用户在 agentd 崩着的时候敲 restart，要的是
	// 它起来，而不是一句「它没在跑」。
	//
	// 单元没装时返回包装了 ErrNotInstalled 的错误，**不代为安装**。
	Restart() error
```

在文件末尾加哨兵错误与构造器：

```go
// ErrNotInstalled 是「单元没装」的哨兵错误。
//
// Start / Stop / Restart 都不代为安装，一律用它包装返回。上层（CLI、桌面壳）
// 靠 errors.Is 区分「没装」与「装了但操作失败」：前者的处置是
// handoff service install，后者是去查日志。
var ErrNotInstalled = errors.New("服务单元未安装")

// errNotInstalled 造一个带单元路径的 ErrNotInstalled。
//
// 参数：
//   - unit: 单元文件路径或任务名，用于告诉用户该去看哪个东西
//
// 返回：可被 errors.Is(err, ErrNotInstalled) 认出的错误
func errNotInstalled(unit string) error {
	return fmt.Errorf("%w: %s（先跑 handoff service install）", ErrNotInstalled, unit)
}
```

import 里补 `"errors"`。

- [ ] **Step 4: 三个平台各加桩**

`internal/service/launchd.go` 末尾：

```go
// Stop 见 Manager.Stop。TODO(handoff): Task 2 换成真实现。
func (m *launchdManager) Stop() error {
	return fmt.Errorf("launchd Stop 尚未实现")
}

// Restart 见 Manager.Restart。TODO(handoff): Task 2 换成真实现。
func (m *launchdManager) Restart() error {
	return fmt.Errorf("launchd Restart 尚未实现")
}
```

`internal/service/systemd.go` 末尾同形（`systemdManager`，文案换 systemd，TODO 指 Task 3）。
`internal/service/windows.go` 末尾同形（`windowsManager`，文案换 schtasks，TODO 指 Task 4）。

**桩报错而不是返回 nil**：返回 nil 的桩会让「命令跑完什么也没发生」看起来像成功，是这三个 task 之间最容易漏掉的半成品状态。

- [ ] **Step 5: 两处 fakeManager 补齐**

`cmd/service_test.go`，在 `fakeManager` 结构体里加：

```go
	stopped    bool
	stopErr    error
	restarted  bool
	restartErr error
```

并在方法区加：

```go
func (f *fakeManager) Stop() error    { f.stopped = true; return f.stopErr }
func (f *fakeManager) Restart() error { f.restarted = true; return f.restartErr }
```

`desktop/internal/shell/lifecycle_test.go` 的 `fakeManager` 加同样两个字段与两个方法（`stopped` / `restarted` 用于断言「被停用时一个都没调」）。

- [ ] **Step 6: 跑测试确认通过**

```bash
gofmt -l ./internal/service ./cmd ./desktop && go build ./... && go test ./internal/service/ ./cmd/ ./desktop/... 
```

Expected: `gofmt -l` 无输出；build 通过；测试全绿。

- [ ] **Step 7: 提交**

```bash
git add internal/service/service.go internal/service/service_test.go internal/service/launchd.go internal/service/systemd.go internal/service/windows.go cmd/service_test.go desktop/internal/shell/lifecycle_test.go
git commit -m "feat(service): Manager 加 Stop/Restart，Status 加 Disabled

三平台先落报错的桩，后续 task 逐个换真实现。桩不返回 nil——
静默成功的半成品比报错难发现得多。"
```

---

### Task 2: launchd 的 Start / Stop / Restart

**Files:**
- Modify: `internal/service/launchd.go`
- Test: `internal/service/launchd_test.go`

**Interfaces:**
- Consumes: `ErrNotInstalled` / `errNotInstalled(unit string) error`（Task 1）；`Status.Disabled`（Task 1）
- Produces:
  - `launchdManager` 新增两个测试缝字段：`stat func(string) (os.FileInfo, error)`、`sleep func(time.Duration)`
  - 包内函数 `parsePrintPid(out string) int`
  - `launchdManager` 新增私有方法 `isDisabled() bool`、`currentPid() int`、`waitRunning() bool`、`waitStopped() bool`

- [ ] **Step 1: 写失败的测试**

在 `internal/service/launchd_test.go` 里，先把 `newTestLaunchd` 补上两个新缝（这是既有 helper，必须改）：

```go
func newTestLaunchd(t *testing.T, runErr error) (*launchdManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &launchdManager{
		log:      testLogger(),
		homeDir:  func() (string, error) { return "/home/u", nil },
		plistDir: "/home/u/Library/LaunchAgents",
		mkdirAll: func(string, os.FileMode) error { return nil },
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte("ok"), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
		// 默认「plist 在」：多数用例测的是已装场景。测未装的用例自己覆盖它
		stat: func(string) (os.FileInfo, error) { return nil, nil },
		// 测试里不真的睡：复核轮询最多 25 次，真睡会把包的单测从毫秒拖到秒级
		sleep: func(time.Duration) {},
	}
	return m, &calls, &written
}
```

然后追加这些用例：

```go
// Stop 必须 disable 在前、bootout 在后。
//
// why 顺序承重：disable 成功而 bootout 失败，留下的是「还在跑但已停用」，
// 重启后自己下去；反过来 bootout 成功而 disable 失败，留下的是「停了但仍
// 启用」，下次登录 launchd 自动 bootstrap 回来，把用户的 stop 无声撤销。
// 选前一种失败形态。
func TestLaunchdStopDisablesBeforeBootout(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	// print 失败 => Status.Running 为 false，复核轮询第一次就通过
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			return []byte(""), errors.New("exit status 113")
		}
		return []byte("ok"), nil
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	di, bi := strings.Index(joined, "disable"), strings.Index(joined, "bootout")
	if di < 0 || bi < 0 {
		t.Fatalf("Stop 必须同时发出 disable 与 bootout: %s", joined)
	}
	if di > bi {
		t.Errorf("disable 必须先于 bootout（否则 bootout 成功而 disable 失败时，下次登录它会自己回来）: %s", joined)
	}
}

// 只 bootout 不 disable 撑不过一次登录，所以 disable 失败必须让 Stop 失败。
func TestLaunchdStopFailsWhenDisableFails(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "disable" {
			return []byte("Could not disable service"), errors.New("exit status 1")
		}
		return []byte("ok"), nil
	}
	err := m.Stop()
	if err == nil {
		t.Fatal("disable 失败时 Stop 必须报错——只 bootout 的话它下次登录就回来了")
	}
	if !strings.Contains(err.Error(), "Could not disable") {
		t.Errorf("报错要带 launchctl 原文（真因）: %v", err)
	}
}

// 停不下来时不许报成功：bootout 返回 0 只说明请求被受理。
func TestLaunchdStopFailsWhenStillRunning(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "print" {
			return []byte("state = running\n\tpid = 42"), nil
		}
		return []byte("ok"), nil
	}
	if err := m.Stop(); err == nil {
		t.Fatal("复核到仍在运行时 Stop 必须报错，不能报「已停止」")
	}
}

// plist 不在 => 未安装，三个命令一律硬拒，且错误可被 errors.Is 判别。
func TestLaunchdLifecycleRefusesWhenNotInstalled(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*launchdManager) error
	}{
		{"Start", (*launchdManager).Start},
		{"Stop", (*launchdManager).Stop},
		{"Restart", (*launchdManager).Restart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, calls, _ := newTestLaunchd(t, nil)
			m.stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
			err := tc.call(m)
			if !errors.Is(err, ErrNotInstalled) {
				t.Fatalf("未安装时应返回 ErrNotInstalled，得到: %v", err)
			}
			for _, c := range *calls {
				for _, mutating := range []string{"bootout", "bootstrap", "kill", "disable", "enable"} {
					if strings.Contains(c, mutating) {
						t.Errorf("未安装时不得发出任何变更类命令，却发了: %s", c)
					}
				}
			}
		})
	}
}

// Start 必须 enable 在前、bootstrap 在后。
//
// why：被 launchctl disable 过的 target，bootstrap 会直接拒（Service is
// disabled）。而 Stop 正是靠 disable 生效的，所以这是 stop→start 的必经之路。
func TestLaunchdStartEnablesBeforeBootstrap(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			return []byte("state = running\n\tpid = 42"), nil
		}
		return []byte("ok"), nil
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	ei, bi := strings.Index(joined, "enable"), strings.Index(joined, "bootstrap")
	if ei < 0 || bi < 0 {
		t.Fatalf("Start 必须同时发出 enable 与 bootstrap: %s", joined)
	}
	if ei > bi {
		t.Errorf("enable 必须先于 bootstrap（被 disable 过的 target，bootstrap 会直接拒）: %s", joined)
	}
}

// Restart 发 SIGTERM，不发 kickstart -k。
//
// why：kickstart -k 是 SIGKILL，会把在途任务砍在半路；SIGTERM 走的是
// agentd 自己那条优雅关停（停收新连接→等在途请求→按序收尾），
// 而 KeepAlive=true 保证它随后被拉回来。
func TestLaunchdRestartSendsSigtermNotKickstartK(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	pid := 100
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			return []byte(fmt.Sprintf("state = running\n\tpid = %d", pid)), nil
		}
		if len(args) > 0 && args[0] == "kill" {
			pid = 200 // 模拟被 KeepAlive 拉起的新实例
		}
		return []byte("ok"), nil
	}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "kill SIGTERM") {
		t.Errorf("Restart 必须发 kill SIGTERM: %s", joined)
	}
	if strings.Contains(joined, "kickstart -k") {
		t.Error("Restart 不得用 kickstart -k：那是 SIGKILL，会把在途任务砍在半路")
	}
}

// 复核判据是 pid 变了，不是「还在跑」。
//
// why：launchd 的重启是异步的，kill 返回时旧进程可能还没死。只查
// 「在不在跑」的话，「什么都没发生」和「重启成功」长得一模一样。
func TestLaunchdRestartFailsWhenPidUnchanged(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "print" {
			return []byte("state = running\n\tpid = 100"), nil
		}
		return []byte("ok"), nil
	}
	err := m.Restart()
	if err == nil {
		t.Fatal("pid 没变时 Restart 必须报错——那说明它根本没被重启")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("报错应带上没变的那个 pid，便于排障: %v", err)
	}
}

// 没在跑时 Restart 等价于 Start：用户在 agentd 崩着的时候敲 restart，
// 要的是它起来，而不是一句「它没在跑」。语义与 systemctl restart 对齐。
func TestLaunchdRestartOnStoppedServiceStarts(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	loaded := false
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "print" {
			if !loaded {
				return []byte(""), errors.New("exit status 113")
			}
			return []byte("state = running\n\tpid = 7"), nil
		}
		if len(args) > 0 && args[0] == "bootstrap" {
			loaded = true
		}
		return []byte("ok"), nil
	}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "bootstrap") {
		t.Errorf("没在跑时 Restart 应走 Start（enable+bootstrap）: %s", joined)
	}
}

// print 查不到但 plist 还在 => 已安装、未运行。
//
// why 承重：Stop 会 bootout（卸载 job）但保留 plist。若 Installed 按
// launchctl print 判，stop 之后 start 会被「没装」硬拒——
// 「停到显式 start」当场自相矛盾。
func TestLaunchdStatusInstalledWhenPlistExistsButNotLoaded(t *testing.T) {
	m, _, _ := newTestLaunchd(t, errors.New("exit status 113"))
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed {
		t.Error("plist 还在就算已安装——bootout 只卸载 job，不删 plist")
	}
	if st.Running {
		t.Error("print 查不到时不该报在跑")
	}
}

// 两种 print-disabled 输出格式都要认。
//
// why：macOS 26 打的是 => disabled/enabled，更早的系统打的是 => true/false。
// 只认一种，会在另一种系统上把「已停用」读成「启用」，
// status 于是给出错误的处置建议。
func TestLaunchdStatusDisabledBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{
		{"新格式-已停用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => disabled\n\t}", true},
		{"新格式-已启用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => enabled\n\t}", false},
		{"旧格式-已停用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => true\n\t}", true},
		{"旧格式-已启用", "\tdisabled services = {\n\t\t\"" + LaunchdLabel + "\" => false\n\t}", false},
		{"从未出现过", "\tdisabled services = {\n\t\t\"com.other\" => disabled\n\t}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newTestLaunchd(t, nil)
			m.run = func(name string, args ...string) ([]byte, error) {
				if len(args) > 0 && args[0] == "print-disabled" {
					return []byte(tc.out), nil
				}
				return []byte("ok"), nil
			}
			st, err := m.Status()
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Disabled != tc.want {
				t.Errorf("Disabled=%v, want %v（输出: %q）", st.Disabled, tc.want, tc.out)
			}
		})
	}
}

// Install 也必须 enable，否则 stop 过的机器再也装不回来。
//
// why：Stop 用 launchctl disable 把 target 写进了停用清单，而 Install 的
// bootstrap 对停用的 target 会直接拒。Install 失败会回滚删掉刚写的 plist
// ——于是 stop 之后跑 install，用户会看到「装不上」而且 plist 也没了。
func TestLaunchdInstallEnablesBeforeBootstrap(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	if err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	ei, bi := strings.Index(joined, "enable"), strings.Index(joined, "bootstrap")
	if ei < 0 {
		t.Fatalf("Install 必须发出 enable（否则 stop 过的 target 装不回来）: %s", joined)
	}
	if ei > bi {
		t.Errorf("enable 必须先于 bootstrap: %s", joined)
	}
}
```

测试文件 import 补 `"fmt"` 与 `"time"`。

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./internal/service/ -run TestLaunchd -v
```

Expected: 编译失败（`stat` / `sleep` 字段不存在），以及桩返回「尚未实现」导致的 FAIL。

- [ ] **Step 3: 加两个缝并改 Status**

`internal/service/launchd.go`，结构体加两个字段并在文件头的字段注释里说明「七个字段是测试缝」：

```go
type launchdManager struct {
	log       *slog.Logger
	homeDir   func() (string, error)
	plistDir  string
	mkdirAll  func(path string, perm os.FileMode) error
	run       func(name string, args ...string) ([]byte, error)
	writeFile func(path string, data []byte, perm uint32) error
	remove    func(path string) error
	// stat 用来判断 plist 在不在，即「装没装」。必须是缝：Status 现在按
	// plist 存在与否判 Installed，测试要能构造「装了但没加载」这一状态
	stat func(path string) (os.FileInfo, error)
	// sleep 是复核轮询的等待缝：测试注入空实现，避免为了走完复核窗口
	// 真的睡几秒（那会让 service 包的单测从毫秒级变成秒级）
	sleep func(time.Duration)
}
```

`newLaunchd` 里补：

```go
		stat:  os.Stat,
		sleep: time.Sleep,
```

import 补 `"time"`。

文件顶部常量区加：

```go
// launchdVerifyInterval / launchdVerifyAttempts 是复核状态变化的轮询节奏。
//
// 为什么轮询而不是查一次：launchctl 的 bootstrap / bootout / kill 都是异步的
// ——命令返回只说明请求被受理，进程到位或退出还要几十到几百毫秒。查一次会
// 把正常的启动误报成失败，也会把还没死透的旧进程误报成「已停止」。
const (
	launchdVerifyInterval = 200 * time.Millisecond
	launchdVerifyAttempts = 25
	launchdVerifyWindow   = launchdVerifyInterval * launchdVerifyAttempts
)
```

用下面这版替换现有的 `Status`：

```go
// Status 查询单元的安装、运行与停用状态。
//
// 返回：
//   - Status：Installed 看 plist 在不在，Running 看 job 加载且不是
//     not running，Disabled 查 launchd 的停用清单
//   - 错误：只有取不到 plist 路径（主目录读不出来）才算查询失败；
//     「没装」「没跑」「停用了」都是正常答案，不是错误
//
// 注意：
//   - **Installed 的判据是 plist 存在，不是 launchctl print 能查到。**
//     Stop 会 bootout（把 job 从 launchd 卸载）但保留 plist；若按 print 判，
//     stop 之后 start 会被「没装」硬拒，「停到显式 start」当场自相矛盾
func (m *launchdManager) Status() (Status, error) {
	path, err := m.UnitPath()
	if err != nil {
		return Status{}, err
	}
	s := Status{}
	if _, statErr := m.stat(path); statErr == nil {
		s.Installed = true
	}
	if out, printErr := m.run("launchctl", "print", m.target()); printErr == nil {
		// 加载着就一定装着：plist 可能刚被手工删掉而 job 还留在内存里
		s.Installed = true
		s.Detail = firstLine(string(out))
		// print 输出里带 "state = not running" 才算没跑；只注册没跑是常见状态
		s.Running = !strings.Contains(string(out), "state = not running")
	}
	s.Disabled = m.isDisabled()
	m.log.Debug("launchd 服务状态", "label", LaunchdLabel,
		"installed", s.Installed, "running", s.Running, "disabled", s.Disabled)
	return s, nil
}

// isDisabled 查 launchd 的停用覆写数据库，判断本 job 是否被显式停用。
//
// 返回：被 launchctl disable 过则 true；查不到、查询失败、从未出现在清单里
// 都按 false（未停用）处理
//
// 注意：
//   - 两种输出格式都要认。macOS 26 打的是 "<label>" => disabled/enabled，
//     更早的系统打的是 => true/false。只认一种，会在另一种系统上把「已停用」
//     读成「启用」，status 于是给出错误的处置建议
//   - 从未被 enable/disable 过的 label 根本不出现在这份清单里——不出现即未停用
func (m *launchdManager) isDisabled() bool {
	out, err := m.run("launchctl", "print-disabled", m.domain())
	if err != nil {
		m.log.Debug("查 launchd 停用清单失败，按未停用处理",
			"cause", err, "output", strings.TrimSpace(string(out)))
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, `"`+LaunchdLabel+`"`) {
			continue
		}
		i := strings.Index(line, "=>")
		if i < 0 {
			continue
		}
		v := strings.TrimSpace(line[i+2:])
		return v == "disabled" || v == "true"
	}
	return false
}

// parsePrintPid 从 launchctl print 的输出里取当前实例的 pid。
//
// 返回：取不到（未加载、或那一刻没有进程）返回 0
//
// 注意：pid 是 Restart 唯一可信的复核判据——「还在跑」区分不了新旧实例
func parsePrintPid(out string) int {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "pid = ") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}
```

- [ ] **Step 4: 实现 Start / Stop / Restart，替换 Task 1 的桩**

把现有 `Start` 整体替换，并把 Task 1 落的两个桩换成下面的实现：

```go
// ensureInstalled 在做任何变更前确认 plist 在。
//
// 返回：plist 路径；不在时返回包装了 ErrNotInstalled 的错误
//
// 注意：三个生命周期方法共用它，且必须在发出任何 launchctl 变更命令之前调用
// ——「没装」的正确处置是去 install，不是让 start 悄悄替 install 干活
func (m *launchdManager) ensureInstalled() (string, error) {
	path, err := m.UnitPath()
	if err != nil {
		return "", err
	}
	if _, statErr := m.stat(path); statErr != nil {
		m.log.Error("单元未安装", "label", LaunchdLabel, "plist", path, "cause", statErr)
		return "", errNotInstalled(path)
	}
	return path, nil
}

// waitRunning 轮询到服务真的在跑为止。超时返回 false。
func (m *launchdManager) waitRunning() bool {
	for i := 0; i < launchdVerifyAttempts; i++ {
		if st, err := m.Status(); err == nil && st.Running {
			return true
		}
		m.sleep(launchdVerifyInterval)
	}
	return false
}

// waitStopped 轮询到服务真的不在跑为止。超时返回 false。
func (m *launchdManager) waitStopped() bool {
	for i := 0; i < launchdVerifyAttempts; i++ {
		if st, err := m.Status(); err == nil && !st.Running {
			return true
		}
		m.sleep(launchdVerifyInterval)
	}
	return false
}

// currentPid 取当前实例的 pid，取不到（未加载 / 没在跑）返回 0。
func (m *launchdManager) currentPid() int {
	out, err := m.run("launchctl", "print", m.target())
	if err != nil {
		return 0
	}
	return parsePrintPid(string(out))
}

// Start 启动一个已安装的服务，并解除可能存在的停用状态。
//
// 返回：错误——plist 不在（ErrNotInstalled）、enable 失败、加载失败、
// 或复核窗口内没见它跑起来
//
// 注意：
//   - **enable 必须在 bootstrap 之前。** 被 launchctl disable 过的 target，
//     bootstrap 会直接拒（Service is disabled），而 Stop 正是靠 disable 才让
//     「停到显式 start」跨得过重启——这条路是 stop→start 的必经之路
//   - 用 bootstrap 而不是 kickstart：Stop 做过 bootout，job 已从 launchd
//     卸载，kickstart 找不到目标。已经加载着时 bootstrap 会报
//     "service already loaded"，那不是失败（目标状态本就是「加载着」），
//     降级为 kickstart 把它踢起来
//   - 不代为 Install
func (m *launchdManager) Start() error {
	path, err := m.ensureInstalled()
	if err != nil {
		return err
	}
	m.log.Info("启动 launchd 服务", "label", LaunchdLabel, "plist", path)
	if out, eerr := m.run("launchctl", "enable", m.target()); eerr != nil {
		m.log.Error("解除停用失败", "label", LaunchdLabel,
			"cause", eerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("解除停用失败: %s（%w）", strings.TrimSpace(string(out)), eerr)
	}
	if out, berr := m.run("launchctl", "bootstrap", m.domain(), path); berr != nil {
		m.log.Debug("bootstrap 报错，改用 kickstart（已加载时属正常）",
			"output", strings.TrimSpace(string(out)))
		if kout, kerr := m.run("launchctl", "kickstart", m.target()); kerr != nil {
			m.log.Error("启动 launchd 服务失败", "label", LaunchdLabel,
				"cause", kerr, "output", strings.TrimSpace(string(kout)))
			return fmt.Errorf("启动 launchd 服务失败: %s（%w）",
				strings.TrimSpace(string(kout)), kerr)
		}
	}
	if !m.waitRunning() {
		m.log.Error("服务已触发但复核窗口内未见运行",
			"label", LaunchdLabel, "window", launchdVerifyWindow)
		return fmt.Errorf("服务已触发，但 %s 内未复核到运行（可能起来即退出）", launchdVerifyWindow)
	}
	m.log.Info("launchd 服务已启动", "label", LaunchdLabel)
	return nil
}

// Stop 停止服务并关掉自动拉起，直到显式 Start。
//
// 返回：错误——plist 不在（ErrNotInstalled）、disable 失败、
// 或复核窗口内它仍在跑
//
// 注意：
//   - **disable 在前、bootout 在后，顺序是承重的。** disable 成功而 bootout
//     失败，留下的是「还在跑但已停用」，重启后自己下去；反过来 bootout 成功
//     而 disable 失败，留下的是「停了但仍启用」，下次登录 launchd 自动把它
//     bootstrap 回来，用户的 stop 被无声撤销。选前一种失败形态
//   - **只 bootout 不 disable 是不够的**：plist 还躺在 ~/Library/LaunchAgents
//     里，RunAtLoad 会在下次登录时把它拉回来
func (m *launchdManager) Stop() error {
	path, err := m.ensureInstalled()
	if err != nil {
		return err
	}
	m.log.Info("停止并停用 launchd 服务", "label", LaunchdLabel, "plist", path)
	if out, derr := m.run("launchctl", "disable", m.target()); derr != nil {
		m.log.Error("停用 launchd 服务失败", "label", LaunchdLabel,
			"cause", derr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("停用失败: %s（%w）", strings.TrimSpace(string(out)), derr)
	}
	if out, berr := m.run("launchctl", "bootout", m.target()); berr != nil {
		// 本来就没加载时 bootout 必然报错，这不是失败
		m.log.Debug("bootout 报错（本来就没加载时属正常）",
			"output", strings.TrimSpace(string(berr.Error())), "output_raw", strings.TrimSpace(string(out)))
	}
	if !m.waitStopped() {
		m.log.Error("已请求停止但复核窗口内仍在运行",
			"label", LaunchdLabel, "window", launchdVerifyWindow)
		return fmt.Errorf("已请求停止，但 %s 内它仍在运行", launchdVerifyWindow)
	}
	m.log.Info("launchd 服务已停止并停用", "label", LaunchdLabel)
	return nil
}

// Restart 就地重启服务，不改动 plist。
//
// 返回：错误——plist 不在（ErrNotInstalled）、发信号失败、
// 或复核窗口内 pid 没换
//
// 注意：
//   - **发 SIGTERM 而不是 kickstart -k。** 后者是 SIGKILL，会把在途任务砍在
//     半路；SIGTERM 走的是 agentd 自己那条优雅关停（停收新连接 → 等在途请求
//     → 按序收尾），而 plist 里的 KeepAlive=true 保证它随后被拉回来
//   - **复核判据是 pid 变了且在跑，不是「还在跑」。** launchd 的重启是异步的，
//     kill 返回时旧进程可能还没死；只查「在不在跑」的话，「什么都没发生」和
//     「重启成功」长得一模一样
//   - 没在跑（含被 Stop 停住）时等价于 Start，语义与 systemctl restart 对齐：
//     用户在 agentd 崩着的时候敲 restart，要的是它起来
func (m *launchdManager) Restart() error {
	if _, err := m.ensureInstalled(); err != nil {
		return err
	}
	before := m.currentPid()
	if before == 0 {
		m.log.Info("重启时发现服务未在运行，改为启动", "label", LaunchdLabel)
		return m.Start()
	}
	m.log.Info("重启 launchd 服务", "label", LaunchdLabel, "pid_before", before)
	if out, kerr := m.run("launchctl", "kill", "SIGTERM", m.target()); kerr != nil {
		m.log.Error("发送 SIGTERM 失败", "label", LaunchdLabel,
			"cause", kerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("发送 SIGTERM 失败: %s（%w）", strings.TrimSpace(string(out)), kerr)
	}
	for i := 0; i < launchdVerifyAttempts; i++ {
		m.sleep(launchdVerifyInterval)
		if now := m.currentPid(); now != 0 && now != before {
			m.log.Info("launchd 服务已重启", "label", LaunchdLabel,
				"pid_before", before, "pid_after", now)
			return nil
		}
	}
	m.log.Error("已发 SIGTERM 但复核窗口内 pid 未换",
		"label", LaunchdLabel, "pid_before", before, "window", launchdVerifyWindow)
	return fmt.Errorf("已发 SIGTERM，但 %s 内 pid 仍是 %d（可能没被拉起来，检查日志）",
		launchdVerifyWindow, before)
}
```

import 补 `"strconv"`（`parsePrintPid` 用）——文件里已有则不重复。

**注意**：`Stop` 里那行 bootout 的 Debug 日志按上面写会重复取值，实现时写成简洁的一行即可：

```go
	if out, berr := m.run("launchctl", "bootout", m.target()); berr != nil {
		m.log.Debug("bootout 报错（本来就没加载时属正常）", "output", strings.TrimSpace(string(out)))
	}
```

- [ ] **Step 5: 给 Install 补 enable**

`Install` 里，把「先清旧」那段改成 bootout 之后、写盘之前多发一次 enable：

```go
	// 先清旧：同名 job 还注册着时 bootstrap 会直接失败（"service already loaded"）。
	// 忽略这一步的错误——绝大多数情况下它本来就没装，报错是正常的
	if out, err := m.run("launchctl", "bootout", m.target()); err != nil {
		m.log.Debug("bootout 旧 job（未装时报错属正常）", "output", strings.TrimSpace(string(out)))
	}

	// 解除可能残留的停用状态。
	//
	// 承重：Stop 用 launchctl disable 把 target 写进了 launchd 的停用清单，
	// 而那份清单独立于 plist——删掉 plist 再重装也不会清掉它。对停用的 target
	// bootstrap 会直接拒，Install 随后回滚删掉刚写的 plist：用户 stop 过一次
	// 之后跑 install，看到的是「装不上」而且 plist 也没了。
	// 忽略错误：从未被 disable 过的 target，enable 报错是正常的
	if out, err := m.run("launchctl", "enable", m.target()); err != nil {
		m.log.Debug("enable（从未停用过时报错属正常）", "output", strings.TrimSpace(string(out)))
	}
```

- [ ] **Step 6: 复核日志覆盖**

对照检查（这是 instrumenting-code 的清单，不是走过场）：
- 每个方法入口有 Info，带 label 与关键输入
- 每个错误分支有 Error，带 cause 与 launchctl 原文
- 成功路径有 Info 收尾（Start/Stop/Restart 各一条，文案互不相同，走查时按文案分辨力区分）
- `Status` / `isDisabled` 的高频路径降到 Debug
- 全文件无 `fmt.Printf`

- [ ] **Step 7: 跑测试确认通过**

```bash
gofmt -l ./internal/service && go test ./internal/service/ -run TestLaunchd -v
```

Expected: `gofmt -l` 无输出；全部 PASS。

- [ ] **Step 8: 跑全包测试**

```bash
go test ./internal/service/
```

Expected: PASS（既有 Install / Status 用例不得回归）。

- [ ] **Step 9: 提交**

```bash
git add internal/service/launchd.go internal/service/launchd_test.go
git commit -m "feat(service): launchd 的 start/stop/restart

stop 是 disable+bootout（顺序承重）、restart 发 SIGTERM 而非 kickstart -k
（后者是 SIGKILL），复核判据是 pid 变了。Status 的 Installed 改看 plist
在不在——bootout 之后 print 查不到，按 print 判会让 start 被自己硬拒。"
```

---

### Task 3: systemd 的 Start / Stop / Restart

**Files:**
- Modify: `internal/service/systemd.go`
- Test: `internal/service/systemd_test.go`

**Interfaces:**
- Consumes: `errNotInstalled(unit string) error`、`ErrNotInstalled`（Task 1）
- Produces:
  - `systemdManager` 新增 `stat func(string) (os.FileInfo, error)` 缝
  - 包内函数 `sudoHint(action, output string) string`

- [ ] **Step 1: 写失败的测试**

先给 `newTestSystemd` 补 `stat` 缝：

```go
func newTestSystemd(t *testing.T, runErr error) (*systemdManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &systemdManager{
		log:     testLogger(),
		unitDir: "/etc/systemd/system",
		user:    "alice",
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte("ok"), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
		// 默认「unit 在」：多数用例测的是已装场景
		stat: func(string) (os.FileInfo, error) { return nil, nil },
	}
	return m, &calls, &written
}
```

追加用例：

```go
// Stop 必须走 disable --now，不是裸 stop。
//
// why：只 stop 的话 unit 还是 enabled，下次开机 systemd 照样拉起来，
// 「停到显式 start」撑不过一次重启。
func TestSystemdStopDisablesNotJustStops(t *testing.T) {
	m, calls, _ := newTestSystemd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "is-active" {
			return []byte("inactive"), errors.New("exit status 3")
		}
		return []byte("ok"), nil
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "disable --now") {
		t.Errorf("Stop 必须 disable --now（只 stop 撑不过一次重启）: %s", joined)
	}
}

// 停不下来时不许报成功：is-active 仍返回 0 说明它还活着。
func TestSystemdStopFailsWhenStillActive(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	if err := m.Stop(); err == nil {
		t.Fatal("is-active 仍返回 0（还活着）时 Stop 必须报错")
	}
}

// Start 必须 enable --now，不是裸 start。
//
// why：Stop 用 disable 摘掉了开机自启，只 start 能跑起来但下次开机又没了
// ——「显式 start 之后恢复原状」才是这里的目标。
func TestSystemdStartEnablesNotJustStarts(t *testing.T) {
	m, calls, _ := newTestSystemd(t, nil)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "enable --now") {
		t.Errorf("Start 必须 enable --now（否则 stop 过的单元下次开机不回来）: %s", joined)
	}
}

// Restart 走 systemctl restart，并复核 is-active。
func TestSystemdRestartVerifiesActive(t *testing.T) {
	m, calls, _ := newTestSystemd(t, nil)
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "systemctl restart") {
		t.Errorf("Restart 必须发 systemctl restart: %s", joined)
	}
	ri, ai := strings.Index(joined, "restart"), strings.LastIndex(joined, "is-active")
	if ai < ri {
		t.Errorf("复核（is-active）必须在 restart 之后: %s", joined)
	}
}

// unit 文件不在 => 未安装，三个命令一律硬拒且不发变更命令。
func TestSystemdLifecycleRefusesWhenNotInstalled(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*systemdManager) error
	}{
		{"Start", (*systemdManager).Start},
		{"Stop", (*systemdManager).Stop},
		{"Restart", (*systemdManager).Restart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, calls, _ := newTestSystemd(t, nil)
			m.stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
			err := tc.call(m)
			if !errors.Is(err, ErrNotInstalled) {
				t.Fatalf("未安装时应返回 ErrNotInstalled，得到: %v", err)
			}
			for _, c := range *calls {
				for _, mutating := range []string{"start", "stop", "restart", "enable", "disable"} {
					if strings.Contains(c, mutating) {
						t.Errorf("未安装时不得发出变更类命令，却发了: %s", c)
					}
				}
			}
		})
	}
}

// 权限不足要给出「用 sudo 重跑」，不能扁平抛原文。
//
// why（B45 的教训）：system unit 落在 /etc/systemd/system，所有变更类操作
// 都需要 root；只抛一句 Access denied，用户不知道下一步该干什么。
func TestSystemdStopHintsSudoOnPermissionDenied(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "disable" {
			return []byte("Failed to disable unit: Access denied"), errors.New("exit status 1")
		}
		return []byte("ok"), nil
	}
	err := m.Stop()
	if err == nil {
		t.Fatal("权限不足时 Stop 必须报错")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("权限不足的报错必须提示用 sudo 重跑，得到: %v", err)
	}
}

// is-enabled 报 disabled 时 Status.Disabled 为 true。
//
// why 可以按文本判：is-enabled 的输出是稳定的英文枚举，不随 locale 变
// （这与 Windows 的 Status 列不同，那一列会本地化）。
func TestSystemdStatusDisabled(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		switch {
		case len(args) > 0 && args[0] == "is-enabled":
			// disabled 时 systemctl 返回非 0，所以这里连错误一起给
			return []byte("disabled\n"), errors.New("exit status 1")
		case len(args) > 0 && args[0] == "is-active":
			return []byte("inactive"), errors.New("exit status 3")
		}
		return []byte("ok"), nil
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Disabled {
		t.Error("is-enabled 报 disabled 时 Status.Disabled 应为 true")
	}
}
```

测试 import 补 `"os"`。

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./internal/service/ -run TestSystemd -v
```

Expected: 编译失败（`stat` 字段不存在）+ 桩返回「尚未实现」的 FAIL。

- [ ] **Step 3: 加 stat 缝、改 Status、加 sudoHint**

结构体加：

```go
	// stat 用来判断 unit 文件在不在，即「装没装」。做成缝是为了让
	// 「装了但没跑」「没装」这两种状态在测试里都构造得出来
	stat func(path string) (os.FileInfo, error)
```

构造器里补 `stat: os.Stat,`。把 `Status` 里现有的 `os.Stat(path)` 换成 `m.stat(path)`。

`Status` 改成同时填 Disabled：

```go
// Status 查询 unit 是否装了、是否活跃、是否被停用。
//
// 返回：
//   - Status：Installed 看 unit 文件在不在，Running 看 is-active，
//     Disabled 看 is-enabled
//   - 错误：恒为 nil。「没装」「没跑」「停用了」都是正常答案
//
// 注意：
//   - is-enabled 的输出是稳定的英文枚举（enabled/disabled/static/...），
//     不随 locale 变，可以按文本判。disabled 时它返回非 0，所以不看 err
func (m *systemdManager) Status() (Status, error) {
	s := Status{}
	path, _ := m.UnitPath()
	if _, statErr := m.stat(path); statErr == nil {
		s.Installed = true
	}
	out, err := m.run("systemctl", "is-active", SystemdUnit)
	s.Detail = firstLine(string(out))
	if err == nil {
		s.Installed = true
		s.Running = true
	}
	if eout, _ := m.run("systemctl", "is-enabled", SystemdUnit); strings.TrimSpace(firstLine(string(eout))) == "disabled" {
		s.Disabled = true
	}
	m.log.Debug("systemd 服务状态", "unit", SystemdUnit,
		"installed", s.Installed, "running", s.Running, "disabled", s.Disabled)
	return s, nil
}

// sudoHint 在 systemctl 的输出里认出权限不足，并把报文换成可执行的处置。
//
// 参数：
//   - action: 正在做的事，用于拼报文（如「停止」「启动」「重启」）
//   - output: systemctl 的原文
//
// 返回：面向用户的报文。权限不足时带上「请用 sudo 重跑」，否则原样带出原文
//
// 注意：B45 的教训——system unit 落在 /etc/systemd/system，所有变更类操作
// 都要 root；扁平抛一句 Access denied，用户不知道下一步该干什么
func sudoHint(action, output string) string {
	low := strings.ToLower(output)
	for _, sig := range []string{"access denied", "permission denied", "interactive authentication required"} {
		if strings.Contains(low, sig) {
			return fmt.Sprintf("%s systemd 服务需要 root 权限，请用 sudo 重跑：%s", action, output)
		}
	}
	return fmt.Sprintf("%s systemd 服务失败: %s", action, output)
}
```

- [ ] **Step 4: 实现 Start / Stop / Restart**

替换现有 `Start`，并把 Task 1 的两个桩换掉：

```go
// ensureInstalled 在做任何变更前确认 unit 文件在。
//
// 返回：unit 路径；不在时返回包装了 ErrNotInstalled 的错误
func (m *systemdManager) ensureInstalled() (string, error) {
	path, err := m.UnitPath()
	if err != nil {
		return "", err
	}
	if _, statErr := m.stat(path); statErr != nil {
		m.log.Error("单元未安装", "unit", SystemdUnit, "path", path, "cause", statErr)
		return "", errNotInstalled(path)
	}
	return path, nil
}

// Start 启动已安装的单元，并恢复开机自启。
//
// 返回：错误——unit 不在（ErrNotInstalled）、enable --now 失败、
// 或复核不到 active
//
// 注意：
//   - **用 enable --now 而不是裸 start。** Stop 用 disable 摘掉了开机自启，
//     只 start 能把它跑起来但下次开机又没了；「显式 start 之后恢复原状」
//     才是这里的目标
//   - 不重写 unit 文件、不 daemon-reload：那是 Install 的职责
//   - 不代为 Install
func (m *systemdManager) Start() error {
	path, err := m.ensureInstalled()
	if err != nil {
		return err
	}
	m.log.Info("启动 systemd 服务", "unit", SystemdUnit, "path", path)
	if out, serr := m.run("systemctl", "enable", "--now", SystemdUnit); serr != nil {
		m.log.Error("启动 systemd 服务失败", "unit", SystemdUnit,
			"cause", serr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("%s（%w）", sudoHint("启动", strings.TrimSpace(string(out))), serr)
	}
	// 复核：enable --now 返回 0 只说明请求被受理，起来即退出照样「成功」
	if out, aerr := m.run("systemctl", "is-active", SystemdUnit); aerr != nil {
		m.log.Error("服务已触发但未 active", "unit", SystemdUnit,
			"cause", aerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("服务已触发但未处于活跃状态（可能起来即退出，查 journalctl -u %s）: %s（%w）",
			SystemdUnit, strings.TrimSpace(string(out)), aerr)
	}
	m.log.Info("systemd 服务已启动", "unit", SystemdUnit)
	return nil
}

// Stop 停止服务并关掉开机自启，直到显式 Start。
//
// 返回：错误——unit 不在（ErrNotInstalled）、disable --now 失败、
// 或复核到它仍然 active
//
// 注意：
//   - **用 disable --now 而不是裸 stop。** 只 stop 的话 unit 还是 enabled，
//     下次开机 systemd 照样把它拉起来，「停到显式 start」撑不过一次重启
//   - unit 里的 Restart=always 不会跟显式 stop 打架：systemd 的重启策略只对
//     「进程自己退出」生效，对 systemctl stop 不生效
func (m *systemdManager) Stop() error {
	path, err := m.ensureInstalled()
	if err != nil {
		return err
	}
	m.log.Info("停止并停用 systemd 服务", "unit", SystemdUnit, "path", path)
	if out, derr := m.run("systemctl", "disable", "--now", SystemdUnit); derr != nil {
		m.log.Error("停止 systemd 服务失败", "unit", SystemdUnit,
			"cause", derr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("%s（%w）", sudoHint("停止", strings.TrimSpace(string(out))), derr)
	}
	// 复核：is-active 仍返回 0 说明它还活着，那时报「已停止」就是撒谎
	if out, aerr := m.run("systemctl", "is-active", SystemdUnit); aerr == nil {
		m.log.Error("已请求停止但仍处于活跃状态", "unit", SystemdUnit,
			"output", strings.TrimSpace(string(out)))
		return fmt.Errorf("已请求停止，但 unit 仍处于活跃状态: %s", strings.TrimSpace(string(out)))
	}
	m.log.Info("systemd 服务已停止并停用", "unit", SystemdUnit)
	return nil
}

// Restart 就地重启，不重写 unit、不 daemon-reload。
//
// 返回：错误——unit 不在（ErrNotInstalled）、restart 失败、或复核不到 active
//
// 注意：
//   - systemctl restart 默认发 SIGTERM，配合 unit 里的 KillMode=process，
//     执行者不会被连坐（B36）
//   - 它是**同步**的：返回时新实例已经起来。所以复核一次 is-active 就够，
//     不像 launchd 那样要轮询等 KeepAlive 把它拉回来
//   - 单元当前没在跑（含被 Stop 停住）时，systemctl restart 会直接把它启起来
//     ——与 Manager.Restart 的约定一致，无需在这里特判
func (m *systemdManager) Restart() error {
	path, err := m.ensureInstalled()
	if err != nil {
		return err
	}
	m.log.Info("重启 systemd 服务", "unit", SystemdUnit, "path", path)
	if out, rerr := m.run("systemctl", "restart", SystemdUnit); rerr != nil {
		m.log.Error("重启 systemd 服务失败", "unit", SystemdUnit,
			"cause", rerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("%s（%w）", sudoHint("重启", strings.TrimSpace(string(out))), rerr)
	}
	if out, aerr := m.run("systemctl", "is-active", SystemdUnit); aerr != nil {
		m.log.Error("已重启但未 active", "unit", SystemdUnit,
			"cause", aerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("已重启但未处于活跃状态（可能起来即退出，查 journalctl -u %s）: %s（%w）",
			SystemdUnit, strings.TrimSpace(string(out)), aerr)
	}
	m.log.Info("systemd 服务已重启", "unit", SystemdUnit)
	return nil
}
```

**注意**：`Restart` 里 `Stop` 停住（disabled）的单元被 restart 后会跑起来但仍是 disabled（restart 不 enable）。这与 systemd 原生语义一致，`status` 会把它报成「运行中但已停用」那一档（Task 5），不需要在这里特判。

- [ ] **Step 5: 复核日志覆盖**

同 Task 2 的清单：入口 Info、错误分支 Error 带 cause 与 systemctl 原文、成功路径 Info 收尾且三条文案互不相同、Status 降 Debug、无 `fmt.Printf`。

- [ ] **Step 6: 跑测试**

```bash
gofmt -l ./internal/service && go test ./internal/service/ -run TestSystemd -v && go test ./internal/service/
```

Expected: `gofmt -l` 无输出；全部 PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/service/systemd.go internal/service/systemd_test.go
git commit -m "feat(service): systemd 的 start/stop/restart

stop 是 disable --now、start 是 enable --now：只 stop/start 的话
开机自启状态对不上，停住的单元下次开机又回来了。权限不足统一走
sudoHint 给出可执行处置（B45）。"
```

---

### Task 4: schtasks（Windows）的 Start / Stop / Restart

**Files:**
- Modify: `internal/service/windows.go`
- Test: `internal/service/windows_test.go`

**Interfaces:**
- Consumes: `errNotInstalled(unit string) error`、`ErrNotInstalled`（Task 1）
- Produces: `windowsManager` 新增私有方法 `ensureInstalled() error`、`waitStopped() error`、`isDisabled() bool`

**平台验证说明（承重，别跳过）**：本仓库的 Windows 侧断言只有单测（喂假输出）这一道防线，本 task 不做真机验证。`isDisabled` 的判据（`schtasks /Query /XML` 里的 `<Enabled>false</Enabled>`）与 `/Change /Disable` 的实际行为**必须在下一次 Windows 走查时真机复核**，走查前不得把它当成已验证的事实。

- [ ] **Step 1: 写失败的测试**

在 `internal/service/windows_test.go` 里追加。既有 helper 的签名是
`newTestWindows(t *testing.T, runOut string, runErr error) (*windowsManager, *[]string, *map[string][]byte)`，
默认让 `/FO LIST` 答「在跑」（含 `267009`）。需要别的答案时，按文件里既有用例的做法**整体替换 `m.run`**，
并记得往返回的 `*calls` 里追加，否则序列断言拿不到调用记录。

```go
// Stop 必须先 /Change /Disable 再 /End。
//
// why 顺序承重：任务的 TimeTrigger 每分钟重复触发一次（等价于
// Restart=always）。先 /End 的话它会在 60 秒内被重新拉起，「停住」
// 根本不成立。先掐掉触发源，再回收进程。
func TestWindowsStopDisablesBeforeEnd(t *testing.T) {
	m, calls, _ := newTestWindows(t, "ok", nil)
	// 复核轮询要立刻看到「不在跑」，否则要等满复核窗口才失败
	m.run = func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "/FO LIST") {
			return []byte("TaskName: \\handoff-agentd\r\nLast Result: 0\r\n"), nil
		}
		return []byte("ok"), nil
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	di, ei := strings.Index(joined, "/Disable"), strings.Index(joined, "/End")
	if di < 0 || ei < 0 {
		t.Fatalf("Stop 必须同时发出 /Change /Disable 与 /End: %s", joined)
	}
	if di > ei {
		t.Errorf("/Disable 必须先于 /End（否则每分钟的重复触发会在 60 秒内把它拉回来）: %s", joined)
	}
}

// 停不下来时不许报成功。
func TestWindowsStopFailsWhenStillRunning(t *testing.T) {
	m, _, _ := newTestWindows(t, "ok", nil)
	// 默认的 /FO LIST 就含 267009（在跑），无需覆盖
	if err := m.Stop(); err == nil {
		t.Fatal("复核到仍在运行时 Stop 必须报错，不能报「已停止」")
	}
}

// Start 必须先 /Change /Enable 再 /Run。
//
// why：被 /Change /Disable 停用过的任务，/Run 会直接报「任务已禁用」。
func TestWindowsStartEnablesBeforeRun(t *testing.T) {
	m, calls, _ := newTestWindows(t, "ok", nil)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	ei, ri := strings.Index(joined, "/Enable"), strings.Index(joined, "/Run")
	if ei < 0 || ri < 0 {
		t.Fatalf("Start 必须同时发出 /Change /Enable 与 /Run: %s", joined)
	}
	if ei > ri {
		t.Errorf("/Enable 必须先于 /Run（停用过的任务 /Run 会被拒）: %s", joined)
	}
}

// Restart 必须 /End 之后才 /Run。
//
// why 不能省中间那次复核：上一个实例还在时，计划任务服务会忽略这次
// /Run，于是「什么都没发生」被报成「已重启」。
func TestWindowsRestartEndsThenRuns(t *testing.T) {
	m, calls, _ := newTestWindows(t, "ok", nil)
	ended := false
	m.run = func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		*calls = append(*calls, name+" "+joined)
		if strings.Contains(joined, "/FO LIST") {
			// /End 之前答「在跑」，之后答「不在跑」，/Run 之后再答「在跑」
			if ended {
				return []byte("TaskName: \\handoff-agentd\r\nLast Result: 0\r\n"), nil
			}
			return []byte("TaskName: \\handoff-agentd\r\nLast Result: 267009\r\n"), nil
		}
		if strings.Contains(joined, "/End") {
			ended = true
		}
		if strings.Contains(joined, "/Run") {
			ended = false
		}
		return []byte("ok"), nil
	}
	if err := m.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	ei, ri := strings.Index(joined, "/End"), strings.LastIndex(joined, "/Run")
	if ei < 0 || ri < 0 || ei > ri {
		t.Errorf("Restart 必须先 /End 再 /Run: %s", joined)
	}
}

// 任务查不到 => 未安装，三个命令一律硬拒且不发变更命令。
func TestWindowsLifecycleRefusesWhenNotInstalled(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*windowsManager) error
	}{
		{"Start", (*windowsManager).Start},
		{"Stop", (*windowsManager).Stop},
		{"Restart", (*windowsManager).Restart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, calls, _ := newTestWindows(t, "ERROR: 找不到任务", errors.New("exit status 1"))
			err := tc.call(m)
			if !errors.Is(err, ErrNotInstalled) {
				t.Fatalf("未安装时应返回 ErrNotInstalled，得到: %v", err)
			}
			for _, c := range *calls {
				for _, mutating := range []string{"/Run", "/End", "/Change", "/Create", "/Delete"} {
					if strings.Contains(c, mutating) {
						t.Errorf("未安装时不得发出变更类命令，却发了: %s", c)
					}
				}
			}
		})
	}
}

// Disabled 判据取自任务 XML 的 <Enabled>，不取本地化的状态列。
//
// why：/Query /V 的「Scheduled Task State」列会本地化（英文 Disabled、
// 中文「已禁用」），按文本判会在换一台机器时静默失效；XML 里的 <Enabled>
// 是 schema 标签名，不随 locale 变。
//
// 输出可能是 UTF-16LE（schtasks 的 /XML 走 Unicode），所以判之前先滤掉
// NUL 字节——这比为一处判据引一个真解码器便宜，且两种编码都能命中。
func TestWindowsStatusDisabledFromXML(t *testing.T) {
	for _, tc := range []struct {
		name string
		xml  string
		want bool
	}{
		{"UTF-8 已停用", "<Settings><Enabled>false</Enabled></Settings>", true},
		{"UTF-8 已启用", "<Settings><Enabled>true</Enabled></Settings>", false},
		{"缺省即启用", "<Settings><Hidden>false</Hidden></Settings>", false},
		{"UTF-16LE 已停用", string(toUTF16LE("<Settings><Enabled>false</Enabled></Settings>")), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newTestWindows(t, "ok", nil)
			xml := tc.xml
			m.run = func(name string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "/XML") {
					return []byte(xml), nil
				}
				if strings.Contains(joined, "/FO LIST") {
					return []byte("TaskName: \\handoff-agentd\r\nLast Result: 267009\r\n"), nil
				}
				return []byte("ok"), nil
			}
			st, err := m.Status()
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Disabled != tc.want {
				t.Errorf("Disabled=%v, want %v", st.Disabled, tc.want)
			}
		})
	}
}
```

上面用到的 `toUTF16LE` 是 `windows.go` 里既有的函数，测试文件里还有一个现成的 `fromUTF16LE`，都不需要新造。

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./internal/service/ -run TestWindows -v
```

Expected: 桩返回「尚未实现」的 FAIL，以及缺 helper 的编译错误。

- [ ] **Step 3: 加 ensureInstalled / waitStopped / isDisabled**

```go
// ensureInstalled 在做任何变更前确认计划任务存在。
//
// 返回：任务不存在时返回包装了 ErrNotInstalled 的错误
//
// 注意：三个生命周期方法共用它，且必须在发出任何 schtasks 变更命令之前调用
func (m *windowsManager) ensureInstalled() error {
	if out, qerr := m.run("schtasks", "/Query", "/TN", WindowsTaskName); qerr != nil {
		m.log.Error("计划任务查询不到，单元未安装",
			"task", WindowsTaskName, "cause", qerr, "output", strings.TrimSpace(string(out)))
		return errNotInstalled(WindowsTaskName)
	}
	return nil
}

// waitStopped 轮询到任务真的不在跑为止，供 Stop 与 Restart 共用。
//
// 参数：
//   - okMsg: 复核通过时打的日志文案。由调用方给而不是写死——Stop 与 Restart
//     是两件不同的事，走查按这行文案区分它们，合并会让判据失去分辨力
//
// 返回：复核窗口内没停下来时返回错误
//
// 注意：轮询而不是查一次——/End 只是把停止请求交给计划任务服务，
// 进程退出还要一段时间；查一次会把「还没死透」当成「停不下来」
func (m *windowsManager) waitStopped(okMsg string) error {
	var last Status
	for i := 0; i < installVerifyAttempts; i++ {
		m.sleep(installVerifyInterval)
		st, serr := m.Status()
		if serr == nil && !st.Running {
			m.log.Info(okMsg, "task", WindowsTaskName, "probe", i+1)
			return nil
		}
		last = st
	}
	m.log.Error("已请求停止但复核窗口内进程仍在",
		"task", WindowsTaskName, "window", installVerifyWindow, "detail", last.Detail)
	return fmt.Errorf("已请求停止，但 %s 内任务仍在运行", installVerifyWindow)
}

// isDisabled 从任务 XML 读 Settings/Enabled，判断任务是否被显式停用。
//
// 返回：显式为 false 才算停用；查不到、查询失败、标签缺省都按未停用处理
//
// 注意：
//   - **不看 /Query /V 的「Scheduled Task State」列**：那一列会本地化
//     （英文机器 Disabled、中文机器「已禁用」），按文本判会在换一台机器时
//     静默失效。XML 里的 <Enabled> 是 schema 标签名，不随 locale 变
//   - schtasks 的 /XML 输出走 Unicode，可能是 UTF-16LE。判之前滤掉 NUL 字节
//     ——这比为一处判据引一个真解码器便宜，且 UTF-8/UTF-16LE 都能命中
func (m *windowsManager) isDisabled() bool {
	out, err := m.run("schtasks", "/Query", "/TN", WindowsTaskName, "/XML", "ONE")
	if err != nil {
		m.log.Debug("查任务 XML 失败，按未停用处理",
			"task", WindowsTaskName, "output", strings.TrimSpace(string(out)))
		return false
	}
	plain := strings.ReplaceAll(string(out), "\x00", "")
	return strings.Contains(plain, "<Enabled>false</Enabled>")
}
```

在 `Status` 的成功分支里补一行 `s.Disabled = m.isDisabled()`，并把它加进那条 Debug 日志的字段里。

- [ ] **Step 4: 实现 Start / Stop / Restart**

替换现有 `Start`，并把 Task 1 的两个桩换掉：

```go
// Start 启动已安装的计划任务，并解除可能存在的停用状态。
//
// 返回：错误——任务不存在（ErrNotInstalled）、/Change 或 /Run 失败、
// 或复核窗口内没见进程存活
//
// 注意：
//   - **/Change /Enable 必须在 /Run 之前**：被 /Change /Disable 停用过的任务，
//     /Run 会直接报「任务已禁用」
//   - 不写 XML、不 /Create、不 /Delete——这正是它存在的理由：Windows 的
//     Install 会先 /Delete /F 再重建，每次换版都会把任务定义恢复成默认值，
//     用户对任务定义的修改和任务历史一并消失
func (m *windowsManager) Start() error {
	if err := m.ensureInstalled(); err != nil {
		return err
	}
	m.log.Info("启动已安装的 Windows 计划任务（不重建定义）", "task", WindowsTaskName)
	if out, cerr := m.run("schtasks", "/Change", "/TN", WindowsTaskName, "/Enable"); cerr != nil {
		m.log.Error("解除任务停用失败", "task", WindowsTaskName,
			"cause", cerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("解除任务停用失败: %s（%w）", strings.TrimSpace(string(out)), cerr)
	}
	if out, rerr := m.run("schtasks", "/Run", "/TN", WindowsTaskName); rerr != nil {
		m.log.Error("启动计划任务失败", "task", WindowsTaskName,
			"cause", rerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("启动计划任务失败: %s（%w）", strings.TrimSpace(string(out)), rerr)
	}
	return m.waitRunning("Windows 计划任务已启动", "")
}

// Stop 停止计划任务并停用它，直到显式 Start。
//
// 返回：错误——任务不存在（ErrNotInstalled）、/Change /Disable 失败、
// 或复核窗口内它仍在跑
//
// 注意：
//   - **/Change /Disable 必须在 /End 之前，顺序承重。** 任务的 TimeTrigger
//     每分钟重复触发一次（等价于 systemd 的 Restart=always）；先 /End 的话
//     它会在 60 秒内被重新拉起，「停住」根本不成立。先掐触发源，再回收进程
//   - 用 /End 而不是按镜像名杀：本实现的任务动作进程直接就是 handoff.exe
//     （不套 cmd.exe），/End 精确命中；而 agentd 与操作者正在敲的 handoff CLI
//     同镜像名，按名字杀会连自己一起杀掉
//   - agentd 若是手工起的（不由本任务拉起），/End 杀不到它——那是诚实的结果：
//     stop 停的是托管，手工起的进程不归管理器处置
func (m *windowsManager) Stop() error {
	if err := m.ensureInstalled(); err != nil {
		return err
	}
	m.log.Info("停用并停止 Windows 计划任务", "task", WindowsTaskName)
	if out, cerr := m.run("schtasks", "/Change", "/TN", WindowsTaskName, "/Disable"); cerr != nil {
		m.log.Error("停用计划任务失败", "task", WindowsTaskName,
			"cause", cerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("停用计划任务失败: %s（%w）", strings.TrimSpace(string(out)), cerr)
	}
	if out, eerr := m.run("schtasks", "/End", "/TN", WindowsTaskName); eerr != nil {
		// 本来就没在跑时 /End 必然报错，这不是失败
		m.log.Debug("停止任务报错（本来就没在跑时属正常）",
			"output", strings.TrimSpace(string(out)))
	}
	return m.waitStopped("Windows 计划任务已停止并停用")
}

// Restart 就地重启计划任务，不改动任务定义。
//
// 返回：错误——任务不存在（ErrNotInstalled）、停不下来、起不来、
// 或复核窗口内没见进程存活
//
// 注意：
//   - 先 /End 并**复核真的停下来**，再 /Run。不复核就 /Run 的话，计划任务
//     服务可能因为「上一个实例还在」而忽略这次启动请求，于是「什么都没发生」
//     被报成「已重启」
//   - 被 Stop 停住时走 Start：/Run 对停用的任务会被拒，而用户敲 restart
//     要的是它起来（与 Manager.Restart 的约定一致）
func (m *windowsManager) Restart() error {
	if err := m.ensureInstalled(); err != nil {
		return err
	}
	st, serr := m.Status()
	if serr == nil && (st.Disabled || !st.Running) {
		m.log.Info("重启时发现任务已停用或未在运行，改为启动",
			"task", WindowsTaskName, "disabled", st.Disabled, "running", st.Running)
		return m.Start()
	}
	m.log.Info("重启 Windows 计划任务", "task", WindowsTaskName)
	if out, eerr := m.run("schtasks", "/End", "/TN", WindowsTaskName); eerr != nil {
		m.log.Error("停止计划任务失败", "task", WindowsTaskName,
			"cause", eerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("停止计划任务失败: %s（%w）", strings.TrimSpace(string(out)), eerr)
	}
	if err := m.waitStopped("重启中：旧实例已停止"); err != nil {
		return err
	}
	if out, rerr := m.run("schtasks", "/Run", "/TN", WindowsTaskName); rerr != nil {
		m.log.Error("重启中启动计划任务失败", "task", WindowsTaskName,
			"cause", rerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("重启中启动计划任务失败: %s（%w）", strings.TrimSpace(string(out)), rerr)
	}
	return m.waitRunning("Windows 计划任务已重启", "")
}
```

- [ ] **Step 5: 复核日志覆盖**

同前两个 task 的清单。额外确认 `waitStopped` 的 `okMsg` 在三个调用点各不相同（Stop / Restart 的两处），否则走查时分不清是哪一步停的。

- [ ] **Step 6: 跑测试**

```bash
gofmt -l ./internal/service && go test ./internal/service/ -run TestWindows -v && go test ./internal/service/
```

Expected: `gofmt -l` 无输出；全部 PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/service/windows.go internal/service/windows_test.go
git commit -m "feat(service): schtasks 的 start/stop/restart

stop 先 /Change /Disable 再 /End：任务每分钟重复触发，先 /End 的话
60 秒内就被拉回来了。Disabled 判据取任务 XML 的 <Enabled>，不取会
本地化的状态列。真机复核留待下一次 Windows 走查。"
```

---

### Task 5: 三个子命令与 status 输出

**Files:**
- Modify: `cmd/service.go`
- Test: `cmd/service_test.go`

**Interfaces:**
- Consumes: `service.Manager` 的 `Start()` / `Stop()` / `Restart()`、`service.Status.Disabled`、`service.ErrNotInstalled`
- Produces: cobra 命令 `serviceStartCmd` / `serviceStopCmd` / `serviceRestartCmd`，均已注册到 `serviceCmd`

- [ ] **Step 1: 写失败的测试**

在 `cmd/service_test.go` 追加：

```go
// start 调 Manager.Start 并报出结果。
func TestServiceStart(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if !f.started {
		t.Error("start 必须调 Manager.Start")
	}
	if !strings.Contains(out, "已启动") {
		t.Errorf("输出应报「已启动」，得到:\n%s", out)
	}
}

// stop 必须把「它不会自己回来」这句打出来。
//
// why：这是形态变化。不说的话，用户下次发现本机派不了活时不会想到
// 是自己停的——那正是 install 打「Ctrl-C 停不掉它」要避免的同一类坑。
func TestServiceStopWarnsItWontComeBack(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "stop")
	if err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if !f.stopped {
		t.Error("stop 必须调 Manager.Stop")
	}
	for _, want := range []string{"不会自己回来", "重启机器", "handoff service start"} {
		if !strings.Contains(out, want) {
			t.Errorf("stop 的输出必须含 %q（形态变化要说清楚），得到:\n%s", want, out)
		}
	}
}

// restart 调 Manager.Restart 并报出结果。
func TestServiceRestart(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "restart")
	if err != nil {
		t.Fatalf("restart: %v\n%s", err, out)
	}
	if !f.restarted {
		t.Error("restart 必须调 Manager.Restart")
	}
	if !strings.Contains(out, "已重启") {
		t.Errorf("输出应报「已重启」，得到:\n%s", out)
	}
}

// 未安装时报错必须直接给出 install，而不是把底层原文原样抛给用户。
func TestServiceLifecycleNotInstalledPointsAtInstall(t *testing.T) {
	for _, sub := range []string{"start", "stop", "restart"} {
		t.Run(sub, func(t *testing.T) {
			f := &fakeManager{
				startErr:   service.ErrNotInstalled,
				stopErr:    service.ErrNotInstalled,
				restartErr: service.ErrNotInstalled,
			}
			withFakeManager(t, f)
			out, err := runService(t, writeStatusConfig(t), sub)
			if err == nil {
				t.Fatal("未安装时应报错")
			}
			combined := out + err.Error()
			if !strings.Contains(combined, "handoff service install") {
				t.Errorf("未安装的处置必须是 install，得到:\n%s", combined)
			}
		})
	}
}

// status 要把「被停用」单独报出来。
//
// why：现状「已安装但未运行」的处置是「看日志找原因，或 install 重装」。
// 被 stop 停住时那条是错的——会把用户支去重装一个本来好好的单元。
func TestServiceStatusReportsDisabled(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Disabled: true}}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "已停止") {
		t.Errorf("被停用时应报「已停止」，得到:\n%s", out)
	}
	if !strings.Contains(out, "handoff service start") {
		t.Errorf("被停用的处置是 start，得到:\n%s", out)
	}
	if strings.Contains(out, "重装") {
		t.Errorf("被停用时不得建议重装（那是崩溃场景的处置），得到:\n%s", out)
	}
}

// 「在跑但已停用」是 stop 半途失败留下的真实状态，不能被报成一切正常。
func TestServiceStatusReportsRunningButDisabled(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Running: true, Disabled: true}}
	withFakeManager(t, f)
	out, err := runService(t, writeStatusConfig(t), "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "已停用") {
		t.Errorf("在跑但已停用时必须点出「已停用」，否则重启机器后它不会回来而用户毫不知情，得到:\n%s", out)
	}
}
```

若 `cmd/service_test.go` 里还没有 `writeTestConfig` 这个 helper，按文件里既有用例构造配置路径的方式调用（用文件里实际存在的写法，不要新造）。

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./cmd/ -run TestService -v
```

Expected: 三个子命令不存在，cobra 报 unknown command；status 的两个新用例 FAIL。

- [ ] **Step 3: 加三个子命令**

`cmd/service.go`，在 `serviceStatusCmd` 之前加：

```go
// lifecycleManager 取一个 Manager，是三个生命周期子命令的共用前半段。
//
// 返回：平台对应的 Manager；构造失败时返回错误（平台不支持等）
func lifecycleManager() (service.Manager, error) {
	return newServiceManager(slog.Default())
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "启动已托管但被停止的 agentd（恢复自动拉起）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := lifecycleManager()
		if err != nil {
			return err
		}
		if err := m.Start(); err != nil {
			return fmt.Errorf("启动服务失败: %w", err)
		}
		unit, _ := m.UnitPath()
		fmt.Fprintf(cmd.OutOrStdout(), "已启动   %s   %s\n", m.Kind(), unit)
		fmt.Fprintf(cmd.OutOrStdout(), "         agentd 已恢复自动拉起与开机自启\n")
		return nil
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "停止 agentd，并关掉自动拉起（直到 handoff service start）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := lifecycleManager()
		if err != nil {
			return err
		}
		if err := m.Stop(); err != nil {
			return fmt.Errorf("停止服务失败: %w", err)
		}
		out := cmd.OutOrStdout()
		unit, _ := m.UnitPath()
		fmt.Fprintf(out, "已停止   %s   %s\n", m.Kind(), unit)
		// 形态变化必须说清楚：不说的话，用户下次发现本机派不了活时
		// 不会想到是自己停的。与 install 打「Ctrl-C 停不掉它」同一类提示
		fmt.Fprintf(out, "\n注意     agentd 不会自己回来，重启机器也不会。\n")
		fmt.Fprintf(out, "         恢复用 handoff service start。\n")
		return nil
	},
}

var serviceRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "重启 agentd（改完配置让它生效）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := lifecycleManager()
		if err != nil {
			return err
		}
		if err := m.Restart(); err != nil {
			return fmt.Errorf("重启服务失败: %w", err)
		}
		unit, _ := m.UnitPath()
		fmt.Fprintf(cmd.OutOrStdout(), "已重启   %s   %s\n", m.Kind(), unit)
		return nil
	},
}
```

`init()` 里改成：

```go
	serviceCmd.AddCommand(serviceInstallCmd, serviceUninstallCmd, serviceStatusCmd,
		serviceStartCmd, serviceStopCmd, serviceRestartCmd)
```

`cmd/service.go` 的文件头注释同步补上新职责：

```go
//   - start / stop / restart：改已装单元的运行状态，不改单元定义本身
```

并把边界那条「不启动/停止 agentd 进程本身」改成：

```go
//   - 不替代进程管理器：start/stop/restart 是对管理器下指令，不自己 fork
//     或 kill agentd；单元没装时一律硬拒，不代为 install
```

- [ ] **Step 4: 改 status 的分档**

把 `serviceStatusCmd` 里的 `switch` 换成：

```go
		switch {
		case st.Installed && st.Running && st.Disabled:
			// stop 的 disable 成功、停进程失败会留下这个状态。报成「已托管」
			// 会让用户以为一切正常，而重启机器后它不会回来
			fmt.Fprintf(out, "运行中但已停用  %s   %s\n", m.Kind(), unit)
			fmt.Fprintf(out, "处置          它还在跑，但重启机器后不会回来；handoff service start 恢复\n")
		case st.Installed && st.Running:
			fmt.Fprintf(out, "已托管        %s   %s\n", m.Kind(), unit)
		case st.Installed && st.Disabled:
			// 与「装了没跑」分开：这是被 handoff service stop 停住的，
			// 处置是 start，不是去查日志、更不是重装
			fmt.Fprintf(out, "已停止        %s   %s\n", m.Kind(), unit)
			fmt.Fprintf(out, "处置          handoff service start\n")
		case st.Installed:
			// 装了没跑是一个真实且常见的状态（崩溃循环），必须与「没装」
			// 和「被停住」都分开报
			fmt.Fprintf(out, "已安装但未运行  %s   %s\n", m.Kind(), unit)
			fmt.Fprintf(out, "处置          看日志找原因，或 handoff service install 重装\n")
		default:
			fmt.Fprintf(out, "未托管        %s 上没有 handoff 的服务单元\n", m.Kind())
			fmt.Fprintf(out, "处置          handoff service install\n")
		}
```

- [ ] **Step 5: 让未安装的报错指向 install**

三个 RunE 的错误包装改成走一个共用函数：

```go
// lifecycleErr 把生命周期操作的失败包成面向用户的报文。
//
// 参数：
//   - action: 「启动」「停止」「重启」
//   - err: 底层错误
//
// 返回：包装后的错误。单元没装时把处置直接写进报文——那是与「装了但操作
// 失败」完全不同的处置（去 install，而不是去查日志）
func lifecycleErr(action string, err error) error {
	if errors.Is(err, service.ErrNotInstalled) {
		return fmt.Errorf("%s服务失败：本机还没有 handoff 的服务单元，先跑 handoff service install（%w）", action, err)
	}
	return fmt.Errorf("%s服务失败: %w", action, err)
}
```

三处 `return fmt.Errorf("启动服务失败: %w", err)` 等改成 `return lifecycleErr("启动", err)` / `lifecycleErr("停止", err)` / `lifecycleErr("重启", err)`。import 补 `"errors"`。

- [ ] **Step 6: 加日志**

三个 RunE 在调 Manager 之前各打一条 Info（`slog.Default().Info("执行服务生命周期命令", "action", "restart", "kind", m.Kind())`），失败分支各打一条 Error 带 cause。**面向用户的 `fmt.Fprintf` 不算日志**，两者都要有。

- [ ] **Step 7: 跑测试**

```bash
gofmt -l ./cmd && go test ./cmd/ -run TestService -v && go test ./cmd/
```

Expected: `gofmt -l` 无输出；全部 PASS。

- [ ] **Step 8: 提交**

```bash
git add cmd/service.go cmd/service_test.go
git commit -m "feat(cmd): handoff service start/stop/restart

status 从三档扩到五档：把「被 stop 停住」和「在跑但已停用」跟
「崩了没起来」分开——原来那句「或 install 重装」在停用场景是错的
处置。stop 必须打「它不会自己回来」。"
```

---

### Task 6: 桌面壳不再复活被停住的 agentd

**Files:**
- Modify: `desktop/internal/shell/lifecycle.go`
- Modify: `desktop/main.go:12-13`（文件头注释）
- Test: `desktop/internal/shell/lifecycle_test.go`

**Interfaces:**
- Consumes: `service.Status.Disabled`（Task 1）
- Produces: 无新导出符号；`EnsureRunning` 行为变更

- [ ] **Step 1: 写失败的测试**

在 `desktop/internal/shell/lifecycle_test.go` 追加：

```go
// 被 handoff service stop 显式停用时，绝不自愈。
//
// why 承重：EnsureRunning 的既有逻辑是「没在跑 → Start，Start 失败 →
// Install 自愈」。launchd 上 stop 做过 bootout，Start 会失败，于是回落
// Install 把用户刚显式停掉的 agentd 装回来跑起来——stop 这个动作在装了
// 桌面壳的机器上当场失效。
func TestEnsureRunningRespectsDisabled(t *testing.T) {
	f := &fakeManager{status: service.Status{Installed: true, Running: false, Disabled: true}}
	withManager(t, f, nil)
	if err := EnsureRunning(slog.Default(), service.Spec{BinPath: "/opt/bin/handoff"}); err != nil {
		t.Fatalf("被停用不是错误，EnsureRunning 应正常返回: %v", err)
	}
	if f.started {
		t.Error("被停用时不得调 Start")
	}
	if f.installed {
		t.Error("被停用时不得调 Install——那会把用户显式停掉的 agentd 装回来")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./desktop/internal/shell/ -run TestEnsureRunningRespectsDisabled -v
```

Expected: FAIL——`f.started` 或 `f.installed` 为 true。

- [ ] **Step 3: 改 EnsureRunning**

在 `if st.Running { ... return nil }` 之后、`log.Info("agentd 未在运行，准备托管拉起", ...)` 之前插入：

```go
	// 被 handoff service stop 显式停用：不自愈。
	//
	// 承重的理由：下面那段「Start 失败就 Install 自愈」在 launchd 上会把
	// 被 bootout 的单元重新 bootstrap 起来——用户明确停掉的东西又被拉回来，
	// stop 这个动作在装了桌面壳的机器上当场失效。停用是一个显式意图，
	// 不是一个待修复的故障。
	if st.Disabled {
		log.Info("agentd 已被显式停用，不自愈",
			"kind", m.Kind(), "installed", st.Installed, "detail", st.Detail)
		return nil
	}
```

同时把文件头的边界注释补上第四条：

```go
//   - **绝不复活被显式停用的 agentd**。handoff service stop 是显式意图，
//     不是待修复的故障；把它当故障自愈，stop 在装了薄壳的机器上就失效了
```

- [ ] **Step 4: 订正 desktop/main.go 的文件头注释**

把这两行：

```go
//   - 托盘不提供「停止 agentd」。**不做「停止 agentd」**：
//     service.Manager 没有 Stop，用 Uninstall 冒充是错的语义
```

改成：

```go
//   - 托盘不提供「停止 agentd」。这是产品决定而非能力缺失：薄壳是 agentd
//     的观察窗，不是它的开关；要停用 CLI 的 handoff service stop
```

**why 必须改**：原注释给出的理由是「Manager 没有 Stop」。Task 1 之后它有了，那条理由不成立，留着会误导下一个人以为「加了 Stop 就该给托盘加按钮」。

- [ ] **Step 5: 跑测试**

```bash
gofmt -l ./desktop && go build ./desktop/... && go test ./desktop/...
```

Expected: `gofmt -l` 无输出；全部 PASS（既有的「已在运行不做事」「装了就只 Start」用例不得回归）。

- [ ] **Step 6: 提交**

```bash
git add desktop/internal/shell/lifecycle.go desktop/internal/shell/lifecycle_test.go desktop/main.go
git commit -m "fix(desktop): 不再复活被显式停用的 agentd

EnsureRunning 的「Start 失败就 Install 自愈」在 launchd 上会把
被 bootout 的单元重新 bootstrap——用户 stop 掉的东西又回来了。
顺带订正 main.go 里那条「Manager 没有 Stop」的注释，它已不成立。"
```

---

### Task 7: 文档

**Files:**
- Modify: `README.zh-CN.md:89`、`README.zh-CN.md:204`
- Modify: `README.md:100`、`README.md:348`
- Modify: `CHANGELOG.md`（`## [Unreleased]` 小节）

**Interfaces:**
- Consumes: Task 5 定稿的命令名与输出文案
- Produces: 无代码符号

- [ ] **Step 1: 改 README.zh-CN.md 的托管小节**

第 85-86 行的代码块改成：

```bash
handoff service install
handoff service status
```

保持不变；第 89 行那句里的最后一句改成：

> 托管后 Ctrl-C 停不掉它（会被自动拉回）：改完配置让它生效用 `handoff service restart`，临时停掉用 `handoff service stop`（会一直停到 `handoff service start`，重启机器也不会自己回来），彻底摘掉托管用 `handoff service uninstall`。

- [ ] **Step 2: 改 README.zh-CN.md 的命令表**

第 204 行整行替换为：

```
| `handoff service install\|uninstall\|status` | agentd 交给 launchd / systemd 托管 | — |
| `handoff service start\|stop\|restart` | 起停/重启已托管的 agentd（`stop` 会一直停到 `start`） | — |
```

- [ ] **Step 3: 改 README.md 的对应两处**

第 96-100 行那段的最后一句改成：

> Once managed, Ctrl-C won't kill it (it gets pulled right back up): to make a config change take effect, use `handoff service restart`; to stop it for a while, use `handoff service stop` (it stays down until `handoff service start` — a reboot won't bring it back); to remove the management entirely, use `handoff service uninstall`.

第 348 行整行替换为：

```
| `handoff service install\|uninstall\|status` | Put agentd under launchd / systemd management | — |
| `handoff service start\|stop\|restart` | Start / stop / restart the managed agentd (`stop` stays down until `start`) | — |
```

- [ ] **Step 4: 加 CHANGELOG 条目**

把 `## [Unreleased]` 下的 `_（下一版的改动记在这里。）_` 换成：

```markdown
### 新增

- **`handoff service start` / `stop` / `restart`。** 改完配置想让 agentd
  生效，此前只能 kill 掉等进程管理器把它拉回来。现在 `handoff service restart`
  就地重启（发 SIGTERM 走 agentd 自己的优雅关停，不是硬砍），`stop` 停到你
  显式 `start` 为止——**包括重启机器也不会自己回来**，因为三个平台都配了
  「退出就拉起」，只杀进程根本停不住。三个命令都只对已安装的单元生效，
  没装时直接告诉你去 `handoff service install`，不代为安装。
- **`handoff service status` 认得「被停住」了。** 此前「装了没跑」只有一档，
  处置写的是「看日志找原因，或 install 重装」——被 `stop` 停住时那是错的
  处置。现在被停用会单独报一档，处置是 `handoff service start`。

### 修复

- **桌面薄壳不再复活被显式停掉的 agentd。** 薄壳启动时会自愈「装了没跑」的
  agentd，而 `stop` 之后正好长这样，于是你停掉的东西一开薄壳就回来了。
```

- [ ] **Step 5: 核对文档与实现一致**

逐条对照 Task 5 定稿的输出文案，确认 README 与 CHANGELOG 里写的命令名、行为描述与实现一致（尤其「重启机器也不会回来」这句——它是 `stop` 的核心承诺，写错就是骗人）。

- [ ] **Step 6: 提交**

```bash
git add README.md README.zh-CN.md CHANGELOG.md
git commit -m "docs: 记下 handoff service start/stop/restart"
```

---

## 收尾（不是一个 task，是审核者的验收清单）

跑完 7 个 task 后，在**本机 macOS** 上做一次真机走查——这几步会真的动本机 agentd，且需要驱动 handoff 自身，**不适合派发给 executor**：

```bash
go build -o /tmp/handoff-verify . && /tmp/handoff-verify service status
```

1. `service status` → 应报「已托管 launchd」
2. `service restart` → 应报「已重启」；`launchctl print gui/$(id -u)/dev.gosuper.handoff.agentd | grep pid` 前后对比，pid 必须变了
3. `service stop` → 应报「已停止」+「不会自己回来」；`service status` 应报「已停止」并建议 `start`
4. `launchctl print-disabled gui/$(id -u) | grep handoff` → 应出现且为 `disabled`
5. `service start` → 应报「已启动」；`service status` 回到「已托管」；`print-disabled` 里应变回 `enabled`

任何一步对不上，**先看 `~/.handoff/agentd.log` 与命令自身的 stderr**，不要靠猜改代码。

Linux（systemd）与 Windows（schtasks）侧本轮只有单测覆盖，真机复核留待下一次对应平台的走查。
