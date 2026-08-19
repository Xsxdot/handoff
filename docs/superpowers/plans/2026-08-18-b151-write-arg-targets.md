# B151 参数位写落点判据 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让「路径在参数位」的写命令（`tee`/`cp`/`mv`/`ln`/`install`/`dd`）像重定向落点一样受确定性范围判定，并堵上 opencode 静态表对 `tee` 的静默放行。

**Architecture:** 两件事捆绑做。① permgate 新增 `WriteArgTargets`——与 `RedirectTargets` 同构的、执行器无关的落点提取器，从命令原文里摘出写命令的目的地参数，接进 `judgeBash` 已有的范围判定循环。② opencode 的静态表补一条 `tee` 的 ask 模式，让这类命令**先送得进来**——今天它连权限请求都不发。只做①等于给一条没人走的路铺砖；只做②等于把命令推给廉价模型判。

**Tech Stack:** Go 1.26，标准库；`internal/permgate`（判据）、`internal/executor/opencode`（静态表）。

## 背景：两趟真机探针的实测结论（判据来源，不要重新推测）

2026-08-18 在 mac-02 上跑的两个探针任务，结论逐条如下：

| 命令形态 | opencode（默认执行器，任务 `4feb3766`） | claude（任务 `b0327cd8`） |
|---|---|---|
| `cp go.mod /tmp/x` | `external_directory` **检出**，`paths=[/tmp]` → permgate `InScope` → **升级人工** | 请求里 `paths` 为空 → `交审批者 黑名单未命中` |
| `echo x \| tee /tmp/y` | **零权限请求，文件实写 13 字节** | 同上，`交审批者 黑名单未命中` |
| `cp a ./区内文件` | 零请求直接执行（无噪音） | — |

要害：**opencode 对「管道后的写命令」是盲的**，而它的静态表末尾是 `"*": "allow"`，于是这类命令连 permgate 都到不了——不是判据不够，是请求没发出来。claude 侧则是另一种缺失：请求到得了 permgate，但 `Paths` 恒为空、bash 路由没有参数解析。

## 判据已在基线上跑过（不要重新推测）

写这份 plan 时，协调者把三个 task 的代码在 `3cf547c93` 上完整跑了一遍再落笔，所以下面的
「Expected」都是实测输出而不是预期：

- Task 1 的 `WriteArgTargets` 全部子用例通过；**首版撞了一次编译错误**——包内已有
  `splitSegments`（`selfcmd.go:78`），已在本 plan 里改名为 `splitWriteSegments`。
- Task 2 的 `TestJudgeBashWriteArgEscalate` 在改动前的失败原文就是
  `Action = consult，期望 Escalate（reason="黑名单未命中"）`；`TestJudgeBashWriteArgInScopeFallsBack`
  改动前就是绿的（它是防回归基线）。
- Task 3 的失败原文就是 `模式 "*tee*" = ""，期望 ask`。
- 三处都落地后 `gofmt -l` 无输出、`go test ./... -count=1` 全绿。

**执行者仍要自己跑一遍**——上面只说明判据本身没写错，不代表你的实现一定对。

## Global Constraints

- **不要动 `judgeCommand`**。它「永不返回 AutoAllow」是写进 doc 的不变式，本轮不碰。
- **落点在范围内时必须落回 `judgeCommand`**，不得因为「有落点」就升级——否则每次往工作区里 `tee` 一份日志都叫人。
- **越界文案逐字复用现有那句** `目标路径越出任务范围: %s`：B27/B134 的真机验收记录按这句话 grep 日志，改字面量就等于让历史取证失效。
- **丢弃落点必须豁免**：复用 `IsDiscardTarget`，`| tee /dev/null` 不得升级。
- 新代码与 `redirect.go` 同包（`package permgate`），可直接复用未导出的 `readTarget` / `expandTilde`，**不要另写一份**。
- **包内已有一个 `splitSegments`（`selfcmd.go:78`），名字不能撞**：本轮新写的那个叫 `splitWriteSegments`。两者语义不同，不要合并——理由写在函数注释里。
- `gofmt -l` 必须无输出；`go build ./...`、`go vet ./...` 退 0；`go test ./... -count=1` 全绿。

