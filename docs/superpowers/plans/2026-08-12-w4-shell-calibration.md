# W4 外壳校准期 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 handoff Web 控制台的页面形态对齐 `prototypes/desktop-console/`——左栏四级导航树、中央三种 tab（终端 / 文件 / TUI）、右栏文件树、弹出式看板与工单、设置页。

**Architecture:** 前端引入「当前基准目录 + 按基准目录持有的 tab 组」这一唯一全局选中态；tab 的增删改查抽成纯函数模块（`workbench/tabs.ts`）先测后用，React 只做状态容器。W3/W4a/W4b 已交付的面板（TimelinePanel / EventsPanel / ReviewPanel / AdvanceActions / TicketsPanel / BoardPage / MachinesPage）**只搬位置不重写**。后端只新增两个按工作树寻址的只读接口，共用一条白名单闸门。

**Tech Stack:** Go 1.26（`net/http` ServeMux、`os.OpenRoot`）；React 19 + TypeScript + react-router-dom 7 + Tailwind 4 + lucide-react；测试用 `go test` 与 vitest + @testing-library/react。

## Global Constraints

以下取自 spec `docs/superpowers/specs/2026-08-12-w4-shell-calibration-design.md`，每个任务都隐式受其约束：

- **形态基准是 `prototypes/desktop-console/`**，判据是与 `prototypes/desktop-console/implementation-complete-workbench.png` 并排看结构能对上。偏离只允许 spec §8 记录的五条。
- **只有三种 tab：终端、文件、TUI。** 不得引入第四种。空白态是「尚未选择种类」的 tab 状态，不是第四种 tab。
- **中央 `+` 与左栏/看板快捷操作的基准目录 = 当前选中的工作树目录；悬浮按钮的基准目录 = 用户 home。**
- **悬浮按钮本期只有「新终端」一项。** 不得为它放宽 §7.1 的白名单。理由：`~/.handoff/config.yaml` 里存着 agentd 主令牌，控制台会话是刻意做得比主令牌弱的凭据，能读 `$HOME` 即弱凭据提权成强凭据。
- **目录列举与读文件接口只接受已探测到的 `Workspace.path`**（`filepath.Clean` 归一化后等值比对），其余一律 403。
- **不做**：PTY 终端（壳做、内容是占位说明）、文件写入 / 在线编辑、浏览器预览 tab（整个取消）、TUI 顶栏的模型名与 context token 用量、以 home 为基准的文件浏览。
- **诚实展示纪律（不得回退）**：不可达机器保持可见并标「已断开」+ 原因原文；未归属任务（`project_id === ''`）挂树末尾的「未归属」分组；agentd 的中文错误原文透传，不得吞成「操作失败」。
- **审批入口唯一**：时间线里的 `EventMark` 仍然不可点，只做指向。
- **不置灰**：不渲染永远点不动的控件；可点但未就绪的，点了要给出明确的「尚未实现」说明。
- **同时只有一个弹出层**（看板、工单），否则 Esc 关哪个会含糊。设置页不是弹层，是整页替换中央内容区。
- **日志与注释**（`instrumenting-code`）：Go 侧一律 `s.log` / `log()`，禁止 `fmt.Printf`；每个新文件写文件头注释（职责 + 边界），每个导出函数写文档注释，非显然分支写「为什么」注释。前端不引入 `console.log`。

## 本计划新增的两条决定（spec 未覆盖，实现前先看这里）

**1. 悬浮按钮开出的 tab 落在哪里。** spec §2.5 说悬浮按钮以 home 为基准开 tab，但没说这个 tab 渲染在什么位置。本计划的决定：**home 作为一个伪目录进入同一套 tab 系统**，键为 `~`。点悬浮按钮的「新终端」= 把当前基准切到 home 并在其 tab 组里开一个终端 tab；面包屑显示 `home`。理由：tab 组按基准目录分别持有且切换无损（spec §1.2），切到 home 再切回工作树不丢任何东西；另造一套浮动窗口容器等于把 tab 系统写两遍。

**2. `/tasks/:id` 深链保留。** W3b 的 `TaskPage` 是可深链的，直接删路由会让已有书签 404。本计划的决定：`/tasks/:id` 仍然可访问，但它的行为改为「选中该任务所在目录 + 在中央开它的 TUI tab + `navigate('/', {replace: true})`」。`TaskPage.tsx` 本身删除，它的数据编排提取为 `useTaskSession` 供 TUI tab 复用——两份实现必然漂移。

---

## File Structure

**后端（Go）**

| 文件 | 责任 |
|---|---|
| `internal/proto/projects.go`（改） | 新增 `DirEntry` / `DirListResult` 线格式类型 |
| `internal/proto/contract_fixture_test.go`（改） | 新类型进 fixture 列表 |
| `internal/agentd/workspace.go`（改） | 新增 `ListDir`（与 `ReadFile` 同一套 `os.OpenRoot` 防护）、新增 `ErrPathNotDir` |
| `internal/agentd/workspacefiles.go`（新） | 白名单闸门 `resolveWorkspace` + 两个 HTTP handler |
| `internal/agentd/server.go`（改） | 注册两条路由 + 路由表注释 |

**前端：工作台（新目录 `web/src/app/workbench/`）**

| 文件 | 责任 |
|---|---|
| `tabs.ts` | tab 模型的纯函数：身份、去重、开关、激活、分屏。无 React 依赖 |
| `useWorkbench.ts` | React 状态容器：当前基准目录 + `Map<基准键, Workbench>` |
| `WorkbenchPage.tsx` | 中央区：一或两组 tab 的布局与渲染分发 |
| `TabBar.tsx` | 单组的 tab 条 + `+` 按钮 |
| `BlankTab.tsx` | 空白 tab 的种类选择面板（含快捷键提示）。**悬浮按钮不复用它**——那边只有「新终端」一项，见 Task 15 |
| `TerminalTab.tsx` | 终端 tab 的壳 + 「PTY 后端尚未实现」说明 |
| `FileTab.tsx` | 只读文件查看 |
| `TuiTab.tsx` | 桌面端 TUI：时间线 + 事件流 + 审阅取证 + 底部指令框 |
| `EmptyWorkbench.tsx` | 未选中目录时的全局空态 |
| `FloatingNewPane.tsx` | 右下角悬浮按钮 |

**前端：右栏文件树（新目录 `web/src/app/files/`）**

| 文件 | 责任 |
|---|---|
| `changedFiles.ts` | 纯函数：从 `git diff` 文本解析出改动过的相对路径集合 |
| `useDirEntries.ts` | 单层目录列举的取数（按需展开，不递归） |
| `FileTree.tsx` | 右栏：头部 + 搜索 + 可展开树 + `M` 角标 |

**前端：弹出层（新目录 `web/src/app/overlay/`）**

| 文件 | 责任 |
|---|---|
| `Overlay.tsx` | 弹层基座：遮罩、Esc 关闭、焦点收敛。看板与工单共用 |
| `BoardOverlay.tsx` | 看板弹层（内容复用 `BoardPage` 的列与卡片） |
| `TicketsOverlay.tsx` | 全局工单弹层 |
| `useGlobalTickets.ts` | 跨任务的挂起工单聚合（`waiting_answer` 任务逐个取详情） |

**前端：改造**

| 文件 | 改动 |
|---|---|
| `web/src/app/shell/Shell.tsx` | 三栏布局 + 面包屑 + 弹层与悬浮按钮的宿主 |
| `web/src/app/shell/Breadcrumb.tsx`（新） | `项目 / 开发机 / 目录` + 分屏按钮 |
| `web/src/app/shell/TopTabs.tsx` | **删除**（顶层导航收进左栏顶部与设置页） |
| `web/src/app/tree/ProjectTree.tsx` | 点击语义从「写 filter」改为「导航」；底部三按钮；顶部看板入口 |
| `web/src/app/task/useTaskSession.ts`（新） | 从 `TaskPage` 提取的任务会话编排 |
| `web/src/app/task/TaskPage.tsx` | **删除** |
| `web/src/app/task/EventMark.tsx` | 指向文案改为工单面板的新位置 |
| `web/src/app/settings/SettingsPage.tsx`（新） | 开发机 / 常规 / Env 三个分区 |
| `web/src/app/machines/MachinesPage.tsx` | 去掉自己的 `<main>` 外框，改为可嵌入设置页的分区 |
| `web/src/api/client.ts` / `types.ts` | 新增 `fetchWorkspaceDir` / `fetchWorkspaceFile` 与对应类型 |
| `web/src/App.tsx` | 路由改为 `/`、`/tasks/:id`、`/settings`、`/machines`→`/settings` |

---

## Task 1: 后端 —— 目录列举的线格式类型与 ListDir

**Files:**
- Modify: `internal/proto/projects.go`（文件末尾追加类型）
- Modify: `internal/proto/contract_fixture_test.go:62-79`（cases 列表）与文件末尾（新增 sample 函数）
- Modify: `internal/agentd/workspace.go:53-55`（错误哨兵）与文件末尾（新增 `ListDir`）
- Test: `internal/agentd/workspace_test.go`（追加）
- Create: `web/src/api/testdata/DirListResult.json`（由 `-update` 生成，不手写）

**Interfaces:**
- Consumes: 既有 `ErrPathEscape` / `openNonBlock` / `rootErrIsEscape`（`internal/agentd/workspace.go`）
- Produces:
  - `proto.DirEntry{Name string; IsDir bool; Size int64}`（json: `name` / `is_dir` / `size,omitempty`）
  - `proto.DirListResult{Entries []DirEntry}`（json: `entries`）
  - `agentd.ErrPathNotDir`
  - `agentd.ListDir(repo, rel string) ([]proto.DirEntry, error)`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/workspace_test.go`：

```go
// TestListDirBasic 覆盖列举的四条硬约束：只列一层、目录在前、字典序、rel 为空即根。
func TestListDirBasic(t *testing.T) {
	repo := t.TempDir()
	mustMkdirAll(t, filepath.Join(repo, "internal", "agentd"))
	mustMkdirAll(t, filepath.Join(repo, "cmd"))
	mustWriteFile(t, filepath.Join(repo, "go.mod"), "module x\n")
	mustWriteFile(t, filepath.Join(repo, "README.md"), "hi")

	entries, err := ListDir(repo, "")
	if err != nil {
		t.Fatalf("ListDir 根目录: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, fmt.Sprintf("%s/%v", e.Name, e.IsDir))
	}
	want := []string{"cmd/true", "internal/true", "README.md/false", "go.mod/false"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("根目录列举 = %v, want %v", got, want)
	}

	// 只列一层：internal 下只应有 agentd，不含 internal/agentd 的内容
	sub, err := ListDir(repo, "internal")
	if err != nil {
		t.Fatalf("ListDir internal: %v", err)
	}
	if len(sub) != 1 || sub[0].Name != "agentd" || !sub[0].IsDir {
		t.Errorf("internal 列举 = %+v, want 只有目录 agentd", sub)
	}

	// 普通文件带 size
	root, err := ListDir(repo, ".")
	if err != nil {
		t.Fatalf("ListDir .: %v", err)
	}
	for _, e := range root {
		if e.Name == "go.mod" && e.Size != int64(len("module x\n")) {
			t.Errorf("go.mod size = %d, want %d", e.Size, len("module x\n"))
		}
	}
}

// TestListDirRejectsEscape 断言列举与 ReadFile 共用同一条逃逸红线。
func TestListDirRejectsEscape(t *testing.T) {
	repo := t.TempDir()
	for _, rel := range []string{"..", "../etc", "/etc", filepath.Join("a", "..", "..")} {
		if _, err := ListDir(repo, rel); !errors.Is(err, ErrPathEscape) {
			t.Errorf("ListDir(%q) err = %v, want ErrPathEscape", rel, err)
		}
	}
}

// TestListDirOnFileIsNotDir 断言把文件当目录列举时给出可辨识的错误（映射 400）。
func TestListDirOnFileIsNotDir(t *testing.T) {
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, "go.mod"), "module x\n")
	if _, err := ListDir(repo, "go.mod"); !errors.Is(err, ErrPathNotDir) {
		t.Errorf("ListDir(go.mod) err = %v, want ErrPathNotDir", err)
	}
}

// TestListDirMissing 断言不存在的子目录返回 fs.ErrNotExist（映射 404）。
func TestListDirMissing(t *testing.T) {
	repo := t.TempDir()
	if _, err := ListDir(repo, "nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ListDir(nope) err = %v, want fs.ErrNotExist", err)
	}
}

// mustMkdirAll 建目录，失败即 Fatal。
func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录 %s: %v", dir, err)
	}
}

