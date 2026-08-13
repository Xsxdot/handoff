# B75 游标可持久化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让审核者侧游标在 `$HOME` 不可写的沙箱里仍能持久化，按 agentd 分篓不再串，任务归档后被回收。

**Architecture:** 新增 `internal/client/cursordir.go`（游标根解析 + 两级降级 + 命名空间折算）与 `internal/client/cursorgc.go`（回收）。`client.go` 里的 `cursorPath` / `readCursor` / `writeCursor` 改为调用前者，`sweepStaleCursorTemps` 迁入后者。布局从 `~/.handoff/cursor-<taskID>` 变为 `<根>/cursors/<agentd>/<taskID>`。

**Tech Stack:** Go 1.26，标准库 `os` / `path/filepath` / `net/url` / `sync`，日志用 `c.log()`（`internal/logx` 的 slog，写 stderr），测试用标准 `testing` + `t.Setenv` / `t.TempDir`。

## Global Constraints

- 依据 spec：`docs/superpowers/specs/2026-08-12-false-completion-and-cursor-durability-design.md` §4。
- **不新增任何 flag 或环境变量**（spec §4.2「不做的事」）：不加 `--cursor-dir`、不加 `HANDOFF_CURSOR_DIR`。
- **不做软链**（spec §2.4 已排除）。
- **不改 `client.New` / `NewWithWSTiming` 的签名**：命名空间由已有的 `baseURL` 推出（spec §4.1）。
- 游标根**只解析一次并缓存**（spec §4.3 末条），降级提示只打印一次。
- 日志一律用 `c.log()`（slog），**禁止 `fmt.Printf` / `println`**。
- 全部注释与日志文案用中文，遵循用户 CLAUDE.md §2：新文件必须有「职责 / 边界」头注释，导出函数必须有 doc 注释，非显然分支必须有「为什么」注释。
- 每个 task 结束时 `gofmt -l .` 无输出、`go vet ./...` 无输出。

---

### Task 1: 游标根解析与 agentd 命名空间

**Files:**
- Create: `internal/client/cursordir.go`
- Create: `internal/client/cursordir_test.go`
- Modify: `internal/client/client.go:157-166`（`Client` 结构体加缓存字段并订正并发安全注释）

**Interfaces:**
- Consumes: 无（本 task 是地基）
- Produces:
  - `func cursorNamespace(baseURL string) string` —— 把 agentd 地址折成路径段
  - `func (c *Client) cursorRootDir() (string, error)` —— 返回**已确认可写**的游标根目录（形如 `<root>/cursors`），全 Client 生命周期只解析一次
  - 常量 `cursorDirName = "cursors"`

- [ ] **Step 1: 写失败的测试**

创建 `internal/client/cursordir_test.go`：

```go
package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorNamespaceFoldsAddressForms(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:7777", "127.0.0.1_7777"},
		{"http://100.73.238.21:7777", "100.73.238.21_7777"},
		{"https://box.example.com:8443", "box.example.com_8443"},
		{"127.0.0.1:7777", "127.0.0.1_7777"}, // 无 scheme 也要折到同一个篓
		{"", "unknown"},
	}
	for _, c := range cases {
		if got := cursorNamespace(c.in); got != c.want {
			t.Fatalf("cursorNamespace(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCursorRootPrefersHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	got, err := c.cursorRootDir()
	if err != nil {
		t.Fatalf("cursorRootDir: %v", err)
	}
	want := filepath.Join(home, ".handoff", "cursors")
	if got != want {
		t.Fatalf("根 = %q, want %q", got, want)
	}
}

func TestCursorRootFallsBackToCwdWhenHomeUnwritable(t *testing.T) {
	home := t.TempDir()
	// 造一个不可写的 ~/.handoff：先建目录再摘掉写权限，
	// 这样 MkdirAll 成功而 CreateTemp 失败——正是沙箱里的形状
	if err := os.MkdirAll(filepath.Join(home, ".handoff"), 0o500); err != nil {
		t.Fatal(err)
	}
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

func TestCursorRootResolvesOnlyOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	first, err := c.cursorRootDir()
	if err != nil {
		t.Fatal(err)
	}
	// 解析后把 HOME 换掉：缓存生效的话第二次必须仍返回第一次的值
	t.Setenv("HOME", t.TempDir())
	second, err := c.cursorRootDir()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("游标根被重复解析：first=%q second=%q", first, second)
	}
}

func TestCursorRootErrorNamesBothPaths(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".handoff"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".handoff"), 0o500); err != nil {
		t.Fatal(err)
	}
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

- [ ] **Step 2: 运行测试，确认它以「未定义」失败**

Run: `go test ./internal/client/ -run 'TestCursor' -v`
Expected: 编译失败，`undefined: cursorNamespace`、`c.cursorRootDir undefined`

- [ ] **Step 3: 写实现**

创建 `internal/client/cursordir.go`：

```go
// cursordir.go —— 审核者侧游标目录的解析、降级与命名空间折算。
//
// 职责：
//   - 解析游标根：~/.handoff → <cwd>/.handoff 两级确定性降级，都不可写则报错
//   - 把 agentd 地址折算成可作路径段的命名空间名
//
// 边界：
//   - 不读写游标内容（那是 client.go 的 readCursor/writeCursor）
//   - 不做回收（那是 cursorgc.go）
//   - 不认识 --target：命名空间按 agentd 地址而非本机别名，见 cursorNamespace 的 why
package client

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// cursorDirName 是游标在游标根下的子目录名。
//
// 为什么必须有这一层而不是平铺：平铺时游标与 config.yaml、agentd 的 DataDir
// 混在同一层，没有任何一个目录可以被整体处置——既没法「清掉某台机器的全部
// 游标」，也没法把游标单独重定向走。
const cursorDirName = "cursors"

