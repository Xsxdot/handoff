# B249 契约增量：权限判据降噪

**上游状态：已批准**（源 spec：`docs/superpowers/specs/2026-08-25-b249-permission-noise-reduction.md`）
**级别：L3 轻档**
**冻结状态：本提交随 `codegraph/target.json` 与本分支视图 diff 一并冻结**

## 1. 现状查证与边界

本仓的架构形态是“按子系统分域，执行契约为执行域的窄缝入口；无 controller/service/dao 横向分层”。本卡只增加“协作控制 → 执行契约”的一条入口，不改变领域归属，也不让 `permgate` 依赖 `executor`。

已查证的现状签名如下；括号内是代码事实出处，行号只用于本次核对，符号锚是引用锚点：

| 现状签名 | 代码事实 | 本卡契约变化 |
| --- | --- | --- |
| `type Scope struct { Workdir string; TaskDir string }` | `internal/permgate/permgate.go#Scope`（70-73） | 增加 `TaskTmpDir string`，三根按同一 `InScope` 规则判定 |
| `func InScope(path string, scope Scope) (in bool, base string, err error)` | `internal/permgate/path.go#InScope`（39-69） | 签名不变；根集合由两根扩为 `Workdir`、`TaskDir`、`TaskTmpDir` |
| `func (g *Gate) Judge(req Request, scope Scope) Verdict` | `internal/permgate/permgate.go#Gate.Judge`（151-164） | 路由不变；仅 Bash 在落点通过后进入安全命令白名单 |
| `func (g *Gate) judgeBash(req Request, scope Scope) Verdict` | `internal/permgate/permgate.go#Gate.judgeBash`（216-253） | 必须保持“收集全部落点 → 范围判定 → 命令判定”的顺序 |
| `func (m *Manager) judgePermission(taskID string, ev executor.AdapterEvent) permgate.Verdict` | `internal/agentd/manager.go#Manager.judgePermission`（1825-1871） | `Scope.TaskTmpDir` 必须由执行契约查询组装，不得手拼路径 |
| `func (m *Manager) autoAllowPermission(taskID string, ev executor.AdapterEvent)` | `internal/agentd/manager.go#Manager.autoAllowPermission`（1898-1913） | 变为 `func (m *Manager) autoAllowPermission(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict)`；只对白名单 AutoAllow 追加审计事件 |
| `type EventType string` 与既有权限事件常量 | `internal/proto/proto.go#EventType`（39-124） | 新增独立 `permission_auto_allow`，不复用 `approver_decision` 或 `permission_reuse` |
| `func (s *Store) AppendEvent(taskID string, typ proto.EventType, payload any) (proto.Event, error)` | `internal/store/store.go#Store.AppendEvent`（741-762） | 作为审计事件唯一落库入口；不新增 HTTP/WS 端点 |
| `func isDeliverable(t proto.EventType) bool` | `internal/client/client.go#isDeliverable`（139-149） | 新事件加入不可交付集合；`all=true` 仍可读取完整审计流 |
| `func (a *Adapter) Start(ctx context.Context, req executor.StartReq) error` | `internal/executor/executor.go#Adapter`（208-231）；参数模型 `StartReq`（53-75） | 签名不变；四个适配器在启动环境追加任务专属临时目录变量 |
| `func (a *Adapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error)` | `internal/executor/resume.go#ResumeReq`（13-41）及 claude/grok/opencode 的 `Adapter.Resume` | 签名不变；Cold 重启路径追加同一任务专属临时目录变量 |

基线中的 Codex 私有路径规则已查证为 `internal/executor/codex/adapter.go#taskTmpDir`（129-132）：从 `TaskDir=<DataDir>/tasks/<id>` 向上两级取 `DataDir`，再拼 `<DataDir>/tmp/<id8>`；基线里的 `shortTaskID` 取前 8 个字节，不足则原样返回。Ticket 0 已将唯一计算迁入 `internal/executor/tempdir.go#TaskTmpDir`（11-17），当前 Codex 通过直通镜像调用它，金样本在 `internal/executor/tempdir_test.go#TestTaskTmpDirGoldenVectors` 冻结。

依赖行为也已钉死：Go `filepath.Join`（`/usr/local/go/src/path/filepath/path.go:123-131`）忽略空元素并 Clean；`filepath.Abs`（同文件 156-163）对相对路径接当前工作目录；`filepath.Rel`（176-206）跨卷或无法相对化返回错误。新查询只做 `Join`，不创建目录；范围判定沿用现有 `Abs`/`Rel` 失败即升级人工的 fail-closed 行为。

