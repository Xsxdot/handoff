# 任务进程足迹：可识别、可回收、可见（B69 + B70）设计

## 1. 范围与动机

今天 agentd 对「一个任务在机器上占了哪些进程」这件事**既不认识、也不回收、更看不见**。

两条缺口，共用同一个缺失的原语——**任务的进程足迹**：

- **B69（回收）**：executor **自己死掉**时，没有任何一条路径清扫它留下的后代。
- **B70（可见）**：agentd 不知道自己在机器上占了多少进程，出事时也说不出离上限还有多远。

它们要的是同一份数据：B69 拿它去**杀**，B70 拿它去**数**。分开做会把这个原语设计两遍，且第二次必然迁就第一次的形状——所以合成一个 spec。

### 1.1 触发事件

2026-08-12，devbox（macOS，`kern.maxprocperuid = 2666`）整机 fork 瘫痪：任何进程都 fork 不出子进程，`handoff run` 报 `fork/exec /bin/sh: resource temporarily unavailable`，ssh 报 `exec request failed on channel 0`，sftp 子系统同样起不来。agentd 自身健康（HTTP 正常应答、DB 完整、状态机自洽），只是和别人一样 fork 不动。

时间线（从事件流复原）：

| 时间 (UTC) | 事件 |
|---|---|
| 03:41 | 任务 `4bb7171e` 起，随即连续派发 7 个 subagent，每个跑 `go test ./...` + `gofmt` |
| 03:54 | 任务 `ea6b8f06` 起 |
| 03:55:50 | `ea6b8f06` 申请 `subagent-driven-development` skill 目录权限 |
| 03:58:37 | `4bb7171e` completed |
| **04:01:08.596** | `4bb7171e` 的 `opencode serve 已退出` |
| **04:01:08.672** | `ea6b8f06` 的 `opencode serve 已退出` |

两个独立任务的 executor **在 90 毫秒内先后死亡**——共享资源见底，不是两个各自的 bug。故障在两个 executor 都死掉之后**持续了 45 分钟仍未自愈**，直到人工重启机器。

### 1.2 不在范围内

- **并发限流 / 进程预算**。要不要限、限在哪一层（agentd 并发任务数、按机器 nproc 预算、还是纯派发者纪律）尚未收敛；而定阈值必须以数据为前提，本 spec 先把数据造出来。**这是本 spec 最重要的一条边界**：见 §2.6，事故量级至今无法解释，此时定任何阈值都是猜。
- **自动清扫已归档任务的历史欠账**。本 spec 的清扫只覆盖「当下」与「agentd 重启窗口」两类（§3.4）。历史任务只**读**不动手（`handoff footprint`，§3.5）。
- **修改 `prochost.Kill` 的既有契约**。见 §3.1 的方案取舍。

## 2. 事实基础：读码与真机复核结论

实现前必须以之为准的事实，全部已复核。

### 2.1 executor 自然死亡时，`Kill` 根本不发信号

`prochost.Kill`（[prochost.go:144](../../../internal/prochost/prochost.go)）在存活锁已释放时**直接返回、绝不发信号**：

```go
if !aliveFn(h) {
    log().Info("存活锁已释放，无需回收", "pid", h.PID, "lock", h.LockPath)
    return nil
}
```

这条规则本身**是对的**——对已回收的 pid 发信号有误杀被复用 pid 的风险，`TestKillSkipsWhenLockFree` 明确钉住「锁已释放时绝不发信号」。

问题在于链条：executor 自行退出 → shim 的 `cmd.Wait()` 返回 → shim 退出 → 存活锁被内核释放 → **此后任何 `done` / `stop` 走到 `Kill` 都是空操作**。而 `stopExecutor`（[reconcile.go:72](../../../internal/agentd/reconcile.go)）只在 `Kill` 返回 `ErrStillAlive` 时才提示人工——自然死亡这条路径连提示都不会有。

**结论**：回收只覆盖「我们主动杀」，不覆盖「它自己死」。而后者是更常见的那条。

