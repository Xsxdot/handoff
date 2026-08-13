# B71 executor 发现不再依赖「恰好正确的 PATH」实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 agentd 与 `handoff init` 用同一套确定的规则找到 executor，不再依赖进程继承的 PATH 或用户 rc 文件是否恰好包含工具目录；并让托管（重启后 agentd 还在）成为安装流程里被主动追问的一步。

**Architecture:** 新包 `internal/pathenv` 是 PATH 解析的唯一真相，四层来源按序追加（进程继承 → 登录 shell → `config.path_dirs` → 内置已知目录表）。agentd 启动时调用它并紧跟一次 executor 探测自检；`handoff init` 调用同一个包（关掉登录 shell 层）让探测口径与 agentd 一致，并在执行机上追问是否代跑 `handoff service install`。`install.sh` 把下一步指向 `handoff init`。

**Tech Stack:** Go 1.26（`go.mod` 声明）、标准库 `log/slog`、`gopkg.in/yaml.v3`（严格解析）、bash（`install.sh`）。

对应 spec：[2026-08-12-executor-path-discovery-design.md](../specs/2026-08-12-executor-path-discovery-design.md)。backlog：**B71**。

## Global Constraints

- **注释一律中文**，新文件必须有文件头注释（职责 + 边界），导出函数必须有 doc comment（参数/返回/注意），非显然分支必须有「为什么」的行内注释。
- **日志一律走 `log/slog`**（`*slog.Logger`），**禁止 `fmt.Printf` / `println` 当日志用**。CLI 面向用户的输出走 `cmd.OutOrStdout()`，那是输出不是日志，两者不可互相顶替。
- **PATH 相关的任何失败都不得阻断启动**：只记 `WARN`，函数不返回 error。理由：PATH 不全只是找不到某些工具，启动失败是整机不可用。
- **追加而非覆盖**：合并 PATH 时既有条目的顺序一律不动（不改 launchd/systemd 显式注入的优先级）。
- **`path_dirs` 的 yaml tag 必须是 `yaml:"path_dirs,omitempty"`**。配置是 `KnownFields(true)` 严格解析的；缺 `omitempty` 会让新版写出的 `config.yaml` 把旧版 agentd 直接卡死在启动阶段。
- **`install.sh` 的既有边界不变**：不写服务单元、不改用户 rc 文件、不 sudo。
- 每个任务结束前跑 `gofmt -l .`（须无输出）、`go vet ./...`、`go build ./...`。
- 提交信息用中文，格式沿用仓库现状（`feat(pkg): …` / `fix(pkg): …` / `docs: …`）。

---

### Task 1: `internal/pathenv` 包 + agentd 接线

把 B7 的 `internal/agentd/loginpath.go` 整体迁进新包，并补上「已知目录表」与「显式目录」两层。迁移与接线必须在同一个任务里完成——`MergeLoginShellPATH` 只有 `cmd/agentd.go` 一个调用方，拆开做中间态编译不过。

本任务先不接 `config.path_dirs`（`ExtraDirs` 传 `nil`），配置字段在 Task 2 加。

**Files:**
- Create: `internal/pathenv/pathenv.go`
- Create: `internal/pathenv/pathenv_test.go`
- Delete: `internal/agentd/loginpath.go`
- Delete: `internal/agentd/loginpath_test.go`
- Modify: `cmd/agentd.go:65-68`（`agentd.MergeLoginShellPATH` → `pathenv.Apply`）

**Interfaces:**
- Consumes: 无（本任务是最底层）
- Produces:
  - `pathenv.Options{IncludeLoginShell bool, ExtraDirs []string}`
  - `pathenv.Apply(ctx context.Context, opt Options, log *slog.Logger) []string` —— 返回本次新增的目录（按加入顺序），无新增时返回 `nil`
  - 包级测试缝 `loginShellPATH`、`dirExists`、`homeDir`、`homeRelDirs`、`absDirs`

- [ ] **Step 1: 写失败的测试**

创建 `internal/pathenv/pathenv_test.go`：