## 2. 执行契约：任务专属临时目录

新增唯一导出查询：

```go
// TaskTmpDir returns <dataDir>/tmp/<first eight bytes of taskID>.
// It performs no I/O and never creates the directory.
func TaskTmpDir(dataDir, taskID string) string
```

语义冻结：

1. `dataDir` 是 agentd 的 `Manager.cfg.DataDir`；`taskID` 是任务 ID。任务 ID 超过 8 字节时截前 8 字节，短 ID 原样保留，空 ID 交给 `filepath.Join` 的既有空元素语义。
2. 返回值必须在 `DataDir/tmp` 下，不得落到任务 worktree、`DataDir/tasks/<id>` 或系统共享 `/tmp`。
3. 查询是无 I/O、无错误返回的纯函数；目录创建仍由各 adapter 在启动/Cold 恢复路径负责。
4. 判据侧只消费该查询，不复制 `<DataDir>/tmp/<id8>` 规则。新增依赖方向为 `d_orchestration → d_execution`，入口登记为 `executor.TaskTmpDir`。

`Scope` 的冻结形状：

```go
type Scope struct {
	Workdir   string
	TaskDir   string
	TaskTmpDir string
}
```

`InScope` 保持精确签名不变，按 `[]string{scope.Workdir, scope.TaskDir, scope.TaskTmpDir}` 顺序遍历；空根跳过，路径归一化、软链最长已存在前缀解析、`filepath.Rel` 边界判断和返回的 `base` 语义均不变。`/tmp` 或 `/tmp/<executor>` 不加入集合。

`Manager.judgePermission` 的组装点冻结为：

```go
scope := permgate.Scope{
	Workdir:   task.Workdir(),
	TaskDir:   filepath.Join(m.cfg.DataDir, "tasks", taskID),
	TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, taskID),
}
```

其中前两项保持现状代码事实（`internal/agentd/manager.go#Manager.judgePermission` 1845-1848），第三项是本卡新增的唯一接线。`permgate` 保持纯计算边界；不新增对执行器运行态的依赖或调用（现有工具常量引用保持不变）。

四个执行器的 Start 与 Cold Resume 都必须在保留用户 `req.Env` 的前提下追加同一任务的：

```text
TMPDIR=<TaskTmpDir>
GOTMPDIR=<TaskTmpDir>
GOCACHE=<TaskTmpDir>/gocache
```

追加顺序必须是用户环境在前、受 handoff 管理的三项在后，使任务隔离变量不能被任务配置覆盖；Workdir/cwd 仍为任务仓库，不能用 `$PWD` 作为临时目录。现有 Codex 的 `tmpEnvKVs`（`internal/executor/codex/adapter.go#tmpEnvKVs`，138-143）是行为基准。其余三家改动只限 Start/Cold Resume 的环境接线，不改变 `StartReq`、`ResumeReq`、权限上报形状或适配器接口。

## 3. 判据契约：顺序、白名单与自指令

### 3.1 顺序与范围

`Gate.Judge` 的现有 fail-closed 路由保留：截断先 `Escalate`；write/edit 仍由路径和黑名单决定；非 Bash 仍不进入本卡命令白名单。Bash 的唯一允许顺序是：

1. 合并 `Request.Paths`、`RedirectTargets(req.Command)`、`WriteArgTargets(req.Command)` 的全部落点；
2. 丢弃设备按现有 `IsDiscardTarget` 规则跳过，其余落点逐个 `InScope`；任一路径归一化失败或越界立即 `Escalate`；
3. 只有所有落点通过后，才执行现有黑名单/自指令优先级和安全命令主体匹配。

这条顺序不是把 `/tmp` 放宽的变体：`/tmp` 共享目录仍必须 Escalate。`go test ./... > <TaskTmpDir>/out` 可因第三根通过；同命令重定向到 `/tmp/shared/out` 必须在命令白名单前 Escalate。

为使白名单不掩盖写入落点，`WriteArgTargets(s string) []string`（现状：`internal/permgate/writeargs.go#WriteArgTargets`，49-77）须增补以下可观测写入形态：`gofmt -w` 的文件参数、`git add` 的 pathspec、`git diff --output=<path>` 与 `git diff --output <path>`。无法提取的写入落点不得被白名单当作无落点放行；已有 tee/cp/mv/ln/install/dd 识别保持不变。

### 3.2 已知安全命令白名单

新增内部匹配器（不导出）：

```go
func safeCommandID(s string) (id string, ok bool)
```