---

### Task 1: `WriteArgTargets`——从命令原文摘写命令的目的地参数

**Files:**
- Create: `internal/permgate/writeargs.go`
- Test: `internal/permgate/writeargs_test.go`

**Interfaces:**
- Consumes: 同包内的 `readTarget(rs []rune, i int) (string, int)`、`expandTilde(p string) string`（都在 `redirect.go`）
- Produces: `func WriteArgTargets(cmd string) []string`——Task 2 在 `judgeBash` 里调用它

- [ ] **Step 1: 写失败的测试**

在 `internal/permgate/writeargs_test.go`：

```go
package permgate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteArgTargets(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到 home，跳过 ~ 展开用例")
	}
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"管道后的 tee", "echo x | tee /tmp/y", []string{"/tmp/y"}},
		{"tee 带追加标志", "echo x | tee -a /tmp/y", []string{"/tmp/y"}},
		{"tee 多个落点", "echo x | tee a.log /tmp/y", []string{"a.log", "/tmp/y"}},
		{"tee 落到家目录", "echo x | tee ~/.zshrc", []string{filepath.Join(home, ".zshrc")}},
		{"cp 取目的地不取源", "cp a.txt /etc/x", []string{"/etc/x"}},
		{"cp 带递归标志", "cp -r src dst", []string{"dst"}},
		{"cp 的 -t 形态", "cp -t /etc a.txt b.txt", []string{"/etc"}},
		{"cp 的长选项 -t 形态", "cp --target-directory=/etc a.txt", []string{"/etc"}},
		{"mv 取目的地", "mv a ~/b", []string{filepath.Join(home, "b")}},
		{"ln 取目的地", "ln -s /etc/passwd ./link", []string{"./link"}},
		{"install 取目的地", "install -m 755 bin /usr/local/bin/x", []string{"/usr/local/bin/x"}},
		{"dd 的 of= 形态", "dd if=/dev/zero of=/tmp/x bs=1", []string{"/tmp/x"}},
		{"落点带引号且含空格", `cp a.txt "/tmp/two words.txt"`, []string{"/tmp/two words.txt"}},
		{"-- 之后不再当标志", "cp -- -weird.txt /tmp/z", []string{"/tmp/z"}},

		// 以下必须摘不出落点——它们是误伤面，摘出来就会平白升级
		{"命令名不是写命令", "ls /usr/bin/tee /bin", nil},
		{"写命令名只出现在引号里", `git commit -m "cp a /etc/x"`, nil},
		{"写命令名只是别的词的一部分", "go test ./internal/steering/...", nil},
		{"只有一个参数的 cp 不算完整命令", "cp a.txt", nil},
		{"纯重定向不归本函数管", "echo x > /tmp/y", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WriteArgTargets(c.cmd)
			if len(got) != len(c.want) {
				t.Fatalf("WriteArgTargets(%q) = %v，期望 %v", c.cmd, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("第 %d 个落点 = %q，期望 %q（全部：%v）", i, got[i], c.want[i], got)
				}
			}
		})
	}
}

// TestWriteArgTargetsSegmentsIndependently 钉住分段：复合命令里每一段各判各的，
// 前一段是无害命令不影响后一段被摘出落点。
func TestWriteArgTargetsSegmentsIndependently(t *testing.T) {
	got := WriteArgTargets("go build ./... && cp bin /usr/local/bin/x ; echo done")
	if len(got) != 1 || got[0] != "/usr/local/bin/x" {
		t.Fatalf("复合命令的落点 = %v，期望 [/usr/local/bin/x]", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/permgate/ -run TestWriteArgTargets -count=1`
Expected: FAIL，`undefined: WriteArgTargets`

- [ ] **Step 3: 实现**

新建 `internal/permgate/writeargs.go`：