```go
// pathenv 测试：验证四层来源的合并顺序、去重、以及每一层失败时的降级行为。
package pathenv

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSources 把三个测试缝换成可编排的假实现，t.Cleanup 负责还原。
//
// 参数：
//   - home: homeDir 的返回值；空串表示「取不到 HOME」
//   - exist: 视为存在的目录集合（dirExists 只对集合内的路径返回 true）
func stubSources(t *testing.T, home string, exist ...string) {
	t.Helper()
	oldHome, oldExists, oldRel, oldAbs := homeDir, dirExists, homeRelDirs, absDirs
	t.Cleanup(func() { homeDir, dirExists, homeRelDirs, absDirs = oldHome, oldExists, oldRel, oldAbs })

	set := map[string]bool{}
	for _, d := range exist {
		set[d] = true
	}
	dirExists = func(p string) bool { return set[p] }
	homeDir = func() (string, error) {
		if home == "" {
			return "", errors.New("取不到 HOME")
		}
		return home, nil
	}
}

// stubLoginShell 把登录 shell 那一层换成固定返回值。
func stubLoginShell(t *testing.T, out string, err error) {
	t.Helper()
	old := loginShellPATH
	t.Cleanup(func() { loginShellPATH = old })
	loginShellPATH = func(context.Context, string) (string, error) { return out, err }
}

// nopLogger 是丢弃一切的 logger：测试断言的是 PATH 与返回值，不是日志文本。
func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 已知目录表里存在的目录被追加，不存在的不追加，且原有 PATH 顺序不变。
//
// why：这是 B71 的核心——~/.opencode/bin 不在任何 rc 文件里，登录 shell 那一层
// 够不着它，只有已知目录表能兜住。
func TestApplyAppendsExistingKnownDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	home := t.TempDir()
	opencode := filepath.Join(home, ".opencode/bin")
	stubSources(t, home, opencode, "/opt/homebrew/bin")
	homeRelDirs = []string{".opencode/bin", ".grok/bin"}
	absDirs = []string{"/opt/homebrew/bin", "/snap/bin"}

	added := Apply(context.Background(), Options{}, nopLogger())

	got := os.Getenv("PATH")
	if !strings.HasPrefix(got, "/usr/bin:/bin") {
		t.Errorf("原有 PATH 必须保持在前，实得 %q", got)
	}
	for _, want := range []string{opencode, "/opt/homebrew/bin"} {
		if !strings.Contains(got, want) {
			t.Errorf("PATH 应含 %s，实得 %q", want, got)
		}
	}
	if strings.Contains(got, ".grok/bin") || strings.Contains(got, "/snap/bin") {
		t.Errorf("不存在的目录不应追加，实得 %q", got)
	}
	if len(added) != 2 {
		t.Errorf("added 应为 2 个目录，实得 %v", added)
	}
}

// 已在继承 PATH 里的目录不重复追加，也不出现在 added 里。
func TestApplySkipsDirsAlreadyOnPath(t *testing.T) {
	t.Setenv("PATH", "/opt/homebrew/bin:/usr/bin")
	stubSources(t, t.TempDir(), "/opt/homebrew/bin")
	homeRelDirs = nil
	absDirs = []string{"/opt/homebrew/bin"}

	added := Apply(context.Background(), Options{}, nopLogger())

	if len(added) != 0 {
		t.Errorf("已在 PATH 上的目录不该算新增，实得 %v", added)
	}
	if strings.Count(os.Getenv("PATH"), "/opt/homebrew/bin") != 1 {
		t.Errorf("目录被重复追加：%q", os.Getenv("PATH"))
	}
}

// ExtraDirs（config.path_dirs）排在内置已知目录表之前——用户显式声明的优先于内置猜测。
func TestApplyExtraDirsRankBeforeKnownDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	extra := t.TempDir()
	known := t.TempDir()
	stubSources(t, t.TempDir(), extra, known)
	homeRelDirs = nil
	absDirs = []string{known}

	added := Apply(context.Background(), Options{ExtraDirs: []string{extra}}, nopLogger())

	if len(added) != 2 || added[0] != extra || added[1] != known {
		t.Fatalf("added 顺序应为 [extra, known]，实得 %v", added)
	}
	if strings.Index(os.Getenv("PATH"), extra) > strings.Index(os.Getenv("PATH"), known) {
		t.Errorf("path_dirs 应排在已知目录表之前，实得 %q", os.Getenv("PATH"))
	}
}

// ExtraDirs 里不存在的目录被跳过（用户写错路径时要有信号，而不是静默）。
func TestApplySkipsMissingExtraDir(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	stubSources(t, t.TempDir())
	homeRelDirs, absDirs = nil, nil

	added := Apply(context.Background(), Options{ExtraDirs: []string{"/no/such/dir"}}, nopLogger())

	if len(added) != 0 {
		t.Errorf("不存在的 path_dirs 条目不该进 PATH，实得 %v", added)
	}
}

// IncludeLoginShell=false 时绝不执行登录 shell（init 走这条路，省掉最多 3 秒）。
func TestApplySkipsLoginShellWhenDisabled(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	stubSources(t, t.TempDir())
	homeRelDirs, absDirs = nil, nil
	old := loginShellPATH
	t.Cleanup(func() { loginShellPATH = old })
	loginShellPATH = func(context.Context, string) (string, error) {
		t.Fatal("IncludeLoginShell=false 时不该执行登录 shell")
		return "", nil
	}

	Apply(context.Background(), Options{}, nopLogger())
}

// 登录 shell 解析失败时，其余三层照常生效、PATH 不被破坏。
func TestApplyDegradesWhenLoginShellFails(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("SHELL", "/bin/zsh")
	known := t.TempDir()
	stubSources(t, t.TempDir(), known)
	homeRelDirs = nil
	absDirs = []string{known}
	stubLoginShell(t, "", errors.New("shell 不存在"))

	added := Apply(context.Background(), Options{IncludeLoginShell: true}, nopLogger())

	if len(added) != 1 || added[0] != known {
		t.Fatalf("登录 shell 失败不应影响其余层，实得 %v", added)
	}
	if !strings.HasPrefix(os.Getenv("PATH"), "/usr/bin") {
		t.Errorf("PATH 被破坏：%q", os.Getenv("PATH"))
	}
}

// 取不到 HOME 时跳过全部 ~ 系条目，绝对路径条目仍然生效。
//
// why：老 systemd 不为 User= 设置 HOME，那台机器不该因此一个目录都补不上。
func TestApplyWithoutHomeStillAddsAbsoluteDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	stubSources(t, "", "/opt/homebrew/bin")
	homeRelDirs = []string{".opencode/bin"}
	absDirs = []string{"/opt/homebrew/bin"}

	added := Apply(context.Background(), Options{}, nopLogger())

	if len(added) != 1 || added[0] != "/opt/homebrew/bin" {
		t.Fatalf("取不到 HOME 时绝对路径条目仍应生效，实得 %v", added)
	}
}

// 内置表必须覆盖 opencode 官方安装器的落点——B71 的故障现场就是它。
func TestKnownTableCoversOpencodeInstaller(t *testing.T) {
	for _, d := range homeRelDirs {
		if d == ".opencode/bin" {
			return
		}
	}
	t.Fatalf("内置已知目录表必须含 .opencode/bin（B71 故障现场），实得 %v", homeRelDirs)
}

// 默认实现只取 stdout、不以退出码判定成败、stderr 不得混入。
//
// why（B7 原有覆盖，迁移时不能丢）：交互式 shell（-i）在非 TTY 下会打作业控制
// 告警并可能非零退出，但 PATH 已经打出来了；按退出码判失败会让这条修复在真实
// 机器上白做，而把 stderr 并进来会直接把告警文本拼进 PATH。
func TestLoginShellPATHToleratesNonZeroExit(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "fakeshell")
	script := "#!/bin/sh\nprintf %s /opt/x:/opt/y\necho 'warning: no job control' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := loginShellPATH(context.Background(), fake)
	if err != nil {
		t.Fatalf("非零退出但 stdout 有内容时不应报错，实得 %v", err)
	}
	if got != "/opt/x:/opt/y" {
		t.Errorf("PATH = %q，期望 /opt/x:/opt/y（stderr 的告警不得混入）", got)
	}
}
```

顶部 import 里补 `"io"`（`nopLogger` 用）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/pathenv/`
Expected: FAIL —— `no Go files in .../internal/pathenv`（包还不存在）

- [ ] **Step 3: 写实现**

创建 `internal/pathenv/pathenv.go`：

```go
// Package pathenv 把「本进程能看到的 PATH」补成「这台机器上用户实际可用的 PATH」。
//
// 职责：
//   - 从四个来源按序合成 PATH：进程继承 → 登录 shell → 显式配置目录 → 内置已知目录表
//   - 把合成结果写回 os.Setenv("PATH")，并返回本次新增的目录供调用方解释给用户
//
// 为什么需要第三、四层（B71）：B7 的登录 shell 合并只能拿到用户 rc 文件里写了的
// 目录。opencode 官方安装器把二进制放在 ~/.opencode/bin 却不一定改 rc——那台机器
// 的登录 shell 自己都不知道这个目录，agentd 更不可能知道，重启后第一次派发必然
// 报 "opencode: executable file not found in $PATH"。
//
// 边界：
//   - 只补 PATH，不动其他环境变量（补别的收益远小于误伤风险）
//   - 追加而非覆盖：既有条目顺序一律不动，不改 launchd/systemd 显式注入的优先级
//   - 任何一步失败都只记 WARN、绝不返回错误——PATH 不全只是找不到某些工具，
//     而启动失败是整机不可用
//   - 不做 symlink 归一：EvalSymlinks 会在网络盘与权限受限目录上引入新的失败模式，
//     而重复条目对 exec.LookPath 无害
package pathenv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// loginShellTimeout 是登录 shell 解析的时长上限。
//
// 登录 shell 会跑用户的 profile 脚本，个别环境里那些脚本很慢甚至挂住；
// 这是 agentd 的启动路径，不能为了补 PATH 把服务卡在启动中。
const loginShellTimeout = 3 * time.Second

// Options 描述一次解析要启用哪些来源。
type Options struct {
	// IncludeLoginShell 是否执行登录 shell 取 PATH（最多 loginShellTimeout）。
	// agentd 必须开——它常由非登录 shell 或进程管理器拉起；CLI 关，
	// 它本来就跑在用户的登录 shell 里，再跑一次只是白等。
	IncludeLoginShell bool
	// ExtraDirs 是 config.path_dirs：用户显式声明的目录，优先于内置已知目录表。
	ExtraDirs []string
}

// homeRelDirs 是相对 HOME 的已知安装目录。每一条都对应一个真实的安装落点，
// 不是「顺手加上」——加一条前先确认它是哪个工具的官方落点。
var homeRelDirs = []string{
	".opencode/bin",   // opencode 官方安装器（B71 故障现场）
	".grok/bin",       // grok CLI
	".claude/local",   // Claude Code 本地安装（migrate installer 落点）
	".local/bin",      // Claude Code native install / pipx / handoff 自己
	"bin",             // 传统用户 bin
	".bun/bin",        // bun 全局
	".npm-global/bin", // npm 自定义 prefix 的常见落点
	".cargo/bin",      // rust
	"go/bin",          // go
}

// absDirs 是与 HOME 无关的已知安装目录。
//
// 为什么不展开 ~/.nvm/versions/node/*/bin：用 nvm 的机器 rc 里必有 nvm 初始化，
// 登录 shell 那一层已经覆盖，且拿到的是用户当前选中的版本。glob 只能靠字典序
// 猜一个版本，猜错时的症状（工具在、node 版本不对）比找不到更难诊断。
var absDirs = []string{
	"/opt/homebrew/bin",  // Homebrew（Apple Silicon）
	"/opt/homebrew/sbin", //
	"/usr/local/bin",     // Homebrew（Intel）/ 手工安装
	"/usr/local/sbin",    //
	"/snap/bin",          // Linux snap
}

