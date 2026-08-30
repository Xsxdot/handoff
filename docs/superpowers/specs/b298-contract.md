# B298 终态任务释放构建缓存，并提供机器级清理命令：契约冻结

状态：**已冻结**（2026-08-29，提交 26e2ab7fb5；上游 spec `docs/superpowers/specs/b298.md` 2026-08-29 已批准。拆解期仅允许头部状态元数据与 §8 修订记录追加，冻结正文不改——b229 先例）

本文件是 B298 的契约增量与 Ticket 0 冻结物。冻结随本提交落盘：

- 目标图：`codegraph/target.json`
- 本分支视图：`codegraph/diffs/cards-B298-charter.json`
- 节点台账：`docs/superpowers/ledgers/2026-08-29-b298-contract-ledger.md`

架构形态：**按子系统分域的平铺领域包，无横向 controller/service/dao 分层**。本卡不新增领域，不改变 `best.json` 的结构树；新增接缝归入既有 `d_cli`、`d_gateway`、`d_transport`（其 `d_transport_channel` 子域）、`d_orchestration` 与 `d_protocol`。

## 1. 现状签名查证

以下是本轮对当前工作树的代码事实核对；行号是本轮写文档时的现状行号，符号锚用于行号漂移后的复核。

| 接缝 | 当前签名/事实 | 现状代码出处 |
|---|---|---|
| 归档收口 | `func (m *Manager) Done(ctx context.Context, taskID, note string) (err error)` | `internal/agentd/manager.go#Manager.Done`（第 1387 行） |
| 主动中止收口 | `func (m *Manager) Stop(ctx context.Context, taskID string) (worktreeRemoved bool, err error)` | `internal/agentd/manager.go#Manager.Stop`（第 1502 行） |
| 派发失败补偿 | `func (m *Manager) compensateWorkspace(ctx context.Context, taskID string, repo string, ws Workspace)` | `internal/agentd/manager.go#Manager.compensateWorkspace`（第 1090 行）；当前图节点显示旧行 `1051`，属图保鲜债 |
| 活动缓存路径 | `func TaskTmpDir(dataDir, taskID string) string` | `internal/executor/tempdir.go#TaskTmpDir`（第 18 行） |
| 单任务工作树回收 | `func (m *Manager) Reclaim(ctx context.Context, taskID string, force bool) (resp *proto.ReclaimResp, err error)` | `internal/agentd/reclaim.go#Manager.Reclaim`（第 251 行） |
| 批量残树列表 | `func (m *Manager) ReclaimList() (*proto.ReclaimListResp, error)` | `internal/agentd/reclaim.go#Manager.ReclaimList`（第 342 行） |
| 现有 client 基础拨号 | `func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error)` | `internal/client/client.go#Client.do`（第 399 行） |
| 现有列表 client | `func (c *Client) ReclaimList(ctx context.Context) (*proto.ReclaimListResp, error)` | `internal/client/client.go#Client.ReclaimList`（第 573 行） |
| 现有单树 client | `func (c *Client) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)` | `internal/client/client.go#Client.Reclaim`（第 610 行） |
| 现有列表 HTTP | `func (s *Server) handleReclaimList(w http.ResponseWriter, r *http.Request)` | `internal/agentd/server.go#Server.handleReclaimList`（第 805 行） |
| 现有单树 HTTP | `func (s *Server) handleReclaim(w http.ResponseWriter, r *http.Request)` | `internal/agentd/server.go#Server.handleReclaim`（第 828 行） |
| 现有 CLI 目标入口 | `func newTargetClient() (*client.Client, func(), error)` 与 `func TargetEndpoint() (addr, token string, err error)` | `cmd/root.go#newTargetClient`（第 237 行）、`cmd/root.go#TargetEndpoint`（第 197 行） |

当前 `TaskTmpDir` 对 UUID 取前 8 个字节，对短 ID 原样保留；空 ID 经 `filepath.Join` 得到 `<DataDir>/tmp` 根目录。此形状由 `internal/executor/tempdir_test.go#TestTaskTmpDirGoldenVectors`（第 9 行）的三组向量锁定，本卡不改该函数。

