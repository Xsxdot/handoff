# B219 实现计划：负向权限用例的前提探针

> Spec：`docs/superpowers/specs/2026-08-23-b219-permission-precondition-helper.md`
>
> 卡号：B219；级别：L2。本计划只改测试辅助与测试代码，不改生产代码、运行身份、
> CI 拓扑或任何跨进程契约。

## 目标与冻结边界

将所有依赖文件 mode 位制造写/读失败的测试，统一改为「施加限制 → 直接探针 →
限制失效则恢复并 skip，权限拒绝则继续，其他探针错误则失败 → `t.Cleanup` 恢复」的
测试辅助。探针观察的是直接性质，不检查 euid，也不猜 `CAP_DAC_OVERRIDE`、ACL 或
平台实现。

本次收编的 mode 位站点是：

- `internal/client/cursordir_test.go` 的两个不可写目录用例；
- `internal/config/config_test.go` 的不可写配置文件用例；
- `internal/executor/grok/authsync_test.go` 的不可写 `.grok` 目录用例；
- `internal/client/cursor_layout_test.go` 的不可读游标文件用例；
- `internal/release/install_test.go` 的不可写安装目录用例。

`internal/prochost/fence_inherit_test.go:139-181` 保持原样：它的 `os.Geteuid()` 守卫
保护的是 `RLIMIT_NPROC` 的 `CAP_SYS_RESOURCE` 语义，不是文件 mode，不能塞进同一个
文件权限抽象。该边界已在台账记录。

### 基线事实与依赖取证

计划编写前已在当前基线真实执行：

- `id -u; go test ./internal/client ./internal/config ./internal/executor/grok ./internal/release -run 'TestCursorRootFallsBackToCwdWhenHomeUnwritable|TestCursorRootErrorNamesBothPaths|TestLoadStripUpdateDoesNotBlockOnSaveFailure|TestSyncAuthKeepsTaskCopyWhenWriteFails|TestReadCursorPermissionDeniedIsReported|TestActivateUnwritableDir' -count=1`：原始结果为 euid `0`，四包均 `ok`。
- 同四包的 `-v` 复核：`TestReadCursorPermissionDeniedIsReported` 在 `cursor_layout_test.go:77`
  SKIP，`TestActivateUnwritableDir` 在 `install_test.go:349` SKIP；四个恒红目标用例
  PASS。当前容器虽然 euid 为 0，但 `capsh --print` 的原始输出含
  `Current IAB: !cap_dac_override`，所以这不是目标 root+DAC_OVERRIDE 机器，也不是
  非 root 读数。
- `codegraph` 不可用：`command -v codegraph` 的原始输出为 `codegraph: command not found`；
  调用面已用 `rg`/`nl -ba` 复核，覆盖债登记在 `docs/ledger-b219.md`。

实现依赖的标准库行为已从本机 `/usr/local/go`、`go1.26.1` 取证：

- `testing.TB` 的精确接口及 `Cleanup`/`Skipf` 在 `/usr/local/go/src/testing/testing.go:889`、
  `:1238-1242`、`:1293-1299`；`Cleanup` 按后注册先执行，故调用点在 `t.TempDir()` 之后
  调 helper 时，恢复先于临时目录删除。
- `os.Chmod` 的声明及平台行为在 `/usr/local/go/src/os/file.go:644-647`；`go doc os.Chmod`
  明确 Windows 只使用 owner-writable 位，因此 Windows 上目录 mode 无法表达目录不可写，
  必须依赖探针成功后的 skip。
- `os.CreateTemp` 在 `/usr/local/go/src/os/tempfile.go:35-39`，创建并打开文件且由调用者
  删除；目录写探针必须在成功后关闭并删除探针文件。
- `os.OpenFile` 在 `/usr/local/go/src/os/file.go:410-413`；文件写探针使用
  `os.O_WRONLY`、不使用 `os.O_TRUNC`，避免探针破坏被测文件内容。
- `fs.ErrPermission` 在 `/usr/local/go/src/io/fs/fs.go:153-162`，`errors.Is` 的使用说明在
  `/usr/local/go/src/os/error.go:94-101`；只有该类拒绝才表示「限制生效」，`ENOENT`、
  `EROFS` 等无关错误不能静默 skip。

## Interfaces

### Task 1 Produces

