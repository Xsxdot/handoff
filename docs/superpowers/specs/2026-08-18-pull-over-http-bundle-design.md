# 回程 pull 改走 agentd HTTP 面（git bundle）设计（B143）

> 状态：设计已确认（2026-08-18），待实现。
> 背景缺陷：backlog B143。发现于 [Windows 执行机全方位真机验证](../notes/2026-08-18-windows-executor-full-acceptance.md) §3 D2。

## 1. 问题与根因

`handoff pull` 与任务结束时的自动同步，在 Windows 执行机上完全不可用。两个
**独立**原因叠加，各自都足以致命。

### 1.1 ssh 主机由 agentd 的 HTTP 地址推导

`sshHostFromTarget`（`cmd/attach.go:74`）把 `Target.Addr` 去掉端口就当 ssh 目标，
而 `Target` 只有 `Addr / Token / User` 三个字段（`internal/config/config.go` 的 `Target`，撰写时在 187 行），
没有任何位置能单独写 ssh 主机。

win-b37 的 agentd 经 ssh 隧道暴露在 `127.0.0.1:17777`，于是 pull 去 ssh
`Administrator@127.0.0.1`——那是**协调者本机**，不是执行机。实测报
`Host key verification failed`。

这与 Windows 无关：任何经隧道或端口转发接入的执行机都中招。但 win-b37 恰恰是
**必须**走隧道的那台（公网 IP，B127 记录过被爆破，不能直接暴露 agentd 端口）。

### 1.2 git 的 ssh transport 假定远端是 POSIX shell

`git fetch user@host:<路径>` 不是 git 自己连过去的，它 fork 一个 ssh，把要在远端
执行的命令拼成一个字符串：

```
ssh user@host "git-upload-pack 'C:/Users/administrator/.handoff/repos/handoff'"
```

那对单引号由 git 主动加上（`sq_quote_buf`，shell 引用），因为路径可能含空格。
接下来两条路分岔：

- **远端是 POSIX shell**：sshd 把这串交给 `/bin/sh`，sh 解析后**剥掉单引号**，
  `git-upload-pack` 收到 `C:/Users/...`，正确。
- **远端是 cmd.exe**：cmd 的引用规则里没有单引号这个概念，它是普通字面字符。
  `git-upload-pack` 收到的是带引号的 `'C:/Users/...'`，找不到仓库。

责任不在 Windows 也不在 git，在一条**没有写下来的契约**：git 的 ssh transport
假定远端登录 shell 是 POSIX。

### 1.3 物证（08-18 win-b37 实测）

- 该机 `HKLM:\SOFTWARE\OpenSSH\DefaultShell` **未设置**，sshd 因此用 cmd.exe
- 远端仓库与分支**确实存在**：`git log --oneline -1 tmp/wintest-a` → `1c63509b`
- 反斜杠 / 正斜杠 / `ssh://` 三种路径形式**全部**失败于同一签名——远端路径被套了
  两层单引号，`does not appear to be a git repository`

前两条排除了「仓库不存在」，第三条排除了「路径写法不对」。

## 2. 目标与非目标

**目标**：让协调者能把远程任务分支取回本地 git，且不依赖执行机上有 ssh、不依赖
远端登录 shell 的形态、不依赖 ssh 主机能从 agentd 地址推导。

**非目标**：

- 不改 `localsync` 的职责与行为（见 §3.2——它恰好不需要改）
- 不动 `dispatch` 的去程校验（那条链路是好的）
- 不删 ssh 路径：老 agentd 仍需要它（§7）
- 不为 `Target` 增加 ssh 主机字段。该字段只能修 §1.1 那一半，对 win-b37 而言
  等于没修——它会从「连错机器」变成「连对机器，然后照样死在引号上」

## 3. 架构与放置

### 3.1 一个新端点

```
GET /api/tasks/{id}/bundle?have=<sha>
→ 200 application/octet-stream   git bundle 的原始字节，带 Content-Length
→ 204 No Content                 have..branch 为空区间，本地已是最新
```

服务端在**主仓库**（`task.RepoPath`）执行：

```sh
git bundle create <临时文件> <have>..<branch>   # 有 have：薄包
git bundle create <临时文件> <branch>           # 无 have：全量
```

**为什么先落临时文件而不是直接写进 `ResponseWriter`**：直接写的话，git 中途失败时
响应头早已发出，客户端收到的是一个**截断的 200**——一次服务端故障被伪装成一个
内容不完整的成功。先落盘则可以在失败时干净地返回 500，成功时带上 `Content-Length`。
代价是服务端一次磁盘写，随即 `defer os.Remove`。

