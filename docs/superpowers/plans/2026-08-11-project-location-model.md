# 项目位置模型（B62）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把派发的入参从「代码在那台机器的哪个目录」收敛成「哪个项目 + 哪台机器」，落成一张以 `project_id` 为主键的项目位置表，并让本机 CLI 在目标机缺位置时自动补登记。

**Architecture:** 新增纯函数包 `internal/projectid` 计算跨机一致的 `project_id`；`internal/store` 用 `project_locations` 表取代 `repos`；`internal/agentd` 把「登记」与「解析」两层重写为项目语义（`projectadmin.go` / `projectresolve.go`），HTTP 面 `/api/repos` 改 `/api/projects`，dispatch 请求体去掉一切路径字段；`cmd` 侧删除 `--repo`、`repo` 子命令改名 `project`，并在 dispatch 遇到 400 未登记时编排「登记本机 + 登记目标机 + 重发」两跳。

**Tech Stack:** Go（标准库 + cobra + modernc.org/sqlite）、SQLite、`log/slog`。

## Global Constraints

- 语言与注释：全部注释、日志、错误报文用**中文**。新文件必须有文件头注释（职责 + 边界），导出函数必须有 doc 注释（参数/返回/注意）。
- 日志：agentd 侧用 `m.log`（Manager）或包级 `log()`；**禁止 `fmt.Printf` 作为日志手段**。CLI 侧面向人的输出走 `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`（这是输出契约，不是日志）。
- 分层：`cmd` → `internal/client` → HTTP → `internal/agentd` → `internal/store`。`internal/store` **不得**导入 `internal/agentd`（会成环），因此 `project_id` 的计算必须放在两者都能导入的 `internal/projectid`。
- `internal/proto` 保持「纯类型包：无 I/O、无业务逻辑」，不放计算函数。
- 破坏性变更，**不留任何兼容别名**：不保留 `--repo`、不保留 `repo` 子命令、不加 `--allow-unregistered` 之类逃生口。
- 每个 task 结束时 `go build ./... && go vet ./... && go test ./...` 必须全绿才提交。
- `project_id` 定义（逐字）：`sha256(normalizeGitURL(origin_url))` 的十六进制串前 16 位。
- `repo_root` 默认值（逐字）：`<DataDir>/repos`，即 `~/.handoff/repos`。
- 工作目录：本计划在 worktree `/Users/xushixin/workspace/handoff/.claude/worktrees/b62-repo-registration`（分支 `handoff/b62-repo-registration`）内实施。

**Spec:** [docs/superpowers/specs/2026-08-11-repo-registration-normalization-design.md](../specs/2026-08-11-repo-registration-normalization-design.md)

---

## 文件结构

**新建：**

| 文件 | 职责 |
|---|---|
| `internal/projectid/projectid.go` | 纯函数：git URL 归一化 + `project_id` 派生。无依赖，store/agentd/cmd 共用 |
| `internal/projectid/projectid_test.go` | 折叠用例（表驱动） |
| `internal/agentd/gitroot.go` | `MainWorktreeRoot`：把 linked worktree 归并到主工作树 |
| `internal/agentd/gitroot_test.go` | 主仓/worktree/子目录/非仓库四种位置 |
| `internal/agentd/projectadmin.go` | 登记操作层：`RegisterProject` / `ListProjects` / `UnregisterProject` |
| `internal/agentd/projectadmin_test.go` | 登记的各分支（替代 `repoadmin_test.go`） |
| `internal/agentd/projectresolve.go` | 纯解析：`project_id` / `project_name` → 位置行 |
| `internal/agentd/projectresolve_test.go` | 解析表驱动用例（替代 `reporegistry_test.go`） |
| `internal/store/projects.go` | `project_locations` 表的 CRUD + `repos` 表迁移 |
| `internal/store/projects_test.go` | CRUD + 迁移用例（替代 `repos_test.go`） |
| `cmd/project.go` | `handoff project add/ls/rm`（替代 `cmd/repo.go`） |
| `cmd/project_test.go` | flag 校验与输出（替代 `cmd/repo_test.go`） |

**删除：** `internal/agentd/reporegistry.go`、`internal/agentd/reporegistry_test.go`、`internal/agentd/repoadmin.go`、`internal/agentd/repoadmin_test.go`、`internal/store/repos.go`、`internal/store/repos_test.go`、`cmd/repo.go`、`cmd/repo_test.go`。

**修改：** `internal/proto/proto.go`、`internal/store/store.go`、`internal/agentd/manager.go`、`internal/agentd/server.go`、`internal/config/config.go`、`internal/client/client.go`、`cmd/dispatch.go`、`cmd/root.go`、`cmd/init.go`、`README.md`、`skills/handoff/SKILL.md`，以及 agentd 的既有测试（`manager_test.go` / `workspace_test.go` / `approver_test.go` / `integration_test.go`）。

---

## Task 1: `internal/projectid` 纯函数包

**Files:**
- Create: `internal/projectid/projectid.go`
- Test: `internal/projectid/projectid_test.go`

**Interfaces:**
- Consumes: 无（新包，只依赖标准库）
- Produces:
  - `projectid.NormalizeGitURL(raw string) string` — git 远程地址折叠成可比对的规范串；空白输入返回空串
  - `projectid.FromOrigin(originURL string) string` — `sha256(NormalizeGitURL(originURL))` 前 16 位十六进制；归一化后为空时返回空串

**为什么单独成包：** `internal/store` 的迁移要算 `project_id`，`internal/agentd` 的登记与解析要算，`cmd` 侧不需要算（它把 origin 交给 agentd 现读）。store 不能导入 agentd（agentd 已导入 store，会成环），`internal/proto` 又定了「纯类型包，无业务逻辑」的边界，所以只能新开一个零依赖的纯函数包。

- [ ] **Step 1: 写失败测试**

创建 `internal/projectid/projectid_test.go`：

```go
package projectid

import "testing"

// TestNormalizeGitURLFolds 验证同一仓库的各种写法折叠成同一个规范串。
// 用例沿用被替换的 agentd/reporegistry_test.go，行为不得回退。
func TestNormalizeGitURLFolds(t *testing.T) {
	const want = "github.com/xushixin/handoff"
	for _, raw := range []string{
		"git@github.com:xushixin/handoff.git",
		"git@github.com:xushixin/handoff",
		"https://github.com/xushixin/handoff.git",
		"https://github.com/xushixin/handoff/",
		"http://GitHub.com/xushixin/handoff.git",
		"ssh://git@github.com/xushixin/handoff.git",
		"ssh://git@github.com:22/xushixin/handoff.git",
	} {
		if got := NormalizeGitURL(raw); got != want {
			t.Errorf("NormalizeGitURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := NormalizeGitURL("   "); got != "" {
		t.Errorf("空白输入应归一化为空串，got %q", got)
	}
	if NormalizeGitURL("git@github.com:a/x.git") == NormalizeGitURL("git@github.com:b/x.git") {
		t.Error("不同 owner 的同名仓库被错误折叠")
	}
}

// TestFromOriginIsStableAndDistinct 验证 project_id 的两条性质：
// 同仓库各写法必得同一个 ID（跨机一致的基础）；不同仓库必得不同 ID。
func TestFromOriginIsStableAndDistinct(t *testing.T) {
	id := FromOrigin("git@github.com:xushixin/handoff.git")
	if len(id) != 16 {
		t.Fatalf("project_id 长度 = %d, want 16（值 %q）", len(id), id)
	}
	for _, raw := range []string{
		"https://github.com/xushixin/handoff",
		"https://github.com/xushixin/handoff.git",
		"ssh://git@GitHub.com/xushixin/handoff.git",
	} {
		if got := FromOrigin(raw); got != id {
			t.Errorf("FromOrigin(%q) = %q, want %q", raw, got, id)
		}
	}
	if FromOrigin("git@github.com:xushixin/tk.git") == id {
		t.Error("不同仓库得到了相同的 project_id")
	}
	if got := FromOrigin("  "); got != "" {
		t.Errorf("空 origin 应返回空 ID，got %q", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/projectid/ -run TestNormalizeGitURL -v
```

Expected: 编译失败，`undefined: NormalizeGitURL`。

- [ ] **Step 3: 写实现**

创建 `internal/projectid/projectid.go`。`NormalizeGitURL` 的函数体**逐字搬运**自 `internal/agentd/reporegistry.go` 的 `normalizeGitURL`（该文件将在 Task 5 删除），只改名与导出性：

```go
// Package projectid 计算项目的机器无关身份。
//
// 职责：
//   - NormalizeGitURL：把同一仓库的各种 git URL 写法折叠成可比对的规范串
//   - FromOrigin：由 origin 派生 project_id（跨机一致、可离线计算）
//
// 边界：
//   - 纯函数包：无 I/O、无日志、无数据库、无 git 调用，只依赖标准库
//   - 不判断 URL 是否真的可访问、不解析 DNS、不做 host 别名等价
//   - 不做持久化：表里存的始终是原始 origin，本包只产出比对/派生用的值
//
// 为什么单独成包而不是放 proto 或 agentd：internal/store 的迁移与
// internal/agentd 的登记/解析都要算 project_id，而 store 不能导入 agentd
//（会成引用环），proto 又定了「纯类型包、无业务逻辑」的边界。
package projectid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// idLen 是 project_id 的十六进制字符数。
//
// 为什么 16 位（64 bit）而不是全量 64 位：它要出现在日志、错误报文与将来的
// Web 项目树里，给人读；64 bit 的碰撞概率对「一台机器上几十个项目」的量级
// 远远够用，而全量 sha256 会把有用信息挤出视线。
const idLen = 16

// NormalizeGitURL 把 git 远程地址折叠成可比对的规范串。
//
// 参数：
//   - raw: 原始 URL，如 git@github.com:xushixin/handoff.git
//
// 返回：
//   - 规范串，如 github.com/xushixin/handoff；输入为空白时返回空串
//
// 注意：
//   - 仅用于**比对与派生**，位置表里存的始终是原始 URL
//   - 只把首段（host）转小写：路径段在部分 git 服务端是大小写敏感的，
//     整串转小写有把两个不同仓库折叠到一起的风险
//   - 不做的事：不解析 DNS、不做 host 别名等价（github.com 与其镜像不视为同一个）
func NormalizeGitURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// 1) 剥 scheme
	for _, p := range []string{"ssh://", "git://", "https://", "http://"} {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			s = s[len(p):]
			break
		}
	}
	// 2) 剥 user@ 前缀（只在首个 '/' 之前找 '@'，避免误伤路径里的 '@'）
	if i := strings.IndexByte(s, '@'); i >= 0 {
		if j := strings.IndexByte(s, '/'); j < 0 || i < j {
			s = s[i+1:]
		}
	}
	// 3) 首个 '/' 之前的 ':' 有两种含义，分别处理：
	//    - scp-like 分隔符（github.com:owner/repo）→ 换成 '/'
	//    - 端口（github.com:22/owner/repo）→ 整段丢弃
	//    不处理的话，同一仓库的 ssh 与 https 写法永远匹配不上。
	if c := strings.IndexByte(s, ':'); c >= 0 {
		slash := strings.IndexByte(s, '/')
		if slash < 0 || c < slash {
			rest := s[c+1:]
			seg := rest
			if k := strings.IndexByte(rest, '/'); k >= 0 {
				seg = rest[:k]
			}
			if seg != "" && strings.IndexFunc(seg, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
				// 纯数字=端口，连同它一起丢掉
				s = s[:c] + rest[len(seg):]
			} else {
				s = s[:c] + "/" + rest
			}
		}
	}
	// 4) 去尾部 '/' 与 '.git'（顺序不能反：形如 ".../repo.git/" 两者都要去掉）
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	// 5) 首段（host）转小写
	if i := strings.IndexByte(s, '/'); i > 0 {
		s = strings.ToLower(s[:i]) + s[i:]
	} else {
		s = strings.ToLower(s)
	}
	return s
}

// FromOrigin 由 origin 地址派生 project_id。
//
// 参数：
//   - originURL: 仓库的 origin 原始地址
//
// 返回：
//   - 16 位十六进制串；originURL 归一化后为空时返回空串（调用方据此判「算不出身份」）
//
// 注意：
//   - 这是**纯函数**：每台机器各算各的，同一个 origin 必然得到同一个值。
//     跨机引用因此不需要任何中心服务或协调协议
//   - 取 sha256 而非直接用归一化串：归一化串含 '/' 与大小写，做主键与
//     URL 路径段都要转义；定长十六进制串没有这些麻烦
func FromOrigin(originURL string) string {
	norm := NormalizeGitURL(originURL)
	if norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])[:idLen]
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/projectid/ -v
```

Expected: PASS（两个测试全绿）。

- [ ] **Step 5: 加关键节点日志**

本包**刻意不打任何日志**，这是有意的例外，不是遗漏。理由必须写进文件头注释（Step 3 已写入「无 I/O、无日志」）：它是被 dispatch 每次调用的纯函数，在这里打日志会在热路径上刷屏，而调用方（`store` 的迁移、`agentd` 的登记与解析）已经在各自的边界打了带上下文的日志，`project_id` 的取值在那些行里可见。

自检：确认 `internal/projectid/projectid.go` 中没有 `fmt.Printf`、`println`、`log.` 的痕迹。

```bash
grep -n "fmt.Print\|println\|log\." internal/projectid/projectid.go || echo "OK: 无日志调用（符合纯函数包边界）"
```

- [ ] **Step 6: 加意图注释**

确认 Step 3 已包含：包头注释（职责 + 边界 + 为什么单独成包）、`idLen` 常量的「为什么 16 位」、`NormalizeGitURL` 与 `FromOrigin` 的 doc 注释、归一化五步中每一步的「为什么」（尤其第 3 步的 `:` 两义与第 5 步的只转 host 小写）。缺任何一条就补上。

- [ ] **Step 7: 提交**

```bash
go build ./... && go vet ./... && go test ./internal/projectid/
git add internal/projectid/
git commit -m "feat(projectid): 新增纯函数包，由 origin 派生跨机一致的 project_id"
```

---

## Task 2: `MainWorktreeRoot` 主工作树归并

**Files:**
- Create: `internal/agentd/gitroot.go`
- Test: `internal/agentd/gitroot_test.go`

**Interfaces:**
- Consumes: `gitRun(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error)`（`internal/agentd/workspace.go:92`）、`ErrRepoUnusable`（`internal/agentd/workspace.go`）
- Produces: `agentd.MainWorktreeRoot(ctx context.Context, dir string) (string, error)` — 返回 `dir` 所属仓库的**主工作树根目录**绝对路径；`dir` 不是 git 仓库时返回包装 `ErrRepoUnusable` 的错误

**为什么放 `internal/agentd` 而不是新包：** 它要跑 git，而 `gitRun`（带调用日志与超时透传）已经在这里；`cmd` 包本来就已导入 `internal/agentd`（见 `cmd/agentd.go:28`），CLI 侧直接调用即可，不必为一个函数再开一个包、更不要在 CLI 里复制一份实现——spec §5 要求全项目只有一套归并算法。

**算法依据（已实测）：** `git rev-parse --git-common-dir` 在四种位置的输出：

| 执行位置 | 输出 |
|---|---|
| 主仓根目录 | `.git`（相对） |
| 主仓子目录 | `../.git`（相对） |
| linked worktree 根目录 | `/绝对/路径/到/主仓/.git` |
| linked worktree 子目录 | `/绝对/路径/到/主仓/.git` |

