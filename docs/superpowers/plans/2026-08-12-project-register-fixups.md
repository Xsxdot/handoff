# 项目登记干净流程 —— 评审修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修掉 `handoff/project-register-clean-flow` 代码评审出的 7 项问题——其中「clone-to-path 不做路径归一化导致落一条指向不存在路径的死记录」是阻塞合并项——让这条分支可以合进 `handoff/web-console`。

**Architecture:** 后端在 `RegisterProject` **入口**加一道路径形态闸门（`path` 必须是绝对路径），把整类「相对路径 / `~` 未展开 → clone 落点与落库路径不同基准」的缺陷从源头掐掉，而不是在每个用到 path 的分支各补一次。clone-to-path 的幂等短路对齐它的兄弟分支 `registerExistingProject`（同落点才幂等，异落点报 409）。前端把「远程未尝试」这个状态从组件里现编下沉到 `registerFromForm`，让编排知识只有一份；表单侧只做「省一次往返」的提示型校验，权威判定仍在 agentd。

**Tech Stack:** Go 1.x（`internal/agentd`、`cmd`）、React + TypeScript + vitest（`web/src/app/projects`）

## Global Constraints

- **基线分支：`handoff/project-register-clean-flow`**（HEAD = `371dd663`）。所有 task 在这条分支上顺序提交；不要 rebase 到 main。
- **锁定决策 1 —— `path` 必须是绝对路径**：`~` **不展开、直接 400**；相对路径同样 400。理由：`filepath.Abs` 的基准是 agentd 进程的 cwd，而调用方（尤其跨机那一跳）根本不知道那是哪个目录，"能算出一个路径"和"算出用户想要的路径"是两回事。宁可报错也不猜。
- **锁定决策 2 —— CLI `--path` 保留后端新语义**：路径不存在时 clone 到该路径。本 plan **只改 `cmd/project.go` 的文案**，不给 CLI 开后端特例，不引入 `must_exist` 之类的布尔位。
- **日志**：一律 `m.log`（`*slog.Logger`）。**禁止** `fmt.Printf` / `println` 作为日志手段。错误分支必须带 path / origin / cause。
- **注释**：中文。新函数写文档注释（参数、返回、注意），非显然分支写「为什么」而不是「做了什么」。
- **测试 helper（已存在，直接用，不要另造）**：
  - `newTestManagerWithAds(t, nil, "fake")` → `(m *Manager, _, _)`
  - `initGitRepo(t) string` → 造一个本地 git 仓（可当 clone 源）
  - `initGitRepoWithOrigin(t, origin string) string` → 造一个带指定 origin 的仓
- **前端测试**：vitest + @testing-library/react，跑之前先在 `web/` 下 `npm ci`（当前仓库 `web/node_modules` 不存在）。

---

## 文件地图

| 文件 | 本 plan 里的职责 |
|------|------------------|
| `internal/agentd/projectadmin.go` | 入口路径闸门（Task 1）、clone-to-path 幂等短路收紧（Task 2）、clone 失败回收新建目录 + `firstMissingAncestor` 助手（Task 6） |
| `internal/agentd/projectadmin_test.go` | Task 1/2/6 的红绿测试 |
| `cmd/project.go` | `--path` flag help 与三处注释同步到新语义（Task 3） |
| `web/src/app/projects/AddProjectWizard.tsx` | 远程重试带上表单 name（Task 4）、结果页按当前状态渲染「未尝试」文案（Task 5）、path 形态提示与提交闸门（Task 7） |
| `web/src/app/projects/register.ts` | `RegisterOutcome.skipped` 字段与 `registerFromForm` 补「未尝试」行（Task 5）、`absPathHint` 纯函数（Task 7） |
| `web/src/app/projects/register.test.ts` | Task 5/7 的编排与纯函数测试 |
| `web/src/app/projects/AddProjectWizard.test.tsx` | Task 4/5/7 的 UI 规则测试 |

---

### Task 1: 后端 —— `path` 必须是绝对路径（阻塞项 #1）

**问题**：`registerAtPath` 用 `os.Stat(req.Path)` 原样判存在，`cloneToPathAndRegister` 用 `dest := req.Path` 原样交给 `gitRun(ctx, parent, "clone", "--", origin, dest)`——git 以 `parent` 为 cwd 再解析一次相对 dest；而 `persistProject` 是相对 **agentd 的 cwd** 做 `filepath.Abs`。两边基准不同，结果是仓库克隆到 `<cwd>/workdir/workdir/proj`，位置表却落 `<cwd>/workdir/proj`（不存在），且返回 `nil` error。`~/code/x` 更糟：`MkdirAll("~/code")` 会在 agentd 的 cwd 里造一个字面量 `~` 目录。

**Files:**
- Modify: `internal/agentd/projectadmin.go`（`RegisterProject` 入口，trim 之后、分派之前）
- Test: `internal/agentd/projectadmin_test.go`

**Interfaces:**
- Consumes: 既有 `errBadDispatchRequest`、`m.log`
- Produces: `RegisterProject` 对 `Path` 非空且非绝对路径的请求一律返回包裹 `errBadDispatchRequest` 的错误（HTTP 400）。后续 task 可以假定进入 `registerAtPath` 的 `req.Path` 一定是 `filepath.Clean` 过的绝对路径。

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/projectadmin_test.go`：

```go
// TestRegisterProjectRejectsRelativePath 验证相对 path 被入口拦下。
//
// 为什么必须拦而不是 filepath.Abs 兜底：Abs 的基准是 agentd 进程的 cwd，
// 调用方（尤其 Web 表单和跨机那一跳）根本不知道那是哪个目录；更要命的是
// gitRun 以 dest 的父目录为 cwd，相对 dest 会被 git 再解析一次，克隆落点
// 与落库路径就此分叉，留下一条指向不存在路径的死记录。
func TestRegisterProjectRejectsRelativePath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "relproj", Path: "workdir/relproj",
	})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
	if !strings.Contains(err.Error(), "绝对路径") {
		t.Errorf("报文 = %q, want 含「绝对路径」（人要看得懂怎么改）", err.Error())
	}
}

// TestRegisterProjectRejectsTildePath 验证 ~ 开头的 path 被拦下。
// Go 不做 ~ 展开——不拦的话 MkdirAll 会造出一个字面量 ~ 目录。
func TestRegisterProjectRejectsTildePath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "tildeproj", Path: "~/code/tildeproj",
	})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
	if _, serr := os.Stat("~"); serr == nil {
		t.Errorf("cwd 下出现了字面量 ~ 目录——说明请求走到了 MkdirAll")
	}
}

