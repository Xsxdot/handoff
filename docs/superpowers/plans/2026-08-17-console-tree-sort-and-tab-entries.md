# 控制台目录行排序与三条新入口 Implementation Plan

**Goal:** 左栏目录行按工单/任务/时间排序；空白 tab 加任务选择器与新建文件；左栏任务可拖入中央区分屏；右下角浮窗加临时文件。

**Architecture:** 两处后端改动（`Workspace.created_at` 现场 stat；`<DataDir>/scratch` 作为文件白名单第二入口，复用既有六个文件端点）。前端以纯函数打底（`sortWorkspaces`、`splitGroupAt`），组件层只做接线。一处前置重构：把「组下标」从 Shell 的确认弹层 state 里拔掉，换成按 tabId 反查——不做这步就无法安全地在中间插入分屏栏。

**Tech Stack:** Go 1.x（标准库 + 项目内 `log()` / `s.log`）；React 19 + TypeScript + Vite + Tailwind 4 + lucide-react；vitest + @testing-library/react。

**Spec:** `docs/superpowers/specs/2026-08-17-console-tree-sort-and-tab-entries-design.md`（本计划的每条决定都能在 spec 里找到依据；两者冲突以 spec 为准并上报）

## Global Constraints

这一节的每条都是**所有 task 隐含的验收项**，不在各 task 里重复。

**注释与日志（用户 CLAUDE.md §2 + instrumenting-code）：**
- 每个新建文件顶部写「职责 + 边界」块注释（Go 用 package 注释，TS 用文件头块注释），中文。
- 每个导出函数/方法写注释：参数、返回、注意事项。
- 复杂逻辑与边界条件写中文行内注释，解释**为什么**而不是做了什么。与代码重复的注释是噪音，删掉。
- Go 侧日志一律 `log()` / `s.log`（本仓既有用法），**禁止 `fmt.Printf`**。错误分支必须带上下文（路径、机器名、cause）。探测类竞态失败用 Debug，可展示的状态用 Warn，真故障用 Error。
- 前端不新增 `console.log`；已有的 `console.warn` 用法（如 `useMachineCaps` 的降级提示）可照抄其风格。

**契约同步（改任何线格式字段时三处必须一起改，缺一处契约测试红）：**
1. `internal/proto/*.go` 的结构体
2. `web/src/api/types.ts` 的对应接口
3. `web/src/api/testdata/*.json` —— **不手写**，跑 `go test ./internal/proto/ -run TestContractFixtures -update` 生成

**验证命令（每个 task 的验证步骤都从这几条里选）：**
```
go build ./...
go test ./internal/agentd/ ./internal/proto/
gofmt -l .                      # 必须无输出
cd web && npm run test          # vitest run
cd web && npm run lint
cd web && npm run typecheck
```

**`gofmt -l .` 无输出是硬性验收项**，不是可选步骤：测试全绿不等于格式干净，而格式脏会在合并时被打回。

**环境纪律：**
- **不要设置 `TMPDIR` 指向仓库内的任何路径**。那会让一族 git 相关测试假红（现象是「实得 nil」且路径里带 `.gotmp`），排查会浪费掉半小时。用系统默认 TMPDIR。
- 这份计划跑在一台**同时运行着生产 agentd** 的机器上。**禁止宽泛 `pkill`、禁止 `killall`、禁止 Ctrl-C 式的批量终止**。需要重启服务时只重启你自己起的进程，或者干脆不起——本计划的全部验证都能靠单元测试完成，不需要跑起 agentd。

**提交纪律：** 每个 task 完成即 commit，提交信息用各 task「Commit」步骤里给定的原文。

---

## 文件结构

### 后端（Go）

| 文件 | 责任 | 动作 |
|------|------|------|
| `internal/proto/projects.go` | 项目树线格式 | 加 `Workspace.CreatedAt`、`Machine.ScratchRoot` |
| `internal/proto/status.go` | 状态线格式 | 加 `StatusResp.ScratchRoot` |
| `internal/agentd/workspaceprobe.go` | 工作树现场探测 | 探测时补 stat 创建时间 |
| `internal/agentd/workspacefiles.go` | 文件端点白名单闸门 | `scratchRoot()` + 闸门第二入口 |
| `internal/agentd/machines.go` | 机器投影 | 本机/远端投影 ScratchRoot |

### 前端（TS）

| 文件 | 责任 | 动作 |
|------|------|------|
| `web/src/app/tree/sortWorkspaces.ts` | 目录行排序纯函数 | **新建** |
| `web/src/app/workbench/TaskPickerDialog.tsx` | 给当前 tab 选一个任务 | **新建** |
| `web/src/app/workbench/paneDrop.ts` | 投放区判定纯函数 | **新建** |
| `web/src/app/workbench/newFile.ts` | untitled-N 命名 + 建文件 | **新建** |
| `web/src/api/types.ts` | 线格式镜像 | 加三个字段 |
| `web/src/app/tree/ProjectTree.tsx` | 左栏树 | 排序 + 任务行 draggable |
| `web/src/app/overlay/useGlobalTickets.ts` | 工单聚合 | 加 `byWorkDir` |
| `web/src/app/workbench/tabs.ts` | tab 模型纯函数 | 加 `splitGroupAt` |
| `web/src/app/workbench/useWorkbench.ts` | tab 状态容器 | 加 `splitAt` / `closeById` |
| `web/src/app/workbench/WorkbenchPage.tsx` | 中央区布局 | 投放区 + 选择器接线 + 删 PICK_HINT |
| `web/src/app/workbench/BlankTab.tsx` | 空白 tab 面板 | 三项改版 + 删 hint/onBack |
| `web/src/app/homedock/useHomeDock.ts` | 浮窗状态 | `HomeTab.kind` + `newFile` + `setDraft` |
| `web/src/app/homedock/HomeWindow.tsx` | 浮窗容器 | 两个新建图标 + 按种类出标题 |
| `web/src/app/data/useMachineCaps.ts` | 机器能力位 | 加 `scratchRoot(machine)` |
| `web/src/app/files/FileTree.tsx` | 右栏文件树 | 加 `refreshKey` prop |
| `web/src/app/shell/Shell.tsx` | 三栏外框 | 组下标拔除 + 全部新接线 |

---

## Task 1: `Workspace.created_at`

**Files:**
- Modify: `internal/proto/projects.go`（`Workspace` 结构体，约 23-33 行）
- Modify: `internal/agentd/workspaceprobe.go`
- Modify: `web/src/api/types.ts`（`Workspace` 接口，约 109-115 行）
- Regenerate: `web/src/api/testdata/*.json`
- Test: `internal/agentd/workspaceprobe_test.go`

**Interfaces:**
- Consumes: 无（首个 task）
- Produces: `proto.Workspace.CreatedAt time.Time`（JSON `created_at`，RFC3339Nano 字符串）；TS 侧 `Workspace.created_at: string`。Task 3/5 依赖这个字段。

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/workspaceprobe_test.go` 末尾追加。这个测试建一个真的 git 仓库加一个真的 worktree——`workspaceCreatedAt` 读的是 git 自己写下的文件，用假目录测不出它对不对：

```go
// TestWorkspaceCreatedAt 验证主工作树与链接工作树各自都能取到创建时间。
//
// 为什么要建真仓库：本函数读的是 git worktree add 写下的
// .git/worktrees/<名>/gitdir，用手工造的目录结构测等于在测自己写的假数据。
func TestWorkspaceCreatedAt(t *testing.T) {
	main := t.TempDir()
	mustGit(t, main, "init")
	mustGit(t, main, "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-m", "init")

	linked := filepath.Join(t.TempDir(), "wt")
	mustGit(t, main, "worktree", "add", "-b", "feat", linked)

	ws, probeErr := probeWorkspaces(context.Background(), main, "")
	if probeErr != "" {
		t.Fatalf("探测失败: %s", probeErr)
	}
	if len(ws) != 2 {
		t.Fatalf("期望 2 个工作树，实得 %d", len(ws))
	}
	for _, w := range ws {
		if w.CreatedAt.IsZero() {
			t.Errorf("工作树 %s 的 CreatedAt 是零值，期望取到真实时间（is_main=%v）", w.Path, w.IsMain)
		}
	}
}

// TestWorkspaceCreatedAtMissingIsZero 验证取不到时留零值而不是报错。
//
// 这是 spec §1.3 的诚实降级：整棵项目树不该因为一个 stat 失败就 500。
func TestWorkspaceCreatedAtMissingIsZero(t *testing.T) {
	got := workspaceCreatedAt(filepath.Join(t.TempDir(), "不存在"), true)
	if !got.IsZero() {
		t.Errorf("不存在的路径应得零值，实得 %v", got)
	}
}

// mustGit 在 dir 里跑一条 git 命令，失败即 t.Fatal（带完整输出，便于排查）。
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// 关掉全局 hook 与签名配置的干扰：CI/开发机上的 commit.gpgsign 会让
	// --allow-empty 提交失败，而这个测试与签名无关
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
}
```

import 需要补 `os`、`os/exec`、`path/filepath`、`context`、`time`（按文件现状增删，不要重复 import）。若文件里已有同名的 `mustGit`，复用既有的、删掉这里这份。

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./internal/agentd/ -run 'TestWorkspaceCreatedAt' -v
```
预期：编译失败，`undefined: workspaceCreatedAt`、`w.CreatedAt undefined`。

- [ ] **Step 3: 加 proto 字段**

`internal/proto/projects.go` 的 `Workspace` 结构体末尾（`Managed` 之后）加：

```go
	// CreatedAt 是这个工作树被建出来的时间；零值 = 取不到。
	//
	// 取法分两种：
	//   - 主工作树：stat <path>/.git
	//   - 链接工作树：stat <git 公共目录>/worktrees/<名>/gitdir
	//
	// 为什么链接工作树不 stat 工作树目录本身：那个目录的 mtime 会随着往里写
	// 代码变化，排出来的是「最近动过」而不是「什么时候建的」。gitdir 这个文件
	// 由 git worktree add 写一次之后就不再动，是唯一稳定的创建时间证据。
	//
	// 为什么取不到时留零值而不是报错：整棵项目树不该因为一个 stat 失败就 500。
	// 消费方（控制台排序）把零值当「最旧」处理。
	CreatedAt time.Time `json:"created_at"`
```

- [ ] **Step 4: 实现 `workspaceCreatedAt` 并在探测时填入**

`internal/agentd/workspaceprobe.go`：`probeWorkspaces` 里 `ws := parseWorktreePorcelain(...)` 之后、返回之前补一轮填充：

```go
	ws := parseWorktreePorcelain(out, managedRoot)
	// 创建时间在解析之后单独补：porcelain 输出里没有这个信息，只能落到文件系统上问。
	// 每个工作树一次 stat（毫秒级），与已经付出的一次 git 子进程相比可忽略
	for i := range ws {
		ws[i].CreatedAt = workspaceCreatedAt(ws[i].Path, ws[i].IsMain)
	}
	log().Debug("工作区探测完成", "dir", dir, "worktrees", len(ws))
	return ws, ""
```

同文件末尾加：

```go
// workspaceCreatedAt 取一个工作树的创建时间；取不到返回零值。
//
// 参数：
//   - path: 工作树根的绝对路径
//   - isMain: 是否主工作树（决定去哪个文件上问）
//
// 返回：创建时间；任何一步失败都返回零值，**不返回 error**——调用方要的是
// 一个可展示的字段，不是一个能让整棵树 500 的错误。
//
// 主工作树看 <path>/.git：仓库初始化时建出来，之后不再重建。
// 链接工作树看 <公共目录>/worktrees/<名>/gitdir：git worktree add 写一次就不再动。
// 刻意都不 stat 工作树目录本身——它的 mtime 会随着往里写代码变化，那是「最近
// 动过」不是「什么时候建的」。
func workspaceCreatedAt(path string, isMain bool) time.Time {
	dotGit := filepath.Join(path, ".git")
	fi, err := os.Stat(dotGit)
	if err != nil {
		// 探测与 stat 之间工作树被 git worktree remove 掉是正常竞态，
		// 不是故障——只 Debug，不 Warn
		log().Debug("取工作树创建时间失败，留零值", "path", path, "cause", err)
		return time.Time{}
	}
	if isMain {
		return fi.ModTime()
	}
	// 链接工作树的 .git 是**一个文件**，内容形如 "gitdir: /主仓库/.git/worktrees/名"。
	// 它自己的 mtime 不可靠（git 会在 prune/repair 时重写它），要顺着它指到管理目录
	// 里的 gitdir 文件上——那个才是只写一次的。
	if fi.IsDir() {
		// 少见但真实：有人手工把工作树的 .git 做成了目录。此时没有可跟的指针，
		// 就用它自己的时间，比留零值有信息
		return fi.ModTime()
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		log().Debug("读工作树 .git 指针失败，留零值", "path", path, "cause", err)
		return time.Time{}
	}
	adminDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if adminDir == "" {
		log().Debug("工作树 .git 指针为空，留零值", "path", path)
		return time.Time{}
	}
	gi, err := os.Stat(filepath.Join(adminDir, "gitdir"))
	if err != nil {
		log().Debug("取工作树管理目录时间失败，留零值", "path", path, "admin_dir", adminDir, "cause", err)
		return time.Time{}
	}
	return gi.ModTime()
}
```

