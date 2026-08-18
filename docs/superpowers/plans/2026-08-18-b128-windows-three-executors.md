# Windows 补齐三个执行器 Implementation Plan

> **For agentic workers:** 按你所在执行器的机制逐 task 实现。Steps 用 checkbox（`- [ ]`）标记进度。

**Goal:** 让 Windows 执行机上 claude、grok、codex 三个执行器都可用——claude 补齐输入通道的平台实现，grok 改为注册期能力探测，codex 无代码改动只待验收。

**Architecture:** 平台差异全部关在 `internal/prochost` 内。输入通道在 Windows 上由「shim 持有的命名管道服务端 + 中继 goroutine + 匿名管道」三段构成，匿名管道写端由 shim 全程持有，等价于 unix 侧 `O_RDWR` 打开 FIFO 的「永不 EOF」语义。裁决 socket 继续用 AF_UNIX（已实测 Windows 原生可用），传输代码零改动。

**Tech Stack:** Go 1.24+，`golang.org/x/sys/windows`（已是直接依赖），标准库 `net` / `os` / `crypto/sha256`。

## Global Constraints

以下每条都是硬要求，每个 task 的验收隐含包含本节。

- **不引入任何新的 Go 模块。** 不引 `go-winio`。Windows 系统调用一律走已在 `go.mod` 里的 `golang.org/x/sys/windows`。
- **平台差异只允许出现在 `internal/prochost` 内。** adapter 层（`internal/executor/**`）不得出现 `//go:build windows` 或 `runtime.GOOS` 分支。唯一例外是 §Task 7 对 `perm.go` 注释的分平台表述（注释不是代码分支）。
- **`proc.json` 的结构不得改动**，三个 adapter 的对外行为不得改动。
- **执行机是 macOS。** Windows 上的**运行期**行为一律不得声称「已验证」——你能跑的只有交叉编译、`go vet`、以及在 macOS 上真能执行的测试。Windows 运行期验证由 CI 的 windows-latest job 和审核者的真机验收承担。
- **`GOOS=windows go build` 与 `go vet` 全绿不是可用性证据。** 本 spec §4.4 的实证：`syscall.O_NONBLOCK` 在 Windows 上有定义、编译得过、运行期必炸。写代码时不要用「编译过了」当作对的依据。
- **完工六门**（每个 task 结束前自己跑，任一不过就停下修）：
  ```
  go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go') && GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
  ```
  额外补一条 arm64：`GOOS=windows GOARCH=arm64 go build ./...`
- **`gofmt -l` 必须无输出。** 测试全绿不代表格式干净，这两件事分开验。
- **不得调用 `handoff` CLI、不得启动 agentd、不得派发子任务。** 需要驱动 handoff 自身的验收步骤全部留给审核者（见文末「审核者专属」一节）。
- **日志一律用包内既有的 `log()`（`slog`），禁止 `fmt.Printf`。** shim 侧用传入的 `*slog.Logger`。

---

## File Structure

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/prochost/pipename.go` | 由通道路径确定性推导 Windows 命名管道名。**无 build tag**——纯函数，要在 macOS 上也能测 | 新建 |
| `internal/prochost/pipename_test.go` | 上者的单测 | 新建 |
| `internal/prochost/inputch_windows.go` | Windows 输入通道全部实现：命名管道服务端、中继、匿名管道、四个原语 | 新建 |
| `internal/prochost/inputch_windows_test.go` | 上者的单测（只在 Windows 上运行） | 新建 |
| `internal/prochost/prochost.go` | 导出 `WriteInputChannel`；补 `CreateInputChannel` 的分平台文档 | 改 |
| `internal/prochost/platform_unix.go` | 补 `writeInputChannel` / `openInputChannel` | 改 |
| `internal/prochost/platform_windows.go` | 删掉两个 not-implemented 桩（实现移入 `inputch_windows.go`）；补 `writeInputChannel` / `openInputChannel` 的转发 | 改 |
| `internal/prochost/platform_other.go` | 补两个新原语的 not-implemented 桩 | 改 |
| `internal/prochost/shim.go` | 把内联的 `os.OpenFile(spec.InputCh, O_RDWR)` 换成 `openInputChannel` 钩子 | 改 |
| `internal/executor/claudecode/proc.go` | `WriteInput` 改为调 `prochost.WriteInputChannel` | 改 |
| `internal/executor/claudecode/perm.go` | 安全边界注释改为分平台表述；socket 路径超长给明确错误 | 改 |
| `internal/executor/grok/symlinkcap.go` | grok 的符号链接能力探测 | 新建 |
| `internal/executor/grok/symlinkcap_test.go` | 上者的单测 | 新建 |
| `cmd/agentd.go` | `adaptersFor` 解禁 claude、grok 改能力探测 | 改 |
| `.github/workflows/ci.yml` | windows-latest job 追加 `go test` | 改 |

**为什么 Windows 输入通道单独一个文件、而 unix 侧的两个新原语直接加在 `platform_unix.go` 里**：Windows 侧约 200 行（管道服务端 + 安全描述符 + 中继循环），塞进 `platform_windows.go` 会让那个文件同时承担「进程/锁/Job Object」和「输入通道」两件事；unix 侧两个新原语各约 10 行，与既有的 `createInputChannel` / `waitInputReader` 紧邻，拆出去反而割裂。按体量决定，不追求两边对称。

---

## Task 1: `WriteInputChannel` 原语下沉

把 `WriteInput` 里的 unix-ism 从 adapter 挪进 prochost。这是**编译期看不见的缺陷**：`syscall.O_NONBLOCK` 在 Windows 上有定义（`types_windows.go:50`，值 `0x00800`），所以现状能通过 `GOOS=windows go build`，但运行期必然 file-not-found。

**Files:**
- Modify: `internal/prochost/prochost.go`（在 `WaitInputReader` 之后追加导出函数）
- Modify: `internal/prochost/platform_unix.go`（在 `waitInputReader` 之后追加）
- Modify: `internal/prochost/platform_windows.go`（在既有两个桩旁追加）
- Modify: `internal/prochost/platform_other.go`（在既有两个桩旁追加）
- Modify: `internal/executor/claudecode/proc.go:256-275`
- Test: `internal/prochost/inputch_unix_test.go`（新建，带 `//go:build unix`）

**Interfaces:**
- Produces: `func prochost.WriteInputChannel(path string, data []byte) error` —— 往输入通道投递一段字节。读端不在时返回错误（调用方据此判「进程不在」）。Task 4 提供它的 Windows 实现。

- [ ] **Step 1: 写失败的测试**

新建 `internal/prochost/inputch_unix_test.go`：

```go
//go:build unix

package prochost

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWriteInputChannelDeliversToReader 钉住投递本身：有读端时字节必须到达。
func TestWriteInputChannelDeliversToReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	// O_RDWR 持有读端，模拟 shim 的行为（只读打开会在写端关闭时 EOF）
	r, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("持有读端失败: %v", err)
	}
	defer r.Close()

	if err := WriteInputChannel(path, []byte("hello\n")); err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	buf := make([]byte, 16)
	if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("设读超时失败: %v", err)
	}
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("读失败: %v", err)
	}
	if got := string(buf[:n]); got != "hello\n" {
		t.Fatalf("读到 %q，想要 %q", got, "hello\n")
	}
}

// TestWriteInputChannelFailsWithoutReader 钉住承重语义：读端不在时必须立刻失败。
// 这是调用方判定「进程已不在」的唯一依据——若这里改成阻塞或静默成功，
// ErrTaskNotRunning 就再也不会被触发，任务会挂在一个死执行者上等到超时。
func TestWriteInputChannelFailsWithoutReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	err := WriteInputChannel(path, []byte("hello\n"))
	if err == nil {
		t.Fatalf("无读端时投递竟然成功了")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("错误里没带通道路径，排障时定位不到: %v", err)
	}
	_ = syscall.ENXIO // 文档性引用：unix 上根因是 ENXIO
}

// TestWriteInputChannelMissingPath 钉住通道根本不存在时的行为。
func TestWriteInputChannelMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.fifo")
	if err := WriteInputChannel(path, []byte("x")); err == nil {
		t.Fatalf("通道不存在时投递竟然成功了")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run TestWriteInputChannel -count=1`
Expected: FAIL，报 `undefined: WriteInputChannel`

- [ ] **Step 3: 加跨平台导出函数**