现有 `proto.TaskState.IsTerminal` 只把 `completed` 与 `failed` 判为终态，`waiting_review` 不是终态；因此 B298 的终态扫描必须复用该事实，不得自行维护第三份状态名单。

## 2. 冻结的新增签名与 wire

### 2.1 协议类型

文件：`internal/proto/gc.go`

```go
type GCRequest struct {
	Force bool `json:"force"`
}

type GCItemStatus string

const (
	GCItemPlanned GCItemStatus = "planned"
	GCItemDeleted GCItemStatus = "deleted"
	GCItemSkipped GCItemStatus = "skipped"
	GCItemFailed  GCItemStatus = "failed"
)

type GCCacheRow struct {
	TaskID string        `json:"task_id"`
	Path   string        `json:"path"`
	Bytes  int64         `json:"bytes"`
	Status GCItemStatus `json:"status"`
	Error  string        `json:"error,omitempty"`
}

type GCWorktreeRow struct {
	TaskID     string        `json:"task_id"`
	Name       string        `json:"name"`
	State      string        `json:"state"`
	Branch     string        `json:"branch"`
	WorkDir    string        `json:"work_dir"`
	Worktree   WorktreeState `json:"worktree"`
	DirtyCount int           `json:"dirty_count"`
	Note       string        `json:"note,omitempty"`
	Status     GCItemStatus  `json:"status"`
	Error      string        `json:"error,omitempty"`
}

type GCResp struct {
	Preview         bool            `json:"preview"`
	Force           bool            `json:"force"`
	ReleasableBytes *int64          `json:"releasable_bytes,omitempty"`
	CacheRows       []GCCacheRow    `json:"cache_rows"`
	WorktreeRows    []GCWorktreeRow `json:"worktree_rows"`
	Scanned         int             `json:"scanned"`
	Failures        int             `json:"failures"`
}
```

现状出处：`internal/proto/gc.go#GCRequest`（第 16 行）、`internal/proto/gc.go#GCItemStatus`（第 21 行）、`internal/proto/gc.go#GCCacheRow`（第 35 行）、`internal/proto/gc.go#GCWorktreeRow`（第 47 行）、`internal/proto/gc.go#GCResp`（第 64 行）。上述字段是本轮新增的可编译 DTO；`WorktreeState` 直接复用 `internal/proto/reclaim.go#WorktreeState` 的既有状态名，不重新声明一套残树状态。

原子 wire 断言：

1. `GCRequest` 的 POST 请求体只有 `force` 布尔字段。
2. GET 预览的 `force` 通过 `/api/gc?force=true` 表达；HTTP 方法表达 preview/execute，不由请求体伪造执行动作。
3. `GCResp.preview=true` 表示预览。
4. `GCResp.preview=false` 表示执行响应。
5. `GCResp.force` 回显本次 force 选择。
6. `GCCacheRow.task_id` 标识计算该缓存叶子的终态任务。
7. `GCCacheRow.path` 只承载被报告的 cache leaf 路径。
8. `GCCacheRow.bytes` 使用 `int64` 表示该叶子的字节量。
9. `GCWorktreeRow` 显式展开既有 reclaim 行字段，不依赖 Go 匿名字段 JSON 扁平化。
10. `GCResp.releasable_bytes` 缺席表示尚未取得可计算结果。
11. `GCResp.releasable_bytes=0` 表示已计算且确认没有可释放字节；缺席与零必须可区分。
12. `GCResp.cache_rows` 报告缓存叶子，`worktree_rows` 报告残留 managed worktree。
13. `GCResp.failures` 统计本应删除但失败的条目，不能把 skip 计为失败。

可执行冻结：`internal/proto/gc_test.go#TestGCGoldenJSON`（第 17 行）已锁定请求 JSON 为 `{"force":true}`、最小预览 JSON 及 `releasable_bytes` 的显式零/缺席差异。本轮已运行该测试并通过。

### 2.2 agentd 编排与 HTTP

新增签名：