// cursorNamespace 把 agentd 的 baseURL 折算成一个可作路径段的名字。
//
// 参数：baseURL 为 Client 持有的 agentd 地址，可带或不带 scheme
//
// 返回：只含字母数字与 . - 的路径段（如 "100.73.238.21_7777"）；无法解析时返回 "unknown"
//
// 为什么按地址而不是 --target 名字：地址是 agentd 的身份，名字只是本机别名。
// 两个 target 名指向同一台 agentd 时按名字分篓会把同一批任务的游标分裂成两份；
// 改个名字则让已有游标全部失联。这与 resolveProject 里「projectID 是身份、
// 名字只是引用」是同一个判断。
func cursorNamespace(baseURL string) string {
	host := baseURL
	// 不带 scheme 时 url.Parse 会把整串当 Path、Host 为空，此时退回原串按同样
	// 规则折算——两种写法必须折到同一个篓，否则 handoff --agentd 127.0.0.1:7777
	// 与 http://127.0.0.1:7777 会各持一份游标
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// probeCursorDirWritable 通过「真写一次」确认目录可写。
//
// 为什么不查权限位：沙箱（codex 的 seatbelt/landlock）的拒绝不体现在 mode 上，
// 目录 mode 是 0700 而写入照样 EPERM。唯一可靠的判据是真的建一个文件。
func probeCursorDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录: %w", err)
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return fmt.Errorf("试写: %w", err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name) // 探针文件用完即删，失败无所谓：下一次 TTL 清扫会带走
	return nil
}

// cursorRootDir 返回已确认可写的游标根目录（形如 <root>/cursors）。
//
// 返回：
//   - 目录绝对路径；两级候选都不可写时返回错误，错误里点名两个路径与各自原因
//
// 注意：
//   - 全 Client 生命周期只解析一次，结果（含错误）被缓存
//   - 降级发生时打一条 Warn（stderr），只打一次
func (c *Client) cursorRootDir() (string, error) {
	c.cursorRootOnce.Do(func() {
		c.cursorRoot, c.cursorRootErr = c.resolveCursorRoot()
	})
	return c.cursorRoot, c.cursorRootErr
}