```go
// writeargs.go —— 「落点在参数位」的写命令目的地提取。
//
// 职责：
//   - 从命令原文里摘出 tee/cp/mv/ln/install/dd 会写到哪个路径，供 judgeBash 做范围判定
//   - 展开 ~ 前缀（与 redirect.go 同一套语义）
//
// 边界：
//   - 不做完整 shell 解析：不展开变量、不解析子 shell、不跟 cd
//   - 不判断路径是否越界：范围判定在 path.go
//   - 不管重定向：那是 redirect.go 的事，两者由 judgeBash 分别调用后合并
//
// 为什么需要它：重定向落点由 RedirectTargets 覆盖，但同样是写仓库外，
// `echo x | tee /tmp/y`、`cp secret /outside`、`dd of=/tmp/x` 的落点在**参数位**，
// 摘不出来就只能落 Consult 交廉价模型（2026-08-18 真机实测：claude 上这两条都是
// `交审批者 黑名单未命中`）。
//
// 为什么只看每一段的**首个词元**：这样 `git commit -m "cp a /etc/x"` 天然不误伤——
// 段首是 git，引号里的 cp 不是命令。误伤一次的代价是平白叫醒协调者，比漏判更常发生。
package permgate

import "strings"

// argTargetKind 描述某个写命令的落点在哪几个参数位。
type argTargetKind int

const (
	// targetAllArgs：所有非标志参数都是落点（tee 会写到它列出的每一个文件）
	targetAllArgs argTargetKind = iota
	// targetLastArg：最后一个非标志参数是目的地，其余是源（cp/mv/ln/install）
	targetLastArg
	// targetOfPrefix：落点写在 of=PATH 里（dd）
	targetOfPrefix
)

// writeCommands 是「落点在参数位」的写命令表。
//
// 只收**确定会写文件**的命令。刻意不收 `sed`：它的 -i 形态落点是源文件本身，
// 语义与本表三种都不同，单独处理才不会把 `sed s/x/y/ file` 这类只读用法误判成写。
// 每次修改本表必须同步 writeargs_test 的逐条断言。
var writeCommands = map[string]argTargetKind{
	"tee":     targetAllArgs,
	"cp":      targetLastArg,
	"mv":      targetLastArg,
	"ln":      targetLastArg,
	"install": targetLastArg,
	"dd":      targetOfPrefix,
}

// WriteArgTargets 从命令串里摘出全部「参数位写落点」。
//
// 参数：cmd 为命令原文（**不要**先过 StripQuoted：落点常被引号包住，剥完就没了）
// 返回：落点路径切片，按出现顺序；没有则返回 nil
//
// 注意：
//   - 按 `|`、`&`、`;` 分段，逐段判首个词元是不是写命令——只有是，才摘该段的落点
//   - `--` 之后的词元一律不当标志看
//   - `~` 与 `~/xxx` 展开为当前用户 home（why 见 expandTilde）
//   - 丢弃落点（/dev/null 等）**不在这里过滤**：由 judgeBash 统一用 IsDiscardTarget 跳过，
//     两个落点来源共用同一处豁免，不会两边写两套
func WriteArgTargets(cmd string) []string {
	var out []string
	for _, seg := range splitWriteSegments(cmd) {
		toks := tokenize(seg)
		if len(toks) == 0 {
			continue
		}
		name := baseName(toks[0])
		kind, ok := writeCommands[name]
		if !ok {
			continue
		}
		for _, t := range targetsOf(kind, toks[1:]) {
			out = append(out, expandTilde(t))
		}
	}
	return out
}

// targetsOf 按落点种类从参数列表里挑出目的地。
//
// 参数：kind 为落点种类，args 为命令名之后的全部词元
// 返回：落点切片；判不出（如 cp 只给了一个参数）时返回 nil
func targetsOf(kind argTargetKind, args []string) []string {
	if kind == targetOfPrefix {
		var out []string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "of="); ok && v != "" {
				out = append(out, v)
			}
		}
		return out
	}

	var plain []string // 非标志参数
	var byFlag []string
	endOfFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endOfFlags && a == "--" {
			endOfFlags = true
			continue
		}
		if !endOfFlags && strings.HasPrefix(a, "-") && a != "-" {
			// -t DIR / --target-directory=DIR：目的地在标志上，其余参数全是源
			if v, ok := strings.CutPrefix(a, "--target-directory="); ok && v != "" {
				byFlag = append(byFlag, v)
				continue
			}
			if a == "-t" || a == "--target-directory" {
				if i+1 < len(args) {
					byFlag = append(byFlag, args[i+1])
					i++
				}
				continue
			}
			continue
		}
		plain = append(plain, a)
	}
	if len(byFlag) > 0 {
		return byFlag
	}
	switch kind {
	case targetAllArgs:
		return plain
	case targetLastArg:
		// 只有一个参数时判不出源与目的地（cp a.txt 本身就不是完整命令），不猜
		if len(plain) < 2 {
			return nil
		}
		return plain[len(plain)-1:]
	}
	return nil
}

// splitWriteSegments 按 shell 的命令分隔符把命令串切成段，引号内的分隔符不算。
//
// 参数：cmd 为命令原文
// 返回：各段原文（可能含前导空白），空段丢弃
//
// 为什么不复用 selfcmd.go 里的 splitSegments：那个用 strings.FieldsFunc，
// **不认引号**——`git commit -m "a | tee /etc/x"` 会被它切出一段 `tee /etc/x`，
// 在本文件里就成了一次凭空的越界升级。那边不认引号是刻意的（judgeCommand 对
// 原文与 StripQuoted 结果各跑一遍来覆盖包装器），改它会动到自指令判据，不碰。
func splitWriteSegments(cmd string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for _, r := range cmd {
		switch {
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			b.WriteRune(r)
		case quote != 0 && r == quote:
			quote = 0
			b.WriteRune(r)
		case quote == 0 && (r == '|' || r == '&' || r == ';' || r == '\n'):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

// tokenize 把一段命令切成词元，引号内的空白不分词、包裹引号被去掉。
//
// 参数：seg 为单段命令原文
// 返回：词元切片
//
// 复用 readTarget 而不是另写一个扫描器：落点词元与命令词元的边界规则是同一套，
// 两份实现迟早会漂移。
func tokenize(seg string) []string {
	var out []string
	rs := []rune(seg)
	for i := 0; i < len(rs); {
		switch rs[i] {
		case ' ', '\t', '\n':
			i++
			continue
		case '>', '<', '(', ')':
			// 重定向与子 shell 不归本文件管，遇到就停：后面的词元不再是本命令的参数
			return out
		}
		tok, next := readTarget(rs, i)
		if next == i { // 防御：readTarget 未推进则强制前进，避免死循环
			i++
			continue
		}
		i = next
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// baseName 取命令名的最后一段，让 /usr/bin/tee 与 tee 判成同一个命令。
func baseName(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/permgate/ -run TestWriteArgTargets -count=1 -v`
Expected: PASS，全部子用例通过