新增包 `github.com/Xsxdot/handoff/internal/testperm`，导出两个且仅两个供测试调用的
入口，精确签名如下：

```go
package testperm

func DenyWrite(t testing.TB, path string)
func DenyRead(t testing.TB, path string)
```

语义：`DenyWrite` 接受已存在的目录或普通文件；目录以创建临时文件探针，文件以
`os.OpenFile(path, os.O_WRONLY, 0)` 探针。`DenyRead` 接受已存在的普通文件，以
`os.Open(path)` 探针。两者均保存原始 permission bits、清除相应位、注册恢复清理。

包内唯一接缝为以下纯决策函数；它不做 IO、不调用 `testing.TB`、不记录日志：

```go
type probeAction uint8

const (
	probeContinue probeAction = iota
	probeSkip
	probeFatal
)

type probeDecision struct {
	action              probeAction
	restoreBeforeAction bool
	message             string
}

func decideProbe(operation, path string, probeErr error) probeDecision
```

`probeErr == nil` 产出 `probeSkip` 且 `restoreBeforeAction == true`；
`errors.Is(probeErr, fs.ErrPermission)` 产出 `probeContinue` 且由已注册 cleanup 恢复；
其他错误产出 `probeFatal` 且 `restoreBeforeAction == true`。这样不会把探针自身的
`ENOENT`、关闭失败或其他无关错误误报成环境 skip。

### Task 1 Consumes

- `testing.TB` 的 `Helper`, `Cleanup`, `Skipf`, `Fatalf`, `Errorf` 精确方法集；
- `os.Stat`, `os.Chmod`, `os.CreateTemp`, `os.OpenFile`, `os.Open`, `os.Remove`；
- `errors.Is(error, fs.ErrPermission)`；
- 项目现有的标准 `log/slog` 默认 logger（`slog.Default()`），不使用 `print`。

### Task 2 Produces

不新增导出接口；保留以下既有测试函数名与签名，只替换前提构造代码：

```go
func TestCursorRootFallsBackToCwdWhenHomeUnwritable(t *testing.T)
func TestCursorRootErrorNamesBothPaths(t *testing.T)
func TestReadCursorPermissionDeniedIsReported(t *testing.T)
func TestLoadStripUpdateDoesNotBlockOnSaveFailure(t *testing.T)
func TestSyncAuthKeepsTaskCopyWhenWriteFails(t *testing.T)
func TestActivateUnwritableDir(t *testing.T)
```

### Task 2 Consumes

```go
func DenyWrite(t testing.TB, path string)
func DenyRead(t testing.TB, path string)
```

调用表达式固定为 `testperm.DenyWrite(t, path)` 或 `testperm.DenyRead(t, path)`，不在
调用点自行传 mode、euid 或 skip 文案。

具体消费映射：

| 调用点 | helper | path |
|---|---|---|
| `internal/client/cursordir_test.go:43` | `DenyWrite` | `filepath.Join(home, ".handoff")` |
| `internal/client/cursordir_test.go:82` | `DenyWrite` | `filepath.Join(home, ".handoff")` |
| `internal/client/cursordir_test.go:87` | `DenyWrite` | `filepath.Join(cwd, ".handoff")` |
| `internal/client/cursor_layout_test.go:89` | `DenyRead` | `p` |
| `internal/config/config_test.go:476` | `DenyWrite` | `p` |
| `internal/executor/grok/authsync_test.go:255` | `DenyWrite` | `grokDir` |
| `internal/release/install_test.go:344` | `DenyWrite` | `dir` |

## Task 1：新增 `internal/testperm` 权限前提 helper

### Files

- Create: `internal/testperm/permission.go`
- Create: `internal/testperm/permission_test.go`

### 测试范围声明

本 task 只跑 `go test ./internal/testperm -count=1`；不跑全量测试，不触及其他包。

### Step 1：基线判据先跑

在写新文件前执行：

```sh
go test ./internal/client ./internal/config ./internal/executor/grok ./internal/release \
  -run 'TestCursorRootFallsBackToCwdWhenHomeUnwritable|TestCursorRootErrorNamesBothPaths|TestLoadStripUpdateDoesNotBlockOnSaveFailure|TestSyncAuthKeepsTaskCopyWhenWriteFails|TestReadCursorPermissionDeniedIsReported|TestActivateUnwritableDir' \
  -count=1 -v
```