因此算法是：输出为相对路径时以 `dir` 为基准 `filepath.Join`，然后取其父目录。

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/gitroot_test.go`：

```go
package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestMainWorktreeRootMergesLinkedWorktree 验证四种位置都归并到主仓根：
// 主仓根 / 主仓子目录 / linked worktree 根 / linked worktree 子目录。
//
// 为什么必须覆盖子目录：git rev-parse --git-common-dir 在主仓里返回的是
// **相对**路径（根目录 ".git"，子目录 "../.git"），只测根目录会漏掉相对
// 路径的拼接分支。
func TestMainWorktreeRootMergesLinkedWorktree(t *testing.T) {
	ctx := context.Background()
	main := initGitRepo(t)
	sub := filepath.Join(main, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("建子目录: %v", err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitAt(t, main, "worktree", "add", "-b", "feat/x", wt)
	wtSub := filepath.Join(wt, "internal")
	if err := os.MkdirAll(wtSub, 0o755); err != nil {
		t.Fatalf("建 worktree 子目录: %v", err)
	}

	wantMain, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", main, err)
	}
	for _, dir := range []string{main, sub, wt, wtSub} {
		got, err := MainWorktreeRoot(ctx, dir)
		if err != nil {
			t.Fatalf("MainWorktreeRoot(%s): %v", dir, err)
		}
		gotReal, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatalf("EvalSymlinks(%s): %v", got, err)
		}
		if gotReal != wantMain {
			t.Errorf("MainWorktreeRoot(%s) = %s, want %s", dir, gotReal, wantMain)
		}
	}
}

// TestMainWorktreeRootRejectsNonRepo 验证非 git 目录被拒，且错误可判别、报文含路径。
func TestMainWorktreeRootRejectsNonRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := MainWorktreeRoot(context.Background(), dir)
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want errors.Is(..., ErrRepoUnusable)", err)
	}
}
```

> `initGitRepo(t)` 与 `gitAt(t, dir, args...)` 是 `internal/agentd/workspace_test.go` 里的既有助手，直接复用。

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/agentd/ -run TestMainWorktreeRoot -v
```

Expected: 编译失败，`undefined: MainWorktreeRoot`。

- [ ] **Step 3: 写实现**

创建 `internal/agentd/gitroot.go`：

```go
// 本文件负责一件事：把「cwd 落在哪」归并成「项目在这台机器上的那一个位置」。
//
// 职责：
//   - MainWorktreeRoot：无论调用点在主仓、主仓子目录、linked worktree 还是
//     worktree 子目录，一律返回**主工作树的根目录**
//
// 边界：
//   - 不读登记表、不碰数据库：它只回答「这个目录属于哪个仓库根」
//   - 不判断该仓库有没有 origin：那是登记层 projectOriginURL 的事
//   - 不做 symlink 求真（不 EvalSymlinks）：返回值来自 git 自己的输出与调用方
//     给的目录，保持与 git 一致的视角
//
// 为什么必须归并（B62）：项目位置表以 project_id 为主键，一个项目在一台机器上
// 只能有一行。本仓库当前有十几个 linked worktree，它们与主仓 origin 相同、
// project_id 相同——不归并就会撞主键，允许多行则项目树彻底没法看。
package agentd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// MainWorktreeRoot 返回 dir 所属 git 仓库的主工作树根目录。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - dir: 任意目录（主仓根/主仓子目录/linked worktree/worktree 子目录）
//
// 返回：
//   - 主工作树根目录的绝对路径（已 Clean）
//   - 错误：dir 不是 git 仓库（或 git 不可用）时返回包装 ErrRepoUnusable 的错误，
//     报文带 git 的 stderr 原文
//
// 注意：
//   - git rev-parse --git-common-dir 在**主仓内**返回相对路径（根目录 ".git"，
//     子目录 "../.git"），在 **linked worktree 内**返回指向主仓的绝对路径。
//     两种形态都要处理：相对时以 dir 为基准拼接，再取父目录
//   - 返回值一律绝对化：位置表的 path 列是绝对路径，UNIQUE 约束要靠它才有意义
func MainWorktreeRoot(ctx context.Context, dir string) (string, error) {
	out, stderr, err := gitRun(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		log().Warn("主工作树归并失败：目录不是 git 仓库", "dir", dir,
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return "", fmt.Errorf("%w: %s 不是 git 仓库: %s: %v",
			ErrRepoUnusable, dir, strings.TrimSpace(stderr), err)
	}
	common := strings.TrimSpace(out)
	if common == "" {
		log().Warn("主工作树归并失败：git 返回空的 common-dir", "dir", dir)
		return "", fmt.Errorf("%w: %s 的 git-common-dir 为空", ErrRepoUnusable, dir)
	}
	// 相对路径（主仓内的 ".git" / "../.git"）以 dir 为基准展开；绝对路径
	//（linked worktree 内）原样使用——它已经指向主仓的 .git。
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	root, err := filepath.Abs(filepath.Dir(common))
	if err != nil {
		log().Error("主工作树归并失败：绝对化仓库根出错", "dir", dir, "common", common, "cause", err)
		return "", fmt.Errorf("%w: 绝对化 %s: %v", ErrRepoUnusable, common, err)
	}
	if root != dir {
		log().Info("主工作树归并", "from", dir, "root", root)
	}
	return filepath.Clean(root), nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/agentd/ -run TestMainWorktreeRoot -v
```

Expected: PASS（两个测试全绿）。

- [ ] **Step 5: 加关键节点日志**

确认以下三条已在 Step 3 的实现里（缺则补）：

- git 调用失败分支：`log().Warn("主工作树归并失败：目录不是 git 仓库", "dir", ..., "stderr", ..., "cause", err)`
- 空输出分支与绝对化失败分支各有一条带 `dir` 的 Warn/Error
- 成功且**发生了归并**时（`root != dir`）打 `log().Info("主工作树归并", "from", dir, "root", root)`
  —— 这条是行为变更的现场证据：spec §5 明确「在 worktree 里派发的语义变了」，
  不打这一行，人会以为执行者找错了目录。`root == dir` 时不打，避免每次派发都刷一行无信息量的日志。

> `gitRun` 自身已经打了每次 git 调用（`log().Info("git 调用", ...)`），所以「调用前」的日志不必重复。

- [ ] **Step 6: 加意图注释**

确认：文件头注释（职责 + 边界 + 为什么必须归并）、`MainWorktreeRoot` 的 doc 注释（参数/返回/两种输出形态的注意）、相对/绝对分支上的「为什么」内联注释。

- [ ] **Step 7: 提交**

```bash
go build ./... && go vet ./... && go test ./internal/agentd/ -run TestMainWorktreeRoot
git add internal/agentd/gitroot.go internal/agentd/gitroot_test.go
git commit -m "feat(agentd): 加 MainWorktreeRoot，把 linked worktree 归并到主仓"
```

---

## Task 3: `proto.ProjectLocation` 与 `project_locations` 表

**Files:**
- Create: `internal/store/projects.go`
- Test: `internal/store/projects_test.go`
- Modify: `internal/proto/proto.go`（在 `Repo` 之后追加新类型，**本任务不删 `Repo`**）
- Modify: `internal/store/store.go`（`Open` 的建表清单里追加 `project_locations`）

**Interfaces:**
- Consumes: `projectid.FromOrigin`（Task 1）、`store.rowScanner`、`store.fmtTime` / `store.parseTime`、`store.ErrNotFound`
- Produces:
  - `proto.ProjectLocation{ProjectID, Name, Path, OriginURL string; CreatedAt time.Time; Status string}`
  - `store.ErrProjectDuplicate error`
  - `(*Store).CreateProjectLocation(loc *proto.ProjectLocation) error`
  - `(*Store).GetProjectLocationByName(name string) (proto.ProjectLocation, error)`
  - `(*Store).ListProjectLocations() ([]proto.ProjectLocation, error)`
  - `(*Store).DeleteProjectLocation(name string) error`

**为什么本任务不动旧的 `repos`：** 新旧并存一步，`go build`/`go test` 在本次提交上依然全绿；Task 5 的切换任务再一次性拆掉旧表、旧类型与旧 CRUD。中间态只存在于同一个分支的相邻两次提交之间，且两张表都是空的（spec §4.2 实测），不存在双写不一致的风险。

- [ ] **Step 1: 写失败测试**

创建 `internal/store/projects_test.go`：

```go
package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/projectid"
	"github.com/xushixin/handoff/internal/proto"
)

// newProjectStore 开一个临时库，供本文件的用例共用。
func newProjectStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// mustCreateLoc 按 origin 算出 project_id 后落一条位置。
func mustCreateLoc(t *testing.T, st *Store, name, path, origin string) proto.ProjectLocation {
	t.Helper()
	loc := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin(origin), Name: name, Path: path,
		OriginURL: origin, CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&loc); err != nil {
		t.Fatalf("CreateProjectLocation(%s): %v", name, err)
	}
	return loc
}

// TestProjectLocationCRUD 覆盖增查列删的正常路径。
func TestProjectLocationCRUD(t *testing.T) {
	st := newProjectStore(t)
	a := mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:xushixin/handoff.git")
	mustCreateLoc(t, st, "tk", "/w/tk", "git@github.com:xushixin/tk.git")

	got, err := st.GetProjectLocationByName("handoff")
	if err != nil {
		t.Fatalf("GetProjectLocationByName: %v", err)
	}
	if got.ProjectID != a.ProjectID || got.Path != "/w/handoff" {
		t.Fatalf("查回的行不对: %+v", got)
	}

	list, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(list) != 2 || list[0].Name != "handoff" || list[1].Name != "tk" {
		t.Fatalf("列表应按名字字典序返回 2 行，got %+v", list)
	}

	if err := st.DeleteProjectLocation("handoff"); err != nil {
		t.Fatalf("DeleteProjectLocation: %v", err)
	}
	if _, err := st.GetProjectLocationByName("handoff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后再查应 ErrNotFound，got %v", err)
	}
	if err := st.DeleteProjectLocation("handoff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删不存在的行应 ErrNotFound（不能静默成功），got %v", err)
	}
}

// TestProjectLocationPrimaryKeyEnforcesOneLocationPerProject 验证 ADR-0008
// 的「一台机器上一个项目最多一个位置」由主键直接强制：同一个 origin 的
// 第二次登记（哪怕换了名字和路径）必须被拒。
func TestProjectLocationPrimaryKeyEnforcesOneLocationPerProject(t *testing.T) {
	st := newProjectStore(t)
	mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:xushixin/handoff.git")
	dup := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin("https://github.com/xushixin/handoff"),
		Name:      "handoff-again", Path: "/w/handoff-again",
		OriginURL: "https://github.com/xushixin/handoff", CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&dup); !errors.Is(err, ErrProjectDuplicate) {
		t.Fatalf("同 origin 的第二个位置应被主键拒绝，got %v", err)
	}
}

// TestProjectLocationNameAndPathAreUnique 验证名字与路径各自唯一：
// 名字唯一是因为 --project <名字> 与 project rm <名字> 要靠它引用；
// 路径唯一是因为两个不同项目不能声称在同一个目录。
func TestProjectLocationNameAndPathAreUnique(t *testing.T) {
	st := newProjectStore(t)
	mustCreateLoc(t, st, "handoff", "/w/handoff", "git@github.com:xushixin/handoff.git")

	sameName := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin("git@github.com:other/handoff.git"),
		Name:      "handoff", Path: "/w/other", OriginURL: "git@github.com:other/handoff.git",
		CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&sameName); !errors.Is(err, ErrProjectDuplicate) {
		t.Fatalf("重名应被拒，got %v", err)
	}
	samePath := proto.ProjectLocation{
		ProjectID: projectid.FromOrigin("git@github.com:other/thing.git"),
		Name:      "thing", Path: "/w/handoff", OriginURL: "git@github.com:other/thing.git",
		CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(&samePath); !errors.Is(err, ErrProjectDuplicate) {
		t.Fatalf("同路径应被拒，got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/store/ -run TestProjectLocation -v
```

Expected: 编译失败，`undefined: proto.ProjectLocation` / `undefined: ErrProjectDuplicate`。

- [ ] **Step 3: 加 `proto.ProjectLocation`**

在 `internal/proto/proto.go` 末尾（既有 `Repo` 类型之后）追加：

```go
// ProjectLocation 是一条「项目 × 机器」位置记录：项目在**这一台**机器上的
// 那一个工作副本。
//
// 模型（B62）：
//   - 项目（project）：一份代码的逻辑身份，与机器无关，由 ProjectID 标识
//   - 位置（location）：项目在某一台机器上的工作副本，由 Path 标识
//   - ADR-0008：一台机器上一个项目**最多一个位置**，由 ProjectID 做主键强制
//
// 字段：
//   - ProjectID: sha256(归一化 origin) 前 16 位；**纯函数派生**，每台机器各算
//     各的，同一个 origin 必然得到同一个值——跨机引用因此不需要任何协调
//   - Name: 人可读引用（每台机器内唯一），由 origin 末段派生，冲突时补 -2；
//     只用于 --project <名字> 与 project rm <名字>，**不参与身份判定**
//   - Path: 该机器上的绝对路径（登记时 Abs+Clean，且已归并到主工作树）
//   - OriginURL: agentd 在该机器上**现读**的权威值，不采信调用方上送的字符串
//   - CreatedAt: 登记时间
//   - Status: project ls 时**现场探得**的实际状态（"有效"/"路径不存在"/
//     "不是 git 仓库"），不落库，仅列表响应携带——它是登记与文件系统漂移的
//     可见化手段
type ProjectLocation struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	OriginURL string    `json:"origin_url"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status,omitempty"`
}
```

- [ ] **Step 4: 建表**

在 `internal/store/store.go` 的 `Open` 建表清单里，紧跟在 `repos` 的 DDL 之后追加一条：

```go
		`CREATE TABLE IF NOT EXISTS project_locations (
  -- project_id 做主键：ADR-0008 的「一台机器上一个项目最多一个位置」由它
  -- 直接强制，不需要额外唯一索引，也不需要在应用层再校验一遍。
  project_id TEXT PRIMARY KEY,
  -- name 唯一：--project <名字> 与 project rm <名字> 要靠它引用。
  name TEXT NOT NULL UNIQUE,
  -- path 唯一：两个不同项目不能声称在同一个目录。
  path TEXT NOT NULL UNIQUE,
  origin_url TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`,
```

- [ ] **Step 5: 写 CRUD 实现**

创建 `internal/store/projects.go`：

```go
// 本文件是 project_locations 表（项目 × 本机位置）的持久化实现。
//
// 职责：
//   - project_locations 的增（CreateProjectLocation）、查（GetProjectLocationByName）、
//     列（ListProjectLocations）、删（DeleteProjectLocation）
//   - 把 SQLite 的主键/UNIQUE 冲突翻译成 ErrProjectDuplicate 哨兵，供上层映射 409
//
// 边界：
//   - 不算 project_id：那是 internal/projectid 的纯函数，调用方算好后传进来
//   - 不判断路径是否存在、是不是 git 仓库——那是 agentd 侧 EnsureRepoUsable 的事
//   - 不做名字派生、不做名字去重——那是 agentd/projectadmin.go 的事
//   - 与 store.go 一致的叶子层纪律：方法错误 return 前不打日志，由调用方带上下文记录
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// ErrProjectDuplicate 表示位置登记冲突，三种成因合用一个哨兵：
//   - project_id 已存在 → 这个项目在本机已经有位置了（ADR-0008 只允许一个）
//   - name 已被占用 → 引用名撞了
//   - path 已被另一个项目指向 → 两个项目声称在同一个目录
//
// 为什么合并成一个哨兵：三者在 HTTP 上都是 409，且报文由 agentd 侧按上下文
// 拼装（它知道是自动登记还是人工登记）；分成三个哨兵只会让映射层多两个分支。
var ErrProjectDuplicate = errors.New("项目位置冲突（项目、名字或路径已存在）")

// projectColumns 是 project_locations 的完整读取列清单，Get 与 List 共用同一份。
const projectColumns = `project_id, name, path, origin_url, created_at`

// scanProjectRow 把一行 project_locations 记录读成 proto.ProjectLocation。
func scanProjectRow(sc rowScanner) (proto.ProjectLocation, error) {
	var (
		loc       proto.ProjectLocation
		createdAt string
	)
	if err := sc.Scan(&loc.ProjectID, &loc.Name, &loc.Path, &loc.OriginURL, &createdAt); err != nil {
		return proto.ProjectLocation{}, err
	}
	loc.CreatedAt = parseTime(createdAt)
	return loc, nil
}

