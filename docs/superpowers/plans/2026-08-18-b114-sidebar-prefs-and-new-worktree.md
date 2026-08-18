# 左栏显示偏好菜单 + 机器行新建工作树 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让控制台左栏从「只能看」变成「能收拾、能开工作树」——项目可隐藏可排序、空闲工作树可折叠，机器行 hover 能直接在那台执行机上新建工作树。

**Architecture:** 前端偏好全部落 localStorage，判断逻辑抽成 `treePrefs.ts` 的纯函数（排序/拆分都走 metrics 回调，测试用手写数字驱动）。建树能力在 agentd 侧新增两条项目级接口，git 操作收在新文件 `internal/agentd/manualworktree.go`，与既有的 `PrepareWorkspace`（任务工作区）互不干涉，只共用 `gitRun` 与那套参数注入防线。

**Tech Stack:** Go 1.26（`net/http` ServeMux 路由模式、`log/slog`）、React 19 + TypeScript + Vite + Tailwind v4、vitest + @testing-library/react。

**Spec:** [`docs/superpowers/specs/2026-08-18-b114-sidebar-prefs-and-new-worktree-design.md`](../specs/2026-08-18-b114-sidebar-prefs-and-new-worktree-design.md)（下称 spec；条款号如 §3.3 均指它）

## Global Constraints

以下为 spec 的项目级约束，**每个 task 的需求都隐含包含本节**：

- **日志绝不用 `fmt.Printf` / `console.log` 当机制**。Go 侧：`Server` 方法用 `s.log`，包级函数用 `log()`（`internal/agentd/workspace.go:116`，即 `slog.Default()`）。每个错误分支必须带上下文与 `cause`；成功路径也必须有一条收尾日志。
- **注释**：新文件写文件头注释（职责 + 边界，即「它不做什么」）；导出函数写 doc 注释（参数、返回、注意事项）；非显然的分支写「为什么」的中文注释。重复代码的注释是噪音，删掉。
- **不动 `GET /api/tasks/{id}/branches`**，不动 `PrepareWorkspace`，不动 `sortWorkspaces` 的既有排序规则，不动看板/工单/设置页。
- **契约三处同步**：`internal/proto/*.go` 结构体、`web/src/api/types.ts` 接口、`web/src/api/testdata/*.json`。fixture **不手写**，用 `go test ./internal/proto/ -run TestContractFixtures -update` 生成。
- **`git worktree remove` 类的删除动作本期一律不做**（spec 非目标）。清理失败的落点只允许 `os.Remove`（只删空目录），**任何情况下都不得出现 `os.RemoveAll`**。
- 每个 task 完成即 commit，提交信息用该 task 最后一步给定的原文。
- 前端每次改完跑 `cd web && npx vitest run`；Go 每次改完跑 `go test ./internal/...`。**没亲自跑到结果的命令，不许写它的结论。**

## File Structure

| 文件 | 责任 |
|---|---|
| `internal/proto/projects.go`（改） | 新增 `ProjectBranch` / `ProjectBranchesResp` / `CreateWorktreeReq`；订正 `Workspace.Managed` 注释 |
| `internal/proto/contract_fixture_test.go`（改） | 两个新类型进 `cases` |
| `internal/agentd/manualworktree.go`（新建） | `ManualWorktreeRoot` / `CreateManualWorktree`——手工工作树的校验、建树、回读 |
| `internal/agentd/manualworktree_test.go`（新建） | 上述函数的成功路径与六条拒绝 |
| `internal/agentd/projectadmin.go`（改） | `handleProjectBranches` / `handleProjectWorktreeCreate` 两个 HTTP handler |
| `internal/agentd/server.go`（改） | 注册两条路由 |
| `internal/agentd/projectadmin_test.go`（改） | handler 层的 404 / 400 |
| `web/src/api/types.ts`（改） | 三个新接口类型 |
| `web/src/api/client.ts`（改） | `fetchProjectBranches` / `createWorktree` |
| `web/src/app/tree/treePrefs.ts`（新建） | 偏好类型、读写、三个纯函数（排序/拆项目/拆目录） |
| `web/src/app/tree/treePrefs.test.ts`（新建） | 上述纯函数的用例 |
| `web/src/app/lib/IconMenu.tsx`（改） | 项类型扩展：`kind` / `checked` / `keepOpen` + 菜单滚动 |
| `web/src/app/lib/IconMenu.test.tsx`（改） | 扩展项的用例 |
| `web/src/app/tree/TreePrefsMenu.tsx`（新建） | 把 prefs + 项目列表装配成 IconMenu 的 items |
| `web/src/app/tree/NewWorktreeDialog.tsx`（新建） | 建树弹层 |
| `web/src/app/tree/NewWorktreeDialog.test.tsx`（新建） | 弹层用例 |
| `web/src/app/tree/ProjectTree.tsx`（改） | 持有 prefs、渲染菜单/「已隐藏 N」行/机器行 `+`/弹层 |
| `web/src/app/tree/ProjectTree.test.tsx`（改） | 上述交互用例 |
| `web/src/app/shell/Shell.tsx`（改） | 接 `onWorktreeCreated`：刷新树 + 选中新目录 |

**执行顺序即 Task 顺序**：后端契约（1）→ 后端能力（2）→ 后端接口（3）→ 前端接线（4）→ 前端偏好（5-7）→ 建树 UI（8-9）→ 终审（10）。

---

## Task 1: proto 契约与 fixture

**Files:**
- Modify: `internal/proto/projects.go`
- Modify: `internal/proto/contract_fixture_test.go`
- Modify: `web/src/api/types.ts`
- Create（由 `-update` 生成，不手写）: `web/src/api/testdata/ProjectBranchesResp.json`、`web/src/api/testdata/CreateWorktreeReq.json`

**Interfaces:**
- Produces: `proto.ProjectBranch{Name,Worktree}`、`proto.ProjectBranchesResp{Branches,Default,WorktreeRoot}`、`proto.CreateWorktreeReq{Mode,Branch,Base}`，以及 TS 侧同名接口 `ProjectBranch` / `ProjectBranchesResp` / `CreateWorktreeReq`

- [ ] **Step 1: 加 proto 类型**

在 `internal/proto/projects.go` 末尾追加：

```go
// ProjectBranch 是一个本地分支，带「是否已被工作树占用」。
//
// Worktree 为已检出该分支的工作树路径；空串 = 没有任何工作树占用它。
// git 不允许同一分支被两个工作树同时检出，所以占用者就是「这个分支现在
// 不能再开树」的全部原因——界面据此把选项置灰并说清是谁占着。
type ProjectBranch struct {
	Name     string `json:"name"`
	Worktree string `json:"worktree"`
}

// ProjectBranchesResp 是 GET /api/projects/{name}/branches 的响应。
//
// 顶层形状（branches + default）与 /api/tasks/{id}/branches 一致，但 branches
// 是对象数组而非字符串数组——多了占用信息，两者刻意不共用类型。
type ProjectBranchesResp struct {
	// Branches 永不为 nil（空仓库返回空数组）。
	Branches []ProjectBranch `json:"branches"`
	// Default 是推导出的基准分支；推导不出为空串。
	Default string `json:"default"`
	// WorktreeRoot 是手工新建工作树的落点根目录，供界面如实回显「会建在哪」。
	// 界面只回显这个根，不自己拼完整路径——目录名的生成规则只有服务端一份。
	WorktreeRoot string `json:"worktree_root"`
}

// CreateWorktreeReq 是 POST /api/projects/{name}/worktrees 的请求体。
type CreateWorktreeReq struct {
	// Mode 二选一："new_branch"（建新分支并开树）/ "existing_branch"（把已有分支开成一棵树）。
	Mode string `json:"mode"`
	// Branch 是要新建或要检出的分支名，必填。
	Branch string `json:"branch"`
	// Base 是新分支的起点，仅 new_branch 模式有意义；空串时由服务端推导。
	Base string `json:"base"`
}
```

同时把 `Workspace.Managed` 的注释订正为（只改注释，判据一个字不动）：

```go
	// Managed 表示该工作树落在 agentd 的数据区（<DataDir>/worktrees）下——
	// 既包括任务自建树（worktrees/<id8>），也包括手工新建树（worktrees/manual/<名>）。
	// 判据只看路径前缀，不区分二者：本字段没有任何行为消费者（回收只认终态任务
	// 的记录、从不扫目录），为它加特例只会留下一个要读三处代码才懂的例外。
	Managed bool `json:"managed"`
```

- [ ] **Step 2: 加 fixture 样本并生成**

在 `internal/proto/contract_fixture_test.go` 的 `cases` 列表末尾追加两行：

```go
		{"ProjectBranchesResp", projectBranchesSample()},
		{"CreateWorktreeReq", createWorktreeReqSample()},
```

并在该文件的样本函数区追加：

```go
// projectBranchesSample 是分支列表响应的契约样本：一条被占用、一条空闲。
func projectBranchesSample() ProjectBranchesResp {
	return ProjectBranchesResp{
		Branches: []ProjectBranch{
			{Name: "main", Worktree: "/Users/dev/code/handoff"},
			{Name: "feat/b114-sidebar-prefs", Worktree: ""},
		},
		Default:      "main",
		WorktreeRoot: "/Users/dev/.handoff/worktrees/manual",
	}
}

// createWorktreeReqSample 是建树请求的契约样本。
func createWorktreeReqSample() CreateWorktreeReq {
	return CreateWorktreeReq{Mode: "new_branch", Branch: "feat/b114-sidebar-prefs", Base: "main"}
}
```

Run: `go test ./internal/proto/ -run TestContractFixtures -update`
然后 `go test ./internal/proto/`（不带 `-update`）应 PASS。

- [ ] **Step 3: TS 侧同步**

在 `web/src/api/types.ts` 的 `ProjectTreeResp` 附近追加：

```ts
// ProjectBranch 是一个本地分支；worktree 非空表示已被那个工作树检出，
// 不能再开第二棵树（git 的硬约束，不是我们加的规矩）。
export interface ProjectBranch {
  name: string
  worktree: string
}

// ProjectBranchesResp 是 GET /api/projects/{name}/branches 的响应。
// 注意与 BranchesResult（/api/tasks/{id}/branches）不是同一类型：那边的
// branches 是字符串数组，这边是带占用信息的对象数组。
export interface ProjectBranchesResp {
  branches: ProjectBranch[]
  default: string
  worktree_root: string
}

// CreateWorktreeReq 是 POST /api/projects/{name}/worktrees 的请求体。
export interface CreateWorktreeReq {
  mode: 'new_branch' | 'existing_branch'
  branch: string
  base: string
}
```

- [ ] **Step 4: 验证前后端契约一致**

Run: `cd web && npx vitest run src/api/contract.test.ts && npx tsc -b`
Expected: PASS，无类型错误。

- [ ] **Step 5: Commit**

```bash
git add internal/proto web/src/api/types.ts web/src/api/testdata
git commit -m "feat(proto): 加建树与分支列表的契约类型"
```

---

## Task 2: 手工工作树的建树核心

**Files:**
- Create: `internal/agentd/manualworktree.go`
- Create: `internal/agentd/manualworktree_test.go`

**Interfaces:**
- Consumes: `gitRun(ctx, repo string, args ...string) (stdout, stderr string, err error)`；`probeWorkspaces(ctx, dir, managedRoot string) ([]proto.Workspace, string)`；`canonPath(p string) string`；`resolveBaseBranch(repo string) string`；`WorkspaceGitTimeout`（2 分钟）；`log() *slog.Logger`；测试助手 `initGitRepo(t)` 与 `gitAt(t, dir, args...)`（在 `workspace_test.go`，同包可直接用）
- Produces: `agentd.ManualWorktreeRoot(worktreesDir string) string`、`agentd.ErrBadWorktreeReq`、`agentd.CreateManualWorktree(ctx context.Context, repo, worktreesDir string, req proto.CreateWorktreeReq) (proto.Workspace, error)`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/manualworktree_test.go`：

```go
package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// newBranchReq / existingBranchReq 是两种模式的请求构造捷径，让用例只写变量部分。
func newBranchReq(branch, base string) proto.CreateWorktreeReq {
	return proto.CreateWorktreeReq{Mode: "new_branch", Branch: branch, Base: base}
}