预期是复核台账中的基线形态：在当前无 DAC_OVERRIDE 沙箱里，四个目标断言 PASS、
已有两个 euid 守卫 SKIP；在目标 root+DAC_OVERRIDE 执行机上，当前缺 helper 的四个
目标用例会因写探针未被挡住而进入错误断言。若实际输出不同，停止实现并把原始输出
追加台账，不能凭预期改写判据。

### Step 2：先写失败测试并跑红

先创建 `internal/testperm/permission_test.go`，完整内容如下：

```go
// Package testperm 的测试覆盖权限探针三态决策和恢复时序。
//
// 职责：证明限制生效、限制失效、无关探针错误分别走继续、skip、fatal。
// 边界：不检查 euid，不依赖特定内核能力；恢复时序通过子测试和真实文件 mode 验证。
package testperm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecideProbe(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantAction  probeAction
		wantRestore bool
		wantText    string
	}{
		{name: "write succeeded", err: nil, wantAction: probeSkip, wantRestore: true, wantText: "探针成功"},
		{name: "permission denied", err: &fs.PathError{Op: "open", Path: "p", Err: fs.ErrPermission}, wantAction: probeContinue, wantRestore: false, wantText: "限制已生效"},
		{name: "unrelated error", err: errors.New("file disappeared"), wantAction: probeFatal, wantRestore: true, wantText: "无关错误"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideProbe("写", "/tmp/probe", tt.err)
			if got.action != tt.wantAction {
				t.Fatalf("action = %d, want %d", got.action, tt.wantAction)
			}
			if got.restoreBeforeAction != tt.wantRestore {
				t.Fatalf("restoreBeforeAction = %v, want %v", got.restoreBeforeAction, tt.wantRestore)
			}
			if !strings.Contains(got.message, tt.wantText) {
				t.Fatalf("message = %q, want substring %q", got.message, tt.wantText)
			}
		})
	}
}

func TestApplyProbeRestoresBeforeSkip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe-file")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !t.Run("probe succeeded", func(t *testing.T) {
		apply(t, path, 0o600, 0o400, "写", func() error { return nil })
	}) {
		t.Fatal("probe succeeded 分支不应失败")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("skip 前必须恢复原 mode，got %#o", got)
	}
}

func TestApplyProbeKeepsRestrictionUntilCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe-file")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !t.Run("permission denied", func(t *testing.T) {
		apply(t, path, 0o600, 0o400, "写", func() error { return fs.ErrPermission })
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o400 {
			t.Fatalf("限制生效分支应保持受限 mode，got %#o", got)
		}
	}) {
		t.Fatal("permission denied 分支不应失败")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("子测试 cleanup 后必须恢复原 mode，got %#o", got)
	}
}
```

运行 `go test ./internal/testperm -run 'TestDecideProbe|TestApplyProbe' -count=1`；
预期为编译失败，原始错误以实际 Go 输出为准，不能写成未执行的 PASS。

### Step 3：写最小实现并加关键节点日志

创建 `internal/testperm/permission.go`，完整内容如下：