// CreateProjectLocation 写入一条项目位置。
//
// 参数：
//   - loc: 位置条目；ProjectID/Name/Path/OriginURL 必须非空，CreatedAt 由调用方给定
//
// 返回：
//   - 错误：项目/名字/路径任一已存在时返回包装 ErrProjectDuplicate 的错误；其余为写库故障
func (s *Store) CreateProjectLocation(loc *proto.ProjectLocation) error {
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO project_locations (project_id, name, path, origin_url, created_at)
VALUES (?, ?, ?, ?, ?)`,
		loc.ProjectID, loc.Name, loc.Path, loc.OriginURL, fmtTime(loc.CreatedAt))
	if err != nil {
		// modernc.org/sqlite 的约束错误只有文本可判：主键冲突报
		// "PRIMARY KEY constraint failed"，UNIQUE 冲突报 "UNIQUE constraint failed"，
		// 没有可用的错误码常量。
		if strings.Contains(err.Error(), "UNIQUE constraint failed") ||
			strings.Contains(err.Error(), "PRIMARY KEY constraint failed") {
			return fmt.Errorf("%w: project_id=%s name=%s path=%s: %v",
				ErrProjectDuplicate, loc.ProjectID, loc.Name, loc.Path, err)
		}
		return fmt.Errorf("写入项目位置 %s: %w", loc.Name, err)
	}
	return nil
}

// GetProjectLocationByName 按引用名查询单条位置。
//
// 返回：
//   - 位置条目；不存在时返回 ErrNotFound
func (s *Store) GetProjectLocationByName(name string) (proto.ProjectLocation, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT `+projectColumns+` FROM project_locations WHERE name = ?`, name)
	loc, err := scanProjectRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.ProjectLocation{}, fmt.Errorf("项目 %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("查询项目位置 %s: %w", name, err)
	}
	return loc, nil
}

// ListProjectLocations 返回本机全部项目位置，按名字字典序。
//
// 注意：
//   - 返回的 Status 字段恒为空——实际状态由 agentd 侧现场探测后填充
func (s *Store) ListProjectLocations() ([]proto.ProjectLocation, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+projectColumns+` FROM project_locations ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("查询项目位置列表: %w", err)
	}
	defer rows.Close()
	var locs []proto.ProjectLocation
	for rows.Next() {
		loc, err := scanProjectRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取项目位置行: %w", err)
		}
		locs = append(locs, loc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历项目位置: %w", err)
	}
	return locs, nil
}

// DeleteProjectLocation 删除一条位置。
//
// 返回：
//   - 错误：位置不存在时返回 ErrNotFound（而非静默成功——调用方需要知道自己删错了名字）
//
// 注意：
//   - 只删登记，**不动磁盘上的仓库**
func (s *Store) DeleteProjectLocation(name string) error {
	res, err := s.db.ExecContext(context.Background(),
		`DELETE FROM project_locations WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("删除项目位置 %s: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("删除项目位置 %s 后取影响行数: %w", name, err)
	}
	if n == 0 {
		return fmt.Errorf("项目 %s: %w", name, ErrNotFound)
	}
	return nil
}
```

- [ ] **Step 6: 运行测试确认通过**

```bash
go test ./internal/store/ -v
```

Expected: 新的 `TestProjectLocation*` 三个用例 PASS，旧的 `repos_test.go` 用例继续 PASS。

- [ ] **Step 7: 加关键节点日志**

`internal/store` 是叶子层，既有纪律是**方法在 return 错误前不打日志，由调用方带上下文记录**（见 `internal/store/repos.go` 文件头）。本任务遵循同一纪律：`projects.go` 内不打日志，但每个错误都用 `fmt.Errorf` 包上足够的上下文（`project_id` / `name` / `path` 三项），保证调用方那一行日志读得懂。

自检：确认 `CreateProjectLocation` 的冲突分支报文同时含 `project_id`、`name`、`path`（三种成因合用一个哨兵，不带全三项就分不清是哪一种撞了）。

```bash
grep -n "ErrProjectDuplicate" internal/store/projects.go
```

- [ ] **Step 8: 加意图注释**

确认：`projects.go` 文件头（职责 + 边界 + 叶子层不打日志的纪律）、`proto.ProjectLocation` 的类型注释（模型三名词 + 每个字段的语义与「不参与身份判定」）、建表 DDL 里三行约束各自的「为什么」、`ErrProjectDuplicate` 的「为什么合并成一个哨兵」、文本判约束冲突的「为什么只能按文本判」。

- [ ] **Step 9: 提交**

```bash
go build ./... && go vet ./... && go test ./internal/store/ ./internal/proto/
git add internal/proto/proto.go internal/store/store.go internal/store/projects.go internal/store/projects_test.go
git commit -m "feat(store): 加 project_locations 表与 proto.ProjectLocation（与旧 repos 并存）"
```

---

## Task 4: agentd 登记层与解析层

**Files:**
- Create: `internal/agentd/projectresolve.go`
- Create: `internal/agentd/projectresolve_test.go`
- Create: `internal/agentd/projectadmin.go`
- Create: `internal/agentd/projectadmin_test.go`

**Interfaces:**
- Consumes: `projectid.FromOrigin`（Task 1）、`MainWorktreeRoot`（Task 2）、Task 3 的 store CRUD、既有 `gitRun` / `EnsureRepoUsable` / `ErrRepoUnusable` / `errBadDispatchRequest` / `ErrWorkdirBusy` / `truncateRunes` / `(*Store).ActiveTasksByRepoPath`
- Produces:
  - `agentd.ErrProjectNotRegistered error`
  - `agentd.ErrProjectAlreadyExists error`
  - `agentd.ErrProjectOriginMismatch error`
  - `resolveProject(projectID, projectName string, entries []proto.ProjectLocation) (proto.ProjectLocation, error)`（包内）
  - `agentd.RegisterProjectReq{OriginURL, Name, Path string}`
  - `(*Manager).RegisterProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error)`
  - `(*Manager).ListProjects(ctx context.Context) ([]proto.ProjectLocation, error)`
  - `(*Manager).UnregisterProject(ctx context.Context, name string) error`

**注意：** 本任务只**新增**，不接线。旧的 `reporegistry.go` / `repoadmin.go` 与 `/api/repos` 路由继续存在并继续被 dispatch 使用，直到 Task 5 一次性切换并删除。

**`RegisterProjectReq` 为什么没有 `Clone bool`：** 形态由 `Path` 是否为空决定——给了 `Path` 就是「这台机器上已经有一份，用它」，没给就是「你自己 clone 到 `repo_root/<名字>`」。多一个布尔位就多一组非法组合（`Clone=true` 且 `Path` 指向已有仓库该怎么办？），而它表达不出任何新语义。

- [ ] **Step 1: 写解析层的失败测试**

创建 `internal/agentd/projectresolve_test.go`：

```go
package agentd

import (
	"errors"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/projectid"
	"github.com/xushixin/handoff/internal/proto"
)

// locFixture 是解析用例共用的位置表快照。
func locFixture() []proto.ProjectLocation {
	mk := func(name, path, origin string) proto.ProjectLocation {
		return proto.ProjectLocation{
			ProjectID: projectid.FromOrigin(origin), Name: name, Path: path, OriginURL: origin,
		}
	}
	return []proto.ProjectLocation{
		mk("handoff", "/root/work/handoff", "git@github.com:xushixin/handoff.git"),
		mk("tk", "/root/work/tk", "https://github.com/xushixin/tk.git"),
	}
}

// TestResolveProject 覆盖 project_id / project_name / 都空 三种入参 × 命中与否。
func TestResolveProject(t *testing.T) {
	handoffID := projectid.FromOrigin("git@github.com:xushixin/handoff.git")
	// 同一仓库的另一种写法必须算出同一个 ID——这是跨机引用成立的前提。
	altID := projectid.FromOrigin("https://github.com/xushixin/handoff")

	tests := []struct {
		name     string
		id       string
		projName string
		entries  []proto.ProjectLocation
		wantPath string
		wantErr  error
	}{
		{name: "project_id 命中", id: handoffID, entries: locFixture(), wantPath: "/root/work/handoff"},
		{name: "另一种 URL 写法算出的 id 同样命中", id: altID, entries: locFixture(), wantPath: "/root/work/handoff"},
		{name: "project_id 未命中（表非空）", id: "deadbeefdeadbeef", entries: locFixture(), wantErr: ErrProjectNotRegistered},
		{name: "project_id 未命中（表为空）", id: handoffID, entries: nil, wantErr: ErrProjectNotRegistered},
		{name: "project_name 命中", projName: "tk", entries: locFixture(), wantPath: "/root/work/tk"},
		{name: "project_name 未命中", projName: "nope", entries: locFixture(), wantErr: ErrProjectNotRegistered},
		{name: "两者都空", entries: locFixture(), wantErr: errBadDispatchRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveProject(tt.id, tt.projName, tt.entries)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is(..., %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got.Path != tt.wantPath {
				t.Fatalf("path = %q, want %q", got.Path, tt.wantPath)
			}
		})
	}
}

// TestResolveProjectErrorsAreActionable 验证拒绝报文带得走「本机登记了什么」，
// 而不是一句干巴巴的「未登记」——远程派发时审核者读不到执行机的 agentd.log，
// 报文是他唯一的线索。
func TestResolveProjectErrorsAreActionable(t *testing.T) {
	_, err := resolveProject("deadbeefdeadbeef", "", locFixture())
	for _, want := range []string{"handoff", "/root/work/handoff", "tk", "/root/work/tk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文 %q 未包含 %q", err.Error(), want)
		}
	}
	_, err = resolveProject("deadbeefdeadbeef", "", nil)
	if !strings.Contains(err.Error(), "本机尚无任何项目") {
		t.Errorf("空表报文应说明本机尚无任何项目，got %q", err.Error())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/agentd/ -run TestResolveProject -v
```

Expected: 编译失败，`undefined: resolveProject`。

- [ ] **Step 3: 写解析层实现**

创建 `internal/agentd/projectresolve.go`：

```go
// 本文件是项目解析的**纯逻辑**层：把「派发请求指的是哪个项目」翻译成
// 「executor 应该在本机的哪个目录工作」。
//
// 职责：
//   - resolveProject：按 project_id / project_name 在位置表里查出那一行
//   - locationLines：把位置表压成人可读的清单，供拒绝报文使用
//
// 边界：
//   - 不碰数据库：位置行由调用方查好后以切片传入
//   - 不碰 git、不碰文件系统：路径是否真的可用由 EnsureRepoUsable 另行判定
//   - 不碰 HTTP：错误只用哨兵表达，状态码映射在 server.go
//   - **不接受任何路径入参**：调用方描述「代码在这台机器的哪个目录」正是 B62
//     要根除的漏洞（spec §1.2）。路径由本机查表得出，别人不许指定
//
// 为什么单独成文件且刻意保持纯净：这段规则是 dispatch 的必经之路，一旦错了
// 就会把任务派到错误的项目上。纯函数才能表驱动穷举。
package agentd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// ErrProjectNotRegistered 表示派发请求指向的项目在本机没有位置。
//
// 映射为 400（调用方先解决请求本身的问题），见 server.go 的 writeDispatchError。
// 本机 CLI 收到它会触发自动登记后重发（spec §6.2）——因此报文既要给人看，
// 也要能被 CLI 用 errors 判别，两者都靠这个哨兵。
var ErrProjectNotRegistered = errors.New("项目未登记")

// locationLines 把位置表压成「名字 → 路径」的一行串，供拒绝报文使用。
//
// 报文必须带得走「本机登记了什么」——远程派发时审核者读不到执行机的
// agentd.log，一句干巴巴的「未登记」等于让他去猜。
func locationLines(entries []proto.ProjectLocation) string {
	if len(entries) == 0 {
		return "（本机尚无任何项目）"
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.Name+" → "+e.Path)
	}
	return strings.Join(lines, "; ")
}

// resolveProject 把派发请求里的项目引用解析成本机的位置行。
//
// 参数：
//   - projectID: 调用方算出的 project_id（优先）
//   - projectName: 人可读引用（仅 --project <名字> 与 Web 控制台会用）
//   - entries: 本机全部位置行
//
// 返回：
//   - 命中的位置行（Path 即 executor 的工作仓库）
//   - 错误：ErrProjectNotRegistered（查不到）或 errBadDispatchRequest（两者都空），
//     均映射 400，且报文自带本机已登记清单
//
// 注意：
//   - projectID 与 projectName 同时给出时以 projectID 为准：它是身份，名字只是引用
//   - 本函数不判断路径是否真的存在，那是 EnsureRepoUsable 的职责
func resolveProject(projectID, projectName string, entries []proto.ProjectLocation) (proto.ProjectLocation, error) {
	switch {
	case projectID != "":
		for _, e := range entries {
			if e.ProjectID == projectID {
				log().Info("项目解析：project_id 命中", "project_id", projectID,
					"name", e.Name, "path", e.Path)
				return e, nil
			}
		}
		log().Warn("项目解析被拒：project_id 查不到", "project_id", projectID,
			"registered", locationLines(entries))
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: project_id=%s；本机已登记的项目：%s（用 handoff project add 落地它）",
			ErrProjectNotRegistered, projectID, locationLines(entries))
	case projectName != "":
		for _, e := range entries {
			if e.Name == projectName {
				log().Info("项目解析：名字命中", "name", projectName, "path", e.Path)
				return e, nil
			}
		}
		log().Warn("项目解析被拒：名字查不到", "name", projectName,
			"registered", locationLines(entries))
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: %q；本机已登记的项目：%s（用 handoff project ls 查看）",
			ErrProjectNotRegistered, projectName, locationLines(entries))
	default:
		log().Warn("项目解析被拒：请求未指明项目")
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 请求未指明项目（project_id 与 project_name 至少其一）", errBadDispatchRequest)
	}
}
```

- [ ] **Step 4: 运行解析层测试确认通过**

```bash
go test ./internal/agentd/ -run TestResolveProject -v
```

Expected: PASS。

- [ ] **Step 5: 写登记层的失败测试**

创建 `internal/agentd/projectadmin_test.go`：

```go
package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/projectid"
)

// initGitRepoWithOrigin 造一个带初始提交且配好 origin 的仓库，返回路径。
// 登记层的每条路径都要求非空 origin，所以本文件的仓库都要走这个助手。
func initGitRepoWithOrigin(t *testing.T, origin string) string {
	t.Helper()
	repo := initGitRepo(t)
	gitAt(t, repo, "remote", "add", "origin", origin)
	return repo
}

// TestRegisterProjectExisting 验证登记已有目录：现读 origin、归并主工作树、
// 算出 project_id 落库。
func TestRegisterProjectExisting(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if loc.ProjectID != projectid.FromOrigin(origin) {
		t.Fatalf("project_id = %q, want %q", loc.ProjectID, projectid.FromOrigin(origin))
	}
	if loc.Name != "handoff" {
		t.Fatalf("名字应由 origin 末段派生，got %q", loc.Name)
	}
	if !filepath.IsAbs(loc.Path) {
		t.Fatalf("路径必须绝对化，got %q", loc.Path)
	}
}

// TestRegisterProjectMergesWorktree 验证给的是 linked worktree 时登记主仓：
// 位置表以 project_id 为主键，worktree 与主仓 origin 相同，不归并就撞主键。
func TestRegisterProjectMergesWorktree(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	main := initGitRepoWithOrigin(t, origin)
	wt := filepath.Join(t.TempDir(), "wt")
	gitAt(t, main, "worktree", "add", "-b", "feat/x", wt)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: wt})
	if err != nil {
		t.Fatalf("RegisterProject(worktree): %v", err)
	}
	wantMain, _ := filepath.EvalSymlinks(main)
	gotReal, _ := filepath.EvalSymlinks(loc.Path)
	if gotReal != wantMain {
		t.Fatalf("worktree 应归并到主仓: got %s, want %s", gotReal, wantMain)
	}
}

