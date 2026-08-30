# B298 终态缓存收口删除 + `handoff gc` 实施计划

状态：执行计划
卡：B298
定级：L3 轻档
路由：plan → implement
法定产出：`docs/superpowers/plans/b298-plan.md`
事实台账：`docs/superpowers/ledgers/2026-08-30-b298-plan-ledger.md`

**已由 plan docs/superpowers/plans/b298-plan.md 吸收（2026-08-30）**（契约 §6 移交 plan 附区；C-4：吸收标注落本 plan 文档头，不落 implement）

拍板结果引用：breakdown 岔口 1–3 已拍板——

1. `GCResp.scanned` = 本轮判定读过的任务表终态任务行数（含无缓存叶子、无残树的行；对齐 `ReclaimListResp.scanned` 注释语义，**不是** `ReclaimList` 实现里「managed+WorkDir」过滤计数）。必须用测试钉死。
2. 契约期四包全红是沙箱/flake，不是本分支。T4 无沙箱跑 `go test ./internal/proto ./internal/client ./internal/agentd ./cmd`；若红，先重跑一次再归因本卡。
3. C-2 追认：工作树清理失败不豁免缓存叶子删除。`compensateWorkspace` 的提前 `return` 不得截走缓存删除。

上游：已批准 spec `docs/superpowers/specs/b298.md`；已冻结契约 `docs/superpowers/specs/b298-contract.md`（提交 `26e2ab7f`，72 条）；已拍板拆解 `docs/superpowers/specs/b298-breakdown.md`。

读者：零上下文、品味存疑。按步骤复制代码，不要发明第二条占用规则、不要 `RemoveAll(DataDir/tmp)`、不要把缓存塞进 reclaim、不要改 `TaskTmpDir` 路径形状、不要改 `web/`、不要另开卡。

本卡要锁的行为今天从声明缝调用**还得不到**：`Manager.GC` 返回 `ErrGCUnwired`（503），`handleGC` 成功路径不写 JSON，`runGC` 成功后恒退 0，三收口不删缓存。Ticket 0 的 503 **不是**活路径。DAG 第一条必须点亮 helper + 收口删除。

---

## 0. 硬边界（写进步骤，不是散文）

- 叶子只有两条：`executor.TaskTmpDir(DataDir, id)` 与 `filepath.Join(DataDir, "tasks", id, "tmp")`。禁止 `RemoveAll` 等值 `filepath.Join(DataDir, "tmp")` 的路径。
- 短号占用：同一 `ListTasks` 快照里，**其他**非终态任务的 id 前 8 字节相同 → 现役叶子本轮不删。正在进入终态的自己不算占用者。禁止复用 `ActiveTasksByWorkDir`。
- 遗留完整-id 叶子不受短号规则。两条叶子都要做 tmp 根等值保护（`taskID==".."` 经 `Join`+`Clean` 也能拼出 tmp 根）。
- Done/Stop/compensate 删除失败：日志含 task/path/cause，不阻断归档/中止/补偿。
- `waiting_review` 不是终态（`proto.TaskState.IsTerminal`）。Done 会先把它迁成 completed 再删自己的叶子；GC 扫描不含它。
- 预览 = `GET /api/gc`（`?force=true` 若 force）；执行 = `POST /api/gc` body `proto.GCRequest{Force}`。走与 reclaim 相同的 `s.auth` 门。
- 批处理只扫任务表终态行；路径去重；`releasable_bytes` 用 `*int64`（缺席 vs 0）；skip dirty/unknown/not-managed 不中止；`RemoveAll` 失败进人读+JSON；`Failures` 只计本应删却失败；skip 不计。
- 执行重读快照，不复用预览快照。agentd 内部调 `Reclaim`/`ReclaimList`；CLI 禁止逐任务 POST reclaim。
- `--force` 只作用于脏 managed 树；缓存删除不读 force；`--force` 无 `--yes` 仍预览。
- 老 agentd 双 404 → 「过旧」退出 0（已锁，保持绿）。
- `os.RemoveAll` 对缺失返回 nil = 成功/幂等，不是 failed 行。
- 字节：只加普通文件；`filepath.WalkDir`（不跟随 dir symlink）；`d.Info()` 不跟 symlink。
- 无前端。`git diff 26e2ab7f --name-only -- web/` 必须空。
- 不改 `internal/proto/gc.go` 字段（零改动原则；scanned 语义测试放 agentd）。
- 有界文件集之外只许改本 plan 与本台账。

---

## 1. 基线证据、图覆盖与执行边界

### 1.1 本节点已实跑的基线

工作树 HEAD `c2524c8c`，未改实现。实现者每个 task 动手前重跑该 task 的最小命令，预期沿用下表。

| 范围 | 命令 | 实跑结果 |
|---|---|---|
| wire 金样本 | `go test ./internal/proto -run 'TestGCGoldenJSON' -count=1` | 退出 0；`ok github.com/Xsxdot/handoff/internal/proto 0.685s` |
| client 双 404 | `go test ./internal/client -run 'TestGCPostDouble404IsUnsupported' -count=1` | 退出 0；`ok .../internal/client 0.519s` |
| CLI 过旧/字节文案 | `go test ./cmd -run 'TestRunGCDegradesOnOldAgentd\|TestRenderGCDistinguishesUnknownBytes' -count=1` | 退出 0；`ok .../cmd 0.572s` |
| Ticket 0 + 收口/reclaim harness | `go test ./internal/agentd -run 'TestHandleGCTicket0\|TestDoneRemovesManagedWorktree\|TestCompensateKeepsBranchWhenWorktreeRemoveFails\|TestDoneWorktreeRemoveFailureDoesNotBlockArchive\|TestReclaimRemovesCleanWorktree\|TestReclaimRefusesDirtyWithoutForce\|TestReclaimListShowsResidueOnly' -count=1` | 退出 0；`ok .../internal/agentd 4.897s` |
| TaskTmpDir 形状 | `go test ./internal/executor -run 'TestTaskTmpDirGoldenVectors' -count=1` | 退出 0；`ok .../internal/executor 0.414s` |
| 四包无沙箱 | `go test ./internal/proto ./internal/client ./internal/agentd ./cmd -count=1` | 退出 0；`ok proto 0.325s` / `client 9.120s` / `agentd 85.359s` / `cmd 5.728s` |

`TestHandleGCTicket0` 今日绿是因为 503 空壳。T2 必须删掉它，换成 200+JSON；留下 = 假绿。

### 1.2 图与源码核对

`codegraph` 不在 PATH。本节点用 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --view cards-B298-charter`。

- `sym 'internal/agentd/manager.go#Manager.Done'` **失败**：`符号 ... 不在图中`。`file#Symbol` 查询形被拒，记图覆盖债。短名 `sym 'Manager.Done'` 命中 `n_agentd_Manager_Done` 行 1387，`anchor=moved`。
- `flow 'Manager.GC'`：`degraded=true`，`missing=基线没有 flows 段`，`steps=[]`。禁止用 `chain` 冒充。源码：直接 `return nil, ErrGCUnwired`。
- `who-calls 'Manager.GC'`：仅 `Server.handleGC`。`chain 'Manager.GC'` 无出边。
- `who-calls 'Manager.Done'`：`handleDone` / `RemoveManagedWorktree`；`compensateWorkspace` 图行 1051 vs 源码 1090（保鲜债）。

实现者复核用的符号锚（`resolve --doc` 认这种写法）：`internal/executor/tempdir.go#TaskTmpDir`、`internal/agentd/manager.go#Manager.Done`、`internal/agentd/manager.go#Manager.Stop`、`internal/agentd/manager.go#Manager.compensateWorkspace`、`internal/agentd/gc.go#Manager.GC`、`internal/agentd/gc.go#Server.handleGC`、`internal/agentd/reclaim.go#Manager.Reclaim`、`internal/agentd/reclaim.go#Manager.ReclaimList`、`internal/agentd/server.go#writeJSON`、`internal/store/store.go#ListTasks`、`internal/proto/proto.go#IsTerminal`、`internal/client/gc.go#Client.GCPreview`、`internal/client/gc.go#Client.GC`、`cmd/gc.go#runGC`、`cmd/gc.go#renderGC`、`cmd/root.go#newTargetClient`。

库行为（本机 `/usr/local/go`，go1.26.1，禁止凭记忆）：

1. `filepath.Join` 忽略空元素并 Clean：`src/path/filepath/path.go:123-132` → `TaskTmpDir(dataDir,"")` = `<DataDir>/tmp`。
2. `filepath.WalkDir` 含 root、字典序、不跟随 symlink、根用 `Lstat`：`path.go:381-407`。
3. `os.RemoveAll` 空路径 / 目标不存在 → nil：`src/os/removeall_at.go:15-32`。
4. `RemoveAll` 递归并返回首错：`removeall_at.go:66-160`。
5. `openDirAt` 把非目录（含指向目录的 symlink）当错误，不进入目标：`removeall_at.go:163-177`。
6. `fs.DirEntry.Info`：可能 `ErrNotExist`；symlink 描述链接本身：`src/io/fs/fs.go:116-122`。
7. `FileInfo.Size` 对普通文件是字节长度：`fs.go:167-175`。
8. `os.Stat` 跟随、`os.Lstat` 不跟随：`src/os/stat.go:9-27`。存在性与遍历都用 Lstat/WalkDir/Info，禁止 `os.Stat` 再声称没跟随。

### 1.3 任务 DAG 与精确接口

```
helper（cachegc.go：短号占用 + tmp 根保护 + 两叶子）
        │
        ├─→ T1 三收口接线（Done / Stop / compensateWorkspace）
        │
        └─→ T2 Manager.GC + handleGC 写 200 JSON
                    │
                    └─→ T3 CLI 渲染 / 退出码
                                │
                                └─→ T4 缝合与负例（含协调者门禁）
```

单卡单上下文。先落 helper，再 T1，再 T2。T3 依赖真实 `GCResp`。T4 收口。不并行改同一 helper。

#### 跨 task 冻结接口（逐字，邻居只看得见这个）

**Produces（helper / T1，T2 Consumes 必须逐字符相同）：**

```go
func cacheActiveLeaf(dataDir, taskID string) string
func cacheLegacyLeaf(dataDir, taskID string) string
func cacheTmpRoot(dataDir string) string
func cachePathEqual(a, b string) bool
func cacheID8(taskID string) string
func isCacheTmpRoot(dataDir, path string) bool
func activeLeafOccupied(tasks []proto.Task, selfID string) bool
func sumRegularFileBytes(root string) (int64, error)

type cacheLeafPlan struct {
	TaskID string
	Path   string
	Kind   string // "active" | "legacy"
	Skip   bool
	Note   string
}

func planTaskCacheLeaves(dataDir, taskID string, tasks []proto.Task) []cacheLeafPlan
func (m *Manager) removeCacheLeaf(path string) error
func (m *Manager) purgeTaskCache(taskID string)
```

Manager 新增测试缝（生产恒 nil，与 `writeTaskFile` 同款）：

```go
removeCacheLeafFn func(path string) error
```

**Consumes（helper / T1）：**