// resolveCursorRoot 执行两级确定性降级。
//
// 顺序硬约束：先 ~/.handoff（缺省，与历史行为一致），不可写才退 <cwd>/.handoff。
// 为什么降级目标是 cwd 而不是 $TMPDIR：codex 的 workspace-write 可写 cwd、
// $TMPDIR、/tmp 三处，但只有 cwd 是审核者的项目目录、跨 session 稳定；
// $TMPDIR 会被清理，游标续不上等于没修。
func (c *Client) resolveCursorRoot() (string, error) {
	var homeReason string
	home, err := os.UserHomeDir()
	if err != nil {
		homeReason = fmt.Sprintf("读取用户主目录失败: %v", err)
	} else {
		cand := filepath.Join(home, ".handoff", cursorDirName)
		if perr := probeCursorDirWritable(cand); perr == nil {
			c.log().Debug("游标根就位", "dir", cand)
			return cand, nil
		} else {
			homeReason = fmt.Sprintf("%s 不可写: %v", filepath.Dir(cand), perr)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("游标目录不可用：%s；且读取当前目录失败: %w", homeReason, err)
	}
	cand := filepath.Join(cwd, ".handoff", cursorDirName)
	if perr := probeCursorDirWritable(cand); perr != nil {
		return "", fmt.Errorf("游标目录不可用：%s；%s 也不可写: %v",
			homeReason, filepath.Dir(cand), perr)
	}
	// 降级是审核者必须知道的事实（游标换了地方，跨目录 wait 会各持一份），
	// 因此是 Warn 不是 Debug；只打一次由 cursorRootOnce 保证
	c.log().Warn("游标目录不可写，已降级", "原因", homeReason, "改用", cand)
	return cand, nil
}
```

修改 `internal/client/client.go` 的 `Client` 结构体（当前 157-166 行），加三个缓存字段并订正并发安全注释：

```go
// 并发安全：baseURL/token/hc 与 WS 节奏字段构造后只读；游标根由
// cursorRootOnce 保护，首次调用解析、后续读缓存，可被多个 goroutine 同时使用。
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
	// WS 断线重连的退避区间与「这次连接算健康」的存活门槛（见 WaitEvent）。
	// 测试经 NewWithWSTiming 注入毫秒级值，生产一律用包级默认。
	wsInitialBackoff time.Duration
	wsMaxBackoff     time.Duration
	wsStableAfter    time.Duration
	// 游标根解析结果的缓存（见 cursordir.go）。缓存错误与缓存成功同等重要：
	// 不缓存错误的话，两处都不可写时每写一次游标都要重跑两次文件系统探测。
	cursorRootOnce sync.Once
	cursorRoot     string
	cursorRootErr  error
}
```

`internal/client/client.go` 的 import 增加 `"sync"`（若尚未导入）。

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/client/ -run 'TestCursor' -v`
Expected: 五条全部 PASS

- [ ] **Step 5: 加关键节点日志**

本 task 的日志点已内联在 Step 3，逐条核对：

- 游标根解析成功（缺省路径）：`c.log().Debug("游标根就位", "dir", cand)` —— Debug 级，因为它是每次运行都发生的常态。
- **降级发生**：`c.log().Warn("游标目录不可写，已降级", "原因", ..., "改用", ...)` —— Warn 级写 stderr，审核者必须看到；由 `cursorRootOnce` 保证只打一次。
- 两级都失败：不打日志，直接把两个路径与各自原因编进 `error` 返回给调用方——错误会一路冒到 CLI 顶层展示，再打一条日志是重复。

确认无 `fmt.Printf` / `println` 作为日志手段（`fmt.Sprintf`/`fmt.Errorf` 构造字符串不算）。

- [ ] **Step 6: 加注释**

- `cursordir.go` 文件头：职责 + 边界（已在 Step 3 写入，核对「不做回收」「不认识 --target」两条边界在）。
- 导出/包内函数 doc 注释：`cursorNamespace`（参数/返回/为什么按地址）、`cursorRootDir`（返回/只解析一次）、`resolveCursorRoot`（顺序硬约束 + 为什么 cwd 不是 $TMPDIR）、`probeCursorDirWritable`（为什么不查权限位）、常量 `cursorDirName`（为什么必须有这一层）。
- `Client` 结构体新字段的「为什么缓存错误」注释。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/client/ -count=1
git add internal/client/cursordir.go internal/client/cursordir_test.go internal/client/client.go
git commit -m "feat(client): 游标根两级确定性降级与 agentd 命名空间折算

~/.handoff 不可写时降级到 <cwd>/.handoff，两处都不可写则响亮报错并点名
两个路径。可写性用「真写一次」判定而非查权限位——沙箱的拒绝不体现在 mode 上。
命名空间按 agentd 地址而非 --target 名字：地址是身份、名字只是本机别名。
根只解析一次并缓存，降级告警只打一次。"
```

---

### Task 2: 新布局接线与读失败分类

**Files:**
- Modify: `internal/client/client.go:1138-1169`（`cursorPath` / `readCursor`）
- Modify: `internal/client/client.go:1182-1211`（`writeCursor`）
- Create: `internal/client/cursor_layout_test.go`

**Interfaces:**
- Consumes: `cursorNamespace`、`(*Client).cursorRootDir`、`cursorDirName`（Task 1）
- Produces:
  - `func (c *Client) cursorPath(taskID string) (string, error)` —— **由包级函数改为方法**（需要 `baseURL` 与缓存的根），返回 `<根>/<namespace>/<taskID>`
  - `readCursor` / `writeCursor` 签名不变

- [ ] **Step 1: 写失败的测试**

创建 `internal/client/cursor_layout_test.go`：

```go
package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorPathUsesNamespacedLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://100.73.238.21:7777", "")
	p, err := c.cursorPath("task-abc")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".handoff", "cursors", "100.73.238.21_7777", "task-abc")
	if p != want {
		t.Fatalf("游标路径 = %q, want %q", p, want)
	}
}