import 补 `os`（`filepath`、`strings`、`time` 已在）。

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run 'TestWorkspaceCreatedAt' -v
```
预期：两个用例 PASS。

- [ ] **Step 6: 同步 TS 类型**

`web/src/api/types.ts` 的 `Workspace` 接口加：

```ts
  // created_at 是工作树的创建时间（RFC3339Nano）。零值时间 = agentd 取不到，
  // 排序时当「最旧」处理，见 sortWorkspaces。
  created_at: string
```

- [ ] **Step 7: 重生成契约 fixture 并跑全套契约测试**

```bash
go test ./internal/proto/ -run TestContractFixtures -update
go test ./internal/proto/
cd web && npm run test -- src/api/contract.test.ts
```
预期：fixture 里出现 `created_at`；Go 与 web 两侧契约测试都 PASS。

- [ ] **Step 8: 格式与全量测试**

```bash
gofmt -l .          # 必须无输出
go test ./internal/agentd/ ./internal/proto/
```

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat(agentd): Workspace 上报创建时间

取法分主/链接两种：主工作树 stat .git，链接工作树顺着 .git 指针
stat 管理目录里的 gitdir——那个文件 git worktree add 写一次就不再动，
比工作树目录自身的 mtime（会随写代码变）稳。取不到留零值不报错。"
```

---

## Task 2: scratch 草稿区与白名单第二入口

**Files:**
- Modify: `internal/proto/status.go`（`StatusResp`）
- Modify: `internal/proto/projects.go`（`Machine`）
- Modify: `internal/agentd/workspacefiles.go`（`scratchRoot` + `resolveWorkspace`）
- Modify: `internal/agentd/machines.go`（`localMachine` / `probeRemote` 投影）
- Modify: `web/src/api/types.ts`
- Regenerate: `web/src/api/testdata/*.json`
- Test: `internal/agentd/workspacefiles_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `(*Server).scratchRoot() string`（返回绝对路径；不可用时返回空串）。`proto.StatusResp.ScratchRoot` / `proto.Machine.ScratchRoot`（JSON `scratch_root`，`omitempty`）。Task 11 依赖 TS 侧的 `Machine.scratch_root`。

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/workspacefiles_test.go` 末尾追加。参照该文件里既有用例的 Server 构造方式（若既有用例用的是某个 `newTestServer` 之类的辅助函数，**复用它**，不要新造一套）：

```go
// TestScratchRootWhitelisted 验证草稿区命中白名单闸门，且闸门外的路径仍被拒。
//
// 这是 spec §5.1 的核心断言：scratch 不在 project_locations 表里、也不是 git
// 工作树，两段既有比对都命中不了它，所以必须有专门的一支——而那一支绝不能
// 顺手把别的路径也放进来。
func TestScratchRootWhitelisted(t *testing.T) {
	s := newTestServer(t) // 复用本文件既有的构造方式
	root := s.scratchRoot()
	if root == "" {
		t.Fatal("scratchRoot 为空，草稿区应当在启动时建好")
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Fatalf("草稿区目录不存在或不是目录: %v", err)
	}
	if got, ok := s.resolveWorkspace(context.Background(), root); !ok || got != root {
		t.Errorf("草稿区未命中白名单: got=%q ok=%v", got, ok)
	}
	// 闸门不能顺手放行草稿区的**父目录**或**子目录**：只有它自己是入口
	if _, ok := s.resolveWorkspace(context.Background(), filepath.Dir(root)); ok {
		t.Error("草稿区的父目录不该命中白名单")
	}
	if _, ok := s.resolveWorkspace(context.Background(), filepath.Join(root, "sub")); ok {
		t.Error("草稿区的子目录不该命中白名单（子目录经 rel 参数访问，不是独立入口）")
	}
}

// TestScratchEntryRoundTrip 验证草稿区下建文件 → 列举 → 写 → 读一条链路全通。
//
// 复用的是既有六个文件端点，不是新端点——这个用例存在的意义就是证明「复用」
// 这个决定成立：scratch 不是 git 仓库，而 ListDir 对 git 只是尽力而为。
func TestScratchEntryRoundTrip(t *testing.T) {
	s := newTestServer(t)
	root := s.scratchRoot()

	if _, err := CreateEntry(root, "", "untitled-1.md", "file"); err != nil {
		t.Fatalf("在草稿区建文件失败: %v", err)
	}
	entries, err := ListDir(root, "")
	if err != nil {
		t.Fatalf("列举草稿区失败（scratch 不是 git 仓库，但列举不该因此失败）: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "untitled-1.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("列举结果里没有刚建的文件，实得 %+v", entries)
	}
}
```

`CreateEntry` / `ListDir` 的确切签名以 `internal/agentd/workspace.go` 里的现状为准——**先读那个文件确认**，签名不符就按实际的改，别硬套。

- [ ] **Step 2: 跑测试确认它失败**

```bash
go test ./internal/agentd/ -run 'TestScratch' -v
```
预期：`undefined: scratchRoot`。

- [ ] **Step 3: 实现 `scratchRoot`**

`internal/agentd/workspacefiles.go`，`resolveWorkspace` 之前加：

```go
// scratchDirName 是草稿区在 DataDir 下的目录名。
const scratchDirName = "scratch"

// scratchRoot 返回草稿区的绝对路径；不可用时返回空串。
//
// 草稿区是控制台右下角浮窗「临时文件」的落点：一个不属于任何 git 工作树、
// 也不在 project_locations 表里的受管目录（spec §5.1）。
//
// 返回空串的两种情形都不是故障：DataDir 没配出来，或目录建不出来（磁盘满、
// 权限）。此时闸门那一支恒不命中、StatusResp 里这个字段缺席、前端入口不渲染
// ——整条链路对「草稿区不可用」是收敛的，不会有任何一处拿着空路径去发请求。
//
// 每次调用都 MkdirAll 而不是启动时建一次：目录可能在 agentd 运行期间被人删掉，
// 而 MkdirAll 对已存在的目录是零成本的 stat。
func (s *Server) scratchRoot() string {
	dataDir := s.conf().DataDir
	if dataDir == "" {
		return ""
	}
	root := filepath.Join(dataDir, scratchDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		// 只 Warn 不 Error：草稿区是附属功能，建不出来不该让人以为 agentd 坏了
		s.log.Warn("草稿区目录建立失败，临时文件功能不可用", "path", root, "cause", err)
		return ""
	}
	return filepath.Clean(root)
}
```

import 补 `os`。

- [ ] **Step 4: 闸门加第二入口**

`resolveWorkspace` 函数体开头，`want := filepath.Clean(path)` 之后立刻加：

```go
	// 草稿区是这道闸门的第二个入口。它不是 git 工作树，也不在 project_locations
	// 表里，所以下面按登记表比对与现场探测两段都命中不了它（spec §5.1）。
	//
	// 放在最前面短路：这是一次纯字符串比较，比读一次数据库便宜；而草稿区的请求
	// 频率与工作树同量级（浮窗里每存一次就是一次 PUT）。
	//
	// 只放行它**自己**，不放行父目录或子目录：子目录经 rel 参数访问，走的是
	// 各端点内部已有的路径逃逸校验，不需要在闸门这一层多开一个口子。
	if root := s.scratchRoot(); root != "" && want == root {
		return root, true
	}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run 'TestScratch' -v
```
预期：两个用例 PASS。

- [ ] **Step 6: 上报字段**

`internal/proto/status.go` 的 `StatusResp` 里（`DataDir` 附近）加：

```go
	// ScratchRoot 是草稿区的绝对路径（<DataDir>/scratch），控制台浮窗的临时文件
	// 落在这里。**缺席 = 这台机器不支持临时文件**（老 agentd，或目录建不出来）。
	//
	// 与 PtySupported 那种能力位的三态纪律不同：那里 nil 要按「不知道，放行」处理，
	// 而这里缺的是一个**路径**——没有路径就没法发请求，放行只会换来一次必然 400。
	ScratchRoot string `json:"scratch_root,omitempty"`
```

`internal/proto/projects.go` 的 `Machine` 里（`RevealSupported` 之后）加：

```go
	// ScratchRoot 是这台机器的草稿区路径，探活时从它的 StatusResp 投影而来。
	// 空串（omitempty 后为缺席）= 这台机器不支持临时文件，前端不渲染入口。
	ScratchRoot string `json:"scratch_root,omitempty"`
```

- [ ] **Step 7: 填充上报值**

- `internal/agentd/machines.go` 的 `localMachine()`：给 `m.ScratchRoot = s.scratchRoot()`。
- 同文件的 `probeRemote()`：从对端 StatusResp 投影，**与既有的 `PtySupported` / `RevealSupported` 写在同一处、同一风格**（先读那段代码，照它的形状加一行）。
- agentd 的 `handleStatus`（或组装 StatusResp 的那个函数，grep `StatusResp{` 定位）：填 `ScratchRoot: s.scratchRoot()`。

- [ ] **Step 8: 同步 TS 类型**

`web/src/api/types.ts`：

```ts
// StatusResp 里加：
  // scratch_root 是草稿区绝对路径；缺席 = 这台 agentd 不支持临时文件。
  scratch_root?: string

// Machine 里加：
  // scratch_root 是这台机器的草稿区路径，探活时从对端 StatusResp 投影。
  // 缺席 = 不支持临时文件（老 agentd 或目录建不出来），前端不渲染入口。
  scratch_root?: string
```

- [ ] **Step 9: 重生成 fixture + 全套验证**

```bash
go test ./internal/proto/ -run TestContractFixtures -update
go test ./internal/agentd/ ./internal/proto/
gofmt -l .
cd web && npm run test -- src/api/contract.test.ts && npm run typecheck
```

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "feat(agentd): <DataDir>/scratch 草稿区作为文件白名单第二入口

复用既有六个文件端点，零新端点——已核实 ListDir 对 git 只是尽力而为，
scratch 不是 git 仓库不影响任何一个端点。闸门只放行草稿区自己，
子目录仍走各端点内部的路径逃逸校验。目录建不出来时整条链路收敛到
「前端不渲染入口」，不会有一处拿着空路径发请求。"
```

---

## Task 3: `sortWorkspaces` 纯函数

**Files:**
- Create: `web/src/app/tree/sortWorkspaces.ts`
- Test: `web/src/app/tree/sortWorkspaces.test.ts`

**Interfaces:**
- Consumes: `Workspace.created_at`（Task 1）
- Produces:
  ```ts
  export interface WorkspaceMetrics { tickets: number; tasks: number; createdAt: string }
  export function sortWorkspaces(
    list: Workspace[],
    metricsOf: (ws: Workspace) => WorkspaceMetrics,
  ): Workspace[]
  ```
  Task 5 调用它。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/tree/sortWorkspaces.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { sortWorkspaces, type WorkspaceMetrics } from './sortWorkspaces'
import type { Workspace } from '../../api/types'

// ws 造一个最小工作树。branch 兼作身份，断言里靠它认人。
function ws(branch: string, isMain = false): Workspace {
  return { path: `/r/${branch}`, branch, head: 'abc1234', is_main: isMain, managed: false, created_at: '' }
}

// metrics 把一张 branch → 三元组的表包成回调。
function metrics(table: Record<string, [number, number, string]>) {
  return (w: Workspace): WorkspaceMetrics => {
    const [tickets, tasks, createdAt] = table[w.branch] ?? [0, 0, '']
    return { tickets, tasks, createdAt }
  }
}

const names = (list: Workspace[]) => list.map((w) => w.branch)

describe('sortWorkspaces', () => {
  it('工单数优先级最高，压过任务数与时间', () => {
    const list = [ws('a'), ws('b'), ws('c')]
    const got = sortWorkspaces(list, metrics({
      a: [0, 99, '2026-08-17T10:00:00Z'],
      b: [1, 0, '2020-01-01T00:00:00Z'],
      c: [0, 50, '2026-08-16T10:00:00Z'],
    }))
    expect(names(got)).toEqual(['b', 'a', 'c'])
  })

  it('工单数相同时按任务数降序', () => {
    const list = [ws('a'), ws('b')]
    const got = sortWorkspaces(list, metrics({
      a: [2, 1, '2026-08-17T10:00:00Z'],
      b: [2, 5, '2020-01-01T00:00:00Z'],
    }))
    expect(names(got)).toEqual(['b', 'a'])
  })

  it('工单与任务都相同时按创建时间降序（新的在前）', () => {
    const list = [ws('old'), ws('new')]
    const got = sortWorkspaces(list, metrics({
      old: [0, 0, '2020-01-01T00:00:00Z'],
      new: [0, 0, '2026-08-17T10:00:00Z'],
    }))
    expect(names(got)).toEqual(['new', 'old'])
  })

  it('主工作树恒排第一，不参与排序——哪怕别人有工单', () => {
    const list = [ws('feat'), ws('main', true)]
    const got = sortWorkspaces(list, metrics({
      feat: [9, 9, '2026-08-17T10:00:00Z'],
      main: [0, 0, '2020-01-01T00:00:00Z'],
    }))
    expect(names(got)).toEqual(['main', 'feat'])
  })

  it('空 created_at 当最旧，排在有时间的后面', () => {
    const list = [ws('empty'), ws('dated')]
    const got = sortWorkspaces(list, metrics({
      empty: [0, 0, ''],
      dated: [0, 0, '2020-01-01T00:00:00Z'],
    }))
    expect(names(got)).toEqual(['dated', 'empty'])
  })

  it('三个键全等时按 path 升序，结果稳定不随输入顺序变', () => {
    const same = metrics({})
    const forward = sortWorkspaces([ws('c'), ws('a'), ws('b')], same)
    const backward = sortWorkspaces([ws('b'), ws('c'), ws('a')], same)
    expect(names(forward)).toEqual(['a', 'b', 'c'])
    expect(names(backward)).toEqual(['a', 'b', 'c'])
  })

  it('不改入参数组', () => {
    const list = [ws('b'), ws('a')]
    const copy = [...list]
    sortWorkspaces(list, metrics({}))
    expect(list).toEqual(copy)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npm run test -- src/app/tree/sortWorkspaces.test.ts
```
预期：FAIL，`Failed to resolve import './sortWorkspaces'`。

