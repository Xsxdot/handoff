# 需求 B 实现计划 · 工作台自定义启动项（B1~B5）

- 上游：[spec §B](2026-08-22-executor-timing-and-custom-launchers-design.md)、[契约冻结物](2026-08-22-custom-launchers-contract.md)（`ce79d234`）、[拆解](2026-08-22-custom-launchers-breakdown.md)（四个岔口已拍板）
- 本计划范围：**B1~B5 五张实现卡**。**B6（真机验收）不在派发范围**，见文末真机清单
- 基线提交：本文提交前的 HEAD（`74fdb762` 之后）

**拍板结论（写死，不得重议）**：Q1=(c) 另开 `onPickLauncher` 回调；Q2=(b) Shell 层另发
`GET /api/launchers` 并按项目树节奏刷新（**不扩 `ProjectTreeResp`**）；Q3=(a) tab 标题
用启动项名字；Q4=(a) 不回显标记，**写进终端的内容 = 命令原文 + `\n`，不多不少**。

---

## 0. 基线核验（本轮实测）

```
$ go build ./...
（无输出）

$ go test ./internal/agentd/ ./internal/ptyhost/... ./internal/launcher/
ok  	github.com/Xsxdot/handoff/internal/agentd            79.947s
ok  	github.com/Xsxdot/handoff/internal/ptyhost           (cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/engine    (cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/hostproc  (cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/sessdir   (cached)
ok  	github.com/Xsxdot/handoff/internal/ptyhost/wire      (cached)
ok  	github.com/Xsxdot/handoff/internal/launcher          (cached)

$ cd web && npx tsc -b
退出码 0
```

`internal/agentd` 单包 80 秒是**它的正常耗时**，不是本次引入的。

---

## 1. 代码与库行为事实（每条带出处；凭印象即 plan failure）

### 1.1 Ticket 0 已交付、本计划直接消费的件

| 符号 | 出处 | 契约 |
|---|---|---|
| `launcher.Item{Name,EnvFile,Command}` | `internal/launcher/launcher.go:44` | **落盘形态**，刻意不带 `EnvMissing`（派生字段落盘就有两个真相） |
| `launcher.Load(dataDir) ([]Item, error)` | `launcher.go:63` | 文件不存在 → `(nil, nil)`，那是正常起点；**Load 不做校验** |
| `launcher.Save(dataDir, list) error` | `launcher.go:93` | 先校验后落盘；0600/0700 |
| `launcher.Validate(list) error` | `launcher.go:135` | 四条规则，错误包 `ErrInvalid`，文本可直接当 400 响应体 |
| `launcher.Dir(dataDir) string` | `launcher.go:38` | 路径知识只此一处 |
| `proto.Launcher{Name,EnvFile,Command,EnvMissing}` | `internal/proto/launcher.go` | **`EnvMissing` 不带 `omitempty`**（前端靠「缺键」与「false」的区别判断服务端认不认识它） |
| `proto.LaunchersResp` / `proto.LaunchersReq` | 同上 | 整段替换 |
| `envfile.LoadFile(dir, name, log) ([]string, error)` | `internal/envfile/resolver.go:106` | 解析一份 env 文件成 `KEY=VAL` 切片；只打 key 不打值 |
| `envfile.Dir(dataDir) string` | `resolver.go:24` | `<DataDir>/env` |
| `envfile.Read(dir, name) (content, sha, size, err)` | `internal/envfile/files.go:91` | **存在性检查就用它**，`handleEnvMapping` 的既有做法 |
| `envfile.ErrBadName` | `files.go:33` | 文件名不是纯文件名，调用方答 400 |
| `ptyhost.OpenOptions.InitCommand` | `internal/ptyhost/types.go:44` | 不含换行（实现补）；空 = 不写；**不进 argv** |
| `proto.CreatePtySessionReq.EnvFile/InitCommand` | `internal/proto/pty.go` | 均 `omitempty` |
| `proto.StatusResp.LaunchersSupported *bool` | `internal/proto/status.go` | **nil 按不支持处置**（与 `PtySupported` 相反） |
| `proto.Machine.LaunchersSupported *bool` | `internal/proto/projects.go` | 同上 |
| TS `Launcher` / `LaunchersResp` / `LaunchersReq` / `CreatePtySessionReq.env_file,init_command` / `Machine.launchers_supported` | `web/src/api/types.ts` | 契约夹具已在 contract 节点落地（`internal/proto/contract_fixture_test.go` + `web/src/api/contract.test.ts`），**本计划不重复造夹具** |

### 1.2 库行为（本轮实测，不引记忆）

**`exec.Cmd.Env` 里同名变量重复时，最后一个生效。** 实测程序与输出：

```go
c := exec.Command("/bin/sh", "-c", "printf %s \"$FOO\"")
c.Env = []string{"FOO=first", "FOO=second"}
out, _ := c.Output()   // => "second"
```
```
FOO="second"   (go1.26.1 darwin/arm64)
```

**这条事实是叠加顺序的全部依据**：契约要求「`sessionEnv()` 在前、启动项 env 文件在后，
后者覆盖」，只有在「最后一个生效」的前提下这句话才成立。反过来就是选文件这个动作
在最需要它的场景下静默失效。

**这条路径确实经过 `exec.Cmd.Start`**：`pty.StartWithSize`（`creack/pty@v1.1.24/start.go:18`）
→ `StartWithAttrs`（`run.go`）→ `c.Start()`（`run.go:52`）。不是自己 fork/exec，
所以上面的 dedup 语义适用。

### 1.3 d_host（`internal/ptyhost/engine`）

- `Engine.Open`（`engine.go:78`）：`!ptySupported` 时**第一行就返回** `ErrNotSupported`；
  之后 `startPty` → 建 `session` → 入表 → `go h.pump(s)` + `go h.reap(s)`（`:110-111`）。
- `pump`（`engine.go:123`）：`s.f.Read(b)`，`n > 0` 时 `s.broadcast(b[:n])`。
  **这是全包唯一看得见「shell 有动静了」的地方。**
- `Engine.Write(id, p)`（`engine.go:222`）：查表 → 判 `exited` → `s.f.Write(p)`。
  失败日志只记 `"bytes", len(p)`，**不记内容**——启动命令走它天然满足「命令原文不进日志」。
- `startPty`（`platform_unix.go:44`）：`exec.Command(shell, "-l")`，即 login shell。
  **改成 `sh -lc cmd` 会让会话在命令退出时结束**，与需求相悖。
- `platform_other.go`（`//go:build !unix`）：`ptySupported = false`，`startPty` 直接返回
  `ErrNotSupported`。**结论：非 unix 侧无需改动**——`Open` 在 `!ptySupported` 时压根
  走不到写命令那一步。这是一条要写进验收的结论，不是遗漏。

### 1.4 d_controlplane（`internal/agentd`）

- 路由注册在 `server.go:429-484` 一带（`api.HandleFunc("GET /api/env", ...)` 那一片），
  文件头 `:396-401` 有一份端点清单注释，**新增端点要同时补进那份清单**。
- `handleEnvMapping`（`env.go:287`）是 B2 的逐条原型：`forwardIfRequested` 第一行 →
  解析体 → 逐条校验并**在错误文本里点名是哪一条** → 落盘 → **调 GET handler 回最新状态**。
- `sessionEnv()`（`pty_api.go:42`）：`os.Environ()` + `TERM`/`COLORTERM` + `env_forward` 解析。
- `handleCreatePtySession`（`pty_api.go:100`）：`forwardIfRequested` → 解析 → `resolvePtyBase`
  → 取 `$SHELL`（空则 `/bin/sh`）→ **单一 `s.pty.Open(...)` 调用点**（`:125`）。
  接线爆炸半径 = 1。
- 能力位三处写入端：
  1. `server.go:670-673` 一带（`ptyOK := s.pty.Supported(); resp.PtySupported = &ptyOK`）；
  2. `machines.go` 的 `localMachine`，`fillFromStatus` 之后那段「本机能力位就地填」
     （`ptyOK := s.pty.Supported(); m.PtySupported = &ptyOK`）；
  3. `machines.go` 的 `fillFromStatus`，末尾「能力位原样搬运，包括 nil」那三行。
- **`forwardIfRequested` 对新端点零改动**：它原样转发请求体，跨机自动可用。

### 1.5 d_web

- `useMachineCaps`（`web/src/app/data/useMachineCaps.ts`）：**只在加载时拉一次，刻意不轮询**
  （「能力位是平台属性，一台机器不会跑着跑着就不支持 PTY 了」）。三态查询函数返回
  `boolean | null`，`null` = 不知道。**`launchers` 要加进这里**（它是能力位）；
  **启动项列表不加进这里**（它会变，见下条）。
- `usePoll(fetcher, intervalMs, {enabled})`（`web/src/app/data/usePoll.ts`）：立即首拉 +
  定时续拉，`document.hidden` 停表，401 落终止态。**Q2(b) 的载体就是它。**
- `ptyNote(machine)`（`Shell.tsx:214`）：能力三态翻成人话，**`null` 一律放行**。
  启动项的能力门**方向相反**（`!== true` 即不展示），这是本需求最容易写错的一行。
