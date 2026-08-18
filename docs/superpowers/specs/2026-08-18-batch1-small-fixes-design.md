# 批 1 琐碎修设计：B120 / B121 / B82(main) / B101

> 职责：给四条互不相干的小缺陷定死改法、边界与判据，供 writing-plans 拆成一份可派发的实现计划。
>
> 边界：只覆盖这四条。不碰 B122（Windows procenum 实现）、B144/B148（procenum 缺失的日志档位与误报）、
> B145/B147（另一会话在处理），也不做任何配置结构扩展。

## 1. 为什么这四条放一起

它们**不共享抽象、不共享落点、实现顺序无依赖**。合成一份 spec 只有一个理由：形状相同——
「一处判据/一个字段/一条文案是错的，改动几行，判据能在单测里钉死」，因而适合一次派发一次终审。

实现方不要试图为它们抽公共代码。任何「顺手统一一下」的冲动都超出本 spec。

四条共同的性质：

- 都**不新增配置键**。B88（配了新键的机器跨版本回滚会被 KnownFields 拒启动，进无限崩溃循环）是本仓库
  已知的老坑，本批刻意不去碰它。
- 都**不改变任何对外协议字段**。
- 都能在本机单测里判定；其中两条的 Windows 行为只有单测证据，如实记为真机欠账（见 §7）。

## 2. B120：项目名派生不认 Windows 分隔符

**现状**（[`internal/agentd/projectadmin.go:113`](../../../internal/agentd/projectadmin.go)）：

```go
func projectNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}
```

origin 为 `C:\work\probe-origin.git` 时派生出 `\work\probe-origin`，随即撞上 `validateProjectName`
的「名字含 `/ \ :` → 拒」（那条校验本身是对的：名字会被直接拼进 `repo_root/<名字>`），
于是自动登记失败、dispatch 400。

**改法**：把两处分隔符集合都补上 `\`——

- `strings.TrimRight(..., "/")` → `strings.TrimRight(..., "/\\")`（去掉尾部反斜杠）
- `strings.LastIndexAny(s, "/:")` → `strings.LastIndexAny(s, "/:\\")`

**为什么安全**：git URL（`https://`、`ssh://`、`git@host:path`、scp 简写）都不含反斜杠，把它加进
分隔符集合对既有形态零影响；只有本地路径 origin 会走到新分支。

**不做**：不为 Windows 路径做任何额外规范化（盘符、UNC 路径、大小写折叠）。取末段就够了，
`validateProjectName` 仍是最后一道闸。

**测试**：`projectNameFromURL` 的表驱动用例补四行——

| 输入 | 期望 |
|---|---|
| `C:\work\probe-origin.git` | `probe-origin` |
| `C:\work\probe-origin\` | `probe-origin` |
| `git@github.com:Xsxdot/handoff.git` | `handoff`（回归，确认不变） |
| `https://github.com/Xsxdot/handoff` | `handoff`（回归，确认不变） |

再补一条断言：上面两个 Windows 输入派生出的名字能过 `validateProjectName`。

## 3. B121：Windows 上的 opencode 登录判据是假的

**现状**（[`internal/toolchain/detect.go:82`](../../../internal/toolchain/detect.go)）：

```go
var credRelPath = map[string]string{
	"opencode": ".local/share/opencode/auth.json",
	"grok":     ".grok/auth.json",
	"codex":    ".codex/auth.json",
}
```

`.local/share/...` 是 XDG 落点，Windows 上 opencode 不用它，于是探测**恒定误报「未登录」**。

这个文件里已经有一条同形先例：`claude` 被刻意排除在表外，注释写明理由是「没有可靠的轻量文件判据，
拿它当判据会把没登录的机器报成就绪」。B121 是它的镜像——拿一个在该平台不成立的路径去断言「未登录」，
同样是在撒谎。而且实测里「登录」本就不是使用的必要条件：未登录状态下 `opencode models` 仍列出 8 个
免费模型，B37 整轮真机验收就跑在这个前提上。

**改法**：让 `credRelPath` 的查表结果与平台相关——**Windows 上 opencode 不在表里**。
于是它自然落进 `Detect` 中已经存在的那条分支：

```go
rel, ok := credRelPath[name]
if !ok || homeErr != nil {
	r.State = StateAuthUnknown   // 「查不了」不是「没登录」
	...
}
```