// 三个测试缝，生产实现即标准库；测试替换它们，从而不依赖跑测机器的真实环境。
var (
	// loginShellPATH 执行登录+交互 shell 取其 PATH。
	//
	// 为什么必须同时带 -l 和 -i（2026-08-08 devbox 实测，B7）：-l 只 source
	// .zshenv/.zprofile/.zlogin，而用户的 PATH 追加常写在 .zshrc——那是交互式才
	// 加载的文件。实测该机 .zshrc 第 2 行才是 /usr/local/go/bin 的来源，只用 -l
	// 拿到的 PATH 里根本没有它，这条补全会在它要解决的那台机器上恰好无效。
	//
	// 为什么不看退出码、只看 stdout：交互式 shell 在非 TTY 下会输出作业控制告警
	// 并可能以非零码退出，但 PATH 本身是拿到了的。stderr 必须丢弃——告警文本混进
	// stdout 会直接污染 PATH。
	loginShellPATH = func(ctx context.Context, shell string) (string, error) {
		cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", `printf %s "$PATH"`)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = io.Discard
		runErr := cmd.Run()
		got := strings.TrimSpace(out.String())
		if got == "" {
			if runErr != nil {
				return "", runErr
			}
			return "", errors.New("登录 shell 未输出 PATH")
		}
		return got, nil
	}

	// dirExists 判断路径存在且是目录。
	dirExists = func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	}

	// homeDir 取当前用户主目录。
	//
	// 为什么要有 user.Current 兜底：老版本 systemd 不为 User= 设置 HOME，
	// 那台机器上 os.UserHomeDir 会失败，而 ~ 系条目正是最需要补的那批。
	homeDir = func() (string, error) {
		if h, err := os.UserHomeDir(); err == nil && h != "" {
			return h, nil
		}
		u, err := user.Current()
		if err != nil {
			return "", err
		}
		if u.HomeDir == "" {
			return "", errors.New("当前用户没有主目录")
		}
		return u.HomeDir, nil
	}
)

// Apply 解析 PATH 并写回进程环境。
//
// 参数：
//   - ctx: 上层上下文；登录 shell 那一层内部叠加 loginShellTimeout
//   - opt: 启用哪些来源
//   - log: 日志入口
//
// 返回：
//   - 本次新增的目录（按加入顺序）；无新增或写回失败时为 nil
//
// 注意：
//   - 不返回 error：本函数是 best-effort 增强，任何失败都只记日志
//   - 调用方拿 added 是为了向用户解释「这个工具是靠补全才找到的」（见 cmd/init.go）
//   - agentd 侧必须在**任何 fork 子进程之前**调用，合并结果才能被 executor、
//     审批者 CLI、审阅命令一并继承
func Apply(ctx context.Context, opt Options, log *slog.Logger) []string {
	cur := os.Getenv("PATH")
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(cur) {
		if d != "" {
			seen[d] = true
		}
	}

	merged := cur
	var added, fromLogin, fromExtra, fromKnown []string
	// appendDir 追加一个尚未出现过的目录，同时记进对应的来源桶。
	// 分桶是为了让日志能说清「这个目录是哪一层带来的」——排障时
	// 「靠内置表兜住的」与「本来就在你 rc 里」是完全不同的结论。
	appendDir := func(d string, bucket *[]string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		if merged == "" {
			merged = d
		} else {
			merged += string(os.PathListSeparator) + d
		}
		added = append(added, d)
		*bucket = append(*bucket, d)
	}

	if opt.IncludeLoginShell {
		for _, d := range loginShellDirs(ctx, log) {
			appendDir(d, &fromLogin)
		}
	}
	for _, d := range opt.ExtraDirs {
		if !dirExists(d) {
			// 用户显式写下的目录却不存在，多半是笔误：必须给信号，不能静默
			log.Warn("config.path_dirs 里的目录不存在，已跳过", "dir", d)
			continue
		}
		appendDir(d, &fromExtra)
	}
	for _, d := range knownDirs(log) {
		appendDir(d, &fromKnown)
	}

	if len(added) == 0 {
		log.Info("PATH 无需补全")
		return nil
	}
	if err := os.Setenv("PATH", merged); err != nil {
		log.Warn("写入补全后的 PATH 失败，保持当前 PATH", "cause", err)
		return nil
	}
	log.Info("已补全 PATH",
		"login_shell", fromLogin, "extra_dirs", fromExtra, "known_dirs", fromKnown)
	return added
}

// loginShellDirs 取登录 shell 的 PATH 并拆成目录列表；失败返回 nil。
func loginShellDirs(ctx context.Context, log *slog.Logger) []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		log.Warn("未设置 $SHELL，跳过登录 shell 的 PATH 解析")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, loginShellTimeout)
	defer cancel()
	got, err := loginShellPATH(ctx, shell)
	if err != nil {
		log.Warn("登录 shell 解析 PATH 失败，跳过该来源", "shell", shell, "cause", err)
		return nil
	}
	return filepath.SplitList(got)
}