- `pickItemsFor(base, terminalUnavailable)`（`BlankTab.tsx:59`）：两条过滤叠加
  （home 只留终端 / 终端不可用则摘掉）。导出正是为了让 `+` 菜单
  （`TabBar.tsx:84`）与面板用**同一份**判断。
- `PickKind`（`BlankTab.tsx:20`）：三个字面量的闭集；`hotkeyOf`（`:41`）、
  `pick`/`newIn`/`startFromEmpty`（`WorkbenchPage.tsx:82,108,136`）三处在 switch 它。
- `ptyBase(base, rel)`（`TerminalTab.tsx:59`）：**新字段在这里汇合**。它对
  `rel` 为空时返回的对象与历史形态逐字节一致——新字段要沿用同一条纪律。
- `MachineDetail.tsx:71-73`：`<MachineDiscipline/> <MachineEnv/> <MachineExecutor/>`
  三块并列，**新块加在这里**。`MachineEnv.tsx` 是形态与尺寸参照。
- `client.ts:363-406`：`fetchEnv` / `putEnvMapping` 一族的写法（`machineQuery(machine)`、
  `request<T>` / `putJSON<T>`）。

---

## Task 1 · B1：`ptyhost` 的 `InitCommand`（d_host · 边界型）

**Interfaces**
- Consumes：`ptyhost.OpenOptions.InitCommand`（已冻结）
- Produces：`engine` 包内 `initCommandReadyWait` 常量、`session.firstOut` 信号、
  `(*Engine).writeInitCommand`。**对外签名零变化**（`Open` 的入参结构体已含该字段）。

### 1.1 先写失败测试

新建 `internal/ptyhost/engine/initcmd_test.go`：

```go
//go:build unix

// initcmd_test.go —— OpenOptions.InitCommand 的行为锁。
//
// 职责：钉住三件事——不给命令时行为不变、给了命令时首字节即写、
// shell 一直不出声时 3s 兜底照样写。
//
// 边界：不验命令在真实 login shell 里的语义（那是真机清单第 2 条），
// 只验「字节有没有按时写进 PTY 输入」。用假 shell 把「什么时候出第一个字节」
// 变成测试能控制的量。
package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/engine"
)

// fakeShell 写一个可执行的假 shell 并返回其路径。
//
// body 之后统一接一个 read 回显循环：这样「命令有没有被写进去」可以通过
// 会话输出观察到，而 body 决定 shell 在**收到输入之前**出不出声——那正是
// 首字节路径与兜底路径的分界。
//
// 假 shell 会拿到一个 `-l` 参数（startPty 起的是 login shell），脚本忽略它。
func fakeShell(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-shell")
	script := "#!/bin/sh\n" + body + "\nwhile IFS= read -r line; do echo \"GOT:$line\"; done\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatalf("写假 shell: %v", err)
	}
	return p
}

// openAndCollect 开一个会话并订阅它，返回「等一行含 want 的输出」的函数。
//
// testHost 与 Attach 都是包内既有测试的现成写法（engine_test.go:17,64），
// 不另造辅助。
func openAndCollect(t *testing.T, opt ptyhost.OpenOptions) (*engine.Engine, ptyhost.Session, func(want string, within time.Duration) bool) {
	t.Helper()
	h := testHost(t)
	sess, err := h.Open(opt)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = h.Close(sess.ID) })
	a, err := h.Attach(sess.ID, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(a.Detach)
	var sb strings.Builder
	sb.Write(a.Backlog) // 订阅前已经产出的字节也算数
	wait := func(want string, within time.Duration) bool {
		if strings.Contains(sb.String(), want) {
			return true
		}
		deadline := time.After(within)
		for {
			select {
			case b, ok := <-a.Out:
				if !ok {
					return strings.Contains(sb.String(), want)
				}
				sb.Write(b)
				if strings.Contains(sb.String(), want) {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	return h, sess, wait
}

// TestInitCommandEmptyKeepsOldBehaviour 是兼容性守卫：不给命令时什么都不写。
//
// 反向断言必须配正面断言（下面两个用例就是），单独一条「没有 GOT:」在
// 写入路径整个被删掉之后照样绿。
func TestInitCommandEmptyKeepsOldBehaviour(t *testing.T) {
	sh := fakeShell(t, "printf 'READY\\n'")
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(),
	})
	if !wait("READY", 5*time.Second) {
		t.Fatal("假 shell 的 banner 没出现，用例前提不成立")
	}
	if wait("GOT:", 1500*time.Millisecond) {
		t.Fatal("InitCommand 为空时不得向 PTY 写入任何东西")
	}
}

// TestInitCommandWrittenOnFirstByte 钉住首字节路径：banner 一出就写，
// 远早于 3s 兜底。
func TestInitCommandWrittenOnFirstByte(t *testing.T) {
	sh := fakeShell(t, "printf 'READY\\n'")
	start := time.Now()
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(), InitCommand: "echo hello",
	})
	if !wait("GOT:echo hello", 5*time.Second) {
		t.Fatal("首字节到达后应写入启动命令")
	}
	// 判据的要害在**上界**：走了兜底路径也会「最终写入」，只有 <2s 才证明
	// 它走的是首字节路径。initCommandReadyWait 是 3s。
	if el := time.Since(start); el >= 2*time.Second {
		t.Errorf("首字节路径不应等到兜底：耗时 %v", el)
	}
}

// TestInitCommandFallbackWrites 钉住「超时不是失败」：shell 一直不出声，
// 3s 后照样写。
//
// 下界断言（>=2.5s）而不是上界：上界在慢机器上会偶发翻红，而偶发红会被当
// 噪音忽略，那条判据就实际失效了。下界证明的恰恰是「它确实等过」。
func TestInitCommandFallbackWrites(t *testing.T) {
	sh := fakeShell(t, "") // 收到输入之前一个字节都不出
	start := time.Now()
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(), InitCommand: "echo hello",
	})
	if !wait("GOT:echo hello", 15*time.Second) {
		t.Fatal("shell 不出声时也必须在兜底到点后写入启动命令")
	}
	if el := time.Since(start); el < 2500*time.Millisecond {
		t.Errorf("兜底路径不该提前触发：耗时 %v", el)
	}
}

// TestInitCommandWritesExactlyCommandPlusNewline 钉住 Q4(a) 的拍板：
// 写进去的就是命令原文 + \n，不带任何前缀标记。
//
// 这条判据脆弱是**有意的**：Q4 是一个「反过来写不会有任何测试变红」的裁决，
// 给它一条自己的判据，正是为了让后人的「顺手加个标记」撞红而不是无声通过。
func TestInitCommandWritesExactlyCommandPlusNewline(t *testing.T) {
	sh := fakeShell(t, "printf 'READY\\n'")
	_, _, wait := openAndCollect(t, ptyhost.OpenOptions{
		Shell: sh, BasePath: t.TempDir(), BaseKind: "workspace",
		Env: os.Environ(), InitCommand: "echo hello",
	})
	if !wait("GOT:echo hello", 5*time.Second) {
		t.Fatal("应写入启动命令")
	}
	// 回显循环按行读：收到的整行必须**恰好**是命令原文。带前缀标记时
	// 这里会是 "GOT:▶ 跑测试" 之类，对不上。
	if wait("GOT:echo hello\r\nGOT:", 800*time.Millisecond) {
		t.Fatal("只应写入一行，实际写了不止一行")
	}
}
```

> **测试包是 `engine_test`（外部测试包），不是 `engine`**——包内既有测试
> （`engine_test.go:3`）就是这么写的，且本文件只用公开 API，没有理由破例。
> `testHost(t)` 是那个文件里现成的辅助（`engine_test.go:17`），**不要新造**。
> 订阅用 `h.Attach(id, since) (*ptyhost.Attachment, error)`（`engine.go:279`），
> 增量在 `a.Out`、订阅前的存量在 `a.Backlog`——**两者都要收**，否则 banner
> 在 Attach 之前就吐完了会漏判。

跑红：

```bash
go test ./internal/ptyhost/engine/ -run TestInitCommand 2>&1 | tail -20
```

预期四条全红（`InitCommand` 目前无人消费，`GOT:` 永不出现；空命令那条会**假绿**
——这是正常的，它是守卫不是新功能）。

### 1.2 最小实现

(a) `internal/ptyhost/engine/engine.go` 的常量区（`:40` 一带，`termGrace` 旁）：

```go
	// initCommandReadyWait 是等 shell 出第一个字节的上限。
	//
	// 到点仍没动静就照写不误：内核的 PTY 输入缓冲一直在，字节不会丢，
	// 最坏情况只是命令排在 shell 的 rc 链之后被读到。**超时不是失败**——
	// 把它当失败就意味着「rc 链慢的机器上启动项静默不工作」。
	initCommandReadyWait = 3 * time.Second
```

(b) `session` 结构体（`engine.go:66` 一带）补两个字段：

```go
	// firstOut 在 shell 吐出第一个字节时关闭，是「shell 就绪」的唯一观测点。
	// 只有带 InitCommand 的会话才有人等它；不带的会话也照常关，代价是一次
	// sync.Once（比在 pump 的热路径上加一个 if 判空更简单）。
	firstOut     chan struct{}
	firstOutOnce sync.Once
```