// mustWriteFile 写文件，失败即 Fatal。
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件 %s: %v", path, err)
	}
}
```

注意：`mustMkdirAll` / `mustWriteFile` 若 `workspace_test.go` 里已有同名 helper，删掉本处这两个函数改用既有的（先 `grep -n "func mustWriteFile" internal/agentd/*_test.go` 确认）。import 需要 `errors` / `fmt` / `io/fs` / `os` / `path/filepath` / `reflect` / `testing`。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestListDir' -v
```

Expected: 编译失败，`undefined: ListDir`、`undefined: ErrPathNotDir`。

- [ ] **Step 3: 加 proto 类型**

在 `internal/proto/projects.go` 末尾追加：

```go
// DirEntry 是工作树目录列举里的一项（GET /api/workspaces/dir）。
//
// 只有三个字段是刻意的：文件浏览需要的是「这一层有什么、哪些能展开、多大」，
// 而 mtime / mode / owner 都会诱导前端做它不该做的判断（比如按 mtime 猜改动，
// 那是 diff 的活）。Size 只对普通文件有意义，目录恒 0 并被 omitempty 省略。
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

// DirListResult 是 GET /api/workspaces/dir 的响应体。
//
// Entries 永不为 nil：空目录返回 []，前端 `.map` 不需要判空。
type DirListResult struct {
	Entries []DirEntry `json:"entries"`
}
```

- [ ] **Step 4: 加错误哨兵与 ListDir**

`internal/agentd/workspace.go` 的错误哨兵块（约 53-55 行）追加一行：

```go
	ErrPathNotDir      = errors.New("路径不是目录")
```

同时在该 `var` 块上方的文档注释里补一行说明（与既有三条并列）：

```go
//   - ErrPathNotDir：请求列举的路径不是目录（ListDir 只服务目录）
```

在 `internal/agentd/workspace.go` 的 `ReadFile` 之后追加：

```go
// ListDir 列举工作树内某个目录的**直接子项**，不递归。
//
// 与 ReadFile 共用同一套路径防护（安全红线，两道）：
//  1. filepath.Clean 归一化后，绝对路径或残留 .. 前缀一律拒绝（ErrPathEscape）
//  2. 实际打开经 os.OpenRoot（内核级 jail），符号链接逃逸由内核在单次系统调用
//     内拒绝，不留 TOCTOU 窗口
//
// 为什么不递归：一次递归列举一个大仓库要遍历几十万个 inode，而前端一次只画
// 一层。按需展开把成本摊到用户真正点开的那几层上。
//
// 排序：目录在前、各自按名称字典序。为什么由服务端排而不是前端排：前端会有
// 搜索过滤与虚拟滚动，排序稳定性交给一处比在多处各排一次可靠。
//
// 参数：
//   - repo: 工作树绝对路径（调用方必须已过白名单闸门，本函数不做白名单判定）
//   - rel: 相对工作树根的目录路径；"" 与 "." 都表示根
//
// 返回：
//   - 子项列表（**永不为 nil**）
//   - err: 逃逸返回 ErrPathEscape；目标不是目录返回 ErrPathNotDir；
//     目标不存在返回 *fs.PathError（含 %w 链，errors.Is(err, fs.ErrNotExist) 为真）
func ListDir(repo, rel string) ([]proto.DirEntry, error) {
	cleaned := filepath.Clean(rel)
	if rel == "" {
		cleaned = "."
	}
	if filepath.IsAbs(cleaned) || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		log().Warn("目录列举路径逃逸被拒绝", "repo", repo, "path", rel)
		return nil, fmt.Errorf("%w: %q", ErrPathEscape, rel)
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, fmt.Errorf("打开工作树 %s: %w", repo, err)
	}
	defer root.Close()
	// O_NONBLOCK 的理由与 ReadFile 相同：没有写端的 FIFO 会让 openat 永久挂住，
	// 而「不是目录」的判定排在打开之后
	f, err := root.OpenFile(cleaned, os.O_RDONLY|openNonBlock, 0)
	if err != nil {
		if rootErrIsEscape(err) {
			log().Warn("目录列举路径逃逸被拒绝", "repo", repo, "path", rel)
			return nil, fmt.Errorf("%w: %q", ErrPathEscape, rel)
		}
		return nil, fmt.Errorf("列举目录 %s: %w", filepath.Join(repo, cleaned), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("列举目录 %s: %w", filepath.Join(repo, cleaned), err)
	}
	if !fi.IsDir() {
		log().Warn("目录列举目标不是目录", "repo", repo, "path", rel, "mode", fi.Mode().String())
		return nil, fmt.Errorf("%w: %q", ErrPathNotDir, rel)
	}
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("列举目录 %s: %w", filepath.Join(repo, cleaned), err)
	}
	entries := make([]proto.DirEntry, 0, len(des))
	for _, de := range des {
		e := proto.DirEntry{Name: de.Name(), IsDir: de.IsDir()}
		if !de.IsDir() {
			// Info 失败（列举与 stat 之间文件被删）不是整次列举的失败：
			// 少一个 size 比整棵树列不出来强，如实按 0 记并 Debug
			if info, err := de.Info(); err == nil {
				e.Size = info.Size()
			} else {
				log().Debug("取子项大小失败，按 0 记", "repo", repo, "path", rel, "name", de.Name(), "cause", err)
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // 目录在前
		}
		return entries[i].Name < entries[j].Name
	})
	log().Debug("目录列举完成", "repo", repo, "path", rel, "entries", len(entries))
	return entries, nil
}
```

`internal/agentd/workspace.go` 的 import 需要补 `sort`（`fmt` / `os` / `path/filepath` / `strings` 已有）；文件头注释的职责列表补一行「ListDir（列举工作树内一层目录）」。

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run 'TestListDir' -v
```

Expected: 四个测试全 PASS。

- [ ] **Step 6: 把新类型钉进契约 fixture**

`internal/proto/contract_fixture_test.go` 的 `cases` 列表在 `{"Frame", frameSample(now)}` 之后追加一行：

```go
		{"DirListResult", dirListSample()},
```

文件末尾追加：

```go
// dirListSample 返回 DirListResult 的代表性样本。
//
// 一目录一文件覆盖 Size 的 omitempty 边界：目录不带 size 键，普通文件带。
func dirListSample() DirListResult {
	return DirListResult{
		Entries: []DirEntry{
			{Name: "internal", IsDir: true},
			{Name: "go.mod", IsDir: false, Size: 1284},
		},
	}
}
```

生成 fixture：

```bash
go test ./internal/proto/ -run TestContractFixtures -update
```

- [ ] **Step 7: 全量验证**

```bash
go test ./internal/proto/ ./internal/agentd/
```

Expected: 全部 PASS（`TestContractFixtures` 在 `-update` 之后应当逐字节一致）。

- [ ] **Step 8: 提交**

```bash
git add internal/proto/projects.go internal/proto/contract_fixture_test.go internal/agentd/workspace.go internal/agentd/workspace_test.go web/src/api/testdata/DirListResult.json
git commit -m "feat(agentd): 新增 ListDir 与目录列举线格式类型"
```

---

## Task 2: 后端 —— 白名单闸门与两个工作树接口

**Files:**
- Create: `internal/agentd/workspacefiles.go`
- Create: `internal/agentd/workspacefiles_test.go`
- Modify: `internal/agentd/server.go:209-215`（路由注册）与 `server.go:185-192`（路由表注释）

**Interfaces:**
- Consumes: `ListDir` / `ReadFile` / `ErrPathEscape` / `ErrPathIsDir` / `ErrPathNotDir` / `ErrNotRegularFile`（Task 1 与既有）；`probeWorkspaces(ctx, dir, managedRoot)`（`workspaceprobe.go`）；`s.st.ListProjectLocations()`；`s.forwardIfRequested(w, r)`（`forward.go`）；`writeJSON(w, status, v)`
- Produces:
  - `GET /api/workspaces/dir?path=&rel=[&machine=]` → `proto.DirListResult`
  - `GET /api/workspaces/file?path=&rel=[&machine=]` → `{"content": "..."}`
  - `(s *Server) resolveWorkspace(ctx context.Context, path string) (string, bool)`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/workspacefiles_test.go`：

```go
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// wsFilesFixture 起一个带单个已登记项目的 agentd，返回 server 与该项目路径。
// newTestServer / registerLocation 是 agentd 包既有测试脚手架，若签名不同以实际为准。
func wsFilesFixture(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	repo := t.TempDir()
	gitInit(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	registerLocation(t, s, "demo", repo)
	return s, repo
}

// doGet 发一个 GET 到 s.Handler() 并返回响应。
func doGet(t *testing.T, s *Server, path string, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path+"?"+q.Encode(), nil)
	authorize(t, req, s)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// TestWorkspaceDirListsWhitelistedPath 断言已探测到的工作树可以被列举。
func TestWorkspaceDirListsWhitelistedPath(t *testing.T) {
	s, repo := wsFilesFixture(t)
	rec := doGet(t, s, "/api/workspaces/dir", url.Values{"path": {repo}})
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200；体 = %s", rec.Code, rec.Body.String())
	}
	var got proto.DirListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Name)
	}
	// .git 也会被列出，只断言我们建的两项都在、且目录排在文件前
	if len(got.Entries) == 0 || !got.Entries[0].IsDir {
		t.Errorf("列举结果 = %v, want 目录在前", names)
	}
	found := map[string]bool{}
	for _, e := range got.Entries {
		found[e.Name] = true
	}
	if !found["internal"] || !found["go.mod"] {
		t.Errorf("列举结果 = %v, want 含 internal 与 go.mod", names)
	}
}

// TestWorkspaceDirRejectsUnknownPath 是本任务的安全红线：
// 未登记的任意目录一律 403，agentd 不是任意目录浏览器。
func TestWorkspaceDirRejectsUnknownPath(t *testing.T) {
	s, _ := wsFilesFixture(t)
	outside := t.TempDir()
	rec := doGet(t, s, "/api/workspaces/dir", url.Values{"path": {outside}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码 = %d, want 403；体 = %s", rec.Code, rec.Body.String())
	}
}

// TestWorkspaceDirRejectsHome 单独钉住 $HOME：spec §2.6 的整条论证依赖它。
func TestWorkspaceDirRejectsHome(t *testing.T) {
	s, _ := wsFilesFixture(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到 home 目录")
	}
	rec := doGet(t, s, "/api/workspaces/dir", url.Values{"path": {home}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("$HOME 列举状态码 = %d, want 403", rec.Code)
	}
}

// TestWorkspaceDirMissingPath 断言缺 path 参数是 400 而不是 403。
func TestWorkspaceDirMissingPath(t *testing.T) {
	s, _ := wsFilesFixture(t)
	rec := doGet(t, s, "/api/workspaces/dir", url.Values{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400", rec.Code)
	}
}

// TestWorkspaceFileReads 断言按工作树寻址的读文件与 /api/tasks/{id}/file 同语义。
func TestWorkspaceFileReads(t *testing.T) {
	s, repo := wsFilesFixture(t)
	rec := doGet(t, s, "/api/workspaces/file", url.Values{"path": {repo}, "rel": {"go.mod"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200；体 = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if got.Content != "module x\n" {
		t.Errorf("content = %q, want %q", got.Content, "module x\n")
	}
}

// TestWorkspaceFileErrorMapping 断言四种路径错误各自映射到正确状态码。
func TestWorkspaceFileErrorMapping(t *testing.T) {
	s, repo := wsFilesFixture(t)
	cases := []struct {
		name string
		rel  string
		want int
	}{
		{"逃逸", "../etc/passwd", http.StatusBadRequest},
		{"不存在", "nope.go", http.StatusNotFound},
		{"是目录", "internal", http.StatusBadRequest},
		{"缺 rel", "", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := url.Values{"path": {repo}}
			if c.rel != "" {
				q.Set("rel", c.rel)
			}
			rec := doGet(t, s, "/api/workspaces/file", q)
			if rec.Code != c.want {
				t.Errorf("状态码 = %d, want %d；体 = %s", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}
```

先跑 `grep -n "func newTestServer\|func registerLocation\|func gitInit\|func authorize" internal/agentd/*_test.go` 确认这四个脚手架的真实名字与签名，按实际改写 `wsFilesFixture` / `doGet`；不要新造重复的脚手架。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestWorkspace(Dir|File)' -v
```

Expected: 全部 404（路由不存在）。

- [ ] **Step 3: 写 handler**

新建 `internal/agentd/workspacefiles.go`：

```go
// 本文件实现「按工作树寻址」的两个只读文件接口：目录列举与读文件。
//
// 职责：
//   - resolveWorkspace：白名单闸门——只有 GET /api/projects/tree 探测得出的
//     工作树路径才被接受
//   - handleWorkspaceDir / handleWorkspaceFile：两个端点的 HTTP 层
//
// 边界：
//   - 不写文件、不建目录、不删任何东西：本期是只读的（spec §7.3）
//   - 不接受任意路径。这是防止 agentd 变成任意目录浏览器的**唯一**闸门，
//     它的存在理由见 spec §2.6：~/.handoff/config.yaml 里存着 agentd 主令牌，
//     而浏览器控制台会话是刻意做得比主令牌弱的凭据（一次性 ticket 换取、
//     可吊销、按设备记录）。让一个控制台会话能读 $HOME，就是把弱凭据当场
//     提权成强凭据，整套会话管理的意义归零
//   - 不改 /api/tasks/{id}/file：那是 CLI `handoff fetch` 在用的既有契约。
//     另开一条是因为工作树可以没有任务（人手开的、任务已 done 的），
//     文件浏览不能依赖任务存在
package agentd

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/xushixin/handoff/internal/proto"
)

// resolveWorkspace 判定 path 是否是本机**已探测到**的某个工作树。
//
// 判定口径与 GET /api/projects/tree 完全一致：先比登记表里的位置路径（一次
// 数据库读，绝大多数请求命中主目录时到此为止），未命中再对每个 location 跑一次
// git worktree list 现场探测。两段式是为了让最常见的请求不付探测成本。
//
// 为什么不缓存探测结果：worktree 会在 agentd 背后被 git worktree add/remove
// 改动，缓存必然产生「已经删掉的工作树还能浏览」的失真窗口——而这条闸门是
// 安全边界，失真窗口开在安全边界上代价太大。真变慢了再谈带短 TTL 的缓存。
//
// 参数：
//   - ctx: 上下文，透传给 probeWorkspaces（其内部另有 5s 兜底超时）
//   - path: 调用方上送的工作树绝对路径
//
// 返回：
//   - 归一化（filepath.Clean）后的路径，仅在 ok 为真时有意义
//   - ok: 命中白名单为真
func (s *Server) resolveWorkspace(ctx context.Context, path string) (string, bool) {
	want := filepath.Clean(path)
	locs, err := s.st.ListProjectLocations()
	if err != nil {
		// 读不出位置表时**拒绝**而不是放行：闸门坏了要关上，不能敞开
		s.log.Error("工作树白名单：查询位置表失败，按拒绝处理", "cause", err)
		return "", false
	}
	for _, l := range locs {
		if l.ProjectID == "" {
			continue
		}
		if filepath.Clean(l.Path) == want {
			return want, true
		}
	}
	managedRoot := filepath.Join(s.cfg.DataDir, "worktrees")
	for _, l := range locs {
		if l.ProjectID == "" {
			continue
		}
		ws, probeErr := probeWorkspaces(ctx, l.Path, managedRoot)
		if probeErr != "" {
			continue
		}
		for _, w := range ws {
			if filepath.Clean(w.Path) == want {
				return want, true
			}
		}
	}
	return "", false
}

// workspaceRootOrErr 取出并校验 path 查询参数，失败时已写好响应。
//
// 返回 ok=false 时调用方必须直接 return。
func (s *Server) workspaceRootOrErr(w http.ResponseWriter, r *http.Request) (string, bool) {
	path := r.URL.Query().Get("path")
	if path == "" {
		s.log.Warn("工作树请求缺 path 参数", "url_path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 path 参数"})
		return "", false
	}
	root, ok := s.resolveWorkspace(r.Context(), path)
	if !ok {
		// 403 而不是 404：路径存在与否不该从状态码泄露出去，而「你没有权限
		// 浏览这个目录」正是真实原因
		s.log.Warn("工作树白名单拒绝", "path", path, "url_path", r.URL.Path)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "路径不是本机已探测到的工作树，拒绝访问"})
		return "", false
	}
	return root, true
}

// handleWorkspaceDir 处理 GET /api/workspaces/dir?path=&rel=[&machine=]。
//
// 参数：
//   - path: 工作树绝对路径（必须，且必须命中白名单，否则 403）
//   - rel: 工作树内的相对目录路径；省略或空串表示工作树根
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested）
func (s *Server) handleWorkspaceDir(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树目录列举请求", "path", r.URL.Query().Get("path"), "rel", rel)
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	entries, err := ListDir(root, rel)
	if err != nil {
		switch {
		case errors.Is(err, ErrPathEscape):
			s.log.Warn("目录列举路径逃逸被拒绝", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法（不允许逃出工作树）"})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("目录列举目标不存在", "root", root, "rel", rel)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "目录不存在"})
		case errors.Is(err, ErrPathNotDir):
			s.log.Warn("目录列举目标不是目录", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不是目录"})
		default:
			s.log.Error("目录列举失败", "root", root, "rel", rel, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "目录列举失败"})
		}
		return
	}
	s.log.Info("工作树目录列举完成", "root", root, "rel", rel, "entries", len(entries))
	writeJSON(w, http.StatusOK, proto.DirListResult{Entries: entries})
}

// handleWorkspaceFile 处理 GET /api/workspaces/file?path=&rel=[&machine=]。
//
// 语义与 GET /api/tasks/{id}/file 完全一致（同一个 ReadFile、同一套错误映射），
// 只是寻址从任务改为工作树。
func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树读文件请求", "path", r.URL.Query().Get("path"), "rel", rel)
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	if rel == "" {
		s.log.Warn("工作树读文件缺 rel 参数", "root", root)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 rel 参数"})
		return
	}
	content, err := ReadFile(root, rel)
	if err != nil {
		switch {
		case errors.Is(err, ErrPathEscape):
			s.log.Warn("读文件路径逃逸被拒绝", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法（不允许逃出工作树）"})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("读文件目标不存在", "root", root, "rel", rel)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		case errors.Is(err, ErrPathIsDir):
			s.log.Warn("读文件目标是目录", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径是目录，不是文件"})
		case errors.Is(err, ErrNotRegularFile):
			s.log.Warn("读文件目标不是普通文件", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不是普通文件"})
		default:
			s.log.Error("读取文件失败", "root", root, "rel", rel, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		}
		return
	}
	s.log.Info("工作树读文件完成", "root", root, "rel", rel, "bytes", len(content))
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}
```

- [ ] **Step 4: 注册路由**

`internal/agentd/server.go` 在 `mux.HandleFunc("GET /api/machines", s.handleMachines)` 之后追加两行：

```go
	mux.HandleFunc("GET /api/workspaces/dir", s.handleWorkspaceDir)
	mux.HandleFunc("GET /api/workspaces/file", s.handleWorkspaceFile)
```

同时在 `Handler()` 上方的路由表注释里（`//   - GET  /api/projects ...` 那一段）追加两行：

```go
//   - GET  /api/workspaces/dir          列举工作树内一层目录（白名单：仅已探测到的工作树）
//   - GET  /api/workspaces/file         读工作树内单个文件（同上白名单）
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run 'TestWorkspace(Dir|File)' -v
```

Expected: 六个测试全 PASS，其中 `TestWorkspaceDirRejectsUnknownPath` 与 `TestWorkspaceDirRejectsHome` 是安全红线。

- [ ] **Step 6: 全量回归**

```bash
go build ./... && go vet ./... && gofmt -l internal/ && go test ./...
```

Expected: `gofmt -l` 无输出；测试全绿。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspacefiles.go internal/agentd/workspacefiles_test.go internal/agentd/server.go
git commit -m "feat(agentd): 新增按工作树寻址的目录列举与读文件接口"
```

---

## Task 3: 前端 —— 两个新接口的客户端与类型

**Files:**
- Modify: `web/src/api/types.ts`（文件末尾追加）
- Modify: `web/src/api/client.ts`（在 `fetchMachines` 之后追加）
- Modify: `web/src/api/contract.test.ts`（新增 fixture 断言）

**Interfaces:**
- Consumes: Task 1 生成的 `web/src/api/testdata/DirListResult.json`；Task 2 的两条端点
- Produces:
  - `interface DirEntry { name: string; is_dir: boolean; size?: number }`
  - `interface DirListResult { entries: DirEntry[] }`
  - `fetchWorkspaceDir(path: string, rel?: string, machine?: string): Promise<DirListResult>`
  - `fetchWorkspaceFile(path: string, rel: string, machine?: string): Promise<FileResult>`

- [ ] **Step 1: 写失败的测试**

`web/src/api/contract.test.ts` 顶部 import 区追加：

```ts
import dirListFixture from './testdata/DirListResult.json'
```

类型 import 区追加 `type DirListResult,`，并在文件末尾追加：

```ts
describe('DirListResult 契约', () => {
  it('目录项不带 size，普通文件带 size', () => {
    const resp: DirListResult = dirListFixture
    expect(resp.entries).toHaveLength(2)
    const [dir, file] = resp.entries
    expect(dir.is_dir).toBe(true)
    // 目录的 size 被 omitempty 省略：缺键而不是 0
    expect(dir.size).toBeUndefined()
    expect(file.is_dir).toBe(false)
    expect(file.size).toBe(1284)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- contract
```

Expected: FAIL，`Cannot find module './testdata/DirListResult.json'` 或 `DirListResult` 未导出。

- [ ] **Step 3: 加类型**

`web/src/api/types.ts` 末尾追加：

```ts
// DirEntry 是 GET /api/workspaces/dir 列举出的一项。
//
// size 只对普通文件存在（Go 侧 omitempty）：目录是**缺键**而不是 0，
// 前端不要用 `entry.size ?? 0` 去掩盖这个区别——它是「这是目录」的第二个证据。
export interface DirEntry {
  name: string
  is_dir: boolean
  size?: number
}

// DirListResult 是 GET /api/workspaces/dir 的响应体；entries 永不为 null。
export interface DirListResult {
  entries: DirEntry[]
}
```

- [ ] **Step 4: 加客户端函数**

`web/src/api/client.ts` 的 import 类型列表补 `DirListResult,`，并在 `fetchMachines` 之后追加：

```ts
// workspaceQuery 拼两个工作树接口共用的查询串。
//
// rel 省略表示工作树根；machine 省略或空串 = 本机（与 Task.machine 的空串语义一致）。
function workspaceQuery(path: string, rel?: string, machine?: string): string {
  const q = new URLSearchParams({ path })
  if (rel) q.set('rel', rel)
  if (machine) q.set('machine', machine)
  return q.toString()
}

// fetchWorkspaceDir 列举工作树内一层目录（GET /api/workspaces/dir）。
//
// path 必须是 GET /api/projects/tree 给出的某个 Workspace.path 原样值——
// agentd 侧按等值比对做白名单，任意路径返回 403（spec §7.1）。
export function fetchWorkspaceDir(path: string, rel?: string, machine?: string): Promise<DirListResult> {
  return request<DirListResult>(`/api/workspaces/dir?${workspaceQuery(path, rel, machine)}`)
}

// fetchWorkspaceFile 读工作树内单个文件（GET /api/workspaces/file）。
// 语义与 fetchTaskFile 一致，只是寻址从任务改为工作树。
export function fetchWorkspaceFile(path: string, rel: string, machine?: string): Promise<FileResult> {
  return request<FileResult>(`/api/workspaces/file?${workspaceQuery(path, rel, machine)}`)
}
```

- [ ] **Step 5: 跑测试与类型检查**

```bash
npm --prefix web run test && npm --prefix web run typecheck && npm --prefix web run lint
```

Expected: 全绿。

- [ ] **Step 6: 提交**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/contract.test.ts
git commit -m "feat(web): 接入工作树目录列举与读文件接口"
```

---

## Task 4: tab 模型的纯函数

**Files:**
- Create: `web/src/app/workbench/tabs.ts`
- Test: `web/src/app/workbench/tabs.test.ts`

**Interfaces:**
- Consumes: 无（纯 TS，不依赖 React 与 api）
- Produces（后续所有 workbench 任务都按这些名字用）：
  - `type TabContent = { kind: 'blank' } | { kind: 'terminal'; seq: number } | { kind: 'file'; rel: string } | { kind: 'tui'; taskId: string }`
  - `interface Tab { id: string; content: TabContent }`
  - `interface TabGroup { tabs: Tab[]; activeId: string | null }`
  - `interface Workbench { groups: TabGroup[]; active: number }`
  - `const EMPTY_WORKBENCH: Workbench`
  - `dedupKey(c: TabContent): string | null`
  - `openTab(wb: Workbench, content: TabContent, group?: number): Workbench`
  - `closeTab(wb: Workbench, group: number, tabId: string): Workbench`
  - `activateTab(wb: Workbench, group: number, tabId: string): Workbench`
  - `setTabContent(wb: Workbench, group: number, tabId: string, content: TabContent): Workbench`
  - `splitGroup(wb: Workbench): Workbench`
  - `nextTerminalSeq(wb: Workbench): number`
  - `tabTitle(c: TabContent, baseLabel: string): string`

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/workbench/tabs.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import {
  EMPTY_WORKBENCH,
  activateTab,
  closeTab,
  dedupKey,
  nextTerminalSeq,
  openTab,
  setTabContent,
  splitGroup,
  tabTitle,
  type Workbench,
} from './tabs'

describe('dedupKey', () => {
  it('文件与 TUI 按目标去重，终端与空白不去重', () => {
    expect(dedupKey({ kind: 'file', rel: 'go.mod' })).toBe('file:go.mod')
    expect(dedupKey({ kind: 'tui', taskId: 'T1' })).toBe('tui:T1')
    expect(dedupKey({ kind: 'terminal', seq: 2 })).toBeNull()
    expect(dedupKey({ kind: 'blank' })).toBeNull()
  })
})

describe('openTab', () => {
  it('开一个 tab 并自动激活它', () => {
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'go.mod' })
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[0].activeId).toBe(wb.groups[0].tabs[0].id)
  })

  it('同身份不重复打开，已存在则激活', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'go.mod' })
    const firstId = wb.groups[0].tabs[0].id
    wb = openTab(wb, { kind: 'tui', taskId: 'T1' })
    wb = openTab(wb, { kind: 'file', rel: 'go.mod' })
    expect(wb.groups[0].tabs).toHaveLength(2)
    expect(wb.groups[0].activeId).toBe(firstId)
  })

  it('终端可以在同一目录开多个', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    wb = openTab(wb, { kind: 'terminal', seq: 2 })
    expect(wb.groups[0].tabs).toHaveLength(2)
  })

  it('去重跨组生效：另一组已有同身份 tab 时激活它而不是再开一个', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'tui', taskId: 'T1' })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'a.go' }, 1)
    wb = openTab(wb, { kind: 'tui', taskId: 'T1' }, 1)
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[1].tabs).toHaveLength(1)
    expect(wb.active).toBe(0)
    expect(wb.groups[0].activeId).toBe(wb.groups[0].tabs[0].id)
  })

  it('tab id 在整个 workbench 内唯一', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'terminal', seq: 2 }, 1)
    const ids = wb.groups.flatMap((g) => g.tabs.map((t) => t.id))
    expect(new Set(ids).size).toBe(ids.length)
  })
})

describe('closeTab', () => {
  it('关掉激活 tab 后激活右邻，没有右邻则左邻', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = openTab(wb, { kind: 'file', rel: 'b.go' })
    wb = openTab(wb, { kind: 'file', rel: 'c.go' })
    const [a, b, c] = wb.groups[0].tabs
    wb = activateTab(wb, 0, b.id)
    wb = closeTab(wb, 0, b.id)
    expect(wb.groups[0].activeId).toBe(c.id)
    wb = closeTab(wb, 0, c.id)
    expect(wb.groups[0].activeId).toBe(a.id)
  })

  it('关掉非激活 tab 不改变激活项', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = openTab(wb, { kind: 'file', rel: 'b.go' })
    const [a, b] = wb.groups[0].tabs
    expect(wb.groups[0].activeId).toBe(b.id)
    wb = closeTab(wb, 0, a.id)
    expect(wb.groups[0].activeId).toBe(b.id)
  })

  it('两组时关掉一组的最后一个 tab，该组消失，另一组占满', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'file', rel: 'b.go' }, 1)
    const bId = wb.groups[1].tabs[0].id
    wb = closeTab(wb, 1, bId)
    expect(wb.groups).toHaveLength(1)
    expect(wb.active).toBe(0)
    expect(wb.groups[0].tabs[0].content).toEqual({ kind: 'file', rel: 'a.go' })
  })

  it('单组时关掉最后一个 tab，组保留但变空', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = closeTab(wb, 0, wb.groups[0].tabs[0].id)
    expect(wb.groups).toHaveLength(1)
    expect(wb.groups[0].tabs).toHaveLength(0)
    expect(wb.groups[0].activeId).toBeNull()
  })
})

describe('setTabContent', () => {
  it('空白 tab 选了种类后原地变成对应内容，位置与 id 不变', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    const id = wb.groups[0].tabs[0].id
    wb = setTabContent(wb, 0, id, { kind: 'terminal', seq: 1 })
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[0].tabs[0].id).toBe(id)
    expect(wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
  })

  it('选中的目标已在别的 tab 里打开时，合并到那个 tab 并关掉空白 tab', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    const existing = wb.groups[0].tabs[0].id
    wb = openTab(wb, { kind: 'blank' })
    const blank = wb.groups[0].tabs[1].id
    wb = setTabContent(wb, 0, blank, { kind: 'file', rel: 'a.go' })
    expect(wb.groups[0].tabs).toHaveLength(1)
    expect(wb.groups[0].activeId).toBe(existing)
  })
})

describe('splitGroup', () => {
  it('第一次分屏产生第二组并激活它；已经两组时是空操作', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = splitGroup(wb)
    expect(wb.groups).toHaveLength(2)
    expect(wb.active).toBe(1)
    const again = splitGroup(wb)
    expect(again.groups).toHaveLength(2)
  })
})

describe('nextTerminalSeq', () => {
  it('从 1 起，跨组取最大值 +1', () => {
    expect(nextTerminalSeq(EMPTY_WORKBENCH)).toBe(1)
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'terminal', seq: 1 })
    wb = splitGroup(wb)
    wb = openTab(wb, { kind: 'terminal', seq: 4 }, 1)
    expect(nextTerminalSeq(wb)).toBe(5)
  })
})

describe('tabTitle', () => {
  it('终端带基准目录名，文件取路径末段，TUI 带短 id，空白是「新建标签页」', () => {
    expect(tabTitle({ kind: 'terminal', seq: 2 }, 'b2-b3')).toBe('bash · b2-b3 (2)')
    expect(tabTitle({ kind: 'terminal', seq: 1 }, 'b2-b3')).toBe('bash · b2-b3')
    expect(tabTitle({ kind: 'file', rel: 'internal/agentd/server.go' }, 'b2-b3')).toBe('server.go')
    expect(tabTitle({ kind: 'tui', taskId: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad' }, 'b2-b3')).toBe('TUI · 7ec762e7')
    expect(tabTitle({ kind: 'blank' }, 'b2-b3')).toBe('新建标签页')
  })
})

// 不可变性：所有写入函数都必须返回新对象，否则 React 不会重渲染
describe('不可变性', () => {
  it('openTab 不修改入参', () => {
    const before: Workbench = EMPTY_WORKBENCH
    const after = openTab(before, { kind: 'blank' })
    expect(before.groups[0].tabs).toHaveLength(0)
    expect(after).not.toBe(before)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- tabs
```

Expected: FAIL，`Failed to resolve import "./tabs"`。

- [ ] **Step 3: 写实现**

新建 `web/src/app/workbench/tabs.ts`：

```ts
// tabs.ts —— 中央 tab 系统的模型层（纯函数，无 React 依赖）。
//
// 职责：
//   - 定义 tab 的身份、去重规则、开关与激活、左右分屏的全部状态迁移
//   - 每个写入函数都「拷贝再改」，返回新对象
//
// 边界：
//   - 不碰 React、不发请求、不认识具体渲染组件
//   - 不持有基准目录：一个 Workbench 对象**属于**某个基准目录，是谁由
//     useWorkbench 的 Map 决定。这里只管一组 tab 内部的事
//
// 只有三种 tab：终端、文件、TUI（spec §2.2）。`blank` 不是第四种，它是
// 「这个 tab 还没选种类」的中间状态——用户点 `+` 先得到一个空白 tab，
// 在里面选一种（spec §2.2.1）。把它建模成 content 的一支而不是单独的字段，
// 是为了让「选了种类」变成一次原地 setTabContent，位置与 id 都不动。

// TabContent 是一个 tab 承载的东西。
//
// 三种正式种类的「目标」各不相同，这决定了去重规则（见 dedupKey）：
//   - terminal 的目标是序号：同一目录可以开多个终端，永不去重
//   - file 的目标是基准目录内的相对路径
//   - tui 的目标是 task id
export type TabContent =
  | { kind: 'blank' }
  | { kind: 'terminal'; seq: number }
  | { kind: 'file'; rel: string }
  | { kind: 'tui'; taskId: string }

export interface Tab {
  id: string
  content: TabContent
}

// TabGroup 是一组 tab（分屏后的一侧）。activeId 为 null 表示该组为空。
export interface TabGroup {
  tabs: Tab[]
  activeId: string | null
}

// Workbench 是一个基准目录下的全部 tab：一组或两组，外加「哪一组是焦点」。
//
// 为什么最多两组：原型就是左右两组（左 TUI、右编辑器），再多的分屏在 1280px
// 宽度下每列都窄到没法读代码。真需要时改这里的不变式，而不是改调用方。
export interface Workbench {
  groups: TabGroup[]
  active: number
}

export const EMPTY_WORKBENCH: Workbench = { groups: [{ tabs: [], activeId: null }], active: 0 }

// dedupKey 返回一个 tab 内容的去重键；返回 null 表示这种内容**永不去重**。
//
// 为什么 terminal 与 blank 不去重：它们没有「目标」——再开一个终端就是真的
// 想要第二个终端，把它折叠到已有终端上是把用户的意图吃掉了。
export function dedupKey(c: TabContent): string | null {
  switch (c.kind) {
    case 'file':
      return `file:${c.rel}`
    case 'tui':
      return `tui:${c.taskId}`
    default:
      return null
  }
}

// cloneWorkbench 深拷贝到「组与 tab 数组」这一层；Tab 对象本身不可变，可共享引用。
function cloneWorkbench(wb: Workbench): Workbench {
  return {
    groups: wb.groups.map((g) => ({ tabs: [...g.tabs], activeId: g.activeId })),
    active: wb.active,
  }
}

// nextTabId 生成整个 workbench 内唯一的 tab id。
//
// 为什么不用随机数/时间戳：纯函数要可测。这里按已有 id 的数字后缀取 max+1，
// 同一串操作永远得到同一串 id。
function nextTabId(wb: Workbench): string {
  let max = 0
  for (const g of wb.groups) {
    for (const t of g.tabs) {
      const n = Number(t.id.slice(1))
      if (Number.isFinite(n) && n > max) max = n
    }
  }
  return `t${max + 1}`
}

// findByKey 在整个 workbench 里找去重键相同的 tab，返回 [组下标, tab id]。
function findByKey(wb: Workbench, key: string): [number, string] | null {
  for (let gi = 0; gi < wb.groups.length; gi++) {
    for (const t of wb.groups[gi].tabs) {
      if (dedupKey(t.content) === key) return [gi, t.id]
    }
  }
  return null
}

// openTab 在指定组（默认当前焦点组）开一个 tab 并激活它。
//
// 同身份的 tab 已存在时**不重复打开**：激活已有的那个，哪怕它在另一组
// （spec §2.2）。跨组去重是刻意的——同一个文件在左右两屏各开一份，
// 编辑时哪份是真的会当场变成一个问题。
export function openTab(wb: Workbench, content: TabContent, group?: number): Workbench {
  const key = dedupKey(content)
  if (key !== null) {
    const hit = findByKey(wb, key)
    if (hit) {
      const [gi, id] = hit
      const next = activateTab(wb, gi, id)
      next.active = gi
      return next
    }
  }
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group ?? next.active)
  const tab: Tab = { id: nextTabId(next), content }
  next.groups[gi].tabs.push(tab)
  next.groups[gi].activeId = tab.id
  next.active = gi
  return next
}

// clampGroup 把组下标夹到合法范围，避免调用方传了一个已被合并掉的组号。
function clampGroup(wb: Workbench, group: number): number {
  if (group < 0) return 0
  if (group >= wb.groups.length) return wb.groups.length - 1
  return group
}

// closeTab 关掉一个 tab。
//
// 激活项的接替规则：关掉的是激活项时接替右邻，没有右邻取左邻——这是所有
// 编辑器的共同习惯，用户不需要重新学。
//
// 两组时关空一组，该组消失、另一组占满（spec §2.1）。单组时组保留但变空，
// 由渲染层显示空态。
export function closeTab(wb: Workbench, group: number, tabId: string): Workbench {
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group)
  const g = next.groups[gi]
  const idx = g.tabs.findIndex((t) => t.id === tabId)
  if (idx === -1) return wb
  g.tabs.splice(idx, 1)
  if (g.activeId === tabId) {
    const heir = g.tabs[idx] ?? g.tabs[idx - 1] ?? null
    g.activeId = heir ? heir.id : null
  }
  if (g.tabs.length === 0 && next.groups.length > 1) {
    next.groups.splice(gi, 1)
    next.active = 0
  } else if (next.active >= next.groups.length) {
    next.active = next.groups.length - 1
  }
  return next
}

// activateTab 把某个 tab 设为其所在组的激活项，并把焦点移到该组。
export function activateTab(wb: Workbench, group: number, tabId: string): Workbench {
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group)
  if (!next.groups[gi].tabs.some((t) => t.id === tabId)) return wb
  next.groups[gi].activeId = tabId
  next.active = gi
  return next
}

// setTabContent 把一个 tab 的内容原地换掉（空白 tab 选了种类时用）。
//
// 边界情形：选中的目标已经在别的 tab 里打开了。此时正确的行为是激活那个 tab
// 并把这个空白 tab 关掉——否则用户会得到两个标着同一个文件的 tab，其中一个
// 是刚才的空白页。
export function setTabContent(wb: Workbench, group: number, tabId: string, content: TabContent): Workbench {
  const key = dedupKey(content)
  if (key !== null) {
    const hit = findByKey(wb, key)
    if (hit && hit[1] !== tabId) {
      const closed = closeTab(wb, group, tabId)
      const again = findByKey(closed, key)
      if (again) return activateTab(closed, again[0], again[1])
      return closed
    }
  }
  const next = cloneWorkbench(wb)
  const gi = clampGroup(next, group)
  const idx = next.groups[gi].tabs.findIndex((t) => t.id === tabId)
  if (idx === -1) return wb
  next.groups[gi].tabs[idx] = { id: tabId, content }
  next.groups[gi].activeId = tabId
  next.active = gi
  return next
}

// splitGroup 开启左右分屏；已经是两组时是空操作。新组为空并成为焦点。
export function splitGroup(wb: Workbench): Workbench {
  if (wb.groups.length >= 2) return wb
  const next = cloneWorkbench(wb)
  next.groups.push({ tabs: [], activeId: null })
  next.active = next.groups.length - 1
  return next
}

// nextTerminalSeq 返回下一个终端序号（跨组取最大值 +1，从 1 起）。
export function nextTerminalSeq(wb: Workbench): number {
  let max = 0
  for (const g of wb.groups) {
    for (const t of g.tabs) {
      if (t.content.kind === 'terminal' && t.content.seq > max) max = t.content.seq
    }
  }
  return max + 1
}

// tabTitle 生成 tab 条上显示的标题。
//
// 参数：
//   - c: tab 内容
//   - baseLabel: 基准目录的短名（工作树取分支名或目录名，home 取 'home'）
//
// 终端第一个不带序号（"bash · b2-b3"），第二个起才带——只有一个的时候
// 标个 (1) 是纯噪音。
export function tabTitle(c: TabContent, baseLabel: string): string {
  switch (c.kind) {
    case 'terminal':
      return c.seq <= 1 ? `bash · ${baseLabel}` : `bash · ${baseLabel} (${c.seq})`
    case 'file':
      return c.rel.split('/').pop() || c.rel
    case 'tui':
      return `TUI · ${c.taskId.slice(0, 8)}`
    default:
      return '新建标签页'
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
npm --prefix web run test -- tabs && npm --prefix web run typecheck
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/app/workbench/tabs.ts web/src/app/workbench/tabs.test.ts
git commit -m "feat(web): tab 模型的纯函数层（三种 tab、跨组去重、分屏）"
```

---

## Task 5: useWorkbench —— 当前基准目录与按目录持有的 tab 组

**Files:**
- Create: `web/src/app/workbench/useWorkbench.ts`
- Test: `web/src/app/workbench/useWorkbench.test.ts`

**Interfaces:**
- Consumes: Task 4 的 `tabs.ts` 全部导出
- Produces:
  - `interface BaseDir { key: string; kind: 'workspace' | 'home'; path: string; label: string; projectName: string; machine: string }`
  - `const HOME_BASE: BaseDir`
  - `interface WorkbenchApi { base: BaseDir | null; wb: Workbench; select(b: BaseDir): void; open(c: TabContent, b?: BaseDir): void; openTerminal(b?: BaseDir): void; close(g: number, id: string): void; activate(g: number, id: string): void; setContent(g: number, id: string, c: TabContent): void; split(): void }`
  - `useWorkbench(): WorkbenchApi`

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/workbench/useWorkbench.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { HOME_BASE, useWorkbench, type BaseDir } from './useWorkbench'

const wsA: BaseDir = {
  key: '/home/dev/handoff',
  kind: 'workspace',
  path: '/home/dev/handoff',
  label: 'main',
  projectName: 'handoff',
  machine: '',
}
const wsB: BaseDir = {
  key: '/home/dev/.handoff/worktrees/w1',
  kind: 'workspace',
  path: '/home/dev/.handoff/worktrees/w1',
  label: 'w1',
  projectName: 'handoff',
  machine: '',
}

describe('useWorkbench', () => {
  it('初始未选中任何目录，tab 组为空', () => {
    const { result } = renderHook(() => useWorkbench())
    expect(result.current.base).toBeNull()
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
  })

  it('切目录时中央整组 tab 一起切换，切回来原样恢复', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'file', rel: 'go.mod' }))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)

    act(() => result.current.select(wsB))
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
    act(() => result.current.open({ kind: 'tui', taskId: 'T1' }))

    act(() => result.current.select(wsA))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
    expect(result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'file', rel: 'go.mod' })

    act(() => result.current.select(wsB))
    expect(result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('open 带基准目录参数时先切过去再开（左栏点任务、看板点卡片走这条）', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'tui', taskId: 'T9' }, wsB))
    expect(result.current.base?.key).toBe(wsB.key)
    expect(result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T9' })
  })

  it('openTerminal 自动取下一个序号', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.openTerminal())
    act(() => result.current.openTerminal())
    const seqs = result.current.wb.groups[0].tabs.map((t) =>
      t.content.kind === 'terminal' ? t.content.seq : -1,
    )
    expect(seqs).toEqual([1, 2])
  })

  it('home 是独立的一套 tab 组，与工作树互不干扰', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.openTerminal())
    act(() => result.current.openTerminal(HOME_BASE))
    expect(result.current.base?.kind).toBe('home')
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
    act(() => result.current.select(wsA))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
  })

  it('未选中目录时 open 是空操作，不静默造一个基准出来', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.open({ kind: 'file', rel: 'a.go' }))
    expect(result.current.base).toBeNull()
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- useWorkbench
```

Expected: FAIL，`Failed to resolve import "./useWorkbench"`。

- [ ] **Step 3: 写实现**

新建 `web/src/app/workbench/useWorkbench.ts`：

```ts
// useWorkbench —— 中央工作台的状态容器。
//
// 职责：
//   - 持有「当前基准目录」这一唯一的全局选中态（spec §1.2）
//   - 按基准目录分别持有 tab 组，切目录时整组一起换、切回来原样恢复
//   - 把 tabs.ts 的纯函数包成组件层能直接调的动作
//
// 边界：
//   - 不发请求、不认识 ProjectTree 的数据形状：调用方把选中的目录整理成
//     BaseDir 传进来
//   - 不做持久化。tab 组存内存，刷新即丢（spec §10）——持久化要处理
//     「目录被删了但 tab 还在」这类失效态，本期不值得
//
// 为什么按目录分别持有而不是一份全局 tab 列表：一份全局列表切目录时要么
// 全清空（用户丢工作现场），要么混在一起（左栏选了 A 却看见 B 的文件）。
// 按目录分持是唯一不产生第三种状态的做法。
import { useCallback, useRef, useState } from 'react'
import {
  EMPTY_WORKBENCH,
  activateTab,
  closeTab,
  nextTerminalSeq,
  openTab,
  setTabContent,
  splitGroup,
  type TabContent,
  type Workbench,
} from './tabs'

// BaseDir 是一个 tab 组的基准目录。
//
// key 是它在 Map 里的身份：工作树用绝对路径，home 用 '~'。
// label 是面包屑与 tab 标题里的短名——工作树优先用分支名（原型显示的是
// `integration/b2-b3` 这样的分支），没有分支（detached）时退回目录名。
export interface BaseDir {
  key: string
  kind: 'workspace' | 'home'
  path: string
  label: string
  projectName: string
  machine: string
}

// HOME_BASE 是悬浮按钮的基准：用户 home。
//
// 它进同一套 tab 系统（本计划的决定 1），所以也需要一个 BaseDir。
// path 是 '~' 而不是真实 home 路径：本期它只用于终端 tab 的标题，
// **不会**被发给任何后端接口——目录列举与读文件的白名单不为它放宽（spec §2.6）。
export const HOME_BASE: BaseDir = {
  key: '~',
  kind: 'home',
  path: '~',
  label: 'home',
  projectName: '',
  machine: '',
}

export interface WorkbenchApi {
  base: BaseDir | null
  wb: Workbench
  select: (b: BaseDir) => void
  open: (c: TabContent, b?: BaseDir) => void
  openTerminal: (b?: BaseDir) => void
  close: (group: number, tabId: string) => void
  activate: (group: number, tabId: string) => void
  setContent: (group: number, tabId: string, c: TabContent) => void
  split: () => void
}

export function useWorkbench(): WorkbenchApi {
  const [base, setBase] = useState<BaseDir | null>(null)
  const [byBase, setByBase] = useState<Record<string, Workbench>>({})
  // baseRef 让 open/openTerminal 在同一个事件里「先切基准再写它的 tab 组」时
  // 读到刚切过去的那个，而不是本次渲染闭包里的旧值
  const baseRef = useRef<BaseDir | null>(null)
  baseRef.current = base

  const wb = base ? (byBase[base.key] ?? EMPTY_WORKBENCH) : EMPTY_WORKBENCH

  const select = useCallback((b: BaseDir) => {
    baseRef.current = b
    setBase(b)
  }, [])

  // mutate 是所有写入的唯一通道：确定目标基准 → 取它的 Workbench → 应用纯函数。
  // 未选中任何基准且调用方也没给一个时，是空操作——静默造一个基准出来会让
  // 「tab 开在哪个目录下」变得不可解释。
  const mutate = useCallback(
    (fn: (w: Workbench) => Workbench, b?: BaseDir) => {
      const target = b ?? baseRef.current
      if (!target) return
      if (b && b.key !== baseRef.current?.key) select(b)
      setByBase((prev) => ({ ...prev, [target.key]: fn(prev[target.key] ?? EMPTY_WORKBENCH) }))
    },
    [select],
  )

  const open = useCallback(
    (c: TabContent, b?: BaseDir) => mutate((w) => openTab(w, c), b),
    [mutate],
  )

  const openTerminal = useCallback(
    (b?: BaseDir) => mutate((w) => openTab(w, { kind: 'terminal', seq: nextTerminalSeq(w) }), b),
    [mutate],
  )

  const close = useCallback((g: number, id: string) => mutate((w) => closeTab(w, g, id)), [mutate])
  const activate = useCallback((g: number, id: string) => mutate((w) => activateTab(w, g, id)), [mutate])
  const setContent = useCallback(
    (g: number, id: string, c: TabContent) => mutate((w) => setTabContent(w, g, id, c)),
    [mutate],
  )
  const split = useCallback(() => mutate(splitGroup), [mutate])

  return { base, wb, select, open, openTerminal, close, activate, setContent, split }
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
npm --prefix web run test -- useWorkbench && npm --prefix web run typecheck
```

Expected: 六个测试全 PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/app/workbench/useWorkbench.ts web/src/app/workbench/useWorkbench.test.ts
git commit -m "feat(web): 按基准目录持有 tab 组的工作台状态容器"
```

---

## Task 6: 中央区骨架 —— tab 条、空白 tab 与种类选择、全局空态

**Files:**
- Create: `web/src/app/workbench/TabBar.tsx`
- Create: `web/src/app/workbench/BlankTab.tsx`
- Create: `web/src/app/workbench/EmptyWorkbench.tsx`
- Create: `web/src/app/workbench/WorkbenchPage.tsx`
- Test: `web/src/app/workbench/WorkbenchPage.test.tsx`

**Interfaces:**
- Consumes: `tabs.ts`（`TabContent` / `Tab` / `tabTitle`）、`useWorkbench.ts`（`BaseDir` / `WorkbenchApi`）
- Produces:
  - `type PickKind = 'terminal' | 'file' | 'tui'`
  - `BlankTab({ base, onPick, hint?, onBack? })`，`onPick: (k: PickKind) => void`；`hint` 有值时面板下方多一行指路文案与「返回」按钮（`onBack`），用于「种类已选、目标待定」。面板上印的 ⌘T / ⌘⇧O / ⌘⇧A **是真接了的**（容器级 keydown，非 window 级）
  - `const PICK_HINT: Record<Exclude<PickKind, 'terminal'>, string>`
  - `TabBar({ group, tabs, activeId, baseLabel, onActivate, onClose, onNew })`
  - `EmptyWorkbench({ onAddProject })`
  - `WorkbenchPage({ api, onAddProject, renderContent })`，`renderContent: (c: TabContent, base: BaseDir) => ReactNode`
  - 常量 `PICK_ITEMS`（三项：新终端 ⌘T / 打开文件 ⌘⇧O / 打开任务 TUI ⌘⇧A）

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/workbench/WorkbenchPage.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { WorkbenchPage } from './WorkbenchPage'
import { BlankTab } from './BlankTab'
import type { BaseDir, WorkbenchApi } from './useWorkbench'
import { EMPTY_WORKBENCH, openTab } from './tabs'

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'b2-b3',
  projectName: 'handoff',
  machine: '',
}

function api(overrides: Partial<WorkbenchApi> = {}): WorkbenchApi {
  return {
    base,
    wb: EMPTY_WORKBENCH,
    select: vi.fn(),
    open: vi.fn(),
    openTerminal: vi.fn(),
    close: vi.fn(),
    activate: vi.fn(),
    setContent: vi.fn(),
    split: vi.fn(),
    ...overrides,
  }
}

describe('BlankTab', () => {
  it('列出三项且只有三项：新终端 / 打开文件 / 打开任务 TUI', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /打开文件/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /打开任务 TUI/ })).toBeInTheDocument()
    expect(screen.getAllByRole('button')).toHaveLength(3)
  })

  it('带快捷键提示', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByText('⌘T')).toBeInTheDocument()
    expect(screen.getByText('⌘⇧O')).toBeInTheDocument()
    expect(screen.getByText('⌘⇧A')).toBeInTheDocument()
  })

  it('home 基准下只有新终端一项（spec §2.6）', () => {
    const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }
    render(<BlankTab base={home} onPick={vi.fn()} />)
    expect(screen.getAllByRole('button')).toHaveLength(1)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
  })

  it('显示基准目录，让人知道这个 tab 会开在哪', () => {
    render(<BlankTab base={base} onPick={vi.fn()} />)
    expect(screen.getByText(/b2-b3/)).toBeInTheDocument()
  })

  it('点一项回调对应种类', () => {
    const onPick = vi.fn()
    render(<BlankTab base={base} onPick={onPick} />)
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(onPick).toHaveBeenCalledWith('terminal')
  })

  it('印在面板上的快捷键是真能按的（⌘T / ⌘⇧O / ⌘⇧A）', () => {
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={base} onPick={onPick} />)
    const panel = container.firstElementChild as HTMLElement
    fireEvent.keyDown(panel, { key: 't', metaKey: true })
    fireEvent.keyDown(panel, { key: 'o', metaKey: true, shiftKey: true })
    fireEvent.keyDown(panel, { key: 'a', metaKey: true, shiftKey: true })
    expect(onPick.mock.calls.map((c) => c[0])).toEqual(['terminal', 'file', 'tui'])
  })

  it('home 基准下 ⌘⇧O 不生效——隐藏项不能被快捷键绕过', () => {
    const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={home} onPick={onPick} />)
    fireEvent.keyDown(container.firstElementChild as HTMLElement, { key: 'o', metaKey: true, shiftKey: true })
    expect(onPick).not.toHaveBeenCalled()
  })

  it('没按 meta 的普通输入不被吞掉', () => {
    const onPick = vi.fn()
    const { container } = render(<BlankTab base={base} onPick={onPick} />)
    fireEvent.keyDown(container.firstElementChild as HTMLElement, { key: 't' })
    expect(onPick).not.toHaveBeenCalled()
  })

  it('hint 非空时换成指路 + 返回选择，不再列三项', () => {
    render(<BlankTab base={base} onPick={vi.fn()} hint="在右侧文件树里点一个文件" onBack={vi.fn()} />)
    expect(screen.getByText('在右侧文件树里点一个文件')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
  })

  it('点返回选择回到三项', () => {
    const onBack = vi.fn()
    render(<BlankTab base={base} onPick={vi.fn()} hint="随便什么提示" onBack={onBack} />)
    fireEvent.click(screen.getByRole('button', { name: '返回选择' }))
    expect(onBack).toHaveBeenCalled()
  })
})

describe('WorkbenchPage', () => {
  it('未选中目录时显示全局空态而不是死空白', () => {
    render(
      <WorkbenchPage
        api={api({ base: null })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    expect(screen.getByText(/从侧边栏选择一个目录开始/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /添加项目/ })).toBeInTheDocument()
  })

  it('选中目录但没有 tab 时，中央仍然给出可用起点（种类选择）', () => {
    render(<WorkbenchPage api={api()} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
  })

  it('点 + 开一个空白 tab', () => {
    const open = vi.fn()
    render(<WorkbenchPage api={api({ open })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))
    expect(open).toHaveBeenCalledWith({ kind: 'blank' })
  })

  it('渲染 tab 条与激活 tab 的内容', () => {
    const wb = openTab(openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a/go.mod' }), {
      kind: 'tui',
      taskId: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad',
    })
    render(
      <WorkbenchPage
        api={api({ wb })}
        onAddProject={vi.fn()}
        renderContent={(c) => <div>渲染:{c.kind}</div>}
      />,
    )
    expect(screen.getByRole('tab', { name: /go.mod/ })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /TUI · 7ec762e7/ })).toBeInTheDocument()
    expect(screen.getByText('渲染:tui')).toBeInTheDocument()
    expect(screen.queryByText('渲染:file')).not.toBeInTheDocument()
  })

  it('点 tab 激活它，点关闭按钮关掉它', () => {
    const activate = vi.fn()
    const close = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a/go.mod' })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb, activate, close })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('tab', { name: /go.mod/ }))
    expect(activate).toHaveBeenCalledWith(0, id)
    fireEvent.click(screen.getByRole('button', { name: /关闭 go.mod/ }))
    expect(close).toHaveBeenCalledWith(0, id)
  })

  it('两组时左右并排，各有自己的 tab 条', () => {
    let wb = openTab(EMPTY_WORKBENCH, { kind: 'file', rel: 'a.go' })
    wb = { ...wb, groups: [...wb.groups, { tabs: [], activeId: null }], active: 1 }
    render(<WorkbenchPage api={api({ wb })} onAddProject={vi.fn()} renderContent={() => <div>内容</div>} />)
    expect(screen.getAllByRole('tablist')).toHaveLength(2)
  })

  it('空白 tab 选了种类后调 setContent 而不是再开一个 tab', () => {
    const setContent = vi.fn()
    const open = vi.fn()
    const wb = openTab(EMPTY_WORKBENCH, { kind: 'blank' })
    const id = wb.groups[0].tabs[0].id
    render(
      <WorkbenchPage
        api={api({ wb, setContent, open })}
        onAddProject={vi.fn()}
        renderContent={() => <div>内容</div>}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(setContent).toHaveBeenCalledWith(0, id, { kind: 'terminal', seq: 1 })
    expect(open).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- WorkbenchPage
```

Expected: FAIL，模块解析失败。

- [ ] **Step 3: 写 BlankTab**

新建 `web/src/app/workbench/BlankTab.tsx`：

```tsx
// BlankTab —— 空白 tab 的种类选择面板（spec §2.2.1）。
//
// 职责：把「只有三种 tab」这条规则做成用户看得见的形态——新开一个 tab 默认
// 空白，中间列出可选种类，选中后该 tab 才变成对应内容。
//
// 边界：
//   - 不自己开 tab，只回调选中的种类。是 setContent（原地变）还是 open（新开），
//     由调用方决定
//   - 不做目标选择（选哪个文件、哪个任务）。那是选中种类之后的第二步，
//     由 WorkbenchPage 接管
//
// 为什么中央区在没有 tab 时也渲染它：中央区域一块死掉的空白会让人以为
// 「这里还没做好」，而它其实是整个工作台的起点。
import { FileText, Bot, TerminalSquare } from 'lucide-react'
import type { BaseDir } from './useWorkbench'

// PickKind 是用户能选的三种 tab。与 TabContent 的三种正式种类一一对应。
export type PickKind = 'terminal' | 'file' | 'tui'

// PICK_ITEMS 是选择面板的三项。顺序与原型/Orca 一致：终端在最上（最常用）。
export const PICK_ITEMS: { kind: PickKind; label: string; hotkey: string; icon: typeof TerminalSquare }[] = [
  { kind: 'terminal', label: '新终端', hotkey: '⌘T', icon: TerminalSquare },
  { kind: 'file', label: '打开文件', hotkey: '⌘⇧O', icon: FileText },
  { kind: 'tui', label: '打开任务 TUI', hotkey: '⌘⇧A', icon: Bot },
]

export interface BlankTabProps {
  base: BaseDir
  onPick: (k: PickKind) => void
  // hint 非空表示「种类已选好，但目标还没选」。此时换成一句指路 + 返回按钮。
  // 这个中间态**不进 TabContent**：TabContent 只有三种正式种类加一个 blank，
  // 多一支就等于承认了第四种 tab，与 spec 的硬约束冲突
  hint?: string
  onBack?: () => void
}

// hotkeyOf 把一次按键映射成一种 tab。返回 null = 与本面板无关，交给浏览器。
//
// 为什么用 metaKey 而不区分平台：控制台目前只在 macOS 的桌面壳里用，面板上印的
// 也是 ⌘。将来上 Windows 时这里要一起改成 metaKey || ctrlKey，那时面板文案也得改，
// 两处必须同时动——所以刻意不在这里提前做半套。
function hotkeyOf(e: React.KeyboardEvent): PickKind | null {
  if (!e.metaKey) return null
  const k = e.key.toLowerCase()
  if (k === 't' && !e.shiftKey) return 'terminal'
  if (k === 'o' && e.shiftKey) return 'file'
  if (k === 'a' && e.shiftKey) return 'tui'
  return null
}

export function BlankTab({ base, onPick, hint, onBack }: BlankTabProps) {
  // home 基准只留终端：以 $HOME 为根浏览文件是安全边界的实质放宽，本期不做
  // （spec §2.6 —— ~/.handoff/config.yaml 里存着 agentd 主令牌）
  const items = base.kind === 'home' ? PICK_ITEMS.filter((i) => i.kind === 'terminal') : PICK_ITEMS

  // 快捷键接在**这个面板自己身上**（容器可聚焦 + 挂载自动聚焦），不是 window 级监听。
  // 理由：分屏时可能有两个空白面板同时在屏上，window 级监听会让一次 ⌘T 开出两个终端；
  // 而印在面板上的提示如果按了没反应，就是一句 UI 说了不算的话（与「不置灰」同源）。
  const onKeyDown = (e: React.KeyboardEvent) => {
    const kind = hotkeyOf(e)
    if (kind === null) return
    // home 基准下只有终端可用，别让隐藏项被快捷键绕过
    if (!items.some((i) => i.kind === kind)) return
    e.preventDefault()
    onPick(kind)
  }
  if (hint) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
        <p className="max-w-sm text-sm text-muted-foreground">{hint}</p>
        <button type="button" onClick={onBack} className="rounded-md border px-3 py-1.5 text-sm hover:bg-accent">
          返回选择
        </button>
      </div>
    )
  }
  return (
    <div
      // eslint-disable-next-line jsx-a11y/no-noninteractive-element-to-interactive-role
      tabIndex={-1}
      autoFocus
      onKeyDown={onKeyDown}
      className="flex h-full flex-col items-center justify-center gap-4 p-8 outline-none"
    >
      <p className="text-xs text-muted-foreground">
        基准目录 <span className="font-mono text-foreground">{base.label}</span>
        {base.kind === 'home' && '（不挂在任何项目上）'}
      </p>
      <ul className="flex w-full max-w-xs flex-col gap-1">
        {items.map((item) => (
          <li key={item.kind}>
            <button
              type="button"
              onClick={() => onPick(item.kind)}
              className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm hover:bg-accent"
            >
              <item.icon className="size-4 shrink-0 text-muted-foreground" />
              <span className="flex-1">{item.label}</span>
              <span className="font-mono text-[11px] text-muted-foreground">{item.hotkey}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}
```

- [ ] **Step 4: 写 TabBar 与 EmptyWorkbench**

新建 `web/src/app/workbench/TabBar.tsx`：

```tsx
// TabBar —— 一组 tab 的标签条。
//
// 职责：渲染标签、标出激活项、提供关闭与「新建标签页」。
// 边界：不持有状态，全部经回调上抛；不认识 tab 内容的语义，标题由 tabTitle 算。
import { Plus, X } from 'lucide-react'
import { tabTitle, type Tab } from './tabs'
import { cn } from '@/lib/utils'

export interface TabBarProps {
  group: number
  tabs: Tab[]
  activeId: string | null
  baseLabel: string
  onActivate: (group: number, tabId: string) => void
  onClose: (group: number, tabId: string) => void
  onNew: (group: number) => void
}

export function TabBar({ group, tabs, activeId, baseLabel, onActivate, onClose, onNew }: TabBarProps) {
  return (
    <div role="tablist" className="flex min-h-9 items-stretch border-b bg-background">
      {tabs.map((t) => {
        const title = tabTitle(t.content, baseLabel)
        const active = t.id === activeId
        return (
          <div key={t.id} className={cn('group flex items-center border-r', active && 'bg-muted/60')}>
            <button
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => onActivate(group, t.id)}
              className="max-w-48 truncate px-3 py-1.5 text-[13px]"
            >
              {title}
            </button>
            <button
              type="button"
              aria-label={`关闭 ${title}`}
              onClick={() => onClose(group, t.id)}
              className="mr-1 rounded p-0.5 text-muted-foreground opacity-0 hover:bg-accent group-hover:opacity-100"
            >
              <X className="size-3.5" />
            </button>
          </div>
        )
      })}
      <button
        type="button"
        aria-label="新建标签页"
        onClick={() => onNew(group)}
        className="flex items-center px-2 text-muted-foreground hover:text-foreground"
      >
        <Plus className="size-4" />
      </button>
    </div>
  )
}
```

新建 `web/src/app/workbench/EmptyWorkbench.tsx`：

```tsx
// EmptyWorkbench —— 一个目录都没选中时的全局空态（spec §2.2.1）。
//
// 职责：给出下一步该做什么，而不是一块空白。
// 边界：不渲染任何要求「先选目录」才有意义的入口。
import { Plus } from 'lucide-react'

export function EmptyWorkbench({ onAddProject }: { onAddProject: () => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      <span className="flex size-10 items-center justify-center rounded-xl bg-[#10151b] text-base font-semibold text-white">
        h
      </span>
      <p className="text-sm text-muted-foreground">从侧边栏选择一个目录开始</p>
      <button
        type="button"
        onClick={onAddProject}
        className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-accent"
      >
        <Plus className="size-4" />
        添加项目
      </button>
      {/* 这里的快捷键是「选中目录之后能用什么」的预告：没有基准目录时它们无处可去，
          所以写成「选中目录后」，而不是印一行按了没反应的键 */}
      <p className="font-mono text-[11px] text-muted-foreground">
        选中目录后：⌘T 新终端 · ⌘⇧O 打开文件 · ⌘⇧A 打开任务 TUI
      </p>
    </div>
  )
}
```

- [ ] **Step 5: 写 WorkbenchPage**

新建 `web/src/app/workbench/WorkbenchPage.tsx`：

```tsx
// WorkbenchPage —— 中央内容承载区。
//
// 职责：
//   - 按当前基准目录渲染一组或两组 tab（tab 条 + 激活 tab 的内容）
//   - 空白 tab 的种类选择、以及「选了种类之后要不要再选目标」的分流
//   - 没有 tab / 没有基准目录时的两种空态
//
// 边界：
//   - 不认识任何一种 tab 的具体内容：renderContent 由 Shell 注入，
//     这样中央区的布局与「终端/文件/TUI 各自怎么画」互不牵连
//   - 不持有状态：全部经 WorkbenchApi
//
// 「选了种类之后」的分流（spec §2.2.1）：
//   - 终端：直接就位，序号由 tabs.ts 算
//   - 文件：需要再选一个文件。本期不做独立的文件选择器——右栏文件树就是
//     那个选择器，所以这里给一句指路，不造第二个入口
//   - 任务 TUI：需要再选一个任务。同理指向左栏该目录下的任务行
import { useState, type ReactNode } from 'react'
import { nextTerminalSeq, type TabContent } from './tabs'
import { BlankTab, type PickKind } from './BlankTab'
import { EmptyWorkbench } from './EmptyWorkbench'
import { TabBar } from './TabBar'
import type { BaseDir, WorkbenchApi } from './useWorkbench'