// knownDirs 返回内置表里**确实存在**的目录。
//
// 只返回存在的：把一堆不存在的目录塞进 PATH 会让每次 LookPath 多做一轮无用 stat，
// 也让日志里的「已补全」失去意义。
func knownDirs(log *slog.Logger) []string {
	var out []string
	home, err := homeDir()
	if err != nil {
		// 不致命：绝对路径那批（Homebrew / snap）仍然可用
		log.Warn("取不到主目录，跳过全部 ~ 系已知目录", "cause", err)
	} else {
		for _, rel := range homeRelDirs {
			if p := filepath.Join(home, rel); dirExists(p) {
				out = append(out, p)
			}
		}
	}
	for _, p := range absDirs {
		if dirExists(p) {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/pathenv/ -v`
Expected: PASS（9 个用例全绿）

- [ ] **Step 5: 删掉 B7 的旧实现并接线 agentd**

删除 `internal/agentd/loginpath.go` 与 `internal/agentd/loginpath_test.go`（覆盖已迁到 pathenv，两条 B7 断言在新测试里都有对应用例）。

`cmd/agentd.go` 顶部 import 加 `"github.com/xushixin/handoff/internal/pathenv"`，把第 65-68 行换成：

```go
			// PATH 补全（B7 + B71）：agentd 常由非登录 shell 或进程管理器拉起，
			// 拿到的 PATH 可能只有 /usr/bin:/bin:/usr/sbin:/sbin。必须早于任何
			// fork 子进程的动作，合并结果才能被 executor/审批者/审阅命令继承。
			// ExtraDirs 在 Task 2 接上 cfg.PathDirs
			pathenv.Apply(context.Background(), pathenv.Options{IncludeLoginShell: true}, logger)
```

同步改 `cmd/agentd.go` 文件头注释第 6 行：`agentd.MergeLoginShellPATH（PATH 补全，先于一切 fork 子进程）` → `pathenv.Apply（PATH 补全，先于一切 fork 子进程）`。

- [ ] **Step 6: 日志与注释自检**

逐条对照，缺一条就补一条（不是「代码里大概有」，是逐条看）：

日志（全部走 `*slog.Logger`，本包**没有**任何 `fmt.Printf`）：
- `$SHELL` 未设置 → `WARN 未设置 $SHELL，跳过登录 shell 的 PATH 解析`
- 登录 shell 解析失败 → `WARN` 带 `shell` 与 `cause`
- `path_dirs` 条目不存在 → `WARN` 带 `dir`
- 取不到 HOME → `WARN` 带 `cause`
- `os.Setenv` 失败 → `WARN` 带 `cause`
- **成功路径也要出声**：有新增 → `INFO 已补全 PATH`，且按 `login_shell`/`extra_dirs`/`known_dirs` 三个桶分开；无新增 → `INFO PATH 无需补全`。静默的成功路径正是「重启后不知道补没补上」的成因

注释：
- 文件头：职责 + 边界（含「为什么需要第三、四层」与「不做 symlink 归一」）
- `Options`、`Apply` 两个导出符号有 doc comment（参数/返回/注意）
- `homeRelDirs`/`absDirs` 表上有「不展开 nvm glob」的理由
- `loginShellPATH` 上保留 B7 的两段「为什么必须 -l -i」「为什么不看退出码」
- `appendDir` 上有「为什么要分桶」的理由

- [ ] **Step 7: 全量自检**

Run: `gofmt -l . && go vet ./... && go build ./... && go test ./...`
Expected: `gofmt -l .` 无输出；其余全绿（26 个包）

- [ ] **Step 8: 提交**

```bash
git add internal/pathenv cmd/agentd.go
git add -u internal/agentd
git commit -m "feat(pathenv): PATH 解析独立成包，补上已知安装目录兜底

B7 的登录 shell 合并只能拿到 rc 文件里写了的目录，opencode 装在
~/.opencode/bin 而该目录不在任何 rc 文件里时够不着。新增内置已知目录表
（存在才追加）与 ExtraDirs 两层，loginpath.go 整体迁入本包。"
```

---

### Task 2: `config.path_dirs` 配置项

**Files:**
- Modify: `internal/config/config.go`（`Config` 加字段、`decodeStrict` 的已知键清单）
- Modify: `cmd/agentd.go`（`ExtraDirs: cfg.PathDirs`）
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: Task 1 的 `pathenv.Options.ExtraDirs`
- Produces: `config.Config.PathDirs []string`（yaml 键 `path_dirs`）

- [ ] **Step 1: 写失败的测试**

追加到 `internal/config/config_test.go`：

```go
// path_dirs 能被读进来，且空值绝不落盘。
//
// why 空值不能落盘（硬要求）：配置是 KnownFields(true) 严格解析的。没配过这一项的
// 机器上一旦被写进 path_dirs: []，一台还没换版的旧 agentd 读到这个未知键会**直接
// 启动失败**——B59 spec D7 那个坑的反方向同款。
func TestPathDirsRoundTripAndOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")

	cfg, err := Load(p) // 首次运行：生成默认配置并写盘
	if err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回配置: %v", err)
	}
	if strings.Contains(string(b), "path_dirs") {
		t.Errorf("未配置时 path_dirs 不得落盘，实得:\n%s", b)
	}

	cfg.PathDirs = []string{"/opt/tools/bin"}
	if err := Save(p, cfg); err != nil {
		t.Fatalf("写盘: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if len(got.PathDirs) != 1 || got.PathDirs[0] != "/opt/tools/bin" {
		t.Errorf("path_dirs = %v，期望 [/opt/tools/bin]", got.PathDirs)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestPathDirsRoundTripAndOmitEmpty -v`
Expected: FAIL with `cfg.PathDirs undefined (type *Config has no field or method PathDirs)`

- [ ] **Step 3: 写实现**

`internal/config/config.go` 的 `Config` 里，紧跟 `RepoRoot` 之后加：

```go
	// PathDirs 是本机额外的可执行文件搜索目录：agentd 启动时按序追加到 PATH 末尾
	// （见 internal/pathenv）。内置已知目录表没覆盖到的安装位置写在这里。
	//
	// 为什么放顶层而不是放进 Executor：它描述的是「**这台机器**上工具装在哪」，
	// 不是执行者的属性——与 RepoRoot 同一个道理。
	//
	// omitempty 是硬要求，不是风格：配置以 KnownFields(true) 严格解析，未知键让
	// agentd **启动失败**。没有 omitempty 时，新版 Save 会把 path_dirs: [] 写进
	// 每一台机器的 config.yaml，而一台还没换版的旧 agentd 读到它就再也起不来了
	//（B59 spec D7 同款，方向相反）。
	PathDirs []string `yaml:"path_dirs,omitempty"`
```

`decodeStrict` 的错误文案里，已知键清单从 `…/stalltimeout/targets{…}` 改成含 `path_dirs`：把 `listen/token/datadir/repo_root/stalltimeout/` 改为 `listen/token/datadir/repo_root/path_dirs/stalltimeout/`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: 接线 agentd**

`cmd/agentd.go` 的 `pathenv.Apply` 调用补上 `ExtraDirs`，并删掉 Task 1 留的那句 TODO 式注释：

```go
			pathenv.Apply(context.Background(),
				pathenv.Options{IncludeLoginShell: true, ExtraDirs: cfg.PathDirs}, logger)
```

- [ ] **Step 6: 日志与注释自检**

- 本任务不新增运行期分支，**不需要新日志**；`path_dirs` 条目不存在的 WARN 已在 Task 1 的 `Apply` 里
- 注释：`PathDirs` 字段上必须写清三件事——它是什么（本机额外搜索目录）、为什么放顶层（与 `RepoRoot` 同理）、`omitempty` 为什么是硬要求（旧 agentd 会因未知键起不来）。第三条尤其不能省：删掉 `omitempty` 的人看不到后果，只会觉得它多余
- `decodeStrict` 的已知键清单是给用户看的错误文案，改完自己念一遍确认 `path_dirs` 在里面

- [ ] **Step 7: 全量自检**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: 全绿

- [ ] **Step 8: 提交**

```bash
git add internal/config cmd/agentd.go
git commit -m "feat(config): path_dirs 顶层配置项，agentd 启动时并进 PATH

omitempty 是硬要求：空值落盘会让未换版的旧 agentd 因 KnownFields 严格
解析而起不来。"
```

---

### Task 3: agentd 启动 executor 探测自检

**Files:**
- Modify: `cmd/agentd.go`（新增 `logExecutorDetection`，在 `pathenv.Apply` 之后调用）
- Test: `cmd/agentd_test.go`

**Interfaces:**
- Consumes: `toolchain.Detect() []toolchain.Result`（已存在）、`cfg.Executor.Default`
- Produces: `logExecutorDetection(log *slog.Logger, defaultExecutor string, rs []toolchain.Result)`

- [ ] **Step 1: 写失败的测试**

追加到 `cmd/agentd_test.go`（顶部 import 补 `"bytes"`、`"strings"`、`"github.com/xushixin/handoff/internal/toolchain"`）：

```go
// 缺省执行者没找到时必须 WARN，且处置里要指出 path_dirs 这条出路。
//
// why：B71 之前，「opencode 没装」这件事要等到第一次派发才暴露，报错落在
// 任务事件流里，离「重启后 agentd 起来了」这个时间点最远。启动时报出来，
// 重启完看一眼日志就知道。
func TestLogExecutorDetectionWarnsOnMissingDefault(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateMissing},
		{Name: "claude", State: toolchain.StateAuthUnknown, Path: "/usr/local/bin/claude"},
	}

	logExecutorDetection(log, "opencode", rs)

	out := buf.String()
	if !strings.Contains(out, "executor 探测") {
		t.Errorf("四家探测结果必须成表进启动日志，实得:\n%s", out)
	}
	if !strings.Contains(out, "/usr/local/bin/claude") {
		t.Errorf("探测到的绝对路径必须进日志（排障要靠它），实得:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("缺省执行者缺失必须 WARN，实得:\n%s", out)
	}
	if !strings.Contains(out, "path_dirs") {
		t.Errorf("WARN 必须给出处置（path_dirs），实得:\n%s", out)
	}
}

// 缺省执行者在位时不 WARN——每次启动打一条无从处置的告警，只会让人学会忽略日志。
func TestLogExecutorDetectionQuietWhenDefaultPresent(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rs := []toolchain.Result{{Name: "opencode", State: toolchain.StateReady, Path: "/x/opencode"}}

	logExecutorDetection(log, "opencode", rs)

	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("缺省执行者就绪时不该 WARN，实得:\n%s", buf.String())
	}
}

// 缺省是 fake 时不 WARN：fake 是脚本演示执行者，本来就没有对应的二进制。
func TestLogExecutorDetectionQuietForFake(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rs := []toolchain.Result{{Name: "opencode", State: toolchain.StateMissing}}

	logExecutorDetection(log, "fake", rs)

	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("缺省是 fake 时不该 WARN，实得:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestLogExecutorDetection -v`
Expected: FAIL with `undefined: logExecutorDetection`

- [ ] **Step 3: 写实现**

`cmd/agentd.go` 里，在 `newAgentdHTTPServer` 之前加：

```go
// logExecutorDetection 把四家 executor 的探测结果成表写进启动日志，并对
// 「缺省执行者没找到」打一条带处置的 WARN。
//
// 参数：
//   - log: 日志入口
//   - defaultExecutor: cfg.Executor.Default
//   - rs: toolchain.Detect() 的结果
//
// 注意：
//   - **不阻断启动**：一台机器不该因为少装一个 executor 就彻底起不来；托管形态下
//     启动失败还会变成崩溃循环。codex 那条硬预检拦的是更窄的判据，两者不冲突
//   - defaultExecutor 是 fake 时不会命中任何一条（fake 不在 Detect 的四家里），
//     于是自然不告警——它是脚本演示执行者，本来就没有对应的二进制
func logExecutorDetection(log *slog.Logger, defaultExecutor string, rs []toolchain.Result) {
	attrs := make([]any, 0, len(rs)*2)
	for _, r := range rs {
		v := r.State.String()
		if r.Path != "" {
			// 路径是排障时唯一有用的信息：它直接回答「补全到底有没有生效」
			v += "  " + r.Path
		}
		attrs = append(attrs, r.Name, v)
	}
	log.Info("executor 探测", attrs...)

	for _, r := range rs {
		if r.Name != defaultExecutor {
			continue
		}
		if r.State == toolchain.StateMissing {
			log.Warn("缺省执行者未找到，派发到本机的任务会失败",
				"executor", r.Name,
				"处置", "在本机装上它，或把它所在目录写进 config.yaml 的 path_dirs")
		}
		return
	}
}
```

import 补 `"github.com/xushixin/handoff/internal/toolchain"`。

在 `RunE` 里 `pathenv.Apply(...)` 之后、`agentd.WarnIfKillModeUnsafe(logger)` 之前插入：

```go
			// 启动自检（B71）：补全之后立刻报一次四家的解析结果。不报的话，
			// 「opencode 没找到」要等到第一次派发才暴露，那时离根因（PATH）已经很远
			logExecutorDetection(logger, cfg.Executor.Default, toolchain.Detect())
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestLogExecutorDetection -v`
Expected: PASS（3 个用例）

- [ ] **Step 5: 日志与注释自检**

日志（本任务的产出**就是**日志，逐条核对）：
- `INFO executor 探测`：四家都在，探到的带绝对路径——路径是排障时唯一有用的信息，它直接回答「补全到底有没有生效」
- `WARN 缺省执行者未找到`：带 `executor` 与 `处置`，处置必须给出**两条**可执行的出路（装它 / 写 `path_dirs`），只说「没找到」等于没说
- 缺省在位时**不打** WARN：每次启动打一条无从处置的告警，只会让人学会忽略日志

注释：
- `logExecutorDetection` 的 doc comment 写清参数、以及两条「注意」：为什么不阻断启动、为什么 `fake` 不会触发告警
- 调用点有一句「为什么要在启动时报」的行内注释（不报的话要等第一次派发才暴露，那时离根因已经很远）

- [ ] **Step 6: 全量自检**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: 全绿

- [ ] **Step 7: 提交**

```bash
git add cmd/agentd.go cmd/agentd_test.go
git commit -m "feat(agentd): 启动时成表报 executor 探测，缺省缺失打 WARN 不阻断"
```

---

### Task 4: `handoff init` 探测口径统一 + 「是补出来的」说明

**Files:**
- Modify: `cmd/init.go`（`RunE` 里先 `pathenv.Apply`；`printDetection` 增参；新增 `coveredBy`）
- Test: `cmd/init_test.go`

**Interfaces:**
- Consumes: `pathenv.Apply` / `pathenv.Options`（Task 1）、`cfg.PathDirs`（Task 2）
- Produces: `printDetection(w io.Writer, rs []toolchain.Result, addedDirs []string)`、`coveredBy(path string, added []string) string`

- [ ] **Step 1: 写失败的测试**

追加到 `cmd/init_test.go`：

```go
// 工具落在「本次补全新增的目录」里时，探测表要说清楚这件事。
//
// why：这是整个 B71 里用户唯一能直接看到的价值——它同时回答了「为什么我 shell 里
// which opencode 找不到、handoff 却说就绪」和「重启之后还灵不灵」。
func TestPrintDetectionExplainsAugmentedDir(t *testing.T) {
	var buf bytes.Buffer
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateReady, Path: "/home/u/.opencode/bin/opencode"},
	}

	printDetection(&buf, rs, []string{"/home/u/.opencode/bin"})

	out := buf.String()
	if !strings.Contains(out, "/home/u/.opencode/bin") {
		t.Errorf("应点名那个补出来的目录，实得:\n%s", out)
	}
	if !strings.Contains(out, "不在你的 PATH 里") {
		t.Errorf("应说明该目录不在用户 PATH 里，实得:\n%s", out)
	}
	if !strings.Contains(out, "自动补上") {
		t.Errorf("应说明 agentd 启动时会自动补上，实得:\n%s", out)
	}
}

// 工具本来就在用户 PATH 上时不加这句——不是每一行都要挂个说明。
func TestPrintDetectionQuietWhenDirAlreadyOnPath(t *testing.T) {
	var buf bytes.Buffer
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateReady, Path: "/usr/local/bin/opencode"},
	}

	printDetection(&buf, rs, []string{"/home/u/.opencode/bin"})

	if strings.Contains(buf.String(), "不在你的 PATH 里") {
		t.Errorf("目录本来就在 PATH 上时不该加说明，实得:\n%s", buf.String())
	}
}

// 没探到的工具（Path 为空）不该匹配上任何补全目录。
func TestPrintDetectionQuietForMissingTool(t *testing.T) {
	var buf bytes.Buffer
	rs := []toolchain.Result{{Name: "grok", State: toolchain.StateMissing}}

	printDetection(&buf, rs, []string{"/home/u/.grok/bin"})

	if strings.Contains(buf.String(), "不在你的 PATH 里") {
		t.Errorf("没装的工具不该带补全说明，实得:\n%s", buf.String())
	}
}
```

顶部 import 补 `"bytes"`、`"github.com/xushixin/handoff/internal/toolchain"`（`strings` 已有）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestPrintDetection -v`
Expected: FAIL with `too many arguments in call to printDetection`

- [ ] **Step 3: 写实现**

`cmd/init.go` 的 `RunE` 里，把 `results := toolchain.Detect()` 那两行换成：

```go
		// PATH 补全（B71）：探测前先按 agentd 的同一套规则补全，否则 init 说
		// 「就绪」而 agentd 说「未安装」是可能的——两边的 PATH 来源本就不同。
		// 关掉登录 shell 那一层：init 本来就跑在用户的登录 shell 里，再跑一次
		// 只是白等最多 3 秒。
		//
		// 用一个只放行 WARN 的 logger：补全成功是常态，把 INFO 打进交互向导的
		// 输出里只会挤掉用户真正要读的探测表；真出问题（$SHELL 没设、path_dirs
		// 目录不存在）仍然要让用户看见。
		quiet := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelWarn}))
		added := pathenv.Apply(cmd.Context(), pathenv.Options{ExtraDirs: cfg.PathDirs}, quiet)

		results := toolchain.Detect()
		printDetection(out, results, added)
```

import 补 `"log/slog"` 与 `"github.com/xushixin/handoff/internal/pathenv"`。

`printDetection` 改成：

```go
// printDetection 打印四家 executor 的探测表。
//
// 参数：
//   - w: 输出目标
//   - rs: 探测结果
//   - addedDirs: 本次 PATH 补全新增的目录（pathenv.Apply 的返回值）
//
// 注意：
//   - 工具的所在目录若来自 addedDirs，要在该行下面说明清楚——用户在自己 shell 里
//     `which` 不到它，不解释的话这张表看起来就是错的
func printDetection(w io.Writer, rs []toolchain.Result, addedDirs []string) {
	fmt.Fprintln(w, "本机 executor 探测：")
	for _, r := range rs {
		path := r.Path
		if path == "" {
			path = "—"
		}
		fmt.Fprintf(w, "  %-9s %-20s %s\n", r.Name, r.State.String(), path)
		if d := coveredBy(r.Path, addedDirs); d != "" {
			fmt.Fprintf(w, "            ↳ %s 不在你的 PATH 里，agentd 启动时会自动补上。\n", d)
		}
	}
	// …以下 claude / codex 两段提示原样保留…
}

// coveredBy 返回 path 所在目录——当且仅当那个目录是本次 PATH 补全新增的；
// 否则返回空串。
//
// 为什么按目录精确相等而不是前缀匹配：前缀匹配会把 /opt/homebrew/bin/x/y 这类
// 更深层的路径也算进来，那不是同一个目录，说明会是错的。
func coveredBy(path string, added []string) string {
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	for _, d := range added {
		if d == dir {
			return d
		}
	}
	return ""
}
```

import 补 `"path/filepath"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestPrintDetection|TestInit' -v`
Expected: PASS（新 3 条 + 原有 init 用例全绿）

- [ ] **Step 5: 日志与注释自检**

日志：
- init 是交互向导，**面向用户的输出走 `cmd.OutOrStdout()`，不是日志**——两者不可互相顶替
- 补全过程的日志走那个只放行 WARN 的 `quiet` logger：成功是常态不打扰用户，`$SHELL` 没设 / `path_dirs` 目录不存在这类真问题仍然要冒出来
- 不要为了「有日志」把 INFO 也放出来：那会挤掉用户真正要读的探测表

注释：
- `printDetection` 的 doc comment 增写 `addedDirs` 参数与那条「注意」（不解释的话这张表在用户眼里就是错的）
- `coveredBy` 的 doc comment 写清返回语义，并写明**为什么按目录精确相等而不是前缀匹配**
- `RunE` 里的补全调用点有三段行内理由：为什么探测前要补全（口径一致）、为什么关掉登录 shell 层（省 3 秒）、为什么用 quiet logger

- [ ] **Step 6: 全量自检**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: 全绿

- [ ] **Step 7: 提交**

```bash
git add cmd/init.go cmd/init_test.go
git commit -m "feat(init): 探测前按 agentd 同一套规则补全 PATH，并说明补出来的目录"
```

---

### Task 5: `handoff init` 追问托管并代跑 `service install`

**Files:**
- Modify: `cmd/service.go`（抽出 `installService`，`serviceInstallCmd.RunE` 改为调它）
- Modify: `cmd/init.go`（`askAll` 返回 `isExec`；新增 `maybeInstallService`；替换末尾那行提示；改文件头注释）
- Test: `cmd/init_test.go`

**Interfaces:**
- Consumes: `newServiceManager`（`cmd/service.go` 已有的测试缝）、`askBool`（`cmd/init.go` 已有）
- Produces:
  - `installService(out io.Writer, cfgPath string) error`
  - `askAll(w io.Writer, r *bufio.Reader, cfg *config.Config, rs []toolchain.Result) (isExec bool, err error)`
  - `maybeInstallService(w io.Writer, r *bufio.Reader, isExec bool, cfgPath string)`

- [ ] **Step 1: 写失败的测试**

追加到 `cmd/init_test.go`（复用 `cmd/service_test.go` 里已有的 `fakeManager` 与 `withFakeManager`，同包可直接用）：

```go
// execAnswers 是「角色=执行机」那条问答路径的答案脚本，末尾一项是托管追问。
//
// 顺序对应 askAll 的提问顺序：角色 / 缺省执行者 / 模型 / 监听 / repo_root /
// 审批链执行者 / update.auto / 托管追问。空行=取默认值。
func execAnswers(installAnswer string) string {
	return strings.Join([]string{
		"1", // 角色：执行机
		"",  // 缺省执行者：取默认
		"",  // 模型
		"",  // 监听地址
		"",  // repo_root
		"",  // 审批链执行者（空=不启用，后续不再追问模型）
		"",  // update.auto
		installAnswer,
	}, "\n") + "\n"
}

// 执行机上答 y：init 必须真的把 agentd 托管起来（而不是只打一行提示）。
//
// why：托管是「机器重启后 agentd 还回得来」的唯一保障。B71 现场那台就是因为
// 这一步只是最后一行提示，从没被执行过，重启后 PATH 全靠运气。
func TestInitInstallsServiceWhenAccepted(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	p := filepath.Join(t.TempDir(), "config.yaml")

	out, err := runInit(t, p, true, execAnswers("y"))
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if f.installed == nil {
		t.Fatalf("答 y 必须真的调 Install，实得输出:\n%s", out)
	}
}

// 答 n：不装，但要留下可直接复制的命令与「不托管的后果」。
func TestInitSkipsServiceWhenDeclined(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	p := filepath.Join(t.TempDir(), "config.yaml")

	out, err := runInit(t, p, true, execAnswers("n"))
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if f.installed != nil {
		t.Error("答 n 时不该调 Install")
	}
	if !strings.Contains(out, "handoff service install") {
		t.Errorf("答 n 时要留下可复制的命令，实得:\n%s", out)
	}
	if !strings.Contains(out, "重启") {
		t.Errorf("要说清不托管的后果（重启后不会自己回来），实得:\n%s", out)
	}
}

// 托管失败不能让 init 失败：配置已经写盘了，为一个附属动作退非零，
// 用户会以为配置没保存。
func TestInitSurvivesServiceInstallFailure(t *testing.T) {
	f := &fakeManager{installErr: errors.New("launchctl 挂了")}
	withFakeManager(t, f)
	p := filepath.Join(t.TempDir(), "config.yaml")

	out, err := runInit(t, p, true, execAnswers("y"))
	if err != nil {
		t.Fatalf("托管失败不应让 init 报错，实得: %v", err)
	}
	if !strings.Contains(out, "launchctl 挂了") {
		t.Errorf("失败真因必须回显，实得:\n%s", out)
	}
	if _, statErr := os.Stat(p); statErr != nil {
		t.Errorf("配置必须已经写盘: %v", statErr)
	}
}

// 审核者机不追问托管——那台机器上根本不跑 agentd。
func TestInitDoesNotAskServiceForReviewer(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	p := filepath.Join(t.TempDir(), "config.yaml")

	answers := strings.Join([]string{"2", "", "", "", ""}, "\n") + "\n" // 角色=审核者机
	out, err := runInit(t, p, true, answers)
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if f.installed != nil {
		t.Errorf("审核者机不该装服务，实得输出:\n%s", out)
	}
}
```

顶部 import 补 `"errors"`（`os`、`strings`、`filepath` 已有）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestInit -v`
Expected: FAIL —— `TestInitInstallsServiceWhenAccepted` 报 `答 y 必须真的调 Install`（当前 init 只打一行提示）

- [ ] **Step 3: 抽出 `installService`**

`cmd/service.go` 新增，并把 `serviceInstallCmd.RunE` 改成一行调用：

```go
// installService 安装并启动服务单元，把结果打给用户。
//
// 参数：
//   - out: 面向用户的输出（不是日志）
//   - cfgPath: 传给 agentd 的配置路径
//
// 返回：
//   - 错误：构造管理器、解析 Spec、安装任一步失败
//
// 注意：
//   - 抽成函数是为了让 handoff init 能走**同一条**代码路径追问并代跑（B71）。
//     init 复制一份逻辑的话，两处的托管行为会各自演化
func installService(out io.Writer, cfgPath string) error {
	log := slog.Default()
	m, err := newServiceManager(log)
	if err != nil {
		return err
	}
	spec, err := resolveSpec(cfgPath)
	if err != nil {
		return err
	}
	if err := m.Install(spec); err != nil {
		return fmt.Errorf("安装服务失败: %w", err)
	}
	unit, _ := m.UnitPath()
	fmt.Fprintf(out, "已托管   %s\n", m.Kind())
	fmt.Fprintf(out, "单元     %s\n", unit)
	fmt.Fprintf(out, "二进制   %s\n", spec.BinPath)
	fmt.Fprintf(out, "配置     %s\n", spec.ConfigPath)
	fmt.Fprintf(out, "日志     %s\n", spec.LogPath)
	// 形态变化必须说清楚：托管之后手动 Ctrl-C 会被拉回来，这是最容易
	// 让人以为「服务停不掉」的一点
	fmt.Fprintf(out, "\n注意     agentd 现在由 %s 托管，崩溃或退出都会被自动拉起。\n", m.Kind())
	fmt.Fprintf(out, "         想真正停掉它请用 handoff service uninstall，Ctrl-C 只会让它被重新拉起。\n")
	return nil
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装并启动服务单元",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return installService(cmd.OutOrStdout(), effectiveConfigPath())
	},
}
```

import 补 `"io"`。

- [ ] **Step 4: 改 `init` 的问答收口**

`cmd/init.go`：

1. `askAll` 签名改为返回 `(bool, error)`：函数体开头 `isExec := role == roleExecutor || role == roleBoth` 保持不变，末尾 `return nil` 改成 `return isExec, nil`，中途 `return err` 处改成 `return false, err`（若有）。
2. `RunE` 里调用处改为：

```go
		isExec, err := askAll(out, r, cfg, results)
		if err != nil {
			return err
		}
		if err := config.Save(p, cfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "\n已写入 %s\n", p)
		printPairing(out, cfg)
		maybeInstallService(out, r, isExec, p)
		return nil