(c) `Open` 里造 `session` 时补 `firstOut: make(chan struct{})`；在
`go h.pump(s)` / `go h.reap(s)` **之后**、`h.log.Info("终端会话已创建", ...)` 之前插入：

```go
	// 启动命令走 PTY **输入**，不进 argv：argv 会把 login shell 变成非交互
	// shell，命令退出即会话结束（见 OpenOptions.InitCommand 的注释）。
	// 另起 goroutine 是因为它要等 shell 就绪，而 Open 不能为此阻塞——
	// 阻塞会让建会话的 HTTP 请求挂最多 3 秒，一个 3 秒的空白 tab 比
	// 极小概率的输入交错更糟（拆解 §6.1 的处置）。
	if opt.InitCommand != "" {
		go h.writeInitCommand(s, opt.InitCommand)
	}
```

(d) `pump`（`:123`）的 `n > 0` 分支补一行：

```go
		if n > 0 {
			s.broadcast(b[:n])
			// 首字节即「shell 就绪」。放在 broadcast 之后：订阅者先看到输出，
			// 启动命令再写进去，顺序与人肉眼看到提示符再敲字一致
			s.firstOutOnce.Do(func() { close(s.firstOut) })
		}
```

(e) 新增（放在 `pump` 之后）：

```go
// writeInitCommand 等 shell 就绪后把启动命令写进 PTY 输入。
//
// 参数：s 为已入表的会话；cmd 为命令原文（不含换行，本函数补）。
//
// 就绪判据：**首字节输出 或 initCommandReadyWait 到点，以先到者为准**。
// 前者是真实就绪信号，后者保证「rc 链一直不出声」的 shell 也不会永远等下去。
//
// 注意：
//   - 写入内容恰好是 `cmd + "\n"`，**不加任何前缀标记**（Q4 拍板 (a)：
//     用户要的是「像人亲手敲进去一样」，多一行 handoff 自己的标记既破坏这个
//     错觉，那行文本还会混进滚动历史被 Ctrl-R 搜到）
//   - **命令原文绝不进日志**：启动项的命令可能含凭据（`API_KEY=xxx cmd`
//     是常见写法）。失败只记会话 id 与错误
//   - 会话在这 0~3 秒内被关掉是正常的：Write 会返回「会话不存在/已退出」，
//     按 Debug 记，不是告警
func (h *Engine) writeInitCommand(s *session, cmd string) {
	select {
	case <-s.firstOut:
	case <-time.After(initCommandReadyWait):
		h.log.Debug("等 shell 首字节超时，按兜底路径写入启动命令",
			"session", s.meta.ID, "wait", initCommandReadyWait)
	}
	if err := h.Write(s.meta.ID, []byte(cmd+"\n")); err != nil {
		// 不带 cmd：命令原文可能含凭据
		h.log.Debug("写入启动命令未成功（会话可能已关闭），终端不受影响",
			"session", s.meta.ID, "cause", err)
		return
	}
	h.log.Info("启动命令已写入终端", "session", s.meta.ID, "bytes", len(cmd)+1)
}
```

(f) import 补 `sync`（若尚未引入）。

### 1.3 跑绿

```bash
go test ./internal/ptyhost/engine/ 2>&1 | tail -20
```

**测试范围声明（最小化）**：只跑 `./internal/ptyhost/engine/`。本 task 不碰 agentd、
不碰 wire/sessdir/hostproc。四条新用例里有一条要真等 3 秒，包耗时会涨几秒，属正常。

### 1.4 日志

已内联：兜底触发 Debug、写入失败 Debug、写入成功 Info（只记字节数）。
**没有任何一条带命令原文**——这是本 task 的一条硬纪律，实现完自查一遍
`grep -n "cmd" internal/ptyhost/engine/engine.go`，确认 `cmd` 只出现在
`len(cmd)+1` 与 `[]byte(cmd+"\n")` 里，没进过任何日志键值。

### 1.5 注释

已内联：`initCommandReadyWait`（为什么超时不是失败）、`session.firstOut`（唯一观测点）、
`Open` 里那段（为什么不进 argv、为什么另起 goroutine）、`pump` 那一行（为什么在
broadcast 之后）、`writeInitCommand`（就绪判据 + 三条注意）。新文件 `initcmd_test.go`
有文件头注释。

### 1.6 平台结论（写进 ledger，不是代码）

`platform_other.go`（`//go:build !unix`）**无需改动**，理由：`Open` 在
`!ptySupported` 时第一行就返回 `ErrNotSupported`，走不到写命令那一步。
新测试文件带 `//go:build unix`。核验：

```bash
GOOS=windows go build ./... && echo "windows 构建通过"
```

### 1.7 提交

```bash
gofmt -l internal/ptyhost/ && go test ./internal/ptyhost/engine/ && git add -A && git commit -m "feat(ptyhost): 支持会话就绪后写入启动命令"
```

---

## Task 2 · B2：`/api/launchers` 的 CRUD（d_controlplane · 逻辑型）

**Interfaces**
- Consumes：`launcher.Load/Save/Validate`、`envfile.Read`、`envfile.Dir`、
  `proto.Launcher/LaunchersResp/LaunchersReq`、`(*Server).forwardIfRequested`、
  `(*Server).conf()`、`writeJSON`
- Produces：`(*Server).handleLaunchersGet`、`(*Server).handleLaunchersPut`、
  包内 `toProtoLaunchers(dir string, list []launcher.Item) []proto.Launcher`

**与 B1 无依赖，可先做。**

### 2.1 先写失败测试

新建 `internal/agentd/launchers_api_test.go`。按包内既有 HTTP 测试的搭台方式写
（**动手前 `grep -n "func newTestServer\|httptest.NewRequest" internal/agentd/*_test.go | head`
看包里现成的辅助叫什么，用它，不要新造**）。用例清单：

| 用例 | 判据 |
|---|---|
| `TestLaunchersPutRejectsEmptyName` | 400，且响应体**点名是第几条** |
| `TestLaunchersPutRejectsDuplicateName` | 400，响应体含那个重复的名字 |
| `TestLaunchersPutRejectsBothEmpty` | 400，响应体含该条的名字 |
| `TestLaunchersPutRejectsSeparatorInEnvFile` | 400，响应体含「不能含路径分隔符」 |
| `TestLaunchersPutRejectsMissingEnvFile` | 400，响应体含**该启动项的名字**与文件名 |
| `TestLaunchersPutReturnsLatestList` | 200，响应体就是保存后的 `LaunchersResp`（界面直接拿它刷新） |
| `TestLaunchersGetEnvMissingBothWays` | **成对断言**：指向存在的文件 → `env_missing=false`；指向不存在的 → `true` |
| `TestLaunchersPutIgnoresClientEnvMissing` | PUT 送 `env_missing:true`（文件真实存在），GET 回来是 `false` |
| `TestLaunchersGetOnFreshDataDir` | 没有 `launchers.json` 时返回**空列表**而不是 500 |
| `TestLaunchersPutDoesNotLogCommand` | **反向断言**：把 logger 接到一个 buffer，PUT 一条 `Command: "SECRET_TOKEN=abc deploy.sh"`，断言 buffer 里**不含** `SECRET_TOKEN`；**配一条正面断言**——buffer 里含条数（证明这条日志确实打了，不是整条日志没打导致的假绿） |

关键用例的骨架（其余照此形态）：

```go
// TestLaunchersGetEnvMissingBothWays 成对断言 env_missing 的两个方向。
//
// 为什么必须成对：只断言 true 那一侧的话，把 EnvMissing 写成常量 true
// 也能全绿；只断言 false 那一侧同理。派生字段的判据天然要成对。
func TestLaunchersGetEnvMissingBothWays(t *testing.T) {
	srv, dataDir := newLauncherTestServer(t)
	// 造一份真实存在的 env 文件
	envDir := envfile.Dir(dataDir)
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatalf("建 env 目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "real.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("写 env 文件: %v", err)
	}
	// 直接落盘一份含「已失踪文件」的配置：PUT 会拒绝它，而这个现场必须能读出来
	// ——磁盘上就是可能存着昨天还在、今天被删了的引用
	if err := os.WriteFile(launcher.Dir(dataDir), []byte(
		`[{"name":"在","env_file":"real.env"},{"name":"不在","env_file":"gone.env"}]`), 0o600); err != nil {
		t.Fatalf("写启动项配置: %v", err)
	}

	var resp proto.LaunchersResp
	doJSON(t, srv, http.MethodGet, "/api/launchers", nil, http.StatusOK, &resp)
	byName := map[string]proto.Launcher{}
	for _, l := range resp.Launchers {
		byName[l.Name] = l
	}
	if byName["在"].EnvMissing {
		t.Error("指向存在的 env 文件时 env_missing 应为 false")
	}
	if !byName["不在"].EnvMissing {
		t.Error("指向不存在的 env 文件时 env_missing 应为 true")
	}
}

// TestLaunchersPutDoesNotLogCommand 钉住「命令原文不进日志」。
//
// 反向断言（不含 SECRET）单独存在是稳定假绿的温床：整条日志被删掉它照样绿。
// 故同时断言这条日志确实打了（含条数），两条一起才有意义。
func TestLaunchersPutDoesNotLogCommand(t *testing.T) {
	var buf bytes.Buffer
	srv, _ := newLauncherTestServerWithLog(t, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	req := proto.LaunchersReq{Launchers: []proto.Launcher{
		{Name: "部署", Command: "SECRET_TOKEN=abc deploy.sh"},
	}}
	doJSON(t, srv, http.MethodPut, "/api/launchers", req, http.StatusOK, nil)

	logged := buf.String()
	if strings.Contains(logged, "SECRET_TOKEN") || strings.Contains(logged, "deploy.sh") {
		t.Fatal("命令原文不得进日志")
	}
	if !strings.Contains(logged, "启动项已保存") {
		t.Fatal("保存日志根本没打——上面那条『不含密钥』的断言因此没有意义")
	}
}
```