### 2.2 后代继承 pgid，不逃逸（真机证据）

2026-08-12 devbox 重启后、3 个任务并发运行时实测：

```
pid   ppid  pgid  stat  comm
1859     1  1856  SN    handoff     ← agentd
1910  1859  1910  SNs   handoff     ← shim（会话组长，pgid == pid）
1911  1910  1910  RN    opencode    ← executor，在 shim 组内
8707  1859  8707  SNs   handoff     ← shim
8708  8707  8707  SN    claude      ← executor
8723  8708  8707  SN    handoff     ← executor 拉起的 handoff CLI（孙进程），仍在组内
16121 1859 16121  SNs   handoff
16122 16121 16121 SN    opencode
```

孙进程 `8723` 留在 shim 的进程组里。全机唯一一个 `ppid=1` 自成一组的 `node` 经核实是无关应用（`local-browser-worker`），不是逃逸出来的后代。

**这推翻了排障初期的一个假设**：[prochost.go:164](../../../internal/prochost/prochost.go) 的「可能有逃逸出进程组的后代」是一句防御性措辞，不是观测到的事实。**但这条观测是 executor 相关的**：本节的孙进程是 **claude** executor 拉起的（继承组内 pgid）；而同一批复核（本 spec 落地当天）实测 **opencode 的 Bash 工具用 `setsid` 把每条命令放进全新会话+进程组**，其子进程不在 shim 组内——「不逃逸」只在部分 executor 上成立，pgid 因此**不是**够用的全量足迹标识（判据的明确定界与盲区见 §3.2 规则二）。

### 2.3 `reconcileExecutorGone` 已是「executor 已不在」的唯一收尾点

[reconcile.go:160](../../../internal/agentd/reconcile.go)，三个调用点：

| 调用点 | 场景 |
|---|---|
| [manager.go:1304](../../../internal/agentd/manager.go) | 事件流终结（进程退出或连接断开） |
| [watchdog.go:222](../../../internal/agentd/watchdog.go) | agentd 重启后执行器已不在 |
| [manager.go:2340](../../../internal/agentd/manager.go) | 工单投递失败 |

这个触发面**恰好等于本 spec 需要覆盖的两类场景**（当下 + 重启窗口）。清扫因此不需要任何新调用点。

注意它有一条提前返回：状态非 `running`/`waiting_answer` 时空操作。清扫**不能**受这条约束，见 §3.4。

### 2.4 `Done` 不删任务目录，`proc.json` 长期留存

`Manager.Done`（[manager.go:994](../../../internal/agentd/manager.go)）清理的是 **managed worktree**，**不删** `~/.handoff/tasks/<id>/`。`stopExecutor` 的兜底回收也仍按该目录取凭据。

**结论**：历史任务（含已归档）的 `proc.json` 都还在，`handoff footprint` 的历史体检有据可读。事故当天 devbox 上有 105 个历史任务目录。

### 2.5 `status` 有时限纪律，历史体检不能塞进去

[status.go](../../../internal/agentd/status.go) 开头的既有约束：单任务探活 2s、总计 10s，「活跃任务再多，这条命令也不能变成慢命令」；且该文件明确**只读**，「不改任务状态、不发事件、不回收任何 executor 资源」。

**结论**：per-task 足迹计数可以进 `status`（只读、有界）；遍历上百个历史任务目录的体检**必须另开入口**。

### 2.6 事故量级仍然无法解释——本 spec 不假装能解释

重启后 3 个任务并发时的基线是 **346** 个进程，上限 2666，净空约 2300。两个任务的 subagent 扇出要吃掉 2300 个槽位，量级上说不通：`go test ./...` 的瞬时峰值撑死一两百。

也就是说：**要么当时存在持续累积，要么有一个尚未识别的放大器**。故障期间所有 exec 通道都被故障本身封死，拿不到进程计数；机器重启后现场即灭。

这是本 spec 把「限流策略」划出范围的直接理由（§1.2），也是 `handoff footprint` 存在的理由——它是此后唯一能安全回答这个问题的手段。