**必须用 `RepoPath` 而不是 `Workdir()`**：worktree 是主仓的从属工作树，分支对象
在主仓库里。`cmd/pull.go:56` 的现有注释已经写明这一点，新端点沿用同一判据——
注意这与 `handleTaskDiff` 不同，那个用的是 `Workdir()`，因为它要的是工作树状态。

路由挂在既有的 `/api/tasks/{id}/...` 族里，与 `diff` / `file` / `render` 同级，
走 `s.byTask` 包装（任务不存在时统一 404）。

### 3.2 客户端几乎不写新逻辑

**`localsync.Fetch` 一行都不用改。** 它的文档注释里就写着「RemoteURL……也接受
本地路径，git 同一条路径处理」，而 git 把 bundle 文件当作一种合法 transport：
`git fetch <某.bundle> <branch>:<branch>` 直接可用，前置对象缺失时 git 自己报
`Repository lacks these prerequisite commits`。

所以客户端流程是：

1. `client.Bundle(ctx, taskID, have)` 取回字节流
2. 写进 OS 临时目录的临时文件，`defer os.Remove`
3. 把该文件路径当 `RemoteURL` 交给现有的 `localsync.Fetch`
4. 结果渲染（`syncMessage`）与今天完全一致

**`wait` 的自动同步与 `pull` 共用 `syncTaskBranch`，因此一并被修好。**

### 3.3 承重前提已实测（08-18，本机 git 2.x）

设计的支点都不是推断，是跑出来的。判据错的代价比实现错大——执行者遇到错的
判据只能停下来问，所以这几条在写 plan 之前先验掉。

| 前提 | 命令 | 实测结果 |
|---|---|---|
| 薄包能生成且极小 | `git bundle create - <base>..feat/x` | `rc=0`，单个提交的包 **364 字节** |
| 空区间是**失败**而非空包 | `git bundle create - feat/x..feat/x` | `rc=128`，`fatal: Refusing to create empty bundle.` |
| 空区间的正确判据 | `git rev-list --count feat/x..feat/x` | `0`（这才是 §5 该用的判据，不是匹配 stderr 文案） |
| **bundle 可当 URL 直接 fetch** | 在只有 base、没有 feat/x 的仓库里 `git fetch <包> feat/x:feat/x` | `* [new branch] feat/x -> feat/x`，`git log feat/x` 见到 work 与 base 两个提交 |
| 缺前置对象的报文 | 在空仓库里 fetch 同一个薄包 | `error: Repository lacks these prerequisite commits: 584001c…` |

第四行是整个设计的支点：**它成立，`localsync` 才一行都不用改。**

另有一条顺带确认、对设计有利的性质：**客户端重复 fetch 同一个包是无操作、不报错**。
所以 §5 的 204 是**服务端**的正确性要求（空区间造不出包），客户端侧本身已经幂等——
两者不重复，各管一段。

## 4. 基线协商

`have` 取 `task.BaseCommit`。该字段客户端已经有了——`pull` 的第一步
`client.Attach(ctx, taskID)` 取回的就是任务快照，`syncTaskBranch` 拿到的
`*proto.Task` 里带着它，不需要额外请求。

但**发出前先在本地核实自己真有**：

```sh
git cat-file -e <sha>^{commit}
```

核实不过就不带 `have`，服务端给全量包。**不能假设协调者有** ——它可能换了一台
机器接管、或在另一个克隆里执行 pull。

服务端反向也校验：`have` 给了但任务仓库里没有该对象 → **400 并带上那个 sha**。
客户端只会回传任务记录里的值，所以这种情况意味着真的出了异常，应当响亮地失败，
而不是悄悄退回全量——后者会让「协调者拿到的包比预期大得多」这件事无声发生。

性质：**薄包是常态且极小**（就是这次任务的几个提交），全量只在协调者手上没有
基线时发生，是罕见退路。

## 5. 空区间必须单独处置

`have..branch` 为空区间时 `git bundle create` **不是成功产出空包，而是失败**，
报 `Refusing to create empty bundle.`。

**08-18 真机验收修正了这里的一句错话**：原文写的是「连着 pull 两次就会撞空区间」，
**不成立**。`have` 取的是 `task.BaseCommit`，永远不是分支 tip，所以第二次 pull
送的还是同一个 base，区间照样非空——实测第二、第三次 pull 都照样下载了那个
531 字节的薄包，从未回 204。空区间**只在任务一个提交都没产生时**发生（真机验收
里的任务 `209628dc` 正是这种）。204 的处置仍然必要，但触发条件比原文窄得多。

