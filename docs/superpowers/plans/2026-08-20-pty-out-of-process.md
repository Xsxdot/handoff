# PTY 托管出 agentd 进程实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 PTY 终端会话活得比 agentd 长——agentd 崩溃、被 OOM 杀、`handoff upgrade`
换版重启之后，终端还在，滚屏内容还在，跑着的 build 没断。

**Architecture:** 一个会话一个独立进程（`handoff _ptyhost`），持有 PTY 主端 fd 与环形
缓冲；agentd 经 unix socket 连它，只是它的一个订阅者。`ptyhost.Host` 的六方法接口
（`Open`/`List`/`Get`/`Write`/`Close`/`Attach`）**保持不变、换掉实现**：现有实现原样搬进
ptyhost 进程，agentd 侧变成一个说同样六个方法的客户端。因此 `pty_api.go` 与 `pty_ws.go`
几乎一行不用改。

**Tech Stack:** Go 1.x；`github.com/creack/pty`；unix domain socket；`flock` 判活
（复用 `internal/prochost` 的 `AcquireLock` / `IsLocked`）。

**Spec:** `docs/superpowers/specs/2026-08-20-pty-out-of-process-design.md`

## Global Constraints

以下要求适用于**每一个** task：

1. **注释**：每个新建文件顶部写「职责 + 边界（它**不**做什么）」；每个导出符号写参数、
   返回、注意事项；非显然的判断写中文「为什么」，不复述代码。
2. **日志**：一律用 `*slog.Logger`，**禁止 `fmt.Printf`**。
   - ptyhost 进程有**自己的**日志落点 `~/.handoff/ptys/<id>/ptyhost.log`。
     **agentd 不在的时候只有它能作证**，这是它必须自己落盘的理由。
   - `internal/ptyhost/wire` 是纯编解码层，**不打日志**（错误原样上抛，由两侧带上下文记录）。
3. **错误分支必须带上下文**：`fmt.Errorf("绑定会话 socket %s: %w", path, err)`。
4. **成功路径不许静默**：进程启动、绑 socket、每次 attach/detach、shell 退出、
   24 小时到点自退，各一条。
5. **平台**：`go build ./...` 与 `GOOS=windows go build ./...` **都必须过**。
   Windows 上 PTY 仍然如实降级为 `ErrNotSupported`（不做 ConPTY）。
6. **gofmt**：每个提交前 `gofmt -l internal/ cmd/ | head`，输出必须为空。
7. **提交信息**：中文 `type(scope): 摘要`，结尾附
   `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。
8. **测试命令**：`go test ./internal/ptyhost/... ./internal/agentd/ ./cmd/`；
   全量 `go test ./...`。
9. **常量取值**（逐字照抄）：
   - 协议版本 `ProtoVersion = 1`
   - socket 出现的等待上限 `3 * time.Second`
   - `stat` 查询超时 `1 * time.Second`
   - 已退出会话的存活上限 `24 * time.Hour`
   - `service stop` 等待会话退出的上限 `2 * time.Second`
   - 会话目录 `<DataDir>/ptys/<session-id>/`，目录 0700，文件 0600
10. **关于 Task 5 / Task 6 的实现步骤**：这两个 task 给的是**完整的测试代码 + 逐条
    行为规格**，而不是整段实现代码。这是有意的——它们的实现各有三四百行，逐字写进
    计划只会让计划变成一份没被编译器检查过的源码；而测试是可执行的规格，行为规格
    列全了每一条分支。**如果你在实现时发现某条分支规格里没有，那是计划的漏洞，
    停下来问，不要自己发明。**
11. **两个信号不能合并**：「断开订阅」= 直接关 socket，**不发任何帧**；
    「杀掉会话」= 显式发 `kill` 帧。把它们压在一起就是「切个 tab 就杀掉跑了一晚上的
    build」。浏览器↔agentd 那一段已经守住这条（见 `TerminalTab` 与 `deletePtySession`
    的注释），agentd↔ptyhost 这一段必须同样守住。

---

### Task 1: 先确认进程围栏与压力告警的口径

spec §11 把这条标成「实现首个 task 读码确认，不要靠推测」。每个终端会话多一个常驻进程，
如果它被计入 executor 的进程围栏或压力告警，开几个终端就会让**任务派发**开始报
`resource_pressure`——那是一个查起来极其绕的假警报。

**Files:**
- Read: `internal/prochost/fence.go`、`internal/prochost/footprint.go`、
  `internal/prochost/members.go`、`internal/agentd/watchdog.go`
- Modify: `docs/superpowers/specs/2026-08-20-pty-out-of-process-design.md`（§11 那条）

- [ ] **Step 1: 读码回答三个问题**

1. `NprocLimit`（`fence.go`）作用在**谁**身上？是 executor 树的 RLIMIT_NPROC，
   还是 agentd 全进程？ptyhost 由 agentd fork，会不会继承到这个限制？
2. `resource_pressure` 的 `used` 从哪来（`footprint.go` / `procenum*.go`）？
   是「本机 uid 已用进程数」还是「executor 树成员数」？
3. `task_proc_pressure` 的 `used/budget` 统计口径（`members.go` 的后代名册）
   是否只沿 task 的进程树走？

- [ ] **Step 2: 把结论写回 spec §11**

把「**这两处的当前口径要在实现的第一个 task 里先读码确认**……」整段替换成实际结论，
形如：

```markdown
- **进程数**：每个终端会话多一个常驻进程（一个 goroutine 泵 + 256 KiB 环形缓冲）。
  **口径已确认（2026-08-20 读码）**：<这里写实际结论，含文件与行号>。
  因此 <不需要调整 / 需要在 X 处排除 ptyhost>。
```

如果结论是「会被计入」，**不要在本 task 里改代码**——把它记成一条新增 task 追加到
本计划末尾（Task 10 之前），说明要改哪里、判据是什么。改动与调研分开是为了让审核者
能单独否掉其中一个。

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/specs/2026-08-20-pty-out-of-process-design.md docs/superpowers/plans/2026-08-20-pty-out-of-process.md
git commit -m "docs(spec): 确认 ptyhost 进程与进程围栏/压力告警的口径

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: `internal/ptyhost/wire`——帧编解码

**Files:**
- Create: `internal/ptyhost/wire/wire.go`
- Create: `internal/ptyhost/wire/wire_test.go`

**Interfaces:**
- Produces:
  - `const ProtoVersion = 1`
  - `const (KindData byte = 0; KindControl byte = 1)`
  - `const MaxFrame = 1 << 20`
  - `type Control struct { Type string; Since uint64; Truncated bool; ProtoVersion int; Cols, Rows int; BytesOut uint64; Foreground bool; Attached int; ExitCode *int }`
  - 控制帧类型常量：`CtrlAttach` / `CtrlAttached` / `CtrlResize` / `CtrlStat` / `CtrlStatResp` / `CtrlExit` / `CtrlKill`
  - `func WriteData(w io.Writer, p []byte) error`
  - `func WriteControl(w io.Writer, c Control) error`
  - `func ReadFrame(r io.Reader) (kind byte, data []byte, ctrl *Control, err error)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/ptyhost/wire/wire_test.go`：

```go
package wire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestDataRoundTrip 数据帧原样往返，一个字节都不能变。
func TestDataRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("\x1b[31mhello\x00\xff world\x1b[0m")
	if err := WriteData(&buf, payload); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	kind, data, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if kind != KindData || ctrl != nil {
		t.Fatalf("kind=%d ctrl=%v，期望数据帧", kind, ctrl)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("data = %q，期望 %q", data, payload)
	}
}

// TestControlRoundTrip 控制帧的每个字段都要活着回来。
func TestControlRoundTrip(t *testing.T) {
	code := 3
	var buf bytes.Buffer
	in := Control{
		Type: CtrlStatResp, BytesOut: 12345, Foreground: true, Attached: 2,
		Cols: 120, Rows: 40, ExitCode: &code, ProtoVersion: ProtoVersion,
	}
	if err := WriteControl(&buf, in); err != nil {
		t.Fatalf("WriteControl: %v", err)
	}
	kind, _, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if kind != KindControl || ctrl == nil {
		t.Fatalf("kind=%d ctrl=%v，期望控制帧", kind, ctrl)
	}
	if ctrl.Type != CtrlStatResp || ctrl.BytesOut != 12345 || !ctrl.Foreground ||
		ctrl.Attached != 2 || ctrl.Cols != 120 || ctrl.Rows != 40 {
		t.Fatalf("ctrl = %+v", *ctrl)
	}
	if ctrl.ExitCode == nil || *ctrl.ExitCode != 3 {
		t.Fatalf("exit_code = %v，期望 3", ctrl.ExitCode)
	}
}

// TestExitCodeNilStaysNil 退出码的三态不能被 0 冒充。
//
// 这条与 proto.PtySession.ExitCode 同一纪律：缺席 = 还活着，出现 = 已退出。
// 编解码把 nil 变成 0，会让「还在跑」变成「已退出，成功」。
func TestExitCodeNilStaysNil(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteControl(&buf, Control{Type: CtrlStatResp}); err != nil {
		t.Fatalf("WriteControl: %v", err)
	}
	_, _, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if ctrl.ExitCode != nil {
		t.Fatalf("exit_code = %v，期望 nil", *ctrl.ExitCode)
	}
}

// TestUnknownFieldsIgnored 未知字段忽略——这是「协议只在破坏性变更时升版本」的前提。
func TestUnknownFieldsIgnored(t *testing.T) {
	var buf bytes.Buffer
	// 手工拼一个带未来字段的控制帧
	body := []byte(`{"type":"stat_resp","bytes_out":7,"future_field":{"a":1}}`)
	buf.WriteByte(KindControl)
	buf.Write([]byte{byte(len(body) >> 24), byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))})
	buf.Write(body)

	_, _, ctrl, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("未知字段不该让解码失败: %v", err)
	}
	if ctrl.BytesOut != 7 {
		t.Fatalf("bytes_out = %d，期望 7", ctrl.BytesOut)
	}
}

// TestOversizeRejected 超长帧当场拒绝，不先分配再失败。
//
// 长度前缀来自对端，信它去 make([]byte, n) 就是一个 4 GiB 的分配。
func TestOversizeRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(KindData)
	buf.Write([]byte{0xff, 0xff, 0xff, 0xff}) // 4 GiB
	_, _, _, err := ReadFrame(&buf)
	if err == nil {
		t.Fatal("期望拒绝超长帧")
	}
	if !strings.Contains(err.Error(), "长度") {
		t.Fatalf("错误应说明是长度问题: %v", err)
	}
}

// TestTruncatedFrameIsEOF 半截帧要能与「干净结束」区分开。
func TestTruncatedFrameIsEOF(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(KindData)
	buf.Write([]byte{0, 0, 0, 10})
	buf.Write([]byte("abc")) // 说好 10 字节只给 3
	_, _, _, err := ReadFrame(&buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v，期望 io.ErrUnexpectedEOF", err)
	}
}