```go
// Package testperm 提供只供测试使用的文件权限前提探针。
//
// 职责：施加读/写限制，直接尝试对应操作，并在当前机器无法表达限制时 skip。
// 边界：只服务测试辅助，不参与生产运行时；不检查 euid，不改变运行身份，不注入生产错误。
package testperm

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"testing"
)

type probeAction uint8

const (
	probeContinue probeAction = iota
	probeSkip
	probeFatal
)

type probeDecision struct {
	action              probeAction
	restoreBeforeAction bool
	message             string
}

// DenyWrite 清除 path 的全部写 permission bits，并用一次真实写探针确认限制。
//
// 参数：t 是测试句柄；path 是已存在的目录或普通文件。
// 返回：无；限制生效则返回给调用点继续执行，限制失效则恢复现场并 skip。
// 注意：只有 errors.Is(err, fs.ErrPermission) 才算限制生效；其他探针错误会使测试失败。
func DenyWrite(t testing.TB, path string) {
	t.Helper()
	info := targetInfo(t, path, "写")
	restricted := info.Mode().Perm() &^ 0o222
	if info.IsDir() {
		apply(t, path, info.Mode().Perm(), restricted, "写", func() error {
			f, err := os.CreateTemp(path, ".handoff-permission-probe-*")
			if err != nil {
				return err
			}
			name := f.Name()
			closeErr := f.Close()
			removeErr := os.Remove(name)
			if closeErr != nil {
				return closeErr
			}
			return removeErr
		})
		return
	}
	apply(t, path, info.Mode().Perm(), restricted, "写", func() error {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

// DenyRead 清除 path 的全部读 permission bits，并用一次真实读探针确认限制。
//
// 参数：t 是测试句柄；path 是已存在的普通文件。
// 返回：无；限制生效则返回给调用点继续执行，限制失效则恢复现场并 skip。
// 注意：目录不是本入口的目标；无关探针错误不会被转换成 skip。
func DenyRead(t testing.TB, path string) {
	t.Helper()
	info := targetInfo(t, path, "读")
	if info.IsDir() {
		t.Fatalf("读权限前提目标必须是文件，path=%q", path)
		return
	}
	restricted := info.Mode().Perm() &^ 0o444
	apply(t, path, info.Mode().Perm(), restricted, "读", func() error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

func targetInfo(t testing.TB, path, operation string) os.FileInfo {
	logger := slog.Default()
	logger.Info("测试权限前提入口", "operation", operation, "path", path)
	logger.Debug("读取测试权限目标状态", "operation", operation, "path", path)
	info, err := os.Stat(path)
	if err != nil {
		logger.Error("读取测试权限目标状态失败", "operation", operation, "path", path, "err", err)
		t.Fatalf("权限前提目标不可用（operation=%s path=%q）: %v", operation, path, err)
		return nil
	}
	logger.Debug("测试权限目标状态已读取", "operation", operation, "path", path,
		"mode", info.Mode().Perm(), "is_dir", info.IsDir())
	return info
}

func apply(t testing.TB, path string, original, restricted os.FileMode, operation string,
	probe func() error) {
	logger := slog.Default()
	logger.Debug("施加测试权限限制前", "operation", operation, "path", path,
		"original_mode", original, "restricted_mode", restricted)
	if err := os.Chmod(path, restricted); err != nil {
		logger.Error("施加测试权限限制失败", "operation", operation, "path", path,
			"restricted_mode", restricted, "err", err)
		t.Fatalf("权限前提无法设置（operation=%s path=%q mode=%#o）: %v",
			operation, path, restricted, err)
		return
	}
	logger.Info("测试权限限制已施加", "operation", operation, "path", path,
		"restricted_mode", restricted)

	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		logger.Debug("恢复测试权限限制前", "operation", operation, "path", path,
			"original_mode", original)
		if err := os.Chmod(path, original); err != nil {
			logger.Error("恢复测试权限限制失败", "operation", operation, "path", path,
				"original_mode", original, "err", err)
			t.Errorf("恢复权限前提失败（operation=%s path=%q mode=%#o）: %v",
				operation, path, original, err)
			return
		}
		logger.Info("测试权限限制已恢复", "operation", operation, "path", path,
			"original_mode", original)
	}
	t.Cleanup(restore)

	logger.Debug("执行测试权限探针前", "operation", operation, "path", path)
	probeErr := probe()
	logger.Debug("执行测试权限探针后", "operation", operation, "path", path, "err", probeErr)
	decision := decideProbe(operation, path, probeErr)
	switch decision.action {
	case probeContinue:
		logger.Info("测试权限限制已被探针证实", "operation", operation, "path", path,
			"probe_err", probeErr)
	case probeSkip:
		logger.Warn("当前机器无法表达测试权限前提", "operation", operation, "path", path)
		restore()
		t.Skipf("%s", decision.message)
	case probeFatal:
		logger.Error("测试权限探针出现无关错误", "operation", operation, "path", path,
			"err", probeErr)
		restore()
		t.Fatalf("%s", decision.message)
	}
}

func decideProbe(operation, path string, probeErr error) probeDecision {
	if probeErr == nil {
		return probeDecision{
			action:              probeSkip,
			restoreBeforeAction: true,
			message: fmt.Sprintf("权限前提未成立：%s 探针成功，当前机器无法表达 path=%q 的限制；这不是禁用用例",
				operation, path),
		}
	}
	if errors.Is(probeErr, fs.ErrPermission) {
		return probeDecision{
			action:  probeContinue,
			message: fmt.Sprintf("权限前提已成立：%s 探针被拒绝，path=%q", operation, path),
		}
	}
	return probeDecision{
		action:              probeFatal,
		restoreBeforeAction: true,
		message: fmt.Sprintf("权限探针出现无关错误：operation=%s path=%q err=%v",
			operation, path, probeErr),
	}
}
```

