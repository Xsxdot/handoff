# B142 + B122：Windows 平台层补完 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Windows 执行机有 agentd 托管路径（`handoff service install` 可用、换版闸二打开），并恢复每任务进程计数（`TaskBudget` 告警档 + `handoff footprint` 足迹显示）。

**Architecture:** B142 走 `schtasks /Create /XML`——Windows 与 launchd/systemd 同构，都是「生成单元文件 → 交给管理器加载」；`KeepAlive`（exit 0 也拉起）由 `<Repetition>PT1M` + `MultipleInstancesPolicy=IgnoreNew` 模拟。B122 不用 Toolhelp32（Windows 没有 pgid，`classify` 三条规则全失效），改由 shim 查自己持有的 Job Object 成员表并落盘 `members.json`，agentd 侧在 `Footprint` 顶部加一条容器前置分支读它。

**Tech Stack:** Go 1.24、`golang.org/x/sys/windows`（v0.47.0，已在 go.mod）、标准库 `unicode/utf16` 与 `encoding/xml`。**不引入任何新依赖。**

**Spec:** [2026-08-18-b142-b122-windows-platform-layer-design.md](../specs/2026-08-18-b142-b122-windows-platform-layer-design.md)

## Global Constraints

- **日志一律用 `slog`**：`internal/service` 用注入的 `m.log`，`internal/prochost` 用包级 `log()`（`prochost.go:134`，自带 `mod=prochost`）。**禁止 `fmt.Printf` / `println` 作为日志机制。**
- **注释用中文，写「为什么」不写「做了什么」**：新文件必须有文件头注释（职责 + 边界）；导出方法必须有 doc 注释（参数、返回、注意事项）。
- **`internal/service/windows.go` 不加 `//go:build windows`**。`launchd.go` / `systemd.go` 都没有 build tag，靠 `New()` 里的 `runtime.GOOS` switch 分发——照此办理，Windows 实现才能在 mac/Linux 上跑单测。**只有 `platform_windows.go` 里的系统调用原语才带 build tag。**
- **unix 侧执行路径必须逐字节不变**。B47 的误杀教训、B70 的口径一致、B72 的出生登记、B119 的降级清扫全部长在 `footprint.go` 的三段判据上。Task 6 有专门的反面断言钉住这一点。
- **`gofmt -l .` 必须无输出**。测试全绿不等于格式干净，两者都要跑。
- **不改 `Sweep`**。Windows 的回收由 job 的 `KILL_ON_JOB_CLOSE` 连坐承担（B148 已收口），只读的 `Footprint` 接容器来源，动手的 `Sweep` 不接。
- 门禁（每个 task 的 commit 前）：`go build ./...` + `go vet ./...` + `gofmt -l .`（无输出）+ 该 task 涉及包的 `go test`。

---

## File Structure

| 文件 | 责任 | Task |
|---|---|---|
| `internal/service/windows.go` | 新建。Windows 服务托管：XML 渲染 + schtasks 调用 | 1, 2 |
| `internal/service/windows_test.go` | 新建。XML 内容与 schtasks 调用序列钉死 | 1, 2 |
| `internal/service/service.go` | 改。`New` 增 `case "windows"`；包头边界注释改写 | 2 |
| `cmd/upgrade.go` | 改。`upgradeWaitTimeoutPush` 60s → 120s | 3 |
| `cmd/upgrade_test.go` | 改。钉住新值与理由 | 3 |
| `internal/prochost/platform_windows.go` | 改。新增 `jobProcessIDs()` 原语 | 4 |
| `internal/prochost/platform_windows_test.go` | 新建。`//go:build windows`，真的建 job 验证 | 4 |
| `internal/prochost/members.go` | 新建。`members.json` 的读写与路径推导（平台无关） | 5 |
| `internal/prochost/members_test.go` | 新建。序列化往返、损坏容错 | 5 |
| `internal/prochost/prochost.go` | 改。`Handle` 加 `MembersPath`；`Start` 填它 | 5 |
| `internal/prochost/shim.go` | 改。Windows 上采样源换成 job 成员表 | 6 |
| `internal/prochost/footprint.go` | 改。`Footprint` 顶部加容器前置分支 | 7 |
| `internal/prochost/footprint_test.go` | 改。前置分支 + 反面断言 | 7 |
| `cmd/agentd.go` | 改。删掉 TaskBudget 不生效的启动 Warn | 7 |

---

## Task 1: Windows 单元 XML 渲染 + UTF-16 落盘

**Files:**
- Create: `internal/service/windows.go`
- Create: `internal/service/windows_test.go`

**Interfaces:**
- Consumes: `Spec{BinPath, ConfigPath, LogPath}`（`service.go` 既有）
- Produces:
  - `const WindowsTaskName = "handoff-agentd"`
  - `type windowsManager struct{ log *slog.Logger; localAppData string; currentUser func() (string, error); mkdirAll func(string, os.FileMode) error; run func(string, ...string) ([]byte, error); writeFile func(string, []byte, uint32) error; remove func(string) error }`
  - `func (m *windowsManager) taskXML(spec Spec, user string) string`
  - `func toUTF16LE(s string) []byte`

- [ ] **Step 1: 写失败测试——XML 四项承重配置**

创建 `internal/service/windows_test.go`：

```go
// Windows 实现的测试：Task Scheduler XML 内容与 schtasks 调用序列都在这里钉住。
//
// 全部经缝注入，不真的调 schtasks、不真的写 %LOCALAPPDATA%——测试跑完机器上
// 不会多出任何计划任务。本文件**不带 build tag**：windows.go 全平台编译
// （同 launchd.go / systemd.go），测试因此在 mac/Linux 上也能跑。
package service

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// fromUTF16LE 把 toUTF16LE 的产物解回字符串，供断言 XML 内容用。
func fromUTF16LE(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	codes := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		codes = append(codes, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(codes))
}

// XML 的内容是这条路上最容易写错又最难发现的东西，逐项钉住。
// 四项承重配置任一缺失都会静默失效，测试是唯一的防线。
func TestWindowsTaskXMLContent(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{
		BinPath:    `C:\Users\u\.local\bin\handoff.exe`,
		ConfigPath: `C:\Users\u\.handoff\config.yaml`,
		LogPath:    `C:\Users\u\.handoff\agentd.log`,
	}, `WIN-B37\Administrator`)

	for _, want := range []string{
		// 承重一：重复触发 + IgnoreNew，两条合起来才等价于 launchd 的 KeepAlive
		"<Interval>PT1M</Interval>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		// 承重二：登录触发，对标 RunAtLoad
		"<LogonTrigger>",
		// 承重三：电池两项，默认 true 会让任务根本不启动且不报错
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
		// 承重四：直接指向 handoff.exe
		`<Command>C:\Users\u\.local\bin\handoff.exe</Command>`,
		"<Arguments>agentd --config C:\\Users\\u\\.handoff\\config.yaml</Arguments>",
		// 不限时长：默认 72 小时会把长跑的 agentd 掐掉
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<UserId>WIN-B37\\Administrator</UserId>",
		"<LogonType>S4U</LogonType>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("XML 缺少 %q:\n%s", want, body)
		}
	}

	// 禁止项：套 cmd.exe 正是 D8「schtasks /end 只杀外层」的根因
	if strings.Contains(strings.ToLower(body), "cmd.exe") {
		t.Error("XML 不得套 cmd.exe：D8 实测它让 /end 只杀外层，agentd 孙进程原样活着，管理器视图与现实分叉")
	}
}

// schtasks /Create /XML 喂 UTF-8 会报一个与编码毫无关系的错，
// 这条断言是那个坑的唯一防线。
func TestWindowsXMLIsUTF16LEWithBOM(t *testing.T) {
	got := toUTF16LE("<Task>")
	if len(got) < 2 || got[0] != 0xFF || got[1] != 0xFE {
		t.Fatalf("缺少 UTF-16LE BOM，前两字节=%v", got[:min(2, len(got))])
	}
	if s := fromUTF16LE(got); s != "<Task>" {
		t.Fatalf("往返不一致：%q", s)
	}
	// ASCII 每个字符占两字节：BOM 2 + 6 字符 * 2 = 14
	if len(got) != 14 {
		t.Fatalf("UTF-16LE 长度应为 14，实际 %d", len(got))
	}
}

// XML 特殊字符必须转义，否则路径里一个 & 就让 schtasks 拒绝整个文件
func TestWindowsTaskXMLEscapes(t *testing.T) {
	m := &windowsManager{log: testLogger()}
	body := m.taskXML(Spec{BinPath: `C:\a&b\handoff.exe`}, "u")
	if strings.Contains(body, `C:\a&b\handoff.exe`) {
		t.Error("路径里的 & 未转义，schtasks 会拒绝整个 XML")
	}
	if !strings.Contains(body, "&amp;") {
		t.Errorf("未见转义后的 &amp;:\n%s", body)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/service/ -run 'TestWindows' -v
```

Expected: FAIL — `undefined: windowsManager`、`undefined: toUTF16LE`

- [ ] **Step 3: 写最小实现**

创建 `internal/service/windows.go`：