export interface WorkbenchPageProps {
  api: WorkbenchApi
  onAddProject: () => void
  renderContent: (content: TabContent, base: BaseDir) => ReactNode
}

// PICK_HINT 是「种类选好了但还缺一个目标」时的指路文案。
//
// 为什么只指路不弹选择器：右栏文件树本身就是文件选择器，左栏任务行本身就是
// 任务选择器。再造一个模态选择器等于同一件事有两个入口，而且那个入口还得
// 自己再实现一遍目录列举与任务列表。
export const PICK_HINT: Record<Exclude<PickKind, 'terminal'>, string> = {
  file: '在右侧文件树里点一个文件，它会在这里打开。',
  tui: '在左侧该目录下点一个任务，它的 TUI 会在这里打开。',
}

export function WorkbenchPage({ api, onAddProject, renderContent }: WorkbenchPageProps) {
  const { base, wb } = api
  // awaiting 记「哪个空白 tab 已经选了种类、正在等目标」。
  // 它是本组件的本地 UI 状态，**不进 TabContent**：TabContent 多一支就等于
  // 承认了第四种 tab，与「只有三种 tab」的硬约束冲突。tab 被关掉后这里会残留
  // 一条键，无害——下一个 tab 的 id 是新的，不会命中。
  const [awaiting, setAwaiting] = useState<Record<string, Exclude<PickKind, 'terminal'>>>({})

  if (!base) return <EmptyWorkbench onAddProject={onAddProject} />

  // 选了种类之后：终端直接就位；文件与 TUI 缺目标，把该 tab 停在提示态
  const pick = (group: number, tabId: string, kind: PickKind) => {
    if (kind === 'terminal') {
      api.setContent(group, tabId, { kind: 'terminal', seq: nextTerminalSeq(wb) })
      return
    }
    setAwaiting((prev) => ({ ...prev, [tabId]: kind }))
  }

  const back = (tabId: string) =>
    setAwaiting((prev) => {
      const next = { ...prev }
      delete next[tabId]
      return next
    })

  // startFromEmpty 处理「组里一个 tab 都没有」时直接在空态面板上选种类：
  // 此时没有可原地改内容的 tab，终端直接开一个，其余先开一个空白 tab
  // 承接（用户随即会在它上面看到指路）。
  const startFromEmpty = (kind: PickKind) => {
    if (kind === 'terminal') {
      api.openTerminal(base)
      return
    }
    api.open({ kind: 'blank' })
  }

  return (
    <div className="flex h-full min-h-0 gap-px bg-border">
      {wb.groups.map((g, gi) => {
        const activeTab = g.tabs.find((t) => t.id === g.activeId) ?? null
        return (
          <section key={gi} className="flex min-w-0 flex-1 flex-col bg-background">
            <TabBar
              group={gi}
              tabs={g.tabs}
              activeId={g.activeId}
              baseLabel={base.label}
              onActivate={api.activate}
              onClose={api.close}
              onNew={() => api.open({ kind: 'blank' })}
            />
            <div className="min-h-0 flex-1 overflow-auto">
              {activeTab === null ? (
                <BlankTab base={base} onPick={startFromEmpty} />
              ) : activeTab.content.kind === 'blank' ? (
                <BlankTab
                  base={base}
                  onPick={(k) => pick(gi, activeTab.id, k)}
                  hint={awaiting[activeTab.id] ? PICK_HINT[awaiting[activeTab.id]] : undefined}
                  onBack={() => back(activeTab.id)}
                />
              ) : (
                renderContent(activeTab.content, base)
              )}
            </div>
          </section>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 6: 跑测试确认通过**

```bash
npm --prefix web run test -- WorkbenchPage && npm --prefix web run typecheck && npm --prefix web run lint
```

Expected: 全绿，且 `TabContent` 没有出现第四支。

- [ ] **Step 7: 提交**

```bash
git add web/src/app/workbench/
git commit -m "feat(web): 中央 tab 条、空白 tab 的种类选择与两种空态"
```

---

## Task 7: 终端 tab（诚实占位）与文件 tab（只读查看）

**Files:**
- Create: `web/src/app/workbench/TerminalTab.tsx`
- Create: `web/src/app/workbench/FileTab.tsx`
- Test: `web/src/app/workbench/FileTab.test.tsx`

**Interfaces:**
- Consumes: Task 3 的 `fetchWorkspaceFile`；Task 5 的 `BaseDir`
- Produces:
  - `TerminalTab({ base, seq })`
  - `FileTab({ base, rel })`

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/workbench/FileTab.test.tsx`：

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { FileTab } from './FileTab'
import { TerminalTab } from './TerminalTab'
import type { BaseDir } from './useWorkbench'
import { ApiError } from '../../api/client'

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'b2-b3',
  projectName: 'handoff',
  machine: 'devbox',
}

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchWorkspaceFile: vi.fn() }
})
const { fetchWorkspaceFile } = await import('../../api/client')

afterEach(() => vi.mocked(fetchWorkspaceFile).mockReset())

describe('TerminalTab', () => {
  it('明说 PTY 未实现并给出当前可用的替代路径，不置灰任何按钮', () => {
    render(<TerminalTab base={base} seq={1} />)
    expect(screen.getByText(/PTY 后端尚未实现/)).toBeInTheDocument()
    expect(screen.getByText(/handoff attach/)).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})

describe('FileTab', () => {
  it('按基准目录 + 相对路径 + 机器名取文件并显示内容', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'module handoff\n' })
    render(<FileTab base={base} rel="go.mod" />)
    await waitFor(() => expect(screen.getByText(/module handoff/)).toBeInTheDocument())
    expect(fetchWorkspaceFile).toHaveBeenCalledWith('/w/b2-b3', 'go.mod', 'devbox')
  })

  it('加载中先给出提示而不是空白', () => {
    vi.mocked(fetchWorkspaceFile).mockReturnValue(new Promise(() => {}))
    render(<FileTab base={base} rel="go.mod" />)
    expect(screen.getByText(/正在读取/)).toBeInTheDocument()
  })

  it('agentd 的中文错误原文透传，不吞成「操作失败」', async () => {
    vi.mocked(fetchWorkspaceFile).mockRejectedValue(new ApiError(403, '路径不在已探测到的工作树白名单内'))
    render(<FileTab base={base} rel="../../etc/passwd" />)
    await waitFor(() =>
      expect(screen.getByText(/路径不在已探测到的工作树白名单内/)).toBeInTheDocument(),
    )
  })

  it('本期只读：不渲染保存按钮，且明示只读', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'x' })
    render(<FileTab base={base} rel="a.txt" />)
    await waitFor(() => expect(screen.getByText('x')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /保存/ })).not.toBeInTheDocument()
    expect(screen.getByText(/只读/)).toBeInTheDocument()
  })

  it('换文件时重新取数', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'a' })
    const { rerender } = render(<FileTab base={base} rel="a.txt" />)
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument())
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'b' })
    rerender(<FileTab base={base} rel="b.txt" />)
    await waitFor(() => expect(screen.getByText('b')).toBeInTheDocument())
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- FileTab
```

Expected: FAIL，模块解析失败。

- [ ] **Step 3: 写 TerminalTab**

新建 `web/src/app/workbench/TerminalTab.tsx`：

```tsx
// TerminalTab —— 终端 tab 的壳（spec §2.4）。
//
// 职责：把「终端」这一 tab 种类的外形做到位——能开、能命名、能关、能参与分屏，
// 内容区诚实说明 PTY 后端尚未实现，并给出当前真正可用的替代路径。
//
// 边界：
//   - 不连 PTY、不渲染 xterm。本期的目标是形态校准，不是把终端跑通
//   - **不渲染置灰按钮**（W3b §0 既有纪律）：置灰控件承诺「以后能用」，
//     用户会反复点它。这里干脆不放控件，只放一句说明
//
// 为什么要把一个空壳做出来：三种 tab 里少任何一种，「中央区是一个 tab 系统」
// 这件事就没法在真实页面上验证——而这一期交付的正是那个判断。
import { TerminalSquare } from 'lucide-react'
import type { BaseDir } from './useWorkbench'

export function TerminalTab({ base, seq }: { base: BaseDir; seq: number }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <TerminalSquare className="size-3.5" />
        <span className="font-mono">
          bash · {base.label}
          {seq > 1 && ` (${seq})`}
        </span>
        <span className="ml-auto font-mono">{base.path}</span>
      </div>
      <div className="flex flex-1 items-center justify-center p-8">
        <div className="max-w-md space-y-2 text-center">
          <p className="text-sm">PTY 后端尚未实现。</p>
          <p className="text-xs text-muted-foreground">
            当前查看 executor 现场请用 <code className="font-mono">handoff attach &lt;task&gt;</code>。
          </p>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 写 FileTab**

新建 `web/src/app/workbench/FileTab.tsx`：

```tsx
// FileTab —— 只读查看基准目录下的一个文件（spec §2.2）。
//
// 职责：取 GET /api/workspaces/file 并把正文原样显示。
//
// 边界：
//   - **只读**。写入与在线编辑不在本期（spec §0），所以这里不放保存按钮，
//     而是在头部明确写「只读」——不置灰、不给假承诺
//   - 不做语法高亮：本期判据是「点右侧文件能在中间打开」，高亮不影响这个判断，
//     而引一个高亮库会把包体和首屏都拖下去
//   - 不缓存。切走再切回来重新取一次，代价小于「文件已变但页面还是旧的」
//
// 错误处理：agentd 的中文错误原文原样透传（诚实展示纪律），不吞成「操作失败」。
import { useEffect, useState } from 'react'
import { fetchWorkspaceFile } from '../../api/client'
import { errorMessage } from '../lib/format'
import type { BaseDir } from './useWorkbench'

export function FileTab({ base, rel }: { base: BaseDir; rel: string }) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    // cancelled 防止「快速连点两个文件」时先发的请求后到，把后选的内容盖掉
    let cancelled = false
    setContent(null)
    setError(null)
    fetchWorkspaceFile(base.path, rel, base.machine || undefined)
      .then((r) => {
        if (!cancelled) setContent(r.content)
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [base.path, base.machine, rel])

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <span className="truncate font-mono text-foreground">{rel}</span>
        <span className="ml-auto shrink-0 rounded border px-1.5 py-0.5">只读</span>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {error !== null ? (
          <p className="p-4 text-sm text-destructive">{error}</p>
        ) : content === null ? (
          <p className="p-4 text-sm text-muted-foreground">正在读取 {rel}…</p>
        ) : (
          <pre className="p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap">{content}</pre>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
npm --prefix web run test -- FileTab && npm --prefix web run typecheck
```

Expected: 六个测试全 PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/app/workbench/TerminalTab.tsx web/src/app/workbench/FileTab.tsx web/src/app/workbench/FileTab.test.tsx
git commit -m "feat(web): 终端 tab 壳与只读文件 tab"
```

---

## Task 8: 把任务会话编排提取为 useTaskSession，TUI tab 装真内容，删除 TaskPage

**Files:**
- Create: `web/src/app/task/useTaskSession.ts`
- Create: `web/src/app/workbench/TuiTab.tsx`
- Delete: `web/src/app/task/TaskPage.tsx`
- Test: `web/src/app/task/useTaskSession.test.ts`

**Interfaces:**
- Consumes: `fetchTaskDetail` / `replyTicket` / `ApiError`（`api/client`）、`connectEvents` / `WsStatus`（`api/ws`）、既有面板 `TimelinePanel` / `EventsPanel` / `AdvanceActions` / `ReviewPanel` / `TaskHeader`
- Produces:
  - `interface TaskSession { detail: TaskDetail | null; events: Event[]; wsStatus: WsStatus; wsError: string | null; loadError: string | null; disconnected: boolean; disconnectReason: string; sessionExpired: boolean; refresh: () => void; replyToTicket: (t: Ticket, answer: string) => Promise<void> }`
  - `useTaskSession(id: string | undefined): TaskSession`
  - `TuiTab({ taskId })`

**背景（实现者必读）**：`TaskPage.tsx` 现在同时干两件事——数据编排（轮询 + WS + 应答）和页面布局（含它自己的 `<header>` 与返回看板链接）。新 IA 里布局归 tab，页头归面包屑，只有编排要留下来。所以这一步是**纯搬运 + 去掉外框**，不改任何数据语义：轮询间隔、`[id, seeded]` 依赖、closeCode 1008 的终止态处理、401 收敛，全部照抄。**照抄时连注释一起搬**——那些注释解释的是「为什么依赖数组是 `[id, seeded]` 而不是加上 detail」这类踩过坑的决定。

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/task/useTaskSession.test.ts`：

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { ApiError } from '../../api/client'
import type { TaskDetail } from '../../api/types'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchTaskDetail: vi.fn(), replyTicket: vi.fn() }
})
vi.mock('../../api/ws', () => ({
  connectEvents: vi.fn(() => ({ close: vi.fn() })),
}))

const { fetchTaskDetail, replyTicket } = await import('../../api/client')
const { connectEvents } = await import('../../api/ws')
const { useTaskSession } = await import('./useTaskSession')

function detailOf(state: string): TaskDetail {
  return {
    task: { id: 'T1', name: 'demo', state, branch: 'x', repo: '/r', executor: 'opencode' } as TaskDetail['task'],
    pending_tickets: [],
    recent_events: [{ seq: 7, task_id: 'T1', type: 'completed', ts: '2026-08-12T00:00:00Z', payload: {} }],
  } as unknown as TaskDetail
}

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
afterEach(() => {
  vi.useRealTimers()
  vi.mocked(fetchTaskDetail).mockReset()
  vi.mocked(replyTicket).mockReset()
  vi.mocked(connectEvents).mockClear()
})

describe('useTaskSession', () => {
  it('首拉后以 recent_events 打底，并以其最大 seq 为 WS 起点', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.detail).not.toBeNull())
    expect(result.current.events.map((e) => e.seq)).toEqual([7])
    await waitFor(() => expect(connectEvents).toHaveBeenCalled())
    expect(vi.mocked(connectEvents).mock.calls[0][0]).toMatchObject({ taskId: 'T1', fromSeq: 7 })
  })

  it('4s 轮询续拉详情', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(fetchTaskDetail).toHaveBeenCalledTimes(1))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000)
    })
    expect(fetchTaskDetail).toHaveBeenCalledTimes(2)
  })

  it('首拉失败落终止态 loadError；已有数据后再失败只标已断开并保留数据', async () => {
    vi.mocked(fetchTaskDetail).mockRejectedValueOnce(new ApiError(500, 'agentd 挂了'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.loadError).toBe('agentd 挂了'))
    expect(result.current.disconnected).toBe(false)

    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.detail).not.toBeNull())

    vi.mocked(fetchTaskDetail).mockRejectedValue(new ApiError(500, '连接被拒'))
    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.disconnected).toBe(true))
    expect(result.current.disconnectReason).toBe('连接被拒')
    expect(result.current.detail).not.toBeNull() // 保留最后拿到的数据
  })

  it('401 收敛到会话已失效并停轮询', async () => {
    vi.mocked(fetchTaskDetail).mockRejectedValue(new ApiError(401, '会话已失效'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.sessionExpired).toBe(true))
    const calls = vi.mocked(fetchTaskDetail).mock.calls.length
    await act(async () => {
      await vi.advanceTimersByTimeAsync(12000)
    })
    expect(vi.mocked(fetchTaskDetail).mock.calls.length).toBe(calls)
  })

  it('WS 的 onTerminal 同样落会话已失效', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(connectEvents).toHaveBeenCalled())
    act(() => vi.mocked(connectEvents).mock.calls[0][0].onTerminal?.())
    expect(result.current.sessionExpired).toBe(true)
  })

  it('应答工单后立即补拉', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('waiting_answer'))
    vi.mocked(replyTicket).mockResolvedValue({ ok: true } as never)
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.detail).not.toBeNull())
    const before = vi.mocked(fetchTaskDetail).mock.calls.length
    await act(async () => {
      await result.current.replyToTicket({ id: 'K1' } as never, '批了')
    })
    expect(replyTicket).toHaveBeenCalledWith('T1', { ticket_id: 'K1', answer: '批了' })
    expect(vi.mocked(fetchTaskDetail).mock.calls.length).toBeGreaterThan(before)
  })

  it('id 为空时什么都不做', () => {
    renderHook(() => useTaskSession(undefined))
    expect(fetchTaskDetail).not.toHaveBeenCalled()
    expect(connectEvents).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- useTaskSession
```

Expected: FAIL，`Failed to resolve import "./useTaskSession"`。

- [ ] **Step 3: 写 useTaskSession（从 TaskPage 平移编排）**

新建 `web/src/app/task/useTaskSession.ts`。把 `TaskPage.tsx:19-168`（`useState` 声明、`refreshDetail`、两个 `useEffect`、`replyToTicket`）**原样搬进来**，只做三处改动：① 参数从 `useParams()` 改为入参 `id`；② 末尾 `return` 一个 `TaskSession` 对象；③ 文件头注释重写为下面这段。

```ts
// useTaskSession —— 一个任务会话的数据编排（原 TaskPage 的上半截）。
//
// 职责：
//   - GET /api/tasks/{id} 轮询详情（任务 + 挂起工单，4s 一次），页面隐藏时暂停
//   - 首拉时以 recent_events 打底事件流，然后开**一条** /ws/events?task=<id>
//     &from_seq=<最大 seq> 收实时增量（WS 层自己推进游标，重连不重放）
//   - 工单应答（POST reply）并在成功后立即补拉
//
// 边界：
//   - 不渲染任何东西。W4 把它从 TaskPage 里提出来，是因为同一份会话要同时
//     喂给 TUI tab 与（未来的）其他消费者；留在页面组件里必然要复制一份
//   - 不管 frames / render 流：那两条在 TimelinePanel / RenderPanel 内部自管，
//     且互斥（任一时刻只开一条）
//
// 断线语义（硬契约，照搬不得放宽）：
//   - 断线保留最后拿到的数据继续显示，所有会改状态的按钮禁用，标注「已断开」；
//     不称为「只读」——只读暗示数据是新的，而它不是
//   - WS close code 1008（会话被吊销）落到「会话已失效」终止态，不无脑重连；
//     HTTP 401 同样收敛到终止态并停轮询
//
// cursor 归属：浏览器不碰 ~/.handoff/cursor-*（那是 CLI 审核者的本机游标账本），
// 这里从 from_seq=0 或已知最大 seq 续，是观察者；与 CLI 同时盯同一任务互不干扰。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, fetchTaskDetail, replyTicket } from '../../api/client'
import type { Event, TaskDetail, Ticket } from '../../api/types'
import { connectEvents, type WsStatus } from '../../api/ws'
import { errorMessage } from '../lib/format'

// DETAIL_POLL_INTERVAL 是详情轮询间隔：任务状态与挂起工单靠它保鲜。
const DETAIL_POLL_INTERVAL = 4000

export interface TaskSession {
  detail: TaskDetail | null
  events: Event[]
  wsStatus: WsStatus
  wsError: string | null
  loadError: string | null
  disconnected: boolean
  disconnectReason: string
  sessionExpired: boolean
  refresh: () => void
  replyToTicket: (ticket: Ticket, answer: string) => Promise<void>
}

export function useTaskSession(id: string | undefined): TaskSession {
  const [detail, setDetail] = useState<TaskDetail | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [disconnected, setDisconnected] = useState(false)
  const [disconnectReason, setDisconnectReason] = useState('')
  const [sessionExpired, setSessionExpired] = useState(false)
  const [events, setEvents] = useState<Event[]>([])
  const [wsStatus, setWsStatus] = useState<WsStatus>('connecting')
  const [wsError, setWsError] = useState<string | null>(null)

  const initializedRef = useRef(false)
  const sessionExpiredRef = useRef(false)
  // 事件打底与 WS 起点：首拉后把 recent_events 及其最大 seq 记进 ref，
  // WS 订阅（单独 effect）以 [id, seeded] 为依赖，只在新任务/首拉完成时重建。
  const initialEventsRef = useRef<Event[]>([])
  const initialSeqRef = useRef(0)
  const [seeded, setSeeded] = useState(false)

  // refreshDetail 拉一次详情并合并进状态。首拉时以 recent_events 打底事件流，
  // 之后轮询只刷新 task / pending_tickets（事件增量归 WS）。
  const refreshDetail = useCallback(async () => {
    if (!id || sessionExpiredRef.current) return
    try {
      const d = await fetchTaskDetail(id)
      if (!initializedRef.current) {
        initializedRef.current = true
        initialEventsRef.current = d.recent_events
        initialSeqRef.current = d.recent_events.reduce((m, e) => Math.max(m, e.seq), 0)
        setSeeded(true)
      }
      setDetail(d)
      setLoadError(null)
      setDisconnected(false)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        sessionExpiredRef.current = true
        setSessionExpired(true)
        return
      }
      const msg = errorMessage(err)
      if (!initializedRef.current) {
        setLoadError(msg) // 还没有任何可显示的数据：终止态 + 重试
      } else {
        setDisconnected(true) // 已有数据：保留显示，标注已断开
        setDisconnectReason(msg)
      }
    }
  }, [id])

  // 详情轮询循环：立即首拉 → 定时续拉；页面隐藏停表、可见恢复并立即补拉。
  useEffect(() => {
    if (!id) return
    // 换任务（同一 hook 实例换 id）时重置首拉标记与会话失效态
    initializedRef.current = false
    sessionExpiredRef.current = false
    setSeeded(false)
    let timer: number | undefined

    const stopTimer = () => {
      if (timer !== undefined) {
        window.clearInterval(timer)
        timer = undefined
      }
    }
    const startTimer = () => {
      if (timer !== undefined || sessionExpiredRef.current) return
      timer = window.setInterval(() => void refreshDetail(), DETAIL_POLL_INTERVAL)
    }
    const onVisibility = () => {
      if (document.hidden) {
        stopTimer()
      } else {
        startTimer()
        void refreshDetail()
      }
    }

    void refreshDetail()
    startTimer()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stopTimer()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [id, refreshDetail])

  // 事件流 WS：详情首拉完成后以 recent_events 的最大 seq 为起点订阅实时增量。
  // 依赖 [id, seeded]：只在新任务或首拉完成时（重建）订阅，不随 4s 详情轮询
  // 反复断开重连；WS 层自己推进游标，重连不重放已见事件。
  useEffect(() => {
    if (!id || !seeded) return
    setEvents(initialEventsRef.current)
    setWsStatus('connecting')
    setWsError(null)
    const conn = connectEvents({
      taskId: id,
      fromSeq: initialSeqRef.current,
      onEvent: (ev) => setEvents((prev) => [...prev, ev]),
      onStatus: (s) => {
        setWsStatus(s)
        if (s === 'open') {
          setDisconnected(false)
          setWsError(null)
        }
      },
      onError: (msg, code) => {
        // closeCode 0 只是「解析不出事件帧」的瞬时错，连接未断，不置为已断开
        if (code !== 0) setDisconnected(true)
        setWsError(msg)
        setDisconnectReason(msg)
      },
      onTerminal: () => {
        // 会话被吊销：WS 侧已停止重连，HTTP 侧由 401 收敛，这里落终止态
        sessionExpiredRef.current = true
        setSessionExpired(true)
        setWsStatus('closed')
      },
    })
    return () => conn.close()
  }, [id, seeded])

  // replyToTicket 是工单应答的提交入口：POST reply，成功后立即补拉让工单消失。
  const replyToTicket = useCallback(
    async (ticket: Ticket, answer: string) => {
      if (!id) return
      await replyTicket(id, { ticket_id: ticket.id, answer })
      void refreshDetail()
    },
    [id, refreshDetail],
  )

  return {
    detail,
    events,
    wsStatus,
    wsError,
    loadError,
    disconnected,
    disconnectReason,
    sessionExpired,
    refresh: () => void refreshDetail(),
    replyToTicket,
  }
}
```

**平移的机械核对（逐条打勾，错一条就是行为漂移）**：

| 原位置 | 搬后必须仍然成立 |
|---|---|
| `TaskPage.tsx:60` | `if (!id \|\| sessionExpiredRef.current) return` —— 两个条件都要在 |
| `TaskPage.tsx:63-68` | 只有首拉才写 `initialEventsRef` / `initialSeqRef` 并 `setSeeded(true)` |
| `TaskPage.tsx:73-77` | 401 → `sessionExpiredRef.current = true` 且**提前 return**，不落 loadError |
| `TaskPage.tsx:79-84` | 未初始化 → `loadError`；已初始化 → `disconnected` + 原因，**不清空 detail** |
| `TaskPage.tsx:92-94` | 换任务时重置 `initializedRef` / `sessionExpiredRef` / `seeded` |
| `TaskPage.tsx:104` | `startTimer` 在 `sessionExpiredRef.current` 为真时不起表 |
| `TaskPage.tsx:128` | WS effect 依赖是 `[id, seeded]`，**不能**加 detail 或 refreshDetail |
| `TaskPage.tsx:146` | `code !== 0` 才置 `disconnected`（0 是解析瞬时错，连接没断） |

- [ ] **Step 4: 写 TuiTab**

新建 `web/src/app/workbench/TuiTab.tsx`：

```tsx
// TuiTab —— 桌面端 TUI（spec §2.3）。
//
// 职责：把一个任务会话按原型的单栏纵向流渲染出来——会话正文在上，指令输入框
// 固定在底部；任务进 waiting_review 后，审阅取证接在正文末尾。
//
// 边界：
//   - **不含 TicketsPanel**。工单裁决收敛到全局工单弹层（spec §5.2）：一张工单
//     可能属于任何一个任务，藏在某个 tab 里就等于要求人先猜对是哪个任务
//   - 不含返回看板链接与页头：那是面包屑与左栏的事
//   - 不自己取数：全部经 useTaskSession
//
// 关于「TUI 的终局是一个 agent」（spec §2.3 末段）：本组件对外只依赖一个
// taskId，但内部布局不假设「必须有 task」——将来以 home 为基准开一个不绑任务的
// agent 会话时，替换的是数据源，不是这套布局。
import { Badge } from '@/components/ui/badge'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { useTaskSession } from '../task/useTaskSession'
import { TaskHeader } from '../task/TaskHeader'
import { TimelinePanel } from '../task/TimelinePanel'
import { EventsPanel } from '../task/EventsPanel'
import { ReviewPanel } from '../task/ReviewPanel'
import { AdvanceActions } from '../task/AdvanceActions'

export function TuiTab({ taskId }: { taskId: string }) {
  const s = useTaskSession(taskId)

  if (s.detail === null) {
    if (s.loadError) return <LoadFailed message={s.loadError} onRetry={s.refresh} />
    if (s.sessionExpired) return <SessionExpiredBanner />
    return <p className="p-4 text-sm text-muted-foreground">正在加载任务…</p>
  }

  // waiting_review 才挂 ReviewPanel：它是「这一轮干完了，你来验」的自然延续，
  // 不是常驻侧栏。任务还在跑时挂着它，等于邀请人去 diff 一个半成品
  const inReview = s.detail.task.state === 'waiting_review'

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs">
        <TaskHeader task={s.detail.task} compact />
        <div className="ml-auto flex items-center gap-2">
          {s.disconnected && <Badge variant="destructive">已断开</Badge>}
          {s.wsStatus === 'open' && <Badge variant="outline">实时</Badge>}
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-4 overflow-auto p-3">
        {s.sessionExpired && <SessionExpiredBanner />}
        {s.disconnected && !s.sessionExpired && <DisconnectedBanner message={s.disconnectReason} />}
        <TimelinePanel taskId={taskId} taskState={s.detail.task.state} />
        <EventsPanel events={s.events} status={s.wsStatus} error={s.wsError} />
        {inReview && <ReviewPanel taskId={taskId} />}
      </div>

      {/* 指令输入框固定在底部——原型的形态判据之一 */}
      <div className="border-t p-3">
        <AdvanceActions task={s.detail.task} disabled={s.disconnected} onChanged={s.refresh} />
      </div>
    </div>
  )
}
```

`TaskHeader` 目前的根元素是 `<section className="flex flex-col gap-3 rounded-lg border bg-background p-4">`（一张卡片，`TaskPage` 把它放在右列）。TUI tab 只需要单行摘要，所以给它加一个 `compact?: boolean`。**改法必须向后兼容**——`compact` 缺省即现有形态，其余调用点一行不动。

改 `web/src/app/task/TaskHeader.tsx` 的签名与首段：

```tsx
// compact 为真时只出单行摘要：TUI tab 的顶栏只有一行高度，塞不下完整的
// 定义列表，而任务 ID、分支、工作目录这些在面包屑与左栏已经能看到。
export function TaskHeader({ task, compact = false }: { task: Task; compact?: boolean }) {
  if (compact) {
    return (
      <div className="flex min-w-0 items-center gap-2">
        <span className="truncate text-sm font-medium">
          {task.name || task.plan_summary || '（无名称）'}
        </span>
        <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
          handoff-{shortID(task.id)}
        </span>
        <Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>
      </div>
    )
  }
  return (
    <section className="flex flex-col gap-3 rounded-lg border bg-background p-4">
      {/* …以下原样保留… */}
```

`web/src/app/task/TaskHeader.test.tsx` **目前不存在，本步新建**：

```tsx
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskHeader } from './TaskHeader'
import type { Task } from '../../api/types'

const task = {
  id: '7ec762e7-3bd2-412c-a39c-e4cf8b4057ad',
  name: '重构工单通道',
  state: 'running',
  branch: 'feat/x',
  executor: 'opencode',
  repo_dirty_count: 0,
  base_ahead: 0,
} as unknown as Task

describe('TaskHeader', () => {
  it('缺省仍是卡片形态，含完整定义列表', () => {
    const { container } = render(<TaskHeader task={task} />)
    expect(container.firstElementChild?.className).toContain('rounded-lg')
    expect(screen.getByText('工作目录')).toBeInTheDocument()
  })

  it('compact 去掉卡片外框，只留任务名 / 短号 / 状态', () => {
    const { container } = render(<TaskHeader task={task} compact />)
    expect(container.firstElementChild?.className).not.toContain('rounded-lg')
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.getByText('handoff-7ec762e7')).toBeInTheDocument()
    expect(screen.queryByText('工作目录')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 5: 删除 TaskPage**

```bash
git rm web/src/app/task/TaskPage.tsx
```

`App.tsx` 里对它的引用在 Task 11 一并改。**这一步之后到 Task 11 之间构建是断的**，这是有意的：把「删旧页」和「接新路由」放进一次提交会让 diff 变得没法读。所以本任务的提交只到 Step 6，构建绿灯由 Task 11 负责。

- [ ] **Step 6: 跑测试确认通过并提交**

```bash
npm --prefix web run test -- useTaskSession TaskHeader
```

Expected: `useTaskSession` 七条 + `TaskHeader` 全 PASS。（`npm run typecheck` 此时会因 `App.tsx` 仍引用已删除的 `TaskPage` 而报错，属预期，Task 11 修复。）

```bash
git add web/src/app/task/useTaskSession.ts web/src/app/task/useTaskSession.test.ts \
        web/src/app/task/TaskHeader.tsx web/src/app/task/TaskHeader.test.tsx \
        web/src/app/workbench/TuiTab.tsx
git commit -m "refactor(web): 任务会话编排提取为 useTaskSession，TUI tab 承载任务详情"
```

---

## Task 9: 右栏文件树 —— 改动集解析、按需目录列举、树渲染

**Files:**
- Create: `web/src/app/files/changedFiles.ts`
- Create: `web/src/app/files/useDirEntries.ts`
- Create: `web/src/app/files/FileTree.tsx`
- Test: `web/src/app/files/changedFiles.test.ts`
- Test: `web/src/app/files/FileTree.test.tsx`

**Interfaces:**
- Consumes: Task 3 的 `fetchWorkspaceDir` / `DirEntry`；既有 `fetchTaskDiff`；Task 5 的 `BaseDir`
- Produces:
  - `parseChangedFiles(diff: string): Set<string>`
  - `useChangedFiles(taskId: string | null): Set<string>`
  - `useDirEntries(base: BaseDir | null): DirEntriesApi`，`interface DirEntriesApi { entriesOf(rel: string): DirEntry[] | undefined; errorOf(rel: string): string | undefined; ensure(rel: string): void; refresh(): void }`
  - `FileTree({ base, taskId, onOpenFile })`

- [ ] **Step 1: 写 changedFiles 的失败测试**

新建 `web/src/app/files/changedFiles.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { parseChangedFiles } from './changedFiles'

describe('parseChangedFiles', () => {
  it('从 diff --git 行取出相对路径', () => {
    const diff = [
      'diff --git a/internal/agentd/transport_test.go b/internal/agentd/transport_test.go',
      'index 1111111..2222222 100644',
      '--- a/internal/agentd/transport_test.go',
      '+++ b/internal/agentd/transport_test.go',
      '@@ -1 +1 @@',
      '-x',
      '+y',
      'diff --git a/Makefile b/Makefile',
      '--- a/Makefile',
      '+++ b/Makefile',
    ].join('\n')
    expect([...parseChangedFiles(diff)].sort()).toEqual(['Makefile', 'internal/agentd/transport_test.go'])
  })

  it('新增文件也算改动', () => {
    const diff = ['diff --git a/new.txt b/new.txt', 'new file mode 100644', '--- /dev/null', '+++ b/new.txt'].join('\n')
    expect([...parseChangedFiles(diff)]).toEqual(['new.txt'])
  })

  it('删除文件取 a/ 侧路径', () => {
    const diff = ['diff --git a/gone.txt b/gone.txt', 'deleted file mode 100644', '--- a/gone.txt', '+++ /dev/null'].join('\n')
    expect([...parseChangedFiles(diff)]).toEqual(['gone.txt'])
  })

  it('重命名两侧都算改动', () => {
    const diff = ['diff --git a/old.go b/new.go', 'similarity index 95%', 'rename from old.go', 'rename to new.go'].join('\n')
    expect([...parseChangedFiles(diff)].sort()).toEqual(['new.go', 'old.go'])
  })

  it('带空格的路径不被截断', () => {
    const diff = 'diff --git a/docs/my notes.md b/docs/my notes.md'
    expect([...parseChangedFiles(diff)]).toEqual(['docs/my notes.md'])
  })

  it('空 diff 得到空集合，不抛异常', () => {
    expect(parseChangedFiles('').size).toBe(0)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- changedFiles
```

Expected: FAIL，模块解析失败。

- [ ] **Step 3: 写 changedFiles.ts**

新建 `web/src/app/files/changedFiles.ts`：

```ts
// changedFiles —— 从 `handoff diff` 的正文里解析出「相对基线已改动」的路径集合。
//
// 职责：给右栏文件树提供 M 角标的数据来源（spec §4）。
//
// 边界：
//   - **不是 git status**。`handoff diff` 是 `git diff base...HEAD`，只反映**已提交**
//     的改动，看不见工作区里未提交的编辑。角标含义因此是「相对基线已改动」，
//     tooltip 必须原样说清楚，不能让人当成 IDE 里那个 M
//   - 只解析 `diff --git` 头行与 rename 行，不解析 hunk。要的只是路径集合
//   - 没有任务挂在这个目录上时就没有数据源，返回空集合——不为一个装饰性角标
//     去开 git status 接口（那是文件写入期真正需要时才做的事）
//
// 为什么不用 `--- a/` / `+++ b/` 两行来取：新增文件那侧是 `/dev/null`，删除文件
// 另一侧也是 `/dev/null`，还要分别处理；`diff --git` 头行两侧永远都在。
import { useEffect, useState } from 'react'
import { fetchTaskDiff } from '../../api/client'

// RENAME_FROM 与 RENAME_TO 用于把重命名的两侧都算作改动——旧路径消失了、
// 新路径出现了，两个位置在树上都该有标记。
const RENAME_FROM = 'rename from '
const RENAME_TO = 'rename to '

// parseChangedFiles 解析 diff 正文，返回改动过的仓库相对路径集合。
//
// 参数：
//   - diff: `GET /api/tasks/{id}/diff` 返回的正文（可能为空串）
//
// 返回：相对路径集合。解析不出任何头行时返回空集合，不抛异常。
export function parseChangedFiles(diff: string): Set<string> {
  const out = new Set<string>()
  for (const line of diff.split('\n')) {
    if (line.startsWith('diff --git ')) {
      // 头行形如 `diff --git a/<路径> b/<路径>`。路径可能含空格，所以不能按空格
      // 切；改为找 ` b/` 这个分隔点，左右两段分别剥掉 `a/` 与 `b/` 前缀。
      const rest = line.slice('diff --git '.length)
      const sep = rest.indexOf(' b/')
      if (sep < 0) continue
      const left = rest.slice(0, sep)
      const right = rest.slice(sep + 1)
      if (left.startsWith('a/')) out.add(left.slice(2))
      if (right.startsWith('b/')) out.add(right.slice(2))
      continue
    }
    if (line.startsWith(RENAME_FROM)) out.add(line.slice(RENAME_FROM.length))
    else if (line.startsWith(RENAME_TO)) out.add(line.slice(RENAME_TO.length))
  }
  return out
}

// useChangedFiles 取「这个目录上挂着的任务」的改动集合。
//
// 参数：
//   - taskId: 目录上正在执行的任务 id；为 null 表示这个目录没有任务
//
// 返回：改动过的相对路径集合。没有任务、或取 diff 失败时都返回空集合——
// 角标是装饰，缺了不影响文件树可用，所以失败静默降级，不弹错误。
export function useChangedFiles(taskId: string | null): Set<string> {
  const [files, setFiles] = useState<Set<string>>(() => new Set())
  useEffect(() => {
    if (!taskId) {
      setFiles(new Set())
      return
    }
    let cancelled = false
    fetchTaskDiff(taskId)
      .then((r) => {
        if (!cancelled) setFiles(parseChangedFiles(r.diff))
      })
      .catch(() => {
        if (!cancelled) setFiles(new Set())
      })
    return () => {
      cancelled = true
    }
  }, [taskId])
  return files
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
npm --prefix web run test -- changedFiles
```

Expected: 六条全 PASS。

- [ ] **Step 5: 写 useDirEntries**

新建 `web/src/app/files/useDirEntries.ts`：

```ts
// useDirEntries —— 按需、单层的目录列举缓存。
//
// 职责：为文件树提供「展开哪一层就取哪一层」的数据；已取过的层留在内存里，
// 折叠再展开不重复请求。
//
// 边界：
//   - **不递归**。递归列举一个大仓库会打出上万条目，而树上同时可见的只有几十行
//   - 不做搜索：搜索框是对已列举内容的前端过滤（spec §4），不发请求。真正的
//     全仓搜索需要后端支持，本期不做
//   - 换基准目录时整份缓存清空——不同工作树的同名相对路径是不同的东西
//
// 失败按层记录：某一层 403/404 只让那一层显示原因，不把整棵树打成错误态。
import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchWorkspaceDir } from '../../api/client'
import type { DirEntry } from '../../api/types'
import { errorMessage } from '../lib/format'
import type { BaseDir } from '../workbench/useWorkbench'

export interface DirEntriesApi {
  entriesOf: (rel: string) => DirEntry[] | undefined
  errorOf: (rel: string) => string | undefined
  ensure: (rel: string) => void
  refresh: () => void
}

export function useDirEntries(base: BaseDir | null): DirEntriesApi {
  const [entries, setEntries] = useState<Record<string, DirEntry[]>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  // loaded 记「这一层已经发过请求」，挡住重复请求：树上一次渲染里 ensure 会被
  // 每个展开的层各调一次，而 entries/errors 要等响应回来才有值
  const loaded = useRef<Set<string>>(new Set())
  const baseKey = base?.key ?? ''

  // 换基准目录 = 换一棵树，旧缓存全部作废
  useEffect(() => {
    setEntries({})
    setErrors({})
    loaded.current = new Set()
  }, [baseKey])

  const load = useCallback(
    (rel: string) => {
      if (!base) return
      if (loaded.current.has(rel)) return
      loaded.current.add(rel)
      fetchWorkspaceDir(base.path, rel || undefined, base.machine || undefined)
        .then((r) => {
          setEntries((prev) => ({ ...prev, [rel]: r.entries }))
          setErrors((prev) => {
            const next = { ...prev }
            delete next[rel]
            return next
          })
        })
        .catch((err) => {
          setErrors((prev) => ({ ...prev, [rel]: errorMessage(err) }))
        })
    },
    [base],
  )

  // refresh 丢掉全部缓存；已展开的层由树在下一次渲染时经 ensure 重新取回
  const refresh = useCallback(() => {
    setEntries({})
    setErrors({})
    loaded.current = new Set()
    load('')
  }, [load])

  const entriesOf = useCallback((rel: string) => entries[rel], [entries])
  const errorOf = useCallback((rel: string) => errors[rel], [errors])

  return { entriesOf, errorOf, ensure: load, refresh }
}
```

- [ ] **Step 6: 写 FileTree 的失败测试**

新建 `web/src/app/files/FileTree.test.tsx`：

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { FileTree } from './FileTree'
import type { BaseDir } from '../workbench/useWorkbench'

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'integration/b2-b3',
  projectName: 'handoff',
  machine: '',
}

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchWorkspaceDir: vi.fn(), fetchTaskDiff: vi.fn() }
})
const { fetchWorkspaceDir, fetchTaskDiff } = await import('../../api/client')

afterEach(() => {
  vi.mocked(fetchWorkspaceDir).mockReset()
  vi.mocked(fetchTaskDiff).mockReset()
})

function dir(entries: { name: string; is_dir: boolean }[]) {
  return { entries: entries.map((e) => ({ ...e, size: 0 })) }
}

describe('FileTree', () => {
  it('头部有「文件」与刷新，根标题是当前目录名', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    expect(screen.getByText('文件')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '刷新' })).toBeInTheDocument()
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Makefile')).toBeInTheDocument())
  })

  it('点文件回调相对路径', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    const onOpenFile = vi.fn()
    render(<FileTree base={base} taskId={null} onOpenFile={onOpenFile} />)
    await waitFor(() => expect(screen.getByText('Makefile')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Makefile'))
    expect(onOpenFile).toHaveBeenCalledWith('Makefile')
  })

  it('展开目录时按需取下一层，路径拼接正确', async () => {
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p, rel) => {
      if (!rel) return dir([{ name: 'internal', is_dir: true }])
      if (rel === 'internal') return dir([{ name: 'agentd', is_dir: true }])
      return dir([{ name: 'server.go', is_dir: false }])
    })
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('internal')).toBeInTheDocument())
    fireEvent.click(screen.getByText('internal'))
    await waitFor(() => expect(screen.getByText('agentd')).toBeInTheDocument())
    fireEvent.click(screen.getByText('agentd'))
    await waitFor(() => expect(screen.getByText('server.go')).toBeInTheDocument())
    expect(fetchWorkspaceDir).toHaveBeenCalledWith('/w/b2-b3', 'internal/agentd', undefined)
  })

  it('M 角标的 tooltip 说的是「相对基线已改动」，不是 git status', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: 'diff --git a/Makefile b/Makefile' })
    render(<FileTree base={base} taskId="T1" onOpenFile={vi.fn()} />)
    const badge = await screen.findByText('M')
    expect(badge.getAttribute('title')).toContain('相对基线已改动')
    expect(badge.getAttribute('title')).not.toContain('工作区已修改')
  })

  it('没有任务的目录不显示角标，也不去取 diff', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Makefile')).toBeInTheDocument())
    expect(screen.queryByText('M')).not.toBeInTheDocument()
    expect(fetchTaskDiff).not.toHaveBeenCalled()
  })

  it('搜索框只做前端过滤，不发请求', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(
      dir([
        { name: 'Makefile', is_dir: false },
        { name: 'go.mod', is_dir: false },
      ]),
    )
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('go.mod')).toBeInTheDocument())
    const before = vi.mocked(fetchWorkspaceDir).mock.calls.length
    fireEvent.change(screen.getByPlaceholderText('搜索文件…'), { target: { value: 'make' } })
    expect(screen.queryByText('go.mod')).not.toBeInTheDocument()
    expect(screen.getByText('Makefile')).toBeInTheDocument()
    expect(vi.mocked(fetchWorkspaceDir).mock.calls.length).toBe(before)
  })

  it('某一层取数失败时只有那一层显示原文，整棵树仍可用', async () => {
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p, rel) => {
      if (!rel) {
        return dir([
          { name: 'secret', is_dir: true },
          { name: 'ok.txt', is_dir: false },
        ])
      }
      throw new Error('路径不在已探测到的工作树白名单内')
    })
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('secret')).toBeInTheDocument())
    fireEvent.click(screen.getByText('secret'))
    await waitFor(() => expect(screen.getByText(/白名单内/)).toBeInTheDocument())
    expect(screen.getByText('ok.txt')).toBeInTheDocument()
  })
})
```

- [ ] **Step 7: 跑测试确认失败**

```bash
npm --prefix web run test -- FileTree
```

Expected: FAIL，模块解析失败。

- [ ] **Step 8: 写 FileTree**

新建 `web/src/app/files/FileTree.tsx`：

```tsx
// FileTree —— 右栏文件树（spec §4）。
//
// 职责：
//   - 头部（文件 / 刷新 / 折叠全部）+ 搜索框 + 根标题 + 可展开的树体
//   - 点文件 → 回调相对路径，由中央开 file tab
//   - 「相对基线已改动」的 M 角标
//
// 边界：
//   - 只在选中目录时渲染（挂不挂由 Shell 决定）
//   - 只读：不发写请求，不提供新建/重命名/删除
//   - 搜索是**对已列举内容的前端过滤**，不发请求、不展开未展开的层
//
// 角标语义（不得含糊）：数据来自 `handoff diff` = `git diff base...HEAD`，只反映
// 已提交的改动。tooltip 写「相对基线已改动」，不写「工作区已修改」——后者是
// git status 的语义，这里给不出来。
import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, File, FolderClosed, FolderOpen, RefreshCw } from 'lucide-react'
import type { DirEntry } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { useChangedFiles } from './changedFiles'
import { useDirEntries, type DirEntriesApi } from './useDirEntries'

// CHANGED_TITLE 是 M 角标的 tooltip。措辞是 spec §4 的硬要求，不要改写。
const CHANGED_TITLE = '相对基线已改动（git diff base...HEAD，不含工作区未提交的编辑）'

export interface FileTreeProps {
  base: BaseDir
  // taskId 是这个目录上挂着的任务；为 null 表示没有任务，此时不显示角标
  taskId: string | null
  onOpenFile: (rel: string) => void
}

export function FileTree({ base, taskId, onOpenFile }: FileTreeProps) {
  const dirs = useDirEntries(base)
  const changed = useChangedFiles(taskId)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [query, setQuery] = useState('')

  // 挂载与换目录时取根层。子层在展开时由 Row 显式 ensure，不在渲染期取数——
  // 渲染期调用会 setState 的函数容易被 lint 判为副作用，也难推理
  useEffect(() => {
    setExpanded(new Set())
    dirs.ensure('')
  }, [base.key, dirs])

  const toggle = (rel: string) => {
    dirs.ensure(rel)
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(rel)) next.delete(rel)
      else next.add(rel)
      return next
    })
  }

  return (
    <aside className="flex h-full min-h-0 flex-col border-l bg-background">
      <div className="flex items-center gap-1 border-b px-3 py-2">
        <span className="text-sm font-medium">文件</span>
        <div className="ml-auto flex items-center gap-0.5">
          <button
            type="button"
            aria-label="刷新"
            onClick={dirs.refresh}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <RefreshCw className="size-3.5" />
          </button>
          <button
            type="button"
            aria-label="折叠全部"
            onClick={() => setExpanded(new Set())}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <ChevronDown className="size-3.5" />
          </button>
        </div>
      </div>

      <div className="border-b px-3 py-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索文件…"
          className="w-full rounded-md border px-2 py-1 text-xs"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-auto py-1">
        <p className="truncate px-3 py-1 font-mono text-[11px] text-muted-foreground">{base.label}</p>
        <DirLevel
          dirs={dirs}
          rel=""
          depth={0}
          expanded={expanded}
          onToggle={toggle}
          onOpenFile={onOpenFile}
          changed={changed}
          query={query.trim().toLowerCase()}
        />
      </div>
    </aside>
  )
}

interface LevelProps {
  dirs: DirEntriesApi
  rel: string
  depth: number
  expanded: Set<string>
  onToggle: (rel: string) => void
  onOpenFile: (rel: string) => void
  changed: Set<string>
  query: string
}

// DirLevel 渲染一层目录：加载中 / 该层失败 / 条目列表三种形态。
function DirLevel(props: LevelProps) {
  const { dirs, rel, depth, query } = props
  const entries = dirs.entriesOf(rel)
  const error = dirs.errorOf(rel)
  const pad = { paddingLeft: `${12 + depth * 12}px` }

  if (error !== undefined) {
    return (
      <p className="py-1 pr-3 text-[11px] text-destructive" style={pad}>
        {error}
      </p>
    )
  }
  if (entries === undefined) {
    return (
      <p className="py-1 pr-3 text-[11px] text-muted-foreground" style={pad}>
        正在列举…
      </p>
    )
  }

  // 前端过滤：目录始终保留（否则通往匹配文件的展开路径会被过滤断掉），
  // 文件按名字匹配
  const shown = query ? entries.filter((e) => e.is_dir || e.name.toLowerCase().includes(query)) : entries

  return (
    <ul>
      {shown.map((e) => (
        <Row key={e.name} entry={e} pad={pad} {...props} />
      ))}
    </ul>
  )
}

// Row 渲染一个条目。目录行负责拼接子层的相对路径并递归渲染 DirLevel。
function Row({ entry, pad, ...rest }: LevelProps & { entry: DirEntry; pad: { paddingLeft: string } }) {
  const { rel: parentRel, depth, expanded, onToggle, onOpenFile, changed } = rest
  const rel = parentRel ? `${parentRel}/${entry.name}` : entry.name
  const open = expanded.has(rel)

  if (entry.is_dir) {
    return (
      <li>
        <button
          type="button"
          onClick={() => onToggle(rel)}
          className="flex w-full items-center gap-1.5 py-0.5 pr-3 text-left text-xs hover:bg-accent"
          style={pad}
        >
          {open ? <ChevronDown className="size-3 shrink-0" /> : <ChevronRight className="size-3 shrink-0" />}
          {open ? (
            <FolderOpen className="size-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <FolderClosed className="size-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate">{entry.name}</span>
        </button>
        {open && <DirLevel {...rest} rel={rel} depth={depth + 1} />}
      </li>
    )
  }

  return (
    <li>
      <button
        type="button"
        onClick={() => onOpenFile(rel)}
        className="flex w-full items-center gap-1.5 py-0.5 pr-3 text-left text-xs hover:bg-accent"
        style={pad}
      >
        <File className="ml-3 size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate">{entry.name}</span>
        {changed.has(rel) && (
          <span title={CHANGED_TITLE} className="ml-auto shrink-0 font-mono text-[10px] text-amber-600">
            M
          </span>
        )}
      </button>
    </li>
  )
}
```

- [ ] **Step 9: 跑测试确认通过**

```bash
npm --prefix web run test -- FileTree changedFiles && npm --prefix web run typecheck
```

Expected: 全 PASS。若 `useEffect` 的依赖数组因 `dirs` 每次渲染都是新对象而反复触发，把 `useDirEntries` 返回的对象用 `useMemo` 包一层再返回（依赖 `[entriesOf, errorOf, load, refresh]`）。

- [ ] **Step 10: 提交**

```bash
git add web/src/app/files/
git commit -m "feat(web): 右栏文件树（按需列举、前端过滤、相对基线改动角标）"
```

---

## Task 10: 左栏语义改造 —— 目录选中、任务开 TUI、顶部看板入口、底部三按钮

**Files:**
- Modify: `web/src/app/tree/ProjectTree.tsx`
- Modify: `web/src/app/tree/ProjectTree.test.tsx`

**Interfaces:**
- Consumes: Task 5 的 `BaseDir`
- Produces（`ProjectTreeProps` 的新形态）：
  ```ts
  export interface ProjectTreeProps {
    tree: ProjectTreeResp
    tasks: Task[]
    selectedKey: string | null            // 当前选中目录的 BaseDir.key
    ticketCount: number                   // 挂起工单总数，0 时不显示角标
    onSelectDir: (base: BaseDir) => void
    // base 为 null = 未归属任务（不在树上的任何目录下），由 Shell 决定用哪个基准开
    onOpenTask: (base: BaseDir | null, taskId: string) => void
    onOpenBoard: () => void
    onOpenTickets: () => void
    onOpenSettings: () => void
    onAddProject?: () => void
    onUnregister?: (name: string, machine: string) => Promise<void> | void
  }
  ```
- 也导出供 Shell 与看板复用的三个 join 函数：`export function workspaceBase(project: ProjectNode, machine: string, ws: Workspace): BaseDir`、`export function tasksOfWorkspace(tasks: Task[], project: ProjectNode, machine: string, ws: Workspace): Task[]`、`export function findBaseOfTask(tree: ProjectTreeResp, tasks: Task[], taskId: string): BaseDir | null`
  三个都放在 `ProjectTree.tsx` 里，因为它们是同一套「树 ↔ 任务」的 join 规则；Shell（`/tasks/:id` 深链）与看板弹层（点卡片跳目录）都要用第三个，两处各写一份必然会分叉。

**背景（实现者必读）**：`filter` / `onFilterChange` 两个入参从这里**移除**，`onOpenTask` 的签名换成带基准目录的两参形态——看板的筛选改由看板弹层自己的 `FilterBar` 承担（spec §3.1）。`board/filter.ts` 的纯函数**不删**，`selectProject` / `selectMachine` / `selectWorkspace` 三个「树写 filter」的函数在本任务后无人调用，一并删除（连同 `filter.test.ts` 中对它们的用例）；`FilterBar` 用到的其余函数原样保留。

`ProjectTree.tsx` 现有的这些必须原样保留，不得回退：`locationProblem` / `DisconnectedBadge` / 不可达机器的原因原文行、未归属分组、`countsForProject` / `countsForMachine` / `wsCounts` 计数、机器行的注销按钮与 `ConfirmDialog` 全流程（含 agentd 错误原文透出）、`collapsed` 用「收起集合」的默认全展开语义。

- [ ] **Step 1: 写失败的测试**

改 `web/src/app/tree/ProjectTree.test.tsx`。先 `grep -n "onFilterChange\|selectProject\|selectWorkspace\|filter=" web/src/app/tree/ProjectTree.test.tsx` 找到所有依赖旧入参的用例，把它们改成新语义；然后追加下面这组：

```tsx
it('点项目行只展开折叠，不再写筛选', () => {
  const onSelectDir = vi.fn()
  render(<ProjectTree {...props({ onSelectDir })} />)
  fireEvent.click(screen.getByText('handoff'))
  expect(onSelectDir).not.toHaveBeenCalled()
  // 折叠后其下的目录行消失
  expect(screen.queryByText('integration/b2-b3')).not.toBeInTheDocument()
})

it('点目录行选中它，回调带完整 BaseDir', () => {
  const onSelectDir = vi.fn()
  render(<ProjectTree {...props({ onSelectDir })} />)
  fireEvent.click(screen.getByText('integration/b2-b3'))
  expect(onSelectDir).toHaveBeenCalledWith({
    key: '/w/b2-b3',
    kind: 'workspace',
    path: '/w/b2-b3',
    label: 'integration/b2-b3',
    projectName: 'handoff',
    machine: '',
  })
})

it('detached 的目录用目录名兜底作为 label', () => {
  const onSelectDir = vi.fn()
  render(<ProjectTree {...props({ onSelectDir, branch: '' })} />)
  fireEvent.click(screen.getByText('b2-b3'))
  expect(onSelectDir).toHaveBeenCalledWith(expect.objectContaining({ label: 'b2-b3' }))
})

it('selectedKey 命中的目录行带 aria-current', () => {
  render(<ProjectTree {...props({ selectedKey: '/w/b2-b3' })} />)
  expect(screen.getByRole('button', { name: /integration\/b2-b3/ })).toHaveAttribute('aria-current', 'true')
})

it('点任务行同时给出它所在目录与任务 id', () => {
  const onOpenTask = vi.fn()
  render(<ProjectTree {...props({ onOpenTask })} />)
  fireEvent.click(screen.getByText('重构工单通道'))
  expect(onOpenTask).toHaveBeenCalledWith(expect.objectContaining({ key: '/w/b2-b3' }), 'T1')
})

it('work_dir 为空的任务挂到主目录（原地模式）', () => {
  const onOpenTask = vi.fn()
  render(<ProjectTree {...props({ inPlaceTask: true })} />)
  // 主目录行下应出现这条任务
  expect(screen.getByText('原地任务')).toBeInTheDocument()
})

it('顶部有任务看板入口，且不再有开发机入口', () => {
  const onOpenBoard = vi.fn()
  render(<ProjectTree {...props({ onOpenBoard })} />)
  fireEvent.click(screen.getByRole('button', { name: /任务看板/ }))
  expect(onOpenBoard).toHaveBeenCalled()
  expect(screen.queryByRole('button', { name: '开发机' })).not.toBeInTheDocument()
})

it('底部三个入口都在；工单数为 0 时按钮仍在但不显示角标', () => {
  render(<ProjectTree {...props({ ticketCount: 0 })} />)
  expect(screen.getByRole('button', { name: /添加项目/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /工单/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '设置' })).toBeInTheDocument()
  expect(screen.queryByText('0')).not.toBeInTheDocument()
})

it('工单数大于 0 时显示角标并可点开', () => {
  const onOpenTickets = vi.fn()
  render(<ProjectTree {...props({ ticketCount: 3, onOpenTickets })} />)
  expect(screen.getByText('3')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /工单/ }))
  expect(onOpenTickets).toHaveBeenCalled()
})

it('设置入口可点', () => {
  const onOpenSettings = vi.fn()
  render(<ProjectTree {...props({ onOpenSettings })} />)
  fireEvent.click(screen.getByRole('button', { name: '设置' }))
  expect(onOpenSettings).toHaveBeenCalled()
})
```

`props()` 是这个测试文件里的既有工厂（若没有就新建一个）：返回一棵含一个项目 `handoff`、一台本机、两个目录（`is_main` 的主目录 + 分支为 `integration/b2-b3` 的工作树 `/w/b2-b3`）、目录下挂一个任务 `T1 重构工单通道` 的树，并允许用参数覆写 `branch` / `selectedKey` / `ticketCount` / `inPlaceTask` 与各回调。

**同时保留并跑通现有的诚实展示用例**（不可达机器可见 + 原因原文、未归属分组、注销确认与错误原文）——它们不依赖被删掉的 `filter` 入参，只需把 `props()` 的构造改掉。

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- ProjectTree
```

Expected: FAIL，新用例找不到看板/工单/设置按钮，且旧的 `onFilterChange` 断言报类型/未定义错误。

- [ ] **Step 3: 改 ProjectTree.tsx —— 入参与两个 join 函数**

替换文件头注释里「筛选写入」那一段，改为：

```
// 点击语义（W4 §3.1 改造）：
//   - 项目 / 开发机行：只展开折叠，不再写筛选。看板的筛选归看板弹层自己的
//     FilterBar，树不再是筛选的编辑入口
//   - 目录行：**选中为当前目录**——切中央 tab 组 + 右栏文件树 + 面包屑
//   - 任务行：选中其所在目录，并在中央开它的 TUI tab
//
// 任务挂到目录的依据是 Task.work_dir 与 Workspace.path 路径等值（纯前端 join，
// 不需要新接口）。work_dir 为空表示原地模式，挂到主目录——与 proto.Task.Workdir()
// 的回退语义一致。
```

替换 `ProjectTreeProps`，并在其下加两个导出的 join 函数：

```tsx
import type { BaseDir } from '../workbench/useWorkbench'

export interface ProjectTreeProps {
  tree: ProjectTreeResp
  tasks: Task[]
  selectedKey: string | null
  ticketCount: number
  onSelectDir: (base: BaseDir) => void
  onOpenTask: (base: BaseDir, taskId: string) => void
  onOpenBoard: () => void
  onOpenTickets: () => void
  onOpenSettings: () => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
}

// dirLabel 是目录行显示的短名。
//
// 优先分支名（原型显示的是 `integration/b2-b3` 这样的分支），detached 时分支为
// 空串，退回路径最后一段——总得有个能认的名字，显示整条绝对路径会把行撑爆。
function dirLabel(ws: Workspace): string {
  if (ws.branch !== '') return ws.branch
  const seg = ws.path.split('/').filter(Boolean)
  return seg.length > 0 ? seg[seg.length - 1] : ws.path
}

// workspaceBase 把树上的一个目录节点转成 BaseDir。
//
// 参数：project 所属项目；machine 机器名（""=本机）；ws 目录节点
// 返回：可直接交给 useWorkbench.select 的基准目录
//
// key 用绝对路径：同一台机器上路径唯一，且它正是后端白名单比对的那个值，
// 前后端用同一个字符串做身份，不需要额外的映射表。
export function workspaceBase(project: ProjectNode, machine: string, ws: Workspace): BaseDir {
  return {
    key: ws.path,
    kind: 'workspace',
    path: ws.path,
    label: dirLabel(ws),
    projectName: project.name,
    machine,
  }
}

// tasksOfWorkspace 挑出挂在这个目录下的任务。
//
// work_dir 为空 = 原地模式，挂到主目录（is_main）。这条回退与后端
// proto.Task.Workdir() 一致，不要改成「挂到第一个目录」。
export function tasksOfWorkspace(
  tasks: Task[],
  project: ProjectNode,
  machine: string,
  ws: Workspace,
): Task[] {
  return tasks.filter((t) => {
    if (t.project_id !== project.project_id || t.machine !== machine) return false
    if (t.work_dir === '') return ws.is_main
    return t.work_dir === ws.path
  })
}

// findBaseOfTask 在树上反查任务所在的目录。
//
// 返回 null 的两种情形都是真实的，不要当异常处理：任务未归属（项目不在树上），
// 或者它的目录已经不在了（工作树被删但任务还在）。调用方（Shell 的 openTaskTui）
// 拿到 null 时退回「用当前选中目录开」，一个都没选中才提示。
export function findBaseOfTask(
  tree: ProjectTreeResp,
  tasks: Task[],
  taskId: string,
): BaseDir | null {
  if (!tasks.some((t) => t.id === taskId)) return null
  for (const project of tree.projects) {
    for (const loc of project.locations) {
      for (const ws of loc.workspaces) {
        if (tasksOfWorkspace(tasks, project, loc.machine, ws).some((t) => t.id === taskId)) {
          return workspaceBase(project, loc.machine, ws)
        }
      }
    }
  }
  return null
}
```

`wsCounts` 改为基于 `tasksOfWorkspace`，这样计数与列出的任务永远一致（否则原地任务会被算漏）：

```tsx
const wsCounts = (project: ProjectNode, machine: string, ws: Workspace) => {
  const under = tasksOfWorkspace(tasks, project, machine, ws)
  return {
    running: under.filter((t) => t.state === 'running').length,
    pending: under.filter((t) => t.state === 'waiting_answer' || t.state === 'waiting_review').length,
  }
}
```

- [ ] **Step 4: 改点击语义与选中态**

- 组件签名换成新入参；删掉 `multi` / `singleProject` 两行（它们是 filter 多选高亮的产物）。
- 项目行：`onClick={() => toggle(pKey)}`，去掉 `aria-current`（项目不再是选中态）。`Arrow` 的 `onToggle` 保留，行本身与箭头做同一件事不冲突。
- 机器行：`onClick={problem !== '' ? undefined : () => toggle(mKey)}`，去掉 `aria-current`。**`problem !== ''` 时不可展开这条保留**。
- 目录行：

```tsx
const base = workspaceBase(project, loc.machine, ws)
const dSelected = selectedKey === base.key
const under = wsCounts(project, loc.machine, ws)
const wsTasks = tasksOfWorkspace(tasks, project, loc.machine, ws)
// 原来的 dKey 局部变量删除，key 改用 base.key（同一把身份，与选中态比较的是同一个值）
return (
  <div key={base.key}>
    <button
      type="button"
      aria-current={dSelected ? 'true' : undefined}
      onClick={() => onSelectDir(base)}
      className={cn(ROW_CLASS, 'hover:bg-accent/60', dSelected && 'bg-sidebar-accent font-medium')}
      style={{ paddingLeft: 8 + 32 }}
    >
      <span className="size-4 shrink-0" />
      {ws.is_main ? (
        <Home className="size-3.5 shrink-0 text-muted-foreground" />
      ) : (
        <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
      )}
      <span className="min-w-0 flex-1 truncate font-mono">{dirLabel(ws)}</span>
      <RowCounts text={`${under.running}·${under.pending}`} title="运行·待处理" />
    </button>
    {/* 这个 <div> 里 <button> 之后的任务行渲染保持原样，只改下面这条 onClick */}
```

（`Home` / `GitBranch` 从 `lucide-react` 引入。主目录与工作树用不同图标是原型的形态——截图里是 `🏠 主目录` 与 `⑂ integration/b2-b3` 并排。）

- 任务行：`onClick={() => onOpenTask(base, t.id)}`。
- 未归属分组里的任务行没有目录可归——保持它调 `onOpenTask`，但基准目录传 `null`。为此把回调签名放宽为 `onOpenTask: (base: BaseDir | null, taskId: string) => void`，并在文件头注释里写明「未归属任务没有基准目录，中央以当前选中目录开它的 TUI tab；一个都没选中时由 Shell 提示先选目录」。**同步把 Step 1 里那条断言改成 `expect.objectContaining({ key: '/w/b2-b3' })` 之外再加一条未归属用例断言首参为 `null`。**

- [ ] **Step 5: 加顶部看板入口与底部三按钮**

顶部（`return` 的第一个子元素，在 `multi` 提示删除后的位置）：

```tsx
<button
  type="button"
  onClick={onOpenBoard}
  className={cn(ROW_CLASS, 'mb-1 hover:bg-accent/60')}
  style={{ paddingLeft: 8 }}
>
  <LayoutGrid className="size-4 shrink-0 text-muted-foreground" />
  <span>任务看板</span>
</button>
```

底部（替换现有的单个「添加项目」按钮）：

```tsx
{/* 底部三入口：添加项目占主位，工单与设置收在右侧图标区（spec §3.2）。
    工单数为 0 时按钮仍在、角标不显示——按钮消失会让人以为功能没了 */}
<div className="mt-1 flex items-center gap-1 border-t px-2 pt-2">
  <button
    type="button"
    onClick={onAddProject}
    className="flex flex-1 items-center gap-1.5 rounded-md py-1 pl-1 text-left text-[13px] text-muted-foreground hover:bg-accent/60 hover:text-foreground"
  >
    <Plus className="size-4 shrink-0" />
    <span>添加项目</span>
  </button>
  <button
    type="button"
    aria-label="工单"
    onClick={onOpenTickets}
    className="relative rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
  >
    <Ticket className="size-4" />
    {ticketCount > 0 && (
      <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-amber-500 px-1 text-center text-[10px] leading-4 text-white">
        {ticketCount}
      </span>
    )}
  </button>
  <button
    type="button"
    aria-label="设置"
    onClick={onOpenSettings}
    className="rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
  >
    <Settings className="size-4" />
  </button>
</div>
```

（新增 import：`LayoutGrid`、`Ticket`、`Settings`、`Home`、`GitBranch`。`aria-label="工单"` 让测试能按名取到它，同时读屏也能念出来。）

- [ ] **Step 6: 删掉 filter.ts 里三个不再有人调的函数**

```bash
grep -rn "selectProject\|selectMachine\|selectWorkspace" web/src/
```

确认只剩 `filter.ts` 自身与 `filter.test.ts`。删掉这三个导出函数与它们的用例。**`BoardFilter` 类型、`FilterBar` 用到的其余函数一律保留。**

- [ ] **Step 7: 跑测试确认通过**

```bash
npm --prefix web run test -- ProjectTree filter && npm --prefix web run typecheck
```

Expected: `ProjectTree` 全绿（含原有的诚实展示用例），`filter` 用例在删掉三条后仍全绿。`Shell.tsx` 此时会因入参不匹配报类型错，属预期，Task 11 修复。

- [ ] **Step 8: 提交**

```bash
git add web/src/app/tree/ProjectTree.tsx web/src/app/tree/ProjectTree.test.tsx \
        web/src/app/board/filter.ts web/src/app/board/filter.test.ts
git commit -m "refactor(web): 左栏点击语义改为导航，加看板入口与底部三按钮"
```

---

## Task 11: Shell 三栏布局、面包屑、路由改写，删 TopTabs

**Files:**
- Create: `web/src/app/shell/Breadcrumb.tsx`
- Modify: `web/src/app/shell/Shell.tsx`
- Modify: `web/src/app/shell/Shell.test.tsx`
- Modify: `web/src/App.tsx`
- Delete: `web/src/app/shell/TopTabs.tsx`

**Interfaces:**
- Consumes: Task 5 `useWorkbench` / `BaseDir` / `HOME_BASE`；Task 6 `WorkbenchPage`；Task 7 `TerminalTab` / `FileTab`；Task 8 `TuiTab`；Task 9 `FileTree`；Task 10 的 `ProjectTree` 新入参与 `findBaseOfTask`
- Produces:
  - `Breadcrumb({ base, onSplit })`
  - `Shell()` —— 不再用 `<Outlet>` 下发上下文；`ShellContext` / `useShellContext` **删除**
  - `TaskDeepLink()` —— `/tasks/:id` 的深链承接组件

**这一步是全期的合龙点。** 前十个任务各自能测但拼不起来；本任务之后 `npm run build` 必须重新绿。

**路由的新形态**（写进 `App.tsx` 的文件头注释）：

| 路径 | 行为 |
|---|---|
| `/` | 工作台（三栏）。中央是 tab 系统 |
| `/settings` | 三栏不变，中央整页换成设置页（spec §6：不是弹层） |
| `/tasks/:id` | 深链承接：选中该任务所在目录 + 在中央开它的 TUI tab + `navigate('/', { replace: true })` |
| `/machines` | `<Navigate to="/settings" replace />`（原开发机页收进设置页，spec §8.4） |

- [ ] **Step 1: 写失败的测试**

改 `web/src/app/shell/Shell.test.tsx`。先 `grep -n "TopTabs\|Outlet\|useShellContext" web/src/app/shell/Shell.test.tsx` 清掉依赖旧外框的断言，然后写：

```tsx
it('未选中目录时右栏文件树不渲染，中央是全局空态', async () => {
  renderShell()
  await waitFor(() => expect(screen.getByText('handoff')).toBeInTheDocument())
  expect(screen.queryByText('文件')).not.toBeInTheDocument()
  expect(screen.getByText(/从侧边栏选择一个目录开始/)).toBeInTheDocument()
})

it('选中目录后右栏出现、面包屑显示 项目 / 开发机 / 目录', async () => {
  renderShell()
  fireEvent.click(await screen.findByText('integration/b2-b3'))
  await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
  const crumb = screen.getByLabelText('当前位置')
  expect(crumb).toHaveTextContent('handoff')
  expect(crumb).toHaveTextContent('本机')
  expect(crumb).toHaveTextContent('integration/b2-b3')
})

it('点左栏任务在中央开 TUI tab', async () => {
  renderShell()
  fireEvent.click(await screen.findByText('重构工单通道'))
  await waitFor(() => expect(screen.getByRole('tab', { name: /TUI · T1/ })).toBeInTheDocument())
})

it('点右栏文件在中央开 file tab', async () => {
  renderShell()
  fireEvent.click(await screen.findByText('integration/b2-b3'))
  fireEvent.click(await screen.findByText('go.mod'))
  await waitFor(() => expect(screen.getByRole('tab', { name: /go.mod/ })).toBeInTheDocument())
})

it('切到另一个目录再切回来，两边的 tab 组各自保持', async () => {
  renderShell()
  fireEvent.click(await screen.findByText('integration/b2-b3'))
  fireEvent.click(await screen.findByText('go.mod'))
  await screen.findByRole('tab', { name: /go.mod/ })

  fireEvent.click(screen.getByText('主目录'))
  await waitFor(() => expect(screen.queryByRole('tab', { name: /go.mod/ })).not.toBeInTheDocument())

  fireEvent.click(screen.getByText('integration/b2-b3'))
  await waitFor(() => expect(screen.getByRole('tab', { name: /go.mod/ })).toBeInTheDocument())
})

it('面包屑的分屏按钮把中央分成两组', async () => {
  renderShell()
  fireEvent.click(await screen.findByText('integration/b2-b3'))
  fireEvent.click(screen.getByRole('button', { name: '分屏' }))
  await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(2))
})

it('/settings 整页替换中央，左栏仍在', async () => {
  renderShell('/settings')
  await waitFor(() => expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument())
  expect(screen.getByText('handoff')).toBeInTheDocument() // 左栏还在
})

it('/machines 重定向到 /settings', async () => {
  renderShell('/machines')
  await waitFor(() => expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument())
})

it('/tasks/:id 深链选中目录、开 TUI tab 并换回 /', async () => {
  renderShell('/tasks/T1')
  await waitFor(() => expect(screen.getByRole('tab', { name: /TUI · T1/ })).toBeInTheDocument())
  expect(screen.getByLabelText('当前位置')).toHaveTextContent('integration/b2-b3')
})

it('顶部 tab 条已删除', async () => {
  renderShell()
  await waitFor(() => expect(screen.getByText('handoff')).toBeInTheDocument())
  expect(screen.queryByRole('navigation', { name: '主导航' })).not.toBeInTheDocument()
})
```

`renderShell(path = '/')` 用 `MemoryRouter initialEntries={[path]}` 包住 `<AppRoutes />`（把 `App.tsx` 里的 `<Routes>` 部分提取为具名导出 `AppRoutes`，`App` 只负责套 `BrowserRouter`——否则测试没法指定初始路径）。数据侧 mock `fetchTasks` / `fetchProjectTree` / `fetchWorkspaceDir` / `fetchTaskDetail` / `fetchTaskDiff`，树的形状与 Task 10 的 `props()` 一致（主目录标签为「主目录」，工作树标签为 `integration/b2-b3`，其下一个任务 `T1 重构工单通道`），`fetchWorkspaceDir` 返回 `go.mod`。

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- Shell
```

Expected: FAIL。

- [ ] **Step 3: 写 Breadcrumb**

新建 `web/src/app/shell/Breadcrumb.tsx`：

```tsx
// Breadcrumb —— 顶部面包屑：项目 / 开发机 / 目录，右侧分屏按钮。
//
// 职责：回答「我现在在哪」。当前目录是唯一的全局选中态（spec §1.2），面包屑
// 就是它的可见形式。
//
// 边界：
//   - 只显示不导航。三段都不可点——上级（项目、开发机）在这套 IA 里不是可以
//     「进入」的东西，做成链接会承诺一个不存在的页面
//   - 未选中目录时不渲染（由 Shell 判断）
import { ChevronRight, Columns2 } from 'lucide-react'
import type { BaseDir } from '../workbench/useWorkbench'

export function Breadcrumb({ base, onSplit }: { base: BaseDir; onSplit: () => void }) {
  // home 基准不属于任何项目/机器，只显示一段
  const segments =
    base.kind === 'home'
      ? ['home']
      : [base.projectName, base.machine === '' ? '本机' : base.machine, base.label]
  return (
    <div className="flex items-center gap-1 border-b bg-background px-3 py-1.5">
      <nav aria-label="当前位置" className="flex min-w-0 items-center gap-1 text-xs">
        {segments.map((s, i) => (
          <span key={i} className="flex min-w-0 items-center gap-1">
            {i > 0 && <ChevronRight className="size-3 shrink-0 text-muted-foreground" />}
            <span className={i === segments.length - 1 ? 'truncate font-medium' : 'truncate text-muted-foreground'}>
              {s}
            </span>
          </span>
        ))}
      </nav>
      <button
        type="button"
        aria-label="分屏"
        onClick={onSplit}
        className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        <Columns2 className="size-4" />
      </button>
    </div>
  )
}
```

- [ ] **Step 4: 重写 Shell**

`web/src/app/shell/Shell.tsx` 整体替换为：

```tsx
// Shell —— 控制台的三栏外框：左栏导航树 / 中央 tab 工作台 / 右栏文件树。
//
// 职责：
//   - 持有跨栏共享的数据流（任务流 2.5s、项目树流 30s）与**当前基准目录**这一
//     唯一全局选中态（useWorkbench）
//   - 把三栏接起来：左栏点目录 → 切基准；左栏点任务 → 切基准 + 开 TUI tab；
//     右栏点文件 → 开 file tab；中央按 tab 种类分发渲染
//   - 承载弹出层（看板、工单）、设置页与右下角悬浮按钮
//
// 边界：
//   - 不自己取目录内容（归 FileTree）、不自己取任务会话（归 TuiTab）
//   - 中央 tab 的具体渲染经 renderContent 注入 WorkbenchPage，Shell 只做分发
//   - 机器流只随登记向导开表（useMachines(wizardOpen)）：探活会向每台远程机发
//     GET /api/status，没人看的时候没有理由持续打扰它们（spec §6）
//
// 关于 ShellContext 的移除：W3 用 <Outlet context> 给三个子页面下发共享数据。
// 新 IA 里中央不再是路由页面而是 tab，Outlet 没有了消费者；看板与工单改为弹层，
// 它们要的数据直接由 Shell 以 props 传下去。留一个没人用的 context 只会误导。
import { useMemo, useState } from 'react'
import { Navigate, Route, Routes, useNavigate, useParams } from 'react-router-dom'
import { deleteProject } from '../../api/client'
import type { ProjectNode, Task, Workspace } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { AddProjectWizard } from '../projects/AddProjectWizard'
import { findBaseOfTask, ProjectTree } from '../tree/ProjectTree'
import { FileTree } from '../files/FileTree'
import { WorkbenchPage } from '../workbench/WorkbenchPage'
import { TerminalTab } from '../workbench/TerminalTab'
import { FileTab } from '../workbench/FileTab'
import { TuiTab } from '../workbench/TuiTab'
import { FloatingNewPane } from '../workbench/FloatingNewPane'
import { useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import { BoardOverlay } from '../overlay/BoardOverlay'
import { TicketsOverlay } from '../overlay/TicketsOverlay'
import { useGlobalTickets } from '../overlay/useGlobalTickets'
import { SettingsPage } from '../settings/SettingsPage'
import { Breadcrumb } from './Breadcrumb'

// OverlayKind 是当前打开的弹层。同时只允许一个（spec §0）：两个叠在一起时
// Esc 该关哪个会变得含糊。
type OverlayKind = 'none' | 'board' | 'tickets'

export function Shell() {
  const tasksState = useTasks()
  const treeState = useProjectTree()
  const tasks = useMemo(() => tasksState.data ?? [], [tasksState.data])
  const wb = useWorkbench()
  const navigate = useNavigate()

  const [overlay, setOverlay] = useState<OverlayKind>('none')
  const [wizardOpen, setWizardOpen] = useState(false)
  const machinesState = useMachines(wizardOpen)
  const tickets = useGlobalTickets(tasks)

  const onUnregister = async (name: string, machine: string) => {
    await deleteProject(name, machine)
    treeState.refresh()
  }

  // openTaskTui 是「点一个任务 → 在它所在目录开 TUI tab」的唯一实现。
  // 左栏任务行、看板卡片、/tasks/:id 深链三条路径都走它，避免三份各自漂移。
  const openTaskTui = (base: BaseDir | null, taskId: string) => {
    setOverlay('none')
    wb.open({ kind: 'tui', taskId }, base ?? undefined)
  }

  // currentTaskId 是当前目录上「最该看的那个任务」，只用于右栏 M 角标的数据源。
  // 一个目录下可能有多个任务，取第一个正在跑的，没有就取第一个——角标是装饰，
  // 选谁都不影响正确性，但要稳定（不随渲染抖动）。
  const currentTaskId = useMemo(() => {
    if (!wb.base || wb.base.kind !== 'workspace') return null
    const under = tasks.filter((t) => t.work_dir === wb.base?.path)
    return under.find((t) => t.state === 'running')?.id ?? under[0]?.id ?? null
  }, [tasks, wb.base])

  return (
    <div className="flex h-dvh bg-background">
      <aside role="complementary" className="flex w-[260px] shrink-0 flex-col overflow-y-auto border-r bg-sidebar">
        {treeState.sessionExpired && <SessionExpiredBanner />}
        {treeState.disconnected && !treeState.sessionExpired && (
          <DisconnectedBanner message={treeState.errorText} compact />
        )}
        {treeState.data && (
          <ProjectTree
            tree={treeState.data}
            tasks={tasks}
            selectedKey={wb.base?.key ?? null}
            ticketCount={tickets.count}
            onSelectDir={wb.select}
            onOpenTask={openTaskTui}
            onOpenBoard={() => setOverlay('board')}
            onOpenTickets={() => setOverlay('tickets')}
            onOpenSettings={() => navigate('/settings')}
            onAddProject={() => setWizardOpen(true)}
            onUnregister={onUnregister}
          />
        )}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {wb.base && <Breadcrumb base={wb.base} onSplit={wb.split} />}
        <main className="min-h-0 flex-1">
          <Routes>
            <Route
              path="/settings"
              element={<SettingsPage onClose={() => navigate('/')} />}
            />
            <Route path="/machines" element={<Navigate to="/settings" replace />} />
            <Route path="/tasks/:id" element={<TaskDeepLink tree={treeState.data} tasks={tasks} onOpen={openTaskTui} />} />
            <Route
              path="*"
              element={
                <WorkbenchPage
                  api={wb}
                  onAddProject={() => setWizardOpen(true)}
                  renderContent={(c, base) => {
                    switch (c.kind) {
                      case 'terminal':
                        return <TerminalTab base={base} seq={c.seq} />
                      case 'file':
                        return <FileTab base={base} rel={c.rel} />
                      case 'tui':
                        return <TuiTab taskId={c.taskId} />
                      default:
                        return null
                    }
                  }}
                />
              }
            />
          </Routes>
        </main>
      </div>

      {wb.base && wb.base.kind === 'workspace' && (
        <div className="w-[280px] shrink-0">
          <FileTree
            base={wb.base}
            taskId={currentTaskId}
            onOpenFile={(rel) => wb.open({ kind: 'file', rel })}
          />
        </div>
      )}

      <FloatingNewPane onNewTerminal={() => wb.openTerminal(HOME_BASE)} />

      {overlay === 'board' && (
        <BoardOverlay
          tasksState={tasksState}
          tree={treeState.data}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}
      {overlay === 'tickets' && (
        <TicketsOverlay
          tickets={tickets}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}

      <AddProjectWizard
        open={wizardOpen}
        machines={machinesState.data?.machines ?? []}
        onClose={() => setWizardOpen(false)}
        onDone={() => treeState.refresh()}
      />
    </div>
  )
}

// TaskDeepLink 承接 /tasks/:id 这条 W3b 留下的深链。
//
// 为什么保留：已有书签与 --notify 的通知文案里都可能带这个地址，直接删路由会
// 让它们 404。行为改为「选中该任务所在目录 + 开它的 TUI tab + 换回 /」——地址栏
// 不停在一个不再有对应页面的路径上。
function TaskDeepLink({
  tree,
  tasks,
  onOpen,
}: {
  tree: ProjectTreeResp | null
  tasks: Task[]
  onOpen: (base: BaseDir | null, taskId: string) => void
}) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [done, setDone] = useState(false)

  useEffect(() => {
    // 等树到位再解析目录：树还没来时目录解析不出来，会把 tab 开在错的基准上
    if (!id || done || !tree) return
    onOpen(findBaseOfTask(tree, tasks, id), id)
    setDone(true)
    navigate('/', { replace: true })
  }, [id, done, tree, tasks, onOpen, navigate])

  return <p className="p-4 text-sm text-muted-foreground">正在打开任务…</p>
}

```

（`findBaseOfTask` **不在这里定义**——它是 Task 10 从 `ProjectTree.tsx` 导出的，与 `workspaceBase` / `tasksOfWorkspace` 同源；Task 12 的看板弹层要用同一个。本文件只 import。）

补齐 import：`useEffect` 从 react、`HOME_BASE` 从 `../workbench/useWorkbench`、`ProjectTreeResp` 从 `../../api/types`。删掉不再用到的 `ProjectNode` / `Workspace` / `BoardFilter` / `EMPTY_FILTER` / `PollState` / `Outlet` / `useOutletContext` 等 import——留着会被 lint 挡下。

- [ ] **Step 5: 改 App.tsx，删 TopTabs**

`web/src/App.tsx` 整体替换为：

```tsx
// 控制台入口：路由骨架。
//
// W4 起中央不再是「一个路由一个页面」，而是一套 tab 工作台。路由只剩三件事：
//
//   /          工作台（三栏：导航树 / tab 工作台 / 文件树）
//   /settings  三栏不变，中央整页换成设置页（spec §6：设置不是弹层）
//   /tasks/:id 深链承接：选中任务所在目录 + 开它的 TUI tab + 换回 /
//   /machines  重定向到 /settings（开发机页收进设置页，spec §8.4）
//
// 具体的路由分发在 Shell 内部（它要在三栏布局里只替换中央那一块），这里只负责
// 把整棵树交给 Shell 并套上 Router。
//
// 用 BrowserRouter（vite dev server 自带 history fallback）；W5 把前端 embed 进
// agentd 时，agentd 需要对未知路径回落 index.html——已记入 web/README.md。
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { Shell } from './app/shell/Shell'

// AppRoutes 单独导出，供测试用 MemoryRouter 指定初始路径。
export function AppRoutes() {
  return (
    <Routes>
      <Route path="*" element={<Shell />} />
    </Routes>
  )
}

function App() {
  return (
    <BrowserRouter>
      <AppRoutes />
    </BrowserRouter>
  )
}

export default App
```

```bash
git rm web/src/app/shell/TopTabs.tsx
grep -rn "TopTabs\|useShellContext\|ShellContext" web/src/
```

第二条命令必须无输出（`BoardPage` / `MachinesPage` 里对 `useShellContext` 的调用在 Task 12 / Task 14 改成 props，若此时还有残留，说明那两个任务的顺序需要提前——**照本计划的顺序，这里会有残留，属预期**：Task 12 与 Task 14 落地前 `npm run build` 不绿。为避免长时间红灯，本任务与 Task 12、Task 14 应连续执行、最后一起验收构建）。

- [ ] **Step 6: 跑测试确认通过**

先把 Task 12 / 13 / 14 / 15 落地（它们提供 `BoardOverlay` / `TicketsOverlay` / `useGlobalTickets` / `SettingsPage` / `FloatingNewPane`），再回到这里跑：

```bash
npm --prefix web run test && npm --prefix web run typecheck && npm --prefix web run lint && npm --prefix web run build
```

Expected: 全绿。**这是本期第一次要求整套构建通过。**

- [ ] **Step 7: 提交**

```bash
git add web/src/App.tsx web/src/app/shell/
git commit -m "feat(web): 三栏外框、面包屑与路由改写，删除顶部 tab 条"
```

---

## Task 12: 弹层基座与看板弹层

**Files:**
- Create: `web/src/app/overlay/Overlay.tsx`
- Create: `web/src/app/overlay/BoardOverlay.tsx`
- Modify: `web/src/app/board/BoardPage.tsx`（去掉 `useShellContext`，改为受控 props）
- Test: `web/src/app/overlay/Overlay.test.tsx`

**Interfaces:**
- Consumes: 既有 `BoardPage` 的列与卡片、`FilterBar`、`applyFilter`、`EMPTY_FILTER`
- Produces:
  - `Overlay({ title, onClose, children, wide })`
  - `BoardOverlay({ tasksState, tree, onOpenTask, onClose })`
  - `BoardPage` 的新签名：`BoardPage({ tasksState, tree, onOpenTask })`（filter 改为内部状态）

**为什么 `BoardPage` 的 filter 改成内部状态**：W3 里 filter 由 Shell 持有，因为左栏和看板都要写它。Task 10 之后左栏不再写 filter，唯一的编辑入口就是看板自己的 `FilterBar`。状态提到 Shell 只会让「关掉看板再打开，筛选是否保留」这个问题变得需要决定——放在 `BoardOverlay` 里，答案自然是「每次打开重置」，而这正是想要的：弹层是「扫一眼总账」，带着上次的筛选打开反而容易漏看。

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/overlay/Overlay.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Overlay } from './Overlay'
import { BoardOverlay } from './BoardOverlay'
import type { ProjectTreeResp, Task } from '../../api/types'
import type { PollState } from '../data/usePoll'

function pollState(tasks: Task[]): PollState<Task[]> {
  return {
    data: tasks,
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  } as unknown as PollState<Task[]>
}

const task = {
  id: 'T1',
  name: '重构工单通道',
  state: 'waiting_answer',
  executor: 'opencode',
  branch: 'feat/x',
  machine: '',
  project_id: 'P1',
  work_dir: '/w/b2-b3',
  updated_at: '2026-08-12T00:00:00Z',
} as unknown as Task

const tree = {
  projects: [
    {
      project_id: 'P1',
      name: 'handoff',
      locations: [
        {
          machine: '',
          name: 'handoff',
          path: '/w',
          probe_error: '',
          workspaces: [{ path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'abc', is_main: false, managed: false }],
        },
      ],
    },
  ],
  machines: [],
  unowned: [],
} as unknown as ProjectTreeResp

describe('Overlay', () => {
  it('渲染标题与内容，并有关闭按钮', () => {
    render(
      <Overlay title="任务看板" onClose={vi.fn()}>
        <p>内容</p>
      </Overlay>,
    )
    expect(screen.getByRole('dialog', { name: '任务看板' })).toBeInTheDocument()
    expect(screen.getByText('内容')).toBeInTheDocument()
  })

  it('Esc 关闭', () => {
    const onClose = vi.fn()
    render(<Overlay title="x" onClose={onClose}>内容</Overlay>)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('点遮罩关闭，点内容不关', () => {
    const onClose = vi.fn()
    render(<Overlay title="x" onClose={onClose}>内容</Overlay>)
    fireEvent.click(screen.getByText('内容'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('overlay-backdrop'))
    expect(onClose).toHaveBeenCalled()
  })

  it('点关闭按钮关闭', () => {
    const onClose = vi.fn()
    render(<Overlay title="x" onClose={onClose}>内容</Overlay>)
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(onClose).toHaveBeenCalled()
  })
})

describe('BoardOverlay', () => {
  it('四列都在，卡片按状态落列', () => {
    render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={vi.fn()} onClose={vi.fn()} />,
    )
    for (const label of ['等待执行', '进行中', 'Review', '完成']) {
      expect(screen.getByRole('heading', { name: label })).toBeInTheDocument()
    }
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.getByText('等你答复')).toBeInTheDocument()
  })

  it('点卡片关闭弹层并带上任务所在目录', () => {
    const onOpenTask = vi.fn()
    const onClose = vi.fn()
    render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={onOpenTask} onClose={onClose} />,
    )
    fireEvent.click(screen.getByText('重构工单通道'))
    expect(onOpenTask).toHaveBeenCalledWith(expect.objectContaining({ key: '/w/b2-b3' }), 'T1')
    expect(onClose).toHaveBeenCalled()
  })

  it('筛选栏在弹层内，且每次打开都是空筛选', () => {
    const { unmount } = render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={vi.fn()} onClose={vi.fn()} />,
    )
    fireEvent.change(screen.getByPlaceholderText(/搜索/), { target: { value: '不存在的任务' } })
    expect(screen.queryByText('重构工单通道')).not.toBeInTheDocument()
    unmount()

    render(
      <BoardOverlay tasksState={pollState([task])} tree={tree} onOpenTask={vi.fn()} onClose={vi.fn()} />,
    )
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- Overlay
```

Expected: FAIL，模块解析失败。

- [ ] **Step 3: 写 Overlay**

新建 `web/src/app/overlay/Overlay.tsx`：

```tsx
// Overlay —— 弹出层基座：遮罩 + 标题栏 + Esc 关闭 + 点遮罩关闭。
//
// 职责：给看板与工单两个弹层提供同一套壳，让它们的关闭方式、层级、尺寸一致。
//
// 边界：
//   - 不管内容。内容组件自己取数、自己排版
//   - **同时只允许一个弹层**（spec §0）：这条约束由调用方（Shell 的 OverlayKind）
//     保证，本组件不做栈——做了栈就等于允许叠加，Esc 关哪个又要另定规则
//
// 焦点：挂载时把焦点收到面板上，卸载时不主动还原（还原到哪个元素在 tab 系统里
// 不好界定，交给浏览器默认行为比猜一个错的好）。
import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface OverlayProps {
  title: string
  onClose: () => void
  children: ReactNode
  // wide 给看板用：四列横排需要更宽的面板
  wide?: boolean
}

export function Overlay({ title, onClose, children, wide }: OverlayProps) {
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    panelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
      <div
        data-testid="overlay-backdrop"
        className="absolute inset-0 bg-black/40"
        onClick={onClose}
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className={cn(
          'relative flex max-h-full w-full flex-col rounded-lg border bg-background shadow-xl outline-none',
          wide ? 'max-w-6xl' : 'max-w-3xl',
        )}
      >
        <header className="flex items-center gap-2 border-b px-4 py-2.5">
          <h2 className="text-sm font-semibold">{title}</h2>
          <button
            type="button"
            aria-label="关闭"
            onClick={onClose}
            className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="size-4" />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-auto">{children}</div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 把 BoardPage 改成受控组件**

`web/src/app/board/BoardPage.tsx`：

- 删掉 `import { useShellContext } from '../shell/Shell'`。
- 签名改为：

```tsx
export interface BoardPageProps {
  tasksState: PollState<Task[]>
  tree: ProjectTreeResp | null
  // onOpenTask 的首参是任务所在目录（在树上 join 得到），null = 未归属或目录已不在
  onOpenTask: (base: BaseDir | null, taskId: string) => void
}

export function BoardPage({ tasksState, tree, onOpenTask }: BoardPageProps) {
  const [filter, setFilter] = useState<BoardFilter>(EMPTY_FILTER)
  const tasks = tasksState.data ?? []
  // …以下原样…
```

- 文件头注释里「filter 是唯一真相，本页与左栏项目树是它的两个编辑入口」那段改为：

```
// 筛选：全部在客户端做（applyFilter）。W4 起左栏不再写 filter（它改成了导航），
// 所以本页的 FilterBar 是唯一编辑入口，filter 就地持有。弹层每次打开都是新实例，
// 筛选因此每次重置——「扫一眼总账」时带着上次的筛选打开容易漏看。
```

- 卡片的 `onOpen` 从 `onOpen(t.id)` 改为 `onOpen(findBaseOfTask(tree, tasks, t.id), t.id)`。`findBaseOfTask` 是 Task 10 从 `web/src/app/tree/ProjectTree.tsx` 导出的那一个，Shell 用的也是它——**不要在本文件里再写一份反查**，两份必然会分叉。`tree` 为 `null` 时（树还没到）直接传 `null`，Shell 会退回用当前选中目录开。
- `main` 的外框去掉 `min-h-dvh` 之类的整页假设，改为 `flex w-full flex-col gap-3 p-3`（原样即可）。

- [ ] **Step 5: 写 BoardOverlay**

新建 `web/src/app/overlay/BoardOverlay.tsx`：

```tsx
// BoardOverlay —— 任务看板弹层（spec §5.1）。
//
// 职责：把既有的 BoardPage 装进 Overlay 基座，并在点开卡片时关掉自己。
//
// 边界：
//   - 看板内容一行不重写。四列布局、卡片、干预态标记全部是 BoardPage 的既有实现
//   - **全局，不被当前选中目录过滤**（spec §1.3）：它是「你还欠哪些没处理」的
//     总账，被当前选中项筛掉会直接导致漏处理
//
// 点卡片的行为（原型 AGENTS.md：Opening an actionable card returns to its existing
// task session in the workbench）：关闭弹层 → 选中该任务所在目录 → 在中央开它的
// TUI tab。三件事的实现在 Shell 的 openTaskTui 里，这里只负责先关自己。
import type { ProjectTreeResp, Task } from '../../api/types'
import type { PollState } from '../data/usePoll'
import type { BaseDir } from '../workbench/useWorkbench'
import { BoardPage } from '../board/BoardPage'
import { Overlay } from './Overlay'

export interface BoardOverlayProps {
  tasksState: PollState<Task[]>
  tree: ProjectTreeResp | null
  onOpenTask: (base: BaseDir | null, taskId: string) => void
  onClose: () => void
}

export function BoardOverlay({ tasksState, tree, onOpenTask, onClose }: BoardOverlayProps) {
  return (
    <Overlay title="任务看板" onClose={onClose} wide>
      <BoardPage
        tasksState={tasksState}
        tree={tree}
        onOpenTask={(base, id) => {
          onClose()
          onOpenTask(base, id)
        }}
      />
    </Overlay>
  )
}
```

（弹层不单独接 `tasks`：`tasksState.data` 已经是同一份，多一个入参就多一处可能对不上的真相。）

- [ ] **Step 6: 跑测试确认通过**

```bash
npm --prefix web run test -- Overlay board
```

Expected: `Overlay` 七条 + 既有 board 用例全 PASS。（`BoardPage` 若有既有测试文件，同步把它改成传 props 而非 mock `useShellContext`。）

- [ ] **Step 7: 提交**

```bash
git add web/src/app/overlay/Overlay.tsx web/src/app/overlay/BoardOverlay.tsx \
        web/src/app/overlay/Overlay.test.tsx web/src/app/board/BoardPage.tsx \
        web/src/app/tree/ProjectTree.tsx web/src/app/shell/Shell.tsx
git commit -m "feat(web): 弹层基座与看板弹层，看板改为受控组件"
```

---

## Task 13: 全局工单 —— 跨任务聚合、工单弹层、左栏角标、EventMark 文案

**Files:**
- Create: `web/src/app/overlay/useGlobalTickets.ts`
- Create: `web/src/app/overlay/TicketsOverlay.tsx`
- Modify: `web/src/app/task/EventMark.tsx`
- Modify: `web/src/app/task/blocks.test.tsx`
- Test: `web/src/app/overlay/useGlobalTickets.test.ts`
- Test: `web/src/app/overlay/TicketsOverlay.test.tsx`

**Interfaces:**
- Consumes: `fetchTaskDetail` / `replyTicket`；既有 `TicketsPanel`
- Produces:
  - `interface GlobalTicket { ticket: Ticket; task: Task }`
  - `interface GlobalTickets { items: GlobalTicket[]; count: number; refresh: () => void }`
  - `useGlobalTickets(tasks: Task[]): GlobalTickets`
  - `TicketsOverlay({ tickets, onOpenTask, onClose })`

**数据来源（spec §5.2，无新接口）**：`pending_tickets` 只在 `GET /api/tasks/{id}` 里有。从任务流筛出 `state === 'waiting_answer'` 的任务，逐个取详情。N+1 在这里可接受——`waiting_answer` 的任务每一个都在阻塞一个人，数量天然极小。**若将来常态化到两位数，再加 `GET /api/tickets` 汇总接口。**

- [ ] **Step 1: 写 useGlobalTickets 的失败测试**

新建 `web/src/app/overlay/useGlobalTickets.test.ts`：

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import type { Task } from '../../api/types'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchTaskDetail: vi.fn() }
})
const { fetchTaskDetail } = await import('../../api/client')
const { useGlobalTickets } = await import('./useGlobalTickets')

afterEach(() => vi.mocked(fetchTaskDetail).mockReset())

const waiting = (id: string) => ({ id, state: 'waiting_answer', name: id }) as unknown as Task
const running = (id: string) => ({ id, state: 'running', name: id }) as unknown as Task

describe('useGlobalTickets', () => {
  it('只对 waiting_answer 的任务取详情', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ pending_tickets: [{ id: 'K1' }] } as never)
    const { result } = renderHook(() => useGlobalTickets([waiting('A'), running('B')]))
    await waitFor(() => expect(result.current.count).toBe(1))
    expect(fetchTaskDetail).toHaveBeenCalledTimes(1)
    expect(fetchTaskDetail).toHaveBeenCalledWith('A')
  })

  it('一个任务挂多张工单时全部计入', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ pending_tickets: [{ id: 'K1' }, { id: 'K2' }] } as never)
    const { result } = renderHook(() => useGlobalTickets([waiting('A')]))
    await waitFor(() => expect(result.current.count).toBe(2))
    expect(result.current.items.map((i) => i.ticket.id)).toEqual(['K1', 'K2'])
    expect(result.current.items[0].task.id).toBe('A')
  })

  it('某个任务取详情失败不影响其余任务', async () => {
    vi.mocked(fetchTaskDetail).mockImplementation(async (id: string) => {
      if (id === 'A') throw new Error('炸了')
      return { pending_tickets: [{ id: 'K9' }] } as never
    })
    const { result } = renderHook(() => useGlobalTickets([waiting('A'), waiting('B')]))
    await waitFor(() => expect(result.current.count).toBe(1))
    expect(result.current.items[0].ticket.id).toBe('K9')
  })

  it('没有 waiting_answer 任务时不发请求，count 为 0', () => {
    renderHook(() => useGlobalTickets([running('B')]))
    expect(fetchTaskDetail).not.toHaveBeenCalled()
  })

  it('waiting_answer 的任务集合没变时不重复取数', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ pending_tickets: [{ id: 'K1' }] } as never)
    const { rerender, result } = renderHook(({ ts }: { ts: Task[] }) => useGlobalTickets(ts), {
      initialProps: { ts: [waiting('A'), running('B')] },
    })
    await waitFor(() => expect(result.current.count).toBe(1))
    // 任务流每 2.5s 换一个新数组，但 waiting_answer 的 id 集合没变
    rerender({ ts: [waiting('A'), running('B')] })
    expect(fetchTaskDetail).toHaveBeenCalledTimes(1)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- useGlobalTickets
```

Expected: FAIL，模块解析失败。

- [ ] **Step 3: 写 useGlobalTickets**

新建 `web/src/app/overlay/useGlobalTickets.ts`：

```ts
// useGlobalTickets —— 跨任务的挂起工单聚合（spec §5.2）。
//
// 职责：给左栏角标与工单弹层提供「此刻一共欠多少张工单、分别属于谁」。
//
// 边界：
//   - 不做裁决：应答走 TicketsPanel → replyTicket，本 hook 只提供 refresh
//   - 不跨机：前端目前只请求本机 /api/tasks，不带 ?scope=all（spec §5.2 末段）
//
// 取数形态（无新接口）：pending_tickets 只出现在 GET /api/tasks/{id} 的响应里，
// 任务列表上没有。所以从任务流筛出 state === 'waiting_answer' 的任务，逐个取详情。
// 这是 N+1，但可接受：waiting_answer 的任务每一个都在**阻塞一个人**，数量天然
// 极小。若将来常态化到两位数，再加 GET /api/tickets 汇总接口——那时它有真实依据。
//
// 为什么按 id 集合而不是按数组身份去重触发：任务流每 2.5s 换一个新数组，按数组
// 身份做依赖会每 2.5s 打一轮 N+1 请求。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { fetchTaskDetail } from '../../api/client'
import type { Task, Ticket } from '../../api/types'

export interface GlobalTicket {
  ticket: Ticket
  task: Task
}

export interface GlobalTickets {
  items: GlobalTicket[]
  count: number
  refresh: () => void
}

export function useGlobalTickets(tasks: Task[]): GlobalTickets {
  const [items, setItems] = useState<GlobalTicket[]>([])
  const [nonce, setNonce] = useState(0)

  const waiting = useMemo(() => tasks.filter((t) => t.state === 'waiting_answer'), [tasks])
  // key 是 waiting 任务的 id 集合的稳定表示。任务流每 2.5s 换新数组，但只要这批
  // id 没变就不需要重新取详情。
  const key = useMemo(() => waiting.map((t) => t.id).sort().join(','), [waiting])

  useEffect(() => {
    if (key === '') {
      setItems([])
      return
    }
    let cancelled = false
    const ids = key.split(',')
    // 逐个任务取详情；单个失败只丢它自己那份，不连累其余（诚实降级）
    Promise.all(
      ids.map(async (id) => {
        try {
          const d = await fetchTaskDetail(id)
          const task = waiting.find((t) => t.id === id)
          if (!task) return []
          return (d.pending_tickets ?? []).map((ticket) => ({ ticket, task }))
        } catch {
          return []
        }
      }),
    ).then((groups) => {
      if (!cancelled) setItems(groups.flat())
    })
    return () => {
      cancelled = true
    }
    // waiting 故意不进依赖：它每 2.5s 是新数组，而 key 已经代表了它的身份
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, nonce])

  const refresh = useCallback(() => setNonce((n) => n + 1), [])

  return { items, count: items.length, refresh }
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
npm --prefix web run test -- useGlobalTickets
```

Expected: 五条全 PASS。

- [ ] **Step 5: 写 TicketsOverlay 的失败测试**

新建 `web/src/app/overlay/TicketsOverlay.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { TicketsOverlay } from './TicketsOverlay'
import type { GlobalTickets } from './useGlobalTickets'

const item = {
  ticket: { id: 'K1', kind: 'question', question: '要不要加重试？' },
  task: { id: 'T1', name: '重构工单通道', machine: '', work_dir: '/w/b2-b3', project_id: 'P1' },
} as unknown as GlobalTickets['items'][number]

const tickets: GlobalTickets = { items: [item], count: 1, refresh: vi.fn() }

describe('TicketsOverlay', () => {
  it('列出工单并标注它属于哪个任务与目录', () => {
    render(<TicketsOverlay tickets={tickets} onOpenTask={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByRole('dialog', { name: /工单/ })).toBeInTheDocument()
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.getByText('/w/b2-b3')).toBeInTheDocument()
  })

  it('每行有「跳到该任务」，点了关闭弹层', () => {
    const onOpenTask = vi.fn()
    const onClose = vi.fn()
    render(<TicketsOverlay tickets={tickets} onOpenTask={onOpenTask} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: '跳到该任务' }))
    expect(onOpenTask).toHaveBeenCalledWith(null, 'T1')
    expect(onClose).toHaveBeenCalled()
  })

  it('一张工单都没有时给出明确空态，不是空白', () => {
    render(<TicketsOverlay tickets={{ items: [], count: 0, refresh: vi.fn() }} onOpenTask={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText(/没有待处理的工单/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 6: 写 TicketsOverlay**

新建 `web/src/app/overlay/TicketsOverlay.tsx`：

```tsx
// TicketsOverlay —— 全局工单弹层（spec §5.2）。
//
// 职责：把所有目录下的挂起工单列在一处，就地裁决；每行标注它属于哪个任务与目录，
// 并提供「跳到该任务」。
//
// 边界：
//   - **全局，不被当前选中目录过滤**（spec §1.3）。一张工单可能属于任何一个任务，
//     按当前目录筛掉等于要求人先猜对是哪个任务才能看到它
//   - 裁决逻辑不重写：整段复用 TicketsPanel（含「拒绝必须填理由」这条）
//
// 审批入口唯一（W4b 既定纪律，不得回退）：时间线里的 EventMark 仍然不可点，只做
// 指向；能按下批准/拒绝的地方只有这里。
import { ArrowUpRight } from 'lucide-react'
import type { BaseDir } from '../workbench/useWorkbench'
import { replyTicket } from '../../api/client'
import { TicketsPanel } from '../task/TicketsPanel'
import { Overlay } from './Overlay'
import type { GlobalTickets } from './useGlobalTickets'

export interface TicketsOverlayProps {
  tickets: GlobalTickets
  onOpenTask: (base: BaseDir | null, taskId: string) => void
  onClose: () => void
}

export function TicketsOverlay({ tickets, onOpenTask, onClose }: TicketsOverlayProps) {
  return (
    <Overlay title={`工单（${tickets.count}）`} onClose={onClose}>
      {tickets.items.length === 0 ? (
        <p className="p-6 text-center text-sm text-muted-foreground">没有待处理的工单。</p>
      ) : (
        <ul className="divide-y">
          {tickets.items.map(({ ticket, task }) => (
            <li key={ticket.id} className="p-3">
              <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
                <span className="truncate font-medium text-foreground">{task.name || task.id}</span>
                <span aria-hidden>·</span>
                <span className="truncate font-mono">{task.work_dir || '（原地）'}</span>
                <span aria-hidden>·</span>
                <span className="shrink-0">{task.machine === '' ? '本机' : task.machine}</span>
                <button
                  type="button"
                  onClick={() => {
                    onClose()
                    // 基准目录传 null：这里没有树，解析目录是 Shell 的活
                    onOpenTask(null, task.id)
                  }}
                  className="ml-auto inline-flex shrink-0 items-center gap-0.5 rounded px-1.5 py-0.5 hover:bg-accent hover:text-foreground"
                >
                  跳到该任务
                  <ArrowUpRight className="size-3" />
                </button>
              </div>
              <TicketsPanel
                tickets={[ticket]}
                disabled={false}
                onReply={async (t, answer) => {
                  await replyTicket(task.id, { ticket_id: t.id, answer })
                  tickets.refresh()
                }}
              />
            </li>
          ))}
        </ul>
      )}
    </Overlay>
  )
}
```

**核对 `TicketsPanel` 的实际入参**：先 `grep -n "export function TicketsPanel" -A 12 web/src/app/task/TicketsPanel.tsx`。若它自带卡片外框与「审批台」标题，在弹层里逐条重复会很吵——此时给它加一个 `bare?: boolean`（缺省 false 保持现状），为真时去掉外框与标题，只渲染工单本体与操作按钮，并在 `TicketsPanel.test.tsx` 补一条 `bare` 用例。

**`onOpenTask` 首参传 `null` 的含义**：Shell 的 `openTaskTui` 收到 `null` 时会用当前选中目录。这对「跳到该任务」是错的——应该跳到**任务自己**的目录。所以 Shell 的 `openTaskTui` 改为：首参为 `null` 时，用 `findBaseOfTask(tree, tasks, taskId)` 自己解析一次；解析不出（未归属）才退回当前目录。把这条写进 `openTaskTui` 的注释，并在 Shell 的测试里补一条：从工单弹层点「跳到该任务」，面包屑切到了该任务的目录。

- [ ] **Step 7: 改 EventMark 的指向文案**

`web/src/app/task/EventMark.tsx:40` 的 `' · 裁决入口在右侧工单区'` 改为 `' · 裁决入口在左栏底部的工单面板'`。同时把文件头注释里「TicketsPanel 管裁决」那条更新为「全局工单弹层管裁决（左栏底部入口）」。

`web/src/app/task/blocks.test.tsx` 里断言 `/工单区/` 的用例改为断言 `/工单面板/`：

```bash
grep -n "工单区" web/src/
```

必须只剩 0 处。

- [ ] **Step 8: 跑测试确认通过**

```bash
npm --prefix web run test -- useGlobalTickets TicketsOverlay blocks TicketsPanel
```

Expected: 全 PASS。

- [ ] **Step 9: 提交**

```bash
git add web/src/app/overlay/ web/src/app/task/EventMark.tsx web/src/app/task/blocks.test.tsx \
        web/src/app/task/TicketsPanel.tsx web/src/app/task/TicketsPanel.test.tsx
git commit -m "feat(web): 全局工单聚合与工单弹层，审批入口收敛到左栏底部"
```

---

## Task 14: 设置页（开发机 / 常规 / Env 三个分区）

**Files:**
- Create: `web/src/app/settings/SettingsPage.tsx`
- Modify: `web/src/app/machines/MachinesPage.tsx`
- Modify: `web/src/app/machines/MachinesPage.test.tsx`
- Test: `web/src/app/settings/SettingsPage.test.tsx`

**Interfaces:**
- Consumes: 既有 `MachinesPage` / `MachineDetail` / `useMachines` / `useProjectTree`
- Produces:
  - `SettingsPage({ onClose })`
  - `MachinesPage` 的新签名：`MachinesPage({ tree })`（去掉 `useShellContext`，去掉自己的 `<main>` 外框）

**为什么设置是整页替换中央而不是弹层**（spec §6，写进文件头注释）：它内容重、有多个分区，塞进弹层会挤；它是低频配置动作，不需要「扫一眼就回去」的弹层语义；原型的开发机页本来就是这个形态。同时也少一层弹层叠加的可能——看板与工单已经是弹层，设置再做成弹层就要处理「弹层上开弹层」。

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/settings/SettingsPage.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { SettingsPage } from './SettingsPage'

vi.mock('../data/useMachines', () => ({
  useMachines: () => ({
    data: { machines: [{ name: '', ok: true, error: '', version: 'v1' }] },
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  }),
}))
vi.mock('../data/useProjectTree', () => ({
  useProjectTree: () => ({
    data: { projects: [], machines: [], unowned: [] },
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  }),
}))

describe('SettingsPage', () => {
  it('三个分区都在，缺省停在开发机', async () => {
    render(<SettingsPage onClose={vi.fn()} />)
    expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument()
    for (const label of ['开发机', '常规', 'Env 文件']) {
      expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
    }
    await waitFor(() => expect(screen.getByText('本机')).toBeInTheDocument())
  })

  it('切到常规分区显示明确的空占位，而不是空白', () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '常规' }))
    expect(screen.getByText(/本期没有可配置项/)).toBeInTheDocument()
  })

  it('切到 Env 文件分区说明本期不做', () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Env 文件' }))
    expect(screen.getByText(/本期不做/)).toBeInTheDocument()
  })

  it('返回工作台调 onClose', () => {
    const onClose = vi.fn()
    render(<SettingsPage onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: '返回工作台' }))
    expect(onClose).toHaveBeenCalled()
  })
})
```

同时在 `web/src/app/machines/MachinesPage.test.tsx` 里加一条：

```tsx
it('三个未接线的操作可点，点了明说尚未实现（不置灰）', () => {
  render(<MachinesPage tree={tree} />)
  fireEvent.click(screen.getByText('本机'))
  for (const label of ['可用执行者', '重启 agent', '打开终端']) {
    const btn = screen.getByRole('button', { name: new RegExp(label) })
    expect(btn).not.toBeDisabled()
    fireEvent.click(btn)
  }
  expect(screen.getAllByText(/尚未实现/).length).toBeGreaterThan(0)
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- SettingsPage
```

Expected: FAIL，模块解析失败。

- [ ] **Step 3: 改 MachinesPage 为可嵌入分区**

- 删掉 `import { useShellContext } from '../shell/Shell'`，签名改为 `export function MachinesPage({ tree }: { tree: ProjectTreeResp | null })`。
- 去掉组件最外层的 `<main>` / 整页 padding，改为 `<div className="flex h-full min-h-0">`——它现在是设置页里的一块，不是一个页面。
- `useMachines(true)` 保留：设置页的开发机分区可见时才挂载，卸载即停表，与原来的语义一致。文件头注释里「只在 /machines 可见时开表」改为「只在设置页的开发机分区可见时开表」。
- 文件头「不渲染任何未实现功能（配对/重启/终端/Env/操作系统格），不留置灰入口」这条改为：

```
// 三个未接线的操作（可用执行者开关 / 重启 agent / 打开终端）本期**只渲染不接线**：
// 它们需要 agentd 侧的写接口，不在本期。按「不置灰」纪律（spec §0），它们可点，
// 点了给出明确的「尚未实现」说明，而不是一个永远按不动的灰按钮。
// 配对开发机与 Env 文件仍然不渲染——那两项连形态都还没定。
```

- 在 `MachineDetail` 里加这三项（若 `MachineDetail` 已有对应位置就填进去，没有就在详情底部加一段 `<section>`）：

```tsx
// NOT_WIRED 是三个「形态已定、后端未做」的操作。点击后就地展开一句说明——
// 不置灰（置灰承诺"以后能用"，用户会反复点），也不静默无反应。
const NOT_WIRED = [
  { key: 'executors', label: '可用执行者', note: '执行者开关尚未实现：需要 agentd 提供机器级配置的写接口。' },
  { key: 'restart', label: '重启 agent', note: '重启尚未实现：需要 agentd 提供自重启接口，且要先想清楚重启期间在跑的任务怎么办。' },
  { key: 'terminal', label: '打开终端', note: '终端尚未实现：PTY 后端未做，当前请用 handoff attach <task>。' },
]
```

配一个 `const [openNote, setOpenNote] = useState<string | null>(null)`，点按钮 `setOpenNote(item.key)`，展开时在按钮下方渲染 `note`。

- [ ] **Step 4: 写 SettingsPage**

新建 `web/src/app/settings/SettingsPage.tsx`：

```tsx
// SettingsPage —— 设置页（spec §6）。
//
// 职责：把配置性质的内容集中到一处：开发机、常规、Env 文件。
//
// 形态：**整页替换中央内容区，左栏保持可见——不是弹出层。** 三条理由：
//   - 内容重、有多个分区，塞进弹层会挤
//   - 低频配置动作，不需要「扫一眼就回去」的弹层语义
//   - 原型的开发机页本来就是这个形态，这点不必偏离
// 设置若也做成弹层，就要处理「弹层上开弹层」——spec §0 要求同时只有一个弹层。
//
// 边界：
//   - 退出设置回到工作台时，中央 tab 组与当前选中目录保持不变——它们由
//     useWorkbench 持有，与本页无关，天然满足
//   - 不自己取项目树：树流在 Shell 手里，这里按需拉一份只读的（useProjectTree
//     内部有共享，不会打出第二条轮询）。若实测出现双份轮询，改为由 Shell 传入
import { useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import { useProjectTree } from '../data/useProjectTree'
import { MachinesPage } from '../machines/MachinesPage'
import { cn } from '@/lib/utils'

// SECTIONS 是设置页的三个分区。顺序即原型的顺序：开发机在最上（唯一有真内容的）。
const SECTIONS = [
  { key: 'machines', label: '开发机' },
  { key: 'general', label: '常规' },
  { key: 'env', label: 'Env 文件' },
] as const

type SectionKey = (typeof SECTIONS)[number]['key']

export function SettingsPage({ onClose }: { onClose: () => void }) {
  const [section, setSection] = useState<SectionKey>('machines')
  const treeState = useProjectTree()

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-3 border-b px-4 py-2.5">
        <h1 className="text-sm font-semibold">设置</h1>
        <button
          type="button"
          onClick={onClose}
          className="ml-auto inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" />
          返回工作台
        </button>
      </header>

      <div className="flex min-h-0 flex-1">
        <nav className="w-40 shrink-0 border-r p-2">
          {SECTIONS.map((s) => (
            <button
              key={s.key}
              type="button"
              onClick={() => setSection(s.key)}
              aria-current={section === s.key ? 'true' : undefined}
              className={cn(
                'block w-full rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-accent',
                section === s.key && 'bg-accent font-medium',
              )}
            >
              {s.label}
            </button>
          ))}
        </nav>

        <div className="min-h-0 flex-1 overflow-auto">
          {section === 'machines' && <MachinesPage tree={treeState.data} />}
          {/* 空分区也要有话说：一块空白会让人以为页面坏了（spec §0 不置灰的同源纪律） */}
          {section === 'general' && (
            <p className="p-6 text-sm text-muted-foreground">
              常规设置本期没有可配置项。桌面行为、主题、快捷键等留待后续。
            </p>
          )}
          {section === 'env' && (
            <p className="p-6 text-sm text-muted-foreground">
              Env 文件管理（每台机器下的物理 .env 文件、每个文件多个变量）本期不做。
              原型里有完整设计，等它有真实落点时再实现。
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
npm --prefix web run test -- SettingsPage MachinesPage
```

Expected: 全 PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/app/settings/ web/src/app/machines/
git commit -m "feat(web): 设置页（开发机/常规/Env 三分区），开发机页改为可嵌入分区"
```

---

## Task 15: 右下角悬浮按钮

**Files:**
- Create: `web/src/app/workbench/FloatingNewPane.tsx`
- Test: `web/src/app/workbench/FloatingNewPane.test.tsx`

**Interfaces:**
- Produces: `FloatingNewPane({ onNewTerminal })`

**硬约束（不得放宽）**：本期这个面板里**只有「新终端」一项**。理由是 spec §2.6：以 `$HOME` 为基准浏览文件要求 agentd 的文件接口接受 `$HOME` 作为根，而 `~/.handoff/config.yaml` 里存着 agentd 主令牌；控制台会话是刻意做得比主令牌弱的凭据，能读 `$HOME` 即弱凭据当场提权成强凭据。**不要"顺手"把 `BlankTab` 的三项直接搬过来。**

- [ ] **Step 1: 写失败的测试**

新建 `web/src/app/workbench/FloatingNewPane.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { FloatingNewPane } from './FloatingNewPane'

describe('FloatingNewPane', () => {
  it('缺省是收起的一个按钮', () => {
    render(<FloatingNewPane onNewTerminal={vi.fn()} />)
    expect(screen.getByRole('button', { name: '新建（以 home 为基准）' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
  })

  it('展开后只有「新终端」一项——本期不放文件与 TUI（spec §2.6）', () => {
    render(<FloatingNewPane onNewTerminal={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '新建（以 home 为基准）' }))
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /打开文件/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /打开任务 TUI/ })).not.toBeInTheDocument()
  })

  it('明说基准是 home、不挂在任何项目上', () => {
    render(<FloatingNewPane onNewTerminal={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '新建（以 home 为基准）' }))
    expect(screen.getByText(/不挂在任何项目上/)).toBeInTheDocument()
  })

  it('点新终端回调并收起面板', () => {
    const onNewTerminal = vi.fn()
    render(<FloatingNewPane onNewTerminal={onNewTerminal} />)
    fireEvent.click(screen.getByRole('button', { name: '新建（以 home 为基准）' }))
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(onNewTerminal).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
npm --prefix web run test -- FloatingNewPane
```

Expected: FAIL，模块解析失败。

- [ ] **Step 3: 写实现**

新建 `web/src/app/workbench/FloatingNewPane.tsx`：

```tsx
// FloatingNewPane —— 右下角常驻的悬浮按钮（spec §2.5）。
//
// 职责：开一个**不挂在任何项目上**的 tab。与中央 `+` 的唯一区别是基准目录是
// 用户 home，不是当前工作树。
//
// 边界（安全，不得放宽）：
//   - **本期只有「新终端」一项**。以 $HOME 为基准浏览文件要求 agentd 的文件接口
//     接受 $HOME 作为根，而 ~/.handoff/config.yaml 里存着 agentd 主令牌；控制台
//     会话是刻意做得比主令牌弱的凭据（一次性 ticket 换取、可吊销、按设备记录），
//     能读 $HOME 即弱凭据当场提权成强凭据。$HOME 下还有 ~/.ssh/ 与各种 CLI 的
//     凭据文件，问题同理（spec §2.6）
//   - 因此**不要**把 BlankTab 的三项直接搬过来。home 基准的文件浏览需要单独设计
//     （排除清单 / 显式授权目录 / 用户在设置里逐个添加可浏览根），那是独立一轮的事
//
// 形态参照 Orca 的悬浮面板：收起时是一个圆钮，展开是一张小面板。
import { useState } from 'react'
import { Plus, TerminalSquare } from 'lucide-react'

export function FloatingNewPane({ onNewTerminal }: { onNewTerminal: () => void }) {
  const [open, setOpen] = useState(false)

  if (!open) {
    return (
      <button
        type="button"
        aria-label="新建（以 home 为基准）"
        onClick={() => setOpen(true)}
        className="fixed bottom-5 right-5 z-40 flex size-11 items-center justify-center rounded-full bg-[#10151b] text-white shadow-lg hover:opacity-90"
      >
        <Plus className="size-5" />
      </button>
    )
  }

  return (
    <div className="fixed bottom-5 right-5 z-40 w-64 rounded-lg border bg-background p-2 shadow-xl">
      <div className="flex items-center px-1 pb-1">
        <span className="text-xs text-muted-foreground">基准 home（不挂在任何项目上）</span>
        <button
          type="button"
          aria-label="收起"
          onClick={() => setOpen(false)}
          className="ml-auto rounded p-0.5 text-muted-foreground hover:bg-accent"
        >
          <Plus className="size-4 rotate-45" />
        </button>
      </div>
      <button
        type="button"
        onClick={() => {
          setOpen(false)
          onNewTerminal()
        }}
        className="flex w-full items-center gap-3 rounded-md px-2 py-2 text-left text-sm hover:bg-accent"
      >
        <TerminalSquare className="size-4 shrink-0 text-muted-foreground" />
        <span className="flex-1">新终端</span>
        <span className="font-mono text-[11px] text-muted-foreground">⌘T</span>
      </button>
    </div>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
npm --prefix web run test -- FloatingNewPane
```

Expected: 四条全 PASS。

- [ ] **Step 5: 全量验收（Task 11 Step 6 在此闭合）**

```bash
npm --prefix web run test && npm --prefix web run typecheck && npm --prefix web run lint && npm --prefix web run build
go test ./... 2>&1 | tail -20
```

Expected: 全绿。Go 侧覆盖 Task 1 / Task 2 的新测试。

- [ ] **Step 6: 提交**

```bash
git add web/src/app/workbench/FloatingNewPane.tsx web/src/app/workbench/FloatingNewPane.test.tsx
git commit -m "feat(web): 右下角悬浮按钮（本期只开终端）"
```

---

## Task 16: 同步原型的 AGENTS.md（五条偏离全部回填）

**Files:**
- Modify: `prototypes/desktop-console/AGENTS.md`

**Interfaces:**
- Consumes: 无代码依赖
- Produces: 无代码产出

`AGENTS.md` 是原型目录的「已确认产品与视觉决定」清单，下一个读原型的人（人或 agent）会拿它当准绳。spec §8 的五条偏离已经把其中四条推翻了；spec §8.1 明确要求同步它，理由是「否则它会误导下一个读原型的人」。**这个理由对另外三条同样成立**，所以本任务一次把五条全部回填，不只改 8.1。

本任务只改这一个 markdown 文件，不动原型的 `src/`——原型的视觉基准仍然有效，被推翻的是几条产品结构决定，不是画面。

- [ ] **Step 1: 改 8.1（attach 原生 CLI → handoff 自渲染）**

把这一行整行删掉：

```
- handoff is the executor attachment layer. A task opens the executor's native Codex CLI/OpenCode-style TUI; do not represent handoff itself as a task-table TUI.
```

在原位置换成：

```
- handoff renders the task session itself; it does not attach the executor's native CLI. (Superseded the earlier "executor attachment layer" decision — see `docs/superpowers/specs/2026-08-12-w4-shell-calibration-design.md` §8.1.) The task TUI is a handoff-rendered turn timeline plus event stream plus an instruction box, built on W4a's `frames.jsonl`. It is still not a task *table* — a task tab shows one task's session, and the cross-task table lives in the task board.
```

「它仍然不是 task table」这半句要留着：原句里被推翻的是「attach 原生 CLI」，不是「handoff 别做成一张任务表」——后者仍然是对的，中央 tab 里放的是单个任务的会话，跨任务的表在看板里。删整行会把这条一起丢掉。

- [ ] **Step 2: 改 8.3 / 8.4（看板与开发机的形态）**

把这一行：

```
- Global task board, machine/agent management, and settings replace the workbench content area while keeping the project overview visible. The file tree appears only when a concrete directory is selected in the workbench.
```

换成：

```
- The global task board and the global ticket list are **overlays** over the workbench, not content-area replacements: they are cross-directory views, and replacing the content area would evict the tab group the user is looking at (spec §8.3). Only settings replaces the content area, with the project overview staying visible. The file tree appears only when a concrete directory is selected in the workbench.
- Machine/agent management is **a section inside settings**, no longer a top-level nav destination (spec §8.4). Machines still appear as the second level of the left tree — that is navigation ("which machines does this project land on"), which is a different job from managing them.
```

同时把这一行：

```
- Settings contain only basic desktop behavior and Env-file management. An Env file is a physical `.env` file under one machine's handoff directory; each machine has many files and each file has many variables.
```

换成：

```
- Settings has three sections: development machines, general desktop behavior, and Env-file management. An Env file is a physical `.env` file under one machine's handoff directory; each machine has many files and each file has many variables. (Env-file management is designed but unimplemented in the real console as of W4.)
```

- [ ] **Step 3: 改 8.2 / 8.5（tab 只有三种，没有 dock，没有预览）**

把这一行：

```
- Use an Orca-like three-column desktop workbench: project overview on the left, terminal/editor/browser tab groups in the center, and the selected directory's file tree on the right.
```

换成：

```
- Use an Orca-like three-column desktop workbench: project overview on the left, tab groups in the center, and the selected directory's file tree on the right.
- There are exactly **three kinds of center tab**: terminal, file, and task TUI (spec §8.5). No browser-preview tab — the user's real browser previews a local port better than an embedded iframe can (devtools, extensions, existing sessions); "open in browser" is a link, not a tab kind.
- **No bottom dock** (spec §8.2). Problems / Output / Debug Console do not apply — handoff is not an IDE and has no language server or debugger. The terminal is an ordinary tab that participates in splitting, so a one-tab dock bar would only waste vertical space.
```

- [ ] **Step 4: 标注 8.6（一个项目最多一台远程机——这条不改，只加注）**

在这一行的末尾追加一句（保留原句）：

```
- Each project has at least one code location: local, one paired remote development machine, or both. A project can never bind more than one remote machine. The left hierarchy is project → code location(s) → main/worktree directory → handoff tasks. Project rows aggregate directory, running-task, and attention counts. **Known divergence: agentd does not enforce the one-remote-machine rule (`ProjectNode.Locations` is an array), and the real console renders whatever the data says rather than hiding extras — showing two machines when there are two is safer than hiding one (spec §8.6). Whether the constraint should move into the backend is undecided.**
```

- [ ] **Step 5: 核对没有漏网的旧决定**

```bash
grep -n "attachment layer\|browser tab\|replace the workbench content area\|Settings contain only" prototypes/desktop-console/AGENTS.md
```

Expected: 无输出（四条旧措辞全部已被替换）。

再人工读一遍整个「Confirmed product and visual decisions」小节，确认没有第二处说「开发机是顶层页」或「看板替换内容区」。

- [ ] **Step 6: 提交**

```bash
git add prototypes/desktop-console/AGENTS.md
git commit -m "docs(prototype): 回填 W4 与原型的五条偏离，AGENTS.md 不再误导后来者"
```

---

## Task 17: 形态对照与验收走查

**Files:**
- Modify: 仅在发现偏差时修对应源文件（本任务不预先指定）

**Interfaces:**
- Consumes: Task 1–16 的全部产出
- Produces: 一份走查结论（写进本任务的勾选框与最后的提交信息）

这是本计划的最后一个任务，也是唯一一个**跑真机、用眼睛看**的任务。前面每个任务的单测只能证明各自的组件行为对，证明不了三栏拼起来是不是原型那个东西。

- [ ] **Step 1: 起真机环境**

需要一个活着的 agentd 和至少一个有工作树的项目——空数据下第 2/5/6 项无从验起。

```bash
handoff status
```

Expected: 一屏正常输出（版本 / 数据 / 任务）。若报 `connection refused`，先起 agentd 再继续；**不要**为了走查另起第二个 agentd 抢同一份数据目录。

然后起 dev server（它把 `/api` `/ws` `/console` 反代给 `127.0.0.1:7777`）：

```bash
npm --prefix web run dev
```

拿一次性 ticket，把 origin 换成 dev server 的：

```bash
handoff console --print-url
```

把打印出来的 URL 里的 `http://127.0.0.1:7777` 换成 `http://localhost:5173`，60 秒内在浏览器打开。`/console` 会消费 ticket、Set-Cookie（host-only，落在 `localhost`）、302 回 `/`，此后 5173 上的 `/api` `/ws` 都带着会话 cookie。

**必须用 `localhost` 而不是 `127.0.0.1`**：cookie 是 host-only 的，两个主机名各自一份，混用会出现「明明登录过却 401」。

- [ ] **Step 2: 视口设成 1440×1024**

原型基准图 `prototypes/desktop-console/implementation-complete-workbench.png` 是 1440×1024 出的。对照必须同尺寸，否则三栏宽度比例天然对不上，会误判成实现有偏差。

浏览器窗口调到 1440×1024（或用开发者工具的设备尺寸）。

- [ ] **Step 3: 逐条走 spec §9 的 13 条**

一条一条点，不要凭「代码里写了」跳过。每条后面写下实际观察到的现象。

- [ ] 1. 左栏能看到 项目 → 开发机 → 目录 → 任务 四级；主目录与工作树平级；断开的机器仍在列并标原因
- [ ] 2. 点一个目录：面包屑变成 `项目 / 开发机 / 目录`；右栏出现该目录的文件树；中央切到该目录的 tab 组
- [ ] 3. 切到另一个目录再切回来：两边的 tab 组各自保持自己的 tab 与激活项
- [ ] 4. 点左栏一个任务：中央开出 TUI tab，里面有回合时间线、事件流、底部指令输入框；`waiting_review` 的任务能看到审阅取证并能跑 diff
- [ ] 5. 点右栏一个文件：中央开出文件 tab，内容可见（只读）；再点同一个文件不重复开
- [ ] 6. 开左右分屏，两组各放一个 tab；切目录时两组一起换
- [ ] 7. 点中央 tab 条的 `+`：开出空白 tab，中间列出「新终端 / 打开文件 / 打开任务 TUI」三项带快捷键；选一项后该 tab 变成对应内容。**顺手按一次 ⌘T**，确认印上去的快捷键真能用（面板刚开出来时是聚焦的）
- [ ] 8. 点右下角悬浮按钮：弹出同一个选择面板，但**只有「新终端」一项**；开出的终端 tab 标题显示基准目录是 home
- [ ] 9. 终端 tab（无论从哪开）：能关、能分屏，内容区是「PTY 后端尚未实现」的说明
- [ ] 10. 左栏底部工单按钮带角标；点开是全局工单列表，能就地批准/拒绝/回答；批准后角标数量下降
- [ ] 11. 点左栏顶部任务看板：弹出四列看板，卡片上的干预态是橙色标记；点一张卡片，弹层关闭并跳到该任务所在目录、开出它的 TUI tab
- [ ] 12. 点设置：能看到开发机列表与详情
- [ ] 13. 形态对照（下一步单独做）

第 10 条需要一个真的挂起工单。手上没有就造一个：派一个会触发权限请求的任务，或直接用已有的 `waiting_answer` 任务。**造不出来就如实记「未验」，不要因为「看代码应该没问题」就打勾。**

- [ ] **Step 4: 第 13 条——与基准图并排对照**

把 `prototypes/desktop-console/implementation-complete-workbench.png` 和实现截图并排看，只判四件事：

| 区域 | 对照点 |
|------|--------|
| 左栏 | 宽度、四级缩进层次、顶部看板入口、底部三按钮的位置 |
| 面包屑 | 在中央区顶部、三段式、不是浏览器地址栏那种长条 |
| 中央 | tab 条在上、内容在下；分屏是左右两组各带自己的 tab 条 |
| 右栏 | 文件树在右、宽度与左栏相仿、有「改动集 / 目录」两段 |

**判据是位置与层次能对上，不是像素级一致。** 颜色、字号、间距的细微差别不算偏差——原型是 React + 另一套样式，实现是 Tailwind，本来就不会一样。

**允许的差异只有 spec §8 记录的五条 + §8.6 那条已知不一致：** 没有底部 dock（§8.2）、没有浏览器预览 tab（§8.5）、看板与工单是弹层（§8.3）、开发机在设置里（§8.4）、TUI 是 handoff 自渲染而非 attach 原生 CLI（§8.1）、一个项目可能显示多台远程机（§8.6）。

**出现第七种差异 = 实现偏了，回去改。** 不要在这一步现场发明新的偏离理由——五条是用户裁决过的，第六条得再问过用户。

- [ ] **Step 5: 处置走查结果**

- 小偏差（文案、缩进、少一个 hover 态）：就地修，补上对应组件的单测，跟着本任务提交。
- 大偏差（某条验收整条做不到、需要改接口）：**不要临时糊**。记成残留，报告给用户，由用户决定是本期补还是另开一条。
- 无法验证（没有挂起工单、没有远程机、没有 `waiting_review` 任务）：如实记「未验 + 原因」，不要打勾。

- [ ] **Step 6: 收尾自检与提交**

```bash
npm --prefix web run test && npm --prefix web run typecheck && npm --prefix web run lint && npm --prefix web run build
go test ./... 2>&1 | tail -20
grep -rn "工单区" web/src/ ; echo "exit=$?"
```

Expected: 前两行全绿；`grep` 无输出（`exit=1`）。

再按 `instrumenting-code` 的清单过一遍本批改动：
- [ ] 每个新建文件有文件头注释（职责 + 边界）
- [ ] 每个导出组件/hook 有注释说明参数与注意事项
- [ ] 每条错误分支带上下文（agentd 的错误原文透传到界面，不吞成「加载失败」）
- [ ] 成功路径不静默（空列表、无改动、无工单都有明确文案，不是一块空白）
- [ ] 没有把 `console.log` 当日志用

```bash
git add -A
git commit -m "chore(web): W4 形态对照走查，修正走查发现的偏差"
```

提交信息里写清楚 13 条各自的结论（通过 / 未验 + 原因 / 残留 + 编号）。

---
