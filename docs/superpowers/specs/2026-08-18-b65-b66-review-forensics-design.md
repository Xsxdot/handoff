# B65 + B66：审阅取证的两处失真（diff 基准 / run 引号）

> 状态：设计定稿，待实现
> 覆盖 backlog 的 **B65** 与 **B66**（两条同属「协调者审阅取证」，共用本份 spec，
> 与 B74/B75 共用一份是同一先例）

## 1. 背景与目标

协调者审一个派发任务时，取证只有三条只读命令：`handoff diff` / `fetch` / `run`。
其中两条现在会**静默给出错误结果**——不是报错，是看起来正常但内容不对。两处都在
2026-08-18 审 B129 交付时真实咬到，各浪费了一轮取证。

| | 现象 | 根因 |
|---|---|---|
| **B65** | `handoff diff <task>` 不带 `--base` 时吐出一堆与本任务无关的文件 | `handleTaskDiff` 缺省走 `resolveBaseBranch`（`origin/HEAD` → `main` → `master`），而任务记录里**本来就有** `Task.BaseCommit`（该任务分支的实际起点），完全没被用上 |
| **B66** | `handoff run <task> sh -c "go build ./... && go vet ./..."` 退出码 2、打印 go 的用法帮助 | `cmd/run.go:31` 的 `strings.Join(args[1:], " ")` 把本地 shell 已拆好、已剥掉引号的 token 重拼成一个字符串，服务端再 `sh -c` 拆第二遍，token 边界在中间那步就没了 |

**目标**：

1. `handoff diff` 缺省基准取任务自己的起点，且**把实际用的基准回显给协调者**——
   今天咬人的是「静默用错基准」，不是「基准错」；只改缺省而不回显，下次换一种
   失真仍然静默。
2. `handoff run` 的参数原样到达远端，引号与转义不丢。
3. Web 控制台的基准下拉与 CLI 保持同一套解析，不留新的两边不一致。

**非目标**（明确不做，见 §7）：不改服务端 `sh -c` 语义、不改 `handoff run` 的
flag 顺序约束、不为老 agentd 补协议降级。

## 2. B65：diff 缺省基准取任务起点

### 2.1 解析顺序

`handleTaskDiff` 的基准解析改成四级：

| 级 | 来源 | `base_source` |
|---|---|---|
| 1 | `?base=` 查询参数（CLI 的 `--base`、控制台下拉选定项） | `explicit` |
| 2 | `Task.BaseCommit`，且能在任务仓库里验证存在 | `task_base` |
| 3 | `resolveBaseBranch(repo)`（今天的行为，保留为兜底） | `repo_default` |
| 4 | 都取不到 → 400「无法确定基准分支，请用 base 参数指定」 | 不产出 |

第 2 级**必须验证存在性**再采用。`BaseCommit` 是派发当时落库的 40 位 sha，之后
可能不可达：起点所在分支被删且已 gc、任务仓库被重新 clone、`--no-sync-check`
派发到了另一个仓库。验不过就带 Warn 落到第 3 级，比让 `git diff` 抛一个 500 强。

`Task.BaseCommit` 为空是**正常情况**（切已存在分支时没有「起点」这回事，以及该
列后加、老任务不回填），直接落第 3 级，不告警。

### 2.2 新函数

在 `internal/agentd/workspace.go` 新增，与 `resolveBaseBranch` 相邻：

```go
// diff 基准的来源标识，随 diff/branches 响应回传给协调者。
const (
	DiffBaseExplicit    = "explicit"     // 调用方显式指定
	DiffBaseTaskStart   = "task_base"    // 任务分支的实际起点（Task.BaseCommit）
	DiffBaseRepoDefault = "repo_default" // 仓库默认分支推导
)

// ResolveDiffBase 确定 diff 的缺省基准（调用方未显式指定时用）。
//
// 参数：
//   - repo: 任务工作区目录
//   - baseCommit: 任务记录里的 Task.BaseCommit，可为空
//
// 返回：基准 rev 与其来源；两者同时为空表示推导不出（由路由层报 400）。
func ResolveDiffBase(repo, baseCommit string) (base, source string)
```