```go
func TaskTmpDir(dataDir, taskID string) string                         // internal/executor/tempdir.go
func (s TaskState) IsTerminal() bool                                  // internal/proto/proto.go
func (s *Store) ListTasks() ([]proto.Task, error)                     // internal/store/store.go
func (m *Manager) Done(ctx context.Context, taskID, note string) error
func (m *Manager) Stop(ctx context.Context, taskID string) (worktreeRemoved bool, err error)
func (m *Manager) compensateWorkspace(ctx context.Context, taskID string, repo string, ws Workspace)
```

**Produces（T2）——签名已冻结，禁止改：**

```go
func (m *Manager) GC(ctx context.Context, force, execute bool) (resp *proto.GCResp, err error)
func (s *Server) handleGC(w http.ResponseWriter, r *http.Request)
func (m *Manager) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)
func (m *Manager) ReclaimList() (*proto.ReclaimListResp, error)
func writeJSON(w http.ResponseWriter, status int, v any)
```

删除：`var ErrGCUnwired`、`func jsonDecode`、`TestHandleGCTicket0`。

**Produces（T3）——签名已冻结，禁止改：**

```go
var gcCmd = &cobra.Command{Use: "gc"}
func runGC(cmd *cobra.Command, cl *client.Client, addr string) error
func renderGC(w io.Writer, resp *proto.GCResp)
func newTargetClient() (*client.Client, func(), error)
func (c *Client) GCPreview(ctx context.Context, force bool) (*proto.GCResp, error)
func (c *Client) GC(ctx context.Context, force bool) (*proto.GCResp, error)
```

**Consumes（T4）：** 上列全部 + `TestGCGoldenJSON` + `TestGCPostDouble404IsUnsupported` + `TestRunGCDegradesOnOldAgentd`。

---

## 2. Task 1：helper + 缝 1 三收口删缓存

契约：42–58；记录 B/E；C-2；§6 附区 1–3。

### 2.1 文件集（不得越界）

- 新建 `internal/agentd/cachegc.go`
- 新建 `internal/agentd/cachegc_test.go`
- 改 `internal/agentd/manager.go`：结构体加 `removeCacheLeafFn`；`Done` 在工作树块之后、`return nil` 之前调 `purgeTaskCache`；`Stop` 同位置；`compensateWorkspace` 在空 WorkDir 守卫之后 `defer purgeTaskCache`（唯一合法接法，见步骤 6）

只读：`internal/executor/tempdir.go`、`internal/store/store.go#ListTasks`、`internal/proto/proto.go#IsTerminal`、`internal/agentd/reclaim.go`（不改）。

### 2.2 判据先在基线跑（本 task 最小范围）

```bash
go test ./internal/agentd -run 'TestHandleGCTicket0|TestDoneRemovesManagedWorktree|TestCompensateKeepsBranchWhenWorktreeRemoveFails|TestDoneWorktreeRemoveFailureDoesNotBlockArchive' -count=1
go test ./internal/executor -run 'TestTaskTmpDirGoldenVectors' -count=1
```

预期：沿用 §1.1 退出 0。本 task **只**跑 `./internal/agentd -run 'TestCache|TestDonePurge|TestStopPurge|TestCompensatePurge|TestDoneRemovesManagedWorktree|TestCompensateKeepsBranchWhenWorktreeRemoveFails|TestDoneWorktreeRemoveFailureDoesNotBlockArchive'` 与 executor 金样本。不跑四包。

### 2.3 步骤 1 — 红：helper 纯函数（锁根保护 / 占用 / 两叶子；2–5 分钟）

在 `cachegc_test.go` 写入下列完整测试。实现前必红（未定义符号）。不要删断言求绿。

**内部锁声明（占位符扫描已重复）：** `TestCacheID8AndLeaves` / `TestCacheTmpRootGuard` / `TestActiveLeafOccupied` 的入口是 helper 符号，不在 spec 缝 1/2 上。合法理由：从 `Done`/`Stop`/`GC` 构造不出空 taskID（`GetTask("")` 先失败；store 不会列出空 ID），根保护必须直喂 helper。占用与两叶子形状另外用缝级测试从 `Done`/`GC` 再锁一次；这三支是附加，不能顶替缝级测试。

```go
package agentd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestCacheID8AndLeaves(t *testing.T) {
	data := "/data"
	id := "137a7dc9-df89-4c1c-891e-ebe106c68b37"
	if got, want := cacheID8(id), "137a7dc9"; got != want {
		t.Fatalf("cacheID8 = %q want %q", got, want)
	}
	if got, want := cacheID8("T1"), "T1"; got != want {
		t.Fatalf("short cacheID8 = %q want %q", got, want)
	}
	if got, want := cacheActiveLeaf(data, id), executor.TaskTmpDir(data, id); got != want {
		t.Fatalf("active = %q want %q", got, want)
	}
	if got, want := cacheLegacyLeaf(data, id), filepath.Join(data, "tasks", id, "tmp"); got != want {
		t.Fatalf("legacy = %q want %q", got, want)
	}
	if got, want := cacheTmpRoot(data), filepath.Join(data, "tmp"); got != want {
		t.Fatalf("root = %q want %q", got, want)
	}
}

func TestCacheTmpRootGuard(t *testing.T) {
	data := "/opt/handoff"
	if !isCacheTmpRoot(data, cacheActiveLeaf(data, "")) {
		t.Fatal("空 taskID 的活动叶子必须判为 tmp 根")
	}
	if !isCacheTmpRoot(data, filepath.Join(data, "tmp", ".")) {
		t.Fatal("Clean 后的 tmp/. 必须判为 tmp 根")
	}
	dotdot := cacheLegacyLeaf(data, "..")
	if !isCacheTmpRoot(data, dotdot) {
		t.Fatalf("taskID=.. 的遗留叶子 %q 必须判为 tmp 根", dotdot)
	}
	id := "abcd1234-xxxx"
	plans := planTaskCacheLeaves(data, "", nil)
	if len(plans) == 0 || !plans[0].Skip || plans[0].Note == "" {
		t.Fatalf("空 ID 必须 skip 并带原因，实得 %+v", plans)
	}
	for _, p := range planTaskCacheLeaves(data, id, nil) {
		if isCacheTmpRoot(data, p.Path) && !p.Skip {
			t.Fatalf("根路径不得进入可删计划：%+v", p)
		}
	}
}

func TestActiveLeafOccupied(t *testing.T) {
	self := "deadbeef-0000-4000-8000-aaaaaaaaaaaa"
	otherRun := proto.Task{ID: "deadbeef-0000-4000-8000-bbbbbbbbbbbb", State: proto.TaskStateRunning}
	otherDone := proto.Task{ID: "deadbeef-0000-4000-8000-cccccccccccc", State: proto.TaskStateCompleted}
	otherReview := proto.Task{ID: "deadbeef-0000-4000-8000-dddddddddddd", State: proto.TaskStateWaitingReview}
	unrelated := proto.Task{ID: "cafebabe-0000-4000-8000-eeeeeeeeeeee", State: proto.TaskStateRunning}
	selfRow := proto.Task{ID: self, State: proto.TaskStateCompleted}

	if activeLeafOccupied([]proto.Task{selfRow, otherDone, unrelated}, self) {
		t.Fatal("终态同号与无关短号不得占用")
	}
	if !activeLeafOccupied([]proto.Task{selfRow, otherRun}, self) {
		t.Fatal("其他 running 同 id8 必须占用")
	}
	if !activeLeafOccupied([]proto.Task{selfRow, otherReview}, self) {
		t.Fatal("其他 waiting_review 同 id8 必须占用（非终态）")
	}
	if activeLeafOccupied([]proto.Task{selfRow}, self) {
		t.Fatal("自己不得算占用者")
	}
}

func TestSumRegularFileBytesIgnoresDirSymlinkAndNonRegular(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "linkfile")); err != nil {
		t.Fatal(err)
	}
	n, err := sumRegularFileBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("只计普通文件 a.txt 的 4 字节，不得跟随 symlink，实得 %d", n)
	}
	missing, err := sumRegularFileBytes(filepath.Join(root, "nope"))
	if err != nil || missing != 0 {
		t.Fatalf("缺失目录应 0,nil，实得 %d %v", missing, err)
	}
}
```

跑：`go test ./internal/agentd -run 'TestCacheID8AndLeaves|TestCacheTmpRootGuard|TestActiveLeafOccupied|TestSumRegularFileBytesIgnoresDirSymlinkAndNonRegular' -count=1`
预期红：未定义 `cacheID8` 等。

### 2.4 步骤 2 — 绿：最小 helper 实现

新建 `internal/agentd/cachegc.go`，完整文件如下（含头注释；导出/包级符号按步骤 8 再核一遍，不得删头）：

