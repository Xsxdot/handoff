# handoff status —— 一条命令回答「这个 agentd 能不能用、是什么」 —— 设计

> backlog **B33**（曾短暂记作 B30，与并发会话已建的 B30 撞号后改号）。
>
> 根问题：**CLI 没有健康检查入口。** 想知道一个 agentd 是否可用，现有手段全是
> 侧面证据——用列表命令 `tasks` 当探针（空库时零输出，屏幕上与「连不上」无异），
> 或绕到机器上查进程/查端口（平台差异与引号嵌套让每一种失败都长得像「没有」）。

日期：2026-08-10
状态：待实现

---

## 1. 背景与目标

### 1.1 现场

2026-08-10，一个 agent 用 ssh 探针检查远程开发机 devbox，结论是「没装 handoff、
没有 Go、没有配置、agentd 没跑」，并准备**在同一台机器上另起一个 agentd**。

四条结论全错。直接 ssh 核实：agentd 存活（PID 40751，已运行 2h19m，`TCP *:7777
(LISTEN)`），配置在（`listen: 0.0.0.0:7777`），Go 1.26.1 在 `/usr/local/go/bin/go`，
二进制在 `/Users/sycm/bin/handoff`。

误判的直接原因是探针本身：非交互 ssh 的 PATH 只有 `/usr/bin:/bin:/usr/sbin:/sbin`；
macOS 上 `ss` 不存在、`netstat -ltn` 不支持这些 flag（只打表头）、`pgrep -af` 是
BSD 语义（只打裸 PID）——每一条都被 `|| true` 折叠成了「这东西不存在」。

但探针写法只是表象。**真正的缺口是：那个 agent 手里有现成的工具却没用**，因为
handoff 没有一条命令叫「看看这个 agentd 怎么样」。它把问题理解成了「这台机器上装了
什么」（零件考古），而实际要回答的是「我能不能对这台机器派活」（端到端可用性）。

### 1.2 为什么 `handoff tasks` 顶不上

`tasks` 是列表命令：遍历任务逐行打 JSON（`cmd/tasks.go`）。**任务库为空时输出零行、
退出 0**——屏幕上和「连不上」一模一样，只有退出码能区分，而退出码恰恰是最不被读的
东西。拿列表命令当健康检查，是在拿沉默当肯定信号，与被否定的 ssh 探针犯同一个错。

更根本的是，现有全部路由都是 task 作用域的（`server.go` 的 `Handler()`，
`/api/tasks*` + `/ws/events`），**没有任何端点能回答「这是哪个 agentd」**：版本、
DataDir、监听地址、支持哪些执行者，一概问不出来。由此，CLI 与 agentd 的版本错配
完全不可见——同日实撞：`~/.local/bin/handoff` 是合并前编的，`diff` 缺 `--base`，
只能翻源码才发现。

### 1.3 目标

1. **第一次猜测就猜对**：`handoff status` 出现在 `handoff --help` 里，是任何人（含
   AI 审核者）想确认远端可用性时最先想到的名字。能靠命名解决的，不靠文档解决。
2. **端到端而非零件**：回答的是「这个服务现在能不能用」，不是「机器上装了什么」。
3. **说得出自己是谁**：版本、DataDir、监听地址、执行者清单，让版本错配从不可见变成
   一眼可见。
4. **账面与实际对得上**：不止报 store 里记的状态，还报非终结任务的 executor 是否真
   活着——「账面 running、executor 早没了」正是最该被这条命令抓到的失配。

### 1.4 非目标

- **不做多机全景**：一次只查一个 agentd，与 `dispatch` / `tasks` / `wait` 共用同一套
  `--target` / `--agentd` 语义。要看全景就写 `for` 循环。默认遍历全部 target 会让
  退出码语义立刻模糊（三台里挂一台算成功还是失败？），而退出码是本命令的主判据。
- **不兼做恢复**：发现失配只报告，绝不就地修复。见 §3.3。
- **不引入 API 版本协商机制**：老 agentd 同样不会发版本头，第一代兼容问题依旧得靠
  404 解，现在做它解决不了眼下的问题。见 §6。
- **不加单实例锁**：「同一 DataDir 上起第二个 agentd 无人阻止」是本次现场暴露的另一
  个真实缺口（代码里没有 pid 文件也没有 flock），但那是 agentd 启动期的约束，与本
  设计正交，另记 backlog。