跑红：

```bash
go test ./internal/agentd/ -run TestLaunchers 2>&1 | tail -20
```

预期编译失败（handler 不存在）。

### 2.2 最小实现

新建 `internal/agentd/launchers_api.go`：

```go
// launchers_api.go —— 工作台自定义启动项的配置面（GET/PUT /api/launchers）。
//
// 职责：
//   - GET：读该机启动项列表，并**现算** env_missing
//   - PUT：整段替换，保存前一次性跑完全部校验，成功后回最新列表
//
// 边界：
//   - 不落盘、不校验规则本身——那归 internal/launcher（纯函数，可穷举测试）
//   - 不解析 env 文件内容，只查存在性（envfile.Read）
//   - 跨机由 forwardIfRequested 原样转发，本文件不认识 machine 参数
//
// 日志纪律：**命令原文绝不进日志**。启动项的 Command 可能含凭据
//（`API_KEY=xxx some-cmd` 是常见写法），只记条数与「有几条带命令」。
//
// 形态照 env.go 的 handleEnvGet / handleEnvMapping：同一个心智模型
//（一个配置面一个文件、整段替换、保存时一次性校验、成功后回最新状态）。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/launcher"
	"github.com/Xsxdot/handoff/internal/proto"
)

// handleLaunchersGet 处理 GET /api/launchers[?machine=]。
//
// 读盘失败时返回 500 并带真因（文件坏了要让人看得见，不是静默当空）。
// 文件不存在返回空列表——那是正常起点（launcher.Load 的既有语义）。
func (s *Server) handleLaunchersGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	dataDir := s.conf().DataDir
	list, err := launcher.Load(dataDir)
	if err != nil {
		s.log.Error("读取启动项配置失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.LaunchersResp{Launchers: toProtoLaunchers(envfile.Dir(dataDir), list)}
	s.log.Debug("启动项列表查询完成", "count", len(resp.Launchers))
	writeJSON(w, http.StatusOK, resp)
}

// toProtoLaunchers 把落盘形态换算成线格式，并现算 env_missing。
//
// **env_missing 必须在这里算、不落盘**：落一份派生值就有两个真相——文件里
// 说 false、磁盘上那个 env 文件早已被删。这也是 launcher.Item 刻意不带这个
// 字段的原因（见该类型的注释）。
//
// 返回值恒为非 nil 切片：JSON 里 `[]` 与 `null` 对前端是两种东西，
// 列表接口只该给前者。
func toProtoLaunchers(envDir string, list []launcher.Item) []proto.Launcher {
	out := make([]proto.Launcher, 0, len(list))
	for _, it := range list {
		l := proto.Launcher{Name: it.Name, EnvFile: it.EnvFile, Command: it.Command}
		if it.EnvFile != "" {
			// 只关心「读得到吗」，不关心内容：Read 的错误统一折成 missing
			if _, _, _, err := envfile.Read(envDir, it.EnvFile); err != nil {
				l.EnvMissing = true
			}
		}
		out = append(out, l)
	}
	return out
}

// handleLaunchersPut 处理 PUT /api/launchers[?machine=]：整段替换。
//
// 校验分两段：
//  1. launcher.Validate 的四条纯规则（名字非空/唯一、至少填一个、无路径分隔符）；
//  2. 本层追加的第五条——env 文件必须真实存在。它要读盘，故不在纯函数里。
//
// 两段的顺序不能反：先跑纯规则，「第 3 条名字为空」这类错误才不会被
// 「文件不存在」抢先报出来（用户看到的应该是最根本的那条）。
//
// **客户端送来的 env_missing 一律忽略**：它是 GET 时现算的派生字段，
// 采信客户端等于让前端能往磁盘上写一个谎。
func (s *Server) handleLaunchersPut(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	var req proto.LaunchersReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("启动项保存：请求体无法解析")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	dataDir := s.conf().DataDir
	list := make([]launcher.Item, 0, len(req.Launchers))
	withCmd := 0
	for _, l := range req.Launchers {
		// EnvMissing 有意不读：派生字段不采信客户端
		it := launcher.Item{
			Name:    strings.TrimSpace(l.Name),
			EnvFile: strings.TrimSpace(l.EnvFile),
			Command: strings.TrimSpace(l.Command),
		}
		if it.Command != "" {
			withCmd++
		}
		list = append(list, it)
	}
	s.log.Info("启动项保存请求", "count", len(list), "with_command", withCmd)

	if err := launcher.Validate(list); err != nil {
		// 错误文本是中文原文且已点名是哪一条，直接作为 400 响应体
		s.log.Warn("启动项保存被拒：规则校验不通过", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	envDir := envfile.Dir(dataDir)
	for _, it := range list {
		if it.EnvFile == "" {
			continue
		}
		if _, _, _, err := envfile.Read(envDir, it.EnvFile); err != nil {
			// 点名是哪一条：错误会原样成为 400 响应体，只报「文件不可用」
			// 等于让用户自己去猜是五条里的哪一条
			s.log.Warn("启动项保存被拒：env 文件不可用", "launcher", it.Name, "file", it.EnvFile)
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("启动项 %q 指定的 env 文件 %q 不可用：%v", it.Name, it.EnvFile, err)})
			return
		}
	}
	if err := launcher.Save(dataDir, list); err != nil {
		if errors.Is(err, launcher.ErrInvalid) {
			// 理论上到不了这里（上面已 Validate 过），但 Save 自己也校验，
			// 真到了就说明两处规则漂移了——按 400 如实报，不吞成 500
			s.log.Warn("启动项落盘被拒：规则校验不通过", "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.log.Error("启动项落盘失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("启动项已保存", "count", len(list), "with_command", withCmd)
	s.handleLaunchersGet(w, r) // 回最新状态，界面直接拿它刷新（与 handleEnvMapping 同款）
}
```

> **末尾调 `s.handleLaunchersGet(w, r)` 会再跑一次 `forwardIfRequested`，这是安全的**
> ——已查证（`internal/agentd/forward.go:50-54`）：它在
> `name == "" || isForwarded(r)` 时**第一行就返回 false**。三种情形都不会二次转发：
> 本机请求（`machine` 为空）→ 两次都 false；带 `machine=` 的请求 → **第一次**就
> 转发并 return，压根走不到这一行；落在远端的那份请求 → `isForwarded(r)` 为真，
> 两次都 false。`handleEnvMapping`（`env.go:347`）用的正是同一条路径。

(b) 路由注册（`server.go:480` 那一片，紧跟 env 一族之后）：

```go
	api.HandleFunc("GET /api/launchers", s.handleLaunchersGet)
	api.HandleFunc("PUT /api/launchers", s.handleLaunchersPut)
```

(c) 文件头端点清单（`server.go:401` 之后）补两行：

```go
//   - GET  /api/launchers              工作台自定义启动项列表
//   - PUT  /api/launchers              整段替换启动项列表
```

### 2.3 跑绿

```bash
go test ./internal/agentd/ -run TestLaunchers 2>&1 | tail -20
go test ./internal/agentd/ 2>&1 | tail -5
```

**测试范围声明（最小化）**：只跑 `./internal/agentd/`（单包 ~80s，是它的正常耗时）。
本 task 不碰前端、不碰 ptyhost。

### 2.4 日志

已内联：请求入口 Info（条数 + 带命令条数）、三条拒绝 Warn（带 launcher 名与文件名，
**不带命令**）、落盘失败 Error、成功 Info、GET 完成 Debug。

### 2.5 注释

已内联：文件头（职责/边界/日志纪律/形态出处）、`toProtoLaunchers`（为什么现算、
为什么非 nil）、`handleLaunchersPut`（两段校验的顺序理由、为什么忽略客户端
`env_missing`）、`ErrInvalid` 那条兜底分支（为什么不吞成 500）。

### 2.6 提交

```bash
gofmt -l internal/agentd/ && go test ./internal/agentd/ && git add -A && git commit -m "feat(agentd): 新增 /api/launchers 配置面"
```

---

## Task 3 · B3：会话创建接线 + 能力位三处（d_controlplane · 逻辑型）

**Interfaces**
- Consumes：`envfile.LoadFile`、`envfile.Dir`、`envfile.ErrBadName`、
  `ptyhost.OpenOptions{EnvFile 解析结果 → Env, InitCommand}`、`proto.CreatePtySessionReq`
- Produces：`(*Server).launcherEnv(req) ([]string, error)`；三处能力位赋值

**依赖 Task 1**：能力位置 `true` 的那一行只有在 `InitCommand` 真的落地后才诚实
（契约 §3.1 第 2 点：能力位与实现同生同死，不允许先上报 true、下一版补实现）。
**Task 1 未绿之前不得做 3.2(c)。**

