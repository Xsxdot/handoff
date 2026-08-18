# B134 + B137 权限门两洞 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让「命令把输出写到任务范围外」这件事重新被人看见并由人裁决，同时让协调者拒绝时给的理由与拒绝**同帧**送达模型。

**Architecture:** 两条线各自独立。B134 线：permgate 新增重定向落点提取（`redirect.go`），bash 路由改走 `judgeBash`——先判全部落点是否落在任务范围内，越界即 `Escalate`，落点干净才落回既有的 `judgeCommand`；同时给 opencode 的静态规则表补四条 ask 模式，否则这类命令根本进不到 permgate。B137 线：`Adapter.RespondPermission` 加 `reason` 形参，claude 把它喂进已有但被写死成常量的 `permDecision.Message`，并经一个可选接口 `DenyReasonInBand` 告诉 manager「这条理由已经送到了，别再走带外注入」。

**Tech Stack:** Go 1.26；标准库（`os` / `path/filepath` / `strings` / `regexp`）；无新增依赖。

**设计出处：** [spec](../specs/2026-08-18-b134-b137-permission-gate-holes-design.md)。spec 的 §7 两条真机探针**已由审核者跑完**，结论已回填，实现期没有条件分支。

## Global Constraints

- **语言与注释**：所有新增注释用中文，讲「为什么」不讲「做了什么」。每个新建文件顶部写「职责 + 边界」；每个导出函数写参数、返回、注意事项。
- **日志**：用 `log/slog`（permgate 用 `g.log`，agentd 用 `m.log`，adapter 用 `a.log`）。**禁止 `fmt.Printf` / `print` 作为日志手段。** 错误分支必须带上下文与 cause。
- **不得引入新依赖**：`go.mod` / `go.sum` 相对基线必须零改动。
- **不得改动的东西**：`permgate.judgeCommand` 的「永不返回 AutoAllow」不变式；`external_directory: "ask"`（`opencode/taskenv_test.go:185` 焊死）；`deny_guidance_dropped` 的丢弃语义与它的 Publish 唤醒（B91）。
- **越界文案必须逐字复用既有那条**：`目标路径越出任务范围: %s`。它已经是 `judgeFileWrite` 的文案，也是 B27 真机验收记录里被 grep 的那条，两处不一致会让日志检索失效。
- **每个 task 完成即 commit**，提交信息用各 task「Commit」步骤里给定的原文。

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/permgate/redirect.go` | 新建 | 从命令原文摘重定向落点；`~` 展开；`/dev/*` 丢弃落点识别 |
| `internal/permgate/redirect_test.go` | 新建 | 上述三者的逐形态断言 |
| `internal/permgate/permgate.go` | 改 | bash 路由改走新增的 `judgeBash` |
| `internal/permgate/permgate_test.go` | 改 | bash + 落点的四类断言（越界 / 范围内 / 空 / 丢弃落点） |
| `internal/executor/opencode/taskenv.go` | 改 | `bashPermissionRules` 补四条重定向 ask 模式 |
| `internal/executor/opencode/taskenv_test.go` | 改 | 规则表逐条断言同步 |
| `internal/executor/turn/deny.go` | 新建 | 拒绝理由正文的唯一渲染点（两个出口共用） |
| `internal/executor/turn/deny_test.go` | 新建 | 渲染结果断言 |
| `internal/executor/executor.go` | 改 | `RespondPermission` 契约加 `reason` 形参 |
| `internal/executor/claudecode/adapter.go` | 改 | 喂真实理由进 `permDecision.Message`；实现 `DenyReasonInBand` |
| `internal/executor/grok/perm.go` | 改 | 仅签名 |
| `internal/executor/codex/perm.go` | 改 | 仅签名 |
| `internal/executor/opencode/adapter.go` | 改 | 签名 + 一条「老端点丢弃额外字段」的注释 |
| `internal/executor/fake/fake.go` | 改 | 签名；`PermCall` 记录 `Reason`；可注入的 `DenyReasonInBand` |
| `internal/agentd/manager.go` | 改 | 四处调用点传 `reason`；`denyReasonInBander` 可选接口；按它跳过带外挂起 |
| `README.md` / `README.zh-CN.md` | 改 | 「Permission tiers / 权限分级」段补一句越界落点 |

---

### Task 1: permgate 重定向落点提取

**Files:**
- Create: `internal/permgate/redirect.go`
- Test: `internal/permgate/redirect_test.go`

**Interfaces:**
- Consumes: 无（纯函数，不依赖前序 task）
- Produces: `permgate.RedirectTargets(cmd string) []string`、`permgate.IsDiscardTarget(p string) bool`。Task 2 会用这两个。

- [ ] **Step 1: 写失败的测试**

新建 `internal/permgate/redirect_test.go`：

```go
// redirect_test.go —— 重定向落点提取的逐形态断言。
//
// 每加一条形态就在这里加一行：漏掉一种写法就是一条静默放行的通道。
package permgate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRedirectTargets(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("取 home 失败: %v", err)
	}
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"空串", "", nil},
		{"无重定向", "go build ./...", nil},
		{"覆盖写带空格", "echo hi > /tmp/x", []string{"/tmp/x"}},
		{"覆盖写无空格", "echo hi >/tmp/x", []string{"/tmp/x"}},
		{"追加写", "echo hi >> /etc/hosts", []string{"/etc/hosts"}},
		{"追加写无空格", "echo hi>>/etc/hosts", []string{"/etc/hosts"}},
		{"强制覆盖 >|", "echo hi >| /tmp/x", []string{"/tmp/x"}},
		{"带 fd 号", "cmd 2> err.log", []string{"err.log"}},
		{"合并重定向 &>", "cmd &> /tmp/all", []string{"/tmp/all"}},
		{"合并追加 &>>", "cmd &>> /tmp/all", []string{"/tmp/all"}},
		{"相对落点", "echo hi > out.txt", []string{"out.txt"}},
		{"多个落点", "a > b | c > d", []string{"b", "d"}},
		{"落点带引号", `echo x > "/etc/foo bar"`, []string{"/etc/foo bar"}},
		{"落点带单引号", "echo x > '/tmp/y'", []string{"/tmp/y"}},
		{"家目录展开", "echo x >> ~/.zshrc", []string{filepath.Join(home, ".zshrc")}},
		{"裸家目录", "echo x > ~", []string{home}},

		// 以下都不是文件写入，必须一条都不产出
		{"fd 复制 2>&1", "go test ./... 2>&1", nil},
		{"fd 复制 >&2", "echo err >&2", nil},
		{"fd 关闭 >&-", "cmd >&-", nil},
		{"引号内的尖括号", `echo "a > b"`, nil},
		{"单引号内的尖括号", "echo 'a > b'", nil},
		{"字符串里的箭头", `grep "x->y" file.txt`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedirectTargets(c.cmd)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("RedirectTargets(%q) = %v，期望 %v", c.cmd, got, c.want)
			}
		})
	}
}

func TestIsDiscardTarget(t *testing.T) {
	yes := []string{"/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/fd/3"}
	for _, p := range yes {
		if !IsDiscardTarget(p) {
			t.Fatalf("%s 必须判为丢弃落点——否则 `go test > /dev/null` 每次都升级人工", p)
		}
	}
	no := []string{"/dev/sda", "/tmp/null", "/etc/passwd", "out.txt", ""}
	for _, p := range no {
		if IsDiscardTarget(p) {
			t.Fatalf("%s 不得判为丢弃落点", p)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/permgate/ -run 'TestRedirectTargets|TestIsDiscardTarget' -v`
Expected: 编译失败，`undefined: RedirectTargets` / `undefined: IsDiscardTarget`

- [ ] **Step 3: 写实现**

新建 `internal/permgate/redirect.go`：

```go
// redirect.go —— shell 输出重定向落点的提取。
//
// 职责：
//   - 从命令原文里摘出「输出会落到哪个文件」，供 judgeBash 做范围判定
//   - 展开 ~ 前缀；识别 /dev/* 这类不构成文件写入的丢弃落点
//
// 边界：
//   - 不做完整 shell 解析：不展开变量、不处理 heredoc、不解析子 shell、不跟 cd
//   - 不判断路径是否越界：范围判定在 path.go，本文件只负责「落点是什么」
//
// 为什么不能先过 StripQuoted：落点常常被引号包住（echo x > "/etc/foo"），
// 剥完引号落点就没了。这里要的恰恰是引号里的内容，所以扫描的是命令原文，
// 自己带引号状态机。
//
// 已知不覆盖（spec §4.3 明列的残余）：相对路径逃逸（> ../../x）能摘出来并交
// InScope 判，但 `cd /etc && echo x > passwd` 这种先换目录再相对写的形态摘到的
// 是 "passwd"，InScope 会按 workdir 拼接判成范围内——本轮不跟 cd。
package permgate

import (
	"os"
	"path/filepath"
	"strings"
)

// discardTargets 是不构成文件写入的重定向落点：写它们不改变文件系统。
//
// 不豁免的代价是实打实的：`go test ./... > /dev/null` 是高频写法，
// 每次都判越界就等于每次都升级人工。
var discardTargets = map[string]bool{
	"/dev/null":   true,
	"/dev/stdout": true,
	"/dev/stderr": true,
	"/dev/tty":    true,
}

// IsDiscardTarget 判断落点是否为 /dev 下的丢弃或终端设备。
//
// 参数：p 为落点路径（已展开 ~）
// 返回：是则 true，调用方应跳过对它的范围判定
func IsDiscardTarget(p string) bool {
	if discardTargets[p] {
		return true
	}
	return strings.HasPrefix(p, "/dev/fd/")
}

// RedirectTargets 从命令串里摘出全部输出重定向的落点。
//
// 参数：cmd 为命令原文（**不要**先过 StripQuoted，见文件头 why）
// 返回：落点路径切片，按出现顺序；没有则返回 nil
//
// 识别的形态：`>`、`>>`、`>|`、`n>`、`n>>`、`&>`、`&>>`
// 明确排除的形态：fd 复制与关闭（`2>&1`、`>&2`、`>&-`）——它们不写文件
//
// 注意：
//   - 引号内的 `>` 不算重定向（`echo "a > b"`、`grep "x->y"` 都不命中）
//   - 落点带引号时取引号内的内容，可以含空格
//   - `~` 与 `~/` 前缀展开为当前用户 home（why 见 expandTilde）
func RedirectTargets(cmd string) []string {
	var out []string
	rs := []rune(cmd)
	var quote rune
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			continue
		case quote != 0 && r == quote:
			quote = 0
			continue
		case quote != 0:
			continue
		}
		if r != '>' {
			continue
		}
		j := i + 1
		// `>>` 与 `>|` 的第二个字符只是操作符的一部分，不是落点
		for j < len(rs) && (rs[j] == '>' || rs[j] == '|') {
			j++
		}
		// `>&` 后面跟数字或 `-` 是 fd 复制/关闭，不写文件；跟其他内容才是落点
		if j < len(rs) && rs[j] == '&' {
			k := j + 1
			if k < len(rs) && (rs[k] == '-' || (rs[k] >= '0' && rs[k] <= '9')) {
				i = k
				continue
			}
			j = k
		}
		for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t') {
			j++
		}
		tgt, next := readTarget(rs, j)
		// 扫描位置直接跳到落点之后：落点内部的字符不该再被当成操作符看
		i = next - 1
		if tgt != "" {
			out = append(out, expandTilde(tgt))
		}
	}
	return out
}

// readTarget 从 rs[i] 起读一个落点词元。
//
// 参数：rs 为命令的 rune 切片，i 为起始下标（调用方已跳过空白）
// 返回：词元内容（去掉包裹引号）与下一个待扫描下标
func readTarget(rs []rune, i int) (string, int) {
	if i >= len(rs) {
		return "", i
	}
	if q := rs[i]; q == '\'' || q == '"' {
		var b strings.Builder
		j := i + 1
		for ; j < len(rs) && rs[j] != q; j++ {
			b.WriteRune(rs[j])
		}
		if j < len(rs) {
			j++ // 吃掉右引号
		}
		return b.String(), j
	}
	var b strings.Builder
	j := i
	for ; j < len(rs); j++ {
		switch rs[j] {
		case ' ', '\t', '\n', '|', ';', '&', '(', ')', '<', '>':
			return b.String(), j
		}
		b.WriteRune(rs[j])
	}
	return b.String(), j
}

// expandTilde 把 `~` 与 `~/xxx` 展开为当前用户 home。
//
// 参数：p 为原始落点
// 返回：展开后的路径；不以 ~ 开头、或取不到 home 时原样返回
//
// 为什么必须展开：filepath.Abs 不认识 `~`，`> ~/.zshrc` 会被 InScope 拼成
// <workdir>/~/.zshrc，判成范围内直接放行——那正是本条要拦的写入。
// 取不到 home 时原样返回是安全的：`~/.zshrc` 作为相对路径拼到 workdir 下，
// InScope 判在范围内，与展开前的行为一致，不会比现状更松。
func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `go test ./internal/permgate/ -run 'TestRedirectTargets|TestIsDiscardTarget' -v`
Expected: 全部 PASS

- [ ] **Step 5: 注释自检**

确认下面四条都在，缺一条就补上：
- 文件头有「职责 + 边界」，并写明「为什么不能先过 StripQuoted」
- `RedirectTargets` / `IsDiscardTarget` 两个导出函数有参数、返回、注意事项
- `expandTilde` 写清「不展开会被判成范围内」这个后果，以及取不到 home 时为何安全
- 文件头列出了本轮不覆盖的形态（不跟 cd）

本 task 不加日志：本文件是纯函数，permgate 包的日志出口在 `Gate.log` 上，
落点判定的日志由 Task 2 的 `judgeBash` 统一打——同一件事在两处打会重复。

- [ ] **Step 6: Commit**

```bash
git add internal/permgate/redirect.go internal/permgate/redirect_test.go
git commit -m "feat(permgate): 从命令原文摘重定向落点，识别 fd 复制与 /dev 丢弃落点"
```

---

### Task 2: permgate bash 路由并判落点范围

**Files:**
- Modify: `internal/permgate/permgate.go`（`Judge` 的 bash 分支，新增 `judgeBash`）
- Test: `internal/permgate/permgate_test.go`

**Interfaces:**
- Consumes: Task 1 的 `RedirectTargets`、`IsDiscardTarget`；既有的 `InScope(path string, scope Scope) (bool, string, error)` 与 `(g *Gate) judgeCommand(s string) Verdict`
- Produces: `Judge` 在 bash 路由上的新行为。Task 3 的 opencode 规则表改动依赖它才有意义。

- [ ] **Step 1: 写失败的测试**

在 `internal/permgate/permgate_test.go` 末尾追加：

```go
// TestJudgeBashPathsEscalate 钉住 B134 主修：bash 请求的落点越界必须升级人工，
// 而不是落到 Consult 交廉价模型。落点有两个来源，都要覆盖。
func TestJudgeBashPathsEscalate(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []struct {
		name string
		req  Request
	}{
		{"executor 检出的越界目录", Request{
			Tool: "bash", Text: "external_directory: ls /etc", Command: "ls /etc",
			Paths: []string{"/etc"}}},
		{"handoff 自己摘的重定向落点", Request{
			Tool: "bash", Text: "Bash: echo x > /etc/hosts", Command: "echo x > /etc/hosts"}},
		{"追加写到家目录", Request{
			Tool: "bash", Text: "Bash: echo x >> ~/.zshrc", Command: "echo x >> ~/.zshrc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := g.Judge(c.req, scope)
			if v.Action != Escalate {
				t.Fatalf("Action = %v，期望 Escalate（reason=%q）", v.Action, v.Reason)
			}
			if !strings.Contains(v.Reason, "目标路径越出任务范围") {
				t.Fatalf("Reason = %q，必须逐字复用 judgeFileWrite 的越界文案", v.Reason)
			}
		})
	}
}

// TestJudgeBashInScopeFallsBack 落点全部在范围内时必须落回命令判据，
// 而不是因为「有落点」就升级——否则每次往工作区里写日志都叫人。
func TestJudgeBashInScopeFallsBack(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []Request{
		{Tool: "bash", Text: "Bash: echo x > out.txt", Command: "echo x > out.txt"},
		{Tool: "bash", Text: "Bash: go test ./... > " + wd + "/log", Command: "go test ./... > " + wd + "/log"},
		{Tool: "bash", Text: "Bash: go test ./... > /dev/null", Command: "go test ./... > /dev/null"},
		{Tool: "bash", Text: "Bash: go test ./... 2>&1", Command: "go test ./... 2>&1"},
	}
	for _, req := range cases {
		t.Run(req.Command, func(t *testing.T) {
			if v := g.Judge(req, scope); v.Action != Consult {
				t.Fatalf("Action = %v（reason=%q），期望 Consult——落点合法就该落回命令判据",
					v.Action, v.Reason)
			}
		})
	}
}

// TestJudgeBashNoPathsUnchanged 无落点的 bash 请求必须与改动前逐字同判：
// 本 task 不许顺带改变绝大多数命令的走向。
func TestJudgeBashNoPathsUnchanged(t *testing.T) {
	g := newTestGate(t)
	scope := Scope{Workdir: t.TempDir()}
	if v := g.Judge(Request{Tool: "bash", Text: "Bash: go build ./...", Command: "go build ./..."}, scope); v.Action != Consult {
		t.Fatalf("无害命令 Action = %v，期望 Consult", v.Action)
	}
	if v := g.Judge(Request{Tool: "bash", Text: "Bash: rm -rf /", Command: "rm -rf /"}, scope); v.Action != Escalate {
		t.Fatalf("黑名单命令 Action = %v，期望 Escalate", v.Action)
	}
}
```

三条用例都用本包测试既有的 `newTestGate(t)` 构造 Gate（`permgate_test.go` 的其余用例
就是这么写的），不要自造 `New(nil, nil)`。若该文件尚未 import `strings`，补上。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/permgate/ -run TestJudgeBash -v`
Expected: `TestJudgeBashPathsEscalate` 的三个子用例全部 FAIL，实得 `Consult`

- [ ] **Step 3: 写实现**

`internal/permgate/permgate.go`，把 `Judge` 的 bash 分支从

```go
	case executor.PermToolBash:
		return g.judgeCommand(req.Command)
```

改为

```go
	case executor.PermToolBash:
		return g.judgeBash(req, scope)
```

并同步 `Judge` 的 doc 注释：把「bash → 对 Command 做命令类判定」改为
「bash → 先判全部落点的范围归属，落点干净才做命令类判定」。

在 `judgeFileWrite` 之后新增：

```go
// judgeBash 判定命令类请求：先判落点范围，落点干净才落回命令判据。
//
// 参数：req 为结构化请求（用 Command 与 Paths），scope 为任务范围
// 返回：Escalate（任一落点越界或归一化失败），否则 judgeCommand 的结果
//
// 落点有两个来源，同等对待：
//   - req.Paths —— executor 自己检出的越界目录。opencode 的 external_directory
//     bash 形态把它填在这里（opencode/adapter_test.go 有同名断言钉住），
//     但**此前被本函数的前身整个丢弃**，越界因此从「人来判」降级成「廉价模型判」
//   - RedirectTargets(req.Command) —— handoff 自己从命令原文摘的重定向落点。
//     不能只靠前者：2026-08-18 真机探针实测 opencode 只解析「作为参数出现的
//     路径」，`echo x > /tmp/f` 零权限请求直接写成功（spec §2.2.1）
//
// 为什么路径判据前置于命令判据：越界是确定性事实，而 judgeCommand 最好的结果
// 也只是 Consult。放在后面会让「越界」被稀释成廉价模型的一次裁决。
//
// 为什么落点为空不 fail-closed（与 judgeFileWrite 的 len(Paths)==0 → Escalate 不同）：
// 纯 bash 门类本来就不带路径，`go build ./...` 是绝大多数情形；在这里 fail-closed
// 等于把每条命令都升级人工，那是 spec §3 明确排除的反转。
func (g *Gate) judgeBash(req Request, scope Scope) Verdict {
	targets := append(append([]string(nil), req.Paths...), RedirectTargets(req.Command)...)
	for _, p := range targets {
		if IsDiscardTarget(p) {
			g.log.Debug("命令落点是丢弃设备，跳过范围判定", "path", p)
			continue
		}
		in, base, err := InScope(p, scope)
		if err != nil {
			g.log.Debug("命令落点归一化失败，按越界处置", "path", p, "cause", err)
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("命令落点归一化失败 %q: %v", p, err)}
		}
		if !in {
			// 文案逐字复用 judgeFileWrite：B27 的真机验收记录按这句话 grep 日志
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("目标路径越出任务范围: %s", p)}
		}
		g.log.Debug("命令落点在任务范围内", "path", p, "base", base)
	}
	return g.judgeCommand(req.Command)
}
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `go test ./internal/permgate/ -v`
Expected: 全包 PASS，含既有全部用例

- [ ] **Step 5: 日志与注释自检**

- 三条 Debug 日志都在：丢弃落点跳过、归一化失败、落点在范围内（各带 `path`）
- 归一化失败分支带 `cause`
- 越界分支不打日志：权威的 WARN 由 `manager.judgePermission` 打（那里才有 task id 与 perm id），两处都打会让同一件事出现两遍——这条要写进注释
- `judgeBash` 的 doc 说清两个落点来源、前置顺序的理由、以及为什么不 fail-closed
- `Judge` 的 doc 里 bash 那一行已同步

- [ ] **Step 6: Commit**

```bash
git add internal/permgate/permgate.go internal/permgate/permgate_test.go
git commit -m "fix(permgate): bash 路由接回越界判据——executor 检出的路径与自摘的重定向落点都判"
```

---

### Task 3: opencode 静态规则表补重定向 ask 模式

**Files:**
- Modify: `internal/executor/opencode/taskenv.go`（`bashPermissionRules`）
- Test: `internal/executor/opencode/taskenv_test.go`

**Interfaces:**
- Consumes: Task 2 的 `judgeBash`（这四条规则只是把命令送进去，判定在那边）
- Produces: 无新标识符

- [ ] **Step 1: 写失败的测试**

`internal/executor/opencode/taskenv_test.go` 里断言 `bashPermissionRules` 的用例，
把四条新模式加进期望值。若该文件是逐条比对整张表，同步表；若是逐条 `if _, ok :=` 检查，
追加四条。另加一条守卫用例：

```go
// TestBashRulesRejectBareRedirectGlob 守住一条取舍：不许把重定向模式放宽成 "*>*"。
//
// "*>*" 会命中 2>&1，而 `go test ./... 2>&1 | tail` 是高频写法，每条都送 Consult；
// 在没配审批者的部署上 Consult 会退化成升级人工，等于把审批回路淹掉。
func TestBashRulesRejectBareRedirectGlob(t *testing.T) {
	if _, ok := bashPermissionRules["*>*"]; ok {
		t.Fatal(`bashPermissionRules 不得含 "*>*"——它会命中 2>&1，见本用例注释`)
	}
	for _, p := range []string{"*>/*", "*> /*", "*>~*", "*> ~*"} {
		if got := bashPermissionRules[p]; got != "ask" {
			t.Fatalf("模式 %q = %q，期望 ask——少一条就是一条静默放行的重定向通道", p, got)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/executor/opencode/ -run TestBashRules -v`
Expected: FAIL，四条模式取值为空串

- [ ] **Step 3: 写实现**

`internal/executor/opencode/taskenv.go` 的 `bashPermissionRules`，在 `"wget *"` 之后、
`"*": "allow"` 之前插入：

```go
	// 重定向到绝对路径或家目录：opencode 自己不检出重定向落点（2026-08-18 真机
	// 探针，spec §2.2.1——同一个路径，作为参数出现要授权、作为重定向落点不要），
	// 不在这里把它们捞进来，permgate 的落点判据根本没机会跑。
	// 四条而不是一条 "*>*"：后者会命中 2>&1，`go test ./... 2>&1 | tail` 是高频
	// 写法，每条都送 Consult，在没配审批者的部署上等于升级人工。
	"*>/*":  "ask", // >/abs、>>/abs 都含子串 ">/"
	"*> /*": "ask", // > /abs、>> /abs 都含子串 "> /"
	"*>~*":  "ask", // >~/x
	"*> ~*": "ask", // > ~/x
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `go test ./internal/executor/opencode/ -v`
Expected: 全包 PASS

- [ ] **Step 5: 注释自检**

- 四条模式上方有「为什么不用 `*>*`」与「opencode 自己不检出重定向落点」两条 why
- `bashPermissionRules` 头部注释里那句「每次修改本表必须同步 taskenv_test 的逐条断言」已被本次改动遵守

本 task 不加日志：`WriteTaskEnv` 末尾已有 `opencode 任务环境已生成` 的 Info，
规则条数变化会自然体现在既有日志里，无需新增。

- [ ] **Step 6: Commit**

```bash
git add internal/executor/opencode/taskenv.go internal/executor/opencode/taskenv_test.go
git commit -m "fix(opencode): 重定向到绝对路径/家目录进 ask，让 permgate 的落点判据够得着"
```

---

### Task 4: Adapter 契约加 reason 形参

**Files:**
- Modify: `internal/executor/executor.go`（接口与 doc）
- Modify: `internal/executor/claudecode/adapter.go`、`internal/executor/grok/perm.go`、`internal/executor/codex/perm.go`、`internal/executor/opencode/adapter.go`、`internal/executor/fake/fake.go`
- Modify: `internal/agentd/manager.go`（四处调用点）
- Test: 既有测试全绿即可，另加 fake 的实参记录断言

**Interfaces:**
- Consumes: 无
- Produces: `RespondPermission(ctx context.Context, taskID, permID, decision, reason string) error`（五个实现同签名）；`fake.PermCall` 新增字段 `Reason string`。Task 5、6 都依赖本 task。

**本 task 只搬管道，不改行为**：五个实现都先把 `reason` 收下不用（claude 在 Task 5 才用）。这样做的理由是签名变更牵动 5 个实现与十几处测试，与行为改动混在一起会让审查看不清哪个 diff 是哪件事。

- [ ] **Step 1: 改契约**

`internal/executor/executor.go`：

接口方法改为

```go
	// RespondPermission 应答 executor 的权限请求。
	//
	// 参数：
	//   - taskID: 目标任务
	//   - permID: 权限请求 id（与事件中的 PermissionID 一致；manager 的 ticket id
	//     经 taskID:permID 命名空间化，此处传裸 permID，不得传命名空间化 id）
	//   - decision: "once"（批准本次）或 "reject"（拒绝）
	//   - reason: decision 为 "reject" 时协调者给出的原因；批准时忽略，可为空。
	//     原生协议带得了消息的 adapter 应把它与裁决同帧送达模型，并实现
	//     manager 侧的 DenyReasonInBand 可选接口；带不了的忽略即可，
	//     manager 会退回带外注入（B50）
	RespondPermission(ctx context.Context, taskID, permID, decision, reason string) error
```

同步文件头那行五动作摘要：`RespondPermission: 应答权限门，decision 取 "once" 或 "reject"，reject 时可带协调者理由`。

- [ ] **Step 2: 改五个实现的签名**

四个只改签名、收下不用：

- `internal/executor/grok/perm.go`：`func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, _ string) (err error)`，并在 doc 参数表补一行 `- reason: ACP 的 outcome 只有 optionId，带不了消息，本 adapter 忽略（spec §2.5）`
- `internal/executor/codex/perm.go`：同上，doc 写 `- reason: codex 的应答体只有 decision，带不了消息，本 adapter 忽略（spec §2.5）`
- `internal/executor/opencode/adapter.go`：同上，doc 写：

```go
//   - reason: 本 adapter 忽略。handoff 走的老端点
//     POST /session/{id}/permissions/{permID} 服务端只读 payload.response，
//     多带的字段被静默丢弃；带 message 的是新端点 /permission/{requestID}/reply
//     （2026-08-18 读 opencode 1.18.18 二进制实证，spec §2.5）。迁端点另计
```

  注意 `internal/executor/opencode/api.go` 的 `(a *API) RespondPermission` **不改**——
  它是 HTTP 层，老端点本来就没有这个字段。

- `internal/executor/claudecode/adapter.go`：签名加 `reason string`，本 task 里**仍用既有常量**，
  Task 5 才接上。

- `internal/executor/fake/fake.go`：签名加 `reason string`；`PermCall` 加字段（**fake 刻意
  不实现 `DenyReasonInBand`**——它得留在「未实现该可选接口」那一侧，让所有既有的 fake
  基础用例继续走带外注入路径，把类型断言失败的分支也覆盖到）：

```go
// PermCall 记录一次 RespondPermission 调用的实参。
type PermCall struct {
	TaskID   string
	PermID   string
	Decision string
	Reason   string // 协调者的拒绝理由；批准时为空
}
```

  并在构造 `PermCall` 处填上 `Reason: reason`，在既有的 `fake 收到 RespondPermission`
  Debug 日志里补 `"reason", reason`。

- [ ] **Step 3: 改四处调用点**

`internal/agentd/manager.go`：

- `RelayAnswer` 的 gate 分支（`ad.RespondPermission(actx, taskID, permID, decision)`）→ 加 `, reason`
- `waitPermission`（`ad.RespondPermission(actx, taskID, permID, decision)`）→ 加 `, reason`
- `autoAllowPermission`（`..., ev.PermissionID, "once"`）→ 加 `, ""`
- 审批者批准路径（`..., permID, "once"`）→ 加 `, ""`

两处 `"once"` 传空串即可：批准没有理由可言，契约已写明批准时忽略。

- [ ] **Step 4: 改测试里的实现与调用**

`internal/agentd/manager_test.go`、`status_test.go`、`integration_test.go`、
`internal/executor/opencode/adapter_test.go`、`api_test.go`、`grok/perm_test.go`
里所有自建的 stub adapter 与直接调用，同步签名。

Run: `go build ./... && go vet ./...`
Expected: 退出 0，无 `not enough arguments` / `does not implement` 报错

- [ ] **Step 5: 加一条 fake 实参断言**

`internal/executor/fake/` 的测试里追加（没有测试文件就新建 `fake_test.go`）：

```go
// TestFakeRecordsDenyReason 钉住 reason 一路传到 adapter：契约加了形参却在中途
// 丢掉，是这类改动最典型的失败形态，且不会有任何编译错误提示。
func TestFakeRecordsDenyReason(t *testing.T) {
	f := New(nil)
	if err := f.RespondPermission(context.Background(), "T1", "perm-1", "reject", "别删，先 git mv 归档"); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	perms := f.Perms()
	if len(perms) != 1 {
		t.Fatalf("实参记录 %d 条，期望 1", len(perms))
	}
	if perms[0].Reason != "别删，先 git mv 归档" {
		t.Fatalf("Reason = %q，期望原样记录", perms[0].Reason)
	}
}
```

`New` 的实际构造签名以 `fake.go` 现状为准，不要臆造。

- [ ] **Step 6: 跑全量测试**

Run: `go test ./...`
Expected: 无失败包

- [ ] **Step 7: 注释自检**

- `executor.go` 接口方法的 `reason` 参数说明写清「带得了的同帧送、带不了的忽略、manager 会退回带外注入」
- grok / codex / opencode 三处 doc 各自写明**为什么**忽略（协议无字段 / 老端点丢弃），不是笼统一句「不支持」
- opencode 那处点名新端点，让下一个读代码的人不必重跑探针

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(executor): RespondPermission 契约加 reason 形参，五个实现与四处调用点同步"
```

---

### Task 5: claude 让拒绝理由与裁决同帧送达

**Files:**
- Create: `internal/executor/turn/deny.go`
- Test: `internal/executor/turn/deny_test.go`
- Modify: `internal/executor/claudecode/adapter.go`
- Modify: `internal/agentd/manager.go`（`relayDenyGuidance` 改用共享渲染）
- Test: `internal/executor/claudecode/adapter_test.go`（或该包既有的 perm 测试文件）

**Interfaces:**
- Consumes: Task 4 的 `reason` 形参
- Produces: `turn.DenyGuidanceText(reason string) string`；claude 的 `(a *Adapter) DenyReasonInBand() bool`。Task 6 靠后者做类型断言。

- [ ] **Step 1: 写失败的测试**

新建 `internal/executor/turn/deny_test.go`：

```go
package turn

import (
	"strings"
	"testing"
)

// TestDenyGuidanceText 钉住两件事：理由原文必须出现，且必须带上「不要重复发起
// 同一请求」——少了后半句，模型被拒后最常见的下一步就是原地再试一次。
func TestDenyGuidanceText(t *testing.T) {
	got := DenyGuidanceText("改用 go build ./...")
	if !strings.Contains(got, "改用 go build ./...") {
		t.Fatalf("正文 = %q，必须含理由原文", got)
	}
	if !strings.Contains(got, "不要重复发起同一请求") {
		t.Fatalf("正文 = %q，必须含「不要重复发起同一请求」", got)
	}
}
```

在 `internal/executor/claudecode/` 的权限测试文件里追加：

```go
// respondAndRead 起裁决 socket、装好 runState、发一条 ask，调 RespondPermission
// 后把回发的裁决读出来。脚手架沿用 perm_test.go 既有的 newPermServer + dialAsk，
// 不另起一套 mock。
func respondAndRead(t *testing.T, decision, reason string) (behavior, message string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "perm.sock")
	srv, err := newPermServer(sock, slog.Default(), func(permAsk) {})
	if err != nil {
		t.Fatalf("newPermServer: %v", err)
	}
	defer srv.Close()

	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.runs["T1"] = &runState{
		taskID: "T1", perm: srv,
		evCh: make(chan executor.AdapterEvent, 4), stopCh: make(chan struct{}),
	}
	conn := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"rm -rf x"}`)
	defer conn.Close()

	if err := a.RespondPermission(context.Background(), "T1", "toolu_1", decision, reason); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	var got struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&got); err != nil {
		t.Fatalf("读裁决: %v", err)
	}
	return got.Behavior, got.Message
}

// TestRespondPermissionCarriesReason 钉住 B137 主修：协调者的理由必须原样进
// permDecision.Message，而不是被换成一句通用话。通道早就是通的
// （perm.go 的 Message 字段 → cmd/permission_mcp.go 回给模型），
// 此前断在 adapter 把它写死成常量。
func TestRespondPermissionCarriesReason(t *testing.T) {
	const reason = "别删，先 git mv 归档"
	behavior, message := respondAndRead(t, "reject", reason)
	if behavior != "deny" {
		t.Fatalf("behavior = %q，期望 deny", behavior)
	}
	if want := turn.DenyGuidanceText(reason); message != want {
		t.Fatalf("message = %q，期望 %q", message, want)
	}
}

// TestRespondPermissionEmptyReasonFallsBack 协调者没给理由时不能送一句空的：
// 空 message 会让模型以为「理由缺失」本身是异常，通用句才是对的兜底。
func TestRespondPermissionEmptyReasonFallsBack(t *testing.T) {
	behavior, message := respondAndRead(t, "reject", "   ")
	if behavior != "deny" {
		t.Fatalf("behavior = %q，期望 deny", behavior)
	}
	if message != "协调者拒绝了本次操作" {
		t.Fatalf("message = %q，期望回退到通用句", message)
	}
}

// TestDenyReasonInBand claude 必须自报「理由已同帧送达」，否则 manager 会再走
// 一遍带外注入，模型被同一条理由说两遍。
func TestDenyReasonInBand(t *testing.T) {
	if !New(nil).DenyReasonInBand() {
		t.Fatal("claude adapter 必须返回 true")
	}
}
```

按需补 import：`bufio`、`context`、`encoding/json`、`io`、`log/slog`、`path/filepath`，
以及 `internal/executor` 与 `internal/executor/turn`。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/executor/turn/ ./internal/executor/claudecode/ -run 'DenyGuidance|RespondPermissionCarries|RespondPermissionEmpty|DenyReasonInBand' -v`
Expected: 编译失败（`undefined: DenyGuidanceText` / `DenyReasonInBand`）

- [ ] **Step 3: 写实现**

新建 `internal/executor/turn/deny.go`：

```go
// deny.go —— 协调者拒绝时下发给模型的正文渲染。
//
// 职责：
//   - 提供拒绝理由正文的唯一渲染点
//
// 边界：
//   - 不决定「送不送」「怎么送」：同帧送达在 claude adapter，带外注入在 agentd
//
// 为什么单独抽出来：同一段话有两个出口——claude 经 permDecision.Message 与裁决
// 同帧送达，其余 executor 经 manager 的带外注入。两处措辞若各写各的，同一件事
// 在不同 executor 上读起来会像两回事，而这段话正是要让模型改变做法的那段。
package turn

// DenyGuidanceText 渲染「操作被拒 + 理由 + 别再重试」的正文。
//
// 参数：reason 为协调者给出的原因，调用方保证已 trim 且非空
// 返回：可直接下发给模型的正文
//
// 注意：末句「不要重复发起同一请求」不是客套——不给这句，模型被拒后最常见的
// 下一步就是原地再试一次同样的操作，白烧一个回合。
func DenyGuidanceText(reason string) string {
	return "你请求的操作已被协调者拒绝。原因：" + reason +
		"\n请据此调整做法后继续，不要重复发起同一请求。"
}
```

`internal/executor/claudecode/adapter.go` 的 `RespondPermission`，把

```go
	behavior, msg := "allow", ""
	if decision != "once" {
		behavior, msg = "deny", "协调者拒绝了本次操作"
	}
```

改为

```go
	behavior, msg := "allow", ""
	if decision != "once" {
		behavior = "deny"
		// 理由与裁决同帧：msg 会作为 tool_result 正文当场回给模型（perm.go 的
		// Message → cmd/permission_mcp.go），模型在同一个回合里就知道该怎么改。
		// 走带外注入的话，实测要迟到整整一个回合，中间那段空窗模型会自行发挥
		// （B137 来源：B128 真机验收 seq32→seq33）
		if r := strings.TrimSpace(reason); r != "" {
			msg = turn.DenyGuidanceText(r)
		} else {
			// 协调者没给理由：送一句空的比送通用句更差，模型会以为理由缺失是异常
			msg = "协调者拒绝了本次操作"
		}
	}
	a.log.Info("claude 回发拒绝裁决", "task", taskID, "perm", permID,
		"with_reason", msg != "协调者拒绝了本次操作")
```

在同文件加：

```go
// DenyReasonInBand 表明本 adapter 把拒绝理由与裁决同帧送达模型。
//
// 返回恒为 true：理由进 permDecision.Message，经裁决 socket 回到
// cmd/permission_mcp.go，再作为 tool_result 正文当场交给模型。
//
// manager 据此跳过 B50 的带外挂起注入——两条路都走会让模型被同一条理由说两遍。
func (a *Adapter) DenyReasonInBand() bool { return true }
```

补 import：`strings` 与 `github.com/Xsxdot/handoff/internal/executor/turn`（该包大概率已被 import）。

`internal/agentd/manager.go` 的 `relayDenyGuidance`，把内联拼串改为

```go
	text := turn.DenyGuidanceText(guidance)
```

并在该函数 doc 里补一句：`正文渲染与 claude 的同帧送达共用 turn.DenyGuidanceText，两条路措辞必须一致`。补 import。

- [ ] **Step 4: 跑测试确认它通过**

Run: `go test ./internal/executor/turn/ ./internal/executor/claudecode/ ./internal/agentd/ -v`
Expected: 全部 PASS

- [ ] **Step 5: 日志与注释自检**

- `claude 回发拒绝裁决` 的 Info 带 `task` / `perm` / `with_reason`——**不打理由原文**：
  它已随工单落库可查，日志里再抄一遍徒增体积且可能含敏感路径
- 同帧那段注释写明「不这么做会迟到一个回合」以及来源（B128 验收 seq32→seq33）
- `DenyGuidanceText` 的 doc 写明末句为什么不是客套
- `deny.go` 文件头写清「为什么单独抽出来」

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "fix(claude): 拒绝理由与裁决同帧送达，正文渲染与带外注入共用一处"
```

---

### Task 6: manager 按能力位跳过带外挂起

**Files:**
- Modify: `internal/agentd/manager.go`
- Test: `internal/agentd/manager_test.go`
- Modify: `README.md`、`README.zh-CN.md`

**Interfaces:**
- Consumes: Task 5 的 `(a *Adapter) DenyReasonInBand() bool`
- Produces: `denyReasonInBander` 可选接口；`(m *Manager) denyReasonDelivered(taskID string) bool`；
  `(m *Manager) noteDenyGuidanceUnlessInBand(taskID, permID, reason string)`；
  测试侧 `(a *chanAdapter) setDenyReasonInBand(bool)`

- [ ] **Step 1: 写失败的测试**

`internal/agentd/manager_test.go` 追加：

先给 `chanAdapter`（`manager_test.go:77` 那个，`newTestManager` 把它注册为 `"fake"`）
加一个可注入开关。**注意注入点选它而不是 `fake.Fake`**：`fake.Fake` 要留在
「未实现该可选接口」那一侧，好让所有既有的 fake 基础用例继续覆盖类型断言失败的分支。

```go
// chanAdapter 结构体追加字段（放在 respondErr 之后）：

	// denyInBand 让本 adapter 可选地自报「拒绝理由已与裁决同帧送达」，
	// 供 B137 的两条分支各测一次
	denyInBand bool
}

// setDenyReasonInBand 设置本 adapter 自报的同帧送达能力（默认 false）。
func (a *chanAdapter) setDenyReasonInBand(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.denyInBand = v
}

// DenyReasonInBand 实现 manager 的同名可选接口。
func (a *chanAdapter) DenyReasonInBand() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.denyInBand
}
```

再追加两条用例：

```go
// TestDenyGuidanceSkippedWhenInBand 钉住能力位真的被用上：adapter 自报同帧送达
// 时不得再挂起带外注入，否则模型先在 tool_result 里读到理由、下一条 question
// 时又被同一条理由砸一次。
func TestDenyGuidanceSkippedWhenInBand(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	ad.setDenyReasonInBand(true)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	m.noteDenyGuidanceUnlessInBand("T1", "perm-1", "别删，先 git mv 归档")

	if got := m.takeDenyGuidance("T1"); got != "" {
		t.Fatalf("挂起的理由 = %q，期望空——adapter 已同帧送达，不该再挂一份", got)
	}
}

// TestDenyGuidanceKeptWhenNotInBand B50 的既有行为不许回归：adapter 不自报同帧
// 送达时仍必须挂起，否则理由一句都到不了模型手里。
func TestDenyGuidanceKeptWhenNotInBand(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	ad.setDenyReasonInBand(false)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	m.noteDenyGuidanceUnlessInBand("T1", "perm-1", "别删，先 git mv 归档")

	if got := m.takeDenyGuidance("T1"); got != "别删，先 git mv 归档" {
		t.Fatalf("挂起的理由 = %q，B50 的既有行为不许回归", got)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestDenyGuidance -v`
Expected: `TestDenyGuidanceSkippedWhenInBand` FAIL（条目仍被挂起）

- [ ] **Step 3: 写实现**

`internal/agentd/manager.go`，紧跟 `volatilePermitter` 之后：

```go
// denyReasonInBander 表示该 adapter 能把拒绝理由与裁决同帧送达模型
// （claude：理由进 permDecision.Message，作为 tool_result 正文当场回给模型）。
//
// 不实现本接口的 adapter（grok/codex 的原生协议没有消息字段，opencode 走的
// 老端点会丢弃额外字段，见 spec §2.5）行为不变，仍走 B50 的带外挂起注入。
type denyReasonInBander interface {
	DenyReasonInBand() bool
}

// denyReasonDelivered 判断本任务的 executor 是否已把拒绝理由同帧送达。
//
// 参数：taskID 为任务 id
// 返回：true 表示理由已到模型手里
//
// 注意：返回 true 时调用方**不得**再 noteDenyGuidance——两条路都走会让模型
// 被同一条理由说两遍。解析 adapter 失败时保守返回 false：宁可多说一遍，
// 也不能让理由一句都没送到。
func (m *Manager) denyReasonDelivered(taskID string) bool {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Warn("判定拒绝理由送达方式时解析执行者失败，按带外注入处置",
			"task", taskID, "cause", err)
		return false
	}
	d, ok := ad.(denyReasonInBander)
	return ok && d.DenyReasonInBand()
}
```

再加一个薄封装，让两处调用点共用同一段判断（**不要把 if/else 内联到两处**——
内联的分支没法直接测，而它正是本 task 唯一的行为改动）：

```go
// noteDenyGuidanceUnlessInBand 按 executor 的送达能力决定要不要挂起拒绝理由。
//
// 参数：taskID / permID 用于日志定位；reason 为协调者给出的原因
//
// executor 已把理由与裁决同帧送达时直接返回：再挂一份会让模型先在 tool_result
// 里读到理由、下一条 question 时又被同一条理由砸一次。
func (m *Manager) noteDenyGuidanceUnlessInBand(taskID, permID, reason string) {
	if m.denyReasonDelivered(taskID) {
		m.log.Debug("拒绝理由已与裁决同帧送达，跳过带外挂起",
			"task", taskID, "perm", permID)
		return
	}
	// 拒绝原因挂起（B50）：executor 收 reject 会当场终结回合，此刻 Send 会撞上
	// 正在终结的回合；挂起到下一条 question 到达时再下发
	m.noteDenyGuidance(taskID, reason)
}
```

两处调用点（`RelayAnswer` 的 gate 分支与 `waitPermission`）各自把

```go
	m.noteDenyGuidance(taskID, reason)
```

改为

```go
	m.noteDenyGuidanceUnlessInBand(taskID, permID, reason)
```

原先那两行 B50 注释随之上移进封装里，调用点不再重复。

- [ ] **Step 4: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -v`
Expected: 全包 PASS，含 `TestDenyGuidanceRelayedOnNextQuestion`、
`TestDenyGuidanceConsumedOnce`、`TestDenyGuidanceDroppedWakesOnTurnEnd` 三条既有用例

- [ ] **Step 5: 更新 README 两个语种**

`README.md` 的 **Permission tiers** 段末尾追加一句：

```
Write targets outside the task's workspace — including shell redirect targets such as
`echo x > ~/.zshrc` — always escalate to the human, never to the approver.
```

`README.zh-CN.md` 的 **权限分级** 段末尾追加：

```
写到任务范围外的落点——包括 `echo x > ~/.zshrc` 这类 shell 重定向落点——一律升级人工，不经审批者。
```

- [ ] **Step 6: 日志与注释自检**

- `denyReasonDelivered` 解析失败分支是 Warn 且带 `cause`，并在 doc 里写明「保守返回 false」的理由
- 跳过分支有 Debug 日志（带 task 与 perm），否则「理由去哪了」在日志里断线
- B50 的原注释上移进 `noteDenyGuidanceUnlessInBand`，没有被删掉
- 两处调用点确实都改成了封装调用，没有漏掉 `waitPermission` 那处

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "fix(agentd): 理由已同帧送达时不再带外挂起，避免模型被同一条理由说两遍"
```

---

## 交付后由审核者做（不派发）

以下步骤要驱动 handoff 自身（起 agentd、派子任务、调 `handoff` CLI），与执行纪律块
「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」直接冲突（B126）。
**执行者不要尝试，也不要在 ledger 里写它们的结论。**

1. **B134 真机复验**：起隔离 DataDir + 非默认端口的 agentd（不碰生产实例），派一个
   opencode 任务执行 `echo x > /tmp/<唯一串>.txt`，判据是——该命令产出
   `permission_request`（而不是零请求直接写成功），agentd 日志有 WARN 级
   `权限判定：升级人工 … reason="目标路径越出任务范围: /tmp/<唯一串>.txt"`，
   拒绝后该文件不存在。对照组：`go test ./... > /dev/null` 不产生工单。
2. **B137 真机复验**：派一个 claude 任务，对它的权限请求
   `reply --deny --reason "<一段可辨识的独特文本>"`，判据是——**同一回合内**模型的
   下一条输出复述该文本，且事件流中**没有** `deny_guidance_relayed`（理由已同帧到达，
   不再走带外）。
3. **变异测试**（沿 B47/B57/B91 先例）：至少验三处——摘掉 `judgeBash` 的路径循环、
   把 `IsDiscardTarget` 恒返回 true、摘掉 `denyReasonDelivered` 的类型断言，
   确认各有用例单独 FAIL 且不互相兜底。