## 3. 设计

### 3.1 新原语：`Sweep` 与 `Footprint`

`internal/prochost` 新增两个**孪生**原语，`Handle` 增加一个字段：

```go
type Handle struct {
    PID       int    `json:"pid"`                  // shim pid；因 Setsid，它同时就是 pgid
    LockPath  string `json:"lock_path"`
    StartedAt int64  `json:"started_at,omitempty"` // shim 启动时刻（unix nano），身份校验的时间下界
}

// Verdict 是一次足迹判定的结论，三态如实返回。
type Verdict string

const (
    VerdictOK           Verdict = "ok"            // 身份校验通过
    VerdictLeaderReuse  Verdict = "leader_reuse"  // pgid 已被复用，整组放弃
    VerdictNoCredential Verdict = "no_credential" // 凭据不全（StartedAt 缺失），放弃
)

// Footprint 枚举组内通过身份校验的成员——只数不动。
// 对**存活中**与**已死亡**的 executor 均可调用，判据随存活锁状态切换（§3.2 规则一）。
func Footprint(h Handle) (members []int, v Verdict, err error)

// Sweep 回收一个已死执行者的残留后代。
// 前提是执行者**已死**：若存活锁仍被持有，直接拒绝执行并返回错误——杀活着的
// 执行者是 Kill 的职责，两者不得互相代劳。
func Sweep(h Handle) (killed int, v Verdict, err error)
```

`PID` 本身就是 pgid（`Setsid` 保证 `pgid == pid`），无须新记；真正缺的只是**时间下界**。`StartedAt` 落在 `Handle` 上，四个 adapter 的 `procInfo` 均内嵌 `Handle`，一处改动全覆盖。

**为什么是新原语而不是扩展 `Kill`**：两者的风险模型根本不同。

| | `Kill`（现有） | `Sweep`（新增） |
|---|---|---|
| 语义 | 杀一个**活着的**执行者 | 收一个**已死**执行者的残留 |
| 前提 | 存活锁**被持有** | 存活锁**已释放**（shim 已死） |
| 安全判据 | 锁在 ⇒ 组长在 ⇒ pgid 未被复用 | 组长已死，须逐个成员校验身份 |

给 `Kill` 加一个 `allowDeadLeader bool` 开关会把「不确认存活就绝不发信号」这条**用 300 条命令误杀 114 次换来的纪律**降级成一个参数——此后任何人传 `true` 就能绕过它，且函数文档要同时描述两套语义。方案否决。

**为什么 `Footprint` 与 `Sweep` 必须是孪生**：它们共用同一份成员枚举与校验实现，一个返回计数、一个发信号。这样「数出来的」和「会被杀的」在代码层面就是同一批进程，不可能对不上。§5 有一条测试专门钉住这点。

### 3.2 身份硬校验：三条规则

**规则一 · 组长身份判定（以存活锁为准），一票否决。** 分两种情形，**判据由存活锁状态决定**：

- **锁仍被持有** ⇒ 组长就是我们的 shim，pgid 不可能被复用（锁由内核在进程死亡时释放，`Alive` 的判据本身不受 pid 复用影响）。组内成员全部是我们的，正常计数。此时只有 `Footprint` 可调用，`Sweep` 拒绝执行（§3.1）。
- **锁已释放** ⇒ 组长（shim）已死。此时若组内仍存在 `pid == pgid == h.PID` 的**活进程**，那必然是内核把这个 pid 分配给了新进程、且它成了组长——pgid 被复用。整组放弃，不数也不杀，返回 `VerdictLeaderReuse`。

**这条区分不能省。** 若不看锁状态、一律把「存在 `pid == pgid` 的活进程」判成复用，那么对**所有正在运行的任务**（shim 本来就活着且就是组长）都会误判为复用而拒绝计数——§3.5 里 `status` 的 per-task 进程数将永远取不到值。