// TestCleanEOF 连接干净关闭时给 io.EOF，调用方据此判断「对端正常走了」。
func TestCleanEOF(t *testing.T) {
	_, _, _, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v，期望 io.EOF", err)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ptyhost/wire/`
Expected: FAIL（包不存在）

- [ ] **Step 3: 写实现**

创建 `internal/ptyhost/wire/wire.go`：

```go
// Package wire 是 agentd 与 ptyhost 进程之间的帧格式。
//
// 职责：
//   - 定义帧布局 [类型:1][长度:4 大端][载荷] 与两种帧（PTY 原始字节 / 控制帧 JSON）
//   - 定义六个控制帧的词汇表与它们的载荷形状
//   - 编解码，仅此而已
//
// 边界：
//   - **不认识 socket、不认识会话、不打日志**：错误原样上抛，由两侧各自带上下文记录
//   - 不做版本协商：ReadFrame 只管解出来，版本怎么处置是调用方的事（spec §1.3）
//   - 不解析转义序列：数据帧就是一段字节
//
// 为什么数据走裸字节而不是塞进 JSON：PTY 输出是高频路径，base64 会带来 33% 膨胀
// 加两次编解码。这与浏览器那一段 /ws/pty 的取舍逐字一致（binary 帧走数据，
// text 帧走控制）——两段刻意同形，agentd 在中间转译时不需要状态机。
package wire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// ProtoVersion 是当前协议版本。
//
// **只有破坏性变更才 +1。** 加字段走「未知字段忽略」（ReadFrame 用 json.Unmarshal，
// 天然忽略未知键），不动版本号。这条纪律是 spec §1.3 那个降级方案能成立的前提：
// 版本号轻易变动，每次 handoff upgrade 都会让一批会话变成只能关闭的死物。
const ProtoVersion = 1

// 帧类型。
const (
	KindData    byte = 0 // 载荷是 PTY 原始字节
	KindControl byte = 1 // 载荷是 Control 的 JSON
)

// MaxFrame 是单帧载荷的上限（1 MiB）。
//
// 长度前缀来自对端，直接信它去 make([]byte, n) 就是一个可被对端指定大小的分配。
// PTY 单次读取上限是 32 KiB（engine 的 readChunk），控制帧不过几百字节，
// 1 MiB 有两个数量级余量。
const MaxFrame = 1 << 20

// 控制帧类型。
const (
	CtrlAttach   = "attach"    // agentd → ptyhost：订阅，带 since
	CtrlAttached = "attached"  // ptyhost → agentd：订阅已建立，带 since/truncated/proto_version
	CtrlResize   = "resize"    // agentd → ptyhost：本订阅者的尺寸
	CtrlStat     = "stat"      // agentd → ptyhost：查活事实
	CtrlStatResp = "stat_resp" // ptyhost → agentd：活事实
	CtrlExit     = "exit"      // ptyhost → agentd：shell 已退出
	CtrlKill     = "kill"      // agentd → ptyhost：杀进程组并退出
)

// Control 是所有控制帧共用的载荷。
//
// 一个结构体走七种帧：控制路径是低频的（订阅、改尺寸、查状态、收尸），
// 拆成七个类型只会让两端各多六个分支。高频的数据路径**不经过它**。
//
// ExitCode 用指针表达三态里的两态：**缺席 = 还活着**，出现 = 已退出且这是退出码。
// 与 proto.PtySession.ExitCode 同一纪律——绝不用 0 或 -1 冒充「不知道」。
//
// Truncated / Foreground **不带 omitempty**：false 是有意义的结论（「没有截断」
// 「空闲，随便关」），缺键会让对端分不清它和「这版还不认识这个字段」。
type Control struct {
	Type         string `json:"type"`
	Since        uint64 `json:"since,omitempty"`
	Truncated    bool   `json:"truncated"`
	ProtoVersion int    `json:"proto_version,omitempty"`
	Cols         int    `json:"cols,omitempty"`
	Rows         int    `json:"rows,omitempty"`
	BytesOut     uint64 `json:"bytes_out,omitempty"`
	Foreground   bool   `json:"foreground"`
	Attached     int    `json:"attached,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
}

// writeFrame 是两个写入口的共同实现。
func writeFrame(w io.Writer, kind byte, body []byte) error {
	if len(body) > MaxFrame {
		return fmt.Errorf("帧长度 %d 超过上限 %d", len(body), MaxFrame)
	}
	var head [5]byte
	head[0] = kind
	binary.BigEndian.PutUint32(head[1:], uint32(len(body)))
	// 头和体分两次写会让并发写入交错成乱帧。调用方保证串行，但拼成一次写
	// 让这个前提不必被记住——代价是一次小分配，值得。
	buf := make([]byte, 0, len(head)+len(body))
	buf = append(buf, head[:]...)
	buf = append(buf, body...)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("写帧（类型 %d，%d 字节）: %w", kind, len(body), err)
	}
	return nil
}

// WriteData 写一个数据帧。
//
// 参数：w 是目标；p 是 PTY 原始字节（不做任何转义或复制语义保证——
// 本函数在返回前用完 p，调用方之后可以复用它）。
// 返回：写失败或长度超上限时报错。
func WriteData(w io.Writer, p []byte) error { return writeFrame(w, KindData, p) }

// WriteControl 写一个控制帧。
//
// 参数：w 是目标；c 是控制内容，Type 必须是本包的 Ctrl* 之一（**不做校验**：
// 未知类型由对端忽略，这与「未知字段忽略」是同一条向前兼容策略）。
// 返回：JSON 编码失败或写失败时报错。
func WriteControl(w io.Writer, c Control) error {
	body, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码控制帧 %s: %w", c.Type, err)
	}
	return writeFrame(w, KindControl, body)
}

