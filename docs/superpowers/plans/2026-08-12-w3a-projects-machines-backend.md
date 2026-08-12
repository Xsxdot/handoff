# W3a：项目与机器控制面（后端）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让本机 agentd 能回答「有哪些项目、它们在哪些机器的哪些工作树上、每台机器活着没有」，并把远端 agentd 的任务与事件透明地代理/镜像到本机，使浏览器与 CLI 只跟本机说话。

**Architecture:** 三层模型（project / location / workspace）各有各的真相所在：project 是 `origin_url` 的纯函数（`internal/projectid`），location 的真相在 B62 的 `project_locations` 表（本期只读），workspace 现场 `git worktree list` 探测、不落库。跨机能力由两条独立机制承担——**转发**（写操作与取证，按任务 id 或显式 `?machine=` 路由，一跳封顶）与**镜像**（agentd 自己发现远端活跃任务、订上游 WS，把事件复制进 `mirror_events`，快照进 `mirror_tasks`）。看板读镜像快照，不现场扇出。

**Tech Stack:** Go 1.26、`net/http` ServeMux（Go 1.22 方法+通配路由）、modernc.org/sqlite、coder/websocket、cobra、log/slog。

## Global Constraints

- **只动 `internal/` + `cmd/`。不动 `web/`。** `internal/proto/` 与 `web/src/api/testdata/*.json` 是**审核者独占面，已在派发前改完**（提交 `78b2d97b`）——本计划一行都不许改它们。发现契约不对：**停下来上报**，不要自己改 proto、不要自己改 fixture、不要 `-update`。
- **不新增第三方依赖**。需要新依赖时停下来上报。
- **不改 B62 已交付的语义**：`POST/GET /api/projects`、`DELETE /api/projects/{name}` 三条端点的**单机行为**、`handoff project add/ls/rm` 的**默认输出**一律原样。本计划只在它们之外加东西。
- **不另写 origin 归一化**：一律调 `internal/projectid` 的 `NormalizeGitURL` / `FromOrigin`。两份实现的分歧会让同一项目在 UI 上裂成两个，且极难归因。
- **凭据纪律**：`Target.Token`、会话 cookie、auth ticket 明文**一律不得进日志**。机器名、addr、任务 id、project_id 可以。
- **不可达是数据不是错误**：任何跨机汇总，单台失败都不得让整个响应变成非 200；失败那台必须出现在响应的 `machines` 里且 `ok=false`、`error` 带原文。静默少几行是本设计的头号失败模式。
- **报错原文不改写**：转发的响应状态码与中文报错体原样透传，不做二次包装。
- **命名雷区**：`internal/agentd` 包内**已经有一个** `Workspace` 类型（`workspace.go` 的 `PrepareWorkspace` 返回值）。本计划涉及的工作树类型一律写全 `proto.Workspace`，且**不得**新建任何叫 `Workspace` 的本地类型。
- 每个新建文件写文件头注释（职责 + 边界）；每个导出函数写用途/参数/返回/注意；非显然分支写「为什么」注释。范本是 `internal/agentd/hostguard.go` 与 `internal/agentd/projectresolve.go` 的头部。
- 日志一律 `s.log` / `m.log` / `log()`（slog），**禁止 `fmt.Printf`**。关键节点见每个任务的「加日志」步骤。
- 命令一律在仓库根执行：`go build ./...`、`go test ./...`、`gofmt -l internal/ cmd/`。
- 既有测试**不得变红**。`internal/agentd` 的测试环境用 `newTestEnv(t)` / `newTestEnvWithCfg(t, cfg, logger)`（真实 SQLite + httptest，token 常量 `testToken`）；git 仓库用 `initGitRepo(t)` / `initGitRepoWithOrigin(t, origin)` / `gitAt(t, dir, args...)`。

---

## 契约附录（规范性，**已落地，只读**）

以下 Go 类型**已经在分支上**（`internal/proto/projects.go` 与 `internal/proto/proto.go`），由审核者在派发前提交。实现时以它们为准，**一个字段都不要改**：

```go
// internal/proto/projects.go
type Workspace struct {
    Path string `json:"path"`; Branch string `json:"branch"`; Head string `json:"head"`
    IsMain bool `json:"is_main"`; Managed bool `json:"managed"`
}
type ProjectLocationNode struct {
    Machine string `json:"machine"`; Name string `json:"name"`; Path string `json:"path"`
    Workspaces []Workspace `json:"workspaces"`; ProbeError string `json:"probe_error"`
}
type ProjectNode struct {
    ProjectID string `json:"project_id"`; OriginURL string `json:"origin_url"`
    Name string `json:"name"`; Locations []ProjectLocationNode `json:"locations"`
}
type MachineStatus struct {
    Name string `json:"name"`; Ok bool `json:"ok"`
    FetchedAt time.Time `json:"fetched_at"`; Error string `json:"error"`
}
type ProjectTreeResp struct {
    Projects []ProjectNode `json:"projects"`; Unowned []string `json:"unowned"`
    Machines []MachineStatus `json:"machines,omitempty"`
}
type Machine struct {
    Name string `json:"name"`; Addr string `json:"addr"`; Reachable bool `json:"reachable"`
    Version string `json:"version"`; Executors []string `json:"executors"`
    DefaultExecutor string `json:"default_executor"`; ProbeMs int64 `json:"probe_ms"`
    ActiveTasks int `json:"active_tasks"`; Error string `json:"error"`
}
type MachinesResp struct { Machines []Machine `json:"machines"` }
type TasksResp struct {
    Machines []MachineStatus `json:"machines"`; Tasks []TaskView `json:"tasks"`
}

// internal/proto/proto.go 的 Task 末尾已加两个线注解字段（不入库）：
//   Machine   string `json:"machine"`     ""=本机；否则为本机 cfg.Targets 的键，汇总方盖章
//   ProjectID string `json:"project_id"`  读时 join 得到；未归属为 ""
```

**一处 spec 读法需要写进代码注释**：spec §5.3 提到 `GET /api/projects?scope=all`，但 §3 明文「W3a 不动 `/api/projects` 这三条」。因此**项目的跨机汇总面是 `/api/projects/tree?scope=all`**，扁平端点保持单机。实现按后者，并在 handler 注释里写明这条取舍。

---

## Task 1: 工作区探测

**Files:**
- Create: `internal/agentd/workspaceprobe.go`
- Test: `internal/agentd/workspaceprobe_test.go`

**Interfaces:**
- Consumes: `gitRun(ctx, repo, args...) (stdout, stderr string, err error)`（`internal/agentd/workspace.go:92`）
- Produces: `func probeWorkspaces(ctx context.Context, dir, managedRoot string) ([]proto.Workspace, string)` —— 返回工作树列表与**人话的探测失败说明**（空串=正常）；`func parseWorktreePorcelain(out, managedRoot string) []proto.Workspace`

- [ ] **Step 1: 契约闸——先确认 proto 已就位**

Run: `go build ./... && go test ./internal/proto/ -run TestContractFixtures`

期望：编译通过、fixture 测试绿，且 `internal/proto/projects.go` 里能看到 `Workspace` / `ProjectTreeResp` / `Machine` / `MachinesResp` / `TasksResp`，`proto.Task` 末尾有 `Machine` 与 `ProjectID`。

**若不符合：停下来上报，不要自己改 `internal/proto/`，不要跑 `-update`。** 契约由审核者独占，两边各改一半会让前后端契约测试测了个寂寞。

- [ ] **Step 2: 写失败的测试**

新建 `internal/agentd/workspaceprobe_test.go`：

```go
// 工作区探测测试：porcelain 解析的四种形态 + 目录失效的降级。
package agentd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseWorktreePorcelainShapes 覆盖主工作树/linked/detached/managed 四种形态。
func TestParseWorktreePorcelainShapes(t *testing.T) {
	out := strings.Join([]string{
		"worktree /home/dev/handoff",
		"HEAD 482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6",
		"branch refs/heads/main",
		"",
		"worktree /home/dev/.handoff/worktrees/w1",
		"HEAD 9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6482aab1",
		"branch refs/heads/handoff/w1",
		"",
		"worktree /home/dev/scratch",
		"HEAD aa11bb22cc33dd44ee55ff66aa77bb88cc99dd00",
		"detached",
		"",
	}, "\n")
	ws := parseWorktreePorcelain(out, "/home/dev/.handoff/worktrees")
	if len(ws) != 3 {
		t.Fatalf("工作树数 = %d，期望 3：%+v", len(ws), ws)
	}
	if !ws[0].IsMain || ws[0].Branch != "main" || ws[0].Head != "482aab1" || ws[0].Managed {
		t.Errorf("主工作树解析错：%+v", ws[0])
	}
	if ws[1].IsMain || !ws[1].Managed || ws[1].Branch != "handoff/w1" {
		t.Errorf("managed 工作树解析错：%+v", ws[1])
	}
	// detached 时 branch 为空串，head 仍在——UI 靠 head 显示，不能两个都空
	if ws[2].Branch != "" || ws[2].Head != "aa11bb2" || ws[2].Managed {
		t.Errorf("detached 工作树解析错：%+v", ws[2])
	}
}

// TestProbeWorkspacesRealRepo 对真实仓库探测：主工作树 + 一个 linked worktree。
func TestProbeWorkspacesRealRepo(t *testing.T) {
	repo := initGitRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	gitAt(t, repo, "worktree", "add", "-b", "side", linked)

	ws, probeErr := probeWorkspaces(context.Background(), repo, filepath.Dir(linked))
	if probeErr != "" {
		t.Fatalf("探测不该失败：%s", probeErr)
	}
	if len(ws) != 2 {
		t.Fatalf("工作树数 = %d，期望 2：%+v", len(ws), ws)
	}
	if !ws[0].IsMain {
		t.Errorf("第一条应是主工作树：%+v", ws[0])
	}
	if !ws[1].Managed {
		t.Errorf("linked 落在 managedRoot 下，应判定为 managed：%+v", ws[1])
	}
}

// TestProbeWorkspacesBadDirDegrades 是本任务最重要的一条：目录失效不炸树。
//
// 项目树必须能展示「登记还在、目录已失效」，整棵树 500 会让用户连哪个项目
// 坏了都看不见。
func TestProbeWorkspacesBadDirDegrades(t *testing.T) {
	ws, probeErr := probeWorkspaces(context.Background(), filepath.Join(t.TempDir(), "gone"), "")
	if probeErr == "" {
		t.Fatal("目录不存在时必须给出 probe_error")
	}
	if ws == nil {
		t.Fatal("失败时也必须返回空切片而非 nil：JSON 要序列化成 [] 不是 null")
	}
	if len(ws) != 0 {
		t.Fatalf("失败时不该有工作树：%+v", ws)
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run 'Workspaces|Porcelain' -v`
Expected: FAIL，`undefined: probeWorkspaces` / `undefined: parseWorktreePorcelain`。

- [ ] **Step 4: 写实现**

新建 `internal/agentd/workspaceprobe.go`：

```go
// 本文件实现工作区（git 工作树）的**现场探测**：给一个 location 的路径，
// 吐出它下面的全部工作树。
//
// 职责：
//   - 调一次 git worktree list --porcelain，解析成 []proto.Workspace
//   - 判定每条是不是主工作树、是不是 agentd 自建的任务工作树
//   - 探测失败时降级为「空列表 + 人话说明」，不向上抛错
//
// 边界：
//   - 不落库：worktree 会在 agentd 背后被 git worktree add/remove 改动，
//     落表必然产生说谎的行；本机文件系统调用是毫秒级的，缓存只会引入失真窗口
//   - 不判断工作树上挂着哪个任务（那是 join 的事，见 projectjoin.go）
//   - 不做鉴权、不碰 HTTP
package agentd

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// worktreeProbeTimeout 是单次探测的上限。
//
// 为什么要有：目录落在挂掉的网络盘上时 git 会卡住，而项目树是 UI 的常规请求，
// 一个卡住的 location 不能拖垮整棵树。
const worktreeProbeTimeout = 5 * time.Second

// headShortLen 是短 sha 的长度，与 git 默认的 7 位一致。
const headShortLen = 7

// probeWorkspaces 现场探测 dir 下的全部工作树。
//
// 参数：
//   - ctx: 上下文；内部再叠加 worktreeProbeTimeout 作为兜底上限
//   - dir: location 的路径（B62 保证它是主工作树根）
//   - managedRoot: agentd 自建 worktree 的根目录（<DataDir>/worktrees）；
//     空串表示「无法判定 managed」，此时全部按 false
//
// 返回：
//   - 工作树列表（**永不为 nil**，失败时是空切片）
//   - 探测失败的人话说明，空串=正常
//
// 注意：
//   - 失败不返回 error 而返回说明字符串，是刻意的：调用方要把它放进
//     ProjectLocationNode.ProbeError 展示，而不是让整棵树 500
func probeWorkspaces(ctx context.Context, dir, managedRoot string) ([]proto.Workspace, string) {
	ctx, cancel := context.WithTimeout(ctx, worktreeProbeTimeout)
	defer cancel()

	log().Debug("工作区探测开始", "dir", dir)
	out, stderr, err := gitRun(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		// 目录被删、不是 git 仓库、git 超时都走这里：这是**可展示的状态**，
		// 不是服务端故障，所以只 Warn 不 Error
		log().Warn("工作区探测失败，降级为空列表", "dir", dir, "cause", msg)
		return []proto.Workspace{}, msg
	}
	ws := parseWorktreePorcelain(out, managedRoot)
	log().Debug("工作区探测完成", "dir", dir, "worktrees", len(ws))
	return ws, ""
}

// parseWorktreePorcelain 解析 git worktree list --porcelain 的输出。
//
// 输出形态（每块之间空行分隔，第一块恒为主工作树）：
//
//	worktree /path
//	HEAD <40 位 sha>
//	branch refs/heads/main      ← detached 时这一行换成 detached
//
// 返回的切片永不为 nil。
func parseWorktreePorcelain(out, managedRoot string) []proto.Workspace {
	list := []proto.Workspace{}
	var cur *proto.Workspace
	flush := func() {
		if cur != nil {
			cur.IsMain = len(list) == 0 // 第一块即主工作树，git 的输出顺序保证
			cur.Managed = underRoot(cur.Path, managedRoot)
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &proto.Workspace{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			// 块外的行（不该出现）直接忽略，别 panic
		case strings.HasPrefix(line, "HEAD "):
			sha := strings.TrimPrefix(line, "HEAD ")
			if len(sha) > headShortLen {
				sha = sha[:headShortLen]
			}
			cur.Head = sha
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			// detached 时 branch 留空串——UI 靠 head 显示，不能编一个假分支名
			cur.Branch = ""
		}
	}
	flush()
	return list
}

// underRoot 判断 path 是否落在 root 目录下（含 root 自身的子目录）。
//
// 为什么不用 strings.HasPrefix 裸比：/a/worktrees-old 会被 /a/worktrees 误判为
// 子目录。走 filepath.Rel 再看有没有 ".." 才是准的。
func underRoot(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run 'Workspaces|Porcelain' -v`
Expected: PASS（三条）。

