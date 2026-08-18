# 批 1 琐碎修实现计划（B120 / B121 / B82(main) / B101）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修掉四条互不相干的小缺陷——项目名派生不认 Windows 分隔符、Windows 上 opencode 登录态恒定误报、`run` 对已回收工作树报错指错方向、`executor.model` 被套到所有执行者头上。

**Architecture:** 四个任务彼此**完全独立**，无共享代码、无先后依赖，可按任意顺序做。**不要**为它们抽公共抽象——任何「顺手统一一下」都超出范围。每个任务改一到两个文件，各自带一组表驱动单测。

**Tech Stack:** Go 1.26，标准库 `testing`（表驱动），项目自带的 `slog` 日志。

**Spec:** [docs/superpowers/specs/2026-08-18-batch1-small-fixes-design.md](../specs/2026-08-18-batch1-small-fixes-design.md)

## Global Constraints

- **不新增任何配置键。** 配置以 `KnownFields(true)` 严格解析，新键会让配了它的机器跨版本回滚时被旧二进制拒启动、进无限崩溃循环（B88）。本批四条都不需要新键。
- **不改任何对外协议字段**（`internal/proto` 不动）。
- **不新增依赖**，只用标准库。
- **除 Task 3 外不新增日志**，这是 spec §6 的明确裁决，不是遗漏：`internal/toolchain` 的包注释把「不打日志」写成了包边界；`projectNameFromURL` 是纯函数；Task 4 改的赋值条件所在的 `Dispatch` 路径已有完整日志覆盖。**不要因为「加日志总是对的」而破例。**
- **注释一律中文，解释 why 不解释 what。**
- 每个任务结束时 `gofmt` 必须无输出——执行者的 ledger 曾漏过这一项，**必须亲自跑 `gofmt -l .` 看到空输出**，不许凭「IDE 会自动格式化」写结论。
- **没有亲自跑到结果的命令，不许写它的结论。** 跑了但失败就贴原始报错原文，不要替它归因。

---

### Task 1: B120 — 项目名派生认 Windows 分隔符

**Files:**
- Modify: `internal/agentd/projectadmin.go:113-119`（`projectNameFromURL`）
- Test: `internal/agentd/projectadmin_test.go`（新增一个表驱动测试函数，文件已存在）