// TestRegisterProjectRejectsOriginMismatch 验证「路径敲错但恰好指到另一个真实
// 仓库」被拒——这是自动化最容易造出的脏登记（spec §3.1）。
func TestRegisterProjectRejectsOriginMismatch(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	repo := initGitRepoWithOrigin(t, "git@github.com:xushixin/tk.git")

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:xushixin/handoff.git", Path: repo})
	if !errors.Is(err, ErrProjectOriginMismatch) {
		t.Fatalf("err = %v, want errors.Is(..., ErrProjectOriginMismatch)", err)
	}
	for _, want := range []string{"tk", "handoff"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文应同时给出两边的 origin，%q 未含 %q", err.Error(), want)
		}
	}
}

// TestRegisterProjectRejectsNoOrigin 验证没有 origin 的仓库拒绝登记：
// 它算不出 project_id，登记进来只会是一条永远引用不到的死记录。
func TestRegisterProjectRejectsNoOrigin(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	repo := initGitRepo(t) // 刻意不加 origin
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:xushixin/handoff.git", Path: repo})
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want errors.Is(..., ErrRepoUnusable)", err)
	}
}

// TestRegisterProjectDuplicateProject 验证同一项目重复登记被拒，且报文指向已有位置。
func TestRegisterProjectDuplicateProject(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	first := initGitRepoWithOrigin(t, origin)
	if _, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: first}); err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	second := initGitRepoWithOrigin(t, origin)
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: second})
	if !errors.Is(err, ErrProjectAlreadyExists) {
		t.Fatalf("err = %v, want errors.Is(..., ErrProjectAlreadyExists)", err)
	}
	if !strings.Contains(err.Error(), first) {
		t.Errorf("报文 %q 应指向已有位置 %s", err.Error(), first)
	}
}

// TestRegisterProjectNameCollisionFallsBack 验证不同项目撞名字时落到 name-2。
func TestRegisterProjectNameCollisionFallsBack(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	a := initGitRepoWithOrigin(t, "git@github.com:xushixin/handoff.git")
	if _, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:xushixin/handoff.git", Path: a}); err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	b := initGitRepoWithOrigin(t, "git@github.com:other/handoff.git")
	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:other/handoff.git", Path: b})
	if err != nil {
		t.Fatalf("同名不同项目登记: %v", err)
	}
	if loc.Name != "handoff-2" {
		t.Fatalf("名字应退让为 handoff-2，got %q", loc.Name)
	}
}

// TestRegisterProjectClonesWhenNoPath 验证不给 path 时 clone 到 repo_root/<名字>。
// 用本地目录当 clone 源，不依赖网络。
func TestRegisterProjectClonesWhenNoPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)
	root := filepath.Join(t.TempDir(), "repos")
	m.cfg.RepoRoot = root

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: src, Name: "src"})
	if err != nil {
		t.Fatalf("RegisterProject(clone): %v", err)
	}
	want := filepath.Join(root, "src")
	if loc.Path != want {
		t.Fatalf("落点 = %q, want %q", loc.Path, want)
	}
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatalf("落点应是一个克隆好的仓库: %v", err)
	}
}

// TestUnregisterProjectRejectsBusy 验证仓库仍被活跃任务占用时拒绝注销。
func TestUnregisterProjectRejectsBusy(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("登记: %v", err)
	}
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: loc.Path, State: proto.TaskStateRunning})
	if err := m.UnregisterProject(context.Background(), loc.Name); !errors.Is(err, ErrWorkdirBusy) {
		t.Fatalf("err = %v, want errors.Is(..., ErrWorkdirBusy)", err)
	}
}
```

> 该文件还需 `import "github.com/xushixin/handoff/internal/proto"`（`TestUnregisterProjectRejectsBusy` 用到 `proto.Task`）。

- [ ] **Step 6: 运行测试确认失败**

```bash
go test ./internal/agentd/ -run "TestRegisterProject|TestUnregisterProject" -v
```

Expected: 编译失败，`undefined: RegisterProjectReq` 等。

- [ ] **Step 7: 写登记层实现**

创建 `internal/agentd/projectadmin.go`：

```go
// 本文件是 agentd 侧「项目 × 本机位置」的操作层。
//
// 职责：
//   - RegisterProject：登记本机已有的一份代码，或先 clone 再登记
//   - ListProjects：列出位置，并**现场探测**每条的实际状态（漂移可见化）
//   - UnregisterProject：注销位置（只删记录，不动磁盘）
//
// 边界：
//   - 不做解析：派发时「这个请求指哪个项目」由 projectresolve.go 的纯函数决定
//   - 不做持久化细节：SQL 在 internal/store/projects.go
//   - 不算 project_id：那是 internal/projectid 的纯函数
//   - 不删磁盘上的仓库：注销只影响登记，磁盘由人自己处置
//   - clone 在**本机**执行（agentd 就跑在这台机器上），不走 ssh——
//     用的是这台机器自己的 git 凭据
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/projectid"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// ErrProjectAlreadyExists 表示位置冲突或克隆落点已被占用，映射 409。
//
// 与 ErrRepoUnusable（400）的区别：那是「请求本身有问题，改了再来」，
// 这是「当前状态与请求冲突」——和 ErrDirtyWorktree / ErrWorkdirBusy 同层级。
var ErrProjectAlreadyExists = errors.New("项目位置冲突或克隆落点已存在")

// ErrProjectOriginMismatch 表示调用方声称的项目与该路径上实际仓库的 origin 不符，映射 400。
//
// 为什么必须单列一个哨兵：这是自动化最容易造出的脏登记——路径敲错但恰好指到
// 另一个真实仓库。若并进 ErrRepoUnusable，报文就变成含糊的「仓库不可用」，
// 而人需要的是「你说的是 A，那儿实际是 B」。
var ErrProjectOriginMismatch = errors.New("路径上的仓库与请求的项目不是同一个")

// project ls 的状态取值。不落库，每次列出时现场探得。
const (
	projectStatusOK      = "有效"
	projectStatusMissing = "路径不存在"
	projectStatusNotRepo = "不是 git 仓库"
)

// nameFallbackLimit 是名字冲突时的最大退让次数（handoff-2 … handoff-50）。
//
// 为什么要有上限：退让是个循环，没有上限时一张被写坏的表能让登记请求空转。
// 50 远超「一台机器上有 50 个同末段名的不同项目」的现实量级。
const nameFallbackLimit = 50

// RegisterProjectReq 是登记一个项目位置的请求。
//
// 两种形态由 Path 是否为空决定：
//   - Path 非空：这台机器上已经有一份，用它（agentd 现读它的 origin 校验一致）
//   - Path 为空：由本机 clone 到 cfg.RepoRoot/<Name>
//
// 为什么没有 Clone 布尔位：形态已被 Path 完全决定，多一个布尔位只会多出
// 一组无意义的非法组合。
//
// Name 可省，此时由 OriginURL 末段派生；它只是人可读引用，不参与身份判定。
type RegisterProjectReq struct {
	OriginURL string
	Name      string
	Path      string
}

// projectOriginURL 读取仓库的 origin 地址。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - repo: 仓库路径
//
// 返回：
//   - origin 地址；仓库不可用或没有 origin 时返回包装 ErrRepoUnusable 的错误
//
// 注意：
//   - 没有 origin 的仓库拒绝登记：project_id 由 origin 派生，没有 origin 就
//     算不出身份，登记进来只会是一条永远引用不到的死记录
func projectOriginURL(ctx context.Context, repo string) (string, error) {
	out, stderr, err := gitRun(ctx, repo, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("%w: 读取 %s 的 origin 失败: %s: %v",
			ErrRepoUnusable, repo, strings.TrimSpace(stderr), err)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", fmt.Errorf("%w: 仓库 %s 没有配置 origin remote", ErrRepoUnusable, repo)
	}
	return url, nil
}

// projectNameFromURL 从 git URL 末段派生缺省引用名（去掉 .git 后缀）。
//
// 例：git@github.com:xushixin/handoff.git → handoff
func projectNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// validateProjectName 校验引用名的合法性，返回包装 errBadDispatchRequest 的错误。
//
// 规则：
//   - 空名或纯空白 → 拒
//   - 名字含 / \ : → 拒：clone 落点是 repo_root/<名字>，这三个字符会让它跑到别处
//   - 名字为 . / .. 或含 .. 路径段 → 拒：会让落点逃出 repo_root
//
// 为什么必须入口拦：名字由 origin 末段派生或人工指定，没人保证它干净；
// 而它会被直接拼进文件系统路径。
func validateProjectName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: 项目名不能为空", errBadDispatchRequest)
	}
	if strings.ContainsAny(name, `/\:`) {
		return fmt.Errorf("%w: 项目名 %q 含路径特征字符（/ \\ :），会让克隆落点跑到 repo_root 之外",
			errBadDispatchRequest, name)
	}
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%w: 项目名 %q 含 . 或 .. 路径段，会让克隆落点逃出 repo_root",
				errBadDispatchRequest, name)
		}
	}
	return nil
}

// RegisterProject 登记一个项目位置（两种形态见 RegisterProjectReq）。
//
// 参数：
//   - ctx: 控制整组 git 调用的生命周期
//   - req: 登记请求
//
// 返回：
//   - 落库后的位置条目
//   - 错误：ErrRepoUnusable（400，路径不是仓库/无 origin/clone 失败）、
//     ErrProjectOriginMismatch（400，路径上是另一个项目）、
//     ErrProjectAlreadyExists（409，项目/名字/路径已被占用，或落点已存在）、
//     errBadDispatchRequest（400，参数缺失或名字非法）
//
// 注意：
//   - **登记在 clone 成功之后才落库**：反过来会在 clone 失败时留下一条指向
//     不存在路径的死记录
//   - clone 的落点若已存在则直接拒绝，绝不往里 clone、绝不覆盖
func (m *Manager) RegisterProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	m.log.Info("登记项目请求", "origin", req.OriginURL, "name", req.Name, "path", req.Path)
	if strings.TrimSpace(req.OriginURL) == "" {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 登记必须带 origin_url（项目身份由它派生）",
			errBadDispatchRequest)
	}
	if strings.HasPrefix(req.OriginURL, "-") {
		// git 会把以 - 开头的参数解释为选项——参数注入面，与 ErrBadBaseBranch 同源。
		return proto.ProjectLocation{}, fmt.Errorf("%w: origin_url 不允许以 - 开头", errBadDispatchRequest)
	}
	if req.Path != "" {
		return m.registerExistingProject(ctx, req)
	}
	return m.cloneAndRegisterProject(ctx, req)
}

// registerExistingProject 登记本机上已存在的一份代码。
func (m *Manager) registerExistingProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	if err := EnsureRepoUsable(ctx, req.Path); err != nil {
		m.log.Warn("登记被拒：路径不是可用的 git 仓库", "path", req.Path, "cause", err)
		return proto.ProjectLocation{}, err
	}
	// 归并主工作树：位置表一个项目只允许一行，而 linked worktree 与主仓
	// origin 相同、project_id 相同（spec §5）。
	root, err := MainWorktreeRoot(ctx, req.Path)
	if err != nil {
		m.log.Warn("登记被拒：归并主工作树失败", "path", req.Path, "cause", err)
		return proto.ProjectLocation{}, err
	}
	// origin 由 agentd 在本机现读，而不是采信调用方上送的值：登记的是这个
	// 路径上真实存在的仓库，它的 origin 才是权威。
	actual, err := projectOriginURL(ctx, root)
	if err != nil {
		m.log.Warn("登记被拒：读不到 origin", "path", root, "cause", err)
		return proto.ProjectLocation{}, err
	}
	// 校验一致：挡住「路径敲错但恰好指到另一个真实仓库」这种脏登记。
	if projectid.FromOrigin(actual) != projectid.FromOrigin(req.OriginURL) {
		m.log.Warn("登记被拒：路径上的仓库不是请求的项目",
			"path", root, "actual_origin", actual, "want_origin", req.OriginURL)
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: %s 上的 origin 是 %s，而请求的项目是 %s；换个路径，或去掉 --path 让本机自己 clone",
			ErrProjectOriginMismatch, root, actual, req.OriginURL)
	}
	return m.persistProject(req.Name, root, actual)
}

// cloneAndRegisterProject 先 clone 再登记。
func (m *Manager) cloneAndRegisterProject(ctx context.Context, req RegisterProjectReq) (proto.ProjectLocation, error) {
	name := req.Name
	if name == "" {
		name = projectNameFromURL(req.OriginURL)
	}
	// 校验必须早于 dest 计算：名字含 .. 时 dest=repo_root/<名字> 会逃出 repo_root，
	// 等 persistProject 再拦就晚了（落点已建/克隆已跑）。
	if err := validateProjectName(name); err != nil {
		m.log.Warn("克隆登记被拒：项目名非法", "name", name, "cause", err)
		return proto.ProjectLocation{}, err
	}
	if m.cfg.RepoRoot == "" {
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 本机未配置 repo_root，无法决定克隆落点（在 config.yaml 里配它）", errBadDispatchRequest)
	}
	dest := filepath.Join(m.cfg.RepoRoot, name)
	// 落点已存在就拒绝：往一个已有目录里 clone 要么失败要么污染它，两种都不该发生。
	if _, err := os.Stat(dest); err == nil {
		m.log.Warn("克隆被拒：落点已存在", "dest", dest)
		return proto.ProjectLocation{}, fmt.Errorf(
			"%w: 落点 %s 已存在；用 handoff project add --target <机器> --path %s 直接登记它",
			ErrProjectAlreadyExists, dest, dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 探查落点 %s: %v", ErrRepoUnusable, dest, err)
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 创建落点父目录 %s: %v", ErrRepoUnusable, parent, err)
	}
	m.log.Info("开始克隆项目", "origin", req.OriginURL, "dest", dest)
	start := time.Now()
	// gitRun 以 parent 为 cwd 执行；-- 分隔符防止 URL/路径被当成选项。
	if _, stderr, err := gitRun(ctx, parent, "clone", "--", req.OriginURL, dest); err != nil {
		m.log.Error("克隆项目失败", "origin", req.OriginURL, "dest", dest,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return proto.ProjectLocation{}, fmt.Errorf("%w: 克隆 %s 到 %s 失败: %s: %v",
			ErrRepoUnusable, req.OriginURL, dest, strings.TrimSpace(stderr), err)
	}
	m.log.Info("克隆项目完成", "origin", req.OriginURL, "dest", dest,
		"elapsed_ms", time.Since(start).Milliseconds())
	return m.persistProject(name, dest, req.OriginURL)
}

// persistProject 把一条位置落库：算 project_id、定引用名、翻译冲突哨兵。
func (m *Manager) persistProject(name, path, origin string) (proto.ProjectLocation, error) {
	pid := projectid.FromOrigin(origin)
	if pid == "" {
		m.log.Warn("登记落库被拒：origin 算不出 project_id", "origin", origin, "path", path)
		return proto.ProjectLocation{}, fmt.Errorf("%w: origin %q 归一化后为空，算不出项目身份",
			errBadDispatchRequest, origin)
	}
	entries, err := m.st.ListProjectLocations()
	if err != nil {
		m.log.Error("登记落库前读位置表失败", "cause", err)
		return proto.ProjectLocation{}, err
	}
	// 同一项目已有位置时直接拒，并把已有路径写进报文——比等主键冲突再报
	// 一句「已存在」有用得多（ADR-0008：一台机器一个项目只能有一个位置）。
	for _, e := range entries {
		if e.ProjectID == pid {
			m.log.Warn("登记被拒：该项目在本机已有位置",
				"project_id", pid, "existing", e.Path, "requested", path)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 项目 %s 在本机已登记于 %s；要换位置先 handoff project rm %s",
				ErrProjectAlreadyExists, e.Name, e.Path, e.Name)
		}
	}
	if name == "" {
		name = projectNameFromURL(origin)
	}
	if err := validateProjectName(name); err != nil {
		m.log.Warn("登记落库被拒：项目名非法", "name", name, "cause", err)
		return proto.ProjectLocation{}, err
	}
	name, err = uniqueProjectName(name, entries)
	if err != nil {
		m.log.Warn("登记落库被拒：名字退让次数用尽", "name", name, "cause", err)
		return proto.ProjectLocation{}, err
	}
	loc := proto.ProjectLocation{
		ProjectID: pid, Name: name, Path: filepath.Clean(path),
		OriginURL: origin, CreatedAt: time.Now(),
	}
	if err := m.st.CreateProjectLocation(&loc); err != nil {
		if errors.Is(err, store.ErrProjectDuplicate) {
			m.log.Warn("登记被拒：项目/名字/路径已被占用",
				"project_id", pid, "name", name, "path", loc.Path, "cause", err)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 项目 %s、名字 %q 或路径 %s 已被登记（handoff project ls 查看）",
				ErrProjectAlreadyExists, pid, name, loc.Path)
		}
		m.log.Error("登记落库失败", "name", name, "path", loc.Path, "cause", err)
		return proto.ProjectLocation{}, err
	}
	m.log.Info("项目位置登记完成",
		"project_id", pid, "name", name, "path", loc.Path, "origin", origin)
	loc.Status = projectStatusOK
	return loc, nil
}