### 3.1 先写失败测试

追加进 `internal/agentd` 的既有 pty 测试文件（或新建 `pty_launcher_test.go`）：

| 用例 | 判据 |
|---|---|
| `TestCreatePtySessionNoLauncherFieldsUnchanged` | **兼容性唯一守卫**：都不带时，`Open` 收到的 `OpenOptions` 与今天逐字段相同（`Env` 等于 `sessionEnv()`、`InitCommand` 为空） |
| `TestCreatePtySessionEnvFileOnly` | 只带 env：`Open` 收到的 `Env` 尾部含文件里的变量 |
| `TestCreatePtySessionCommandOnly` | 只带命令：`InitCommand` 原样透传 |
| `TestCreatePtySessionBoth` | 两者都带 |
| `TestCreatePtySessionEnvFileOverridesSessionEnv` | env 文件里定义 `TERM=dumb`，断言最终 `Env` 里 `TERM` 的**最后一次出现**是 `dumb`（`sessionEnv()` 钉死的是 `xterm-256color`）。**这条是叠加顺序的判据** |
| `TestCreatePtySessionMissingEnvFileRejected` | 400，**且没有任何会话被创建**（`s.pty.List()` 长度不变）——反向断言 |
| `TestCreatePtySessionValidEnvFileCreatesSession` | 上一条的正面对照：合法文件时 `List()` 确实多一个 |
| `TestCreatePtySessionEnvFileWithSeparatorRejected` | 400，错误文本透传 `envfile.ErrBadName` 的中文原文 |
| `TestStatusReportsLaunchersSupported` | `/api/status` 的 `launchers_supported` 为 `true` |
| `TestLocalMachineReportsLaunchersSupported` | 本机 `Machine.launchers_supported` 为 `true` |
| `TestFillFromStatusCarriesLaunchersSupportedIncludingNil` | 远端：对端上报 `true` → `true`；**对端没上报（nil）→ 仍是 nil**，不得被折成 false |

> **怎么断言「`Open` 收到了什么」**：包内已有 pty 相关测试，先
> `grep -n "pty\b\|ptyhost\." internal/agentd/*_test.go | head -20` 看现成的搭台方式
> ——若已有可替换的 pty 接口/假实现就用它；若 `s.pty` 是具体的 `*engine.Engine`，
> 就退一步断言**可观测的外部效果**（会话真的建起来了、`sessionEnv` 的覆盖效果
> 通过在假 shell 里 `printf %s "$TERM"` 观察）。**两条路都行，但要在 ledger 里
> 写清最后走的是哪条**——「断言了内部入参」与「断言了外部效果」是两种强度不同的
> 判据，含糊过去等于让审核者以为验的是前者。

### 3.2 最小实现

(a) `internal/agentd/pty_api.go` 新增（放在 `sessionEnv` 之后）：

```go
// launcherEnv 解析启动项指定的 env 文件，返回要**追加**在 sessionEnv() 之后的变量。
//
// 参数：name 为 env 文件名（纯文件名，空串 = 不带）。
// 返回：变量切片（`KEY=VAL` 形态）与错误；name 为空时返回 (nil, nil)。
//
// 注意：
//   - **返回值必须追加在 sessionEnv() 之后，不能放前面。** exec.Cmd 对同名变量
//     取**最后一个**（go1.26.1 实测；这条路径经 pty.StartWithSize → cmd.Start()）。
//     用户选一份 env 文件恰恰是为了覆盖 TERM/PATH 这类缺省——顺序反了，
//     选文件这个动作就在最需要它的场景下静默失效
//   - 文件不存在/名字非法一律**返回错误让调用方答 400，不降级**：契约 §3.1
//     写死了这一点。静默忽略会让用户以为环境生效了，然后在一个环境不对的
//     终端里跑半天
//   - 只打 key 不打值由 envfile.LoadFile 保证（见该函数）
func (s *Server) launcherEnv(name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	return envfile.LoadFile(envfile.Dir(s.conf().DataDir), name, s.log)
}
```

(b) `handleCreatePtySession`（`pty_api.go:100`）：

入口日志那行（`s.log.Info("建终端会话请求", ...)`）补两个键——**只记有没有，不记内容**：

```go
	s.log.Info("建终端会话请求", "base_kind", req.BaseKind, "base_path", req.BasePath,
		"rel", req.Rel, "size", req.Cols, "rows", req.Rows,
		"env_file", req.EnvFile, "with_init_command", req.InitCommand != "")
```

> `env_file` 是文件名（不是内容），记它是有价值的定位信息；`init_command`
> **只记 bool**——命令原文可能含凭据。

在 `resolvePtyBase` 之后、取 `$SHELL` 之前插入：

```go
	extraEnv, err := s.launcherEnv(req.EnvFile)
	if err != nil {
		// 400 而不是降级：静默忽略会让用户在一个环境不对的终端里跑半天
		s.log.Warn("建终端会话：env 文件不可用", "env_file", req.EnvFile, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
```

`Open` 调用点（`:125`）改成：

```go
	sess, err := s.pty.Open(ptyhost.OpenOptions{
		BasePath: base, BaseKind: kind, Shell: shell,
		// 叠加顺序不可颠倒：exec.Cmd 取同名变量的**最后一个**，
		// 启动项的文件必须排在 sessionEnv() 之后才能覆盖缺省（见 launcherEnv）
		Env:         append(s.sessionEnv(), extraEnv...),
		Cols:        req.Cols,
		Rows:        req.Rows,
		InitCommand: req.InitCommand,
	})
```

> `append(s.sessionEnv(), extraEnv...)` 是安全的：`sessionEnv()` 每次都新建切片
> （`base := append([]string{}, os.Environ()...)`，`pty_api.go:43`），不存在
> 复用底层数组把别人的数据踩掉的问题。

(c) 能力位三处（**Task 1 绿了才做**）：

`server.go` 的 `handleStatus`（`ptyOK` 那几行之后）：

```go
	// 启动项能力位：它与 InitCommand 的实现同生同死（契约 §3.1 第 2 点）。
	// **不允许先上报 true、下一版补实现**——那会让新版控制台把请求发给一个
	// 会静默忽略它的 agentd
	launchersOK := true
	resp.LaunchersSupported = &launchersOK
```

`machines.go` 的 `localMachine`（`revealOK` 那两行之后）：

```go
	launchersOK := true
	m.LaunchersSupported = &launchersOK
```

`machines.go` 的 `fillFromStatus`（「能力位原样搬运，包括 nil」那段里）：

```go
	m.LaunchersSupported = st.LaunchersSupported
```

> **`fillFromStatus` 这一行是投影链上最容易漏的一环，也是危害最大的一环**：
> 漏了它，本机永远显示支持、远端永远显示不支持，而控制台读的恰恰是这里。
> 三处都写完后自查：`grep -rn "LaunchersSupported" internal/agentd/` 应恰好
> 三个赋值点（外加 proto 里的两处定义）。

### 3.3 跑绿

```bash
go test ./internal/agentd/ 2>&1 | tail -5
```

**测试范围声明（最小化）**：只跑 `./internal/agentd/`。

### 3.4 日志 / 3.5 注释

已内联。**自查一遍**：`grep -n "InitCommand\|init_command" internal/agentd/pty_api.go`
——`req.InitCommand` 只应出现在「透传给 Open」与「`!= ""` 的 bool 日志」两处，
**任何一处把它当日志值都是违规**。

### 3.6 提交

```bash
gofmt -l internal/agentd/ && go test ./internal/agentd/ && git add -A && git commit -m "feat(agentd): 建终端会话支持 env 文件与启动命令，自报启动项能力位"
```

---

## Task 4 · B4：web api client + 机器详情页管理块（d_web · 逻辑型）

**Interfaces**
- Consumes：TS `Launcher` / `LaunchersResp` / `LaunchersReq`（已在 contract 落地）、
  `request<T>` / `putJSON<T>` / `machineQuery`（`client.ts`）、`fetchEnv`（Env 文件下拉的数据源）
- Produces：`fetchLaunchers(machine)`、`putLaunchers(machine, launchers)`、
  组件 `MachineLaunchers`

**依赖 Task 2**（端点要先存在才谈得上联调；类型已冻结，编译不依赖）。

### 4.1 先写失败测试

新建 `web/src/app/machines/MachineLaunchers.test.tsx`。**动手前先
`cat web/src/app/machines/MachineEnv.test.tsx`**（若存在）对齐搭台方式；
没有就照 `web/src/app/workbench/*.test.tsx` 的形态。用例：

| 用例 | 判据 |
|---|---|
| `名字为空时不发请求` | 点保存 → `putLaunchers` 未被调用，且界面上出现一句**实话**（不是静默无反应） |
| `env 与命令都空时不发请求` | 同上 |
| `env_missing 的条目有可见标注` | 渲染 `{name:'x', env_file:'gone.env', env_missing:true}`，断言标注文本出现 |
| `保存后用返回值刷新` | `putLaunchers` resolve 一份**与提交内容不同**的 `LaunchersResp`（模拟服务端规整），断言界面显示的是**返回的那份**（不是本地乐观值） |
| `launchers_supported !== true 时整块不渲染` | 三态各一条：`true` 渲染 / `false` 不渲染 / **`undefined` 不渲染** |