**Interfaces:**
- Consumes: 无
- Produces: 无（`projectNameFromURL` 是包内私有函数，签名不变：`func projectNameFromURL(url string) string`）

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/projectadmin_test.go` 末尾追加：

```go
// TestProjectNameFromURLHandlesWindowsSeparators 覆盖本地路径 origin 的名字派生。
//
// why 这个用例存在：origin 为 Windows 本地路径（`C:\work\x.git`）时，派生名若不切
// 反斜杠就会是 `\work\x`，撞上 validateProjectName 的「含 / \ : 拒收」，
// 表现为自动登记失败、dispatch 400。
func TestProjectNameFromURLHandlesWindowsSeparators(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"windows 本地路径", `C:\work\probe-origin.git`, "probe-origin"},
		{"windows 本地路径带尾部反斜杠", `C:\work\probe-origin\`, "probe-origin"},
		{"ssh scp 简写（回归）", "git@github.com:Xsxdot/handoff.git", "handoff"},
		{"https（回归）", "https://github.com/Xsxdot/handoff", "handoff"},
		{"https 带尾部斜杠（回归）", "https://github.com/Xsxdot/handoff/", "handoff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := projectNameFromURL(c.in)
			if got != c.want {
				t.Fatalf("projectNameFromURL(%q) = %q，期望 %q", c.in, got, c.want)
			}
			// 派生名必须能过校验，否则自动登记依然会失败
			if err := validateProjectName(got); err != nil {
				t.Fatalf("派生名 %q 未通过 validateProjectName: %v", got, err)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestProjectNameFromURLHandlesWindowsSeparators -v`

Expected: FAIL。前两个子用例报 `projectNameFromURL("C:\\work\\probe-origin.git") = "\\work\\probe-origin"，期望 "probe-origin"`。

> 若 `validateProjectName` 的签名与上面不符（不是 `func validateProjectName(string) error`），
> 以仓库实际签名为准调整断言，**不要**改生产代码去迁就测试。

- [ ] **Step 3: 改实现**

把 `internal/agentd/projectadmin.go` 的 `projectNameFromURL` 改成：

```go
// projectNameFromURL 从 git URL 末段派生缺省引用名（去掉 .git 后缀）。
//
// 例：git@github.com:Xsxdot/handoff.git → handoff
//
// why 分隔符集合里有反斜杠：origin 可以是 Windows 本地路径（`C:\work\x.git`）。
// git URL 的四种形态（https/ssh/scp 简写/file）都不含反斜杠，把它加进集合
// 对既有形态零影响，只有本地路径 origin 会走到这一支。
func projectNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), `/\`)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, `/:\`); i >= 0 {
		s = s[i+1:]
	}
	return s
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestProjectNameFromURLHandlesWindowsSeparators -v`
Expected: PASS（5 个子用例全过）

- [ ] **Step 5: 跑整包回归**

Run: `go test ./internal/agentd/`
Expected: ok。若有别的用例翻红，**先看它是不是本改动引起的**（`git stash` 后重跑对照），不是就照实记，别顺手改。

- [ ] **Step 6: 格式与提交**

```bash
gofmt -l .   # 必须无输出
git add internal/agentd/projectadmin.go internal/agentd/projectadmin_test.go
git commit -m "fix(agentd): 项目名派生认 Windows 路径分隔符（B120）"
```

---

### Task 2: B121 — Windows 上不再谎报 opencode 未登录

**Files:**
- Modify: `internal/toolchain/detect.go`（加一个包级平台缝；改 `credRelPath` 的查表）
- Test: `internal/toolchain/detect_test.go`（`withStubs` 加平台参数；新增两个测试函数）

**Interfaces:**
- Consumes: 无
- Produces: 包级变量 `goos`（`var goos = runtime.GOOS`），仅供本包测试替换；`Detect() []Result` 签名不变

**背景（实现者必读）:** `credRelPath` 里 opencode 那条写的是 XDG 落点 `.local/share/opencode/auth.json`，Windows 上 opencode 不用它，于是探测**恒定误报「未登录」**。本包已有先例：`claude` 被刻意排除在表外，理由是「没有可靠的轻量文件判据，拿它当判据会把没登录的机器报成就绪」。本任务是同一条原则的镜像——拿一个在该平台不成立的路径断言「未登录」，同样是撒谎。**修法是让 Windows 上的 opencode 落进 `Detect` 里已经存在的那条 `!ok → StateAuthUnknown` 分支，不加新分支、不加新状态。**

- [ ] **Step 1: 给测试助手加平台参数**

修改 `internal/toolchain/detect_test.go` 的 `withStubs`，增加第 5 个参数并替换新的 `goos` 缝：

```go
// withStubs 替换四个探测缝（PATH 查找、文件存在、HOME、平台），返回时自动还原。
func withStubs(t *testing.T, home string, inPath map[string]bool, files map[string]bool, platform string) {
	t.Helper()
	oldLook, oldStat, oldHome, oldGOOS := lookPath, statFile, userHomeDir, goos
	lookPath = func(name string) (string, error) {
		if inPath[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	statFile = func(p string) error {
		if files[p] {
			return nil
		}
		return os.ErrNotExist
	}
	userHomeDir = func() (string, error) { return home, nil }
	goos = platform
	t.Cleanup(func() { lookPath, statFile, userHomeDir, goos = oldLook, oldStat, oldHome, oldGOOS })
}
```

然后把该文件里所有**既有** `withStubs(...)` 调用点末尾补上 `, "darwin"`——既有用例的语义就是「在非 Windows 平台上」，行为必须逐字不变。

- [ ] **Step 2: 写失败的测试**

在 `internal/toolchain/detect_test.go` 末尾追加：

```go
// TestDetectWindowsOpencodeAuthIsUnknown 覆盖 Windows 上的 opencode 登录判据。
//
// why：.local/share/opencode/auth.json 是 XDG 落点，Windows 上 opencode 不用它。
// 拿一个在该平台不成立的路径去断言「未登录」是撒谎——如实报「查不了」。
// 反过来 grok / codex 的 ~/.grok、~/.codex 在 Windows 上同样成立，必须仍按文件判定，
// 这条断言是防止把三家一起误伤。
func TestDetectWindowsOpencodeAuthIsUnknown(t *testing.T) {
	home := "/home/u"
	inPath := map[string]bool{"opencode": true, "grok": true, "codex": true}
	files := map[string]bool{
		filepath.Join(home, ".local/share/opencode/auth.json"): true, // 就算这个文件在，Windows 上也不该拿它当判据
		filepath.Join(home, ".grok/auth.json"):                 true,
	}
	withStubs(t, home, inPath, files, "windows")

	rs := Detect()
	if got := byName(t, rs, "opencode").State; got != StateAuthUnknown {
		t.Fatalf("windows 上 opencode 应为 StateAuthUnknown，实为 %v", got)
	}
	if got := byName(t, rs, "grok").State; got != StateReady {
		t.Fatalf("windows 上 grok 凭证文件在，应为 StateReady，实为 %v", got)
	}
	if got := byName(t, rs, "codex").State; got != StateNoCreds {
		t.Fatalf("windows 上 codex 凭证文件不在，应为 StateNoCreds，实为 %v", got)
	}
	// claude 不在 PATH 里（inPath 没给它），仍应是 StateMissing——
	// 「没装」与「查不了」是两件事，本改动不得把它们混起来
	if got := byName(t, rs, "claude").State; got != StateMissing {
		t.Fatalf("windows 上 claude 未安装，应为 StateMissing，实为 %v", got)
	}
}

// TestDetectDarwinOpencodeUnchanged 是回归：非 Windows 平台行为逐字不变。
func TestDetectDarwinOpencodeUnchanged(t *testing.T) {
	home := "/home/u"
	inPath := map[string]bool{"opencode": true}
	files := map[string]bool{filepath.Join(home, ".local/share/opencode/auth.json"): true}
	withStubs(t, home, inPath, files, "darwin")

	if got := byName(t, Detect(), "opencode").State; got != StateReady {
		t.Fatalf("darwin 上凭证文件在，opencode 应为 StateReady，实为 %v", got)
	}
}
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/toolchain/ -run 'TestDetectWindows|TestDetectDarwin' -v`

Expected: **编译失败**——`goos` 未定义、`withStubs` 参数个数不对。这是预期的失败形态（先改测试助手再改生产代码时必然如此），不要因为「没看到断言失败」就以为测试没写对。

- [ ] **Step 4: 改实现**

在 `internal/toolchain/detect.go`：

1. import 加 `"runtime"`。
2. 把平台加成第四个包级探测缝，紧挨着已有三个：

```go
// 四个探测缝，生产实现即标准库/运行时；测试替换它们，从而不依赖跑测机器的真实环境。
var (
	lookPath    = exec.LookPath
	statFile    = func(p string) error { _, err := os.Stat(p); return err }
	userHomeDir = os.UserHomeDir
	goos        = runtime.GOOS
)
```

3. 把 `credRelPath` 的直接查表换成一个按平台过滤的取值函数，放在 `credRelPath` 定义之后：

```go
// credRelPathFor 返回某家 executor 在当前平台的凭证文件相对路径；
// 第二个返回值为 false 表示「本平台没有可靠的文件判据」。
//
// why 要按平台过滤：credRelPath 里 opencode 那条是 XDG 落点，Windows 上 opencode
// 不用它。拿一个在该平台不成立的路径去断言「未登录」，与拿 ~/.claude.json 断言
// claude 已就绪是同一种撒谎——后者正是 claude 被排除在表外的理由。
// 查不到判据时调用方会落到 StateAuthUnknown（「查不了」≠「没登录」）。
//
// grok 与 codex 不在此列：~/.grok、~/.codex 在 Windows 上同样成立。
func credRelPathFor(name string) (string, bool) {
	if goos == "windows" && name == "opencode" {
		return "", false
	}
	rel, ok := credRelPath[name]
	return rel, ok
}
```

4. 把 `Detect()` 里的 `rel, ok := credRelPath[name]` 改成 `rel, ok := credRelPathFor(name)`。**其余一行不动**——下面那条 `if !ok || homeErr != nil { r.State = StateAuthUnknown ... }` 就是我们要落进去的分支。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/toolchain/ -v`
Expected: PASS，包括改过调用点的全部既有用例。

- [ ] **Step 6: 确认没有顺手改坏 FirstReady**

Run: `go test ./internal/toolchain/ -run FirstReady -v`
Expected: PASS 或「no tests to run」。

`FirstReady` 明确不把 `StateAuthUnknown` 算作就绪，因此 Windows 上只装了 opencode 时 `init` 挑不出缺省执行者——**这是如实的结果，不是缺陷，不要去改 `FirstReady` 的语义。**

- [ ] **Step 7: 格式与提交**

```bash
gofmt -l .   # 必须无输出
git add internal/toolchain/detect.go internal/toolchain/detect_test.go
git commit -m "fix(toolchain): Windows 上 opencode 无可靠登录判据时如实报未知（B121）"
```

---

### Task 3: B82(main) — `run` 对已回收工作树给出真因与 400

**Files:**
- Modify: `internal/agentd/admission.go:24` 附近（新增哨兵错误 `ErrWorkdirGone`）
- Modify: `internal/agentd/workspace.go:1572-1600`（`RunCmd` 加存在性判据）
- Modify: `internal/agentd/server.go:1534-1542`（错误分支加映射）
- Create: `internal/agentd/workspace_run_test.go`（本任务的三条用例都放这里；同包内新建测试文件，需写文件头注释说明职责与边界）

**Interfaces:**
- Consumes: 无
- Produces: `var ErrWorkdirGone = errors.New("工作目录不存在")`（`internal/agentd` 包级导出哨兵，供路由层 `errors.Is` 判定）。`RunCmd` 签名不变：`func RunCmd(ctx context.Context, repo, cmdline string) (stdout string, exitCode int, err error)`

**背景（实现者必读）:** `RunCmd` 设了 `cmd.Dir = repo` 就直接 `cmd.Start()`，从不检查 repo 在不在。工作树被 `done`/`stop` 回收后再对该任务发 `run`，内核在 chdir 阶段就失败，错误却长成 `fork/exec /bin/sh: no such file or directory`——指向一个完全无辜的 sh。路由层又把它按 500 返回，而「你要的工作树没了」不是服务端故障。

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/workspace_run_test.go`，文件头写清职责与边界，然后放入两条用例：

```go
// workspace_run_test.go —— RunCmd 与 /api/tasks/<id>/run 路由的工作目录判据测试。
//
// 职责：钉死「工作树已被回收」时的错误类型、状态码与「不启动任何子进程」。
// 边界：不覆盖 run 的超时、进程组回收与输出截断，那些由既有用例负责。
package agentd

// TestRunCmdMissingWorkdirIsTypedAndStartsNothing 覆盖工作树已被回收的场景。
//
// why 这个用例存在：以前 RunCmd 直接 cmd.Start()，chdir 失败被内核报成
// 「fork/exec /bin/sh: no such file or directory」，指向一个完全无辜的 sh，
// 排查时被引到错误方向。
func TestRunCmdMissingWorkdirIsTypedAndStartsNothing(t *testing.T) {
	base := t.TempDir()
	gone := filepath.Join(base, "已被回收的工作树")
	sentinel := filepath.Join(base, "副作用.txt")

	// 命令本身有可观测副作用：若进程真被启动，这个文件就会出现
	_, exitCode, err := RunCmd(context.Background(), gone, "echo x > "+sentinel)

	if !errors.Is(err, ErrWorkdirGone) {
		t.Fatalf("期望 ErrWorkdirGone，实为 %v", err)
	}
	if exitCode != -1 {
		t.Fatalf("启动前失败时 exitCode 应为 -1，实为 %d", exitCode)
	}
	if !strings.Contains(err.Error(), gone) {
		t.Fatalf("错误文案应点名路径 %q，实为 %q", gone, err.Error())
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("工作目录不存在时不得启动任何子进程，但副作用文件出现了")
	}
}

// TestRunCmdExistingWorkdirUnchanged 是回归：目录在时行为不变。
func TestRunCmdExistingWorkdirUnchanged(t *testing.T) {
	repo := t.TempDir()
	stdout, exitCode, err := RunCmd(context.Background(), repo, "echo hello")
	if err != nil {
		t.Fatalf("目录存在时不应报错: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode 应为 0，实为 %d", exitCode)
	}
	if !strings.Contains(stdout, "hello") {
		t.Fatalf("stdout 应含 hello，实为 %q", stdout)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestRunCmdMissingWorkdir -v`
Expected: 编译失败（`ErrWorkdirGone` 未定义）。

- [ ] **Step 3: 加哨兵错误**

在 `internal/agentd/admission.go` 里 `ErrNoProcHeadroom` 声明的紧邻处加：

```go
// ErrWorkdirGone 表示 run 要用的工作目录已不存在。
//
// why 单独一个哨兵：它是调用方的条件（多半是 managed worktree 已被 done/stop 回收），
// 不是服务端故障，路由层据此返回 400 而不是 500。
var ErrWorkdirGone = errors.New("工作目录不存在")
```

- [ ] **Step 4: 在 RunCmd 里加判据**

在 `internal/agentd/workspace.go` 的 `RunCmd` 里，**在 `runShell()` 调用之前**插入：

```go
	// why 判在这里而不是在路由层按任务状态反推：目录缺失的原因不止「任务已归档」
	// 一种——人手删、盘掉了、路径被改都会到这里。按状态反推只覆盖归档那一种，
	// 其余场景会退回误导性报错；stat 是对真实原因的直接判据。
	//
	// why 必须排在 runShell() 之前：否则 Windows 上「找不到 sh」的错误会抢在
	// 「工作树没了」前面报出来，又是一次指错方向。
	if _, statErr := os.Stat(repo); statErr != nil {
		log().Warn("run 被拒：工作目录不存在", "repo", repo,
			"cmd", truncateRunes(cmdline, 200), "cause", statErr)
		return "", -1, fmt.Errorf("%w（managed worktree 可能已被 done/stop 回收）: %s",
			ErrWorkdirGone, repo)
	}
```

日志档位是 `Warn` 不是 `Error`——它是调用方的条件不是服务端故障，与紧邻的「run 被拒：进程余量不足」同档。若 `os` 未 import 则补上。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestRunCmd -v`
Expected: PASS（两个用例都过）

- [ ] **Step 6: 路由层加映射，并写它的测试**

先在 `internal/agentd/server.go` 的 run 处理器里，把 `ErrNoProcHeadroom` 那条分支扩成两条：

```go
		if errors.Is(err, ErrNoProcHeadroom) || errors.Is(err, ErrWorkdirGone) {
			s.log.Warn("run 被拒", "task", taskID, "cmd", truncateRunes(req.Cmd, 200), "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": truncateRunes(err.Error(), 200)})
			return
		}