// TestRegisterProjectNormalizesDirtyAbsPath 验证绝对路径里的冗余段被 Clean 掉，
// 落库的是归一化后的路径（同一目录不会因为写法不同登记成两条）。
func TestRegisterProjectNormalizesDirtyAbsPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		Path: filepath.Join(repo, "sub", ".."),
	})
	if err != nil {
		t.Fatalf("RegisterProject(含 .. 的绝对路径): %v", err)
	}
	if loc.OriginURL != origin {
		t.Errorf("OriginURL = %q, want %q", loc.OriginURL, origin)
	}
}
```

如果 `projectadmin_test.go` 顶部还没 import `strings`，补上。

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
go test ./internal/agentd/ -run 'TestRegisterProjectRejectsRelativePath|TestRegisterProjectRejectsTildePath|TestRegisterProjectNormalizesDirtyAbsPath' -count=1
```

Expected: `RejectsRelativePath` 与 `RejectsTildePath` FAIL（当前会走到 clone 并返回 nil error）；`NormalizesDirtyAbsPath` 大概率已 PASS（`inspectRepoDir` 现读 root）。

- [ ] **Step 3: 在 `RegisterProject` 入口加路径形态闸门**

`internal/agentd/projectadmin.go`，把三个 `TrimSpace` 之后、`if req.Path != ""` 分派之前的部分改成：

```go
	req.OriginURL = strings.TrimSpace(req.OriginURL)
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)
	if req.OriginURL != "" && strings.HasPrefix(req.OriginURL, "-") {
		// git 会把以 - 开头的参数解释为选项——参数注入面，与 ErrBadBaseBranch 同源。
		return proto.ProjectLocation{}, fmt.Errorf("%w: origin_url 不允许以 - 开头", errBadDispatchRequest)
	}
	if req.Path != "" {
		// path 必须是绝对路径：clone 落点由 gitRun（cwd=父目录）解析，落库路径由
		// persistProject（cwd=agentd 进程）解析，两边基准不同——相对路径会让仓库
		// 克隆到一处、位置表记到另一处，留下一条指向不存在路径的死记录。
		// ~ 同理：Go 不展开它，不拦就会在 agentd 的 cwd 里造出字面量 ~ 目录。
		// 不用 filepath.Abs 兜底：调用方不知道 agentd 的 cwd 是哪儿，猜一个
		// "能算出来的路径"不等于猜对了用户要的路径。
		if !filepath.IsAbs(req.Path) {
			m.log.Warn("登记被拒：path 不是绝对路径", "path", req.Path)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: path 必须是绝对路径（不支持 ~ 展开与相对路径）：%s",
				errBadDispatchRequest, req.Path)
		}
		req.Path = filepath.Clean(req.Path)
		return m.registerAtPath(ctx, req)
	}
```

- [ ] **Step 4: 同步文档注释**

`RegisterProjectReq` 的决策表注释里，`Path` 字段那几行补一条约束（放在三态决策表之后）：

```go
// Path 的形态约束：必须是**绝对路径**。相对路径与 ~ 一律 400——clone 落点与
// 落库路径的解析基准不同，猜错的代价是一条指向不存在路径的死记录。
```

`RegisterProject` 的「注意：」块补一条：

```go
//   - Path 非空时必须是绝对路径，否则 400（见 RegisterProjectReq）
```

- [ ] **Step 5: 加日志（instrumenting-code）**

- 入口已有 `m.log.Info("登记项目请求", ...)`，保持。
- 新增拒绝分支必须 `m.log.Warn("登记被拒：path 不是绝对路径", "path", req.Path)`（Step 3 的代码块里已含）。
- 确认没有引入任何 `fmt.Printf`。

- [ ] **Step 6: 跑测试确认通过**

Run:

```bash
go test ./internal/agentd/ -run 'TestRegisterProject' -count=1
```

Expected: 全部 PASS（含既有用例）。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/projectadmin.go internal/agentd/projectadmin_test.go
git commit -m "$(cat <<'EOF'
fix(agentd): path 必须是绝对路径，相对路径与 ~ 一律 400

clone 落点由 gitRun（cwd=父目录）解析、落库路径由 persistProject
（cwd=agentd 进程）解析，基准不同——相对 path 会把仓库克隆到一处、
把位置记到另一处，返回 200 却留下死记录。~ 不展开同理。
EOF
)"
```

---

### Task 2: 后端 —— clone-to-path 的幂等短路不再吞掉调用方指定的 path（评审项 #3）

**问题**：`cloneToPathAndRegister` 里，只要该 `project_id` 已登记就无条件返回已有行。用户在 Web 上填了 `/new/path`，拿回的是 `/old/path` 的成功结果，页面显示「已登记」，而他填的路径从没被用过。同一个用户意图走「路径已存在」分支（`registerExistingProject`）时给的是 409 + 「项目 X 在本机已登记于 Y」——两个分支对同一件事给了两种答复。

**Files:**
- Modify: `internal/agentd/projectadmin.go`（`cloneToPathAndRegister` 的幂等短路块）
- Test: `internal/agentd/projectadmin_test.go`

**Interfaces:**
- Consumes: Task 1 保证的「`req.Path` 是绝对路径」；既有 `sameLocation(a, b string) bool`、`ErrProjectAlreadyExists`
- Produces: 无新导出符号；行为契约变为「同项目 + 同落点 → 幂等 200；同项目 + 异落点 → 409」

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/projectadmin_test.go`：

```go
// TestRegisterProjectCloneToPathRejectsDifferentLocation 验证同一项目已登记在
// 别处时，clone-to-path 报 409 并指向已有位置——而不是静默返回别处那一行、
// 让调用方以为自己填的落点生效了。
func TestRegisterProjectCloneToPathRejectsDifferentLocation(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)

	first, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: filepath.Join(t.TempDir(), "first"),
	})
	if err != nil {
		t.Fatalf("首次 clone-to-path: %v", err)
	}

	other := filepath.Join(t.TempDir(), "second")
	_, err = m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: other,
	})
	if !errors.Is(err, ErrProjectAlreadyExists) {
		t.Fatalf("err = %v, want ErrProjectAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), first.Path) {
		t.Errorf("报文 = %q, want 含已有位置 %q", err.Error(), first.Path)
	}
	if _, serr := os.Stat(other); serr == nil {
		t.Errorf("被拒的请求不该在 %s 上留下任何东西", other)
	}
}

// TestRegisterProjectCloneToPathIdempotentSameLocation 验证同项目 + 同落点仍幂等：
// 落点被人手动 rm 掉、位置表还留着那一行时，重复登记不该被 409 打断
//（自动登记链靠这条不断）。
func TestRegisterProjectCloneToPathIdempotentSameLocation(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)
	dest := filepath.Join(t.TempDir(), "proj")

	first, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: dest,
	})
	if err != nil {
		t.Fatalf("首次 clone-to-path: %v", err)
	}
	if err := os.RemoveAll(dest); err != nil {
		t.Fatalf("清掉磁盘上的克隆: %v", err)
	}

	again, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: dest,
	})
	if err != nil {
		t.Fatalf("同落点重复登记应幂等: %v", err)
	}
	if again.Path != first.Path {
		t.Errorf("Path = %q, want %q", again.Path, first.Path)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
go test ./internal/agentd/ -run 'TestRegisterProjectCloneToPath' -count=1
```