> 最后一条的三态断言是整个需求 B 最容易写错的地方（写成 `!== false` 就把
> `undefined` 放行了，而 `undefined` 正是旧版远端 agentd 的取值）。它在
> Task 4 与 Task 5 **各出现一次**——不是重复，两处读的是不同的入口
> （详情页 vs 工作台），漏任何一处都是一个真实可见的 bug。

### 4.2 最小实现

(a) `web/src/api/client.ts`（`fetchEnv` 一族之后）：

```ts
// fetchLaunchers 取某台机器的工作台启动项（GET /api/launchers）。
//
// env_missing 是服务端**每次读盘现算**的派生字段，不要缓存它——用户可能刚
// 在 Env 那一块把文件删了。
export function fetchLaunchers(machine: string): Promise<LaunchersResp> {
  return request<LaunchersResp>(`/api/launchers${machineQuery(machine)}`)
}

// putLaunchers 整段替换某台机器的启动项（PUT /api/launchers）。
//
// 返回保存后的最新列表，界面直接拿它刷新——**不要本地乐观更新**：
// 服务端会 trim 名字、会重算 env_missing，本地那份可能是错的。
export function putLaunchers(machine: string, launchers: Launcher[]): Promise<LaunchersResp> {
  return putJSON<LaunchersResp>(`/api/launchers${machineQuery(machine)}`, { launchers })
}
```

并在文件顶部的类型导入里补 `Launcher`、`LaunchersResp`。

(b) 新建 `web/src/app/machines/MachineLaunchers.tsx`，形态照 `MachineEnv.tsx`
（**动手前 `cat web/src/app/machines/MachineEnv.tsx`**，逐条对齐：卡片外壳、
标题、加载/错误态、保存按钮的 disabled 与提示位置）。要点：

- **能力门写在最外层，第一行**：

```tsx
  // 三态门，方向与 pty 相反：**只有明确 true 才渲染**。
  //
  // 为什么不能写 !== false：undefined 正是旧版远端 agentd 的取值——它根本
  // 没有 /api/launchers 这个端点，画一个存不进去的表单比不画更糟。
  // pty 那边 null 放行是因为「建会话失败会返回 501，那句实话由 TerminalTab 说」，
  // 这里没有对应的兜底出口（PUT 到旧端会 404，用户只会看到一句莫名其妙的报错）。
  if (machine.launchers_supported !== true) return null
```

- Env 文件下拉的数据源是 `fetchEnv(machine.name)`（既有接口，返回里有文件列表）。
  **动手前确认 `EnvResp` 里文件清单的字段名**：`grep -n "EnvResp" -A 12 web/src/api/types.ts`。
- 表单本地校验只做两条（名字非空、两者至少一个非空），**并且明说它不替代服务端**：

```tsx
  // 前端校验只是为了少一次往返，**不是权威**：同一套规则的真相在
  // internal/launcher/Validate，服务端每次保存都会再跑一遍。
  // 这里少写一条不会造成漏洞，只会让用户多看一次 400。
```

- 保存后 `setList(resp.launchers)`，不 setState 本地那份。
- `env_missing` 的条目给一个可见标注（照 `MachineEnv` 里既有的告警样式）。

(c) `MachineDetail.tsx:71-73` 插进第四块：

```tsx
      <MachineDiscipline machine={machine} />
      <MachineEnv machine={machine} />
      <MachineLaunchers machine={machine} />
      <MachineExecutor machine={machine} />
```

> 位置在 `MachineEnv` **之后**：启动项引用 env 文件，用户的自然顺序是先有文件
> 再配启动项。

### 4.3 跑绿

```bash
cd web && npx vitest run src/app/machines src/api 2>&1 | tail -20
cd web && npx tsc -b && echo "tsc ok"
```

**测试范围声明（最小化）**：只跑 `src/app/machines` 与 `src/api`（契约夹具在后者）。
全量 `npm test` 不属于本 task。

### 4.4 日志

前端无结构化 logger。**唯一的等价物是错误展示**：保存失败时把服务端 400 的
中文原文**原样显示给用户**（服务端已经点名了是哪一条启动项，吞掉它等于把
最有用的信息扔了）。不要 `console.log` 命令原文。

### 4.5 注释

新文件头写职责与边界；能力门那一行、前端校验非权威那一段、「不本地乐观更新」
各写一句「为什么」。

### 4.6 提交

```bash
cd web && npx tsc -b && npx eslint src/app/machines src/api && cd .. && git add -A && git commit -m "feat(web): 机器详情页新增启动项管理块"
```

---

## Task 5 · B5：web 工作台接入（d_web · 逻辑型）

**Interfaces**
- Consumes：`fetchLaunchers`（Task 4）、`usePoll`、`useMachineCaps`、
  `pickItemsFor`、`ptyBase`、`createPtySession`
- Produces：`useMachineCaps().launchers(machine)`；`BlankTab` 的
  `launchers` / `onPickLauncher` 两个 prop；`pickItemsFor` 的启动项分支；
  `TerminalTab` 的 `envFile` / `initCommand` / `title` 透传

**依赖 Task 3 与 Task 4。**

### 5.1 先写失败测试

追加进 `web/src/app/workbench/`（`BlankTab` 有无独立测试文件先
`ls web/src/app/workbench/*.test.tsx` 确认；`WorkbenchPage.test.tsx` 已存在）：

| 用例 | 判据 |
|---|---|
| `pickItemsFor 列出内置三项 + N 条启动项` | 纯函数断言，顺序：内置在上、启动项在下 |
| `terminalUnavailable 非空时两者都摘掉` | **启动项跟着终端一起消失，不置灰**（它就是终端） |
| `home 基准下启动项保留` | 内置只剩终端，启动项照留（「启动项就是终端，同类」） |
| `启动项不分配快捷键` | 断言启动项条目的 hotkey 为空；且 `hotkeyOf` 对任何按键都返回不到启动项 |
| `launchers_supported 三态` | `true` 展示 / `false` 不展示 / **`undefined` 不展示** |
| `点一条启动项 → 请求体带 env_file 与 init_command` | **断言请求体，不是断言 UI** |
| `不带启动项时请求体与今天逐字节一致` | 兼容性守卫：不出现 `env_file` / `init_command` 键（与 `ptyBase` 对 `rel` 的既有纪律同款） |
| `tab 标题用启动项名字（Q3）` | 开出来的 tab 标题是「跑测试」而不是「终端 N」 |
| `空白 tab 与 + 菜单列出的启动项完全一致` | 两个消费者共用 `pickItemsFor` 的证据 |

> **「不带时请求体逐字节一致」这条不是仪式**：`ptyBase` 的注释里已经为 `rel`
> 写过同一条纪律（`TerminalTab.tsx:59`），而第一批就发现过 TS 的
> `CreatePtySessionReq` 漏了 `rel` 却没被 `tsc` 抓住——**对象展开不触发
> 多余属性检查**。同一个陷阱在这里同样张着口。

### 5.2 最小实现

(a) `useMachineCaps.ts` 补第三个能力位：

```ts
  // launchers 与 pty/reveal 的三态**语义相同、处置相反**：
  // 调用方对 null 的正确反应是「不展示」，不是「放行」。
  // 理由见 MachineLaunchers 的能力门注释——这里没有「先发请求、失败了再说
  // 实话」的兜底出口。
  launchers: (machine: string) => boolean | null
```

实现照 `pty` 那一行：

```ts
    if (typeof m.launchers_supported === 'boolean') nextLaunchers[m.name] = m.launchers_supported
    ...
    launchers: (machine: string) => (launchersMap && machine in launchersMap ? launchersMap[machine] : null),
```

> **能力位进 `useMachineCaps`（不轮询），启动项列表**不**进**：前者是平台属性，
> 一台机器不会跑着跑着就不支持了（该 hook 的文件头注释写死了这条）；
> 后者会变——用户刚在详情页加了一条，回工作台就该看见。

(b) Shell 层拉列表（Q2(b)）。在 `Shell.tsx` 与 `cardsState` 那几行并列：

```tsx
  // 启动项列表按项目树的节奏刷新（30s）：用户在机器详情页改完启动项，
  // 回到工作台就该看见——那正是他改完之后的第一个动作。
  //
  // 为什么不搭 ProjectTreeResp 的车：那要改一个既有响应的线格式，
  // 是一次契约变更；另发一次 GET 只是**刷新节奏**搭同一班车，形状零改动。
  const launchersState = usePoll(
    () => fetchLaunchers(wb.base?.machine ?? ''),
    30_000,
    { enabled: caps.launchers(wb.base?.machine ?? '') === true },
  )
```

> **`enabled` 那一行不是优化，是正确性**：对一台不支持的机器轮询
> `/api/launchers` 会每 30 秒刷一条 404。
>
> **注意 `usePoll` 的 fetcher 是内联箭头函数**——该 hook 用 `fetcherRef` 专门
> 处理了这一点（`usePoll.ts:37` 的注释），所以 `wb.base` 变化不会重启轮询，
> 但也意味着**基准目录切到另一台机器时列表不会立刻重拉**。处置：把
> `wb.base?.machine` 作为 key 传下去，或在 `useEffect` 里 `launchersState.refresh()`。
> **实现时挑一条并在 ledger 里写清**——这是一个真实的、会让用户看到隔壁机器
> 启动项的缺口，不许含糊过去。