```

（删掉原来那行 `fmt.Fprintln(out, "\n下一步   handoff service install …")`——它被 `maybeInstallService` 接管。）

3. 新增：

```go
// maybeInstallService 在执行机上追问是否现在把 agentd 交给进程管理器托管，
// 答 y 则就地代跑。
//
// 参数：
//   - w: 面向用户的输出
//   - r: 问答输入
//   - isExec: 本机角色是否包含执行机
//   - cfgPath: 配置路径（传给服务单元）
//
// 注意：
//   - 无返回值：托管失败**绝不**让 init 失败。配置此时已经写盘，为一个附属动作
//     把整条 init 退非零，用户会以为配置没保存（与 install.sh 对 skill install
//     的处置同一个道理）
//   - Linux 上非 root 时不代跑：systemd 单元要写 /etc/systemd/system，需要 root，
//     而 init 不 sudo。此时只打印命令
//   - why 要追问而不是只提示：托管是「机器重启后 agentd 还回得来」的唯一保障，
//     它此前只是最后一行提示——B71 现场那台就是这么变成手工拉起的，重启后
//     PATH 全靠运气
func maybeInstallService(w io.Writer, r *bufio.Reader, isExec bool, cfgPath string) {
	if !isExec {
		// 审核者机不跑 agentd，托管对它没有意义
		return
	}
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		fmt.Fprintln(w, "\n下一步   sudo handoff service install")
		fmt.Fprintln(w, "         systemd 单元要写 /etc/systemd/system，需要 root，init 不替你 sudo。")
		fmt.Fprintln(w, "         没有托管的 agentd 在机器重启后不会自己回来。")
		return
	}
	if !askBool(w, r, "\n现在把 agentd 交给本机进程管理器托管", true) {
		fmt.Fprintln(w, "\n下一步   handoff service install")
		fmt.Fprintln(w, "         没有托管的 agentd 在机器重启后不会自己回来。")
		return
	}
	fmt.Fprintln(w)
	if err := installService(w, cfgPath); err != nil {
		fmt.Fprintf(w, "托管失败：%v\n", err)
		fmt.Fprintln(w, "配置已经写好了，稍后单独重跑 handoff service install 即可。")
	}
}
```

import 补 `"runtime"`。

4. 文件头注释第 10-11 行的边界说明必须同步改写（代码与注释对不上比没注释更糟）：

```go
//   - **不主动装服务，但会问**：角色含执行机且 stdin 是终端时，init 会追问一句
//     是否托管，答 y 则调 installService（与 handoff service install 同一条路径）。
//     托管是「重启后 agentd 还回得来」的唯一保障，只留一行提示的触达率不够（B71）。
//     Linux 上非 root 时一律不代跑，只打印 sudo 命令
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestInit|TestService' -v`
Expected: PASS（新 4 条 + 原有 init/service 用例全绿）