- [ ] **Step 6: 加关键节点日志**

上面实现里已含：探测开始（Debug，dir）、探测失败（Warn，dir + 原文）、探测完成（Debug，dir + 工作树数）。**确认**这三条都在，且失败分支带了 `cause`。探测是高频调用（每次项目树请求 × 每个 location），成功路径必须是 Debug 而非 Info，否则刷屏。

- [ ] **Step 7: 加注释**

确认：文件头有职责 + 边界；`probeWorkspaces` 有参数/返回/注意；`underRoot` 有「为什么不用 HasPrefix」；`parseWorktreePorcelain` 有输出形态说明；detached 分支有「不编假分支名」的 why。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/workspaceprobe.go internal/agentd/workspaceprobe_test.go
git commit -m "feat(agentd): 工作区现场探测——porcelain 解析 + 目录失效降级不炸树"
```

---

## Task 2: 任务归属 join（repo_path → project_id）

**Files:**
- Create: `internal/agentd/projectjoin.go`
- Test: `internal/agentd/projectjoin_test.go`
- Modify: `internal/agentd/server.go`（`handleListTasks`、`handleGetTask` 处注解）

**Interfaces:**
- Consumes: `store.ListProjectLocations() ([]proto.ProjectLocation, error)`
- Produces: `type projectIndex map[string]string`；`func newProjectIndex(locs []proto.ProjectLocation) projectIndex`；`func (idx projectIndex) projectIDOf(repoPath string) string`；`func (s *Server) projectIndex() projectIndex`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/projectjoin_test.go`：

```go
// 任务归属 join 测试：命中 / 未登记 / 已注销 / 遗留 linked worktree 四态。
package agentd

import (
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

func TestProjectIndexLookup(t *testing.T) {
	idx := newProjectIndex([]proto.ProjectLocation{
		{ProjectID: "aaaa111122223333", Name: "handoff", Path: "/home/dev/handoff"},
		{ProjectID: "bbbb444455556666", Name: "tk", Path: "/home/dev/tk/"},
	})

	cases := []struct {
		name     string
		repoPath string
		want     string
	}{
		{"命中", "/home/dev/handoff", "aaaa111122223333"},
		{"命中（尾斜杠归一）", "/home/dev/tk", "bbbb444455556666"},
		{"命中（非规范路径归一）", "/home/dev/./handoff", "aaaa111122223333"},
		// 已注销 = 表里没这行了；未登记 = 从来没登记过。对 join 是同一件事：
		// 诚实显示未归属，而不是留一列陈旧数据说谎
		{"未登记", "/home/dev/other", ""},
		// B62 之前派发的任务，repo_path 可能指向 linked worktree（当时不归并）。
		// 这类任务 join 不中，显示未归属——这是诚实的降级，不做回填
		{"遗留 linked worktree", "/home/dev/handoff/.worktrees/w1", ""},
		{"空路径", "", ""},
	}
	for _, c := range cases {
		if got := idx.projectIDOf(c.repoPath); got != c.want {
			t.Errorf("%s: projectIDOf(%q) = %q，期望 %q", c.name, c.repoPath, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestProjectIndexLookup -v`
Expected: FAIL，`undefined: newProjectIndex`。

- [ ] **Step 3: 写实现**

新建 `internal/agentd/projectjoin.go`：

```go
// 本文件实现任务的项目归属 join：task.repo_path → project_locations.path →
// 该行的 project_id。
//
// 职责：
//   - 把位置表压成一张 path → project_id 的等值索引
//   - 给任务盖上 ProjectID 注解（线字段，不入库）
//
// 边界：
//   - 不加库列：tasks 表**不加** project_id 列。历史任务或已注销项目的任务
//     应当诚实显示「未归属」，而不是一列陈旧数据说谎；加列的代价（回填 +
//     注销后列失真）只换来微不足道的查询加速
//   - 不做模糊匹配：B62 的 MainWorktreeRoot 归并让 repo_path 与
//     project_locations.path 同源同形态，一次 filepath.Clean 后等值比即可。
//     早前设想的「先比 location 再逐个比 workspace」两段式已整个去掉
package agentd

import (
	"path/filepath"

	"github.com/xushixin/handoff/internal/proto"
)

// projectIndex 是 归一化路径 → project_id 的等值索引。
type projectIndex map[string]string

// newProjectIndex 由位置表构建索引。
func newProjectIndex(locs []proto.ProjectLocation) projectIndex {
	idx := make(projectIndex, len(locs))
	for _, l := range locs {
		if l.Path == "" {
			continue
		}
		idx[filepath.Clean(l.Path)] = l.ProjectID
	}
	return idx
}

// projectIDOf 返回该仓库路径所属的 project_id；未登记/已注销返回空串。
//
// 注意：空串是**正常结果**而非错误——「未归属」是项目树与看板要如实展示的一种状态。
func (idx projectIndex) projectIDOf(repoPath string) string {
	if repoPath == "" {
		return ""
	}
	return idx[filepath.Clean(repoPath)]
}

// projectIndex 读一次位置表并构建索引；查表失败时返回空索引（全部未归属）。
//
// 为什么失败不向上抛：归属只是任务列表上的一个注解，位置表读不到时列表本身
// 仍然有效。为一个注解让 /api/tasks 500，是把附加信息变成了单点故障。
func (s *Server) projectIndex() projectIndex {
	locs, err := s.st.ListProjectLocations()
	if err != nil {
		s.log.Warn("读取位置表失败，任务归属本次全部显示未归属", "cause", err)
		return projectIndex{}
	}
	return newProjectIndex(locs)
}
```

- [ ] **Step 4: 在任务列表与详情上盖注解**

`internal/agentd/server.go` 的 `handleListTasks`：在构建 `views` 之前取一次索引，循环里给每条盖 `ProjectID`（`Machine` 本机恒为空串，零值即正确，不用显式赋）：

```go
	idx := s.projectIndex()
	views := make([]proto.TaskView, 0, len(tasks))
	unattended := 0
	for _, t := range tasks {
		t.ProjectID = idx.projectIDOf(t.RepoPath) // 读时 join，不落库
		w := s.hub.Watchers(t.ID)
		...
	}
```

`handleGetTask`（`server.go:373` 附近构造 `proto.TaskView{Task: *task, ...}` 那处）同样盖一次：

```go
	task.ProjectID = s.projectIndex().projectIDOf(task.RepoPath)
```

- [ ] **Step 5: 写端到端断言**

追加到 `internal/agentd/projectjoin_test.go`（用真实 store + httptest，验证注解真的出现在响应里）：

```go
// TestTaskListAnnotatesProjectID 断言 GET /api/tasks 的每条都带上归属注解。
func TestTaskListAnnotatesProjectID(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.st.CreateProjectLocation(proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff",
		Path: "/home/dev/handoff", OriginURL: "git@github.com:x/handoff.git",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}
	now := time.Now().UTC()
	mustCreateTask(t, env.st, &proto.Task{
		ID: uuid.NewString(), RepoPath: "/home/dev/handoff",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	mustCreateTask(t, env.st, &proto.Task{
		ID: uuid.NewString(), RepoPath: "/home/dev/nowhere",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})

	var views []proto.TaskView
	env.getJSON(t, "/api/tasks", &views)
	if len(views) != 2 {
		t.Fatalf("任务数 = %d，期望 2", len(views))
	}
	got := map[string]string{}
	for _, v := range views {
		got[v.RepoPath] = v.ProjectID
	}
	if got["/home/dev/handoff"] != "aaaa111122223333" {
		t.Errorf("已登记任务应带 project_id，实得 %q", got["/home/dev/handoff"])
	}
	if got["/home/dev/nowhere"] != "" {
		t.Errorf("未登记任务应显示未归属（空串），实得 %q", got["/home/dev/nowhere"])
	}
}
```

**注意**：`CreateProjectLocation` 的实际签名以 `internal/store/projects.go` 为准；`env.getJSON` 若不存在，就照 `internal/agentd/server_test.go` 里既有的请求辅助写法（`http.NewRequest` + `Authorization: Bearer env.token` + `json.NewDecoder`）自己发一次 GET，**不要为此改既有测试文件的公共辅助**。

- [ ] **Step 6: 运行测试**

Run: `go test ./internal/agentd/ -run 'ProjectIndex|AnnotatesProjectID' -v`
Expected: PASS。

- [ ] **Step 7: 加关键节点日志**

- `s.projectIndex()` 查表失败：Warn + cause（已在实现里）。
- `handleListTasks` 完成那行日志加一个 `owned` 计数（有归属的条数），让「归属突然全空」这种故障在日志里看得见：

```go
	s.log.Info("任务列表完成", "tasks", len(views), "unattended", unattended, "owned", owned)
```

- [ ] **Step 8: 加注释**

确认文件头写了「不加库列」与「不做模糊匹配」两条边界及其 why；`projectIDOf` 注明空串是正常结果；`handleListTasks` 里那行盖注解带 `// 读时 join，不落库`。

- [ ] **Step 9: Commit**

```bash
git add internal/agentd/projectjoin.go internal/agentd/projectjoin_test.go internal/agentd/server.go
git commit -m "feat(agentd): 任务归属读时 join——repo_path 等值匹配位置表，未归属诚实留空"
```

---

## Task 3: `GET /api/projects/tree`（单机）

**Files:**
- Create: `internal/agentd/projecttree.go`
- Test: `internal/agentd/projecttree_test.go`
- Modify: `internal/agentd/server.go`（注册路由）