func existingBranchReq(branch string) proto.CreateWorktreeReq {
	return proto.CreateWorktreeReq{Mode: "existing_branch", Branch: branch}
}

// TestCreateManualWorktreeNewBranch 验证新建分支模式：落点在 manual 子目录下、
// 分支真的被建出来、返回的 Workspace 与项目树同口径（Managed=true、Branch 对得上）。
func TestCreateManualWorktreeNewBranch(t *testing.T) {
	repo := initGitRepo(t)
	dataDir := t.TempDir()
	worktreesDir := filepath.Join(dataDir, "worktrees")

	ws, err := CreateManualWorktree(context.Background(), repo, worktreesDir, newBranchReq("feat/x", "main"))
	if err != nil {
		t.Fatalf("CreateManualWorktree: %v", err)
	}
	wantDir := filepath.Join(worktreesDir, "manual", "feat-x")
	if canonPath(ws.Path) != canonPath(wantDir) {
		t.Fatalf("落点 = %q, want %q", ws.Path, wantDir)
	}
	if ws.Branch != "feat/x" {
		t.Fatalf("分支 = %q, want feat/x", ws.Branch)
	}
	if !ws.Managed {
		t.Fatalf("落在数据区的树 Managed 应为 true")
	}
	if ws.IsMain {
		t.Fatalf("新建树不可能是主工作树")
	}
	if _, statErr := os.Stat(filepath.Join(wantDir, "README.md")); statErr != nil {
		t.Fatalf("新树里应有仓库内容: %v", statErr)
	}
	if out := gitAt(t, repo, "branch", "--list", "feat/x"); !strings.Contains(out, "feat/x") {
		t.Fatalf("分支未建出来: %q", out)
	}
}

// TestCreateManualWorktreeExistingBranch 验证检出已有分支模式。
func TestCreateManualWorktreeExistingBranch(t *testing.T) {
	repo := initGitRepo(t)
	gitAt(t, repo, "branch", "feat/done")
	worktreesDir := filepath.Join(t.TempDir(), "worktrees")

	ws, err := CreateManualWorktree(context.Background(), repo, worktreesDir, existingBranchReq("feat/done"))
	if err != nil {
		t.Fatalf("CreateManualWorktree: %v", err)
	}
	if ws.Branch != "feat/done" {
		t.Fatalf("分支 = %q, want feat/done", ws.Branch)
	}
}

// TestCreateManualWorktreeBaseInferred 验证 base 为空时走 resolveBaseBranch 推导，
// 不是直接拒绝——弹层允许不选基线。
func TestCreateManualWorktreeBaseInferred(t *testing.T) {
	repo := initGitRepo(t)
	worktreesDir := filepath.Join(t.TempDir(), "worktrees")

	if _, err := CreateManualWorktree(context.Background(), repo, worktreesDir, newBranchReq("feat/inferred", "")); err != nil {
		t.Fatalf("base 为空应能推导: %v", err)
	}
}

// TestCreateManualWorktreeRejects 把全部拒绝钉在一处：每条都必须是
// ErrBadWorktreeReq（HTTP 层据此回 400），且报文含出问题的那个值。
func TestCreateManualWorktreeRejects(t *testing.T) {
	cases := []struct {
		name    string
		prep    func(t *testing.T, repo, worktreesDir string)
		req     proto.CreateWorktreeReq
		wantSub string
	}{
		{
			name:    "模式非法",
			prep:    func(*testing.T, string, string) {},
			req:     proto.CreateWorktreeReq{Mode: "whatever", Branch: "feat/a"},
			wantSub: "whatever",
		},
		{
			name:    "分支名空",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("", "main"),
			wantSub: "branch",
		},
		{
			name:    "分支名以横杠开头",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("-rf", "main"),
			wantSub: "-rf",
		},
		{
			name:    "分支名不是合法 ref",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("feat/..x", "main"),
			wantSub: "feat/..x",
		},
		{
			name: "新建模式下分支已存在",
			prep: func(t *testing.T, repo, _ string) {
				gitAt(t, repo, "branch", "feat/dup")
			},
			req:     newBranchReq("feat/dup", "main"),
			wantSub: "feat/dup",
		},
		{
			name:    "检出模式下分支不存在",
			prep:    func(*testing.T, string, string) {},
			req:     existingBranchReq("feat/ghost"),
			wantSub: "feat/ghost",
		},
		{
			name: "分支已被别的工作树占用",
			prep: func(t *testing.T, repo, worktreesDir string) {
				gitAt(t, repo, "branch", "feat/taken")
				if _, err := CreateManualWorktree(context.Background(), repo, worktreesDir, existingBranchReq("feat/taken")); err != nil {
					t.Fatalf("预置第一棵树: %v", err)
				}
			},
			req:     existingBranchReq("feat/taken"),
			wantSub: "feat/taken",
		},
		{
			name: "落点已存在",
			prep: func(t *testing.T, _, worktreesDir string) {
				if err := os.MkdirAll(filepath.Join(worktreesDir, "manual", "feat-occupied"), 0o700); err != nil {
					t.Fatalf("预置落点: %v", err)
				}
			},
			req:     newBranchReq("feat/occupied", "main"),
			wantSub: "feat-occupied",
		},
		{
			name:    "基线不存在",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("feat/badbase", "nonexistent-base"),
			wantSub: "nonexistent-base",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := initGitRepo(t)
			worktreesDir := filepath.Join(t.TempDir(), "worktrees")
			c.prep(t, repo, worktreesDir)

			_, err := CreateManualWorktree(context.Background(), repo, worktreesDir, c.req)
			if err == nil {
				t.Fatalf("应当被拒")
			}
			if !errors.Is(err, ErrBadWorktreeReq) {
				t.Fatalf("错误应可判别为 ErrBadWorktreeReq, got %v", err)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("报文应含 %q, got %q", c.wantSub, err.Error())
			}
		})
	}
}