若 `execAnswers` 的答案条数与实际提问顺序对不上，以 `askAll` 的实际提问顺序为准调整脚本——`ask` 在 stdin 提前结束时按「全部取默认」处理，所以多一行无害、少一行会让托管追问吃到 EOF 而取默认值 y。

- [ ] **Step 6: 日志与注释自检**

日志：
- 托管的成功输出（已托管/单元/二进制/配置/日志五行 + 两句形态提醒）是**给用户的输出**，走 `w`，不是日志——`installService` 从 `service` 子命令搬过来时这一点不能变味
- 托管失败：真因必须原样回显给用户（`fmt.Fprintf(w, "托管失败：%v\n", err)`），并紧跟一句「配置已经写好了」——只说失败会让用户以为整条 init 白跑了
- `installService` 内部的管理器日志（launchd/systemd 的安装、回滚、复核）由 `internal/service` 自己打，本任务不重复打

注释：
- `installService` 的 doc comment 写清参数/返回，以及**为什么要抽出来**（init 复制一份的话两处托管行为会各自演化）
- `maybeInstallService` 的 doc comment 写全三条「注意」：托管失败绝不让 init 失败、Linux 非 root 不代跑、为什么要追问而不是只提示
- `cmd/init.go` 的**文件头边界注释必须同步改写**——原文写着「不装服务」，改完不改注释就是代码与注释直接矛盾