func TestCursorsOfDifferentAgentdDoNotCollide(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := New("http://127.0.0.1:7777", "")
	b := New("http://100.73.238.21:7777", "")

	if err := a.writeCursor("same-id", 11); err != nil {
		t.Fatal(err)
	}
	if err := b.writeCursor("same-id", 22); err != nil {
		t.Fatal(err)
	}
	if got := a.readCursor("same-id"); got != 11 {
		t.Fatalf("本机游标被另一台 agentd 覆盖：got %d want 11", got)
	}
	if got := b.readCursor("same-id"); got != 22 {
		t.Fatalf("远端游标读错：got %d want 22", got)
	}
}

func TestReadCursorMissingFileIsSilentFirstRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	if got := c.readCursor("never-seen"); got != 0 {
		t.Fatalf("首次必须从 0 开始，got %d", got)
	}
}

func TestReadCursorCorruptContentIsReported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	p, err := c.cursorPath("corrupt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not-a-number"), 0o600); err != nil {
		t.Fatal(err)
	}
	seq, reported := c.readCursorWithDiag("corrupt")
	if seq != 0 {
		t.Fatalf("损坏内容必须退回 0，got %d", seq)
	}
	if !reported {
		t.Fatal("内容损坏必须被报告，不得与「文件不存在」一样静默")
	}
}

func TestReadCursorPermissionDeniedIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 无视权限位，本用例无意义")
	}
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
	if err := os.WriteFile(p, []byte("42"), 0o000); err != nil {
		t.Fatal(err)
	}
	seq, reported := c.readCursorWithDiag("denied")
	if seq != 0 {
		t.Fatalf("读不了必须退回 0，got %d", seq)
	}
	if !reported {
		t.Fatal("权限被拒必须被报告，不得与「文件不存在」一样静默")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/client/ -run 'TestCursorPath|TestCursorsOf|TestReadCursor' -v`
Expected: 编译失败（`c.cursorPath` 是包级函数不是方法、`readCursorWithDiag` 未定义），以及布局断言不符

- [ ] **Step 3: 写实现**

替换 `internal/client/client.go` 当前 1138-1169 行：

```go
// cursorPath 返回任务 cursor 文件路径（<游标根>/<agentd 命名空间>/<taskID>）。
//
// 为什么放用户主目录而非配置 DataDir：cursor 是审核者侧的本地状态，
// 与配置/数据库文件位置解耦；即使 DataDir 被移动，审核者已看过的进度也不重投。
// 该决策保留，cursordir.go 只是让这个根在不可写时可以降级。
//
// 为什么要 agentd 这一层：文件名只按 taskID 时，两台 agentd 上碰巧同 ID 的
// 任务会共用一个游标文件，互相把对方的进度顶掉。
func (c *Client) cursorPath(taskID string) (string, error) {
	root, err := c.cursorRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, cursorNamespace(c.baseURL), taskID), nil
}

// readCursor 读取任务 cursor；任何读不出来的情形都返回 0（从头开始）。
func (c *Client) readCursor(taskID string) int64 {
	seq, _ := c.readCursorWithDiag(taskID)
	return seq
}

// readCursorWithDiag 是 readCursor 的可诊断变体。
//
// 返回：
//   - seq: 游标值；读不出来时为 0
//   - reported: 是否属于「游标存在但用不了」并已告警（供测试断言，生产不用）
//
// 为什么要把「文件不存在」与其它错误分开：文件不存在是每个任务第一次 wait 的
// 常态，报它等于每次都喊狼来了；而权限被拒与内容损坏意味着游标存在却用不了，
// 后果是静默从 0 重放全部历史事件——审核者会看到一串早就处理过的旧事件，
// 却没有任何一条信息指向真正的原因。这是 B75 现场的成因。
func (c *Client) readCursorWithDiag(taskID string) (seq int64, reported bool) {
	p, err := c.cursorPath(taskID)
	if err != nil {
		c.log().Warn("游标路径不可用，本次从头开始", "task", taskID, "cause", err)
		return 0, true
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			c.log().Debug("cursor 文件不存在，从头开始", "task", taskID, "path", p)
			return 0, false
		}
		c.log().Warn("cursor 存在但读不了，本次将从头重放事件",
			"task", taskID, "path", p, "cause", err)
		return 0, true
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		c.log().Warn("cursor 内容损坏，本次将从头重放事件",
			"task", taskID, "path", p, "content", turnTailForLog(string(b)))
		return 0, true
	}
	c.log().Debug("cursor 读取", "task", taskID, "path", p, "seq", n)
	return n, false
}