Expected: `RejectsDifferentLocation` FAIL（当前返回 nil error 与 first 那一行）；`IdempotentSameLocation` PASS。

- [ ] **Step 3: 收紧 `cloneToPathAndRegister` 的幂等短路**

把 `cloneToPathAndRegister` 开头那段幂等短路改成：

```go
	// 幂等短路：必须发生在 clone 之前——重复登记同一个项目不应再 clone 出第二份。
	// 但只有**同落点**才算"重复声明同一个事实"：调用方明确指了一个新落点时，
	// 静默返回旧位置等于把他填的 path 吞了。异落点报 409，与「路径已存在」分支
	// （registerExistingProject）给出同一种答复（ADR-0008：一台机器一个项目一个位置）。
	pid := projectid.FromOrigin(req.OriginURL)
	if pid != "" {
		existing, ok, err := m.registeredProjectByID(pid)
		if err != nil {
			return proto.ProjectLocation{}, err
		}
		if ok {
			if sameLocation(existing.Path, req.Path) {
				m.log.Info("项目位置已存在且落点相同，幂等返回",
					"project_id", existing.ProjectID, "name", existing.Name, "path", existing.Path)
				existing.Status = projectStatusOK
				return existing, nil
			}
			m.log.Warn("克隆登记被拒：该项目在本机已有位置",
				"project_id", pid, "existing", existing.Path, "requested", req.Path)
			return proto.ProjectLocation{}, fmt.Errorf(
				"%w: 项目 %s 在本机已登记于 %s；要换位置先 handoff project rm %s",
				ErrProjectAlreadyExists, existing.Name, existing.Path, existing.Name)
		}
	}
```

- [ ] **Step 4: 同步 `cloneToPathAndRegister` 的文档注释**

在该函数的文档注释末尾补一段：

```go
// 幂等边界：同项目 + 同落点 → 返回已有行（磁盘被 rm 掉、位置表还在时，重复登记
// 不该把自动登记链打断）；同项目 + 异落点 → ErrProjectAlreadyExists，报文指向
// 已有位置。绝不静默返回一个与请求 path 不同的位置。
```

- [ ] **Step 5: 加日志**

Step 3 的代码块已含幂等 Info 与拒绝 Warn（都带 `project_id` / `existing` / `requested`）。确认没有其它静默 return。

- [ ] **Step 6: 跑测试确认通过**

Run:

```bash
go test ./internal/agentd/ -run 'TestRegisterProject' -count=1
```

Expected: 全部 PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/projectadmin.go internal/agentd/projectadmin_test.go
git commit -m "$(cat <<'EOF'
fix(agentd): clone-to-path 异落点报 409，不再静默返回旧位置

同项目 + 同落点仍幂等；调用方明确指了新落点时静默返回旧行
等于把他填的 path 吞了，与「路径已存在」分支的答复也不一致。
EOF
)"
```

---

### Task 3: CLI —— `--path` 文案同步到新语义（评审项 #2）

**问题**：后端把「path 不存在 → clone 到该 path」加进决策表后，`handoff project add --target devbox --path /root/x` 的语义跟着变宽了：`/root/wrok/x`（打错字）以前会 400 报「不是 git 仓库」，现在会在 devbox 上克隆一份新的然后登记成功。上一轮的 Task 3 同步了 `server.go` 与 `client.go` 的注释，漏了 `cmd/project.go`。**决策已锁定：保留新语义，只修文案。**

**Files:**
- Modify: `cmd/project.go`（`projectAddPath` 变量注释、`projectAddCmd` 用法示例、`registerProjectBothHops` 参数注释、`init()` 里的 flag help）

**Interfaces:**
- Consumes: 无
- Produces: 无（纯文案；无行为改动）

**本 task 不写测试**：改动只有注释与 cobra flag 的 usage 字符串，没有可断言的行为。对 help 文案做字符串断言是脆而无价值的测试。验证靠 Step 4 的 `--help` 目视核对。

- [ ] **Step 1: 改 `projectAddPath` 变量注释**

`cmd/project.go` 第 32 行附近：

```go
// projectAddPath 是 --path：目标机上这份代码的落点。
//
// 三态（与 agentd 的决策表一致）：路径已存在 → 登记它；路径不存在 → 由那台
// 机器 clone 到该路径；省略 → 由那台机器 clone 到它自己的 repo_root/<名字>。
// 注意「路径不存在就 clone」意味着 --path 打错字不会被拦下，而是会在错的
// 路径上克隆出一份新的——路径要自己核对。
var projectAddPath string
```

- [ ] **Step 2: 改 `projectAddCmd` 的用法示例**

第 83 行附近，把第三条示例与紧随其后的示例块改成：

```go
//	handoff project add [名字]                                       # 把 cwd 登记为本机位置
//	handoff project add [名字] --target devbox                       # 本机与 devbox 一起登记，devbox clone 到自己的 repo_root
//	handoff project add [名字] --target devbox --path /root/work/x   # 同上，但落点指定为 /root/work/x（已有就登记它，没有就 clone 过去）
```

- [ ] **Step 3: 改 `registerProjectBothHops` 的参数注释与 flag help**

参数注释（第 112 行附近）：

```go
//   - remotePath: 目标机上的落点（可空，空则让那台机器 clone 到自己的 repo_root/<名字>；
//     非空时已存在就登记它、不存在就 clone 到它）
```

`init()` 里的 flag help（第 302 行附近）：

```go
	projectAddCmd.Flags().StringVar(&projectAddPath, "path", "",
		"目标机上的落点（仅与 --target 连用）：已存在则登记它，不存在则 clone 到它；省略则 clone 到那台机器的 repo_root/<名字>")
