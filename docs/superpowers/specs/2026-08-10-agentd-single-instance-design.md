# agentd 单实例保护设计（B34）

> 状态：设计已确认，待写实现计划
> 来源：`docs/superpowers/backlog.md` B34 行（08-10 从 B33 分出）

## 0. 背景：为什么这条不能靠自觉

B33（`handoff status`）的 spec §1.4 把单实例锁明列为**非目标**——B33 只做「看得见」，让人能发现同机已有 agentd 在跑；它不做「拦得住」。

于是现状是：**代码层面没有任何东西阻止第二个 agentd 起在同一个 DataDir 上**。触发 B33 的那个原始事故里，一个 agent 已经准备在 devbox 上另起一个 agentd —— 只是恰好没起成。

有人会以为「端口冲突自然会挡住第二个」。**这个假设是错的**，这是本设计最关键的一条事实：

```
config.Load → logx.Setup → MergeLoginShellPATH → MkdirAll(DataDir)
    → store.Open → NewServer → RecoverOnStartup → RunWatchdog → ListenAndServe  ← 端口在这里才绑
```

`ListenAndServe()` 是 `cmd/agentd.go` RunE 的**最后一条语句**。在它之前，`RecoverOnStartup`（`internal/agentd/watchdog.go`）已经跑完了：

```go
if probe(t.ID) {  // probe == mgr.ResumeTask —— 会真的重建 SSE 订阅、重启中介循环
    recovered++; ...; continue
}
if t.State == proto.TaskStateWaitingReview { kept++; ...; continue }
failed++
reconcileExecutorGone(st, hub, t.ID, "agentd 重启后执行器已不在", log)
```

也就是说，**第二个 agentd 在撞端口失败之前，已经对在役 agentd 的活执行器重建了订阅、并写入了状态迁移**。破坏在端口冲突之前就发生了。SQLite 那一层也拦不住：`store.Open` 用 `?_pragma=journal_mode(WAL)`，WAL 允许多进程打开同一个库，第二个进程在这里不会报错。

所以本设计要做的，是**在破坏发生之前**把第二个 agentd 挡在门外。

## 1. 目标与非目标

**目标**：一个 DataDir 同时只接纳一个 agentd。第二个必须在**碰到任何数据之前**失败，并且失败信息要能直接指向下一步动作。

**非目标**：

- **跨 DataDir 的仓库级互斥**。有人会想「同一个仓库不该被两个 agentd 接管」，但 agentd **不是 repo-scoped 的**——`proto.Task.RepoPath` 是**每个任务各自的字段**，agentd 启动时手里根本没有「仓库」这个键，无从加锁。仓库级互斥只有在派发任务时才有意义，那是另一个题目，本设计不碰。
- **优雅关停时的锁释放**。不需要：flock 由内核在进程终止时释放，`kill -9`、panic、断电重启后一样干净。
- **跨机器的锁**。flock 是本机语义。两台机器各跑各的 agentd 本来就是 handoff 的正常形态。

## 2. 机制：flock

在 `<DataDir>/agentd.lock` 上做 `syscall.Flock(fd, LOCK_EX|LOCK_NB)`，持有的 `*os.File` 存活到进程结束。

选它的三条理由：

1. **零新依赖**。`syscall.Flock` 在 darwin/linux 都是标准库；handoff 只跑这两个平台。
2. **不存在陈旧锁**。锁挂在打开的文件描述上，进程无论怎么死（正常退出 / panic / SIGKILL / 掉电），内核都会释放。**"进程死了锁还在" 这个状态根本不存在**，所以不需要写 PID、不需要 `kill -0` 探活、不需要 `--force` 逃生口。这是它相对 PID 文件的决定性优势。
3. **语义已实测**。锁挂在 open file description 上，因此**同一个进程内两次 `open()` 也会互斥**——这条对测试设计至关重要，见 §6。

不选的两个方案，理由记在这里免得以后重问：

- **端口占用**：见 §0，绑定发生得太晚，破坏已成事实。
- **PID 文件**：要自己处理陈旧锁（进程死了文件还在）、PID 复用、以及随之而来的 `--force` 逃生口。**引入了本可以不存在的状态**。用户明确选了「不给逃生口，硬失败」，PID 文件方案的复杂度就全是白付的。

## 3. 获取时机

插在 `os.MkdirAll(cfg.DataDir)` 与 `store.Open` 之间：

```
config.Load → logx.Setup → MergeLoginShellPATH → MkdirAll(DataDir)
    → ★ 获取 DataDir 锁 ★
    → store.Open → NewServer → RecoverOnStartup → RunWatchdog → ListenAndServe
```

三个约束共同定死了这个位置：