`ResolveDiffBase` 只产出 `DiffBaseTaskStart` / `DiffBaseRepoDefault` / 空三种
source——`DiffBaseExplicit` 由路由层在「调用方给了 `?base=`、根本没进解析函数」
这条路径上直接填。三个常量放在一起是因为它们是同一套词汇表，会一起出现在
diff 与 branches 两个响应里。

实现要点：

- `baseCommit` 非空且**不以 `-` 开头**时，用
  `git rev-parse --verify --quiet <sha>^{commit}` 验证；通过则返回
  `(baseCommit, DiffBaseTaskStart)`。
  - `-` 前缀防的是 git 参数注入，与 `Diff()` 里既有的同款守卫一致；正常路径下
    `BaseCommit` 恒为 40 位十六进制，这条守卫是纵深防御而非常态分支。
  - `^{commit}` 后缀确保验的是「存在且是个提交」，而不是恰好同名的其他对象。
- 验不过：`log().Warn("任务起点在仓库中不可达，回落仓库默认分支", "repo", repo, "base_commit", baseCommit)`。
- 落到 `resolveBaseBranch(repo)`：非空返回 `(b, DiffBaseRepoDefault)`，空则返回 `("", "")`。

`resolveBaseBranch` 本身不改——它仍是兜底链，只是不再是唯一路径。它的文档注释
里那句「派发时并未记录基准分支名」现在已经过时（B35 之后记了 `BaseCommit`），
一并订正。

### 2.3 取任务记录

`taskRepoOrErr`（`server.go:1342`）现在读出 `*proto.Task` 后只返回 `Workdir()`，
把 task 本身丢掉。改法：

```go
// taskAndRepoOrErr 读取路径中的任务，返回任务体与其工作区目录。
func (s *Server) taskAndRepoOrErr(w http.ResponseWriter, taskID string) (*proto.Task, string, bool)
```

`taskRepoOrErr` 保留为瘦包装（`_, repo, ok := s.taskAndRepoOrErr(...)`），四个调用点
（diff `:1373` / branches `:1409` / file `:1431` / run `:1492`）里**只有 diff 与
branches 两处改用新函数**，file 与 run 不动。

### 2.4 契约变更

`GET /api/tasks/{id}/diff` 的响应从 `{"diff": "..."}` 变为：

```json
{"diff": "...", "base": "a1a83f34db2ce70eba4261f44562eee7694e27ec", "base_source": "task_base"}
```

**纯增字段**：老前端与老 CLI 忽略多余字段，不受影响。

`GET /api/tasks/{id}/branches` 的响应从 `{"branches": [...], "default": "main"}` 变为：

```json
{"branches": ["main", "feat/x"], "default": "a1a83f34...", "default_source": "task_base"}
```

`default` 的语义随之变化：它不再必然是一个**分支名**，可能是一个 sha。这是刻意的
——该字段回答的是「不选的话实际会用什么」，而实际会用的就是任务起点。前端据
`default_source` 决定怎么展示（§4）。

### 2.5 CLI 回显

`internal/client` 的 `Diff` 返回值从 `string` 改为结构体（唯一调用点是
`cmd/diff.go:31`）：

```go
// DiffResult 是 diff 的结果：diff 正文，以及本次实际使用的基准与其来源。
//
// Base/BaseSource 可能为空——老 agentd 不返回这两个字段。空即「不知道」，
// 调用方据此省略回显，不要编造。
type DiffResult struct {
	Diff       string
	Base       string
	BaseSource string
}

func (c *Client) Diff(ctx context.Context, taskID, base string) (DiffResult, error)
```

`cmd/diff.go`：

- **stdout 保持纯 diff 正文**，一个字节不多——协调者常把它接进管道分析，多一行
  头就多一处要剥。
- **stderr 打一行基准回显**，与 `dispatch` 已有的 `基线 <短号>` 是同一个模式：

| `base_source` | stderr 文案 |
|---|---|
| `explicit` | `基准 main（显式指定）` |
| `task_base` | `基准 a1a83f34（任务起点）` |
| `repo_default` | `基准 main（仓库默认分支；未用任务起点）` |
| 空（老 agentd） | 不打印 |

`task_base` 的 sha 显示截断到 8 位。`repo_default` 的文案**不写原因**——落到这一
级有两种可能（任务没有起点记录 / 起点不可达），CLI 无从分辨，精确原因在 agentd
日志里。宁可少说，不能说错。

## 3. B66：run 的引号保真