// uniqueProjectName 在 base 已被占用时退让为 base-2、base-3……
//
// 参数：
//   - base: 期望的引用名
//   - entries: 本机现有位置
//
// 返回：
//   - 未被占用的名字；退让 nameFallbackLimit 次仍冲突时返回 errBadDispatchRequest
//
// 注意：
//   - 只在**不同项目**撞名时才会走到这里：同项目重复登记已在 persistProject
//     前段按 project_id 拒掉了
func uniqueProjectName(base string, entries []proto.ProjectLocation) (string, error) {
	taken := make(map[string]bool, len(entries))
	for _, e := range entries {
		taken[e.Name] = true
	}
	if !taken[base] {
		return base, nil
	}
	for i := 2; i <= nameFallbackLimit; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: 名字 %s 及其 -2..-%d 变体全部被占用，请用 [名字] 参数显式指定",
		errBadDispatchRequest, base, nameFallbackLimit)
}

// ListProjects 列出本机全部项目位置，并现场探测每条的实际状态。
//
// 参数：
//   - ctx: 控制探测用的 git 调用生命周期
//
// 返回：
//   - 位置列表（Status 已填充）；查库失败时返回错误
//
// 注意：
//   - 探测是登记与文件系统漂移的可见化手段。探测失败不影响列出——
//     状态本身就是要报给人看的结果，不是错误
func (m *Manager) ListProjects(ctx context.Context) ([]proto.ProjectLocation, error) {
	locs, err := m.st.ListProjectLocations()
	if err != nil {
		m.log.Error("列出项目位置失败", "cause", err)
		return nil, err
	}
	for i := range locs {
		locs[i].Status = probeProjectStatus(ctx, locs[i].Path)
	}
	m.log.Info("列出项目位置", "count", len(locs))
	return locs, nil
}

// probeProjectStatus 探测一条位置指向的路径当前是什么状态。
func probeProjectStatus(ctx context.Context, path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return projectStatusMissing
	}
	if err := EnsureRepoUsable(ctx, path); err != nil {
		return projectStatusNotRepo
	}
	return projectStatusOK
}

// UnregisterProject 注销一条项目位置。
//
// 参数：
//   - ctx: 上下文（当前实现不发起 git 调用，保留以对齐其余操作签名）
//   - name: 项目引用名
//
// 返回：
//   - 错误：位置不存在时 store.ErrNotFound（404）；路径被活跃任务占用时
//     ErrWorkdirBusy（409）
//
// 注意：
//   - **只删登记，永不删磁盘上的仓库**。磁盘上那份是不是还要留，由人自己决定
func (m *Manager) UnregisterProject(ctx context.Context, name string) error {
	loc, err := m.st.GetProjectLocationByName(name)
	if err != nil {
		m.log.Warn("注销项目失败：位置不存在", "name", name, "cause", err)
		return err
	}
	tasks, err := m.st.ActiveTasksByRepoPath(loc.Path)
	if err != nil {
		m.log.Error("注销项目前查活跃任务失败", "name", name, "path", loc.Path, "cause", err)
		return err
	}
	if len(tasks) > 0 {
		ids := make([]string, 0, len(tasks))
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		m.log.Warn("注销项目被拒：仓库被活跃任务占用",
			"name", name, "path", loc.Path, "tasks", strings.Join(ids, ","))
		return fmt.Errorf("%w: 项目 %s（%s）上还有 %d 个活跃任务（%s）；先 done 或 stop 它们",
			ErrWorkdirBusy, name, loc.Path, len(tasks), strings.Join(ids, ", "))
	}
	if err := m.st.DeleteProjectLocation(name); err != nil {
		m.log.Error("注销项目落库失败", "name", name, "cause", err)
		return err
	}
	m.log.Info("项目位置已注销（磁盘仓库未动）", "name", name, "path", loc.Path)
	return nil
}
```

- [ ] **Step 8: 运行测试确认通过**

```bash
go test ./internal/agentd/ -run "TestResolveProject|TestRegisterProject|TestUnregisterProject" -v
```

Expected: 全部 PASS。若 `TestRegisterProjectClonesWhenNoPath` 因 `m.cfg` 为 nil 而 panic，说明 `newTestManagerWithAds` 传的 cfg 未被 Manager 持有——检查 `NewManager` 是否把 `cfg` 存进 `m.cfg`（它是存的，见 `manager.go`），必要时改成先构造 cfg 再建 manager。

- [ ] **Step 9: 加关键节点日志**

对照检查（缺则补）：

- 入口：`RegisterProject` 进入时 Info 一行，带 `origin` / `name` / `path`
- 外部调用前后：clone **前** Info（`origin`+`dest`）、clone **后** Info 带 `elapsed_ms`；失败时 Error 带 `elapsed_ms` + git stderr 截断 + cause
  —— 耗时是 spec §14「风险三：自动 clone 在慢网络上耗时长」要求的可见化手段，**成功和失败两条都要有**
- 每个拒绝分支各一条 Warn，带足以判因的键：路径不可用 / 归并失败 / 读不到 origin / origin 不符（`actual_origin` 与 `want_origin` 两个都要打）/ 落点已存在 / 项目已有位置 / 名字非法 / 名字退让用尽 / 落库冲突
- 成功路径：`persistProject` 末尾 Info「项目位置登记完成」带 `project_id`/`name`/`path`/`origin`——**这是「登记表终于有流量了」的唯一证据**，B62 的整个病灶就是这条日志从来没被打过
- `ListProjects` 成功时 Info 带 `count`；`UnregisterProject` 成功时 Info 带 `name`/`path`
- `resolveProject` 的每个分支（两条命中 Info、三条拒绝 Warn）已在 Step 3 写入

- [ ] **Step 10: 加意图注释**

确认：两个新文件的文件头（职责 + 边界，`projectresolve.go` 要写明「不接受任何路径入参」及其理由）、三个哨兵各自的「与谁的区别 / 为什么单列」、`RegisterProjectReq` 的「为什么没有 Clone 布尔位」、`nameFallbackLimit` 的「为什么要有上限」、归并主工作树与 origin 校验两处的「为什么」、`persistProject` 里「先按 project_id 拒再落库」的理由。

- [ ] **Step 11: 提交**

```bash
go build ./... && go vet ./... && go test ./internal/agentd/
git add internal/agentd/projectresolve.go internal/agentd/projectresolve_test.go internal/agentd/projectadmin.go internal/agentd/projectadmin_test.go
git commit -m "feat(agentd): 加项目登记层与解析层（尚未接线）"
```

---

## Task 5: 全量切换与旧代码删除

这是本计划唯一的 cutover 任务：类型名、表名、路由、请求字段同时变，中间态没有意义。逻辑已在 Task 1–4 各自测过，本任务是接线 + 删除 + 改测试。

**Files:**
- Modify: `internal/store/store.go`（建表清单去掉 `repos`；加 `repos` → `project_locations` 的迁移）
- Modify: `internal/store/projects.go`（加迁移函数）
- Modify: `internal/store/projects_test.go`（加迁移用例）
- Modify: `internal/proto/proto.go`（删 `Repo` 类型）
- Modify: `internal/agentd/manager.go`（`DispatchReq` 换字段；`Dispatch` 改调 `resolveProject`）
- Modify: `internal/agentd/server.go`（路由 `/api/repos` → `/api/projects`；`dispatchRequest` 换字段；错误映射换哨兵）
- Modify: `internal/client/client.go`（`DispatchOpts` 换字段；`Repo*` → `Project*`）
- Modify: `cmd/dispatch.go`（删 `--repo`，加 `--project`）
- Create: `cmd/project.go`；Delete: `cmd/repo.go`、`cmd/repo_test.go`；Create: `cmd/project_test.go`
- Delete: `internal/agentd/reporegistry.go`、`reporegistry_test.go`、`repoadmin.go`、`repoadmin_test.go`、`internal/store/repos.go`、`internal/store/repos_test.go`
- Modify: `internal/agentd/manager_test.go`、`workspace_test.go`、`approver_test.go`、`integration_test.go`（加登记助手，改 56 处 `Repo:` 赋值）

**Interfaces:**
- Consumes: Task 1–4 的全部产出
- Produces:
  - `store.migrateReposToProjectLocations(db *sql.DB) error`（包内，`Open` 里调用）
  - `agentd.DispatchReq{ProjectID, ProjectName string; ...}`（`Repo` 与 `OriginURL` 删除）
  - `client.DispatchOpts{ProjectID, ProjectName string; ...}`（`Repo` 与 `OriginURL` 删除）
  - `client.ProjectAddOpts{OriginURL, Name, Path string}`
  - `(*client.Client).ProjectAdd(ctx, ProjectAddOpts) (*proto.ProjectLocation, error)`
  - `(*client.Client).ProjectList(ctx) ([]proto.ProjectLocation, error)`
  - `(*client.Client).ProjectRemove(ctx, name string) error`
  - `agentd.registerTestProject(t *testing.T, m *Manager, repo string) string`（测试助手，返回 project_id）

- [ ] **Step 1: 写迁移的失败测试**

在 `internal/store/projects_test.go` 追加：

```go
// TestMigrateReposToProjectLocations 验证旧 repos 表迁入新表：算出 project_id、
// 同 origin 多行保留 created_at 最早的一条、迁完 DROP 掉旧表。
func TestMigrateReposToProjectLocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	// 先手工造一个「旧库」：建 repos 表并塞三行，其中两行同 origin。
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := old.Exec(`CREATE TABLE repos (
  name TEXT PRIMARY KEY, path TEXT NOT NULL UNIQUE,
  origin_url TEXT NOT NULL, created_at TIMESTAMP NOT NULL)`); err != nil {
		t.Fatalf("建旧表: %v", err)
	}
	rows := []struct{ name, path, origin, created string }{
		{"handoff", "/w/handoff", "git@github.com:xushixin/handoff.git", "2026-01-01T00:00:00Z"},
		{"handoff-wt", "/w/handoff-wt", "https://github.com/xushixin/handoff", "2026-02-01T00:00:00Z"},
		{"tk", "/w/tk", "git@github.com:xushixin/tk.git", "2026-01-15T00:00:00Z"},
	}
	for _, r := range rows {
		if _, err := old.Exec(`INSERT INTO repos VALUES (?, ?, ?, ?)`,
			r.name, r.path, r.origin, r.created); err != nil {
			t.Fatalf("塞旧行 %s: %v", r.name, err)
		}
	}
	old.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open（应顺带跑完迁移）: %v", err)
	}
	defer st.Close()

	locs, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("同 origin 的两行应折叠成一行，共 2 行，got %d: %+v", len(locs), locs)
	}
	got, err := st.GetProjectLocationByName("handoff")
	if err != nil {
		t.Fatalf("最早的那条应被保留: %v", err)
	}
	if got.Path != "/w/handoff" {
		t.Fatalf("保留的应是 created_at 最早的一条，got %q", got.Path)
	}

	// 旧表必须已被 DROP，且第二次 Open 是无操作（幂等）。
	var n int
	if err := st.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='repos'`).Scan(&n); err != nil {
		t.Fatalf("查 sqlite_master: %v", err)
	}
	if n != 0 {
		t.Fatal("迁移完成后 repos 表应被 DROP")
	}
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("第二次 Open 应无操作: %v", err)
	}
	defer st2.Close()
	locs2, err := st2.ListProjectLocations()
	if err != nil || len(locs2) != 2 {
		t.Fatalf("第二次 Open 不应改变数据: locs=%d err=%v", len(locs2), err)
	}
}
```

> 该文件需追加 `import ("database/sql"; _ "modernc.org/sqlite")`。

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/store/ -run TestMigrateRepos -v
```

Expected: FAIL —— `Open` 没跑迁移，`ListProjectLocations` 返回 0 行。

- [ ] **Step 3: 写迁移实现**

在 `internal/store/projects.go` 追加：

```go
// migrateReposToProjectLocations 把旧 repos 表逐行迁入 project_locations，随后 DROP 掉它。
//
// 参数：
//   - db: 已打开的数据库句柄（Open 内部调用，此时新表已建好）
//
// 返回：
//   - 错误：查/写/DROP 失败时返回；旧表不存在时直接返回 nil（新库与二次调用都走这条）
//
// 注意：
//   - 按 created_at 升序遍历，**同一个 project_id 保留最早的一条**：ADR-0008
//     只允许一个位置，多出来的（多半是同一仓库的 worktree 各登了一条）丢弃并
//     Warn 出 name/path/origin 三项完整信息，人照着 handoff project add --path 自己补
//   - 路径做 Abs+Clean：新表的 path UNIQUE 约束要靠绝对路径才有意义
//   - 迁移不探测文件系统：目录已被移走的行照样迁入，下一次派发在 EnsureRepoUsable
//     处报「路径不存在」，处置是 project rm 后重新 add（spec §14 风险一）
//   - 幂等靠 DROP：跑完旧表就没了，第二次调用直接返回
func migrateReposToProjectLocations(db *sql.DB) error {
	ctx := context.Background()
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='repos'`).Scan(&n); err != nil {
		return fmt.Errorf("探查旧 repos 表: %w", err)
	}
	if n == 0 {
		return nil // 新库或已迁过，无操作
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name, path, origin_url, created_at FROM repos ORDER BY created_at ASC`)
	if err != nil {
		return fmt.Errorf("读旧 repos 表: %w", err)
	}
	type oldRepo struct{ name, path, origin, createdAt string }
	var olds []oldRepo
	for rows.Next() {
		var r oldRepo
		if err := rows.Scan(&r.name, &r.path, &r.origin, &r.createdAt); err != nil {
			rows.Close()
			return fmt.Errorf("读旧 repos 行: %w", err)
		}
		olds = append(olds, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("遍历旧 repos 表: %w", err)
	}
	rows.Close()

	seen := make(map[string]oldRepo, len(olds))
	migrated, skipped := 0, 0
	for _, r := range olds {
		pid := projectid.FromOrigin(r.origin)
		if pid == "" {
			log().Warn("迁移跳过：origin 算不出 project_id（人工处置：handoff project add）",
				"name", r.name, "path", r.path, "origin", r.origin)
			skipped++
			continue
		}
		if prev, dup := seen[pid]; dup {
			log().Warn("迁移跳过：同一项目已有更早的登记，本条丢弃（人工处置：handoff project add --path <该路径>）",
				"name", r.name, "path", r.path, "origin", r.origin,
				"kept_name", prev.name, "kept_path", prev.path)
			skipped++
			continue
		}
		abs, err := filepath.Abs(r.path)
		if err != nil {
			log().Warn("迁移跳过：路径无法绝对化",
				"name", r.name, "path", r.path, "origin", r.origin, "cause", err)
			skipped++
			continue
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO project_locations (project_id, name, path, origin_url, created_at)
VALUES (?, ?, ?, ?, ?)`,
			pid, r.name, filepath.Clean(abs), r.origin, r.createdAt); err != nil {
			return fmt.Errorf("迁移登记 %s 到 project_locations: %w", r.name, err)
		}
		seen[pid] = r
		migrated++
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE repos`); err != nil {
		return fmt.Errorf("迁移后删除旧 repos 表: %w", err)
	}
	log().Info("旧仓库登记已迁入项目位置表", "migrated", migrated, "skipped", skipped)
	return nil
}
```

> `projects.go` 需追加 `import ("path/filepath"; "github.com/xushixin/handoff/internal/projectid")`。
> `log()` 是 `internal/store` 的包级日志入口——若该包没有，用 `store.go` 里既有的写法对齐；这是**迁移**，不是叶子 CRUD，必须留下可读的运行记录。

在 `internal/store/store.go` 的 `Open` 中：

1. 从建表清单里**删掉** `repos` 的 DDL（新库不再创建它）。
2. 在既有的 `ALTER TABLE` 迁移块之后追加：

```go
	// 迁移（B62）：旧 repos 表 → project_locations，随后 DROP 旧表。
	// 放在建表之后：迁移要往新表里写。幂等由「旧表已 DROP 则无操作」保证。
	if err := migrateReposToProjectLocations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移 repos → project_locations: %w", err)
	}