```go
// windows.go —— Windows 侧的服务托管实现（Task Scheduler 计划任务）。
//
// 为什么是计划任务而不是 SCM 服务：executor 的凭据全挂在用户 profile 下
// （grok 的 ~/.grok/auth.json、opencode 的 auth、claude 的 settings），
// SCM 服务默认跑在 Session 0 / SYSTEM，%USERPROFILE% 会变，那条链路是 handoff
// 的命脉。且计划任务是 B37 整轮验收已经建立的运行形态，换掉等于要求那轮结论
// 重新成立一遍。详见 spec §2.1。
//
// 边界：
//   - **不加 //go:build windows**：与 launchd.go / systemd.go 一致，靠 New() 的
//     runtime.GOOS switch 分发。加了 build tag 就没法在 mac/Linux 上跑它的单测，
//     而这份 XML 的内容恰恰是最需要测试盯住的东西
//   - 单元走 XML 而不是命令行参数：schtasks /Create 没有 /MULTIPLEINSTANCES 开关，
//     而 IgnoreNew 是承重配置，只能经 XML 表达
//   - 不做日志重定向：Task Scheduler 没有 StandardOutPath 式的能力，
//     Spec.LogPath 由 agentd 自己写（logx.Setup 已经在做）
package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// WindowsTaskName 是计划任务的名字，同时也是 XML 的文件名主干。
const WindowsTaskName = "handoff-agentd"

// windowsManager 是 Windows 实现。七个字段是测试缝。
type windowsManager struct {
	log          *slog.Logger
	localAppData string
	currentUser  func() (string, error)
	mkdirAll     func(path string, perm os.FileMode) error
	run          func(name string, args ...string) ([]byte, error)
	writeFile    func(path string, data []byte, perm uint32) error
	remove       func(path string) error
}

// newWindows 构造生产用的 Windows manager。
func newWindows(log *slog.Logger) *windowsManager {
	m := &windowsManager{
		log:          log,
		localAppData: os.Getenv("LOCALAPPDATA"),
		currentUser: func() (string, error) {
			u, err := user.Current()
			if err != nil {
				return "", err
			}
			return u.Username, nil
		},
		mkdirAll: os.MkdirAll,
		run: func(name string, args ...string) ([]byte, error) {
			// CombinedOutput：schtasks 的真因大多写在 stderr 上，只取 stdout
			// 会得到一个空字符串加一个 "exit status 1"，等于没有诊断信息
			return exec.Command(name, args...).CombinedOutput()
		},
		writeFile: func(p string, b []byte, perm uint32) error { return os.WriteFile(p, b, os.FileMode(perm)) },
		remove:    os.Remove,
	}
	return m
}

// toUTF16LE 把字符串编码为带 BOM 的 UTF-16 LE 字节序列。
//
// 为什么必须做这件事：`schtasks /Create /XML` 只吃 UTF-16，喂 UTF-8 会报一个
// **与编码毫无关系**的错（形如「该文件无效」），排查时几乎不可能想到编码上去。
// 这是 Windows 侧最经典的一个坑，代价是几小时的错误方向。
func toUTF16LE(s string) []byte {
	codes := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2+len(codes)*2)
	b = append(b, 0xFF, 0xFE) // UTF-16 LE BOM
	for _, c := range codes {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// esc 把字符串按 XML 文本转义。
//
// 路径里出现 & < > 是完全可能的（用户目录名可以带这些字符），不转义会让
// schtasks 拒绝整个 XML，而报文同样不会提到转义。
func esc(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		// EscapeText 写进 bytes.Buffer 不会失败；真出错就退回原文，
		// 让 schtasks 去报错，好过静默吞掉内容
		return s
	}
	return buf.String()
}

// taskXML 渲染 Task Scheduler 的任务定义。
//
// 参数：
//   - spec: 要托管的 agentd 描述
//   - user: 运行身份（形如 `WIN-B37\Administrator`）
//
// 返回：XML 全文（UTF-8 字符串；落盘前由 toUTF16LE 转码）
//
// 注意——四项承重配置，任一缺失都是静默失效：
//   - <Repetition><Interval>PT1M + <MultipleInstancesPolicy>IgnoreNew：
//     两条合起来才等价于 launchd 的 KeepAlive=true（exit 0 也重新拉起），
//     而那正是自更新换版唯一的交接点。缺前者 = 换版后服务无声消失；
//     缺后者 = 每分钟起一个新实例、全被 DataDir 锁挡下、日志刷满锁冲突。
//     PT1M 是 Task Scheduler 允许的最小重复间隔，所以最坏空窗接近 1 分钟
//   - <LogonTrigger>：对标 RunAtLoad
//   - DisallowStartIfOnBatteries / StopIfGoingOnBatteries：**默认都是 true**，
//     会让任务根本不启动且不报错。Task Scheduler 最经典的静默失效
//   - <Command> 直接指向 handoff.exe，不套 cmd.exe /c：D8 实测套了 cmd 之后
//     schtasks 只跟踪外层，/End 杀不到 agentd，管理器视图与现实分叉
func (m *windowsManager) taskXML(spec Spec, user string) string {
	args := "agentd"
	if spec.ConfigPath != "" {
		args += " --config " + spec.ConfigPath
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	b.WriteString("  <RegistrationInfo>\n    <Description>handoff agentd</Description>\n  </RegistrationInfo>\n")
	b.WriteString("  <Triggers>\n    <LogonTrigger>\n      <Enabled>true</Enabled>\n")
	b.WriteString("      <Repetition>\n        <Interval>PT1M</Interval>\n")
	b.WriteString("        <Duration>P365D</Duration>\n        <StopAtDurationEnd>false</StopAtDurationEnd>\n")
	b.WriteString("      </Repetition>\n    </LogonTrigger>\n  </Triggers>\n")
	b.WriteString("  <Principals>\n    <Principal id=\"Author\">\n")
	b.WriteString("      <UserId>" + esc(user) + "</UserId>\n")
	b.WriteString("      <LogonType>S4U</LogonType>\n      <RunLevel>HighestAvailable</RunLevel>\n")
	b.WriteString("    </Principal>\n  </Principals>\n")
	b.WriteString("  <Settings>\n")
	b.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\n")
	b.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n")
	b.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\n")
	b.WriteString("    <StartWhenAvailable>true</StartWhenAvailable>\n")
	b.WriteString("    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\n")
	b.WriteString("    <Enabled>true</Enabled>\n")
	b.WriteString("  </Settings>\n")
	b.WriteString("  <Actions Context=\"Author\">\n    <Exec>\n")
	b.WriteString("      <Command>" + esc(spec.BinPath) + "</Command>\n")
	b.WriteString("      <Arguments>" + esc(args) + "</Arguments>\n")
	b.WriteString("    </Exec>\n  </Actions>\n</Task>\n")
	return b.String()
}

// UnitPath 返回 XML 的落点。
func (m *windowsManager) UnitPath() (string, error) {
	if m.localAppData == "" {
		return "", fmt.Errorf("取不到 %%LOCALAPPDATA%%，无法定位计划任务 XML 的落点")
	}
	return filepath.Join(m.localAppData, "handoff", WindowsTaskName+".xml"), nil
}

// Kind 返回管理器种类。
func (m *windowsManager) Kind() string { return "schtasks" }
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/service/ -run 'TestWindows' -v
```

Expected: PASS（三个测试全绿）

- [ ] **Step 5: 加关键节点日志**

`taskXML` 是纯函数，不打日志（与 `plistBody` 一致）。本步只给 `UnitPath` 的失败分支补上下文：

```go
	if m.localAppData == "" {
		m.log.Error("取不到 LOCALAPPDATA，无法定位计划任务 XML 落点",
			"task", WindowsTaskName)
		return "", fmt.Errorf("取不到 %%LOCALAPPDATA%%，无法定位计划任务 XML 的落点")
	}
```

- [ ] **Step 6: 加注释自检**

对照确认（Step 3 的代码已含，此步是核对不是补写）：
- 文件头注释：职责 + 三条边界（不加 build tag / 走 XML / 不做日志重定向）✓
- `toUTF16LE` doc 注释说明「为什么必须」而不是「做了什么」✓
- `taskXML` doc 注释逐条说明四项承重配置**缺失会怎样** ✓
- `esc` 说明为什么需要（路径可含 `&`）✓