```go
func (m *Manager) GC(ctx context.Context, force, execute bool) (resp *proto.GCResp, err error)
func (s *Server) handleGC(w http.ResponseWriter, r *http.Request)
```

现状出处：`internal/agentd/gc.go#Manager.GC`（第 36 行）、`internal/agentd/gc.go#Server.handleGC`（第 52 行）。`internal/agentd/server.go#Server.Handler`（第 497-498 行）已登记 `GET /api/gc` 与 `POST /api/gc`，两者继续位于 `Server.Handler` 的既有 `auth` 包裹下。

原子接缝断言：

14. `Manager.GC` 的 `ctx` 是请求生命周期，清理与 reclaim 调用必须透传它。
15. `Manager.GC(force=false, execute=false)` 是只读预览。
16. `Manager.GC(force=true, execute=false)` 仍是只读预览，只改变脏 managed worktree 的报告处置。
17. `Manager.GC(force=false, execute=true)` 执行缓存与残树动作，但缓存删除条件不读取 force。
18. `Manager.GC(force=true, execute=true)` 仅额外允许按 reclaim 语义强删脏 managed worktree。
19. `GET /api/gc` 只调用 `execute=false`。
20. `POST /api/gc` 只调用 `execute=true`。
21. `GET /api/gc` 与 `POST /api/gc` 使用与 `/api/reclaim` 相同的既有 Bearer/cookie 鉴权中间件，不开未鉴权表面。
22. B298 的资源动作不得改变任务状态、追加状态迁移事件、删除 SQLite 行、删除任务分支或任务目录。
23. B298 的资源动作不得删除用户自建 worktree、`repos/` 或 `agentd.log`。

Ticket 0 当前仅提供签名、请求解码接线和 503 空壳：`internal/agentd/gc.go#ErrGCUnwired`（第 22 行）。`internal/agentd/gc_test.go#TestHandleGCTicket0`（第 21 行）已跑通，保证空壳不会动盘；实现节点必须把该测试推进为真实资源断言，而不能保留“已接线但 503”的假绿。

### 2.3 client

新增签名：

```go
var ErrGCUnsupported = errors.New("对端 agentd 不支持 gc")

func (c *Client) GCPreview(ctx context.Context, force bool) (*proto.GCResp, error)
func (c *Client) GC(ctx context.Context, force bool) (*proto.GCResp, error)
```

现状出处：`internal/client/gc.go#ErrGCUnsupported`（第 23 行）、`internal/client/gc.go#Client.GCPreview`（第 35 行）、`internal/client/gc.go#Client.GC`（第 75 行）。两方法复用 `internal/client/client.go#Client.do`（第 399 行）的 Bearer、请求上下文、JSON body 与既有 `httpError`；不在 client 侧做 DataDir 或字节计算。

原子接缝断言：

24. `Client.GCPreview` 只发 GET `/api/gc`，force 为 true 时追加 `?force=true`。
25. `Client.GC` 只发 POST `/api/gc`，请求体使用 `proto.GCRequest{Force: force}`，不能传会被再次 `json.Marshal` 成 `{}` 的预编码 reader。
26. `Client.GCPreview` 收到 200 时解码 `proto.GCResp`。
27. `Client.GC` 收到 200 时解码 `proto.GCResp`。
28. `Client.GCPreview` 收到 404 返回 `ErrGCUnsupported`。
29. `Client.GC` 收到 POST 404 时再探测 GET `/api/gc`；只有探测也 404 才返回 `ErrGCUnsupported`。
30. POST 404 后探测 GET 非 404 时，保留 POST 404 的真实错误，不误判为旧 agentd。
31. 连接错误、401、5xx 与非法 JSON 仍是错误，不转换成“过旧”。

`internal/client/gc_test.go#TestGCPostDouble404IsUnsupported`（第 20 行）本轮已运行并通过，锁定 POST 后强制 GET 探测顺序与请求路径。

### 2.4 CLI

新增签名/命令：

```go
var gcCmd = &cobra.Command{Use: "gc"}
func runGC(cmd *cobra.Command, cl *client.Client, addr string) error
func renderGC(w io.Writer, resp *proto.GCResp)
```