```go
// cachegc.go —— 任务私有缓存叶子的路径、短号占用、tmp 根保护与收口删除。
//
// 职责：
//   - 计算现役叶子 TaskTmpDir(DataDir,id) 与遗留叶子 DataDir/tasks/<完整id>/tmp
//   - 用同一份 ListTasks 快照判定短号占用（不含自己；仅非终态占用）
//   - 拒绝任何等值 DataDir/tmp 根的删除目标
//   - 给 Done/Stop/compensateWorkspace/Manager.GC 提供同一套计划与删除动作
//
// 边界：
//   - 不改任务状态、不删任务目录/分支/render.log/frames.jsonl/proc.json
//   - 不扫描无任务行的孤儿目录，不清空 tmp 根
//   - 不复用 ActiveTasksByWorkDir（那是 workdir 占用，不是短号占用）
package agentd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

func cacheActiveLeaf(dataDir, taskID string) string {
	return executor.TaskTmpDir(dataDir, taskID)
}

func cacheLegacyLeaf(dataDir, taskID string) string {
	return filepath.Join(dataDir, "tasks", taskID, "tmp")
}

func cacheTmpRoot(dataDir string) string {
	return filepath.Join(dataDir, "tmp")
}

func cachePathEqual(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func cacheID8(taskID string) string {
	if len(taskID) > 8 {
		return taskID[:8]
	}
	return taskID
}

func isCacheTmpRoot(dataDir, path string) bool {
	return cachePathEqual(path, cacheTmpRoot(dataDir))
}

// activeLeafOccupied 报告同一 id8 上是否存在「自己以外」的非终态任务。
// 自己即使仍是 waiting_review，也不算占用者——收口时它正在进入终态。
func activeLeafOccupied(tasks []proto.Task, selfID string) bool {
	self8 := cacheID8(selfID)
	if self8 == "" {
		return false
	}
	for _, t := range tasks {
		if t.ID == selfID {
			continue
		}
		if t.State.IsTerminal() {
			continue
		}
		if cacheID8(t.ID) == self8 {
			return true
		}
	}
	return false
}

type cacheLeafPlan struct {
	TaskID string
	Path   string
	Kind   string
	Skip   bool
	Note   string
}

func planTaskCacheLeaves(dataDir, taskID string, tasks []proto.Task) []cacheLeafPlan {
	plan := func(path, kind string, skip bool, note string) cacheLeafPlan {
		return cacheLeafPlan{TaskID: taskID, Path: path, Kind: kind, Skip: skip, Note: note}
	}
	var out []cacheLeafPlan
	active := cacheActiveLeaf(dataDir, taskID)
	switch {
	case isCacheTmpRoot(dataDir, active):
		out = append(out, plan(active, "active", true, "拒绝删除 DataDir/tmp 根"))
	case activeLeafOccupied(tasks, taskID):
		out = append(out, plan(active, "active", true, "短号被其他非终态任务占用"))
	default:
		out = append(out, plan(active, "active", false, ""))
	}
	legacy := cacheLegacyLeaf(dataDir, taskID)
	if isCacheTmpRoot(dataDir, legacy) {
		out = append(out, plan(legacy, "legacy", true, "拒绝删除 DataDir/tmp 根"))
	} else {
		out = append(out, plan(legacy, "legacy", false, ""))
	}
	return out
}

func sumRegularFileBytes(root string) (int64, error) {
	var sum int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			if errors.Is(ierr, fs.ErrNotExist) {
				return nil
			}
			return ierr
		}
		if info.Mode().IsRegular() {
			sum += info.Size()
		}
		return nil
	})
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return sum, err
}

func (m *Manager) removeCacheLeaf(path string) error {
	if m.removeCacheLeafFn != nil {
		return m.removeCacheLeafFn(path)
	}
	return os.RemoveAll(path)
}

// purgeTaskCache 尝试删除该任务的两处缓存叶子。失败只打日志。
func (m *Manager) purgeTaskCache(taskID string) {
	if m.log != nil {
		m.log.Info("缓存清理进入", "task", taskID)
	}
	if m.cfg == nil || m.st == nil {
		if m.log != nil {
			m.log.Error("缓存清理缺少 cfg 或 store", "task", taskID)
		}
		return
	}
	tasks, err := m.st.ListTasks()
	if err != nil {
		if m.log != nil {
			m.log.Error("缓存清理读任务表失败", "task", taskID, "cause", err)
		}
		return
	}
	for _, leaf := range planTaskCacheLeaves(m.cfg.DataDir, taskID, tasks) {
		if leaf.Skip {
			if m.log != nil {
				m.log.Info("缓存叶子已跳过", "task", taskID, "path", leaf.Path, "kind", leaf.Kind, "reason", leaf.Note)
			}
			continue
		}
		if isCacheTmpRoot(m.cfg.DataDir, leaf.Path) {
			if m.log != nil {
				m.log.Error("缓存叶子命中 tmp 根，拒绝删除", "task", taskID, "path", leaf.Path)
			}
			continue
		}
		if m.log != nil {
			m.log.Info("缓存叶子删除前", "task", taskID, "path", leaf.Path, "kind", leaf.Kind)
		}
		if err := m.removeCacheLeaf(leaf.Path); err != nil {
			if m.log != nil {
				m.log.Error("缓存叶子删除失败", "task", taskID, "path", leaf.Path, "kind", leaf.Kind, "cause", err)
			}
			continue
		}
		if m.log != nil {
			m.log.Info("缓存叶子已删除", "task", taskID, "path", leaf.Path, "kind", leaf.Kind)
		}
	}
	if m.log != nil {
		m.log.Info("缓存清理完成", "task", taskID)
	}
}
```

在 `internal/agentd/manager.go` 的 `Manager` 结构体里、`writeTaskFile` 字段之后插入：

```go
	// removeCacheLeafFn 是缓存叶子删除的测试缝（B298）。**生产路径恒为 nil**，
	// 由 removeCacheLeaf 退回 os.RemoveAll；非测试代码不得赋值。
	removeCacheLeafFn func(path string) error
```

跑步骤 1 的测试，预期绿。提交：

```bash
git add internal/agentd/cachegc.go internal/agentd/cachegc_test.go internal/agentd/manager.go
git commit -m "feat(B298): add cache-leaf helper with occupancy and tmp-root guard"
```

### 2.5 步骤 3 — 红：从声明缝 Done/Stop/compensate 锁删除（缝 1）

照抄 harness：`newTestManager` / `mustCreateTask` / `mustDone` 来自 `internal/agentd/manager_test.go`；`compensateOnlyManager` / `initTestRepo` 来自同文件与 `workspace_test.go`。不要复制那些函数，同包可直接调用。

把下列测试**追加**进 `cachegc_test.go`。

```go
func writeCacheLeaves(t *testing.T, dataDir, id string) (active, legacy, taskDir string) {
	t.Helper()
	active = executor.TaskTmpDir(dataDir, id)
	if err := os.MkdirAll(filepath.Join(active, "gocache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "gocache", "obj"), []byte("cache-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskDir = filepath.Join(dataDir, "tasks", id)
	legacy = filepath.Join(taskDir, "tmp")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "old"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"render.log", "frames.jsonl", "proc.json"} {
		if err := os.WriteFile(filepath.Join(taskDir, name), []byte(name+"-keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return active, legacy, taskDir
}

func seedTaskWithCache(t *testing.T, m *Manager, id string, state proto.TaskState) (active, legacy, taskDir string) {
	t.Helper()
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{
		ID: id, Target: "local", Executor: "fake",
		State: state, CreatedAt: now, UpdatedAt: now,
	})
	return writeCacheLeaves(t, m.cfg.DataDir, id)
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s 应已删除: %v", path, err)
	}
}

func assertKeptFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q want %q", path, b, want)
	}
}

func TestDonePurgesBothCacheLeavesAndKeepsTaskDir(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "11111111-0000-4000-8000-000000000001"
	active, legacy, taskDir := seedTaskWithCache(t, m, id, proto.TaskStateWaitingReview)
	mustDone(t, m, id, "")
	assertGone(t, active)
	assertGone(t, legacy)
	assertKeptFile(t, filepath.Join(taskDir, "render.log"), "render.log-keep")
	assertKeptFile(t, filepath.Join(taskDir, "frames.jsonl"), "frames.jsonl-keep")
	assertKeptFile(t, filepath.Join(taskDir, "proc.json"), "proc.json-keep")
	if _, err := os.Lstat(taskDir); err != nil {
		t.Fatalf("任务目录必须保留: %v", err)
	}
	cur, err := st.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateCompleted {
		t.Fatalf("state=%s want completed", cur.State)
	}
}

func TestStopPurgesBothCacheLeaves(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "22222222-0000-4000-8000-000000000002"
	active, legacy, taskDir := seedTaskWithCache(t, m, id, proto.TaskStateRunning)
	if _, err := m.Stop(context.Background(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertGone(t, active)
	assertGone(t, legacy)
	assertKeptFile(t, filepath.Join(taskDir, "render.log"), "render.log-keep")
	cur, _ := st.GetTask(id)
	if cur.State != proto.TaskStateFailed {
		t.Fatalf("state=%s want failed", cur.State)
	}
}

func TestDoneKeepsActiveLeafWhenOtherNonTerminalSharesID8(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	self := "deadbeef-0000-4000-8000-aaaaaaaaaaaa"
	other := "deadbeef-0000-4000-8000-bbbbbbbbbbbb"
	active, legacy, _ := seedTaskWithCache(t, m, self, proto.TaskStateWaitingReview)
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{
		ID: other, Target: "local", Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now,
	})
	mustDone(t, m, self, "")
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("被占用的现役叶子必须保留: %v", err)
	}
	assertGone(t, legacy)
}

func TestDoneLegacyLeafIgnoresShortIDOccupancy(t *testing.T) {
	// 与上一则共用形状：遗留叶子按完整 id，必须删。已在 TestDoneKeepsActiveLeafWhenOtherNonTerminalSharesID8 钉死。
	// 本则再钉：占用者不存在时两条都删（对照）。
	m, _, _, _ := newTestManager(t)
	id := "feedfeed-0000-4000-8000-000000000009"
	active, legacy, _ := seedTaskWithCache(t, m, id, proto.TaskStateWaitingReview)
	mustDone(t, m, id, "")
	assertGone(t, active)
	assertGone(t, legacy)
}

func TestDoneOnRunningDoesNotPurgeCache(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	id := "33333333-0000-4000-8000-000000000003"
	active, legacy, _ := seedTaskWithCache(t, m, id, proto.TaskStateRunning)
	if err := m.Done(context.Background(), id, ""); err == nil {
		t.Fatal("running 走 Done 必须失败")
	}
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("非终态不得删现役叶子: %v", err)
	}
	if _, err := os.Lstat(legacy); err != nil {
		t.Fatalf("非终态不得删遗留叶子: %v", err)
	}
}

func TestDonePurgeFailureDoesNotBlockArchive(t *testing.T) {
	var buf bytes.Buffer
	m, st, _, _ := newTestManager(t)
	m.log = slog.New(slog.NewTextHandler(&buf, nil))
	id := "44444444-0000-4000-8000-000000000004"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateWaitingReview)
	m.removeCacheLeafFn = func(path string) error {
		return errors.New("injected-remove-fail")
	}
	mustDone(t, m, id, "")
	cur, _ := st.GetTask(id)
	if cur.State != proto.TaskStateCompleted {
		t.Fatalf("删除失败不得阻断归档，state=%s", cur.State)
	}
	logs := buf.String()
	for _, want := range []string{id, active, "injected-remove-fail"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("失败日志缺少 %q，实得 %s", want, logs)
		}
	}
}

func TestCompensatePurgesCacheWhenWorktreeRemoveFails(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "e2e/stuck-cache")
	tip := gitOut(t, repo, "rev-parse", "refs/heads/e2e/stuck-cache")
	m := compensateOnlyManager(t)
	id := "2c58bbb7-0000-0000-0000-000000000000"
	active, legacy, taskDir := seedTaskWithCache(t, m, id, proto.TaskStateFailed)
	m.compensateWorkspace(context.Background(), id, repo, Workspace{
		Branch: "e2e/stuck-cache", WorkDir: filepath.Join(t.TempDir(), "not-a-worktree"),
		Managed: true, NewBranchTip: tip,
	})
	assertGone(t, active)
	assertGone(t, legacy)
	assertKeptFile(t, filepath.Join(taskDir, "render.log"), "render.log-keep")
	if !branchExists(t, repo, "e2e/stuck-cache") {
		t.Fatal("工作树删除失败时分支必须保留（回归 TestCompensateKeepsBranchWhenWorktreeRemoveFails）")
	}
}

func TestPurgeRefusesTmpRootEvenIfCalledDirectly(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	root := cacheTmpRoot(m.cfg.DataDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "keep-me")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.purgeTaskCache("")
	if _, err := os.Lstat(marker); err != nil {
		t.Fatalf("tmp 根内文件必须幸存: %v", err)
	}
}
```

跑：`go test ./internal/agentd -run 'TestDonePurge|TestStopPurge|TestDoneKeepsActiveLeaf|TestDoneLegacyLeaf|TestDoneOnRunning|TestDonePurgeFailure|TestCompensatePurge|TestPurgeRefusesTmpRoot' -count=1`

预期红：Done/Stop/compensate 尚未调 `purgeTaskCache`，叶子仍在。`TestCompensateKeepsBranchWhenWorktreeRemoveFails` 必须保持绿。

若 `TestDonePurgesBothCacheLeavesAndKeepsTaskDir` **意外先绿**，停止。退路：先确认是否误把 helper 测试当缝级；不得改成只调 `purgeTaskCache` 来让「Done 测试」变绿——那会把入口从缝 1 改成内部锁，未声明即失败。

### 2.6 步骤 4 — 绿：三处接线（含 C-2 defer）

`internal/agentd/manager.go#Manager.Done`：在现有工作树块（约 1459-1473）之后、`return nil` 之前插入，原 `return nil` 保留在最后：