```

- [ ] **Step 4: 编译并目视核对 help**

Run:

```bash
go build ./... && go run . project add --help
```

Expected: 编译通过；`--path` 那一行显示新文案，不再出现「已有的那份代码」。

- [ ] **Step 5: Commit**

```bash
git add cmd/project.go
git commit -m "docs(cli): --path 文案同步到三态语义（不存在则 clone 到该路径）"
```

---

### Task 4: 前端 —— 远程重试带上表单里的 name（评审项 #4）

**问题**：`AddProjectWizard.tsx` 的 `retry()` 在「本机也失败、退到表单 gitUrl」这条分支上组的 choice 是 `{ machine, originUrl: gitUrl, path: remotePath }`——没带 `name`。用户填了「demo」，远程会被登记成 origin 末段派生的名字。而本机重试那条分支是带 `name` 的，两边不一致。现有测试还把这个遗漏锁进了断言。

**Files:**
- Modify: `web/src/app/projects/AddProjectWizard.tsx`（`retry` 函数的 else 分支）
- Test: `web/src/app/projects/AddProjectWizard.test.tsx`（改现有用例「本机失败但填了 gitUrl 时，远程可用表单 gitUrl 单独重试」）

**Interfaces:**
- Consumes: 既有 `LocationChoice { machine, originUrl?, name?, path? }`、`registerAll`
- Produces: 无新符号

- [ ] **Step 1: 改测试为失败态**

`web/src/app/projects/AddProjectWizard.test.tsx`，把用例「本机失败但填了 gitUrl 时，远程可用表单 gitUrl 单独重试」整体替换为：

```tsx
  it('本机失败但填了 gitUrl 时，远程可用表单 gitUrl + name 单独重试', async () => {
    // 本 task 里编排还只回一条本机结果，「未尝试」那行仍由组件现编（Task 5 才下沉）
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: false, error: '路径不存在' },
    ])
    vi.mocked(register.registerAll).mockResolvedValue([
      { machine: 'devbox', ok: true, error: '', result: localOk() },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fireEvent.change(screen.getByPlaceholderText(/可选.*仓库名/), { target: { value: 'demo' } })
    fillLocalPath('/nope')
    fireEvent.change(screen.getByPlaceholderText(/Git/), { target: { value: 'git@x:h.git' } })
    enableRemote('devbox')
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/未尝试/)).toBeInTheDocument())
    const retries = screen.getAllByRole('button', { name: '重试' })
    fireEvent.click(retries[1])
    await waitFor(() =>
      expect(register.registerAll).toHaveBeenCalledWith([
        // 表单里填了 name 就必须带上——否则远程会按 origin 末段自己派生一个，
        // 与本机成功时用权威 name 的行为不一致
        { machine: 'devbox', originUrl: 'git@x:h.git', name: 'demo', path: '' },
      ]),
    )
  })
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd web && npx vitest run src/app/projects/AddProjectWizard.test.tsx
```

Expected: FAIL——实际调用是 `{ machine: 'devbox', originUrl: 'git@x:h.git', path: '' }`，少一个 `name: 'demo'`。

- [ ] **Step 3: 改 `retry` 的 else 分支**

`web/src/app/projects/AddProjectWizard.tsx`：

```tsx
      // 远程重试：优先本机成功结果里的权威 origin/name；本机也失败时退到表单
      // gitUrl + name（调用方保证 canRetryRemote，即此时 gitUrl 非空）。
      // name 必须一起退，否则远程会按 origin 末段自己派生一个，跟用户在表单里
      // 填的名字对不上——两台机器上同一个项目叫两个名字是最难查的那类问题。
      const local = (outcomes ?? []).find((o) => o.machine === '')
      if (local?.ok && local.result) {
        choice = { machine, originUrl: local.result.origin_url, name: local.result.name, path: remotePath }
      } else {
        choice = { machine, originUrl: gitUrl, name, path: remotePath }
      }
```

- [ ] **Step 4: 跑测试确认通过**

Run:

```bash
cd web && npx vitest run src/app/projects/
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/app/projects/AddProjectWizard.tsx web/src/app/projects/AddProjectWizard.test.tsx
git commit -m "fix(web): 远程重试退到表单时一并带上 name