```

- [ ] **Step 4: 运行迁移测试确认通过**

```bash
go test ./internal/store/ -run TestMigrateRepos -v
```

Expected: PASS。（此时 `repos_test.go` 会红——它下一步就删。）

- [ ] **Step 5: 删除旧 store 与旧 proto 类型**

```bash
git rm internal/store/repos.go internal/store/repos_test.go
```

在 `internal/proto/proto.go` 里删除 `Repo` 类型及其注释块。

此刻 `go build ./...` 会红（agentd 还在用旧类型），Step 6–10 把它修绿。

- [ ] **Step 6: 切换 agentd 的解析与登记接线**

```bash
git rm internal/agentd/reporegistry.go internal/agentd/reporegistry_test.go \
       internal/agentd/repoadmin.go internal/agentd/repoadmin_test.go
```

在 `internal/agentd/manager.go`：

1. `DispatchReq` 中删除 `Repo` 与 `OriginURL` 两个字段，替换为：

```go
	// ProjectID 是项目身份（sha256(归一化 origin) 前 16 位），由调用方离线算出。
	// 与 ProjectName 二选一，都空时 400；同时给出时以 ProjectID 为准。
	ProjectID string
	// ProjectName 是项目的人可读引用，仅服务 --project <名字> 与 Web 控制台
	//（它没有 cwd，从项目树里选）。
	ProjectName string
```

2. `Dispatch` 开头的日志行把 `"repo", req.Repo` 换成 `"project_id", req.ProjectID, "project_name", req.ProjectName`（进入与失败两处）。

3. 解析块替换为：

```go
	// B62：项目解析。放在最前面：后面所有前置校验（仓库可用性、工作目录占用、
	// 基线决议）都要拿到本机路径才有意义。它同时是「必须先登记才能派发」这条
	// 不变式的唯一执行点——本机 CLI 收到 ErrProjectNotRegistered 会自动补登记
	// 后重发，服务端这边不做任何降级。
	entries, err := m.st.ListProjectLocations()
	if err != nil {
		m.log.Error("dispatch 前置：读取项目位置失败", "cause", err)
		return nil, err
	}
	loc, err := resolveProject(req.ProjectID, req.ProjectName, entries)
	if err != nil {
		return nil, err
	}
	// repoPath 是本次派发的工作仓库，从此刻起全部前置校验都用它。
	// 它由**本机查表**得出，调用方无从指定——这正是 B62 要立的规矩。
	repoPath := loc.Path
	m.log.Info("dispatch 项目已解析",
		"project_id", loc.ProjectID, "name", loc.Name, "path", repoPath)
```

4. 把该块之后所有 `req.Repo` 的引用改成 `repoPath`（共 10 处，位于原 `manager.go:430/432/483/493/504/517/520/527/545/566`）。校验行改为：

```go
	if repoPath == "" || (req.PlanB64 == "" && req.Prompt == "") {
		return nil, fmt.Errorf("%w: repo_path=%q plan_b64 长度=%d prompt 长度=%d",
			errBadDispatchRequest, repoPath, len(req.PlanB64), len(req.Prompt))
	}
```

- [ ] **Step 7: 切换 server 的路由、请求体与错误映射**

在 `internal/agentd/server.go`：

1. 路由三条改名，处理器同步改名：

```go
	mux.HandleFunc("POST /api/projects", s.handleProjectAdd)
	mux.HandleFunc("GET /api/projects", s.handleProjectList)
	mux.HandleFunc("DELETE /api/projects/{name}", s.handleProjectRemove)