处置：服务端识别该情形，返回 **204 No Content**；客户端据此合成
`localsync.Result{Created: false, NewCommits: 0}`，报「本地已是最新」，
**并且必须保证本地分支引用存在**——见 §5.1。

### 5.1 204 也必须把本地分支引用建出来（08-18 真机验收发现）

初版实现在 204 时直接短路返回，不调 `localsync.Fetch`，于是**本地分支引用根本
没被创建**，而输出仍是「已是最新」。实测：pull 任务 `209628dc` 报「已同步分支
tmp/wintest-g（新增 0 个提交）」，随后 `git rev-parse --verify tmp/wintest-g`
报 `Needed a single revision`——协调者手上什么都没有。

这是相对 ssh 老路的**静默行为倒退**。纯 git 对照实测：目标仓库没有该 ref、
且没有任何新提交时，`git fetch <远端> <分支>:<分支>` **照样把 ref 建出来**
（空区间用例里 `feat/zero` fetch 后指向 `60c9680`）。

修法：服务端在 204（以及 200）响应上带一个 `X-Handoff-Branch-Head: <sha>` 头；
客户端收到 204 时，若本地尚无该分支引用，就用这个 sha 建出来。空区间意味着
分支 tip 从 `have` 可达，所以**客户端一定已经有这个对象**，不需要再取任何数据。
拿不到该头（对端是更早的中间版本）时，如实报「远端无新提交，但本地尚无该分支
引用」，不要谎称已是最新。

识别方式：先跑 `git rev-list --count <have>..<branch>`，为 0 直接返回 204，
不去调 `bundle create`。**不要靠匹配 stderr 文案判断**——那是英文文案，随 git
版本变，且把「预期形态」的判据建在字符串比较上，与 B141 那类「判据用代理条件」
是同一个错误。

**不识别它，第二次 pull 就会变成一个 500。**

## 6. 降级阶梯

客户端在 `syncTaskBranch` 里：

| 情形 | 处置 |
|---|---|
| 端点 404 | 映射为 `client.ErrBundleUnsupported` → **退回 ssh 老路**，并打一行说明本次走了哪条 |
| 其它 HTTP 错误（400 / 401 / 500） | **如实报错，不回落** |
| 200 / 204 | 按 §3.2 / §5 处理 |
| 包拿到了但 `git fetch` 失败 | 如实报错，**不回落**——包已经到手，说明 HTTP 这条路是通的，失败在 git 侧（如缺前置对象），换 ssh 重来只会掩盖它 |

第二行是承重的，沿用 `ErrStatusUnsupported` 的既有纪律——`internal/client/client.go`
里那个哨兵（撰写时在 335 行）的注释就把它讲成「一条**有用的结论**」：
**404 是结论（对端过旧），其它是故障**。
故障时回落会把一次真失败伪装成「老路也能跑」，把问题藏起来。

**404 的歧义如何排除**：`byTask` 对「任务不存在」也回 404，与「对端没有这个路由」
状态码相同。排除依据在调用顺序上——`pull` 的第一步是 `client.Attach(taskID)`，
它成功返回才轮到 `Bundle`，**任务存在已被上一次请求证明**。因此这里的 404 只能
来自路由缺失。实现不去比对响应体文案（那同样是把判据建在字符串上）。

服务端错误映射：

| 情形 | 状态码 |
|---|---|
| 任务不存在 | 404（`byTask` 已有） |
| 任务无分支 | 400 |
| `have` 在任务仓库中不存在 | 400，报文带该 sha |
| `git bundle create` 失败 | 500，带截断的 stderr（照 `handleTaskDiff` 的 `truncateRunes(err, 200)`） |

## 7. 为什么保留 ssh 路径

mac-02 当前跑的是老 agentd（`console-optim-99c4ab16c`），没有这个端点。保留回落
使得**升级与否都不会把它的 pull 弄坏**：Windows 走新路，POSIX 机器新旧都能用。

代价是 `cmd/pull.go` 多一个分支、`sshHostFromTarget` 与 `Target.User` 暂时不能删。
这是有意接受的——删它们要等全部执行机换版之后，那是另一件事。

## 8. 体积、超时与临时文件

- **不设人为体积上限。** 一个会拒绝合法全量包的上限，是把能用的路径改成坏的。
  改为**两侧都记录字节数**，让异常可见。