**不新增分支、不新增状态、不新增字段。**

**可测性**：`Detect()` 目前直接读 `runtime.GOOS` 之外的三个 seam（`userHomeDir` / `lookPath` / `statFile`）。
按本仓库既有形状（[`internal/agentd/runshell.go:104`](../../../internal/agentd/runshell.go) 的
`resolveRunShell(runtime.GOOS, exec.LookPath, statFile)`）把平台作为参数传给一个内部函数，
`Detect()` 作为薄包装传 `runtime.GOOS`。**不要**引入构建标签分文件——那会让 macOS 上跑不到 Windows 分支。

**grok 与 codex 不动**：`~/.grok/auth.json` 与 `~/.codex/auth.json` 在 Windows 上同样成立，
无证据表明它们错，不要顺手一起改。

**测试**：

- `goos = "windows"`：opencode 报 `StateAuthUnknown`；**grok / codex 仍按文件存在性判定**
  （即 statFile 命中报 `StateReady`、不命中报 `StateNoCreds`）——这条断言是防止把三家一起误伤
- `goos = "darwin"`：三家行为与今天逐字相同（回归）
- `claude` 在两个平台都仍是 `StateAuthUnknown`（回归）

**连带影响**：`FirstReady` 明确不把 `StateAuthUnknown` 算作就绪。因此 Windows 上若只装了 opencode，
`init` 挑不出 `executor.default` 的默认值——这是**如实**的结果（我们确实不知道它登没登录），
不是本条要修的东西，也不要顺手改 `FirstReady` 的语义。

## 4. B82(main)：run 对已回收的工作树报「/bin/sh 不存在」

**现状**（[`internal/agentd/workspace.go:1572`](../../../internal/agentd/workspace.go)）：
`RunCmd` 把 `cmd.Dir = repo` 之后直接 `cmd.Start()`，**从不检查 repo 是否存在**。
工作树被 `done` / `stop` 回收后再对该任务发 `run`，内核在 chdir 阶段就失败，
错误却长成 `fork/exec /bin/sh: no such file or directory`——指向一个完全无辜的 sh。

路由层（[`internal/agentd/server.go:1533`](../../../internal/agentd/server.go)）只给
`ErrNoProcHeadroom` 映射 400，其余一律 500。「你要的工作树没了」不是服务端故障，500 是撒谎。

**改法**（两处，形状完全对齐紧邻的 `ErrNoProcHeadroom`）：

1. `internal/agentd` 新增哨兵错误 `ErrWorkdirGone`。
2. `RunCmd` 在 `cmd.Start()` 之前 stat 一次 `repo`；不存在则**不启动任何进程**，
   返回包装 `ErrWorkdirGone` 的错误，文案点名真因与路径，例如：
   `工作目录不存在（managed worktree 可能已被 done/stop 回收）: <repo>`
3. `server.go` 的错误分支加一条 `errors.Is(err, ErrWorkdirGone)` → 400。

**为什么判在 RunCmd 而不是路由层按任务状态推断**（这条理由必须落成代码注释）：
目录缺失的原因不止「任务已归档」一种——人手删、盘掉了、路径被改，都会到这里。
按状态反推只覆盖归档那一种，其余场景会重新退回误导性报错；而 stat 是对**真实原因**的直接判据。

**顺序要求（只有一条是承重的）**：stat **必须排在 `runShell()` 之前**——否则 Windows 上
「找不到 sh」的错误会抢在「工作树没了」前面报出来，又是一次指错方向。
它与 `checkProcHeadroom` 的先后无所谓，实现方自行决定。

**测试**：

- `RunCmd` 对一个不存在的目录返回的错误满足 `errors.Is(err, ErrWorkdirGone)`，`exitCode == -1`
- 同一用例断言**没有子进程被启动**（用一条会产生可观测副作用的 cmdline，如写文件，断言文件不存在）
- 路由层用例：该错误映射为 400 且响应体含真因文案（不是 500）
- 回归：目录存在时行为不变（正常执行、非零退出仍返回 200）

## 5. B101：`executor.model` 只对缺省执行者生效

**现状**（[`internal/agentd/manager.go:622`](../../../internal/agentd/manager.go)）：

```go
model := req.Model
if model == "" {
	model = m.cfg.Executor.Model // 配置级兜底；仍空则 executor 自身默认
}
```