- [ ] **Step 3: 实现**

创建 `web/src/app/tree/sortWorkspaces.ts`：

```ts
// sortWorkspaces —— 左栏目录行的排序规则（纯函数，无 React 依赖）。
//
// 职责：把一个机器节点下的工作树按「现在最该看哪个」排好序。
//
// 边界：
//   - 不认识任务、不认识工单：三个排序键由调用方经 metricsOf 回调提供。
//     这样测试可以用手写数字驱动，不必造一整棵项目树加一批任务
//   - 不改入参，返回新数组
//
// 排序规则（spec §1.1）：主工作树恒第一，其余按
//   工单数 ↓ → 任务数 ↓ → 创建时间 ↓ → path ↑
import type { Workspace } from '../../api/types'

// WorkspaceMetrics 是一个目录行的三个排序键。
//
// createdAt 是 RFC3339Nano 字符串；空串表示 agentd 取不到，按**最旧**处理
// （见 createdRank）。
export interface WorkspaceMetrics {
  tickets: number
  tasks: number
  createdAt: string
}

// createdRank 把创建时间换成可比较的毫秒数；取不到时返回 -Infinity。
//
// 为什么空串与非法值都当最旧而不是当最新：这个字段的缺席只有两种来源——老
// agentd 不上报，或 stat 失败。两种都意味着「这个工作树的时间信息不可信」，
// 而把不可信的东西排到最前面会挤掉真正新建的分支。
function createdRank(createdAt: string): number {
  if (createdAt === '') return -Infinity
  const t = Date.parse(createdAt)
  return Number.isNaN(t) ? -Infinity : t
}

// sortWorkspaces 返回排好序的新数组。
//
// 参数：
//   - list: 一个机器节点下的工作树（原样，不要求已排序）
//   - metricsOf: 给一个工作树算出它的三个排序键
//
// 返回：新数组；入参不被修改。
//
// 主工作树（is_main）恒排第一且不参与其余比较：它不是一个任务分支，是这个
// 项目在这台机器上的家。让它被别的分支的工单顶下去，用户对「主目录在第一行」
// 的肌肉记忆当场失效，而那条记忆比「主目录也参与排序」更值钱（spec §1.1）。
//
// 末位的 path 升序不是排序意图，是**稳定性兜底**：前三个键全等时若不给确定
// 次序，不同引擎的 sort 结果可能不同，行会随每次 2.5s 任务流心跳无缘无故重排。
export function sortWorkspaces(
  list: Workspace[],
  metricsOf: (ws: Workspace) => WorkspaceMetrics,
): Workspace[] {
  return [...list].sort((a, b) => {
    if (a.is_main !== b.is_main) return a.is_main ? -1 : 1
    if (a.is_main && b.is_main) return a.path < b.path ? -1 : a.path > b.path ? 1 : 0
    const ma = metricsOf(a)
    const mb = metricsOf(b)
    if (ma.tickets !== mb.tickets) return mb.tickets - ma.tickets
    if (ma.tasks !== mb.tasks) return mb.tasks - ma.tasks
    const ra = createdRank(ma.createdAt)
    const rb = createdRank(mb.createdAt)
    if (ra !== rb) return rb - ra
    return a.path < b.path ? -1 : a.path > b.path ? 1 : 0
  })
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
cd web && npm run test -- src/app/tree/sortWorkspaces.test.ts
```
预期：7 个用例全 PASS。

- [ ] **Step 5: 加注释自检**

本文件是新建文件，确认：文件头块注释（职责 + 边界）✅、每个导出符号有注释 ✅、`createdRank` 的「为什么当最旧」与 `sortWorkspaces` 的「为什么主工作树置顶 / 为什么要 path 兜底」都解释了**为什么** ✅。纯函数无 I/O、无错误分支，按 instrumenting-code 的豁免条款不需要日志。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/tree/sortWorkspaces.ts web/src/app/tree/sortWorkspaces.test.ts
git commit -m "feat(web): 目录行排序纯函数

工单 → 任务 → 创建时间三级降序，主工作树恒第一。末位 path 升序是
稳定性兜底：不给确定次序的话，行会随 2.5s 任务流心跳无缘无故重排。"
```

---

## Task 4: `useGlobalTickets.byWorkDir`

**Files:**
- Modify: `web/src/app/overlay/useGlobalTickets.ts`
- Test: `web/src/app/overlay/useGlobalTickets.test.ts`（补充）

**Interfaces:**
- Consumes: 无
- Produces: `GlobalTickets.byWorkDir: Map<string, number>`（键是任务的 `work_dir` 绝对路径；空 `work_dir` 的任务**不进表**）。Task 5 消费它。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/overlay/useGlobalTickets.test.ts` 末尾追加一个 describe。**先读该文件既有用例**，照它 mock `fetchTaskDetail` 的方式写，不要另造一套 mock：

```ts
describe('byWorkDir', () => {
  it('按 work_dir 归集工单张数，一个任务多张工单要累加', async () => {
    // 造两个 waiting_answer 任务：T1 在 /r/a 挂 2 张，T2 在 /r/b 挂 1 张
    // （mock 方式照抄本文件既有用例）
    // 断言：
    //   result.current.byWorkDir.get('/r/a') === 2
    //   result.current.byWorkDir.get('/r/b') === 1
  })

  it('空 work_dir 的任务不进表——它归主目录，而这里不知道谁是主目录', async () => {
    // 造一个 work_dir === '' 的 waiting_answer 任务，挂 1 张工单
    // 断言：byWorkDir.size === 0
  })

  it('没有挂起工单时是空表，不是 undefined', () => {
    // 断言：byWorkDir instanceof Map && byWorkDir.size === 0
  })
})
```

**上面三个用例的注释体是要你填的实现**，不是可以留空的占位：按既有用例的 mock 形状把它们写完整再进下一步。断言值已经给死了，不要改。

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npm run test -- src/app/overlay/useGlobalTickets.test.ts
```
预期：FAIL，`byWorkDir` 是 undefined。

- [ ] **Step 3: 实现**

`web/src/app/overlay/useGlobalTickets.ts`：

接口加字段：

```ts
export interface GlobalTickets {
  items: GlobalTicket[]
  count: number
  // byWorkDir 是「目录绝对路径 → 挂起工单张数」，供左栏目录行排序用。
  //
  // 空 work_dir 的任务**不进这张表**：它们按原地模式归主目录，而这里不知道
  // 哪个是主目录（那要看项目树）。归集主目录那一步由 ProjectTree 做——它手上
  // 有 ws.is_main，判据与 tasksOfWorkspace 一致，两处不会分叉。
  byWorkDir: Map<string, number>
  refresh: () => void
}
```

在 `return` 之前加派生：

```ts
  // 从 items 派生而不是在取详情时顺手累加：items 是这个 hook 的单一真相，
  // 两份状态各自累加迟早会对不上（一次失败的详情请求只丢它自己那份，
  // 而累加器不知道该减掉多少）。
  const byWorkDir = useMemo(() => {
    const m = new Map<string, number>()
    for (const { task } of items) {
      if (task.work_dir === '') continue
      m.set(task.work_dir, (m.get(task.work_dir) ?? 0) + 1)
    }
    return m
  }, [items])

  return { items, count: items.length, byWorkDir, refresh }
```

- [ ] **Step 4: 跑测试确认通过**

```bash
cd web && npm run test -- src/app/overlay/useGlobalTickets.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add web/src/app/overlay/useGlobalTickets.ts web/src/app/overlay/useGlobalTickets.test.ts
git commit -m "feat(web): 工单按 work_dir 归集，供目录行排序

从 items 派生而不是取详情时顺手累加：items 是这个 hook 的单一真相，
两份状态各自累加迟早对不上。空 work_dir 不进表，归主目录那一步留给
ProjectTree——它手上有 is_main，与 tasksOfWorkspace 同口径不会分叉。"
```

---

## Task 5: 左栏目录行接入排序

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`
- Modify: `web/src/app/shell/Shell.tsx`（传新 prop）
- Test: `web/src/app/tree/ProjectTree.test.tsx`（补充）

**Interfaces:**
- Consumes: `sortWorkspaces`（Task 3）、`byWorkDir`（Task 4）、`Workspace.created_at`（Task 1）
- Produces: `ProjectTreeProps.ticketsByDir: Map<string, number>`

- [ ] **Step 1: 写失败的测试**

在 `ProjectTree.test.tsx` 里加。**先读既有用例**，复用它构造 tree/tasks 的辅助函数与查询目录行的方式（`data-testid="workspace-row"`）：

```ts
it('目录行按工单 → 任务 → 时间排序，主工作树恒第一', () => {
  // 造一个项目一台机器四个工作树：
  //   main（is_main，无任务无工单，created_at 最旧）
  //   quiet（无任务无工单，created_at 最新）
  //   busy（2 个 running 任务，无工单）
  //   blocked（1 个 waiting_answer 任务 + 1 张工单）
  // 传 ticketsByDir = new Map([['/r/blocked', 1]])
  // 展开到目录层后，读所有 data-testid="workspace-row" 的文本
  // 断言顺序为：main, blocked, busy, quiet
})
```

同样，注释体是要你填的实现。断言的顺序已给死。

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npm run test -- src/app/tree/ProjectTree.test.tsx
```

- [ ] **Step 3: 实现**

`ProjectTree.tsx`：

1. import：`import { sortWorkspaces, type WorkspaceMetrics } from './sortWorkspaces'`
2. `ProjectTreeProps` 加：

```ts
  // ticketsByDir 是「目录绝对路径 → 挂起工单张数」，来自 useGlobalTickets。
  // 只用于目录行排序，不显示在界面上——工单数已经由 ticketCount 角标在
  // 底部说了一次，行上再说一遍是噪音。
  ticketsByDir: Map<string, number>
```

3. 解构参数里加 `ticketsByDir`。
4. 在组件内、`wsCounts` 附近加：

```ts
  // wsMetrics 给一个目录行算出三个排序键。
  //
  // 工单归集：ticketsByDir 的键是任务的 work_dir，而原地模式任务的 work_dir
  // 是空串——它们的工单在那张表里没有键。这里按 is_main 把它们补回来，判据与
  // tasksOfWorkspace 完全一致（work_dir 为空归主目录），两处不会分叉。
  const wsMetrics = (project: ProjectNode, machine: string, ws: Workspace): WorkspaceMetrics => {
    const under = tasksOfWorkspace(tasks, project, machine, ws)
    let tickets = ticketsByDir.get(ws.path) ?? 0
    if (ws.is_main) {
      // 原地模式任务的工单：它们在 byWorkDir 里没有键，逐个从任务侧找回来。
      // 只有主目录走这一支，与 tasksOfWorkspace 的回退口径对齐
      for (const t of tasks) {
        if (t.work_dir === '' && t.project_id === project.project_id && t.machine === machine) {
          tickets += ticketsByDir.get('') ?? 0
          break
        }
      }
    }
    return {
      tickets,
      tasks: under.filter(
        (t) => t.state === 'running' || t.state === 'waiting_answer' || t.state === 'waiting_review',
      ).length,
      createdAt: ws.created_at ?? '',
    }
  }