现状出处：`cmd/gc.go#gcCmd`（第 30 行）、`cmd/gc.go#runGC`（第 57 行）、`cmd/gc.go#renderGC`（第 95 行）。命令通过既有 `cmd/root.go#newTargetClient`（第 237 行）获取目标 client；`--target` 不新造解析逻辑。

原子接缝断言：

32. `handoff gc` 不接受位置参数。
33. `handoff gc` 默认 preview。
34. `handoff gc --force` 仍是 preview。
35. `handoff gc --yes` 才选择 client execute。
36. `handoff gc --yes --force` 选择 execute 且 force 透传。
37. `--json` 输出完整 `proto.GCResp`，包括 `releasable_bytes` 的缺席/零区别。
38. client 返回 `ErrGCUnsupported` 时，CLI 输出含“过旧”、建议升级后再跑 gc，并退出 0；`cmd/gc_test.go#TestRunGCDegradesOnOldAgentd`（第 26 行）锁定该降级路径。
39. preview 列表正常取得时，即使有 skip 或待清理项也退出 0；拿不到列表才返回错误。
40. execute 仅在报告中存在本应删除但失败的条目时返回非零；dirty/unknown/non-managed skip 不单独造成失败。
41. `--target` 与 reclaim 使用同一 `newTargetClient`/TargetEndpoint 目标选择语义。

## 3. 资源清理语义（实现节点逐条对账）

### 3.1 结束收口

以下三处是三个独立调用点，不能只改其中一个：

42. `Manager.Done` 在现有 managed worktree 清理之后尝试清理该任务缓存。
43. `Manager.Stop` 在现有 managed worktree 清理之后尝试清理该任务缓存。
44. `Manager.compensateWorkspace` 在派发失败补偿收口时尝试清理该任务缓存。
45. 三处均计算活动 leaf `executor.TaskTmpDir(m.cfg.DataDir, taskID)`。
46. 三处均计算遗留 leaf `filepath.Join(m.cfg.DataDir, "tasks", taskID, "tmp")`。
47. 两个 leaf 只在路径存在时执行 `RemoveAll`；不存在按幂等成功处理。
48. 任何删除目标等于 `filepath.Join(DataDir, "tmp")` 根目录时必须拒绝，不能调用删除动作。
49. 活动 leaf 的短号碰撞检查排除正在进入终态的当前任务自身。
50. 若同一前 8 字节短号存在任一其他非终态任务，活动 leaf 保留。
51. 遗留完整任务 ID 路径不受短号碰撞规则影响。
52. 缓存删除失败只记录带任务/路径/原因上下文的日志，不阻断归档。
53. 缓存删除失败只记录带任务/路径/原因上下文的日志，不阻断 stop。
54. 缓存删除失败只记录带任务/路径/原因上下文的日志，不阻断派发失败补偿。
55. `render.log`、`frames.jsonl`、`proc.json` 保留。
56. 任务目录本身、任务分支与 SQLite 行保留。
57. 非终态任务（含 `waiting_review`）不进入结束缓存删除集合。
58. 结束路径清理失败的历史重试入口是 gc，不通过重复发送已短路的 done。

### 3.2 机器级批处理

59. 批处理只从任务表终态行计算两处缓存 leaf，不扫描无任务行的孤儿目录。
60. 对计算出的路径去重；多个终态任务共用同一活动 leaf 时只报告、计数、删除一次。
61. 预览 `releasable_bytes` 按去重后的将删除缓存路径求和。
62. preview 不调用会改变磁盘的删除动作。
63. execute 在动作前重新读取并判定任务快照，不复用上一轮 preview 快照。
64. preview 与 execute 之间新变成终态的任务，本次 execute 可进入集合。
65. preview 与 execute 之间变成 running 的任务，本次 execute 不进入集合。
66. 残留 managed worktree 的净、prunable 行复用既有 `Manager.Reclaim`/`Manager.ReclaimList` 判定语义，不另造 dirty 规则。
67. dirty managed worktree 无 force 时报告 skip，不阻断其他缓存或工作树行。
68. dirty managed worktree 有 force 时按 reclaim 的强删语义处理。
69. unknown、non-managed 与非终态工作树报告 skip，不阻断其他行。
70. 缓存 `RemoveAll` 失败在 human 与 JSON 报告中均以 failed 行出现，不能只写 `agentd.log`。
71. 单个工作树失败或 skip 不阻断其余缓存叶子与其余工作树。
72. skip 不计入 `Failures`；本应删除却失败才计入 `Failures`。