---

## 2. 与 Spec A（运行态对账）的关系

Spec A（`2026-08-09-handoff-runtime-reconciliation-design.md`）的非目标第一条是
**「不新增周期性存活探活」**，其 §2.2 给了三条理由。本设计要探活，必须先说清它没有
违背那条决定：

| Spec A §2.2 的理由 | 对 status 是否成立 |
|---|---|
| 到达口②是事件驱动的，精度与及时性优于轮询，且零开销 | **不适用**。status 不替代通知路径，它回答的是「此刻」这个人的提问。事件驱动解决的是「系统怎么知道」，status 解决的是「我怎么知道」。 |
| 周期性探活会因抖动把活着的 executor 判死，而 adapter 各自已做抖动吸收 | **成立，且是最强反对**。见下。 |
| `StallTimeout` 保留原职责，与之正交 | 成立，本设计不碰看门狗。 |

对第二条的回应是本设计的核心约束：**status 的探活结果绝不改变任何状态。** 抖动导致
的一次误判，代价是输出里一行错话，而不是一个被错误判死的任务——这与周期性探活的代价
根本不同（后者会真的把任务转 failed）。正因为只读，这个风险才承担得起。

再叠一层防护：**超时归 `unknown`，不归 `dead`**（§3.3）。假阳性是诊断命令最贵的失败
模式——一条会说谎的诊断命令比没有更糟，因为你会信它。

结论：Spec A 拒绝的是「后台定时扫」，本设计做的是「人主动问一次」。两者语义不同，
且本设计用「只读 + 超时判 unknown」把 §2.2 唯一成立的那条理由的代价压到可接受。

---

## 3. 探活契约

### 3.1 为什么必须走 adapter

存活判据是 **per-adapter** 的，并且在 manager 层统一做 `tmux has-session` 会对
claude 稳定给出假阳性。`internal/executor/claudecode/resume.go` 的文件头写得很直白：

> 存活判据（本 adapter 与 opencode 的关键差异）：tmux has-session **不可用**——窗口 1
> 的 `tail -f render.log` 会一直活着，claude 早死了会话依然存在（opencode 靠 HTTP
> 探活兜住这一点，claude 没有这个面）。判据是两条，缺一即视为死亡：
> 1. `out.jsonl` 中不含 `handoff_exit` 哨兵（含则进程已退，带退出码）
> 2. tmux 会话存在（会话都没了，进程一定没了）

所以探活必须由各 adapter 用自己的判据实现。manager 层直接查 tmux 会复现 B24 那类
误判。

### 3.2 为什么不复用 `Resume(Cold=false)`

`Resume` 的热重连路径判据正确，但**有副作用**，不能当只读探针：

- 判死后会 `proc.Kill()` 回收旧会话（为了让后续冷恢复不撞名）；
- 进门先在 `a.runs` 上占位（冷恢复互斥——两个 claude 进程抢同一会话是数据损坏级别的
  后果）。

拿它当 status 的探针，等于让一条只读命令去动 executor。

### 3.3 契约

```go
// internal/executor/probe.go —— 与 resume.go 同规格：只有数据，没有接口。
// 接口由消费方（manager）定义并做类型断言，这样「不支持探活的 adapter 一律按
// unknown 处理」是自然语义，executor.Adapter 的五动作核心契约也不被污染。

// ProbeReq 是一次只读存活探测请求。
type ProbeReq struct {
    TaskID    string // 目标任务
    TaskDir   string // DataDir/tasks/<id>，proc 信息文件在里面
    SessionID string // 落库的 task.ExecutorSession
}

// ProbeOutcome 是探测结论。
type ProbeOutcome struct {
    Alive bool   // executor 是否仍在
    Note  string // 一句话理由，直接给审核者看（如「tmux 会话 handoff-1c28505a 不存在」）
}
```

```go
// internal/agentd/manager.go —— 与 restorer / reaper 同一套路数的可选能力接口
type prober interface {
    Probe(executor.ProbeReq) (executor.ProbeOutcome, error)
}
```

**三态靠 error 区分**，不塞第三个布尔字段：

| 返回 | 结论 | 例 |
|---|---|---|
| `err == nil && Alive` | `alive` | 哨兵未出现且 tmux 会话在 |
| `err == nil && !Alive` | `dead` | tmux 会话已不存在 |
| `err != nil` | `unknown` | proc 信息文件读不出来、探针自身失败 |
| adapter 未实现 `prober` | `unknown` | `fake` adapter |
| 探测超时 | `unknown` | 见下 |