否则远程按 origin 末段自己派生名字，与本机成功时用权威 name 的
行为不一致，同一个项目在两台机器上叫两个名字。"
```

---

### Task 5: 前端 —— 「远程未尝试」下沉到编排，文案按当前状态渲染（评审项 #5 + #6）

**问题（两个，同源）**：
- #6：`submit()` 在组件里现编那一行「本机登记失败，未尝试远程」——`registerFromForm` 本身就知道 `remoteMachine`，编排知识被拆成了两份。
- #5：那行文案是在提交时**烤死**的字符串。本机重试成功后，重试按钮已解禁，那行却还写着「本机登记失败，未尝试远程」。

**一起修**：给 `RegisterOutcome` 加一个显式的 `skipped` 标记，由 `registerFromForm` 产出；结果页看到 `skipped` 时**按当前状态**算文案，不读烤死的串。

**Files:**
- Modify: `web/src/app/projects/register.ts`（`RegisterOutcome` 加字段、`registerFromForm` 补行）
- Modify: `web/src/app/projects/AddProjectWizard.tsx`（`submit` 去掉现编逻辑、结果页渲染分支）
- Test: `web/src/app/projects/register.test.ts`、`web/src/app/projects/AddProjectWizard.test.tsx`

**Interfaces:**
- Consumes: Task 4 之后的 `retry`
- Produces:
  - `RegisterOutcome` 新增可选字段 `skipped?: boolean` —— `true` 表示「这个位置本次根本没发起请求」，与 `ok: false`（发起了但失败）区分开
  - `registerFromForm(input)` 在「本机失败 + `input.remoteMachine` 非 null」时返回 **2 条**：本机失败行 + `{ machine: input.remoteMachine, ok: false, error: '', skipped: true }`

- [ ] **Step 1: 写失败的测试（编排层）**

`web/src/app/projects/register.test.ts`，把用例「本机失败时不请求远程，只回一条本机结果且透传 agentd 原文」替换为：

```ts
  it('本机失败时不请求远程，但补一条 skipped 的远程行（让用户看到它没被漏掉）', async () => {
    const spy = vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, '路径不存在'))
    const out = await registerFromForm({
      name: '',
      localPath: '/nope',
      gitUrl: '',
      remoteMachine: 'devbox',
      remotePath: '',
    })
    expect(spy).toHaveBeenCalledTimes(1)
    expect(out).toHaveLength(2)
    expect(out[0]).toMatchObject({ machine: '', ok: false })
    expect(out[0].error).toContain('路径不存在')
    // skipped 与 ok:false 是两回事：一个是没发起，一个是发起了但失败。
    // 结果页要靠这个区分才能算出正确的文案。
    expect(out[1]).toMatchObject({ machine: 'devbox', ok: false, skipped: true })
  })

  it('没勾远程时本机失败只回一条，不无中生有一行 skipped', async () => {
    vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, '路径不存在'))
    const out = await registerFromForm({
      name: '',
      localPath: '/nope',
      gitUrl: '',
      remoteMachine: null,
      remotePath: '',
    })
    expect(out).toHaveLength(1)
  })
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd web && npx vitest run src/app/projects/register.test.ts
```

Expected: 第一个用例 FAIL（`out` 只有 1 条）。

- [ ] **Step 3: 改 `register.ts`**

字段：

```ts
// RegisterOutcome 是单个位置的登记结果；error 透传 agentd 原文（带解法）。
// skipped=true 表示这个位置本次**根本没发起请求**（本机失败时编排不打远程），
// 与 ok=false（发起了但被拒）是两回事——结果页要靠它算出正确的文案与可重试性。
export interface RegisterOutcome {
  machine: string
  ok: boolean
  error: string
  result?: CreateProjectResp
  skipped?: boolean
}
```

编排：

```ts
export async function registerFromForm(input: RegisterFormInput): Promise<RegisterOutcome[]> {
  const local = await settleOne({
    machine: '',
    originUrl: input.gitUrl,
    name: input.name,
    path: input.localPath,
  })
  const outcomes: RegisterOutcome[] = [local]
  if (!input.remoteMachine) return outcomes
  if (!local.ok) {
    // 本机失败就不打远程（远程要用本机响应里的权威 origin/name）。但仍然回一行，
    // 否则用户在结果页看不到远程，会以为自己勾的那台机器被漏掉了。文案由结果页
    // 按当时的状态算——本机随后重试成功的话，这行的含义就变了。
    outcomes.push({ machine: input.remoteMachine, ok: false, error: '', skipped: true })
    return outcomes
  }

  const remote = await settleOne({
    machine: input.remoteMachine,
    originUrl: local.result!.origin_url,
    name: local.result!.name,
    path: input.remotePath,
  })
  outcomes.push(remote)
  return outcomes
}
```

- [ ] **Step 4: 跑编排测试确认通过**

Run:

```bash
cd web && npx vitest run src/app/projects/register.test.ts
```

Expected: PASS。

- [ ] **Step 5: 写失败的 UI 测试**

`web/src/app/projects/AddProjectWizard.test.tsx`，把用例「本机失败时远程行标为未尝试；gitUrl 为空则远程重试禁用并提示」替换为下面两个：

```tsx
  it('本机失败时远程行标为未尝试；gitUrl 为空则远程重试禁用并提示', async () => {
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: false, error: '路径不存在' },
      { machine: 'devbox', ok: false, error: '', skipped: true },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath('/nope')
    enableRemote('devbox')
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/未尝试：本机登记失败/)).toBeInTheDocument())
    expect(screen.getByText(/先修好本机.*Git 地址/)).toBeInTheDocument()
    const retries = screen.getAllByRole('button', { name: '重试' })
    expect(retries).toHaveLength(2)
    expect(retries[0]).toBeEnabled()
    expect(retries[1]).toBeDisabled()
  })

  it('本机重试成功后，远程「未尝试」行的文案跟着变，不再说本机失败', async () => {
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: false, error: '路径不存在' },
      { machine: 'devbox', ok: false, error: '', skipped: true },
    ])
    vi.mocked(register.registerAll).mockResolvedValue([
      { machine: '', ok: true, error: '', result: localOk() },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath('/Users/me/h')
    enableRemote('devbox')
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/未尝试：本机登记失败/)).toBeInTheDocument())

    // 重试本机并成功
    fireEvent.click(screen.getAllByRole('button', { name: '重试' })[0])
    await waitFor(() => expect(screen.getByText('已登记')).toBeInTheDocument())
    // 远程那行的含义已经变了：本机好了，现在只差点一下远程
    expect(screen.getByText(/未尝试：本机已登记/)).toBeInTheDocument()
    expect(screen.queryByText(/未尝试：本机登记失败/)).toBeNull()
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled()
  })
```

- [ ] **Step 6: 跑测试确认失败**

Run:

```bash
cd web && npx vitest run src/app/projects/AddProjectWizard.test.tsx
```

Expected: 两个用例都 FAIL（当前渲染的是烤死的「本机登记失败，未尝试远程」）。

- [ ] **Step 7: 改 `AddProjectWizard.tsx`**

`submit` 去掉现编逻辑：

```tsx
  const submit = async () => {
    setSubmitting(true)
    // 「本机失败 → 远程未尝试」那一行由 registerFromForm 产出（编排知识只有一份）。
    const result = await registerFromForm({
      name,
      localPath,
      gitUrl,
      remoteMachine: remoteEnabled ? remoteMachine : null,
      remotePath,
    })
    setOutcomes(result)
    setSubmitting(false)
    setView('results')
    if (result.some((o) => o.ok)) onDone()
  }
```

结果页的行渲染改成（替换 `outcomes.map` 那个回调体）：

```tsx
            {outcomes.map((o) => {
              const retryDisabled = o.machine !== '' && !canRetryRemote
              // skipped 行的文案按**当前**状态算，不用提交那一刻烤死的串：
              // 本机随后重试成功时，"未尝试"的原因就从"本机失败"变成"只差点一下"。
              const message = o.skipped
                ? localSucceeded
                  ? '未尝试：本机已登记，点重试即可登记远程'
                  : '未尝试：本机登记失败'
                : o.error
              return (
                <div key={o.machine} className="flex items-center gap-2 rounded-md border p-3 text-sm">
                  <span className="font-medium">{machineLabel(o.machine)}</span>
                  {o.ok ? (
                    <span className="text-emerald-600">已登记</span>
                  ) : (
                    <>
                      <span className="min-w-0 flex-1 break-words text-destructive">
                        {message}
                        {retryDisabled && (
                          <span className="block text-[11px] text-muted-foreground">
                            先修好本机，或在本机区块填 Git 地址后再重试远程
                          </span>
                        )}
                      </span>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={retryDisabled}
                        onClick={() => void retry(o.machine)}
                      >
                        重试
                      </Button>
                    </>
                  )}
                </div>
              )
            })}