它只接收完整 Bash 命令串，返回稳定审计 ID；匹配是 token/主体形态匹配，不是子串包含。命令含未许可的 `|`、`;`、换行、额外 `&` 或执行包装器时不命中；现有黑名单、自指令、wrapper/eval 判定优先于白名单。

允许的稳定 ID 与形态：

| ID | 精确主体形态 |
| --- | --- |
| `git-ledger-amend` | `git add <一个或多个 pathspec> && git commit --amend --no-edit`；`git add` 的每个 pathspec 先做落点判定 |
| `go-build` | `go build` 加任意普通参数 |
| `go-test` | `go test` 加任意普通参数 |
| `go-vet` | `go vet` 加任意普通参数 |
| `gofmt` | `gofmt`，可带 `-w`；`-w` 文件参数先做落点判定 |
| `npm-test` | `npm test` 加任意普通参数 |
| `npm-run` | `npm run <非空脚本名>` 加任意普通参数 |
| `make` | `make` 加任意普通参数 |
| `ls` | `ls` 加任意普通参数 |
| `cat` | `cat` 加任意普通参数 |
| `grep` | `grep` 加任意普通参数 |
| `git-status` | `git status` 加任意普通参数 |
| `git-diff` | `git diff` 加任意普通参数；输出文件落点仍须先通过范围判定 |
| `git-log` | `git log` 加任意普通参数 |

安全命令命中返回 `permgate.AutoAllow`，并设置 `Verdict.Rule = permgate.RuleSafeCommand`（新增常量值为 `safe-command`），`Verdict.Reason` 包含稳定 ID。写文件路径 AutoAllow 保持 `Rule == ""`，因此不会误记为白名单审计。

以下不是白名单：默认命令类出口、非 Bash 工具、未知 `handoff` 子命令、`/tmp` 共享落点、不能解析的写入选项、任意通过引号伪造主体的 `echo "go test"`。它们沿现有 Consult/Escalate 规则走，不做默认出口翻转。

### 3.3 handoff 自指令

在现有 `internal/permgate/selfcmd.go#selfCmdReadOnly`（28-31）的只读表加入 `graph`。因此 `handoff graph resolve --doc <文档>` 不再命中 `RuleSelfCommand`；未知子命令仍命中，且 `selfCmdMutating` 优先级不变，`handoff graph dispatch` 仍升级。`RuleSelfCommand = "self-command"` 的生产者是 `judgeSegment`（84-126），消费者是 `Manager.escalateLogLevel`（1882-1887），本卡不改其字面值。

## 4. 白名单 AutoAllow 审计事件

新增协议常量：

```go
const EventTypePermissionAutoAllow EventType = "permission_auto_allow"
```

新增 agentd 私有 payload：

```go
type permissionAutoAllowPayload struct {
	PermissionID string   `json:"permission_id"`
	Permission   string   `json:"permission"`
	Tool         string   `json:"tool"`
	Command      string   `json:"command,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	Rule         string   `json:"rule"`
	Reason       string   `json:"reason"`
}
```

事件生产与消费冻结：

1. `handlePermission`（`internal/agentd/manager.go#Manager.handlePermission`，1729-1761）先保存 `verdict := m.judgePermission(...)`，AutoAllow 分支调用新的 `autoAllowPermission(taskID, ev, verdict)`。
2. 仅 `verdict.Rule == permgate.RuleSafeCommand` 追加该事件；范围内 write/edit 的既有 AutoAllow 不产生此事件。`Permission` 采用现有 `permEventText` 的有界展示，`Tool/Command/Paths/Rule/Reason` 来自结构化请求与裁决；事件不创建 ticket、不改变任务状态、不调用 `m.hub.Publish`。追加失败只记 Error，仍尝试把 `once` 应答送回 executor；应答成功后才累计 `noteAutoAllowed`。
3. 通过 `m.st.AppendEvent(taskID, EventTypePermissionAutoAllow, payload)` 同步入库。事件 hook/ledger mirror 继续看到它；`internal/ledgermirror/mirror.go#mirrorSkip` 不增加此类型。
4. `client.isDeliverable` 将此类型加入不可交付集合，防止 WS replay 唤醒一次性 wait；`all=true` 仍交付排障所需全量事件。

既有 `EventTypeApproverDecision` 的生产方是审批者路径，`EventTypePermissionReuse` 的生产方是人工批准复用路径；两者已有各自消费者与语义，不能拿来承载静态白名单事件。

## 5. 可执行冻结与交棒测试

本节点已执行的可执行冻结：

