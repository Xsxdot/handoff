# 仓库登记归一化（B62）

> **定位**：W3a（项目与机器控制面）的**前置**。W3a 要在 Web 控制台里画一棵
> 「这台机器上有哪些项目」的树，而这棵树的数据源今天是漏的——本文把漏堵上。
> 本文**不**建 `projects` 表、**不**引入 `project_id`、**不**做 workspace 探测，
> 那些都在 W3a 自己的 spec 里。
>
> **分支**：`handoff/b62-repo-registration`，基于 `main`。做完合进 `handoff/web-console`，
> 且必须在 W3 开工前合并——W3a 的实现直接依赖本文确立的不变式。

## 1. 病灶

`resolveRepoInput`（[internal/agentd/reporegistry.go:150](../../../internal/agentd/reporegistry.go:150)）把 `--repo` 分三支解析：

```go
func resolveRepoInput(input, originURL string, entries []proto.Repo) (string, error) {
	if looksLikePath(input) {
		log().Info("仓库解析：按路径直通", "input", input)
		return input, nil          // ← 病灶：完全不看 entries
	}
	...
```

第一支叫「路径直通」：只要 `--repo` 里含 `/`、`\`、`:` 三个字符之一，就**完全绕开登记表**，
原样返回该路径。注释写着「保持今天的行为不变」——这是 B46 引入登记表时刻意留的向后兼容口子。

后果是：**agentd 不知道自己在哪些项目上干过活。**

实测证据（08-11，devbox）：

```
$ handoff repo ls --target devbox
（该执行机上还没有任何仓库登记，用 handoff repo add 落地一个）
```

而这台机器上已经跑过十几个 handoff 任务。每一次 dispatch 都写了绝对路径，每一次都走了直通分支，
一条登记都没留下。登记表在这台机器上是空的，不是因为没干活，而是因为**干活根本不经过它**。

这不只是「统计缺失」。它有三个具体代价：

1. **W3a 的项目树直接卡在这里**。树要显示「这台机器上有哪些项目」，唯一的持久化数据源就是
   `repos` 表；表是空的，树就是空的，而机器上明明有十几个任务在跑。
2. **跨机对话没有共同名词**。多台执行机之间要互相引用同一个项目，需要一个各机都算得出的
   稳定标识；标识只能从 origin 派生，而 origin 只有登记条目里有。直通派发的仓库连 origin 都没被读过。
3. **登记表是半真的，比全假更坏**。`repo ls` 显示的是「登记过的」，不是「用过的」，
   两者今天可以毫无交集。任何基于它做的判断都是错的。

## 2. 目标与非目标

**目标**

- 让「agentd 在这个仓库上干过活」与「这个仓库在 `repos` 表里」成为同一件事，不留旁路。
- 把已经发生过的历史（`tasks.repo_path`）尽可能地回填成登记，让存量机器不必从零重来。
- 让新增的硬要求**不疼**：被拒时报文直接给出可复制粘贴的下一步命令。

**非目标**

- 不建 `projects` 表、不加 `project_id`、不改 `proto.Repo` 的字段——W3a 的事。
- 不做 workspace / worktree 探测——W3a 的事（且已定为现场探测，不落库）。
- 不动 `/api/repos` 三条路由的形状——它们今天就够 W3a 用。
- 不碰 `repo rm` 的占用校验（`ActiveTasksByRepoPath` 已经在做）。

## 3. 设计

### 3.1 「路径直通」改为「路径查表」

`looksLikePath` 分支不再原样返回，改为**拿这个路径去登记表里查**：

```go
if looksLikePath(input) {
    want := filepath.Clean(input)
    for _, e := range entries {
        if filepath.Clean(e.Path) == want {
            log().Info("仓库解析：路径命中登记", "input", input, "name", e.Name)
            return e.Path, nil        // 返回登记里的 Path，不是用户输入
        }
    }
    log().Warn("仓库解析被拒：路径未登记", "input", input, "registered", repoNames(entries))
    return "", fmt.Errorf("%w: ...", ErrRepoNotRegistered)  // 报文见 3.2
}
```

三点要交代清楚：

**为什么保留路径写法，而不是干脆只认登记名。** 目标是「登记表成为唯一真相」，不是
「禁止路径语法」。`--repo <绝对路径>` 是肌肉记忆，也散落在 README 与既有脚本里；
让它继续可用、只是改由登记表背书，破坏面最小而收益不变。路径此后是**登记表的一种查法**，
不是绕过它的一条路。

**为什么返回 `e.Path` 而不是 `input`。** 登记条目里的路径是权威写法；用户写的可能是
`/a/b/` 这种带尾斜杠的等价形式。统一取权威值，下游（worktree 落点、`ActiveTasksByRepoPath`
的按路径反查）才不会出现两种写法指向同一仓库。

**为什么只做 `filepath.Clean`，不解析 symlink。** 本文件的头注释写明「不碰 git、不碰文件系统……
纯函数才能表驱动穷举 + 变异检验」。为了认出 `/tmp` 与 `/private/tmp` 这类别名而引入
`filepath.EvalSymlinks`，等于把 dispatch 的必经之路从纯函数变成依赖文件系统状态的函数——
代价远大于收益。别名不互认时报文会列出已登记的路径原文，人一眼就能看出差别（见 3.2）。

其余三支（登记名命中 / 登记名查不到 / 省略 `--repo` 走 origin 匹配）**一个字不动**。

### 3.2 拒绝报文必须自带下一步

被拒的人多半不在执行机上，读不到 `agentd.log`。报文是他唯一的线索，所以要一次给全：
写了什么、本机有什么、下一步敲什么。

路径未登记：

```
仓库未登记: /Users/sycm/workspace/handoff
本机已登记的仓库：（本机尚无任何登记）
先落地它再派发：
  handoff repo add --path /Users/sycm/workspace/handoff [--target <执行机>]