- 超时沿用 `localsync.FetchTimeout`（2 分钟）的量级；下载与 fetch 各自计时。
- 服务端的临时文件同样落 OS 临时目录并 `defer os.Remove`——**不能落进任务仓库**，
  那会让 `dispatch` 的干净工作区校验误报。
- 临时文件落 **OS 临时目录**（`os.CreateTemp`），`defer os.Remove`。
  **绝不能落在本地仓库里**——那会把协调者的工作区弄脏，而干净工作区是
  `dispatch` 的前置条件。

## 9. 日志纪律

- 服务端：请求进入打 `task` / `have` / `branch`；完成打 `bytes` 与耗时；
  204 单独打一条 Info 说明是空区间而非失败。
- 客户端：打**本次走了哪条路**（bundle / ssh 回落）——这是排障时第一个要问的问题；
  回落时把 404 的事实一并写出，不要只说「用了 ssh」。
- **任何情况下不打 token、不打 bundle 内容。**

## 10. 测试策略

单测：

1. 服务端表驱动：薄包正常 / 无 `have` 走全量 / `have` 不存在 → 400 /
   空区间 → 204 / 任务不存在 → 404
2. 客户端：404 → `ErrBundleUnsupported`；其它状态 → 普通错误
3. **降级阶梯的反面断言（承重）**：客户端返回 500 时，`syncTaskBranch` 必须报错
   且**不**调用 ssh 路径。少了这条，把「其它错误也回落」写回去照样能过
4. 进程内端到端：`t.TempDir()` 里建真 git 仓库 → 跑真 handler 取包 → 把包 fetch
   进第二个仓库 → 断言那个 commit 真的到了。不需要网络

第 3 条与第 4 条是承重的：前者守住 §6 的纪律，后者证明这条链路真的能搬运对象，
而不只是 HTTP 状态码对了。

## 11. 真机验收

**留协调者本地执行，不派发**——判据要驱动隧道与 agentd 本身（B126 的教训）。

1. 向 win-b37 派一个会产生提交的任务 → `completed`
2. `handoff pull --target win-b37 <完整 id>` → **分支真的落到本地**，
   `git log <分支>` 见到那个 commit。这正是改前做不到的那一条
3. **再 pull 一次** → 报「已是最新」而不是 500（§5 的空区间路径）
4. 任务结束时 `wait` 的自动同步不再打「自动同步跳过: …… Host key verification failed」
5. **对照组**：mac-02 仍是老 agentd，pull 应仍走 ssh 且成功，客户端日志明确写出
   走的是回落——证明 §7 没被改坏

第 5 条是刻意留的：只验新路径通，证明不了回落还在。

## 12. 已知边界

### 12.1 改 Windows sshd 的 DefaultShell 是一条已知手工替代方案（本轮未实测）

把 `HKLM:\SOFTWARE\OpenSSH\DefaultShell` 指到 `C:\Program Files\Git\bin\bash.exe`
（或 PowerShell），远端就会正确剥掉 git 加的单引号，§1.2 那一半随之消失。这是
广为流传的标准解法。

**记为替代方案而非修复路径，三个理由**：

1. **本轮未实测。** 改注册表是系统设置，按纪律没动，所以这条是「机制上成立」，
   不是「验过」。
2. **整机生效**，影响那台机器上所有 ssh 会话，不只是 git。
3. **它不解决 §1.1。** 隧道那一半是 handoff 自己的设计问题，与 Windows 无关。

而且它是一台机器上的一次性手工设置，没有任何东西会提醒下一台 Windows 执行机
也要做。本设计选择把整条依赖链拿掉，而不是在每台机器上维护一个前置条件。

### 12.2 全量包可能很大

协调者手上没有基线时会走全量。本仓库的全量包是百 MB 量级。按 §8 不设上限、
只记录字节数——这是有意的取舍：拒绝它会把一条能用的退路变成死路。

### 12.3 `sshHostFromTarget` 与 `Target.User` 成为遗留

保留期内它们只服务于回落路径。全部执行机换版之后可以一并删除，那时
`Target` 只剩 `Addr / Token`。本设计不做这一步。

## 13. 影响面

- 平台无关：三个 OS 同一套代码路径，不是 Windows 特有修复
- 对已有用户：老 agentd 走回落，行为与今天逐字节一致；新 agentd 走 bundle
- `pull` 与 `wait` 的自动同步共用 `syncTaskBranch`，两者一起被修好
- 不改任何已有函数签名；`localsync` 包一行不动