在 `internal/prochost/prochost.go` 的 `WaitInputReader` 函数之后追加：

```go
// WriteInputChannel 往输入通道投递一段字节。
//
// 参数：
//   - path: 通道路径（与 Spec.InputCh 同一个值）
//   - data: 原样投递的字节，本函数不加工、不追加换行
//
// 返回：读端不在、通道不存在、写失败时返回错误。
//
// 注意：
//   - **「打不开即读端不在」是承重语义**：unix 上以 O_WRONLY|O_NONBLOCK 打开，
//     读端未就绪时 POSIX 规定直接失败（ENXIO）；Windows 上 CreateFile 打不开
//     管道名报 ERROR_FILE_NOT_FOUND。两边都是调用方判定「执行者已不在」的依据，
//     实现不得改成阻塞等待或静默成功
//   - 本函数不做 JSON 序列化：那是 adapter 的协议知识，prochost 只搬字节
func WriteInputChannel(path string, data []byte) error {
	return writeInputChannel(path, data)
}
```

- [ ] **Step 4: 实现 unix 版本**

在 `internal/prochost/platform_unix.go` 的 `waitInputReader` 之后追加：

```go
// writeInputChannel 往 FIFO 投递字节（见 WriteInputChannel 的文档）。
//
// O_NONBLOCK 不是性能选择而是语义选择：没有它，打开写端会一直阻塞到出现读端，
// 「执行者已不在」就变成「永远等下去」。
func writeInputChannel(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("打开输入通道 %s（读端可能已不在）: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("写输入通道 %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 5: 加两个平台桩**

`internal/prochost/platform_windows.go`，紧邻既有的 `createInputChannel` 桩：

```go
func writeInputChannel(path string, data []byte) error {
	log().Error("Windows 输入通道尚未实现", "path", path, "bytes", len(data))
	return errNotImplemented
}
```

`internal/prochost/platform_other.go`，紧邻既有桩：

```go
func writeInputChannel(path string, data []byte) error { return errNotImplemented }
```

- [ ] **Step 6: 改 adapter 调用点**

`internal/executor/claudecode/proc.go`，把 `WriteInput` 改成（注意：`syscall` 与 `os` 这两个 import 若在本文件其它地方不再使用，必须一并删掉，否则编译不过）：

```go
// WriteInput 往输入通道投递一条 stream-json user message。
//
// 参数：
//   - text: 指令原文，原样透传不加工（executor 契约要求）
//
// 注意：
//   - 投递失败多半意味着执行者已不在（读端消失），调用方据此包装
//     executor.ErrTaskNotRunning。平台差异（unix 的 ENXIO / Windows 的
//     ERROR_FILE_NOT_FOUND）由 prochost 吸收，本层不感知
func (p *Proc) WriteInput(text string) error {
	path := filepath.Join(p.TaskDir, fifoFileName)
	line, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	})
	if err != nil {
		return fmt.Errorf("序列化 user message: %w", err)
	}
	if err := prochost.WriteInputChannel(path, append(line, '\n')); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 7: 加关键节点日志**

在 `WriteInput` 的成功路径补一条 Debug（**不要用 Info**：每轮对话都会投递，Info 会把 agentd.log 淹掉），在 `prochost.WriteInputChannel` 的失败路径已由返回的 error 携带上下文、不重复打日志：

```go
	if err := prochost.WriteInputChannel(path, append(line, '\n')); err != nil {
		return err
	}
	slog.Default().Debug("已投递指令到输入通道", "task_dir", p.TaskDir, "bytes", len(line)+1)
	return nil
```

判据：失败路径的 error 里必须含通道路径（Task 1 Step 1 的第二条测试钉住了这点）；成功路径不静默。

- [ ] **Step 8: 加注释**

确认三处注释已就位（上面的代码块里已包含，此步是自检）：
- `WriteInputChannel` 的导出文档，写明「打不开即读端不在」是承重语义、以及为什么不做序列化
- unix `writeInputChannel` 的 `O_NONBLOCK` 是语义选择而非性能选择
- adapter `WriteInput` 写明平台差异由 prochost 吸收

- [ ] **Step 9: 跑测试**

Run: `go test ./internal/prochost/ ./internal/executor/claudecode/ -count=1`
Expected: PASS

- [ ] **Step 10: 跑完工六门**

Run: 见 Global Constraints
Expected: 全绿，`gofmt -l` 无输出

- [ ] **Step 11: Commit**

```bash
git add internal/prochost/ internal/executor/claudecode/proc.go
git commit -m "refactor(prochost): WriteInputChannel 原语下沉，输入通道的第三处平台缝归位"
```

---

## Task 2: `openInputChannel` 平台钩子

shim 准备子进程 stdin 的那段现在硬编码 `os.OpenFile(spec.InputCh, os.O_RDWR, 0)`，Windows 上没有等价物。抽成平台钩子。