实现步骤紧接着执行：

1. 运行 `gofmt -w internal/testperm/permission.go internal/testperm/permission_test.go`；
2. 运行 `go test ./internal/testperm -count=1`，必须真实得到 `ok`；
3. 运行 `git diff --check`，无输出才进入下一步；
4. 将 Task 1 文件提交为独立 commit，commit 前运行
   `git status --short internal/testperm docs/ledger-b219.md`，原始输出追加台账。

## Task 2：把六个 mode 前提站点切到 helper

### Files

- Modify: `internal/client/cursordir_test.go`
- Modify: `internal/client/cursor_layout_test.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/executor/grok/authsync_test.go`
- Modify: `internal/release/install_test.go`
- Do not modify: `internal/prochost/fence_inherit_test.go`

### 测试范围声明

本 task 只跑下列五个触及包的定向测试；不把全量 `go test ./...` 归入本 task：

```sh
go test ./internal/client -run 'TestCursorRootFallsBackToCwdWhenHomeUnwritable|TestCursorRootErrorNamesBothPaths|TestReadCursorPermissionDeniedIsReported' -count=1 -v
go test ./internal/config -run '^TestLoadStripUpdateDoesNotBlockOnSaveFailure$' -count=1 -v
go test ./internal/executor/grok -run '^TestSyncAuthKeepsTaskCopyWhenWriteFails$' -count=1 -v
go test ./internal/release -run '^TestActivateUnwritableDir$' -count=1 -v
```

每个命令的真实输出必须显示 PASS 或 helper 给出的环境事实 SKIP；不得以固定的
`root`/非 root 预期代替探针结果。

### Step 1：在基线重跑判据并保存原始输出

先执行上面四条命令的基线版本（当前文件仍是旧写法），将原始输出追加
`docs/ledger-b219.md`。基线复核要点是：当前已知的两个 euid 守卫仍可能 SKIP，四个
恒红用例在当前无 DAC_OVERRIDE 沙箱中 PASS；目标 root+DAC_OVERRIDE 机器的基线红法
由实际输出确定，不能在本计划中伪造。

### Step 2：替换调用点，保留既有断言

五个文件的 import 均只增加这一行：

```go
"github.com/Xsxdot/handoff/internal/testperm"
```

`internal/client/cursordir_test.go` 的两个前提构造替换为以下完整代码；保留各自
后续 `cursorRootDir` 断言不动：

```go
// TestCursorRootFallsBackToCwdWhenHomeUnwritable 保证 HOME 候选写失败时降级到 cwd。
func TestCursorRootFallsBackToCwdWhenHomeUnwritable(t *testing.T) {
	home := t.TempDir()
	homeHandoff := filepath.Join(home, ".handoff")
	if err := os.MkdirAll(homeHandoff, 0o700); err != nil {
		t.Fatal(err)
	}
	testperm.DenyWrite(t, homeHandoff)
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	t.Chdir(cwd)

	c := New("http://127.0.0.1:7777", "")
	got, err := c.cursorRootDir()
	if err != nil {
		t.Fatalf("cursorRootDir: %v", err)
	}
	want := filepath.Join(cwd, ".handoff", "cursors")
	if got != want {
		t.Fatalf("根 = %q, want %q（应降级到 cwd）", got, want)
	}
}

func TestCursorRootErrorNamesBothPaths(t *testing.T) {
	home := t.TempDir()
	homeHandoff := filepath.Join(home, ".handoff")
	if err := os.MkdirAll(homeHandoff, 0o700); err != nil {
		t.Fatal(err)
	}
	testperm.DenyWrite(t, homeHandoff)
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	cwdHandoff := filepath.Join(cwd, ".handoff")
	if err := os.MkdirAll(cwdHandoff, 0o700); err != nil {
		t.Fatal(err)
	}
	testperm.DenyWrite(t, cwdHandoff)
	t.Chdir(cwd)

	c := New("http://127.0.0.1:7777", "")
	_, err := c.cursorRootDir()
	if err == nil {
		t.Fatal("两处都不可写时必须报错，不得静默")
	}
	msg := err.Error()
	if !strings.Contains(msg, home) || !strings.Contains(msg, cwd) {
		t.Fatalf("错误必须同时点名两个候选路径，实际: %s", msg)
	}
}
```