- [ ] **Step 5: 日志与注释自检**

本 task 全是纯函数、无 I/O、无外部调用、无状态变更，**按 instrumenting-code 的例外条款不加日志**——`redirect.go` 同理也没有日志，运行期的可观测性由 Task 2 的 `judgeBash` 承担（那里才拿得到 scope 与判定结论）。逐项确认：

- [ ] 新文件有文件头注释（职责 + 边界 + 「为什么需要它」）
- [ ] 导出函数 `WriteArgTargets` 有参数/返回/注意事项
- [ ] `writeCommands` 表上方写清了收条与不收条的理由（为什么不收 `sed`）
- [ ] `targetsOf` 里「只有一个参数时不猜」这条边界有中文注释说明为什么

- [ ] **Step 6: Commit**

```bash
git add internal/permgate/writeargs.go internal/permgate/writeargs_test.go
git commit -m "feat(permgate): 摘出参数位写落点（tee/cp/mv/ln/install/dd）"
```

---

### Task 2: 接进 `judgeBash`，让参数位落点走同一条范围判定

**Files:**
- Modify: `internal/permgate/permgate.go:213-235`（`judgeBash`）
- Test: `internal/permgate/permgate_test.go`

**Interfaces:**
- Consumes: Task 1 的 `WriteArgTargets(cmd string) []string`
- Produces: 无新导出符号；`judgeBash` 的行为变化由 Task 3 依赖