三条硬约束：

1. **Probe 绝不写**：不 `Kill`、不占 `runs`、不碰 store、不发事件。这三件事 `Resume`
   都做，正是它不能被复用的原因。
2. **判据不许分叉**：从每个 adapter 的 `Resume` 里把「判死」抽成不带回收动作的纯
   函数，`Resume` 与 `Probe` 调同一份。判据一旦分叉，status 说的和实际恢复行为就是
   两回事，而这类 bug 极难被发现——它只在两条路径给出不同结论时才显形。
3. **超时归 `unknown`**：单个探测超时 **2 秒**；status 整体对全部活跃任务的探测总时
   限 **10 秒**，超出部分一律记 `unknown`。理由见 §2。

四个真实 adapter（opencode / claudecode / grok / codex）均实现 `Probe`；`fake`
不实现，其任务恒为 `unknown`。

---

## 4. 服务端：`GET /api/status`

挂在既有 `Handler()` 上，**走既有 Bearer 鉴权中间件**，不开匿名口——版本、DataDir、
任务计数都是部署信息，不该对拿不到 token 的人可见；而且 401 本身就是一条有用的诊断
结论（§6）。

聚合全部在服务端完成，CLI 只渲染。不是为省往返，而是探活判据在 adapter 里、CLI 够
不着；任务计数一并放服务端，才不至于一半数据在这边算、一半在那边算。

### 4.1 响应契约

```go
// internal/proto/proto.go

// BuildInfo 是一个 handoff 二进制的构建标识，取自 runtime/debug.ReadBuildInfo。
type BuildInfo struct {
    Revision string `json:"revision"` // vcs.revision，空=非 go build 产物
    Time     string `json:"time"`     // vcs.time
    Modified bool   `json:"modified"` // vcs.modified：带未提交改动编出来的
    Go       string `json:"go"`       // GoVersion
}

// ActiveTask 是一个非终结任务及其 executor 存活结论。
type ActiveTask struct {
    ID       string `json:"id"`
    Name     string `json:"name"`
    State    string `json:"state"`
    Executor string `json:"executor"`
    RepoPath string `json:"repo_path"`
    Live     string `json:"live"` // "alive" | "dead" | "unknown"
    Note     string `json:"note"` // 判死/判不出的一句话理由；alive 时为空
}

// StatusResp 是 GET /api/status 的响应。
type StatusResp struct {
    Version         BuildInfo      `json:"version"`
    Listen          string         `json:"listen"`
    DataDir         string         `json:"data_dir"`
    StartedAt       time.Time      `json:"started_at"`       // agentd 进程启动时刻
    Executors       []string       `json:"executors"`        // 已注册执行者名，字典序
    DefaultExecutor string         `json:"default_executor"`
    TaskCounts      map[string]int `json:"task_counts"`      // 六个状态各自计数，零值也出现
    Active          []ActiveTask   `json:"active"`           // 非终结任务，created_at 降序
}
```

`StartedAt` 与 `Executors` 是主动加的两项：uptime 回答「是不是刚被谁重启过」（本次
现场里 ssh 查到的 `up 2h19m` 正是这条信息）；执行者清单让「这台机器支持哪些
executor」在派发前就可知——两台机器配置不一致时这是唯一的照面。

### 4.2 数据来源

| 字段 | 来源 |
|---|---|
| `Version` | `runtime/debug.ReadBuildInfo()`，读 `Settings` 里的 `vcs.revision` / `vcs.time` / `vcs.modified` |
| `Listen` / `DataDir` / `DefaultExecutor` | `cfg.Listen` / `cfg.DataDir` / `cfg.Executor.Default` |
| `StartedAt` | agentd bootstrap 时记录的进程启动时刻（新增，见 §4.3） |
| `Executors` | manager 的 `ads` 注册表键集合，排序后输出 |
| `TaskCounts` / `Active` | `store.ListTasks()` 后在内存分组（任务量小，无需新增计数 SQL）。**六个状态的键恒存在，计数为零也出现**——缺键与零值对消费方是两回事 |
| `ActiveTask.Live` / `Note` | 逐个走 §3.3 的 `prober` |