`internal/client/cursor_layout_test.go` 的完整目标函数替换为：

```go
func TestReadCursorPermissionDeniedIsReported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	p, err := c.cursorPath("denied")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("42"), 0o600); err != nil {
		t.Fatal(err)
	}
	testperm.DenyRead(t, p)
	seq, reported := c.readCursorWithDiag("denied")
	if seq != 0 {
		t.Fatalf("读不了必须退回 0，got %d", seq)
	}
	if !reported {
		t.Fatal("权限被拒必须被报告，不得与「文件不存在」一样静默")
	}
}
```

`internal/config/config_test.go` 的完整目标函数替换为：

```go
	func TestLoadStripUpdateDoesNotBlockOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: tk\nupdate:\n  auto: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testperm.DenyWrite(t, p)
	if _, err := config.Load(p); err != nil {
		t.Fatalf("回写失败不得阻断启动: %v", err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "update") {
		t.Fatalf("回写应失败，磁盘上仍须留着 update 段:\n%s", body)
	}
	}
```

`internal/executor/grok/authsync_test.go` 的完整目标函数替换为：

```go
	func TestSyncAuthKeepsTaskCopyWhenWriteFails(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	grokDir := filepath.Dir(authPath)
	testperm.DenyWrite(t, grokDir)
	link := writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err == nil {
		t.Fatalf("写回失败应返回错误")
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("任务侧副本不该被删: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("写回失败时不应复位软链——那份副本可能是唯一的新凭据")
	}
	if m := markerIn(t, link); m != "task" {
		t.Errorf("写回失败时任务侧副本内容被动了：%q", m)
	}
	}
```

`internal/release/install_test.go` 的完整目标函数替换为：

```go
func TestActivateUnwritableDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handoff")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	newp := filepath.Join(dir, TempName("v1.0.0"))
	if err := os.WriteFile(newp, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	testperm.DenyWrite(t, dir)
	_, err := Activate(newp, target)
	if err == nil {
		t.Fatal("只读目录下 Activate 应失败")
	}
	if !strings.Contains(err.Error(), "写权限") {
		t.Fatalf("报错应点明是目录写权限问题，得到: %v", err)
	}
}
```

`internal/prochost/fence_inherit_test.go` 不增加 import、不替换 `os.Geteuid()`，并在
代码审查记录中明确它验证的是 RLIMIT 的第二段能力分档。

### Step 3：跑定向绿测试

执行四条定向命令。Linux 非 root/无特殊 DAC 能力时，六个 mode 用例必须进入既有
断言并 PASS；root+DAC_OVERRIDE 或 Windows 上 helper 的写/读探针成功时，相关用例
必须输出含“当前机器无法表达”及“探针成功”的 SKIP。两种结果都必须是 helper 真实
观察的结果，不能用 euid 文案代替。

### Step 4：跑一次强制变异，验证四个恒红断言确实被 guard 住

在 `internal/testperm/permission.go` 中临时把以下已确定代码：

```go
	if probeErr == nil {
```

改为：

```go
	if true { // B219 mutation：强制把限制判为未生效
```

只运行四个原恒红用例的 `-v` 命令：

```sh
go test ./internal/client -run 'TestCursorRootFallsBackToCwdWhenHomeUnwritable|TestCursorRootErrorNamesBothPaths' -count=1 -v
go test ./internal/config -run '^TestLoadStripUpdateDoesNotBlockOnSaveFailure$' -count=1 -v
go test ./internal/executor/grok -run '^TestSyncAuthKeepsTaskCopyWhenWriteFails$' -count=1 -v
```

每个目标必须出现 `--- SKIP`，且 skip 文案必须包含“探针成功”；若任一目标仍 PASS，
先修 helper/调用点再继续。随后立即用 `apply_patch` 将该行恢复为原始
`if probeErr == nil {`，重跑 Task 1 测试和 Task 2 四条定向命令；变异不能留在工作树。

### Step 5：静态检查与提交

依次执行：