```

再把这条路由用例追加进 `internal/agentd/workspace_run_test.go`（与 Step 1 的用例同文件）。它复用两个**已存在**的助手，**不要**新造 HTTP 脚手架：`newTestServerWithTask`（`frames_stream_test.go:24`，建的任务其 `RepoPath` 指向 `<DataDir>/tasks/<id>`，该目录在磁盘上并不存在——恰好就是本用例要的「工作树没了」）与 `doWorktreeReq`（`projectadmin_test.go:757`，已带 Host 与 Bearer）。

```go
// TestTaskRunMissingWorkdirReturns400 验证工作树已回收时是 400 而不是 500。
//
// why 这条断言重要：500 会让脚本化调用方把「你要的工作树没了」读成「服务端炸了」，
// 两者的处置完全相反——前者该换任务/重新派发，后者该去查 agentd。
func TestTaskRunMissingWorkdirReturns400(t *testing.T) {
	s, taskID := newTestServerWithTask(t) // 该任务的 RepoPath 指向一个不存在的目录

	rec := doWorktreeReq(t, s, http.MethodPost, "/api/tasks/"+taskID+"/run", `{"cmd":"echo x"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码应为 400，实为 %d，响应体 %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "工作目录不存在") {
		t.Fatalf("响应体应点名真因，实为 %s", rec.Body.String())
	}
}
```

> 若 `newTestServerWithTask` 建的任务恰好落在一个真实存在的目录上（实现时请先跑一遍确认），
> 就在用例里显式把该任务的 `RepoPath` 改成 `filepath.Join(t.TempDir(), "已被回收")` 后再发请求，
> **不要**为了让用例通过去改这两个既有助手——它们被别的用例依赖着。

- [ ] **Step 7: 跑路由测试确认通过**

Run: `go test ./internal/agentd/ -run Run -v`
Expected: PASS

- [ ] **Step 8: 整包回归 + 格式 + 提交**

```bash
go test ./internal/agentd/
gofmt -l .   # 必须无输出
git add internal/agentd/admission.go internal/agentd/workspace.go internal/agentd/server.go internal/agentd/*_test.go
git commit -m "fix(agentd): run 对已回收工作树给出真因并返回 400（B82）"
```

---

### Task 4: B101 — `executor.model` 只对缺省执行者生效

**Files:**
- Modify: `internal/agentd/manager.go:619-623`（抽出模型解析并加条件）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `func (m *Manager) resolveModel(reqModel, execName string) string`（包内私有方法）

**背景（实现者必读）:** `ExecutorConfig` 只有 `Default` 与 `Model` 两个字段，`Model` 语义上是**缺省执行者**的默认模型，但 `Dispatch` 不看解析出来的是谁，一律套上。于是配了 `executor.model: opencode-go/deepseek-v4-flash` 的机器派 codex，第一回合直接吃 `400 ... model is not supported when using Codex with a ChatGPT account`。

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/manager_test.go` 末尾追加：

```go
// TestResolveModelOnlyAppliesToDefaultExecutor 钉死 executor.model 的语义。
//
// why：executor.model 是「缺省执行者的默认模型」，不是全局默认。以前不分执行者
// 一律套上，配了 opencode 模型名的机器派 codex 时第一回合就 400。
func TestResolveModelOnlyAppliesToDefaultExecutor(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": &chanAdapter{}}, "opencode")
	m.cfg.Executor.Model = "cheap/model"

	cases := []struct {
		name     string
		reqModel string
		execName string
		want     string
	}{
		{"缺省执行者且未指定模型：套配置值", "", "opencode", "cheap/model"},
		{"非缺省执行者且未指定模型：留空交给执行者自身默认", "", "codex", ""},
		{"显式指定模型恒优先（缺省执行者）", "x/y", "opencode", "x/y"},
		{"显式指定模型恒优先（非缺省执行者）", "x/y", "codex", "x/y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := m.resolveModel(c.reqModel, c.execName); got != c.want {
				t.Fatalf("resolveModel(%q, %q) = %q，期望 %q", c.reqModel, c.execName, got, c.want)
			}
		})
	}
}
```

> 关于第一个用例的边界：`execName` 由 `resolveExecutor` 产出，它已经把空的 `req.Executor`
> 归一化成 `cfg.Executor.Default`。所以「没写执行者」和「显式写了 `--executor opencode`
> 而它恰好等于 default」走到这里是同一个值 `"opencode"`，**两者都该套上配置模型**。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestResolveModelOnlyAppliesToDefaultExecutor -v`
Expected: 编译失败（`m.resolveModel` 未定义）。