```go
	// B298：工作树处置尝试之后再删任务私有缓存。失败只记日志，不阻断归档。
	// 重试入口是 gc，不是重发 done（已 completed 会短路）。
	m.purgeTaskCache(taskID)
	return nil
```

`Manager.Stop`：在工作树块（约 1565-1580）之后、`m.hub.Publish(evt)` 之前或之后均可，但必须在 `return worktreeRemoved, nil` 之前，且在工作树 `if` 块**外面**（工作树失败时这块 if 仍会走完，不会 return；不要把 purge 放进 `else`）。插在 `m.hub.Publish(evt)` 之前：

```go
	// B298：stop 工作树处置尝试之后删缓存；失败不阻断 stop。
	m.purgeTaskCache(taskID)
	m.hub.Publish(evt)
	return worktreeRemoved, nil
```

`Manager.compensateWorkspace`：**禁止**把 `purgeTaskCache` 只写在函数末尾。在 `if ws.WorkDir == "" { return }` 之后立即：

```go
	if ws.WorkDir == "" {
		return
	}
	// B298 C-2：工作树删除/切回失败会提前 return；用 defer 保证缓存删除仍被尝试。
	// 顺序仍是「先尝试工作树处置，再删缓存」（defer 在函数返回时运行）。
	defer m.purgeTaskCache(taskID)
	m.log.Warn("dispatch 后续失败，补偿复原工作区", "repo", repo, "workdir", ws.WorkDir,
```

不要改动其后 `RemoveManagedWorktree` / `deleteCreatedBranch` 逻辑。

跑步骤 3 测试 +
`go test ./internal/agentd -run 'TestDoneRemovesManagedWorktree|TestDoneWorktreeRemoveFailureDoesNotBlockArchive|TestCompensateKeepsBranchWhenWorktreeRemoveFails' -count=1`
预期全绿。

提交：

```bash
git add internal/agentd/manager.go internal/agentd/cachegc.go internal/agentd/cachegc_test.go
git commit -m "feat(B298): purge cache leaves on done, stop, and compensate"
```

### 2.7 步骤 5 — 日志

`purgeTaskCache` 已含：进入（task）、ListTasks 失败（task/cause）、跳过（task/path/kind/reason）、删除前、删除失败（task/path/cause）、删除成功、完成。禁止 `fmt.Printf`。用 `m.log`。成功路径不得静默。实现后用 `TestDonePurgeFailureDoesNotBlockArchive` 核对失败日志三件套。

### 2.8 步骤 6 — 注释

核对这些注释都在源码里，缺则补，不要另写空话：

- `cachegc.go` 文件头职责+边界（步骤 2 已给）。
- `activeLeafOccupied`：为什么自己不算占用者；为什么不用 `ActiveTasksByWorkDir`。
- `planTaskCacheLeaves`：为什么遗留叶子也做 tmp 根保护（`Join`+`Clean` 可把 `..` 拼成根）。
- `sumRegularFileBytes`：为什么 WalkDir+Info 而不是 Stat（不跟随 symlink；只计普通文件）。
- `Manager.removeCacheLeafFn`：生产 nil。
- `Done`/`Stop`/`compensateWorkspace` 插入点的 B298 / C-2 why。

### 2.9 T1 验收与缺陷族

1. waiting_review → Done：两叶子消失，render.log/frames.jsonl/proc.json/任务目录仍在，Done nil（42、55、56）。`TestDonePurgesBothCacheLeavesAndKeepsTaskDir`
2. Stop 同款（43）。`TestStopPurgesBothCacheLeaves`
3. compensate：点名 `compensateWorkspace`；工作树删除失败仍删缓存（44、C-2）。`TestCompensatePurgesCacheWhenWorktreeRemoveFails`
4. 空 ID / tmp 根拒绝（48）。`TestCacheTmpRootGuard` + `TestPurgeRefusesTmpRootEvenIfCalledDirectly`
5. 同 id8 其他非终态 → 现役保留、自己不算占用者（49、50）。`TestDoneKeepsActiveLeafWhenOtherNonTerminalSharesID8`
6. 遗留叶子不受短号影响（51）。同上 + `TestDoneLegacyLeafIgnoresShortIDOccupancy`
7. 注入删除失败 → 收口成功，日志含 task/path/cause（52–54）。`TestDonePurgeFailureDoesNotBlockArchive`
8. 非终态不进删除集合（57）。`TestDoneOnRunningDoesNotPurgeCache`

缺陷族：

- 族 1：收口中途崩溃 → 缓存可能残留；法定重试是 gc（58）。TOCTOU（ListTasks 与 RemoveAll 之间同 id8 新任务 Start）已识别并接受，测试不锁该窗口。无新状态机。
- 族 2：收口失败只进日志是 spec 明选；窗口「归档成功缓存仍在」由 gc 兜底。日志必须含 task/path/cause。RemoveAll nil 不得虚报「曾删除失败」。
- 族 3：路径全 `filepath`；失败走日志不 panic。Windows 文件锁进真机清单第 5 条。
- 族 4：夹具目录 ≠ 真机 gocache，真机清单 1。负例能红：根保护、占用保留、running 不删。
- 族 5：三收口 + 后续 gc 共用 helper；本 task 钉收口侧。

---

## 3. Task 2：`Manager.GC` + `handleGC` 写响应

契约：14–23、59–72；记录 A/C/D；Ticket 0 注记（退役 503）。

### 3.1 文件集

- 改 `internal/agentd/gc.go`（实现 `GC`、`handleGC` 成功写 JSON；删 `ErrGCUnwired` 与 `jsonDecode`）
- 改 `internal/agentd/gc_test.go`（替换 `TestHandleGCTicket0`）
- 只读复用 `internal/agentd/cachegc.go`、`internal/agentd/reclaim.go`、`internal/agentd/server.go`（路由已在 498-499，零改动）

### 3.2 判据先在基线跑

```bash
go test ./internal/agentd -run 'TestHandleGCTicket0|TestReclaimRemovesCleanWorktree|TestReclaimRefusesDirtyWithoutForce|TestReclaimListShowsResidueOnly' -count=1
```

预期：`TestHandleGCTicket0` 仍 503 绿。本 task 最小范围：`go test ./internal/agentd -run 'TestHandleGC|TestGC' -count=1`。不要跑 `./cmd`。

### 3.3 步骤 1 — 红：缝 2 资源断言（先删 503 测试）

**删除** `TestHandleGCTicket0` 全文。若留下，T2 假绿。

在 `gc_test.go` 写入完整测试（同包，可调用 T1 的 `writeCacheLeaves` / `seedTaskWithCache` / `assertGone`，以及 `newTestManager` / `newReclaimManager` / `newWorktree` / `seedTerminalTask` / `initGitRepo`。不要把 helper 再抄一份到本文件）。

占位符扫描例外：**不**适用。下列测试给完整代码。git 工作树夹具照抄 `internal/agentd/reclaim_test.go` 的 `newReclaimManager`/`newWorktree`/`seedTerminalTask`，不要重写 git init。