// TestManualWorktreeRoot 钉住落点根：界面回显的就是它，改了等于改契约。
func TestManualWorktreeRoot(t *testing.T) {
	if got := ManualWorktreeRoot("/data/worktrees"); got != filepath.Join("/data/worktrees", "manual") {
		t.Fatalf("ManualWorktreeRoot = %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run 'TestCreateManualWorktree|TestManualWorktreeRoot' -v`
Expected: 编译失败，`undefined: CreateManualWorktree` / `undefined: ManualWorktreeRoot` / `undefined: ErrBadWorktreeReq`

- [ ] **Step 3: 写实现**

新建 `internal/agentd/manualworktree.go`：

```go
// manualworktree.go —— 手工工作树：不属于任何任务的 git worktree。
//
// 职责：
//   - 校验建树请求（模式/分支名/基线/占用/落点）并给出可判别的拒绝理由
//   - 在 <DataDir>/worktrees/manual/<分支名安全化> 下建树
//   - 回读一次项目树口径的 proto.Workspace 交给调用方
//
// 边界：
//   - **不认识任务**：不落库、不发事件、不参与回收。它建出来的树没有任何自动
//     清理路径（本期不做删除入口，见 spec §8），这是自觉的取舍不是遗漏
//   - 不复用 PrepareWorkspace：那条路径要求 task_id 且按 id8 命名目录，语义是
//     「为一次派发准备工作区」；本文件的语义是「人手开一棵树」，共用只会让两边
//     的参数互相污染。二者共用的只有 gitRun 与参数注入防线
//   - 失败清理只用 os.Remove（只删空目录），**绝不 RemoveAll**：落点里一旦有
//     内容，那就是用户的东西，宁可留残骸也不能替他删
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrBadWorktreeReq 标记「请求本身不合法」，HTTP 层据此回 400 而不是 500。
var ErrBadWorktreeReq = errors.New("建树请求不合法")

// manualSubdir 是手工树在 agentd 数据区里的子目录名。
//
// 与任务自建树（worktrees/<id8>）分层而不是混在一起：路径形状本身就能回答
// 「这棵树是谁建的」，不需要再查库。
const manualSubdir = "manual"

// ManualWorktreeRoot 返回手工树的落点根目录。
//
// 参数：worktreesDir 即 <DataDir>/worktrees。
// 返回：<DataDir>/worktrees/manual。界面用它如实回显「会建在哪」。
func ManualWorktreeRoot(worktreesDir string) string {
	return filepath.Join(worktreesDir, manualSubdir)
}

// manualDirName 把分支名转成目录名：'/' 换 '-'，其余原样。
//
// 分支名此前已过 git check-ref-format，不含空格与控制字符，所以这一步只需处理
// 层级分隔符。副作用是 feat/x 与 feat-x 会撞同一个目录名——撞了直接拒（调用方
// 的落点存在性检查），不自动加后缀：自动改名会让人以为建在了他以为的位置。
func manualDirName(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// rejectWorktree 包一条拒绝理由，统一挂上 ErrBadWorktreeReq 并打 Warn。
func rejectWorktree(reason string, req proto.CreateWorktreeReq) error {
	log().Warn("建树被拒", "mode", req.Mode, "branch", req.Branch, "base", req.Base, "cause", reason)
	return fmt.Errorf("%w: %s", ErrBadWorktreeReq, reason)
}

// branchExists 判定本地分支是否存在。
//
// 判据是 rev-parse --verify --quiet refs/heads/<名> 有非空输出：--quiet 让
// 「不存在」走退出码而不是 stderr，与 PrepareWorkspace 的判法保持一致。
func branchExists(ctx context.Context, repo, branch string) bool {
	out, _, err := gitRun(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil && strings.TrimSpace(out) != ""
}

// CreateManualWorktree 在 worktreesDir/manual 下开一棵不属于任何任务的工作树。
//
// 参数：
//   - repo: 项目主仓路径（登记表里的 path）
//   - worktreesDir: <DataDir>/worktrees
//   - req: 建树请求，见 proto.CreateWorktreeReq
//
// 返回：
//   - 新工作树的 proto.Workspace，口径与项目树上那一条完全一致（含 head/created_at）
//   - 错误：请求类问题一律包 ErrBadWorktreeReq（调用方回 400）；git 执行失败原样返回
//
// 注意：
//   - 整个过程限时 WorkspaceGitTimeout（2 分钟）——pre-checkout hook 与凭证交互
//     提示都能把 git 挂死，不设上限会拖死一个 HTTP 连接
//   - 成功后会多跑一次 git worktree list 做回读，为的是让返回值与树上同源；
//     回读挑不到时退回手工组装并 Warn，**不因为回读失败就把成功报成失败**
func CreateManualWorktree(ctx context.Context, repo, worktreesDir string, req proto.CreateWorktreeReq) (proto.Workspace, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	start := time.Now()
	log().Info("手工建树进入", "repo", repo, "mode", req.Mode, "branch", req.Branch,
		"base", req.Base, "worktrees_dir", worktreesDir, "timeout", WorkspaceGitTimeout)

	// 第 1 层：纯内存参数校验（模式/空值/注入面）
	if req.Mode != "new_branch" && req.Mode != "existing_branch" {
		return proto.Workspace{}, rejectWorktree("mode 必须是 new_branch 或 existing_branch，收到 "+req.Mode, req)
	}
	if strings.TrimSpace(req.Branch) == "" {
		return proto.Workspace{}, rejectWorktree("branch 必填", req)
	}
	for _, v := range []struct{ what, val string }{{"branch", req.Branch}, {"base", req.Base}} {
		if strings.HasPrefix(v.val, "-") {
			return proto.Workspace{}, rejectWorktree(v.what+" 不允许以 - 开头（git 参数注入面）: "+v.val, req)
		}
	}
	// 分支名合法性交给 git 自己判，不自己写正则：ref 命名规则有十来条，
	// 手写一份必然与 git 的实现分叉
	if _, stderr, err := gitRun(ctx, repo, "check-ref-format", "--branch", req.Branch); err != nil {
		return proto.Workspace{}, rejectWorktree("分支名 "+req.Branch+" 不是合法的 git 分支名: "+strings.TrimSpace(stderr), req)
	}

	// 第 2 层：仓库现状校验
	exists := branchExists(ctx, repo, req.Branch)
	base := req.Base
	switch req.Mode {
	case "new_branch":
		if exists {
			return proto.Workspace{}, rejectWorktree("分支 "+req.Branch+" 已存在，请换个名字或改用「检出已有分支」", req)
		}
		if base == "" {
			base = resolveBaseBranch(repo)
			log().Info("建树基线由推导得出", "repo", repo, "base", base)
		}
		if base == "" {
			return proto.Workspace{}, rejectWorktree("推导不出基准分支，请显式指定基线", req)
		}
		if _, _, err := gitRun(ctx, repo, "rev-parse", "--verify", "--quiet", base); err != nil {
			return proto.Workspace{}, rejectWorktree("基线 "+base+" 在仓库里不存在", req)
		}
	case "existing_branch":
		if !exists {
			return proto.Workspace{}, rejectWorktree("分支 "+req.Branch+" 不存在", req)
		}
	}

	managedRoot := worktreesDir
	// 占用检查放在 git 之前只为给人话：git 自己那层拒绝（already checked out）
	// 仍然是最终防线，两处都留着
	if req.Mode == "existing_branch" {
		existing, probeErr := probeWorkspaces(ctx, repo, managedRoot)
		if probeErr != "" {
			log().Warn("建树前探测已有工作树失败，占用检查降级为由 git 兜底", "repo", repo, "cause", probeErr)
		}
		for _, ws := range existing {
			if ws.Branch == req.Branch {
				return proto.Workspace{}, rejectWorktree("分支 "+req.Branch+" 已被工作树 "+ws.Path+" 检出，一个分支只能有一棵树", req)
			}
		}
	}

	// 第 3 层：落点
	root := ManualWorktreeRoot(worktreesDir)
	dir := filepath.Join(root, manualDirName(req.Branch))
	if _, err := os.Stat(dir); err == nil {
		return proto.Workspace{}, rejectWorktree("落点 "+dir+" 已存在，请换个分支名或先清理该目录", req)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		log().Error("建树失败：创建落点根目录", "root", root, "cause", err)
		return proto.Workspace{}, fmt.Errorf("创建目录 %s: %w", root, err)
	}

	args := []string{"worktree", "add", dir, req.Branch}
	if req.Mode == "new_branch" {
		args = []string{"worktree", "add", "-b", req.Branch, dir, base}
	}
	if _, stderr, err := gitRun(ctx, repo, args...); err != nil {
		log().Error("建树失败：git worktree add", "repo", repo, "dir", dir,
			"branch", req.Branch, "stderr", truncateRunes(stderr, 500), "cause", err)
		// best-effort 清空目录：add 失败通常不留目录，留下的也只可能是空壳。
		// 只用 Remove（非空即失败）——落点里若有内容那是用户的东西
		if rmErr := os.Remove(dir); rmErr != nil && !os.IsNotExist(rmErr) {
			log().Warn("建树失败后清理落点未完成，保留现场待查", "dir", dir, "cause", rmErr)
		}
		return proto.Workspace{}, fmt.Errorf("git worktree add %s: %s: %w", dir, strings.TrimSpace(stderr), err)
	}

	ws := readBackWorktree(ctx, repo, managedRoot, dir, req.Branch)
	log().Info("手工建树完成", "repo", repo, "dir", ws.Path, "branch", ws.Branch,
		"managed", ws.Managed, "elapsed_ms", time.Since(start).Milliseconds())
	return ws, nil
}

// readBackWorktree 回读刚建出来的树，返回与项目树同口径的 proto.Workspace。
//
// 路径比对走 canonPath：macOS 上 /var → /private/var 是实景，直接字符串比会
// 一个都对不上。挑不到时退回手工组装的最小值并 Warn——树已经建成了，不能因为
// 回读没赶上（并发被删、探测超时）就把一次成功报成失败。
func readBackWorktree(ctx context.Context, repo, managedRoot, dir, branch string) proto.Workspace {
	list, probeErr := probeWorkspaces(ctx, repo, managedRoot)
	if probeErr != "" {
		log().Warn("建树后回读探测失败，返回最小信息", "dir", dir, "cause", probeErr)
	}
	want := canonPath(dir)
	for _, ws := range list {
		if canonPath(ws.Path) == want {
			return ws
		}
	}
	log().Warn("建树后回读挑不到新树，返回最小信息", "dir", dir, "branch", branch)
	return proto.Workspace{Path: dir, Branch: branch, Managed: true}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestCreateManualWorktree|TestManualWorktreeRoot' -v`
Expected: 全部 PASS

- [ ] **Step 5: 自查日志与注释覆盖**

对照逐条确认（不满足就补，别跳过）：
- 进入有日志（含 repo/mode/branch/base/落点根）✓
- 每条拒绝有 Warn 且带 cause ✓（`rejectWorktree` 统一承担）
- 外部调用（`git worktree add`）失败有 Error 且带 stderr ✓
- 成功路径有收尾日志且带 elapsed_ms ✓
- 文件头注释写了职责与边界 ✓；导出函数有 doc 注释 ✓
- 没有任何 `fmt.Printf` ✓

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/manualworktree.go internal/agentd/manualworktree_test.go
git commit -m "feat(agentd): 手工工作树的建树核心（校验/建树/回读）"
```

---

## Task 3: 两条 HTTP 接口

**Files:**
- Modify: `internal/agentd/projectadmin.go`（追加两个 handler）
- Modify: `internal/agentd/server.go`（注册路由，紧挨现有 `PATCH /api/projects/{name}` 那行之后）
- Modify: `internal/agentd/projectadmin_test.go`（追加 handler 层用例）

**Interfaces:**
- Consumes: Task 2 的 `CreateManualWorktree` / `ManualWorktreeRoot` / `ErrBadWorktreeReq`；既有的 `s.forwardIfRequested(w, r) bool`、`s.st.GetProjectLocationByName(name) (proto.ProjectLocation, error)`、`store.ErrNotFound`、`Branches(repo) ([]string, error)`、`resolveBaseBranch(repo) string`、`probeWorkspaces`、`writeJSON`、`truncateRunes`
- Produces: `GET /api/projects/{name}/branches?machine=`、`POST /api/projects/{name}/worktrees?machine=`

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/projectadmin_test.go` 末尾追加。

**三条本包既有的踩坑约定，照抄不要自创**：
- Server 用 `newTestServerWithManager(t)`（`regression_round2_test.go:41`），它已经把 `Token: "test"` 与 `DataDir: t.TempDir()` 装好
- `httptest.NewRequest` 的默认 Host 是 `example.com`，会被 hostGuard 在鉴权**之前** 403 掉，必须显式 `req.Host = "127.0.0.1:7777"`
- 鉴权走 `req.Header.Set("Authorization", "Bearer test")`

```go
// registerWorktreeTestProject 在库里登记一个真仓库，返回登记名。
// 建树接口按登记名寻址，所以用例必须有一条真的位置行，不能只造目录。
func registerWorktreeTestProject(t *testing.T, st *store.Store, repo string) string {
	t.Helper()
	loc := &proto.ProjectLocation{
		ProjectID: "p-worktree-test",
		Name:      "demo",
		Path:      repo,
		OriginURL: "git@github.com:Xsxdot/demo.git",
		CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(loc); err != nil {
		t.Fatalf("登记项目: %v", err)
	}
	return loc.Name
}

// doWorktreeReq 发一条已带 Host 与 Bearer 的请求，返回 recorder。
func doWorktreeReq(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// TestProjectWorktreeCreateRejectsUnknownProject 验证项目不存在时是 404 而不是 500：
// 500 会让界面把「名字打错了」显示成「服务端炸了」。
func TestProjectWorktreeCreateRejectsUnknownProject(t *testing.T) {
	s, _, _ := newTestServerWithManager(t)
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/nope/worktrees",
		`{"mode":"new_branch","branch":"feat/x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestProjectWorktreeCreateRejectsBadBody 验证坏请求体是 400 且报文说清要什么。
func TestProjectWorktreeCreateRejectsBadBody(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	name := registerWorktreeTestProject(t, st, initGitRepo(t))
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/"+name+"/worktrees", "not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestProjectWorktreeCreateOK 验证成功路径：返回的是项目树口径的 Workspace，
// 且落点真的在 <DataDir>/worktrees/manual 下。
func TestProjectWorktreeCreateOK(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	name := registerWorktreeTestProject(t, st, initGitRepo(t))
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/"+name+"/worktrees",
		`{"mode":"new_branch","branch":"feat/x","base":"main"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var ws proto.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if ws.Branch != "feat/x" {
		t.Fatalf("分支 = %q", ws.Branch)
	}
	wantRoot := ManualWorktreeRoot(filepath.Join(s.conf().DataDir, "worktrees"))
	if !strings.HasPrefix(canonPath(ws.Path), canonPath(wantRoot)) {
		t.Fatalf("落点 %q 不在 %q 下", ws.Path, wantRoot)
	}
}

// TestProjectWorktreeCreateRejectsDuplicateBranch 验证请求类拒绝映射成 400 而非 500。
func TestProjectWorktreeCreateRejectsDuplicateBranch(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	repo := initGitRepo(t)
	gitAt(t, repo, "branch", "feat/dup")
	name := registerWorktreeTestProject(t, st, repo)
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/"+name+"/worktrees",
		`{"mode":"new_branch","branch":"feat/dup","base":"main"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "feat/dup") {
		t.Fatalf("报文应含出问题的分支名: %s", rec.Body.String())
	}
}

// TestProjectBranchesMarksOccupied 验证分支列表把「已被工作树检出」如实标出来——
// 弹层据此置灰，标不出来用户就会选一个必然失败的分支。
func TestProjectBranchesMarksOccupied(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	name := registerWorktreeTestProject(t, st, initGitRepo(t))
	rec := doWorktreeReq(t, s, http.MethodGet, "/api/projects/"+name+"/branches", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp proto.ProjectBranchesResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(resp.Branches) == 0 {
		t.Fatalf("至少应列出主分支")
	}
	// 主仓自己就检出着默认分支，它必须被标为已占用
	var def *proto.ProjectBranch
	for i := range resp.Branches {
		if resp.Branches[i].Name == resp.Default {
			def = &resp.Branches[i]
		}
	}
	if def == nil || def.Worktree == "" {
		t.Fatalf("默认分支应被主工作树占用, got %+v", resp.Branches)
	}
	if resp.WorktreeRoot == "" {
		t.Fatalf("worktree_root 不能为空，界面要靠它回显落点")
	}
}

// TestProjectBranchesUnknownProject 验证项目不存在时 404。
func TestProjectBranchesUnknownProject(t *testing.T) {
	s, _, _ := newTestServerWithManager(t)
	if rec := doWorktreeReq(t, s, http.MethodGet, "/api/projects/nope/branches", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
```

补该文件的 import：`net/http`、`net/http/httptest`、`encoding/json`、`strings`、`path/filepath`、`time`、`store`、`proto`（缺哪个补哪个）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run 'TestProjectWorktreeCreate|TestProjectBranches' -v`
Expected: FAIL（404 路由不存在会返回 404 但 branches 那条会失败；若助手名不对则编译失败——此时按 Step 1 的提示换成本包真实存在的助手名，**不要新造**）

- [ ] **Step 3: 写 handler**

在 `internal/agentd/projectadmin.go` 末尾追加：

```go
// handleProjectBranches 处理 GET /api/projects/{name}/branches[?machine=]。
//
// 列出该项目位置的本地分支，并标出每个分支是否已被某棵工作树检出——建树弹层
// 的两个下拉都吃这份数据。
//
// 参数：
//   - name（路径）: 项目登记名
//   - machine（查询）: 可选，转发到指定机器
//
// 响应：200 proto.ProjectBranchesResp；项目不存在 404；列分支失败 500（原文透出）
func (s *Server) handleProjectBranches(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	s.log.Info("列项目分支请求", "name", name, "machine", r.URL.Query().Get("machine"))
	loc, err := s.st.GetProjectLocationByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("列项目分支被拒：项目不存在", "name", name, "status", http.StatusNotFound, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
			return
		}
		s.log.Error("列项目分支失败：查询位置表", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	names, err := Branches(loc.Path)
	if err != nil {
		s.log.Error("列项目分支失败", "name", name, "repo", loc.Path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	worktreesDir := filepath.Join(s.conf().DataDir, "worktrees")
	// 占用表复用项目树那次探测的同一个函数，不另跑一遍 worktree list：
	// 两处口径分叉时，界面置灰的分支与真能建的分支会对不上
	existing, probeErr := probeWorkspaces(r.Context(), loc.Path, worktreesDir)
	if probeErr != "" {
		s.log.Warn("列项目分支：探测工作树失败，占用信息缺失", "name", name, "cause", probeErr)
	}
	byBranch := make(map[string]string, len(existing))
	for _, ws := range existing {
		if ws.Branch != "" {
			byBranch[ws.Branch] = ws.Path
		}
	}
	resp := proto.ProjectBranchesResp{
		Branches:     make([]proto.ProjectBranch, 0, len(names)),
		Default:      resolveBaseBranch(loc.Path),
		WorktreeRoot: ManualWorktreeRoot(worktreesDir),
	}
	for _, b := range names {
		resp.Branches = append(resp.Branches, proto.ProjectBranch{Name: b, Worktree: byBranch[b]})
	}
	s.log.Info("列项目分支完成", "name", name, "count", len(resp.Branches),
		"default", resp.Default, "occupied", len(byBranch))
	writeJSON(w, http.StatusOK, resp)
}

// handleProjectWorktreeCreate 处理 POST /api/projects/{name}/worktrees[?machine=]。
//
// 在该项目位置上开一棵不属于任何任务的工作树（spec §3.2）。
//
// 参数：
//   - name（路径）: 项目登记名
//   - machine（查询）: 可选，转发到指定机器
//   - 请求体: proto.CreateWorktreeReq
//
// 响应：200 proto.Workspace；项目不存在 404；请求不合法 400；git 失败 500（原文透出）
func (s *Server) handleProjectWorktreeCreate(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	var req proto.CreateWorktreeReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("建树请求体解析失败", "name", name, "status", http.StatusBadRequest, "cause", err)
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "请求体必须是 JSON {mode, branch, base}"})
		return
	}
	s.log.Info("建树请求", "name", name, "machine", r.URL.Query().Get("machine"),
		"mode", req.Mode, "branch", req.Branch, "base", req.Base)
	loc, err := s.st.GetProjectLocationByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("建树被拒：项目不存在", "name", name, "status", http.StatusNotFound, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
			return
		}
		s.log.Error("建树失败：查询位置表", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	ws, err := CreateManualWorktree(r.Context(), loc.Path, filepath.Join(s.conf().DataDir, "worktrees"), req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrBadWorktreeReq) {
			status = http.StatusBadRequest
		}
		s.log.Error("建树失败", "name", name, "repo", loc.Path, "mode", req.Mode,
			"branch", req.Branch, "status", status, "cause", err)
		writeJSON(w, status, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	s.log.Info("建树完成", "name", name, "dir", ws.Path, "branch", ws.Branch)
	writeJSON(w, http.StatusOK, ws)
}
```

补齐 import（`errors` / `io` / `encoding/json` / `net/http` / `path/filepath` / `store` / `proto` 多数已在该文件，缺哪个补哪个）。

在 `internal/agentd/server.go` 的 `api.HandleFunc("PATCH /api/projects/{name}", s.handleProjectPatch)` 之后加两行：

```go
	api.HandleFunc("GET /api/projects/{name}/branches", s.handleProjectBranches)
	api.HandleFunc("POST /api/projects/{name}/worktrees", s.handleProjectWorktreeCreate)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestProjectWorktreeCreate|TestProjectBranches' -v && go test ./internal/...`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentd
git commit -m "feat(agentd): 项目级分支列表与建树两条接口"
```

---

## Task 4: 前端 API 客户端

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.test.ts`

**Interfaces:**
- Consumes: 既有的 `request<T>`、`postJSON<T>`、`machineQuery(machine?, sep?)`；Task 1 的 TS 类型
- Produces: `fetchProjectBranches(name: string, machine?: string): Promise<ProjectBranchesResp>`、`createWorktree(name: string, req: CreateWorktreeReq, machine?: string): Promise<Workspace>`

- [ ] **Step 1: 写失败的测试**

在 `web/src/api/client.test.ts` 末尾追加（**沿用该文件已有的 fetch mock 写法**，不要新引测试库）：

```ts
describe('建树接口', () => {
  it('fetchProjectBranches 按登记名寻址并带上 machine', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ branches: [], default: 'main', worktree_root: '/d/manual' }), { status: 200 }),
    )
    await fetchProjectBranches('my repo', 'mac-02')
    expect(spy.mock.calls[0][0]).toBe('/api/projects/my%20repo/branches?machine=mac-02')
  })

  it('createWorktree 本机时不带 machine 参数', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ path: '/d/manual/feat-x' }), { status: 200 }),
    )
    await createWorktree('handoff', { mode: 'new_branch', branch: 'feat/x', base: 'main' })
    expect(spy.mock.calls[0][0]).toBe('/api/projects/handoff/worktrees')
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: FAIL（`fetchProjectBranches is not defined`）

- [ ] **Step 3: 写实现**

在 `web/src/api/client.ts` 的 `patchProject` 之后追加：

```ts
// fetchProjectBranches 列项目位置的本地分支（GET /api/projects/{name}/branches）。
//
// name 是**登记名**（ProjectLocationNode.name），不是 ProjectNode.name——后者取的是
// 该项目下首条登记的名字，跨机时两者可能不同，用错会寻址到别的位置或 404。
// machine 省略或空串 = 本机。
export function fetchProjectBranches(name: string, machine?: string): Promise<ProjectBranchesResp> {
  return request<ProjectBranchesResp>(`/api/projects/${encodeURIComponent(name)}/branches${machineQuery(machine)}`)
}

// createWorktree 在项目位置上新建一棵工作树（POST /api/projects/{name}/worktrees）。
//
// 返回的 Workspace 与项目树上那一条同口径，可直接拿去组装 BaseDir 选中，
// 不必等下一轮树刷新。name 的取值同 fetchProjectBranches。
export function createWorktree(name: string, req: CreateWorktreeReq, machine?: string): Promise<Workspace> {
  return postJSON<Workspace>(`/api/projects/${encodeURIComponent(name)}/worktrees${machineQuery(machine)}`, req)
}
```

补 import：`CreateWorktreeReq`、`ProjectBranchesResp`、`Workspace`（该文件顶部已有 `import type {...} from './types'`，加进去即可）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/api/client.test.ts && npx tsc -b`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/api
git commit -m "feat(web): 建树与分支列表的 API 客户端"
```

---

## Task 5: 左栏偏好的纯函数层

**Files:**
- Create: `web/src/app/tree/treePrefs.ts`
- Create: `web/src/app/tree/treePrefs.test.ts`

**Interfaces:**
- Produces:
  - `type ProjectSort = 'active' | 'name' | 'recent'`
  - `interface TreePrefs { v: 1; hideIdleWorktrees: boolean; projectSort: ProjectSort; hiddenProjects: string[] }`
  - `const PREFS_KEY = 'handoff.tree.prefs'`、`const DEFAULT_PREFS: TreePrefs`
  - `loadPrefs(): TreePrefs`、`savePrefs(p: TreePrefs): void`
  - `interface ProjectMetrics { active: number; updatedAt: string; name: string }`
  - `sortProjects<T>(list: T[], metricsOf: (x: T) => ProjectMetrics, mode: ProjectSort): T[]`
  - `splitHiddenProjects<T>(list: T[], idOf: (x: T) => string, hidden: string[]): { shown: T[]; hiddenCount: number }`
  - `interface WorkspaceIdleInfo { isMain: boolean; selected: boolean; active: number }`
  - `splitIdleWorkspaces<T>(list: T[], infoOf: (x: T) => WorkspaceIdleInfo, hideIdle: boolean): { shown: T[]; hidden: T[] }`

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/tree/treePrefs.test.ts`：

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_PREFS, PREFS_KEY, loadPrefs, savePrefs,
  sortProjects, splitHiddenProjects, splitIdleWorkspaces,
} from './treePrefs'

beforeEach(() => localStorage.clear())

describe('偏好读写', () => {
  it('没存过时给默认值', () => {
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })

  it('存过就读回来', () => {
    savePrefs({ ...DEFAULT_PREFS, hideIdleWorktrees: true, hiddenProjects: ['p2'] })
    expect(loadPrefs().hideIdleWorktrees).toBe(true)
    expect(loadPrefs().hiddenProjects).toEqual(['p2'])
  })

  it('坏 JSON 静默回退默认值，不抛错', () => {
    localStorage.setItem(PREFS_KEY, '{不是 json')
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })

  it('版本号对不上就丢弃', () => {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ v: 99, hideIdleWorktrees: true }))
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })

  it('字段类型不对就丢弃（防手改）', () => {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ v: 1, hideIdleWorktrees: 'yes', projectSort: 'active', hiddenProjects: [] }))
    expect(loadPrefs()).toEqual(DEFAULT_PREFS)
  })
})

describe('项目排序', () => {
  const list = [
    { id: 'a', name: 'zeta', active: 1, updatedAt: '2026-08-18T10:00:00+08:00' },
    { id: 'b', name: 'alpha', active: 3, updatedAt: '2026-08-16T10:00:00+08:00' },
    { id: 'c', name: 'mid', active: 1, updatedAt: '2026-08-17T10:00:00+08:00' },
  ]
  const metricsOf = (x: (typeof list)[number]) => ({ active: x.active, updatedAt: x.updatedAt, name: x.name })

  it('active：活跃多的在前，相同活跃按名称升序兜底', () => {
    expect(sortProjects(list, metricsOf, 'active').map((x) => x.id)).toEqual(['b', 'c', 'a'])
  })

  it('name：纯名称升序', () => {
    expect(sortProjects(list, metricsOf, 'name').map((x) => x.id)).toEqual(['b', 'c', 'a'])
  })

  it('recent：最近动过的在前，没有时间的排最后', () => {
    const withEmpty = [...list, { id: 'd', name: 'never', active: 0, updatedAt: '' }]
    expect(sortProjects(withEmpty, metricsOf, 'recent').map((x) => x.id)).toEqual(['a', 'c', 'b', 'd'])
  })

  it('不改入参', () => {
    const copy = [...list]
    sortProjects(list, metricsOf, 'name')
    expect(list).toEqual(copy)
  })
})

describe('项目隐藏', () => {
  const list = [{ id: 'p1' }, { id: 'p2' }, { id: 'p3' }]
  it('剔除名单里的，并报出被剔了几个', () => {
    const r = splitHiddenProjects(list, (x) => x.id, ['p2'])
    expect(r.shown.map((x) => x.id)).toEqual(['p1', 'p3'])
    expect(r.hiddenCount).toBe(1)
  })
  it('名单为空时原样返回', () => {
    expect(splitHiddenProjects(list, (x) => x.id, []).hiddenCount).toBe(0)
  })
})

describe('空闲目录折叠', () => {
  const list = [
    { p: '/w', isMain: true, selected: false, active: 0 },
    { p: '/w/busy', isMain: false, selected: false, active: 2 },
    { p: '/w/idle', isMain: false, selected: false, active: 0 },
    { p: '/w/picked', isMain: false, selected: true, active: 0 },
  ]
  const infoOf = (x: (typeof list)[number]) => ({ isMain: x.isMain, selected: x.selected, active: x.active })

  it('关掉开关时一个都不折', () => {
    expect(splitIdleWorkspaces(list, infoOf, false).hidden).toEqual([])
  })

  it('开着时只折无活跃任务的，主工作树与选中目录豁免', () => {
    const r = splitIdleWorkspaces(list, infoOf, true)
    expect(r.shown.map((x) => x.p)).toEqual(['/w', '/w/busy', '/w/picked'])
    expect(r.hidden.map((x) => x.p)).toEqual(['/w/idle'])
  })

  it('保持入参顺序（排序已由 sortWorkspaces 定好，这里不得重排）', () => {
    const r = splitIdleWorkspaces([...list].reverse(), infoOf, true)
    expect(r.shown.map((x) => x.p)).toEqual(['/w/picked', '/w/busy', '/w'])
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/treePrefs.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 写实现**

新建 `web/src/app/tree/treePrefs.ts`：

```ts
// treePrefs —— 左栏显示偏好：读写 localStorage + 三个纯函数（spec §1）。
//
// 职责：
//   - 偏好的形状、默认值与持久化（单键 handoff.tree.prefs）
//   - 项目排序、项目隐藏、空闲目录折叠三条规则本身
//
// 边界：
//   - **不认识 React、不认识项目树类型**：三个函数都收泛型 + metrics/info 回调，
//     测试可以用手写数字驱动，不必造一整棵树加一批任务
//   - 不管「搜索期间要不要旁路」：那是调用方的取舍（ProjectTree 决定传不传），
//     规则本身不该知道有搜索这回事
//   - 不碰目录行的既有排序（sortWorkspaces）：这里只折叠，绝不重排
export type ProjectSort = 'active' | 'name' | 'recent'

// TreePrefs 是落盘的全部偏好。v 用于将来改形状时判断要不要整份丢弃。
export interface TreePrefs {
  v: 1
  hideIdleWorktrees: boolean
  projectSort: ProjectSort
  // hiddenProjects 存的是**隐藏名单**（project_id）而不是显示名单：
  // 新登记的项目必须默认可见，否则刚登记完在左栏找不到，看起来像登记失败
  hiddenProjects: string[]
}

export const PREFS_KEY = 'handoff.tree.prefs'

export const DEFAULT_PREFS: TreePrefs = {
  v: 1,
  hideIdleWorktrees: false,
  // 默认按「谁在动」而不是按名称：左栏的本职是回答「我该看哪」，
  // 按名字找项目已经有搜索框了
  projectSort: 'active',
  hiddenProjects: [],
}

// isPrefs 校验一份解析出来的对象是否真是 TreePrefs。
//
// 逐字段查类型而不是信 as：这份数据落在用户可手改的 localStorage 里，
// 一个 hiddenProjects: null 就能让整棵树渲染时崩掉。
function isPrefs(v: unknown): v is TreePrefs {
  if (typeof v !== 'object' || v === null) return false
  const p = v as Record<string, unknown>
  return (
    p.v === 1 &&
    typeof p.hideIdleWorktrees === 'boolean' &&
    (p.projectSort === 'active' || p.projectSort === 'name' || p.projectSort === 'recent') &&
    Array.isArray(p.hiddenProjects) &&
    p.hiddenProjects.every((x) => typeof x === 'string')
  )
}

// loadPrefs 读偏好；任何异常都静默回退默认值。
//
// 读不出偏好的正确反应是「按默认显示」而不是报错打断——它是视图偏好，
// 不是业务数据。但会 console.warn 一次带上被丢弃的原文，坏偏好是真实排查线索。
export function loadPrefs(): TreePrefs {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(PREFS_KEY)
  } catch {
    return DEFAULT_PREFS   // 隐私模式下 localStorage 可能直接抛
  }
  if (raw === null) return DEFAULT_PREFS
  try {
    const parsed: unknown = JSON.parse(raw)
    if (isPrefs(parsed)) return parsed
    console.warn('[treePrefs] 偏好形状不认识，已回退默认值：', raw.slice(0, 200))
  } catch (err) {
    console.warn('[treePrefs] 偏好不是合法 JSON，已回退默认值：', raw.slice(0, 200), err)
  }
  return DEFAULT_PREFS
}

// savePrefs 落盘一份偏好；写失败只警告不抛（配额满/隐私模式）。
export function savePrefs(p: TreePrefs): void {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(p))
  } catch (err) {
    console.warn('[treePrefs] 偏好写入失败，本次改动只在内存里生效', err)
  }
}

// ProjectMetrics 是一个项目行的三个排序键。
// updatedAt 是该项目下任务 updated_at 的最大值；空串 = 一条任务都没有，视为最旧。
export interface ProjectMetrics {
  active: number
  updatedAt: string
  name: string
}

// timeRank 把 RFC3339 字符串换成可比较的毫秒；空串与非法值都当最旧。
function timeRank(s: string): number {
  if (s === '') return -Infinity
  const t = Date.parse(s)
  return Number.isNaN(t) ? -Infinity : t
}

// sortProjects 返回排好序的新数组，不改入参。
//
// 三档的末位一律以名称升序兜底：这不是排序意图，是**稳定性**。前键全等时若不给
// 确定次序，行会随每次 2.5s 任务流心跳无缘无故重排（与 sortWorkspaces 末位的
// path ↑ 同一条理由）。
export function sortProjects<T>(list: T[], metricsOf: (x: T) => ProjectMetrics, mode: ProjectSort): T[] {
  return [...list].sort((a, b) => {
    const ma = metricsOf(a)
    const mb = metricsOf(b)
    if (mode === 'active' && ma.active !== mb.active) return mb.active - ma.active
    if (mode === 'recent') {
      const ra = timeRank(ma.updatedAt)
      const rb = timeRank(mb.updatedAt)
      if (ra !== rb) return rb - ra
    }
    return ma.name.localeCompare(mb.name)
  })
}

// splitHiddenProjects 按隐藏名单剔除项目，并报出剔了几个。
//
// 返回 hiddenCount 而不是 hidden 列表：界面只需要在「项目 N」旁说一句
// 「已隐藏 2」，被藏起来的项目不在树上出现（要拿回来去菜单里勾）。
export function splitHiddenProjects<T>(
  list: T[],
  idOf: (x: T) => string,
  hidden: string[],
): { shown: T[]; hiddenCount: number } {
  if (hidden.length === 0) return { shown: list, hiddenCount: 0 }
  const set = new Set(hidden)
  const shown = list.filter((x) => !set.has(idOf(x)))
  return { shown, hiddenCount: list.length - shown.length }
}

// WorkspaceIdleInfo 是折叠判据的三个输入。
// active = 该目录下 running + waiting_answer + waiting_review 的任务数。
export interface WorkspaceIdleInfo {
  isMain: boolean
  selected: boolean
  active: number
}

// splitIdleWorkspaces 把无活跃任务的目录拆到 hidden 里，保持原顺序。
//
// 两条恒不折叠的豁免：
//   - 主工作树：它是项目在这台机器上的家，不是任务分支。藏掉它，用户对
//     「主目录在第一行」的肌肉记忆当场失效
//   - 当前选中目录：选中态的行凭空消失是 bug 观感，不是「界面变干净了」
//
// hideIdle=false 时原样返回（连数组都不新建），调用方在搜索期间靠传 false 旁路。
export function splitIdleWorkspaces<T>(
  list: T[],
  infoOf: (x: T) => WorkspaceIdleInfo,
  hideIdle: boolean,
): { shown: T[]; hidden: T[] } {
  if (!hideIdle) return { shown: list, hidden: [] }
  const shown: T[] = []
  const hidden: T[] = []
  for (const x of list) {
    const info = infoOf(x)
    if (info.isMain || info.selected || info.active > 0) shown.push(x)
    else hidden.push(x)
  }
  return { shown, hidden }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/treePrefs.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/app/tree/treePrefs.ts web/src/app/tree/treePrefs.test.ts
git commit -m "feat(web): 左栏显示偏好的纯函数层"
```

---

## Task 6: IconMenu 支持勾选项 / 单选项 / 分组标题

**Files:**
- Modify: `web/src/app/lib/IconMenu.tsx`
- Modify: `web/src/app/lib/IconMenu.test.tsx`

**Interfaces:**
- Produces: `IconMenuItem` 扩展字段 `kind?: 'action' | 'check' | 'radio' | 'header'`、`checked?: boolean`、`keepOpen?: boolean`，`onSelect` 变为可选（`header` 不需要）

**为什么扩展它而不是新写一个弹层**：portal 到 body、点外部关闭、Esc 关闭，以及那条「mousedown 时不能关否则 click 永远不发生」的坑，它全趟过并有测试兜着。重写一份等于重新踩。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/lib/IconMenu.test.tsx` 末尾追加：

```ts
describe('扩展项', () => {
  it('check 选中时渲染勾、未选中不渲染', () => {
    render(
      <IconMenu
        label="偏好"
        icon={<span>i</span>}
        items={[
          { key: 'a', label: '隐藏空闲', kind: 'check', checked: true, onSelect: vi.fn() },
          { key: 'b', label: '别的开关', kind: 'check', checked: false, onSelect: vi.fn() },
        ]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    expect(screen.getByRole('menuitemcheckbox', { name: /隐藏空闲/ })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('menuitemcheckbox', { name: /别的开关/ })).toHaveAttribute('aria-checked', 'false')
  })

  it('keepOpen 的项点完菜单还在', () => {
    const onSelect = vi.fn()
    render(
      <IconMenu label="偏好" icon={<span>i</span>}
        items={[{ key: 'a', label: '隐藏空闲', kind: 'check', checked: false, keepOpen: true, onSelect }]} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏空闲/ }))
    expect(onSelect).toHaveBeenCalled()
    expect(screen.getByRole('menu')).toBeInTheDocument()
  })

  it('header 不可点', () => {
    render(
      <IconMenu label="偏好" icon={<span>i</span>}
        items={[{ key: 'h', label: '显示', kind: 'header' }]} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    expect(screen.queryByRole('menuitem', { name: '显示' })).toBeNull()
    expect(screen.getByText('显示')).toBeInTheDocument()
  })

  it('radio 用 menuitemradio 角色', () => {
    render(
      <IconMenu label="偏好" icon={<span>i</span>}
        items={[{ key: 'r', label: '活跃优先', kind: 'radio', checked: true, keepOpen: true, onSelect: vi.fn() }]} />,
    )
    fireEvent.click(screen.getByRole('button', { name: '偏好' }))
    expect(screen.getByRole('menuitemradio', { name: /活跃优先/ })).toHaveAttribute('aria-checked', 'true')
  })

  it('不带 kind 的老用法照旧：点完就关', () => {
    const onSelect = vi.fn()
    render(<IconMenu label="操作" icon={<span>i</span>} items={[{ key: 'a', label: '关闭', onSelect }]} />)
    fireEvent.click(screen.getByRole('button', { name: '操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '关闭' }))
    expect(onSelect).toHaveBeenCalled()
    expect(screen.queryByRole('menu')).toBeNull()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/lib/IconMenu.test.tsx`
Expected: FAIL（新用例失败，老用例仍 PASS）

- [ ] **Step 3: 写实现**

改 `web/src/app/lib/IconMenu.tsx`：

接口改成（保留原有字段与注释，只加下面几项）：

```ts
export interface IconMenuItem {
  key: string
  label: string
  // kind 决定这一项是什么：缺省 'action'（点了就关，老用法一字不改）、
  // 'check' 独立开关、'radio' 单选组成员、'header' 不可点的分组标题。
  kind?: 'action' | 'check' | 'radio' | 'header'
  checked?: boolean   // check/radio 的选中态
  // keepOpen=true 时点完不关菜单。连着调三个开关是常态，每点一次就关掉
  // 等于逼人开三次。
  keepOpen?: boolean
  icon?: ReactNode
  hotkey?: string
  onSelect?: () => void   // header 不需要
}
```

菜单容器加高度上限（在原有 className 里追加）：

```ts
              'fixed z-[60] max-h-[min(60vh,420px)] min-w-40 overflow-y-auto rounded-md border p-1 shadow-lg',
```

`items.map` 整体替换为：

```tsx
            {items.map((item) => {
              const kind = item.kind ?? 'action'
              if (kind === 'header') {
                return (
                  <div
                    key={item.key}
                    className={cn(
                      'px-2 pb-0.5 pt-1.5 text-[10px] font-medium uppercase tracking-wide',
                      dark ? 'text-[#8e9bab]' : 'text-muted-foreground',
                    )}
                  >
                    {item.label}
                  </div>
                )
              }
              const role = kind === 'check' ? 'menuitemcheckbox' : kind === 'radio' ? 'menuitemradio' : 'menuitem'
              return (
                <button
                  key={item.key}
                  type="button"
                  role={role}
                  aria-checked={kind === 'action' ? undefined : item.checked === true}
                  onClick={() => {
                    // keepOpen 的项不关菜单；关闭要排在 onSelect 之前，
                    // 与改造前的次序保持一致（onSelect 可能自己再开别的层）
                    if (!item.keepOpen) setOpen(false)
                    item.onSelect?.()
                  }}
                  className={cn(
                    'flex w-full cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs',
                    dark ? 'text-[#d7dde5] hover:bg-[#1a2430]' : 'hover:bg-accent',
                  )}
                >
                  {/* 选中标记占等宽位：不占位的话，勾选状态一变行文字就左右跳 */}
                  {kind !== 'action' && (
                    <span className="flex size-3.5 shrink-0 items-center justify-center">
                      {item.checked === true &&
                        (kind === 'check' ? (
                          <Check className="size-3.5" />
                        ) : (
                          <span className="size-1.5 rounded-full bg-current" />
                        ))}
                    </span>
                  )}
                  {item.icon}
                  <span className="flex-1">{item.label}</span>
                  {item.hotkey !== undefined && (
                    <span className={cn('font-mono text-[10px]', dark ? 'text-[#8e9bab]' : 'text-muted-foreground')}>
                      {item.hotkey}
                    </span>
                  )}
                </button>
              )
            })}
```

补 import：`import { Check } from 'lucide-react'`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/lib/IconMenu.test.tsx && npx vitest run`
Expected: 全部 PASS（**老用例必须一条都没红**，红了说明破坏了向后兼容）

- [ ] **Step 5: Commit**

```bash
git add web/src/app/lib/IconMenu.tsx web/src/app/lib/IconMenu.test.tsx
git commit -m "feat(web): IconMenu 支持勾选项、单选项与分组标题"
```

---

## Task 7: 偏好菜单接进左栏树

**Files:**
- Create: `web/src/app/tree/TreePrefsMenu.tsx`
- Modify: `web/src/app/tree/ProjectTree.tsx`
- Modify: `web/src/app/tree/ProjectTree.test.tsx`

**Interfaces:**
- Consumes: Task 5 的全部导出；Task 6 的 `IconMenu`；既有的 `countsForProject(tasks, project)`、`sortWorkspaces`、`filterTree`
- Produces: `TreePrefsMenu({ prefs, projects, onChange })`，其中 `projects: { project_id: string; name: string }[]`

- [ ] **Step 1: 写 TreePrefsMenu**

新建 `web/src/app/tree/TreePrefsMenu.tsx`：

```tsx
// TreePrefsMenu —— 左栏「项目 N」那行右侧的显示偏好菜单（spec §1.2）。
//
// 职责：把 TreePrefs 与项目列表装配成 IconMenu 的 items，仅此而已。
//
// 边界：
//   - 不持有状态、不落盘：改动经 onChange 交回 ProjectTree，由它统一 setState + savePrefs
//   - **projects 必须是未经隐藏过滤的全量项目**：被藏起来的项目要能在菜单里勾回来，
//     传过滤后的列表等于藏一个少一个、再也拿不回来
//   - 触发器用推子图标而不是齿轮：左栏底部已有一个齿轮通向设置页，两个齿轮
//     会让人以为点哪个都一样，而它们不是一类东西
import { SlidersHorizontal } from 'lucide-react'
import { IconMenu, type IconMenuItem } from '../lib/IconMenu'
import type { ProjectSort, TreePrefs } from './treePrefs'

export interface TreePrefsMenuProps {
  prefs: TreePrefs
  projects: { project_id: string; name: string }[]
  onChange: (next: TreePrefs) => void
}

// SORT_LABELS 是三档排序的人话标签，顺序即菜单里的顺序。
const SORT_LABELS: { value: ProjectSort; label: string }[] = [
  { value: 'active', label: '活跃优先' },
  { value: 'name', label: '名称' },
  { value: 'recent', label: '最近活动' },
]

export function TreePrefsMenu({ prefs, projects, onChange }: TreePrefsMenuProps) {
  const hidden = new Set(prefs.hiddenProjects)
  const items: IconMenuItem[] = [
    { key: 'h-display', label: '显示', kind: 'header' },
    {
      key: 'hide-idle',
      label: '隐藏无活跃任务的工作树',
      kind: 'check',
      checked: prefs.hideIdleWorktrees,
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, hideIdleWorktrees: !prefs.hideIdleWorktrees }),
    },
    { key: 'h-sort', label: '排序方式', kind: 'header' },
    ...SORT_LABELS.map((s) => ({
      key: `sort-${s.value}`,
      label: s.label,
      kind: 'radio' as const,
      checked: prefs.projectSort === s.value,
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, projectSort: s.value }),
    })),
    { key: 'h-projects', label: `项目 · ${projects.length}`, kind: 'header' },
    {
      key: 'all-on',
      label: '全选',
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, hiddenProjects: [] }),
    },
    {
      key: 'all-off',
      label: '全不选',
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, hiddenProjects: projects.map((p) => p.project_id) }),
    },
    ...projects.map((p) => ({
      key: `p-${p.project_id}`,
      label: p.name,
      kind: 'check' as const,
      checked: !hidden.has(p.project_id),
      keepOpen: true,
      onSelect: () => {
        const next = new Set(prefs.hiddenProjects)
        // 勾 = 从隐藏名单里拿掉；取消勾 = 加进名单。名单存的是「不显示谁」，
        // 所以这里的取反方向与直觉相反，改的时候看清楚
        if (next.has(p.project_id)) next.delete(p.project_id)
        else next.add(p.project_id)
        onChange({ ...prefs, hiddenProjects: [...next] })
      },
    })),
  ]
  return (
    <IconMenu
      label="显示偏好"
      icon={<SlidersHorizontal className="size-3.5" />}
      items={items}
      className="rounded p-0.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
    />
  )
}
```

- [ ] **Step 2: 写 ProjectTree 的失败测试**

在 `web/src/app/tree/ProjectTree.test.tsx` 顶部的 import 之后加一行清理（**必须加**，偏好落 localStorage，用例之间会串味）：

```ts
beforeEach(() => localStorage.clear())
```

并在文件末尾追加：

```ts
describe('显示偏好', () => {
  it('取消勾选项目后它不在树上，「项目 N」旁说明已隐藏几个', () => {
    render(<ProjectTree {...props({})} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /handoff/ }))
    expect(screen.queryByText('handoff')).toBeNull()
    expect(screen.getByTestId('project-count')).toHaveTextContent('0')
    expect(screen.getByText(/已隐藏 1/)).toBeInTheDocument()
  })

  it('开「隐藏无活跃任务的工作树」后，没有活跃任务的目录收进「已隐藏」行，点开还能看到', () => {
    // 默认树里 /w 是主目录（豁免），/w/b2-b3 挂着一条 running 任务。
    // 把那条任务改成 done，它就成了空闲目录
    const p = props({})
    const tasks = p.tasks.map((t) => ({ ...t, state: 'done' }))
    render(<ProjectTree {...p} tasks={tasks} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏无活跃任务的工作树/ }))
    expect(screen.queryByText('integration/b2-b3')).toBeNull()
    fireEvent.click(screen.getByText(/已隐藏 1 个目录/))
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
  })

  it('主工作树与当前选中目录不会被折叠', () => {
    const p = props({ selectedKey: '/w/b2-b3' })
    const tasks = p.tasks.map((t) => ({ ...t, state: 'done' }))
    render(<ProjectTree {...p} tasks={tasks} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏无活跃任务的工作树/ }))
    expect(screen.getByText('main')).toBeInTheDocument()          // 主目录
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument() // 选中目录
    expect(screen.queryByText(/已隐藏/)).toBeNull()
  })

  it('搜索期间旁路隐藏偏好：藏起来的项目照样能被搜出来', () => {
    render(<ProjectTree {...props({})} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /handoff/ }))
    expect(screen.queryByText('handoff')).toBeNull()
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), { target: { value: 'handoff' } })
    expect(screen.getByText('handoff')).toBeInTheDocument()
  })
})
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: FAIL（找不到「显示偏好」按钮）

- [ ] **Step 4: 改 ProjectTree**

改动分五处（其余一字不动）：

**(a) 顶部 import 与状态：**

```tsx
import { TreePrefsMenu } from './TreePrefsMenu'
import { loadPrefs, savePrefs, sortProjects, splitHiddenProjects, splitIdleWorkspaces, type TreePrefs } from './treePrefs'
```

在 `const [query, setQuery] = useState('')` 附近加：

```tsx
  // 显示偏好：初值从 localStorage 读一次（惰性初始化，不要每次渲染都读）。
  // 改动统一走 updatePrefs——落盘与 setState 必须成对，分开写迟早漏一处
  const [prefs, setPrefs] = useState<TreePrefs>(() => loadPrefs())
  const updatePrefs = (next: TreePrefs) => {
    setPrefs(next)
    savePrefs(next)
  }
  // 「已隐藏 N 个目录」的展开状态：**刻意不落盘**——它是一次性的「我现在想看看」，
  // 不是长期设定。键用机器节点 key
  const [openHiddenDirs, setOpenHiddenDirs] = useState<Set<string>>(new Set())
  const toggleHiddenDirs = (key: string) =>
    setOpenHiddenDirs((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
```

**(b) 项目列表的隐藏与排序**（放在 `const unassigned = ...` 之前）：

```tsx
  // 搜索期间旁路「隐藏」类偏好：搜到了却被偏好过滤掉，等于搜索坏了。
  // 排序不旁路——排序不会让东西消失，跟着当前档反而更连贯
  const projectSplit = searching
    ? { shown: filtered.projects, hiddenCount: 0 }
    : splitHiddenProjects(filtered.projects, (p) => p.project_id, prefs.hiddenProjects)
  // 项目的「最近活动」= 该项目下任务 updated_at 的最大值；一条任务都没有时为空串
  const lastActivity = (projectID: string) =>
    tasks.reduce((max, t) => (t.project_id === projectID && t.updated_at > max ? t.updated_at : max), '')
  const orderedProjects = sortProjects(
    projectSplit.shown,
    (p) => {
      const c = countsForProject(tasks, p)
      return { active: c.running + c.pending, updatedAt: lastActivity(p.project_id), name: p.name }
    },
    prefs.projectSort,
  )
```

把渲染入口 `filtered.projects.map((project) => {` 改成 `orderedProjects.map((project) => {`。

**(c) 标题行加计数说明与菜单触发器**（替换现有那个 `<div className="flex items-center gap-1 px-3 ...">` 块）：

```tsx
      <div className="flex items-center gap-1 px-3 pb-1 pt-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        <span>项目</span>
        <span data-testid="project-count" className="font-normal text-muted-foreground/70">
          {orderedProjects.length}
        </span>
        {/* 藏了东西就必须说一声：偏好可以让它不占地方，但不能让人以为它不存在 */}
        {projectSplit.hiddenCount > 0 && (
          <span className="font-normal normal-case text-muted-foreground/70">· 已隐藏 {projectSplit.hiddenCount}</span>
        )}
        <span className="ml-auto">
          {/* 菜单吃的是**原树**的项目，不是过滤后的：藏起来的项目要能勾回来 */}
          <TreePrefsMenu
            prefs={prefs}
            projects={tree.projects.map((p) => ({ project_id: p.project_id, name: p.name }))}
            onChange={updatePrefs}
          />
        </span>
      </div>
```

**(d) 目录行的折叠**：把现有的

```tsx
                      sortWorkspaces(loc.workspaces, (ws) => wsMetrics(project, loc.machine, ws)).map((ws) => {
```

这一段改成：**先算出 sorted，再拆分，然后把原来的行渲染体抽成一个局部函数 `renderWorkspace(ws)`**（原样搬，一个字不改），最后：

```tsx
                    {problem === '' && mOpen && (() => {
                      const sorted = sortWorkspaces(loc.workspaces, (ws) => wsMetrics(project, loc.machine, ws))
                      const split = splitIdleWorkspaces(
                        sorted,
                        (ws) => {
                          const c = wsCounts(project, loc.machine, ws)
                          return {
                            isMain: ws.is_main,
                            selected: selectedKey === ws.path,
                            active: c.running + c.pending,
                          }
                        },
                        // 搜索期间不折叠，理由同项目隐藏
                        prefs.hideIdleWorktrees && !searching,
                      )
                      const hiddenOpen = openHiddenDirs.has(mKey)
                      return (
                        <>
                          {split.shown.map(renderWorkspace)}
                          {split.hidden.length > 0 && (
                            <>
                              <button
                                type="button"
                                data-testid="hidden-dirs-row"
                                aria-expanded={hiddenOpen}
                                onClick={() => toggleHiddenDirs(mKey)}
                                className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60')}
                                style={{ paddingLeft: 8 + 32 }}
                              >
                                <Arrow open={hiddenOpen} onToggle={() => toggleHiddenDirs(mKey)} />
                                <span className="min-w-0 flex-1 truncate">已隐藏 {split.hidden.length} 个目录</span>
                              </button>
                              {hiddenOpen && split.hidden.map(renderWorkspace)}
                            </>
                          )}
                        </>
                      )
                    })()}
```

`renderWorkspace` 定义在该 `loc` 的 map 闭包内（它要用到 `project` / `loc`），签名 `(ws: Workspace) => JSX.Element`，函数体就是现在那个 `.map((ws) => { ... })` 的回调体，**包括 `key={base.key}`**。

**(e)** 不改任何既有回调与 props。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/ && npx tsc -b`
Expected: 全部 PASS（既有的 ProjectTree 用例一条都不许红）

- [ ] **Step 6: 补日志与注释自查**

- 偏好读取失败已在 `treePrefs.ts` 里 `console.warn`（Task 5 已完成）✓
- `TreePrefsMenu` 有文件头注释（职责 + 三条边界）✓
- ProjectTree 里三处非显然逻辑有「为什么」注释：搜索旁路、隐藏名单取反方向、展开状态不落盘 ✓

- [ ] **Step 7: Commit**

```bash
git add web/src/app/tree
git commit -m "feat(web): 左栏显示偏好菜单（隐藏项目/折叠空闲目录/项目排序）"
```

---

## Task 8: 建树弹层

**Files:**
- Create: `web/src/app/tree/NewWorktreeDialog.tsx`
- Create: `web/src/app/tree/NewWorktreeDialog.test.tsx`

**Interfaces:**
- Consumes: Task 4 的 `fetchProjectBranches` / `createWorktree`；既有的 `errorMessage`（`../lib/format`）、`Button`（`@/components/ui/button`）
- Produces: `NewWorktreeDialog({ open, projectName, machine, onClose, onCreated })`，`onCreated: (ws: Workspace) => void`

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/tree/NewWorktreeDialog.test.tsx`：

```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { NewWorktreeDialog } from './NewWorktreeDialog'
import * as client from '../../api/client'

const branches = {
  branches: [
    { name: 'main', worktree: '/w' },
    { name: 'feat/free', worktree: '' },
  ],
  default: 'main',
  worktree_root: '/data/worktrees/manual',
}

beforeEach(() => vi.restoreAllMocks())

function open(over: Partial<Parameters<typeof NewWorktreeDialog>[0]> = {}) {
  return render(
    <NewWorktreeDialog
      open
      projectName="handoff"
      machine="mac-02"
      onClose={vi.fn()}
      onCreated={vi.fn()}
      {...over}
    />,
  )
}

describe('建树弹层', () => {
  it('打开时拉分支列表，基线默认选中 default，落点根如实回显', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toHaveValue('main'))
    expect(client.fetchProjectBranches).toHaveBeenCalledWith('handoff', 'mac-02')
    expect(screen.getByText(/\/data\/worktrees\/manual/)).toBeInTheDocument()
  })

  it('推导出的基线是 origin/main 这种远端名时，下拉里也得有它（否则显示为空白）', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue({
      branches: [{ name: 'main', worktree: '/w' }],
      default: 'origin/main',
      worktree_root: '/data/worktrees/manual',
    })
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toHaveValue('origin/main'))
    expect(screen.getByRole('option', { name: 'origin/main' })).toBeInTheDocument()
  })

  it('检出已有分支模式下，被占用的分支不可选且标出占用者', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('检出已有分支'))
    const opt = screen.getByRole('option', { name: /main（已被 \/w 占用）/ }) as HTMLOptionElement
    expect(opt.disabled).toBe(true)
  })

  it('创建成功把新工作树交回调用方', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    const ws = { path: '/data/worktrees/manual/feat-x', branch: 'feat/x', head: 'abc', is_main: false, managed: true, created_at: '' }
    vi.spyOn(client, 'createWorktree').mockResolvedValue(ws)
    const onCreated = vi.fn()
    open({ onCreated })
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/x' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(ws))
    expect(client.createWorktree).toHaveBeenCalledWith('handoff', { mode: 'new_branch', branch: 'feat/x', base: 'main' }, 'mac-02')
  })

  it('创建失败把 agentd 原文贴出来，不缩略成「操作失败」', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    vi.spyOn(client, 'createWorktree').mockRejectedValue(new Error('分支 feat/x 已存在，请换个名字'))
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('分支名'), { target: { value: 'feat/x' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(screen.getByText(/分支 feat\/x 已存在，请换个名字/)).toBeInTheDocument())
  })

  it('分支名为空时创建按钮禁用', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockResolvedValue(branches)
    open()
    await waitFor(() => expect(screen.getByLabelText('基线')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('拉分支失败时给原文与重试', async () => {
    vi.spyOn(client, 'fetchProjectBranches').mockRejectedValue(new Error('机器 mac-02 不可达'))
    open()
    await waitFor(() => expect(screen.getByText(/机器 mac-02 不可达/)).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/NewWorktreeDialog.test.tsx`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 写实现**

新建 `web/src/app/tree/NewWorktreeDialog.tsx`：

```tsx
// NewWorktreeDialog —— 在某台机器的某个项目位置上新建工作树（spec §2.2）。
//
// 入口：左栏机器行 hover 出现的 + 按钮，以及机器行右键菜单的「新建工作树」。
//
// 两种模式与 dispatch 的 --new-branch / --branch 对齐：
//   - 新建分支：填分支名 + 选基线（基线默认取服务端推导的 default）
//   - 检出已有分支：选一个尚未被别的工作树占用的分支
// 被占用的分支**列出但置灰**并标出占用者——直接不列会让人以为分支丢了。
//
// 边界：
//   - **只建树，不派任务**：本期 web 端没有 dispatch 表单，建完的树照旧走 CLI 派发
//   - 不自己拼落点完整路径：目录名的生成规则只有服务端一份，前端复刻必然分叉，
//     所以只回显服务端给的落点根
//   - 不刷新项目树、不选中新目录：那是 ProjectTree/Shell 的职责，这里只把
//     新工作树交回去（onCreated）
import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { createWorktree, fetchProjectBranches } from '../../api/client'
import type { ProjectBranchesResp, Workspace } from '../../api/types'
import { Button } from '@/components/ui/button'
import { errorMessage } from '../lib/format'

export interface NewWorktreeDialogProps {
  open: boolean
  // projectName 是**登记名**（ProjectLocationNode.name）。跨机时它与 ProjectNode.name
  // 可能不同，接口按登记名寻址，传错会 404。
  projectName: string
  machine: string
  onClose: () => void
  onCreated: (ws: Workspace) => void
}

// INPUT_CLASS 与项目编辑弹层的输入框保持一字不差，界面词汇统一。
const INPUT_CLASS = 'h-8 w-full rounded-md border border-input bg-background px-2.5 text-xs shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring'

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(machine: string): string {
  return machine === '' ? '本机' : machine
}

// baseOptions 是基线下拉的选项：本地分支表，外加**服务端推导出的 default**。
//
// 为什么要额外并进 default：服务端的推导优先取 origin/HEAD，返回的是
// "origin/main" 这种远端跟踪分支名，它不在本地分支表里。不并进来，下拉的 value
// 就落不到任何 option 上，选择框显示为空白——用户看到的是「基线没选」，
// 而实际上它有值且完全合法（git worktree add 认远端跟踪分支作起点）。
function baseOptions(data: ProjectBranchesResp): string[] {
  const names = data.branches.map((b) => b.name)
  if (data.default !== '' && !names.includes(data.default)) return [data.default, ...names]
  return names
}

export function NewWorktreeDialog({ open, projectName, machine, onClose, onCreated }: NewWorktreeDialogProps) {
  const [mode, setMode] = useState<'new_branch' | 'existing_branch'>('new_branch')
  const [branch, setBranch] = useState('')
  const [base, setBase] = useState('')
  const [existing, setExisting] = useState('')
  const [data, setData] = useState<ProjectBranchesResp | null>(null)
  const [loadError, setLoadError] = useState('')
  const [submitError, setSubmitError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  // reloadKey 只为「重试」按钮服务：改它就重跑下面那个 effect
  const [reloadKey, setReloadKey] = useState(0)

  // 每次打开都重置：弹层是复用的同一个实例，不重置会把上一次的输入与报错带过来
  useEffect(() => {
    if (!open) return
    setMode('new_branch')
    setBranch('')
    setExisting('')
    setSubmitError('')
    setData(null)
    setLoadError('')
    let alive = true
    fetchProjectBranches(projectName, machine)
      .then((resp) => {
        if (!alive) return
        setData(resp)
        setBase(resp.default)
      })
      .catch((err) => {
        if (!alive) return
        // 原文透出：这里最常见的失败是「机器不可达」，缩略成「加载失败」
        // 就把唯一可行动的信息弄丢了
        setLoadError(errorMessage(err))
      })
    // alive 挡住「弹层已关但请求才回来」的 setState
    return () => { alive = false }
  }, [open, projectName, machine, reloadKey])

  if (!open) return null

  const free = (data?.branches ?? []).filter((b) => b.worktree === '')
  const canSubmit =
    !submitting && data !== null &&
    (mode === 'new_branch' ? branch.trim() !== '' && base !== '' : existing !== '')

  const submit = async () => {
    setSubmitting(true)
    setSubmitError('')
    try {
      const ws = await createWorktree(
        projectName,
        mode === 'new_branch'
          ? { mode, branch: branch.trim(), base }
          : { mode, branch: existing, base: '' },
        machine,
      )
      onCreated(ws)
      onClose()
    } catch (err) {
      setSubmitError(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div role="dialog" aria-label="新建工作树" className="w-[420px] rounded-lg border bg-background p-4 shadow-lg">
        <div className="mb-3 flex items-center">
          <h2 className="text-sm font-semibold">新建工作树</h2>
          <button type="button" aria-label="关闭" onClick={onClose} className="ml-auto text-muted-foreground hover:text-foreground">
            <X className="size-4" />
          </button>
        </div>
        <p className="mb-3 text-xs text-muted-foreground">
          项目 {projectName} · {machineLabel(machine)}
        </p>

        {loadError !== '' ? (
          <div className="space-y-2">
            <p className="break-words text-xs text-destructive">{loadError}</p>
            <Button size="sm" variant="outline" onClick={() => setReloadKey((k) => k + 1)}>重试</Button>
          </div>
        ) : data === null ? (
          <p className="text-xs text-muted-foreground">正在读取分支…</p>
        ) : (
          <div className="space-y-3">
            <label className="flex items-center gap-2 text-xs">
              <input type="radio" aria-label="新建分支" checked={mode === 'new_branch'} onChange={() => setMode('new_branch')} />
              <span>新建分支</span>
            </label>
            {mode === 'new_branch' && (
              <div className="space-y-2 pl-5">
                <label className="block text-xs">
                  <span className="mb-1 block text-muted-foreground">分支名</span>
                  <input aria-label="分支名" className={INPUT_CLASS} value={branch} onChange={(e) => setBranch(e.target.value)} />
                </label>
                <label className="block text-xs">
                  <span className="mb-1 block text-muted-foreground">基线</span>
                  <select aria-label="基线" className={INPUT_CLASS} value={base} onChange={(e) => setBase(e.target.value)}>
                    {baseOptions(data).map((name) => (
                      <option key={name} value={name}>{name}</option>
                    ))}
                  </select>
                </label>
              </div>
            )}

            <label className="flex items-center gap-2 text-xs">
              <input type="radio" aria-label="检出已有分支" checked={mode === 'existing_branch'} onChange={() => setMode('existing_branch')} />
              <span>检出已有分支</span>
            </label>
            {mode === 'existing_branch' && (
              <div className="pl-5">
                <select
                  aria-label="已有分支"
                  className={INPUT_CLASS}
                  value={existing}
                  onChange={(e) => setExisting(e.target.value)}
                >
                  <option value="">请选择</option>
                  {data.branches.map((b) => (
                    <option key={b.name} value={b.name} disabled={b.worktree !== ''}>
                      {b.worktree !== '' ? `${b.name}（已被 ${b.worktree} 占用）` : b.name}
                    </option>
                  ))}
                </select>
                {free.length === 0 && (
                  <p className="mt-1 text-[11px] text-muted-foreground">所有分支都已被工作树检出，请改用「新建分支」</p>
                )}
              </div>
            )}

            <p className="text-[11px] text-muted-foreground">
              将建在 {data.worktree_root} 下，目录名按分支名生成
            </p>
            {submitError !== '' && <p className="break-words text-xs text-destructive">{submitError}</p>}
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>取消</Button>
              <Button size="sm" onClick={submit} disabled={!canSubmit}>{submitting ? '创建中…' : '创建'}</Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/tree/NewWorktreeDialog.test.tsx && npx tsc -b`
Expected: PASS。若 `aria-label` 与 `getByRole('option', ...)` 的匹配方式与实现有出入，**改测试的选择器去贴合实现，不要改实现去迁就测试的写法**——断言的行为（默认基线、置灰、原文透出、回调实参）一条都不许放宽。

- [ ] **Step 5: Commit**

```bash
git add web/src/app/tree/NewWorktreeDialog.tsx web/src/app/tree/NewWorktreeDialog.test.tsx
git commit -m "feat(web): 新建工作树弹层"
```

---

## Task 9: 机器行入口与 Shell 接线

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`
- Modify: `web/src/app/tree/ProjectTree.test.tsx`
- Modify: `web/src/app/shell/Shell.tsx`

**Interfaces:**
- Consumes: Task 8 的 `NewWorktreeDialog`；既有的 `workspaceBase(project, machine, ws)`（本文件已导出）
- Produces: `ProjectTree` 新增可选 prop `onWorktreeCreated?: (project: ProjectNode, machine: string, ws: Workspace) => void`

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/tree/ProjectTree.test.tsx` 末尾追加：

```tsx
describe('机器行新建工作树', () => {
  it('传了 onWorktreeCreated 才给 + 按钮', () => {
    const { rerender } = render(<ProjectTree {...props({})} onWorktreeCreated={vi.fn()} />)
    expect(screen.getByRole('button', { name: '新建工作树' })).toBeInTheDocument()
    rerender(<ProjectTree {...props({})} />)
    expect(screen.queryByRole('button', { name: '新建工作树' })).toBeNull()
  })

  it('机器不可达时不给这个入口', () => {
    const p = props({})
    const tree = {
      ...p.tree,
      projects: [{
        ...p.tree.projects[0],
        locations: [{ ...p.tree.projects[0].locations[0], probe_error: 'ssh 超时' }],
      }],
    }
    render(<ProjectTree {...p} tree={tree} onWorktreeCreated={vi.fn()} />)
    expect(screen.queryByRole('button', { name: '新建工作树' })).toBeNull()
  })

  it('点 + 开弹层；右键菜单里也有同一个入口', () => {
    render(<ProjectTree {...props({})} onWorktreeCreated={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '新建工作树' }))
    expect(screen.getByRole('dialog', { name: '新建工作树' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    fireEvent.contextMenu(screen.getByTestId('machine-row'))
    expect(screen.getByText('新建工作树')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`
Expected: FAIL

- [ ] **Step 3: 改 ProjectTree**

**(a) props 加一项**（在 `onEdit?` 之后）：

```tsx
  // onWorktreeCreated 建完树后回调，由 Shell 刷新树并把新目录选为当前基准目录。
  // 与 onUnregister / onEdit 同一条规矩：没传就不给这个入口。
  onWorktreeCreated?: (project: ProjectNode, machine: string, ws: Workspace) => void
```

并加到解构参数里。

**(b) 弹层目标状态**（挨着 `unregisterTarget` 那几行）：

```tsx
  // 建树弹层的目标位置。project 与 loc 一起记：弹层要用 loc.name（登记名）寻址，
  // 而回调要把 project 交回去组装 BaseDir
  const [worktreeTarget, setWorktreeTarget] = useState<{ project: ProjectNode; loc: ProjectLocationNode } | null>(null)
```

**(c) 机器行右端让位 + 按钮**。把机器行 `<button>` 里的

```tsx
                        <RowCounts dirs={mCounts.dirs} running={mCounts.running} pending={mCounts.pending} />
```

改成

```tsx
                        {/* hover 时让位给 + 按钮：两者都要行右端，让位是唯一
                            不重叠的排法（此前的结论是「排不出来」，那是因为只
                            试过叠加）。用 invisible 而不是 hidden——保留占位，
                            行内其它元素不会因为 hover 左右位移 */}
                        <span className={cn(onWorktreeCreated && problem === '' && 'group-hover:invisible')}>
                          <RowCounts dirs={mCounts.dirs} running={mCounts.running} pending={mCounts.pending} />
                        </span>
```

在该 `<button>` **之后、`group relative` 容器之内**加：

```tsx
                      {onWorktreeCreated && problem === '' && (
                        <button
                          type="button"
                          aria-label="新建工作树"
                          title="新建工作树"
                          onClick={() => setWorktreeTarget({ project, loc })}
                          className="absolute right-2 top-1/2 hidden -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground group-hover:block"
                        >
                          <Plus className="size-3.5" />
                        </button>
                      )}
```

**(d) 右键菜单加一项**（`items` 数组里，排在「编辑」之前）：

```tsx
            ...(onWorktreeCreated
              ? [{
                  label: '新建工作树',
                  // hover 出现的 + 按钮对键盘/触屏不友好，右键是它的等价通道，
                  // 走的是同一个弹层
                  onSelect: () => {
                    const loc = menu.project.locations.find((l) => l.machine === menu.machine)
                    if (loc) setWorktreeTarget({ project: menu.project, loc })
                  },
                }]
              : []),
```

**(e) 渲染弹层**（挨着 `ConfirmDialog` 那块）：

```tsx
      {onWorktreeCreated && worktreeTarget && (
        <NewWorktreeDialog
          open
          projectName={worktreeTarget.loc.name}
          machine={worktreeTarget.loc.machine}
          onClose={() => setWorktreeTarget(null)}
          onCreated={(ws) => onWorktreeCreated(worktreeTarget.project, worktreeTarget.loc.machine, ws)}
        />
      )}
```

补 import：`NewWorktreeDialog`、类型 `Workspace`（`../../api/types` 已导入其它类型，加进去即可）。

- [ ] **Step 4: 接 Shell**

`web/src/app/shell/Shell.tsx`：把 `import { findBaseOfTask, ProjectTree } from '../tree/ProjectTree'` 改为同时导入 `workspaceBase`，并给 `<ProjectTree>` 加：

```tsx
            onWorktreeCreated={(project, machine, ws) => {
              // 先刷新树再选中：选中只改 useWorkbench 的 base，树上那一行要等
              // 这次 refresh 回来才会出现，两件事都必须做
              treeState.refresh()
              wb.select(workspaceBase(project, machine, ws))
            }}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run && npx tsc -b && npx eslint .`
Expected: 全部 PASS / 0 error

- [ ] **Step 6: Commit**

```bash
git add web/src/app/tree web/src/app/shell/Shell.tsx
git commit -m "feat(web): 机器行 hover 新建工作树入口并接进工作台"
```

---

## Task 10: 整分支终审

**Files:** 无新增；只做验证与记账

- [ ] **Step 1: 全量跑 Go 侧**

Run:
```bash
gofmt -l .
go build ./... && go vet ./... && go test ./...
```
Expected: `gofmt -l .` **无输出**（有输出就先 `gofmt -w` 那些文件再提交）；其余全绿。

- [ ] **Step 2: 全量跑前端**

Run:
```bash
cd web && npx tsc -b && npx eslint . && npx vitest run && npm run build
```
Expected: 全绿。记下 vitest 的 `N passed (M files)`，与基线（本分支起点上跑一次得到的数）一起写进 ledger。

- [ ] **Step 3: 相对分支起点做一次整体 diff 复审**

Run: `git diff --stat $(git merge-base HEAD origin/handoff/web-console)..HEAD`

逐条核对：
- 变更文件是否全在 File Structure 那张表里？多出来的文件要能说清为什么
- `git diff -- internal/ | grep -n "RemoveAll"` **必须无输出**（全局约束）
- 每个新文件都有文件头注释、每个导出函数都有 doc 注释
- 没有 `fmt.Printf` / `console.log` 当日志（`console.warn` 用于偏好读取失败是允许的，它是有意的诊断线索）

有发现项就**一次性全量修完**，再做一次范围复审；不搞逐项修，也没有第二轮修复波。

- [ ] **Step 4: 写 ledger**

在 `docs/superpowers/ledgers/` 下新建 `ledger-b114-sidebar-prefs.md`，逐 task 一行，含 commit 范围、每次自动化验证的**实得输出**（不是"应该通过"）。spec §7 的 5-7 条（需肉眼看页面的三项）**如实标「未验：无浏览器」**，不许猜通过。

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/ledgers
git commit -m "docs(ledger): B114 执行记录"
```