## 4. 依赖库行为契约

以下不是凭印象的实现建议，而是本轮读取 Go 1.26.1 源码后的依赖事实；实现不得依赖与这些行为相反的假设。

1. `filepath.Join` 忽略空元素并清理结果：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/path/filepath/path.go:123-131`。因此 `TaskTmpDir(dataDir, "")` 得到 `<DataDir>/tmp`，根目录保护必须在业务层显式执行。
2. `filepath.WalkDir` 包含 root、按字典序遍历且不跟随 symbolic links：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/path/filepath/path.go:381-407`。若实现节点为计算普通文件字节而遍历目录，不得因 symlink 跟随越出任务 leaf。
3. `os.RemoveAll` 对空路径为兼容性直接返回 nil，先 Remove，目标不存在也返回 nil：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/os/removeall_at.go:15-38`。因此不能把 RemoveAll 的 nil 当作“目标有效”，也不能把空路径保护交给库。
4. `RemoveAll` 递归删除子项并返回首个错误：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/os/removeall_at.go:66-160`；实现节点必须保留失败报告，不能只以最终目录状态推断成功。
5. RemoveAll 的 `openDirAt` 把目录 symlink 视为非目录，不进入其目标：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/os/removeall_at.go:163-175`。
6. `fs.DirEntry.Info` 在目录项已被删除或改名时可能返回 `errors.Is(err, fs.ErrNotExist)`，symlink 的 Info 描述链接本身：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/io/fs/fs.go:116-122`。
7. `fs.FileInfo.Size` 对普通文件是字节长度，对其他类型由系统决定：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/io/fs/fs.go:167-175`；字节统计只把普通文件尺寸计入。
8. `os.Stat` 跟随 symlink，`os.Lstat` 不跟随：`/opt/homebrew/Cellar/go/1.26.1/libexec/src/os/stat.go:9-27`。路径存在性与字节计算必须明确选择语义，不能混用后声称安全。

## 5. 三重闸门拍板记录

### 记录 A：新增独立 `/api/gc`，而非复用 reclaim 名称或让 CLI 循环单任务 reclaim

这是跨 `d_cli`、`d_transport_channel`、`d_gateway`、`d_coordination_task`、`d_protocol` 的难逆 wire/组装决定；后人看到“清理工作树已有 reclaim”会自然想把缓存动作塞入旧端点；取舍是新增一个鉴权端点与统一批处理接缝，换取同一机器上的缓存字节预览、缓存失败报告和单次重算。否掉的方案：只做命令、只在结束收口删除、把缓存删除塞进 reclaim、CLI 逐个 POST reclaim。明确不做：在本卡另建无鉴权清理表面。

### 记录 B：活动缓存按任务 ID 前 8 字节共享 leaf，但有其他非终态占用者就保留

这是由既有 `TaskTmpDir` 路径形状承载的跨执行器资源决定，改成完整 ID 会同时动 adapter、策略范围与历史目录；后人可能想“终态就直接删”而忽略仍运行任务共享 leaf；取舍是保留磁盘占用的少数碰撞 leaf，换取绝不误删进行中 executor 的 `TMPDIR/GOTMPDIR/GOCACHE`。否掉的方案：清空整个 `<DataDir>/tmp`、扫描并删除孤儿短号、无条件删除终态任务的活动 leaf。明确不做：本卡改路径形状或清理无任务行孤儿目录。

### 记录 C：preview/execute 共用判定语义，但 execute 在动作前重读快照

这是跨 CLI、client、agentd 与批处理实现的难逆一致性决定；后人可能想把 preview 做成缓存快照或让 execute 复用上次结果；取舍是执行前多一次读取与报告可能变化，换取不因旧预览误删后来变成 running 的任务。否掉的方案：共享上一轮预览快照、preview 直接执行、单独维护两套候选集合。明确不做：持久化 gc 预览快照。

### 记录 D：`releasable_bytes` 用 `*int64` 区分缺席与零

这是跨进程 JSON wire 的难逆字段类型决定；后人看到字节统计常会用 `int64` 零值，导致“没算出来”和“算出来是零”无法区分；取舍是调用方多处理一个指针，换取批量报告不会以假零掩盖未计算结果。否掉的方案：复用没有字节字段的 `ReclaimListResp`、使用无指针 `int64`、缺席时填零。明确不做：另开只返回字节的探测端点。

### 记录 E：现有收口清 managed worktree 后再删缓存 leaf

这是收口步骤顺序的难逆运行时决定：executor 后代可能仍以 worktree 为 cwd，先删缓存不会替代工作树清理；后人看到两项都是 best-effort 可能会顺手合并或反转顺序；取舍是多一次独立路径计算，换取保留现有 worktree 清理的失败/提示语义。否掉的方案：把缓存删除移到状态迁移前、与 worktree 删除并行、用 gc 替代结束收口。明确不做：因缓存删除失败回滚归档/stop/补偿。

## 6. 骨架、图与已知债项

### Ticket 0

本轮新增 `internal/proto/gc.go`、`internal/agentd/gc.go`、`internal/client/gc.go`、`cmd/gc.go` 与对应契约测试；只落签名、DTO、路由、client/CLI 透传镜像及可观测的 503 空壳，不落最终删除行为。新增文件均有职责/边界头注释，导出函数有注释，agentd/client 关键进入、错误、退出分支有结构化日志。

本轮编译/测试证据写入 `docs/superpowers/ledgers/2026-08-29-b298-contract-ledger.md`。全包测试已进入测试阶段但退出 1，失败原文已原样记账；契约金样本、Ticket 0 空壳与 CLI 降级/渲染定向测试本轮退出 0。

### 目标图与视图

`codegraph/target.json` 本轮新增以下允许契约面，legacy budget 保持现状值；图契约入口按现有 container label 记录，不把单个方法名伪装成独立 container：

- `d_cli → d_transport`：沿用既有 `client.Client` container，覆盖 `Client.GCPreview` 与 `Client.GC`。
- `d_cli → d_protocol`：沿用既有 `proto 实体` container。
- `d_gateway → d_orchestration`：新增 `agentd.Manager` container 入口，覆盖 `Manager.GC`。
- `d_gateway → d_protocol`：沿用既有 `proto 实体` container。
- `d_orchestration → d_protocol`：沿用既有 `proto 实体` 与 `proto.Task` container。

`codegraph/diffs/cards-B298-charter.json` 随骨架记录新增 DTO、Manager/Server/client/CLI 符号和本轮代码边。`best.json` 不变。

### 图覆盖债

本轮对 `codegraph sym internal/agentd/manager.go#Manager.Done` 的真实查询返回“符号不在图中（图未覆盖或名字有误）”；用节点 id 查询 `m_agentd_Manager` 与 `n_agentd_Manager_ReclaimList` 才能命中，且后者显示旧行号。`codegraph validate --repo . --stale` 本轮退出 1，原始输出含 `[decl d_cli] 领域 d_cli 不在图 domains 段中`、`[decl d_execution_contract] 领域 d_execution_contract 不在图 domains 段中` 与 stale 节点；`codegraph check --repo . --stale` 本轮退出 0 但仍有既有 warnings。故本契约以符号锚和本分支视图 diff 为准，不把当前 baseline 的 stale 当成新增 B298 失败。