```

2. `dispatchRequest` 中删除 `Repo` 与 `OriginURL` 字段，替换为：

```go
	// project_id 与 project_name 二选一。**请求体里没有任何路径字段**：
	// 「代码在这台机器的哪个目录」是执行机自己的私事，调用方不该描述它（B62）。
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
```

3. `handleDispatch` 里 `Repo: req.Repo` / `OriginURL: req.OriginURL` 换成 `ProjectID: req.ProjectID, ProjectName: req.ProjectName`；`s.writeDispatchError(w, req.Repo, err)` 换成 `s.writeDispatchError(w, req.ProjectID, err)`；错误报文的解析提示改为 `"请求体必须是 JSON {project_id, plan_b64, ...}"`。

4. `writeDispatchError` 的形参改名 `repo` → `projectRef`，全部日志键 `"repo"` 改 `"project"`；删除 `ErrRepoAmbiguous` 分支，`ErrRepoNotRegistered` 分支换成：

```go
	case errors.Is(err, ErrProjectNotRegistered):
		s.log.Warn("dispatch 被拒：项目未登记", "project", projectRef, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
```

并把该函数 doc 注释里对应的两行改写成新语义（未登记 → 400，报文自带本机已登记清单；本机 CLI 会据此自动补登记后重发）。

5. `repoAddRequest` 换成：

```go
// projectAddRequest 是 POST /api/projects 的请求体。
//
// 两种形态由 path 是否为空决定：给了 path 就是「这台机器上已经有一份，用它」
//（agentd 会现读它的 origin 校验一致）；没给就是「你自己 clone 到 repo_root/<name>」。
type projectAddRequest struct {
	OriginURL string `json:"origin_url"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}
```

6. 三个处理器改名并改调 `RegisterProject` / `ListProjects` / `UnregisterProject`，`[]proto.Repo{}` 空切片兜底改成 `[]proto.ProjectLocation{}`；`writeRepoError` 改名 `writeProjectError`，`ErrRepoAlreadyExists` 分支换 `ErrProjectAlreadyExists`，并新增：

```go
	case errors.Is(err, ErrProjectOriginMismatch):
		s.log.Warn("项目登记被拒：路径上是另一个项目", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
```

- [ ] **Step 8: 切换 client**

在 `internal/client/client.go`：

1. `DispatchOpts` 删 `Repo` 与 `OriginURL`，加：

```go
	// ProjectID 是项目身份，由 CLI 从 cwd 的 origin 离线算出；与 ProjectName 二选一。
	ProjectID string
	// ProjectName 是 --project <名字> 的取值，仅在 cwd 不是目标项目时使用。
	ProjectName string
```

2. `Dispatch` 的 body：`"repo": opts.Repo` 与 `"origin_url": opts.OriginURL` 换成
   `"project_id": opts.ProjectID, "project_name": opts.ProjectName`。

3. `RepoAddOpts` → `ProjectAddOpts{OriginURL, Name, Path string}`；`RepoAdd`/`RepoList`/`RepoRemove` → `ProjectAdd`/`ProjectList`/`ProjectRemove`，端点改 `/api/projects`，返回类型改 `*proto.ProjectLocation` / `[]proto.ProjectLocation`，body 改 `{"origin_url", "name", "path"}`。doc 注释同步改写（含「路径上是另一个项目返回 400」这条新错误）。

- [ ] **Step 9: `cmd/repo.go` → `cmd/project.go`**

```bash
git rm cmd/repo.go cmd/repo_test.go
```

创建 `cmd/project.go`：

```go
// 本文件实现 handoff project 子命令族：把一个项目登记到本机与（可选的）一台
// 远程开发机上，并维护「项目 × 机器」的位置表。
//
// 职责：
//   - project add：把 cwd 登记为本机位置；--target 时一并登记到那台机器
//   - project ls：列出位置，并显示每条的实际状态（登记与磁盘漂移时看得见）
//   - project rm：注销位置
//
// 边界：
//   - 不自己 ssh、不自己 clone：clone 由目标机上的 agentd 执行，用它自己的 git 凭据
//   - 不删磁盘上的仓库：rm 只删登记
//   - 不决定「项目在那台机器的哪个目录」：远程落点由那台机器的 repo_root 决定，
//     本机一个远程细节都不需要知道（spec §6.2）
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/client"
)

// projectAddPath 是 --path：目标机上已有的那份代码的路径（省略则让它自己 clone）。
var projectAddPath string

// localOriginURL 读当前目录仓库的 origin 地址；不是 git 仓库或没有 origin 时返回空串。
//
// 注意：取的是 **cwd** 的信息，因此 cwd 必须在你要登记的那个项目里。
func localOriginURL() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// localProjectRoot 返回 cwd 所属项目在本机的位置（已归并到主工作树）。
//
// 返回：
//   - 主工作树根目录的绝对路径
//   - 错误：cwd 不是 git 仓库时返回可读提示
//
// 为什么归并：位置表一个项目只允许一行，而本仓库有十几个 linked worktree
//（spec §5）。归并算法与 agentd 侧共用同一个实现，绝不在这里复制一份。
func localProjectRoot(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("取当前目录: %w", err)
	}
	return agentd.MainWorktreeRoot(ctx, cwd)
}

// projectCmd 是 project 子命令族的父命令。
var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "管理项目位置（登记到本机与开发机、列出、注销）",
}

// projectAddCmd 登记一个项目。
//
// 使用方式：
//
//	handoff project add [名字]                                       # 把 cwd 登记为本机位置
//	handoff project add [名字] --target devbox                       # 本机与 devbox 一起登记，devbox 自动 clone
//	handoff project add [名字] --target devbox --path /root/work/x   # 同上，但 devbox 上已有一份
var projectAddCmd = &cobra.Command{
	Use:   "add [名字]",
	Short: "把当前项目登记到本机（--target 时一并登记到那台开发机）",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		origin := localOriginURL()
		if origin == "" {
			return fmt.Errorf("当前目录不是 git 仓库（或没有 origin）：项目身份由 origin 派生，请在项目目录内执行")
		}
		root, err := localProjectRoot(cmd.Context())
		if err != nil {
			return err
		}
		return registerProjectBothHops(cmd, origin, name, root, projectAddPath)
	},
}

// registerProjectBothHops 执行「本机 + 可选目标机」两跳登记。
//
// 参数：
//   - cmd: 用于取 context 与输出流
//   - origin: cwd 的 origin（项目身份来源）
//   - name: 人可读引用名（可空，由 agentd 从 origin 末段派生）
//   - localPath: 本机位置（cwd 的主工作树）
//   - remotePath: 目标机上已有的路径（可空，空则让那台机器自己 clone）
//
// 返回：
//   - 错误：任一跳失败即返回；**不回滚另一跳**（登记是幂等的，重跑即可）
//
// 注意：
//   - --target 的语义是「本机与那台机器**一起**登记」，不是「只登记那台机器」：
//     项目身份是从 cwd 算的，本机位置已知且免费，刻意不登它只会让本机项目树缺一行
//   - 本机永远不 clone（它已经有 cwd 这份了）；远程不给 path 时由它自己 clone
func registerProjectBothHops(cmd *cobra.Command, origin, name, localPath, remotePath string) error {
	localAddr, localToken, err := LocalEndpoint()
	if err != nil {
		return err
	}
	local, err := client.New(localAddr, localToken).ProjectAdd(cmd.Context(), client.ProjectAddOpts{
		OriginURL: origin, Name: name, Path: localPath,
	})
	if err != nil {
		return fmt.Errorf("登记到本机: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "本机 %s → %s\n", local.Name, local.Path)
	if targetName == "" {
		return nil
	}
	addr, token, err := TargetEndpoint()
	if err != nil {
		return err
	}
	if remotePath == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "正在让 %s 克隆 %s（首次可能较慢）…\n", targetName, origin)
	}
	remote, err := client.New(addr, token).ProjectAdd(cmd.Context(), client.ProjectAddOpts{
		OriginURL: origin, Name: local.Name, Path: remotePath,
	})
	if err != nil {
		return fmt.Errorf("登记到 %s: %w", targetName, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s → %s\n", targetName, remote.Name, remote.Path)
	return nil
}

// projectLsCmd 列出位置。
var projectLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "列出机器上的项目位置（含实际状态）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		locs, err := client.New(addr, token).ProjectList(cmd.Context())
		if err != nil {
			return err
		}
		if len(locs) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "（这台机器上还没有任何项目，在项目目录里执行 handoff project add）")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "名字\t路径\t状态\tproject_id\torigin")
		for _, l := range locs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.Name, l.Path, l.Status, l.ProjectID, l.OriginURL)
		}
		return tw.Flush()
	},
}

// projectRmCmd 注销一条位置。
var projectRmCmd = &cobra.Command{
	Use:   "rm <名字>",
	Short: "注销一条项目位置（只删登记，不删磁盘上的代码）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).ProjectRemove(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已注销 %s（磁盘上的代码未动）\n", args[0])
		return nil
	},
}

func init() {
	projectAddCmd.Flags().StringVar(&projectAddPath, "path", "",
		"目标机上已有的那份代码的路径（仅与 --target 连用；省略则由那台机器 clone 到它的 repo_root/<名字>）")
	projectCmd.AddCommand(projectAddCmd, projectLsCmd, projectRmCmd)
	rootCmd.AddCommand(projectCmd)
}
```

在 `cmd/root.go` 追加 `LocalEndpoint`（把 `TargetEndpoint` 的本机分支抽出来复用，两者共用同一段配置加载）：

```go
// LocalEndpoint 返回**本机** agentd 的地址与令牌，忽略 --target。
//
// 返回：
//   - addr: 本机 agentd 完整地址（含 http:// 前缀）
//   - token: 本机令牌
//   - err: 配置加载失败或本机 token 为空时返回
//
// 为什么需要它而不是复用 TargetEndpoint：登记是**两跳**（本机 + 目标机，
// spec §6.1），而 TargetEndpoint 读的是包级 targetName，指定了 --target 时
// 拿不到本机端点。两跳都要发，就必须有一个不受 --target 影响的取端点入口。
func LocalEndpoint() (addr, token string, err error) {
	saved := targetName
	targetName = ""
	defer func() { targetName = saved }()
	return TargetEndpoint()
}
```

> 这个存取包级变量的写法之所以可接受：CLI 是单次执行的单线程进程，`targetName` 由 cobra 在解析 flag 时一次性写入，之后只读。若实现时发现 `TargetEndpoint` 已被并发调用，改为把 `TargetEndpoint` 的函数体抽成 `endpointFor(name string)`，两个导出函数各传各的名字——那是更干净的形态，优先选它。

创建 `cmd/project_test.go`（覆盖本地拦截，不打网络）：

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestProjectAddRejectsNonRepo 验证 cwd 不是 git 仓库时本地就拒，报文说明原因。
// 为什么在本地拦：项目身份只依赖本机信息，多跑一次网络毫无意义。
func TestProjectAddRejectsNonRepo(t *testing.T) {
	t.Chdir(t.TempDir()) // 临时目录不是 git 仓库
	var out bytes.Buffer
	projectAddCmd.SetOut(&out)
	projectAddCmd.SetErr(&out)
	err := projectAddCmd.RunE(projectAddCmd, nil)
	if err == nil {
		t.Fatal("非 git 目录应被拒")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("报文应说明身份由 origin 派生，got %q", err.Error())
	}
}
```

- [ ] **Step 10: 切换 `cmd/dispatch.go` 的 flag（自动登记编排留到 Task 7）**

在 `cmd/dispatch.go`：

1. 包级变量 `dispatchRepo` 改名 `dispatchProject`。
2. flag 定义换成：

```go
	dispatchCmd.Flags().StringVar(&dispatchProject, "project", "",
		"跨项目派发时指定项目名（省略则由当前目录自动识别；用 handoff project ls 查看有哪些）")
```

3. 在读 plan 之后、取端点之前插入项目识别：

```go
			// B62：派发的项目由 cwd 识别，路径不再出现在命令上。
			// 这一条在 CLI 侧判是因为它只依赖本机信息，多跑一次网络毫无意义。
			projectID := ""
			if dispatchProject == "" {
				origin := localOriginURL()
				if origin == "" {
					return fmt.Errorf("派发的项目由当前目录识别：当前目录不是 git 仓库（或没有 origin）；" +
						"请在项目目录内执行，或用 --project <名字> 指定")
				}
				projectID = projectid.FromOrigin(origin)
			}
```

4. `client.DispatchOpts` 里 `Repo: dispatchRepo` 与 `OriginURL: localOriginURL()` 换成
   `ProjectID: projectID, ProjectName: dispatchProject`。
5. 文件头注释与 `--no-sync-check` 的 flag 说明里凡提到 `--repo` 的措辞，改成「cwd 与目标项目不是同一个仓库时用」。
6. 追加 import `"github.com/xushixin/handoff/internal/projectid"`。

- [ ] **Step 11: 改造 agentd 既有测试**

在 `internal/agentd/manager_test.go` 追加助手：

```go
// registerTestProject 把 repo 登记成一个项目位置，返回它的 project_id。
//
// 为什么每个派发用例都要先登记：B62 之后「必须先登记才能派发」是服务端单方面
// 保证的不变式，**不给测试开旁路**——开了旁路，测试就测不到真实调用路径。
//
// 参数：
//   - m: 被测 Manager（登记会落到它的 store）
//   - repo: 仓库路径；本助手会给它配一个由路径派生的唯一 origin
//
// 返回：
//   - project_id，供 DispatchReq{ProjectID: ...} 使用
func registerTestProject(t *testing.T, m *Manager, repo string) string {
	t.Helper()
	// origin 由路径派生：每个用例的临时仓库各不相同，project_id 因此天然不撞。
	origin := "git@handoff.test:" + strings.ReplaceAll(strings.TrimPrefix(repo, "/"), "/", "-") + ".git"
	gitAt(t, repo, "remote", "add", "origin", origin)
	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("registerTestProject(%s): %v", repo, err)
	}
	return loc.ProjectID
}
```

然后逐文件改造（四个文件共 56 处 `Repo:` 赋值）：

- `DispatchReq{Repo: repo, ...}` → 先 `pid := registerTestProject(t, m, repo)`，再 `DispatchReq{ProjectID: pid, ...}`
- `proto.Task{RepoPath: "/r", ...}` 这类**直接造任务**的用例不受影响（它们不走 Dispatch），保持原样
- `DispatchReq{Repo: "/r"}` 这类只为触发参数校验的用例：改成 `DispatchReq{}`（不给项目），断言从 `errBadDispatchRequest` 保持不变——未指明项目同样落在这个哨兵上；若某个用例本意是测「plan 与 prompt 都缺」，则先登记一个真实 repo 再派，避免两个错误互相遮蔽

逐个跑，逐个修：

```bash
go test ./internal/agentd/ 2>&1 | head -50
```

- [ ] **Step 12: 全量编译与测试**

```bash
go build ./... && go vet ./... && go test ./...
```

Expected: 全绿。若 `cmd` 包的既有测试（`dispatch_test.go` / `dispatch_dirty_test.go`）引用了 `dispatchRepo`，同步改名。

- [ ] **Step 13: 加关键节点日志**

本步骤只审接线处新增/改动的日志（Task 4 已覆盖登记与解析自身）：

- `Dispatch` 进入/失败两行的键已从 `repo` 换成 `project_id` + `project_name`
- 新增「dispatch 项目已解析」Info，带 `project_id` / `name` / `path` —— 它把「请求说的项目」与「实际动的目录」钉在同一行，是排查派发到错误位置的第一线索
- `writeDispatchError` / `writeProjectError` 的每个分支都保留了 Warn/Error，日志键已从 `repo` 换成 `project`
- 迁移函数：跳过的每一行各一条 Warn（带 name/path/origin，且指出人工处置命令），结束一条 Info 带 `migrated` / `skipped` 计数 —— 静默迁移等于数据凭空少了几行而无人知晓
- 确认没有引入 `fmt.Printf` 作为日志：

```bash
grep -rn "fmt.Printf" internal/agentd/ internal/store/ internal/client/ || echo "OK"
```

- [ ] **Step 14: 加意图注释**

- `cmd/project.go` 文件头（职责 + 边界，含「不决定项目在那台机器的哪个目录」）
- `registerProjectBothHops` 的 doc 注释：`--target` 是「一起登记」而非「只登远程」，以及任一跳失败不回滚的理由
- `LocalEndpoint` 的「为什么不能复用 TargetEndpoint」
- `DispatchReq` / `dispatchRequest` 新字段的注释，尤其「请求体里没有任何路径字段」这条及其理由
- `Dispatch` 解析块的「这是必须先登记才能派发的唯一执行点」
- `migrateReposToProjectLocations` 的四条注意（保留最早、Abs+Clean、不探测文件系统、幂等靠 DROP）
- `registerTestProject` 的「为什么不给测试开旁路」

- [ ] **Step 15: 提交**

```bash
go build ./... && go vet ./... && go test ./...
git add -A
git commit -m "refactor!: 派发改用 project_id，删除 --repo 与 repo 子命令族

BREAKING CHANGE: /api/repos 改为 /api/projects；dispatch 请求体不再接受
repo 与 origin_url，改为 project_id / project_name；handoff repo add/ls/rm
改名 handoff project add/ls/rm；旧 repos 表迁入 project_locations 后删除。"
```

---

## Task 6: `repo_root` 默认值

**Files:**
- Modify: `internal/config/config.go`（`Load` 里补默认；字段注释改写）
- Modify: `cmd/init.go:143`（交互问句的措辞）
- Test: `internal/config/config_test.go`（若不存在则创建）

**Interfaces:**
- Consumes: `config.defaultDataDir()`
- Produces: 行为契约——`config.Load` 返回的 `cfg.RepoRoot` 恒非空；未显式配置时为 `filepath.Join(cfg.DataDir, "repos")`

**为什么必须有默认值：** 自动登记（Task 7）把 clone 变成了首次派发的主路径，而 `cloneAndRegisterProject` 在 `RepoRoot == ""` 时直接拒。默认值缺席等于自动登记在全新机器上必然失败。

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 追加（文件不存在则新建，`package config`）：

```go
// TestLoadFillsRepoRootDefault 验证 repo_root 未配置时补 <DataDir>/repos，
// 且配置里写了的值不被覆盖。
//
// 为什么必须有默认值：自动登记（B62 §6）把 clone 变成首次派发的主路径，
// repo_root 为空时 agentd 直接拒绝 clone，全新开发机上第一次派发必然失败。
func TestLoadFillsRepoRootDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// 首次运行：文件不存在，生成默认配置并写盘。
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(cfg.DataDir, "repos")
	if cfg.RepoRoot != want {
		t.Fatalf("RepoRoot = %q, want %q", cfg.RepoRoot, want)
	}
	// 默认值必须**落盘**，让人看得见，而不是藏在使用点。
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回配置: %v", err)
	}
	if !strings.Contains(string(b), "repo_root:") {
		t.Fatalf("默认 repo_root 应随首次写盘落到 config.yaml，实际内容:\n%s", b)
	}

	// 显式配置不被覆盖。
	explicit := filepath.Join(dir, "explicit.yaml")
	if err := os.WriteFile(explicit,
		[]byte("token: abc\nrepo_root: /srv/code\n"), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	cfg2, err := Load(explicit)
	if err != nil {
		t.Fatalf("Load(explicit): %v", err)
	}
	if cfg2.RepoRoot != "/srv/code" {
		t.Fatalf("显式 repo_root 被覆盖了: %q", cfg2.RepoRoot)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/config/ -run TestLoadFillsRepoRootDefault -v
```

Expected: FAIL —— `RepoRoot = "", want ".../repos"`。

- [ ] **Step 3: 写实现**

在 `internal/config/config.go` 的 `Load` 中，**yaml 解码之后、`cfg.validate()` 之前**插入：

```go
	// repo_root 的默认值必须在解码之后补，不能预置在初始字面量里：
	// 它派生自 DataDir，而 DataDir 本身可能被配置文件改写。
	//
	// 为什么必须有默认值：自动登记（B62）把 clone 变成首次派发的主路径，
	// repo_root 为空时 agentd 会直接拒绝 clone，新开发机上第一次派发必然失败。
	// 落盘之后就固定，此后改 datadir 不会静默改克隆落点。
	if cfg.RepoRoot == "" {
		cfg.RepoRoot = filepath.Join(cfg.DataDir, "repos")
		log().Info("repo_root 未配置，采用默认落点", "repo_root", cfg.RepoRoot)
	}
```

首次运行分支（`errors.Is(err, os.ErrNotExist)`）里 `save(path, cfg)` 发生在这段之前，因此需把默认值补齐移到 `save` 之前——具体做法：把上面这段提到 `switch` 语句**之前**不行（那时还没解码），改为在 `switch` 结束后立刻补默认，然后对「首次运行」这一路再写一次盘。最简形态是把 `switch` 里的 `save` 挪到补默认之后，用一个 `firstRun bool` 标记：

```go
	firstRun := false
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg.Token = randToken()
		firstRun = true
		log().Info("首次运行，将生成配置", "path", path)
	case err != nil:
		return nil, fmt.Errorf("读配置 %s: %w", path, err)
	default:
		if uerr := decodeStrict(b, cfg); uerr != nil {
			log().Error("配置解析失败", "path", path, "cause", uerr)
			return nil, fmt.Errorf("解析配置 %s: %w", path, uerr)
		}
	}
	// …（此处补 repo_root 默认值）…
	if firstRun {
		if werr := save(path, cfg); werr != nil {
			return nil, fmt.Errorf("写默认配置 %s: %w", path, werr)
		}
		log().Info("首次运行，已生成配置", "path", path)
	}
```

同时改写 `RepoRoot` 字段的注释：把「空=未配置，此时 --clone 必须显式给路径」改为「空=未配置，Load 会补 `<DataDir>/repos`；自动登记的 clone 落点即此处」。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/config/ -v && go test ./cmd/ -run TestInit -v
```

Expected: PASS。`cmd/init_test.go` 若断言了 repo_root 的空默认值，同步更新断言。

- [ ] **Step 5: 改 `cmd/init.go` 的问句**

```go
		cfg.RepoRoot = askString(w, r, "项目落点根目录 repo_root（自动登记时 clone 到这里）", cfg.RepoRoot)
```

- [ ] **Step 6: 加关键节点日志**

确认：补默认值时打了一条 Info（带最终 `repo_root`）——它决定了此后所有自动 clone 的落点，静默取默认会让人在磁盘上找不到代码时无从下手。首次写盘成功后保留原有的 Info。

- [ ] **Step 7: 加意图注释**

确认：补默认那段的「为什么在解码之后」与「为什么必须有默认值」、`RepoRoot` 字段注释已更新、`firstRun` 标记的存在理由（默认值要随首次写盘一起落地）。

- [ ] **Step 8: 提交**

```bash
go build ./... && go vet ./... && go test ./internal/config/ ./cmd/
git add internal/config/config.go internal/config/config_test.go cmd/init.go
git commit -m "feat(config): repo_root 默认 <DataDir>/repos，并随首次写盘落地"
```

---

## Task 7: CLI 侧自动登记编排

**Files:**
- Modify: `cmd/dispatch.go`
- Test: `cmd/dispatch_autoregister_test.go`（新建）

**Interfaces:**
- Consumes: `client.ProjectAdd`（Task 5）、`LocalEndpoint`（Task 5）、`registerProjectBothHops`（Task 5）、`agentd.ErrProjectNotRegistered` 的**报文**（跨进程，只能按 HTTP 400 + 文本判别）
- Produces:
  - `cmd.isProjectNotRegistered(err error) bool`（包内）
  - `cmd.dispatchWithAutoRegister(dispatch func() (*proto.Task, error), register func() error) (*proto.Task, error)`（包内）—— 编排本身，两个副作用以闭包注入，因此三条语义（触发一次 / 只重试一次 / 登记失败不重试）可以零网络单测

**判别方式：** `client` 把非 200 响应包成 `httpError`，CLI 拿到的是文本。因此判别只能按报文特征做。为了不让它变成脆弱的字符串匹配，**agentd 侧的报文以哨兵文案 `项目未登记` 开头**（`ErrProjectNotRegistered` 的 `Error()` 就是这四个字，`fmt.Errorf("%w: …")` 保证它出现在报文最前）。这个耦合必须在两边都写进注释。

- [ ] **Step 1: 写失败测试**

创建 `cmd/dispatch_autoregister_test.go`：

```go
package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestIsProjectNotRegistered 验证 CLI 能从 agentd 的 400 报文里认出
// 「项目未登记」，从而触发自动补登记后重发（spec §6.2）。
//
// 为什么按文本判：错误跨进程传递，errors.Is 在这一侧失效；agentd 的
// ErrProjectNotRegistered 报文以「项目未登记」四字开头，两边靠这个约定对齐。
func TestIsProjectNotRegistered(t *testing.T) {
	yes := []string{
		"dispatch 失败: HTTP 400: 项目未登记: project_id=9f2a1c7d5e3b0a84；本机已登记的项目：（本机尚无任何项目）",
		"项目未登记: \"nova\"；本机已登记的项目：handoff → /w/handoff",
	}
	for _, s := range yes {
		if !isProjectNotRegistered(errStr(s)) {
			t.Errorf("应识别为未登记: %q", s)
		}
	}
	no := []string{
		"dispatch 失败: HTTP 409: 工作区不干净",
		"dispatch 失败: HTTP 400: 请求未指明项目（project_id 与 project_name 至少其一）",
		"",
	}
	for _, s := range no {
		if isProjectNotRegistered(errStr(s)) {
			t.Errorf("不应识别为未登记: %q", s)
		}
	}
	if isProjectNotRegistered(nil) {
		t.Error("nil 不应识别为未登记")
	}
}

// errStr 把字符串包成 error，供表驱动用例使用。
type errStr string

func (e errStr) Error() string { return string(e) }

// TestDispatchWithAutoRegisterRetriesOnce 验证编排的正常路径：
// 首次派发被拒 → 触发一次登记 → 重发成功。
func TestDispatchWithAutoRegisterRetriesOnce(t *testing.T) {
	dispatches, registers := 0, 0
	task, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			if dispatches == 1 {
				return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
			}
			return &proto.Task{ID: "t1"}, nil
		},
		func() error { registers++; return nil },
	)
	if err != nil {
		t.Fatalf("重发应成功: %v", err)
	}
	if task == nil || task.ID != "t1" {
		t.Fatalf("应返回重发得到的任务, got %+v", task)
	}
	if dispatches != 2 || registers != 1 {
		t.Fatalf("应派发 2 次、登记 1 次，got dispatch=%d register=%d", dispatches, registers)
	}
}

// TestDispatchWithAutoRegisterGivesUpAfterOneRetry 验证登记成功后仍被拒时**不再重试**：
// 那说明另有原因（如刚被别人 project rm 掉），无限重试会把可诊断的失败变成死循环。
func TestDispatchWithAutoRegisterGivesUpAfterOneRetry(t *testing.T) {
	dispatches, registers := 0, 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
		},
		func() error { registers++; return nil },
	)
	if err == nil {
		t.Fatal("持续被拒时应返回错误")
	}
	if dispatches != 2 || registers != 1 {
		t.Fatalf("最多派发 2 次、登记 1 次，got dispatch=%d register=%d", dispatches, registers)
	}
}

// TestDispatchWithAutoRegisterSurfacesRegisterFailure 验证登记失败时透出原文、
// **不重发**：clone 失败或落点被占都需要人去那台机器上处置，替它猜只会掩盖真因。
func TestDispatchWithAutoRegisterSurfacesRegisterFailure(t *testing.T) {
	dispatches := 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) {
			dispatches++
			return nil, errStr("HTTP 400: 项目未登记: project_id=abc")
		},
		func() error { return errStr("落点 /root/work/handoff 已存在") },
	)
	if err == nil {
		t.Fatal("登记失败时 dispatch 应整体失败")
	}
	if !strings.Contains(err.Error(), "落点 /root/work/handoff 已存在") {
		t.Errorf("应透出登记失败原文，got %q", err.Error())
	}
	if dispatches != 1 {
		t.Fatalf("登记失败后不应重发，got dispatch=%d", dispatches)
	}
}

// TestDispatchWithAutoRegisterPassesThroughOtherErrors 验证非「未登记」的错误
// 原样透出，绝不触发登记——工作区不干净之类的失败自动登记帮不上任何忙。
func TestDispatchWithAutoRegisterPassesThroughOtherErrors(t *testing.T) {
	sentinel := errStr("HTTP 409: 工作区不干净")
	registers := 0
	_, err := dispatchWithAutoRegister(
		func() (*proto.Task, error) { return nil, sentinel },
		func() error { registers++; return nil },
	)
	if !errors.Is(err, error(sentinel)) {
		t.Fatalf("应原样透出原错误，got %v", err)
	}
	if registers != 0 {
		t.Fatalf("不该触发登记，got register=%d", registers)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./cmd/ -run TestIsProjectNotRegistered -v
```

Expected: 编译失败，`undefined: isProjectNotRegistered` / `undefined: dispatchWithAutoRegister`。

- [ ] **Step 3: 写实现**

在 `cmd/dispatch.go` 追加：

```go
// projectNotRegisteredMarker 是 agentd 侧 ErrProjectNotRegistered 的哨兵文案。
//
// 为什么用文本而不是 errors.Is：错误跨进程传递，到 CLI 这一侧只剩报文。
// agentd 的报文一律形如 fmt.Errorf("%w: …", ErrProjectNotRegistered)，
// 因此这四个字必然出现在报文里。**改动 agentd 侧那个哨兵的文案就必须同步改这里**
//（internal/agentd/projectresolve.go 的 ErrProjectNotRegistered 上有对应提示）。
const projectNotRegisteredMarker = "项目未登记"

// isProjectNotRegistered 报告一个 dispatch 错误是不是「目标机上没有这个项目」。
//
// 参数：
//   - err: Dispatch 返回的错误（可为 nil）
//
// 返回：
//   - true 表示可以走自动补登记后重发的路径
func isProjectNotRegistered(err error) bool {
	return err != nil && strings.Contains(err.Error(), projectNotRegisteredMarker)
}

// dispatchWithAutoRegister 执行「派发 → 未登记则补登记 → 重发一次」的编排。
//
// 参数：
//   - dispatch: 发一次派发请求
//   - register: 补一次登记（两跳：本机 + 目标机）
//
// 返回：
//   - 派发成功的任务；任一环节失败时返回错误
//
// 注意：
//   - 为什么「先发再被拒再重发」而不是先预检：项目解析是 dispatch 的第一道闸，
//     早于建任务目录、早于工作区准备、早于 executor 启动——被拒的全部代价就是
//     一次 HTTP 400，没有任何残留要清理。而预检还多一次 TOCTOU（查完到派发之间
//     可以 project rm），服务端照样得判（spec §6.4）
//   - **最多重发一次**：登记成功后仍被拒说明另有原因（如刚被别人 rm 掉），
//     无限重试只会把一个可诊断的失败变成一个死循环
//   - 登记失败时**不重发、不降级**，原文透出：clone 失败或落点被占都需要人去
//     那台机器上处置，替它猜只会掩盖真因
//   - 两个副作用以闭包注入，编排本身因此可以零网络单测
func dispatchWithAutoRegister(dispatch func() (*proto.Task, error), register func() error) (*proto.Task, error) {
	task, err := dispatch()
	if !isProjectNotRegistered(err) {
		return task, err
	}
	if rerr := register(); rerr != nil {
		return nil, fmt.Errorf("自动登记失败: %w", rerr)
	}
	return dispatch()
}
```

在 `dispatchCmd.RunE` 里，把单次 `Dispatch` 调用换成这个编排：

```go
			opts := client.DispatchOpts{
				ProjectID: projectID, ProjectName: dispatchProject,
				PlanB64: planB64, PlanName: planName, Target: targetName,
				Prompt: dispatchPrompt, Name: dispatchName,
				Executor: dispatchExecutor, Model: dispatchModel,
				Branch: dispatchBranch, NewBranch: dispatchNewBranch, Base: dispatchBase,
				Worktree: dispatchWorktree, NewWorktree: dispatchNewWorktree,
				BaseCommit: baseCommit,
			}
			cli := client.New(addr, token)
			task, err := dispatchWithAutoRegister(
				func() (*proto.Task, error) { return cli.Dispatch(cmd.Context(), opts) },
				func() error {
					// 用 --project <名字> 指名的项目查不到时，自动登记帮不上忙：
					// 名字不是身份，本机无从知道那个名字该指向哪个 origin。
					if dispatchProject != "" {
						return fmt.Errorf("--project 指定的 %q 在目标机上不存在；"+
							"在该项目目录里执行 handoff project add 登记它", dispatchProject)
					}
					fmt.Fprintln(cmd.ErrOrStderr(), "目标机上还没有这个项目，正在自动登记…")
					root, rerr := localProjectRoot(cmd.Context())
					if rerr != nil {
						return rerr
					}
					// 走的就是 project add 那条路：既补本机，也补目标机（spec §6.2）。
					// 本机送主工作树路径、永不 clone；目标机不送 path，由它 clone 到
					// 自己的 repo_root/<名字>——本机因此一个远程细节都不需要知道。
					return registerProjectBothHops(cmd, localOriginURL(), "", root, "")
				},
			)
			if err != nil {
				return err
			}
```

追加 import：`"strings"` 与 `"fmt"` 已有；新增 `"github.com/xushixin/handoff/internal/proto"`（`dispatchWithAutoRegister` 的返回类型）。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./cmd/ -run "TestIsProjectNotRegistered|TestDispatchWithAutoRegister" -v && go build ./... && go test ./cmd/
```

Expected: 五个用例全 PASS（判别 1 个 + 编排 4 个）。

- [ ] **Step 5: 端到端手工验证**

在本机起一个 agentd（沿用日常方式），然后在本仓库目录里：

```bash
handoff project ls
```

确认列表为空。再：

```bash
handoff dispatch --prompt "只回一句 ok，不要改任何文件" --executor fake
```

Expected（按顺序观察）：
1. stderr 出现「目标机上还没有这个项目，正在自动登记…」
2. stdout 出现「本机 handoff → /Users/xushixin/workspace/handoff」（**主仓路径，不是 worktree 路径**——这是 §5 归并的现场验证）
3. 任务 JSON 输出，`state` 为 `running`
4. `handoff project ls` 现在有一行

然后验证复发：

```bash
handoff project rm handoff && handoff dispatch --prompt "再来一次" --executor fake
```

Expected: 再次自动登记并成功。

- [ ] **Step 6: 加关键节点日志**

CLI 侧面向人的输出走 stderr（stdout 是「单行任务 JSON」的既有契约，多打一行会打断上层脚本按行解析）。确认三条都在：

- 触发自动登记时：「目标机上还没有这个项目，正在自动登记…」
- 远程需要 clone 时（`registerProjectBothHops` 内）：「正在让 <target> 克隆 <origin>（首次可能较慢）…」—— 首次派发因全量 clone 显著变慢是 spec §14 风险三，不说人会以为卡死
- 每一跳登记成功后各一行「<机器> <名字> → <路径>」

自检：没有引入 `fmt.Printf`（它绕开 cobra 的输出流，测试捕获不到）：

```bash
grep -n "fmt.Printf(" cmd/dispatch.go cmd/project.go || echo "OK"
```

- [ ] **Step 7: 加意图注释**

确认：`projectNotRegisteredMarker` 的「为什么用文本判 + 改文案要两边同步」、自动登记块的「为什么先发再被拒」「为什么只重试一次」「为什么 --project 指名时不自动登记」「为什么失败不重试不降级」。

同时在 `internal/agentd/projectresolve.go` 的 `ErrProjectNotRegistered` 注释里补一句：

```go
// 注意：本机 CLI 按报文里的「项目未登记」四字判别并触发自动补登记
//（cmd/dispatch.go 的 projectNotRegisteredMarker）。**改这里的文案要同步改那边。**
```

- [ ] **Step 8: 提交**

```bash
go build ./... && go vet ./... && go test ./...
git add cmd/dispatch.go cmd/dispatch_autoregister_test.go internal/agentd/projectresolve.go
git commit -m "feat(dispatch): 目标机缺项目时自动补登记并重发一次"
```

---

## Task 8: 文档与 skill

**Files:**
- Modify: `README.md`（第 138–144、172–175、221、239 行附近）
- Modify: `skills/handoff/SKILL.md`（第 68、142、144、150 行附近）

> **spec §9（ADR-0008 补一笔）本分支不做**：`docs/adr/` 不在这个分支上。ADR 的修订留到合并回主线后单独提交，本计划不涉及。

**Interfaces:**
- Consumes: Task 5–7 落定的最终命令形态
- Produces: 无代码接口；产出是「agent 照着文档写出来的命令是对的」

**为什么必须同一轮改完：** `skills/handoff/SKILL.md` 随二进制 embed 分发（见近期提交 `bd7cbdee`），文档里留着 `--repo <路径>` 的示例，agent 就会照旧示例继续写路径，然后撞未知 flag。

- [ ] **Step 1: 改 README 的命令示例**

把第 138–144 行的示例块整体换成：

````markdown
```bash
# 派发的项目由当前目录识别，不需要写路径
handoff dispatch plan.md
handoff dispatch --prompt "把 README 安装命令改成 brew"                        # 无 plan 文件
handoff dispatch --new-worktree --executor opencode --model cheap/model plan.md
handoff dispatch --new-worktree --executor claude plan.md                      # 用 Claude Code 执行
handoff dispatch --target devbox plan.md                                      # 派到开发机（未登记会自动登记）
handoff dispatch --project nova --target devbox plan.md                       # 跨项目：cwd 不是目标项目时
handoff dispatch --no-terminal plan.md                                        # 派发后不弹终端
```
````

- [ ] **Step 2: 改 README 的命令表**

第 172–175 行四行替换为：

```markdown
| `handoff dispatch [plan.md]` | 派发计划任务（项目由当前目录自动识别） | `--project <名字>`（cwd 不是目标项目时指定）；`--prompt "<指令>"`（prompt-only 派发，与 plan 文件至少其一）；`--name`/`--executor`/`--model`；`--branch <b>\|--new-branch <b>`；`--base <t>`；`--worktree <路径>\|--new-worktree`；`--no-terminal`；`--no-sync-check`；`--allow-dirty` |
| `handoff project add [名字]` | 把当前项目登记到本机（`--target` 时一并登记到那台开发机） | `--target <机器>`（一起登记；那台机器上没有时自动 clone）；`--path <该机器上已有的路径>`（仅与 `--target` 连用） |
| `handoff project ls` | 列出机器上的项目位置（含实际状态） | `--target <机器>` |
| `handoff project rm <名字>` | 注销一条项目位置（只删登记，不删磁盘上的代码） | `--target <机器>` |
```

- [ ] **Step 3: 改 README 的 `repo_root` 说明**

第 221 行改为：

```yaml
repo_root: ""                 # 项目落点根目录；留空由 handoff 补 <datadir>/repos 并写回本文件
```

第 239 行附近那段解释改为：说明它是**自动登记时 clone 的落点**（首次派发到一台新开发机的必经之路），并保留原有的「每台执行机自己决定项目放在哪」的理由。

- [ ] **Step 4: 改 `skills/handoff/SKILL.md`**

- 第 68 行：`handoff dispatch --repo /path/to/repo --new-worktree --executor opencode plan.md`
  → `handoff dispatch --new-worktree --executor opencode plan.md`
- 第 142 行：「基线取自 cwd，不是 `--repo`」→ 改为说明**项目本身就取自 cwd**，因此必须在项目目录里发 dispatch；cwd 不是 git 仓库时直接被拒（不再是「只打一行提示」）
- 第 144 行：「只在 cwd 和 `--repo` 根本不是同一个仓库时用它」→ 「只在 cwd 与 `--project` 指定的项目不是同一个仓库时用它」
- 第 150 行：`handoff dispatch --target devbox --repo /remote/path \` → `handoff dispatch --target devbox \`
- 在派发一节补一段新内容：

```markdown
**项目由 cwd 识别，第一次派到某台开发机会自动登记。** 你不需要（也无法）告诉
handoff「代码在那台机器的哪个目录」——那是它自己的事。首次派发会多一次往返，
远程还含一次全量 clone，stderr 会打出「正在让 <机器> 克隆 …」。

**在 worktree 里派发会归并到主仓。** 项目位置永远是主工作树，不是你当前所在的
那个 worktree。想接着某个分支干，用 `--base <分支>` 显式表达。
```

- [ ] **Step 5: 校对——文档里不许再出现旧形态**

```bash
grep -rn -- "--repo\b\|repo add\|repo ls\|repo rm\|/api/repos" README.md skills/ \
  || echo "OK: 文档已无旧形态残留"
```

Expected: 输出 `OK`。若有命中，逐条改掉（spec §10：不留别名，也不留会让旧心智繁殖的示例）。

- [ ] **Step 6: 日志与注释自检（全计划收尾）**

本任务不改代码，但这是整个计划的收尾闸。逐项确认：

- [ ] 每个错误分支都打了带上下文与 cause 的日志（`projectadmin.go` / `projectresolve.go` / `gitroot.go` / `manager.go` 解析块 / `server.go` 两个 write*Error / `store` 迁移）
- [ ] 每个外部调用（clone、git rev-parse、两跳 HTTP 登记）在前后各有记录；clone 的**成功与失败**都带 `elapsed_ms`
- [ ] 成功路径不静默：「项目位置登记完成」「dispatch 项目已解析」「旧仓库登记已迁入项目位置表」「主工作树归并」四条都在
- [ ] 没有把 `fmt.Printf` / `println` 当日志用：

```bash
grep -rn "fmt.Printf\|println(" internal/ | grep -v "_test.go" || echo "OK"
```

- [ ] 新文件全部有文件头注释（职责 + 边界）：`internal/projectid/projectid.go`、`internal/agentd/gitroot.go`、`projectadmin.go`、`projectresolve.go`、`internal/store/projects.go`、`cmd/project.go`
- [ ] 新增导出函数全部有 doc 注释（参数/返回/注意）
- [ ] 非显然分支有「为什么」注释：主工作树归并、origin 校验、名字退让、先发再被拒、只重试一次、迁移保留最早一条

任一项未过，回到对应 task 补完再继续。

- [ ] **Step 7: 全量验证与提交**

```bash
go build ./... && go vet ./... && go test ./...
git add README.md skills/handoff/SKILL.md
git commit -m "docs: README/skill 同步项目位置模型"
```

---

## 收尾

全部 task 完成后：

1. 跑一遍完整验证：`go build ./... && go vet ./... && go test ./...`
2. 按 spec §11 逐条对照「交付给 W3a 的不变式」是否成立：
   - 凡派发过的项目都在 `project_locations` 里（无旁路：`grep -rn "ListProjectLocations" internal/agentd/manager.go` 应只有解析块那一处入口）
   - 一台机器上一个项目最多一行（主键强制，`TestProjectLocationPrimaryKeyEnforcesOneLocationPerProject` 覆盖）
   - 每条位置都有非空 origin（`projectOriginURL` 拒绝无 origin 的仓库，`TestRegisterProjectRejectsNoOrigin` 覆盖）
   - `project_id` 跨机一致且可离线计算（`TestFromOriginIsStableAndDistinct` 覆盖）
3. 合并到 `handoff/web-console` 时，按 spec §8 的并行冲突提示**重跑 W1 契约 fixture 的更新开关并提交**——本轮改了 `internal/proto/`，不跑两侧测试会同时变红。
4. 按 `superpowers:finishing-a-development-branch` 收尾。