```

> **注意**：`byWorkDir` 按 Task 4 的定义**不含空串键**，所以上面 `ticketsByDir.get('')` 恒为 0。这一支保留是为了让「原地模式任务的工单归主目录」这条口径在代码里显式可见——若将来 Task 4 改成收录空串键，这里不需要再改。**如果你觉得这是死代码，不要删它，也不要改 Task 4 的定义；把疑问作为工单发给审核者。**

5. 渲染目录行的那一处（`loc.workspaces.map((ws) => {`）改为：

```ts
                    {problem === '' &&
                      mOpen &&
                      sortWorkspaces(loc.workspaces, (ws) => wsMetrics(project, loc.machine, ws)).map((ws) => {
```

`Workspace` 类型已在文件顶部 import，不需要新增。

`Shell.tsx`：给 `<ProjectTree>` 加 `ticketsByDir={tickets.byWorkDir}`。

- [ ] **Step 4: 跑测试确认通过**

```bash
cd web && npm run test -- src/app/tree/ProjectTree.test.tsx src/app/shell/Shell.test.tsx
```

- [ ] **Step 5: 全套前端验证**

```bash
cd web && npm run test && npm run lint && npm run typecheck
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(web): 左栏目录行按工单/任务/时间排序

原地模式任务（work_dir 为空）的工单归主目录，判据与 tasksOfWorkspace
完全一致，两处不会分叉。"
```

---

## Task 6: 拔掉组下标 + `splitGroupAt`

这个 task 不产生用户可见的变化，它是 Task 9 的地基。**顺序不可调换**：先有安全的插入式分屏，才能做拖拽。

**Files:**
- Modify: `web/src/app/workbench/tabs.ts`
- Modify: `web/src/app/workbench/useWorkbench.ts`
- Modify: `web/src/app/shell/Shell.tsx`
- Test: `web/src/app/workbench/tabs.test.ts`、`useWorkbench.test.ts`、`Shell.test.tsx`（补充/改）

**Interfaces:**
- Consumes: 无
- Produces:
  ```ts
  // tabs.ts
  export function splitGroupAt(wb: Workbench, index: number): Workbench
  // useWorkbench.ts —— WorkbenchApi 新增
  splitAt: (index: number) => void
  closeById: (tabId: string) => void
  ```
  Task 9 调用 `splitAt`。

- [ ] **Step 1: 写失败的测试（tabs.ts）**

`web/src/app/workbench/tabs.test.ts` 追加：

```ts
describe('splitGroupAt', () => {
  it('在指定下标处插入空栏并聚焦它', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'tui', taskId: 'T1' })
    wb = splitGroupAt(wb, 0)
    expect(wb.groups).toHaveLength(2)
    expect(wb.groups[0].tabs).toHaveLength(0)   // 新栏插在最前
    expect(wb.groups[1].tabs).toHaveLength(1)   // 原来那栏被推到后面
    expect(wb.active).toBe(0)
  })

  it('插到末尾等价于 splitGroup', () => {
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'tui', taskId: 'T1' })
    expect(splitGroupAt(wb, wb.groups.length)).toEqual(splitGroup(wb))
  })

  it('下标越界时夹到合法范围，不抛错', () => {
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'tui', taskId: 'T1' })
    expect(splitGroupAt(wb, -5).groups).toHaveLength(2)
    expect(splitGroupAt(wb, 99).groups).toHaveLength(2)
    expect(splitGroupAt(wb, -5).groups[0].tabs).toHaveLength(0)
    expect(splitGroupAt(wb, 99).groups[1].tabs).toHaveLength(0)
  })

  it('已到 MAX_GROUPS 时原样返回同一个对象', () => {
    let wb = EMPTY_WORKBENCH
    while (wb.groups.length < MAX_GROUPS) wb = splitGroup(wb)
    expect(splitGroupAt(wb, 1)).toBe(wb)
  })

  it('sizes 与 groups 等长这条不变式在插入后仍成立', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'tui', taskId: 'T1' })
    wb = splitGroupAt(wb, 0)
    wb = splitGroupAt(wb, 1)
    expect(wb.sizes).toHaveLength(wb.groups.length)
  })
})
```

import 里补 `splitGroupAt`、`MAX_GROUPS`。

- [ ] **Step 2: 写失败的测试（useWorkbench.closeById）**

`web/src/app/workbench/useWorkbench.test.ts` 追加：

```ts
it('closeById 按 tabId 关闭，不需要调用方知道它在哪一组', () => {
  // 用 renderHook 开两栏，在第二栏开一个 tab，记下它的 id
  // 调 closeById(那个 id)
  // 断言它没了，且**第一栏的 tab 不受影响**
})

it('closeById 对不存在的 id 是空操作，不抛错', () => {
  // 断言调用后 wb 内容不变
})
```

注释体是要你填的实现，照该文件既有用例的 `renderHook` 形状写。

- [ ] **Step 3: 跑测试确认失败**

```bash
cd web && npm run test -- src/app/workbench/tabs.test.ts src/app/workbench/useWorkbench.test.ts
```

- [ ] **Step 4: 实现 `splitGroupAt`**

`tabs.ts` 里把现有的 `splitGroup` 换成：

```ts
// splitGroupAt 在 index 处插入一个空栏并聚焦它；已到 MAX_GROUPS 时**原样返回
// 同一个对象**（调用方可据此跳过一次无谓的 setState）。宽度重置为等分。
//
// 参数：
//   - index: 新栏插在哪个位置（0 = 最左）。越界时夹到 [0, groups.length]
//
// 关于「插入会不会打乱谁的下标」：曾经不能这么做——Shell 把 (组下标, tabId)
// 存进了确认弹层的 state，中间插入会让存着的下标指向别的栏。那条耦合已经在
// 本 task 里拔掉了（Shell 改为按 tabId 反查），所以插入现在是安全的。
// **如果你在别处又看到有人把组下标存进跨事件的 state，那是在把这个坑挖回来。**
export function splitGroupAt(wb: Workbench, index: number): Workbench {
  if (wb.groups.length >= MAX_GROUPS) return wb
  const next = cloneWorkbench(wb)
  const at = Math.max(0, Math.min(index, next.groups.length))
  next.groups.splice(at, 0, { tabs: [], activeId: null })
  next.active = at
  next.sizes = evenSizes(next.groups.length)
  return next
}

// splitGroup 在末尾再开一栏。⌘D 与面包屑的分屏按钮走它，行为与本函数存在
// 之前逐字节一致。
export function splitGroup(wb: Workbench): Workbench {
  return splitGroupAt(wb, wb.groups.length)
}
```

**删掉** 原 `splitGroup` 上那段解释「为什么 push 到末尾而不是插在当前栏右边」的注释——那条约束已经不成立了，留着会骗人。它的历史教训已经被搬进 `splitGroupAt` 的注释里。

- [ ] **Step 5: 实现 `splitAt` / `closeById`**

`useWorkbench.ts`：

1. import 里加 `splitGroupAt`。
2. `WorkbenchApi` 接口加：

```ts
  // splitAt 在指定位置插入一栏（0 = 最左）。拖放分屏用它；⌘D 与面包屑按钮
  // 仍走 split（末尾追加）。
  splitAt: (index: number) => void
  // closeById 按 tab id 关闭，自己反查它在哪一组。
  //
  // 为什么要有它：组下标只在一次事件内可靠。确认弹层打开期间用户可能分屏、
  // 关栏，等他点「确认」时存下来的下标已经指向别的栏了——那会关掉另一栏的
  // tab。tabId 在整个 workbench 内唯一（nextTabId 保证），反查是确定的。
  closeById: (tabId: string) => void
```

3. 实现：

```ts
  const splitAt = useCallback((index: number) => mutate((w) => splitGroupAt(w, index)), [mutate])
  const closeById = useCallback(
    (tabId: string) =>
      mutate((w) => {
        const gi = w.groups.findIndex((g) => g.tabs.some((t) => t.id === tabId))
        // 找不到是正常情形：确认弹层还开着时这个 tab 被别的路径关掉了。
        // 空操作，不抛错——弹层的「确认」按钮不该因此炸掉
        if (gi === -1) return w
        return closeTab(w, gi, tabId)
      }),
    [mutate],
  )
```

4. 返回对象里加 `splitAt, closeById`。

- [ ] **Step 6: 改 Shell —— 拔掉存下来的组下标**

`Shell.tsx` 三处改动：

```ts
// 1) closingPty 去掉 group
const [closingPty, setClosingPty] = useState<
  { tabId: string; sessionId: string; machine: string } | null
>(null)

// 2) closingDirtyFile 去掉 group
const [closingDirtyFile, setClosingDirtyFile] = useState<{ tabId: string; rel: string } | null>(null)
```

`beforeCloseTab` 里两处 `setClosing*` 相应去掉 `group`（形参 `group` 变成未使用——从签名里删掉它，并同步改 `WorkbenchPageProps.onBeforeClose` 的签名为 `(c: TabContent, tabId: string) => boolean`，以及 `WorkbenchPage` 内部的调用处）。

两处确认回调改为：

```ts
// confirmClosePty 里
wb.closeById(closingPty.tabId)

// 未保存文件的 onConfirm 里
if (closingDirtyFile) wb.closeById(closingDirtyFile.tabId)
```

同时把 `closingPty` 上方那段「为什么连 machine 一起留」的注释**保留**（那条理由仍然成立），但删掉其中提到「组下标」的部分（如果有）。在 `closingPty` 声明上补一句：

```ts
// 只存 tabId 不存组下标：组下标在确认弹层打开期间会因为分屏/关栏而失效，
// 而 tabId 在整个 workbench 内唯一。关闭走 wb.closeById 自己反查。
```

- [ ] **Step 7: 跑测试确认通过**

```bash
cd web && npm run test -- src/app/workbench/ src/app/shell/
```
预期：全 PASS。`Shell.test.tsx` 里若有断言依赖旧的 `onBeforeClose` 签名，一并改掉。

- [ ] **Step 8: 全套前端验证**

```bash
cd web && npm run test && npm run lint && npm run typecheck
```

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "refactor(web): 把组下标从确认弹层的 state 里拔掉，分屏可插入任意位置

组下标只在一次事件内可靠——确认弹层开着时用户分屏/关栏，存下来的下标
就指向别的栏，点确认关掉的是另一栏的 tab。改为存 tabId 由 closeById
反查。地基铺好之后 splitGroup 才能推广成 splitGroupAt。"
```

---

## Task 7: `TaskPickerDialog` 组件

**Files:**
- Create: `web/src/app/workbench/TaskPickerDialog.tsx`
- Test: `web/src/app/workbench/TaskPickerDialog.test.tsx`

**Interfaces:**
- Consumes: 无
- Produces:
  ```ts
  export interface TaskPickerDialogProps {
    open: boolean
    base: BaseDir
    tree: ProjectTreeResp | null
    tasks: Task[]
    onPick: (taskId: string) => void
    onClose: () => void
  }
  export function TaskPickerDialog(props: TaskPickerDialogProps): ReactNode
  // 供测试与 Task 8 复用的纯函数：
  export function projectIdOfBase(tree: ProjectTreeResp | null, base: BaseDir): string | null
  export function isTerminalState(state: string): boolean
  ```
  Task 8 挂载它。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/workbench/TaskPickerDialog.test.tsx`。照 `web/src/app/overlay/TicketsOverlay.test.tsx` 的形状写（先读它）：

```tsx
describe('TaskPickerDialog', () => {
  it('只列当前基准所属项目的任务，别的项目不出现', () => { /* … */ })
  it('别的分支的任务也在列表里——这正是这个弹层存在的理由', () => { /* … */ })
  it('已结束任务默认折叠，标题上带条数；点开才显示', () => { /* … */ })
  it('每行带目录短名，同名任务能区分开', () => { /* … */ })
  it('搜索框按任务名过滤', () => { /* … */ })
  it('点一行触发 onPick 带上 taskId', () => { /* … */ })
  it('Esc 触发 onClose', () => { /* … */ })
  it('这个项目没有任务时显示空态文案，不是空列表', () => { /* … */ })
})