```

省略 `--repo` 且 origin 零命中（既有分支，只补最后两行）：

```
仓库未登记: 当前仓库 git@github.com:Xsxdot/handoff.git 尚未登记到本机
本机已登记的仓库：nova, tk
先落地它再派发：
  handoff repo add --path <该仓库在执行机上的路径> [--target <执行机>]
```

`[--target <执行机>]` 写成字面量占位符：agentd 不知道调用方用的是哪个 target 名字，
而 CLI **不应该**去改写服务端报文——那正是「把 agentd 的中文原文透出来」这条纪律
（Web 控制台总方案 §4.8）要防的事。让人自己填一个他刚敲过的名字，成本是零。

### 3.3 历史回填：一次性，可跳过，不复活

新规则只管住未来。存量机器上已经跑过的任务留下了 `tasks.repo_path`——那是「这台机器
在这个仓库上干过活」的直接证据，回填它就是把既成事实补登记。

**触发点**：agentd 启动时，`RecoverOnStartup`（[cmd/agentd.go:155](../../../cmd/agentd.go:155)）之后、对外服务之前。

**一次性**：新增 `meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)` 表，回填完成后写
`repo_backfill_v1 = <RFC3339 时间戳>`；键已存在则整个回填跳过。

> **为什么必须一次性**：每次启动都跑，会让用户 `handoff repo rm` 掉的登记在下次重启时
> 复活。那是个又隐蔽又气人的 bug——注销是明确的意图表达，任何自动化都不该推翻它。
> 一次性还意味着这段代码将来可以安全删除（所有机器都跑过之后）。

**算法**：

```
paths := SELECT DISTINCT repo_path FROM tasks WHERE repo_path <> ''
for p in paths:
    if 已有登记的 Clean(Path) == Clean(p): 跳过（已登记）
    if EnsureRepoUsable(p) 失败:          跳过，Warn 记原因（路径不存在 / 不是 git 仓库）
    origin, err := repoOriginURL(p)
    if err != nil:                        跳过，Warn 记原因（没有 origin）
    name := 唯一化(repoNameFromURL(origin))   // 冲突时依次尝试 name-2、name-3……
    CreateRepo{Name: name, Path: p, OriginURL: origin}