(c) `BlankTab.tsx`：

```ts
// LauncherItem 是面板上的一条启动项。
//
// 与 PICK_ITEMS 的内置项**刻意不合并成一个数组**：内置种类是闭集（PickKind），
// 启动项是一张会长的列表，两个轴正交（Q1 拍板 (c)）。合并意味着 PickKind
// 变成开集，hotkeyOf 的穷举与 pickItemsFor 的过滤都要开始解析字符串前缀。
export interface LauncherItem {
  name: string
  envMissing: boolean
}
```

`BlankTabProps` 补两个：

```ts
  // launchers 是当前基准目录所在机器的启动项。空数组 = 一条都没有，
  // **也包括「这台机器不支持」**——那个判断在调用方（Shell）做，本组件不认识能力位。
  launchers?: LauncherItem[]
  onPickLauncher?: (name: string) => void
```

`pickItemsFor` **保持原签名不变**（它是内置项的过滤器，三处调用方都在用），
新增一个并列的纯函数：

```ts
// launchersFor 过滤出某个基准目录下能展示的启动项。
//
// 只有一条规则：终端不可用时一条都不展示——启动项开出来的**就是终端**，
// 终端开不了它们全都开不了。**不置灰**（W3b §0 既有纪律：置灰是在承诺
// 「以后能用」，用户会反复点它）。
//
// 与 pickItemsFor 不同，这里**不按 base.kind 过滤**：home 基准下内置项只留
// 终端，而启动项就是终端，同类，照留。
//
// 导出与 pickItemsFor 同因：+ 菜单与本面板必须用同一份判断，两处分别写
// 就会出现「面板里没有、+ 菜单里却有」。
export function launchersFor(launchers: LauncherItem[] | undefined, terminalUnavailable?: string): LauncherItem[] {
  if (terminalUnavailable) return []
  return launchers ?? []
}
```

渲染：内置项的 `<ul>` 之后追加启动项的一段，**hotkey 那一栏留空**：

```tsx
        {/* 启动项不分配快捷键：数量不定，分配规则会立刻变成一张要维护的表，
            而印在面板上却按不动的快捷键是一句 UI 说了不算的话。 */}
```

`hotkeyOf` **一行不改**——它的返回类型仍是 `PickKind | null`，启动项本来就不参与。

(d) `TabBar.tsx:84`：`+` 菜单在 `pickItemsFor(...)` 之后追加
`launchersFor(launchers, terminalUnavailable)`，两个 prop 一路透传。

(e) `WorkbenchPage.tsx`：`pick` / `newIn` / `startFromEmpty` 三处**不动**
（它们 switch 的是 `PickKind`）；新增三条并列的 `pickLauncher` / `newLauncherIn` /
`startLauncherFromEmpty`，各自开一个终端 tab 并把启动项名字带进 `TabContent`。

`TabContent` 的 terminal 分支要能记住启动项（Q3 的标题 + 重建会话时的参数）：

```ts
  | { kind: 'terminal'; seq: number; sessionId?: string; rel?: string; incompatible?: boolean
      // launcher 是开这个终端用的启动项名字（Q3：tab 标题用它）。
      // 缺席 = 普通终端。
      launcher?: string }
```

> **`persist.ts` 必须同步改两处**，否则重开控制台后启动项 tab 的标题变回
> 「终端 N」：`stripTab`（`persist.ts:56`，terminal 分支是**逐字段重建**的，
> 不加进去就会被剥掉）与 `parseContent`（`persist.ts:144`，不加就整行丢弃）。
> `pruneDeadSessions`（`:198`）的 terminal 分支同样逐字段重建，**也要带上**。
> **这三处是本 task 最容易漏的地方**——它们不会让 `tsc` 红（都是可选字段），
> 只会让重开之后悄悄丢东西。给它们一条自己的用例：
> 「带 launcher 的 terminal tab 存盘再读回来，`launcher` 还在」。

(f) `TerminalTab.tsx`：`ptyBase` 之外，建会话请求补两个字段。**沿用 `rel` 的既有纪律**
（不带时不加键）：

```ts
// launcherFields 把启动项参数翻译成建会话请求的两个字段。
//
// 与 ptyBase 对 rel 的处置同款：**不带时返回空对象**，请求体与历史形态
// 逐字节一致。对象展开不触发多余属性检查，tsc 不会替你抓这个。
function launcherFields(envFile?: string, initCommand?: string): { env_file?: string; init_command?: string } {
  const out: { env_file?: string; init_command?: string } = {}
  if (envFile) out.env_file = envFile
  if (initCommand) out.init_command = initCommand
  return out
}
```

> `TerminalTab` 拿到的是**启动项名字**，而请求要的是 `env_file` / `command`。
> 换算在**上层**（`WorkbenchPage`，它持有 `launchers` 列表）做，把两个具体字段
> 传下来——`TerminalTab` 不该为了开一个终端去认识「启动项」这个概念。
> **启动项在开 tab 之后被删掉时**：上层查不到就按普通终端开（不报错、不空转）。
> 给它一条用例。

(g) tab 标题（Q3）：找到终端 tab 标题的生成处
（`grep -rn "终端" web/src/app/workbench/*.tsx | head`），`content.launcher` 非空时用它。

### 5.3 跑绿

```bash
cd web && npx vitest run src/app/workbench src/app/data 2>&1 | tail -20
cd web && npx tsc -b && echo "tsc ok"
```

**测试范围声明（最小化）**：`src/app/workbench` 与 `src/app/data`。

### 5.4 日志 / 5.5 注释

同 Task 4：无 logger，等价物是把服务端 400 原文展示给用户。注释已内联
（Q1 正交两轴、启动项不按 base.kind 过滤、不分配快捷键、能力门方向、
`launcherFields` 的逐字节一致纪律）。

### 5.6 提交

```bash
cd web && npx tsc -b && npx eslint src && cd .. && gofmt -l internal/ && git add -A && git commit -m "feat(web): 工作台空白 tab 与 + 菜单接入自定义启动项"
```

---

## Task 6 · 收口

```bash
gofmt -l internal/ && go vet ./... && go build ./... && GOOS=windows go build ./...
go test ./internal/... 2>&1 | tail -20
cd web && npx tsc -b && npm test 2>&1 | tail -20 && npx eslint src
```

写 ledger：`docs/superpowers/ledgers/2026-08-22-custom-launchers-ledger.md`。
逐 task 记改了哪些文件、跑了什么命令、输出是什么。**三个悬置决定必须各写一条结论**：

1. Task 3 的「断言 `Open` 入参」还是「断言外部效果」，最后走的哪条；
2. Task 5(b) 的「切换机器时列表怎么重拉」，最后选的哪条；
3. Task 5(e) 的 `persist.ts` 三处是否都改了，用例是否覆盖。

**没跑到结果不许写结论**；没验的写「未验证」，不写「应该没问题」。

---

## 四项检查（出稿自审）

### 1. 缺陷族对抗审查

| 族 | 结论 |
|---|---|
| **生命周期 / 状态机中断** | ①`Open` 已返回、命令还没写进去的 0~3 秒里，用户敲的字会与命令**交错**。拆解 §6.1 已拍板：接受它、把窗口压到最小（首字节即写），不做「写完再返回」——那会让建会话请求阻塞最多 3 秒。**真机清单第 4 条量这个窗口**。②这 3 秒内会话被关掉：`writeInitCommand` 的 `h.Write` 返回错误，按 Debug 记，不留孤儿（goroutine 自然退出）。③agentd 重启：PTY 会话本就是进程内内存态，全部消失，等待中的 goroutine 随进程一起走——**既有行为，非本次引入**。④启动项配置是文件，重启后照读。 |
| **静默失败 / 误导报错** | ①`env_file` 不存在 → **400 不降级**（契约写死）：静默忽略会让用户在一个环境不对的终端里跑半天。②PUT 的每条拒绝都**点名是哪一条启动项**——错误原样成为 400 响应体，只报「不合法」等于没报。③前端把服务端 400 原文原样显示，不吞。④`writeInitCommand` 失败只 Debug：终端仍完全可用，那不是故障。⑤**唯一残余**：命令写进去之后跑失败了，handoff 一无所知——那是终端里的事，与人手敲错命令没有区别，**不该报错**。 |
| **跨平台假设** | `platform_other.go`（`!unix`）无需改动：`Open` 在 `!ptySupported` 时第一行就返回 `ErrNotSupported`，走不到写命令。新测试文件带 `//go:build unix`。核验命令 `GOOS=windows go build ./...` 写进 Task 1.6 与 Task 6。**能力位不因此撒谎**：`launchers_supported` 报的是「这个 agentd 认识 `init_command` 这个字段」，与该平台有没有 PTY 是两件事——没有 PTY 时终端入口本来就整个消失，启动项跟着一起消失（Task 5 的 `launchersFor`）。 |
| **假红 / 假绿测试** | ①**反向断言清单**（本计划共 6 条，每条都配了正面对照）：B1 的「空命令时不写」配「非空时确实写」；B1 的「只写一行」配「写了那一行」；B2 的「日志不含密钥」配「日志确实打了条数」；B2 的「PUT 忽略 env_missing」配「GET 确实算了它」；B3 的「400 时没有会话被创建」配「合法时确实创建了」；B5 的「不带时无 env_file 键」配「带时有」。②**时间判据的方向**：B1 首字节路径断**上界**（<2s，证明没走兜底）、兜底路径断**下界**（>=2.5s，证明确实等过）——上界在慢机器上会偶发翻红，而偶发红会被当噪音忽略，那条判据就实际失效了。③**成对断言**：`env_missing` 两个方向都断（只断一侧的话写成常量也能全绿）。④夹具里的行为假设：B1 用**假 shell**把「什么时候出第一个字节」变成可控量，不假装知道真 zsh 的行为——真 shell 的行为在真机清单第 2、3、4 条。 |
| **门禁绕过** | ①`/api/launchers` 与既有配置面走同一套鉴权（`api` mux 上的中间件，与 `/api/env` 逐字同路）。②**新增的执行路径是 `InitCommand`**，它过的门是 `CreatePtySessionReq` 那条既有的会话创建门——而拆解 §2 已核过：控制台会话在能力上**等价于主令牌**（`POST /api/tasks/{id}/run` 就是 `sh -c`），`InitCommand` 不扩大任何权限面。③`EnvFile` 的路径穿越由 `envfile` 的纯文件名约束挡（`ErrBadName`），**且两处都挡**：保存时（B2 第五档）与使用时（B3 的 `LoadFile` 内部 `resolvePath`）。 |
| **第六族 · webview / 平台差异** | 本次前端只加表单与列表，不碰剪贴板、不碰拖放、不碰 cookie——三条已知的 WKWebView 差异点一个都不沾。**结论：无，因为本次前端改动不触及任何已知有平台差异的 API。** |