### 3.1 修法

在客户端逐 token 做 POSIX shell 转义，再 join。服务端**一行不动**，协议**不变**，
因而**对已经跑着的老 agentd 立即生效**（mac-02、Windows 两台不必同步升级）。

新增 `cmd/shellquote.go`：

```go
// shellQuote 把一个参数包装成 POSIX shell 的单引号字面量。
//
// 单引号内除 ' 外一切字符都是字面量（含空格、$、`、\、换行），因此只需处理 '
// 本身：闭合、插入一个转义的 '、再重开，即 '\''。空串产出 ''，否则 join 后会
// 整个消失。
func shellQuote(arg string) string

// shellQuoteJoin 把一组参数转义后用空格连接成可交给 sh -c 的命令行。
func shellQuoteJoin(args []string) string
```

`cmd/run.go:31` 的 `strings.Join(args[1:], " ")` 换成 `shellQuoteJoin(args[1:])`。

### 3.2 为什么客户端转义 + 服务端 sh，而不是改传 argv

两者的**可观测行为完全一致**——服务端始终用 POSIX `sh`（`runshell.go` 里
Windows 也是找 Git 的 `sh.exe`，找不到硬失败、绝不降级到 cmd/PowerShell），所以
客户端做 POSIX 转义永远是对的，不存在方言分叉。改传 argv 需要协议变更、需要处理
新客户端撞老 agentd（老的收不到 `cmd` 会报「cmd 不能为空」这种看不懂的 400）、
服务端还要长期留 `cmd` 与 `argv` 两条路，而换来的行为是同一个。

### 3.3 shell 特性怎么写

转义之后 `&&`、管道、`$()` 都成了字面量，正规出口是显式写 `sh -c`：

```bash
handoff run <task> sh -c 'go build ./... && go vet ./...'
```

这在修复后能正确工作：本地 shell 把它拆成 `["sh", "-c", "go build ./... && go vet ./..."]`
三个 token，转义后成为 `sh -c 'go build ./... && go vet ./...'`，远端 `sh` 解析回
同样三个 token，第三个原样交给内层 sh。

**独立于本次修复的一个既存脚坑**（文档要写明，代码不管）：直接敲
`handoff run <task> go build ./... && go vet ./...` 时，`&&` 被**本地** shell 吃掉，
handoff 只收到 `go build ./...`，而 `go vet` 在协调者本机跑了。这是 shell 的行为，
不是 handoff 的缺陷，两种修法都改不了它。

## 4. Web 控制台对齐

服务端 `handleTaskBranches`（`server.go:1420`）改用 `ResolveDiffBase(repo, task.BaseCommit)`
产出 `default` 与 `default_source`，与 diff 走**同一个函数**——这是「不留两边不
一致」的关键：控制台下拉展示的默认基准，就是不选时 diff 真正会用的那个。

`web/src/api/types.ts` 的 `BranchesResult` 加一个字段：

```ts
export interface BranchesResult {
  branches: string[]
  // default 是不选基准时实际会用的 rev——**可能是 sha 而不是分支名**
  //（任务起点）。空串 = 推导不出。
  default: string
  // default_source ∈ 'task_base' | 'repo_default'，空串 = 老 agentd 或推导不出。
  default_source?: string
}
```

`web/src/app/task/ReviewSidePanel.tsx:89` 的「自动推导」标签按来源渲染：

| 条件 | 标签 |
|---|---|
| `default` 为空 | `自动推导` |
| `default_source === 'task_base'` | `自动推导（a1a83f34 · 任务起点）` |
| 其他（含老 agentd 的无 source） | `自动推导（main · 仓库默认）` |

`task_base` 时 sha 截断到 8 位显示。下拉的其余选项仍只列本地分支——起点 sha 不
在列表里，但它只是 `value=""` 这一项的标签，不需要成为可选项。

## 5. 契约变更汇总

| 位置 | 变更 | 兼容性 |
|---|---|---|
| `GET /api/tasks/{id}/diff` | 响应加 `base`、`base_source` | 纯增，老客户端忽略 |
| `GET /api/tasks/{id}/branches` | 响应加 `default_source`；`default` 语义扩展为「可能是 sha」 | 纯增字段；`default` 语义变化只影响展示 |
| `client.Diff` | 返回 `string` → `DiffResult` | Go 内部 API，唯一调用点同批改 |
| `POST /api/tasks/{id}/run` | **不变** | — |

新客户端 + 老 agentd：diff 拿不到 `base`/`base_source`，回显行省略，diff 正文
仍然正确（只是缺省基准还是老逻辑）。run 直接受益，无需升级服务端。

## 6. 测试与验收

### 6.1 Go 单元测试

`internal/agentd`：

- `TestResolveDiffBasePrefersTaskStart`：临时仓库里造一个真实提交，以它为
  `baseCommit` → 返回 `(sha, "task_base")`
- `TestResolveDiffBaseFallsBackWhenStartUnreachable`：`baseCommit` 给一个仓库里
  不存在的 40 位 sha → 返回 `("main", "repo_default")`
- `TestResolveDiffBaseFallsBackWhenEmpty`：`baseCommit` 为空 → `repo_default`
- `TestResolveDiffBaseRejectsDashPrefix`：`baseCommit` 为 `--output=/tmp/x` →
  不得被当成 rev 采用，落 `repo_default`
- `TestTaskDiffDefaultsToTaskStart`：server 级，造带 `BaseCommit` 的任务，
  `GET .../diff` 不带 `base` → 响应 `base == BaseCommit` 且 `base_source == "task_base"`

`cmd`：

- `TestShellQuoteTable`：纯函数表格测试，覆盖空格、单引号、双引号、`$VAR`、
  反引号、`&&`、换行、空串、中文
- `TestShellQuoteRoundTripThroughSh`：**真的起一个 `sh` 跑一遍**——对每个用例
  执行 `sh -c "printf %s <quoted>"`，断言 stdout 字节与原参数逐字相同。
  这是本条修复的核心判据：不能只断言「转义串长这样」，那等于把实现抄进测试。
  `runtime.GOOS == "windows"` 时跳过这一半（纯函数表格测试仍然跑）。

### 6.2 前端测试

`ReviewSidePanel.test.tsx`：三态标签渲染各一条（无 default / task_base / repo_default）。

### 6.3 真机验收 —— **由审核者本地执行，不随 plan 派发**

这三条都需要**驱动 handoff 自身**（起 agentd、派任务、调 handoff CLI），与派发给
执行者的纪律块「不要派发、不要调用 handoff CLI」直接冲突。按 B126 的结论，它们
必须留在本地验收清单里。

1. 对一个 `--new-worktree --new-branch` 派出的任务执行 `handoff diff <task>`，
   不带 `--base`：stdout 只含该任务分支的改动；stderr 打出 `基准 <短号>（任务起点）`
2. `handoff run <task> grep -rn 'foo bar' .` —— 带空格的模式不被拆开
3. `handoff run <task> sh -c 'go build ./... && go vet ./...'` —— 两条命令都在
   远端跑完，退出码正确

## 7. 已知边界（明确不做）

- **不改服务端 `sh -c` 语义**。`RunCmd` 仍然 `sh -c <cmdline>`，进程组回收、
  10min 超时、输出上限一律不动。
- **不改 `handoff run` 的 flag 顺序约束**。`--target` 等 handoff 自有 flag 仍须写在
  任务名之前，这是 `SetInterspersed(false)`（P1-13）的必然结果，是特性不是缺陷。
- **不为老 agentd 补 base 回显的降级路径**。字段缺失即不打印，不猜、不编。
- **不动 `GET /api/projects/{name}/branches`**（另一个同名接口，服务于新建工作树
  对话框，与任务基准无关）。
- **本地 shell 吃掉 `&&`** 的脚坑只在文档里写明，不在代码里补救。

## 8. 文档与记忆

**随代码同分支交付**：仓库 `README.md` 的 diff / run 两节补基准来源与引号保真的
说明。行为变了而仓库文档没变，等于交付了一个说明书过时的功能——这不该等验收。

**等真机验收通过后再做**：

- 用户全局 `~/.claude/skills/handoff/SKILL.md` 的「审阅取证」段现在写着 run 的
  参数顺序有坑、没提引号丢失，需要订正。**那是用户的文件，实现方不得自行修改**，
  由协调者在真机验收通过后单独征询。
- 记忆 `coordinator-forensics-pitfalls` 记录的正是这两个坑与规避写法，两条都验
  通过后应当删除。同样等验收，不提前。