**规则二 · 会话封闭性（只封闭外侧）。** 通过规则一之后，组内**不会混入外部进程**：shim 调用过 `setsid`，该进程组属于 shim 独有的会话；而 `setpgid(2)` 要求目标进程组必须与调用者处于**同一会话**——会话外的进程加不进来。非组长成员要么由 fork 继承 pgid，要么根本无法从外侧进入该组。

**但内侧不封闭——这是本判据的盲区，必须如实写明：** 组内进程可以随时 `setsid` 自成新会话逃出去。08-12 真机实证（本 spec 落地当天复核）：opencode 的 Bash 工具把每条命令都 `setsid` 成**新的会话+新的进程组**（命令的 pgid == 它自己的 pid，STAT 带 `s`），executor 经 Bash 工具拉起的子进程（subagent 扇出、并行 `go test` 等）因此**不在 shim 的进程组里**，`Footprint`/`Sweep` 数不到也杀不到它们。**事后无法用祖先链补救**：`setsid` 改的是 pgid/sid、不改 ppid，进程树活着时沿 ppid 从 shim 能走到逃逸者，但清扫要工作的时刻正是执行者已死、子进程被 reparent 给 init/launchd 之后——ppid 恰好断在最需要它的地方。基于 ppid 的实现会得到一个「测试里好使、事故现场失效」的结果，比诚实的盲区更糟。**本特性因此只宣称「shim + executor 本体这一层」的占用与回收，不宣称「任务一共占了多少进程」**；补全占用口径的方案（树活着时由 shim 记账）已另立条目（backlog B72）走 brainstorm。

**规则三 · 时间下界，双保险。** 每个成员的启动时刻必须 ≥ `h.StartedAt`，否则将该成员排除（不计入、不杀）。规则二只挡外侧混入、挡不住内侧逃逸，这条补的是「比 shim 更早的进程必然不可能是它的后代」的下界——代价是**漏杀而非误杀**。

**为什么规则一让「重启回溯」也安全**：agentd 重启后清扫时，我们**不知道 shim 何时死亡**，时间窗口缺少上界，仅靠规则三挡不住重启期间产生的复用者。规则一不需要上界——它直接检测「这个 pgid 此刻是否被别人当作组长占用」，而那正是 pid 复用唯一能造成误杀的形态。

### 3.3 平台原语：不 fork 是硬要求

`platform_unix.go` 新增两类能力（非 unix 平台一律 no-op，沿用 `platform_other.go` 既有模式）：

1. **按 pgid 枚举组成员 + 读取各成员启动时刻**：macOS 走 `sysctl KERN_PROC`（`kinfo_proc` 同时带 `e_pgid` 与 `p_starttime`），Linux 读 `/proc/<pid>/stat`（`pgrp` 与 `starttime`，配合 `/proc/stat` 的 `btime` 换算绝对时刻）。
2. **按 uid 统计进程总数 + 读取上限**：macOS `kern.maxprocperuid`，Linux `RLIMIT_NPROC`。

**两类能力实现分离，不共用代码**——它们回答不同的问题：前者是「这个任务占了哪些」，后者是「这台机器还剩多少」。

**一律不得 fork**（不调用 `ps`/`lsof` 等外部命令）。理由是硬性的：这套代码要在**机器已经 fork 不动的时候**仍然可用，否则它会在最需要它的那一刻恰好失灵——事故当天所有基于 exec 的诊断手段全部失效，正是这个教训。

### 3.4 接入点与数据流

清扫挂在 `reconcileExecutorGone` 内，**无新增调用点**。

```
shim 启动    prochost.Start 记录 StartedAt → adapter 写 proc.json
                                   ↓
executor 死  shim 退出 → 存活锁释放 → agentd 观察到
                        （事件流终结 / 重启后 watchdog / 工单投递失败）
                                   ↓
             reconcileExecutorGone ── 状态收尾（作废工单、追加事件、迁 waiting_review）
                                   └─ Sweep(handle)   ← 无条件后置动作
                                        ├ ok            → 日志记 killed 数
                                        ├ leader_reuse  → 放弃，notifyOrphanRisk 上报
                                        └ no_credential → 放弃，notifyOrphanRisk 上报
```