`ExecutorConfig` 只有 `Default` 与 `Model` 两个字段，`Model` 语义上是**缺省执行者**的默认模型
（全局纪律文档也这么写），但这里不看解析出来的是谁，一律套上。于是配了
`executor.model: opencode-go/deepseek-v4-flash` 的机器派 codex，任务 `model` 就是这个值，
第一回合直接吃 `400 ... model is not supported when using Codex with a ChatGPT account`。

**改法**：只有当解析出的执行者就是缺省执行者时才套配置值——

```go
model := req.Model
if model == "" && execName == m.cfg.Executor.Default {
	model = m.cfg.Executor.Model
}
```

`execName` 来自 `resolveExecutor`，它已经把空的 `req.Executor` 归一化成 `cfg.Executor.Default`，
所以这一个判断同时覆盖「没写执行者」和「显式写了执行者」两条路径。

**必须钉死的边界**：显式传 `--executor opencode` 而它恰好等于 `executor.default` 时，
**照样套配置模型**。语义是「缺省执行者的默认模型」，与调用方有没有把名字显式写出来无关。
这条要写成注释，否则下一个人还会把它读成「只有省略时才生效」。

**非缺省执行者的结果**：`model` 留空下发，由该执行者自身的默认模型接管。这恰恰是想要的——
模型名本来就是按机器不同的（同一个 codex，mac-02 上是 `gpt-5.6-luna`，win-b37 上是
`deepseek-v4-pro`），本机默认比协调者硬编码更准。

**明确不做**：不新增 `executor.models` 之类的按执行者映射表。它需要新配置键，会撞上 B88；
且目前没有「必须给非缺省执行者钉死模型」的实际场景——`--model` 已经能覆盖。真出现了再单独立项。

**测试**：

- `default = "opencode"`、`cfg.Executor.Model = "cheap/model"`：派 opencode（不带 `--model`）→ 任务 model 为 `cheap/model`
- 同配置下派 codex（不带 `--model`）→ 任务 model 为**空**
- 同配置下显式 `--executor opencode` → 仍为 `cheap/model`（边界回归）
- 任何执行者带 `--model x` → 恒为 `x`（`req.Model` 优先级最高，回归）

## 6. 日志与注释要求

- **B82** 的新分支打一条 `Warn`（不是 Error——这是调用方的条件，不是服务端故障），带 `task` 与 `repo`。
  与紧邻的「run 被拒：进程余量不足」同档位。
- **B101** 的判断加中文注释解释 why（语义是缺省执行者的默认模型 + 显式写出来也算）。
- **B121** 的平台条件加中文注释解释 why（Windows 上 opencode 不用 XDG 落点，没有可靠判据就如实报未知），
  并指向 `claude` 那条既有先例。
- **B120** 的分隔符集合加一句注释说明为何加 `\` 安全（git URL 不含反斜杠）。
- 四条都不新增函数级日志——它们都在已有日志点的覆盖范围内，加了只是噪音。

## 7. 验收判据

**本机全量门**（缺一不可，实现方必须亲自跑到结果，不许写没跑过的结论）：

```
go build ./... && go vet ./... && gofmt -l .   # gofmt 必须无输出
go test ./...
go test -race ./internal/agentd/ ./internal/toolchain/ ./cmd/
```

**真机欠账（如实记，不假装验过）**：

- B120 的 Windows 本地路径 origin 自动登记：只有单测证据，**未在 Windows 执行机上真跑过**
- B121 的 Windows 分支：只有 `goos = "windows"` 的单测证据，**未在真 Windows 上确认 opencode 的实际落点**
  （本 spec 刻意不去查那个落点，见 §3）
- 两条都随批 3（Windows 收尾，含 B122）上机时补验

B82 与 B101 无真机欠账：两者都与平台无关，单测即为充分判据。

## 8. 收尾提醒（不属本 spec 实现范围）

B101 落地后，全局 `~/.claude/CLAUDE.md` §4 里「mac-02 派 codex 要显式 `--model gpt-5.6-luna`」
那条派发纪律就失效了——配置模型不再污染 codex，本机默认接管。收尾时提醒用户退休这条纪律，
但**不要由实现方去改 CLAUDE.md**（那是用户的个人配置，不在仓库里）。