> 若 `Manager` 的配置字段名不是 `m.cfg`，以仓库实际字段名为准调整测试，
> **不要**为了迁就测试去改 `Manager` 的字段。

- [ ] **Step 3: 抽出解析方法**

在 `internal/agentd/manager.go` 里 `resolveExecutor` 的紧邻处加：

```go
// resolveModel 决定任务下发时用哪个模型名。空串表示「不指定，由执行者自身默认接管」。
//
// 优先级：任务级 req.Model > 缺省执行者的配置模型 > 空（执行者自身默认）。
//
// why 要判 execName == Default：cfg.Executor.Model 的语义是**缺省执行者**的默认模型，
// 不是全局默认。以前不分执行者一律套上，于是配了 opencode 模型名的机器派 codex 时
// 第一回合就被 provider 顶回 400。
//
// 边界：显式传 --executor 且它恰好等于 cfg.Executor.Default 时，**照样套配置模型**——
// 语义与调用方有没有把名字显式写出来无关。（execName 来自 resolveExecutor，
// 空的 req.Executor 在那里已被归一化成 Default，故这一个判断覆盖两条路径。）
//
// why 不做按执行者的模型映射表：那需要新配置键，会撞上「配了新键的机器跨版本回滚
// 被严格解析拒启动」的老坑（B88）；而 --model 已经能覆盖非缺省执行者的场景。
func (m *Manager) resolveModel(reqModel, execName string) string {
	if reqModel != "" {
		return reqModel
	}
	if execName == m.cfg.Executor.Default {
		return m.cfg.Executor.Model
	}
	return ""
}
```