// ReadFrame 读一个帧。
//
// 返回：
//   - kind: KindData 或 KindControl
//   - data: kind==KindData 时是载荷；否则为 nil
//   - ctrl: kind==KindControl 时是解出的控制帧；否则为 nil
//   - err: io.EOF 表示对端**干净地**关闭了连接（正常终止）；
//     io.ErrUnexpectedEOF 表示读到半截帧（对端崩了或连接被切断）；
//     其余为格式错误或 IO 故障
//
// 注意：调用方必须能区分 io.EOF 与 io.ErrUnexpectedEOF——前者是「订阅者走了」，
// 后者是「对端死了」，在 agentd 侧一个该静默一个该记 Warn。
func ReadFrame(r io.Reader) (byte, []byte, *Control, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		// ReadFull 在一个字节都没读到时给 io.EOF，读了一部分才给 ErrUnexpectedEOF，
		// 正好就是我们要的两种语义，原样上抛
		return 0, nil, nil, err
	}
	kind := head[0]
	n := binary.BigEndian.Uint32(head[1:])
	if n > MaxFrame {
		return 0, nil, nil, fmt.Errorf("帧长度 %d 超过上限 %d", n, MaxFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		// 头读到了体没读全 = 半截帧。ReadFull 此时给 ErrUnexpectedEOF，
		// 但 n==0 时它给 nil，两种都要正确处理
		if err == io.EOF {
			return 0, nil, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, nil, err
	}
	switch kind {
	case KindData:
		return kind, body, nil, nil
	case KindControl:
		var c Control
		if err := json.Unmarshal(body, &c); err != nil {
			return 0, nil, nil, fmt.Errorf("解码控制帧: %w", err)
		}
		return kind, nil, &c, nil
	default:
		// 不认识的帧类型：**不报错、当作可忽略**。与「未知字段忽略」同一条策略，
		// 让将来新增第三种帧类型时旧的一端不至于断连
		return kind, nil, nil, nil
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ptyhost/wire/ -v`
Expected: PASS（七个用例全绿）

- [ ] **Step 5: 注释与格式自查**

- 文件头有职责 + 三条边界 + 「为什么数据走裸字节」✓
- `ProtoVersion` 说明了「只有破坏性变更才 +1」及其理由 ✓
- `MaxFrame` 说明了「长度前缀来自对端」这个安全理由 ✓
- `ReadFrame` 说明了两种 EOF 的语义差别 ✓
- 本包**不打日志** ✓

- [ ] **Step 6: 提交**

```bash
gofmt -l internal/ | head
git add internal/ptyhost/wire/
git commit -m "feat(ptyhost): agentd 与 ptyhost 进程之间的帧格式

[类型:1][长度:4][载荷]，数据走裸字节、控制走 JSON，与浏览器那一段同形。
未知字段与未知帧类型一律忽略，为「只在破坏性变更时升版本」兜底。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: `internal/ptyhost/sessdir`——会话目录与三态扫描

**Files:**
- Create: `internal/ptyhost/sessdir/sessdir.go`
- Create: `internal/ptyhost/sessdir/sessdir_test.go`

**Interfaces:**
- Consumes: `internal/prochost` 的 `AcquireLock` / `IsLocked` / `ErrLockHeld`
- Produces:
  - `type Meta struct { ID, BasePath, BaseKind, Cwd, Shell string; CreatedAt time.Time; PID, ProtoVersion int }`
  - `type State string`，取值 `StateLive` / `StateDead` / `StateBroken`
  - `type Entry struct { ID string; Meta Meta; State State; Err error }`
  - `func Dir(root, id string) string` / `SockPath` / `LockPath` / `MetaPath` / `LogPath`
  - `func Create(root, id string) error`（建目录 0700）
  - `func WriteMeta(root string, m Meta) error` / `func ReadMeta(root, id string) (Meta, error)`
  - `func Scan(root string) ([]Entry, error)`
  - `func Remove(root, id string) error`
  - `func CheckSockPath(root, id string) error`

**Scan 刻意不删任何东西**：它只报告三态，删由调用方做。这样「扫描」能被表驱动测试，
而「删」这个不可逆动作留在能打日志、能被审计的那一层。

- [ ] **Step 1: 写失败的测试**

创建 `internal/ptyhost/sessdir/sessdir_test.go`：

```go
package sessdir

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
)

func sampleMeta(id string) Meta {
	return Meta{
		ID: id, BasePath: "/repo/a", BaseKind: "workspace", Cwd: "/repo/a",
		Shell: "/bin/zsh", CreatedAt: time.Unix(1755648000, 0).UTC(),
		PID: 4242, ProtoVersion: 1,
	}
}

// TestMetaRoundTrip 元数据写读往返。
func TestMetaRoundTrip(t *testing.T) {
	root := t.TempDir()
	m := sampleMeta("s1")
	if err := Create(root, m.ID); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := WriteMeta(root, m); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	got, err := ReadMeta(root, m.ID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got != m {
		t.Fatalf("读回 = %+v，期望 %+v", got, m)
	}
	// 目录 0700、文件 0600：socket 上没有鉴权，谁连上谁就有一个 shell
	di, err := os.Stat(Dir(root, m.ID))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("目录权限 = %o，期望 0700", di.Mode().Perm())
	}
	fi, err := os.Stat(MetaPath(root, m.ID))
	if err != nil {
		t.Fatalf("stat meta: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("meta 权限 = %o，期望 0600", fi.Mode().Perm())
	}
}

// TestScanThreeStates 三态各来一个：活着、已死、meta 坏但锁还占着。
func TestScanThreeStates(t *testing.T) {
	root := t.TempDir()

	// ① 活着：锁被持有 + meta 正常
	live := sampleMeta("live")
	if err := Create(root, live.ID); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(root, live); err != nil {
		t.Fatal(err)
	}
	lk, err := prochost.AcquireLock(LockPath(root, live.ID))
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = lk.Release() })

	// ② 已死：meta 正常但没人持锁
	dead := sampleMeta("dead")
	if err := Create(root, dead.ID); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(root, dead); err != nil {
		t.Fatal(err)
	}

	// ③ 坏：锁被持有但 meta 是垃圾
	if err := Create(root, "broken"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MetaPath(root, "broken"), []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	lk2, err := prochost.AcquireLock(LockPath(root, "broken"))
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = lk2.Release() })

	entries, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	byID := map[string]Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	if len(byID) != 3 {
		t.Fatalf("扫到 %d 条，期望 3：%+v", len(byID), entries)
	}
	if byID["live"].State != StateLive || byID["live"].Meta != live {
		t.Fatalf("live = %+v", byID["live"])
	}
	if byID["dead"].State != StateDead {
		t.Fatalf("dead = %+v", byID["dead"])
	}
	// meta 坏但锁占着 = 有个进程活着而我们不知道它是什么。
	// 必须是 broken 而不是 dead——判成 dead 会让调用方去删目录、去杀进程
	if byID["broken"].State != StateBroken || byID["broken"].Err == nil {
		t.Fatalf("broken = %+v", byID["broken"])
	}
}

// TestScanDoesNotDelete Scan 不删任何东西——删是调用方的事。
func TestScanDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	m := sampleMeta("dead")
	if err := Create(root, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(root, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(root); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if _, err := os.Stat(Dir(root, m.ID)); err != nil {
		t.Fatalf("Scan 不该删目录: %v", err)
	}
}

// TestScanMissingRoot 根目录不存在时给空结果，不报错——首次启动就是这样。
func TestScanMissingRoot(t *testing.T) {
	entries, err := Scan(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v，期望空", entries)
	}
}

// TestScanIgnoresStrayFiles 根目录下的散文件不是会话，跳过。
func TestScanIgnoresStrayFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v，期望空", entries)
	}
}

// TestRemoveIsIdempotent 删不存在的会话不报错。
func TestRemoveIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := Remove(root, "never-existed"); err != nil {
		t.Fatalf("Remove 应幂等: %v", err)
	}
}

// TestCheckSockPath 路径过长要在 bind 之前给出可读错误。
func TestCheckSockPath(t *testing.T) {
	if err := CheckSockPath("/Users/dev/.handoff/ptys", "7ec762e7-3bd2-412c-a39c-e4cf8b4057ad"); err != nil {
		t.Fatalf("正常路径不该被拒: %v", err)
	}
	long := filepath.Join("/tmp", string(make([]byte, 200)))
	err := CheckSockPath(long, "s1")
	if err == nil {
		t.Fatal("超长路径应被拒")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ptyhost/sessdir/`
Expected: FAIL（包不存在）

- [ ] **Step 3: 写实现**

创建 `internal/ptyhost/sessdir/sessdir.go`：

```go
// Package sessdir 是 PTY 会话在磁盘上的落点：目录布局、元数据、以及跨 agentd
// 重启的三态扫描。
//
// 布局（<root> 通常是 <DataDir>/ptys）：
//
//	<root>/<session-id>/
//	  meta.json     静态事实，agentd 重启后据此重建会话表
//	  sock          unix socket，ptyhost 进程监听
//	  lock          存活锁，ptyhost 全生命周期持有
//	  ptyhost.log   ptyhost 进程自己的日志
//
// 职责：路径拼装、元数据读写、扫描判活、删除。
//
// 边界：
//   - **Scan 不删任何东西**，只报告三态。删由调用方做——这样扫描能被表驱动
//     测试，而「删」这个不可逆动作留在能打日志、能被审计的那一层
//   - 不起进程、不连 socket、不认识帧格式
//   - 不打日志：它是叶子层，错误带上下文上抛，由调用方记录
//
// 为什么判活用文件锁不用 pid：pid 会被操作系统复用，「进程存在」不等于
// 「我的那个进程存在」——workspace.go 历史上就因此误杀过无关进程组。
// flock 由内核在进程终止时无条件释放（正常退出/panic/SIGKILL/掉电皆然），
// 不存在陈旧锁。这条与 internal/prochost 同源。
package sessdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// maxSockPath 是 unix socket 路径的保守上限。
//
// macOS 的 sockaddr_un.sun_path 是 104 字节、Linux 是 108，取小的再留 4 字节余量。
// 超了 bind 会返回一个 "invalid argument"——那句话完全看不出是路径太长，
// 所以必须在 bind 之前自己检查并给出可读错误。
const maxSockPath = 100

// State 是一个会话目录的扫描结论。
type State string

const (
	// StateLive：锁被持有且 meta 可读 —— 那个 ptyhost 进程还活着。
	StateLive State = "live"
	// StateDead：没人持锁 —— 进程已经不在，目录可以清掉。
	StateDead State = "dead"
	// StateBroken：锁被持有但 meta 读不出来 —— 有个进程活着而我们不知道它是什么。
	//
	// **必须与 dead 分开。** 判成 dead 会让调用方去删目录、去杀进程，而盲杀一个
	// 说不清来历的进程是这套东西最不该做的事（spec §7）。
	StateBroken State = "broken"
)

// Meta 是一个会话的静态事实，落在 meta.json 里。
//
// 只放**不会变**的东西：cols/rows/bytes_out/foreground 都是活事实，会随时间变化，
// 存在这里必然是陈的，读它比不读更糟（spec §4）。活事实经 stat 控制帧现问。
type Meta struct {
	ID        string    `json:"id"`
	BasePath  string    `json:"base_path"`
	BaseKind  string    `json:"base_kind"`
	Cwd       string    `json:"cwd"`
	Shell     string    `json:"shell"`
	CreatedAt time.Time `json:"created_at"`
	PID       int       `json:"pid"`
	// ProtoVersion 是**起这个会话的那个二进制**所用的协议版本。
	//
	// 写进 meta.json 而不是只在握手里给：这样 agentd 在列表阶段就知道哪个会话
	// 接不进来，能直接标出「由 vX 托管」，而不是等用户点进去才发现（spec §3）。
	ProtoVersion int `json:"proto_version"`
}

// Entry 是扫描结果里的一条。State 为 StateBroken 时 Err 说明是怎么坏的。
type Entry struct {
	ID    string
	Meta  Meta
	State State
	Err   error
}

// Dir 返回一个会话的目录路径。
func Dir(root, id string) string { return filepath.Join(root, id) }

// MetaPath / SockPath / LockPath / LogPath 返回会话目录里的四个落点。
func MetaPath(root, id string) string { return filepath.Join(Dir(root, id), "meta.json") }
func SockPath(root, id string) string { return filepath.Join(Dir(root, id), "sock") }
func LockPath(root, id string) string { return filepath.Join(Dir(root, id), "lock") }
func LogPath(root, id string) string  { return filepath.Join(Dir(root, id), "ptyhost.log") }

// CheckSockPath 在 bind 之前检查 socket 路径长度。
//
// 参数：root 是会话根目录；id 是会话 id。
// 返回：超过 maxSockPath 时返回可读错误，否则 nil。
//
// 注意：这**不是**多余的防御。DataDir 可以被配置到任意深的路径下，
// 而 bind 对超长路径只会给一句 "invalid argument"。
func CheckSockPath(root, id string) error {
	p := SockPath(root, id)
	if len(p) > maxSockPath {
		return fmt.Errorf("会话 socket 路径过长（%d 字节，上限 %d）：%s；请把 DataDir 换到更短的路径下", len(p), maxSockPath, p)
	}
	return nil
}

// Create 建出一个会话目录（0700）。已存在时不报错。
func Create(root, id string) error {
	if err := os.MkdirAll(Dir(root, id), 0o700); err != nil {
		return fmt.Errorf("创建会话目录 %s: %w", Dir(root, id), err)
	}
	return nil
}

// WriteMeta 写入元数据（0600，整体覆盖）。
//
// 参数：root 是会话根目录；m 是元数据，m.ID 决定写到哪个会话目录下。
// 返回：编码或写入失败时报错。
//
// 注意：**先写临时文件再 rename**。agentd 可能在任何时刻读它；直接覆写会让读者
// 有机会读到半截 JSON，而那会被判成 StateBroken——一个纯属自找的假警报。
func WriteMeta(root string, m Meta) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("编码会话元数据 %s: %w", m.ID, err)
	}
	tmp := MetaPath(root, m.ID) + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("写会话元数据 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, MetaPath(root, m.ID)); err != nil {
		return fmt.Errorf("落定会话元数据 %s: %w", MetaPath(root, m.ID), err)
	}
	return nil
}

// ReadMeta 读一个会话的元数据。
//
// 返回：解析失败或文件不存在时报错（调用方据此判 StateBroken）。
func ReadMeta(root, id string) (Meta, error) {
	body, err := os.ReadFile(MetaPath(root, id))
	if err != nil {
		return Meta{}, fmt.Errorf("读会话元数据 %s: %w", MetaPath(root, id), err)
	}
	var m Meta
	if err := json.Unmarshal(body, &m); err != nil {
		return Meta{}, fmt.Errorf("解析会话元数据 %s: %w", MetaPath(root, id), err)
	}
	return m, nil
}

// Scan 扫描根目录下的全部会话，逐个判活。
//
// 参数：root 是会话根目录；不存在时返回空结果而**不报错**（首次启动就是这样）。
// 返回：每个会话一条 Entry，含三态结论；只有 IO 故障才返回错误。
//
// 注意：**本函数不删任何东西。** StateDead 的目录由调用方决定何时清——
// 那是不可逆动作，该留在能打日志的那一层。
func Scan(root string) ([]Entry, error) {
	items, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读会话根目录 %s: %w", root, err)
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		// 根目录下的散文件（.DS_Store 之类）不是会话
		if !it.IsDir() {
			continue
		}
		id := it.Name()
		held, err := prochost.IsLocked(LockPath(root, id))
		if err != nil {
			// 试锁本身失败：既不能判活也不能判死，按 broken 处理——
			// 宁可留一个要人看的条目，也不要删掉一个可能还活着的会话
			out = append(out, Entry{ID: id, State: StateBroken, Err: fmt.Errorf("试锁失败: %w", err)})
			continue
		}
		if !held {
			out = append(out, Entry{ID: id, State: StateDead})
			continue
		}
		m, err := ReadMeta(root, id)
		if err != nil {
			out = append(out, Entry{ID: id, State: StateBroken, Err: err})
			continue
		}
		out = append(out, Entry{ID: id, Meta: m, State: StateLive})
	}
	return out, nil
}

// Remove 删掉一个会话目录。不存在时不报错（幂等）。
//
// 注意：调用方**必须**先确认该会话已死（Scan 报 StateDead）。删掉一个还活着的
// 会话的目录不会杀死它的进程，只会让它变成谁也找不到的孤儿。
func Remove(root, id string) error {
	if err := os.RemoveAll(Dir(root, id)); err != nil {
		return fmt.Errorf("删除会话目录 %s: %w", Dir(root, id), err)
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ptyhost/sessdir/ -v`
Expected: PASS（七个用例）

Windows 上 `prochost.LockSupported()` 可能为 false，此时试锁恒不撞、三态用例会失败。
**先跑 `go test`（本机 unix）确认通过即可**；Windows 只要求 `GOOS=windows go build ./...` 过。
若本包的测试在 Windows CI 上跑，给三态那两个用例加
`if !prochost.LockSupported() { t.Skip("本平台不支持文件锁，判活语义不成立") }`。

- [ ] **Step 5: 注释自查**

- 文件头画了目录布局、写了三条边界、说明了「为什么用文件锁不用 pid」✓
- `StateBroken` 说明了「必须与 dead 分开」及其后果 ✓
- `Meta` 说明了「只放不会变的东西」✓
- `WriteMeta` 说明了「先写临时文件再 rename」的理由 ✓
- `CheckSockPath` 说明了「这不是多余的防御」✓
- `Remove` 的注释警告了「先确认已死」✓

- [ ] **Step 6: 提交**

```bash
gofmt -l internal/ | head
git add internal/ptyhost/sessdir/
git commit -m "feat(ptyhost): 会话目录布局与三态扫描

meta.json 只放静态事实（活事实经 stat 现问）；判活用 flock 不用 pid。
Scan 只报告不删除——删是不可逆动作，留给能打日志的那一层。
meta 坏但锁占着单列 broken：盲杀一个说不清来历的进程是最不该做的事。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: 把 PTY 引擎搬进 `internal/ptyhost/engine`

**这是一次纯搬家：一行逻辑都不改。** 既有的那批测试原样跟着搬，它们仍是同一份网——
如果搬完有测试变红，那就是搬坏了，不是测试过时了。

**Files:**
- Move: `internal/ptyhost/ptyhost.go` → `internal/ptyhost/engine/engine.go`
- Move: `internal/ptyhost/ring.go` / `ring_test.go` → `internal/ptyhost/engine/`
- Move: `internal/ptyhost/platform_unix.go` / `platform_other.go` / `platform_unix_test.go` → `internal/ptyhost/engine/`
- Move: `internal/ptyhost/ptyhost_test.go` → `internal/ptyhost/engine/engine_test.go`
- Keep（留在 `internal/ptyhost`）: `envforward.go` / `envforward_test.go` / `errors.go`
- Create: `internal/ptyhost/types.go`（Session / OpenOptions 上移）
- Create: `internal/ptyhost/attachment.go`（**Attachment 上移**，见 Step 2 第 6 条）
- Create: `internal/ptyhost/supported_unix.go` / `supported_other.go`

**为什么这样切**：`pty_api.go` 现在用的是 `ptyhost.OpenOptions` / `ptyhost.Session` /
`ptyhost.ErrNotSupported` / `ptyhost.ErrNoSession` / `ptyhost.DefaultEnvForward` /
`ptyhost.ResolveEnvForward`。这些**必须留在 `internal/ptyhost`**，否则 agentd 侧要改一堆
import——而本计划的核心承诺就是 `pty_api.go` 几乎不动。引擎 import `ptyhost` 取类型，
`ptyhost` 不 import 引擎，没有环。

**Interfaces:**
- Produces:
  - `internal/ptyhost`: `Session` / `OpenOptions` / `ErrNoSession` / `ErrTooManySubscribers` /
    `ErrSessionExited` / `ErrNotSupported` / `Supported() bool` / 既有的 envforward 两函数
  - `internal/ptyhost`: `Attachment`（上移，含 `Backlog` / `Since` / `Truncated` / `Out`
    四个字段与 `Detach()` / `ExitCode()` / `Resize()` 三个方法）
  - `internal/ptyhost/engine`: `Engine`（由原 `Host` 改名）/ `New`，
    与原 `Host` 完全相同的六方法；`Attach` 返回 `*ptyhost.Attachment`

- [ ] **Step 1: 建目录并搬文件**

```bash
mkdir -p internal/ptyhost/engine
git mv internal/ptyhost/ptyhost.go        internal/ptyhost/engine/engine.go
git mv internal/ptyhost/ptyhost_test.go   internal/ptyhost/engine/engine_test.go
git mv internal/ptyhost/ring.go           internal/ptyhost/engine/ring.go
git mv internal/ptyhost/ring_test.go      internal/ptyhost/engine/ring_test.go
git mv internal/ptyhost/platform_unix.go  internal/ptyhost/engine/platform_unix.go
git mv internal/ptyhost/platform_other.go internal/ptyhost/engine/platform_other.go
git mv internal/ptyhost/platform_unix_test.go internal/ptyhost/engine/platform_unix_test.go
```

- [ ] **Step 2: 改包名与类型归属**

1. 搬过去的五个文件包名改成 `package engine`（测试文件是 `package engine` 与
   `package engine_test`，照原文件的口径改）。
2. `Host` 改名为 `Engine`，`New` 保持不变（`engine.New(log)`）。
   `Attachment` 保持不变。方法签名一个都不改。
3. 把 `Session` 与 `OpenOptions` 两个结构体**从 engine.go 剪切**到新建的
   `internal/ptyhost/types.go`，连同它们的全部注释。engine.go 里改为引用
   `ptyhost.Session` / `ptyhost.OpenOptions`（`import "github.com/Xsxdot/handoff/internal/ptyhost"`）。
4. 把 `ErrNoSession` / `ErrTooManySubscribers` / `ErrSessionExited` 三个哨兵**从
   engine.go 剪切**到 `internal/ptyhost/errors.go`（`ErrNotSupported` 已经在那儿），
   engine.go 里改为引用 `ptyhost.ErrNoSession` 等。
6. **`Attachment` 必须上移到 `internal/ptyhost`，且必须做成「壳 + 注入行为」。**

   为什么：`pty_ws.go` 的 `pumpPtyUplink` 签名里写死了 `att *ptyhost.Attachment`。
   如果 `Attachment` 留在 engine，本 task 的过渡接线**当场编译不过**——而 Task 6 之后
   客户端又要一个同名同形、内部实现完全不同的类型。把它做成一个壳，两边各注入自己的
   行为，`pty_ws.go` 就永远不用改。

   新建 `internal/ptyhost/attachment.go`：

   ```go
   // attachment.go —— 一次订阅的对外形态。
   //
   // 职责：定义调用方（agentd 的 pty_ws.go）看得见的四个字段与三个方法。
   //
   // 边界：**它自己什么都不做**——三个方法逐字转交注入进来的 ops。
   //
   // 为什么是「壳 + 注入」而不是接口：pty_ws.go 的 pumpPtyUplink 签名里写死了
   // `att *ptyhost.Attachment`（具体类型，不是接口）。改成接口就要改它，而
   // 「pty_api.go / pty_ws.go 一行不改」是本次搬家的核心承诺。壳保住了这个承诺：
   // 进程内引擎与 socket 客户端各注入一套 ops，对调用方完全同形。
   package ptyhost

   // AttachOps 是一次订阅的三个行为，由构造它的一方提供。
   //
   // 导出它是因为 internal/ptyhost/engine 要在包外实现它；本包之外没有别的
   // 合法实现者（客户端在本包内）。
   type AttachOps interface {
       Detach()
       ExitCode() *int
       Resize(cols, rows int) error
   }

   // Attachment 是一次订阅。Backlog 是建连瞬间的历史回放，Out 是后续实时输出；
   // Out 被关闭意味着**会话结束**（不是网络抖动），客户端应停止重连。
   //
   // 这段语义是承重的：pty_ws.go 的下行循环就按它写的，两侧实现都必须遵守——
   // 引擎在 shell 退出时关它，客户端在收到 exit 帧或连接断开时关它。
   type Attachment struct {
       Backlog   []byte
       Since     uint64
       Truncated bool
       Out       <-chan []byte

       ops AttachOps
   }

   // NewAttachment 组装一个订阅壳。两侧构造者用它，调用方不用。
   func NewAttachment(backlog []byte, since uint64, truncated bool, out <-chan []byte, ops AttachOps) *Attachment {
       return &Attachment{Backlog: backlog, Since: since, Truncated: truncated, Out: out, ops: ops}
   }

   // Detach 退订。**不杀会话**——切 tab、切目录、关页面都走它。
   func (a *Attachment) Detach() { a.ops.Detach() }

   // ExitCode 返回 shell 的退出码；nil = 还活着，或对端没给出退出码。
   func (a *Attachment) ExitCode() *int { return a.ops.ExitCode() }

   // Resize 上报本订阅者的尺寸。实际尺寸由所有订阅者取最小值协商而来。
   func (a *Attachment) Resize(cols, rows int) error { return a.ops.Resize(cols, rows) }
   ```

   engine 侧把原 `Attachment` 的三个方法搬成一个不导出的 `engineAttachOps`
   （持有原来那三个私有字段 `h` / `s` / `sub`），`Engine.Attach` 末尾改成
   `return ptyhost.NewAttachment(backlog, start, truncated, sub.ch, &engineAttachOps{...}), nil`。
   **三个方法的方法体一行不改**，只是换了接收者类型。

7. `ptySupported` 常量在两个 platform 文件里各有一份，engine 需要它、`ptyhost.Supported()`
   也需要它。**不要跨包引用一个常量**——新建两个只含常量的小文件：

`internal/ptyhost/supported_unix.go`：

```go
//go:build unix

package ptyhost

// ptySupported 是本平台的 PTY 能力常量。
//
// 为什么这里和 engine 各留一份：它是编译期常量，跨包引用要么导出一个只有一个
// 布尔的符号，要么让 ptyhost 反过来 import engine（那就有环了）。两行重复
// 换掉一个包依赖，划算。两边同时要改的场景只有「新增一个支持 PTY 的平台」，
// 那时本来就要通读所有 platform 文件。
const ptySupported = true
```

`internal/ptyhost/supported_other.go`：

```go
//go:build !unix

package ptyhost

// 见 supported_unix.go 的说明。文件名用 _other 而不是 _windows：
// Go 会把 _windows.go 当成隐式 GOOS 约束，那样除 windows 外的其它非 unix
// 平台就没有实现了。与 platform_other.go 同款。
const ptySupported = false
```

在 `internal/ptyhost/types.go` 里加：

```go
// Supported 报告本平台是否支持 PTY，供 /api/status 的 pty_supported 上报。
//
// 它是编译期常量而不是运行时探测：agentd 与 ptyhost 进程跑在同一台机器上、
// 由同一个二进制拉起，能力必然相同，不需要问对端。
func Supported() bool { return ptySupported }
```

- [ ] **Step 3: 跑既有测试，确认搬家没搬坏**

Run: `go test ./internal/ptyhost/...`
Expected: PASS。**既有用例一个都不该改。** 如果有用例变红，先看是不是搬漏了一段逻辑，
而不是去改用例。

- [ ] **Step 4: 确认两个平台都能编译**

Run: `go build ./... && GOOS=windows go build ./...`
Expected: 都过

此时 `internal/agentd` 会编译失败（`ptyhost.New` 没了）——**这是预期的**，Task 6/8 才接上。
为了让本 task 能独立提交，在 `internal/agentd/server.go` 里把 `pty` 字段的类型暂时改成
`*engine.Engine`、`ptyhost.New(log)` 改成 `engine.New(log)`（加一行 import）。
这是**一次性的过渡接线**，Task 6 会把它换成客户端。在改动处留一行注释：

```go
	// 过渡接线：Task 6 会把它换成连 ptyhost 进程的客户端。
	// 现在直连引擎，行为与搬家前逐字节一致。
	pty *engine.Engine
```

- [ ] **Step 5: 全量测试**

Run: `go test ./...`
Expected: ok（agentd 的既有 PTY 测试也该全绿——行为没变）

- [ ] **Step 6: 提交**

```bash
gofmt -l internal/ | head
git add -A
git commit -m "refactor(ptyhost): 把 PTY 引擎搬进 internal/ptyhost/engine

纯搬家，一行逻辑不改：Host 改名 Engine，Session/OpenOptions/三个哨兵上移到
internal/ptyhost 供两侧共用。既有测试原样跟着搬，仍是同一份网。
agentd 侧暂时直连引擎，Task 6 换成客户端。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: ptyhost 进程主体与 `handoff _ptyhost` 子命令

**Files:**
- Create: `internal/ptyhost/hostproc/hostproc.go`
- Create: `internal/ptyhost/hostproc/hostproc_test.go`
- Create: `cmd/ptyhost.go`

**Interfaces:**
- Consumes: `internal/ptyhost/engine`、`internal/ptyhost/sessdir`、`internal/ptyhost/wire`、
  `internal/prochost`（`AcquireLock`）
- Produces:
  - `type Spec struct { Root, ID, BasePath, BaseKind, Cwd, Shell string; Env []string; Cols, Rows int }`
  - `func Run(specPath string) error`
  - `const ExitedTTL = 24 * time.Hour`
  - 隐藏子命令 `handoff _ptyhost --spec <路径>`

**agentd 用到的这套接口的全部表面**（读码确认，`pty_api.go` + `pty_ws.go`）：
`Open` / `List` / `Close` / `Write(id, p)` / `Attach(id, since)`，以及 `Attachment` 的
`Since` / `Truncated` / `Backlog` / `Out` / `Detach()` / `ExitCode()` / `Resize(cols, rows)`。
ptyhost 进程要能服务的就是这些，一个不多。

- [ ] **Step 1: 写失败的测试**

创建 `internal/ptyhost/hostproc/hostproc_test.go`：

```go
//go:build unix

package hostproc_test

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
)

// startHost 在后台跑一个真的 ptyhost 主体，返回会话根目录与会话 id。
func startHost(t *testing.T) (root, id string) {
	t.Helper()
	root = t.TempDir()
	id = "s1"
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	spec := hostproc.Spec{
		Root: root, ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
		Shell: "/bin/sh", Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "PS1=$ "},
		Cols: 80, Rows: 24,
	}
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(root, id, "spec.json")
	if err := os.WriteFile(specPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- hostproc.Run(specPath) }()
	t.Cleanup(func() {
		// 用 kill 帧收口，让 Run 正常返回；超时就不等了，测试进程退出会带走它
		if c, err := net.Dial("unix", sessdir.SockPath(root, id)); err == nil {
			_ = wire.WriteControl(c, wire.Control{Type: wire.CtrlKill})
			_ = c.Close()
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	waitSock(t, sessdir.SockPath(root, id))
	return root, id
}

func waitSock(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket 迟迟没出现: %s", path)
}

// TestMetaWrittenWithPidAndVersion 起来之后 meta.json 必须齐活——
// agentd 重启后就靠它重建会话表。
func TestMetaWrittenWithPidAndVersion(t *testing.T) {
	root, id := startHost(t)
	m, err := sessdir.ReadMeta(root, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if m.PID != os.Getpid() || m.ProtoVersion != wire.ProtoVersion || m.Shell != "/bin/sh" {
		t.Fatalf("meta = %+v", m)
	}
}

// TestAttachEchoesInput 打通「写进去 → 从订阅里出来」这一整条。
func TestAttachEchoesInput(t *testing.T) {
	root, id := startHost(t)
	c, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := wire.WriteControl(c, wire.Control{Type: wire.CtrlAttach, Since: 0}); err != nil {
		t.Fatal(err)
	}
	// 首帧必须是 attached，且带上协议版本
	_, _, ctrl, err := wire.ReadFrame(c)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if ctrl == nil || ctrl.Type != wire.CtrlAttached || ctrl.ProtoVersion != wire.ProtoVersion {
		t.Fatalf("首帧 = %+v，期望 attached", ctrl)
	}

	if err := wire.WriteData(c, []byte("echo HANDOFF_MARK\n")); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, c, "HANDOFF_MARK") {
		t.Fatal("没等到 shell 的回显")
	}
}

// waitFor 持续读帧直到看到 want 或超时。
func waitFor(t *testing.T, c net.Conn, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var acc []byte
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		kind, data, _, err := wire.ReadFrame(c)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if err == io.EOF {
				return false
			}
			continue
		}
		if kind == wire.KindData {
			acc = append(acc, data...)
			if len(acc) > 1<<16 {
				acc = acc[len(acc)-(1<<15):]
			}
			for i := 0; i+len(want) <= len(acc); i++ {
				if string(acc[i:i+len(want)]) == want {
					return true
				}
			}
		}
	}
	return false
}

// TestStatReportsLiveFacts stat 是活事实的唯一来源（meta.json 里的必然是陈的）。
func TestStatReportsLiveFacts(t *testing.T) {
	root, id := startHost(t)
	c, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := wire.WriteControl(c, wire.Control{Type: wire.CtrlStat}); err != nil {
		t.Fatal(err)
	}
	_, _, ctrl, err := wire.ReadFrame(c)
	if err != nil {
		t.Fatal(err)
	}
	if ctrl == nil || ctrl.Type != wire.CtrlStatResp {
		t.Fatalf("ctrl = %+v", ctrl)
	}
	if ctrl.Cols != 80 || ctrl.Rows != 24 {
		t.Fatalf("尺寸 = %dx%d，期望 80x24", ctrl.Cols, ctrl.Rows)
	}
	if ctrl.ExitCode != nil {
		t.Fatalf("shell 还活着，exit_code 必须缺席，得到 %d", *ctrl.ExitCode)
	}
}

// TestSinceResumeAfterReconnect 这是整个 A 的核心承诺：
// 「agentd 那一侧」断了再连回来，滚屏一个字节都不丢。
func TestSinceResumeAfterReconnect(t *testing.T) {
	root, id := startHost(t)

	c1, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteControl(c1, wire.Control{Type: wire.CtrlAttach, Since: 0}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wire.ReadFrame(c1); err != nil { // attached
		t.Fatal(err)
	}
	if err := wire.WriteData(c1, []byte("echo BEFORE_DROP\n")); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, c1, "BEFORE_DROP") {
		t.Fatal("没等到第一段输出")
	}
	// 模拟 agentd 崩了：直接把连接切掉，**不发任何帧**
	_ = c1.Close()

	// 重新连上，since=0 全量回放，之前那段必须还在
	c2, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := wire.WriteControl(c2, wire.Control{Type: wire.CtrlAttach, Since: 0}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wire.ReadFrame(c2); err != nil { // attached
		t.Fatal(err)
	}
	if !waitFor(t, c2, "BEFORE_DROP") {
		t.Fatal("重连后回放里没有断线前的输出——since 续传没保住")
	}
}

// TestDetachDoesNotKill 断开订阅**不能**杀掉会话。
//
// 这条是承重的：把「断开订阅」和「杀掉会话」压在一个信号上，就是
// 「切个 tab 就杀掉跑了一晚上的 build」。
func TestDetachDoesNotKill(t *testing.T) {
	root, id := startHost(t)
	c1, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteControl(c1, wire.Control{Type: wire.CtrlAttach}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wire.ReadFrame(c1); err != nil {
		t.Fatal(err)
	}
	_ = c1.Close()

	time.Sleep(200 * time.Millisecond)
	// 还能连上、还能问到 stat = 会话没被杀
	c2, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatalf("断开订阅之后会话就没了: %v", err)
	}
	defer c2.Close()
	if err := wire.WriteControl(c2, wire.Control{Type: wire.CtrlStat}); err != nil {
		t.Fatal(err)
	}
	if _, _, ctrl, err := wire.ReadFrame(c2); err != nil || ctrl == nil {
		t.Fatalf("会话应仍然可用: err=%v ctrl=%v", err, ctrl)
	}
}

// TestKillEndsProcessAndCleansDir kill 帧要杀干净并清目录。
func TestKillEndsProcessAndCleansDir(t *testing.T) {
	root := t.TempDir()
	id := "k1"
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	spec := hostproc.Spec{
		Root: root, ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
		Shell: "/bin/sh", Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color"},
		Cols: 80, Rows: 24,
	}
	body, _ := json.Marshal(spec)
	specPath := filepath.Join(root, id, "spec.json")
	if err := os.WriteFile(specPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- hostproc.Run(specPath) }()
	waitSock(t, sessdir.SockPath(root, id))

	c, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteControl(c, wire.Control{Type: wire.CtrlKill}); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run 应正常返回: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("收到 kill 之后 Run 没有返回")
	}
	if _, err := os.Stat(sessdir.Dir(root, id)); !os.IsNotExist(err) {
		t.Fatalf("kill 之后会话目录应被清掉: %v", err)
	}
}

// TestSecondInstanceRefuses 同一个会话目录不能被两个 ptyhost 同时占。
func TestSecondInstanceRefuses(t *testing.T) {
	root, id := startHost(t)
	err := hostproc.Run(filepath.Join(root, id, "spec.json"))
	if err == nil {
		t.Fatal("第二个实例应被锁挡下")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ptyhost/hostproc/`
Expected: FAIL（包不存在）

- [ ] **Step 3: 写实现**

创建 `internal/ptyhost/hostproc/hostproc.go`。要点逐条（结构与 `prochost.RunShim` 同款）：

```go
// Package hostproc 是 ptyhost 进程的主体：一个进程托管一个 PTY 会话。
//
// 职责：
//   - 持有存活锁（整个生命周期），作为 agentd 侧判活的唯一判据
//   - 起引擎、开 shell、写 meta.json
//   - 监听 unix socket，把每条连接当成一个订阅者，收发 wire 帧
//   - shell 退出后继续活着守退出码与最后那屏输出，ExitedTTL 到点自退
//   - 收到 kill 帧时杀进程组、清目录、退出
//
// 边界：
//   - **不认识 agentd**：它不知道对端是谁，也不关心对端在不在。
//     agentd 整段时间不在，本进程照常跑——这正是它存在的理由
//   - 不认识 HTTP / WebSocket / 任务模型
//   - 不做鉴权：socket 是 0600、目录 0700，能连上它的人本来就能在这台机器上
//     以同一个 uid 起 shell
//
// 为什么必须是独立进程而不是 agentd 的一个 goroutine：agentd 一死，
// PTY 主端 fd 关闭，shell 收到 SIGHUP，整棵进程树跟着走。这条与
// prochost/shim.go 的「退出哨兵需要一个常驻父进程 waitpid」是同一类理由。
package hostproc
```

**`Run(specPath string) error` 的次序（不可调换）：**

1. 读 spec（`os.ReadFile` + `json.Unmarshal`）。
2. **先抢锁**：`prochost.AcquireLock(sessdir.LockPath(spec.Root, spec.ID))`。
   撞锁（`errors.Is(err, prochost.ErrLockHeld)`）说明已经有一个 ptyhost 占着这个会话，
   **立刻返回错误、什么都不碰**——抢锁必须在开 shell 之前，否则第二个实例会先 fork 出
   一个 shell 再发现自己是多余的。
3. 打开 `sessdir.LogPath` 追加落盘，`slog.New(slog.NewTextHandler(f, ...))`。
   之后所有日志走它。**这一步之后才有作证能力**，所以前两步的失败只能靠返回值。
4. `sessdir.CheckSockPath` → `net.Listen("unix", sockPath)`。
   listen 之前先 `os.Remove(sockPath)` 清掉可能的陈旧文件（进程被 SIGKILL 时 socket
   文件会留下）——**但只有在第 2 步抢到锁之后才允许这么做**：锁没抢到就删别人的
   socket，会把一个活着的会话弄哑。
5. `engine.New(log)` + `eng.Open(ptyhost.OpenOptions{...})`。失败则清理 listener 并返回。
6. `sessdir.WriteMeta`（含 `PID: os.Getpid()`、`ProtoVersion: wire.ProtoVersion`）。
7. accept 循环 + 一个 `sync.WaitGroup`；同时起一条 goroutine 监听「shell 退出」，
   退出后启动 `time.AfterFunc(ExitedTTL, 收摊)`。
8. 收摊：`eng.Close(id)`（杀进程组）→ `listener.Close()` → `lock.Release()` →
   `sessdir.Remove(root, id)` → 返回 nil。

**每条连接的处理（`serveConn`）：**

- 一条**独立的写 goroutine**，从一个 `chan []byte` 取已编好的帧写出去。
  **绝不允许两个 goroutine 直接往同一个 conn 写**——`wire.writeFrame` 保证了单帧是一次
  `Write`，但下行泵和 stat 应答是两个 goroutine，不串行就会交错成乱帧。
- 读循环：
  - `KindData` → `eng.Write(id, data)`（这是上行按键）
  - `CtrlAttach` → `eng.Attach(id, since)`；先回一帧 `attached`
    （带 `Since` / `Truncated` / `ProtoVersion`），再把 `att.Backlog` 作为**一个数据帧**发出，
    然后起下行泵：`for b := range att.Out { 发数据帧 }`；`Out` 关闭后发 `exit` 帧
    （`ExitCode` 取 `att.ExitCode()`，**nil 就是 nil**，不要填 0）。
  - `CtrlResize` → `att.Resize(cols, rows)`；还没 attach 就收到 resize 时忽略并记 Debug。
  - `CtrlStat` → 从 `eng.Get(id)` 取快照，回 `stat_resp`。
  - `CtrlKill` → 触发收摊。
  - `io.EOF` → 订阅者正常走了，记 Debug，`att.Detach()`，**不做别的**。
  - `io.ErrUnexpectedEOF` → 对端崩了，记 Warn，同样只 Detach。
- **连接关闭只 Detach，绝不 Close 会话**（Global Constraints 第 10 条）。

**日志**（本进程是 agentd 不在时唯一的证人，这几条必须有）：
启动（含 id/pid/cwd/shell）、绑 socket、每次 attach（含 since/backlog 字节数/当前订阅数）、
每次 detach（含原因：正常关闭 / 对端崩溃）、shell 退出（含退出码）、TTL 到点自退、
收到 kill、收摊完成。

- [ ] **Step 4: 写 `cmd/ptyhost.go`**

照 `cmd/shim.go` 逐字同构：

```go
// 本文件实现隐藏子命令 handoff _ptyhost：单个 PTY 会话的承载进程。
//
// 职责：解析 --spec，把控制权交给 hostproc.Run（阻塞到会话收摊）。
//
// 边界：
//   - 不做任何业务判断：全部逻辑在 hostproc.Run 里，本文件只是 cobra 包装
//   - 不面向用户：Hidden=true。它由 agentd 自己拉起，人手动跑没有意义
package cmd

import (
	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/spf13/cobra"
)

var ptyhostSpecPath string

var ptyhostCmd = &cobra.Command{
	Use:    "_ptyhost",
	Short:  "PTY 会话承载进程（内部使用）",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return hostproc.Run(ptyhostSpecPath)
	},
}

func init() {
	ptyhostCmd.Flags().StringVar(&ptyhostSpecPath, "spec", "", "spec.json 路径（必填）")
	_ = ptyhostCmd.MarkFlagRequired("spec")
	rootCmd.AddCommand(ptyhostCmd)
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/ptyhost/hostproc/ -v`
Expected: PASS（七个用例）

`TestSinceResumeAfterReconnect` 与 `TestDetachDoesNotKill` 是本 task 最承重的两条，
它们红了不要放过——那正是整个 A 要保证的东西。

- [ ] **Step 6: 两平台编译 + 全量**

Run: `go build ./... && GOOS=windows go build ./... && go test ./...`
Expected: 都过。`hostproc` 的测试带 `//go:build unix`，Windows 上不参与。

- [ ] **Step 7: 提交**

```bash
gofmt -l internal/ cmd/ | head
git add internal/ptyhost/hostproc/ cmd/ptyhost.go
git commit -m "feat(ptyhost): 会话承载进程与 handoff _ptyhost 子命令

先抢锁再开 shell（顺序反了会让第二个实例先 fork 出一个多余的 shell）；
每条连接一个订阅者，关连接只 Detach 绝不 Close 会话；shell 退出后守着
退出码与滚屏 24 小时。进程自己落 ptyhost.log——agentd 不在时只有它能作证。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: agentd 侧客户端——同样的六个方法，换成连 socket

**Files:**
- Create: `internal/ptyhost/client.go`（含客户端的 `AttachOps` 实现）
- Create: `internal/ptyhost/client_test.go`
- 注意：`attachment.go` 已在 Task 4 建好，本 task **只加一套 ops 实现**，不改那个壳

**Interfaces:**
- Produces（**与搬家前的 `Host` 逐字同签名**，这是 `pty_api.go` / `pty_ws.go` 不用改的前提）:
  - `func New(root, selfExe string, log *slog.Logger) *Host`
  - `func (h *Host) Supported() bool`
  - `func (h *Host) Open(opt OpenOptions) (Session, error)`
  - `func (h *Host) List() []Session`
  - `func (h *Host) Get(id string) (Session, bool)`
  - `func (h *Host) Write(id string, p []byte) error`
  - `func (h *Host) Close(id string) error`
  - `func (h *Host) Attach(id string, since uint64) (*Attachment, error)`
  - `func (h *Host) Adopt(entries []sessdir.Entry)`（启动时登记，Task 7 用）
  - `var ErrProtoMismatch = errors.New("会话由不兼容的版本托管")`
  - 一套不导出的客户端 `AttachOps`（`Detach` 关连接、`ExitCode` 读已记下的退出码、
    `Resize` 在本连接上发 resize 帧）

**`New` 的签名比原来多两个参数**（`root` 会话根目录、`selfExe` 自身可执行文件路径）。
这是**唯一**要改 `server.go` 的地方，`pty_api.go` / `pty_ws.go` 一行不动。

**连接策略（spec §4）：**

- **控制查询**（`List` / `Get`）：短连接，发 `stat`、读一帧、断开，1 秒超时。
  `List` 对多个会话**并发**问，否则 N 个会话就是 N 秒。
- **数据订阅**（`Attach`）：一条长连接，生命周期等于 `Attachment`。
- **`Write(id, p)`**：优先复用该会话**已有的**某条订阅连接（引擎的 `Write` 是会话级的，
  从哪条连接进去都到同一个 PTY）；一条都没有时开一条短连接发完就断。
  **不要为每次按键开连接**——那是每敲一个字符一次 connect。

- [ ] **Step 1: 写失败的测试**

创建 `internal/ptyhost/client_test.go`（`//go:build unix`）。用**真的** hostproc 进程做对端
（`hostproc.Run` 跑在 goroutine 里，同 Task 5 的 `startHost`），覆盖：

1. `Open` → 拉起进程 → `List()` 能列到它，`Session` 的 `BasePath` / `Shell` / `PID` 正确。
2. `Write` + `Attach`：写 `echo HANDOFF_MARK\n`，从 `att.Out` 读到回显。
3. `Attach` 两次（模拟两个浏览器）→ `stat` 报 `attached == 2`。
4. `Detach` 之后会话**还在**（`List` 仍列得到，能再 `Attach`）。
5. `Close` 之后会话没了（`List` 空，目录被清）。
6. **协议版本不认识**：手工把 `meta.json` 的 `proto_version` 改成 `99`，
   `Attach` 必须返回包装 `ErrProtoMismatch` 的错误，而 `List` 仍然**列得出这个会话**
   （标记不可接入）——列不出来用户就没有出口关掉它。
7. `Open` 时 socket 迟迟不出现（把 `selfExe` 指向 `/bin/true`，进程立刻退出）→
   3 秒内返回错误，且**不留残骸**（会话目录被清掉）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ptyhost/ -run TestClient`
Expected: FAIL

- [ ] **Step 3: 写实现**

`internal/ptyhost/client.go` 要点：

```go
// client.go —— agentd 侧的 Host：与搬家前逐字同签名的六个方法，实现换成连 socket。
//
// 职责：
//   - Open：建会话目录、写 spec、detached 拉起 handoff _ptyhost、等 socket 出现
//   - List / Get：短连接问 stat，拿活事实
//   - Write / Attach：走订阅长连接
//   - Close：发 kill 帧
//   - Adopt：把 agentd 启动时扫到的活会话登记进来
//
// 边界：
//   - **不认识 PTY**：它不开伪终端、不认识转义序列。真正的引擎在
//     internal/ptyhost/engine，跑在 ptyhost 进程里
//   - 不做启动时扫描：那是调用方（agentd）的事，扫描结果经 Adopt 交进来
//   - 不删还活着的会话目录
//
// 为什么方法签名一个都不改：pty_api.go 与 pty_ws.go 本来就只跟这六个方法打交道
//（它们的边界注释写着「不持有任何会话状态，全部转交 s.pty」）。保持签名，
// 这次搬家对它们就是不可见的。
```

**关键实现点，逐条：**

- `Open`：
  1. `id := uuid.NewString()`；`sessdir.CheckSockPath` → `sessdir.Create`
  2. 写 `spec.json`（0600）：`hostproc.Spec{Root, ID, BasePath, BaseKind, Cwd, Shell, Env, Cols, Rows}`
  3. `exec.Command(h.selfExe, "_ptyhost", "--spec", specPath)`，
     **detached**（`SysProcAttr{Setsid: true}`，与 `prochost` 同款；Windows 走各自的
     平台文件），`Start()` 后**不 `Wait`**——它要活得比 agentd 长
  4. 轮询等 socket 出现，上限 3 秒（10ms 一次）。超时则杀掉刚起的进程、
     `sessdir.Remove`、返回带真因的错误。**「不留残骸」是硬要求**：留下一个连不上的
     会话目录，下次启动扫描会把它当 broken，要人工处理
  5. 成功后短连接问一次 `stat`，组装并返回 `Session`
- `List`：对表里每个会话并发发 `stat`（`errgroup` 或手写 WaitGroup，1 秒超时）。
  **问不到的不要丢掉**——用 meta 里的静态事实 + 「活事实未知」返回。
  `Foreground` 问不到时按 `false` 返回（沿用 `Session.Foreground` 已有的纪律：
  读不到时「关掉它会打断什么」的答案就是「不会」）。
- `Attach`：
  1. 先查 `meta.ProtoVersion`；不等于 `wire.ProtoVersion` → 直接返回
     `fmt.Errorf("会话 %s 由 v%d 托管，本版（v%d）接不进来: %w", id, m.ProtoVersion, wire.ProtoVersion, ErrProtoMismatch)`。
     **在这里拦，而不是连上去再握手**：`meta.json` 里已经有版本了，多一次连接毫无意义
  2. `net.Dial("unix", sock)` → 发 `attach{since}` → 读首帧，必须是 `attached`
  3. 起一条 goroutine 读后续帧：数据帧 → 送进 `out` channel；`exit` 帧 → 记下退出码、
     `close(out)`；`io.EOF` / `io.ErrUnexpectedEOF` → 同样 `close(out)`
     （**`Out` 关闭 = 会话结束**，这正是 `pty_ws.go` 既有路径的理解，不要改这条语义）
  4. `Backlog` 从首帧之后的第一个数据帧取——**要与 `attached` 帧配对读完**再返回，
     否则调用方拿到的 `Backlog` 是空的、历史全走 `Out`，会与它「先灌 backlog 再进
     实时循环」的写法打架
- `Detach`：关连接，从该会话的连接表里摘掉自己。**绝不发 kill**。
- `Resize`：在自己那条连接上发 `resize` 帧。
- `Write`：从连接表里挑一条已有连接发数据帧；没有就开短连接发完即断。
- `Close`：短连接发 `kill` 帧，等对端关闭（1 秒），然后从表里摘掉。
  目录由 ptyhost 进程自己清（它清得比我们准——它知道 shell 真死了没有）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ptyhost/ -run TestClient -v`
Expected: PASS（七条）

- [ ] **Step 5: 全量与两平台编译**

Run: `go build ./... && GOOS=windows go build ./... && go test ./internal/ptyhost/...`
Expected: 都过

- [ ] **Step 6: 提交**

```bash
gofmt -l internal/ | head
git add internal/ptyhost/client.go internal/ptyhost/attachment.go internal/ptyhost/client_test.go
git commit -m "feat(ptyhost): agentd 侧客户端，六方法签名一字不改

Open 拉起 detached 的 _ptyhost 并等 socket（超时不留残骸）；List 并发问 stat；
Attach 前先按 meta.json 里的 proto_version 拦版本错配，不连上去再握手。
Detach 绝不发 kill。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: agentd 启动扫描、登记，与 `service stop` 收口

**Files:**
- Create: `internal/agentd/ptyreclaim.go`
- Create: `internal/agentd/ptyreclaim_test.go`
- Modify: `internal/agentd/shutdown.go`

**Interfaces:**
- Consumes: `sessdir.Scan` / `sessdir.Remove`；`Host.Adopt` / `Host.List` / `Host.Close`
- Produces:
  - `func (s *Server) reclaimPtySessions() error`
  - `func (s *Server) shutdownPtySessions(ctx context.Context)`

- [ ] **Step 1: 写失败的测试**

`internal/agentd/ptyreclaim_test.go` 覆盖四条：

1. **活的被登记**：造一个持锁 + meta 正常的会话目录 → `reclaimPtySessions` 之后
   `s.pty.List()` 里有它，且目录**还在**。
2. **死的被清掉**：造一个没人持锁的目录 → 之后目录不在了，`List()` 里没有它。
3. **broken 既不删也不登记**：造一个持锁但 meta 是垃圾的目录 → 目录**还在**、
   `List()` 里没有它、日志里有一条 Error。
   （断言日志用 `slog` 接一个 `bytes.Buffer`，照 `envforward_test.go` 的写法。）
4. **根目录不存在**（首次启动）→ 不报错，`List()` 为空。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestPtyReclaim`

- [ ] **Step 3: 写实现**

```go
// ptyreclaim.go —— agentd 启动时认领既有 PTY 会话，退出时收口。
//
// 职责：
//   - reclaimPtySessions：扫会话目录，活的登记进会话表，死的清掉，坏的留给人
//   - shutdownPtySessions：显式停止时逐个杀掉会话
//
// 边界：
//   - **启动时不连任何 socket**：只扫目录 + 试锁。启动路径不该被 N 次连接拖慢，
//     没人看的会话也不需要 agentd 在中间转发字节（spec §3）
//   - 不解释 broken：目录留着、进程不动，只记 Error 让人来看
//
// 为什么「显式 stop 一起停」而「崩溃/升级不停」：用户敲 handoff service stop 的
// 意图是「让这台机器上的 handoff 全停下来」，背着他留一堆 shell 是违背意图的。
// 升级走 handoff upgrade，那条明确算「重启」，会话保留——两者的区别是显式
// 表达出来的，不是猜的（spec §1.2）。
```

`reclaimPtySessions`：

```go
func (s *Server) reclaimPtySessions() error {
	root := s.ptyRoot()
	entries, err := sessdir.Scan(root)
	if err != nil {
		return fmt.Errorf("扫描 PTY 会话目录 %s: %w", root, err)
	}
	var live, cleaned, broken int
	var adopt []sessdir.Entry
	for _, e := range entries {
		switch e.State {
		case sessdir.StateLive:
			adopt = append(adopt, e)
			live++
		case sessdir.StateDead:
			if err := sessdir.Remove(root, e.ID); err != nil {
				s.log.Warn("清理已死的 PTY 会话目录失败", "session", e.ID, "err", err)
				continue
			}
			cleaned++
		case sessdir.StateBroken:
			// 有个进程活着而我们不知道它是什么。不删目录、不杀进程——
			// 盲杀一个说不清来历的进程是这套东西最不该做的事
			s.log.Error("PTY 会话目录异常，已跳过（进程可能仍在运行，需人工处理）",
				"session", e.ID, "dir", sessdir.Dir(root, e.ID), "err", e.Err)
			broken++
		}
	}
	s.pty.Adopt(adopt)
	s.log.Info("PTY 会话认领完成", "live", live, "cleaned", cleaned, "broken", broken)
	return nil
}
```

`shutdownPtySessions`：遍历 `s.pty.List()`，逐个 `s.pty.Close(id)`，整体等 **2 秒**；
到点无论结果直接返回。

```go
	// agentd 的退出不能被一个赖着不死的 shell 卡住。极端情况下可能留下一个没杀
	// 干净的 ptyhost——下次启动扫目录会发现它还活着并重新认领，不会变成孤儿。
```

在 `shutdown.go` 的停止流程里调它，位置在「停 executor」之后、「关 store」之前。

**接线**：在 `NewServer` 之后、开始服务之前调用 `reclaimPtySessions()`。失败**不阻断启动**
（记 Error 继续）——扫描失败不该让整个 agentd 起不来。

- [ ] **Step 4–6: 跑测试、全量、提交**

Run: `go test ./internal/agentd/ -run TestPtyReclaim -v` → PASS
Run: `go test ./...` → ok

```bash
gofmt -l internal/ | head
git add internal/agentd/ptyreclaim.go internal/agentd/ptyreclaim_test.go internal/agentd/shutdown.go
git commit -m "feat(agentd): 启动认领既有 PTY 会话，显式 stop 时收口

启动只扫目录+试锁，不连 socket；死的清、活的登记、坏的留给人（不删不杀）。
service stop 逐个 kill 并整体等 2 秒——agentd 的退出不能被赖着不死的 shell 卡住。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: 接线 server.go 与版本错配的界面降级

**Files:**
- Modify: `internal/agentd/server.go`（`pty` 字段类型、`ptyhost.New` 参数、调用 reclaim）
- Modify: `internal/agentd/pty_api.go`（**只加一处**：版本错配的标记）
- Modify: `internal/proto/pty.go`（`PtySession` 加一个字段）
- Modify: `internal/proto/contract_fixture_test.go` 与 `web/src/api/testdata/PtySession.json`
- Modify: `web/src/api/types.ts`、`web/src/app/workbench/TerminalTab.tsx`

- [ ] **Step 1: 接线 server.go**

把 Task 4 留下的过渡接线换掉：

```go
	pty *ptyhost.Host
```

```go
	exe, err := os.Executable()
	if err != nil {
		// 拿不到自身路径就起不了 ptyhost 进程。不 panic：PTY 只是控制台的一个
		// 功能，agentd 的主业（任务派发）不该被它拖垮。此时 Supported 仍为 true
		// 但 Open 会失败并给出真因
		log.Error("无法确定自身可执行文件路径，PTY 会话将无法创建", "err", err)
	}
	pty: ptyhost.New(filepath.Join(cfg.DataDir, "ptys"), exe, log),
```

并在启动流程里调 `reclaimPtySessions()`。

- [ ] **Step 2: 线格式加一个字段**

`internal/proto/pty.go` 的 `PtySession` 加：

```go
	// Incompatible 表示这个会话由一个协议不兼容的旧版本托管：进程还活着
	// （里面跑着的东西没白跑），但本版接不进去，只能关闭（spec §1.3）。
	//
	// **不带 omitempty**：false 是有意义的结论（「能接」），缺键会让前端分不清
	// 它和「这版服务端还不认识这个字段」。与 Foreground 同一条纪律。
	Incompatible bool `json:"incompatible"`
```

`pty_api.go` 的 `ptySessionView` 里把它填上（`Host.List()` 返回的 `Session` 加同名字段）。
**这是 `pty_api.go` 唯一的改动**。

刷新契约 fixture：`go test ./internal/proto/ -run TestContractFixtures -update`，
然后不带 `-update` 再跑一次确认钉死。

- [ ] **Step 3: 前端如实呈现**

`web/src/api/types.ts` 的 `PtySession` 加 `incompatible: boolean`。

`TerminalTab.tsx`：`incompatible` 的会话不建连、不重连，直接走已有的 `dead` 分支
（它已经有「重开一个终端」的出口了），文案改成服务端给的那句：

```tsx
// 版本不兼容不是网络问题，没有重连可等——直接进 dead 分支，
// 复用「重开一个终端」那个出口。用户的旧会话进程还活着，但这个界面接不进去，
// 能给的最好动作就是让他开一个新的
```

判断放在 Shell 恢复那一路（会话列表里就有 `incompatible`），**不要**等建连失败才发现。

- [ ] **Step 4: 全量测试与类型检查**

Run: `go test ./... && cd web && npm run typecheck && npm test`
Expected: 全绿

- [ ] **Step 5: 提交**

```bash
gofmt -l internal/ | head
git add -A
git commit -m "feat: agentd 接上 ptyhost 客户端，版本错配如实降级

PtySession 加 incompatible：进程还活着但本版接不进去，界面直接给「重开一个
终端」的出口，而不是让用户点进去撞一次建连失败。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 9: 跨进程验收用例——「杀掉 agentd 那一侧，滚屏不丢」

**这条是整个 A 的验收判据，必须自动化。** 前面每个 task 各自测的是自己那一层；
只有这条测的是「A 到底解决没解决那个问题」。

**Files:**
- Create: `internal/ptyhost/survive_test.go`（`//go:build unix`）

- [ ] **Step 1: 写测试**

```go
// survive_test.go —— A 的验收判据：agentd 那一侧整个消失再回来，会话与滚屏都还在。
//
// 与 client_test.go 的区别：那里测的是客户端各个方法对不对；这里测的是
// **把客户端整个丢掉、重新造一个**之后还能不能接上——那才是 agentd 重启的形状。
package ptyhost_test
```

用例主体：

1. 造 `root := t.TempDir()`，`h1 := ptyhost.New(root, selfExe, log)`
   （`selfExe` 用 `os.Executable()` 指向测试二进制不行——它不认识 `_ptyhost` 子命令。
   **改为在测试里用 `go build` 产出一份 handoff 二进制**，或者更省事：
   直接在 goroutine 里跑 `hostproc.Run`，`h1.Adopt` 登记它。
   **推荐后者**：本用例要验的是客户端与进程之间的协议与续传，不是「怎么 fork」，
   fork 那一段已由 Task 6 的 `Open` 用例覆盖。）
2. `att1, _ := h1.Attach(id, 0)`；`h1.Write(id, []byte("echo BEFORE\n"))`；
   从 `att1.Out` 读到 `BEFORE`。
3. **把 h1 整个丢掉**：`att1.Detach()`，然后不再使用 h1（模拟 agentd 进程没了）。
4. `entries, _ := sessdir.Scan(root)`——必须扫到**一条 StateLive**。
5. `h2 := ptyhost.New(root, selfExe, log)`；`h2.Adopt(entries)`；
   `h2.List()` 里有它，`PID` 与之前一致。
6. `att2, _ := h2.Attach(id, 0)`——`att2.Backlog` 里**必须含 `BEFORE`**。
   这是本 task 的核心断言：滚屏跨「agentd 重启」活下来了。
7. `h2.Write(id, []byte("echo AFTER\n"))`；从 `att2.Out` 读到 `AFTER`——
   会话不只是能看，还能继续用。
8. `h2.Close(id)`；确认目录被清、`Scan` 返回空。

- [ ] **Step 2: 跑，确认它真的在测东西**

Run: `go test ./internal/ptyhost/ -run TestSurvive -v`
Expected: PASS

**跑完之后做一次装饰性变异，确认这张网真的罩得住**：把 `hostproc` 里 `attach` 的
`since` 透传改成恒传 0 之外的值（比如恒传 `1<<40`），再跑本用例——它**必须变红**。
不变红说明断言没咬住 backlog，回去改断言。改完把变异撤销。

（这一步不是形式主义：反面断言与「取早了的判据」是稳定假绿的两个主要来源，
而本用例是 A 唯一的整体判据，它假绿等于整个 feature 没有网。）

- [ ] **Step 3: 提交**

```bash
git add internal/ptyhost/survive_test.go
git commit -m "test(ptyhost): A 的验收判据——agentd 那一侧消失再回来，滚屏不丢

客户端整个丢掉重造，Scan 认领后 Attach 的 backlog 里必须还有断线前的输出。
已用装饰性变异确认这张网咬得住。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 10: 排除 ptyhost 对机器级进程压力告警的干扰

Task 1 已确认：`resource_pressure` 按当前 uid 的全机进程枚举计数，ptyhost 会让
executor 派发的机器级告警虚高；`task_proc_pressure` 只沿任务足迹统计，不受影响。

**Files:**
- Modify: `internal/prochost/procenum*.go` 或其调用层（以实现时读码确定最小改动点）
- Create/Modify: 针对 ptyhost 排除口径的单元测试

- [ ] **行为规格**
  - 只从机器级 `resource_pressure` 的计数中排除 ptyhost；不得改变 executor 树的
    RLIMIT_NPROC、`task_proc_pressure` 的任务足迹口径或普通用户进程的计数。
  - 判定必须使用可验证的 ptyhost 身份凭据（不能按进程名或模糊祖先关系猜测）；
    无法确认身份时宁可计入，不得漏报真实压力。
  - 覆盖一个真实/测试 ptyhost 被排除、普通同 uid 进程仍计入、身份不明仍计入的测试。
  - `resource_pressure` 的 `used` 与前端/事件字段保持既有含义，日志说明排除数量。

- [ ] **验证**
  - `go test ./internal/prochost/... ./internal/agentd/`
  - `go test ./...`
  - `GOOS=windows go build ./...`

- [ ] **提交**

```bash
gofmt -l internal/ | head
git add internal/prochost/ internal/agentd/ docs/superpowers/plans/2026-08-20-pty-out-of-process.md
git commit -m "fix(prochost): 排除 ptyhost 对机器级压力告警的干扰

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 11: 真机走查与收尾

> **本 task 由审核者在本地执行，不派发。**
> 它要反复重启 agentd、开桌面端、看终端里的滚屏，属于交互式真机操作；
> 派发出去的执行者既没有桌面环境，也不该被要求重启一台生产机上的 agentd。
> 执行者做完 Task 9 即交回。

- [ ] **Step 1: 起隔离实例**

**不要重启本机常驻的那个 agentd**——launchd 会用旧二进制把它拉回来。
起一个独立 DataDir + 独立端口的实例验收。

- [ ] **Step 2: 走查清单**

- [ ] 开两个终端，各跑一个长命令（`ping`、`tail -f`）
- [ ] `kill -9` 掉 agentd，确认两个 shell 进程**还活着**（`ps` 看 pid）
- [ ] 重新起 agentd，日志里有「PTY 会话认领完成 live=2 cleaned=0 broken=0」
- [ ] 刷新控制台：两个终端都在，**滚屏内容还在**，长命令还在刷
- [ ] 往终端里敲字，能继续用
- [ ] `handoff service stop`：两个 shell 进程**都没了**，会话目录清空
- [ ] shell 里 `exit`：tab 显示退出码，ptyhost 进程**仍在**（守着最后那屏）
- [ ] 点 × 关掉：进程没了，目录清了
- [ ] 造一个 broken 目录（持锁 + 垃圾 meta），重启 agentd：目录**还在**、
      日志里有一条 Error、进程**没被杀**
- [ ] 跨机：远程机器上的终端照常可用（A 不碰反代层，这条是回归确认）

- [ ] **Step 3: 目录对账**

```bash
ls -la <隔离实例 DataDir>/ptys/
cat <隔离实例 DataDir>/ptys/*/meta.json
tail -30 <隔离实例 DataDir>/ptys/*/ptyhost.log
```

- [ ] **Step 4: CHANGELOG 与提交**

在 `[Unreleased]` 下记：PTY 会话托管到 agentd 之外的独立进程，agentd 崩溃或升级
重启后终端与滚屏都还在；`service stop` 一起停；协议不兼容时如实降级。

---

## 自查清单（每个 task 完成后逐条确认）

| 检查项 | 要求 |
|--------|------|
| 测试先红后绿 | 每个 task 都先跑到失败，再实现，再跑到通过 |
| 文件头注释 | 每个新建文件写了职责和**边界**（它不做什么） |
| 导出注释 | 每个导出符号写了参数、返回、注意事项 |
| 中文「为什么」 | 非显然的判断说明了理由，不复述代码 |
| 日志 | ptyhost 进程自己落 `ptyhost.log`；关键节点、错误分支、**成功路径**都有 |
| 无 print | 没有 `fmt.Printf` |
| 两个信号没合并 | 断开订阅 ≠ 杀会话（Global Constraints 第 10 条） |
| 三态没塌成两态 | `StateBroken` 既不删也不杀 |
| 退出码三态 | `nil` 没有被 0 冒充 |
| gofmt | `gofmt -l internal/ cmd/` 输出为空 |
| 两平台编译 | `go build ./...` 与 `GOOS=windows go build ./...` 都过 |
| 全量测试 | `go test ./...` 全绿 |
| 范围 | 没改本计划之外的文件；`forward_ws.go`（跨机反代）一行未动 |