**Interfaces:**
- Consumes: Task 1 的 `probeWorkspaces`；`store.ListProjectLocations`
- Produces: `func (s *Server) buildLocalTree(ctx context.Context) (proto.ProjectTreeResp, error)`；`func (s *Server) handleProjectTree(w http.ResponseWriter, r *http.Request)`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/projecttree_test.go`（用 `package agentd` 而非 `agentd_test`——要直接调未导出的 `buildLocalTree`）：

```go
// 项目树测试：分组、单机不变式、探测降级。
package agentd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// TestBuildLocalTreeGroupsAndProbes 断言：按 project_id 分组、单机每项目恒 1 个
// location、工作树被真实探到。
func TestBuildLocalTreeGroupsAndProbes(t *testing.T) {
	env := newTestEnv(t)
	repo := initGitRepoWithOrigin(t, "git@github.com:x/handoff.git")
	if _, err := env.st.CreateProjectLocation(proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff", Path: repo,
		OriginURL: "git@github.com:x/handoff.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}

	tree, err := env.srv.buildLocalTree(context.Background())
	if err != nil {
		t.Fatalf("buildLocalTree: %v", err)
	}
	if len(tree.Projects) != 1 {
		t.Fatalf("项目数 = %d，期望 1", len(tree.Projects))
	}
	p := tree.Projects[0]
	if p.ProjectID != "aaaa111122223333" || p.Name != "handoff" {
		t.Errorf("项目头信息错：%+v", p)
	}
	// 单机不变式：每个项目恒 0 或 1 个 location（ADR-0008）
	if len(p.Locations) != 1 {
		t.Fatalf("单机 locations 长度 = %d，必须 ≤1", len(p.Locations))
	}
	loc := p.Locations[0]
	if loc.Machine != "" {
		t.Errorf("本机的 machine 必须是空串，实得 %q", loc.Machine)
	}
	if loc.ProbeError != "" {
		t.Errorf("真实仓库不该有探测错误：%s", loc.ProbeError)
	}
	if len(loc.Workspaces) != 1 || !loc.Workspaces[0].IsMain {
		t.Errorf("应探到一个主工作树：%+v", loc.Workspaces)
	}
}

// TestBuildLocalTreeBrokenLocationStillListed 是「登记还在、目录已失效」的核心
// 断言：那条 location 必须仍然出现在树里，带 probe_error，而不是整棵树报错。
func TestBuildLocalTreeBrokenLocationStillListed(t *testing.T) {
	env := newTestEnv(t)
	gone := filepath.Join(t.TempDir(), "gone")
	os.MkdirAll(gone, 0o755)
	os.RemoveAll(gone)
	if _, err := env.st.CreateProjectLocation(proto.ProjectLocation{
		ProjectID: "bbbb444455556666", Name: "ghost", Path: gone,
		OriginURL: "git@github.com:x/ghost.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}
	tree, err := env.srv.buildLocalTree(context.Background())
	if err != nil {
		t.Fatalf("目录失效不该让整棵树报错：%v", err)
	}
	if len(tree.Projects) != 1 || len(tree.Projects[0].Locations) != 1 {
		t.Fatalf("失效的 location 必须仍然列出：%+v", tree)
	}
	if tree.Projects[0].Locations[0].ProbeError == "" {
		t.Error("失效的 location 必须带 probe_error")
	}
}

// TestProjectTreeEndpointShape 断言端点返回 200 且空库时 projects/unowned 都是 []。
func TestProjectTreeEndpointShape(t *testing.T) {
	env := newTestEnv(t)
	var resp proto.ProjectTreeResp
	code := env.getJSONCode(t, "/api/projects/tree", &resp)
	if code != 200 {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if resp.Projects == nil || resp.Unowned == nil {
		t.Fatal("空列表必须序列化为 [] 而非 null")
	}
	if resp.Machines != nil {
		t.Error("单机请求不该带 machines 栏（omitempty）")
	}
}
```

（`env.getJSONCode` 若不存在，同 Task 2 Step 5 的说明：照既有风格自己发请求，别改公共辅助。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run 'LocalTree|ProjectTreeEndpoint' -v`
Expected: FAIL，`undefined: buildLocalTree` / 404。

- [ ] **Step 3: 写实现**

新建 `internal/agentd/projecttree.go`：

```go
// 本文件实现项目树：把扁平的位置表折成 project → location → workspace 三层，
// 并对每个 location 现场探测工作树。
//
// 职责：
//   - buildLocalTree：本机那一棵（GET /api/projects/tree）
//   - handleProjectTree：端点入口，含 ?scope=all 的分流（汇总实现见 projectfanout.go）
//
// 边界：
//   - 不改 B62 的 GET /api/projects：那条端点返回的就是位置表本身，语义本分。
//     项目树是另一种表示（嵌套、带探测、可跨机），塞进同一个端点会让一个端点
//     有两种响应形状，因此另开子路径
//   - spec §5.3 提到的 “projects?scope=all” 落在**本端点**上：§3 明文
//     「W3a 不动 /api/projects 那三条」，扁平端点保持单机
//   - 不写库：树是读出来的，一个字节都不落盘
package agentd

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// buildLocalTree 构建本机项目树。
//
// 返回：
//   - 项目树（Projects/Unowned 永不为 nil）
//   - 错误：只有「位置表都读不出来」才算错误；单个 location 探测失败是数据
//
// 注意：
//   - 同一 project_id 的多行会被合并到一个 ProjectNode 下。本机理论上不可能
//     出现（project_id 是主键），但代码按可能处理——真出现了就是库被手改过，
//     此时合并展示比崩掉强
func (s *Server) buildLocalTree(ctx context.Context) (proto.ProjectTreeResp, error) {
	start := time.Now()
	locs, err := s.st.ListProjectLocations()
	if err != nil {
		s.log.Error("项目树：查询位置表失败", "cause", err)
		return proto.ProjectTreeResp{}, err
	}
	managedRoot := filepath.Join(s.cfg.DataDir, "worktrees")

	resp := proto.ProjectTreeResp{Projects: []proto.ProjectNode{}, Unowned: []string{}}
	byID := map[string]int{} // project_id → resp.Projects 下标
	broken := 0
	for _, l := range locs {
		if l.ProjectID == "" {
			// 算不出 project_id 的脏行：诚实列出，不吞、也不塞进某个项目里
			resp.Unowned = append(resp.Unowned, l.Name)
			continue
		}
		ws, probeErr := probeWorkspaces(ctx, l.Path, managedRoot)
		if probeErr != "" {
			broken++
		}
		node := proto.ProjectLocationNode{
			Machine:    "", // 本机恒空串，与 tasks.target 的空串语义一致
			Name:       l.Name,
			Path:       l.Path,
			Workspaces: ws,
			ProbeError: probeErr,
		}
		if i, ok := byID[l.ProjectID]; ok {
			resp.Projects[i].Locations = append(resp.Projects[i].Locations, node)
			continue
		}
		byID[l.ProjectID] = len(resp.Projects)
		resp.Projects = append(resp.Projects, proto.ProjectNode{
			ProjectID: l.ProjectID,
			OriginURL: l.OriginURL,
			Name:      l.Name, // 取该项目下首条登记的 name
			Locations: []proto.ProjectLocationNode{node},
		})
	}
	s.log.Info("项目树构建完成", "projects", len(resp.Projects),
		"locations", len(locs), "broken", broken, "unowned", len(resp.Unowned),
		"elapsed_ms", time.Since(start).Milliseconds())
	return resp, nil
}

// handleProjectTree 处理 GET /api/projects/tree[?scope=all]。
//
// 路由说明：net/http ServeMux 的字面段优先于通配段，本路由与
// DELETE /api/projects/{name} 方法与形态都不冲突；即便将来补
// GET /api/projects/{name}，字面段仍然优先。
func (s *Server) handleProjectTree(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	s.log.Info("项目树请求", "scope", scope, "remote_addr", r.RemoteAddr)
	tree, err := s.buildLocalTree(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	writeJSON(w, http.StatusOK, tree)
}
```

在 `server.go` 的路由表里，**紧挨着 `GET /api/projects` 那行之后**加：

```go
	mux.HandleFunc("GET /api/projects/tree", s.handleProjectTree)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run 'LocalTree|ProjectTreeEndpoint' -v`
Expected: PASS（三条）。

- [ ] **Step 5: 加关键节点日志**

已含：请求入口（Info，scope + remote_addr）、构建完成（Info，projects/locations/broken/unowned/耗时）、位置表读取失败（Error + cause）。**确认 broken 计数在**——「有几个 location 探不动」是这条路径上唯一能提前预警「登记与磁盘漂移」的信号。

- [ ] **Step 6: 加注释**

确认文件头三条边界（不改 B62 三条端点、§5.3 的读法、不写库）；`buildLocalTree` 的返回语义（什么才算 error）；`Machine: ""` 处的空串语义注释；ServeMux 字面段优先的路由说明。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/projecttree.go internal/agentd/projecttree_test.go internal/agentd/server.go
git commit -m "feat(agentd): GET /api/projects/tree——三层项目树，失效 location 带 probe_error 照常列出"
```

---

## Task 4: `GET /api/machines`（机器投影与探活）

**Files:**
- Create: `internal/agentd/machines.go`
- Test: `internal/agentd/machines_test.go`
- Modify: `internal/agentd/server.go`（注册路由）

**Interfaces:**
- Consumes: `client.New(addr, token).Status(ctx) (*proto.StatusResp, error)`；`Manager.Status() (*proto.StatusResp, error)`；`cfg.Targets map[string]config.Target`
- Produces: `func (s *Server) probeMachines(ctx context.Context) proto.MachinesResp`；`func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request)`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/machines_test.go`（`package agentd`）：

```go
package agentd

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/xushixin/handoff/internal/config"
)

// TestProbeMachinesLocalAndRemote 起两个真实 agentd：本机 + 一台可达的“远程”。
//
// 为什么用真 server 当远程而不是打桩 HTTP：探活打的是 GET /api/status，
// 桩会把「响应形状变了」这类真实故障挡在测试之外。
func TestProbeMachinesLocalAndRemote(t *testing.T) {
	remote := newTestEnv(t)
	local := newTestEnvWithCfg(t, &config.Config{
		Token:  testToken,
		Listen: "127.0.0.1:7777",
		Targets: map[string]config.Target{
			"devbox": {Addr: remote.ts.URL, Token: testToken},
			"nas":    {Addr: "http://127.0.0.1:1", Token: testToken}, // 必然拒连
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := local.srv.probeMachines(context.Background())
	if len(resp.Machines) != 3 {
		t.Fatalf("机器数 = %d，期望 3（本机 + devbox + nas）", len(resp.Machines))
	}
	byName := map[string]int{}
	for i, m := range resp.Machines {
		byName[m.Name] = i
	}
	self := resp.Machines[byName[""]]
	if !self.Reachable || self.ProbeMs != 0 {
		t.Errorf("本机必须可达且 probe_ms 恒 0：%+v", self)
	}
	// 不可达是数据不是错误：那台仍然在列表里，且 error 非空
	nas := resp.Machines[byName["nas"]]
	if nas.Reachable {
		t.Errorf("127.0.0.1:1 不该可达：%+v", nas)
	}
	if nas.Error == "" {
		t.Error("不可达时 error 必须带原文——静默少一行是本设计的头号失败模式")
	}
	if nas.Executors == nil {
		t.Error("不可达时 executors 也要是 []，不能是 null")
	}
}
```

**注意**：远程那台若 `mgr` 未注入，`GET /api/status` 会 503。测试里若 `newTestEnv` 不带 manager，就断言「devbox 出现在列表里且 error 非空」而不是断言它可达——**不要为了让测试好看而给 newTestEnv 注入 manager**，先看 `internal/agentd/server_test.go` 里既有的 status 测试怎么做，照它来。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestProbeMachines -v`
Expected: FAIL，`undefined: probeMachines`。

- [ ] **Step 3: 写实现**

新建 `internal/agentd/machines.go`：

```go
// 本文件实现机器投影与探活：GET /api/machines。
//
// 职责：
//   - 把 cfg.Targets + 本机自身投影成一张机器列表
//   - 并发探活（每台打一次 GET /api/status），共 machineProbeBudget 预算
//
// 边界：
//   - **不建表**：~/.handoff/config.yaml 的 targets 段已经是机器的真相
//     （addr/user/token），再建表就是两份真相——改了配置忘了改表，就会有
//     一台早已删除的机器永远躺在列表里
//   - 只读：执行者开关、审批器配置、重启 agent 等写操作明确不在范围内
//   - 不 ssh：探活走 HTTP，与 CLI 的 handoff status 同源同凭据
package agentd

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// machineProbeBudget 是整轮扇出探活的总预算。
//
// 3s 短于任何调用方超时：单台黑洞对端不能把整个列表拖垮。
const machineProbeBudget = 3 * time.Second

// probeMachines 投影并探活全部机器。
//
// 返回：
//   - 机器列表（本机恒在第一条，其余按名字排序）；**永不返回错误**——
//     单台不可达是数据，整体恒 200
func (s *Server) probeMachines(ctx context.Context) proto.MachinesResp {
	ctx, cancel := context.WithTimeout(ctx, machineProbeBudget)
	defer cancel()

	out := make([]proto.Machine, 0, len(s.cfg.Targets)+1)
	out = append(out, s.localMachine())

	names := make([]string, 0, len(s.cfg.Targets))
	for name := range s.cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names) // 顺序稳定：UI 列表不该每次刷新都跳

	remote := make([]proto.Machine, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			remote[i] = s.probeRemote(ctx, name)
		}(i, name)
	}
	wg.Wait()
	out = append(out, remote...)

	unreachable := 0
	for _, m := range out {
		if !m.Reachable {
			unreachable++
		}
	}
	s.log.Info("机器探活完成", "machines", len(out), "unreachable", unreachable)
	return proto.MachinesResp{Machines: out}
}

// localMachine 投影本机：进程内直查，不自拨 HTTP（自拨会在 agentd 忙时把
// 自己的健康状态也一起拖垮，且毫无必要）。
func (s *Server) localMachine() proto.Machine {
	m := proto.Machine{
		Name: "", Addr: s.cfg.Listen, Reachable: true,
		Executors: []string{}, ProbeMs: 0,
	}
	if s.mgr == nil {
		// manager 未注入时本机确实答不出运行数据，但它显然“在”——
		// 如实降级：可达但字段留空，附原因
		m.Error = "manager 未就绪"
		return m
	}
	st, err := s.mgr.Status()
	if err != nil {
		s.log.Warn("本机探活：聚合状态失败", "cause", err)
		m.Error = err.Error()
		return m
	}
	fillFromStatus(&m, st)
	return m
}

// probeRemote 探活一台远程机器。
func (s *Server) probeRemote(ctx context.Context, name string) proto.Machine {
	t := s.cfg.Targets[name]
	m := proto.Machine{Name: name, Addr: t.Addr, Executors: []string{}}
	start := time.Now()
	// 注意：token 只进请求头，绝不进日志
	st, err := client.New(t.Addr, t.Token).Status(ctx)
	m.ProbeMs = time.Since(start).Milliseconds()
	if err != nil {
		s.log.Warn("机器探活失败", "machine", name, "addr", t.Addr,
			"probe_ms", m.ProbeMs, "cause", err)
		m.Error = err.Error()
		return m
	}
	m.Reachable = true
	fillFromStatus(&m, st)
	s.log.Debug("机器探活成功", "machine", name, "probe_ms", m.ProbeMs,
		"active_tasks", m.ActiveTasks)
	return m
}

// fillFromStatus 把 GET /api/status 的响应投影进机器条目。
//
// executors / default_executor 是探活本来就拿到的东西，丢掉纯属浪费；
// active_tasks 由 TaskCounts 的非终态部分求和得到。
func fillFromStatus(m *proto.Machine, st *proto.StatusResp) {
	m.Reachable = true
	m.Version = st.Version.Version
	if st.Executors != nil {
		m.Executors = st.Executors
	}
	m.DefaultExecutor = st.DefaultExecutor
	m.ActiveTasks = len(st.Active)
}

// handleMachines 处理 GET /api/machines。
func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	s.log.Info("机器列表请求", "remote_addr", r.RemoteAddr)
	writeJSON(w, http.StatusOK, s.probeMachines(r.Context()))
}
```

路由表加一行（放在 `GET /api/projects/tree` 之后）：

```go
	mux.HandleFunc("GET /api/machines", s.handleMachines)