- `executor.TaskTmpDir` 金样本：`/root/.handoff` + `137a7dc9-df89-4c1c-891e-ebe106c68b37` 必须得 `/root/.handoff/tmp/137a7dc9`；短 ID `T1` 与空 ID 也锁定 `filepath.Join` 语义。
- 本轮骨架命令：`go test ./internal/executor/...`，涵盖 executor、claudecode、codex、fake、grok、opencode、rawtap、turn，退出码 0。

下游实现必须新增并本轮跑过以下测试，不能以纸面声明替代：

1. `InScope`：TaskTmpDir 内部通过；`/tmp/shared`、仓库外和前缀相似路径不通过；三根命中 `base` 正确。
2. 白名单表驱动：全量 ID 命中；`echo "go test"`、额外 shell 连接符、越界重定向、越界 `gofmt -w`/`git add`/`git diff --output` 均不能 AutoAllow；Bash 落点检查先于 matcher。
3. `handoff graph resolve --doc` 只读不命中，未知和变更类自指令仍命中。
4. 审计事件：白名单 AutoAllow 一次且只一次写入 `permission_auto_allow`；范围写入不写该事件；事件不建 ticket、不 Publish；客户端 replay 过滤但 `all=true` 可见；AppendEvent 失败仍回传 `once`。
5. 四执行器 Start 与 Cold Resume：三项临时环境都指向同一个 `TaskTmpDir`，用户同名变量不能覆盖 handoff 值，且临时目录不在 worktree 内。

L3 轻档不执行跨子系统直通竖切；本节点只落契约查询的 Ticket 0 与金样本，以上其余行为作为实现节点的最薄路径测试条目。

## 6. 三重闸门拍板记录

命中三重闸门的决定只有以下三项：

1. **任务临时目录作为第三合法根，拒绝把共享 `/tmp` 纳入范围。** 这是跨执行器、Manager、permgate 的难逆契约；没有本文上下文时把“第三根在沙箱内却不在判据内”修掉是自然反应；被否方案是把整个 `/tmp` 放入范围，但会放大并发任务互相覆盖风险，且与 Codex 已有 `excludeSlashTmp`/`excludeTmpdirEnvVar` 决定冲突。不做共享 `/tmp` 放宽。
2. **采用可枚举的正向安全命令白名单，拒绝默认命令出口整体 AutoAllow。** 这是判据行为与审计链的难逆变化；后人看到大量 Consult 下降时会自然想继续翻转默认出口；被否方案是负向默认放行，但执行器换版本后不可枚举、三天取证不足以支撑它。不做默认出口翻转。
3. **白名单审计事件只入库、可镜像、不可交付且不 Publish。** 这是 Store、client、mirror、manager 的跨组件事件语义；没有上下文时把新事件当普通事件 Publish 会造成 replay/wait 收权竞争；被否方案是复用 `approver_decision` 或直接 Publish，前者污染既有生产/消费语义，后者会重新制造唤醒噪声。不做工单、不做实时 Publish。

## 7. 本节点欠账

无“已实现但零测试”的欠账：Ticket 0 仅实现 `TaskTmpDir` 纯查询与 Codex 直通镜像，且已有本轮金样本和整包测试证据。范围第三根、白名单、审计事件、`graph` 自指令和另外三个执行器环境接线均尚未在本节点实现，按上节测试清单交给后续实现节点；不得将它们描述为当前已生效行为。

## 8. Breakdown 轮边界澄清修订记录

- 2026-08-25：拆解核对澄清：本轮只消费已冻结的 `d_orchestration → d_execution` / `executor.TaskTmpDir` 接缝；`permgate` 仍是 `d_policy` 内的纯判据，`internal/store` 的既有 `AppendEvent` 属编排域，`ledgermirror` 只是既有事件消费验证点，不新增域间入口。此记录不改变第 1–6 节冻结语义。
- 2026-08-25：拆解核对澄清：任务临时目录的 AF_UNIX 字节预算属于实现注释的权威知识搬家，不是新 API 或新接缝；完整中文账必须归 `internal/executor/tempdir.go#TaskTmpDir`，Codex 直通镜像不得成为唯一知识源。此记录不改变 `TaskTmpDir` 的签名与路径语义。
- 2026-08-25：拆解核对澄清：B248 先合入是实现轮的安全基线前置，不是 B249 契约增量；若实现轮开工前实查已含 `execWrapperRx` 放宽则跳过合并，否则不得在 B249 放宽白名单的中间态落地。此记录不改变本卡的 out-of-scope 黑名单规则。