**两处顺序必须显式遵守**：

1. **清扫在状态收尾之后**。审核者的工作流（任务进 `waiting_review`）不受清扫成败影响。
2. **清扫是无条件后置动作，不受该函数既有的提前返回约束**。当前实现在状态非 `running`/`waiting_answer` 时空操作返回；清扫必须绕开这条——它的前提（executor 已不在）本身就是清扫的触发条件，与任务状态无关。已 `done` 过的任务同样可能有残留，那次 `Kill` 正是因为锁已释放而空转掉了（§2.1）。

### 3.5 可见性：两个入口

**入口一 · `handoff status`**（快，只碰活跃任务）：

```
任务     running 3 · completed 68 · failed 40
进程     346 / 2666  (13%)
活跃
  a307d489  B71 …       running  opencode  executor 存活  12 进程
  52dd15df  seglog…     running  claude    executor 存活   4 进程
  8205ef68  w4a-frames  running  opencode  executor 存活   2 进程
```

全局的 `346 / 2666` 与 per-task 计数并排呈现：**只看前者不知道 handoff 占了多少，只看后者永远不知道离墙还有多远**。per-task 计数复用 `status` 既有的探活时限纪律（§2.5），不得使其变慢。

**入口二 · `handoff footprint`**（慢，含历史，只读）：遍历全部任务目录的 `proc.json`（含已归档，§2.4），逐个 `Footprint` 并汇总——哪个历史任务底下还挂着多少活进程。

它**只数不杀**。数出来之后要不要动手，是届时看数据再决定的事，不属于本 spec。

## 4. 错误处理与降级

**总原则**：清扫是后置动作，任何失败都不回头影响已完成的状态收尾。与既有的 worktree 清理失败降级同款——任务该进 `waiting_review` 就进，残留是运维问题而非任务问题。

| 情形 | 处置 |
|---|---|
| 清扫成功 | 仅日志（Info，含 killed 数） |
| 无残留可清 | 仅日志（Info） |
| `leader_reuse` / `no_credential` | 日志 Warn + `notifyOrphanRisk` 事件 |
| 平台枚举失败 | 日志 Error + `notifyOrphanRisk` 事件 |
| 已发信号，复核仍存活 | 日志 Error + `notifyOrphanRisk` 事件（与 `Kill` 一致） |

**上报要节制**。[reconcile.go:80](../../../internal/agentd/reconcile.go) 已有明确观点：Stop 失败五花八门，「全发事件等于把审核者淹了，那样这条提示就没人看了」。因此只有**「确实有残留、但我们没敢动」**才发事件；成功与无残留只进日志。

**发完信号必须复核**，沿用 `Kill` 的 `killVerifyBackoff` 窗口（B47 的教训）：复核窗口走完仍存活则如实上报，不假装成功。

## 5. 测试策略

### 5.1 层一：判据纯逻辑单测（不发真信号）

沿用 prochost 既有的测试缝模式（`aliveFn`、`killGroupFn`、`killVerifyBackoff` 均为包级 `var`），新增 `enumGroupFn` 作为同款缝。

| 输入 | 期望 Verdict | 期望信号次数 |
|---|---|---|
| 锁已释放 + 组内存在 `pid == pgid` 的活进程 | `leader_reuse` | **0** |
| **锁仍被持有** + 组内存在 `pid == pgid` 的活进程 | `ok`（组长是我们的 shim，正常计数） | — （`Footprint` 用例） |
| 锁仍被持有时调用 `Sweep` | 拒绝执行并返回错误 | **0** |
| `StartedAt == 0` | `no_credential` | **0** |
| 成员启动时刻 < `StartedAt` | `ok`，该成员被排除 | 1（不含它） |
| 正常（锁已释放、无复用者） | `ok` | 恰好 1 次组信号 + 复核 |

**孪生一致性用例**：同一份 stub 下，`Footprint` 与 `Sweep` 必须给出**完全相同的成员集合**。这条钉住 §3.1 的核心不变式——数出来的与被杀的永远是同一批。