```

**若 `proto.StatusResp` 的字段名与上面不符**（例如 `Version` 是 `BuildInfo` 而非字符串），以 `internal/proto/status.go` 的实际定义为准调整 `fillFromStatus`，**不要改 proto**。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run TestProbeMachines -v`
Expected: PASS。

- [ ] **Step 5: 加关键节点日志**

已含：请求入口、整轮完成（machines + unreachable 计数）、单台失败（Warn，machine/addr/probe_ms/cause）、单台成功（Debug）。**自查：任何一条日志都不许出现 token。**

- [ ] **Step 6: 加注释**

确认文件头写了「不建表」的 why；`machineProbeBudget` 的 3s 理由；本机不自拨 HTTP 的 why；排序稳定的 why；`probeRemote` 里「token 只进请求头」的注释。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/machines.go internal/agentd/machines_test.go internal/agentd/server.go
git commit -m "feat(agentd): GET /api/machines——投影 targets + 并发探活，不可达是数据不是错误"
```

---

## Task 5: 转发基座与显式按机器路由（`?machine=`）

**Files:**
- Create: `internal/agentd/forward.go`
- Test: `internal/agentd/forward_test.go`
- Modify: `internal/agentd/server.go`（`handleProjectAdd`、`handleProjectRemove` 前置分流）

**Interfaces:**
- Consumes: `cfg.Targets`
- Produces: 常量 `forwardedHeader = "X-Handoff-Forwarded"`；`func isForwarded(r *http.Request) bool`；`func (s *Server) forwardIfRequested(w http.ResponseWriter, r *http.Request) bool`（true=已处理，调用方直接 return）；`func (s *Server) forwardTo(w http.ResponseWriter, r *http.Request, name, addr, token string)`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/forward_test.go`（`package agentd`）：

```go
package agentd

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/xushixin/handoff/internal/config"
)

// TestForwardProjectAddToNamedMachine 断言：带 ?machine= 的登记请求被原样搬到
// 那台机器，响应状态码与报文原样透传。
func TestForwardProjectAddToNamedMachine(t *testing.T) {
	remote := newTestEnv(t) // 远程那台：manager 未注入，登记必 503
	local := newTestEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox",
		bytes.NewReader([]byte(`{"origin_url":"git@github.com:x/h.git"}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// 远端答什么就透什么：状态码与中文报错原文一律不改写
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望原样透传远端的 503；体=%s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("manager 未就绪")) {
		t.Errorf("远端报错原文必须原样透传，实得 %s", body)
	}
}

// TestForwardUnknownMachineRejected 断言：机器名不在 targets 里 → 400 且点名它。
func TestForwardUnknownMachineRejected(t *testing.T) {
	local := newTestEnv(t)
	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=ghost", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("ghost")) {
		t.Errorf("报文必须点名那个机器名，实得 %s", body)
	}
}