```

同时把组件顶部的文件头注释里「提交：」那一段更新为：

```tsx
// 提交：registerFromForm（本机优先编排，含「本机失败 → 远程未尝试」那一行）；任一
// 成功即调 onDone 让父级 refresh 项目树；结果面板逐位置显示，失败的可「重试」。远程
// 重试用本机成功结果的 origin/name；本机也失败时仅当表单填了 Git 地址才允许远程单独
// 重试，否则禁用并提示先修本机。
```

- [ ] **Step 8: 跑测试确认通过**

Run:

```bash
cd web && npx vitest run src/app/projects/
```

Expected: PASS。**注意**：Task 4 改过的那个用例（「本机失败但填了 gitUrl 时…」）现在必须把 `registerFromForm` 的 mock 补成两条——加上 `{ machine: 'devbox', ok: false, error: '', skipped: true }`，否则组件拿不到远程行、`retries[1]` 会取不到。

- [ ] **Step 9: Commit**

```bash
git add web/src/app/projects/register.ts web/src/app/projects/register.test.ts \
        web/src/app/projects/AddProjectWizard.tsx web/src/app/projects/AddProjectWizard.test.tsx
git commit -m "$(cat <<'EOF'
refactor(web): 「远程未尝试」下沉到 registerFromForm，文案按当前状态渲染

编排知识不再拆成组件与编排两份；本机重试成功后远程行的文案跟着
变，不再停在「本机登记失败」这句烤死的串。
EOF
)"
```

---

### Task 6: 后端 —— clone 失败时回收本次新建的目录（评审项 #7）

**问题**：`cloneToPathAndRegister` 和 `cloneAndRegisterProject` 都会 `os.MkdirAll(parent, 0o755)`，clone 失败后这些目录留在磁盘上。反复重试一个打错的 URL 会在文件系统里堆一串空目录。

**只回收本次自己造的那些**——调用方原本就有的目录一根汗毛都不能动。

**Files:**
- Modify: `internal/agentd/projectadmin.go`（新增 `firstMissingAncestor` 助手；`cloneToPathAndRegister` 与 `cloneAndRegisterProject` 的失败分支各接一次回收）
- Test: `internal/agentd/projectadmin_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `func firstMissingAncestor(dir string) string` —— 返回从 `dir` 往上第一个尚不存在的祖先目录（即 `MkdirAll` 会从哪一层开始造）；`dir` 本身已存在时返回 `""`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/projectadmin_test.go`：

```go
// TestFirstMissingAncestor 验证助手找的是「MkdirAll 会从哪一层开始造」。
func TestFirstMissingAncestor(t *testing.T) {
	base := t.TempDir()
	if got := firstMissingAncestor(base); got != "" {
		t.Errorf("已存在的目录应返回空串，got %q", got)
	}
	want := filepath.Join(base, "a")
	if got := firstMissingAncestor(filepath.Join(base, "a", "b", "c")); got != want {
		t.Errorf("firstMissingAncestor = %q, want %q", got, want)
	}
}