```go
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

func newGCServer(t *testing.T) (*Server, *Manager) {
	t.Helper()
	m, st, hub, _ := newTestManager(t)
	s := &Server{st: st, hub: hub, log: m.log, mgr: m}
	s.cfg.Store(&config.Config{Token: "test"})
	return s, m
}

func doGC(t *testing.T, s *Server, method, rawURL, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, rawURL, nil)
	} else {
		r = httptest.NewRequest(method, rawURL, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Host = "127.0.0.1:7777"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

func TestGCPreviewListsTerminalLeavesWithoutDeleting(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	term := "aaaaaaa1-0000-4000-8000-000000000001"
	live := "bbbbbbbb-0000-4000-8000-000000000002"
	active, legacy, _ := seedTaskWithCache(t, m, term, proto.TaskStateFailed)
	liveActive, _, _ := seedTaskWithCache(t, m, live, proto.TaskStateRunning)
	orphan := filepath.Join(m.cfg.DataDir, "tmp", "orphan99")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "x"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := m.GC(context.Background(), false, false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !resp.Preview || resp.Force {
		t.Fatalf("preview/force 标记错误：%+v", resp)
	}
	if resp.Scanned != 1 {
		t.Fatalf("scanned=%d want 1（只计终态行）", resp.Scanned)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes == 0 {
		t.Fatalf("应报告可释放字节，实得 %+v", resp.ReleasableBytes)
	}
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("预览不得删终态叶子: %v", err)
	}
	if _, err := os.Lstat(liveActive); err != nil {
		t.Fatalf("预览不得碰非终态: %v", err)
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatalf("孤儿目录不得扫删: %v", err)
	}
	foundTerm := false
	for _, row := range resp.CacheRows {
		if row.Path == liveActive {
			t.Fatal("非终态叶子不得出现在 cache_rows")
		}
		if row.Path == orphan {
			t.Fatal("孤儿路径不得入表")
		}
		if row.TaskID == term && row.Status != proto.GCItemPlanned {
			t.Fatalf("终态行应为 planned，实得 %+v", row)
		}
		if row.TaskID == term {
			foundTerm = true
		}
	}
	if !foundTerm {
		t.Fatalf("缺少终态 cache 行: %+v", resp.CacheRows)
	}
	_ = legacy
}

func TestGCScannedCountsAllTerminalRows(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{ID: "s1", State: proto.TaskStateCompleted, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	mustCreateTask(t, m.st, &proto.Task{ID: "s2", State: proto.TaskStateFailed, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	mustCreateTask(t, m.st, &proto.Task{ID: "s3", State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	mustCreateTask(t, m.st, &proto.Task{ID: "s4", State: proto.TaskStateWaitingReview, CreatedAt: now, UpdatedAt: now, Executor: "fake"})
	resp, err := m.GC(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Scanned != 2 {
		t.Fatalf("scanned=%d want 2（completed+failed，含无叶子行；waiting_review/running 不计）", resp.Scanned)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes != 0 {
		t.Fatalf("无叶子时应显式 0，实得 %+v", resp.ReleasableBytes)
	}
}

func TestGCDedupesSharedActiveLeafBytesAndDelete(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	a := "abcdabcd-0000-4000-8000-00000000000a"
	b := "abcdabcd-0000-4000-8000-00000000000b"
	active, _, _ := seedTaskWithCache(t, m, a, proto.TaskStateFailed)
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{ID: b, Target: "local", Executor: "fake", State: proto.TaskStateCompleted, CreatedAt: now, UpdatedAt: now})
	resp, err := m.GC(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, row := range resp.CacheRows {
		if row.Path == active {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("共用活动叶子应只报告一次，实得 %d 行 %+v", n, resp.CacheRows)
	}
	want, err := sumRegularFileBytes(active)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes < want {
		t.Fatalf("去重字节应含这一份叶子 %d，实得 %+v", want, resp.ReleasableBytes)
	}
	if _, err := m.GC(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	assertGone(t, active)
}

func TestGCExecuteRereadsSnapshot(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "re-read00-0000-4000-8000-000000000001"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateFailed)
	preview, err := m.GC(context.Background(), false, false)
	if err != nil || preview.Scanned != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateRunning); err != nil {
		t.Fatal(err)
	}
	execResp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if execResp.Preview {
		t.Fatal("execute 的 preview 必须 false")
	}
	if execResp.Scanned != 0 {
		t.Fatalf("变成 running 后 scanned=%d want 0", execResp.Scanned)
	}
	if _, err := os.Lstat(active); err != nil {
		t.Fatalf("执行必须重读快照，不得删已 running 的叶子: %v", err)
	}
}

func TestGCExecuteDeletesNewTerminal(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	id := "new-term0-0000-4000-8000-000000000001"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateRunning)
	preview, _ := m.GC(context.Background(), false, false)
	if preview.Scanned != 0 {
		t.Fatalf("running 预览 scanned=%d", preview.Scanned)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GC(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	assertGone(t, active)
}

func TestGCForcePreviewDoesNotDeleteDirtyTree(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-gc-dirty", "f-gc-dirty")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-gc-dirty", proto.TaskStateFailed, true)
	resp, err := m.GC(context.Background(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Force || !resp.Preview {
		t.Fatalf("force 预览标记错误 %+v", resp)
	}
	if _, err := os.Lstat(wt); err != nil {
		t.Fatalf("force 预览不得删脏树: %v", err)
	}
	_ = id
}

func TestGCExecuteSkipsDirtyWithoutForceAndContinues(t *testing.T) {
	m, repo := newReclaimManager(t)
	dirtyWT := newWorktree(t, repo, "wt-gc-d", "f-gc-d")
	if err := os.WriteFile(filepath.Join(dirtyWT, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanWT := newWorktree(t, repo, "wt-gc-c", "f-gc-c")
	dirtyID := seedTerminalTask(t, m, repo, dirtyWT, "f-gc-d", proto.TaskStateFailed, true)
	cleanID := seedTerminalTask(t, m, repo, cleanWT, "f-gc-c", proto.TaskStateFailed, true)
	active, _, _ := writeCacheLeaves(t, m.cfg.DataDir, cleanID)
	resp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dirtyWT); err != nil {
		t.Fatalf("无 force 脏树必须留: %v", err)
	}
	if _, err := os.Lstat(cleanWT); !os.IsNotExist(err) {
		t.Fatalf("净树应被 reclaim 掉: %v", err)
	}
	assertGone(t, active)
	if resp.Failures != 0 {
		t.Fatalf("skip 不得计入 Failures，实得 %d", resp.Failures)
	}
	var sawSkip, sawDeleted bool
	for _, row := range resp.WorktreeRows {
		if row.TaskID == dirtyID && row.Status == proto.GCItemSkipped {
			sawSkip = true
		}
		if row.TaskID == cleanID && row.Status == proto.GCItemDeleted {
			sawDeleted = true
		}
	}
	if !sawSkip || !sawDeleted {
		t.Fatalf("应同时有脏 skip 与净 deleted：%+v", resp.WorktreeRows)
	}
}

func TestGCExecuteForceRemovesDirty(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-gc-force", "f-gc-force")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedTerminalTask(t, m, repo, wt, "f-gc-force", proto.TaskStateFailed, true)
	if _, err := m.GC(context.Background(), true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Fatalf("force 执行应删脏树: %v", err)
	}
}

func TestGCMissingLeafIsIdempotentNotFailed(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	id := "missing0-0000-4000-8000-000000000001"
	now := time.Now().UTC()
	mustCreateTask(t, m.st, &proto.Task{ID: id, Executor: "fake", State: proto.TaskStateFailed, CreatedAt: now, UpdatedAt: now})
	resp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failures != 0 {
		t.Fatalf("缺失叶子不得 failed，Failures=%d rows=%+v", resp.Failures, resp.CacheRows)
	}
	if resp.ReleasableBytes == nil || *resp.ReleasableBytes != 0 {
		t.Fatalf("缺失不得虚增字节：%+v", resp.ReleasableBytes)
	}
}

func TestGCRemoveAllFailureIsFailedRowAndContinues(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	id := "failrm00-0000-4000-8000-000000000001"
	other := "okrm0000-0000-4000-8000-000000000002"
	active, _, _ := seedTaskWithCache(t, m, id, proto.TaskStateFailed)
	otherActive, _, _ := seedTaskWithCache(t, m, other, proto.TaskStateFailed)
	m.removeCacheLeafFn = func(path string) error {
		if path == active {
			return errors.New("cache-remove-injected")
		}
		return os.RemoveAll(path)
	}
	resp, err := m.GC(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failures < 1 {
		t.Fatalf("注入失败必须计入 Failures，实得 %+v", resp)
	}
	var sawFail bool
	for _, row := range resp.CacheRows {
		if row.Path == active && row.Status == proto.GCItemFailed && strings.Contains(row.Error, "cache-remove-injected") {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("失败必须进 JSON 行：%+v", resp.CacheRows)
	}
	assertGone(t, otherActive)
}

func TestHandleGCGetPreviewPostExecuteAndAuth(t *testing.T) {
	s, m := newGCServer(t)
	id := "httpgc00-0000-4000-8000-000000000001"
	seedTaskWithCache(t, m, id, proto.TaskStateFailed)

	unauth := doGC(t, s, http.MethodGet, "/api/gc", "", "")
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权 GET /api/gc 应 401，实得 %d %s", unauth.Code, unauth.Body.String())
	}
	unauthP := doGC(t, s, http.MethodPost, "/api/gc", `{"force":false}`, "")
	if unauthP.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权 POST /api/gc 应 401，实得 %d", unauthP.Code)
	}

	get := doGC(t, s, http.MethodGet, "/api/gc", "", "test")
	if get.Code != http.StatusOK {
		t.Fatalf("GET 应 200 不是 503，实得 %d %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "gc 尚未接线") {
		t.Fatal("503 空壳不得再达")
	}
	var preview proto.GCResp
	if err := json.Unmarshal(get.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Preview {
		t.Fatal("GET 必须 preview=true")
	}

	forceGet := doGC(t, s, http.MethodGet, "/api/gc?force=true", "", "test")
	var fg proto.GCResp
	if err := json.Unmarshal(forceGet.Body.Bytes(), &fg); err != nil {
		t.Fatal(err)
	}
	if !fg.Preview || !fg.Force {
		t.Fatalf("GET ?force=true 仍是预览且 force=true，实得 %+v", fg)
	}

	post := doGC(t, s, http.MethodPost, "/api/gc", `{"force":false}`, "test")
	if post.Code != http.StatusOK {
		t.Fatalf("POST 应 200，实得 %d %s", post.Code, post.Body.String())
	}
	var execResp proto.GCResp
	if err := json.Unmarshal(post.Body.Bytes(), &execResp); err != nil {
		t.Fatal(err)
	}
	if execResp.Preview {
		t.Fatal("POST 必须 preview=false")
	}
}

func TestHandleGCJSONZeroReleasableBytesPresent(t *testing.T) {
	s, _ := newGCServer(t)
	rec := doGC(t, s, http.MethodGet, "/api/gc", "", "test")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["releasable_bytes"]
	if !ok {
		t.Fatal("空快照成功响应必须带 releasable_bytes:0，不得缺席")
	}
	if string(raw) != "0" {
		t.Fatalf("releasable_bytes=%s want 0", raw)
	}
}
```

跑 `go test ./internal/agentd -run 'TestGC|TestHandleGC' -count=1`
预期红：`ErrGCUnwired` / 503 / 叶子还在。

### 3.4 步骤 2 — 绿：实现 `Manager.GC` 与 `handleGC`

把 `internal/agentd/gc.go` **整文件替换**为：