describe('projectIdOfBase', () => {
  it('按 base.path 在树上反查所属项目', () => { /* … */ })
  it('树还没到位或路径不在树上时返回 null', () => { /* … */ })
})
```

每个用例都要写成真断言，不是留空。

- [ ] **Step 2: 跑测试确认失败**

```bash
cd web && npm run test -- src/app/workbench/TaskPickerDialog.test.tsx
```

- [ ] **Step 3: 实现**

创建 `web/src/app/workbench/TaskPickerDialog.tsx`：

```tsx
// TaskPickerDialog —— 给当前 tab 选一个任务打开（spec §2）。
//
// 职责：列出当前基准所属项目的全部任务（跨机器、含已结束），带搜索，
// 选中后把 taskId 抛给调用方。
//
// 边界：
//   - 不自己开 tab：选中即回调，是 setContent 还是 open 由调用方决定
//   - 不发任何请求：任务来自已有的 2.5s 任务流，项目归属来自已有的项目树
//   - 不做筛选条、不分状态栏——那是看板的形态。这里是「给这个 tab 选一个」，
//     一个搜索框加一个列表就够了
//
// 为什么不复用看板弹层：看板是全屏、按状态分栏、带筛选条的**纵览**形态；
// 把「我只是想在这个 tab 里开个任务」变成一次全屏导航是不对等的交换。
// 两者都能开 TUI，但意图不同（spec §2.1）。
import { useEffect, useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import type { ProjectTreeResp, Task } from '../../api/types'
import { StateDot } from '../board/StateDot'
import { stateTone } from '../board/columns'
import type { BaseDir } from './useWorkbench'

// TERMINAL_STATES 是「已结束」的判据。
//
// 与看板的分栏口径共用同一批字符串——一个任务在看板上归"已完成"、在这里却
// 出现在"进行中"，两个面就自相矛盾了。
const TERMINAL_STATES = new Set(['done', 'failed', 'cancelled', 'archived'])

// isTerminalState 判断一个任务状态是否终态。
export function isTerminalState(state: string): boolean {
  return TERMINAL_STATES.has(state)
}

// projectIdOfBase 在项目树上反查基准目录所属的项目 id；查不到返回 null。
//
// 为什么不用 base.projectName：登记名只在一台机器内唯一，同一个项目在两台机器
// 上可以叫不同的名字（proto 的 ProjectNode.Name 注释写明了这条）。project_id
// 才是跨机同一的身份。
//
// 返回 null 的两种情形都是真实的：树还没加载完，或这个目录已经不在树上
// （工作树被删但 tab 还开着）。调用方应显示空态而不是当异常处理。
export function projectIdOfBase(tree: ProjectTreeResp | null, base: BaseDir): string | null {
  if (tree === null) return null
  for (const p of tree.projects) {
    for (const loc of p.locations) {
      if (loc.workspaces.some((ws) => ws.path === base.path)) return p.project_id
    }
  }
  return null
}

// dirLabelOfTask 给一行任务配一个能认人的目录短名。
//
// 这个弹层存在的理由就是「打开**别的分支**的任务」，不显示分支等于让用户在
// 一堆同名任务里猜。work_dir 为空（原地模式）时说「主目录」，不编一个假分支名。
function dirLabelOfTask(t: Task): string {
  if (t.branch !== '') return t.branch
  if (t.work_dir === '') return '主目录'
  const seg = t.work_dir.split('/').filter(Boolean)
  return seg.length > 0 ? seg[seg.length - 1] : t.work_dir
}

// taskName 与左栏同一口径：名字 → 计划摘要 → 兜底。
function taskName(t: Task): string {
  return t.name || t.plan_summary || '（无名称）'
}

export function TaskPickerDialog({ open, base, tree, tasks, onPick, onClose }: TaskPickerDialogProps) {
  const [query, setQuery] = useState('')
  // doneOpen 记「已结束那一段展开了没」。默认收起：历史堆积（实测单机 60 条）
  // 会把正在做的活挤出视口，这与左栏「已结束」分组默认收起是同一条理由
  const [doneOpen, setDoneOpen] = useState(false)

  // 每次重新打开都回到初始态：上次搜的词留着会让人以为「这个项目只有这几个任务」
  useEffect(() => {
    if (open) {
      setQuery('')
      setDoneOpen(false)
    }
  }, [open])

  // Esc 关闭。挂 window 而不是容器：弹层打开时它是唯一的交互焦点，
  // 不存在「该归谁」的竞争（与 BlankTab 的 ⌘T 不同，那里可能有两个面板同时在屏上）
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const projectId = useMemo(() => projectIdOfBase(tree, base), [tree, base])

  const { live, done } = useMemo(() => {
    const q = query.trim().toLowerCase()
    const mine = tasks.filter((t) => projectId !== null && t.project_id === projectId)
    const hit = q === '' ? mine : mine.filter((t) => taskName(t).toLowerCase().includes(q))
    // 按 updated_at 倒序：最近动过的最可能是要找的那个
    const byRecent = (a: Task, b: Task) => (a.updated_at < b.updated_at ? 1 : a.updated_at > b.updated_at ? -1 : 0)
    return {
      live: hit.filter((t) => !isTerminalState(t.state)).sort(byRecent),
      done: hit.filter((t) => isTerminalState(t.state)).sort(byRecent),
    }
  }, [tasks, projectId, query])

  if (!open) return null

  const row = (t: Task) => (
    <li key={t.id}>
      <button
        type="button"
        onClick={() => onPick(t.id)}
        className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-accent"
      >
        <StateDot tone={stateTone(t.state)} />
        <span className="min-w-0 flex-1 truncate">{taskName(t)}</span>
        <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{dirLabelOfTask(t)}</span>
      </button>
    </li>
  )

  return (
    // z-50 与既有 Overlay 同层：浮窗（z-40）应当被盖住
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[12vh]"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-label="选择要打开的任务"
        className="flex max-h-[60vh] w-[min(560px,90vw)] flex-col overflow-hidden rounded-lg border bg-background shadow-xl"
        // 点内容区不该关掉弹层——遮罩上那次 onClick 会冒泡上来
        onClick={(e) => e.stopPropagation()}
      >
        <label className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索任务"
            className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
        </label>
        <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
          {live.length === 0 && done.length === 0 ? (
            // 空态必须有话说：一片空白会被当成加载失败
            <p className="px-2 py-6 text-center text-sm text-muted-foreground">
              {projectId === null ? '这个目录还没归到任何项目下。' : '这个项目下还没有任务。'}
            </p>
          ) : (
            <>
              <ul>{live.map(row)}</ul>
              {done.length > 0 && (
                <>
                  <button
                    type="button"
                    aria-expanded={doneOpen}
                    onClick={() => setDoneOpen((v) => !v)}
                    className="mt-1 w-full px-2 py-1 text-left text-[11px] font-medium uppercase tracking-wide text-muted-foreground hover:text-foreground"
                  >
                    已结束 {done.length}
                  </button>
                  {doneOpen && <ul>{done.map(row)}</ul>}
                </>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
```

`TERMINAL_STATES` 里那四个字符串**必须与 `web/src/app/board/columns.ts` 的终态口径核对一致**——先读那个文件，以它为准；不一致就改这里，不要改 columns.ts。

- [ ] **Step 4: 跑测试确认通过**

```bash
cd web && npm run test -- src/app/workbench/TaskPickerDialog.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add web/src/app/workbench/TaskPickerDialog.tsx web/src/app/workbench/TaskPickerDialog.test.tsx
git commit -m "feat(web): 任务选择器弹层

给当前 tab 选一个任务：当前项目跨机器、含已结束、带搜索。已结束默认
折叠。不复用看板——看板是全屏纵览形态，把「在这个 tab 里开个任务」
变成一次全屏导航不对等。"
```

---

## Task 8: 选择器接线 + 删掉 PICK_HINT

**Files:**
- Modify: `web/src/app/workbench/WorkbenchPage.tsx`
- Modify: `web/src/app/workbench/BlankTab.tsx`
- Modify: `web/src/app/shell/Shell.tsx`
- Test: `WorkbenchPage.test.tsx`、`BlankTab` 相关用例

**Interfaces:**
- Consumes: `TaskPickerDialog`（Task 7）
- Produces: `WorkbenchPageProps` 新增 `tree: ProjectTreeResp | null`、`tasks: Task[]`

- [ ] **Step 1: 写失败的测试**

`WorkbenchPage.test.tsx` 追加：

```tsx
it('空白 tab 点「打开任务」弹出选择器，选中后原地变成 TUI tab', async () => { /* … */ })
it('选中的任务已在别的 tab 里开着时，激活那个并关掉这个空白 tab', async () => { /* … */ })
it('空组面板点「打开任务」先开一个空白 tab 承接，再弹选择器', async () => { /* … */ })
```

写成真断言。

- [ ] **Step 2: 跑测试确认失败**

- [ ] **Step 3: 改 `BlankTab.tsx`**

- `PickKind` 改为 `'terminal' | 'newfile' | 'tui'`
- `PICK_ITEMS` 改为：

```ts
export const PICK_ITEMS: { kind: PickKind; label: string; hotkey: string; icon: typeof TerminalSquare }[] = [
  { kind: 'terminal', label: '新终端', hotkey: '⌘T', icon: TerminalSquare },
  { kind: 'newfile', label: '新建文件', hotkey: '⌘N', icon: FilePlus },
  { kind: 'tui', label: '打开任务', hotkey: '⌘⇧A', icon: Bot },
]
```

import 把 `FileText` 换成 `FilePlus`。

- `hotkeyOf` 里 `⌘⇧O` 那条换成：

```ts
  if (k === 'n' && !e.shiftKey) return 'newfile'
```

- **删掉** `hint` / `onBack` 两个 prop、它们在 `BlankTabProps` 里的声明与注释、组件里 `if (hint) { … }` 那整段返回，以及 `useEffect` 里对 `hint` 的依赖（改成 `[]`，并把注释里解释「依赖 hint 而不是空数组」的那段一起删掉——那条理由已经不存在了）。

- [ ] **Step 4: 改 `WorkbenchPage.tsx`**

- **删掉** `PICK_HINT` 常量及其上方注释、`awaiting` state、`back()` 函数，以及 `BlankTab` 上的 `hint` / `onBack` 两个 prop。
- `WorkbenchPageProps` 加：

```ts
  // tree / tasks 只为任务选择器而收。中央区自己不消费它们——这是刻意的
  // 转手：选择器的生命周期属于某个具体 tab，挂在 Shell 上就得再往下传一个
  // 「现在是哪个 tab 在等」，那个状态本来就该住在这里
  tree: ProjectTreeResp | null
  tasks: Task[]
```

- 加 state 与两个动作：

```ts
  // picking 记「哪个空白 tab 正在选任务」。null = 弹层关闭。
  // 与被它取代的 awaiting 不同：那个是「已选种类、等目标」的中间态，会在
  // 面板上画出一句指路文案；这个只是一个弹层的开关，tab 本身仍是空白 tab
  const [picking, setPicking] = useState<{ group: number; tabId: string } | null>(null)
```

`pick` 改为：

```ts
  const pick = (group: number, tabId: string, kind: PickKind) => {
    if (kind === 'terminal') {
      if (terminalUnavailable) return
      api.setContent(group, tabId, { kind: 'terminal', seq: nextTerminalSeq(wb) })
      return
    }
    if (kind === 'tui') {
      setPicking({ group, tabId })
      return
    }
    // newfile 在 Task 10 接上；此刻先留空，Task 10 会替换这一支
  }
```

`startFromEmpty` 的非终端分支保持 `api.open({ kind: 'blank' }, undefined, group)` 不变——空组里先开一个空白 tab 承接，用户随即在它上面看到面板。

在返回的 JSX 末尾（最外层 div 内）加：

```tsx
      {picking !== null && base !== null && (
        <TaskPickerDialog
          open
          base={base}
          tree={tree}
          tasks={tasks}
          onPick={(taskId) => {
            api.setContent(picking.group, picking.tabId, { kind: 'tui', taskId })
            setPicking(null)
          }}
          onClose={() => setPicking(null)}
        />
      )}
```

`setTabContent` 已有的去重分支会处理「这个任务已经在别的 tab 里开着」：激活那个、关掉这个空白 tab。这里不需要额外判断。

- [ ] **Step 5: 改 `Shell.tsx`**

给 `<WorkbenchPage>` 加 `tree={treeState.data}` 与 `tasks={tasks}`。

- [ ] **Step 6: 跑测试确认通过**

```bash
cd web && npm run test -- src/app/workbench/ src/app/shell/
```
既有测试里凡断言 `PICK_HINT` 文案或"返回选择"按钮的，**删掉那些用例**（它们测的是已经不存在的形态），不要改成断言新形态——新形态已经由 Task 7/8 的新用例覆盖。

- [ ] **Step 7: 全套前端验证**

```bash
cd web && npm run test && npm run lint && npm run typecheck
```

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat(web): 空白 tab 接上任务选择器，删掉指路文案那套中间态

PICK_HINT / awaiting / BlankTab 的 hint+onBack 一并删除：那套东西
存在的唯一理由是没有选择器，留着等于同一件事有两个说法。"
```

---

## Task 9: 拖拽分屏

**Files:**
- Create: `web/src/app/workbench/paneDrop.ts`
- Test: `web/src/app/workbench/paneDrop.test.ts`
- Modify: `web/src/app/tree/ProjectTree.tsx`（任务行可拖）
- Modify: `web/src/app/workbench/WorkbenchPage.tsx`（投放区）
- Test: `WorkbenchPage.test.tsx`（补充）

**Interfaces:**
- Consumes: `splitAt`（Task 6）
- Produces:
  ```ts
  export type DropZone = 'left' | 'right' | 'center'
  export function dropZoneAt(offsetX: number, width: number, canSplit: boolean): DropZone
  export const DRAG_TASK_MIME = 'text/handoff-task'
  export const DRAG_BASE_MIME = 'text/handoff-base'
  ```

- [ ] **Step 1: 写失败的测试（纯函数）**

创建 `web/src/app/workbench/paneDrop.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { dropZoneAt } from './paneDrop'

describe('dropZoneAt', () => {
  it('左侧边缘算 left，右侧边缘算 right，中间算 center', () => {
    expect(dropZoneAt(10, 400, true)).toBe('left')
    expect(dropZoneAt(390, 400, true)).toBe('right')
    expect(dropZoneAt(200, 400, true)).toBe('center')
  })

  it('边缘区取 25% 与 120px 的较小者——宽栏上 25% 会让人频繁误触发分屏', () => {
    // 800px 宽：25% 是 200px，但上限 120px 生效
    expect(dropZoneAt(150, 800, true)).toBe('center')
    expect(dropZoneAt(110, 800, true)).toBe('left')
    expect(dropZoneAt(690, 800, true)).toBe('right')
  })

  it('窄栏上 25% 小于 120px，此时 25% 生效', () => {
    // 200px 宽：25% 是 50px
    expect(dropZoneAt(40, 200, true)).toBe('left')
    expect(dropZoneAt(60, 200, true)).toBe('center')
  })

  it('不能再分屏时边缘退化成 center，不给一次落空的拖拽', () => {
    expect(dropZoneAt(10, 400, false)).toBe('center')
    expect(dropZoneAt(390, 400, false)).toBe('center')
  })

  it('宽度为 0（还没布局完）时一律 center，不做除法', () => {
    expect(dropZoneAt(0, 0, true)).toBe('center')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

- [ ] **Step 3: 实现 `paneDrop.ts`**

```ts
// paneDrop —— 中央区投放区的判定规则（纯函数，无 React 依赖）。
//
// 职责：给一次拖放在某一栏内的横向位置，算出它落在左边缘 / 右边缘 / 中间。
//
// 边界：不认识 tab、不认识任务、不碰 DOM。调用方量好宽度与偏移量传进来。
import type { BaseDir } from './useWorkbench'

// DRAG_TASK_MIME / DRAG_BASE_MIME 是拖放数据的自定义类型。
//
// 为什么不用 text/plain：中央区只认这两个类型，从别处（浏览器地址栏、桌面
// 文件、编辑器选中文本）拖进来时不会被误判成一次任务拖放。
export const DRAG_TASK_MIME = 'text/handoff-task'
export const DRAG_BASE_MIME = 'text/handoff-base'

// EDGE_RATIO / EDGE_MAX_PX 共同决定边缘区有多宽，取两者的较小值。
//
// 为什么要有像素上限：栏可以被拖得很宽，一个 800px 的栏上 25% 就是 200px 的
// 边缘区，会让「我只是想在这栏开个 tab」频繁误触发分屏。
const EDGE_RATIO = 0.25
const EDGE_MAX_PX = 120

// DropZone 是一次拖放的三种落点。
export type DropZone = 'left' | 'right' | 'center'

// dropZoneAt 判定投放区。
//
// 参数：
//   - offsetX: 指针相对该栏左边缘的横向偏移（像素）
//   - width: 该栏的宽度（像素）
//   - canSplit: 现在还能不能再分出一栏（未到 MAX_GROUPS）
//
// 返回：'left' / 'right' 表示在该栏的那一侧插入新栏；'center' 表示在该栏开 tab。
//
// canSplit 为假时边缘退化成 center 而不是「无效投放」：拖放过程中没有地方放
// 一句「最多三栏」的提示，而一次落空的拖拽比一次「落在了这栏」更让人困惑
// （spec §3.2）。这与「分屏按钮到上限置灰」不冲突——按钮是常驻控件，说得起话。
export function dropZoneAt(offsetX: number, width: number, canSplit: boolean): DropZone {
  // 宽度量不到（jsdom、尚未布局完成）时一律 center：宁可这一次不分屏，
  // 也不要因为除以 0 得到 NaN 而让整次拖放失灵
  if (!canSplit || width <= 0) return 'center'
  const edge = Math.min(width * EDGE_RATIO, EDGE_MAX_PX)
  if (offsetX < edge) return 'left'
  if (offsetX > width - edge) return 'right'
  return 'center'
}

// readDragBase 从 dataTransfer 里取出拖源写进去的基准目录；没有或解析失败返回 null。
//
// 解析失败返回 null 而不是抛错：dataTransfer 里的内容不是我们能保证的
// （用户可能从别的应用拖了个同名类型进来），一次拖放不该把界面炸掉。
export function readDragBase(raw: string): BaseDir | null {
  if (raw === '' || raw === 'null') return null
  try {
    const v = JSON.parse(raw) as BaseDir
    return typeof v?.key === 'string' && typeof v?.path === 'string' ? v : null
  } catch {
    return null
  }
}
```

- [ ] **Step 4: 跑纯函数测试确认通过**

```bash
cd web && npm run test -- src/app/workbench/paneDrop.test.ts
```

- [ ] **Step 5: 左栏任务行加 draggable**

`ProjectTree.tsx` 里**三处**任务行（目录下的、已结束分组里的、未归属分组里的）都加：

```tsx
                                draggable
                                onDragStart={(e) => {
                                  e.dataTransfer.setData(DRAG_TASK_MIME, t.id)
                                  // 基准一并写进去：中央区据此判断这是不是跨基准拖放，
                                  // 不必再反查一遍树。未归属任务写 'null'
                                  e.dataTransfer.setData(DRAG_BASE_MIME, JSON.stringify(base ?? null))
                                  e.dataTransfer.effectAllowed = 'copy'
                                }}
```

- 目录下的任务行：`base` 就是那一行的 `base`
- 已结束分组：用 `archivedBase(project, loc.machine)`
- 未归属分组：写 `null`

import 补 `import { DRAG_BASE_MIME, DRAG_TASK_MIME } from '../workbench/paneDrop'`。

在文件头注释的「点击语义」那段之后补一段：

```
// 拖放（W4 §3）：任务行可拖进中央区。拖到某一栏的边缘 = 在那一侧分出新栏
// 并在其中打开；拖到栏中间 = 在那一栏开一个 tab。数据用自定义 MIME，从别处
// 拖进来的东西不会被误判。拖动不影响点击——HTML5 拖放只在真的拖起来之后
// 才吞掉 click。
```

- [ ] **Step 6: 中央区加投放区**

`WorkbenchPage.tsx`：

state：

```ts
  // dragOver 记「指针现在悬在哪一栏的哪个区」，只用于高亮。null = 没有拖放在进行
  const [dragOver, setDragOver] = useState<{ group: number; zone: DropZone } | null>(null)
```

在每个 `<section>` 上加事件（`gi` 是该栏下标）：

```tsx
              onDragOver={(e) => {
                // 没有我们的数据类型就不接管：让浏览器按默认行为处理，
                // 否则从别处拖进来的东西会显示成"可以放在这里"却什么也不发生
                if (!e.dataTransfer.types.includes(DRAG_TASK_MIME)) return
                e.preventDefault()
                e.dataTransfer.dropEffect = 'copy'
                const r = e.currentTarget.getBoundingClientRect()
                const zone = dropZoneAt(e.clientX - r.left, r.width, wb.groups.length < MAX_GROUPS)
                setDragOver({ group: gi, zone })
              }}
              onDragLeave={(e) => {
                // 只在真的离开这一栏时清高亮：拖过子元素边界也会触发 dragleave，
                // 不加这个判断高亮会疯狂闪烁
                if (e.currentTarget.contains(e.relatedTarget as Node | null)) return
                setDragOver((prev) => (prev?.group === gi ? null : prev))
              }}
              onDrop={(e) => {
                const taskId = e.dataTransfer.getData(DRAG_TASK_MIME)
                setDragOver(null)
                if (taskId === '') return
                e.preventDefault()
                const r = e.currentTarget.getBoundingClientRect()
                const zone = dropZoneAt(e.clientX - r.left, r.width, wb.groups.length < MAX_GROUPS)
                const from = readDragBase(e.dataTransfer.getData(DRAG_BASE_MIME))
                onDropTask(gi, zone, taskId, from)
              }}
```

高亮用一个绝对定位的覆盖层（放在 `<section>` 内、`TabBar` 之前）：

```tsx
              {dragOver?.group === gi && (
                <div
                  aria-hidden="true"
                  data-testid={`drop-${dragOver.zone}`}
                  className={cn(
                    'pointer-events-none absolute inset-0 z-10',
                    dragOver.zone === 'center' && 'ring-2 ring-inset ring-primary/60',
                  )}
                >
                  {dragOver.zone !== 'center' && (
                    <span
                      className={cn(
                        'absolute inset-y-0 w-[3px] bg-primary',
                        dragOver.zone === 'left' ? 'left-0' : 'right-0',
                      )}
                    />
                  )}
                </div>
              )}
```

`<section>` 需要加 `relative` 到 className 里（覆盖层的定位祖先）。import 补 `cn`。

投放处理函数：

```ts
  // onDropTask 处理一次任务拖放。
  //
  // 参数：
  //   - group: 落在哪一栏
  //   - zone: 落在该栏的哪个区
  //   - taskId: 拖来的任务
  //   - from: 该任务所属的基准目录；null = 未归属任务（用当前基准开）
  //
  // 跨基准拖放（from 与当前基准不是同一个）时**位置语义退化**：工作台整体切到
  // from，边缘投放变成「在末尾新开一栏」。理由是 group 这个下标是在**当前**
  // 基准的 tab 组里算的，切过去之后那一套组已经换了一批（byBase 那张 Map），
  // 下标不再指任何东西。硬要保留位置就得先切基准、等重渲染、再重新命中投放区，
  // 那是两帧之后的事，而拖放在落下的那一刻就要给出结果（spec §3.4）。
  const onDropTask = (group: number, zone: DropZone, taskId: string, from: BaseDir | null) => {
    const content: TabContent = { kind: 'tui', taskId }
    // from 为 null = 未归属任务，它没有自己的目录，用当前基准开——与在左栏
    // 点它的行为一致（Shell 的 openTaskTui 也是这条回退）
    if (from !== null && from.key !== base.key) {
      if (zone === 'center') {
        // 带显式基准的 open 内部会先 select 过去，一步到位
        api.open(content, from)
        return
      }
      // 边缘投放退化成「末尾新开一栏」。三步必须按这个顺序：select 同步更新
      // useWorkbench 的 baseRef，所以后两步落在**新基准**的那套 tab 组上
      api.select(from)
      api.split()
      api.open(content)
      return
    }
    if (zone === 'center') {
      api.open(content, undefined, group)
      return
    }
    // 插在左边时新栏就占据 group 这个下标，原来那栏被推到 group+1；
    // 插在右边时新栏是 group+1。两种情况下「新栏的下标」都等于插入位置
    const at = zone === 'left' ? group : group + 1
    api.splitAt(at)
    api.open(content, undefined, at)
  }
```

`splitAt` 与 `open` 是两次 `setState`，但都走 `useWorkbench.mutate` 的函数式更新，第二次拿到的是第一次的结果，不会读到旧值。`dropZoneAt` 在到达 `MAX_GROUPS` 时已经返回 `'center'`，所以这里不需要再判一次上限。

- [ ] **Step 7: 写组件测试**

`WorkbenchPage.test.tsx` 追加。jsdom 不实现 `DataTransfer`，也不给元素真实布局，所以两样都得自己造：

```tsx
// dt 造一个够用的 DataTransfer 替身。
// jsdom 里 fireEvent.drop 的 dataTransfer 是我们自己塞进去的普通对象，
// 只要有 types / getData 两样，被测代码就跑得动
function dt(taskId: string, from: BaseDir | null) {
  const data: Record<string, string> = {
    'text/handoff-task': taskId,
    'text/handoff-base': JSON.stringify(from),
  }
  return { types: Object.keys(data), getData: (k: string) => data[k] ?? '', dropEffect: '' }
}

// layout 给一个元素钉死 getBoundingClientRect。
// jsdom 里所有元素的宽高都是 0，不钉的话 dropZoneAt 恒返回 center，
// 三个投放区的用例会全部"通过"却什么也没测到
function layout(el: Element, width: number) {
  el.getBoundingClientRect = () =>
    ({ left: 0, top: 0, right: width, bottom: 400, width, height: 400, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
}

describe('拖放投放区', () => {
  // 这些用例共用一个已选中基准、单栏、栏宽 400px 的工作台。
  // 400px 宽下边缘区是 min(100, 120) = 100px
  const setup = () => {
    // 照本文件既有用例的方式渲染 WorkbenchPage（含 base、api、renderContent）
    // 返回 { api, section } —— section 是 <section> 那个 DOM 节点
  }

  it('拖到栏中间：在那一栏开 tab，不分屏', () => {
    const { api, section } = setup()
    layout(section, 400)
    fireEvent.drop(section, { clientX: 200, dataTransfer: dt('T1', null) })
    expect(api.wb.groups).toHaveLength(1)
    expect(api.wb.groups[0].tabs.at(-1)?.content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('拖到右边缘：在右边分出新栏并在其中打开', () => {
    const { api, section } = setup()
    layout(section, 400)
    fireEvent.drop(section, { clientX: 390, dataTransfer: dt('T1', null) })
    expect(api.wb.groups).toHaveLength(2)
    expect(api.wb.groups[1].tabs).toHaveLength(1)
    expect(api.wb.groups[1].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('拖到左边缘：新栏插在左边，原来那栏被推到右边', () => {
    const { api, section } = setup()
    layout(section, 400)
    fireEvent.drop(section, { clientX: 10, dataTransfer: dt('T1', null) })
    expect(api.wb.groups).toHaveLength(2)
    expect(api.wb.groups[0].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('已到三栏时拖边缘退化成在这栏开 tab，不是无效投放', () => {
    const { api, section } = setup()
    // 先分到三栏（MAX_GROUPS）
    act(() => { api.split(); api.split() })
    layout(section, 400)
    fireEvent.drop(section, { clientX: 390, dataTransfer: dt('T1', null) })
    expect(api.wb.groups).toHaveLength(3)
    // 落在了被拖到的那一栏，而不是什么都没发生
    expect(api.wb.groups.flatMap((g) => g.tabs)).toHaveLength(1)
  })

  it('没有 handoff MIME 的拖放被忽略——从别处拖进来的东西不该开出 tab', () => {
    const { api, section } = setup()
    layout(section, 400)
    fireEvent.drop(section, {
      clientX: 200,
      dataTransfer: { types: ['text/plain'], getData: () => 'https://example.com', dropEffect: '' },
    })
    expect(api.wb.groups.flatMap((g) => g.tabs)).toHaveLength(0)
  })
})
```

`setup()` 的函数体要你按本文件既有用例的渲染方式补完——**断言部分已经给死，不要改**。`api` 需要能被测试读到当前 `wb`，若既有用例已有取 api 的做法就复用它；没有的话用 `renderHook(useWorkbench)` 把 api 造出来再传给 `WorkbenchPage`。

- [ ] **Step 8: 全套前端验证**

```bash
cd web && npm run test && npm run lint && npm run typecheck
```

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat(web): 左栏任务可拖进中央区，边缘分屏中间开 tab

边缘区取 25% 与 120px 的较小者——宽栏上 25% 会让人频繁误触发分屏。
到三栏上限时边缘退化成中间而不是无效投放：拖放过程中没地方放提示，
一次落空的拖拽比一次「落在了这栏」更让人困惑。"
```

---

## Task 10: 空白 tab 新建文件

**Files:**
- Create: `web/src/app/workbench/newFile.ts`
- Test: `web/src/app/workbench/newFile.test.ts`
- Modify: `web/src/app/workbench/WorkbenchPage.tsx`
- Modify: `web/src/app/files/FileTree.tsx`（`refreshKey`）
- Modify: `web/src/app/shell/Shell.tsx`

**Interfaces:**
- Consumes: `PickKind` 的 `'newfile'`（Task 8）
- Produces:
  ```ts
  export function nextUntitledName(existing: string[]): string
  export async function createUntitledFile(base: BaseDir): Promise<string>  // 返回新文件的 rel
  ```
  Task 11 复用两者。

- [ ] **Step 1: 写失败的测试**

创建 `web/src/app/workbench/newFile.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { nextUntitledName } from './newFile'

describe('nextUntitledName', () => {
  it('空目录得到 untitled-1.md', () => {
    expect(nextUntitledName([])).toBe('untitled-1.md')
  })

  it('跳过已占用的编号', () => {
    expect(nextUntitledName(['untitled-1.md', 'untitled-2.md'])).toBe('untitled-3.md')
  })

  it('中间空出来的编号会被捡回来——连着建删几次不该一直往上爬', () => {
    expect(nextUntitledName(['untitled-1.md', 'untitled-3.md'])).toBe('untitled-2.md')
  })

  it('不相干的文件不影响编号', () => {
    expect(nextUntitledName(['README.md', 'untitled.md', 'untitled-a.md'])).toBe('untitled-1.md')
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

- [ ] **Step 3: 实现 `newFile.ts`**

```ts
// newFile —— 「新建一个空白文件」的命名与创建（spec §4.2 / §5.2）。
//
// 职责：在一个基准目录根下挑一个不冲突的 untitled-N.md，把它建出来。
//
// 边界：
//   - 只建文件，不打开它——打开是调用方的事（中央区变成 file tab，
//     浮窗开一个 file tab，两处对同一个结果有不同的处置）
//   - 不吞错误：agentd 的中文原文原样抛出，由调用方展示
import { createWorkspaceEntry, fetchWorkspaceDir } from '../../api/client'
import type { BaseDir } from './useWorkbench'

// UNTITLED_RE 匹配 untitled-<正整数>.md，捕获编号。
const UNTITLED_RE = /^untitled-(\d+)\.md$/

// nextUntitledName 在已有条目名里挑第一个空出来的 untitled 编号。
//
// 参数：existing 该目录下的全部条目名（文件与目录都算——同名目录一样会撞）
// 返回：形如 'untitled-3.md' 的单层文件名
//
// 为什么捡中间空出来的编号而不是一直取 max+1：连着建了删、删了建几次之后，
// max+1 会爬到 untitled-47.md，而目录里其实只有一个文件。
//
// 为什么固定 .md：总得选一个，而 .md 在纯文本编辑器里无害且是记东西最常见的
// 格式。想要别的后缀可以在右栏或浮窗里改名，那是一步既有操作。
export function nextUntitledName(existing: string[]): string {
  const used = new Set<number>()
  for (const name of existing) {
    const m = UNTITLED_RE.exec(name)
    if (m !== null) used.add(Number(m[1]))
  }
  let n = 1
  while (used.has(n)) n++
  return `untitled-${n}.md`
}

// createUntitledFile 在基准目录根下建一个空白文件，返回它的相对路径。
//
// 参数：base 基准目录（工作树或草稿区都行——两者都在文件端点的白名单里）
// 返回：新文件的 rel（就是文件名，因为它建在根上）
//
// 抛出：agentd 的错误原样上抛（ApiError）。**特别是 409**：列举与创建之间
// 另一个客户端建了同名文件时会撞上。这里**不重试**——那是真实的并发冲突，
// 静默重试会掩盖「有别人在动这个目录」这个事实，用户再点一次就好。
//
// 为什么先列举再命名，而不是从 1 开始建、撞 409 就 +1：后者在已经有
// untitled-1..9 的目录里会打出 9 个 409，服务端日志里全是拒绝记录，
// 排障时看着像出了故障。
export async function createUntitledFile(base: BaseDir): Promise<string> {
  const listed = await fetchWorkspaceDir(base.path, undefined, base.machine || undefined)
  const name = nextUntitledName(listed.entries.map((e) => e.name))
  await createWorkspaceEntry(base.path, '', name, 'file', base.machine || undefined)
  return name
}
```

- [ ] **Step 4: 跑测试确认通过**

- [ ] **Step 5: 接进 WorkbenchPage**

`pick` 的 `newfile` 分支：

```ts
    if (kind === 'newfile') {
      void createUntitledFile(base)
        .then((rel) => {
          api.setContent(group, tabId, { kind: 'file', rel })
          onFileCreated?.()
        })
        .catch((err: unknown) => setNewFileError(errorMessage(err)))
      return
    }
```

`startFromEmpty` 的 `newfile` 分支：先 `api.open({ kind: 'blank' }, undefined, group)`，与 tui 同路（用户随即在新开的空白 tab 上点一次）。**注意**：这样点空组面板上的「新建文件」需要点两次。这是有意的取舍——空组面板与空白 tab 面板是同一个组件的两次挂载，让空组那次直接建文件就得在这里凭空造一个 tab id，而 `openTab` 生成 id 是在纯函数内部。**如果你有干净的做法可以改进它，先发工单问审核者，不要自作主张改 `openTab` 的签名。**

state 与展示：

```ts
  // newFileError 是建文件失败的原文（409 撞名、磁盘满、白名单拒绝）。
  // 显示在中央区顶部而不是弹层：它不需要用户做决定，只需要被看见
  const [newFileError, setNewFileError] = useState('')
```

在中央区最外层 div 内、`wb.groups.map` 之前渲染：

```tsx
      {newFileError !== '' && (
        <p className="absolute inset-x-0 top-0 z-20 bg-destructive/10 px-3 py-1.5 text-xs text-destructive">
          新建文件失败：{newFileError}
        </p>
      )}
```

外层 div 加 `relative`。

`WorkbenchPageProps` 加：

```ts
  // onFileCreated 在新建文件成功后触发，让右栏文件树把新文件显示出来。
  // 可选：没有右栏时（home 基准）不需要它
  onFileCreated?: () => void
```

- [ ] **Step 6: 右栏加 `refreshKey`**

`FileTree.tsx`：

```ts
// props 加：
  // refreshKey 变化时重取根目录一层。调用方（Shell）在中央区新建文件之后
  // 递增它——不刷新的话用户刚建的文件在右栏看不见，会以为没建成。
  //
  // 用 reload('') 而不是 refresh()：新文件建在根上，只有那一层需要重取；
  // refresh 会丢掉全部已展开层的缓存，用户展开的目录会全部塌回去
  refreshKey?: number
```

组件内：

```ts
  const firstRef = useRef(true)
  useEffect(() => {
    // 首次挂载不重取：那一层由树自己的 ensure 负责，这里再来一次是白打一个请求
    if (firstRef.current) {
      firstRef.current = false
      return
    }
    dirs.reload('')
  }, [refreshKey, dirs])
```

`Shell.tsx`：

```ts
  // fileTreeNonce 是右栏刷新的触发器。中央区新建文件后递增它。
  // 用计数器而不是把 FileTree 的 refresh 传上来：那会把中央区与右栏焊死，
  // 而它们现在互不认识
  const [fileTreeNonce, setFileTreeNonce] = useState(0)
```

`<WorkbenchPage onFileCreated={() => setFileTreeNonce((n) => n + 1)} …>`
`<FileTree refreshKey={fileTreeNonce} …>`

- [ ] **Step 7: 组件测试**

`WorkbenchPage.test.tsx` 追加：

```tsx
it('点「新建文件」建出 untitled-1.md 并原地变成 file tab', async () => { /* mock client */ })
it('建文件失败时把 agentd 的原文显示出来，不吞成「操作失败」', async () => { /* … */ })
```

- [ ] **Step 8: 全套前端验证**

```bash
cd web && npm run test && npm run lint && npm run typecheck
```

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat(web): 空白 tab 的「打开文件」改为「新建文件」

先列举再挑编号，不是从 1 开始建、撞 409 就 +1——后者会在服务端日志里
打出一串拒绝记录，排障时看着像故障。真撞上并发 409 不重试，把原文透出。"
```

---

## Task 11: 浮窗临时文件

**Files:**
- Modify: `web/src/app/homedock/useHomeDock.ts`
- Modify: `web/src/app/homedock/HomeWindow.tsx`
- Modify: `web/src/app/homedock/HomeDock.tsx`
- Modify: `web/src/app/data/useMachineCaps.ts`
- Modify: `web/src/app/workbench/useWorkbench.ts`（`BaseDir.kind` 加 `'scratch'` + `scratchBase`）
- Modify: `web/src/app/shell/Shell.tsx`
- Test: `useHomeDock.test.ts`、`HomeWindow.test.tsx`、`HomeDock.test.tsx`

**Interfaces:**
- Consumes: `Machine.scratch_root`（Task 2）、`createUntitledFile`（Task 10）
- Produces: `HomeTab.kind`、`useHomeDock.newFile/setDraft`、`scratchBase(root, machine)`

- [ ] **Step 1: 写失败的测试**

`useHomeDock.test.ts` 追加：

```ts
it('newFile 建出一个 file 种类的 tab 并激活它、打开浮窗', () => { /* … */ })
it('终端与文件共用同一个只增不减的 seq 计数器，不会撞号', () => { /* … */ })
it('setDraft 把草稿寄存到 tab 上，切走再切回来还在', () => { /* … */ })
```

`HomeWindow.test.tsx` 追加：

```tsx
it('tab 条上有「新终端」与「新建临时文件」两个入口', () => { /* … */ })
it('file 种类的 tab 标题显示文件名而不是 bash · home N', () => { /* … */ })
```

全部写成真断言。

- [ ] **Step 2: 跑测试确认失败**

- [ ] **Step 3: `BaseDir` 加 `'scratch'`**

`useWorkbench.ts`：

```ts
export interface BaseDir {
  key: string
  // kind 三种：
  //   workspace —— git 工作树，可被左栏选中，右栏文件树跟着它
  //   home      —— 用户 home，只用于浮窗终端的标题，**不发给任何文件接口**
  //   scratch   —— agentd 的草稿区，只被浮窗里的 file tab 用来发文件请求
  //
  // scratch 刻意不伪装成 workspace：它不是 git 工作树，不该被左栏选中、
  // 不该有右栏文件树。把它标成 workspace 是一句半年后会骗到人的谎。
  kind: 'workspace' | 'home' | 'scratch'
  …
}

// scratchBase 把一台机器的草稿区路径做成 BaseDir，供浮窗里的文件 tab 用。
//
// 参数：root agentd 上报的草稿区绝对路径；machine 机器名（''=本机）
//
// 它**不进** byBase 那张 Map：草稿区不是可选中的基准目录，左栏点不到它、
// 面包屑也不显示它。它唯一的消费者是 FileTab（只读 path 与 machine 发请求）。
export function scratchBase(root: string, machine: string): BaseDir {
  return {
    key: `scratch:${machine}:${root}`,
    kind: 'scratch',
    path: root,
    label: '临时',
    projectName: '',
    machine,
  }
}
```

**加完这一支后审三处既有分支判断**（`Shell` 渲染右栏的 `kind === 'workspace'`、`BlankTab` 的两处 `kind === 'home'`），确认它们**不需要改**：scratch 从不是选中基准，也从不渲染 BlankTab，三处都走不到。**在这三处各留一句注释说明为什么不用管 scratch**，否则下一个人读到会以为漏了分支。

- [ ] **Step 4: `useMachineCaps` 加 `scratchRoot`**

```ts
export interface MachineCaps {
  pty: (machine: string) => boolean | null
  reveal: (machine: string) => boolean | null
  // scratchRoot 返回一台机器的草稿区路径；空串 = 这台机器不支持临时文件。
  //
  // 与 pty/reveal 的三态不同，这里是**两态**：缺的是一个路径，没有路径就没法
  // 发请求，「不知道所以放行」在这里只会换来一次必然 400（spec §5.1）。
  scratchRoot: (machine: string) => string
  error: string
}
```

实现照 `ptyMap` / `revealMap` 的形状加一张 `scratchMap: Record<string, string>`，只收非空的 `m.scratch_root`。

- [ ] **Step 5: `useHomeDock` 加种类与草稿**

按 spec §5.2 给 `HomeTab` 加 `kind` / `rel` / `draft` / `baseSha`；`newTerminal` 建的 tab 补 `kind: 'terminal'`；新增：

```ts
  // newFile 把一个已经建好的草稿区文件收成一个 file tab。
  //
  // 参数：rel 草稿区根下的文件名（由调用方先 createUntitledFile 建出来）
  //
  // 为什么不在这里建文件：这个 hook 的边界是「不发任何请求」（见文件头注释），
  // 建文件是一次 POST。破这条边界会让 useHomeDock 的单测需要 mock 网络。
  newFile: (rel: string) => void
  // setDraft 把草稿寄存到 tab 上。
  //
  // 必须寄存在 tab 上而不是 FileTab 的组件 state 里：浮窗同时只渲染激活 tab，
  // 切走即卸载，草稿活在组件里的话「点一下隔壁终端再切回来」改的字就全没了。
  // 与中央 file tab 的 draft 寄存是同一条理由、同一条路。
  setDraft: (id: string, d: { draft: string; baseSha: string } | null) => void
```

`seq` 仍由同一个 `seqCounter` 分配（两种 tab 共用，不撞号）。

- [ ] **Step 6: `HomeWindow` 按种类渲染**

`tabLabel` 改为：

```ts
// tabLabel 给一个 tab 出标题：终端按序号，文件按文件名。
function tabLabel(t: HomeTab): string {
  if (t.kind === 'file') return t.rel ?? '未命名'
  return t.seq === 1 ? 'bash · home' : `bash · home ${t.seq}`
}
```

图标同理按 `kind` 在 `TerminalSquare` 与 `FileText` 之间选。

props 加 `onNewFile: () => void`，在既有 `+`（新终端）旁边加第二个按钮：

```tsx
        <button
          type="button"
          aria-label="新建临时文件"
          title="新建临时文件（落在 agentd 的草稿区）"
          onClick={() => onNewFile()}
          className="my-auto inline-flex shrink-0 cursor-pointer rounded p-1 text-[#8e9bab] hover:bg-[#1a2430] hover:text-[#d7dde5]"
        >
          <FilePlus className="size-3.5" />
        </button>
```

既有 `+` 的 `aria-label` 保持「新终端」不变。**必须同样用箭头函数包一层**——文件头那段注释解释了为什么（`onClick={onNewFile}` 会把 MouseEvent 当实参传下去）。

关闭按钮的 `title` 按种类分：终端仍是「关闭并结束会话」，文件改成「关闭（文件保留在草稿区）」——文件关了还在磁盘上，沿用终端那句话是假话。

- [ ] **Step 7: `HomeDock` 与 `Shell` 接线**

`HomeDock` 加 `onNewFile` prop 直接透传给 `HomeWindow`；**FAB 的行为一字不改**（它仍是「开/收 + 没有 tab 时开终端」）。文件头注释里补一句为什么新入口不放 FAB 上。

`Shell.tsx`：

```ts
  // scratchRoot 是本机草稿区路径；空串 = 这台 agentd 不支持临时文件，
  // 浮窗里那个入口不渲染
  const scratchRoot = caps.scratchRoot('')

  // newScratchFile 建一个草稿区文件并把它收进浮窗。
  // 建文件是一次 POST，所以放在 Shell 而不是 useHomeDock（那个 hook 不发请求）
  const newScratchFile = () => {
    if (scratchRoot === '') return
    void createUntitledFile(scratchBase(scratchRoot, ''))
      .then((rel) => dock.newFile(rel))
      .catch((err: unknown) => setScratchError(errorMessage(err)))
  }
```

`renderTab` 按种类分发：

```tsx
          renderTab={(t) =>
            t.kind === 'file' ? (
              <FileTab
                base={scratchBase(scratchRoot, t.machine)}
                rel={t.rel ?? ''}
                initial={
                  t.draft !== undefined && t.baseSha !== undefined
                    ? { draft: t.draft, baseSha: t.baseSha }
                    : undefined
                }
                onDraftChange={(d) => dock.setDraft(t.id, d)}
              />
            ) : (
              <TerminalTab … />   /* 保持现状 */
            )
          }
```

`killHomeSession` 按种类分流：文件 tab 干净时直接 `dock.closeTab(id)`；有草稿时进新的确认 state：

```ts
  // closingDirtyHome 记「哪个浮窗文件 tab 有草稿、正在等确认」。
  //
  // 为什么不复用 closingDirtyFile：它的确认回调调的是 wb.closeById，而浮窗 tab
  // 根本不在 wb 里（useHomeDock 与 useWorkbench 刻意完全独立）。与既有的
  // closingPty / closingHome 那一对同构——那两个也是因为同一条独立性而分开的
  const [closingDirtyHome, setClosingDirtyHome] = useState<{ id: string; rel: string } | null>(null)
```

确认弹层的文案复用中央区那份（把 `closingDirtyFile?.rel` 换成两者中非空的那个），`onConfirm` 按哪个非空分别调 `wb.closeById` 或 `dock.closeTab`。

`onNewFile` 与入口显隐：`scratchRoot === ''` 时给 `HomeDock` 传 `onNewFile={undefined}`，`HomeWindow` 据此不渲染那个按钮——**不置灰**。置灰的控件承诺"以后能用"，而这台 agentd 就是没有这个能力，与既有的"终端不可用时摘掉该项"是同一条纪律。

- [ ] **Step 8: 跑全套测试**

```bash
cd web && npm run test && npm run lint && npm run typecheck
```

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat(web): 右下角浮窗新增临时文件

入口放在浮窗 tab 条的 + 旁边，不放 FAB——FAB 上那层清单面板是被特意
删掉的，塞回去等于把它请回来。agentd 不支持时入口不渲染而不是置灰：
置灰承诺「以后能用」，而这台机器就是没有这个能力。"
```

---

## Task 12: 整分支终审

- [ ] **Step 1: 全量验证**

```bash
go build ./...
go test ./...
gofmt -l .
cd web && npm run test && npm run lint && npm run typecheck && npm run build
```

`gofmt -l .` 有任何输出就跑 `gofmt -w` 修掉再提交——测试全绿不等于格式干净。

- [ ] **Step 2: 相对分支起点的完整 diff 自审**

```bash
git diff $(git merge-base HEAD origin/handoff/web-console)...HEAD
```

对照 spec 的 §1–§5 逐条确认实现到位、没有多做。特别检查：
- 每个新建文件有文件头注释（职责 + 边界）
- 每个导出函数有注释（参数、返回、注意事项）
- Go 侧错误分支都带上下文，没有 `fmt.Printf`
- 删掉的东西（`PICK_HINT`、`awaiting`、`hint`/`onBack`、旧 `splitGroup` 的过期注释）真的删干净了，没有留下引用

- [ ] **Step 3: 有发现项就一次性全量修，再做一次范围复审**

不逐项修、不搞第二轮修复波。

- [ ] **Step 4: Commit（若有终审修复）**

```bash
git add -A
git commit -m "fix: 整分支终审修复"
```

---

## Self-Review 记录

**Spec 覆盖检查：**

| Spec 章节 | 实现于 |
|-----------|--------|
| §1.1 排序键 + 主工作树置顶 | Task 3 |
| §1.2 计数口径 | Task 4 + Task 5 |
| §1.3 `Workspace.created_at` | Task 1 |
| §1.4 前端接线 | Task 5 |
| §1.5 排序不改的东西 | Task 5（只改目录行那一处 map） |
| §2.1 与看板分工 | Task 7（看板一字不改） |
| §2.2 组件 | Task 7 |
| §2.3 接线 | Task 8 |
| §2.4 净删除 | Task 8 |
| §3.1 拖源 | Task 9 |
| §3.2 投放区 | Task 9 |
| §3.3 前置修复 | Task 6 |
| §3.4 跨基准 | Task 9 |
| §4.1 面板改版 | Task 8（PICK_ITEMS）+ Task 10（行为） |
| §4.2 新建流程 | Task 10 |
| §4.3 右栏刷新 | Task 10 |
| §5.1 scratch 后端 | Task 2 |
| §5.2 前端 | Task 11 |
| §5.3 `BaseDir.kind` 加 scratch | Task 11 |
| §6 测试 | 各 task 内 + Task 12 |

无遗漏。

**类型一致性检查：** `WorkspaceMetrics`（Task 3 定义 → Task 5 消费）、`splitGroupAt`（Task 6 定义 → Task 9 消费）、`DropZone`/`dropZoneAt`（Task 9 内自洽）、`createUntitledFile`（Task 10 定义 → Task 11 消费）、`scratchBase`（Task 11 内自洽）、`Machine.scratch_root`（Task 2 定义 → Task 11 消费）—— 名称与签名前后一致。

**已知的两处「实现时需要判断」的地方**（已在正文标注，执行者若拿不准应发工单而不是自作主张）：
1. Task 5 里 `ticketsByDir.get('')` 恒为 0 的那一支——保留，不删。
2. Task 10 里空组面板点「新建文件」需要点两次——接受，除非有不改 `openTab` 签名的干净做法。