// TestCloneToPathCleansUpOnFailure 验证 clone 失败时本次新建的目录被回收，
// 而调用方原本就有的目录一根汗毛不动。
func TestCloneToPathCleansUpOnFailure(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	base := t.TempDir()
	// 不是仓库的目录当 origin：git clone 必失败，且不依赖网络。
	bogus := filepath.Join(t.TempDir(), "not-a-repo")
	if err := os.MkdirAll(bogus, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: bogus, Name: "proj", Path: filepath.Join(base, "a", "b", "proj"),
	})
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want ErrRepoUnusable", err)
	}
	if _, serr := os.Stat(filepath.Join(base, "a")); serr == nil {
		t.Errorf("clone 失败后 %s 不该留下", filepath.Join(base, "a"))
	}
	if _, serr := os.Stat(base); serr != nil {
		t.Errorf("调用方原本就有的 %s 被误删了: %v", base, serr)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
go test ./internal/agentd/ -run 'TestFirstMissingAncestor|TestCloneToPathCleansUpOnFailure' -count=1
```

Expected: `TestFirstMissingAncestor` 编译失败（函数不存在）；补上函数后 `CleansUpOnFailure` FAIL（`<base>/a` 还在）。

- [ ] **Step 3: 加 `firstMissingAncestor` 助手**

放在 `cloneToPathAndRegister` 上方：

```go
// firstMissingAncestor 返回从 dir 往上第一个「尚不存在」的祖先目录——也就是
// os.MkdirAll(dir) 会从哪一层开始真正创建目录。
//
// 参数：
//   - dir: 待创建的目录（绝对路径）
//
// 返回：
//   - 第一个不存在的祖先的绝对路径；dir 本身已存在时返回空串
//
// 为什么需要它：clone 失败要回收「本次自己造的」目录，而 MkdirAll 可能一次造好
// 几层。只删最后那一层会留下中间空目录；从根上 RemoveAll 又会删掉调用方原本就有
// 的目录。这个函数给出的正是那条分界线。
func firstMissingAncestor(dir string) string {
	missing := ""
	for p := dir; ; {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = p
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return missing
}
```

- [ ] **Step 4: 在两处 clone 失败分支接上回收**

`cloneToPathAndRegister` 里，把 `parent := filepath.Dir(dest)` 到 clone 失败 return 那一段改成：

```go
	dest := req.Path
	parent := filepath.Dir(dest)
	// clone 前先记下 MkdirAll 会从哪一层开始造——失败时只回收这一层往下，
	// 调用方原本就有的目录绝不碰。
	created := firstMissingAncestor(parent)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("%w: 创建落点父目录 %s: %v", ErrRepoUnusable, parent, err)
	}
	m.log.Info("开始克隆项目到指定路径", "origin", req.OriginURL, "dest", dest)
	start := time.Now()
	// gitRun 以 parent 为 cwd 执行；-- 分隔符防止 URL/路径被当成选项。
	if _, stderr, err := gitRun(ctx, parent, "clone", "--", req.OriginURL, dest); err != nil {
		m.log.Error("克隆到指定路径失败", "origin", req.OriginURL, "dest", dest,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		m.cleanupCreatedDir(created)
		return proto.ProjectLocation{}, fmt.Errorf("%w: 克隆 %s 到 %s 失败: %s: %v",
			ErrRepoUnusable, req.OriginURL, dest, strings.TrimSpace(stderr), err)
	}
```

`cloneAndRegisterProject` 里做同样的两处改动（`created := firstMissingAncestor(parent)` 紧跟在 `parent := filepath.Dir(dest)` 之后；失败分支的 `return` 之前加 `m.cleanupCreatedDir(created)`）。

再加回收助手（放在 `firstMissingAncestor` 下方）：

```go
// cleanupCreatedDir 回收 clone 失败后本次新建的目录树。
//
// 参数：
//   - created: firstMissingAncestor 的返回值；空串表示本次没造过任何目录，直接返回
//
// 注意：回收失败**不改变**调用方看到的错误——clone 的失败原因才是人要看的那条，
// 残留目录只是需要人工清理的次要事实，写进 Warn 日志即可。
func (m *Manager) cleanupCreatedDir(created string) {
	if created == "" {
		return
	}
	if err := os.RemoveAll(created); err != nil {
		m.log.Warn("克隆失败后回收目录失败，需人工清理", "dir", created, "cause", err)
		return
	}
	m.log.Info("克隆失败，已回收本次新建的目录", "dir", created)
}
```

- [ ] **Step 5: 跑测试确认通过**

Run:

```bash
go test ./internal/agentd/ -count=1
```

Expected: 全部 PASS（含既有的 `TestRegisterProjectClones*`——它们的落点父目录来自 `t.TempDir()`，成功路径不触发回收）。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/projectadmin.go internal/agentd/projectadmin_test.go
git commit -m "$(cat <<'EOF'
fix(agentd): clone 失败时回收本次新建的目录

firstMissingAncestor 给出「MkdirAll 从哪一层开始造」这条分界线，
只回收这一层往下；调用方原本就有的目录不碰。回收失败只 Warn，
不遮盖 clone 本身的失败原因。
EOF
)"
```

---

### Task 7: 前端 —— path 形态提示与提交闸门（配合 Task 1）

**问题**：Task 1 之后后端会 400 掉相对路径与 `~`，但 Web 表单毫无提示，用户要往返一次才知道。**权威判定仍在 agentd**——前端这层只是省一次往返，不是第二套规则。

**Files:**
- Modify: `web/src/app/projects/register.ts`（新增 `absPathHint` 纯函数）
- Modify: `web/src/app/projects/AddProjectWizard.tsx`（placeholder、提示行、`canSubmit`）
- Test: `web/src/app/projects/register.test.ts`、`web/src/app/projects/AddProjectWizard.test.tsx`

**Interfaces:**
- Consumes: 无
- Produces: `export function absPathHint(path: string): string` —— 路径形态没问题时返回 `''`；否则返回给人看的中文提示。空串输入返回 `''`（"必填"由 `canSubmit` 另行管，不在这里重复）

- [ ] **Step 1: 写失败的测试（纯函数）**

追加到 `web/src/app/projects/register.test.ts`：

```ts
describe('absPathHint', () => {
  it('绝对路径没有提示', () => {
    expect(absPathHint('/Users/me/handoff')).toBe('')
  })

  it('空串没有提示——「必填」由提交按钮管，不在这里重复说一遍', () => {
    expect(absPathHint('')).toBe('')
    expect(absPathHint('   ')).toBe('')
  })

  it('~ 开头给出明确提示（agentd 不展开 ~）', () => {
    expect(absPathHint('~/code/handoff')).toContain('~')
  })

  it('相对路径给出明确提示', () => {
    expect(absPathHint('code/handoff')).toContain('绝对路径')
  })
})
```

在该文件顶部的 import 里加上 `absPathHint`。

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd web && npx vitest run src/app/projects/register.test.ts
```

Expected: FAIL（`absPathHint` 未导出）。

- [ ] **Step 3: 实现 `absPathHint`**

`web/src/app/projects/register.ts` 末尾：

```ts
// absPathHint 检查一个目录路径的形态，返回给人看的提示（没问题时返回空串）。
//
// 为什么前端也要查一遍：agentd 才是权威（它会 400），但让用户为一个一眼可见的
// 形态问题多走一次往返不值当。这里**只**查形态，不查存在性——路径存不存在、是不是
// 仓库，只有目标机的 agentd 知道，浏览器侧不猜。
//
// 空串返回空串：「必填」是提交按钮的职责，在这里重复说一遍只会让两处文案打架。
export function absPathHint(path: string): string {
  const p = path.trim()
  if (p === '') return ''
  if (p.startsWith('~')) return '不支持 ~：请写完整的绝对路径，如 /Users/you/code/handoff'
  if (!p.startsWith('/')) return '请填绝对路径（以 / 开头），如 /Users/you/code/handoff'
  return ''
}
```

- [ ] **Step 4: 跑纯函数测试确认通过**

Run:

```bash
cd web && npx vitest run src/app/projects/register.test.ts
```

Expected: PASS。

- [ ] **Step 5: 写失败的 UI 测试**

追加到 `web/src/app/projects/AddProjectWizard.test.tsx` 的 `describe` 内：

```tsx
  it('本机 path 写成 ~ 开头时提示并禁用提交', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath('~/code/h')
    expect(screen.getByText(/不支持 ~/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '提交' })).toBeDisabled()
  })

  it('远程 path 写成相对路径时提示并禁用提交', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath()
    enableRemote('devbox')
    fireEvent.change(screen.getByPlaceholderText(/留空由该机器 clone/), { target: { value: 'srv/h' } })
    expect(screen.getByText(/绝对路径/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '提交' })).toBeDisabled()
  })
```

- [ ] **Step 6: 跑测试确认失败**

Run:

```bash
cd web && npx vitest run src/app/projects/AddProjectWizard.test.tsx
```

Expected: 两个用例 FAIL。

- [ ] **Step 7: 改 `AddProjectWizard.tsx`**

import 里加上 `absPathHint`：

```tsx
import { absPathHint, registerAll, registerFromForm, type LocationChoice, type RegisterOutcome } from './register'
```

`canSubmit` 上方加两个提示，并把它们并进闸门：

```tsx
  // 形态提示（不是权威校验——agentd 才是）：省用户一次「填了 ~ 才发现不行」的往返。
  const localPathHint = absPathHint(localPath)
  const remotePathHint = remoteEnabled ? absPathHint(remotePath) : ''

  // path 必填且形态合法；gitUrl 不作要求（path 不存在且无 URL 的错误交给后端 400 原文）；
  // 勾了远程就必须选定一台机器。
  const canSubmit =
    localPath.trim() !== '' &&
    localPathHint === '' &&
    remotePathHint === '' &&
    (!remoteEnabled || remoteMachine !== null)