- [ ] **Step 4: 在 Dispatch 里改用它**

把 `internal/agentd/manager.go` 里这三行：

```go
	model := req.Model
	if model == "" {
		model = m.cfg.Executor.Model // 配置级兜底；仍空则 executor 自身默认
	}
```

替换成：

```go
	model := m.resolveModel(req.Model, execName)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestResolveModel -v`
Expected: PASS（4 个子用例全过）

- [ ] **Step 6: 整包回归 + 格式 + 提交**

```bash
go test ./internal/agentd/
gofmt -l .   # 必须无输出
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "fix(agentd): executor.model 只对缺省执行者生效（B101）"
```

---

### Task 5: 整批终审与全量门

**Files:** 无新增，只跑门与修门发现的问题

- [ ] **Step 1: 跑全量门**

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./...
```

Expected：前三条无输出（`gofmt -l .` 尤其要**亲眼确认是空的**），`go test ./...` 全 ok。

- [ ] **Step 2: 跑 race 门**

```bash
go test -race ./internal/agentd/ ./internal/toolchain/ ./cmd/
```

Expected: 三包全 ok。`./internal/agentd/` 这一包较慢（约 2 分钟），属正常。

> 若这里翻红且失败用例**不在**本批改动的文件里，先在分支起点上跑同一条命令做对照。
> 对照也红 = 既存偶发缺陷（本仓库已知有几条，见 backlog B140/B32），照实记进 ledger，
> **不要**把它算成本批的失败，也不要顺手去修。

- [ ] **Step 3: 整分支终审**

对相对分支起点的完整 diff 做一次通读，检查：

- 有没有超出本计划范围的改动（本批**不该**出现新配置键、新协议字段、新依赖、公共抽象）
- 四个任务的注释是否都在解释 why 而不是复述代码
- 除 Task 3 外是否**没有**新增日志（Global Constraints 的明确要求）

有发现项就一次性全量修，再做一次范围复审。

- [ ] **Step 4: 落 ledger**

把每个 task 的完成裁决与提交范围追加进 `docs/superpowers/ledgers/ledger-batch1-small-fixes.md`（不存在则创建，文件头写职责与边界）。**只写亲自跑到结果的结论**；跑了但失败的贴原始报错原文。

- [ ] **Step 5: 提交 ledger**

```bash
git add docs/superpowers/ledgers/ledger-batch1-small-fixes.md
git commit -m "chore: 记录批 1 琐碎修 ledger"
```

---

## 真机欠账（实现方不必做，但**不许在 ledger 里写成已验**）

- **B120** 的 Windows 本地路径 origin 自动登记：只有单测证据，未在 Windows 执行机上真跑过
- **B121** 的 Windows 分支：只有 `goos = "windows"` 的单测证据，未在真 Windows 上确认 opencode 的实际凭证落点（本批刻意不去查那个落点）

两条都随批 3（Windows 收尾）上机时补验。**Task 5 的 ledger 里要如实写上这两条未验。**

Task 3 与 Task 4 无真机欠账：两者都与平台无关，单测即为充分判据。

## 不属实现范围的收尾（留给协调者）

Task 4 落地后，全局 `~/.claude/CLAUDE.md` §4 里「mac-02 派 codex 要显式 `--model gpt-5.6-luna`」
那条派发纪律就失效了——配置模型不再污染 codex，本机默认接管。**实现方不要去改它**：
那是用户机器上的个人配置，不在本仓库里。协调者在验收时提醒用户退休这条即可。