// TestForwardedRequestNeverForwardsAgain 是防环的核心断言：带转发头的请求
// 一律本机处理，哪怕它自己也带着 ?machine=。
func TestForwardedRequestNeverForwardsAgain(t *testing.T) {
	local := newTestEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(forwardedHeader, "1")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	// devbox 是黑洞地址：真转发了就会是 502/超时；本机处理则是 503（manager 未注入）
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("带转发头的请求必须本机处理，实得状态码 %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestForward -v`
Expected: FAIL，`undefined: forwardedHeader`。

- [ ] **Step 3: 写实现**

新建 `internal/agentd/forward.go`：

```go
// 本文件实现 agentd → agentd 的请求转发基座。
//
// 职责：
//   - 判定一个请求是否要转发（显式 ?machine= / 按任务 id 路由都走这里的搬运）
//   - 原样搬运：方法、路径、请求体、查询参数（去掉路由用的 machine）
//   - 原样回送：状态码、Content-Type、响应体一律不改写
//   - 防环：转发请求带 X-Handoff-Forwarded: 1，带此头的请求一律本机处理
//
// 边界：
//   - 不解释业务语义：登记契约由目标机器解释，本机只做搬运，不加校验也不加解释
//   - 不做凭据转换：用 cfg.Targets 里现成的 addr+token（与 CLI --target 同源
//     同凭据，信任模型零新增）。**token 绝不进日志**
//   - 不做重试：转发失败即如实回 502 带原文。重试会让「已登记成功但响应丢了」
//     变成重复登记
package agentd

import (
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// forwardedHeader 是防环标记。一跳封顶：A→B→A 不可能成环。
const forwardedHeader = "X-Handoff-Forwarded"

// forwardBodyLimit 是搬运请求体的上限，与 handleProjectAdd 的 1MB 一致。
const forwardBodyLimit = 1 << 20

// isForwarded 报告该请求是否已经是别的 agentd 转过来的。
//
// 带此头的请求**永不再向外扇出**（scope=all 降级为仅本机、?machine= 忽略）。
func isForwarded(r *http.Request) bool { return r.Header.Get(forwardedHeader) != "" }

// forwardIfRequested 处理显式 ?machine= 路由。
//
// 返回：
//   - true 表示请求已被处理（已转发或已拒绝），调用方必须直接 return
//   - false 表示这是本机的活，继续原来的处理
//
// 注意：
//   - machine 省略或为空串 = 本机（与 Task.Machine 的空串语义一致）
//   - 带转发头时一律返回 false（防环优先于路由）
func (s *Server) forwardIfRequested(w http.ResponseWriter, r *http.Request) bool {
	name := r.URL.Query().Get("machine")
	if name == "" || isForwarded(r) {
		return false
	}
	t, ok := s.cfg.Targets[name]
	if !ok {
		s.log.Warn("转发被拒：机器名未在配置中定义", "machine", name, "path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "机器 " + name + " 未在本机配置的 targets 中定义"})
		return true
	}
	s.forwardTo(w, r, name, t.Addr, t.Token)
	return true
}

// forwardTo 把请求原样搬到目标机器，并把响应原样回送。
//
// 参数：
//   - name/addr/token: 目标机器的名字、地址与令牌（token 只进请求头）
//
// 注意：
//   - **不设独立超时**：跟随 r.Context()。项目登记可能触发目标机 clone，耗时
//     以分钟计；§5.2 的 3s 预算约束的是**汇总扇出**，不是这条显式路由。
//     浏览器/CLI 断开时 r.Context() 取消，上游连接随之断开
//   - 转发失败回 502 带原文：这是本机与目标机之间的问题，不能伪装成目标机
//     的业务错误
func (s *Server) forwardTo(w http.ResponseWriter, r *http.Request, name, addr, token string) {
	target, err := forwardURL(addr, r.URL)
	if err != nil {
		s.log.Error("转发失败：目标地址不合法", "machine", name, "addr", addr, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return
	}
	start := time.Now()
	s.log.Info("转发请求", "machine", name, "method", r.Method, "path", r.URL.Path)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target,
		io.LimitReader(r.Body, forwardBodyLimit))
	if err != nil {
		s.log.Error("转发失败：构造请求", "machine", name, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set(forwardedHeader, "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Error("转发失败：上游不可达", "machine", name, "path", r.URL.Path,
			"elapsed_ms", time.Since(start).Milliseconds(), "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发到 " + name + " 失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	n, cerr := io.Copy(w, resp.Body)
	if cerr != nil {
		// 头已经写出去了，改不了状态码，只能记账
		s.log.Warn("转发响应回送中断", "machine", name, "written", n, "cause", cerr)
	}
	if resp.StatusCode >= 400 {
		s.log.Warn("转发上游返回非 2xx（原样透传，不改写）", "machine", name,
			"path", r.URL.Path, "status", resp.StatusCode,
			"elapsed_ms", time.Since(start).Milliseconds())
		return
	}
	s.log.Info("转发完成", "machine", name, "path", r.URL.Path,
		"status", resp.StatusCode, "elapsed_ms", time.Since(start).Milliseconds())
}

// forwardURL 拼出目标 URL：目标机地址 + 原路径 + 去掉 machine 的查询串。
//
// 为什么要摘掉 machine：它是**本机的路由指令**，不是业务参数。留着它，目标机
// 看到的就是一个「让我转发给我自己」的请求——虽然被防环头挡住，但语义上是脏的。
func forwardURL(addr string, src *url.URL) (string, error) {
	base, err := url.Parse(normalizeAddr(addr))
	if err != nil {
		return "", err
	}
	q := src.Query()
	q.Del("machine")
	base.Path = src.Path
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// normalizeAddr 给缺 scheme 的地址补 http://，与 client.New 的行为一致。
//
// 为什么不直接复用 client 里的那份：那是包内私有的，且这里只需要这三行。
// 若将来 client 导出了它，换过来即可。
func normalizeAddr(addr string) string {
	if !strings.Contains(addr, "://") {
		return "http://" + addr
	}
	return addr
}
```

（import 清单以编译器为准：上面用到 `io`/`net/http`/`net/url`/`strings`/`time`。）

在 `handleProjectAdd` 与 `handleProjectRemove` 的**最开头**（`s.log.Info(...)` 之后、`s.mgr == nil` 判断之前）各加一句分流：

```go
	if s.forwardIfRequested(w, r) {
		return // 显式指名了别的机器：本机只做搬运（W3a §5.1.1）
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run TestForward -v`
Expected: PASS（三条）。

- [ ] **Step 5: 加关键节点日志**

已含：转发发起（machine/method/path）、上游非 2xx（status + 耗时，Warn）、上游不可达（Error + cause）、转发完成（status + 耗时）、回送中断（Warn）。**自查：没有一条日志打了 token 或请求体。**

- [ ] **Step 6: 加注释**

确认文件头四条职责 + 三条边界；`forwardedHeader` 的一跳封顶说明；`forwardTo` 的「不设独立超时」why（clone 可能几分钟）；`forwardURL` 摘掉 machine 的 why；「不做重试」的 why。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/forward.go internal/agentd/forward_test.go internal/agentd/server.go
git commit -m "feat(agentd): 转发基座 + 项目登记/注销的显式 ?machine= 路由（防环一跳封顶）"
```

---

## Task 6: 项目树跨机汇总（`?scope=all`）

**Files:**
- Create: `internal/agentd/projectfanout.go`
- Test: `internal/agentd/projectfanout_test.go`
- Modify: `internal/agentd/projecttree.go`（`handleProjectTree` 分流）、`internal/client/client.go`（加 `ProjectTree` 方法与转发标记）

**Interfaces:**
- Consumes: Task 3 的 `buildLocalTree`；Task 5 的 `forwardedHeader`
- Produces: `func (c *Client) ProjectTree(ctx context.Context) (*proto.ProjectTreeResp, error)`；`func (c *Client) MarkForwarded() *Client`；`func (s *Server) buildTreeAll(ctx context.Context) proto.ProjectTreeResp`

- [ ] **Step 1: 给 client 加取树能力（含转发标记）**

`internal/client/client.go`：给 `Client` 加一个只读的额外请求头，并在 `do` 里带上。

```go
// extraHeaders 是每个请求都要带的附加头（目前只有 agentd→agentd 的防环标记）。
// nil 表示没有附加头，生产上的审核者客户端恒为 nil。
extraHeaders map[string]string
```

```go
// MarkForwarded 返回一个副本，其后续请求都带上 X-Handoff-Forwarded: 1。
//
// 用途：agentd 扇出到别的 agentd 时必须带这个标记，让对端不再向外扇出——
// 一跳封顶，A→B→A 不可能成环。审核者 CLI **不要**用它。
func (c *Client) MarkForwarded() *Client {
	cp := *c
	cp.extraHeaders = map[string]string{"X-Handoff-Forwarded": "1"}
	return &cp
}
```

在 `do` 里设置 Bearer 之后加：

```go
	for k, v := range c.extraHeaders {
		req.Header.Set(k, v)
	}
```

再加取树方法（照 `ProjectList` 的写法：`do` → 非 2xx 走 `httpError` → `json.NewDecoder`）：

```go
// ProjectTree 取项目树（GET /api/projects/tree）。
//
// 注意：本方法只取**单机**树。跨机汇总是 agentd 侧的事（它对每台取单机树再合并），
// 客户端拿汇总请打 ?scope=all 的那条路径，由 agentd 负责扇出。
func (c *Client) ProjectTree(ctx context.Context) (*proto.ProjectTreeResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/projects/tree", nil)
	if err != nil {
		return nil, fmt.Errorf("请求项目树: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("项目树", resp)
	}
	var out proto.ProjectTreeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析项目树响应: %w", err)
	}
	return &out, nil
}
```

- [ ] **Step 2: 写失败的测试**

新建 `internal/agentd/projectfanout_test.go`（`package agentd`）：

```go
package agentd

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
)

// TestBuildTreeAllMergesAndReportsAbsence 是 §5.3 硬约束的核心断言：
// 一台可达一台不可达，两台都必须出现在 machines 里，失败那台 ok=false 带原文。
func TestBuildTreeAllMergesAndReportsAbsence(t *testing.T) {
	remote := newTestEnv(t)
	remoteRepo := initGitRepoWithOrigin(t, "git@github.com:x/handoff.git")
	if _, err := remote.st.CreateProjectLocation(proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff-dev", Path: remoteRepo,
		OriginURL: "git@github.com:x/handoff.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("远端登记失败: %v", err)
	}

	localRepo := initGitRepoWithOrigin(t, "git@github.com:x/handoff.git")
	local := newTestEnvWithCfg(t, &config.Config{
		Token: testToken,
		Targets: map[string]config.Target{
			"devbox": {Addr: remote.ts.URL, Token: testToken},
			"nas":    {Addr: "http://127.0.0.1:1", Token: testToken},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := local.st.CreateProjectLocation(proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff", Path: localRepo,
		OriginURL: "git@github.com:x/handoff.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("本机登记失败: %v", err)
	}

	tree := local.srv.buildTreeAll(context.Background())

	// 三台都要在 machines 里（本机 + devbox + nas），nas 必须 ok=false 带原因
	if len(tree.Machines) != 3 {
		t.Fatalf("machines 数 = %d，期望 3：%+v", len(tree.Machines), tree.Machines)
	}
	for _, m := range tree.Machines {
		if m.Name == "nas" {
			if m.Ok || m.Error == "" {
				t.Errorf("不可达的机器必须 ok=false 且 error 非空：%+v", m)
			}
		}
		if m.FetchedAt.IsZero() {
			t.Errorf("每台都要有 fetched_at：%+v", m)
		}
	}
	// 同一个 origin 在两台机器上 → 同一个项目下两个 location，machine 互不相同
	if len(tree.Projects) != 1 {
		t.Fatalf("同 origin 必须归并成一个项目，实得 %d 个", len(tree.Projects))
	}
	seen := map[string]bool{}
	for _, l := range tree.Projects[0].Locations {
		if seen[l.Machine] {
			t.Errorf("同项目下 machine 不得重复：%q", l.Machine)
		}
		seen[l.Machine] = true
	}
	if !seen[""] || !seen["devbox"] {
		t.Errorf("本机与 devbox 的 location 都要在：%+v", tree.Projects[0].Locations)
	}
}

// TestTreeAllDegradesWhenForwarded 断言：带转发头时 scope=all 降级为仅本机。
func TestTreeAllDegradesWhenForwarded(t *testing.T) { /* 见 Step 4 的实现后补齐 */ }
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestBuildTreeAll -v`
Expected: FAIL，`undefined: buildTreeAll`。

- [ ] **Step 4: 写实现**

新建 `internal/agentd/projectfanout.go`：

```go
// 本文件实现项目树的跨机汇总：GET /api/projects/tree?scope=all。
//
// 职责：
//   - 对每台 target 现场取它的**单机**树，按 project_id 与本机的树合并
//   - 给每台机器的 location 盖上 machine 名
//   - 无论成败，每台机器都在响应的 machines 里留一行
//
// 边界：
//   - 现场扇出（不读缓存）：项目登记是低频操作，实时性换简单。任务列表的
//     scope=all 走的是镜像快照（见 tasksfanout），两者刻意不同
//   - 一跳封顶：扇出请求带 X-Handoff-Forwarded，对端不再扇出
//   - 单台失败不影响整体：整体恒 200
package agentd

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// treeFanoutBudget 是整轮扇出的总预算（§5.2：短于任何调用方超时）。
const treeFanoutBudget = 3 * time.Second

// buildTreeAll 汇总本机与全部 target 的项目树。
//
// 返回值恒有效：单台失败记进 Machines 那一行，不影响其余机器与本机的数据。
func (s *Server) buildTreeAll(ctx context.Context) proto.ProjectTreeResp {
	out, err := s.buildLocalTree(ctx)
	if err != nil {
		// 连本机的树都读不出来：仍然返回可用信封，让 UI 看到本机 ok=false
		out = proto.ProjectTreeResp{Projects: []proto.ProjectNode{}, Unowned: []string{}}
		out.Machines = []proto.MachineStatus{{Name: "", Ok: false,
			FetchedAt: time.Now().UTC(), Error: err.Error()}}
	} else {
		out.Machines = []proto.MachineStatus{{Name: "", Ok: true, FetchedAt: time.Now().UTC()}}
	}

	names := make([]string, 0, len(s.cfg.Targets))
	for name := range s.cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)

	fanCtx, cancel := context.WithTimeout(ctx, treeFanoutBudget)
	defer cancel()

	type result struct {
		status proto.MachineStatus
		tree   *proto.ProjectTreeResp
	}
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			t := s.cfg.Targets[name]
			st := proto.MachineStatus{Name: name, FetchedAt: time.Now().UTC()}
			tree, err := client.New(t.Addr, t.Token).MarkForwarded().ProjectTree(fanCtx)
			if err != nil {
				s.log.Warn("项目树扇出失败", "machine", name, "addr", t.Addr, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			st.Ok = true
			results[i] = result{status: st, tree: tree}
		}(i, name)
	}
	wg.Wait()

	for _, r := range results {
		out.Machines = append(out.Machines, r.status)
		if r.tree == nil {
			continue
		}
		mergeTree(&out, r.status.Name, *r.tree)
	}
	s.log.Info("项目树汇总完成", "machines", len(out.Machines),
		"projects", len(out.Projects))
	return out
}

// mergeTree 把一台远程机器的单机树并进汇总结果。
//
// 合并键是 project_id——它是同一个纯函数从同一个 origin 算出的，跨机天然相等，
// **不需要任何协商**。同一项目在不同机器上的 location 因此排在一起。
func mergeTree(dst *proto.ProjectTreeResp, machine string, src proto.ProjectTreeResp) {
	idx := map[string]int{}
	for i, p := range dst.Projects {
		idx[p.ProjectID] = i
	}
	for _, p := range src.Projects {
		for i := range p.Locations {
			// 远端答的是它的单机树，machine 恒空串；由**汇总方**盖章为 target 名
			p.Locations[i].Machine = machine
		}
		if i, ok := idx[p.ProjectID]; ok {
			dst.Projects[i].Locations = append(dst.Projects[i].Locations, p.Locations...)
			continue
		}
		idx[p.ProjectID] = len(dst.Projects)
		dst.Projects = append(dst.Projects, p)
	}
	dst.Unowned = append(dst.Unowned, src.Unowned...)
}
```

`handleProjectTree` 改成分流：

```go
	if scope == "all" && !isForwarded(r) {
		// 带转发头时降级为仅本机（防环优先于范围）
		writeJSON(w, http.StatusOK, s.buildTreeAll(r.Context()))
		return
	}
```

- [ ] **Step 5: 补齐降级测试并运行**

把 Step 2 里占位的 `TestTreeAllDegradesWhenForwarded` 写实：对 `local.ts.URL+"/api/projects/tree?scope=all"` 发请求并带 `forwardedHeader: 1`，断言响应里 `machines` 为空（`omitempty` 不出现）且不含远端数据——**因为带转发头的请求一律不扇出**。

Run: `go test ./internal/agentd/ -run 'TreeAll' -v`
Expected: PASS（两条）。

- [ ] **Step 6: 加关键节点日志**

已含：单台扇出失败（Warn，machine/addr/cause）、汇总完成（Info，机器数/项目数）。再确认 `buildLocalTree` 本身的日志没被绕过。**token 不进日志。**

- [ ] **Step 7: 加注释**

确认：文件头写了「现场扇出 vs 镜像快照」的分工与 why；`mergeTree` 里「project_id 跨机天然相等，无需协商」；「machine 由汇总方盖章」；`treeFanoutBudget` 的 3s 依据。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/projectfanout.go internal/agentd/projectfanout_test.go internal/agentd/projecttree.go internal/client/client.go
git commit -m "feat: 项目树跨机汇总——按 project_id 归并，缺席的机器必在 machines 里带原因"
```

---

## Task 7: 事件镜像存储（`mirror_events` / `mirror_tasks`）

**Files:**
- Create: `internal/store/mirror.go`
- Test: `internal/store/mirror_test.go`
- Modify: `internal/store/store.go`（Open 的建表列表加两张表）

**Interfaces:**
- Produces:
  - `func (s *Store) AppendMirrorEvent(taskID string, ev proto.Event) (inserted bool, err error)`
  - `func (s *Store) MirrorWatermark(taskID string) (int64, error)`
  - `func (s *Store) MirrorEventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error)`
  - `func (s *Store) UpsertMirrorTask(target string, task proto.Task, fetchedAt time.Time) error`
  - `func (s *Store) ListMirrorTasks() ([]MirrorTask, error)`
  - `func (s *Store) MirrorTaskTarget(taskID string) (string, bool, error)`
  - `func (s *Store) DeleteMirrorTask(taskID string) error`
  - `type MirrorTask struct { Task proto.Task; Target string; FetchedAt time.Time }`

- [ ] **Step 1: 写失败的测试**

新建 `internal/store/mirror_test.go`：

```go
// 镜像存储测试：幂等追加、水位、区间读、快照 upsert 与路由索引。
package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

func TestAppendMirrorEventIdempotent(t *testing.T) {
	st := openTestStore(t) // 复用 store_test.go 里既有的开库辅助；名字以实际为准
	ev := proto.Event{Seq: 7, TaskID: "T1", Type: proto.EventTypeQuestion,
		Payload: json.RawMessage(`{"text":"继续吗"}`), CreatedAt: time.Now().UTC()}

	first, err := st.AppendMirrorEvent("T1", ev)
	if err != nil || !first {
		t.Fatalf("首次追加应插入：inserted=%v err=%v", first, err)
	}
	// 重连补拉会把同一条再送一遍：必须幂等，且如实报告「没插入」
	again, err := st.AppendMirrorEvent("T1", ev)
	if err != nil {
		t.Fatalf("重复追加不该报错：%v", err)
	}
	if again {
		t.Error("重复的 (task_id, seq) 不该被计为插入")
	}

	wm, err := st.MirrorWatermark("T1")
	if err != nil || wm != 7 {
		t.Fatalf("水位 = %d（err=%v），期望 7", wm, err)
	}
	// 没有任何镜像事件的任务，水位是 0——首次订阅从头拉
	if wm2, err := st.MirrorWatermark("T-none"); err != nil || wm2 != 0 {
		t.Fatalf("空任务水位 = %d（err=%v），期望 0", wm2, err)
	}

	evs, err := st.MirrorEventsFrom("T1", 0, 100)
	if err != nil || len(evs) != 1 || evs[0].Seq != 7 {
		t.Fatalf("区间读结果不对：%+v err=%v", evs, err)
	}
	// 远端 seq 原值保留：本机不重编号，重连凭它续拉
	if evs[0].Type != proto.EventTypeQuestion {
		t.Errorf("事件类型丢了：%+v", evs[0])
	}
	if none, _ := st.MirrorEventsFrom("T1", 7, 100); len(none) != 0 {
		t.Errorf("from_seq 是开区间，seq=7 不该再出现：%+v", none)
	}
}

func TestMirrorTaskSnapshotAndRouting(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	task := proto.Task{ID: "T1", Name: "远端任务", State: proto.TaskStateRunning,
		RepoPath: "/remote/handoff", CreatedAt: now, UpdatedAt: now}
	if err := st.UpsertMirrorTask("devbox", task, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}
	task.State = proto.TaskStateWaitingReview
	if err := st.UpsertMirrorTask("devbox", task, now.Add(time.Minute)); err != nil {
		t.Fatalf("二次 upsert: %v", err)
	}

	list, err := st.ListMirrorTasks()
	if err != nil || len(list) != 1 {
		t.Fatalf("镜像任务数 = %d（err=%v），期望 1", len(list), err)
	}
	if list[0].Task.State != proto.TaskStateWaitingReview {
		t.Errorf("快照应被覆盖为最新状态：%+v", list[0].Task)
	}
	if list[0].Target != "devbox" {
		t.Errorf("target 丢了：%+v", list[0])
	}

	target, ok, err := st.MirrorTaskTarget("T1")
	if err != nil || !ok || target != "devbox" {
		t.Fatalf("路由索引查不到：target=%q ok=%v err=%v", target, ok, err)
	}
	if _, ok, _ := st.MirrorTaskTarget("T-none"); ok {
		t.Error("不存在的任务不该报告命中")
	}
}
```

（`openTestStore` 用 `internal/store/store_test.go` 里既有的开库辅助，名字以实际为准；没有就照它的写法内联一个。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run Mirror -v`
Expected: FAIL，`undefined: AppendMirrorEvent`。

- [ ] **Step 3: 建表**

在 `internal/store/store.go` 的 `Open` 建表列表末尾追加（保持 `IF NOT EXISTS` 的幂等风格）：

```go
		// 镜像两表（W3a §6.2）：远端权威日志的**副本**，不是第二份真相。
		// 可随时整表删掉，从远端按 from_seq=0 重建。
		//
		// 为什么不混进本机 events 表：远端 events.seq 是**远端库的全局自增**，
		// 本机 seq 也是全局自增主键，混表必撞。
		`CREATE TABLE IF NOT EXISTS mirror_events (
  task_id TEXT NOT NULL,
  -- seq 保留远端原值：远端是权威，本机不重编号，重连凭它续拉
  seq INTEGER NOT NULL,
  type TEXT NOT NULL, payload TEXT NOT NULL, created_at TIMESTAMP NOT NULL,
  -- 复合主键即幂等键：重连补拉重复到达时 INSERT OR IGNORE 静默跳过
  PRIMARY KEY (task_id, seq))`,
		`CREATE TABLE IF NOT EXISTS mirror_tasks (
  task_id TEXT PRIMARY KEY,
  -- target 是 §5.1 透明路由的索引：这条任务该转发给谁
  target TEXT NOT NULL,
  -- snapshot 是最近一次拉到的任务体 JSON（§6.3 的事件触发刷新 + 慢对账）
  snapshot TEXT NOT NULL,
  fetched_at TIMESTAMP NOT NULL)`,
```

- [ ] **Step 4: 写实现**

新建 `internal/store/mirror.go`，按 `internal/store/projects.go` 的既有风格（`ExecContext`/`QueryContext` + 包装错误 + 中文错误文案）实现上面 8 个 API。要点：

- `AppendMirrorEvent`：`INSERT OR IGNORE INTO mirror_events(...) VALUES(?,?,?,?,?)`，用 `RowsAffected()` 判断是否真插入。
- `MirrorWatermark`：`SELECT COALESCE(MAX(seq),0) FROM mirror_events WHERE task_id=?`。
- `MirrorEventsFrom`：`WHERE task_id=? AND seq>? ORDER BY seq ASC LIMIT ?`（**开区间**，与本机 `EventsFromAsc` 的语义一致）。
- `UpsertMirrorTask`：`INSERT INTO mirror_tasks(...) VALUES(...) ON CONFLICT(task_id) DO UPDATE SET target=excluded.target, snapshot=excluded.snapshot, fetched_at=excluded.fetched_at`；snapshot 用 `json.Marshal(task)`。
- `ListMirrorTasks`：扫全表，`json.Unmarshal` 回 `proto.Task`；**单行解析失败只 Warn 跳过，不让整个列表失败**（副本脏了不该让看板挂掉）——注意 store 包里拿 logger 的方式以本包既有写法为准，没有就返回错误里带上 task_id。
- `MirrorTaskTarget`：`SELECT target FROM mirror_tasks WHERE task_id=?`，`sql.ErrNoRows` → `("", false, nil)`。
- `DeleteMirrorTask`：同时删 `mirror_tasks` 与该任务的 `mirror_events`，放一个事务里。

文件头注释必须写清楚：**镜像是 replication 不是真相**，权威始终在任务所在机器；可整表删除重建。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/store/ -run Mirror -v`
Expected: PASS（两条）。

- [ ] **Step 6: 加关键节点日志**

store 层是被高频调用的底座，**不要每次 append 都打日志**。只在这两处打：
- `ListMirrorTasks` 遇到解析不了的快照行：Warn + task_id（副本脏了必须留痕）；
- `DeleteMirrorTask` 成功：Info + task_id + 删掉的事件条数（这是唯一的破坏性操作）。

- [ ] **Step 7: 加注释**

确认：文件头「副本非真相」；DDL 里三条 why（seq 保留原值 / 复合主键即幂等键 / 不混进 events 表）；`MirrorEventsFrom` 注明开区间。

- [ ] **Step 8: Commit**

```bash
git add internal/store/mirror.go internal/store/mirror_test.go internal/store/store.go
git commit -m "feat(store): 事件镜像两表——INSERT OR IGNORE 幂等 + 快照 upsert + 路由索引"
```

---

## Task 8: 客户端无 cursor 事件流

**Files:**
- Modify: `internal/client/client.go`
- Test: `internal/client/mirrorstream_test.go`

**Interfaces:**
- Consumes: 已有的私有 `streamOnce(ctx, taskID, fromSeq, readDeadline, onFrame)`
- Produces: `func (c *Client) StreamEventsOnce(ctx context.Context, taskID string, fromSeq int64, onEvent func(proto.Event) error) error`

- [ ] **Step 1: 写失败的测试**

新建 `internal/client/mirrorstream_test.go`：起一个 `httptest` WS 服务端（照 `internal/client` 里既有的 WS 测试写法），推两条事件，断言：

1. `StreamEventsOnce` 把两条都交给了 `onEvent`；
2. **调用前后 `~/.handoff/cursor-<task>` 都不存在**（把 `HOME` 指到 `t.TempDir()` 再断言目录里没有 cursor 文件）。

第 2 条是本任务存在的全部理由，测试必须有它。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/client/ -run StreamEventsOnce -v`
Expected: FAIL，`undefined: StreamEventsOnce`。

- [ ] **Step 3: 写实现**

在 `internal/client/client.go` 里加：

```go
// StreamEventsOnce 建立一次事件流连接，把收到的每一帧交给 onEvent，直到连接
// 断开或 ctx 取消。**不读写任何 cursor 文件，不做重连**。
//
// 参数：
//   - taskID: 任务 id（必须是完整 UUID）
//   - fromSeq: 起始 seq（开区间）；调用方自己持有水位
//   - onEvent: 每帧回调；返回错误即中止本次连接
//
// 为什么必须有这个「无 cursor」变体：FollowEvents / WaitEvent 把水位存在
// ~/.handoff/cursor-<task>，那是**审核者本机**的状态。agentd 做事件镜像时
// 跑在同一台机器上，若复用带 cursor 的路径，agentd 的镜像与人手敲的
// handoff wait 会互相推进对方的水位——一方吃掉另一方的事件，且极难归因。
// 镜像的水位属于 mirror_events 表，不属于文件系统。
//
// 注意：单次连接、不重连。退避与重连策略由调用方（镜像订阅循环）决定，
// 它的节奏（300ms→×2→10s）与审核者 CLI 的（1s→60s）刻意不同。
func (c *Client) StreamEventsOnce(ctx context.Context, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	c.log().Debug("镜像事件流建立", "addr", c.baseURL, "task", taskID, "from_seq", fromSeq)
	// readDeadline 返回零值 = 不设读超时：镜像是常驻订阅，长时间无事件是正常态
	return c.streamOnce(ctx, taskID, fromSeq, func() time.Time { return time.Time{} }, onEvent)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/client/ -run StreamEventsOnce -v`
Expected: PASS。

- [ ] **Step 5 / 6: 日志与注释自检**

日志：连接建立（Debug，含 from_seq）。断开与错误由 `streamOnce` 自己打，不要重复打。
注释：上面的 doc comment 已含「为什么必须有无 cursor 变体」——这是本任务最重要的一段字，别删。

- [ ] **Step 7: Commit**

```bash
git add internal/client/client.go internal/client/mirrorstream_test.go
git commit -m "feat(client): StreamEventsOnce——无 cursor 单次事件流，供 agentd 镜像使用"
```

---

## Task 9: 发现式镜像（discovery loop + 上游订阅 + 快照刷新）

**Files:**
- Create: `internal/agentd/mirror.go`
- Test: `internal/agentd/mirror_test.go`
- Modify: `cmd/agentd.go`（启动 `RunMirror`，与 `RunWatchdog` 同处）

**Interfaces:**
- Consumes: Task 7 的 store API；Task 8 的 `StreamEventsOnce`；`client.ListTasks`；`Hub.Publish`
- Produces: `type Mirror struct{...}`；`func NewMirror(cfg *config.Config, st *store.Store, hub *Hub, log *slog.Logger) *Mirror`；`func (m *Mirror) Run(ctx context.Context)`；`func (m *Mirror) discoverOnce(ctx context.Context)`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/mirror_test.go`（`package agentd`）。用一台真 agentd 当远端，直接往它的 store 里塞任务与事件，然后跑**一轮** `discoverOnce`，断言本机镜像表被填上：

```go
// TestMirrorDiscoverOnceSubscribesActiveTasks 断言一轮发现即：
// 活跃任务进 mirror_tasks（带 target），其事件被复制进 mirror_events。
func TestMirrorDiscoverOnceSubscribesActiveTasks(t *testing.T) {
	remote := newTestEnv(t)
	now := time.Now().UTC()
	taskID := uuid.NewString()
	mustCreateTask(t, remote.st, &proto.Task{ID: taskID, Name: "远端活",
		State: proto.TaskStateRunning, RepoPath: "/remote/handoff",
		CreatedAt: now, UpdatedAt: now})
	if _, err := remote.st.AppendEvent(taskID, proto.EventTypeQuestion,
		json.RawMessage(`{"text":"继续吗"}`)); err != nil {
		t.Fatalf("远端落事件: %v", err)
	}

	localSt := newTestStore(t)
	hub := NewHub()
	cfg := &config.Config{Token: testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}}}
	m := NewMirror(cfg, localSt, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.discoverOnce(ctx)

	// 快照：一轮发现之后就该有
	list, err := localSt.ListMirrorTasks()
	if err != nil || len(list) != 1 || list[0].Target != "devbox" {
		t.Fatalf("镜像任务不对：%+v err=%v", list, err)
	}
	// 事件：订阅是异步的，等到水位推上去为止（最长 5s）
	deadline := time.Now().Add(5 * time.Second)
	for {
		wm, _ := localSt.MirrorWatermark(taskID)
		if wm > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("5s 内没有镜像到任何事件")
		}
		time.Sleep(50 * time.Millisecond)
	}
	evs, err := localSt.MirrorEventsFrom(taskID, 0, 10)
	if err != nil || len(evs) == 0 || evs[0].Type != proto.EventTypeQuestion {
		t.Fatalf("镜像事件不对：%+v err=%v", evs, err)
	}
	m.Stop() // 收掉全部订阅，别把 goroutine 漏给下一个测试
}

// TestMirrorDropsTerminalTasks 断言：终态任务不再订阅（快照仍在，供审阅历史）。
func TestMirrorDropsTerminalTasks(t *testing.T) { /* 同上，任务塞 completed，断言无订阅 */ }
```

（`AppendEvent` 的实际签名以 `internal/store/store.go` 为准。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestMirror -v`
Expected: FAIL，`undefined: NewMirror`。

- [ ] **Step 3: 写实现**

新建 `internal/agentd/mirror.go`。结构：

```go
// 本文件实现远端任务的事件镜像：让浏览器永远只连本机一条 WS，也能看到
// 远端任务的实时事件。
//
// 职责：
//   - discovery loop：每 mirrorDiscoveryTick 对每台 target 拉一次任务列表，
//     给活跃任务开上游订阅，给终态任务收掉订阅
//   - 上游订阅：从本机水位续拉，事件写进 mirror_events 并 Publish 进本机 Hub
//   - 快照刷新：收到事件即防抖刷新该 target 的任务快照（事件即门铃）
//
// 边界：
//   - 不改派发链路：dispatch --target 仍是 CLI 直拨远端，本机 agentd 不知情；
//     镜像因此挂在**发现**上而不是派发上
//   - 不推导状态：任务状态一律来自远端的 GET /api/tasks，本机不拿事件重算
//     状态机（那是重新实现一遍状态机，两份必然漂移）
//   - 不改 CLI wait：--target 直拨照旧。镜像跑稳后再谈让 wait 走本机
//   - 副本不是真相：整表删掉可从 from_seq=0 重建
package agentd

// mirrorDiscoveryTick 是发现轮询间隔（§6.1）。慢对账靠它补漏「不伴随事件的
// 跃迁」与断线空窗。
const mirrorDiscoveryTick = 30 * time.Second

// 上游断线重连退避（§6.1）：300ms 起、×2、上限 10s。
// 刻意快于审核者 CLI 的 1s→60s：镜像断线期间本机看板会显示陈旧数据，
// 而 CLI 断线只是一个人在等。
const (
	mirrorBackoffInitial = 300 * time.Millisecond
	mirrorBackoffMax     = 10 * time.Second
)

// snapshotDebounce 是「事件即门铃」的防抖窗口：突发事件合并成一次列表拉取。
const snapshotDebounce = 500 * time.Millisecond
```

`Mirror` 字段：`cfg/st/hub/log`、`mu sync.Mutex`、`subs map[string]context.CancelFunc`（task_id → 取消订阅）、`ring map[string]chan struct{}`（target → 防抖门铃）、`wg sync.WaitGroup`。

方法：

- `Run(ctx)`：`discoverOnce` 一次（启动即跑一轮），然后 ticker 循环；`ctx.Done()` 时 `Stop()`。日志：启动（tick）、退出（cause）。
- `discoverOnce(ctx)`：对每台 target 并发 `client.New(addr, token).MarkForwarded().ListTasks(ctx)`（3s 预算）：
  - 失败 → Warn（machine/cause），**继续下一台**；
  - 成功 → 对每条任务 `UpsertMirrorTask(name, task, now)`；活跃态（`!state.IsTerminal()`）且未订阅 → `go m.subscribe(ctx, name, target, taskID)`；终态且已订阅 → 取消订阅。
  - 每轮结束 Info 一行汇总：`"镜像发现完成", "machines", n, "subscribed", a, "dropped", b, "unreachable", c`。
- `subscribe(ctx, machine string, t config.Target, taskID string)`：循环 `StreamEventsOnce`，`fromSeq` 每轮从 `MirrorWatermark` 现读（这样重连自然续拉），`onEvent` 里：
  - `AppendMirrorEvent(taskID, ev)`；重复（inserted=false）就跳过，不 Publish（否则重连会给前端重复推同一条）；
  - 新插入 → `m.hub.Publish(ev)`（让本机的 `/ws/events` 订阅者立刻收到）+ 敲门铃刷快照；
  - 事件是终态（completed/failed）→ 记一条 Info，循环退出（终态后不再产生事件）。
  - 断线：退避重连，**日志必须能分辨「断线」与「收掉」**：断线 Warn 带 attempt 与下次退避，收掉 Info 带原因。
- `refreshSnapshot(ctx, machine, t)`：防抖后拉一次 `ListTasks` 并 upsert。失败 Warn。
- `Stop()`：取消全部订阅并 `wg.Wait()`。

`cmd/agentd.go` 里在起看门狗那处并列起镜像（**只在 `len(cfg.Targets) > 0` 时起**，没有远程机器就没必要开一条常驻循环，日志说明这一点）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run TestMirror -v`
Expected: PASS（两条）。再跑一次 `go test ./internal/agentd/ -race -run TestMirror`，确认没有数据竞争（`subs` map 必须在锁下访问）。

- [ ] **Step 5: 加关键节点日志**

对照 spec §8 逐条自查：
- 订阅建立：Info，`task` / `machine` / `from_seq`；
- 断线：Warn，`task` / `machine` / `attempt` / `backoff_ms` / `cause`；
- 重连成功：Info，`task` / `from_seq`（续拉水位）；
- 订阅收掉：Info，`task` / `reason`（terminal / stopped）；
- discovery 每轮：Info 一行汇总（见上），**单任务细节降 Debug**；
- 快照刷新失败：Warn + cause；成功：Debug（高频，防刷屏）；
- **token 一律不入日志**；机器名、addr、任务 id 可以。

- [ ] **Step 6: 加注释**

确认：文件头四条边界（不改派发链路 / 不推导状态 / 不改 CLI wait / 副本非真相）；三组常量各自的 why（尤其「退避为什么比 CLI 快」）；`onEvent` 里「重复事件不 Publish」的 why；`Stop` 为什么要 `wg.Wait`。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/mirror.go internal/agentd/mirror_test.go cmd/agentd.go
git commit -m "feat(agentd): 发现式事件镜像——30s 对账 + 上游订阅续拉 + 事件即门铃刷快照"
```

---

## Task 10: 按任务 id 的透明转发

**Files:**
- Create: `internal/agentd/taskroute.go`
- Test: `internal/agentd/taskroute_test.go`
- Modify: `internal/agentd/server.go`（`/api/tasks/{id}` 系列路由包一层）

**Interfaces:**
- Consumes: Task 5 的 `forwardTo` / `isForwarded`；Task 7 的 `MirrorTaskTarget`
- Produces: `func (s *Server) byTask(next http.HandlerFunc) http.HandlerFunc`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/taskroute_test.go`：远端 agentd 里塞一条任务，本机没有；对本机发 `GET /api/tasks/{id}`，断言：

1. 本机 `mirror_tasks` 有这条路由记录时 → 响应来自远端（断言任务名是远端那条）；
2. 两处都没有 → 404（与今天一致）；
3. 带 `forwardedHeader` 时不再转发 → 404（防环）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestTaskRoute -v`
Expected: FAIL。

- [ ] **Step 3: 写实现**

新建 `internal/agentd/taskroute.go`：

```go
// 本文件实现「按任务 id 的透明路由」：任务 id 是 UUID、全网唯一，因此
// /api/tasks/{id}/... 不需要调用方指定机器。
//
// 判定顺序（§5.1）：
//  1. 本机 tasks 表有该 id → 本机处理（现状不变）
//  2. 否则查镜像索引 mirror_tasks 得所属机器 → 原样转发
//  3. 两处都没有 → 交给本机处理，由它给出与今天一致的 404
//
// 边界：
//   - 不改任何被包住的 handler 的行为
//   - 带 X-Handoff-Forwarded 的请求一律本机处理（防环优先于路由）
//   - 不缓存判定结果：一次本机主键查询的成本远低于「任务刚归档但缓存说它在
//     远端」这类失真
package agentd

// byTask 包住 /api/tasks/{id}/... 系列 handler，按任务归属决定本机处理还是转发。
func (s *Server) byTask(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" || isForwarded(r) {
			next(w, r)
			return
		}
		if _, err := s.st.GetTask(id); err == nil {
			next(w, r) // 本机的活
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("任务路由：查本机任务失败", "task", id, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
			return
		}
		target, ok, err := s.st.MirrorTaskTarget(id)
		if err != nil {
			s.log.Error("任务路由：查镜像索引失败", "task", id, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
			return
		}
		if !ok {
			next(w, r) // 两处都没有：让 handler 给出与今天一致的 404
			return
		}
		t, defined := s.cfg.Targets[target]
		if !defined {
			// 镜像里记着一台配置里已经没有的机器：如实报告，别假装 404
			s.log.Warn("任务路由：镜像指向的机器已不在配置中",
				"task", id, "machine", target)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "任务在机器 " + target + " 上，但它已不在本机配置的 targets 中"})
			return
		}
		s.log.Info("任务路由：转发到远端", "task", id, "machine", target,
			"method", r.Method, "path", r.URL.Path)
		s.forwardTo(w, r, target, t.Addr, t.Token)
	}
}
```

`server.go` 里把 `/api/tasks/{id}` 及其全部子路径包起来（`GET /api/tasks/{id}`、reply、continue、done、stop、resume、diff、render、file、run 共 10 条）：

```go
	mux.HandleFunc("GET /api/tasks/{id}", s.byTask(s.handleGetTask))
	mux.HandleFunc("POST /api/tasks/{id}/reply", s.byTask(s.handleReply))
	// …其余 8 条同样包一层
```

**`render` 是流式响应**：`forwardTo` 用 `io.Copy` 直通，客户端断开时 `r.Context()` 取消、上游连接随之断开——这条路径不需要特殊处理，但要在 `byTask` 的注释里点明「流式也走同一条搬运」，免得后人以为漏了。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run TestTaskRoute -v` 然后 `go test ./internal/agentd/`（全包，确认 10 条路由包装没有破坏既有测试）。
Expected: PASS。

- [ ] **Step 5: 加关键节点日志**

已含：转发到远端（Info，task/machine/method/path）、镜像指向的机器已不在配置（Warn）、两处查询失败（Error + cause）。**不要**给「本机处理」加日志——那是绝大多数请求，会把日志淹掉。

- [ ] **Step 6: 加注释**

确认：文件头三步判定顺序 + 三条边界；「不缓存判定结果」的 why；「机器已不在配置里」为什么回 502 而不是 404。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/taskroute.go internal/agentd/taskroute_test.go internal/agentd/server.go
git commit -m "feat(agentd): 按任务 id 的透明转发——本机没有就查镜像索引转发，报文原样透传"
```

---

## Task 11: `/ws/events` 覆盖镜像任务

**Files:**
- Modify: `internal/agentd/server.go`（`handleEvents`）
- Test: `internal/agentd/mirrorws_test.go`

**Interfaces:**
- Consumes: Task 7 的 `MirrorEventsFrom` / `MirrorTaskTarget`
- Produces: 无新导出；`handleEvents` 内部多一条镜像分支

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/mirrorws_test.go`：往本机 `mirror_tasks` + `mirror_events` 直接塞一条任务与两条事件（不起真远端，测的是服务端读取路径），用 `websocket.Dial` 连 `/ws/events?task=<id>&from_seq=0`，断言：

1. 两条历史事件被重放，`seq` 保留远端原值；
2. 重放期间 `hub.Publish` 一条新事件，它随后也被收到（活事件续接）；
3. 帧形状与本机任务无差别（能直接 `json.Unmarshal` 成 `proto.Event`）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestMirrorWS -v`
Expected: FAIL（当前对镜像任务会以 `task not found` 关闭连接）。

- [ ] **Step 3: 写实现**

`handleEvents` 里那段「任务存在性校验」改为：本机查不到时**再查镜像**，命中则把本连接标记为镜像模式：

```go
	mirrored := false
	if _, err := s.st.GetTask(taskID); err != nil {
		if !errors.Is(err, store.ErrNotFound) { /* 原有 500 分支不动 */ }
		// 本机没有：可能是镜像任务（远端的活，本机订着它的事件）
		if _, ok, mErr := s.st.MirrorTaskTarget(taskID); mErr == nil && ok {
			mirrored = true
			s.log.Info("WS 订阅镜像任务", "task", taskID, "from_seq", fromSeq)
		} else {
			/* 原有 PolicyViolation 关闭分支不动 */
		}
	}
```

再把重放那处的取事件调用按 `mirrored` 分流：本机走原有的 `EventsFromAsc`，镜像走 `MirrorEventsFrom(taskID, fromSeq, s.replayLimit)`。**其余一律不动**——订阅、排空器、归并去重、会话复验、关闭码全部复用，因为镜像事件是 Task 9 通过同一个 `hub.Publish` 进来的，对本函数而言与本机事件无差别。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestMirrorWS|WS' -v`
Expected: PASS，且既有 WS 回归测试不变红。

- [ ] **Step 5: 加关键节点日志**

新增一条：`WS 订阅镜像任务`（Info，task + from_seq）。这是唯一能区分「浏览器在看本机任务」与「在看远端任务」的信号，必须有。

- [ ] **Step 6: 加注释**

在 `handleEvents` 的函数头注释里补一段：

```
//   - **镜像任务同形**：本机 tasks 表没有、但 mirror_tasks 有的任务，从
//     mirror_events 重放历史，活事件由镜像订阅经同一个 Hub 送来。对浏览器
//     协议完全同形（帧就是带 seq 的 Event），ws.ts 无感——这正是「浏览器
//     永远只连本机一条 WS」的兑现处
```

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/server.go internal/agentd/mirrorws_test.go
git commit -m "feat(agentd): /ws/events 覆盖镜像任务——历史从 mirror_events 重放，活事件同一个 Hub"
```

---

## Task 12: `GET /api/tasks` 的 `?project=` 与 `?scope=all`

**Files:**
- Create: `internal/agentd/tasksfanout.go`
- Test: `internal/agentd/tasksfanout_test.go`
- Modify: `internal/agentd/server.go`（`handleListTasks`）

**Interfaces:**
- Consumes: Task 2 的 `projectIndex`；Task 7 的 `ListMirrorTasks`
- Produces: `func (s *Server) tasksAll(ctx context.Context) proto.TasksResp`

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/tasksfanout_test.go`，三条：

1. `?project=<id>` 只返回该项目的任务（裸数组形状不变）；
2. `?scope=all` 返回 `proto.TasksResp` 信封：本机任务 `machine=""`、镜像任务带 target 名，`machines` 里每台都有一行且 `fetched_at` 非零；
3. **不带参数时响应逐字节仍是裸数组**——W2 契约一行不改。这条是本任务最容易被破坏的地方，必须显式断言（解到 `[]proto.TaskView` 成功、解到 `proto.TasksResp` 得到零值）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestTasks -v`
Expected: FAIL。

- [ ] **Step 3: 写实现**

新建 `internal/agentd/tasksfanout.go`：

```go
// 本文件实现任务列表的跨机汇总：GET /api/tasks?scope=all。
//
// 职责：
//   - 把本机任务与 mirror_tasks 里的远端快照合并成 §5.3 的信封
//   - 给每台机器留一行 MachineStatus（快照的 fetched_at 即数据新旧）
//
// 边界：
//   - **不现场扇出**：看板 2.5s 轮询打的是本机，快慢与远端可达性解耦。
//     远端部分取自镜像快照（§6.3），刷新由镜像的「事件即门铃 + 30s 慢对账」
//     负责——这是本设计里「看板不被远端抖动波及」的兑现处
//   - 不改无参数时的响应形状：裸数组 []TaskView，与 W2 契约逐字节一致
package agentd
```

`tasksAll(ctx)`：

- 本机任务：`ListTasks` + 盖 `ProjectID`（`Machine` 留空串）→ `[]proto.TaskView`；
- 远端：`ListMirrorTasks()`，每条 `Task.Machine = mt.Target`，`Watchers` 取 `s.hub.Watchers(id)`（本机确实可能有人在看镜像任务）；
- `Machines`：本机一行（`Ok: true`, `FetchedAt: now`）+ 每台 target 一行，`Ok` 与 `FetchedAt` 取该机**最新的一条快照**的 `fetched_at`；该机一条快照都没有 → `Ok: false`、`Error: "尚无该机器的镜像快照（可能从未可达，或它上面没有任务）"`。
  - **注意这条报文的诚实性**：「没有快照」不等于「不可达」，报文必须把两种可能都说出来，不能武断地写成「不可达」。
- 完成后 Info 一行：`"任务汇总完成", "local", n, "mirrored", m, "machines", k`。

`handleListTasks` 开头加分流：

```go
	if r.URL.Query().Get("scope") == "all" && !isForwarded(r) {
		writeJSON(w, http.StatusOK, s.tasksAll(r.Context()))
		return
	}
```

`?project=` 过滤在盖注解之后、写响应之前做：

```go
	if pid := r.URL.Query().Get("project"); pid != "" {
		filtered := views[:0]
		for _, v := range views {
			if v.ProjectID == pid {
				filtered = append(filtered, v)
			}
		}
		views = filtered
		// 过滤后可能为空：空数组是正确答案，不是 404
		if views == nil {
			views = []proto.TaskView{}
		}
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agentd/ -run TestTasks -v && go test ./internal/agentd/`
Expected: PASS，既有任务列表测试不变红。

- [ ] **Step 5: 加关键节点日志**

- 汇总完成：Info（local / mirrored / machines 三个计数）；
- `?project=` 过滤：在既有的「任务列表完成」那行加 `"project", pid, "filtered", len(views)`（pid 为空时不影响可读性）。

- [ ] **Step 6: 加注释**

确认：文件头「不现场扇出」的 why 与「不改裸数组形状」的边界；「没有快照 ≠ 不可达」的报文注释；过滤后空数组是正确答案的注释。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/tasksfanout.go internal/agentd/tasksfanout_test.go internal/agentd/server.go
git commit -m "feat(agentd): 任务列表 ?project= 过滤与 ?scope=all 汇总（远端读镜像快照，不现场扇出）"
```

---

## Task 13: CLI 三件（验收面）

**Files:**
- Modify: `cmd/project.go`（`ls --tree/--all/--json`）
- Create: `cmd/machines.go`
- Modify: `cmd/tasks.go`（`--all`）
- Modify: `internal/client/client.go`（加 `Machines`、`ProjectTreeAll`、`ListTasksAll`）
- Test: `cmd/machines_test.go`、`cmd/project_tree_test.go`

**Interfaces:**
- Produces: `func (c *Client) Machines(ctx) (*proto.MachinesResp, error)`；`func (c *Client) ProjectTreeAll(ctx) (*proto.ProjectTreeResp, error)`；`func (c *Client) ListTasksAll(ctx) (*proto.TasksResp, error)`

- [ ] **Step 1: 给 client 加三个方法**

照 Task 6 里 `ProjectTree` 的同一套写法（`do` → 非 2xx `httpError` → `Decode`）：
- `Machines`：`GET /api/machines`；
- `ProjectTreeAll`：`GET /api/projects/tree?scope=all`；
- `ListTasksAll`：`GET /api/tasks?scope=all`。

三者都**不要**带 `MarkForwarded`——那是 agentd 之间的标记，CLI 用了会让本机拒绝扇出。

- [ ] **Step 2: 写失败的 CLI 测试**

新建 `cmd/machines_test.go`：照 `cmd/` 下既有命令测试的风格（若没有，就用 `cobra` 的 `SetOut`/`SetArgs` + 一个 `httptest` 假 agentd + `--agentd` 指向它）断言：

1. `handoff machines` 输出表头 `名字 地址 状态 版本 活跃 延迟 缺省执行者`，本机那行名字显示「本机」而不是空白；
2. `handoff machines --json` 输出可解析为 `proto.MachinesResp` 的单行 JSON；
3. 不可达的机器那行**必须带原因**（表格里有 error 列或在名字后缀里显示），不能只显示「不可达」三个字。

新建 `cmd/project_tree_test.go`：断言 `handoff project ls --tree` 的缩进三层输出，以及**不带 `--tree` 时输出与 B62 逐字节一致**（这条最重要：拿一份固定的 `ProjectList` 响应，比对输出字符串）。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./cmd/ -run 'Machines|ProjectTree' -v`
Expected: FAIL。

- [ ] **Step 4: 写实现**

`cmd/machines.go`（新文件，文件头注释写职责与边界）：

```go
// machinesCmd 列出本机视角的全部机器与探活结果。
var machinesCmd = &cobra.Command{
	Use:   "machines",
	Short: "列出机器与探活结果（本机 + 配置里的 targets）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		resp, err := client.New(addr, token).Machines(cmd.Context())
		if err != nil {
			return err
		}
		if machinesJSON {
			b, err := json.Marshal(resp)
			if err != nil {
				return fmt.Errorf("序列化机器列表: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "名字\t地址\t状态\t版本\t活跃\t延迟\t缺省执行者")
		for _, m := range resp.Machines {
			name := m.Name
			if name == "" {
				name = "本机" // 空串是线格式，人看的是「本机」
			}
			state := "可达"
			if !m.Reachable {
				// 不可达必须带原因：一句干巴巴的「不可达」等于让人去猜
				state = "不可达（" + firstLineOf(m.Error) + "）"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%dms\t%s\n",
				name, m.Addr, state, m.Version, m.ActiveTasks, m.ProbeMs, m.DefaultExecutor)
		}
		return tw.Flush()
	},
}
```

`cmd/project.go` 的 `ls`：加三个 flag（`--tree`、`--all`、`--json`），并在 `RunE` 开头分流。**不带 `--tree` 的路径一个字符都不许动**（B62 的测试与文档都按那个形态写的）。树形输出：

```
handoff  (a1b2c3d4e5f60718)  git@github.com:x/handoff.git
  本机  /Users/dev/handoff
    * main      482aab1  /Users/dev/handoff
      w1        9e12a3b  ~/.handoff/worktrees/w1   [任务工作树]
  devbox  /home/dev/handoff        ← 探测失败：路径不存在
```

（`*` 标主工作树；`[任务工作树]` 标 `managed`；探测失败的 location 仍然打印，后缀 `← 探测失败：<原文>`。）`--all` 时额外在末尾打一段机器应答情况，**没答上来的机器必须逐台列出带原因**。

`cmd/tasks.go` 的 `--all`：走 `ListTasksAll`，仍是**每行一个任务 JSON**（保持既有输出契约，脚本按行解析），并在 stderr 打一行机器应答摘要——没答上来的机器逐台列出。

> 为什么摘要走 stderr：stdout 是给脚本按行解析的任务 JSON 流，往里掺人话会破坏既有消费方。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./cmd/ -v`
Expected: PASS，既有 cmd 测试不变红。

- [ ] **Step 6: 加关键节点日志**

CLI 层的「日志」就是给人看的输出，纪律是：**任何一台机器没答上来都必须在输出里逐台可见**（表格里的状态列 / `--all` 的机器段 / tasks 的 stderr 摘要）。自查这三处都做到了。另外确认没有任何一条输出打印了 token。

- [ ] **Step 7: 加注释**

`cmd/machines.go` 文件头（职责 + 边界：只读投影，不做机器配置写操作）；`ls` 的 `--tree` 分支注明「不带 --tree 的输出是 B62 的契约，不许改」；`--all` 段注明「缺席必须可见」。

- [ ] **Step 8: Commit**

```bash
git add cmd/machines.go cmd/project.go cmd/tasks.go cmd/machines_test.go cmd/project_tree_test.go internal/client/client.go
git commit -m "feat(cli): project ls --tree/--all、machines、tasks --all（W3a 的验收面）"
```

---

## Task 14: 收口自检

**Files:** 无新增（只做检查与必要的补漏）

- [ ] **Step 1: 全量测试与格式**

```bash
go build ./... && go test ./... && gofmt -l internal/ cmd/
```

Expected: build 成功、测试全绿、`gofmt -l` **只可能**列出你没碰过的既有文件（当前分支上 main 带来的 6 个：`internal/agentd/integration_test.go`、`internal/agentd/projectresolve.go`、`internal/agentd/server.go`、`internal/projectid/projectid.go`、`cmd/dispatch.go`、`cmd/project.go`——**它们是 B62 遗留的注释缩进小疵，不属本期范围，别顺手格式化**，那会把无关改动混进任务分支）。你新建的文件一个都不该出现在列表里；若出现，`gofmt -w` 它。

- [ ] **Step 2: 竞态检查**

```bash
go test ./internal/agentd/ -race
```

Expected: PASS。镜像的 `subs` map、探活的结果切片、转发的并发都在这条命令的覆盖面上。

- [ ] **Step 3: instrumenting-code 清单逐项自查**

对本期**所有新增/修改**的函数过一遍，任一未打勾就回去补：

- [ ] 每个错误分支都 log 了，且带 context + cause（不是干巴巴的 `"failed"`）
- [ ] 每个外部调用（git、跨机 HTTP、上游 WS）前后都有日志，失败必 Error/Warn 带原文
- [ ] 成功路径有结论日志（探测完成 / 汇总完成 / 转发完成 / 发现完成），**没有静默的 happy path**
- [ ] 高频路径（工作区探测成功、快照刷新成功、单任务镜像细节）降到 Debug，不刷屏
- [ ] 没有任何 `fmt.Printf` / `println` 充当日志
- [ ] **没有任何一条日志打印了 token / cookie / ticket 明文**（`grep -rn "token" internal/agentd/*.go | grep -i "log\."` 自查一遍）
- [ ] 每个新建文件有文件头注释（职责 + 边界）
- [ ] 每个导出函数有 doc comment；非显然分支有「为什么」注释

- [ ] **Step 4: 契约面终检**

```bash
git diff --stat main -- internal/proto/ web/
```

Expected: **空**。本计划一行都不该动 `internal/proto/` 与 `web/`；有输出就说明越界了，回退那部分改动并上报。

- [ ] **Step 5: 真机验收（spec §11 的完成判据）**

在「本机 + devbox」两台上各跑一遍，把输出贴进最终报告：

```bash
handoff project ls --tree
handoff project ls --tree --all
handoff machines
handoff tasks --all
```

自查四条：树是三层且工作树真实；`--all` 里两台机器的 location 都在、machine 互不相同；`machines` 里不可达的机器带原因；`tasks --all` 的远端任务带 machine 名。**任何一条不符，如实报告，不要粉饰**——若因环境（devbox 不可达、无远程机器）跑不成，明确说出哪几条没验、为什么。

- [ ] **Step 6: Commit（若前面几步有补漏改动）**

```bash
git add -A
git commit -m "chore(w3a): 收口自检——日志/注释清单补齐"
```

---

## 自评（写完计划后对着 spec 走的一遍）

**Spec 覆盖**：§1.2 project_id（Task 2/6 一律调 `internal/projectid`，不另写）、§1.3 join（Task 2）、§2 工作区探测（Task 1）、§3 项目树 + tasks 查询参数（Task 3、12）、§4 机器投影（Task 4）、§5.1 透明路由（Task 10）、§5.1.1 显式机器路由（Task 5）、§5.2 防环与预算（Task 5、6）、§5.3 汇总形状（Task 6、12）、§6.1 发现式订阅（Task 9）、§6.2 两表（Task 7）、§6.3 快照与慢对账（Task 9、12）、§6.4 `/ws/events`（Task 11）、§7 CLI ×3（Task 13）、§8 日志纪律（每任务的日志步骤 + Task 14 清单）、§9 测试清单（分散在各任务的测试步骤，Task 14 汇总）。

**刻意不做**（spec 明说或推论）：`DELETE /api/mirror/{task_id}` 手动清理端点——§6.2 说「提供即可，不做自动过期」，但它不在 §7 的 CLI 交付物里，也不在 §11 的完成判据里；store 层的 `DeleteMirrorTask` 已备好，端点留给真有清理需求时再开（YAGNI）。若评审认为必须有，那是一个 5 行的 handler + 一条路由，单开一个任务补。

**两处需要执行者注意的已知不确定**：

1. `proto.StatusResp` 的字段名（Task 4 的 `fillFromStatus`）以 `internal/proto/status.go` 实际定义为准，别照抄本计划的猜测；
2. `internal/store` 与 `internal/agentd` 的测试辅助函数名（`openTestStore` / `env.getJSON` / `AppendEvent` 签名）以各自 `_test.go` 里既有的为准，缺了就照既有风格内联一个，**不要改动公共辅助的签名**——那会波及一大片既有测试。