// turnTailForLog 把可能很长的损坏内容截到可入日志的长度。
//
// 为什么截断：损坏的 cursor 文件可能是任意内容（磁盘故障写进了别的东西），
// 原样入日志会把一行日志撑成几 MB。
func turnTailForLog(s string) string {
	const max = 64
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
```

替换 `writeCursor` 内的 `cursorPath(taskID)` 调用为 `c.cursorPath(taskID)`，并把临时文件前缀由 `"cursor-"+taskID+"-*.tmp"` 改为 `filepath.Base(p)+"-*.tmp"`（新布局下文件名就是 taskID，前缀保持与文件同名便于人工辨认）：

```go
	p, err := c.cursorPath(taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("创建 cursor 目录: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(p), taskID+"-*.tmp")
```

同步把 `sweepStaleCursorTemps` 的 glob 由 `"cursor-"+taskID+"-*.tmp"` 改为 `taskID+"-*.tmp"`。

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/client/ -count=1`
Expected: 全部 PASS（含既有的 `backlog_internal_test.go` 等——它们已用 `t.Setenv("HOME", ...)`，新布局对它们透明）

- [ ] **Step 5: 加关键节点日志**

- 游标路径不可用（根解析失败）：Warn，带 task 与 cause。
- **文件不存在**：Debug（正常首次，不许升级成 Warn，否则每个新任务喊一次）。
- **存在但读不了**（权限/IO）：Warn，文案必须写出后果「本次将从头重放事件」——只说「读失败」不足以让审核者把它和眼前的重复事件联系起来。
- **内容损坏**：Warn，带截断后的内容片段作为现场。
- 读成功：Debug 带 seq（成功路径不静默）。
- 写成功：沿用既有 Debug 带 seq；写失败由三个调用点既有的 Warn 负责（`WaitEvent` / `FollowEvents` / 对账 fast-forward，不改）。

- [ ] **Step 6: 加注释**

- `cursorPath`：保留原有「为什么不跟 DataDir 走」的 why，新增「为什么要 agentd 这一层」。
- `readCursorWithDiag`：参数/返回/**为什么把文件不存在与其它错误分开**（这是本 task 的核心判断，必须写清后果）。
- `turnTailForLog`：为什么截断。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/client/ -count=1
git add internal/client/client.go internal/client/cursor_layout_test.go
git commit -m "feat(client): 游标改按 agentd 分篓，读失败不再一律静默

布局 <根>/cursors/<agentd>/<taskID>，两台 agentd 上同 ID 的任务不再互相顶掉
进度。readCursor 拆出可诊断变体：文件不存在仍是静默的正常首次，权限被拒与
内容损坏改为 Warn 并写明后果「本次将从头重放事件」——这正是 B75 现场里
审核者看到一串重复事件却找不到原因的成因。"
```

---

### Task 3: 游标回收

**Files:**
- Create: `internal/client/cursorgc.go`
- Create: `internal/client/cursorgc_test.go`
- Modify: `internal/client/client.go:1213-1244`（`cursorTempTTL` / `sweepStaleCursorTemps` 迁出）
- Modify: `internal/client/client.go:790-800`（`WaitEvent` 交付事件后按类型回收）
- Modify: `internal/client/client.go:1074-1085`（`FollowEvents` 同上）
- Modify: `cmd/done.go:45`（`done` 成功后兜底回收）

**Interfaces:**
- Consumes: `(*Client).cursorPath`、`(*Client).cursorRootDir`、`cursorNamespace`（Task 1、2）
- Produces:
  - `func (c *Client) DropCursor(taskID string)` —— 幂等删除某任务游标，导出供 `cmd/done.go` 调用
  - `func (c *Client) sweepCursors()` —— TTL 清扫（游标文件与遗留 .tmp 一并）
  - `func (c *Client) purgeLegacyFlatCursors()` —— 一次性清除旧平铺 `cursor-*`
  - 常量 `cursorTTL = 30 * 24 * time.Hour`

- [ ] **Step 1: 写失败的测试**

创建 `internal/client/cursorgc_test.go`：

```go
package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDropCursorIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	if err := c.writeCursor("t1", 5); err != nil {
		t.Fatal(err)
	}
	c.DropCursor("t1")
	c.DropCursor("t1") // 第二次不得 panic、不得报错
	if got := c.readCursor("t1"); got != 0 {
		t.Fatalf("游标应已删除，got %d", got)
	}
}

func TestSweepCursorsRemovesOnlyExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c := New("http://127.0.0.1:7777", "")
	if err := c.writeCursor("fresh", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.writeCursor("stale", 2); err != nil {
		t.Fatal(err)
	}
	stalePath, err := c.cursorPath("stale")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-cursorTTL - time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatal(err)
	}

	c.sweepCursors()

	if got := c.readCursor("fresh"); got != 1 {
		t.Fatalf("未超期游标被误删，got %d", got)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("超期游标未被清掉: %v", err)
	}
}

func TestPurgeLegacyFlatCursors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyDir := filepath.Join(home, ".handoff")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "cursor-old-task")
	if err := os.WriteFile(legacy, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyTmp := filepath.Join(legacyDir, "cursor-old-task-123.tmp")
	if err := os.WriteFile(legacyTmp, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(legacyDir, "config.yaml")
	if err := os.WriteFile(keep, []byte("listen: x"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New("http://127.0.0.1:7777", "")
	c.purgeLegacyFlatCursors()

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("旧平铺游标未被清除")
	}
	if _, err := os.Stat(legacyTmp); !os.IsNotExist(err) {
		t.Fatal("旧平铺临时文件未被清除")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("非游标文件被误删: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/client/ -run 'TestDropCursor|TestSweepCursors|TestPurgeLegacy' -v`
Expected: 编译失败，`DropCursor` / `sweepCursors` / `purgeLegacyFlatCursors` / `cursorTTL` 未定义

- [ ] **Step 3: 写实现**

创建 `internal/client/cursorgc.go`（并把 `cursorTempTTL` 与 `sweepStaleCursorTemps` 从 `client.go` 整段迁入本文件，`client.go` 对应段落删除）：

```go
// cursorgc.go —— 审核者侧游标的回收。
//
// 职责：
//   - 任务归档时删掉它的游标（DropCursor）
//   - 按 TTL 清扫超期游标与遗留的写入临时文件（sweepCursors）
//   - 一次性清除旧平铺布局遗留的 cursor-* 文件（purgeLegacyFlatCursors）
//
// 边界：
//   - 不判断任务是否真的终结：那是调用方（观察到 archived 事件 / done 成功）的事
//   - 不解析游标根：复用 cursordir.go 的 cursorRootDir
//   - 全部回收动作都是尽力而为，失败只记 Debug，绝不影响游标读写的成败
package client

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cursorTTL 是游标文件被判定为「无人认领」的年龄阈值。
//
// 为什么需要它而不是只靠 DropCursor：DropCursor 只覆盖「审核者跑完 done」这条
// 主路径。任务被 stop、审核者换了机器、wait 进程被 Ctrl+C——这些情形下没有任何
// 代码会再碰那个文件。实测审核者本机曾堆积 98 个从无回收的游标。
//
// 为什么是 30 天而不是更短：游标的作用是跨 wait 调用记住看到哪儿了，而一个
// 任务从派发到归档可能横跨数日。30 天足够长到不会误删在办任务，也足够短到
// 目录不会无界增长。
const cursorTTL = 30 * 24 * time.Hour

// cursorTempTTL 是 cursor 临时文件被判定为「遗留垃圾」的年龄阈值。
//
// 为什么按年龄而不是一律清空：同一任务可能有并发的 wait 进程正在写各自的
// 临时文件，无差别删除会掐掉别人在途的 Rename。而任何一次正常写入都在毫秒级
// 完成，1 小时的阈值把「在途」与「遗留」分得足够开。
const cursorTempTTL = time.Hour

// DropCursor 删除某任务的游标，幂等。
//
// 参数：taskID 为已终结（归档）的任务 ID
//
// 注意：
//   - 文件不存在不是错误：本函数有两条调用通道（观察到 archived 事件、done 成功
//     返回），两条都可能先到，必须能重复调用
//   - 任何失败只记 Debug：回收是卫生工作，失败不影响任何正确性
func (c *Client) DropCursor(taskID string) {
	p, err := c.cursorPath(taskID)
	if err != nil {
		c.log().Debug("回收游标时路径不可用", "task", taskID, "cause", err)
		return
	}
	if err := os.Remove(p); err != nil {
		if !os.IsNotExist(err) {
			c.log().Debug("回收游标失败", "task", taskID, "path", p, "cause", err)
		}
		return
	}
	c.log().Debug("任务已归档，游标已回收", "task", taskID, "path", p)
}

// sweepCursors 清扫本 agentd 命名空间下超期的游标与遗留临时文件。
//
// 注意：只扫自己这一篓，不碰别的 agentd 的目录——判断别人的文件是否超期需要
// 别人的上下文，本客户端没有。
func (c *Client) sweepCursors() {
	root, err := c.cursorRootDir()
	if err != nil {
		return
	}
	dir := filepath.Join(root, cursorNamespace(c.baseURL))
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.log().Debug("扫描游标目录失败", "dir", dir, "cause", err)
		return
	}
	var removed int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		ttl := cursorTTL
		if strings.HasSuffix(e.Name(), ".tmp") {
			ttl = cursorTempTTL
		}
		if time.Since(fi.ModTime()) < ttl {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if rerr := os.Remove(p); rerr != nil {
			c.log().Debug("清理超期游标失败", "path", p, "cause", rerr)
			continue
		}
		removed++
	}
	if removed > 0 {
		c.log().Debug("已清理超期游标", "dir", dir, "n", removed)
	}
}

// purgeLegacyFlatCursors 一次性清除旧平铺布局遗留的 cursor-* 文件。
//
// 为什么删而不迁移：旧文件里绝大多数是已归档任务的游标，本来就该删；保住它们
// 唯一的收益是极少数仍在 waiting_review 的老任务下次 wait 少重放一次历史事件，
// 不值得为此写一段只跑一次的迁移代码及其测试。
//
// 只删严格匹配 cursor-* 的文件，config.yaml / agentd.log / skill 等一律不碰。
func (c *Client) purgeLegacyFlatCursors() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	matches, err := filepath.Glob(filepath.Join(home, ".handoff", "cursor-*"))
	if err != nil {
		c.log().Debug("扫描旧平铺游标失败", "cause", err)
		return
	}
	var removed int
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || fi.IsDir() {
			continue // 目录不碰：cursors/ 这一层就是目录，绝不能被当成旧文件删掉
		}
		if rerr := os.Remove(m); rerr != nil {
			c.log().Debug("清除旧平铺游标失败", "path", m, "cause", rerr)
			continue
		}
		removed++
	}
	if removed > 0 {
		c.log().Info("已清除旧布局遗留的游标文件", "n", removed)
	}
}
```

在 `resolveCursorRoot` 成功返回**缺省路径**（`~/.handoff/cursors`）时，追加一次 `go c.purgeLegacyFlatCursors()`？**不要**——改为在 `cursorRootDir` 的 `once.Do` 内同步调用一次，理由写进注释：并发清理会与测试的临时 HOME 竞态，而这次清理只发生一次、耗时是一次 Glob + 少量 Remove。在 `cursordir.go` 的 `cursorRootDir` 中改为：

```go
func (c *Client) cursorRootDir() (string, error) {
	c.cursorRootOnce.Do(func() {
		c.cursorRoot, c.cursorRootErr = c.resolveCursorRoot()
		if c.cursorRootErr == nil {
			// 旧平铺布局的一次性清除挂在这里：它必须只跑一次，而 once 已经
			// 提供了这个保证；单独找一个「启动时」的挂载点反而要在每个命令里
			// 各接一次，漏一个就永远不清
			c.purgeLegacyFlatCursors()
			c.sweepCursors()
		}
	})
	return c.cursorRoot, c.cursorRootErr
}
```

在 `WaitEvent` 交付事件处（当前 client.go:797 附近）追加归档回收：

```go
		if err == nil {
			if werr := c.writeCursor(taskID, ev.Seq); werr != nil {
				// cursor 写失败不吞事件：先把事件交还用户（宁可下次重投，不可这次挂住）
				c.log().Warn("cursor 写盘失败", "task", taskID, "seq", ev.Seq, "cause", werr)
			}
			// 任务归档后游标再无用处：立刻回收，不等 TTL。放在返回前而非
			// 调用方，是因为两个消费端（wait / follow）都要这个行为
			if ev.Type == proto.EventTypeArchived {
				c.DropCursor(taskID)
			}
			return ev, nil
		}
```

在 `FollowEvents` 的事件交付处（当前 client.go:1078 附近）追加同样两行。

修改 `cmd/done.go:45` 附近，`Done` 成功后兜底回收：

```go
		cli := client.New(addr, token)
		noteSaved, err := cli.Done(cmd.Context(), taskID, doneNote)
		if err != nil {
			return err
		}
		// 兜底回收：审核者可能从不跑 wait/follow（直接 dispatch → done），
		// 那条通道就观察不到 archived 事件。两条通道幂等，先到者生效
		cli.DropCursor(taskID)
```

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/client/ ./cmd/ -count=1`
Expected: 全部 PASS

- [ ] **Step 5: 加关键节点日志**

- `DropCursor` 成功：Debug 带 task 与 path（成功路径不静默）。文件不存在**不记**——那是幂等的正常情形，记了会在两条通道都触发时每次刷一条。
- `DropCursor` 其它失败：Debug 带 cause。
- `sweepCursors` 清掉了东西：Debug 带数量（`removed > 0` 才记，否则每次运行都刷一条空信息）。
- `purgeLegacyFlatCursors` 清掉了东西：**Info** 带数量——这是一次性、不可逆的删除，审核者有权知道；同样只在 `removed > 0` 时记。
- 全部回收失败一律 Debug，且绝不向上返回错误（文件头边界已声明）。

- [ ] **Step 6: 加注释**

- `cursorgc.go` 文件头：职责三条 + 边界三条。
- `cursorTTL`：为什么需要它而不是只靠 `DropCursor`；为什么是 30 天。
- `DropCursor`：参数 + 为什么必须幂等（两条调用通道）+ 失败只记 Debug。
- `sweepCursors`：为什么只扫自己这一篓。
- `purgeLegacyFlatCursors`：**为什么删而不迁移** + 只匹配 `cursor-*` 且跳过目录的理由。
- `cursorRootDir` 里挂载一次性清理的「为什么挂在 once 里」注释。
- `WaitEvent` / `FollowEvents` / `cmd/done.go` 三处调用点的「为什么在这里回收」注释。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1 && go test -race ./internal/client/ ./cmd/
git add internal/client/cursorgc.go internal/client/cursorgc_test.go internal/client/client.go internal/client/cursordir.go cmd/done.go
git commit -m "feat(client): 游标回收——归档即删、TTL 清扫、旧布局一次性清除

三条通道：观察到 archived 事件即删、done 成功后兜底删（两条幂等），30 天
TTL 兜住所有没走 done 的漏网。旧平铺 cursor-* 一次性清除（删而不迁移：
旧文件绝大多数是已归档任务的游标，本来就该删）。实测审核者本机曾堆积 98 个
从无回收的游标。全部回收动作失败只记 Debug，绝不影响游标读写。"
```

---

## Self-Review

**1. Spec coverage**

| spec 条目 | 落在哪 |
|---|---|
| §4.1 布局 `<根>/cursors/<agentd>/<taskID>` | Task 1（命名空间）+ Task 2（`cursorPath`） |
| §4.1 按地址不按 target 名 | Task 1 `cursorNamespace` 及其 why |
| §4.2 三级降级 + 真写探测 | Task 1 `resolveCursorRoot` / `probeCursorDirWritable` |
| §4.2 降级打一行 stderr | Task 1 Step 5（Warn） |
| §4.2 不加 flag/env、不做软链 | Global Constraints |
| §4.3 读失败分两类 | Task 2 `readCursorWithDiag` |
| §4.3 写失败降级、三级不成立则响亮失败 | Task 1（错误点名两路径）+ Task 2（接线） |
| §4.3 根只解析一次并缓存 | Task 1 `cursorRootOnce` + `TestCursorRootResolvesOnlyOnce` |
| §4.4 归档即删（双通道幂等） | Task 3 `DropCursor` + 三处调用点 |
| §4.4 TTL 清扫 | Task 3 `sweepCursors` |
| §4.4 旧平铺一次性清除、不迁移 | Task 3 `purgeLegacyFlatCursors` |
| §6 测试清单（三级降级/读失败两类/命名空间/回收三条） | Task 1、2、3 各自的 Step 1 |

无遗漏。

**2. Placeholder scan**：无 TBD/TODO；每个代码步骤都给了可直接粘贴的完整实现与测试；无「参照 Task N」。

**3. Type consistency**：`cursorPath` 在 Task 2 由包级函数改为方法，Task 3 全部按方法调用（`c.cursorPath`）；`cursorTempTTL` 由 `client.go` 迁入 `cursorgc.go`，Task 3 Step 3 已明确要求删除原处定义，避免重复声明；`cursorNamespace` / `cursorDirName` / `cursorTTL` 在 Task 1、3 定义处唯一。