- [ ] **Step 7: 全量自检**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: 全绿

- [ ] **Step 8: 提交**

```bash
git add cmd/init.go cmd/init_test.go cmd/service.go
git commit -m "feat(init): 执行机上追问并代跑 service install，与 service 子命令同路径

托管是重启后 agentd 还回得来的唯一保障，此前只是最后一行提示。"
```

---

### Task 6: `install.sh` 把下一步指清楚

**Files:**
- Modify: `install.sh`（新增 `print_next_steps`，`main` 末尾调用）
- Test: `install_test.sh`

**Interfaces:**
- Consumes: 无
- Produces: shell 函数 `print_next_steps`（输出到 stderr，与脚本内 `log` 一致）

- [ ] **Step 1: 写失败的测试**

追加到 `install_test.sh` 末尾（`fails` 汇总之前）：

```bash
# 装完必须把下一步指向 handoff init，并说清不托管的后果。
#
# why 值得一条断言：这是本脚本唯一直接影响用户下一步动作的产物。B71 现场那台
# 机器的 agentd 从来没被托管过——因为安装脚本从头到尾没提过 handoff init，
# 而托管提示躺在 init 的最后一行，用户要先知道该跑 init 才看得到它。
out="$(print_next_steps 2>&1)"
case "$out" in
  *"handoff init"*) ;;
  *) printf 'FAIL  下一步提示必须点名 handoff init\n      实得 %s\n' "$out" >&2
     fails=$((fails + 1)) ;;
esac
case "$out" in
  *重启*) ;;
  *) printf 'FAIL  下一步提示必须说清不托管的后果（重启后不会自己回来）\n      实得 %s\n' "$out" >&2
     fails=$((fails + 1)) ;;
esac
```

- [ ] **Step 2: 跑测试确认失败**