```

本机 path 输入框的 placeholder 与提示行：

```tsx
              <input
                value={localPath}
                onChange={(e) => setLocalPath(e.target.value)}
                placeholder="本机目录路径（必填，绝对路径，如 /Users/you/code/handoff）"
                className={inputClass}
              />
              {localPathHint && <p className="text-[11px] text-destructive">{localPathHint}</p>}
```

远程 path 输入框同理，在它下面插一行：

```tsx
                  {remotePathHint && <p className="text-[11px] text-destructive">{remotePathHint}</p>}
```

- [ ] **Step 8: 跑全部前端测试确认通过**

Run:

```bash
cd web && npx vitest run
```

Expected: PASS。注意既有用例 `fillLocalPath()` 的默认值是 `/Users/me/h`（绝对路径），不受影响。

- [ ] **Step 9: Commit**

```bash
git add web/src/app/projects/register.ts web/src/app/projects/register.test.ts \
        web/src/app/projects/AddProjectWizard.tsx web/src/app/projects/AddProjectWizard.test.tsx
git commit -m "feat(web): path 形态提示——~ 与相对路径当场拦下

只查形态不查存在性（存在性只有目标机 agentd 知道），省一次往返；
权威判定仍在 agentd 的 400。"
```

---

### Task 8: 全量验证与手工验收

**Files:** 无改动（只跑验证）

- [ ] **Step 1: Go 全量**

Run:

```bash
go build ./... && go test ./internal/agentd/ ./internal/client/ ./cmd/ -count=1
```

Expected: PASS

- [ ] **Step 2: 前端全量**

Run:

```bash
cd web && npx vitest run && npx tsc -b --noEmit && npx eslint .
```

Expected: vitest PASS；tsc 0 错误；eslint 0 error（6 个既有 warning 在本分支未改的文件里，不管）

- [ ] **Step 3: instrumenting-code 自检**

- [ ] 新增的每个错误分支都 `m.log.Warn`/`Error` 并带 path / origin / cause
- [ ] clone 失败回收成功与失败都有日志（Info / Warn）
- [ ] 成功路径没有变静默（既有的「克隆到指定路径完成」「幂等返回」都还在）
- [ ] 没有引入 `fmt.Printf` / `console.log` 作为日志手段
- [ ] `firstMissingAncestor`、`cleanupCreatedDir`、`absPathHint` 都有文档注释（含"为什么"）
- [ ] 新增的非显然分支都有中文「为什么」注释

- [ ] **Step 4: 手工验收（需要一个跑着的 agentd）**

| # | 操作 | 期望 |
|---|------|------|
| 1 | 本机 path 填 `~/code/x` | 表单当场提示「不支持 ~」，提交按钮禁用 |
| 2 | 绕过表单直接 `curl -XPOST /api/projects -d '{"path":"~/code/x","origin_url":"…"}'` | 400，报文含「绝对路径」；agentd 的 cwd 下**没有**出现字面量 `~` 目录 |
| 3 | 同上但 path 用 `workdir/x` | 400，报文含「绝对路径」 |
| 4 | 本机 path 不存在（绝对）+ 有效 URL | clone 到该 path 并登记；`project ls` 里的路径就是填的那个，且磁盘上确实是仓库 |
| 5 | 同一项目再登记到另一个不存在的绝对 path | 409，报文指向已有位置 |
| 6 | 同一项目、同一 path 再登记一次 | 幂等 200 |
| 7 | path 不存在 + 一个假的 git URL | 400；`<path>` 及本次新建的父目录都不残留 |
| 8 | 本机失败 + 勾了远程 | 结果页两行：本机报错行 + 「未尝试：本机登记失败」 |
| 9 | 接上条，修好本机 path 后点本机「重试」 | 本机变「已登记」；远程行文案变成「未尝试：本机已登记，点重试即可登记远程」，重试按钮解禁 |
| 10 | 表单填了 name「demo」+ 本机失败 + 填了 Git 地址 + 单独重试远程 | 远程登记名为 `demo`，不是 origin 末段 |
| 11 | `handoff project add --help` | `--path` 文案说的是三态语义 |

- [ ] **Step 5: 合并到 web-console**

REQUIRED SUB-SKILL: 用 `superpowers:finishing-a-development-branch` 收尾（它会提示本分支是否有原型改动要回流 `prototypes/base/`——本分支只动了真实前端，预期无回流）。

```bash
git checkout handoff/web-console
git merge --no-ff handoff/project-register-clean-flow
```

Expected: 干净合并（`web-console` 只比本分支多一个 docs 提交，已验证试合无冲突）

---

## Spec 覆盖自检

| 评审项 | 严重度 | Task |
|--------|--------|------|
| #1 clone-to-path 不归一化 path → 死记录 | 阻塞 | Task 1（+ Task 7 前端提示） |
| #2 CLI `--path` 语义变化 + 文案过期 | 重要 | Task 3 |
| #3 幂等短路静默忽略指定 path | 重要 | Task 2 |
| #4 远程重试丢了表单 name | 次要 | Task 4 |
| #5 重试后「未尝试」文案陈旧 | 次要 | Task 5 |
| #6 「未尝试」行在组件里现编 | 次要 | Task 5 |
| #7 clone 失败残留父目录 | 次要 | Task 6 |

## Placeholder 扫描

无 TBD/TODO 步骤。每个 code step 都给了可直接粘贴的代码；Task 3 是纯文案 task，已显式说明"不写测试"及理由，不是遗漏。

## 类型一致性

- Go 新增：`firstMissingAncestor(dir string) string`、`(m *Manager) cleanupCreatedDir(created string)`。两者在 Task 6 内定义与使用，无跨 task 引用。
- Go 既有签名不变：`RegisterProject` / `registerAtPath` / `cloneToPathAndRegister` / `registerExistingProject` / `persistProject` / `sameLocation` 一个都没改。
- TS 新增：`RegisterOutcome.skipped?: boolean`（Task 5 定义并首次使用；Task 4 完全不碰它）、`absPathHint(path: string): string`（Task 7 定义并使用）。
- TS 既有签名不变：`registerFromForm(input: RegisterFormInput)`、`registerAll(choices: LocationChoice[])`、`LocationChoice`、`RegisterFormInput`、`AddProjectWizardProps` 全部原样。
- HTTP JSON 字段名不变：`origin_url` / `name` / `path`。