写 meta[repo_backfill_v1]
最后打一行汇总：登记 N 条，跳过 M 条（每条跳过的路径与原因各一行 Warn）
```

**为什么探测失败的路径只跳过、不登记一条「坏」的**：`repoOriginURL` 的既有注释已经立过规矩——
「没有 origin 的仓库拒绝登记：它永远参与不了 dispatch 省略 `--repo` 时的 origin 自动匹配，
登记进来只会变成一条永远匹配不上的死记录」。回填不该给这条规矩开后门。跳过的路径都进
Warn 日志，人看得见，需要的话手工 `repo add` 一条。

**为什么用 `repo_path` 而不是 `work_dir`**：`work_dir` 在 worktree 模式下是工作树目录，
不是仓库本身；`repo_path` 才是登记要指向的那个东西。

### 3.4 削掉新硬要求带来的摩擦

登记从此是 dispatch 的前置条件，所以登记本身不能麻烦。一处改动：

`handoff repo add` 在**本机模式**（没有 `--target`）、没有 `--clone`、也没给 `--path` 时，
`--path` 默认取 **cwd**。于是在仓库目录里敲一句 `handoff repo add` 就完成登记。

远程模式不做这个默认——cwd 是审核者本机的路径，跟执行机上的路径没有任何关系，
猜错了比不猜更坏。远程仍要求显式 `--path`，报文里已经写清楚了。

### 3.5 本文交付给 W3a 的不变式

做完之后，W3a 可以直接依赖这三条：

1. **凡是 agentd 派发过的仓库，都在 `repos` 表里**——新任务由 3.1 保证，历史任务由 3.3 尽力回填，
   回填不了的在启动日志里有明确原因。
2. **每条登记都有非空 origin**（`repoOriginURL` 的既有规则，回填也遵守）。于是
   `project_id = sha256(normalizeGitURL(origin_url))[:16]` 在任何一台机器上都算得出同一个值，
   跨机对话不需要任何协调，也不需要派发前多跑一轮读 ID。
3. **`repos` 表可以被 W3a 当作项目的投影源**，而不必再担心「表里有的只是全部的一小半」。

## 4. 影响面

**这是一个破坏性变更。** 明确列出来，不藏在实现里：

| 影响 | 说明 | 缓解 |
|---|---|---|
| 未登记的裸路径派发开始被拒 | 400 + `ErrRepoNotRegistered` | 报文自带 `repo add` 命令（3.2）；存量路径多半已被回填（3.3） |
| **本机派发同样受影响** | 本机 dispatch 也经 agentd，规则一致 | `handoff repo add` 在仓库目录里零参数可用（3.4） |
| 既有测试里用路径 dispatch 的用例会红 | 数量不小，但都是机械改动 | 加一个 `registerTestRepo(t, env, path)` 助手，在建任务前先登记；不要给测试开旁路 |
| 文档里的 `--repo /path/to/repo` 示例过时 | `README.md`、`skills/handoff` | 同一次改掉：示例改用登记名，并在 dispatch 段落补一句「仓库需先 `repo add` 登记」 |

`--no-sync-check`、`--allow-dirty`、`--base` 等既有 flag 与本文无关，行为不变。

**不提供 `--allow-unregistered` 之类的逃生口。** 那等于把刚堵上的洞重新开一个带名字的版本，
半年后所有脚本都会带上它。逃生口就是 `repo add`（一条命令）与 `repo rm`（用完注销）。

## 5. 测试

**`resolveRepoInput` 表驱动穷举**（纯函数，是这次改动的核心，必须打满）：

| 输入 | entries | 期望 |
|---|---|---|
| 路径，与某条登记 Clean 后相等 | 有 | 返回该条的 `Path`（**不是**输入原文） |
| 路径，带尾斜杠 / 含 `./` | 有等价登记 | 命中 |
| 路径，无任何登记匹配 | 有若干 | `ErrRepoNotRegistered`，报文含输入路径、已登记名字、`handoff repo add` |
| 路径，登记表为空 | 无 | 同上，报文含「（本机尚无任何登记）」 |
| 登记名命中 / 查不到 | — | 行为不变（回归） |
| 省略，origin 唯一命中 / 零命中 / 多命中 / cwd 非 git | — | 行为不变（回归） |

**回填**（每条一个用例）：

- 路径已登记 → 不重复登记；
- 路径不存在 / 不是 git 仓库 / 没有 origin → 跳过且各自 Warn 出可读原因；
- 正常路径 → 登记成功，名字取自 origin 末段；
- 名字冲突 → 落到 `name-2`；
- **幂等**：连跑两次，第二次因 `meta` 标记整体跳过，不产生任何写；
- **不复活**：回填 → `repo rm` 注销 → 重启 → 该条**不**回来。

**端到端**：未登记路径 dispatch → 400，响应体 `error` 原文含 `handoff repo add`；
`repo add` 之后同一条命令成功。

`go build ./...`、`go test ./...`、`go vet` 全绿是底线。

## 6. 风险与回滚

**风险**：某台执行机上有历史任务，但仓库目录已被移走或改名——回填跳过它，下一次 dispatch 被拒。
这是可接受的：报文会告诉他路径未登记以及怎么登记，而「仓库已经不在原处」本来就是他需要知道的事实。

**回滚**：改动面很窄——`resolveRepoInput` 的一个分支、一个启动期回填函数、一张 `meta` 表、
`repo add` 的一个默认值。revert 即恢复旧行为；回填已写入的登记留在表里也无害（它们本来就该在）。