- [ ] **Step 1: 写失败的测试**

在 `internal/permgate/permgate_test.go` 末尾追加：

```go
// TestJudgeBashWriteArgEscalate 钉住 B151 主修：落点在参数位的写命令越界，
// 必须升级人工，而不是落 Consult 交廉价模型。
//
// 真机基线（2026-08-18，claude 任务 b0327cd8）：这两条当时都判成
// `交审批者 黑名单未命中`——本用例就是那条基线的反面。
func TestJudgeBashWriteArgEscalate(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []struct {
		name string
		cmd  string
	}{
		{"管道后的 tee 写到 /tmp", "echo x | tee /tmp/b151-probe.txt"},
		{"tee 追加到家目录", "echo x | tee -a ~/.zshrc"},
		{"cp 到仓库外", "cp go.mod /tmp/b151-cp.txt"},
		{"mv 到仓库外", "mv go.mod /etc/go.mod"},
		{"dd 写到仓库外", "dd if=/dev/zero of=/tmp/b151-dd.bin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Text: "Bash: " + c.cmd, Command: c.cmd}, scope)
			if v.Action != Escalate {
				t.Fatalf("Action = %v，期望 Escalate（reason=%q）", v.Action, v.Reason)
			}
			if !strings.Contains(v.Reason, "目标路径越出任务范围") {
				t.Fatalf("Reason = %q，必须逐字复用越界文案", v.Reason)
			}
		})
	}
}

// TestJudgeBashWriteArgInScopeFallsBack 误伤面：落点在工作区内、或压根不是写命令时，
// 必须落回命令判据。少了这条，每次 `go test | tee out.log` 都要叫人。
func TestJudgeBashWriteArgInScopeFallsBack(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []string{
		"go test ./... | tee out.log",
		"cp a.txt b.txt",
		"cp go.mod " + wd + "/copy.mod",
		"echo x | tee /dev/null",
		"ls /usr/bin/tee",
		`git commit -m "cp a /etc/x"`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Text: "Bash: " + cmd, Command: cmd}, scope)
			if v.Action == Escalate {
				t.Fatalf("不该升级：Action = %v，reason = %q", v.Action, v.Reason)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/permgate/ -run TestJudgeBashWriteArg -count=1`
Expected: `TestJudgeBashWriteArgEscalate` FAIL（实得 Consult，reason `黑名单未命中`）；`TestJudgeBashWriteArgInScopeFallsBack` 此时应当已经通过（改动前它就不升级）——两条都要跑，后者是防回归的基线。

- [ ] **Step 3: 实现**

把 `internal/permgate/permgate.go` 的 `judgeBash` 里这一行：

```go
	targets := append(append([]string(nil), req.Paths...), RedirectTargets(req.Command)...)
```

替换为：

```go
	// 落点有三个来源，判定规则完全一致，合并后走同一个循环：
	//   - req.Paths：executor 自己检出的（opencode 的 external_directory）
	//   - RedirectTargets：handoff 从重定向语法里摘的（B134）
	//   - WriteArgTargets：handoff 从写命令参数位摘的（B151）——claude 的 bash
	//     请求 Paths 恒为空，opencode 对管道后的 tee 也检不出，这一路是唯一兜底
	targets := append([]string(nil), req.Paths...)
	targets = append(targets, RedirectTargets(req.Command)...)
	targets = append(targets, WriteArgTargets(req.Command)...)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/permgate/ -count=1`
Expected: PASS（含既有的 `TestJudgeBashPathsEscalate`、`TestJudgeBashInScopeFallsBack` 不得回归）

- [ ] **Step 5: 补日志**

`judgeBash` 已有的两条 Debug（`命令落点是丢弃设备，跳过范围判定` / `命令落点在任务范围内`）对新落点自动生效，无需重复。**需要补的是「落点从哪来」**——三个来源合并后，日志里看不出某个落点是重定向摘的还是参数位摘的，排障时分不清该怀疑哪段代码。在合并之后、循环之前加一行：