「非终结任务」= 状态为 `pending` / `running` / `waiting_answer` / `waiting_review`
四者之一（`completed` / `failed` 为终结态）。

### 4.3 需要新增的服务端状态

agentd 目前不记录自身启动时刻。在 bootstrap（`cmd/agentd.go`）取一次
`time.Now()` 传给 `NewServer`，作为只读字段持有。这是本设计对现有结构唯一的侵入。

`Version` 不需要构建期改动：实测 `go build` 默认就把 VCS 信息戳进二进制——

```
mod   github.com/xushixin/handoff  v0.0.0-20260810014537-8353ef68d711
build vcs.revision=8353ef68d711eaf63eeb1287f342f3238204aec8
build vcs.time=2026-08-10T01:45:37Z
build vcs.modified=false
```

无需 Makefile、无需 ldflags。

---

## 5. CLI：`handoff status`

### 5.1 默认输出（文本）

```
agentd   http://100.73.238.21:7777   可用
版本     8353ef68d711  2026-08-10T01:45:37Z  go1.26.1
本地     8353ef68d711  一致
数据     /Users/sycm/.handoff        已运行 2h19m
执行者   opencode(缺省)  claude  grok  codex

任务     running 1 · waiting_review 2 · completed 14 · failed 1
活跃
  40f81a2e  B28 codex adapter   waiting_review  claude    executor 存活
  1c28505a  B19 env 注入         running         opencode  executor 已不在（tmux 会话 handoff-1c28505a 不存在）
```

选文本而非 JSON 作默认：status 是唯一一条人会天天手敲的命令，而排障正是它最常被
使用的时刻；机器可操作的那一位（能不能用）已经在退出码里，agent 读中文结论行也没有
障碍。

「本地」行三种写法：

| 情形 | 输出 |
|---|---|
| 两边 revision 相同 | `一致` |
| 不同 | 并列两个 revision + 一句提醒，**仍退 0** |
| 本地读不到 vcs 戳（`go run`、测试二进制） | `本地版本未知（非 go build 产物）` |

`vcs.modified=true` 的一侧一律显式标 `带未提交改动`——它意味着这个二进制对不上任何
一个提交，排障时这是关键信息（同日撞过的 `--base` 缺失就属这类）。

revision 不同**不阻断**：handoff 没有兼容矩阵，revision 不同不等于不兼容。并列报出，
该不该继续交给人判。

「任务」一行**只列计数非零的状态**（否则六个状态里常年四个是 0，把结论淹了）；
全部为零时输出 `任务     无`。JSON 侧不做这个省略（§4.2）——人看的要短，机器读的
要齐。

活跃任务行的 id 用 8 位短 id 显示（与 tmux 会话命名 `handoff-<id8>` 一致，便于人肉
对照），但 JSON 里始终是完整 UUID，且**任何需要拿去当参数的地方都必须用完整 UUID**
——`store.GetTask` 是精确匹配（`WHERE id = ?`），不做前缀查找。

### 5.2 `--json`

```json
{ "reachable": true, "degraded": false, "cli": {…BuildInfo}, "agentd": {…StatusResp} }
```

`reachable` 与退出码同源，脚本读哪个都行。`degraded: true` 表示对端是老 agentd，此时
`agentd` 为 `null`。

### 5.3 退出码

**0 = 可达且鉴权通过**（含老版 agentd、含探活超时、含版本不一致）；**1 = 够不着**。

退出码回答的是「这个 agentd 能不能用」，不是「我问全了吗」。老版 agentd 照样能派发、
能审阅，判它失败会让一台完全能用的机器被判死——而一个会误报的诊断命令会被绕过去。

不新增第三个退出码：现有只有 `ExitFailure=1` 与 `ExitTimeout=124`（`cmd/exit.go`），
agent 连第二个码的含义都少查，第三个更不会。

---

## 6. 旧版兼容与错误处理

老 agentd 不认 `/api/status`：请求经 auth 中间件后落到 `http.ServeMux`，返回 **404**。
CLI 把它**直译成一条成功的诊断**，而不是失败：

```
agentd   http://100.73.238.21:7777   可用（版本过旧）
已确认   TCP 可达 · HTTP 正常 · Bearer 鉴权通过
限制     该 agentd 不支持 /api/status，详情不可得
处置     升级远端 agentd 后重试
```

措辞是刻意的：**能收到 404 本身已经把「能不能用」回答了一大半**——TCP 通、HTTP 正常、
Bearer 过。而在版本错配这个场景里，「远端过旧」正是你要的诊断结论。

