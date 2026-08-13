# W4 PTY 终端 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把控制台中央的终端 tab 从占位说明接成真正可用的 shell——本机与远程开发机都行，跑着长任务时关掉页面走人、回来接着看。

**Architecture:** 新增独立包 `internal/ptyhost` 承担纯进程+字节的 PTY 托管（按平台分文件，与 `internal/prochost` 同形）；agentd 侧只做接口层（REST 管会话 + `/ws/pty` 接流），远程机器由本机 agentd 用一个全新的 WS 反代拨过去对拷；前端 `TerminalTab` 用 xterm 重写，会话真相在服务端，刷新后按服务端列表恢复。

**Tech Stack:** Go 1.x + `github.com/creack/pty`（新增直接依赖）+ `coder/websocket`（已有）+ `net/http` ServeMux 方法路由；前端 React + `@xterm/xterm` / `@xterm/addon-fit` / `@xterm/addon-webgl`（新增）+ vitest。

**Spec:** [2026-08-12-w4-pty-terminal-design.md](../specs/2026-08-12-w4-pty-terminal-design.md)

## Global Constraints

- **平台分文件用 `_unix` / `_other` 后缀，不用 `_windows`**：Go 把 `_windows.go` 当成隐式 GOOS 约束，`//go:build !unix` 写在 `_windows.go` 里永远不会被编译。照抄 `internal/prochost/platform_unix.go` / `platform_other.go` 的命名。
- **`windows_build_test.go` 是现成闸门**：它在仓库根跑 `GOOS=windows go build ./...`，新包一落地就自动纳入。任何 unix-only 符号泄漏到无标签文件里都会让它变红。
- **配置新键必须 `omitempty`**：配置以 `yaml.KnownFields(true)` 严格解析，未知键让 agentd **启动失败**。新版 `Save` 写出旧版不认识的键 = 顶死一台还没换版的机器。
- **默认值只能在用的时候取，不能在 `Load` 时填进结构体**：填了就会被下一次 `Save` 落盘，`omitempty` 形同虚设。判定恒为「字段 nil → 用内置默认；字段非 nil → 完全以配置为准（含显式 `[]` = 一个都不转发）」。
- **指针三态**：`*bool` / `*int` 配 `omitempty`，`nil` 恒表示「对端没上报」，绝不猜。同 `StatusResp.Update` / `StatusResp.Proc`。
- **日志用 `s.log` / 注入的 `*slog.Logger`，禁止 `fmt.Printf`**。主令牌、ticket 明文、cookie 明文一律不得进日志；设备名、会话 id、`base_path` 可以。
- **`base_path` 白名单是参数校验，不是安全边界**（spec §1/§5.2）。代码注释必须写明，不得复述 W4 spec §2.6 那套已被证伪的安全说辞。
- **契约 fixture 是逐字节钉死的**：改 `internal/proto` 的结构体后必须跑 `go test ./internal/proto/ -run TestContractFixtures -update` 重新生成，并同步 `web/src/api/types.ts`。
- **每个实现类 task 必须含「加关键节点日志」与「加意图注释」两个 step**（`instrumenting-code`）：错误分支带上下文、成功路径不静默、新文件有职责/边界头注释、导出方法有 doc 注释。

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/ptyhost/ring.go` | 256 KiB 定长环形缓冲：按绝对字节序号写入与回取，`truncated` 判定 |
| `internal/ptyhost/platform_unix.go` | `//go:build unix`。openpty + 起 login shell + resize + 进程组终止 + 退出码换算 |
| `internal/ptyhost/platform_other.go` | `//go:build !unix`。同签名桩，一律 `ErrNotSupported`，`ptySupported=false` |
| `internal/ptyhost/ptyhost.go` | `Host`：会话表、订阅广播、最小尺寸协商、生命周期。无平台标签 |
| `internal/ptyhost/envforward.go` | `env_forward` 三级解析（inherited / resolved / unavailable），三态各一条日志 |
| `internal/config/config.go` | 新增 `EnvForward []string \`yaml:"env_forward,omitempty"\`` + 未知键提示串 |
| `internal/proto/pty.go` | `PtySession` / 请求响应体 / WS 控制帧类型 |
| `internal/proto/status.go` | `StatusResp` 增 `PtySupported *bool` 与 `PtySessions *int`；新增 `PtyFootprintRow` |
| `internal/proto/projects.go` | `Machine` 增 `PtySupported *bool`（探活时从 `StatusResp` 投影） |
| `internal/prochost/footprint.go` | 新增 `CountGroup`：数一个进程组的成员数（终端会话足迹用） |
| `internal/agentd/pty_api.go` | REST 三个端点 + `?machine=` 转发接线 + `ptyFootprint` |
| `internal/agentd/pty_ws.go` | `/ws/pty`：Accept → Attach → 回放 → 双向搬字节 |
| `internal/agentd/forward_ws.go` | WS 反代：拨远端 → Accept 本地 → 双向对拷。`forwardTo` 的 WS 孪生 |
| `cmd/status.go` / `cmd/footprint.go` | 终端会话进可见性账本的两处渲染 |
| `web/src/api/client.ts` | 三个 PTY REST 函数（与其余端点同处一个模块，沿用私有 `request`） |
| `web/src/api/pty.ts` | `connectPty`：`ws.ts` 的孪生，字节游标续传 + 二进制帧 |
| `web/src/app/workbench/tabs.ts` | `TabContent` 终端项增 `sessionId?`，`dedupKey` 据此去重 |
| `web/src/app/workbench/useWorkbench.ts` | 增 `restoreTerminal`：写进目标目录的 tab 组但不切换选中态 |
| `web/src/app/workbench/TerminalTab.tsx` | xterm 重写：建会话 / 接流 / resize / 重连 / 退出展示 |
| `web/src/app/workbench/usePtyRestore.ts` | 加载时按服务端会话列表恢复终端 tab；会话 → `BaseDir` 的反解 |
| `web/src/app/workbench/WorkbenchPage.tsx` | `renderContent` 扩签名；`onBeforeClose` 拦截；终端不可用时的透传 |
| `web/src/app/workbench/BlankTab.tsx` | 终端不可用时**不渲染**该项，改说一句实话（不置灰） |
| `web/src/app/data/usePtySupport.ts` | 每台机器的 PTY 能力三态（加载时拉一次 `/api/machines`） |
| `web/src/app/shell/Shell.tsx` | 接线 restore、能力降级门、关 tab 前的确认与删会话 |

---

### Task 1: `internal/ptyhost` 环形缓冲

**Files:**
- Create: `internal/ptyhost/ring.go`
- Test: `internal/ptyhost/ring_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `type ring struct`；`newRing(size int) *ring`；`(*ring).write(p []byte)`；`(*ring).since(from uint64) (data []byte, start uint64, truncated bool)`；`(*ring).total() uint64`

- [ ] **Step 1: 写失败测试**

`internal/ptyhost/ring_test.go`：

```go
package ptyhost

import (
	"bytes"
	"testing"
)

// 环没写满时，since(0) 必须原样回放全部内容且不报截断。
func TestRingSinceWithinCapacity(t *testing.T) {
	r := newRing(16)
	r.write([]byte("hello"))
	data, start, truncated := r.since(0)
	if string(data) != "hello" || start != 0 || truncated {
		t.Fatalf("since(0) = (%q, %d, %v)，期望 (\"hello\", 0, false)", data, start, truncated)
	}
	if r.total() != 5 {
		t.Fatalf("total = %d，期望 5", r.total())
	}
}

// 表驱动钉住 since 的三类边界：环内续传、被覆盖后截断、游标越界。
func TestRingSinceBoundaries(t *testing.T) {
	r := newRing(8)
	r.write([]byte("0123456789ab")) // 写 12 字节，环容量 8 → 最旧可用字节序号为 4

	cases := []struct {
		name      string
		from      uint64
		wantData  string
		wantStart uint64
		wantTrunc bool
	}{
		{"从头请求被截断到环头", 0, "456789ab", 4, true},
		{"正好命中环头不算截断", 4, "456789ab", 4, false},
		{"环内续传", 10, "ab", 10, false},
		{"游标等于总量返回空", 12, "", 12, false},
		{"游标越界按当前尾部处理", 99, "", 12, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, start, truncated := r.since(c.from)
			if string(data) != c.wantData || start != c.wantStart || truncated != c.wantTrunc {
				t.Fatalf("since(%d) = (%q, %d, %v)，期望 (%q, %d, %v)",
					c.from, data, start, truncated, c.wantData, c.wantStart, c.wantTrunc)
			}
		})
	}
}

// 单次写入超过环容量时，只保留最后 size 个字节，且 total 仍按真实写入量累加。
func TestRingWriteLargerThanCapacity(t *testing.T) {
	r := newRing(4)
	r.write([]byte("abcdefgh"))
	data, start, truncated := r.since(0)
	if !bytes.Equal(data, []byte("efgh")) || start != 4 || !truncated {
		t.Fatalf("since(0) = (%q, %d, %v)，期望 (\"efgh\", 4, true)", data, start, truncated)
	}
	if r.total() != 8 {
		t.Fatalf("total = %d，期望 8", r.total())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/ptyhost/ -run TestRing -v
```

预期：编译失败 `undefined: newRing`。

- [ ] **Step 3: 写最小实现**

`internal/ptyhost/ring.go`：

```go
package ptyhost

// ring 是一个定长字节环形缓冲，用于 PTY 输出的断线续传回放。
//
// 核心不变式：写入的第 i 个字节（从 0 计）恒落在 buf[i%len(buf)]。因此只要知道
// 累计写入量 n，就能算出任一绝对序号还在不在环里——不需要额外维护头尾指针。
//
// 并发：ring 自身不加锁，由持有它的 session 统一在锁内调用。
type ring struct {
	buf []byte
	n   uint64 // 累计写入的字节数，也是下一个字节的绝对序号
}

func newRing(size int) *ring {
	return &ring{buf: make([]byte, size)}
}

// total 返回累计写入的字节数，即下一个字节的绝对序号。
func (r *ring) total() uint64 { return r.n }

// write 把 p 追加进环。p 超过环容量时只保留末尾 len(buf) 个字节，
// 但 n 仍按 len(p) 累加——n 是「输出了多少」，不是「留下了多少」。
func (r *ring) write(p []byte) {
	size := len(r.buf)
	if size == 0 {
		r.n += uint64(len(p))
		return
	}
	// 超容量时前面的部分必然会被自己覆盖掉，直接丢，只搬最后一段。
	if len(p) > size {
		r.n += uint64(len(p) - size)
		p = p[len(p)-size:]
	}
	off := int(r.n % uint64(size))
	c := copy(r.buf[off:], p)
	if c < len(p) { // 跨过环尾，剩下的绕回开头
		copy(r.buf, p[c:])
	}
	r.n += uint64(len(p))
}

// since 回取绝对序号 from 之后的全部字节。
//
// 返回：
//   - data: 实际能给出的字节（新分配的副本，调用方可在锁外持有）
//   - start: data 首字节的绝对序号；调用方据此推进自己的游标
//   - truncated: from 早于环里最旧的字节，中间有一段永久丢失
//
// 注意：from 大于 total 时（客户端报了一个未来的游标，通常是换了会话）按当前
// 尾部处理，返回空而不是报错——重连路径上宁可少画一段，也不能把连接打掉。
func (r *ring) since(from uint64) (data []byte, start uint64, truncated bool) {
	size := uint64(len(r.buf))
	oldest := uint64(0)
	if r.n > size {
		oldest = r.n - size
	}
	if from < oldest {
		from, truncated = oldest, true
	}
	if from > r.n {
		from = r.n
	}
	out := make([]byte, 0, r.n-from)
	for i := from; i < r.n; i++ {
		out = append(out, r.buf[i%size])
	}
	return out, from, truncated
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/ptyhost/ -run TestRing -v
```

预期：3 个测试全 PASS。

- [ ] **Step 5: 加意图注释自检**

ring 是纯数据结构、无 I/O 无错误分支，按 `instrumenting-code` 的边界属于「不需要日志」的一类——但**注释不豁免**。确认：
- 文件顶部有「核心不变式：第 i 个字节落在 buf[i%len(buf)]」这一句（它是全部三个方法能成立的唯一理由）
- `write` 里超容量分支、`since` 里 `from > r.n` 分支各有一句「为什么」而不是「做了什么」

- [ ] **Step 6: 提交**

```bash
git add internal/ptyhost/ring.go internal/ptyhost/ring_test.go && git commit -m "feat(ptyhost): 定长环形缓冲，支撑 PTY 输出断线续传"
```

---

### Task 2: `internal/ptyhost` 平台原语

**Files:**
- Create: `internal/ptyhost/platform_unix.go`、`internal/ptyhost/platform_other.go`、`internal/ptyhost/errors.go`
- Test: `internal/ptyhost/platform_unix_test.go`
- Modify: `go.mod`（新增 `github.com/creack/pty`）

**Interfaces:**
- Consumes: 无
- Produces: `var ErrNotSupported error`；`const ptySupported bool`；
  `startPty(shell, cwd string, env []string, cols, rows int) (*os.File, *exec.Cmd, error)`；
  `resizePty(f *os.File, cols, rows int) error`；
  `terminatePty(cmd *exec.Cmd) error`；`killPty(cmd *exec.Cmd) error`；
  `waitExitCode(cmd *exec.Cmd) int`

- [ ] **Step 1: 拉依赖**

```bash
go get github.com/creack/pty@latest && go mod tidy
```

确认 `go.mod` 的 require 块里出现 `github.com/creack/pty`，且它没有带进任何传递依赖。

- [ ] **Step 2: 写失败测试**

`internal/ptyhost/platform_unix_test.go`：

```go
//go:build unix

package ptyhost

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// startPty 必须能起一个真 shell，回显可读，尺寸按传入值生效。
func TestStartPtyEchoAndSize(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"TERM=xterm-256color", "PATH=/usr/bin:/bin"}, 120, 40)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()

	if _, err := f.WriteString("stty size; exit\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	_ = f.SetReadDeadline(time.Now().Add(10 * time.Second))
	var got string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "40 120") {
			got = sc.Text()
			break
		}
	}
	if got == "" {
		t.Fatalf("PTY 输出里没读到 `40 120`（stty size 的 行 列 顺序）")
	}
}

// resizePty 改尺寸后，shell 里 stty size 必须读到新值。
func TestResizePty(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = killPty(cmd); _ = f.Close() }()

	if err := resizePty(f, 100, 30); err != nil {
		t.Fatalf("resizePty: %v", err)
	}
	if _, err := f.WriteString("stty size; exit\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	_ = f.SetReadDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(f)
	found := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), "30 100") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("resize 之后 stty size 没读到 `30 100`")
	}
}

// shell 正常退出时，waitExitCode 返回它的退出码。
func TestWaitExitCodeNormal(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("exit 7\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	if code := waitExitCode(cmd); code != 7 {
		t.Fatalf("exit code = %d，期望 7", code)
	}
}

// 被信号杀掉时，退出码换算为 128+signo（SIGKILL=9 → 137）。
// 这是 shell 的通行约定，前端直接展示这个数字，不能是 -1。
func TestWaitExitCodeSignal(t *testing.T) {
	_, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	if err := killPty(cmd); err != nil {
		t.Fatalf("killPty: %v", err)
	}
	if code := waitExitCode(cmd); code != 137 {
		t.Fatalf("exit code = %d，期望 137（128+SIGKILL）", code)
	}
}

// terminatePty 打的是进程组：shell 的子进程也要一起走，否则关会话会留孤儿。
func TestTerminatePtyKillsProcessGroup(t *testing.T) {
	f, cmd, err := startPty("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("startPty: %v", err)
	}
	defer func() { _ = f.Close() }()
	// 起一个后台子进程并打印它的 pid
	if _, err := f.WriteString("sleep 300 & echo CHILD=$!\n"); err != nil {
		t.Fatalf("写入 PTY: %v", err)
	}
	_ = f.SetReadDeadline(time.Now().Add(10 * time.Second))
	child := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if i := strings.Index(sc.Text(), "CHILD="); i >= 0 {
			_, _ = fmtSscan(strings.TrimSpace(sc.Text()[i+len("CHILD="):]), &child)
			break
		}
	}
	if child == 0 {
		t.Fatal("没读到子进程 pid")
	}
	if err := terminatePty(cmd); err != nil {
		t.Fatalf("terminatePty: %v", err)
	}
	_ = waitExitCode(cmd)
	// 给内核一点时间收割
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := os.FindProcess(child); err != nil || p.Signal(nil) != nil {
			return // 子进程已不在，符合预期
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("终止会话后子进程 %d 仍存活，进程组没打中", child)
}
```

测试里的 `fmtSscan` 是 `fmt.Sscan` 的别名，加在同一文件顶部的 import 后：

```go
var fmtSscan = func(s string, a ...any) (int, error) { return fmt.Sscan(s, a...) }
```

（同时把 `"fmt"` 加进 import。用别名只是为了让上面的调用点一眼看出它在解析 pid，不引入新依赖。）

- [ ] **Step 3: 跑测试确认失败**

```bash
go test ./internal/ptyhost/ -run 'TestStartPty|TestResize|TestWaitExit|TestTerminate' -v
```

预期：编译失败 `undefined: startPty`。

- [ ] **Step 4: 写共享错误定义**

`internal/ptyhost/errors.go`：

```go
package ptyhost

import "errors"

// ErrNotSupported 表示当前平台没有 PTY 实现（Windows：ConPTY 是另一套 API，
// 本轮如实降级而不假装支持，见 spec §10）。
//
// 这个变量刻意放在**无构建标签**的文件里：两套 platform_*.go 都要引用它，
// 放进任一带标签的文件都会让另一套编译不过。
var ErrNotSupported = errors.New("当前平台不支持 PTY 终端")
```

- [ ] **Step 5: 写 unix 实现**

`internal/ptyhost/platform_unix.go`：

```go
//go:build unix

// PTY 的 unix 平台原语：开伪终端、起 login shell、调尺寸、按进程组终止。
//
// 职责：
//   - 把 openpty 与进程组信号这两件平台相关的事收敛在本文件
//   - 向上只暴露与 platform_other.go 完全同签名的五个函数
//
// 边界：
//   - 不认识会话、缓冲、订阅者——那是 ptyhost.go 的事
//   - 不做参数校验（shell 是否存在等），失败原样上抛
package ptyhost

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// ptySupported 是本平台的能力常量，经 Host.Supported() 上报到 /api/status。
const ptySupported = true

// startPty 在新伪终端里启动一个 login shell。
//
// 参数：
//   - shell: shell 的绝对路径；cwd: 工作目录；env: 完整环境（不追加 os.Environ）
//   - cols/rows: 初始尺寸
//
// 返回：PTY 主端 fd、已启动的 cmd、错误。
//
// 注意：
//   - 用 `-l` 起 login shell，rc 链照读——用户要的是「和我在 iTerm 里一样」
//   - pty.StartWithSize 内部设置了 Setsid+Setctty，因此 shell 的 pid 即 pgid，
//     terminatePty 才能用 -pid 打整个进程组
//   - env 是**完整替换**：调用方必须自己把要继承的变量拼进来（见 envforward.go）
func startPty(shell, cwd string, env []string, cols, rows int) (*os.File, *exec.Cmd, error) {
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cwd
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, nil, err
	}
	return f, cmd, nil
}

// resizePty 调整伪终端尺寸，内核随即向前台进程组发 SIGWINCH。
func resizePty(f *os.File, cols, rows int) error {
	return pty.Setsize(f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// terminatePty 向整个进程组发 SIGTERM。
//
// 为什么是 -pid 而不是 pid：用户在终端里起的 `npm run dev`、`sleep 300 &` 都是
// shell 的子进程，只杀 shell 会留下一堆孤儿。startPty 用 Setsid 保证了 pid==pgid。
//
// 进程已经不在时（ESRCH）视为成功——它本来就是我们想要的终局。
func terminatePty(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGTERM) }

// killPty 是 terminatePty 的强制版，用于宽限期结束后的兜底。
func killPty(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGKILL) }

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, sig)
	if err != nil && errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// waitExitCode 阻塞等待 shell 退出并换算退出码。
//
// 被信号杀掉时返回 128+signo（SIGKILL → 137），这是 shell 的通行约定，
// 前端直接展示这个数字；返回 -1 会让用户看到一个没有含义的负数。
func waitExitCode(cmd *exec.Cmd) int {
	err := cmd.Wait()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}
```

- [ ] **Step 6: 写非 unix 桩**

`internal/ptyhost/platform_other.go`：

```go
//go:build !unix

// PTY 平台原语在非 unix 平台上的桩实现。
//
// 职责：让 `GOOS=windows go build ./...` 能过，并把「不支持」这个事实
// 沿 ErrNotSupported 一路传到 HTTP 层（501）与 /api/status 的 pty_supported=false。
//
// 边界：不做任何模拟。Windows 的 ConPTY 是另一套 API，本轮不假装支持（spec §10）。
//
// 文件名用 _other 而不是 _windows：Go 会把 _windows.go 当成隐式 GOOS 约束，
// 那样 `//go:build !unix` 里除 windows 外的其它非 unix 平台就没有实现了。
// 与 internal/prochost/platform_other.go 同款。
package ptyhost

import (
	"os"
	"os/exec"
)

const ptySupported = false

func startPty(shell, cwd string, env []string, cols, rows int) (*os.File, *exec.Cmd, error) {
	return nil, nil, ErrNotSupported
}

func resizePty(f *os.File, cols, rows int) error { return ErrNotSupported }

func terminatePty(cmd *exec.Cmd) error { return ErrNotSupported }

func killPty(cmd *exec.Cmd) error { return ErrNotSupported }

// waitExitCode 在本平台永远不会被调用（会话根本起不来），返回 -1 表示未知。
func waitExitCode(cmd *exec.Cmd) int { return -1 }
```

- [ ] **Step 7: 跑测试与跨平台闸门**

```bash
go test ./internal/ptyhost/ -v && GOOS=windows go build ./... && go vet ./internal/ptyhost/
```

预期：unix 测试全 PASS，windows 构建通过，vet 无输出。

- [ ] **Step 8: 加关键节点日志**

本层刻意**不打日志**：它没有 logger 参数，且每个错误都原样上抛给 `ptyhost.go`，由那一层带着会话 id 记录——同一个错误记两遍反而让日志难读。**在 `platform_unix.go` 的包注释里写明这个决定**，否则下一个人会以为是漏了：

```go
// 日志：本文件不打日志。所有错误原样上抛，由 ptyhost.go 带着会话 id 统一记录，
// 避免同一个失败在两层各留一条无法关联的记录。
```

- [ ] **Step 9: 提交**

```bash
git add go.mod go.sum internal/ptyhost/ && git commit -m "feat(ptyhost): PTY 平台原语（unix 实现 + 非 unix 桩）"
```

---

### Task 3: `ptyhost.Host` 会话表与广播

**Files:**
- Create: `internal/ptyhost/ptyhost.go`
- Test: `internal/ptyhost/ptyhost_test.go`

**Interfaces:**
- Consumes: Task 1 的 `newRing/write/since/total`；Task 2 的 `startPty/resizePty/terminatePty/killPty/waitExitCode/ptySupported/ErrNotSupported`
- Produces:
  - `type Host struct`；`func New(log *slog.Logger) *Host`；`func (*Host) Supported() bool`
  - `type OpenOptions struct { BasePath, BaseKind, Shell string; Env []string; Cols, Rows int }`
  - `type Session struct { ID, BasePath, BaseKind, Shell string; CreatedAt time.Time; Cols, Rows, Attached, PID int; ExitCode *int; BytesOut uint64 }`
  - `func (*Host) Open(OpenOptions) (Session, error)`；`List() []Session`；`Get(id string) (Session, bool)`；`Close(id string) error`；`Write(id string, p []byte) error`；`Attach(id string, since uint64) (*Attachment, error)`
  - `type Attachment struct { Backlog []byte; Since uint64; Truncated bool; Out <-chan []byte }`；`func (*Attachment) Resize(cols, rows int) error`；`func (*Attachment) Detach()`；`func (*Attachment) ExitCode() *int`
  - `var ErrNoSession, ErrTooManySubscribers, ErrSessionExited error`

- [ ] **Step 1: 写失败测试**

`internal/ptyhost/ptyhost_test.go`：

```go
//go:build unix

package ptyhost_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/ptyhost"
)