### 移交 plan 附区（不计冻结条目）

以下是查证期交给 plan/implement 的实现级约束，不是第二份冻结清单：

- 收口删除 helper 应接在三个已点名收口内，不把 `compensateWorkspace` 漏在只改 Done/Stop 的路径外。
- 活动路径调用现有 `executor.TaskTmpDir`；旧路径严格按完整 task ID 组装；先做根目录等值保护，再做存在性/去重/删除。
- 短号占用者从 `store.Store.ListTasks` 的同一任务快照计算，非终态才占用；自己不计入。不要复用 `ActiveTasksByWorkDir`，它按 workdir 而非短号表达占用。
- gc 的残树批处理复用既有 reclaim 分类和 `WorkspaceGitTimeout`；不要从 CLI 逐个调用 `/api/tasks/{id}/reclaim`。
- 字节统计采用普通文件大小并对 leaf 去重；并发删除看到 `ErrNotExist` 时应保持幂等，但真实 RemoveAll 首错仍要进入报告。
- 实现节点吸收本附区后，在 plan 文档头标注“已由 plan〈文档〉吸收（日期）”并销区；若实现选择发生契约变化，退回 spec，不在 plan 内顺手改签名。

## 7. 节点交棒

交棒：`breakdown`。

欠账：

- 全包 `go test ./internal/proto ./internal/client ./internal/agentd ./cmd` 本轮退出 1，失败原文见节点台账；定向契约测试与编译阶段已通过，但本节点不把既有全包失败归因或隐瞒。
- 最终缓存删除、短号碰撞、三处收口接线、gc 批处理与 CLI 退出码尚未实现，交由 implement；Ticket 0 的 `ErrGCUnwired` 及其 503 测试必须被替换。
- 图 baseline 的既有 stale/decl-domain warnings 未由本卡修复；本分支 view diff 与 target.json 已落盘冻结。