```sh
gofmt -w internal/testperm/permission.go internal/testperm/permission_test.go \
  internal/client/cursordir_test.go internal/client/cursor_layout_test.go \
  internal/config/config_test.go internal/executor/grok/authsync_test.go \
  internal/release/install_test.go
git diff --check
go vet ./internal/testperm ./internal/client ./internal/config ./internal/executor/grok ./internal/release
git status --short
```

每条命令的真实输出追加台账；`git diff --check` 与 `go vet` 无输出才可提交。Task 2
提交时只 `git add` 本计划涉及的 helper、五个测试文件和 `docs/ledger-b219.md`，
不 add 其他工作树变化；不 push、不切分支、不改 git 配置。

## 验收栏

### 缺陷族对抗审查

- root、非 root+`CAP_DAC_OVERRIDE`、Windows 目录 `chmod` 空操作：写/读探针成功时
  恢复并 skip，skip 文案写观察事实，不写 euid 推断。
- 目录写与文件写、文件读：三种访问形状分别由 `CreateTemp`、`O_WRONLY`、`Open`
  探针覆盖，不把目录 mode 当文件写判据。
- 探针无关错误：`decideProbe` 的 `probeFatal` 分支由单测覆盖，`ENOENT` 等错误
  恢复后 `Fatalf`，不静默 skip。
- 清理与早退：原 mode 在 `t.Cleanup` 中恢复；限制失效和无关错误在 `Skipf`/
  `Fatalf` 前主动恢复；Task 1 的两个子测试分别断言这两条时序。
- 现有生产行为：六个测试的既有断言不改；fence 的 RLIMIT 语义不被文件权限 helper
  污染；`go vet` 覆盖五个触及包。
- 未来新增用例：包对外只暴露 `DenyWrite`/`DenyRead` 两个入口，调用点不再复制
  euid 守卫、mode 恢复和 skip 文案。

### 序列化边界

本卡不新增数据字段、DTO、wire payload、CLI 投影或跨语言边界，因此没有手写序列化
链路。唯一接缝是 `decideProbe(operation, path, probeErr) probeDecision`；
`TestDecideProbe` 逐条覆盖 nil、`fs.ErrPermission`、无关错误三态，并用可区分的
`probeAction`/`restoreBeforeAction` 布尔值区分“限制失效需 skip”“限制生效继续”“无关
错误失败”，不把零值/缺失折叠成同一分支。

### 上下文预算与类型标注

- Task 1 文件集固定为两个新文件；Task 2 文件集固定为五个测试文件，且 fence 文件
  明确不改，均为有界集合。
- 真机验收清单：Linux 非 root 正常权限、Linux root+DAC_OVERRIDE、当前无
  DAC_OVERRIDE root 沙箱、Windows 目录 mode 空操作；每种环境只依据 `-v` 输出中
  的 PASS/SKIP 与具体 skip 文案判定。

### Spec 覆盖映射

| Spec 故事/决定 | 计划归属 |
|---|---|
| root 执行机看到环境事实 SKIP | Task 1 的探针三态 + Task 2 变异验收 |
| 后续作者复用 helper、自动还原 | Task 1 两个导出函数、`t.Cleanup` 单测 |
| skip 文案区分限制失效与禁用 | `decideProbe` 的 `probeSkip` 文案与定向 `-v` 检查 |
| Windows 不再补平台守卫 | `os.Chmod` 取证、写/读探针成功分支 |
| 三处已有守卫收编但不混淆 RLIMIT | Task 2 收编 client/release；fence 明确保持原样 |
| 不改生产代码/不换被测故障形状 | 文件清单与调用点只替换 mode 前提构造 |

### 计划完整性自声明

本计划已完成骨架标记扫描，没有未给出代码的步骤。`permission_test.go` 与
`permission.go` 已给出完整代码块；Task 2 对每个修改函数给出完整替换块，并明确
删除的旧行。计划中的“以实际输出为准”只表示执行纪律要求记录真实结果，不是缺失实现。

## 交付顺序

1. 实现者按 Task 1 独立提交 helper 与单测。
2. 实现者按 Task 2 独立提交五个调用点改造与台账。
3. **本 task 由协调者执行，不派发**：协调者在合并两次提交后执行 `go build ./...`、
   `go vet ./...`、`go test ./... -count=1`，并在 root 执行机读取全量结果，判断是否仍
   存在第四族权限前提恒红；这些全量命令不属于任一实现 task 的局部绿灯。