不写「该端点自 xxx 版本引入」：CLI 无从知道对端版本，编一个引入点就是编造。

| 情况 | 输出要点 | 退出码 |
|---|---|---|
| 404（老 agentd） | 如上 | **0** |
| connection refused | 原样带上 dial 错误（`dial tcp …: connect: connection refused`），点明「没有 agentd 在这个地址监听」 | 1 |
| 401 | `token 不匹配：配置 <path> 中 target <name> 的 token 与远端 agentd 不一致` | 1 |
| `--target` 未定义 | 复用 `TargetEndpoint` 既有错误，不新造 | 1 |
| 其他 5xx | 原样带上状态码与响应体 | 1 |
| 探活超时 / adapter 不支持 | 该行显示 `unknown` + 理由，其余照常输出 | 0 |

---

## 7. 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/executor/probe.go` | 新建 | `ProbeReq` / `ProbeOutcome` 数据契约（只有数据，无接口、无 I/O） |
| `internal/executor/{opencode,claudecode,grok,codex}/probe.go` | 新建 ×4 | 各自的只读 `Probe`；判据纯函数从对应 `resume.go` 抽出后两处共用 |
| `internal/agentd/status.go` | 新建 | 服务端聚合：版本 + 配置 + 计数 + 逐个探活；`prober` 接口断言 |
| `internal/agentd/server.go` | 修改 | 注册 `GET /api/status`；`Server` 持有 `startedAt` |
| `cmd/agentd.go` | 修改 | bootstrap 时把启动时刻传给 `NewServer` |
| `internal/proto/proto.go` | 修改 | `BuildInfo` / `ActiveTask` / `StatusResp` |
| `internal/client/client.go` | 修改 | `Status(ctx) (*proto.StatusResp, error)`，404 返回可判别的哨兵错误 |
| `cmd/status.go` | 新建 | CLI：调用、文本/JSON 渲染、退出码、404 直译 |
| `internal/buildinfo/buildinfo.go` | 新建 | `Read()` 读 `debug.ReadBuildInfo` 并归一成 `proto.BuildInfo`；包一层函数变量以便注入（见 §8） |

`internal/buildinfo` 单开一个包，因为 CLI 与 agentd 都要读自己的构建标识，放任何一
边都会造成反向依赖。

---

## 8. 测试

**服务端**（`httptest` 挂 `Handler()`）：

- 200 响应的字段齐全性（六个状态计数即使为零也出现）。
- 注入不实现 `prober` 的 `fake` adapter → 该任务 `live == "unknown"`。
- 注入桩 adapter 覆盖三态各一：`alive`、`dead`（`Note` 非空）、`unknown`（`err != nil`）。
- 探测超时 → `unknown`，且整体不超过总时限。
- 无 token / 错 token → 401（走既有鉴权，回归即可）。

**CLI**（起 `httptest` 后端）：

- 后端返回 404 → 输出含「版本过旧」**且退出码 0**。
- 后端返回 401 → 退出码 1。
- 端口无人监听 → 退出码 1，错误文本含 `connection refused`。
- 正常 200 → 退出码 0，文本含关键字段；`--json` 的 `reachable == true`。

**adapter**：各自判据纯函数的单测，复用已有的 `tmuxHasSession` 变量注入手法
（`internal/executor/claudecode/resume_test.go:45`）。必须覆盖 claude 的「tmux 会话
在但哨兵已出现」这一路径——它正是「单看 tmux 会假阳性」的那个反例。

**一个必须避开的坑**：`go test` 编出的测试二进制**不带 vcs 戳**（实测：`Settings` 里
只有 `-buildmode` / `GOARCH` / `CGO_*` 之类，没有 `vcs.revision`）。所以单测里不能
断言 revision 非空。`internal/buildinfo` 把 `debug.ReadBuildInfo` 包成可替换的函数
变量，测试注入桩值——与 `tmuxHasSession` 同一手法。

---

## 9. 落地后的连带动作

`skills/handoff/SKILL.md` 增一节「确认 agentd 在不在」，内容就是 `handoff status`
加 §6 那张错误分诊表，**不写 ssh 探针配方**。本设计的立场是：能靠命名解决的，不靠
文档解决——教 agent 用列表命令探活，是在训练一个反常识动作。