无命中：无其他需要在本节点拍板的三重闸门决定。

## 8. 拆解期修订记录（breakdown 节点，2026-08-30 出稿）

以下为拆解核对期做出的边界澄清，依 b229 先例（「拆解期仅允许头部状态元数据与 §8 修订记录追加」）回写于此；冻结正文（§1–§7）未改动一字。

- C-0 头部状态位修正：原头部只写「已批准」（指上游 spec 的批准），缺本文件自身的冻结标记，冻结状态曾只活在会话记忆与提交信息里；已改为「已冻结（2026-08-29，提交 26e2ab7fb5）」。
- C-1 边界澄清（面归属）：收口 helper 调 `executor.TaskTmpDir` 走的是 target.json 已在册的 `d_orchestration → d_execution` 契约面（contracts[22]，manager.go 现状即 import `internal/executor`），不属契约增量；`internal/executor` 的 `TaskTmpDir` 是包内 API 复用，不是本卡新契约面。
- C-2 边界澄清（收口顺序的补全）：断言 42/43/44 的「在现有 managed worktree 清理之后」指收口流程中工作树处置**尝试**之后（不论成败）——工作树清理失败不豁免缓存叶子删除尝试。现状 `Manager.Done`/`Manager.Stop` 的工作树清理失败本就不提前返回；`compensateWorkspace` 的失败分支有提前 return，implement 不得让缓存删除被这些 return 截走。记录 E 只钉了先后顺序，本条补「不被失败路径抑制」；spec 测试决定 1 的「补偿路径漏接必须能被测试抓住」按本条口径出题。
- C-3 边界澄清（附区吸收落点）：~~§6 移交 plan 附区的「plan 文档头标注吸收」义务，因本卡无独立 plan 节点（spec 路由 contract → breakdown →（单轮）implement），落点改为 implement 卡的执行文档头。~~ **（前提有误，已由 C-4 更正作废）**
- C-4 更正（2026-08-30 拍板）：C-3 前提有误——账本 charter v9 流含 plan 列（`breakdown.next=plan`），plan 为派发列、法定产出 `docs/superpowers/plans/b298-plan.md`，implement 门验 plan/breakdown 附件。本卡 L3 轻档仍走 plan 节点；§6 附区吸收标注落点回归 **plan 文档头**。
- 已拍板（2026-08-30，协调者）：`GCResp.scanned` 语义取「本轮判定读过的任务表终态任务行数」（对齐 `ReclaimListResp.scanned` 既有语义，拆解稿岔口 1 候选 (a)）；implement 落实现时以测试钉死，本条由「待拍板」转「已拍板」。