```go
	if n := len(targets); n > 0 {
		g.log.Debug("命令落点已汇总", "count", n,
			"from_executor", len(req.Paths),
			"from_redirect", len(RedirectTargets(req.Command)),
			"from_write_args", len(WriteArgTargets(req.Command)))
	}
```

**不要**把这条提到 Info：判定结论的权威 WARN/INFO 落在 agentd 的 `judgePermission`（那里才有 task/perm id），这里重复打会让同一件事在日志里出现两遍——与 `selfCmdVerdict` 的注释同一条理由。

- [ ] **Step 6: 注释自检**

- [ ] 三个落点来源的合并处有中文注释说明每一路各自覆盖什么、为什么第三路是唯一兜底
- [ ] 新增的两个测试函数各有 doc 注释，写明它钉的是哪条真机基线

- [ ] **Step 7: Commit**

```bash
git add internal/permgate/permgate.go internal/permgate/permgate_test.go
git commit -m "fix(permgate): 参数位写落点接入范围判定，越界升人工不再落 Consult（B151）"
```

---

### Task 3: opencode 静态表补 `tee`——先让它送得进来

**Files:**
- Modify: `internal/executor/opencode/taskenv.go:67-87`（`bashPermissionRules`）
- Test: `internal/executor/opencode/taskenv_test.go`

**Interfaces:**
- Consumes: 无
- Produces: 无新导出符号

- [ ] **Step 1: 写失败的测试**

在 `internal/executor/opencode/taskenv_test.go` 末尾追加：

```go
// TestBashRulesAskOnTee 钉住 B151 的 opencode 半边。
//
// 真机基线（2026-08-18，任务 4feb3766）：`echo oc-tee-probe | tee /tmp/b151-oc-tee.txt`
// **零权限请求、文件实写 13 字节**——opencode 的 external_directory 对管道后的写命令
// 是盲的，而静态表末尾是 "*": "allow"，于是它连 permgate 都到不了。
//
// 为什么是宽模式 "*tee*" 而不是 "*tee /*" 那种锚定形态：落点可以被标志隔开
// （tee -a /tmp/x）、被引号包住、跟在管道后也可以不跟，锚定形态要枚举四五条还留缝。
// 而 tee 在构建/测试流里本就低频，误伤的代价只是一次 Consult，不是拦截。
// 这与重定向那四条的取舍方向不同，是因为那边要避开高频的 2>&1。
func TestBashRulesAskOnTee(t *testing.T) {
	quietLog(t)
	configPath, _, err := opencode.WriteTaskEnv(t.TempDir(), "t1", "", "plan", "")
	if err != nil {
		t.Fatalf("WriteTaskEnv: %v", err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取 opencode.json: %v", err)
	}
	var cfg struct {
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("opencode.json 解析失败: %v", err)
	}
	if got := cfg.Permission.Bash["*tee*"]; got != "ask" {
		t.Fatalf(`模式 "*tee*" = %q，期望 ask——少了它，| tee /tmp/x 零请求实写（B151）`, got)
	}
	// 兜底 allow 必须还在：本轮只补漏，不反转整张表（B150 已并入 B151 并记明理由）
	if got := cfg.Permission.Bash["*"]; got != "allow" {
		t.Fatalf(`兜底模式 "*" = %q，期望 allow——本轮不反转静态表`, got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestBashRulesAskOnTee -count=1`
Expected: FAIL，`模式 "*tee*" = ""，期望 ask`

- [ ] **Step 3: 实现**

在 `internal/executor/opencode/taskenv.go` 的 `bashPermissionRules` 里，`"*": "allow"` 那一行**之前**加：

```go
	// tee：落点在参数位，opencode 的 external_directory 对管道后的写命令检不出
	// （2026-08-18 真机任务 4feb3766：`echo x | tee /tmp/y` 零权限请求、文件实写）。
	// 用宽模式而不是 "*tee /*" 这种锚定形态：落点可以被标志隔开（tee -a /tmp/x）、
	// 被引号包住，锚定要枚举四五条还留缝；tee 在构建/测试流里低频，误伤只是一次
	// Consult。送进来之后由 permgate 的 WriteArgTargets 给确定性裁决（B151）。
	"*tee*": "ask",
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -count=1`
Expected: PASS（既有的 `TestBashRulesRejectBareRedirectGlob`、`TestWriteTaskEnv` 不得回归）