```go
// gc.go —— 机器级 handoff gc 的编排与 HTTP 接缝。
//
// 职责：
//   - 按任务表终态行预览/执行缓存叶子删除，并在 agentd 内复用 reclaim 收残树
//   - GET /api/gc 只预览，POST /api/gc 才执行；两条路由继续走 Server.Handler 的 auth
//
// 边界：
//   - 纯资源动作：不改任务状态、不删任务目录/分支/用户自建树/repos/agentd.log
//   - 不扫描无任务行孤儿目录；不 RemoveAll DataDir/tmp 根
//   - CLI 不得逐任务 POST /api/tasks/{id}/reclaim——残树循环只在本文件
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/proto"
)

func (m *Manager) GC(ctx context.Context, force, execute bool) (resp *proto.GCResp, err error) {
	if m.log != nil {
		m.log.Info("gc 进入", "force", force, "execute", execute)
		defer func() {
			if err != nil {
				m.log.Error("gc 未完成", "force", force, "execute", execute, "cause", err)
				return
			}
			if resp != nil {
				var bytes int64
				if resp.ReleasableBytes != nil {
					bytes = *resp.ReleasableBytes
				}
				m.log.Info("gc 完成", "force", force, "execute", execute,
					"preview", resp.Preview, "scanned", resp.Scanned,
					"failures", resp.Failures, "bytes", bytes,
					"cache_rows", len(resp.CacheRows), "worktree_rows", len(resp.WorktreeRows))
			}
		}()
	}
	if m.st == nil || m.cfg == nil {
		return nil, fmt.Errorf("gc 未就绪")
	}
	tasks, err := m.st.ListTasks()
	if err != nil {
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	if m.log != nil {
		m.log.Info("gc 已读任务快照", "tasks", len(tasks), "execute", execute)
	}
	resp = &proto.GCResp{
		Preview:      !execute,
		Force:        force,
		CacheRows:    []proto.GCCacheRow{},
		WorktreeRows: []proto.GCWorktreeRow{},
	}
	var releasable int64
	seen := map[string]struct{}{}
	for _, t := range tasks {
		if !t.State.IsTerminal() {
			continue
		}
		resp.Scanned++
		for _, leaf := range planTaskCacheLeaves(m.cfg.DataDir, t.ID, tasks) {
			key := filepath.Clean(leaf.Path)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			row := proto.GCCacheRow{TaskID: t.ID, Path: leaf.Path}
			if leaf.Skip || isCacheTmpRoot(m.cfg.DataDir, leaf.Path) {
				row.Status = proto.GCItemSkipped
				row.Error = leaf.Note
				if row.Error == "" {
					row.Error = "拒绝删除 DataDir/tmp 根"
				}
				if m.log != nil {
					m.log.Info("gc 跳过缓存叶子", "task", t.ID, "path", leaf.Path, "reason", row.Error)
				}
				resp.CacheRows = append(resp.CacheRows, row)
				continue
			}
			n, werr := sumRegularFileBytes(leaf.Path)
			if werr != nil {
				if m.log != nil {
					m.log.Error("gc 统计缓存字节失败", "task", t.ID, "path", leaf.Path, "cause", werr)
				}
			} else {
				row.Bytes = n
			}
			_, lerr := os.Lstat(leaf.Path)
			missing := lerr != nil && os.IsNotExist(lerr)
			if !execute {
				if missing {
					continue
				}
				row.Status = proto.GCItemPlanned
				releasable += row.Bytes
				resp.CacheRows = append(resp.CacheRows, row)
				continue
			}
			if missing {
				continue
			}
			if m.log != nil {
				m.log.Info("gc 删除缓存叶子前", "task", t.ID, "path", leaf.Path, "bytes", row.Bytes)
			}
			if rerr := m.removeCacheLeaf(leaf.Path); rerr != nil {
				if m.log != nil {
					m.log.Error("gc 删除缓存叶子失败", "task", t.ID, "path", leaf.Path, "cause", rerr)
				}
				row.Status = proto.GCItemFailed
				row.Error = rerr.Error()
				resp.Failures++
				resp.CacheRows = append(resp.CacheRows, row)
				continue
			}
			row.Status = proto.GCItemDeleted
			releasable += row.Bytes
			resp.CacheRows = append(resp.CacheRows, row)
		}
	}
	resp.ReleasableBytes = &releasable
	if execute {
		m.appendGCWorktreesExecute(ctx, resp, tasks, force)
	} else {
		m.appendGCWorktreesPreview(resp, tasks, force)
	}
	return resp, nil
}

func (m *Manager) appendGCWorktreesPreview(resp *proto.GCResp, tasks []proto.Task, force bool) {
	list, err := m.ReclaimList()
	if err != nil {
		if m.log != nil {
			m.log.Error("gc 残树体检失败，工作树行留空继续缓存报告", "cause", err)
		}
		return
	}
	for _, r := range list.Rows {
		row := proto.GCWorktreeRow{
			TaskID: r.TaskID, Name: r.Name, State: r.State, Branch: r.Branch,
			WorkDir: r.WorkDir, Worktree: r.Worktree, DirtyCount: r.DirtyCount, Note: r.Note,
		}
		switch r.Worktree {
		case proto.WorktreeDirty:
			if !force {
				row.Status = proto.GCItemSkipped
				if row.Note == "" {
					row.Note = "脏工作树未带 force，跳过"
				}
			} else {
				row.Status = proto.GCItemPlanned
			}
		case proto.WorktreeUnknown:
			row.Status = proto.GCItemSkipped
			if row.Note == "" {
				row.Note = "工作树状态判不出，跳过"
			}
		default:
			row.Status = proto.GCItemPlanned
		}
		resp.WorktreeRows = append(resp.WorktreeRows, row)
	}
	for _, t := range tasks {
		if !t.State.IsTerminal() || t.WorkDir == "" || t.WorktreeManaged {
			continue
		}
		resp.WorktreeRows = append(resp.WorktreeRows, proto.GCWorktreeRow{
			TaskID: t.ID, Name: t.Name, State: string(t.State), Branch: t.Branch,
			WorkDir: t.WorkDir, Status: proto.GCItemSkipped, Note: "非 managed 工作树，跳过",
		})
	}
}

func (m *Manager) appendGCWorktreesExecute(ctx context.Context, resp *proto.GCResp, tasks []proto.Task, force bool) {
	for _, t := range tasks {
		if !t.State.IsTerminal() || t.WorkDir == "" {
			continue
		}
		row := proto.GCWorktreeRow{
			TaskID: t.ID, Name: t.Name, State: string(t.State), Branch: t.Branch, WorkDir: t.WorkDir,
		}
		if !t.WorktreeManaged {
			row.Status = proto.GCItemSkipped
			row.Note = "非 managed 工作树，跳过"
			resp.WorktreeRows = append(resp.WorktreeRows, row)
			continue
		}
		if m.log != nil {
			m.log.Info("gc 调用 reclaim 前", "task", t.ID, "force", force, "workdir", t.WorkDir)
		}
		wr, err := m.Reclaim(ctx, t.ID, force)
		if err == nil {
			row.Status = proto.GCItemDeleted
			if wr != nil {
				row.WorkDir = wr.WorkDir
				row.Branch = wr.Branch
			}
			resp.WorktreeRows = append(resp.WorktreeRows, row)
			continue
		}
		if m.log != nil {
			m.log.Info("gc reclaim 未删除", "task", t.ID, "cause", err)
		}
		var dirty *DirtyWorktreeError
		switch {
		case errors.As(err, &dirty):
			row.Status = proto.GCItemSkipped
			row.Worktree = proto.WorktreeDirty
			row.DirtyCount = len(dirty.Files)
			row.Note = err.Error()
		case errors.Is(err, ErrReclaimNotManaged),
			errors.Is(err, ErrReclaimNotTerminal),
			errors.Is(err, ErrReclaimRepoUnreachable):
			row.Status = proto.GCItemSkipped
			row.Error = err.Error()
			row.Note = err.Error()
		default:
			row.Status = proto.GCItemFailed
			row.Error = err.Error()
			resp.Failures++
		}
		resp.WorktreeRows = append(resp.WorktreeRows, row)
	}
}

func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	if r.Method == http.MethodPost {
		var req proto.GCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.log.Warn("gc 请求体解码失败，按默认 force=false 处理", "cause", err)
		}
		force = req.Force
	}
	execute := r.Method == http.MethodPost
	s.log.Info("gc HTTP 进入", "method", r.Method, "path", r.URL.Path, "force", force, "execute", execute)
	if s.mgr == nil {
		s.log.Warn("gc 请求到达但 manager 未注入", "method", r.Method, "path", r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.GC(r.Context(), force, execute)
	if err != nil {
		s.log.Error("gc 请求失败", "method", r.Method, "force", force, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	s.log.Info("gc HTTP 完成", "method", r.Method, "force", force, "execute", execute,
		"scanned", resp.Scanned, "failures", resp.Failures)
	writeJSON(w, http.StatusOK, resp)
}
```

要点（不要「适当处理」）：

- GET 的 force 只认查询串 `force=true`；POST 的 force 只认 JSON body。POST 不要把查询串 force 留下来覆盖 body——上面的代码在 POST 分支里用 body 覆盖。
- 成功路径**必须** `writeJSON(w, 200, resp)`。禁止再返回 503。
- `ReclaimList` 无 ctx 参数（B77 冻结签名）。预览用它；执行用 `Reclaim(ctx, …)` 满足断言 14 对动作透传 ctx。
- `scanned` 在终态行循环里 `++`，不要用 `ReclaimList.Scanned`。

跑 `go test ./internal/agentd -run 'TestGC|TestHandleGC|TestReclaim' -count=1` 预期绿。
确认 `rg ErrGCUnwired internal/agentd` 无命中。
确认 `rg TestHandleGCTicket0 internal/agentd` 无命中。

提交：`git commit -m "feat(B298): implement Manager.GC batch preview/execute and write JSON"`

### 3.5 步骤 3 — 日志

`GC` 进入（force/execute）、读快照后（tasks）、每条跳过/删除前/删除失败、reclaim 前、完成（scanned/failures/bytes）。`handleGC` 进入、manager 空、GC 错误、成功。用 `m.log` / `s.log`。

### 3.6 步骤 4 — 注释

文件头已给。给 `GC` 保留原导出注释（参数 ctx/force/execute、缓存不读 force、execute 重读 ListTasks）。`appendGCWorktreesPreview` 注明 ReclaimList 无 ctx。`handleGC` 注明 GET/POST 与 auth 包裹、解码失败 force=false。

### 3.7 T2 缺陷族

- 族 1：执行中崩溃 → 幂等重跑；TOCTOU 同 T1 接受项。execute 自己 ListTasks，不吃 CLI 预览。
- 族 2：RemoveAll 失败进 JSON+Failures（70、13、72）。缺失叶子不是 failed。ReclaimList 失败只空工作树行，不把整个 GC 报成「无可清」。
- 族 3：WalkDir 不跟随；Windows 锁 → failed 行。
- 族 4：503 测试已删。负例：未鉴权 401、孤儿不删、dirty 无 force 不中止。
- 族 5：双路由同一 auth；占用/根保护与 T1 同一 helper。

---

## 4. Task 3：CLI 渲染与退出码

契约：32–41。

### 4.1 文件集

`cmd/gc.go`、`cmd/gc_test.go`。不改 `cmd/root.go`（`--target` 已是 persistent + `newTargetClient`）。

### 4.2 判据先在基线跑

```bash
go test ./cmd -run 'TestRunGCDegradesOnOldAgentd|TestRenderGCDistinguishesUnknownBytes' -count=1
```

预期退出 0（§1.1）。本 task 只跑 `go test ./cmd -run 'TestRunGC|TestRenderGC|TestGCCmd' -count=1`。

### 4.3 步骤 1 — 红：退出码与请求形状

**保持** `TestRunGCDegradesOnOldAgentd` 与 `TestRenderGCDistinguishesUnknownBytes` 原样（文案 `将释放字节：0` / `将释放字节：未计算` 不得改）。

在 `cmd/gc_test.go` 追加：

```go
func withGCFlags(t *testing.T, force, yes, jsonOut bool) {
	t.Helper()
	oldF, oldY, oldJ := gcForce, gcYes, gcJSON
	gcForce, gcYes, gcJSON = force, yes, jsonOut
	t.Cleanup(func() { gcForce, gcYes, gcJSON = oldF, oldY, oldJ })
}

func newRunGCCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	return cmd, &out
}

func TestRunGCPreviewUsesGETAndDoesNotPost(t *testing.T) {
	withGCFlags(t, false, false, false)
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		zero := int64(0)
		_ = json.NewEncoder(w).Encode(&proto.GCResp{Preview: true, ReleasableBytes: &zero, CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{}, Scanned: 3})
	}))
	t.Cleanup(ts.Close)
	cmd, out := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatalf("预览应退出 0: %v", err)
	}
	if len(paths) != 1 || paths[0] != "GET /api/gc" {
		t.Fatalf("无 --yes 只发 GET /api/gc，实得 %v", paths)
	}
	if !strings.Contains(out.String(), "将释放字节：0") {
		t.Fatalf("预览必须打字节量：%s", out.String())
	}
	if !strings.Contains(out.String(), "共扫") || !strings.Contains(out.String(), "3") {
		t.Fatalf("预览应打共扫终态任务数：%s", out.String())
	}
}

func TestRunGCForceWithoutYesStillGET(t *testing.T) {
	withGCFlags(t, true, false, false)
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"preview":true,"force":true,"releasable_bytes":0,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`))
	}))
	t.Cleanup(ts.Close)
	cmd, _ := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "GET /api/gc?force=true" {
		t.Fatalf("仅 --force 应 GET ?force=true，实得 %v", paths)
	}
}

func TestRunGCYesPostsForceBody(t *testing.T) {
	withGCFlags(t, true, true, true)
	var paths []string
	var body proto.GCRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&proto.GCResp{Preview: false, Force: true, CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{}})
	}))
	t.Cleanup(ts.Close)
	cmd, _ := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "POST /api/gc" {
		t.Fatalf(" --yes 应只 POST /api/gc，实得 %v", paths)
	}
	if !body.Force {
		t.Fatal(" --yes --force 必须把 force=true 放进 JSON body")
	}
}