- **必须在 `MkdirAll` 之后**：首次运行时 DataDir 还不存在，锁文件没地方放。
- **必须在 `store.Open` 之前**：这是「碰数据」的第一步。虽然 WAL 让它不会报错，但真正的破坏从 `RecoverOnStartup` 开始——卡在 `store.Open` 之前是唯一能保证「什么都没动过」的位置。
- **必须在 `logx.Setup` 之后**：否则撞锁失败的日志无处可去，排障时 `agentd.log` 里一片空白。代价是有一个很窄的窗口：两个 agentd 会各自往同一个 `agentd.log` 追加几行。可以接受——追加写不会互相截断，而且第二个进程紧接着就退了。

## 4. 撞锁时的表现

`RunE` 返回错误，cobra 打印后退出码 1。`PersistentPreRun` 已设 `SilenceUsage`，不会再糊一屏用法出来。

错误文案：

```
数据目录 /Users/sycm/.handoff 已被另一个 agentd 占用（agentd.lock）。
同一个数据目录同时只能有一个 agentd——两个进程会抢同一份 SQLite、
同一批 worktree 与 tmux 会话，正是状态机最怕的失配。
先看现役那个是谁：handoff status
它能用就直接复用，不要再起一个。
```

设计要点：

- **不试图报出锁的持有者**。用户选了朴素 flock 而非「锁文件里写 PID」的变体——报持有者就得往锁文件里写内容并读回，而那内容随时可能是陈旧的（写入与读取之间进程可能已死），为一个诊断信息重新引入本已消除的状态，不划算。
- **最后两行才是重点**：不止说「被占了」，而是给出下一步动作。B33 刚做出来的 `handoff status` 在这里自然接上——它会打印在役 agentd 的版本、启动时间、任务分布与执行器存活。
- **没有 `--force`**。用户明确选了「不给逃生口，硬失败」。理由：这个锁保护的是不可逆的状态破坏，而 flock 不存在陈旧锁，所以不存在「锁明明该开却开不了」的合法场景。真要强上，`rm agentd.lock` 也不管用（锁在 fd 上不在路径上），只能先停掉在役那个——这恰恰是我们希望用户走的路。

## 5. 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/agentd/lock.go` | 新建 | `AcquireDataDirLock(dataDir string, log *slog.Logger) (*DataDirLock, error)` 与 `(*DataDirLock).Release() error` |
| `internal/agentd/lock_test.go` | 新建 | §6 的六条用例 |
| `cmd/agentd.go` | 修改 | §3 的插入点接线一处 + `defer lock.Release()` |
| `skills/handoff/SKILL.md` | 修改 | 「绝不为同一个仓库起第二个 agentd」这条红线改写（§7） |

接口形态：

```go
// DataDirLock 持有一个 DataDir 的独占权，直到 Release 或进程退出。
type DataDirLock struct{ f *os.File }

// AcquireDataDirLock 对 <dataDir>/agentd.lock 取非阻塞独占锁。
// 已被占用时返回带可行动指引的错误（见 §4）。
func AcquireDataDirLock(dataDir string, log *slog.Logger) (*DataDirLock, error)

// Release 释放锁。生产侧可有可无——进程退出内核即释放；
// 保留它是为了测试能验证「释放后可重新获取」，以及 defer 的习惯写法。
func (l *DataDirLock) Release() error
```

日志（按 `instrumenting-code`）：成功获取打 Info（带 DataDir 与锁文件路径），撞锁打 Error（带 DataDir + cause），`Release` 打 Debug。成功路径不静默。

## 6. 测试

§2 已实测：同一进程内两个 fd 就能互相排斥。所以全部用例都是普通单测——**不起子进程、不碰 tmux、不需要真的拉起 agentd**：

1. 首次获取成功，`agentd.lock` 被创建
2. 同一 DataDir 第二次获取失败
3. 错误信息含 DataDir 路径与 `handoff status` 指引
4. `Release()` 后可重新获取
5. 两个不同 DataDir 互不干扰
6. DataDir 不存在时返回可读错误而非 panic

**一条如实说明**：「锁必须在 `store.Open` 之前」这个**顺序**约束不写测试。要测它得真起两次 agentd 并断言数据库未被改动，成本远超收益。它靠 `cmd/agentd.go` 插入点的注释（写明三个约束的理由）和 code review 保证。这里把取舍写明，而不是假装测过了。

## 7. 落地后的连带动作

**SKILL.md 红线改写**。`skills/handoff/SKILL.md` 里那条「绝不为同一个仓库起第二个 agentd」目前是**靠自觉**的约定。落地后它变成代码事实，措辞应改为「起不来——会明确报错并告诉你怎么办」，并顺带指向 `handoff status`。约定和实现不一致会侵蚀文档的可信度。

**一条运维影响**。这个特性上线后，升级 agentd 的流程多一步：**必须先停旧的再起新的**。以前是新的起来撞端口失败（但已经造成破坏），以后是新的直接被锁挡住、什么也没动。行为更安全，但不能再指望「起个新的把旧的顶掉」。这条要写进 SKILL.md 的运维段落。

（具体例子：devbox 上那个还在跑旧二进制、没有 `/api/status` 的 agentd，将来升级就得走这个流程。）