### 2. 序列化边界设问

新增字段的产生→消费全链路，以及每一处**手写序列化/投影**：

| # | 投影点 | 文件 | 有断言吗 |
|---|---|---|---|
| 1 | `launcher.Item` ↔ `launchers.json` | `internal/launcher/launcher.go` | ✅ Ticket 0 已有测试 |
| 2 | `launcher.Item` → `proto.Launcher`（**手写，且现算 EnvMissing**） | `toProtoLaunchers` | ✅ B2 的成对断言 |
| 3 | `proto.Launcher` → `launcher.Item`（**手写，且丢弃 EnvMissing**） | `handleLaunchersPut` | ✅ B2 的「忽略客户端 env_missing」 |
| 4 | Go `LaunchersResp` ↔ JSON | 契约夹具 | ✅ contract 节点已落 |
| 5 | JSON ↔ TS `LaunchersResp` | `contract.test.ts` | ✅ contract 节点已落 |
| 6 | TS `Launcher` → `LauncherItem`（**手写**） | `WorkbenchPage` / `Shell` | ✅ B5 的「三态 + 列表一致」 |
| 7 | `TabContent.launcher` ↔ 落盘 payload（**手写、逐字段重建、共三处**） | `persist.ts` 的 `stripTab` / `parseContent` / `pruneDeadSessions` | ⚠️ **这是本次最险的一处**，Task 5(e) 已点名并要求一条穿过存盘-读回的用例 |
| 8 | TS 请求体 → Go `CreatePtySessionReq`（**对象展开，tsc 不查多余属性**） | `TerminalTab.launcherFields` | ✅ B5 的「不带时逐字节一致」 |

**「两端各自有测试」≠「这条链路有测试」**：第 7 项就是典型——`persist.ts` 两端
（写/读）各自都有测试，但只要 `stripTab` 漏了新字段，存进去的东西就少一块，
而读回来那侧的测试喂的是自己构造的完整对象，永远发现不了。**必须有一条穿过
真实 encode→decode 的用例。**

**可空类型区分「字段缺失」与「值为零」**：`LaunchersSupported *bool` 与
`Launcher.EnvMissing`（**故意不带 `omitempty`**）两处都已按这条办；
`TabContent.launcher?: string` 用 `undefined` 表达缺席。

### 3. 枚举新值过既有白名单

本次**不引入任何新的枚举取值**：没有新状态名、没有新事件类型、没有新 kind。
`PickKind` 刻意**保持闭集不变**（Q1 拍板 (c) 的全部意义就在这里）——启动项
走的是一条并列的新通道，不往既有闭集里塞值，因此不存在「两侧入口各自绿、
中间一处白名单挡死」的通道分裂风险。

**一处需要点名的例外**：`TabContent` 的 `kind` 仍是那四个值，启动项复用
`'terminal'`，只多一个可选字段。`persist.ts` 的 `parseContent` 是那个白名单，
Task 5(e) 已覆盖。

### 4. 上下文预算检查

| Task | 有界文件集 | 圈得出？ |
|---|---|---|
| 1 | `internal/ptyhost/engine/{engine.go, initcmd_test.go}` | ✅ |
| 2 | `internal/agentd/{launchers_api.go, launchers_api_test.go, server.go}` | ✅ |
| 3 | `internal/agentd/{pty_api.go, machines.go, server.go, *_test.go}` | ✅ |
| 4 | `web/src/api/client.ts` + `web/src/app/machines/{MachineLaunchers.tsx,.test.tsx, MachineDetail.tsx}` | ✅ |
| 5 | `web/src/app/workbench/{BlankTab,TabBar,WorkbenchPage,TerminalTab,tabs,persist}.tsx/ts` + `web/src/app/{data/useMachineCaps.ts, shell/Shell.tsx}` | ✅（9 个文件，是五张卡里最大的一张，但仍一条路径规则写得出来） |

架构法第三条判据 2 在 `internal/agentd` 上**命中**（61 文件平铺、无子包）。
回答义务已由拆解 §1.1 履行：**能圈出有界文件集**（Task 2/3 合计四个文件），
新建的 `launchers_api.go` 照 `env.go` 的「一个配置面一个文件」形态落，
不往平铺包里再塞一个概念。**竖切欠账仍在，本次不预支。**

### 5. 类型标注

- **d_host（B1）· 边界型**：机内只验「字节有没有按时写进 PTY 输入」（用假 shell
  把就绪时刻变成可控量）；「命令在真实 login shell 里的语义」走真机清单 2/3/4。
- **d_remote · 边界型**：本次**零代码改动**（`forwardIfRequested` 原样转发），
  但「新版控制台 → 旧版远端 agentd」的实际行为是行为事实，走真机清单第 1 条。
- **d_controlplane / d_web · 逻辑型**：测试可闭环，判据全在机内。

---

## 真机清单（**B6 · 归协调者执行，不派发**）

派发出去的执行者被纪律块禁止起 executor 进程与调 handoff CLI；第 1 条还要驱动
一台旧版 agentd。这四条留本地：

1. **新版控制台 → 旧版远端 agentd 的实际行为**（头号风险）。三问：
   (i) 该机的 `Machine.launchers_supported` 确实是**缺席**而非 `false`？
   (ii) 前端确实不展示该机的启动项？
   (iii) 手工绕过前端直发带 `env_file` 的建会话请求，旧端确实**静默忽略**？
   —— 第 (iii) 问是在**确认危害的形状**，不是假设它。
2. **命令在真实 login shell 里执行后会话继续存在**，且 Ctrl-C 只杀命令不杀会话。
3. **rc 链读 stdin 的真实发生率**（承契约 §3.2 的残余风险）：在自己的 zsh/bash
   配置上验一次。命中时的症状是命令原文被 rc 当输入吃掉。
4. **真实 shell 从 exec 到首字节的延迟**（承拆解 §6.1 的交错窗口）：量一下，
   确认它远小于人手反应时间。**若不是**（例如某些 rc 要跑 1 秒才出提示符），
   交错窗口的处置要重议——那不是「验收不通过」，是「拍板要重来」。

---

## 自审三查

- **spec / 拆解覆盖**：B1→Task 1，B2→Task 2，B3→Task 3，B4→Task 4，B5→Task 5，
  B6→真机清单（不派发）。四个岔口的裁决各自落到了具体判据上：Q1 在 Task 5(c)
  的 `LauncherItem` 与 `hotkeyOf` 不改；Q2 在 Task 5(b)；Q3 在 Task 5(e)(g)
  与那条标题用例；Q4 在 Task 1 的 `TestInitCommandWritesExactlyCommandPlusNewline`。
- **占位符扫描**：无 TBD。五处「动手前先 `cat`/`grep` 确认包内既有写法」是
  **纪律不是占位符**——它们指名了要确认什么、确认不了时走哪条替代路径
  （Task 3.1 的两条断言强度、Task 5(b) 的两条重拉方案），且都要求在 ledger
  里写清最后走的哪条。三个悬置决定已集中列进 Task 6 的 ledger 要求。
- **跨 task 类型一致性**：`proto.Launcher`（Go）/ `Launcher`（TS）/ `launcher.Item`
  三者的字段对应关系在 §1.1 表里写死；`LauncherItem`（前端展示态）刻意只带
  `name` + `envMissing`——**命令原文不进前端的展示态**，它只在管理块的表单里
  出现，工作台一侧根本不需要它。