### 5.2 层二：真进程测试（有界）

沿用 [platform_test.go](../../../internal/prochost/platform_test.go) 既有手法（`os.Args[0]` 重入测试二进制起真 helper，`sleep 30` 级别自限，用例结束主动回收）。

必须固化的事实：**setsid 之后孙进程确实继承 pgid**（§2.2）。整个规则二建立在它之上，不能只靠一次 `ps` 观察。

### 5.3 层三：变异检验（验收硬条件）

B69 的价值全在「不误杀」，而不误杀是**否定性质**：测试通过不能证明它成立，只能证明没测出反例。变异检验是唯一能证明测试确实在守这条线的手段。

| 变异 | 必须 FAIL 的用例 |
|---|---|
| 删除规则一整段 | `TestSweepAbortsOnLeaderReuse` |
| 规则一去掉锁状态区分（一律判复用） | 锁被持有时的 `Footprint` 正常计数用例 |
| 规则三改为恒真 | 时间下界对应用例 |
| 「凭据不全降级」改为继续执行 | `no_credential` 对应用例 |

恢复后必须全绿，且 `git diff --exit-code` 干净。B47 即以此标准验收（两处变异由审核者独立复现），本 spec 沿用。

### 5.4 层四：真机烟测（本机隔离实例）

隔离方式沿用 B47 先例：独立端口 + 独立 DataDir + 独立编译二进制 + 独立仓库副本 + 自带 config，**不占用 devbox**（其上有他人任务在跑）。

场景：让 executor 拉起一棵带孙进程的树，随后**直接杀掉 executor 本身**（模拟自然死亡，绕开 `done`/`stop`）→ 观察 agentd 是否自动清扫、日志 killed 数是否正确、`handoff footprint` 前后数字是否变化。

**该场景在修复前必然失败**——现状就是不清扫。因此它同时是当前缺陷的复现证据：**先写它、先看它红，再修**。

## 6. 证明强度的边界

「pid 复用误杀」这一场景**无法在测试中真实构造**——无法让内核按需复用一个指定 pid。规则一的正确性依赖于：stub 单测 + 变异检验 + 规则二的会话封闭性论证。

真机烟测能证明「正常路径不误伤」，**证明不了「复用路径挡得住」**。

这是本设计中唯一一处证据等级低一档的地方。判断为可接受（规则二提供理论支撑，规则三提供双保险），但**必须让后来者知道**——而不是让一片全绿看起来像是什么都证明了。B47 的血正是流在这个位置。

## 7. 兼容性

- **既有 `proc.json` 无 `StartedAt`**：读出为 0 → 判定 `no_credential` → 降级为只上报不清扫。老任务不会因升级而被动手。
- **非 unix 平台**：新增平台原语一律 no-op，`Footprint` 返回空集与 `no_credential`，`Sweep` 直接返回 0。沿用 `platform_other.go` 既有模式。
- **`Kill` 契约不变**：既有调用方与 `TestKillSkipsWhenLockFree` 等用例不受影响。

## 8. 验收标准

1. `go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./...` 全绿。
2. `go test -race ./internal/prochost/ ./internal/agentd/` 全绿。
3. §5.3 三处变异检验逐条复现：变异后指定用例 FAIL，恢复后全绿，`git diff --exit-code` 干净。
4. §5.4 真机烟测：修复前该场景失败（残留进程存活），修复后自动清扫成功，日志出现 killed 数且复核确认退出，全程无误杀（隔离实例外的进程 pid 前后一致）。
5. `handoff status` 显示 per-task 进程数与全局 `占用 / 上限`；`handoff footprint` 能列出历史任务的残留计数。
6. 按 `instrumenting-code` 自检：关键节点有日志（清扫进入/结论/killed 数、判定为放弃时的原因）、错误分支带上下文、成功路径不静默；新增文件有文件头职责与边界注释，新增导出方法有参数/返回/注意事项注释。