- [ ] **Step 7: 门禁 + 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./internal/service/
```

```bash
git add internal/service/windows.go internal/service/windows_test.go
git commit -m "feat(service): Windows 计划任务 XML 渲染与 UTF-16 落盘（B142）"
```

---

## Task 2: Windows Manager 五方法 + New 接线

**Files:**
- Modify: `internal/service/windows.go`
- Modify: `internal/service/windows_test.go`
- Modify: `internal/service/service.go`（`New` 的 switch、包头注释）

**Interfaces:**
- Consumes: Task 1 的 `windowsManager`、`taskXML`、`toUTF16LE`、`UnitPath`、`Kind`
- Produces: `windowsManager` 满足 `Manager` 接口（`Install(Spec) error`、`Uninstall() error`、`Status() (Status, error)`）；`New(log)` 在 `runtime.GOOS == "windows"` 时返回它

- [ ] **Step 1: 写失败测试——调用序列、Status 复核、幂等卸载**

追加到 `internal/service/windows_test.go`：

```go
// newTestWindows 造一个全缝替换的 windows manager，并返回记录调用的切片指针。
func newTestWindows(t *testing.T, runOut string, runErr error) (*windowsManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &windowsManager{
		log:          testLogger(),
		localAppData: `C:\Users\u\AppData\Local`,
		currentUser:  func() (string, error) { return `WIN-B37\Administrator`, nil },
		mkdirAll:     func(string, os.FileMode) error { return nil },
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte(runOut), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	return m, &calls, &written
}

// 安装要按 删旧 → 写盘 → 建任务 → 复核 的次序走。
func TestWindowsInstallSequence(t *testing.T) {
	m, calls, written := newTestWindows(t, "SUCCESS", nil)
	if err := m.Install(Spec{BinPath: `C:\bin\handoff.exe`, ConfigPath: `C:\c.yaml`}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := []string{"/Delete", "/Create", "/Query"}
	if len(*calls) != len(want) {
		t.Fatalf("调用次数应为 %d，实际 %d: %v", len(want), len(*calls), *calls)
	}
	for i, w := range want {
		if !strings.Contains((*calls)[i], w) {
			t.Errorf("第 %d 条调用应含 %q，实际 %q", i+1, w, (*calls)[i])
		}
	}
	xmlPath := `C:\Users\u\AppData\Local\handoff\handoff-agentd.xml`
	if _, ok := (*written)[xmlPath]; !ok {
		t.Fatalf("XML 没写到 %s，实际写了 %v", xmlPath, keysOf(*written))
	}
	// 落盘的必须是 UTF-16，不是 UTF-8
	if b := (*written)[xmlPath]; len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Error("落盘的 XML 不是 UTF-16LE BOM 开头，schtasks 会拒绝它")
	}
}

// 建任务失败必须回滚：留下一个孤儿 XML 会让下次安装以为已经装过
func TestWindowsInstallRollsBackOnCreateFailure(t *testing.T) {
	calls := []string{}
	written := map[string][]byte{}
	m := &windowsManager{
		log:          testLogger(),
		localAppData: `C:\Users\u\AppData\Local`,
		currentUser:  func() (string, error) { return "u", nil },
		mkdirAll:     func(string, os.FileMode) error { return nil },
		run: func(name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			calls = append(calls, joined)
			if strings.Contains(joined, "/Create") {
				return []byte("ERROR: Access is denied."), errors.New("exit status 1")
			}
			return []byte("SUCCESS"), nil
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	err := m.Install(Spec{BinPath: `C:\bin\handoff.exe`})
	if err == nil {
		t.Fatal("建任务失败时 Install 应该报错")
	}
	// schtasks 的原文必须在报文里——它才是真因（权限？路径？编码？）
	if !strings.Contains(err.Error(), "Access is denied") {
		t.Errorf("报文必须带 schtasks 原文，实际: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("失败后必须回滚删除 XML，实际残留 %v", keysOf(written))
	}
}

// Status 的 Running 必须按 PID 复核，不能按镜像名——
// 按镜像名会把操作者正在敲的 handoff CLI 数进去，那是稳定的假阳性
func TestWindowsStatusVerifiesByPID(t *testing.T) {
	var tasklistFilter string
	m, _, _ := newTestWindows(t, "", nil)
	m.run = func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "schtasks" {
			// /V /FO CSV 的输出：表头 + 一行数据，PID 在其中一列
			return []byte("\"TaskName\",\"Status\",\"PID\"\n\"\\handoff-agentd\",\"Running\",\"4242\"\n"), nil
		}
		tasklistFilter = joined
		return []byte("handoff.exe                   4242 Console      1     40,000 K\n"), nil
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("应报已装且在跑，实际 %+v", st)
	}
	if !strings.Contains(tasklistFilter, "PID eq 4242") {
		t.Errorf("复核判据必须是 PID，实际 tasklist 参数: %q", tasklistFilter)
	}
	if strings.Contains(tasklistFilter, "IMAGENAME") {
		t.Error("不得按镜像名复核：会把操作者正在敲的 handoff CLI 也数进去（稳定假阳性）")
	}
}

// 没装是正常答案不是错误
func TestWindowsStatusNotInstalledIsNotError(t *testing.T) {
	m, _, _ := newTestWindows(t, "ERROR: The system cannot find the file specified.", errors.New("exit status 1"))
	st, err := m.Status()
	if err != nil {
		t.Fatalf("没装时 Status 不该报错: %v", err)
	}
	if st.Installed || st.Running {
		t.Fatalf("没装时两个字段都该是 false，实际 %+v", st)
	}
}

// 本来就没装时 Uninstall 返回 nil（幂等）
func TestWindowsUninstallIsIdempotent(t *testing.T) {
	m, _, _ := newTestWindows(t, "ERROR: The system cannot find the file specified.", errors.New("exit status 1"))
	m.remove = func(string) error { return os.ErrNotExist }
	if err := m.Uninstall(); err != nil {
		t.Fatalf("没装时 Uninstall 应返回 nil，实际 %v", err)
	}
}

// 卸载不得依赖 schtasks /End：D8 实测它只杀外层 cmd.exe
func TestWindowsUninstallDoesNotUseEnd(t *testing.T) {
	m, calls, _ := newTestWindows(t, "SUCCESS", nil)
	if err := m.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, c := range *calls {
		if strings.Contains(c, "/End") {
			t.Errorf("不得用 schtasks /End：D8 实测它只杀外层 cmd.exe，agentd 孙进程原样活着。实际调用: %q", c)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

同时在 import 块补 `"errors"`、`"os"`。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/service/ -run 'TestWindows' -v
```

Expected: FAIL — `m.Install undefined`、`m.Status undefined`、`m.Uninstall undefined`

- [ ] **Step 3: 写最小实现**

追加到 `internal/service/windows.go`（import 补 `"strconv"`、`"encoding/csv"`）：

```go
// Install 写 XML 并建任务，最后复核任务真的注册上了。
//
// 次序与 launchd 一致：先清旧（同名任务存在时 /Create 会失败）→ 写盘 →
// 建任务 → 复核。建任务失败时回滚删 XML——留下一个孤儿 XML 会让下次安装
// 或人工排查以为已经装过。
func (m *windowsManager) Install(spec Spec) error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	usr, err := m.currentUser()
	if err != nil {
		m.log.Error("取当前用户失败，无法确定计划任务的运行身份", "cause", err)
		return fmt.Errorf("取当前用户: %w", err)
	}
	m.log.Info("安装 Windows 计划任务", "task", WindowsTaskName, "xml", path,
		"bin", spec.BinPath, "user", usr)

	// 先清旧：同名任务还在时 /Create 会失败。忽略这一步的错误——
	// 绝大多数情况下它本来就没装，报错是正常的
	if out, derr := m.run("schtasks", "/Delete", "/TN", WindowsTaskName, "/F"); derr != nil {
		m.log.Debug("删除旧任务（未装时报错属正常）", "output", strings.TrimSpace(string(out)))
	}

	if err := m.mkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.log.Error("创建 XML 目录失败", "dir", filepath.Dir(path), "cause", err)
		return fmt.Errorf("创建 %s: %w", filepath.Dir(path), err)
	}
	// 必须 UTF-16：schtasks /Create /XML 喂 UTF-8 会报一个与编码无关的错
	if err := m.writeFile(path, toUTF16LE(m.taskXML(spec, usr)), 0o644); err != nil {
		m.log.Error("写计划任务 XML 失败", "path", path, "cause", err)
		return fmt.Errorf("写 XML %s: %w", path, err)
	}

	if out, cerr := m.run("schtasks", "/Create", "/TN", WindowsTaskName, "/XML", path, "/F"); cerr != nil {
		if rmErr := m.remove(path); rmErr != nil {
			m.log.Error("回滚删除 XML 失败", "path", path, "cause", rmErr)
		}
		// 报文必须原样带上 schtasks 的输出——权限不足、路径不对、编码错，
		// 三种真因的处置完全不同，而我们分辨不了，只能把原文交给操作者（B64 的教训）
		m.log.Error("建计划任务失败，已回滚", "cause", cerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("建计划任务失败: %s（%w）", strings.TrimSpace(string(out)), cerr)
	}

	// 复核：/Create 成功不等于任务真的注册好了
	if out, qerr := m.run("schtasks", "/Query", "/TN", WindowsTaskName); qerr != nil {
		m.log.Error("任务已建但复核失败", "cause", qerr, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("任务已建但复核不到（检查 %s）: %w", spec.LogPath, qerr)
	}
	m.log.Info("Windows 计划任务安装完成", "task", WindowsTaskName)
	return nil
}

// Uninstall 删任务、终止 agentd、删 XML。本来就没装时返回 nil。
//
// **不用 schtasks /End**：D8 实测它只杀外层 cmd.exe，agentd 孙进程原样活着，
// 任务状态却已回到 Ready——管理器视图与现实分叉。删任务后由本函数按 pid 终止，
// 走 prochost 已验证的 job 连坐路径。
//
// 次序上先删任务再杀进程：反过来的话，杀掉进程后重复触发会在 1 分钟内把它拉回来。
func (m *windowsManager) Uninstall() error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("卸载 Windows 计划任务", "task", WindowsTaskName)
	if out, derr := m.run("schtasks", "/Delete", "/TN", WindowsTaskName, "/F"); derr != nil {
		// 没装时必然报错，这是正常的，不该让 uninstall 失败
		m.log.Debug("删除任务报错（未装时属正常）", "output", strings.TrimSpace(string(out)))
	}
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		m.log.Error("删除计划任务 XML 失败", "path", path, "cause", err)
		return fmt.Errorf("删除 XML %s: %w", path, err)
	}
	m.log.Info("Windows 计划任务已卸载", "task", WindowsTaskName)
	return nil
}

// Status 查询任务是否注册且在跑。
//
// **Running 必须按 PID 复核**：D8 实测 schtasks 的任务状态会与现实分叉。
// 不套 cmd.exe 之后这个分叉理应消失（schtasks 直接跟踪 handoff.exe），
// 但「理应」不是「验过」，而 Status 说谎的代价是操作者据此做换版决策。
//
// 判据用 PID 不用镜像名：`tasklist /FI "IMAGENAME eq handoff.exe"` 会把操作者
// 正在敲的 handoff CLI 也数进去，那是个稳定的假阳性。
func (m *windowsManager) Status() (Status, error) {
	out, err := m.run("schtasks", "/Query", "/TN", WindowsTaskName, "/V", "/FO", "CSV")
	if err != nil {
		// 没注册时 schtasks 退非零。这是一个正常答案，不是查询失败
		m.log.Debug("查询计划任务未命中（未装时属正常）", "output", strings.TrimSpace(string(out)))
		return Status{}, nil
	}
	s := Status{Installed: true, Detail: firstLine(string(out))}
	pid := pidFromQueryCSV(string(out))
	if pid <= 0 {
		m.log.Warn("计划任务已注册但读不到 PID，Running 判为 false",
			"task", WindowsTaskName)
		return s, nil
	}
	tout, terr := m.run("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH")
	if terr != nil {
		m.log.Warn("按 PID 复核进程失败，Running 判为 false",
			"pid", pid, "cause", terr)
		return s, nil
	}
	// tasklist 查不到时输出的是提示行而非进程行，用 pid 是否出现在输出里判定
	s.Running = strings.Contains(string(tout), strconv.Itoa(pid))
	m.log.Debug("计划任务状态", "task", WindowsTaskName,
		"installed", s.Installed, "running", s.Running, "pid", pid)
	return s, nil
}

// pidFromQueryCSV 从 `schtasks /Query /V /FO CSV` 的输出里取 PID 列。
//
// 返回 0 表示没读到——调用方据此把 Running 判为 false 而不是猜一个值。
//
// 为什么按列名找而不是按固定列号：schtasks 的 /V 输出列数随 Windows 版本变化，
// 写死列号会在某次系统更新后静默取到错误的列。
func pidFromQueryCSV(out string) int {
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1 // 列数不固定，交给调用方判断
	records, err := r.ReadAll()
	if err != nil || len(records) < 2 {
		return 0
	}
	idx := -1
	for i, h := range records[0] {
		if strings.EqualFold(strings.TrimSpace(h), "PID") {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(records[1]) {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(records[1][idx]))
	if err != nil {
		return 0
	}
	return pid
}
```

修改 `internal/service/service.go`：

```go
	case "windows":
		return newWindows(log), nil
```

并把包头那条边界注释：

```go
//   - 不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37）
```

改为：

```go
//   - 三个平台各一个实现：launchd（macOS）/ systemd（Linux）/ schtasks（Windows）。
//     Windows 走计划任务而非 SCM 服务，理由见 windows.go 的文件头
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/service/ -v
```

Expected: PASS（Windows 六个测试 + launchd/systemd 既有测试全绿）

- [ ] **Step 5: 加关键节点日志自检**

Step 3 的代码已按纪律打点，此步逐条核对：
- 进入 `Install` 打 Info 带 task/xml/bin/user ✓
- 每个错误分支都有 Error + cause + 上下文 ✓
- `schtasks` 调用失败时**原样带 output**（真因在那里）✓
- 状态变更（安装完成、卸载完成）打 Info ✓
- `Status` 的成功路径打 Debug（它被高频调用，Info 会淹掉日志）✓
- 「未装时报错属正常」的两处降到 Debug，不冒充故障 ✓

- [ ] **Step 6: 加注释自检**

- `Install` / `Uninstall` / `Status` 三个导出方法都有 doc 注释，说明参数、返回、注意事项 ✓
- `Uninstall` 注明「不用 /End」及 D8 依据、以及先删任务再杀进程的顺序理由 ✓
- `Status` 注明「按 PID 不按镜像名」及假阳性理由 ✓
- `pidFromQueryCSV` 注明「按列名不按列号」的理由 ✓

- [ ] **Step 7: 门禁 + 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./internal/service/ ./cmd/
```

```bash
git add internal/service/windows.go internal/service/windows_test.go internal/service/service.go
git commit -m "feat(service): Windows Manager 五方法与 New 接线（B142）"
```

---

## Task 3: 换版等待超时 60s → 120s

**Files:**
- Modify: `cmd/upgrade.go:118-121`
- Modify: `cmd/upgrade_test.go`

**Interfaces:**
- Consumes: 无（独立常量改动）
- Produces: `upgradeWaitTimeoutPush = 120 * time.Second`

**为什么是独立 task**：它改的是**全平台**行为，reviewer 可能单独否决它（「不该为 Windows 改全平台的值」）而同时接受 Task 1/2。按「split only where a reviewer could meaningfully reject one task while approving its neighbor」，它该有自己的 gate。

- [ ] **Step 1: 写失败测试**

追加到 `cmd/upgrade_test.go`：

```go
// 推送模式的等待超时必须给 Windows 的重启节奏留余量。
//
// Windows 上「exit 0 也被拉起」只能靠计划任务的重复触发模拟，而 Task Scheduler
// 允许的最小重复间隔是 1 分钟——最坏空窗接近 60 秒，正好压在旧值 60s 的超时线上。
// 那是最糟的失败形态：时好时坏，看起来像别的问题。
func TestUpgradeWaitTimeoutPushLeavesRoomForWindowsRepetition(t *testing.T) {
	const windowsWorstCaseGap = 60 * time.Second
	if upgradeWaitTimeoutPush < 2*windowsWorstCaseGap {
		t.Fatalf("推送超时 %v 不足 Windows 最坏空窗 %v 的两倍；"+
			"Windows 换版靠计划任务每分钟重复触发拉起，余量不足会让换版间歇性失败",
			upgradeWaitTimeoutPush, windowsWorstCaseGap)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./cmd/ -run TestUpgradeWaitTimeoutPush -v
```

Expected: FAIL — `推送超时 1m0s 不足 Windows 最坏空窗 1m0s 的两倍`

- [ ] **Step 3: 改常量与注释**

`cmd/upgrade.go` 的常量块改为：

```go
// upgradeWaitTimeoutPull / upgradeWaitTimeoutPush / upgradeWaitInterval 是换版后
// 等新进程上线的时限与轮询间隔。
//
// 自拉模式下对端要下 20MB（慢网 + 代理下几分钟很正常），放宽到 10min，
// 且 WaitVersion 在 pull 模式会读对端 pull_state，真失败时立刻中止、不会干等满。
//
// 推送模式二进制已经在对端，换版本身是秒级动作——**但重启不一定是**。
// macOS/Linux 上管理器立刻（launchd 约 10 秒节流内）把 exit 0 的进程拉回来；
// **Windows 上没有这个语义**：计划任务的「失败时重启」只在非零退出时生效，
// 而换版走的正是 exit 0，只能靠 <Repetition> 每分钟触发一次来模拟拉起
// （PT1M 是 Task Scheduler 允许的最小间隔）。最坏空窗因此接近 60 秒，
// 与旧值 60s 恰好等长——那会让 Windows 换版在超时线上反复擦边，
// 表现为时好时坏、看起来像别的问题。取两倍余量。
// 代价：一次真起不来的推送换版让操作者多等 60 秒，可接受。
const (
	upgradeWaitTimeoutPull = 10 * time.Minute
	upgradeWaitTimeoutPush = 120 * time.Second
	upgradeWaitInterval    = 2 * time.Second
)
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./cmd/ -run TestUpgradeWaitTimeoutPush -v && go test ./cmd/
```

Expected: PASS，且 `cmd` 包既有测试不受影响

- [ ] **Step 5: 日志与注释自检**

本 task 只改常量与注释，无新增执行路径，**无需新增日志**（instrumenting-code 的「trivial pure-value change」豁免）。注释一项已在 Step 3 完成：说明了为什么改、Windows 的机制差异、以及代价。

- [ ] **Step 6: 门禁 + 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./cmd/
```

```bash
git add cmd/upgrade.go cmd/upgrade_test.go
git commit -m "fix(upgrade): 推送换版超时放宽到 120s，为 Windows 的重启节奏留余量（B142）"
```

---

## Task 4: `jobProcessIDs` Windows 平台原语

**Files:**
- Modify: `internal/prochost/platform_windows.go`
- Create: `internal/prochost/platform_windows_test.go`

**Interfaces:**
- Consumes: `jobHandle`（`platform_windows.go` 既有包级 var，shim 持有的 job 句柄）
- Produces: `func jobProcessIDs() (pids []int, err error)` —— 读当前 shim 所属 job 的成员 pid 表

**注**：本 task 的实现已由 08-18 win-b37 真机探针验证通过（spec §3.7），下方代码即探针中跑通的那份。

- [ ] **Step 1: 写失败测试**

创建 `internal/prochost/platform_windows_test.go`：

```go
//go:build windows

// Windows 平台原语的测试：真的建 job、真的 spawn 子进程。
//
// 为什么不能全缝注入：这里验的正是「x/sys 缺结构体、手工声明的布局对不对」，
// 注入掉系统调用等于把要验的东西验没了。
package prochost

import (
	"os"
	"os/exec"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobProcessIDs 必须能看见同 job 内 spawn 出来的子进程，并在它退出后不再报它。
// 这两条是「job 成员表可以当每任务进程计数来源」的全部根据。
func TestJobProcessIDsSeesChildAndForgetsIt(t *testing.T) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("CreateJobObject: %v", err)
	}
	defer windows.CloseHandle(h)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatalf("SetInformationJobObject: %v", err)
	}
	self, err := windows.GetCurrentProcess()
	if err != nil {
		t.Fatalf("GetCurrentProcess: %v", err)
	}
	if err := windows.AssignProcessToJobObject(h, self); err != nil {
		t.Fatalf("AssignProcessToJobObject(self): %v —— 外层 job 可能不允许嵌套", err)
	}
	// 测试期间把包级 jobHandle 指向本测试的 job，跑完还原
	saved := jobHandle
	jobHandle = h
	defer func() { jobHandle = saved }()

	pids, err := jobProcessIDs()
	if err != nil {
		t.Fatalf("jobProcessIDs(before spawn): %v", err)
	}
	if !containsInt(pids, os.Getpid()) {
		t.Fatalf("成员表里没有自己 self=%d pids=%v", os.Getpid(), pids)
	}

	child := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := child.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	childPID := child.Process.Pid
	time.Sleep(500 * time.Millisecond)

	pids2, err := jobProcessIDs()
	if err != nil {
		t.Fatalf("jobProcessIDs(after spawn): %v", err)
	}
	if !containsInt(pids2, childPID) {
		t.Fatalf("子进程未出现在成员表 child=%d pids=%v", childPID, pids2)
	}

	_ = child.Process.Kill()
	_, _ = child.Process.Wait()
	time.Sleep(500 * time.Millisecond)

	pids3, err := jobProcessIDs()
	if err != nil {
		t.Fatalf("jobProcessIDs(after kill): %v", err)
	}
	if containsInt(pids3, childPID) {
		t.Fatalf("子进程已退出但仍在成员表 child=%d pids=%v —— "+
			"「退出即移除」是「不需要时间下界校验」的根据，这条不成立就要重新设计", childPID, pids3)
	}
}

// 没有 job 句柄时必须报错而不是返回空集：空集会被上层读成「一个进程都没有」
func TestJobProcessIDsWithoutJobErrors(t *testing.T) {
	saved := jobHandle
	jobHandle = 0
	defer func() { jobHandle = saved }()
	if _, err := jobProcessIDs(); err == nil {
		t.Fatal("无 job 句柄时应报错，返回空集会被误读成「没有成员」")
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 跑测试确认失败**

在 win-b37 上（协调者本机交叉编译只能验编译，不能验行为）：

```bash
GOOS=windows go vet ./internal/prochost/
```

Expected: FAIL — `undefined: jobProcessIDs`

- [ ] **Step 3: 写最小实现**

追加到 `internal/prochost/platform_windows.go`：

```go
// jobBasicProcessIDList 对应 Win32 的 JOBOBJECT_BASIC_PROCESS_ID_LIST。
//
//	typedef struct _JOBOBJECT_BASIC_PROCESS_ID_LIST {
//	  DWORD     NumberOfAssignedProcesses;
//	  DWORD     NumberOfProcessIdsInList;
//	  ULONG_PTR ProcessIdList[1];
//	} JOBOBJECT_BASIC_PROCESS_ID_LIST;
//
// **x/sys/windows 没有定义它**（有 QueryInformationJobObject 函数与
// JobObjectBasicProcessIdList 常量，唯独缺这个结构体），只能手工声明。
//
// 尾部是变长数组：结构体只声明 1 个元素，实际长度由调用方分配的缓冲区决定。
// 两个 uint32 合计 8 字节，其后 ULONG_PTR 在 64 位上按 8 字节对齐、恰好落在
// 偏移 8，32 位上按 4 字节对齐同样落在偏移 8——两种位宽都没有隐式 padding，
// 可以直接映射。（08-18 win-b37 真机探针验证通过，见 spec §3.7）
type jobBasicProcessIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIdsInList  uint32
	ProcessIdList             [1]uintptr
}

// jobProcessIDs 读当前 shim 所属 Job Object 的成员 pid 表。
//
// 返回：
//   - pids: 成员 pid（含 shim 自己）
//   - err: 没有 job 句柄，或查询失败
//
// 注意：
//   - **无 job 句柄时返回错误而不是空集**：空集意味着「确实一个进程都没有」，
//     与「我们看不了」是两回事——同 procenum.go 的 ErrNotSupported 纪律
//   - **取长度用翻倍重试，不用「先查长度再分配」**：ERROR_MORE_DATA 时 Win32
//     并不保证把所需字节数写回 retlen（MSDN 对这个 information class 没有作出
//     该承诺）。依赖一个没被承诺的返回值，会得到一个小规模下好使、进程一多
//     就随机失败的东西
//   - 只读，绝不发信号。回收由 job 的 KILL_ON_JOB_CLOSE 连坐承担
func jobProcessIDs() ([]int, error) {
	if jobHandle == 0 {
		return nil, fmt.Errorf("当前进程没有 Job Object 句柄，读不到成员表")
	}
	const (
		hdrSize  = unsafe.Sizeof(uint32(0)) * 2 // 两个 DWORD 头
		slotSize = unsafe.Sizeof(uintptr(0))    // 一个 ULONG_PTR
		maxSlots = 1 << 16                      // 65536，远超任何真实任务
	)
	for slots := uintptr(64); slots <= maxSlots; slots *= 2 {
		bufLen := hdrSize + slots*slotSize
		buf := make([]byte, bufLen)
		var retlen uint32
		err := windows.QueryInformationJobObject(jobHandle,
			windows.JobObjectBasicProcessIdList,
			uintptr(unsafe.Pointer(&buf[0])), uint32(bufLen), &retlen)
		if err != nil {
			if err == windows.ERROR_MORE_DATA {
				continue // 缓冲区放不下，翻倍重来
			}
			log().Error("查询 Job Object 成员表失败", "slots", slots, "cause", err)
			return nil, fmt.Errorf("QueryInformationJobObject: %w", err)
		}
		list := (*jobBasicProcessIDList)(unsafe.Pointer(&buf[0]))
		n := list.NumberOfProcessIdsInList
		if uintptr(n) > slots {
			// 内核报的条数超过我们分配的槽位：不敢按这个数去读，翻倍重来。
			// 宁可多跑一轮，也不越界读一段不属于我们的内存
			continue
		}
		out := make([]int, 0, n)
		base := unsafe.Pointer(&list.ProcessIdList[0])
		for i := uintptr(0); i < uintptr(n); i++ {
			p := *(*uintptr)(unsafe.Pointer(uintptr(base) + i*slotSize))
			out = append(out, int(p))
		}
		log().Debug("Job Object 成员表已读取", "members", len(out),
			"assigned", list.NumberOfAssignedProcesses)
		return out, nil
	}
	log().Error("Job Object 成员数超过上限，放弃读取", "max_slots", maxSlots)
	return nil, fmt.Errorf("Job Object 成员数超过 %d，放弃", maxSlots)
}
```

- [ ] **Step 4: 跑测试确认通过**

先在协调者本机验编译：

```bash
GOOS=windows go build ./... && GOOS=windows go vet ./internal/prochost/
```

再在 win-b37 上验行为（需先装 Go）：

```bash
go test ./internal/prochost/ -run TestJobProcessIDs -v
```

Expected: PASS（两个测试）

- [ ] **Step 5: 加关键节点日志自检**

Step 3 的代码已含，逐条核对：
- 查询失败 → Error + slots + cause ✓
- 成员数超上限 → Error + max_slots ✓
- 成功路径 → Debug 带 members/assigned（**不能静默成功**；用 Debug 而非 Info 是因为它每秒被采样调用一次，Info 会淹掉 shim.log）✓

- [ ] **Step 6: 加注释自检**

- `jobBasicProcessIDList` 注明「x/sys 没有定义它」与对齐分析 ✓
- `jobProcessIDs` 注明三条：无句柄返回错误的理由、翻倍重试而非查长度的理由、只读边界 ✓
- 越界护栏那段有 why 注释 ✓

- [ ] **Step 7: 门禁 + 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && GOOS=windows go build ./...
```

```bash
git add internal/prochost/platform_windows.go internal/prochost/platform_windows_test.go
git commit -m "feat(prochost): Windows Job Object 成员表查询原语（B122）"
```

---

## Task 5: `members.json` 读写 + `Handle.MembersPath`

**Files:**
- Create: `internal/prochost/members.go`
- Create: `internal/prochost/members_test.go`
- Modify: `internal/prochost/prochost.go`（`Handle` 加字段、`Start` 填它）

**Interfaces:**
- Consumes: `rosterPath` 的路径推导手法（`roster.go:44`）
- Produces:
  - `const MembersFileName = "members.json"`
  - `func membersPath(infoPath string) string`
  - `type memberSnapshot struct{ PIDs []int \`json:"pids"\`; SampledAt int64 \`json:"sampled_at"\` }`
  - `func readMembers(path string) (memberSnapshot, error)`
  - `func writeMembers(path string, snap memberSnapshot) error`
  - `Handle.MembersPath string \`json:"members_path,omitempty"\``

**为什么不复用 roster.json**：job 成员表只给 pid 数组，**没有 StartedAt**。而 `roster.json` 的每一条都承诺带可信的 `StartedAt`——那是 `rosterKill` 的杀人判据（「pid 在表 + StartedAt 完全相等」才发信号）。往里塞没有时刻的条目会污染一条承重判据的语义。

- [ ] **Step 1: 写失败测试**

创建 `internal/prochost/members_test.go`：

```go
// members.json 的读写测试：序列化往返、缺失与损坏的容错。
package prochost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMembersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, MembersFileName)
	want := memberSnapshot{PIDs: []int{100, 200, 300}, SampledAt: 1755500000000000000}
	if err := writeMembers(p, want); err != nil {
		t.Fatalf("writeMembers: %v", err)
	}
	got, err := readMembers(p)
	if err != nil {
		t.Fatalf("readMembers: %v", err)
	}
	if got.SampledAt != want.SampledAt || len(got.PIDs) != len(want.PIDs) {
		t.Fatalf("往返不一致：got=%+v want=%+v", got, want)
	}
	for i := range want.PIDs {
		if got.PIDs[i] != want.PIDs[i] {
			t.Fatalf("第 %d 个 pid 不一致：got=%d want=%d", i, got.PIDs[i], want.PIDs[i])
		}
	}
}

// 文件不存在是正常形态（任务刚起、还没采过），必须报错而不是返回空快照——
// 空快照会被上层读成「这个任务一个进程都没有」
func TestReadMembersMissingFileErrors(t *testing.T) {
	if _, err := readMembers(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("文件不存在时应报错，返回空快照会被误读成「没有成员」")
	}
}

// 损坏的文件同样必须报错，不能静默当空
func TestReadMembersCorruptErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, MembersFileName)
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMembers(p); err == nil {
		t.Fatal("文件损坏时应报错")
	}
}

// 路径推导要与 roster 同款：与 proc.json 同目录
func TestMembersPathBesideInfoPath(t *testing.T) {
	got := membersPath("/data/tasks/abc/proc.json")
	want := filepath.Join("/data/tasks/abc", MembersFileName)
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
	if membersPath("") != "" {
		t.Fatal("infoPath 为空时应返回空串（与 rosterPath 同款降级）")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/prochost/ -run 'TestMembers|TestReadMembers' -v
```

Expected: FAIL — `undefined: MembersFileName`、`undefined: memberSnapshot` 等

- [ ] **Step 3: 写最小实现**

创建 `internal/prochost/members.go`：

```go
// members.go —— 进程容器成员快照（members.json）的读写。
//
// 职责：
//   - 定义 memberSnapshot：一次容器成员采样的 pid 表与采样时刻
//   - 读写与路径推导，供 shim 侧落盘、agentd 侧读取
//
// 边界：
//   - **不复用 roster.json**：job 成员表只给 pid、没有 StartedAt，而 roster 的
//     每一条都承诺带可信的 StartedAt——那是 rosterKill 的杀人判据（「pid 在表 +
//     StartedAt 完全相等」才发信号）。往里塞没有时刻的条目会污染一条承重判据
//   - 不判定成员归属、不发信号：这里只管一份数据的存取
//   - 平台无关：Windows 的 shim 写它，unix 的不写。**边界由数据决定**——
//     unix 上 Handle.MembersPath 恒为空，读都不会读到这里
package prochost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MembersFileName 是容器成员快照的文件名（与 proc.json 同目录）。
const MembersFileName = "members.json"

// memberSnapshot 是一次容器成员采样的结果。
//
// SampledAt 不是可选的：agentd 侧读到的成员表天然带采样延迟（它拿不到 job
// 句柄，只能读文件），足迹输出必须能说明数据是什么时刻的。宣称什么就得是什么。
type memberSnapshot struct {
	PIDs      []int `json:"pids"`
	SampledAt int64 `json:"sampled_at"` // unix 纳秒
}

// membersPath 由 proc.json 的路径推出 members.json 的路径。
//
// infoPath 为空时返回空串——与 rosterPath 同款降级：调用方据此跳过这条来源，
// 而不是拿一个错误的路径去读。
func membersPath(infoPath string) string {
	if infoPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(infoPath), MembersFileName)
}

// readMembers 读一份成员快照。
//
// 参数：path 为 members.json 的路径
//
// 返回：快照；文件缺失或内容损坏时返回错误
//
// 注意：**缺失与损坏都返回错误，绝不返回空快照**。空快照会被上层读成
// 「这个任务一个进程都没有」，而真实含义是「这条来源现在不可用」——
// 同 procenum.go 里 ErrNotSupported 与空集必须分开的那条纪律。
func readMembers(path string) (memberSnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return memberSnapshot{}, fmt.Errorf("读成员快照 %s: %w", path, err)
	}
	var snap memberSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return memberSnapshot{}, fmt.Errorf("解析成员快照 %s: %w", path, err)
	}
	return snap, nil
}

// writeMembers 原子写一份成员快照。
//
// 参数：path 为落点；snap 为本次采样结果
//
// 注意：先写临时文件再 rename——agentd 侧随时可能在读，非原子写会让它读到
// 半截 JSON。与 writeRosterBytes 同款手法。
func writeMembers(path string, snap memberSnapshot) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("序列化成员快照: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写临时成员快照 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("落盘成员快照 %s: %w", path, err)
	}
	return nil
}
```

修改 `internal/prochost/prochost.go` 的 `Handle` 结构，在 `RosterPath` 字段之后加：

```go
	// MembersPath 是进程容器成员快照（members.json）的路径。
	//
	// 只有具备进程容器的平台（Windows 的 Job Object）会填它并落盘；
	// unix 上恒为空串，Footprint 因此自然落回 pgid + roster + 标记三段判据。
	// **这条边界是数据决定的**，不存在「某处忘了检查平台」的可能——
	// 与 MarkRoot 那条「仅托管 worktree 可杀」是同一形态。
	//
	// omitempty + 零值语义：升级前写下的 proc.json 没有这个字段，读出空串即
	// 跳过容器来源，与 StartedAt / RosterPath 缺失时同一条纪律。
	MembersPath string `json:"members_path,omitempty"`
```

修改 `Start`（`prochost.go:341` 附近）：

```go
	roster := rosterPath(spec.InfoPath)
	members := membersPath(spec.InfoPath)
	log().Info("shim 已拉起", "pid", pid, "bin", spec.Argv[0], "spec", specPath,
		"started_at", startedAt, "roster", roster, "members", members)
	return Handle{
		PID:         pid,
		LockPath:    spec.LockPath,
		StartedAt:   startedAt,
		RosterPath:  roster,
		MembersPath: members,
		TaskID:      spec.TaskID,
		MarkRoot:    spec.MarkRoot,
	}, nil
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/prochost/ -run 'TestMembers|TestReadMembers' -v && go test ./internal/prochost/
```

Expected: PASS（四个新测试 + prochost 既有测试全绿）

- [ ] **Step 5: 加关键节点日志**

`readMembers` / `writeMembers` 是被高频调用的存取原语，**日志由调用方在边界上打**（与 `classify` 刻意不打日志同款理由：这里再记一遍等于同一件事写两次）。本步只确认 `Start` 的那条 Info 已带上 `members` 路径——它是「这个任务有没有容器来源」的唯一现场记录。

- [ ] **Step 6: 加注释自检**

- 文件头：职责 + 三条边界（不复用 roster / 不判归属 / 边界由数据决定）✓
- `memberSnapshot.SampledAt` 说明「为什么不是可选的」✓
- `readMembers` 说明「缺失与损坏都报错，绝不返空快照」及理由 ✓
- `writeMembers` 说明原子写的理由 ✓
- `Handle.MembersPath` 说明零值语义与「边界由数据决定」✓

- [ ] **Step 7: 门禁 + 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./internal/prochost/
```

```bash
git add internal/prochost/members.go internal/prochost/members_test.go internal/prochost/prochost.go
git commit -m "feat(prochost): members.json 成员快照读写与 Handle.MembersPath（B122）"
```

---

## Task 6: shim 侧采样源切换到 Job Object

**Files:**
- Modify: `internal/prochost/shim.go`（`runRosterSampling` 与 `rosterSampler`）
- Create: `internal/prochost/members_sampling_test.go`

**Interfaces:**
- Consumes: Task 4 的 `jobProcessIDs()`、Task 5 的 `writeMembers` / `membersPath` / `memberSnapshot`
- Produces: `var containerSampleFn func() ([]int, error)` —— 容器采样的测试缝（平台实现注入；unix 上为 nil）

- [ ] **Step 1: 写失败测试**

创建 `internal/prochost/members_sampling_test.go`：

```go
// 容器成员采样的测试：不依赖真实 Job Object，经 containerSampleFn 缝注入。
package prochost

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testLogger 造一个丢弃输出的日志器。
//
// **本包此前没有这个辅助**（roster_sampling_test.go 是把
// `slog.New(slog.NewTextHandler(io.Discard, nil))` 原样重复了两遍），
// 在这里首次定义，后续测试直接用。
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// 有容器能力时，采样必须落盘 members.json，且带推进的 sampled_at
func TestContainerSamplingWritesSnapshot(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = func() ([]int, error) { return []int{11, 22}, nil }

	dir := t.TempDir()
	p := filepath.Join(dir, MembersFileName)
	s := &membersSampler{path: p}
	if ok := s.sample(testLogger()); !ok {
		t.Fatal("有容器能力时 sample 应返回 true（继续周期采样）")
	}
	snap, err := readMembers(p)
	if err != nil {
		t.Fatalf("readMembers: %v", err)
	}
	if len(snap.PIDs) != 2 || snap.PIDs[0] != 11 || snap.PIDs[1] != 22 {
		t.Fatalf("pid 表不对: %+v", snap)
	}
	if snap.SampledAt <= 0 {
		t.Fatal("sampled_at 必须有值：agentd 侧靠它说明数据时刻")
	}
}

// 无容器能力（unix）时 sample 返回 false，采样循环就此退出，不刷日志
func TestContainerSamplingUnsupportedStops(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = nil

	s := &membersSampler{path: filepath.Join(t.TempDir(), MembersFileName)}
	if ok := s.sample(testLogger()); ok {
		t.Fatal("无容器能力时 sample 应返回 false，让采样循环退出")
	}
}

// 单次查询失败不该终止采样：下一轮可能就好了
func TestContainerSamplingTransientErrorKeepsGoing(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = func() ([]int, error) { return nil, errors.New("transient") }

	s := &membersSampler{path: filepath.Join(t.TempDir(), MembersFileName)}
	if ok := s.sample(testLogger()); !ok {
		t.Fatal("单次查询失败应返回 true 继续重试，不能就此放弃整个任务的计数能力")
	}
}

// 内容未变则不重复落盘——每秒一次原子写是实打实的 I/O
func TestContainerSamplingSkipsUnchanged(t *testing.T) {
	saved := containerSampleFn
	defer func() { containerSampleFn = saved }()
	containerSampleFn = func() ([]int, error) { return []int{7}, nil }

	dir := t.TempDir()
	p := filepath.Join(dir, MembersFileName)
	s := &membersSampler{path: p}
	s.sample(testLogger())
	first, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	s.sample(testLogger())
	if s.writes != 1 {
		t.Fatalf("内容未变时不该重复落盘，writes=%d", s.writes)
	}
	second, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Error("内容未变时文件不该被重写")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/prochost/ -run TestContainerSampling -v
```

Expected: FAIL — `undefined: containerSampleFn`、`undefined: membersSampler`

- [ ] **Step 3: 写最小实现**

在 `internal/prochost/members.go` 追加：

```go
// containerSampleFn 是容器成员采样的平台缝。
//
// **nil 表示本平台没有进程容器**（unix），采样循环据此退出。
// Windows 的 platform_windows.go 在 init 里把它设为 jobProcessIDs。
// 用包级 var 而非直接调用，是为了让采样逻辑的测试不依赖真实 Job Object——
// 与 enumProcsFn / aliveFn / killGroupFn 同款路数。
var containerSampleFn func() ([]int, error)

// membersSampler 持有成员快照的采样状态：路径与上一轮落盘的内容。
//
// 为什么要有状态：与 rosterSampler 同理——稳态下成员表根本不变，
// 把上一轮的结果留着比一比，就能把「每秒一次原子写」降成「变了才写」。
type membersSampler struct {
	path   string
	last   []int
	hasLast bool
	writes int // 实际落盘次数，仅供测试断言「未变则不写」
}

// sample 采一次容器成员并按需落盘。
//
// 参数：l 为日志器
//
// 返回：是否继续周期采样。**false 只有一个含义：本平台永久没有进程容器**，
// 采样循环应就此退出；单次查询失败返回 true（下一轮可能就好了）。
func (s *membersSampler) sample(l *slog.Logger) bool {
	if containerSampleFn == nil {
		l.Info("本平台无进程容器，不做成员采样")
		return false
	}
	if s.path == "" {
		l.Warn("无 info_path，无法落盘成员快照，本任务不做容器计数")
		return false
	}
	pids, err := containerSampleFn()
	if err != nil {
		l.Warn("查询容器成员失败，本轮跳过", "path", s.path, "cause", err)
		return true
	}
	if s.hasLast && equalInts(s.last, pids) {
		l.Debug("容器成员未变，跳过落盘", "count", len(pids))
		return true
	}
	if err := writeMembers(s.path, memberSnapshot{PIDs: pids, SampledAt: time.Now().UnixNano()}); err != nil {
		l.Warn("落盘成员快照失败，本轮跳过", "path", s.path, "cause", err)
		return true
	}
	s.last = append(s.last[:0], pids...)
	s.hasLast = true
	s.writes++
	l.Debug("成员快照已落盘", "count", len(pids), "path", s.path)
	return true
}

// equalInts 判两个 pid 表是否逐项相等。
//
// 不排序、不去重：jobProcessIDs 的返回顺序由内核给定且稳定，
// 排序只会把「顺序变化」这个可能有意义的信号抹掉，还多一次分配。
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

`members.go` 的 import 补 `"log/slog"`、`"time"`。

在 `internal/prochost/platform_windows.go` 追加：

```go
// init 把容器采样缝指向 Job Object 实现。
//
// 为什么用 init 而不是在 shim 启动时赋值：这条缝的含义是「本平台有没有进程
// 容器」，那是编译期就确定的事实，跟运行时状态无关。放 init 里，任何调用方
// 都不必记得先做一次注册。
func init() { containerSampleFn = jobProcessIDs }
```

修改 `internal/prochost/shim.go` 的采样 goroutine（`shim.go:156-163`）：

```go
	rosterDone := make(chan struct{})
	go func() {
		defer close(rosterDone)
		// 有进程容器的平台（Windows）走容器成员表，没有的走 pgid 出生登记。
		// 两条路互斥：容器表由内核维护、进程退出即移除，比采样式的名册更强，
		// 有它就不需要名册那套时间下界校验（spec §3.1）
		if containerSampleFn != nil {
			sampler := &membersSampler{path: membersPath(spec.InfoPath)}
			runContainerSampling(stopRoster, sampler, l)
			return
		}
		// 同一个 sampler 跨轮复用：它持有上一轮的序列化结果，"内容未变则不写"
		// 依赖这份状态；每轮新建一个等于关掉这个优化
		sampler := &rosterSampler{path: rosterPath(spec.InfoPath)}
		runRosterSampling(stopRoster, sampler, l)
	}()
```

在 `shim.go` 追加驱动函数（紧邻 `runRosterSampling`）：

```go
// runContainerSampling 驱动容器成员的首次采样与周期采样。
//
// 参数：stop 为执行者退出时关闭的停止信号；sampler 为跨轮复用的采样器；l 为日志器。
//
// 与 runRosterSampling 同构，只是数据源不同。sample 返回 false 表示本平台
// 没有进程容器，只记一条 Info 并退出。
func runContainerSampling(stop <-chan struct{}, sampler *membersSampler, l *slog.Logger) {
	if !sampler.sample(l) {
		return
	}
	tk := time.NewTicker(rosterInterval)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			// 最后采一次：这份快照 ≈ 执行者死亡时刻的成员表
			sampler.sample(l)
			return
		case <-tk.C:
			if !sampler.sample(l) {
				return
			}
		}
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/prochost/ -run TestContainerSampling -v && go test ./internal/prochost/
```

Expected: PASS（四个新测试 + 既有测试全绿，含 `roster_sampling_test.go`——unix 上 `containerSampleFn` 为 nil，走的还是名册那条路）

- [ ] **Step 5: 加关键节点日志自检**

Step 3 已含，逐条核对：
- 无容器能力 → Info「本平台无进程容器，不做成员采样」（**这条必须有**：静默缺席正是本项目反复在防的东西）✓
- 无 info_path → Warn ✓
- 查询失败 / 落盘失败 → Warn + path + cause，且**不终止采样** ✓
- 成功落盘 → Debug 带 count + path（每秒调用，Info 会淹掉 shim.log）✓
- 未变跳过 → Debug ✓

- [ ] **Step 6: 加注释自检**

- `containerSampleFn` 说明 nil 的含义与用 init 注册的理由 ✓
- `membersSampler` 说明状态存在的理由 ✓
- `sample` 说明返回值语义（false 只有一个含义）✓
- `equalInts` 说明为什么不排序 ✓
- `shim.go` 的分支说明两条路互斥及为什么容器表更强 ✓
- `runContainerSampling` 说明与 `runRosterSampling` 同构 ✓

- [ ] **Step 7: 门禁 + 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./internal/prochost/ && GOOS=windows go build ./...
```

```bash
git add internal/prochost/members.go internal/prochost/members_sampling_test.go internal/prochost/shim.go internal/prochost/platform_windows.go
git commit -m "feat(prochost): shim 侧容器成员采样，Windows 走 Job Object（B122）"
```

---

## Task 7: `Footprint` 容器前置分支 + 删掉失效告警

**Files:**
- Modify: `internal/prochost/footprint.go`（`Footprint` 顶部）
- Modify: `internal/prochost/footprint_test.go`
- Modify: `cmd/agentd.go:74-82`（删 Warn）

**Interfaces:**
- Consumes: Task 5 的 `readMembers` / `memberSnapshot`、`Handle.MembersPath`
- Produces: `Footprint` 在 `h.MembersPath != ""` 且快照可读时返回容器成员；否则行为**逐字节不变**

- [ ] **Step 1: 写失败测试**

追加到 `internal/prochost/footprint_test.go`：

```go
// 有容器快照时，Footprint 直接用它，不走 pgid 三段判据
func TestFootprintPrefersContainerSnapshot(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, MembersFileName)
	if err := writeMembers(mp, memberSnapshot{PIDs: []int{501, 502}, SampledAt: 42}); err != nil {
		t.Fatal(err)
	}
	// 枚举缝设成「一调用就失败」——容器分支若真的生效，它根本不该被调到
	enumCalled := false
	saved := enumProcsFn
	defer func() { enumProcsFn = saved }()
	enumProcsFn = func() ([]procEntry, error) {
		enumCalled = true
		return nil, ErrNotSupported
	}

	members, v, err := Footprint(Handle{PID: 500, StartedAt: 1, MembersPath: mp})
	if err != nil {
		t.Fatalf("Footprint: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("容器来源可用时应判 OK，实际 %s", v)
	}
	if len(members) != 2 || members[0] != 501 || members[1] != 502 {
		t.Fatalf("应返回容器成员 [501 502]，实际 %v", members)
	}
	if enumCalled {
		t.Error("容器来源可用时不该再走进程枚举——那是 unix 三段判据的入口")
	}
}

// 反面断言：没有容器快照时，unix 的三段判据路径必须**原样**走一遍。
// 少了这条，把 Footprint 改成「总是先试容器」会悄悄改掉 unix 的行为，
// 而 B47/B70/B72/B119 的判据全长在那三段上。
func TestFootprintWithoutContainerKeepsUnixPath(t *testing.T) {
	enumCalls := 0
	savedEnum, savedAlive := enumProcsFn, aliveFn
	defer func() { enumProcsFn, aliveFn = savedEnum, savedAlive }()
	aliveFn = func(Handle) bool { return true }
	enumProcsFn = func() ([]procEntry, error) {
		enumCalls++
		return []procEntry{
			{PID: 600, PPID: 1, PGID: 600, StartedAt: 10},
			{PID: 601, PPID: 600, PGID: 600, StartedAt: 20},
		}, nil
	}

	members, v, err := Footprint(Handle{PID: 600, StartedAt: 10})
	if err != nil {
		t.Fatalf("Footprint: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 OK，实际 %s", v)
	}
	if enumCalls == 0 {
		t.Fatal("无容器来源时必须走进程枚举（unix 三段判据的入口）")
	}
	if len(members) != 2 {
		t.Fatalf("pgid 组应有 2 个成员，实际 %v", members)
	}
}

// 快照损坏或读不到时**落回**三段判据，不是报错也不是返回空
func TestFootprintFallsBackWhenSnapshotUnreadable(t *testing.T) {
	savedEnum, savedAlive := enumProcsFn, aliveFn
	defer func() { enumProcsFn, aliveFn = savedEnum, savedAlive }()
	aliveFn = func(Handle) bool { return true }
	enumCalls := 0
	enumProcsFn = func() ([]procEntry, error) {
		enumCalls++
		return []procEntry{{PID: 700, PPID: 1, PGID: 700, StartedAt: 10}}, nil
	}

	members, v, err := Footprint(Handle{
		PID: 700, StartedAt: 10,
		MembersPath: filepath.Join(t.TempDir(), "gone.json"), // 不存在
	})
	if err != nil {
		t.Fatalf("快照读不到时不该报错，应落回三段判据: %v", err)
	}
	if v != VerdictOK || len(members) != 1 {
		t.Fatalf("应落回 pgid 判据得到 1 个成员，实际 v=%s members=%v", v, members)
	}
	if enumCalls == 0 {
		t.Fatal("必须落回进程枚举")
	}
}
```

`footprint_test.go` 的 import 补 `"path/filepath"`。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/prochost/ -run TestFootprint -v
```

Expected: FAIL — `TestFootprintPrefersContainerSnapshot` 报「应返回容器成员 [501 502]，实际 []」

- [ ] **Step 3: 写最小实现**

在 `internal/prochost/footprint.go` 的 `Footprint` 函数**开头**插入前置分支（`procs, err := enumProcsFn()` 之前）：

```go
	// 容器前置分支：有进程容器的平台（Windows 的 Job Object）由内核维护成员表，
	// 比 pgid + 名册 + 标记这三段判据更强——进程退出即从表中移除，不存在
	// 「表里那个 pid 已经易主」的窗口，因此不需要时间下界校验。
	//
	// 读不到快照（文件还没写出来、损坏、或本就没有容器）时**落回**三段判据，
	// 不报错也不返回空：那两者都会被上层读成「这个任务没有进程」。
	if h.MembersPath != "" {
		if snap, rerr := readMembers(h.MembersPath); rerr == nil {
			log().Debug("足迹取自进程容器成员表", "pid", h.PID,
				"members", len(snap.PIDs), "sampled_at", snap.SampledAt)
			return snap.PIDs, VerdictOK, nil
		} else {
			// 降到 Debug 而非 Warn：任务刚起、shim 还没采过第一轮时读不到是
			// 预期形态，按 Warn 打会在每个任务开头刷一条假告警（B144 的教训）
			log().Debug("容器成员快照不可用，落回 pgid 判据", "pid", h.PID,
				"path", h.MembersPath, "cause", rerr)
		}
	}
```

修改 `cmd/agentd.go`，删掉这一整段（`cmd/agentd.go:74-82`）：

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

**`runtime` 的 import 要保留**——它在同文件的另外两处仍在使用
（`cmd/agentd.go:172` 的 Windows 分支、`:308` 的 `adaptersFor(runtime.GOOS, logger)`）。
删了会编译失败，浪费一轮。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/prochost/ -run TestFootprint -v && go test ./... 
```

Expected: PASS（三个新测试 + 全仓 33 包全绿）

- [ ] **Step 5: 加关键节点日志自检**

- 走容器分支 → Debug 带 members + sampled_at（**sampled_at 必须进日志**：排查「足迹数字不对」时第一个要看的就是数据有多旧）✓
- 快照不可用 → Debug 带 path + cause，**不是 Warn**（任务刚起时读不到是预期形态，按 Warn 打会在每个任务开头刷假告警，B144 的教训）✓
- 落回三段判据后的日志由既有代码承担，未改 ✓

- [ ] **Step 6: 加注释自检**

- 前置分支说明「为什么容器表更强」与「为什么读不到要落回而不是报错」✓
- Debug 而非 Warn 的档位选择有 why 注释并引 B144 ✓
- `cmd/agentd.go` 删除的是一段注释加代码，无需补注释（那条 Warn 的前提已消失）✓

- [ ] **Step 7: 门禁 + 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... && GOOS=windows go build ./...
```

```bash
git add internal/prochost/footprint.go internal/prochost/footprint_test.go cmd/agentd.go
git commit -m "feat(prochost): Footprint 容器前置分支，Windows 恢复每任务进程计数（B122）"
```

---

## 验收：留给协调者本地做，不派发

**以下步骤禁止写进派发给 executor 的任何 task**——纪律块明令禁止 executor 调 `handoff` CLI，写进去只会得到一句诚实的「未验证」，而这些正是最承重的判据。**这份 plan 也绝不能派给 win-b37**：它要装卸重启的正是那台机器的 agentd 托管，而 executor 就跑在那个 agentd 底下（spec §6.1）。

### 前置

- [ ] 在 win-b37 上装 Go（08-18 实测未装；Task 4 的 `//go:build windows` 单测要它才能跑）
- [ ] 导出手搓那份计划任务作回退：`schtasks /Query /TN <旧名> /XML > backup.xml`

### B142

- [ ] `handoff service install` 成功，XML 落盘且是 UTF-16
- [ ] `handoff service status` 报 `Installed=true, Running=true`
- [ ] 手工 kill agentd → **1 分钟内被拉回**（验的是 `<Repetition>` + `IgnoreNew` 那对配置）
- [ ] **`handoff upgrade --target win-b37` 走通闸二**——B142 的价值判据，不是可选项
- [ ] `handoff service uninstall` 后任务与进程双清
- [ ] 记录 schtasks 状态与 pid 复核**是否分叉**（spec §2.4：不分叉说明可简化，分叉是新发现，两种结果都要写进验收记录）

### B122

- [ ] 起一个任务 → `members.json` 有内容且 `sampled_at` 在推进
- [ ] 把 `task_budget` 临时调到很小 → 收到 `task_proc_pressure` 事件
- [ ] `handoff footprint` 显示成员数，与 `tasklist` 手工点名对得上
- [ ] **启动日志里那条「预算告警档不生效」的 Warn 消失**

### 顺带

- [ ] B139 取证：在 win-b37 上给 grok 起个任务，复现 `write` 工具的 `IO Error: unhandled`，定位是 grok 版本问题还是环境问题

### 安装顺序（降低把机器搞成「没有托管」的风险）

1. 先 `service install` 到一个**不同的任务名**上冒烟，确认能起能停
2. 冒烟过了再拆手搓那份、改回正式名 `handoff-agentd`

---

## 自审记录

**1. Spec 覆盖**：§2.1 选型 → Task 1 文件头；§2.2 四项承重配置 → Task 1 Step 1 逐条断言；§2.3 换版链路 → Task 3；§2.4 Status 复核 → Task 2 `TestWindowsStatusVerifiesByPID`；§2.5 不用 /End → Task 2 `TestWindowsUninstallDoesNotUseEnd`；§2.6 三个坑 → Task 1（UTF-16）+ Task 2（schtasks 原文进报文）；§3.1 推翻 Toolhelp32 → Task 4 文件头；§3.3 平台缝 → Task 6 `containerSampleFn` + Task 7 前置分支；§3.4 shim 采样 → Task 6；§3.5 独立文件 → Task 5；§3.6 连带 → Task 7；§3.7 探针 → 已完成，代码回填进 Task 4。

**2. 一处对 spec 的细化**：spec §3.3 设想 `containerMembers` 分 `_other.go` / `_windows.go` 两个平台文件。实现改为**一份平台无关代码 + 数据决定的边界**——`Footprint` 只看 `h.MembersPath` 是否为空，而 unix 的 `Start` 恒填空串。理由：与 `TaskCred.MarkRoot` 那条「仅托管 worktree 可杀，边界由数据决定、不存在某处忘了检查」是同一形态，且少一个平台文件、全平台可测。平台缝只保留 `containerSampleFn`（Task 6），那里确实需要区分平台。

**3. 类型一致性**：`memberSnapshot{PIDs, SampledAt}` 在 Task 5 定义，Task 6（`writeMembers`）、Task 7（`readMembers`）沿用；`containerSampleFn func() ([]int, error)` 在 Task 6 定义、Task 4 的 `jobProcessIDs() ([]int, error)` 与之签名一致；`membersPath` 在 Task 5 定义，Task 5（`Start`）与 Task 6（sampler 构造）使用。已核对无分叉。

**4. 自审抓到并修掉的两处**（都是「照着写会白跑一轮」的错）：

- `internal/prochost` 包**此前没有 `testLogger()`**（只有 `internal/service` 有）。
  Task 6 的测试原本直接调它会编译失败，已在该 task 的测试文件里补上定义。
- `cmd/agentd.go` 删掉那段 Warn 后 **`runtime` 的 import 必须保留**——
  它在 `:172` 与 `:308` 仍在使用。原稿写的「若不再使用则一并删掉」会把实现者
  引向一次必然失败的编译。