- [ ] **Step 5: 日志与注释自检**

`WriteTaskEnv` 已有 `INFO opencode 任务环境已生成` 一行，规则条数是它的字段之一，新增模式自动被计入，**不另加日志**。逐项确认：

- [ ] 新模式上方有中文注释，写清它拦什么、为什么用宽模式、送进来之后谁给裁决
- [ ] 注释里带上真机任务号 `4feb3766`，让后来人能回溯判据来源

- [ ] **Step 6: Commit**

```bash
git add internal/executor/opencode/taskenv.go internal/executor/opencode/taskenv_test.go
git commit -m "fix(opencode): tee 落 ask，管道后的写命令不再零请求实写（B151）"
```

---

### Task 4: 整分支终审与全量门

**Files:**
- 无新增；本 task 只跑门与复查

- [ ] **Step 1: 全量门**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
```

Expected: `gofmt -l` 无输出；三条命令全部退 0。**任何一条不过就地修，不要记账搁置。**

- [ ] **Step 2: 对着分支起点看一遍完整 diff**

```bash
# 分支起点 = 本任务第一个提交的父提交（本分支从 origin/handoff/web-console 开出）
BASE=$(git log --format=%H --reverse origin/handoff/web-console..HEAD | head -1)^
git diff $BASE..HEAD
```

取不到 `origin/handoff/web-console` 时退回：`git diff $(git log --format=%H --reverse HEAD | sed -n '1p')^..HEAD` 之外，直接用 `git log --oneline` 数出本轮的三个提交、取最早那个的父提交也行。

逐项确认：

- [ ] `judgeCommand` 一个字没改（Global Constraints 第一条）
- [ ] 越界文案仍是 `目标路径越出任务范围: %s`，一字未动
- [ ] `bashPermissionRules` 里 `"*": "allow"` 仍在，四条重定向模式仍在
- [ ] 没有新增任何 `fmt.Printf` / `println`
- [ ] 新文件有文件头注释，导出函数有 doc 注释

- [ ] **Step 3: Commit（若终审有修复）**

```bash
git add -A
git commit -m "chore: B151 终审修复"
```

无修复项则跳过本步，不要造空提交。

---

## 交付后由审核者做（不派发）

这几步要驱动 handoff 自身（起任务、看 agentd 日志），与派发纪律块里的「不要调用 handoff CLI」直接冲突，**留在本地做**：

- [ ] 部署新构建到 mac-02（停 launchd → 删了重建二进制 → bootstrap），确认 `handoff status --target mac-02` 的版本与 revision 换过来了
- [ ] 派一个 **opencode** 探针跑 `echo b151-verify | tee /tmp/b151-verify.txt`，判据：agentd.log 出现 `WARN 权限判定：升级人工 … reason="目标路径越出任务范围: /tmp/b151-verify.txt"`，拒绝后 `ls` 确认文件不存在
- [ ] 同一个任务里跑 `go version | tee ./inside.log`（区内），判据：**零权限请求**或至多一次 Consult，不得升级人工
- [ ] 派一个 **claude** 探针跑 `cp go.mod /tmp/b151-claude.txt`，判据：从改前的 `INFO 交审批者 黑名单未命中` 变成 `WARN 升级人工 … 目标路径越出任务范围`
- [ ] **未验证的一处，本轮不猜**：opencode 的 `external_directory` 对 `mv`/`ln`/`install`/`dd` 检不检出，只对 `cp` 实测过。这几个今天靠 external_directory 兜着，Task 2 之后即使 opencode 不报 Paths，只要命令送得进 permgate 就有判据——但**送不送得进来仍取决于 opencode 的检出**，静态表这轮只补了 tee。要收口就再跑一条同形状的探针，按结果决定要不要给静态表再补模式