func TestRunGCJSONDistinguishesAbsentAndZero(t *testing.T) {
	withGCFlags(t, false, false, true)
	cases := []struct {
		name string
		body string
		has  bool
		zero bool
	}{
		{"zero", `{"preview":true,"force":false,"releasable_bytes":0,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`, true, true},
		{"absent", `{"preview":true,"force":false,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(ts.Close)
			cmd, out := newRunGCCmd(t)
			if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
				t.Fatalf("cli json: %v %s", err, out.String())
			}
			raw, ok := fields["releasable_bytes"]
			if ok != tc.has {
				t.Fatalf("present=%v want %v (%s)", ok, tc.has, out.String())
			}
			if tc.zero && string(raw) != "0" {
				t.Fatalf("want 0 got %s", raw)
			}
		})
	}
}

func TestRunGCExecuteFailuresNonZero(t *testing.T) {
	withGCFlags(t, false, true, false)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&proto.GCResp{
			Preview:  false,
			Failures: 1,
			CacheRows: []proto.GCCacheRow{{
				TaskID: "t1", Path: "/tmp/x", Status: proto.GCItemFailed, Error: "e",
			}},
			WorktreeRows: []proto.GCWorktreeRow{},
		})
	}))
	t.Cleanup(ts.Close)
	cmd, out := newRunGCCmd(t)
	err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL)
	if err == nil {
		t.Fatal("Failures>0 的 execute 必须非零")
	}
	if !strings.Contains(out.String(), "失败") && !strings.Contains(err.Error(), "失败") {
		t.Fatalf("必须能看见失败：stdout=%s err=%v", out.String(), err)
	}
}

func TestRunGCExecuteSkipIsZero(t *testing.T) {
	withGCFlags(t, false, true, false)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		zero := int64(12)
		_ = json.NewEncoder(w).Encode(&proto.GCResp{
			Preview: false, Failures: 0, ReleasableBytes: &zero,
			CacheRows:    []proto.GCCacheRow{{TaskID: "t", Path: "/p", Status: proto.GCItemDeleted, Bytes: 12}},
			WorktreeRows: []proto.GCWorktreeRow{{TaskID: "t2", Status: proto.GCItemSkipped, Note: "脏"}},
			Scanned:      2,
		})
	}))
	t.Cleanup(ts.Close)
	cmd, out := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatalf("仅 skip 应退出 0: %v %s", err, out.String())
	}
}

func TestGCCmdRejectsPositionalArgs(t *testing.T) {
	if err := gcCmd.Args(gcCmd, []string{"task-id"}); err == nil {
		t.Fatal("handoff gc 不得接受位置参数")
	}
}

func TestGCCmdReusesRootTargetFlag(t *testing.T) {
	if gcCmd.Flags().Lookup("target") != nil {
		t.Fatal("gc 不得自建 --target，必须复用 root persistent / newTargetClient")
	}
	if rootCmd.PersistentFlags().Lookup("target") == nil {
		t.Fatal("root 必须已有 --target")
	}
}

func TestRenderGCShowsFourStatuses(t *testing.T) {
	var buf bytes.Buffer
	zero := int64(1)
	renderGC(&buf, &proto.GCResp{
		Preview: true, ReleasableBytes: &zero, Scanned: 4,
		CacheRows: []proto.GCCacheRow{
			{TaskID: "a", Path: "/a", Status: proto.GCItemPlanned, Bytes: 1},
			{TaskID: "b", Path: "/b", Status: proto.GCItemDeleted},
			{TaskID: "c", Path: "/c", Status: proto.GCItemSkipped, Error: "占用"},
			{TaskID: "d", Path: "/d", Status: proto.GCItemFailed, Error: "e"},
		},
		WorktreeRows: []proto.GCWorktreeRow{
			{TaskID: "e", Status: proto.GCItemSkipped, Worktree: proto.WorktreeDirty, DirtyCount: 1},
		},
	})
	got := buf.String()
	for _, want := range []string{"将删", "已删", "跳过", "失败"} {
		if !strings.Contains(got, want) {
			t.Fatalf("渲染缺少 %q：%s", want, got)
		}
	}
}
```

`cmd/gc_test.go` 的 import 改为：

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)
```

跑 `go test ./cmd -run 'TestRunGC|TestRenderGC|TestGCCmd' -count=1`
预期：过旧/字节文案仍绿；`TestRunGCExecuteFailuresNonZero` 红（今日 `runGC` 恒 nil）；`TestRenderGCShowsFourStatuses` 红（今日不打四态）。

### 4.4 步骤 2 — 绿：`runGC` 退出码 + `renderGC` 行

替换 `cmd/gc.go` 的 `runGC` 成功尾部（保留过旧分支）。`runGC` 完整函数：

```go
func runGC(cmd *cobra.Command, cl *client.Client, addr string) error {
	slog.Default().Info("CLI gc 进入", "target", addr, "force", gcForce, "execute", gcYes, "json", gcJSON)
	var (
		resp *proto.GCResp
		err  error
	)
	if gcYes {
		resp, err = cl.GC(cmd.Context(), gcForce)
	} else {
		resp, err = cl.GCPreview(cmd.Context(), gcForce)
	}
	if errors.Is(err, client.ErrGCUnsupported) {
		slog.Default().Info("CLI gc 对端过旧", "target", addr)
		_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "agentd %s 过旧，升级后再跑 gc\n", addr)
		return printErr
	}
	if err != nil {
		slog.Default().Error("CLI gc 请求失败", "target", addr, "cause", err)
		return err
	}
	out := cmd.OutOrStdout()
	if gcJSON {
		if err := json.NewEncoder(out).Encode(resp); err != nil {
			slog.Default().Error("CLI gc JSON 输出失败", "target", addr, "cause", err)
			return err
		}
	} else {
		renderGC(out, resp)
	}
	if !resp.Preview && resp.Failures > 0 {
		slog.Default().Error("CLI gc 执行有失败项", "target", addr, "failures", resp.Failures)
		return fmt.Errorf("gc 有 %d 项本应删除但失败", resp.Failures)
	}
	slog.Default().Info("CLI gc 完成", "target", addr, "preview", resp.Preview, "failures", resp.Failures)
	return nil
}
```

`renderGC` **在现有两行字节文案之后**追加，不得改那两行（`TestRenderGCDistinguishesUnknownBytes`）：

```go
func renderGC(w io.Writer, resp *proto.GCResp) {
	mode := "预览"
	if !resp.Preview {
		mode = "已执行"
	}
	if resp.ReleasableBytes == nil {
		fmt.Fprintf(w, "%s     将释放字节：未计算\n", mode)
	} else {
		fmt.Fprintf(w, "%s     将释放字节：%d\n", mode, *resp.ReleasableBytes)
	}
	fmt.Fprintf(w, "缓存     %d 行；工作树 %d 行；失败 %d\n",
		len(resp.CacheRows), len(resp.WorktreeRows), resp.Failures)
	fmt.Fprintf(w, "共扫     %d 个终态任务\n", resp.Scanned)
	for _, row := range resp.CacheRows {
		fmt.Fprintf(w, "  缓存  %s  %s  %d  %s", short8(row.TaskID), gcItemLabel(row.Status), row.Bytes, row.Path)
		if row.Error != "" {
			fmt.Fprintf(w, "  %s", row.Error)
		}
		fmt.Fprintln(w)
	}
	for _, row := range resp.WorktreeRows {
		fmt.Fprintf(w, "  工作树 %s  %s  %s  %s", short8(row.TaskID), gcItemLabel(row.Status), string(row.Worktree), row.WorkDir)
		if row.Note != "" {
			fmt.Fprintf(w, "  %s", row.Note)
		}
		if row.Error != "" {
			fmt.Fprintf(w, "  %s", row.Error)
		}
		fmt.Fprintln(w)
	}
}

func gcItemLabel(s proto.GCItemStatus) string {
	switch s {
	case proto.GCItemPlanned:
		return "将删"
	case proto.GCItemDeleted:
		return "已删"
	case proto.GCItemSkipped:
		return "跳过"
	case proto.GCItemFailed:
		return "失败"
	default:
		return string(s)
	}
}
```

`short8` 已在 `cmd/status.go` 同包存在，直接用。

跑 T3 测试预期绿；`TestRunGCDegradesOnOldAgentd` 必须仍绿。

提交：`git commit -m "feat(B298): render gc report and fail execute only on Failures"`

### 4.5 日志 / 注释

`runGC` 已有进入/过旧/请求失败/完成。补执行 Failures 的 Error。`renderGC` 纯投影不打 slog。`gcItemLabel` 一行注释：人读四态，JSON 仍走 `--json` 的枚举原值。

### 4.6 T3 缺陷族

- 族 2：预览恒 0（拿不到列表才非零）；execute 仅 Failures>0 非零；先打印报告再 return error。
- 族 4：无 `--yes` 以请求路径断言可红；过旧测试保持。
- 族 5：CLI 无本地删除。

---

## 5. Task 4：缝合与负例

契约：10、11、26、27、37、38；序列化边界。

### 5.1 文件集

- `internal/client/gc_test.go`（补 200 解码）
- `cmd/gc_test.go`（链路：client → `--json` / `renderGC`）
- 实现文件目标零改动。若 T1–T3 已列文件需微修，不得扩大到 `web/` 或 `internal/proto/gc.go` 字段。

### 5.2 判据先在基线跑

```bash
go test ./internal/client -run 'TestGCPostDouble404IsUnsupported' -count=1
go test ./cmd -run 'TestRunGCDegradesOnOldAgentd|TestRunGCJSONDistinguishesAbsentAndZero' -count=1
go test ./internal/proto -run 'TestGCGoldenJSON' -count=1
```

本 task 测试范围：上述 + 本步新增。四包全量与 `web/` diff **由协调者执行，不派发**（§5.6）。

### 5.3 步骤 1 — 红/绿：client 200 解码

在 `internal/client/gc_test.go` 追加：

```go
func TestGCPreviewAndGCDecode200ReleasableBytes(t *testing.T) {
	zero := int64(0)
	present, err := json.Marshal(proto.GCResp{
		Preview: true, ReleasableBytes: &zero,
		CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{},
	})
	if err != nil {
		t.Fatal(err)
	}
	absent, err := json.Marshal(proto.GCResp{
		Preview: false, CacheRows: []proto.GCCacheRow{}, WorktreeRows: []proto.GCWorktreeRow{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write(absent)
			return
		}
		n++
		_, _ = w.Write(present)
	}))
	t.Cleanup(ts.Close)
	cl := New(ts.URL, "tok")
	pre, err := cl.GCPreview(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pre.ReleasableBytes != nil {
		t.Fatalf("缺席 JSON 必须解成 nil，实得 %+v", pre.ReleasableBytes)
	}
	got, err := cl.GC(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReleasableBytes == nil || *got.ReleasableBytes != 0 {
		t.Fatalf("显式 0 必须可分，实得 %+v", got.ReleasableBytes)
	}
	if n != 1 {
		t.Fatalf("POST 次数=%d", n)
	}
}
```

跑 `go test ./internal/client -run 'TestGC' -count=1`：client 已有 200 `Decode`，此测试应直接绿。若红，修的是解码而不是改 DTO。

### 5.4 步骤 2 — 穿过真实 JSON 的 CLI 链路

T3 的 `TestRunGCJSONDistinguishesAbsentAndZero` 已经是 httptest JSON → `Client.GCPreview` → `runGC --json`。再加一条人读：

```go
func TestRunGCRenderPreservesAbsentVsZeroThroughClient(t *testing.T) {
	withGCFlags(t, false, false, false)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"preview":true,"force":false,"cache_rows":[],"worktree_rows":[],"scanned":0,"failures":0}`))
	}))
	t.Cleanup(ts.Close)
	cmd, out := newRunGCCmd(t)
	if err := runGC(cmd, client.New(ts.URL, "tok"), ts.URL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "将释放字节：未计算") {
		t.Fatalf("缺席必须显示未计算：%s", out.String())
	}
}
```

agentd 成功路径（T2 `TestHandleGCJSONZeroReleasableBytesPresent`）锁「真实 handleGC → JSON 显式 0」。缺席形状在成功 GC 里不会再出现（成功总写指针）；client/CLI 仍必须能区分，避免假零。这不是两端各自测：JSON 字节经过 `Client.do` 的 HTTP body 与 `json.Decoder`。

跑 `go test ./cmd -run 'TestRunGCJSON|TestRunGCRenderPreserves|TestRenderGCShowsFourStatuses|TestRunGCDegradesOnOldAgentd' -count=1`

### 5.5 步骤 3 — 日志/注释

T4 原则上零生产改动。若必须碰文件，沿用 T1–T3 的 slog，禁止新增 `fmt.Printf`。

### 5.6 协调者门禁（本 task 由协调者执行，不派发）

这些步骤会驱动长时间测试 / 对照 diff，且与「不派发、不调派发 CLI」冲突，**留本地**：

1. `go test ./internal/proto ./internal/client ./internal/agentd ./cmd -count=1`
   预期退出 0（本 plan 基线已绿：agentd 85.359s）。若红：再跑一次同一命令。仍红才查本卡测试；第二次绿记 flake，不改生产去「稳」它。
2. `git diff 26e2ab7f --name-only -- web/` 必须空。`git diff --name-only -- web/` 相对 plan 提交也必须空。
3. 真机清单 §7 全部由协调者执行，不写进实现者 task。

---

## 6. 五项检查

### 6.1 缺陷族

已写入 T1 §2.9、T2 §3.7、T3 §4.6。追加：

- 序列化：agentd `writeJSON(GCResp)`（T2 `TestHandleGCJSONZeroReleasableBytesPresent` + proto `TestGCGoldenJSON`）→ client 200 解码（T4）→ CLI `--json` Encode 与 `renderGC`（T3/T4）。缺席 vs 0 可分。
- 枚举：`GCItemStatus` 无第三方白名单；T3 `TestRenderGCShowsFourStatuses` 四值各现一次。无风险，因为中间 client 不解释状态字。
- 承重安全：tmp 根（T1）、非终态不删（T1+T2）、短号占用（T1+T2）、未鉴权 401（T2）、force 无 yes 不动盘（T2+T3）。每条有能红的测试。

### 6.2 序列化边界

手写投影点：

| 点 | 文件 | 测试 |
|---|---|---|
| proto JSON tags | `internal/proto/gc.go` | `TestGCGoldenJSON`（零改动） |
| agentd Encode | `writeJSON` via `handleGC` | `TestHandleGCJSONZeroReleasableBytesPresent` |
| client Decode | `internal/client/gc.go` | `TestGCPreviewAndGCDecode200ReleasableBytes` |
| CLI `--json` Encode | `cmd/gc.go#runGC` | `TestRunGCJSONDistinguishesAbsentAndZero` |
| CLI 人读 | `renderGC` | `TestRenderGCDistinguishesUnknownBytes` + `TestRunGCRenderPreservesAbsentVsZeroThroughClient` |

不手搭 map。不新增跨语言。

### 6.3 有界文件集

T1：`cachegc.go` / `cachegc_test.go` / `manager.go`
T2：`gc.go` / `gc_test.go`
T3：`cmd/gc.go` / `cmd/gc_test.go`
T4：`internal/client/gc_test.go` / `cmd/gc_test.go`
另允许：本 plan、本台账。禁止：`web/`、`internal/executor/tempdir.go`、`internal/proto/gc.go` 字段、`TaskTmpDir` 形状、第二份 helper。

### 6.4 类型 / 真机

`d_gateway`/`d_transport` 图标 boundary，本卡对面是自有 Manager 与 httptest，机内可闭环。真机清单见 §7，协调者所有。Windows 文件锁、linux-01 真实 gocache 规模、未升级机器过旧——夹具验不了。

### 6.5 接缝覆盖（双向）

**测试 → 缝**（看入口符号，不看标注）：

| 测试 | 入口 | 缝 |
|---|---|---|
| `TestDonePurgesBothCacheLeavesAndKeepsTaskDir` | `Manager.Done` | 缝 1 |
| `TestStopPurgesBothCacheLeaves` | `Manager.Stop` | 缝 1 |
| `TestCompensatePurgesCacheWhenWorktreeRemoveFails` | `Manager.compensateWorkspace` | 缝 1 |
| `TestDoneKeepsActiveLeafWhenOtherNonTerminalSharesID8` | `Manager.Done` | 缝 1 |
| `TestDoneOnRunningDoesNotPurgeCache` | `Manager.Done` | 缝 1 |
| `TestDonePurgeFailureDoesNotBlockArchive` | `Manager.Done` | 缝 1 |
| `TestGCPreviewListsTerminalLeavesWithoutDeleting` | `Manager.GC` | 缝 2 |
| `TestGCScannedCountsAllTerminalRows` | `Manager.GC` | 缝 2 |
| `TestGCExecuteRereadsSnapshot` | `Manager.GC` | 缝 2 |
| `TestGCExecuteSkipsDirtyWithoutForceAndContinues` | `Manager.GC` | 缝 2 |
| `TestHandleGCGetPreviewPostExecuteAndAuth` | `Server.Handler` → `handleGC` → `Manager.GC` | 缝 2 |
| `TestRunGCPreviewUsesGETAndDoesNotPost` | `runGC` → `Client.GCPreview` | 缝 2 调用方 |
| `TestRunGCYesPostsForceBody` | `runGC` → `Client.GC` | 缝 2 调用方 |
| `TestRunGCDegradesOnOldAgentd` | `runGC` → `Client.GCPreview` | 缝 2 过旧 |
| `TestRunGCJSONDistinguishesAbsentAndZero` | `runGC` → client HTTP JSON | 缝 2 序列化 |

**缝 → 测试**：缝 1 至少 `TestDonePurges*` / `TestStopPurge*` / `TestCompensatePurge*`。缝 2 至少 `TestGCPreview*` + `TestHandleGCGetPreview*` + `TestRunGCPreview*`。

**内部锁（已声明）：** `TestCacheID8AndLeaves`、`TestCacheTmpRootGuard`、`TestActiveLeafOccupied`、`TestSumRegularFileBytesIgnoresDirSymlinkAndNonRegular`、`TestPurgeRefusesTmpRootEvenIfCalledDirectly`、`TestGCCmdRejectsPositionalArgs`、`TestGCCmdReusesRootTargetFlag`、`TestRenderGCShowsFourStatuses`（renderGC 不是声明缝，但是 CLI 投影；`TestRunGCPreviewUsesGETAndDoesNotPost` 从 `runGC` 进缝，四态渲染是附加）。理由：空 taskID 从 Done/GC 构造不出；Args/flag 是 cobra 配置不是 HTTP 缝。

无未声明退路。

---

## 7. 真机清单（抄自 breakdown；协调者所有；不派发）

1. linux-01 升级 agentd 后：`done` 一个跑过 `go test` 的任务 → 该任务 gocache 叶子消失、`handoff attach` 仍可读 render.log。
2. linux-01：`handoff gc --target linux-01` 预览给出真实缓存规模的去重字节与脏树 skip；`--yes` 后终态叶子消失、非终态 tmp 仍在、退出 0。
3. 未升级机器：`handoff gc --target …` 报过旧、退出 0、盘上不变。
4. linux-01：一净一脏无 `--force` → 脏树仍在退 0；`--yes --force` 后脏树消失。
5. Windows（若仍在支持矩阵）：gc 冒烟——文件占用导致的删除失败呈现为 failed 行/日志，命令不崩溃。
6. 全包 `go test ./internal/proto ./internal/client ./internal/agentd ./cmd` 无沙箱；若红重跑一次。

本 plan 节点不把未跑真机写成 pass。

---

## 8. spec 用户故事 → task

| 故事 | task |
|---|---|
| 1 `done` 后 gocache 没了，attach 仍可读 | T1 `TestDonePurgesBothCacheLeavesAndKeepsTaskDir`；真机 1 |
| 2 linux-01 预览字节与 skip，`--yes` 清终态留脏树，再 `--force` | T2 预览/执行/脏树 + T3 渲染；真机 2、4 |
| 3 对端过旧，文案含过旧，退出 0 | Ticket 0 + T4 复验 `TestRunGCDegradesOnOldAgentd`；真机 3 |
| 4 waiting_review 缓存仍在，可 continue | T1 `TestDoneOnRunningDoesNotPurgeCache`（非终态）；T2 扫描不含 waiting_review（`TestGCScannedCountsAllTerminalRows` 的 s4） |
| 5 设置页无清理按钮 | T4 `git diff 26e2ab7f -- web/` 空 |

---

## 9. 占位符扫描

本文件不含 TBD、「加适当的错误处理」、「同 Task N」、只描述不给代码的步骤。

自我声明的内部锁 / 不贴全量 harness 源码的测试：

- T1 helper 四支纯函数测试：入口不在声明缝，理由见 §2.3（空 ID 从缝构造不出）。
- T1/T2 使用既有 `newTestManager`、`mustCreateTask`、`mustDone`、`compensateOnlyManager`、`initTestRepo`、`gitT`、`gitOut`、`branchExists`、`newReclaimManager`、`newWorktree`、`seedTerminalTask`——这些函数已在 `manager_test.go` / `reclaim_test.go` / `workspace_test.go`，同包直接调用，不在计划里再贴一遍定义。T1 新写的 `writeCacheLeaves` / `seedTaskWithCache` / `assertGone` / `assertKeptFile` 正文已完整给出，T2 同包复用、不得再抄一份。新测试函数正文已完整给出。
- `TestRenderGCShowsFourStatuses` 直喂 `renderGC`：附加投影锁；缝级入口是 `runGC`。

未声明的骨架测试 = 本 plan 失败。审查时按此表勾。

---

## 10. 图覆盖债

- `codegraph` 不在 PATH；用 `go run github.com/Xsxdot/charter/graph/cmd/codegraph`。
- `sym` 拒 `file#Symbol`（`Manager.Done` 完整锚失败）。锚点合法性以 `resolve --doc` 为准。
- `flow Manager.GC` degraded（baseline 无 flows）。本卡不补 flows。
- `who-calls`/`chain` 报 `unscannedEntries=6`。空邻域 ≠ 无调用方。
- `compensateWorkspace` 图行 1051 vs 源码 1090。
- `validate --stale` / decl-domain 是 baseline 债，不归本卡。
- 实现后新符号 `purgeTaskCache` / `planTaskCacheLeaves` 在下次扫描前不在图里，属预期。

提交本 plan 前必须跑：

```bash
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --view cards-B298-charter resolve --doc docs/superpowers/plans/b298-plan.md
```

坏锚当场修。`moved` 可留（行号漂移）。

---

## 11. 实现者提交信息（按 task，不要 squash 进 plan 提交）

1. `feat(B298): add cache-leaf helper with occupancy and tmp-root guard`
2. `feat(B298): purge cache leaves on done, stop, and compensate`
3. `feat(B298): implement Manager.GC batch preview/execute and write JSON`
4. `feat(B298): render gc report and fail execute only on Failures`

T4 若只有测试：`test(B298): lock gc JSON absent-vs-zero through client and CLI`

禁止改 `web/`。禁止改 `TaskTmpDir`。禁止 `git rebase` 到 main。禁止 push（本 plan 节点也不 push）。