Run: `bash install_test.sh`
Expected: FAIL —— `print_next_steps: command not found`，两条断言都不过

- [ ] **Step 3: 写实现**

`install.sh` 里，在 `main` 定义之前加：

```bash
# print_next_steps 打印装完之后该做什么。
#
# 独立成函数是为了能被 install_test.sh source 之后单独断言：提示文案是本脚本
# 唯一直接影响用户下一步动作的产物，值得一条断言守着。
#
# 边界不变：本脚本仍然不写服务单元、不改 rc、不 sudo——托管由 handoff init
# 追问、由 handoff service install 执行。
print_next_steps() {
  log ""
  log "下一步   handoff init"
  log "         执行机会探测 executor，并问你是否把 agentd 交给 launchd / systemd 托管。"
  log "         没有托管的 agentd 在机器重启后不会自己回来。"
}
```

`main` 末尾（现有的 `case ":${PATH}:"` 那段之后）加一行 `print_next_steps`。

- [ ] **Step 4: 跑测试确认通过**

Run: `bash install_test.sh && echo OK`
Expected: 静默通过并打印 `OK`

- [ ] **Step 5: 日志与注释自检**

- 输出走脚本自带的 `log`（写 stderr），**不用裸 `echo` 到 stdout**——stdout 留给可能被管道消费的内容，这是 `install.sh` 现有的约定
- `print_next_steps` 上有函数注释：说清它做什么、**为什么要独立成函数**（可被单测断言）、以及边界不变（不写单元、不改 rc、不 sudo）
- 提示文案本身必须含「后果」那一句。只说「建议跑 handoff init」的提示，用户会当成可选的美化步骤跳过——B71 现场就是这么来的

- [ ] **Step 6: 提交**

```bash
git add install.sh install_test.sh
git commit -m "feat(install): 安装脚本把下一步指向 handoff init 并说清不托管的后果"
```

---

### Task 7: 文档同步

**Files:**
- Modify: `README.md`（快速开始的托管段落、配置段加 `path_dirs`、Troubleshooting 加一条）

**Interfaces:**
- Consumes: 前六个 task 的全部对外行为
- Produces: 无代码接口

- [ ] **Step 1: 配置段加 `path_dirs`**

`README.md` 的「配置（~/.handoff/config.yaml）」代码块里，`repo_root` 那行之后加：

```yaml
path_dirs: []                 # 额外的可执行文件搜索目录；agentd 启动时追加到 PATH 末尾
```

代码块之后、`repo_root` 说明段之前，插入一段：

```markdown
`path_dirs` 是**执行机顶层配置**：agentd 启动时会把这些目录追加到 PATH 末尾，用来兜住
「工具装了、但它的目录不在任何 shell rc 文件里」的机器。agentd 本身已经会做三件事——
继承启动时的 PATH、合并登录 shell（`$SHELL -l -i`）的 PATH、扫描一批内置的已知安装目录
（`~/.opencode/bin`、`~/.grok/bin`、`~/.claude/local`、`~/.local/bin`、`~/bin`、`~/.bun/bin`、
`~/.npm-global/bin`、`~/.cargo/bin`、`~/go/bin`、`/opt/homebrew/{bin,sbin}`、
`/usr/local/{bin,sbin}`、`/snap/bin`，**存在才追加**）——`path_dirs` 只用于这三层都没覆盖到的
安装位置。**没配就别写这个键**（留空时不会落盘）。

这不是给 executor 传环境变量的地方：代理、私有 registry 那些走 `env` 段。
```

- [ ] **Step 2: 快速开始段落改口径**

`handoff init` 那段说明（「`handoff init` 可以随时重跑当改配置用…」）后面补一句：

```markdown
角色选了执行机时，`handoff init` 会在最后**追问一句**是否现在就把 agentd 交给
launchd / systemd 托管，答 y 即就地装好（Linux 上需要 root 写 `/etc/systemd/system`，
非 root 时只打印 `sudo handoff service install` 不代跑）。**不托管的 agentd 在机器
重启后不会自己回来**，而且它的 PATH 取决于当时那个 shell——这正是「重启后第一次派发
报 executor 未安装」的成因。
```

- [ ] **Step 3: Troubleshooting 加一条**

「常见问题」列表里加：

```markdown
- 派发报 `xxx 未安装: executable file not found in $PATH`，但你在自己终端里明明能跑它 → agentd 拿到的 PATH 与你终端里的不是一回事（launchd 给的就是 `/usr/bin:/bin:/usr/sbin:/sbin`）。agentd 启动时会补全（登录 shell + 内置已知目录表），启动日志里搜 `已补全 PATH` 看它到底补了什么、搜 `executor 探测` 看四家各自解析到哪。都没覆盖到就把工具目录写进 `config.yaml` 的 `path_dirs` 后重启 agentd。
```

- [ ] **Step 4: 自检**

Run: `grep -n 'path_dirs' README.md`
Expected: 至少三处命中（配置块、说明段、Troubleshooting）

- [ ] **Step 5: 提交**

```bash
git add README.md
git commit -m "docs: README 同步 path_dirs、init 的托管追问与 PATH 排障入口"
```

---

## 真机验收（devbox，代码全部落地后执行）

按 spec §9 执行，**全程不允许有任何一次手工 `export PATH`**：

| 步 | 动作 | 判据 |
|---|---|---|
| V1 | `handoff upgrade --now --target devbox` | 对端版本号变了 |
| V2 | 在 devbox 上 `handoff service install` | `handoff service status` 报已托管 |
| V3 | 从裸最小 PATH 的非交互 ssh 会话重启 agentd（`launchctl kickstart -k gui/$(id -u)/dev.gosuper.handoff.agentd`） | 启动日志 `已补全 PATH` 的 `known_dirs` 段含 `/Users/sycm/.opencode/bin` |
| V4 | 同一次启动的日志 | `executor 探测` 一行里 opencode 解析到 `/Users/sycm/.opencode/bin/opencode`，无 WARN |
| V5 | 真派发一个任务 | 跑通，不出现 `opencode 未安装` |
| V6 | 在 devbox 上重跑 `handoff init`（一路回车，不改配置） | 探测表里 opencode 那行带「不在你的 PATH 里」的说明 |

V3 重启前先确认 devbox 上没有别人的活跃任务；重启不杀执行者（setsid，B36/B59 已实证），但仍需当事人知情。

验收通过后把证据写回 `docs/superpowers/backlog.md` 的 B71 行，状态改 `✅ done(已验)`。

---

## Self-Review

**Spec 覆盖**：§3（pathenv 四层来源 + 内置表 + HOME 兜底 + 日志分桶）→ Task 1；§7（`path_dirs` + `omitempty` + 严格解析键清单）→ Task 2；§4（启动自检 + 缺省缺失 WARN 不阻断）→ Task 3；§5.1/5.2（init 补全 + 「是补出来的」说明）→ Task 4；§5.3（追问托管 + Linux 非 root 不代跑 + 失败不让 init 失败）→ Task 5；§6（install.sh 下一步）→ Task 6；§8 测试全部内联在各 task 的 Step 1；§9 真机验收独立成节。

**与 spec 的一处偏差（有意）**：spec §8.4 写「断言安装成功的输出里出现 `handoff init`」。`install_test.sh` 的既有边界是「只测能纯函数化的部分，不测安装本身」，所以 Task 6 把提示抽成 `print_next_steps` 单独断言，而不是去跑一次真安装。断言的内容与 spec 一致。

**类型一致性**：`Apply` 的返回值 `[]string` 在 Task 4 被命名为 `added` 并传给 `printDetection(w, rs, addedDirs)`，`coveredBy` 消费同一个切片；`askAll` 的新返回值 `isExec bool` 与 `maybeInstallService(w, r, isExec, cfgPath)` 的形参一致；`installService(out io.Writer, cfgPath string) error` 在 `serviceInstallCmd.RunE` 与 `maybeInstallService` 两处签名相同。