**Files:**
- Modify: `internal/prochost/shim.go:116-126`
- Modify: `internal/prochost/platform_unix.go`
- Modify: `internal/prochost/platform_windows.go`
- Modify: `internal/prochost/platform_other.go`
- Test: `internal/prochost/inputch_unix_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `writeInputChannel`（测试里用来验证读端可用）
- Produces: `func openInputChannel(path string) (io.ReadCloser, func(), error)` —— 包内未导出。返回给 `cmd.Stdin` 用的读端、子进程退出后调用的 cleanup、以及错误。Task 4 提供 Windows 实现。

- [ ] **Step 1: 写失败的测试**

追加到 `internal/prochost/inputch_unix_test.go`：

```go
// TestOpenInputChannelNeverEOFs 钉住整个输入通道设计的地基：
// 写端来了又走之后，读端**不得** EOF。
//
// 这条不是形式主义——unix 上靠 shim 自己 O_RDWR 同时持有写端来保证，
// Windows 上靠 shim 攥着匿名管道写端来保证，两边实现完全不同但契约相同。
// 少了这条，症状是「执行者跑完第一条指令后再也不响应」，极难归因。
func TestOpenInputChannelNeverEOFs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	r, cleanup, err := openInputChannel(path)
	if err != nil {
		t.Fatalf("打开输入通道失败: %v", err)
	}
	defer cleanup()

	// 第一次投递：写端开了又关
	if err := WriteInputChannel(path, []byte("one\n")); err != nil {
		t.Fatalf("第一次投递失败: %v", err)
	}
	buf := make([]byte, 32)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("第一次读失败: %v", err)
	}
	if got := string(buf[:n]); got != "one\n" {
		t.Fatalf("第一次读到 %q，想要 %q", got, "one\n")
	}

	// 第二次投递：若上一次写端关闭导致了 EOF，这里会读到 io.EOF 而不是数据
	if err := WriteInputChannel(path, []byte("two\n")); err != nil {
		t.Fatalf("第二次投递失败: %v", err)
	}
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("第二次读失败（写端关闭把读端 EOF 掉了）: %v", err)
	}
	if got := string(buf[:n]); got != "two\n" {
		t.Fatalf("第二次读到 %q，想要 %q", got, "two\n")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run TestOpenInputChannelNeverEOFs -count=1`
Expected: FAIL，报 `undefined: openInputChannel`

- [ ] **Step 3: 实现 unix 版本**

在 `internal/prochost/platform_unix.go` 追加：

```go
// openInputChannel 为子进程准备 stdin（见 shim.go 调用点）。
//
// 参数：path 为通道路径
//
// 返回：
//   - r: 交给 cmd.Stdin 的读端
//   - cleanup: 子进程退出后调用，释放本函数占用的资源
//   - error: 非 nil 时 shim 必须放弃拉起执行者
//
// 注意：**O_RDWR 而不是 O_RDONLY 是承重的**。只读打开会在所有写端关闭时收到
// EOF，执行者的 stdin 随即关闭；O_RDWR 让 shim 自己同时也是写端，FIFO 因此
// 永不 EOF。这是旧 sh 脚本 `exec 3<> in.fifo` 的等价手法。
func openInputChannel(path string) (io.ReadCloser, func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("打开输入通道 %s: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}
```

需要在该文件 import 里加 `"io"`。

- [ ] **Step 4: 加两个平台桩**

`internal/prochost/platform_windows.go`：

```go
func openInputChannel(path string) (io.ReadCloser, func(), error) {
	log().Error("Windows 输入通道尚未实现", "path", path)
	return nil, nil, errNotImplemented
}
```

`internal/prochost/platform_other.go`：

```go
func openInputChannel(path string) (io.ReadCloser, func(), error) {
	return nil, nil, errNotImplemented
}
```

两个文件都要加 `"io"` import。

- [ ] **Step 5: 改 shim 调用点**

`internal/prochost/shim.go`，把现有的：

```go
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
```

替换为：

```go
	if spec.InputCh != "" {
		// 「永不 EOF」由平台实现各自保证：unix 靠 O_RDWR，Windows 靠 shim
		// 攥着匿名管道写端。契约见 openInputChannel 的文档
		in, cleanup, ferr := openInputChannel(spec.InputCh)
		if ferr != nil {
			l.Error("打开输入通道失败", "path", spec.InputCh, "cause", ferr)
			return fmt.Errorf("打开输入通道 %s: %w", spec.InputCh, ferr)
		}
		defer cleanup()
		cmd.Stdin = in
		l.Info("输入通道已就位", "path", spec.InputCh)
	}
```

- [ ] **Step 6: 改文件头注释**

`internal/prochost/shim.go` 文件头有一段：

```
// 为什么 shim 以 O_RDWR 打开 FIFO：只读打开会在写端全部关闭时收到 EOF，
// executor 的 stdin 随即关闭；O_RDWR 让 shim 自己同时是写端，FIFO 永不 EOF。
// 这是旧 sh 脚本 `exec 3<> in.fifo` 的等价手法。
```

改为分平台表述（**这不是措辞润色**：这段现在描述的是一个已经被抽走的实现细节，留着会让人以为 Windows 上也是 FIFO）：

```
// 为什么输入通道必须「永不 EOF」：读端一旦 EOF，executor 的 stdin 就关闭，
// 它跑完第一条指令后再也收不到后续投递。unix 上靠 shim 以 O_RDWR 打开 FIFO
// （自己同时是写端）保证，Windows 上靠 shim 攥着匿名管道写端保证——两边实现
// 不同、契约相同，见 openInputChannel。
```

同时把「职责」那段的 `InputCh 非空时以 O_RDWR 持有 FIFO 读端` 改为
`InputCh 非空时经 openInputChannel 准备子进程 stdin（平台各自实现）`。

- [ ] **Step 7: 加关键节点日志**

上面 Step 5 已加 `l.Info("输入通道已就位", ...)`（成功路径不静默）、失败路径已有 `l.Error` 带 path 与 cause。此步确认无遗漏，不额外加。

- [ ] **Step 8: 跑测试**

Run: `go test ./internal/prochost/ -count=1`
Expected: PASS，包括既有的 shim 相关测试

- [ ] **Step 9: 跑完工六门**

Expected: 全绿

- [ ] **Step 10: Commit**

```bash
git add internal/prochost/
git commit -m "refactor(prochost): shim 的 stdin 准备抽成 openInputChannel 平台钩子"
```

---

## Task 3: 命名管道名推导

纯函数，**故意不加 build tag**——它是本轮唯一能在 macOS 上真正测到的 Windows 相关逻辑，加了 tag 就等于放弃这块覆盖。

**Files:**
- Create: `internal/prochost/pipename.go`
- Test: `internal/prochost/pipename_test.go`

**Interfaces:**
- Produces: `func pipeNameFor(path string) string` —— 由通道路径推导 Windows 命名管道全名，形如 `\\.\pipe\handoff-<16 个十六进制字符>`。Task 4 的服务端与客户端两侧都用它。

- [ ] **Step 1: 写失败的测试**

新建 `internal/prochost/pipename_test.go`：

```go
package prochost

import (
	"strings"
	"testing"
)

// TestPipeNameForIsDeterministic 钉住确定性：这是 agentd 与 shim 不共享额外状态
// 就能算出同一个名字的前提，proc.json 与三个 adapter 的零改动全靠它。
func TestPipeNameForIsDeterministic(t *testing.T) {
	const p = `C:\Users\u\.handoff\tasks\3e70fd90-98a4-42b0-be02-c22a357e0ed4\in.fifo`
	a, b := pipeNameFor(p), pipeNameFor(p)
	if a != b {
		t.Fatalf("同一路径推导出两个名字: %q vs %q", a, b)
	}
}

// TestPipeNameForDistinctPerTask 钉住不同任务不撞名——撞名意味着两个任务的
// 执行者共用一根 stdin，指令会投递到错误的模型上。
func TestPipeNameForDistinctPerTask(t *testing.T) {
	a := pipeNameFor(`C:\t\aaaaaaaa-0000-0000-0000-000000000000\in.fifo`)
	b := pipeNameFor(`C:\t\bbbbbbbb-0000-0000-0000-000000000000\in.fifo`)
	if a == b {
		t.Fatalf("不同任务推导出同一个管道名: %q", a)
	}
}

// TestPipeNameForShape 钉住形态与长度：Windows 管道名上限 256 字符，
// 而任务目录路径可以很长——名字必须与输入长度无关。
func TestPipeNameForShape(t *testing.T) {
	long := `C:\` + strings.Repeat("verydeep\\", 30) + `in.fifo`
	name := pipeNameFor(long)
	if !strings.HasPrefix(name, `\\.\pipe\handoff-`) {
		t.Fatalf("管道名前缀不对: %q", name)
	}
	if len(name) > 256 {
		t.Fatalf("管道名 %d 字符，超过 Windows 上限 256: %q", len(name), name)
	}
	hexPart := strings.TrimPrefix(name, `\\.\pipe\handoff-`)
	if len(hexPart) != 16 {
		t.Fatalf("哈希段 %d 字符，想要 16: %q", len(hexPart), hexPart)
	}
	for _, r := range hexPart {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("哈希段含非十六进制字符 %q: %q", r, hexPart)
		}
	}
}

// TestPipeNameForNormalizesSeparators 钉住路径归一：同一个位置的两种写法
// 必须推出同一个名字，否则 agentd 与 shim 会各算各的。
func TestPipeNameForNormalizesSeparators(t *testing.T) {
	a := pipeNameFor(`C:\t\x\in.fifo`)
	b := pipeNameFor(`C:\t\y\..\x\in.fifo`)
	if a != b {
		t.Fatalf("等价路径推出不同名字: %q vs %q", a, b)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run TestPipeNameFor -count=1`
Expected: FAIL，报 `undefined: pipeNameFor`

- [ ] **Step 3: 实现**

新建 `internal/prochost/pipename.go`：

```go
// pipename.go —— Windows 命名管道名的确定性推导。
//
// 职责：把输入通道路径映射成一个稳定、等长、不含路径分隔符的管道全名。
//
// 边界：
//   - 纯函数，不碰系统调用、不判平台。**故意不加 build tag**：它是 Windows
//     输入通道里唯一能在任何平台上被真正执行到的逻辑，加 tag 等于放弃这块覆盖
//   - 不负责安全：名字可推导不等于可随意连接，抢占防护由
//     FILE_FLAG_FIRST_PIPE_INSTANCE 与安全描述符承担（见 inputch_windows.go）
package prochost

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// pipeNamePrefix 是所有 handoff 管道的公共前缀，便于运维一眼认出归属。
const pipeNamePrefix = `\\.\pipe\handoff-`

// pipeNameFor 由输入通道路径推导 Windows 命名管道全名。
//
// 参数：path 为通道路径（Spec.InputCh 的值）
//
// 返回：形如 `\\.\pipe\handoff-a1b2c3d4e5f60718` 的全名，恒为 33 个字符。
//
// 注意：
//   - **确定性是硬要求**：agentd 与 shim 是两个进程、不共享额外状态，
//     只能各自从同一个 InputCh 值算出同一个名字。这也是 proc.json 与三个
//     adapter 能零改动的原因
//   - 取哈希而不是直接编码路径：路径含 `\` 与 `:`，而管道名里 `\` 是命名空间
//     分隔符；且路径长度无上限，管道名上限 256
//   - 只取前 8 字节（16 个十六进制字符）：碰撞面是同一台机器上并存的任务数
//     （量级几十），2^64 远够用，换来一个短到便于日志阅读的名字
func pipeNameFor(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return pipeNamePrefix + hex.EncodeToString(sum[:8])
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/prochost/ -run TestPipeNameFor -count=1 -v`
Expected: PASS，四条全过

- [ ] **Step 5: 加关键节点日志**

本函数**刻意不打日志**：它是纯函数、无失败分支，且会在 `waitInputReader` 的轮询里被高频调用。使用方（Task 4 的服务端创建与客户端连接）各自在边界上打一次带 `pipe` 字段的日志，那才是排障需要的位置。此步是显式决定，不是遗漏。

- [ ] **Step 6: 加注释**

确认 Step 3 的文件头注释含「职责 + 边界」、导出行为的三条注意事项（为什么确定性承重、为什么取哈希、为什么取 8 字节）。

- [ ] **Step 7: 跑完工六门**

Expected: 全绿

- [ ] **Step 8: Commit**

```bash
git add internal/prochost/pipename.go internal/prochost/pipename_test.go
git commit -m "feat(prochost): 命名管道名的确定性推导（跨平台可测的纯函数）"
```

---

## Task 4: Windows 输入通道实现

本 plan 的主体。四个原语的 Windows 实现全部落在一个新文件里。

**Files:**
- Create: `internal/prochost/inputch_windows.go`
- Create: `internal/prochost/inputch_windows_test.go`
- Modify: `internal/prochost/platform_windows.go`（删掉 `createInputChannel` / `waitInputReader` / `writeInputChannel` / `openInputChannel` 四个桩，以及不再被引用的 `errNotImplemented` 相关注释）

**Interfaces:**
- Consumes: Task 3 的 `pipeNameFor(path string) string`
- Produces: `createInputChannel` / `waitInputReader` / `writeInputChannel` / `openInputChannel` 的 Windows 实现，签名与 unix 侧完全一致

- [ ] **Step 1: 写失败的测试**

新建 `internal/prochost/inputch_windows_test.go`：

```go
//go:build windows

package prochost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWindowsInputChannelNeverEOFs 是本 task 的核心判据，与 unix 侧
// TestOpenInputChannelNeverEOFs 同契约不同实现：投递两次，第二次必须也读得到。
//
// 为什么必须两次：命名管道客户端断开会让服务端侧 broken pipe，若实现把服务端
// 句柄直接当子进程 stdin，第一次投递能过、第二次就死——单次投递测不出来。
func TestWindowsInputChannelNeverEOFs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	r, cleanup, err := openInputChannel(path)
	if err != nil {
		t.Fatalf("打开输入通道失败: %v", err)
	}
	defer cleanup()

	if _, err := waitInputReader(path, 5*time.Second); err != nil {
		t.Fatalf("等待读端就绪失败: %v", err)
	}

	for i, want := range []string{"one\n", "two\n"} {
		if err := WriteInputChannel(path, []byte(want)); err != nil {
			t.Fatalf("第 %d 次投递失败: %v", i+1, err)
		}
		buf := make([]byte, 32)
		n, rerr := r.Read(buf)
		if rerr != nil {
			t.Fatalf("第 %d 次读失败（很可能是子进程 stdin 被 EOF 掉了）: %v", i+1, rerr)
		}
		if got := string(buf[:n]); got != want {
			t.Fatalf("第 %d 次读到 %q，想要 %q", i+1, got, want)
		}
	}
}

// TestWindowsWaitInputReaderTimesOutWithoutServer 钉住「服务端没建起来必须超时」。
// createInputChannel 在 Windows 上是 no-op，等待责任全压在这里——它若误报就绪，
// StartProc 会带着一个没有 stdin 的执行者继续往下走。
func TestWindowsWaitInputReaderTimesOutWithoutServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("建通道失败: %v", err)
	}
	_, err := waitInputReader(path, 300*time.Millisecond)
	if err == nil {
		t.Fatalf("服务端不存在时竟然报告读端已就绪")
	}
}

// TestWindowsWriteFailsWithoutServer 钉住承重语义：服务端不在时投递必须失败，
// 这是调用方判「执行者已不在」的依据。
func TestWindowsWriteFailsWithoutServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	err := WriteInputChannel(path, []byte("x\n"))
	if err == nil {
		t.Fatalf("服务端不存在时投递竟然成功了")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("错误里没带通道路径，排障定位不到: %v", err)
	}
}

// TestWindowsFirstInstanceRejectsSquatting 钉住抢占防护：同名管道已存在时，
// 第二次创建必须失败。这是安全判据不是健壮性判据——管道名被抢占意味着
// 别人能拿到执行者的 stdin，可以直接给模型下指令。
func TestWindowsFirstInstanceRejectsSquatting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	_, cleanup, err := openInputChannel(path)
	if err != nil {
		t.Fatalf("第一次创建失败: %v", err)
	}
	defer cleanup()

	_, cleanup2, err := openInputChannel(path)
	if err == nil {
		cleanup2()
		t.Fatalf("同名管道竟然允许第二次创建——抢占防护失效")
	}
}

// TestWindowsCreateInputChannelIsNoop 钉住 no-op 的契约：它成功不代表通道可用。
// 若哪天有人给它加了「创建服务端」的实现，这条会失败并提醒他：服务端归属
// 必须在 shim，agentd 侧建服务端会让 agentd 重启杀死执行者 stdin。
func TestWindowsCreateInputChannelIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.fifo")
	if err := createInputChannel(path); err != nil {
		t.Fatalf("no-op 竟然失败: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("createInputChannel 在 Windows 上不该创建任何文件")
	}
	if _, err := waitInputReader(path, 200*time.Millisecond); err == nil {
		t.Fatalf("createInputChannel 之后读端不该就绪——服务端由 shim 建")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOOS=windows GOARCH=amd64 go vet ./internal/prochost/`
Expected: FAIL，报四个原语重复声明（`platform_windows.go` 的桩还在）或未定义

> 执行机是 macOS，跑不了 Windows 测试。这一步用 `go vet` 代替「跑测试看它失败」——它能验证测试文件本身编译得过、且新旧实现没有重复声明。真正的执行由 CI 的 windows-latest job（Task 5）承担。

- [ ] **Step 3: 删掉旧桩**

`internal/prochost/platform_windows.go`：删除 `createInputChannel` 与 `waitInputReader` 两个函数（含它们上方那行 `// createInputChannel / waitInputReader 见文件头：只在 claude 路径上，本轮不做。`），以及 Task 1 / Task 2 加的 `writeInputChannel` / `openInputChannel` 两个桩。

同时改文件头注释里这一段：

```
//   - 输入通道（命名管道）不在本轮范围：它只在 claude 路径上，而 claude 在
//     Windows 上根本不注册（见 cmd/agentd.go 的 defaultAdapters）
```

改为：

```
//   - 输入通道的实现在 inputch_windows.go：那一块约 200 行（管道服务端、
//     安全描述符、中继循环），与本文件的进程/锁/Job Object 是两件事
```

以及 `errNotImplemented` 的文档注释：

```
// 本轮之后它只剩输入通道两个原语在用，且**实际不可达**：调用它们的唯一路径是
// claude adapter，而 Windows 上 claude 不进注册表，dispatch 在门口就被拒了。
// 这个不可达是被注册层挡出来的，不是碰巧——改注册表时要连带想到这里。
```

改为：

```
// 输入通道落地后本平台已无 not-implemented 的原语，本变量保留给将来新增的
// 平台缝使用——保留一个统一的返回值，好过每处各编一个错误。
```

- [ ] **Step 4: 实现**

新建 `internal/prochost/inputch_windows.go`：

```go
//go:build windows

// inputch_windows.go —— Windows 输入通道：命名管道服务端 + 中继 + 匿名管道。
//
// 职责：提供 createInputChannel / waitInputReader / writeInputChannel /
// openInputChannel 四个原语的 Windows 实现。
//
// 边界：
//   - 只搬字节，不解析内容、不按帧缓冲：claude 的 stream-json 是逐行 JSON，
//     原样抄即可
//   - 不判断执行者死活：那是存活锁的事
//
// 为什么是「匿名管道 + 中继」而不是「命名管道直接当 stdin」：
// unix 侧 shim 以 O_RDWR 打开 FIFO，图的是**永不 EOF**——agentd 每次投递
// 开写端、写完就关，子进程 stdin 不受影响。Windows 命名管道没有这个性质：
// 客户端一断开，服务端侧即 broken pipe。若把服务端句柄直接给子进程当 stdin，
// 它会在第一条指令投递完成的瞬间看到 EOF，claude 的 stream-json 输入模式当场
// 结束——症状是「执行者起来了、第一条指令也执行了，然后再也不响应」。
// 因此：匿名管道的写端由 shim 全程持有（这才是 O_RDWR 的等价物），命名管道
// 只做 agentd → shim 的搬运。
//
// 为什么服务端必须是 shim 而不是 agentd：agentd 当服务端时，agentd 重启会
// 关闭管道、杀死执行者 stdin，而「执行者活过 agentd 重启」是 B36 的招牌属性，
// B37 已在 Windows 真机上验证过。
package prochost

import (
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// pipeBufSize 是命名管道两侧的内核缓冲区大小。投递的是单条指令（量级 KB），
// 4KB 够用；不够时 WriteFile 会阻塞等中继读走，不丢数据。
const pipeBufSize = 4096

// pipePollInterval 是 waitInputReader 的轮询间隔，与 unix 侧保持一致。
const pipePollInterval = 20 * time.Millisecond

// createInputChannel 在 Windows 上是 no-op。
//
// **返回 nil 表示「无事可做」，不表示「已验证通道可用」。** 命名管道服务端
// 必须由 shim 创建（见文件头「为什么服务端必须是 shim」），agentd 侧没有
// 任何可做的准备工作。就绪判定全部由 waitInputReader 承担：服务端没建起来时
// 它必然超时，而超时路径已有的处置（StartProc 自行 Kill 回收 shim）不变。
func createInputChannel(path string) error {
	log().Debug("Windows 输入通道无需预建，等待责任在 waitInputReader",
		"path", path, "pipe", pipeNameFor(path))
	return nil
}

// waitInputReader 轮询等待 shim 把命名管道服务端建起来。
//
// 参数：path 为通道路径；timeout 为等待上限
//
// 返回：等待耗时与错误。
//
// 注意：
//   - 探测方式是以客户端身份 CreateFile 管道名。ERROR_FILE_NOT_FOUND 表示
//     服务端还没建，继续等；成功或 ERROR_PIPE_BUSY 都表示已建好
//   - **探测成功后立即关闭句柄**。中继会把这次探测看成一次「连上又断开的
//     客户端」——这是无害的：中继是循环受理的，读到 EOF 后回到下一轮
//     ConnectNamedPipe 即可
//   - 其它错误立即返回，不重试：重试一个权限错误没有意义
func waitInputReader(path string, timeout time.Duration) (time.Duration, error) {
	name := pipeNameFor(path)
	deadline := time.Now().Add(timeout)
	start := time.Now()
	for {
		h, err := dialPipe(name)
		if err == nil {
			_ = windows.CloseHandle(h)
			return time.Since(start), nil
		}
		if err == windows.ERROR_PIPE_BUSY {
			// 服务端在、只是实例都忙着：对「是否就绪」这个问题而言就是就绪
			return time.Since(start), nil
		}
		if err != windows.ERROR_FILE_NOT_FOUND {
			return time.Since(start), fmt.Errorf("探测管道 %s: %w", name, err)
		}
		if time.Now().After(deadline) {
			return time.Since(start), fmt.Errorf("管道 %s（通道 %s）在 %s 内未出现服务端",
				name, path, timeout)
		}
		time.Sleep(pipePollInterval)
	}
}

// writeInputChannel 以客户端身份连上管道投递一段字节（见 WriteInputChannel 的文档）。
//
// 注意：打不开即「读端不在」，这条语义与 unix 侧的 ENXIO 对齐，是调用方判定
// 「执行者已不在」的唯一依据，不得改成重试等待。
func writeInputChannel(path string, data []byte) error {
	name := pipeNameFor(path)
	h, err := dialPipe(name)
	if err != nil {
		return fmt.Errorf("连接管道 %s（通道 %s，读端可能已不在）: %w", name, path, err)
	}
	defer windows.CloseHandle(h)
	var written uint32
	for off := 0; off < len(data); {
		if err := windows.WriteFile(h, data[off:], &written, nil); err != nil {
			return fmt.Errorf("写管道 %s（通道 %s）: %w", name, path, err)
		}
		if written == 0 {
			return fmt.Errorf("写管道 %s 返回 0 字节，放弃", name)
		}
		off += int(written)
	}
	return nil
}

// openInputChannel 为子进程准备 stdin：建匿名管道 + 命名管道服务端 + 中继。
//
// 返回：
//   - r: 匿名管道读端，交给 cmd.Stdin
//   - cleanup: 子进程退出后调用，停中继、关服务端与写端
//   - error: 非 nil 时 shim 必须放弃拉起执行者
//
// 注意：**写端 w 不在这里关闭**——shim 全程持有它，子进程才永不见 EOF。
// 它由 cleanup 关闭，而 cleanup 在子进程退出后才被调用。
func openInputChannel(path string) (io.ReadCloser, func(), error) {
	name := pipeNameFor(path)
	srv, err := createPipeServer(name)
	if err != nil {
		log().Error("创建命名管道服务端失败", "pipe", name, "path", path, "cause", err)
		return nil, nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		_ = windows.CloseHandle(srv)
		return nil, nil, fmt.Errorf("创建匿名管道: %w", err)
	}
	stop := make(chan struct{})
	go relayPipe(srv, w, name, stop)
	log().Info("Windows 输入通道已就位", "pipe", name, "path", path)
	cleanup := func() {
		close(stop)
		_ = windows.CloseHandle(srv)
		_ = w.Close()
		_ = r.Close()
	}
	return r, cleanup, nil
}

// createPipeServer 建立命名管道服务端。
//
// 两条安全要求，都不是可选项：
//   - FILE_FLAG_FIRST_PIPE_INSTANCE：命名管道位于**全局命名空间**，不加它的话
//     任何本机进程都能抢先创建同名管道，之后 agentd 连上去的是它的实例——
//     这条通道直接就是执行者的 stdin，被搭上去等于能给模型下任意指令。
//     加上之后，抢占表现为这里创建失败，是可见故障而非静默劫持
//   - 显式安全描述符只授权当前用户与 SYSTEM：默认 ACL 会放行同机其它用户
func createPipeServer(name string) (windows.Handle, error) {
	sa, err := pipeSecurityAttributes()
	if err != nil {
		return windows.InvalidHandle, err
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("管道名 %s 转 UTF16: %w", name, err)
	}
	h, err := windows.CreateNamedPipe(namePtr,
		windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1, // nMaxInstances：中继循环复用同一个实例，一个就够
		0, pipeBufSize, 0, sa)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("创建命名管道 %s（名字可能已被占用）: %w", name, err)
	}
	return h, nil
}

// pipeSecurityAttributes 构造只授权当前用户与 SYSTEM 的安全属性。
func pipeSecurityAttributes() (*windows.SecurityAttributes, error) {
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("取当前用户 SID: %w", err)
	}
	// D:P = 不继承父对象 ACL；GA = 全部权限；SY = LocalSystem
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("构造安全描述符 %s: %w", sddl, err)
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

// dialPipe 以客户端身份打开管道（写方向）。
func dialPipe(name string) (windows.Handle, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(namePtr, windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, 0, 0)
}

// relayPipe 循环受理客户端并把字节抄进匿名管道写端。
//
// 每次 agentd 投递就是一次客户端连接：连上 → 写一帧 → 断开。断开在服务端侧
// 表现为 ReadFile 返回 ERROR_BROKEN_PIPE，那是**正常结束**不是故障。
//
// 失败取舍（有意为之，不是缺陷）：受理或读取出错时打日志并重建连接继续；
// 只有 stop 被关闭才退出。**任何情况下都不杀子进程**——回合中间的产出不该
// 被丢弃。中继彻底失效后，后续 writeInputChannel 会连不上管道，走既有的
// 「读端不在」路径；真实原因去 shim.log 找。
func relayPipe(srv windows.Handle, w *os.File, name string, stop <-chan struct{}) {
	buf := make([]byte, pipeBufSize)
	for {
		select {
		case <-stop:
			log().Info("输入通道中继退出", "pipe", name)
			return
		default:
		}
		err := windows.ConnectNamedPipe(srv, nil)
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			log().Error("受理输入通道客户端失败", "pipe", name, "cause", err)
			_ = windows.DisconnectNamedPipe(srv)
			time.Sleep(pipePollInterval)
			continue
		}
		for {
			var n uint32
			rerr := windows.ReadFile(srv, buf, &n, nil)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					log().Error("写匿名管道失败，执行者可能已退出",
						"pipe", name, "cause", werr)
					break
				}
			}
			if rerr != nil {
				// ERROR_BROKEN_PIPE = 客户端投递完毕正常断开，不是错误
				if rerr != windows.ERROR_BROKEN_PIPE {
					log().Warn("读输入通道出错", "pipe", name, "cause", rerr)
				}
				break
			}
		}
		_ = windows.DisconnectNamedPipe(srv)
	}
}
```

`pipeSecurityAttributes` 用到了 `unsafe.Sizeof`，import 里要加 `"unsafe"`。

- [ ] **Step 5: 加关键节点日志**

上面的实现已按 instrumenting-code 的清单布点，此步逐条自检：
- 进入关键操作：`openInputChannel` 成功后 `Info("Windows 输入通道已就位", pipe, path)`
- 外部调用前后：`createPipeServer` 失败 `Error` 带 pipe/path/cause；中继受理失败 `Error`；读出错 `Warn`
- 每个错误分支都带上下文（管道名 + 通道路径 + cause）
- 状态变更：中继退出打 `Info`
- 成功路径不静默：`createInputChannel` 的 Debug、`openInputChannel` 的 Info
- 高频路径降级：`createInputChannel` 用 Debug（`waitInputReader` 轮询里不打日志）

- [ ] **Step 6: 加注释**

自检：文件头「职责 + 边界」+ 两段 why（为什么匿名管道+中继、为什么服务端是 shim）；四个原语各有导出级文档；`createInputChannel` 写明「nil 表示无事可做不表示已验证」；`createPipeServer` 写明两条安全要求各自防的是什么；`relayPipe` 写明失败取舍是有意为之。

- [ ] **Step 7: 跑交叉编译与 vet**

Run:
```
GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=arm64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
```
Expected: 全绿

> **不要写「Windows 测试通过」**——执行机是 macOS，这些测试在这里一次都没跑过。如实记「已交叉编译与 vet，运行期未验」。

- [ ] **Step 8: 跑完工六门**

Expected: 全绿

- [ ] **Step 9: Commit**

```bash
git add internal/prochost/
git commit -m "feat(prochost): Windows 输入通道（命名管道服务端+中继+匿名管道）"
```

---

## Task 5: CI 的 windows-latest job 加 `go test`

Go 侧在 Windows 上一行测试都没跑过。Task 4 的中继、匿名管道不 EOF、抢占防护全是运行期行为，在 macOS 上写多少单测都碰不到。

**Files:**
- Modify: `.github/workflows/ci.yml:57-68`

**Interfaces:**
- Consumes: Task 4 的 `inputch_windows_test.go`

- [ ] **Step 1: 改 workflow**

在 `powershell` job 里，`install.ps1 单测（PowerShell 7）` 那一步之后追加：

```yaml
      # Go 侧此前在 Windows 上一行测试都没跑过：命名管道中继、匿名管道不 EOF、
      # 管道名抢占防护、AF_UNIX 往返，全是运行期行为，在 Linux/macOS runner 上
      # 写多少单测都碰不到。交叉编译门禁只防编译期回归，防不住运行期语义错配
      # （B128 实证：syscall.O_NONBLOCK 在 Windows 上有定义、编译得过、运行期必炸）。
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Windows 运行期单测
        run: go test ./internal/prochost/... ./internal/executor/claudecode/... -count=1
```

同时把该 job 的名字从 `powershell` 改为 `windows`，并在 job 上方加一行注释说明它现在不止跑 PowerShell：

```yaml
  # windows：Windows runner 上的运行期验证。install.ps1 的 PowerShell 单测 +
  # Go 侧真正需要 Windows 内核语义的那些包。
  windows:
    runs-on: windows-latest
```

- [ ] **Step 2: 本地校验 YAML 可解析**

Run: `python3 -c "import yaml,sys; d=yaml.safe_load(open('.github/workflows/ci.yml')); print(sorted(d['jobs'].keys()))"`
Expected: 输出含 `windows`，不含 `powershell`

> 若本机没有 pyyaml，改用 `python3 -c "import json,subprocess"` 不可行——直接跳过本步，但必须在 commit 信息里写明「YAML 未本地校验」，由 CI 首跑兜底。**不要假装校验过。**

- [ ] **Step 3: 加注释**

Step 1 的两段注释即为本步产出，自检它们回答了「为什么加」而不是「加了什么」。

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: windows job 追加 go test，覆盖 Windows 运行期语义"
```

---

## Task 6: 注册层解禁 claude 与 grok

**Files:**
- Create: `internal/executor/grok/symlinkcap.go`
- Create: `internal/executor/grok/symlinkcap_test.go`
- Modify: `cmd/agentd.go:324-340`
- Test: `cmd/agentd_test.go`（追加）

**Interfaces:**
- Produces: `func grok.SymlinkCapability(probeDir string) (supported bool, reason string)` —— 在 probeDir 下试建一个符号链接再删掉，报告本机是否具备该能力。

- [ ] **Step 1: 写失败的测试**

新建 `internal/executor/grok/symlinkcap_test.go`：

```go
package grok

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSymlinkCapabilityOnUnix 钉住 unix 上恒可用——那里建符号链接不需要特权。
func TestSymlinkCapabilityOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("本用例只描述 unix 上的恒真行为")
	}
	ok, reason := SymlinkCapability(t.TempDir())
	if !ok {
		t.Fatalf("unix 上竟然报不支持: %s", reason)
	}
	if reason != "" {
		t.Fatalf("支持时 reason 应为空，实得 %q", reason)
	}
}

// TestSymlinkCapabilityLeavesNothingBehind 钉住探测不留垃圾：它跑在 DataDir 下，
// 每次 agentd 启动都会执行，留一个就是每次启动留一个。
func TestSymlinkCapabilityLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if ok, reason := SymlinkCapability(dir); !ok && runtime.GOOS != "windows" {
		t.Fatalf("探测失败: %s", reason)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录失败: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("探测后目录不干净，残留 %v", names)
	}
}

// TestSymlinkCapabilityUnwritableDir 钉住探测目录不可用时给出的是理由而不是 panic。
func TestSymlinkCapabilityUnwritableDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ok, reason := SymlinkCapability(missing)
	if ok {
		t.Fatalf("目录不存在时竟然报支持")
	}
	if reason == "" {
		t.Fatalf("不支持时必须给出理由")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run TestSymlinkCapability -count=1`
Expected: FAIL，报 `undefined: SymlinkCapability`

- [ ] **Step 3: 实现**

新建 `internal/executor/grok/symlinkcap.go`：

```go
// symlinkcap.go —— grok 的符号链接能力探测。
//
// 职责：回答「本机现在能不能建符号链接」，供 agentd 决定是否注册 grok。
//
// 边界：
//   - 只探测，不注册、不报错到调用方之外：结论由 agentd 呈现
//   - 不缓存：agentd 启动时探一次即可，权限在运行中变化不是要覆盖的场景
//
// 为什么 grok 需要这个而别的执行器不需要：grok 给每个任务建一条指向用户
// auth 文件的符号链接（taskenv.go 建、authsync.go 周期复位），而 Windows 上
// 建符号链接需要 SeCreateSymbolicLinkPrivilege（管理员）或开发者模式。
//
// 为什么不改成复制文件绕开：软链的意义是 auth 文件只有一份权威副本。改成
// 复制后，grok 在任务里刷新 token 写的是副本，用户那份与任务那份各自漂移，
// 且这种不一致是静默的——正是 B26 那一整类问题。宁可诚实拒绝，不静默降级。
package grok

import (
	"fmt"
	"os"
	"path/filepath"
)

// SymlinkCapability 探测本机是否具备创建符号链接的能力。
//
// 参数：probeDir 为探测目录（用 DataDir，它一定存在且可写）
//
// 返回：
//   - supported: 是否可用
//   - reason: 不可用的原因，**含可行动的处置建议**；可用时为空串
//
// 注意：探测会在 probeDir 下建一个临时软链再删掉，正常路径不留任何残留。
func SymlinkCapability(probeDir string) (supported bool, reason string) {
	link := filepath.Join(probeDir, ".handoff-symlink-probe")
	// 先清掉上次异常退出可能留下的残留，否则 Symlink 会因已存在而失败，
	// 把一台其实可用的机器误判成不可用
	_ = os.Remove(link)
	if err := os.Symlink(probeDir, link); err != nil {
		return false, fmt.Sprintf("在 %s 下建符号链接失败: %v"+
			"（Windows 上需要管理员权限或开启开发者模式）", probeDir, err)
	}
	if err := os.Remove(link); err != nil {
		// 建成了但删不掉：能力是有的，只是留了个残留。不因此拒绝注册，
		// 但要让人看见——下次启动的 os.Remove 会把它清掉
		return true, ""
	}
	return true, ""
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -run TestSymlinkCapability -count=1 -v`
Expected: PASS，三条全过

- [ ] **Step 5: 改注册层**

`cmd/agentd.go`，把 `adaptersFor` 改为：

```go
// adaptersFor 按平台能力构造执行器注册表。
//
// 参数：goos 为目标平台；logger 用于播报不注册的理由
//
// 返回：可用执行器的注册表。
//
// 注意：
//   - **不注册必须有明确理由并打日志**：静默缺席会让用户以为是配置问题，
//     而 dispatch 在门口被拒时只知道「没这个执行器」
//   - grok 走**运行期能力探测**而不是按平台写死：它卡的是符号链接权限，
//     而那是部署形态决定的（管理员 / 开发者模式），同一个 Windows 上装法
//     不同结论就不同。写死等于把一台其实可用的机器判成不可用
func adaptersFor(goos string, logger *slog.Logger) map[string]executor.Adapter {
	return adaptersForWithProbe(goos, logger, os.TempDir())
}

// adaptersForWithProbe 是 adaptersFor 的可测形态：探测目录由调用方给。
func adaptersForWithProbe(goos string, logger *slog.Logger, probeDir string) map[string]executor.Adapter {
	ads := map[string]executor.Adapter{
		"opencode": opencode.New(logger),
		"codex":    codex.New(logger),
		"claude":   claudecode.New(logger),
		"fake":     fake.New(nil),
	}
	if supported, reason := grok.SymlinkCapability(probeDir); supported {
		ads["grok"] = grok.New(logger)
	} else {
		logger.Warn("不注册 grok：本机不具备创建符号链接的能力",
			"reason", reason,
			"note", "grok 用软链让 auth 文件只有一份权威副本，改成复制会让用户那份与任务那份静默漂移")
	}
	_ = goos // 平台不再直接决定注册面，保留参数是为了不改调用方与既有测试
	return ads
}
```

> **注意 `_ = goos` 这一行**：若 `goos` 在改动后确实不再被任何分支使用，Go 不会因未使用的**参数**报错，这一行只是给读者一个明确信号。若你发现留着它反而让人困惑，可以删掉 `_ = goos` 并在函数文档里写一句「goos 现已不参与判定，保留是为了签名稳定」——两种都行，但**不要删掉参数本身**，那会波及既有测试。

同时删掉 `adaptersFor` 上方那段已经过时的注释（`codex 照常注册但记为「未验」而非「支持」…`），改为：

```go
// claude 从 B128 起在所有平台注册：Windows 的输入通道（inputch_windows.go）
// 与裁决 socket（AF_UNIX，Windows 原生支持）都已落地。
```

- [ ] **Step 6: 加 agentd 层的测试**

追加到 `cmd/agentd_test.go`：

```go
// TestAdaptersForRegistersClaudeOnAllPlatforms 钉住 B128 的核心结论：
// claude 不再按平台拒绝。
func TestAdaptersForRegistersClaudeOnAllPlatforms(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		ads := adaptersForWithProbe(goos, slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
		if _, ok := ads["claude"]; !ok {
			t.Fatalf("goos=%s 时 claude 未注册", goos)
		}
	}
}

// TestAdaptersForSkipsGrokWhenSymlinkUnavailable 钉住 grok 走能力探测：
// 探测目录不可用时必须不注册，而不是注册了等运行期炸。
func TestAdaptersForSkipsGrokWhenSymlinkUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ads := adaptersForWithProbe("windows", slog.New(slog.NewTextHandler(io.Discard, nil)), missing)
	if _, ok := ads["grok"]; ok {
		t.Fatalf("符号链接不可用时 grok 仍被注册")
	}
	// 其余执行器不受影响：一个执行器不可用不该拖垮整张注册表
	for _, name := range []string{"opencode", "codex", "claude", "fake"} {
		if _, ok := ads[name]; !ok {
			t.Fatalf("%s 被误伤，未注册", name)
		}
	}
}
```

- [ ] **Step 7: 加关键节点日志**

自检：不注册 grok 时打 `Warn` 且带**可行动的** reason 与 note（说明为什么不能退化成复制文件）。注册成功的情况不单独打日志——agentd 启动时已有 `executor 探测` 那行汇总，重复打是噪音。

- [ ] **Step 8: 加注释**

自检：`symlinkcap.go` 文件头含职责/边界 + 两段 why（为什么只有 grok 需要、为什么不能改成复制）；`SymlinkCapability` 的导出文档写明 reason 必须可行动；`adaptersFor` 写明「不注册必须有理由」与「grok 为何走探测而非写死」。

- [ ] **Step 9: 跑测试**

Run: `go test ./cmd/ ./internal/executor/grok/ -count=1`
Expected: PASS

- [ ] **Step 10: 跑完工六门**

Expected: 全绿

- [ ] **Step 11: Commit**

```bash
git add cmd/agentd.go cmd/agentd_test.go internal/executor/grok/
git commit -m "feat(agentd): 解禁 Windows 上的 claude，grok 改为注册期能力探测"
```

---

## Task 7: 裁决 socket 的 Windows 适配

AF_UNIX 在 Windows 上原生可用（spec §3.1 已实测），传输代码零改动。要改的只有安全论证的表述与一处超长路径的错误信息。

**Files:**
- Modify: `internal/executor/claudecode/perm.go:1-14`（文件头注释）与 `newPermServer`
- Test: `internal/executor/claudecode/perm_test.go`（追加）

**Interfaces:**
- Consumes: 无
- Produces: 无新导出

- [ ] **Step 1: 写失败的测试**

追加到 `internal/executor/claudecode/perm_test.go`：

```go
// TestNewPermServerRejectsOverlongPath 钉住超长路径给出的是可读错误。
//
// AF_UNIX 的 sun_path 上限 108 字节，而 DataDir 可以被配到很深的位置。
// 不加这层包装时，用户看到的是 net.Listen 的原始错误（"invalid argument"
// 一类），完全指不到「路径太长」这个真因。
func TestNewPermServerRejectsOverlongPath(t *testing.T) {
	deep := filepath.Join(t.TempDir(), strings.Repeat("d", 120), "perm.sock")
	_, err := newPermServer(deep, slog.New(slog.NewTextHandler(io.Discard, nil)), func(permAsk) {})
	if err == nil {
		t.Fatalf("超长 socket 路径竟然绑定成功了")
	}
	if !strings.Contains(err.Error(), "过长") {
		t.Fatalf("错误没点明「路径过长」这个真因: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run TestNewPermServerRejectsOverlongPath -count=1`
Expected: FAIL——错误信息里没有「过长」

- [ ] **Step 3: 加长度前置检查**

在 `internal/executor/claudecode/perm.go` 的 `newPermServer` 里，`net.Listen` 之前加：

```go
	// AF_UNIX 的 sun_path 上限是 108 字节（含结尾 NUL），三大平台一致。
	// 不在这里拦下的话，用户拿到的是 net.Listen 的原始错误，指不到真因。
	// 上限取 107 是给结尾 NUL 留位。
	const sunPathMax = 107
	if len(sockPath) > sunPathMax {
		return nil, fmt.Errorf("裁决 socket 路径过长（%d 字节，上限 %d）: %s"+
			"——把 DataDir 配到更浅的位置", len(sockPath), sunPathMax, sockPath)
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -run TestNewPermServerRejectsOverlongPath -count=1 -v`
Expected: PASS

- [ ] **Step 5: 改安全论证的注释**

`internal/executor/claudecode/perm.go` 文件头现在写着：

```
// 为什么用 unix socket 而不是 agentd 的 HTTP 口：被监管的 executor 不该拿到
// agentd token；socket 文件落在 0700 的任务目录内，权限即边界，且无需分配端口。
```

Windows 上没有 POSIX 权限位（实测 socket 文件 mode 显示为 `Srw-rw-rw-`），这句在那里是**假的**。改为：

```
// 为什么用 unix socket 而不是 agentd 的 HTTP 口：被监管的 executor 不该拿到
// agentd token；socket 文件落在任务目录内，由**目录本身的访问控制**构成边界，
// 且无需分配端口。
//
// 「访问控制」分平台：unix 上是任务目录的 0700 权限位；Windows 上没有 POSIX
// 权限位（socket 文件的 mode 恒显示为 Srw-rw-rw-），边界由任务目录的 NTFS ACL
// 继承提供。**这不是同一句话的两种说法**——换平台时要重新确认边界真的成立，
// 不能因为 unix 上验过就假定 Windows 上也成立。
//
// AF_UNIX 在 Windows 10 1803+ / Server 2019+ 上原生可用，Go 的 net 包支持它
// （src/net/unixsock_posix.go 的构建约束含 windows），B128 已在 Server 2025 上
// 跨进程实测 Listen/Dial/双向收发全通，且 Close() 会自动删除 socket 文件。
```

- [ ] **Step 6: 加关键节点日志**

`newPermServer` 的路径过长分支返回的 error 已含全部上下文（实际长度、上限、路径、处置建议），调用方会记录它，此处不重复打日志。**这是显式决定**：同一个失败打两遍会让 agentd.log 里出现看似两次故障的记录。

- [ ] **Step 7: 加注释**

自检：Step 3 的常量与检查有「为什么」注释；Step 5 的文件头改动已把安全论证分平台写清，并明确标注「换平台要重新确认」。

- [ ] **Step 8: 跑完工六门**

Expected: 全绿

- [ ] **Step 9: Commit**

```bash
git add internal/executor/claudecode/perm.go internal/executor/claudecode/perm_test.go
git commit -m "fix(claudecode): 裁决 socket 的安全论证分平台表述，超长路径给可行动错误"
```

---

## Task 8: README 与文档同步

**Files:**
- Modify: `README.md`（「各 executor 须知」一节）

- [ ] **Step 1: 找到需要改的段落**

Run: `grep -n "Windows" README.md | head -20`

把其中声称「Windows 上 claude / grok 不可用」的表述改为现状。

- [ ] **Step 2: 改写**

把 Windows 相关的执行器可用性描述改为：

```markdown
Windows 执行机上四个执行器的现状：

| 执行器 | 状态 | 说明 |
|---|---|---|
| opencode | 可用 | B37 真机验收通过 |
| codex | 可用 | B123 真机验收通过 |
| claude | 可用 | 输入通道走命名管道 + 中继，裁决 socket 走 AF_UNIX（Windows 原生支持） |
| grok | 取决于部署形态 | 需要创建符号链接的权限：agentd 以管理员身份运行，或开启开发者模式。agentd 启动时会探测并在日志里说明 |
```

若 README 里没有这样一节，就在「各 executor 须知」下新增。

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs(readme): 更新 Windows 执行机上四个执行器的可用性现状"
```

---

## 审核者专属（**不要执行这一节**）

以下步骤需要驱动 handoff 自身（起 agentd、派任务、读回收日志），与本 plan 头部
「不得调用 handoff CLI」的纪律直接冲突，**由审核者在 Windows 真机上执行**。
执行者读到这里就算完成，不要尝试，也不要在 ledger 里声称验过。

**前置（必须先做，否则以下全部无法进行）**：
1. 收紧云安全组：22 与 3389 的来源从 `0.0.0.0/0` 改为特定出口 IP。该机 Administrator 账户正被公网爆破反复锁定（B127），可用窗口约一分钟。
2. 拉起 agentd：`handoff-agentd` 计划任务存在但处于 `Ready`（未运行）。

（spec §5.3 的 ACL 前置**已实测通过**，无需再验：`~/.handoff/tasks` 只授权 SYSTEM / Administrators / Administrator，没有 `Users` 也没有 `Everyone`。）

**验收剧本**（spec §10）：

| # | 内容 | 判据 |
|---|---|---|
| 1 | 注册面 | `handoff status` 列出 claude / codex / fake / grok / opencode；agentd 日志有 grok 的符号链接探测记录 |
| 2 | claude 全链路 | dispatch → 权限门拦截产工单 → `reply --approve` → `completed` |
| 3 | **多轮投递不 EOF** | `continue` 至少两次，每次都被响应 |
| 4 | `deny` 路径 | 拒绝后模型收到理由；目标文件未被改动 |
| 5 | 活过 agentd 重启 | 先确认 agentd 的 pid 真的消失，再断言 shim 与 claude 的 pid 不变地存活；新 agentd 日志 `recovered=1` |
| 6 | `done` 零残留 | 进程、managed worktree、任务目录三样都清干净 |
| 7 | grok 全链路 | 同第 2 条，外加确认 auth 软链真的建成（`Get-Item` 看到 ReparsePoint） |
| 8 | codex 全链路（B123） | 五动作走完 |

第 3 条与第 5 条都要求**先证明「该没有的确实没有」再断言「该有的还在」**——这是 B37 §12.5 那条教训（假 PASS 连续两次）的同款。

---

## 自审

**1. Spec 覆盖**

| Spec 章节 | 落在哪个 Task |
|---|---|
| §3.1 AF_UNIX 可用（订正） | Task 7（注释订正 + 长度检查） |
| §3.2 命名管道直接当 stdin 不可行（订正） | Task 4（文件头 why + 双轮投递测试） |
| §4.1 服务端归属必须是 shim | Task 4（文件头 why + `TestWindowsCreateInputChannelIsNoop` 钉住 agentd 侧不建服务端） |
| §4.2 数据流（匿名管道 + 中继） | Task 4 |
| §4.3 三个原语的平台映射 | Task 1（写）、Task 4（另两个） |
| §4.4 写入必须下沉 | Task 1 |
| §4.5 shim 的第二个平台钩子 | Task 2 |
| §4.6 管道命名与抢占防护 | Task 3（命名）、Task 4（`FIRST_PIPE_INSTANCE` + 安全描述符 + 抢占测试） |
| §5.1 继续用 AF_UNIX | Task 7 |
| §5.2 socket 文件残留无需处理 | 无需代码，Task 7 的注释记录了实测结论 |
| §5.3 安全论证分平台 + ACL 待验 | Task 7（注释）、审核者专属（ACL 实测） |
| §5.4 路径长度 | Task 7 |
| §6 grok 能力探测 | Task 6 |
| §7 codex 纯验收 | 审核者专属第 8 条 |
| §8.1 no-op 的风险由分工消解 | Task 4（`createInputChannel` 的文档 + 测试） |
| §8.2 抢占失败要在 shim.log 里说清 | Task 4（`createPipeServer` 失败时 `openInputChannel` 打 Error 带 pipe/path/cause） |
| §8.3 中继失败取舍 | Task 4（`relayPipe` 的文档 + 实现） |
| §9.1 CI 加 go test | Task 5 |
| §9.2 交叉编译不是可用性证据 | Global Constraints + Task 4 Step 7 的告诫 |
| §10 验收剧本 | 审核者专属 |
| §11 已知边界 | 审核者专属的「前置」 |

无遗漏。

**2. 占位符扫描**

无 TBD / TODO / 「类似 Task N」/ 「加上适当的错误处理」。每个代码步骤都给了可直接落地的代码。Task 8 Step 1 用 `grep` 定位而非写死行号，因为 README 的行号会随其它改动漂移——这是**故意的**，不是含糊。

**3. 类型一致性**

- `pipeNameFor(path string) string` —— Task 3 定义，Task 4 的 `createInputChannel` / `waitInputReader` / `writeInputChannel` / `openInputChannel` 四处使用，签名一致
- `openInputChannel(path string) (io.ReadCloser, func(), error)` —— Task 2 定义 unix 版与两个桩，Task 4 实现 Windows 版，Task 2 Step 5 的 shim 调用点按此签名接收，一致
- `writeInputChannel(path string, data []byte) error` —— Task 1 定义，Task 4 实现 Windows 版，一致
- `WriteInputChannel(path string, data []byte) error` —— Task 1 导出，Task 1 Step 6 的 adapter、Task 2 与 Task 4 的测试使用，一致
- `SymlinkCapability(probeDir string) (bool, string)` —— Task 6 定义并在同 task 的 `adaptersForWithProbe` 中使用，一致
- `adaptersForWithProbe(goos string, logger *slog.Logger, probeDir string)` —— Task 6 定义，同 task 的测试使用，一致