func testHost(t *testing.T) *ptyhost.Host {
	t.Helper()
	return ptyhost.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testOpen(t *testing.T, h *ptyhost.Host) ptyhost.Session {
	t.Helper()
	s, err := h.Open(ptyhost.OpenOptions{
		BasePath: t.TempDir(), BaseKind: "workspace", Shell: "/bin/sh",
		Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color"}, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = h.Close(s.ID) })
	return s
}

// waitFor 反复调用 read 直到输出里出现 want 或超时。
// PTY 输出是流式的，不能假设一次读就拿到完整行。
func waitFor(t *testing.T, out <-chan []byte, want string) string {
	t.Helper()
	var sb strings.Builder
	deadline := time.After(10 * time.Second)
	for {
		select {
		case b, ok := <-out:
			if !ok {
				t.Fatalf("订阅通道已关闭，累计输出:\n%s", sb.String())
			}
			sb.Write(b)
			if strings.Contains(sb.String(), want) {
				return sb.String()
			}
		case <-deadline:
			t.Fatalf("等待 %q 超时，累计输出:\n%s", want, sb.String())
		}
	}
}

// 最基本的一条：开会话 → 写命令 → 从订阅里读到回显。
func TestOpenWriteAttach(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	if s.PID <= 0 || s.ExitCode != nil {
		t.Fatalf("新会话应有 pid 且 exit_code 为 nil，实得 pid=%d exit=%v", s.PID, s.ExitCode)
	}
	a, err := h.Attach(s.ID, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Detach()
	if err := h.Write(s.ID, []byte("echo HANDOFF_OK\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, a.Out, "HANDOFF_OK")
}

// 两个订阅者必须都收到同一份输出（tmux 语义，spec §3.3）。
func TestBroadcastToAllSubscribers(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a1, _ := h.Attach(s.ID, 0)
	defer a1.Detach()
	a2, err := h.Attach(s.ID, 0)
	if err != nil {
		t.Fatalf("第二次 Attach: %v", err)
	}
	defer a2.Detach()
	if got, _ := h.Get(s.ID); got.Attached != 2 {
		t.Fatalf("attached = %d，期望 2", got.Attached)
	}
	_ = h.Write(s.ID, []byte("echo BOTH\n"))
	waitFor(t, a1.Out, "BOTH")
	waitFor(t, a2.Out, "BOTH")
}

// 尺寸取所有订阅者里最小的那个：大屏一 resize 就把小屏刷成乱码是不可接受的。
func TestResizeTakesMinimumAcrossSubscribers(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a1, _ := h.Attach(s.ID, 0)
	defer a1.Detach()
	a2, _ := h.Attach(s.ID, 0)
	defer a2.Detach()

	if err := a1.Resize(200, 60); err != nil {
		t.Fatalf("a1.Resize: %v", err)
	}
	if err := a2.Resize(100, 30); err != nil {
		t.Fatalf("a2.Resize: %v", err)
	}
	got, _ := h.Get(s.ID)
	if got.Cols != 100 || got.Rows != 30 {
		t.Fatalf("生效尺寸 = %dx%d，期望 100x30（取最小）", got.Cols, got.Rows)
	}
	_ = h.Write(s.ID, []byte("stty size\n"))
	waitFor(t, a1.Out, "30 100")
}

// 断开后重连带 since，只补没看过的那段，不重复。
func TestAttachSinceResumes(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a1, _ := h.Attach(s.ID, 0)
	_ = h.Write(s.ID, []byte("echo FIRST\n"))
	waitFor(t, a1.Out, "FIRST")
	cursor := func() uint64 { g, _ := h.Get(s.ID); return g.BytesOut }()
	a1.Detach()

	_ = h.Write(s.ID, []byte("echo SECOND\n"))
	time.Sleep(500 * time.Millisecond) // 让输出落进环

	a2, err := h.Attach(s.ID, cursor)
	if err != nil {
		t.Fatalf("重连 Attach: %v", err)
	}
	defer a2.Detach()
	if a2.Truncated {
		t.Error("256 KiB 环装得下这点输出，不该报 truncated")
	}
	if strings.Contains(string(a2.Backlog), "FIRST") {
		t.Errorf("since 之前的内容不该再回放一遍，实得:\n%s", a2.Backlog)
	}
	if !strings.Contains(string(a2.Backlog), "SECOND") {
		t.Errorf("since 之后的内容必须补齐，实得:\n%s", a2.Backlog)
	}
}

// 订阅者上限 8：第 9 个必须被明确拒绝，不是静默丢弃。
func TestSubscriberLimit(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	for i := 0; i < 8; i++ {
		a, err := h.Attach(s.ID, 0)
		if err != nil {
			t.Fatalf("第 %d 个订阅者被拒: %v", i+1, err)
		}
		defer a.Detach()
	}
	if _, err := h.Attach(s.ID, 0); err != ptyhost.ErrTooManySubscribers {
		t.Fatalf("第 9 个订阅者的错误 = %v，期望 ErrTooManySubscribers", err)
	}
}

// shell 自己退出后：会话进终态、仍在列表里、exit_code 如实记录、订阅通道关闭。
func TestShellExitKeepsTerminalSession(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	a, _ := h.Attach(s.ID, 0)
	defer a.Detach()
	_ = h.Write(s.ID, []byte("exit 3\n"))

	deadline := time.After(10 * time.Second)
	for {
		g, ok := h.Get(s.ID)
		if !ok {
			t.Fatal("shell 自己退出不该让会话从列表消失（spec §3.2）")
		}
		if g.ExitCode != nil {
			if *g.ExitCode != 3 {
				t.Fatalf("exit_code = %d，期望 3", *g.ExitCode)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("等待 exit_code 超时")
		case <-time.After(50 * time.Millisecond):
		}
	}
	select {
	case _, ok := <-a.Out:
		if ok { // 可能还有残留输出，再收一次直到关闭
			for range a.Out {
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("会话退出后订阅通道必须关闭，否则前端不知道该停止重连")
	}
	if err := h.Write(s.ID, []byte("echo x\n")); err != ptyhost.ErrSessionExited {
		t.Fatalf("向已退出会话写入的错误 = %v，期望 ErrSessionExited", err)
	}
}

// 显式 Close 之后会话从列表消失，再操作一律 ErrNoSession。
func TestCloseRemovesSession(t *testing.T) {
	h := testHost(t)
	s := testOpen(t, h)
	if err := h.Close(s.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := h.Get(s.ID); ok {
		t.Fatal("显式关闭后会话必须从列表消失")
	}
	if len(h.List()) != 0 {
		t.Fatalf("List 长度 = %d，期望 0", len(h.List()))
	}
	if err := h.Close(s.ID); err != ptyhost.ErrNoSession {
		t.Fatalf("重复 Close 的错误 = %v，期望 ErrNoSession", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/ptyhost/ -run 'TestOpen|TestBroadcast|TestResizeTakes|TestAttachSince|TestSubscriber|TestShellExit|TestCloseRemoves' -v
```

预期：编译失败 `undefined: ptyhost.New`。

- [ ] **Step 3: 写实现**

`internal/ptyhost/ptyhost.go`：

```go
// Package ptyhost 托管伪终端（PTY）会话：开 shell、持有会话表、维护回放缓冲、
// 向多个订阅者广播输出、按进程组终止。
//
// 职责：
//   - 会话的完整生命周期：创建、写入、订阅、调尺寸、显式关闭、自然退出
//   - 每会话一个环形缓冲，支撑断线重连的 since 续传
//   - 多方接入时的尺寸协商（取所有订阅者中的最小值）
//
// 边界：
//   - 不认识 HTTP / WebSocket / JSON，也不认识 agentd 的任务模型
//   - 不做鉴权，不做 base_path 白名单校验（那是 agentd 接口层的参数校验）
//   - 不落盘：会话表只在内存里，随 agentd 生死（spec §3.1、§10）
//   - 不解析终端转义序列，只搬字节
package ptyhost

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// ringSize 是每会话的回放缓冲大小。256 KiB 够装满屏幕若干次滚动，
	// 又不至于让几十个会话把内存吃光。
	ringSize = 256 << 10
	// maxSubscribers 是单会话的订阅者上限。超出拒绝而不是静默丢弃（spec §3.3）。
	maxSubscribers = 8
	// subscriberBuffer 是单个订阅者的积压上限。写满说明这个客户端跟不上，
	// 此时**关掉它**而不是阻塞广播——它带着 since 重连即可从环里补齐，
	// 这正是环形缓冲存在的意义。
	subscriberBuffer = 256
	// termGrace 是 SIGTERM 到 SIGKILL 的宽限期。
	termGrace = 2 * time.Second
	// readChunk 是单次从 PTY 主端读取的上限。
	readChunk = 32 << 10
)

var (
	// ErrNoSession 表示会话 id 不存在（或已被显式关闭）。
	ErrNoSession = errors.New("终端会话不存在")
	// ErrTooManySubscribers 表示该会话的订阅者已达上限。
	ErrTooManySubscribers = errors.New("终端会话的连接数已达上限")
	// ErrSessionExited 表示 shell 已经退出，只能读历史不能再写。
	ErrSessionExited = errors.New("终端会话已退出")
)

// Session 是一个会话的**快照**，跨出锁之后可以自由持有。
//
// ExitCode 用指针表达三态里的两态：nil = 还活着，非 nil = 已退出且这是退出码。
// 这条与项目里 Watchers / Live / Procs 同一纪律——绝不用 0 或 -1 冒充「不知道」。
type Session struct {
	ID        string
	BasePath  string
	BaseKind  string
	Shell     string
	CreatedAt time.Time
	Cols      int
	Rows      int
	Attached  int
	PID       int
	ExitCode  *int
	BytesOut  uint64
}

// OpenOptions 是开会话的入参。Env 是**完整环境**，不会再追加 os.Environ()——
// 这正是 spec §4.2 要绕开的那个坑（封存分支 service_unix.go:43 的写法）。
type OpenOptions struct {
	BasePath string
	BaseKind string
	Shell    string
	Env      []string
	Cols     int
	Rows     int
}

// Host 是本机所有 PTY 会话的持有者。零值不可用，请用 New。
type Host struct {
	log  *slog.Logger
	mu   sync.Mutex
	sess map[string]*session
}

// New 创建一个 Host。log 用于记录会话生命周期与错误，不得为 nil。
func New(log *slog.Logger) *Host {
	return &Host{log: log, sess: map[string]*session{}}
}

// Supported 报告本平台是否支持 PTY，供 /api/status 的 pty_supported 上报。
func (h *Host) Supported() bool { return ptySupported }

type subscriber struct {
	ch   chan []byte
	cols int // 0 = 该订阅者还没报过尺寸，不参与最小值计算
	rows int
}

type session struct {
	mu       sync.Mutex
	meta     Session
	f        *os.File
	cmd      *exec.Cmd
	buf      *ring
	subs     map[*subscriber]struct{}
	exited   bool
	exitCode *int
}

// Open 起一个新会话。失败时不留残骸。
func (h *Host) Open(opt OpenOptions) (Session, error) {
	if !ptySupported {
		return Session{}, ErrNotSupported
	}
	if opt.Cols <= 0 {
		opt.Cols = 80
	}
	if opt.Rows <= 0 {
		opt.Rows = 24
	}
	f, cmd, err := startPty(opt.Shell, opt.BasePath, opt.Env, opt.Cols, opt.Rows)
	if err != nil {
		// shell 起不来是最常见的失败（$SHELL 指向不存在的路径、cwd 被删）。
		// 带齐 shell 与 cwd，否则这行日志无法定位。
		h.log.Error("开终端会话失败", "shell", opt.Shell, "cwd", opt.BasePath, "err", err)
		return Session{}, err
	}
	s := &session{
		meta: Session{
			ID: uuid.NewString(), BasePath: opt.BasePath, BaseKind: opt.BaseKind,
			Shell: opt.Shell, CreatedAt: time.Now(),
			Cols: opt.Cols, Rows: opt.Rows, PID: cmd.Process.Pid,
		},
		f: f, cmd: cmd, buf: newRing(ringSize), subs: map[*subscriber]struct{}{},
	}
	h.mu.Lock()
	h.sess[s.meta.ID] = s
	total := len(h.sess)
	h.mu.Unlock()

	go h.pump(s)
	go h.reap(s)

	h.log.Info("终端会话已创建", "session", s.meta.ID, "pid", s.meta.PID,
		"shell", opt.Shell, "base_kind", opt.BaseKind, "cwd", opt.BasePath,
		"size", fmtSize(opt.Cols, opt.Rows), "sessions", total)
	return s.snapshot(), nil
}

func fmtSize(cols, rows int) string {
	return strconv.Itoa(cols) + "x" + strconv.Itoa(rows)
}

// pump 是读循环：把 PTY 输出写进环并广播。PTY 主端在子进程退出后返回
// EIO（linux）或 EOF（darwin），两者都只意味着「读到头了」，不记为错误。
func (h *Host) pump(s *session) {
	b := make([]byte, readChunk)
	for {
		n, err := s.f.Read(b)
		if n > 0 {
			s.broadcast(b[:n])
		}
		if err != nil {
			h.log.Debug("终端会话输出流结束", "session", s.meta.ID, "err", err)
			return
		}
	}
}

// reap 等待 shell 退出，落 exit_code 并关闭所有订阅通道。
//
// 订阅通道的关闭是前端「停止重连」的唯一信号——不关，客户端会一直以为
// 只是网络抖动。
func (h *Host) reap(s *session) {
	code := waitExitCode(s.cmd)
	s.mu.Lock()
	s.exited = true
	s.exitCode = &code
	for sub := range s.subs {
		close(sub.ch)
	}
	s.subs = map[*subscriber]struct{}{}
	s.mu.Unlock()
	_ = s.f.Close()
	h.log.Info("终端会话已退出", "session", s.meta.ID, "pid", s.meta.PID, "exit_code", code)
}

func (s *session) broadcast(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.write(p)
	for sub := range s.subs {
		cp := make([]byte, len(p))
		copy(cp, p)
		select {
		case sub.ch <- cp:
		default:
			// 跟不上的订阅者：断开它而不是拖垮所有人。它重连时带 since，
			// 能从环里把这段补回来（可能带 truncated 标记）。
			close(sub.ch)
			delete(s.subs, sub)
		}
	}
}

func (s *session) snapshot() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.meta
	m.Attached = len(s.subs)
	m.ExitCode = s.exitCode
	m.BytesOut = s.buf.total()
	return m
}

func (h *Host) get(id string) (*session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sess[id]
	if !ok {
		return nil, ErrNoSession
	}
	return s, nil
}

// List 返回全部会话快照（含已退出但未被显式关闭的）。
func (h *Host) List() []Session {
	h.mu.Lock()
	all := make([]*session, 0, len(h.sess))
	for _, s := range h.sess {
		all = append(all, s)
	}
	h.mu.Unlock()
	out := make([]Session, 0, len(all))
	for _, s := range all {
		out = append(out, s.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Get 取单个会话快照。第二个返回值 false = 不存在。
func (h *Host) Get(id string) (Session, bool) {
	s, err := h.get(id)
	if err != nil {
		return Session{}, false
	}
	return s.snapshot(), true
}

// Write 把用户按键送进 PTY。会话已退出时返回 ErrSessionExited。
func (h *Host) Write(id string, p []byte) error {
	s, err := h.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return ErrSessionExited
	}
	if _, err := s.f.Write(p); err != nil {
		h.log.Error("向终端会话写入失败", "session", id, "bytes", len(p), "err", err)
		return err
	}
	return nil
}

// Close 显式关闭会话：整组 SIGTERM，宽限 termGrace 后 SIGKILL，并立即
// 把会话从列表里摘掉。
//
// 注意：摘除是同步的、杀进程的兜底是异步的——DELETE 请求不该为了等一个
// 赖着不走的进程而挂 2 秒。用户点了 ×，列表里就该立刻没有它。
func (h *Host) Close(id string) error {
	h.mu.Lock()
	s, ok := h.sess[id]
	if ok {
		delete(h.sess, id)
	}
	remain := len(h.sess)
	h.mu.Unlock()
	if !ok {
		return ErrNoSession
	}
	if err := terminatePty(s.cmd); err != nil {
		h.log.Error("终止终端会话失败", "session", id, "pid", s.meta.PID, "err", err)
	}
	go func() {
		time.Sleep(termGrace)
		s.mu.Lock()
		exited := s.exited
		s.mu.Unlock()
		if exited {
			return
		}
		h.log.Warn("终端会话在宽限期内未退出，强制终止",
			"session", id, "pid", s.meta.PID, "grace", termGrace)
		_ = killPty(s.cmd)
	}()
	h.log.Info("终端会话已关闭", "session", id, "pid", s.meta.PID, "sessions", remain)
	return nil
}

// Attachment 是一次订阅。Backlog 是建连瞬间的历史回放，Out 是后续实时输出；
// Out 被关闭意味着会话结束（不是网络抖动），客户端应停止重连。
type Attachment struct {
	Backlog   []byte
	Since     uint64
	Truncated bool
	Out       <-chan []byte

	h   *Host
	s   *session
	sub *subscriber
}

// Attach 订阅一个会话，并原子地取回 since 之后的历史。
//
// 「原子」是关键：回放与订阅必须在同一把锁里完成，否则两者之间产生的输出
// 会两头都不落，用户看到的历史就缺了一段。
func (h *Host) Attach(id string, since uint64) (*Attachment, error) {
	s, err := h.get(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.subs) >= maxSubscribers {
		s.mu.Unlock()
		h.log.Warn("终端会话连接数已达上限，拒绝新连接",
			"session", id, "limit", maxSubscribers)
		return nil, ErrTooManySubscribers
	}
	backlog, start, truncated := s.buf.since(since)
	sub := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	if s.exited {
		// 会话已退出：给历史、给一个已关闭的通道，让调用方走「灌完再报 exit」
		// 的正常路径，而不是把它当成一个错误。
		close(sub.ch)
	} else {
		s.subs[sub] = struct{}{}
	}
	attached := len(s.subs)
	s.mu.Unlock()

	h.log.Info("终端会话已接入", "session", id, "since", since,
		"backlog_bytes", len(backlog), "truncated", truncated, "attached", attached)
	return &Attachment{
		Backlog: backlog, Since: start, Truncated: truncated, Out: sub.ch,
		h: h, s: s, sub: sub,
	}, nil
}

// Resize 上报本订阅者的尺寸，并按「所有订阅者取最小」重新协商实际尺寸。
func (a *Attachment) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil // 客户端还没量出来，忽略而不是把 PTY 调成 0
	}
	s := a.s
	s.mu.Lock()
	a.sub.cols, a.sub.rows = cols, rows
	minC, minR := 0, 0
	for sub := range s.subs {
		if sub.cols <= 0 || sub.rows <= 0 {
			continue
		}
		if minC == 0 || sub.cols < minC {
			minC = sub.cols
		}
		if minR == 0 || sub.rows < minR {
			minR = sub.rows
		}
	}
	changed := minC > 0 && minR > 0 && (minC != s.meta.Cols || minR != s.meta.Rows)
	if changed {
		s.meta.Cols, s.meta.Rows = minC, minR
	}
	exited := s.exited
	s.mu.Unlock()
	if exited || !changed {
		return nil
	}
	if err := resizePty(s.f, minC, minR); err != nil {
		a.h.log.Error("调整终端尺寸失败", "session", s.meta.ID,
			"size", fmtSize(minC, minR), "err", err)
		return err
	}
	a.h.log.Debug("终端尺寸已协商", "session", s.meta.ID, "size", fmtSize(minC, minR))
	return nil
}

// Detach 退订。**只断连接，不动进程**——这是 spec §3.2 的核心分工：
// 关页面、切设备、组件卸载一律走这里，杀会话只有 Close 一条路。
func (a *Attachment) Detach() {
	s := a.s
	s.mu.Lock()
	if _, ok := s.subs[a.sub]; ok {
		delete(s.subs, a.sub)
		close(a.sub.ch)
	}
	attached := len(s.subs)
	s.mu.Unlock()
	a.h.log.Info("终端会话已断开连接", "session", s.meta.ID, "attached", attached)
}

// ExitCode 返回会话的退出码，nil = 还活着。
func (a *Attachment) ExitCode() *int {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	return a.s.exitCode
}
```

import 里补上 `"sort"` 与 `"strconv"`（`fmtSize` 与 `List` 用到）。`uuid` 用仓库已有的 `github.com/google/uuid`。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/ptyhost/ -race -v && GOOS=windows go build ./...
```

预期：全 PASS，无数据竞争，windows 构建仍通过。

- [ ] **Step 5: 加关键节点日志（自检）**

对照 `instrumenting-code` 逐条确认（上面的实现已内置，这一步是**核对**不是补写）：

| 关键节点 | 日志 |
|---|---|
| 开会话失败 | `Error` 带 shell / cwd / err |
| 开会话成功 | `Info` 带 session / pid / shell / base_kind / cwd / size / 当前会话总数 |
| shell 退出 | `Info` 带 session / pid / exit_code（成功路径不静默） |
| 接入 / 断开 | `Info` 带 session / since / backlog_bytes / truncated / attached |
| 连接数超限 | `Warn` 带 session / limit |
| 写入失败、resize 失败、终止失败 | `Error` 各带 session 与 err |
| 宽限期未退出 | `Warn` 带 session / pid / grace |
| 输出流结束 | `Debug`（高频路径降级，不刷屏） |

红线复核：**日志里不得出现 `Env` 的内容**——`env_forward` 转发的是 socket 路径，虽不是令牌，但环境变量整体打印极易在将来夹带凭据。只记变量名与三态结论（Task 4）。

- [ ] **Step 6: 加意图注释（自检）**

确认：包头注释写清职责与四条边界；`Session.ExitCode` 的指针三态、`subscriberBuffer` 写满即断的取舍、`Close` 的「同步摘除 + 异步兜底杀」、`Attach` 的「回放与订阅必须同锁」、`Detach` 的「只断连接不动进程」各有一段「为什么」。

- [ ] **Step 7: 提交**

```bash
git add internal/ptyhost/ && git commit -m "feat(ptyhost): 会话表、广播订阅与最小尺寸协商"
```

---

### Task 4: `env_forward` 会话级环境变量转发

**Files:**
- Create: `internal/ptyhost/envforward.go`
- Test: `internal/ptyhost/envforward_test.go`
- Modify: `internal/config/config.go`（新增 `EnvForward` 字段 + 未知键提示串）
- Test: `internal/config/config_test.go`（追加往返测试）

**Interfaces:**
- Consumes: 无（`ptyhost` 内部工具，被 Task 6 的 agentd 接口层调用）
- Produces:
  - `func DefaultEnvForward() []string`（返回 `["SSH_AUTH_SOCK"]` 的副本）
  - `func ResolveEnvForward(names []string, base []string, log *slog.Logger) []string`
  - `var launchctlGetenv func(name string) (string, bool)`（可打桩）
  - `config.Config.EnvForward []string`

- [ ] **Step 1: 写失败测试（三态 + 「不在 os.Environ 里」那条路径）**

`internal/ptyhost/envforward_test.go`：

```go
package ptyhost

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func hasKV(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// ① 继承：agentd 自身环境里就有 → 直接带过去，不去问 launchctl。
func TestResolveEnvForwardInherited(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/inherited.sock")
	launchctlGetenv = func(string) (string, bool) {
		t.Fatal("自身环境已有该变量时不该再调 launchctl")
		return "", false
	}
	t.Cleanup(func() { launchctlGetenv = launchctlGetenvReal })

	var buf bytes.Buffer
	out := ResolveEnvForward([]string{"SSH_AUTH_SOCK"}, []string{"PATH=/bin"}, slog.New(slog.NewTextHandler(&buf, nil)))
	if !hasKV(out, "SSH_AUTH_SOCK=/tmp/inherited.sock") {
		t.Fatalf("环境里应含继承来的值，实得 %v", out)
	}
	if !strings.Contains(buf.String(), "inherited") {
		t.Errorf("成功路径必须有声（source=inherited），实得日志:\n%s", buf.String())
	}
}

// ② 解析：**这是本轮修复的那个缺陷**——变量不在 os.Environ() 里（托管形态），
// 必须走平台解析。没有这条用例，缺陷会在开发者手起 agentd 的机器上永远绿。
func TestResolveEnvForwardResolved(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "") // 显式清空，模拟 launchd/systemd 托管形态
	launchctlGetenv = func(name string) (string, bool) {
		if name == "SSH_AUTH_SOCK" {
			return "/var/run/launchd/Listeners", true
		}
		return "", false
	}
	t.Cleanup(func() { launchctlGetenv = launchctlGetenvReal })

	var buf bytes.Buffer
	out := ResolveEnvForward([]string{"SSH_AUTH_SOCK"}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
	if !hasKV(out, "SSH_AUTH_SOCK=/var/run/launchd/Listeners") {
		t.Fatalf("应带上解析出的值，实得 %v", out)
	}
	if !strings.Contains(buf.String(), "resolved") {
		t.Errorf("解析成功必须记 source=resolved，实得日志:\n%s", buf.String())
	}
}

// ③ 探不到：如实记 unavailable，**不编造、不设默认值、不阻断会话创建**。
func TestResolveEnvForwardUnavailable(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	launchctlGetenv = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { launchctlGetenv = launchctlGetenvReal })

	var buf bytes.Buffer
	out := ResolveEnvForward([]string{"SSH_AUTH_SOCK"}, []string{"PATH=/bin"}, slog.New(slog.NewTextHandler(&buf, nil)))
	for _, e := range out {
		if strings.HasPrefix(e, "SSH_AUTH_SOCK=") {
			t.Fatalf("探不到时绝不能凭空造一个值，实得 %q", e)
		}
	}
	if !hasKV(out, "PATH=/bin") {
		t.Error("base 环境必须原样保留")
	}
	if !strings.Contains(buf.String(), "unavailable") {
		t.Errorf("必须留下可搜的 unavailable 结论，实得日志:\n%s", buf.String())
	}
}

// 默认清单是内置常量，调用方拿到的是副本——改它不该影响下一次调用。
func TestDefaultEnvForwardIsCopy(t *testing.T) {
	a := DefaultEnvForward()
	if len(a) != 1 || a[0] != "SSH_AUTH_SOCK" {
		t.Fatalf("默认清单 = %v，期望 [SSH_AUTH_SOCK]", a)
	}
	a[0] = "TAMPERED"
	if DefaultEnvForward()[0] != "SSH_AUTH_SOCK" {
		t.Fatal("DefaultEnvForward 必须返回副本，不能把内置清单暴露出去")
	}
}
```

`internal/config/config_test.go` 追加（照 `TestPathDirsRoundTripAndOmitEmpty` 的形）：

```go
// env_forward 能被读进来，且**未配置时绝不落盘**。
//
// why：这是 spec §4.2 那条「默认值只能在用的时候取」的钉子。实现者最顺手的写法
// 是在 Load 里把内置默认 ["SSH_AUTH_SOCK"] 填进结构体——那样下一次 Save 就会把
// env_forward 写进每台机器的 config.yaml，一台还没换版的旧 agentd 读到这个未知键
// 会直接**启动失败**，而所有功能测试仍然全绿。
func TestEnvForwardRoundTripAndOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")

	cfg, err := config.Load(p) // 首次运行：生成默认配置并写盘
	if err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	if cfg.EnvForward != nil {
		t.Errorf("Load 不得把默认值填进结构体，实得 %v", cfg.EnvForward)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回配置: %v", err)
	}
	if strings.Contains(string(b), "env_forward") {
		t.Errorf("未配置时 env_forward 不得落盘，实得:\n%s", b)
	}

	// 显式空列表要能被区分出来：它表示「一个都不转发」，不是「没配过」。
	cfg.EnvForward = []string{}
	if err := config.Save(p, cfg); err != nil {
		t.Fatalf("写盘: %v", err)
	}

	cfg.EnvForward = []string{"SSH_AUTH_SOCK", "GPG_AGENT_INFO"}
	if err := config.Save(p, cfg); err != nil {
		t.Fatalf("写盘: %v", err)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if len(got.EnvForward) != 2 || got.EnvForward[0] != "SSH_AUTH_SOCK" {
		t.Errorf("env_forward = %v，期望 [SSH_AUTH_SOCK GPG_AGENT_INFO]", got.EnvForward)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/ptyhost/ -run TestResolveEnvForward -v; go test ./internal/config/ -run TestEnvForward -v
```

预期：两边都编译失败（`undefined: ResolveEnvForward`、`cfg.EnvForward undefined`）。

- [ ] **Step 3: 加配置字段**

`internal/config/config.go`，紧挨 `PathDirs` 之后（两者是邻居，§8.3）：

```go
	// EnvForward 是要转发进终端会话的环境变量名单（见 internal/ptyhost）。
	//
	// 它解决的是 PathDirs 解决不了的**另一类**问题：SSH_AUTH_SOCK 这类变量由
	// launchd / ssh-agent **按会话注入**，不来自任何 dotfile，因此 login shell
	// 的 rc 链**无法**像恢复 PATH 那样把它恢复出来。agentd 以服务形态托管时，
	// 终端里的 ssh / git push 会因此全部失败。
	//
	// 三态语义（**不要**在 Load 里填默认值）：
	//   nil        → 用内置默认清单 ptyhost.DefaultEnvForward()（当前是 SSH_AUTH_SOCK）
	//   非 nil     → 完全以配置为准
	//   []（显式） → 一个都不转发
	// 一旦 Load 把默认值填进结构体，下一次 Save 就会把 env_forward 落进
	// config.yaml，omitempty 形同虚设，旧 agentd 照样被顶死。
	//
	// omitempty 是硬要求，理由同 PathDirs（B59 spec D7）。
	EnvForward []string `yaml:"env_forward,omitempty"`
```

同时把 `env_forward` 加进 `config.go:306` 的未知字段提示串：`.../repo_root/path_dirs/env_forward/stalltimeout/...`。

- [ ] **Step 4: 写解析实现**

`internal/ptyhost/envforward.go`：

```go
// 会话级环境变量转发：把 SSH_AUTH_SOCK 这类「由会话注入、不来自 dotfile」的
// 变量解析出来，注入单个终端会话的环境。
//
// 职责：
//   - 按三级顺序解析每个变量：继承 → 平台查询 → 探不到
//   - 逐个变量记录三态结论，让「终端里 git push 失败」变成一行可搜的日志
//
// 边界：
//   - 只产出**这个会话的** cmd.Env，**绝不写回 agentd 自身环境**。这与
//     internal/pathenv 相反：PATH 是进程级恒定事实，socket 路径是会话级易变
//     事实，写回会让后续所有 fork 拿到一个可能已经失效的路径。
//   - 探不到就是探不到，不编造默认值（spec §4.2）
//   - 解析失败一律降级为 unavailable，不阻断会话创建
package ptyhost

import (
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// defaultEnvForward 是内置默认转发清单。配置里没写（nil）时用它。
//
// 只有 SSH_AUTH_SOCK 一个：它是**实测确认**会因托管形态丢失、且丢失后
// 直接让 git push / ssh 失效的那一个。其余变量按需由用户在配置里显式追加，
// 不预先塞一堆猜的。
var defaultEnvForward = []string{"SSH_AUTH_SOCK"}

// DefaultEnvForward 返回内置默认清单的副本。
//
// 返回副本而不是切片本身：调用方（config 解析、测试）拿到后可能就地排序或改写，
// 那会污染进程内所有后续会话。
func DefaultEnvForward() []string {
	out := make([]string, len(defaultEnvForward))
	copy(out, defaultEnvForward)
	return out
}

// launchctlGetenv 是平台级变量查询，测试可整体替换。
var launchctlGetenv = launchctlGetenvReal

// launchctlGetenvReal 在 macOS 上用 `launchctl getenv <name>` 查会话级变量。
//
// 关于这次 fork：B73 要求「防线全链路零 fork」，那条约束的对象是**进程耗尽时
// 仍需工作的诊断路径**。会话创建本身就要 fork 一个 shell，此处多一次 fork
// 不改变可用性边界。
//
// Linux 不猜：systemd 用户会话下没有等价的稳定查询口径，直接返回 false，
// 由「继承 + 用户显式配置」两条路兜底。
func launchctlGetenvReal(name string) (string, bool) {
	if runtime.GOOS != "darwin" {
		return "", false
	}
	out, err := exec.Command("launchctl", "getenv", name).Output()
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", false
	}
	return v, true
}

// ResolveEnvForward 把 names 里每个变量按三级顺序解析后追加到 base，返回新环境。
//
// 参数：
//   - names: 要转发的变量名清单（调用方已按 nil→默认清单 归一化）
//   - base:  会话的基础环境（PATH / TERM 等），原样保留
//   - log:   逐个变量记录三态结论，不得为 nil
//
// 返回：base + 解析成功的 `NAME=VALUE`。探不到的变量**不出现**在结果里。
//
// 注意：日志只记变量名与结论来源，**不记变量值**——今天转发的是 socket 路径，
// 但这份清单是用户可配的，明天可能就有人往里加一个带凭据的变量。
func ResolveEnvForward(names []string, base []string, log *slog.Logger) []string {
	out := make([]string, 0, len(base)+len(names))
	out = append(out, base...)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if v := os.Getenv(name); v != "" {
			out = append(out, name+"="+v)
			log.Info("终端环境变量已转发", "name", name, "source", "inherited")
			continue
		}
		if v, ok := launchctlGetenv(name); ok {
			out = append(out, name+"="+v)
			log.Info("终端环境变量已转发", "name", name, "source", "resolved", "via", "launchctl")
			continue
		}
		// 成功路径与失败路径都有声：不然无法区分「解析失败」与「这段代码没跑」。
		log.Warn("终端环境变量无法解析，该会话里将没有它",
			"name", name, "source", "unavailable", "goos", runtime.GOOS)
	}
	return out
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/ptyhost/ ./internal/config/ -v 2>&1 | tail -30 && GOOS=windows go build ./...
```

预期：`internal/ptyhost` 与 `internal/config` 全 PASS，windows 构建通过（本文件无平台标签，`runtime.GOOS` 判断在编译期无碍）。

- [ ] **Step 6: 加关键节点日志（自检）**

三态各一条、逐个变量、都带 `name` 与 `source`——这条本身就是 spec §4.2 第 4 点的要求，也是验收标准 4「agentd 日志里能看到每个 `env_forward` 变量的三态结论」。核对：`inherited` / `resolved` 走 `Info`（成功路径不静默），`unavailable` 走 `Warn`（它预示一个将来会发生的 `git push` 失败）。**核对日志里没有变量值。**

- [ ] **Step 7: 加意图注释（自检）**

确认这四处「为什么」都在：不写回 agentd 自身环境（与 pathenv 相反的理由）、`DefaultEnvForward` 返回副本、Linux 不猜、B73 零 fork 约束为何不适用。

- [ ] **Step 8: 提交**

```bash
git add internal/ptyhost/envforward.go internal/ptyhost/envforward_test.go internal/config/ && git commit -m "feat(ptyhost): 会话级环境变量转发，修复托管形态下终端 ssh/git push 失效"
```

---

### Task 5: 线格式契约（`internal/proto` + fixture + TS 类型）

**Files:**
- Create: `internal/proto/pty.go`
- Modify: `internal/proto/status.go`（`StatusResp` 增 `PtySupported *bool`）
- Modify: `internal/proto/contract_fixture_test.go:62-79`（cases 表增两行 + 两个 sample 函数）
- Modify: `web/src/api/types.ts`、`web/src/api/contract.test.ts`
- Generated: `web/src/api/testdata/PtySession.json`、`PtySessionsResp.json`、`StatusResp.json`（重新生成）

**Interfaces:**
- Consumes: 无
- Produces: `proto.PtySession`、`proto.PtySessionsResp`、`proto.CreatePtySessionReq`、`proto.PtyControl` 与四个 `PtyCtrl*` 常量、`proto.StatusResp.PtySupported *bool`；TS 侧同名镜像类型

- [ ] **Step 1: 写 Go 契约类型**

`internal/proto/pty.go`：

```go
// PTY 终端会话的线格式类型（W4 PTY 终端 spec §3.1、§5）。
//
// 职责：定义 REST 与 /ws/pty 的请求/响应/控制帧形状。
// 边界：不含任何行为；会话真相在 internal/ptyhost，这里只是它的线格式投影。
package proto

import "time"

// PtySession 是一个终端会话的线格式投影。
//
// ExitCode 用指针表达三态里的两态：**缺席 = 还活着**，出现 = 已退出且这是退出码。
// 与 StatusResp.Update / StatusResp.Proc 同一纪律——不用 0 或 -1 冒充「不知道」。
type PtySession struct {
	ID string `json:"id"`
	// Machine 是**线注解，不入库**：""=本机，否则为汇总方 cfg.Targets 的键，
	// 由汇总方盖章。与 Task.Machine 同款。
	Machine   string    `json:"machine"`
	BasePath  string    `json:"base_path"`
	BaseKind  string    `json:"base_kind"` // "workspace" | "home"
	Shell     string    `json:"shell"`
	CreatedAt time.Time `json:"created_at"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	Attached  int       `json:"attached"`
	PID       int       `json:"pid"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	// BytesOut 是该会话累计输出的字节数，也是 /ws/pty 的 since 水位。
	BytesOut uint64 `json:"bytes_out"`
}

// PtySessionsResp 是 GET /api/pty/sessions 的响应。
// Machines 仅在 ?scope=all 时出现，形状与 ProjectTreeResp 一致。
type PtySessionsResp struct {
	Sessions []PtySession    `json:"sessions"`
	Machines []MachineStatus `json:"machines,omitempty"`
}

// CreatePtySessionReq 是 POST /api/pty/sessions 的请求体。
// BaseKind="home" 时 BasePath 被忽略（服务端用它自己的 $HOME，见 spec §5.2）。
type CreatePtySessionReq struct {
	BasePath string `json:"base_path"`
	BaseKind string `json:"base_kind"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
}

// /ws/pty 的 text 帧类型。binary 帧恒为 PTY 原始字节，不走 JSON。
const (
	PtyCtrlAttached = "attached" // 服务端 → 客户端，建连首帧
	PtyCtrlExit     = "exit"     // 服务端 → 客户端，shell 已退出
	PtyCtrlError    = "error"    // 服务端 → 客户端
	PtyCtrlResize   = "resize"   // 客户端 → 服务端
)

// PtyControl 是 /ws/pty 上双向共用的控制帧。
//
// 为什么一个结构体走两个方向：控制帧是低频路径（建连、退出、改尺寸），
// 拆成四个类型只会让两端各多三个分支。高频的数据路径**不经过它**——
// PTY 字节走 binary 帧，零解析零 base64 膨胀（spec §5.3）。
//
// Since / Truncated 刻意不带 omitempty：attached 帧里「从 0 开始」与
// 「没有截断」都是有意义的结论，缺键会让前端分不清「服务端说了 false」
// 和「服务端这版还不认识这个字段」。
type PtyControl struct {
	Type      string `json:"type"`
	Since     uint64 `json:"since"`
	Truncated bool   `json:"truncated"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Message   string `json:"message,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}
```

- [ ] **Step 2: 给 `StatusResp` 加能力位**

`internal/proto/status.go`，在 `Proc *ProcUsage` 附近追加：

```go
	// PtySupported 报告本机 agentd 是否支持 PTY 终端。
	//
	// 三态，与 Update / Proc 同一纪律：
	//   缺席(nil) = 对端 agentd 太老，没上报这个字段——**不许当成 false**
	//   false     = 平台不支持（Windows：ConPTY 是另一套 API，本轮不假装支持）
	//   true      = 支持
	// 前端据此决定画真终端、画「这台机器不支持」还是画「对端版本过旧，未上报」。
	PtySupported *bool `json:"pty_supported,omitempty"`
```

- [ ] **Step 3: 写失败的契约测试（Go 侧）**

`internal/proto/contract_fixture_test.go`：cases 表追加两行，并在文件末尾加样本函数。

```go
		{"PtySession", ptySessionSample(now)},
		{"PtySessionsResp", ptySessionsRespSample(now)},
```

```go
// ptySessionSample 返回 PtySession 的代表性样本（活着的会话：exit_code 缺席）。
func ptySessionSample(now time.Time) PtySession {
	return PtySession{
		ID:        "2f0f6a3c-8f1e-4f2a-9a77-1c2d3e4f5a6b",
		Machine:   "",
		BasePath:  "/home/dev/handoff",
		BaseKind:  "workspace",
		Shell:     "/bin/zsh",
		CreatedAt: now,
		Cols:      120,
		Rows:      40,
		Attached:  1,
		PID:       48213,
		BytesOut:  81920,
	}
}

// ptySessionsRespSample 覆盖 scope=all 信封：一条本机活会话 + 一条远端已退出会话
//（exit_code 出现），外加两行机器应答。
func ptySessionsRespSample(now time.Time) PtySessionsResp {
	code := 3
	remote := ptySessionSample(now)
	remote.ID = "9b8a7c6d-5e4f-4a3b-2c1d-0e9f8a7b6c5d"
	remote.Machine = "devbox"
	remote.Attached = 0
	remote.ExitCode = &code
	return PtySessionsResp{
		Sessions: []PtySession{ptySessionSample(now), remote},
		Machines: []MachineStatus{
			{Name: "", Ok: true, FetchedAt: now},
			{Name: "devbox", Ok: true, FetchedAt: now},
		},
	}
}
```

同时给 `statusSample` 加上能力位（`StatusResp` 多了字段，这个 fixture 必然要重生成）：

```go
	ptyOK := true
	// ...在 statusSample 构造的 StatusResp 里加：
	PtySupported: &ptyOK,
```

- [ ] **Step 4: 跑测试确认失败**

```bash
go test ./internal/proto/ -run TestContractFixtures
```

预期：`PtySession`/`PtySessionsResp` 报「读取 fixture 失败（契约尚未生成？请运行 -update）」，`StatusResp` 报「序列化结果与 fixture 不一致」。**这正是它该有的反应**——契约漂移必须先变红。

- [ ] **Step 5: 生成 fixture 并核对差异**

```bash
go test ./internal/proto/ -run TestContractFixtures -update && git diff --stat web/src/api/testdata/
```

逐行 review `git diff web/src/api/testdata/StatusResp.json`：应当**只**多出 `"pty_supported": true` 一行。多出别的 = 你不小心改了别的结构体。

- [ ] **Step 6: 写 TS 镜像类型与前端契约测试**

`web/src/api/types.ts` 末尾追加：

```typescript
// PtySession 是一个 PTY 终端会话（W4 PTY 终端 spec §3.1）。
//
// exit_code 缺席 = 会话还活着（Go 侧是 *int + omitempty）。不要写
// `session.exit_code ?? 0`——那会把「跑着的会话」显示成「正常退出」。
export interface PtySession {
  id: string
  machine: string        // ""=本机；否则为汇总方 cfg.Targets 的键
  base_path: string
  base_kind: string      // 'workspace' | 'home'
  shell: string
  created_at: string
  cols: number
  rows: number
  attached: number
  pid: number
  exit_code?: number
  bytes_out: number      // /ws/pty 的 since 水位
}

export interface PtySessionsResp {
  sessions: PtySession[]
  machines?: MachineStatus[]
}

export interface CreatePtySessionReq {
  base_path: string
  base_kind: string
  cols: number
  rows: number
}

// PtyControl 是 /ws/pty 上的 text 帧。二进制帧是 PTY 原始字节，不经过这里。
export interface PtyControl {
  type: string           // 'attached' | 'exit' | 'error' | 'resize'
  since: number
  truncated: boolean
  exit_code?: number
  message?: string
  cols?: number
  rows?: number
}
```

并在 `StatusResp` 接口里加：

```typescript
  // 缺席 = 对端 agentd 没上报（版本过旧），**不等于 false**。见 types 头注释的三态约定。
  pty_supported?: boolean
```

`web/src/api/contract.test.ts` 追加 import 与一个 describe：

```typescript
import ptySessionFixture from './testdata/PtySession.json'
import ptySessionsRespFixture from './testdata/PtySessionsResp.json'
// 并把 PtySession / PtySessionsResp 加进从 './types' 的 type import 列表

describe('PtySession 契约', () => {
  it('活着的会话：exit_code 缺席而不是 0', () => {
    const s: PtySession = ptySessionFixture
    expect(s.base_kind).toBe('workspace')
    expect(s.bytes_out).toBe(81920)
    expect('exit_code' in s).toBe(false)
  })

  it('scope=all 信封：远端会话带 machine 与 exit_code', () => {
    const resp = ptySessionsRespFixture as PtySessionsResp
    expect(resp.sessions).toHaveLength(2)
    expect(resp.sessions[0].machine).toBe('')
    expect(resp.sessions[1].machine).toBe('devbox')
    expect(resp.sessions[1].exit_code).toBe(3)
    expect(resp.machines?.map((m) => m.name)).toEqual(['', 'devbox'])
  })

  it('StatusResp：pty_supported 已上报', () => {
    const status = statusFixture as StatusResp
    expect(status.pty_supported).toBe(true)
  })
})
```

- [ ] **Step 7: 跑双侧测试确认通过**

```bash
go test ./internal/proto/ && (cd web && npm run typecheck && npx vitest run src/api/contract.test.ts)
```

预期：Go 侧 fixture 逐字节一致，TS 侧 typecheck 与契约断言全绿。

- [ ] **Step 8: 加意图注释（自检）**

本 task 全是类型定义，**没有可打日志的运行节点**——这一步只核对注释：`ExitCode` / `PtySupported` 的三态各有说明；`PtyControl` 为何双向共用一个结构体、`Since`/`Truncated` 为何不带 `omitempty`；TS 侧那句「不要写 `?? 0`」的反面教材。

- [ ] **Step 9: 提交**

```bash
git add internal/proto/ web/src/api/ && git commit -m "feat(proto): PTY 会话线格式契约与 pty_supported 能力位"
```

---

### Task 6: agentd REST 会话管理

**Files:**
- Create: `internal/agentd/pty_api.go`
- Test: `internal/agentd/pty_api_test.go`
- Modify: `internal/agentd/server.go`（Server 增 `pty` 字段、`NewServer` 构造、四条路由）
- Modify: `internal/agentd/status.go`（填 `PtySupported`）
- Modify: `internal/client/client.go`（增 `PtySessions`，供 `scope=all` 扇出）
- Modify: `internal/agentd/workspacefiles.go:1-18`（更正已被证伪的安全说辞）

**Interfaces:**
- Consumes: Task 3 的 `ptyhost.Host`；Task 4 的 `ptyhost.DefaultEnvForward` / `ResolveEnvForward`；Task 5 的 proto 类型；既有 `s.forwardIfRequested`、`s.resolveWorkspace`、`writeJSON`、`client.New(...).MarkForwarded()`
- Produces: `(*Server).handleListPtySessions/handleCreatePtySession/handleDeletePtySession`；`Server.pty *ptyhost.Host`；`(*client.Client).PtySessions(ctx) (*proto.PtySessionsResp, error)`

- [ ] **Step 1: 写失败测试**

`internal/agentd/pty_api_test.go`（照 `forward_test.go` 的既有约定：`Do` 的 err 必查、`defer resp.Body.Close()`）：

```go
package agentd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

func ptyPost(t *testing.T, env *testAgentdEnv, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, env.ts.URL+"/api/pty/sessions", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// base_path 不是本机已探测到的工作树 → 400 且文案说清是参数问题。
//
// 400 而不是 403：会话在能力上等价于主令牌（spec §1），白名单是参数校验
// 不是安全边界，不能再借安全的名义（spec §5.2）。
func TestCreatePtySessionRejectsUnknownBasePath(t *testing.T) {
	env := newTestAgentdEnv(t)
	resp, body := ptyPost(t, env, `{"base_path":"/nowhere/at/all","base_kind":"workspace","cols":80,"rows":24}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400；体=%s", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte("拒绝访问")) || bytes.Contains(body, []byte("权限")) {
		t.Errorf("文案不得再用安全口径，实得 %s", body)
	}
}

// home 基准：忽略传入的 base_path，直接用服务端 $HOME（spec §5.2）。
func TestCreatePtySessionHomeBase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持，另有降级用例")
	}
	env := newTestAgentdEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	resp, body := ptyPost(t, env, `{"base_path":"/攻击者传的路径","base_kind":"home","cols":100,"rows":30}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200；体=%s", resp.StatusCode, body)
	}
	var s proto.PtySession
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("解析响应: %v；体=%s", err, body)
	}
	if s.BasePath != home {
		t.Errorf("home 基准必须落在 $HOME=%s，实得 %s", home, s.BasePath)
	}
	if s.ExitCode != nil {
		t.Errorf("新会话的 exit_code 必须缺席，实得 %d", *s.ExitCode)
	}
	t.Cleanup(func() { env.srv.pty.Close(s.ID) })
}

// 列表 → 删除 → 再删 404。
func TestPtySessionListAndDelete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持，另有降级用例")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	_, body := ptyPost(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	var s proto.PtySession
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("解析响应: %v；体=%s", err, body)
	}

	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/pty/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	lb, _ := io.ReadAll(resp.Body)
	var list proto.PtySessionsResp
	if err := json.Unmarshal(lb, &list); err != nil {
		t.Fatalf("解析列表: %v；体=%s", err, lb)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != s.ID {
		t.Fatalf("列表 = %s，期望恰好含刚建的会话 %s", lb, s.ID)
	}
	if list.Machines != nil {
		t.Errorf("不带 scope=all 时不该有 machines 信封，实得 %s", lb)
	}

	del := func() int {
		r, _ := http.NewRequest(http.MethodDelete, env.ts.URL+"/api/pty/sessions/"+s.ID, nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		rr, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer rr.Body.Close()
		return rr.StatusCode
	}
	if code := del(); code != http.StatusOK {
		t.Fatalf("首次 DELETE = %d，期望 200", code)
	}
	if code := del(); code != http.StatusNotFound {
		t.Fatalf("重复 DELETE = %d，期望 404", code)
	}
}

// /api/status 必须上报能力位，且**不是 nil**——nil 是留给老版本的。
func TestStatusReportsPtySupported(t *testing.T) {
	env := newTestAgentdEnv(t)
	req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var st proto.StatusResp
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatalf("解析 status: %v", err)
	}
	if st.PtySupported == nil {
		t.Fatal("本版 agentd 必须上报 pty_supported，nil 只能表示对端版本过旧")
	}
	if *st.PtySupported != (runtime.GOOS != "windows") {
		t.Errorf("pty_supported = %v，与平台不符（GOOS=%s）", *st.PtySupported, runtime.GOOS)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestCreatePtySession|TestPtySession|TestStatusReportsPty' -v
```

预期：编译失败（`env.srv.pty` 未定义）+ 404（路由未注册）。

- [ ] **Step 3: 写 REST 实现**

`internal/agentd/pty_api.go`：

```go
// 本文件实现 PTY 终端会话的 HTTP 接口层。
//
// 职责：
//   - REST 三个端点：列会话（含 ?scope=all 扇出）、建会话、显式关会话
//   - 把 base_path / base_kind 归一化成 ptyhost.OpenOptions
//   - 组装会话环境：基础环境 + env_forward 解析结果
//
// 边界：
//   - **不持有任何会话状态**，全部转交 s.pty（internal/ptyhost）
//   - 不认识 PTY 的平台细节；平台不支持时只负责把 ErrNotSupported 翻成 501
//   - WS 数据通道在 pty_ws.go，反代在 forward_ws.go
package agentd

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/ptyhost"
)

// ptyFanoutBudget 是 ?scope=all 整轮扇出的总预算，与 treeFanoutBudget 同量级：
// 短于任何调用方超时，单台慢机器不拖垮整个列表。
const ptyFanoutBudget = 3 * time.Second

// sessionEnv 组装一个会话的完整环境。
//
// 基础三件：从 agentd 自身环境继承（PATH 已由 B71 的 pathenv.Apply 在任何 fork
// 之前补全过，见 spec §4.1），再钉死 TERM / COLORTERM。
//
// 之后叠加 env_forward 的解析结果：配置为 nil 时用内置默认清单，非 nil 时完全
// 以配置为准（含显式空列表 = 一个都不转发）。**默认值只在这里取，绝不回填进
// cfg**——回填会让下一次 Save 把它落进 config.yaml 顶死旧 agentd（spec §4.2）。
func (s *Server) sessionEnv() []string {
	base := append([]string{}, os.Environ()...)
	base = append(base, "TERM=xterm-256color", "COLORTERM=truecolor")
	names := s.cfg.EnvForward
	if names == nil {
		names = ptyhost.DefaultEnvForward()
	}
	return ptyhost.ResolveEnvForward(names, base, s.log)
}

// resolvePtyBase 把请求里的 base_kind/base_path 归一化成实际 cwd。
//
// **这是参数校验，不是安全边界。** 控制台会话在能力上等价于主令牌
//（POST /api/tasks/{id}/run 就是 sh -c，见 spec §1），白名单挡不住任何有心人
// ——终端里一条 `cd ~` 就出去了。它存在的唯一理由是：防止前端传一个打错的
// 路径、让 shell 起在文件系统某个莫名其妙的角落。因此失败是 400（参数错），
// 不是 403（没权限）。
func (s *Server) resolvePtyBase(r *http.Request, req proto.CreatePtySessionReq) (path, kind string, err error) {
	if req.BaseKind == "home" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", errors.New("服务端无法确定 $HOME: " + herr.Error())
		}
		return home, "home", nil
	}
	if req.BasePath == "" {
		return "", "", errors.New("缺少 base_path 参数")
	}
	root, ok := s.resolveWorkspace(r.Context(), req.BasePath)
	if !ok {
		return "", "", errors.New("base_path " + filepath.Clean(req.BasePath) +
			" 不是本机已探测到的工作树，请从工作树列表里选一个")
	}
	return root, "workspace", nil
}

// handleCreatePtySession 处理 POST /api/pty/sessions[?machine=]。
func (s *Server) handleCreatePtySession(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	var req proto.CreatePtySessionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("建终端会话：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON: " + err.Error()})
		return
	}
	s.log.Info("建终端会话请求", "base_kind", req.BaseKind, "base_path", req.BasePath,
		"size", req.Cols, "rows", req.Rows)

	base, kind, err := s.resolvePtyBase(r, req)
	if err != nil {
		s.log.Warn("建终端会话：基准目录不合法", "base_kind", req.BaseKind,
			"base_path", req.BasePath, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh" // 兜底：托管形态下 $SHELL 常常是空的
	}
	sess, err := s.pty.Open(ptyhost.OpenOptions{
		BasePath: base, BaseKind: kind, Shell: shell,
		Env: s.sessionEnv(), Cols: req.Cols, Rows: req.Rows,
	})
	if errors.Is(err, ptyhost.ErrNotSupported) {
		s.log.Warn("建终端会话：本平台不支持 PTY")
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": ptyhost.ErrNotSupported.Error()})
		return
	}
	if err != nil {
		s.log.Error("建终端会话失败", "base_kind", kind, "cwd", base, "shell", shell, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "开终端失败: " + err.Error()})
		return
	}
	s.log.Info("终端会话已建立", "session", sess.ID, "pid", sess.PID, "cwd", base, "base_kind", kind)
	writeJSON(w, http.StatusOK, ptySessionView(sess, ""))
}

// handleListPtySessions 处理 GET /api/pty/sessions[?scope=all][&machine=]。
//
// 平台不支持时返回**空列表而不是错误**：「本机没有终端会话」是一句真话，
// 让列表接口报错会把前端的会话恢复路径整个打断（spec §7）。
func (s *Server) handleListPtySessions(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	local := make([]proto.PtySession, 0)
	for _, sess := range s.pty.List() {
		local = append(local, ptySessionView(sess, ""))
	}
	if r.URL.Query().Get("scope") != "all" || isForwarded(r) {
		// 带转发头时降级为仅本机：防环优先于范围，与 projectfanout 同款
		s.log.Info("终端会话列表", "count", len(local), "scope", "local")
		writeJSON(w, http.StatusOK, proto.PtySessionsResp{Sessions: local})
		return
	}
	writeJSON(w, http.StatusOK, s.ptySessionsAll(r, local))
}

// ptySessionsAll 现场扇出所有 target，给每行盖 machine 章。
//
// 现场扇出而非读镜像：终端会话是内存态、生死以秒计，缓存出来的列表会让用户
// 恢复出一批早就没了的 tab。单台失败只影响它自己那一行（machines 里 ok=false）。
func (s *Server) ptySessionsAll(r *http.Request, local []proto.PtySession) proto.PtySessionsResp {
	out := proto.PtySessionsResp{
		Sessions: local,
		Machines: []proto.MachineStatus{{Name: "", Ok: true, FetchedAt: time.Now().UTC()}},
	}
	names := make([]string, 0, len(s.cfg.Targets))
	for name := range s.cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)

	ctx, cancel := contextWithTimeout(r, ptyFanoutBudget)
	defer cancel()

	type result struct {
		status proto.MachineStatus
		resp   *proto.PtySessionsResp
	}
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			t := s.cfg.Targets[name]
			st := proto.MachineStatus{Name: name, FetchedAt: time.Now().UTC()}
			resp, err := client.New(t.Addr, t.Token).MarkForwarded().PtySessions(ctx)
			if err != nil {
				s.log.Warn("终端会话扇出失败", "machine", name, "addr", t.Addr, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			st.Ok = true
			results[i] = result{status: st, resp: resp}
		}(i, name)
	}
	wg.Wait()

	for _, res := range results {
		out.Machines = append(out.Machines, res.status)
		if res.resp == nil {
			continue
		}
		for _, sess := range res.resp.Sessions {
			// 远端答的恒是 machine=""；由**汇总方**盖章为 target 名
			sess.Machine = res.status.Name
			out.Sessions = append(out.Sessions, sess)
		}
	}
	s.log.Info("终端会话汇总完成", "machines", len(out.Machines), "sessions", len(out.Sessions))
	return out
}

// handleDeletePtySession 处理 DELETE /api/pty/sessions/{id}[?machine=]。
func (s *Server) handleDeletePtySession(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	id := r.PathValue("id")
	err := s.pty.Close(id)
	if errors.Is(err, ptyhost.ErrNoSession) {
		s.log.Warn("关终端会话：会话不存在", "session", id)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "终端会话 " + id + " 不存在"})
		return
	}
	if err != nil {
		s.log.Error("关终端会话失败", "session", id, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("终端会话已按请求关闭", "session", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ptySessionView 把 ptyhost 的内存快照翻成线格式，machine 由调用方盖章。
func ptySessionView(s ptyhost.Session, machine string) proto.PtySession {
	return proto.PtySession{
		ID: s.ID, Machine: machine, BasePath: s.BasePath, BaseKind: s.BaseKind,
		Shell: s.Shell, CreatedAt: s.CreatedAt, Cols: s.Cols, Rows: s.Rows,
		Attached: s.Attached, PID: s.PID, ExitCode: s.ExitCode, BytesOut: s.BytesOut,
	}
}
```

`contextWithTimeout(r, d)` 若仓库里还没有同名助手，就在本文件内联为
`context.WithTimeout(r.Context(), d)`（`projectfanout.go` 用的就是这个写法）。

- [ ] **Step 4: 接线（Server 字段、构造、路由、status、client）**

`internal/agentd/server.go`：

```go
	// pty 是本机 PTY 终端会话的持有者。会话只在内存里，随 agentd 生死
	//（spec §3.1）——重启后列表为空，前端如实显示，不假装。
	pty *ptyhost.Host
```

`NewServer` 里 `s := &Server{...}` 中加 `pty: ptyhost.New(log),`。

路由（挨着 `/api/workspaces/*` 那几行）：

```go
	mux.HandleFunc("GET /api/pty/sessions", s.handleListPtySessions)
	mux.HandleFunc("POST /api/pty/sessions", s.handleCreatePtySession)
	mux.HandleFunc("DELETE /api/pty/sessions/{id}", s.handleDeletePtySession)
```

`internal/agentd/status.go` 里组装 `proto.StatusResp` 处加：

```go
	ptyOK := s.pty.Supported()
	// ...
	PtySupported: &ptyOK,
```

`internal/client/client.go` 追加（照 `ProjectTree` 的形）：

```go
// PtySessions 取对端的**单机**终端会话列表（GET /api/pty/sessions）。
//
// 供本机 agentd 的 ?scope=all 扇出使用，调用方应先 MarkForwarded()——
// 否则对端会再扇出一轮，一跳封顶的约定就破了。
func (c *Client) PtySessions(ctx context.Context) (*proto.PtySessionsResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/pty/sessions", nil)
	if err != nil {
		return nil, fmt.Errorf("请求终端会话列表: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("终端会话列表", resp)
	}
	var out proto.PtySessionsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析终端会话列表响应: %w", err)
	}
	return &out, nil
}
```

- [ ] **Step 5: 更正 `workspacefiles.go` 里已被证伪的安全说辞**

`internal/agentd/workspacefiles.go:9-15` 现在写着「浏览器控制台会话是刻意做得比主令牌弱的凭据……让一个控制台会话能读 `$HOME`，就是把弱凭据当场提权成强凭据」。这段与 spec §1 的结论直接冲突，且本 task 刚落地的 `base_kind=home` 就是在读 `$HOME`——留着它，下一个人会照一个错误的安全模型做决策。替换那段边界注释为：

```go
//   - 不接受任意路径。**这是参数校验，不是安全边界**：控制台会话在能力上
//     等价于主令牌（auth 中间件让两者落在同一个 mux 上，其中包含
//     POST /api/tasks/{id}/run 的 sh -c），白名单挡不住任何有心人。它存在的
//     理由是防止前端传一个打错的路径把 agentd 变成任意目录浏览器。
//     完整论证见 docs/superpowers/specs/2026-08-12-w4-pty-terminal-design.md §1。
```

`resolveWorkspace` 的 doc 注释里「而这条闸门是安全边界，失真窗口开在安全边界上代价太大」一句同步改为「而这条闸门是**参数校验**，失真窗口会让用户浏览到一个已经不存在的工作树，报错莫名其妙」。**不改任何行为**，只改说法与状态码语义的依据（`workspaceRootOrErr` 的 403 属既有契约，本轮不动，仅新端点用 400）。

- [ ] **Step 6: 跑测试确认通过**

```bash
go test ./internal/agentd/ ./internal/client/ -run 'Pty|Forward|Workspace|Status' -v 2>&1 | tail -30 && GOOS=windows go build ./...
```

预期：新增四个用例全 PASS，既有工作树/转发用例不受影响，windows 构建通过。

- [ ] **Step 7: 加关键节点日志（自检）**

| 节点 | 日志 |
|---|---|
| 建会话请求进入 | `Info` 带 base_kind / base_path / 尺寸 |
| 请求体解析失败、基准目录不合法 | `Warn` 带 cause |
| 平台不支持 | `Warn`（这条会变成 501，用户看得见，日志要能对上） |
| 建会话失败 | `Error` 带 base_kind / cwd / shell / cause |
| 建会话成功 | `Info` 带 session / pid / cwd（**成功路径不静默**） |
| 列表 | `Info` 带 count / scope |
| 扇出单台失败 | `Warn` 带 machine / addr / cause |
| 扇出完成 | `Info` 带 machines / sessions |
| 关会话：不存在 / 失败 / 成功 | `Warn` / `Error` / `Info`，都带 session |

红线复核：`sessionEnv()` 返回的切片**绝不能进日志**（它含 agentd 的完整环境，其中可能有令牌）；`ResolveEnvForward` 内部只记变量名与三态，已在 Task 4 保证。

- [ ] **Step 8: 加意图注释（自检）**

确认这四段「为什么」在位：`resolvePtyBase` 的「参数校验不是安全边界 → 所以 400 不是 403」；`sessionEnv` 的「默认值只在这里取，绝不回填 cfg」；`handleListPtySessions` 的「不支持时返回空列表而不是错误」；`ptySessionsAll` 的「现场扇出而非读镜像」。

- [ ] **Step 9: 提交**

```bash
git add internal/agentd/ internal/client/ && git commit -m "feat(agentd): PTY 会话 REST 接口、能力上报与跨机汇总"
```

---

### Task 7: `/ws/pty` 数据通道

**Files:**
- Create: `internal/agentd/pty_ws.go`
- Test: `internal/agentd/pty_ws_test.go`
- Modify: `internal/agentd/server.go`（一条路由）

**Interfaces:**
- Consumes: Task 3 的 `Host.Attach/Write/Get`、`Attachment.{Backlog,Since,Truncated,Out,Resize,Detach,ExitCode}`；Task 5 的 `proto.PtyControl` 与 `PtyCtrl*`
- Produces: `(*Server).handlePtyWS`

- [ ] **Step 1: 写失败测试**

`internal/agentd/pty_ws_test.go`：

```go
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/proto"
)

// dialPty 建一条 /ws/pty 连接并断言首帧是 attached。
func dialPty(t *testing.T, env *testAgentdEnv, id string, since uint64) (*websocket.Conn, proto.PtyControl) {
	t.Helper()
	url := strings.Replace(env.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=" + id + "&since=" + itoa(since)
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("拨 /ws/pty 失败: %v", err)
	}
	typ, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("读首帧: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("首帧类型 = %v，期望 text（attached 控制帧）", typ)
	}
	var ctrl proto.PtyControl
	if err := json.Unmarshal(data, &ctrl); err != nil {
		t.Fatalf("解析 attached 帧: %v；原文=%s", err, data)
	}
	if ctrl.Type != proto.PtyCtrlAttached {
		t.Fatalf("首帧 type = %q，期望 attached", ctrl.Type)
	}
	return c, ctrl
}

func itoa(n uint64) string { return strconv.FormatUint(n, 10) }

// readUntil 累积 binary 帧直到出现 want。
func readUntil(t *testing.T, c *websocket.Conn, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var sb strings.Builder
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("读帧失败: %v；累计:\n%s", err, sb.String())
		}
		if typ != websocket.MessageBinary {
			continue // 控制帧，本用例不关心
		}
		sb.Write(data)
		if strings.Contains(sb.String(), want) {
			return sb.String()
		}
	}
}

// 打字 → 回显：binary 帧双向跑通。
func TestPtyWSEchoRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	_, body := ptyPost(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	var s proto.PtySession
	_ = json.Unmarshal(body, &s)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	c, ctrl := dialPty(t, env, s.ID, 0)
	defer c.Close(websocket.StatusNormalClosure, "")
	if ctrl.Truncated {
		t.Error("新会话不该报 truncated")
	}
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("echo WS_OK\n")); err != nil {
		t.Fatalf("写按键: %v", err)
	}
	readUntil(t, c, "WS_OK")
}

// text 控制帧 resize 生效。
func TestPtyWSResize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	_, body := ptyPost(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	var s proto.PtySession
	_ = json.Unmarshal(body, &s)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	c, _ := dialPty(t, env, s.ID, 0)
	defer c.Close(websocket.StatusNormalClosure, "")
	msg, _ := json.Marshal(proto.PtyControl{Type: proto.PtyCtrlResize, Cols: 132, Rows: 43})
	if err := c.Write(context.Background(), websocket.MessageText, msg); err != nil {
		t.Fatalf("写 resize: %v", err)
	}
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("stty size\n"))
	readUntil(t, c, "43 132")
}

// 断开重连带 since：只补没看过的那段。
func TestPtyWSResumeSince(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	_, body := ptyPost(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	var s proto.PtySession
	_ = json.Unmarshal(body, &s)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	c1, _ := dialPty(t, env, s.ID, 0)
	_ = c1.Write(context.Background(), websocket.MessageBinary, []byte("echo ROUND1\n"))
	readUntil(t, c1, "ROUND1")
	cur, _ := env.srv.pty.Get(s.ID)
	_ = c1.Close(websocket.StatusNormalClosure, "")

	_ = env.srv.pty.Write(s.ID, []byte("echo ROUND2\n"))
	time.Sleep(500 * time.Millisecond)

	c2, ctrl := dialPty(t, env, s.ID, cur.BytesOut)
	defer c2.Close(websocket.StatusNormalClosure, "")
	if ctrl.Truncated {
		t.Error("这点输出装得下，不该 truncated")
	}
	got := readUntil(t, c2, "ROUND2")
	if strings.Contains(got, "ROUND1") {
		t.Errorf("since 之前的内容不该重放，实得:\n%s", got)
	}
}

// 会话已退出时建连：先灌历史、再发 exit 帧、再正常关闭——不是错误路径。
func TestPtyWSAttachToExitedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	_, body := ptyPost(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	var s proto.PtySession
	_ = json.Unmarshal(body, &s)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	_ = env.srv.pty.Write(s.ID, []byte("echo BYE; exit 5\n"))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if g, ok := env.srv.pty.Get(s.ID); ok && g.ExitCode != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	c, _ := dialPty(t, env, s.ID, 0)
	defer c.Close(websocket.StatusNormalClosure, "")
	sawBye, exitCode := false, -1
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			break // 服务端已 close(1000)
		}
		if typ == websocket.MessageBinary && bytes.Contains(data, []byte("BYE")) {
			sawBye = true
			continue
		}
		if typ == websocket.MessageText {
			var ctrl proto.PtyControl
			_ = json.Unmarshal(data, &ctrl)
			if ctrl.Type == proto.PtyCtrlExit && ctrl.ExitCode != nil {
				exitCode = *ctrl.ExitCode
			}
		}
	}
	if !sawBye {
		t.Error("已退出会话的历史输出必须先灌给用户看最后一眼")
	}
	if exitCode != 5 {
		t.Errorf("exit 控制帧的 exit_code = %d，期望 5", exitCode)
	}
}

// 会话不存在：1008 policy violation（「你这个请求不合法，别重连」）。
func TestPtyWSUnknownSession(t *testing.T) {
	env := newTestAgentdEnv(t)
	url := strings.Replace(env.ts.URL, "http://", "ws://", 1) + "/ws/pty?session=nope&since=0"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("拨 /ws/pty: %v", err)
	}
	defer c.Close(websocket.StatusInternalError, "")
	_, _, rerr := c.Read(context.Background())
	if websocket.CloseStatus(rerr) != websocket.StatusPolicyViolation {
		t.Fatalf("关闭码 = %v，期望 1008（不该让前端一直重连）", websocket.CloseStatus(rerr))
	}
}
```

测试文件 import 里补 `"strconv"`。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestPtyWS -v
```

预期：全部拨号失败（路由未注册，返回 404）。

- [ ] **Step 3: 写实现**

`internal/agentd/pty_ws.go`：

```go
// 本文件实现 /ws/pty：浏览器与 PTY 会话之间的双向字节通道。
//
// 职责：
//   - 建连：按 session/since 订阅 ptyhost，首帧回 attached（含 since 与 truncated）
//   - 下行：binary 帧搬 PTY 原始字节，text 帧搬 exit / error 控制信息
//   - 上行：binary 帧是用户按键，text 帧是 resize
//
// 边界：
//   - 不持有会话状态，全部转交 s.pty
//   - **断开只 detach，不杀会话**（spec §3.2）：关页面、切设备、网络抖动
//     都走这条路，杀会话只有 DELETE 一条
//   - machine != "" 的远程会话不落在本文件，走 forward_ws.go 的反代
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/ptyhost"
)

// handlePtyWS 处理 GET /ws/pty?session=<id>&since=<n>[&machine=]。
func (s *Server) handlePtyWS(w http.ResponseWriter, r *http.Request) {
	if machine := r.URL.Query().Get("machine"); machine != "" && !isForwarded(r) {
		s.forwardWS(w, r, machine)
		return
	}
	id := r.URL.Query().Get("session")
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("终端 WS 升级失败", "session", id, "cause", err)
		return
	}
	att, aerr := s.pty.Attach(id, since)
	if aerr != nil {
		// **先升级再 close(1008)，不要在升级前回 404。** 会话不存在与连接数超限
		// 都是「你这个请求不合法，别重连」，1008 policy violation 正是这个语义，
		// 与 /ws/events 对未知 task-id 的处理同款，前端 ws.ts 已有对应的终止分支。
		// 若在升级前回 HTTP 状态码，coder/websocket 的 Dial 只会返回一个泛化的
		// 握手错误，前端分不清「服务端不接」与「网络断了」，会一直重连下去。
		s.log.Warn("终端 WS 建连被拒", "session", id, "since", since, "cause", aerr)
		_ = conn.Close(websocket.StatusPolicyViolation, aerr.Error())
		return
	}
	defer att.Detach()
	ctx := r.Context()

	// 首帧必须是 attached：truncated 决定前端要不要先清屏，不给它前端就会
	// 把同一段输出重复画一遍（spec §5.3）。
	if err := writeCtrl(ctx, conn, proto.PtyControl{
		Type: proto.PtyCtrlAttached, Since: att.Since, Truncated: att.Truncated,
	}); err != nil {
		s.log.Warn("终端 WS 首帧写失败", "session", id, "cause", err)
		_ = conn.Close(websocket.StatusInternalError, "首帧写失败")
		return
	}
	if len(att.Backlog) > 0 {
		if err := conn.Write(ctx, websocket.MessageBinary, att.Backlog); err != nil {
			s.log.Warn("终端 WS 回放写失败", "session", id,
				"backlog_bytes", len(att.Backlog), "cause", err)
			_ = conn.Close(websocket.StatusInternalError, "回放写失败")
			return
		}
	}
	s.log.Info("终端 WS 已建连", "session", id, "since", att.Since,
		"backlog_bytes", len(att.Backlog), "truncated", att.Truncated)

	// 上行独立一条 goroutine：读用户按键与 resize。它出错即整体收工。
	upDone := make(chan struct{})
	go func() {
		defer close(upDone)
		s.pumpPtyUplink(ctx, conn, att, id)
	}()

	// 下行在本 goroutine：att.Out 关闭 = 会话结束（不是网络抖动），
	// 此时补一帧 exit 让前端停止重连，再正常 close(1000)。
	for {
		select {
		case <-ctx.Done():
			s.log.Info("终端 WS 断开（客户端离开）", "session", id)
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		case <-upDone:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		case b, ok := <-att.Out:
			if !ok {
				code := att.ExitCode()
				s.log.Info("终端 WS 收尾：会话已退出", "session", id, "exit_code", code)
				_ = writeCtrl(ctx, conn, proto.PtyControl{Type: proto.PtyCtrlExit, ExitCode: code})
				_ = conn.Close(websocket.StatusNormalClosure, "会话已退出")
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, b); err != nil {
				s.log.Warn("终端 WS 下行写失败", "session", id, "bytes", len(b), "cause", err)
				return
			}
		}
	}
}

// pumpPtyUplink 读客户端上行：binary=按键，text=控制帧。
func (s *Server) pumpPtyUplink(ctx context.Context, conn *websocket.Conn, att *ptyhost.Attachment, id string) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			s.log.Debug("终端 WS 上行结束", "session", id, "cause", err)
			return
		}
		if typ == websocket.MessageBinary {
			if err := s.pty.Write(id, data); err != nil {
				// 会话已退出时用户还在打字：告诉他，不要静默吞掉按键
				s.log.Warn("终端 WS 上行写入失败", "session", id, "bytes", len(data), "cause", err)
				_ = writeCtrl(ctx, conn, proto.PtyControl{Type: proto.PtyCtrlError, Message: err.Error()})
				return
			}
			continue
		}
		var ctrl proto.PtyControl
		if err := json.Unmarshal(data, &ctrl); err != nil {
			s.log.Warn("终端 WS 控制帧无法解析", "session", id, "cause", err)
			continue
		}
		if ctrl.Type != proto.PtyCtrlResize {
			s.log.Warn("终端 WS 收到未知控制帧类型", "session", id, "type", ctrl.Type)
			continue
		}
		if err := att.Resize(ctrl.Cols, ctrl.Rows); err != nil {
			s.log.Warn("终端 WS resize 失败", "session", id,
				"cols", ctrl.Cols, "rows", ctrl.Rows, "cause", err)
		}
	}
}

// writeCtrl 发一帧 JSON 控制信息（text 帧）。
func writeCtrl(ctx context.Context, conn *websocket.Conn, c proto.PtyControl) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, b)
}
```

`internal/agentd/server.go` 路由（挨着 `GET /ws/events`）：

```go
	mux.HandleFunc("GET /ws/pty", s.handlePtyWS)
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run TestPtyWS -race -v && GOOS=windows go build ./...
```

预期：5 个用例全 PASS，无数据竞争。

- [ ] **Step 5: 加关键节点日志（自检）**

| 节点 | 日志 |
|---|---|
| 建连被拒（会话不存在 / 超限） | `Warn` 带 session / since / cause |
| 升级失败 | `Warn` 带 session / cause |
| 建连成功 | `Info` 带 session / since / backlog_bytes / truncated（**成功路径不静默**） |
| 首帧 / 回放 / 下行写失败 | `Warn` 各带 session 与字节数 |
| 会话退出收尾 | `Info` 带 session / exit_code |
| 客户端离开 | `Info` 带 session |
| 上行写入失败、控制帧解析失败、未知控制帧、resize 失败 | `Warn` 各带 session |
| 上行结束 | `Debug`（每次断开都会走，降级避免刷屏） |

红线复核：**按键内容与 PTY 输出字节一律不进日志**（用户会在终端里敲密码），只记字节数。

- [ ] **Step 6: 加意图注释（自检）**

确认：文件头写清「断开只 detach 不杀会话」；首帧 attached 为何必须带 `truncated`；`att.Out` 关闭为何等于「会话结束而非网络抖动」；1008 的选择理由。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/ && git commit -m "feat(agentd): /ws/pty 双向字节通道，binary 走数据、text 走控制"
```

---

### Task 8: WS 反代（跨机终端）

**Files:**
- Create: `internal/agentd/forward_ws.go`
- Test: `internal/agentd/forward_ws_test.go`

**Interfaces:**
- Consumes: 既有 `forwardURL`、`normalizeAddr`、`forwardedHeader`、`isForwarded`、`s.cfg.Targets`；Task 7 的 `handlePtyWS`（它调用本 task 的入口）
- Produces: `(*Server).forwardWS(w http.ResponseWriter, r *http.Request, machine string)`

- [ ] **Step 1: 写失败测试（两个 httptest agentd 串起来）**

`internal/agentd/forward_ws_test.go`：

```go
package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
	"log/slog"
	"io"
)

// 端到端：浏览器 → 本机 agentd → 远端 agentd → 远端 ptyhost，字节双向透传。
func TestForwardWSPtyEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	t.Setenv("HOME", t.TempDir())
	_, body := ptyPost(t, remote, `{"base_kind":"home","cols":80,"rows":24}`)
	var s proto.PtySession
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("解析建会话响应: %v；体=%s", err, body)
	}
	t.Cleanup(func() { _ = remote.srv.pty.Close(s.ID) })

	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=" + s.ID + "&since=0&machine=devbox"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("经本机拨远端 /ws/pty: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// 首帧必须是远端原样发出的 attached 控制帧——反代不解析、不改写
	typ, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("读首帧: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("首帧类型 = %v，期望 text", typ)
	}
	var ctrl proto.PtyControl
	if err := json.Unmarshal(data, &ctrl); err != nil {
		t.Fatalf("解析 attached: %v；原文=%s", err, data)
	}
	if ctrl.Type != proto.PtyCtrlAttached {
		t.Fatalf("首帧 type = %q，期望 attached", ctrl.Type)
	}

	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("echo PROXY_OK\n")); err != nil {
		t.Fatalf("上行写: %v", err)
	}
	readUntil(t, c, "PROXY_OK")
}

// 远端够不着：**在升级之前**回 502 带原文，而不是升级成功后再关。
//
// 这条是分诊的关键：升级成功再关，前端只会看到「连上了又断了」，会一直重连；
// 502 才让它知道是本机与目标机之间的问题。
func TestForwardWSUnreachableRemoteYields502(t *testing.T) {
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=any&since=0&machine=devbox"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err == nil {
		t.Fatal("远端不可达时握手必须失败")
	}
	if resp == nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("状态码 = %v，期望 502", resp)
	}
}

// 机器名不在 targets 里：400，且点名它。与 REST 的 forwardIfRequested 一致。
func TestForwardWSUnknownMachine(t *testing.T) {
	local := newTestAgentdEnv(t)
	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=any&since=0&machine=ghost"
	_, resp, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err == nil {
		t.Fatal("未知机器名必须被拒")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %v，期望 400", resp)
	}
}

// 远端主动关闭时，关闭码与原因要传回浏览器——否则前端分不清「会话被拒」
// 与「网络断了」，1008 的终止分支就永远走不到。
func TestForwardWSPropagatesCloseStatus(t *testing.T) {
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 远端上没有这个会话 → 远端 close(1008)，反代必须原样传回
	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=missing&since=0&machine=devbox"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("拨号: %v", err)
	}
	defer c.Close(websocket.StatusInternalError, "")
	_, _, rerr := c.Read(context.Background())
	if websocket.CloseStatus(rerr) != websocket.StatusPolicyViolation {
		t.Fatalf("关闭码 = %v，期望 1008 原样传回", websocket.CloseStatus(rerr))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestForwardWS -v
```

预期：`undefined: s.forwardWS`（Task 7 已经调用了它）。

- [ ] **Step 3: 写实现**

`internal/agentd/forward_ws.go`：

```go
// 本文件实现 WebSocket 的跨机反代：`forwardTo` 的 WS 孪生。
//
// 职责：
//   - 按 ?machine= 找到 target，拨它的同路径 WS 端点（带 Bearer 与防环头）
//   - 两条连接双向对拷，**保持帧类型**（binary 仍是 binary）
//   - 关闭码与原因双向传播
//
// 边界：
//   - **不解析帧内容**。它不知道 PTY、不知道 JSON，只搬帧
//   - 一跳封顶：出站带 X-Handoff-Forwarded，对端 handlePtyWS 因此不再反代
//   - 不重试。与 forwardTo 一致：一次失败就 502 带原文，让调用方决定
//
// 为什么不让浏览器直连远端 agentd：cookie 是 host-only 的，远端那台没有本机
// 这份会话，等于要另做一套跨机 ticket 分发与跨域处理。「本机 agentd 是唯一
// 入口」本来就是既有模型（/api/workspaces/* 的 ?machine= 转发即此）。
package agentd

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// wsDialBudget 是拨远端的握手预算。握手不该慢：慢就是不可达，早点回 502
// 比让用户对着一个转圈的终端等强。建连之后的数据流**不受它约束**。
const wsDialBudget = 10 * time.Second

// forwardWS 把一条 WS 请求反代到 machine 指定的机器。
//
// 关键顺序：**先拨上游，成功了再 Accept 本地**。反过来的话，上游不可达时
// 本地已经升级成 101，只能发一个 close——前端看到的是「连上了又断了」，
// 会一直重连；而 502 才让它知道是本机与目标机之间的问题（与 forwardTo 一致）。
func (s *Server) forwardWS(w http.ResponseWriter, r *http.Request, machine string) {
	t, ok := s.cfg.Targets[machine]
	if !ok {
		s.log.Warn("WS 转发被拒：机器名未在配置中定义", "machine", machine, "path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "机器 " + machine + " 未在本机配置的 targets 中定义"})
		return
	}
	target, err := forwardURL(t.Addr, r.URL) // 复用 REST 那份：同时摘掉 machine 参数
	if err != nil {
		s.log.Error("WS 转发失败：目标地址不合法", "machine", machine, "addr", t.Addr, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	target = toWSScheme(target)

	dialCtx, cancelDial := context.WithTimeout(r.Context(), wsDialBudget)
	defer cancelDial()
	hdr := http.Header{forwardedHeader: {"1"}}
	if t.Token != "" {
		hdr.Set("Authorization", "Bearer "+t.Token)
	}
	start := time.Now()
	up, _, err := websocket.Dial(dialCtx, target, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		s.log.Error("WS 转发失败：上游不可达", "machine", machine, "path", r.URL.Path,
			"elapsed_ms", time.Since(start).Milliseconds(), "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "转发到 " + machine + " 失败: " + err.Error()})
		return
	}
	defer up.Close(websocket.StatusNormalClosure, "")

	down, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("WS 转发：本地升级失败", "machine", machine, "cause", err)
		return
	}
	defer down.Close(websocket.StatusNormalClosure, "")

	// 上游握手成功后就把读限制放开：PTY 的回放帧可能有几百 KB，
	// coder/websocket 默认 32KiB 的读上限会把它判成协议错误。
	up.SetReadLimit(wsForwardReadLimit)
	down.SetReadLimit(wsForwardReadLimit)

	s.log.Info("WS 转发已建立", "machine", machine, "path", r.URL.Path,
		"elapsed_ms", time.Since(start).Milliseconds())

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	errCh := make(chan error, 2)
	go func() { errCh <- pipeWS(ctx, down, up) }() // 浏览器 → 远端
	go func() { errCh <- pipeWS(ctx, up, down) }() // 远端 → 浏览器

	first := <-errCh
	cancel()
	// 任一方向结束就收工，并把对端的关闭码原样传给另一端：1008「别重连」
	// 这种语义必须穿过反代，否则前端会永远重连一个已经明确拒绝它的会话。
	status, reason := websocket.CloseStatus(first), ""
	var ce websocket.CloseError
	if errors.As(first, &ce) {
		reason = ce.Reason
	}
	if status == -1 {
		status = websocket.StatusNormalClosure
	}
	_ = down.Close(status, reason)
	_ = up.Close(status, reason)
	s.log.Info("WS 转发结束", "machine", machine, "path", r.URL.Path,
		"close_status", int(status), "elapsed_ms", time.Since(start).Milliseconds())
}

// wsForwardReadLimit 是反代两侧的单帧上限。512 KiB 覆盖 256 KiB 回放缓冲
// 加上任何合理的膨胀，同时仍是一道防线（不至于让一个坏掉的对端把内存吃光）。
const wsForwardReadLimit = 512 << 10

// pipeWS 把 src 的每一帧原样写进 dst，**保持帧类型**。
//
// 帧类型必须保持：数据走 binary、控制走 text 是 /ws/pty 的契约（spec §5.3），
// 反代若把一切都当 text 转发，二进制里任何非 UTF-8 字节都会被判为协议错误。
func pipeWS(ctx context.Context, src, dst *websocket.Conn) error {
	for {
		typ, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

// toWSScheme 把 http/https 换成 ws/wss。forwardURL 产出的恒是 http(s)://。
func toWSScheme(u string) string {
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run 'TestForwardWS|TestPtyWS|TestForward' -race -v 2>&1 | tail -30
```

预期：4 个新用例 + Task 7 的 5 个 + 既有 3 个转发用例全 PASS。

- [ ] **Step 5: 加关键节点日志（自检）**

| 节点 | 日志 |
|---|---|
| 机器名未定义 | `Warn` 带 machine / path |
| 目标地址不合法 | `Error` 带 machine / addr / cause |
| 上游不可达 | `Error` 带 machine / path / elapsed_ms / cause（最常见的故障，必须能一眼看出耗时） |
| 本地升级失败 | `Warn` 带 machine / cause |
| 转发建立 | `Info` 带 machine / path / elapsed_ms（**成功路径不静默**） |
| 转发结束 | `Info` 带 machine / path / close_status / elapsed_ms |

红线复核：`t.Token` **绝不能进日志**，它只出现在 `hdr.Set("Authorization", ...)` 那一行——这与 `forwardTo` 的既有纪律一致（「token 只进请求头」）。`pipeWS` 里**一个字节都不记**。

- [ ] **Step 6: 加意图注释（自检）**

确认四段「为什么」在位：先拨上游再 Accept 的顺序理由；为什么不让浏览器直连远端；`pipeWS` 为何必须保持帧类型；关闭码为何要穿过反代。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/forward_ws.go internal/agentd/forward_ws_test.go && git commit -m "feat(agentd): WebSocket 跨机反代，forwardTo 的 WS 孪生"
```

---

### Task 9: 前端 PTY 客户端（REST + WS）

**Files:**
- Modify: `web/src/api/client.ts`（三个 REST 函数）
- Create: `web/src/api/pty.ts`
- Test: `web/src/api/pty.test.ts`

**Interfaces:**
- Consumes: Task 5 的 TS 类型；`client.ts` 私有的 `request` / `postJSON` / `machineQuery`
- Produces:
  - `fetchPtySessions(scope?: 'all'): Promise<PtySessionsResp>`
  - `createPtySession(req: CreatePtySessionReq, machine?: string): Promise<PtySession>`
  - `deletePtySession(id: string, machine?: string): Promise<{ ok: boolean }>`
  - `interface PtySocketLike`、`interface PtyOptions`、`interface PtyHandle { close(): void; send(bytes: Uint8Array): void; resize(cols: number, rows: number): void }`
  - `function connectPty(options: PtyOptions): PtyHandle`

- [ ] **Step 1: 加 REST 函数**

`web/src/api/client.ts` 末尾追加（沿用同文件里 `machineQuery` 的写法）：

```typescript
// fetchPtySessions 列终端会话（GET /api/pty/sessions）。
//
// scope='all' 取跨机汇总（多一个 machines 字段）。**这是会话恢复的唯一真相源**：
// 前端不做任何本地持久化，列表里没有的会话就是不存在（spec §6.1）。
export function fetchPtySessions(scope?: 'all'): Promise<PtySessionsResp> {
  return request<PtySessionsResp>(`/api/pty/sessions${scope === 'all' ? '?scope=all' : ''}`)
}

// createPtySession 开一个终端会话（POST /api/pty/sessions）。
//
// base_kind='home' 时 base_path 被服务端忽略（它用自己的 $HOME）。
// 501 = 那台机器的平台不支持 PTY；400 = base_path 不是已探测到的工作树。
export function createPtySession(req: CreatePtySessionReq, machine?: string): Promise<PtySession> {
  return postJSON<PtySession>(`/api/pty/sessions${machineQuery(machine)}`, req)
}

// deletePtySession 显式关闭一个终端会话（DELETE /api/pty/sessions/{id}）。
//
// **只有用户点 × 才该调它。** 组件卸载、切基准目录、关页面都只断 WS，
// 不调这里——否则「跑一晚上的 build」会被一次切目录杀掉（spec §3.2）。
export function deletePtySession(id: string, machine?: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>(
    `/api/pty/sessions/${encodeURIComponent(id)}${machineQuery(machine)}`,
    { method: 'DELETE' },
  )
}
```

并把 `CreatePtySessionReq` / `PtySession` / `PtySessionsResp` 加进文件顶部的 type import。

- [ ] **Step 2: 写失败测试**

`web/src/api/pty.test.ts`：

```typescript
import { describe, expect, it, vi } from 'vitest'
import { connectPty, type PtySocketLike } from './pty'

// FakePtySocket 是可编程替身：测试手动驱动 open/message/close。
class FakePtySocket implements PtySocketLike {
  url: string
  binaryType = 'blob'
  onopen: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  sent: Array<string | ArrayBufferLike> = []
  closed = false
  constructor(url: string) {
    this.url = url
  }
  send(d: string | ArrayBufferLike) {
    this.sent.push(d)
  }
  close() {
    this.closed = true
  }
  emitText(obj: unknown) {
    this.onmessage?.({ data: JSON.stringify(obj) } as MessageEvent)
  }
  emitBinary(s: string) {
    this.onmessage?.({ data: new TextEncoder().encode(s).buffer } as MessageEvent)
  }
  emitClose(code: number) {
    this.onclose?.({ code } as CloseEvent)
  }
}

function harness(overrides: Partial<Parameters<typeof connectPty>[0]> = {}) {
  const sockets: FakePtySocket[] = []
  const onData = vi.fn()
  const onAttached = vi.fn()
  const onExit = vi.fn()
  const handle = connectPty({
    sessionId: 's1',
    onData,
    onAttached,
    onExit,
    create: (url) => {
      const s = new FakePtySocket(url)
      sockets.push(s)
      return s
    },
    ...overrides,
  })
  return { sockets, onData, onAttached, onExit, handle }
}

describe('connectPty', () => {
  it('binaryType 必须是 arraybuffer：默认 blob 会让 onData 拿到一个 Promise 而不是字节', () => {
    const { sockets } = harness()
    expect(sockets[0].binaryType).toBe('arraybuffer')
  })

  it('首帧 attached 转成回调；二进制帧转成字节', () => {
    const { sockets, onAttached, onData } = harness()
    sockets[0].emitText({ type: 'attached', since: 0, truncated: false })
    expect(onAttached).toHaveBeenCalledWith({ since: 0, truncated: false })
    sockets[0].emitBinary('hi')
    expect(new TextDecoder().decode(onData.mock.calls[0][0])).toBe('hi')
  })

  it('重连时按已收字节数续传，不重复请求已看过的输出', () => {
    vi.useFakeTimers()
    const { sockets } = harness()
    sockets[0].emitText({ type: 'attached', since: 0, truncated: false })
    sockets[0].emitBinary('12345') // 5 字节
    sockets[0].emitClose(1006)
    vi.advanceTimersByTime(20000)
    expect(sockets.length).toBeGreaterThan(1)
    expect(sockets[1].url).toContain('since=5')
    vi.useRealTimers()
  })

  it('收到 exit 后停止重连——会话没了，重连一百次也没用', () => {
    vi.useFakeTimers()
    const { sockets, onExit } = harness()
    sockets[0].emitText({ type: 'exit', exit_code: 7 })
    expect(onExit).toHaveBeenCalledWith(7)
    sockets[0].emitClose(1000)
    vi.advanceTimersByTime(60000)
    expect(sockets).toHaveLength(1)
    vi.useRealTimers()
  })

  it('close code 1008 是终止而不是抖动，不重连', () => {
    vi.useFakeTimers()
    const onTerminal = vi.fn()
    const { sockets } = harness({ onTerminal })
    sockets[0].emitClose(1008)
    vi.advanceTimersByTime(60000)
    expect(sockets).toHaveLength(1)
    expect(onTerminal).toHaveBeenCalled()
    vi.useRealTimers()
  })

  it('send 走二进制帧，resize 走 JSON 文本帧', () => {
    const { sockets, handle } = harness()
    handle.send(new TextEncoder().encode('ls\n'))
    handle.resize(120, 40)
    expect(sockets[0].sent[0]).toBeInstanceOf(ArrayBuffer)
    expect(JSON.parse(String(sockets[0].sent[1]))).toEqual({ type: 'resize', cols: 120, rows: 40 })
  })

  it('machine 非空时进查询串——远程终端由本机 agentd 反代', () => {
    const { sockets } = harness({ machine: 'devbox' })
    expect(sockets[0].url).toContain('machine=devbox')
  })
})
```

- [ ] **Step 3: 跑测试确认失败**

```bash
cd web && npx vitest run src/api/pty.test.ts
```

预期：`Failed to resolve import './pty'`。

- [ ] **Step 4: 写实现**

`web/src/api/pty.ts`：

```typescript
// agentd 的 /ws/pty 客户端（浏览器侧）——ws.ts 的孪生。
//
// 职责：
//   - 打开同源 /ws/pty?session=&since=[&machine=]，收发 PTY 字节
//   - binary 帧 = 数据（双向），text 帧 = JSON 控制（attached / exit / error / resize）
//   - 指数退避自动重连，**按已收字节数续传**
//
// 与 ws.ts 的两点关键差异（不要照抄 ws.ts 了事）：
//   1. 游标是**字节数**不是事件 seq：PTY 输出没有天然的条目边界，服务端的
//      环形缓冲按绝对字节序号索引，客户端也就只能数字节（抄 handoff attach）
//   2. 必须设 binaryType='arraybuffer'：默认的 'blob' 会让 onmessage 拿到一个
//      需要 await 的 Blob，终端渲染顺序会因此乱掉
//
// 终止语义（两种，都不重连）：
//   - close code 1008：会话不存在 / 连接数超限 / 会话被吊销
//   - 收到 exit 控制帧：shell 自己退出了，会话已成终态
// 其余（1006 网络断、1000 正常关）一律退避重连——这正是「关掉页面走人，
// 回来接着看」能成立的地方。
//
// 边界：不认识 xterm、不解析转义序列，只搬字节。
import type { PtyControl } from './types'
import { wsCloseReason, type WsStatus, type WsTermination } from './ws'

// PtySocketLike 是本层用到的 WebSocket 最小表面（真 WebSocket 与测试替身都满足）。
export interface PtySocketLike {
  url: string
  binaryType: string
  onopen: ((ev: Event) => void) | null
  onmessage: ((ev: MessageEvent) => void) | null
  onerror: ((ev: Event) => void) | null
  onclose: ((ev: CloseEvent) => void) | null
  send: (data: string | ArrayBufferLike) => void
  close: () => void
}

export interface PtyOptions {
  sessionId: string
  machine?: string
  // since: 起始字节游标。恢复已有会话时传 0 即可——服务端会把整个环形缓冲
  // 回放给你并在 attached 帧里标 truncated。
  since?: number
  onData: (bytes: Uint8Array) => void
  // onAttached 在**每次**建连时触发（含重连）。truncated=true 表示中间丢了一段，
  // 调用方必须先清屏再灌，否则同一段输出会被重复画。
  onAttached: (info: { since: number; truncated: boolean }) => void
  // onExit：shell 已退出。exitCode 可能缺席（对端没给），此时不要显示成 0。
  onExit: (exitCode?: number) => void
  onStatus?: (status: WsStatus) => void
  onError?: (message: string, closeCode: number) => void
  onTerminal?: (termination: WsTermination) => void
  create?: (url: string) => PtySocketLike
}

export interface PtyHandle {
  close: () => void
  send: (bytes: Uint8Array) => void
  resize: (cols: number, rows: number) => void
}

function ptyUrl(sessionId: string, since: number, machine?: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const q = new URLSearchParams({ session: sessionId, since: String(since) })
  if (machine) q.set('machine', machine)
  return `${proto}//${window.location.host}/ws/pty?${q.toString()}`
}

// connectPty 打开一条 PTY 数据通道，返回可写可关的句柄。
//
// 注意：close() 只断连接，**不删会话**——服务端会话继续跑。要真的杀掉它，
// 调 deletePtySession（spec §3.2、§6.2）。
export function connectPty(options: PtyOptions): PtyHandle {
  // cursor 是「已收到的字节数」：重连时作为 since 续传。
  let cursor = options.since ?? 0
  let ws: PtySocketLike | null = null
  let closedByUs = false
  let terminal = false
  let retryDelay = 300
  let retryTimer: number | undefined

  function cleanup() {
    if (!ws) return
    ws.onopen = null
    ws.onmessage = null
    ws.onerror = null
    ws.onclose = null
    ws.close()
    ws = null
  }

  function scheduleReconnect() {
    if (closedByUs || terminal) return
    options.onStatus?.('connecting')
    retryTimer = window.setTimeout(open, retryDelay)
    retryDelay = Math.min(retryDelay * 2 + Math.floor(Math.random() * 200), 10000)
  }

  function handleControl(raw: string) {
    let ctrl: PtyControl
    try {
      ctrl = JSON.parse(raw) as PtyControl
    } catch (err) {
      options.onError?.(`收到无法解析的控制帧：${err instanceof Error ? err.message : String(err)}`, 0)
      return
    }
    switch (ctrl.type) {
      case 'attached':
        // 服务端说它从哪个字节开始给：以**它**的口径为准推进游标。
        // 用本地的猜测会在 truncated 时把游标停在一个环里已经没有的位置。
        cursor = ctrl.since
        options.onAttached({ since: ctrl.since, truncated: ctrl.truncated })
        return
      case 'exit':
        terminal = true
        options.onExit(ctrl.exit_code)
        return
      case 'error':
        options.onError?.(ctrl.message ?? '服务端报告了一个未说明的终端错误', 0)
        return
      default:
        // 不认识的控制帧一律忽略：前端比后端晚部署是常态，新增一种控制帧
        // 不该让旧前端崩掉（与 KNOWN_FRAME_TYPES 同一条纪律）
        return
    }
  }

  function open() {
    if (closedByUs || terminal) return
    ws = (options.create ?? ((url: string) => new WebSocket(url) as unknown as PtySocketLike))(
      ptyUrl(options.sessionId, cursor, options.machine),
    )
    // 必须在任何消息到达之前设：blob 模式下 onmessage 拿到的是需要 await 的对象
    ws.binaryType = 'arraybuffer'
    ws.onopen = () => {
      retryDelay = 300
      options.onStatus?.('open')
    }
    ws.onmessage = (msg) => {
      if (typeof msg.data === 'string') {
        handleControl(msg.data)
        return
      }
      const bytes = new Uint8Array(msg.data as ArrayBuffer)
      cursor += bytes.byteLength
      options.onData(bytes)
    }
    ws.onerror = () => {
      // 与 onclose 成对出现，统一在 onclose 收口
    }
    ws.onclose = (ev) => {
      if (closedByUs) return
      options.onStatus?.('closed')
      if (terminal) {
        // 已经收到 exit：这次关闭是它的正常收尾，不是故障，也不重连
        cleanup()
        return
      }
      const reason = wsCloseReason(ev)
      if (ev.code === 1008) {
        terminal = true
        cleanup()
        options.onTerminal?.({ message: reason.message, closeCode: ev.code })
        return
      }
      options.onError?.(reason.message, ev.code)
      cleanup()
      scheduleReconnect()
    }
  }

  open()

  return {
    close() {
      closedByUs = true
      if (retryTimer !== undefined) window.clearTimeout(retryTimer)
      cleanup()
    },
    send(bytes) {
      // 切出一个独立的 ArrayBuffer：Uint8Array 可能是某个更大 buffer 的视图，
      // 直接送 .buffer 会把整块内存发出去。
      ws?.send(bytes.slice().buffer)
    },
    resize(cols, rows) {
      ws?.send(JSON.stringify({ type: 'resize', cols, rows }))
    },
  }
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
cd web && npx vitest run src/api/pty.test.ts && npm run typecheck
```

预期：7 个用例全 PASS，typecheck 无错。

- [ ] **Step 6: 加关键节点日志（自检）**

浏览器侧没有 `slog`，本层的等价物是**回调**：`onStatus` / `onError` / `onTerminal` / `onExit` 就是它的可观测面，调用方（Task 11）负责把它们渲染出来。核对：

- [ ] 每条断开都经 `onStatus('closed')` + `onError`（或 `onTerminal`），**没有静默的 return**
- [ ] 控制帧解析失败经 `onError` 上报而不是 `catch {}` 吞掉
- [ ] 不认识的控制帧类型走 default 忽略——这是**有意**的静默，注释已写明理由
- [ ] `console.log` 一个都没有

- [ ] **Step 7: 加意图注释（自检）**

确认：文件头的「与 ws.ts 的两点关键差异」；`binaryType='arraybuffer'` 为何必须在消息到达前设；`attached` 为何以服务端口径推进游标；`send` 里 `bytes.slice()` 的理由；`close()` 只断连接不删会话。

- [ ] **Step 8: 提交**

```bash
git add web/src/api/ && git commit -m "feat(web): PTY REST 客户端与 /ws/pty 字节通道"
```

---

### Task 10: `tabs.ts` 承载会话身份

**Files:**
- Modify: `web/src/app/workbench/tabs.ts:23-27`（`TabContent`）、`:55-64`（`dedupKey`）
- Test: `web/src/app/workbench/tabs.test.ts`（追加）

**Interfaces:**
- Consumes: 无
- Produces: `TabContent` 的终端支变为 `{ kind: 'terminal'; seq: number; sessionId?: string }`；`dedupKey` 对带 `sessionId` 的终端返回 `pty:${sessionId}`

- [ ] **Step 1: 写失败测试**

`web/src/app/workbench/tabs.test.ts` 追加：

```typescript
describe('终端 tab 的会话身份', () => {
  it('还没建出会话的终端仍然永不去重——再点一次就是真的想要第二个', () => {
    expect(dedupKey({ kind: 'terminal', seq: 1 })).toBeNull()
    expect(dedupKey({ kind: 'terminal', seq: 2 })).toBeNull()
  })

  it('已有会话 id 的终端按会话去重：刷新恢复不该长出两个同一会话的 tab', () => {
    expect(dedupKey({ kind: 'terminal', seq: 1, sessionId: 'abc' })).toBe('pty:abc')
  })

  it('重复 openTab 同一个会话只得到一个 tab', () => {
    let wb = EMPTY_WORKBENCH
    wb = openTab(wb, { kind: 'terminal', seq: 1, sessionId: 'abc' })
    wb = openTab(wb, { kind: 'terminal', seq: 2, sessionId: 'abc' })
    expect(wb.groups[0].tabs).toHaveLength(1)
  })

  it('不同会话各占一个 tab', () => {
    let wb = EMPTY_WORKBENCH
    wb = openTab(wb, { kind: 'terminal', seq: 1, sessionId: 'a' })
    wb = openTab(wb, { kind: 'terminal', seq: 2, sessionId: 'b' })
    expect(wb.groups[0].tabs).toHaveLength(2)
  })

  it('nextTerminalSeq 不受 sessionId 影响', () => {
    let wb = EMPTY_WORKBENCH
    wb = openTab(wb, { kind: 'terminal', seq: 1, sessionId: 'a' })
    expect(nextTerminalSeq(wb)).toBe(2)
  })
})
```

（`dedupKey` / `EMPTY_WORKBENCH` / `openTab` / `nextTerminalSeq` 若尚未在该测试文件里 import，一并补上。）

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npx vitest run src/app/workbench/tabs.test.ts
```

预期：`pty:abc` 那条得到 `null`；重复 open 那条得到 2 个 tab。

- [ ] **Step 3: 写实现**

`tabs.ts` 的 `TabContent`：

```typescript
export type TabContent =
  | { kind: 'blank' }
  // sessionId 是服务端会话的 id，**建出来之后**才有。
  //
  // 为什么可选而不是必填：tab 先出现、会话后建立——用户点「终端」的那一刻
  // 界面就该有反应，不能等一次网络往返。会话建成后由 TerminalTab 回填。
  | { kind: 'terminal'; seq: number; sessionId?: string }
  | { kind: 'file'; rel: string }
  | { kind: 'tui'; taskId: string }
```

`dedupKey`：

```typescript
// dedupKey 返回一个 tab 内容的去重键；返回 null 表示这种内容**永不去重**。
//
// 终端分两种情况：
//   - 还没有 sessionId：没有「目标」，永不去重——再开一个终端就是真的想要
//     第二个终端，把它折叠到已有终端上是把用户的意图吃掉了
//   - 已有 sessionId：目标就是那个服务端会话。刷新页面时会话列表与残留 tab
//     可能同时命中同一个会话，不去重就会长出两个连着同一个 PTY 的 tab
export function dedupKey(c: TabContent): string | null {
  switch (c.kind) {
    case 'file':
      return `file:${c.rel}`
    case 'tui':
      return `tui:${c.taskId}`
    case 'terminal':
      return c.sessionId ? `pty:${c.sessionId}` : null
    default:
      return null
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
cd web && npx vitest run src/app/workbench/ && npm run typecheck
```

预期：新增 5 条与既有 tabs/WorkbenchPage/useWorkbench 用例全 PASS。

- [ ] **Step 5: 加意图注释（自检）**

`tabs.ts` 是纯函数模型层，无 I/O、无错误分支——**不打日志**，但两处注释必须在：`sessionId` 为何可选（tab 先出现、会话后建立）、`dedupKey` 为何分两种情况。

- [ ] **Step 6: 提交**

```bash
git add web/src/app/workbench/tabs.ts web/src/app/workbench/tabs.test.ts && git commit -m "feat(web): 终端 tab 承载服务端会话 id，按会话去重"
```

---

### Task 11: `TerminalTab` 接 xterm

**Files:**
- Modify: `web/src/app/workbench/TerminalTab.tsx`（整文件重写，现 37 行占位）
- Modify: `web/package.json`（三个新依赖）
- Test: `web/src/app/workbench/TerminalTab.test.tsx`

**Interfaces:**
- Consumes: Task 9 的 `createPtySession` / `connectPty` / `PtyHandle`；`BaseDir`
- Produces: `TerminalTab` 的新 props —
  `{ base: BaseDir; seq: number; sessionId?: string; onSession: (id: string) => void }`

- [ ] **Step 1: 装依赖**

```bash
cd web && npm i @xterm/xterm@^5.5.0 @xterm/addon-fit@^0.10.0 @xterm/addon-webgl@^0.18.0
```

三个都是稳定版、不带补丁（spec §8.4）。装完 `git diff package.json` 确认只多了这三行。

- [ ] **Step 2: 写失败测试**

`web/src/app/workbench/TerminalTab.test.tsx`：

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { TerminalTab } from './TerminalTab'
import type { BaseDir } from './useWorkbench'

// xterm 要量真实字体尺寸，jsdom 给不了。整体替身：本测试关心的是
// 「什么时候建会话、拿什么参数连、收到帧之后往终端写什么」，
// 不是 xterm 自己的渲染——那是上游的测试职责。
const termInstance = {
  cols: 100,
  rows: 30,
  open: vi.fn(),
  write: vi.fn(),
  writeln: vi.fn(),
  clear: vi.fn(),
  focus: vi.fn(),
  dispose: vi.fn(),
  loadAddon: vi.fn(),
  onData: vi.fn(),
  onResize: vi.fn(),
}
vi.mock('@xterm/xterm', () => ({ Terminal: vi.fn(() => termInstance) }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: vi.fn(() => ({ fit: vi.fn() })) }))
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: vi.fn(() => ({})) }))

const createPtySession = vi.fn()
const connectPty = vi.fn()
vi.mock('../../api/client', () => ({ createPtySession: (...a: unknown[]) => createPtySession(...a) }))
vi.mock('../../api/pty', () => ({ connectPty: (...a: unknown[]) => connectPty(...a) }))

const WS: BaseDir = {
  key: '/home/dev/handoff', kind: 'workspace', path: '/home/dev/handoff',
  label: 'main', projectName: 'handoff', machine: '',
}
const HOME: BaseDir = {
  key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '',
}

beforeAll(() => {
  // jsdom 没有 ResizeObserver，而组件用它跟随容器尺寸
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.clearAllMocks()
  createPtySession.mockResolvedValue({ id: 'new-1', base_path: WS.path })
  connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize: vi.fn() })
})

describe('TerminalTab', () => {
  it('没有会话 id 时先建会话，参数取自基准目录与当前尺寸', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(createPtySession).toHaveBeenCalledTimes(1))
    expect(createPtySession).toHaveBeenCalledWith(
      { base_kind: 'workspace', base_path: '/home/dev/handoff', cols: 100, rows: 30 },
      '',
    )
  })

  it('home 基准不把 "~" 发给后端——那不是一个服务端认识的路径', async () => {
    render(<TerminalTab base={HOME} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())
    expect(createPtySession.mock.calls[0][0]).toMatchObject({ base_kind: 'home', base_path: '' })
  })

  it('建成后把会话 id 回报给上层，供 tab 记住', async () => {
    const onSession = vi.fn()
    render(<TerminalTab base={WS} seq={1} onSession={onSession} />)
    await waitFor(() => expect(onSession).toHaveBeenCalledWith('new-1'))
  })

  it('已有会话 id 时直接接流，不再建第二个会话', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="old-9" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    expect(createPtySession).not.toHaveBeenCalled()
    expect(connectPty.mock.calls[0][0]).toMatchObject({ sessionId: 'old-9', machine: '' })
  })

  it('收到字节写进终端', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onData(new TextEncoder().encode('hello'))
    expect(termInstance.write).toHaveBeenCalled()
  })

  it('attached 带 truncated 时先清屏——不清就会把同一段输出画两遍', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 4096, truncated: true })
    expect(termInstance.clear).toHaveBeenCalled()
  })

  it('attached 不带 truncated 时不清屏', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 0, truncated: false })
    expect(termInstance.clear).not.toHaveBeenCalled()
  })

  it('退出后在终端下方显示退出码，tab 不自己消失', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onExit(7)
    expect(await screen.findByText(/退出码 7/)).toBeInTheDocument()
  })

  it('建会话失败时说实话，不是白屏', async () => {
    createPtySession.mockRejectedValue(new Error('该 agentd 所在平台不支持 PTY 终端'))
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    expect(await screen.findByText(/不支持 PTY 终端/)).toBeInTheDocument()
  })

  it('卸载时只断连接，不删会话', async () => {
    const handle = { close: vi.fn(), send: vi.fn(), resize: vi.fn() }
    connectPty.mockReturnValue(handle)
    const { unmount } = render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    unmount()
    expect(handle.close).toHaveBeenCalled()
    expect(termInstance.dispose).toHaveBeenCalled()
  })
})
```

- [ ] **Step 3: 跑测试确认失败**

```bash
cd web && npx vitest run src/app/workbench/TerminalTab.test.tsx
```

预期：全部 FAIL（现组件不接受 `onSession`，也不发任何请求）。

- [ ] **Step 4: 写实现**

`web/src/app/workbench/TerminalTab.tsx` 整文件替换：

```tsx
// TerminalTab —— 中央区的真终端（W4 PTY spec §6）。
//
// 职责：
//   - 挂 xterm，把一个服务端 PTY 会话的字节流画出来
//   - 没有会话时先建一个，并把 id 回报给 tab（onSession）
//   - 按键上送、尺寸上送、断线重连（重连逻辑在 api/pty.ts，这里只消费）
//   - shell 退出后在下方显示退出码，tab 留着等用户自己关
//
// 边界：
//   - **不删会话**。卸载只断 WS——切 tab、切基准目录、关页面都不该杀掉
//     跑了一晚上的 build（spec §6.2）。删会话是 × 按钮的事，在 Shell 里
//   - 不做重连退避、不认识 WS 帧格式：那都在 api/pty.ts
//   - 不判断这台机器支不支持 PTY：那是 Shell 的降级门（Task 14）。
//     这里只兜住「真发了请求才知道不支持」的那一路（501）
//
// 关于「切 tab 就重放整段回放」：WorkbenchPage 只渲染激活 tab，切走即卸载，
// 游标随之丢失，切回来从 since=0 起重放整个环形缓冲。这是**有意**的——
// 环形缓冲的存在就是为了让任何一次重新接入都能重建屏幕，256 KiB 的重放
// 远比维护一份前端的「上次看到哪」更可靠。
import { useEffect, useRef, useState } from 'react'
import { TerminalSquare } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import '@xterm/xterm/css/xterm.css'
import { createPtySession } from '../../api/client'
import { connectPty, type PtyHandle } from '../../api/pty'
import type { BaseDir } from './useWorkbench'

export interface TerminalTabProps {
  base: BaseDir
  seq: number
  // sessionId 缺席 = 这个 tab 还没有会话，挂载时建一个。
  sessionId?: string
  // onSession 把新建会话的 id 交回上层写进 TabContent。必须回报：
  // 不回报的话切一次 tab 就会再建一个会话，用户每切一次多留一个 shell。
  onSession: (id: string) => void
}

// ptyBase 把一个基准目录翻译成建会话请求的两个字段。
//
// home 基准的 path 是字面量 '~'，**不是**服务端认识的路径（useWorkbench 里
// 早有这条纪律）。base_kind=home 时服务端用它自己的 $HOME，所以这里发空串，
// 免得将来有人把 '~' 当路径去 stat。
function ptyBase(base: BaseDir): { base_kind: string; base_path: string } {
  return { base_kind: base.kind, base_path: base.kind === 'home' ? '' : base.path }
}

export function TerminalTab({ base, seq, sessionId, onSession }: TerminalTabProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [error, setError] = useState<string | null>(null)
  // exit 为 undefined 表示还活着；已退出时它是退出码（对端没给退出码时是 null）
  const [exit, setExit] = useState<number | null | undefined>(undefined)
  const [status, setStatus] = useState<'connecting' | 'open' | 'closed'>('connecting')

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let disposed = false
    let handle: PtyHandle | null = null

    // 终端底色固定为深色：xterm 的 WebGL 渲染器不支持透明背景，跟着页面主题
    // 走会在浅色主题下拿到一块透不过去的白底。终端惯例本就是深色，不折腾。
    const term = new Terminal({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 12,
      cursorBlink: true,
      scrollback: 5000,
      theme: { background: '#0b0b0c' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    try {
      term.loadAddon(new WebglAddon())
    } catch (err) {
      // WebGL 不可用（远程桌面、禁用了硬件加速、老显卡）时 xterm 自动回退到
      // canvas/DOM 渲染：慢一点，但**不能白屏**（spec §6.3）。吞掉这个异常正是
      // 为了让回退发生，所以它不是「静默失败」——功能完好，只是渲染路径不同。
      console.warn('WebGL 渲染器不可用，已回退到 canvas 渲染', err)
    }
    fit.fit()

    const start = async () => {
      let id = sessionId
      if (!id) {
        const created = await createPtySession(
          { ...ptyBase(base), cols: term.cols, rows: term.rows },
          base.machine,
        )
        id = created.id
        if (disposed) return
        onSession(id)
      }
      if (disposed) return
      handle = connectPty({
        sessionId: id,
        machine: base.machine || undefined,
        onAttached: ({ truncated }) => {
          // 服务端说中间丢了一段：屏幕上现有的内容与即将到来的回放接不上，
          // 不清就会把同一段输出画两遍
          if (truncated) term.clear()
        },
        onData: (bytes) => term.write(bytes),
        onExit: (code) => {
          setExit(code ?? null)
          setStatus('closed')
        },
        onStatus: setStatus,
        onError: (message) => setError(message),
        onTerminal: ({ message }) => setError(message),
      })
      term.onData((d) => handle?.send(new TextEncoder().encode(d)))
      term.onResize(({ cols, rows }) => handle?.resize(cols, rows))
      term.focus()
    }

    start().catch((err: unknown) => {
      if (disposed) return
      setError(err instanceof Error ? err.message : String(err))
    })

    const ro = new ResizeObserver(() => fit.fit())
    ro.observe(host)

    return () => {
      disposed = true
      ro.disconnect()
      // 只断连接，不发 DELETE：服务端会话继续跑
      handle?.close()
      term.dispose()
    }
    // 依赖故意只有会话身份与基准：base.label 之类的展示字段变化不该重建终端
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, base.key, base.machine])

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <TerminalSquare className="size-3.5" />
        <span className="font-mono">
          {base.label}
          {seq > 1 && ` (${seq})`}
        </span>
        {status === 'connecting' && exit === undefined && <span>连接中…</span>}
        <span className="ml-auto font-mono">{base.path}</span>
      </div>
      <div ref={hostRef} className="min-h-0 flex-1 bg-[#0b0b0c]" />
      {error !== null && (
        <div className="border-t px-3 py-1.5 text-xs text-destructive">{error}</div>
      )}
      {exit !== undefined && (
        <div className="border-t px-3 py-1.5 text-xs text-muted-foreground">
          {exit === null ? 'shell 已退出（对端未给出退出码）' : `shell 已退出，退出码 ${exit}`}
          ．关闭这个 tab 即可清理
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
cd web && npx vitest run src/app/workbench/TerminalTab.test.tsx && npm run typecheck && npm run lint
```

预期：10 个用例全 PASS。

- [ ] **Step 6: 加关键节点日志（自检）**

浏览器侧的可观测面是**界面本身**，逐条核对「用户能不能看出发生了什么」：

- [ ] 建会话失败 → `error` 横幅显示原文（含 501 的「平台不支持」）
- [ ] WS 断开 → `onError` 进 `error` 横幅；重连中 → 顶栏「连接中…」
- [ ] 1008 终止 → `onTerminal` 进 `error` 横幅（不是静默停住）
- [ ] shell 退出 → 底部退出码行（**成功路径也有产出**，不是静默消失）
- [ ] WebGL 回退是唯一一处 `console.warn`：它对用户无影响，只对排障有用，
      注释已写明它为什么不是「静默吞异常」
- [ ] 红线复核：不打印任何按键内容、不打印 PTY 输出字节——用户会在终端里
      输密码

- [ ] **Step 7: 加意图注释（自检）**

确认：文件头的职责/边界 + 「切 tab 重放整段」为什么是有意的；`ptyBase` 为何
把 home 的 `~` 变成空串；终端底色为何固定深色；`onSession` 为何必须回报；
`truncated` 为何要清屏；卸载为何只 `close()` 不 DELETE。

- [ ] **Step 8: 提交**

```bash
git add web/package.json web/package-lock.json web/src/app/workbench/TerminalTab.tsx web/src/app/workbench/TerminalTab.test.tsx && git commit -m "feat(web): TerminalTab 接 xterm，接服务端 PTY 会话"
```

---

### Task 12: 能力位的最后一公里（`proto.Machine.pty_supported`）

**Files:**
- Modify: `internal/proto/projects.go:91`（`Machine` 增 `PtySupported *bool`）
- Modify: `internal/agentd/machines.go:115-128`（`fillFromStatus`）
- Modify: `internal/proto/contract_fixture_test.go:239-265`（`machinesSample` 补字段）
- Modify: `web/src/api/types.ts`（`Machine` 镜像）
- Test: `internal/agentd/machines_test.go`

**Interfaces:**
- Consumes: Task 5 的 `StatusResp.PtySupported *bool`
- Produces: `proto.Machine.PtySupported *bool`；TS `Machine.pty_supported?: boolean | null`

**为什么走 `/api/machines` 而不是新开一个能力接口**：`/api/machines` 的全部
工作就是「向每台机器打一次 `GET /api/status`，把结果投影成 `Machine`」
（`fillFromStatus`）。能力位已经在那个 `StatusResp` 里躺着了，投影时丢掉它才是
浪费。新开接口等于把同一次探活再打一遍。

- [ ] **Step 1: 写失败测试**

`internal/agentd/machines_test.go` 追加（沿用该文件「用真 server 当远程」的既有姿势）：

```go
// TestMachinesCarriesPtyCapability 断言：探活拿到的 pty_supported 被投影进
// Machine，而不是在 fillFromStatus 里丢掉。
func TestMachinesCarriesPtyCapability(t *testing.T) {
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := getMachines(t, local)
	for _, m := range resp.Machines {
		if !m.Reachable {
			continue
		}
		if m.PtySupported == nil {
			t.Fatalf("机器 %q 可达却没带能力位：nil 是「对端没上报」，这里对端明明上报了", m.Name)
		}
	}
}

// TestMachinesUnreachableHasNilCapability 断言：够不着的机器能力位是 nil，
// **不是 false**——「探不到」与「明确不支持」是两个结论。
func TestMachinesUnreachableHasNilCapability(t *testing.T) {
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"ghost": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, m := range getMachines(t, local).Machines {
		if m.Name == "ghost" && m.PtySupported != nil {
			t.Fatalf("够不着的机器能力位必须是 nil，实得 %v", *m.PtySupported)
		}
	}
}

// getMachines 带 Bearer 请求 /api/machines 并解出响应。
func getMachines(t *testing.T, e *testAgentdEnv) proto.MachinesResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/api/machines", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var out proto.MachinesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解响应失败: %v", err)
	}
	return out
}
```

（`getMachines` 若该文件里已有同名助手，复用既有的，别加第二个。）

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestMachines
```

预期：编译失败，`m.PtySupported undefined`。

- [ ] **Step 3: 加字段**

`internal/proto/projects.go` 的 `Machine` 结构体末尾（`Error` 之后）：

```go
	// PtySupported 是这台机器的 PTY 能力位，探活时从它的 StatusResp 投影而来。
	//
	// 三态，与 StatusResp.PtySupported 同一纪律：
	//   nil   = 没上报（对端版本过旧，或这台机器压根没探到）
	//   false = 平台明确不支持
	//   true  = 支持
	// 消费方（控制台）据此决定终端入口画什么。**nil 不许当 false 用**：
	// 那会让老版本 agentd 上的终端入口凭空消失，而它其实可能是能用的。
	PtySupported *bool `json:"pty_supported,omitempty"`
```

- [ ] **Step 4: 投影它**

`internal/agentd/machines.go` 的 `fillFromStatus` 末尾追加一行：

```go
	// 能力位原样搬运，包括 nil：探到了但对端没这个字段，结论就是「没上报」
	m.PtySupported = st.PtySupported
```

- [ ] **Step 5: 跑测试确认通过 + 重生成 fixture**

```bash
go test ./internal/agentd/ -run TestMachines
go test ./internal/proto/ -run TestContractFixtures -update && go test ./internal/proto/
```

`machinesSample`（`contract_fixture_test.go:239`）的两台机器分别覆盖两种结局，
能力位也照此分：本机那台加 `PtySupported: &ptyOK`（`ptyOK := true` 在函数开头
声明），不可达的 `devbox` **保持缺席**——探不到就是没上报。重生成后
`git diff web/src/api/testdata/MachinesResp.json` 应当只多一个 `pty_supported: true`。

- [ ] **Step 6: 镜像 TS 类型**

`web/src/api/types.ts` 的 `Machine`：

```typescript
  // pty_supported 三态：缺席/null = 对端没上报（**不是**不支持），
  // false = 平台明确不支持，true = 支持。
  pty_supported?: boolean | null
```

跑 `cd web && npx vitest run src/api/contract.test.ts` 确认契约测试仍绿。

- [ ] **Step 7: 加关键节点日志（自检）**

`fillFromStatus` 是纯投影函数，无 I/O 无错误分支，**不加日志**——它的上游
`probeMachine` 已经打了「机器探活成功 / 失败」并带 machine 名。核对：

- [ ] 探活失败仍走既有的 `s.log.Warn`（本次改动没有新增静默分支）
- [ ] 红线复核：日志里没有 target token

- [ ] **Step 8: 加意图注释（自检）**

确认：`PtySupported` 字段的三态注释写明「nil 不许当 false」；
`fillFromStatus` 那行注释说明「包括 nil 也原样搬」。

- [ ] **Step 9: 提交**

```bash
git add internal/proto/ internal/agentd/machines.go internal/agentd/machines_test.go web/src/api/ && git commit -m "feat: /api/machines 投影 pty_supported 能力位"
```

---

### Task 13: 会话里有没有前台进程

**Files:**
- Modify: `internal/ptyhost/platform_unix.go`、`internal/ptyhost/platform_other.go`
- Modify: `internal/ptyhost/ptyhost.go`（`Session` 增 `Foreground bool`，快照处填充）
- Modify: `internal/proto/pty.go`（`PtySession` 增 `Foreground bool`）
- Modify: `internal/proto/contract_fixture_test.go`（两个 PTY 样本补字段）
- Modify: `web/src/api/types.ts`（`PtySession` 镜像）
- Test: `internal/ptyhost/platform_unix_test.go`（追加）

**Interfaces:**
- Consumes: Task 2 的 `startPty`、Task 3 的 `Host.List/Get`
- Produces: `ptyhost.Session.Foreground bool`、`proto.PtySession.Foreground bool`、
  TS `PtySession.foreground: boolean`

**这个字段是给谁用的**：spec §6.2 有一格是「点 × 且会话内还有前台进程 → 先弹
确认」。没有这个字段，Task 15 的 × 只有两条路——每次都弹（烦），或者从不弹
（一次误点杀掉跑了一晚上的 build）。`TIOCGPGRP` 正是内核给出的那个判据，而
`golang.org/x/sys` 已经是直接依赖，成本是一次 ioctl。

- [ ] **Step 1: 写失败测试**

`internal/ptyhost/platform_unix_test.go` 追加：

```go
// TestForegroundPgidIdleShellIsItself 断言：shell 空闲时前台进程组就是它自己，
// 判据据此得出「没有前台进程」。
func TestForegroundPgidIdleShellIsItself(t *testing.T) {
	p, err := startPty("/bin/sh", []string{"sh"}, t.TempDir(), []string{"PS1=$ "}, 80, 24)
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer killPty(p)

	fg, ok := foregroundPgid(p.ptmx)
	if !ok {
		t.Fatalf("拿不到前台进程组")
	}
	if fg != p.pid {
		t.Fatalf("空闲 shell 的前台组应当是它自己：fg=%d pid=%d", fg, p.pid)
	}
}

// TestForegroundPgidRunsChild 断言：shell 里跑一个前台命令时，前台进程组换成
// 那个命令的组——这正是「别把用户的 build 静默杀掉」所需要的判据。
func TestForegroundPgidRunsChild(t *testing.T) {
	p, err := startPty("/bin/sh", []string{"sh"}, t.TempDir(), []string{"PS1=$ "}, 80, 24)
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer killPty(p)

	if _, err := p.ptmx.Write([]byte("sleep 5\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	// 轮询而不是 sleep 一个定值：shell 起子进程的耗时在不同机器上差一个数量级，
	// 定值要么慢要么偶发失败
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fg, ok := foregroundPgid(p.ptmx); ok && fg != p.pid {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("3 秒内前台进程组始终等于 shell 自己，前台命令没被识别出来")
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/ptyhost/ -run TestForegroundPgid
```

预期：编译失败，`undefined: foregroundPgid`。

- [ ] **Step 3: 写平台原语**

`internal/ptyhost/platform_unix.go` 追加（`golang.org/x/sys/unix` 已是直接依赖）：

```go
// foregroundPgid 读出该 PTY 当前的前台进程组 id。
//
// 参数：ptmx 为主设备端文件
//
// 返回：
//   - pgid: 前台进程组 id
//   - ok: 读到了才为 true。读不到的两种情形都归到 false：shell 已退出
//     （fd 已关）、或本平台不认这个 ioctl
//
// 注意：调用方要的通常不是 pgid 本身，而是「它是否 != shell 自己的 pid」——
// 相等意味着 shell 在等提示符（没有前台命令），不等意味着有个命令正跑在前台。
func foregroundPgid(ptmx *os.File) (int, bool) {
	pgid, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil || pgid <= 0 {
		return 0, false
	}
	return pgid, true
}
```

`internal/ptyhost/platform_other.go`：

```go
// foregroundPgid 在没有 PTY 的平台上恒为「读不到」。
//
// 返回 false 而不是「没有前台进程」：这里连会话都开不出来，给一个 bool 结论
// 等于替一个不存在的东西作证。
func foregroundPgid(*os.File) (int, bool) { return 0, false }
```

- [ ] **Step 4: 挂进快照**

`internal/ptyhost/ptyhost.go` 的 `Session` 结构体：

```go
	// Foreground 表示会话里当前有一个跑在前台的命令（前台进程组 ≠ shell 自己）。
	//
	// 为什么是 bool 而不是三态：读不到（shell 已退出、平台不支持）时结论是
	// **false**——两种情形下「关掉它会打断什么」的答案都是「不会」，与真的空闲
	// 同解。这与 PtySupported 那种「不知道」不是一回事，不要照抄那条纪律。
	Foreground bool
```

以及构造快照的那个方法（`Host.snapshot`）里补一行：

```go
	fg, ok := foregroundPgid(s.proc.ptmx)
	// 相等 = shell 自己在前台，也就是在等提示符
	snap.Foreground = ok && fg != s.proc.pid
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/ptyhost/ && GOOS=windows go build ./...
```

- [ ] **Step 6: 过线格式**

`internal/proto/pty.go` 的 `PtySession` 增字段（放在 `Attached` 之后）：

```go
	// Foreground 表示会话里有命令跑在前台。控制台据此决定关 tab 时要不要先确认
	//（spec §6.2）。**不带 omitempty**：false 是一个有意义的结论（「空闲，随便关」），
	// 缺键会让前端分不清它和「这版服务端还不认识这个字段」。
	Foreground bool `json:"foreground"`
```

`internal/agentd/pty_api.go` 的 `ptySessionView` 里搬运它；`web/src/api/types.ts`
的 `PtySession` 加 `foreground: boolean`；两个 PTY 样本
（`ptySessionSample` / `ptySessionsRespSample` 里的 remote）分别设 `true` / `false`
——一活一闲，两种取值都进 fixture。重生成并核对：

```bash
go test ./internal/proto/ -run TestContractFixtures -update && go test ./internal/proto/
cd web && npx vitest run src/api/contract.test.ts
```

- [ ] **Step 7: 加关键节点日志（自检）**

`foregroundPgid` 每次快照都会被调用（列表接口每次刷新一次），**不打日志**：
高频路径，且失败是常态（会话退出后必然读不到）。核对：

- [ ] 它的失败被转成 `Foreground=false` 这个明确结论，不是错误上抛后被吞
- [ ] 注释写清了「返回 false 不等于不知道」，避免下一个人照抄三态纪律
- [ ] 红线复核：不打印 PTY 内容

- [ ] **Step 8: 加意图注释（自检）**

确认：`foregroundPgid` 的返回值语义（调用方真正要的是「!= pid」）；`Foreground`
为何是 bool 而非三态；proto 那边为何不带 `omitempty`。

- [ ] **Step 9: 提交**

```bash
git add internal/ptyhost/ internal/proto/ internal/agentd/pty_api.go web/src/api/ && git commit -m "feat(ptyhost): 上报会话是否有前台进程（TIOCGPGRP）"
```

---

### Task 14: 终端会话进可见性账本

**Files:**
- Modify: `internal/prochost/footprint.go`（新增导出函数 `CountGroup`）
- Modify: `internal/proto/status.go:107-137`（`StatusResp` 增 `PtySessions *int`）、
  `:152-155`（`FootprintResp` 增 `Pty []PtyFootprintRow`）与新类型 `PtyFootprintRow`
- Modify: `internal/agentd/status.go`（`handleStatus` 填会话数）、
  `internal/agentd/server.go:319-332`（`handleFootprint` 追加 PTY 行）
- Modify: `cmd/status.go:98-146`（`renderStatus`）、`cmd/footprint.go:70-95`（`renderFootprint`）
- Test: `internal/prochost/footprint_test.go`、`cmd/status_test.go`、`cmd/footprint_test.go`

**Interfaces:**
- Consumes: Task 3 的 `Host.List`、Task 13 的 `Session.Foreground`
- Produces: `prochost.CountGroup(pgid int) (int, error)`、`proto.PtyFootprintRow`、
  `proto.StatusResp.PtySessions *int`、`proto.FootprintResp.Pty []PtyFootprintRow`

**为什么会话数在 status、进程数在 footprint**：`status` 有一条「不能变成慢命令」
的硬纪律（`FootprintAll` 的注释里写着两者分开的理由）。数会话只是读一个
内存 map 的长度；数进程要枚举全机进程。按这条既有的分工切开，spec §8.2 的
「有几个终端会话」落在 status，「各占多少进程」落在 footprint。

- [ ] **Step 1: 写失败测试（prochost）**

`internal/prochost/footprint_test.go` 追加：

```go
// TestCountGroupCountsOnlyItsOwnGroup 断言：只数同组成员，无关进程不计入。
func TestCountGroupCountsOnlyItsOwnGroup(t *testing.T) {
	stubProcs(t, []procEntry{
		{PID: 300, PGID: 300, StartedAt: t0},     // 组长（PTY 里的 shell）
		{PID: 301, PGID: 300, StartedAt: t0 + 1}, // 它起的命令
		{PID: 302, PGID: 300, StartedAt: t0 + 2},
		{PID: 400, PGID: 400, StartedAt: t0}, // 无关
	})
	n, err := CountGroup(300)
	if err != nil {
		t.Fatalf("不该出错: %v", err)
	}
	if n != 3 {
		t.Fatalf("同组成员应为 3，实得 %d", n)
	}
}

// TestCountGroupEmptyGroupIsZeroNotError 断言：组里一个都没有是 0 而不是错误
//（会话刚退出、进程刚被收走都会走到这里）。
func TestCountGroupEmptyGroupIsZeroNotError(t *testing.T) {
	stubProcs(t, []procEntry{{PID: 400, PGID: 400, StartedAt: t0}})
	n, err := CountGroup(300)
	if err != nil || n != 0 {
		t.Fatalf("空组应当是 (0, nil)，实得 (%d, %v)", n, err)
	}
}

// TestCountGroupPropagatesEnumFailure 断言：枚举失败必须上抛，
// **不能降级成 0**——0 会被渲染成「没有残留」，那是个假结论。
func TestCountGroupPropagatesEnumFailure(t *testing.T) {
	orig := enumProcsFn
	enumProcsFn = func() ([]procEntry, error) { return nil, errNotSupported }
	t.Cleanup(func() { enumProcsFn = orig })
	if _, err := CountGroup(300); err == nil {
		t.Fatalf("枚举失败必须上抛")
	}
}

// stubProcs 把进程枚举替换成固定结果（沿用本文件既有的 enumProcsFn 接缝）。
func stubProcs(t *testing.T, procs []procEntry) {
	t.Helper()
	orig := enumProcsFn
	enumProcsFn = func() ([]procEntry, error) { return procs, nil }
	t.Cleanup(func() { enumProcsFn = orig })
}
```

（若 `stubProcs` 与文件里既有的替身助手重名，复用既有的。）

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/prochost/ -run TestCountGroup
```

预期：`undefined: CountGroup`。

- [ ] **Step 3: 写 `CountGroup`**

`internal/prochost/footprint.go` 追加：

```go
// CountGroup 数出进程组 pgid 当前有多少个属于本 uid 的成员。
//
// 参数：pgid 为进程组 id（PTY 会话里就是 shell 自己的 pid，因为它 setsid 后
// 是组长）
//
// 返回：成员数；枚举失败时上抛错误（**不降级成 0**——0 会被渲染成「没有残留」，
// 那是个我们并没有得出的结论）
//
// 注意：
//   - 与 Footprint 不同，这里**没有启动时刻校验**。Footprint 面对的是可能已死的
//     shim，pid 复用是真实风险；本函数的调用方仍然持有组长进程（会话活着、
//     *os.Process 未被回收），此时组长的 pid 不可能被复用，多一道校验只会
//     要求调用方额外记一个内核时间戳
//   - 因此**只能对仍存活的组调用**。对已退出的会话调用它，数出来的东西没有身份
//   - 只读，绝不发信号
func CountGroup(pgid int) (int, error) {
	procs, err := enumProcsFn()
	if err != nil {
		log().Error("进程组计数失败", "pgid", pgid, "cause", err)
		return 0, err
	}
	n := 0
	for _, p := range procs {
		if p.PGID == pgid {
			n++
		}
	}
	log().Debug("进程组计数完成", "pgid", pgid, "members", n)
	return n, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/prochost/
```

- [ ] **Step 5: 扩线格式**

`internal/proto/status.go`，`StatusResp` 末尾：

```go
	// PtySessions 是当前活着的终端会话数。指针 + omitempty，与 Proc 同一纪律：
	// nil = 对端没上报，渲染时整行不打印；0 = 确实一个都没有。
	//
	// 为什么 status 只给个数、不给每个会话占多少进程：数进程要枚举全机进程，
	// 而 status 有「不能变成慢命令」的硬纪律。进程数在 /api/footprint 里给。
	PtySessions *int `json:"pty_sessions,omitempty"`
```

新类型（挨着 `FootprintRow` 放）：

```go
// PtyFootprintRow 是一个终端会话的足迹体检结果。
//
// Procs 为指针：数不出来（平台不支持枚举）时是 nil，**不是 0**——与 ProcUsage
// 同一条理由，0 看起来像结论。
type PtyFootprintRow struct {
	ID         string `json:"id"`
	BasePath   string `json:"base_path"`
	PID        int    `json:"pid"`
	Procs      *int   `json:"procs,omitempty"`
	Foreground bool   `json:"foreground"`
}
```

`FootprintResp` 增字段：

```go
	// Pty 是终端会话的足迹。会话只在内存里，所以这一段与 Rows 不同——
	// 它不含历史，列出的都是此刻活着的会话。
	Pty []PtyFootprintRow `json:"pty,omitempty"`
```

- [ ] **Step 6: 服务端填数**

`internal/agentd/status.go` 的 `handleStatus`，在填 `Proc` 附近：

```go
	// 会话数是读一个内存 map 的长度，不枚举进程——status 必须保持快
	if s.pty != nil {
		n := len(s.pty.List())
		resp.PtySessions = &n
	}
```

`internal/agentd/server.go` 的 `handleFootprint`，在 `writeJSON` 之前：

```go
	resp.Pty = s.ptyFootprint()
```

并在 `pty_api.go` 里加：

```go
// ptyFootprint 体检当前全部终端会话的进程占用。
//
// 返回：每个活着的会话一行。会话已退出的不入表——它已经没有进程组可数了。
//
// 注意：数不出来（平台不支持进程枚举）时 Procs 留 nil 并记一条日志，不写 0。
func (s *Server) ptyFootprint() []proto.PtyFootprintRow {
	if s.pty == nil {
		return nil
	}
	sessions := s.pty.List()
	rows := make([]proto.PtyFootprintRow, 0, len(sessions))
	for _, sess := range sessions {
		if sess.ExitCode != nil {
			continue
		}
		row := proto.PtyFootprintRow{
			ID: sess.ID, BasePath: sess.BasePath, PID: sess.PID, Foreground: sess.Foreground,
		}
		if n, err := prochost.CountGroup(sess.PID); err == nil {
			row.Procs = &n
		} else {
			s.log.Warn("终端会话足迹：进程组数不出来，该字段留空",
				"session", sess.ID, "pid", sess.PID, "cause", err)
		}
		rows = append(rows, row)
	}
	s.log.Info("终端会话足迹完成", "sessions", len(rows))
	return rows
}
```

- [ ] **Step 7: 写 CLI 渲染的失败测试**

`cmd/status_test.go` 追加：

```go
// TestRenderStatusShowsPtySessions 验证终端会话数出现在 status，
// 且 nil 时整行不打印（对端没上报，编一个 0 就是假结论）。
func TestRenderStatusShowsPtySessions(t *testing.T) {
	two := 2
	st := &proto.StatusResp{
		Listen: "127.0.0.1:7777", DataDir: "/d", StartedAt: time.Now(),
		Executors: []string{"opencode"}, DefaultExecutor: "opencode",
		TaskCounts: map[string]int{}, PtySessions: &two,
	}
	var buf bytes.Buffer
	renderStatus(&buf, "http://x", proto.BuildInfo{}, st)
	if !strings.Contains(buf.String(), "终端") || !strings.Contains(buf.String(), "2 个会话") {
		t.Fatalf("应含终端会话行:\n%s", buf.String())
	}

	var nilBuf bytes.Buffer
	st.PtySessions = nil
	renderStatus(&nilBuf, "http://x", proto.BuildInfo{}, st)
	if strings.Contains(nilBuf.String(), "终端") {
		t.Fatalf("PtySessions=nil 时不该打这一行:\n%s", nilBuf.String())
	}
}
```

`cmd/footprint_test.go` 追加：

```go
// ptyFootprintBody 是带终端会话段的体检结果：一个有前台命令、一个空闲、
// 一个数不出进程数。
const ptyFootprintBody = `{"usage":{"used":346,"limit":2666},"rows":[],"pty":[
	{"id":"2f0f6a3c-8f1e-4f2a-9a77-1c2d3e4f5a6b","base_path":"/home/dev/handoff","pid":48213,"procs":4,"foreground":true},
	{"id":"9b8a7c6d-5e4f-4a3b-2c1d-0e9f8a7b6c5d","base_path":"/home/dev","pid":48999,"procs":1,"foreground":false},
	{"id":"3c3c3c3c-1111-2222-3333-444455556666","base_path":"/x","pid":50001,"foreground":false}]}`

// TestFootprintShowsPtySessions 断言：终端会话进账本，且 procs 缺席时如实说
// 「未知」而不是渲染成 0。
//
// **第三行是重点**：会话足迹的整个立论是「先让占用可见」，用一个 0 盖住
// 「我们数不出来」正是它要防的事。
func TestFootprintShowsPtySessions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(ptyFootprintBody))
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if err != nil {
		t.Fatalf("footprint 应成功，得到错误: %v", err)
	}
	for _, want := range []string{"终端", "2f0f6a3c", "4 进程", "前台", "50001", "未知"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestFootprintNoPtySectionWhenEmpty 断言：没有终端会话时不打这一段——
// 空标题也是噪音。
func TestFootprintNoPtySectionWhenEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(footprintBody))
	}))
	t.Cleanup(ts.Close)

	out, _ := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if strings.Contains(out, "终端") {
		t.Fatalf("没有会话时不该打终端段:\n%s", out)
	}
}
```

- [ ] **Step 8: 跑测试确认失败**

```bash
go test ./cmd/ -run 'TestRenderStatusShowsPtySessions|TestFootprintShowsPtySessions|TestFootprintNoPtySection'
```

预期：编译失败（`PtySessions` 未定义）后，渲染断言失败。

- [ ] **Step 9: 写渲染**

`cmd/status.go` 的 `renderStatus`，紧接在 `Proc` 那一行之后：

```go
	// 同上：nil 整行不打。放在进程行之后——会话是进程占用的一个来源，
	// 先给总量再给来源
	if st.PtySessions != nil {
		fmt.Fprintf(w, "终端     %d 个会话（handoff footprint 看各自占用）\n", *st.PtySessions)
	}
```

`cmd/footprint.go` 的 `renderFootprint` 末尾（任务行之后）：

```go
	// 终端会话单列一段：它们不是任务，没有 task_id，也不参与 --all 的过滤规则
	//（会话本来就只有活着的才在表里，没有「干净的历史行」需要折叠）
	if len(fp.Pty) > 0 {
		fmt.Fprintln(w, "终端会话")
		for _, p := range fp.Pty {
			procs := "未知进程数"
			if p.Procs != nil {
				procs = fmt.Sprintf("%d 进程", *p.Procs)
			}
			line := fmt.Sprintf("  %s  %s  pid %d  %s", short8(p.ID), p.BasePath, p.PID, procs)
			if p.Foreground {
				// 关掉它会打断正在跑的东西——这是「能不能顺手清掉」的关键信息
				line += "  ⚠ 有前台命令"
			}
			fmt.Fprintln(w, line)
		}
	}
```

- [ ] **Step 10: 跑测试确认通过**

```bash
go test ./cmd/ ./internal/... && go vet ./... && GOOS=windows go build ./...
go test ./internal/proto/ -run TestContractFixtures -update && go test ./internal/proto/
```

（`StatusResp` 与 `FootprintResp` 都变了，fixture 必然要重生成；`FootprintResp`
若不在 fixture 表里则只有前者变化。）

- [ ] **Step 11: 加关键节点日志（自检）**

- [ ] `CountGroup` 枚举失败 → `Error` 带 pgid 与 cause；成功 → `Debug` 带成员数
- [ ] `ptyFootprint` 数不出来 → `Warn` 带 session 与 pid，并**明说该字段留空**
- [ ] `ptyFootprint` 成功路径 → `Info` 带会话数（不是静默返回）
- [ ] `handleStatus` 不为会话数新增日志：它已有请求级日志，高频路径不加噪
- [ ] 红线复核：日志里只有会话 id 与 pid，没有 base_path 之外的任何会话内容，
      更没有 PTY 字节

- [ ] **Step 12: 加意图注释（自检）**

确认：`CountGroup` 为何**没有**启动时刻校验、以及由此而来的「只能对存活的组
调用」；`PtySessions` 为何只给个数（status 的快纪律）；`Procs` 为何是指针；
footprint 的终端段为何不参与 `--all` 过滤。

- [ ] **Step 13: 提交**

```bash
git add internal/prochost/ internal/proto/ internal/agentd/ cmd/ web/src/api/ && git commit -m "feat: 终端会话进 status/footprint 可见性账本"
```

---

### Task 15: 会话恢复与 Shell 接线

**Files:**
- Modify: `web/src/app/workbench/useWorkbench.ts:58-73`（`WorkbenchApi` 增 `restoreTerminal`）、`:108-112`
- Create: `web/src/app/workbench/usePtyRestore.ts`
- Modify: `web/src/app/workbench/WorkbenchPage.tsx:25-29`、`:115`（`renderContent` 扩签名）
- Modify: `web/src/app/shell/Shell.tsx:119-139`（接线）
- Test: `web/src/app/workbench/usePtyRestore.test.ts`、`web/src/app/workbench/useWorkbench.test.ts`（追加）、
  `web/src/app/workbench/WorkbenchPage.test.tsx`（追加一条）

**Interfaces:**
- Consumes: Task 9 的 `fetchPtySessions`；Task 10 的 `sessionId`；Task 11 的 `TerminalTab`
- Produces:
  - `WorkbenchApi.restoreTerminal: (b: BaseDir, sessionId: string) => void`
  - `usePtyRestore(restore: (b: BaseDir, sessionId: string) => void): { error: string }`
  - `WorkbenchPageProps.renderContent: (content: TabContent, base: BaseDir, group: number, tabId: string) => ReactNode`

- [ ] **Step 1: 写 `restoreTerminal` 的失败测试**

`web/src/app/workbench/useWorkbench.test.ts` 追加：

```typescript
describe('restoreTerminal', () => {
  it('把会话恢复进目标目录的 tab 组，但**不切换当前基准**', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.restoreTerminal(wsB, 'sess-b'))
    // 选中的仍是 A：页面加载时恢复一批会话，不该把用户的选择拽到最后一条上
    expect(result.current.base?.key).toBe(wsA.key)
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
    act(() => result.current.select(wsB))
    expect(result.current.wb.groups[0].tabs[0].content).toMatchObject({
      kind: 'terminal', sessionId: 'sess-b',
    })
  })

  it('同一个会话恢复两次只得到一个 tab（dedupKey 生效）', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.restoreTerminal(wsA, 's'))
    act(() => result.current.restoreTerminal(wsA, 's'))
    act(() => result.current.select(wsA))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
  })

  it('同目录两个会话各占一个 tab，序号递增', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.restoreTerminal(wsA, 's1'))
    act(() => result.current.restoreTerminal(wsA, 's2'))
    act(() => result.current.select(wsA))
    const seqs = result.current.wb.groups[0].tabs.map((t) => (t.content as { seq: number }).seq)
    expect(seqs).toEqual([1, 2])
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npx vitest run src/app/workbench/useWorkbench.test.ts
```

预期：`result.current.restoreTerminal is not a function`。

- [ ] **Step 3: 实现 `restoreTerminal`**

`useWorkbench.ts` 的 `WorkbenchApi` 增一条：

```typescript
  // restoreTerminal 把一个**已存在于服务端**的会话恢复成 tab。
  //
  // 与 openTerminal 的关键差别：它**不切换当前基准**。页面加载时可能一次恢复
  // 好几个目录下的会话，逐个 select 过去会让用户的选中态落在最后一条上——
  // 那是把「后台恢复」变成了「替用户点了一下左栏」。
  restoreTerminal: (b: BaseDir, sessionId: string) => void
```

实现（注意**不走 mutate**，因为 mutate 会 select）：

```typescript
  const restoreTerminal = useCallback((b: BaseDir, sessionId: string) => {
    setByBase((prev) => {
      const w = prev[b.key] ?? EMPTY_WORKBENCH
      // seq 在 updater 里算：连着恢复多个会话时，闭包外算出来的序号全是旧的
      return { ...prev, [b.key]: openTab(w, { kind: 'terminal', seq: nextTerminalSeq(w), sessionId }) }
    })
  }, [])
```

并加进返回对象。

给 `WorkbenchApi` 加成员会同时打断 `WorkbenchPage.test.tsx` 里那个**逐字枚举全部
成员**的 `api()` 构造器（`:17-30`），typecheck 会报缺字段。补上：

```typescript
    setContent: vi.fn(),
    split: vi.fn(),
    restoreTerminal: vi.fn(),
    ...overrides,
```

- [ ] **Step 4: 跑测试确认通过**

```bash
cd web && npx vitest run src/app/workbench/useWorkbench.test.ts && npx vitest run src/app/workbench/WorkbenchPage.test.tsx
```

- [ ] **Step 5: 写 `usePtyRestore` 的失败测试**

`web/src/app/workbench/usePtyRestore.test.ts`：

```typescript
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePtyRestore } from './usePtyRestore'

const fetchPtySessions = vi.fn()
vi.mock('../../api/client', () => ({ fetchPtySessions: () => fetchPtySessions() }))

function session(over: Record<string, unknown> = {}) {
  return {
    id: 's1', machine: '', base_path: '/home/dev/handoff', base_kind: 'workspace',
    shell: '/bin/zsh', created_at: '2026-08-12T00:00:00Z', cols: 120, rows: 40,
    attached: 0, pid: 1, bytes_out: 0, foreground: false, ...over,
  }
}

beforeEach(() => vi.clearAllMocks())

describe('usePtyRestore', () => {
  it('把工作树会话恢复到与左栏同一个基准键上——否则会长出第二个「同一个目录」', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session()] })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    const [base, id] = restore.mock.calls[0]
    // key 必须与 ProjectTree.workspaceBase 一样是绝对路径
    expect(base).toMatchObject({ key: '/home/dev/handoff', kind: 'workspace', path: '/home/dev/handoff', machine: '' })
    expect(id).toBe('s1')
  })

  it('本机 home 会话落在 HOME_BASE 上', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session({ base_kind: 'home', base_path: '/Users/dev' })] })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    expect(restore.mock.calls[0][0]).toMatchObject({ key: '~', kind: 'home', path: '~' })
  })

  it('远端 home 会话不与本机 home 混在一起', async () => {
    fetchPtySessions.mockResolvedValue({
      sessions: [session({ base_kind: 'home', machine: 'devbox' })],
    })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    expect(restore.mock.calls[0][0].key).toBe('~@devbox')
  })

  it('已退出的会话不恢复——tab 里放一个死会话只会让人以为它还能用', async () => {
    fetchPtySessions.mockResolvedValue({
      sessions: [session({ id: 'dead', exit_code: 0 }), session({ id: 'alive' })],
    })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalledTimes(1))
    expect(restore.mock.calls[0][1]).toBe('alive')
  })

  it('只跑一次：重渲染不会把同一批会话反复往回灌', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session()] })
    const restore = vi.fn()
    const { rerender } = renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    rerender()
    rerender()
    expect(fetchPtySessions).toHaveBeenCalledTimes(1)
  })

  it('拉不到列表时给出原文，不静默', async () => {
    fetchPtySessions.mockRejectedValue(new Error('会话过期'))
    const { result } = renderHook(() => usePtyRestore(vi.fn()))
    await waitFor(() => expect(result.current.error).toContain('会话过期'))
  })
})
```

- [ ] **Step 6: 跑测试确认失败**

```bash
cd web && npx vitest run src/app/workbench/usePtyRestore.test.ts
```

预期：`Failed to resolve import './usePtyRestore'`。

- [ ] **Step 7: 写实现**

`web/src/app/workbench/usePtyRestore.ts`：

```typescript
// usePtyRestore —— 加载时按服务端会话列表恢复终端 tab（spec §6.1）。
//
// 职责：拉一次 GET /api/pty/sessions?scope=all，把每个**活着的**会话恢复成
// 对应基准目录下的一个终端 tab。
//
// 边界：
//   - 不做前端持久化。服务端列表是唯一真相，所以「目录被删了但 tab 还在」
//     这类失效态天然不存在——会话不在列表里就是没有
//   - 只恢复终端 tab。文件 tab 与 TUI tab 仍然刷新即丢（W4 spec §10 原状）
//   - 不切换用户的选中目录：恢复是后台动作，见 restoreTerminal 的注释
//
// 为什么只拉一次而不轮询：会话列表变化的唯一来源是用户自己的操作（开/关），
// 那些路径各自会更新 tab。定时轮询等于每 N 秒向每台远程机打一次探活，
// 换来的只是「别的设备上开的会话会自己冒出来」——那不是本期要的能力。
import { useEffect, useRef, useState } from 'react'
import { fetchPtySessions } from '../../api/client'
import type { PtySession } from '../../api/types'
import { HOME_BASE, type BaseDir } from './useWorkbench'
import { errorMessage } from '../lib/format'

// baseOfSession 把一个会话反解成它所属的基准目录。
//
// 工作树的 key 必须与 ProjectTree.workspaceBase 完全一致（绝对路径）——
// 两边对不上就会出现「左栏点进这个目录，恢复出来的终端却在另一个组里」。
//
// label 退回目录名：会话不带分支信息，而树上的 label 优先用分支名。这只影响
// 标题文字，**不影响归组**（key 相同），用户点一下左栏就会换成带分支名的那个。
export function baseOfSession(s: PtySession): BaseDir {
  if (s.base_kind === 'home') {
    // 远端 home 与本机 home 必须分开：路径都叫「~」，但它们是两台机器上的两个目录
    if (s.machine !== '') {
      return { key: `~@${s.machine}`, kind: 'home', path: '~', label: `home@${s.machine}`, projectName: '', machine: s.machine }
    }
    return HOME_BASE
  }
  const name = s.base_path.split('/').filter(Boolean).pop() ?? s.base_path
  return { key: s.base_path, kind: 'workspace', path: s.base_path, label: name, projectName: '', machine: s.machine }
}

// usePtyRestore 在挂载时恢复一次终端会话。
//
// 参数：restore 为「把这个会话放进那个目录的 tab 组」的写入口
//（通常是 WorkbenchApi.restoreTerminal）
//
// 返回：error 为拉取失败的原文（空串 = 没出错）。**不吞**：拉不到列表意味着
// 用户会看到一个「终端都不见了」的界面，必须说清是为什么。
export function usePtyRestore(restore: (b: BaseDir, sessionId: string) => void): { error: string } {
  const [error, setError] = useState('')
  // ranRef 让它严格只跑一次：React 18 的 StrictMode 会把 effect 跑两遍，
  // 空依赖数组挡不住，而这里跑两遍就是两次跨机探活
  const ranRef = useRef(false)
  // restoreRef 让 effect 不必把 restore 列进依赖：调用方每次渲染都会传一个新
  // 函数引用，列进去就等于每次渲染都重新恢复一遍
  const restoreRef = useRef(restore)
  restoreRef.current = restore

  useEffect(() => {
    if (ranRef.current) return
    ranRef.current = true
    let cancelled = false
    fetchPtySessions('all')
      .then((resp) => {
        if (cancelled) return
        for (const s of resp.sessions) {
          // exit_code 出现 = 已退出。恢复一个死会话只会让人以为它还能用
          if (s.exit_code !== undefined && s.exit_code !== null) continue
          restoreRef.current(baseOfSession(s), s.id)
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [])

  return { error }
}
```

（`errorMessage` 就是 `ProjectTree` 用的那个 `../lib/format` 助手；若它的签名
不接受 `unknown`，用 `err instanceof Error ? err.message : String(err)`。）

- [ ] **Step 8: 跑测试确认通过**

```bash
cd web && npx vitest run src/app/workbench/usePtyRestore.test.ts
```

- [ ] **Step 9: 扩 `renderContent` 签名**

`WorkbenchPage.tsx` 的 props：

```typescript
  // renderContent 多收 group 与 tabId：终端 tab 建出会话之后要把 id 写回
  // **它自己**（setContent(group, tabId, …)），而中央区是唯一知道自己在哪一组、
  // 哪个 tab 的地方。
  renderContent: (content: TabContent, base: BaseDir, group: number, tabId: string) => ReactNode
```

调用处（`:115`）：

```tsx
                renderContent(activeTab.content, base, gi, activeTab.id)
```

`WorkbenchPage.test.tsx` 的 `describe('WorkbenchPage')` 里追加一条（`api` / `openTab`
/ `EMPTY_WORKBENCH` 都是该文件已有的导入与本地构造器）：

```tsx
  it('renderContent 拿得到自己所在的组号与 tab id', () => {
    const seen: Array<[number, string]> = []
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb })}
        onAddProject={vi.fn()}
        renderContent={(_c, _b, group, tabId) => {
          seen.push([group, tabId])
          return <div>内容</div>
        }}
      />,
    )
    expect(seen[0]).toEqual([0, id])
  })
```

- [ ] **Step 10: 接线 Shell**

`Shell.tsx`：

```tsx
  // 恢复服务端已有的终端会话（spec §6.1）。写入口用 restoreTerminal 而不是
  // openTerminal：它不会把用户的选中目录拽走
  const ptyRestore = usePtyRestore(wb.restoreTerminal)
```

`renderContent` 的 terminal 分支：

```tsx
                  renderContent={(c, base, group, tabId) => {
                    switch (c.kind) {
                      case 'terminal':
                        return (
                          <TerminalTab
                            base={base}
                            seq={c.seq}
                            sessionId={c.sessionId}
                            // 会话 id 必须写回这个 tab：不写回的话切一次 tab
                            // 就会再建一个会话，用户每切一次多留一个 shell
                            onSession={(id) => api.setContent(group, tabId, { ...c, sessionId: id })}
                          />
                        )
```

（`api` 即 `wb`；`setContent` 已在 `WorkbenchApi` 里。）

恢复失败的横幅接在左栏既有的两条横幅旁边：

```tsx
        {ptyRestore.error !== '' && (
          <DisconnectedBanner message={`终端会话恢复失败：${ptyRestore.error}`} compact />
        )}
```

Shell 一旦开始调 `fetchPtySessions`，`Shell.test.tsx` 里那份 `vi.mock('../../api/client')`
就漏了它——`...actual` 会把**真的**函数放行，测试环境里发出真实 fetch 然后炸在
未处理的 rejection 上。补进 mock 清单与 `beforeEach`：

```typescript
    fetchTaskDiff: vi.fn(),
    fetchPtySessions: vi.fn(),
```

```typescript
const { fetchTasks, fetchProjectTree, fetchWorkspaceDir, fetchTaskDetail, fetchTaskDiff, fetchPtySessions } =
  await import('../../api/client')
```

```typescript
  vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] })
```

- [ ] **Step 11: 全量回归**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint
```

- [ ] **Step 12: 加关键节点日志（自检）**

- [ ] 恢复失败 → `error` 上抛到 Shell 的横幅（**不是** `catch {}`）
- [ ] 恢复成功但一个会话都没有 → 界面就是「没有终端 tab」，这是正确的空态，
      不需要额外提示
- [ ] 已退出的会话被跳过时不报错：那是正常结论，不是异常
- [ ] 红线复核：不打印会话 id 之外的任何内容到 console

- [ ] **Step 13: 加意图注释（自检）**

确认：`usePtyRestore` 文件头的「为什么只拉一次」；`baseOfSession` 的 key 为何
必须与 `workspaceBase` 一致、label 退化为何无害；`ranRef` 为何存在（StrictMode）；
`restoreRef` 为何存在；`restoreTerminal` 为何不走 `mutate`。

- [ ] **Step 14: 提交**

```bash
git add web/src/app/ && git commit -m "feat(web): 加载时按服务端列表恢复终端会话，Shell 接线"
```

---

### Task 16: 关 tab 即删会话 + 能力降级门

**Files:**
- Create: `web/src/app/data/usePtySupport.ts`
- Modify: `web/src/app/workbench/BlankTab.tsx:29-36`、`:53-60`（`terminalUnavailable`）
- Modify: `web/src/app/workbench/WorkbenchPage.tsx`（透传 + `onBeforeClose`）
- Modify: `web/src/app/shell/Shell.tsx`（确认弹层、降级门、隐藏悬浮按钮）
- Test: `web/src/app/data/usePtySupport.test.ts`、`web/src/app/workbench/WorkbenchPage.test.tsx`（追加）

**Interfaces:**
- Consumes: Task 9 的 `deletePtySession` / `fetchPtySessions`；Task 12 的 `Machine.pty_supported`；
  Task 13 的 `PtySession.foreground`
- Produces:
  - `usePtySupport(): { supported: (machine: string) => boolean | null; error: string }`
  - `BlankTabProps.terminalUnavailable?: string`
  - `WorkbenchPageProps.onBeforeClose?: (c: TabContent, group: number, tabId: string) => boolean`
    （返回 `false` = 上层接管这次关闭）

**与 spec §6.2 的一处收紧（有意为之）**：spec 那一格写的是「点 × 且会话内还有
前台进程 → 先弹确认」。这里改成**只要是带会话的终端 tab 就弹**，前台判据只用来
加重措辞。理由：关闭即终止是不可逆操作，而「有没有前台进程」这个判据在用户点下
× 的那一瞬间可能刚好过期（命令是上一秒起的）。宁可多问一句，也不静默杀掉跑了
一晚上的 build——那正是本设计不做空闲回收的同一条理由。

- [ ] **Step 1: 写 `usePtySupport` 的失败测试**

`web/src/app/data/usePtySupport.test.ts`：

```typescript
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePtySupport } from './usePtySupport'

const fetchMachines = vi.fn()
vi.mock('../../api/client', () => ({ fetchMachines: () => fetchMachines() }))

beforeEach(() => vi.clearAllMocks())

describe('usePtySupport', () => {
  it('三态原样转达：true / false / 没上报', async () => {
    fetchMachines.mockResolvedValue({
      machines: [
        { name: '', pty_supported: true },
        { name: 'winbox', pty_supported: false },
        { name: 'oldbox' }, // 老 agentd：字段缺席
      ],
    })
    const { result } = renderHook(() => usePtySupport())
    await waitFor(() => expect(result.current.supported('')).toBe(true))
    expect(result.current.supported('winbox')).toBe(false)
    // 缺席必须是 null 而不是 false：老 agentd 很可能是支持的，只是没上报
    expect(result.current.supported('oldbox')).toBeNull()
  })

  it('还没拉到、或机器不在列表里，一律 null（不猜）', async () => {
    fetchMachines.mockResolvedValue({ machines: [] })
    const { result } = renderHook(() => usePtySupport())
    expect(result.current.supported('')).toBeNull()
    await waitFor(() => expect(fetchMachines).toHaveBeenCalled())
    expect(result.current.supported('ghost')).toBeNull()
  })

  it('拉取失败时能力全 null，且给出原文', async () => {
    fetchMachines.mockRejectedValue(new Error('连不上'))
    const { result } = renderHook(() => usePtySupport())
    await waitFor(() => expect(result.current.error).toContain('连不上'))
    expect(result.current.supported('')).toBeNull()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npx vitest run src/app/data/usePtySupport.test.ts
```

- [ ] **Step 3: 写实现**

`web/src/app/data/usePtySupport.ts`：

```typescript
// usePtySupport —— 每台机器的 PTY 能力位（spec §5.5）。
//
// 职责：加载时拉一次 GET /api/machines，把 pty_supported 整理成
// 「机器名 → true / false / null」的查询函数。
//
// 边界：
//   - 只读能力位，不管会话
//   - **不轮询**。能力位是平台属性，一台机器不会跑着跑着就不支持 PTY 了；
//     useMachines 那条「没人看的时候别打扰远程机」的纪律在这里同样成立，
//     所以只在加载时打一次
//
// 三态是这个 hook 存在的全部理由：一个 boolean 会把「老 agentd 没上报」
// 压成「不支持」，于是终端入口在一台其实能用的机器上凭空消失。
import { useEffect, useRef, useState } from 'react'
import { fetchMachines } from '../../api/client'
import { errorMessage } from '../lib/format'

export interface PtySupport {
  // supported 返回 null 表示**不知道**：没拉到、机器不在列表里、或对端没上报。
  // 调用方对 null 的正确反应是「照常放行，出了错再说实话」，不是「禁用」。
  supported: (machine: string) => boolean | null
  error: string
}

export function usePtySupport(): PtySupport {
  const [map, setMap] = useState<Record<string, boolean> | null>(null)
  const [error, setError] = useState('')
  const ranRef = useRef(false)

  useEffect(() => {
    if (ranRef.current) return
    ranRef.current = true
    let cancelled = false
    fetchMachines()
      .then((resp) => {
        if (cancelled) return
        const next: Record<string, boolean> = {}
        for (const m of resp.machines) {
          // 只收明确上报的：缺席/null 不进表，查询时自然落到 null
          if (typeof m.pty_supported === 'boolean') next[m.name] = m.pty_supported
        }
        setMap(next)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [])

  return {
    supported: (machine: string) => (map && machine in map ? map[machine] : null),
    error,
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
cd web && npx vitest run src/app/data/usePtySupport.test.ts
```

- [ ] **Step 5: BlankTab 收起不可用的终端项**

`BlankTab.tsx` 的 props 增：

```typescript
  // terminalUnavailable 非空 = 这台机器不能开终端，附带原因原文。
  // 此时**不渲染**终端项，改在面板底部说一句实话——置灰控件承诺「以后能用」，
  // 用户会反复点它（W3b §0 既有纪律）。
  terminalUnavailable?: string
```

`items` 那一行改成：

```typescript
  // home 基准只留终端（spec §2.6）；终端不可用时把它摘掉，两条过滤叠加
  const items = (base.kind === 'home' ? PICK_ITEMS.filter((i) => i.kind === 'terminal') : PICK_ITEMS)
    .filter((i) => i.kind !== 'terminal' || !terminalUnavailable)
```

`ul` 之后追加：

```tsx
      {terminalUnavailable && (
        <p className="max-w-xs text-center text-xs text-muted-foreground">{terminalUnavailable}</p>
      )}
```

`hotkeyOf` 那条既有的守卫（`if (!items.some(...)) return`）已经天然覆盖了
「⌘T 绕过隐藏项」，不用再加。

- [ ] **Step 6: WorkbenchPage 透传两件事**

props 增：

```typescript
  // terminalUnavailable：当前基准目录所在机器不能开终端时的原因原文
  terminalUnavailable?: string
  // onBeforeClose 返回 false = 这次关闭由上层接管（要先弹确认、先删服务端会话）。
  // 返回 true 或不提供 = 直接关。
  onBeforeClose?: (c: TabContent, group: number, tabId: string) => boolean
```

`pick` 里挡住终端（快捷键与空态入口都汇到这里）：

```typescript
    if (kind === 'terminal') {
      if (terminalUnavailable) return
      api.setContent(group, tabId, { kind: 'terminal', seq: nextTerminalSeq(wb) })
      return
    }
```

`startFromEmpty` 同款一行守卫。`TabBar` 的 `onClose` 改成：

```tsx
              onClose={(g, id) => {
                const tab = wb.groups[g]?.tabs.find((t) => t.id === id)
                if (tab && onBeforeClose && !onBeforeClose(tab.content, g, id)) return
                api.close(g, id)
              }}
```

两处 `BlankTab` 都加上 `terminalUnavailable={terminalUnavailable}`。

- [ ] **Step 7: 写 WorkbenchPage 的失败测试**

`WorkbenchPage.test.tsx` 的 `describe('WorkbenchPage')` 里追加三条。`api` /
`openTab` / `EMPTY_WORKBENCH` / `fireEvent` 都是该文件既有的；`base.label` 是
`'b2-b3'`，所以 seq=1 的终端 tab 标题是 `bash · b2-b3`，关闭按钮的 aria-label
按 `TabBar.tsx:38` 的 `` `关闭 ${title}` `` 拼出来：

```tsx
  it('终端不可用时选择面板不列终端项，改说一句实话', () => {
    render(
      <WorkbenchPage
        api={api()}
        onAddProject={vi.fn()}
        terminalUnavailable="这台机器的 agentd 运行在不支持 PTY 的平台上"
        renderContent={() => <div>内容</div>}
      />,
    )
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
    expect(screen.getByText(/不支持 PTY/)).toBeInTheDocument()
  })

  it('onBeforeClose 返回 false 时 tab 不关——上层要先删服务端会话', () => {
    const close = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1, sessionId: 's1' })
    render(
      <WorkbenchPage
        api={api({ wb, close })}
        onAddProject={vi.fn()}
        onBeforeClose={() => false}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '关闭 bash · b2-b3' }))
    expect(close).not.toHaveBeenCalled()
  })

  it('没挂 onBeforeClose 时照常直接关——拦截是加出来的，不是默认的', () => {
    const close = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1, sessionId: 's1' })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage api={api({ wb, close })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '关闭 bash · b2-b3' }))
    expect(close).toHaveBeenCalledWith(0, id)
  })
```

- [ ] **Step 8: 接线 Shell 的确认弹层与降级门**

`Shell.tsx` 增状态与两个回调：

```tsx
  const ptySupport = usePtySupport()
  // closingPty 记「哪个终端 tab 正在等确认」。会话 id 与所在位置都要留着：
  // 确认之后要先删会话、再关那个 tab
  const [closingPty, setClosingPty] = useState<{ group: number; tabId: string; sessionId: string } | null>(null)
  const [closeBusy, setCloseBusy] = useState(false)
  const [closeError, setCloseError] = useState('')
  // closingBusyProc：这个会话里是不是还有前台命令。null = 还没问出来
  const [closingBusyProc, setClosingBusyProc] = useState<boolean | null>(null)

  // ptyNote 把能力三态翻成一句给人看的话；空串 = 可用（或不知道，照常放行）
  const ptyNote = (machine: string): string => {
    if (ptySupport.supported(machine) === false) {
      return machine === ''
        ? '本机 agentd 运行在不支持 PTY 的平台上，终端不可用。'
        : `机器 ${machine} 的 agentd 运行在不支持 PTY 的平台上，终端不可用。`
    }
    // null 一律放行：老 agentd 没上报能力位，很可能是支持的。真不支持时
    // 建会话会返回 501，那句实话由 TerminalTab 显示
    return ''
  }

  // beforeCloseTab 拦下带会话的终端 tab：关它等于终止会话，必须先确认
  const beforeCloseTab = (c: TabContent, group: number, tabId: string): boolean => {
    if (c.kind !== 'terminal' || !c.sessionId) return true
    setClosingPty({ group, tabId, sessionId: c.sessionId })
    setCloseError('')
    setClosingBusyProc(null)
    // 问一句「它现在忙不忙」，只用于加重措辞，**不阻塞弹层出现**
    fetchPtySessions('all')
      .then((r) => setClosingBusyProc(r.sessions.some((s) => s.id === c.sessionId && s.foreground)))
      .catch(() => setClosingBusyProc(null))
    return false
  }

  const confirmClosePty = async () => {
    if (!closingPty) return
    setCloseBusy(true)
    setCloseError('')
    try {
      await deletePtySession(closingPty.sessionId, wb.base?.machine || undefined)
      wb.close(closingPty.group, closingPty.tabId)
      setClosingPty(null)
    } catch (err) {
      // 删失败**不关 tab**：关掉就等于把一个还活着的会话从视野里抹掉，
      // 而它仍在占着进程。原文照抄给用户
      setCloseError(errorMessage(err))
    } finally {
      setCloseBusy(false)
    }
  }
```

`WorkbenchPage` 的调用处加两个 prop：

```tsx
                  terminalUnavailable={wb.base ? ptyNote(wb.base.machine) : ''}
                  onBeforeClose={beforeCloseTab}
```

`renderContent` 的 terminal 分支加降级守卫（恢复出来的 tab 也走这里）：

```tsx
                      case 'terminal': {
                        const note = ptyNote(base.machine)
                        if (note !== '') {
                          return <p className="p-4 text-sm text-muted-foreground">{note}</p>
                        }
                        return (<TerminalTab … />)
                      }
```

悬浮按钮：

```tsx
      {/* 本机明确不支持时不渲染这个按钮：置灰控件承诺「以后能用」 */}
      {ptySupport.supported('') !== false && (
        <FloatingNewPane onNewTerminal={() => wb.openTerminal(HOME_BASE)} />
      )}
```

弹层（挂在既有两个弹层旁边）：

```tsx
      <ConfirmDialog
        open={closingPty !== null}
        title="关闭终端会话"
        description={
          '关闭会终止这个终端会话，里面正在运行的命令会被一并结束。\n' +
          '只是想切走的话直接切到别的 tab——会话会继续在后台跑。' +
          (closingBusyProc === true ? '\n\n⚠ 这个终端里现在还有命令在运行。' : '')
        }
        confirmLabel="关闭并终止"
        destructive
        busy={closeBusy}
        error={closeError}
        onConfirm={() => void confirmClosePty()}
        onCancel={() => setClosingPty(null)}
      />
```

Shell 这一步新添了两个真实网络调用（`usePtySupport` 里的 `fetchMachines`、
确认后的 `deletePtySession`），`Shell.test.tsx` 那份 `vi.mock('../../api/client')`
的 `...actual` 会把它们原样放行。补进 mock 清单与 `beforeEach`（Task 15 已补过
`fetchPtySessions`，这里接着加）：

```typescript
    fetchPtySessions: vi.fn(),
    fetchMachines: vi.fn(),
    deletePtySession: vi.fn(),
```

```typescript
const {
  fetchTasks, fetchProjectTree, fetchWorkspaceDir, fetchTaskDetail, fetchTaskDiff,
  fetchPtySessions, fetchMachines, deletePtySession,
} = await import('../../api/client')
```

```typescript
  // 本机上报支持 PTY：能力门在既有用例里必须是「放行」，否则一堆无关用例
  // 会因为终端项被收起而失败
  vi.mocked(fetchMachines).mockResolvedValue({
    machines: [{ name: '', addr: '', reachable: true, pty_supported: true }],
  })
  vi.mocked(deletePtySession).mockResolvedValue({ ok: true })
```

（`machines` 元素的其余字段按 `MachinesResp` 的实际必填项补齐——以 `web/src/api/types.ts`
里 `Machine` 的定义为准，缺字段 typecheck 会直接报出来。）

- [ ] **Step 9: 全量回归**

```bash
cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

- [ ] **Step 10: 加关键节点日志（自检）**

- [ ] 删会话失败 → `ConfirmDialog` 的 `error` 显示原文，**且 tab 不关**
- [ ] 能力位拉取失败 → `ptySupport.error` 有值；此时 `supported()` 全 null，
      终端照常放行（不因为探测失败就把功能关掉）
- [ ] 忙碌探测失败 → `closingBusyProc` 留 null，弹层照出，只是不加重措辞
      （这是**有意**的降级，注释已写明）
- [ ] 红线复核：确认弹层与横幅里不出现 token、cookie、会话内容

- [ ] **Step 11: 加意图注释（自检）**

确认：「与 spec §6.2 的一处收紧」写进 `beforeCloseTab` 的注释；`ptyNote` 为何
对 null 放行；删失败为何不关 tab；悬浮按钮为何是隐藏而不是置灰。

- [ ] **Step 12: 提交**

```bash
git add web/src/app/ && git commit -m "feat(web): 关终端 tab 先确认再删会话；pty_supported 三态降级"
```

---

### Task 17: 真机走查

**Files:**
- Create: `docs/superpowers/walkthroughs/2026-08-12-w4-pty-terminal.md`

**Interfaces:**
- Consumes: 全部前置任务
- Produces: 一份逐条带证据的走查记录

**这一步不是形式**：spec §4.2 那个 `SSH_AUTH_SOCK` 缺陷是**在开发者手起的
agentd 上测不出来的**——那台机器的环境里本来就有这个变量，单测与现实会一起绿。
本任务的第 3、8 两条是这一整轮里唯一能证伪它的地方。

- [ ] **Step 1: 起环境**

```bash
go build -o /tmp/handoff-pty . && /tmp/handoff-pty status
cd web && npm run build && cd ..
```

用**新构建的**二进制重启本机 agentd（走 SuperDev 或 `handoff service` 既有路径，
不要 shell 直接拉起），并确认 `handoff status` 里的 revision 就是当前提交。
devbox 同样换上新版（`handoff upgrade` 或既有部署路径）。

- [ ] **Step 2: 逐条走查并原样记录**

新建 `docs/superpowers/walkthroughs/2026-08-12-w4-pty-terminal.md`，按下表逐条填。
**每条都要贴证据**（命令 + 实际输出片段 / 截图路径 / 日志行），不要只打勾。

| # | 走查项（spec §9） | 判据 |
|---|---|---|
| 1 | 本机工作树开终端 | `stty size` 与窗口一致；中文输入正常（输入「你好」再退格，字符边界不错乱） |
| 2 | devbox 开终端 | 能交互；**且确认走反代**——本机 agentd 日志有 `/ws/pty` 转发行，浏览器只连了本机 |
| 3 | 终端里 `ssh -T git@github.com` 或 `git push --dry-run` | 成功。这是 §4.2 的验收 |
| 4 | 长任务 + 关页面 + 换窗口尺寸重开 | 会话自动恢复、输出连续；`truncated` 时清屏不重复 |
| 5 | 两个浏览器窗口接同一会话 | 输出双方都见；任一方可输入；尺寸取两者最小 |
| 6 | 点 `×` 关闭 | 确认弹层出现 → 确认后 `handoff footprint` 里该会话消失、进程归零 |
| 7 | `pathenv` 补全在终端里生效 | 终端里 `echo $PATH` 含 B71 补出来的目录（对照 agentd 自身的 PATH） |
| 8 | **托管形态**的 `env_forward` | 见 Step 3 |
| 9 | shell 自己 `exit` | 终端显示退出码；`GET /api/pty/sessions` 里该会话 `exit_code` 出现 |
| 10 | 切换左栏基准目录 | 会话**不死**（切回来输出连续），且没有 DELETE 请求 |
| 11 | `config.yaml` 未配 `env_forward` 时触发一次 Save | 文件里**仍然没有** `env_forward` 键，而 `SSH_AUTH_SOCK` 转发照常工作 |
| 12 | agentd 日志的三态结论 | 每个 `env_forward` 变量各有一行 inherited / resolved / unavailable |
| 13 | **home 基准**：右下悬浮按钮开终端（spec §11 第 2 条） | 终端起在 `$HOME`（`pwd` 是 home）；tab 标题是 `bash · home`；关它同样先弹确认 |
| 14 | 恢复出来的 tab 在 home 组里也认得出来 | 开一个 home 终端 → 刷新页面 → 它回来了，且没有多出第二个（`dedupKey` 生效） |

- [ ] **Step 3: 托管形态的 `env_forward` 验证（可能验不了，要如实记）**

理想做法：在 `handoff service install` 托管的 agentd 上开终端，`ssh -T` 成功。

拿不到停机窗口时（B71 的 V2 就是这个情况：launchd label 固定、无法与生产实例
并存），退而求其次用隔离最小环境复现：

```bash
env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="$HOME" /tmp/handoff-pty agentd --listen 127.0.0.1:7788 --data-dir /tmp/pty-walkthrough
```

然后向 7788 开一个终端，在里面 `echo $SSH_AUTH_SOCK` 与 `ssh -T git@github.com`。

**这条如果没能在真托管形态下验，就在走查记录里写「托管形态未验 + 原因」，
不打勾。** spec §9 明确要求如实记，不要用最小环境的绿灯冒充托管形态的绿灯。

- [ ] **Step 4: 逐条对照 spec §11 的 12 条验收标准**

在走查记录末尾附一张表，12 条各写「通过 / 未通过 / 未验（原因）」。

三条不能靠上面的走查表直接得出结论，按下面的口径写：

- **第 9、10 条（能力位降级门）**：手上没有 Windows agentd，也没有旧版对端，
  真机验不了。写「未验（无 `pty_supported=false` / `nil` 的对端）」，并指向
  Task 16 Step 1 的单测——那三条用例覆盖了 true / false / nil 三态的分支。
  **不要**为了打勾去手改 `/api/machines` 的响应然后声称验过了。
- **第 12 条**（W4 spec §2.6 的修正说明）是文档项：确认
  `docs/superpowers/specs/2026-08-12-w4-shell-calibration-design.md:179` 那条
  指向 PTY spec §1 的警告仍在，链接锚点仍能跳转。

- [ ] **Step 5: 把走查里发现的问题分流**

- 属于本轮范围、能当场修的 → 修掉，补测试，另起一个提交
- 属于本轮范围但要动设计的 → 记进 `docs/superpowers/backlog.md`（走
  `product-backlog` 的记录入口，别在这里直接改 spec）
- 明确属于「本轮不做」（spec §10）的 → 走查记录里写清「已知不做，见 §10」

- [ ] **Step 6: 提交**

```bash
git add docs/superpowers/walkthroughs/ && git commit -m "docs(walkthrough): W4 PTY 终端真机走查记录"
```

---

## 交付前总核对

全部任务完成后，逐项确认（用户 CLAUDE.md §5 的清单 + 本轮特有项）：

| 检查项 | 要求 |
|---|---|
| 完成目标 | spec §11 的 12 条验收标准逐条有结论（通过 / 未验 + 原因） |
| 架构一致 | `ptyhost` 不认识 HTTP、`agentd` 不认识 PTY 细节，边界没被越过 |
| 平台切分 | 新文件用 `_unix.go` / `_other.go`，**没有** `_windows.go`；`GOOS=windows go build ./...` 通过 |
| 配置纪律 | `config.yaml` 未配 `env_forward` 时，Save 后仍不含这个键 |
| 三态纪律 | `pty_supported` / `exit_code` / `Procs` 全是指针 + `omitempty`，nil 不被当成 false 或 0 |
| 契约 fixture | `go test ./internal/proto/` 与 `web` 侧 `contract.test.ts` 同时绿 |
| 文件头注释 | 每个新建文件顶部有职责与边界 |
| 方法注释 | 每个导出方法有参数、返回、注意事项 |
| 关键节点日志 | 每个 Go 包的错误分支带上下文与 cause；成功路径不静默；用 `s.log` 不用 `fmt.Printf` |
| 红线复核 | 日志里没有主令牌、ticket 明文、cookie 明文、`Env` 内容、按键与 PTY 输出字节 |
| 无跨层调用 | 前端 REST 只经 `api/client.ts`；组件不直接 `fetch` |
| 优先复用 | WS 反代复用 `forwardTo` 的地址解析与防环头；fan-out 复用 `projectfanout` 的形状 |
| `go vet` | `go vet ./...` 干净 |

